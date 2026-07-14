//go:build linux

package safefile

import (
	"os"
	"syscall"
)

func metadataLinkCount(_ *os.File, info os.FileInfo) (uint64, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return 0, false
	}
	return uint64(stat.Nlink), true //nolint:unconvert // Nlink is uint32 on supported non-amd64 Linux targets.
}
