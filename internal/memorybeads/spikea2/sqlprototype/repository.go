package sqlprototype

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	a2 "github.com/steveyegge/beads/internal/memorybeads/spikea2"
)

const (
	// prototypeNamespace is a schema discriminator, not an instance identity.
	// The physical repository plus Project ID is the durable authority key.
	prototypeNamespace = "memory-a2-prototype-v1"
	revisionsTable     = "memory_a2_spike_revisions"
	headsTable         = "memory_a2_spike_heads"
	viewsTable         = "memory_a2_spike_views"
)

// TableNames returns the throwaway tables a direct adapter must mark dirty.
// It is fixture plumbing, not a production storage contract.
func TableNames() []string {
	return []string{revisionsTable, headsTable, viewsTable}
}

// Install creates only the fixture schema used by this spike. Callers install
// it inside their real provider publication boundary, and no migration refers
// to these tables.
func Install(ctx context.Context, session Session) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS memory_a2_spike_revisions (
			fixture_namespace VARCHAR(64) NOT NULL,
			project_id VARCHAR(128) NOT NULL,
			bead_id VARCHAR(128) NOT NULL,
			revision_id VARCHAR(128) NOT NULL,
			payload_json LONGTEXT NOT NULL,
			PRIMARY KEY (fixture_namespace, project_id, bead_id, revision_id)
		)`,
		`CREATE TABLE IF NOT EXISTS memory_a2_spike_heads (
			fixture_namespace VARCHAR(64) NOT NULL,
			project_id VARCHAR(128) NOT NULL,
			view_id VARCHAR(64) NOT NULL,
			bead_id VARCHAR(128) NOT NULL,
			revision_id VARCHAR(128) NOT NULL,
			PRIMARY KEY (fixture_namespace, project_id, view_id, bead_id, revision_id)
		)`,
		`CREATE TABLE IF NOT EXISTS memory_a2_spike_views (
			fixture_namespace VARCHAR(64) NOT NULL,
			project_id VARCHAR(128) NOT NULL,
			view_id VARCHAR(64) NOT NULL,
			PRIMARY KEY (fixture_namespace, project_id, view_id)
		)`,
	}
	for _, statement := range statements {
		if _, err := session.Exec(ctx, statement); err != nil {
			return fmt.Errorf("install A2 SQL prototype schema: %w", err)
		}
	}
	return nil
}

type storedRevision struct {
	Revision   a2.Revision              `json:"revision"`
	LineBlame  []a2.RevisionID          `json:"line_blame"`
	FieldBlame map[string]a2.RevisionID `json:"field_blame"`
}

type repository struct {
	namespace string
	projectID a2.ProjectID
}

func (r repository) ensureView(ctx context.Context, session Session, view string) error {
	if view == "" {
		return fmt.Errorf("view ID is required")
	}
	if _, err := session.Exec(ctx, `
		INSERT IGNORE INTO memory_a2_spike_views
			(fixture_namespace, project_id, view_id)
		VALUES (?, ?, ?)`, r.namespace, r.projectID, view); err != nil {
		return fmt.Errorf("register A2 view %q: %w", view, err)
	}
	return nil
}

func (r repository) viewExists(ctx context.Context, session Session, view string) (bool, error) {
	rows, err := session.Query(ctx, `
		SELECT view_id
		FROM memory_a2_spike_views
		WHERE fixture_namespace = ? AND project_id = ? AND view_id = ?`,
		r.namespace, r.projectID, view)
	if err != nil {
		return false, fmt.Errorf("read A2 view %q: %w", view, err)
	}
	if len(rows) > 1 {
		return false, fmt.Errorf("read A2 view %q: got %d rows, want at most 1", view, len(rows))
	}
	if len(rows) == 0 {
		return false, nil
	}
	if len(rows[0]) != 1 {
		return false, fmt.Errorf("read A2 view %q: got %d columns, want 1", view, len(rows[0]))
	}
	return true, nil
}

func (r repository) heads(ctx context.Context, session Session, view string, beadID a2.BeadID) ([]a2.RevisionID, error) {
	rows, err := session.Query(ctx, `
		SELECT revision_id
		FROM memory_a2_spike_heads
		WHERE fixture_namespace = ? AND project_id = ? AND view_id = ? AND bead_id = ?
		ORDER BY revision_id`, r.namespace, r.projectID, view, beadID)
	if err != nil {
		return nil, fmt.Errorf("read A2 current heads: %w", err)
	}
	heads := make([]a2.RevisionID, 0, len(rows))
	for _, row := range rows {
		if len(row) != 1 {
			return nil, fmt.Errorf("read A2 current heads: got %d columns, want 1", len(row))
		}
		value, err := textValue(row[0])
		if err != nil {
			return nil, fmt.Errorf("read A2 current head: %w", err)
		}
		heads = append(heads, a2.RevisionID(value))
	}
	return heads, nil
}

func (r repository) revision(ctx context.Context, session Session, beadID a2.BeadID, revisionID a2.RevisionID) (*storedRevision, error) {
	rows, err := session.Query(ctx, `
		SELECT payload_json
		FROM memory_a2_spike_revisions
		WHERE fixture_namespace = ? AND project_id = ? AND bead_id = ? AND revision_id = ?`,
		r.namespace, r.projectID, beadID, revisionID)
	if err != nil {
		return nil, fmt.Errorf("read A2 exact revision: %w", err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("%w: bead %q revision %q", a2.ErrNotFound, beadID, revisionID)
	}
	if len(rows) != 1 || len(rows[0]) != 1 {
		return nil, fmt.Errorf("read A2 exact revision: got %d rows", len(rows))
	}
	payload, err := textValue(rows[0][0])
	if err != nil {
		return nil, fmt.Errorf("read A2 exact revision payload: %w", err)
	}
	var stored storedRevision
	if err := json.Unmarshal([]byte(payload), &stored); err != nil {
		return nil, fmt.Errorf("decode A2 exact revision: %w", err)
	}
	return &stored, nil
}

func (r repository) selectRevision(ctx context.Context, session Session, view string, beadID a2.BeadID, revisionID a2.RevisionID) (*storedRevision, error) {
	if beadID == "" {
		return nil, fmt.Errorf("%w: bead ID is required", a2.ErrInvalid)
	}
	if revisionID == "" {
		heads, err := r.heads(ctx, session, view, beadID)
		if err != nil {
			return nil, err
		}
		switch len(heads) {
		case 0:
			return nil, fmt.Errorf("%w: bead %q", a2.ErrNotFound, beadID)
		case 1:
			revisionID = heads[0]
		default:
			return nil, newConflictError(beadID, heads)
		}
	}
	return r.revision(ctx, session, beadID, revisionID)
}

func (r repository) insertRevision(ctx context.Context, session Session, stored storedRevision) error {
	payload, err := json.Marshal(stored)
	if err != nil {
		return fmt.Errorf("encode A2 revision: %w", err)
	}
	address := stored.Revision.Address
	if _, err := session.Exec(ctx, `
		INSERT INTO memory_a2_spike_revisions
			(fixture_namespace, project_id, bead_id, revision_id, payload_json)
		VALUES (?, ?, ?, ?, ?)`,
		r.namespace, r.projectID, address.BeadID, address.RevisionID, string(payload)); err != nil {
		return fmt.Errorf("append A2 revision: %w", err)
	}
	return nil
}

func (r repository) replaceHead(ctx context.Context, session Session, view string, beadID a2.BeadID, revisionID a2.RevisionID) error {
	if _, err := session.Exec(ctx, `
		DELETE FROM memory_a2_spike_heads
		WHERE fixture_namespace = ? AND project_id = ? AND view_id = ? AND bead_id = ?`,
		r.namespace, r.projectID, view, beadID); err != nil {
		return fmt.Errorf("remove A2 current head: %w", err)
	}
	if _, err := session.Exec(ctx, `
		INSERT INTO memory_a2_spike_heads
			(fixture_namespace, project_id, view_id, bead_id, revision_id)
		VALUES (?, ?, ?, ?, ?)`,
		r.namespace, r.projectID, view, beadID, revisionID); err != nil {
		return fmt.Errorf("publish A2 current head: %w", err)
	}
	return nil
}

func (r repository) revisions(ctx context.Context, session Session, beadID a2.BeadID) ([]storedRevision, error) {
	rows, err := session.Query(ctx, `
		SELECT payload_json
		FROM memory_a2_spike_revisions
		WHERE fixture_namespace = ? AND project_id = ? AND bead_id = ?
		ORDER BY revision_id`, r.namespace, r.projectID, beadID)
	if err != nil {
		return nil, fmt.Errorf("list A2 revisions: %w", err)
	}
	result := make([]storedRevision, 0, len(rows))
	for _, row := range rows {
		if len(row) != 1 {
			return nil, fmt.Errorf("list A2 revisions: got %d columns, want 1", len(row))
		}
		payload, err := textValue(row[0])
		if err != nil {
			return nil, err
		}
		var stored storedRevision
		if err := json.Unmarshal([]byte(payload), &stored); err != nil {
			return nil, fmt.Errorf("decode A2 revision list entry: %w", err)
		}
		result = append(result, stored)
	}
	return result, nil
}

func (r repository) current(ctx context.Context, session Session, view string) (map[a2.BeadID][]a2.RevisionID, error) {
	rows, err := session.Query(ctx, `
		SELECT bead_id, revision_id
		FROM memory_a2_spike_heads
		WHERE fixture_namespace = ? AND project_id = ? AND view_id = ?
		ORDER BY bead_id, revision_id`, r.namespace, r.projectID, view)
	if err != nil {
		return nil, fmt.Errorf("list A2 current heads: %w", err)
	}
	result := make(map[a2.BeadID][]a2.RevisionID)
	for _, row := range rows {
		if len(row) != 2 {
			return nil, fmt.Errorf("list A2 current heads: got %d columns, want 2", len(row))
		}
		bead, err := textValue(row[0])
		if err != nil {
			return nil, err
		}
		revision, err := textValue(row[1])
		if err != nil {
			return nil, err
		}
		id := a2.BeadID(bead)
		result[id] = append(result[id], a2.RevisionID(revision))
	}
	return result, nil
}

func (r repository) copyHeads(ctx context.Context, session Session, sourceView, targetView string) error {
	if err := r.ensureView(ctx, session, sourceView); err != nil {
		return err
	}
	if err := r.ensureView(ctx, session, targetView); err != nil {
		return err
	}
	_, err := session.Exec(ctx, `
		INSERT INTO memory_a2_spike_heads
			(fixture_namespace, project_id, view_id, bead_id, revision_id)
		SELECT fixture_namespace, project_id, ?, bead_id, revision_id
		FROM memory_a2_spike_heads
		WHERE fixture_namespace = ? AND project_id = ? AND view_id = ?`,
		targetView, r.namespace, r.projectID, sourceView)
	return err
}

func (r repository) deleteView(ctx context.Context, session Session, view string) error {
	if _, err := session.Exec(ctx, `
		DELETE FROM memory_a2_spike_heads
		WHERE fixture_namespace = ? AND project_id = ? AND view_id = ?`,
		r.namespace, r.projectID, view); err != nil {
		return err
	}
	_, err := session.Exec(ctx, `
		DELETE FROM memory_a2_spike_views
		WHERE fixture_namespace = ? AND project_id = ? AND view_id = ?`,
		r.namespace, r.projectID, view)
	return err
}

func (r repository) setHeads(ctx context.Context, session Session, view string, beadID a2.BeadID, revisions []a2.RevisionID) error {
	if _, err := session.Exec(ctx, `
		DELETE FROM memory_a2_spike_heads
		WHERE fixture_namespace = ? AND project_id = ? AND view_id = ? AND bead_id = ?`,
		r.namespace, r.projectID, view, beadID); err != nil {
		return err
	}
	for _, revision := range revisions {
		if _, err := session.Exec(ctx, `
			INSERT INTO memory_a2_spike_heads
				(fixture_namespace, project_id, view_id, bead_id, revision_id)
			VALUES (?, ?, ?, ?, ?)`, r.namespace, r.projectID, view, beadID, revision); err != nil {
			return err
		}
	}
	return nil
}

func textValue(value any) (string, error) {
	switch typed := value.(type) {
	case string:
		return typed, nil
	case []byte:
		return string(typed), nil
	default:
		return "", fmt.Errorf("SQL value has type %T, want text", value)
	}
}

func newConflictError(beadID a2.BeadID, heads []a2.RevisionID) *a2.ConflictError {
	copyOfHeads := append([]a2.RevisionID(nil), heads...)
	sort.Slice(copyOfHeads, func(i, j int) bool { return copyOfHeads[i] < copyOfHeads[j] })
	return &a2.ConflictError{BeadID: beadID, Heads: copyOfHeads}
}

func isSemanticRejection(err error) bool {
	if err == nil {
		return false
	}
	var stale *a2.StaleError
	var conflict *a2.ConflictError
	return errors.Is(err, a2.ErrInvalid) || errors.Is(err, a2.ErrNotFound) ||
		errors.As(err, &stale) || errors.As(err, &conflict)
}
