//go:build !linux

package backendmigration

import (
	"path/filepath"
	"testing"
)

func TestInspectSourceShapeNonLinuxPrecedesEmbeddedBuildAndSourceAccess(t *testing.T) {
	request := validSelectionRequest(filepath.Join(t.TempDir(), "missing", ".beads"))
	candidate, err := InspectSourceShape(request)
	requireRefusal(t, candidate, err, CodePlatformUnsupported, ReasonOperatingSystem)
}
