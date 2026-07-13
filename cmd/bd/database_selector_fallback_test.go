//go:build !linux && !android && !darwin && !ios && !windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDatabaseSelectorFailsClosedWithoutMetadataOnlyHandle(t *testing.T) {
	selector := filepath.Join(t.TempDir(), "beads.db")
	if err := os.WriteFile(selector, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := validatedDatabaseSelectorPath(selector)
	if got != "" || !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("unsupported selector = %q, err=%v, want errors.ErrUnsupported", got, err)
	}
}
