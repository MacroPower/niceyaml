// Package limitread reads a text up to a byte limit, keeping one byte past
// the limit so that a caller tells a text past the limit from one at it.
package limitread

import (
	"io"
	"math"
)

// ReadAll reads r to its end or to one byte past limit, whichever comes
// first. A caller tells a text of more than limit bytes by a length past
// limit. A limit of [math.MaxInt] reads r to its end.
func ReadAll(r io.Reader, limit int) ([]byte, error) {
	n := int64(limit)
	if n < math.MaxInt64 {
		n++
	}

	//nolint:wrapcheck // The caller wraps the error.
	return io.ReadAll(io.LimitReader(r, n))
}
