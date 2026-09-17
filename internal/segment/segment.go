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

// Contains reports whether tk is this segment's source or part token.
func (s Segment) Contains(tk *token.Token) bool {
	return s.source == tk || s.part == tk
}

// ContentSpan returns the columns of the part's content relative to the
// segment start, excluding leading and trailing spaces. The span is empty
// when the part holds only spaces.
func (s Segment) ContentSpan() position.Span {
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
