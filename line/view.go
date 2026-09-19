package line

import (
	"fmt"
	"iter"
	"slices"
	"strings"

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
	lines       Lines
	flags       []Flag
	overlays    []Overlays
	annotations []Annotations
}

// NewView creates a new [*View] over lines with no decoration.
func NewView(lines Lines) *View {
	return &View{lines: lines}
}

// Lines returns the content of the [View]. The slice is the view's own, so
// treat it as read-only.
func (v *View) Lines() Lines {
	if v == nil {
		return nil
	}

	return v.lines
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

// AllLines returns an iterator over the lines within the given spans, as
// [Lines.AllLines] does. Each iteration yields the 0-indexed line index and
// the [*Line] at that index, and the index reaches the line's decoration
// through [View.Flag], [View.Overlays], and [View.Annotations].
func (v *View) AllLines(spans ...position.Span) iter.Seq2[int, *Line] {
	return v.Lines().AllLines(spans...)
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
// It splits multi-line ranges into per-line overlays and clamps each
// overlay's columns to its line's width. It skips lines outside the view,
// the same way [Lines.AllLines] clamps its spans, so a range computed
// against a longer view is safe to apply. A range that covers no columns
// of a line adds no overlay to it.
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

// addOverlayRange adds a single overlay range, splitting across lines as
// needed and skipping lines outside the view.
func (v *View) addOverlayRange(s kind.Kind, blend bool, r position.Range) {
	for _, lineRange := range r.SliceLines() {
		lineIdx := lineRange.Start.Line
		if lineIdx < 0 || lineIdx >= len(v.lines) {
			continue
		}

		cols := position.NewSpan(
			max(0, lineRange.Start.Col),
			min(lineRange.End.Col, v.lines[lineIdx].Width()),
		)
		if cols.Len() <= 0 {
			continue
		}

		v.AddLineOverlay(lineIdx, Overlay{Cols: cols, Kind: s, Blend: blend})
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
// view as [Lines.AllLines] clamps them. The result shares the lines with
// the receiver and owns its decoration, so it is the view a caller renders
// to show part of a document, such as the hunks around an error.
func (v *View) Slice(spans ...position.Span) *View {
	out := &View{}

	for i := range v.AllLines(spans...) {
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

// String reconstructs every line as a string, including its annotations.
// This should generally only be used for debugging.
func (v *View) String() string {
	var sb strings.Builder

	for i, l := range v.AllLines() {
		if i > 0 {
			sb.WriteByte('\n')
		}

		prefix := fmt.Sprintf("%4d | ", l.Number())
		anns := v.Annotations(i)

		// Render annotations above if applicable.
		above := anns.Filter(Above)
		if len(above) > 0 {
			sb.WriteString(prefix)
			sb.WriteString(above.String())
			sb.WriteByte('\n')
		}

		sb.WriteString(prefix)
		sb.WriteString(l.Content())

		// Render annotations below if applicable, with the "^ " prefix
		// that marks an error pointer in debug output.
		below := anns.Filter(Below)
		if len(below) > 0 {
			sb.WriteByte('\n')
			sb.WriteString(prefix)

			padding := strings.Repeat(" ", max(0, below.Col()))
			sb.WriteString(padding)
			sb.WriteString("^ ")
			sb.WriteString(strings.Join(below.Contents(), "; "))
		}
	}

	return sb.String()
}
