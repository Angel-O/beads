package safefile

// ValidateMetadataPath performs platform-specific lexical rejection required
// before resolving an untrusted path for metadata observation. It performs no
// filesystem access. Windows rejects raw/IPC namespaces, alternate data
// streams, and reserved DOS device components; other platforms are a no-op.
func ValidateMetadataPath(path string) error {
	return validateMetadataPath(path)
}
