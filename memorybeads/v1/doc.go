// Package v1 contains the descriptor-only Memory acquisition experiment.
//
// It demonstrates that storage-backed and proxied callers can use one optional
// seam without widening the existing memoryops.Memories or storage interfaces.
// It does not select a production API, provider boundary, or operational Memory
// contract. Memory Beads is a first-class core Bead kind regardless of whether
// an implementation uses this pattern internally.
package v1
