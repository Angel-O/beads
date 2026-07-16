//go:build cgo && !unix && !windows

package embeddeddolt

import (
	"errors"
	"os"
)

func migrationDirectoryIdentity(*os.File) (string, error) {
	return "", errors.New("embeddeddolt: durable filesystem identity is unsupported on this platform")
}
