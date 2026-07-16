package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sync/atomic"

	"github.com/steveyegge/beads/internal/atomicfile"
	"github.com/steveyegge/beads/internal/noreplace"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/issueops"
)

// MigrationAttemptMetadataKey proves that a staging/final database belongs to
// one durable migration attempt. It is provider-owned and excluded from the
// portable snapshot.
const MigrationAttemptMetadataKey = "_beads_sqlite_migration_attempt"

const (
	MigrationStateMetadataKey = "_beads_sqlite_migration_state"
	migrationStateStaging     = "staging"
	migrationStateAdopted     = "adopted"
)

var migrationAttemptIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

type MigrationTarget struct {
	store      *Store
	path       string
	attemptID  string
	identity   os.FileInfo
	checkpoint func(string)
	closed     atomic.Bool
}

var _ storage.BackendMigrationTarget = (*MigrationTarget)(nil)

func migrationReservedMetadata(attemptID string) map[string]string {
	return map[string]string{
		schemaVersionKey:            schemaVersion,
		MigrationAttemptMetadataKey: attemptID,
		MigrationStateMetadataKey:   migrationStateStaging,
	}
}

// CreateFreshForMigration exclusively creates a single-file DELETE-journal
// staging database. The caller must create its durable attempt marker first.
func CreateFreshForMigration(ctx context.Context, path, attemptID string, checkpoints ...func(string)) (target *MigrationTarget, err error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("sqlite migration staging path must be absolute and clean")
	}
	if !migrationAttemptIDPattern.MatchString(attemptID) {
		return nil, errors.New("sqlite migration attempt ID is invalid")
	}
	if err := RefuseMigrationTargetCollision(path); err != nil {
		return nil, err
	}
	creatingPath := path + ".creating"
	if err := RefuseMigrationTargetCollision(creatingPath); err != nil {
		return nil, fmt.Errorf("sqlite migration creation workspace is unavailable: %w", err)
	}
	file, err := os.OpenFile(creatingPath, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- validated attempt-owned staging path.
	if err != nil {
		return nil, fmt.Errorf("sqlite migration create staging target: %w", err)
	}
	identity, statErr := file.Stat()
	ownedPath := creatingPath
	if identity != nil {
		defer func() {
			if err != nil {
				err = errors.Join(err, removeMigrationFileWithIdentity(ownedPath, identity))
			}
		}()
	}
	closeErr := file.Close()
	if statErr != nil || closeErr != nil {
		return nil, errors.Join(statErr, closeErr)
	}
	var checkpoint func(string)
	if len(checkpoints) > 0 {
		checkpoint = checkpoints[0]
	}
	if checkpoint != nil {
		checkpoint("created_unstamped")
	}

	d := filesystemDSN(creatingPath, "rw", "DELETE")
	raw, err := sql.Open("sqlite", d)
	if err != nil {
		return nil, fmt.Errorf("sqlite migration open staging target: %w", err)
	}
	if err := initSchema(ctx, raw, false); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("sqlite migration initialize staging target: %w", err)
	}
	if _, err := raw.ExecContext(ctx, "INSERT INTO metadata (`key`, `value`) VALUES (?, ?)", MigrationAttemptMetadataKey, attemptID); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("sqlite migration stamp staging target: %w", err)
	}
	if _, err := raw.ExecContext(ctx, "INSERT INTO metadata (`key`, `value`) VALUES (?, ?)", MigrationStateMetadataKey, migrationStateStaging); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("sqlite migration stamp staging state: %w", err)
	}
	if err := raw.Close(); err != nil {
		return nil, fmt.Errorf("sqlite migration close staging initializer: %w", err)
	}
	initialized, err := os.Open(creatingPath) // #nosec G304 -- retained identity is checked before publication.
	if err != nil {
		return nil, fmt.Errorf("sqlite migration reopen initialized staging target: %w", err)
	}
	opened, statErr := initialized.Stat()
	syncErr := initialized.Sync()
	closeErr = initialized.Close()
	after, err := os.Lstat(creatingPath)
	if statErr != nil || syncErr != nil || closeErr != nil || err != nil || !regularSingleLink(after) || !os.SameFile(identity, opened) || !os.SameFile(opened, after) {
		return nil, errors.Join(errors.New("sqlite migration staging target changed during initialization"), statErr, syncErr, closeErr, err)
	}
	if err := refuseSQLiteSidecars(creatingPath); err != nil {
		return nil, err
	}
	if err := noreplace.Rename(creatingPath, path); err != nil {
		return nil, fmt.Errorf("sqlite migration publish initialized staging target: %w", err)
	}
	ownedPath = path
	if err := syncParentDirectory(filepath.Dir(path)); err != nil {
		return nil, fmt.Errorf("sqlite migration staging target published: %w: %v", atomicfile.ErrApplied, err)
	}
	after, err = os.Lstat(path)
	if err != nil || !regularSingleLink(after) || !os.SameFile(identity, after) {
		return nil, errors.New("sqlite migration staging target changed during publication")
	}
	d = filesystemDSN(path, "rw", "DELETE")
	store, err := New(ctx, Config{DSN: d})
	if err != nil {
		return nil, err
	}
	if err := verifyOwnedMigrationStore(ctx, store, attemptID); err != nil {
		_ = store.Close()
		return nil, err
	}
	return &MigrationTarget{store: store, path: path, attemptID: attemptID, identity: identity, checkpoint: checkpoint}, nil
}

func removeMigrationFileWithIdentity(path string, identity os.FileInfo) error {
	named, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("sqlite migration inspect failed target cleanup: %w", err)
	}
	if identity == nil || !regularSingleLink(named) || !os.SameFile(identity, named) {
		return errors.New("sqlite migration failed target changed; preserving it")
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("sqlite migration remove failed target: %w", err)
	}
	if err := syncParentDirectory(filepath.Dir(path)); err != nil {
		return fmt.Errorf("sqlite migration failed target removed: %w: %v", atomicfile.ErrApplied, err)
	}
	return nil
}

// OpenMigrationTarget opens an existing attempt-owned target without creating
// or provisioning it. It is the only recovery/adoption opener.
func OpenMigrationTarget(ctx context.Context, path, attemptID string) (*MigrationTarget, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || !migrationAttemptIDPattern.MatchString(attemptID) {
		return nil, errors.New("sqlite migration target identity is invalid")
	}
	before, err := os.Lstat(path)
	if err != nil || !regularSingleLink(before) {
		return nil, errors.New("sqlite migration target is absent or not a regular file")
	}
	d := filesystemDSN(path, "rw", "DELETE")
	store, err := New(ctx, Config{DSN: d})
	if err != nil {
		return nil, fmt.Errorf("sqlite migration open owned target: %w", err)
	}
	if err := verifyOwnedMigrationStore(ctx, store, attemptID); err != nil {
		_ = store.Close()
		return nil, err
	}
	after, err := os.Lstat(path)
	if err != nil || !regularSingleLink(after) || !os.SameFile(before, after) {
		_ = store.Close()
		return nil, errors.New("sqlite migration target changed while opening")
	}
	return &MigrationTarget{store: store, path: path, attemptID: attemptID, identity: before}, nil
}

func verifyOwnedMigrationStore(ctx context.Context, store *Store, attemptID string) error {
	if err := store.DB().PingContext(ctx); err != nil {
		return fmt.Errorf("sqlite migration target is unavailable: %w", err)
	}
	if err := verifySchemaVersion(ctx, store.DB()); err != nil {
		return err
	}
	var storedAttempt string
	if err := store.DB().QueryRowContext(ctx, "SELECT value FROM metadata WHERE `key` = ?", MigrationAttemptMetadataKey).Scan(&storedAttempt); err != nil || storedAttempt != attemptID {
		return errors.New("sqlite migration target is foreign")
	}
	return nil
}

func (m *MigrationTarget) RestoreBackendMigration(ctx context.Context, snapshot storage.BackendMigrationSnapshot) error {
	if m == nil || m.store == nil || m.closed.Load() {
		return errors.New("sqlite migration target is closed")
	}
	tx, err := m.store.DB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite migration begin restore: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after commit
	if err := issueops.RestoreBackendMigrationSnapshotInTx(ctx, tx, snapshot, migrationReservedMetadata(m.attemptID)); err != nil {
		return err
	}
	if m.checkpoint != nil {
		m.checkpoint("restore_journal")
	}
	if err := verifySQLiteMigrationIntegrity(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite migration commit restore: %w", err)
	}
	return nil
}

func (m *MigrationTarget) VerifyBackendMigration(ctx context.Context, snapshot storage.BackendMigrationSnapshot) error {
	if m == nil || m.store == nil || m.closed.Load() {
		return errors.New("sqlite migration target is closed")
	}
	tx, err := m.store.DB().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("sqlite migration begin verification: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // verification never commits
	if err := issueops.VerifyBackendMigrationSnapshotInTx(ctx, tx, snapshot, migrationReservedMetadata(m.attemptID)); err != nil {
		return err
	}
	return verifySQLiteMigrationIntegrity(ctx, tx)
}

func (m *MigrationTarget) VerifyBackendMigrationDigest(ctx context.Context, expectedDigest string, expectedCounts map[string]int) error {
	if m == nil || m.store == nil || m.closed.Load() {
		return errors.New("sqlite migration target is closed")
	}
	tx, err := m.store.DB().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("sqlite migration begin digest verification: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // verification never commits
	snapshot, err := issueops.ReadBackendMigrationSnapshotForVerificationInTx(ctx, tx, migrationReservedMetadata(m.attemptID))
	if err != nil {
		return err
	}
	digest, err := snapshot.Digest()
	if err != nil || digest != expectedDigest || !reflect.DeepEqual(snapshot.RowCounts(), expectedCounts) {
		return errors.New("sqlite migration target digest or counts differ from the durable attempt")
	}
	return verifySQLiteMigrationIntegrity(ctx, tx)
}

func (m *MigrationTarget) IsAdopted(ctx context.Context) (bool, error) {
	if m == nil || m.store == nil || m.closed.Load() {
		return false, errors.New("sqlite migration target is closed")
	}
	var state string
	if err := m.store.DB().QueryRowContext(ctx, "SELECT value FROM metadata WHERE `key` = ?", MigrationStateMetadataKey).Scan(&state); err != nil {
		return false, fmt.Errorf("sqlite migration read adoption state: %w", err)
	}
	return state == migrationStateAdopted, nil
}

func (m *MigrationTarget) MarkAdopted(ctx context.Context) error {
	if m == nil || m.store == nil || m.closed.Load() {
		return errors.New("sqlite migration target is closed")
	}
	result, err := m.store.DB().ExecContext(ctx,
		"UPDATE metadata SET value = ? WHERE `key` = ? AND value = ?",
		migrationStateAdopted, MigrationStateMetadataKey, migrationStateStaging)
	if err != nil {
		return fmt.Errorf("sqlite migration mark target adopted: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return errors.New("sqlite migration target adoption state is invalid")
	}
	return nil
}

func (m *MigrationTarget) VerifyIntegrity(ctx context.Context) error {
	if m == nil || m.store == nil || m.closed.Load() {
		return errors.New("sqlite migration target is closed")
	}
	tx, err := m.store.DB().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // verification never commits
	return verifySQLiteMigrationIntegrity(ctx, tx)
}

func verifySQLiteMigrationIntegrity(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("sqlite migration foreign-key check: %w", err)
	}
	if rows.Next() {
		_ = rows.Close()
		return errors.New("sqlite migration target has a foreign-key violation")
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	var integrity string
	if err := tx.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		return fmt.Errorf("sqlite migration integrity check: %w", err)
	}
	if integrity != "ok" {
		return errors.New("sqlite migration target failed integrity check")
	}
	return nil
}

func (m *MigrationTarget) Close() error {
	if m == nil || m.closed.Swap(true) {
		return nil
	}
	if err := m.store.Close(); err != nil {
		return err
	}
	named, err := os.Lstat(m.path)
	if err != nil || !regularSingleLink(named) || !os.SameFile(m.identity, named) {
		return errors.New("sqlite migration target changed before close")
	}
	file, err := os.Open(m.path) // #nosec G304 -- no-follow named identity is checked around this open.
	if err != nil {
		return err
	}
	opened, statErr := file.Stat()
	syncErr := file.Sync()
	closeErr := file.Close()
	after, afterErr := os.Lstat(m.path)
	if statErr != nil || afterErr != nil || opened == nil || !regularSingleLink(after) || !os.SameFile(m.identity, opened) || !os.SameFile(opened, after) {
		return errors.New("sqlite migration target changed before close")
	}
	return errors.Join(syncErr, closeErr, syncParentDirectory(filepath.Dir(m.path)))
}

// Promote installs the already-closed staging database at finalPath without
// replacing any existing entry, then fsyncs the namespace change.
func (m *MigrationTarget) Promote(finalPath string) error {
	if m == nil || !m.closed.Load() {
		return errors.New("sqlite migration target must be closed before promotion")
	}
	if !filepath.IsAbs(finalPath) || filepath.Clean(finalPath) != finalPath {
		return errors.New("sqlite migration final path must be absolute and clean")
	}
	sourceDirectory := filepath.Dir(m.path)
	finalDirectory := filepath.Dir(finalPath)
	if err := RefuseMigrationTargetCollision(finalPath); err != nil {
		return err
	}
	if err := refuseSQLiteSidecars(m.path); err != nil {
		return err
	}
	if err := noreplace.Rename(m.path, finalPath); err != nil {
		return fmt.Errorf("sqlite migration publish target: %w", err)
	}
	m.path = finalPath
	finalInfo, err := os.Lstat(finalPath)
	if err != nil || !regularSingleLink(finalInfo) || !os.SameFile(m.identity, finalInfo) {
		return errors.New("sqlite migration published target identity mismatch")
	}
	if err := syncParentDirectory(finalDirectory); err != nil {
		return fmt.Errorf("sqlite migration target published: %w: %v", atomicfile.ErrApplied, err)
	}
	if sourceDirectory != finalDirectory {
		if err := syncParentDirectory(sourceDirectory); err != nil {
			return fmt.Errorf("sqlite migration staging removal published: %w: %v", atomicfile.ErrApplied, err)
		}
	}
	return nil
}

// RefuseMigrationTargetCollision rejects every existing target entry and every
// SQLite sidecar, including symlinks and special files.
func RefuseMigrationTargetCollision(path string) error {
	if _, err := os.Lstat(path); err == nil {
		return errors.New("sqlite migration target already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("sqlite migration inspect target: %w", err)
	}
	return refuseSQLiteSidecars(path)
}

func refuseSQLiteSidecars(path string) error {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(path + suffix); err == nil {
			return errors.New("sqlite migration target sidecar already exists")
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("sqlite migration inspect sidecar: %w", err)
		}
	}
	return nil
}

func RemoveOwnedMigrationTarget(ctx context.Context, path, attemptID string) error {
	quarantine := migrationCleanupPath(path, attemptID)
	pathInfo, pathErr := os.Lstat(path)
	quarantineInfo, quarantineErr := os.Lstat(quarantine)
	pathMissing := errors.Is(pathErr, os.ErrNotExist)
	quarantineMissing := errors.Is(quarantineErr, os.ErrNotExist)
	if pathErr != nil && !pathMissing {
		return fmt.Errorf("sqlite migration inspect owned target: %w", pathErr)
	}
	if quarantineErr != nil && !quarantineMissing {
		return fmt.Errorf("sqlite migration inspect cleanup quarantine: %w", quarantineErr)
	}
	if err := refuseSQLiteSidecars(path); err != nil {
		return err
	}
	if err := refuseSQLiteSidecars(quarantine); err != nil {
		return err
	}
	if pathMissing && quarantineMissing {
		return nil
	}
	if !pathMissing && !quarantineMissing {
		if regularSingleLink(pathInfo) && regularSingleLink(quarantineInfo) && os.SameFile(pathInfo, quarantineInfo) {
			return errors.New("sqlite migration cleanup found duplicate links to one target; preserving both")
		}
		return errors.New("sqlite migration cleanup quarantine collides with another entry; preserving both")
	}

	ownedPath := path
	if pathMissing {
		ownedPath = quarantine
	}
	target, err := OpenMigrationTarget(ctx, ownedPath, attemptID)
	if err != nil {
		return err
	}
	identity := target.identity
	if err := target.Close(); err != nil {
		return err
	}
	if ownedPath == quarantine {
		return removeMigrationFileWithIdentity(quarantine, identity)
	}
	return quarantineAndRemoveMigrationFile(path, quarantine, identity, nil)
}

func migrationCleanupPath(path, attemptID string) string {
	return filepath.Join(filepath.Dir(path), ".backend-migration-"+attemptID+"-"+filepath.Base(path)+".cleanup")
}

func quarantineAndRemoveMigrationFile(path, quarantine string, identity os.FileInfo, beforeRename func()) error {
	if beforeRename != nil {
		beforeRename()
	}
	if err := RefuseMigrationTargetCollision(quarantine); err != nil {
		return fmt.Errorf("sqlite migration reserve cleanup quarantine: %w", err)
	}
	if err := noreplace.Rename(path, quarantine); err != nil {
		return fmt.Errorf("sqlite migration quarantine owned target: %w", err)
	}
	quarantined, err := os.Lstat(quarantine)
	if err != nil || identity == nil || !regularSingleLink(quarantined) || !os.SameFile(identity, quarantined) {
		if restoreErr := noreplace.Rename(quarantine, path); restoreErr != nil {
			return errors.Join(errors.New("sqlite migration cleanup path changed; preserving the replacement in quarantine"), err, restoreErr)
		}
		if syncErr := syncParentDirectory(filepath.Dir(path)); syncErr != nil {
			return fmt.Errorf("sqlite migration restored changed cleanup path but could not sync it: %w: %v", atomicfile.ErrApplied, syncErr)
		}
		return errors.Join(errors.New("sqlite migration cleanup path changed; restored the replacement and preserved the owned target"), err)
	}
	if err := syncParentDirectory(filepath.Dir(path)); err != nil {
		return fmt.Errorf("sqlite migration target quarantined: %w: %v", atomicfile.ErrApplied, err)
	}
	return removeMigrationFileWithIdentity(quarantine, identity)
}

func syncParentDirectory(path string) error {
	return atomicfile.SyncDirectory(path)
}
