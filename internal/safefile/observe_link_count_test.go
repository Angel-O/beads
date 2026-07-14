//go:build android || darwin || ios || linux || windows

package safefile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestObserveMetadataNoFollowReportsSingleLink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "database.db")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	observation, err := ObserveMetadataNoFollow(path)
	if err != nil {
		t.Fatal(err)
	}
	if !observation.LinkCountKnown || observation.LinkCount != 1 {
		t.Fatalf("link count known=%v count=%d, want known single link", observation.LinkCountKnown, observation.LinkCount)
	}
}

func TestObserveMetadataNoFollowReportsHardLinks(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "database.db")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "database-alias.db")
	if err := os.Link(path, alias); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}

	for _, candidate := range []string{path, alias} {
		observation, err := ObserveMetadataNoFollow(candidate)
		if err != nil {
			t.Fatal(err)
		}
		if !observation.LinkCountKnown || observation.LinkCount != 2 {
			t.Fatalf("%q link count known=%v count=%d, want known two links", candidate, observation.LinkCountKnown, observation.LinkCount)
		}
	}
}
