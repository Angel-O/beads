package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/noreplace"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/issueops"
)

const (
	testSourceAttempt = "11111111111111111111111111111111"
	testTargetAttempt = "22222222222222222222222222222222"
)

func TestBackendMigrationRoundTripsAllPortableTablesAndRepairsDerivedState(t *testing.T) {
	ctx := context.Background()
	source := newMigrationTargetForTest(t, ctx, filepath.Join(t.TempDir(), "source.db"), testSourceAttempt)
	populateEveryMigrationTable(t, ctx, source.store.DB())
	snapshot := readMigrationSnapshotForTest(t, ctx, source, testSourceAttempt)
	if got := len(snapshot.Tables); got != 19 {
		t.Fatalf("snapshot table count = %d, want 19", got)
	}
	if got := len(snapshot.Tables[0].Rows[0]); got != 57 {
		t.Fatalf("issue column count = %d, want all 57 current physical columns", got)
	}
	if err := source.Close(); err != nil {
		t.Fatalf("close source: %v", err)
	}

	target := newMigrationTargetForTest(t, ctx, filepath.Join(t.TempDir(), "target.db"), testTargetAttempt)
	defer target.Close() //nolint:errcheck // test cleanup
	if err := target.RestoreBackendMigration(ctx, snapshot); err != nil {
		t.Fatalf("restore snapshot: %v", err)
	}
	if err := target.VerifyBackendMigration(ctx, snapshot); err != nil {
		t.Fatalf("verify snapshot: %v", err)
	}

	var blocked int
	if err := target.store.DB().QueryRowContext(ctx, "SELECT is_blocked FROM issues WHERE id = ?", "mig-root").Scan(&blocked); err != nil {
		t.Fatalf("read repaired is_blocked: %v", err)
	}
	if blocked != 1 {
		t.Fatalf("migrated root is_blocked = %d, want derived value 1", blocked)
	}
	var nullWispEventTime, emptyWispEventValue int
	if err := target.store.DB().QueryRowContext(ctx,
		"SELECT created_at IS NULL, old_value = '' FROM wisp_events WHERE id = ?", "w-event-stable").
		Scan(&nullWispEventTime, &emptyWispEventValue); err != nil {
		t.Fatalf("read wisp event null/empty fidelity: %v", err)
	}
	if nullWispEventTime != 1 || emptyWispEventValue != 1 {
		t.Fatalf("wisp event null/empty fidelity = (%d,%d), want (1,1)", nullWispEventTime, emptyWispEventValue)
	}
	for table, wantID := range map[string]string{
		"dependencies":      "dep-stable",
		"wisp_dependencies": "wdep-stable",
		"comments":          "comment-stable",
		"wisp_comments":     "wcomment-stable",
		"events":            "event-stable",
		"wisp_events":       "w-event-stable",
	} {
		var got string
		if err := target.store.DB().QueryRowContext(ctx, "SELECT id FROM `"+table+"` LIMIT 1").Scan(&got); err != nil { //nolint:gosec // fixed test manifest
			t.Fatalf("read stable ID from %s: %v", table, err)
		}
		if got != wantID {
			t.Fatalf("%s ID = %q, want %q", table, got, wantID)
		}
	}
	var configCount int
	if err := target.store.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM config").Scan(&configCount); err != nil {
		t.Fatalf("count config: %v", err)
	}
	if configCount != 2 {
		t.Fatalf("migrated config count = %d, want exact source count 2 (no target seeds)", configCount)
	}
	childID, err := target.store.GetNextChildID(ctx, "mig-root")
	if err != nil {
		t.Fatalf("continue child counter: %v", err)
	}
	if childID != "mig-root.3" {
		t.Fatalf("next child ID = %q, want mig-root.3", childID)
	}
}

func TestBackendMigrationRestoreRollsBackLateFailure(t *testing.T) {
	ctx := context.Background()
	source := newMigrationTargetForTest(t, ctx, filepath.Join(t.TempDir(), "source.db"), testSourceAttempt)
	populateEveryMigrationTable(t, ctx, source.store.DB())
	snapshot := readMigrationSnapshotForTest(t, ctx, source, testSourceAttempt)
	_ = source.Close()

	target := newMigrationTargetForTest(t, ctx, filepath.Join(t.TempDir(), "target.db"), testTargetAttempt)
	defer target.Close() //nolint:errcheck // test cleanup
	if err := target.RestoreBackendMigration(ctx, snapshot); err != nil {
		t.Fatalf("initial restore: %v", err)
	}
	broken := cloneMigrationSnapshot(t, snapshot)
	repoMtimes := &broken.Tables[len(broken.Tables)-1]
	if repoMtimes.Name != "repo_mtimes" || len(repoMtimes.Rows) != 1 {
		t.Fatalf("unexpected final migration table: %#v", repoMtimes)
	}
	repoMtimes.Rows[0][1] = storage.BackendMigrationCell{Kind: storage.BackendMigrationCellNull}
	if err := target.RestoreBackendMigration(ctx, broken); err == nil {
		t.Fatal("late NOT NULL violation unexpectedly committed")
	}
	if err := target.VerifyBackendMigration(ctx, snapshot); err != nil {
		t.Fatalf("failed restore changed previously verified target: %v", err)
	}
}

func TestBackendMigrationRejectsNoncanonicalSnapshotBeforeMutation(t *testing.T) {
	ctx := context.Background()
	source := newMigrationTargetForTest(t, ctx, filepath.Join(t.TempDir(), "source.db"), testSourceAttempt)
	populateEveryMigrationTable(t, ctx, source.store.DB())
	snapshot := readMigrationSnapshotForTest(t, ctx, source, testSourceAttempt)
	_ = source.Close()

	target := newMigrationTargetForTest(t, ctx, filepath.Join(t.TempDir(), "target.db"), testTargetAttempt)
	defer target.Close() //nolint:errcheck // test cleanup
	broken := cloneMigrationSnapshot(t, snapshot)
	broken.Tables[0].Rows[0], broken.Tables[0].Rows[1] = broken.Tables[0].Rows[1], broken.Tables[0].Rows[0]
	if err := target.RestoreBackendMigration(ctx, broken); err == nil || !strings.Contains(err.Error(), "canonical primary-key order") {
		t.Fatalf("noncanonical snapshot error = %v", err)
	}
	var applicationRows int
	if err := target.store.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM issues").Scan(&applicationRows); err != nil {
		t.Fatalf("count untouched target: %v", err)
	}
	if applicationRows != 0 {
		t.Fatalf("invalid snapshot mutated target: %d issue rows", applicationRows)
	}
}

func TestBackendMigrationRejectsEveryTargetReservedMetadataCollision(t *testing.T) {
	ctx := context.Background()
	source := newMigrationTargetForTest(t, ctx, filepath.Join(t.TempDir(), "source.db"), testSourceAttempt)
	populateEveryMigrationTable(t, ctx, source.store.DB())
	snapshot := readMigrationSnapshotForTest(t, ctx, source, testSourceAttempt)
	_ = source.Close()

	for _, key := range []string{schemaVersionKey, MigrationAttemptMetadataKey, MigrationStateMetadataKey} {
		t.Run(key, func(t *testing.T) {
			broken := cloneMigrationSnapshot(t, snapshot)
			metadata := &broken.Tables[11]
			if metadata.Name != "metadata" {
				t.Fatalf("manifest table 11 = %q, want metadata", metadata.Name)
			}
			metadata.Rows = append([][]storage.BackendMigrationCell{{
				{Kind: storage.BackendMigrationCellText, Value: key},
				{Kind: storage.BackendMigrationCellText, Value: "foreign"},
			}}, metadata.Rows...)

			target := newMigrationTargetForTest(t, ctx, filepath.Join(t.TempDir(), "target.db"), testTargetAttempt)
			defer target.Close() //nolint:errcheck // test cleanup
			if err := target.RestoreBackendMigration(ctx, broken); err == nil || !strings.Contains(err.Error(), "collides") {
				t.Fatalf("reserved key %q restore error = %v", key, err)
			}
		})
	}
}

func TestMigrationTargetPromotionNeverReplacesCollision(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	staging := filepath.Join(dir, "staging.db")
	final := filepath.Join(dir, "final.db")
	target := newMigrationTargetForTest(t, ctx, staging, testSourceAttempt)
	if err := target.Close(); err != nil {
		t.Fatalf("close staging: %v", err)
	}
	if err := os.WriteFile(final, []byte("foreign"), 0o600); err != nil {
		t.Fatalf("create collision: %v", err)
	}
	if err := target.Promote(final); err == nil {
		t.Fatal("promotion unexpectedly replaced existing target")
	}
	data, err := os.ReadFile(final)
	if err != nil || string(data) != "foreign" {
		t.Fatalf("collision changed: data=%q err=%v", data, err)
	}
	if _, err := os.Stat(staging); err != nil {
		t.Fatalf("failed promotion removed staging: %v", err)
	}
}

func TestOwnedTargetCleanupRecoversQuarantineAndNeverDeletesReplacement(t *testing.T) {
	ctx := context.Background()
	t.Run("recover quarantine", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "target.db")
		target := newMigrationTargetForTest(t, ctx, path, testSourceAttempt)
		if err := target.Close(); err != nil {
			t.Fatal(err)
		}
		quarantine := migrationCleanupPath(path, testSourceAttempt)
		if err := noreplace.Rename(path, quarantine); err != nil {
			t.Fatal(err)
		}
		if err := RemoveOwnedMigrationTarget(ctx, path, testSourceAttempt); err != nil {
			t.Fatalf("recover quarantined cleanup: %v", err)
		}
		for _, candidate := range []string{path, quarantine} {
			if _, err := os.Lstat(candidate); !os.IsNotExist(err) {
				t.Fatalf("cleanup residue %s: %v", candidate, err)
			}
		}
	})

	t.Run("replacement survives", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "target.db")
		target := newMigrationTargetForTest(t, ctx, path, testSourceAttempt)
		if err := target.Close(); err != nil {
			t.Fatal(err)
		}
		identity, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		ownedBackup := filepath.Join(dir, "owned-backup.db")
		quarantine := migrationCleanupPath(path, testSourceAttempt)
		foreign := []byte("foreign replacement must survive")
		var hookErr error
		err = quarantineAndRemoveMigrationFile(path, quarantine, identity, func() {
			if renameErr := os.Rename(path, ownedBackup); renameErr != nil {
				hookErr = renameErr
				return
			}
			hookErr = os.WriteFile(path, foreign, 0o600)
		})
		if hookErr != nil {
			t.Fatalf("replacement hook: %v", hookErr)
		}
		if err == nil {
			t.Fatal("cleanup unexpectedly accepted a replaced pathname")
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil || !bytes.Equal(data, foreign) {
			t.Fatalf("foreign replacement was deleted: data=%q err=%v", data, readErr)
		}
		if _, err := os.Lstat(quarantine); !os.IsNotExist(err) {
			t.Fatalf("restored replacement still had a quarantine entry: %v", err)
		}
		if _, err := os.Stat(ownedBackup); err != nil {
			t.Fatalf("original owned target was lost: %v", err)
		}
	})
}

func TestCreateFreshForMigrationRemovesUnstampedFileAfterInitializationFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	path := filepath.Join(t.TempDir(), "staging.db")

	target, err := CreateFreshForMigration(ctx, path, testSourceAttempt)
	if err == nil {
		_ = target.Close()
		t.Fatal("canceled initialization unexpectedly succeeded")
	}
	if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
		t.Fatalf("failed initialization left an unstamped staging file: %v", statErr)
	}
}

func TestOpenExistingMissingAndHardlinkedSQLiteFailClosed(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.db")
	if _, err := OpenExisting(ctx, missing); err == nil {
		t.Fatal("missing configured SQLite unexpectedly opened")
	}
	if _, err := os.Lstat(missing); !os.IsNotExist(err) {
		t.Fatalf("missing configured SQLite was created: %v", err)
	}

	original := filepath.Join(dir, "original.db")
	store, err := Provision(ctx, original)
	if err != nil {
		t.Fatalf("provision original: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close original: %v", err)
	}
	hardlink := filepath.Join(dir, "hardlink.db")
	if err := os.Link(original, hardlink); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	if _, err := OpenExisting(ctx, hardlink); err == nil {
		t.Fatal("hardlinked configured SQLite unexpectedly opened")
	}
}

func newMigrationTargetForTest(t *testing.T, ctx context.Context, path, attemptID string) *MigrationTarget {
	t.Helper()
	target, err := CreateFreshForMigration(ctx, path, attemptID)
	if err != nil {
		t.Fatalf("create migration target: %v", err)
	}
	return target
}

func readMigrationSnapshotForTest(t *testing.T, ctx context.Context, target *MigrationTarget, attemptID string) storage.BackendMigrationSnapshot {
	t.Helper()
	tx, err := target.store.DB().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("begin snapshot: %v", err)
	}
	defer tx.Rollback() //nolint:errcheck // read-only test transaction
	snapshot, err := issueops.ReadBackendMigrationSnapshotForVerificationInTx(ctx, tx, migrationReservedMetadata(attemptID))
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	return snapshot
}

func populateEveryMigrationTable(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	insertCompleteIssueRow(t, ctx, db, "issues", "mig-root", false)
	insertCompleteIssueRow(t, ctx, db, "issues", "mig-blocker", false)
	insertCompleteIssueRow(t, ctx, db, "wisps", "mig-wisp", true)
	now := time.Date(2026, 7, 16, 12, 34, 56, 0, time.UTC)
	statements := []struct {
		query string
		args  []any
	}{
		{"INSERT INTO labels VALUES (?, ?)", []any{"mig-root", "alpha"}},
		{"INSERT INTO wisp_labels VALUES (?, ?)", []any{"mig-wisp", "wisp-label"}},
		{"INSERT INTO dependencies (id, issue_id, depends_on_issue_id, depends_on_wisp_id, depends_on_external, type, created_at, created_by, metadata, thread_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)", []any{"dep-stable", "mig-root", "mig-blocker", nil, nil, "blocks", now, "actor", `{}`, ""}},
		{"INSERT INTO wisp_dependencies (id, issue_id, depends_on_issue_id, depends_on_wisp_id, depends_on_external, type, created_at, created_by, metadata, thread_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)", []any{"wdep-stable", "mig-wisp", "mig-root", nil, nil, "blocks", now, "", `{}`, "thread"}},
		{"INSERT INTO comments VALUES (?, ?, ?, ?, ?)", []any{"comment-stable", "mig-root", "actor", "comment", now}},
		{"INSERT INTO wisp_comments VALUES (?, ?, ?, ?, ?)", []any{"wcomment-stable", "mig-wisp", "", "wisp comment", now}},
		{"INSERT INTO events VALUES (?, ?, ?, ?, ?, ?, ?, ?)", []any{"event-stable", "mig-root", "created", "actor", nil, "new", "", now}},
		{"INSERT INTO wisp_events VALUES (?, ?, ?, ?, ?, ?, ?, ?)", []any{"w-event-stable", "mig-wisp", "updated", "", "", nil, "", nil}},
		{"INSERT INTO config VALUES (?, ?), (?, ?)", []any{"custom.migration", "kept", "status.custom", "config-only:open"}},
		{"INSERT INTO metadata (`key`, value) VALUES (?, ?)", []any{"source-metadata", "kept"}},
		{"INSERT INTO local_metadata VALUES (?, ?)", []any{"local-key", "local-value"}},
		{"INSERT INTO issue_counter VALUES (?, ?)", []any{"mig", 41}},
		{"INSERT INTO child_counters VALUES (?, ?)", []any{"mig-root", 2}},
		{"INSERT INTO wisp_child_counters VALUES (?, ?)", []any{"mig-wisp", 7}},
		{"INSERT INTO custom_statuses VALUES (?, ?)", []any{"table-only", "open"}},
		{"INSERT INTO custom_types VALUES (?)", []any{"custom-kind"}},
		{"INSERT INTO repo_mtimes VALUES (?, ?, ?, ?)", []any{"/repo", "/repo/.beads/issues.jsonl", int64(123456789), now}},
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("populate migration table with %q: %v", statement.query, err)
		}
	}
}

func insertCompleteIssueRow(t *testing.T, ctx context.Context, db *sql.DB, table, id string, wisp bool) {
	t.Helper()
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(`"+table+"`)") //nolint:gosec // fixed test table names
	if err != nil {
		t.Fatalf("inspect %s columns: %v", table, err)
	}
	defer rows.Close() //nolint:errcheck // test cleanup
	var columns []string
	var values []any
	for rows.Next() {
		var ordinal, notNull, primaryKey int
		var name, dataType string
		var defaultValue sql.NullString
		if err := rows.Scan(&ordinal, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan %s column: %v", table, err)
		}
		columns = append(columns, "`"+name+"`")
		values = append(values, completeIssueValue(name, dataType, id, wisp))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("inspect %s columns: %v", table, err)
	}
	if len(columns) != 57 {
		t.Fatalf("%s physical column count = %d, want all 57 current columns", table, len(columns))
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(columns)), ",")
	query := fmt.Sprintf("INSERT INTO `%s` (%s) VALUES (%s)", table, strings.Join(columns, ","), placeholders) //nolint:gosec // fixed test schema
	if _, err := db.ExecContext(ctx, query, values...); err != nil {
		t.Fatalf("insert complete %s row: %v", table, err)
	}
}

func completeIssueValue(name, dataType, id string, wisp bool) any {
	now := time.Date(2026, 7, 16, 12, 34, 56, 0, time.UTC)
	switch name {
	case "id":
		return id
	case "title":
		return "title " + id
	case "status":
		return "open"
	case "priority":
		return 1
	case "issue_type":
		return "feature"
	case "created_at", "updated_at", "last_activity", "due_at", "defer_until", "started_at", "lease_expires_at", "heartbeat_at":
		return now
	case "closed_at", "compacted_at":
		return nil
	case "assignee":
		return nil
	case "metadata":
		return `{"physical":true}`
	case "ephemeral", "no_history":
		if wisp {
			return 1
		}
		return 0
	case "is_blocked":
		return 0 // deliberately stale for mig-root; restored state must be derived.
	case "row_lock":
		return 17
	case "estimated_minutes", "compaction_level", "original_size", "pinned", "is_template", "timeout_ns":
		return 3
	case "created_by", "closed_by_session":
		return "" // exercise empty strings independently from NULL assignee.
	}
	if strings.Contains(strings.ToLower(dataType), "int") {
		return 0
	}
	return name + "-value"
}

func cloneMigrationSnapshot(t *testing.T, snapshot storage.BackendMigrationSnapshot) storage.BackendMigrationSnapshot {
	t.Helper()
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot clone: %v", err)
	}
	var clone storage.BackendMigrationSnapshot
	if err := json.Unmarshal(data, &clone); err != nil {
		t.Fatalf("unmarshal snapshot clone: %v", err)
	}
	return clone
}
