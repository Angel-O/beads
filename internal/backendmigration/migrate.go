package backendmigration

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"

	"github.com/steveyegge/beads/internal/atomicfile"
	backendmigrationcontrol "github.com/steveyegge/beads/internal/backendmigration/control"
	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
	beadssqlite "github.com/steveyegge/beads/internal/storage/sqlite"
)

type Result struct {
	Status             string                                    `json:"status"`
	Effect             string                                    `json:"effect,omitempty"`
	SourceBackend      string                                    `json:"source_backend"`
	TargetBackend      string                                    `json:"target_backend"`
	SQLitePath         string                                    `json:"sqlite_path"`
	ApplyRequired      bool                                      `json:"apply_required,omitempty"`
	SourcePreserved    bool                                      `json:"source_preserved"`
	HistoryTransferred bool                                      `json:"history_transferred"`
	Verified           bool                                      `json:"verified,omitempty"`
	CutoverApplied     bool                                      `json:"cutover_applied,omitempty"`
	Recovered          bool                                      `json:"recovered,omitempty"`
	Authority          string                                    `json:"authority,omitempty"`
	RecoveryRequired   bool                                      `json:"recovery_required,omitempty"`
	RowCounts          map[string]int                            `json:"row_counts,omitempty"`
	Portability        storage.BackendMigrationPortabilityReport `json:"portability"`
}

const (
	AuthorityDoltSource = "dolt_source"
	AuthoritySQLite     = "sqlite"
)

type Admission struct {
	SQLitePath       string `json:"sqlite_path"`
	Authority        string `json:"authority"`
	RecoveryRequired bool   `json:"recovery_required"`
}

type sourceAuthority struct {
	metadata []byte
	config   *configfile.Config
	database string
	beadsDir string
	binding  *embeddeddolt.MigrationSourceBinding
}

var backendMigrationCheckpoint func(string)

// Inspect performs the effect-free admission check used by preview and
// confirmation. An empty requestedPath means the default for a fresh attempt
// or the durable target recorded by a pending attempt.
func Inspect(beadsDir, requestedPath string) (Admission, error) {
	abs, err := validateWorkspace(beadsDir)
	if err != nil {
		return Admission{}, err
	}
	if requestedPath != "" {
		requestedPath, err = ValidateSQLiteBasename(requestedPath)
		if err != nil {
			return Admission{}, err
		}
	}
	pending, err := pendingAttempt(abs)
	if err != nil {
		return Admission{}, err
	}
	if !pending {
		if requestedPath == "" {
			requestedPath = "beads.db"
		}
		authority, err := readSourceAuthority(abs)
		if err != nil {
			return Admission{}, err
		}
		if err := authority.binding.Close(); err != nil {
			return Admission{}, err
		}
		if err := beadssqlite.RefuseMigrationTargetCollision(filepath.Join(abs, requestedPath)); err != nil {
			return Admission{}, newSafeError(
				ErrorCodeTargetExists,
				fmt.Sprintf("SQLite target %q is unavailable; choose a new basename with --sqlite-path", requestedPath),
				AuthorityDoltSource,
				"",
			)
		}
		return Admission{SQLitePath: requestedPath, Authority: AuthorityDoltSource}, nil
	}

	state, err := readAttempt(abs)
	if err != nil {
		return Admission{}, err
	}
	if requestedPath != "" && requestedPath != state.SQLitePath {
		return Admission{}, fmt.Errorf("pending backend migration targets %q; rerun with that exact --sqlite-path", state.SQLitePath)
	}
	current, cfg, err := configfile.ReadForBackendMigration(abs)
	if err != nil {
		return Admission{}, errors.New("backend migration authority is unreadable; preserving both stores")
	}
	admission := Admission{SQLitePath: state.SQLitePath, RecoveryRequired: true}
	switch {
	case bytes.Equal(current, state.OriginalMetadata):
		authority, err := readSourceAuthority(abs)
		if err != nil {
			return Admission{}, err
		}
		if err := authority.binding.VerifyWitness(state.SourceIdentity); err != nil {
			_ = authority.binding.Close()
			return Admission{}, err
		}
		if err := authority.binding.Close(); err != nil {
			return Admission{}, err
		}
		admission.Authority = AuthorityDoltSource
	case bytes.Equal(current, state.TargetMetadata):
		if cfg.GetBackend() != configfile.BackendSQLite || cfg.GetSQLitePath() != state.SQLitePath {
			return Admission{}, errors.New("backend migration SQLite authority is inconsistent; preserving both stores")
		}
		originalConfig, err := configfile.ParseForBackendMigration(state.OriginalMetadata)
		if err != nil {
			return Admission{}, err
		}
		binding, err := embeddeddolt.BindMigrationSource(abs, metadataDoltDatabase(originalConfig))
		if err != nil {
			return Admission{}, fmt.Errorf("verify preserved Dolt source: %w", err)
		}
		if err := binding.VerifyWitness(state.SourceIdentity); err != nil {
			_ = binding.Close()
			return Admission{}, err
		}
		if err := binding.Close(); err != nil {
			return Admission{}, err
		}
		admission.Authority = AuthoritySQLite
	default:
		return Admission{}, errors.New("backend migration metadata diverged; preserving both stores for manual recovery")
	}
	return admission, nil
}

func Preview(ctx context.Context, beadsDir, sqlitePath string) (Result, error) {
	_ = ctx // Preview is deliberately static; Apply performs the snapshot.
	admission, err := Inspect(beadsDir, sqlitePath)
	if err != nil {
		return Result{}, err
	}
	result := plannedResult(admission.SQLitePath)
	result.Authority = admission.Authority
	result.RecoveryRequired = admission.RecoveryRequired
	return result, nil
}

func Apply(ctx context.Context, beadsDir, sqlitePath string) (result Result, err error) {
	abs, err := validateWorkspace(beadsDir)
	if err != nil {
		return Result{}, err
	}
	sqlitePath, err = ValidateSQLiteBasename(sqlitePath)
	if err != nil {
		return Result{}, err
	}
	guard, err := backendmigrationcontrol.TryAcquire(abs)
	if err != nil {
		if errors.Is(err, backendmigrationcontrol.ErrBusy) {
			return Result{}, newSafeError(
				ErrorCodeBusy,
				"this workspace is active in another process; wait for it to finish and retry the migration",
				AuthorityUnknown,
				applyCommand(sqlitePath),
			)
		}
		return Result{}, err
	}
	defer func() {
		err = errors.Join(err, guard.Close())
	}()
	if pending, err := pendingAttempt(abs); err != nil {
		return Result{}, err
	} else if pending {
		return recoverAttempt(ctx, abs, sqlitePath)
	}
	return applyFresh(ctx, abs, sqlitePath)
}

func applyFresh(ctx context.Context, beadsDir, sqlitePath string) (result Result, err error) {
	authority, err := readSourceAuthority(beadsDir)
	if err != nil {
		return Result{}, err
	}
	bindingOpen := true
	defer func() {
		if bindingOpen {
			err = errors.Join(err, authority.binding.Close())
		}
	}()
	if err := beadssqlite.RefuseMigrationTargetCollision(filepath.Join(beadsDir, sqlitePath)); err != nil {
		return Result{}, newSafeError(
			ErrorCodeTargetExists,
			fmt.Sprintf("SQLite target %q is unavailable; choose a new basename with --sqlite-path", sqlitePath),
			AuthorityDoltSource,
			"",
		)
	}
	source, snapshot, report, err := snapshotSource(ctx, authority)
	if err != nil {
		if source != nil {
			_ = source.Close()
		}
		return Result{}, err
	}
	sourceOpen := true
	defer func() {
		if sourceOpen {
			err = errors.Join(err, source.Close())
		}
	}()
	if err := requireMetadata(beadsDir, authority.metadata); err != nil {
		return Result{}, err
	}

	digest, err := snapshot.Digest()
	if err != nil {
		return Result{}, err
	}
	sourceIdentity, err := authority.binding.Witness()
	if err != nil {
		return Result{}, err
	}
	targetConfig := *authority.config
	targetConfig.Backend = configfile.BackendSQLite
	targetConfig.SQLitePath = sqlitePath
	targetMetadata, err := configfile.MarshalForBackendMigration(&targetConfig)
	if err != nil {
		return Result{}, err
	}
	attemptID, err := newAttemptID()
	if err != nil {
		return Result{}, err
	}
	workspaceIdentity, err := createAttemptWorkspace(beadsDir, attemptID)
	if err != nil {
		return Result{}, err
	}
	state := &attemptState{
		Version: attemptSchemaVersion, AttemptID: attemptID, Phase: phasePrepared,
		SQLitePath: sqlitePath, SourceIdentity: sourceIdentity, WorkspaceIdentity: workspaceIdentity,
		SnapshotDigest: digest, RowCounts: snapshot.RowCounts(),
		OriginalMetadata: authority.metadata, TargetMetadata: targetMetadata,
	}
	if err := createAttempt(beadsDir, state); err != nil {
		return Result{}, errors.Join(err, removeAttemptWorkspace(beadsDir, attemptID, workspaceIdentity))
	}

	rollback := func(cause error) error {
		if identityErr := authority.binding.VerifyWitness(state.SourceIdentity); identityErr != nil {
			return errors.Join(cause, errors.New("Dolt source identity changed; preserving recovery state and both stores"), identityErr)
		}
		if metadataErr := requireMetadata(beadsDir, authority.metadata); metadataErr != nil {
			return errors.Join(cause, errors.New("backend authority changed; preserving both stores"), metadataErr)
		}
		remove, cleanupErr := cleanupBeforeAttemptRemoval(ctx, beadsDir, state,
			filepath.Join(beadsDir, stagingBasename(state.AttemptID))+".creating",
			filepath.Join(beadsDir, stagingBasename(state.AttemptID)),
			filepath.Join(beadsDir, state.SQLitePath),
		)
		if cleanupErr != nil {
			return errors.Join(cause, cleanupErr)
		}
		return errors.Join(cause, remove())
	}

	stagingPath := attemptWorkspaceTargetPath(beadsDir, state.AttemptID)
	target, err := beadssqlite.CreateFreshForMigration(ctx, stagingPath, state.AttemptID, backendMigrationCheckpoint)
	if err != nil {
		return Result{}, rollback(err)
	}
	if err := target.RestoreBackendMigration(ctx, snapshot); err != nil {
		_ = target.Close()
		return Result{}, rollback(err)
	}
	if err := target.VerifyBackendMigration(ctx, snapshot); err != nil {
		_ = target.Close()
		return Result{}, rollback(err)
	}
	if err := target.Close(); err != nil {
		return Result{}, rollback(err)
	}
	if err := updateAttemptPhase(beadsDir, state, phaseTargetReady); err != nil {
		return Result{}, rollback(err)
	}
	if err := authority.binding.Verify(); err != nil {
		return Result{}, rollback(err)
	}
	if err := target.Promote(filepath.Join(beadsDir, sqlitePath)); err != nil {
		return Result{}, rollback(err)
	}

	finalTarget, err := beadssqlite.OpenMigrationTarget(ctx, filepath.Join(beadsDir, sqlitePath), state.AttemptID)
	if err != nil {
		return Result{}, rollback(err)
	}
	if err := finalTarget.VerifyBackendMigrationDigest(ctx, state.SnapshotDigest, state.RowCounts); err != nil {
		_ = finalTarget.Close()
		return Result{}, rollback(err)
	}
	if err := finalTarget.Close(); err != nil {
		return Result{}, rollback(err)
	}

	if err := authority.binding.Verify(); err != nil {
		return Result{}, rollback(err)
	}
	secondSnapshot, secondReport, err := source.(storage.BackendMigrationSource).SnapshotBackendMigration(ctx)
	if err != nil || !secondReport.Portable() {
		return Result{}, rollback(errors.Join(err, errors.New("Dolt source changed portability before cutover")))
	}
	if err := authority.binding.Verify(); err != nil {
		return Result{}, rollback(err)
	}
	secondDigest, digestErr := secondSnapshot.Digest()
	if digestErr != nil || secondDigest != state.SnapshotDigest || !reflect.DeepEqual(secondSnapshot.RowCounts(), state.RowCounts) {
		return Result{}, rollback(errors.Join(digestErr, errors.New("Dolt source changed before cutover")))
	}
	if err := requireMetadata(beadsDir, authority.metadata); err != nil {
		return Result{}, rollback(err)
	}
	if err := updateAttemptPhase(beadsDir, state, phaseCutover); err != nil {
		return Result{}, rollback(err)
	}
	if err := authority.binding.Verify(); err != nil {
		return Result{}, rollback(err)
	}
	if err := atomicfile.WriteFileDurable(configfile.ConfigPath(beadsDir), state.TargetMetadata, 0o600); err != nil {
		current, _, readErr := configfile.ReadForBackendMigration(beadsDir)
		if readErr != nil || !bytes.Equal(current, state.TargetMetadata) {
			return Result{}, rollback(errors.Join(err, readErr))
		}
	}
	if err := source.Close(); err != nil {
		return Result{}, fmt.Errorf("release preserved Dolt source after cutover: %w", err)
	}
	sourceOpen = false
	if err := authority.binding.VerifyWitness(state.SourceIdentity); err != nil {
		return Result{}, err
	}
	if err := authority.binding.Close(); err != nil {
		return Result{}, fmt.Errorf("release preserved Dolt source identity after cutover: %w", err)
	}
	bindingOpen = false

	result, err = finishSQLiteAuthority(ctx, beadsDir, state, report, false)
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

func recoverAttempt(ctx context.Context, beadsDir, requestedPath string) (Result, error) {
	state, err := readAttempt(beadsDir)
	if err != nil {
		return Result{}, err
	}
	if requestedPath != state.SQLitePath {
		return Result{}, fmt.Errorf("pending backend migration targets %q; rerun with that exact --sqlite-path", state.SQLitePath)
	}
	current, cfg, err := configfile.ReadForBackendMigration(beadsDir)
	if err != nil {
		return Result{}, errors.New("backend migration authority is unreadable; preserving both stores")
	}
	switch {
	case bytes.Equal(current, state.TargetMetadata):
		if cfg.GetBackend() != configfile.BackendSQLite || cfg.GetSQLitePath() != state.SQLitePath {
			return Result{}, errors.New("backend migration SQLite authority is inconsistent; preserving both stores")
		}
		return finishSQLiteAuthority(ctx, beadsDir, state, storage.BackendMigrationPortabilityReport{}, true)
	case bytes.Equal(current, state.OriginalMetadata):
		authority, sourceErr := readSourceAuthority(beadsDir)
		if sourceErr != nil {
			return Result{}, sourceErr
		}
		defer authority.binding.Close() //nolint:errcheck // primary recovery result wins
		source, _, _, sourceErr := snapshotSource(ctx, authority)
		if sourceErr != nil {
			if source != nil {
				_ = source.Close()
			}
			return Result{}, sourceErr
		}
		if identityErr := authority.binding.VerifyWitness(state.SourceIdentity); identityErr != nil {
			_ = source.Close()
			return Result{}, identityErr
		}
		remove, cleanupErr := cleanupBeforeAttemptRemoval(ctx, beadsDir, state,
			filepath.Join(beadsDir, stagingBasename(state.AttemptID))+".creating",
			filepath.Join(beadsDir, stagingBasename(state.AttemptID)),
			filepath.Join(beadsDir, state.SQLitePath),
		)
		if cleanupErr != nil {
			return Result{}, errors.Join(cleanupErr, source.Close())
		}
		if closeErr := source.Close(); closeErr != nil {
			return Result{}, closeErr
		}
		if err := requireMetadata(beadsDir, state.OriginalMetadata); err != nil {
			return Result{}, err
		}
		if err := authority.binding.Verify(); err != nil {
			return Result{}, err
		}
		if err := authority.binding.Close(); err != nil {
			return Result{}, err
		}
		if removeErr := remove(); removeErr != nil {
			return Result{}, removeErr
		}
		result, err := applyFresh(ctx, beadsDir, requestedPath)
		result.Recovered = err == nil
		return result, err
	default:
		return Result{}, errors.New("backend migration metadata diverged; preserving both stores for manual recovery")
	}
}

func finishSQLiteAuthority(ctx context.Context, beadsDir string, state *attemptState, report storage.BackendMigrationPortabilityReport, recovered bool) (Result, error) {
	if err := requireMetadata(beadsDir, state.TargetMetadata); err != nil {
		return Result{}, errors.New("backend migration SQLite authority changed; preserving both stores")
	}
	originalConfig, err := configfile.ParseForBackendMigration(state.OriginalMetadata)
	if err != nil {
		return Result{}, fmt.Errorf("verify preserved Dolt source metadata: %w", err)
	}
	sourceBinding, err := embeddeddolt.BindMigrationSource(beadsDir, metadataDoltDatabase(originalConfig))
	if err != nil {
		return Result{}, fmt.Errorf("verify preserved Dolt source: %w", err)
	}
	bindingOpen := true
	defer func() {
		if bindingOpen {
			_ = sourceBinding.Close()
		}
	}()
	if err := sourceBinding.VerifyWitness(state.SourceIdentity); err != nil {
		return Result{}, err
	}
	target, err := beadssqlite.OpenMigrationTarget(ctx, filepath.Join(beadsDir, state.SQLitePath), state.AttemptID)
	if err != nil {
		return Result{}, err
	}
	adopted, err := target.IsAdopted(ctx)
	if err == nil && adopted {
		// Adoption is the irreversible authority handoff. An older bd binary or
		// direct SQLite client may have made legitimate post-cutover writes before
		// recovery removed the gate, so reapplying the original digest here would
		// reject authoritative current state. Verify the adopted database itself.
		err = target.VerifyIntegrity(ctx)
	} else if err == nil {
		err = target.VerifyBackendMigrationDigest(ctx, state.SnapshotDigest, state.RowCounts)
		if err == nil {
			err = requireMetadata(beadsDir, state.TargetMetadata)
		}
		if err == nil {
			err = target.MarkAdopted(ctx)
		}
	}
	closeErr := target.Close()
	if err := errors.Join(err, closeErr); err != nil {
		return Result{}, err
	}
	remove, err := cleanupBeforeAttemptRemoval(ctx, beadsDir, state,
		filepath.Join(beadsDir, stagingBasename(state.AttemptID))+".creating",
		filepath.Join(beadsDir, stagingBasename(state.AttemptID)),
	)
	if err != nil {
		return Result{}, err
	}
	if err := requireMetadata(beadsDir, state.TargetMetadata); err != nil {
		return Result{}, errors.New("backend migration SQLite authority changed; preserving recovery state")
	}
	if err := sourceBinding.VerifyWitness(state.SourceIdentity); err != nil {
		return Result{}, err
	}
	if err := sourceBinding.Close(); err != nil {
		return Result{}, fmt.Errorf("release preserved Dolt source identity before completing migration: %w", err)
	}
	bindingOpen = false
	if err := remove(); err != nil {
		return Result{}, err
	}
	return Result{
		Status: "migrated", SourceBackend: configfile.BackendDolt, TargetBackend: configfile.BackendSQLite,
		SQLitePath: state.SQLitePath, SourcePreserved: true, HistoryTransferred: false,
		Verified: true, CutoverApplied: true, Recovered: recovered, RowCounts: state.RowCounts, Portability: report,
	}, nil
}

func plannedResult(sqlitePath string) Result {
	return Result{
		Status: "planned", Effect: "none", SourceBackend: configfile.BackendDolt,
		TargetBackend: configfile.BackendSQLite, SQLitePath: sqlitePath, ApplyRequired: true,
		SourcePreserved: true, HistoryTransferred: false,
	}
}

func validateWorkspace(beadsDir string) (string, error) {
	if beadsDir == "" {
		return "", newSafeError(ErrorCodeUnsafeWorkspace, "no beads workspace found", AuthorityUnknown, "")
	}
	abs, err := filepath.Abs(beadsDir)
	if err != nil {
		return "", newSafeError(ErrorCodeUnsafeWorkspace, "beads workspace path is invalid", AuthorityUnknown, "")
	}
	info, err := os.Lstat(abs)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", newSafeError(ErrorCodeUnsafeWorkspace, "beads workspace must be a real directory", AuthorityUnknown, "")
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return "", newSafeError(ErrorCodeUnsupported, "backend migration is currently supported on Linux and macOS only", AuthorityUnknown, "")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o022 != 0 {
		return "", newSafeError(ErrorCodeUnsafeWorkspace, "beads workspace must not be group- or world-writable", AuthorityUnknown, "")
	}
	return filepath.Clean(abs), nil
}

func readSourceAuthority(beadsDir string) (*sourceAuthority, error) {
	metadata, cfg, err := configfile.ReadForBackendMigration(beadsDir)
	if err != nil {
		if hasLegacyOnlyMetadata(beadsDir) {
			return nil, newSafeError(
				ErrorCodeUnsupported,
				"workspace metadata must be upgraded first; run bd doctor, then preview the backend migration again",
				AuthorityUnknown,
				"",
			)
		}
		return nil, err
	}
	if cfg != nil && cfg.GetBackend() == configfile.BackendSQLite {
		return nil, newSafeError(ErrorCodeUnsupported, "workspace already uses SQLite; no backend migration is needed", AuthoritySQLite, "")
	}
	if cfg == nil || cfg.GetBackend() != configfile.BackendDolt {
		return nil, newSafeError(ErrorCodeUnsupported, "backend migration source must be Dolt", AuthorityUnknown, "")
	}
	serverSelected, err := effectiveDoltServerSelection(beadsDir, cfg)
	if err != nil {
		return nil, err
	}
	if serverSelected {
		return nil, newSafeError(ErrorCodeUnsupported, "this slice supports embedded Dolt sources only; server-backed Dolt is selected", AuthorityDoltSource, "")
	}
	mode := strings.ToLower(cfg.DoltMode)
	if mode == "" {
		mode = configfile.DoltModeEmbedded
	}
	if mode != configfile.DoltModeEmbedded {
		return nil, newSafeError(ErrorCodeUnsupported, "this slice supports embedded Dolt sources only", AuthorityDoltSource, "")
	}
	if cfg.DoltDataDir != "" {
		return nil, newSafeError(ErrorCodeUnsupported, "custom Dolt data directories are not supported by this migration slice", AuthorityDoltSource, "")
	}
	database := metadataDoltDatabase(cfg)
	binding, err := embeddeddolt.BindMigrationSource(beadsDir, database)
	if err != nil {
		return nil, err
	}
	return &sourceAuthority{metadata: metadata, config: cfg, database: database, beadsDir: beadsDir, binding: binding}, nil
}

func hasLegacyOnlyMetadata(beadsDir string) bool {
	if _, err := os.Lstat(configfile.ConfigPath(beadsDir)); !errors.Is(err, os.ErrNotExist) {
		return false
	}
	info, err := os.Lstat(filepath.Join(beadsDir, "config.json"))
	return err == nil && info.Mode().IsRegular()
}

func effectiveDoltServerSelection(beadsDir string, cfg *configfile.Config) (bool, error) {
	if cfg == nil {
		return false, nil
	}
	// The CLI initializes config against the physically selected workspace, so
	// these checks include project, local, user, and legacy-global config layers.
	if cfg.IsDoltServerMode() || config.GetBool("dolt.shared-server") {
		return true, nil
	}
	// Apply holds workspace control while this strict read runs. Project config
	// writers take the same control for backend selectors, closing the gap between
	// CLI admission and cutover; config.local.yaml is included as an override.
	workspaceMode, workspaceShared, err := config.ReadWorkspaceDoltSelection(beadsDir)
	if err != nil {
		return false, newSafeError(
			ErrorCodeUnsafeWorkspace,
			"workspace configuration could not be read safely; run bd doctor before retrying",
			AuthorityDoltSource,
			"",
		)
	}
	if cfg.DoltMode == "" && strings.EqualFold(workspaceMode, configfile.DoltModeServer) {
		return true, nil
	}
	return workspaceShared, nil
}

func metadataDoltDatabase(cfg *configfile.Config) string {
	if cfg != nil && cfg.DoltDatabase != "" {
		return cfg.DoltDatabase
	}
	return configfile.DefaultDoltDatabase
}

func snapshotSource(ctx context.Context, authority *sourceAuthority) (storage.DoltStorage, storage.BackendMigrationSnapshot, storage.BackendMigrationPortabilityReport, error) {
	source, err := authority.binding.OpenReadOnly(ctx, authority.database, "main")
	if err != nil {
		return nil, storage.BackendMigrationSnapshot{}, storage.BackendMigrationPortabilityReport{}, err
	}
	if err := requireMetadata(authority.beadsDir, authority.metadata); err != nil {
		return source, storage.BackendMigrationSnapshot{}, storage.BackendMigrationPortabilityReport{}, err
	}
	migrationSource, ok := source.(storage.BackendMigrationSource)
	if !ok {
		return source, storage.BackendMigrationSnapshot{}, storage.BackendMigrationPortabilityReport{}, errors.New("embedded Dolt source lacks migration support")
	}
	snapshot, report, err := migrationSource.SnapshotBackendMigration(ctx)
	if err != nil {
		return source, storage.BackendMigrationSnapshot{}, report, err
	}
	if err := authority.binding.Verify(); err != nil {
		return source, storage.BackendMigrationSnapshot{}, report, err
	}
	if !report.Portable() {
		return source, storage.BackendMigrationSnapshot{}, report, fmt.Errorf("embedded Dolt source has unrepresentable current state: %+v", report)
	}
	return source, snapshot, report, nil
}

func requireMetadata(beadsDir string, expected []byte) error {
	current, _, err := configfile.ReadForBackendMigration(beadsDir)
	if err != nil || !bytes.Equal(current, expected) {
		return errors.Join(errors.New("workspace metadata changed during backend migration"), err)
	}
	return nil
}

func cleanupOwnedPath(ctx context.Context, path, attemptID string) error {
	return beadssqlite.RemoveOwnedMigrationTarget(ctx, path, attemptID)
}

// cleanupBeforeAttemptRemoval makes marker removal unavailable until every
// named migration artifact has either been verified-owned and removed or was
// already absent. Callers may perform final authority/source checks before
// invoking the returned removal function.
func cleanupBeforeAttemptRemoval(ctx context.Context, beadsDir string, state *attemptState, paths ...string) (func() error, error) {
	cleanupErr := removeAttemptWorkspace(beadsDir, state.AttemptID, state.WorkspaceIdentity)
	for _, path := range paths {
		cleanupErr = errors.Join(cleanupErr, cleanupOwnedPath(ctx, path, state.AttemptID))
	}
	if cleanupErr != nil {
		return nil, cleanupErr
	}
	return func() error {
		return removeAttempt(beadsDir, state)
	}, nil
}

func newAttemptID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}
