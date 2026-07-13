//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris || zos

package beads

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/safefile"
	"golang.org/x/sys/unix"
)

func TestRedirectReadersDoNotBlockOnReplacementSpecialFiles(t *testing.T) {
	for _, replacement := range []struct {
		name    string
		symlink bool
	}{
		{name: "fifo"},
		{name: "symlink to fifo", symlink: true},
	} {
		t.Run(replacement.name, func(t *testing.T) {
			var symlinkTarget string
			if replacement.symlink {
				symlinkTarget = filepath.Join(t.TempDir(), "target-fifo")
				if err := unix.Mkfifo(symlinkTarget, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			for _, strict := range []bool{false, true} {
				mode := "lenient"
				if strict {
					mode = "strict"
				}
				t.Run(mode, func(t *testing.T) {
					source := t.TempDir()
					target := t.TempDir()
					path := filepath.Join(source, RedirectFileName)
					if err := os.WriteFile(path, []byte(target+"\n"), 0o600); err != nil {
						t.Fatal(err)
					}
					var replacementErr error
					openerCalled := false
					opener := func(string) (*os.File, error) {
						openerCalled = true
						if err := os.Remove(path); err != nil {
							replacementErr = err
							return nil, err
						}
						if replacement.symlink {
							if err := os.Symlink(symlinkTarget, path); err != nil {
								replacementErr = err
								return nil, err
							}
						} else if err := unix.Mkfifo(path, 0o600); err != nil {
							replacementErr = err
							return nil, err
						}
						if strict {
							return safefile.OpenReadOnlyNoFollow(path)
						}
						return safefile.OpenReadOnly(path)
					}
					result := make(chan struct {
						path string
						err  error
					}, 1)
					go func() {
						if strict {
							got, err := followRedirectStrictWithOpener(source, opener)
							result <- struct {
								path string
								err  error
							}{path: got, err: err}
							return
						}
						result <- struct {
							path string
							err  error
						}{path: followRedirectWithOpener(source, opener)}
					}()
					select {
					case got := <-result:
						if !openerCalled || replacementErr != nil {
							t.Fatalf("replacement setup called=%v err=%v", openerCalled, replacementErr)
						}
						if strict && (got.err == nil || got.path != "") {
							t.Fatalf("strict reader got path=%q err=%v, want error", got.path, got.err)
						}
						if !strict && (got.err != nil || got.path != source) {
							t.Fatalf("lenient reader got path=%q err=%v, want source %q", got.path, got.err, source)
						}
					case <-time.After(2 * time.Second):
						t.Fatal("redirect reader blocked on replacement special file")
					}
				})
			}
		})
	}
}

func TestFollowRedirectStrictRejectsFIFOChainMarker(t *testing.T) {
	source := t.TempDir()
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, RedirectFileName), []byte(target+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(filepath.Join(target, RedirectFileName), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := FollowRedirectStrict(source); err == nil {
		t.Fatal("FIFO redirect chain marker was accepted")
	}
}
