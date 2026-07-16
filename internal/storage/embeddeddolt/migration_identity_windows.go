//go:build cgo && windows

package embeddeddolt

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func migrationDirectoryIdentity(handle *os.File) (string, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(handle.Fd()), &info); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x:%x:%x", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow), nil
}
