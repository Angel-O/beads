//go:build !darwin && !ios && !linux && !windows

package safefile

import "os"

func metadataLinkCount(*os.File, os.FileInfo) (uint64, bool) {
	return 0, false
}
