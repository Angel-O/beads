package main

import (
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
)

func bulkHistoryInput(count int) string {
	var input strings.Builder
	for i := range count {
		fmt.Fprintf(&input, "bd-%04d\n", i)
	}
	return input.String()
}

func TestReadHistoryIDsBoundsAndDeduplicates(t *testing.T) {
	t.Run("accepts exact boundary", func(t *testing.T) {
		ids, err := readHistoryIDs("-", strings.NewReader(bulkHistoryInput(storage.MaxBulkHistoryIDs)))
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) != storage.MaxBulkHistoryIDs {
			t.Fatalf("IDs = %d", len(ids))
		}
	})

	t.Run("duplicates do not consume boundary", func(t *testing.T) {
		input := bulkHistoryInput(storage.MaxBulkHistoryIDs) + strings.Repeat("bd-0000\n", storage.MaxBulkHistoryIDs)
		ids, err := readHistoryIDs("-", strings.NewReader(input))
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) != storage.MaxBulkHistoryIDs {
			t.Fatalf("IDs = %d", len(ids))
		}
	})

	t.Run("rejects count above boundary", func(t *testing.T) {
		if _, err := readHistoryIDs("-", strings.NewReader(bulkHistoryInput(storage.MaxBulkHistoryIDs+1))); err == nil || !strings.Contains(err.Error(), "at most 1000") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("accepts exact ID length", func(t *testing.T) {
		id := strings.Repeat("x", storage.MaxBulkHistoryIDLength)
		ids, err := readHistoryIDs("-", strings.NewReader(id+"\n"))
		if err != nil || len(ids) != 1 || ids[0] != id {
			t.Fatalf("IDs = %#v, error = %v", ids, err)
		}
	})

	t.Run("rejects overlong ID", func(t *testing.T) {
		id := strings.Repeat("x", storage.MaxBulkHistoryIDLength+1)
		if _, err := readHistoryIDs("-", strings.NewReader(id+"\n")); err == nil || !strings.Contains(err.Error(), "max 255") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("propagates scanner errors", func(t *testing.T) {
		if _, err := readHistoryIDs("-", io.MultiReader(strings.NewReader("bd-one\n"), errorReader{})); err == nil || err.Error() != "read failed" {
			t.Fatalf("error = %v", err)
		}
	})
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, fmt.Errorf("read failed") }
