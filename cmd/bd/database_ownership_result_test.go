//go:build android || darwin || ios || linux || windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
)

func TestResolveDatabaseOwnershipStrictResultFindsMetadataLessStructuralWorkspace(t *testing.T) {
	t.Setenv("BEADS_DIR", "")

	for _, test := range []struct {
		name     string
		selector func(string) string
		create   func(*testing.T, string)
	}{
		{
			name:     "selected workspace root",
			selector: func(beadsDir string) string { return beadsDir },
			create:   makeOwnershipDirectory,
		},
		{
			name:     "embedded Dolt root produced by discovery",
			selector: func(beadsDir string) string { return filepath.Join(beadsDir, "embeddeddolt") },
			create:   makeOwnershipDirectory,
		},
		{
			name:     "embedded Dolt database directory",
			selector: func(beadsDir string) string { return filepath.Join(beadsDir, "embeddeddolt", "source") },
			create:   makeOwnershipDirectory,
		},
		{
			name:     "missing embedded Dolt database leaf",
			selector: func(beadsDir string) string { return filepath.Join(beadsDir, "embeddeddolt", "source") },
			create: func(t *testing.T, selector string) {
				t.Helper()
				makeOwnershipDirectory(t, filepath.Dir(selector))
			},
		},
		{
			name:     "default SQLite path",
			selector: func(beadsDir string) string { return filepath.Join(beadsDir, "beads.db") },
			create:   makeOwnershipFile,
		},
		{
			name:     "missing SQLite leaf",
			selector: func(beadsDir string) string { return filepath.Join(beadsDir, "beads.db") },
			create: func(t *testing.T, selector string) {
				t.Helper()
				makeOwnershipDirectory(t, filepath.Dir(selector))
			},
		},
		{
			name:     "custom database path",
			selector: func(beadsDir string) string { return filepath.Join(beadsDir, "issues.db") },
			create:   makeOwnershipFile,
		},
		{
			name:     "nested selector shape is provider agnostic",
			selector: func(beadsDir string) string { return filepath.Join(beadsDir, "custom", "layout") },
			create:   makeOwnershipDirectory,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			beadsDir := filepath.Join(t.TempDir(), ".beads")
			selector := test.selector(beadsDir)
			test.create(t, selector)

			result, err := resolveDatabaseOwnershipStrictResult(selector, false)
			if err != nil || result.binding != nil {
				t.Fatalf("result=%#v err=%v, want structural-only resolution", result, err)
			}
			assertDatabaseStructuralWorkspace(t, result.structural, beadsDir, beadsDir)
		})
	}
}

func TestResolveDatabaseOwnershipStrictResultDoesNotPromoteSiblingWorkspace(t *testing.T) {
	t.Setenv("BEADS_DIR", "")
	root := t.TempDir()
	makeOwnershipDirectory(t, filepath.Join(root, ".beads"))
	selector := filepath.Join(root, "data", "new.db")
	makeOwnershipDirectory(t, filepath.Dir(selector))

	result, err := resolveDatabaseOwnershipStrictResult(selector, false)
	assertDatabaseOwnershipResolutionZero(t, result, err, false)
}

func TestResolveDatabaseOwnershipStrictResultRequiresCanonicalContainment(t *testing.T) {
	t.Setenv("BEADS_DIR", "")

	t.Run("outward leaf symlink", func(t *testing.T) {
		beadsDir := filepath.Join(t.TempDir(), ".beads")
		makeOwnershipDirectory(t, beadsDir)
		victim := filepath.Join(t.TempDir(), "victim.db")
		makeOwnershipFile(t, victim)
		selector := filepath.Join(beadsDir, "out")
		if err := os.Symlink(victim, selector); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}

		result, err := resolveDatabaseOwnershipStrictResult(selector, false)
		assertDatabaseOwnershipResolutionZero(t, result, err, runtime.GOOS == "windows")
	})

	t.Run("outward ancestor symlink", func(t *testing.T) {
		beadsDir := filepath.Join(t.TempDir(), ".beads")
		makeOwnershipDirectory(t, beadsDir)
		victim := t.TempDir()
		makeOwnershipFile(t, filepath.Join(victim, "victim.db"))
		if err := os.Symlink(victim, filepath.Join(beadsDir, "out")); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}

		result, err := resolveDatabaseOwnershipStrictResult(filepath.Join(beadsDir, "out", "victim.db"), false)
		assertDatabaseOwnershipResolutionZero(t, result, err, false)
	})

	t.Run("inward ancestor alias", func(t *testing.T) {
		root := t.TempDir()
		realRoot := filepath.Join(root, "real")
		beadsDir := filepath.Join(realRoot, ".beads")
		selector := filepath.Join(beadsDir, "custom", "data")
		makeOwnershipDirectory(t, selector)
		alias := filepath.Join(root, "alias")
		if err := os.Symlink(realRoot, alias); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}

		result, err := resolveDatabaseOwnershipStrictResult(filepath.Join(alias, ".beads", "custom", "data"), false)
		if err != nil || result.binding != nil {
			t.Fatalf("result=%#v err=%v, want canonical structural alias", result, err)
		}
		assertDatabaseStructuralWorkspace(t, result.structural, beadsDir, beadsDir)
	})

	t.Run("inward final alias is inspection only", func(t *testing.T) {
		beadsDir := filepath.Join(t.TempDir(), ".beads")
		databasePath := filepath.Join(beadsDir, "custom.db")
		makeOwnershipFile(t, databasePath)
		selector := filepath.Join(t.TempDir(), "selected.db")
		if err := os.Symlink(databasePath, selector); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}

		result, err := resolveDatabaseOwnershipStrictResult(selector, false)
		if runtime.GOOS == "windows" {
			assertDatabaseOwnershipResolutionZero(t, result, err, true)
			return
		}
		if err != nil || result.binding != nil {
			t.Fatalf("result=%#v err=%v, want canonical inspection route", result, err)
		}
		assertDatabaseStructuralWorkspace(t, result.structural, beadsDir, beadsDir)
	})
}

func TestResolveDatabaseOwnershipStrictResultChoosesNearestStructuralSource(t *testing.T) {
	t.Setenv("BEADS_DIR", "")

	for _, mergedTarget := range []bool{false, true} {
		name := "different routed targets"
		if mergedTarget {
			name = "sources merged at one routed target"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			outerSource := filepath.Join(root, ".beads")
			innerSource := filepath.Join(outerSource, "nested", ".beads")
			selector := filepath.Join(innerSource, "selected")
			makeOwnershipDirectory(t, selector)
			outerTarget := filepath.Join(root, "shared", "with", "a", "long", "outer", "target")
			innerTarget := filepath.Join(root, "inner-target")
			if mergedTarget {
				innerTarget = outerTarget
			}
			makeOwnershipDirectory(t, outerTarget)
			makeOwnershipDirectory(t, innerTarget)
			writeOwnershipRedirect(t, outerSource, outerTarget)
			writeOwnershipRedirect(t, innerSource, innerTarget)

			result, err := resolveDatabaseOwnershipStrictResult(selector, false)
			if err != nil || result.binding != nil {
				t.Fatalf("result=%#v err=%v, want nearest structural route", result, err)
			}
			assertDatabaseStructuralWorkspace(t, result.structural, innerTarget, innerSource)
		})
	}
}

func TestResolveDatabaseOwnershipStrictResultRoutesStructureStrictly(t *testing.T) {
	t.Setenv("BEADS_DIR", "")

	for _, test := range []struct {
		name     string
		relative bool
	}{
		{name: "absolute arbitrary directory target"},
		{name: "relative arbitrary directory target", relative: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "source", ".beads")
			selector := filepath.Join(source, "selected")
			target := filepath.Join(root, "target-data")
			makeOwnershipDirectory(t, selector)
			makeOwnershipDirectory(t, target)
			redirectTarget := target
			if test.relative {
				redirectTarget = filepath.Join("..", "target-data")
			}
			writeOwnershipRedirect(t, source, redirectTarget)

			result, err := resolveDatabaseOwnershipStrictResult(selector, false)
			if err != nil || result.binding != nil {
				t.Fatalf("result=%#v err=%v, want strict redirected structure", result, err)
			}
			assertDatabaseStructuralWorkspace(t, result.structural, target, source)
		})
	}

	for _, test := range []struct {
		name          string
		configure     func(*testing.T, string, string)
		wantSubstring string
	}{
		{
			name: "empty redirect",
			configure: func(t *testing.T, source, _ string) {
				t.Helper()
				writeOwnershipRedirect(t, source, "# no target")
			},
			wantSubstring: "no target",
		},
		{
			name: "multiple targets",
			configure: func(t *testing.T, source, target string) {
				t.Helper()
				second := t.TempDir()
				if err := os.WriteFile(filepath.Join(source, "redirect"), []byte(target+"\n"+second+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantSubstring: "multiple targets",
		},
		{
			name: "redirect chain",
			configure: func(t *testing.T, source, target string) {
				t.Helper()
				writeOwnershipRedirect(t, source, target)
				writeOwnershipRedirect(t, target, t.TempDir())
			},
			wantSubstring: "chains are not supported",
		},
		{
			name: "symlinked marker",
			configure: func(t *testing.T, source, target string) {
				t.Helper()
				contents := filepath.Join(t.TempDir(), "contents")
				if err := os.WriteFile(contents, []byte(target+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(contents, filepath.Join(source, "redirect")); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			},
			wantSubstring: "not a regular file",
		},
		{
			name: "symlinked target",
			configure: func(t *testing.T, source, target string) {
				t.Helper()
				link := filepath.Join(t.TempDir(), "target-link")
				if err := os.Symlink(target, link); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
				writeOwnershipRedirect(t, source, link)
			},
			wantSubstring: "target is a symlink",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "source", ".beads")
			selector := filepath.Join(source, "selected")
			target := filepath.Join(root, "target")
			makeOwnershipDirectory(t, selector)
			makeOwnershipDirectory(t, target)
			test.configure(t, source, target)

			result, err := resolveDatabaseOwnershipStrictResult(selector, false)
			if result != (databaseOwnershipResolution{}) || err == nil || !strings.Contains(err.Error(), test.wantSubstring) {
				t.Fatalf("result=%#v err=%v, want strict redirect error containing %q", result, err, test.wantSubstring)
			}
		})
	}
}

func TestResolveDatabaseOwnershipStrictResultPrefersPersistedOwner(t *testing.T) {
	t.Setenv("BEADS_DIR", "")
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	selector := filepath.Join(beadsDir, "beads.db")
	writeOwnershipMetadata(t, beadsDir, configfile.Config{
		Database:   "beads.db",
		Backend:    configfile.BackendSQLite,
		SQLitePath: "beads.db",
	})
	makeOwnershipFile(t, selector)

	result, err := resolveDatabaseOwnershipStrictResult(selector, false)
	if err != nil || result.binding == nil || result.structural != nil {
		t.Fatalf("result=%#v err=%v, want persisted binding only", result, err)
	}
	if !databasePathsEqualForTest(t, result.binding.beadsDir, beadsDir) {
		t.Fatalf("binding workspace=%q, want %q", result.binding.beadsDir, beadsDir)
	}
}

func TestResolveDatabaseOwnershipStrictResultDoesNotFallBackPastMetadata(t *testing.T) {
	t.Setenv("BEADS_DIR", "")

	for _, test := range []struct {
		name  string
		write func(*testing.T, string)
	}{
		{
			name: "mismatched metadata",
			write: func(t *testing.T, beadsDir string) {
				t.Helper()
				writeOwnershipMetadata(t, beadsDir, configfile.Config{
					Database:   "beads.db",
					Backend:    configfile.BackendSQLite,
					SQLitePath: "other.db",
				})
			},
		},
		{
			name: "malformed metadata",
			write: func(t *testing.T, beadsDir string) {
				t.Helper()
				makeOwnershipDirectory(t, beadsDir)
				if err := os.WriteFile(configfile.ConfigPath(beadsDir), []byte(`{"database":`), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			beadsDir := filepath.Join(t.TempDir(), ".beads")
			selector := filepath.Join(beadsDir, "selected.db")
			test.write(t, beadsDir)
			makeOwnershipFile(t, selector)

			result, err := resolveDatabaseOwnershipStrictResult(selector, false)
			assertDatabaseOwnershipResolutionZero(t, result, err, true)
		})
	}
}

func TestResolveDatabaseOwnershipStrictResultLetsNestedStructureOverrideConditionalParent(t *testing.T) {
	t.Setenv("BEADS_DIR", "")
	parent := filepath.Join(t.TempDir(), "non-dot-parent")
	beadsDir := filepath.Join(parent, ".beads")
	makeOwnershipDirectory(t, beadsDir)
	writeOwnershipMetadata(t, parent, configfile.Config{
		Database:   "beads.db",
		Backend:    configfile.BackendSQLite,
		SQLitePath: "other.db",
	})

	result, err := resolveDatabaseOwnershipStrictResult(beadsDir, false)
	if err != nil || result.binding != nil {
		t.Fatalf("result=%#v err=%v, want nested structural workspace", result, err)
	}
	assertDatabaseStructuralWorkspace(t, result.structural, beadsDir, beadsDir)

	for _, resolve := range []struct {
		name string
		fn   func(string, ...databaseWorkspaceHint) (*databaseOwnershipBinding, error)
	}{
		{name: "legacy binding wrapper", fn: resolveDatabaseOwnershipStrict},
		{name: "no-automatic-hints binding wrapper", fn: resolveDatabaseOwnershipStrictWithoutAutomaticHints},
	} {
		t.Run(resolve.name, func(t *testing.T) {
			binding, err := resolve.fn(beadsDir)
			if binding != nil || !errors.Is(err, errDatabaseOwnershipContradiction) {
				t.Fatalf("binding=%#v err=%v, want legacy conditional contradiction", binding, err)
			}
		})
	}
}

func TestResolveDatabaseOwnershipStrictResultPreservesConditionalContradictionPrecedence(t *testing.T) {
	t.Setenv("BEADS_DIR", "")
	dataRoot := filepath.Join(t.TempDir(), "external-data")
	selector := filepath.Join(dataRoot, "source")
	makeOwnershipDirectory(t, selector)
	writeOwnershipMetadata(t, dataRoot, configfile.Config{
		Database:   "beads.db",
		Backend:    configfile.BackendSQLite,
		SQLitePath: "other.db",
	})

	var hints []databaseWorkspaceHint
	for range 2 {
		owner := filepath.Join(t.TempDir(), ".beads")
		writeOwnershipMetadata(t, owner, configfile.Config{
			Database:    "dolt",
			Backend:     configfile.BackendDolt,
			DoltDataDir: dataRoot,
		})
		hints = append(hints, databaseWorkspaceHint{beadsDir: owner})
	}

	result, err := resolveDatabaseOwnershipStrictResult(selector, false, hints...)
	if result != (databaseOwnershipResolution{}) || !errors.Is(err, errDatabaseOwnershipContradiction) || errors.Is(err, errDatabaseOwnershipAmbiguous) {
		t.Fatalf("result=%#v err=%v, want conditional contradiction before owner ambiguity", result, err)
	}
}

func TestResolveDatabaseOwnershipStrictResultDoesNotPromoteHintsToStructure(t *testing.T) {
	t.Setenv("BEADS_DIR", "")
	selector := filepath.Join(t.TempDir(), "new.db")
	hinted := filepath.Join(t.TempDir(), ".beads")
	makeOwnershipDirectory(t, hinted)

	result, err := resolveDatabaseOwnershipStrictResult(selector, false, databaseWorkspaceHint{beadsDir: hinted})
	assertDatabaseOwnershipResolutionZero(t, result, err, false)

	t.Run("authoritative hint deduplicated with structural source", func(t *testing.T) {
		beadsDir := filepath.Join(t.TempDir(), ".beads")
		structuralSelector := filepath.Join(beadsDir, "selected")
		makeOwnershipDirectory(t, structuralSelector)

		result, err := resolveDatabaseOwnershipStrictResult(structuralSelector, false, databaseWorkspaceHint{beadsDir: beadsDir})
		if err != nil || result.structural == nil {
			t.Fatalf("advisory result=%#v err=%v, want path-derived structure retained", result, err)
		}
		result, err = resolveDatabaseOwnershipStrictResult(structuralSelector, false, databaseWorkspaceHint{
			beadsDir:      beadsDir,
			authoritative: true,
		})
		if result != (databaseOwnershipResolution{}) || !errors.Is(err, errDatabaseOwnershipContradiction) {
			t.Fatalf("result=%#v err=%v, want authoritative metadata requirement", result, err)
		}

		t.Setenv("BEADS_DIR", beadsDir)
		result, err = resolveDatabaseOwnershipStrictResult(structuralSelector, true)
		if result != (databaseOwnershipResolution{}) || !errors.Is(err, errDatabaseOwnershipContradiction) {
			t.Fatalf("automatic result=%#v err=%v, want ambient metadata requirement", result, err)
		}
	})
}

func TestResolveDatabaseOwnershipStrictWithoutAutomaticHintsIgnoresAmbientWorkspace(t *testing.T) {
	dataRoot := filepath.Join(t.TempDir(), "external-dolt")
	selector := filepath.Join(dataRoot, "source")
	makeOwnershipDirectory(t, selector)
	ambient := filepath.Join(t.TempDir(), ".beads")
	writeOwnershipMetadata(t, ambient, configfile.Config{
		Database:    "dolt",
		Backend:     configfile.BackendDolt,
		DoltDataDir: dataRoot,
	})
	t.Setenv("BEADS_DIR", ambient)
	t.Setenv("BEADS_DOLT_DATA_DIR", "")

	binding, err := resolveDatabaseOwnershipStrict(selector)
	if err != nil || binding == nil || !databasePathsEqualForTest(t, binding.beadsDir, ambient) {
		t.Fatalf("automatic binding=%#v err=%v, want ambient owner %q", binding, err, ambient)
	}
	binding, err = resolveDatabaseOwnershipStrictWithoutAutomaticHints(selector)
	if err != nil || binding != nil {
		t.Fatalf("explicit-selector binding=%#v err=%v, want ambient hint excluded", binding, err)
	}
}

func TestResolveDatabaseOwnershipStrictWithoutAutomaticHintsIgnoresAmbientMissingMetadata(t *testing.T) {
	selector := filepath.Join(t.TempDir(), "new.db")
	ambient := filepath.Join(t.TempDir(), ".beads")
	makeOwnershipDirectory(t, ambient)
	t.Setenv("BEADS_DIR", ambient)
	t.Setenv("BEADS_DOLT_DATA_DIR", filepath.Join(t.TempDir(), "ambient-data"))

	binding, err := resolveDatabaseOwnershipStrict(selector)
	if binding != nil || !errors.Is(err, errDatabaseOwnershipContradiction) {
		t.Fatalf("automatic binding=%#v err=%v, want ambient missing-metadata contradiction", binding, err)
	}
	binding, err = resolveDatabaseOwnershipStrictWithoutAutomaticHints(selector)
	if err != nil || binding != nil {
		t.Fatalf("explicit-selector binding=%#v err=%v, want ambient authority excluded", binding, err)
	}
}

func TestDatabaseOwnershipBindingWrappersDiscardStructuralResult(t *testing.T) {
	t.Setenv("BEADS_DIR", "")
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	selector := filepath.Join(beadsDir, "selected")
	makeOwnershipDirectory(t, selector)

	result, err := resolveDatabaseOwnershipStrictResult(selector, false)
	if err != nil || result.binding != nil || result.structural == nil {
		t.Fatalf("result=%#v err=%v, want structural-only detailed result", result, err)
	}
	for _, resolve := range []struct {
		name string
		fn   func(string, ...databaseWorkspaceHint) (*databaseOwnershipBinding, error)
	}{
		{name: "legacy", fn: resolveDatabaseOwnershipStrict},
		{name: "without automatic hints", fn: resolveDatabaseOwnershipStrictWithoutAutomaticHints},
	} {
		t.Run(resolve.name, func(t *testing.T) {
			binding, err := resolve.fn(selector)
			if err != nil || binding != nil {
				t.Fatalf("binding=%#v err=%v, want structural result hidden", binding, err)
			}
		})
	}
}

func TestSelectDatabaseStructuralWorkspaceRejectsIncomparableSources(t *testing.T) {
	root := t.TempDir()
	firstSource := observedOwnershipDirectory(t, filepath.Join(root, "first", ".beads"))
	secondSource := observedOwnershipDirectory(t, filepath.Join(root, "second", ".beads"))
	firstTarget := observedOwnershipDirectory(t, filepath.Join(root, "first-target"))
	secondTarget := observedOwnershipDirectory(t, filepath.Join(root, "second-target"))

	structural, err := selectDatabaseStructuralWorkspace([]routedDatabaseOwnershipCandidate{
		{
			beadsDir:   firstTarget.path,
			resolved:   firstTarget,
			structural: true,
			sources: []routedDatabaseOwnershipSource{{
				resolved:   firstSource,
				structural: true,
			}},
		},
		{
			beadsDir:   secondTarget.path,
			resolved:   secondTarget,
			structural: true,
			sources: []routedDatabaseOwnershipSource{{
				resolved:   secondSource,
				structural: true,
			}},
		},
	})
	if structural != nil || !errors.Is(err, errDatabaseOwnershipAmbiguous) {
		t.Fatalf("structural=%#v err=%v, want incomparable-source ambiguity", structural, err)
	}
}

func makeOwnershipDirectory(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

func makeOwnershipFile(t *testing.T, path string) {
	t.Helper()
	makeOwnershipDirectory(t, filepath.Dir(path))
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeOwnershipRedirect(t *testing.T, source, target string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(source, "redirect"), []byte(target+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func observedOwnershipDirectory(t *testing.T, path string) *resolvedDatabasePath {
	t.Helper()
	makeOwnershipDirectory(t, path)
	resolved, err := resolveCanonicalDatabasePath(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func assertDatabaseOwnershipResolutionZero(t *testing.T, result databaseOwnershipResolution, err error, wantError bool) {
	t.Helper()
	if result != (databaseOwnershipResolution{}) || (err != nil) != wantError {
		t.Fatalf("result=%#v err=%v, want zero result with error=%t", result, err, wantError)
	}
}

func assertDatabaseStructuralWorkspace(t *testing.T, candidate *databaseWorkspaceCandidate, wantTarget, wantSource string) {
	t.Helper()
	if candidate == nil || candidate.resolved == nil || candidate.sourceResolved == nil {
		t.Fatalf("structural=%#v, want observed route %q -> %q", candidate, wantSource, wantTarget)
	}
	if !databasePathsEqualForTest(t, candidate.beadsDir, wantTarget) ||
		!databasePathsEqualForTest(t, candidate.resolved.path, wantTarget) ||
		!databasePathsEqualForTest(t, candidate.sourceResolved.path, wantSource) {
		t.Fatalf("structural=%#v, want observed route %q -> %q", candidate, wantSource, wantTarget)
	}
}
