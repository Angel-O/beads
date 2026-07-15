//go:build linux && !cgo

package backendmigration

import (
	"path/filepath"
	"testing"
)

func TestInspectSourceShapeNoCGORefusesBeforeSourceAccess(t *testing.T) {
	_, wsl, err := probeNativeLinux()
	if err != nil || wsl {
		t.Skipf("requires native non-WSL Linux: wsl=%v err=%v", wsl, err)
	}
	request := validSelectionRequest(filepath.Join(t.TempDir(), "missing", ".beads"))
	candidate, err := InspectSourceShape(request)
	requireRefusal(t, candidate, err, CodePlatformUnsupported, ReasonEmbeddedBuild)
}
