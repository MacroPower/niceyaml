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
// owns its decoration alone, so creating one costs nothing and decorating
// one reaches no other. Two renderings of one document, such as search
// highlights and error marks, are two Views over the same [Lines].
//
// Every index and every [position.Range] a View takes or yields is in the
// coordinates of its content, the [Lines] that [View.Lines] returns, where
// line i is [Lines.Line] i. A View from [View.Slice] holds some of those
// lines and keeps their indices, so a range from a search of the content
// or from the path of a document applies to a slice of the content as it
// applies to the whole, and slicing before or after decorating renders the
// same. [View.All] yields the lines the View holds with their indices, and
// [View.Contains] reports whether it holds a line.
//
// Index-taking methods panic when the index is outside the content, as
// indexing a slice does. Decoration on a line the View does not hold is
// kept and never renders. A View is not safe for concurrent mutation.
// Decorate from one goroutine at a time, and do not decorate while another
// goroutine renders.
//
// Create instances with [NewView]. The zero value is an empty view.
type View struct {
	lines Lines
	// The index in lines of each line the View holds, in view order.
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

// Lines returns the content of the [View]: every line of the [Lines] it is
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

// Len returns the number of lines the [View] holds.
func (v *View) Len() int {
	if v == nil {
		return 0
	}

	return len(v.held)
}

// Line returns line i of the content, whether or not the [View] holds it.
func (v *View) Line(i int) *Line {
	return v.lines.lines[i]
}

// Contains reports whether the [View] holds line i of its content. A nil
// View holds no line, and an index outside the content is held by none.
func (v *View) Contains(i int) bool {
	if v == nil || i < 0 || i >= len(v.mask) {
		return false
	}

	return v.mask[i]
}

// Indices returns the index of every line the [View] holds that is l, in
// view order and each once. Lines are shared by pointer between every view
// over the same content, so a decorator that knows a line of a [Lines]
// value finds it in a slice of that content, or in a diff that interleaves
// it with another revision, without knowing how the view was built. A
// line the view does not hold, such as one from other content, yields nil.
func (v *View) Indices(l *Line) []int {
	if v == nil || l == nil {
		return nil
	}

	var out []int

	for _, i := range v.held {
		if v.lines.lines[i] == l && !slices.Contains(out, i) {
			out = append(out, i)
		}
	}

	return out
}

// All returns an iterator over the lines the [View] holds within the given
// spans, in view order within each span and in the order the spans are
// given, or over every line it holds when no span is given. Each iteration
// yields the index of the line in the content and the [*Line], and the
// index reaches the line's decoration through [View.Flag],
// [View.Overlays], and [View.Annotations]. A span reaching outside the
// content selects the lines it does hold.
func (v *View) All(spans ...position.Span) iter.Seq2[int, *Line] {
	return func(yield func(int, *Line) bool) {
		if v == nil {
			return
		}

		if len(spans) == 0 {
			for _, i := range v.held {
				if !yield(i, v.lines.lines[i]) {
					return
				}
			}

			return
		}

		for _, span := range spans {
			for _, i := range v.held {
				if span.Contains(i) && !yield(i, v.lines.lines[i]) {
					return
				}
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
// the receiver holds within the given spans, in the order [View.All]
// yields them, each with its index and its decoration. The result owns
// its decoration and carries that of the lines it holds, so it is the view
// a caller renders to show part of a document, such as the hunks around
// an error, and a range or an index that applies to the receiver applies
// to it. Slicing an already sliced view narrows it further.
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

// String renders the [View] as plain text: each line behind its number,
// the annotations above it on rows of their own, and a row below it that
// marks its decoration, with a caret under every column an overlay covers,
// a caret at the column of the annotations below the line, and their
// contents after the last caret. Flags are not rendered. The number
// column is at least four wide and grows to fit the largest number in the
// view, so every row lines up.
//
// Control characters render as their pictures, and a rune that takes two
// cells in a terminal gets two carets, so the carets stay under the runes
// they mark in a fixed-width font. The output holds no escape sequences,
// so it goes into a log or a golden file as it is, and the %+v verb of a
// bound error prints its excerpt this way. A printer renders the same
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
		// the marker row.
		if above := anns.Filter(Above).String(); above != "" {
			rows = append(rows, blank+escape.Control(above))
		}

		rows = append(rows, fmt.Sprintf("%*d | %s", width, ln.Number(), escape.Control(ln.Content())))

		if marker := markerRow(ln, v.Overlays(i), anns.Filter(Below)); marker != "" {
			rows = append(rows, blank+marker)
		}
	}

	return strings.Join(rows, "\n")
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
