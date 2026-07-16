// Package safefile contains narrow helpers for reading security-sensitive
// workspace control files without following symbolic links.
package safefile

// ReadRegularFile reads at most maxBytes from a single-link regular file.
// Implementations verify the named entry before and after the read.
func ReadRegularFile(path string, maxBytes int64) ([]byte, error) {
	return readRegularFile(path, maxBytes)
}
