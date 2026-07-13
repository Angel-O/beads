//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris || zos

package safefile

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

const (
	readOnlyOpenFlags = unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NONBLOCK
	noFollowOpenFlag  = unix.O_NOFOLLOW
)

func openReadOnly(path string, noFollow bool) (*os.File, error) {
	flags := readOnlyOpenFlags
	if noFollow {
		flags |= noFollowOpenFlag
	}
	fd, err := unix.Open(path, flags, 0)
	if err != nil {
		return nil, err
	}
	return wrapUnixFileDescriptor(fd, path, "read-only")
}

func wrapUnixFileDescriptor(fd int, path, kind string) (*os.File, error) {
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("could not wrap " + kind + " file descriptor")
	}
	return file, nil
}
