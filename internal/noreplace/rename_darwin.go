//go:build darwin

// Package noreplace publishes filesystem entries without overwriting an
// existing destination.
package noreplace

import "golang.org/x/sys/unix"

func Rename(oldPath, newPath string) error {
	return unix.RenameatxNp(unix.AT_FDCWD, oldPath, unix.AT_FDCWD, newPath, unix.RENAME_EXCL)
}
