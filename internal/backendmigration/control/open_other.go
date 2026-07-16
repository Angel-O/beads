//go:build !unix

package control

import (
	"errors"
	"os"
)

func openControlFile(path string) (*os.File, error) {
	if named, err := os.Lstat(path); err == nil {
		if !named.Mode().IsRegular() || named.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("backend migration workspace control must be a regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 -- identity is checked below.
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() {
		_ = file.Close()
		return nil, errors.New("backend migration workspace control is not a regular file")
	}
	named, err := os.Lstat(path)
	if err != nil || !named.Mode().IsRegular() || named.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, named) {
		_ = file.Close()
		return nil, errors.New("backend migration workspace control changed while opening")
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}
