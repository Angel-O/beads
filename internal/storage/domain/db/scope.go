package db

import (
	"context"

	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/storage/scopeops"
	"github.com/steveyegge/beads/internal/types"
)

func NewScopeSQLRepository(runner Runner) domain.ScopeSQLRepository {
	return &scopeSQLRepositoryImpl{runner: runner}
}

type scopeSQLRepositoryImpl struct{ runner Runner }

var _ domain.ScopeSQLRepository = (*scopeSQLRepositoryImpl)(nil)

func (r *scopeSQLRepositoryImpl) Create(ctx context.Context, scope *types.Scope, activate bool) error {
	return scopeops.Create(ctx, r.runner, scope, activate)
}
func (r *scopeSQLRepositoryImpl) List(ctx context.Context) ([]*types.Scope, error) {
	return scopeops.List(ctx, r.runner)
}
func (r *scopeSQLRepositoryImpl) ListCatalog(ctx context.Context, req types.ScopeCatalogRequest) (*types.ScopeCatalogPage, error) {
	return scopeops.ListCatalog(ctx, r.runner, req)
}
func (r *scopeSQLRepositoryImpl) Get(ctx context.Context, id string) (*types.ScopeDetails, error) {
	return scopeops.Get(ctx, r.runner, id)
}
func (r *scopeSQLRepositoryImpl) ListMembers(ctx context.Context, scopeID string, req types.ScopeMemberPageRequest) (*types.ScopeMemberPage, error) {
	return scopeops.ListMembers(ctx, r.runner, scopeID, req)
}
func (r *scopeSQLRepositoryImpl) Active(ctx context.Context) (*types.Scope, error) {
	return scopeops.Active(ctx, r.runner)
}
func (r *scopeSQLRepositoryImpl) Activate(ctx context.Context, id string) error {
	return scopeops.Activate(ctx, r.runner, id)
}
func (r *scopeSQLRepositoryImpl) Deactivate(ctx context.Context) error {
	return scopeops.Deactivate(ctx, r.runner)
}
func (r *scopeSQLRepositoryImpl) AddMembers(ctx context.Context, scopeID string, issueIDs []string) error {
	return scopeops.AddMembers(ctx, r.runner, scopeID, issueIDs)
}
func (r *scopeSQLRepositoryImpl) RemoveMembers(ctx context.Context, scopeID string, issueIDs []string) error {
	return scopeops.RemoveMembers(ctx, r.runner, scopeID, issueIDs)
}
func (r *scopeSQLRepositoryImpl) MoveMembers(ctx context.Context, sourceID, targetID string, issueIDs []string) error {
	return scopeops.MoveMembers(ctx, r.runner, sourceID, targetID, issueIDs)
}
