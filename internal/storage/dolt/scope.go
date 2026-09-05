package dolt

import (
	"context"
	"database/sql"

	"github.com/steveyegge/beads/internal/storage"
	storageissueops "github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/storage/scopeops"
	"github.com/steveyegge/beads/internal/types"
)

var _ storage.ScopeStore = (*DoltStore)(nil)

func (s *DoltStore) CreateScope(ctx context.Context, scope *types.Scope, activate bool) error {
	return s.withCircuitWrite(ctx, func(ctx context.Context) error {
		return s.runIssueOperationTx(ctx, "bd: create scope", func(tx *sql.Tx) (storageissueops.ChangedTables, error) {
			if err := scopeops.Create(ctx, tx, scope, activate); err != nil {
				return nil, err
			}
			return scopeTables(), nil
		})
	})
}

func (s *DoltStore) ListScopes(ctx context.Context) ([]*types.Scope, error) {
	var result []*types.Scope
	err := s.withReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		result, err = scopeops.List(ctx, tx)
		return err
	})
	return result, err
}

func (s *DoltStore) GetScope(ctx context.Context, id string) (*types.ScopeDetails, error) {
	var result *types.ScopeDetails
	err := s.withReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		result, err = scopeops.Get(ctx, tx, id)
		return err
	})
	return result, err
}

func (s *DoltStore) GetActiveScope(ctx context.Context) (*types.Scope, error) {
	var result *types.Scope
	err := s.withReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		result, err = scopeops.Active(ctx, tx)
		return err
	})
	return result, err
}

func (s *DoltStore) ActivateScope(ctx context.Context, id string) error {
	return s.withCircuitWrite(ctx, func(ctx context.Context) error {
		return s.runIssueOperationTx(ctx, "bd: activate scope", func(tx *sql.Tx) (storageissueops.ChangedTables, error) {
			if err := scopeops.Activate(ctx, tx, id); err != nil {
				return nil, err
			}
			return scopeTables(), nil
		})
	})
}

func (s *DoltStore) DeactivateScope(ctx context.Context) error {
	return s.withCircuitWrite(ctx, func(ctx context.Context) error {
		return s.runIssueOperationTx(ctx, "bd: deactivate scope", func(tx *sql.Tx) (storageissueops.ChangedTables, error) {
			if err := scopeops.Deactivate(ctx, tx); err != nil {
				return nil, err
			}
			return scopeTables(), nil
		})
	})
}

func (s *DoltStore) AddScopeMembers(ctx context.Context, scopeID string, issueIDs []string) error {
	return s.withCircuitWrite(ctx, func(ctx context.Context) error {
		return s.runIssueOperationTx(ctx, "bd: add scope members", func(tx *sql.Tx) (storageissueops.ChangedTables, error) {
			if err := scopeops.AddMembers(ctx, tx, scopeID, issueIDs); err != nil {
				return nil, err
			}
			return scopeTables(), nil
		})
	})
}

func (s *DoltStore) RemoveScopeMembers(ctx context.Context, scopeID string, issueIDs []string) error {
	return s.withCircuitWrite(ctx, func(ctx context.Context) error {
		return s.runIssueOperationTx(ctx, "bd: remove scope members", func(tx *sql.Tx) (storageissueops.ChangedTables, error) {
			if err := scopeops.RemoveMembers(ctx, tx, scopeID, issueIDs); err != nil {
				return nil, err
			}
			return scopeTables(), nil
		})
	})
}

func (s *DoltStore) MoveScopeMembers(ctx context.Context, sourceID, targetID string, issueIDs []string) error {
	return s.withCircuitWrite(ctx, func(ctx context.Context) error {
		return s.runIssueOperationTx(ctx, "bd: move scope members", func(tx *sql.Tx) (storageissueops.ChangedTables, error) {
			if err := scopeops.MoveMembers(ctx, tx, sourceID, targetID, issueIDs); err != nil {
				return nil, err
			}
			return scopeTables(), nil
		})
	})
}

func scopeTables() storageissueops.ChangedTables {
	return map[string]bool{"scopes": true, "scope_state": true, "scope_members": true}
}
