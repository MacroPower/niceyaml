package line

import (
	"strings"

	"github.com/goccy/go-yaml/token"

	"jacobcolvin.com/niceyaml/tokens"
)

// linesBuilder constructs [Lines] from [token.Tokens].
// It encapsulates all state needed during the line-building process.
// Create instances with [newLinesBuilder].
type linesBuilder struct {
	// Result accumulation.
	lines               []Line
	currentLineSegments tokens.Segments
	currentLine         int // Current line number being built.
	built               bool

	// Token tracking.
	prevTokenEndedWithNewline bool // Track if previous token's Origin ended with "\n".

	// Position tracking.
	currentOffset      int // Cumulative rune offset (1-indexed like lexer).
	currentIndentNum   int // Leading spaces on current line.
	prevLineIndentNum  int // IndentNum from previous line.
	currentIndentLevel int // Nesting depth level.
}

// newLinesBuilder creates a new [*linesBuilder] initialized from the first token.
// Returns nil if tks is empty.
func newLinesBuilder(tks token.Tokens) *linesBuilder {
	if len(tks) == 0 {
		return nil
	}

	b := &linesBuilder{}

	// Initialize currentLine from the first token's position.
	//
	// If the first token's Origin has leading newlines, we need to start earlier
	// because Position.Line points to the content, not the Origin start.
	if tks[0].Position != nil {
		b.currentLine = tks[0].Position.Line
		// Count leading newlines in first token's Origin and adjust.
		leadingNewlines := countLeadingNewlines(tks[0].Origin)
		if leadingNewlines > 0 && b.currentLine > leadingNewlines {
			b.currentLine -= leadingNewlines
		}
	} else {
		b.currentLine = 1
	}

	// Initialize position tracking from first token (1-indexed like lexer).
	if tks[0].Position != nil && tks[0].Position.Offset > 0 {
		b.currentOffset = tks[0].Position.Offset
		b.currentIndentNum = tks[0].Position.IndentNum
		b.currentIndentLevel = tks[0].Position.IndentLevel
	} else {
		b.currentOffset = 1
	}

	// Pre-allocate lines slice based on last token's line number.
	// This provides a reasonable upper bound for expected line count.
	if last := tks[len(tks)-1]; last.Position != nil {
		b.lines = make([]Line, 0, last.Position.Line)
	}

	return b
}

// AddToken adds a single token, splitting it into per-line parts.
func (b *linesBuilder) AddToken(tk *token.Token) {
	if b.built {
		panic("linesBuilder: cannot add token after Build() has been called")
	}

	// Detect if this token is block scalar content by checking if it follows a
	// Literal/Folded header in the token chain.
	isBlockScalarContent := isBlockScalarContent(tk)

	origin := tk.Origin

	// For simple tokens, check for line number gaps and sync forward if needed.
	b.handleGap(tk, origin)

	// Split token at newline boundaries, filtering empty parts upfront.
	parts := splitOriginIntoParts(origin)

	// Multi-part means the token's Origin was split into multiple parts.
	isMultiPart := len(parts) > 1

	// Find the last non-pure-newline part index for Value assignment.
	// Pure newlines (like trailing "\n" in keep blocks) shouldn't get Value.
	lastContentPartIdx := findLastContentPartIndex(parts)

	isFirstContentPart := true

	for i, part := range parts {
		b.processPart(&partContext{
			tk:                   tk,
			part:                 part,
			partIndex:            i,
			isLastPart:           i == len(parts)-1,
			isBlockScalarContent: isBlockScalarContent,
			isMultiPart:          isMultiPart,
			lastContentPartIdx:   lastContentPartIdx,
			parts:                parts,
			isFirstContentPart:   &isFirstContentPart,
		})
	}

	// Track whether this token ended with a newline for duplicate detection.
	// Only \n counts as ending with newline (not \r alone).
	b.prevTokenEndedWithNewline = strings.HasSuffix(origin, "\n")
}

// Build finalizes and returns the constructed [Lines].
func (b *linesBuilder) Build() Lines {
	// Handle the last line which may not end with a newline.
	if len(b.currentLineSegments) > 0 {
		b.lines = append(b.lines, Line{
			segments: b.currentLineSegments,
			number:   b.currentLine,
		})
	}

	// Mark as built to prevent reuse.
	b.built = true

	return b.lines
}

// finishLine completes the current line and prepares for the next one.
func (b *linesBuilder) finishLine() {
	b.lines = append(b.lines, Line{
		segments: b.currentLineSegments,
		number:   b.currentLine,
	})

	// Prepare indentation tracking for next line.
	b.prevLineIndentNum = b.currentIndentNum

	b.currentLineSegments = nil
	b.currentIndentNum = 0 // Will be recalculated for next line's first content.
	b.currentLine++
}

// partContext contains information needed to process a single origin part.
type partContext struct {
	tk                   *token.Token
	isFirstContentPart   *bool
	part                 string
	parts                []string
	partIndex            int
	lastContentPartIdx   int
	isLastPart           bool
	isBlockScalarContent bool
	isMultiPart          bool
}

// processPart processes a single origin part within a token.
// Returns false if this was a duplicate newline that was handled specially.
//
//nolint:nestif // Complex part processing requires nested conditions.
func (b *linesBuilder) processPart(ctx *partContext) bool {
	partIsPureNewline := isPureNewline(ctx.part)

	// Handle duplicate leading newline: the go-yaml lexer sometimes includes the
	// same newline character at both the end of one token and the start of the next.
	//
	// Detect this by checking if we're already at the token's line - if so,
	// processing the leading "\n" would incorrectly advance us past the token's
	// position.
	//
	// Instead of skipping it entirely (which would make Origin non-invertible), we
	// append it to the previous line so the newline is preserved in the Origin but
	// doesn't cause an extra line advance.
	isDuplicateNewline := ctx.partIndex == 0 && partIsPureNewline && b.prevTokenEndedWithNewline &&
		ctx.tk.Position != nil && b.currentLine == ctx.tk.Position.Line
	if isDuplicateNewline && len(b.lines) > 0 {
		// Create a segment for the duplicate newline and attach to previous line.
		lastLine := &b.lines[len(b.lines)-1]
		newTk := &token.Token{
			Type:          ctx.tk.Type,
			CharacterType: ctx.tk.CharacterType,
			Indicator:     ctx.tk.Indicator,
			Origin:        ctx.part,
			Position: &token.Position{
				Line:        b.currentLine - 1, // Goes on previous line.
				Column:      lastLine.segments.NextColumn() + 1,
				Offset:      b.currentOffset,
				IndentNum:   b.prevLineIndentNum,
				IndentLevel: b.currentIndentLevel,
			},
		}
		lastLine.segments = lastLine.segments.Append(ctx.tk, newTk)

		b.currentOffset += len(ctx.part)

		return false
	}

	// Update indentation tracking for first content on new line.
	//
	// Use the token's Position if available (more accurate than counting spaces in
	// Origin, since some tokens like MappingKey don't include leading spaces).
	//
	// Exception: multi-part block scalar content has special Position handling, so
	// calculate indentation for each part to maintain proper tracking.
	if len(b.currentLineSegments) == 0 && !partIsPureNewline {
		if ctx.partIndex == 0 && ctx.tk.Position != nil && (!ctx.isBlockScalarContent || !ctx.isMultiPart) {
			// First part of non-block-scalar, or single-line block scalar:
			// use the token's Position.
			b.currentIndentNum = ctx.tk.Position.IndentNum
			b.currentIndentLevel = ctx.tk.Position.IndentLevel
		} else {
			// Subsequent parts, or multi-part block scalars: calculate from Origin.
			b.currentIndentNum = countLeadingWhitespace(ctx.part)
			b.currentIndentLevel = updateIndentLevel(b.prevLineIndentNum, b.currentIndentNum, b.currentIndentLevel)
		}
	}

	var (
		col int
		val string
	)

	isLastContentPart := ctx.partIndex == ctx.lastContentPartIdx
	// Determine which part should receive the token's Value:
	// Block scalar: Value goes to last content part (lexer behavior).
	// Plain/quoted multiline: Value goes to first content part (lexer behavior).
	shouldHaveValue := shouldPartReceiveValue(ctx.isBlockScalarContent, *ctx.isFirstContentPart, isLastContentPart)
	// Capture before it changes for later use.
	wasFirstContentPart := *ctx.isFirstContentPart && !partIsPureNewline
	if partIsPureNewline {
		col = b.currentLineSegments.NextColumn() + 1
	} else {
		col, val = partColumnAndValue(ctx.tk, *ctx.isFirstContentPart, shouldHaveValue)
		*ctx.isFirstContentPart = false
	}

	// Calculate offset where Value starts within the document.
	// Use original Offset when:
	//   - Single-part token (not split), or
	//   - Last part of block scalar (for recombination Position), or
	//   - Any part that receives the token's Value.
	useOriginalOffset := !ctx.isMultiPart ||
		(ctx.isLastPart && ctx.isBlockScalarContent) ||
		(shouldHaveValue && val != "")
	valueOffset := b.currentOffset
	if useOriginalOffset && ctx.tk.Position != nil && ctx.tk.Position.Offset > 0 {
		valueOffset = ctx.tk.Position.Offset
	}

	// Determine token type: use SpaceType for pure horizontal whitespace parts.
	//
	// This handles cases where the lexer bundles trailing whitespace (like next
	// line's indentation) with the previous token.
	//
	// Exception: block scalar content where whitespace is meaningful and should
	// retain the original StringType.
	tokenType := ctx.tk.Type
	if isPureHorizontalWhitespace(ctx.part) && val == "" && !ctx.isBlockScalarContent {
		tokenType = token.SpaceType
	}

	// Create token for this part.
	newTk := &token.Token{
		Type:          tokenType,
		CharacterType: ctx.tk.CharacterType,
		Indicator:     ctx.tk.Indicator,
		Origin:        ctx.part,
		Value:         val,
		Error:         ctx.tk.Error,
		Position: &token.Position{
			Line:        b.currentLine,
			Column:      col,
			Offset:      valueOffset,
			IndentNum:   b.currentIndentNum,
			IndentLevel: b.currentIndentLevel,
		},
	}

	// For block scalars, preserve the original token's Position.
	//
	// The go-yaml lexer behavior varies:
	//   - With following content: Position points to first content line (Column=0)
	//   - Standalone: Position points to last content line (Column>0)
	//
	// We determine which case and put the original Position on the appropriate
	// part.
	if ctx.isBlockScalarContent && ctx.isMultiPart && ctx.tk.Position != nil {
		isFirstLinePosition := ctx.tk.Position.Column == 0
		if (wasFirstContentPart && isFirstLinePosition) || (isLastContentPart && !isFirstLinePosition) {
			newTk.Position = clonePosition(ctx.tk.Position)
		}
	}

	// For tokens with a leading blank line (Origin starts with "\n"), preserve the
	// original Position for the first content part.
	//
	// The lexer's Position reflects the content line, not the blank line, so we
	// should use it to ensure round-trip fidelity.
	//
	// Also update our tracking to match, so subsequent tokens get correct values.
	hasLeadingBlankLine := ctx.isMultiPart && len(ctx.parts) > 1 && isPureNewline(ctx.parts[0])
	if hasLeadingBlankLine && wasFirstContentPart && ctx.tk.Position != nil {
		newTk.Position = clonePosition(ctx.tk.Position)
		// Sync our tracking with the original Position to fix subsequent tokens.
		b.currentIndentLevel = ctx.tk.Position.IndentLevel
	}

	b.currentLineSegments = b.currentLineSegments.Append(ctx.tk, newTk)

	b.currentOffset += len(ctx.part)

	// If this part ends with a newline, finish the current line.
	//
	// Only \n terminates lines (per YAML spec).
	//
	// CRLF (\r\n) may be split by go-yaml across tokens, but we wait for the \n to
	// create a new line.
	//
	// The \r is preserved in Content() and stripped during output.
	if strings.HasSuffix(ctx.part, "\n") {
		b.finishLine()
	}

	return true
}

// handleGap detects and handles line number gaps for simple tokens.
// Simple tokens have no internal newlines (or just a trailing newline).
//
// When a gap is detected (token is ahead of currentLine), it flushes the
// current line and syncs forward to the token's line.
func (b *linesBuilder) handleGap(tk *token.Token, origin string) {
	newlineCount := strings.Count(origin, "\n")
	isSimple := newlineCount == 0 || (newlineCount == 1 && strings.HasSuffix(origin, "\n"))
	if !isSimple {
		return
	}

	tkLine := b.currentLine
	if tk.Position != nil {
		tkLine = tk.Position.Line
	}

	// If there's a gap (simple token is ahead), flush and sync forward.
	// Never sync backwards - currentLine must be monotonically increasing.
	if tkLine > b.currentLine+1 && len(b.currentLineSegments) > 0 {
		b.lines = append(b.lines, Line{segments: b.currentLineSegments, number: b.currentLine})
		b.currentLineSegments = nil
	}

	if len(b.currentLineSegments) == 0 && tkLine > b.currentLine {
		b.currentLine = tkLine

		if tk.Position != nil {
			if tk.Position.Offset > 0 {
				b.currentOffset = tk.Position.Offset
			}

			b.currentIndentNum = tk.Position.IndentNum
			b.currentIndentLevel = tk.Position.IndentLevel
		}
	}
}

// partColumnAndValue calculates the column position and value for a content part.
//
// Parameters:
//   - tk: Original token with Type, Value, Position
//   - isFirst: Whether this is the first content part
//   - shouldHaveValue: Whether this part should receive the token's Value
//     (determined by caller: first part for plain/quoted, last part for
//     block scalars)
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

// isBlockScalarContent returns true if tk is a StringType that follows a block
// scalar header (Literal/Folded).
//
// Comments can appear between the header and content, so we traverse the Prev
// chain.
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

// isPureHorizontalWhitespace returns true if s contains only spaces and tabs.
func isPureHorizontalWhitespace(s string) bool {
	return s != "" && strings.TrimLeft(s, " \t") == ""
}

// splitOriginIntoParts splits a token's Origin at newline boundaries.
//
// Empty strings from SplitAfter are filtered, but an empty origin is preserved
// as a single empty part (semantically significant for empty block scalar
// content).
//
// Each part retains its trailing newline if present.
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
// actual content (not a pure newline).
//
// Used to identify which part should receive the Value for block scalars.
func findLastContentPartIndex(parts []string) int {
	for i := len(parts) - 1; i >= 0; i-- {
		if !isPureNewline(parts[i]) {
			return i
		}
	}

	return len(parts) - 1
}

// shouldPartReceiveValue determines if a token part should receive the Value
// field.
//
// Block scalars (literal/folded): Value goes to the last content part.
// Plain/quoted multiline: Value goes to the first content part.
func shouldPartReceiveValue(isBlockScalar, isFirstContentPart, isLastContentPart bool) bool {
	if isBlockScalar {
		return isLastContentPart
	}

	return isFirstContentPart
}

// clonePosition creates a deep copy of a [*token.Position].
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
