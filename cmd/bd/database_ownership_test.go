//go:build android || darwin || ios || linux || windows

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
)

func TestResolveDatabaseOwnershipStrictDoesNotMigrateLegacyConfig(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	dbPath := filepath.Join(beadsDir, "embeddeddolt", "source")
	if err := os.MkdirAll(dbPath, 0o700); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(beadsDir, "config.json")
	legacyBytes := []byte("{\n  \"database\": \"dolt\",\n  \"backend\": \"dolt\"\n}\n")
	if err := os.WriteFile(legacyPath, legacyBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	binding, err := resolveDatabaseOwnershipStrict(dbPath)
	if err != nil {
		t.Fatalf("strict resolution: %v", err)
	}
	if binding == nil || !databasePathsEqualForTest(t, binding.beadsDir, beadsDir) {
		t.Fatalf("binding = %#v, want owner %q", binding, beadsDir)
	}
	if _, err := os.Stat(configfile.ConfigPath(beadsDir)); !os.IsNotExist(err) {
		t.Fatalf("resolver created metadata.json: %v", err)
	}
	after, err := os.ReadFile(legacyPath)
	if err != nil || !bytes.Equal(after, legacyBytes) {
		t.Fatalf("resolver changed legacy config: got %q err=%v want %q", after, err, legacyBytes)
	}
}

func TestResolveDatabaseOwnershipStrictSupportsNonDotWorkspaceRoots(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), "beads-data")
	dbPath := filepath.Join(beadsDir, "embeddeddolt")
	if err := os.MkdirAll(dbPath, 0o700); err != nil {
		t.Fatal(err)
	}
	writeOwnershipMetadata(t, beadsDir, configfile.Config{Database: "dolt", Backend: configfile.BackendDolt})

	binding, err := resolveDatabaseOwnershipStrict(dbPath)
	if err != nil || binding == nil || !databasePathsEqualForTest(t, binding.beadsDir, beadsDir) {
		t.Fatalf("binding=%#v err=%v, want non-dot owner %q", binding, err, beadsDir)
	}
}

func TestResolveDatabaseOwnershipStrictRejectsStaleProviderPathAtNonDotRoot(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), "beads-data")
	dbPath := filepath.Join(beadsDir, "embeddeddolt")
	if err := os.MkdirAll(dbPath, 0o700); err != nil {
		t.Fatal(err)
	}
	writeOwnershipMetadata(t, beadsDir, configfile.Config{Database: "beads.db", Backend: configfile.BackendSQLite})

	binding, err := resolveDatabaseOwnershipStrict(dbPath)
	if binding != nil || !errors.Is(err, errDatabaseOwnershipContradiction) {
		t.Fatalf("binding=%#v err=%v, want non-dot stale-provider contradiction", binding, err)
	}
}

func TestResolveDatabaseOwnershipStrictLetsNestedRootOverrideDirectParent(t *testing.T) {
	parentBeadsDir := filepath.Join(t.TempDir(), "parent-workspace")
	nestedBeadsDir := filepath.Join(parentBeadsDir, "nested-workspace")
	writeOwnershipMetadata(t, parentBeadsDir, configfile.Config{Database: "beads.db", Backend: configfile.BackendSQLite})
	writeOwnershipMetadata(t, nestedBeadsDir, configfile.Config{Database: "dolt", Backend: configfile.BackendDolt})

	binding, err := resolveDatabaseOwnershipStrict(nestedBeadsDir)
	if err != nil || binding == nil || !databasePathsEqualForTest(t, binding.beadsDir, nestedBeadsDir) {
		t.Fatalf("binding=%#v err=%v, want nested workspace %q", binding, err, nestedBeadsDir)
	}
}

func TestResolveDatabaseOwnershipStrictPreservesSourceHierarchyAcrossRedirects(t *testing.T) {
	t.Setenv("BEADS_DIR", "")

	t.Run("parent redirects outside hierarchy", func(t *testing.T) {
		root := t.TempDir()
		parentSource := filepath.Join(root, "parent-workspace")
		nestedSource := filepath.Join(parentSource, "nested-workspace")
		parentTarget := filepath.Join(root, "shared-parent")
		if err := os.MkdirAll(nestedSource, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(parentTarget, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(parentSource, "redirect"), []byte(parentTarget+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		writeOwnershipMetadata(t, parentTarget, configfile.Config{Database: "beads.db", Backend: configfile.BackendSQLite})
		writeOwnershipMetadata(t, nestedSource, configfile.Config{Database: "dolt", Backend: configfile.BackendDolt})

		binding, err := resolveDatabaseOwnershipStrict(nestedSource)
		if err != nil || binding == nil || !databasePathsEqualForTest(t, binding.beadsDir, nestedSource) {
			t.Fatalf("binding=%#v err=%v, want nested source owner %q", binding, err, nestedSource)
		}
	})

	t.Run("nested owner redirects outside hierarchy", func(t *testing.T) {
		root := t.TempDir()
		parentSource := filepath.Join(root, "parent-workspace")
		nestedSource := filepath.Join(parentSource, "nested-workspace")
		nestedTarget := filepath.Join(root, "shared-nested")
		if err := os.MkdirAll(nestedSource, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(nestedTarget, 0o700); err != nil {
			t.Fatal(err)
		}
		writeOwnershipMetadata(t, parentSource, configfile.Config{Database: "beads.db", Backend: configfile.BackendSQLite})
		if err := os.WriteFile(filepath.Join(nestedSource, "redirect"), []byte(nestedTarget+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		writeOwnershipMetadata(t, nestedTarget, configfile.Config{Database: "dolt", Backend: configfile.BackendDolt})

		binding, err := resolveDatabaseOwnershipStrict(nestedSource)
		if err != nil || binding == nil || !databasePathsEqualForTest(t, binding.beadsDir, nestedTarget) {
			t.Fatalf("binding=%#v err=%v, want routed nested owner %q", binding, err, nestedTarget)
		}
	})

	t.Run("parent and nested owner both redirect", func(t *testing.T) {
		root := t.TempDir()
		parentSource := filepath.Join(root, "parent-workspace")
		nestedSource := filepath.Join(parentSource, "nested-workspace")
		parentTarget := filepath.Join(root, "shared-parent")
		nestedTarget := filepath.Join(root, "shared-nested")
		for _, path := range []string{nestedSource, parentTarget, nestedTarget} {
			if err := os.MkdirAll(path, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(parentSource, "redirect"), []byte(parentTarget+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(nestedSource, "redirect"), []byte(nestedTarget+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		writeOwnershipMetadata(t, parentTarget, configfile.Config{Database: "beads.db", Backend: configfile.BackendSQLite})
		writeOwnershipMetadata(t, nestedTarget, configfile.Config{Database: "dolt", Backend: configfile.BackendDolt})

		binding, err := resolveDatabaseOwnershipStrict(nestedSource)
		if err != nil || binding == nil || !databasePathsEqualForTest(t, binding.beadsDir, nestedTarget) {
			t.Fatalf("binding=%#v err=%v, want doubly routed nested owner %q", binding, err, nestedTarget)
		}
	})
}

func TestResolveDatabaseOwnershipStrictRejectsInvalidCandidateMetadata(t *testing.T) {
	for _, tt := range []struct {
		name   string
		create func(t *testing.T, path string)
	}{
		{
			name: "symlink",
			create: func(t *testing.T, path string) {
				target := filepath.Join(t.TempDir(), "metadata.json")
				if err := os.WriteFile(target, []byte(`{"database":"dolt"}`), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			},
		},
		{name: "directory", create: func(t *testing.T, path string) {
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "malformed", create: func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte(`{"database":`), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "oversized", create: func(t *testing.T, path string) {
			if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 1<<20+1), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			beadsDir := filepath.Join(t.TempDir(), ".beads")
			dbPath := filepath.Join(beadsDir, "embeddeddolt", "source")
			if err := os.MkdirAll(dbPath, 0o700); err != nil {
				t.Fatal(err)
			}
			tt.create(t, configfile.ConfigPath(beadsDir))
			binding, err := resolveDatabaseOwnershipStrict(dbPath)
			if err == nil || binding != nil {
				t.Fatalf("binding=%#v err=%v, want fail-closed metadata error", binding, err)
			}
		})
	}
}

func TestResolveDatabaseOwnershipStrictRoutesEveryCandidate(t *testing.T) {
	t.Run("invalid path-derived redirect", func(t *testing.T) {
		beadsDir := filepath.Join(t.TempDir(), ".beads")
		dbPath := filepath.Join(beadsDir, "embeddeddolt", "source")
		if err := os.MkdirAll(dbPath, 0o700); err != nil {
			t.Fatal(err)
		}
		writeOwnershipMetadata(t, beadsDir, configfile.Config{Database: "dolt", Backend: configfile.BackendDolt})
		if err := os.WriteFile(filepath.Join(beadsDir, "redirect"), []byte("missing-target\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		binding, err := resolveDatabaseOwnershipStrict(dbPath)
		if err == nil || binding != nil {
			t.Fatalf("binding=%#v err=%v, want redirect error", binding, err)
		}
	})

	t.Run("non-regular redirect markers", func(t *testing.T) {
		for _, test := range []struct {
			name   string
			create func(string) error
		}{
			{name: "directory", create: func(path string) error { return os.Mkdir(path, 0o700) }},
			{name: "dangling symlink", create: func(path string) error { return os.Symlink(path+"-missing", path) }},
		} {
			t.Run(test.name, func(t *testing.T) {
				beadsDir := filepath.Join(t.TempDir(), ".beads")
				if err := os.Mkdir(beadsDir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := test.create(filepath.Join(beadsDir, "redirect")); err != nil {
					t.Skipf("redirect marker unavailable: %v", err)
				}
				binding, err := resolveDatabaseOwnershipStrict(beadsDir)
				if binding != nil || err == nil || !strings.Contains(err.Error(), "not a regular file") {
					t.Fatalf("binding=%#v err=%v, want non-regular redirect rejection", binding, err)
				}
			})
		}
	})

	t.Run("redirect source is validation only", func(t *testing.T) {
		root := t.TempDir()
		source := filepath.Join(root, "source", ".beads")
		target := filepath.Join(root, "target", ".beads")
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
		writeOwnershipMetadata(t, source, configfile.Config{Database: "dolt", Backend: configfile.BackendDolt, DoltDataDir: filepath.Join(target, "embeddeddolt")})
		writeOwnershipMetadata(t, target, configfile.Config{Database: "dolt", Backend: configfile.BackendDolt})

		binding, err := resolveDatabaseOwnershipStrict(dbPath, databaseWorkspaceHint{
			beadsDir:      source,
			authoritative: true,
		})
		if err != nil || binding == nil || !databasePathsEqualForTest(t, binding.beadsDir, target) {
			t.Fatalf("binding=%#v err=%v, want routed target %q", binding, err, target)
		}
	})

	t.Run("redirect source workspace selector binds target", func(t *testing.T) {
		root := t.TempDir()
		source := filepath.Join(root, "source", ".beads")
		target := filepath.Join(root, "target", ".beads")
		if err := os.MkdirAll(source, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(source, "redirect"), []byte(target+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		writeOwnershipMetadata(t, target, configfile.Config{Database: "beads.db", Backend: configfile.BackendPostgres})

		binding, err := resolveDatabaseOwnershipStrict(source)
		if err != nil || binding == nil || !databasePathsEqualForTest(t, binding.beadsDir, target) || binding.backend != configfile.BackendPostgres {
			t.Fatalf("binding=%#v err=%v, want redirected workspace target %q", binding, err, target)
		}
	})

	t.Run("malformed redirect source metadata fails closed", func(t *testing.T) {
		root := t.TempDir()
		source := filepath.Join(root, "source", ".beads")
		target := filepath.Join(root, "target", ".beads")
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
		if err == nil || binding != nil || !strings.Contains(err.Error(), "redirect source metadata") {
			t.Fatalf("binding=%#v err=%v, want source-metadata error", binding, err)
		}
	})
}

func TestRouteDatabaseOwnershipCandidatesBoundsRedirectWork(t *testing.T) {
	newCandidates := func(t *testing.T, count int) []databaseOwnershipCandidate {
		t.Helper()
		root := t.TempDir()
		candidates := make([]databaseOwnershipCandidate, 0, count)
		for index := 0; index < count; index++ {
			source := filepath.Join(root, fmt.Sprintf("candidate-%03d", index))
			if err := os.Mkdir(source, 0o700); err != nil {
				t.Fatal(err)
			}
			resolved, err := resolveCanonicalDatabasePath(source)
			if err != nil {
				t.Fatal(err)
			}
			candidates = append(candidates, databaseOwnershipCandidate{source: resolved.path, resolved: resolved})
		}
		return candidates
	}

	t.Run("marker-free candidates skip strict routing", func(t *testing.T) {
		candidates := newCandidates(t, maxDatabaseOwnershipCandidates)
		probeCalls := 0
		followerCalls := 0
		routed, err := routeDatabaseOwnershipCandidatesWithDependencies(
			candidates,
			func(string) (bool, error) {
				probeCalls++
				return false, nil
			},
			func(string) (string, error) {
				followerCalls++
				return "", errors.New("strict follower must not run without a marker")
			},
		)
		if err != nil || len(routed) != len(candidates) || probeCalls != len(candidates) || followerCalls != 0 {
			t.Fatalf("routed=%d probes=%d followers=%d err=%v, want %d cheap probes and no strict calls", len(routed), probeCalls, followerCalls, err, len(candidates))
		}
	})

	t.Run("exact redirect limit is accepted and merged", func(t *testing.T) {
		candidates := newCandidates(t, maxDatabaseOwnershipRedirectCandidates)
		sharedTarget := t.TempDir()
		followerCalls := 0
		routed, err := routeDatabaseOwnershipCandidatesWithDependencies(
			candidates,
			func(string) (bool, error) { return true, nil },
			func(string) (string, error) {
				followerCalls++
				return sharedTarget, nil
			},
		)
		if err != nil || len(routed) != 1 || followerCalls != maxDatabaseOwnershipRedirectCandidates {
			t.Fatalf("routed=%#v followers=%d err=%v, want one merged target after %d calls", routed, followerCalls, err, maxDatabaseOwnershipRedirectCandidates)
		}
		if len(routed[0].sources) != maxDatabaseOwnershipRedirectCandidates {
			t.Fatalf("merged sources=%d, want %d", len(routed[0].sources), maxDatabaseOwnershipRedirectCandidates)
		}
	})

	t.Run("redirect over limit stops before the next strict call", func(t *testing.T) {
		candidates := newCandidates(t, maxDatabaseOwnershipRedirectCandidates+1)
		sharedTarget := t.TempDir()
		followerCalls := 0
		routed, err := routeDatabaseOwnershipCandidatesWithDependencies(
			candidates,
			func(string) (bool, error) { return true, nil },
			func(string) (string, error) {
				followerCalls++
				return sharedTarget, nil
			},
		)
		if routed != nil || !errors.Is(err, errDatabaseOwnershipLimit) || followerCalls != maxDatabaseOwnershipRedirectCandidates {
			t.Fatalf("routed=%#v followers=%d err=%v, want limit before call %d", routed, followerCalls, err, maxDatabaseOwnershipRedirectCandidates+1)
		}
	})

	t.Run("probe failure stops before strict routing", func(t *testing.T) {
		candidates := newCandidates(t, 1)
		wantErr := errors.New("probe failed")
		followerCalls := 0
		routed, err := routeDatabaseOwnershipCandidatesWithDependencies(
			candidates,
			func(string) (bool, error) { return false, wantErr },
			func(string) (string, error) {
				followerCalls++
				return "", nil
			},
		)
		if routed != nil || !errors.Is(err, wantErr) || followerCalls != 0 {
			t.Fatalf("routed=%#v followers=%d err=%v, want fail-closed probe error", routed, followerCalls, err)
		}
	})

	t.Run("observed marker disappearance fails closed", func(t *testing.T) {
		candidates := newCandidates(t, 1)
		followerCalls := 0
		routed, err := routeDatabaseOwnershipCandidatesWithDependencies(
			candidates,
			func(string) (bool, error) { return true, nil },
			func(source string) (string, error) {
				followerCalls++
				return source, nil
			},
		)
		if routed != nil || err == nil || followerCalls != 1 || !strings.Contains(err.Error(), "changed during inspection") {
			t.Fatalf("routed=%#v followers=%d err=%v, want disappeared-marker rejection", routed, followerCalls, err)
		}
	})
}

func TestResolveDatabaseOwnershipStrictRejectsContradictionAndAmbiguity(t *testing.T) {
	t.Run("contradictory authoritative metadata", func(t *testing.T) {
		beadsDir := filepath.Join(t.TempDir(), ".beads")
		selected := filepath.Join(beadsDir, "dolt", "source")
		if err := os.MkdirAll(selected, 0o700); err != nil {
			t.Fatal(err)
		}
		writeOwnershipMetadata(t, beadsDir, configfile.Config{Database: "dolt", Backend: configfile.BackendDolt, DoltMode: configfile.DoltModeEmbedded})
		binding, err := resolveDatabaseOwnershipStrict(selected)
		if err == nil || binding != nil || !errors.Is(err, errDatabaseOwnershipContradiction) {
			t.Fatalf("binding=%#v err=%v, want ownership contradiction", binding, err)
		}
	})

	t.Run("authoritative hint requires metadata", func(t *testing.T) {
		beadsDir := filepath.Join(t.TempDir(), ".beads")
		dbPath := filepath.Join(t.TempDir(), "external-dolt", "source")
		if err := os.MkdirAll(beadsDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(dbPath, 0o700); err != nil {
			t.Fatal(err)
		}
		binding, err := resolveDatabaseOwnershipStrict(dbPath, databaseWorkspaceHint{
			beadsDir:      beadsDir,
			authoritative: true,
		})
		if binding != nil || !errors.Is(err, errDatabaseOwnershipContradiction) {
			t.Fatalf("binding=%#v err=%v, want missing-metadata contradiction", binding, err)
		}
	})

	t.Run("authoritative hint cannot fall back to another owner", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "external-dolt", "source")
		if err := os.MkdirAll(dbPath, 0o700); err != nil {
			t.Fatal(err)
		}
		wrong := filepath.Join(t.TempDir(), ".beads")
		owner := filepath.Join(t.TempDir(), ".beads")
		writeOwnershipMetadata(t, wrong, configfile.Config{Database: "beads.db", Backend: configfile.BackendSQLite})
		writeOwnershipMetadata(t, owner, configfile.Config{Database: "dolt", Backend: configfile.BackendDolt, DoltDataDir: filepath.Dir(dbPath)})
		binding, err := resolveDatabaseOwnershipStrict(dbPath,
			databaseWorkspaceHint{beadsDir: wrong, authoritative: true},
			databaseWorkspaceHint{beadsDir: owner},
		)
		if binding != nil || !errors.Is(err, errDatabaseOwnershipContradiction) {
			t.Fatalf("binding=%#v err=%v, want authoritative contradiction", binding, err)
		}
	})

	t.Run("two physical owners", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "external-dolt", "source")
		if err := os.MkdirAll(dbPath, 0o700); err != nil {
			t.Fatal(err)
		}
		var hints []databaseWorkspaceHint
		for i := 0; i < 2; i++ {
			beadsDir := filepath.Join(t.TempDir(), ".beads")
			if err := os.Mkdir(beadsDir, 0o700); err != nil {
				t.Fatal(err)
			}
			writeOwnershipMetadata(t, beadsDir, configfile.Config{Database: "dolt", Backend: configfile.BackendDolt, DoltDataDir: filepath.Dir(dbPath)})
			hints = append(hints, databaseWorkspaceHint{beadsDir: beadsDir})
		}
		binding, err := resolveDatabaseOwnershipStrict(dbPath, hints...)
		if err == nil || binding != nil || !errors.Is(err, errDatabaseOwnershipAmbiguous) {
			t.Fatalf("binding=%#v err=%v, want ambiguous-owner error", binding, err)
		}
		wantError := err.Error()
		hints[0], hints[1] = hints[1], hints[0]
		binding, err = resolveDatabaseOwnershipStrict(dbPath, hints...)
		if err == nil || binding != nil || err.Error() != wantError {
			t.Fatalf("reordered ambiguity binding=%#v err=%v, want stable %q", binding, err, wantError)
		}
	})
}

func TestResolveDatabaseOwnershipStrictProviderPaths(t *testing.T) {
	root := t.TempDir()
	absCustom := filepath.Join(root, "absolute-custom")
	absSQLite := filepath.Join(root, "absolute.db")
	for _, tt := range []struct {
		name        string
		cfg         configfile.Config
		selector    func(string) string
		wantBackend string
	}{
		{
			name:        "dolt embedded descendant",
			cfg:         configfile.Config{Database: "dolt", Backend: configfile.BackendDolt},
			selector:    func(dir string) string { return filepath.Join(dir, "embeddeddolt", "source") },
			wantBackend: configfile.BackendDolt,
		},
		{
			name:        "dolt server",
			cfg:         configfile.Config{Database: "dolt", Backend: configfile.BackendDolt, DoltMode: configfile.DoltModeServer},
			selector:    func(dir string) string { return filepath.Join(dir, "dolt") },
			wantBackend: configfile.BackendDolt,
		},
		{
			name:        "dolt proxied descendant",
			cfg:         configfile.Config{Database: "dolt", Backend: configfile.BackendDolt, DoltMode: configfile.DoltModeProxiedServer},
			selector:    func(dir string) string { return filepath.Join(dir, "proxieddb", "source") },
			wantBackend: configfile.BackendDolt,
		},
		{
			name:        "dolt relative custom",
			cfg:         configfile.Config{Database: "dolt", Backend: configfile.BackendDolt, DoltDataDir: "custom"},
			selector:    func(dir string) string { return filepath.Join(dir, "custom", "source") },
			wantBackend: configfile.BackendDolt,
		},
		{
			name:        "dolt absolute custom",
			cfg:         configfile.Config{Database: "dolt", Backend: configfile.BackendDolt, DoltDataDir: absCustom},
			selector:    func(string) string { return filepath.Join(absCustom, "source") },
			wantBackend: configfile.BackendDolt,
		},
		{
			name:        "legacy absolute database",
			cfg:         configfile.Config{Database: absCustom, Backend: configfile.BackendDolt},
			selector:    func(string) string { return filepath.Join(absCustom, "source") },
			wantBackend: configfile.BackendDolt,
		},
		{
			name:        "sqlite default file",
			cfg:         configfile.Config{Database: "beads.db", Backend: configfile.BackendSQLite},
			selector:    func(dir string) string { return filepath.Join(dir, "beads.db") },
			wantBackend: configfile.BackendSQLite,
		},
		{
			name:        "sqlite custom file",
			cfg:         configfile.Config{Database: "beads.db", Backend: configfile.BackendSQLite, SQLitePath: "custom.db"},
			selector:    func(dir string) string { return filepath.Join(dir, "custom.db") },
			wantBackend: configfile.BackendSQLite,
		},
		{
			name:        "sqlite absolute file",
			cfg:         configfile.Config{Database: "beads.db", Backend: configfile.BackendSQLite, SQLitePath: absSQLite},
			selector:    func(string) string { return absSQLite },
			wantBackend: configfile.BackendSQLite,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			beadsDir := filepath.Join(t.TempDir(), ".beads")
			if err := os.Mkdir(beadsDir, 0o700); err != nil {
				t.Fatal(err)
			}
			writeOwnershipMetadata(t, beadsDir, tt.cfg)
			selector := tt.selector(beadsDir)
			if err := os.MkdirAll(filepath.Dir(selector), 0o700); err != nil {
				t.Fatal(err)
			}
			binding, err := resolveDatabaseOwnershipStrict(selector, databaseWorkspaceHint{beadsDir: beadsDir, authoritative: true})
			if err != nil || binding == nil || binding.backend != tt.wantBackend {
				t.Fatalf("binding=%#v err=%v, want backend %q", binding, err, tt.wantBackend)
			}
			wantScope := databaseOwnershipScopeDescendant
			if tt.wantBackend == configfile.BackendSQLite {
				wantScope = databaseOwnershipScopeExact
			}
			if binding.scope != wantScope || binding.beadsResolved == nil || binding.ownedResolved == nil {
				t.Fatalf("binding evidence=%#v, want scope %v with retained observations", binding, wantScope)
			}
		})
	}
}

func TestResolveDatabaseOwnershipStrictWorkspaceRootMayBindAnyProvider(t *testing.T) {
	for _, tt := range []struct {
		name      string
		cfg       configfile.Config
		ownedPath func(string) string
	}{
		{
			name:      "dolt",
			cfg:       configfile.Config{Database: "dolt", Backend: configfile.BackendDolt},
			ownedPath: func(beadsDir string) string { return filepath.Join(beadsDir, "embeddeddolt") },
		},
		{
			name:      "dolt custom path",
			cfg:       configfile.Config{Database: "dolt", Backend: configfile.BackendDolt, DoltDataDir: "custom-dolt"},
			ownedPath: func(beadsDir string) string { return filepath.Join(beadsDir, "custom-dolt") },
		},
		{
			name:      "sqlite",
			cfg:       configfile.Config{Database: "beads.db", Backend: configfile.BackendSQLite},
			ownedPath: func(beadsDir string) string { return filepath.Join(beadsDir, "beads.db") },
		},
		{
			name:      "sqlite custom path",
			cfg:       configfile.Config{Database: "beads.db", Backend: configfile.BackendSQLite, SQLitePath: "custom.db"},
			ownedPath: func(beadsDir string) string { return filepath.Join(beadsDir, "custom.db") },
		},
		{
			name:      "postgres",
			cfg:       configfile.Config{Database: "beads.db", Backend: configfile.BackendPostgres},
			ownedPath: func(beadsDir string) string { return beadsDir },
		},
		{
			name:      "mysql",
			cfg:       configfile.Config{Database: "beads.db", Backend: configfile.BackendMySQL},
			ownedPath: func(beadsDir string) string { return beadsDir },
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			beadsDir := filepath.Join(t.TempDir(), ".beads")
			writeOwnershipMetadata(t, beadsDir, tt.cfg)
			binding, err := resolveDatabaseOwnershipStrict(beadsDir, databaseWorkspaceHint{
				beadsDir:      beadsDir,
				authoritative: true,
			})
			wantOwnedPath := tt.ownedPath(beadsDir)
			if err != nil || binding == nil || binding.backend != tt.cfg.GetBackend() || !databasePathsEqualForTest(t, binding.ownedPath, wantOwnedPath) {
				t.Fatalf("binding=%#v err=%v, want %s workspace with owned path %q", binding, err, tt.cfg.GetBackend(), wantOwnedPath)
			}
			if binding.scope != databaseOwnershipScopeWorkspace || binding.beadsResolved == nil || binding.ownedResolved == nil {
				t.Fatalf("binding evidence=%#v, want retained workspace-scope observations", binding)
			}
		})
	}
}

func TestResolveDatabaseOwnershipStrictProviderPathBoundaries(t *testing.T) {
	t.Run("sqlite descendant is not the database", func(t *testing.T) {
		beadsDir := filepath.Join(t.TempDir(), ".beads")
		selector := filepath.Join(beadsDir, "beads.db", "child")
		if err := os.MkdirAll(filepath.Dir(selector), 0o700); err != nil {
			t.Fatal(err)
		}
		writeOwnershipMetadata(t, beadsDir, configfile.Config{Database: "beads.db", Backend: configfile.BackendSQLite})
		binding, err := resolveDatabaseOwnershipStrict(selector, databaseWorkspaceHint{beadsDir: beadsDir})
		if binding != nil || err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("binding=%#v err=%v, want SQLite directory rejection", binding, err)
		}
	})

	for _, backend := range []string{configfile.BackendPostgres, configfile.BackendMySQL} {
		t.Run(backend+" has no filesystem database path", func(t *testing.T) {
			beadsDir := filepath.Join(t.TempDir(), ".beads")
			selector := filepath.Join(t.TempDir(), "remote-provider-selector")
			if err := os.Mkdir(selector, 0o700); err != nil {
				t.Fatal(err)
			}
			writeOwnershipMetadata(t, beadsDir, configfile.Config{Database: "beads.db", Backend: backend})
			binding, err := resolveDatabaseOwnershipStrict(selector, databaseWorkspaceHint{beadsDir: beadsDir})
			if err != nil || binding != nil {
				t.Fatalf("binding=%#v err=%v, want no filesystem owner", binding, err)
			}
		})
	}
}

func TestResolveDatabaseOwnershipStrictRejectsInvalidOwnedPathTypes(t *testing.T) {
	t.Run("Dolt data path is a regular file", func(t *testing.T) {
		beadsDir := filepath.Join(t.TempDir(), ".beads")
		ownedPath := filepath.Join(beadsDir, "dolt-data")
		writeOwnershipMetadata(t, beadsDir, configfile.Config{
			Database:    "dolt",
			Backend:     configfile.BackendDolt,
			DoltDataDir: ownedPath,
		})
		if err := os.WriteFile(ownedPath, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		binding, err := resolveDatabaseOwnershipStrict(beadsDir, databaseWorkspaceHint{beadsDir: beadsDir, authoritative: true})
		if binding != nil || err == nil || !strings.Contains(err.Error(), "not a directory") {
			t.Fatalf("binding=%#v err=%v, want Dolt file rejection", binding, err)
		}
	})

	t.Run("SQLite database path is a directory", func(t *testing.T) {
		beadsDir := filepath.Join(t.TempDir(), ".beads")
		ownedPath := filepath.Join(beadsDir, "beads.db")
		writeOwnershipMetadata(t, beadsDir, configfile.Config{
			Database:   "beads.db",
			Backend:    configfile.BackendSQLite,
			SQLitePath: ownedPath,
		})
		if err := os.Mkdir(ownedPath, 0o700); err != nil {
			t.Fatal(err)
		}
		binding, err := resolveDatabaseOwnershipStrict(beadsDir, databaseWorkspaceHint{beadsDir: beadsDir, authoritative: true})
		if binding != nil || err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("binding=%#v err=%v, want SQLite directory rejection", binding, err)
		}
	})
}

func TestResolveDatabaseOwnershipStrictIgnoresInvalidUnrelatedParentProviderPath(t *testing.T) {
	for _, backend := range []string{configfile.BackendDolt, configfile.BackendSQLite} {
		t.Run(backend, func(t *testing.T) {
			root := t.TempDir()
			parentBeadsDir := filepath.Join(root, ".beads")
			nestedBeadsDir := filepath.Join(root, "nested", ".beads")
			selector := filepath.Join(nestedBeadsDir, "embeddeddolt", "source")
			if err := os.MkdirAll(selector, 0o700); err != nil {
				t.Fatal(err)
			}
			writeOwnershipMetadata(t, nestedBeadsDir, configfile.Config{Database: "dolt", Backend: configfile.BackendDolt})

			invalidPath := filepath.Join(parentBeadsDir, "invalid-provider-path")
			cfg := configfile.Config{Database: "dolt", Backend: backend}
			if backend == configfile.BackendDolt {
				cfg.DoltDataDir = invalidPath
				writeOwnershipMetadata(t, parentBeadsDir, cfg)
				if err := os.WriteFile(invalidPath, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				cfg.Database = "beads.db"
				cfg.SQLitePath = invalidPath
				writeOwnershipMetadata(t, parentBeadsDir, cfg)
				if err := os.Mkdir(invalidPath, 0o700); err != nil {
					t.Fatal(err)
				}
			}

			binding, err := resolveDatabaseOwnershipStrict(selector)
			if err != nil || binding == nil || !databasePathsEqualForTest(t, binding.beadsDir, nestedBeadsDir) {
				t.Fatalf("binding=%#v err=%v, want nested owner %q", binding, err, nestedBeadsDir)
			}
		})
	}
}

func TestResolveDatabaseOwnershipStrictRejectsHardLinkedSQLiteWithOrWithoutHints(t *testing.T) {
	t.Setenv("BEADS_DIR", "")
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	databasePath := filepath.Join(beadsDir, "beads.db")
	writeOwnershipMetadata(t, beadsDir, configfile.Config{
		Database:   "beads.db",
		Backend:    configfile.BackendSQLite,
		SQLitePath: databasePath,
	})
	if err := os.WriteFile(databasePath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	aliasPath := filepath.Join(beadsDir, "beads-alias.db")
	if err := os.Link(databasePath, aliasPath); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}

	for _, test := range []struct {
		name     string
		selector string
		hints    []databaseWorkspaceHint
	}{
		{name: "database path without hint", selector: databasePath},
		{name: "database path with hint", selector: databasePath, hints: []databaseWorkspaceHint{{beadsDir: beadsDir, authoritative: true}}},
		{name: "workspace root without hint", selector: beadsDir},
		{name: "workspace root with hint", selector: beadsDir, hints: []databaseWorkspaceHint{{beadsDir: beadsDir, authoritative: true}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			binding, err := resolveDatabaseOwnershipStrict(test.selector, test.hints...)
			if binding != nil || err == nil || !strings.Contains(err.Error(), "hard links") {
				t.Fatalf("binding=%#v err=%v, want hard-link rejection", binding, err)
			}
		})
	}
}

func TestResolveDatabaseOwnershipStrictBoundsDiscovery(t *testing.T) {
	t.Run("workspace hints", func(t *testing.T) {
		t.Setenv("BEADS_DIR", "")
		selector := filepath.Join(t.TempDir(), "future.db")
		hints := make([]databaseWorkspaceHint, maxDatabaseWorkspaceHints+1)
		binding, err := resolveDatabaseOwnershipStrict(selector, hints...)
		if binding != nil || !errors.Is(err, errDatabaseOwnershipLimit) {
			t.Fatalf("binding=%#v err=%v, want workspace-hint limit", binding, err)
		}
	})

	t.Run("candidate workspaces", func(t *testing.T) {
		root := t.TempDir()
		selector, err := validatedDatabaseSelector(filepath.Join(root, "future.db"))
		if err != nil {
			t.Fatal(err)
		}
		candidates := make([]databaseOwnershipCandidate, 0, maxDatabaseOwnershipCandidates)
		for index := 0; index <= maxDatabaseOwnershipCandidates; index++ {
			candidateRoot := filepath.Join(root, fmt.Sprintf("candidate-%03d", index))
			if err := os.Mkdir(candidateRoot, 0o700); err != nil {
				t.Fatal(err)
			}
			candidates, err = appendDatabaseOwnershipCandidate(candidates, selector, databaseOwnershipCandidate{
				source: filepath.Join(candidateRoot, ".beads"),
			})
			if index < maxDatabaseOwnershipCandidates && err != nil {
				t.Fatalf("candidate %d: %v", index, err)
			}
		}
		if !errors.Is(err, errDatabaseOwnershipLimit) {
			t.Fatalf("candidate limit error = %v", err)
		}
	})
}

func TestResolveDatabaseOwnershipStrictReturnsNoOwnerWithoutEvidence(t *testing.T) {
	selector := filepath.Join(t.TempDir(), "future.db")
	binding, err := resolveDatabaseOwnershipStrict(selector)
	if err != nil || binding != nil {
		t.Fatalf("binding=%#v err=%v, want no ownership evidence", binding, err)
	}
}

func TestResolveDatabaseOwnershipStrictEscapesControlPaths(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), "unsafe\n\x1b[31m", ".beads")
	selector := filepath.Join(beadsDir, "embeddeddolt", "source")
	if err := os.MkdirAll(selector, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configfile.ConfigPath(beadsDir), []byte(`{"database":`), 0o600); err != nil {
		t.Fatal(err)
	}
	binding, err := resolveDatabaseOwnershipStrict(selector)
	if binding != nil || err == nil {
		t.Fatalf("binding=%#v err=%v, want malformed metadata rejection", binding, err)
	}
	if strings.ContainsAny(err.Error(), "\n\x1b") {
		t.Fatalf("control-unsafe ownership error = %q", err)
	}
}

func TestResolveDatabaseOwnershipStrictRequiresCallerHintForCWDWorkspace(t *testing.T) {
	root := t.TempDir()
	beadsDir := filepath.Join(root, ".beads")
	dbPath := filepath.Join(t.TempDir(), "external-dolt", "source")
	if err := os.MkdirAll(dbPath, 0o700); err != nil {
		t.Fatal(err)
	}
	writeOwnershipMetadata(t, beadsDir, configfile.Config{
		Database:    "dolt",
		Backend:     configfile.BackendDolt,
		DoltDataDir: filepath.Dir(dbPath),
	})
	t.Chdir(root)
	t.Setenv("BEADS_DIR", "")

	binding, err := resolveDatabaseOwnershipStrict(dbPath)
	if err != nil || binding != nil {
		t.Fatalf("binding=%#v err=%v, want no implicit CWD evidence", binding, err)
	}
	binding, err = resolveDatabaseOwnershipStrict(dbPath, databaseWorkspaceHint{beadsDir: beadsDir})
	if err != nil || binding == nil || !databasePathsEqualForTest(t, binding.beadsDir, beadsDir) {
		t.Fatalf("binding=%#v err=%v, want caller-supplied owner %q", binding, err, beadsDir)
	}
}

func TestResolveDatabaseOwnershipStrictDoesNotClaimStaleDoltPathsForOtherBackends(t *testing.T) {
	for _, backend := range []string{configfile.BackendSQLite, configfile.BackendPostgres, configfile.BackendMySQL} {
		t.Run(backend, func(t *testing.T) {
			beadsDir := filepath.Join(t.TempDir(), ".beads")
			staleDolt := filepath.Join(beadsDir, "embeddeddolt", "source")
			if err := os.MkdirAll(staleDolt, 0o700); err != nil {
				t.Fatal(err)
			}
			writeOwnershipMetadata(t, beadsDir, configfile.Config{Database: "beads.db", Backend: backend, SQLitePath: "beads.db"})
			binding, err := resolveDatabaseOwnershipStrict(staleDolt)
			if err == nil || binding != nil || !strings.Contains(err.Error(), "contradicts") {
				t.Fatalf("binding=%#v err=%v, want stale-Dolt contradiction", binding, err)
			}
		})
	}
}

func TestResolveDatabaseOwnershipStrictEnvironmentPolicyIsRetained(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "external-dolt", "source")
	if err := os.MkdirAll(dbPath, 0o700); err != nil {
		t.Fatal(err)
	}
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeOwnershipMetadata(t, beadsDir, configfile.Config{Database: "dolt", Backend: configfile.BackendDolt})
	t.Setenv("BEADS_DOLT_DATA_DIR", filepath.Dir(dbPath))

	binding, err := resolveDatabaseOwnershipStrict(dbPath, databaseWorkspaceHint{beadsDir: beadsDir})
	if err != nil || binding != nil {
		t.Fatalf("ambient override binding=%#v err=%v, want no owner", binding, err)
	}
	binding, err = resolveDatabaseOwnershipStrict(dbPath, databaseWorkspaceHint{
		beadsDir:                beadsDir,
		allowEnvironmentDataDir: true,
		authoritative:           true,
	})
	if err != nil || binding == nil || binding.source != databaseOwnershipExplicitEnvironment || !databasePathsEqualForTest(t, binding.ownedPath, filepath.Dir(dbPath)) {
		t.Fatalf("explicit binding=%#v err=%v, want retained environment policy", binding, err)
	}
}

func TestResolveDatabaseOwnershipStrictAuthoritativeHintSuppressesAmbientWorkspace(t *testing.T) {
	root := t.TempDir()
	selectedBeadsDir := filepath.Join(root, "selected", ".beads")
	ambientBeadsDir := filepath.Join(root, "ambient", ".beads")
	dataRoot := filepath.Join(root, "external-dolt")
	selector := filepath.Join(dataRoot, "source")
	if err := os.MkdirAll(selector, 0o700); err != nil {
		t.Fatal(err)
	}
	writeOwnershipMetadata(t, selectedBeadsDir, configfile.Config{
		Database:    "dolt",
		Backend:     configfile.BackendDolt,
		DoltDataDir: dataRoot,
	})
	writeOwnershipMetadata(t, ambientBeadsDir, configfile.Config{Database: "dolt", Backend: configfile.BackendDolt})
	t.Setenv("BEADS_DIR", ambientBeadsDir)
	t.Setenv("BEADS_DOLT_DATA_DIR", dataRoot)

	binding, err := resolveDatabaseOwnershipStrict(selector, databaseWorkspaceHint{
		beadsDir:      selectedBeadsDir,
		authoritative: true,
	})
	if err != nil || binding == nil || !databasePathsEqualForTest(t, binding.beadsDir, selectedBeadsDir) {
		t.Fatalf("binding=%#v err=%v, want authoritative selected owner %q", binding, err, selectedBeadsDir)
	}
	if binding.source != databaseOwnershipPersisted {
		t.Fatalf("binding source=%v, want persisted selected-workspace evidence", binding.source)
	}
}

func TestResolveDatabaseOwnershipStrictIgnoresMissingNonAuthoritativeHint(t *testing.T) {
	t.Setenv("BEADS_DIR", "")
	selector := filepath.Join(t.TempDir(), "future.db")
	missingHint := filepath.Join(t.TempDir(), "missing", "deep", ".beads")
	binding, err := resolveDatabaseOwnershipStrict(selector, databaseWorkspaceHint{beadsDir: missingHint})
	if err != nil || binding != nil {
		t.Fatalf("binding=%#v err=%v, want missing non-authoritative hint ignored", binding, err)
	}
}

func TestResolveDatabaseOwnershipStrictRejectsDanglingNonAuthoritativeHint(t *testing.T) {
	t.Setenv("BEADS_DIR", "")
	root := t.TempDir()
	dangling := filepath.Join(root, "dangling")
	if err := os.Symlink(filepath.Join(root, "missing-target"), dangling); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	selector := filepath.Join(t.TempDir(), "future.db")

	for _, test := range []struct {
		name string
		hint string
	}{
		{name: "final symlink", hint: dangling},
		{name: "dangling ancestor", hint: filepath.Join(dangling, "nested", ".beads")},
	} {
		t.Run(test.name, func(t *testing.T) {
			binding, err := resolveDatabaseOwnershipStrict(selector, databaseWorkspaceHint{beadsDir: test.hint})
			if err == nil || binding != nil {
				t.Fatalf("binding=%#v err=%v, want dangling non-authoritative hint rejection", binding, err)
			}
		})
	}
}

func TestResolveDatabaseOwnershipStrictEnvironmentPermissionIsInputOnly(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	dbPath := filepath.Join(beadsDir, "embeddeddolt", "source")
	if err := os.MkdirAll(dbPath, 0o700); err != nil {
		t.Fatal(err)
	}
	writeOwnershipMetadata(t, beadsDir, configfile.Config{Database: "dolt", Backend: configfile.BackendDolt})
	t.Setenv("BEADS_DOLT_DATA_DIR", "")

	binding, err := resolveDatabaseOwnershipStrict(dbPath, databaseWorkspaceHint{
		beadsDir:                beadsDir,
		allowEnvironmentDataDir: true,
		authoritative:           true,
	})
	if err != nil || binding == nil || binding.source != databaseOwnershipPersisted || !databasePathsEqualForTest(t, binding.ownedPath, filepath.Join(beadsDir, "embeddeddolt")) {
		t.Fatalf("binding=%#v err=%v, want persisted source without an environment override", binding, err)
	}
}

func TestResolveDatabaseOwnershipStrictEnvironmentOverrideReplacesPersistedPath(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	persistedSelector := filepath.Join(beadsDir, "embeddeddolt", "source")
	if err := os.MkdirAll(persistedSelector, 0o700); err != nil {
		t.Fatal(err)
	}
	writeOwnershipMetadata(t, beadsDir, configfile.Config{Database: "dolt", Backend: configfile.BackendDolt})
	t.Setenv("BEADS_DOLT_DATA_DIR", filepath.Join(t.TempDir(), "override"))

	binding, err := resolveDatabaseOwnershipStrict(persistedSelector, databaseWorkspaceHint{
		beadsDir:                beadsDir,
		allowEnvironmentDataDir: true,
		authoritative:           true,
	})
	if binding != nil || !errors.Is(err, errDatabaseOwnershipContradiction) {
		t.Fatalf("binding=%#v err=%v, want environment override to replace persisted ownership", binding, err)
	}
}

func TestResolveDatabaseOwnershipStrictDoesNotMergeSplitEnvironmentAuthority(t *testing.T) {
	root := t.TempDir()
	beadsDir := filepath.Join(root, "real", ".beads")
	dbPath := filepath.Join(root, "external", "source")
	if err := os.MkdirAll(dbPath, 0o700); err != nil {
		t.Fatal(err)
	}
	writeOwnershipMetadata(t, beadsDir, configfile.Config{Database: "dolt", Backend: configfile.BackendDolt})
	aliasRoot := filepath.Join(root, "alias")
	if err := os.Symlink(filepath.Join(root, "real"), aliasRoot); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	t.Setenv("BEADS_DOLT_DATA_DIR", filepath.Dir(dbPath))

	binding, err := resolveDatabaseOwnershipStrict(dbPath,
		databaseWorkspaceHint{beadsDir: filepath.Join(aliasRoot, ".beads"), allowEnvironmentDataDir: true},
		databaseWorkspaceHint{beadsDir: beadsDir, authoritative: true},
	)
	if binding != nil || !errors.Is(err, errDatabaseOwnershipContradiction) {
		t.Fatalf("binding=%#v err=%v, want split environment authority rejected", binding, err)
	}
}

func TestResolveDatabaseOwnershipStrictRelativeEnvironmentPath(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	dbPath := filepath.Join(beadsDir, "external", "source")
	if err := os.MkdirAll(dbPath, 0o700); err != nil {
		t.Fatal(err)
	}
	writeOwnershipMetadata(t, beadsDir, configfile.Config{Database: "dolt", Backend: configfile.BackendDolt})
	t.Setenv("BEADS_DOLT_DATA_DIR", "external")

	binding, err := resolveDatabaseOwnershipStrict(dbPath, databaseWorkspaceHint{
		beadsDir:                beadsDir,
		allowEnvironmentDataDir: true,
		authoritative:           true,
	})
	if err != nil || binding == nil || !databasePathsEqualForTest(t, binding.ownedPath, filepath.Join(beadsDir, "external")) {
		t.Fatalf("binding=%#v err=%v, want workspace-relative environment root", binding, err)
	}
}

func TestResolveDatabaseOwnershipStrictDeduplicatesWorkspaceAliases(t *testing.T) {
	root := t.TempDir()
	beadsDir := filepath.Join(root, "real", ".beads")
	dbPath := filepath.Join(beadsDir, "embeddeddolt", "source")
	if err := os.MkdirAll(dbPath, 0o700); err != nil {
		t.Fatal(err)
	}
	writeOwnershipMetadata(t, beadsDir, configfile.Config{Database: "dolt", Backend: configfile.BackendDolt})
	aliasRoot := filepath.Join(root, "alias")
	if err := os.Symlink(filepath.Join(root, "real"), aliasRoot); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	aliasBeadsDir := filepath.Join(aliasRoot, ".beads")

	binding, err := resolveDatabaseOwnershipStrict(dbPath,
		databaseWorkspaceHint{beadsDir: beadsDir},
		databaseWorkspaceHint{beadsDir: aliasBeadsDir},
	)
	if err != nil || binding == nil || !databasePathsEqualForTest(t, binding.beadsDir, beadsDir) {
		t.Fatalf("binding=%#v err=%v, want one canonical owner", binding, err)
	}
}

func TestDatabasePathOwnershipCandidatesDeduplicateMissingAliases(t *testing.T) {
	root := t.TempDir()
	realRoot := filepath.Join(root, "real")
	if err := os.Mkdir(realRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	aliasRoot := filepath.Join(root, "alias")
	if err := os.Symlink(realRoot, aliasRoot); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	original := filepath.Join(aliasRoot, "future.db")
	selector, err := validatedDatabaseSelector(filepath.Join(realRoot, "future.db"))
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := databasePathOwnershipCandidates(original, selector)
	if err != nil {
		t.Fatal(err)
	}
	for left := range candidates {
		for right := left + 1; right < len(candidates); right++ {
			equal, err := databasePathEqual(candidates[left].source, candidates[right].source)
			if err != nil {
				t.Fatal(err)
			}
			if equal {
				t.Fatalf("equivalent missing candidates were retained: %q and %q", candidates[left].source, candidates[right].source)
			}
		}
	}
}

func TestResolveDatabaseOwnershipStrictCanonicalizesSelectorAliases(t *testing.T) {
	root := t.TempDir()
	realRoot := filepath.Join(root, "real")
	beadsDir := filepath.Join(realRoot, ".beads")
	dbPath := filepath.Join(beadsDir, "embeddeddolt", "source")
	if err := os.MkdirAll(dbPath, 0o700); err != nil {
		t.Fatal(err)
	}
	writeOwnershipMetadata(t, beadsDir, configfile.Config{Database: "dolt", Backend: configfile.BackendDolt})
	aliasRoot := filepath.Join(root, "alias")
	if err := os.Symlink(realRoot, aliasRoot); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	binding, err := resolveDatabaseOwnershipStrict(filepath.Join(aliasRoot, ".beads", "embeddeddolt", "source"))
	if err != nil || binding == nil || !databasePathsEqualForTest(t, binding.beadsDir, beadsDir) {
		t.Fatalf("binding=%#v err=%v, want canonical selector owner %q", binding, err, beadsDir)
	}
}

func TestResolveDatabaseOwnershipStrictDoesNotLetParentWorkspaceContradictNestedOwner(t *testing.T) {
	root := t.TempDir()
	parentBeadsDir := filepath.Join(root, ".beads")
	nestedBeadsDir := filepath.Join(root, "nested", ".beads")
	dbPath := filepath.Join(nestedBeadsDir, "embeddeddolt", "source")
	if err := os.MkdirAll(dbPath, 0o700); err != nil {
		t.Fatal(err)
	}
	writeOwnershipMetadata(t, parentBeadsDir, configfile.Config{Database: "beads.db", Backend: configfile.BackendSQLite})
	writeOwnershipMetadata(t, nestedBeadsDir, configfile.Config{Database: "dolt", Backend: configfile.BackendDolt})

	binding, err := resolveDatabaseOwnershipStrict(dbPath)
	if err != nil || binding == nil || !databasePathsEqualForTest(t, binding.beadsDir, nestedBeadsDir) {
		t.Fatalf("binding=%#v err=%v, want nested owner %q", binding, err, nestedBeadsDir)
	}
}

func TestDatabaseResolutionHintsStrictRejectsInvalidExplicitBeadsDir(t *testing.T) {
	for _, tt := range []struct {
		name string
		path func(string) string
		make func(string) error
	}{
		{name: "missing", path: func(root string) string { return filepath.Join(root, "missing") }, make: func(string) error { return nil }},
		{name: "file", path: func(root string) string { return filepath.Join(root, "file") }, make: func(path string) error { return os.WriteFile(path, nil, 0o600) }},
		{name: "dangling symlink", path: func(root string) string { return filepath.Join(root, "link") }, make: func(path string) error { return os.Symlink(path+"-missing", path) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			path := tt.path(root)
			if err := tt.make(path); err != nil {
				t.Skipf("setup unavailable: %v", err)
			}
			t.Setenv("BEADS_DIR", path)
			if hints, err := databaseResolutionHintsStrict(); err == nil || hints != nil {
				t.Fatalf("hints=%#v err=%v, want explicit binding error", hints, err)
			}
		})
	}
}

func TestDatabaseResolutionHintsStrictRetainsValidExplicitBeadsDir(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BEADS_DIR", beadsDir)
	hints, err := databaseResolutionHintsStrict()
	if err != nil || len(hints) != 1 {
		t.Fatalf("hints=%#v err=%v, want one explicit hint", hints, err)
	}
	if !databasePathsEqualForTest(t, hints[0].beadsDir, beadsDir) || !hints[0].authoritative || !hints[0].allowEnvironmentDataDir {
		t.Fatalf("explicit hint lost policy: %#v", hints[0])
	}
}

func TestResolveDatabaseOwnershipStrictRoutesExplicitBeadsDir(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source", ".beads")
	target := filepath.Join(root, "target", ".beads")
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
	writeOwnershipMetadata(t, target, configfile.Config{Database: "dolt", Backend: configfile.BackendDolt})
	t.Setenv("BEADS_DIR", source)

	binding, err := resolveDatabaseOwnershipStrict(dbPath)
	if err != nil || binding == nil || !databasePathsEqualForTest(t, binding.beadsDir, target) {
		t.Fatalf("binding=%#v err=%v, want explicit redirect target %q", binding, err, target)
	}
}

func writeOwnershipMetadata(t *testing.T, beadsDir string, cfg configfile.Config) {
	t.Helper()
	if err := os.MkdirAll(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configfile.ConfigPath(beadsDir), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func databasePathsEqualForTest(t *testing.T, left, right string) bool {
	t.Helper()
	equal, err := databasePathEqual(left, right)
	if err != nil {
		t.Fatalf("compare database paths %q and %q: %v", left, right, err)
	}
	return equal
}

func TestDatabaseOwnershipBindingErrorIsStable(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	dbPath := filepath.Join(beadsDir, "dolt", "source")
	if err := os.MkdirAll(dbPath, 0o700); err != nil {
		t.Fatal(err)
	}
	writeOwnershipMetadata(t, beadsDir, configfile.Config{
		Database: "dolt",
		Backend:  configfile.BackendDolt,
		DoltMode: configfile.DoltModeEmbedded,
	})
	_, err := resolveDatabaseOwnershipStrict(dbPath)
	if !errors.Is(err, errDatabaseOwnershipContradiction) {
		t.Fatalf("contradiction error %v does not preserve sentinel", err)
	}
}
