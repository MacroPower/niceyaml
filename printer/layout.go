package printer

import (
	"math"
	"slices"
	"sort"
	"unicode"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"go.jacobcolvin.com/niceyaml/internal/cells"
	"go.jacobcolvin.com/niceyaml/internal/clip"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
)

// Layout is the row structure of a view as [Printer.Print] renders it: how
// many rows each line takes once its content wraps and its annotations take
// rows of their own, which row a position in the view lands on, and how
// wide the rows are. A viewer that scrolls by rendered row maps rows to
// lines and back through it, and one that scrolls horizontally reads the
// width of the widest row. A viewer that takes a pointer maps the cell
// under it back to a position.
//
// A Layout styles and wraps each line as [Printer.Print] does, since a
// style may transform the text it styles and change its width. It
// assembles no output, so one Layout replaces a render for every question
// about rows. It stays valid until the view or the printer changes.
//
// The layout speaks in the coordinates of the view. [Layout.LineRows],
// [Layout.LineWidth], [Layout.LineStart], and [Layout.LineAt] take and
// return the index of a line in the content of the view, the one every
// [line.View] method takes, so a viewer that finds the line at a row
// reaches its decoration through the view with the same index.
// [Layout.RowOf] and [Layout.CellOf] take a position in the content, as a
// search yields one, and [Layout.PositionAt] returns one. On a line the
// view clips, as [line.View.Clip] sets a view to, a column the row leaves
// out takes the row and the cell of the "..." that stands for it, and no
// cell gives that column back.
// A line the view does not hold takes no rows and starts nowhere. Rows
// count from 0 at the first row of the first line and leave the container
// style out. An empty view has no rows, though [Printer.Print] draws one
// empty row for it to carry the container.
//
// Create instances with [Printer.Layout].
type Layout struct {
	lines       []lineLayout
	indices     []int // indices[k] is the index in the view's content of the k-th line, ascending.
	starts      []int // starts[k] is the first row of the k-th line; starts[len(lines)] is the row count.
	width       int
	gutterWidth int
}

// lineLayout is the row structure of one line. It holds the column of the
// content at which each wrapped content row begins and the row each
// content row takes among the line's rows. It also holds the number of
// rows the line takes, annotation rows included, and the width of the
// widest of them. Its shown text maps a column of the content to the cell
// it takes on its row. For a line the view clips, the content is the text
// of its row, and clip maps a column of the line to it and back.
type lineLayout struct {
	clip    *clip.Map
	cols    []int
	offsets []int
	shown   shownLine
	rows    int
	width   int
}

// col returns the column of the content of the row that shows col, a
// column of the line.
func (ll lineLayout) col(col int) int {
	if ll.clip == nil {
		return col
	}

	return ll.clip.Col(col)
}

// lineCol returns the column of the line that col, a column of the
// content of the row, shows, and true. It reports false for a column of
// a "..." on a line the view clips, which shows no column of the line.
func (ll lineLayout) lineCol(col int) (int, bool) {
	if ll.clip == nil {
		return col, true
	}

	return ll.clip.LineCol(col)
}

// shownLine is the shown text of one line, the rendered line without its
// escape sequences, with the runs it renders in. It holds the offset in
// the text at which each wrapped row begins, and the offset just past the
// last rune each row shows.
type shownLine struct {
	text       string
	runs       []runSpan
	starts     []int
	ends       []int
	contentLen int
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
		ll := p.layoutLine(view, idx, ln, gutterWidth)

		l.width = max(l.width, ll.width)
		l.lines = append(l.lines, ll)
		l.indices = append(l.indices, idx)
		l.starts = append(l.starts, l.starts[len(l.starts)-1]+ll.rows)
	}

	return l
}

// layoutLine computes the row structure of line idx of view, which is ln,
// as [Printer.renderLine] writes its rows.
func (p *Printer) layoutLine(view *line.View, idx int, ln *line.Line, gutterWidth int) lineLayout {
	vl := newViewLine(view, idx, ln)

	w := p.wrapLine(vl, gutterWidth)
	above := p.annotationRows(vl, gutterWidth, line.Above, w)
	below := p.annotationRows(vl, gutterWidth, line.Below, w)

	ll := lineLayout{
		clip:    vl.clip,
		shown:   w.shown,
		cols:    w.starts,
		offsets: make([]int, len(w.pieces)),
	}

	for j, piece := range w.pieces {
		ll.rows += len(rowsAt(above, j))
		ll.offsets[j] = ll.rows
		ll.rows += 1 + len(rowsAt(below, j))
		ll.width = max(ll.width, gutterWidth+lipgloss.Width(piece))
	}

	for _, block := range slices.Concat(above, below) {
		for _, row := range block {
			ll.width = max(ll.width, gutterWidth+lipgloss.Width(row))
		}
	}

	return ll
}

// wrappedLine is the content of one line wrapped to the printer width.
type wrappedLine struct {
	pieces []string  // The wrapped rows of the rendered content.
	starts []int     // The column of the content at which each piece begins.
	ends   []int     // The column of the content just past the last rune each piece shows.
	shown  shownLine // The shown text of the line.
}

// wrapLine renders the content of vl, wraps it to the printer width, and
// returns the pieces with the columns of the content each one shows.
func (p *Printer) wrapLine(vl viewLine, gutterWidth int) wrappedLine {
	// The content wraps as the rendered line does, styles included, since
	// a style's transform may change the shown text. The wrap is
	// ANSI-aware and measures the shown cells.
	rendered, runs := p.renderRuns(vl)

	return mapRows(vl.width(), rendered, runs, p.wrapContent(rendered, gutterWidth))
}

// mapRows returns pieces, the rows the wrap cut from rendered, the
// rendered content of a line that holds contentLen columns, with the
// columns of the content each piece shows. Runs holds the runs that style
// rendered.
func mapRows(contentLen int, rendered string, runs []runSpan, pieces []string) wrappedLine {
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

	offsets, endOffsets := rowBounds(shownText, plain)
	starts := make([]int, len(offsets))
	ends := make([]int, len(endOffsets))

	for i, offset := range offsets {
		starts[i] = sourceCol(runs, shown, offset, contentLen)
		ends[i] = sourceCol(runs, shown, endOffsets[i], contentLen)
	}

	return wrappedLine{
		pieces: pieces,
		starts: starts,
		ends:   ends,
		shown: shownLine{
			text:       shownText,
			runs:       runs,
			starts:     offsets,
			ends:       endOffsets,
			contentLen: contentLen,
		},
	}
}

// shownOffset returns the offset in shown, the shown text of a line, at
// which col, a column of the content, begins. It inverts [sourceCol] with
// the same runs and contentLen, so it returns the first offset that
// sourceCol maps to col or past it. A column at or past the end of the
// content maps to the end of shown.
//
// In a run that shows as many runes as it covers, the offset moves rune
// for rune. The first column of any other run begins where the run
// begins, so it takes the runes a transform adds before the text of the
// run, such as an opening bracket. A later column of a run that shows
// its text verbatim begins at its rune within that text, which starts
// at the textAt field of [runSpan]. In a run whose transform rewrites
// the text, the shown runes match against the text of the run as
// sourceCol matches them.
func shownOffset(runs []runSpan, shown []rune, col, contentLen int) int {
	switch {
	case col <= 0 || len(runs) == 0:
		return 0
	case col >= contentLen:
		return len(shown)
	}

	// The last run that starts at or before col, which covers it. Search
	// finds the first run that starts past col, and the first run starts
	// at column 0.
	run := runs[sort.Search(len(runs), func(i int) bool { return runs[i].col > col })-1]
	if run.shownLen == run.cols {
		return run.shown + col - run.col
	}

	want := col - run.col
	if want == 0 {
		return run.shown
	}

	if run.textAt >= 0 {
		return run.shown + run.textAt + want
	}

	text := []rune(run.text)
	next := 0

	for i, r := range shown[run.shown : run.shown+run.shownLen] {
		if next >= want {
			return run.shown + i
		}

		next = skipDropped(text, nil, next, r)
		if next < len(text) && text[next] == r {
			next++
		}
	}

	return run.shown + run.shownLen
}

// sourceCol returns the column of the content at which offset in shown,
// the shown text of a line, falls. Runs holds the runs the line renders
// in, and contentLen is the number of columns of the content. Offset 0 is
// column 0, and an offset at or past the end of shown is contentLen.
//
// The run that shows the rune at offset decides the column, and the
// column stays within the run. A run that shows as many runes as it
// covers maps rune for rune, as a style that only colors its text or
// changes its case does. A run that shows its text verbatim among runes
// a transform adds maps rune for rune from the textAt field of
// [runSpan], where the text starts. An added rune before the text takes
// the first column of the run, and one after it takes the column past
// the run. In a run whose transform rewrites the text, the shown runes
// before offset match against the text of the run as [rowBounds]
// matches pieces.
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

	if run.textAt >= 0 {
		return run.col + min(max(offset-run.shown-run.textAt, 0), run.cols)
	}

	text := []rune(run.text)
	next := 0

	// The shown text comes before the wrap, so it keeps the runes the
	// wrapper drops after a space, and no rune counts as a tail.
	for _, r := range shown[run.shown:offset] {
		next = skipDropped(text, nil, next, r)
		if next < len(text) && text[next] == r {
			next++
		}
	}

	return run.col + min(next, run.cols)
}

// shownCol returns the column of the content that the rune at offset in
// shown, the shown text of a line, shows. It is the column [sourceCol]
// returns, kept inside the run that shows the rune. A rune a transform
// adds after the text of a run thus takes the last column of the run,
// where sourceCol gives it the column past the run.
func shownCol(runs []runSpan, shown []rune, offset, contentLen int) int {
	col := sourceCol(runs, shown, offset, contentLen)
	if len(runs) == 0 {
		return col
	}

	run := runs[max(0, sort.Search(len(runs), func(i int) bool { return runs[i].shown > offset })-1)]

	return max(run.col, min(col, run.col+run.cols-1))
}

// nbsp is the non-breaking space, the one Unicode space the wrapper keeps
// at a break.
const nbsp = '\u00a0'

// isBreakSpace reports whether r is a space the wrapper drops at a break:
// any Unicode space but [nbsp].
func isBreakSpace(r rune) bool {
	return unicode.IsSpace(r) && r != nbsp
}

// rowBounds returns the rune offset in text at which each piece of its
// wrapped form begins, and the offset just past the last rune of text
// each piece shows. It matches the runes of each piece against text
// in order, and skips the runes of text the wrapper drops. The wrapper
// drops every Unicode space but [nbsp] at a break and at the end of the
// text. From a grapheme cluster that starts with such a space outside
// ASCII, it keeps at most the space and drops the runes after it
// wherever the cluster falls. The first piece begins after any leading
// spaces the wrapper drops, which it does when the first word does not
// fit beside them. The caller escapes the text, so no tab reaches it.
// An excerpt shows a tab as its control picture, and the text of an
// error shows it as four spaces.
func rowBounds(text string, pieces []string) ([]int, []int) {
	runes := []rune(text)
	tails := spaceTails(text, len(runes))
	starts := make([]int, len(pieces))
	ends := make([]int, len(pieces))
	next := 0

	for i, piece := range pieces {
		for j, r := range piece {
			next = skipDropped(runes, tails, next, r)

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

		// The spaces the wrapper drops after the piece come after next,
		// so the piece ends before them.
		ends[i] = next
	}

	return starts, ends
}

// spaceTails marks the runes of text, which holds n runes, that follow the
// first rune of a grapheme cluster starting with a break space outside
// ASCII. The wrapper segments text into clusters wherever a rune outside
// ASCII starts one and takes each ASCII byte alone, so a mark after an
// ASCII space stays.
func spaceTails(text string, n int) []bool {
	tails := make([]bool, n)
	col := 0

	for off := 0; off < len(text); {
		r, size := utf8.DecodeRuneInString(text[off:])
		if r < utf8.RuneSelf || !isBreakSpace(r) {
			off += size
			col++

			continue
		}

		cluster, _ := ansi.FirstGraphemeCluster(text[off:], ansi.GraphemeWidth)
		count := utf8.RuneCountInString(cluster)

		for i := col + 1; i < col+count; i++ {
			tails[i] = true
		}

		off += len(cluster)
		col += count
	}

	return tails
}

// skipDropped returns the index of the first rune of runes at or after
// next that r can match. It skips every rune tails marks, which the
// wrapper always drops, and every break space that is not r. A nil tails
// marks no rune.
func skipDropped(runes []rune, tails []bool, next int, r rune) int {
	for next < len(runes) {
		tail := next < len(tails) && tails[next]
		if !tail && (runes[next] == r || !isBreakSpace(runes[next])) {
			break
		}

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

	return l.lines[k].rows
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

	// The line whose first row is the last start at or before row. The
	// last entry of starts is the row count rather than a line, so the
	// lookup leaves it out.
	return l.indices[rowIndex(l.starts[:n], row)]
}

// RowOf returns the row that holds column pos.Col of line pos.Line of the
// content: the content row the column wraps onto. The line's annotation
// rows sit beside the wrapped rows that hold their columns, so the rows
// before it include those of the earlier wrapped rows and those above
// it. A column in the spaces the wrapper dropped at a break belongs to
// the row before the break, and one past the end of the content to the
// last row. A column a clipped line leaves out belongs to the row that
// holds the "..." in its place. Returns -1 when the layout does not hold
// the line.
func (l Layout) RowOf(pos position.Position) int {
	k, ok := l.position(pos.Line)
	if !ok {
		return -1
	}

	ll := l.lines[k]

	return l.starts[k] + ll.offsets[rowIndex(ll.cols, ll.col(pos.Col))]
}

// CellOf returns the cell that column pos.Col of line pos.Line of the
// content takes on the row [Layout.RowOf] puts it on, counted from the
// start of that row with the gutter left out. A viewer that scrolls
// horizontally adds [Layout.GutterWidth] to find the cell of a column.
// The cell comes from the shown text, so it counts the cells a style's
// transform adds or removes before the column. The first column of a
// run takes the cell where the run begins, so it covers any rune a
// transform adds before the text of the run, such as an opening
// bracket. A column inside a grapheme cluster takes the cell where its
// cluster starts. A column past the end of the content takes one cell
// for every column past it, as [ColWidth] counts them. On a clipped
// line, the cell counts the row the line renders as, and a column the
// row leaves out takes the cell where the "..." in its place begins.
// Returns -1 when the layout does not hold the line.
func (l Layout) CellOf(pos position.Position) int {
	k, ok := l.position(pos.Line)
	if !ok {
		return -1
	}

	ll := l.lines[k]
	sl := ll.shown
	shown := []rune(sl.text)
	col := ll.col(max(0, pos.Col))

	offset := shownOffset(sl.runs, shown, col, sl.contentLen)

	from := 0
	if r := rowIndex(ll.cols, col); r < len(sl.starts) {
		from = min(sl.starts[r], offset)
	}

	cell := cells.NewRow(string(shown[from:])).Width(offset - from)
	past := max(0, col-sl.contentLen)

	return cell + min(past, math.MaxInt-cell)
}

// PositionAt returns the position in the content of the column that
// cell of row shows, and true. It inverts [Layout.LineAt],
// [Layout.RowOf], and [Layout.CellOf], so a viewer finds the content
// under a pointer with it. The cell counts from the start of the row with
// the gutter left out, as [Layout.CellOf] counts it, so a viewer
// subtracts [Layout.GutterWidth] from a cell of the rendered row.
//
// Any cell of a grapheme cluster gives the column where the cluster
// starts, so the second cell of a wide rune gives the column of that
// rune. A style's transform adds runes around the run it styles, such as
// brackets. A cell that shows one gives a column of that run: the first
// for a rune before the text of the run, and the last for a rune after
// it.
//
// PositionAt reports false where a row shows no content, which is on a
// row outside the layout, on an annotation row, and on a cell past the
// last rune the row shows. A negative cell lies in the gutter and reports
// false too. The line ending, the spaces the wrapper dropped at a break,
// and a rune of no width, such as a zero-width space, take no cell, so no
// cell gives their columns.
//
// On a line the view clips, a cell gives the column of the line it
// shows, whatever the row leaves out before it. A cell of a "..." shows
// no column of the line, so PositionAt reports false for it.
// [line.View.Windows] returns the columns the row keeps.
func (l Layout) PositionAt(row, cell int) (position.Position, bool) {
	if row < 0 || row >= l.Rows() || cell < 0 {
		return position.Position{}, false
	}

	k := rowIndex(l.starts[:len(l.lines)], row)
	ll := l.lines[k]

	// The content rows of the line sit at the offsets, and every other
	// row of the line belongs to an annotation.
	j, ok := slices.BinarySearch(ll.offsets, row-l.starts[k])
	if !ok {
		return position.Position{}, false
	}

	sl := ll.shown
	shown := []rune(sl.text)
	from, to := sl.starts[j], sl.ends[j]
	cellsRow := cells.NewRow(string(shown[from:to]))

	if cell >= cellsRow.Width(to-from) {
		return position.Position{}, false
	}

	// The first rune whose cluster ends past the cell belongs to the
	// cluster that covers it.
	at := cellsRow.Start(sort.Search(to-from, func(i int) bool { return cellsRow.Width(i+1) > cell }))

	col, ok := ll.lineCol(shownCol(sl.runs, shown, from+at, sl.contentLen))
	if !ok {
		return position.Position{}, false
	}

	return position.New(l.indices[k], col), true
}

// Width returns the width in cells of the widest row, gutter included,
// before the container style applies. A viewer that scrolls horizontally
// uses it to find how far the content reaches.
func (l Layout) Width() int {
	return l.width
}

// LineWidth returns the width in cells of the widest row of line i of the
// content, annotation rows and gutter included, before the container style
// applies. [Layout.Width] is the largest of these. A line the layout does
// not hold has width 0.
func (l Layout) LineWidth(i int) int {
	k, ok := l.position(i)
	if !ok {
		return 0
	}

	return l.lines[k].width
}

// GutterWidth returns the width in cells of the gutter on every row, so a
// viewer that scrolls horizontally subtracts it from [Layout.Width] to
// find the width of the content.
func (l Layout) GutterWidth() int {
	return l.gutterWidth
}
