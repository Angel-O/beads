// Package safefile provides the smallest cross-platform primitive needed to
// open untrusted filesystem leaves without blocking on Unix special files.
package safefile

import "os"

// OpenReadOnlyNoFollow opens path for reading without following a final
// symlink or waiting for Unix FIFO/device readiness. Callers must still inspect
// the returned descriptor and reject non-regular files before reading it.
func OpenReadOnlyNoFollow(path string) (*os.File, error) {
	return openReadOnlyNoFollow(path)
}
