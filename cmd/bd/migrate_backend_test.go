package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/configfile"
)

func TestBackendMigrationConfirmationRequiresExactBasename(t *testing.T) {
	for line, want := range map[string]bool{
		"beads.db\n":   true,
		"beads.db\r\n": true,
		"beads.db":     true,
		" beads.db\n":  false,
		"beads.db \n":  false,
		"BEADS.DB\n":   false,
	} {
		if got := backendMigrationConfirmationMatches(line, "beads.db"); got != want {
			t.Errorf("confirmation %q = %v, want %v", line, got, want)
		}
	}
}

func TestBackendMigrationApplyCommandPreservesCustomTarget(t *testing.T) {
	if got := backendMigrationApplyCommand("beads.db"); got != "bd migrate backend --to=sqlite --apply" {
		t.Fatalf("default apply command = %q", got)
	}
	if got := backendMigrationApplyCommand("archive.db"); got != "bd migrate backend --to=sqlite --sqlite-path=archive.db --apply" {
		t.Fatalf("custom apply command = %q", got)
	}
}

func TestBackendMigrationEnvFileSelection(t *testing.T) {
	for _, test := range []struct {
		name string
		env  string
		want string
	}{
		{name: "exported physical redirect", env: "# comment\nexport BEADS_DIR=/tmp/decoy/.beads\n", want: "BEADS_DIR"},
		{name: "database path", env: "BEADS_DB=/tmp/decoy/beads.db\n", want: "BEADS_DB"},
		{name: "legacy database alias", env: "BD_DB=/tmp/decoy/beads.db\n", want: "BD_DB"},
		{name: "deterministic first key", env: "BD_DB=/tmp/b\nBEADS_DIR=/tmp/a\n", want: "BEADS_DIR"},
		{name: "empty selector allowed", env: "BEADS_DIR=   \nBEADS_DOLT_PASSWORD=secret\n"},
		{name: "unrelated credentials ignored", env: "BEADS_DOLT_PASSWORD=secret\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			beadsDir := filepath.Join(t.TempDir(), ".beads")
			if err := os.Mkdir(beadsDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(beadsDir, ".env"), []byte(test.env), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := backendMigrationEnvFileSelection(beadsDir)
			if err != nil || got != test.want {
				t.Fatalf("backendMigrationEnvFileSelection() = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestBackendMigrationSuppressesOnlyItsExpectedPendingWarning(t *testing.T) {
	if shouldWarnNoStoreConfigError(configfile.ErrBackendMigrationPending) {
		t.Fatal("migrate backend should suppress its expected pending recovery warning")
	}
	if !shouldWarnNoStoreConfigError(errors.New("corrupt metadata")) {
		t.Fatal("migrate backend unexpectedly suppressed a different config error")
	}
}

func TestBackendMigrationRejectsNonPhysicalWorkspaceSelectors(t *testing.T) {
	originalDBPath := dbPath
	originalGlobal := globalFlag
	originalAmbient := migrateBackendAmbientBeadsDir
	t.Cleanup(func() {
		dbPath = originalDBPath
		globalFlag = originalGlobal
		migrateBackendAmbientBeadsDir = originalAmbient
	})
	t.Chdir(t.TempDir())
	migrateBackendAmbientBeadsDir = false
	for _, name := range []string{
		"BEADS_DIR", "BEADS_DB", "BD_DB", "BEADS_DOLT_SERVER_DATABASE", "BEADS_DOLT_DATA_DIR",
		"BEADS_DOLT_SERVER_MODE", "BEADS_DOLT_SHARED_SERVER", "BD_IGNORE_SCHEMA_SKEW",
	} {
		t.Setenv(name, "")
	}

	newCommand := func() *cobra.Command {
		root := &cobra.Command{Use: "bd"}
		root.PersistentFlags().BoolVar(&globalFlag, "global", false, "")
		child := &cobra.Command{Use: "backend"}
		root.AddCommand(child)
		return child
	}

	t.Run("configured db", func(t *testing.T) {
		dbPath = "/tmp/configured-beads.db"
		globalFlag = false
		err := validateBackendMigrationWorkspaceSelection(newCommand())
		if err == nil || !strings.Contains(err.Error(), "configured") {
			t.Fatalf("configured db selection error = %v", err)
		}
	})

	t.Run("global", func(t *testing.T) {
		dbPath = ""
		globalFlag = false
		cmd := newCommand()
		if err := cmd.Root().PersistentFlags().Set("global", "true"); err != nil {
			t.Fatal(err)
		}
		err := validateBackendMigrationWorkspaceSelection(cmd)
		if err == nil || !strings.Contains(err.Error(), "--global") {
			t.Fatalf("global selection error = %v", err)
		}
	})

	t.Run("schema skew override", func(t *testing.T) {
		dbPath = ""
		globalFlag = false
		t.Setenv("BD_IGNORE_SCHEMA_SKEW", "1")
		err := validateBackendMigrationWorkspaceSelection(newCommand())
		if err == nil || !strings.Contains(err.Error(), "BD_IGNORE_SCHEMA_SKEW") {
			t.Fatalf("schema-skew override error = %v", err)
		}
	})
}
