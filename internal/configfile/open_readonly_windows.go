//go:build windows

package configfile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func openReadOnlyConfigFile(path string) (*os.File, error) {
	extendedPath, err := extendedWindowsPath(path)
	if err != nil {
		return nil, err
	}
	pathPtr, err := windows.UTF16PtrFromString(extendedPath)
	if err != nil {
		return nil, err
	}
	// Open the final reparse point itself so a replacement symlink is rejected
	// from handle metadata rather than followed to its target.
	handle, err := windows.CreateFile(
		pathPtr,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS,
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
		return closeOnError(errors.New("config handle is not a disk file"))
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return closeOnError(err)
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return closeOnError(errors.New("config handle is a reparse point"))
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		return closeOnError(errors.New("could not wrap config file handle"))
	}
	return file, nil
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
