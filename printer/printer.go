// Package printer renders [line.View] content as styled terminal output.
//
// A [Printer] takes any [line.View], such as the [line.Lines] view of a
// niceyaml Source, and renders it with syntax highlighting through
// [lipgloss.Style] values from a [StyleGetter]. Create one with [New] and
// render with [Printer.Print] or [Printer.Fprint]:
//
//	p := printer.New(printer.WithStyles(theme.Charm()))
//	fmt.Println(p.Print(source.Lines()))
//
// Every setting is an [Option]. A Printer never changes after construction,
// so [Printer.With] derives a copy with more options applied while the
// original stays as it was.
//
// # Gutters
//
// A [GutterFunc] renders the left edge of each row from a [GutterContext].
// [DefaultGutter] shows the line number and a diff marker, [DiffGutter] the
// marker only, [LineNumberGutter] the number only, and [NoGutter] nothing.
// Pass one to [WithGutter].
//
// # Overlays and Annotations
//
// The printer renders the [line.Overlays] and [line.Annotations] a view
// carries. An overlay styles a column span, and an [AnnotationFunc] renders
// the annotations above or below a line; [DefaultAnnotation] joins them with
// "; " and prefixes [line.Below] annotations with "^ ".
//
// # Word Wrapping
//
// [WithWidth] wraps content at a width, with the gutter width subtracted.
// [Printer.Rows] reports how many rows each line takes, so a viewer that
// scrolls by rendered row can map rows back to lines, and
// [Printer.RowWidth] reports the width of the widest row, so a viewer that
// scrolls horizontally knows how far the content reaches.
package printer

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
// It accepts a [line.View], such as the [line.Lines] view of a niceyaml
// Source, and produces styled terminal output using [lipgloss.Style]s.
// It applies syntax highlighting to YAML tokens, with support for
// customizable gutters, annotations, styled overlays, and word wrapping.
//
// A Printer is immutable after construction and safe for concurrent use.
// Every setting is a [Option]; to change one on an existing Printer,
// derive a copy with [Printer.With]:
//
//	narrow := printer.With(printer.WithWidth(40))
//
// Create instances with [New].
//
// # Rendering
//
// Use [Printer.Print] to render lines. When called without spans, it renders all
// lines. Pass [position.Span] arguments to render specific line spans, which is
// useful for showing error context or diff hunks:
//
//	p.Print(view)                   // All lines.
//	p.Print(view, span1, span2)     // Specific spans.
//
// Use [Printer.Fprint] to write the rendered output to an [io.Writer] instead
// of returning it as a string:
//
//	p.Fprint(os.Stdout, view)
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
// Add them to a [line.Lines] view with [line.Lines.AddOverlay], which
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
	maxNumber          int
	hasCustomStyle     bool
	annotationsEnabled bool
}

// New creates a new [*Printer].
// By default it uses [style.Default], [DefaultGutter], and [DefaultAnnotation].
func New(opts ...Option) *Printer {
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
//	wrapped := printer.With(printer.WithWidth(80))
//
// The copy shares the receiver's cache of blended styles unless [WithStyles]
// is among the options; the cache is safe for concurrent use.
func (p *Printer) With(opts ...Option) *Printer {
	c := *p
	c.apply(opts)

	return &c
}

// apply runs opts and recomputes the derived container style.
func (p *Printer) apply(opts []Option) {
	for _, opt := range opts {
		opt(p)
	}

	if !p.hasCustomStyle {
		p.style = p.styles.Style(style.Text).
			PaddingRight(1)
	}
}

// Option configures a [Printer].
//
// Available options:
//   - [WithStyles]
//   - [WithContainerStyle]
//   - [WithGutter]
//   - [WithAnnotationFunc]
//   - [WithWidth]
//   - [WithMaxNumber]
//   - [WithAnnotations]
type Option func(*Printer)

// GutterContext provides context about the current row for gutter rendering.
// It is passed to [GutterFunc] to determine the appropriate gutter content.
//
// Index, Number, and Flag describe the line the row belongs to. MaxNumber is
// the largest line number in the view, which sizes the line number column
// so every row of the view lines up. Soft marks a wrapped continuation row
// of that line, and Annotation marks a row that holds one of its
// annotations rather than its content, so the built-in gutters leave the
// line number and diff marker out of it.
type GutterContext struct {
	Styles     StyleGetter
	Index      int
	Number     int
	MaxNumber  int
	Flag       line.Flag
	Soft       bool
	Annotation bool
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
	Styles StyleGetter

	// Content is the text of the annotated line, without its line ending.
	// Annotation columns count runes of this text, and the display width
	// of those runes is what a marker must be padded by to sit under them.
	Content string

	Annotations line.Annotations
	Placement   line.Placement
}

// ColWidth returns the display width of the first col runes of ctx.Content,
// plus one cell for every column past the end of the content, so a marker
// padded by it lands under the rune at col whatever the width of the runes
// before it.
func (ctx AnnotationContext) ColWidth(col int) int {
	col = max(0, col)
	runes := []rune(ctx.Content)

	if col <= len(runes) {
		return lipgloss.Width(string(runes[:col]))
	}

	return lipgloss.Width(ctx.Content) + col - len(runes)
}

// AnnotationFunc returns the rendered annotation content based on
// [AnnotationContext].
type AnnotationFunc func(AnnotationContext) string

// DefaultAnnotation is the [AnnotationFunc] [New] uses. It joins the
// annotations with "; ", pads them to their column as
// [AnnotationContext.ColWidth] measures it, and prefixes [line.Below]
// annotations with "^ ". Annotations with empty content are left out, and
// it returns "" when none remain, as [line.Annotation.String] does. Control
// characters in the content render as their pictures, so an escape sequence
// in a message shows as text.
func DefaultAnnotation(ctx AnnotationContext) string {
	// Filter the annotations rather than their contents, so the column
	// comes from the ones that are shown. DeleteFunc zeroes the tail in
	// place, so work on a copy of the caller's slice.
	kept := slices.DeleteFunc(slices.Clone(ctx.Annotations), func(a line.Annotation) bool {
		return a.Content == ""
	})
	if len(kept) == 0 {
		return ""
	}

	for i := range kept {
		kept[i].Content = escape.Control(kept[i].Content)
	}

	padding := strings.Repeat(" ", ctx.ColWidth(kept.Col()))
	combined := strings.Join(kept.Contents(), "; ")

	// Add "^ " prefix for Below annotations.
	if ctx.Placement == line.Below {
		return padding + "^ " + combined
	}

	return padding + combined
}

// renderLineNumber renders the line number portion of a gutter. The number
// column is at least four wide and grows to fit the largest line number in
// the view, so every row of the view lines up. A line with no number, such
// as the placeholder a side-by-side diff inserts opposite an inserted or
// deleted line, gets a blank column.
func renderLineNumber(ctx GutterContext) string {
	lineNumStyle := ctx.Styles.Style(style.Text).
		Foreground(ctx.Styles.Style(style.Comment).GetForeground())

	width := max(4, len(strconv.Itoa(ctx.MaxNumber)))

	switch {
	case ctx.Annotation:
		return lineNumStyle.Render(strings.Repeat(" ", width+1))
	case ctx.Soft:
		return lineNumStyle.Render(strings.Repeat(" ", width-1) + "- ")
	case ctx.Number <= 0:
		return lineNumStyle.Render(strings.Repeat(" ", width+1))
	default:
		return lineNumStyle.Render(fmt.Sprintf("%*d ", width, ctx.Number))
	}
}

// renderDiffMarker renders the diff marker portion of a gutter. An
// annotation row carries no marker.
func renderDiffMarker(ctx GutterContext) string {
	if ctx.Annotation {
		return ctx.Styles.Style(style.Text).Render(" ")
	}

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

// DefaultGutter is the [GutterFunc] [New] uses. It renders the line
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

// WithContainerStyle is a [Option] that sets the [lipgloss.Style]
// wrapped around the whole rendered output. By default the container is the
// theme's [style.Text] style with one cell of right padding.
//
// To set the theme, which styles the tokens inside, use [WithStyles].
//
//nolint:gocritic // hugeParam: Copying.
func WithContainerStyle(s lipgloss.Style) Option {
	return func(p *Printer) {
		p.style = s
		p.hasCustomStyle = true
	}
}

// WithStyles is a [Option] that sets the [StyleGetter], typically a
// theme from [go.jacobcolvin.com/niceyaml/style/theme], that styles tokens,
// gutters, and annotations. A nil s selects [style.Default].
//
// To style the frame around the output, use [WithContainerStyle].
func WithStyles(s StyleGetter) Option {
	return func(p *Printer) {
		if s == nil {
			s = style.Default()
		}

		p.styles = s
		p.blends = newBlendCache()
	}
}

// WithGutter is a [Option] that sets the [GutterFunc] for rendering.
// By default, [DefaultGutter] renders line numbers and diff markers. A nil
// fn selects [NoGutter].
func WithGutter(fn GutterFunc) Option {
	return func(p *Printer) {
		if fn == nil {
			fn = NoGutter
		}

		p.gutterFunc = fn
	}
}

// WithAnnotationFunc is a [Option] that sets the [AnnotationFunc] for
// rendering annotations.
//
// By default, [DefaultAnnotation] is used which adds "^ " prefix for
// [line.Below] annotations. A nil fn selects [DefaultAnnotation].
func WithAnnotationFunc(fn AnnotationFunc) Option {
	return func(p *Printer) {
		if fn == nil {
			fn = DefaultAnnotation
		}

		p.annotationFunc = fn
	}
}

// WithWidth is a [Option] that sets the width for word wrapping.
// A width of 0, the default, disables wrapping.
func WithWidth(width int) Option {
	return func(p *Printer) {
		p.width = width
	}
}

// WithMaxNumber is a [Option] that sets the line number the gutter sizes
// itself for, instead of the largest number in the view. A max number of 0,
// the default, takes the number from the view.
//
// Use it to give two views the same gutter width, as a side-by-side diff
// needs when one revision is longer than the other.
func WithMaxNumber(n int) Option {
	return func(p *Printer) {
		p.maxNumber = max(0, n)
	}
}

// WithAnnotations is a [Option] that sets whether annotations are
// rendered. Defaults to true.
func WithAnnotations(enabled bool) Option {
	return func(p *Printer) {
		p.annotationsEnabled = enabled
	}
}

// Width returns the width used for word wrapping, or 0 when wrapping is
// disabled.
func (p *Printer) Width() int {
	return p.width
}

// MaxNumber returns the line number the gutter sizes itself for when
// rendering view: the number [WithMaxNumber] set, or the largest line number
// in the view.
func (p *Printer) MaxNumber(view line.View) int {
	if p.maxNumber > 0 {
		return p.maxNumber
	}

	return maxNumber(view)
}

// GutterWidth returns the width in cells of the gutter [Printer.Print]
// renders for every row of view. The gutter grows with the largest line
// number in the view, so a viewer that scrolls horizontally subtracts it
// from the row width to find the width of the content.
func (p *Printer) GutterWidth(view line.View) int {
	return p.gutterWidth(p.MaxNumber(view))
}

// ContainerStyle returns the [lipgloss.Style] wrapped around the whole
// rendered output. See [WithContainerStyle].
func (p *Printer) ContainerStyle() lipgloss.Style {
	return p.style
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
func (p *Printer) Fprint(w io.Writer, lines line.View, spans ...position.Span) (int, error) {
	n, err := io.WriteString(w, p.Print(lines, spans...))
	if err != nil {
		return n, fmt.Errorf("write rendered output: %w", err)
	}

	return n, nil
}

// Print prints any [line.View].
// It prints lines within the given [position.Span]s, in the supplied order.
// If no [position.Span]s are provided, all lines are printed.
func (p *Printer) Print(lines line.View, spans ...position.Span) string {
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

	maxNumber := p.MaxNumber(lines)

	// A span that renders no rows, because it is empty or lies outside the
	// view, adds no separator either, so the output holds exactly the rows
	// Rows counts.
	wrote := false

	for _, span := range spans {
		rows := p.renderSpan(lines, span, maxNumber)
		if len(rows) == 0 {
			continue
		}

		if wrote {
			sb.WriteByte('\n')
		}

		sb.WriteString(strings.Join(rows, "\n"))

		wrote = true
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
func (p *Printer) Rows(lines line.View, spans ...position.Span) []int {
	if len(spans) == 0 {
		spans = position.Spans{position.NewSpan(0, lines.Len())}
	}

	var rows []int

	maxNumber := p.MaxNumber(lines)
	gutterWidth := p.gutterWidth(maxNumber)

	for _, span := range spans {
		for idx, ln := range lines.AllLines(span) {
			rows = append(rows, len(p.renderLine(idx, ln, maxNumber, gutterWidth)))
		}
	}

	return rows
}

// RowWidth returns the width in cells of the widest row [Printer.Print]
// renders for the given spans, before the container style applies. It
// measures the rendered rows, so the gutter, the annotations, and wide
// characters all count. Without spans, RowWidth covers every line.
//
// Viewers that scroll horizontally use RowWidth to find the column the last
// row ends on.
func (p *Printer) RowWidth(lines line.View, spans ...position.Span) int {
	if len(spans) == 0 {
		spans = position.Spans{position.NewSpan(0, lines.Len())}
	}

	maxNumber := p.MaxNumber(lines)
	gutterWidth := p.gutterWidth(maxNumber)

	var width int

	for _, span := range spans {
		for idx, ln := range lines.AllLines(span) {
			for _, row := range p.renderLine(idx, ln, maxNumber, gutterWidth) {
				width = max(width, lipgloss.Width(row))
			}
		}
	}

	return width
}

// maxNumber returns the largest line number in view, or 0 when the view is
// empty. A view built from part of a document, such as a diff hunk or a
// slice of its lines, numbers its lines past its length.
func maxNumber(view line.View) int {
	n := 0

	for _, ln := range view.AllLines() {
		n = max(n, ln.Number())
	}

	return n
}

// gutterWidth returns the width of the gutter for a view whose largest line
// number is maxNumber. The widest gutter carries that number, so it
// samples with it.
func (p *Printer) gutterWidth(maxNumber int) int {
	return lipgloss.Width(p.gutterFunc(GutterContext{
		Styles:    p.styles,
		Number:    maxNumber,
		MaxNumber: maxNumber,
	}))
}

// renderSpan renders the lines of span as rows, with the gutter sized for
// maxNumber, the largest line number in t.
func (p *Printer) renderSpan(t line.View, span position.Span, maxNumber int) []string {
	if t.Len() == 0 {
		return nil
	}

	gutterWidth := p.gutterWidth(maxNumber)

	var rows []string

	for idx, ln := range t.AllLines(span) {
		rows = append(rows, p.renderLine(idx, ln, maxNumber, gutterWidth)...)
	}

	return rows
}

// renderLine renders one line as rows: its annotations above, its content
// wrapped to the printer width, and its annotations below.
func (p *Printer) renderLine(idx int, ln *line.Line, maxNumber, gutterWidth int) []string {
	var rows []string

	if p.annotationsEnabled {
		rows = append(rows, p.renderAnnotation(ln, idx, maxNumber, line.Above, gutterWidth)...)
	}

	gutterCtx := GutterContext{
		Index:     idx,
		Number:    ln.Number(),
		MaxNumber: maxNumber,
		Flag:      ln.Flag(),
		Styles:    p.styles,
	}

	var content string

	switch ln.Flag() {
	case line.FlagDeleted:
		content = p.styleLineWithRanges(ln.Content(), position.New(idx, 0), style.GenericDeleted, ln.Overlays())

	case line.FlagInserted:
		content = p.styleLineWithRanges(ln.Content(), position.New(idx, 0), style.GenericInserted, ln.Overlays())

	default: // line.FlagDefault (equal line).
		// Render with syntax highlighting.
		content = p.renderTokenLine(idx, ln)
	}

	rows = append(rows, p.contentRows(content, gutterCtx, gutterWidth)...)

	if p.annotationsEnabled {
		rows = append(rows, p.renderAnnotation(ln, idx, maxNumber, line.Below, gutterWidth)...)
	}

	return rows
}

// renderAnnotation renders the annotations of ln at the given placement as
// rows, each with its gutter. It returns nil when the line has none there
// or the [AnnotationFunc] renders them as nothing.
func (p *Printer) renderAnnotation(
	ln *line.Line,
	idx, maxNumber int,
	placement line.Placement,
	gutterWidth int,
) []string {
	anns := ln.Annotations().Filter(placement)
	if len(anns) == 0 {
		return nil
	}

	content := p.annotationFunc(AnnotationContext{
		Annotations: anns,
		Placement:   placement,
		Styles:      p.styles,
		Content:     ln.Content(),
	})
	if content == "" {
		return nil
	}

	// The func owns escaping, so styling it applied through ctx.Styles
	// survives. The wrap is ANSI-aware and measures the shown cells.
	//
	// The indent stays out of the wrapped text and comes back on every
	// row: the first row keeps it as rendered and continuation rows get
	// the same width in spaces, so the annotation column survives the
	// wrap and no row exceeds the width.
	indent, body := splitAnnotationIndent(content, placement)
	indentWidth := lipgloss.Width(indent)
	subLines := p.wrapContent(body, gutterWidth+indentWidth)

	rows := make([]string, 0, len(subLines))

	for j, subLine := range subLines {
		var sb strings.Builder

		sb.WriteString(p.gutterFunc(GutterContext{
			Index:      idx,
			Number:     ln.Number(),
			MaxNumber:  maxNumber,
			Soft:       j > 0,
			Flag:       ln.Flag(),
			Annotation: true,
			Styles:     p.styles,
		}))

		prefix := indent
		if j > 0 {
			prefix = strings.Repeat(" ", indentWidth)
		}

		sb.WriteString(p.styles.Style(style.Comment).Render(prefix + subLine))

		rows = append(rows, sb.String())
	}

	return rows
}

// splitAnnotationIndent splits rendered annotation content into the indent
// its wrapped rows align under and the body to wrap. The indent is the
// leading run of spaces and tabs plus, for [line.Below] content, the "^ "
// marker [DefaultAnnotation] puts after it.
func splitAnnotationIndent(content string, placement line.Placement) (string, string) {
	body := strings.TrimLeft(content, " \t")
	if placement == line.Below {
		body = strings.TrimPrefix(body, "^ ")
	}

	return content[:len(content)-len(body)], body
}

// contentRows wraps a line's rendered content to the printer width and
// returns the rows, each with the gutter generated from gutterCtx.
//
// Callers style and escape the whole line first, overlays included, so the
// wrap measures the text the terminal shows and each overlay covers the
// columns it names in the source line.
func (p *Printer) contentRows(content string, gutterCtx GutterContext, gutterWidth int) []string {
	subLines := p.wrapContent(content, gutterWidth)
	rows := make([]string, 0, len(subLines))

	for j, subLine := range subLines {
		// Generate gutter at write-time with correct Soft flag.
		ctx := gutterCtx
		ctx.Soft = j > 0

		rows = append(rows, p.gutterFunc(ctx)+subLine)
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
// with the same key render with the same style. Each name is quoted, so
// a name that contains a marker cannot collide with a different overlay
// sequence.
func blendKey(base style.Style, overlays line.Overlays, point int) string {
	var sb strings.Builder

	for _, ov := range overlays {
		if !ov.Cols.Contains(point) {
			continue
		}

		if sb.Len() == 0 {
			sb.WriteString(strconv.Quote(string(base)))
		}

		if ov.Blend {
			sb.WriteString("+")
		} else {
			sb.WriteString("!")
		}

		sb.WriteString(strconv.Quote(string(ov.Style)))
	}

	// A point no overlay covers keeps the base style, with no key to build.
	if sb.Len() == 0 {
		return strconv.Quote(string(base))
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
// gutter width. A positive printer width always wraps, so the result is at
// least one column even when the gutter alone fills the width.
//
// Returns 0 if wrapping is disabled.
func (p *Printer) contentWidth(gutterWidth int) int {
	if p.width <= 0 {
		return 0
	}

	return max(1, p.width-gutterWidth)
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
		tokenStyle := typeStyle(tk, ln.TokenAt(pos.Col))

		// Drop the line ending, CR included, as Line.Content does. Print
		// joins the rows with newlines.
		origin := tokens.TrimLineEnding(tk.Origin)
		originRunes := []rune(origin)

		// The separator is the whitespace the token carries before its
		// text, whether that text is a plain scalar, a quoted string, an
		// anchor, a comment, or the continuation of a multiline scalar.
		separatorRunes := leadingWhitespaceRunes(origin)

		// Part 1: Render separator portion (default style).
		if separatorRunes > 0 {
			sepPart := string(originRunes[:separatorRunes])
			sb.WriteString(
				p.styleLineWithRanges(sepPart, pos, style.Text, ln.Overlays()),
			)

			pos.Col += separatorRunes
			originRunes = originRunes[separatorRunes:]
		}

		// Part 2: Render content portion (token style).
		if len(originRunes) > 0 {
			sb.WriteString(
				p.styleLineWithRanges(string(originRunes), pos, tokenStyle, ln.Overlays()),
			)

			pos.Col += len(originRunes)
		}
	}

	return sb.String()
}

// leadingWhitespaceRunes returns the number of runes in the run of spaces
// and tabs that starts s.
func leadingWhitespaceRunes(s string) int {
	return len(s) - len(strings.TrimLeft(s, " \t"))
}
