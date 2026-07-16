package control

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestTryAcquireSerializesWorkspaceMutations(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}

	first, err := TryAcquire(beadsDir)
	if err != nil {
		t.Fatalf("first TryAcquire: %v", err)
	}
	if _, err := TryAcquire(beadsDir); !errors.Is(err, ErrBusy) {
		t.Fatalf("contending TryAcquire error = %v, want ErrBusy", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("release first guard: %v", err)
	}

	second, err := TryAcquire(beadsDir)
	if err != nil {
		t.Fatalf("TryAcquire after release: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("release second guard: %v", err)
	}
	if info, err := os.Lstat(filepath.Join(beadsDir, FileName)); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("stable control file = %#v, %v", info, err)
	}
}

func TestTryAcquireRejectsLinkedControlFile(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "foreign.lock")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(beadsDir, FileName)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if guard, err := TryAcquire(beadsDir); err == nil {
		_ = guard.Close()
		t.Fatal("TryAcquire accepted a symlinked control file")
	}
}
