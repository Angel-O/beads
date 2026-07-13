// Package safefile provides cross-platform primitives for opening untrusted
// filesystem leaves with explicit symlink and nonblocking policies.
package safefile

import "os"

// OpenReadOnly opens path for reading with nonblocking Unix semantics so a FIFO
// does not wait for a writer. It follows a final symlink for
// compatibility-sensitive readers. On success, the caller owns the returned
// file and must inspect and close it.
func OpenReadOnly(path string) (*os.File, error) {
	return openReadOnly(path, false)
}

// OpenReadOnlyNoFollow opens path for reading with nonblocking Unix semantics
// without following its final path component. Ancestor symlinks are still
// followed. On success, the caller owns the returned file and must inspect,
// validate the expected object type, and close it. Unsupported platforms
// return errors.ErrUnsupported.
func OpenReadOnlyNoFollow(path string) (*os.File, error) {
	return openReadOnly(path, true)
}

// OpenMetadataNoFollow opens path only for stable metadata inspection without
// following its final component. Linux and Android use O_PATH, Darwin and iOS
// use O_EVTONLY with O_NONBLOCK, and Windows requests attribute-only access.
// Ancestor symlinks are still followed. The caller owns the returned file and
// may only Stat and Close it. A final symlink/reparse point is rejected. The
// handle pins the opened object, not its pathname or ancestors; it is not a
// lifetime fence. Platforms without a metadata-only primitive return
// errors.ErrUnsupported.
func OpenMetadataNoFollow(path string) (*os.File, error) {
	return openMetadataNoFollow(path)
}
