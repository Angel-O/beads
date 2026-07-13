//go:build windows

package configfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadStableConfigFileDoesNotFollowReplacementReparsePoint(t *testing.T) {
	beadsDir := t.TempDir()
	path := ConfigPath(beadsDir)
	if err := os.WriteFile(path, []byte(`{"database":"dolt","backend":"dolt"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target.json")
	if err := os.WriteFile(target, []byte(`{"database":"beads.db","backend":"sqlite","sqlite_path":"beads.db"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := readStableConfigFileWithOpener(path, func(path string) (*os.File, error) {
		if err := os.Remove(path); err != nil {
			return nil, err
		}
		if err := os.Symlink(target, path); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		return openReadOnlyConfigFile(path)
	})
	if err == nil {
		t.Fatal("replacement reparse point was accepted")
	}
	if !strings.Contains(err.Error(), "reparse point") {
		t.Fatalf("replacement error = %v, want reparse-point rejection", err)
	}
}
