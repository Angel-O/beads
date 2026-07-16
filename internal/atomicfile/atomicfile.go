// Package atomicfile provides atomic file writes via temp-file + rename.
//
// Writes land in a temporary file in the same directory as the target,
// are fsynced, then atomically renamed into place. Readers never see a
// partial or truncated file — only the previous complete version or the
// new complete version.
package atomicfile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ErrApplied marks an error reported after the requested namespace change was
// applied but its parent directory could not be durably synchronized.
var ErrApplied = errors.New("atomic file change was applied but not durably synced")

// SyncDirectory applies the platform's directory durability policy. Unix
// fsyncs the directory; Windows has no portable equivalent and returns nil.
func SyncDirectory(path string) error {
	return syncDirectory(path)
}

// WriteFile atomically writes data to path with the given permissions.
// It creates an atomic Writer, copies data in via io.Copy, then
// fsyncs and renames into place.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	return writeFile(path, data, perm, false)
}

// WriteFileDurable atomically writes data and synchronizes the containing
// directory. ErrApplied means the rename succeeded but directory sync failed.
func WriteFileDurable(path string, data []byte, perm os.FileMode) error {
	return writeFile(path, data, perm, true)
}

func writeFile(path string, data []byte, perm os.FileMode, durable bool) error {
	w, err := Create(path, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, bytes.NewReader(data)); err != nil {
		_ = w.Abort()
		return err
	}
	return w.close(durable)
}

// Writer is an io.WriteCloser that writes to a temporary file and
// atomically renames it to the target path on Close. Call Abort to
// discard the temp file without touching the target.
type Writer struct {
	target     string
	f          *os.File
	perm       os.FileMode
	done       bool
	syncParent func(string) error
}

// Create returns a Writer that will atomically replace path on Close.
// The temp file is created in the same directory as path to guarantee
// same-filesystem rename semantics.
func Create(path string, perm os.FileMode) (*Writer, error) {
	dir := filepath.Dir(path)
	base := filepath.Base(path)

	f, err := os.CreateTemp(dir, ".~"+base+".")
	if err != nil {
		return nil, fmt.Errorf("atomicfile: create temp: %w", err)
	}

	return &Writer{
		target:     path,
		f:          f,
		perm:       perm,
		syncParent: syncDirectory,
	}, nil
}

// Write delegates to the underlying temp file.
func (w *Writer) Write(p []byte) (int, error) {
	return w.f.Write(p)
}

// Close fsyncs the temp file and atomically renames it to the target path.
// After Close returns successfully, the target contains exactly the data
// written. On error the temp file is removed and the target is untouched.
func (w *Writer) Close() error {
	return w.close(false)
}

func (w *Writer) close(durable bool) error {
	if w.done {
		return nil
	}
	w.done = true

	// Ensure permissions before rename — CreateTemp uses 0600 by default.
	if err := w.f.Chmod(w.perm); err != nil {
		_ = w.f.Close()
		_ = os.Remove(w.f.Name())
		return fmt.Errorf("atomicfile: chmod: %w", err)
	}

	if err := w.f.Sync(); err != nil {
		_ = w.f.Close()
		_ = os.Remove(w.f.Name())
		return fmt.Errorf("atomicfile: sync: %w", err)
	}

	if err := w.f.Close(); err != nil {
		_ = os.Remove(w.f.Name())
		return fmt.Errorf("atomicfile: close: %w", err)
	}

	if err := os.Rename(w.f.Name(), w.target); err != nil {
		_ = os.Remove(w.f.Name())
		return fmt.Errorf("atomicfile: rename: %w", err)
	}
	if durable {
		if err := w.syncParent(filepath.Dir(w.target)); err != nil {
			return fmt.Errorf("atomicfile: sync parent directory: %w: %v", ErrApplied, err)
		}
	}

	return nil
}

// RemoveDurable durably removes path. ErrApplied means unlink succeeded but syncing
// the parent directory failed.
func RemoveDurable(path string) error {
	return removeDurable(path, syncDirectory)
}

func removeDurable(path string, syncParent func(string) error) error {
	if err := os.Remove(path); err != nil {
		return err
	}
	if err := syncParent(filepath.Dir(path)); err != nil {
		return fmt.Errorf("atomicfile: sync parent directory: %w: %v", ErrApplied, err)
	}
	return nil
}

// Abort discards the temp file without renaming. The target is untouched.
// Safe to call multiple times or after Close.
func (w *Writer) Abort() error {
	if w.done {
		return nil
	}
	w.done = true
	_ = w.f.Close()
	return os.Remove(w.f.Name())
}
