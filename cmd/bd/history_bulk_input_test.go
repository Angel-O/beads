package main

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/issueops"
)

func TestPositionalHistoryInputNormalization(t *testing.T) {
	ids, err := issueops.NormalizeBulkHistoryIDs([]string{" bd-two ", "bd-one", "bd-two", "", "   "})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"bd-one", "bd-two"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("normalized IDs = %#v, want %#v", ids, want)
	}
}

func TestPositionalHistoryInputBounds(t *testing.T) {
	t.Run("accepts exact boundary", func(t *testing.T) {
		ids := make([]string, storage.MaxBulkHistoryIDs)
		for i := range ids {
			ids[i] = fmt.Sprintf("bd-%04d", i)
		}
		if _, err := issueops.NormalizeBulkHistoryIDs(ids); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("rejects over limit", func(t *testing.T) {
		ids := make([]string, storage.MaxBulkHistoryIDs+1)
		for i := range ids {
			ids[i] = fmt.Sprintf("bd-%04d", i)
		}
		if _, err := issueops.NormalizeBulkHistoryIDs(ids); err == nil || !strings.Contains(err.Error(), "at most 1000") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("rejects overlong ID", func(t *testing.T) {
		id := strings.Repeat("x", storage.MaxBulkHistoryIDLength+1)
		if _, err := issueops.NormalizeBulkHistoryIDs([]string{id}); err == nil || !strings.Contains(err.Error(), "max 255") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestRunBulkHistoryUsesOnePositionalBatch(t *testing.T) {
	backend := &recordingBulkHistoryBackend{}
	ids := []string{"bd-two", "bd-one", "bd-two"}
	if err := runBulkHistory(context.Background(), backend, ids, 0); err != nil {
		t.Fatal(err)
	}
	if backend.calls != 1 {
		t.Fatalf("BulkHistory calls = %d, want 1", backend.calls)
	}
	if !reflect.DeepEqual(backend.ids, ids) {
		t.Fatalf("BulkHistory IDs = %#v, want %#v", backend.ids, ids)
	}
}

type recordingBulkHistoryBackend struct {
	calls int
	ids   []string
}

func (b *recordingBulkHistoryBackend) BulkHistory(_ context.Context, ids []string) ([]storage.IssueHistory, error) {
	b.calls++
	b.ids = append([]string(nil), ids...)
	return []storage.IssueHistory{}, nil
}
