//go:build windows

package safefile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsReadOnlyNoFollowRejectsSymlink(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("target data"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	file, err := OpenReadOnlyNoFollow(link)
	if err == nil || file != nil {
		if file != nil {
			_ = file.Close()
		}
		t.Fatalf("no-follow open got file=%v err=%v, want rejection", file, err)
	}
	if !strings.Contains(err.Error(), "reparse point") {
		t.Fatalf("no-follow error = %v, want reparse-point rejection", err)
	}
}
