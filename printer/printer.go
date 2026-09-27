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
	"github.com/charmbracelet/x/ansi"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/cells"
	"go.jacobcolvin.com/niceyaml/internal/colors"
	"go.jacobcolvin.com/niceyaml/internal/escape"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

const wrapOnCharacters = " /-"

// Printer prints YAML with syntax highlighting for terminal output.
//
// It accepts a [line.View], such as the view of a niceyaml Source, and
// renders its YAML tokens as styled terminal output using
// [lipgloss.Style] values, with customizable gutters, annotations, styled
// overlays, and word wrapping. The package documentation covers each of
// these.
//
// A Printer is immutable after construction and safe for concurrent use.
// Every setting is an [Option]; to change one on an existing Printer,
// derive a copy with [Printer.With]:
//
//	narrow := p.With(printer.WithWrap(40))
//
// Create instances with [New].
//
// # Rendering
//
// Use [Printer.Print] to render every line of a view. To render part of a
// document, such as the lines around an error or the hunks of a diff, print
// the view [line.View.Slice] returns, whose lines keep their numbers:
//
//	p.Print(view)                       // All lines.
//	p.Print(view.Slice(span1, span2))   // The lines of two spans.
//
// Use [Printer.Fprint] to write the rendered output to an [io.Writer] instead
// of returning it as a string:
//
//	p.Fprint(os.Stdout, view)
//
// # Errors
//
// [Printer.PrintError] renders any error as a tree, with a connector in
// front of each nested error, and the source excerpt of every
// [niceyaml.SourceError] in it, with the printer's styles, width, and the
// context lines [WithContextLines] sets. A program configures one printer
// with its terminal width and theme and prints its errors through it.
type Printer struct {
	styles         style.Styler
	style          lipgloss.Style
	gutter         Gutter
	annotationFunc AnnotationFunc
	// Blended styles by the kinds that produce them. WithStyles replaces
	// it, since the kinds then resolve to other styles.
	blends         *blendCache
	wrap           int
	containerWidth int
	maxNumber      int
	contextLines   int
	hasCustomStyle bool
}

// DefaultContextLines is the number of context lines [Printer.PrintError]
// shows around each error location unless [WithContextLines] sets another.
// It is [niceyaml.DefaultContextLines], the count the %+v verb of a
// [*niceyaml.SourceError] uses, so PrintError and %+v show the same
// context by default.
const DefaultContextLines = niceyaml.DefaultContextLines

// New creates a new [*Printer].
// By default it uses [style.Default], [DefaultGutter], and [DefaultAnnotation].
func New(opts ...Option) *Printer {
	p := &Printer{
		styles:         style.Default(),
		gutter:         DefaultGutter,
		annotationFunc: DefaultAnnotation,
		blends:         newBlendCache(),
		contextLines:   DefaultContextLines,
	}

	p.apply(opts)

	return p
}

// With returns a copy of the [Printer] with the given options applied. The
// receiver is unchanged, so callers can specialize a shared Printer per call:
//
//	wrapped := p.With(printer.WithWrap(80))
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
		p.style = p.styles.Style(kind.Text).
			PaddingRight(1)
	}
}

// Option configures a [Printer].
//
// Available options:
//   - [WithStyles]
//   - [WithContainerStyle]
//   - [WithContainerWidth]
//   - [WithGutter]
//   - [WithAnnotation]
//   - [WithWrap]
//   - [WithMaxNumber]
//   - [WithContextLines]
type Option func(*Printer)

// GutterContext provides context about the current row for gutter rendering.
// The printer passes it to [Gutter.Render] to determine the gutter content.
//
// Index, Number, and Flag describe the line the row belongs to. MaxNumber
// is the number the gutter sizes its line number column for, the larger
// of the view's largest line number and the one [WithMaxNumber] sets, so
// every row of the view lines up. Annotation marks a row that holds one
// of the line's annotations rather than its content, so the built-in
// gutters leave the line number and diff marker out of it. Soft marks a
// row that continues the one above it. On a content row, Soft marks a
// wrapped continuation of the line. On an annotation row, Soft marks
// every row after the first of the line's annotations at one placement,
// which covers the first row of a later [AnnotationRow] and a row that
// starts at a newline in its Text. A gutter that draws a wrap marker
// only beside wrapped content checks Annotation before Soft.
type GutterContext struct {
	Styles     style.Styler
	Index      int
	Number     int
	MaxNumber  int
	Flag       line.Flag
	Soft       bool
	Annotation bool
}

// styler returns the styles a gutter renders with: the ones the context
// carries, or the default styles when it carries none, so a gutter called
// with a zero context renders rather than panics.
func (c GutterContext) styler() style.Styler {
	if c.Styles == nil {
		return style.Default()
	}

	return c.Styles
}

// Gutter renders the left edge of each row. The printer asks it once per
// view for the width of the gutter, budgets word wrapping and
// [Layout.GutterWidth] from that, and renders every row's gutter at that
// width: text that falls short is padded with spaces in [kind.Text], and
// text that runs over is cut, so the content of every row starts in the
// same column whatever the gutter renders for the row.
//
// A gutter that shows a marker on some rows and nothing on others
// therefore declares the width of the marker and renders what it likes:
//
//	type markerGutter struct{}
//
//	func (markerGutter) Width(printer.GutterContext) int { return 2 }
//
//	func (markerGutter) Render(ctx printer.GutterContext) string {
//		if ctx.Flag == line.FlagInserted {
//			return "+ "
//		}
//
//		return ""
//	}
//
//	p := printer.New(printer.WithGutter(markerGutter{}))
//
// [DefaultGutter], [DiffGutter], [LineNumberGutter], and [NoGutter] are
// ready-made gutters, and [GutterFunc] adapts a function; pass one to
// [WithGutter].
type Gutter interface {
	// Width returns the width in cells of the gutter on every row of a
	// view. The context holds the styles the gutter renders with and, in
	// both Number and MaxNumber, the largest line number of the view, and
	// nothing else, since the width may not depend on the row.
	Width(ctx GutterContext) int
	// Render returns the gutter text for a row. The printer pads or cuts
	// it to Width.
	Render(ctx GutterContext) string
}

// GutterFunc adapts a function to the [Gutter] interface. Its width is
// the width of what the function renders for the context [Gutter.Width]
// receives, so a function whose width depends on the line number alone,
// as the built-in gutters do, measures as it renders, and one that varies
// its width on the flag or the row is padded or cut to what it renders
// for that context:
//
//	p := printer.New(printer.WithGutter(printer.GutterFunc(func(ctx printer.GutterContext) string {
//		return fmt.Sprintf("%3d ", ctx.Number)
//	})))
type GutterFunc func(GutterContext) string

// Width implements [Gutter] by measuring what f renders for ctx.
func (f GutterFunc) Width(ctx GutterContext) int {
	return lipgloss.Width(f(ctx))
}

// Render implements [Gutter].
func (f GutterFunc) Render(ctx GutterContext) string {
	return f(ctx)
}

// AnnotationContext is what the printer hands an [AnnotationFunc] for the
// annotations of one line and placement that render in one kind, their
// [line.Annotation.Kind] or [kind.UIAnnotation] for the zero Kind. Each
// annotation keeps its Kind as given, so a context for [kind.UIAnnotation]
// can hold annotations of the zero Kind alongside ones of that kind. The
// func returns one [AnnotationRow] for them, and the printer pads,
// escapes, wraps, and styles it.
type AnnotationContext struct {
	// Content is the text of the annotated line, without its line ending.
	// Annotation columns count runes of this text, and their display
	// width gives the padding a marker needs to sit under them.
	Content string

	// Overlays are the overlays of the annotated line, so a func that
	// marks the line rather than describing it draws under the columns
	// they cover.
	Overlays line.Overlays

	Annotations line.Annotations
	Placement   line.Placement
}

// ColWidth returns the display width of the first col runes of ctx.Content,
// plus one cell for every column past the end of the content, so a marker
// padded by it lands under the rune at col whatever the width of the runes
// before it. See [ColWidth] for the rules it measures by.
func (ctx AnnotationContext) ColWidth(col int) int {
	return ColWidth(ctx.Content, col)
}

// ColWidth returns the display width of the first col runes of content, the
// text of a line without its line ending, plus one cell for every column
// past the end of the content. The width is that of the row the printer
// renders, in which a control character shows as a one-cell picture and a
// grapheme cluster, such as a letter with a combining mark or an emoji ZWJ
// sequence, takes its display width once. A column inside a cluster has no
// cell of its own, so it measures up to the start of its cluster. A viewer
// that scrolls horizontally adds the result to [Layout.GutterWidth] to
// find the cell a column of the line occupies.
func ColWidth(content string, col int) int {
	return cells.NewRow(content).Width(col)
}

// AnnotationRow is what an [AnnotationFunc] renders for the annotations
// of one line and placement that render in one kind, their
// [line.Annotation.Kind] or [kind.UIAnnotation] for the zero Kind, so one
// [kind.UIAnnotation] row can hold annotations of the zero Kind alongside
// ones of that kind. The printer pads the row to Col as
// [AnnotationContext.ColWidth] measures it, writes Marker, then Text, and
// styles the row in the kind the annotations render in, so the func
// decides what the row says and where it starts and the printer decides
// how it looks. When the content wraps, the printer measures the padding
// from the start of the wrapped row that holds Col, so the row starts in
// the cell Col takes on that row.
type AnnotationRow struct {
	// Marker is the text between the padding and Text on the first row,
	// such as the "^ " [DefaultAnnotation] puts before a [line.Below]
	// annotation. When Text wraps, its continuation rows indent past the
	// Marker, so they align under the start of Text. When the column
	// leaves Text less room than its widest word, the Marker keeps its
	// column on a row of its own, and Text moves to the rows below under
	// a smaller indent. That indent leaves Text 20 cells, or its widest
	// word when that is wider, and shrinks to nothing when the printer
	// width has less room.
	Marker string

	// Text is the body of the row. A newline in it starts a new row, and
	// every other control character renders as its picture, so an escape
	// sequence in a message shows as text rather than styling the output.
	Text string

	// Kind is the style the row renders in when it is not the zero Kind,
	// in place of the kind the annotations render in.
	Kind kind.Kind

	// Col is the column of the content the row starts under, in runes of
	// [AnnotationContext.Content]. A negative Col starts at column zero,
	// and a Col more than 1024 columns past the end of the content starts
	// 1024 columns past it.
	Col int
}

// AnnotationFunc renders the annotations an [AnnotationContext] holds as
// one [AnnotationRow], and reports false to leave them out, so the
// printer drops the rows they would take.
//
// The printer does the padding, escaping, wrapping, and styling, so a
// func returns the text of the row and the column it starts under:
//
//	func(ctx printer.AnnotationContext) (printer.AnnotationRow, bool) {
//		kept := ctx.Annotations.WithContent()
//		if len(kept) == 0 {
//			return printer.AnnotationRow{}, false
//		}
//
//		return printer.AnnotationRow{
//			Col:    kept.Col(),
//			Marker: "-> ",
//			Text:   strings.Join(kept.Contents(), " | "),
//		}, true
//	}
type AnnotationFunc func(AnnotationContext) (AnnotationRow, bool)

// NoAnnotation is an [AnnotationFunc] that renders nothing, so the printer
// leaves out the rows annotations would take.
func NoAnnotation(AnnotationContext) (AnnotationRow, bool) {
	return AnnotationRow{}, false
}

// DefaultAnnotation is the [AnnotationFunc] [New] uses. It joins the
// annotations with "; " at their column, [line.Annotations.Col], and
// marks [line.Below] annotations with "^ ". It leaves out annotations
// with empty content. When none remain, an annotation below the line
// still marks it. The row is then a caret under every column the line's
// overlays cover, as [line.View.String] and [line.Overlays.MarkerRow]
// draw them, so a marked range shows its extent without color, or
// nothing when the overlays cover no column. An annotation above the
// line with no content renders nothing, as [line.Annotation.String]
// does. A newline in an annotation renders as its picture rather than
// starting a row, as every other control character does.
func DefaultAnnotation(ctx AnnotationContext) (AnnotationRow, bool) {
	// Filter the annotations rather than their contents, so the column
	// comes from the ones that remain.
	kept := ctx.Annotations.WithContent()
	if len(kept) == 0 {
		if ctx.Placement != line.Below {
			return AnnotationRow{}, false
		}

		col, ok := markerCol(ctx.Overlays, ctx.Content)
		if !ok {
			return AnnotationRow{}, false
		}

		// MarkerRow pads the carets to their column itself, and the
		// printer pads the row to Col, so the carets go without the
		// padding.
		return AnnotationRow{
			Col:  col,
			Text: strings.TrimLeft(ctx.Overlays.MarkerRow(ctx.Content), " "),
		}, true
	}

	row := AnnotationRow{
		Col:  kept.Col(),
		Text: escape.Control(strings.Join(kept.Contents(), "; ")),
	}

	if ctx.Placement == line.Below {
		row.Marker = "^ "
	}

	return row, true
}

// markerCol returns the first column of content that an overlay covers,
// the one [line.Overlays.MarkerRow] puts its first caret under, and false
// when no overlay covers a column of the content.
func markerCol(overlays line.Overlays, content string) (int, bool) {
	var (
		col   int
		found bool
	)

	end := utf8.RuneCountInString(content)

	for _, ov := range overlays {
		start := max(0, ov.Cols.Start)
		if start >= min(ov.Cols.End, end) {
			continue
		}

		if !found || start < col {
			col, found = start, true
		}
	}

	return col, found
}

// renderLineNumber renders the line number portion of a gutter. The number
// column is at least four wide and grows to fit the largest line number in
// the view, so every row of the view lines up. A line with no number, such
// as the placeholder a side-by-side diff inserts opposite an inserted or
// deleted line, gets a blank column.
func renderLineNumber(ctx GutterContext) string {
	lineNumStyle := ctx.styler().Style(kind.UILineNumber)

	width := numberWidth(ctx.MaxNumber)

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
		return ctx.styler().Style(kind.Text).Render(" ")
	}

	if ctx.Soft {
		switch ctx.Flag {
		case line.FlagInserted:
			return ctx.styler().Style(kind.GenericInserted).Render(" ")
		case line.FlagDeleted:
			return ctx.styler().Style(kind.GenericDeleted).Render(" ")
		default:
			return ctx.styler().Style(kind.Text).Render(" ")
		}
	}

	switch ctx.Flag {
	case line.FlagInserted:
		return ctx.styler().Style(kind.GenericInserted).Render("+")
	case line.FlagDeleted:
		return ctx.styler().Style(kind.GenericDeleted).Render("-")
	default:
		return ctx.styler().Style(kind.Text).Render(" ")
	}
}

// numberWidth returns the width of the line number column for a view
// whose largest line number is maxNumber: at least four, and as many
// digits as the number has.
func numberWidth(maxNumber int) int {
	return max(4, len(strconv.Itoa(maxNumber)))
}

var (
	// DefaultGutter is the [Gutter] [New] uses. It renders the line number
	// followed by the diff marker.
	DefaultGutter Gutter = numberGutter{marker: true}

	// DiffGutter is a [Gutter] that renders diff markers only (" ", "+",
	// "-"), styled with [kind.GenericInserted] and [kind.GenericDeleted].
	DiffGutter Gutter = diffGutter{}

	// LineNumberGutter is a [Gutter] that renders line numbers only, in
	// [kind.UILineNumber]. Soft-wrapped continuation rows show " - ".
	LineNumberGutter Gutter = numberGutter{}

	// NoGutter is a [Gutter] that renders nothing.
	NoGutter Gutter = noGutter{}
)

// numberGutter is the [Gutter] behind [DefaultGutter] and
// [LineNumberGutter]: the line number column, followed by the diff marker
// when marker is set.
type numberGutter struct {
	marker bool
}

// Width implements [Gutter] by measuring what the gutter renders for ctx,
// as [GutterFunc] does, so it measures with the styles it renders with.
func (g numberGutter) Width(ctx GutterContext) int {
	return lipgloss.Width(g.Render(ctx))
}

// Render implements [Gutter].
func (g numberGutter) Render(ctx GutterContext) string {
	if g.marker {
		return renderLineNumber(ctx) + renderDiffMarker(ctx)
	}

	return renderLineNumber(ctx)
}

// diffGutter is the [Gutter] behind [DiffGutter].
type diffGutter struct{}

// Width implements [Gutter] by measuring what the gutter renders for ctx.
func (g diffGutter) Width(ctx GutterContext) int {
	return lipgloss.Width(g.Render(ctx))
}

// Render implements [Gutter].
func (diffGutter) Render(ctx GutterContext) string {
	return renderDiffMarker(ctx)
}

// noGutter is the [Gutter] behind [NoGutter].
type noGutter struct{}

// Width implements [Gutter].
func (noGutter) Width(GutterContext) int {
	return 0
}

// Render implements [Gutter].
func (noGutter) Render(GutterContext) string {
	return ""
}

// WithContainerStyle is an [Option] that sets the [lipgloss.Style]
// wrapped around the whole rendered output. By default the container is the
// theme's [kind.Text] style with one cell of right padding.
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

// WithContainerWidth is an [Option] that pins the width of the container
// style's box to n columns. [Printer.Print] pads every row it renders with
// [kind.Text] spaces out to n less the container's horizontal frame, so the
// frame sits in the same columns whatever the widest row of the view is. A
// width of 0, the default, lets the container shrink to the widest row.
//
// Pad a row, never cut one. A row can still run past n, since an
// annotation column may push the layout wider than [WithWrap] and a style
// transform may widen a row after it wraps. A viewer that scrolls
// horizontally cuts the rows itself.
//
// Use it for a viewer that renders one window of a document at a time, where
// a window of short lines would otherwise draw a narrower box than the one
// beside it.
func WithContainerWidth(n int) Option {
	return func(p *Printer) {
		p.containerWidth = max(0, n)
	}
}

// WithStyles is an [Option] that sets the [style.Styler], typically a
// theme from [go.jacobcolvin.com/niceyaml/style/theme], that styles tokens,
// gutters, and annotations. A nil s selects [style.Default].
//
// To style the frame around the output, use [WithContainerStyle].
func WithStyles(s style.Styler) Option {
	return func(p *Printer) {
		if s == nil {
			s = style.Default()
		}

		p.styles = s
		p.blends = newBlendCache()
	}
}

// WithGutter is an [Option] that sets the [Gutter] for rendering.
// By default, [DefaultGutter] renders line numbers and diff markers. A nil
// g selects [NoGutter].
func WithGutter(g Gutter) Option {
	return func(p *Printer) {
		if g == nil {
			g = NoGutter
		}

		p.gutter = g
	}
}

// WithAnnotation is an [Option] that sets the [AnnotationFunc] that
// renders annotations. By default, [DefaultAnnotation] joins them and
// marks [line.Below] annotations with "^ ". [NoAnnotation] leaves
// annotations out. A nil fn selects [DefaultAnnotation].
func WithAnnotation(fn AnnotationFunc) Option {
	return func(p *Printer) {
		if fn == nil {
			fn = DefaultAnnotation
		}

		p.annotationFunc = fn
	}
}

// WithWrap is an [Option] that sets the width for word wrapping.
// A width of 0, the default, disables wrapping, and a negative width
// counts as 0. The width of the box around the output is
// [WithContainerWidth].
func WithWrap(width int) Option {
	return func(p *Printer) {
		p.wrap = max(0, width)
	}
}

// WithMaxNumber is an [Option] that sets the smallest line number the gutter
// sizes itself for. The gutter fits the larger of n and the largest number
// in the view, so a number the view holds never overflows it. A max number
// of 0, the default, takes the number from the view alone.
//
// Use it to give two views the same gutter width, as a side-by-side diff
// needs when one revision is longer than the other.
func WithMaxNumber(n int) Option {
	return func(p *Printer) {
		p.maxNumber = max(0, n)
	}
}

// WithContextLines is an [Option] that sets the number of context lines
// [Printer.PrintError] shows around each error location. The default is
// [DefaultContextLines], and a negative count shows the error lines alone,
// as 0 does.
func WithContextLines(n int) Option {
	return func(p *Printer) {
		p.contextLines = max(0, n)
	}
}

// Wrap returns the width used for word wrapping, or 0 when wrapping is
// disabled.
func (p *Printer) Wrap() int {
	return p.wrap
}

// ContainerWidth returns the width the container style's box is pinned to,
// or 0 when it shrinks to the widest row. See [WithContainerWidth].
func (p *Printer) ContainerWidth() int {
	return p.containerWidth
}

// ContextLines returns the number of context lines [Printer.PrintError]
// shows around each error location.
func (p *Printer) ContextLines() int {
	return p.contextLines
}

// MaxNumber returns the line number the gutter sizes itself for when
// rendering view: the larger of the number [WithMaxNumber] set and the
// largest line number in the view.
func (p *Printer) MaxNumber(view *line.View) int {
	return max(p.maxNumber, maxNumber(view))
}

// ContainerStyle returns the [lipgloss.Style] wrapped around the whole
// rendered output. See [WithContainerStyle].
func (p *Printer) ContainerStyle() lipgloss.Style {
	return p.style
}

// Style retrieves the [lipgloss.Style] for the given [kind.Kind] from the
// printer's [style.Styler].
func (p *Printer) Style(s kind.Kind) lipgloss.Style {
	return p.styles.Style(s)
}

// Fprint renders view to w as [Printer.Print] does.
//
// It returns the number of bytes written and any write error encountered.
func (p *Printer) Fprint(w io.Writer, view *line.View) (int, error) {
	n, err := io.WriteString(w, p.Print(view))
	if err != nil {
		return n, fmt.Errorf("write rendered output: %w", err)
	}

	return n, nil
}

// Print renders every line of view, one row per line plus a row for each
// wrapped piece and each annotation, and wraps the result in the container
// style. To print part of a document, pass the view [line.View.Slice]
// returns. An empty view renders as the container around one empty row.
//
// The container shrinks to the widest row unless [WithContainerWidth] pins
// its width.
func (p *Printer) Print(view *line.View) string {
	return p.style.Render(strings.Join(p.padRows(p.renderRows(view)), "\n"))
}

// padRows pads rows out to the container width, which pins the width of the
// box the container style draws around them. It returns rows unchanged when
// the container width is 0, and pads a row that already reaches the width
// by nothing at all.
func (p *Printer) padRows(rows []string) []string {
	if p.containerWidth == 0 {
		return rows
	}

	// An empty view renders as one empty row, which carries the width for
	// the frame around it.
	if len(rows) == 0 {
		rows = []string{""}
	}

	width := max(0, p.containerWidth-p.style.GetHorizontalFrameSize())
	textStyle := p.Style(kind.Text)

	for i, row := range rows {
		if pad := width - lipgloss.Width(row); pad > 0 {
			rows[i] = row + textStyle.Render(strings.Repeat(" ", pad))
		}
	}

	return rows
}

// maxNumber returns the largest line number in view, or 0 when the view is
// empty. A view built from part of a document, such as a diff hunk or a
// slice of its lines, numbers its lines past its length.
func maxNumber(view *line.View) int {
	n := 0

	for _, ln := range view.All() {
		n = max(n, ln.Number())
	}

	return n
}

// gutterWidth returns the width of the gutter for a view whose largest line
// number is maxNumber, which the [Gutter] declares for a context holding
// that number and the printer's styles.
func (p *Printer) gutterWidth(maxNumber int) int {
	return max(0, p.gutter.Width(GutterContext{
		Styles:    p.styles,
		Number:    maxNumber,
		MaxNumber: maxNumber,
	}))
}

// renderGutter renders the gutter for ctx at width cells: the text the
// [Gutter] renders, padded with spaces in [kind.Text] to width or cut to
// it, so every row of a view starts its content in the same column
// whatever the gutter renders for the row.
func (p *Printer) renderGutter(ctx GutterContext, width int) string {
	text := p.gutter.Render(ctx)

	if lipgloss.Width(text) > width {
		text = ansi.Truncate(text, width, "")
	}

	// The cut drops a wide rune that straddles the width whole, so a cut
	// row can fall short of the width and gets padded like any other.
	if w := lipgloss.Width(text); w < width {
		text += p.styles.Style(kind.Text).Render(strings.Repeat(" ", width-w))
	}

	return text
}

// renderRows renders the lines of view as rows, with the gutter sized for
// [Printer.MaxNumber].
func (p *Printer) renderRows(view *line.View) []string {
	if view.Count() == 0 {
		return nil
	}

	maxNumber := p.MaxNumber(view)
	gutterWidth := p.gutterWidth(maxNumber)

	// A viewer prints one window of a long document at a time.
	rows := make([]string, 0, view.Count())

	for idx, ln := range view.All() {
		rows = append(rows, p.renderLine(view, idx, ln, maxNumber, gutterWidth)...)
	}

	return rows
}

// renderLine renders line idx of view, which is ln, as rows: its
// annotations above, its content wrapped to the printer width, and its
// annotations below.
func (p *Printer) renderLine(view *line.View, idx int, ln *line.Line, maxNumber, gutterWidth int) []string {
	var rows []string

	pieces, starts := p.wrapLine(view, idx, ln, gutterWidth)

	rows = append(rows, p.renderAnnotation(view, ln, idx, maxNumber, line.Above, gutterWidth, starts)...)

	gutterCtx := GutterContext{
		Index:     idx,
		Number:    ln.Number(),
		MaxNumber: maxNumber,
		Flag:      view.Flag(idx),
		Styles:    p.styles,
	}

	rows = append(rows, p.contentRows(pieces, gutterCtx, gutterWidth)...)

	rows = append(rows, p.renderAnnotation(view, ln, idx, maxNumber, line.Below, gutterWidth, starts)...)

	return rows
}

// runSpan places one styled run of a line in both the content and the
// shown text, the rendered line without its escape sequences. Offsets and
// lengths count runes.
type runSpan struct {
	text     string // The escaped content the run covers.
	col      int    // The column of the content at which the run starts.
	cols     int    // The number of columns of the content the run covers.
	shown    int    // The offset in the shown text at which the run starts.
	shownLen int    // The number of runes the run shows.
}

// renderRuns renders the content of line idx of view with its overlays,
// one styled run per run of segments from [line.View.Segments] that
// style the same. A deleted or inserted line takes the diff style for its
// flag, and any other line takes the kind of each segment, with the
// overlays that cover the segment applied over that. It returns the
// rendered content and a [runSpan] for each run, in order.
func (p *Printer) renderRuns(view *line.View, idx int) (string, []runSpan) {
	var base kind.Kind

	switch view.Flag(idx) {
	case line.FlagDeleted:
		base = kind.GenericDeleted

	case line.FlagInserted:
		base = kind.GenericInserted

	case line.FlagDefault:
		// Each segment renders in its own kind.
	}

	var (
		sb      strings.Builder
		run     strings.Builder
		runKey  string
		runSeg  line.Segment
		started bool
		spans   []runSpan
		col     int
		shown   int
	)

	flush := func() {
		if !started {
			return
		}

		// The escape keeps one rune per rune of the content, so the escaped
		// text counts the columns of the run.
		text := escape.Control(run.String())
		rendered := p.blended(runKey, runSeg).Render(text)
		span := runSpan{
			text:     text,
			col:      col,
			cols:     utf8.RuneCountInString(text),
			shown:    shown,
			shownLen: utf8.RuneCountInString(ansi.Strip(rendered)),
		}

		sb.WriteString(rendered)
		run.Reset()

		spans = append(spans, span)
		col += span.cols
		shown += span.shownLen
	}

	for seg := range view.Segments(idx) {
		if base != "" {
			seg.Kind = base
		}

		key := blendKey(seg)
		if !started || key != runKey {
			flush()

			runKey, runSeg, started = key, seg, true
		}

		run.WriteString(seg.Text)
	}

	flush()

	return sb.String(), spans
}

// renderAnnotation renders the annotations of line idx of view, which is
// ln, at the given placement as rows, each with its gutter. Starts holds
// the column of the content at which each wrapped row of the line begins.
// It returns nil when the line has none there or the [AnnotationFunc]
// leaves them out.
func (p *Printer) renderAnnotation(
	view *line.View,
	ln *line.Line,
	idx, maxNumber int,
	placement line.Placement,
	gutterWidth int,
	starts []int,
) []string {
	var rows []string

	for _, group := range p.annotationGroups(view, ln, idx, gutterWidth, placement, starts) {
		for _, row := range group.rows {
			// Every row after the first of the line's annotation block is
			// a continuation, whichever kind group it belongs to.
			gutter := p.renderGutter(GutterContext{
				Index:      idx,
				Number:     ln.Number(),
				MaxNumber:  maxNumber,
				Soft:       len(rows) > 0,
				Flag:       view.Flag(idx),
				Annotation: true,
				Styles:     p.styles,
			}, gutterWidth)

			rows = append(rows, gutter+row)
		}
	}

	return rows
}

// annotationGroup holds the annotations on a line that render in one kind
// as the rows they take once the printer wraps and styles them. Each row
// is one terminal row, without the gutter.
type annotationGroup struct {
	rows []string
}

// minAnnotationWidth is the fewest cells the text of an annotation wraps
// to once it moves off the marker row, when the printer width has room
// for them.
const minAnnotationWidth = 20

// maxColPastEnd is the furthest an annotation row starts past the end of
// the content, in columns, so a far column pads the row by at most this
// many cells past the content.
const maxColPastEnd = 1024

// annotationGroups renders the annotations of line idx of view, which is
// ln, at the given placement: one group per kind, as
// [line.Annotations.ByKind] groups and orders them, each rendered by the
// [AnnotationFunc], wrapped to the printer width, and styled in the style
// of its kind. Starts holds the column of the content at which each
// wrapped row of the line begins. It leaves out a group the func leaves
// out.
func (p *Printer) annotationGroups(
	view *line.View,
	ln *line.Line,
	idx, gutterWidth int,
	placement line.Placement,
	starts []int,
) []annotationGroup {
	anns := view.Annotations(idx).Filter(placement)
	if len(anns) == 0 {
		return nil
	}

	cr := cells.NewRow(ln.Content())
	lastCol := utf8.RuneCountInString(ln.Content()) + maxColPastEnd

	var groups []annotationGroup

	for _, group := range anns.ByKind() {
		row, ok := p.annotationFunc(AnnotationContext{
			Annotations: group,
			Placement:   placement,
			Content:     ln.Content(),
			Overlays:    view.Overlays(idx),
		})
		if !ok {
			continue
		}

		// The padding runs from the start of the wrapped row that holds
		// the column, so the marker lands in the cell the column takes on
		// that row. The column stops at lastCol, so a stray column such as
		// math.MaxInt pads a bounded row.
		col := min(max(0, row.Col), lastCol)

		from := 0
		if len(starts) > 0 {
			from = starts[rowIndex(starts, col)]
		}

		// The printer escapes the text, so a control character in a
		// message shows as its picture and the wrap measures the cells
		// the terminal shows. The escape keeps each newline, and the wrap
		// starts a new row at each one.
		marker := escape.Control(row.Marker)
		mark := strings.TrimRight(marker, " ")
		padding := strings.Repeat(" ", max(0, cr.Width(col)-cr.Width(from)))
		indent := padding + marker
		indentWidth := lipgloss.Width(indent)
		text := escape.Rows(row.Text)
		widest := widestWord(text)

		k := row.Kind
		if k == "" {
			k = annotationKind(group[0].Kind)
		}

		kindStyle := p.styles.Style(k)

		// The style of the kind may lay a row out over several terminal
		// rows, through a width, vertical padding, a margin, or a border.
		// The group keeps each terminal row apart, so Print puts the
		// gutter on each one and Layout counts each one.
		var rows []string

		add := func(s string) {
			rows = append(rows, strings.Split(kindStyle.Render(s), "\n")...)
		}

		// The indent is the padding to the column plus the marker, and
		// the wrap leaves it out. The first row keeps it as rendered, and
		// continuation rows get the same width in spaces, so every row of
		// the text starts under the start of the text.
		//
		// When the column leaves the text less room than its widest word,
		// the marker keeps its column on a row of its own, without its
		// trailing spaces, and the text moves to the rows below under a
		// smaller indent. That indent leaves the text minAnnotationWidth
		// cells, or its widest word when that is wider, and shrinks to
		// nothing when the width has less room. A row without a marker
		// keeps its text at the column, since only the marker holds the
		// column once the text moves. A column past the end of the
		// content keeps its cell even when that cell lies past the width,
		// and the rows indented to it then run wider.
		if p.wrap > 0 && mark != "" && widest > p.contentWidth(gutterWidth+indentWidth) {
			hang := max(0, p.contentWidth(gutterWidth)-max(minAnnotationWidth, widest))

			add(padding + mark)

			for _, wrapped := range p.wrapContent(text, gutterWidth+hang) {
				add(strings.Repeat(" ", hang) + wrapped)
			}
		} else {
			for j, wrapped := range p.wrapContent(text, gutterWidth+indentWidth) {
				prefix := indent
				if j > 0 {
					prefix = strings.Repeat(" ", indentWidth)
				}

				add(prefix + wrapped)
			}
		}

		groups = append(groups, annotationGroup{rows: rows})
	}

	return groups
}

// annotationKind returns the style an annotation of k renders in:
// [kind.UIAnnotation] for the zero Kind, and k itself otherwise.
func annotationKind(k kind.Kind) kind.Kind {
	if k == "" {
		return kind.UIAnnotation
	}

	return k
}

// widestWord returns the width in cells of the widest word of text, where
// words run between newlines and the characters the wrap breaks on.
func widestWord(text string) int {
	words := strings.FieldsFunc(text, func(r rune) bool {
		return r == '\n' || strings.ContainsRune(wrapOnCharacters, r)
	})

	widest := 0
	for _, word := range words {
		widest = max(widest, lipgloss.Width(word))
	}

	return widest
}

// contentRows returns the wrapped pieces of a line's rendered content as
// rows, each with the gutter generated from gutterCtx.
//
// Callers style and escape the whole line before they wrap it, overlays
// included, so the wrap measures the text the terminal shows and each
// overlay covers the columns it names in the source line.
func (p *Printer) contentRows(subLines []string, gutterCtx GutterContext, gutterWidth int) []string {
	rows := make([]string, 0, len(subLines))

	for j, subLine := range subLines {
		// Generate gutter at write-time with correct Soft flag.
		ctx := gutterCtx
		ctx.Soft = j > 0

		rows = append(rows, p.renderGutter(ctx, gutterWidth)+subLine)
	}

	return rows
}

// blendKey returns the cache key of the effective style of seg: its kind
// followed by each overlay that covers it, in order, marked by whether it
// blends with or replaces the style underneath. Two segments with the
// same key render with the same style. It quotes each name, so a name
// that contains a marker cannot collide with a different overlay
// sequence.
func blendKey(seg line.Segment) string {
	var sb strings.Builder

	sb.WriteString(strconv.Quote(string(seg.Kind)))

	for _, ov := range seg.Overlays {
		if ov.Blend {
			sb.WriteString("+")
		} else {
			sb.WriteString("!")
		}

		sb.WriteString(strconv.Quote(string(ov.Kind)))
	}

	return sb.String()
}

// blended returns the style for key, which [blendKey] built from seg. It
// computes the style on the first request and serves the cache after
// that. The overlays apply in order. A blending overlay mixes with the
// result so far, and any other replaces it.
func (p *Printer) blended(key string, seg line.Segment) lipgloss.Style {
	if st, ok := p.blends.get(key); ok {
		return st
	}

	result := p.styles.Style(seg.Kind)

	for _, ov := range seg.Overlays {
		if ov.Blend {
			result = colors.BlendStyles(result, p.styles.Style(ov.Kind))
		} else {
			result = colors.OverrideStyles(result, p.styles.Style(ov.Kind))
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
	if p.wrap <= 0 {
		return 0
	}

	return max(1, p.wrap-gutterWidth)
}

// wrapContent splits content for word wrapping if enabled.
func (p *Printer) wrapContent(content string, gutterWidth int) []string {
	cw := p.contentWidth(gutterWidth)
	if cw <= 0 {
		// A newline in the content is a row of its own with or without
		// wrapping, so the row count matches what Print writes.
		return strings.Split(content, "\n")
	}

	var out []string

	for text := range strings.SplitSeq(content, "\n") {
		out = append(out, wrapLine(text, cw)...)
	}

	return out
}

// wrapLine wraps text, which holds no newline, to rows of at most cw
// cells. A grapheme cluster wider than cw takes a row of its own and runs
// past the width.
func wrapLine(text string, cw int) []string {
	rows := strings.Split(lipgloss.Wrap(text, cw, wrapOnCharacters), "\n")

	// The wrap leaves a row wider than cw when a breakpoint falls just
	// past the width, so a hard wrap cuts such a row down to size, and
	// every row fits the width the caller asked for. The hard wrap does
	// not reopen a style on the rows it cuts off, and a second pass of
	// the wrap, which does, restores it.
	out := make([]string, 0, len(rows))

	for _, row := range rows {
		if lipgloss.Width(row) <= cw {
			out = append(out, row)

			continue
		}

		cut := lipgloss.Wrap(ansi.Hardwrap(row, cw, true), cw, "")
		out = append(out, strings.Split(cut, "\n")...)
	}

	// After the cut, a row stays wider than cw only when it holds a lone
	// cluster the width cannot fit. Both wraps break ahead of such a
	// cluster even at the start of a row, which leaves rows with no text
	// on them, so those rows go. When a style opens just before the
	// cluster, the wrap also puts the ASCII spaces ahead of it on rows of
	// their own, so rows of only those spaces that lead up to the cluster
	// go too. Any other rune stays, even one with no width, so the rows
	// still spell out the content for the layout.
	tooWide := func(row string) bool { return lipgloss.Width(row) > cw }
	if !slices.ContainsFunc(out, tooWide) {
		return out
	}

	// The walk starts at the last row, so each row of spaces can check the
	// kept row that follows it.
	kept := make([]string, 0, len(out))
	beforeWide := false

	for _, row := range slices.Backward(out) {
		plain := ansi.Strip(row)
		if plain == "" {
			continue
		}

		if beforeWide && strings.Trim(plain, " ") == "" {
			continue
		}

		beforeWide = tooWide(row)
		kept = append(kept, row)
	}

	slices.Reverse(kept)

	return kept
}
