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

	"github.com/steveyegge/beads/internal/configfile"
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

func TestListRejectsV0570ServerWorkspaceBeforeMutation(t *testing.T) {
	bd := buildBDUnderTest(t)
	repoDir, beadsDir := newV0570ServerWorkspace(t)
	before := hashBackendPreflightTree(t, beadsDir)

	started := time.Now()
	stdout, stderr, err := runPrestoreGuardCommand(t, bd, repoDir,
		"list", "--json", "--limit", "0", "--all")
	elapsed := time.Since(started)
	output := stdout + stderr

	if err == nil {
		t.Errorf("bd list succeeded against a v0.57.0 server workspace; stdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if elapsed > 2*time.Second {
		t.Errorf("bd list took %s to refuse the v0.57.0 server workspace, want at most 2s", elapsed)
	}
	if !strings.Contains(output, "requires explicit migration") || !strings.Contains(output, "bd 0.57.0") {
		t.Errorf("bd list did not explain the required v0.57.0 migration; err=%v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if _, statErr := os.Stat(filepath.Join(beadsDir, "dolt", ".bd-dolt-ok")); statErr == nil {
		t.Error("bd list marked the legacy Dolt directory compatible before refusing it")
	} else if !os.IsNotExist(statErr) {
		t.Fatalf("inspect Dolt compatibility marker: %v", statErr)
	}

	after := hashBackendPreflightTree(t, beadsDir)
	if before != after {
		t.Errorf("bd list changed the v0.57.0 server workspace before refusing: before=%x after=%x", before, after)
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

func TestLegacyDoltServerGuardAcceptsCurrentWorkspaceShapes(t *testing.T) {
	tests := []struct {
		name         string
		backend      string
		doltMode     string
		localVersion string
		projectID    string
	}{
		{name: "current_server", backend: configfile.BackendDolt, doltMode: configfile.DoltModeServer, localVersion: Version, projectID: "current-project-id"},
		{name: "embedded", backend: configfile.BackendDolt, doltMode: configfile.DoltModeEmbedded, localVersion: "0.57.0"},
		{name: "proxied_server", backend: configfile.BackendDolt, doltMode: configfile.DoltModeProxiedServer, localVersion: "0.57.0"},
		{name: "postgres_provider", backend: configfile.BackendPostgres, localVersion: "0.57.0"},
		{name: "mysql_provider", backend: configfile.BackendMySQL, localVersion: "0.57.0"},
		{name: "sqlite_provider", backend: configfile.BackendSQLite, localVersion: "0.57.0"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			beadsDir := t.TempDir()
			writeFile(t, filepath.Join(beadsDir, ".local_version"), []byte(test.localVersion+"\n"))
			writeFile(t, filepath.Join(beadsDir, "dolt", ".dolt", "config.json"), []byte("{}\n"))
			writeFile(t, filepath.Join(beadsDir, "dolt", "smoke", ".dolt", "config.json"), []byte("{}\n"))
			cfg := &configfile.Config{
				Database:     "dolt",
				Backend:      test.backend,
				DoltMode:     test.doltMode,
				DoltDatabase: "smoke",
				ProjectID:    test.projectID,
			}
			if err := refuseLegacyDoltServerWorkspace(beadsDir, cfg); err != nil {
				t.Fatalf("current %s workspace rejected: %v", test.name, err)
			}
		})
	}
}

func TestLegacyDoltServerGuardDoesNotTrustMutableVersionMarkers(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, string)
	}{
		{name: "missing_local_version"},
		{name: "rewritten_local_version", setup: func(t *testing.T, beadsDir string) {
			writeFile(t, filepath.Join(beadsDir, ".local_version"), []byte(Version+"\n"))
			writeFile(t, filepath.Join(beadsDir, "dolt", ".bd-dolt-ok"), []byte("ok\n"))
		}},
		{name: "symlinked_local_version", setup: func(t *testing.T, beadsDir string) {
			target := filepath.Join(t.TempDir(), "version")
			writeFile(t, target, []byte("0.57.0\n"))
			if err := os.Symlink(target, filepath.Join(beadsDir, ".local_version")); err != nil {
				t.Skipf("version symlinks are unavailable: %v", err)
			}
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, beadsDir := newV0570ServerWorkspace(t)
			if err := os.Remove(filepath.Join(beadsDir, ".local_version")); err != nil {
				t.Fatalf("remove source version witness: %v", err)
			}
			if test.setup != nil {
				test.setup(t, beadsDir)
			}
			cfg := &configfile.Config{
				Database:     "dolt",
				Backend:      configfile.BackendDolt,
				DoltMode:     configfile.DoltModeServer,
				DoltDatabase: "smoke",
			}
			if err := refuseLegacyDoltServerWorkspace(beadsDir, cfg); err == nil {
				t.Fatal("legacy server layout was accepted because its mutable version witness was unavailable or rewritten")
			}
		})
	}
}

func TestLegacyDoltServerGuardFailsClosedOnAmbiguousShape(t *testing.T) {
	tests := []struct {
		name         string
		doltDatabase string
		setup        func(*testing.T, string)
	}{
		{name: "invalid_database_name", doltDatabase: "../smoke", setup: func(t *testing.T, beadsDir string) {
			if err := os.Mkdir(filepath.Join(beadsDir, "dolt"), 0o700); err != nil {
				t.Fatalf("create Dolt root: %v", err)
			}
		}},
		{name: "non_directory_dolt_root", doltDatabase: "smoke", setup: func(t *testing.T, beadsDir string) {
			writeFile(t, filepath.Join(beadsDir, "dolt"), []byte("ambiguous\n"))
		}},
		{name: "symlinked_dolt_root", doltDatabase: "smoke", setup: func(t *testing.T, beadsDir string) {
			target := filepath.Join(t.TempDir(), "dolt")
			if err := os.Mkdir(target, 0o700); err != nil {
				t.Fatalf("create Dolt symlink target: %v", err)
			}
			if err := os.Symlink(target, filepath.Join(beadsDir, "dolt")); err != nil {
				t.Skipf("Dolt-root symlinks are unavailable: %v", err)
			}
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			beadsDir := t.TempDir()
			test.setup(t, beadsDir)
			cfg := &configfile.Config{
				Database:     "dolt",
				Backend:      configfile.BackendDolt,
				DoltMode:     configfile.DoltModeServer,
				DoltDatabase: test.doltDatabase,
			}
			if err := refuseLegacyDoltServerWorkspace(beadsDir, cfg); err == nil {
				t.Fatal("ambiguous legacy server workspace was accepted")
			}
		})
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

func newV0570ServerWorkspace(t *testing.T) (repoDir, beadsDir string) {
	t.Helper()
	repoDir = newPrestoreGuardRepo(t)
	beadsDir = filepath.Join(repoDir, ".beads")

	// This is the canonical metadata written by the official v0.57.0 binary.
	writeFile(t, filepath.Join(beadsDir, "metadata.json"), []byte(`{
  "database": "dolt",
  "backend": "dolt",
  "dolt_mode": "server",
  "dolt_database": "smoke"
}`))
	writeFile(t, filepath.Join(beadsDir, ".local_version"), []byte("0.57.0\n"))

	// v0.57.0's external SQL server used .beads/dolt as a Dolt data root:
	// the root repository held the catalog and each SQL database was a child
	// repository. These small witnesses faithfully reconstruct that layout
	// without checking a release-specific Noms database into the test suite.
	writeFile(t, filepath.Join(beadsDir, "dolt", ".dolt", "config.json"), []byte("{}\n"))
	writeFile(t, filepath.Join(beadsDir, "dolt", ".dolt", "repo_state.json"), []byte("{\"head\":\"refs/heads/main\"}\n"))
	writeFile(t, filepath.Join(beadsDir, "dolt", "smoke", ".dolt", "config.json"), []byte("{}\n"))
	writeFile(t, filepath.Join(beadsDir, "dolt", "smoke", ".dolt", "repo_state.json"), []byte("{\"head\":\"refs/heads/main\"}\n"))
	writeFile(t, filepath.Join(beadsDir, "dolt", "config.yaml"), []byte("listener:\n  host: 127.0.0.1\n  port: 3307\n"))
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
