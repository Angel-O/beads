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

const bundleQuiescenceCompositionPythonTestCount = 2

func TestBundleQuiescenceCompositionPythonSuite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bundle and quiescence composition requires POSIX dirfd and flock support")
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

	command := exec.Command(
		"python3",
		"-B",
		filepath.Join(
			repoRoot,
			"test",
			"upgrade",
			"testdata",
			"bundle_quiescence_composition_test.py",
		),
	)
	command.Env = bundleQuiescenceCompositionTestEnvironment(
		os.Environ(),
		home,
		config,
		filepath.Join(repoRoot, "scripts"),
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("bundle/quiescence composition suite failed: %v\n%s", err, output)
	}
	executed := fmt.Sprintf(
		"BUNDLE_QUIESCENCE_COMPOSITION_TESTS_EXECUTED count=%d",
		bundleQuiescenceCompositionPythonTestCount,
	)
	passed := fmt.Sprintf(
		"BUNDLE_QUIESCENCE_COMPOSITION_TESTS_PASSED count=%d",
		bundleQuiescenceCompositionPythonTestCount,
	)
	if bundleQuiescenceCompositionExactOutputLineCount(output, executed) != 1 ||
		bundleQuiescenceCompositionExactOutputLineCount(output, passed) != 1 {
		t.Fatalf(
			"bundle/quiescence composition suite did not report exactly %d successful tests:\n%s",
			bundleQuiescenceCompositionPythonTestCount,
			output,
		)
	}
}

func bundleQuiescenceCompositionExactOutputLineCount(output []byte, expected string) int {
	count := 0
	for _, line := range strings.Split(string(output), "\n") {
		if strings.TrimSuffix(line, "\r") == expected {
			count++
		}
	}
	return count
}

func bundleQuiescenceCompositionTestEnvironment(base []string, home, config, pythonPath string) []string {
	environment := make([]string, 0, len(base)+6)
	for _, entry := range base {
		name, _, _ := strings.Cut(entry, "=")
		if name == "HOME" || name == "XDG_CONFIG_HOME" || name == "PYTHONPATH" || strings.HasPrefix(name, "GIT_") {
			continue
		}
		environment = append(environment, entry)
	}
	return append(
		environment,
		"HOME="+home,
		"XDG_CONFIG_HOME="+config,
		"PYTHONDONTWRITEBYTECODE=1",
		"PYTHONPATH="+pythonPath,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
	)
}
