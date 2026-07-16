// Package artifactpreflight provides the shared, effect-free workspace
// discovery and backend classification used by doctor artifact scan and fix.
package artifactpreflight

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/steveyegge/beads/internal/configfile"
)

type Workspace struct {
	BeadsDir  string
	Backend   string
	CruftOnly bool
}

type Snapshot struct {
	Workspaces []Workspace
}

// Preflight discovers the complete workspace set, rejects every pending
// migration marker, and only then classifies backends without compatibility
// writes. No partial snapshot is returned on failure.
func Preflight(root string) (Snapshot, error) {
	dirs, err := discoverBeadsDirs(root)
	if err != nil {
		return Snapshot{}, err
	}
	for _, beadsDir := range dirs {
		if err := configfile.RejectPendingBackendMigration(beadsDir); err != nil {
			return Snapshot{}, err
		}
	}

	workspaces := make([]Workspace, 0, len(dirs))
	for _, beadsDir := range dirs {
		workspace := Workspace{
			BeadsDir:  beadsDir,
			Backend:   configfile.BackendDolt,
			CruftOnly: IsRedirectExpected(beadsDir),
		}
		// A redirect-only directory's non-redirect contents are disposable
		// cruft, so invalid legacy metadata must not prevent doctor from
		// removing it. Migration markers were still checked above.
		if !workspace.CruftOnly {
			cfg, err := configfile.LoadReadOnly(beadsDir)
			if err != nil {
				return Snapshot{}, fmt.Errorf("inspect artifact workspace backend: %w", err)
			}
			if cfg != nil {
				switch cfg.Backend {
				case "", configfile.BackendDolt:
					workspace.Backend = configfile.BackendDolt
				case configfile.BackendSQLite, configfile.BackendPostgres, configfile.BackendMySQL:
					workspace.Backend = cfg.Backend
				default:
					return Snapshot{}, fmt.Errorf("inspect artifact workspace backend: unsupported backend %q", cfg.Backend)
				}
			}
		}
		workspaces = append(workspaces, workspace)
	}
	return Snapshot{Workspaces: workspaces}, nil
}

func discoverBeadsDirs(root string) ([]string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve artifact scan root: %w", err)
	}
	found := make(map[string]struct{})
	var worktreeRoots []string
	if err := walkForBeads(absRoot, true, found, &worktreeRoots); err != nil {
		return nil, fmt.Errorf("scan artifact workspaces: %w", err)
	}
	for _, worktreeRoot := range worktreeRoots {
		if err := walkForBeads(worktreeRoot, false, found, nil); err != nil {
			return nil, fmt.Errorf("scan git-managed artifact workspaces: %w", err)
		}
	}
	dirs := make([]string, 0, len(found))
	for dir := range found {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	return dirs, nil
}

func walkForBeads(root string, collectGitWorktrees bool, found map[string]struct{}, worktreeRoots *[]string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		base := filepath.Base(path)
		if base == ".beads" {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return errors.New("artifact workspace .beads entry must be a physical directory")
			}
			found[filepath.Clean(path)] = struct{}{}
			return filepath.SkipDir
		}
		if !info.IsDir() {
			return nil
		}
		if base == "node_modules" || base == "vendor" || base == "__pycache__" {
			return filepath.SkipDir
		}
		if base == ".git" {
			if collectGitWorktrees {
				worktrees := filepath.Join(path, "beads-worktrees")
				worktreesInfo, err := os.Lstat(worktrees)
				switch {
				case err == nil && worktreesInfo.IsDir():
					*worktreeRoots = append(*worktreeRoots, worktrees)
				case err == nil:
					// A non-directory entry is not a traversable worktree root.
				case os.IsNotExist(err):
				default:
					return err
				}
			}
			return filepath.SkipDir
		}
		return nil
	})
}

// IsRedirectExpected reports locations whose .beads directory should contain
// only redirect state. Historical orchestrator layouts remain supported for
// artifact cleanup compatibility.
func IsRedirectExpected(beadsDir string) bool {
	parent := filepath.Dir(beadsDir)
	parentName := filepath.Base(parent)
	grandparent := filepath.Dir(parent)
	grandparentName := filepath.Base(grandparent)

	if grandparentName == "polecats" || grandparentName == "crew" {
		return true
	}
	if parentName == "rig" && grandparentName == "refinery" {
		return true
	}
	if grandparentName == "beads-worktrees" {
		return true
	}
	if hasDirectory(filepath.Join(parent, "mayor")) || hasDirectory(filepath.Join(parent, "polecats")) {
		return hasDirectory(filepath.Join(parent, "mayor", "rig", ".beads"))
	}
	return false
}

func hasDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
