// Package scopeops contains the shared SQL bodies for both Dolt storage paths
// and the domain/db path. Keeping the invariant checks here prevents the
// embedded, server, and unit-of-work backends from acquiring different scope
// semantics.
package scopeops

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
)

type Runner = issueops.DBTX

func Create(ctx context.Context, r Runner, scope *types.Scope, activate bool) error {
	if scope == nil {
		return storage.ErrScopeInvalid
	}
	if err := validateScope(scope); err != nil {
		return err
	}
	if scope.CreatedOn.IsZero() {
		scope.CreatedOn = time.Now().UTC().Truncate(time.Second)
	} else {
		scope.CreatedOn = scope.CreatedOn.UTC().Truncate(time.Second)
	}
	scope.NormalizedName = normalizeName(scope.Name)
	if _, err := r.ExecContext(ctx, `
		INSERT INTO scopes (id, name, normalized_name, created_on)
		VALUES (?, ?, ?, ?)`, scope.ID, scope.Name, scope.NormalizedName, scope.CreatedOn); err != nil {
		if isDuplicateError(err) {
			return fmt.Errorf("%w: %s", storage.ErrScopeAlreadyExists, scope.ID)
		}
		return fmt.Errorf("create scope: %w", err)
	}
	if activate {
		return Activate(ctx, r, scope.ID)
	}
	return nil
}

func List(ctx context.Context, r Runner) ([]*types.Scope, error) {
	rows, err := r.QueryContext(ctx, `
		SELECT id, name, normalized_name, created_on
		FROM scopes ORDER BY created_on ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list scopes: %w", err)
	}
	defer rows.Close()
	var scopes []*types.Scope
	for rows.Next() {
		scope, err := scanScope(rows)
		if err != nil {
			return nil, fmt.Errorf("list scopes: %w", err)
		}
		scopes = append(scopes, scope)
	}
	return scopes, rows.Err()
}

func Get(ctx context.Context, r Runner, id string) (*types.ScopeDetails, error) {
	if id == "" {
		return nil, storage.ErrScopeNotFound
	}
	scope, err := getScope(ctx, r, id)
	if err != nil {
		return nil, err
	}
	memberIDs, err := memberIDs(ctx, r, id)
	if err != nil {
		return nil, err
	}
	details := &types.ScopeDetails{Scope: *scope}
	for _, issueID := range memberIDs {
		issue, err := issueops.GetIssueInTx(ctx, r, issueID)
		if err != nil {
			return nil, fmt.Errorf("read scope %s member %s: %w", id, issueID, err)
		}
		details.Members = append(details.Members, issue)
	}
	details.Relationships, err = memberRelationships(ctx, r, id)
	if err != nil {
		return nil, err
	}
	return details, nil
}

func Active(ctx context.Context, r Runner) (*types.Scope, error) {
	var id sql.NullString
	if err := r.QueryRowContext(ctx,
		`SELECT active_scope_id FROM scope_state WHERE singleton_id = 1`).Scan(&id); err != nil {
		return nil, fmt.Errorf("read active scope: %w", err)
	}
	if !id.Valid || id.String == "" {
		return nil, nil
	}
	return getScope(ctx, r, id.String)
}

func Activate(ctx context.Context, r Runner, id string) error {
	if id == "" {
		return storage.ErrScopeNotFound
	}
	if _, err := lockScope(ctx, r, id); err != nil {
		return err
	}
	if err := r.QueryRowContext(ctx,
		`SELECT active_scope_id FROM scope_state WHERE singleton_id = 1 FOR UPDATE`).Scan(new(sql.NullString)); err != nil {
		return fmt.Errorf("activate scope: lock state: %w", err)
	}
	if _, err := r.ExecContext(ctx,
		`UPDATE scope_state SET active_scope_id = ? WHERE singleton_id = 1`, id); err != nil {
		return fmt.Errorf("activate scope: %w", err)
	}
	return nil
}

func Deactivate(ctx context.Context, r Runner) error {
	if err := r.QueryRowContext(ctx,
		`SELECT active_scope_id FROM scope_state WHERE singleton_id = 1 FOR UPDATE`).Scan(new(sql.NullString)); err != nil {
		return fmt.Errorf("deactivate scope: lock state: %w", err)
	}
	if _, err := r.ExecContext(ctx,
		`UPDATE scope_state SET active_scope_id = NULL WHERE singleton_id = 1`); err != nil {
		return fmt.Errorf("deactivate scope: %w", err)
	}
	return nil
}

func AddMembers(ctx context.Context, r Runner, scopeID string, requested []string) error {
	if _, err := lockScope(ctx, r, scopeID); err != nil {
		return err
	}
	ids := uniqueSorted(requested)
	if err := ensureIssues(ctx, r, ids); err != nil {
		return err
	}
	members, err := lockedMemberships(ctx, r, ids)
	if err != nil {
		return err
	}
	newCount := 0
	for _, issueID := range ids {
		if current, ok := members[issueID]; ok {
			if current != scopeID {
				return &storage.ScopeMembershipConflictError{IssueID: issueID, ExistingScope: current, RequestedScope: scopeID}
			}
			continue
		}
		newCount++
	}
	current, err := countMembers(ctx, r, scopeID)
	if err != nil {
		return err
	}
	if current+newCount > storage.MaxScopeMembers {
		return &storage.ScopeCapacityError{ScopeID: scopeID, Current: current, Requested: newCount}
	}
	for _, issueID := range ids {
		if _, exists := members[issueID]; exists {
			continue
		}
		if _, err := r.ExecContext(ctx,
			`INSERT INTO scope_members (issue_id, scope_id) VALUES (?, ?)`, issueID, scopeID); err != nil {
			return fmt.Errorf("add scope member %s: %w", issueID, err)
		}
	}
	return nil
}

func RemoveMembers(ctx context.Context, r Runner, scopeID string, requested []string) error {
	if _, err := lockScope(ctx, r, scopeID); err != nil {
		return err
	}
	ids := uniqueSorted(requested)
	for _, issueID := range ids {
		if _, err := r.ExecContext(ctx,
			`DELETE FROM scope_members WHERE issue_id = ? AND scope_id = ?`, issueID, scopeID); err != nil {
			return fmt.Errorf("remove scope member %s: %w", issueID, err)
		}
	}
	return nil
}

func MoveMembers(ctx context.Context, r Runner, sourceID, targetID string, requested []string) error {
	ids := uniqueSorted(requested)
	if sourceID == targetID {
		if _, err := lockScope(ctx, r, sourceID); err != nil {
			return err
		}
		members, err := lockedMemberships(ctx, r, ids)
		if err != nil {
			return err
		}
		for _, issueID := range ids {
			if members[issueID] != sourceID {
				return &storage.ScopeSourceMembershipError{IssueID: issueID, ScopeID: sourceID}
			}
		}
		return nil
	}
	if sourceID == "" || targetID == "" {
		return storage.ErrScopeNotFound
	}
	if err := lockScopes(ctx, r, sourceID, targetID); err != nil {
		return err
	}
	members, err := lockedMemberships(ctx, r, ids)
	if err != nil {
		return err
	}
	for _, issueID := range ids {
		if members[issueID] != sourceID {
			return &storage.ScopeSourceMembershipError{IssueID: issueID, ScopeID: sourceID}
		}
	}
	current, err := countMembers(ctx, r, targetID)
	if err != nil {
		return err
	}
	if current+len(ids) > storage.MaxScopeMembers {
		return &storage.ScopeCapacityError{ScopeID: targetID, Current: current, Requested: len(ids)}
	}
	if len(ids) == 0 {
		return nil
	}
	placeholders, args := inArgs(ids)
	if _, err := r.ExecContext(ctx, fmt.Sprintf(
		`UPDATE scope_members SET scope_id = ? WHERE issue_id IN (%s) AND scope_id = ?`, placeholders),
		append([]any{targetID}, append(args[:len(ids)], sourceID)...)...); err != nil {
		return fmt.Errorf("move scope members: %w", err)
	}
	return nil
}

func validateScope(scope *types.Scope) error {
	if scope.ID == "" || len(scope.ID) > 36 || strings.TrimSpace(scope.Name) == "" || len(scope.Name) > 255 {
		return storage.ErrScopeInvalid
	}
	return nil
}

func normalizeName(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

func getScope(ctx context.Context, r Runner, id string) (*types.Scope, error) {
	row := r.QueryRowContext(ctx,
		`SELECT id, name, normalized_name, created_on FROM scopes WHERE id = ?`, id)
	scope, err := scanScope(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", storage.ErrScopeNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("read scope %s: %w", id, err)
	}
	return scope, nil
}

func lockScope(ctx context.Context, r Runner, id string) (bool, error) {
	if id == "" {
		return false, storage.ErrScopeNotFound
	}
	var found string
	if err := r.QueryRowContext(ctx,
		`SELECT id FROM scopes WHERE id = ? FOR UPDATE`, id).Scan(&found); errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("%w: %s", storage.ErrScopeNotFound, id)
	} else if err != nil {
		return false, fmt.Errorf("lock scope %s: %w", id, err)
	}
	return true, nil
}

func lockScopes(ctx context.Context, r Runner, ids ...string) error {
	ordered := uniqueSorted(ids)
	for _, id := range ordered {
		if _, err := lockScope(ctx, r, id); err != nil {
			return err
		}
	}
	return nil
}

func ensureIssues(ctx context.Context, r Runner, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	placeholders, args := inArgs(ids)
	rows, err := r.QueryContext(ctx, fmt.Sprintf(
		`SELECT id FROM issues WHERE id IN (%s) FOR UPDATE`, placeholders), args...)
	if err != nil {
		return fmt.Errorf("check scope issues: %w", err)
	}
	defer rows.Close()
	found := make(map[string]bool, len(ids))
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("check scope issues: %w", err)
		}
		found[id] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("check scope issues: %w", err)
	}
	for _, id := range ids {
		if !found[id] {
			return fmt.Errorf("%w: %s", storage.ErrScopeIssueNotFound, id)
		}
	}
	return nil
}

func lockedMemberships(ctx context.Context, r Runner, ids []string) (map[string]string, error) {
	result := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	placeholders, args := inArgs(ids)
	rows, err := r.QueryContext(ctx, fmt.Sprintf(
		`SELECT issue_id, scope_id FROM scope_members WHERE issue_id IN (%s) ORDER BY issue_id FOR UPDATE`, placeholders), args...)
	if err != nil {
		return nil, fmt.Errorf("lock scope memberships: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var issueID, scopeID string
		if err := rows.Scan(&issueID, &scopeID); err != nil {
			return nil, fmt.Errorf("lock scope memberships: %w", err)
		}
		result[issueID] = scopeID
	}
	return result, rows.Err()
}

func countMembers(ctx context.Context, r Runner, scopeID string) (int, error) {
	var count int
	if err := r.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM scope_members WHERE scope_id = ?`, scopeID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count scope members: %w", err)
	}
	return count, nil
}

func memberIDs(ctx context.Context, r Runner, scopeID string) ([]string, error) {
	rows, err := r.QueryContext(ctx,
		`SELECT issue_id FROM scope_members WHERE scope_id = ? ORDER BY issue_id`, scopeID)
	if err != nil {
		return nil, fmt.Errorf("list scope members: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("list scope members: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func memberRelationships(ctx context.Context, r Runner, scopeID string) ([]*types.Dependency, error) {
	rows, err := r.QueryContext(ctx, `
		SELECT d.issue_id, d.depends_on_issue_id AS depends_on_id, d.type, d.created_at, d.created_by, d.metadata, d.thread_id
		FROM dependencies d
		JOIN scope_members source_member ON source_member.issue_id = d.issue_id AND source_member.scope_id = ?
		JOIN scope_members target_member ON target_member.issue_id = d.depends_on_issue_id AND target_member.scope_id = ?
		ORDER BY d.issue_id, depends_on_id, d.type, d.id`, scopeID, scopeID)
	if err != nil {
		return nil, fmt.Errorf("list scope relationships: %w", err)
	}
	defer rows.Close()
	var result []*types.Dependency
	for rows.Next() {
		var dep types.Dependency
		var createdAt sql.NullTime
		var metadata, threadID sql.NullString
		if err := rows.Scan(&dep.IssueID, &dep.DependsOnID, &dep.Type, &createdAt, &dep.CreatedBy, &metadata, &threadID); err != nil {
			return nil, fmt.Errorf("list scope relationships: %w", err)
		}
		if createdAt.Valid {
			dep.CreatedAt = createdAt.Time
		}
		if metadata.Valid {
			dep.Metadata = metadata.String
		}
		if threadID.Valid {
			dep.ThreadID = threadID.String
		}
		result = append(result, &dep)
	}
	return result, rows.Err()
}

type scanner interface{ Scan(...any) error }

func scanScope(row scanner) (*types.Scope, error) {
	var scope types.Scope
	if err := row.Scan(&scope.ID, &scope.Name, &scope.NormalizedName, &scope.CreatedOn); err != nil {
		return nil, err
	}
	return &scope, nil
}

func uniqueSorted(ids []string) []string {
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id != "" {
			set[id] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for id := range set {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

func inArgs(ids []string) (string, []any) {
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i], args[i] = "?", id
	}
	return strings.Join(placeholders, ","), args
}

func isDuplicateError(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "duplicate") || strings.Contains(message, "1062")
}
