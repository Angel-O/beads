package storage

import (
	"context"

	"github.com/steveyegge/beads/internal/types"
)

type ScopeCatalogRequest = types.ScopeCatalogRequest
type ScopeCatalogRow = types.ScopeCatalogRow
type ScopeCatalogPage = types.ScopeCatalogPage
type ScopeMemberStatus = types.ScopeMemberStatus
type ScopeMemberPageRequest = types.ScopeMemberPageRequest
type ScopeMemberPage = types.ScopeMemberPage

const (
	ScopeMemberStatusOpen      = types.ScopeMemberStatusOpen
	ScopeMemberStatusCompleted = types.ScopeMemberStatusCompleted
	ScopeMemberStatusReady     = types.ScopeMemberStatusReady
)

// ScopeStore is the storage-level scope capability. Scope membership is
// durable state, not a list-filter concern: every mutation is atomic and the
// membership methods enforce the one-scope-per-issue and 100-member rules.
type ScopeStore interface {
	CreateScope(ctx context.Context, scope *types.Scope, activate bool) error
	ListScopes(ctx context.Context) ([]*types.Scope, error)
	ListScopeCatalog(ctx context.Context, req ScopeCatalogRequest) (*ScopeCatalogPage, error)
	GetScope(ctx context.Context, id string) (*types.ScopeDetails, error)
	ListScopeMembers(ctx context.Context, scopeID string, req ScopeMemberPageRequest) (*ScopeMemberPage, error)
	GetActiveScope(ctx context.Context) (*types.Scope, error)
	ActivateScope(ctx context.Context, id string) error
	DeactivateScope(ctx context.Context) error
	AddScopeMembers(ctx context.Context, scopeID string, issueIDs []string) error
	RemoveScopeMembers(ctx context.Context, scopeID string, issueIDs []string) error
	MoveScopeMembers(ctx context.Context, sourceID, targetID string, issueIDs []string) error
}

const MaxScopeMembers = 100

var (
	ErrScopeInvalid            = newStorageError("invalid scope")
	ErrScopeAlreadyExists      = newStorageError("scope already exists")
	ErrScopeNotFound           = newStorageError("scope not found")
	ErrScopeIssueNotFound      = newStorageError("scope issue not found")
	ErrScopeMembershipConflict = newStorageError("scope membership conflict")
	ErrScopeCapacityExceeded   = newStorageError("scope capacity exceeded")
	ErrScopeSourceMembership   = newStorageError("scope source membership required")
	ErrScopeCursorInvalid      = newStorageError("invalid scope cursor")
)

type storageError string

func newStorageError(message string) error { return storageError(message) }
func (e storageError) Error() string       { return string(e) }

// ScopeMembershipConflictError identifies an issue already assigned to a
// different scope. ExistingScope is empty when the issue is unscoped.
type ScopeMembershipConflictError struct {
	IssueID        string
	ExistingScope  string
	RequestedScope string
}

func (e *ScopeMembershipConflictError) Error() string {
	return "scope membership conflict for issue " + e.IssueID
}

func (e *ScopeMembershipConflictError) Unwrap() error { return ErrScopeMembershipConflict }

// ScopeCapacityError reports a failed all-or-nothing membership operation.
type ScopeCapacityError struct {
	ScopeID   string
	Current   int
	Requested int
}

func (e *ScopeCapacityError) Error() string { return "scope capacity exceeded" }
func (e *ScopeCapacityError) Unwrap() error { return ErrScopeCapacityExceeded }

// ScopeSourceMembershipError reports an issue that is not a member of the
// source named by a move request.
type ScopeSourceMembershipError struct {
	IssueID string
	ScopeID string
}

func (e *ScopeSourceMembershipError) Error() string { return "scope source membership required" }
func (e *ScopeSourceMembershipError) Unwrap() error { return ErrScopeSourceMembership }
