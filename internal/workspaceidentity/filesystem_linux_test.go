//go:build linux

package workspaceidentity

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

type mountinfoStub struct {
	io.Reader
	fd       uintptr
	closeErr error
	closes   int
}

func (f *mountinfoStub) Fd() uintptr { return f.fd }
func (f *mountinfoStub) Close() error {
	f.closes++
	return f.closeErr
}

func newFilesystemTestWitness(t *testing.T) *Witness {
	t.Helper()
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(beadsDir, "embeddeddolt"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "metadata.json"), []byte(`{"backend":"dolt","dolt_mode":"embedded"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	witness, _, err := BindExisting(beadsDir, MaxMetadataBytes)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := witness.Close(); err != nil {
			t.Error(err)
		}
	})
	return witness
}

func qualifiedFilesystemAccess(t *testing.T, mountinfo *mountinfoStub, closeProvider func(int) error) filesystemAccess {
	t.Helper()
	if mountinfo == nil {
		mountinfo = &mountinfoStub{Reader: strings.NewReader("7 1 0:1 / / rw - ext4 /dev/test rw\n"), fd: 901}
	}
	if closeProvider == nil {
		closeProvider = func(int) error { return nil }
	}
	return filesystemAccess{
		openat: func(_ int, name string, flags int, mode uint32) (int, error) {
			if name != "embeddeddolt" || flags != embeddedDoltOpenFlags || mode != 0 {
				t.Fatalf("openat name=%q flags=%#x mode=%#o", name, flags, mode)
			}
			return 900, nil
		},
		statx: func(_ int, path string, flags, mask int, stat *unix.Statx_t) error {
			if path != "" || flags != unix.AT_EMPTY_PATH || mask != unix.STATX_MNT_ID {
				t.Fatalf("statx path=%q flags=%#x mask=%#x", path, flags, mask)
			}
			stat.Mask = unix.STATX_MNT_ID
			stat.Mnt_id = 7
			return nil
		},
		fstatfs: func(fd int, stat *unix.Statfs_t) error {
			if fd == int(mountinfo.fd) {
				stat.Type = unix.PROC_SUPER_MAGIC
			} else {
				stat.Type = unix.EXT4_SUPER_MAGIC
			}
			return nil
		},
		fstat: func(fd int, stat *unix.Stat_t) error {
			if fd != 900 {
				t.Fatalf("fstat fd=%d", fd)
			}
			stat.Dev, stat.Ino = 11, 12
			return nil
		},
		openMountinfo: func() (mountinfoFile, error) { return mountinfo, nil },
		closeProvider: closeProvider,
	}
}

func TestInspectEmbeddedDoltFilesystemUsesRetainedRelativeHandle(t *testing.T) {
	witness := newFilesystemTestWitness(t)
	mountinfo := &mountinfoStub{Reader: strings.NewReader("7 1 0:1 / / rw - ext4 /dev/test rw\n"), fd: 901}
	providerClosed := 0
	access := qualifiedFilesystemAccess(t, mountinfo, func(fd int) error {
		if fd != 900 {
			t.Fatalf("closed provider fd=%d", fd)
		}
		providerClosed++
		return nil
	})
	openat := access.openat
	access.openat = func(dirfd int, name string, flags int, mode uint32) (int, error) {
		if dirfd != int(witness.root.Fd()) {
			t.Fatalf("openat dirfd=%d, want retained root fd %d", dirfd, witness.root.Fd())
		}
		return openat(dirfd, name, flags, mode)
	}

	snapshot, err := witness.inspectEmbeddedDoltFilesystemWith(access)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Qualified() {
		t.Fatal("injected exact ext4 facts were not qualified")
	}
	if providerClosed != 1 || mountinfo.closes != 1 {
		t.Fatalf("provider closes=%d mountinfo closes=%d, want 1/1", providerClosed, mountinfo.closes)
	}
	if !snapshot.Equal(snapshot) || (FilesystemSnapshot{}).Equal(FilesystemSnapshot{}) {
		t.Fatal("filesystem snapshot validity/equality contract failed")
	}
	drift := snapshot
	drift.providerMountID++
	if snapshot.Equal(drift) {
		t.Fatal("different provider mount IDs compared equal")
	}
	drift = snapshot
	drift.providerInode++
	if snapshot.Equal(drift) {
		t.Fatal("different provider identities compared equal")
	}
}

func TestInspectEmbeddedDoltFilesystemRealLinuxProbe(t *testing.T) {
	witness := newFilesystemTestWitness(t)
	snapshot, err := witness.InspectEmbeddedDoltFilesystem()
	if err != nil {
		if errors.Is(err, ErrUnverifiable) || errors.Is(err, ErrUnsupported) {
			t.Skipf("host does not provide the qualified probe primitives: %v", err)
		}
		t.Fatal(err)
	}
	if !snapshot.valid {
		t.Fatal("successful real filesystem probe returned an invalid snapshot")
	}
	if !snapshot.Qualified() {
		t.Log("host filesystem was safely identified but is outside exact ext4/XFS qualification")
	}
}

func TestInspectEmbeddedDoltFilesystemMarksCleanupFailures(t *testing.T) {
	closeFailure := errors.New("injected close failure")
	t.Run("provider", func(t *testing.T) {
		witness := newFilesystemTestWitness(t)
		_, err := witness.inspectEmbeddedDoltFilesystemWith(qualifiedFilesystemAccess(t, nil, func(int) error { return closeFailure }))
		if !errors.Is(err, ErrCleanup) || !errors.Is(err, ErrUnverifiable) || !errors.Is(err, closeFailure) {
			t.Fatalf("provider close error = %v, want cleanup, unverifiable, and injected cause", err)
		}
	})

	t.Run("mountinfo", func(t *testing.T) {
		witness := newFilesystemTestWitness(t)
		mountinfo := &mountinfoStub{
			Reader:   strings.NewReader("7 1 0:1 / / rw - ext4 /dev/test rw\n"),
			fd:       901,
			closeErr: closeFailure,
		}
		_, err := witness.inspectEmbeddedDoltFilesystemWith(qualifiedFilesystemAccess(t, mountinfo, nil))
		if !errors.Is(err, ErrCleanup) || !errors.Is(err, ErrUnverifiable) || !errors.Is(err, closeFailure) {
			t.Fatalf("mountinfo close error = %v, want cleanup, unverifiable, and injected cause", err)
		}
		if mountinfo.closes != 1 {
			t.Fatalf("mountinfo closes=%d, want 1", mountinfo.closes)
		}
	})

	t.Run("cleanup preserves drift", func(t *testing.T) {
		witness := newFilesystemTestWitness(t)
		access := qualifiedFilesystemAccess(t, nil, func(int) error { return closeFailure })
		access.fstat = func(int, *unix.Stat_t) error {
			if err := os.WriteFile(witness.metadataPath, []byte(`{"backend":"dolt","dolt_mode":"server"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			return nil
		}
		_, err := witness.inspectEmbeddedDoltFilesystemWith(access)
		if !errors.Is(err, ErrCleanup) || !errors.Is(err, ErrChanged) || !errors.Is(err, closeFailure) {
			t.Fatalf("cleanup plus drift = %v, want all causes", err)
		}
	})
}

func TestInspectEmbeddedDoltFilesystemMarksOnlyProbeFailures(t *testing.T) {
	witness := newFilesystemTestWitness(t)
	access := qualifiedFilesystemAccess(t, nil, nil)
	access.statx = func(int, string, int, int, *unix.Statx_t) error { return os.ErrPermission }
	_, err := witness.inspectEmbeddedDoltFilesystemWith(access)
	var probe interface{ FilesystemProbeFailure() }
	if !errors.Is(err, ErrUnverifiable) || !errors.As(err, &probe) {
		t.Fatalf("syscall failure=%v, want marked filesystem probe failure", err)
	}

	drifted := newFilesystemTestWitness(t)
	if err := os.WriteFile(drifted.metadataPath, []byte(`{"backend":"dolt","dolt_mode":"server"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = drifted.inspectEmbeddedDoltFilesystemWith(qualifiedFilesystemAccess(t, nil, nil))
	probe = nil
	if !errors.Is(err, ErrChanged) || errors.As(err, &probe) {
		t.Fatalf("witness revalidation=%v, must remain unmarked ErrChanged", err)
	}
}

func TestInspectFilesystemDescriptorsQualificationMatrix(t *testing.T) {
	tests := []struct {
		name          string
		mountType     string
		magic         int64
		providerMount uint64
		qualified     bool
	}{
		{name: "ext4", mountType: "ext4", magic: unix.EXT4_SUPER_MAGIC, providerMount: 7, qualified: true},
		{name: "xfs", mountType: "xfs", magic: unix.XFS_SUPER_MAGIC, providerMount: 7, qualified: true},
		{name: "ext2 shares magic but is refused", mountType: "ext2", magic: unix.EXT4_SUPER_MAGIC, providerMount: 7},
		{name: "ext3 shares magic but is refused", mountType: "ext3", magic: unix.EXT4_SUPER_MAGIC, providerMount: 7},
		{name: "overlay", mountType: "overlay", magic: unix.OVERLAYFS_SUPER_MAGIC, providerMount: 7},
		{name: "magic mismatch", mountType: "ext4", magic: unix.XFS_SUPER_MAGIC, providerMount: 7},
		{name: "different provider mount", mountType: "ext4", magic: unix.EXT4_SUPER_MAGIC, providerMount: 8},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mountinfo := &mountinfoStub{
				Reader: strings.NewReader(fmt.Sprintf("%d 1 0:1 / / rw - %s /dev/test rw\n", test.providerMount, test.mountType)),
				fd:     901,
			}
			access := qualifiedFilesystemAccess(t, mountinfo, nil)
			access.statx = func(fd int, _ string, _, _ int, stat *unix.Statx_t) error {
				stat.Mask = unix.STATX_MNT_ID
				stat.Mnt_id = 7
				if fd == 900 {
					stat.Mnt_id = test.providerMount
				}
				return nil
			}
			access.fstatfs = func(fd int, stat *unix.Statfs_t) error {
				if fd == int(mountinfo.fd) {
					stat.Type = unix.PROC_SUPER_MAGIC
				} else {
					reflect.ValueOf(&stat.Type).Elem().SetInt(test.magic)
				}
				return nil
			}
			snapshot, err := inspectFilesystemDescriptors(access, 10, 11, 900)
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.Qualified() != test.qualified {
				t.Fatalf("qualified=%v, want %v", snapshot.Qualified(), test.qualified)
			}
			if !snapshot.valid {
				t.Fatal("successfully identified unsupported filesystem returned an invalid snapshot")
			}
			drift := snapshot
			drift.providerMountID++
			if snapshot.Qualified() != drift.Qualified() || snapshot.Equal(drift) {
				t.Fatalf("same-qualification mount drift was not detected: qualified=%v", snapshot.Qualified())
			}
		})
	}
}

func TestInspectEmbeddedDoltFilesystemSerializesWithClose(t *testing.T) {
	witness := newFilesystemTestWitness(t)
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		_, err := witness.inspectEmbeddedDoltFilesystemWith(qualifiedFilesystemAccess(t, nil, nil))
		results <- err
	}()
	go func() {
		<-start
		results <- witness.Close()
	}()
	close(start)
	for range 2 {
		err := <-results
		if err != nil && !errors.Is(err, ErrClosed) {
			t.Fatalf("concurrent inspect/close error=%v, want nil or ErrClosed", err)
		}
	}
}

func TestParseMountinfoTypeBoundaries(t *testing.T) {
	line := func(id string, optionals int) string {
		parts := []string{id, "1", "0:1", "/", "/", "rw"}
		for i := 0; i < optionals; i++ {
			parts = append(parts, "x:y")
		}
		parts = append(parts, "-", "ext4", "/dev/test", "rw")
		return strings.Join(parts, " ") + "\n"
	}
	maxID := strconv.FormatUint(^uint64(0), 10)
	for _, test := range []struct {
		name    string
		data    []byte
		wantID  uint64
		wantErr bool
	}{
		{name: "valid", data: []byte(line("7", 0)), wantID: 7},
		{name: "maximum mount ID", data: []byte(line(maxID, 0)), wantID: ^uint64(0)},
		{name: "numeric overflow", data: []byte(line(maxID+"0", 0)), wantID: 7, wantErr: true},
		{name: "signed numeric ID", data: []byte(line("+7", 0)), wantID: 7, wantErr: true},
		{name: "duplicate ID", data: []byte(line("7", 0) + line("7", 0)), wantID: 7, wantErr: true},
		{name: "missing separator", data: []byte("7 1 0:1 / / rw ext4 /dev/test rw\n"), wantID: 7, wantErr: true},
		{name: "extra post-separator field", data: []byte("7 1 0:1 / / rw - ext4 /dev/test rw injected\n"), wantID: 7, wantErr: true},
		{name: "truncated", data: []byte(strings.TrimSuffix(line("7", 0), "\n")), wantID: 7, wantErr: true},
		{name: "exact field limit", data: []byte(line("7", mountinfoMaxFields-10)), wantID: 7},
		{name: "field limit plus one", data: []byte(line("7", mountinfoMaxFields-9)), wantID: 7, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseMountinfoType(test.data, test.wantID)
			if test.wantErr {
				if err == nil {
					t.Fatalf("parse got type %q, want error", got)
				}
				return
			}
			if err != nil || got != "ext4" {
				t.Fatalf("parse got type %q err=%v, want ext4", got, err)
			}
		})
	}

	prefix := "7 1 0:1 / "
	suffix := " rw - ext4 /dev/test rw\n"
	exactLine := []byte(prefix + strings.Repeat("x", mountinfoMaxLine-len(prefix)-len(suffix)+1) + suffix)
	if len(exactLine)-1 != mountinfoMaxLine {
		t.Fatalf("test line length=%d, want %d", len(exactLine)-1, mountinfoMaxLine)
	}
	if got, err := parseMountinfoType(exactLine, 7); err != nil || got != "ext4" {
		t.Fatalf("exact line limit got %q, %v", got, err)
	}
	tooLong := append(append([]byte(nil), exactLine[:len(exactLine)-1]...), 'x', '\n')
	if _, err := parseMountinfoType(tooLong, 7); err == nil {
		t.Fatal("line limit plus one was accepted")
	}
}

func TestReadMountinfoTypeEnforcesTotalAndProcfs(t *testing.T) {
	const exactLineBytes = mountinfoMaxBytes / 64
	var exact bytes.Buffer
	for id := 1; id <= 64; id++ {
		base := fmt.Sprintf("%d 1 0:1 / / rw - ext4 /dev/test rw\n", id)
		padding := exactLineBytes - len(base)
		if padding < 0 {
			t.Fatal("mountinfo exact-total fixture line is too large")
		}
		line := fmt.Sprintf("%d 1 0:1 / /%s rw - ext4 /dev/test rw\n", id, strings.Repeat("x", padding))
		if len(line) != exactLineBytes {
			t.Fatalf("exact-total line bytes=%d, want %d", len(line), exactLineBytes)
		}
		exact.WriteString(line)
	}
	if exact.Len() != mountinfoMaxBytes {
		t.Fatalf("exact-total bytes=%d, want %d", exact.Len(), mountinfoMaxBytes)
	}
	exactFile := &mountinfoStub{Reader: bytes.NewReader(exact.Bytes()), fd: 901}
	if got, err := readMountinfoType(qualifiedFilesystemAccess(t, exactFile, nil), 1); err != nil || got != "ext4" {
		t.Fatalf("exact-total mountinfo got %q, %v", got, err)
	}

	access := qualifiedFilesystemAccess(t, &mountinfoStub{
		Reader: bytes.NewReader(bytes.Repeat([]byte("x"), mountinfoMaxBytes+1)),
		fd:     901,
	}, nil)
	if _, err := readMountinfoType(access, 7); !errors.Is(err, ErrUnverifiable) {
		t.Fatalf("oversized mountinfo error=%v, want unverifiable", err)
	}

	notProc := &mountinfoStub{Reader: strings.NewReader("7 1 0:1 / / rw - ext4 /dev/test rw\n"), fd: 901}
	access = qualifiedFilesystemAccess(t, notProc, nil)
	access.fstatfs = func(_ int, stat *unix.Statfs_t) error {
		stat.Type = unix.EXT4_SUPER_MAGIC
		return nil
	}
	if _, err := readMountinfoType(access, 7); !errors.Is(err, ErrUnverifiable) {
		t.Fatalf("non-proc mountinfo error=%v, want unverifiable", err)
	}
}

func TestParseMountinfoTypeAcceptsExactLineCount(t *testing.T) {
	var data strings.Builder
	for i := 1; i <= mountinfoMaxLines; i++ {
		fmt.Fprintf(&data, "%d 1 0:1 / / rw - ext4 /dev/test rw\n", i)
	}
	if got, err := parseMountinfoType([]byte(data.String()), mountinfoMaxLines); err != nil || got != "ext4" {
		t.Fatalf("exact line count got %q, %v", got, err)
	}
}

func TestParseMountinfoTypeRejectsLineCountOverflow(t *testing.T) {
	var data strings.Builder
	for i := 1; i <= mountinfoMaxLines+1; i++ {
		fmt.Fprintf(&data, "%d 1 0:1 / / rw - ext4 /dev/test rw\n", i)
	}
	if _, err := parseMountinfoType([]byte(data.String()), 1); err == nil {
		t.Fatal("mountinfo line limit plus one was accepted")
	}
}

func TestCloseRetainedMarksCleanupAndPreservesCause(t *testing.T) {
	path := filepath.Join(t.TempDir(), "already-closed")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	err = closeRetained(nil, file, "", path)
	if !errors.Is(err, ErrCleanup) || !errors.Is(err, ErrUnverifiable) || !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closeRetained error=%v, want cleanup, unverifiable, and closed causes", err)
	}
}
