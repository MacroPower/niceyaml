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
	segments    tokens.Segments
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

// Number returns the 1-indexed line number of this [Line].
func (l Line) Number() int {
	if l.number != 0 {
		return l.number
	}

	if len(l.segments) == 0 {
		return 0
	}

	part := l.segments[0].Part()
	if part == nil || part.Position == nil {
		return 0
	}

	return part.Position.Line
}

// Content returns this [Line]'s content as a string.
//
// Line endings (LF or CRLF) are stripped from each segment for a clean
// single-line representation.
func (l Line) Content() string {
	var sb strings.Builder

	for _, seg := range l.segments {
		part := seg.Part()
		origin := strings.TrimSuffix(part.Origin, "\n")
		origin = strings.TrimSuffix(origin, "\r")
		sb.WriteString(origin)
	}

	return sb.String()
}

// Clone returns a copy of this [Line] with cloned Part tokens.
//
// Source pointers remain shared since they reference the original
// unmodified tokens.
func (l Line) Clone() Line {
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
// positions.
func (l Line) Tokens() token.Tokens {
	return l.segments.PartTokens()
}

// Token returns the [*token.Token] at the given index.
// Panics if idx is out of range.
func (l Line) Token(idx int) *token.Token {
	return l.segments[idx].Part()
}

// tokenPositions returns the [position.Position]s where the given
// [*token.Token] appears on this line.
func (l Line) tokenPositions(lineIdx int, tk *token.Token) []position.Position {
	var positions []position.Position

	col := 0
	for _, seg := range l.segments {
		if seg.Contains(tk) {
			positions = append(positions, position.New(lineIdx, col))
		}

		col += seg.Width()
	}

	return positions
}

// tokenPositionRanges returns [position.Ranges] for occurrences of the given
// [*token.Token] on this line.
func (l Line) tokenPositionRanges(lineIdx int, tk *token.Token) position.Ranges {
	var ranges position.Ranges

	col := 0
	for _, seg := range l.segments {
		w := seg.Width()
		if seg.Contains(tk) && w > 0 {
			ranges = append(ranges, position.NewRange(
				position.New(lineIdx, col),
				position.New(lineIdx, col+w),
			))
		}

		col += w
	}

	return ranges
}

// IsEmpty returns true if there are no tokens on this [Line].
func (l Line) IsEmpty() bool {
	return len(l.segments) == 0
}

// Width returns the total rune width of this line's content.
func (l Line) Width() int {
	var w int

	for _, seg := range l.segments {
		w += seg.Width()
	}

	return w
}

// String reconstructs this [Line] as a string, including any annotations.
// This should generally only be used for debugging.
func (l Line) String() string {
	var sb strings.Builder

	prefix := fmt.Sprintf("%4d | ", l.Number())

	// Render annotations above if applicable.
	above := l.Annotations.FilterPosition(Above)
	if len(above) > 0 {
		sb.WriteString(prefix)
		sb.WriteString(above.String())
		sb.WriteByte('\n')
	}

	sb.WriteString(prefix)
	sb.WriteString(l.Content())

	// Render annotations below if applicable.
	// Add "^ " prefix for below annotations (error pointers) in debug output.
	below := l.Annotations.FilterPosition(Below)
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
// line endings, so a newline occupies the column after the last visible rune.
func (ls Lines) AllRunes(ranges ...position.Range) iter.Seq2[position.Position, rune] {
	return func(yield func(position.Position, rune) bool) {
		if len(ranges) == 0 {
			for i, ln := range ls {
				col := 0

				for _, tk := range ln.Tokens() {
					for _, r := range tk.Origin {
						if !yield(position.New(i, col), r) {
							return
						}

						col++
					}
				}
			}

			return
		}

		for _, rng := range ranges {
			startLine := max(0, rng.Start.Line)
			endLine := min(len(ls)-1, rng.End.Line)

			for i := startLine; i <= endLine; i++ {
				col := 0

				for _, tk := range ls[i].Tokens() {
					for _, r := range tk.Origin {
						pos := position.New(i, col)
						if rng.Contains(pos) {
							if !yield(pos, r) {
								return
							}
						}

						col++
					}
				}
			}
		}
	}
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
// For multiline tokens that were split across lines, this function recombines
// them by returning clones of the original tokens (via [tokens.Segment.Source]).
// Segments that share a Source pointer are collapsed to a single token.
func (ls Lines) Tokens() token.Tokens {
	if len(ls) == 0 {
		return nil
	}

	var combined tokens.Segments

	for _, line := range ls {
		combined = combined.Merge(line.segments)
	}

	return combined.SourceTokens()
}

// TokenPositions returns all positions where the given token appears across all
// lines.
//
// A token may appear on multiple lines when split across lines.
// Returns nil if the token is nil or not found.
func (ls Lines) TokenPositions(tk *token.Token) []position.Position {
	if tk == nil {
		return nil
	}

	var positions []position.Position

	for i, l := range ls {
		positions = append(positions, l.tokenPositions(i, tk)...)
	}

	return positions
}

// TokenAt returns the [*token.Token] at the given position.
// Returns nil if the position is out of bounds or no token exists there.
func (ls Lines) TokenAt(pos position.Position) *token.Token {
	if pos.Line < 0 || pos.Line >= len(ls) {
		return nil
	}

	return ls[pos.Line].segments.SourceTokenAt(pos.Col)
}

// TokenPositionRangesAt returns [position.Ranges] for all occurrences of the
// token at the given position.
//
// For multi-line tokens, returns one range per line.
//
// Returns nil if the position is out of bounds or no token exists there.
func (ls Lines) TokenPositionRangesAt(pos position.Position) position.Ranges {
	lineSegs := make(tokens.Segments2, len(ls))
	for i, l := range ls {
		lineSegs[i] = l.segments
	}

	return lineSegs.TokenRangesAt(pos.Line, pos.Col)
}

// TokenPositionRanges returns position ranges for the tokens at each of the
// given positions. For a token split across lines, it returns one range per
// line of the token. It removes duplicate ranges.
//
// Returns nil if no tokens exist at any of the given positions.
func (ls Lines) TokenPositionRanges(positions ...position.Position) []position.Range {
	var allRanges position.Ranges

	for _, pos := range positions {
		allRanges = append(allRanges, ls.TokenPositionRangesAt(pos)...)
	}

	return allRanges.UniqueValues()
}

// TokenPositionRangesFromToken returns position ranges for all occurrences of
// the given token.
//
// For multi-line tokens split across lines, returns one range per line.
//
// Returns nil if the token is nil or not found.
func (ls Lines) TokenPositionRangesFromToken(tk *token.Token) []position.Range {
	if tk == nil {
		return nil
	}

	var ranges position.Ranges

	for i, l := range ls {
		ranges = append(ranges, l.tokenPositionRanges(i, tk)...)
	}

	return ranges
}

// ContentPositionRangesAt returns position ranges for content at the given
// position, excluding leading and trailing spaces.
//
// Returns nil if the position is out of bounds or no content exists there.
func (ls Lines) ContentPositionRangesAt(pos position.Position) position.Ranges {
	lineSegs := make(tokens.Segments2, len(ls))
	for i, l := range ls {
		lineSegs[i] = l.segments
	}

	return lineSegs.ContentRangesAt(pos.Line, pos.Col)
}

// ContentPositionRanges returns position ranges for content at each of the
// given positions, excluding leading and trailing spaces. It removes duplicate
// ranges.
//
// Returns nil if no content exists at any of the given positions.
func (ls Lines) ContentPositionRanges(positions ...position.Position) []position.Range {
	var allRanges position.Ranges

	for _, pos := range positions {
		allRanges = append(allRanges, ls.ContentPositionRangesAt(pos)...)
	}

	return allRanges.UniqueValues()
}

// ContentPositionRangesFromToken returns position ranges for content of the
// given token, excluding leading and trailing spaces.
//
// Returns nil if the token is nil or not found.
func (ls Lines) ContentPositionRangesFromToken(tk *token.Token) []position.Range {
	return ls.ContentPositionRanges(ls.TokenPositions(tk)...)
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
// Multi-line ranges are split into per-line overlays automatically.
//
// Panics if a range refers to a line index outside the collection.
func (ls Lines) AddOverlay(kind style.Style, ranges ...position.Range) {
	if len(ls) == 0 {
		return
	}

	for _, r := range ranges {
		ls.addOverlayRange(kind, r)
	}
}

// addOverlayRange adds a single overlay range, splitting across lines as needed.
func (ls Lines) addOverlayRange(kind style.Style, r position.Range) {
	for _, lineRange := range r.SliceLines() {
		lineIdx := lineRange.Start.Line
		ls[lineIdx].AddOverlay(Overlay{
			Cols: position.NewSpan(lineRange.Start.Col, lineRange.End.Col),
			Kind: kind,
		})
	}
}

// ClearOverlays removes all [Overlay] values from all lines.
func (ls Lines) ClearOverlays() {
	for i := range ls {
		ls[i].Overlays = nil
	}
}
