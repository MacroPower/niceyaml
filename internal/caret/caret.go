// Package caret lays out the rows that mark the labels of a line below
// it, as a renderer draws the annotations below a line.
//
// Each run of columns that labels mark gets a row of its own. The row
// holds carets under the columns of the run, then the text of its labels
// after a space. Labels that mark the same columns share their row, with
// their texts joined by "; ". A label without text gets its caret run
// alone. The rows come in the order that their runs start, and runs that
// start at one column come shorter first.
//
// A column gets one caret for each cell that its rune renders in. The
// carets thus stay under the runes that they mark on a line that holds
// wide or control characters.
//
// On a line that wraps, a run gets a row beside each wrapped row that
// shows some of its columns. The text follows the carets beside the
// wrapped row that holds the start of the run.
package caret

import (
	"cmp"
	"slices"
	"sort"
	"strings"

	"go.jacobcolvin.com/niceyaml/internal/cells"
	"go.jacobcolvin.com/niceyaml/internal/escape"
	"go.jacobcolvin.com/niceyaml/position"
)

// Label is a run of columns of a line to mark, with the text that names
// it.
type Label struct {
	// Text is the text beside the carets, or the empty string for a label
	// without text.
	Text string
	// Cols are the columns of the line that the label marks. An empty span
	// marks the one column at its start.
	Cols position.Span
}

// Row is one row below a line that marks a run of columns.
type Row struct {
	// Marker holds the carets of the row, from the first caret to the last,
	// with a space after them when the row holds text.
	Marker string
	// Text holds the texts of the labels of the run joined with "; ", with
	// every control character as [escape.Control] draws it.
	Text string
	// Col is the column of the line that the first caret sits under.
	Col int
}

// Rows returns the rows that mark labels below content, the text of a line
// without its line ending, in the order that a renderer draws them.
//
// Wrapped holds the columns of content that each row of the line shows
// when the line wraps, in order, each without the spaces that the wrap
// drops. A nil wrapped stands for one row that shows the whole line. The
// row that holds the end of content also marks the columns past its end.
//
// A label marks the columns of its span that content holds. It marks the
// column at its start when its span does not hold any of those columns,
// as with an empty span or one past the end of content. That column can
// lie past the end. A start before column zero counts as zero. Callers
// must bound the start, because a row pads out to it.
func Rows(content string, labels []Label, wrapped []position.Span) []Row {
	if len(labels) == 0 {
		return nil
	}

	l := newLayout(content, wrapped)

	var rows []Row

	for _, r := range l.runs(labels) {
		rows = append(rows, l.rows(r)...)
	}

	return rows
}

// run is the columns that one or more labels mark, with the text of each
// of those labels that has text.
type run struct {
	texts []string
	cols  position.Span
}

// layout is the line that [Rows] marks, with the columns that each of its
// wrapped rows shows.
type layout struct {
	row     cells.Row
	wrapped []position.Span
	// The number of columns of the content.
	width int
}

// newLayout creates a new [layout] for content, wrapped as [Rows] takes
// it.
func newLayout(content string, wrapped []position.Span) layout {
	width := len([]rune(content))

	if len(wrapped) == 0 {
		wrapped = []position.Span{position.NewSpan(0, width)}
	}

	return layout{row: cells.NewRow(content), wrapped: wrapped, width: width}
}

// runs groups labels by the columns that they mark, as [layout.marked]
// finds them, in the order that the runs start. Runs that start at the
// same column come shorter first, and labels of one run keep the order
// that they come in.
func (l layout) runs(labels []Label) []run {
	var out []run

	index := make(map[position.Span]int)

	for _, lb := range labels {
		cols := l.marked(lb.Cols)

		i, ok := index[cols]
		if !ok {
			i = len(out)
			index[cols] = i

			out = append(out, run{cols: cols})
		}

		if lb.Text != "" {
			out[i].texts = append(out[i].texts, lb.Text)
		}
	}

	slices.SortStableFunc(out, func(a, b run) int {
		return cmp.Or(cmp.Compare(a.cols.Start, b.cols.Start), cmp.Compare(a.cols.End, b.cols.End))
	})

	return out
}

// marked returns the columns that a label with the span cols marks, as
// [Rows] describes them. The result is never empty, and it starts at
// column zero or later.
func (l layout) marked(cols position.Span) position.Span {
	start := max(0, cols.Start)
	end := min(cols.End, l.width)

	if end <= start {
		end = start + 1
	}

	return position.NewSpan(start, end)
}

// piece is a row of carets that [layout.cut] finds, with the index of the
// wrapped row that it sits beside.
type piece struct {
	row Row
	at  int
}

// rows returns the rows that mark r. Each wrapped row that shows some of
// the columns of r gets a caret row. The text of r follows the carets
// beside the wrapped row that holds its start. That wrapped row might not
// show any of the columns of r, as when the start falls on a space that
// the wrap drops. The text then follows a single caret at the start, on a
// row of its own. A run that has neither text nor a caret row gets that
// single caret alone.
func (l layout) rows(r run) []Row {
	start := r.cols.Start

	marks := make([]bool, r.cols.End)
	for col := start; col < r.cols.End; col++ {
		marks[col] = true
	}

	pieces := l.cut(l.render(marks))
	text := escape.Control(strings.Join(r.texts, "; "))
	at := l.rowOf(start)

	i := slices.IndexFunc(pieces, func(p piece) bool { return p.at >= at })

	switch {
	case i >= 0 && pieces[i].at == at:
		if text != "" {
			pieces[i].row.Marker += " "
			pieces[i].row.Text = text
		}

	case text != "" || len(pieces) == 0:
		single := piece{row: l.single(start), at: at}
		if text != "" {
			single.row.Marker += " "
			single.row.Text = text
		}

		if i < 0 {
			i = len(pieces)
		}

		pieces = slices.Insert(pieces, i, single)
	}

	out := make([]Row, 0, len(pieces))
	for _, p := range pieces {
		out = append(out, p.row)
	}

	return out
}

// rowOf returns the index of the wrapped row that holds col. That row is
// the last one that starts at or before col, or the first when every row
// starts after col.
func (l layout) rowOf(col int) int {
	// Search finds the first row that starts past col. It compares with >
	// rather than searching for col+1, which overflows at [math.MaxInt].
	return max(0, sort.Search(len(l.wrapped), func(i int) bool { return l.wrapped[i].Start > col })-1)
}

// single returns the row that marks col alone, with one caret for each
// cell that the rune there renders in. A column past the end of the content
// gets one caret.
func (l layout) single(col int) Row {
	marks := make([]bool, col+1)
	marks[col] = true

	return Row{Col: col, Marker: strings.Trim(l.render(marks), " ")}
}

// cut returns a caret row for each wrapped row of the line that holds a
// caret of carets, in order. Carets is the caret row of the whole line,
// and each result holds the carets of its own wrapped row. A wrapped row
// that holds the end of the content runs to the end of carets, so it
// shows the marks past the end.
func (l layout) cut(carets string) []piece {
	var pieces []piece

	for r, w := range l.wrapped {
		lo := min(max(0, w.Start), l.width)
		hi := min(max(lo, w.End), l.width)

		// A row can start inside a grapheme cluster when the wrap drops
		// the space that begins the cluster and keeps the rest, such as a
		// combining accent. The caret row draws the whole cluster at the
		// space, so the row takes its cells from the first cluster that
		// starts on it.
		lo = l.row.Next(lo)
		from, to := l.row.Width(lo), l.row.Width(l.row.Next(hi))

		if hi == l.width {
			to = len(carets)
		}

		cut := carets[min(from, len(carets)):min(max(from, to), len(carets))]

		i := strings.IndexByte(cut, '^')
		if i < 0 {
			continue
		}

		// A renderer pads the row to Col, so the carets go without the
		// padding before them.
		pieces = append(pieces, piece{
			row: Row{
				Col:    l.row.Col(lo, from+i),
				Marker: strings.TrimRight(cut[i:], " "),
			},
			at: r,
		})
	}

	return pieces
}

// render renders marks as a row under the content: a caret under every
// marked column and a space under every other, one byte per cell. The
// content row renders each grapheme cluster at its display width. The
// first column of a cluster is therefore as many cells wide as the cluster
// renders. Every other column of it does not take a cell, and a column
// past the end of the content takes one cell.
//
// A mark on any column of a cluster lands under the whole cluster. A
// cluster that does not render a cell, such as a zero-width space, does
// not have a cell to put a caret under. Its mark moves to the next column
// that takes a cell, which shows what follows the cluster.
func (l layout) render(marks []bool) string {
	marks = slices.Clone(marks)
	for col, marked := range marks {
		if marked {
			marks[l.row.Start(col)] = true
		}
	}

	// The loop reads the length of marks on every pass, so it still
	// renders a mark it moves past the end.
	for col := 0; col < len(marks); col++ {
		if !marks[col] || l.row.Start(col) != col || l.row.Cells(col) > 0 {
			continue
		}

		next := col + 1
		for l.row.Cells(next) == 0 {
			next++
		}

		if next >= len(marks) {
			marks = append(marks, make([]bool, next+1-len(marks))...)
		}

		marks[next] = true
	}

	var sb strings.Builder

	for col, marked := range marks {
		cell := " "
		if marked {
			cell = "^"
		}

		sb.WriteString(strings.Repeat(cell, l.row.Cells(col)))
	}

	return sb.String()
}
