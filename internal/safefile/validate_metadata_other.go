//go:build !windows

package safefile

func validateMetadataPath(string) error {
	return nil
}
