package niceyaml

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/goccy/go-yaml/token"

	"github.com/macropower/niceyaml/internal/colors"
	"github.com/macropower/niceyaml/internal/styletree"
	"github.com/macropower/niceyaml/line"
	"github.com/macropower/niceyaml/position"
	"github.com/macropower/niceyaml/style"
	"github.com/macropower/niceyaml/style/theme"
	"github.com/macropower/niceyaml/tokens"
)

// StyleGetter retrieves styles by category.
// See [style.Styles] for an implementation.
type StyleGetter interface {
	Style(s style.Style) *lipgloss.Style
}

// TokenStyler manages style ranges for YAML tokens.
// See [Printer] for an implementation.
type TokenStyler interface {
	StyleGetter
	AddStyleToRange(s *lipgloss.Style, ranges ...position.Range)
	ClearStyles()
}

// StyledPrinter extends [TokenStyler] with printing capabilities.
// See [Printer] for an implementation.
type StyledPrinter interface {
	TokenStyler
	Print(lines LineIterator) string
}

// StyledSlicePrinter extends [TokenStyler] with slice printing capabilities.
// See [Printer] for an implementation.
type StyledSlicePrinter interface {
	TokenStyler
	PrintSlice(lines LineIterator, minLine, maxLine int) string
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
// Create instances with [NewPrinter].
type Printer struct {
	styles             StyleGetter
	style              lipgloss.Style
	gutterFunc         GutterFunc
	annotationFunc     AnnotationFunc
	rangeStyles        *styletree.Tree
	width              int
	hasCustomStyle     bool
	annotationsEnabled bool
	wordWrap           bool
}

// NewPrinter creates a new [Printer].
// By default it uses [theme.Charm], [DefaultGutter], and [DefaultAnnotation].
func NewPrinter(opts ...PrinterOption) *Printer {
	p := &Printer{
		styles:             theme.Charm(),
		gutterFunc:         DefaultGutter(),
		annotationFunc:     DefaultAnnotation(),
		annotationsEnabled: true,
		wordWrap:           true,
	}

	for _, opt := range opts {
		opt(p)
	}

	if !p.hasCustomStyle {
		p.style = p.styles.Style(style.Text).
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

// NoGutter is a [GutterFunc] that returns an empty string for all lines.
var NoGutter GutterFunc = func(GutterContext) string { return "" }

// AnnotationContext provides context for annotation rendering.
// It is passed to [AnnotationFunc] to determine the appropriate annotation content.
type AnnotationContext struct {
	Styles      StyleGetter
	Annotations line.Annotations
	Position    line.RelativePosition
}

// AnnotationFunc returns the rendered annotation content based on [AnnotationContext].
type AnnotationFunc func(AnnotationContext) string

// DefaultAnnotation creates an [AnnotationFunc] that renders annotations with
// position-based prefixes: "^ " for [line.Below], none for [line.Above].
func DefaultAnnotation() AnnotationFunc {
	return func(ctx AnnotationContext) string {
		anns := ctx.Annotations
		if len(anns) == 0 {
			return ""
		}

		// Find minimum column and collect content.
		minCol := anns[0].Col
		contents := make([]string, len(anns))

		for i, ann := range anns {
			contents[i] = ann.Content
			if ann.Col < minCol {
				minCol = ann.Col
			}
		}

		padding := strings.Repeat(" ", max(0, minCol))
		combined := strings.Join(contents, "; ")

		// Add "^ " prefix for Below annotations.
		if ctx.Position == line.Below {
			return padding + "^ " + combined
		}

		return padding + combined
	}
}

// DefaultGutter creates a [GutterFunc] that renders both line numbers and diff markers.
// This is the default gutter used by [NewPrinter].
func DefaultGutter() GutterFunc {
	return func(ctx GutterContext) string {
		lineNumStyle := ctx.Styles.Style(style.Text).
			Foreground(ctx.Styles.Style(style.Comment).GetForeground())

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
			marker = ctx.Styles.Style(style.GenericInserted).Render("+")
		case line.FlagDeleted:
			marker = ctx.Styles.Style(style.GenericDeleted).Render("-")
		default:
			marker = ctx.Styles.Style(style.Text).Render(" ")
		}

		// Use builder to avoid intermediate string allocation from concatenation.
		var sb strings.Builder
		sb.Grow(len(lineNum) + len(marker))
		sb.WriteString(lineNum)
		sb.WriteString(marker)

		return sb.String()
	}
}

// DiffGutter creates a [GutterFunc] that renders diff-style markers only (" ", "+", "-").
// No line numbers are rendered. Uses [style.GenericInserted] and [style.GenericDeleted] for styling.
func DiffGutter() GutterFunc {
	return func(ctx GutterContext) string {
		if ctx.Soft {
			return " "
		}

		switch ctx.Flag {
		case line.FlagInserted:
			return ctx.Styles.Style(style.GenericInserted).Render("+")
		case line.FlagDeleted:
			return ctx.Styles.Style(style.GenericDeleted).Render("-")
		default:
			return ctx.Styles.Style(style.Text).Render(" ")
		}
	}
}

// LineNumberGutter creates a [GutterFunc] that renders styled line numbers only.
// For soft-wrapped continuation lines, renders "   - " as a continuation marker.
// No diff markers are rendered. Uses [style.Comment] foreground for styling.
func LineNumberGutter() GutterFunc {
	return func(ctx GutterContext) string {
		lineNumStyle := ctx.Styles.Style(style.Text).
			Foreground(ctx.Styles.Style(style.Comment).GetForeground())

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

// WithAnnotationFunc sets the [AnnotationFunc] for rendering annotations.
// By default, [DefaultAnnotation] is used which adds "^ " prefix for [line.Below] annotations.
func WithAnnotationFunc(fn AnnotationFunc) PrinterOption {
	return func(p *Printer) {
		p.annotationFunc = fn
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

// SetWordWrap sets whether word wrapping is enabled. Defaults to true.
// Word wrapping requires a width to be set via [Printer.SetWidth].
func (p *Printer) SetWordWrap(enabled bool) {
	p.wordWrap = enabled
}

// AddStyleToRange adds a style to apply to the character range [r.Start, r.End).
// The range is half-open: Start is inclusive, End is exclusive.
// Line and column are 0-indexed.
// Overlapping range colors are blended; transforms are composed (overlay wraps base).
func (p *Printer) AddStyleToRange(s *lipgloss.Style, ranges ...position.Range) {
	ls := lipgloss.NewStyle()
	if s != nil {
		ls = *s
	}

	if p.rangeStyles == nil {
		p.rangeStyles = styletree.New()
	}

	for _, r := range ranges {
		start := r.Start.Line*maxCol + r.Start.Col
		end := r.End.Line*maxCol + r.End.Col
		p.rangeStyles.Insert(start, end, &ls)
	}
}

// Style retrieves the underlying [lipgloss.Style] for the given [style.Style].
func (p *Printer) Style(s style.Style) *lipgloss.Style {
	return p.styles.Style(s)
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

	totalLines := t.Len()

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

		var (
			hasAboveAnnotation bool
			hasBelowAnnotation bool
		)
		if p.annotationsEnabled {
			hasAboveAnnotation = !ln.Annotations.FilterPosition(line.Above).IsEmpty()
			hasBelowAnnotation = !ln.Annotations.FilterPosition(line.Below).IsEmpty()
		}

		if hasAboveAnnotation {
			// Add newline between hunks (not before first hunk).
			if renderedIdx > 0 {
				sb.WriteByte('\n')
			}

			// Render annotation above the line.
			p.renderAnnotation(&sb, ln, pos, lineNum, totalLines, line.Above)
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
			deleted := p.styles.Style(style.GenericDeleted)
			p.writeLine(&sb, ln.Content(), pos.Line, deleted, gutterCtx, gutterWidth)

		case line.FlagInserted:
			inserted := p.styles.Style(style.GenericInserted)
			p.writeLine(&sb, ln.Content(), pos.Line, inserted, gutterCtx, gutterWidth)

		default: // FlagDefault (equal line).
			// Render with syntax highlighting.
			styledContent := p.renderTokenLine(pos.Line, ln)
			p.writeLine(&sb, styledContent, pos.Line, nil, gutterCtx, gutterWidth)
		}

		if hasBelowAnnotation {
			sb.WriteByte('\n')
			p.renderAnnotation(&sb, ln, pos, lineNum, totalLines, line.Below)
		}

		renderedIdx++
	}

	return sb.String()
}

// renderAnnotation renders annotation lines with gutter padding for the given position.
// The annotation content uses [AnnotationFunc] for rendering.
func (p *Printer) renderAnnotation(
	sb *strings.Builder,
	ln line.Line,
	pos position.Position,
	lineNum, totalLines int,
	relPos line.RelativePosition,
) {
	gutterCtx := GutterContext{
		Index:      pos.Line,
		Number:     lineNum,
		TotalLines: totalLines,
		Soft:       false,
		Flag:       line.FlagAnnotation,
		Styles:     p.styles,
	}
	sb.WriteString(p.gutterFunc(gutterCtx))

	annCtx := AnnotationContext{
		Annotations: ln.Annotations.FilterPosition(relPos),
		Position:    relPos,
		Styles:      p.styles,
	}
	sb.WriteString(p.styles.Style(style.Comment).Render(p.annotationFunc(annCtx)))
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

		col += utf8.RuneCountInString(subLine)
	}
}

// styleLineWithRanges styles a line with range-aware styling.
// It splits the line into spans based on effective styles (base + overlapping ranges).
// The pos parameter specifies the 0-indexed visual line and column position.
// If alwaysBlend is true, range styles always blend with base (used for diff lines).
func (p *Printer) styleLineWithRanges(src string, pos position.Position, s *lipgloss.Style, alwaysBlend bool) string {
	if src == "" {
		return src
	}

	if p.rangeStyles == nil || p.rangeStyles.Len() == 0 {
		return s.Render(src)
	}

	// Query all intervals overlapping this line in one batch.
	lineStart := pos.Line*maxCol + pos.Col
	lineEnd := lineStart + utf8.RuneCountInString(src)

	intervals := p.rangeStyles.QueryRange(lineStart, lineEnd)
	if len(intervals) == 0 {
		return s.Render(src)
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
		// Boundaries always contains at least [lineStart, lineEnd] for non-empty src.
		return ""
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

		spanStyle := p.computeStyleForPoint(spanPoint, intervals, s, alwaysBlend)

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
				result = colors.OverrideStyles(result, iv.Style)
				firstRange = false
			} else {
				result = colors.BlendStyles(result, iv.Style)
			}
		}
	}

	return result
}

// contentWidth returns the available width for content after accounting for
// gutter width. Returns 0 if wrapping is disabled.
func (p *Printer) contentWidth(gutterWidth int) int {
	if !p.wordWrap || p.width <= 0 {
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
		tokenStyle := p.styles.Style(tokens.TypeStyle(tk))
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
			defaultStyle := p.styles.Style(style.Text)
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

	return utf8.RuneCountInString(prefix)
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

// stylesEqual compares two styles for equality (for span grouping purposes).
// Two styles are equal if they produce the same visual output.
func stylesEqual(s1, s2 *lipgloss.Style) bool {
	// Compare rendered output of a test string.
	// This is a pragmatic approach since lipgloss doesn't expose style internals.
	const testStr = "x"
	return s1.Render(testStr) == s2.Render(testStr)
}
