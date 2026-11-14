package input

import (
	"errors"
	"syscall"
)

// metLink reports whether err holds a reason that an open gives when it
// meets a link that it does not follow. NetBSD gives EFTYPE. The check
// also takes ELOOP and EMLINK, the reasons of other systems, because an
// [os.Root] counts all three as an open that met a link.
func metLink(err error) bool {
	return errors.Is(err, syscall.EFTYPE) || errors.Is(err, syscall.ELOOP) ||
		errors.Is(err, syscall.EMLINK)
}
