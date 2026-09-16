package line

import (
	"errors"
	"fmt"
	"iter"
	"strings"

	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/segment"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/style"
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

// View is read-only, line-by-line access to content that rendering and
// search utilities consume, such as the printer and finder packages.
//
// AllLines yields each [Line] by value, so a change to a yielded line
// reaches nothing. Overlays and annotations go through the [Lines] methods
// and through indexing a [Lines] collection directly.
//
// [Lines] implements View directly, and a niceyaml Source implements it over
// its pristine lines.
type View interface {
	AllLines(spans ...position.Span) iter.Seq2[int, Line]
	AllRunes(ranges ...position.Range) iter.Seq2[position.Position, rune]
	Len() int
}

// Lines is an ordered collection of [Line] values and the unit that
// rendering utilities consume.
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
		lines[i] = Line{segments: l.Segments, number: l.Number}
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

// Clone returns a deep copy of the [Lines].
//
// Clone copies each [Line] with [Line.Clone], so overlays and
// annotations added to the copy do not affect the original.
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
// Without spans, AllLines yields every line. Each iteration yields the
// 0-indexed line index and the [Line] at that index by value, so a
// change to the yielded line reaches nothing. To add overlays or
// annotations, use [Lines.AddOverlay] and the other Lines methods, or index
// the collection directly. AllLines clamps spans to the available lines.
func (ls Lines) AllLines(spans ...position.Span) iter.Seq2[int, Line] {
	return func(yield func(int, Line) bool) {
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

// TokenRanges returns the ranges tk occupies, one per line it appears on.
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
		sp, ok := span(&ls[i], tk)
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

// String reconstructs all lines as a string, including any annotations.
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

	for i, l := range ls {
		// Check: line numbers strictly increasing.
		lineNum := l.Number()
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

		for j, tk := range l.Tokens() {
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
// The overlay replaces the style underneath it; use [Lines.BlendOverlay] to
// mix with it instead.
//
// It splits multi-line ranges into per-line overlays and clamps each
// overlay's columns to its line's width. It skips lines outside the
// collection, the same way [Lines.AllLines] clamps its spans, so a range
// computed against a longer view is safe to apply. A range that
// covers no columns of a line adds no overlay to it.
func (ls Lines) AddOverlay(s style.Style, ranges ...position.Range) {
	for _, r := range ranges {
		ls.addOverlayRange(s, false, r)
	}
}

// BlendOverlay adds an overlay like [Lines.AddOverlay], but one that blends
// with the style underneath it. A search highlight added this way keeps the
// token or diff color of the text it covers.
func (ls Lines) BlendOverlay(s style.Style, ranges ...position.Range) {
	for _, r := range ranges {
		ls.addOverlayRange(s, true, r)
	}
}

// addOverlayRange adds a single overlay range, splitting across lines as
// needed and skipping lines outside the collection.
func (ls Lines) addOverlayRange(s style.Style, blend bool, r position.Range) {
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

		ls[lineIdx].AddOverlay(Overlay{Cols: cols, Style: s, Blend: blend})
	}
}

// ClearOverlays removes all [Overlay] values from all lines.
func (ls Lines) ClearOverlays() {
	for i := range ls {
		ls[i].Overlays = nil
	}
}
