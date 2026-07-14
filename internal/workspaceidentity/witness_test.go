package workspaceidentity

import (
	"bytes"
	"errors"
	"math"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func makeWorkspace(t *testing.T, metadata []byte) string {
	t.Helper()
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "metadata.json"), metadata, 0o600); err != nil {
		t.Fatal(err)
	}
	return beadsDir
}

func TestWitnessRejectsPersistentRootReplacement(t *testing.T) {
	metadata := []byte(`{"backend":"dolt"}`)
	beadsDir := makeWorkspace(t, metadata)

	witness, _, err := BindExisting(beadsDir, MaxMetadataBytes)
	if !Supported() {
		if witness != nil || !errors.Is(err, ErrUnsupported) {
			t.Fatalf("unsupported witness = %#v, %v; want ErrUnsupported", witness, err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	defer witness.Close() //nolint:errcheck // RED-path test cleanup

	if err := os.Rename(beadsDir, beadsDir+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "metadata.json"), metadata, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := witness.Revalidate(); !errors.Is(err, ErrChanged) {
		t.Fatalf("workspace identity witness accepted a persistent root replacement: %v", err)
	}
}

func TestWitnessRejectsMetadataReplacementAndUnsafeLeaves(t *testing.T) {
	if !Supported() {
		return
	}
	metadata := []byte(`{"backend":"dolt"}`)

	t.Run("same-byte replacement", func(t *testing.T) {
		beadsDir := makeWorkspace(t, metadata)
		witness, _, err := BindExisting(beadsDir, MaxMetadataBytes)
		if err != nil {
			t.Fatal(err)
		}
		defer witness.Close() //nolint:errcheck // test cleanup
		path := filepath.Join(beadsDir, "metadata.json")
		replacement := filepath.Join(beadsDir, "replacement")
		if err := os.WriteFile(replacement, metadata, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(replacement, path); err != nil {
			t.Fatal(err)
		}
		if err := witness.Revalidate(); !errors.Is(err, ErrChanged) {
			t.Fatalf("same-byte replacement error = %v, want ErrChanged", err)
		}
	})

	t.Run("hard link", func(t *testing.T) {
		beadsDir := makeWorkspace(t, metadata)
		path := filepath.Join(beadsDir, "metadata.json")
		if err := os.Link(path, path+".alias"); err != nil {
			t.Skipf("hard links unavailable: %v", err)
		}
		witness, _, err := BindExisting(beadsDir, MaxMetadataBytes)
		if witness != nil || !errors.Is(err, ErrUnverifiable) {
			t.Fatalf("hard-linked witness = %#v, %v; want ErrUnverifiable", witness, err)
		}
	})

	t.Run("non-regular metadata", func(t *testing.T) {
		beadsDir := filepath.Join(t.TempDir(), ".beads")
		if err := os.MkdirAll(filepath.Join(beadsDir, "metadata.json"), 0o700); err != nil {
			t.Fatal(err)
		}
		witness, _, err := BindExisting(beadsDir, MaxMetadataBytes)
		if witness != nil || !errors.Is(err, ErrUnverifiable) {
			t.Fatalf("directory metadata witness = %#v, %v; want ErrUnverifiable", witness, err)
		}
	})

	t.Run("metadata symlink", func(t *testing.T) {
		beadsDir := makeWorkspace(t, metadata)
		path := filepath.Join(beadsDir, "metadata.json")
		target := filepath.Join(beadsDir, "metadata-target.json")
		if err := os.Rename(path, target); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
		witness, _, err := BindExisting(beadsDir, MaxMetadataBytes)
		if witness != nil || !errors.Is(err, ErrUnverifiable) {
			t.Fatalf("symlink metadata witness = %#v, %v; want ErrUnverifiable", witness, err)
		}
	})

	t.Run("root symlink", func(t *testing.T) {
		target := makeWorkspace(t, metadata)
		rootLink := filepath.Join(t.TempDir(), ".beads")
		if err := os.Symlink(target, rootLink); err != nil {
			t.Fatal(err)
		}
		witness, _, err := BindExisting(rootLink, MaxMetadataBytes)
		if witness != nil || !errors.Is(err, ErrUnverifiable) {
			t.Fatalf("symlink root witness = %#v, %v; want ErrUnverifiable", witness, err)
		}
	})

	t.Run("socket metadata", func(t *testing.T) {
		beadsDir := filepath.Join(t.TempDir(), ".beads")
		if err := os.Mkdir(beadsDir, 0o700); err != nil {
			t.Fatal(err)
		}
		listener, err := net.Listen("unix", filepath.Join(beadsDir, "metadata.json"))
		if err != nil {
			t.Skipf("unix sockets unavailable: %v", err)
		}
		defer listener.Close() //nolint:errcheck // test cleanup
		witness, _, err := BindExisting(beadsDir, MaxMetadataBytes)
		if witness != nil || !errors.Is(err, ErrUnverifiable) {
			t.Fatalf("socket metadata witness = %#v, %v; want ErrUnverifiable", witness, err)
		}
	})
}

func TestWitnessClassifiesPersistentByteDriftAndNameErrors(t *testing.T) {
	if !Supported() {
		return
	}

	t.Run("growth beyond retained bound is changed", func(t *testing.T) {
		metadata := []byte(`{"backend":"dolt"}`)
		beadsDir := makeWorkspace(t, metadata)
		witness, _, err := BindExisting(beadsDir, int64(len(metadata)))
		if err != nil {
			t.Fatal(err)
		}
		defer witness.Close() //nolint:errcheck // test cleanup
		if err := os.WriteFile(filepath.Join(beadsDir, "metadata.json"), append(metadata, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := witness.Revalidate(); !errors.Is(err, ErrChanged) {
			t.Fatalf("grown metadata revalidation = %v, want ErrChanged", err)
		}
	})

	t.Run("named path permission failure is unverifiable", func(t *testing.T) {
		beadsDir := makeWorkspace(t, []byte(`{"backend":"dolt"}`))
		witness, _, err := BindExisting(beadsDir, MaxMetadataBytes)
		if err != nil {
			t.Fatal(err)
		}
		defer witness.Close() //nolint:errcheck // test cleanup
		parent := filepath.Dir(beadsDir)
		if err := os.Chmod(parent, 0); err != nil {
			t.Fatal(err)
		}
		revalidateErr := witness.Revalidate()
		if err := os.Chmod(parent, 0o700); err != nil {
			t.Fatal(err)
		}
		if revalidateErr == nil {
			t.Skip("process privileges bypass directory search permissions")
		}
		if !errors.Is(revalidateErr, ErrUnverifiable) || errors.Is(revalidateErr, ErrChanged) {
			t.Fatalf("permission failure revalidation = %v, want only ErrUnverifiable", revalidateErr)
		}
	})
}

func TestWitnessCopiesBytesAndValidatesLimits(t *testing.T) {
	if !Supported() {
		return
	}
	metadata := []byte(`{"backend":"dolt"}`)
	beadsDir := makeWorkspace(t, metadata)
	witness, returned, err := BindExisting(beadsDir, MaxMetadataBytes)
	if err != nil {
		t.Fatal(err)
	}
	returned[0] ^= 0xff
	if err := witness.Revalidate(); err != nil {
		t.Fatalf("caller mutation changed private baseline: %v", err)
	}
	if err := witness.Close(); err != nil {
		t.Fatal(err)
	}
	if err := witness.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if err := witness.Revalidate(); !errors.Is(err, ErrClosed) {
		t.Fatalf("revalidate after close = %v, want ErrClosed", err)
	}

	for _, limit := range []int64{0, -1, MaxMetadataBytes + 1, math.MaxInt64} {
		witness, _, err := BindExisting(beadsDir, limit)
		if witness != nil || !errors.Is(err, ErrUnverifiable) {
			t.Fatalf("limit %d witness = %#v, %v; want ErrUnverifiable", limit, witness, err)
		}
	}
	witness, _, err = BindExisting(beadsDir, int64(len(metadata)-1))
	if witness != nil || !errors.Is(err, ErrUnverifiable) {
		t.Fatalf("over-limit witness = %#v, %v; want ErrUnverifiable", witness, err)
	}

	exactMaximum := bytes.Repeat([]byte{'x'}, int(MaxMetadataBytes))
	maximumDir := makeWorkspace(t, exactMaximum)
	witness, returned, err = BindExisting(maximumDir, MaxMetadataBytes)
	if err != nil || len(returned) != len(exactMaximum) {
		t.Fatalf("exact-maximum witness = %#v, %v, bytes=%d; want success with %d bytes", witness, err, len(returned), len(exactMaximum))
	}
	if err := witness.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(maximumDir, "metadata.json"), append(exactMaximum, 'x'), 0o600); err != nil {
		t.Fatal(err)
	}
	witness, _, err = BindExisting(maximumDir, MaxMetadataBytes)
	if witness != nil || !errors.Is(err, ErrUnverifiable) {
		t.Fatalf("maximum-plus-one witness = %#v, %v; want ErrUnverifiable", witness, err)
	}
}

func TestWitnessReleasesRetainedDescriptors(t *testing.T) {
	if !Supported() {
		return
	}
	beadsDir := makeWorkspace(t, []byte(`{"backend":"dolt"}`))
	countWorkspaceDescriptors := func() (int, error) {
		entries, err := os.ReadDir("/proc/self/fd")
		if err != nil {
			return 0, err
		}
		count := 0
		for _, entry := range entries {
			target, err := os.Readlink(filepath.Join("/proc/self/fd", entry.Name()))
			if err == nil && strings.HasPrefix(strings.TrimSuffix(target, " (deleted)"), beadsDir) {
				count++
			}
		}
		return count, nil
	}
	before, err := countWorkspaceDescriptors()
	if err != nil {
		t.Skipf("descriptor inspection unavailable: %v", err)
	}
	witness, _, err := BindExisting(beadsDir, MaxMetadataBytes)
	if err != nil {
		t.Fatal(err)
	}
	during, err := countWorkspaceDescriptors()
	if err != nil {
		t.Fatal(err)
	}
	if during != before+2 {
		t.Fatalf("workspace descriptors while bound = %d, want %d", during, before+2)
	}
	if err := witness.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := countWorkspaceDescriptors()
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("workspace descriptors after close = %d, want %d", after, before)
	}

	metadataPath := filepath.Join(beadsDir, "metadata.json")
	if err := os.Link(metadataPath, metadataPath+".alias"); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	for i := 0; i < 16; i++ {
		failed, _, bindErr := BindExisting(beadsDir, MaxMetadataBytes)
		if failed != nil || !errors.Is(bindErr, ErrUnverifiable) {
			t.Fatalf("failed bind %d = %#v, %v; want ErrUnverifiable", i, failed, bindErr)
		}
	}
	afterFailures, err := countWorkspaceDescriptors()
	if err != nil {
		t.Fatal(err)
	}
	if afterFailures != before {
		t.Fatalf("workspace descriptors after failed binds = %d, want %d", afterFailures, before)
	}
}

func TestWitnessUnsupportedBeforeFilesystemAccess(t *testing.T) {
	if Supported() {
		return
	}
	witness, data, err := BindExisting("invalid\x00workspace", MaxMetadataBytes)
	if witness != nil || data != nil || !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unsupported bind = %#v, %q, %v; want ErrUnsupported before path access", witness, data, err)
	}
}

func TestWitnessRejectsControlCharacterPaths(t *testing.T) {
	if !Supported() {
		return
	}
	for _, test := range []struct {
		name   string
		suffix string
	}{
		{name: "escape", suffix: "\x1b"},
		{name: "delete", suffix: "\x7f"},
		{name: "unicode C1", suffix: "\u0085"},
		{name: "invalid UTF-8 C1 byte", suffix: "\x85"},
	} {
		t.Run(test.name, func(t *testing.T) {
			beadsDir := filepath.Join(t.TempDir(), ".beads-"+test.suffix)
			if err := os.Mkdir(beadsDir, 0o700); err != nil {
				t.Fatal(err)
			}
			metadataPath := filepath.Join(beadsDir, "metadata.json")
			if err := os.WriteFile(metadataPath, []byte(`{"backend":"dolt"}`), 0o600); err != nil {
				t.Fatal(err)
			}

			witness, _, err := BindExisting(beadsDir, MaxMetadataBytes)
			if witness != nil {
				_ = witness.Close()
			}
			if witness != nil || !errors.Is(err, ErrUnverifiable) {
				t.Fatalf("control-path witness = %#v, %v; want ErrUnverifiable", witness, err)
			}
			if strings.Contains(err.Error(), test.suffix) {
				t.Fatalf("control-path error contains raw terminal control %q: %q", test.suffix, err)
			}
			if _, statErr := os.Stat(metadataPath); statErr != nil {
				t.Fatalf("control-path refusal changed the workspace: %v", statErr)
			}
		})
	}

	for _, test := range []struct {
		name   string
		suffix string
	}{
		{name: "cleaned escape component", suffix: "\x1b"},
		{name: "cleaned invalid UTF-8 component", suffix: "\x85"},
	} {
		t.Run(test.name, func(t *testing.T) {
			parent := t.TempDir()
			beadsDir := filepath.Join(parent, "workspace")
			if err := os.Mkdir(beadsDir, 0o700); err != nil {
				t.Fatal(err)
			}
			metadataPath := filepath.Join(beadsDir, "metadata.json")
			if err := os.WriteFile(metadataPath, []byte(`{"backend":"dolt"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			separator := string(filepath.Separator)
			rawPath := parent + separator + "discard-" + test.suffix + separator + ".." + separator + "workspace"
			witness, _, err := BindExisting(rawPath, MaxMetadataBytes)
			if witness != nil {
				_ = witness.Close()
			}
			if witness != nil || !errors.Is(err, ErrUnverifiable) {
				t.Fatalf("cleaned control-path witness = %#v, %v; want ErrUnverifiable", witness, err)
			}
			if strings.Contains(err.Error(), test.suffix) {
				t.Fatalf("cleaned control-path error contains raw terminal control %q: %q", test.suffix, err)
			}
			if _, statErr := os.Stat(metadataPath); statErr != nil {
				t.Fatalf("cleaned control-path refusal changed target workspace: %v", statErr)
			}
		})
	}

	for _, test := range []struct {
		name   string
		suffix string
	}{
		{name: "relative path from escape cwd", suffix: "\x1b"},
		{name: "relative path from invalid UTF-8 cwd", suffix: "\x85"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cwd := filepath.Join(t.TempDir(), "cwd-"+test.suffix)
			beadsDir := filepath.Join(cwd, ".beads")
			if err := os.MkdirAll(beadsDir, 0o700); err != nil {
				t.Fatal(err)
			}
			metadataPath := filepath.Join(beadsDir, "metadata.json")
			if err := os.WriteFile(metadataPath, []byte(`{"backend":"dolt"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Chdir(cwd)
			witness, _, err := BindExisting(".beads", MaxMetadataBytes)
			if witness != nil {
				_ = witness.Close()
			}
			if witness != nil || !errors.Is(err, ErrUnverifiable) {
				t.Fatalf("relative control-CWD witness = %#v, %v; want ErrUnverifiable", witness, err)
			}
			if strings.Contains(err.Error(), test.suffix) {
				t.Fatalf("relative control-CWD error contains raw terminal control %q: %q", test.suffix, err)
			}
			if _, statErr := os.Stat(metadataPath); statErr != nil {
				t.Fatalf("relative control-CWD refusal changed the workspace: %v", statErr)
			}
		})
	}
}

func TestWitnessErrorsSanitizeUntrustedPaths(t *testing.T) {
	untrusted := "workspace\x00\x1b\x7f\u0085"
	nested := &os.PathError{Op: "outer", Path: "secret-outer\x1b", Err: &os.PathError{
		Op: "inner", Path: "secret-inner\x00", Err: errors.New("denied"),
	}}
	err := witnessError(ErrUnverifiable, "inspect", untrusted, nested)
	message := err.Error()
	for _, raw := range []string{"\x00", "\x1b", "\x7f", "\u0085", "secret-outer", "secret-inner"} {
		if strings.Contains(message, raw) {
			t.Fatalf("sanitized error contains raw control/path %q: %q", raw, message)
		}
	}
	for _, escaped := range []string{`\x00`, `\x1b`, `\x7f`, `\u0085`} {
		if !strings.Contains(message, escaped) {
			t.Fatalf("sanitized error %q does not quote %q", message, escaped)
		}
	}
	if !errors.Is(err, ErrUnverifiable) {
		t.Fatalf("sanitized error = %v, want ErrUnverifiable", err)
	}
}

func TestWitnessCloseSerializesWithRevalidate(t *testing.T) {
	if !Supported() {
		return
	}
	witness, _, err := BindExisting(makeWorkspace(t, []byte(`{"backend":"dolt"}`)), MaxMetadataBytes)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(2)
	results := make(chan error, 2)
	go func() {
		defer wait.Done()
		<-start
		results <- witness.Revalidate()
	}()
	go func() {
		defer wait.Done()
		<-start
		results <- witness.Close()
	}()
	close(start)
	wait.Wait()
	close(results)
	for result := range results {
		if result != nil && !errors.Is(result, ErrClosed) {
			t.Fatalf("concurrent lifecycle result = %v, want nil or ErrClosed", result)
		}
	}
}
