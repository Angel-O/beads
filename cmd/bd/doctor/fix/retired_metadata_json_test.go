package fix

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestFixRetiredMetadataKeys_StripsRetiredPreservesForeign verifies the doctor
// remediation the guard's refusal message points at: it strips a same-lineage
// key a newer bd retired from Config, while preserving a genuinely-foreign key
// so a workspace written by a newer bd stays refused by the pre-store guard.
func TestFixRetiredMetadataKeys_StripsRetiredPreservesForeign(t *testing.T) {
	dir := setupTestWorkspace(t)
	configPath := filepath.Join(dir, ".beads", "metadata.json")

	metadata := []byte(`{
  "database": "dolt",
  "backend": "dolt",
  "dolt_proxied_server_config": "proxied-config.yaml",
  "jsonl_export": "issues.jsonl"
}`)
	if err := os.WriteFile(configPath, metadata, 0o600); err != nil {
		t.Fatalf("write metadata.json: %v", err)
	}

	if err := FixRetiredMetadataKeys(dir); err != nil {
		t.Fatalf("FixRetiredMetadataKeys failed: %v", err)
	}

	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("decode rewritten metadata.json: %v", err)
	}
	if _, ok := fields["dolt_proxied_server_config"]; ok {
		t.Error("retired key survived FixRetiredMetadataKeys")
	}
	if _, ok := fields["jsonl_export"]; !ok {
		t.Error("foreign key was dropped; guard's forward-incompat protection weakened")
	}
	if _, ok := fields["database"]; !ok {
		t.Error("recognized key was dropped")
	}
}

// TestFixRetiredMetadataKeys_NoOpWhenClean verifies the fix leaves a workspace
// without retired keys byte-for-byte unchanged.
func TestFixRetiredMetadataKeys_NoOpWhenClean(t *testing.T) {
	dir := setupTestWorkspace(t)
	configPath := filepath.Join(dir, ".beads", "metadata.json")

	original := []byte(`{"database":"dolt","backend":"dolt"}`)
	if err := os.WriteFile(configPath, original, 0o600); err != nil {
		t.Fatalf("write metadata.json: %v", err)
	}

	if err := FixRetiredMetadataKeys(dir); err != nil {
		t.Fatalf("FixRetiredMetadataKeys failed: %v", err)
	}

	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, original) {
		t.Fatalf("clean metadata was rewritten: got %q want %q", after, original)
	}
}
