// Package clip lays out the row of a line that shows some of its columns.
//
// A view that clips a long line keeps a few windows of its columns and
// leaves out the rest. The row that renders the line holds the text of
// each window in order, with [Ellipsis] in place of each run of columns
// left out. A [Map] holds that layout. It cuts the text of the row from
// the text of the line. It also maps a column of the line to the column
// of the row that shows it, so a renderer draws a caret or a style under
// the rune it marks.
//
//	m := clip.New(26, []position.Span{position.NewSpan(0, 4), position.NewSpan(20, 26)})
//	m.Content("abcdefghijklmnopqrstuvwxyz") // "abcd...uvwxyz"
//	m.Col(22)                               // 9, the column of "w" in the row
//
// The windows count runes, as every column of a line does, so a caller
// that keeps grapheme clusters whole picks windows that end between them.
package clip

import (
	"iter"
	"math"
	"sort"
	"strings"

	"go.jacobcolvin.com/niceyaml/position"
)

// Ellipsis is the text a row shows in place of each run of columns it
// leaves out.
const Ellipsis = "..."

// EllipsisCols is the number of columns [Ellipsis] takes in a row. A cut
// of that many columns or fewer saves nothing, so a caller keeps them.
const EllipsisCols = len(Ellipsis)

// Piece is one part of a row: a run of columns the row keeps, or the
// [Ellipsis] that stands for a run it leaves out.
type Piece struct {
	// Cols are the columns of the line the piece stands for.
	Cols position.Span
	// At is the column of the row the piece starts at.
	At int
	// Gap reports whether the piece is an ellipsis.
	Gap bool
}

// cols returns the number of columns the piece takes in the row.
func (p Piece) cols() int {
	if p.Gap {
		return EllipsisCols
	}

	return p.Cols.Len()
}

// Map is the layout of the row of one line that keeps some of its
// columns. Its methods treat a negative column as column 0.
//
// Create instances with [New].
type Map struct {
	// The pieces of the row in order, which cover every column of the
	// line once.
	pieces []Piece
	// The number of columns of the line.
	width int
	// The number of columns of the row.
	shown int
}

// New creates a new [*Map] for a line of width columns whose row keeps
// windows, the runs of columns to show, in ascending order. It clamps
// each window to the line and to the end of the window before it, and
// drops one that holds no column there.
func New(width int, windows []position.Span) *Map {
	m := &Map{width: max(0, width)}

	add := func(cols position.Span, gap bool) {
		p := Piece{Cols: cols, At: m.shown, Gap: gap}

		m.pieces = append(m.pieces, p)
		m.shown += p.cols()
	}

	end := 0

	for _, w := range position.Spans(windows).Clamp(0, m.width) {
		w.Start = max(w.Start, end)
		if w.Start >= w.End {
			continue
		}

		if w.Start > end {
			add(position.NewSpan(end, w.Start), true)
		}

		add(w, false)

		end = w.End
	}

	if end < m.width {
		add(position.NewSpan(end, m.width), true)
	}

	return m
}

// Pieces returns an iterator over the pieces of the row, in order.
func (m *Map) Pieces() iter.Seq[Piece] {
	return func(yield func(Piece) bool) {
		for _, p := range m.pieces {
			if !yield(p) {
				return
			}
		}
	}
}

// Width returns the number of columns the row takes.
func (m *Map) Width() int {
	return m.shown
}

// Col returns the column of the row that shows col, a column of the
// line. A column the row leaves out maps to the start of the ellipsis
// that stands for it. A column past the end of the line maps as far past
// the end of the row, and the result saturates at [math.MaxInt].
func (m *Map) Col(col int) int {
	col = max(0, col)
	if col >= m.width {
		return m.shown + min(col-m.width, math.MaxInt-m.shown)
	}

	// The last piece that starts at or before col, which holds it. Search
	// finds the first piece that starts past col, and the first piece
	// starts at column 0.
	p := m.pieces[sort.Search(len(m.pieces), func(i int) bool { return m.pieces[i].Cols.Start > col })-1]
	if p.Gap {
		return p.At
	}

	return p.At + col - p.Cols.Start
}

// Spans returns the columns of the row that show cols, a run of columns
// of the line: one span for each window that holds some of them, in
// order. The columns the row leaves out, and those past the end of the
// line, map to nothing, so a run that an ellipsis cuts in two comes back
// as two spans.
func (m *Map) Spans(cols position.Span) position.Spans {
	var out position.Spans

	for _, p := range m.pieces {
		lo, hi := max(cols.Start, p.Cols.Start), min(cols.End, p.Cols.End)
		if p.Gap || lo >= hi {
			continue
		}

		out = append(out, position.NewSpan(p.At+lo-p.Cols.Start, p.At+hi-p.Cols.Start))
	}

	return out
}

// Content returns the text of the row for content, the text of the line
// without its line ending: the runes of each window, with [Ellipsis] in
// place of each run of columns the row leaves out.
func (m *Map) Content(content string) string {
	runes := []rune(content)

	var sb strings.Builder

	for _, p := range m.pieces {
		if p.Gap {
			sb.WriteString(Ellipsis)

			continue
		}

		sb.WriteString(string(runes[min(p.Cols.Start, len(runes)):min(p.Cols.End, len(runes))]))
	}

	return sb.String()
}
