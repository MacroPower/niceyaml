package printer_test

import (
	"strconv"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

// unevenPanes returns two panes whose first line takes six zipped rows.
// The left pane has two annotation rows above that line. With panes 14
// cells wide, the right pane wraps the line onto four rows.
func unevenPanes() (*line.View, *line.View) {
	left := niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "b: 2")).View()
	left.AnnotateLine(0,
		line.Annotation{Content: "one", Kind: kind.TextError, Placement: line.Above},
		line.Annotation{Content: "two", Kind: kind.TextWarn, Placement: line.Above},
	)

	right := niceyaml.NewSourceFromString(stringtest.LinesLF("a: one two three four", "b: 2")).View()

	return left, right
}

func TestPrinter_RenderSideBySide(t *testing.T) {
	t.Parallel()

	twoLines := stringtest.LinesLF("a: 1", "b: 2")
	view := func(input string) *line.View { return niceyaml.NewSourceFromString(input).View() }
	border := lipgloss.NewStyle().Border(lipgloss.NormalBorder())

	// Each pane of the uneven panes wraps to 14 cells: (31-3)/2 without a
	// frame, and (33-2-3)/2 inside the border.
	unevenRows := []string{
		"      one      │       ",
		"      two      │       ",
		"   1  a: 1     │    1  a: one",
		"               │    -  two",
		"               │    -  three",
		"               │    -  four",
		"   2  b: 2     │    2  b: 2",
	}
	unevenLeft := []string{
		"      one",
		"      two",
		"   1  a: 1",
		"      ",
		"      ",
		"      ",
		"   2  b: 2",
	}
	unevenRight := []string{
		"      ",
		"      ",
		"   1  a: one",
		"   -  two",
		"   -  three",
		"   -  four",
		"   2  b: 2",
	}

	tcs := map[string]struct {
		views          func() (*line.View, *line.View)
		opts           []printer.Option
		want           []string
		wantLeft       []string
		wantRight      []string
		wantRightStart int
	}{
		"equal panes": {
			views:          func() (*line.View, *line.View) { return view(twoLines), view(twoLines) },
			want:           []string{"   1  a: 1 │    1  a: 1", "   2  b: 2 │    2  b: 2"},
			wantLeft:       []string{"   1  a: 1", "   2  b: 2"},
			wantRight:      []string{"   1  a: 1", "   2  b: 2"},
			wantRightStart: 13,
		},
		// Each pane wraps to (38-3-3)/2 = 16 cells, which fit the gutter
		// and the 10 cells of the line.
		"box stays out of the panes": {
			views: func() (*line.View, *line.View) { return view("a: 1234567"), view("a: 1234567") },
			opts: []printer.Option{
				printer.WithWrapWidth(38),
				printer.WithBox(true),
				printer.WithContainerStyle(border),
			},
			want:           []string{"   1  a: 1234567 │    1  a: 1234567"},
			wantLeft:       []string{"   1  a: 1234567"},
			wantRight:      []string{"   1  a: 1234567"},
			wantRightStart: 19,
		},
		"filler rows keep the panes as long as each other": {
			views:          unevenPanes,
			opts:           []printer.Option{printer.WithWrapWidth(31)},
			want:           unevenRows,
			wantLeft:       unevenLeft,
			wantRight:      unevenRight,
			wantRightStart: 17,
		},
		"container frame stays out of the rows": {
			views:          unevenPanes,
			opts:           []printer.Option{printer.WithWrapWidth(33), printer.WithContainerStyle(border)},
			want:           unevenRows,
			wantLeft:       unevenLeft,
			wantRight:      unevenRight,
			wantRightStart: 17,
		},
		"container width does not pad the rows": {
			views:          unevenPanes,
			opts:           []printer.Option{printer.WithWrapWidth(31), printer.WithContainerWidth(60)},
			want:           unevenRows,
			wantLeft:       unevenLeft,
			wantRight:      unevenRight,
			wantRightStart: 17,
		},
		"line that only one pane holds": {
			views: func() (*line.View, *line.View) {
				return view(twoLines).Slice(position.NewSpan(0, 1)), view(twoLines)
			},
			want:           []string{"   1  a: 1 │    1  a: 1", "           │    2  b: 2"},
			wantLeft:       []string{"   1  a: 1", "      "},
			wantRight:      []string{"   1  a: 1", "   2  b: 2"},
			wantRightStart: 13,
		},
		"empty views have no rows": {
			views:          func() (*line.View, *line.View) { return view(""), view("") },
			opts:           []printer.Option{printer.WithContainerStyle(border)},
			want:           []string{},
			wantRightStart: 3,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := testPrinterWithGutter(printer.DefaultGutter()).With(tc.opts...)
			left, right := tc.views()
			got := p.RenderSideBySide(left, right)

			assert.Equal(t, tc.want, got.Rows())

			gotLeft, gotRight := got.Panes()
			assert.Equal(t, tc.wantLeft, gotLeft)
			assert.Equal(t, tc.wantRight, gotRight)

			assert.Equal(t, 0, got.PaneStart(false))
			assert.Equal(t, tc.wantRightStart, got.PaneStart(true))

			for _, row := range got.Rows() {
				before, _, ok := strings.Cut(row, "│")
				require.True(t, ok, "row %q", row)
				assert.Equal(t, tc.wantRightStart, lipgloss.Width(before)+2, "row %q", row)
			}

			framed := got.String()
			assert.Equal(t, p.PrintSideBySide(left, right), framed)

			// String pads and frames a copy of the rows, so the rows, the
			// panes, and a second String match the first.
			assert.Equal(t, tc.want, got.Rows())

			gotLeft, gotRight = got.Panes()
			assert.Equal(t, tc.wantLeft, gotLeft)
			assert.Equal(t, tc.wantRight, gotRight)

			assert.Equal(t, framed, got.String())
		})
	}
}

func TestPrinter_RenderSideBySide_Layouts(t *testing.T) {
	t.Parallel()

	left := niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "b: 2", "c: 3")).View()
	left.AnnotateLine(0,
		line.Annotation{Content: "one", Kind: kind.TextError, Placement: line.Above},
		line.Annotation{Content: "two", Kind: kind.TextWarn, Placement: line.Above},
	)

	right := niceyaml.NewSourceFromString(stringtest.LinesLF("a: one two three four", "b: 2", "c: 3")).View()
	right.AnnotateLine(2, line.Annotation{Content: "a note", Placement: line.Above})

	p := testPrinterWithGutter(printer.DefaultGutter()).With(
		printer.WithWrapWidth(33),
		printer.WithContainerStyle(lipgloss.NewStyle().Border(lipgloss.NormalBorder())),
		printer.WithContainerWidth(33),
	)

	// Each pane renders through a copy of the printer without the
	// container, with the gutter of the larger line number, wrapped to
	// half of what the frame and the separator leave.
	pane := p.With(
		printer.WithContainerStyle(lipgloss.NewStyle()),
		printer.WithContainerWidth(0),
		printer.WithMaxNumber(max(p.MaxNumber(left), p.MaxNumber(right))),
		printer.WithWrapWidth((33-2-3)/2),
	)

	got := p.RenderSideBySide(left, right)
	leftLayout, rightLayout := got.Layouts()

	assert.Equal(t, pane.Layout(left), leftLayout)
	assert.Equal(t, pane.Layout(right), rightLayout)

	// The first content row of each line sits below the larger block of
	// rows above it, and it shows the line number in both panes.
	rows := got.Rows()

	for i := range 3 {
		row := got.LineStart(i) + max(leftLayout.LineLead(i), rightLayout.LineLead(i))
		require.Less(t, row, len(rows))

		leftHalf, rightHalf, ok := strings.Cut(rows[row], " │ ")
		require.True(t, ok, "row %q", rows[row])

		number := " " + strconv.Itoa(i+1) + "  "
		assert.Contains(t, leftHalf, number, "line %d", i)
		assert.Contains(t, rightHalf, number, "line %d", i)
	}
}

func TestSideBySide_Lines(t *testing.T) {
	t.Parallel()

	left, right := unevenPanes()
	got := testPrinterWithGutter(printer.DefaultGutter()).
		With(printer.WithWrapWidth(31)).
		RenderSideBySide(left, right.Slice(position.NewSpan(0, 2)))

	starts := map[string]struct {
		line      int
		wantStart int
		wantRows  int
	}{
		"line that takes more rows than in either pane": {line: 0, wantStart: 0, wantRows: 6},
		"line after it":           {line: 1, wantStart: 6, wantRows: 1},
		"line neither pane holds": {line: 2, wantStart: -1, wantRows: 0},
		"negative line":           {line: -1, wantStart: -1, wantRows: 0},
	}

	for name, tc := range starts {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.wantStart, got.LineStart(tc.line))
			assert.Equal(t, tc.wantRows, got.LineRows(tc.line))
		})
	}

	rows := map[string]struct {
		row  int
		want int
	}{
		"first row":              {row: 0, want: 0},
		"filler row of one pane": {row: 4, want: 0},
		"last row of a line":     {row: 5, want: 0},
		"first row of a line":    {row: 6, want: 1},
		"row before the first":   {row: -3, want: 0},
		"row past the last":      {row: 99, want: 1},
	}

	for name, tc := range rows {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, got.LineAt(tc.row))
		})
	}
}

func TestSideBySide_PaneRow(t *testing.T) {
	t.Parallel()

	left, right := unevenPanes()
	got := testPrinterWithGutter(printer.DefaultGutter()).
		With(printer.WithWrapWidth(31)).
		RenderSideBySide(left, right)

	tcs := map[string]struct {
		right  bool
		row    int
		want   int
		wantOK bool
	}{
		"left annotation row":            {row: 0, want: 0, wantOK: true},
		"left content row":               {row: 2, want: 2, wantOK: true},
		"left filler below the line":     {row: 3},
		"left last filler":               {row: 5},
		"left next line":                 {row: 6, want: 3, wantOK: true},
		"left row before the first":      {row: -1},
		"left row past the last":         {row: 7},
		"right filler above the line":    {right: true, row: 0},
		"right last filler above":        {right: true, row: 1},
		"right content row":              {right: true, row: 2, want: 0, wantOK: true},
		"right last wrapped row":         {right: true, row: 5, want: 3, wantOK: true},
		"right next line":                {right: true, row: 6, want: 4, wantOK: true},
		"right row past the last":        {right: true, row: 7},
		"right row far before the first": {right: true, row: -9},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			row, ok := got.PaneRow(tc.right, tc.row)

			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.want, row)
		})
	}
}

func TestSideBySide_PaneRow_MissingLine(t *testing.T) {
	t.Parallel()

	// The left pane does not hold the second line, so its row beside that
	// line is a filler row.
	source := niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "b: 2", "c: 3"))
	left := source.View().Slice(position.NewSpan(0, 1), position.NewSpan(2, 3))
	got := testPrinterWithGutter(printer.DefaultGutter()).RenderSideBySide(left, source.View())

	_, ok := got.PaneRow(false, 1)
	assert.False(t, ok)

	row, ok := got.PaneRow(false, 2)
	assert.True(t, ok)
	assert.Equal(t, 1, row)

	row, ok = got.PaneRow(true, 1)
	assert.True(t, ok)
	assert.Equal(t, 1, row)
}

func TestSideBySide_PositionAt(t *testing.T) {
	t.Parallel()

	// A viewer maps a cell of a zipped row to a position through the row
	// and the start of its pane.
	left, right := unevenPanes()
	got := testPrinterWithGutter(printer.DefaultGutter()).
		With(printer.WithWrapWidth(31)).
		RenderSideBySide(left, right)
	leftLayout, rightLayout := got.Layouts()

	tcs := map[string]struct {
		row    int
		cell   int
		want   position.Position
		wantOK bool
	}{
		// Row 4 shows "three" in the right pane, after the 17 cells of
		// the left pane and the separator and the 6 cells of the gutter.
		"wrapped row of the right pane": {row: 4, cell: 23, want: position.New(0, 11), wantOK: true},
		"content of the left pane":      {row: 2, cell: 9, want: position.New(0, 3), wantOK: true},
		"filler row of the left pane":   {row: 4, cell: 7},
		"filler row of the right pane":  {row: 0, cell: 23},
		"annotation row":                {row: 0, cell: 7},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			onRight := tc.cell >= got.PaneStart(true)

			layout := leftLayout
			if onRight {
				layout = rightLayout
			}

			row, ok := got.PaneRow(onRight, tc.row)
			if !ok {
				assert.False(t, tc.wantOK)

				return
			}

			pos, ok := layout.PositionAt(row, tc.cell-got.PaneStart(onRight)-layout.GutterWidth())

			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.want, pos)
		})
	}
}

func TestSideBySide_Rows(t *testing.T) {
	t.Parallel()

	t.Run("returns copies", func(t *testing.T) {
		t.Parallel()

		view := func() *line.View { return niceyaml.NewSourceFromString("a: 1\n").View() }
		got := testPrinterWithGutter(printer.DefaultGutter()).RenderSideBySide(view(), view())

		rows := got.Rows()
		rows[0] = "changed"

		leftRows, rightRows := got.Panes()
		leftRows[0], rightRows[0] = "changed", "changed"

		gotLeft, gotRight := got.Panes()

		assert.Equal(t, []string{"   1  a: 1 │    1  a: 1"}, got.Rows())
		assert.Equal(t, []string{"   1  a: 1"}, gotLeft)
		assert.Equal(t, []string{"   1  a: 1"}, gotRight)
		assert.Equal(t, "   1  a: 1 │    1  a: 1", got.String())
	})

	t.Run("zero value", func(t *testing.T) {
		t.Parallel()

		var got printer.SideBySide

		gotLeft, gotRight := got.Panes()
		_, ok := got.PaneRow(false, 0)

		assert.Empty(t, got.Rows())
		assert.Empty(t, gotLeft)
		assert.Empty(t, gotRight)
		assert.Equal(t, -1, got.LineStart(0))
		assert.Equal(t, 0, got.LineRows(0))
		assert.Equal(t, -1, got.LineAt(0))
		assert.False(t, ok)
		assert.Empty(t, got.String())
	})
}
