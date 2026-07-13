//go:build linux

package safefile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestObserveMetadataNoFollowReturnsOneHandleObservation(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "StoredCase.db")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	observation, err := ObserveMetadataNoFollow(path)
	if err != nil {
		t.Fatal(err)
	}
	if observation.CanonicalPath != path {
		t.Fatalf("canonical path = %q, want %q", observation.CanonicalPath, path)
	}
	if observation.Info == nil || !observation.Info.Mode().IsRegular() {
		t.Fatalf("metadata info = %#v, want regular file", observation.Info)
	}

	directory, err := ObserveMetadataNoFollow(root)
	if err != nil {
		t.Fatal(err)
	}
	if !directory.Info.IsDir() {
		t.Fatalf("directory info mode = %v", directory.Info.Mode())
	}
	if directory.CaseSensitivityKnown && !directory.CaseSensitive {
		t.Fatalf("temporary directory unexpectedly reported case-insensitive")
	}
}

func TestObserveMetadataNoFollowAllowsLiveDeletedSuffix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "live (deleted)")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	observation, err := ObserveMetadataNoFollow(path)
	if err != nil {
		t.Fatal(err)
	}
	if observation.CanonicalPath != path {
		t.Fatalf("canonical path = %q, want live path %q", observation.CanonicalPath, path)
	}
}

func TestObserveMetadataNoFollowRejectsFinalSymlink(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if observation, err := ObserveMetadataNoFollow(link); err == nil || observation != nil {
		t.Fatalf("symlink observation = %#v, err=%v, want rejection", observation, err)
	}
}
