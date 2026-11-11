package input

import (
	"errors"
	"slices"
	"strings"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/filepaths"
)

var (
	// ErrOutsideLink indicates a symbolic link that a directory walk found
	// and does not follow. The target of the link lies outside the input
	// directory or does not match the include patterns, or the file system
	// does not report the link, as [FS] describes.
	ErrOutsideLink = errors.New("link leads outside the input directory")

	// ErrInvalidPattern indicates a pattern of [WithInclude] or
	// [WithExclude] that a directory input rejects. It is
	// [go.jacobcolvin.com/niceyaml/matcher.ErrInvalidPattern], so
	// either name matches the error.
	ErrInvalidPattern = filepaths.ErrInvalidPattern
)

// problemError is the error of a problem that a read writes itself: its
// message and the errors that it matches beside it.
type problemError struct {
	msg   string
	marks []error
}

// Error returns the message of e.
func (e *problemError) Error() string {
	return e.msg
}

// Is reports whether target matches one of the marks of e.
func (e *problemError) Is(target error) bool {
	return slices.ContainsFunc(e.marks, func(m error) bool { return errors.Is(m, target) })
}

// newProblem returns the problem msg, which matches mark, with opts. The
// file is at fault for it.
func newProblem(mark error, msg string, opts ...niceyaml.ErrorOption) error {
	return niceyaml.Invalid(&problemError{msg: msg, marks: []error{mark}}, opts...)
}

// joinOr returns items as a list in prose, as in "*.yaml or *.yml" or
// "A, B, or C".
func joinOr(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " or " + items[1]
	default:
		return strings.Join(items[:len(items)-1], ", ") + ", or " + items[len(items)-1]
	}
}
