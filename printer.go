package niceyaml

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/goccy/go-yaml/token"
)

const wrapOnCharacters = " /-"

// crlfNormalizer converts Windows (CRLF) and old Mac (CR) line endings to Unix (LF).
var crlfNormalizer = strings.NewReplacer("\r\n", "\n", "\r", "\n")

// Printer renders YAML tokens with syntax highlighting using [lipgloss.Style].
// It supports custom color schemes, line numbers, and styled token/range overlays
// for highlighting specific positions such as errors.
type Printer struct {
	colorScheme              *ColorScheme
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
// By default it uses [DefaultColorScheme].
func NewPrinter(opts ...PrinterOption) *Printer {
	dcs := DefaultColorScheme()
	p := &Printer{
		colorScheme:        &dcs,
		linePrefix:         " ",
		lineInsertedPrefix: "+",
		lineDeletedPrefix:  "-",
		initialLineNumber:  1,
	}

	for _, opt := range opts {
		opt(p)
	}

	if !p.hasCustomStyle {
		p.style = p.colorScheme.Default.
			PaddingRight(1)
	}
	if !p.hasCustomLineNumberStyle {
		p.lineNumberStyle = p.colorScheme.Default.
			Foreground(p.colorScheme.Comment.GetForeground()).
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

// WithColorScheme configures the printer with the given color scheme.
//
//nolint:gocritic // hugeParam: Copying.
func WithColorScheme(cs ColorScheme) PrinterOption {
	return func(p *Printer) {
		p.colorScheme = &cs
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
//
//nolint:gocritic // hugeParam: Copying.
func (p *Printer) AddStyleToToken(style lipgloss.Style, pos Position) {
	p.tokenStyles = append(p.tokenStyles, &tokenStyle{
		style: style,
		pos:   pos,
	})
}

// AddStyleToRange adds a style to apply to the character range [r.Start, r.End).
// The range is half-open: Start is inclusive, End is exclusive.
// Line and column are 1-indexed, matching [token.Position].
// Overlapping range colors are blended; transforms are composed (overlay wraps base).
//
//nolint:gocritic // hugeParam: Copying.
func (p *Printer) AddStyleToRange(style lipgloss.Style, r PositionRange) {
	p.rangeStyles = append(p.rangeStyles, &rangeStyle{
		style: style,
		rng:   r,
	})
}

// ClearStyles removes all previously added styles.
func (p *Printer) ClearStyles() {
	p.tokenStyles = nil
	p.rangeStyles = nil
}

// PrintTokens renders tokens with syntax highlighting and returns the result.
// If line numbers are enabled, each line is prefixed with its number.
func (p *Printer) PrintTokens(tokens token.Tokens) string {
	content := p.getTokenString(tokens)
	content = p.applyLinePrefixes(content, p.initialLineNumber)

	// Apply word wrapping when line numbers are disabled.
	if !p.lineNumbers && p.width > 0 {
		content = p.wrapContent(content)
	}

	return p.style.Render(content)
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

	tokens := p.extractTokensInRange(tk, minLine, maxLine)
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
			p.writeLine(&sb, p.lineDeletedPrefix, op.content, op.beforeLine, &p.colorScheme.DiffDeleted)

		case diffInsert:
			p.writeLine(&sb, p.lineInsertedPrefix, op.content, op.afterLine, &p.colorScheme.DiffInserted)

		default: // Equal.
			// Use syntax-highlighted content from pre-rendered after tokens.
			// AfterLine is always >= 1 for Equal ops from [lcsLineDiff].
			line := styledLines[op.afterLine-1]
			p.writeLine(&sb, p.linePrefix, line, op.afterLine, nil)
		}
	}

	return sb.String()
}

// writeLine writes a line with optional word wrapping.
// If contentStyle is non-nil, applies it to prefix+content together (for diff lines).
// If contentStyle is nil, styles only the prefix and preserves content styling (for equal lines).
func (p *Printer) writeLine(sb *strings.Builder, prefix, content string, lineNum int, contentStyle *lipgloss.Style) {
	renderLine := func(pfx, cnt string) {
		if contentStyle != nil {
			sb.WriteString(contentStyle.Render(pfx + cnt))
		} else {
			sb.WriteString(p.colorScheme.Default.Render(pfx))
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

	for j, subLine := range subLines {
		if j > 0 {
			sb.WriteByte('\n')
		}

		if p.lineNumbers && lineNum > 0 {
			if j == 0 {
				sb.WriteString(p.formatLineNumber(lineNum))
			} else {
				sb.WriteString(p.formatContinuationMarker())
			}
		}

		if j == 0 {
			renderLine(prefix, subLine)
		} else {
			renderLine(continuationPrefix, subLine)
		}
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
// blending the base style with all applicable range styles.
func (p *Printer) styleForPosition(line, col int, style *lipgloss.Style) *lipgloss.Style {
	for i := range p.rangeStyles {
		if p.rangeStyles[i].rng.Contains(line, col) {
			style = blendStyles(style, &p.rangeStyles[i].style)
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
		return &p.colorScheme.Anchor
	case token.AliasType:
		return &p.colorScheme.Alias
	}

	//nolint:exhaustive // Only needed for the current token.
	switch tk.NextType() {
	case token.MappingValueType:
		return &p.colorScheme.Key
	}

	switch tk.Type {
	case token.BoolType:
		return &p.colorScheme.Bool

	case token.AnchorType:
		return &p.colorScheme.Anchor

	case token.AliasType, token.MergeKeyType:
		return &p.colorScheme.Alias

	case token.StringType, token.SingleQuoteType, token.DoubleQuoteType:
		return &p.colorScheme.String

	case token.IntegerType, token.FloatType,
		token.BinaryIntegerType, token.OctetIntegerType, token.HexIntegerType,
		token.InfinityType, token.NanType:
		return &p.colorScheme.Number

	case token.NullType, token.ImplicitNullType:
		return &p.colorScheme.Null

	case token.CommentType:
		return &p.colorScheme.Comment

	case token.TagType:
		return &p.colorScheme.Tag

	case token.DocumentHeaderType, token.DocumentEndType:
		return &p.colorScheme.Document

	case token.DirectiveType:
		return &p.colorScheme.Directive

	case token.LiteralType, token.FoldedType:
		return &p.colorScheme.BlockScalar

	case token.SequenceEntryType, token.MappingKeyType, token.MappingValueType,
		token.CollectEntryType, token.SequenceStartType, token.SequenceEndType,
		token.MappingStartType, token.MappingEndType:
		return &p.colorScheme.Punctuation

	default:
		return &p.colorScheme.Default
	}
}

// styleLineWithRanges styles a line with range-aware styling.
// It splits the line into spans based on effective styles (base + overlapping ranges).
// LineNum is 1-indexed, startCol is the column of the first character in src.
func (p *Printer) styleLineWithRanges(src string, lineNum, startCol int, style *lipgloss.Style) string {
	if src == "" {
		return src
	}

	if len(p.rangeStyles) == 0 {
		return style.Render(src)
	}

	var sb strings.Builder

	runes := []rune(src)
	spanStart := 0
	currentStyle := p.styleForPosition(lineNum, startCol, style)

	for i := 1; i < len(runes); i++ {
		nextStyle := p.styleForPosition(lineNum, startCol+i, style)
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
	return p.lineNumberStyle.Render("   -") + p.colorScheme.Default.Render(p.linePrefix)
}

// addLineNumbers prepends line numbers to each line of the content.
// When word wrap is enabled, continuation lines get a "-" marker instead of line numbers.
func (p *Printer) addLineNumbers(content string, startLine int) string {
	var sb strings.Builder

	lines := strings.Split(content, "\n")
	lineNum := startLine
	styledPrefix := p.colorScheme.Default.Render(p.linePrefix)
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
				sb.WriteString(p.styleLineWithRanges(sepPart, pt.Line, lineStartCol, &p.colorScheme.Default))
				pt.AdvanceBy(separatorRunesInLine)

				lineRunes = lineRunes[separatorRunesInLine:]
			}

			// Part 2: Render content portion (token style).
			if len(lineRunes) > 0 {
				contentStartCol := pt.Col
				sb.WriteString(p.styleLineWithRanges(string(lineRunes), pt.Line, contentStartCol, tokenStyle))

				pt.AdvanceBy(len(lineRunes))
			}
		}
	}

	return sb.String()
}

// extractTokensInRange extracts tokens that touch [minLine, maxLine].
// It clones tokens and adjusts the first token's Origin to remove leading
// newlines while preserving leading whitespace from the previous token.
func (p *Printer) extractTokensInRange(tk *token.Token, minLine, maxLine int) token.Tokens {
	// Walk backward to find the first token at or after minLine.
	for tk.Prev != nil && tk.Prev.Position.Line >= minLine {
		tk = tk.Prev
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

	// Trim leading newlines (we're not showing earlier lines).
	firstTk.Origin = strings.TrimLeft(firstTk.Origin, "\r\n")

	tokens := token.Tokens{firstTk}

	// Walk forward to collect tokens up to maxLine.
	for t := tk.Next; t != nil && t.Position.Line <= maxLine; t = t.Next {
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
