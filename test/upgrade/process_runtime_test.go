package upgrade_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const processRuntimePythonTestCount = 42
const processRuntimePythonSuiteTimeout = 60 * time.Second

func TestProcessRuntimePythonSuite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process runtime requires POSIX sessions and process groups")
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

	commandContext, cancel := context.WithTimeout(
		context.Background(),
		processRuntimePythonSuiteTimeout,
	)
	defer cancel()
	command := exec.CommandContext(
		commandContext,
		"python3",
		"-B",
		filepath.Join(
			repoRoot,
			"test",
			"upgrade",
			"testdata",
			"process_runtime_test.py",
		),
	)
	command.WaitDelay = 2 * time.Second
	command.Env = processRuntimeTestEnvironment(
		os.Environ(),
		home,
		config,
		filepath.Join(repoRoot, "scripts"),
	)
	output, err := command.CombinedOutput()
	if commandContext.Err() == context.DeadlineExceeded {
		t.Fatalf(
			"process runtime suite timed out after %s\n%s",
			processRuntimePythonSuiteTimeout,
			output,
		)
	}
	if err != nil {
		t.Fatalf("process runtime suite failed: %v\n%s", err, output)
	}
	executed := fmt.Sprintf(
		"PROCESS_RUNTIME_TESTS_EXECUTED stage=all count=%d",
		processRuntimePythonTestCount,
	)
	passed := fmt.Sprintf(
		"PROCESS_RUNTIME_TESTS_PASSED stage=all count=%d",
		processRuntimePythonTestCount,
	)
	if processRuntimeExactOutputLineCount(output, executed) != 1 ||
		processRuntimeExactOutputLineCount(output, passed) != 1 {
		t.Fatalf(
			"process runtime suite did not report exactly %d successful tests:\n%s",
			processRuntimePythonTestCount,
			output,
		)
	}
}

func processRuntimeExactOutputLineCount(output []byte, expected string) int {
	count := 0
	for _, line := range strings.Split(string(output), "\n") {
		if strings.TrimSuffix(line, "\r") == expected {
			count++
		}
	}
	return count
}

func processRuntimeTestEnvironment(base []string, home, config, pythonPath string) []string {
	environment := make([]string, 0, len(base)+7)
	for _, entry := range base {
		name, _, _ := strings.Cut(entry, "=")
		if name == "HOME" ||
			name == "XDG_CONFIG_HOME" ||
			name == "PYTHONHOME" ||
			name == "PYTHONINSPECT" ||
			name == "PYTHONPATH" ||
			name == "PYTHONSTARTUP" ||
			name == "PYTHONUSERBASE" ||
			name == "PYTHONWARNINGS" ||
			name == "PROCESS_RUNTIME_TEST_STAGE" ||
			strings.HasPrefix(name, "GIT_") {
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
		"PROCESS_RUNTIME_TEST_STAGE=all",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
	)
}
