package upgrade_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const bundlePythonTestCount = 83

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
	executed := fmt.Sprintf("BUNDLE_TESTS_EXECUTED count=%d", bundlePythonTestCount)
	passed := fmt.Sprintf("BUNDLE_TESTS_PASSED count=%d", bundlePythonTestCount)
	if bundleExactOutputLineCount(output, executed) != 1 || bundleExactOutputLineCount(output, passed) != 1 {
		t.Fatalf("bundle primitive suite did not report exactly %d successful tests:\n%s", bundlePythonTestCount, output)
	}
}

func bundleExactOutputLineCount(output []byte, expected string) int {
	count := 0
	for _, line := range strings.Split(string(output), "\n") {
		if strings.TrimSuffix(line, "\r") == expected {
			count++
		}
	}
	return count
}
