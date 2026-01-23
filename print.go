package niceyaml

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"

	"github.com/macropower/niceyaml/internal/ansi"
	"github.com/macropower/niceyaml/internal/colors"
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

// StyledPrinter provides printing capabilities with style support.
// See [Printer] for an implementation.
type StyledPrinter interface {
	Style(s style.Style) *lipgloss.Style
	SetWidth(width int)
	Print(lines LineIterator, spans ...position.Span) string
}

const wrapOnCharacters = " /-"

// NoGutter is a [GutterFunc] that returns an empty string for all lines.
var NoGutter GutterFunc = func(GutterContext) string { return "" }

// Printer renders YAML tokens with syntax highlighting using [lipgloss.Style].
// It supports custom styles, gutters, and styled overlays
// for highlighting specific positions such as errors.
// Create instances with [NewPrinter].
type Printer struct {
	styles             StyleGetter
	style              lipgloss.Style
	gutterFunc         GutterFunc
	annotationFunc     AnnotationFunc
	blender            *colors.Blender
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
		blender:            colors.NewBlender(),
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
		if len(ctx.Annotations) == 0 {
			return ""
		}

		padding := strings.Repeat(" ", max(0, ctx.Annotations.Col()))
		combined := strings.Join(ctx.Annotations.Contents(), "; ")

		// Add "^ " prefix for Below annotations.
		if ctx.Position == line.Below {
			return padding + "^ " + combined
		}

		return padding + combined
	}
}

// renderLineNumber renders the line number portion of a gutter.
func renderLineNumber(ctx GutterContext) string {
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

// renderDiffMarker renders the diff marker portion of a gutter.
func renderDiffMarker(ctx GutterContext) string {
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

// DefaultGutter creates a [GutterFunc] that renders both line numbers and diff markers.
// This is the default gutter used by [NewPrinter].
func DefaultGutter() GutterFunc {
	return func(ctx GutterContext) string {
		return renderLineNumber(ctx) + renderDiffMarker(ctx)
	}
}

// DiffGutter creates a [GutterFunc] that renders diff-style markers only (" ", "+", "-").
// No line numbers are rendered. Uses [style.GenericInserted] and [style.GenericDeleted] for styling.
func DiffGutter() GutterFunc {
	return renderDiffMarker
}

// LineNumberGutter creates a [GutterFunc] that renders styled line numbers only.
// For soft-wrapped continuation lines, renders "   - " as a continuation marker.
// No diff markers are rendered. Uses [style.Comment] foreground for styling.
func LineNumberGutter() GutterFunc {
	return renderLineNumber
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

// Style retrieves the underlying [*lipgloss.Style] for the given [style.Style],
// or an empty style if not found.
func (p *Printer) Style(s style.Style) *lipgloss.Style {
	return p.styles.Style(s)
}

// Print prints any [LineIterator].
// It prints lines within the given [position.Span]s, in the supplied order.
// If no [position.Span]s are provided, all lines are printed.
func (p *Printer) Print(lines LineIterator, spans ...position.Span) string {
	if len(spans) == 0 {
		// No spans specified, print all lines.
		content := p.renderLinesInSpan(lines, position.NewSpan(0, lines.Len()))

		return p.style.Render(content)
	}

	var sb strings.Builder

	for i, span := range spans {
		if i > 0 {
			sb.WriteByte('\n')
		}

		sb.WriteString(p.renderLinesInSpan(lines, span))
	}

	return p.style.Render(sb.String())
}

// renderLinesInSpan renders lines in the half-open span [span.Start, span.End).
func (p *Printer) renderLinesInSpan(t LineIterator, span position.Span) string {
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

	// Cache styles outside the loop to avoid repeated lookups.
	deletedStyle := p.styles.Style(style.GenericDeleted)
	insertedStyle := p.styles.Style(style.GenericInserted)

	for pos, ln := range t.Lines() {
		lineNum := ln.Number()

		// Filter by 0-indexed line index using half-open span [Start, End).
		if !span.Contains(pos.Line) {
			continue
		}

		var (
			hasAboveAnnotation bool
			hasBelowAnnotation bool
		)
		if p.annotationsEnabled {
			hasAboveAnnotation = len(ln.Annotations.FilterPosition(line.Above)) > 0
			hasBelowAnnotation = len(ln.Annotations.FilterPosition(line.Below)) > 0
		}

		if hasAboveAnnotation {
			// Add newline between hunks (not before first hunk).
			if renderedIdx > 0 {
				sb.WriteByte('\n')
			}

			// Render annotation above the line.
			p.renderAnnotation(&sb, ln, pos, lineNum, totalLines, line.Above, gutterWidth)
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

		linePos := position.New(pos.Line, 0)

		var (
			content      string
			contentStyle *lipgloss.Style
		)

		switch ln.Flag {
		case line.FlagDeleted:
			content = ln.Content()
			contentStyle = deletedStyle

		case line.FlagInserted:
			content = ln.Content()
			contentStyle = insertedStyle

		default: // FlagDefault (equal line).
			// Render with syntax highlighting.
			content = p.renderTokenLine(pos.Line, ln)
			contentStyle = nil
		}

		p.writeLine(&sb, content, linePos, contentStyle, gutterCtx, gutterWidth)

		if hasBelowAnnotation {
			sb.WriteByte('\n')
			p.renderAnnotation(&sb, ln, pos, lineNum, totalLines, line.Below, gutterWidth)
		}

		renderedIdx++
	}

	return sb.String()
}

// renderAnnotation renders annotation lines with gutter padding for the given position.
// The annotation content uses [AnnotationFunc] for rendering.
// The gutterWidth parameter enables width calculation for wrapping.
func (p *Printer) renderAnnotation(
	sb *strings.Builder,
	ln line.Line,
	pos position.Position,
	lineNum, totalLines int,
	relPos line.RelativePosition,
	gutterWidth int,
) {
	anns := ln.Annotations.FilterPosition(relPos)
	if len(anns) == 0 {
		return
	}

	annCtx := AnnotationContext{
		Annotations: anns,
		Position:    relPos,
		Styles:      p.styles,
	}
	content := p.annotationFunc(annCtx)
	if content == "" {
		return
	}

	subLines := p.wrapContent(content, gutterWidth)

	// Calculate continuation padding for wrapped lines.
	// For Below annotations: col spaces + "^ " = col + 2.
	// For Above annotations: col spaces.
	continuationPadding := strings.Repeat(" ", anns.Col())
	if relPos == line.Below {
		continuationPadding += "  " // Align with text after "^ ".
	}

	for j, subLine := range subLines {
		if j > 0 {
			sb.WriteByte('\n')
		}

		gutterCtx := GutterContext{
			Index:      pos.Line,
			Number:     lineNum,
			TotalLines: totalLines,
			Soft:       j > 0,
			Flag:       line.FlagAnnotation,
			Styles:     p.styles,
		}
		sb.WriteString(p.gutterFunc(gutterCtx))

		// Add continuation padding for wrapped lines.
		if j > 0 {
			sb.WriteString(p.styles.Style(style.Comment).Render(continuationPadding))
		}

		sb.WriteString(p.styles.Style(style.Comment).Render(ansi.Escape(subLine)))
	}
}

// writeLine writes a line with optional word wrapping.
// The gutter is generated at write-time for each segment using gutterCtx.
// The gutterWidth parameter is pre-computed once per render pass for efficiency.
func (p *Printer) writeLine(
	sb *strings.Builder,
	content string,
	pos position.Position,
	contentStyle *lipgloss.Style,
	gutterCtx GutterContext,
	gutterWidth int,
) {
	subLines := p.wrapContent(content, gutterWidth)

	for j, subLine := range subLines {
		if j > 0 {
			sb.WriteByte('\n')
		}

		// Generate gutter at write-time with correct Soft flag.
		ctx := gutterCtx
		ctx.Soft = j > 0
		gutter := p.gutterFunc(ctx)
		sb.WriteString(gutter)

		// Write content.
		if contentStyle != nil {
			// For diff lines: apply diff style to content.
			sb.WriteString(p.styleLineWithRanges(subLine, pos, contentStyle, true, nil))
		} else {
			// For equal lines: content is already styled.
			sb.WriteString(subLine)
		}

		pos.Col += utf8.RuneCountInString(subLine)
	}
}

// styleLineWithRanges styles a line with range-aware styling.
// It splits the line into spans based on effective styles (base + overlapping ranges).
// The pos parameter specifies the 0-indexed visual line and column position.
// If alwaysBlend is true, range styles always blend with base (used for diff lines).
// The overlays parameter provides style overlays from Line; pass nil if none.
func (p *Printer) styleLineWithRanges(
	src string,
	pos position.Position,
	s *lipgloss.Style,
	alwaysBlend bool,
	overlays line.Overlays,
) string {
	if src == "" {
		return src
	}

	if len(overlays) == 0 {
		return s.Render(ansi.Escape(src))
	}

	// Create span for this line segment's column range.
	cols := position.NewSpan(pos.Col, pos.Col+utf8.RuneCountInString(src))

	// Filter overlays that overlap this column span with resolved styles.
	var active []overlayWithStyle
	for _, o := range overlays {
		if o.Cols.Overlaps(cols) {
			if st := p.styles.Style(o.Kind); st != nil {
				active = append(active, overlayWithStyle{
					cols:  o.Cols,
					style: st,
				})
			}
		}
	}

	if len(active) == 0 {
		return s.Render(ansi.Escape(src))
	}

	boundaries := computeStyleBoundaries(active, cols)
	if len(boundaries) < 2 {
		return ""
	}

	// Render spans between boundaries, merging adjacent same-styled spans.
	var sb strings.Builder
	sb.Grow(len(src) * 2)

	runes := []rune(src)

	var currentStyle *lipgloss.Style

	spanStart := 0

	for i := range len(boundaries) - 1 {
		boundaryStart := boundaries[i] - cols.Start // Convert to rune index.
		boundaryEnd := boundaries[i+1] - cols.Start // Convert to rune index.
		spanPoint := boundaries[i]                  // Point for style lookup.

		if boundaryStart < 0 || boundaryEnd > len(runes) || boundaryStart >= boundaryEnd {
			continue
		}

		spanStyle := p.computeStyleForPoint(spanPoint, active, s, alwaysBlend)

		// Merge adjacent spans with the same style.
		if currentStyle == nil {
			currentStyle = spanStyle
			spanStart = boundaryStart
		} else if currentStyle != spanStyle {
			// Style changed - flush current span.
			sb.WriteString(currentStyle.Render(ansi.Escape(string(runes[spanStart:boundaryStart]))))

			currentStyle = spanStyle
			spanStart = boundaryStart
		}
		// If styles are equal, continue accumulating the span.
	}

	// Flush remaining content.
	if currentStyle != nil && spanStart < len(runes) {
		sb.WriteString(currentStyle.Render(ansi.Escape(string(runes[spanStart:]))))
	}

	return sb.String()
}

// computeStyleBoundaries returns sorted, deduplicated boundary points where styles change.
// The cols span defines the line segment boundaries.
func computeStyleBoundaries(active []overlayWithStyle, cols position.Span) []int {
	boundaries := make([]int, 0, len(active)*2+2)
	boundaries = append(boundaries, cols.Start, cols.End)

	for _, ov := range active {
		if ov.cols.Start > cols.Start && ov.cols.Start < cols.End {
			boundaries = append(boundaries, ov.cols.Start)
		}
		if ov.cols.End > cols.Start && ov.cols.End < cols.End {
			boundaries = append(boundaries, ov.cols.End)
		}
	}

	slices.Sort(boundaries)

	return slices.Compact(boundaries)
}

// overlayWithStyle combines overlay column span with its resolved style.
type overlayWithStyle struct {
	style *lipgloss.Style
	cols  position.Span
}

// computeStyleForPoint computes the effective style at a point given overlapping overlays.
// This uses pre-filtered overlays from styleLineWithRanges.
// Results are cached so identical style combinations return the same pointer.
func (p *Printer) computeStyleForPoint(
	point int,
	overlays []overlayWithStyle,
	baseStyle *lipgloss.Style,
	alwaysBlend bool,
) *lipgloss.Style {
	result := baseStyle
	firstRange := true

	for _, ov := range overlays {
		// Check if this overlay contains the point.
		if ov.cols.Contains(point) {
			override := !alwaysBlend && firstRange
			result = p.blender.Blend(result, ov.style, override)
			firstRange = false
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

// wrapContent splits content for word wrapping if enabled.
func (p *Printer) wrapContent(content string, gutterWidth int) []string {
	cw := p.contentWidth(gutterWidth)
	if cw <= 0 {
		return []string{content}
	}

	return strings.Split(lipgloss.Wrap(content, cw, wrapOnCharacters), "\n")
}

// renderTokenLine renders a single line's tokens with syntax highlighting.
// It handles separator (leading whitespace) and content styling, plus overlays from the Line.
// The lineIndex parameter is the 0-indexed position in the Lines collection.
func (p *Printer) renderTokenLine(lineIndex int, ln line.Line) string {
	if ln.IsEmpty() {
		return ""
	}

	pos := position.New(lineIndex, 0)

	var sb strings.Builder

	for _, tk := range ln.Tokens() {
		tokenStyle := p.styles.Style(tokens.TypeStyle(tk))
		valueOffset := tokens.ValueOffset(tk)

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
			sb.WriteString(
				p.styleLineWithRanges(sepPart, pos, defaultStyle, false, ln.Overlays),
			)

			pos.Col += separatorRunes
			originRunes = originRunes[separatorRunes:]
		}

		// Part 2: Render content portion (token style).
		if len(originRunes) > 0 {
			sb.WriteString(
				p.styleLineWithRanges(string(originRunes), pos, tokenStyle, false, ln.Overlays),
			)

			pos.Col += len(originRunes)
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
