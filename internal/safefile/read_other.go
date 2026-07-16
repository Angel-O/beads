//go:build !unix

package safefile

import (
	"errors"
	"io"
	"os"
)

func readRegularFile(path string, maxBytes int64) ([]byte, error) {
	named, err := os.Lstat(path)
	if err != nil || !named.Mode().IsRegular() || named.Size() < 0 || named.Size() > maxBytes {
		return nil, errors.New("control file must be a bounded regular file")
	}
	file, err := os.Open(path) // #nosec G304 -- identity is checked around the bounded read.
	if err != nil {
		return nil, err
	}
	defer file.Close() //nolint:errcheck // read result is authoritative
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(named, opened) {
		return nil, errors.New("control file changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil || int64(len(data)) > maxBytes {
		return nil, errors.New("control file could not be read safely")
	}
	after, err := os.Lstat(path)
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(opened, after) {
		return nil, errors.New("control file changed while reading")
	}
	return data, nil
}
