//go:build windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
)

func TestStrictDatabaseOwnershipPreservesWindowsCaseSensitiveIdentity(t *testing.T) {
	t.Run("SQLite case-distinct file", func(t *testing.T) {
		beadsDir := t.TempDir()
		requireCaseSensitiveOwnershipDirectory(t, beadsDir)
		writeOwnershipMetadata(t, beadsDir, configfile.Config{
			Database:   "beads.db",
			Backend:    configfile.BackendSQLite,
			SQLitePath: "database.db",
		})
		for _, name := range []string{"database.db", "Database.db"} {
			if err := os.WriteFile(filepath.Join(beadsDir, name), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}

		binding, err := resolveDatabaseOwnershipStrict(filepath.Join(beadsDir, "Database.db"), databaseWorkspaceHint{beadsDir: beadsDir})
		if binding != nil || !errors.Is(err, errDatabaseOwnershipContradiction) {
			t.Fatalf("binding=%#v err=%v, want case-distinct SQLite contradiction", binding, err)
		}
	})

	t.Run("case-distinct owners stay ambiguous", func(t *testing.T) {
		root := t.TempDir()
		requireCaseSensitiveOwnershipDirectory(t, root)
		upper := filepath.Join(root, "Owner")
		lower := filepath.Join(root, "owner")
		dbPath := filepath.Join(root, "external-dolt", "source")
		if err := os.MkdirAll(dbPath, 0o700); err != nil {
			t.Fatal(err)
		}
		for _, beadsDir := range []string{upper, lower} {
			writeOwnershipMetadata(t, beadsDir, configfile.Config{
				Database:    "dolt",
				Backend:     configfile.BackendDolt,
				DoltDataDir: filepath.Dir(dbPath),
			})
		}

		binding, err := resolveDatabaseOwnershipStrict(dbPath,
			databaseWorkspaceHint{beadsDir: upper},
			databaseWorkspaceHint{beadsDir: lower},
		)
		if binding != nil || !errors.Is(err, errDatabaseOwnershipAmbiguous) {
			t.Fatalf("binding=%#v err=%v, want case-distinct owner ambiguity", binding, err)
		}
	})

	t.Run("case-distinct authoritative hint is not selected root", func(t *testing.T) {
		root := t.TempDir()
		requireCaseSensitiveOwnershipDirectory(t, root)
		selected := filepath.Join(root, "Workspace")
		hinted := filepath.Join(root, "workspace")
		if err := os.Mkdir(selected, 0o700); err != nil {
			t.Fatal(err)
		}
		writeOwnershipMetadata(t, hinted, configfile.Config{Database: "beads.db", Backend: configfile.BackendPostgres})

		binding, err := resolveDatabaseOwnershipStrict(selected, databaseWorkspaceHint{
			beadsDir:      hinted,
			authoritative: true,
		})
		if binding != nil || !errors.Is(err, errDatabaseOwnershipContradiction) {
			t.Fatalf("binding=%#v err=%v, want case-distinct authoritative contradiction", binding, err)
		}
	})

	t.Run("case-distinct dot directory is not structural", func(t *testing.T) {
		root := t.TempDir()
		requireCaseSensitiveOwnershipDirectory(t, root)
		beadsDir := filepath.Join(root, ".BEADS")
		databasePath := filepath.Join(beadsDir, "beads.db")
		if err := os.Mkdir(beadsDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(databasePath, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		selector, err := validatedDatabaseSelector(databasePath)
		if err != nil {
			t.Fatal(err)
		}
		candidates, err := databasePathOwnershipCandidates(databasePath, selector)
		if err != nil {
			t.Fatal(err)
		}
		assertWindowsCandidatePolicy(t, candidates, beadsDir, databaseOwnershipContradictUnlessNestedOwner, false)
	})
}

func TestDatabaseOwnershipStoredCaseBeadsMergesStructuralPolicy(t *testing.T) {
	root := t.TempDir()
	beadsDir := filepath.Join(root, ".BEADS")
	databasePath := filepath.Join(beadsDir, "beads.db")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".beads")); err != nil {
		t.Skipf("test directory is case-sensitive: %v", err)
	}
	if err := os.WriteFile(databasePath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	selector, err := validatedDatabaseSelector(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := databasePathOwnershipCandidates(databasePath, selector)
	if err != nil {
		t.Fatal(err)
	}
	assertWindowsCandidatePolicy(t, candidates, beadsDir, databaseOwnershipAlwaysContradict, true)
}

func TestResolveDatabaseOwnershipStrictFindsOwnerThroughWindowsJunction(t *testing.T) {
	root := t.TempDir()
	realRoot := filepath.Join(root, "real")
	beadsDir := filepath.Join(realRoot, ".beads")
	databasePath := filepath.Join(beadsDir, "embeddeddolt", "source")
	if err := os.MkdirAll(databasePath, 0o700); err != nil {
		t.Fatal(err)
	}
	writeOwnershipMetadata(t, beadsDir, configfile.Config{Database: "dolt", Backend: configfile.BackendDolt})

	junction := filepath.Join(root, "junction")
	if output, err := exec.Command("cmd", "/c", "mklink", "/J", junction, realRoot).CombinedOutput(); err != nil {
		t.Skipf("junction unavailable: %v (%s)", err, strings.TrimSpace(string(output)))
	}
	binding, err := resolveDatabaseOwnershipStrict(filepath.Join(junction, ".beads", "embeddeddolt", "source"))
	if err != nil || binding == nil || !databasePathsEqualForTest(t, binding.beadsDir, beadsDir) {
		t.Fatalf("junction binding=%#v err=%v, want owner %q", binding, err, beadsDir)
	}
	if binding.backend != configfile.BackendDolt || binding.scope != databaseOwnershipScopeDescendant {
		t.Fatalf("junction binding=%#v, want Dolt descendant scope", binding)
	}
}

func TestStructuralDatabaseOwnershipHandlesWindowsReparsePoints(t *testing.T) {
	createJunction := func(t *testing.T, junction, target string) {
		t.Helper()
		if output, err := exec.Command("cmd", "/c", "mklink", "/J", junction, target).CombinedOutput(); err != nil {
			t.Skipf("junction unavailable: %v (%s)", err, strings.TrimSpace(string(output)))
		}
	}
	assertRejected := func(t *testing.T, selector string) {
		t.Helper()
		result, err := resolveDatabaseOwnershipStrictResult(selector, false)
		if result != (databaseOwnershipResolution{}) || err == nil {
			t.Fatalf("result=%#v err=%v, want metadata-less reparse rejection", result, err)
		}
	}
	assertNoStructure := func(t *testing.T, selector string) {
		t.Helper()
		result, err := resolveDatabaseOwnershipStrictResult(selector, false)
		if result != (databaseOwnershipResolution{}) || err != nil {
			t.Fatalf("result=%#v err=%v, want no canonical structural containment", result, err)
		}
	}

	t.Run("valid ancestor junction", func(t *testing.T) {
		root := t.TempDir()
		realRoot := filepath.Join(root, "real")
		beadsDir := filepath.Join(realRoot, ".beads")
		if err := os.MkdirAll(filepath.Join(beadsDir, "selected"), 0o700); err != nil {
			t.Fatal(err)
		}
		junction := filepath.Join(root, "junction")
		createJunction(t, junction, realRoot)

		result, err := resolveDatabaseOwnershipStrictResult(filepath.Join(junction, ".beads", "selected"), false)
		if err != nil || result.binding != nil || result.structural == nil ||
			!databasePathsEqualForTest(t, result.structural.beadsDir, beadsDir) ||
			!databasePathsEqualForTest(t, result.structural.sourceResolved.path, beadsDir) {
			t.Fatalf("result=%#v err=%v, want canonical structural workspace %q", result, err, beadsDir)
		}
	})

	t.Run("final selector junction", func(t *testing.T) {
		beadsDir := filepath.Join(t.TempDir(), ".beads")
		providerRoot := filepath.Join(beadsDir, "embeddeddolt")
		if err := os.MkdirAll(providerRoot, 0o700); err != nil {
			t.Fatal(err)
		}
		target := t.TempDir()
		selector := filepath.Join(providerRoot, "source")
		createJunction(t, selector, target)
		assertRejected(t, selector)
	})

	t.Run("outward provider root junction", func(t *testing.T) {
		beadsDir := filepath.Join(t.TempDir(), ".beads")
		if err := os.MkdirAll(beadsDir, 0o700); err != nil {
			t.Fatal(err)
		}
		target := t.TempDir()
		if err := os.Mkdir(filepath.Join(target, "source"), 0o700); err != nil {
			t.Fatal(err)
		}
		createJunction(t, filepath.Join(beadsDir, "embeddeddolt"), target)
		assertNoStructure(t, filepath.Join(beadsDir, "embeddeddolt", "source"))
	})

	t.Run("redirect target junction", func(t *testing.T) {
		root := t.TempDir()
		source := filepath.Join(root, "source", ".beads")
		selector := filepath.Join(source, "embeddeddolt", "source")
		if err := os.MkdirAll(selector, 0o700); err != nil {
			t.Fatal(err)
		}
		realTarget := filepath.Join(root, "real-target", ".beads")
		if err := os.MkdirAll(realTarget, 0o700); err != nil {
			t.Fatal(err)
		}
		junction := filepath.Join(root, "target-junction")
		createJunction(t, junction, realTarget)
		if err := os.WriteFile(filepath.Join(source, "redirect"), []byte(junction+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		assertRejected(t, selector)
	})
}

func TestStrictDatabaseOwnershipValidatesCaseOnlyRedirectSource(t *testing.T) {
	root := t.TempDir()
	requireCaseSensitiveOwnershipDirectory(t, root)
	source := filepath.Join(root, "Workspace")
	target := filepath.Join(root, "workspace")
	dbPath := filepath.Join(target, "embeddeddolt", "source")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dbPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "redirect"), []byte(target+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configfile.ConfigPath(source), []byte(`{"database":`), 0o600); err != nil {
		t.Fatal(err)
	}
	writeOwnershipMetadata(t, target, configfile.Config{Database: "dolt", Backend: configfile.BackendDolt})

	binding, err := resolveDatabaseOwnershipStrict(dbPath, databaseWorkspaceHint{
		beadsDir:      source,
		authoritative: true,
	})
	if binding != nil || err == nil || !strings.Contains(err.Error(), "redirect source metadata") {
		t.Fatalf("binding=%#v err=%v, want malformed case-only redirect source rejection", binding, err)
	}
}

func requireCaseSensitiveOwnershipDirectory(t *testing.T, path string) {
	t.Helper()
	if output, err := exec.Command("fsutil", "file", "setCaseSensitiveInfo", path, "enable").CombinedOutput(); err != nil {
		t.Skipf("per-directory case sensitivity unavailable: %v (%s)", err, strings.TrimSpace(string(output)))
	}
}

func assertWindowsCandidatePolicy(t *testing.T, candidates []databaseOwnershipCandidate, path string, wantMode databaseOwnershipContradictionMode, wantStructural bool) {
	t.Helper()
	resolved, err := resolveCanonicalDatabasePath(path)
	if err != nil {
		t.Fatal(err)
	}
	matches := 0
	for _, candidate := range candidates {
		if !resolvedDatabasePathEqual(candidate.resolved, resolved) {
			continue
		}
		matches++
		if candidate.contradictionMode != wantMode || candidate.structural != wantStructural {
			t.Fatalf("candidate %q policy = {contradiction:%v structural:%t}, want {%v %t}",
				candidate.source, candidate.contradictionMode, candidate.structural, wantMode, wantStructural)
		}
	}
	if matches != 1 {
		t.Fatalf("physical candidate %q matches = %d, want one merged candidate", path, matches)
	}
}
