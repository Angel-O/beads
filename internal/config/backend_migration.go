package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
	backendmigrationcontrol "github.com/steveyegge/beads/internal/backendmigration/control"
	"github.com/steveyegge/beads/internal/safefile"
)

const maxWorkspaceBackendSelectionConfig = 1 << 20

// ReadWorkspaceDoltSelection strictly reads the workspace-local Dolt mode
// selectors without consulting cached global configuration. config.local.yaml
// has the same precedence it has during normal command initialization.
func ReadWorkspaceDoltSelection(beadsDir string) (mode string, sharedServer bool, err error) {
	reader := viper.New()
	reader.SetConfigType("yaml")
	loaded := false
	for _, name := range []string{"config.yaml", "config.local.yaml"} {
		path := filepath.Join(beadsDir, name)
		if _, statErr := os.Lstat(path); errors.Is(statErr, os.ErrNotExist) {
			continue
		} else if statErr != nil {
			return "", false, errors.New("workspace configuration could not be inspected safely")
		}
		data, readErr := safefile.ReadRegularFile(path, maxWorkspaceBackendSelectionConfig)
		if readErr != nil {
			return "", false, errors.New("workspace configuration must be a bounded regular file")
		}
		if !loaded {
			readErr = reader.ReadConfig(bytes.NewReader(data))
			loaded = true
		} else {
			readErr = reader.MergeConfig(bytes.NewReader(data))
		}
		if readErr != nil {
			return "", false, errors.New("workspace configuration is invalid")
		}
	}
	return reader.GetString("dolt.mode"), reader.GetBool("dolt.shared-server"), nil
}

func withBackendSelectionControl(configPath, key string, write func() error) (err error) {
	key = normalizeYamlKey(key)
	if key != "dolt.mode" && key != "dolt.shared-server" {
		return write()
	}
	beadsDir := filepath.Dir(configPath)
	if filepath.Base(beadsDir) != ".beads" {
		return write()
	}
	// Check before acquiring so a marker-only refusal leaves no stable control
	// file, then check again under the guard to close the publication race.
	if err := backendmigrationcontrol.RejectPending(beadsDir); err != nil {
		return err
	}
	guard, err := backendmigrationcontrol.TryAcquire(beadsDir)
	if err != nil {
		return fmt.Errorf("acquire backend-selection workspace control: %w", err)
	}
	defer func() {
		err = errors.Join(err, guard.Close())
	}()
	if err := backendmigrationcontrol.RejectPending(beadsDir); err != nil {
		return err
	}
	return write()
}
