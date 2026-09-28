package segment

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/goccy/go-yaml/token"

	"go.jacobcolvin.com/niceyaml/internal/lineend"
	"go.jacobcolvin.com/niceyaml/tokens"
)

// Line holds the [Segments] of one source line together with the 1-indexed
// line number the lexer assigned to it.
//
// [Split] produces Line values. The line package builds its own Line type
// from each one and keeps the decoration that rendering attaches to a line
// on its View.
type Line struct {
	Segments Segments
	// The 1-indexed line number used for display.
	Number int
}

// Split cuts tks into one [Line] per source line, splitting multiline
// tokens into per-line parts. It skips nil tokens in the stream, and
// returns nil when tks holds no other token.
//
// The tokens are ones [tokens.Tokenize] returns or the clones
// [tokens.ResetPositions] makes of them, whose Line, Column, and Offset
// name the rune where the token's text starts. A stream built by hand
// splits as well, and Split numbers its lines from the positions it
// carries.
//
// Every part carries a Position of its own, 1-indexed like the lexer's:
//   - Line is the line the part sits on.
//   - The first part of a token that holds text keeps the token's Column
//     and Offset, so it names the rune where the text starts. Every other
//     part names the rune where the part starts, so the later lines of a
//     block scalar start at column 1, and a line ending that closes a
//     line sits just past the parts before it.
//   - Offset counts runes from the start of the document, not bytes, and
//     grows from part to part.
//   - IndentNum and IndentLevel describe the line the part sits on, and
//     the parts of a line share them. The first part of the line that
//     holds any rune besides a line ending sets them, so the empty
//     content of a block scalar never does. That part takes them from
//     its token when it holds the token's first text, unless the token
//     holds block scalar content, and from the spaces it opens with
//     otherwise. A token whose text starts partway through a line,
//     such as a comment after a multiline quoted scalar, can carry
//     indentation its part does not.
//
// The Value of a block scalar goes to its last content part, and the
// Value of every other token to its first text part. A part that holds
// horizontal whitespace alone, such as the indentation the lexer bundles
// into the token before it, becomes a SpaceType part unless it belongs to
// a block scalar.
//
// The lexer keeps a CRLF in Origin and normalizes it to "\n" in Value, and
// a bare "\r" ends a line for Split as it does for the lexer. The lexer
// absorbs blank lines into the previous token's Origin, cuts a CRLF between
// a comment and the next token, and repeats the line ending after a tag at
// the start of the next token. Split puts each such line ending on the
// line it closes.
func Split(tks token.Tokens) []Line {
	b := newBuilder(tks)
	if b == nil {
		return nil
	}

	ahead := breaksAhead(tks)

	for i, tk := range tks {
		if tk == nil {
			continue
		}

		b.AddToken(tk, ahead[i])
	}

	return b.Build()
}

// breaksAhead returns, for each token of tks, the number of line breaks
// the tokens after it hold before the next rune of text. That counts every
// line break of a later token whose Origin holds whitespace alone, and the
// line breaks in the whitespace that opens the next Origin that holds text.
// It skips nil tokens.
func breaksAhead(tks token.Tokens) []int {
	ahead := make([]int, len(tks))
	n := 0

	for i := len(tks) - 1; i >= 0; i-- {
		ahead[i] = n

		tk := tks[i]
		if tk == nil {
			continue
		}

		text := strings.TrimLeft(tk.Origin, " \t\r\n")
		lead := lineend.CountBreaks(tk.Origin[:len(tk.Origin)-len(text)])

		if text == "" {
			n += lead
		} else {
			n = lead
		}
	}

	return ahead
}

// builder constructs [Line] values from [token.Tokens].
// Create instances with [newBuilder].
type builder struct {
	// Result accumulation.
	lastPart *token.Token // Most recent part on the current line, for linking.

	// Token tracking.
	prevLineEnding string // Line ending that closed the previous token's Origin, or "".

	lines               []Line
	currentLineSegments Segments
	currentLine         int        // Current line number being built.
	prevType            token.Type // Type of the last token added other than a comment.

	// Position tracking.
	currentOffset      int  // Cumulative rune offset (1-indexed like lexer).
	currentColumn      int  // Column just past the parts on the current line.
	prevLineEndColumn  int  // Column just past the parts of the line finished last.
	prevLineEndOffset  int  // Offset of the rune at prevLineEndColumn.
	currentIndentNum   int  // Leading spaces on current line.
	prevLineIndentNum  int  // IndentNum from previous line.
	currentIndentLevel int  // Nesting depth level.
	lineIndentSet      bool // Whether a part of the current line set its indentation.
}

// newBuilder creates a new [*builder] initialized from the token that
// anchors the start of tks. It skips nil tokens, and returns nil when tks
// holds no other token.
func newBuilder(tks token.Tokens) *builder {
	anchor, lead := startAnchor(tks)
	if anchor == nil {
		return nil
	}

	b := &builder{currentColumn: 1, currentLine: 1, currentOffset: 1}
	if anchor.Position == nil {
		return b
	}

	// The position names the line the anchor's text sits on, so the stream
	// starts as many lines before Position.Line as lead holds line breaks.
	b.currentLine = anchor.Position.Line

	before := lineend.CountBreaks(lead)
	if before > 0 && b.currentLine > before {
		b.currentLine -= before
	}

	// Initialize position tracking from the anchor (1-indexed like lexer).
	if anchor.Position.Offset > 0 {
		b.currentOffset = max(1, anchor.Position.Offset-utf8.RuneCountInString(lead))
		b.currentIndentNum = anchor.Position.IndentNum
		b.currentIndentLevel = anchor.Position.IndentLevel
	}

	return b
}

// startAnchor returns the token whose position anchors the start of tks,
// together with the text of tks that comes before the rune that position
// names. It skips nil tokens, and returns a nil token when tks holds no
// other token.
//
// The anchor is the first token whose Origin holds a rune besides spaces,
// tabs, and line breaks, as [tokens.ResetPositions] picks it. A position
// points past the whitespace and line breaks that open the Origin, so they
// come before the rune it names. A token whose Origin holds them alone
// shares the position of the text after it, so its whole Origin comes
// before that rune too. When no token holds such a rune, the first token
// anchors the start.
func startAnchor(tks token.Tokens) (*token.Token, string) {
	var (
		first *token.Token
		lead  strings.Builder
	)

	for _, tk := range tks {
		if tk == nil {
			continue
		}

		if first == nil {
			first = tk
		}

		trimmed := strings.TrimLeft(tk.Origin, " \t\r\n")
		if trimmed != "" {
			lead.WriteString(tk.Origin[:len(tk.Origin)-len(trimmed)])

			return tk, lead.String()
		}

		lead.WriteString(tk.Origin)
	}

	if first == nil {
		return nil, ""
	}

	return first, first.Origin
}

// AddToken adds a single token, splitting it into per-line parts. The
// ahead argument is the number of line breaks the tokens after tk hold
// before the next rune of text, as [breaksAhead] counts them.
func (b *builder) AddToken(tk *token.Token, ahead int) {
	// Detect if this token is block scalar content by checking if it follows a
	// Literal/Folded header in the stream.
	isBlockScalarContent := isBlockScalarContent(tk, b.prevType)
	if tk.Type != token.CommentType {
		b.prevType = tk.Type
	}

	origin := tk.Origin

	// Split token at line ending boundaries.
	parts := splitOriginIntoParts(origin)

	// For simple tokens, check for line number gaps and sync forward if needed.
	b.handleGap(tk, parts, isBlockScalarContent)

	ctx := &partContext{
		tk:                   tk,
		breaksAhead:          ahead,
		leadingNewlines:      countLeadingNewlineParts(parts),
		isBlockScalarContent: isBlockScalarContent,
		lastContentPartIdx:   findLastContentPartIndex(parts),
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
	//
	// The tokenizer places a token that holds no text and sits past the
	// end of the source, such as the empty content of a block scalar
	// that keeps its trailing lines, on the last line the source has.
	// Such a token opens no line of its own here, since its line closed
	// already, so it joins that line rather than adding one the file
	// does not have.
	if len(b.currentLineSegments) > 0 {
		if b.currentLineIsBehind() && len(b.lines) > 0 {
			b.joinCurrentLineToPrevious()
		} else {
			b.lines = append(b.lines, Line{
				Segments: b.currentLineSegments,
				Number:   b.currentLine,
			})
		}
	}

	return b.lines
}

// currentLineIsBehind reports whether every segment of the current line
// holds no rune of the source and comes from a token the tokenizer placed
// on a line before the current one.
func (b *builder) currentLineIsBehind() bool {
	for _, seg := range b.currentLineSegments {
		src := seg.Source()
		if seg.Part().Origin != "" || src == nil || src.Position == nil || src.Position.Line >= b.currentLine {
			return false
		}
	}

	return true
}

// joinCurrentLineToPrevious moves the segments of the current line onto
// the line finished last, placing each where that line ended and giving
// each the indentation of that line.
//
// No segment it moves holds a rune, so none of them set the indentation
// of the current line, and currentIndentLevel still holds the level of
// the line finished last.
func (b *builder) joinCurrentLineToPrevious() {
	lastLine := &b.lines[len(b.lines)-1]

	for _, seg := range b.currentLineSegments {
		seg.Part().Position.Line = lastLine.Number
		seg.Part().Position.Column = max(b.prevLineEndColumn, 1)
		seg.Part().Position.Offset = max(b.prevLineEndOffset, 1)
		seg.Part().Position.IndentNum = b.prevLineIndentNum
		seg.Part().Position.IndentLevel = b.currentIndentLevel
	}

	if n := len(lastLine.Segments); n > 0 {
		linkParts(lastLine.Segments[n-1].Part(), b.currentLineSegments[0].Part())
	}

	lastLine.Segments = append(lastLine.Segments, b.currentLineSegments...)
	b.currentLineSegments = nil
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
	b.prevLineEndColumn = b.currentColumn

	// The column stops at the line ending that closed the line, while
	// currentOffset counts past it, so the offset steps back over the
	// ending to reach the rune the column names. Line endings are ASCII,
	// so byte and rune counts agree.
	b.prevLineEndOffset = b.currentOffset
	if n := len(b.currentLineSegments); n > 0 {
		b.prevLineEndOffset -= len(lineEnding(b.currentLineSegments[n-1].Part().Origin))
	}

	b.currentLineSegments = nil
	b.currentIndentNum = 0 // The next line's first content sets it again.
	b.lineIndentSet = false
	b.currentColumn = 1
	b.currentLine++
}

// partContext contains information needed to process a single origin part.
type partContext struct {
	tk                   *token.Token
	part                 string
	partIndex            int
	breaksAhead          int // Line breaks the later tokens hold before the next text.
	leadingNewlines      int // Number of pure-newline parts at the start of parts.
	lastContentPartIdx   int
	isBlockScalarContent bool
	textPlaced           bool // Whether an earlier part held the token's first text.
}

// processPart processes a single origin part within a token.
func (b *builder) processPart(ctx *partContext) {
	partIsPureNewline := isPureNewline(ctx.part)

	// A leading newline part can belong to the line the previous token closed.
	// Instead of skipping it entirely (which would make Origin non-invertible),
	// we append it to the previous line, so the Origin keeps the newline
	// without an extra line advance.
	if b.continuesPreviousLine(ctx) && len(b.lines) > 0 {
		b.appendToPreviousLine(ctx)

		return
	}

	// The token's text starts in the first part that holds any, and that
	// part carries the token's own position. Parts before it hold line
	// endings or whitespace alone.
	hasText := strings.TrimLeft(tokens.TrimLineEnding(ctx.part), " \t") != ""
	isFirstText := hasText && !ctx.textPlaced

	// The first text part skips the whitespace it opens with to reach the
	// rune where the text starts. No other part skips any, so its lead
	// stays zero.
	lead := 0
	if isFirstText {
		lead = len(ctx.part) - len(strings.TrimLeft(ctx.part, " \t"))
	}

	// The first part of a line that holds any rune besides a line ending
	// sets the indentation, and every part of the line shares it. The
	// empty content of a block scalar can open a line, so the parts
	// already on the line take the indentation when a later part sets it.
	//
	// Use the token's Position if available (more accurate than counting
	// spaces in Origin, since some tokens like MappingKey don't include
	// leading spaces). The lexer gives block scalar content the
	// indentation of a later line, so every part of it counts the spaces
	// it opens with instead.
	if !b.lineIndentSet && ctx.part != "" && !partIsPureNewline {
		if isFirstText && ctx.tk.Position != nil && !ctx.isBlockScalarContent {
			b.currentIndentNum = ctx.tk.Position.IndentNum
			b.currentIndentLevel = ctx.tk.Position.IndentLevel
		} else {
			b.currentIndentNum = countLeadingWhitespace(ctx.part)
			b.currentIndentLevel = updateIndentLevel(b.prevLineIndentNum, b.currentIndentNum, b.currentIndentLevel)
		}

		b.lineIndentSet = true

		for _, seg := range b.currentLineSegments {
			seg.Part().Position.IndentNum = b.currentIndentNum
			seg.Part().Position.IndentLevel = b.currentIndentLevel
		}
	}

	// The first text part names the rune where the text starts, as the
	// token does, and every other part names the rune where it starts.
	col, offset := b.currentColumn, b.currentOffset
	if isFirstText {
		col, offset = textPosition(ctx.tk, col+lead, offset+lead)
	}

	// Determine which part should receive the token's Value:
	// Block scalar: Value goes to last content part (lexer behavior).
	// Plain/quoted multiline: Value goes to first text part (lexer behavior).
	isLastContentPart := ctx.partIndex == ctx.lastContentPartIdx

	val := ""
	if shouldPartReceiveValue(ctx.isBlockScalarContent, isFirstText, isLastContentPart) {
		val = ctx.tk.Value
	}

	// Use SpaceType for pure horizontal whitespace parts.
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
			Offset:      offset,
			IndentNum:   b.currentIndentNum,
			IndentLevel: b.currentIndentLevel,
		},
	}

	linkParts(b.lastPart, newTk)

	b.lastPart = newTk

	seg := New(ctx.tk, newTk)
	b.currentLineSegments = append(b.currentLineSegments, seg)

	// Count past the part. The first text part counts from the position it
	// took, so the parts after it follow the source where the Origin
	// opened with whitespace the lexer dropped.
	b.currentColumn = col - lead + seg.Width()
	b.currentOffset = offset - lead + utf8.RuneCountInString(ctx.part)

	if isFirstText {
		ctx.textPlaced = true
	}

	// If this part ends with a line ending, finish the current line.
	//
	// Each part but the last ends a line, because splitOriginIntoParts cuts
	// after "\n" and after a bare "\r". The last part ends one when the
	// Origin did. The lexer advances Position.Line on a bare "\r" as well,
	// and this mirrors it. The part's Origin keeps the ending, so the parts
	// rebuild the token's Origin, and Content() strips it.
	if lineEnding(ctx.part) != "" {
		b.finishLine()
	}
}

// textPosition returns the column and offset of the rune where the text of
// tk starts, as its Position carries them. For each one the Position leaves
// at zero, or for both when tk has none, it returns col or offset, the
// count the builder reached.
func textPosition(tk *token.Token, col, offset int) (int, int) {
	if tk.Position == nil {
		return col, offset
	}

	if tk.Position.Column > 0 {
		col = tk.Position.Column
	}

	if tk.Position.Offset > 0 {
		offset = tk.Position.Offset
	}

	return col, offset
}

// appendToPreviousLine attaches the pure-newline part that opens the token
// to the line finished last, which the part closes. The part sits just
// past the parts of that line, and its Offset names the rune it starts
// with, which the previous token may already have counted. Only the runes
// it adds beyond that line ending advance the offset.
func (b *builder) appendToPreviousLine(ctx *partContext) {
	lastLine := &b.lines[len(b.lines)-1]
	newTk := &token.Token{
		Type:          ctx.tk.Type,
		CharacterType: ctx.tk.CharacterType,
		Indicator:     ctx.tk.Indicator,
		Origin:        ctx.part,
		Position: &token.Position{
			Line:        b.currentLine - 1, // Goes on previous line.
			Column:      max(b.prevLineEndColumn, 1),
			Offset:      b.currentOffset - lineEndingOverlap(b.prevLineEnding, ctx.part),
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
//     the full "\r\n". Position.Line names the line the token's text
//     starts on, and each leading pure-newline part advances one line from
//     currentLine, so more leading newlines than lines to advance means the
//     first one is the repeat. Comparing counts rather than checking
//     currentLine == Position.Line also catches a repeat that real blank
//     lines follow.
//
// A token whose Origin holds whitespace alone, such as the invalid token
// the lexer makes of a line that holds a tab alone, shares the Position of
// the next text. Its Position.Line then also counts the line breaks that
// the tokens between it and that text hold, so the count leaves those out.
//
// Block scalar content never reaches the count, because handleGap leaves
// currentLine on the content line for it. No line is left to advance, so the
// count would read the lone newline of an empty scalar as a repeat and drop
// the blank line it stands for.
func (b *builder) continuesPreviousLine(ctx *partContext) bool {
	if ctx.partIndex != 0 || !isPureNewline(ctx.part) || b.prevLineEnding == "" {
		return false
	}

	if b.prevLineEnding == "\r" && ctx.part == "\n" {
		return true
	}

	if ctx.isBlockScalarContent || ctx.tk.Position == nil {
		return false
	}

	advance := ctx.tk.Position.Line - b.currentLine
	if strings.Trim(ctx.tk.Origin, " \t\r\n") == "" {
		advance -= ctx.breaksAhead
	}

	return ctx.leadingNewlines > advance
}

// handleGap detects and handles line number gaps for simple tokens.
// Simple tokens split into a single part. They have no internal line
// endings, at most a trailing one.
//
// When it detects a gap (the token is ahead of currentLine), it flushes the
// current line and syncs forward to the token's line.
//
// A token without text is no evidence of a gap. It sits where the next
// text starts, which can be lines below the line its Origin closes, so
// syncing to it would skip the blank line it stands for. Block scalar
// content is no evidence either, since the empty content of a scalar has
// no text and the content of one has its line ending in the Origin.
func (b *builder) handleGap(tk *token.Token, parts []string, isBlockScalarContent bool) {
	if len(parts) != 1 || isBlockScalarContent || tk.Position == nil {
		return
	}

	if strings.Trim(tk.Origin, " \t\r\n") == "" {
		return
	}

	tkLine := tk.Position.Line

	// If there's a gap (simple token is ahead), flush and sync forward.
	// Never sync backwards, since currentLine must be monotonically increasing.
	//
	// Closing the line through finishLine also clears lastPart, so the next
	// line's first part does not link back across the boundary.
	if tkLine > b.currentLine && len(b.currentLineSegments) > 0 {
		b.finishLine()
	}

	if len(b.currentLineSegments) == 0 && tkLine > b.currentLine {
		b.currentLine = tkLine
	}
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

// isBlockScalarContent reports whether tk holds the content that follows a
// block scalar header (Literal/Folded). The lexer emits that content as a
// StringType token, or as an InvalidType token when a header with an
// indentation indicator, such as "|2", finds only blank lines below it.
//
// Comments can appear between the header and content, so prevType is the
// type of the last token before tk in the stream that is not a comment. It
// comes from the stream order rather than the Prev links, which a stream
// built by hand may lack.
func isBlockScalarContent(tk *token.Token, prevType token.Type) bool {
	if tk.Type != token.StringType && tk.Type != token.InvalidType {
		return false
	}

	return prevType == token.LiteralType || prevType == token.FoldedType
}

// isPureNewline reports whether s is exactly one line ending, as
// [tokens.TrimLineEnding] recognizes them.
func isPureNewline(s string) bool {
	return s != "" && tokens.TrimLineEnding(s) == ""
}

// lineEnding returns the line ending that closes s, as
// [tokens.TrimLineEnding] recognizes them, or "" when s ends with none.
func lineEnding(s string) string {
	return s[len(tokens.TrimLineEnding(s)):]
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

// isPureHorizontalWhitespace reports whether s contains only spaces and tabs.
func isPureHorizontalWhitespace(s string) bool {
	return s != "" && strings.TrimLeft(s, " \t") == ""
}

// splitOriginIntoParts splits a token's Origin after each line ending: "\n",
// "\r\n", or a bare "\r", the three the lexer advances Position.Line on.
//
// An empty origin becomes a single empty part (semantically significant
// for empty block scalar content).
//
// Each part retains its trailing line ending if present.
func splitOriginIntoParts(origin string) []string {
	if origin == "" {
		return []string{""}
	}

	return slices.Collect(lineend.Lines(origin))
}

// findLastContentPartIndex returns the index of the last part that contains
// actual content (not a pure newline).
//
// The lexer bundles the next line's indentation into a block scalar's
// Origin when a comment or a key follows the scalar, so a final part that
// holds only horizontal whitespace and no line ending is that indentation,
// not content. Falls back to the last part when nothing else qualifies.
//
// AddToken uses this to find the part that receives the Value for block
// scalars.
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

// shouldPartReceiveValue reports whether a token part should receive the Value
// field.
//
// Block scalars (literal/folded): Value goes to the last content part.
// Plain/quoted multiline: Value goes to the first text part.
func shouldPartReceiveValue(isBlockScalar, isFirstTextPart, isLastContentPart bool) bool {
	if isBlockScalar {
		return isLastContentPart
	}

	return isFirstTextPart
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
