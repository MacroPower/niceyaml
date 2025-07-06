package niceyaml

import (
	"fmt"
	"math"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/cellbuf"
	"github.com/goccy/go-yaml/token"

	udiff "github.com/aymanbagabas/go-udiff"
)

const wrapOnCharacters = " /-"

// lineOp represents a line in the full diff output.
type lineOp struct {
	content    string       // Line content without newline.
	kind       udiff.OpKind // Delete, Insert, or Equal.
	afterLine  int          // 1-indexed line in "after" (for syntax highlighting).
	beforeLine int          // 1-indexed line in "before" (for deleted lines).
}

// Printer renders YAML tokens with syntax highlighting using [lipgloss.Style].
// It supports custom color schemes, line numbers, and styled token/range overlays
// for highlighting specific positions such as errors.
type Printer struct {
	colorScheme              ColorScheme
	style                    lipgloss.Style
	lineNumberStyle          lipgloss.Style
	linePrefix               string
	lineInsertedPrefix       string
	lineDeletedPrefix        string
	tokenStyles              []tokenStyle
	rangeStyles              []rangeStyle
	initialLineNumber        int
	width                    int
	hasCustomStyle           bool
	hasCustomLineNumberStyle bool
	lineNumbers              bool
}

// NewPrinter creates a new [Printer].
// By default it uses [DefaultColorScheme].
func NewPrinter(opts ...PrinterOption) *Printer {
	p := &Printer{
		colorScheme:        DefaultColorScheme(),
		linePrefix:         " ",
		lineInsertedPrefix: "+",
		lineDeletedPrefix:  "-",
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
			PaddingRight(2)
	}

	return p
}

// PrinterOption configures a [Printer].
type PrinterOption func(*Printer)

// WithStyle configures the printer with the given container style.
func WithStyle(s lipgloss.Style) PrinterOption {
	return func(p *Printer) {
		p.style = s
		p.hasCustomStyle = true
	}
}

// WithColorScheme configures the printer with the given color scheme.
func WithColorScheme(cs ColorScheme) PrinterOption {
	return func(p *Printer) {
		p.colorScheme = cs
	}
}

// WithLineNumbers enables line number display.
func WithLineNumbers() PrinterOption {
	return func(p *Printer) {
		p.lineNumbers = true
	}
}

// WithLineNumberStyle sets the style for line numbers.
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
func (p *Printer) AddStyleToToken(style lipgloss.Style, pos Position) {
	p.tokenStyles = append(p.tokenStyles, tokenStyle{
		style: style,
		pos:   pos,
	})
}

// AddStyleToRange adds a style to apply to the character range [r.Start, r.End).
// The range is half-open: Start is inclusive, End is exclusive.
// Line and column are 1-indexed, matching [token.Position].
// Overlapping range colors are blended; transforms are composed (overlay wraps base).
func (p *Printer) AddStyleToRange(style lipgloss.Style, r PositionRange) {
	p.rangeStyles = append(p.rangeStyles, rangeStyle{
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
	content = addLinePrefix(content, p.linePrefix)

	if p.lineNumbers {
		startLine := p.initialLineNumber
		if startLine < 1 {
			startLine = 1
		}

		content = p.addLineNumbers(content, startLine)
	} else if p.width > 0 {
		// Apply word wrapping when line numbers are disabled.
		content = p.wrapContent(content)
	}

	return p.style.Render(content)
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

		sb.WriteString(cellbuf.Wrap(line, p.width, wrapOnCharacters))
	}

	return sb.String()
}

// PrintErrorToken prints the tokens around the error token with context.
// Returns the formatted string and the starting line number.
func (p *Printer) PrintErrorToken(tk *token.Token, lines int) (string, int) {
	curLine := tk.Position.Line
	curExtLine := curLine + countNewlines(strings.TrimLeft(tk.Origin, "\r\n"))
	if endsWithNewline(tk.Origin) {
		// If last character ( exclude white space ) is new line character, ignore it.
		curExtLine--
	}

	minLine := int(math.Max(float64(curLine-lines), 1))
	maxLine := curExtLine + lines

	beforeTokens := p.printBeforeTokens(tk, minLine, curExtLine)
	lastTk := beforeTokens[len(beforeTokens)-1]
	afterTokens := p.printAfterTokens(lastTk.Next, maxLine)

	beforeSource := p.getTokenString(beforeTokens)
	afterSource := p.getTokenString(afterTokens)

	all := beforeSource + "\n" + afterSource
	all = addLinePrefix(all, p.linePrefix)

	if p.lineNumbers {
		startLine := p.initialLineNumber
		if startLine < 1 {
			startLine = minLine
		}

		all = p.addLineNumbers(all, startLine)
	}

	return p.style.Render(all), minLine
}

// PrintTokenDiff generates a full-file diff between two token collections.
// It outputs the entire file with markers for inserted and deleted lines.
// Unchanged lines preserve syntax highlighting; inserted/deleted lines use diff styles.
func (p *Printer) PrintTokenDiff(before, after token.Tokens) string {
	beforeStr := p.getPlainTokenString(before)
	afterStr := p.getPlainTokenString(after)

	edits := udiff.Strings(beforeStr, afterStr)
	if len(edits) == 0 {
		return ""
	}

	// Build line operations directly from edits.
	ops := p.computeLineOpsFromEdits(beforeStr, afterStr, edits)

	// Render full file with diff markers.
	return p.style.Render(p.renderFullFileDiff(ops, after))
}

// computeLineOpsFromEdits converts byte-level edits to line-level operations.
// It uses LCS to compute the minimal diff between before and after lines.
func (p *Printer) computeLineOpsFromEdits(beforeStr, afterStr string, _ []udiff.Edit) []lineOp {
	beforeLines := strings.Split(beforeStr, "\n")
	afterLines := strings.Split(afterStr, "\n")

	// Remove trailing empty line from split if content ends with newline.
	if len(beforeLines) > 0 && beforeLines[len(beforeLines)-1] == "" {
		beforeLines = beforeLines[:len(beforeLines)-1]
	}

	if len(afterLines) > 0 && afterLines[len(afterLines)-1] == "" {
		afterLines = afterLines[:len(afterLines)-1]
	}

	// Build the diff output using longest common subsequence approach.
	return p.lcsLineDiff(beforeLines, afterLines)
}

// lcsLineDiff computes line operations using a simple LCS-based diff.
func (p *Printer) lcsLineDiff(beforeLines, afterLines []string) []lineOp {
	// Simple O(nm) LCS-based diff algorithm.
	m, n := len(beforeLines), len(afterLines)

	// Build LCS table.
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}

	for i := m - 1; i >= 0; i-- {
		for j := n - 1; j >= 0; j-- {
			if beforeLines[i] == afterLines[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else {
				dp[i][j] = max(dp[i+1][j], dp[i][j+1])
			}
		}
	}

	// Backtrack to build operations.
	// Standard diff convention: deletions come before insertions.
	var ops []lineOp

	i, j := 0, 0

	for i < m || j < n {
		switch {
		case i < m && j < n && beforeLines[i] == afterLines[j]:
			// Equal line.
			ops = append(ops, lineOp{
				kind:       udiff.Equal,
				content:    afterLines[j],
				afterLine:  j + 1,
				beforeLine: i + 1,
			})
			i++
			j++

		case i < m && (j >= n || dp[i+1][j] >= dp[i][j+1]):
			// Delete from before (prefer deletion when tied).
			ops = append(ops, lineOp{
				kind:       udiff.Delete,
				content:    beforeLines[i],
				afterLine:  0,
				beforeLine: i + 1,
			})
			i++

		default:
			// Insert from after.
			ops = append(ops, lineOp{
				kind:       udiff.Insert,
				content:    afterLines[j],
				afterLine:  j + 1,
				beforeLine: 0,
			})
			j++
		}
	}

	return ops
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
		case udiff.Delete:
			p.writeDiffLine(&sb, p.lineDeletedPrefix, op.content, p.colorScheme.DiffDeleted, op.beforeLine)

		case udiff.Insert:
			p.writeDiffLine(&sb, p.lineInsertedPrefix, op.content, p.colorScheme.DiffInserted, op.afterLine)

		default: // Equal.
			// Use syntax-highlighted content from pre-rendered after tokens.
			if op.afterLine > 0 && op.afterLine <= len(styledLines) {
				p.writeEqualLine(&sb, styledLines[op.afterLine-1], op.afterLine)
			} else {
				p.writeEqualLine(&sb, op.content, op.afterLine)
			}
		}
	}

	return sb.String()
}

// writeDiffLine writes a diff line (inserted or deleted) with optional word wrapping.
// Continuation lines use spaces instead of the diff marker.
func (p *Printer) writeDiffLine(sb *strings.Builder, prefix, content string, style lipgloss.Style, lineNum int) {
	if p.width <= 0 {
		if p.lineNumbers && lineNum > 0 {
			sb.WriteString(p.formatLineNumber(lineNum))
		}

		sb.WriteString(style.Render(prefix + content))

		return
	}

	// Calculate available width for content (subtract prefix width).
	// Reserve 6 chars for line numbers (4 digits + 2 padding) when enabled.
	prefixWidth := len(prefix)
	lineNumWidth := 0

	if p.lineNumbers {
		lineNumWidth = 6
	}

	contentWidth := p.width - prefixWidth - lineNumWidth

	if contentWidth <= 0 {
		if p.lineNumbers && lineNum > 0 {
			sb.WriteString(p.formatLineNumber(lineNum))
		}

		sb.WriteString(style.Render(prefix + content))

		return
	}

	wrapped := cellbuf.Wrap(content, contentWidth, wrapOnCharacters)
	subLines := strings.Split(wrapped, "\n")

	// Create continuation prefix (spaces matching the diff marker width).
	continuationPrefix := strings.Repeat(" ", prefixWidth)

	for j, subLine := range subLines {
		if j > 0 {
			sb.WriteByte('\n')
		}

		if j == 0 {
			if p.lineNumbers && lineNum > 0 {
				sb.WriteString(p.formatLineNumber(lineNum))
			}

			sb.WriteString(style.Render(prefix + subLine))
		} else {
			if p.lineNumbers {
				sb.WriteString(p.formatContinuationMarker())
			}

			sb.WriteString(style.Render(continuationPrefix + subLine))
		}
	}
}

// writeEqualLine writes an equal line with optional word wrapping.
func (p *Printer) writeEqualLine(sb *strings.Builder, content string, lineNum int) {
	if p.width <= 0 {
		if p.lineNumbers && lineNum > 0 {
			sb.WriteString(p.formatLineNumber(lineNum))
		}

		sb.WriteString(p.colorScheme.Default.Render(p.linePrefix))
		sb.WriteString(content)

		return
	}

	// Calculate available width for content (subtract prefix width).
	// Reserve 6 chars for line numbers (4 digits + 2 padding) when enabled.
	prefixWidth := len(p.linePrefix)
	lineNumWidth := 0

	if p.lineNumbers {
		lineNumWidth = 6
	}

	contentWidth := p.width - prefixWidth - lineNumWidth

	if contentWidth <= 0 {
		if p.lineNumbers && lineNum > 0 {
			sb.WriteString(p.formatLineNumber(lineNum))
		}

		sb.WriteString(p.colorScheme.Default.Render(p.linePrefix))
		sb.WriteString(content)

		return
	}

	wrapped := cellbuf.Wrap(content, contentWidth, wrapOnCharacters)
	subLines := strings.Split(wrapped, "\n")

	// Create continuation prefix (spaces matching the prefix width).
	continuationPrefix := strings.Repeat(" ", prefixWidth)

	for j, subLine := range subLines {
		if j > 0 {
			sb.WriteByte('\n')
		}

		if j == 0 {
			if p.lineNumbers && lineNum > 0 {
				sb.WriteString(p.formatLineNumber(lineNum))
			}

			sb.WriteString(p.colorScheme.Default.Render(p.linePrefix))
		} else {
			if p.lineNumbers {
				sb.WriteString(p.formatContinuationMarker())
			}

			sb.WriteString(p.colorScheme.Default.Render(continuationPrefix))
		}

		sb.WriteString(subLine)
	}
}

func (p *Printer) getPlainTokenString(tokens token.Tokens) string {
	var sb strings.Builder

	for _, tk := range tokens {
		sb.WriteString(tk.Origin)
	}

	return sb.String()
}

// styleForPosition returns the effective style for a character at (line, col),
// blending the base style with all applicable range styles.
func (p *Printer) styleForPosition(line, col int, base lipgloss.Style) lipgloss.Style {
	result := base

	for i := range p.rangeStyles {
		if p.rangeStyles[i].rng.Contains(line, col) {
			result = blendStyles(result, p.rangeStyles[i].style)
		}
	}

	return result
}

func (p *Printer) styleForToken(tk *token.Token) lipgloss.Style {
	// Check highlights first.
	for i := range p.tokenStyles {
		if tk.Position.Line == p.tokenStyles[i].pos.Line && tk.Position.Column == p.tokenStyles[i].pos.Col {
			return p.tokenStyles[i].style
		}
	}

	//nolint:exhaustive // Only needed for the current token.
	switch tk.PreviousType() {
	case token.AnchorType:
		return p.colorScheme.Anchor
	case token.AliasType:
		return p.colorScheme.Alias
	}

	//nolint:exhaustive // Only needed for the current token.
	switch tk.NextType() {
	case token.MappingValueType:
		return p.colorScheme.Key
	}

	switch tk.Type {
	case token.BoolType:
		return p.colorScheme.Bool

	case token.AnchorType:
		return p.colorScheme.Anchor

	case token.AliasType, token.MergeKeyType:
		return p.colorScheme.Alias

	case token.StringType, token.SingleQuoteType, token.DoubleQuoteType:
		return p.colorScheme.String

	case token.IntegerType, token.FloatType,
		token.BinaryIntegerType, token.OctetIntegerType, token.HexIntegerType,
		token.InfinityType, token.NanType:
		return p.colorScheme.Number

	case token.NullType, token.ImplicitNullType:
		return p.colorScheme.Null

	case token.CommentType:
		return p.colorScheme.Comment

	case token.TagType:
		return p.colorScheme.Tag

	case token.DocumentHeaderType, token.DocumentEndType:
		return p.colorScheme.Document

	case token.DirectiveType:
		return p.colorScheme.Directive

	case token.LiteralType, token.FoldedType:
		return p.colorScheme.BlockScalar

	case token.SequenceEntryType, token.MappingKeyType, token.MappingValueType,
		token.CollectEntryType, token.SequenceStartType, token.SequenceEndType,
		token.MappingStartType, token.MappingEndType:
		return p.colorScheme.Punctuation

	default:
		return p.colorScheme.Default
	}
}

// styleLineWithRanges styles a line with range-aware styling.
// It splits the line into spans based on effective styles (base + overlapping ranges).
// LineNum is 1-indexed, startCol is the column of the first character in src.
func (p *Printer) styleLineWithRanges(src string, lineNum, startCol int, baseStyle lipgloss.Style) string {
	if src == "" {
		return src
	}

	if len(p.rangeStyles) == 0 {
		return baseStyle.Render(src)
	}

	var sb strings.Builder

	runes := []rune(src)

	col := startCol
	spanStart := 0
	currentStyle := p.styleForPosition(lineNum, col, baseStyle)

	for i := 1; i <= len(runes); i++ {
		var nextStyle lipgloss.Style

		if i < len(runes) {
			nextStyle = p.styleForPosition(lineNum, col+i-spanStart, baseStyle)
		}

		// End of string or style changed: render the span.
		if i == len(runes) || !stylesEqual(currentStyle, nextStyle) {
			span := string(runes[spanStart:i])
			sb.WriteString(currentStyle.Render(span))

			if i < len(runes) {
				spanStart = i
				currentStyle = nextStyle
				col = startCol + i
			}
		}
	}

	return sb.String()
}

// formatLineNumber formats a line number using consistent 4-char width.
// It then applies the line number style.
func (p *Printer) formatLineNumber(lineNum int) string {
	return p.lineNumberStyle.Render(fmt.Sprintf("%4d", lineNum))
}

// formatContinuationMarker formats a continuation marker for wrapped lines.
// Uses "   -" to match the 4-char width of line numbers.
func (p *Printer) formatContinuationMarker() string {
	return p.lineNumberStyle.Render("   -" + p.linePrefix)
}

// addLineNumbers prepends line numbers to each line of the content.
// When word wrap is enabled, continuation lines get a "-" marker instead of line numbers.
func (p *Printer) addLineNumbers(content string, startLine int) string {
	lines := strings.Split(content, "\n")

	var sb strings.Builder

	lineNum := startLine

	for i, line := range lines {
		if i > 0 {
			sb.WriteByte('\n')
		}

		// Apply word wrapping if enabled.
		// Reserve 6 chars for line numbers (4 digits + 2 padding) when calculating wrap width.
		if p.width > 0 {
			wrapWidth := max(0, p.width-6)
			wrapped := cellbuf.Wrap(line, wrapWidth, wrapOnCharacters)
			subLines := strings.Split(wrapped, "\n")

			for j, subLine := range subLines {
				if j > 0 {
					sb.WriteByte('\n')
				}

				if j == 0 {
					sb.WriteString(p.formatLineNumber(lineNum))
				} else {
					sb.WriteString(p.formatContinuationMarker())
				}

				sb.WriteString(subLine)
			}
		} else {
			sb.WriteString(p.formatLineNumber(lineNum))
			sb.WriteString(line)
		}

		lineNum++
	}

	return sb.String()
}

func (p *Printer) getTokenString(tokens token.Tokens) string {
	if len(tokens) == 0 {
		return ""
	}

	return p.getTokenStringWithRanges(tokens)
}

// getTokenStringWithRanges renders tokens with range-aware styling.
func (p *Printer) getTokenStringWithRanges(tokens token.Tokens) string {
	var sb strings.Builder

	for _, tk := range tokens {
		lines := strings.Split(tk.Origin, "\n")
		tokenStyle := p.styleForToken(tk)
		offset := tokenValueOffset(tk)

		// Find content start index (first non-empty line).
		contentStartIdx := 0
		for i, l := range lines {
			if l != "" {
				contentStartIdx = i
				break
			}
		}

		// Calculate starting line for this token.
		// Leading empty lines in Origin don't advance the line number.
		lineNum := tk.Position.Line

		for idx, src := range lines {
			if idx > 0 {
				sb.WriteByte('\n')

				lineNum++
			}

			if src == "" {
				continue
			}

			// Determine starting column for this line.
			// For the content start line: Origin[0] is at column (Position.Column - offset).
			// For subsequent lines within a multi-line token: starts at column 1.
			var startCol int
			if idx == contentStartIdx {
				// Content start line: Origin[0] is at Position.Column - offset.
				// Offset chars of separator precede the token content at Position.Column.
				startCol = tk.Position.Column - offset
				if startCol < 1 {
					startCol = 1
				}
			} else {
				// Subsequent lines start at column 1.
				startCol = 1
			}

			// Handle separator whitespace for content start line.
			if idx == contentStartIdx && offset > 0 && offset < len(src) {
				if strings.TrimLeft(src[:offset], " \t") == "" {
					// Separator is at columns startCol through startCol+offset-1.
					// Token content starts at startCol+offset (== tk.Position.Column).
					sb.WriteString(p.styleLineWithRanges(src[:offset], lineNum, startCol, p.colorScheme.Default))

					// Content starts after separator at tk.Position.Column.
					sb.WriteString(p.styleLineWithRanges(src[offset:], lineNum, tk.Position.Column, tokenStyle))

					continue
				}
			}

			// No separator or validation failed: style entire line.
			sb.WriteString(p.styleLineWithRanges(src, lineNum, startCol, tokenStyle))
		}
	}

	return sb.String()
}

func (p *Printer) printBeforeTokens(tk *token.Token, minLine, extLine int) token.Tokens {
	for tk.Prev != nil {
		if tk.Prev.Position.Line < minLine {
			break
		}

		tk = tk.Prev
	}

	minTk := tk.Clone()
	if minTk.Prev != nil {
		// Add white spaces to minTk by prev token.
		prev := minTk.Prev
		whiteSpaceLen := len(prev.Origin) - len(strings.TrimRight(prev.Origin, " "))
		minTk.Origin = strings.Repeat(" ", whiteSpaceLen) + minTk.Origin
	}

	minTk.Origin = strings.TrimLeft(minTk.Origin, "\r\n")
	tokens := token.Tokens{minTk}

	tk = minTk.Next
	for tk != nil && tk.Position.Line <= extLine {
		clonedTk := tk.Clone()
		tokens.Add(clonedTk)

		tk = clonedTk.Next
	}

	lastTk := tokens[len(tokens)-1]
	trimmedOrigin := strings.TrimRight(lastTk.Origin, " \r\n")
	suffix := lastTk.Origin[len(trimmedOrigin):]
	lastTk.Origin = trimmedOrigin

	if lastTk.Next != nil && len(suffix) > 1 {
		next := lastTk.Next.Clone()
		// Add suffix to header of next token.
		if suffix[0] == '\n' || suffix[0] == '\r' {
			suffix = suffix[1:]
		}

		next.Origin = suffix + next.Origin
		lastTk.Next = next
	}

	return tokens
}

func (p *Printer) printAfterTokens(tk *token.Token, maxLine int) token.Tokens {
	tokens := token.Tokens{}
	if tk == nil {
		return tokens
	}
	if tk.Position.Line > maxLine {
		return tokens
	}

	minTk := tk.Clone()
	minTk.Origin = strings.TrimLeft(minTk.Origin, "\r\n")
	tokens.Add(minTk)

	tk = minTk.Next
	for tk != nil && tk.Position.Line <= maxLine {
		clonedTk := tk.Clone()
		tokens.Add(clonedTk)

		tk = clonedTk.Next
	}

	return tokens
}

func countNewlines(s string) int {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")

	return strings.Count(s, "\n")
}

func endsWithNewline(s string) bool {
	s = strings.TrimRight(s, " ")
	return strings.HasSuffix(s, "\n") || strings.HasSuffix(s, "\r")
}

// addLinePrefix prefixes each line with the given string.
func addLinePrefix(content, prefix string) string {
	lines := strings.Split(content, "\n")

	var sb strings.Builder

	for i, line := range lines {
		if i > 0 {
			sb.WriteByte('\n')
		}

		sb.WriteString(prefix)
		sb.WriteString(line)
	}

	return sb.String()
}
