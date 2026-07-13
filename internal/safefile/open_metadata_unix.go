//go:build aix || dragonfly || freebsd || illumos || netbsd || openbsd || solaris || zos

package safefile

import (
	"errors"
	"os"
)

func openMetadataNoFollow(string) (*os.File, error) {
	return nil, errors.ErrUnsupported
}
