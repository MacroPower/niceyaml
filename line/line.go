package line

import (
	"fmt"
	"strings"

	"github.com/goccy/go-yaml/token"

	"github.com/macropower/niceyaml/position"
)

// Annotation represents extra content to be added around a line.
// It can be used to add comments or notes to the rendered output, without being
// part of the main token stream.
type Annotation struct {
	Content string
	Column  int // Optional, 1-indexed column position for the annotation.
}

// String returns the annotation as a string, properly padded to the specified column.
func (a Annotation) String() string {
	if a.Content == "" {
		return ""
	}

	padding := strings.Repeat(" ", max(0, a.Column-1))

	return padding + "^ " + a.Content
}

// Flag identifies a category for YAML lines.
type Flag int

// Flag constants for YAML line categories.
const (
	FlagDefault    Flag = iota // Default/fallback.
	FlagInserted               // Lines inserted in diff (+).
	FlagDeleted                // Lines deleted in diff (-).
	FlagAnnotation             // Annotation/header lines (no line number).
)

// Line contains data for a specific line in a [Source] collection.
type Line struct {
	// Segments contains the tokens for this line, with references to original tokens.
	// For multiline tokens split across lines, segments share the same Source pointer.
	segments SegmentedTokens
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

	if len(l.segments) == 0 || l.segments[0].Part == nil || l.segments[0].Part.Position == nil {
		return 0
	}

	return l.segments[0].Part.Position.Line
}

// Content returns the line content as a string.
// Line endings (LF or CRLF) are stripped from each segment for a clean single-line representation.
func (l Line) Content() string {
	var sb strings.Builder
	for _, seg := range l.segments {
		origin := strings.TrimSuffix(seg.Part.Origin, "\n")
		origin = strings.TrimSuffix(origin, "\r")
		sb.WriteString(origin)
	}

	return sb.String()
}

// Clone returns a copy of the Line with cloned Part tokens.
// Source pointers remain shared since they reference the original unmodified tokens.
func (l Line) Clone() Line {
	clone := Line{
		Annotation: l.Annotation,
		Flag:       l.Flag,
		number:     l.number,
	}
	for _, seg := range l.segments {
		// Clone Part tokens. Source pointers are kept as-is since they
		// reference the original unmodified tokens.
		clone.segments = append(clone.segments, SegmentedToken{
			Source: seg.Source,
			Part:   seg.Part.Clone(),
		})
	}

	return clone
}

// Tokens returns the tokens for this line (Part tokens for display).
func (l Line) Tokens() token.Tokens {
	return l.segments.PartTokens()
}

// Token returns the token at the given index. Panics if idx is out of range.
func (l Line) Token(idx int) *token.Token {
	return l.segments[idx].Part
}

// tokenPositions returns the positions where the given token appears on this line.
// Matches against both the Part pointer and Source pointer to handle tokens obtained
// via [Line.Token] or from multiline token sources.
func (l Line) tokenPositions(lineIdx int, tk *token.Token) []position.Position {
	var positions []position.Position

	col := 0
	for _, seg := range l.segments {
		if seg.Part == tk || seg.Source == tk {
			positions = append(positions, position.New(lineIdx, col))
		}

		col += len([]rune(strings.TrimSuffix(seg.Part.Origin, "\n")))
	}

	return positions
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

	sb.WriteString(prefix)
	sb.WriteString(l.Content())

	if l.Annotation.Content != "" {
		sb.WriteByte('\n')
		sb.WriteString(prefix)
		sb.WriteString(l.Annotation.String())
	}

	return sb.String()
}

// Lines represents a collection of [token.Tokens] organized into [Line]s with
// associated metadata. Create instances using [NewLines].
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
//
//nolint:nestif // Complex token splitting logic requires nested conditions.
func NewLines(tks token.Tokens) Lines {
	if len(tks) == 0 {
		return nil
	}

	var (
		lines                     []Line
		currentLineSegments       SegmentedTokens
		currentLine               int  // Current line number being built.
		prevTokenEndedWithNewline bool // Track if previous token's Origin ended with "\n".

		// Position tracking.
		currentOffset      int // Cumulative rune offset (1-indexed like lexer).
		currentIndentNum   int // Leading spaces on current line.
		prevLineIndentNum  int // IndentNum from previous line.
		currentIndentLevel int // Nesting depth level.
	)

	// Initialize currentLine from the first token's position.
	// If the first token's Origin has leading newlines, we need to start earlier
	// because Position.Line points to the content, not the Origin start.
	if tks[0].Position != nil {
		currentLine = tks[0].Position.Line
		// Count leading newlines in first token's Origin and adjust.
		leadingNewlines := countLeadingNewlines(tks[0].Origin)
		if leadingNewlines > 0 && currentLine > leadingNewlines {
			currentLine -= leadingNewlines
		}
	} else {
		currentLine = 1
	}

	// Initialize position tracking from first token (1-indexed like lexer).
	if tks[0].Position != nil && tks[0].Position.Offset > 0 {
		currentOffset = tks[0].Position.Offset
		currentIndentNum = tks[0].Position.IndentNum
		currentIndentLevel = tks[0].Position.IndentLevel
	} else {
		currentOffset = 1
	}

	for _, tk := range tks {
		// Detect if this token is block scalar content by checking if it follows
		// a Literal/Folded header in the token chain.
		isBlockScalarContent := isBlockScalarContent(tk)

		origin := tk.Origin
		newlineCount := strings.Count(origin, "\n")

		tkLine := currentLine
		if tk.Position != nil {
			tkLine = tk.Position.Line
		}

		// For simple tokens (no internal newlines), check for gaps.
		// Multiline tokens are handled by the splitting logic which uses currentLine.
		isSimple := newlineCount == 0 || (newlineCount == 1 && strings.HasSuffix(origin, "\n"))
		if isSimple {
			// If there's a gap (simple token is ahead), flush and sync forward.
			// Never sync backwards - currentLine must be monotonically increasing.
			if tkLine > currentLine+1 && len(currentLineSegments) > 0 {
				lines = append(lines, Line{segments: currentLineSegments, number: currentLine})
				currentLineSegments = nil
			}

			if len(currentLineSegments) == 0 && tkLine > currentLine {
				currentLine = tkLine

				if tk.Position != nil {
					if tk.Position.Offset > 0 {
						currentOffset = tk.Position.Offset
					}

					currentIndentNum = tk.Position.IndentNum
					currentIndentLevel = tk.Position.IndentLevel
				}
			}
		}

		// Split token at newline boundaries, filtering empty parts upfront.
		parts := splitOriginIntoParts(origin)

		// Multi-part means the token's Origin was split into multiple parts.
		isMultiPart := len(parts) > 1

		// Find the last non-pure-newline part index for Value assignment.
		// Pure newlines (like trailing "\n" in keep blocks) shouldn't get Value.
		lastContentPartIdx := findLastContentPartIndex(parts)

		isFirstContentPart := true

		for i, part := range parts {
			isLastPart := i == len(parts)-1
			partIsPureNewline := isPureNewline(part)

			// Handle duplicate leading newline: the go-yaml lexer sometimes includes the same
			// newline character at both the end of one token and the start of the next.
			// Detect this by checking if we're already at the token's line - if so, processing
			// the leading "\n" would incorrectly advance us past the token's position.
			// Instead of skipping it entirely (which would make Origin non-invertible), we append
			// it to the previous line so the newline is preserved in the Origin but doesn't
			// cause an extra line advance.
			isDuplicateNewline := i == 0 && partIsPureNewline && prevTokenEndedWithNewline &&
				tk.Position != nil && currentLine == tk.Position.Line
			if isDuplicateNewline && len(lines) > 0 {
				// Create a segment for the duplicate newline and attach to previous line.
				lastLine := &lines[len(lines)-1]
				newTk := &token.Token{
					Type:          tk.Type,
					CharacterType: tk.CharacterType,
					Indicator:     tk.Indicator,
					Origin:        part,
					Position: &token.Position{
						Line:        currentLine - 1, // Goes on previous line.
						Column:      segmentsNextColumn(lastLine.segments),
						Offset:      currentOffset,
						IndentNum:   prevLineIndentNum,
						IndentLevel: currentIndentLevel,
					},
				}
				lastLine.segments = append(lastLine.segments, SegmentedToken{
					Source: tk,
					Part:   newTk,
				})

				currentOffset += len(part)

				continue
			}

			// Update indentation tracking for first content on new line.
			// Use the token's Position if available (more accurate than counting spaces
			// in Origin, since some tokens like MappingKey don't include leading spaces).
			// Exception: multi-part block scalar content has special Position handling,
			// so calculate indentation for each part to maintain proper tracking.
			if len(currentLineSegments) == 0 && !partIsPureNewline {
				if i == 0 && tk.Position != nil && (!isBlockScalarContent || !isMultiPart) {
					// First part of non-block-scalar, or single-line block scalar:
					// use the token's Position.
					currentIndentNum = tk.Position.IndentNum
					currentIndentLevel = tk.Position.IndentLevel
				} else {
					// Subsequent parts, or multi-part block scalars: calculate from Origin.
					currentIndentNum = countLeadingWhitespace(part)
					currentIndentLevel = updateIndentLevel(prevLineIndentNum, currentIndentNum, currentIndentLevel)
				}
			}

			var (
				col int
				val string
			)

			isLastContentPart := i == lastContentPartIdx
			// Determine which part should receive the token's Value:
			// Block scalar: Value goes to last content part (lexer behavior).
			// Plain/quoted multiline: Value goes to first content part (lexer behavior).
			shouldHaveValue := shouldPartReceiveValue(isBlockScalarContent, isFirstContentPart, isLastContentPart)
			// Capture before it changes for later use.
			wasFirstContentPart := isFirstContentPart && !partIsPureNewline
			if partIsPureNewline {
				col = segmentsNextColumn(currentLineSegments)
			} else {
				col, val = partColumnAndValue(tk, isFirstContentPart, shouldHaveValue)
				isFirstContentPart = false
			}

			// Calculate offset where Value starts within the document.
			// Use original Offset when:
			//   - Single-part token (not split), or
			//   - Last part of block scalar (for recombination Position), or
			//   - Any part that receives the token's Value.
			useOriginalOffset := !isMultiPart ||
				(isLastPart && isBlockScalarContent) ||
				(shouldHaveValue && val != "")
			valueOffset := currentOffset
			if useOriginalOffset && tk.Position != nil && tk.Position.Offset > 0 {
				valueOffset = tk.Position.Offset
			}

			// Create token for this part.
			newTk := &token.Token{
				Type:          tk.Type,
				CharacterType: tk.CharacterType,
				Indicator:     tk.Indicator,
				Origin:        part,
				Value:         val,
				Error:         tk.Error,
				Position: &token.Position{
					Line:        currentLine,
					Column:      col,
					Offset:      valueOffset,
					IndentNum:   currentIndentNum,
					IndentLevel: currentIndentLevel,
				},
			}

			// For block scalars, preserve the original token's Position.
			// The go-yaml lexer behavior varies:
			//   - With following content: Position points to first content line (Column=0)
			//   - Standalone: Position points to last content line (Column>0)
			// We determine which case and put the original Position on the appropriate part.
			if isBlockScalarContent && isMultiPart && tk.Position != nil {
				isFirstLinePosition := tk.Position.Column == 0
				if (wasFirstContentPart && isFirstLinePosition) || (isLastContentPart && !isFirstLinePosition) {
					newTk.Position = clonePosition(tk.Position)
				}
			}

			// For tokens with a leading blank line (Origin starts with "\n"), preserve
			// the original Position for the first content part. The lexer's Position
			// reflects the content line, not the blank line, so we should use it
			// to ensure round-trip fidelity.
			// Also update our tracking to match, so subsequent tokens get correct values.
			hasLeadingBlankLine := isMultiPart && len(parts) > 1 && isPureNewline(parts[0])
			if hasLeadingBlankLine && wasFirstContentPart && tk.Position != nil {
				newTk.Position = clonePosition(tk.Position)
				// Sync our tracking with the original Position to fix subsequent tokens.
				currentIndentLevel = tk.Position.IndentLevel
			}

			currentLineSegments = append(currentLineSegments, SegmentedToken{
				Source: tk,
				Part:   newTk,
			})

			currentOffset += len(part)

			// If this part ends with a newline, finish the current line.
			// Only \n terminates lines (per YAML spec). CRLF (\r\n) may be split by
			// go-yaml across tokens, but we wait for the \n to create a new line.
			// The \r is preserved in Content() and stripped during output.
			if strings.HasSuffix(part, "\n") {
				lines = append(lines, Line{
					segments: currentLineSegments,
					number:   currentLine,
				})

				// Prepare indentation tracking for next line.
				prevLineIndentNum = currentIndentNum

				currentLineSegments = nil
				currentIndentNum = 0 // Will be recalculated for next line's first content.
				currentLine++
			}
		}

		// Track whether this token ended with a newline for duplicate detection.
		// Only \n counts as ending with newline (not \r alone).
		prevTokenEndedWithNewline = strings.HasSuffix(origin, "\n")
	}

	// Handle last line (may not end with newline).
	if len(currentLineSegments) > 0 {
		lines = append(lines, Line{
			segments: currentLineSegments,
			number:   currentLine,
		})
	}

	return lines
}

// Tokens reconstructs the full [token.Tokens] stream from all [Line]s.
//
// For multiline tokens that were split across lines, this function recombines
// them by returning copies of the original tokens (via [*SegmentedToken.Source]).
// Segments that share a Source pointer are collapsed to a single token.
func (ls Lines) Tokens() token.Tokens {
	if len(ls) == 0 {
		return nil
	}

	result := token.Tokens{}

	var lastSource *token.Token

	for _, line := range ls {
		for _, seg := range line.segments {
			// Only add unique Source tokens.
			if seg.Source != lastSource {
				result.Add(seg.Source.Clone())

				lastSource = seg.Source
			}
		}
	}

	return result
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
			return fmt.Errorf("line at index %d: line number %d not greater than previous %d", i, lineNum, prevLineNum)
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
			tk := seg.Part
			if tk == nil || tk.Position == nil {
				continue
			}

			// Check token line number consistency.
			if expectedLineNum == -1 {
				expectedLineNum = tk.Position.Line
			} else if tk.Position.Line != expectedLineNum {
				return fmt.Errorf("line at index %d, token %d: line number %d differs from expected %d", i, j, tk.Position.Line, expectedLineNum)
			}

			// Check columns strictly increasing.
			// Skip check for zero-width tokens (empty Origin) as they don't occupy column space.
			// The lexer can produce tokens at the same position (e.g., empty block scalar content).
			if tk.Origin != "" {
				if tk.Position.Column <= prevCol {
					return fmt.Errorf(
						"line at index %d, token %d: column %d not greater than previous %d",
						i,
						j,
						tk.Position.Column,
						prevCol,
					)
				}

				prevCol = tk.Position.Column
			}
		}
	}

	return nil
}

// segmentsNextColumn returns the next available column position after existing segments.
// Used for positioning pure-newline parts and duplicate newlines that need a column.
// Returns 1 if segments is empty or no segment has a valid column.
func segmentsNextColumn(segs SegmentedTokens) int {
	col := 1
	for _, seg := range segs {
		if seg.Part != nil && seg.Part.Position != nil && seg.Part.Position.Column >= col {
			col = seg.Part.Position.Column + 1
		}
	}

	return col
}

// partColumnAndValue calculates the column position and value for a content part.
//
// Parameters:
//   - tk: Original token with Type, Value, Position
//   - isFirst: Whether this is the first content part
//   - shouldHaveValue: Whether this part should receive the token's Value
//     (determined by caller: first part for plain/quoted, last part for block scalars)
//
// Column assignment mirrors go-yaml lexer Position.Column behavior:
//   - If shouldHaveValue is true: use the original token's Column
//   - If isFirst is true (even without Value): use the original token's Column
//   - Otherwise: Column defaults to 1
func partColumnAndValue(tk *token.Token, isFirst, shouldHaveValue bool) (int, string) {
	col := 1
	val := ""

	if tk.Value != "" && shouldHaveValue {
		val = tk.Value
		if tk.Position != nil && tk.Position.Column > 0 {
			col = tk.Position.Column
		}
	} else if isFirst && tk.Position != nil && tk.Position.Column > 0 {
		col = tk.Position.Column
	}

	return col, val
}

// countLeadingWhitespace returns the number of leading space characters in s.
func countLeadingWhitespace(s string) int {
	count := 0
	for _, r := range s {
		if r != ' ' {
			break
		}

		count++
	}

	return count
}

// countLeadingNewlines returns the number of leading newline characters in s.
// CR characters are skipped to properly handle CRLF line endings.
func countLeadingNewlines(s string) int {
	count := 0
	for _, r := range s {
		if r == '\r' {
			continue // Skip CR in CRLF.
		}
		if r != '\n' {
			break
		}

		count++
	}

	return count
}

// updateIndentLevel calculates indent level based on indentation changes.
// This mirrors the go-yaml scanner's updateIndentLevel logic.
func updateIndentLevel(prevIndentNum, currentIndentNum, currentLevel int) int {
	if prevIndentNum < currentIndentNum {
		return currentLevel + 1
	} else if prevIndentNum > currentIndentNum && currentLevel > 0 {
		return currentLevel - 1
	}

	return currentLevel
}

// isBlockScalarContent returns true if tk is a StringType that follows
// a block scalar header (Literal/Folded). Comments can appear between
// the header and content, so we traverse the Prev chain.
func isBlockScalarContent(tk *token.Token) bool {
	if tk.Type != token.StringType {
		return false
	}
	// Walk backwards through Prev chain, skipping comments.
	for prev := tk.Prev; prev != nil; prev = prev.Prev {
		switch prev.Type {
		case token.LiteralType, token.FoldedType:
			return true
		case token.CommentType:
			// Comments can appear between header and content, continue.
			continue
		default:
			// Any other token type means this is not block scalar content.
			return false
		}
	}

	return false
}

// isPureNewline returns true if s is exactly a line ending (LF or CRLF).
func isPureNewline(s string) bool {
	return s == "\n" || s == "\r\n"
}

// splitOriginIntoParts splits a token's Origin at newline boundaries.
// Empty strings from SplitAfter are filtered, but an empty origin is
// preserved as a single empty part (semantically significant for empty
// block scalar content). Each part retains its trailing newline if present.
func splitOriginIntoParts(origin string) []string {
	// Handle empty origin: preserve as single empty part.
	// This is semantically significant for empty block scalar content.
	if origin == "" {
		return []string{""}
	}

	var parts []string
	for _, p := range strings.SplitAfter(origin, "\n") {
		if p != "" {
			parts = append(parts, p)
		}
	}

	return parts
}

// findLastContentPartIndex returns the index of the last part that contains
// actual content (not a pure newline). Used to identify which part should
// receive the Value for block scalars.
func findLastContentPartIndex(parts []string) int {
	for i := len(parts) - 1; i >= 0; i-- {
		if !isPureNewline(parts[i]) {
			return i
		}
	}

	return len(parts) - 1
}

// shouldPartReceiveValue determines if a token part should receive the Value field.
// Block scalars (literal/folded): Value goes to the last content part.
// Plain/quoted multiline: Value goes to the first content part.
func shouldPartReceiveValue(isBlockScalar, isFirstContentPart, isLastContentPart bool) bool {
	if isBlockScalar {
		return isLastContentPart
	}

	return isFirstContentPart
}

// clonePosition creates a deep copy of a [token.Position].
// Returns nil if pos is nil.
func clonePosition(pos *token.Position) *token.Position {
	if pos == nil {
		return nil
	}

	return &token.Position{
		Line:        pos.Line,
		Column:      pos.Column,
		Offset:      pos.Offset,
		IndentNum:   pos.IndentNum,
		IndentLevel: pos.IndentLevel,
	}
}
