package safefile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ValidateMissingPathAncestors verifies that an ENOENT result represents a
// genuinely absent leaf or future directory. Valid symlink ancestors that
// resolve to directories are allowed; dangling symlinks and non-directory
// ancestors fail closed.
func ValidateMissingPathAncestors(path string) error {
	if path == "" {
		return errors.New("missing-path validation requires a path")
	}
	if err := ValidateMetadataPath(path); err != nil {
		return err
	}
	parent := filepath.Dir(filepath.Clean(path))
	for {
		info, err := os.Lstat(parent)
		switch {
		case err == nil && info.Mode()&os.ModeSymlink != 0:
			targetInfo, statErr := os.Stat(parent)
			if statErr != nil {
				return safeMissingPathError("resolve missing-path parent", parent, statErr)
			}
			if !targetInfo.IsDir() {
				return fmt.Errorf("missing-path parent is not a directory: %q", parent)
			}
			return nil
		case err == nil && !info.IsDir():
			return fmt.Errorf("missing-path parent is not a directory: %q", parent)
		case err == nil:
			return nil
		case !errors.Is(err, os.ErrNotExist):
			return safeMissingPathError("inspect missing-path parent", parent, err)
		}

		next := filepath.Dir(parent)
		if next == parent {
			return fmt.Errorf("missing path has no existing directory ancestor: %q", path)
		}
		parent = next
	}
}

func safeMissingPathError(operation, path string, err error) error {
	var pathErr *os.PathError
	for errors.As(err, &pathErr) {
		err = pathErr.Err
		pathErr = nil
	}
	// This error describes an unsafe ancestor, not verified leaf absence. Do
	// not wrap ENOENT: callers must not be able to reclassify it as absence via
	// errors.Is(err, os.ErrNotExist).
	return fmt.Errorf("%s %q: %v", operation, path, err)
}
