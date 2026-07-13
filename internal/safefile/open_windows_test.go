//go:build windows

package safefile

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsReadOnlyNoFollowRejectsSymlink(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("target data"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	file, err := OpenReadOnly(link)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	data, readErr := io.ReadAll(file)
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || string(data) != "target data" {
		t.Fatalf("followed read got %q readErr=%v closeErr=%v", data, readErr, closeErr)
	}

	file, err = OpenReadOnlyNoFollow(link)
	if err == nil || file != nil {
		if file != nil {
			_ = file.Close()
		}
		t.Fatalf("no-follow open got file=%v err=%v, want rejection", file, err)
	}
	if !strings.Contains(err.Error(), "reparse point") {
		t.Fatalf("no-follow error = %v, want reparse-point rejection", err)
	}
}

func TestWindowsMetadataNoFollowSupportsDiskObjects(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("target data"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{target, root} {
		file, err := OpenMetadataNoFollow(path)
		if err != nil {
			t.Fatalf("metadata open %q: %v", path, err)
		}
		if _, err := file.Stat(); err != nil {
			_ = file.Close()
			t.Fatalf("metadata stat %q: %v", path, err)
		}
		if err := file.Close(); err != nil {
			t.Fatalf("metadata close %q: %v", path, err)
		}
	}
}

func TestWindowsMetadataNoFollowRejectsFinalReparsePoints(t *testing.T) {
	t.Run("file symlink", func(t *testing.T) {
		root := t.TempDir()
		target := filepath.Join(root, "target")
		if err := os.WriteFile(target, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(root, "link")
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		file, err := OpenMetadataNoFollow(link)
		if err == nil || file != nil {
			if file != nil {
				_ = file.Close()
			}
			t.Fatalf("metadata reparse open got file=%v err=%v, want rejection", file, err)
		}
		if !strings.Contains(err.Error(), "reparse point") {
			t.Fatalf("metadata reparse error = %v, want reparse-point rejection", err)
		}
	})

	t.Run("directory junction", func(t *testing.T) {
		junctionTarget := t.TempDir()
		junction := filepath.Join(t.TempDir(), "junction")
		if output, err := exec.Command("cmd", "/c", "mklink", "/J", junction, junctionTarget).CombinedOutput(); err != nil {
			t.Fatalf("create junction: %v (%s)", err, output)
		}
		file, err := OpenMetadataNoFollow(junction)
		if err == nil || file != nil {
			if file != nil {
				_ = file.Close()
			}
			t.Fatalf("metadata junction open got file=%v err=%v, want rejection", file, err)
		}
		if !strings.Contains(err.Error(), "reparse point") {
			t.Fatalf("metadata junction error = %v, want reparse-point rejection", err)
		}
	})
}

func TestWindowsMetadataNoFollowHasNoDataAccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data")
	if err := os.WriteFile(path, []byte("must not be readable"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := OpenMetadataNoFollow(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close() //nolint:errcheck // test descriptor
	if _, err := file.Read(make([]byte, 1)); err == nil {
		t.Fatal("attribute-only metadata handle read file data")
	}
}

func TestWindowsMetadataNoFollowRejectsRawDeviceNamespace(t *testing.T) {
	for _, tt := range []struct {
		path string
		want string
	}{
		{path: `\\.\NUL`, want: "device namespace"},
		{path: `\\server\pipe\name`, want: "device namespace"},
		{path: `\\server\PIPE.\name`, want: "device namespace"},
		{path: `\\?\UNC\server\mailslot\name`, want: "device namespace"},
		{path: `C:\workspace\NUL:$DATA`, want: "alternate data stream"},
		{path: `C:\workspace\COM1:stream`, want: "alternate data stream"},
		{path: `C:\workspace\COM¹`, want: "reserved DOS device"},
		{path: `C:\workspace\LPT².txt`, want: "reserved DOS device"},
	} {
		t.Run(tt.path, func(t *testing.T) {
			file, err := OpenMetadataNoFollow(tt.path)
			if err == nil || file != nil {
				if file != nil {
					_ = file.Close()
				}
				t.Fatalf("device namespace open got file=%v err=%v, want rejection", file, err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("unsafe-path error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestWindowsMetadataPathAllowsDOSDeviceNearMisses(t *testing.T) {
	for _, path := range []string{
		`C:\workspace\COMLPT1`,
		`C:\workspace\LPTCOM1`,
		`C:\workspace\COM0`,
		`C:\workspace\LPT10`,
		`C:\workspace\COMLPT²`,
	} {
		if err := validateWindowsMetadataPath(path); err != nil {
			t.Fatalf("safe near-miss path %q: %v", path, err)
		}
	}
}
