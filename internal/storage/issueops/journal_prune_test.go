package issueops

import (
	"testing"
	"time"
)

func TestBuildMutationsPruneWhere(t *testing.T) {
	now := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	before := int64(500)

	cases := []struct {
		name       string
		retainDays int
		rowsCeil   int64
		rowsCeilOK bool
		wantWhere  string
		wantArgs   []any
	}{
		{
			name:      "no floors",
			wantWhere: "seq < ?",
			wantArgs:  []any{before},
		},
		{
			name:       "retain-days only",
			retainDays: 7,
			wantWhere:  "seq < ? AND ts < ?",
			wantArgs:   []any{before, now.AddDate(0, 0, -7).UTC()},
		},
		{
			name:       "retain-rows only",
			rowsCeil:   480,
			rowsCeilOK: true,
			wantWhere:  "seq < ? AND seq <= ?",
			wantArgs:   []any{before, int64(480)},
		},
		{
			name:       "both floors",
			retainDays: 3,
			rowsCeil:   490,
			rowsCeilOK: true,
			wantWhere:  "seq < ? AND ts < ? AND seq <= ?",
			wantArgs:   []any{before, now.AddDate(0, 0, -3).UTC(), int64(490)},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			where, args := BuildMutationsPruneWhere(before, tc.retainDays, now, tc.rowsCeil, tc.rowsCeilOK)
			if where != tc.wantWhere {
				t.Errorf("where = %q, want %q", where, tc.wantWhere)
			}
			if len(args) != len(tc.wantArgs) {
				t.Fatalf("args len = %d (%v), want %d (%v)", len(args), args, len(tc.wantArgs), tc.wantArgs)
			}
			for i := range args {
				if args[i] != tc.wantArgs[i] {
					t.Errorf("arg[%d] = %v, want %v", i, args[i], tc.wantArgs[i])
				}
			}
		})
	}
}
