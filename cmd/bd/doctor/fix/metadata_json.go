package fix

import (
	"fmt"
	"os"
	"strings"

	"github.com/steveyegge/beads/internal/configfile"
)

// FixMissingMetadataJSON detects and regenerates a missing metadata.json file.
// This is the most common failure scenario: the file is deleted but .beads/ exists.
// Regenerates with default config values (similar to bd init). (GH#2478)
func FixMissingMetadataJSON(path string) error {
	beadsDir, err := resolvedWorkspaceBeadsDir(path)
	if err != nil {
		return err
	}

	configPath := configfile.ConfigPath(beadsDir)

	if _, err := os.Stat(configPath); err == nil {
		return nil
	}

	cfg := configfile.DefaultConfig()
	cfg.Backend = configfile.BackendDolt
	cfg.Database = "dolt"
	cfg.DoltMode = configfile.DoltModeServer

	if err := cfg.Save(beadsDir); err != nil {
		return fmt.Errorf("failed to regenerate metadata.json: %w", err)
	}

	fmt.Printf("  Regenerated metadata.json with default values\n")
	return nil
}

// FixRetiredMetadataKeys strips metadata.json keys that a newer bd version has
// retired from Config, preserving every other key. It is safe cruft cleanup that
// never opens the store: it removes only keys bd knows are retired, never an
// unknown key a newer bd might still require, so a workspace written by a newer
// bd stays refused by the pre-store metadata guard. This is the in-band recovery
// path the guard's refusal message points at for obsolete metadata keys.
func FixRetiredMetadataKeys(path string) error {
	beadsDir, err := resolvedWorkspaceBeadsDir(path)
	if err != nil {
		return err
	}

	removed, err := configfile.RemoveRetiredMetadataFields(beadsDir)
	if err != nil {
		return fmt.Errorf("failed to strip retired metadata keys: %w", err)
	}
	if len(removed) > 0 {
		fmt.Printf("  Removed %d retired metadata key(s): %s\n", len(removed), strings.Join(removed, ", "))
	}
	return nil
}
