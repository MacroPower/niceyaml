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

// Line holds the tokens on one line of source together with the metadata
// rendering utilities attach to it: a [Flag], [Overlays], and [Annotations].
//
// Create instances with [NewLines], which cuts a token stream into one Line
// per source line. Every token a Line hands out, from [Line.Tokens],
// [Line.Token], [Line.TokenAt], or [Line.SourceTokens], is shared with the
// line. Treat them as read-only and call [token.Token.Clone] before modifying
// one.
//
// The metadata changes through pointer methods, so index a [Lines]
// collection to reach a line that keeps the change. A Line yielded by value,
// as [Lines.AllLines] does, is a copy, and a change to it reaches nothing.
type Line struct {
	annotations Annotations
	overlays    Overlays
	segments    segment.Segments
	flag        Flag

	// The 1-indexed line number used for display purposes.
	// This may differ from the first token's Position.Line for block scalars.
	number int
}

// Flag returns the [Flag] of this [Line]. The zero value is [FlagDefault].
func (l *Line) Flag() Flag {
	return l.flag
}

// SetFlag sets the [Flag] of this [Line].
func (l *Line) SetFlag(f Flag) {
	l.flag = f
}

// Annotations returns the [Annotation] values on this [Line], in the order
// they were added. The slice is shared with the line, so treat it as
// read-only and add to it with [Line.AddAnnotation].
func (l *Line) Annotations() Annotations {
	return l.annotations
}

// AddAnnotation adds the given [Annotation] values to this [Line].
func (l *Line) AddAnnotation(ann ...Annotation) {
	l.annotations = append(l.annotations, ann...)
}

// Overlays returns the [Overlay] values on this [Line], in the order they
// were added. The slice is shared with the line, so treat it as read-only
// and add to it with [Line.AddOverlay] or the [Lines] overlay methods, which
// clamp a range to the line.
func (l *Line) Overlays() Overlays {
	return l.overlays
}

// AddOverlay adds the given [Overlay] values to this [Line] as given.
// [Lines.AddOverlay] and [Lines.BlendOverlay] clamp a range to the lines it
// covers before adding; AddOverlay does not.
func (l *Line) AddOverlay(o ...Overlay) {
	l.overlays = append(l.overlays, o...)
}

// ClearOverlays removes every [Overlay] from this [Line].
func (l *Line) ClearOverlays() {
	l.overlays = nil
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

// Clone returns a copy of this [Line] with its own annotations and overlays.
//
// The copy shares the underlying tokens with the original, since the line
// never modifies them.
func (l *Line) Clone() Line {
	var ann Annotations

	if len(l.annotations) > 0 {
		ann = make(Annotations, len(l.annotations))
		copy(ann, l.annotations)
	}

	var ovl Overlays

	if len(l.overlays) > 0 {
		ovl = make(Overlays, len(l.overlays))
		copy(ovl, l.overlays)
	}

	return Line{
		annotations: ann,
		overlays:    ovl,
		flag:        l.flag,
		number:      l.number,
		segments:    l.segments.Clone(),
	}
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
// The iteration includes the line ending as a single '\n', so a newline
// occupies the column after the last visible rune whether the source used LF
// or CRLF, and columns match [Line.Width]. The lexer may leave a line's last
// token ending in a bare '\r', with the '\n' moved into the next token or
// dropped at the end of input, so a bare '\r' on the last token also yields
// a newline.
func (l *Line) Runes() iter.Seq2[int, rune] {
	return func(yield func(int, rune) bool) {
		col := 0

		for i, seg := range l.segments {
			origin := seg.Part().Origin

			for _, r := range tokens.TrimLineEnding(origin) {
				if !yield(col, r) {
					return
				}

				col++
			}

			last := i == len(l.segments)-1
			endsLine := strings.HasSuffix(origin, "\n") || (last && strings.HasSuffix(origin, "\r"))

			if endsLine && !yield(col, '\n') {
				return
			}
		}
	}
}

// String reconstructs this [Line] as a string, including any annotations.
// This should generally only be used for debugging.
func (l *Line) String() string {
	var sb strings.Builder

	prefix := fmt.Sprintf("%4d | ", l.Number())

	// Render annotations above if applicable.
	above := l.annotations.Filter(Above)
	if len(above) > 0 {
		sb.WriteString(prefix)
		sb.WriteString(above.String())
		sb.WriteByte('\n')
	}

	sb.WriteString(prefix)
	sb.WriteString(l.Content())

	// Render annotations below if applicable.
	// Add "^ " prefix for below annotations (error pointers) in debug output.
	below := l.annotations.Filter(Below)
	if len(below) > 0 {
		sb.WriteByte('\n')
		sb.WriteString(prefix)

		padding := strings.Repeat(" ", max(0, below.Col()))
		sb.WriteString(padding)
		sb.WriteString("^ ")
		sb.WriteString(strings.Join(below.Contents(), "; "))
	}

	return sb.String()
}
