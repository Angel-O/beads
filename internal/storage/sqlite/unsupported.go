package sqlite

import "github.com/steveyegge/beads/internal/storage"

//go:generate go run ../uowstore/gen -pkg sqlite -src .. -out unsupported_gen.go -type DoltStorage -skip <ops>

// errUnsupported returns the backend-agnostic *storage.ErrUnsupported sentinel; Backend
// is "sqlite". Same allowlist as postgres/mysql (identical sqlkit base + overrides).
func errUnsupported(op string) error {
	return &storage.ErrUnsupported{Op: op, Backend: Backend}
}
