//go:build darwin || ios

package safefile

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/unix"
)

const darwinPathconfCaseSensitive = 11

func observeMetadataNoFollow(path string) (*MetadataObservation, error) {
	file, err := OpenMetadataNoFollow(path)
	if err != nil {
		return nil, err
	}
	return observeOpenedMetadata(file, inspectDarwinMetadataHandle)
}

func inspectDarwinMetadataHandle(file *os.File, info os.FileInfo) (string, bool, bool, error) {
	buffer := make([]byte, unix.PathMax)
	_, _, errno := unix.Syscall(unix.SYS_FCNTL, file.Fd(), uintptr(unix.F_GETPATH), uintptr(unsafe.Pointer(&buffer[0])))
	if errno != 0 {
		return "", true, false, errno
	}
	end := bytes.IndexByte(buffer, 0)
	if end < 0 {
		return "", true, false, errors.New("canonical path is not null-terminated")
	}
	canonicalPath := filepath.Clean(string(buffer[:end]))
	if !info.IsDir() {
		return canonicalPath, true, false, nil
	}
	caseSensitive, err := unix.Fpathconf(int(file.Fd()), darwinPathconfCaseSensitive)
	if err != nil {
		return canonicalPath, true, false, nil
	}
	sensitive, known := interpretDarwinCaseSensitivity(caseSensitive)
	return canonicalPath, sensitive, known, nil
}

func interpretDarwinCaseSensitivity(value int) (sensitive, known bool) {
	switch value {
	case 0:
		return false, true
	case 1:
		return true, true
	default:
		return true, false
	}
}
