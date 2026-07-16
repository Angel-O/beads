//go:build windows

// Package noreplace publishes filesystem entries without overwriting an
// existing destination.
package noreplace

import "golang.org/x/sys/windows"

func Rename(oldPath, newPath string) error {
	oldPtr, err := windows.UTF16PtrFromString(oldPath)
	if err != nil {
		return err
	}
	newPtr, err := windows.UTF16PtrFromString(newPath)
	if err != nil {
		return err
	}
	// Omitting MOVEFILE_REPLACE_EXISTING is the no-replace guarantee.
	return windows.MoveFileEx(oldPtr, newPtr, windows.MOVEFILE_WRITE_THROUGH)
}
