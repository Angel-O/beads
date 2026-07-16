package fix

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/steveyegge/beads/internal/artifactpreflight"
	backendmigrationcontrol "github.com/steveyegge/beads/internal/backendmigration/control"
	"github.com/steveyegge/beads/internal/configfile"
)

// ClassicArtifacts removes beads classic artifacts found by scanning the path.
// Only removes artifacts that are safe to delete:
// - JSONL export artifacts (not issues.jsonl itself)
// - SQLite WAL/SHM files and backup databases
// - Extra files in redirect-only .beads directories
func ClassicArtifacts(path string) error {
	return classicArtifacts(path, os.Stdout)
}

// ClassicArtifactsQuiet performs the same cleanup without writing human
// progress lines, so JSON callers can emit exactly one document.
func ClassicArtifactsQuiet(path string) error {
	return classicArtifacts(path, io.Discard)
}

func classicArtifacts(path string, output io.Writer) (returnErr error) {
	var removed, skipped, errCount int

	// The first pass is strictly effect-free and checks every marker before any
	// backend inspection. Then acquire every workspace control before deleting
	// anything, and repeat the pass while those controls are held.
	snapshot, err := artifactpreflight.Preflight(path)
	if err != nil {
		return err
	}
	guards := make([]*backendmigrationcontrol.Guard, 0, len(snapshot.Workspaces))
	defer func() {
		for index := len(guards) - 1; index >= 0; index-- {
			returnErr = errors.Join(returnErr, guards[index].Close())
		}
	}()
	for _, workspace := range snapshot.Workspaces {
		guard, err := backendmigrationcontrol.TryAcquire(workspace.BeadsDir)
		if err != nil {
			return err
		}
		guards = append(guards, guard)
	}
	controlled, err := artifactpreflight.Preflight(path)
	if err != nil {
		return err
	}
	if !sameWorkspacePaths(snapshot.Workspaces, controlled.Workspaces) {
		return errors.New("artifact workspace set changed during cleanup admission; retry")
	}
	for _, workspace := range controlled.Workspaces {
		if workspace.CruftOnly {
			r, e := cleanCruftBeadsDirFiles(workspace.BeadsDir, output)
			removed += r
			errCount += e
			continue
		}
		if workspace.Backend != configfile.BackendDolt {
			continue
		}
		r, s, e := cleanBeadsDirArtifacts(workspace.BeadsDir, output)
		removed += r
		skipped += s
		errCount += e
	}

	// Report summary
	fmt.Fprintf(output, "  Artifact cleanup: %d removed, %d skipped, %d errors\n", removed, skipped, errCount)

	if skipped > 0 {
		fmt.Fprintln(output, "  Skipped items may need manual review (e.g., issues.jsonl in dolt dirs, beads.db files)")
	}

	if errCount > 0 {
		return fmt.Errorf("%d artifact(s) could not be removed", errCount)
	}

	return nil
}

func sameWorkspacePaths(first, second []artifactpreflight.Workspace) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if first[index].BeadsDir != second[index].BeadsDir {
			return false
		}
	}
	return true
}

// cleanBeadsDirArtifacts cleans artifacts from a single .beads directory.
// Returns counts of removed, skipped, and errored items.
func cleanBeadsDirArtifacts(beadsDir string, output io.Writer) (removed, skipped, errCount int) {
	hasDolt := hasDoltDir(beadsDir)
	isRedirectExpected := isRedirectExpectedLocation(beadsDir)

	// 1. Clean JSONL artifacts in dolt-native directories
	if hasDolt {
		r, s, e := cleanJSONLArtifacts(beadsDir, output)
		removed += r
		skipped += s
		errCount += e
	}

	// 2. Clean SQLite artifacts
	r, s, e := cleanSQLiteArtifacts(beadsDir, output)
	removed += r
	skipped += s
	errCount += e

	// 3. Clean cruft .beads directories (if redirect is expected)
	// Clean even when the redirect file is missing — stale cruft files
	// (config.yaml, metadata.json, README.md, issues.jsonl, etc.) prevent
	// the redirect from being created and should be removed regardless.
	if isRedirectExpected {
		r, e := cleanCruftBeadsDirFiles(beadsDir, output)
		removed += r
		errCount += e
	}

	return
}

// hasDoltDir returns true if the .beads directory contains a dolt/ subdirectory.
func hasDoltDir(beadsDir string) bool {
	info, err := os.Stat(getDatabasePath(beadsDir))
	return err == nil && info.IsDir()
}

// isRedirectExpectedLocation returns true if this .beads directory should contain
// only a redirect file.
func isRedirectExpectedLocation(beadsDir string) bool {
	return artifactpreflight.IsRedirectExpected(beadsDir)
}

// cleanJSONLArtifacts removes stale JSONL files from a dolt-native .beads directory.
func cleanJSONLArtifacts(beadsDir string, output io.Writer) (removed, skipped, errCount int) {
	// Safe to delete (not the primary data source)
	safeFiles := []string{
		"issues.jsonl.new",
	}

	for _, name := range safeFiles {
		path := filepath.Join(beadsDir, name)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if err := os.Remove(path); err != nil {
			fmt.Fprintf(output, "  Error removing %s: %v\n", path, err)
			errCount++
			continue
		}
		fmt.Fprintf(output, "  Removed: %s (JSONL artifact)\n", path)
		removed++
	}

	// interactions.jsonl - only remove if empty
	interPath := filepath.Join(beadsDir, "interactions.jsonl")
	if info, err := os.Stat(interPath); err == nil {
		if info.Size() == 0 {
			if err := os.Remove(interPath); err != nil {
				fmt.Fprintf(output, "  Error removing %s: %v\n", interPath, err)
				errCount++
			} else {
				fmt.Fprintf(output, "  Removed: %s (empty interactions log)\n", interPath)
				removed++
			}
		} else {
			fmt.Fprintf(output, "  Skip (not empty): %s\n", interPath)
			skipped++
		}
	}

	// issues.jsonl in dolt-native directory - skip (needs manual review)
	issuesPath := filepath.Join(beadsDir, "issues.jsonl")
	if _, err := os.Stat(issuesPath); err == nil {
		fmt.Fprintf(output, "  Skip (needs review): %s (issues.jsonl in dolt-native dir)\n", issuesPath)
		skipped++
	}

	return
}

// cleanSQLiteArtifacts removes leftover SQLite database files.
func cleanSQLiteArtifacts(beadsDir string, output io.Writer) (removed, skipped, errCount int) {
	entries, err := os.ReadDir(beadsDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()

		// WAL and SHM files are always safe to delete
		if name == "beads.db-shm" || name == "beads.db-wal" {
			path := filepath.Join(beadsDir, name)
			if err := os.Remove(path); err != nil {
				fmt.Fprintf(output, "  Error removing %s: %v\n", path, err)
				errCount++
				continue
			}
			fmt.Fprintf(output, "  Removed: %s (SQLite WAL/SHM)\n", path)
			removed++
			continue
		}

		// beads.db - skip (needs manual review, could be active)
		if name == "beads.db" {
			path := filepath.Join(beadsDir, name)
			fmt.Fprintf(output, "  Skip (needs review): %s\n", path)
			skipped++
			continue
		}

		// Backup databases are safe to delete
		if strings.HasPrefix(name, "beads.backup-") && strings.HasSuffix(name, ".db") {
			path := filepath.Join(beadsDir, name)
			if err := os.Remove(path); err != nil {
				fmt.Fprintf(output, "  Error removing %s: %v\n", path, err)
				errCount++
				continue
			}
			fmt.Fprintf(output, "  Removed: %s (pre-migration backup)\n", path)
			removed++
		}
	}

	return
}

// cleanCruftBeadsDirFiles removes everything from a .beads directory except
// the redirect file and .gitkeep.
func cleanCruftBeadsDirFiles(beadsDir string, output io.Writer) (removed, errCount int) {
	entries, err := os.ReadDir(beadsDir)
	if err != nil {
		return 0, 1
	}

	for _, entry := range entries {
		name := entry.Name()
		// Keep redirect and .gitkeep
		if name == "redirect" || name == ".gitkeep" || name == backendmigrationcontrol.FileName {
			continue
		}

		entryPath := filepath.Join(beadsDir, name)

		// Validate path doesn't escape
		rel, err := filepath.Rel(beadsDir, entryPath)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			continue
		}

		if entry.IsDir() {
			if err := os.RemoveAll(entryPath); err != nil {
				fmt.Fprintf(output, "  Error removing %s: %v\n", entryPath, err)
				errCount++
				continue
			}
		} else {
			if err := os.Remove(entryPath); err != nil {
				fmt.Fprintf(output, "  Error removing %s: %v\n", entryPath, err)
				errCount++
				continue
			}
		}
		fmt.Fprintf(output, "  Removed: %s (cruft in redirect-only dir)\n", entryPath)
		removed++
	}

	return
}
