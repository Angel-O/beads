//go:build cgo && unix

package embeddeddolt

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func migrationDirectoryIdentity(handle *os.File) (string, error) {
	info, err := handle.Stat()
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", errors.New("embeddeddolt: filesystem identity is unavailable")
	}
	return fmt.Sprintf("%x:%x", stat.Dev, stat.Ino), nil
}
