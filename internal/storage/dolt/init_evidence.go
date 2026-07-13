package dolt

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const maxInitializationEvidenceEntries = 1024

// HasLocalInitializationEvidence reports whether an otherwise unconfigured
// workspace contains a local Dolt repository marker. Only verified absence
// returns false: unreadable, malformed, oversized, or partial provider layouts
// fail closed so a caller cannot mistake damaged source state for a fresh
// workspace.
func HasLocalInitializationEvidence(beadsDir string) (bool, error) {
	if beadsDir == "" {
		return false, errors.New("Dolt evidence requires a workspace directory")
	}
	rootInfo, err := os.Lstat(beadsDir)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, safePathError("inspect Dolt workspace root", beadsDir, err)
	}
	if !rootInfo.IsDir() {
		return false, fmt.Errorf("Dolt workspace root is not a directory: %q", beadsDir)
	}

	if exists, err := repositoryTreeAt(filepath.Join(beadsDir, "dolt")); err != nil || exists {
		return exists, err
	}

	embeddedRoot := filepath.Join(beadsDir, "embeddeddolt")
	embeddedInfo, err := os.Lstat(embeddedRoot)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, safePathError("inspect embedded Dolt root", embeddedRoot, err)
	}
	if !embeddedInfo.IsDir() {
		return false, fmt.Errorf("embedded Dolt root is not a directory: %q", embeddedRoot)
	}
	entries, err := boundedReadDir(embeddedRoot)
	if err != nil {
		return false, err
	}
	foundRepository := false
	for _, entry := range entries {
		path := filepath.Join(embeddedRoot, entry.Name())
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
		exists, err := repositoryMarkerAt(path)
		if err != nil {
			return false, err
		}
		if !exists {
			return false, fmt.Errorf("embedded Dolt directory has no repository marker: %q", path)
		}
		foundRepository = true
	}
	if foundRepository {
		return true, nil
	}
	return false, fmt.Errorf("embedded Dolt root is present but contains no repositories: %q", embeddedRoot)
}

// repositoryTreeAt recognizes both the legacy single repository at
// <root>/.dolt and current server data roots whose database repositories live
// at <root>/<database>/.dolt.
func repositoryTreeAt(root string) (bool, error) {
	info, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, safePathError("inspect Dolt directory", root, err)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("Dolt path is not a directory: %q", root)
	}
	if exists, err := repositoryMarkerAt(root); err != nil || exists {
		return exists, err
	}

	entries, err := boundedReadDir(root)
	if err != nil {
		return false, err
	}
	foundRepository := false
	for _, entry := range entries {
		path := filepath.Join(root, entry.Name())
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
		exists, err := repositoryMarkerAt(path)
		if err != nil {
			return false, err
		}
		if !exists {
			return false, fmt.Errorf("Dolt database directory has no repository marker: %q", path)
		}
		foundRepository = true
	}
	if foundRepository {
		return true, nil
	}
	return false, fmt.Errorf("Dolt data root is present but contains no repositories: %q", root)
}

func repositoryMarkerAt(path string) (bool, error) {
	marker := filepath.Join(path, ".dolt")
	info, err := os.Lstat(marker)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, safePathError("inspect Dolt repository marker", marker, err)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("Dolt repository marker is not a directory: %q", marker)
	}
	return true, nil
}

func boundedReadDir(path string) ([]os.DirEntry, error) {
	dir, err := os.Open(path) // #nosec G304 -- fixed provider directory under selected workspace
	if err != nil {
		return nil, safePathError("open Dolt directory", path, err)
	}
	defer dir.Close() //nolint:errcheck // read-only evidence descriptor
	entries, err := dir.ReadDir(maxInitializationEvidenceEntries + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, safePathError("read Dolt directory", path, err)
	}
	if len(entries) > maxInitializationEvidenceEntries {
		return nil, fmt.Errorf("Dolt directory %q exceeds the %d-entry evidence limit", path, maxInitializationEvidenceEntries)
	}
	return entries, nil
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
