package sqlite

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/steveyegge/beads/internal/safefile"
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
	return hasLocalInitializationEvidenceWithWorkspaceOpener(
		beadsDir,
		configuredPath,
		safefile.OpenReadOnlyNoFollow,
	)
}

func hasLocalInitializationEvidenceWithWorkspaceOpener(
	beadsDir string,
	configuredPath string,
	workspaceOpener func(string) (*os.File, error),
) (exists bool, err error) {
	if beadsDir == "" {
		return false, errors.New("SQLite evidence requires a workspace directory")
	}
	workspace, present, err := openStableSQLiteWorkspaceRoot(beadsDir, workspaceOpener)
	if err != nil || !present {
		return false, err
	}
	defer workspace.finishBooleanResult(&exists, &err)

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

type stableSQLiteWorkspaceRoot struct {
	path       string
	file       *os.File
	openedInfo os.FileInfo
}

func openStableSQLiteWorkspaceRoot(
	beadsDir string,
	opener func(string) (*os.File, error),
) (*stableSQLiteWorkspaceRoot, bool, error) {
	if err := safefile.ValidateMetadataPath(beadsDir); err != nil {
		return nil, false, safePathError("validate SQLite workspace root", beadsDir, err)
	}
	namedInfo, err := os.Lstat(beadsDir)
	if os.IsNotExist(err) {
		if ancestorErr := safefile.ValidateMissingPathAncestors(beadsDir); ancestorErr != nil {
			return nil, false, fmt.Errorf("validate absent SQLite workspace root %q: %w", beadsDir, ancestorErr)
		}
		return nil, false, nil
	}
	if err != nil {
		return nil, false, safePathError("inspect SQLite workspace root", beadsDir, err)
	}
	if !namedInfo.IsDir() {
		return nil, true, fmt.Errorf("SQLite workspace root is not a directory: %q", beadsDir)
	}
	if opener == nil {
		opener = safefile.OpenReadOnlyNoFollow
	}
	file, err := opener(beadsDir)
	if err != nil {
		return nil, true, safePathError("open SQLite workspace root", beadsDir, err)
	}
	if file == nil {
		return nil, true, fmt.Errorf("open SQLite workspace root %q: opener returned no descriptor", beadsDir)
	}
	closeOnError := func(result error) (*stableSQLiteWorkspaceRoot, bool, error) {
		_ = file.Close()
		return nil, true, result
	}
	openedInfo, err := file.Stat()
	if err != nil {
		return closeOnError(safePathError("inspect opened SQLite workspace root", beadsDir, err))
	}
	namedAtOpen, err := os.Lstat(beadsDir)
	if err != nil {
		return closeOnError(safePathError("reinspect opened SQLite workspace root", beadsDir, err))
	}
	if !sameSQLiteEvidenceDirectory(namedInfo, openedInfo) ||
		!sameSQLiteEvidenceDirectory(openedInfo, namedAtOpen) {
		return closeOnError(fmt.Errorf("SQLite workspace root changed while opening: %q", beadsDir))
	}
	return &stableSQLiteWorkspaceRoot{path: beadsDir, file: file, openedInfo: openedInfo}, true, nil
}

func (workspace *stableSQLiteWorkspaceRoot) finishBooleanResult(exists *bool, resultErr *error) {
	afterInfo, statErr := workspace.file.Stat()
	namedAfter, namedErr := os.Lstat(workspace.path)
	closeErr := workspace.file.Close()
	if *resultErr == nil {
		switch {
		case statErr != nil:
			*resultErr = safePathError("reinspect opened SQLite workspace root", workspace.path, statErr)
		case namedErr != nil:
			*resultErr = safePathError("reinspect SQLite workspace root name", workspace.path, namedErr)
		case !sameSQLiteEvidenceDirectory(workspace.openedInfo, afterInfo) ||
			!sameSQLiteEvidenceDirectory(workspace.openedInfo, namedAfter):
			*resultErr = fmt.Errorf("SQLite workspace root changed while inspected: %q", workspace.path)
		case closeErr != nil:
			*resultErr = safePathError("close SQLite workspace root", workspace.path, closeErr)
		}
	}
	if *resultErr != nil {
		*exists = false
	}
}

func sameSQLiteEvidenceDirectory(before, after os.FileInfo) bool {
	return before != nil && after != nil && before.IsDir() && after.IsDir() &&
		os.SameFile(before, after) && before.Size() == after.Size() && before.ModTime().Equal(after.ModTime())
}

func sqliteDatabaseAt(path string) (valid, present bool, err error) {
	return sqliteDatabaseAtWithOpener(path, safefile.OpenReadOnlyNoFollow)
}

func sqliteDatabaseAtWithOpener(
	path string,
	opener func(string) (*os.File, error),
) (valid, present bool, err error) {
	if err := safefile.ValidateMetadataPath(path); err != nil {
		return false, true, safePathError("validate SQLite path", path, err)
	}
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

	file, err := opener(path)
	if err != nil {
		return false, true, safePathError("open SQLite path", path, err)
	}
	defer file.Close() //nolint:errcheck // read-only evidence descriptor
	openedInfo, err := file.Stat()
	if err != nil {
		return false, true, safePathError("inspect opened SQLite path", path, err)
	}
	if !sameSQLiteEvidenceFile(namedInfo, openedInfo) {
		return false, true, fmt.Errorf("SQLite path changed while opening: %q", path)
	}
	namedAtOpen, err := os.Lstat(path)
	if err != nil {
		return false, true, safePathError("reinspect opened SQLite path", path, err)
	}
	if !sameSQLiteEvidenceFile(openedInfo, namedAtOpen) {
		return false, true, fmt.Errorf("SQLite path changed while opening: %q", path)
	}
	if err := validateSQLiteEvidenceLinkCount(path, file, openedInfo); err != nil {
		return false, true, err
	}

	header := make([]byte, sqliteHeaderBytes)
	_, readErr := io.ReadFull(file, header)
	afterInfo, err := file.Stat()
	if err != nil {
		return false, true, safePathError("reinspect SQLite path", path, err)
	}
	namedAfter, err := os.Lstat(path)
	if err != nil {
		return false, true, safePathError("reinspect SQLite name", path, err)
	}
	if !sameSQLiteEvidenceFile(openedInfo, afterInfo) || !sameSQLiteEvidenceFile(openedInfo, namedAfter) {
		return false, true, fmt.Errorf("SQLite path changed while inspected: %q", path)
	}
	if err := validateSQLiteEvidenceLinkCount(path, file, afterInfo); err != nil {
		return false, true, err
	}
	if readErr != nil {
		if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) {
			return false, true, nil
		}
		return false, true, safePathError("read SQLite header", path, readErr)
	}
	return validSQLiteHeader(header, afterInfo.Size()), true, nil
}

func validateSQLiteEvidenceLinkCount(path string, file *os.File, info os.FileInfo) error {
	linkCount, known := safefile.OpenedFileLinkCount(file, info)
	if !known {
		return fmt.Errorf("SQLite database hard-link count is unavailable: %q", path)
	}
	if linkCount != 1 {
		return fmt.Errorf("SQLite database has %d hard links; exactly one is required: %q", linkCount, path)
	}
	return nil
}

func sameSQLiteEvidenceFile(before, after os.FileInfo) bool {
	return before != nil && after != nil && before.Mode().IsRegular() && after.Mode().IsRegular() &&
		os.SameFile(before, after) && before.Size() == after.Size() && before.ModTime().Equal(after.ModTime())
}

// validateMissingSQLitePath distinguishes an absent leaf or future directory
// from a path hidden behind a dangling symlink or non-directory ancestor.
func validateMissingSQLitePath(path string) error {
	return safefile.ValidateMissingPathAncestors(path)
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
