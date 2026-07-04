package sqlkit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
)

// CreateIssue creates a new issue. Wisp routing (ephemeral / no-history / infra
// type) is resolved inside the tx; issueops picks the wisps table from there.
func (s *Store) CreateIssue(ctx context.Context, issue *types.Issue, actor string) error {
	if issue == nil {
		return fmt.Errorf("issue must not be nil")
	}
	return s.withMutationTx(ctx, func(tx *sql.Tx) error {
		// Route to wisps if ephemeral, no-history, or an infra type. Infra types
		// get marked ephemeral (legacy behavior); TableRouting inside issueops
		// then selects the wisps table.
		useWisps := issue.Ephemeral || issue.NoHistory ||
			issueops.ResolveInfraTypesInTx(ctx, tx)[string(issue.IssueType)]
		if useWisps && !issue.NoHistory {
			issue.Ephemeral = true
		}
		// SkipPrefixValidation matches legacy: the single-issue path never
		// prefix-validates explicit IDs.
		bc, err := issueops.NewBatchContext(ctx, tx, storage.BatchCreateOptions{
			SkipPrefixValidation: true,
		})
		if err != nil {
			return err
		}
		_, err = issueops.CreateIssueInTxWithResult(ctx, tx, bc, issue, actor)
		return err
	})
}

// CreateIssues creates multiple issues in a single transaction. issueops routes
// mixed batches per issue and validates cross-bucket dependencies internally.
func (s *Store) CreateIssues(ctx context.Context, issues []*types.Issue, actor string) error {
	if len(issues) == 0 {
		return nil
	}
	return s.withMutationTx(ctx, func(tx *sql.Tx) error {
		_, err := issueops.CreateIssuesInTxWithResult(ctx, tx, issues, actor, storage.BatchCreateOptions{
			OrphanHandling:       storage.OrphanAllow,
			SkipPrefixValidation: false,
		})
		return err
	})
}

// GetIssue retrieves an issue by ID. Returns storage.ErrNotFound (wrapped) when
// the issue does not exist.
func (s *Store) GetIssue(ctx context.Context, id string) (*types.Issue, error) {
	var issue *types.Issue
	err := s.withReadTx(ctx, func(tx *sql.Tx) error {
		var e error
		issue, e = issueops.GetIssueInTx(ctx, tx, id)
		return e
	})
	return issue, err
}

// GetIssuesByIDs retrieves multiple issues by ID, spanning both the issues and
// wisps tiers.
func (s *Store) GetIssuesByIDs(ctx context.Context, ids []string) ([]*types.Issue, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var out []*types.Issue
	err := s.withReadTx(ctx, func(tx *sql.Tx) error {
		var e error
		out, e = issueops.GetIssuesByIDsInTx(ctx, tx, ids, nil)
		return e
	})
	return out, err
}

// UpdateIssue updates fields on an issue. Metadata is validated against the
// configured schema before delegation; wisp routing happens inside issueops.
func (s *Store) UpdateIssue(ctx context.Context, id string, updates map[string]interface{}, actor string) error {
	// Validate metadata against schema before delegation (GH#1416 Phase 2).
	if rawMeta, ok := updates["metadata"]; ok {
		metadataStr, err := storage.NormalizeMetadataValue(rawMeta)
		if err != nil {
			return fmt.Errorf("invalid metadata: %w", err)
		}
		if err := validateMetadataIfConfigured(json.RawMessage(metadataStr)); err != nil {
			return err
		}
	}

	// Demoting a regular issue to a wisp is a table migration in dolt; it is out
	// of scope for the wedge. An in-place column write would silently diverge.
	if _, ok := updates["no_history"]; ok {
		return fmt.Errorf("sqlkit: demoting an issue to a wisp via update is not supported")
	}
	if _, ok := updates["wisp"]; ok {
		return fmt.Errorf("sqlkit: demoting an issue to a wisp via update is not supported")
	}

	return s.withMutationTx(ctx, func(tx *sql.Tx) error {
		_, err := issueops.UpdateIssueInTx(ctx, tx, id, updates, actor)
		return err
	})
}

// UpdateIssueType changes the issue_type field of an issue. Type validation
// happens inside UpdateIssueInTx via ResolveCustomTypesInTx.
func (s *Store) UpdateIssueType(ctx context.Context, id string, issueType string, actor string) error {
	return s.UpdateIssue(ctx, id, map[string]interface{}{"issue_type": issueType}, actor)
}

// ReopenIssue reopens a closed issue: sets status=open, clears closed_at and
// defer_until, records EventReopened, adds the reason comment when non-empty,
// and recomputes is_blocked for affected IDs — all in one tx.
func (s *Store) ReopenIssue(ctx context.Context, id string, reason string, actor string) error {
	return s.withMutationTx(ctx, func(tx *sql.Tx) error {
		_, err := issueops.ReopenIssueInTx(ctx, tx, id, reason, actor)
		return err
	})
}

// DeleteIssue permanently removes an issue. issueops routes wisps internally and
// recomputes is_blocked for affected neighbors.
func (s *Store) DeleteIssue(ctx context.Context, id string) error {
	return s.withMutationTx(ctx, func(tx *sql.Tx) error {
		return issueops.DeleteIssueInTx(ctx, tx, id)
	})
}

// --- metadata schema helpers (config-only; copied from dolt/metadata_schema.go) ---

// loadMetadataSchema reads the metadata validation config from YAML and
// returns a parsed schema. Returns mode "none" with empty fields if config
// is not initialized, mode is empty/unknown, or no fields are defined.
func loadMetadataSchema() storage.MetadataSchemaConfig {
	mode := config.MetadataValidationMode()
	if mode == "none" {
		return storage.MetadataSchemaConfig{Mode: "none"}
	}

	rawFields := config.MetadataSchemaFields()
	if rawFields == nil {
		return storage.MetadataSchemaConfig{Mode: "none"}
	}

	fields := make(map[string]storage.MetadataFieldSchema)
	for name, raw := range rawFields {
		fieldMap, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		schema := parseFieldSchema(fieldMap)
		fields[name] = schema
	}

	if len(fields) == 0 {
		return storage.MetadataSchemaConfig{Mode: "none"}
	}

	return storage.MetadataSchemaConfig{
		Mode:   mode,
		Fields: fields,
	}
}

// parseFieldSchema converts a raw config map into a MetadataFieldSchema.
func parseFieldSchema(m map[string]interface{}) storage.MetadataFieldSchema {
	schema := storage.MetadataFieldSchema{}

	if t, ok := m["type"].(string); ok {
		schema.Type = storage.MetadataFieldType(t)
	}

	if req, ok := m["required"].(bool); ok {
		schema.Required = req
	}

	// Parse enum values
	if vals, ok := m["values"]; ok {
		switch v := vals.(type) {
		case []interface{}:
			for _, item := range v {
				if s, ok := item.(string); ok {
					schema.Values = append(schema.Values, s)
				}
			}
		case string:
			// Comma-separated fallback
			for _, s := range strings.Split(v, ",") {
				s = strings.TrimSpace(s)
				if s != "" {
					schema.Values = append(schema.Values, s)
				}
			}
		}
	}

	// Parse min/max for numeric types
	if min, ok := toFloat64(m["min"]); ok {
		schema.Min = &min
	}
	if max, ok := toFloat64(m["max"]); ok {
		schema.Max = &max
	}

	return schema
}

// toFloat64 converts an interface{} to float64, handling int and float YAML values.
func toFloat64(v interface{}) (float64, bool) {
	if v == nil {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

// validateMetadataIfConfigured checks metadata against the schema from config.
// In "warn" mode, prints warnings to stderr and returns nil.
// In "error" mode, returns the first validation error.
// In "none" mode (or if config is not initialized), does nothing.
func validateMetadataIfConfigured(metadata json.RawMessage) error {
	schema := loadMetadataSchema()
	if schema.Mode == "none" {
		return nil
	}

	errs := storage.ValidateMetadataSchema(metadata, schema)
	if len(errs) == 0 {
		return nil
	}

	if schema.Mode == "warn" {
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "warning: %s\n", e.Error())
		}
		return nil
	}

	// mode == "error"
	return fmt.Errorf("metadata schema violation: %s", errs[0].Error())
}
