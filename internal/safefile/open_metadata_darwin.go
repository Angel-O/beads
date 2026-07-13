//go:build darwin || ios

package safefile

import (
	"os"

	"golang.org/x/sys/unix"
)

func openMetadataNoFollow(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_EVTONLY|unix.O_CLOEXEC|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	return wrapUnixFileDescriptor(fd, path, "metadata")
}
