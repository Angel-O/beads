//go:build !unix

package sqlite

import "os"

func regularSingleLink(info os.FileInfo) bool {
	return info != nil && info.Mode().IsRegular()
}
