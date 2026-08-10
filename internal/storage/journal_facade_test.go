package storage

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/steveyegge/beads/journalops"
)

// TestJournalNamesAreAliasesNotCopies pins the property the journal's move into
// a leaf package rests on: storage.EventsJournalRow, EventsJournalPage,
// EventsJournalTruncatedError and EventsJournalCursor are the journalops types,
// not lookalikes declared beside them.
//
// It exists because the failure is nearly invisible. Turning
// `type EventsJournalCursor = journalops.Journal` into
// `type EventsJournalCursor journalops.Journal` still compiles everywhere and
// every implementation still satisfies both, because interface satisfaction is
// structural — so the one thing that would actually break is errors.As on the
// truncation, at runtime, on the backends whose suites need a live engine. The
// role contract does assert that identity (backend/conformance/journal_contract.go
// matches *journalops.TruncatedError against errors every leg constructs as
// *EventsJournalTruncatedError), but it only runs where a Dolt server or cgo
// does. This runs everywhere, in milliseconds, and names the alias as the thing
// that broke.
func TestJournalNamesAreAliasesNotCopies(t *testing.T) {
	for _, alias := range []struct {
		name    string
		storage reflect.Type
		leaf    reflect.Type
	}{
		{"EventsJournalRow", reflect.TypeFor[EventsJournalRow](), reflect.TypeFor[journalops.Row]()},
		{"EventsJournalPage", reflect.TypeFor[EventsJournalPage](), reflect.TypeFor[journalops.Page]()},
		{"EventsJournalTruncatedError", reflect.TypeFor[EventsJournalTruncatedError](), reflect.TypeFor[journalops.TruncatedError]()},
		{"EventsJournalCursor", reflect.TypeFor[EventsJournalCursor](), reflect.TypeFor[journalops.Journal]()},
	} {
		if alias.storage != alias.leaf {
			t.Errorf("storage.%s is %s, not the journalops type %s: it is a redeclaration rather than an "+
				"alias, so every caller now has two vocabularies for one thing and errors.As matches only one",
				alias.name, alias.storage, alias.leaf)
		}
	}

	if EventsJournalTruncatedCode != journalops.TruncatedCode {
		t.Errorf("EventsJournalTruncatedCode = %q, want journalops.TruncatedCode (%q): the wire spelling of "+
			"a truncation cannot differ by which package a handler imported",
			EventsJournalTruncatedCode, journalops.TruncatedCode)
	}
}

// TestJournalTruncationCrossesTheAliasUnderErrorsAs is the runtime half, and the
// one that matches how the failure would actually present: a caller holding the
// leaf's spelling classifies an error a storage-side body constructed with the
// alias, through a wrapper, and gets its fields.
func TestJournalTruncationCrossesTheAliasUnderErrorsAs(t *testing.T) {
	wrapped := fmt.Errorf("reading the journal: %w",
		&EventsJournalTruncatedError{Since: 7, Floor: 12, Head: 40})

	var trunc *journalops.TruncatedError
	if !errors.As(wrapped, &trunc) {
		t.Fatalf("errors.As(%v, **journalops.TruncatedError) did not match an error built as "+
			"*storage.EventsJournalTruncatedError; the two spellings have to be one type", wrapped)
	}
	if trunc.Since != 7 || trunc.Floor != 12 || trunc.Head != 40 {
		t.Errorf("window = [%d..%d] after %d, want [12..40] after 7", trunc.Floor, trunc.Head, trunc.Since)
	}
}
