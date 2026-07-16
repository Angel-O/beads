//go:build unix

package sqlite

import (
	"os"
	"syscall"
)

func regularSingleLink(info os.FileInfo) bool {
	if info == nil || !info.Mode().IsRegular() {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Nlink == 1
}
