package line

import (
	"go.jacobcolvin.com/niceyaml/internal/clip"
	"go.jacobcolvin.com/niceyaml/position"
)

// ClipMap maps a column of a line to the column of the row that shows
// it, and back. A row of a line that [View.Clip] clips holds the windows
// [View.Windows] returns, with "..." in place of each run of columns the
// windows leave out. Its columns therefore differ from those of the line.
// A renderer that draws a caret under a column of the line maps it with
// [ClipMap.Col]. A viewer that finds the content under a pointer maps
// the column of the row back with [ClipMap.LineCol]. The printer's
// layout maps both ways this way.
//
// Each method treats a negative column as column 0. The columns count
// runes, as every column of a [Line] does.
//
// Create instances with [NewClipMap] or [View.ClipMap]. The zero value is
// the map of an empty line that shows whole.
type ClipMap struct {
	// The layout of the row, or nil for a line that shows whole.
	m *clip.Map
	// The number of columns of the line.
	width int
}

// NewClipMap creates a new [ClipMap] for a line of width columns whose
// row keeps windows, the runs of its columns to show, in ascending order
// and as [View.Windows] returns them. Without windows, as View.Windows
// returns for a line the View shows whole, the row is the line. The map
// then returns each column as it is.
func NewClipMap(width int, windows position.Spans) ClipMap {
	m := ClipMap{width: max(0, width)}
	if len(windows) > 0 {
		m.m = clip.New(m.width, windows)
	}

	return m
}

// ClipMap returns the [ClipMap] of line i, built from its width and the
// windows [View.Windows] returns for it. It panics if i is outside the
// content.
func (v *View) ClipMap(i int) ClipMap {
	return NewClipMap(v.lines.lines[i].Width(), v.Windows(i))
}

// Col returns the column of the row that shows col, a column of the line.
// A column that the row leaves out maps to the start of the "..." that
// stands for it. A column past the end of the line maps as far past the
// end of the row.
func (m ClipMap) Col(col int) int {
	if m.m == nil {
		return max(0, col)
	}

	return m.m.Col(col)
}

// LineCol returns the column of the line that col, a column of the row,
// shows, and true. It inverts [ClipMap.Col] on the columns that the row
// keeps. It reports false for a column past the end of the row and for a
// column of a "...". A "..." does not show a column of the line.
func (m ClipMap) LineCol(col int) (int, bool) {
	if m.m == nil {
		col = max(0, col)
		if col >= m.width {
			return 0, false
		}

		return col, true
	}

	return m.m.LineCol(col)
}
