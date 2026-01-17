package line

import (
	"errors"
	"fmt"
	"strings"

	"github.com/goccy/go-yaml/token"

	"github.com/macropower/niceyaml/position"
	"github.com/macropower/niceyaml/tokens"
)

var (
	// ErrLineNumberNotIncreasing indicates a line number is not greater than the previous.
	ErrLineNumberNotIncreasing = errors.New("line number not greater than previous")
	// ErrLineNumberMismatch indicates a token's line number differs from expected.
	ErrLineNumberMismatch = errors.New("token line number differs from expected")
	// ErrColumnNotIncreasing indicates a column is not greater than the previous.
	ErrColumnNotIncreasing = errors.New("column not greater than previous")
)

// Line contains data for a specific line in a [Source] collection.
// Create instances with [NewLines]; access via [Lines] indexing.
type Line struct {
	// Token segments for this line.
	segments tokens.Segments
	// Annotation contains any extra content associated with this line.
	Annotation Annotation
	// Flag indicates the optional special category for this line.
	Flag Flag
	// The 1-indexed line number used for display purposes.
	// This may differ from the first token's Position.Line for block scalars.
	number int
}

// Number returns the line number of this [Line].
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

// Content returns the line content as a string.
// Line endings (LF or CRLF) are stripped from each segment for a clean single-line representation.
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

// Clone returns a copy of the Line with cloned Part tokens.
// Source pointers remain shared since they reference the original unmodified tokens.
func (l Line) Clone() Line {
	return Line{
		Annotation: l.Annotation,
		Flag:       l.Flag,
		number:     l.number,
		segments:   l.segments.Clone(),
	}
}

// Tokens returns the [token.Tokens] for this line (with line-adjusted positions).
func (l Line) Tokens() token.Tokens {
	return l.segments.PartTokens()
}

// Token returns the [*token.Token] at the given index. Panics if idx is out of range.
func (l Line) Token(idx int) *token.Token {
	return l.segments[idx].Part()
}

// tokenPositions returns the [position.Position]s where the given [*token.Token]
// appears on this line.
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

// tokenPositionRanges returns [*position.Ranges] for occurrences of the given
// [*token.Token] on this line.
func (l Line) tokenPositionRanges(lineIdx int, tk *token.Token) *position.Ranges {
	ranges := position.NewRanges()

	col := 0
	for _, seg := range l.segments {
		w := seg.Width()
		if seg.Contains(tk) && w > 0 {
			ranges.Add(position.NewRange(
				position.New(lineIdx, col),
				position.New(lineIdx, col+w),
			))
		}

		col += w
	}

	return ranges
}

// IsEmpty returns true if there are no tokens on this line.
func (l Line) IsEmpty() bool {
	return len(l.segments) == 0
}

// String reconstructs the line as a string, including any annotation.
// This should generally only be used for debugging.
func (l Line) String() string {
	var sb strings.Builder

	prefix := fmt.Sprintf("%4d | ", l.Number())

	// Render annotation above if applicable.
	if l.Annotation.Content != "" && l.Annotation.Position == Above {
		sb.WriteString(prefix)
		sb.WriteString(l.Annotation.String())
		sb.WriteByte('\n')
	}

	sb.WriteString(prefix)
	sb.WriteString(l.Content())

	// Render annotation below if applicable.
	if l.Annotation.Content != "" && l.Annotation.Position == Below {
		sb.WriteByte('\n')
		sb.WriteString(prefix)
		sb.WriteString(l.Annotation.String())
	}

	return sb.String()
}

// Lines represents an ordered collection of [Line]s with associated metadata.
// Create instances with [NewLines].
type Lines []Line

// NewLines creates new [Lines] from [token.Tokens].
//
// This function splits multiline tokens into per-line parts while closely
// matching go-yaml lexer behavior:
//
// Position field semantics (all 1-indexed):
//   - Line: Line number in the document
//   - Column: 1-indexed column position; typically where Value starts,
//     but for certain token types points to structural markers (see below)
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
//   - Multi-line with following content: Column = 0 (marker), Line = first content line
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

// Tokens reconstructs the full [token.Tokens] stream from all [Line]s.
//
// For multiline tokens that were split across lines, this function recombines
// them by returning clones of the original tokens (via [Segment.Source]).
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

// TokenPositions returns all positions where the given token appears across all lines.
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

// TokenPositionRanges returns [*position.Ranges] for all occurrences of the given token.
// For multi-line tokens split across lines, returns one range per line.
// Returns nil if the token is nil or not found.
func (ls Lines) TokenPositionRanges(tk *token.Token) *position.Ranges {
	if tk == nil {
		return nil
	}

	ranges := position.NewRanges()
	for i, l := range ls {
		ranges.Add(l.tokenPositionRanges(i, tk).Values()...)
	}

	return ranges
}

// TokenPositionRangesAt returns [*position.Ranges] for all occurrences of the token
// at the given position. For multi-line tokens, returns one range per line.
// Returns nil if the position is out of bounds or no token exists there.
func (ls Lines) TokenPositionRangesAt(pos position.Position) *position.Ranges {
	lineSegs := make(tokens.Segments2, len(ls))
	for i, l := range ls {
		lineSegs[i] = l.segments
	}

	return lineSegs.TokenRangesAt(pos.Line, pos.Col)
}

// ContentPositionRangesAt returns position ranges for content at the given
// position, excluding leading and trailing spaces.
// Returns nil if the position is out of bounds or no content exists there.
func (ls Lines) ContentPositionRangesAt(pos position.Position) *position.Ranges {
	lineSegs := make(tokens.Segments2, len(ls))
	for i, l := range ls {
		lineSegs[i] = l.segments
	}

	return lineSegs.ContentRangesAt(pos.Line, pos.Col)
}

// Content returns the combined content of all [Line]s as a string.
// [Line]s are joined with newlines.
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

// String reconstructs all [Line]s as a string, including any annotations.
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

// Validate checks the integrity of the [Lines]. It ensures that:
//   - Line numbers are strictly increasing
//   - Every token on a given [Line] has an identical line number in its Position
//   - Every token on a given [Line] has columns that are strictly increasing
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
			// Skip check for zero-width tokens (empty Origin) as they don't occupy column space.
			// The lexer can produce tokens at the same position (e.g., empty block scalar content).
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
