//go:build cgo

package embeddeddolt

import (
	"context"
	"database/sql"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/scopeops"
	"github.com/steveyegge/beads/internal/types"
)

var _ storage.ScopeStore = (*EmbeddedDoltStore)(nil)

func (s *EmbeddedDoltStore) CreateScope(ctx context.Context, scope *types.Scope, activate bool) error {
	return s.runScopeWrite(ctx, "bd: create scope", func(tx *embeddedTransaction) error {
		return tx.CreateScope(ctx, scope, activate)
	})
}

func (s *EmbeddedDoltStore) ListScopes(ctx context.Context) ([]*types.Scope, error) {
	var result []*types.Scope
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		var err error
		result, err = scopeops.List(ctx, tx)
		return err
	})
	return result, err
}

func (s *EmbeddedDoltStore) ListScopeCatalog(ctx context.Context, req storage.ScopeCatalogRequest) (*storage.ScopeCatalogPage, error) {
	var result *storage.ScopeCatalogPage
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		var err error
		result, err = scopeops.ListCatalog(ctx, tx, req)
		return err
	})
	return result, err
}

func (s *EmbeddedDoltStore) GetScope(ctx context.Context, id string) (*types.ScopeDetails, error) {
	var result *types.ScopeDetails
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		var err error
		result, err = scopeops.Get(ctx, tx, id)
		return err
	})
	return result, err
}

func (s *EmbeddedDoltStore) ListScopeMembers(ctx context.Context, scopeID string, req storage.ScopeMemberPageRequest) (*storage.ScopeMemberPage, error) {
	var result *storage.ScopeMemberPage
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		var err error
		result, err = scopeops.ListMembers(ctx, tx, scopeID, req)
		return err
	})
	return result, err
}

func (s *EmbeddedDoltStore) GetActiveScope(ctx context.Context) (*types.Scope, error) {
	var result *types.Scope
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		var err error
		result, err = scopeops.Active(ctx, tx)
		return err
	})
	return result, err
}

func (s *EmbeddedDoltStore) ActivateScope(ctx context.Context, id string) error {
	return s.runScopeWrite(ctx, "bd: activate scope", func(tx *embeddedTransaction) error {
		return tx.ActivateScope(ctx, id)
	})
}

func (s *EmbeddedDoltStore) DeactivateScope(ctx context.Context) error {
	return s.runScopeWrite(ctx, "bd: deactivate scope", func(tx *embeddedTransaction) error {
		return tx.DeactivateScope(ctx)
	})
}

func (s *EmbeddedDoltStore) AddScopeMembers(ctx context.Context, scopeID string, issueIDs []string) error {
	return s.runScopeWrite(ctx, "bd: add scope members", func(tx *embeddedTransaction) error {
		return tx.AddScopeMembers(ctx, scopeID, issueIDs)
	})
}

func (s *EmbeddedDoltStore) RemoveScopeMembers(ctx context.Context, scopeID string, issueIDs []string) error {
	return s.runScopeWrite(ctx, "bd: remove scope members", func(tx *embeddedTransaction) error {
		return tx.RemoveScopeMembers(ctx, scopeID, issueIDs)
	})
}

func (s *EmbeddedDoltStore) MoveScopeMembers(ctx context.Context, sourceID, targetID string, issueIDs []string) error {
	return s.runScopeWrite(ctx, "bd: move scope members", func(tx *embeddedTransaction) error {
		return tx.MoveScopeMembers(ctx, sourceID, targetID, issueIDs)
	})
}

func (s *EmbeddedDoltStore) runScopeWrite(ctx context.Context, commitMsg string, fn func(*embeddedTransaction) error) error {
	return s.runTransactionWithMessage(ctx, func(tx *embeddedTransaction) (string, error) {
		return commitMsg, fn(tx)
	})
}
