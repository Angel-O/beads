package postgres

import (
	"github.com/steveyegge/beads/internal/storage"
)

// Regeneration: the postgres shell is the exact complement of *sqlkit.Store's
// method set. Regenerate unsupported_gen.go with the directive below after
// finalizing the skip list (the sqlkit-implemented ops — the integrator fills
// in <ops>). gen's strict unmatched-skip validation then doubles as a drift
// tripwire against DoltStorage interface changes.
//
//go:generate go run ../uowstore/gen -pkg postgres -src .. -out unsupported_gen.go -type DoltStorage -skip <ops>

// errUnsupported is the constructor every generated stub in unsupported_gen.go
// calls. It returns the backend-agnostic *storage.ErrUnsupported sentinel, whose
// Error() renders `operation %q not supported by the %s backend`. That message is
// correct for every stubbed op — the shell also stubs non-VC core methods
// (GetStatistics, DeleteIssues, ...) that a Dolt backend is not the remedy for —
// so it carries no history/version-control hint. Backend is the package const
// "postgres"; callers can errors.As/Is on the returned sentinel.
func errUnsupported(op string) error {
	return &storage.ErrUnsupported{Op: op, Backend: Backend}
}
