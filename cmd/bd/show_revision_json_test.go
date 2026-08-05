package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func TestProjectShowJSONDetailsExposesRevision(t *testing.T) {
	details := &types.IssueDetails{Issue: types.Issue{
		ID:         "be-revision",
		Title:      "Versioned issue",
		RowVersion: 123456789,
	}}

	data, err := json.Marshal(projectShowJSONDetails(details))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(data)
	if !strings.Contains(js, `"revision":123456789`) {
		t.Errorf("expected revision token in show JSON, got: %s", js)
	}
	for _, forbidden := range []string{"row_version", "RowVersion", "row_lock"} {
		if strings.Contains(js, forbidden) {
			t.Errorf("show JSON leaked storage field %q: %s", forbidden, js)
		}
	}
}
