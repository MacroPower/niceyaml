//go:build !unix

package main

import "os"

// fileIdentity reports false, because [os.FileInfo] does not expose a
// file identity on this platform. [fileSet] then compares files with
// [os.SameFile].
func fileIdentity(os.FileInfo) (fileID, bool) {
	return fileID{}, false
}
