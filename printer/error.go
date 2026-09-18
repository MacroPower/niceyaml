package printer

import (
	"strings"

	"go.jacobcolvin.com/niceyaml"
)

// PrintError renders err for a reader: its message, then the
// [niceyaml.SourceError.Detail] of every [*niceyaml.SourceError] in its
// tree, as [niceyaml.SourceErrors] finds them, each rendered by p with
// context lines of unchanged content on either side of each marked line.
// An error joined from one bound error per document therefore prints an
// excerpt for each document. Blank lines separate the parts. The message
// is [error.Error] as it is, so the context a wrapper added stays in front
// of the position:
//
//	fmt.Println(p.PrintError(err, 3))
//
// An error whose tree holds no SourceError, or whose Details are empty,
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

	for _, bound := range niceyaml.SourceErrors(err) {
		if detail := bound.Detail(p, context); detail != "" {
			parts = append(parts, detail)
		}
	}

	return strings.Join(parts, "\n\n")
}
