// Package sqlprototype is a reversible SQL-backed realization of the A2
// Memory Beads contract. It exists only to test the provider boundary; its
// tables and private interfaces are not a production schema or public API.
package sqlprototype

import (
	"context"
)

// Session is the deliberately small SQL seam needed by the prototype
// repository. Provider adapters translate their real transaction/session
// shape into this interface; it never reaches the caller-facing Memory Module.
type Session interface {
	Exec(context.Context, string, ...any) (int64, error)
	Query(context.Context, string, ...any) ([][]any, error)
}

// PublicationState reports only what the transaction authority can know.
// Published means the callback's complete transaction is known to have
// committed. NotPublished means it is known not to have committed. Unknown
// means acknowledgement was lost and neither conclusion is safe.
type PublicationState uint8

const (
	NotPublished PublicationState = iota
	Published
	Unknown
)

// Publication is the private observation returned by a provider adapter.
type Publication struct {
	State PublicationState
	Err   error
}

// Adapter is the one provider-specific seam. The SQL prototype owns Memory
// semantics and repository queries; adapters own read snapshots and the real
// publication boundary.
type Adapter interface {
	Read(context.Context, func(Session) error) error
	Publish(context.Context, string, func(Session) error) Publication
}
