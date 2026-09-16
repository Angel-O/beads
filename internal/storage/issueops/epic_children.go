package issueops

import (
	"context"
	"fmt"
	"sort"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// GetEpicChildrenInTx returns the direct child rows used by the close gate.
// The target plane is selected exactly as CloseIssueCheckedInTx selects it:
// issues wins when the parent ID is present in both tables. The wisp copy of a
// promoted dependency is excluded by dependency row ID, matching
// countOpenChildrenForTargetInTx. Results include both closed and open rows and
// are deliberately unscoped.
func GetEpicChildrenInTx(ctx context.Context, tx DBTX, parentID string) ([]*types.EpicChild, error) {
	_, targetColumn, found, err := isClosedInTx(ctx, tx, parentID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("%w: issue %s", storage.ErrNotFound, parentID)
	}

	var children []epicChildRow
	if err := readEpicChildrenFromTable(ctx, tx, "dependencies", "issues", targetColumn, parentID, "durable", "", &children); err != nil {
		return nil, err
	}
	if err := readEpicChildrenFromTable(ctx, tx, "wisp_dependencies", "wisps", targetColumn, parentID, "wisp", " AND NOT EXISTS (SELECT 1 FROM dependencies AS durable WHERE durable.id = dependency.id)", &children); err != nil && !(optionalBlockedTable("wisp_dependencies") && isTableNotExistError(err)) {
		return nil, err
	}

	sort.Slice(children, func(i, j int) bool {
		if children[i].child.ID != children[j].child.ID {
			return children[i].child.ID < children[j].child.ID
		}
		if children[i].plane != children[j].plane {
			return children[i].plane < children[j].plane
		}
		return children[i].dependencyID < children[j].dependencyID
	})
	return epicChildResults(children), nil
}

type epicChildRow struct {
	dependencyID string
	plane        string
	child        types.EpicChild
}

//nolint:gosec // G201: table and targetColumn are fixed identifiers validated by callers.
func readEpicChildrenFromTable(ctx context.Context, tx DBTX, dependencyTable, childTable, targetColumn, parentID, plane, extraWhere string, out *[]epicChildRow) error {
	if targetColumn != "depends_on_issue_id" && targetColumn != "depends_on_wisp_id" {
		return fmt.Errorf("get epic children: unsupported target column %q", targetColumn)
	}
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`
		SELECT dependency.id, child.id, child.status, child.issue_type
		FROM %s AS dependency
		JOIN %s AS child ON child.id = dependency.issue_id
		WHERE dependency.%s = ?
		  AND dependency.type = 'parent-child'
		  %s
	`, dependencyTable, childTable, targetColumn, extraWhere), parentID)
	if err != nil {
		return fmt.Errorf("get epic children from %s: %w", dependencyTable, err)
	}
	defer rows.Close()
	for rows.Next() {
		var row epicChildRow
		if err := rows.Scan(&row.dependencyID, &row.child.ID, &row.child.Status, &row.child.IssueType); err != nil {
			return fmt.Errorf("scan epic child from %s: %w", dependencyTable, err)
		}
		row.plane = plane
		row.child.StoragePlane = plane
		*out = append(*out, row)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read epic children from %s: %w", dependencyTable, err)
	}
	return nil
}

func epicChildResults(rows []epicChildRow) []*types.EpicChild {
	result := make([]*types.EpicChild, 0, len(rows))
	for _, row := range rows {
		child := row.child
		result = append(result, &child)
	}
	return result
}
