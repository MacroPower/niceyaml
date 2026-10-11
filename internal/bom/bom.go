// Package bom holds the rule for which UTF-8 byte order marks the tokenizer
// drops from YAML text.
//
// YAML allows a mark at the start of a document, and the go-yaml lexer
// reads one as text and puts it in the first key. The tokenizer drops
// each mark YAML allows before it lexes, so every position counts the
// text without them. Code that maps a position back to the text it came
// from calls this package to learn where the text lost a mark, and an
// [Index] maps a line and a column to a byte offset in that text.
//
//	text, marks := bom.Drop("\ufeffa: 1\n")
//	fmt.Printf("%q %v", text, marks) // "a: 1\n" [0]
package bom

import (
	"slices"
	"strings"
	"unicode/utf8"

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

// Index maps a line and a column of the text the tokenizer reads, which
// counts no mark it dropped, to a byte offset in the text before the
// drop. The lexer counts its lines and columns in the text without the
// marks, so the index counts in that text and then adds the marks back.
//
// Create instances with [NewIndex].
type Index struct {
	// The text without the marks the tokenizer dropped.
	text string
	// The offset in text where each line starts, as the lexer cuts lines.
	// The end of a text that is empty or ends with a line ending starts
	// a last line that holds nothing.
	starts []int
	// The offset in text of each place the tokenizer dropped a mark, in
	// ascending order.
	marks []int
}

// NewIndex creates a new [Index] of src, the text before the tokenizer
// drops its marks.
func NewIndex(src string) Index {
	text, marks := Drop(src)

	// A text that lost no mark equals src, which the caller holds already,
	// so the index reads src and holds no copy of it.
	if len(marks) == 0 {
		text = src
	}

	x := Index{text: text, marks: marks}

	at := 0
	for ln := range lineend.Lines(text) {
		x.starts = append(x.starts, at)
		at += len(ln)
	}

	if text == "" || trimLineEnding(text) != text {
		x.starts = append(x.starts, at)
	}

	return x
}

// Offset returns the byte offset of the rune at line and col, both
// 0-indexed, and true. A column counts runes, and a line ending takes one
// column whether it is LF, CRLF, or a bare CR. A byte that is not valid
// UTF-8 takes one column too. The column past a line ending is where the
// next line starts, and the column past the last rune of a text with no
// final line ending is the length of the text. A mark the tokenizer
// dropped lies before column 0 of its line. Offset reports false for a
// negative line or column, a line the text does not hold, and a column
// past the end of its line.
func (x Index) Offset(line, col int) (int, bool) {
	if line < 0 || col < 0 || line >= len(x.starts) {
		return 0, false
	}

	start, end := x.starts[line], len(x.text)
	if line+1 < len(x.starts) {
		end = x.starts[line+1]
	}

	whole := x.text[start:end]
	content := trimLineEnding(whole)

	at, n := 0, 0
	for ; n < col && at < len(content); n++ {
		_, size := utf8.DecodeRuneInString(content[at:])
		at += size
	}

	switch {
	case n == col:
		at += start
	case n+1 == col && len(content) < len(whole):
		// The line ending takes the column after the content, so the
		// column after that one is where the next line starts.
		at = end
	default:
		return 0, false
	}

	// Every mark the tokenizer dropped at or before the offset lies in
	// front of it in the text before the drop.
	dropped, _ := slices.BinarySearch(x.marks, at+1)

	return at + dropped*len(Mark), true
}

// trimLineEnding returns s without the line ending at its end, if any.
func trimLineEnding(s string) string {
	return strings.TrimSuffix(strings.TrimSuffix(s, "\n"), "\r")
}
