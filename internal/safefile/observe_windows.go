//go:build windows

package safefile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const maxWindowsCanonicalPathUTF16 = 32768

type windowsFinalPathGetter func(windows.Handle, *uint16, uint32, uint32) (uint32, error)

func observeMetadataNoFollow(path string) (*MetadataObservation, error) {
	file, err := OpenMetadataNoFollow(path)
	if err != nil {
		return nil, err
	}
	return observeOpenedMetadata(file, inspectWindowsMetadataHandle)
}

func inspectWindowsMetadataHandle(file *os.File, info os.FileInfo) (string, bool, bool, error) {
	canonicalPath, err := windowsCanonicalPathWithGetter(windows.Handle(file.Fd()), windows.GetFinalPathNameByHandle)
	if err != nil {
		return "", true, false, err
	}
	if !info.IsDir() {
		return canonicalPath, true, false, nil
	}
	type fileCaseSensitiveInfo struct {
		Flags uint32
	}
	var caseInfo fileCaseSensitiveInfo
	err = windows.GetFileInformationByHandleEx(
		windows.Handle(file.Fd()),
		windows.FileCaseSensitiveInfo,
		(*byte)(unsafe.Pointer(&caseInfo)),
		uint32(unsafe.Sizeof(caseInfo)),
	)
	if err != nil {
		return canonicalPath, true, false, nil
	}
	return canonicalPath, caseInfo.Flags&windows.FILE_CS_FLAG_CASE_SENSITIVE_DIR != 0, true, nil
}

func windowsCanonicalPathWithGetter(handle windows.Handle, getter windowsFinalPathGetter) (string, error) {
	buffer := make([]uint16, 256)
	for {
		length, err := getter(handle, &buffer[0], uint32(len(buffer)), 0)
		if err != nil {
			return "", err
		}
		if length < uint32(len(buffer)) {
			path := windows.UTF16ToString(buffer[:length])
			switch {
			case strings.HasPrefix(path, `\\?\UNC\`):
				path = `\\` + strings.TrimPrefix(path, `\\?\UNC\`)
			case strings.HasPrefix(path, `\\?\`) && len(path) >= 7 && path[5] == ':':
				path = strings.TrimPrefix(path, `\\?\`)
			}
			return normalizeWindowsCanonicalVolume(filepath.Clean(path)), nil
		}
		if length >= maxWindowsCanonicalPathUTF16 {
			return "", errors.New("canonical Windows path exceeds UTF-16 limit")
		}
		buffer = make([]uint16, int(length)+1)
	}
}

func normalizeWindowsCanonicalVolume(path string) string {
	volume := filepath.VolumeName(path)
	if volume == "" {
		return path
	}
	return strings.ToUpper(volume) + path[len(volume):]
}
