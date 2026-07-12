package upgrade_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBundlePrimitivesPythonSuite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bundle preservation primitives require POSIX dirfd and flock support")
	}
	_, file, _, _ := runtime.Caller(0)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	command := exec.Command("python3", filepath.Join(repoRoot, "test", "upgrade", "testdata", "bundle_primitives_test.py"))
	command.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1", "PYTHONPATH="+filepath.Join(repoRoot, "scripts"))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("bundle primitive suite failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "OK") {
		t.Fatalf("bundle primitive suite did not report success:\n%s", output)
	}
}
