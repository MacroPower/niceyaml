//go:build !plan9 && !netbsd

package input

import (
	"errors"
	"syscall"
)

// metLink reports whether err holds a reason that an open gives when it
// meets a link that it does not follow. Most systems give ELOOP, and
// FreeBSD gives EMLINK. DragonFly might give EINVAL instead. The check
// leaves EINVAL out, because an open gives EINVAL for other problems too.
func metLink(err error) bool {
	return errors.Is(err, syscall.ELOOP) || errors.Is(err, syscall.EMLINK)
}
