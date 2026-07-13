//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris || zos

package safefile

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

const readOnlyNoFollowFlags = unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NONBLOCK | unix.O_NOFOLLOW

func openReadOnlyNoFollow(path string) (*os.File, error) {
	fd, err := unix.Open(path, readOnlyNoFollowFlags, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("could not wrap read-only file descriptor")
	}
	return file, nil
}
