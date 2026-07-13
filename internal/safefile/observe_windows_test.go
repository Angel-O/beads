//go:build windows

package safefile

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestObserveMetadataNoFollowWindowsReturnsStoredCase(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "StoredCase")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	alternate := filepath.Join(parent, "storedcase")
	if _, err := os.Stat(alternate); err != nil {
		t.Skipf("test directory is case-sensitive: %v", err)
	}
	observation, err := ObserveMetadataNoFollow(alternate)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(observation.CanonicalPath) != "StoredCase" {
		t.Fatalf("canonical path = %q, want stored component case", observation.CanonicalPath)
	}
	if !observation.CaseSensitivityKnown || observation.CaseSensitive {
		t.Fatalf("case semantics known=%v sensitive=%v, want known case-insensitive", observation.CaseSensitivityKnown, observation.CaseSensitive)
	}
}

func TestObserveMetadataNoFollowWindowsResolvesAncestorJunction(t *testing.T) {
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "data"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	junction := filepath.Join(t.TempDir(), "junction")
	if output, err := exec.Command("cmd", "/c", "mklink", "/J", junction, target).CombinedOutput(); err != nil {
		t.Fatalf("create junction: %v (%s)", err, output)
	}
	observation, err := ObserveMetadataNoFollow(filepath.Join(junction, "data"))
	if err != nil {
		t.Fatal(err)
	}
	targetInfo, err := os.Stat(filepath.Join(target, "data"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(observation.Info, targetInfo) {
		t.Fatalf("ancestor-junction observation = %#v, want target identity", observation)
	}
}

func TestObserveMetadataNoFollowWindowsReportsCaseSensitiveDirectory(t *testing.T) {
	path := t.TempDir()
	if output, err := exec.Command("fsutil", "file", "setCaseSensitiveInfo", path, "enable").CombinedOutput(); err != nil {
		t.Skipf("per-directory case sensitivity unavailable: %v (%s)", err, strings.TrimSpace(string(output)))
	}
	observation, err := ObserveMetadataNoFollow(path)
	if err != nil {
		t.Fatal(err)
	}
	if !observation.CaseSensitivityKnown || !observation.CaseSensitive {
		t.Fatalf("case semantics known=%v sensitive=%v, want known case-sensitive", observation.CaseSensitivityKnown, observation.CaseSensitive)
	}
}

func TestWindowsCanonicalMetadataPathRejectsOversizedResult(t *testing.T) {
	getter := func(windows.Handle, *uint16, uint32, uint32) (uint32, error) {
		return maxWindowsCanonicalPathUTF16, nil
	}
	if _, err := windowsCanonicalPathWithGetter(0, getter); err == nil {
		t.Fatal("oversized canonical path was accepted")
	}
}

func TestWindowsCanonicalMetadataPathResizesBoundedly(t *testing.T) {
	calls := 0
	getter := func(_ windows.Handle, buffer *uint16, size uint32, _ uint32) (uint32, error) {
		calls++
		if calls == 1 {
			return size + 10, nil
		}
		value, err := windows.UTF16FromString(`\\?\C:\StoredCase`)
		if err != nil {
			return 0, err
		}
		if uint32(len(value)) > size {
			return 0, errors.New("test buffer remained too small")
		}
		copy(unsafe.Slice(buffer, size), value)
		return uint32(len(value) - 1), nil
	}
	got, err := windowsCanonicalPathWithGetter(0, getter)
	if err != nil || got != `C:\StoredCase` || calls != 2 {
		t.Fatalf("bounded canonical path = %q, err=%v, calls=%d", got, err, calls)
	}
}
