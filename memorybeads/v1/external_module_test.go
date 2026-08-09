package v1_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestExternalBackendModuleCompilesUnchanged is A1's out-of-tree source-
// compatibility gate. The fixture is its own Go module, imports only public
// Beads packages, and explicitly implements backend.DoltStorage without
// embedding that interface. Its static go.mod points back at this checkout;
// the child command may use the existing module cache but cannot use a module
// proxy or update the fixture's module files.
func TestExternalBackendModuleCompilesUnchanged(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	fixture := filepath.Join(filepath.Dir(thisFile), "testdata", "a1external")
	before := fixtureDigest(t, fixture)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-mod=readonly", "-count=1", "./...")
	cmd.Dir = fixture
	cmd.Env = cleanGoEnv(os.Environ(),
		"CGO_ENABLED=1",
		"GOFLAGS=-tags=gms_pure_go",
		"GONOSUMDB=*",
		"GOPROXY=off",
		"GOSUMDB=off",
		"GOTOOLCHAIN=local",
		"GOWORK=off",
	)
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("external module compile timed out: %v\n%s", ctx.Err(), output)
	}
	if err != nil {
		t.Fatalf("external module compile: %v\n%s", err, output)
	}
	if after := fixtureDigest(t, fixture); after != before {
		t.Fatalf("external module changed while compiling: before %s, after %s", before, after)
	}
}

func cleanGoEnv(inherited []string, additions ...string) []string {
	overridden := make(map[string]bool, len(additions))
	for _, item := range additions {
		key, _, _ := strings.Cut(item, "=")
		overridden[key] = true
	}
	out := make([]string, 0, len(inherited)+len(additions))
	for _, item := range inherited {
		key, _, _ := strings.Cut(item, "=")
		if !overridden[key] {
			out = append(out, item)
		}
	}
	return append(out, additions...)
}

func fixtureDigest(t *testing.T, root string) string {
	t.Helper()
	var paths []string
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			paths = append(paths, path)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk external module: %v", err)
	}
	sort.Strings(paths)
	digest := sha256.New()
	for _, path := range paths {
		relative, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatalf("relative external module path: %v", err)
		}
		if _, err := fmt.Fprintln(digest, relative); err != nil {
			t.Fatalf("hash external module path: %v", err)
		}
		file, err := os.Open(path)
		if err != nil {
			t.Fatalf("open external module file %s: %v", relative, err)
		}
		_, copyErr := io.Copy(digest, file)
		closeErr := file.Close()
		if copyErr != nil {
			t.Fatalf("hash external module file %s: %v", relative, copyErr)
		}
		if closeErr != nil {
			t.Fatalf("close external module file %s: %v", relative, closeErr)
		}
	}
	return fmt.Sprintf("%x", digest.Sum(nil))
}
