package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	spikeb "github.com/steveyegge/beads/internal/memorybeads/spikeb"
)

// This is an architecture-spike fixture, not production canonical-import
// support. It pins the one compatibility fact B1 needs before selecting a
// declaration: both currently supported legacy parser families reject its
// first record instead of interpreting later Memory Bead records as issues or
// KV memories.
func TestMemoryBeadsInterchangeGuardStopsLegacyParsersBeforeData(t *testing.T) {
	unit := spikeb.InterchangeUnit{
		Declaration:     spikeb.CanonicalDeclaration(),
		SourceProjectID: "source-project",
		Records: []spikeb.Record{{
			ID: "source-memory", Kind: spikeb.KindMemory, RevisionID: "source-revision",
			Title: "Source memory", Body: "must never reach the legacy data path",
			Lifecycle: spikeb.LifecycleActive, Author: "Spike <spike@example.test>", Origin: "native",
		}},
	}
	wire, err := spikeb.EncodeInterchange(unit)
	if err != nil {
		t.Fatal(err)
	}

	issues, memories, err := parseImportRecords(bytes.NewReader(wire))
	if err == nil {
		t.Fatalf("bd import parser accepted canonical spike wire: issues=%d memories=%d", len(issues), len(memories))
	}
	if len(issues) != 0 || len(memories) != 0 {
		t.Fatalf("bd import parser returned data with guard error: issues=%d memories=%d", len(issues), len(memories))
	}

	path := filepath.Join(t.TempDir(), "memory-beads-spike.jsonl")
	if err := os.WriteFile(path, wire, 0o600); err != nil {
		t.Fatal(err)
	}
	issues, config, err := parseJSONLFile(path)
	if err == nil {
		t.Fatalf("bootstrap parser accepted canonical spike wire: issues=%d config=%d", len(issues), len(config))
	}
	if len(issues) != 0 || len(config) != 0 {
		t.Fatalf("bootstrap parser returned data with guard error: issues=%d config=%d", len(issues), len(config))
	}
}
