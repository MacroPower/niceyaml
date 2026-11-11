package segment

import (
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/tokens"
)

// Segment pairs an original source token with the part of it that sits on
// one line.
//
// Every segment cut from a multi-line token shares the same source pointer
// and holds a distinct part. A segment never modifies its tokens, and its
// accessors return the stored pointers, so callers must treat them as
// read-only.
//
// Create instances with [New].
type Segment struct {
	// The original token from the lexer. Every segment cut from it holds
	// the same pointer.
	source *token.Token

	// The part of source that sits on this line, with a Position of its
	// own. For a single-line token it holds the same content as source.
	part *token.Token

	// The 0-indexed column of the line where part starts. It is 0 until
	// [NewSegments] or [Split] places the segment on its line.
	start int

	// The rune count of part.Origin without its line ending.
	width int
}

// New creates a new [Segment] and caches the part's width. The segment
// starts at column 0 until [NewSegments] or [Split] places it on its line.
func New(source, part *token.Token) Segment {
	var w int

	if part != nil {
		w = utf8.RuneCountInString(tokens.TrimLineEnding(part.Origin))
	}

	return Segment{source: source, part: part, width: w}
}

// Width returns the rune count of the part, excluding its line ending.
func (s Segment) Width() int {
	return s.width
}

// Start returns the 0-indexed column of the line where the part starts.
func (s Segment) Start() int {
	return s.start
}

// Source returns the shared original token.
func (s Segment) Source() *token.Token {
	return s.source
}

// Part returns the shared part token.
func (s Segment) Part() *token.Token {
	return s.part
}

// Contains reports whether tk is this segment's source or part token, or
// a copy of either as [token.Token.Clone] makes one. Such a copy has the
// same type, value, origin, and position. The AST a parser builds holds
// such copies, so a token taken from a node finds its segment.
func (s Segment) Contains(tk *token.Token) bool {
	if tk == nil {
		return false
	}

	return SameToken(tk, s.source) || SameToken(tk, s.part)
}

// SameToken reports whether a and b are one token or copies of one: the
// same pointer, or the same type, value, origin, and position. The parser
// builds the AST from clones of the tokens of a document, so a token from
// a node matches its original by these fields rather than by pointer.
func SameToken(a, b *token.Token) bool {
	if a == b {
		return true
	}

	if a == nil || b == nil {
		return false
	}

	return a.Type == b.Type &&
		a.Value == b.Value &&
		a.Origin == b.Origin &&
		samePosition(a.Position, b.Position)
}

// samePosition reports whether a and b name the same place: both nil, or
// the same line, column, and offset.
func samePosition(a, b *token.Position) bool {
	if a == nil || b == nil {
		return a == b
	}

	return a.Line == b.Line && a.Column == b.Column && a.Offset == b.Offset
}

// ContentSpan returns the columns of the part's content relative to the
// segment start, excluding leading and trailing spaces and tabs. The span
// is empty when the part holds only whitespace or the segment lacks a
// part.
func (s Segment) ContentSpan() position.Span {
	if s.part == nil {
		return position.Span{}
	}

	origin := tokens.TrimLineEnding(s.part.Origin)
	leading := len(origin) - len(strings.TrimLeft(origin, " \t"))
	trailing := len(origin) - len(strings.TrimRight(origin, " \t"))

	end := s.width - trailing
	if end < leading {
		return position.NewSpan(leading, leading)
	}

	return position.NewSpan(leading, end)
}

// Segments is one line's worth of [Segment] values in column order. Each
// segment starts at the column where the segment before it ends, so the
// segments cover the columns of the line without a gap.
//
// Create instances with [NewSegments]. [Split] returns the Segments of
// each line it cuts.
type Segments []Segment

// NewSegments creates new [Segments] that hold segs, the segments of one
// line in column order. It sets the column where each segment starts and
// keeps the slice it receives.
func NewSegments(segs ...Segment) Segments {
	s := Segments(segs)
	s.place()

	return s
}

// place sets the column where each segment starts from the widths of the
// segments before it.
func (s Segments) place() {
	col := 0

	for i := range s {
		s[i].start = col
		col += s[i].width
	}
}

// Width returns the rune count of the line, excluding its line ending.
func (s Segments) Width() int {
	if len(s) == 0 {
		return 0
	}

	last := s[len(s)-1]

	return last.start + last.width
}

// PartTokens returns every part token in order. The slice is new, but it
// holds the same token pointers the segments hold.
func (s Segments) PartTokens() token.Tokens {
	if len(s) == 0 {
		return nil
	}

	result := make(token.Tokens, 0, len(s))
	for _, seg := range s {
		result = append(result, seg.part)
	}

	return result
}

// SourceTokenAt returns the source token covering the given 0-indexed
// column. It returns nil for a column outside the line. It searches the
// columns where the segments start, so it does not walk the line.
//
// The lexer bundles a line's indentation into the token that ends on the
// line before. So a column in that indentation resolves to the token
// whose text follows it on this line instead. The result is nil when such
// a token does not follow, as on a line that holds only whitespace before
// the line ending of the next token.
func (s Segments) SourceTokenAt(col int) *token.Token {
	// A segment ends where the next one starts, so the ends ascend. The
	// first segment that ends past col is thus the only one that can
	// cover it. A segment without width does not cover any column.
	i := sort.Search(len(s), func(i int) bool { return s[i].start+s[i].width > col })
	if i == len(s) || col < s[i].start {
		return nil
	}

	if !s[i].indentation() {
		return s[i].source
	}

	for _, next := range s[i+1:] {
		if next.width > 0 && !next.indentation() {
			return next.source
		}
	}

	return nil
}

// indentation reports whether the part holds only spaces or tabs, without
// a line ending, as the indentation at the start of a line does.
func (s Segment) indentation() bool {
	return s.part != nil && isPureHorizontalWhitespace(s.part.Origin)
}
