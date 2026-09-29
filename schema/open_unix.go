//go:build unix

package schema

import (
	"os"
	"syscall"
)

// openFile opens the file at abs for reading. It opens with O_NONBLOCK,
// so a FIFO that replaces the file after the caller's check opens at
// once rather than waiting for a writer, and [readBounded] refuses it.
// The flag leaves reads of a regular file unchanged.
func openFile(abs string) (*os.File, error) {
	//nolint:gosec,wrapcheck // User-provided file paths are intentional; readFile wraps the error.
	return os.OpenFile(abs, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
