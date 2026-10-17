//go:build !linux

package manifest

import "os"

// keepAttributes gives tmp the extended attributes of old that a rewrite
// keeps. A rewrite on this platform does not keep an extended attribute.
func keepAttributes(_, _ *os.File) error {
	return nil
}
