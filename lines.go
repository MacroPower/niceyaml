package niceyaml

import (
	"fmt"
	"strings"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/lexer"
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

// Flag identifies a category for YAML tokens.
type Flag int

// Flag constants for YAML line categories.
const (
	FlagDefault  Flag = iota // Default/fallback.
	FlagInserted             // Lines inserted in diff (+).
	FlagDeleted              // Lines deleted in diff (-).
)

// Line contains metadata about a specific line in a [Lines] collection.
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
}

// Number returns the 1-indexed line number of this [Line] based on its [token.Position].
func (l Line) Number() int {
	if len(l.value) == 0 || l.value[0].Position == nil {
		return 0
	}

	// Note: All tokens in a line should have the same line number in their Position.
	// If this is not the case, it means the tokens were not properly split into [Line]s.
	// Thus, we return the line number of the first token.
	return l.value[0].Position.Line
}

// Content returns the line content as a string.
// Trailing newlines are stripped for clean comparison.
func (l Line) Content() string {
	var sb strings.Builder
	for _, tk := range l.value {
		origin := strings.TrimSuffix(tk.Origin, "\n")
		sb.WriteString(origin)
	}

	return sb.String()
}

// Clone returns a deep copy of the Line.
func (l Line) Clone() Line {
	clone := Line{
		Annotation: l.Annotation,
		Flag:       l.Flag,
		joinPrev:   l.joinPrev,
		joinNext:   l.joinNext,
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

// Lines represents a collection of [token.Tokens] organized into [Line]s with associated metadata.
//
// Input:
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
//
// This enables clean line-by-line processing, while preserving the original token data.
type Lines struct {
	Name  string
	lines []Line
}

// LinesOption configures [Lines] creation.
type LinesOption func(*Lines)

// WithName sets the name for the [Lines].
func WithName(name string) LinesOption {
	return func(t *Lines) {
		t.Name = name
	}
}

// NewLinesFromString calls [lexer.Tokenize] to create new [Lines] from a YAML string.
func NewLinesFromString(src string, opts ...LinesOption) *Lines {
	tks := lexer.Tokenize(src)

	return NewLinesFromTokens(tks, opts...)
}

// NewLinesFromFile creates new [Lines] from an [*ast.File].
// It uses the [*ast.File]'s Name to name the collection.
func NewLinesFromFile(f *ast.File) *Lines {
	tk := findAnyTokenInFile(f)

	return NewLinesFromToken(tk, WithName(f.Name))
}

// NewLinesFromToken creates new [Lines] from a seed [*token.Token].
// It collects all [token.Tokens] by walking the token chain from start to end.
func NewLinesFromToken(tk *token.Token, opts ...LinesOption) *Lines {
	if tk == nil {
		return &Lines{}
	}

	// Walk to initial token.
	for tk.Prev != nil {
		tk = tk.Prev
	}

	// Collect all tokens forward.
	var tks token.Tokens
	for ; tk != nil; tk = tk.Next {
		// Skip parser-added implicit null tokens to match lexer output.
		if tk.Type == token.ImplicitNullType {
			continue
		}

		tks.Add(tk.Clone())
	}

	return NewLinesFromTokens(tks, opts...)
}

// NewLinesFromTokens creates new [Lines] from [token.Tokens].
//
//nolint:nestif // TODO: refactor.
func NewLinesFromTokens(tks token.Tokens, opts ...LinesOption) *Lines {
	t := &Lines{}
	for _, opt := range opts {
		opt(t)
	}

	if len(tks) == 0 {
		return t
	}

	var (
		lines             []Line
		currentLineTokens token.Tokens
		currentLine       int  // Current line number being built.
		joinFromPrev      bool // Track if the next line should have JoinPrev=true.

		// Position tracking.
		currentOffset      int // Cumulative byte offset (1-indexed like lexer).
		currentIndentNum   int // Leading spaces on current line.
		prevLineIndentNum  int // IndentNum from previous line.
		currentIndentLevel int // Nesting depth level.
	)

	// Initialize currentLine from the first token's position.
	if tks[0].Position != nil {
		currentLine = tks[0].Position.Line
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
				lines = append(lines, Line{value: currentLineTokens, joinPrev: joinFromPrev})
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
		var parts []string
		for _, p := range strings.SplitAfter(origin, "\n") {
			if p != "" {
				parts = append(parts, p)
			}
		}

		// Multi-part means the token's content spans multiple lines.
		// This distinguishes actual multi-line content (like literal blocks)
		// from tokens that just have leading/trailing whitespace newlines.
		isMultiPart := len(parts) > 1 && strings.Contains(tk.Value, "\n")

		isFirstContentPart := true

		for i, part := range parts {
			isPureNewline := part == "\n"

			// Update indentation tracking for first content on new line.
			if len(currentLineTokens) == 0 && !isPureNewline {
				currentIndentNum = countLeadingSpaces(part)
				currentIndentLevel = updateIndentLevel(prevLineIndentNum, currentIndentNum, currentIndentLevel)
			}

			var (
				col int
				val string
			)

			if isPureNewline {
				col = linesNextColumn(currentLineTokens)
			} else {
				col, val = partColumnAndValue(tk, part, isFirstContentPart)
				isFirstContentPart = false
			}

			// Calculate offset where Value starts within the document.
			// Offset typically points to Value, not Origin (like the lexer's behavior).
			// Exception: Comment tokens have Offset pointing to "#" (Origin start).
			valueOffset := currentOffset
			if val != "" && tk.Type != token.CommentType {
				if idx := strings.Index(part, val); idx >= 0 {
					valueOffset += idx
				}
			}

			// Create and add token for this part.
			//
			// Position fields (matching go-yaml lexer behavior):
			//   - Line: 1-indexed line number in the document.
			//   - Column: 1-indexed column where Value starts (not Origin).
			//   - Offset: 1-indexed byte position where Value starts in document.
			//     Exception: Comment tokens point to "#" (Origin start).
			//   - IndentNum: Number of leading spaces at the start of this line.
			//   - IndentLevel: Nesting depth based on indentation changes.
			newTk := &token.Token{
				Type:   tk.Type,
				Origin: part,
				Value:  val,
				Position: &token.Position{
					Line:        currentLine,
					Column:      col,
					Offset:      valueOffset,
					IndentNum:   currentIndentNum,
					IndentLevel: currentIndentLevel,
				},
			}
			currentLineTokens.Add(newTk)

			currentOffset += len(part)

			// If this part ends with newline, finish the current line.
			if strings.HasSuffix(part, "\n") {
				// Set JoinNext if this is a multi-part token and not the last part.
				isLastPart := i == len(parts)-1
				joinNext := isMultiPart && !isLastPart
				lines = append(lines, Line{
					value:    currentLineTokens,
					joinPrev: joinFromPrev,
					joinNext: joinNext,
				})
				joinFromPrev = joinNext
				currentLineTokens = nil
				currentLine++

				// Prepare indentation tracking for next line.
				prevLineIndentNum = currentIndentNum
				currentIndentNum = 0 // Will be recalculated for next line's first content.
			}
		}
	}

	// Handle last line (may not end with newline).
	if len(currentLineTokens) > 0 {
		lines = append(lines, Line{value: currentLineTokens, joinPrev: joinFromPrev})
	}

	t.lines = lines

	return t
}

// Tokens reconstructs and returns the full [token.Tokens] stream from all lines.
// The returned tokens have their Prev/Next pointers properly connected.
func (t *Lines) Tokens() token.Tokens {
	if len(t.lines) == 0 {
		return nil
	}

	result := token.Tokens{}
	for i := range t.lines {
		for _, tk := range t.lines[i].value {
			result.Add(tk)
		}
	}

	return result
}

// LineCount returns the number of lines.
func (t *Lines) LineCount() int {
	return len(t.lines)
}

// IsEmpty returns true if there are no lines.
func (t *Lines) IsEmpty() bool {
	return len(t.lines) == 0
}

// EachLine iterates over all lines, calling fn for each.
func (t *Lines) EachLine(fn func(idx int, line Line)) {
	for i, line := range t.lines {
		fn(i, line)
	}
}

// EachRune iterates through all runes in all lines with their positions.
func (t *Lines) EachRune(fn func(r rune, pos Position)) {
	for i, line := range t.lines {
		col := 0

		for _, tk := range line.value {
			for _, r := range tk.Origin {
				fn(r, NewPosition(i, col))

				col++
			}
		}
	}
}

// Line returns the [Line] at the given index. Panics if idx is out of range.
func (t *Lines) Line(idx int) Line {
	return t.lines[idx]
}

// Annotate sets an [Annotation] on the [Line] at the given index.
// Panics if idx is out of range.
func (t *Lines) Annotate(idx int, ann Annotation) {
	t.lines[idx].Annotation = ann
}

// SetFlag sets a [Flag] on the [Line] at the given index.
// Panics if idx is out of range.
func (t *Lines) SetFlag(idx int, flag Flag) {
	t.lines[idx].Flag = flag
}

// Content returns the combined content of all [Line]s as a string.
// [Line]s are joined with newlines.
func (t *Lines) Content() string {
	if len(t.lines) == 0 {
		return ""
	}

	sb := strings.Builder{}
	for i, l := range t.lines {
		if i > 0 {
			sb.WriteByte('\n')
		}

		sb.WriteString(l.Content())
	}

	return sb.String()
}

// String reconstructs all [Line]s as a string, including any annotations.
// This should generally only be used for debugging.
func (t *Lines) String() string {
	var sb strings.Builder
	for i, l := range t.lines {
		if i > 0 {
			sb.WriteByte('\n')
		}

		sb.WriteString(l.String())
	}

	return sb.String()
}

// Validate checks the integrity of the [Lines]. It ensures that:
//   - JoinPrev/JoinNext flags are consistent between adjacent [Line]s
//   - Line numbers are strictly increasing
//   - Every token on a given [Line] has an identical line number in its Position
//   - Every token on a given [Line] has columns that are strictly increasing
//
// Returns an error if any validation check fails.
func (t *Lines) Validate() error {
	prevLineNum := 0

	for i, line := range t.lines {
		// Check: line numbers strictly increasing.
		lineNum := line.Number()
		if lineNum != 0 && lineNum <= prevLineNum {
			return fmt.Errorf("line at index %d: line number %d not greater than previous %d", i, lineNum, prevLineNum)
		}
		if lineNum != 0 {
			prevLineNum = lineNum
		}

		// Check: JoinPrev/JoinNext consistency.
		if i > 0 && t.lines[i-1].joinNext != line.joinPrev {
			return fmt.Errorf(
				"line at index %d: JoinPrev=%v inconsistent with previous line JoinNext=%v",
				i,
				line.joinPrev,
				t.lines[i-1].joinNext,
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

	return nil
}

// PositionsFromToken returns all positions where the given token appears.
// A token may appear on multiple lines when split across lines.
// Returns nil if the token is nil or not found in the Lines.
func (t *Lines) PositionsFromToken(tk *token.Token) []Position {
	if tk == nil {
		return nil
	}

	var positions []Position

	for i, line := range t.lines {
		for j, lineTk := range line.value {
			if lineTk == tk {
				col := absColForTokenIndex(line, j)
				positions = append(positions, NewPosition(i, col))
			}
		}
	}

	return positions
}

// TokenPositionRangesFromToken returns all position ranges for a given token.
// This is a convenience method that combines [Lines.PositionsFromToken] and [Lines.TokenPositionRanges].
// Returns nil if the token is nil or not found in the Lines.
func (t *Lines) TokenPositionRangesFromToken(tk *token.Token) []PositionRange {
	positions := t.PositionsFromToken(tk)
	return t.TokenPositionRanges(positions...)
}

// TokenPositionRanges returns all token position ranges that are part of
// the same joined token group as the tokens at the given [Position]s.
// For non-joined lines, returns the range of the token at each given column.
// Duplicate ranges are removed.
// Returns nil if no tokens exist at any of the given positions.
func (t *Lines) TokenPositionRanges(positions ...Position) []PositionRange {
	var allRanges []PositionRange

	for _, pos := range positions {
		ranges := t.tokenPositionRangesForPos(pos)
		allRanges = append(allRanges, ranges...)
	}

	return deduplicateRanges(allRanges)
}

// deduplicateRanges removes exact duplicate [PositionRange] entries.
func deduplicateRanges(ranges []PositionRange) []PositionRange {
	if len(ranges) == 0 {
		return nil
	}

	seen := make(map[PositionRange]struct{})
	result := make([]PositionRange, 0, len(ranges))

	for _, r := range ranges {
		if _, exists := seen[r]; !exists {
			seen[r] = struct{}{}
			result = append(result, r)
		}
	}

	return result
}

// tokenPositionRangesForPos returns all token position ranges for a single [Position].
func (t *Lines) tokenPositionRangesForPos(pos Position) []PositionRange {
	if pos.Line < 0 || pos.Line >= len(t.lines) {
		return nil
	}

	line := t.lines[pos.Line]
	if !line.joinPrev && !line.joinNext {
		// For non-joined lines, find and return the token at the given column.
		for i, tk := range line.value {
			absCol := absColForTokenIndex(line, i)
			tkLen := len([]rune(strings.TrimSuffix(tk.Origin, "\n")))
			if pos.Col >= absCol && pos.Col < absCol+tkLen {
				start := NewPosition(pos.Line, absCol)
				end := NewPosition(pos.Line, absCol+tkLen)

				return []PositionRange{NewPositionRange(start, end)}
			}
		}

		return nil
	}

	// For joined lines, find the join token index and check if col is within it.
	joinTokenIdx := -1
	if line.joinPrev {
		joinTokenIdx = 0
	} else if line.joinNext {
		joinTokenIdx = len(line.value) - 1
	}
	if joinTokenIdx == -1 {
		return nil
	}

	joinAbsCol := absColForTokenIndex(line, joinTokenIdx)
	joinTk := line.value[joinTokenIdx]
	joinTkLen := len([]rune(strings.TrimSuffix(joinTk.Origin, "\n")))
	if pos.Col < joinAbsCol || pos.Col >= joinAbsCol+joinTkLen {
		return nil
	}

	var ranges []PositionRange

	start := NewPosition(pos.Line, joinAbsCol)
	end := NewPosition(pos.Line, joinAbsCol+joinTkLen)
	ranges = append(ranges, NewPositionRange(start, end))

	// Walk backward through JoinPrev lines.
	for i := pos.Line - 1; i >= 0 && t.lines[i].joinNext; i-- {
		prevLine := t.lines[i]
		if len(prevLine.value) == 0 {
			continue
		}
		// JoinNext means the last token continues.
		tkIdx := len(prevLine.value) - 1
		tk := prevLine.value[tkIdx]
		absCol := absColForTokenIndex(prevLine, tkIdx)
		tkLen := len([]rune(strings.TrimSuffix(tk.Origin, "\n")))
		start := NewPosition(i, absCol)
		end := NewPosition(i, absCol+tkLen)
		ranges = append(ranges, NewPositionRange(start, end))
	}

	// Walk forward through JoinNext lines.
	for i := pos.Line + 1; i < len(t.lines) && t.lines[i].joinPrev; i++ {
		nextLine := t.lines[i]
		if len(nextLine.value) == 0 {
			continue
		}
		// JoinPrev means the first token is the continuation.
		tk := nextLine.value[0]
		absCol := absColForTokenIndex(nextLine, 0)
		tkLen := len([]rune(strings.TrimSuffix(tk.Origin, "\n")))
		start := NewPosition(i, absCol)
		end := NewPosition(i, absCol+tkLen)
		ranges = append(ranges, NewPositionRange(start, end))
	}

	return ranges
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
func partColumnAndValue(tk *token.Token, part string, isFirst bool) (int, string) {
	col := 1
	val := ""

	if tk.Value != "" && strings.Contains(part, tk.Value) {
		val = tk.Value
		if isFirst && tk.Position != nil && tk.Position.Column > 0 {
			col = tk.Position.Column
		} else {
			col = strings.Index(part, tk.Value) + 1
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

func findAnyTokenInFile(f *ast.File) *token.Token {
	for _, doc := range f.Docs {
		if doc.Start != nil {
			return doc.Start
		}
		if doc.Body != nil {
			return doc.Body.GetToken()
		}
		if doc.End != nil {
			return doc.End
		}
	}

	return nil
}

// absColForTokenIndex calculates the 0-indexed column where the token at idx starts.
func absColForTokenIndex(line Line, idx int) int {
	col := 0
	for i := range idx {
		col += len([]rune(strings.TrimSuffix(line.value[i].Origin, "\n")))
	}

	return col
}
