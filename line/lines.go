package line

import (
	"iter"
	"strings"

	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/segment"
	"go.jacobcolvin.com/niceyaml/position"
)

// Lines is an ordered collection of [*Line] values: the content that a
// [View] decorates and that the finder and diff packages read.
//
// Lines carries the tokens split per line and nothing else. It has no
// knowledge of YAML documents, parsing, or files, so a Lines value may
// describe content that is not a YAML document at all, such as a diff that
// interleaves lines from two revisions. The lines never change after
// creation, so a Lines value is safe to share between views and
// goroutines, and a view over it costs nothing to create.
//
// Create instances with [NewLines], and reach a line by indexing the
// collection or by ranging over [Lines.AllLines]. Never put a nil pointer
// in it.
type Lines []*Line

// NewLines creates new [Lines] from [token.Tokens], one [Line] per source
// line.
//
// NewLines cuts multiline tokens, such as block scalars and quoted multiline
// strings, into one part per line. Each part is a token whose Position
// describes its own line, following the go-yaml lexer conventions for that
// token type, and every part keeps a reference to the original token it was
// cut from. [Lines.Tokens] recombines the parts into the original tokens.
// Returns nil when tks is empty.
func NewLines(tks token.Tokens) Lines {
	split := segment.Split(tks)
	if len(split) == 0 {
		return nil
	}

	lines := make(Lines, len(split))
	for i, l := range split {
		lines[i] = &Line{segments: l.Segments, number: l.Number}
	}

	return lines
}

// Len returns the number of lines.
func (ls Lines) Len() int {
	return len(ls)
}

// IsEmpty reports whether there are no lines.
func (ls Lines) IsEmpty() bool {
	return len(ls) == 0
}

// Width returns the maximum [Line.Width] across all lines.
func (ls Lines) Width() int {
	var maxWidth int

	for _, l := range ls {
		if w := l.Width(); w > maxWidth {
			maxWidth = w
		}
	}

	return maxWidth
}

// AllLines returns an iterator over lines within the given spans.
//
// Without spans, AllLines yields every line. Each iteration yields the
// 0-indexed line index and the [*Line] at that index. AllLines clamps
// spans to the available lines.
func (ls Lines) AllLines(spans ...position.Span) iter.Seq2[int, *Line] {
	return func(yield func(int, *Line) bool) {
		if len(spans) == 0 {
			for i := range ls {
				if !yield(i, ls[i]) {
					return
				}
			}

			return
		}

		for _, span := range spans {
			start := max(0, span.Start)
			end := min(len(ls), span.End)

			for i := start; i < end; i++ {
				if !yield(i, ls[i]) {
					return
				}
			}
		}
	}
}

// AllRunes returns an iterator over runes within the given ranges.
//
// Without ranges, AllRunes yields every rune. Each iteration yields a
// [position.Position] and the rune at that position. The iteration includes
// line endings as a single '\n', as [Line.Runes] does, so a newline
// occupies the column after the last visible rune whether the source used LF
// or CRLF, and columns match [Line.Width].
func (ls Lines) AllRunes(ranges ...position.Range) iter.Seq2[position.Position, rune] {
	return func(yield func(position.Position, rune) bool) {
		if len(ranges) == 0 {
			for i := range ls {
				if !ls.yieldRunes(i, nil, yield) {
					return
				}
			}

			return
		}

		for _, rng := range ranges {
			startLine := max(0, rng.Start.Line)
			endLine := min(len(ls)-1, rng.End.Line)

			for i := startLine; i <= endLine; i++ {
				if !ls.yieldRunes(i, &rng, yield) {
					return
				}
			}
		}
	}
}

// yieldRunes yields every rune of the line at index lineIdx as a position.
// When rng is non-nil, it yields only the runes inside it. Returns false when
// yield stops the iteration.
func (ls Lines) yieldRunes(lineIdx int, rng *position.Range, yield func(position.Position, rune) bool) bool {
	for col, r := range ls[lineIdx].Runes() {
		pos := position.New(lineIdx, col)

		if rng != nil && !rng.Contains(pos) {
			continue
		}

		if !yield(pos, r) {
			return false
		}
	}

	return true
}

// Tokens reconstructs the full [token.Tokens] stream from all lines.
//
// For multiline tokens that were split across lines, Tokens recombines them by
// returning the original token once. The slice is new, but the tokens are the
// originals. Treat them as read-only.
func (ls Lines) Tokens() token.Tokens {
	if len(ls) == 0 {
		return nil
	}

	result := token.Tokens{}

	var last *token.Token

	for _, l := range ls {
		for _, src := range l.SourceTokens() {
			if src != last {
				result = append(result, src)
				last = src
			}
		}
	}

	return result
}

// TokenAt returns the original [*token.Token] covering the given position.
//
// The token is the one the lexer produced, so it can be passed back to
// [Lines.TokenRanges] or [Lines.ContentRanges] to find every range it
// occupies. Treat it as read-only.
//
// Returns nil if the position is out of bounds or no token exists there.
func (ls Lines) TokenAt(pos position.Position) *token.Token {
	if pos.Line < 0 || pos.Line >= len(ls) {
		return nil
	}

	return ls[pos.Line].TokenAt(pos.Col)
}

// TokenRanges returns the ranges tk occupies, one per line where it holds
// visible runes. A line where tk holds only a line ending, such as a blank
// line kept by a block scalar, contributes no range.
//
// The token may be a lexer token, as returned by [Lines.TokenAt] or
// [Lines.Tokens], or one of the per-line parts from [Line.Tokens].
// Returns nil if tk is nil or not found.
func (ls Lines) TokenRanges(tk *token.Token) position.Ranges {
	return ls.ranges(tk, (*Line).TokenSpan)
}

// ContentRanges returns the ranges of tk's content, one per line it appears
// on, excluding leading and trailing spaces. A line where tk holds only
// spaces contributes no range.
//
// The token may be a lexer token or one of the per-line parts, as for
// [Lines.TokenRanges]. Returns nil if tk is nil or not found.
func (ls Lines) ContentRanges(tk *token.Token) position.Ranges {
	return ls.ranges(tk, (*Line).ContentSpan)
}

// ranges collects one range per line that holds tk, using span to pick the
// columns within the line. Lines where span is empty contribute no range.
func (ls Lines) ranges(
	tk *token.Token, span func(*Line, *token.Token) (position.Span, bool),
) position.Ranges {
	if tk == nil {
		return nil
	}

	var result position.Ranges

	for i := range ls {
		sp, ok := span(ls[i], tk)
		if !ok || sp.Len() <= 0 {
			continue
		}

		result = append(result, position.NewRange(
			position.New(i, sp.Start),
			position.New(i, sp.End),
		))
	}

	return result
}

// Content returns the combined content of all lines as a string.
// Lines are joined with newlines.
func (ls Lines) Content() string {
	if len(ls) == 0 {
		return ""
	}

	sb := strings.Builder{}
	for i, l := range ls {
		if i > 0 {
			sb.WriteByte('\n')
		}

		sb.WriteString(l.Content())
	}

	return sb.String()
}

// String returns every line as [Line.String], one per row. This should
// generally only be used for debugging; [View.String] adds the annotations.
func (ls Lines) String() string {
	var sb strings.Builder

	for i, l := range ls {
		if i > 0 {
			sb.WriteByte('\n')
		}

		sb.WriteString(l.String())
	}

	return sb.String()
}
