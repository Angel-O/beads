//go:build linux

package safefile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

const linuxCasefoldFlag = 0x40000000

func observeMetadataNoFollow(path string) (*MetadataObservation, error) {
	file, err := OpenMetadataNoFollow(path)
	if err != nil {
		return nil, err
	}
	return observeOpenedMetadata(file, inspectLinuxMetadataHandle)
}

func inspectLinuxMetadataHandle(file *os.File, info os.FileInfo) (string, bool, bool, error) {
	procPath := fmt.Sprintf("/proc/self/fd/%d", file.Fd())
	var procFilesystem unix.Statfs_t
	if err := unix.Statfs("/proc/self/fd", &procFilesystem); err != nil {
		return "", true, false, err
	}
	if uint64(procFilesystem.Type) != unix.PROC_SUPER_MAGIC {
		return "", true, false, errors.New("/proc/self/fd is not procfs")
	}
	canonicalPath, err := os.Readlink(procPath)
	if err != nil {
		return "", true, false, err
	}
	if !filepath.IsAbs(canonicalPath) {
		return "", true, false, errors.New("metadata handle has no stable absolute pathname")
	}
	namedInfo, err := os.Stat(canonicalPath)
	if err != nil {
		return "", true, false, err
	}
	if !os.SameFile(info, namedInfo) {
		return "", true, false, errors.New("canonical pathname no longer names the observed object")
	}
	if !info.IsDir() {
		return filepath.Clean(canonicalPath), true, false, nil
	}
	caseSensitive, known := linuxDirectoryCaseSensitivity(file)
	return filepath.Clean(canonicalPath), caseSensitive, known, nil
}

func linuxDirectoryCaseSensitivity(file *os.File) (bool, bool) {
	flags, err := unix.IoctlGetInt(int(file.Fd()), unix.FS_IOC_GETFLAGS)
	if err != nil {
		return true, false
	}
	return flags&linuxCasefoldFlag == 0, true
}
