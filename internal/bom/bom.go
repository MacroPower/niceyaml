// Package bom holds the rule for which UTF-8 byte order marks the tokenizer
// drops from YAML text.
//
// YAML allows a mark at the start of a document. The go-yaml lexer reads
// one as text and puts it in the first key. The tokenizer drops each mark
// that YAML allows before it lexes, so every position counts the text
// without them. Code that maps a position back to the text that it came
// from calls this package to learn where the text lost a mark. An
// [Index] maps a line and a column to a byte offset in that text, and to
// a column in UTF-16 code units.
//
//	text, marks := bom.Drop("\ufeffa: 1\n")
//	fmt.Printf("%q %v", text, marks) // "a: 1\n" [0]
package bom

import (
	"slices"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"go.jacobcolvin.com/niceyaml/internal/lineend"
)

// Mark is the UTF-8 byte order mark.
const Mark = "\ufeff"

// Drop returns src without the marks that the tokenizer drops. It also
// returns the byte offset in that text of each place where it dropped a
// mark, in ascending order.
//
// It drops a mark that opens a line before the content of a document, and
// one that stands in front of a document marker. A document starts at the
// start of src and after a marker line that holds only the marker and,
// optionally, a comment. Blank and comment lines keep the document before
// its content. A line ends where the lexer ends one, as [lineend.Lines]
// cuts them.
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

// ContentStart returns the byte offset in src of the start of its first
// line that is not blank or a comment, as [Drop] cuts lines. It returns
// the length of src when every line is blank or a comment. The lines up to
// that one come before the content of a document that src starts. So Drop
// drops a mark at the start of each of them.
func ContentStart(src string) int {
	at := 0

	for line := range lineend.Lines(src) {
		if !isBlankOrComment(line) {
			return at
		}

		at += len(line)
	}

	return len(src)
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

// Index maps a line and a column of the text that the tokenizer reads to
// a byte offset in the text before the drop. The text that the tokenizer
// reads does not hold the marks that it dropped. The lexer counts its lines and
// columns in the text without the marks, so the index counts in that text
// and then adds the marks back.
//
// Create instances with [NewIndex].
type Index struct {
	// The text without the marks that the tokenizer dropped.
	text string
	// The offset in text where each line starts, as the lexer cuts lines.
	// The end of a text that is empty or ends with a line ending starts
	// a last line that is empty.
	starts []int
	// The offset in text of each place where the tokenizer dropped a mark,
	// in ascending order.
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
// next line starts. The column past the last rune of a text without a
// final line ending is the length of the text.
//
// A mark that the tokenizer dropped lies before column 0 of its line.
// Offset reports false for a negative line or column, a line that the
// text does not hold, and a column past the end of its line.
//
// Offset reads the start of the line from the index and then walks the
// runes of the line up to col. The index does not hold the offsets of the
// runes within a line, so a call takes time in proportion to col.
func (x Index) Offset(line, col int) (int, bool) {
	if col < 0 {
		return 0, false
	}

	start, end, content, ok := x.line(line)
	if !ok {
		return 0, false
	}

	at, n := 0, 0
	for ; n < col && at < len(content); n++ {
		_, size := utf8.DecodeRuneInString(content[at:])
		at += size
	}

	switch {
	case n == col:
		at += start
	case n+1 == col && start+len(content) < end:
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

// UTF16Column returns the column in UTF-16 code units of the rune at line
// and col, both 0-indexed, and true. It counts the content of the line in
// front of col in UTF-16, so a rune outside the Basic Multilingual Plane
// takes two units and every other rune takes one. A line ending takes
// one unit, and so does a byte that is not valid UTF-8. A mark that the
// tokenizer dropped lies before unit 0 of its line.
//
// UTF16Column reports false where [Index.Offset] does. It walks the runes
// of the line up to col, as Offset walks them.
func (x Index) UTF16Column(line, col int) (int, bool) {
	if col < 0 {
		return 0, false
	}

	start, end, content, ok := x.line(line)
	if !ok {
		return 0, false
	}

	at, n, units := 0, 0, 0
	for ; n < col && at < len(content); n++ {
		r, size := utf8.DecodeRuneInString(content[at:])
		at += size
		units += utf16.RuneLen(r)
	}

	switch {
	case n == col:
		return units, true
	case n+1 == col && start+len(content) < end:
		// The line ending takes the unit after the content.
		return units + 1, true
	default:
		return 0, false
	}
}

// ColumnAtUTF16 returns the column in runes of the rune that holds the
// UTF-16 code unit at units on line, both 0-indexed, and true. It is the
// inverse of [Index.UTF16Column] and counts as UTF16Column counts. The
// second unit of a surrogate pair thus has the column of its rune.
//
// ColumnAtUTF16 reports false for a negative line or units, a line that
// the text does not hold, and a unit past the end of its line. It walks
// the runes of the line up to units.
func (x Index) ColumnAtUTF16(line, units int) (int, bool) {
	if units < 0 {
		return 0, false
	}

	start, end, content, ok := x.line(line)
	if !ok {
		return 0, false
	}

	at, col := 0, 0
	for units > 0 && at < len(content) {
		r, size := utf8.DecodeRuneInString(content[at:])

		n := utf16.RuneLen(r)
		if units < n {
			// The unit lies inside the surrogate pair of this rune.
			return col, true
		}

		at += size
		units -= n
		col++
	}

	switch {
	case units == 0:
		return col, true
	case units == 1 && start+len(content) < end:
		// The line ending takes the unit after the content.
		return col + 1, true
	default:
		return 0, false
	}
}

// line returns the offsets in the text of the start of line and of the
// start of the line after it, the content of line without its line
// ending, and true. It reports false for a line that the text does not
// hold.
func (x Index) line(line int) (int, int, string, bool) {
	if line < 0 || line >= len(x.starts) {
		return 0, 0, "", false
	}

	start, end := x.starts[line], len(x.text)
	if line+1 < len(x.starts) {
		end = x.starts[line+1]
	}

	return start, end, trimLineEnding(x.text[start:end]), true
}

// trimLineEnding returns s without the line ending at its end, if any.
func trimLineEnding(s string) string {
	return strings.TrimSuffix(strings.TrimSuffix(s, "\n"), "\r")
}

// Position returns the line and the column of the rune that holds the
// byte at off in the text before the drop, both 0-indexed, and true. A
// column counts runes as [Index.Offset] counts them, so each byte of a
// multi-byte rune has the column of that rune, and each byte of a line
// ending has the column after the content of its line. A byte of a mark
// that the tokenizer dropped has column 0 of its line. The length of the
// text has the column past the last rune, or column 0 of the empty last
// line of a text that ends with a line ending.
//
// Position reports false for a negative off and for one past the length
// of the text.
//
// Position finds the line by search and then walks the runes of the line
// up to off, as [Index.Offset] walks them up to a column.
func (x Index) Position(off int) (int, int, bool) {
	if off < 0 {
		return 0, 0, false
	}

	// Each mark the tokenizer dropped takes len(Mark) bytes of the text
	// before the drop, so the offset in the text after the drop lies that
	// many bytes earlier for each mark before off.
	at := off

	for i, mark := range x.marks {
		start := mark + i*len(Mark)

		switch {
		case off >= start+len(Mark):
			at -= len(Mark)
		case off >= start:
			at = mark
		}
	}

	if at > len(x.text) {
		return 0, 0, false
	}

	line, _ := slices.BinarySearch(x.starts, at+1)
	line--

	start, _, content, _ := x.line(line)
	rel := at - start
	col := 0

	for n := 0; n < len(content); col++ {
		_, size := utf8.DecodeRuneInString(content[n:])
		if n+size > rel {
			break
		}

		n += size
	}

	return line, col, true
}
