package niceyaml

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/internal/colors"
	"go.jacobcolvin.com/niceyaml/internal/escape"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/tokens"
)

const wrapOnCharacters = " /-"

// StyleGetter retrieves styles by category.
//
// A [Printer] asks for each category as it renders and caches the styles it
// blends for overlays by the category names involved, so Style should return
// the same style for a category for the life of the value.
//
// See [style.Styles] for an implementation.
type StyleGetter interface {
	Style(s style.Style) lipgloss.Style
}

// Printer prints YAML with syntax highlighting for terminal output.
//
// It accepts a [LineIterator], either a [*Source] or a [line.Lines] view, and
// produces styled terminal output using [lipgloss.Style]s. It applies syntax
// highlighting to YAML tokens, with support for customizable gutters,
// annotations, styled overlays, and word wrapping.
//
// A Printer is immutable after construction and safe for concurrent use.
// Every setting is a [PrinterOption]; to change one on an existing Printer,
// derive a copy with [Printer.With]:
//
//	narrow := printer.With(niceyaml.WithWidth(40))
//
// Create instances with [NewPrinter].
//
// # Rendering
//
// Use [Printer.Print] to render lines. When called without spans, it renders all
// lines. Pass [position.Span] arguments to render specific line spans, which is
// useful for showing error context or diff hunks:
//
//	printer.Print(source)                   // All lines.
//	printer.Print(source, span1, span2)     // Specific spans.
//
// Use [Printer.Fprint] to write the rendered output to an [io.Writer] instead
// of returning it as a string:
//
//	printer.Fprint(os.Stdout, source)
//
// # Gutters
//
// Gutters appear at the left edge of each line and typically show line numbers
// or diff markers. The printer uses [DefaultGutter] by default, which combines
// line numbers with diff markers (+/-). Other built-in options include
// [DiffGutter] (markers only), [LineNumberGutter] (numbers only), and [NoGutter].
//
// # Overlays
//
// Overlays apply visual highlighting to specific column spans within lines.
// Add them to a view from [Source.Lines] with [line.Lines.AddOverlay], which
// replaces the style underneath, or [line.Lines.BlendOverlay], which mixes
// with it, then print the view. Error positions use the first and search
// highlights the second, so a match keeps the token or diff color it covers.
//
// # Annotations
//
// Annotations are extra text lines rendered above or below a line, outside the
// token stream. They display error messages, diff hunk headers, or other
// contextual notes. The printer renders them via [AnnotationFunc], defaulting
// to [DefaultAnnotation] which prefixes below-line annotations with "^ ".
//
// # Word Wrapping
//
// Pass [WithWidth] to enable word wrapping at a given width. The printer
// accounts for gutter width when calculating available content width. Wrapped
// continuation lines show a "-" marker in the gutter. A width of 0 turns
// wrapping off.
type Printer struct {
	styles         StyleGetter
	style          lipgloss.Style
	gutterFunc     GutterFunc
	annotationFunc AnnotationFunc
	// Blended styles by the categories that produce them. WithStyles
	// replaces it, since the categories then resolve to other styles.
	blends             *blendCache
	width              int
	hasCustomStyle     bool
	annotationsEnabled bool
}

// NewPrinter creates a new [*Printer].
// By default it uses [style.Default], [DefaultGutter], and [DefaultAnnotation].
func NewPrinter(opts ...PrinterOption) *Printer {
	p := &Printer{
		styles:             style.Default(),
		gutterFunc:         DefaultGutter,
		annotationFunc:     DefaultAnnotation,
		blends:             newBlendCache(),
		annotationsEnabled: true,
	}

	p.apply(opts)

	return p
}

// With returns a copy of the [Printer] with the given options applied. The
// receiver is unchanged, so a shared Printer can be specialized per call:
//
//	wrapped := printer.With(niceyaml.WithWidth(80))
//
// The copy shares the receiver's cache of blended styles unless [WithStyles]
// is among the options; the cache is safe for concurrent use.
func (p *Printer) With(opts ...PrinterOption) *Printer {
	c := *p
	c.apply(opts)

	return &c
}

// apply runs opts and recomputes the derived container style.
func (p *Printer) apply(opts []PrinterOption) {
	for _, opt := range opts {
		opt(p)
	}

	if !p.hasCustomStyle {
		p.style = p.styles.Style(style.Text).
			PaddingRight(1)
	}
}

// PrinterOption configures a [Printer].
//
// Available options:
//   - [WithStyles]
//   - [WithContainerStyle]
//   - [WithGutter]
//   - [WithAnnotationFunc]
//   - [WithWidth]
//   - [WithAnnotations]
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
//
// [DefaultGutter], [DiffGutter], [LineNumberGutter], and [NoGutter] are
// ready-made gutters; pass one to [WithGutter].
type GutterFunc func(GutterContext) string

// AnnotationContext provides context for annotation rendering.
//
// It is passed to [AnnotationFunc] to determine the appropriate annotation
// content.
type AnnotationContext struct {
	Styles      StyleGetter
	Annotations line.Annotations
	Placement   line.Placement
}

// AnnotationFunc returns the rendered annotation content based on
// [AnnotationContext].
type AnnotationFunc func(AnnotationContext) string

// DefaultAnnotation is the [AnnotationFunc] [NewPrinter] uses. It joins the
// annotations with "; ", pads them to their column, and prefixes [line.Below]
// annotations with "^ ".
func DefaultAnnotation(ctx AnnotationContext) string {
	if len(ctx.Annotations) == 0 {
		return ""
	}

	padding := strings.Repeat(" ", max(0, ctx.Annotations.Col()))
	combined := strings.Join(ctx.Annotations.Contents(), "; ")

	// Add "^ " prefix for Below annotations.
	if ctx.Placement == line.Below {
		return padding + "^ " + combined
	}

	return padding + combined
}

// renderLineNumber renders the line number portion of a gutter. The number
// column is at least four wide and grows to fit the total line count, so
// every row of a document lines up.
func renderLineNumber(ctx GutterContext) string {
	lineNumStyle := ctx.Styles.Style(style.Text).
		Foreground(ctx.Styles.Style(style.Comment).GetForeground())

	width := max(4, len(strconv.Itoa(ctx.TotalLines)))

	switch {
	case ctx.Flag == line.FlagAnnotation:
		return lineNumStyle.Render(strings.Repeat(" ", width+1))
	case ctx.Soft:
		return lineNumStyle.Render(strings.Repeat(" ", width-1) + "- ")
	default:
		return lineNumStyle.Render(fmt.Sprintf("%*d ", width, ctx.Number))
	}
}

// renderDiffMarker renders the diff marker portion of a gutter.
func renderDiffMarker(ctx GutterContext) string {
	if ctx.Soft {
		switch ctx.Flag {
		case line.FlagInserted:
			return ctx.Styles.Style(style.GenericInserted).Render(" ")
		case line.FlagDeleted:
			return ctx.Styles.Style(style.GenericDeleted).Render(" ")
		default:
			return ctx.Styles.Style(style.Text).Render(" ")
		}
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

// DefaultGutter is the [GutterFunc] [NewPrinter] uses. It renders the line
// number followed by the diff marker.
func DefaultGutter(ctx GutterContext) string {
	return renderLineNumber(ctx) + renderDiffMarker(ctx)
}

// DiffGutter is a [GutterFunc] that renders diff markers only (" ", "+",
// "-"), styled with [style.GenericInserted] and [style.GenericDeleted].
func DiffGutter(ctx GutterContext) string {
	return renderDiffMarker(ctx)
}

// LineNumberGutter is a [GutterFunc] that renders line numbers only, in the
// [style.Comment] foreground. Soft-wrapped continuation lines show " - ".
func LineNumberGutter(ctx GutterContext) string {
	return renderLineNumber(ctx)
}

// NoGutter is a [GutterFunc] that renders nothing.
func NoGutter(GutterContext) string {
	return ""
}

// WithContainerStyle is a [PrinterOption] that sets the [lipgloss.Style]
// wrapped around the whole rendered output. By default the container is the
// theme's [style.Text] style with one cell of right padding.
//
// To set the theme, which styles the tokens inside, use [WithStyles].
//
//nolint:gocritic // hugeParam: Copying.
func WithContainerStyle(s lipgloss.Style) PrinterOption {
	return func(p *Printer) {
		p.style = s
		p.hasCustomStyle = true
	}
}

// WithStyles is a [PrinterOption] that sets the [StyleGetter], typically a
// theme from [go.jacobcolvin.com/niceyaml/style/theme], that styles tokens,
// gutters, and annotations.
//
// To style the frame around the output, use [WithContainerStyle].
func WithStyles(s StyleGetter) PrinterOption {
	return func(p *Printer) {
		p.styles = s
		p.blends = newBlendCache()
	}
}

// WithGutter is a [PrinterOption] that sets the [GutterFunc] for rendering.
// By default, [DefaultGutter] renders line numbers and diff markers.
func WithGutter(fn GutterFunc) PrinterOption {
	return func(p *Printer) {
		p.gutterFunc = fn
	}
}

// WithAnnotationFunc is a [PrinterOption] that sets the [AnnotationFunc] for
// rendering annotations.
//
// By default, [DefaultAnnotation] is used which adds "^ " prefix for
// [line.Below] annotations.
func WithAnnotationFunc(fn AnnotationFunc) PrinterOption {
	return func(p *Printer) {
		p.annotationFunc = fn
	}
}

// WithWidth is a [PrinterOption] that sets the width for word wrapping.
// A width of 0, the default, disables wrapping.
func WithWidth(width int) PrinterOption {
	return func(p *Printer) {
		p.width = width
	}
}

// WithAnnotations is a [PrinterOption] that sets whether annotations are
// rendered. Defaults to true.
func WithAnnotations(enabled bool) PrinterOption {
	return func(p *Printer) {
		p.annotationsEnabled = enabled
	}
}

// Width returns the width used for word wrapping, or 0 when wrapping is
// disabled.
func (p *Printer) Width() int {
	return p.width
}

// Style retrieves the [lipgloss.Style] for the given [style.Style] from the
// printer's [StyleGetter].
func (p *Printer) Style(s style.Style) lipgloss.Style {
	return p.styles.Style(s)
}

// Fprint renders lines to w. It renders lines within the given
// [position.Span]s, in the supplied order. If no [position.Span]s are
// provided, all lines are rendered.
//
// It returns the number of bytes written and any write error encountered.
func (p *Printer) Fprint(w io.Writer, lines LineIterator, spans ...position.Span) (int, error) {
	n, err := io.WriteString(w, p.Print(lines, spans...))
	if err != nil {
		return n, fmt.Errorf("write rendered output: %w", err)
	}

	return n, nil
}

// Print prints any [LineIterator].
// It prints lines within the given [position.Span]s, in the supplied order.
// If no [position.Span]s are provided, all lines are printed.
func (p *Printer) Print(lines LineIterator, spans ...position.Span) string {
	if len(spans) == 0 {
		spans = position.Spans{position.NewSpan(0, lines.Len())}
	}

	// Size the buffer by the lines the spans select. A viewer prints one
	// window of a long document at a time.
	selected := 0
	for _, span := range spans {
		selected += max(0, min(span.End, lines.Len())-max(span.Start, 0))
	}

	var sb strings.Builder

	sb.Grow(selected * 100)

	for i, span := range spans {
		if i > 0 {
			sb.WriteByte('\n')
		}

		sb.WriteString(strings.Join(p.renderSpan(lines, span), "\n"))
	}

	return p.style.Render(sb.String())
}

// Rows returns the number of rendered rows each line occupies, in the order
// [Printer.Print] would render the lines of the given spans. A line takes one
// row for each wrapped piece of its content and each wrapped piece of its
// annotations, so the sum is the row count of the output before the
// container style applies. Without spans, Rows covers every line.
//
// Viewers that scroll by rendered row use Rows to map a window of rows back
// to the lines that fill it.
func (p *Printer) Rows(lines LineIterator, spans ...position.Span) []int {
	if len(spans) == 0 {
		spans = position.Spans{position.NewSpan(0, lines.Len())}
	}

	var rows []int

	for _, span := range spans {
		gutterWidth := p.gutterWidth(lines.Len())

		for idx, ln := range lines.AllLines(span) {
			rows = append(rows, len(p.renderLine(idx, ln, lines.Len(), gutterWidth)))
		}
	}

	return rows
}

// gutterWidth returns the width of the gutter for a document of totalLines
// lines. The widest gutter carries the largest line number, so it samples
// with that.
func (p *Printer) gutterWidth(totalLines int) int {
	if p.gutterFunc == nil {
		return 0
	}

	return lipgloss.Width(p.gutterFunc(GutterContext{
		Styles:     p.styles,
		Index:      totalLines - 1,
		Number:     totalLines,
		TotalLines: totalLines,
	}))
}

// renderSpan renders the lines of span as rows.
func (p *Printer) renderSpan(t LineIterator, span position.Span) []string {
	if t.IsEmpty() {
		return nil
	}

	totalLines := t.Len()
	gutterWidth := p.gutterWidth(totalLines)

	var rows []string

	for idx, ln := range t.AllLines(span) {
		rows = append(rows, p.renderLine(idx, ln, totalLines, gutterWidth)...)
	}

	return rows
}

// renderLine renders one line as rows: its annotations above, its content
// wrapped to the printer width, and its annotations below.
func (p *Printer) renderLine(idx int, ln *line.Line, totalLines, gutterWidth int) []string {
	var rows []string

	if p.annotationsEnabled {
		rows = append(rows, p.renderAnnotation(ln, idx, totalLines, line.Above, gutterWidth)...)
	}

	gutterCtx := GutterContext{
		Index:      idx,
		Number:     ln.Number(),
		TotalLines: totalLines,
		Flag:       ln.Flag,
		Styles:     p.styles,
	}

	var (
		content      string
		contentStyle style.Style
		styled       bool
	)

	switch ln.Flag {
	case line.FlagDeleted:
		content = ln.Content()
		contentStyle = style.GenericDeleted

	case line.FlagInserted:
		content = ln.Content()
		contentStyle = style.GenericInserted

	default: // FlagDefault (equal line).
		// Render with syntax highlighting.
		content = p.renderTokenLine(idx, ln)
		styled = true
	}

	rows = append(rows, p.contentRows(
		content,
		position.New(idx, 0),
		contentStyle,
		styled,
		ln.Overlays,
		gutterCtx,
		gutterWidth,
	)...)

	if p.annotationsEnabled {
		rows = append(rows, p.renderAnnotation(ln, idx, totalLines, line.Below, gutterWidth)...)
	}

	return rows
}

// renderAnnotation renders the annotations of ln at the given placement as
// rows, each with its gutter. It returns nil when the line has none there
// or the [AnnotationFunc] renders them as nothing.
func (p *Printer) renderAnnotation(
	ln *line.Line,
	idx, totalLines int,
	placement line.Placement,
	gutterWidth int,
) []string {
	anns := ln.Annotations.Filter(placement)
	if len(anns) == 0 {
		return nil
	}

	content := p.annotationFunc(AnnotationContext{
		Annotations: anns,
		Placement:   placement,
		Styles:      p.styles,
	})
	if content == "" {
		return nil
	}

	subLines := p.wrapContent(content, gutterWidth)

	// Calculate continuation padding for wrapped lines.
	// For Below annotations: col spaces + "^ " = col + 2.
	// For Above annotations: col spaces.
	continuationPadding := strings.Repeat(" ", anns.Col())
	if placement == line.Below {
		continuationPadding += "  " // Align with text after "^ ".
	}

	rows := make([]string, 0, len(subLines))

	for j, subLine := range subLines {
		var sb strings.Builder

		sb.WriteString(p.gutterFunc(GutterContext{
			Index:      idx,
			Number:     ln.Number(),
			TotalLines: totalLines,
			Soft:       j > 0,
			Flag:       line.FlagAnnotation,
			Styles:     p.styles,
		}))

		// Add continuation padding for wrapped lines.
		if j > 0 {
			sb.WriteString(p.styles.Style(style.Comment).Render(continuationPadding))
		}

		sb.WriteString(p.styles.Style(style.Comment).Render(escape.Control(subLine)))

		rows = append(rows, sb.String())
	}

	return rows
}

// contentRows renders a line's content as rows, wrapping to the printer
// width, with the gutter generated for each row from gutterCtx.
//
// When styled is set the content already carries its styles, as a token
// line does; otherwise contentRows applies contentStyle to the content with
// its overlays.
func (p *Printer) contentRows(
	content string,
	pos position.Position,
	contentStyle style.Style,
	styled bool,
	overlays line.Overlays,
	gutterCtx GutterContext,
	gutterWidth int,
) []string {
	subLines := p.wrapContent(content, gutterWidth)
	rows := make([]string, 0, len(subLines))

	for j, subLine := range subLines {
		var sb strings.Builder

		// Generate gutter at write-time with correct Soft flag.
		ctx := gutterCtx
		ctx.Soft = j > 0
		sb.WriteString(p.gutterFunc(ctx))

		if styled {
			// For equal lines: content is already styled.
			sb.WriteString(subLine)
		} else {
			// For diff lines: apply diff style to content.
			sb.WriteString(p.styleLineWithRanges(subLine, pos, contentStyle, overlays))
		}

		pos.Col += utf8.RuneCountInString(subLine)

		rows = append(rows, sb.String())
	}

	return rows
}

// styleLineWithRanges styles a line with range-aware styling. It splits the
// line into spans based on effective styles (base + overlapping ranges).
//
// The pos parameter specifies the visual line and column position. Each
// overlay either replaces or blends with the style underneath it, as its
// [line.Overlay.Blend] field says.
//
// The overlays parameter provides style overlays from [line.Line]; pass nil if
// none.
func (p *Printer) styleLineWithRanges(
	src string,
	pos position.Position,
	base style.Style,
	overlays line.Overlays,
) string {
	if src == "" {
		return src
	}

	if len(overlays) == 0 {
		return p.styles.Style(base).Render(escape.Control(src))
	}

	// Create span for this line segment's column range.
	cols := position.NewSpan(pos.Col, pos.Col+utf8.RuneCountInString(src))

	// Keep the overlays that overlap this column span.
	var active line.Overlays

	for _, o := range overlays {
		if o.Cols.Overlaps(cols) {
			active = append(active, o)
		}
	}

	if len(active) == 0 {
		return p.styles.Style(base).Render(escape.Control(src))
	}

	boundaries := computeStyleBoundaries(active, cols)
	if len(boundaries) < 2 {
		return ""
	}

	// Render spans between boundaries, merging adjacent same-styled spans.
	var sb strings.Builder

	sb.Grow(len(src) * 2)

	runes := []rune(src)

	var (
		currentKey   string
		currentPoint int
	)

	spanStart := 0

	for i := range len(boundaries) - 1 {
		boundaryStart := boundaries[i] - cols.Start // Convert to rune index.
		boundaryEnd := boundaries[i+1] - cols.Start // Convert to rune index.
		spanPoint := boundaries[i]                  // Point for style lookup.

		if boundaryStart < 0 || boundaryEnd > len(runes) || boundaryStart >= boundaryEnd {
			continue
		}

		spanKey := blendKey(base, active, spanPoint)

		// Merge adjacent spans with the same style.
		if currentKey == "" {
			currentKey, currentPoint = spanKey, spanPoint
			spanStart = boundaryStart
		} else if currentKey != spanKey {
			// Style changed - flush current span.
			st := p.blended(currentKey, base, active, currentPoint)
			sb.WriteString(st.Render(escape.Control(string(runes[spanStart:boundaryStart]))))

			currentKey, currentPoint = spanKey, spanPoint
			spanStart = boundaryStart
		}

		// If styles are equal, continue accumulating the span.
	}

	// Flush remaining content.
	if currentKey != "" && spanStart < len(runes) {
		st := p.blended(currentKey, base, active, currentPoint)
		sb.WriteString(st.Render(escape.Control(string(runes[spanStart:]))))
	}

	return sb.String()
}

// computeStyleBoundaries returns sorted, deduplicated boundary points where
// styles change.
//
// The cols span defines the line segment boundaries.
func computeStyleBoundaries(active line.Overlays, cols position.Span) []int {
	boundaries := make([]int, 0, len(active)*2+2)
	boundaries = append(boundaries, cols.Start, cols.End)

	for _, ov := range active {
		if ov.Cols.Start > cols.Start && ov.Cols.Start < cols.End {
			boundaries = append(boundaries, ov.Cols.Start)
		}

		if ov.Cols.End > cols.Start && ov.Cols.End < cols.End {
			boundaries = append(boundaries, ov.Cols.End)
		}
	}

	slices.Sort(boundaries)

	return slices.Compact(boundaries)
}

// blendKey returns the cache key of the effective style at point: the base
// category followed by each overlay that covers the point, in order, marked
// by whether it blends with or replaces the style underneath. Two points
// with the same key render with the same style.
func blendKey(base style.Style, overlays line.Overlays, point int) string {
	var sb strings.Builder

	for _, ov := range overlays {
		if !ov.Cols.Contains(point) {
			continue
		}

		if sb.Len() == 0 {
			sb.WriteString(string(base))
		}

		if ov.Blend {
			sb.WriteString("+")
		} else {
			sb.WriteString("!")
		}

		sb.WriteString(string(ov.Style))
	}

	// A point no overlay covers keeps the base style, with no key to build.
	if sb.Len() == 0 {
		return string(base)
	}

	return sb.String()
}

// blended returns the style for key, which [blendKey] built from base and
// the overlays that cover point. It computes the style on the first request
// and serves the cache after that. The overlays apply in order: a blending
// overlay mixes with the result so far, and any other replaces it.
func (p *Printer) blended(key string, base style.Style, overlays line.Overlays, point int) lipgloss.Style {
	if st, ok := p.blends.get(key); ok {
		return st
	}

	result := p.styles.Style(base)

	for _, ov := range overlays {
		if !ov.Cols.Contains(point) {
			continue
		}

		if ov.Blend {
			result = colors.BlendStyles(result, p.styles.Style(ov.Style))
		} else {
			result = colors.OverrideStyles(result, p.styles.Style(ov.Style))
		}
	}

	return p.blends.put(key, result)
}

// blendCache holds the styles a [Printer] blends for overlays, keyed by
// [blendKey]. Copies of a Printer share one cache until [WithStyles] gives
// a copy its own, so it is safe for concurrent use.
type blendCache struct {
	styles map[string]lipgloss.Style
	mu     sync.RWMutex
}

// newBlendCache creates a new [*blendCache].
func newBlendCache() *blendCache {
	return &blendCache{styles: make(map[string]lipgloss.Style)}
}

// get returns the cached style for key.
func (c *blendCache) get(key string) (lipgloss.Style, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	st, ok := c.styles[key]

	return st, ok
}

// put stores st under key and returns it.
//
//nolint:gocritic // hugeParam: value semantics match lipgloss.
func (c *blendCache) put(key string, st lipgloss.Style) lipgloss.Style {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.styles[key] = st

	return st
}

// contentWidth returns the available width for content after accounting for
// gutter width.
//
// Returns 0 if wrapping is disabled.
func (p *Printer) contentWidth(gutterWidth int) int {
	if p.width <= 0 {
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
//
// It handles separator (leading whitespace) and content styling, plus overlays
// from the [line.Line].
//
// The lineIndex parameter is the 0-indexed position in the [line.Lines]
// collection.
func (p *Printer) renderTokenLine(lineIndex int, ln *line.Line) string {
	if ln.IsEmpty() {
		return ""
	}

	pos := position.New(lineIndex, 0)

	var sb strings.Builder

	for _, tk := range ln.Tokens() {
		tokenStyle := tokens.TypeStyle(tk)
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
			sb.WriteString(
				p.styleLineWithRanges(sepPart, pos, style.Text, ln.Overlays),
			)

			pos.Col += separatorRunes
			originRunes = originRunes[separatorRunes:]
		}

		// Part 2: Render content portion (token style).
		if len(originRunes) > 0 {
			sb.WriteString(
				p.styleLineWithRanges(string(originRunes), pos, tokenStyle, ln.Overlays),
			)

			pos.Col += len(originRunes)
		}
	}

	return sb.String()
}

// leadingWhitespaceRunes returns the number of runes in the leading whitespace
// portion of s, up to maxBytes.
//
// Returns 0 if maxBytes is invalid or if the prefix contains non-whitespace
// characters.
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
