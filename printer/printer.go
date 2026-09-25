package printer

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

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
// [lipgloss.Style]s, with customizable gutters, annotations, styled
// overlays, and word wrapping.
//
// A Printer is immutable after construction and safe for concurrent use.
// Every setting is an [Option]; to change one on an existing Printer,
// derive a copy with [Printer.With]:
//
//	narrow := printer.With(printer.WithWrap(40))
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
// # Gutters
//
// Gutters appear at the left edge of each row and typically show line numbers
// or diff markers. The printer uses [DefaultGutter] by default, which combines
// line numbers with diff markers (+/-). Other built-in options include
// [DiffGutter] (markers only), [LineNumberGutter] (numbers only), and [NoGutter].
// A [Gutter] of your own declares its width and renders each row, and a
// [GutterFunc] adapts a function.
//
// # Overlays
//
// Overlays style column spans within lines. Add them to a [line.View] with
// [line.View.AddOverlay], which replaces the style underneath, or
// [line.View.BlendOverlay], which mixes with it, then print the view. Error
// positions use the first and search highlights the second, so a match keeps
// the token or diff color it covers.
//
// # Annotations
//
// Annotations are extra text rows above or below a line, outside the token
// stream. They display error messages, diff hunk headers, or other
// contextual notes. Each annotation renders in the style of its
// [line.Annotation.Kind], or [kind.UIAnnotation] when it has none, and the
// annotations of one Kind on a line share their rows. The printer renders
// the text of each such group via [AnnotationFunc], defaulting to
// [DefaultAnnotation] which prefixes below-line annotations with "^ " and
// draws a caret under every column the line's overlays cover for a
// below-line annotation without content.
//
// # Word Wrapping
//
// Pass [WithWrap] to enable word wrapping at a given width. The printer
// subtracts the gutter width from it. A wrapped continuation row shows a
// "-" marker in the gutter. A width of 0 turns wrapping off.
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
// It is the count the %+v verb of a [*niceyaml.SourceError] uses.
const DefaultContextLines = 2

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
//	wrapped := printer.With(printer.WithWrap(80))
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
// every row of the view lines up. Soft marks a wrapped continuation row
// of that line, and Annotation marks a row that holds one of its
// annotations rather than its content, so the built-in gutters leave the
// line number and diff marker out of it.
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

// AnnotationContext provides context for annotation rendering.
//
// The printer passes it to [AnnotationFunc] to determine the annotation
// content. Annotations holds the annotations of one line, placement, and
// [line.Annotation.Kind], so the func renders them as one piece of text and
// the printer styles it with that Kind.
type AnnotationContext struct {
	Styles style.Styler

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
// renders, in which a control character shows as a one-cell picture, and a
// combining mark has no cell of its own, so a column on one measures up to
// the rune it renders on. A viewer that scrolls horizontally adds the
// result to [Layout.GutterWidth] to find the cell a column of the line
// occupies.
func ColWidth(content string, col int) int {
	col = max(0, col)
	runes := []rune(content)

	// A combining mark has no cell of its own. It renders on the rune
	// before it, so a marker at its column lands under that rune.
	for col > 0 && col < len(runes) && unicode.In(runes[col], unicode.Mn, unicode.Me) {
		col--
	}

	if col <= len(runes) {
		return lipgloss.Width(escape.Control(string(runes[:col])))
	}

	return lipgloss.Width(escape.Control(content)) + col - len(runes)
}

// AnnotationFunc returns the rendered annotation content based on
// [AnnotationContext].
type AnnotationFunc func(AnnotationContext) string

// NoAnnotation is an [AnnotationFunc] that renders nothing, so the printer
// leaves out the rows annotations would take.
func NoAnnotation(AnnotationContext) string {
	return ""
}

// DefaultAnnotation is the [AnnotationFunc] [New] uses. It joins the
// annotations with "; ", pads them to their column as
// [AnnotationContext.ColWidth] measures it, and prefixes [line.Below]
// annotations with "^ ". It leaves out annotations with empty content.
// When none remain, an annotation below the line still marks it. The
// result is then [line.Overlays.MarkerRow], a caret under every column the
// line's overlays cover, as [line.View.String] draws them, so a marked
// range shows its extent without color, or "" when the overlays cover no
// column. An annotation
// above the line with no content renders as "", as
// [line.Annotation.String] does. Control characters in the content render
// as their pictures, so an escape sequence in a message shows as text.
func DefaultAnnotation(ctx AnnotationContext) string {
	// Filter the annotations rather than their contents, so the column
	// comes from the ones that remain. WithContent returns a new slice,
	// so the escaping below leaves the caller's annotations alone.
	kept := ctx.Annotations.WithContent()
	if len(kept) == 0 {
		if ctx.Placement == line.Below {
			return ctx.Overlays.MarkerRow(ctx.Content)
		}

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
// prefixes [line.Below] annotations with "^ ". [NoAnnotation] leaves
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
		p.contextLines = n
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

	switch w := lipgloss.Width(text); {
	case w < width:
		return text + p.styles.Style(kind.Text).Render(strings.Repeat(" ", width-w))

	case w > width:
		return ansi.Truncate(text, width, "")

	default:
		return text
	}
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

	rows = append(rows, p.renderAnnotation(view, ln, idx, maxNumber, line.Above, gutterWidth)...)

	gutterCtx := GutterContext{
		Index:     idx,
		Number:    ln.Number(),
		MaxNumber: maxNumber,
		Flag:      view.Flag(idx),
		Styles:    p.styles,
	}

	rows = append(rows, p.contentRows(p.renderContent(view, idx), gutterCtx, gutterWidth)...)

	rows = append(rows, p.renderAnnotation(view, ln, idx, maxNumber, line.Below, gutterWidth)...)

	return rows
}

// renderContent renders the content of line idx of view with its
// overlays, one styled run per run of segments from [line.View.Segments]
// that style the same: a deleted or inserted line in the diff style for
// its flag, and any other line in the kind of each segment, with the
// overlays that cover the segment applied over that.
func (p *Printer) renderContent(view *line.View, idx int) string {
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
	)

	flush := func() {
		if started {
			sb.WriteString(p.blended(runKey, runSeg).Render(escape.Control(run.String())))
			run.Reset()
		}
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

	return sb.String()
}

// renderAnnotation renders the annotations of line idx of view, which is
// ln, at the given placement as rows, each with its gutter. It returns nil
// when the line has none there or the [AnnotationFunc] renders them as
// nothing.
func (p *Printer) renderAnnotation(
	view *line.View,
	ln *line.Line,
	idx, maxNumber int,
	placement line.Placement,
	gutterWidth int,
) []string {
	var rows []string

	for _, group := range p.annotationGroups(view, ln, idx, gutterWidth, placement) {
		for j, subLine := range group.rows {
			var sb strings.Builder

			// Every row after the first of the line's annotation block is
			// a continuation, whichever kind group it belongs to.
			sb.WriteString(p.renderGutter(GutterContext{
				Index:      idx,
				Number:     ln.Number(),
				MaxNumber:  maxNumber,
				Soft:       len(rows) > 0,
				Flag:       view.Flag(idx),
				Annotation: true,
				Styles:     p.styles,
			}, gutterWidth))

			prefix := group.indent
			if j > 0 {
				prefix = strings.Repeat(" ", group.indentWidth)
			}

			sb.WriteString(p.styles.Style(group.kind).Render(prefix + subLine))

			rows = append(rows, sb.String())
		}
	}

	return rows
}

// annotationGroup is the rendered text of the annotations of one Kind on a
// line: the style to render it in, the indent every row aligns under, and
// the rows the body wraps to.
type annotationGroup struct {
	indent      string
	kind        kind.Kind
	rows        []string
	indentWidth int
}

// annotationGroups renders the annotations of line idx of view, which is
// ln, at the given placement: one group per [line.Annotation.Kind], as
// [line.Annotations.ByKind] orders them, each rendered by the
// [AnnotationFunc] and wrapped to the printer width. It leaves out a group
// the func renders as nothing.
func (p *Printer) annotationGroups(
	view *line.View,
	ln *line.Line,
	idx, gutterWidth int,
	placement line.Placement,
) []annotationGroup {
	anns := view.Annotations(idx).Filter(placement)
	if len(anns) == 0 {
		return nil
	}

	var groups []annotationGroup

	for _, group := range anns.ByKind() {
		content := p.annotationFunc(AnnotationContext{
			Annotations: group,
			Placement:   placement,
			Styles:      p.styles,
			Content:     ln.Content(),
			Overlays:    view.Overlays(idx),
		})
		if content == "" {
			continue
		}

		// The func owns escaping, so styling it applied through
		// ctx.Styles survives. The wrap is ANSI-aware and measures the
		// shown cells.
		//
		// The indent stays out of the wrapped text and comes back on
		// every row. The first row keeps it as rendered and continuation
		// rows get the same width in spaces, so the annotation column
		// survives the wrap. An annotation column past the width wins
		// over the width, and its rows then run wider, since the body
		// still gets one column.
		indent, body := splitAnnotationIndent(content, placement)
		indentWidth := lipgloss.Width(indent)

		groups = append(groups, annotationGroup{
			kind:        annotationKind(group[0].Kind),
			indent:      indent,
			indentWidth: indentWidth,
			rows:        p.wrapContent(body, gutterWidth+indentWidth),
		})
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

	rows := strings.Split(lipgloss.Wrap(content, cw, wrapOnCharacters), "\n")

	// The wrap leaves a row wider than cw when a breakpoint falls just
	// past the width, so a hard wrap cuts such a row down to size, and
	// every row fits the width the caller asked for.
	out := make([]string, 0, len(rows))

	for _, row := range rows {
		if lipgloss.Width(row) <= cw {
			out = append(out, row)

			continue
		}

		out = append(out, strings.Split(ansi.Hardwrap(row, cw, true), "\n")...)
	}

	return out
}
