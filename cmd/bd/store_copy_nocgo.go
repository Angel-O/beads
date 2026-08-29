//go:build !cgo

package main

import (
	"context"
	"fmt"

	"github.com/steveyegge/beads/internal/storage"
)

func openOfflineStore(context.Context, string, bool) (storage.DoltStorage, error) {
	return nil, fmt.Errorf("offline store-copy requires a CGO build with embedded Dolt support")
}
