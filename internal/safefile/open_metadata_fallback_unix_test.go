//go:build aix || dragonfly || freebsd || illumos || netbsd || openbsd || solaris || zos

package safefile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestMetadataOpenIsUnsupportedWithoutMetadataOnlyPrimitive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := OpenMetadataNoFollow(path)
	if file != nil || !errors.Is(err, errors.ErrUnsupported) {
		if file != nil {
			_ = file.Close()
		}
		t.Fatalf("metadata open got file=%v err=%v, want errors.ErrUnsupported", file, err)
	}
}
