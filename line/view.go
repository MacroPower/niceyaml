package line

import (
	"fmt"
	"iter"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"go.jacobcolvin.com/niceyaml/internal/escape"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

// View is [Lines] content together with the decoration one rendering of it
// carries: a [Flag], [Overlays], and [Annotations] per line. The printer
// renders a View, and every utility that marks content, such as a diff or
// a bound error, hands one out.
//
// A View shares its lines with every other View over the same content and
// owns its decoration alone, so creating one costs an index of the lines
// it holds and no copy of their content, and decorating one reaches no
// other. Two renderings of one document, such as search
// highlights and error marks, are two Views over the same [Lines].
//
// Every index and every [position.Range] a View takes or yields is in the
// coordinates of its content, the [Lines] that [View.Lines] returns, where
// line i is [Lines.Line] i. A View holds each line of its content at most
// once, in content order. A View from [View.Slice] holds some of those
// lines and keeps their indices, so a range from a search of the content
// or from the path of a document applies to a slice of the content as it
// applies to the whole, and slicing before or after decorating renders the
// same. [View.All] yields the lines the View holds with their indices,
// [View.Contains] reports whether it holds a line, [View.Index] finds the
// index of a line it holds, and [View.Count] is the number it holds.
//
// Index-taking methods panic when the index is outside the content, as
// indexing a slice does. A View keeps decoration on a line it does not
// hold, and that decoration never renders. A View is not safe for
// concurrent mutation. Decorate from one goroutine at a time, and do not
// decorate while another goroutine renders.
//
// Create instances with [NewView]. The zero value is an empty view.
type View struct {
	lines Lines
	// The index in lines of each line the View holds, ascending.
	held []int
	// Whether the View holds each line of lines, by index.
	mask        []bool
	flags       []Flag
	overlays    []Overlays
	annotations []Annotations
}

// NewView creates a new [*View] over lines with no decoration, holding
// every line in order.
func NewView(lines Lines) *View {
	held := make([]int, lines.Len())
	mask := make([]bool, lines.Len())

	for i := range held {
		held[i] = i
		mask[i] = true
	}

	return &View{lines: lines, held: held, mask: mask}
}

// Lines returns the content of the [View], every line of the [Lines] it is
// over, whether or not the View holds it. A search of the content, such as
// one a [finder.Finder] loads, yields ranges in the coordinates every View
// method takes.
//
// [finder.Finder]: https://pkg.go.dev/go.jacobcolvin.com/niceyaml/finder#Finder
func (v *View) Lines() Lines {
	if v == nil {
		return Lines{}
	}

	return v.lines
}

// Count returns the number of lines the [View] holds, which is the number
// [View.All] yields. The lines of the content, held or not, are
// [View.Lines], and [Lines.Len] counts those.
func (v *View) Count() int {
	if v == nil {
		return 0
	}

	return len(v.held)
}

// Contains reports whether the [View] holds line i of its content. A nil
// View holds no line, and no View holds an index outside its content.
func (v *View) Contains(i int) bool {
	if v == nil || i < 0 || i >= len(v.mask) {
		return false
	}

	return v.mask[i]
}

// Index returns the index of the line the [View] holds that is l and
// true. Every View over the same content shares its lines by pointer, so
// a decorator that knows a line of a [Lines] value finds it in a slice of
// that content, or in a diff that interleaves it with another revision,
// without knowing how the view was built. A line the view does not hold,
// such as one from other content, reports false.
func (v *View) Index(l *Line) (int, bool) {
	if v == nil || l == nil {
		return 0, false
	}

	for _, i := range v.held {
		if v.lines.lines[i] == l {
			return i, true
		}
	}

	return 0, false
}

// All returns an iterator over the lines the [View] holds within any of
// the given spans, or over every line it holds when no span is given, in
// content order and each once whatever order the spans come in and however
// they overlap. Each iteration yields the index of the line in the content
// and the [*Line], and the index reaches the line's decoration through
// [View.Flag], [View.Overlays], and [View.Annotations]. A span reaching
// outside the content selects the lines it does hold.
func (v *View) All(spans ...position.Span) iter.Seq2[int, *Line] {
	return func(yield func(int, *Line) bool) {
		if v == nil {
			return
		}

		for _, i := range v.held {
			if len(spans) > 0 && !slices.ContainsFunc(spans, func(s position.Span) bool { return s.Contains(i) }) {
				continue
			}

			if !yield(i, v.lines.lines[i]) {
				return
			}
		}
	}
}

// Flag returns the [Flag] of line i. The zero value is [FlagDefault].
func (v *View) Flag(i int) Flag {
	_ = v.lines.lines[i]

	if v.flags == nil {
		return FlagDefault
	}

	return v.flags[i]
}

// SetFlag sets the [Flag] of line i.
func (v *View) SetFlag(i int, f Flag) {
	_ = v.lines.lines[i]

	if v.flags == nil {
		v.flags = make([]Flag, v.lines.Len())
	}

	v.flags[i] = f
}

// Annotations returns the [Annotation] values on line i, in the order they
// were added. The slice is shared with the view, so treat it as read-only
// and add to it with [View.Annotate].
func (v *View) Annotations(i int) Annotations {
	_ = v.lines.lines[i]

	if v.annotations == nil {
		return nil
	}

	return v.annotations[i]
}

// Annotate adds the given [Annotation] values to line i.
func (v *View) Annotate(i int, ann ...Annotation) {
	_ = v.lines.lines[i]

	if v.annotations == nil {
		v.annotations = make([]Annotations, v.lines.Len())
	}

	v.annotations[i] = append(v.annotations[i], ann...)
}

// Overlays returns the [Overlay] values on line i, in the order they were
// added. The slice is shared with the view, so treat it as read-only and
// add to it with [View.AddOverlay], [View.BlendOverlay], or
// [View.AddLineOverlay].
func (v *View) Overlays(i int) Overlays {
	_ = v.lines.lines[i]

	if v.overlays == nil {
		return nil
	}

	return v.overlays[i]
}

// AddLineOverlay adds the given [Overlay] values to line i as given.
// [View.AddOverlay] and [View.BlendOverlay] clamp a range to the lines it
// covers before adding; AddLineOverlay does not.
func (v *View) AddLineOverlay(i int, o ...Overlay) {
	_ = v.lines.lines[i]

	if v.overlays == nil {
		v.overlays = make([]Overlays, v.lines.Len())
	}

	v.overlays[i] = append(v.overlays[i], o...)
}

// AddOverlay adds an overlay with the given style to the specified ranges,
// in the coordinates of the content. The overlay replaces the style
// underneath it; use [View.BlendOverlay] to mix with it instead.
//
// It splits each range into one overlay per line with [Lines.SliceLines],
// which clamps the columns to the width of the line and skips lines
// outside the content, so a range computed against longer content is safe
// to apply. A range that covers no columns of a line adds no overlay to
// it, and an overlay on a line the View does not hold never renders.
func (v *View) AddOverlay(s kind.Kind, ranges ...position.Range) {
	for _, r := range ranges {
		v.addOverlayRange(s, false, r)
	}
}

// BlendOverlay adds an overlay like [View.AddOverlay], but one that blends
// with the style underneath it. A search highlight added this way keeps the
// token or diff color of the text it covers.
func (v *View) BlendOverlay(s kind.Kind, ranges ...position.Range) {
	for _, r := range ranges {
		v.addOverlayRange(s, true, r)
	}
}

// addOverlayRange adds a single overlay range, one overlay per line r
// covers within the content.
func (v *View) addOverlayRange(s kind.Kind, blend bool, r position.Range) {
	for _, lr := range v.lines.SliceLines(r) {
		v.AddLineOverlay(lr.Start.Line, Overlay{
			Cols:  position.NewSpan(lr.Start.Col, lr.End.Col),
			Kind:  s,
			Blend: blend,
		})
	}
}

// Clone returns a copy of the [View] with its own decoration. The copy
// shares the lines with the original and holds the same ones, so it costs
// one copy of the flags, overlays, and annotations, and decorating either
// reaches nothing in the other.
func (v *View) Clone() *View {
	if v == nil {
		return nil
	}

	c := &View{lines: v.lines, held: slices.Clone(v.held), mask: slices.Clone(v.mask)}

	if v.flags != nil {
		c.flags = slices.Clone(v.flags)
	}

	if v.overlays != nil {
		c.overlays = make([]Overlays, len(v.overlays))
		for i, o := range v.overlays {
			c.overlays[i] = slices.Clone(o)
		}
	}

	if v.annotations != nil {
		c.annotations = make([]Annotations, len(v.annotations))
		for i, a := range v.annotations {
			c.annotations[i] = slices.Clone(a)
		}
	}

	return c
}

// Slice returns a new [*View] over the same content that holds the lines
// the receiver holds within any of the given spans, in content order and
// each once, as [View.All] yields them, each with its index and its
// decoration. The result owns its decoration and carries that of the
// lines it holds, so it is the view a caller renders to show part of a
// document, such as the hunks around an error, and a range or an index
// that applies to the receiver applies to it. Slicing an already sliced
// view narrows it further.
func (v *View) Slice(spans ...position.Span) *View {
	out := &View{lines: v.Lines()}
	if v == nil {
		return out
	}

	n := v.lines.Len()
	out.mask = make([]bool, n)

	for i := range v.All(spans...) {
		out.held = append(out.held, i)
		out.mask[i] = true
	}

	if v.flags != nil {
		out.flags = make([]Flag, n)
		for _, i := range out.held {
			out.flags[i] = v.flags[i]
		}
	}

	if v.overlays != nil {
		out.overlays = make([]Overlays, n)
		for _, i := range out.held {
			out.overlays[i] = slices.Clone(v.overlays[i])
		}
	}

	if v.annotations != nil {
		out.annotations = make([]Annotations, n)
		for _, i := range out.held {
			out.annotations[i] = slices.Clone(v.annotations[i])
		}
	}

	return out
}

// Hunks returns a [*View] that holds the decorated lines of the View
// with context lines of unchanged content on either side of each one,
// so a caller that marks a document shows the marked parts alone. A
// decorated line carries a [Flag] other than [FlagDefault], an [Overlay],
// or an [Annotation]. Decorated lines whose context windows overlap or
// touch share a hunk, distant ones become separate hunks, and the first
// line of each hunk after the first carries a "..." annotation of kind
// [kind.UISeparator] above it. A negative context shows the decorated
// lines alone, as 0 does, and a View with no decorated line yields a
// View that holds no line.
//
// The result is a [View.Slice], so the lines keep their indices, a
// range from the content applies to it, and decoration added after
// slicing lands on the lines it holds. Context lines count in the
// content, and the hunks hold those of them the View holds, so a slice
// of a document yields hunks within the slice.
//
// An error excerpt is the hunks of a view the error marked, and a caller
// that marks several errors, or search matches, on one view takes the
// hunks of that view the same way:
//
//	view := source.View()
//	for _, bound := range niceyaml.SourceErrors(err) {
//		bound.Annotate(view)
//	}
//	fmt.Println(p.Print(view.Hunks(2)))
func (v *View) Hunks(context int) *View {
	var marked []int

	for i := range v.All() {
		if v.decorated(i) {
			marked = append(marked, i)
		}
	}

	spans := position.ContextSpans(marked, context, v.Lines().Len())
	if len(spans) == 0 {
		return v.Slice(position.Span{})
	}

	hunks := v.Slice(spans...)

	// The separator goes above the first line the hunk holds, which is
	// the start of its span unless the View skips that line.
	for _, span := range spans[1:] {
		for i := range hunks.All(span) {
			hunks.Annotate(i, Annotation{
				Content:   "...",
				Kind:      kind.UISeparator,
				Placement: Above,
			})

			break
		}
	}

	return hunks
}

// decorated reports whether line i carries a flag, an overlay, or an
// annotation.
func (v *View) decorated(i int) bool {
	return v.Flag(i) != FlagDefault || len(v.Overlays(i)) > 0 || len(v.Annotations(i)) > 0
}

// String renders the [View] as plain text: each line behind its number,
// the annotations above it on rows of their own, and a row below it that
// marks its decoration, with a caret under every column an overlay covers,
// a caret at the column of the annotations below the line, and their
// contents after the last caret. String does not render flags. The number
// column is at least four wide and grows to fit the largest number in the
// view, so every row lines up.
//
// Control characters render as their pictures, and a rune that takes two
// cells in a terminal gets two carets, so the carets stay under the runes
// they mark in a fixed-width font. The output holds no escape sequences,
// so it goes into a log or a golden file as it is, and
// [go.jacobcolvin.com/niceyaml.FormatError] prints the excerpt of a
// bound error this way. A printer renders the same
// view with styles. An empty view renders as "".
func (v *View) String() string {
	width := 4
	for _, ln := range v.All() {
		width = max(width, len(strconv.Itoa(ln.Number())))
	}

	blank := strings.Repeat(" ", width) + " | "

	var rows []string

	for i, ln := range v.All() {
		anns := v.Annotations(i)

		// An annotation without content adds no row, as it adds none to
		// the marker row. The row starts at the column of the annotations
		// as the content row renders it, so it lines up on a line holding
		// wide or control characters.
		if kept := anns.Filter(Above).WithContent(); len(kept) > 0 {
			padding := strings.Repeat(" ", colWidth(ln, kept.Col()))
			rows = append(rows, blank+padding+escape.Control(strings.Join(kept.Contents(), "; ")))
		}

		// A line with no number, such as the placeholder a diff puts
		// opposite an inserted or deleted line, gets a blank gutter, as
		// the printer gives it one.
		number := ""
		if ln.Number() > 0 {
			number = strconv.Itoa(ln.Number())
		}

		rows = append(rows, fmt.Sprintf("%*s | %s", width, number, escape.Control(ln.Content())))

		if marker := markerRow(ln, v.Overlays(i), anns.Filter(Below)); marker != "" {
			rows = append(rows, blank+marker)
		}
	}

	return strings.Join(rows, "\n")
}

// colWidth returns the width in cells of the content of ln before col, as
// the content row renders it, with a column past the end of the content
// taking one cell.
func colWidth(ln *Line, col int) int {
	runes := []rune(ln.Content())
	col = max(0, col)

	if col <= len(runes) {
		return ansi.StringWidth(escape.Control(string(runes[:col])))
	}

	return ansi.StringWidth(escape.Control(ln.Content())) + col - len(runes)
}

// markerRow returns the row below ln that marks its overlays and carries
// its annotations: a caret under every column an overlay covers within the
// line, a caret at the column of the annotations, and their contents after
// the last caret. A column is as many carets wide as the rune on it
// renders, so the carets stay under the runes they mark on a line holding
// wide or control characters. Returns "" when the line has neither.
func markerRow(ln *Line, overlays Overlays, below Annotations) string {
	var marks []bool

	mark := func(col int) {
		col = max(0, col)
		if col >= len(marks) {
			marks = append(marks, make([]bool, col+1-len(marks))...)
		}

		marks[col] = true
	}

	for _, o := range overlays {
		for col := max(0, o.Cols.Start); col < min(o.Cols.End, ln.Width()); col++ {
			mark(col)
		}
	}

	contents := below.WithContent().Contents()
	if len(contents) > 0 {
		mark(below.WithContent().Col())
	}

	if len(marks) == 0 {
		return ""
	}

	var sb strings.Builder

	// The content row renders each rune at its display width, and a column
	// past the end of the content takes one cell.
	runes := []rune(ln.Content())

	for col, marked := range marks {
		cell := " "
		if marked {
			cell = "^"
		}

		cells := 1
		if col < len(runes) {
			cells = ansi.StringWidth(escape.Control(string(runes[col])))
		}

		sb.WriteString(strings.Repeat(cell, cells))
	}

	if len(contents) > 0 {
		sb.WriteByte(' ')
		sb.WriteString(escape.Control(strings.Join(contents, "; ")))
	}

	return sb.String()
}
