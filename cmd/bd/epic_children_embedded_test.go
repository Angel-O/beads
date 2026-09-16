//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func TestEmbeddedEpicChildrenAuthoritativeJSON(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "eac")
	parent := bdCreate(t, bd, dir, "Authoritative epic", "--type", "epic")
	durable := bdCreate(t, bd, dir, "Durable child", "--type", "task")
	wisp := bdCreate(t, bd, dir, "Wisp child", "--type", "task", "--ephemeral")
	bdDepAdd(t, bd, dir, durable.ID, parent.ID, "--type", "parent-child")
	bdDepAdd(t, bd, dir, wisp.ID, parent.ID, "--type", "parent-child")
	bdClose(t, bd, dir, durable.ID)

	cmd := exec.Command(bd, "epic", "child-closure", parent.ID, "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("bd epic children failed: %v\n%s", err, out)
	}
	var got []types.EpicChild
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("parse epic children JSON: %v\n%s", err, out)
	}
	if len(got) != 2 {
		t.Fatalf("got %d children, want 2: %+v", len(got), got)
	}
	if got[0].ID != durable.ID || got[0].Status != types.StatusClosed || got[0].StoragePlane != "durable" {
		t.Errorf("durable child = %+v, want closed durable record", got[0])
	}
	if got[1].ID != wisp.ID || got[1].Status != types.StatusOpen || got[1].StoragePlane != "wisp" {
		t.Errorf("wisp child = %+v, want open wisp record", got[1])
	}
}
