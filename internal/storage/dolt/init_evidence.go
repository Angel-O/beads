package dolt

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/steveyegge/beads/internal/safefile"
)

const maxInitializationEvidenceEntries = 1024

type doltEvidenceAccess struct {
	openDirectory          func(string) (*os.File, error)
	beforeRepositoryMarker func(string) error
}

func defaultDoltEvidenceAccess() doltEvidenceAccess {
	return doltEvidenceAccess{openDirectory: safefile.OpenReadOnlyNoFollow}
}

func (access doltEvidenceAccess) directoryOpener() func(string) (*os.File, error) {
	if access.openDirectory != nil {
		return access.openDirectory
	}
	return safefile.OpenReadOnlyNoFollow
}

// HasLocalInitializationEvidence reports whether an otherwise unconfigured
// workspace contains a local Dolt repository marker. Only verified absence
// returns false: unreadable, malformed, oversized, or partial provider layouts
// fail closed so a caller cannot mistake damaged source state for a fresh
// workspace.
func HasLocalInitializationEvidence(beadsDir string) (bool, error) {
	return hasLocalInitializationEvidenceWithAccess(beadsDir, defaultDoltEvidenceAccess())
}

func hasLocalInitializationEvidenceWithAccess(
	beadsDir string,
	access doltEvidenceAccess,
) (exists bool, err error) {
	if beadsDir == "" {
		return false, errors.New("Dolt evidence requires a workspace directory")
	}
	workspace, present, err := openStableDoltEvidenceDirectory(beadsDir, "Dolt workspace root", access)
	if err != nil || !present {
		return false, err
	}
	defer workspace.finishBooleanResult(&exists, &err)

	beadsDir = workspace.path
	if exists, err = repositoryTreeAtWithAccess(filepath.Join(beadsDir, "dolt"), access); err != nil || exists {
		return exists, err
	}
	return embeddedRepositoriesAtWithAccess(filepath.Join(beadsDir, "embeddeddolt"), access)
}

func embeddedRepositoriesAtWithAccess(
	embeddedRoot string,
	access doltEvidenceAccess,
) (exists bool, err error) {
	root, present, err := openStableDoltEvidenceDirectory(embeddedRoot, "embedded Dolt root", access)
	if err != nil || !present {
		return false, err
	}
	defer root.finishBooleanResult(&exists, &err)

	entries, err := root.readBounded()
	if err != nil {
		return false, err
	}
	foundRepository := false
	for _, entry := range entries {
		path := filepath.Join(root.path, entry.Name())
		if entry.Name() == ".lock" {
			info, err := os.Lstat(path)
			if err != nil {
				return false, safePathError("inspect embedded Dolt lock", path, err)
			}
			if !info.Mode().IsRegular() {
				return false, fmt.Errorf("embedded Dolt lock is not a regular file: %q", path)
			}
			continue
		}
		if !entry.IsDir() {
			return false, fmt.Errorf("unexpected entry in embedded Dolt root: %q", path)
		}
		markerExists, err := repositoryMarkerAtWithAccess(path, access)
		if err != nil {
			return false, err
		}
		if !markerExists {
			return false, fmt.Errorf("embedded Dolt directory has no repository marker: %q", path)
		}
		foundRepository = true
	}
	if foundRepository {
		return true, nil
	}
	return false, fmt.Errorf("embedded Dolt root is present but contains no repositories: %q", root.path)
}

// repositoryTreeAtWithAccess recognizes both the legacy single repository at
// <root>/.dolt and current server data roots whose database repositories live
// at <root>/<database>/.dolt.
func repositoryTreeAtWithAccess(rootPath string, access doltEvidenceAccess) (exists bool, err error) {
	root, present, err := openStableDoltEvidenceDirectory(rootPath, "Dolt path", access)
	if err != nil || !present {
		return false, err
	}
	defer root.finishBooleanResult(&exists, &err)

	if exists, err = repositoryMarkerInDirectory(root, access); err != nil || exists {
		return exists, err
	}
	entries, err := root.readBounded()
	if err != nil {
		return false, err
	}
	foundRepository := false
	for _, entry := range entries {
		path := filepath.Join(root.path, entry.Name())
		switch entry.Name() {
		case ".doltcfg":
			if !entry.IsDir() {
				return false, fmt.Errorf("Dolt configuration path is not a directory: %q", path)
			}
			continue
		case ".lock":
			info, err := os.Lstat(path)
			if err != nil {
				return false, safePathError("inspect Dolt lock artifact", path, err)
			}
			if !info.Mode().IsRegular() {
				return false, fmt.Errorf("Dolt lock artifact is not a regular file: %q", path)
			}
			continue
		}
		if !entry.IsDir() {
			return false, fmt.Errorf("unexpected entry in Dolt data root: %q", path)
		}
		markerExists, err := repositoryMarkerAtWithAccess(path, access)
		if err != nil {
			return false, err
		}
		if !markerExists {
			return false, fmt.Errorf("Dolt database directory has no repository marker: %q", path)
		}
		foundRepository = true
	}
	if foundRepository {
		return true, nil
	}
	return false, fmt.Errorf("Dolt data root is present but contains no repositories: %q", root.path)
}

func repositoryMarkerAtWithAccess(path string, access doltEvidenceAccess) (exists bool, err error) {
	directory, present, err := openStableDoltEvidenceDirectory(path, "Dolt repository directory", access)
	if err != nil || !present {
		return false, err
	}
	defer directory.finishBooleanResult(&exists, &err)
	return repositoryMarkerInDirectory(directory, access)
}

func repositoryMarkerInDirectory(
	directory *stableDoltEvidenceDirectory,
	access doltEvidenceAccess,
) (exists bool, err error) {
	if access.beforeRepositoryMarker != nil {
		if err := access.beforeRepositoryMarker(directory.path); err != nil {
			return false, safePathError("prepare Dolt repository marker inspection", directory.path, err)
		}
	}
	markerPath := filepath.Join(directory.path, ".dolt")
	marker, present, err := openStableDoltEvidenceDirectory(markerPath, "Dolt repository marker", access)
	if err != nil || !present {
		return false, err
	}
	defer marker.finishBooleanResult(&exists, &err)
	return true, nil
}

type stableDoltEvidenceDirectory struct {
	path        string
	description string
	file        *os.File
	openedInfo  os.FileInfo
}

func openStableDoltEvidenceDirectory(
	path string,
	description string,
	access doltEvidenceAccess,
) (*stableDoltEvidenceDirectory, bool, error) {
	path = filepath.Clean(path)
	if err := safefile.ValidateMetadataPath(path); err != nil {
		return nil, false, safePathError("validate "+description, path, err)
	}
	namedInfo, err := os.Lstat(path)
	if os.IsNotExist(err) {
		if ancestorErr := safefile.ValidateMissingPathAncestors(path); ancestorErr != nil {
			return nil, false, fmt.Errorf("validate absent %s %q: %w", description, path, ancestorErr)
		}
		return nil, false, nil
	}
	if err != nil {
		return nil, false, safePathError("inspect "+description, path, err)
	}
	if !namedInfo.IsDir() {
		return nil, true, fmt.Errorf("%s is not a directory: %q", description, path)
	}
	file, err := access.directoryOpener()(path)
	if err != nil {
		return nil, true, safePathError("open "+description, path, err)
	}
	if file == nil {
		return nil, true, fmt.Errorf("open %s %q: opener returned no descriptor", description, path)
	}
	closeOnError := func(result error) (*stableDoltEvidenceDirectory, bool, error) {
		_ = file.Close()
		return nil, true, result
	}
	openedInfo, err := file.Stat()
	if err != nil {
		return closeOnError(safePathError("inspect opened "+description, path, err))
	}
	namedAtOpen, err := os.Lstat(path)
	if err != nil {
		return closeOnError(safePathError("reinspect opened "+description, path, err))
	}
	if !sameDoltEvidenceDirectory(namedInfo, openedInfo) || !sameDoltEvidenceDirectory(openedInfo, namedAtOpen) {
		return closeOnError(fmt.Errorf("%s changed while opening: %q", description, path))
	}
	return &stableDoltEvidenceDirectory{
		path:        path,
		description: description,
		file:        file,
		openedInfo:  openedInfo,
	}, true, nil
}

func (directory *stableDoltEvidenceDirectory) readBounded() ([]os.DirEntry, error) {
	entries, readErr := directory.file.ReadDir(maxInitializationEvidenceEntries + 1)
	if err := directory.validate("reading"); err != nil {
		return nil, err
	}
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return nil, safePathError("read "+directory.description, directory.path, readErr)
	}
	if len(entries) > maxInitializationEvidenceEntries {
		return nil, fmt.Errorf("Dolt directory %q exceeds the %d-entry evidence limit", directory.path, maxInitializationEvidenceEntries)
	}
	return entries, nil
}

func (directory *stableDoltEvidenceDirectory) validate(operation string) error {
	afterInfo, err := directory.file.Stat()
	if err != nil {
		return safePathError("reinspect opened "+directory.description, directory.path, err)
	}
	namedAfter, err := os.Lstat(directory.path)
	if err != nil {
		return safePathError("reinspect "+directory.description+" name", directory.path, err)
	}
	if !sameDoltEvidenceDirectory(directory.openedInfo, afterInfo) ||
		!sameDoltEvidenceDirectory(directory.openedInfo, namedAfter) {
		return fmt.Errorf("%s changed while %s: %q", directory.description, operation, directory.path)
	}
	return nil
}

func (directory *stableDoltEvidenceDirectory) finish(resultErr *error) {
	validationErr := directory.validate("inspected")
	closeErr := directory.file.Close()
	if *resultErr != nil {
		return
	}
	switch {
	case validationErr != nil:
		*resultErr = validationErr
	case closeErr != nil:
		*resultErr = safePathError("close "+directory.description, directory.path, closeErr)
	}
}

func (directory *stableDoltEvidenceDirectory) finishBooleanResult(exists *bool, resultErr *error) {
	directory.finish(resultErr)
	if *resultErr != nil {
		*exists = false
	}
}

func boundedReadDir(path string) ([]os.DirEntry, error) {
	return boundedReadDirWithOpener(path, safefile.OpenReadOnlyNoFollow)
}

func boundedReadDirWithOpener(path string, opener func(string) (*os.File, error)) (entries []os.DirEntry, err error) {
	access := doltEvidenceAccess{openDirectory: opener}
	directory, present, err := openStableDoltEvidenceDirectory(path, "Dolt path", access)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, safePathError("inspect Dolt directory", path, os.ErrNotExist)
	}
	defer func() {
		directory.finish(&err)
		if err != nil {
			entries = nil
		}
	}()
	return directory.readBounded()
}

func sameDoltEvidenceDirectory(before, after os.FileInfo) bool {
	return before != nil && after != nil && before.IsDir() && after.IsDir() &&
		os.SameFile(before, after) && before.Size() == after.Size() && before.ModTime().Equal(after.ModTime())
}

// safePathError quotes the caller-controlled path once and unwraps os.PathError
// so its unquoted copy of that path cannot inject terminal-control characters.
func safePathError(operation, path string, err error) error {
	var pathErr *os.PathError
	for errors.As(err, &pathErr) {
		err = pathErr.Err
		pathErr = nil
	}
	return fmt.Errorf("%s %q: %w", operation, path, err)
}
