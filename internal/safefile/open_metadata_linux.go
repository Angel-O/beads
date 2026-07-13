//go:build linux

package safefile

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func openMetadataNoFollow(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	file, err := wrapUnixFileDescriptor(fd, path, "metadata")
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		_ = file.Close()
		return nil, errors.New("metadata handle is a symbolic link")
	}
	return file, nil
}
