// Package workspaceidentity retains read-only workspace identity evidence
// across admission observations. It is not a writer lock or provider authority.
package workspaceidentity

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/steveyegge/beads/internal/safefile"
)

// MaxMetadataBytes is the largest current-metadata file a witness will retain.
const MaxMetadataBytes int64 = 1 << 20

var (
	ErrUnsupported  = errors.New("workspace identity witness is unsupported")
	ErrIneligible   = errors.New("workspace identity witness is ineligible")
	ErrChanged      = errors.New("workspace identity changed")
	ErrUnverifiable = errors.New("workspace identity is unverifiable")
	ErrClosed       = errors.New("workspace identity witness is closed")
	ErrCleanup      = errors.New("workspace identity cleanup failed")
)

var errMetadataExceedsLimit = errors.New("metadata exceeds the size limit")

// Witness is an opaque retained observation of an existing workspace.
type Witness struct {
	mu sync.Mutex

	rootPath, metadataPath         string
	root, metadata                 *os.File
	rootIdentity, metadataIdentity os.FileInfo
	metadataBytes                  []byte
	metadataDigest                 [sha256.Size]byte
	metadataLimit                  int64
	closed                         bool
}

// FilesystemSnapshot is an opaque, point-in-time qualification of the
// retained workspace, metadata, and canonical embedded-Dolt provider root.
// It is observation evidence only and retains no descriptor or path.
type FilesystemSnapshot struct {
	valid, qualified bool

	rootMountID, metadataMountID, providerMountID uint64
	rootType, metadataType, providerType          int64
	mountinfoType                                 string
	providerDevice, providerInode                 uint64
}

// Qualified reports whether this nonzero snapshot matched the narrow local
// ext4/XFS qualification contract.
func (s FilesystemSnapshot) Qualified() bool { return s.valid && s.qualified }

// Equal reports whether two valid snapshots captured identical private facts.
// Zero or failed snapshots are deliberately never equal.
func (s FilesystemSnapshot) Equal(other FilesystemSnapshot) bool {
	return s.valid && other.valid && s == other
}

// Supported reports whether this build can retain a workspace witness.
func Supported() bool { return runtime.GOOS == "linux" }

// BindExisting retains the existing workspace root and current metadata file.
// It never creates workspace state and the returned bytes do not alias the
// witness's private baseline.
func BindExisting(beadsDir string, metadataLimit int64) (*Witness, []byte, error) {
	if !Supported() {
		return nil, nil, ErrUnsupported
	}
	displayPath := filepath.Clean(beadsDir)
	if metadataLimit <= 0 || metadataLimit > MaxMetadataBytes {
		return nil, nil, witnessError(ErrUnverifiable, "validate metadata size limit", displayPath, nil)
	}
	if err := validatePathText(beadsDir); err != nil {
		return nil, nil, witnessError(ErrUnverifiable, "validate workspace path", displayPath, err)
	}
	if err := validatePathText(displayPath); err != nil {
		return nil, nil, witnessError(ErrUnverifiable, "validate workspace path", displayPath, err)
	}
	absolute, err := filepath.Abs(displayPath)
	if err != nil {
		return nil, nil, witnessError(ErrUnverifiable, "resolve workspace", displayPath, err)
	}
	absolute = filepath.Clean(absolute)
	if err := validatePathText(absolute); err != nil {
		return nil, nil, witnessError(ErrUnverifiable, "validate absolute workspace path", absolute, err)
	}
	if err := safefile.ValidateMetadataPath(absolute); err != nil {
		return nil, nil, witnessError(ErrUnverifiable, "validate workspace", absolute, err)
	}

	rootNamed, err := os.Lstat(absolute)
	if err != nil {
		class := ErrUnverifiable
		if errors.Is(err, os.ErrNotExist) {
			class = ErrIneligible
		}
		return nil, nil, witnessError(class, "inspect workspace", absolute, err)
	}
	if !rootNamed.IsDir() {
		return nil, nil, witnessError(ErrUnverifiable, "require workspace directory", absolute, nil)
	}
	root, err := safefile.OpenMetadataNoFollow(absolute)
	if err != nil {
		return nil, nil, witnessError(openErrorClass(err), "open workspace", absolute, err)
	}
	fail := func(class error, operation, path string, cause error) (*Witness, []byte, error) {
		return nil, nil, errors.Join(witnessError(class, operation, path, cause), closeRetained(root, nil, absolute, ""))
	}
	rootInfo, err := root.Stat()
	if err != nil {
		return fail(ErrUnverifiable, "inspect opened workspace", absolute, err)
	}
	if !rootInfo.IsDir() || !os.SameFile(rootNamed, rootInfo) {
		return fail(ErrChanged, "bind workspace", absolute, nil)
	}

	metadataPath := filepath.Join(absolute, "metadata.json")
	metadataNamed, err := os.Lstat(metadataPath)
	if err != nil {
		class := ErrUnverifiable
		if errors.Is(err, os.ErrNotExist) {
			class = ErrIneligible
		}
		return fail(class, "inspect metadata", metadataPath, err)
	}
	if !metadataNamed.Mode().IsRegular() || metadataNamed.Size() > metadataLimit {
		return fail(ErrUnverifiable, "require bounded regular metadata", metadataPath, nil)
	}
	metadata, err := safefile.OpenReadOnlyNoFollow(metadataPath)
	if err != nil {
		return fail(openErrorClass(err), "open metadata", metadataPath, err)
	}
	fail = func(class error, operation, path string, cause error) (*Witness, []byte, error) {
		return nil, nil, errors.Join(witnessError(class, operation, path, cause), closeRetained(root, metadata, absolute, metadataPath))
	}
	metadataInfo, err := metadata.Stat()
	if err != nil {
		return fail(ErrUnverifiable, "inspect opened metadata", metadataPath, err)
	}
	if !metadataInfo.Mode().IsRegular() || !os.SameFile(metadataNamed, metadataInfo) {
		return fail(ErrChanged, "bind metadata", metadataPath, nil)
	}
	if count, known := safefile.OpenedFileLinkCount(metadata, metadataInfo); !known || count != 1 {
		return fail(ErrUnverifiable, "require single-link metadata", metadataPath, nil)
	}
	data, err := readBounded(metadata, metadataLimit)
	if err != nil {
		return fail(ErrUnverifiable, "read metadata", metadataPath, err)
	}
	metadataAfter, err := metadata.Stat()
	if err != nil {
		return fail(ErrUnverifiable, "reinspect opened metadata", metadataPath, err)
	}
	if !sameStableFile(metadataInfo, metadataAfter) {
		return fail(ErrChanged, "stabilize workspace metadata", absolute, nil)
	}
	if same, nameErr := namedSame(metadataPath, metadataInfo); nameErr != nil {
		return fail(namedErrorClass(nameErr), "restabilize named metadata", metadataPath, nameErr)
	} else if !same {
		return fail(ErrChanged, "restabilize named metadata", metadataPath, nil)
	}
	if same, nameErr := namedSame(absolute, rootInfo); nameErr != nil {
		return fail(namedErrorClass(nameErr), "restabilize named workspace", absolute, nameErr)
	} else if !same {
		return fail(ErrChanged, "restabilize named workspace", absolute, nil)
	}

	privateBytes := append([]byte(nil), data...)
	witness := &Witness{
		rootPath:         absolute,
		metadataPath:     metadataPath,
		root:             root,
		metadata:         metadata,
		rootIdentity:     rootInfo,
		metadataIdentity: metadataInfo,
		metadataBytes:    privateBytes,
		metadataDigest:   sha256.Sum256(privateBytes),
		metadataLimit:    metadataLimit,
	}
	return witness, append([]byte(nil), data...), nil
}

// Revalidate checks that the retained workspace still names the same objects.
func (w *Witness) Revalidate() error {
	if w == nil {
		return ErrClosed
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return ErrClosed
	}
	return w.revalidateLocked()
}

func (w *Witness) revalidateLocked() error {
	rootInfo, err := w.root.Stat()
	if err != nil {
		return witnessError(ErrUnverifiable, "inspect retained workspace", w.rootPath, err)
	}
	if !rootInfo.IsDir() || !os.SameFile(w.rootIdentity, rootInfo) {
		return witnessError(ErrChanged, "revalidate workspace", w.rootPath, nil)
	}
	if same, nameErr := namedSame(w.rootPath, w.rootIdentity); nameErr != nil {
		return witnessError(namedErrorClass(nameErr), "revalidate named workspace", w.rootPath, nameErr)
	} else if !same {
		return witnessError(ErrChanged, "revalidate named workspace", w.rootPath, nil)
	}
	metadataInfo, err := w.metadata.Stat()
	if err != nil {
		return witnessError(ErrUnverifiable, "inspect retained metadata", w.metadataPath, err)
	}
	if !metadataInfo.Mode().IsRegular() || !os.SameFile(w.metadataIdentity, metadataInfo) {
		return witnessError(ErrChanged, "revalidate metadata", w.metadataPath, nil)
	}
	if same, nameErr := namedSame(w.metadataPath, w.metadataIdentity); nameErr != nil {
		return witnessError(namedErrorClass(nameErr), "revalidate named metadata", w.metadataPath, nameErr)
	} else if !same {
		return witnessError(ErrChanged, "revalidate named metadata", w.metadataPath, nil)
	}
	if count, known := safefile.OpenedFileLinkCount(w.metadata, metadataInfo); !known || count != 1 {
		return witnessError(ErrChanged, "revalidate metadata links", w.metadataPath, nil)
	}
	data, err := readBounded(w.metadata, w.metadataLimit)
	if err != nil {
		if errors.Is(err, errMetadataExceedsLimit) {
			return witnessError(ErrChanged, "revalidate metadata size", w.metadataPath, nil)
		}
		return witnessError(ErrUnverifiable, "reread retained metadata", w.metadataPath, err)
	}
	metadataAfter, err := w.metadata.Stat()
	if err != nil {
		return witnessError(ErrUnverifiable, "reinspect retained metadata", w.metadataPath, err)
	}
	if !sameStableFile(metadataInfo, metadataAfter) {
		return witnessError(ErrChanged, "revalidate stable metadata", w.metadataPath, nil)
	}
	if count, known := safefile.OpenedFileLinkCount(w.metadata, metadataAfter); !known || count != 1 {
		return witnessError(ErrChanged, "finalize metadata links", w.metadataPath, nil)
	}
	if sha256.Sum256(data) != w.metadataDigest || !bytes.Equal(data, w.metadataBytes) {
		return witnessError(ErrChanged, "revalidate metadata bytes", w.metadataPath, nil)
	}
	if same, nameErr := namedSame(w.metadataPath, w.metadataIdentity); nameErr != nil {
		return witnessError(namedErrorClass(nameErr), "finalize named metadata", w.metadataPath, nameErr)
	} else if !same {
		return witnessError(ErrChanged, "finalize named metadata", w.metadataPath, nil)
	}
	if same, nameErr := namedSame(w.rootPath, w.rootIdentity); nameErr != nil {
		return witnessError(namedErrorClass(nameErr), "finalize named workspace", w.rootPath, nameErr)
	} else if !same {
		return witnessError(ErrChanged, "finalize named workspace", w.rootPath, nil)
	}
	return nil
}

// Close releases retained workspace observations.
func (w *Witness) Close() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	err := closeRetained(w.root, w.metadata, w.rootPath, w.metadataPath)
	w.root, w.metadata, w.metadataBytes = nil, nil, nil
	return err
}

func readBounded(file *os.File, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.NewSectionReader(file, 0, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errMetadataExceedsLimit
	}
	return data, nil
}

func namedSame(path string, identity os.FileInfo) (bool, error) {
	named, err := os.Lstat(path)
	if err != nil {
		return false, err
	}
	return identity != nil && os.SameFile(named, identity), nil
}

func namedErrorClass(err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return ErrChanged
	}
	return ErrUnverifiable
}

func validatePathText(path string) error {
	if !utf8.ValidString(path) {
		return errors.New("path is not valid UTF-8")
	}
	for _, value := range path {
		if unicode.IsControl(value) {
			return errors.New("path contains terminal control characters")
		}
	}
	return nil
}

func sameStableFile(before, after os.FileInfo) bool {
	return before != nil && after != nil && os.SameFile(before, after) && before.Size() == after.Size() && before.ModTime().Equal(after.ModTime())
}

func closeRetained(root, metadata *os.File, rootPath, metadataPath string) error {
	var errs []error
	if metadata != nil {
		if err := metadata.Close(); err != nil {
			errs = append(errs, markCleanup(witnessError(ErrUnverifiable, "close metadata", metadataPath, err)))
		}
	}
	if root != nil {
		if err := root.Close(); err != nil {
			errs = append(errs, markCleanup(witnessError(ErrUnverifiable, "close workspace", rootPath, err)))
		}
	}
	return errors.Join(errs...)
}

type cleanupError struct{ cause error }

func (e *cleanupError) Error() string { return e.cause.Error() }
func (e *cleanupError) Unwrap() []error {
	return []error{ErrCleanup, e.cause}
}

func markCleanup(err error) error {
	if err == nil || errors.Is(err, ErrCleanup) {
		return err
	}
	return &cleanupError{cause: err}
}

func openErrorClass(err error) error {
	if errors.Is(err, errors.ErrUnsupported) {
		return ErrUnsupported
	}
	if errors.Is(err, os.ErrNotExist) {
		return ErrChanged
	}
	return ErrUnverifiable
}

func witnessError(class error, operation, path string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%w: %s %q", class, operation, path)
	}
	return fmt.Errorf("%w: %s %q: %w", class, operation, path, stripPathErrors(cause))
}

func stripPathErrors(err error) error {
	for {
		var pathErr *os.PathError
		if !errors.As(err, &pathErr) {
			return err
		}
		err = pathErr.Err
	}
}
