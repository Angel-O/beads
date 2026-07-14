package beads

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

func TestParseWorktreePorcelainZPreservesNewlinePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stable\nsuffix")
	data := []byte("worktree " + path + "\x00HEAD abc123\x00branch refs/heads/main\x00\x00")
	worktrees, err := parseWorktreePorcelainZ(data)
	if err != nil || len(worktrees) != 1 || worktrees[0].Path != path {
		t.Fatalf("parsed worktrees=%#v err=%v, want newline path %q", worktrees, err, path)
	}
}

func TestParseWorktreePorcelainZRejectsUnboundedInput(t *testing.T) {
	t.Run("output bytes", func(t *testing.T) {
		output := make([]byte, maxGitOutputBytes+1)
		if _, err := parseWorktreePorcelainZ(output); !errors.Is(err, errGitOutputTooLarge) {
			t.Fatalf("oversized worktree output error = %v, want size limit", err)
		}
	})

	t.Run("record count", func(t *testing.T) {
		var output bytes.Buffer
		for index := 0; index <= maxGitWorktreeRecords; index++ {
			fmt.Fprintf(&output, "worktree /workspace/%d\x00\x00", index)
		}
		if _, err := parseWorktreePorcelainZ(output.Bytes()); !errors.Is(err, errGitWorktreeRecordLimit) {
			t.Fatalf("excess worktree records error = %v, want record limit", err)
		}
	})
}

func TestParseWorktreePorcelainZRequiresBlankRecordTerminator(t *testing.T) {
	data := []byte("worktree /workspace\x00HEAD abc123\x00branch refs/heads/main\x00")
	if _, err := parseWorktreePorcelainZ(data); err == nil {
		t.Fatal("single-NUL record terminator was accepted")
	}
}
