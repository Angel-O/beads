// Package v1 defines the first versioned acquisition seam for Memory Beads.
//
// Acquire is the one entry point for both storage-backed and proxied callers.
// Providers opt in by implementing Source; the legacy memoryops.Memories and
// storage interfaces are deliberately not part of this contract.
//
// This package is currently a spike surface and must not be released as v1.
// Module exposes only a storage-neutral descriptor so acquisition, routing,
// and compatibility can be proved independently. Phase 2 must define the
// complete operational v1 interface before promotion, or choose a later
// version; a released interface is never widened in place.
package v1
