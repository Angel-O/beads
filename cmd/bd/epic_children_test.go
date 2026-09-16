package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func TestEpicChildrenJSONContract(t *testing.T) {
	data, err := json.Marshal([]*types.EpicChild{{
		ID: "bd-child", Status: types.StatusClosed, IssueType: types.TypeTask, StoragePlane: "durable",
	}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const want = `[{"id":"bd-child","status":"closed","issue_type":"task","storage_plane":"durable"}]`
	if string(data) != want {
		t.Fatalf("JSON = %s, want %s", data, want)
	}
	if epicChildrenCmd.Use != "child-closure <parent-id>" {
		t.Fatalf("command Use = %q, want authoritative epic child query shape", epicChildrenCmd.Use)
	}

	oldJSON := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = oldJSON })
	out := captureStdout(t, func() error {
		return renderEpicChildren("bd-epic", []*types.EpicChild{{
			ID: "bd-child", Status: types.StatusOpen, IssueType: types.TypeTask, StoragePlane: "wisp",
		}})
	})
	var got []map[string]string
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("rendered JSON: %v\n%s", err, out)
	}
	wantRendered := []map[string]string{{
		"id": "bd-child", "status": "open", "issue_type": "task", "storage_plane": "wisp",
	}}
	if len(got) != 1 || len(got[0]) != 4 || got[0]["id"] != wantRendered[0]["id"] || got[0]["status"] != wantRendered[0]["status"] || got[0]["issue_type"] != wantRendered[0]["issue_type"] || got[0]["storage_plane"] != wantRendered[0]["storage_plane"] {
		t.Fatalf("rendered JSON = %s, want bare child array with stable fields", strings.TrimSpace(out))
	}
}
