//go:build !linux

package manifest

import "os"

// keepAttributes gives tmp the attributes of old that a rewrite keeps.
// This platform keeps none beyond the mode, the owner, and the group.
func keepAttributes(_, _ *os.File) error {
	return nil
}
