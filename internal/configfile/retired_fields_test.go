package configfile

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
)

func TestParseReadOnlyMetadataToleratesRetiredSameLineageField(t *testing.T) {
	// dolt_proxied_server_* were persisted by this lineage and removed from
	// Config in 5a7cc3e1a. The strict guard must ignore them, matching the
	// lenient store-open loader, rather than lock out an otherwise-openable
	// same-lineage workspace.
	data := []byte(`{
  "database": "dolt",
  "backend": "dolt",
  "dolt_proxied_server_config": "proxied-config.yaml",
  "dolt_proxied_server_log": "proxied.log",
  "dolt_proxied_server_root_path": "proxieddb"
}`)
	cfg, err := ParseReadOnlyMetadata(data)
	if err != nil {
		t.Fatalf("ParseReadOnlyMetadata rejected retired same-lineage fields: %v", err)
	}
	if cfg == nil || cfg.GetBackend() != BackendDolt {
		t.Fatalf("decoded config = %#v, want Dolt backend", cfg)
	}
}

func TestParseReadOnlyMetadataStillRejectsForeignField(t *testing.T) {
	// A field this lineage never had (a newer/foreign workspace marker) must
	// still be refused so forward-incompatibility protection is preserved.
	data := []byte(`{"database":"dolt","backend":"dolt","jsonl_export":"issues.jsonl"}`)
	if cfg, err := ParseReadOnlyMetadata(data); err == nil || cfg != nil {
		t.Fatalf("ParseReadOnlyMetadata accepted a foreign field: cfg=%#v err=%v", cfg, err)
	}
}

func TestParseReadOnlyMetadataRejectsNoncanonicalRetiredCase(t *testing.T) {
	// Only the exact canonical retired key is tolerated; a case-variant is still
	// noncanonical and must be rejected.
	data := []byte(`{"database":"dolt","Dolt_Proxied_Server_Config":"x"}`)
	if cfg, err := ParseReadOnlyMetadata(data); err == nil || cfg != nil {
		t.Fatalf("ParseReadOnlyMetadata accepted a noncanonical retired key: cfg=%#v err=%v", cfg, err)
	}
}

func TestRetiredMetadataFieldsReportsPresentRetired(t *testing.T) {
	beadsDir := t.TempDir()
	writeMetadataForRetiredTest(t, beadsDir, `{
  "database": "dolt",
  "backend": "dolt",
  "dolt_proxied_server_log": "proxied.log",
  "dolt_proxied_server_config": "proxied-config.yaml"
}`)
	got := RetiredMetadataFields(beadsDir)
	want := []string{"dolt_proxied_server_config", "dolt_proxied_server_log"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("RetiredMetadataFields = %v, want %v (sorted)", got, want)
	}
}

func TestRemoveRetiredMetadataFieldsStripsOnlyRetired(t *testing.T) {
	beadsDir := t.TempDir()
	// A retired key plus a genuinely-foreign key. Only the retired key must go;
	// the foreign key must survive so the strict guard still refuses this
	// forward-incompatible workspace.
	writeMetadataForRetiredTest(t, beadsDir, `{
  "database": "dolt",
  "backend": "dolt",
  "dolt_proxied_server_config": "proxied-config.yaml",
  "jsonl_export": "issues.jsonl"
}`)

	removed, err := RemoveRetiredMetadataFields(beadsDir)
	if err != nil {
		t.Fatalf("RemoveRetiredMetadataFields: %v", err)
	}
	if len(removed) != 1 || removed[0] != "dolt_proxied_server_config" {
		t.Fatalf("removed = %v, want [dolt_proxied_server_config]", removed)
	}

	fields := decodeMetadataFields(t, beadsDir)
	if _, ok := fields["dolt_proxied_server_config"]; ok {
		t.Error("retired key survived RemoveRetiredMetadataFields")
	}
	if _, ok := fields["jsonl_export"]; !ok {
		t.Error("foreign key was dropped; forward-incompat protection weakened")
	}
	if _, ok := fields["database"]; !ok {
		t.Error("recognized key was dropped")
	}

	// Idempotent: a second run finds nothing to remove.
	again, err := RemoveRetiredMetadataFields(beadsDir)
	if err != nil {
		t.Fatalf("second RemoveRetiredMetadataFields: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("second run removed %v, want none", again)
	}
}

func TestRemoveRetiredMetadataFieldsNoopWithoutRetired(t *testing.T) {
	beadsDir := t.TempDir()
	original := `{"database":"dolt","backend":"dolt"}`
	writeMetadataForRetiredTest(t, beadsDir, original)

	removed, err := RemoveRetiredMetadataFields(beadsDir)
	if err != nil {
		t.Fatalf("RemoveRetiredMetadataFields: %v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("removed %v from clean metadata, want none", removed)
	}
	// A no-op must not rewrite the file.
	after, err := os.ReadFile(ConfigPath(beadsDir))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != original {
		t.Fatalf("clean metadata was rewritten: got %q want %q", after, original)
	}
}

func TestIsConcurrentMetadataChange(t *testing.T) {
	if !IsConcurrentMetadataChange(configChangedError("opening config", "metadata.json")) {
		t.Error("IsConcurrentMetadataChange did not classify errConfigChanged")
	}
	if IsConcurrentMetadataChange(errors.New(`unknown or noncanonical metadata field "x"`)) {
		t.Error("IsConcurrentMetadataChange misclassified an incompatibility error")
	}
	if IsConcurrentMetadataChange(nil) {
		t.Error("IsConcurrentMetadataChange classified nil")
	}
}

func writeMetadataForRetiredTest(t *testing.T, beadsDir, body string) {
	t.Helper()
	if err := os.WriteFile(ConfigPath(beadsDir), []byte(body), 0o600); err != nil {
		t.Fatalf("write metadata.json: %v", err)
	}
}

func decodeMetadataFields(t *testing.T, beadsDir string) map[string]json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(ConfigPath(beadsDir))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("decode metadata.json: %v", err)
	}
	return fields
}
