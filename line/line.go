package line

import (
	"fmt"
	"iter"
	"strings"

	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/segment"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/tokens"
)

// Line holds the tokens on one line of source. It is content alone: the
// flag, overlays, and annotations that rendering attaches to a line live
// on a [View], so one Line renders many ways without being copied.
//
// A Line never changes after [NewLines] creates it, so it is safe to share
// between views and goroutines. Every token a Line hands out, from
// [Line.Tokens], [Line.Token], [Line.TokenAt], or [Line.SourceTokens], is
// shared with the line. Treat them as read-only and call
// [token.Token.Clone] before modifying one.
//
// The zero value is an empty line with no number, which a side-by-side
// diff uses as the placeholder opposite an inserted or deleted line.
//
// Create instances with [NewLines], which cuts a token stream into one Line
// per source line.
type Line struct {
	segments segment.Segments

	// The 1-indexed line number used for display purposes.
	// This may differ from the first token's Position.Line for block scalars.
	number int
}

// Number returns the 1-indexed line number of this [Line], or 0 for the
// zero value.
func (l *Line) Number() int {
	return l.number
}

// Content returns this [Line]'s content as a string.
//
// Line endings (LF or CRLF) are stripped from each segment for a clean
// single-line representation.
func (l *Line) Content() string {
	var sb strings.Builder

	for _, seg := range l.segments {
		sb.WriteString(tokens.TrimLineEnding(seg.Part().Origin))
	}

	return sb.String()
}

// Tokens returns the [token.Tokens] for this [Line] with line-adjusted
// positions. Each token links to its neighbors on the line through Next and
// Prev, and the chain stops at the line boundary.
//
// The slice is new, but the tokens are shared with the line. Treat them as
// read-only.
func (l *Line) Tokens() token.Tokens {
	return l.segments.PartTokens()
}

// Token returns the [*token.Token] at the given index. The token is shared
// with the line, so treat it as read-only.
// Panics if idx is out of range.
func (l *Line) Token(idx int) *token.Token {
	return l.segments[idx].Part()
}

// SourceTokens returns the original lexer tokens that have a part on this
// [Line], in column order, each once. A token that spans several lines
// appears in the result of every line it touches.
//
// The slice is new, but the tokens are the originals. Treat them as
// read-only.
func (l *Line) SourceTokens() token.Tokens {
	if len(l.segments) == 0 {
		return nil
	}

	result := make(token.Tokens, 0, len(l.segments))

	var last *token.Token

	for _, seg := range l.segments {
		if src := seg.Source(); src != last {
			result = append(result, src)
			last = src
		}
	}

	return result
}

// TokenAt returns the original lexer token covering the given 0-indexed
// column, or nil when no token does. Treat it as read-only.
func (l *Line) TokenAt(col int) *token.Token {
	return l.segments.SourceTokenAt(col)
}

// TokenSpan returns the columns tk occupies on this [Line]. The token may be
// a lexer token or one of the per-line parts from [Line.Tokens]. The second
// result is false when tk is nil or has no part on this line.
func (l *Line) TokenSpan(tk *token.Token) (position.Span, bool) {
	return l.span(tk, func(seg segment.Segment) position.Span {
		return position.NewSpan(0, seg.Width())
	})
}

// ContentSpan returns the columns of tk's content on this [Line], excluding
// leading and trailing spaces. The span is empty when tk holds only spaces
// on this line. The token may be a lexer token or one of the per-line parts,
// as for [Line.TokenSpan]. The second result is false when tk is nil or has
// no part on this line.
func (l *Line) ContentSpan(tk *token.Token) (position.Span, bool) {
	return l.span(tk, segment.Segment.ContentSpan)
}

// span finds the segment holding tk and returns the columns that span picks
// within it, offset to the line.
func (l *Line) span(tk *token.Token, span func(segment.Segment) position.Span) (position.Span, bool) {
	if tk == nil {
		return position.Span{}, false
	}

	col := 0

	for _, seg := range l.segments {
		if seg.Contains(tk) {
			sp := span(seg)

			return position.NewSpan(col+sp.Start, col+sp.End), true
		}

		col += seg.Width()
	}

	return position.Span{}, false
}

// IsEmpty returns true if there are no tokens on this [Line].
func (l *Line) IsEmpty() bool {
	return len(l.segments) == 0
}

// Width returns the total rune width of this line's content.
func (l *Line) Width() int {
	var w int

	for _, seg := range l.segments {
		w += seg.Width()
	}

	return w
}

// Runes returns an iterator over the runes on this [Line]. Each iteration
// yields the 0-indexed column and the rune at that column.
//
// The iteration includes the line ending as a single '\n' at the column
// after the last visible rune, which is [Line.Width], whether the source
// used LF, CRLF, or a bare CR. A line yields at most one newline: the lexer
// sometimes repeats a line ending at the start of the next token, and that
// repeat sits on the same line as the ending it copies.
func (l *Line) Runes() iter.Seq2[int, rune] {
	return func(yield func(int, rune) bool) {
		col := 0

		for _, seg := range l.segments {
			for _, r := range tokens.TrimLineEnding(seg.Part().Origin) {
				if !yield(col, r) {
					return
				}

				col++
			}
		}

		if n := len(l.segments); n > 0 && hasLineEnding(l.segments[n-1].Part().Origin) {
			yield(col, '\n')
		}
	}
}

// hasLineEnding reports whether origin ends with "\n", "\r\n", or a bare
// "\r".
func hasLineEnding(origin string) bool {
	return strings.HasSuffix(origin, "\n") || strings.HasSuffix(origin, "\r")
}

// String returns the line number and content, as "   1 | key: value".
// This should generally only be used for debugging; [View.String] adds the
// annotations.
func (l *Line) String() string {
	return fmt.Sprintf("%4d | %s", l.Number(), l.Content())
}
