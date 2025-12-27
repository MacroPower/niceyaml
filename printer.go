package niceyaml

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/goccy/go-yaml/token"
)

// LineIterator provides line-by-line access to YAML tokens for rendering.
type LineIterator interface {
	EachLine(fn func(idx int, line Line))
	IsEmpty() bool
	TokenPositions(lineNum, col int) []*token.Position
}

const wrapOnCharacters = " /-"

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

// PrintTokens prints any [LineIterator].
func (p *Printer) PrintTokens(lines LineIterator) string {
	content := p.renderLines(lines, true)

	return p.style.Render(content)
}

// PrintErrorToken prints the tokens around the error token with context.
// Returns the formatted string and the starting line number.
func (p *Printer) PrintErrorToken(tk *token.Token, lines int) (string, int) {
	curLine := tk.Position.Line

	// Collect all tokens and slice to the range.
	t := NewLinesFromToken(tk)
	pos := t.TokenPositions(curLine, tk.Position.Column)

	// Always include the current token's content.
	minLine, maxLine := curLine, curLine
	for _, p := range pos {
		if p.Line < minLine {
			minLine = p.Line
		}
		if p.Line > maxLine {
			maxLine = p.Line
		}
	}

	// Expand bounds by context lines.
	minLine = max(1, minLine-lines)
	maxLine = max(minLine, maxLine+lines)

	sliced := t.Slice(minLine, maxLine)
	content := p.renderLines(sliced, true)

	return p.style.Render(content), minLine
}

// renderLines renders a [LineIterator] line by line with syntax highlighting.
//
//nolint:unparam // showAnnotations kept for API flexibility.
func (p *Printer) renderLines(t LineIterator, showAnnotations bool) string {
	if t.IsEmpty() {
		return ""
	}

	// Expand token styles across joined lines before rendering.
	p.expandTokenStylesForJoins(t)

	var sb strings.Builder

	t.EachLine(func(i int, line Line) {
		hasAnnotation := showAnnotations && line.Annotation.Content != ""

		if hasAnnotation {
			// Add newline between hunks (not before first hunk).
			if i > 0 {
				sb.WriteByte('\n')
			}

			// Render hunk header.
			if p.lineNumbers {
				sb.WriteString(p.lineNumberStyle.Render("    "))
			}

			sb.WriteString(p.styles.GetStyle(StyleComment).Render(line.Annotation.Content))
			sb.WriteByte('\n')
		} else if i > 0 {
			// Add newline between lines within a hunk.
			sb.WriteByte('\n')
		}

		lineNum := line.Number()

		switch line.Flag {
		case FlagDeleted:
			deleted := p.styles.GetStyle(StyleDiffDeleted)
			p.writeLine(&sb, p.lineDeletedPrefix, line.Content(), lineNum, deleted, true)

		case FlagInserted:
			inserted := p.styles.GetStyle(StyleDiffInserted)
			p.writeLine(&sb, p.lineInsertedPrefix, line.Content(), lineNum, inserted, false)

		default: // FlagDefault (equal line).
			// Render with syntax highlighting.
			styledContent := p.renderTokenLine(line)
			p.writeLine(&sb, p.linePrefix, styledContent, lineNum, nil, false)
		}
	})

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
		switch {
		// For diff lines: apply diff style to prefix.
		case contentStyle != nil:
			sb.WriteString(contentStyle.Render(pfx))
			if skipRangeStyles {
				// For deleted lines: skip range highlights (they apply to "after" tokens only).
				sb.WriteString(contentStyle.Render(cnt))
			} else {
				// For inserted lines: blend diff style with range highlights.
				sb.WriteString(p.styleLineWithRanges(cnt, lineNum, col, contentStyle, true))
			}

		// For equal lines with line numbers: style the prefix with StyleDefault.
		case p.lineNumbers:
			sb.WriteString(p.styles.GetStyle(StyleDefault).Render(pfx))
			sb.WriteString(cnt)

		// For equal lines without line numbers: use raw prefix.
		default:
			sb.WriteString(pfx)
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

// expandTokenStylesForJoins expands token styles to cover all lines that are
// part of the same split token. When a token is split across multiple lines
// (indicated by JoinPrev/JoinNext flags), highlighting one part should
// highlight all connected parts.
func (p *Printer) expandTokenStylesForJoins(t LineIterator) {
	if len(p.tokenStyles) == 0 || t.IsEmpty() {
		return
	}

	var newStyles []*tokenStyle

	for _, ts := range p.tokenStyles {
		for _, pos := range t.TokenPositions(ts.pos.Line, ts.pos.Col) {
			newStyles = append(newStyles, &tokenStyle{
				style: ts.style,
				pos:   Position{Line: pos.Line, Col: pos.Column},
			})
		}
	}

	p.tokenStyles = append(p.tokenStyles, newStyles...)
}

// renderTokenLine renders a single line's tokens with syntax highlighting.
// It handles separator (leading whitespace) and content styling, plus range overlays.
func (p *Printer) renderTokenLine(line Line) string {
	if line.IsEmpty() {
		return ""
	}

	lineNum := line.Number()
	col := 1 // Column position, 1-indexed.

	var sb strings.Builder

	for _, tk := range line.Tokens() {
		tokenStyle := p.styleForToken(tk)
		valueOffset := tokenValueOffset(tk)

		// Get the token's origin text.
		origin := tk.Origin
		// Strip trailing newline - we add newlines between lines in renderLines.
		origin = strings.TrimSuffix(origin, "\n")
		originRunes := []rune(origin)

		// Calculate separator (leading whitespace before value).
		separatorRunes := leadingWhitespaceRunes(origin, valueOffset)

		// Part 1: Render separator portion (default style).
		if separatorRunes > 0 && separatorRunes <= len(originRunes) {
			sepPart := string(originRunes[:separatorRunes])
			defaultStyle := p.styles.GetStyle(StyleDefault)
			sb.WriteString(p.styleLineWithRanges(sepPart, lineNum, col, defaultStyle, false))

			col += separatorRunes
			originRunes = originRunes[separatorRunes:]
		}

		// Part 2: Render content portion (token style).
		if len(originRunes) > 0 {
			sb.WriteString(p.styleLineWithRanges(string(originRunes), lineNum, col, tokenStyle, false))

			col += len(originRunes)
		}
	}

	return sb.String()
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
