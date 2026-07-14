//go:build android || darwin || ios || linux || windows

package safefile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenedFileLinkCountUsesOpenedIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "database.db")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := OpenReadOnlyNoFollow(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close() //nolint:errcheck // test descriptor
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if count, known := OpenedFileLinkCount(file, info); !known || count != 1 {
		t.Fatalf("initial link count known=%v count=%d, want known single link", known, count)
	}
	if err := os.Link(path, path+".alias"); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	info, err = file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if count, known := OpenedFileLinkCount(file, info); !known || count != 2 {
		t.Fatalf("updated link count known=%v count=%d, want known two links", known, count)
	}
}
