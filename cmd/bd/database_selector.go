package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/steveyegge/beads/internal/safefile"
)

// databaseMetadataObserver must bind canonical path and object type to one
// metadata-only, final-component-no-follow observation.
type databaseMetadataObserver func(string) (*safefile.MetadataObservation, error)

type resolvedDatabasePath struct {
	path     string
	exists   bool
	observed *safefile.MetadataObservation
}

func validateDatabaseSelectorPath(path string) error {
	_, err := validatedDatabaseSelectorPath(path)
	return err
}

// validatedDatabaseSelectorPath resolves a selector and validates the resolved
// object, or the resolved parent of an allowed missing leaf, through one stable
// metadata observation. The result is a point-in-time fail-fast check only.
// Any operation that subsequently opens or creates the selector must repeat
// its authoritative checks and hold the required lifetime fence (bd-3u1fs).
func validatedDatabaseSelectorPath(path string) (string, error) {
	return validatedDatabaseSelectorPathWithObserver(path, safefile.ObserveMetadataNoFollow)
}

func validatedDatabaseSelectorPathWithObserver(path string, observer databaseMetadataObserver) (string, error) {
	if path == "" {
		return "", errors.New("database selector is empty")
	}
	resolved, err := resolveCanonicalDatabasePathWithObserver(path, observer)
	if err != nil {
		return "", err
	}
	if resolved.exists {
		info := resolved.observed.Info
		if !info.IsDir() && !info.Mode().IsRegular() {
			return "", fmt.Errorf("database selector is not a directory or regular file: %q", resolved.path)
		}
		if info.Mode().IsRegular() {
			if err := validateRegularDatabaseFileLinkCount("database selector", resolved.path, resolved.observed); err != nil {
				return "", err
			}
		}
		return resolved.path, nil
	}
	if !resolved.observed.Info.IsDir() {
		return "", fmt.Errorf("database selector parent is not a directory: %q", filepath.Dir(resolved.path))
	}
	return resolved.path, nil
}

func validateRegularDatabaseFileLinkCount(description, path string, observation *safefile.MetadataObservation) error {
	if observation == nil || observation.Info == nil || !observation.Info.Mode().IsRegular() {
		return fmt.Errorf("%s regular-file observation is incomplete: %q", description, path)
	}
	if !observation.LinkCountKnown {
		return fmt.Errorf("%s hard-link count is unavailable: %q", description, path)
	}
	if observation.LinkCount != 1 {
		return fmt.Errorf("%s has %d hard links; exactly one is required: %q", description, observation.LinkCount, path)
	}
	return nil
}

// validateDatabaseWorkspaceDirectory performs point-in-time fail-fast
// validation. A consumer that relies on the directory for a provider effect
// must repeat the authoritative check and hold the lifetime fence in bd-3u1fs.
func validateDatabaseWorkspaceDirectory(path string) error {
	resolved, err := resolveCanonicalDatabasePath(path)
	if err != nil {
		return err
	}
	if !resolved.exists {
		return fmt.Errorf("workspace directory does not exist: %q", resolved.path)
	}
	if !resolved.observed.Info.IsDir() {
		return fmt.Errorf("workspace is not a directory: %q", resolved.path)
	}
	return nil
}

func databasePathRelativeToWorkspace(path, beadsDir string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(beadsDir, path)
}

// databasePathEqualOrDescendant canonicalizes both paths without global case
// folding before comparing them. Canonicalization and containment are
// point-in-time observations; a consuming provider operation must repeat them
// under the lifetime fence tracked by bd-3u1fs.
func databasePathEqualOrDescendant(path, root string) (bool, error) {
	if path == "" || root == "" {
		return false, errors.New("database containment path is empty")
	}
	canonicalPath, err := resolveCanonicalDatabasePath(path)
	if err != nil {
		return false, err
	}
	canonicalRoot, err := resolveCanonicalDatabasePath(root)
	if err != nil {
		return false, err
	}
	if canonicalDatabasePathEqualOrDescendant(canonicalPath.path, canonicalRoot.path) {
		return true, nil
	}
	return equivalentCaseInsensitiveMissingDatabaseLeaf(canonicalPath, canonicalRoot), nil
}

func databasePathEqual(left, right string) (bool, error) {
	if left == "" || right == "" {
		return false, errors.New("database equality path is empty")
	}
	canonicalLeft, err := resolveCanonicalDatabasePath(left)
	if err != nil {
		return false, err
	}
	canonicalRight, err := resolveCanonicalDatabasePath(right)
	if err != nil {
		return false, err
	}
	return canonicalLeft.path == canonicalRight.path ||
		equivalentCaseInsensitiveMissingDatabaseLeaf(canonicalLeft, canonicalRight), nil
}

// canonicalDatabasePathEqualOrDescendant compares already-canonical absolute
// paths component-exactly. In particular it must not use filepath.Rel, whose
// Windows implementation case-folds even inside case-sensitive directories.
func canonicalDatabasePathEqualOrDescendant(path, root string) bool {
	path = filepath.Clean(path)
	root = filepath.Clean(root)
	if path == root {
		return true
	}
	separator := string(filepath.Separator)
	if strings.HasSuffix(root, separator) {
		return strings.HasPrefix(path, root)
	}
	return strings.HasPrefix(path, root+separator)
}

func equivalentCaseInsensitiveMissingDatabaseLeaf(path, root *resolvedDatabasePath) bool {
	if path.exists || root.exists || filepath.Dir(path.path) != filepath.Dir(root.path) {
		return false
	}
	pathObservation := path.observed
	rootObservation := root.observed
	if !pathObservation.CaseSensitivityKnown || pathObservation.CaseSensitive ||
		!rootObservation.CaseSensitivityKnown || rootObservation.CaseSensitive {
		return false
	}
	return equalASCIIFold(filepath.Base(path.path), filepath.Base(root.path))
}

func equalASCIIFold(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range len(left) {
		leftByte := left[index]
		rightByte := right[index]
		if 'A' <= leftByte && leftByte <= 'Z' {
			leftByte += 'a' - 'A'
		}
		if 'A' <= rightByte && rightByte <= 'Z' {
			rightByte += 'a' - 'A'
		}
		if leftByte != rightByte {
			return false
		}
	}
	return true
}

func absoluteCleanDatabasePath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", databasePathOperationError("compute absolute database path", path, err)
	}
	return filepath.Clean(abs), nil
}

// canonicalDatabasePath returns the canonical stored-case path for an existing
// object or for one missing final component whose parent exists. Resolution
// failures are never replaced with lexical paths.
func canonicalDatabasePath(path string) (string, error) {
	resolved, err := resolveCanonicalDatabasePath(path)
	if err != nil {
		return "", err
	}
	return resolved.path, nil
}

func resolveCanonicalDatabasePath(path string) (*resolvedDatabasePath, error) {
	return resolveCanonicalDatabasePathWithObserver(path, safefile.ObserveMetadataNoFollow)
}

func resolveCanonicalDatabasePathWithObserver(path string, observer databaseMetadataObserver) (*resolvedDatabasePath, error) {
	if err := safefile.ValidateMetadataPath(path); err != nil {
		return nil, databasePathOperationError("validate database path", path, err)
	}
	abs, err := absoluteCleanDatabasePath(path)
	if err != nil {
		return nil, err
	}
	if runtime.GOOS == "windows" {
		return resolveCanonicalWindowsDatabasePath(abs, observer)
	}

	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		observation, err := observeCanonicalDatabasePath(filepath.Clean(resolved), observer)
		if err != nil {
			return nil, err
		}
		return &resolvedDatabasePath{path: observation.CanonicalPath, exists: true, observed: observation}, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, databasePathOperationError("resolve database path", abs, err)
	}
	if _, namedErr := os.Lstat(abs); namedErr == nil {
		return nil, databasePathOperationError("resolve database path", abs, err)
	} else if !errors.Is(namedErr, os.ErrNotExist) {
		return nil, databasePathOperationError("inspect database path", abs, namedErr)
	}
	parent := filepath.Dir(abs)
	resolvedParent, parentErr := filepath.EvalSymlinks(parent)
	if parentErr != nil {
		return nil, databasePathOperationError("resolve database path parent", parent, parentErr)
	}
	return resolvedMissingDatabaseLeaf(abs, filepath.Clean(resolvedParent), observer)
}

func resolveCanonicalWindowsDatabasePath(abs string, observer databaseMetadataObserver) (*resolvedDatabasePath, error) {
	observation, err := observeCanonicalDatabasePath(abs, observer)
	if err == nil {
		return &resolvedDatabasePath{path: observation.CanonicalPath, exists: true, observed: observation}, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if _, namedErr := os.Lstat(abs); namedErr == nil {
		return nil, err
	} else if !errors.Is(namedErr, os.ErrNotExist) {
		return nil, databasePathOperationError("inspect database path", abs, namedErr)
	}
	parent := filepath.Dir(abs)
	if err := safefile.ValidateMetadataPath(parent); err != nil {
		return nil, databasePathOperationError("validate database path parent", parent, err)
	}
	// A reparse point in the parent is an ancestor of the missing selector leaf,
	// so resolve it deliberately. resolvedMissingDatabaseLeaf revalidates and
	// observes the resolved parent without following its final component.
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return nil, databasePathOperationError("resolve database path parent", parent, err)
	}
	return resolvedMissingDatabaseLeaf(abs, resolvedParent, observer)
}

func resolvedMissingDatabaseLeaf(original, parent string, observer databaseMetadataObserver) (*resolvedDatabasePath, error) {
	parentObservation, err := observeCanonicalDatabasePath(parent, observer)
	if err != nil {
		return nil, err
	}
	resolution := &resolvedDatabasePath{
		path:     filepath.Join(parentObservation.CanonicalPath, filepath.Base(original)),
		exists:   false,
		observed: parentObservation,
	}
	if !parentObservation.Info.IsDir() {
		return resolution, nil
	}
	leafObservation, err := observeCanonicalDatabasePath(resolution.path, observer)
	if err == nil {
		return &resolvedDatabasePath{
			path:     leafObservation.CanonicalPath,
			exists:   true,
			observed: leafObservation,
		}, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return resolution, nil
}

func observeCanonicalDatabasePath(path string, observer databaseMetadataObserver) (*safefile.MetadataObservation, error) {
	if err := safefile.ValidateMetadataPath(path); err != nil {
		return nil, databasePathOperationError("validate resolved database path", path, err)
	}
	observation, err := observer(path)
	if err != nil {
		return nil, databasePathOperationError("observe database path metadata", path, err)
	}
	if observation == nil || observation.Info == nil || observation.CanonicalPath == "" {
		return nil, fmt.Errorf("observe database path metadata %q: incomplete observation", path)
	}
	return observation, nil
}

func databasePathOperationError(operation, path string, err error) error {
	var pathErr *os.PathError
	for errors.As(err, &pathErr) {
		err = pathErr.Err
		pathErr = nil
	}
	return fmt.Errorf("%s %q: %w", operation, path, err)
}
