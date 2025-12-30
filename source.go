package niceyaml

import (
	"errors"
	"fmt"
	"iter"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/parser"
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

// Source represents a collection of [token.Tokens] organized into [Line]s with associated metadata.
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
// Token streams using [Source]:
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
//
// Source operations are all idempotent, and translations to/from [token.Tokens] are lossless.
type Source struct {
	Name  string
	lines []Line
}

// SourceOption configures [Source] creation.
type SourceOption func(*Source)

// WithName sets the name for the [Source].
func WithName(name string) SourceOption {
	return func(t *Source) {
		t.Name = name
	}
}

// NewSourceFromString calls [lexer.Tokenize] to create new [Source] from a YAML string.
func NewSourceFromString(src string, opts ...SourceOption) *Source {
	tks := lexer.Tokenize(src)

	return NewSourceFromTokens(tks, opts...)
}

// NewSourceFromToken creates new [Source] from a seed [*token.Token].
// It collects all [token.Tokens] by walking the token chain from start to end.
//
// ImplicitNullType tokens are filtered out because they are created by the parser,
// not the lexer. The lexer never produces ImplicitNullType - these are synthesized
// by the parser for empty values like "key:" with no value. Including them would
// break round-trip equality with lexer output.
func NewSourceFromToken(tk *token.Token, opts ...SourceOption) *Source {
	if tk == nil {
		return &Source{}
	}

	// Walk to initial token.
	for tk.Prev != nil {
		tk = tk.Prev
	}

	// Collect all tokens forward, filtering parser-only tokens.
	var tks token.Tokens
	for ; tk != nil; tk = tk.Next {
		// ImplicitNullType is created by parser for empty values (e.g., "key:").
		// The lexer never produces this type, so we skip it for round-trip fidelity.
		if tk.Type == token.ImplicitNullType {
			continue
		}

		tks.Add(tk.Clone())
	}

	return NewSourceFromTokens(tks, opts...)
}

// NewSourceFromTokens creates new [Source] from [token.Tokens].
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
//   - Block scalar content (StringType after Literal/Folded): Points to LAST line
//   - Plain multiline strings: Points to FIRST line
//   - Quoted multiline strings: Points to opening quote line
//
//nolint:nestif // TODO: refactor.
func NewSourceFromTokens(tks token.Tokens, opts ...SourceOption) *Source {
	t := &Source{}
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
		currentOffset      int // Cumulative rune offset (1-indexed like lexer).
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

			// Update indentation tracking for first content on new line.
			if len(currentLineTokens) == 0 && !partIsPureNewline {
				currentIndentNum = countLeadingSpaces(part)
				currentIndentLevel = updateIndentLevel(prevLineIndentNum, currentIndentNum, currentIndentLevel)
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
			currentLineTokens.Add(newTk)

			currentOffset += len(part)

			// If this part ends with newline, finish the current line.
			if strings.HasSuffix(part, "\n") {
				// Set JoinNext if this is a multi-part token and not the last part.
				joinNext := isMultiPart && !isLastPart
				lines = append(lines, Line{
					value:             currentLineTokens,
					joinPrev:          joinFromPrev,
					joinNext:          joinNext,
					isBlockScalarJoin: isBlockScalar && isMultiPart,
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

// Tokens reconstructs the full [token.Tokens] stream from all [Line]s.
//
// For multiline tokens that were split across lines, this function recombines
// them by concatenating Origin and Value, and selecting the appropriate Position.
//
// Position selection for recombined tokens (matching go-yaml lexer behavior):
//   - Block scalars (literal/folded): Use last part's Position (points to last content line)
//   - Plain multiline strings: Use first part's Position (points to first line)
//   - Quoted multiline strings: Use first part's Position (points to opening quote line)
func (s *Source) Tokens() token.Tokens {
	if len(s.lines) == 0 {
		return nil
	}

	result := token.Tokens{}

	for i := range s.lines {
		line := s.lines[i]

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
						prev.Value = tk.Value
						if tk.Position != nil {
							prev.Position = clonePosition(tk.Position)
						}
					}
				}

				// For the last part of a joined sequence (joinPrev but not joinNext),
				// determine whether to use this part's Position.
				//
				// Block scalars: Position should come from the last part (lexer behavior).
				// Plain multiline strings: Position should stay from first part where Value was set.
				if !line.joinNext && tk.Position != nil && line.isBlockScalarJoin {
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

// Parse parses the Source tokens into an [ast.File].
// Any YAML parsing errors are converted to [Error] with source annotations.
func (s *Source) Parse(opts ...parser.Option) (*ast.File, error) {
	file, err := parser.Parse(s.Tokens(), parser.ParseComments, opts...)
	if err == nil {
		return file, nil
	}

	var yamlErr yaml.Error
	if errors.As(err, &yamlErr) {
		return nil, NewError(
			errors.New(yamlErr.GetMessage()),
			WithErrorToken(yamlErr.GetToken()),
		)
	}

	//nolint:wrapcheck // Return the original error if it's not a [yaml.Error].
	return nil, err
}

// Count returns the number of lines.
func (s *Source) Count() int {
	return len(s.lines)
}

// IsEmpty returns true if there are no lines.
func (s *Source) IsEmpty() bool {
	return len(s.lines) == 0
}

// Lines returns an iterator over all lines.
// Each iteration yields a [Position] and the [Line] at that position.
func (s *Source) Lines() iter.Seq2[Position, Line] {
	return func(yield func(Position, Line) bool) {
		for i, line := range s.lines {
			if !yield(NewPosition(i, 0), line) {
				return
			}
		}
	}
}

// Runes returns an iterator over all runes.
// Each iteration yields a [Position] and the rune at that position.
func (s *Source) Runes() iter.Seq2[Position, rune] {
	return func(yield func(Position, rune) bool) {
		for i, line := range s.lines {
			col := 0

			for _, tk := range line.value {
				for _, r := range tk.Origin {
					if !yield(NewPosition(i, col), r) {
						return
					}

					col++
				}
			}
		}
	}
}

// Line returns the [Line] at the given index. Panics if idx is out of range.
func (s *Source) Line(idx int) Line {
	return s.lines[idx]
}

// Annotate sets an [Annotation] on the [Line] at the given index.
// Panics if idx is out of range.
func (s *Source) Annotate(idx int, ann Annotation) {
	s.lines[idx].Annotation = ann
}

// SetFlag sets a [Flag] on the [Line] at the given index.
// Panics if idx is out of range.
func (s *Source) SetFlag(idx int, flag Flag) {
	s.lines[idx].Flag = flag
}

// Content returns the combined content of all [Line]s as a string.
// [Line]s are joined with newlines.
func (s *Source) Content() string {
	if len(s.lines) == 0 {
		return ""
	}

	sb := strings.Builder{}
	for i, l := range s.lines {
		if i > 0 {
			sb.WriteByte('\n')
		}

		sb.WriteString(l.Content())
	}

	return sb.String()
}

// String reconstructs all [Line]s as a string, including any annotations.
// This should generally only be used for debugging.
func (s *Source) String() string {
	var sb strings.Builder
	for i, l := range s.lines {
		if i > 0 {
			sb.WriteByte('\n')
		}

		sb.WriteString(l.String())
	}

	return sb.String()
}

// Validate checks the integrity of the [Source]. It ensures that:
//   - JoinPrev/JoinNext flags are consistent between adjacent [Line]s
//   - Line numbers are strictly increasing
//   - Every token on a given [Line] has an identical line number in its Position
//   - Every token on a given [Line] has columns that are strictly increasing
//
// Returns an error if any validation check fails.
func (s *Source) Validate() error {
	prevLineNum := 0

	for i, line := range s.lines {
		// Check: line numbers strictly increasing.
		lineNum := line.Number()
		if lineNum != 0 && lineNum <= prevLineNum {
			return fmt.Errorf("line at index %d: line number %d not greater than previous %d", i, lineNum, prevLineNum)
		}
		if lineNum != 0 {
			prevLineNum = lineNum
		}

		// Check: JoinPrev/JoinNext consistency.
		if i > 0 && s.lines[i-1].joinNext != line.joinPrev {
			return fmt.Errorf(
				"line at index %d: JoinPrev=%v inconsistent with previous line JoinNext=%v",
				i,
				line.joinPrev,
				s.lines[i-1].joinNext,
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
// Returns nil if the token is nil or not found in the Source.
func (s *Source) PositionsFromToken(tk *token.Token) []Position {
	if tk == nil {
		return nil
	}

	var positions []Position

	for i, line := range s.lines {
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
// This is a convenience method that combines [Source.PositionsFromToken] and [Source.TokenPositionRanges].
// Returns nil if the token is nil or not found in the Source.
func (s *Source) TokenPositionRangesFromToken(tk *token.Token) []PositionRange {
	positions := s.PositionsFromToken(tk)
	return s.TokenPositionRanges(positions...)
}

// TokenPositionRanges returns all token position ranges that are part of
// the same joined token group as the tokens at the given [Position]s.
// For non-joined lines, returns the range of the token at each given column.
// Duplicate ranges are removed.
// Returns nil if no tokens exist at any of the given positions.
func (s *Source) TokenPositionRanges(positions ...Position) []PositionRange {
	var allRanges []PositionRange

	for _, pos := range positions {
		ranges := s.tokenPositionRangesForPos(pos)
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
func (s *Source) tokenPositionRangesForPos(pos Position) []PositionRange {
	if pos.Line < 0 || pos.Line >= len(s.lines) {
		return nil
	}

	line := s.lines[pos.Line]
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
	for i := pos.Line - 1; i >= 0 && s.lines[i].joinNext; i-- {
		prevLine := s.lines[i]
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
	for i := pos.Line + 1; i < len(s.lines) && s.lines[i].joinPrev; i++ {
		nextLine := s.lines[i]
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

// absColForTokenIndex calculates the 0-indexed column where the token at idx starts.
func absColForTokenIndex(line Line, idx int) int {
	col := 0
	for i := range idx {
		col += len([]rune(strings.TrimSuffix(line.value[i].Origin, "\n")))
	}

	return col
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

// isPureNewline returns true if s is exactly a single newline character.
func isPureNewline(s string) bool {
	return s == "\n"
}

// splitOriginIntoParts splits a token's Origin at newline boundaries,
// filtering empty parts. Each part retains its trailing newline if present.
func splitOriginIntoParts(origin string) []string {
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
