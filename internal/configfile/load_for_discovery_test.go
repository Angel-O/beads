package configfile

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// LoadForDiscovery backs the pre-store path/mode probes. It must behave like
// Load (lenient decode) EXCEPT that it never migrates a legacy config.json or
// writes anything, so discovery cannot mutate a workspace before the pre-store
// guard has decided whether it is safe to open.

func TestLoadForDiscoveryDoesNotMigrateLegacyConfig(t *testing.T) {
	beadsDir := t.TempDir()
	legacyPath := filepath.Join(beadsDir, "config.json")
	legacyBytes := []byte("{\n  \"database\": \"dolt\",\n  \"backend\": \"dolt\"\n}\n")
	if err := os.WriteFile(legacyPath, legacyBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadForDiscovery(beadsDir)
	if err != nil {
		t.Fatalf("LoadForDiscovery: %v", err)
	}
	if cfg == nil || cfg.GetBackend() != BackendDolt {
		t.Fatalf("loaded config = %#v, want legacy Dolt", cfg)
	}
	if _, err := os.Stat(ConfigPath(beadsDir)); !os.IsNotExist(err) {
		t.Fatalf("LoadForDiscovery created metadata.json (migrated the legacy config): %v", err)
	}
	after, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, legacyBytes) {
		t.Fatalf("LoadForDiscovery changed legacy config bytes: got %q want %q", after, legacyBytes)
	}
}

func TestLoadForDiscoveryToleratesForwardIncompatibleField(t *testing.T) {
	// A forward-incompatible field must NOT make discovery error: if it did, a
	// server-mode or SQL workspace (no local dolt directory) would resolve to no
	// database path and the command would report a misleading "no beads database
	// found" instead of the guard's precise incompatibility message. Discovery
	// stays lenient; the strict guard is what refuses the workspace.
	beadsDir := t.TempDir()
	metadata := []byte(`{"database":"dolt","backend":"dolt","jsonl_export":"issues.jsonl"}`)
	if err := os.WriteFile(ConfigPath(beadsDir), metadata, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadForDiscovery(beadsDir)
	if err != nil {
		t.Fatalf("LoadForDiscovery rejected a forward-incompatible field: %v", err)
	}
	if cfg == nil || cfg.GetBackend() != BackendDolt {
		t.Fatalf("loaded config = %#v, want Dolt backend with the unknown field ignored", cfg)
	}
}

func TestLoadForDiscoveryDoesNotMigrateForwardIncompatibleLegacyConfig(t *testing.T) {
	// The exact bug this fixes: a legacy config.json carrying a forward-
	// incompatible field must not be silently migrated (field-stripped) during
	// discovery. LoadForDiscovery reads it leniently but leaves it on disk so the
	// strict guard refuses the workspace instead of a lenient probe mutating it.
	beadsDir := t.TempDir()
	legacyPath := filepath.Join(beadsDir, "config.json")
	legacyBytes := []byte(`{"database":"dolt","backend":"dolt","jsonl_export":"issues.jsonl"}`)
	if err := os.WriteFile(legacyPath, legacyBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadForDiscovery(beadsDir); err != nil {
		t.Fatalf("LoadForDiscovery: %v", err)
	}
	if _, err := os.Stat(ConfigPath(beadsDir)); !os.IsNotExist(err) {
		t.Fatalf("LoadForDiscovery migrated an incompatible legacy config to metadata.json: %v", err)
	}
	after, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatalf("legacy config.json was removed by discovery: %v", err)
	}
	if !bytes.Equal(after, legacyBytes) {
		t.Fatalf("LoadForDiscovery changed legacy config bytes: got %q want %q", after, legacyBytes)
	}
}

func TestLoadForDiscoveryAbsentMetadataIsNil(t *testing.T) {
	beadsDir := t.TempDir()
	cfg, err := LoadForDiscovery(beadsDir)
	if err != nil {
		t.Fatalf("LoadForDiscovery for absent metadata: %v", err)
	}
	if cfg != nil {
		t.Fatalf("LoadForDiscovery for absent metadata = %#v, want nil", cfg)
	}
}

func TestLoadForDiscoveryReturnsErrorForMalformedMetadata(t *testing.T) {
	// A present-but-unparseable metadata.json must still be a hard error so the
	// storage-mode probe (loadServerModeFromBeadsDir) refuses rather than falling
	// back to the embedded store.
	beadsDir := t.TempDir()
	if err := os.WriteFile(ConfigPath(beadsDir), []byte(`{"dolt_mode":"serv`), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, err := LoadForDiscovery(beadsDir); err == nil || cfg != nil {
		t.Fatalf("LoadForDiscovery accepted malformed metadata: cfg=%#v err=%v", cfg, err)
	}
}
