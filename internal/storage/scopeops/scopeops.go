// Package scopeops contains the shared SQL bodies for both Dolt storage paths
// and the domain/db path. Keeping the invariant checks here prevents the
// embedded, server, and unit-of-work backends from acquiring different scope
// semantics.
package scopeops

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
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

const (
	defaultScopePageLimit = 50
	maxScopePageLimit     = 1000
	scopeCursorVersion    = 1
	scopeCatalogCursor    = "scope-catalog.v1."
	scopeMembersCursor    = "scope-members.v1."
)

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

// ListCatalog returns scope identity and aggregate counts in creation order.
// Pagination is keyset-based so a catalog walk does not shift when rows are
// added ahead of the current page.
func ListCatalog(ctx context.Context, r Runner, req storage.ScopeCatalogRequest) (*storage.ScopeCatalogPage, error) {
	limit := scopePageLimit(req.Limit)
	cursor, err := decodeCursor(req.Cursor, scopeCatalogCursor, "", "", "")
	if err != nil {
		return nil, err
	}

	var total int
	if err := r.QueryRowContext(ctx, `SELECT COUNT(*) FROM scopes`).Scan(&total); err != nil {
		return nil, fmt.Errorf("count scopes: %w", err)
	}

	completed, err := completedStatuses(ctx, r)
	if err != nil {
		return nil, err
	}
	statusPlaceholders, statusArgs := inArgs(sortedStrings(completed))
	where := ""
	args := make([]any, 0, len(statusArgs)+4)
	args = append(args, statusArgs...)
	if cursor != nil {
		where = "WHERE (s.created_on > ? OR (s.created_on = ? AND s.id > ?))"
		args = append(args, cursor.CreatedOn, cursor.CreatedOn, cursor.ID)
	}
	args = append(args, limit+1)
	rows, err := r.QueryContext(ctx, fmt.Sprintf(`
		SELECT s.id, s.name, s.normalized_name, s.created_on,
		       COUNT(sm.issue_id),
		       COALESCE(SUM(CASE WHEN i.status IN (%s) THEN 1 ELSE 0 END), 0)
		FROM scopes s
		LEFT JOIN scope_members sm ON sm.scope_id = s.id
		LEFT JOIN issues i ON i.id = sm.issue_id
		%s
		GROUP BY s.id, s.name, s.normalized_name, s.created_on
		ORDER BY s.created_on ASC, s.id ASC
		LIMIT ?`, statusPlaceholders, where), args...)
	if err != nil {
		return nil, fmt.Errorf("list scope catalog: %w", err)
	}
	defer rows.Close()
	items := make([]*storage.ScopeCatalogRow, 0, limit)
	for rows.Next() {
		var row storage.ScopeCatalogRow
		if err := rows.Scan(&row.ID, &row.Name, &row.NormalizedName, &row.CreatedOn, &row.MemberCount, &row.CompletedCount); err != nil {
			return nil, fmt.Errorf("scan scope catalog: %w", err)
		}
		items = append(items, &row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read scope catalog: %w", err)
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	page := &storage.ScopeCatalogPage{
		Items: items, Limit: limit, ReturnedCount: len(items), TotalMatching: total, HasMore: hasMore,
	}
	if hasMore {
		last := items[len(items)-1]
		page.NextCursor, err = encodeCursor(scopeCatalogCursor, "", "", "", last.CreatedOn, last.ID)
		if err != nil {
			return nil, err
		}
	}
	return page, nil
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

// ListMembers returns full issue rows after applying every member predicate.
// The scope has a deliberate 100-row ceiling, so filtering in Go keeps the
// status/category and global-readiness rules identical on both SQL backends.
func ListMembers(ctx context.Context, r Runner, scopeID string, req storage.ScopeMemberPageRequest) (*storage.ScopeMemberPage, error) {
	if scopeID == "" {
		return nil, storage.ErrScopeNotFound
	}
	scope, err := getScope(ctx, r, scopeID)
	if err != nil {
		return nil, err
	}
	if err := validateMemberStatus(req.Status); err != nil {
		return nil, err
	}
	contexts := normalizeScopeContexts(req.Contexts)
	cursor, err := decodeCursor(req.Cursor, scopeMembersCursor, scopeID, string(req.Status), string(req.Type), contexts)
	if err != nil {
		return nil, err
	}
	completed, err := completedStatuses(ctx, r)
	if err != nil {
		return nil, err
	}

	rows, err := r.QueryContext(ctx, `
		SELECT issue_id FROM scope_members WHERE scope_id = ? ORDER BY issue_id`, scopeID)
	if err != nil {
		return nil, fmt.Errorf("list scope members: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan scope member: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close scope members: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read scope members: %w", err)
	}
	if len(contexts) > 0 {
		labels, err := issueops.GetLabelsForIssuesInTx(ctx, r, ids)
		if err != nil {
			return nil, fmt.Errorf("read scope member contexts: %w", err)
		}
		filtered := ids[:0]
		for _, id := range ids {
			if matchesScopeContext(labels[id], contexts) {
				filtered = append(filtered, id)
			}
		}
		ids = filtered
	}
	readyIDs := map[string]struct{}{}
	if req.Status == storage.ScopeMemberStatusReady {
		readyIDs, err = issueops.GetReadyIssueIDsInTx(ctx, r, types.WorkFilter{Type: string(req.Type)}, ids)
		if err != nil {
			return nil, fmt.Errorf("read ready scope members: %w", err)
		}
	}
	var members []*types.Issue
	for _, id := range ids {
		issue, err := issueops.GetIssueInTx(ctx, r, id)
		if err != nil {
			return nil, fmt.Errorf("read scope %s member %s: %w", scopeID, id, err)
		}
		if req.Type != "" && issue.IssueType != req.Type {
			continue
		}
		isCompleted := completed[string(issue.Status)]
		matches := true
		switch req.Status {
		case storage.ScopeMemberStatusOpen:
			matches = !isCompleted
		case storage.ScopeMemberStatusCompleted:
			matches = isCompleted
		case storage.ScopeMemberStatusReady:
			_, matches = readyIDs[issue.ID]
		}
		if matches {
			members = append(members, issue)
		}
	}
	memberCount, completedCount, err := countScopeMembers(ctx, r, scopeID, completed)
	if err != nil {
		return nil, err
	}
	sort.Slice(members, func(i, j int) bool { return members[i].ID < members[j].ID })
	total := len(members)
	if cursor != nil {
		start := sort.Search(len(members), func(i int) bool { return members[i].ID > cursor.ID })
		members = members[start:]
	}
	limit := scopePageLimit(req.Limit)
	hasMore := len(members) > limit
	if hasMore {
		members = members[:limit]
	}
	page := &storage.ScopeMemberPage{
		Scope: *scope, Members: members, MemberCount: memberCount, CompletedCount: completedCount,
		TotalMatching: total, Limit: limit, ReturnedCount: len(members), HasMore: hasMore,
	}
	if hasMore {
		last := members[len(members)-1]
		page.NextCursor, err = encodeCursor(scopeMembersCursor, scopeID, string(req.Status), string(req.Type), time.Time{}, last.ID, contexts)
		if err != nil {
			return nil, err
		}
	}
	return page, nil
}

type scopeCursor struct {
	Version   int       `json:"v"`
	Kind      string    `json:"k"`
	ScopeID   string    `json:"s,omitempty"`
	Status    string    `json:"st,omitempty"`
	IssueType string    `json:"t,omitempty"`
	Contexts  []string  `json:"cx,omitempty"`
	CreatedOn time.Time `json:"c,omitempty"`
	ID        string    `json:"i"`
}

func encodeCursor(kind, scopeID, status, issueType string, createdOn time.Time, id string, contexts ...[]string) (string, error) {
	payload, err := json.Marshal(scopeCursor{
		Version: scopeCursorVersion, Kind: kind, ScopeID: scopeID, Status: status,
		IssueType: issueType, Contexts: cursorContexts(contexts...), CreatedOn: createdOn, ID: id,
	})
	if err != nil {
		return "", fmt.Errorf("encode scope cursor: %w", err)
	}
	return kind + base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeCursor(token, kind, scopeID, status, issueType string, contexts ...[]string) (*scopeCursor, error) {
	if token == "" {
		return nil, nil
	}
	if !strings.HasPrefix(token, kind) {
		return nil, storage.ErrScopeCursorInvalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, kind))
	if err != nil {
		return nil, storage.ErrScopeCursorInvalid
	}
	var cursor scopeCursor
	if err := json.Unmarshal(payload, &cursor); err != nil || cursor.Version != scopeCursorVersion || cursor.Kind != kind || cursor.ID == "" {
		return nil, storage.ErrScopeCursorInvalid
	}
	if cursor.ScopeID != scopeID || cursor.Status != status || cursor.IssueType != issueType || !sameStrings(cursor.Contexts, cursorContexts(contexts...)) {
		return nil, storage.ErrScopeCursorInvalid
	}
	return &cursor, nil
}

func cursorContexts(contexts ...[]string) []string {
	if len(contexts) == 0 {
		return nil
	}
	return normalizeScopeContexts(contexts[0])
}

func normalizeScopeContexts(contexts []string) []string {
	if len(contexts) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(contexts))
	result := make([]string, 0, len(contexts))
	for _, context := range contexts {
		if _, ok := seen[context]; ok {
			continue
		}
		seen[context] = struct{}{}
		result = append(result, context)
	}
	sort.Strings(result)
	return result
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func matchesScopeContext(labels, contexts []string) bool {
	for _, context := range contexts {
		if !strings.HasPrefix(context, "ctx:") {
			context = "ctx:" + context
		}
		for _, label := range labels {
			if label == context {
				return true
			}
		}
	}
	return false
}

func scopePageLimit(limit int) int {
	if limit <= 0 {
		return defaultScopePageLimit
	}
	if limit > maxScopePageLimit {
		return maxScopePageLimit
	}
	return limit
}

func validateMemberStatus(status storage.ScopeMemberStatus) error {
	switch status {
	case "", storage.ScopeMemberStatusOpen, storage.ScopeMemberStatusCompleted, storage.ScopeMemberStatusReady:
		return nil
	default:
		return fmt.Errorf("%w: unknown scope member status %q", storage.ErrScopeInvalid, status)
	}
}

func completedStatuses(ctx context.Context, r Runner) (map[string]bool, error) {
	statuses, err := issueops.ResolveCustomStatusesDetailedInTx(ctx, r)
	if err != nil {
		return nil, fmt.Errorf("resolve scope completion statuses: %w", err)
	}
	completed := map[string]bool{string(types.StatusClosed): true}
	for _, status := range statuses {
		if status.Category == types.CategoryDone {
			completed[status.Name] = true
		}
	}
	return completed, nil
}

func sortedStrings(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func countScopeMembers(ctx context.Context, r Runner, scopeID string, completed map[string]bool) (int, int, error) {
	placeholders, args := inArgs(sortedStrings(completed))
	var memberCount, completedCount int
	err := r.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT COUNT(*), COALESCE(SUM(CASE WHEN i.status IN (%s) THEN 1 ELSE 0 END), 0)
		FROM scope_members sm JOIN issues i ON i.id = sm.issue_id
		WHERE sm.scope_id = ?`, placeholders), append(args, scopeID)...).Scan(&memberCount, &completedCount)
	if err != nil {
		return 0, 0, fmt.Errorf("count scope members: %w", err)
	}
	return memberCount, completedCount, nil
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
