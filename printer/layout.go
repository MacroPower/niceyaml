package printer

import (
	"slices"
	"sort"
	"unicode"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
)

// Layout is the row structure of a view as [Printer.Print] renders it: how
// many rows each line takes once its content wraps and its annotations take
// rows of their own, which row a position in the view lands on, and how
// wide the rows are. A viewer that scrolls by rendered row maps rows to
// lines and back through it, and one that scrolls horizontally reads the
// width of the widest row.
//
// A Layout styles and wraps each line as [Printer.Print] does, since a
// style may transform the text it styles and change its width, but it
// assembles no output, so one Layout replaces a render for every question
// about rows. It stays valid until the view or the printer changes.
//
// The layout speaks in the coordinates of the view. [Layout.LineRows],
// [Layout.LineStart], and [Layout.LineAt] take and return the index of a
// line in the content of the view, the one every [line.View] method
// takes, so a viewer that finds the line at a row reaches its decoration
// through the view with the same index, and [Layout.RowOf] takes a
// position in the content, as a search yields one. A line the view does
// not hold takes no rows and starts nowhere. Rows count from 0 at the
// first row of the first line and leave the container style out. An empty
// view has no rows, though [Printer.Print] draws one empty row for it to
// carry the container.
//
// Create instances with [Printer.Layout].
type Layout struct {
	lines       []lineLayout
	indices     []int // indices[k] is the index in the view's content of the k-th line, ascending.
	starts      []int // starts[k] is the first row of the k-th line; starts[len(lines)] is the row count.
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
		lines:       make([]lineLayout, 0, view.Count()),
		indices:     make([]int, 0, view.Count()),
		starts:      make([]int, 1, view.Count()+1),
		gutterWidth: gutterWidth,
	}

	for idx, ln := range view.All() {
		ll := p.layoutLine(view, idx, ln, gutterWidth, &l.width)

		l.lines = append(l.lines, ll)
		l.indices = append(l.indices, idx)
		l.starts = append(l.starts, l.starts[len(l.starts)-1]+ll.above+len(ll.rows)+ll.below)
	}

	return l
}

// layoutLine computes the row structure of line idx of view, which is ln,
// and raises *width to the widest row of the line.
func (p *Printer) layoutLine(view *line.View, idx int, ln *line.Line, gutterWidth int, width *int) lineLayout {
	var ll lineLayout

	pieces, starts := p.wrapLine(view, idx, ln, gutterWidth)
	for _, piece := range pieces {
		*width = max(*width, gutterWidth+lipgloss.Width(piece))
	}

	ll.rows = starts
	ll.above = p.layoutAnnotation(view, ln, idx, gutterWidth, line.Above, starts, width)
	ll.below = p.layoutAnnotation(view, ln, idx, gutterWidth, line.Below, starts, width)

	return ll
}

// wrapLine renders the content of line idx of view, which is ln, wraps it
// to the printer width, and returns the pieces with the column of the
// content at which each piece begins.
func (p *Printer) wrapLine(view *line.View, idx int, ln *line.Line, gutterWidth int) ([]string, []int) {
	// The content wraps as the rendered line does, styles included, since
	// a style's transform may change the shown text. The wrap is
	// ANSI-aware and measures the shown cells.
	rendered, runs := p.renderRuns(view, idx)
	pieces := p.wrapContent(rendered, gutterWidth)

	// The wrap cuts the pieces from the shown text of the line, so each
	// piece matches the shown text at the offset where it begins. The
	// runs then map each offset back to a column of the content, which
	// confines a transform that rewrites its text to the run it styles.
	plain := make([]string, len(pieces))
	for i, piece := range pieces {
		plain[i] = ansi.Strip(piece)
	}

	shownText := ansi.Strip(rendered)
	shown := []rune(shownText)
	contentLen := utf8.RuneCountInString(ln.Content())

	starts := rowStarts(shownText, plain)
	for i, offset := range starts {
		starts[i] = sourceCol(runs, shown, offset, contentLen)
	}

	return pieces, starts
}

// sourceCol returns the column of the content at which offset in shown,
// the shown text of a line, falls. Runs holds the runs the line renders
// in, and contentLen is the number of columns of the content. Offset 0 is
// column 0, and an offset at or past the end of shown is contentLen.
//
// The run that shows the rune at offset decides the column. A run that
// shows as many runes as it covers maps rune for rune, as a style that
// only colors its text or changes its case does. In any other run, the
// shown runes before offset match against the text of the run as
// [rowStarts] matches pieces, and the column stays within the run.
func sourceCol(runs []runSpan, shown []rune, offset, contentLen int) int {
	switch {
	case offset <= 0:
		return 0
	case offset >= len(shown):
		return contentLen
	}

	// The last run that starts at or before offset, which shows the rune
	// there. Search finds the first run that starts past offset, and the
	// first run starts at offset 0.
	run := runs[sort.Search(len(runs), func(i int) bool { return runs[i].shown > offset })-1]
	if run.shownLen == run.cols {
		return run.col + offset - run.shown
	}

	text := []rune(run.text)
	next := 0

	for _, r := range shown[run.shown:offset] {
		next = skipDropped(text, next, r)
		if next < len(text) && text[next] == r {
			next++
		}
	}

	return run.col + min(next, run.cols)
}

// layoutAnnotation returns the number of rows the styled annotations of
// line idx at placement take, as [Printer.renderAnnotation] writes them,
// and raises *width to the widest of them. Starts holds the column of the
// content at which each wrapped row of the line begins.
func (p *Printer) layoutAnnotation(
	view *line.View,
	ln *line.Line,
	idx, gutterWidth int,
	placement line.Placement,
	starts []int,
	width *int,
) int {
	var rows int

	for _, group := range p.annotationGroups(view, ln, idx, gutterWidth, placement, starts) {
		for _, row := range group.rows {
			*width = max(*width, gutterWidth+lipgloss.Width(row))
		}

		rows += len(group.rows)
	}

	return rows
}

// nbsp is the non-breaking space, the one Unicode space the wrapper keeps
// at a break.
const nbsp = '\u00a0'

// rowStarts returns the rune offset in text at which each piece of its
// wrapped form begins. It matches the runes of each piece against text
// in order. The wrapper drops every Unicode space but [nbsp] at a break
// and at the end of the text, so the match skips those spaces in text,
// and they are the only runes it skips. The first piece begins at offset
// 0. The caller escapes the text, so a tab shows as its control picture
// and never counts as a space.
func rowStarts(text string, pieces []string) []int {
	runes := []rune(text)
	starts := make([]int, len(pieces))
	next := 0

	for i, piece := range pieces {
		for j, r := range piece {
			next = skipDropped(runes, next, r)

			if j == 0 && i > 0 {
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

// skipDropped returns the index of the first rune of runes at or after
// next that is r or is no space the wrapper drops.
func skipDropped(runes []rune, next int, r rune) int {
	for next < len(runes) && runes[next] != r && unicode.IsSpace(runes[next]) && runes[next] != nbsp {
		next++
	}

	return next
}

// rowIndex returns the index of the row that holds col, given the column
// at which each row begins: the last row that begins at or before col, or
// the first row when col comes before them all.
func rowIndex(starts []int, col int) int {
	// Search finds the first row that begins past col. It compares with >
	// rather than searching for col+1, which overflows at [math.MaxInt].
	return max(0, sort.Search(len(starts), func(i int) bool { return starts[i] > col })-1)
}

// Rows returns the number of rows the view takes.
func (l Layout) Rows() int {
	if len(l.starts) == 0 {
		return 0
	}

	return l.starts[len(l.starts)-1]
}

// Count returns the number of lines the layout holds, which is
// [line.View.Count] of the view it came from.
func (l Layout) Count() int {
	return len(l.lines)
}

// position returns the position among the lines the layout holds of line
// i of the content, and whether the layout holds it.
func (l Layout) position(i int) (int, bool) {
	return slices.BinarySearch(l.indices, i)
}

// LineRows returns the number of rows line i of the content takes: one
// for each wrapped piece of its content and one for each row its styled
// annotations take. A line the layout does not hold takes none.
func (l Layout) LineRows(i int) int {
	k, ok := l.position(i)
	if !ok {
		return 0
	}

	ll := l.lines[k]

	return ll.above + len(ll.rows) + ll.below
}

// LineStart returns the first row of line i of the content, or -1 when
// the layout does not hold the line.
func (l Layout) LineStart(i int) int {
	k, ok := l.position(i)
	if !ok {
		return -1
	}

	return l.starts[k]
}

// LineAt returns the index in the content of the line that holds row. A
// row before the first belongs to the first line and a row past the last
// to the last, so a scroll offset always names a line. Returns -1 when the
// layout holds no lines.
func (l Layout) LineAt(row int) int {
	n := len(l.lines)
	if n == 0 {
		return -1
	}

	// The last start at or before row, which is the line whose rows hold
	// it. The search finds the first start past row.
	k := sort.Search(n, func(i int) bool { return l.starts[i] > row }) - 1

	return l.indices[min(max(k, 0), n-1)]
}

// RowOf returns the row that holds column pos.Col of line pos.Line of the
// content: the content row the column wraps onto, below any annotation
// rows above the line. A column in the spaces the wrapper dropped at a
// break belongs to the row before the break, and one past the end of the
// content to the last row. Returns -1 when the layout does not hold the
// line.
func (l Layout) RowOf(pos position.Position) int {
	k, ok := l.position(pos.Line)
	if !ok {
		return -1
	}

	ll := l.lines[k]

	// The last content row that starts at or before the column.
	row := sort.Search(len(ll.rows), func(i int) bool { return ll.rows[i] > pos.Col }) - 1

	return l.starts[k] + ll.above + max(row, 0)
}

// Width returns the width in cells of the widest row, gutter included,
// before the container style applies. A viewer that scrolls horizontally
// uses it to find how far the content reaches.
func (l Layout) Width() int {
	return l.width
}

// GutterWidth returns the width in cells of the gutter on every row, so a
// viewer that scrolls horizontally subtracts it from [Layout.Width] to
// find the width of the content.
func (l Layout) GutterWidth() int {
	return l.gutterWidth
}
