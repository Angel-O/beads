//go:build windows

package safefile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func openReadOnly(path string, noFollow bool) (*os.File, error) {
	return openWindowsPath(path, windows.GENERIC_READ, noFollow, "read-only")
}

func openMetadataNoFollow(path string) (*os.File, error) {
	if err := validateWindowsMetadataPath(path); err != nil {
		return nil, err
	}
	return openWindowsPath(path, windows.FILE_READ_ATTRIBUTES, true, "metadata")
}

func openWindowsPath(path string, desiredAccess uint32, noFollow bool, kind string) (*os.File, error) {
	extendedPath, err := extendedWindowsPath(path)
	if err != nil {
		return nil, err
	}
	pathPtr, err := windows.UTF16PtrFromString(extendedPath)
	if err != nil {
		return nil, err
	}
	flags := uint32(windows.FILE_FLAG_BACKUP_SEMANTICS)
	if noFollow {
		// Open the final reparse point itself so callers can reject it rather
		// than consuming a replacement symlink target.
		flags |= windows.FILE_FLAG_OPEN_REPARSE_POINT
	}
	handle, err := windows.CreateFile(
		pathPtr,
		desiredAccess,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		flags,
		0,
	)
	if err != nil {
		return nil, err
	}
	closeOnError := func(err error) (*os.File, error) {
		_ = windows.CloseHandle(handle)
		return nil, err
	}
	fileType, err := windows.GetFileType(handle)
	if err != nil {
		return closeOnError(err)
	}
	if fileType != windows.FILE_TYPE_DISK {
		return closeOnError(errors.New(kind + " handle is not a disk file"))
	}
	if noFollow {
		var info windows.ByHandleFileInformation
		if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
			return closeOnError(err)
		}
		if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return closeOnError(errors.New(kind + " handle is a reparse point"))
		}
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		return closeOnError(errors.New("could not wrap " + kind + " file handle"))
	}
	return file, nil
}

func validateWindowsMetadataPath(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	upper := strings.ToUpper(filepath.Clean(abs))
	if strings.HasPrefix(upper, `\\.\`) || strings.HasPrefix(upper, `\\?\GLOBALROOT\`) {
		return errors.New("metadata path uses a raw device namespace")
	}
	if share, ok := windowsUNCShare(upper); ok && (share == "PIPE" || share == "MAILSLOT") {
		return errors.New("metadata path uses an IPC device namespace")
	}
	if !strings.HasPrefix(upper, `\\?\`) {
		return nil
	}
	remainder := strings.TrimPrefix(upper, `\\?\`)
	if strings.HasPrefix(remainder, `UNC\`) || strings.HasPrefix(remainder, `VOLUME{`) {
		return nil
	}
	if len(remainder) >= 3 && remainder[1] == ':' && remainder[2] == '\\' {
		return nil
	}
	return errors.New("metadata path uses an unsupported device namespace")
}

func windowsUNCShare(path string) (string, bool) {
	if strings.HasPrefix(path, `\\?\UNC\`) {
		path = `\\` + strings.TrimPrefix(path, `\\?\UNC\`)
	}
	if !strings.HasPrefix(path, `\\`) {
		return "", false
	}
	parts := strings.Split(strings.TrimPrefix(path, `\\`), `\`)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

func extendedWindowsPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(abs, `\\?\`) {
		return abs, nil
	}
	if strings.HasPrefix(abs, `\\`) {
		return `\\?\UNC\` + strings.TrimPrefix(abs, `\\`), nil
	}
	return `\\?\` + abs, nil
}
