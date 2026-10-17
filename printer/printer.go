package printer

import (
	"fmt"
	"iter"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"go.jacobcolvin.com/niceyaml/internal/caret"
	"go.jacobcolvin.com/niceyaml/internal/cells"
	"go.jacobcolvin.com/niceyaml/internal/clip"
	"go.jacobcolvin.com/niceyaml/internal/colors"
	"go.jacobcolvin.com/niceyaml/internal/escape"
	"go.jacobcolvin.com/niceyaml/internal/nilness"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

// breakpoints are the characters other than a space that the wrap breaks
// after. The wrap keeps each with the word before it.
const breakpoints = "/-"

// wrapOnCharacters are the characters the wrap breaks on.
const wrapOnCharacters = " " + breakpoints

// Printer prints YAML with syntax highlighting for terminal output.
//
// It accepts a [line.View], such as the view of a niceyaml Source, and
// renders its YAML tokens as styled terminal output with [lipgloss.Style]
// values. Options customize its gutters, annotations, styled overlays, and
// word wrapping, and the package documentation covers each of them.
//
// A Printer is immutable after construction and safe for concurrent use.
// Every setting is an [Option]. To change one on an existing Printer,
// derive a copy with [Printer.With]:
//
//	narrow := p.With(printer.WithWrapWidth(40))
//
// Create instances with [New].
//
// # Rendering
//
// Use [Printer.Print] to render every line of a view. To render part of a
// document, such as the lines around an error or the hunks of a diff, print
// the view [line.View.Slice] returns. Its lines keep their numbers:
//
//	p.Print(view)                       // All lines.
//	p.Print(view.Slice(span1, span2))   // The lines of two spans.
//
// [Printer.PrintSideBySide] renders two views as panes beside each other,
// such as the two revisions of a side-by-side diff.
//
// The output holds 24-bit color escape sequences whatever the destination
// supports. The package documentation covers the functions that fit them
// to a terminal, a pipe, or a file.
//
// # Errors
//
// [Printer.PrintError] renders any error as a tree, with a connector in
// front of each error below another. It adds the source excerpt of every
// [niceyaml.SourceError] in the error. It draws both with the printer's
// styles and width and the context lines a [niceyaml.ExcerptOption] sets.
// A program configures one printer with its terminal width and theme and
// prints its errors through it.
type Printer struct {
	config
}

// New creates a new [*Printer].
// By default it uses [style.Default], [DefaultGutter], [DefaultAnnotation],
// and an empty container style, so each row of the output is as wide as
// its content. [WithContainerWidth] pins a width instead.
func New(opts ...Option) *Printer {
	p := &Printer{
		styles:         style.Default(),
		gutter:         DefaultGutter,
		annotationFunc: DefaultAnnotation,
		blends:         newBlendCache(),
	}

	p.apply(opts)

	return p
}

// With returns a copy of the [Printer] with the given options applied. The
// receiver is unchanged, so callers can specialize a shared Printer per call:
//
//	wrapped := p.With(printer.WithWrapWidth(80))
//
// The copy shares the receiver's cache of blended styles unless [WithStyles]
// is among the options. The cache is safe for concurrent use.
func (p *Printer) With(opts ...Option) *Printer {
	c := *p
	c.apply(opts)

	return &c
}

// apply runs opts and then, when [WithBox] is in effect, derives the
// container style from the styles they leave. The box thus follows a
// [WithStyles] on either side of it.
func (p *Printer) apply(opts []Option) {
	for _, opt := range opts {
		opt(&p.config)
	}

	if p.box {
		p.style = p.styles.Style(kind.Text).
			PaddingRight(1)
	}
}

// Option configures a [Printer].
//
// Available options:
//   - [WithStyles]
//   - [WithBox]
//   - [WithContainerStyle]
//   - [WithContainerWidth]
//   - [WithGutter]
//   - [WithAnnotation]
//   - [WithWrapWidth]
//   - [WithMaxNumber]
type Option func(*config)

// config holds the settings an [Option] configures, and the cache of
// blended styles that [WithStyles] replaces. An option takes it in place
// of the [Printer] that embeds it, so only [New] and [Printer.With] can
// apply one.
type config struct {
	styles         style.Styler
	style          lipgloss.Style
	gutter         Gutter
	annotationFunc AnnotationFunc
	// The cache of blended styles, keyed by the kinds that produce them.
	// WithStyles replaces it, because the kinds then resolve to other
	// styles.
	blends         *blendCache
	wrap           int
	containerWidth int
	maxNumber      int
	// Whether apply derives the container style from the styles. WithBox
	// sets it, and WithContainerStyle clears it.
	box bool
}

// GutterContext describes the current row for gutter rendering. The
// printer passes it to [Gutter.Render], and the gutter renders its text
// for the row from it.
//
// Index, Number, and Flag describe the line the row belongs to. MaxNumber
// is the number the gutter sizes its line number column for. It is the
// larger of the view's largest line number and the one [WithMaxNumber]
// sets, so every row of the view lines up. Annotation marks a row that
// holds one of the line's annotations rather than its content. The
// built-in gutters leave the line number and the diff marker off such a
// row.
//
// Soft marks a row that continues the one above it. On a content row,
// Soft marks a wrapped continuation of the line. On an annotation row,
// Soft marks every row after the first in its block. A block holds the
// annotation rows of a line at one placement beside one wrapped row of
// its content. The soft rows include the first row of a later
// [AnnotationRow] and a row that starts at a newline in its Text. A
// gutter that draws a wrap marker only beside wrapped content checks
// Annotation before Soft.
type GutterContext struct {
	Styles     style.Styler
	Index      int
	Number     int
	MaxNumber  int
	Flag       line.Flag
	Soft       bool
	Annotation bool
}

// styler returns the styles a gutter renders with. It returns the styles
// the context carries, or [style.Default] when the context carries nil or
// a nil pointer. A gutter called with a zero context thus renders rather
// than panics.
func (c GutterContext) styler() style.Styler {
	if nilness.IsNil(c.Styles) {
		return style.Default()
	}

	return c.Styles
}

// Gutter renders the left edge of each row. The printer asks it for the
// width of the gutter once per view and budgets word wrapping and
// [Layout.GutterWidth] from that width. The printer renders every row's
// gutter at that width. It pads text that falls short with spaces in
// [kind.Text] and cuts text that runs over. The content of every row thus
// starts in the same column, whatever the gutter renders for the row.
//
// The gutter takes one row. When its text holds a newline, as text in a
// style with a margin or a border does, the printer keeps only the first
// row.
//
// Because the printer pads text that falls short, a gutter that shows a
// marker on some rows and nothing on others declares the width of the
// marker and renders what it likes:
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
// ready-made gutters, and [GutterFunc] adapts a function. [WithGutter]
// sets the gutter a printer uses.
type Gutter interface {
	// Width returns the width in cells of the gutter on every row of a
	// view. The context holds the styles the gutter renders with. Both
	// Number and MaxNumber hold the number the gutter sizes for, the
	// larger of the view's largest line number and the one
	// [WithMaxNumber] sets. The printer leaves every other field zero,
	// because the width must not depend on the row.
	Width(ctx GutterContext) int
	// Render returns the gutter text for a row. The printer keeps its
	// first row and pads or cuts that row to Width.
	Render(ctx GutterContext) string
}

// GutterFunc adapts a function to the [Gutter] interface. Its width is
// the width of what the function renders for the context [Gutter.Width]
// receives. A function whose width depends on the line number alone, as
// the built-in gutters do, measures as it renders. For a function whose
// width varies with the flag or the row, the printer pads or cuts every
// row to the width of what the function renders for that context:
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

// AnnotationContext is what the printer passes to an [AnnotationFunc] for
// the annotations of one line and placement that render in one kind, as
// [line.Annotation.RenderKind] reports it. Each annotation keeps its Kind
// as given, so a context for [kind.UIAnnotation] can hold annotations of
// the zero Kind alongside ones of that kind. The func returns the
// [AnnotationRow]s for them, and the printer pads, escapes, wraps, and
// styles each one.
//
// Every column the context holds counts runes of Content. For a line the
// view clips, as a view from [line.View.Clip] does, Content is the text
// of the row. Overlays, Annotations, RowStarts, and RowEnds then hold the
// columns of that row. A func thus marks a clipped line as it marks any
// other.
type AnnotationContext struct {
	// Content is the text of the annotated line, without its line ending.
	// Annotation columns count runes of this text. The display width of
	// the text before a column gives the padding that puts a marker under
	// it. The text of a clipped line holds the windows
	// [line.View.Windows] returns, with "..." in place of each run of
	// columns that the windows leave out.
	Content string

	// Overlays are the overlays of the annotated line, the styles its
	// columns render in. [DefaultAnnotation] does not read the overlays.
	// A func can read them to match the colors of the line. Each context
	// holds a copy, so the func can change it without changing the view.
	Overlays line.Overlays

	// RowStarts holds the column of Content at which each row of the line
	// begins after the printer wraps it, in row order. A func that marks
	// columns can thus return a row for each wrapped row it marks. The
	// first row begins after any leading spaces the wrap drops, so it can
	// begin past column 0. RowStarts holds the single column 0 when the
	// line fits one row as it is, and a nil RowStarts means the same. Each
	// context holds a copy.
	RowStarts []int

	// RowEnds holds the column of Content after the last rune each row of
	// the line shows when the printer wraps it, one for each entry of
	// RowStarts. A row thus ends before the spaces the wrap drops at its
	// break or at the end of the line. When RowEnds is nil, each row ends
	// where the next begins, less the spaces the wrap drops before that
	// start. The last row then ends at the end of Content. Each context
	// holds a copy.
	RowEnds []int

	// Annotations are the annotations the func renders, each with the
	// columns of Content it labels in [line.Annotation.Cols].
	Annotations line.Annotations
	Placement   line.Placement
}

// ColWidth returns the display width of the first col runes of
// ctx.Content, plus one cell for every column past the end of the content.
// A marker padded by that width lands under the rune at col, whatever the
// width of the runes before it. On a line that fits one row, the printer
// pads an [AnnotationRow] whose Col is col by that width. On a wrapped
// line, the printer measures the padding from the start of the wrapped
// row that holds Col instead, as the AnnotationRow doc describes. Like the
// printer, ColWidth stops at [line.MaxColPastEnd] columns past the end, so
// a stray column such as [math.MaxInt] pads a bounded row. See [ColWidth]
// for the rules it measures by.
func (ctx AnnotationContext) ColWidth(col int) int {
	return ColWidth(ctx.Content, annotationCol(ctx.Content, col))
}

// annotationCol returns the column the printer starts an [AnnotationRow]
// at when its Col is col and the row sits beside content. The column lies
// between 0 and [line.MaxColPastEnd] columns past the end of the content,
// inclusive.
func annotationCol(content string, col int) int {
	return min(max(0, col), utf8.RuneCountInString(content)+line.MaxColPastEnd)
}

// ColWidth returns the display width of the first col runes of content,
// plus one cell for every column past the end of the content. The content
// is the text of a line without its line ending.
//
// The width is that of the row the printer renders. In that row, a
// control character shows as the one-cell rune that
// [go.jacobcolvin.com/niceyaml/escape.Control] draws for it. A grapheme
// cluster, such as a letter with a combining mark or an emoji ZWJ
// sequence, takes its display width once. A column inside a cluster does
// not have a cell of its own, so it measures up to the start of its
// cluster.
//
// ColWidth measures the content alone. A viewer that scrolls horizontally
// asks [Layout.CellOf] for the cell of a column instead, because that
// method also counts the cells a style's transform adds.
func ColWidth(content string, col int) int {
	return cells.NewRow(content).Width(col)
}

// Cut returns cells left through right-1 of row, or "" when right is at or
// before left. A negative left counts as 0. The row is one row of what
// [Printer.Print] returns, with the escape sequences of its styles. Cut
// counts the cells of the row as [Layout.CellOf] and [Layout.GutterWidth]
// do, so cell left+k of the row is cell k of the result. A viewer that
// scrolls horizontally thus cuts each row to its window from the cells
// those methods return. Every rune in the window keeps the cell the
// layout gives it.
//
// A grapheme cluster that straddles either edge, such as a wide rune that
// the window shows half of, becomes a space for each of its cells inside
// the window. The spaces take the place of the cluster, so they keep its
// style, such as the background of a search highlight. [Layout.PositionAt]
// gives the column of that cluster for each of them. A zero-width cluster
// at cell right falls outside the result. A keycap takes two cells here,
// but [ansi.Cut] counts one for its ASCII base. Every escape sequence
// stays, so a style the row opens still closes. The result spans
// right-left cells when the row reaches cell right, and fewer when the
// row ends before it. [Printer.Pad] pads such a result to the window.
func Cut(row string, left, right int) string {
	return cells.Cut(row, left, right)
}

// CutWidth returns what [Cut] returns along with its width in cells. Like
// Cut, it counts a negative left as 0. The width is right-left when the
// row reaches cell right, and the cells the row has past left otherwise.
// A viewer that pads the cut to its window pads right-left less that
// width.
func CutWidth(row string, left, right int) (string, int) {
	return cells.CutWidth(row, left, right)
}

// AnnotationRow is a row an [AnnotationFunc] renders for the annotations
// of one line and placement that render in one kind, as
// [line.Annotation.RenderKind] reports it. One [kind.UIAnnotation] row can
// thus hold annotations of the zero Kind alongside ones of that kind.
//
// The printer pads the row to Col as [AnnotationContext.ColWidth] measures
// it, writes Marker, then Text, and styles the row in the kind the
// annotations render in. The func thus decides what the row says and
// where it starts, and the printer decides how it looks.
//
// When the content wraps, the printer writes the row beside the wrapped
// row that holds Col, above it for [line.Above] and below it for
// [line.Below]. It measures the padding from the start of that wrapped
// row, so the row starts in the cell Col takes on it.
type AnnotationRow struct {
	// Marker is the text between the padding and Text on the first row,
	// such as the carets and the space [DefaultAnnotation] puts before the
	// text of a [line.Below] annotation. The printer keeps the Marker
	// whole on one row, even when it runs past the width. A func that
	// marks columns thus puts the marks in the Marker, as
	// [DefaultAnnotation] does with its carets.
	//
	// When Text wraps, its continuation rows indent past the Marker, so
	// they align under the start of Text. When the column leaves Text less
	// room than its widest word, the Marker keeps its column on a row of
	// its own. Text then moves to the rows below under a smaller indent.
	// That indent leaves Text 20 cells, or its widest word when that is
	// wider, and shrinks to zero when the printer width has less room.
	Marker string

	// Text is the body of the row. A newline in it starts a new row. Every
	// other control character renders as
	// [go.jacobcolvin.com/niceyaml/escape.Control] draws it, so an escape
	// sequence in a message shows as text rather than styling the output.
	Text string

	// Kind is the kind the row renders in, in place of the kind its
	// annotations render in. The zero Kind keeps the kind of the
	// annotations.
	Kind kind.Kind

	// Col is the column of the content the row starts under, in runes of
	// [AnnotationContext.Content]. A negative Col starts at column zero. A
	// Col more than [line.MaxColPastEnd] columns past the end of the
	// content starts that many columns past it.
	Col int
}

// AnnotationFunc renders the annotations an [AnnotationContext] holds as
// [AnnotationRow]s, in the order the printer writes them. A func that
// returns zero rows leaves the annotations out of the output. A func
// that describes the annotations returns one row. The printer writes
// each row beside one wrapped row. A func that marks columns of a wrapped
// line thus returns a row for each wrapped row it marks, as
// [DefaultAnnotation] does.
//
// The printer pads, escapes, wraps, and styles each row, so a func
// returns the text of each row and the column it starts under:
//
//	func(ctx printer.AnnotationContext) []printer.AnnotationRow {
//		kept := ctx.Annotations.WithContent()
//		if len(kept) == 0 {
//			return nil
//		}
//
//		return []printer.AnnotationRow{{
//			Col:    kept.Start(),
//			Marker: "-> ",
//			Text:   strings.Join(kept.Contents(), " | "),
//		}}
//	}
type AnnotationFunc func(AnnotationContext) []AnnotationRow

// NoAnnotation is an [AnnotationFunc] that renders nothing, so the printer
// leaves annotations out of the output.
func NoAnnotation(AnnotationContext) []AnnotationRow {
	return nil
}

// DefaultAnnotation is the [AnnotationFunc] [New] uses. Annotations with
// empty content do not add text.
//
// Above the line, it joins the contents of the annotations with "; " in
// column order, as [line.Annotations.WithContent] sorts them. The row
// starts at the first column they start at, which
// [line.Annotations.Start] reports. Annotations without content do not
// render above the line. [line.Annotation.String] also returns "" for
// them.
//
// Below the line, each run of columns that the annotations label in their
// [line.Annotation.Cols] gets a row of its own. A marked range thus shows
// its extent without color. The row holds a caret under each cell that
// the columns of the run take. A rune two cells wide thus gets two
// carets, and a combining mark gets none of its own. The contents of its
// annotations follow the carets after a space. Annotations that label
// the same columns share a row, with their contents joined with "; ". An
// annotation without content gets its carets alone. A run that does not
// cover a column of the content, such as an empty one, gets a single
// caret at its start.
//
// The rows below the line come in the order their runs start, and runs
// that start at the same column come shorter first. [line.View.String]
// arranges the rows below a line the same way. DefaultAnnotation does not
// read the overlays, so the overlays of the line do not get carets.
//
// When the line wraps, a run gets a caret row beside each wrapped row
// that shows some of its columns. The contents follow the carets beside
// the wrapped row that holds the start of the run. That wrapped row might
// not show any column of the run, as when the run starts on a space the
// wrap drops. The contents then follow a single caret at the start of the
// run. Apart from that single caret, a space the wrap drops, at a break
// or at the end of the line, does not get a caret.
//
// Every control character in an annotation renders as
// [go.jacobcolvin.com/niceyaml/escape.Control] draws it, so a newline does
// not start a row.
//
// Every row DefaultAnnotation returns starts at a column between 0 and
// [line.MaxColPastEnd] columns past the end of the content, inclusive,
// where the printer starts any row. A renderer that calls
// DefaultAnnotation itself pads each row by [AnnotationContext.ColWidth]
// of its Col and does not need a bound of its own.
func DefaultAnnotation(ctx AnnotationContext) []AnnotationRow {
	if ctx.Placement != line.Below {
		// Filter the annotations rather than their contents, so the column
		// comes from the ones that remain.
		kept := ctx.Annotations.WithContent()
		if len(kept) == 0 {
			return nil
		}

		return []AnnotationRow{{
			Col:  annotationCol(ctx.Content, kept.Start()),
			Text: escape.Control(strings.Join(kept.Contents(), "; ")),
		}}
	}

	labels := make([]caret.Label, 0, len(ctx.Annotations))

	for _, a := range ctx.Annotations {
		cols := a.Cols
		cols.Start = annotationCol(ctx.Content, cols.Start)

		labels = append(labels, caret.Label{Text: a.Content, Cols: cols})
	}

	caretRows := caret.Rows(ctx.Content, labels, wrappedRows(ctx))
	if len(caretRows) == 0 {
		return nil
	}

	rows := make([]AnnotationRow, 0, len(caretRows))

	for _, r := range caretRows {
		rows = append(rows, AnnotationRow{Col: r.Col, Marker: r.Marker, Text: r.Text})
	}

	return rows
}

// wrappedRows returns the columns of ctx.Content each wrapped row of the
// line shows, from [AnnotationContext.RowStarts] and
// [AnnotationContext.RowEnds]. A row without an entry in RowEnds ends
// where the next row begins, less the spaces the wrap drops before that
// start. The last such row ends at the end of the content. First,
// wrappedRows escapes the content as the printer shows it, so a tab
// counts as its picture rather than a space. It returns nil when
// RowStarts is empty. [caret.Rows] reads nil as one row that shows the
// whole line.
func wrappedRows(ctx AnnotationContext) []position.Span {
	if len(ctx.RowStarts) == 0 {
		return nil
	}

	shown := []rune(escape.Control(ctx.Content))
	rows := make([]position.Span, 0, len(ctx.RowStarts))

	for r, lo := range ctx.RowStarts {
		lo = min(max(0, lo), len(shown))
		hi := len(shown)

		switch {
		case r < len(ctx.RowEnds):
			hi = min(max(lo, ctx.RowEnds[r]), hi)

		case r+1 < len(ctx.RowStarts):
			hi = min(max(lo, ctx.RowStarts[r+1]), hi)

			for hi > lo && isBreakSpace(shown[hi-1]) {
				hi--
			}
		}

		rows = append(rows, position.NewSpan(lo, hi))
	}

	return rows
}

// renderLineNumber renders the line number portion of a gutter. The number
// column is at least four cells wide and grows to fit the largest line
// number in the view. Every row of the view thus lines up. A line without
// a number, such as the placeholder a side-by-side diff inserts opposite
// an inserted or deleted line, gets a blank column.
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
// annotation row does not carry a marker, and it renders in [kind.Text]
// whatever its flag. A soft-wrapped continuation row keeps the color of
// its line but not the marker.
func renderDiffMarker(ctx GutterContext) string {
	k, marker := kind.Text, " "

	if !ctx.Annotation {
		switch ctx.Flag {
		case line.FlagInserted:
			k, marker = kind.GenericInserted, "+"
		case line.FlagDeleted:
			k, marker = kind.GenericDeleted, "-"
		case line.FlagDefault:
		}

		if ctx.Soft {
			marker = " "
		}
	}

	return ctx.styler().Style(k).Render(marker)
}

// numberWidth returns the width of the line number column for a view
// whose largest line number is maxNumber. The width is the larger of four
// and the number of digits in maxNumber.
func numberWidth(maxNumber int) int {
	return max(4, len(strconv.Itoa(maxNumber)))
}

var (
	// DefaultGutter is the [Gutter] [New] uses. It renders the line number
	// followed by the diff marker.
	DefaultGutter Gutter = numberGutter{marker: true}

	// DiffGutter is a [Gutter] that renders only diff markers (" ", "+",
	// "-"), styled with [kind.GenericInserted] and [kind.GenericDeleted].
	DiffGutter Gutter = diffGutter{}

	// LineNumberGutter is a [Gutter] that renders only line numbers, in
	// [kind.UILineNumber]. Soft-wrapped continuation rows show " - ".
	LineNumberGutter Gutter = numberGutter{}

	// NoGutter is a [Gutter] that renders nothing.
	NoGutter Gutter = noGutter{}
)

// numberGutter is the [Gutter] behind [DefaultGutter] and
// [LineNumberGutter]. It renders the line number column, followed by the
// diff marker when marker is set.
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

// WithContainerStyle is an [Option] that sets the [lipgloss.Style] the
// printer wraps around the whole rendered output. By default the container
// is an empty style, which leaves each row as the printer renders it. A
// container that sets any property, such as a border or a background,
// pads every row to the widest one. It replaces the box of an earlier
// [WithBox].
//
// A width or a height on s applies to the joined rows. A narrow width
// wraps them, and a vertical alignment moves them, so the rows of a
// [Layout] then do not match the rows of [Printer.Print]. Use
// [WithContainerWidth] to pin the width.
//
// To set the theme, which styles the tokens inside, use [WithStyles].
//
//nolint:gocritic // hugeParam: Copying.
func WithContainerStyle(s lipgloss.Style) Option {
	return func(c *config) {
		c.style = s
		c.box = false
	}
}

// WithBox is an [Option] that draws the output as a box in the [kind.Text]
// style of the theme. The container takes that style with one cell of
// right padding. Every row reaches the width of the widest row plus one
// cell. When [WithContainerWidth] pins a width, every row reaches that
// width instead. A theme that sets a surface, such as
// [go.jacobcolvin.com/niceyaml/style/theme.Charm], fills the box with that
// color. Use WithBox for a full-screen viewer, where the document shows as
// one block of the theme's background.
//
// The box takes its style from the styles the printer holds after every
// option applies. It thus follows a [WithStyles] that comes before or
// after it. A later [WithContainerStyle] replaces the box.
func WithBox() Option {
	return func(c *config) {
		c.box = true
	}
}

// WithContainerWidth is an [Option] that pins the width of the container
// to n columns. [Printer.Print] pads every row it renders with [kind.Text]
// spaces out to n less the container's horizontal frame. The frame thus
// sits in the same columns whatever the widest row of the view is. A width
// of 0, the default, does not pin a width, so a container such as the box
// of [WithBox] shrinks to the widest row.
//
// The printer pads rows but never cuts them. A row can still run past n.
// An annotation column can push the layout wider than [WithWrapWidth], and
// a style transform can widen a row after it wraps. A viewer that scrolls
// horizontally cuts the rows itself and pads each cut row to its window
// with [Printer.Pad].
//
// Use it for a viewer that renders one window of a document at a time.
// Without a pinned width, a window of short lines draws a narrower
// container than a window of long lines.
func WithContainerWidth(n int) Option {
	return func(c *config) {
		c.containerWidth = max(0, n)
	}
}

// WithStyles is an [Option] that sets the [style.Styler] that styles
// tokens, gutters, and annotations. The styler is typically a theme from
// [go.jacobcolvin.com/niceyaml/style/theme]. A nil s, or one holding a nil
// pointer such as a nil [*style.Styles], selects [style.Default].
//
// To draw the output as a box in the theme's colors, use [WithBox]. To
// style the frame around the output, use [WithContainerStyle].
func WithStyles(s style.Styler) Option {
	return func(c *config) {
		if nilness.IsNil(s) {
			s = style.Default()
		}

		c.styles = s
		c.blends = newBlendCache()
	}
}

// WithGutter is an [Option] that sets the [Gutter] the printer renders with.
// By default, [DefaultGutter] renders line numbers and diff markers. A nil
// g, or one holding a nil func or pointer such as a nil [GutterFunc],
// selects [NoGutter].
func WithGutter(g Gutter) Option {
	return func(c *config) {
		if nilness.IsNil(g) {
			g = NoGutter
		}

		c.gutter = g
	}
}

// WithAnnotation is an [Option] that sets the [AnnotationFunc] that
// renders annotations. By default, [DefaultAnnotation] joins the
// annotations above a line onto one row and gives each run of columns
// below a line a caret row of its own. [NoAnnotation] leaves annotations
// out. A nil fn selects [DefaultAnnotation].
func WithAnnotation(fn AnnotationFunc) Option {
	return func(c *config) {
		if fn == nil {
			fn = DefaultAnnotation
		}

		c.annotationFunc = fn
	}
}

// WithWrapWidth is an [Option] that sets the width the output fits, in
// columns. The gutter, the content, and the horizontal frame of the
// container share the width. A row that [Printer.Print] renders, frame
// included, thus takes at most width columns. The printer wraps the
// content of a line at the columns the gutter and the frame leave. A row
// can still run past the width, as [WithContainerWidth] describes.
// [Printer.PrintError] fits its tree and each excerpt to the same width.
//
// A width of 0, the default, disables wrapping, and a negative width
// counts as 0. [WithContainerWidth] pins the container to a width
// instead of wrapping to it.
func WithWrapWidth(width int) Option {
	return func(c *config) {
		c.wrap = max(0, width)
	}
}

// WithMaxNumber is an [Option] that sets the smallest line number the gutter
// sizes itself for. The gutter fits the larger of n and the largest number
// in the view, so a number the view holds never overflows it. A max number
// of 0, the default, takes the number from the view alone.
//
// Use it to give two views the same gutter width, as a viewer that draws a
// side-by-side diff itself needs when one revision is longer than the
// other. [Printer.PrintSideBySide] sizes both gutters itself.
func WithMaxNumber(n int) Option {
	return func(c *config) {
		c.maxNumber = max(0, n)
	}
}

// WrapWidth returns the width the output of the printer fits, as
// [WithWrapWidth] sets it, or 0 when the printer does not wrap.
func (p *Printer) WrapWidth() int {
	return p.wrap
}

// ContainerWidth returns the width [WithContainerWidth] pins the container
// to, or 0 when the printer does not pin a width.
func (p *Printer) ContainerWidth() int {
	return p.containerWidth
}

// MaxNumber returns the line number the gutter sizes itself for when the
// printer renders view. It is the larger of the number [WithMaxNumber] set
// and the largest line number in the view.
func (p *Printer) MaxNumber(view *line.View) int {
	return max(p.maxNumber, maxNumber(view))
}

// ContainerStyle returns the [lipgloss.Style] the printer wraps around the
// whole rendered output: the style of [WithContainerStyle], the box of
// [WithBox], or the empty style by default.
func (p *Printer) ContainerStyle() lipgloss.Style {
	return p.style
}

// Style returns the [lipgloss.Style] for the given [kind.Kind] from the
// printer's [style.Styler].
func (p *Printer) Style(s kind.Kind) lipgloss.Style {
	return p.styles.Style(s)
}

// Segments returns an iterator over the segments of line i of view, as
// [line.View.Segments] yields them, each with the style [Printer.Print]
// draws it in. The style starts from the style of the Kind of the
// segment. On a line that carries [line.FlagInserted] or
// [line.FlagDeleted], it starts from [kind.GenericInserted] or
// [kind.GenericDeleted] instead. Each overlay of the segment then applies
// in order. A replacing overlay sets the properties its style sets. A
// blending overlay mixes its colors with the style under it, so a search
// highlight keeps the token color it covers.
//
// A renderer that writes another format, such as HTML with inline colors,
// reads the colors of each segment from its style. Its output then shows
// the colors that Print draws:
//
//	for seg, st := range p.Segments(view, i) {
//		fg, bg := st.GetForeground(), st.GetBackground()
//		// ...
//	}
//
// The style can hold a transform, which a format other than a terminal
// cannot apply as Print does. Print draws a grapheme cluster that two
// segments share in the style of the segment that holds its first rune.
//
// Segments yields the segments of the whole line, whatever
// [line.View.Clip] set, as line.View.Segments does. It panics if i is
// outside the content.
func (p *Printer) Segments(view *line.View, i int) iter.Seq2[line.Segment, lipgloss.Style] {
	segs := view.Segments(i)
	base := flagKind(view.Flag(i))

	return func(yield func(line.Segment, lipgloss.Style) bool) {
		for seg := range segs {
			styled := seg
			if base != "" {
				styled.Kind = base
			}

			if !yield(seg, p.blended(blendKey(styled), styled)) {
				return
			}
		}
	}
}

// flagKind returns the kind that every segment of a line with flag f
// renders in. That kind is [kind.GenericDeleted] or [kind.GenericInserted]
// for a line a diff changed. It is "" for a line whose segments each
// render in their own kind.
func flagKind(f line.Flag) kind.Kind {
	switch f {
	case line.FlagDeleted:
		return kind.GenericDeleted
	case line.FlagInserted:
		return kind.GenericInserted
	default:
		return ""
	}
}

// Print renders view and wraps the result in the container style. Each
// line takes one row for each wrapped piece of its content and one for
// each row its styled annotations take. [Printer.Layout] counts the same
// rows. To print part of a document, pass the view [line.View.Slice]
// returns. An empty view renders as the container around one empty row.
// The output ends with its last row and no line feed, as
// [line.View.String] does. A program that writes it into a file or a
// fenced block adds that line feed itself.
//
// A line the view clips, as a view from [line.View.Clip] does, renders the
// windows [line.View.Windows] returns. A "..." in [kind.UISeparator] takes
// the place of each run of columns that the windows leave out. The
// overlays and the annotations of the line follow its columns onto that
// row.
//
// The default container leaves each row as wide as its content. A
// container that sets any property, such as the box of [WithBox], pads
// every row to the widest one. [WithContainerWidth] instead pads every row
// to the width it pins.
func (p *Printer) Print(view *line.View) string {
	return p.style.Render(strings.Join(p.padRows(p.renderRows(view)), "\n"))
}

// sideBySideSeparator is the divider between the panes that
// [Printer.PrintSideBySide] renders.
const sideBySideSeparator = " │ "

// PrintSideBySide renders left and right as two panes beside each other,
// as a side-by-side diff shows the panes that
// [go.jacobcolvin.com/niceyaml/diff.Result.Before] and
// [go.jacobcolvin.com/niceyaml/diff.Result.After] return. The views pair
// their lines by index, as the panes of a diff do, so line i of left sits
// beside line i of right. A line that one view holds and the other does
// not gets blank rows in the other pane.
//
// The rows above the first content row of a line, such as the
// annotations above it, end on one row in both panes. A pane with fewer
// rows there gets blank rows at the top, and a pane with fewer rows after
// the first content row gets blank rows at the bottom. Each line thus
// takes the larger block of rows above its first content row, plus the
// larger number of rows from that row to the end of the line. A line can
// take more rows than it takes in either pane alone. The first content
// row of line i in each pane sits on one output row, and line i+1 starts
// on one row in both panes.
//
// Annotations, wrapped rows, and the marks that
// [go.jacobcolvin.com/niceyaml/diff.Result.Carry] copies keep the lines
// of the panes side by side. The rows within a line that wraps need not
// line up, because an annotation sits beside the wrapped row that holds
// its column.
//
// Both gutters size for the larger [Printer.MaxNumber] of the two views.
// With [WithWrapWidth], each pane wraps to half the width that the
// separator and the frame of the container leave. The left pane pads each
// of its rows in [kind.Text] to the wider of its widest row and that pane
// width, so the separator keeps its column on every row. The separator
// " │ " in [kind.UISeparator] joins the panes. The container style wraps
// the joined rows, and [WithContainerWidth] pins their width, as in
// [Printer.Print]. The output ends with its last row and no line feed, as
// [Printer.Print] describes.
//
// PrintSideBySide has no [Layout] counterpart. A viewer that scrolls or
// maps a pointer zips the panes itself by the same rule. For each line, it
// pads the shorter block of rows above the first content row at the top,
// and the rest of the shorter pane at the bottom. The block above the
// first content row of line i takes [Layout.RowOf] of column 0 less
// [Layout.LineStart] rows.
//
// The viewer takes the [Layout] of each pane from the printer that draws
// that pane. PrintSideBySide draws each pane with a copy of p that has an
// empty container style, no container width, and the larger MaxNumber of
// the two views in [WithMaxNumber]. When p wraps, the copy wraps to the
// pane width. A [Layout] by p itself counts the frame of the container and
// wraps to the whole width, so its rows differ from the rows of the panes.
func (p *Printer) PrintSideBySide(left, right *line.View) string {
	maxNumber := max(p.MaxNumber(left), p.MaxNumber(right))

	// Each pane renders without the container, which wraps the joined
	// rows, and wraps to its half of the width.
	pane := p.With(WithContainerStyle(lipgloss.NewStyle()), WithContainerWidth(0))

	paneWidth := 0
	if p.wrap > 0 {
		paneWidth = max(1, (p.wrap-p.style.GetHorizontalFrameSize()-lipgloss.Width(sideBySideSeparator))/2)
		pane = pane.With(WithWrapWidth(paneWidth))
	}

	gutterWidth := pane.gutterWidth(maxNumber)

	var leftRows, rightRows []string

	for _, i := range pairedLines(left, right) {
		l, lLead := pane.paneLine(left, i, maxNumber, gutterWidth)
		r, rLead := pane.paneLine(right, i, maxNumber, gutterWidth)

		// The blocks above the first content rows end on one row, and the
		// rest of the shorter pane fills to the bottom of the line.
		lead := max(lLead, rLead)
		rest := max(len(l)-lLead, len(r)-rLead)

		leftRows = append(leftRows, pane.alignRows(left, i, l, lead-lLead, lead+rest, maxNumber, gutterWidth)...)
		rightRows = append(rightRows, pane.alignRows(right, i, r, lead-rLead, lead+rest, maxNumber, gutterWidth)...)
	}

	leftWidth := paneWidth
	for _, row := range leftRows {
		leftWidth = max(leftWidth, lipgloss.Width(row))
	}

	separator := p.Style(kind.UISeparator).Render(sideBySideSeparator)
	rows := make([]string, len(leftRows))

	for k := range rows {
		rows[k] = p.Pad(leftRows[k], leftWidth) + separator + rightRows[k]
	}

	return p.style.Render(strings.Join(p.padRows(rows), "\n"))
}

// pairedLines returns the indices in the content of the lines that left or
// right holds, in ascending order and without repeats.
func pairedLines(left, right *line.View) []int {
	indices := make([]int, 0, max(left.Count(), right.Count()))

	for i := range left.All() {
		indices = append(indices, i)
	}

	for i := range right.All() {
		indices = append(indices, i)
	}

	slices.Sort(indices)

	return slices.Compact(indices)
}

// paneLine renders line i of view as the rows of one pane, and returns
// them with the number of rows above the first content row. It returns
// no rows for a line the view does not hold.
func (p *Printer) paneLine(view *line.View, i, maxNumber, gutterWidth int) ([]string, int) {
	if !view.Contains(i) {
		return nil, 0
	}

	return p.renderLine(view, i, view.Line(i), maxNumber, gutterWidth)
}

// alignRows returns rows, the rows of line i in the pane of view, with top
// blank rows above them and as many below as fill total rows. A blank row
// holds the gutter of an annotation row of the line, so a gutter that
// draws a border or a background keeps it down the pane.
func (p *Printer) alignRows(view *line.View, i int, rows []string, top, total, maxNumber, gutterWidth int) []string {
	ctx := GutterContext{Styles: p.styles, Index: i, MaxNumber: maxNumber, Annotation: true}
	if view.Contains(i) {
		ctx.Number = view.Line(i).Number()
		ctx.Flag = view.Flag(i)
	}

	blank := p.renderGutter(ctx, gutterWidth)
	out := make([]string, 0, total)

	for range top {
		out = append(out, blank)
	}

	out = append(out, rows...)

	for len(out) < total {
		out = append(out, blank)
	}

	return out
}

// Pad returns row with spaces in [kind.Text] after it, out to width
// cells, as [WithContainerWidth] pads each row that [Printer.Print]
// renders. A theme that sets a surface, such as
// [go.jacobcolvin.com/niceyaml/style/theme.Charm], fills the spaces with
// its background, which a plain space leaves out. A row that takes width
// cells or more comes back as it is, so Pad never cuts. A viewer that
// scrolls horizontally pads each row it cuts to its window:
//
//	window := p.Pad(printer.Cut(row, left, right), right-left)
func (p *Printer) Pad(row string, width int) string {
	pad := width - lipgloss.Width(row)
	if pad <= 0 {
		return row
	}

	return row + p.Style(kind.Text).Render(strings.Repeat(" ", pad))
}

// padRows pads each of rows with [Printer.Pad] out to the container
// width, less the horizontal frame of the container. It returns rows
// unchanged when the container width is 0, and it does not pad a row that
// already reaches the width.
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

	for i, row := range rows {
		rows[i] = p.Pad(row, width)
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

// gutterWidth returns the width the [Gutter] declares for a context
// holding the printer's styles and maxNumber, the number
// [Printer.MaxNumber] returns for the view.
func (p *Printer) gutterWidth(maxNumber int) int {
	return max(0, p.gutter.Width(GutterContext{
		Styles:    p.styles,
		Number:    maxNumber,
		MaxNumber: maxNumber,
	}))
}

// renderGutter renders the gutter for ctx at width cells. It takes the
// first row of the text the [Gutter] renders, and it pads that row with
// spaces in [kind.Text] or cuts it to width. Every row of a view thus
// starts its content in the same column, whatever the gutter renders for
// the row. A style with a margin, vertical padding, a border, or a width
// can spread the text or the pad over several rows. Only the first row of
// each stays, so the gutter never adds a row to the ones [Printer.Layout]
// counts.
func (p *Printer) renderGutter(ctx GutterContext, width int) string {
	// The cut shows a wide rune that straddles the width as a space in its
	// style, so only a row that falls short takes the pad.
	text, w := cells.CutWidth(firstRow(p.gutter.Render(ctx)), 0, width)
	if w < width {
		text += firstRow(p.styles.Style(kind.Text).Render(strings.Repeat(" ", width-w)))
	}

	return text
}

// firstRow returns the first row of text. The escape sequences of the rows
// it drops stay, so a style that spans the newline still closes.
func firstRow(text string) string {
	first, rest, ok := strings.Cut(text, "\n")
	if !ok {
		return text
	}

	return first + cutText(rest, 0)
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
		lineRows, _ := p.renderLine(view, idx, ln, maxNumber, gutterWidth)
		rows = append(rows, lineRows...)
	}

	return rows
}

// renderLine renders ln, line idx of view, as rows. It wraps the content
// to the printer width and puts the gutter in front of each row. Each
// annotation row of the line sits above or below the wrapped row that
// holds its column. It also returns the number of rows above the first
// content row.
func (p *Printer) renderLine(view *line.View, idx int, ln *line.Line, maxNumber, gutterWidth int) ([]string, int) {
	vl := newViewLine(view, idx, ln)

	rendered, runs := p.renderRuns(vl)
	w := wrappedLine{pieces: p.wrapContent(rendered, gutterWidth)}

	// Only annotations read the columns each row shows, so a line without
	// annotations leaves its rows unmapped.
	if len(view.Annotations(idx)) > 0 {
		w = mapRows(vl.width(), rendered, runs, w.pieces)
	}

	above := p.annotationRows(vl, gutterWidth, line.Above, w)
	below := p.annotationRows(vl, gutterWidth, line.Below, w)

	gutterCtx := GutterContext{
		Index:     idx,
		Number:    ln.Number(),
		MaxNumber: maxNumber,
		Flag:      view.Flag(idx),
		Styles:    p.styles,
	}

	rows := make([]string, 0, len(w.pieces))

	for j, piece := range w.pieces {
		rows = append(rows, p.annotationGutters(rowsAt(above, j), gutterCtx, gutterWidth)...)

		ctx := gutterCtx
		ctx.Soft = j > 0

		rows = append(rows, p.renderGutter(ctx, gutterWidth)+piece)
		rows = append(rows, p.annotationGutters(rowsAt(below, j), gutterCtx, gutterWidth)...)
	}

	return rows, len(rowsAt(above, 0))
}

// viewLine is one line of a view as the printer shows it: its content,
// segments, overlays, and annotations in the columns of its row. Those
// are the columns of the line unless the view clips it, as a view from
// [line.View.Clip] does. The row of a clipped line holds the windows
// [line.View.Windows] returns, with an ellipsis in place of each run of
// columns between and beside them. Everything the printer lays out for
// the line counts the columns of that row.
//
// Create instances with [newViewLine].
type viewLine struct {
	view *line.View
	ln   *line.Line
	// The layout of the row of a clipped line, or nil for a line the view
	// shows whole.
	clip *clip.Map
	idx  int
}

// newViewLine creates a new [viewLine] for ln, line idx of view.
func newViewLine(view *line.View, idx int, ln *line.Line) viewLine {
	vl := viewLine{view: view, ln: ln, idx: idx}

	if windows := view.Windows(idx); windows != nil {
		vl.clip = clip.New(ln.Width(), windows)
	}

	return vl
}

// content returns the text of the row, without a line ending.
func (vl viewLine) content() string {
	if vl.clip == nil {
		return vl.ln.Content()
	}

	return vl.clip.Content(vl.ln.Content())
}

// width returns the number of columns of the row.
func (vl viewLine) width() int {
	if vl.clip == nil {
		return vl.ln.Width()
	}

	return vl.clip.Width()
}

// segments returns the segments of the row, in order, as
// [line.View.Segments] yields those of the line. A clipped line keeps the
// part of each segment its windows hold, and each ellipsis is a segment
// of its own in [kind.UISeparator].
func (vl viewLine) segments() []line.Segment {
	segs := slices.Collect(vl.view.Segments(vl.idx))
	if vl.clip == nil {
		return segs
	}

	var out []line.Segment

	// The pieces and the segments both ascend, so first moves past the
	// segments that end before each piece once.
	first := 0

	for piece := range vl.clip.Pieces() {
		if piece.Gap {
			out = append(out, line.Segment{
				Text: clip.Ellipsis,
				Kind: kind.UISeparator,
				Cols: position.NewSpan(piece.At, piece.At+clip.EllipsisCols),
			})

			continue
		}

		for first < len(segs) && segs[first].Cols.End <= piece.Cols.Start {
			first++
		}

		for _, seg := range segs[first:] {
			if seg.Cols.Start >= piece.Cols.End {
				break
			}

			lo, hi := max(seg.Cols.Start, piece.Cols.Start), min(seg.Cols.End, piece.Cols.End)
			at := piece.At + lo - piece.Cols.Start

			out = append(out, line.Segment{
				Text:     cutRunes(seg.Text, lo-seg.Cols.Start, hi-seg.Cols.Start),
				Kind:     seg.Kind,
				Overlays: seg.Overlays,
				Cols:     position.NewSpan(at, at+hi-lo),
			})
		}
	}

	return out
}

// cutRunes returns the runes of s from rune index lo, inclusive, to rune
// index hi, exclusive.
func cutRunes(s string, lo, hi int) string {
	start, end := len(s), len(s)
	n := 0

	for i := range s {
		if n == lo {
			start = i
		}

		if n == hi {
			end = i

			break
		}

		n++
	}

	return s[min(start, end):end]
}

// overlays returns a new slice of the overlays of the line in the columns
// of the row. A clipped line keeps the part of each overlay its windows
// hold, as one overlay per window.
func (vl viewLine) overlays() line.Overlays {
	overlays := vl.view.Overlays(vl.idx)
	if vl.clip == nil {
		return slices.Clone(overlays)
	}

	var out line.Overlays

	for _, o := range overlays {
		for _, cols := range vl.clip.Spans(o.Cols) {
			out = append(out, line.Overlay{Kind: o.Kind, Cols: cols, Blend: o.Blend})
		}
	}

	return out
}

// annotations returns the annotations of the line, each labeling the run
// of the row that shows its columns.
func (vl viewLine) annotations() line.Annotations {
	anns := vl.view.Annotations(vl.idx)
	if vl.clip == nil {
		return anns
	}

	out := make(line.Annotations, len(anns))

	for i, a := range anns {
		a.Cols = vl.clip.Span(a.Cols)
		out[i] = a
	}

	return out
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

	// The offset, counted from shown, at which the shown runes of the run
	// hold its text verbatim, or -1 when they do not. A transform that
	// adds runes around the text, such as brackets or a prefix, keeps it
	// verbatim. A transform that rewrites the text itself does not. The
	// printer matches the text as a whole, so an added rune that equals a
	// rune of the text does not count as that rune. When the added runes
	// hold the text too, as "a"+s does for "a", the first occurrence
	// counts.
	textAt int
}

// renderRuns renders the content of vl with its overlays, one styled run
// per run of its segments that style the same. A deleted or inserted line
// takes the diff style for its flag, and any other line takes the kind of
// each segment. On either kind of line, the overlays that cover a segment
// apply over that style. A run never ends inside a grapheme cluster, so
// an escape sequence never splits one. A cluster that segments of
// different styles share renders in the style of the segment holding its
// first rune, the one [cells.Row] measures it by. It returns the rendered
// content and a [runSpan] for each run, in order.
func (p *Printer) renderRuns(vl viewLine) (string, []runSpan) {
	base := flagKind(vl.view.Flag(vl.idx))

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
		runShown := ansi.Strip(rendered)
		span := runSpan{
			text:     text,
			col:      col,
			cols:     utf8.RuneCountInString(text),
			shown:    shown,
			shownLen: utf8.RuneCountInString(runShown),
			textAt:   -1,
		}

		if before, _, ok := strings.Cut(runShown, text); ok {
			span.textAt = utf8.RuneCountInString(before)
		}

		sb.WriteString(rendered)
		run.Reset()

		spans = append(spans, span)
		col += span.cols
		shown += span.shownLen
	}

	segs := vl.segments()

	var content strings.Builder

	for _, seg := range segs {
		content.WriteString(seg.Text)
	}

	row := cells.NewRow(content.String())
	at := 0 // The column of the content the next rune takes.

	for _, seg := range segs {
		if base != "" {
			seg.Kind = base
		}

		// The runes at the start of the segment that continue the last
		// cluster of the run stay in that run, whatever their style.
		cut := len(seg.Text)

		for i := range seg.Text {
			if row.Start(at) == at {
				cut = i

				break
			}

			at++
		}

		run.WriteString(seg.Text[:cut])

		text := seg.Text[cut:]
		at += utf8.RuneCountInString(text)

		if text == "" {
			continue
		}

		key := blendKey(seg)
		if !started || key != runKey {
			flush()

			runKey, runSeg, started = key, seg, true
		}

		run.WriteString(text)
	}

	flush()

	return sb.String(), spans
}

// annotationGutters puts an annotation gutter for the line gutterCtx
// describes in front of each of rows, the annotation rows at one
// placement beside one wrapped row of the line's content. Every row after
// the first is a continuation, whatever kind it renders in.
func (p *Printer) annotationGutters(rows []string, gutterCtx GutterContext, gutterWidth int) []string {
	out := make([]string, 0, len(rows))

	for i, row := range rows {
		ctx := gutterCtx
		ctx.Annotation = true
		ctx.Soft = i > 0

		out = append(out, p.renderGutter(ctx, gutterWidth)+row)
	}

	return out
}

// rowsAt returns the rows blocks holds for wrapped row j of a line, or
// nil when blocks does not hold an entry for j.
func rowsAt(blocks [][]string, j int) []string {
	if j < len(blocks) {
		return blocks[j]
	}

	return nil
}

// minAnnotationWidth is the fewest cells the text of an annotation wraps
// to after it moves off the marker row, when the printer width has room
// for them.
const minAnnotationWidth = 20

// annotationRows renders the annotations of vl at the given placement as
// terminal rows without the gutter. The wrapped line w holds the columns
// of the content each wrapped row of the line shows. The result holds a
// block of rows for each wrapped row, with the rows of each
// [AnnotationRow] whose column that wrapped row holds. It returns nil when
// the line does not have annotations at the placement.
//
// Within a block, the kinds come in the order [line.Annotations.Groups]
// gives them. Above a line, that is the order the kinds first appear.
// Below a line, a [kind.TextError] group comes first, then a
// [kind.TextWarn] group, then the other kinds in the order they first
// appear. Within each kind, the rows the [AnnotationFunc] returns come in
// order, each wrapped to the printer width and styled in the style of its
// kind.
func (p *Printer) annotationRows(vl viewLine, gutterWidth int, placement line.Placement, w wrappedLine) [][]string {
	groups := vl.annotations().Groups(placement)
	if len(groups) == 0 {
		return nil
	}

	starts := w.starts

	text := vl.content()
	content := []rune(text)
	out := make([][]string, max(1, len(starts)))

	// Each wrapped row gets a [cells.Row] of its own text the first time
	// a row of annotations sits beside it, so the work stays linear in
	// the length of the line.
	rowCells := make([]*cells.Row, len(out))

	for _, group := range groups {
		rows := p.annotationFunc(AnnotationContext{
			Annotations: group,
			Placement:   placement,
			Content:     text,
			Overlays:    vl.overlays(),
			RowStarts:   slices.Clone(starts),
			RowEnds:     slices.Clone(w.ends),
		})

		for _, row := range rows {
			k := row.Kind
			if k == "" {
				k = group[0].RenderKind()
			}

			// The row sits beside the wrapped row that holds its column,
			// and its padding runs from the start of that wrapped row, so
			// the marker lands in the cell the column takes on it. The
			// column stops at line.MaxColPastEnd columns past the end, so a
			// stray column such as math.MaxInt pads a bounded row.
			col := annotationCol(text, row.Col)

			j, from, to := 0, 0, len(content)
			if len(starts) > 0 {
				j = rowIndex(starts, col)
				from = min(max(0, starts[j]), len(content))

				if j+1 < len(starts) {
					to = min(max(from, starts[j+1]), len(content))
				}
			}

			// The padding measures the text of the wrapped row on its own,
			// as the terminal draws it. When the wrap drops the space that
			// begins a grapheme cluster, the rest of the cluster starts the
			// next row, and the padding counts the cells that rest takes
			// alone, such as none for a combining accent.
			if rowCells[j] == nil {
				measured := cells.NewRow(string(content[from:to]))
				rowCells[j] = &measured
			}

			pad := rowCells[j].Width(col - from)
			out[j] = append(out[j], p.renderAnnotationRow(row, p.styles.Style(k), pad, gutterWidth)...)
		}
	}

	return out
}

// renderAnnotationRow renders row, an [AnnotationRow], as terminal rows
// without the gutter. It pads the row by pad cells, wraps it, and styles
// it with kindStyle.
//
//nolint:gocritic // hugeParam: value semantics match lipgloss.
func (p *Printer) renderAnnotationRow(row AnnotationRow, kindStyle lipgloss.Style, pad, gutterWidth int) []string {
	// The printer escapes the text, so a control character in a message
	// shows as the rune that escape.Control draws for it, and the wrap
	// measures the cells the terminal shows. The escape keeps each newline,
	// and the wrap starts a new row at each one.
	marker := escape.Control(row.Marker)
	mark := strings.TrimRight(marker, " ")
	padding := strings.Repeat(" ", pad)
	indent := padding + marker
	indentWidth := lipgloss.Width(indent)
	text := escape.Rows(row.Text)
	widest := widestWord(text)

	// The style of the kind may lay a row out over several terminal rows,
	// through a width, vertical padding, a margin, or a border. The rows
	// keep each terminal row apart, so Print puts the gutter on each one
	// and Layout counts each one.
	var rows []string

	add := func(s string) {
		rows = append(rows, strings.Split(kindStyle.Render(s), "\n")...)
	}

	// The indent is the padding to the column plus the marker, and the
	// wrap leaves it out. The first row keeps it as rendered, and
	// continuation rows get the same width in spaces, so every row of the
	// text starts under the start of the text.
	//
	// When the column leaves the text less room than its widest word, the
	// marker keeps its column on a row of its own, without its trailing
	// spaces, and the text moves to the rows below under a smaller indent.
	// The room is the width the content of a row has less the indent, and
	// none once the indent fills that width. A one-cell word then moves
	// too, while text with no word stays on the marker row. The smaller
	// indent leaves the text minAnnotationWidth cells, or its widest word
	// when that is wider, and shrinks to nothing when the width has less
	// room. A row without a marker keeps its text at the column, since
	// only the marker holds the column once the text moves. A column past
	// the end of the content keeps its cell even when that cell lies past
	// the width, and the rows indented to it then run wider.
	if p.wrap > 0 && mark != "" && widest > max(0, p.contentWidth(gutterWidth)-indentWidth) {
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

	return rows
}

// widestWord returns the width in cells of the widest word of text. Words
// run between newlines, the spaces the wrap breaks at, and [breakpoints].
// [isBreakSpace] reports the spaces the wrap breaks at. A word includes
// the '/' or '-' that ends it, because the wrap keeps that character with
// the word before it.
func widestWord(text string) int {
	widest := 0

	for field := range strings.FieldsFuncSeq(text, isBreakSpace) {
		for field != "" {
			word := field
			if i := strings.IndexAny(field, breakpoints); i >= 0 {
				word = field[:i+1]
			}

			widest = max(widest, lipgloss.Width(word))
			field = field[len(word):]
		}
	}

	return widest
}

// blendKey returns the cache key of the effective style of seg. The key
// holds the kind of seg, then each overlay that covers seg, in order. A
// marker in front of each overlay tells whether it blends with or
// replaces the style underneath. Two segments with the same key render
// with the same style. The key quotes each name, so a name that contains
// a marker cannot collide with a different overlay sequence.
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
// computes the style on the first request for key and returns the cached
// style after that. The overlays apply in order. A blending overlay mixes
// with the result so far, and any other replaces it.
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
// a copy its own. The cache is safe for concurrent use, so copies that
// share it can render at the same time.
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

// contentWidth returns the width left for the content of a row inside the
// container, after the horizontal frame of the container and a gutter
// gutterWidth wide. A positive printer width always wraps, so the result
// is at least one column even when the frame and the gutter alone fill
// the width. It returns 0 when the printer does not wrap.
func (p *Printer) contentWidth(gutterWidth int) int {
	if p.wrap <= 0 {
		return 0
	}

	return max(1, p.wrap-p.style.GetHorizontalFrameSize()-gutterWidth)
}

// textWidth returns the width left for text outside the container, such
// as the tree of an error, after indent columns in front of it. Like
// [Printer.contentWidth], it is at least one column when the printer
// wraps and 0 when it does not.
func (p *Printer) textWidth(indent int) int {
	if p.wrap <= 0 {
		return 0
	}

	return max(1, p.wrap-indent)
}

// wrapContent splits content into rows when the printer wraps, for a row
// inside the container after a gutter gutterWidth wide.
func (p *Printer) wrapContent(content string, gutterWidth int) []string {
	return p.wrapTo(content, p.contentWidth(gutterWidth))
}

// wrapTo splits content into rows of at most cw cells, or only at
// newlines when cw is 0 or less.
func (p *Printer) wrapTo(content string, cw int) []string {
	if cw <= 0 {
		// A newline in the content is a row of its own with or without
		// wrapping, so the row count matches what Print writes.
		return strings.Split(content, "\n")
	}

	var out []string

	for text := range strings.SplitSeq(content, "\n") {
		out = append(out, wrapText(text, cw)...)
	}

	return out
}

// wrapText wraps text, a line without a newline, to rows of at most cw
// cells. It keeps every grapheme cluster whole on one row. A cluster
// wider than cw takes a row of its own and runs past the width.
func wrapText(text string, cw int) []string {
	rows := wrapRows(text, cw, wrapOnCharacters)

	// The wrap leaves a row wider than cw in three cases. When a full row
	// meets a space and then a breakpoint, the row keeps the space, and
	// dropping it, as the wrap drops the space at any other break, fits
	// the row. When a breakpoint falls just past the width, or a cluster
	// that starts with an ASCII byte, such as a keycap, spans more cells
	// than the wrap counts for it, a hard wrap cuts the row down to size.
	// Either way, every row fits the width the caller asked for. The hard
	// wrap does not reopen a style on the rows it cuts off, and a second
	// pass of the wrap, which does, restores it.
	out := make([]string, 0, len(rows))

	for _, row := range rows {
		if lipgloss.Width(row) <= cw {
			out = append(out, row)

			continue
		}

		// The row drops its trailing break spaces before the hard wrap as
		// well, so the hard wrap cannot put one on a row of its own. The
		// cut keeps the escape sequences after the dropped spaces, so a
		// style the row opens still closes.
		text := strings.TrimRightFunc(ansi.Strip(row), isBreakSpace)
		row = cutText(row, len(text))

		if lipgloss.Width(text) <= cw {
			out = append(out, row)

			continue
		}

		out = append(out, wrapRows(hardwrap(row, cw), cw, "")...)
	}

	// The wrap can leave rows with no text on them, so those rows go. It
	// emits them for a word that follows a run of breakpoints longer than
	// the width, and both wraps break ahead of a lone cluster the width
	// cannot fit even at the start of a row. After the cut, a row stays
	// wider than cw only when it holds such a cluster. When a style opens
	// just before the cluster, the wrap also puts the ASCII spaces ahead
	// of it on rows of their own, so rows of only those spaces that lead
	// up to the cluster go too. Any other rune stays, even one with no
	// width, so the rows still spell out the content for the layout.
	tooWide := func(row string) bool { return lipgloss.Width(row) > cw }

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

	// Text with nothing to show, such as an empty line, still takes a
	// row.
	if len(kept) == 0 {
		return out[:1]
	}

	slices.Reverse(kept)

	return kept
}

// cutText returns row with its text cut to the first n bytes. It counts
// bytes of text rather than cells, so a keycap cannot move the cut.
// [ansi.Truncate] counts a keycap as one cell for its ASCII base. Every
// escape sequence stays, so a style the row opens still closes.
func cutText(row string, n int) string {
	var b strings.Builder

	for i := 0; i < len(row); {
		if row[i] == ansi.ESC {
			seq, _, size, _ := ansi.DecodeSequence(row[i:], ansi.NormalState, nil)
			b.WriteString(seq)

			i += size

			continue
		}

		if n > 0 {
			b.WriteByte(row[i])

			n--
		}

		i++
	}

	return b.String()
}

// hardwrap breaks row before each grapheme cluster that takes the row
// past cw cells, and returns the rows joined by newlines. It measures each
// cluster as [lipgloss.Width] does, so a keycap counts its two cells, but
// [ansi.Hardwrap] counts one for its ASCII base. A cluster wider than cw
// stays whole on a row of its own. The escape sequences stay where they
// are, so a row that a break ends might leave a style open.
func hardwrap(row string, cw int) string {
	var b strings.Builder

	width := 0

	for i := 0; i < len(row); {
		if row[i] == ansi.ESC {
			seq, _, size, _ := ansi.DecodeSequence(row[i:], ansi.NormalState, nil)
			b.WriteString(seq)

			i += size

			continue
		}

		cluster, w := ansi.FirstGraphemeCluster(row[i:], ansi.GraphemeWidth)
		if width > 0 && width+w > cw {
			b.WriteByte('\n')

			width = 0
		}

		b.WriteString(cluster)

		width += w
		i += len(cluster)
	}

	return b.String()
}

// wrapRows wraps text to rows of at most cw cells with [lipgloss.Wrap] and
// returns the rows. The wrap breaks at the characters in breaks. It takes
// each ASCII byte as a cluster of its own. It can thus start a row with
// the runes that continue the cluster that ends the row before. The
// combining accent of an "e" and the rest of a keycap are two such runs.
//
// Those runes move back onto the last row with text on it. The move
// keeps the cluster whole even when the wrap puts empty rows between
// them, and wrapRows drops a row that the move leaves empty. The runes
// move only when that row ends where theirs begins in text. When the wrap
// drops a space between the two rows, as it does at a break, the runes
// continue the space rather than the last cluster of the row. They then
// stay at the start of their row. A newline in text, such as one
// [hardwrap] adds, ends a row and does not drop anything.
func wrapRows(text string, cw int, breaks string) []string {
	rows := strings.Split(lipgloss.Wrap(text, cw, breaks), "\n")

	plain := make([]string, len(rows))
	for i, row := range rows {
		plain[i] = ansi.Strip(row)
	}

	starts, ends := rowBounds(strings.ReplaceAll(ansi.Strip(text), "\n", ""), plain)

	out := rows[:0]
	last, lastEnd := -1, 0

	for i, row := range rows {
		if last >= 0 && starts[i] == lastEnd {
			var moved bool

			out[last], row, moved = rejoinCluster(out[last], row)
			if moved && ansi.Strip(row) == "" {
				lastEnd = ends[i]

				continue
			}
		}

		if ansi.Strip(row) != "" {
			last, lastEnd = len(out), ends[i]
		}

		out = append(out, row)
	}

	return out
}

// rejoinCluster moves the runes at the start of row that continue the
// last grapheme cluster of prev onto the end of that cluster. It reports
// whether it moved any. The runes go ahead of any escape sequences that
// close prev, so an escape sequence never splits the cluster. The cluster
// thus measures as the terminal draws it. The escape sequences among
// those runes stay at the start of row, so the style they open still
// covers the rest of row.
func rejoinCluster(prev, row string) (string, string, bool) {
	plainPrev, plainRow := ansi.Strip(prev), ansi.Strip(row)
	if plainPrev == "" || plainRow == "" {
		return prev, row, false
	}

	last := plainPrev[len(cells.TrimLastCluster(plainPrev)):]
	joined, _ := ansi.FirstGraphemeCluster(last+plainRow, ansi.GraphemeWidth)

	n := len(joined) - len(last)
	if n <= 0 {
		return prev, row, false
	}

	var escapes, runes strings.Builder

	i := 0
	for runes.Len() < n && i < len(row) {
		if row[i] == ansi.ESC {
			seq, _, size, _ := ansi.DecodeSequence(row[i:], ansi.NormalState, nil)
			escapes.WriteString(seq)

			i += size

			continue
		}

		_, size := utf8.DecodeRuneInString(row[i:])
		runes.WriteString(row[i : i+size])

		i += size
	}

	end := textEnd(prev)

	return prev[:end] + runes.String() + prev[end:], escapes.String() + row[i:], true
}

// textEnd returns the byte offset in row after its last byte of text, so
// that only escape sequences follow it.
func textEnd(row string) int {
	end := 0

	for i := 0; i < len(row); {
		if row[i] == ansi.ESC {
			_, _, size, _ := ansi.DecodeSequence(row[i:], ansi.NormalState, nil)
			i += size

			continue
		}

		i++
		end = i
	}

	return end
}
