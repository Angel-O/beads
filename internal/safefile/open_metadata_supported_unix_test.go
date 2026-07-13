//go:build android || darwin || ios || linux

package safefile

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestOpenMetadataNoFollowDoesNotConsumeFinalSymlinkTarget(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("target data"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	file, err := OpenMetadataNoFollow(link)
	if err == nil || file != nil {
		if file != nil {
			_ = file.Close()
		}
		t.Fatalf("metadata symlink open got file=%v err=%v, want rejection", file, err)
	}
}

func TestOpenMetadataNoFollowSupportsRegularFilesAndDirectories(t *testing.T) {
	root := t.TempDir()
	regular := filepath.Join(root, "data")
	if err := os.WriteFile(regular, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name    string
		path    string
		wantDir bool
	}{
		{name: "regular file", path: regular},
		{name: "directory", path: root, wantDir: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			file, err := OpenMetadataNoFollow(tt.path)
			if err != nil {
				t.Fatalf("open metadata handle: %v", err)
			}
			info, statErr := file.Stat()
			closeErr := file.Close()
			if statErr != nil || closeErr != nil {
				t.Fatalf("metadata handle statErr=%v closeErr=%v", statErr, closeErr)
			}
			if info.IsDir() != tt.wantDir {
				t.Fatalf("metadata mode = %v, want directory=%v", info.Mode(), tt.wantDir)
			}
		})
	}
}

func TestOpenMetadataNoFollowFIFOIsPrompt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fifo")
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	result := make(chan struct {
		file *os.File
		err  error
	}, 1)
	go func() {
		file, err := OpenMetadataNoFollow(path)
		result <- struct {
			file *os.File
			err  error
		}{file: file, err: err}
	}()
	select {
	case got := <-result:
		if got.err != nil {
			t.Fatalf("open FIFO metadata: %v", got.err)
		}
		defer got.file.Close() //nolint:errcheck // test descriptor
		info, err := got.file.Stat()
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode()&os.ModeNamedPipe == 0 {
			t.Fatalf("metadata descriptor mode = %v, want FIFO", info.Mode())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("metadata open blocked on FIFO")
	}
}
