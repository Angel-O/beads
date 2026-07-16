//go:build windows

package atomicfile

// Windows does not expose a portable directory fsync through os.File. Atomic
// rename still prevents torn readers; the platform cannot provide the Unix
// durability strengthening.
func syncDirectory(string) error { return nil }
