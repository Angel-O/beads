package safefile

import (
	"errors"
	"os"
)

// MetadataObservation binds object metadata and canonical stored-case path to
// one no-follow handle. Case sensitivity describes child lookup when Info is a
// directory, allowing a caller to append and compare one unobserved missing
// leaf; callers must treat an unknown value as case-sensitive. LinkCount is
// derived from the same handle and is valid only when LinkCountKnown is true.
// The observation pins neither the pathname nor its ancestors after return.
type MetadataObservation struct {
	CanonicalPath        string
	Info                 os.FileInfo
	CaseSensitive        bool
	CaseSensitivityKnown bool
	LinkCount            uint64
	LinkCountKnown       bool
}

// OpenedFileLinkCount returns the link count bound to an already-open file
// handle and its FileInfo snapshot. The count is valid only when known is
// true. Callers that use link count as a safety boundary must fail closed when
// it is unavailable.
func OpenedFileLinkCount(file *os.File, info os.FileInfo) (count uint64, known bool) {
	if file == nil || info == nil {
		return 0, false
	}
	return metadataLinkCount(file, info)
}

// ObserveMetadataNoFollow returns a single-handle metadata observation without
// acquiring data-read access. Ancestor links remain followable; only the final
// component is no-follow. Platforms without a canonical metadata-only
// observation return errors.ErrUnsupported.
func ObserveMetadataNoFollow(path string) (*MetadataObservation, error) {
	return observeMetadataNoFollow(path)
}

type metadataHandleInspector func(*os.File, os.FileInfo) (canonicalPath string, caseSensitive, caseSensitivityKnown bool, err error)

func observeOpenedMetadata(file *os.File, inspector metadataHandleInspector) (*MetadataObservation, error) {
	if file == nil {
		return nil, errors.New("metadata observation received a nil handle")
	}
	info, statErr := file.Stat()
	var canonicalPath string
	caseSensitive := true
	caseSensitivityKnown := false
	linkCount := uint64(0)
	linkCountKnown := false
	var inspectErr error
	if statErr == nil {
		linkCount, linkCountKnown = OpenedFileLinkCount(file, info)
		canonicalPath, caseSensitive, caseSensitivityKnown, inspectErr = inspector(file, info)
	}
	closeErr := file.Close()
	switch {
	case statErr != nil:
		return nil, statErr
	case inspectErr != nil:
		return nil, inspectErr
	case closeErr != nil:
		return nil, closeErr
	case canonicalPath == "":
		return nil, errors.New("metadata observation returned an empty canonical path")
	default:
		return &MetadataObservation{
			CanonicalPath:        canonicalPath,
			Info:                 info,
			CaseSensitive:        caseSensitive,
			CaseSensitivityKnown: caseSensitivityKnown,
			LinkCount:            linkCount,
			LinkCountKnown:       linkCountKnown,
		}, nil
	}
}
