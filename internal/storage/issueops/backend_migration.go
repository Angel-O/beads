package issueops

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/steveyegge/beads/internal/storage"
)

type backendMigrationColumn struct {
	name string
	kind storage.BackendMigrationCellKind
}

type backendMigrationTableManifest struct {
	name    string
	columns []backendMigrationColumn
	orderBy []string
}

func migrationText(name string) backendMigrationColumn {
	return backendMigrationColumn{name: name, kind: storage.BackendMigrationCellText}
}

func migrationInteger(name string) backendMigrationColumn {
	return backendMigrationColumn{name: name, kind: storage.BackendMigrationCellInteger}
}

func migrationTime(name string) backendMigrationColumn {
	return backendMigrationColumn{name: name, kind: storage.BackendMigrationCellTime}
}

func backendMigrationIssueColumns() []backendMigrationColumn {
	return []backendMigrationColumn{
		migrationText("id"), migrationText("content_hash"), migrationText("title"),
		migrationText("description"), migrationText("design"), migrationText("acceptance_criteria"),
		migrationText("notes"), migrationText("status"), migrationInteger("priority"),
		migrationText("issue_type"), migrationText("assignee"), migrationInteger("estimated_minutes"),
		migrationTime("created_at"), migrationText("created_by"), migrationText("owner"),
		migrationTime("updated_at"), migrationTime("closed_at"), migrationText("closed_by_session"),
		migrationText("external_ref"), migrationText("spec_id"), migrationInteger("compaction_level"),
		migrationTime("compacted_at"), migrationText("compacted_at_commit"), migrationInteger("original_size"),
		migrationText("sender"), migrationInteger("ephemeral"), migrationText("wisp_type"),
		migrationInteger("pinned"), migrationInteger("is_template"), migrationText("mol_type"),
		migrationText("work_type"), migrationText("source_system"), migrationText("metadata"),
		migrationText("source_repo"), migrationText("close_reason"), migrationText("event_kind"),
		migrationText("actor"), migrationText("target"), migrationText("payload"),
		migrationText("await_type"), migrationText("await_id"), migrationInteger("timeout_ns"),
		migrationText("waiters"), migrationText("hook_bead"), migrationText("role_bead"),
		migrationText("agent_state"), migrationTime("last_activity"), migrationText("role_type"),
		migrationText("rig"), migrationTime("due_at"), migrationTime("defer_until"),
		migrationInteger("no_history"), migrationTime("started_at"), migrationInteger("is_blocked"),
		migrationTime("lease_expires_at"), migrationTime("heartbeat_at"), migrationInteger("row_lock"),
	}
}

var backendMigrationTableManifests = []backendMigrationTableManifest{
	{name: "issues", columns: backendMigrationIssueColumns(), orderBy: []string{"id"}},
	{name: "wisps", columns: backendMigrationIssueColumns(), orderBy: []string{"id"}},
	{name: "labels", columns: []backendMigrationColumn{migrationText("issue_id"), migrationText("label")}, orderBy: []string{"issue_id", "label"}},
	{name: "wisp_labels", columns: []backendMigrationColumn{migrationText("issue_id"), migrationText("label")}, orderBy: []string{"issue_id", "label"}},
	{name: "dependencies", columns: []backendMigrationColumn{
		migrationText("id"), migrationText("issue_id"), migrationText("depends_on_issue_id"),
		migrationText("depends_on_wisp_id"), migrationText("depends_on_external"), migrationText("type"),
		migrationTime("created_at"), migrationText("created_by"), migrationText("metadata"), migrationText("thread_id"),
	}, orderBy: []string{"id"}},
	{name: "wisp_dependencies", columns: []backendMigrationColumn{
		migrationText("id"), migrationText("issue_id"), migrationText("depends_on_issue_id"),
		migrationText("depends_on_wisp_id"), migrationText("depends_on_external"), migrationText("type"),
		migrationTime("created_at"), migrationText("created_by"), migrationText("metadata"), migrationText("thread_id"),
	}, orderBy: []string{"id"}},
	{name: "comments", columns: []backendMigrationColumn{
		migrationText("id"), migrationText("issue_id"), migrationText("author"), migrationText("text"), migrationTime("created_at"),
	}, orderBy: []string{"id"}},
	{name: "wisp_comments", columns: []backendMigrationColumn{
		migrationText("id"), migrationText("issue_id"), migrationText("author"), migrationText("text"), migrationTime("created_at"),
	}, orderBy: []string{"id"}},
	{name: "events", columns: []backendMigrationColumn{
		migrationText("id"), migrationText("issue_id"), migrationText("event_type"), migrationText("actor"),
		migrationText("old_value"), migrationText("new_value"), migrationText("comment"), migrationTime("created_at"),
	}, orderBy: []string{"id"}},
	{name: "wisp_events", columns: []backendMigrationColumn{
		migrationText("id"), migrationText("issue_id"), migrationText("event_type"), migrationText("actor"),
		migrationText("old_value"), migrationText("new_value"), migrationText("comment"), migrationTime("created_at"),
	}, orderBy: []string{"id"}},
	{name: "config", columns: []backendMigrationColumn{migrationText("key"), migrationText("value")}, orderBy: []string{"key"}},
	{name: "metadata", columns: []backendMigrationColumn{migrationText("key"), migrationText("value")}, orderBy: []string{"key"}},
	{name: "local_metadata", columns: []backendMigrationColumn{migrationText("key"), migrationText("value")}, orderBy: []string{"key"}},
	{name: "issue_counter", columns: []backendMigrationColumn{migrationText("prefix"), migrationInteger("last_id")}, orderBy: []string{"prefix"}},
	{name: "child_counters", columns: []backendMigrationColumn{migrationText("parent_id"), migrationInteger("last_child")}, orderBy: []string{"parent_id"}},
	{name: "wisp_child_counters", columns: []backendMigrationColumn{migrationText("parent_id"), migrationInteger("last_child")}, orderBy: []string{"parent_id"}},
	{name: "custom_statuses", columns: []backendMigrationColumn{migrationText("name"), migrationText("category")}, orderBy: []string{"name"}},
	{name: "custom_types", columns: []backendMigrationColumn{migrationText("name")}, orderBy: []string{"name"}},
	{name: "repo_mtimes", columns: []backendMigrationColumn{
		migrationText("repo_path"), migrationText("jsonl_path"), migrationInteger("mtime_ns"), migrationTime("last_checked"),
	}, orderBy: []string{"repo_path"}},
}

var backendMigrationTransferredColumns = func() map[string]map[string]struct{} {
	result := make(map[string]map[string]struct{}, len(backendMigrationTableManifests))
	for _, table := range backendMigrationTableManifests {
		columns := make(map[string]struct{}, len(table.columns))
		for _, column := range table.columns {
			columns[column.name] = struct{}{}
		}
		result[table.name] = columns
	}
	return result
}()

var backendMigrationAllowedOmittedBaseTables = map[string]struct{}{
	"issue_snapshots": {}, "compaction_snapshots": {}, "routes": {},
	"interactions": {}, "federation_peers": {}, "schema_migrations": {},
	"ignored_schema_migrations": {},
}

var backendMigrationSemanticOmissions = []string{
	"issue_snapshots", "compaction_snapshots", "routes", "interactions", "federation_peers",
}

const (
	backendMigrationTablesQuery = `SELECT TABLE_NAME, TABLE_TYPE
		FROM INFORMATION_SCHEMA.TABLES
		WHERE TABLE_SCHEMA = DATABASE()
		ORDER BY TABLE_NAME`
	backendMigrationColumnsQuery = `SELECT TABLE_NAME, COLUMN_NAME
		FROM INFORMATION_SCHEMA.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE()
		ORDER BY TABLE_NAME, COLUMN_NAME`
	backendMigrationSQLiteSchemaVersionKey = "sqlite_schema_version"
	backendMigrationSQLiteAttemptKey       = "_beads_sqlite_migration_attempt"
	backendMigrationSQLiteStateKey         = "_beads_sqlite_migration_state"
)

// SnapshotBackendMigrationInTx performs the fail-closed Dolt census and, when
// portable, reads every application table in the same transaction.
func SnapshotBackendMigrationInTx(ctx context.Context, tx DBTX) (storage.BackendMigrationSnapshot, storage.BackendMigrationPortabilityReport, error) {
	report, err := InspectBackendMigrationPortabilityInTx(ctx, tx)
	if err != nil || !report.Portable() {
		return storage.BackendMigrationSnapshot{}, report, err
	}
	snapshot, err := readBackendMigrationSnapshotInTx(ctx, tx, nil)
	return snapshot, report, err
}

// InspectBackendMigrationPortabilityInTx fails closed over schema drift and
// source semantics SQLite cannot represent. Only aggregate counts escape.
func InspectBackendMigrationPortabilityInTx(ctx context.Context, tx DBTX) (storage.BackendMigrationPortabilityReport, error) {
	report, baseTables, err := inspectBackendMigrationShape(ctx, tx)
	if err != nil {
		return storage.BackendMigrationPortabilityReport{}, err
	}
	if report.MissingTransferredTables != 0 || report.UnexpectedBaseTables != 0 ||
		report.MissingTransferredColumns != 0 || report.UnexpectedTransferredColumns != 0 {
		return report, nil
	}

	for _, table := range backendMigrationSemanticOmissions {
		if _, present := baseTables[table]; !present {
			continue
		}
		var count int
		//nolint:gosec // table is selected from the fixed omission manifest.
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			return storage.BackendMigrationPortabilityReport{}, fmt.Errorf("inspect backend migration omitted semantic rows: %w", err)
		}
		report.OmittedSemanticRows += count
		switch table {
		case "issue_snapshots":
			report.IssueSnapshots = count
		case "compaction_snapshots":
			report.CompactionSnapshots = count
		}
	}

	probes := []struct {
		query string
		count *int
	}{
		{query: "SELECT COUNT(*) FROM issues WHERE COALESCE(compaction_level, 0) > 0", count: &report.CompactedIssues},
		{query: "SELECT COUNT(*) FROM wisps WHERE COALESCE(compaction_level, 0) > 0", count: &report.CompactedIssues},
		{query: "SELECT COUNT(*) FROM issues WHERE COALESCE(ephemeral, 0) <> 0 OR COALESCE(no_history, 0) <> 0", count: &report.TierMismatchedIssues},
		{query: "SELECT COUNT(*) FROM wisps WHERE COALESCE(ephemeral, 0) = 0 AND COALESCE(no_history, 0) = 0", count: &report.TierMismatchedIssues},
		// The regular events table requires created_at on SQLite. Wisp event
		// timestamps are nullable on both providers and transfer exactly.
		{query: "SELECT COUNT(*) FROM events WHERE created_at IS NULL", count: &report.NullEventTimestamps},
	}
	for _, probe := range probes {
		var count int
		if err := tx.QueryRowContext(ctx, probe.query).Scan(&count); err != nil {
			return storage.BackendMigrationPortabilityReport{}, fmt.Errorf("inspect backend migration semantic state: %w", err)
		}
		*probe.count += count
	}
	for _, key := range []string{backendMigrationSQLiteSchemaVersionKey, backendMigrationSQLiteAttemptKey, backendMigrationSQLiteStateKey} {
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM metadata WHERE `key` = ?", key).Scan(&count); err != nil {
			return storage.BackendMigrationPortabilityReport{}, fmt.Errorf("inspect backend migration reserved metadata: %w", err)
		}
		report.ReservedMetadataCollisions += count
	}

	inconsistent, err := CountIsBlockedInconsistenciesInTx(ctx, tx)
	if err != nil {
		return storage.BackendMigrationPortabilityReport{}, fmt.Errorf("inspect backend migration blocked state: %w", err)
	}
	report.BlockedStateInconsistencies = int(inconsistent)

	return report, nil
}

func inspectBackendMigrationShape(ctx context.Context, tx DBTX) (storage.BackendMigrationPortabilityReport, map[string]struct{}, error) {
	baseTables := make(map[string]struct{})
	rows, err := tx.QueryContext(ctx, backendMigrationTablesQuery)
	if err != nil {
		return storage.BackendMigrationPortabilityReport{}, nil, fmt.Errorf("inspect backend migration tables: %w", err)
	}
	for rows.Next() {
		var name, tableType string
		if err := rows.Scan(&name, &tableType); err != nil {
			_ = rows.Close()
			return storage.BackendMigrationPortabilityReport{}, nil, fmt.Errorf("scan backend migration tables: %w", err)
		}
		if !strings.EqualFold(tableType, "VIEW") {
			baseTables[name] = struct{}{}
		}
	}
	if err := closeRows(rows, "backend migration tables"); err != nil {
		return storage.BackendMigrationPortabilityReport{}, nil, err
	}

	actualColumns := make(map[string]map[string]struct{})
	rows, err = tx.QueryContext(ctx, backendMigrationColumnsQuery)
	if err != nil {
		return storage.BackendMigrationPortabilityReport{}, nil, fmt.Errorf("inspect backend migration columns: %w", err)
	}
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			_ = rows.Close()
			return storage.BackendMigrationPortabilityReport{}, nil, fmt.Errorf("scan backend migration columns: %w", err)
		}
		if actualColumns[table] == nil {
			actualColumns[table] = make(map[string]struct{})
		}
		actualColumns[table][column] = struct{}{}
	}
	if err := closeRows(rows, "backend migration columns"); err != nil {
		return storage.BackendMigrationPortabilityReport{}, nil, err
	}
	return compareBackendMigrationShape(baseTables, actualColumns), baseTables, nil
}

func closeRows(rows *sql.Rows, description string) error {
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close %s: %w", description, err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read %s: %w", description, err)
	}
	return nil
}

func compareBackendMigrationShape(baseTables map[string]struct{}, actualColumns map[string]map[string]struct{}) storage.BackendMigrationPortabilityReport {
	var report storage.BackendMigrationPortabilityReport
	for table, expectedColumns := range backendMigrationTransferredColumns {
		if _, present := baseTables[table]; !present {
			report.MissingTransferredTables++
			continue
		}
		for column := range expectedColumns {
			if _, present := actualColumns[table][column]; !present {
				report.MissingTransferredColumns++
			}
		}
		for column := range actualColumns[table] {
			if _, expected := expectedColumns[column]; expected {
				continue
			}
			report.UnexpectedTransferredColumns++
			switch table {
			case "issues":
				report.UnknownIssueColumns++
			case "wisps":
				report.UnknownWispColumns++
			}
		}
	}
	for table := range baseTables {
		if _, transferred := backendMigrationTransferredColumns[table]; transferred {
			continue
		}
		if _, allowed := backendMigrationAllowedOmittedBaseTables[table]; !allowed {
			report.UnexpectedBaseTables++
		}
	}
	return report
}

func readBackendMigrationSnapshotInTx(ctx context.Context, tx DBTX, excludedMetadata map[string]string) (storage.BackendMigrationSnapshot, error) {
	snapshot := storage.BackendMigrationSnapshot{Tables: make([]storage.BackendMigrationTable, 0, len(backendMigrationTableManifests))}
	for _, manifest := range backendMigrationTableManifests {
		table, err := readBackendMigrationTable(ctx, tx, manifest, excludedMetadata)
		if err != nil {
			return storage.BackendMigrationSnapshot{}, err
		}
		snapshot.Tables = append(snapshot.Tables, table)
	}
	return snapshot, nil
}

func readBackendMigrationTable(ctx context.Context, tx DBTX, manifest backendMigrationTableManifest, excludedMetadata map[string]string) (storage.BackendMigrationTable, error) {
	columns := make([]string, len(manifest.columns))
	for index, column := range manifest.columns {
		columns[index] = quoteBackendMigrationIdentifier(column.name)
	}
	order := make([]string, len(manifest.orderBy))
	for index, column := range manifest.orderBy {
		order[index] = quoteBackendMigrationIdentifier(column)
	}
	//nolint:gosec // every identifier comes from the fixed migration manifest.
	query := "SELECT " + strings.Join(columns, ", ") + " FROM " + quoteBackendMigrationIdentifier(manifest.name) + " ORDER BY " + strings.Join(order, ", ")
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return storage.BackendMigrationTable{}, fmt.Errorf("read backend migration table %s: %w", manifest.name, err)
	}
	table := storage.BackendMigrationTable{Name: manifest.name}
	for rows.Next() {
		cells, err := scanBackendMigrationRow(rows, manifest.columns)
		if err != nil {
			_ = rows.Close()
			return storage.BackendMigrationTable{}, fmt.Errorf("scan backend migration table %s: %w", manifest.name, err)
		}
		if manifest.name == "metadata" && len(cells) > 0 && cells[0].Kind == storage.BackendMigrationCellText {
			if _, excluded := excludedMetadata[cells[0].Value]; excluded {
				continue
			}
		}
		canonicalizeBackendMigrationDerivedCells(manifest, cells)
		table.Rows = append(table.Rows, cells)
	}
	if err := closeRows(rows, "backend migration table "+manifest.name); err != nil {
		return storage.BackendMigrationTable{}, err
	}
	sort.Slice(table.Rows, func(left, right int) bool {
		return compareBackendMigrationPrimaryKey(manifest, table.Rows[left], table.Rows[right]) < 0
	})
	return table, nil
}

func canonicalizeBackendMigrationDerivedCells(manifest backendMigrationTableManifest, cells []storage.BackendMigrationCell) {
	if manifest.name != "issues" && manifest.name != "wisps" {
		return
	}
	for index, column := range manifest.columns {
		if column.name == "is_blocked" {
			cells[index] = storage.BackendMigrationCell{Kind: storage.BackendMigrationCellInteger, Value: "0"}
			return
		}
	}
}

func compareBackendMigrationPrimaryKey(manifest backendMigrationTableManifest, left, right []storage.BackendMigrationCell) int {
	for _, name := range manifest.orderBy {
		columnIndex := -1
		for index, column := range manifest.columns {
			if column.name == name {
				columnIndex = index
				break
			}
		}
		if columnIndex < 0 {
			panic("backend migration manifest primary key is not a transferred column")
		}
		if comparison := compareBackendMigrationCell(left[columnIndex], right[columnIndex]); comparison != 0 {
			return comparison
		}
	}
	return 0
}

func compareBackendMigrationCell(left, right storage.BackendMigrationCell) int {
	if left.Kind < right.Kind {
		return -1
	}
	if left.Kind > right.Kind {
		return 1
	}
	return strings.Compare(left.Value, right.Value)
}

type backendMigrationScannedCell struct {
	kind      storage.BackendMigrationCellKind
	text      sql.NullString
	integer   sql.NullInt64
	timestamp sql.NullTime
}

func scanBackendMigrationRow(rows *sql.Rows, columns []backendMigrationColumn) ([]storage.BackendMigrationCell, error) {
	values := make([]backendMigrationScannedCell, len(columns))
	destinations := make([]any, len(columns))
	for index, column := range columns {
		values[index].kind = column.kind
		switch column.kind {
		case storage.BackendMigrationCellText:
			destinations[index] = &values[index].text
		case storage.BackendMigrationCellInteger:
			destinations[index] = &values[index].integer
		case storage.BackendMigrationCellTime:
			destinations[index] = &values[index].timestamp
		default:
			return nil, fmt.Errorf("unsupported migration cell kind %q", column.kind)
		}
	}
	if err := rows.Scan(destinations...); err != nil {
		return nil, err
	}
	result := make([]storage.BackendMigrationCell, len(values))
	for index, value := range values {
		switch value.kind {
		case storage.BackendMigrationCellText:
			if value.text.Valid {
				result[index] = storage.BackendMigrationCell{Kind: value.kind, Value: value.text.String}
			} else {
				result[index] = storage.BackendMigrationCell{Kind: storage.BackendMigrationCellNull}
			}
		case storage.BackendMigrationCellInteger:
			if value.integer.Valid {
				result[index] = storage.BackendMigrationCell{Kind: value.kind, Value: strconv.FormatInt(value.integer.Int64, 10)}
			} else {
				result[index] = storage.BackendMigrationCell{Kind: storage.BackendMigrationCellNull}
			}
		case storage.BackendMigrationCellTime:
			if value.timestamp.Valid {
				result[index] = storage.BackendMigrationCell{Kind: value.kind, Value: value.timestamp.Time.UTC().Format(time.RFC3339Nano)}
			} else {
				result[index] = storage.BackendMigrationCell{Kind: storage.BackendMigrationCellNull}
			}
		}
	}
	return result, nil
}

// RestoreBackendMigrationSnapshotInTx replaces all application rows in a fresh
// target. reservedMetadata is target-owned state (SQLite's schema stamp) that
// must exist exactly and is neither overwritten nor included in verification.
func RestoreBackendMigrationSnapshotInTx(ctx context.Context, tx *sql.Tx, snapshot storage.BackendMigrationSnapshot, reservedMetadata map[string]string) error {
	if err := validateBackendMigrationSnapshot(snapshot, reservedMetadata); err != nil {
		return err
	}
	for key, expected := range reservedMetadata {
		var actual string
		if err := tx.QueryRowContext(ctx, "SELECT value FROM metadata WHERE `key` = ?", key).Scan(&actual); err != nil || actual != expected {
			return fmt.Errorf("target reserved metadata %q is missing or invalid", key)
		}
	}

	// Delete children before parents. metadata retains only provider-owned rows.
	for index := len(backendMigrationTableManifests) - 1; index >= 0; index-- {
		name := backendMigrationTableManifests[index].name
		var err error
		if name == "metadata" && len(reservedMetadata) > 0 {
			keys := make([]string, 0, len(reservedMetadata))
			for key := range reservedMetadata {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			placeholders := make([]string, len(keys))
			args := make([]any, len(keys))
			for i, key := range keys {
				placeholders[i], args[i] = "?", key
			}
			err = execBackendMigration(ctx, tx, "DELETE FROM metadata WHERE `key` NOT IN ("+strings.Join(placeholders, ",")+")", args...)
		} else {
			err = execBackendMigration(ctx, tx, "DELETE FROM "+quoteBackendMigrationIdentifier(name))
		}
		if err != nil {
			return fmt.Errorf("clear backend migration table %s: %w", name, err)
		}
	}

	for index, manifest := range backendMigrationTableManifests {
		if err := insertBackendMigrationTable(ctx, tx, manifest, snapshot.Tables[index]); err != nil {
			return err
		}
	}
	if _, err := RecomputeAllIsBlockedInTx(ctx, tx); err != nil {
		return fmt.Errorf("recompute migrated blocked state: %w", err)
	}
	return VerifyBackendMigrationSnapshotInTx(ctx, tx, snapshot, reservedMetadata)
}

// VerifyBackendMigrationSnapshotInTx reads the restored target without leaving
// its transaction and compares every portable cell against the source.
func VerifyBackendMigrationSnapshotInTx(ctx context.Context, tx *sql.Tx, snapshot storage.BackendMigrationSnapshot, reservedMetadata map[string]string) error {
	actual, err := readBackendMigrationSnapshotInTx(ctx, tx, reservedMetadata)
	if err != nil {
		return fmt.Errorf("verify migrated target: %w", err)
	}
	if !reflect.DeepEqual(snapshot, actual) {
		return errors.New("migrated target differs from source portable state")
	}
	inconsistent, err := CountIsBlockedInconsistenciesInTx(ctx, tx)
	if err != nil {
		return fmt.Errorf("verify migrated blocked state: %w", err)
	}
	if inconsistent != 0 {
		return fmt.Errorf("migrated target has %d blocked-state inconsistencies", inconsistent)
	}
	return nil
}

// ReadBackendMigrationSnapshotForVerificationInTx returns the normalized target
// snapshot while excluding provider-owned metadata. Recovery uses this to
// validate a post-cutover target against the durable digest without reopening
// the Dolt source.
func ReadBackendMigrationSnapshotForVerificationInTx(ctx context.Context, tx *sql.Tx, reservedMetadata map[string]string) (storage.BackendMigrationSnapshot, error) {
	return readBackendMigrationSnapshotInTx(ctx, tx, reservedMetadata)
}

func validateBackendMigrationSnapshot(snapshot storage.BackendMigrationSnapshot, reservedMetadata map[string]string) error {
	if len(snapshot.Tables) != len(backendMigrationTableManifests) {
		return fmt.Errorf("backend migration snapshot has %d tables, want %d", len(snapshot.Tables), len(backendMigrationTableManifests))
	}
	for index, manifest := range backendMigrationTableManifests {
		table := snapshot.Tables[index]
		if table.Name != manifest.name {
			return fmt.Errorf("backend migration snapshot table %d is %q, want %q", index, table.Name, manifest.name)
		}
		for rowIndex, row := range table.Rows {
			if len(row) != len(manifest.columns) {
				return fmt.Errorf("backend migration table %s row %d has %d cells, want %d", table.Name, rowIndex, len(row), len(manifest.columns))
			}
			for columnIndex, cell := range row {
				expectedKind := manifest.columns[columnIndex].kind
				if cell.Kind != storage.BackendMigrationCellNull && cell.Kind != expectedKind {
					return fmt.Errorf("backend migration table %s row %d column %s has kind %q, want %q or null", table.Name, rowIndex, manifest.columns[columnIndex].name, cell.Kind, expectedKind)
				}
				if cell.Kind == storage.BackendMigrationCellInteger {
					if _, err := strconv.ParseInt(cell.Value, 10, 64); err != nil {
						return fmt.Errorf("backend migration integer is invalid: %w", err)
					}
				}
				if cell.Kind == storage.BackendMigrationCellTime {
					if _, err := storage.ParseBackendMigrationTime(cell.Value); err != nil {
						return fmt.Errorf("backend migration time is invalid: %w", err)
					}
				}
			}
			if table.Name == "metadata" && len(row) > 0 && row[0].Kind == storage.BackendMigrationCellText {
				if _, collision := reservedMetadata[row[0].Value]; collision {
					return fmt.Errorf("source metadata collides with target-reserved key %q", row[0].Value)
				}
			}
			if rowIndex > 0 && compareBackendMigrationPrimaryKey(manifest, table.Rows[rowIndex-1], row) >= 0 {
				return fmt.Errorf("backend migration table %s rows are not in canonical primary-key order", table.Name)
			}
		}
	}
	return nil
}

func insertBackendMigrationTable(ctx context.Context, tx *sql.Tx, manifest backendMigrationTableManifest, table storage.BackendMigrationTable) error {
	columns := make([]string, len(manifest.columns))
	placeholders := make([]string, len(manifest.columns))
	for index, column := range manifest.columns {
		columns[index], placeholders[index] = quoteBackendMigrationIdentifier(column.name), "?"
	}
	//nolint:gosec // every identifier comes from the fixed migration manifest.
	query := "INSERT INTO " + quoteBackendMigrationIdentifier(manifest.name) + " (" + strings.Join(columns, ",") + ") VALUES (" + strings.Join(placeholders, ",") + ")"
	for rowIndex, row := range table.Rows {
		args := make([]any, len(row))
		for cellIndex, cell := range row {
			value, err := backendMigrationCellValue(cell)
			if err != nil {
				return fmt.Errorf("decode backend migration table %s row %d: %w", table.Name, rowIndex, err)
			}
			args[cellIndex] = value
		}
		if err := execBackendMigration(ctx, tx, query, args...); err != nil {
			return fmt.Errorf("insert backend migration table %s row %d: %w", table.Name, rowIndex, err)
		}
	}
	return nil
}

func backendMigrationCellValue(cell storage.BackendMigrationCell) (any, error) {
	switch cell.Kind {
	case storage.BackendMigrationCellNull:
		return nil, nil
	case storage.BackendMigrationCellText:
		return cell.Value, nil
	case storage.BackendMigrationCellInteger:
		return strconv.ParseInt(cell.Value, 10, 64)
	case storage.BackendMigrationCellTime:
		return storage.ParseBackendMigrationTime(cell.Value)
	default:
		return nil, fmt.Errorf("unsupported migration cell kind %q", cell.Kind)
	}
}

func quoteBackendMigrationIdentifier(value string) string {
	return "`" + value + "`"
}

func execBackendMigration(ctx context.Context, tx *sql.Tx, query string, args ...any) error {
	_, err := tx.ExecContext(ctx, query, args...)
	return err
}
