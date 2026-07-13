//go:build !windows

package safefile

import "testing"

func TestValidateMetadataPathIsNoOpOutsideWindows(t *testing.T) {
	if err := ValidateMetadataPath("any lexical path"); err != nil {
		t.Fatalf("non-Windows metadata preflight: %v", err)
	}
}
