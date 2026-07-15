//go:build linux

package backendmigration

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/steveyegge/beads/internal/safefile"
	"github.com/steveyegge/beads/internal/workspaceidentity"
	"golang.org/x/sys/unix"
)

const metadataObservationOpenFlags = unix.O_PATH | unix.O_CLOEXEC | unix.O_NOFOLLOW

type metadataObservationAccess struct {
	open     func(string) (*os.File, error)
	statfs   func(string, *unix.Statfs_t) error
	readlink func(string) (string, error)
	lstat    func(string) (os.FileInfo, error)
	close    func(*os.File) error
}

var productionMetadataObservationAccess = metadataObservationAccess{
	open: func(path string) (*os.File, error) {
		fd, err := unix.Open(path, metadataObservationOpenFlags, 0)
		if err != nil {
			return nil, err
		}
		file := os.NewFile(uintptr(fd), path)
		if file != nil {
			return file, nil
		}
		primary := errors.New("could not wrap metadata observation descriptor")
		if closeErr := unix.Close(fd); closeErr != nil {
			return nil, errors.Join(primary, metadataObservationCleanup(closeErr))
		}
		return nil, primary
	},
	statfs:   unix.Statfs,
	readlink: os.Readlink,
	lstat:    os.Lstat,
	close:    func(file *os.File) error { return file.Close() },
}

func probeNativeLinux() (nativeLinux, wsl bool, err error) {
	var name unix.Utsname
	if err := unix.Uname(&name); err != nil {
		return false, false, err
	}
	release := utsnameString(name.Release[:])
	if release == "" {
		return false, false, errors.New("kernel release is empty")
	}
	return true, isWSLRelease(release), nil
}

func isWSLRelease(release string) bool {
	lower := strings.ToLower(release)
	return strings.Contains(lower, "microsoft") || strings.Contains(lower, "wsl")
}

func utsnameString(raw []byte) string {
	end := len(raw)
	for i, value := range raw {
		if value == 0 {
			end = i
			break
		}
	}
	return string(raw[:end])
}

func observeMetadataNoFollow(path string) (*safefile.MetadataObservation, error) {
	return observeMetadataNoFollowWith(path, productionMetadataObservationAccess)
}

func observeMetadataNoFollowWith(path string, access metadataObservationAccess) (observation *safefile.MetadataObservation, returnErr error) {
	if access.open == nil || access.statfs == nil || access.readlink == nil || access.lstat == nil || access.close == nil {
		return nil, errors.New("incomplete metadata observation access")
	}
	file, err := access.open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := access.close(file); closeErr != nil {
			observation = nil
			returnErr = errors.Join(returnErr, metadataObservationCleanup(closeErr))
		}
	}()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	// O_PATH|O_NOFOLLOW opens a final symlink itself. Reject its descriptor
	// before any canonical-path lookup can follow the swapped-in target.
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("metadata observation handle is a symbolic link")
	}
	var procFilesystem unix.Statfs_t
	if err := access.statfs("/proc/self/fd", &procFilesystem); err != nil {
		return nil, err
	}
	if uint64(procFilesystem.Type) != unix.PROC_SUPER_MAGIC { //nolint:unconvert // Type width varies across Linux architectures.
		return nil, errors.New("/proc/self/fd is not procfs")
	}
	canonical, err := access.readlink(fmt.Sprintf("/proc/self/fd/%d", file.Fd()))
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(canonical) {
		return nil, errors.New("metadata observation has no stable absolute pathname")
	}
	named, err := access.lstat(canonical)
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, named) {
		return nil, errors.New("canonical pathname no longer names the observed object")
	}
	linkCount, linkCountKnown := safefile.OpenedFileLinkCount(file, info)
	return &safefile.MetadataObservation{
		CanonicalPath:  filepath.Clean(canonical),
		Info:           info,
		CaseSensitive:  true,
		LinkCount:      linkCount,
		LinkCountKnown: linkCountKnown,
	}, nil
}

func metadataObservationCleanup(closeErr error) error {
	causes := []error{workspaceidentity.ErrCleanup, workspaceidentity.ErrUnverifiable}
	for _, safe := range []error{os.ErrClosed, os.ErrPermission, errors.ErrUnsupported} {
		if errors.Is(closeErr, safe) {
			causes = append(causes, safe)
		}
	}
	return errors.Join(causes...)
}
