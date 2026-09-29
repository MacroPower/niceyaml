//go:build !unix

package main

import "os"

// fileIdentity reports false, because this platform exposes no file
// identity through [os.FileInfo], so [fileSet] falls back to
// [os.SameFile].
func fileIdentity(os.FileInfo) (fileID, bool) {
	return fileID{}, false
}
