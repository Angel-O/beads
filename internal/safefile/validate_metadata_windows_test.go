//go:build windows

package safefile

import "testing"

func TestValidateMetadataPathAppliesWindowsPreflightWithoutOpening(t *testing.T) {
	for _, path := range []string{
		`\\.\NUL`,
		`\\server\pipe\name`,
		`C:\workspace\NUL:$DATA`,
		`C:\workspace\COM¹`,
	} {
		if err := ValidateMetadataPath(path); err == nil {
			t.Fatalf("unsafe metadata path %q was accepted", path)
		}
	}
	for _, path := range []string{
		`C:\workspace\beads.db`,
		`\\server\share\beads.db`,
	} {
		if err := ValidateMetadataPath(path); err != nil {
			t.Fatalf("ordinary metadata path %q: %v", path, err)
		}
	}
}
