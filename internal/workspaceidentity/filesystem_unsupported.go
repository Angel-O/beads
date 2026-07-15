//go:build !linux

package workspaceidentity

// InspectEmbeddedDoltFilesystem is unsupported without Linux descriptor and
// mount-namespace primitives. It performs no filesystem access.
func (w *Witness) InspectEmbeddedDoltFilesystem() (FilesystemSnapshot, error) {
	return FilesystemSnapshot{}, ErrUnsupported
}
