//go:build linux

package workspaceidentity

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	mountinfoMaxBytes  = 4 << 20
	mountinfoMaxLines  = 65_536
	mountinfoMaxLine   = 64 << 10
	mountinfoMaxFields = 256
)

const embeddedDoltOpenFlags = unix.O_PATH | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC

type mountinfoFile interface {
	io.Reader
	io.Closer
	Fd() uintptr
}

type filesystemAccess struct {
	openat        func(int, string, int, uint32) (int, error)
	statx         func(int, string, int, int, *unix.Statx_t) error
	fstatfs       func(int, *unix.Statfs_t) error
	fstat         func(int, *unix.Stat_t) error
	openMountinfo func() (mountinfoFile, error)
	closeProvider func(int) error
}

var productionFilesystemAccess = filesystemAccess{
	openat:  unix.Openat,
	statx:   unix.Statx,
	fstatfs: unix.Fstatfs,
	fstat:   unix.Fstat,
	openMountinfo: func() (mountinfoFile, error) {
		return os.Open("/proc/self/mountinfo")
	},
	closeProvider: unix.Close,
}

// InspectEmbeddedDoltFilesystem qualifies the canonical embedded-Dolt child
// relative to the retained workspace descriptor. It retains no child handle.
func (w *Witness) InspectEmbeddedDoltFilesystem() (FilesystemSnapshot, error) {
	return w.inspectEmbeddedDoltFilesystemWith(productionFilesystemAccess)
}

func (w *Witness) inspectEmbeddedDoltFilesystemWith(access filesystemAccess) (_ FilesystemSnapshot, returnErr error) {
	if w == nil {
		return FilesystemSnapshot{}, ErrClosed
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return FilesystemSnapshot{}, ErrClosed
	}
	if err := validateFilesystemAccess(access); err != nil {
		return FilesystemSnapshot{}, filesystemProbeError("validate filesystem access", err)
	}
	if err := w.revalidateLocked(); err != nil {
		return FilesystemSnapshot{}, err
	}

	providerFD, err := access.openat(int(w.root.Fd()), "embeddeddolt", embeddedDoltOpenFlags, 0)
	if err != nil {
		return FilesystemSnapshot{}, filesystemProbeError("open embedded provider root", err)
	}
	defer func() {
		if closeErr := access.closeProvider(providerFD); closeErr != nil {
			returnErr = errors.Join(returnErr, markCleanup(filesystemProbeError("close embedded provider root", closeErr)))
		}
	}()

	if err := w.revalidateLocked(); err != nil {
		return FilesystemSnapshot{}, err
	}
	snapshot, err := inspectFilesystemDescriptors(access, int(w.root.Fd()), int(w.metadata.Fd()), providerFD)
	if err != nil {
		return FilesystemSnapshot{}, err
	}
	if err := w.revalidateLocked(); err != nil {
		return FilesystemSnapshot{}, err
	}
	return snapshot, nil
}

func validateFilesystemAccess(access filesystemAccess) error {
	if access.openat == nil || access.statx == nil || access.fstatfs == nil || access.fstat == nil ||
		access.openMountinfo == nil || access.closeProvider == nil {
		return errors.New("incomplete filesystem access")
	}
	return nil
}

func inspectFilesystemDescriptors(access filesystemAccess, rootFD, metadataFD, providerFD int) (FilesystemSnapshot, error) {
	rootMountID, err := descriptorMountID(access, rootFD)
	if err != nil {
		return FilesystemSnapshot{}, err
	}
	metadataMountID, err := descriptorMountID(access, metadataFD)
	if err != nil {
		return FilesystemSnapshot{}, err
	}
	providerMountID, err := descriptorMountID(access, providerFD)
	if err != nil {
		return FilesystemSnapshot{}, err
	}

	rootType, err := descriptorFilesystemType(access, rootFD)
	if err != nil {
		return FilesystemSnapshot{}, err
	}
	metadataType, err := descriptorFilesystemType(access, metadataFD)
	if err != nil {
		return FilesystemSnapshot{}, err
	}
	providerType, err := descriptorFilesystemType(access, providerFD)
	if err != nil {
		return FilesystemSnapshot{}, err
	}

	var providerInfo unix.Stat_t
	if err := access.fstat(providerFD, &providerInfo); err != nil {
		return FilesystemSnapshot{}, filesystemProbeError("inspect embedded provider identity", err)
	}
	mountType, err := readMountinfoType(access, providerMountID)
	if err != nil {
		return FilesystemSnapshot{}, err
	}

	expectedMagic := int64(0)
	switch mountType {
	case "ext4":
		expectedMagic = unix.EXT4_SUPER_MAGIC
	case "xfs":
		expectedMagic = unix.XFS_SUPER_MAGIC
	}
	qualified := rootMountID == metadataMountID && metadataMountID == providerMountID &&
		expectedMagic != 0 && rootType == expectedMagic && metadataType == expectedMagic && providerType == expectedMagic
	return FilesystemSnapshot{
		valid:           true,
		qualified:       qualified,
		rootMountID:     rootMountID,
		metadataMountID: metadataMountID,
		providerMountID: providerMountID,
		rootType:        rootType,
		metadataType:    metadataType,
		providerType:    providerType,
		mountinfoType:   mountType,
		providerDevice:  uint64(providerInfo.Dev), //nolint:unconvert // Dev width varies across supported Linux architectures.
		providerInode:   providerInfo.Ino,
	}, nil
}

func descriptorMountID(access filesystemAccess, fd int) (uint64, error) {
	var stat unix.Statx_t
	if err := access.statx(fd, "", unix.AT_EMPTY_PATH, unix.STATX_MNT_ID, &stat); err != nil {
		return 0, filesystemProbeError("inspect descriptor mount", err)
	}
	if stat.Mask&unix.STATX_MNT_ID == 0 || stat.Mnt_id == 0 {
		return 0, filesystemProbeError("inspect descriptor mount", errors.New("mount ID unavailable"))
	}
	return stat.Mnt_id, nil
}

func descriptorFilesystemType(access filesystemAccess, fd int) (int64, error) {
	var stat unix.Statfs_t
	if err := access.fstatfs(fd, &stat); err != nil {
		return 0, filesystemProbeError("inspect descriptor filesystem", err)
	}
	return int64(stat.Type), nil //nolint:unconvert // Type width varies across supported Linux architectures.
}

func readMountinfoType(access filesystemAccess, mountID uint64) (_ string, returnErr error) {
	file, err := access.openMountinfo()
	if err != nil {
		return "", filesystemProbeError("open mountinfo", err)
	}
	if file == nil {
		return "", filesystemProbeError("open mountinfo", errors.New("nil mountinfo file"))
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			returnErr = errors.Join(returnErr, markCleanup(filesystemProbeError("close mountinfo", closeErr)))
		}
	}()

	var procfs unix.Statfs_t
	if err := access.fstatfs(int(file.Fd()), &procfs); err != nil {
		return "", filesystemProbeError("inspect mountinfo filesystem", err)
	}
	if int64(procfs.Type) != unix.PROC_SUPER_MAGIC { //nolint:unconvert // Type width varies across supported Linux architectures.
		return "", filesystemProbeError("inspect mountinfo filesystem", errors.New("mountinfo is not procfs"))
	}
	data, err := io.ReadAll(io.LimitReader(file, mountinfoMaxBytes+1))
	if err != nil {
		return "", filesystemProbeError("read mountinfo", err)
	}
	if len(data) > mountinfoMaxBytes {
		return "", filesystemProbeError("read mountinfo", errors.New("mountinfo exceeds byte limit"))
	}
	mountType, err := parseMountinfoType(data, mountID)
	if err != nil {
		return "", filesystemProbeError("parse mountinfo", err)
	}
	return mountType, nil
}

func parseMountinfoType(data []byte, wantedMountID uint64) (string, error) {
	if wantedMountID == 0 {
		return "", errors.New("invalid requested mount ID")
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		return "", errors.New("mountinfo is empty or truncated")
	}
	lines := bytes.Split(data[:len(data)-1], []byte{'\n'})
	if len(lines) > mountinfoMaxLines {
		return "", errors.New("mountinfo exceeds line limit")
	}

	seen := make(map[uint64]struct{}, len(lines))
	found := ""
	for _, line := range lines {
		if len(line) == 0 || len(line) > mountinfoMaxLine {
			return "", errors.New("mountinfo contains an invalid line length")
		}
		fields := bytes.Fields(line)
		if len(fields) > mountinfoMaxFields {
			return "", errors.New("mountinfo exceeds field limit")
		}
		if len(fields) < 10 {
			return "", errors.New("mountinfo record is incomplete")
		}
		mountID, err := parsePositiveDecimal(fields[0])
		if err != nil {
			return "", fmt.Errorf("invalid mount ID: %w", err)
		}
		if _, duplicate := seen[mountID]; duplicate {
			return "", errors.New("mountinfo contains duplicate mount IDs")
		}
		seen[mountID] = struct{}{}
		if _, err := parsePositiveDecimal(fields[1]); err != nil {
			return "", fmt.Errorf("invalid parent mount ID: %w", err)
		}
		if err := validateDeviceNumber(fields[2]); err != nil {
			return "", err
		}

		separator := -1
		for i := 6; i < len(fields); i++ {
			if bytes.Equal(fields[i], []byte("-")) {
				if separator != -1 {
					return "", errors.New("mountinfo record has duplicate separators")
				}
				separator = i
			}
		}
		if separator == -1 || len(fields) != separator+4 {
			return "", errors.New("mountinfo record has no complete separator section")
		}
		if mountID == wantedMountID {
			found = string(fields[separator+1])
		}
	}
	if found == "" {
		return "", errors.New("mountinfo has no matching mount ID")
	}
	return found, nil
}

func parsePositiveDecimal(value []byte) (uint64, error) {
	if len(value) == 0 {
		return 0, errors.New("value is empty")
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return 0, errors.New("value is not an unsigned decimal")
		}
	}
	parsed, err := strconv.ParseUint(string(value), 10, 64)
	if err != nil || parsed == 0 {
		if err == nil {
			err = errors.New("value is zero")
		}
		return 0, err
	}
	return parsed, nil
}

func validateDeviceNumber(value []byte) error {
	parts := strings.Split(string(value), ":")
	if len(parts) != 2 {
		return errors.New("mountinfo contains an invalid device number")
	}
	for _, part := range parts {
		if _, err := strconv.ParseUint(part, 10, 32); err != nil {
			return errors.New("mountinfo contains an invalid device number")
		}
	}
	return nil
}

func filesystemProbeError(operation string, cause error) error {
	return markFilesystemProbeFailure(fmt.Errorf("%w: %s: %w", ErrUnverifiable, operation, stripPathErrors(cause)))
}

type filesystemProbeFailureError struct{ cause error }

func (e *filesystemProbeFailureError) Error() string           { return e.cause.Error() }
func (e *filesystemProbeFailureError) Unwrap() error           { return e.cause }
func (e *filesystemProbeFailureError) FilesystemProbeFailure() {}

func markFilesystemProbeFailure(err error) error {
	if err == nil {
		return nil
	}
	var marked interface{ FilesystemProbeFailure() }
	if errors.As(err, &marked) {
		return err
	}
	return &filesystemProbeFailureError{cause: err}
}
