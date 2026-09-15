package line

import (
	"strings"
	"unicode/utf8"

	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/tokens"
)

// segment pairs an original source token with the part of it that sits on
// one line.
//
// Every segment cut from a multi-line token shares the same source pointer
// and holds a distinct part. A segment never modifies its tokens, and its
// accessors return the stored pointers, so callers treat them as read-only.
type segment struct {
	// The original token from the lexer, shared by every segment cut from it.
	source *token.Token

	// The portion of source on this line, with its position adjusted. For a
	// single-line token it holds the same content as source.
	part *token.Token

	// The rune count of part.Origin without its line ending.
	width int
}

// newSegment creates a new [segment] and caches the part's width.
func newSegment(source, part *token.Token) segment {
	var w int

	if part != nil {
		w = utf8.RuneCountInString(tokens.TrimLineEnding(part.Origin))
	}

	return segment{source: source, part: part, width: w}
}

// Width returns the rune count of the part, excluding its line ending.
func (s segment) Width() int {
	return s.width
}

// Source returns the shared original token.
func (s segment) Source() *token.Token {
	return s.source
}

// Part returns the shared part token.
func (s segment) Part() *token.Token {
	return s.part
}

// Contains reports whether tk is this segment's source or part token.
func (s segment) Contains(tk *token.Token) bool {
	return s.source == tk || s.part == tk
}

// contentSpan returns the columns of the part's content relative to the
// segment start, excluding leading and trailing spaces. The span is empty
// when the part holds only spaces.
func (s segment) contentSpan() position.Span {
	origin := tokens.TrimLineEnding(s.part.Origin)
	leading := len(origin) - len(strings.TrimLeft(origin, " "))
	trailing := len(origin) - len(strings.TrimRight(origin, " "))

	end := s.width - trailing
	if end < leading {
		return position.NewSpan(leading, leading)
	}

	return position.NewSpan(leading, end)
}

// segments is one line's worth of [segment] values in column order.
type segments []segment

// Clone returns a copy of the slice that shares the tokens with the
// original, since neither modifies them after segmentation.
func (s segments) Clone() segments {
	if len(s) == 0 {
		return nil
	}

	result := make(segments, len(s))
	copy(result, s)

	return result
}

// PartTokens returns every part token in order. The slice is new, but the
// tokens are shared.
func (s segments) PartTokens() token.Tokens {
	if len(s) == 0 {
		return nil
	}

	result := make(token.Tokens, 0, len(s))
	for _, seg := range s {
		result = append(result, seg.part)
	}

	return result
}

// lastColumn returns the largest 1-indexed Column among the parts, which is
// where the last part starts. Returns 0 when no part carries a position.
func (s segments) lastColumn() int {
	col := 0

	for _, seg := range s {
		if seg.part != nil && seg.part.Position != nil && seg.part.Position.Column > col {
			col = seg.part.Position.Column
		}
	}

	return col
}

// SourceTokenAt returns the source token covering the given 0-indexed
// column, or nil when no token does.
func (s segments) SourceTokenAt(col int) *token.Token {
	c := 0

	for _, seg := range s {
		if col >= c && col < c+seg.width {
			return seg.source
		}

		c += seg.width
	}

	return nil
}
