package safefile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadRegularFileBoundsAndIdentity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "control.json")
	if err := os.WriteFile(path, []byte("1234"), 0o600); err != nil {
		t.Fatal(err)
	}

	data, err := ReadRegularFile(path, 4)
	if err != nil || string(data) != "1234" {
		t.Fatalf("exact-bound read = %q, %v", data, err)
	}
	if _, err := ReadRegularFile(path, 3); err == nil {
		t.Fatal("oversized control file unexpectedly read")
	}
	if _, err := ReadRegularFile(dir, 1024); err == nil {
		t.Fatal("directory unexpectedly read as a control file")
	}
}

func TestReadRegularFileRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.json")
	link := filepath.Join(dir, "control.json")
	if err := os.WriteFile(target, []byte("foreign"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := ReadRegularFile(link, 1024); err == nil {
		t.Fatal("symlinked control file unexpectedly read")
	}
}
