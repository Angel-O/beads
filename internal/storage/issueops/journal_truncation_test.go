package issueops

import (
	"errors"
	"fmt"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
)

func rowsAt(seqs ...int64) []storage.EventsJournalRow {
	out := make([]storage.EventsJournalRow, 0, len(seqs))
	for _, s := range seqs {
		out = append(out, storage.EventsJournalRow{Seq: s, Op: string(EventCreate)})
	}
	return out
}

// TestComputeEventsTruncation covers the decision table a consumer's resume
// depends on. The cases that must NOT fail matter as much as the ones that
// must: a false truncation would fault a healthy exporter on every poll.
func TestComputeEventsTruncation(t *testing.T) {
	cases := []struct {
		name      string
		since     int64
		rows      []storage.EventsJournalRow
		head      int64
		wantErr   bool
		wantFloor int64
		wantHead  int64
	}{
		{
			name:  "fresh consumer reads from the start",
			since: 0,
			rows:  rowsAt(1, 2, 3),
			head:  3,
		},
		{
			name:  "restart resumes the contiguous prefix",
			since: 7,
			rows:  rowsAt(8, 9),
			head:  9,
		},
		{
			name:  "caught up: no rows and since is the head",
			since: 9,
			head:  9,
		},
		{
			name:  "empty journal, nothing ever written",
			since: 0,
			head:  0,
		},
		{
			name:  "prune below the checkpoint leaves the resume contiguous",
			since: 7,
			rows:  rowsAt(8, 9),
			head:  9,
		},
		{
			name:      "checkpoint pruned past: rows resume above since+1",
			since:     3,
			rows:      rowsAt(11, 12),
			head:      12,
			wantErr:   true,
			wantFloor: 11,
			wantHead:  12,
		},
		{
			name:      "off by one: exactly one record pruned",
			since:     7,
			rows:      rowsAt(9, 10),
			head:      10,
			wantErr:   true,
			wantFloor: 9,
			wantHead:  10,
		},
		{
			name:      "whole journal pruned: empty result is not caught up",
			since:     5,
			head:      10,
			wantErr:   true,
			wantFloor: 11,
			wantHead:  10,
		},
		{
			name:  "head regression is not reported as truncation",
			since: 20,
			head:  10,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ComputeEventsTruncation(tc.since, tc.rows, func() (int64, error) { return tc.head, nil })
			var trunc *storage.EventsJournalTruncatedError
			switch {
			case !tc.wantErr && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case !tc.wantErr:
				return
			case err == nil:
				t.Fatal("expected a truncation error, got nil")
			case !errors.As(err, &trunc):
				t.Fatalf("error is not *EventsJournalTruncatedError: %T %v", err, err)
			}
			if trunc.Since != tc.since {
				t.Errorf("Since = %d, want %d", trunc.Since, tc.since)
			}
			if trunc.Floor != tc.wantFloor {
				t.Errorf("Floor = %d, want %d", trunc.Floor, tc.wantFloor)
			}
			if trunc.Head != tc.wantHead {
				t.Errorf("Head = %d, want %d", trunc.Head, tc.wantHead)
			}
		})
	}
}

// TestComputeEventsTruncationSkipsHeadReadWhenContiguous pins the hot path:
// `bd events tail --follow` polls once a second, so a healthy resume must not
// pay for the counter read.
func TestComputeEventsTruncationSkipsHeadReadWhenContiguous(t *testing.T) {
	calls := 0
	err := ComputeEventsTruncation(4, rowsAt(5, 6), func() (int64, error) {
		calls++
		return 6, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 0 {
		t.Errorf("readHead called %d times on a contiguous read, want 0", calls)
	}
}

func TestComputeEventsTruncationPropagatesHeadReadError(t *testing.T) {
	sentinel := errors.New("counter unavailable")
	err := ComputeEventsTruncation(3, nil, func() (int64, error) { return 0, sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the readHead error", err)
	}
	var trunc *storage.EventsJournalTruncatedError
	if errors.As(err, &trunc) {
		t.Error("an unreadable counter must not be reported as truncation")
	}
}

// TestEventsJournalTruncatedErrorMessage keeps the message actionable: an
// operator reading it must be able to see the lost span without doing math.
func TestEventsJournalTruncatedErrorMessage(t *testing.T) {
	err := &storage.EventsJournalTruncatedError{Since: 3, Floor: 11, Head: 12}
	want := "events journal truncated: checkpoint 3 is below the retained window [11..12]; records 4..10 were pruned"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if fmt.Sprintf("%v", error(err)) != want {
		t.Error("error does not format through the error interface")
	}
}
