package niceyaml

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/internal/escape"
	"go.jacobcolvin.com/niceyaml/line"
)

// plainRenderer renders the excerpt the %+v verb prints. It renders a view
// as text without styles: each line behind its number, carets on the row below
// under the columns its overlays cover, and its annotations beside the
// carets, so an excerpt reads in a log as it does in a terminal. Control
// characters render as their pictures, as the printer renders them.
type plainRenderer struct{}

// Print renders view as plain text.
func (plainRenderer) Print(view *line.View) string {
	width := 4
	for _, ln := range view.AllLines() {
		width = max(width, len(strconv.Itoa(ln.Number())))
	}

	blank := strings.Repeat(" ", width) + " | "

	var rows []string

	for i, ln := range view.AllLines() {
		anns := view.Annotations(i)

		// An annotation without content adds no row, as it adds none to a
		// marker row or to [line.View.String].
		if above := anns.Filter(line.Above).String(); above != "" {
			rows = append(rows, blank+escape.Control(above))
		}

		rows = append(rows, fmt.Sprintf("%*d | %s", width, ln.Number(), escape.Control(ln.Content())))

		if marker := plainMarker(ln, view.Overlays(i), anns.Filter(line.Below)); marker != "" {
			rows = append(rows, blank+marker)
		}
	}

	return strings.Join(rows, "\n")
}

// plainMarker returns the row below ln that marks its overlays and carries
// its annotations: a caret under every column an overlay covers within the
// line, a caret at the column of the annotations, and their contents after
// the last caret. A column is as many carets wide as the rune on it renders,
// so the carets stay under the runes they mark on a line holding wide or
// control characters, as [printer.AnnotationContext.ColWidth] keeps a
// styled marker under them. Returns "" when the line has neither.
func plainMarker(ln *line.Line, overlays line.Overlays, below line.Annotations) string {
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

	contents := make([]string, 0, len(below))

	for _, ann := range below {
		if ann.Content != "" {
			contents = append(contents, ann.Content)
		}
	}

	if len(contents) > 0 {
		mark(below.Col())
	}

	if len(marks) == 0 {
		return ""
	}

	var sb strings.Builder

	// The content row renders each rune at its display width, and a column
	// past the end of the content takes one cell, as ColWidth counts it.
	runes := []rune(ln.Content())

	for col, marked := range marks {
		cell := " "
		if marked {
			cell = "^"
		}

		cells := 1
		if col < len(runes) {
			cells = lipgloss.Width(escape.Control(string(runes[col])))
		}

		sb.WriteString(strings.Repeat(cell, cells))
	}

	if len(contents) > 0 {
		sb.WriteByte(' ')
		sb.WriteString(escape.Control(strings.Join(contents, "; ")))
	}

	return sb.String()
}
