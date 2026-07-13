//go:build !linux && !android && !darwin && !ios && !windows

package safefile

import "errors"

func observeMetadataNoFollow(string) (*MetadataObservation, error) {
	return nil, errors.ErrUnsupported
}
