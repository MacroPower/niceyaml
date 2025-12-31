// Package line provides abstractions for line-by-line go-yaml Token processing.
//
// Example input:
//
//	┌───────────────────┐
//	│foo: |-            │
//	│  hello            │
//	│  world            │
//	└───────────────────┘
//
// Normal [token.Tokens] stream:
//
//	┌──────┬────────────┐
//	│String│MappingValue│
//	├──────┴────────────┤
//	│String             │
//	│                   │
//	│                   │
//	└───────────────────┘
//
// Token streams using [Lines]:
//
//	┌──────┬────────────┐
//	│String│MappingValue│
//	├──────┴────────────┤
//	│String             │──┐
//	├───────────────────┤ Join
//	│String             │──┘
//	└───────────────────┘
package line

import (
	"fmt"
	"strings"

	"github.com/goccy/go-yaml/token"
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
	// Tokens for this line.
	value token.Tokens
	// Annotation contains any extra content associated with this line.
	Annotation Annotation
	// Flag indicates the optional special category for this line.
	Flag Flag
	// Joins with the previous line.
	joinPrev bool
	// Joins with the next line.
	joinNext bool
	// IsBlockScalarJoin indicates this line is part of block scalar content (literal/folded).
	// Used to differentiate from plain multiline strings during token recombination.
	isBlockScalarJoin bool
	// The 1-indexed line number used for display purposes.
	// This may differ from the first token's Position.Line for block scalars.
	number int
}

// Number returns the line number of this [Line].
func (l Line) Number() int {
	if l.number != 0 {
		return l.number
	}

	if len(l.value) == 0 || l.value[0].Position == nil {
		return 0
	}

	return l.value[0].Position.Line
}

// Content returns the line content as a string.
// Trailing line endings (LF or CRLF) are stripped for clean comparison.
func (l Line) Content() string {
	var sb strings.Builder
	for _, tk := range l.value {
		origin := strings.TrimSuffix(tk.Origin, "\n")
		origin = strings.TrimSuffix(origin, "\r")
		sb.WriteString(origin)
	}

	return sb.String()
}

// Clone returns a deep copy of the Line.
func (l Line) Clone() Line {
	clone := Line{
		Annotation:        l.Annotation,
		Flag:              l.Flag,
		joinPrev:          l.joinPrev,
		joinNext:          l.joinNext,
		isBlockScalarJoin: l.isBlockScalarJoin,
	}
	for _, tk := range l.value {
		clone.value = append(clone.value, tk.Clone())
	}

	return clone
}

// Tokens returns the tokens for this line.
func (l Line) Tokens() token.Tokens {
	return l.value
}

// Token returns the token at the given index. Panics if idx is out of range.
func (l Line) Token(idx int) *token.Token {
	return l.value[idx]
}

// IsEmpty returns true if there are no tokens on this line.
func (l Line) IsEmpty() bool {
	return len(l.value) == 0
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
// This function splits multiline tokens into per-line parts while preserving
// all Position fields to match go-yaml lexer behavior:
//
// Position field semantics (all 1-indexed):
//   - Line: Line number in the document
//   - Column: Points to where Value starts (with exceptions, see below)
//   - Offset: Rune offset from document start (NOT byte offset)
//   - IndentNum: Leading spaces on the current line (space chars only)
//   - IndentLevel: Nesting depth based on indentation changes
//
// Column exceptions by token type:
//   - SingleQuoteType/DoubleQuoteType: Column points to opening quote character
//   - CommentType: Column points to '#' character
//   - LiteralType/FoldedType: Column points to '|' or '>' indicator
//   - Multiline tokens: Column is recalculated using strings.Index()
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
// Column=0 is a special marker indicating "first-line position" for multi-line
// block scalars with following content. This marker is preserved during splitting
// and used during recombination to select the correct Position.
//
// Additional lexer behaviors:
//   - CRLF (\r\n) is preserved in Origin but normalized to \n in Value
//   - Blank lines are absorbed into the previous token's Origin
//   - Comments include the trailing newline in Origin but not in Value
//
// Transformation into [Lines] is invertible via [Lines.Tokens].
//
//nolint:nestif // Complex token splitting logic requires nested conditions.
func NewLines(tks token.Tokens) Lines {
	if len(tks) == 0 {
		return nil
	}

	var (
		lines                     []Line
		currentLineTokens         token.Tokens
		currentLine               int  // Current line number being built.
		joinFromPrev              bool // Track if the next line should have JoinPrev=true.
		blockScalarJoinFromPrev   bool // Track if the join is for a block scalar.
		currentLineHasBlockScalar bool // Track if current line has any block scalar content.
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
		isBlockScalar := isBlockScalarContent(tk)

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
			if tkLine > currentLine+1 && len(currentLineTokens) > 0 {
				lines = append(lines, Line{value: currentLineTokens, joinPrev: joinFromPrev, number: currentLine})
				joinFromPrev = false
				currentLineTokens = nil
			}

			if len(currentLineTokens) == 0 && tkLine > currentLine {
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
				// Create a token for the duplicate newline and attach to previous line.
				lastLine := &lines[len(lines)-1]
				newTk := &token.Token{
					Type:          tk.Type,
					CharacterType: tk.CharacterType,
					Indicator:     tk.Indicator,
					Origin:        part,
					Position: &token.Position{
						Line:        currentLine - 1, // Goes on previous line.
						Column:      linesNextColumn(lastLine.value),
						Offset:      currentOffset,
						IndentNum:   prevLineIndentNum,
						IndentLevel: currentIndentLevel,
					},
				}
				lastLine.value.Add(newTk)

				lastLine.joinNext = true
				joinFromPrev = true
				currentOffset += len(part)

				continue
			}

			// Update indentation tracking for first content on new line.
			// Use the token's Position if available (more accurate than counting spaces
			// in Origin, since some tokens like MappingKey don't include leading spaces).
			// Exception: multi-part block scalar content has special Position handling,
			// so calculate indentation for each part to maintain proper tracking.
			if len(currentLineTokens) == 0 && !partIsPureNewline {
				if i == 0 && tk.Position != nil && (!isBlockScalar || !isMultiPart) {
					// First part of non-block-scalar, or single-line block scalar:
					// use the token's Position.
					currentIndentNum = tk.Position.IndentNum
					currentIndentLevel = tk.Position.IndentLevel
				} else {
					// Subsequent parts, or multi-part block scalars: calculate from Origin.
					currentIndentNum = countLeadingSpaces(part)
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
			shouldHaveValue := shouldPartReceiveValue(isBlockScalar, isFirstContentPart, isLastContentPart)
			// Capture before it changes for later use.
			wasFirstContentPart := isFirstContentPart && !partIsPureNewline
			if partIsPureNewline {
				col = linesNextColumn(currentLineTokens)
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
				(isLastPart && isBlockScalar) ||
				(shouldHaveValue && val != "")
			valueOffset := currentOffset
			if useOriginalOffset && tk.Position != nil && tk.Position.Offset > 0 {
				valueOffset = tk.Position.Offset
			}

			// Create and add token for this part.
			//
			// Position fields (matching go-yaml lexer behavior):
			//   - Line: 1-indexed line number in the document.
			//   - Column: 1-indexed column where Value starts (not Origin).
			//   - Offset: 1-indexed rune position where Value starts in document.
			//     Exception: Comment tokens point to "#" (Origin start).
			//   - IndentNum: Number of leading spaces at the start of this line.
			//   - IndentLevel: Nesting depth based on indentation changes.
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
			// - With following content: Position points to first content line (Column=0)
			// - Standalone: Position points to last content line (Column>0)
			// We determine which case and put the original Position on the appropriate part.
			if isBlockScalar && isMultiPart && tk.Position != nil {
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

			currentLineTokens.Add(newTk)

			// Track if this line contains block scalar content.
			if isBlockScalar && isMultiPart {
				currentLineHasBlockScalar = true
			}

			currentOffset += len(part)

			// If this part ends with a line break (\n or \r), finish the current line.
			// The go-yaml lexer can split CRLF across tokens: comments may end with \r
			// while the following token starts with \n. Both \r and \n are line breaks.
			if strings.HasSuffix(part, "\n") || strings.HasSuffix(part, "\r") {
				// Set JoinNext if this is a multi-part token and not the last part.
				joinNext := isMultiPart && !isLastPart
				// Use currentLineHasBlockScalar to capture if ANY part of this line is block scalar content.
				lineIsBlockScalarJoin := currentLineHasBlockScalar
				lines = append(lines, Line{
					value:             currentLineTokens,
					joinPrev:          joinFromPrev,
					joinNext:          joinNext,
					isBlockScalarJoin: lineIsBlockScalarJoin,
					number:            currentLine,
				})
				joinFromPrev = joinNext
				blockScalarJoinFromPrev = lineIsBlockScalarJoin && joinNext

				// Prepare indentation tracking for next line.
				prevLineIndentNum = currentIndentNum

				currentLineTokens = nil
				currentLineHasBlockScalar = false // Reset for next line.
				currentIndentNum = 0              // Will be recalculated for next line's first content.
				currentLine++
			}
		}

		// Track whether this token ended with a line break for duplicate detection.
		// Include both \n and \r since go-yaml can split CRLF across tokens.
		prevTokenEndedWithNewline = strings.HasSuffix(origin, "\n") || strings.HasSuffix(origin, "\r")
	}

	// Handle last line (may not end with newline).
	if len(currentLineTokens) > 0 {
		// Use either the current line's block scalar content OR the inherited join state.
		lineIsBlockScalarJoin := currentLineHasBlockScalar || blockScalarJoinFromPrev
		lines = append(lines, Line{
			value:             currentLineTokens,
			joinPrev:          joinFromPrev,
			isBlockScalarJoin: lineIsBlockScalarJoin,
			number:            currentLine,
		})
	}

	return lines
}

// Tokens reconstructs the full [token.Tokens] stream from all [Line]s.
//
// For multiline tokens that were split across lines, this function recombines
// them by concatenating Origin and Value, and selecting the appropriate Position.
//
// Position selection for recombined tokens (matching go-yaml lexer behavior):
//   - Plain multiline strings: Use first part's Position (points to first line)
//   - Quoted multiline strings: Use first part's Position (points to opening quote line)
//   - Block scalars: Position selection depends on the Column=0 marker
//
// Block scalar Position selection:
//   - Column=0 (first-line position): Keep first part's Position (multi-line with following)
//   - Column>0 (last-line position): Use last content part's Position (multi-line standalone)
//
// The Column=0 marker from [NewLines] indicates that the lexer originally used
// first-line position semantics, so we preserve the first part's Position during
// recombination. Otherwise, we use the last part's Position.
//
// This method is tested to perfectly invert [NewLines].
func (s Lines) Tokens() token.Tokens {
	if len(s) == 0 {
		return nil
	}

	result := token.Tokens{}

	for i := range s {
		line := s[i]

		for j, tk := range line.value {
			// If this is the first token of a joined line, merge with previous token.
			//nolint:nestif // Recombination logic requires nested conditions.
			if j == 0 && line.joinPrev && len(result) > 0 {
				prev := result[len(result)-1]
				prev.Origin += tk.Origin

				if tk.Value != "" {
					if prev.Value != "" {
						prev.Value += tk.Value
					} else {
						// First non-empty Value - use this part's position
						// since Position points to where Value starts.
						// Exception: For block scalars with first-line-position (Column=0),
						// the correct Position was already set from the first part.
						prev.Value = tk.Value
						isFirstLinePosition := prev.Position != nil && prev.Position.Column == 0
						if tk.Position != nil && (!line.isBlockScalarJoin || !isFirstLinePosition) {
							prev.Position = clonePosition(tk.Position)
						}
					}
				}

				// For block scalars with last-line-position (Column>0), use the last part's Position.
				// The last content part has the original lexer Position preserved from NewLines.
				isFirstLinePosition := prev.Position != nil && prev.Position.Column == 0
				if !line.joinNext && tk.Position != nil && line.isBlockScalarJoin && !isFirstLinePosition {
					prev.Position = clonePosition(tk.Position)
				}

				continue
			}

			// Clone the token to avoid modifying the original.
			newTk := tk.Clone()
			result.Add(newTk)
		}
	}

	return result
}

// Validate checks the integrity of the [Lines]. It ensures that:
//   - JoinPrev/JoinNext flags are consistent between adjacent [Line]s
//   - Line numbers are strictly increasing
//   - Every token on a given [Line] has an identical line number in its Position
//   - Every token on a given [Line] has columns that are strictly increasing
//
// Returns an error if any validation check fails.
func (s Lines) Validate() error {
	prevLineNum := 0

	for i, line := range s {
		// Check: line numbers strictly increasing.
		lineNum := line.Number()
		if lineNum != 0 && lineNum <= prevLineNum {
			return fmt.Errorf("line at index %d: line number %d not greater than previous %d", i, lineNum, prevLineNum)
		}
		if lineNum != 0 {
			prevLineNum = lineNum
		}

		// Check: JoinPrev/JoinNext consistency.
		if i > 0 && s[i-1].joinNext != line.joinPrev {
			return fmt.Errorf(
				"line at index %d: JoinPrev=%v inconsistent with previous line JoinNext=%v",
				i,
				line.joinPrev,
				s[i-1].joinNext,
			)
		}

		// Check: all tokens have identical line number and columns are strictly increasing.
		var (
			expectedLineNum = -1
			prevCol         = 0
		)
		for j, tk := range line.value {
			if tk.Position == nil {
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

// linesNextColumn returns the next available column position after existing tokens.
func linesNextColumn(tks token.Tokens) int {
	col := 1
	for _, tk := range tks {
		if tk.Position != nil && tk.Position.Column >= col {
			col = tk.Position.Column + 1
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

// countLeadingSpaces returns the number of leading space/tab characters in s.
func countLeadingSpaces(s string) int {
	count := 0
	for _, r := range s {
		if r != ' ' && r != '\t' {
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

// splitOriginIntoParts splits a token's Origin at newline boundaries,
// filtering empty parts. Each part retains its trailing newline if present.
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
