//go:build (linux && !android) || (darwin && !ios)

package beads

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPreferStableBranchWorktreeBeadsDirStrictPreservesNewlinePath(t *testing.T) {
	root := t.TempDir()
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	stableRoot := filepath.Join(root, "stable\nsuffix")
	if err := os.Mkdir(stableRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	runGit := func(dir string, args ...string) {
		t.Helper()
		commandArgs := append([]string{"-C", dir}, args...)
		if output, err := exec.Command("git", commandArgs...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, output)
		}
	}
	runGit(stableRoot, "init")
	runGit(stableRoot, "config", "user.email", "strict-redirect@example.com")
	runGit(stableRoot, "config", "user.name", "Strict Redirect Test")
	runGit(stableRoot, "config", "core.hooksPath", ".git/hooks")
	if err := os.WriteFile(filepath.Join(stableRoot, "README"), []byte("seed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(stableRoot, "add", "README")
	runGit(stableRoot, "commit", "-m", "seed")

	detachedRoot := filepath.Join(root, "refs", "commits", "snapshot")
	runGit(stableRoot, "worktree", "add", "--detach", detachedRoot, "HEAD")
	for _, path := range []string{filepath.Join(stableRoot, ".beads"), filepath.Join(detachedRoot, ".beads")} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	decoy := filepath.Join(root, "stable", ".beads")
	if err := os.MkdirAll(decoy, 0o700); err != nil {
		t.Fatal(err)
	}
	caseDecoyRoot := filepath.Join(root, "STABLE\nsuffix")
	caseDecoy := ""
	if err := os.Mkdir(caseDecoyRoot, 0o700); err == nil {
		caseDecoy = filepath.Join(caseDecoyRoot, ".beads")
		if err := os.Mkdir(caseDecoy, 0o700); err != nil {
			t.Fatal(err)
		}
	} else if !os.IsExist(err) {
		t.Fatal(err)
	}

	got := preferStableBranchWorktreeBeadsDirStrict(filepath.Join(detachedRoot, ".beads"))
	want := filepath.Join(stableRoot, ".beads")
	if got != want {
		t.Fatalf("strict stable worktree = %q, want exact Git candidate %q (not newline decoy %q or case decoy %q)", got, want, decoy, caseDecoy)
	}
}
