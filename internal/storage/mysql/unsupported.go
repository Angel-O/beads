package mysql

import (
	"github.com/steveyegge/beads/internal/storage"
)

// Regeneration mirrors postgres: the mysql shell is the exact complement of
// *sqlkit.Store's method set (identical base + identical Commit/CommitGraph overrides,
// so the complement is identical to postgres's shell modulo package name).
//
//go:generate go run ../uowstore/gen -pkg mysql -src .. -out unsupported_gen.go -type DoltStorage -skip <ops>

// errUnsupported is the constructor every generated stub in unsupported_gen.go calls.
// It returns the backend-agnostic *storage.ErrUnsupported sentinel; Backend is "mysql".
func errUnsupported(op string) error {
	return &storage.ErrUnsupported{Op: op, Backend: Backend}
}
