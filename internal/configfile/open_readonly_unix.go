//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris || zos

package configfile

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// NOFOLLOW and NONBLOCK must be applied by the same open call: a pathname
// precheck can be replaced by a symlink or FIFO before a later blocking open.
const openReadOnlyConfigFlags = unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK

func openReadOnlyConfigFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, openReadOnlyConfigFlags, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("could not wrap config file descriptor")
	}
	return file, nil
}
