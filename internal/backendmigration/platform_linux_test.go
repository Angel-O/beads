//go:build linux

package backendmigration

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/workspaceidentity"
	"golang.org/x/sys/unix"
)

func TestLinuxPlatformProbeRecognizesWSLRelease(t *testing.T) {
	for _, test := range []struct {
		release string
		wsl     bool
	}{
		{release: "6.8.0-generic"},
		{release: "4.4.0-19041-Microsoft", wsl: true},
		{release: "5.15.153.1-microsoft-standard-WSL2", wsl: true},
	} {
		if got := isWSLRelease(test.release); got != test.wsl {
			t.Fatalf("isWSLRelease(%q)=%v, want %v", test.release, got, test.wsl)
		}
	}
	if got := utsnameString([]byte{'a', 'b', 0, 'c'}); got != "ab" {
		t.Fatalf("utsnameString=%q, want ab", got)
	}
	if native, _, err := probeNativeLinux(); err != nil || !native {
		t.Fatalf("native Linux probe=%v, %v", native, err)
	}
}

func TestMetadataObservationUsesPathOnlyNoFollowDescriptor(t *testing.T) {
	if metadataObservationOpenFlags != unix.O_PATH|unix.O_CLOEXEC|unix.O_NOFOLLOW {
		t.Fatalf("metadata observation flags=%#x", metadataObservationOpenFlags)
	}
	path := filepath.Join(t.TempDir(), "redirect")
	if err := os.WriteFile(path, []byte("private redirect contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := productionMetadataObservationAccess.open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close() //nolint:errcheck // test descriptor
	flags, err := unix.FcntlInt(file.Fd(), unix.F_GETFL, 0)
	if err != nil {
		t.Fatal(err)
	}
	if flags&unix.O_PATH == 0 {
		t.Fatalf("opened descriptor flags=%#x, want O_PATH", flags)
	}
	if _, err := file.Read(make([]byte, 1)); err == nil {
		t.Fatal("metadata observation descriptor read redirect contents")
	}
}

func TestMetadataObservationMarksCloseFailureAndPreservesInspectionSentinel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	closeFailure := errors.New("private close text")

	t.Run("cleanup alone", func(t *testing.T) {
		access := productionMetadataObservationAccess
		access.close = func(file *os.File) error {
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			return closeFailure
		}
		observation, err := observeMetadataNoFollowWith(path, access)
		if observation != nil || !errors.Is(err, workspaceidentity.ErrCleanup) || !errors.Is(err, workspaceidentity.ErrUnverifiable) {
			t.Fatalf("observation=%#v err=%v, want cleanup and unverifiable", observation, err)
		}
		if errors.Is(err, closeFailure) {
			t.Fatal("raw close error identity escaped the cleanup marker")
		}
		refusalErr := classifyObservationError(&shapeObservationError{reason: ReasonWorkspace, cause: err})
		requireRefusal(t, SourceShapeCandidate{}, refusalErr, CodeWorkspaceUnverifiable, ReasonCleanup)
		if !errors.Is(refusalErr, workspaceidentity.ErrCleanup) || errors.Unwrap(refusalErr) != nil {
			t.Fatalf("cleanup refusal markers/chain = %v / %v", refusalErr, errors.Unwrap(refusalErr))
		}
	})

	t.Run("inspection plus cleanup", func(t *testing.T) {
		access := productionMetadataObservationAccess
		access.readlink = func(string) (string, error) { return "", os.ErrPermission }
		access.close = func(file *os.File) error {
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			return closeFailure
		}
		observation, err := observeMetadataNoFollowWith(path, access)
		if observation != nil || !errors.Is(err, workspaceidentity.ErrCleanup) || !errors.Is(err, os.ErrPermission) {
			t.Fatalf("observation=%#v err=%v, want cleanup plus inspection permission", observation, err)
		}
	})
}

func TestMetadataObservationRejectsOpenedSymlinkBeforeCanonicalLookup(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "metadata.json")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	access := productionMetadataObservationAccess
	canonicalLookups := 0
	access.readlink = func(string) (string, error) {
		canonicalLookups++
		return target, nil
	}
	observation, err := observeMetadataNoFollowWith(link, access)
	if observation != nil || err == nil {
		t.Fatalf("symlink observation=%#v err=%v, want refusal", observation, err)
	}
	if canonicalLookups != 0 {
		t.Fatalf("canonical lookups=%d, opened symlink target may have been followed", canonicalLookups)
	}
}

func TestMetadataObservationRejectsCanonicalPathSwappedToSymlink(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "metadata.json")
	moved := filepath.Join(directory, "metadata-moved.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}

	access := productionMetadataObservationAccess
	readlink := access.readlink
	access.readlink = func(descriptorPath string) (string, error) {
		canonical, err := readlink(descriptorPath)
		if err != nil {
			return "", err
		}
		if err := os.Rename(path, moved); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(moved, path); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		return canonical, nil
	}

	observation, err := observeMetadataNoFollowWith(path, access)
	if observation != nil || err == nil {
		t.Fatalf("canonical-path symlink replacement observation=%#v err=%v, target was followed", observation, err)
	}
}
