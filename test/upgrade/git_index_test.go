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

const gitIndexPythonTestCount = 13
const gitIndexPythonSuiteTimeout = 30 * time.Second

func TestGitIndexCertificationPythonSuite(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	isolation := t.TempDir()
	home := filepath.Join(isolation, "home")
	config := filepath.Join(isolation, "config")
	temporary := filepath.Join(isolation, "tmp")
	for _, directory := range []string{home, config, temporary} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	commandContext, cancel := context.WithTimeout(
		context.Background(),
		gitIndexPythonSuiteTimeout,
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
			"git_index_test.py",
		),
		"--stage",
		"all",
	)
	command.WaitDelay = 2 * time.Second
	command.Env = gitIndexTestEnvironment(
		os.Environ(),
		home,
		config,
		temporary,
		filepath.Join(repoRoot, "scripts"),
	)
	output, err := command.CombinedOutput()
	if commandContext.Err() == context.DeadlineExceeded {
		t.Fatalf(
			"Git index certification suite timed out after %s\n%s",
			gitIndexPythonSuiteTimeout,
			output,
		)
	}
	if err != nil {
		t.Fatalf("Git index certification suite failed: %v\n%s", err, output)
	}
	executed := fmt.Sprintf(
		"GIT_INDEX_TESTS_EXECUTED stage=all count=%d",
		gitIndexPythonTestCount,
	)
	passed := fmt.Sprintf(
		"GIT_INDEX_TESTS_PASSED stage=all count=%d",
		gitIndexPythonTestCount,
	)
	if gitIndexExactOutputLineCount(output, executed) != 1 ||
		gitIndexExactOutputLineCount(output, passed) != 1 {
		t.Fatalf(
			"Git index certification suite did not report exactly %d successful tests:\n%s",
			gitIndexPythonTestCount,
			output,
		)
	}
}

func gitIndexExactOutputLineCount(output []byte, expected string) int {
	count := 0
	for _, line := range strings.Split(string(output), "\n") {
		if strings.TrimSuffix(line, "\r") == expected {
			count++
		}
	}
	return count
}

func gitIndexTestEnvironment(
	base []string,
	home string,
	config string,
	temporary string,
	pythonPath string,
) []string {
	environment := make([]string, 0, len(base)+7)
	for _, entry := range base {
		name, _, _ := strings.Cut(entry, "=")
		if name == "HOME" ||
			name == "XDG_CONFIG_HOME" ||
			name == "TMPDIR" ||
			strings.HasPrefix(name, "PYTHON") ||
			strings.HasPrefix(name, "GIT_") {
			continue
		}
		environment = append(environment, entry)
	}
	return append(
		environment,
		"HOME="+home,
		"XDG_CONFIG_HOME="+config,
		"TMPDIR="+temporary,
		"PYTHONDONTWRITEBYTECODE=1",
		"PYTHONPATH="+pythonPath,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
	)
}
