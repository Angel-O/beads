//go:build darwin || ios

package safefile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestObserveMetadataNoFollowDarwinReturnsStoredCaseAndSemantics(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "StoredCase")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	alternate := filepath.Join(parent, "storedcase")
	lookupPath := alternate
	wantSensitive := false
	if _, err := os.Stat(alternate); err != nil {
		lookupPath = path
		wantSensitive = true
	}
	observation, err := ObserveMetadataNoFollow(lookupPath)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(observation.CanonicalPath) != "StoredCase" {
		t.Fatalf("canonical path = %q, want stored component case", observation.CanonicalPath)
	}
	if !observation.CaseSensitivityKnown || observation.CaseSensitive != wantSensitive {
		t.Fatalf("case semantics known=%v sensitive=%v, want known sensitive=%v", observation.CaseSensitivityKnown, observation.CaseSensitive, wantSensitive)
	}
}

func TestInterpretDarwinCaseSensitivity(t *testing.T) {
	for _, tt := range []struct {
		value         int
		wantSensitive bool
		wantKnown     bool
	}{
		{value: 0, wantSensitive: false, wantKnown: true},
		{value: 1, wantSensitive: true, wantKnown: true},
		{value: -1, wantSensitive: true, wantKnown: false},
	} {
		gotSensitive, gotKnown := interpretDarwinCaseSensitivity(tt.value)
		if gotSensitive != tt.wantSensitive || gotKnown != tt.wantKnown {
			t.Fatalf("case interpretation %d = sensitive=%v known=%v, want %v %v",
				tt.value, gotSensitive, gotKnown, tt.wantSensitive, tt.wantKnown)
		}
	}
}
