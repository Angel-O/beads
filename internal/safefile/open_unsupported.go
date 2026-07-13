//go:build !aix && !android && !darwin && !dragonfly && !freebsd && !illumos && !ios && !linux && !netbsd && !openbsd && !solaris && !windows && !zos

package safefile

import (
	"errors"
	"os"
)

func openReadOnly(path string, noFollow bool) (*os.File, error) {
	if noFollow {
		return nil, errors.ErrUnsupported
	}
	// Preserve compatibility where the platform cannot provide the stronger
	// nonblocking/no-follow primitives. Strict callers always request noFollow
	// and fail closed above.
	return os.Open(path) // #nosec G304 -- compatibility read on unsupported OS
}
