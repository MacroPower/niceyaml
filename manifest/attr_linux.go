package manifest

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// keptAttributes lists the extended attributes that a rewrite keeps, with
// the name that a message gives each.
var keptAttributes = []struct {
	name string
	what string
}{
	{name: "system.posix_acl_access", what: "access control list"},
	{name: "security.selinux", what: "SELinux label"},
}

// keepAttributes gives tmp the access control list and the SELinux label
// of old. For an attribute that it cannot give, it returns a problem that
// matches [ErrReadOnly].
func keepAttributes(tmp, old *os.File) error {
	for _, attr := range keptAttributes {
		value, err := getxattr(old, attr.name)
		if err != nil || value == nil {
			continue
		}

		err = unix.Fsetxattr(int(tmp.Fd()), attr.name, value, 0) //nolint:gosec // A descriptor fits an int.
		if err != nil {
			return unkeptProblem(err, "cannot keep the "+attr.what,
				errors.New("a rewrite replaces the file, and the new file cannot get this "+attr.what))
		}
	}

	return nil
}

// getxattr returns the value of the attribute name of f, or nil when f
// does not have it.
func getxattr(f *os.File, name string) ([]byte, error) {
	fd := int(f.Fd()) //nolint:gosec // A descriptor fits an int.

	size, err := unix.Fgetxattr(fd, name, nil)

	switch {
	case errors.Is(err, unix.ENODATA), errors.Is(err, unix.ENOTSUP):
		return nil, nil
	case err != nil:
		return nil, err //nolint:wrapcheck // The caller skips the attribute.
	}

	buf := make([]byte, size)

	n, err := unix.Fgetxattr(fd, name, buf)
	if err != nil {
		return nil, err //nolint:wrapcheck // The caller skips the attribute.
	}

	return buf[:n], nil
}
