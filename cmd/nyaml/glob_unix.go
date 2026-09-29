//go:build unix

package main

import (
	"os"
	"syscall"
)

// fileIdentity returns the device and inode numbers of the file that info
// describes. It reports false when info carries no [syscall.Stat_t].
func fileIdentity(info os.FileInfo) (fileID, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fileID{}, false
	}

	//nolint:unconvert // Dev is a signed 32-bit number on some platforms, such as darwin.
	return fileID{uint64(st.Dev), st.Ino}, true
}
