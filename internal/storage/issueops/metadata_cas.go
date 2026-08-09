package issueops

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/steveyegge/beads/internal/storage"
	publicops "github.com/steveyegge/beads/issueops"
)

// CompareAndSetMetadataKeyInTx is the body behind issueops.MetadataCAS: read
// one metadata key, compare it against the plan's expectation, and write the
// object back only if they match — all on ONE transaction.
//
// It lives here rather than in an importable internal/workapi/store<role>
// package because the compare and the write must see one snapshot, and
// storage.DoltStorage publishes methods, not transactions. ALL THREE LEGS SHARE
// THIS BODY: the two Dolt-backed stores wrap it in their own transaction, and
// the unit-of-work provider reaches it through the domain issue repository. So
// the three legs are ONE reading plus two wrapper checks, which is what the
// conformance contract's header says and what its cases are written for.
//
// It assumes a plan already produced by storage.PlanCompareAndSetKey — values
// canonical, actor and key checked. The accessors plan BEFORE opening a
// transaction, so a refused request costs no database work.
//
// It reports what the swap DID to the row alongside the result, because only
// the caller knows what to do with that; see MetadataCASWrite for why those are
// two facts rather than one.
func CompareAndSetMetadataKeyInTx(
	ctx context.Context,
	tx DBTX,
	plan storage.CompareAndSetKeyPlan,
) (publicops.CompareAndSetKeyResult, MetadataCASWrite, error) {
	metadata, err := readMetadataMapInTx(ctx, tx, plan.IssueID)
	if err != nil {
		return publicops.CompareAndSetKeyResult{}, MetadataCASWrite{}, err
	}

	current, err := canonicalMetadataKey(metadata, plan.Key)
	if err != nil {
		return publicops.CompareAndSetKeyResult{}, MetadataCASWrite{}, fmt.Errorf(
			"metadata key %q on %s: %w", plan.Key, plan.IssueID, err)
	}

	if !sameCanonicalValue(current, plan.Expected) {
		// A lost race is an ANSWER: the caller recomputes from Current and
		// swaps again. Nothing is written and no error is raised.
		return publicops.CompareAndSetKeyResult{Swapped: false, Current: current}, MetadataCASWrite{}, nil
	}

	if sameCanonicalValue(current, plan.Value) {
		// The precondition held and the stored value is already the requested
		// one. Writing the object back would re-serialize every sibling key and
		// re-read the row for a transition that did not happen.
		//
		// IT IS NOT THE ONLY THING HOLDING "a swap that changes nothing writes
		// nothing": UpdateIssueInTx runs DiscardNoopIssueUpdates and drops a
		// metadata write that matches the row, so deleting this branch leaves
		// every contract case green — measured, not assumed. Keep it anyway.
		// It is the guard that does not depend on a layer below agreeing, and
		// it is what keeps a swap over an unchanged key from paying for a
		// whole-object rewrite on a row with large metadata.
		return publicops.CompareAndSetKeyResult{Swapped: true, Current: plan.Value}, MetadataCASWrite{}, nil
	}

	if plan.Value == nil {
		delete(metadata, plan.Key)
	} else {
		metadata[plan.Key] = *plan.Value
	}
	if err := writeMergedMetadataInTx(ctx, tx, plan.IssueID, metadata, plan.Actor); err != nil {
		return publicops.CompareAndSetKeyResult{}, MetadataCASWrite{}, err
	}

	// The write goes through UpdateIssueInTx, which also records the update
	// event, so both tables changed. ChangedTables.Add drops the ephemeral
	// members, which is what leaves an ephemeral swap with nothing to VERSION
	// while still having written a row.
	issueTable, _, eventTable, _ := WispTableRouting(IsActiveWispInTx(ctx, tx, plan.IssueID))
	write := MetadataCASWrite{Wrote: true, Tables: ChangedTables{}}
	write.Tables.Add(issueTable, eventTable)
	return publicops.CompareAndSetKeyResult{Swapped: true, Current: plan.Value}, write, nil
}

// MetadataCASWrite reports what one compare-and-set did to the row.
//
// THE TWO FIELDS ARE NOT THE SAME QUESTION, and conflating them is a real bug
// rather than a tidiness point: a swap on an EPHEMERAL row writes a row and
// changes no durable table, because the version-control plane ignores the wisp
// tables. A caller that read an empty table set as "nothing happened" would
// roll the write back — which is exactly what the unit-of-work leg did before
// this type existed, since its commit message is what commits the SQL
// transaction as well as what versions it.
type MetadataCASWrite struct {
	// Wrote is true when the metadata column was rewritten. It is false for a
	// lost race and for a swap whose precondition held over a value already
	// equal to the requested one.
	Wrote bool
	// Tables are the DURABLE tables the write changed, for a caller that stages
	// them. It is empty when nothing was written AND when the row that was
	// written is ephemeral.
	Tables ChangedTables
}

// canonicalMetadataKey returns the canonical encoding of one key's stored
// value, or nil when the key is absent. The stored bytes come out of a decoded
// metadata object, so they are well-formed by construction; canonicalizing them
// here is what lets a caller compare against a value it formatted itself.
func canonicalMetadataKey(metadata map[string]json.RawMessage, key string) (*json.RawMessage, error) {
	stored, ok := metadata[key]
	if !ok {
		return nil, nil
	}
	canonical, err := storage.CanonicalMetadataValue(stored)
	if err != nil {
		return nil, err
	}
	return &canonical, nil
}

// sameCanonicalValue compares two already-canonical optional values, with nil
// meaning ABSENT on either side.
func sameCanonicalValue(a, b *json.RawMessage) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return bytes.Equal(*a, *b)
}
