package niceyaml

import (
	"fmt"
	"iter"
	"slices"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/goccy/go-yaml/token"

	"github.com/macropower/niceyaml/internal/styletree"
	"github.com/macropower/niceyaml/line"
	"github.com/macropower/niceyaml/position"
)

// LineIterator provides line-by-line access to YAML tokens for rendering.
type LineIterator interface {
	Lines() iter.Seq2[position.Position, line.Line]
	Count() int
	IsEmpty() bool
}

const (
	wrapOnCharacters = " /-"

	// MaxCol is the maximum column value used for linearizing 2D positions.
	// This allows positions to be compared as single integers while preserving ordering.
	maxCol = 1_000_000
)

// Printer renders YAML tokens with syntax highlighting using [lipgloss.Style].
// It supports custom styles, gutters, and styled range overlays
// for highlighting specific positions such as errors.
type Printer struct {
	styles             StyleGetter
	style              lipgloss.Style
	gutterFunc         GutterFunc
	rangeStyles        *styletree.Tree
	width              int
	hasCustomStyle     bool
	annotationsEnabled bool
}

// NewPrinter creates a new [Printer].
// By default it uses [DefaultStyles] and [DefaultGutter].
func NewPrinter(opts ...PrinterOption) *Printer {
	p := &Printer{
		styles:             DefaultStyles(),
		gutterFunc:         DefaultGutter(),
		annotationsEnabled: true,
	}

	for _, opt := range opts {
		opt(p)
	}

	if !p.hasCustomStyle {
		p.style = p.styles.GetStyle(StyleDefault).
			PaddingRight(1)
	}

	return p
}

// PrinterOption configures a [Printer].
type PrinterOption func(*Printer)

// GutterContext provides context about the current line for gutter rendering.
// It is passed to [GutterFunc] to determine the appropriate gutter content.
type GutterContext struct {
	Styles     StyleGetter
	Index      int
	Number     int
	TotalLines int
	Flag       line.Flag
	Soft       bool
}

// GutterFunc returns the gutter content for a line based on [GutterContext].
// The returned string is rendered as the leftmost content before the line content.
type GutterFunc func(GutterContext) string

// NoGutter returns an empty gutter for all lines.
var NoGutter GutterFunc = func(GutterContext) string { return "" }

// DefaultGutter returns a gutter with both line numbers and diff markers.
// This is the default gutter used by [NewPrinter].
func DefaultGutter() GutterFunc {
	return func(ctx GutterContext) string {
		lineNumStyle := ctx.Styles.GetStyle(StyleDefault).
			Foreground(ctx.Styles.GetStyle(StyleComment).GetForeground())

		var lineNum string

		switch {
		case ctx.Flag == line.FlagAnnotation:
			lineNum = lineNumStyle.Render("     ")
		case ctx.Soft:
			lineNum = lineNumStyle.Render("   - ")
		default:
			lineNum = lineNumStyle.Render(fmt.Sprintf("%4d ", ctx.Number))
		}

		var marker string

		switch ctx.Flag {
		case line.FlagInserted:
			marker = ctx.Styles.GetStyle(StyleDiffInserted).Render("+")
		case line.FlagDeleted:
			marker = ctx.Styles.GetStyle(StyleDiffDeleted).Render("-")
		default:
			marker = ctx.Styles.GetStyle(StyleDefault).Render(" ")
		}

		// Use builder to avoid intermediate string allocation from concatenation.
		var sb strings.Builder
		sb.Grow(len(lineNum) + len(marker))
		sb.WriteString(lineNum)
		sb.WriteString(marker)

		return sb.String()
	}
}

// DiffGutter returns a gutter with diff-style markers only (" ", "+", "-").
// No line numbers are rendered. Uses [StyleDiffInserted] and [StyleDiffDeleted] for styling.
func DiffGutter() GutterFunc {
	return func(ctx GutterContext) string {
		if ctx.Soft {
			return " "
		}

		switch ctx.Flag {
		case line.FlagInserted:
			return ctx.Styles.GetStyle(StyleDiffInserted).Render("+")
		case line.FlagDeleted:
			return ctx.Styles.GetStyle(StyleDiffDeleted).Render("-")
		default:
			return ctx.Styles.GetStyle(StyleDefault).Render(" ")
		}
	}
}

// LineNumberGutter returns a gutter with styled line numbers only.
// For soft-wrapped continuation lines, renders "   - " as a continuation marker.
// No diff markers are rendered. Uses [StyleComment] foreground for styling.
func LineNumberGutter() GutterFunc {
	return func(ctx GutterContext) string {
		lineNumStyle := ctx.Styles.GetStyle(StyleDefault).
			Foreground(ctx.Styles.GetStyle(StyleComment).GetForeground())

		switch {
		case ctx.Flag == line.FlagAnnotation:
			return lineNumStyle.Render("     ")
		case ctx.Soft:
			return lineNumStyle.Render("   - ")
		default:
			return lineNumStyle.Render(fmt.Sprintf("%4d ", ctx.Number))
		}
	}
}

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

// WithGutter sets the [GutterFunc] for rendering.
// Pass [NoGutter] to disable gutters entirely, or [DiffGutter] for diff markers only.
// By default, [DefaultGutter] is used which renders line numbers and diff markers.
func WithGutter(fn GutterFunc) PrinterOption {
	return func(p *Printer) {
		p.gutterFunc = fn
	}
}

// SetWidth sets the width for word wrapping. A width of 0 disables wrapping.
func (p *Printer) SetWidth(width int) {
	p.width = width
}

// SetAnnotationsEnabled sets whether annotations are rendered. Defaults to true.
func (p *Printer) SetAnnotationsEnabled(enabled bool) {
	p.annotationsEnabled = enabled
}

// AddStyleToRange adds a style to apply to the character range [r.Start, r.End).
// The range is half-open: Start is inclusive, End is exclusive.
// Line and column are 0-indexed.
// Overlapping range colors are blended; transforms are composed (overlay wraps base).
func (p *Printer) AddStyleToRange(s *lipgloss.Style, r position.Range) {
	style := lipgloss.NewStyle()
	if s != nil {
		style = *s
	}

	if p.rangeStyles == nil {
		p.rangeStyles = styletree.New()
	}

	start := r.Start.Line*maxCol + r.Start.Col
	end := r.End.Line*maxCol + r.End.Col
	p.rangeStyles.Insert(start, end, &style)
}

// GetStyle retrieves the underlying [lipgloss.Style] for the given [Style].
func (p *Printer) GetStyle(s Style) *lipgloss.Style {
	return p.styles.GetStyle(s)
}

// ClearStyles removes all previously added styles.
func (p *Printer) ClearStyles() {
	if p.rangeStyles != nil {
		p.rangeStyles.Clear()
	}
}

// Print prints any [LineIterator].
func (p *Printer) Print(lines LineIterator) string {
	content := p.renderLinesInRange(lines, -1, -1)

	return p.style.Render(content)
}

// PrintSlice prints a slice of lines from any [LineIterator].
// It prints in the range [minLine, maxLine] as defined by the [LineIterator.Lines] index.
// If minLine < 0, includes from the beginning; if maxLine < 0, includes to the end.
func (p *Printer) PrintSlice(lines LineIterator, minLine, maxLine int) string {
	content := p.renderLinesInRange(lines, minLine, maxLine)

	return p.style.Render(content)
}

// renderLinesInRange renders lines in [minLine, maxLine] using 0-indexed line indices.
// If minLine < 0, includes from the beginning; if maxLine < 0, includes to the end.
func (p *Printer) renderLinesInRange(t LineIterator, minLine, maxLine int) string {
	if t.IsEmpty() {
		return ""
	}

	totalLines := t.Count()

	// Pre-compute gutter width once for consistent wrapping calculations.
	var gutterWidth int
	if p.gutterFunc != nil {
		sampleGutter := p.gutterFunc(GutterContext{Styles: p.styles, TotalLines: totalLines})
		gutterWidth = lipgloss.Width(sampleGutter)
	}

	var (
		sb          strings.Builder
		renderedIdx int
	)

	// Pre-allocate buffer for estimated output size (reduces growth allocations).
	sb.Grow(totalLines * 100)

	for pos, ln := range t.Lines() {
		lineNum := ln.Number()

		// Filter by 0-indexed line index.
		if (minLine >= 0 && pos.Line < minLine) || (maxLine >= 0 && pos.Line > maxLine) {
			continue
		}

		hasAnnotation := p.annotationsEnabled && ln.Annotation.Content != ""

		if hasAnnotation {
			// Add newline between hunks (not before first hunk).
			if renderedIdx > 0 {
				sb.WriteByte('\n')
			}

			// Render hunk header with gutter padding.
			headerCtx := GutterContext{
				Index:      pos.Line,
				Number:     lineNum,
				TotalLines: totalLines,
				Soft:       false,
				Flag:       line.FlagAnnotation,
				Styles:     p.styles,
			}
			sb.WriteString(p.gutterFunc(headerCtx))
			sb.WriteString(p.styles.GetStyle(StyleComment).Render(ln.Annotation.Content))
			sb.WriteByte('\n')
		} else if renderedIdx > 0 {
			// Add newline between lines within a hunk.
			sb.WriteByte('\n')
		}

		gutterCtx := GutterContext{
			Index:      pos.Line,
			Number:     lineNum,
			TotalLines: totalLines,
			Soft:       false,
			Flag:       ln.Flag,
			Styles:     p.styles,
		}

		switch ln.Flag {
		case line.FlagDeleted:
			deleted := p.styles.GetStyle(StyleDiffDeleted)
			p.writeLine(&sb, ln.Content(), pos.Line, deleted, gutterCtx, gutterWidth)

		case line.FlagInserted:
			inserted := p.styles.GetStyle(StyleDiffInserted)
			p.writeLine(&sb, ln.Content(), pos.Line, inserted, gutterCtx, gutterWidth)

		default: // FlagDefault (equal line).
			// Render with syntax highlighting.
			styledContent := p.renderTokenLine(pos.Line, ln)
			p.writeLine(&sb, styledContent, pos.Line, nil, gutterCtx, gutterWidth)
		}

		renderedIdx++
	}

	return sb.String()
}

// writeLine writes a line with optional word wrapping.
// The gutter is generated at write-time for each segment using gutterCtx.
// The gutterWidth parameter is pre-computed once per render pass for efficiency.
func (p *Printer) writeLine(
	sb *strings.Builder,
	content string,
	visualLine int,
	contentStyle *lipgloss.Style,
	gutterCtx GutterContext,
	gutterWidth int,
) {
	cw := p.contentWidth(gutterWidth)

	// Treat non-wrapping as wrapping with a single subLine.
	subLines := []string{content}
	if cw > 0 {
		subLines = strings.Split(lipgloss.Wrap(content, cw, wrapOnCharacters), "\n")
	}

	// Track column offset for wrapped lines (0-indexed).
	col := 0

	for j, subLine := range subLines {
		if j > 0 {
			sb.WriteString("\n")
		}

		// Generate gutter at write-time with correct Soft flag.
		ctx := gutterCtx
		ctx.Soft = j > 0
		gutter := p.gutterFunc(ctx)
		sb.WriteString(gutter)

		// Write content.
		if contentStyle != nil {
			// For diff lines: apply diff style to content.
			sb.WriteString(p.styleLineWithRanges(subLine, position.New(visualLine, col), contentStyle, true))
		} else {
			// For equal lines: content is already styled.
			sb.WriteString(subLine)
		}

		col += len([]rune(subLine))
	}
}

// styleForPosition returns the effective style for a character at pos,
// applying range styles to the base style.
// If alwaysBlend is false, the first matching range overrides the base style;
// subsequent ranges blend. If alwaysBlend is true, all ranges blend with base.
// The pos parameter uses 0-indexed line and column values.
func (p *Printer) styleForPosition(pos position.Position, style *lipgloss.Style, alwaysBlend bool) *lipgloss.Style {
	if p.rangeStyles == nil || p.rangeStyles.Len() == 0 {
		return style
	}

	point := pos.Line*maxCol + pos.Col
	matches := p.rangeStyles.Query(point)

	firstRange := true
	for i := range matches {
		if !alwaysBlend && firstRange {
			style = overrideStyles(style, matches[i])
			firstRange = false
		} else {
			style = blendStyles(style, matches[i])
		}
	}

	return style
}

func (p *Printer) styleForToken(tk *token.Token) *lipgloss.Style {
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
// The pos parameter specifies the 0-indexed visual line and column position.
// If alwaysBlend is true, range styles always blend with base (used for diff lines).
func (p *Printer) styleLineWithRanges(
	src string,
	pos position.Position,
	style *lipgloss.Style,
	alwaysBlend bool,
) string {
	if src == "" {
		return src
	}

	if p.rangeStyles == nil || p.rangeStyles.Len() == 0 {
		return style.Render(src)
	}

	// Query all intervals overlapping this line in one batch.
	lineStart := pos.Line*maxCol + pos.Col
	lineEnd := lineStart + utf8.RuneCountInString(src)

	intervals := p.rangeStyles.QueryRange(lineStart, lineEnd)
	if len(intervals) == 0 {
		return style.Render(src)
	}

	// Collect all boundary points where styles might change.
	// These are interval starts/ends clamped to the line range.
	boundaries := make([]int, 0, len(intervals)*2+2)
	boundaries = append(boundaries, lineStart, lineEnd)

	for _, iv := range intervals {
		if iv.Start > lineStart && iv.Start < lineEnd {
			boundaries = append(boundaries, iv.Start)
		}
		if iv.End > lineStart && iv.End < lineEnd {
			boundaries = append(boundaries, iv.End)
		}
	}

	// Sort and deduplicate boundaries.
	slices.Sort(boundaries)

	boundaries = slices.Compact(boundaries)

	if len(boundaries) < 2 {
		// No actual span breaks; render entire line with base style + first interval.
		return p.styleForPosition(pos, style, alwaysBlend).Render(src)
	}

	var sb strings.Builder
	sb.Grow(len(src) * 2)

	runes := []rune(src)

	// Render spans between consecutive boundaries, merging adjacent spans with same style.
	var currentStyle *lipgloss.Style

	spanStart := 0

	for i := range len(boundaries) - 1 {
		boundaryStart := boundaries[i] - lineStart // Convert to rune index.
		boundaryEnd := boundaries[i+1] - lineStart // Convert to rune index.
		spanPoint := boundaries[i]                 // Point for style lookup.

		if boundaryStart < 0 || boundaryEnd > len(runes) || boundaryStart >= boundaryEnd {
			continue
		}

		spanStyle := p.computeStyleForPoint(spanPoint, intervals, style, alwaysBlend)

		// Merge adjacent spans with the same style.
		if currentStyle == nil {
			currentStyle = spanStyle
			spanStart = boundaryStart
		} else if !stylesEqual(currentStyle, spanStyle) {
			// Style changed - flush current span.
			sb.WriteString(currentStyle.Render(string(runes[spanStart:boundaryStart])))

			currentStyle = spanStyle
			spanStart = boundaryStart
		}
		// If styles are equal, continue accumulating the span.
	}

	// Flush remaining content.
	if currentStyle != nil && spanStart < len(runes) {
		sb.WriteString(currentStyle.Render(string(runes[spanStart:])))
	}

	return sb.String()
}

// computeStyleForPoint computes the effective style at a point given overlapping intervals.
// This avoids repeated tree queries by using pre-fetched intervals.
func (p *Printer) computeStyleForPoint(
	point int,
	intervals []styletree.Interval,
	baseStyle *lipgloss.Style,
	alwaysBlend bool,
) *lipgloss.Style {
	result := baseStyle
	firstRange := true

	for _, iv := range intervals {
		// Check if this interval contains the point.
		if point >= iv.Start && point < iv.End {
			if !alwaysBlend && firstRange {
				result = overrideStyles(result, iv.Style)
				firstRange = false
			} else {
				result = blendStyles(result, iv.Style)
			}
		}
	}

	return result
}

// contentWidth returns the available width for content after accounting for
// gutter width. Returns 0 if wrapping is disabled.
func (p *Printer) contentWidth(gutterWidth int) int {
	if p.width <= 0 {
		return 0
	}

	return max(0, p.width-gutterWidth)
}

// renderTokenLine renders a single line's tokens with syntax highlighting.
// It handles separator (leading whitespace) and content styling, plus range overlays.
// The lineIndex parameter is the 0-indexed position in the Lines collection.
func (p *Printer) renderTokenLine(lineIndex int, ln line.Line) string {
	if ln.IsEmpty() {
		return ""
	}

	col := 0 // Column position, 0-indexed.

	var sb strings.Builder

	for _, tk := range ln.Tokens() {
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
			sb.WriteString(p.styleLineWithRanges(sepPart, position.New(lineIndex, col), defaultStyle, false))

			col += separatorRunes
			originRunes = originRunes[separatorRunes:]
		}

		// Part 2: Render content portion (token style).
		if len(originRunes) > 0 {
			sb.WriteString(p.styleLineWithRanges(string(originRunes), position.New(lineIndex, col), tokenStyle, false))

			col += len(originRunes)
		}
	}

	return sb.String()
}

// leadingWhitespaceRunes returns the number of runes in the leading whitespace
// portion of s, up to maxBytes. Returns 0 if maxBytes is invalid or if the
// prefix contains non-whitespace characters.
func leadingWhitespaceRunes(s string, maxBytes int) int {
	if maxBytes <= 0 || maxBytes > len(s) {
		return 0
	}

	prefix := s[:maxBytes]
	if strings.TrimLeft(prefix, " \t") != "" {
		return 0
	}

	return len([]rune(prefix))
}

// tokenValueOffset calculates the byte offset where Value starts within the
// first non-empty line of the token's Origin. This offset is used for string
// slicing operations.
func tokenValueOffset(tk *token.Token) int {
	lines := strings.SplitSeq(tk.Origin, "\n")
	for l := range lines {
		if l != "" {
			idx := strings.Index(l, tk.Value)
			if idx >= 0 {
				return idx
			}

			break
		}
	}

	return 0
}
