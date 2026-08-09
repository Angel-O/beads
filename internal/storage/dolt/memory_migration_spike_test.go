package dolt

// PROTOTYPE ONLY: this file is executable evidence for Memory Beads spike A3.
// It deliberately does not install a production schema, migration hook, or
// public interface. The throwaway tables model only enough state to test the
// current Dolt publication and recovery constraints.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/steveyegge/beads/internal/storage/schema"
)

const (
	spikeLegacyMemoryPrefix = "kv.memory."
	spikeMigrationScope     = "memory-beads-a3-v1"
	spikeProjectID          = "018f6df0-7b4b-7a20-9d31-4f517f2860c1"
	spikeAuthor             = "Ada Example <ada@example.com>"
)

var (
	errSpikeBeforePublication = errors.New("spike: interrupted before publication")
	errSpikeAfterStaging      = errors.New("spike: interrupted after staging")
	errSpikeAfterPublication  = errors.New("spike: publication acknowledgement lost")
)

type spikeFault int

const (
	spikeNoFault spikeFault = iota
	spikeFaultBeforePublication
	spikeFaultAfterStaging
	spikeFaultAfterPublication
)

type spikeMigrationResult struct {
	Applied        bool
	AlreadyApplied bool
	Migrated       int
}

type spikeLegacyMemory struct {
	key      string
	body     string
	bead     string
	revision string
}

// runMemoryMigrationSpike gives each concurrent attempt its own pinned
// connection. The real production decision must expose this through a private
// provider capability rather than exporting SQL or adding a method to the
// append-closed storage interfaces.
func runMemoryMigrationSpike(
	ctx context.Context,
	db *sql.DB,
	projectID, author string,
	fault spikeFault,
) (spikeMigrationResult, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return spikeMigrationResult{}, err
	}
	defer conn.Close()
	return runMemoryMigrationSpikeOnConn(ctx, conn, projectID, author, fault)
}

// runMemoryMigrationSpikeOnConn is intentionally a narrow current-branch
// prototype. It proves the transaction/marker shape that works and makes the
// absence of a cross-branch atomic primitive visible in the branch test below.
func runMemoryMigrationSpikeOnConn(
	ctx context.Context,
	conn *sql.Conn,
	projectID, author string,
	fault spikeFault,
) (result spikeMigrationResult, err error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" || projectID == "00000000-0000-0000-0000-000000000000" {
		return result, fmt.Errorf("memory migration attribution: a durable non-sentinel project identity is required")
	}
	if err := validateSpikeAuthor(author); err != nil {
		return result, err
	}

	var database string
	if err := conn.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&database); err != nil {
		return result, fmt.Errorf("memory migration: identify database: %w", err)
	}
	lockName := schema.MigrationLockName(database)
	if err := schema.AcquireMigrationLock(ctx, conn, lockName); err != nil {
		return result, err
	}
	defer func() {
		err = errors.Join(err, schema.ReleaseMigrationLock(conn, lockName))
	}()

	var applied int
	if err := conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM memory_migration_spike_ledger WHERE scope = ?",
		spikeMigrationScope,
	).Scan(&applied); err != nil {
		return result, fmt.Errorf("memory migration: read durable marker: %w", err)
	}
	if applied != 0 {
		legacyKeys, err := readSpikeLegacyMemoryKeys(ctx, conn)
		if err != nil {
			return result, err
		}
		if len(legacyKeys) != 0 {
			return result, fmt.Errorf(
				"memory migration: durable marker exists but legacy rows reappeared at key(s) %s; disable the compatibility writer before repair",
				strings.Join(legacyKeys, ", "),
			)
		}
		return spikeMigrationResult{AlreadyApplied: true}, nil
	}

	unsafeKeys, err := spikeUnsafeDirtyConfigKeys(ctx, conn)
	if err != nil {
		return result, err
	}
	if len(unsafeKeys) != 0 {
		return result, fmt.Errorf(
			"memory migration: config has unrelated working-set changes at key(s) %s; commit or revert them before retrying",
			strings.Join(unsafeKeys, ", "),
		)
	}

	memories, err := readSpikeLegacyMemories(ctx, conn, projectID)
	if err != nil {
		return result, err
	}
	var sourceState string
	if err := conn.QueryRowContext(ctx, "SELECT DOLT_HASHOF_DB()").Scan(&sourceState); err != nil {
		return result, fmt.Errorf("memory migration: fingerprint visible source state: %w", err)
	}
	for i := range memories {
		memories[i].revision = spikeStableID("mrev-", memories[i].bead, memories[i].body, sourceState)
	}

	if _, err := conn.ExecContext(ctx, "START TRANSACTION"); err != nil {
		return result, fmt.Errorf("memory migration: start transaction: %w", err)
	}
	transactionOpen := true
	defer func() {
		if transactionOpen {
			_, rollbackErr := conn.ExecContext(context.Background(), "ROLLBACK")
			err = errors.Join(err, rollbackErr)
		}
	}()

	for _, memory := range memories {
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO memory_migration_spike_beads
				(id, legacy_key, body, origin)
			VALUES (?, ?, ?, 'legacy_migration')`,
			memory.bead, memory.key, memory.body,
		); err != nil {
			return result, fmt.Errorf("memory migration: insert bead for key %q: %w", memory.key, err)
		}
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO memory_migration_spike_revisions
				(id, bead_id, body, change_author, origin, source_state)
			VALUES (?, ?, ?, ?, 'legacy_migration', ?)`,
			memory.revision, memory.bead, memory.body, author, sourceState,
		); err != nil {
			return result, fmt.Errorf("memory migration: insert revision for key %q: %w", memory.key, err)
		}
	}
	if _, err := conn.ExecContext(ctx,
		"DELETE FROM config WHERE `key` LIKE CONCAT(?, '%')", spikeLegacyMemoryPrefix,
	); err != nil {
		return result, fmt.Errorf("memory migration: delete legacy rows: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO memory_migration_spike_ledger
			(scope, project_id, source_state, migrated_count, change_author)
		VALUES (?, ?, ?, ?, ?)`,
		spikeMigrationScope, projectID, sourceState, len(memories), author,
	); err != nil {
		return result, fmt.Errorf("memory migration: write durable marker: %w", err)
	}

	if fault == spikeFaultBeforePublication {
		return result, errSpikeBeforePublication
	}

	// Stage only the tables owned by this migration. DOLT_COMMIT('-Am') would
	// sweep unrelated working-set changes into the migration commit.
	for _, table := range []string{
		"config",
		"memory_migration_spike_beads",
		"memory_migration_spike_revisions",
		"memory_migration_spike_ledger",
	} {
		if err := schema.DrainCall(ctx, conn, "CALL DOLT_ADD(?)", table); err != nil {
			return result, fmt.Errorf("memory migration: stage %s: %w", table, err)
		}
	}
	if fault == spikeFaultAfterStaging {
		return result, errSpikeAfterStaging
	}
	if err := schema.DrainCall(ctx, conn,
		"CALL DOLT_COMMIT('-m', ?, '--author', ?)",
		"spike: migrate legacy memories", author,
	); err != nil {
		return result, fmt.Errorf("memory migration: publish: %w", err)
	}
	transactionOpen = false
	result = spikeMigrationResult{Applied: true, Migrated: len(memories)}
	if fault == spikeFaultAfterPublication {
		return result, errSpikeAfterPublication
	}
	return result, nil
}

func validateSpikeAuthor(author string) error {
	author = strings.TrimSpace(author)
	if author == "" || author == "unknown" {
		return fmt.Errorf("memory migration attribution: configure a human author before retrying")
	}
	open := strings.LastIndex(author, " <")
	if open <= 0 || !strings.HasSuffix(author, ">") || open+2 == len(author)-1 {
		return fmt.Errorf("memory migration attribution: author must be configured as Name <email>")
	}
	return nil
}

func spikeUnsafeDirtyConfigKeys(ctx context.Context, conn *sql.Conn) ([]string, error) {
	rows, err := conn.QueryContext(ctx,
		"SELECT COALESCE(to_key, from_key) FROM dolt_diff('HEAD', 'WORKING', 'config')")
	if err != nil {
		return nil, fmt.Errorf("memory migration: inspect config working set: %w", err)
	}
	defer rows.Close()

	var unsafe []string
	for rows.Next() {
		var key sql.NullString
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("memory migration: scan config working-set key: %w", err)
		}
		if key.Valid && !strings.HasPrefix(key.String, spikeLegacyMemoryPrefix) {
			unsafe = append(unsafe, key.String)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("memory migration: iterate config working set: %w", err)
	}
	sort.Strings(unsafe)
	return unsafe, nil
}

func readSpikeLegacyMemories(
	ctx context.Context,
	conn *sql.Conn,
	projectID string,
) ([]spikeLegacyMemory, error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT `+"`key`"+`, value
		FROM config
		WHERE `+"`key`"+` LIKE CONCAT(?, '%')
		ORDER BY `+"`key`"+``, spikeLegacyMemoryPrefix)
	if err != nil {
		return nil, fmt.Errorf("memory migration: read visible legacy rows: %w", err)
	}
	defer rows.Close()

	var memories []spikeLegacyMemory
	for rows.Next() {
		var storageKey, body string
		if err := rows.Scan(&storageKey, &body); err != nil {
			return nil, fmt.Errorf("memory migration: scan visible legacy row: %w", err)
		}
		key := strings.TrimPrefix(storageKey, spikeLegacyMemoryPrefix)
		memories = append(memories, spikeLegacyMemory{
			key:  key,
			body: body,
			bead: spikeStableID("mem-", projectID, key),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("memory migration: iterate visible legacy rows: %w", err)
	}
	return memories, nil
}

func readSpikeLegacyMemoryKeys(ctx context.Context, conn *sql.Conn) ([]string, error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT `+"`key`"+`
		FROM config
		WHERE `+"`key`"+` LIKE CONCAT(?, '%')
		ORDER BY `+"`key`"+``, spikeLegacyMemoryPrefix)
	if err != nil {
		return nil, fmt.Errorf("memory migration: inspect legacy rows after durable marker: %w", err)
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var storageKey string
		if err := rows.Scan(&storageKey); err != nil {
			return nil, fmt.Errorf("memory migration: scan legacy key after durable marker: %w", err)
		}
		keys = append(keys, strings.TrimPrefix(storageKey, spikeLegacyMemoryPrefix))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("memory migration: iterate legacy keys after durable marker: %w", err)
	}
	return keys, nil
}

func spikeStableID(prefix string, parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	return prefix + hex.EncodeToString(h.Sum(nil))[:32]
}

func installMemoryMigrationSpikeSchema(t *testing.T, ctx context.Context, conn *sql.Conn) {
	t.Helper()
	for _, statement := range []string{
		`CREATE TABLE memory_migration_spike_beads (
			id VARCHAR(64) PRIMARY KEY,
			legacy_key VARCHAR(255) NOT NULL UNIQUE,
			body LONGTEXT NOT NULL,
			origin VARCHAR(32) NOT NULL
		)`,
		`CREATE TABLE memory_migration_spike_revisions (
			id VARCHAR(64) PRIMARY KEY,
			bead_id VARCHAR(64) NOT NULL,
			body LONGTEXT NOT NULL,
			change_author VARCHAR(255) NOT NULL,
			origin VARCHAR(32) NOT NULL,
			source_state VARCHAR(64) NOT NULL
		)`,
		`CREATE TABLE memory_migration_spike_ledger (
			scope VARCHAR(64) PRIMARY KEY,
			project_id VARCHAR(64) NOT NULL,
			source_state VARCHAR(64) NOT NULL,
			migrated_count INT NOT NULL,
			change_author VARCHAR(255) NOT NULL
		)`,
	} {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			t.Fatalf("install spike schema: %v", err)
		}
	}
	spikeCommit(t, ctx, conn, "spike fixture: install throwaway memory tables")
}

func spikeCommit(t *testing.T, ctx context.Context, conn *sql.Conn, message string) {
	t.Helper()
	if err := schema.DrainCall(ctx, conn,
		"CALL DOLT_COMMIT('-Am', ?, '--author', ?)", message, spikeAuthor,
	); err != nil {
		t.Fatalf("commit spike fixture %q: %v", message, err)
	}
}

func spikeCount(t *testing.T, ctx context.Context, conn *sql.Conn, query string, args ...any) int {
	t.Helper()
	var count int
	if err := conn.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		t.Fatalf("query spike count: %v\nquery: %s", err, query)
	}
	return count
}

func spikeString(t *testing.T, ctx context.Context, conn *sql.Conn, query string, args ...any) string {
	t.Helper()
	var value string
	if err := conn.QueryRowContext(ctx, query, args...).Scan(&value); err != nil {
		t.Fatalf("query spike string: %v\nquery: %s", err, query)
	}
	return value
}

func TestMemoryMigrationSpike_CurrentAndDirtyStateAreAtomicAndRetryable(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()
	conn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	installMemoryMigrationSpikeSchema(t, ctx, conn)
	boundaryKey := strings.Repeat("k", 245) // kv.memory. + 245 == config's 255-char limit.
	for key, body := range map[string]string{
		"committed": "committed body",
		boundaryKey: "boundary body",
	} {
		if _, err := conn.ExecContext(ctx,
			"INSERT INTO config (`key`, value) VALUES (?, ?)", spikeLegacyMemoryPrefix+key, body,
		); err != nil {
			t.Fatalf("seed committed legacy memory: %v", err)
		}
	}
	spikeCommit(t, ctx, conn, "spike fixture: committed legacy memories")
	for key, body := range map[string]string{
		"working": "working-set body",
		"empty":   "",
	} {
		if _, err := conn.ExecContext(ctx,
			"INSERT INTO config (`key`, value) VALUES (?, ?)", spikeLegacyMemoryPrefix+key, body,
		); err != nil {
			t.Fatalf("seed working-set legacy memory: %v", err)
		}
	}
	const unrelatedIssueID = "memory-spike-unrelated-dirty-issue"
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO issues
			(id, title, description, design, acceptance_criteria, notes)
		VALUES (?, 'unrelated dirty issue', '', '', '', '')`, unrelatedIssueID); err != nil {
		t.Fatalf("seed unrelated dirty issue: %v", err)
	}

	beforeCommits := spikeCount(t, ctx, conn, "SELECT COUNT(*) FROM dolt_log")
	if _, err := runMemoryMigrationSpikeOnConn(
		ctx, conn, spikeProjectID, spikeAuthor, spikeFaultBeforePublication,
	); !errors.Is(err, errSpikeBeforePublication) {
		t.Fatalf("pre-publication interruption error = %v, want injected fault", err)
	}
	if got := spikeCount(t, ctx, conn,
		"SELECT COUNT(*) FROM config WHERE `key` LIKE CONCAT(?, '%')", spikeLegacyMemoryPrefix,
	); got != 4 {
		t.Fatalf("legacy rows after rollback = %d, want 4", got)
	}
	if got := spikeCount(t, ctx, conn, "SELECT COUNT(*) FROM memory_migration_spike_beads"); got != 0 {
		t.Fatalf("canonical rows after rollback = %d, want 0", got)
	}
	if _, err := runMemoryMigrationSpikeOnConn(
		ctx, conn, spikeProjectID, spikeAuthor, spikeFaultAfterStaging,
	); !errors.Is(err, errSpikeAfterStaging) {
		t.Fatalf("post-staging interruption error = %v, want injected fault", err)
	}
	if got := spikeCount(t, ctx, conn,
		"SELECT COUNT(*) FROM config WHERE `key` LIKE CONCAT(?, '%')", spikeLegacyMemoryPrefix,
	); got != 4 {
		t.Fatalf("legacy rows after staged rollback = %d, want 4", got)
	}
	if got := spikeCount(t, ctx, conn, "SELECT COUNT(*) FROM memory_migration_spike_beads"); got != 0 {
		t.Fatalf("canonical rows after staged rollback = %d, want 0", got)
	}
	if got := spikeCount(t, ctx, conn, "SELECT COUNT(*) FROM memory_migration_spike_ledger"); got != 0 {
		t.Fatalf("ledger rows after staged rollback = %d, want 0", got)
	}
	if got := spikeCount(t, ctx, conn, "SELECT COUNT(*) FROM dolt_status WHERE staged = 1"); got != 0 {
		t.Fatalf("staged status rows after rollback = %d, want 0", got)
	}

	result, err := runMemoryMigrationSpikeOnConn(
		ctx, conn, spikeProjectID, spikeAuthor, spikeFaultAfterPublication,
	)
	if !errors.Is(err, errSpikeAfterPublication) {
		t.Fatalf("post-publication result = %+v, err = %v, want injected acknowledgement loss", result, err)
	}
	if !result.Applied || result.Migrated != 4 {
		t.Fatalf("post-publication result = %+v, want applied migration of 4 rows", result)
	}
	if got := spikeCount(t, ctx, conn,
		"SELECT COUNT(*) FROM config WHERE `key` LIKE CONCAT(?, '%')", spikeLegacyMemoryPrefix,
	); got != 0 {
		t.Fatalf("legacy rows after publication = %d, want 0", got)
	}
	if got := spikeCount(t, ctx, conn, "SELECT COUNT(*) FROM memory_migration_spike_beads"); got != 4 {
		t.Fatalf("canonical rows after publication = %d, want 4", got)
	}
	if got := spikeString(t, ctx, conn,
		"SELECT DISTINCT change_author FROM memory_migration_spike_revisions",
	); got != spikeAuthor {
		t.Fatalf("revision change author = %q, want %q", got, spikeAuthor)
	}
	if got := spikeString(t, ctx, conn,
		"SELECT committer FROM dolt_log WHERE message = 'spike: migrate legacy memories' LIMIT 1",
	); got != "Ada Example" {
		t.Fatalf("migration commit author = %q, want Ada Example", got)
	}
	if got := spikeString(t, ctx, conn,
		"SELECT email FROM dolt_log WHERE message = 'spike: migrate legacy memories' LIMIT 1",
	); got != "ada@example.com" {
		t.Fatalf("migration commit email = %q, want ada@example.com", got)
	}
	if got := spikeString(t, ctx, conn,
		"SELECT body FROM memory_migration_spike_beads WHERE legacy_key = 'empty'",
	); got != "" {
		t.Fatalf("empty legacy body changed to %q", got)
	}
	if got := spikeCount(t, ctx, conn,
		"SELECT COUNT(*) FROM memory_migration_spike_beads WHERE legacy_key = ?", boundaryKey,
	); got != 1 {
		t.Fatalf("245-character boundary key rows = %d, want 1", got)
	}
	if got := spikeCount(t, ctx, conn,
		"SELECT COUNT(*) FROM issues WHERE id = ?", unrelatedIssueID,
	); got != 1 {
		t.Fatalf("unrelated issue in working set = %d, want 1", got)
	}
	if got := spikeCount(t, ctx, conn,
		"SELECT COUNT(*) FROM issues AS OF 'HEAD' WHERE id = ?", unrelatedIssueID,
	); got != 0 {
		t.Fatalf("unrelated issue swept into migration commit: HEAD rows = %d, want 0", got)
	}
	if got := spikeCount(t, ctx, conn,
		"SELECT COUNT(*) FROM dolt_status WHERE table_name = 'issues'",
	); got != 1 {
		t.Fatalf("unrelated issue dirty-table status rows = %d, want 1", got)
	}

	retry, err := runMemoryMigrationSpikeOnConn(ctx, conn, spikeProjectID, spikeAuthor, spikeNoFault)
	if err != nil {
		t.Fatalf("retry after lost acknowledgement: %v", err)
	}
	if !retry.AlreadyApplied || retry.Applied {
		t.Fatalf("retry result = %+v, want already-applied", retry)
	}
	afterCommits := spikeCount(t, ctx, conn, "SELECT COUNT(*) FROM dolt_log")
	if afterCommits-beforeCommits != 1 {
		t.Fatalf("migration Dolt commits = %d, want exactly 1", afterCommits-beforeCommits)
	}

	const lateBody = "late-body-must-not-appear-in-diagnostics"
	if _, err := conn.ExecContext(ctx,
		"INSERT INTO config (`key`, value) VALUES ('kv.memory.reappeared', ?)", lateBody,
	); err != nil {
		t.Fatalf("seed recreated legacy row: %v", err)
	}
	beforeInvariantCheck := spikeString(t, ctx, conn, "SELECT DOLT_HASHOF_DB()")
	_, err = runMemoryMigrationSpikeOnConn(ctx, conn, spikeProjectID, spikeAuthor, spikeNoFault)
	if err == nil || !strings.Contains(err.Error(), "reappeared") {
		t.Fatalf("recreated-legacy error = %v, want affected key and repair guidance", err)
	}
	if strings.Contains(err.Error(), lateBody) {
		t.Fatalf("recreated-legacy error leaked body: %v", err)
	}
	if after := spikeString(t, ctx, conn, "SELECT DOLT_HASHOF_DB()"); after != beforeInvariantCheck {
		t.Fatalf("recreated-legacy invariant check mutated state: %s -> %s", beforeInvariantCheck, after)
	}
	if got := spikeCount(t, ctx, conn, "SELECT COUNT(*) FROM dolt_log"); got != afterCommits {
		t.Fatalf("recreated-legacy invariant check added a commit: got %d, want %d", got, afterCommits)
	}
}

func TestMemoryMigrationSpike_PreflightStopsBeforeAttributionOrDirtyConfigLoss(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()
	conn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	installMemoryMigrationSpikeSchema(t, ctx, conn)
	if _, err := conn.ExecContext(ctx,
		"INSERT INTO config (`key`, value) VALUES ('kv.memory.safe', 'safe body')"); err != nil {
		t.Fatal(err)
	}
	spikeCommit(t, ctx, conn, "spike fixture: legacy memory for preflight")

	beforeMissingIdentity := spikeString(t, ctx, conn, "SELECT DOLT_HASHOF_DB()")
	if _, err := runMemoryMigrationSpikeOnConn(
		ctx, conn, "00000000-0000-0000-0000-000000000000", spikeAuthor, spikeNoFault,
	); err == nil || !strings.Contains(err.Error(), "non-sentinel project identity") {
		t.Fatalf("sentinel-project error = %v, want durable project identity error", err)
	}
	if after := spikeString(t, ctx, conn, "SELECT DOLT_HASHOF_DB()"); after != beforeMissingIdentity {
		t.Fatalf("sentinel-project preflight mutated state: %s -> %s", beforeMissingIdentity, after)
	}

	if _, err := runMemoryMigrationSpikeOnConn(ctx, conn, spikeProjectID, "", spikeNoFault); err == nil ||
		!strings.Contains(err.Error(), "configure a human author") {
		t.Fatalf("missing-author error = %v, want actionable attribution error", err)
	}
	if after := spikeString(t, ctx, conn, "SELECT DOLT_HASHOF_DB()"); after != beforeMissingIdentity {
		t.Fatalf("missing-author preflight mutated state: %s -> %s", beforeMissingIdentity, after)
	}

	const unrelatedSecret = "unrelated-body-must-not-appear-in-diagnostics"
	if _, err := conn.ExecContext(ctx,
		"UPDATE config SET value = ? WHERE `key` = 'issue_prefix'", unrelatedSecret,
	); err != nil {
		t.Fatal(err)
	}
	beforeUnsafeDirt := spikeString(t, ctx, conn, "SELECT DOLT_HASHOF_DB()")
	_, err = runMemoryMigrationSpikeOnConn(ctx, conn, spikeProjectID, spikeAuthor, spikeNoFault)
	if err == nil || !strings.Contains(err.Error(), "issue_prefix") {
		t.Fatalf("unsafe-dirt error = %v, want affected key", err)
	}
	if strings.Contains(err.Error(), unrelatedSecret) {
		t.Fatalf("unsafe-dirt error leaked unrelated value: %v", err)
	}
	if after := spikeString(t, ctx, conn, "SELECT DOLT_HASHOF_DB()"); after != beforeUnsafeDirt {
		t.Fatalf("unsafe-dirt preflight mutated state: %s -> %s", beforeUnsafeDirt, after)
	}
	if got := spikeCount(t, ctx, conn, "SELECT COUNT(*) FROM memory_migration_spike_ledger"); got != 0 {
		t.Fatalf("ledger rows after refused preflights = %d, want 0", got)
	}
}

func TestMemoryMigrationSpike_ConcurrentFirstUseConvergesOnDurableMarker(t *testing.T) {
	store, cleanup := setupConcurrentTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	conn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	installMemoryMigrationSpikeSchema(t, ctx, conn)
	if _, err := conn.ExecContext(ctx,
		"INSERT INTO config (`key`, value) VALUES ('kv.memory.concurrent', 'one body')"); err != nil {
		t.Fatal(err)
	}
	spikeCommit(t, ctx, conn, "spike fixture: concurrent legacy memory")
	beforeCommits := spikeCount(t, ctx, conn, "SELECT COUNT(*) FROM dolt_log")
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}

	results := make([]spikeMigrationResult, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = runMemoryMigrationSpike(
				ctx, store.db, spikeProjectID, spikeAuthor, spikeNoFault,
			)
		}(i)
	}
	wg.Wait()

	applied, already := 0, 0
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent attempt %d: %v", i, err)
		}
		if results[i].Applied {
			applied++
		}
		if results[i].AlreadyApplied {
			already++
		}
	}
	if applied != 1 || already != 1 {
		t.Fatalf("concurrent results = %+v, want one applied and one already-applied", results)
	}
	verifyConn := mustSpikeConn(t, ctx, store.db)
	defer verifyConn.Close()
	if got := spikeCount(t, ctx, verifyConn,
		"SELECT COUNT(*) FROM memory_migration_spike_ledger"); got != 1 {
		t.Fatalf("ledger rows after concurrent first use = %d, want 1", got)
	}
	if got := spikeCount(t, ctx, verifyConn, "SELECT COUNT(*) FROM memory_migration_spike_revisions"); got != 1 {
		t.Fatalf("revision rows after concurrent first use = %d, want 1", got)
	}
	afterCommits := spikeCount(t, ctx, verifyConn, "SELECT COUNT(*) FROM dolt_log")
	if afterCommits-beforeCommits != 1 {
		t.Fatalf("concurrent migration Dolt commits = %d, want 1", afterCommits-beforeCommits)
	}
}

func mustSpikeConn(t *testing.T, ctx context.Context, db *sql.DB) *sql.Conn {
	t.Helper()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

func TestMemoryMigrationSpike_MigrationLockDoesNotGateOrdinaryConfigWriter(t *testing.T) {
	store, cleanup := setupConcurrentTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	migrationConn := mustSpikeConn(t, ctx, store.db)
	defer migrationConn.Close()
	// setupConcurrentTestStore seeds issue_prefix in the working set. Commit that
	// fixture state so the first preflight is genuinely clean.
	spikeCommit(t, ctx, migrationConn, "spike fixture: baseline concurrent config")
	ordinaryConn := mustSpikeConn(t, ctx, store.db)
	defer ordinaryConn.Close()

	var database string
	if err := migrationConn.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&database); err != nil {
		t.Fatal(err)
	}
	lockName := schema.MigrationLockName(database)
	if err := schema.AcquireMigrationLock(ctx, migrationConn, lockName); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := schema.ReleaseMigrationLock(migrationConn, lockName); err != nil {
			t.Errorf("release migration lock: %v", err)
		}
	}()

	if keys, err := spikeUnsafeDirtyConfigKeys(ctx, migrationConn); err != nil {
		t.Fatal(err)
	} else if len(keys) != 0 {
		t.Fatalf("initial unsafe config keys = %v, want none", keys)
	}

	// GET_LOCK is advisory. An ordinary config writer that does not participate
	// in the migration protocol can invalidate a completed preflight while the
	// migration connection still holds the database-scoped lock.
	if _, err := ordinaryConn.ExecContext(ctx,
		"UPDATE config SET value = 'ordinary writer after preflight' WHERE `key` = 'issue_prefix'",
	); err != nil {
		t.Fatalf("ordinary config write while migration lock is held: %v", err)
	}
	keys, err := spikeUnsafeDirtyConfigKeys(ctx, migrationConn)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0] != "issue_prefix" {
		t.Fatalf("unsafe config keys after ordinary write = %v, want [issue_prefix]", keys)
	}
}

func TestMemoryMigrationSpike_DeterministicIdentityDoesNotMakeBranchesAtomic(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()
	conn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	installMemoryMigrationSpikeSchema(t, ctx, conn)
	if _, err := conn.ExecContext(ctx,
		"INSERT INTO config (`key`, value) VALUES ('kv.memory.shared', 'ancestor')"); err != nil {
		t.Fatal(err)
	}
	spikeCommit(t, ctx, conn, "spike fixture: shared legacy ancestor")

	var current string
	if err := conn.QueryRowContext(ctx, "SELECT active_branch()").Scan(&current); err != nil {
		t.Fatal(err)
	}
	peer := strings.ReplaceAll(current, "/", "_") + "_memory_a3_peer"
	if err := schema.DrainCall(ctx, conn, "CALL DOLT_BRANCH(?, 'HEAD')", peer); err != nil {
		t.Fatalf("create peer branch: %v", err)
	}
	defer func() {
		_ = schema.DrainCall(context.Background(), conn, "CALL DOLT_CHECKOUT(?)", current)
		_ = schema.DrainCall(context.Background(), conn, "CALL DOLT_BRANCH('-Df', ?)", peer)
	}()

	if _, err := conn.ExecContext(ctx,
		"UPDATE config SET value = 'current branch' WHERE `key` = 'kv.memory.shared'"); err != nil {
		t.Fatal(err)
	}
	spikeCommit(t, ctx, conn, "spike fixture: current branch divergence")
	if err := schema.DrainCall(ctx, conn, "CALL DOLT_CHECKOUT(?)", peer); err != nil {
		t.Fatalf("checkout peer: %v", err)
	}
	if _, err := conn.ExecContext(ctx,
		"UPDATE config SET value = 'peer branch' WHERE `key` = 'kv.memory.shared'"); err != nil {
		t.Fatal(err)
	}
	spikeCommit(t, ctx, conn, "spike fixture: peer branch divergence")
	if err := schema.DrainCall(ctx, conn, "CALL DOLT_CHECKOUT(?)", current); err != nil {
		t.Fatalf("checkout current: %v", err)
	}

	if _, err := runMemoryMigrationSpikeOnConn(ctx, conn, spikeProjectID, spikeAuthor, spikeNoFault); err != nil {
		t.Fatalf("migrate current branch: %v", err)
	}
	currentBead := spikeString(t, ctx, conn,
		"SELECT id FROM memory_migration_spike_beads WHERE legacy_key = 'shared'")
	currentRevision := spikeString(t, ctx, conn,
		"SELECT id FROM memory_migration_spike_revisions WHERE bead_id = ?", currentBead)

	peerRef := spikeSQLStringLiteral(peer)
	if got := spikeCount(t, ctx, conn,
		fmt.Sprintf("SELECT COUNT(*) FROM config AS OF %s WHERE `key` = 'kv.memory.shared'", peerRef),
	); got != 1 {
		t.Fatalf("unmigrated peer legacy rows = %d, want 1", got)
	}
	if got := spikeCount(t, ctx, conn,
		fmt.Sprintf("SELECT COUNT(*) FROM memory_migration_spike_beads AS OF %s", peerRef),
	); got != 0 {
		t.Fatalf("unmigrated peer canonical rows = %d, want 0", got)
	}

	// There is now a provider-visible split: current is new-only and peer is
	// old-only. Resuming peer conversion converges identity, but it cannot make
	// the preceding multi-ref transition atomic after the fact.
	if err := schema.DrainCall(ctx, conn, "CALL DOLT_CHECKOUT(?)", peer); err != nil {
		t.Fatalf("checkout peer for resume: %v", err)
	}
	if _, err := runMemoryMigrationSpikeOnConn(ctx, conn, spikeProjectID, spikeAuthor, spikeNoFault); err != nil {
		t.Fatalf("resume migration on peer: %v", err)
	}
	peerBead := spikeString(t, ctx, conn,
		"SELECT id FROM memory_migration_spike_beads WHERE legacy_key = 'shared'")
	peerRevision := spikeString(t, ctx, conn,
		"SELECT id FROM memory_migration_spike_revisions WHERE bead_id = ?", peerBead)
	if peerBead != currentBead {
		t.Fatalf("canonical bead identity diverged: current %q, peer %q", currentBead, peerBead)
	}
	if peerRevision == currentRevision {
		t.Fatalf("divergent branch bodies collapsed to one revision %q", peerRevision)
	}
}

func spikeSQLStringLiteral(value string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), "'", "''") + "'"
}
