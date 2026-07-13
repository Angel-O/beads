//go:build !aix && !android && !darwin && !dragonfly && !freebsd && !illumos && !ios && !linux && !netbsd && !openbsd && !solaris && !windows && !zos

package safefile

import (
	"errors"
	"os"
)

func openReadOnlyNoFollow(string) (*os.File, error) {
	return nil, errors.ErrUnsupported
}
