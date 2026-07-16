//go:build unix

package safefile

import (
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

func readRegularFile(path string, maxBytes int64) ([]byte, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("open no-follow control file: %w", err)
	}
	file := os.NewFile(uintptr(fd), "workspace-control-file")
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("open no-follow control file")
	}
	defer file.Close() //nolint:errcheck // read result is authoritative

	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return nil, fmt.Errorf("inspect control file: %w", err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1 || stat.Size < 0 || stat.Size > maxBytes {
		return nil, errors.New("control file must be a bounded single-link regular file")
	}
	opened, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect opened control file: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil || int64(len(data)) > maxBytes {
		return nil, errors.New("control file could not be read safely")
	}
	named, err := os.Lstat(path)
	if err != nil || !named.Mode().IsRegular() || !os.SameFile(opened, named) {
		return nil, errors.New("control file changed while reading")
	}
	return data, nil
}
