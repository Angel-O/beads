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
	if share, ok := windowsUNCShare(cleanedWindowsNamespaceComponent(upper)); ok && (share == "PIPE" || share == "MAILSLOT") {
		return errors.New("metadata path uses an IPC device namespace")
	}
	if !strings.HasPrefix(upper, `\\?\`) {
		return validateWindowsMetadataComponents(abs)
	}
	remainder := strings.TrimPrefix(upper, `\\?\`)
	if strings.HasPrefix(remainder, `UNC\`) || strings.HasPrefix(remainder, `VOLUME{`) {
		return validateWindowsMetadataComponents(abs)
	}
	if len(remainder) >= 3 && remainder[1] == ':' && remainder[2] == '\\' {
		return validateWindowsMetadataComponents(abs)
	}
	return errors.New("metadata path uses an unsupported device namespace")
}

func validateWindowsMetadataComponents(path string) error {
	volume := filepath.VolumeName(path)
	remainder := strings.TrimPrefix(path, volume)
	for _, component := range strings.FieldsFunc(remainder, func(r rune) bool { return r == '\\' || r == '/' }) {
		if strings.ContainsRune(component, ':') {
			return errors.New("metadata path uses an alternate data stream")
		}
		if isReservedWindowsMetadataComponent(component) {
			return errors.New("metadata path contains a reserved DOS device component")
		}
	}
	return nil
}

func isReservedWindowsMetadataComponent(component string) bool {
	base := component
	if index := strings.IndexAny(base, ".:"); index >= 0 {
		base = base[:index]
	}
	base = strings.TrimRight(base, " ")
	upper := strings.ToUpper(base)
	switch upper {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$", "CLOCK$":
		return true
	}
	var suffix string
	switch {
	case strings.HasPrefix(upper, "COM"):
		suffix = strings.TrimPrefix(upper, "COM")
	case strings.HasPrefix(upper, "LPT"):
		suffix = strings.TrimPrefix(upper, "LPT")
	default:
		return false
	}
	return suffix == "1" || suffix == "2" || suffix == "3" || suffix == "4" || suffix == "5" ||
		suffix == "6" || suffix == "7" || suffix == "8" || suffix == "9" ||
		suffix == "¹" || suffix == "²" || suffix == "³"
}

func cleanedWindowsNamespaceComponent(path string) string {
	parts := strings.Split(path, `\`)
	for index := range parts {
		parts[index] = strings.TrimRight(parts[index], " .")
	}
	return strings.Join(parts, `\`)
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
