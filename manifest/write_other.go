//go:build !unix

package manifest

import (
	"io/fs"
	"os"
)

// writable reports whether the process can write the file at path. It
// reports true on this platform, so the mode of the file decides.
func writable(string) bool {
	return true
}

// linkCount returns the number of hard links of the file that info
// describes. It returns 1 on this platform.
func linkCount(fs.FileInfo) int {
	return 1
}

// keepOwner gives tmp the owner and the group of the file that info
// describes. It does not set them on this platform.
func keepOwner(*os.File, fs.FileInfo) error {
	return nil
}
