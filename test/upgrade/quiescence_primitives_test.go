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

const quiescencePythonTestCount = 21

func TestQuiescencePrimitivesPythonSuite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("quiescence leases require POSIX descriptor and flock support")
	}
	_, file, _, _ := runtime.Caller(0)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	isolation := t.TempDir()
	home := filepath.Join(isolation, "home")
	config := filepath.Join(isolation, "config")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(config, 0o700); err != nil {
		t.Fatal(err)
	}

	command := exec.Command("python3", "-B", filepath.Join(repoRoot, "test", "upgrade", "testdata", "quiescence_primitives_test.py"))
	command.Env = quiescenceTestEnvironment(os.Environ(), home, config, filepath.Join(repoRoot, "scripts"))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("quiescence primitive suite failed: %v\n%s", err, output)
	}
	executed := fmt.Sprintf("QUIESCENCE_TESTS_EXECUTED count=%d", quiescencePythonTestCount)
	passed := fmt.Sprintf("QUIESCENCE_TESTS_PASSED count=%d", quiescencePythonTestCount)
	if exactOutputLineCount(output, executed) != 1 || exactOutputLineCount(output, passed) != 1 {
		t.Fatalf("quiescence primitive suite did not report exactly %d successful tests:\n%s", quiescencePythonTestCount, output)
	}
}

func exactOutputLineCount(output []byte, expected string) int {
	count := 0
	for _, line := range strings.Split(string(output), "\n") {
		if strings.TrimSuffix(line, "\r") == expected {
			count++
		}
	}
	return count
}

func quiescenceTestEnvironment(base []string, home, config, pythonPath string) []string {
	environment := make([]string, 0, len(base)+6)
	for _, entry := range base {
		name, _, _ := strings.Cut(entry, "=")
		if name == "HOME" || name == "XDG_CONFIG_HOME" || name == "PYTHONPATH" || strings.HasPrefix(name, "GIT_") {
			continue
		}
		environment = append(environment, entry)
	}
	return append(environment,
		"HOME="+home,
		"XDG_CONFIG_HOME="+config,
		"PYTHONDONTWRITEBYTECODE=1",
		"PYTHONPATH="+pythonPath,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
	)
}
