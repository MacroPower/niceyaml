package niceyaml

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"
)

const wrapOnCharacters = " /-"

// crlfNormalizer converts Windows (CRLF) and old Mac (CR) line endings to Unix (LF).
var crlfNormalizer = strings.NewReplacer("\r\n", "\n", "\r", "\n")

// Printer renders YAML tokens with syntax highlighting using [lipgloss.Style].
// It supports custom styles, line numbers, and styled token/range overlays
// for highlighting specific positions such as errors.
type Printer struct {
	styles                   StyleGetter
	style                    lipgloss.Style
	lineNumberStyle          lipgloss.Style
	linePrefix               string
	lineInsertedPrefix       string
	lineDeletedPrefix        string
	tokenStyles              []*tokenStyle
	rangeStyles              []*rangeStyle
	initialLineNumber        int
	width                    int
	hasCustomStyle           bool
	hasCustomLineNumberStyle bool
	lineNumbers              bool
}

// NewPrinter creates a new [Printer].
// By default it uses [DefaultStyles].
func NewPrinter(opts ...PrinterOption) *Printer {
	p := &Printer{
		styles:             DefaultStyles(),
		linePrefix:         " ",
		lineInsertedPrefix: "+",
		lineDeletedPrefix:  "-",
		initialLineNumber:  1,
	}

	for _, opt := range opts {
		opt(p)
	}

	if !p.hasCustomStyle {
		p.style = p.styles.GetStyle(StyleDefault).
			PaddingRight(1)
	}
	if !p.hasCustomLineNumberStyle {
		p.lineNumberStyle = p.styles.GetStyle(StyleDefault).
			Foreground(p.styles.GetStyle(StyleComment).GetForeground()).
			PaddingRight(1)
	}

	return p
}

// PrinterOption configures a [Printer].
type PrinterOption func(*Printer)

// WithStyle configures the printer with the given container style.
//
//nolint:gocritic // hugeParam: Copying.
func WithStyle(s lipgloss.Style) PrinterOption {
	return func(p *Printer) {
		p.style = s
		p.hasCustomStyle = true
	}
}

// WithStyles configures the printer with the given [StyleGetter].
func WithStyles(s StyleGetter) PrinterOption {
	return func(p *Printer) {
		p.styles = s
	}
}

// WithLineNumbers enables line number display.
func WithLineNumbers() PrinterOption {
	return func(p *Printer) {
		p.lineNumbers = true
	}
}

// WithLineNumberStyle sets the style for line numbers.
//
//nolint:gocritic // hugeParam: Copying.
func WithLineNumberStyle(s lipgloss.Style) PrinterOption {
	return func(p *Printer) {
		p.lineNumberStyle = s
		p.hasCustomLineNumberStyle = true
	}
}

// WithInitialLineNumber sets the starting line number (default: 1).
func WithInitialLineNumber(n int) PrinterOption {
	return func(p *Printer) {
		p.initialLineNumber = n
	}
}

// WithLinePrefix sets the prefix for normal/equal lines (default: " ").
func WithLinePrefix(prefix string) PrinterOption {
	return func(p *Printer) {
		p.linePrefix = prefix
	}
}

// WithLineInsertedPrefix sets the prefix for inserted lines in diffs (default: "+").
func WithLineInsertedPrefix(prefix string) PrinterOption {
	return func(p *Printer) {
		p.lineInsertedPrefix = prefix
	}
}

// WithLineDeletedPrefix sets the prefix for deleted lines in diffs (default: "-").
func WithLineDeletedPrefix(prefix string) PrinterOption {
	return func(p *Printer) {
		p.lineDeletedPrefix = prefix
	}
}

// SetWidth sets the width for word wrapping. A width of 0 disables wrapping.
func (p *Printer) SetWidth(width int) {
	p.width = width
}

// AddStyleToToken adds a style to apply to the token at the given position.
// Line and column are 1-indexed, matching [token.Position].
func (p *Printer) AddStyleToToken(s *lipgloss.Style, pos Position) {
	style := lipgloss.NewStyle()
	if s != nil {
		style = *s
	}

	p.tokenStyles = append(p.tokenStyles, &tokenStyle{
		style: style,
		pos:   pos,
	})
}

// AddStyleToRange adds a style to apply to the character range [r.Start, r.End).
// The range is half-open: Start is inclusive, End is exclusive.
// Line and column are 1-indexed, matching [token.Position].
// Overlapping range colors are blended; transforms are composed (overlay wraps base).
func (p *Printer) AddStyleToRange(s *lipgloss.Style, r PositionRange) {
	style := lipgloss.NewStyle()
	if s != nil {
		style = *s
	}

	p.rangeStyles = append(p.rangeStyles, &rangeStyle{
		style: style,
		rng:   r,
	})
}

// GetStyle retrieves the underlying [lipgloss.Style] for the given [Style].
func (p *Printer) GetStyle(s Style) *lipgloss.Style {
	return p.styles.GetStyle(s)
}

// ClearStyles removes all previously added styles.
func (p *Printer) ClearStyles() {
	p.tokenStyles = nil
	p.rangeStyles = nil
}

// PrintTokens prints [token.Tokens].
func (p *Printer) PrintTokens(tokens token.Tokens) string {
	content := p.getTokenString(tokens)
	content = p.applyLinePrefixes(content, p.initialLineNumber)

	// Apply word wrapping when line numbers are disabled.
	if !p.lineNumbers && p.width > 0 {
		content = p.wrapContent(content)
	}

	return p.style.Render(content)
}

// PrintFile prints [*ast.File].
func (p *Printer) PrintFile(f *ast.File) string {
	if len(f.Docs) == 0 {
		return ""
	}

	tk := findAnyTokenInFile(f)
	if tk == nil {
		return ""
	}

	tokens := extractTokensInRange(tk, -1, -1)

	return p.PrintTokens(tokens)
}

// PrintErrorToken prints the tokens around the error token with context.
// Returns the formatted string and the starting line number.
func (p *Printer) PrintErrorToken(tk *token.Token, lines int) (string, int) {
	curLine := tk.Position.Line
	curExtLine := curLine + countNewlines(strings.TrimLeft(tk.Origin, "\r\n"))
	if endsWithNewline(tk.Origin) {
		curExtLine--
	}

	minLine := max(curLine-lines, 1)
	maxLine := curExtLine + lines

	tokens := extractTokensInRange(tk, minLine, maxLine)
	content := p.getTokenString(tokens)

	startLine := p.initialLineNumber
	if startLine < 1 {
		startLine = minLine
	}

	content = p.applyLinePrefixes(content, startLine)

	return p.style.Render(content), minLine
}

// PrintTokenDiff generates a full-file diff between two token collections.
// It outputs the entire file with markers for inserted and deleted lines.
// Unchanged lines preserve syntax highlighting; inserted/deleted lines use diff styles.
func (p *Printer) PrintTokenDiff(before, after token.Tokens) string {
	ops := lcsLineDiff(
		buildLinesFromTokens(before),
		buildLinesFromTokens(after),
	)

	return p.style.Render(p.renderFullFileDiff(ops, after))
}

// PrintTokenDiffSummary generates a summary diff showing only changed lines with context.
// The context parameter specifies how many unchanged lines to show around each change.
// A context of 0 shows only the changed lines.
func (p *Printer) PrintTokenDiffSummary(before, after token.Tokens, context int) string {
	context = max(0, context)
	ops := lcsLineDiff(
		buildLinesFromTokens(before),
		buildLinesFromTokens(after),
	)

	if len(ops) == 0 {
		return ""
	}

	// Find which lines to include based on context.
	included := p.selectContextLines(ops, context)

	return p.style.Render(p.renderDiffSummary(ops, after, included))
}

// formatHunkHeader formats a unified diff hunk header like "@@ -1,3 +1,4 @@".
// Uses the same edge case handling as go-udiff (unified.go lines 218-235).
func (p *Printer) formatHunkHeader(h diffHunk) string {
	var b strings.Builder

	fmt.Fprint(&b, "@@")

	// Format "before" part.
	switch {
	case h.fromCount > 1:
		fmt.Fprintf(&b, " -%d,%d", h.fromLine, h.fromCount)
	case h.fromLine == 1 && h.fromCount == 0:
		// Match GNU diff -u behavior for adding to empty file.
		fmt.Fprint(&b, " -0,0")
	default:
		fmt.Fprintf(&b, " -%d", h.fromLine)
	}

	// Format "after" part.
	switch {
	case h.toCount > 1:
		fmt.Fprintf(&b, " +%d,%d", h.toLine, h.toCount)
	case h.toLine == 1 && h.toCount == 0:
		// Match GNU diff -u behavior for adding to empty file.
		fmt.Fprint(&b, " +0,0")
	default:
		fmt.Fprintf(&b, " +%d", h.toLine)
	}

	fmt.Fprint(&b, " @@")

	return b.String()
}

// selectContextLines returns a slice indicating which operations to include.
// Each change includes `context` lines before and after.
func (p *Printer) selectContextLines(ops []lineOp, context int) []bool {
	included := make([]bool, len(ops))
	n := len(ops)
	lastMarked := -1

	for i, op := range ops {
		if op.kind != diffEqual {
			start := max(0, i-context)
			end := min(n-1, i+context)

			// Mark the range [start, end], avoiding re-marking already included lines.
			// This should prevent O(n^2) issues in large files with many overlapping hunks.
			for j := max(start, lastMarked+1); j <= end; j++ {
				included[j] = true
			}

			lastMarked = max(lastMarked, end)
		}
	}

	return included
}

// renderDiffSummary renders only the included operations with hunk headers.
func (p *Printer) renderDiffSummary(ops []lineOp, afterTokens token.Tokens, included []bool) string {
	var sb strings.Builder

	if len(afterTokens) == 0 {
		return ""
	}

	// Build hunks from included operations.
	hunks := buildHunks(ops, included)

	// Get a token to use for range extraction.
	startToken := afterTokens[0]

	for hunkIdx, hunk := range hunks {
		// Add newline between hunks.
		if hunkIdx > 0 {
			sb.WriteByte('\n')
		}

		// Render hunk header.
		header := p.formatHunkHeader(hunk)
		if p.lineNumbers {
			sb.WriteString(p.lineNumberStyle.Render("    "))
		}

		sb.WriteString(p.styles.GetStyle(StyleComment).Render(header))
		sb.WriteByte('\n')

		// Extract and render only tokens needed for this hunk.
		minLine, maxLine := hunkAfterLineRange(ops, hunk)

		var (
			styledLines []string
			lineOffset  int
		)

		if minLine > 0 && maxLine > 0 {
			hunkTokens := extractTokensInRange(startToken, minLine, maxLine)
			styledHunk := p.getTokenString(hunkTokens)
			styledLines = strings.Split(styledHunk, "\n")
			lineOffset = minLine - 1
		}

		// Render operations within this hunk.
		for i := hunk.startIdx; i < hunk.endIdx; i++ {
			if i > hunk.startIdx {
				sb.WriteByte('\n')
			}

			op := ops[i]

			switch op.kind {
			case diffDelete:
				deleted := p.styles.GetStyle(StyleDiffDeleted)
				p.writeLine(&sb, p.lineDeletedPrefix, op.content, op.beforeLine, deleted, true)

			case diffInsert:
				inserted := p.styles.GetStyle(StyleDiffInserted)
				p.writeLine(&sb, p.lineInsertedPrefix, op.content, op.afterLine, inserted, false)

			default: // Equal.
				line := styledLines[op.afterLine-1-lineOffset]
				p.writeLine(&sb, p.linePrefix, line, op.afterLine, nil, false)
			}
		}
	}

	return sb.String()
}

// applyLinePrefixes adds line numbers or line prefixes to content.
func (p *Printer) applyLinePrefixes(content string, startLine int) string {
	if p.lineNumbers {
		return p.addLineNumbers(content, startLine)
	}

	return addLinePrefix(content, p.linePrefix)
}

// wrapContent wraps each line of content to the configured width.
func (p *Printer) wrapContent(content string) string {
	if p.width <= 0 {
		return content
	}

	lines := strings.Split(content, "\n")

	var sb strings.Builder

	for i, line := range lines {
		if i > 0 {
			sb.WriteByte('\n')
		}

		sb.WriteString(lipgloss.Wrap(line, p.width, wrapOnCharacters))
	}

	return sb.String()
}

// renderFullFileDiff renders line operations with appropriate prefixes and styling.
// Equal lines get syntax highlighting from afterTokens; diff lines use diff styles.
func (p *Printer) renderFullFileDiff(ops []lineOp, afterTokens token.Tokens) string {
	var sb strings.Builder

	// Pre-render the after tokens with syntax highlighting.
	styledAfter := p.getTokenString(afterTokens)
	styledLines := strings.Split(styledAfter, "\n")

	for i, op := range ops {
		if i > 0 {
			sb.WriteByte('\n')
		}

		switch op.kind {
		case diffDelete:
			deleted := p.styles.GetStyle(StyleDiffDeleted)
			p.writeLine(&sb, p.lineDeletedPrefix, op.content, op.beforeLine, deleted, true)

		case diffInsert:
			inserted := p.styles.GetStyle(StyleDiffInserted)
			p.writeLine(&sb, p.lineInsertedPrefix, op.content, op.afterLine, inserted, false)

		default: // Equal.
			// Use syntax-highlighted content from pre-rendered after tokens.
			// AfterLine is always >= 1 for Equal ops from [lcsLineDiff].
			line := styledLines[op.afterLine-1]
			p.writeLine(&sb, p.linePrefix, line, op.afterLine, nil, false)
		}
	}

	return sb.String()
}

// writeLine writes a line with optional word wrapping.
// If contentStyle is non-nil, applies it to prefix+content together (for diff lines).
// If contentStyle is nil, styles only the prefix and preserves content styling (for equal lines).
// If skipRangeStyles is true, range-based highlighting is not applied (used for deleted lines).
func (p *Printer) writeLine(
	sb *strings.Builder,
	prefix, content string,
	lineNum int,
	contentStyle *lipgloss.Style,
	skipRangeStyles bool,
) {
	renderLine := func(pfx, cnt string, col int) {
		if contentStyle != nil {
			// For diff lines: apply diff style to prefix.
			sb.WriteString(contentStyle.Render(pfx))
			if skipRangeStyles {
				// For deleted lines: skip range highlights (they apply to "after" tokens only).
				sb.WriteString(contentStyle.Render(cnt))
			} else {
				// For inserted lines: blend diff style with range highlights.
				sb.WriteString(p.styleLineWithRanges(cnt, lineNum, col, contentStyle, true))
			}
		} else {
			sb.WriteString(p.styles.GetStyle(StyleDefault).Render(pfx))
			sb.WriteString(cnt)
		}
	}

	cw := p.contentWidth(len(prefix))
	continuationPrefix := strings.Repeat(" ", len(prefix))

	// Treat non-wrapping as wrapping with a single subLine.
	subLines := []string{content}
	if cw > 0 {
		subLines = strings.Split(lipgloss.Wrap(content, cw, wrapOnCharacters), "\n")
	}

	// Track column offset for wrapped lines (1-indexed).
	col := 1

	for j, subLine := range subLines {
		if j > 0 {
			sb.WriteString("\n")
		}

		if p.lineNumbers && lineNum > 0 {
			if j == 0 {
				sb.WriteString(p.formatLineNumber(lineNum))
			} else {
				sb.WriteString(p.formatContinuationMarker())
			}
		}

		if j == 0 {
			renderLine(prefix, subLine, col)
		} else {
			renderLine(continuationPrefix, subLine, col)
		}

		col += len([]rune(subLine))
	}
}

// buildLinesFromTokens builds lines directly from token Origins,
// avoiding intermediate string concatenation.
func buildLinesFromTokens(tokens token.Tokens) []string {
	if len(tokens) == 0 {
		return nil
	}

	var (
		lines []string
		sb    strings.Builder
	)
	for _, tk := range tokens {
		origin := crlfNormalizer.Replace(tk.Origin)
		parts := strings.Split(origin, "\n")

		for i, part := range parts {
			if i > 0 {
				lines = append(lines, sb.String())
				sb.Reset()
			}

			sb.WriteString(part)
		}
	}

	if sb.Len() > 0 {
		lines = append(lines, sb.String())
	}

	return lines
}

// styleForPosition returns the effective style for a character at (line, col),
// applying range styles to the base style.
// If alwaysBlend is false, the first matching range overrides the base style;
// subsequent ranges blend. If alwaysBlend is true, all ranges blend with base.
func (p *Printer) styleForPosition(line, col int, style *lipgloss.Style, alwaysBlend bool) *lipgloss.Style {
	firstRange := true

	for i := range p.rangeStyles {
		if p.rangeStyles[i].rng.Contains(line, col) {
			if !alwaysBlend && firstRange {
				// First range overrides base (colors and transforms).
				style = overrideStyles(style, &p.rangeStyles[i].style)
				firstRange = false
			} else {
				// Subsequent ranges blend (colors and compose transforms).
				style = blendStyles(style, &p.rangeStyles[i].style)
			}
		}
	}

	return style
}

func (p *Printer) styleForToken(tk *token.Token) *lipgloss.Style {
	// Check highlights first.
	for i := range p.tokenStyles {
		if tk.Position.Line == p.tokenStyles[i].pos.Line && tk.Position.Column == p.tokenStyles[i].pos.Col {
			return &p.tokenStyles[i].style
		}
	}

	//nolint:exhaustive // Only needed for the current token.
	switch tk.PreviousType() {
	case token.AnchorType:
		return p.styles.GetStyle(StyleAnchor)

	case token.AliasType:
		return p.styles.GetStyle(StyleAlias)
	}

	//nolint:exhaustive // Only needed for the current token.
	switch tk.NextType() {
	case token.MappingValueType:
		return p.styles.GetStyle(StyleKey)
	}

	switch tk.Type {
	case token.BoolType:
		return p.styles.GetStyle(StyleBool)

	case token.AnchorType:
		return p.styles.GetStyle(StyleAnchor)

	case token.AliasType, token.MergeKeyType:
		return p.styles.GetStyle(StyleAlias)

	case token.StringType, token.SingleQuoteType, token.DoubleQuoteType:
		return p.styles.GetStyle(StyleString)

	case token.IntegerType, token.FloatType,
		token.BinaryIntegerType, token.OctetIntegerType, token.HexIntegerType,
		token.InfinityType, token.NanType:
		return p.styles.GetStyle(StyleNumber)

	case token.NullType, token.ImplicitNullType:
		return p.styles.GetStyle(StyleNull)

	case token.CommentType:
		return p.styles.GetStyle(StyleComment)

	case token.TagType:
		return p.styles.GetStyle(StyleTag)

	case token.DocumentHeaderType, token.DocumentEndType:
		return p.styles.GetStyle(StyleDocument)

	case token.DirectiveType:
		return p.styles.GetStyle(StyleDirective)

	case token.LiteralType, token.FoldedType:
		return p.styles.GetStyle(StyleBlockScalar)

	case token.SequenceEntryType, token.MappingKeyType, token.MappingValueType,
		token.CollectEntryType, token.SequenceStartType, token.SequenceEndType,
		token.MappingStartType, token.MappingEndType:
		return p.styles.GetStyle(StylePunctuation)

	case token.UnknownType, token.InvalidType:
		return p.styles.GetStyle(StyleError)

	case token.SpaceType:
		return p.styles.GetStyle(StyleDefault)
	}

	return p.styles.GetStyle(StyleDefault)
}

// styleLineWithRanges styles a line with range-aware styling.
// It splits the line into spans based on effective styles (base + overlapping ranges).
// LineNum is 1-indexed, startCol is the column of the first character in src.
// If alwaysBlend is true, range styles always blend with base (used for diff lines).
func (p *Printer) styleLineWithRanges(
	src string,
	lineNum, startCol int,
	style *lipgloss.Style,
	alwaysBlend bool,
) string {
	if src == "" {
		return src
	}

	if len(p.rangeStyles) == 0 {
		return style.Render(src)
	}

	var sb strings.Builder

	runes := []rune(src)
	spanStart := 0
	currentStyle := p.styleForPosition(lineNum, startCol, style, alwaysBlend)

	for i := 1; i < len(runes); i++ {
		nextStyle := p.styleForPosition(lineNum, startCol+i, style, alwaysBlend)
		if !stylesEqual(currentStyle, nextStyle) {
			sb.WriteString(currentStyle.Render(string(runes[spanStart:i])))

			spanStart = i
			currentStyle = nextStyle
		}
	}

	// Flush remaining content.
	sb.WriteString(currentStyle.Render(string(runes[spanStart:])))

	return sb.String()
}

// formatLineNumber formats a line number using consistent 4-char width.
// It then applies the line number style.
func (p *Printer) formatLineNumber(lineNum int) string {
	return p.lineNumberStyle.Render(fmt.Sprintf("%4d", lineNum))
}

// contentWidth returns the available width for content after accounting for
// prefix and line numbers. Returns 0 if wrapping is disabled.
func (p *Printer) contentWidth(prefixWidth int) int {
	if p.width <= 0 {
		return 0
	}

	lineNumWidth := 0
	if p.lineNumbers {
		lineNumWidth = 6
	}

	return max(0, p.width-prefixWidth-lineNumWidth)
}

// formatContinuationMarker formats a continuation marker for wrapped lines.
// Uses "   -" to match the 4-char width of line numbers.
func (p *Printer) formatContinuationMarker() string {
	return p.lineNumberStyle.Render("   -") + p.styles.GetStyle(StyleDefault).Render(p.linePrefix)
}

// addLineNumbers prepends line numbers to each line of the content.
// When word wrap is enabled, continuation lines get a "-" marker instead of line numbers.
func (p *Printer) addLineNumbers(content string, startLine int) string {
	var sb strings.Builder

	lines := strings.Split(content, "\n")
	lineNum := startLine
	styledPrefix := p.styles.GetStyle(StyleDefault).Render(p.linePrefix)
	wrapWidth := p.contentWidth(0)

	for i, line := range lines {
		if i > 0 {
			sb.WriteByte('\n')
		}

		// Treat non-wrapping as wrapping with a single subLine.
		subLines := []string{line}
		if wrapWidth > 0 {
			subLines = strings.Split(lipgloss.Wrap(line, wrapWidth, wrapOnCharacters), "\n")
		}

		for j, subLine := range subLines {
			if j > 0 {
				sb.WriteByte('\n')
			}

			if j == 0 {
				sb.WriteString(p.formatLineNumber(lineNum))
				sb.WriteString(styledPrefix)
			} else {
				sb.WriteString(p.formatContinuationMarker())
			}

			sb.WriteString(subLine)
		}

		lineNum++
	}

	return sb.String()
}

// getTokenString renders tokens with range-aware styling.
// It tracks column positions cumulatively (character by character) to match
// how the Finder builds its position map, ensuring consistent highlighting.
func (p *Printer) getTokenString(tokens token.Tokens) string {
	if len(tokens) == 0 {
		return ""
	}

	pt := NewPositionTrackerFromTokens(tokens)

	var sb strings.Builder
	for _, tk := range tokens {
		tokenStyle := p.styleForToken(tk)

		// TokenValueOffset returns the character offset within the first non-empty line
		// where Value starts. This is used to detect leading whitespace separators.
		valueOffset := tokenValueOffset(tk)

		// Process the Origin line by line, character by character.
		lines := strings.Split(tk.Origin, "\n")
		firstContentLineProcessed := false

		for lineIdx, line := range lines {
			if lineIdx > 0 {
				sb.WriteByte('\n')
				pt.AdvanceNewline()
			}

			if line == "" {
				continue
			}

			lineRunes := []rune(line)
			lineStartCol := pt.Col

			// Determine where separator ends within this line.
			// The separator only applies to the first content line of the token
			// (not necessarily lineIdx == 0, since Origin may start with newlines).
			var separatorRunesInLine int
			if !firstContentLineProcessed {
				separatorRunesInLine = leadingWhitespaceRunes(line, valueOffset)
			}

			firstContentLineProcessed = true

			// Part 1: Render separator portion (default style).
			if separatorRunesInLine > 0 && separatorRunesInLine <= len(lineRunes) {
				sepPart := string(lineRunes[:separatorRunesInLine])
				defaultStyle := p.styles.GetStyle(StyleDefault)
				sb.WriteString(p.styleLineWithRanges(sepPart, pt.Line, lineStartCol, defaultStyle, false))
				pt.AdvanceBy(separatorRunesInLine)

				lineRunes = lineRunes[separatorRunesInLine:]
			}

			// Part 2: Render content portion (token style).
			if len(lineRunes) > 0 {
				contentStartCol := pt.Col
				sb.WriteString(p.styleLineWithRanges(string(lineRunes), pt.Line, contentStartCol, tokenStyle, false))

				pt.AdvanceBy(len(lineRunes))
			}
		}
	}

	return sb.String()
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

// firstTokenAtOrAfter positions tk at the first token at or after minLine.
// If minLine is negative (unbounded), walks to the start of the token list.
func firstTokenAtOrAfter(tk *token.Token, minLine int) *token.Token {
	// Unbounded: walk to start.
	if minLine < 0 {
		for tk.Prev != nil {
			tk = tk.Prev
		}

		return tk
	}

	// Before range: walk forward.
	if tk.Position.Line < minLine {
		for tk != nil && tk.Position.Line < minLine {
			tk = tk.Next
		}

		return tk
	}

	// At or after range: walk backward to find first token in range.
	for tk.Prev != nil && tk.Prev.Position.Line >= minLine {
		tk = tk.Prev
	}

	return tk
}

// extractTokensInRange extracts tokens that touch [minLine, maxLine].
// It clones tokens and adjusts the first token's Origin to remove leading
// newlines while preserving leading whitespace from the previous token.
// If either range limit is negative, it is unbounded in that direction.
func extractTokensInRange(tk *token.Token, minLine, maxLine int) token.Tokens {
	tk = firstTokenAtOrAfter(tk, minLine)

	// If no tokens in range, return empty.
	if tk == nil || (maxLine >= 0 && tk.Position.Line > maxLine) {
		return token.Tokens{}
	}

	// Clone the first token.
	firstTk := tk.Clone()

	// Preserve leading whitespace from previous token.
	if firstTk.Prev != nil {
		prev := firstTk.Prev
		whiteSpaceLen := len(prev.Origin) - len(strings.TrimRight(prev.Origin, " "))
		if whiteSpaceLen > 0 {
			firstTk.Origin = strings.Repeat(" ", whiteSpaceLen) + firstTk.Origin
		}
	}

	// If min is bounded, trim any leading newlines.
	if minLine >= 0 {
		firstTk.Origin = strings.TrimLeft(firstTk.Origin, "\r\n")
	}

	tokens := token.Tokens{firstTk}

	// Walk forward to collect tokens up to maxLine.
	for t := tk.Next; t != nil && (maxLine < 0 || t.Position.Line <= maxLine); t = t.Next {
		// Skip parser-added implicit null tokens to match lexer output.
		if t.Type == token.ImplicitNullType {
			continue
		}

		tokens.Add(t.Clone())
	}

	return tokens
}

func countNewlines(s string) int {
	return strings.Count(crlfNormalizer.Replace(s), "\n")
}

func endsWithNewline(s string) bool {
	s = strings.TrimRight(s, " ")
	return strings.HasSuffix(s, "\n") || strings.HasSuffix(s, "\r")
}

// addLinePrefix prefixes each line with the given string.
func addLinePrefix(content, prefix string) string {
	return prefix + strings.ReplaceAll(content, "\n", "\n"+prefix)
}

// leadingWhitespaceRunes returns the number of runes in the leading whitespace
// portion of line, up to maxBytes. Returns 0 if maxBytes is invalid or if the
// prefix contains non-whitespace characters.
func leadingWhitespaceRunes(line string, maxBytes int) int {
	if maxBytes <= 0 || maxBytes > len(line) {
		return 0
	}

	prefix := line[:maxBytes]
	if strings.TrimLeft(prefix, " \t") != "" {
		return 0
	}

	return len([]rune(prefix))
}
