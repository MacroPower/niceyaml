//go:build !unix

package manifest

import (
	"io/fs"
	"os"
)

// writable reports whether the process can write the file at path. The
// mode of the file decides on this platform.
func writable(string) bool {
	return true
}

// linkCount returns the number of hard links of the file that info
// describes. This platform reports one.
func linkCount(fs.FileInfo) int {
	return 1
}

// keepOwner gives tmp the owner and the group of the file that info
// describes. This platform sets neither.
func keepOwner(*os.File, fs.FileInfo) error {
	return nil
}
