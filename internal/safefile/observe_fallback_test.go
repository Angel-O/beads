//go:build !linux && !android && !darwin && !ios && !windows

package safefile

import (
	"errors"
	"testing"
)

func TestObserveMetadataNoFollowIsUnsupportedWithoutCanonicalMetadataHandle(t *testing.T) {
	observation, err := ObserveMetadataNoFollow("ignored")
	if observation != nil || !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("fallback observation = %#v, err=%v, want errors.ErrUnsupported", observation, err)
	}
}
