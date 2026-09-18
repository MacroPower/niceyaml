package printer

import (
	"sort"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"go.jacobcolvin.com/niceyaml/internal/escape"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
)

// Layout is the row structure of a view as [Printer.Print] renders it: how
// many rows each line takes once its content wraps and its annotations are
// added, which row a position in the view lands on, and how wide the rows
// are. A viewer that scrolls by rendered row maps rows to lines and back
// through it, and one that scrolls horizontally reads the width of the
// widest row.
//
// A Layout styles and wraps each line as [Printer.Print] does, since a
// style may transform the text it styles and change its width, but it
// assembles no output, so one Layout replaces a render for every question
// about rows. It stays valid until the view or the printer changes.
//
// Line i of the layout is line i of the view. Rows count from 0 at the
// first row of the first line and leave the container style out.
//
// Create instances with [Printer.Layout].
type Layout struct {
	lines       []lineLayout
	starts      []int // starts[i] is the first row of line i; starts[len(lines)] is the row count.
	width       int
	gutterWidth int
}

// lineLayout is the row structure of one line: the annotation rows above
// its content, the content column each content row starts at, and the
// annotation rows below.
type lineLayout struct {
	rows  []int
	above int
	below int
}

// Layout computes the [Layout] of view as [Printer.Print] would render it.
func (p *Printer) Layout(view *line.View) Layout {
	maxNumber := p.MaxNumber(view)
	gutterWidth := p.gutterWidth(maxNumber)
	l := Layout{
		lines:       make([]lineLayout, 0, view.Len()),
		starts:      make([]int, 1, view.Len()+1),
		gutterWidth: gutterWidth,
	}

	for idx, ln := range view.AllLines() {
		ll := p.layoutLine(view, idx, ln, gutterWidth, &l.width)

		l.lines = append(l.lines, ll)
		l.starts = append(l.starts, l.starts[len(l.starts)-1]+ll.above+len(ll.rows)+ll.below)
	}

	return l
}

// layoutLine computes the row structure of line idx of view, which is ln,
// and raises *width to the widest row of the line.
func (p *Printer) layoutLine(view *line.View, idx int, ln *line.Line, gutterWidth int, width *int) lineLayout {
	var ll lineLayout

	if p.annotationsEnabled {
		ll.above = p.layoutAnnotation(view, ln, idx, gutterWidth, line.Above, width)
	}

	// The content wraps as the rendered line does, styles included, since
	// a style's transform may change the shown text. The wrap is
	// ANSI-aware and measures the shown cells.
	pieces := p.wrapContent(p.renderContent(view, idx, ln), gutterWidth)

	// The columns come from the shown text of each piece matched against
	// the escaped content, which is what the rows spell out when no style
	// transforms it.
	plain := make([]string, len(pieces))
	for i, piece := range pieces {
		plain[i] = ansi.Strip(piece)
		*width = max(*width, gutterWidth+lipgloss.Width(piece))
	}

	ll.rows = rowStarts(escape.Control(ln.Content()), plain)

	if p.annotationsEnabled {
		ll.below = p.layoutAnnotation(view, ln, idx, gutterWidth, line.Below, width)
	}

	return ll
}

// layoutAnnotation returns the number of rows the annotations of line idx
// at placement take, as [Printer.renderAnnotation] renders them, and
// raises *width to the widest of them.
func (p *Printer) layoutAnnotation(
	view *line.View,
	ln *line.Line,
	idx, gutterWidth int,
	placement line.Placement,
	width *int,
) int {
	var rows int

	for _, group := range p.annotationGroups(view, ln, idx, gutterWidth, placement) {
		for _, row := range group.rows {
			*width = max(*width, gutterWidth+group.indentWidth+lipgloss.Width(row))
		}

		rows += len(group.rows)
	}

	return rows
}

// rowStarts returns the column of content at which each piece of its
// wrapped form begins. The wrapper drops the spaces at a break and at the
// end of the content, and a style's transform may add text of its own, so
// the columns come from matching the runes of each piece against the
// content in order, skipping the spaces the wrapper dropped and the runes
// the content does not hold.
func rowStarts(content string, pieces []string) []int {
	runes := []rune(content)
	starts := make([]int, len(pieces))
	next := 0

	for i, piece := range pieces {
		for j, r := range piece {
			for next < len(runes) && runes[next] != r && (runes[next] == ' ' || runes[next] == '\t') {
				next++
			}

			if j == 0 {
				starts[i] = next
			}

			if next < len(runes) && runes[next] == r {
				next++
			}
		}

		if piece == "" {
			starts[i] = next
		}
	}

	return starts
}

// Rows returns the number of rows the view takes.
func (l Layout) Rows() int {
	if len(l.starts) == 0 {
		return 0
	}

	return l.starts[len(l.starts)-1]
}

// Len returns the number of lines in the layout.
func (l Layout) Len() int {
	return len(l.lines)
}

// LineRows returns the number of rows line i takes: one for each wrapped
// piece of its content and one for each wrapped piece of its annotations.
func (l Layout) LineRows(i int) int {
	ll := l.lines[i]

	return ll.above + len(ll.rows) + ll.below
}

// LineStart returns the first row of line i.
func (l Layout) LineStart(i int) int {
	_ = l.lines[i]

	return l.starts[i]
}

// LineAt returns the index of the line that holds row. A row before the
// first belongs to the first line and a row past the last to the last, so
// a scroll offset always names a line. Returns -1 when the layout holds no
// lines.
func (l Layout) LineAt(row int) int {
	n := len(l.lines)
	if n == 0 {
		return -1
	}

	// The last start at or before row, which is the line whose rows hold
	// it; SearchInts finds the first start past row.
	i := sort.SearchInts(l.starts[:n], row+1) - 1

	return min(max(i, 0), n-1)
}

// RowOf returns the row that holds column pos.Col of line pos.Line: the
// content row the column wraps onto, below any annotation rows above the
// line. A column in the spaces the wrapper dropped at a break belongs to
// the row before the break, and one past the end of the content to the
// last row. Returns -1 when the layout does not hold the line.
func (l Layout) RowOf(pos position.Position) int {
	if pos.Line < 0 || pos.Line >= len(l.lines) {
		return -1
	}

	ll := l.lines[pos.Line]

	// The last content row that starts at or before the column.
	row := sort.SearchInts(ll.rows, pos.Col+1) - 1

	return l.starts[pos.Line] + ll.above + max(row, 0)
}

// Width returns the width in cells of the widest row, gutter included,
// before the container style applies. A viewer that scrolls horizontally
// uses it to find the column the last row ends on.
func (l Layout) Width() int {
	return l.width
}

// GutterWidth returns the width in cells of the gutter on every row, so a
// viewer that scrolls horizontally subtracts it from [Layout.Width] to
// find the width of the content.
func (l Layout) GutterWidth() int {
	return l.gutterWidth
}
