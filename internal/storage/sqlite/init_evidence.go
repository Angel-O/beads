package sqlite

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	sqliteHeader       = "SQLite format 3\x00"
	sqliteHeaderBytes  = 100
	defaultSQLitePath  = "beads.db"
	maxSQLitePageBytes = 65536
)

// HasLocalInitializationEvidence reports whether an otherwise unconfigured
// workspace contains the canonical legacy SQLite database or the explicitly
// configured SQLite path. Metadata loss makes arbitrary external or nested
// custom paths undiscoverable, so callers must pass a known configuredPath
// rather than asking this probe to scan unrelated workspace files.
//
// Only verified absence returns false. Damaged main files, non-regular paths,
// and orphaned SQLite sidecars fail closed.
func HasLocalInitializationEvidence(beadsDir, configuredPath string) (bool, error) {
	if beadsDir == "" {
		return false, errors.New("SQLite evidence requires a workspace directory")
	}
	if err := validateSQLiteWorkspaceRoot(beadsDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}

	paths := []string{filepath.Join(beadsDir, defaultSQLitePath)}
	if configuredPath != "" {
		path := configuredPath
		if !filepath.IsAbs(path) {
			path = filepath.Join(beadsDir, path)
		}
		if filepath.Clean(path) != filepath.Clean(paths[0]) {
			paths = append(paths, path)
		}
	}
	for _, path := range paths {
		valid, present, err := sqliteDatabaseAt(path)
		if err != nil {
			return false, err
		}
		if present {
			if !valid {
				return false, fmt.Errorf("SQLite database has an invalid header: %q", path)
			}
			return true, nil
		}
		if sidecar, exists, err := firstSQLiteSidecar(path); err != nil {
			return false, err
		} else if exists {
			return false, fmt.Errorf("SQLite sidecar exists without its main database: %q", sidecar)
		}
	}
	return false, nil
}

func validateSQLiteWorkspaceRoot(beadsDir string) error {
	info, err := os.Lstat(beadsDir)
	if err != nil {
		return safePathError("inspect SQLite workspace root", beadsDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("SQLite workspace root is not a directory: %q", beadsDir)
	}
	return nil
}

func sqliteDatabaseAt(path string) (valid, present bool, err error) {
	namedInfo, err := os.Lstat(path)
	if os.IsNotExist(err) {
		if err := validateMissingSQLitePath(path); err != nil {
			return false, false, err
		}
		return false, false, nil
	}
	if err != nil {
		return false, false, safePathError("inspect SQLite path", path, err)
	}
	if !namedInfo.Mode().IsRegular() {
		return false, true, fmt.Errorf("SQLite path is not a regular file: %q", path)
	}

	file, err := os.Open(path) // #nosec G304 -- caller-selected provider evidence path
	if err != nil {
		return false, true, safePathError("open SQLite path", path, err)
	}
	defer file.Close() //nolint:errcheck // read-only evidence descriptor
	openedInfo, err := file.Stat()
	if err != nil {
		return false, true, safePathError("inspect opened SQLite path", path, err)
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(namedInfo, openedInfo) {
		return false, true, fmt.Errorf("SQLite path changed while opening: %q", path)
	}

	header := make([]byte, sqliteHeaderBytes)
	if _, err := io.ReadFull(file, header); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return false, true, nil
		}
		return false, true, safePathError("read SQLite header", path, err)
	}
	afterInfo, err := file.Stat()
	if err != nil {
		return false, true, safePathError("reinspect SQLite path", path, err)
	}
	namedAfter, err := os.Lstat(path)
	if err != nil {
		return false, true, safePathError("reinspect SQLite name", path, err)
	}
	if !os.SameFile(openedInfo, afterInfo) || !os.SameFile(openedInfo, namedAfter) ||
		openedInfo.Size() != afterInfo.Size() || !openedInfo.ModTime().Equal(afterInfo.ModTime()) {
		return false, true, fmt.Errorf("SQLite path changed while inspected: %q", path)
	}
	return validSQLiteHeader(header, afterInfo.Size()), true, nil
}

// validateMissingSQLitePath distinguishes an absent leaf or future directory
// from a path hidden behind a dangling symlink or non-directory ancestor.
func validateMissingSQLitePath(path string) error {
	parent := filepath.Dir(filepath.Clean(path))
	for {
		info, err := os.Lstat(parent)
		switch {
		case err == nil && info.Mode()&os.ModeSymlink != 0:
			targetInfo, statErr := os.Stat(parent)
			if statErr != nil {
				return safePathError("resolve SQLite path parent", parent, statErr)
			}
			if !targetInfo.IsDir() {
				return fmt.Errorf("SQLite path parent is not a directory: %q", parent)
			}
			return nil
		case err == nil && !info.IsDir():
			return fmt.Errorf("SQLite path parent is not a directory: %q", parent)
		case err == nil:
			return nil
		case !os.IsNotExist(err):
			return safePathError("inspect SQLite path parent", parent, err)
		}

		next := filepath.Dir(parent)
		if next == parent {
			return fmt.Errorf("SQLite path has no existing directory ancestor: %q", path)
		}
		parent = next
	}
}

func validSQLiteHeader(header []byte, fileSize int64) bool {
	if len(header) != sqliteHeaderBytes || string(header[:len(sqliteHeader)]) != sqliteHeader {
		return false
	}
	pageSize := int(binary.BigEndian.Uint16(header[16:18]))
	if pageSize == 1 {
		pageSize = maxSQLitePageBytes
	}
	if pageSize < 512 || pageSize > maxSQLitePageBytes || pageSize&(pageSize-1) != 0 {
		return false
	}
	if (header[18] != 1 && header[18] != 2) || (header[19] != 1 && header[19] != 2) {
		return false
	}
	// SQLite requires at least 480 usable bytes on every page after the
	// reserved trailing region is removed.
	if pageSize-int(header[20]) < 480 {
		return false
	}
	if header[21] != 64 || header[22] != 32 || header[23] != 32 {
		return false
	}
	return fileSize >= int64(pageSize) && fileSize%int64(pageSize) == 0
}

func firstSQLiteSidecar(path string) (string, bool, error) {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		sidecar := path + suffix
		if _, err := os.Lstat(sidecar); err == nil {
			return sidecar, true, nil
		} else if !os.IsNotExist(err) {
			return "", false, safePathError("inspect SQLite sidecar", sidecar, err)
		}
	}
	return "", false, nil
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
