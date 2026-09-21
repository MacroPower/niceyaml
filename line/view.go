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
// Index-taking methods panic when the index is outside the view, as
// indexing a slice does. A View is not safe for concurrent mutation.
// Decorate from one goroutine at a time, and do not decorate while another
// goroutine renders.
//
// Create instances with [NewView]. The zero value is an empty view.
type View struct {
	lines       []*Line
	flags       []Flag
	overlays    []Overlays
	annotations []Annotations
}

// NewView creates a new [*View] over lines with no decoration.
func NewView(lines Lines) *View {
	return &View{lines: lines.lines}
}

// Lines returns the content of the [View].
func (v *View) Lines() Lines {
	if v == nil {
		return Lines{}
	}

	return Lines{lines: v.lines}
}

// Len returns the number of lines.
func (v *View) Len() int {
	if v == nil {
		return 0
	}

	return len(v.lines)
}

// Line returns the [*Line] at index i.
func (v *View) Line(i int) *Line {
	return v.lines[i]
}

// Indices returns every index of the [View] that holds l, in view order.
// Lines are shared by pointer between every view over the same content,
// so a decorator that knows a line of a [Lines] value finds where that line
// sits in a slice of it, or in a diff that interleaves it with another
// revision, without knowing how the view was built. A line the view does
// not hold, such as one from other content, yields nil.
func (v *View) Indices(l *Line) []int {
	if v == nil || l == nil {
		return nil
	}

	var out []int

	for i, vl := range v.lines {
		if vl == l {
			out = append(out, i)
		}
	}

	return out
}

// All returns an iterator over the lines within the given spans, as
// [Lines.All] does. Each iteration yields the 0-indexed line index and
// the [*Line] at that index, and the index reaches the line's decoration
// through [View.Flag], [View.Overlays], and [View.Annotations].
func (v *View) All(spans ...position.Span) iter.Seq2[int, *Line] {
	return v.Lines().All(spans...)
}

// Flag returns the [Flag] of line i. The zero value is [FlagDefault].
func (v *View) Flag(i int) Flag {
	_ = v.lines[i]

	if v.flags == nil {
		return FlagDefault
	}

	return v.flags[i]
}

// SetFlag sets the [Flag] of line i.
func (v *View) SetFlag(i int, f Flag) {
	_ = v.lines[i]

	if v.flags == nil {
		v.flags = make([]Flag, len(v.lines))
	}

	v.flags[i] = f
}

// Annotations returns the [Annotation] values on line i, in the order they
// were added. The slice is shared with the view, so treat it as read-only
// and add to it with [View.Annotate].
func (v *View) Annotations(i int) Annotations {
	_ = v.lines[i]

	if v.annotations == nil {
		return nil
	}

	return v.annotations[i]
}

// Annotate adds the given [Annotation] values to line i.
func (v *View) Annotate(i int, ann ...Annotation) {
	_ = v.lines[i]

	if v.annotations == nil {
		v.annotations = make([]Annotations, len(v.lines))
	}

	v.annotations[i] = append(v.annotations[i], ann...)
}

// Overlays returns the [Overlay] values on line i, in the order they were
// added. The slice is shared with the view, so treat it as read-only and
// add to it with [View.AddOverlay], [View.BlendOverlay], or
// [View.AddLineOverlay].
func (v *View) Overlays(i int) Overlays {
	_ = v.lines[i]

	if v.overlays == nil {
		return nil
	}

	return v.overlays[i]
}

// AddLineOverlay adds the given [Overlay] values to line i as given.
// [View.AddOverlay] and [View.BlendOverlay] clamp a range to the lines it
// covers before adding; AddLineOverlay does not.
func (v *View) AddLineOverlay(i int, o ...Overlay) {
	_ = v.lines[i]

	if v.overlays == nil {
		v.overlays = make([]Overlays, len(v.lines))
	}

	v.overlays[i] = append(v.overlays[i], o...)
}

// AddOverlay adds an overlay with the given style to the specified ranges.
// The overlay replaces the style underneath it; use [View.BlendOverlay] to
// mix with it instead.
//
// It splits each range into one overlay per line with [Lines.SliceLines],
// which clamps the columns to the width of the line and skips lines
// outside the view, so a range computed against a longer view is safe to
// apply. A range that covers no columns of a line adds no overlay to it.
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
// covers within the view.
func (v *View) addOverlayRange(s kind.Kind, blend bool, r position.Range) {
	for _, lr := range v.Lines().SliceLines(r) {
		v.AddLineOverlay(lr.Start.Line, Overlay{
			Cols:  position.NewSpan(lr.Start.Col, lr.End.Col),
			Kind:  s,
			Blend: blend,
		})
	}
}

// Clone returns a copy of the [View] with its own decoration. The copy
// shares the lines with the original, so it costs one copy of the flags,
// overlays, and annotations, and decorating either reaches nothing in the
// other.
func (v *View) Clone() *View {
	if v == nil {
		return nil
	}

	c := &View{lines: v.lines}

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

// Slice returns a new [*View] holding the lines within the given spans, in
// the supplied order, each with its decoration. Spans are clamped to the
// view as [Lines.All] clamps them. The result shares the lines with
// the receiver and owns its decoration, so it is the view a caller renders
// to show part of a document, such as the hunks around an error.
func (v *View) Slice(spans ...position.Span) *View {
	out := &View{}

	for i := range v.All(spans...) {
		out.lines = append(out.lines, v.lines[i])

		if v.flags != nil {
			out.flags = append(out.flags, v.flags[i])
		}

		if v.overlays != nil {
			out.overlays = append(out.overlays, slices.Clone(v.overlays[i]))
		}

		if v.annotations != nil {
			out.annotations = append(out.annotations, slices.Clone(v.annotations[i]))
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
