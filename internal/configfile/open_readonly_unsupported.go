//go:build !aix && !android && !darwin && !dragonfly && !freebsd && !illumos && !ios && !linux && !netbsd && !openbsd && !solaris && !windows && !zos

package configfile

import (
	"errors"
	"os"
)

func openReadOnlyConfigFile(string) (*os.File, error) {
	return nil, errors.ErrUnsupported
}
