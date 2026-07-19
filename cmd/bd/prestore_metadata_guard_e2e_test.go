//go:build cgo

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestListRejectsV0554MetadataBeforeWorkspaceMutation(t *testing.T) {
	bd := buildBDUnderTest(t)
	repoDir, beadsDir := newV0554MetadataWorkspace(t)
	before := hashBackendPreflightTree(t, beadsDir)

	stdout, stderr, err := runPrestoreGuardCommand(t, bd, repoDir, "list", "--json", "--limit", "0", "--all")
	output := stdout + stderr

	if err == nil {
		t.Errorf("bd list succeeded against incompatible v0.55.4 metadata; stdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if !strings.Contains(output, `unknown or noncanonical metadata field "jsonl_export"`) {
		t.Errorf("bd list did not report the incompatible v0.55.4 metadata field; err=%v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	after := hashBackendPreflightTree(t, beadsDir)
	if before != after {
		t.Errorf("bd list changed the legacy workspace before refusing: before=%x after=%x", before, after)
	}
}

func TestIncompatibleMetadataStillAllowsStoreFreeCommands(t *testing.T) {
	bd := buildBDUnderTest(t)
	tests := []struct {
		name string
		args []string
	}{
		{name: "version", args: []string{"version"}},
		{name: "root_help_flag", args: []string{"--help"}},
		{name: "help_command", args: []string{"help"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repoDir, beadsDir := newV0554MetadataWorkspace(t)
			before := hashBackendPreflightTree(t, beadsDir)

			stdout, stderr, err := runPrestoreGuardCommand(t, bd, repoDir, test.args...)
			if err != nil {
				t.Fatalf("bd %s failed against incompatible store metadata: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(test.args, " "), err, stdout, stderr)
			}
			if strings.TrimSpace(stdout) == "" {
				t.Errorf("bd %s succeeded without its expected command output", strings.Join(test.args, " "))
			}

			after := hashBackendPreflightTree(t, beadsDir)
			if before != after {
				t.Errorf("bd %s changed the incompatible workspace: before=%x after=%x", strings.Join(test.args, " "), before, after)
			}
		})
	}
}

func TestCanonicalSQLiteMetadataPassesCompatibilityGuard(t *testing.T) {
	bd := buildBDUnderTest(t)
	repoDir := newPrestoreGuardRepo(t)

	stdout, stderr, err := runPrestoreGuardCommand(t, bd, repoDir,
		"init", "--quiet", "--non-interactive", "--prefix", "current",
		"--backend", "sqlite", "--skip-hooks", "--skip-agents")
	if err != nil {
		t.Fatalf("initialize canonical SQLite workspace: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	stdout, stderr, err = runPrestoreGuardCommand(t, bd, repoDir, "list", "--json", "--limit", "0", "--all")
	if err != nil {
		t.Fatalf("list canonical SQLite workspace: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	var issues []json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &issues); err != nil {
		t.Fatalf("parse list JSON from canonical SQLite workspace: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if len(issues) != 0 {
		t.Fatalf("new canonical SQLite workspace returned %d issues, want none", len(issues))
	}
}

func TestCanonicalSQLiteMetadataThroughSymlinkPassesCompatibilityGuard(t *testing.T) {
	bd := buildBDUnderTest(t)
	repoDir := newPrestoreGuardRepo(t)

	stdout, stderr, err := runPrestoreGuardCommand(t, bd, repoDir,
		"init", "--quiet", "--non-interactive", "--prefix", "current-link",
		"--backend", "sqlite", "--skip-hooks", "--skip-agents")
	if err != nil {
		t.Fatalf("initialize canonical SQLite workspace: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	beadsLink := filepath.Join(repoDir, ".beads")
	targetBeadsDir := filepath.Join(t.TempDir(), "beads-data")
	if err := os.Rename(beadsLink, targetBeadsDir); err != nil {
		t.Fatalf("move candidate-created beads directory to symlink target: %v", err)
	}
	if err := os.Symlink(targetBeadsDir, beadsLink); err != nil {
		t.Skipf("symlinked .beads directories are unavailable: %v", err)
	}
	linkInfo, err := os.Lstat(beadsLink)
	if err != nil || linkInfo.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("repo .beads is not the expected symlink: info=%v err=%v", linkInfo, err)
	}
	resolvedLink, err := filepath.EvalSymlinks(beadsLink)
	if err != nil {
		t.Fatalf("resolve repo .beads symlink: %v", err)
	}
	resolvedTarget, err := filepath.EvalSymlinks(targetBeadsDir)
	if err != nil {
		t.Fatalf("resolve target beads directory: %v", err)
	}
	if resolvedLink != resolvedTarget {
		t.Fatalf("repo .beads resolves to %q, want %q", resolvedLink, resolvedTarget)
	}

	metadataPath := filepath.Join(targetBeadsDir, "metadata.json")
	metadataBefore, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatalf("read canonical metadata before list: %v", err)
	}
	stdout, stderr, err = runPrestoreGuardCommand(t, bd, repoDir, "list", "--json", "--limit", "0", "--all")
	if err != nil {
		t.Fatalf("list canonical SQLite workspace through .beads symlink: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	var issues []json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &issues); err != nil {
		t.Fatalf("parse list JSON from symlinked SQLite workspace: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if len(issues) != 0 {
		t.Fatalf("new symlinked SQLite workspace returned %d issues, want none", len(issues))
	}
	metadataAfter, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatalf("read canonical metadata after list: %v", err)
	}
	if !bytes.Equal(metadataBefore, metadataAfter) {
		t.Error("list changed canonical metadata through the .beads symlink")
	}

	if target, err := os.Readlink(beadsLink); err != nil || target != targetBeadsDir {
		t.Errorf("repo .beads symlink changed: target=%q err=%v, want %q", target, err, targetBeadsDir)
	}
}

func TestRetiredSameLineageMetadataFieldPassesCompatibilityGuard(t *testing.T) {
	bd := buildBDUnderTest(t)
	repoDir := newPrestoreGuardRepo(t)

	stdout, stderr, err := runPrestoreGuardCommand(t, bd, repoDir,
		"init", "--quiet", "--non-interactive", "--prefix", "retired",
		"--backend", "sqlite", "--skip-hooks", "--skip-agents")
	if err != nil {
		t.Fatalf("initialize workspace: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	// Inject a field this lineage persisted under an older bd version and has
	// since removed from Config (dolt_proxied_server_config, removed in
	// 5a7cc3e1a). The lenient store-open loader has always ignored such keys;
	// the strict pre-store guard must tolerate them too, or every field removal
	// silently locks out existing same-lineage workspaces. This is the mirror of
	// TestListRejectsV0554MetadataBeforeWorkspaceMutation, which proves a
	// genuinely foreign field is still refused.
	metadataPath := filepath.Join(repoDir, ".beads", "metadata.json")
	injectMetadataField(t, metadataPath, "dolt_proxied_server_config", "proxied-config.yaml")

	stdout, stderr, err = runPrestoreGuardCommand(t, bd, repoDir, "list", "--json", "--limit", "0", "--all")
	if err != nil {
		t.Fatalf("list workspace carrying a retired same-lineage field: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	var issues []json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &issues); err != nil {
		t.Fatalf("parse list JSON: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if len(issues) != 0 {
		t.Fatalf("workspace returned %d issues, want none", len(issues))
	}
}

// A legacy .beads/config.json (the pre-metadata.json filename) carrying a
// forward-incompatible field must be refused by the guard just like a canonical
// metadata.json, and — critically — discovery must not migrate/strip it before
// the guard runs. Before the fix, the pre-guard discovery reads
// (findDatabaseInBeadsDir, resolveBeadsDirForDBPath, the backend probe) went
// through the migrating configfile.Load, which rewrote config.json into a
// field-stripped metadata.json and opened the workspace instead of refusing it.
func TestListRejectsLegacyConfigJSONBeforeWorkspaceMutation(t *testing.T) {
	bd := buildBDUnderTest(t)
	repoDir, beadsDir := newV0554LegacyConfigWorkspace(t)
	before := hashBackendPreflightTree(t, beadsDir)

	stdout, stderr, err := runPrestoreGuardCommand(t, bd, repoDir, "list", "--json", "--limit", "0", "--all")
	output := stdout + stderr

	if err == nil {
		t.Errorf("bd list succeeded against incompatible legacy config.json; stdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if !strings.Contains(output, `unknown or noncanonical metadata field "jsonl_export"`) {
		t.Errorf("bd list did not report the incompatible legacy field; err=%v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	if _, statErr := os.Stat(filepath.Join(beadsDir, "metadata.json")); !os.IsNotExist(statErr) {
		t.Errorf("bd list migrated the legacy config.json into metadata.json before refusing: %v", statErr)
	}
	after := hashBackendPreflightTree(t, beadsDir)
	if before != after {
		t.Errorf("bd list changed the legacy config.json workspace before refusing: before=%x after=%x", before, after)
	}
}

// A SQL-backend (or server-mode) workspace has no local Dolt directory, so its
// database path is resolved from metadata.json alone. Discovery therefore must
// stay lenient: a strict read here would fail to resolve the workspace and the
// command would exit "no beads database found" instead of the guard's precise
// incompatibility message. This pins that discovery uses the lenient,
// never-migrating LoadForDiscovery rather than the strict LoadReadOnly.
func TestListRejectsIncompatibleSQLBackendMetadataWithoutLocalDir(t *testing.T) {
	bd := buildBDUnderTest(t)
	repoDir := newPrestoreGuardRepo(t)
	beadsDir := filepath.Join(repoDir, ".beads")
	writeFile(t, filepath.Join(beadsDir, "metadata.json"), []byte(`{
  "database": "beads.db",
  "jsonl_export": "issues.jsonl",
  "backend": "sqlite"
}`))
	writeFile(t, filepath.Join(beadsDir, ".local_version"), []byte("0.55.4\n"))
	before := hashBackendPreflightTree(t, beadsDir)

	stdout, stderr, err := runPrestoreGuardCommand(t, bd, repoDir, "list", "--json", "--limit", "0", "--all")
	output := stdout + stderr

	if err == nil {
		t.Errorf("bd list succeeded against incompatible SQL-backend metadata; stdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if !strings.Contains(output, `unknown or noncanonical metadata field "jsonl_export"`) {
		t.Errorf("bd list did not report the incompatibility for a no-local-dir workspace; err=%v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if strings.Contains(output, "no beads database found") {
		t.Errorf("discovery regressed to \"no beads database found\" instead of the guard message:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}

	after := hashBackendPreflightTree(t, beadsDir)
	if before != after {
		t.Errorf("bd list changed the incompatible workspace before refusing: before=%x after=%x", before, after)
	}
}

// bd doctor is in noDbCommands and skips the pre-store guard, so it must run its
// own strict metadata check before version tracking, auto-migration, or store
// open. Full diagnostics only run in server mode (embedded/proxied short-circuit
// earlier), which is exactly the path that would otherwise reach the lenient,
// migrating NewSharedStore. The check stays non-fatal (doctor is the repair
// path): it reports the incompatibility and exits zero without mutating the
// workspace or opening the store.
func TestDoctorReportsIncompatibleMetadataWithoutMutation(t *testing.T) {
	bd := buildBDUnderTest(t)
	repoDir := newPrestoreGuardRepo(t)
	beadsDir := filepath.Join(repoDir, ".beads")
	// Server mode so `bd doctor` runs full diagnostics; the foreign field
	// (jsonl_export) makes the metadata incompatible with this bd version.
	writeFile(t, filepath.Join(beadsDir, "metadata.json"), []byte(`{
  "database": "dolt",
  "jsonl_export": "issues.jsonl",
  "backend": "dolt",
  "dolt_mode": "server",
  "dolt_database": "beads_smoke"
}`))
	writeFile(t, filepath.Join(beadsDir, ".local_version"), []byte("0.55.4\n"))
	before := hashBackendPreflightTree(t, beadsDir)

	stdout, stderr, err := runPrestoreGuardCommand(t, bd, repoDir, "doctor", "--json")
	output := stdout + stderr
	if err != nil {
		t.Fatalf("bd doctor failed against incompatible metadata (doctor is the repair path and must run): %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if !strings.Contains(output, "jsonl_export") {
		t.Errorf("bd doctor did not report the metadata incompatibility; stdout:\n%s\nstderr:\n%s", stdout, stderr)
	}

	after := hashBackendPreflightTree(t, beadsDir)
	if before != after {
		t.Errorf("bd doctor changed the incompatible workspace: before=%x after=%x", before, after)
	}
}

// injectMetadataField adds or overwrites a single top-level key in a
// metadata.json file, preserving the other keys, so tests can simulate a
// workspace carrying an extra (e.g. retired) field.
func injectMetadataField(t *testing.T, metadataPath, key string, value any) {
	t.Helper()
	raw, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatalf("read metadata for field injection: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("decode metadata for field injection: %v", err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode injected metadata value: %v", err)
	}
	fields[key] = encoded
	updated, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		t.Fatalf("encode metadata after field injection: %v", err)
	}
	if err := os.WriteFile(metadataPath, updated, 0o600); err != nil {
		t.Fatalf("write metadata after field injection: %v", err)
	}
}

func newV0554MetadataWorkspace(t *testing.T) (repoDir, beadsDir string) {
	t.Helper()
	repoDir = newPrestoreGuardRepo(t)

	beadsDir = filepath.Join(repoDir, ".beads")
	// v0.55.4 persisted jsonl_export even when it held the default filename.
	// The current strict metadata reader deliberately no longer accepts that
	// field, so a store-backed command must refuse before running any upgrade
	// tracking, migration, or store-open path.
	writeFile(t, filepath.Join(beadsDir, "metadata.json"), []byte(`{
  "database": "dolt",
  "jsonl_export": "issues.jsonl",
  "backend": "dolt",
  "dolt_database": "beads_smoke"
}`))
	writeFile(t, filepath.Join(beadsDir, ".local_version"), []byte("0.55.4\n"))
	writeFile(t, filepath.Join(beadsDir, "dolt", "source-marker"), []byte("legacy source\n"))
	if err := os.Chmod(beadsDir, 0o700); err != nil {
		t.Fatalf("chmod legacy beads directory: %v", err)
	}
	return repoDir, beadsDir
}

// newV0554LegacyConfigWorkspace mirrors newV0554MetadataWorkspace but persists
// the forward-incompatible field in a legacy .beads/config.json (the filename
// used before the metadata.json rename) with no metadata.json, exercising the
// discovery paths that historically migrated the legacy file before the guard
// could refuse it.
func newV0554LegacyConfigWorkspace(t *testing.T) (repoDir, beadsDir string) {
	t.Helper()
	repoDir = newPrestoreGuardRepo(t)

	beadsDir = filepath.Join(repoDir, ".beads")
	writeFile(t, filepath.Join(beadsDir, "config.json"), []byte(`{
  "database": "dolt",
  "jsonl_export": "issues.jsonl",
  "backend": "dolt",
  "dolt_database": "beads_smoke"
}`))
	writeFile(t, filepath.Join(beadsDir, ".local_version"), []byte("0.55.4\n"))
	writeFile(t, filepath.Join(beadsDir, "dolt", "source-marker"), []byte("legacy source\n"))
	if err := os.Chmod(beadsDir, 0o700); err != nil {
		t.Fatalf("chmod legacy beads directory: %v", err)
	}
	return repoDir, beadsDir
}

func newPrestoreGuardRepo(t *testing.T) string {
	t.Helper()
	repoDir := t.TempDir()
	initGitRepo(t, repoDir)
	runGitForBootstrapTest(t, repoDir, "config", "core.hooksPath", ".git/hooks")
	runGitForBootstrapTest(t, repoDir, "config", "beads.role", "maintainer")
	return repoDir
}

func runPrestoreGuardCommand(t *testing.T, bd, repoDir string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bd, args...)
	cmd.Dir = repoDir
	cmd.Env = sanitizedPrestoreGuardEnv(t)
	var stdoutBuffer, stderrBuffer bytes.Buffer
	cmd.Stdout = &stdoutBuffer
	cmd.Stderr = &stderrBuffer
	err = cmd.Run()
	return stdoutBuffer.String(), stderrBuffer.String(), err
}

func sanitizedPrestoreGuardEnv(t *testing.T) []string {
	t.Helper()
	home := t.TempDir()
	env := make([]string, 0, len(os.Environ())+8)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "BEADS_") || strings.HasPrefix(name, "BD_") {
			continue
		}
		switch name {
		case "HOME", "USERPROFILE", "XDG_CONFIG_HOME", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_NOSYSTEM":
			continue
		}
		env = append(env, entry)
	}
	return append(env,
		"HOME="+home,
		"USERPROFILE="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, "xdg-config"),
		"GIT_CONFIG_GLOBAL="+filepath.Join(home, "gitconfig"),
		"GIT_CONFIG_NOSYSTEM=1",
		"BEADS_DOLT_AUTO_START=0",
		"BEADS_NO_DAEMON=1",
		"BD_DISABLE_METRICS=1",
		"BD_DISABLE_EVENT_FLUSH=1",
	)
}
