package printer

import (
	"slices"

	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

// sideBySideSeparator is the divider between the panes that
// [Printer.RenderSideBySide] renders.
const sideBySideSeparator = " │ "

// SideBySide is two views as a [Printer] renders them beside each other:
// the zipped rows, the rows of each pane, and the geometry of both.
// [Printer.PrintSideBySide] describes how the panes pair their lines and
// which rows each line takes.
//
// A zipped row holds a row of the left pane, the separator, and a row of
// the right pane. [SideBySide.Rows] returns the zipped rows, and
// [SideBySide.Panes] returns the rows of each pane for a viewer that
// cuts or frames the panes itself. Both leave the container out, as
// [Rendered.Rows] does, and [SideBySide.String] joins the zipped rows
// inside it.
//
// [SideBySide.LineStart], [SideBySide.LineRows], and [SideBySide.LineAt]
// count zipped rows. Each takes or returns the index of a line in the
// content, as the [Layout] method of the same name does. A viewer that
// scrolls by row maps rows to lines and back with them:
//
//	s := p.RenderSideBySide(before, after)
//	rows := s.Rows()
//	top := s.LineStart(i)
//	window := rows[top:min(top+height, len(rows))]
//
// A pane fills the rows that only the other pane takes with filler rows,
// which do not show a row of its view. [SideBySide.PaneRow] maps a zipped
// row to the row of one pane's [Layout], and [SideBySide.PaneStart] gives
// the cell where that pane starts. A viewer that takes a pointer finds
// the position under it with them:
//
//	right := cell >= s.PaneStart(true)
//	if paneRow, ok := s.PaneRow(right, row); ok {
//		left, layout := s.Layouts()
//		if !right {
//			layout = left
//		}
//
//		pos, ok := layout.PositionAt(paneRow, cell-s.PaneStart(right)-layout.GutterWidth())
//		// ...
//	}
//
// A SideBySide stays valid until either view or the printer changes.
//
// Create instances with [Printer.RenderSideBySide].
type SideBySide struct {
	printer    *Printer
	panes      [2][]string // The rows of the left pane and of the right, filler rows included.
	rows       []string    // The zipped rows.
	indices    []int       // indices[k] is the index in the content of the k-th paired line, ascending.
	starts     []int       // starts[k] is the first zipped row of the k-th paired line, and the last entry is the row count.
	layouts    [2]Layout   // The layout of the left pane and of the right.
	rightStart int         // The cell of a zipped row at which the right pane starts.
}

// RenderSideBySide renders left and right as two panes beside each other
// and returns their rows with the geometry of both panes.
// [Printer.PrintSideBySide] describes how the panes pair their lines, how
// their rows align, and how wide each pane is.
//
// Each pane renders through a copy of p with an empty container style and
// without a container width. The copy holds the larger [Printer.MaxNumber]
// of the two views in [WithMaxNumber]. When p wraps, the copy wraps to
// the pane width. [SideBySide.Layouts] returns the layouts of that copy.
func (p *Printer) RenderSideBySide(left, right *line.View) *SideBySide {
	return p.renderSideBySide(left, right, true)
}

// renderSideBySide renders left and right as [Printer.RenderSideBySide]
// describes. With measure set, it also computes the [Layout] of each pane
// in the same pass. The rows are the same either way. Without measure,
// the layouts stay empty, so the result serves only its rows and
// [SideBySide.String].
func (p *Printer) renderSideBySide(left, right *line.View, measure bool) *SideBySide {
	maxNumber := max(p.MaxNumber(left), p.MaxNumber(right))

	// Each pane renders without the container, which wraps the joined
	// rows, and wraps to its half of the width.
	pane := p.With(WithContainerStyle(lipgloss.NewStyle()), WithContainerWidth(0), WithMaxNumber(maxNumber))

	paneWidth := 0
	if p.wrap > 0 {
		paneWidth = max(1, (p.wrap-p.style.GetHorizontalFrameSize()-lipgloss.Width(sideBySideSeparator))/2)
		pane = pane.With(WithWrapWidth(paneWidth))
	}

	gutterWidth := pane.gutterWidth(maxNumber)

	views := [2]*line.View{left, right}
	s := &SideBySide{printer: p, starts: []int{0}}

	if measure {
		s.layouts = [2]Layout{newLayout(left.Count(), gutterWidth), newLayout(right.Count(), gutterWidth)}
	}

	for _, i := range pairedLines(left, right) {
		// A pane that does not hold the line has no rows for it.
		var lines [2]lineRows

		for side, view := range views {
			if !view.Contains(i) {
				continue
			}

			lines[side] = pane.renderLine(view, i, view.Line(i), maxNumber, gutterWidth, measure)
			if measure {
				s.layouts[side] = s.layouts[side].withLine(i, lines[side].layout)
			}
		}

		// The blocks above the first content rows end on one row, and the
		// rest of the shorter pane fills to the bottom of the line.
		lead := max(lines[0].lead, lines[1].lead)
		rest := max(len(lines[0].rows)-lines[0].lead, len(lines[1].rows)-lines[1].lead)

		for side, view := range views {
			s.panes[side] = append(s.panes[side],
				pane.alignRows(view, i, lines[side].rows, lead-lines[side].lead, lead+rest, maxNumber, gutterWidth)...)
		}

		s.indices = append(s.indices, i)
		s.starts = append(s.starts, s.starts[len(s.starts)-1]+lead+rest)
	}

	leftWidth := paneWidth
	for _, row := range s.panes[0] {
		leftWidth = max(leftWidth, lipgloss.Width(row))
	}

	separator := p.Style(kind.UISeparator).Render(sideBySideSeparator)
	s.rightStart = leftWidth + lipgloss.Width(sideBySideSeparator)
	s.rows = make([]string, len(s.panes[0]))

	for k := range s.rows {
		s.rows[k] = p.Pad(s.panes[0][k], leftWidth) + separator + s.panes[1][k]
	}

	return s
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

// alignRows returns rows, the rows of line i in the pane of view, with top
// blank rows above them and as many below as fill total rows. A blank row
// holds the gutter of an annotation row of the line. A gutter that draws
// a border or a background thus keeps it down the pane.
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

// Rows returns the zipped rows without the container. Each row holds a
// row of the left pane padded to the width of that pane, the separator,
// and a row of the right pane. Two empty views have zero rows. The slice
// belongs to the caller.
func (s *SideBySide) Rows() []string {
	return slices.Clone(s.rows)
}

// Panes returns the rows of the left pane and the rows of the right pane,
// as [SideBySide.Rows] zips them. Both slices hold one row for each zipped
// row, filler rows included. A row of a pane is as wide as its content.
// The rows of the left pane thus come without the pad that the zipped
// rows give them. A filler row holds the gutter of the line it sits
// beside. Both slices belong to the caller.
func (s *SideBySide) Panes() ([]string, []string) {
	return slices.Clone(s.panes[0]), slices.Clone(s.panes[1])
}

// Layouts returns the [Layout] of the left pane and the [Layout] of the
// right pane. Each measures the rows of its view without the filler rows.
// [SideBySide.PaneRow] maps a zipped row to a row of one of them.
func (s *SideBySide) Layouts() (Layout, Layout) {
	return s.layouts[0], s.layouts[1]
}

// position returns the position of line i of the content among the paired
// lines, and whether either pane holds that line.
func (s *SideBySide) position(i int) (int, bool) {
	return slices.BinarySearch(s.indices, i)
}

// LineStart returns the first zipped row of line i of the content, or -1
// when neither pane holds the line.
func (s *SideBySide) LineStart(i int) int {
	k, ok := s.position(i)
	if !ok {
		return -1
	}

	return s.starts[k]
}

// LineRows returns the number of zipped rows line i of the content takes.
// The count is the larger block of rows above the first content row in
// either pane, plus the larger number of rows from that row to the end of
// the line. A line neither pane holds takes zero rows.
func (s *SideBySide) LineRows(i int) int {
	k, ok := s.position(i)
	if !ok {
		return 0
	}

	return s.starts[k+1] - s.starts[k]
}

// LineAt returns the index in the content of the line that holds zipped
// row. A row before the first belongs to the first line, and a row past
// the last belongs to the last line, as in [Layout.LineAt]. LineAt
// returns -1 when neither pane holds a line.
func (s *SideBySide) LineAt(row int) int {
	n := len(s.indices)
	if n == 0 {
		return -1
	}

	return s.indices[rowIndex(s.starts[:n], row)]
}

// PaneRow returns the row of a pane that zipped row shows, and true. The
// pane is the left one, or the right one when right is true. The row it
// returns is a row of the [Layout] that [SideBySide.Layouts] returns for
// that pane. PaneRow reports false for a filler row, which does not show
// a row of the pane, and for a row outside the zipped rows.
func (s *SideBySide) PaneRow(right bool, row int) (int, bool) {
	if row < 0 || row >= len(s.rows) {
		return 0, false
	}

	k := rowIndex(s.starts[:len(s.indices)], row)
	i := s.indices[k]

	layout, other := s.layouts[0], s.layouts[1]
	if right {
		layout, other = other, layout
	}

	// The pane with the shorter block above the first content row starts
	// its rows below filler rows. Its rows end above filler rows when the
	// other pane takes more rows after that.
	at := row - s.starts[k] - max(0, other.LineLead(i)-layout.LineLead(i))
	if at < 0 || at >= layout.LineRows(i) {
		return 0, false
	}

	return layout.LineStart(i) + at, true
}

// PaneStart returns the cell of a zipped row at which the left pane
// starts, or the right pane when right is true. The left pane starts at
// cell 0, and the right pane starts after the padded left pane and the
// separator. A viewer subtracts the start from a cell of a zipped row to
// find the cell of the pane row.
func (s *SideBySide) PaneStart(right bool) int {
	if !right {
		return 0
	}

	return s.rightStart
}

// String returns the zipped rows joined by line feeds inside the
// container style, with each row padded as [WithContainerWidth] sets. It
// is what [Printer.PrintSideBySide] returns for the views. The zero
// SideBySide renders as "".
func (s *SideBySide) String() string {
	if s.printer == nil {
		return ""
	}

	return s.printer.frame(s.rows)
}
