//go:build !aix && !android && !darwin && !dragonfly && !freebsd && !illumos && !ios && !linux && !netbsd && !openbsd && !solaris && !windows && !zos

package safefile

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestUnsupportedPlatformOpenPolicies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data")
	if err := os.WriteFile(path, []byte("contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := OpenReadOnly(path)
	if err != nil {
		t.Fatalf("compatibility open: %v", err)
	}
	data, readErr := io.ReadAll(file)
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || string(data) != "contents" {
		t.Fatalf("compatibility read got %q readErr=%v closeErr=%v", data, readErr, closeErr)
	}

	file, err = OpenReadOnlyNoFollow(path)
	if file != nil || !errors.Is(err, errors.ErrUnsupported) {
		if file != nil {
			_ = file.Close()
		}
		t.Fatalf("strict open got file=%v err=%v, want errors.ErrUnsupported", file, err)
	}
}
