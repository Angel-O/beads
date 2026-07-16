//go:build !windows

package atomicfile

import "os"

func syncDirectory(path string) error {
	directory, err := os.Open(path) // #nosec G304 -- callers select a directory to fsync; no file contents are read.
	if err != nil {
		return err
	}
	defer directory.Close() //nolint:errcheck // Sync is the durability result.
	return directory.Sync()
}
