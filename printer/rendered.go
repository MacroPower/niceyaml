package printer

import (
	"slices"
	"strings"

	"go.jacobcolvin.com/niceyaml/line"
)

// Rendered is a view as a [Printer] renders it: the rows of the view with
// the [Layout] that measures them. Both come from one pass over the view,
// so row n of [Rendered.Rows] is row n of [Rendered.Layout]. A viewer that
// scrolls by row, or that maps a pointer to a position, takes the rows and
// the layout from one Rendered.
//
//	r := p.Render(view)
//	layout := r.Layout()
//	row := r.Rows()[layout.RowOf(pos)]
//	cell := layout.GutterWidth() + layout.CellOf(pos)
//
// The rows leave the container out. They do not hold the frame of the
// container style, and they do not hold the pad of [WithContainerWidth].
// Each row is thus as wide as its content. [Printer.Pad] pads a row to
// the window of a viewer. [Rendered.String] joins the rows inside the
// container, as [Printer.Print] returns them.
//
// A Rendered stays valid until the view or the printer changes.
//
// Create instances with [Printer.Render].
type Rendered struct {
	printer *Printer
	rows    []string
	layout  Layout
}

// Render renders view as rows and computes their [Layout] in the same
// pass. [Printer.Print] describes the rows each line takes. To render
// part of a document, pass the view [line.View.Slice] returns.
//
// A viewer that needs only the measures of a view, such as its row count,
// takes them from [Printer.Layout], which does not assemble the rows.
func (p *Printer) Render(view *line.View) *Rendered {
	return p.render(view, true)
}

// Rows returns the rendered rows without the container. Row n is row n
// of [Rendered.Layout], and an empty view has zero rows. The slice
// belongs to the caller.
func (r *Rendered) Rows() []string {
	return slices.Clone(r.rows)
}

// Layout returns the [Layout] of the rows. It is the layout
// [Printer.Layout] computes for the same view.
func (r *Rendered) Layout() Layout {
	return r.layout
}

// String returns the rows joined by line feeds inside the container
// style, with each row padded as [WithContainerWidth] sets. It is what
// [Printer.Print] returns for the view. An empty view renders as the
// container around one empty row. The zero Rendered renders as "".
func (r *Rendered) String() string {
	if r.printer == nil {
		return ""
	}

	return r.printer.frame(r.rows)
}

// frame joins rows inside the container style, with each row padded out
// to the container width.
func (p *Printer) frame(rows []string) string {
	return p.style.Render(strings.Join(p.padRows(rows), "\n"))
}
