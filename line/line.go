package line

import (
	"errors"
	"fmt"
	"iter"
	"strings"

	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/tokens"
)

var (
	// ErrLineNumberNotIncreasing indicates a line number is not greater than
	// the previous.
	ErrLineNumberNotIncreasing = errors.New("line number not greater than previous")
	// ErrLineNumberMismatch indicates a token's line number differs from expected.
	ErrLineNumberMismatch = errors.New("token line number differs from expected")
	// ErrColumnNotIncreasing indicates a column is not greater than the previous.
	ErrColumnNotIncreasing = errors.New("column not greater than previous")
)

// Line contains data for a specific line in a [Lines] collection.
// Create instances with [NewLines]; access individual lines via [Lines] indexing.
type Line struct {
	Annotations Annotations
	Overlays    Overlays
	segments    segments
	Flag        Flag

	// The 1-indexed line number used for display purposes.
	// This may differ from the first token's Position.Line for block scalars.
	number int
}

// AddAnnotation adds the given [Annotation] values to this [Line].
func (l *Line) AddAnnotation(ann ...Annotation) {
	l.Annotations = append(l.Annotations, ann...)
}

// AddOverlay adds the given [Overlay] values to this [Line].
func (l *Line) AddOverlay(o ...Overlay) {
	l.Overlays = append(l.Overlays, o...)
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

	if len(l.Annotations) > 0 {
		ann = make(Annotations, len(l.Annotations))
		copy(ann, l.Annotations)
	}

	var ovl Overlays

	if len(l.Overlays) > 0 {
		ovl = make(Overlays, len(l.Overlays))
		copy(ovl, l.Overlays)
	}

	return Line{
		Annotations: ann,
		Overlays:    ovl,
		Flag:        l.Flag,
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

// String reconstructs this [Line] as a string, including any annotations.
// This should generally only be used for debugging.
func (l *Line) String() string {
	var sb strings.Builder

	prefix := fmt.Sprintf("%4d | ", l.Number())

	// Render annotations above if applicable.
	above := l.Annotations.Filter(Above)
	if len(above) > 0 {
		sb.WriteString(prefix)
		sb.WriteString(above.String())
		sb.WriteByte('\n')
	}

	sb.WriteString(prefix)
	sb.WriteString(l.Content())

	// Render annotations below if applicable.
	// Add "^ " prefix for below annotations (error pointers) in debug output.
	below := l.Annotations.Filter(Below)
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

// Lines is an ordered collection of [Line] values and the unit that rendering
// utilities consume.
//
// Lines carries only what rendering needs, which is the tokens split per
// line plus the overlays, annotations, and flags attached to each line. It has
// no knowledge of YAML documents, parsing, or files. A [Lines] value may
// therefore describe content that is not a YAML document at all, such as a
// diff that interleaves lines from two revisions.
//
// Lines is not safe for concurrent mutation. Add overlays and annotations from
// one goroutine at a time, and do not mutate while another goroutine iterates.
//
// Create instances with [NewLines].
// Access individual lines via slice indexing.
type Lines []Line

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

// Clone returns a deep copy of the [Lines].
//
// Clone copies each [Line] with [Line.Clone], so overlays and annotations
// added to the copy do not affect the original.
func (ls Lines) Clone() Lines {
	if len(ls) == 0 {
		return nil
	}

	result := make(Lines, len(ls))
	for i, l := range ls {
		result[i] = l.Clone()
	}

	return result
}

// AllLines returns an iterator over lines within the given spans.
//
// Without spans, AllLines yields every line. Each iteration yields a
// [position.Position] at column 0 and the [Line] at that position. AllLines
// clamps spans to the available lines.
func (ls Lines) AllLines(spans ...position.Span) iter.Seq2[position.Position, Line] {
	return func(yield func(position.Position, Line) bool) {
		if len(spans) == 0 {
			for i, ln := range ls {
				if !yield(position.New(i, 0), ln) {
					return
				}
			}

			return
		}

		for _, span := range spans {
			start := max(0, span.Start)
			end := min(len(ls), span.End)

			for i := start; i < end; i++ {
				if !yield(position.New(i, 0), ls[i]) {
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
// line endings as a single '\n', so a newline occupies the column after the
// last visible rune whether the source used LF or CRLF, and columns match
// [Line.Width].
func (ls Lines) AllRunes(ranges ...position.Range) iter.Seq2[position.Position, rune] {
	return func(yield func(position.Position, rune) bool) {
		if len(ranges) == 0 {
			for i, ln := range ls {
				if !ln.yieldRunes(i, nil, yield) {
					return
				}
			}

			return
		}

		for _, rng := range ranges {
			startLine := max(0, rng.Start.Line)
			endLine := min(len(ls)-1, rng.End.Line)

			for i := startLine; i <= endLine; i++ {
				if !ls[i].yieldRunes(i, &rng, yield) {
					return
				}
			}
		}
	}
}

// yieldRunes yields every rune of the line at index lineIdx, with the line
// ending collapsed to a single '\n'. When rng is non-nil, it yields only the
// runes inside it. Returns false when yield stops the iteration.
//
// A segment ending in "\n" always yields the newline. The lexer may leave a
// line's last segment ending in a bare "\r", with the "\n" moved into the
// next token or dropped at the end of input, so the last segment also yields
// a newline for a bare "\r".
func (l *Line) yieldRunes(lineIdx int, rng *position.Range, yield func(position.Position, rune) bool) bool {
	col := 0

	emit := func(r rune) bool {
		pos := position.New(lineIdx, col)
		col++

		if rng != nil && !rng.Contains(pos) {
			return true
		}

		return yield(pos, r)
	}

	for i, seg := range l.segments {
		origin := seg.Part().Origin

		for _, r := range tokens.TrimLineEnding(origin) {
			if !emit(r) {
				return false
			}
		}

		last := i == len(l.segments)-1
		endsLine := strings.HasSuffix(origin, "\n") || (last && strings.HasSuffix(origin, "\r"))

		if endsLine && !emit('\n') {
			return false
		}
	}

	return true
}

// NewLines creates new [Lines] from [token.Tokens].
//
// This function splits multiline tokens into per-line parts while closely
// matching go-yaml lexer behavior:
//
// Position field semantics (all 1-indexed):
//   - Line: Line number in the document
//   - Column: 1-indexed column position; typically where Value starts, but for
//     certain token types points to structural markers (see below)
//   - Offset: Rune offset from document start (NOT byte offset)
//   - IndentNum: Leading spaces on the current line (space chars only)
//   - IndentLevel: Nesting depth based on indentation changes
//
// Column exceptions by token type:
//   - SingleQuoteType/DoubleQuoteType: Column points to opening quote character
//   - CommentType: Column points to '#' character
//   - LiteralType/FoldedType: Column points to '|' or '>' indicator
//
// Position.Line assignment for multiline tokens:
//   - Plain multiline strings: Points to FIRST line
//   - Quoted multiline strings: Points to opening quote line
//   - Block scalar content (StringType after Literal/Folded): See below
//
// Block scalar Position has three distinct behaviors:
//   - Single-line content (any context): Column > 0, Line = content line
//   - Multi-line with following content: Column = 0 (marker),
//     Line = first content line
//   - Multi-line standalone/at end: Column > 0, Line = last content line
//
// Additional lexer behaviors:
//   - CRLF (\r\n) is preserved in Origin but normalized to \n in Value
//   - Blank lines are absorbed into the previous token's Origin
//   - Comments include the trailing newline in Origin but not in Value
func NewLines(tks token.Tokens) Lines {
	b := newLinesBuilder(tks)
	if b == nil {
		return nil
	}

	for _, tk := range tks {
		b.AddToken(tk)
	}

	return b.Build()
}

// Tokens reconstructs the full [token.Tokens] stream from all [Line] values.
//
// For multiline tokens that were split across lines, Tokens recombines them by
// returning the original token once. The slice is new, but the tokens are the
// originals. Treat them as read-only.
func (ls Lines) Tokens() token.Tokens {
	if len(ls) == 0 {
		return nil
	}

	result := token.Tokens{}

	var lastSource *token.Token

	for _, line := range ls {
		for _, seg := range line.segments {
			if src := seg.Source(); src != lastSource {
				result = append(result, src)
				lastSource = src
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

	return ls[pos.Line].segments.SourceTokenAt(pos.Col)
}

// TokenRanges returns the ranges tk occupies, one per line it appears on.
//
// The token may be a lexer token, as returned by [Lines.TokenAt] or
// [Lines.Tokens], or one of the per-line parts from [Line.Tokens]. Returns
// nil if tk is nil or not found.
func (ls Lines) TokenRanges(tk *token.Token) position.Ranges {
	return ls.ranges(tk, func(seg segment) position.Span {
		return position.NewSpan(0, seg.Width())
	})
}

// ContentRanges returns the ranges of tk's content, one per line it appears
// on, excluding leading and trailing spaces. A line where tk holds only
// spaces contributes no range.
//
// The token may be a lexer token or one of the per-line parts, as for
// [Lines.TokenRanges]. Returns nil if tk is nil or not found.
func (ls Lines) ContentRanges(tk *token.Token) position.Ranges {
	return ls.ranges(tk, segment.contentSpan)
}

// ranges collects one range per segment that contains tk, using span to
// pick the columns within the segment.
func (ls Lines) ranges(tk *token.Token, span func(segment) position.Span) position.Ranges {
	if tk == nil {
		return nil
	}

	var result position.Ranges

	for i, l := range ls {
		col := 0

		for _, seg := range l.segments {
			if seg.Contains(tk) {
				if sp := span(seg); sp.Len() > 0 {
					result = append(result, position.NewRange(
						position.New(i, col+sp.Start),
						position.New(i, col+sp.End),
					))
				}
			}

			col += seg.Width()
		}
	}

	return result
}

// Content returns the combined content of all [Line] values as a string.
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

// String reconstructs all [Line] values as a string, including any annotations.
// This should generally only be used for debugging.
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

// Validate checks the integrity of the [Lines].
//
// It ensures that:
//   - Line numbers are strictly increasing
//   - Every token on a given line has an identical line number in its Position
//   - Every token on a given line has columns that are strictly increasing
//
// Returns an error if any validation check fails.
func (ls Lines) Validate() error {
	prevLineNum := 0

	for i, line := range ls {
		// Check: line numbers strictly increasing.
		lineNum := line.Number()
		if lineNum != 0 && lineNum <= prevLineNum {
			return fmt.Errorf(
				"line at index %d: line number %d not greater than previous %d: %w",
				i,
				lineNum,
				prevLineNum,
				ErrLineNumberNotIncreasing,
			)
		}

		if lineNum != 0 {
			prevLineNum = lineNum
		}

		// Check: all tokens have identical line number and columns are strictly increasing.
		var (
			expectedLineNum = -1
			prevCol         = 0
		)

		for j, seg := range line.segments {
			tk := seg.Part()
			if tk == nil || tk.Position == nil {
				continue
			}

			// Check token line number consistency.
			if expectedLineNum == -1 {
				expectedLineNum = tk.Position.Line
			} else if tk.Position.Line != expectedLineNum {
				return fmt.Errorf(
					"line at index %d, token %d: line number %d differs from expected %d: %w",
					i,
					j,
					tk.Position.Line,
					expectedLineNum,
					ErrLineNumberMismatch,
				)
			}

			// Check columns strictly increasing.
			//
			// Skip check for zero-width tokens (empty Origin) as they don't occupy
			// column space.
			//
			// The lexer can produce tokens at the same position (e.g., empty block
			// scalar content).
			if tk.Origin != "" {
				if tk.Position.Column <= prevCol {
					return fmt.Errorf(
						"line at index %d, token %d: column %d not greater than previous %d: %w",
						i,
						j,
						tk.Position.Column,
						prevCol,
						ErrColumnNotIncreasing,
					)
				}

				prevCol = tk.Position.Column
			}
		}
	}

	return nil
}

// AddOverlay adds an overlay with the given style to the specified ranges.
// Multi-line ranges are split into per-line overlays automatically, and each
// overlay's columns are clamped to its line's width.
//
// Lines outside the collection are skipped, the same way [Lines.AllLines]
// clamps its spans, so a range computed against a longer view is safe to
// apply. A range that covers no columns of a line adds no overlay to it.
func (ls Lines) AddOverlay(s style.Style, ranges ...position.Range) {
	if len(ls) == 0 {
		return
	}

	for _, r := range ranges {
		ls.addOverlayRange(s, r)
	}
}

// addOverlayRange adds a single overlay range, splitting across lines as
// needed and skipping lines outside the collection.
func (ls Lines) addOverlayRange(s style.Style, r position.Range) {
	for _, lineRange := range r.SliceLines() {
		lineIdx := lineRange.Start.Line
		if lineIdx < 0 || lineIdx >= len(ls) {
			continue
		}

		cols := position.NewSpan(
			max(0, lineRange.Start.Col),
			min(lineRange.End.Col, ls[lineIdx].Width()),
		)
		if cols.Len() <= 0 {
			continue
		}

		ls[lineIdx].AddOverlay(Overlay{Cols: cols, Style: s})
	}
}

// ClearOverlays removes all [Overlay] values from all lines.
func (ls Lines) ClearOverlays() {
	for i := range ls {
		ls[i].Overlays = nil
	}
}
