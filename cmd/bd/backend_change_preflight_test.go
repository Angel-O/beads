package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/beads"
	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/git"
)

func TestSelectInitBackendSelectionPrecedence(t *testing.T) {
	root := t.TempDir()
	paths := func(names ...string) []string {
		got := make([]string, len(names))
		for i, name := range names {
			got[i] = filepath.Join(root, name)
		}
		return got
	}
	candidates := paths(
		"explicit", "beads-db", "bd-db", "beads-dir", "configured", "discovered", "worktree", "cwd",
	)
	explicit, beadsDB, bdDB, beadsDir := candidates[0], candidates[1], candidates[2], candidates[3]
	configured, discovered, worktree, cwd := candidates[4], candidates[5], candidates[6], candidates[7]

	all := initBackendSelectorCandidates{
		explicitDBSet: true,
		explicitDB:    explicit,
		beadsDB:       beadsDB,
		bdDB:          bdDB,
		beadsDir:      beadsDir,
		configuredDB:  configured,
		discovered:    discovered,
		worktree:      worktree,
		cwd:           cwd,
	}
	for _, test := range []struct {
		name       string
		mutate     func(*initBackendSelectorCandidates)
		want       string
		wantSource initBackendSelectionSource
	}{
		{name: "explicit db", want: explicit, wantSource: initBackendSelectionExplicitDB},
		{name: "BEADS_DB", mutate: func(c *initBackendSelectorCandidates) { c.explicitDBSet = false }, want: beadsDB, wantSource: initBackendSelectionBeadsDB},
		{name: "BD_DB", mutate: func(c *initBackendSelectorCandidates) { c.explicitDBSet = false; c.beadsDB = "" }, want: bdDB, wantSource: initBackendSelectionLegacyBDDB},
		{name: "BEADS_DIR", mutate: func(c *initBackendSelectorCandidates) { c.explicitDBSet = false; c.beadsDB, c.bdDB = "", "" }, want: beadsDir, wantSource: initBackendSelectionBeadsDir},
		{name: "configured db", mutate: func(c *initBackendSelectorCandidates) {
			c.explicitDBSet = false
			c.beadsDB, c.bdDB, c.beadsDir = "", "", ""
		}, want: configured, wantSource: initBackendSelectionConfiguredDB},
		{name: "discovered source", mutate: func(c *initBackendSelectorCandidates) {
			c.explicitDBSet = false
			c.beadsDB, c.bdDB, c.beadsDir, c.configuredDB = "", "", "", ""
		}, want: discovered, wantSource: initBackendSelectionDiscovered},
		{name: "worktree fallback", mutate: func(c *initBackendSelectorCandidates) {
			c.explicitDBSet = false
			c.beadsDB, c.bdDB, c.beadsDir, c.configuredDB, c.discovered = "", "", "", "", ""
		}, want: worktree, wantSource: initBackendSelectionWorktree},
		{name: "cwd fallback", mutate: func(c *initBackendSelectorCandidates) {
			c.explicitDBSet = false
			c.beadsDB, c.bdDB, c.beadsDir, c.configuredDB, c.discovered, c.worktree = "", "", "", "", "", ""
		}, want: filepath.Join(cwd, ".beads"), wantSource: initBackendSelectionCWD},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidates := all
			if test.mutate != nil {
				test.mutate(&candidates)
			}
			got, err := selectInitBackendSelection(candidates)
			if err != nil || got.selector != filepath.Clean(test.want) || got.source != test.wantSource {
				t.Fatalf("selection = %#v, %v; want selector=%q source=%d", got, err, filepath.Clean(test.want), test.wantSource)
			}
			wantTarget := filepath.Clean(test.want)
			if test.wantSource.isDatabase() {
				wantTarget = filepath.Dir(wantTarget)
			}
			if got.creationBeadsDir != wantTarget {
				t.Fatalf("creation target = %q, want %q", got.creationBeadsDir, wantTarget)
			}
		})
	}

	t.Run("explicit empty db fails closed", func(t *testing.T) {
		got, err := selectInitBackendSelection(initBackendSelectorCandidates{explicitDBSet: true, beadsDB: beadsDB, cwd: cwd})
		if got != (initBackendSelection{}) || err == nil {
			t.Fatalf("selection = %#v, %v; want explicit-empty error", got, err)
		}
	})

	t.Run("BEADS_DIR suppresses configured db", func(t *testing.T) {
		got, err := selectInitBackendSelection(initBackendSelectorCandidates{beadsDir: beadsDir, configuredDB: configured, cwd: cwd})
		if err != nil || got.selector != beadsDir || got.source != initBackendSelectionBeadsDir {
			t.Fatalf("selection = %#v, %v; want BEADS_DIR %q", got, err, beadsDir)
		}
	})

	t.Run("workspace env selectors suppress configured db", func(t *testing.T) {
		for _, test := range []struct {
			name       string
			candidates initBackendSelectorCandidates
			want       string
			wantSource initBackendSelectionSource
		}{
			{
				name:       "BEADS_DB",
				candidates: initBackendSelectorCandidates{configuredDB: configured, dotEnvBeadsDB: beadsDB, cwd: cwd},
				want:       beadsDB,
				wantSource: initBackendSelectionDotEnvBeadsDB,
			},
			{
				name:       "BD_DB",
				candidates: initBackendSelectorCandidates{configuredDB: configured, dotEnvBDDB: bdDB, cwd: cwd},
				want:       bdDB,
				wantSource: initBackendSelectionDotEnvLegacyBDDB,
			},
			{
				name:       "BEADS_DIR",
				candidates: initBackendSelectorCandidates{configuredDB: configured, dotEnvBeadsDir: beadsDir, cwd: cwd},
				want:       beadsDir,
				wantSource: initBackendSelectionDotEnvBeadsDir,
			},
		} {
			t.Run(test.name, func(t *testing.T) {
				got, err := selectInitBackendSelection(test.candidates)
				if err != nil || got.selector != test.want || got.source != test.wantSource {
					t.Fatalf("selection = %#v, %v; want workspace .env selector %q from source %d", got, err, test.want, test.wantSource)
				}
			})
		}
	})
}

func TestResolveInitBackendSelectionPreservesRedirectSourceAndDotEnvSelectors(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	source := filepath.Join(project, ".beads")
	target := filepath.Join(root, "target", ".beads")
	makeOwnershipDirectory(t, source)
	writeOwnershipMetadata(t, target, configfile.Config{Backend: configfile.BackendSQLite, SQLitePath: "beads.db"})
	writeOwnershipRedirect(t, source, target)
	t.Chdir(project)
	t.Setenv("BEADS_DIR", "")
	t.Setenv("BEADS_DB", "")
	t.Setenv("BD_DB", "")
	oldChangeDir := changeDir
	changeDir = ""
	t.Cleanup(func() {
		changeDir = oldChangeDir
		beads.ResetCaches()
		git.ResetCaches()
	})
	beads.ResetCaches()
	git.ResetCaches()
	initConfigForTest(t)
	cmd := newInitBackendPreflightTestCommand(t, configfile.BackendSQLite)

	got, err := resolveInitBackendSelection(cmd)
	if err != nil || got.selector != source || got.source != initBackendSelectionDiscovered {
		t.Fatalf("redirect selection = %#v, %v; want discovered source %q", got, err, source)
	}

	envDB := filepath.Join(root, "selected", "beads.db")
	if err := os.WriteFile(filepath.Join(target, ".env"), []byte("BEADS_DB="+envDB+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = resolveInitBackendSelection(cmd)
	if err != nil || got.selector != envDB {
		t.Fatalf("selection after .env = %#v, %v; want routed database %q", got, err, envDB)
	}
	if err := os.WriteFile(filepath.Join(target, ".env"), []byte("beads_db="+envDB+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = resolveInitBackendSelection(cmd)
	if err != nil || got.selector != envDB {
		t.Fatalf("case-insensitive .env selection = %#v, %v; want %q", got, err, envDB)
	}
	if err := os.WriteFile(filepath.Join(target, ".env"), []byte("BEADS_DB="+envDB+"\nbeads_db="+envDB+"-other\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveInitBackendSelection(cmd); err == nil || !strings.Contains(err.Error(), "conflicting case-insensitive BEADS_DB") {
		t.Fatalf("conflicting case variants error = %v", err)
	}
}

func TestResolveInitBackendSelectorPreservesDotEnvAuthorityOverConfiguredDB(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	beadsDir := filepath.Join(project, ".beads")
	if err := os.MkdirAll(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	dotEnvDB := filepath.Join(root, "dot-env", "beads.db")
	if err := os.WriteFile(filepath.Join(beadsDir, ".env"), []byte("BEADS_DB="+dotEnvDB+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "config.yaml"), []byte("issue-prefix: envtest\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configuredDB := filepath.Join(root, "configured", "beads.db")
	t.Chdir(project)
	t.Setenv("BEADS_DIR", "")
	t.Setenv("BEADS_DB", "")
	t.Setenv("BD_DB", "")
	oldChangeDir := changeDir
	changeDir = ""
	t.Cleanup(func() {
		changeDir = oldChangeDir
		beads.ResetCaches()
		git.ResetCaches()
	})
	beads.ResetCaches()
	git.ResetCaches()
	initConfigForTest(t)
	config.Set("db", configuredDB)

	got, err := resolveInitBackendSelection(newInitBackendPreflightTestCommand(t, configfile.BackendDolt))
	if err != nil || got.selector != dotEnvDB || got.source != initBackendSelectionDotEnvBeadsDB {
		t.Fatalf("selection = %#v, %v; want historical workspace .env db %q", got, err, dotEnvDB)
	}
}

func TestPrepareInitContextAfterBackendPreflightBindsRedirectTarget(t *testing.T) {
	isolateInitBackendBindingGlobals(t)
	root := t.TempDir()
	source := filepath.Join(root, "source", ".beads")
	target := filepath.Join(root, "target", ".beads")
	makeOwnershipDirectory(t, source)
	makeOwnershipDirectory(t, target)
	writeOwnershipRedirect(t, source, target)
	selector := filepath.Join(source, "new.db")

	t.Cleanup(func() {
		beads.ResetCaches()
		git.ResetCaches()
	})
	t.Setenv("BEADS_DIR", "")
	t.Setenv("BEADS_DB", "")
	t.Setenv("BD_DB", "")
	beads.ResetCaches()
	git.ResetCaches()
	initConfigForTest(t)

	selection, err := selectInitBackendSelection(initBackendSelectorCandidates{explicitDBSet: true, explicitDB: selector})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := inspectBackendWorkspaceSnapshot(selection.selector)
	if err != nil || snapshot == nil || snapshot.route.target.path != target {
		t.Fatalf("snapshot = %#v, %v; want routed target %q", snapshot, err, target)
	}
	admission, err := buildInitBackendAdmission(configfile.BackendSQLite, selection, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	cmd := newInitBackendPreflightTestCommand(t, configfile.BackendSQLite)
	if err := prepareInitContextAfterBackendPreflight(cmd, admission); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("BEADS_DIR"); got != target {
		t.Fatalf("BEADS_DIR = %q, want admitted redirect target %q", got, target)
	}
	if dbPath != filepath.Join(target, "new.db") {
		t.Fatalf("dbPath = %q, want routed database path %q", dbPath, filepath.Join(target, "new.db"))
	}
}

func TestUnresolvedExplicitDBCannotFallBackToAmbientWorkspace(t *testing.T) {
	initConfigForTest(t)
	for _, key := range []string{"BEADS_DIR", "BEADS_DB", "BD_DB"} {
		t.Setenv(key, "test-cleanup")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	ambientProject := filepath.Join(root, "ambient")
	ambient := filepath.Join(ambientProject, ".beads")
	writeOwnershipMetadata(t, ambient, configfile.Config{Backend: configfile.BackendSQLite, SQLitePath: "ambient.db"})
	t.Chdir(ambientProject)
	externalDir := filepath.Join(root, "external")
	if err := os.Mkdir(externalDir, 0o700); err != nil {
		t.Fatal(err)
	}
	selector := filepath.Join(externalDir, "new.db")
	selection, err := selectInitBackendSelection(initBackendSelectorCandidates{explicitDBSet: true, explicitDB: selector})
	if err != nil {
		t.Fatal(err)
	}
	deps := initBackendPreflightDependencies{
		resolveSelection:   func(*cobra.Command) (initBackendSelection, error) { return selection, nil },
		inspectWorkspace:   inspectBackendWorkspaceSnapshot,
		inspectFreshTarget: inspectInitBackendFreshTarget,
		admit:              admitInitBackend,
	}
	cmd := newInitBackendPreflightTestCommand(t, configfile.BackendSQLite)
	err = prepareInitBackendPreflightWith(cmd, deps)
	if !errors.Is(err, errInitBackendSelectorUnowned) {
		t.Fatalf("prepare error = %v, want unowned-selector refusal", err)
	}
	if !strings.Contains(err.Error(), "remove --db") || !strings.Contains(err.Error(), "BEADS_DIR") {
		t.Fatalf("unowned-selector guidance is not actionable with selector precedence: %v", err)
	}
	if got := os.Getenv("BEADS_DIR"); got != "" {
		t.Fatalf("unresolved selector bound ambient or derived workspace: %q (ambient %q, derived %q)", got, ambient, externalDir)
	}
}

func TestPrepareInitContextAfterBackendPreflightIgnoresDotEnvSelectors(t *testing.T) {
	isolateInitBackendBindingGlobals(t)
	initConfigForTest(t)
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	makeOwnershipDirectory(t, beadsDir)
	evilDB := filepath.Join(t.TempDir(), "evil.db")
	if err := os.WriteFile(filepath.Join(beadsDir, ".env"), []byte("BEADS_DB="+evilDB+"\nbeads_db="+evilDB+"\nBD_DB="+evilDB+"\nBEADS_DIR=/evil\nBD_BACKEND=mysql\nBD_DATABASE_BACKEND=mysql\nBEADS_DOLT_DATA_DIR=/evil-dolt\nbeads_dolt_password=secret\nRUNTIME_CANARY=must-not-load\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"BEADS_DIR", "BEADS_DB", "BD_DB", "BD_BACKEND", "BD_DATABASE_BACKEND", "BEADS_DOLT_DATA_DIR", "BEADS_DOLT_PASSWORD", "RUNTIME_CANARY"} {
		t.Setenv(key, "test-cleanup")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	selection := initBackendSelection{source: initBackendSelectionBeadsDir, selector: beadsDir, creationBeadsDir: beadsDir}
	admission := initBackendAdmission{backend: configfile.BackendDolt, selection: selection, beadsDir: beadsDir}
	if err := prepareInitContextAfterBackendPreflight(newInitBackendPreflightTestCommand(t, configfile.BackendDolt), admission); err != nil {
		t.Fatal(err)
	}
	if dbPath != "" {
		t.Fatalf("dbPath = %q, want workspace-selected empty path", dbPath)
	}
	if _, present := os.LookupEnv("BEADS_DB"); present {
		t.Fatalf("BEADS_DB acquired authority from admitted target .env: %q", os.Getenv("BEADS_DB"))
	}
	if _, present := os.LookupEnv("BD_DB"); present {
		t.Fatalf("BD_DB acquired authority from admitted target .env: %q", os.Getenv("BD_DB"))
	}
	for _, key := range []string{"BD_BACKEND", "BD_DATABASE_BACKEND"} {
		if _, present := os.LookupEnv(key); present {
			t.Fatalf("blocked backend key %s loaded from admitted target .env", key)
		}
	}
	if got := os.Getenv("BEADS_DIR"); got != beadsDir {
		t.Fatalf("BEADS_DIR = %q, want pinned target %q", got, beadsDir)
	}
	if got := os.Getenv("BEADS_DOLT_DATA_DIR"); got != "" {
		t.Fatalf("provider locator loaded after admission: %q", got)
	}
	if got := os.Getenv("BEADS_DOLT_PASSWORD"); got != "secret" {
		t.Fatalf("credential-only runtime .env was not loaded canonically: %q", got)
	}
	if got := os.Getenv("RUNTIME_CANARY"); got != "" {
		t.Fatalf("unapproved runtime .env key was loaded: %q", got)
	}
}

func TestCanonicalInitBackendRuntimeEnvKeyIsCaseInsensitiveAndCredentialOnly(t *testing.T) {
	for _, test := range []struct {
		key       string
		want      string
		wantAllow bool
	}{
		{key: "beads_dolt_password", want: "BEADS_DOLT_PASSWORD", wantAllow: true},
		{key: "BEADS_PG_PASSWORD_COMMAND", want: "BEADS_PG_PASSWORD_COMMAND", wantAllow: true},
		{key: "BeAdS_MySqL_PaSsWoRd", want: "BEADS_MYSQL_PASSWORD", wantAllow: true},
		{key: "beads_dir"},
		{key: "bd_db"},
		{key: "bd_backend"},
		{key: "beads_dolt_data_dir"},
		{key: "beads_postgres_url"},
	} {
		got, allowed := canonicalInitBackendRuntimeEnvKey(test.key)
		if got != test.want || allowed != test.wantAllow {
			t.Errorf("key %q = %q, %v; want %q, %v", test.key, got, allowed, test.want, test.wantAllow)
		}
	}
}

func TestFreshLegacyBDDBWithoutWorkspaceFailsClosed(t *testing.T) {
	initConfigForTest(t)
	for _, key := range []string{"BEADS_DIR", "BEADS_DB", "BD_DB"} {
		t.Setenv(key, "test-cleanup")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	selector := filepath.Join(t.TempDir(), "legacy", "beads.db")
	selection, err := selectInitBackendSelection(initBackendSelectorCandidates{bdDB: selector})
	if err != nil {
		t.Fatal(err)
	}
	deps := initBackendPreflightDependencies{
		resolveSelection:   func(*cobra.Command) (initBackendSelection, error) { return selection, nil },
		inspectWorkspace:   func(string) (*backendWorkspaceSnapshot, error) { return nil, nil },
		inspectFreshTarget: inspectInitBackendFreshTarget,
		admit:              admitInitBackend,
	}
	err = prepareInitBackendPreflightWith(newInitBackendPreflightTestCommand(t, configfile.BackendDolt), deps)
	if !errors.Is(err, errInitBackendSelectorUnowned) {
		t.Fatalf("prepare error = %v, want unowned-selector refusal", err)
	}
}

func TestInitBackendPreflightStableLifecycle(t *testing.T) {
	first := newBackendStabilizerSnapshot(t)
	second := cloneBackendStabilizerSnapshot(first)
	cmd := newInitBackendPreflightTestCommand(t, configfile.BackendSQLite)
	selectorCalls, inspectCalls, admitCalls := 0, 0, 0
	selection := initBackendSelection{
		source:           initBackendSelectionBeadsDir,
		selector:         first.selector,
		creationBeadsDir: first.selector,
	}
	deps := initBackendPreflightDependencies{
		resolveSelection: func(*cobra.Command) (initBackendSelection, error) {
			selectorCalls++
			return selection, nil
		},
		inspectWorkspace: func(string) (*backendWorkspaceSnapshot, error) {
			inspectCalls++
			if inspectCalls == 1 {
				return first, nil
			}
			return second, nil
		},
		inspectFreshTarget: func(string) (*initBackendFreshTargetSnapshot, error) {
			t.Fatal("fresh target inspection must not run for an owned workspace")
			return nil, nil
		},
		admit: func(requested string, snapshot *backendWorkspaceSnapshot) (string, error) {
			admitCalls++
			return admitInitBackend(requested, snapshot)
		},
	}

	if err := prepareInitBackendPreflightWith(cmd, deps); err != nil {
		t.Fatalf("prepare preflight: %v", err)
	}
	state, _ := cmd.Context().Value(initBackendPreflightContextKey{}).(*initBackendPreflightContextState)
	if state == nil || state.preflight == nil || state.preflight.snapshot == first {
		t.Fatal("preflight did not retain an independent snapshot")
	}
	first.route.bindingSources[0].exists = false
	admission, err := consumeInitBackendPreflightWith(cmd, deps)
	if err != nil || admission.backend != configfile.BackendSQLite || admission.beadsDir != second.route.target.path {
		t.Fatalf("consume preflight = %#v, %v", admission, err)
	}
	if selectorCalls != 2 || inspectCalls != 2 || admitCalls != 2 {
		t.Fatalf("calls selector=%d inspect=%d admit=%d, want 2 each", selectorCalls, inspectCalls, admitCalls)
	}
	if state.preflight != nil {
		t.Fatal("consume did not clear command-scoped preflight")
	}
	if _, err := consumeInitBackendPreflightWith(cmd, deps); !errors.Is(err, errInitBackendPreflightMissing) {
		t.Fatalf("second consume error = %v, want missing-preflight sentinel", err)
	}
}

func TestInitBackendPreflightStateIsIsolatedByCommandContext(t *testing.T) {
	newFreshDeps := func(selection initBackendSelection) initBackendPreflightDependencies {
		return initBackendPreflightDependencies{
			resolveSelection:   func(*cobra.Command) (initBackendSelection, error) { return selection, nil },
			inspectWorkspace:   func(string) (*backendWorkspaceSnapshot, error) { return nil, nil },
			inspectFreshTarget: inspectInitBackendFreshTarget,
			admit:              admitInitBackend,
		}
	}
	selectionA, err := selectInitBackendSelection(initBackendSelectorCandidates{beadsDir: filepath.Join(t.TempDir(), ".beads")})
	if err != nil {
		t.Fatal(err)
	}
	selectionB, err := selectInitBackendSelection(initBackendSelectorCandidates{beadsDir: filepath.Join(t.TempDir(), ".beads")})
	if err != nil {
		t.Fatal(err)
	}
	cmdA := newInitBackendPreflightTestCommand(t, configfile.BackendDolt)
	cmdB := newInitBackendPreflightTestCommand(t, configfile.BackendSQLite)
	depsA, depsB := newFreshDeps(selectionA), newFreshDeps(selectionB)
	if err := prepareInitBackendPreflightWith(cmdA, depsA); err != nil {
		t.Fatal(err)
	}
	if err := prepareInitBackendPreflightWith(cmdB, depsB); err != nil {
		t.Fatal(err)
	}
	admissionA, err := consumeInitBackendPreflightWith(cmdA, depsA)
	if err != nil || admissionA.backend != configfile.BackendDolt || admissionA.beadsDir != selectionA.creationBeadsDir {
		t.Fatalf("command A admission = %#v, %v", admissionA, err)
	}
	admissionB, err := consumeInitBackendPreflightWith(cmdB, depsB)
	if err != nil || admissionB.backend != configfile.BackendSQLite || admissionB.beadsDir != selectionB.creationBeadsDir {
		t.Fatalf("command B admission = %#v, %v", admissionB, err)
	}
}

func TestInitBackendPreflightAcceptsStableStructuralTarget(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source", ".beads")
	target := filepath.Join(root, "target", ".beads")
	selector := filepath.Join(source, "nested", "issues.db")
	makeOwnershipDirectory(t, filepath.Dir(selector))
	makeOwnershipDirectory(t, target)
	writeOwnershipRedirect(t, source, target)
	selection, err := selectInitBackendSelection(initBackendSelectorCandidates{explicitDBSet: true, explicitDB: selector})
	if err != nil {
		t.Fatal(err)
	}
	deps := initBackendPreflightDependencies{
		resolveSelection:   func(*cobra.Command) (initBackendSelection, error) { return selection, nil },
		inspectWorkspace:   inspectBackendWorkspaceSnapshot,
		inspectFreshTarget: inspectInitBackendFreshTarget,
		admit:              admitInitBackend,
	}
	cmd := newInitBackendPreflightTestCommand(t, configfile.BackendSQLite)
	if err := prepareInitBackendPreflightWith(cmd, deps); err != nil {
		t.Fatal(err)
	}
	admission, err := consumeInitBackendPreflightWith(cmd, deps)
	if err != nil {
		t.Fatal(err)
	}
	if admission.beadsDir != target || admission.databasePath != filepath.Join(target, "nested", "issues.db") {
		t.Fatalf("admission = %#v, want target %q and mapped database", admission, target)
	}
}

func TestAdmittedProviderPathsDriveSQLiteAndDoltEffects(t *testing.T) {
	t.Run("SQLite provisions the admitted mapped file", func(t *testing.T) {
		root := t.TempDir()
		source := filepath.Join(root, "source", ".beads")
		target := filepath.Join(root, "target", ".beads")
		selector := filepath.Join(source, "custom", "issues.db")
		makeOwnershipDirectory(t, filepath.Dir(selector))
		makeOwnershipDirectory(t, target)
		writeOwnershipRedirect(t, source, target)
		selection, err := selectInitBackendSelection(initBackendSelectorCandidates{explicitDBSet: true, explicitDB: selector})
		if err != nil {
			t.Fatal(err)
		}
		snapshot, err := inspectBackendWorkspaceSnapshot(selection.selector)
		if err != nil {
			t.Fatal(err)
		}
		admission, err := buildInitBackendAdmission(configfile.BackendSQLite, selection, snapshot)
		if err != nil {
			t.Fatal(err)
		}
		providerPath, err := resolveAdmittedInitSQLitePath(admission, "", false)
		if err != nil {
			t.Fatal(err)
		}
		if providerPath != filepath.Join(target, "custom", "issues.db") {
			t.Fatalf("provider path = %q, want mapped target", providerPath)
		}
		if err := runInitSQLite(context.Background(), initSQLiteInput{
			beadsDir: target, prefix: "mapped", sqlitePath: providerPath, quiet: true,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(providerPath); err != nil {
			t.Fatalf("admitted SQLite path was not provisioned: %v", err)
		}
		if _, err := os.Stat(filepath.Join(target, "beads.db")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("default SQLite path was provisioned instead of admitted path: %v", err)
		}
		cfg, err := configfile.Load(target)
		if err != nil || cfg == nil || cfg.SQLitePath != providerPath {
			t.Fatalf("metadata = %#v, %v; want SQLite path %q", cfg, err, providerPath)
		}
	})

	t.Run("Dolt uses the admitted owned directory", func(t *testing.T) {
		beadsDir := filepath.Join(t.TempDir(), ".beads")
		writeOwnershipMetadata(t, beadsDir, configfile.Config{Backend: configfile.BackendDolt})
		writeBackendObserverDolt(t, beadsDir)
		snapshot, err := inspectBackendWorkspaceSnapshot(beadsDir)
		if err != nil {
			t.Fatal(err)
		}
		selection, err := selectInitBackendSelection(initBackendSelectorCandidates{beadsDir: beadsDir})
		if err != nil {
			t.Fatal(err)
		}
		admission, err := buildInitBackendAdmission(configfile.BackendDolt, selection, snapshot)
		if err != nil {
			t.Fatal(err)
		}
		fallback := filepath.Join(t.TempDir(), "wrong")
		if got := resolveAdmittedInitDoltPath(admission, fallback); got != snapshot.route.owned.path {
			t.Fatalf("Dolt provider path = %q, want admitted owned path %q", got, snapshot.route.owned.path)
		}
	})

	t.Run("SQLite RunE creates only the admitted mapped parent", func(t *testing.T) {
		isolateInitBackendBindingGlobals(t)
		initConfigForTest(t)
		root := t.TempDir()
		source := filepath.Join(root, "source", ".beads")
		target := filepath.Join(root, "target", ".beads")
		selector := filepath.Join(source, "custom", "nested", "issues.db")
		makeOwnershipDirectory(t, filepath.Dir(selector))
		makeOwnershipDirectory(t, target)
		writeOwnershipRedirect(t, source, target)
		t.Chdir(root)
		for _, key := range []string{"BEADS_DIR", "BEADS_DB", "BD_DB"} {
			t.Setenv(key, "test-cleanup")
			if err := os.Unsetenv(key); err != nil {
				t.Fatal(err)
			}
		}
		t.Setenv("BEADS_DB", selector)
		oldRootCtx := rootCtx
		rootCtx = context.Background()
		t.Cleanup(func() { rootCtx = oldRootCtx })

		cmd := newInitBackendPreflightTestCommand(t, configfile.BackendSQLite)
		cmd.RunE = initCmd.RunE
		cmd.Flags().Bool("quiet", false, "")
		if err := cmd.Flags().Set("quiet", "true"); err != nil {
			t.Fatal(err)
		}
		if err := prepareInitBackendPreflight(cmd); err != nil {
			t.Fatal(err)
		}
		if err := cmd.RunE(cmd, nil); err != nil {
			t.Fatalf("real SQLite RunE with admitted mapped path: %v", err)
		}
		mapped := filepath.Join(target, "custom", "nested", "issues.db")
		if _, err := os.Stat(mapped); err != nil {
			t.Fatalf("admitted mapped SQLite file was not provisioned: %v", err)
		}
		if _, err := os.Stat(filepath.Join(source, "custom", "nested", "issues.db")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("source/decoy SQLite path was written: %v", err)
		}
	})

	t.Run("SQLite directory errors escape control characters", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "unsafe\x00directory", "issues.db")
		err := runInitSQLite(context.Background(), initSQLiteInput{
			beadsDir: t.TempDir(), sqlitePath: dbPath, quiet: true,
		})
		if err == nil {
			t.Fatal("SQLite init unexpectedly accepted a NUL-containing directory")
		}
		if strings.ContainsRune(err.Error(), '\x00') {
			t.Fatalf("SQLite directory error contains a raw terminal control: %q", err.Error())
		}
		if !strings.Contains(err.Error(), `\x00`) {
			t.Fatalf("SQLite directory error does not identify the escaped path: %q", err.Error())
		}
	})

	t.Run("Dolt RunE never creates an ambient data directory", func(t *testing.T) {
		isolateInitBackendBindingGlobals(t)
		initConfigForTest(t)
		root := t.TempDir()
		beadsDir := filepath.Join(root, "project", ".beads")
		writeOwnershipMetadata(t, beadsDir, configfile.Config{
			Backend:        configfile.BackendDolt,
			DoltMode:       configfile.DoltModeServer,
			DoltServerHost: "127.0.0.1",
			DoltServerPort: 1,
		})
		t.Chdir(filepath.Dir(beadsDir))
		decoy := filepath.Join(root, "ambient-decoy")
		t.Setenv("BEADS_DIR", beadsDir)
		t.Setenv("BEADS_DB", "")
		t.Setenv("BD_DB", "")
		t.Setenv("BEADS_DOLT_DATA_DIR", decoy)
		t.Setenv("BEADS_DOLT_AUTO_START", "0")
		oldRootCtx := rootCtx
		canceledContext, cancel := context.WithCancel(context.Background())
		cancel()
		rootCtx = canceledContext
		t.Cleanup(func() { rootCtx = oldRootCtx })

		cmd := newInitBackendPreflightTestCommand(t, configfile.BackendDolt)
		cmd.RunE = initCmd.RunE
		cmd.Flags().Bool("server", false, "")
		cmd.Flags().Bool("reinit-local", false, "")
		cmd.Flags().Bool("quiet", false, "")
		cmd.Flags().String("server-host", "", "")
		cmd.Flags().Int("server-port", 0, "")
		for name, value := range map[string]string{
			"server": "true", "reinit-local": "true", "quiet": "true", "server-host": "127.0.0.1", "server-port": "1",
		} {
			if err := cmd.Flags().Set(name, value); err != nil {
				t.Fatal(err)
			}
		}
		if err := prepareInitBackendPreflight(cmd); err != nil {
			t.Fatal(err)
		}
		state, _ := cmd.Context().Value(initBackendPreflightContextKey{}).(*initBackendPreflightContextState)
		if state == nil || state.preflight == nil || state.preflight.snapshot == nil {
			t.Fatal("Dolt preflight snapshot is missing")
		}
		admittedPath := state.preflight.snapshot.route.owned.path
		if admittedPath == "" {
			t.Fatal("Dolt preflight did not admit a provider path")
		}
		if _, err := os.Lstat(admittedPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Dolt admitted-path fixture must start absent: %v", err)
		}
		_ = cmd.RunE(cmd, nil) // Connection failure is expected; path authority is the assertion.
		if _, err := os.Lstat(decoy); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("unadmitted ambient Dolt data directory was created: %v", err)
		}
		if info, err := os.Stat(admittedPath); err != nil || !info.IsDir() {
			t.Fatalf("admitted Dolt provider directory was not created: info=%v err=%v", info, err)
		}
	})
}

func TestInitBackendPreflightCapturesRedirectSourceDatabase(t *testing.T) {
	setup := func(t *testing.T) (source, target string, selection initBackendSelection, deps initBackendPreflightDependencies) {
		t.Helper()
		root := t.TempDir()
		source = filepath.Join(root, "source", ".beads")
		target = filepath.Join(root, "target", ".beads")
		makeOwnershipDirectory(t, source)
		writeOwnershipRedirect(t, source, target)
		writeOwnershipMetadata(t, source, configfile.Config{
			Backend: configfile.BackendDolt, DoltDataDir: filepath.Join(target, "embeddeddolt"), DoltDatabase: "source_a",
		})
		writeOwnershipMetadata(t, target, configfile.Config{Backend: configfile.BackendDolt})
		writeBackendObserverDolt(t, target)
		var err error
		selection, err = selectInitBackendSelection(initBackendSelectorCandidates{beadsDir: source})
		if err != nil {
			t.Fatal(err)
		}
		deps = initBackendPreflightDependencies{
			resolveSelection:   func(*cobra.Command) (initBackendSelection, error) { return selection, nil },
			inspectWorkspace:   inspectBackendWorkspaceSnapshot,
			inspectFreshTarget: inspectInitBackendFreshTarget,
			admit:              admitInitBackend,
		}
		return source, target, selection, deps
	}

	t.Run("source metadata drift is rejected", func(t *testing.T) {
		source, target, _, deps := setup(t)
		cmd := newInitBackendPreflightTestCommand(t, configfile.BackendDolt)
		if err := prepareInitBackendPreflightWith(cmd, deps); err != nil {
			t.Fatal(err)
		}
		writeOwnershipMetadata(t, source, configfile.Config{
			Backend: configfile.BackendDolt, DoltDataDir: filepath.Join(target, "embeddeddolt"), DoltDatabase: "source_b",
		})
		if _, err := consumeInitBackendPreflightWith(cmd, deps); !errors.Is(err, errInitBackendPreflightChanged) {
			t.Fatalf("consume error = %v, want redirect-source drift refusal", err)
		}
	})

	t.Run("captured database is applied without a live reread", func(t *testing.T) {
		isolateInitBackendBindingGlobals(t)
		initConfigForTest(t)
		for _, key := range []string{"BEADS_DIR", "BEADS_DOLT_SERVER_DATABASE"} {
			t.Setenv(key, "test-cleanup")
			if err := os.Unsetenv(key); err != nil {
				t.Fatal(err)
			}
		}
		source, target, _, deps := setup(t)
		cmd := newInitBackendPreflightTestCommand(t, configfile.BackendDolt)
		if err := prepareInitBackendPreflightWith(cmd, deps); err != nil {
			t.Fatal(err)
		}
		admission, err := consumeInitBackendPreflightWith(cmd, deps)
		if err != nil {
			t.Fatal(err)
		}
		if admission.redirectDatabase != "source_a" {
			t.Fatalf("captured redirect database = %q, want source_a", admission.redirectDatabase)
		}
		writeOwnershipMetadata(t, source, configfile.Config{
			Backend: configfile.BackendDolt, DoltDataDir: filepath.Join(target, "embeddeddolt"), DoltDatabase: "source_b",
		})
		if err := prepareInitContextAfterBackendPreflight(cmd, admission); err != nil {
			t.Fatal(err)
		}
		if got := os.Getenv("BEADS_DOLT_SERVER_DATABASE"); got != "source_a" {
			t.Fatalf("bound redirect database = %q, want captured source_a", got)
		}
	})

	t.Run("structural redirect source database is captured and drift checked", func(t *testing.T) {
		newFixture := func(t *testing.T) (source string, cmd *cobra.Command, deps initBackendPreflightDependencies) {
			t.Helper()
			root := t.TempDir()
			source = filepath.Join(root, "source", ".beads")
			target := filepath.Join(root, "target", ".beads")
			selector := filepath.Join(source, "nested", "issues.db")
			makeOwnershipDirectory(t, filepath.Dir(selector))
			makeOwnershipDirectory(t, target)
			writeOwnershipRedirect(t, source, target)
			writeOwnershipMetadata(t, source, configfile.Config{DoltDatabase: "structural_a"})
			selection, err := selectInitBackendSelection(initBackendSelectorCandidates{beadsDir: source})
			if err != nil {
				t.Fatal(err)
			}
			deps = initBackendPreflightDependencies{
				resolveSelection:   func(*cobra.Command) (initBackendSelection, error) { return selection, nil },
				inspectWorkspace:   inspectBackendWorkspaceSnapshot,
				inspectFreshTarget: inspectInitBackendFreshTarget,
				admit:              admitInitBackend,
			}
			cmd = newInitBackendPreflightTestCommand(t, configfile.BackendDolt)
			return source, cmd, deps
		}

		t.Run("stable", func(t *testing.T) {
			_, cmd, deps := newFixture(t)
			if err := prepareInitBackendPreflightWith(cmd, deps); err != nil {
				t.Fatal(err)
			}
			admission, err := consumeInitBackendPreflightWith(cmd, deps)
			if err != nil || admission.redirectDatabase != "structural_a" {
				t.Fatalf("structural redirect admission = %#v, %v; want captured database", admission, err)
			}
		})

		t.Run("drift", func(t *testing.T) {
			source, cmd, deps := newFixture(t)
			if err := prepareInitBackendPreflightWith(cmd, deps); err != nil {
				t.Fatal(err)
			}
			writeOwnershipMetadata(t, source, configfile.Config{DoltDatabase: "structural_b"})
			if _, err := consumeInitBackendPreflightWith(cmd, deps); !errors.Is(err, errInitBackendPreflightChanged) {
				t.Fatalf("structural source drift error = %v, want changed-preflight refusal", err)
			}
		})
	})
}

func TestInitBackendPreflightRejectsBypassesAndDrift(t *testing.T) {
	t.Run("unknown backend is rejected before inspection", func(t *testing.T) {
		cmd := newInitBackendPreflightTestCommand(t, "mongodb")
		inspectCalls := 0
		err := prepareInitBackendPreflightWith(cmd, initBackendPreflightDependencies{
			resolveSelection: func(*cobra.Command) (initBackendSelection, error) {
				return initBackendSelection{source: initBackendSelectionBeadsDir, selector: t.TempDir()}, nil
			},
			inspectWorkspace: func(string) (*backendWorkspaceSnapshot, error) {
				inspectCalls++
				return nil, nil
			},
			inspectFreshTarget: inspectInitBackendFreshTarget,
			admit:              admitInitBackend,
		})
		if err == nil || !strings.Contains(err.Error(), "unknown backend") || inspectCalls != 0 {
			t.Fatalf("error=%v inspectCalls=%d, want early unknown-backend rejection", err, inspectCalls)
		}
	})

	t.Run("force-style flags cannot bypass mismatch", func(t *testing.T) {
		for _, flags := range [][]string{
			nil,
			{"force"},
			{"reinit-local"},
			{"init-if-missing"},
			{"force", "reinit-local", "init-if-missing"},
		} {
			cmd := newInitBackendPreflightTestCommand(t, configfile.BackendDolt)
			for _, flag := range flags {
				cmd.Flags().Bool(flag, false, "")
				if err := cmd.Flags().Set(flag, "true"); err != nil {
					t.Fatal(err)
				}
			}
			snapshot := newBackendStabilizerSnapshot(t)
			calls := 0
			selection := initBackendSelection{source: initBackendSelectionBeadsDir, selector: snapshot.selector, creationBeadsDir: snapshot.selector}
			deps := initBackendPreflightDependencies{
				resolveSelection:   func(*cobra.Command) (initBackendSelection, error) { return selection, nil },
				inspectWorkspace:   func(string) (*backendWorkspaceSnapshot, error) { calls++; return snapshot, nil },
				inspectFreshTarget: inspectInitBackendFreshTarget,
				admit:              admitInitBackend,
			}
			err := prepareInitBackendPreflightWith(cmd, deps)
			if !errors.Is(err, errBackendChangeRequiresMigration) || calls != 1 || takeInitBackendPreflight(cmd) != nil {
				t.Fatalf("flags=%v error=%v calls=%d", flags, err, calls)
			}
		}
	})

	for _, test := range []struct {
		name   string
		mutate func(*cobra.Command, *backendWorkspaceSnapshot, *initBackendSelection)
	}{
		{name: "selector", mutate: func(_ *cobra.Command, _ *backendWorkspaceSnapshot, selection *initBackendSelection) {
			selection.selector += "-other"
		}},
		{name: "selection source", mutate: func(_ *cobra.Command, _ *backendWorkspaceSnapshot, selection *initBackendSelection) {
			selection.source = initBackendSelectionDiscovered
		}},
		{name: "snapshot", mutate: func(_ *cobra.Command, snapshot *backendWorkspaceSnapshot, _ *initBackendSelection) {
			snapshot.route.bindingSources[0].exists = false
		}},
		{name: "requested backend", mutate: func(cmd *cobra.Command, _ *backendWorkspaceSnapshot, _ *initBackendSelection) {
			if err := cmd.Flags().Set("backend", configfile.BackendDolt); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name+" drift", func(t *testing.T) {
			first := newBackendStabilizerSnapshot(t)
			second := cloneBackendStabilizerSnapshot(first)
			selection := initBackendSelection{source: initBackendSelectionBeadsDir, selector: first.selector, creationBeadsDir: first.selector}
			cmd := newInitBackendPreflightTestCommand(t, configfile.BackendSQLite)
			inspectCalls, admitCalls := 0, 0
			deps := initBackendPreflightDependencies{
				resolveSelection: func(*cobra.Command) (initBackendSelection, error) { return selection, nil },
				inspectWorkspace: func(string) (*backendWorkspaceSnapshot, error) {
					inspectCalls++
					if inspectCalls == 1 {
						return first, nil
					}
					return second, nil
				},
				inspectFreshTarget: inspectInitBackendFreshTarget,
				admit: func(requested string, snapshot *backendWorkspaceSnapshot) (string, error) {
					admitCalls++
					return admitInitBackend(requested, snapshot)
				},
			}
			if err := prepareInitBackendPreflightWith(cmd, deps); err != nil {
				t.Fatal(err)
			}
			test.mutate(cmd, second, &selection)
			if got, err := consumeInitBackendPreflightWith(cmd, deps); got != (initBackendAdmission{}) || !errors.Is(err, errInitBackendPreflightChanged) {
				t.Fatalf("consume = %#v, %v; want changed-preflight error", got, err)
			}
			if takeInitBackendPreflight(cmd) != nil {
				t.Fatal("failed consume retained stale preflight")
			}
			if admitCalls != 2 {
				t.Fatalf("admission calls = %d, want exact prepare+RunE rerun", admitCalls)
			}
		})
	}

	t.Run("fresh target drift", func(t *testing.T) {
		selection, err := selectInitBackendSelection(initBackendSelectorCandidates{beadsDir: filepath.Join(t.TempDir(), ".beads")})
		if err != nil {
			t.Fatal(err)
		}
		first, err := inspectInitBackendFreshTarget(selection.creationBeadsDir)
		if err != nil {
			t.Fatal(err)
		}
		second := *first
		second.root.mode = 0o755
		freshCalls, admitCalls := 0, 0
		deps := initBackendPreflightDependencies{
			resolveSelection: func(*cobra.Command) (initBackendSelection, error) { return selection, nil },
			inspectWorkspace: func(string) (*backendWorkspaceSnapshot, error) { return nil, nil },
			inspectFreshTarget: func(string) (*initBackendFreshTargetSnapshot, error) {
				freshCalls++
				if freshCalls == 1 {
					return first, nil
				}
				return &second, nil
			},
			admit: func(requested string, snapshot *backendWorkspaceSnapshot) (string, error) {
				admitCalls++
				return admitInitBackend(requested, snapshot)
			},
		}
		cmd := newInitBackendPreflightTestCommand(t, configfile.BackendDolt)
		if err := prepareInitBackendPreflightWith(cmd, deps); err != nil {
			t.Fatal(err)
		}
		if _, err := consumeInitBackendPreflightWith(cmd, deps); !errors.Is(err, errInitBackendPreflightChanged) {
			t.Fatalf("consume error = %v, want changed fresh target", err)
		}
		if admitCalls != 2 {
			t.Fatalf("admission calls = %d, want 2", admitCalls)
		}
	})
}

func TestRootInitPreflightRejectsBeforeRuntimeSetup(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	writeOwnershipMetadata(t, beadsDir, configfile.Config{Backend: configfile.BackendSQLite, SQLitePath: "beads.db"})
	sentinelPath := filepath.Join(beadsDir, "sentinel")
	if err := os.WriteFile(sentinelPath, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	metadataBefore, err := os.ReadFile(configfile.ConfigPath(beadsDir))
	if err != nil {
		t.Fatal(err)
	}
	workspaceBefore := hashBackendPreflightTree(t, beadsDir)
	t.Setenv("BEADS_DIR", beadsDir)
	t.Setenv("BEADS_DB", "")
	t.Setenv("BD_DB", "")
	t.Setenv("BD_BACKEND", "")
	t.Setenv("BD_DATABASE_BACKEND", "")
	t.Setenv("BD_DISABLE_METRICS", "1")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	saveRootFlag := func(name string) func() {
		flag := rootCmd.PersistentFlags().Lookup(name)
		value, changed := flag.Value.String(), flag.Changed
		return func() { _ = flag.Value.Set(value); flag.Changed = changed }
	}
	saveInitFlag := func(name string) func() {
		flag := initCmd.Flags().Lookup(name)
		value, changed := flag.Value.String(), flag.Changed
		return func() { _ = flag.Value.Set(value); flag.Changed = changed }
	}
	restoreFlags := []func(){
		saveRootFlag("db"), saveRootFlag("directory"),
		saveInitFlag("backend"), saveInitFlag("force"), saveInitFlag("reinit-local"), saveInitFlag("init-if-missing"), saveInitFlag("quiet"),
	}
	oldDBPath, oldChangeDir := dbPath, changeDir
	oldRootCtx, oldRootCancel, oldChangeDirSnapshot := rootCtx, rootCancel, changeDirEnvSnapshot
	t.Cleanup(func() {
		for i := len(restoreFlags) - 1; i >= 0; i-- {
			restoreFlags[i]()
		}
		dbPath, changeDir = oldDBPath, oldChangeDir
		rootCtx, rootCancel = oldRootCtx, oldRootCancel
		changeDirEnvSnapshot = oldChangeDirSnapshot
		rootCmd.SetArgs(nil)
		clearInitBackendPreflight(initCmd)
		resetCommandContext()
	})
	dbPath, changeDir = "", ""
	for _, flag := range []struct {
		name string
		root bool
	}{
		{name: "db", root: true}, {name: "directory", root: true},
		{name: "backend"}, {name: "force"}, {name: "reinit-local"}, {name: "init-if-missing"}, {name: "quiet"},
	} {
		selected := initCmd.Flags().Lookup(flag.name)
		if flag.root {
			selected = rootCmd.PersistentFlags().Lookup(flag.name)
		}
		if err := selected.Value.Set(selected.DefValue); err != nil {
			t.Fatal(err)
		}
		selected.Changed = false
	}
	rootCtx, rootCancel = nil, nil
	changeDirEnvSnapshot = nil
	rootCmd.SetArgs([]string{"init", "--backend=dolt", "--force", "--reinit-local", "--init-if-missing", "--quiet"})

	var execErr error
	stderr := captureStderr(t, func() { execErr = rootCmd.Execute() })
	err = execErr
	if !errors.Is(err, errBackendChangeRequiresMigration) {
		t.Fatalf("Execute error = %v, want migration refusal", err)
	}
	if rootCtx != nil || rootCancel != nil || cmdCtx == nil || cmdCtx.RootCtx != nil {
		t.Fatalf("runtime initialized before refusal: root=%v cancel=%v context=%#v", rootCtx, rootCancel, cmdCtx)
	}
	if strings.Contains(stderr, "DeprecationWarning") || strings.Contains(stderr, "Skipping init") {
		t.Fatalf("destructive/idempotent flag output preceded refusal: %q", stderr)
	}
	metadataAfter, err := os.ReadFile(configfile.ConfigPath(beadsDir))
	if err != nil || string(metadataAfter) != string(metadataBefore) {
		t.Fatalf("metadata changed after refusal: err=%v\nbefore=%s\nafter=%s", err, metadataBefore, metadataAfter)
	}
	if sentinel, err := os.ReadFile(sentinelPath); err != nil || string(sentinel) != "unchanged" {
		t.Fatalf("workspace sentinel changed after refusal: %q, %v", sentinel, err)
	}
	if workspaceAfter := hashBackendPreflightTree(t, beadsDir); workspaceAfter != workspaceBefore {
		t.Fatalf("workspace tree changed after initial admission refusal: before=%x after=%x", workspaceBefore, workspaceAfter)
	}
}

func TestInitRunERevalidatesBeforeEffectsAndRestoresChangeDir(t *testing.T) {
	root := t.TempDir()
	caller := filepath.Join(root, "caller")
	project := filepath.Join(root, "project")
	beadsDir := filepath.Join(project, ".beads")
	if err := os.MkdirAll(caller, 0o700); err != nil {
		t.Fatal(err)
	}
	writeOwnershipMetadata(t, beadsDir, configfile.Config{Backend: configfile.BackendSQLite, SQLitePath: "beads.db"})
	if err := os.WriteFile(filepath.Join(beadsDir, "sentinel"), []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, ".env"), []byte("RUNTIME_PREFLIGHT_CANARY=must-not-load\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(caller)
	for _, key := range []string{"BEADS_DIR", "BEADS_DB", "BD_DB", "RUNTIME_PREFLIGHT_CANARY"} {
		t.Setenv(key, "test-cleanup")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("BD_BACKEND", "")
	t.Setenv("BD_DATABASE_BACKEND", "")
	t.Setenv("BD_DISABLE_METRICS", "1")
	xdgConfigHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgConfigHome)

	saveRootFlag := func(name string) func() {
		flag := rootCmd.PersistentFlags().Lookup(name)
		value, changed := flag.Value.String(), flag.Changed
		return func() { _ = flag.Value.Set(value); flag.Changed = changed }
	}
	saveInitFlag := func(name string) func() {
		flag := initCmd.Flags().Lookup(name)
		value, changed := flag.Value.String(), flag.Changed
		return func() { _ = flag.Value.Set(value); flag.Changed = changed }
	}
	restoreFlags := []func(){
		saveRootFlag("db"), saveRootFlag("directory"),
		saveInitFlag("backend"), saveInitFlag("force"), saveInitFlag("reinit-local"), saveInitFlag("init-if-missing"), saveInitFlag("quiet"),
	}
	oldDBPath, oldChangeDir := dbPath, changeDir
	oldRootCtx, oldRootCancel, oldChangeDirSnapshot := rootCtx, rootCancel, changeDirEnvSnapshot
	oldPreRunE, oldInitContext, oldCommandContext := initCmd.PreRunE, initCmd.Context(), cmdCtx
	t.Cleanup(func() {
		if rootCancel != nil {
			rootCancel()
		}
		for i := len(restoreFlags) - 1; i >= 0; i-- {
			restoreFlags[i]()
		}
		dbPath, changeDir = oldDBPath, oldChangeDir
		rootCtx, rootCancel = oldRootCtx, oldRootCancel
		changeDirEnvSnapshot = oldChangeDirSnapshot
		initCmd.PreRunE = oldPreRunE
		initCmd.SetContext(oldInitContext)
		cmdCtx = oldCommandContext
		rootCmd.SetArgs(nil)
	})
	dbPath, changeDir = "", ""
	for _, flag := range []struct {
		name string
		root bool
	}{
		{name: "db", root: true}, {name: "directory", root: true},
		{name: "backend"}, {name: "force"}, {name: "reinit-local"}, {name: "init-if-missing"}, {name: "quiet"},
	} {
		selected := initCmd.Flags().Lookup(flag.name)
		if flag.root {
			selected = rootCmd.PersistentFlags().Lookup(flag.name)
		}
		if err := selected.Value.Set(selected.DefValue); err != nil {
			t.Fatal(err)
		}
		selected.Changed = false
	}
	rootCtx, rootCancel = nil, nil
	changeDirEnvSnapshot = nil
	clearInitBackendPreflight(initCmd)

	var treeAtRunE [sha256.Size]byte
	mutated := false
	initCmd.PreRunE = func(*cobra.Command, []string) error {
		cfg, err := configfile.Load(beadsDir)
		if err != nil || cfg == nil {
			return fmt.Errorf("load mutation fixture: %w", err)
		}
		cfg.ProjectID = "changed-between-preflight-and-rune"
		if err := cfg.Save(beadsDir); err != nil {
			return err
		}
		treeAtRunE = hashBackendPreflightTree(t, beadsDir)
		mutated = true
		return nil
	}
	rootCmd.SetArgs([]string{"--directory", project, "init", "--backend=sqlite", "--force", "--quiet"})

	var execErr error
	stderr := captureStderr(t, func() { execErr = rootCmd.Execute() })
	if !errors.Is(execErr, errInitBackendPreflightChanged) {
		t.Fatalf("Execute error = %v, want RunE drift refusal", execErr)
	}
	if !mutated {
		t.Fatal("fixture PreRunE did not execute between root preflight and init RunE")
	}
	if treeAfter := hashBackendPreflightTree(t, beadsDir); treeAfter != treeAtRunE {
		t.Fatalf("RunE changed workspace after revalidation point: before=%x after=%x", treeAtRunE, treeAfter)
	}
	if strings.Contains(stderr, "DeprecationWarning") || strings.Contains(stderr, "Skipping init") {
		t.Fatalf("init effects preceded RunE refusal: %q", stderr)
	}
	if _, present := os.LookupEnv("RUNTIME_PREFLIGHT_CANARY"); present {
		t.Fatalf("target runtime environment loaded before RunE refusal: %q", os.Getenv("RUNTIME_PREFLIGHT_CANARY"))
	}
	if got := os.Getenv("BEADS_DIR"); got != "" || changeDirEnvSnapshot != nil {
		t.Fatalf("-C selection leaked after RunE refusal: BEADS_DIR=%q snapshot=%#v", got, changeDirEnvSnapshot)
	}
	if entries, err := os.ReadDir(xdgConfigHome); err != nil || len(entries) != 0 {
		t.Fatalf("disabled metrics/config path changed before RunE refusal: entries=%v err=%v", entries, err)
	}
}

func TestInitRunERestoresChangeDirAfterLateValidationError(t *testing.T) {
	isolateInitBackendBindingGlobals(t)
	initConfigForTest(t)
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	writeOwnershipMetadata(t, beadsDir, configfile.Config{Backend: configfile.BackendSQLite, SQLitePath: "beads.db"})
	t.Setenv("BEADS_DIR", "original-beads-dir")
	t.Setenv("BEADS_DB", "")
	t.Setenv("BD_DB", "")
	oldSnapshot := changeDirEnvSnapshot
	changeDirEnvSnapshot = map[string]envSnapshotValue{
		"BEADS_DIR": {value: "original-beads-dir", ok: true},
		"BEADS_DB":  {value: "", ok: true},
		"BD_DB":     {value: "", ok: true},
	}
	t.Cleanup(func() { changeDirEnvSnapshot = oldSnapshot })
	if err := os.Setenv("BEADS_DIR", beadsDir); err != nil {
		t.Fatal(err)
	}

	cmd := newInitBackendPreflightTestCommand(t, configfile.BackendSQLite)
	cmd.RunE = initCmd.RunE
	cmd.Flags().Bool("server", false, "")
	if err := cmd.Flags().Set("server", "true"); err != nil {
		t.Fatal(err)
	}
	if err := prepareInitBackendPreflight(cmd); err != nil {
		t.Fatal(err)
	}
	err := cmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "does not support --server") {
		t.Fatalf("RunE error = %v, want late non-Dolt validation refusal", err)
	}
	if got := os.Getenv("BEADS_DIR"); got != "original-beads-dir" || changeDirEnvSnapshot != nil {
		t.Fatalf("late RunE error leaked -C state: BEADS_DIR=%q snapshot=%#v", got, changeDirEnvSnapshot)
	}
}

func TestInitRunERequiresPreflightBeforeEffects(t *testing.T) {
	cmd := newInitBackendPreflightTestCommand(t, configfile.BackendDolt)
	cmd.RunE = initCmd.RunE
	cmd.Flags().Bool("force", false, "")
	if err := cmd.Flags().Set("force", "true"); err != nil {
		t.Fatal(err)
	}
	cmdCtx = &CommandContext{}
	t.Cleanup(resetCommandContext)

	err := cmd.RunE(cmd, nil)
	if !errors.Is(err, errInitBackendPreflightMissing) {
		t.Fatalf("RunE error = %v, want missing-preflight sentinel", err)
	}
}

func newInitBackendPreflightTestCommand(t *testing.T, backend string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "init"}
	cmd.Flags().String("backend", "", "")
	if err := cmd.Flags().Set("backend", backend); err != nil {
		t.Fatal(err)
	}
	return cmd
}

func isolateInitBackendBindingGlobals(t *testing.T) {
	t.Helper()
	oldDBPath, oldCommandContext := dbPath, cmdCtx
	oldJSONOutput, oldReadonlyMode := jsonOutput, readonlyMode
	oldActor, oldDoltAutoCommit := actor, doltAutoCommit
	oldServerMode, oldProxiedServerMode := serverMode, proxiedServerMode
	cmdCtx = nil
	t.Cleanup(func() {
		dbPath, cmdCtx = oldDBPath, oldCommandContext
		jsonOutput, readonlyMode = oldJSONOutput, oldReadonlyMode
		actor, doltAutoCommit = oldActor, oldDoltAutoCommit
		serverMode, proxiedServerMode = oldServerMode, oldProxiedServerMode
	})
}

func hashBackendPreflightTree(t *testing.T, root string) [sha256.Size]byte {
	t.Helper()
	hash := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(hash, "%s\x00%v\x00", filepath.ToSlash(relative), info.Mode())
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			_, _ = io.WriteString(hash, target)
		case info.Mode().IsRegular():
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(hash, file)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		}
		_, _ = hash.Write([]byte{0})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	return digest
}
