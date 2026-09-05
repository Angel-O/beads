//go:build cgo

package embeddeddolt_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage/schema"
)

// TestEmbeddedMigration0067ScopeSchema proves the fresh and upgrade doors
// converge on the durable scope contract. It deliberately stops at schema
// rows: membership and active-scope APIs belong to later storage work.
func TestEmbeddedMigration0067ScopeSchema(t *testing.T) {
	requireEmbedded(t)
	ctx := t.Context()

	t.Run("fresh", func(t *testing.T) {
		dataDir := seedMainSchemaAt(t, ctx, schema.LatestVersion())
		conn, closeConn := openPinnedConn(t, ctx, dataDir)
		defer closeConn()

		assertScopeSchema(t, ctx, conn, "")
	})

	t.Run("upgrade leaves_existing_issues_unscoped", func(t *testing.T) {
		dataDir := seedMainSchemaAt(t, ctx, schema.LatestVersion()-1)
		conn, closeConn := openPinnedConn(t, ctx, dataDir)
		defer closeConn()

		const issueID = "scope-schema-existing-issue"
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO issues (id, title, description, design, acceptance_criteria, notes, status, priority, issue_type)
			VALUES (?, 'existing', '', '', '', '', 'open', 2, 'task')`, issueID); err != nil {
			t.Fatalf("seed existing issue: %v", err)
		}
		mustDrain(t, ctx, conn, "CALL DOLT_ADD('issues')")
		mustDrain(t, ctx, conn, "CALL DOLT_COMMIT('-m', 'test: seed pre-scope issue')")

		if _, err := schema.MigrateUp(ctx, conn); err != nil {
			t.Fatalf("upgrade through 0067: %v", err)
		}
		assertScopeSchema(t, ctx, conn, issueID)
	})
}

type scopeColumnShape struct {
	columnType string
	nullable   string
	defaultVal sql.NullString
	extra      string
}

func assertScopeSchema(t *testing.T, ctx context.Context, conn *sql.Conn, existingIssueID string) {
	t.Helper()

	wantColumns := map[string]map[string]scopeColumnShape{
		"scopes": {
			"id":              {"char(36)", "NO", sql.NullString{}, ""},
			"name":            {"varchar(255)", "NO", sql.NullString{}, ""},
			"normalized_name": {"varchar(255)", "NO", sql.NullString{}, ""},
			"created_on":      {"datetime", "NO", sql.NullString{Valid: true}, "DEFAULT_GENERATED"},
		},
		"scope_state": {
			"singleton_id":    {"tinyint", "NO", sql.NullString{}, ""},
			"active_scope_id": {"char(36)", "YES", sql.NullString{}, ""},
		},
		"scope_members": {
			"issue_id": {"varchar(255)", "NO", sql.NullString{}, ""},
			"scope_id": {"char(36)", "NO", sql.NullString{}, ""},
		},
	}

	for table, want := range wantColumns {
		got := queryScopeColumns(t, ctx, conn, table)
		if len(got) != len(want) {
			t.Fatalf("%s columns = %v, want exactly %v", table, got, want)
		}
		for name, shape := range want {
			gotShape, ok := got[name]
			if !ok {
				t.Errorf("%s missing column %s", table, name)
				continue
			}
			if gotShape.columnType != shape.columnType || gotShape.nullable != shape.nullable || gotShape.extra != shape.extra {
				t.Errorf("%s.%s = %#v, want %#v", table, name, gotShape, shape)
			}
			if gotShape.defaultVal.Valid != shape.defaultVal.Valid {
				t.Errorf("%s.%s default validity = %v, want %v", table, name, gotShape.defaultVal.Valid, shape.defaultVal.Valid)
				continue
			}
			if shape.defaultVal.Valid && !strings.Contains(strings.ToLower(gotShape.defaultVal.String), "current_timestamp") {
				t.Errorf("%s.%s default = %q, want current_timestamp", table, name, gotShape.defaultVal.String)
			}
		}
	}

	for _, index := range []struct {
		table, name, columns string
	}{
		{"scopes", "PRIMARY", "id"},
		{"scopes", "uk_scopes_normalized_created_on", "normalized_name,created_on"},
		{"scope_state", "PRIMARY", "singleton_id"},
		{"scope_members", "PRIMARY", "issue_id"},
		{"scope_members", "idx_scope_members_scope", "scope_id"},
		{"issues", "idx_issues_updated_at_id", "updated_at,id"},
	} {
		var columns string
		if err := conn.QueryRowContext(ctx, `
			SELECT GROUP_CONCAT(COLUMN_NAME ORDER BY SEQ_IN_INDEX)
			FROM INFORMATION_SCHEMA.STATISTICS
			WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND INDEX_NAME = ?`,
			index.table, index.name).Scan(&columns); err != nil {
			t.Fatalf("read %s.%s: %v", index.table, index.name, err)
		}
		if columns != index.columns {
			t.Errorf("%s.%s columns = %q, want %q", index.table, index.name, columns, index.columns)
		}
	}
	for _, foreignKey := range []struct {
		table, name, column, refTable, refColumn string
	}{
		{"scope_state", "fk_scope_state_active_scope", "active_scope_id", "scopes", "id"},
		{"scope_members", "fk_scope_members_issue", "issue_id", "issues", "id"},
		{"scope_members", "fk_scope_members_scope", "scope_id", "scopes", "id"},
	} {
		var refTable, refColumn string
		if err := conn.QueryRowContext(ctx, `
			SELECT REFERENCED_TABLE_NAME, REFERENCED_COLUMN_NAME
			FROM INFORMATION_SCHEMA.KEY_COLUMN_USAGE
			WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND CONSTRAINT_NAME = ? AND COLUMN_NAME = ?`,
			foreignKey.table, foreignKey.name, foreignKey.column).Scan(&refTable, &refColumn); err != nil {
			t.Fatalf("read %s.%s foreign key: %v", foreignKey.table, foreignKey.name, err)
		}
		if refTable != foreignKey.refTable || refColumn != foreignKey.refColumn {
			t.Errorf("%s.%s references %s(%s), want %s(%s)", foreignKey.table, foreignKey.name, refTable, refColumn, foreignKey.refTable, foreignKey.refColumn)
		}
	}

	var singleton, active sql.NullString
	if err := conn.QueryRowContext(ctx,
		"SELECT CAST(singleton_id AS CHAR), active_scope_id FROM scope_state").Scan(&singleton, &active); err != nil {
		t.Fatalf("read scope_state singleton: %v", err)
	}
	if singleton.String != "1" || active.Valid {
		t.Fatalf("scope_state = (%q, %v), want (1, NULL)", singleton.String, active.Valid)
	}

	const wantScopeID = "scope-schema-test-id"
	if _, err := conn.ExecContext(ctx,
		`INSERT INTO scopes (id, name, normalized_name, created_on) VALUES (?, 'first', 'same', '2026-01-01 00:00:00')`, wantScopeID); err != nil {
		t.Fatalf("insert scope: %v", err)
	}
	var scopeID, createdOn string
	if err := conn.QueryRowContext(ctx,
		`SELECT id, CAST(created_on AS CHAR) FROM scopes WHERE normalized_name = 'same'`).Scan(&scopeID, &createdOn); err != nil {
		t.Fatalf("read scope fields: %v", err)
	}
	if scopeID != wantScopeID || createdOn != "2026-01-01 00:00:00" {
		t.Fatalf("scope fields = (%q, %q), want (%q, explicit created_on)", scopeID, createdOn, wantScopeID)
	}

	if _, err := conn.ExecContext(ctx,
		`INSERT INTO scopes (id, name, normalized_name, created_on) VALUES ('scope-schema-duplicate-id', 'duplicate', 'same', '2026-01-01 00:00:00')`); err == nil {
		t.Fatal("duplicate (normalized_name, created_on) insert succeeded")
	}
	if _, err := conn.ExecContext(ctx,
		"INSERT INTO scope_state (singleton_id, active_scope_id) VALUES (2, NULL)"); err == nil {
		t.Fatal("scope_state singleton_id=2 insert succeeded")
	}
	if _, err := conn.ExecContext(ctx,
		"UPDATE scope_state SET active_scope_id = ? WHERE singleton_id = 1", scopeID); err != nil {
		t.Fatalf("set valid active scope: %v", err)
	}

	if existingIssueID == "" {
		return
	}
	var members int
	if err := conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM scope_members WHERE issue_id = ?", existingIssueID).Scan(&members); err != nil {
		t.Fatalf("count inferred membership: %v", err)
	}
	if members != 0 {
		t.Fatalf("pre-existing issue membership count = %d, want 0", members)
	}
	if _, err := conn.ExecContext(ctx,
		"INSERT INTO scope_members (issue_id, scope_id) VALUES (?, ?)", existingIssueID, scopeID); err != nil {
		t.Fatalf("insert scope membership: %v", err)
	}
	if _, err := conn.ExecContext(ctx,
		"INSERT INTO scope_members (issue_id, scope_id) VALUES (?, ?)", existingIssueID, scopeID); err == nil {
		t.Fatal("duplicate issue membership insert succeeded")
	}
}

func queryScopeColumns(t *testing.T, ctx context.Context, conn *sql.Conn, table string) map[string]scopeColumnShape {
	t.Helper()
	rows, err := conn.QueryContext(ctx, `
		SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, COLUMN_DEFAULT, EXTRA
		FROM INFORMATION_SCHEMA.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?`, table)
	if err != nil {
		t.Fatalf("read %s columns: %v", table, err)
	}
	defer rows.Close()

	got := map[string]scopeColumnShape{}
	for rows.Next() {
		var name string
		var shape scopeColumnShape
		if err := rows.Scan(&name, &shape.columnType, &shape.nullable, &shape.defaultVal, &shape.extra); err != nil {
			t.Fatalf("scan %s columns: %v", table, err)
		}
		got[name] = shape
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s columns: %v", table, err)
	}
	return got
}
