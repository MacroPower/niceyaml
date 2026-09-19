package segment

import (
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
// accessors return the stored pointers, so callers treat them as read-only.
//
// Create instances with [New].
type Segment struct {
	// The original token from the lexer, shared by every segment cut from it.
	source *token.Token

	// The portion of source on this line, with its position adjusted. For a
	// single-line token it holds the same content as source.
	part *token.Token

	// The rune count of part.Origin without its line ending.
	width int
}

// New creates a new [Segment] and caches the part's width.
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

// Source returns the shared original token.
func (s Segment) Source() *token.Token {
	return s.source
}

// Part returns the shared part token.
func (s Segment) Part() *token.Token {
	return s.part
}

// Contains reports whether tk is this segment's source or part token, or
// a copy of either as [token.Token.Clone] makes one: a token of the same
// type with the same value, origin, and position. The AST a parser builds
// holds such copies, so a token taken from a node finds its segment.
func (s Segment) Contains(tk *token.Token) bool {
	if tk == nil {
		return false
	}

	return sameToken(tk, s.source) || sameToken(tk, s.part)
}

// sameToken reports whether a and b are one token or copies of one: the
// same pointer, or the same type, value, origin, and position.
func sameToken(a, b *token.Token) bool {
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
// segment start, excluding leading and trailing spaces. The span is empty
// when the part holds only spaces or the segment has no part.
func (s Segment) ContentSpan() position.Span {
	if s.part == nil {
		return position.Span{}
	}

	origin := tokens.TrimLineEnding(s.part.Origin)
	leading := len(origin) - len(strings.TrimLeft(origin, " "))
	trailing := len(origin) - len(strings.TrimRight(origin, " "))

	end := s.width - trailing
	if end < leading {
		return position.NewSpan(leading, leading)
	}

	return position.NewSpan(leading, end)
}

// Segments is one line's worth of [Segment] values in column order.
type Segments []Segment

// Clone returns a copy of the slice that shares the tokens with the
// original, since neither modifies them after segmentation.
func (s Segments) Clone() Segments {
	if len(s) == 0 {
		return nil
	}

	result := make(Segments, len(s))
	copy(result, s)

	return result
}

// PartTokens returns every part token in order. The slice is new, but the
// tokens are shared.
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

// EndColumn returns the 1-indexed column just past the parts, which is the
// largest Column plus the width of the part that starts there. Returns 0
// when no part carries a position.
func (s Segments) EndColumn() int {
	col := 0

	for _, seg := range s {
		if seg.part == nil || seg.part.Position == nil {
			continue
		}

		col = max(col, seg.part.Position.Column+seg.width)
	}

	return col
}

// SourceTokenAt returns the source token covering the given 0-indexed
// column, or nil when no token does.
func (s Segments) SourceTokenAt(col int) *token.Token {
	c := 0

	for _, seg := range s {
		if col >= c && col < c+seg.width {
			return seg.source
		}

		c += seg.width
	}

	return nil
}
