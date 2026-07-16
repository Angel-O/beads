// Package control serializes workspace metadata mutations against backend
// migration cutover. The lock file is stable and must never be removed: flock
// protects an inode, so unlinking it would let another process lock a new inode
// at the same path.
package control

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/steveyegge/beads/internal/lockfile"
)

const (
	FileName      = "backend-migration-control.lock"
	StateFileName = "backend-migration.lock"
)

var (
	ErrBusy            = errors.New("backend migration workspace control is busy")
	ErrRecoveryPending = errors.New("backend migration recovery is required")
)

type Guard struct {
	file *os.File
}

// TryAcquire obtains exclusive workspace control without waiting. Waiting is
// unsafe for a metadata writer: it could resume after migration removed its
// recovery marker and publish a stale pre-cutover configuration.
func TryAcquire(beadsDir string) (*Guard, error) {
	if beadsDir == "" {
		return nil, errors.New("backend migration workspace control requires a beads directory")
	}
	path := filepath.Join(beadsDir, FileName)
	file, err := openControlFile(path)
	if err != nil {
		return nil, fmt.Errorf("open backend migration workspace control: %w", err)
	}
	if err := lockfile.FlockExclusiveNonBlocking(file); err != nil {
		_ = file.Close()
		if lockfile.IsLocked(err) || errors.Is(err, lockfile.ErrLockBusy) {
			return nil, ErrBusy
		}
		return nil, fmt.Errorf("lock backend migration workspace control: %w", err)
	}
	return &Guard{file: file}, nil
}

// RejectPending prevents workspace metadata mutations while durable migration
// or cleanup state requires recovery. It does not create the stable control
// file, so callers can use it before TryAcquire without leaving an artifact.
func RejectPending(beadsDir string) error {
	if beadsDir == "" {
		return nil
	}
	_, err := os.Lstat(filepath.Join(beadsDir, StateFileName))
	switch {
	case err == nil:
		return ErrRecoveryPending
	case !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("inspect backend migration state: %w", err)
	}
	markers, err := CleanupMarkers(beadsDir)
	if err != nil {
		return err
	}
	if len(markers) != 0 {
		return ErrRecoveryPending
	}
	return nil
}

// CleanupMarkers lists durable cleanup markers without interpreting workspace
// path bytes as a glob pattern.
func CleanupMarkers(beadsDir string) ([]string, error) {
	entries, err := os.ReadDir(beadsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("inspect backend migration cleanup state: %w", err)
	}
	markers := make([]string, 0, 1)
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".backend-migration-") && strings.HasSuffix(name, ".cleanup.lock") {
			markers = append(markers, filepath.Join(beadsDir, name))
		}
	}
	return markers, nil
}

func (g *Guard) Close() error {
	if g == nil || g.file == nil {
		return nil
	}
	file := g.file
	g.file = nil
	return errors.Join(lockfile.FlockUnlock(file), file.Close())
}
