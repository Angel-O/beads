//go:build windows

package safefile

func validateMetadataPath(path string) error {
	return validateWindowsMetadataPath(path)
}
