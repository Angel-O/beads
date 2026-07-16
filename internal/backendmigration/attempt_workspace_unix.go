//go:build unix

package backendmigration

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	"github.com/steveyegge/beads/internal/atomicfile"
	"github.com/steveyegge/beads/internal/noreplace"
	"golang.org/x/sys/unix"
)

func createAttemptWorkspace(beadsDir, attemptID string) (identity string, err error) {
	if !attemptIDPattern.MatchString(attemptID) {
		return "", errors.New("backend migration workspace attempt is invalid")
	}
	path := attemptWorkspacePath(beadsDir, attemptID)
	if err := os.Mkdir(path, 0o700); err != nil {
		return "", fmt.Errorf("create backend migration workspace: %w", err)
	}
	removeOnFailure := true
	defer func() {
		if removeOnFailure {
			err = errors.Join(err, os.Remove(path), atomicfile.SyncDirectory(beadsDir))
		}
	}()

	named, err := os.Lstat(path)
	if err != nil || !named.IsDir() || named.Mode()&os.ModeSymlink != 0 || named.Mode().Perm()&0o022 != 0 {
		return "", errors.New("backend migration workspace is not a private directory")
	}
	handle, err := os.Open(path) // #nosec G304 -- freshly created path is identity-checked.
	if err != nil {
		return "", err
	}
	opened, statErr := handle.Stat()
	identity, identityErr := workspaceDirectoryIdentity(opened)
	syncErr := handle.Sync()
	closeErr := handle.Close()
	if statErr != nil || identityErr != nil || syncErr != nil || closeErr != nil || opened == nil || !opened.IsDir() || !os.SameFile(named, opened) {
		return "", errors.Join(errors.New("backend migration workspace changed during creation"), statErr, identityErr, syncErr, closeErr)
	}
	if err := atomicfile.SyncDirectory(beadsDir); err != nil {
		return "", err
	}
	removeOnFailure = false
	return identity, nil
}

func removeAttemptWorkspace(beadsDir, attemptID, expectedIdentity string) error {
	canonical := attemptWorkspacePath(beadsDir, attemptID)
	quarantine := attemptWorkspaceCleanupPath(beadsDir, attemptID)
	canonicalInfo, canonicalErr := os.Lstat(canonical)
	quarantineInfo, quarantineErr := os.Lstat(quarantine)
	canonicalMissing := errors.Is(canonicalErr, os.ErrNotExist)
	quarantineMissing := errors.Is(quarantineErr, os.ErrNotExist)
	if canonicalErr != nil && !canonicalMissing {
		return fmt.Errorf("inspect backend migration workspace: %w", canonicalErr)
	}
	if quarantineErr != nil && !quarantineMissing {
		return fmt.Errorf("inspect backend migration workspace quarantine: %w", quarantineErr)
	}
	if canonicalMissing && quarantineMissing {
		return nil
	}
	if !canonicalMissing && !quarantineMissing {
		return errors.New("backend migration workspace and quarantine both exist; preserving both")
	}

	ownedPath := canonical
	ownedInfo := canonicalInfo
	if canonicalMissing {
		ownedPath = quarantine
		ownedInfo = quarantineInfo
	}
	if err := verifyAttemptWorkspaceInfo(ownedInfo, expectedIdentity); err != nil {
		return err
	}
	if ownedPath == canonical {
		if err := noreplace.Rename(canonical, quarantine); err != nil {
			return fmt.Errorf("quarantine backend migration workspace: %w", err)
		}
		if err := atomicfile.SyncDirectory(beadsDir); err != nil {
			return fmt.Errorf("backend migration workspace quarantined: %w: %v", atomicfile.ErrApplied, err)
		}
	}

	named, err := os.Lstat(quarantine)
	if err != nil {
		return fmt.Errorf("inspect quarantined backend migration workspace: %w", err)
	}
	if err := verifyAttemptWorkspaceInfo(named, expectedIdentity); err != nil {
		return err
	}
	directory, err := os.Open(quarantine) // #nosec G304 -- quarantined identity is checked around use.
	if err != nil {
		return err
	}
	opened, statErr := directory.Stat()
	if statErr != nil || opened == nil || !os.SameFile(named, opened) {
		_ = directory.Close()
		return errors.Join(errors.New("backend migration workspace changed while opening quarantine"), statErr)
	}
	entries, err := directory.ReadDir(-1)
	if err != nil {
		_ = directory.Close()
		return err
	}
	for _, entry := range entries {
		if !allowedAttemptWorkspaceEntry(entry.Name()) {
			_ = directory.Close()
			return fmt.Errorf("backend migration workspace contains unexpected entry %q; preserving it", entry.Name())
		}
		if err := removeAttemptWorkspaceEntry(directory, entry.Name()); err != nil {
			_ = directory.Close()
			return err
		}
	}
	if err := directory.Sync(); err != nil {
		_ = directory.Close()
		return err
	}
	if err := directory.Close(); err != nil {
		return err
	}
	after, err := os.Lstat(quarantine)
	if err != nil || !os.SameFile(named, after) {
		return errors.Join(errors.New("backend migration workspace changed before removal"), err)
	}
	if err := os.Remove(quarantine); err != nil {
		return fmt.Errorf("remove backend migration workspace: %w", err)
	}
	if err := atomicfile.SyncDirectory(beadsDir); err != nil {
		return fmt.Errorf("backend migration workspace removed: %w: %v", atomicfile.ErrApplied, err)
	}
	return nil
}

func workspaceDirectoryIdentity(info os.FileInfo) (string, error) {
	if info == nil || !info.IsDir() {
		return "", errors.New("backend migration workspace identity is unavailable")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", errors.New("backend migration workspace identity is unavailable")
	}
	return fmt.Sprintf("%x:%x", stat.Dev, stat.Ino), nil
}

func verifyAttemptWorkspaceInfo(info os.FileInfo, expectedIdentity string) error {
	if !workspaceIdentityPattern.MatchString(expectedIdentity) || info == nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 {
		return errors.New("backend migration workspace identity is unsafe; preserving it")
	}
	actual, err := workspaceDirectoryIdentity(info)
	if err != nil || actual != expectedIdentity {
		return errors.Join(errors.New("backend migration workspace identity changed; preserving it"), err)
	}
	return nil
}

func allowedAttemptWorkspaceEntry(name string) bool {
	for _, base := range []string{attemptWorkspaceFile, attemptWorkspaceFile + ".creating"} {
		if name == base {
			return true
		}
		for _, suffix := range []string{"-journal", "-wal", "-shm"} {
			if name == base+suffix {
				return true
			}
		}
	}
	return false
}

func removeAttemptWorkspaceEntry(directory *os.File, name string) error {
	fd, err := unix.Openat(int(directory.Fd()), name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return fmt.Errorf("open backend migration workspace entry %q: %w", name, err)
	}
	defer unix.Close(fd) //nolint:errcheck // removal result is authoritative
	var opened unix.Stat_t
	if err := unix.Fstat(fd, &opened); err != nil || opened.Mode&unix.S_IFMT != unix.S_IFREG || opened.Nlink != 1 {
		return errors.Join(fmt.Errorf("backend migration workspace entry %q is unsafe; preserving it", name), err)
	}
	var named unix.Stat_t
	if err := unix.Fstatat(int(directory.Fd()), name, &named, unix.AT_SYMLINK_NOFOLLOW); err != nil || named.Dev != opened.Dev || named.Ino != opened.Ino {
		return errors.Join(fmt.Errorf("backend migration workspace entry %q changed; preserving it", name), err)
	}
	if err := unix.Unlinkat(int(directory.Fd()), name, 0); err != nil {
		return fmt.Errorf("remove backend migration workspace entry %q: %w", name, err)
	}
	return nil
}
