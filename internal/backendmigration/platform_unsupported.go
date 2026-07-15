//go:build !linux

package backendmigration

import (
	"errors"

	"github.com/steveyegge/beads/internal/safefile"
)

func probeNativeLinux() (nativeLinux, wsl bool, err error) {
	return false, false, nil
}

func observeMetadataNoFollow(string) (*safefile.MetadataObservation, error) {
	return nil, errors.ErrUnsupported
}
