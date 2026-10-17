// Package limitread reads a text up to a byte limit. It keeps one byte
// past the limit, so a caller can tell a text past the limit from a text
// at the limit.
package limitread

import (
	"io"
	"math"
)

// ReadAll reads r to its end or to one byte past limit, whichever comes
// first. A result longer than limit means that the text holds more than
// limit bytes. A limit of [math.MaxInt] reads r to its end.
func ReadAll(r io.Reader, limit int) ([]byte, error) {
	n := int64(limit)
	if n < math.MaxInt64 {
		n++
	}

	//nolint:wrapcheck // The caller wraps the error.
	return io.ReadAll(io.LimitReader(r, n))
}
