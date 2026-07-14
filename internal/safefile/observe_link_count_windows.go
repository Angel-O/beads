//go:build windows

package safefile

import (
	"os"

	"golang.org/x/sys/windows"
)

type windowsFileInformationGetter func(windows.Handle, *windows.ByHandleFileInformation) error

func metadataLinkCount(file *os.File, _ os.FileInfo) (uint64, bool) {
	if file == nil {
		return 0, false
	}
	return windowsMetadataLinkCountWithGetter(windows.Handle(file.Fd()), windows.GetFileInformationByHandle)
}

func windowsMetadataLinkCountWithGetter(handle windows.Handle, getter windowsFileInformationGetter) (uint64, bool) {
	if getter == nil {
		return 0, false
	}
	var info windows.ByHandleFileInformation
	if err := getter(handle, &info); err != nil {
		return 0, false
	}
	return uint64(info.NumberOfLinks), true
}
