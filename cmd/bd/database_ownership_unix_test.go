//go:build (darwin && !ios) || (linux && !android)

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
	"golang.org/x/sys/unix"
)

func TestStrictDatabaseOwnershipRejectsMetadataFIFO(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	dbPath := filepath.Join(beadsDir, "embeddeddolt", "source")
	if err := os.MkdirAll(dbPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(configfile.ConfigPath(beadsDir), 0o600); err != nil {
		t.Fatal(err)
	}
	binding, err := resolveDatabaseOwnershipStrict(dbPath)
	if err == nil || binding != nil {
		t.Fatalf("binding=%#v err=%v, want metadata FIFO rejection", binding, err)
	}
}

func TestStrictDatabaseOwnershipRejectsProviderFIFO(t *testing.T) {
	for _, backend := range []string{configfile.BackendDolt, configfile.BackendSQLite} {
		for _, throughSymlink := range []bool{false, true} {
			name := backend + " direct"
			if throughSymlink {
				name = backend + " symlink"
			}
			t.Run(name, func(t *testing.T) {
				beadsDir := filepath.Join(t.TempDir(), ".beads")
				if err := os.MkdirAll(beadsDir, 0o700); err != nil {
					t.Fatal(err)
				}
				target := filepath.Join(beadsDir, "provider-fifo")
				if err := unix.Mkfifo(target, 0o600); err != nil {
					t.Fatal(err)
				}
				ownedPath := target
				if throughSymlink {
					ownedPath = filepath.Join(beadsDir, "provider-link")
					if err := os.Symlink(target, ownedPath); err != nil {
						t.Skipf("symlink unavailable: %v", err)
					}
				}
				cfg := configfile.Config{Database: "dolt", Backend: backend}
				if backend == configfile.BackendDolt {
					cfg.DoltDataDir = ownedPath
				} else {
					cfg.Database = "beads.db"
					cfg.SQLitePath = ownedPath
				}
				writeOwnershipMetadata(t, beadsDir, cfg)

				binding, err := resolveDatabaseOwnershipStrict(beadsDir, databaseWorkspaceHint{
					beadsDir:      beadsDir,
					authoritative: true,
				})
				if binding != nil || err == nil {
					t.Fatalf("binding=%#v err=%v, want provider FIFO rejection", binding, err)
				}
			})
		}
	}
}
