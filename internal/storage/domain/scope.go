package domain

import (
	"context"

	"github.com/steveyegge/beads/internal/types"
)

// ScopeSQLRepository is the transaction-bound persistence seam for scopes.
// Implementations delegate invariant enforcement to the shared scope SQL body.
type ScopeSQLRepository interface {
	Create(ctx context.Context, scope *types.Scope, activate bool) error
	List(ctx context.Context) ([]*types.Scope, error)
	ListCatalog(ctx context.Context, req types.ScopeCatalogRequest) (*types.ScopeCatalogPage, error)
	Get(ctx context.Context, id string) (*types.ScopeDetails, error)
	ListMembers(ctx context.Context, scopeID string, req types.ScopeMemberPageRequest) (*types.ScopeMemberPage, error)
	Active(ctx context.Context) (*types.Scope, error)
	Activate(ctx context.Context, id string) error
	Deactivate(ctx context.Context) error
	AddMembers(ctx context.Context, scopeID string, issueIDs []string) error
	RemoveMembers(ctx context.Context, scopeID string, issueIDs []string) error
	MoveMembers(ctx context.Context, sourceID, targetID string, issueIDs []string) error
}

// ScopeUseCase is the domain seam used by a unit of work. It intentionally
// mirrors ScopeSQLRepository: the surrounding transaction owns atomicity.
type ScopeUseCase interface {
	CreateScope(ctx context.Context, scope *types.Scope, activate bool) error
	ListScopes(ctx context.Context) ([]*types.Scope, error)
	ListScopeCatalog(ctx context.Context, req types.ScopeCatalogRequest) (*types.ScopeCatalogPage, error)
	GetScope(ctx context.Context, id string) (*types.ScopeDetails, error)
	ListScopeMembers(ctx context.Context, scopeID string, req types.ScopeMemberPageRequest) (*types.ScopeMemberPage, error)
	GetActiveScope(ctx context.Context) (*types.Scope, error)
	ActivateScope(ctx context.Context, id string) error
	DeactivateScope(ctx context.Context) error
	AddScopeMembers(ctx context.Context, scopeID string, issueIDs []string) error
	RemoveScopeMembers(ctx context.Context, scopeID string, issueIDs []string) error
	MoveScopeMembers(ctx context.Context, sourceID, targetID string, issueIDs []string) error
}

func NewScopeUseCase(repo ScopeSQLRepository) ScopeUseCase {
	return &scopeUseCaseImpl{repo: repo}
}

type scopeUseCaseImpl struct{ repo ScopeSQLRepository }

var _ ScopeUseCase = (*scopeUseCaseImpl)(nil)

func (u *scopeUseCaseImpl) CreateScope(ctx context.Context, scope *types.Scope, activate bool) error {
	return u.repo.Create(ctx, scope, activate)
}
func (u *scopeUseCaseImpl) ListScopes(ctx context.Context) ([]*types.Scope, error) {
	return u.repo.List(ctx)
}
func (u *scopeUseCaseImpl) ListScopeCatalog(ctx context.Context, req types.ScopeCatalogRequest) (*types.ScopeCatalogPage, error) {
	return u.repo.ListCatalog(ctx, req)
}
func (u *scopeUseCaseImpl) GetScope(ctx context.Context, id string) (*types.ScopeDetails, error) {
	return u.repo.Get(ctx, id)
}
func (u *scopeUseCaseImpl) ListScopeMembers(ctx context.Context, scopeID string, req types.ScopeMemberPageRequest) (*types.ScopeMemberPage, error) {
	return u.repo.ListMembers(ctx, scopeID, req)
}
func (u *scopeUseCaseImpl) GetActiveScope(ctx context.Context) (*types.Scope, error) {
	return u.repo.Active(ctx)
}
func (u *scopeUseCaseImpl) ActivateScope(ctx context.Context, id string) error {
	return u.repo.Activate(ctx, id)
}
func (u *scopeUseCaseImpl) DeactivateScope(ctx context.Context) error {
	return u.repo.Deactivate(ctx)
}
func (u *scopeUseCaseImpl) AddScopeMembers(ctx context.Context, scopeID string, issueIDs []string) error {
	return u.repo.AddMembers(ctx, scopeID, issueIDs)
}
func (u *scopeUseCaseImpl) RemoveScopeMembers(ctx context.Context, scopeID string, issueIDs []string) error {
	return u.repo.RemoveMembers(ctx, scopeID, issueIDs)
}
func (u *scopeUseCaseImpl) MoveScopeMembers(ctx context.Context, sourceID, targetID string, issueIDs []string) error {
	return u.repo.MoveMembers(ctx, sourceID, targetID, issueIDs)
}
