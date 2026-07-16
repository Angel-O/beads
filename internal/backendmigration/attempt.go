package backendmigration

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/steveyegge/beads/internal/atomicfile"
	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/noreplace"
	"github.com/steveyegge/beads/internal/safefile"
)

const (
	attemptSchemaVersion = 1
	maxAttemptBytes      = 2 << 20
	phasePrepared        = "prepared"
	phaseTargetReady     = "target_ready"
	phaseCutover         = "cutover"
	attemptWorkspaceFile = "target.db"
)

var (
	attemptIDPattern         = regexp.MustCompile(`^[0-9a-f]{32}$`)
	sourceIdentityPattern    = regexp.MustCompile(`^v2:[^/]{1,200}/[^/]{1,200}/[^/]{1,200}$`)
	workspaceIdentityPattern = regexp.MustCompile(`^[0-9a-f]+:[0-9a-f]+$`)
	sqliteBasenamePattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,126}\.db$`)
)

type attemptState struct {
	Version           int            `json:"version"`
	AttemptID         string         `json:"attempt_id"`
	Phase             string         `json:"phase"`
	SQLitePath        string         `json:"sqlite_path"`
	SourceIdentity    string         `json:"source_identity"`
	WorkspaceIdentity string         `json:"workspace_identity"`
	SnapshotDigest    string         `json:"snapshot_digest"`
	RowCounts         map[string]int `json:"row_counts"`
	OriginalMetadata  []byte         `json:"original_metadata"`
	TargetMetadata    []byte         `json:"target_metadata"`
}

func ValidateSQLiteBasename(value string) (string, error) {
	if value == "" {
		value = "beads.db"
	}
	if filepath.Base(value) != value || filepath.Clean(value) != value || !sqliteBasenamePattern.MatchString(value) {
		return "", safeInvalidRequest("SQLite migration path must be a lowercase workspace-local basename ending in .db")
	}
	return value, nil
}

func attemptPath(beadsDir string) string {
	return filepath.Join(beadsDir, configfile.BackendMigrationStateFileName)
}

func attemptCleanupPath(beadsDir, attemptID string) string {
	return filepath.Join(beadsDir, ".backend-migration-"+attemptID+".cleanup.lock")
}

func stagingBasename(attemptID string) string {
	return ".backend-migration-" + attemptID + ".db"
}

func attemptWorkspacePath(beadsDir, attemptID string) string {
	return filepath.Join(beadsDir, ".backend-migration-"+attemptID+".work")
}

func attemptWorkspaceCleanupPath(beadsDir, attemptID string) string {
	return filepath.Join(beadsDir, ".backend-migration-"+attemptID+".work.cleanup")
}

func attemptWorkspaceTargetPath(beadsDir, attemptID string) string {
	return filepath.Join(attemptWorkspacePath(beadsDir, attemptID), attemptWorkspaceFile)
}

func pendingAttempt(beadsDir string) (bool, error) {
	_, err := os.Lstat(attemptPath(beadsDir))
	switch {
	case err == nil:
		return true, nil
	case !errors.Is(err, os.ErrNotExist):
		return false, fmt.Errorf("inspect backend migration state: %w", err)
	}
	matches, err := configfile.BackendMigrationCleanupMarkers(beadsDir)
	if err != nil {
		return false, fmt.Errorf("inspect backend migration cleanup state: %w", err)
	}
	return len(matches) != 0, nil
}

func attemptStatePath(beadsDir string) (string, error) {
	paths := make([]string, 0, 2)
	if _, err := os.Lstat(attemptPath(beadsDir)); err == nil {
		paths = append(paths, attemptPath(beadsDir))
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect backend migration state: %w", err)
	}
	matches, err := configfile.BackendMigrationCleanupMarkers(beadsDir)
	if err != nil {
		return "", fmt.Errorf("inspect backend migration cleanup state: %w", err)
	}
	paths = append(paths, matches...)
	if len(paths) != 1 {
		return "", fmt.Errorf("backend migration recovery state is ambiguous: found %d markers", len(paths))
	}
	return paths[0], nil
}

func readAttempt(beadsDir string) (*attemptState, error) {
	statePath, err := attemptStatePath(beadsDir)
	if err != nil {
		return nil, err
	}
	data, err := safefile.ReadRegularFile(statePath, maxAttemptBytes)
	if err != nil {
		return nil, fmt.Errorf("read backend migration state: %w", err)
	}
	state, err := decodeAttempt(data)
	if err != nil {
		return nil, err
	}
	if statePath != attemptPath(beadsDir) && statePath != attemptCleanupPath(beadsDir, state.AttemptID) {
		return nil, errors.New("backend migration cleanup marker does not match its durable attempt")
	}
	return state, nil
}

func decodeAttempt(data []byte) (*attemptState, error) {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var state attemptState
	if err := decoder.Decode(&state); err != nil {
		return nil, fmt.Errorf("decode backend migration state: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, err
	}
	if err := validateAttempt(&state); err != nil {
		return nil, err
	}
	return &state, nil
}

func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := scanStrictJSONValue(decoder); err != nil {
		return fmt.Errorf("decode backend migration state: %w", err)
	}
	return requireJSONEOF(decoder)
}

func scanStrictJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("object key is not a string")
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate object key %q", key)
			}
			seen[key] = struct{}{}
			if err := scanStrictJSONValue(decoder); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := scanStrictJSONValue(decoder); err != nil {
				return err
			}
		}
	default:
		return errors.New("invalid JSON delimiter")
	}
	closing, err := decoder.Token()
	if err != nil || closing != matchingJSONDelimiter(delimiter) {
		return errors.New("invalid JSON container")
	}
	return nil
}

func matchingJSONDelimiter(open json.Delim) json.Delim {
	if open == '{' {
		return '}'
	}
	return ']'
}

func requireJSONEOF(decoder *json.Decoder) error {
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("backend migration state contains trailing JSON")
		}
		return fmt.Errorf("decode backend migration state: %w", err)
	}
	return nil
}

func validateAttempt(state *attemptState) error {
	if state == nil || state.Version != attemptSchemaVersion || !attemptIDPattern.MatchString(state.AttemptID) {
		return errors.New("backend migration state identity is invalid")
	}
	if state.Phase != phasePrepared && state.Phase != phaseTargetReady && state.Phase != phaseCutover {
		return errors.New("backend migration state phase is invalid")
	}
	validatedPath, err := ValidateSQLiteBasename(state.SQLitePath)
	if err != nil || validatedPath != state.SQLitePath {
		return errors.New("backend migration state target is invalid")
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(state.SnapshotDigest) || len(state.RowCounts) == 0 {
		return errors.New("backend migration state snapshot is invalid")
	}
	if !sourceIdentityPattern.MatchString(state.SourceIdentity) {
		return errors.New("backend migration state source identity is invalid")
	}
	if !workspaceIdentityPattern.MatchString(state.WorkspaceIdentity) {
		return errors.New("backend migration state workspace identity is invalid")
	}
	for table, count := range state.RowCounts {
		if table == "" || count < 0 {
			return errors.New("backend migration state row counts are invalid")
		}
	}
	if len(state.OriginalMetadata) == 0 || len(state.TargetMetadata) == 0 {
		return errors.New("backend migration state metadata witnesses are missing")
	}
	original, err := configfile.ParseForBackendMigration(state.OriginalMetadata)
	if err != nil || original.GetBackend() != configfile.BackendDolt || !strings.EqualFold(original.GetDoltMode(), configfile.DoltModeEmbedded) || original.DoltDataDir != "" {
		return errors.New("backend migration state source metadata is invalid")
	}
	expectedTarget := *original
	expectedTarget.Backend = configfile.BackendSQLite
	expectedTarget.SQLitePath = state.SQLitePath
	expectedTargetBytes, err := configfile.MarshalForBackendMigration(&expectedTarget)
	if err != nil || !bytes.Equal(expectedTargetBytes, state.TargetMetadata) {
		return errors.New("backend migration state target metadata is not derived from its source witness")
	}
	return nil
}

func marshalAttempt(state *attemptState) ([]byte, error) {
	if err := validateAttempt(state); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return nil, err
	}
	if len(data) > maxAttemptBytes {
		return nil, errors.New("backend migration state exceeds size limit")
	}
	return data, nil
}

func createAttempt(beadsDir string, state *attemptState) error {
	if pending, err := pendingAttempt(beadsDir); err != nil {
		return err
	} else if pending {
		return errors.New("backend migration recovery is already pending")
	}
	data, err := marshalAttempt(state)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(beadsDir, ".backend-migration-state-*.lock")
	if err != nil {
		return fmt.Errorf("create backend migration state temporary file: %w", err)
	}
	tempPath := file.Name()
	defer func() {
		_ = file.Close()
		_ = os.Remove(tempPath)
	}()
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("write backend migration state: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync backend migration state: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close backend migration state: %w", err)
	}
	if err := noreplace.Rename(tempPath, attemptPath(beadsDir)); err != nil {
		return fmt.Errorf("publish backend migration state without replacement: %w", err)
	}
	published, err := safefile.ReadRegularFile(attemptPath(beadsDir), maxAttemptBytes)
	if err != nil || !bytes.Equal(published, data) {
		return errors.Join(errors.New("published backend migration state differs from prepared state"), err)
	}
	if err := atomicfile.SyncDirectory(beadsDir); err != nil {
		return fmt.Errorf("backend migration state created: %w: %v", atomicfile.ErrApplied, err)
	}
	if matches, markerErr := configfile.BackendMigrationCleanupMarkers(beadsDir); markerErr != nil || len(matches) != 0 {
		return errors.Join(errors.New("backend migration state published while prior cleanup state exists"), markerErr)
	}
	return nil
}

func updateAttemptPhase(beadsDir string, state *attemptState, phase string) error {
	current, err := readAttempt(beadsDir)
	if err != nil || !attemptStatesEqual(current, state) {
		return errors.New("backend migration state changed unexpectedly")
	}
	state.Phase = phase
	data, err := marshalAttempt(state)
	if err != nil {
		return err
	}
	return atomicfile.WriteFileDurable(attemptPath(beadsDir), data, 0o600)
}

func removeAttempt(beadsDir string, state *attemptState) error {
	return quarantineAndRemoveAttempt(beadsDir, state, nil)
}

func attemptStatesEqual(left, right *attemptState) bool {
	leftBytes, leftErr := marshalAttempt(left)
	rightBytes, rightErr := marshalAttempt(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftBytes, rightBytes)
}

func quarantineAndRemoveAttempt(beadsDir string, state *attemptState, beforeRename func()) error {
	expected, err := marshalAttempt(state)
	if err != nil {
		return err
	}
	canonical := attemptPath(beadsDir)
	quarantine := attemptCleanupPath(beadsDir, state.AttemptID)
	canonicalInfo, canonicalErr := os.Lstat(canonical)
	quarantineInfo, quarantineErr := os.Lstat(quarantine)
	canonicalMissing := errors.Is(canonicalErr, os.ErrNotExist)
	quarantineMissing := errors.Is(quarantineErr, os.ErrNotExist)
	if canonicalErr != nil && !canonicalMissing {
		return fmt.Errorf("inspect backend migration marker before removal: %w", canonicalErr)
	}
	if quarantineErr != nil && !quarantineMissing {
		return fmt.Errorf("inspect backend migration marker quarantine: %w", quarantineErr)
	}
	if !canonicalMissing && !quarantineMissing {
		return errors.New("backend migration marker and cleanup quarantine both exist; preserving both")
	}
	if canonicalMissing && quarantineMissing {
		return errors.New("backend migration marker disappeared before verified removal")
	}
	if !canonicalMissing && (!canonicalInfo.Mode().IsRegular() || canonicalInfo.Mode()&os.ModeSymlink != 0) {
		return errors.New("backend migration marker is not a safe regular file")
	}
	if !quarantineMissing && (!quarantineInfo.Mode().IsRegular() || quarantineInfo.Mode()&os.ModeSymlink != 0) {
		return errors.New("backend migration marker quarantine is not a safe regular file")
	}
	if quarantineMissing {
		if beforeRename != nil {
			beforeRename()
		}
		if err := noreplace.Rename(canonical, quarantine); err != nil {
			return fmt.Errorf("quarantine backend migration marker: %w", err)
		}
		if err := atomicfile.SyncDirectory(beadsDir); err != nil {
			return fmt.Errorf("backend migration marker quarantined: %w: %v", atomicfile.ErrApplied, err)
		}
	}
	actual, err := safefile.ReadRegularFile(quarantine, maxAttemptBytes)
	if err != nil || !bytes.Equal(actual, expected) {
		changedErr := errors.Join(errors.New("backend migration marker changed before removal; preserving it"), err)
		if restoreErr := noreplace.Rename(quarantine, canonical); restoreErr != nil {
			return errors.Join(changedErr, errors.New("changed marker remains quarantined"), restoreErr)
		}
		if syncErr := atomicfile.SyncDirectory(beadsDir); syncErr != nil {
			return fmt.Errorf("backend migration marker restored after change: %w: %v", atomicfile.ErrApplied, syncErr)
		}
		return changedErr
	}
	if err := atomicfile.RemoveDurable(quarantine); err != nil && !errors.Is(err, atomicfile.ErrApplied) {
		return err
	}
	return nil
}
