package line

import (
	"fmt"
	"iter"
	"slices"
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
// creation, and nothing outside this package can add to, remove from, or
// reorder a Lines value, so it is safe to share between views and
// goroutines, and a view over it costs nothing to create.
//
// Reach a line with [Lines.Line] or by ranging over [Lines.All], as
// with a [View]. The zero value holds no lines.
//
// Create instances with [NewLines], which cuts a token stream into one
// [Line] per source line, or [Collect], which gathers lines taken from
// other Lines values.
type Lines struct {
	lines []*Line
}

// NewLines creates new [Lines] from [token.Tokens], one [Line] per source
// line.
//
// NewLines cuts multiline tokens, such as block scalars and quoted multiline
// strings, into one part per line. Each part is a token whose Position
// describes its own line, following the go-yaml lexer conventions for that
// token type, and every part keeps a reference to the original token it was
// cut from. [Lines.Tokens] recombines the parts into the original tokens.
// Returns the zero Lines when tks is empty.
func NewLines(tks token.Tokens) Lines {
	split := segment.Split(tks)
	if len(split) == 0 {
		return Lines{}
	}

	lines := make([]*Line, len(split))
	for i, l := range split {
		lines[i] = &Line{segments: l.Segments, number: l.Number}
	}

	return Lines{lines: lines}
}

// Collect creates new [Lines] holding ls in the order given, such as the
// lines of two revisions a diff interleaves. The lines are shared with the
// Lines values they came from, so a [View] over the result finds them by
// identity as it finds them in the originals. Panics when a line is nil.
func Collect(ls ...*Line) Lines {
	for i, l := range ls {
		if l == nil {
			panic(fmt.Sprintf("line: Collect: line %d is nil", i))
		}
	}

	return Lines{lines: slices.Clone(ls)}
}

// Line returns the [*Line] at index i. Panics when i is outside the
// collection, as indexing a slice does.
func (ls Lines) Line(i int) *Line {
	return ls.lines[i]
}

// Len returns the number of lines.
func (ls Lines) Len() int {
	return len(ls.lines)
}

// IsEmpty reports whether there are no lines.
func (ls Lines) IsEmpty() bool {
	return len(ls.lines) == 0
}

// Width returns the maximum [Line.Width] across all lines.
func (ls Lines) Width() int {
	var maxWidth int

	for _, l := range ls.lines {
		if w := l.Width(); w > maxWidth {
			maxWidth = w
		}
	}

	return maxWidth
}

// All returns an iterator over lines within the given spans.
//
// Without spans, All yields every line. Each iteration yields the
// 0-indexed line index and the [*Line] at that index. All clamps
// spans to the available lines.
func (ls Lines) All(spans ...position.Span) iter.Seq2[int, *Line] {
	return func(yield func(int, *Line) bool) {
		if len(spans) == 0 {
			for i := range ls.lines {
				if !yield(i, ls.lines[i]) {
					return
				}
			}

			return
		}

		for _, span := range spans {
			start := max(0, span.Start)
			end := min(len(ls.lines), span.End)

			for i := start; i < end; i++ {
				if !yield(i, ls.lines[i]) {
					return
				}
			}
		}
	}
}

// Runes returns an iterator over runes within the given ranges.
//
// Without ranges, Runes yields every rune. Each iteration yields a
// [position.Position] and the rune at that position. The iteration includes
// line endings as a single '\n', as [Line.Runes] does, so a newline
// occupies the column after the last visible rune whether the source used LF
// or CRLF, and columns match [Line.Width].
func (ls Lines) Runes(ranges ...position.Range) iter.Seq2[position.Position, rune] {
	return func(yield func(position.Position, rune) bool) {
		if len(ranges) == 0 {
			for i := range ls.lines {
				if !ls.yieldRunes(i, nil, yield) {
					return
				}
			}

			return
		}

		for _, rng := range ranges {
			startLine := max(0, rng.Start.Line)
			endLine := min(len(ls.lines)-1, rng.End.Line)

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
	for col, r := range ls.lines[lineIdx].Runes() {
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
// Each original token appears once, where it first occurs, so a multiline
// token split across lines returns as the single token the lexer produced,
// and so does a token a line repeats. The slice is new, but the tokens are
// the originals. Treat them as read-only.
func (ls Lines) Tokens() token.Tokens {
	if len(ls.lines) == 0 {
		return nil
	}

	var (
		result = token.Tokens{}
		seen   = map[*token.Token]struct{}{}
	)

	for _, l := range ls.lines {
		for _, src := range l.SourceTokens() {
			if _, ok := seen[src]; ok {
				continue
			}

			seen[src] = struct{}{}

			result = append(result, src)
		}
	}

	return result
}

// SliceLines splits r into one [position.Range] per line of the [Lines]
// it covers, each holding the columns of r on that line: from the start
// column on the first line and from column 0 on every later one, to the
// end column on the last line and to [Line.Width] on every earlier one.
// The result is what [View.AddOverlay] marks for r, so a caller that
// inspects or compares the per-line ranges sees the columns the lines
// hold.
//
// A range that ends at column 0 of a later line covers nothing on that
// line, as [position.Range.LastLine] counts it, and a range that ends
// before its start covers no lines. Lines outside the collection, columns
// before 0, and columns past the width of a line are left out, and a line
// on which r covers no column contributes no range. Returns nil when no
// range remains.
func (ls Lines) SliceLines(r position.Range) position.Ranges {
	var result position.Ranges

	for i := max(0, r.Start.Line); i <= min(r.LastLine(), len(ls.lines)-1); i++ {
		start, end := 0, ls.lines[i].Width()

		if i == r.Start.Line {
			start = max(0, r.Start.Col)
		}

		if i == r.End.Line {
			end = min(end, r.End.Col)
		}

		if end <= start {
			continue
		}

		result = append(result, position.NewRange(position.New(i, start), position.New(i, end)))
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
	if pos.Line < 0 || pos.Line >= len(ls.lines) {
		return nil
	}

	return ls.lines[pos.Line].TokenAt(pos.Col)
}

// TokenRanges returns the ranges tk occupies, one per line where it holds
// visible runes. A line where tk holds only a line ending, such as a blank
// line kept by a block scalar, contributes no range.
//
// The token may be a lexer token, as returned by [Lines.TokenAt] or
// [Lines.Tokens], one of the per-line parts from [Line.Tokens], or a copy
// of either, such as a token taken from the AST a parser built from the
// same stream. A token matches by its type, value, origin, and position
// rather than by pointer. Returns nil if tk is nil or not found.
func (ls Lines) TokenRanges(tk *token.Token) position.Ranges {
	return ls.ranges(tk, (*Line).TokenSpan)
}

// ContentRanges returns the ranges of tk's content, one per line it appears
// on, excluding leading and trailing spaces. A line where tk holds only
// spaces contributes no range.
//
// The token may be a lexer token, one of the per-line parts, or a copy of
// either, as for [Lines.TokenRanges]. Returns nil if tk is nil or not
// found.
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

	for i := range ls.lines {
		sp, ok := span(ls.lines[i], tk)
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
	if len(ls.lines) == 0 {
		return ""
	}

	sb := strings.Builder{}
	for i, l := range ls.lines {
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

	for i, l := range ls.lines {
		if i > 0 {
			sb.WriteByte('\n')
		}

		sb.WriteString(l.String())
	}

	return sb.String()
}
