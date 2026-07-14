//go:build darwin && !ios

package beads

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFollowRedirectStrictPreservesCaseDistinctDarwinTarget(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wrong := filepath.Join(root, "Workspace")
	target := filepath.Join(root, "workspace")
	if err := os.Mkdir(wrong, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Skipf("test volume is not case-sensitive: %v", err)
	}
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, RedirectFileName), []byte(target+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := FollowRedirectStrict(source)
	if err != nil || got != target {
		t.Fatalf("strict redirect = %q, err=%v, want exact case-distinct target %q", got, err, target)
	}
}
