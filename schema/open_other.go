//go:build !unix

package schema

import "os"

// openFile opens the file at abs for reading.
func openFile(abs string) (*os.File, error) {
	//nolint:gosec,wrapcheck // User-provided file paths are intentional; readFile wraps the error.
	return os.Open(abs)
}
