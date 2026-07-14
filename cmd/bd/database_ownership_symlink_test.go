//go:build android || darwin || ios || linux

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
)

func TestResolveDatabaseOwnershipStrictFindsOwnerThroughInwardLeafSymlink(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	databasePath := filepath.Join(beadsDir, "beads.db")
	writeOwnershipMetadata(t, beadsDir, configfile.Config{Database: "beads.db", Backend: configfile.BackendSQLite, SQLitePath: "beads.db"})
	if err := os.WriteFile(databasePath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	selector := filepath.Join(t.TempDir(), "selected.db")
	if err := os.Symlink(databasePath, selector); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	binding, err := resolveDatabaseOwnershipStrict(selector)
	if err != nil || binding == nil || !databasePathsEqualForTest(t, binding.beadsDir, beadsDir) {
		t.Fatalf("binding=%#v err=%v, want inward-symlink owner %q", binding, err, beadsDir)
	}
}
