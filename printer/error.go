package printer

import (
	"errors"
	"strings"

	"go.jacobcolvin.com/niceyaml"
)

// PrintError renders err for a reader: its message, then, when its chain
// holds a [*niceyaml.SourceError], the [niceyaml.SourceError.Detail] of the
// first one, rendered by p with context lines of unchanged content on
// either side of each marked line. A blank line separates the two. The
// message is [error.Error] as it is, so the context a wrapper added stays
// in front of the position:
//
//	fmt.Println(p.PrintError(err, 3))
//
// An error whose chain holds no SourceError, or whose Detail is empty,
// prints as its message alone, and a nil err prints as "". The %+v verb
// prints the same parts as plain text with two lines of context.
func (p *Printer) PrintError(err error, context int) string {
	if err == nil {
		return ""
	}

	parts := make([]string, 0, 2)

	if msg := err.Error(); msg != "" {
		parts = append(parts, msg)
	}

	bound, ok := errors.AsType[*niceyaml.SourceError](err)
	if ok && bound != nil {
		if detail := bound.Detail(p, context); detail != "" {
			parts = append(parts, detail)
		}
	}

	return strings.Join(parts, "\n\n")
}
