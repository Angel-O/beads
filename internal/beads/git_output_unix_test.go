//go:build (darwin && !ios) || freebsd || (linux && !android)

package beads

import (
	"errors"
	"os/exec"
	"testing"
	"time"
)

func TestReadGitOutputBoundedRejectsOversizedStdout(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "printf 'xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx'")
	output, err := readGitOutputBounded(cmd, 32)
	if output != nil || !errors.Is(err, errGitOutputTooLarge) {
		t.Fatalf("bounded command output = %q, err=%v, want size-limit rejection", output, err)
	}
}

func TestReadGitOutputBoundedClosesDescendantStdout(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "sleep 1 &")
	started := time.Now()
	output, err := readGitOutputBoundedWithWaitDelay(cmd, 32, 25*time.Millisecond)
	if output != nil || !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("descendant-held output = %q, err=%v, want WaitDelay", output, err)
	}
	if elapsed := time.Since(started); elapsed >= 750*time.Millisecond {
		t.Fatalf("descendant-held output returned after %v, want bounded wait", elapsed)
	}
}
