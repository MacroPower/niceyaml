package segment

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/goccy/go-yaml/token"
)

// Line holds the [Segments] of one source line together with the 1-indexed
// line number the lexer assigned to it.
//
// [Split] produces Line values; the line package wraps them in its own Line
// type, which adds rendering metadata.
type Line struct {
	Segments Segments
	// The 1-indexed line number used for display purposes. This may differ
	// from the first token's Position.Line for block scalars.
	Number int
}

// Split cuts tks into one [Line] per source line, splitting multiline
// tokens into per-line parts. Returns nil when tks is empty.
//
// The parts closely match go-yaml lexer behavior:
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
//   - A bare CR (\r) ends a line, as it advances the lexer's Position.Line
//   - Blank lines are absorbed into the previous token's Origin
//   - Comments include the trailing newline in Origin but not in Value
func Split(tks token.Tokens) []Line {
	b := newBuilder(tks)
	if b == nil {
		return nil
	}

	for _, tk := range tks {
		b.AddToken(tk)
	}

	return b.Build()
}

// builder constructs [Line] values from [token.Tokens].
// It encapsulates all state needed during the line-building process.
// Create instances with [newBuilder].
type builder struct {
	// Result accumulation.
	lastPart *token.Token // Most recent part on the current line, for linking.

	// Token tracking.
	prevLineEnding string // Line ending that closed the previous token's Origin, or "".

	lines               []Line
	currentLineSegments Segments
	currentLine         int // Current line number being built.
	built               bool

	// Position tracking.
	currentOffset      int // Cumulative rune offset (1-indexed like lexer).
	currentIndentNum   int // Leading spaces on current line.
	prevLineIndentNum  int // IndentNum from previous line.
	currentIndentLevel int // Nesting depth level.
}

// newBuilder creates a new [*builder] initialized from the first token.
// Returns nil if tks is empty.
func newBuilder(tks token.Tokens) *builder {
	if len(tks) == 0 {
		return nil
	}

	b := &builder{}

	// Initialize currentLine from the first token's position.
	//
	// If the first token's Origin has leading newlines, we need to start earlier
	// because Position.Line points to the content, not the Origin start.
	if tks[0].Position != nil {
		b.currentLine = tks[0].Position.Line
		// Count leading newlines in first token's Origin and adjust.
		leadingNewlines := countLeadingNewlineParts(splitOriginIntoParts(tks[0].Origin))
		if leadingNewlines > 0 && b.currentLine > leadingNewlines {
			b.currentLine -= leadingNewlines
		}
	} else {
		b.currentLine = 1
	}

	// Initialize position tracking from first token (1-indexed like lexer).
	if tks[0].Position != nil && tks[0].Position.Offset > 0 {
		b.currentOffset = originOffset(tks[0])
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
func (b *builder) AddToken(tk *token.Token) {
	if b.built {
		panic("segment: cannot add token after Build() has been called")
	}

	// Detect if this token is block scalar content by checking if it follows a
	// Literal/Folded header in the token chain.
	isBlockScalarContent := isBlockScalarContent(tk)

	origin := tk.Origin

	// Split token at line ending boundaries, filtering empty parts upfront.
	parts := splitOriginIntoParts(origin)

	// For simple tokens, check for line number gaps and sync forward if needed.
	b.handleGap(tk, parts, isBlockScalarContent)

	// Multi-part means the token's Origin was split into multiple parts.
	isMultiPart := len(parts) > 1

	// Find the last non-pure-newline part index for Value assignment.
	// Pure newlines (like trailing "\n" in keep blocks) shouldn't get Value.
	lastContentPartIdx := findLastContentPartIndex(parts)

	isFirstContentPart := true

	ctx := &partContext{
		tk:                   tk,
		parts:                parts,
		leadingNewlines:      countLeadingNewlineParts(parts),
		isBlockScalarContent: isBlockScalarContent,
		isMultiPart:          isMultiPart,
		lastContentPartIdx:   lastContentPartIdx,
		isFirstContentPart:   &isFirstContentPart,
	}

	for i, part := range parts {
		ctx.part = part
		ctx.partIndex = i

		b.processPart(ctx)
	}

	// Remember how this token's Origin ended for duplicate detection.
	b.prevLineEnding = lineEnding(origin)
}

// Build finalizes and returns the constructed [Line] values.
func (b *builder) Build() []Line {
	// Handle the last line which may not end with a newline.
	if len(b.currentLineSegments) > 0 {
		b.lines = append(b.lines, Line{
			Segments: b.currentLineSegments,
			Number:   b.currentLine,
		})
	}

	// Mark as built to prevent reuse.
	b.built = true

	return b.lines
}

// finishLine completes the current line and prepares for the next one.
func (b *builder) finishLine() {
	b.lines = append(b.lines, Line{
		Segments: b.currentLineSegments,
		Number:   b.currentLine,
	})

	b.lastPart = nil

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
	leadingNewlines      int // Number of pure-newline parts at the start of parts.
	lastContentPartIdx   int
	isBlockScalarContent bool
	isMultiPart          bool
}

// processPart processes a single origin part within a token.
// Returns false if this was a duplicate newline that was handled specially.
//
//nolint:nestif // Complex part processing requires nested conditions.
func (b *builder) processPart(ctx *partContext) bool {
	partIsPureNewline := isPureNewline(ctx.part)

	// A leading newline part can belong to the line the previous token closed.
	// Instead of skipping it entirely (which would make Origin non-invertible),
	// we append it to the previous line so the newline is preserved in the
	// Origin but doesn't cause an extra line advance.
	if b.continuesPreviousLine(ctx) && len(b.lines) > 0 {
		// Create a segment for the newline and attach to previous line.
		lastLine := &b.lines[len(b.lines)-1]
		newTk := &token.Token{
			Type:          ctx.tk.Type,
			CharacterType: ctx.tk.CharacterType,
			Indicator:     ctx.tk.Indicator,
			Origin:        ctx.part,
			Position: &token.Position{
				Line:        b.currentLine - 1, // Goes on previous line.
				Column:      newlineColumn(lastLine.Segments),
				Offset:      b.currentOffset,
				IndentNum:   b.prevLineIndentNum,
				IndentLevel: b.currentIndentLevel,
			},
		}
		if n := len(lastLine.Segments); n > 0 {
			linkParts(lastLine.Segments[n-1].Part(), newTk)
		}

		lastLine.Segments = append(lastLine.Segments, New(ctx.tk, newTk))

		// The previous token's Origin already counted the runes it shares
		// with this part, so only the rest advances currentOffset. Line
		// endings are ASCII, so byte and rune counts agree.
		b.currentOffset += len(ctx.part) - lineEndingOverlap(b.prevLineEnding, ctx.part)

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
		col = newlineColumn(b.currentLineSegments)
	} else {
		col, val = partColumnAndValue(ctx.tk, *ctx.isFirstContentPart, shouldHaveValue)
		*ctx.isFirstContentPart = false
	}

	// Calculate offset where Value starts within the document.
	// Use original Offset when:
	//   - Single-part token (not split), or
	//   - A plain or quoted multiline part that receives the token's Value.
	//
	// A block scalar part that keeps the original Position takes its Offset
	// from that Position below. Every other part counts runes from the
	// document start, so offsets within one token stay increasing.
	useOriginalOffset := !ctx.isMultiPart || (shouldHaveValue && val != "" && !ctx.isBlockScalarContent)
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
	// should use it to ensure round-trip fidelity. The part keeps the line it
	// sits on, though: the lexer counts a CRLF it cut between a comment and
	// the next token as two line breaks, and every part on a line reports
	// that line's number.
	//
	// Also update our tracking to match, so subsequent tokens get correct values.
	hasLeadingBlankLine := ctx.isMultiPart && len(ctx.parts) > 1 && isPureNewline(ctx.parts[0])
	if hasLeadingBlankLine && wasFirstContentPart && ctx.tk.Position != nil {
		newTk.Position = clonePosition(ctx.tk.Position)
		newTk.Position.Line = b.currentLine
		// Sync our tracking with the original Position to fix subsequent tokens.
		b.currentIndentLevel = ctx.tk.Position.IndentLevel
	}

	linkParts(b.lastPart, newTk)

	b.lastPart = newTk

	b.currentLineSegments = append(b.currentLineSegments, New(ctx.tk, newTk))

	b.currentOffset += utf8.RuneCountInString(ctx.part)

	// If this part ends with a line ending, finish the current line.
	//
	// The parts are cut after "\n" and after a bare "\r", so every part but
	// the last ends a line, and the last does when the Origin did.
	// The lexer advances Position.Line on a bare "\r" as well, and this
	// mirrors it. The ending stays in the part's Origin so the token can be
	// rebuilt, and Content() strips it.
	if lineEnding(ctx.part) != "" {
		b.finishLine()
	}

	return true
}

// continuesPreviousLine reports whether the part, a pure newline that opens
// its token, belongs to the line the previous token closed rather than
// starting a line of its own. The go-yaml lexer produces this in two ways:
//
//   - It cuts a CRLF between tokens, closing a comment with the "\r" and
//     opening the next token with the "\n". A "\r" directly followed by "\n"
//     is one line break in every convention, so the "\n" joins the "\r".
//   - It repeats a line ending at both the end of one token and the start
//     of the next. After a tag it repeats "\n" as "\n", and in a CRLF
//     document it closes the tag with "\r" and opens the next token with
//     the full "\r\n". Position.Line names the line the token's content
//     starts on, and each leading pure-newline part advances one line from
//     currentLine, so more leading newlines than lines to advance means the
//     first one is the repeat. Comparing counts rather than checking
//     currentLine == Position.Line also catches a repeat that real blank
//     lines follow.
func (b *builder) continuesPreviousLine(ctx *partContext) bool {
	if ctx.partIndex != 0 || !isPureNewline(ctx.part) || b.prevLineEnding == "" {
		return false
	}

	if b.prevLineEnding == "\r" && ctx.part == "\n" {
		return true
	}

	return ctx.tk.Position != nil && ctx.leadingNewlines > ctx.tk.Position.Line-b.currentLine
}

// handleGap detects and handles line number gaps for simple tokens.
// Simple tokens split into a single part: they have no internal line
// endings, at most a trailing one.
//
// When a gap is detected (token is ahead of currentLine), it flushes the
// current line and syncs forward to the token's line.
//
// Block scalar content is never evidence of a gap. The lexer sets its
// Position.Line to the header's line or to the last content line, not to
// the line its Origin starts on, so syncing to it would skip a line.
// The part that owns the original Position carries it forward through
// processPart.
func (b *builder) handleGap(tk *token.Token, parts []string, isBlockScalarContent bool) {
	if len(parts) != 1 || isBlockScalarContent {
		return
	}

	tkLine := b.currentLine
	if tk.Position != nil {
		tkLine = tk.Position.Line
	}

	// If there's a gap (simple token is ahead), flush and sync forward.
	// Never sync backwards - currentLine must be monotonically increasing.
	//
	// Closing the line through finishLine also clears lastPart, so the next
	// line's first part does not link back across the boundary.
	if tkLine > b.currentLine+1 && len(b.currentLineSegments) > 0 {
		b.finishLine()
	}

	if len(b.currentLineSegments) == 0 && tkLine > b.currentLine {
		b.currentLine = tkLine

		if tk.Position != nil {
			if tk.Position.Offset > 0 {
				b.currentOffset = originOffset(tk)
			}

			b.currentIndentNum = tk.Position.IndentNum
			b.currentIndentLevel = tk.Position.IndentLevel
		}
	}
}

// originOffset returns the 1-indexed document rune offset where tk.Origin
// starts.
//
// Position.Offset points at the value, past the whitespace and line breaks
// that open the Origin. The builder counts the whole Origin, so it must
// start counting where the Origin does. Those opening runes are ASCII, so
// their byte count is their rune count.
func originOffset(tk *token.Token) int {
	lead := len(tk.Origin) - len(strings.TrimLeft(tk.Origin, " \t\r\n"))

	return max(1, tk.Position.Offset-lead)
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

// newlineColumn returns the 1-indexed Column for a pure-newline part appended
// to segs: the column just past the existing parts, or 1 when the newline
// starts an otherwise empty line.
func newlineColumn(segs Segments) int {
	return max(segs.EndColumn(), 1)
}

// countLeadingNewlineParts returns the number of pure-newline parts at the
// start of parts.
func countLeadingNewlineParts(parts []string) int {
	count := 0

	for _, p := range parts {
		if !isPureNewline(p) {
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

// isPureNewline returns true if s is exactly a line ending (LF, CRLF, or a
// bare CR).
func isPureNewline(s string) bool {
	return s == "\n" || s == "\r\n" || s == "\r"
}

// lineEnding returns the line ending that closes s ("\r\n", "\n", or "\r"),
// or "" when s ends with none.
func lineEnding(s string) string {
	switch {
	case strings.HasSuffix(s, "\r\n"):
		return "\r\n"
	case strings.HasSuffix(s, "\n"):
		return "\n"
	case strings.HasSuffix(s, "\r"):
		return "\r"
	}

	return ""
}

// lineEndingOverlap returns the length of the longest suffix of prev that
// part starts with. A repeated "\n" after "\n" overlaps fully, while "\r\n"
// after a bare "\r" overlaps by one byte.
func lineEndingOverlap(prev, part string) int {
	for i := range len(prev) {
		if strings.HasPrefix(part, prev[i:]) {
			return len(prev) - i
		}
	}

	return 0
}

// isPureHorizontalWhitespace returns true if s contains only spaces and tabs.
func isPureHorizontalWhitespace(s string) bool {
	return s != "" && strings.TrimLeft(s, " \t") == ""
}

// splitOriginIntoParts splits a token's Origin after each line ending: "\n",
// "\r\n", or a bare "\r", the three the lexer advances Position.Line on.
//
// An empty origin is preserved as a single empty part (semantically
// significant for empty block scalar content).
//
// Each part retains its trailing line ending if present.
func splitOriginIntoParts(origin string) []string {
	// Handle empty origin: preserve as single empty part.
	// This is semantically significant for empty block scalar content.
	if origin == "" {
		return []string{""}
	}

	var parts []string

	start := 0

	for i := range len(origin) {
		switch origin[i] {
		case '\n':
		case '\r':
			if i+1 < len(origin) && origin[i+1] == '\n' {
				continue // The "\n" of a CRLF ends the part.
			}

		default:
			continue
		}

		parts = append(parts, origin[start:i+1])
		start = i + 1
	}

	if start < len(origin) {
		parts = append(parts, origin[start:])
	}

	return parts
}

// findLastContentPartIndex returns the index of the last part that contains
// actual content (not a pure newline).
//
// The lexer bundles the next line's indentation into a block scalar's
// Origin when a comment or a key follows the scalar, so a final part that
// holds only horizontal whitespace and no line ending is that indentation,
// not content. Falls back to the last part when nothing else qualifies.
//
// Used to identify which part should receive the Value for block scalars.
func findLastContentPartIndex(parts []string) int {
	last := len(parts) - 1

	for i, v := range slices.Backward(parts) {
		if isPureNewline(v) || (i == last && i > 0 && isPureHorizontalWhitespace(v)) {
			continue
		}

		return i
	}

	return last
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

// linkParts chains next after prev so that [token.Token.NextType] and
// [token.Token.PreviousType] see the neighboring parts on the same line. The
// chain stops at line boundaries, so the last part on a line has no Next. A nil
// prev leaves next unlinked.
func linkParts(prev, next *token.Token) {
	if prev == nil {
		return
	}

	prev.Next = next
	next.Prev = prev
}
