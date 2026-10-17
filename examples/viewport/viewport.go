package main

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	_ "embed"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/finder"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/style/kind"
	"go.jacobcolvin.com/niceyaml/style/theme"
)

// width and height size the window of the viewer. The width counts cells,
// and the height counts rows.
const (
	width  = 40
	height = 7
)

//go:embed demo.yaml
var example string

func main() {
	source := niceyaml.NewSourceFromString(example)

	// The Charm theme paints a surface behind every token.
	p := printer.New(printer.WithStyles(theme.Charm))

	doc, err := source.Document()
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	// A finder ignores case and diacritics, so "raksmorgas" finds the
	// lunch item that the menu spells "Räksmörgås".
	idx := finder.New().Load(source.Lines())
	dish := idx.Find("raksmorgas")[0]
	lemon := idx.Find("lemon")[0]

	// The annotation above the dish takes a row of its own, and that row
	// does not show any content of the source.
	view := source.View()
	view.BlendOverlay(kind.GenericHighlight, dish, lemon)
	view.Annotate(dish.Start.Line, line.Annotation{
		Content:   "Dagens lunch",
		Kind:      kind.TextOK,
		Placement: line.Above,
		Cols:      position.NewSpan(dish.Start.Col, dish.End.Col),
	})

	fmt.Println("\nCount the rows of a wrapped view:")

	// A layout wraps and styles each line as Print does, without
	// assembling the output. A long line takes a row for each piece it
	// wraps into, and an annotation takes a row of its own. Rows count
	// from 0. The output numbers lines and columns from 1, as the gutter
	// numbers lines.
	wrapped := p.With(printer.WithWrapWidth(width), printer.WithContainerWidth(width))
	layout := wrapped.Layout(view)
	fmt.Printf("%d lines take %d rows at a width of %d cells.\n", layout.Count(), layout.Rows(), width)

	for i := range view.All() {
		if n := layout.LineRows(i); n > 1 {
			start := layout.LineStart(i)
			fmt.Printf("Line %d takes rows %d to %d.\n", i+1, start, start+n-1)
		}
	}

	fmt.Println("\nScroll to a search match:")

	// RowOf gives the row a position lands on, so the viewer centers the
	// match and keeps the window inside the rows the view takes.
	top := clamp(layout.RowOf(dish.Start)-height/2, layout.Rows()-height)

	// These printers draw no frame around the content, so row n of the
	// layout is row n of the output of Print. The doc of Layout maps the
	// rows under a frame. The container width pads every row to the window
	// in kind.Text, so the surface of the theme fills the window. A view
	// that takes fewer rows than the window fills only the top of it.
	rows := strings.Split(wrapped.Print(view), "\n")
	frame(rows[top:min(top+height, len(rows))])

	// LineAt maps a row back to its line, as a status bar needs. A row of
	// a wrapped piece or an annotation belongs to the line it sits beside.
	first, last := layout.LineAt(top), layout.LineAt(top+height-1)
	fmt.Printf("Lines %d to %d of %d.\n", first+1, last+1, layout.Count())

	fmt.Println("\nScroll sideways to a search match:")

	// Without wrapping, the content of each line takes one row, and long
	// lines run past the window. An annotation still takes a row of its
	// own, so this window opens on the row of the annotation above the dish.
	//
	// The viewer keeps the gutter in place and scrolls the content beside it.
	// CellOf counts cells from the end of the gutter. The offset that puts
	// the end of the match at the right edge is that cell less the room
	// beside the gutter.
	layout = p.Layout(view)
	gutter := layout.GutterWidth()
	room := width - gutter
	left := clamp(layout.CellOf(lemon.End)-room, layout.Width()-gutter-room)
	top = clamp(layout.RowOf(lemon.Start)-height/2, layout.Rows()-height)

	from, to := gutter+left, gutter+left+room
	rows = strings.Split(p.Print(view), "\n")
	rows = rows[top:min(top+height, len(rows))]

	for i, row := range rows {
		// Cut keeps every escape sequence of the row, so the text in the
		// window keeps the color of a token that starts before it. It keeps
		// every cell in place too, so each rune of the window takes the cell
		// that the layout gives it. Pad fills the rest of a short row with
		// the surface of the theme, as the container width does.
		rows[i] = printer.Cut(row, 0, gutter) + p.Pad(printer.Cut(row, from, to), room)
	}

	frame(rows)

	fmt.Println("\nMap a click in the window to a path:")

	// A click names a row of the window and a cell inside its frame,
	// both counted from 0. The left edge of this window splits the cucumber
	// emoji in the note of Köttbullar. Cut shows the part of the emoji
	// inside the window as a space in its style, so cell 6 of row 6 gives
	// the emoji. The lemon match and the width of the window set that edge,
	// so a change to either moves it off the emoji.
	clicks := []struct{ row, cell int }{{3, 20}, {6, 8}, {6, 6}, {0, 20}, {2, 20}, {3, 2}}
	for _, c := range clicks {
		at := fmt.Sprintf("Row %d, cell %2d:", c.row, c.cell)
		row := top + c.row

		// The gutter stays in place when the content scrolls, so the viewer
		// tests for a gutter cell before it adds the scroll offset, as the
		// doc of PositionAt describes. LineAt still names the line that a
		// gutter cell sits beside.
		if c.cell < gutter {
			fmt.Println(at, "the gutter of line", layout.LineAt(row)+1)

			continue
		}

		// PositionAt counts cells from the end of the gutter, as CellOf
		// does. It reports false for a cell of an annotation row and for
		// one past the end of its row.
		cell := c.cell - gutter
		pos, ok := layout.PositionAt(row, left+cell)
		if !ok {
			fmt.Println(at, "no content beside line", layout.LineAt(row)+1)

			continue
		}

		// A column counts runes of the line, and PathAt names the node
		// that holds it. PathAt reports false for a comment or a "---"
		// header, because neither belongs to a node.
		r := []rune(view.Line(pos.Line).Content())[pos.Col]
		path, ok := doc.PathAt(pos)
		if !ok {
			fmt.Printf("%s %q on line %d, outside any node\n", at, r, pos.Line+1)

			continue
		}

		fmt.Printf("%s %q on line %d, column %d, in %s\n", at, r, pos.Line+1, pos.Col+1, path)
	}
}

// clamp keeps a scroll offset between 0 and limit, inclusive. A window
// larger than the content has a negative limit, so its offset is 0.
func clamp(offset, limit int) int {
	return max(0, min(offset, limit))
}

// frame prints rows inside a border, as a viewer draws its window.
func frame(rows []string) {
	border := lipgloss.NewStyle().Border(lipgloss.RoundedBorder())
	lipgloss.Println(border.Render(strings.Join(rows, "\n")))
}
