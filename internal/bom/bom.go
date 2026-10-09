// Package bom holds the rule for which UTF-8 byte order marks the tokenizer
// drops from YAML text.
//
// YAML allows a mark at the start of a document, and the go-yaml lexer
// reads one as text and puts it in the first key. The tokenizer drops
// each mark YAML allows before it lexes, so every position counts the
// text without them. Code that maps a position back to the text it came
// from calls this package to learn where the text lost a mark.
//
//	text, marks := bom.Drop("\ufeffa: 1\n")
//	fmt.Printf("%q %v", text, marks) // "a: 1\n" [0]
package bom

import (
	"strings"

	"go.jacobcolvin.com/niceyaml/internal/lineend"
)

// Mark is the UTF-8 byte order mark.
const Mark = "\ufeff"

// Drop returns src without the marks the tokenizer drops. It also returns
// the byte offset in that text of each place it dropped a mark, in
// ascending order.
//
// It drops a mark that opens a line before the content of a document, and
// one that stands in front of a document marker. A document starts at the
// start of src and after a marker line that holds nothing but the marker
// and a comment. Blank and comment lines keep the document before its
// content. A line ends where the lexer ends one, as [lineend.Lines] cuts
// them.
func Drop(src string) (string, []int) {
	var (
		sb    strings.Builder
		marks []int
	)

	sb.Grow(len(src))

	prefix := true

	for line := range lineend.Lines(src) {
		if rest, ok := strings.CutPrefix(line, Mark); ok && (prefix || isDocumentMarker(rest)) {
			marks = append(marks, sb.Len())
			line = rest
		}

		sb.WriteString(line)

		switch {
		case isDocumentMarker(line):
			prefix = isBlankOrComment(line[len("---"):])
		case isBlankOrComment(line):
		default:
			prefix = false
		}
	}

	return sb.String(), marks
}

// isDocumentMarker reports whether line opens with a "---" or "..."
// marker followed by a space, a tab, or the end of the line.
func isDocumentMarker(line string) bool {
	if !strings.HasPrefix(line, "---") && !strings.HasPrefix(line, "...") {
		return false
	}

	rest := line[len("---"):]

	return rest == "" || strings.ContainsAny(rest[:1], " \t\r\n")
}

// isBlankOrComment reports whether line holds only whitespace, or a
// comment after it.
func isBlankOrComment(line string) bool {
	text := strings.TrimLeft(line, " \t")

	return strings.TrimRight(text, "\r\n") == "" || strings.HasPrefix(text, "#")
}
