//go:build !linux && !darwin && !windows

package beads

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFollowRedirectStrictFailsClosedWithoutMetadataObservation(t *testing.T) {
	source := t.TempDir()
	if got, err := FollowRedirectStrict(source); err != nil || got != source {
		t.Fatalf("absent redirect got path=%q err=%v, want unchanged source", got, err)
	}
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, RedirectFileName), []byte(target+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := FollowRedirectStrict(source)
	if got != "" || !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("strict redirect got path=%q err=%v, want unsupported failure", got, err)
	}
}
