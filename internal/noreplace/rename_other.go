//go:build !linux && !darwin && !windows

// Package noreplace publishes filesystem entries without overwriting an
// existing destination.
package noreplace

import "errors"

func Rename(string, string) error {
	return errors.New("atomic no-replace rename is unsupported on this platform")
}
