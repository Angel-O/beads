//go:build !windows && !linux

package config

import (
	"os"

	"golang.org/x/sys/unix"
)

// os.Open may follow a path swapped to a symlink on these platforms. The
// caller's descriptor identity check prevents chmod unless it still refers to
// the directory inspected with Lstat.
func openBeadsDirHandle(path string) (beadsDirHandle, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}
