package printer_test

import (
	"math"
	"slices"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

// layoutRows returns the rows each line of l takes, in layout order.
func layoutRows(l printer.Layout) []int {
	var rows []int

	prev := -1

	for row := range l.Rows() {
		if i := l.LineAt(row); i != prev {
			rows = append(rows, 0)
			prev = i
		}

		rows[len(rows)-1]++
	}

	return rows
}

// assertLayoutMeasures asserts that l counts the rows of printed and
// measures the widest of them.
func assertLayoutMeasures(t *testing.T, l printer.Layout, printed string) {
	t.Helper()

	assert.Equal(t, lipgloss.Height(printed), l.Rows())
	assert.Equal(t, lipgloss.Width(printed), l.Width())
}

// assertRowOfRunes asserts that l maps the column of each rune of input
// that keep selects to the printed row that holds it. The input is the
// content of line 0.
func assertRowOfRunes(t *testing.T, l printer.Layout, input string, rows []string, keep func(rune) bool) {
	t.Helper()

	for col, r := range []rune(input) {
		if !keep(r) {
			continue
		}

		row := l.RowOf(position.New(0, col))
		require.GreaterOrEqual(t, row, 0)
		require.Less(t, row, len(rows))
		assert.Contains(t, rows[row], string(r), "column %d", col)
	}
}

func TestPrinter_Layout(t *testing.T) {
	t.Parallel()

	// Width 20 with a five column line number gutter leaves 15 columns of
	// content, so the first line wraps into three pieces that start at
	// columns 0, 15, and 31, and its annotation above wraps into two rows.
	// Its annotation below marks column 5, so it sits below the first
	// piece.
	newView := func() *line.View {
		view := niceyaml.NewSourceFromString(stringtest.JoinLF(
			"key: this is a long value that wraps",
			"b: 2",
			"c: 3",
		)).View()
		view.Annotate(0, line.Annotation{Content: "a note above that wraps too", Placement: line.Above})
		view.Annotate(0, line.Annotation{Content: "below", Placement: line.Below, Cols: position.NewSpan(5, 5)})
		view.Annotate(2, line.Annotation{Content: "last", Placement: line.Below, Cols: position.NewSpan(3, 3)})

		return view
	}

	p := testPrinterWithGutter(printer.LineNumberGutter).With(printer.WithWrapWidth(20))

	t.Run("rows match print", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			opts     []printer.Option
			rowOf    map[position.Position]int // Position to the row that shows it.
			want     string
			wantRows []int
		}{
			"wrapped with annotations": {
				want: stringtest.JoinLF(
					"     a note above",
					"     that wraps too",
					"   1 key: this is a",
					"          ^ below",
					"   - long value that",
					"   - wraps",
					"   2 b: 2",
					"   3 c: 3",
					"        ^ last",
				),
				wantRows: []int{6, 1, 2},
			},
			"wrapping off gives one content row per line": {
				opts: []printer.Option{printer.WithWrapWidth(0)},
				want: stringtest.JoinLF(
					"     a note above that wraps too",
					"   1 key: this is a long value that wraps",
					"          ^ below",
					"   2 b: 2",
					"   3 c: 3",
					"        ^ last",
				),
				wantRows: []int{3, 1, 2},
				rowOf:    map[position.Position]int{position.New(0, 0): 1, position.New(0, 1000): 1},
			},
			"annotations off counts no annotation rows": {
				opts: []printer.Option{printer.WithAnnotation(printer.NoAnnotation)},
				want: stringtest.JoinLF(
					"   1 key: this is a",
					"   - long value that",
					"   - wraps",
					"   2 b: 2",
					"   3 c: 3",
				),
				wantRows: []int{3, 1, 1},
				rowOf: map[position.Position]int{
					position.New(0, 0):  0,
					position.New(0, 15): 1,
					position.New(1, 0):  3,
				},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				view := newView()
				p := p.With(tc.opts...)

				got := p.Print(view)
				require.Equal(t, tc.want, got)

				l := p.Layout(view)

				assertLayoutMeasures(t, l, got)
				assert.Equal(t, 3, l.Count())
				assert.Equal(t, tc.wantRows, layoutRows(l))

				for pos, row := range tc.rowOf {
					assert.Equal(t, row, l.RowOf(pos), "position %s", pos)
				}
			})
		}
	})

	t.Run("line start is the prefix sum of line rows", func(t *testing.T) {
		t.Parallel()

		l := p.Layout(newView())

		start := 0
		for i := range l.Count() {
			assert.Equal(t, start, l.LineStart(i), "line %d", i)

			start += l.LineRows(i)
		}

		assert.Equal(t, start, l.Rows())
	})

	t.Run("line at maps every row back to its line", func(t *testing.T) {
		t.Parallel()

		l := p.Layout(newView())

		for i := range l.Count() {
			for row := l.LineStart(i); row < l.LineStart(i)+l.LineRows(i); row++ {
				assert.Equal(t, i, l.LineAt(row), "row %d", row)
			}
		}
	})

	t.Run("line at clamps rows outside the layout", func(t *testing.T) {
		t.Parallel()

		l := p.Layout(newView())

		assert.Equal(t, 0, l.LineAt(-1))
		assert.Equal(t, 0, l.LineAt(-100))
		assert.Equal(t, 0, l.LineAt(math.MinInt))
		assert.Equal(t, 2, l.LineAt(l.Rows()))
		assert.Equal(t, 2, l.LineAt(l.Rows()+100))
		assert.Equal(t, 2, l.LineAt(math.MaxInt))
	})

	t.Run("empty view", func(t *testing.T) {
		t.Parallel()

		l := p.Layout(line.NewView(line.Lines{}))

		assert.Equal(t, 0, l.Rows())
		assert.Equal(t, 0, l.Count())
		assert.Equal(t, -1, l.LineAt(0))
		assert.Equal(t, -1, l.RowOf(position.New(0, 0)))
		assert.Equal(t, 0, l.Width())

		// The zero Layout holds no cell either.
		_, ok := printer.Layout{}.PositionAt(0, 0)
		assert.False(t, ok)
	})

	t.Run("row of", func(t *testing.T) {
		t.Parallel()

		l := p.Layout(newView())

		tcs := map[string]struct {
			pos  position.Position
			want int
		}{
			"column zero lands below the annotation rows above":   {pos: position.New(0, 0), want: 2},
			"last column of the first piece":                      {pos: position.New(0, 13), want: 2},
			"space the wrapper dropped belongs to the row before": {pos: position.New(0, 14), want: 2},
			"first column of the second piece":                    {pos: position.New(0, 15), want: 4},
			"space dropped at the second break":                   {pos: position.New(0, 30), want: 4},
			"first column of the third piece":                     {pos: position.New(0, 31), want: 5},
			"column past the end lands on the last content row":   {pos: position.New(0, 1000), want: 5},
			"column at max int lands on the last content row":     {pos: position.New(0, math.MaxInt), want: 5},
			"negative column lands on the first content row":      {pos: position.New(0, -1), want: 2},
			"column at min int lands on the first content row":    {pos: position.New(0, math.MinInt), want: 2},
			"line without annotations":                            {pos: position.New(1, 0), want: 6},
			"line with an annotation below":                       {pos: position.New(2, 3), want: 7},
			"line past the layout":                                {pos: position.New(3, 0), want: -1},
			"negative line":                                       {pos: position.New(-1, 0), want: -1},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				assert.Equal(t, tc.want, l.RowOf(tc.pos))
			})
		}
	})

	t.Run("slice keeps the indices of the content", func(t *testing.T) {
		t.Parallel()

		view := newView().Slice(position.NewSpan(2, 3), position.NewSpan(0, 1))

		got := p.Print(view)
		require.Equal(t, stringtest.JoinLF(
			"     a note above",
			"     that wraps too",
			"   1 key: this is a",
			"          ^ below",
			"   - long value that",
			"   - wraps",
			"   3 c: 3",
			"        ^ last",
		), got)

		l := p.Layout(view)

		assertLayoutMeasures(t, l, got)
		assert.Equal(t, 2, l.Count())
		assert.Equal(t, []int{6, 2}, layoutRows(l))

		// The layout speaks in the indices of the content, as the view
		// does, so line 2 of the content starts after the six rows of
		// line 0 and line 1, which the slice dropped, takes no rows.
		assert.Equal(t, 0, l.LineStart(0))
		assert.Equal(t, 6, l.LineStart(2))
		assert.Equal(t, 6, l.LineRows(0))
		assert.Equal(t, 2, l.LineRows(2))
		assert.Equal(t, -1, l.LineStart(1))
		assert.Equal(t, 0, l.LineRows(1))

		assert.Equal(t, 0, l.LineAt(0))
		assert.Equal(t, 0, l.LineAt(5))
		assert.Equal(t, 2, l.LineAt(6))
		assert.Equal(t, 2, l.LineAt(7))
		assert.Equal(t, 2, l.LineAt(100))
		assert.Equal(t, 0, l.LineAt(-1))

		// RowOf takes a position in the content too, and line 1 is not in
		// the layout.
		assert.Equal(t, 2, l.RowOf(position.New(0, 0)))
		assert.Equal(t, 4, l.RowOf(position.New(0, 15)))
		assert.Equal(t, 6, l.RowOf(position.New(2, 0)))
		assert.Equal(t, -1, l.RowOf(position.New(1, 0)))
	})

	t.Run("a style that changes the text keeps columns aligned", func(t *testing.T) {
		t.Parallel()

		// A transform may rewrite every rune of the text it styles, as
		// strings.ToUpper does, or add text of its own, as brackets do.
		// Either way, each row still starts at the column of the content
		// it shows, and an annotation indents from the start of its row.
		//
		// The runes a transform adds may equal runes of the text it
		// styles, as the "v" and "a" of a "val=" prefix do for "value".
		// They still count as added runes, so every column of the text
		// maps to the rune it shows.
		const words = "key: aaaa bbbb cccc dddd"

		// Literal returns styles whose string values render in st.
		literal := func(st lipgloss.Style) style.Styles {
			return style.New(lipgloss.NewStyle(), style.Set(kind.LiteralString, st))
		}
		prefixed := literal(bracketStyle("val=", ""))

		tcs := map[string]struct {
			styles  style.Styles
			below   []line.Annotation
			content string
			want    string
			rows    map[int]int // Column to row.
			cells   map[int]int // Column to cell.
			wrap    int
		}{
			"base style uppercases every run": {
				styles:  style.New(lipgloss.NewStyle().Transform(strings.ToUpper)),
				content: words,
				wrap:    10,
				want:    stringtest.JoinLF("KEY: AAAA", "BBBB CCCC", "DDDD"),
				rows:    map[int]int{0: 0, 8: 0, 9: 0, 10: 1, 19: 1, 20: 2, 1000: 2},
			},
			"key style uppercases the first run": {
				styles: style.New(
					lipgloss.NewStyle(),
					style.Set(kind.NameTag, lipgloss.NewStyle().Transform(strings.ToUpper)),
				),
				below:   []line.Annotation{{Content: "x", Placement: line.Below, Cols: position.NewSpan(20, 20)}},
				content: words,
				wrap:    10,
				want:    stringtest.JoinLF("KEY: aaaa", "bbbb cccc", "dddd", "^ x"),
				rows:    map[int]int{0: 0, 8: 0, 9: 0, 10: 1, 19: 1, 20: 2, 1000: 2},
			},
			"value style adds brackets": {
				styles:  literal(bracketStyle("<", ">")),
				content: words,
				wrap:    10,
				want:    stringtest.JoinLF("key: <aaaa", "bbbb cccc", "dddd>"),
				rows:    map[int]int{0: 0, 10: 1, 20: 2},
			},
			"prefix that wraps": {
				styles:  prefixed,
				content: "k: value",
				wrap:    5,
				want:    stringtest.JoinLF("k:", "val=v", "alue"),
				rows:    map[int]int{0: 0, 3: 1, 4: 2, 7: 2},
				cells:   map[int]int{3: 0, 4: 0, 5: 1},
			},
			"prefix": {
				styles:  prefixed,
				content: "k: value",
				want:    "k: val=value",
				cells:   map[int]int{3: 3, 4: 8, 5: 9, 7: 11, 8: 12},
			},
			"quotes around a quoted string": {
				styles:  literal(bracketStyle(`"`, `"`)),
				content: `k: "abc"`,
				want:    `k: ""abc""`,
				cells:   map[int]int{3: 3, 4: 5, 5: 6, 7: 8, 8: 10},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				view := niceyaml.NewSourceFromString(tc.content).View()
				view.Annotate(0, tc.below...)

				p := testPrinter().With(printer.WithWrapWidth(tc.wrap), printer.WithStyles(tc.styles))

				require.Equal(t, tc.want, p.Print(view))

				l := p.Layout(view)

				for col, row := range tc.rows {
					assert.Equal(t, row, l.RowOf(position.New(0, col)), "row of column %d", col)
				}

				for col, cell := range tc.cells {
					assert.Equal(t, cell, l.CellOf(position.New(0, col)), "cell of column %d", col)
				}
			})
		}
	})

	t.Run("width and gutter width match the rendered rows", func(t *testing.T) {
		t.Parallel()

		view := newView()
		l := p.Layout(view)
		got := p.Print(view)

		for row := range strings.SplitSeq(got, "\n") {
			assert.GreaterOrEqual(t, lipgloss.Width(row), l.GutterWidth())
		}

		assert.Equal(t, lipgloss.Width(got), l.Width())
		assert.LessOrEqual(t, l.Width(), 20)
		assert.Equal(t, 5, l.GutterWidth())
		assert.Equal(t, lipgloss.Width("   1 "), l.GutterWidth())
	})
}

func TestPrinter_Layout_GutterWidth(t *testing.T) {
	t.Parallel()

	short := niceyaml.NewSourceFromString(stringtest.JoinLF(
		"a: 1",
		"b: 2",
		"c: 3",
	)).View()
	long := niceyaml.NewSourceFromString(tenThousandLines + "last: v").View()

	tcs := map[string]struct {
		view   *line.View
		gutter printer.Gutter
		want   int
	}{
		"no gutter": {
			view:   short,
			gutter: printer.NoGutter,
			want:   0,
		},
		"diff gutter": {
			view:   short,
			gutter: printer.DiffGutter,
			want:   1,
		},
		"line numbers": {
			view:   short,
			gutter: printer.LineNumberGutter,
			want:   5,
		},
		"default gutter": {
			view:   short,
			gutter: printer.DefaultGutter,
			want:   6,
		},
		"default gutter with five digit numbers": {
			view:   long,
			gutter: printer.DefaultGutter,
			want:   7,
		},
		"slice keeps the width of its largest number": {
			view:   long.Slice(position.NewSpan(10000, long.Count())),
			gutter: printer.LineNumberGutter,
			want:   6,
		},
		"empty view": {
			view:   line.NewView(line.Lines{}),
			gutter: printer.DefaultGutter,
			want:   6,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := testPrinterWithGutter(tc.gutter)
			got := p.Layout(tc.view).GutterWidth()

			assert.Equal(t, tc.want, got)

			// Every rendered row starts with a gutter of that width, so the
			// first row is at least that wide.
			if tc.view.Count() > 0 {
				first, _, _ := strings.Cut(p.Print(tc.view), "\n")
				assert.GreaterOrEqual(t, lipgloss.Width(first), got)
			}
		})
	}
}

func TestPrinter_Layout_Width(t *testing.T) {
	t.Parallel()

	uneven := niceyaml.NewSourceFromString(stringtest.JoinLF("a: 1", "bb: 222")).View()
	wide := niceyaml.NewSourceFromString(stringtest.JoinLF("k: 日日日", "b: 2")).View()

	annotated := niceyaml.NewSourceFromString(stringtest.JoinLF("a: 1", "b: 2")).View()
	annotated.Annotate(0, line.Annotation{Content: "a note wider than the lines", Placement: line.Above})

	noted := niceyaml.NewSourceFromString("key: value\n").View()
	noted.Annotate(0, line.Annotation{Content: "note", Placement: line.Below, Cols: position.NewSpan(5, 5)})

	tcs := map[string]struct {
		view   *line.View
		gutter printer.Gutter // Nil for no gutter.
		opts   []printer.Option
		spans  []position.Span // Nil for every line.
		wrap   int
		want   int
	}{
		"widest line": {
			view: uneven,
			want: 7,
		},
		"gutter adds to every row": {
			view:   uneven,
			gutter: printer.DefaultGutter,
			want:   13,
		},
		"wide characters take two cells each": {
			view: wide,
			want: 9,
		},
		// The wrap would split the keycap after its ASCII digit, and the
		// rest of the cluster stays with the digit instead.
		"keycap at a wrap boundary": {
			view:   niceyaml.NewSourceFromString("1\ufe0f\u20e3: bb").View(),
			gutter: printer.DefaultGutter,
			wrap:   7,
			want:   8,
		},
		"annotation row wider than the lines": {
			view: annotated,
			want: 27,
		},
		// The style pads the annotation row past the content, and the layout
		// measures that row styled, as Print renders it.
		"annotation row its style pads": {
			view: noted,
			opts: []printer.Option{printer.WithStyles(style.New(
				lipgloss.NewStyle(),
				style.Set(kind.UIAnnotation, lipgloss.NewStyle().PaddingLeft(6)),
			))},
			want: 17,
		},
		"span selects the lines measured": {
			view:  uneven,
			spans: []position.Span{position.NewSpan(0, 1)},
			want:  4,
		},
		"empty view": {
			view:   line.NewView(line.Lines{}),
			gutter: printer.DefaultGutter,
			want:   0,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := testPrinterWithGutter(tc.gutter).With(printer.WithWrapWidth(tc.wrap)).With(tc.opts...)
			view := tc.view
			if tc.spans != nil {
				view = view.Slice(tc.spans...)
			}

			got := p.Layout(view).Width()

			assert.Equal(t, tc.want, got)

			// The width is the widest row Print renders.
			if view.Count() > 0 {
				assert.Equal(t, got, lipgloss.Width(p.Print(view)))
			}
		})
	}
}

func TestPrinter_Layout_LineWidth(t *testing.T) {
	t.Parallel()

	view := niceyaml.NewSourceFromString(stringtest.JoinLF(
		"a: 1",
		"bb: "+strings.Repeat("日", 3),
		"c: 3",
	)).View()
	view.Annotate(2, line.Annotation{Content: "a note wider than the lines", Placement: line.Below})

	tcs := map[string]struct {
		spans []position.Span
		want  []int
	}{
		"every line": {
			spans: []position.Span{position.NewSpan(0, 3)},
			want:  []int{10, 16, 35},
		},
		"lines the view does not hold": {
			spans: []position.Span{position.NewSpan(1, 2)},
			want:  []int{0, 16, 0},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := testPrinterWithGutter(printer.DefaultGutter)
			sliced := view.Slice(tc.spans...)
			l := p.Layout(sliced)

			got := make([]int, 0, len(tc.want))
			for i := range tc.want {
				got = append(got, l.LineWidth(i))
			}

			assert.Equal(t, tc.want, got)
			assert.Equal(t, slices.Max(got), l.Width())

			// Each width is the widest row Print renders for the line.
			for i := range sliced.All() {
				printed := p.Print(view.Slice(position.NewSpan(i, i+1)))
				assert.Equal(t, lipgloss.Width(printed), l.LineWidth(i), "line %d", i)
			}

			assert.Equal(t, 0, l.LineWidth(-1))
			assert.Equal(t, 0, l.LineWidth(len(tc.want)))
		})
	}
}

func TestPrinter_Layout_MultiLineAnnotation(t *testing.T) {
	t.Parallel()

	// A custom annotation may span several lines. The layout counts each
	// of them as a row, wrapping or not, so the rows it reports match the
	// rows Print writes.
	joined := func(ctx printer.AnnotationContext) []printer.AnnotationRow {
		return []printer.AnnotationRow{{Text: strings.Join(ctx.Annotations.Contents(), "\n")}}
	}

	tcs := map[string]struct {
		width int
	}{
		"without wrapping": {width: 0},
		"with wrapping":    {width: 30},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "b: 2")).View()
			view.Annotate(0, line.Annotation{Content: "one\ntwo"})

			p := testPrinter().With(printer.WithAnnotation(joined), printer.WithWrapWidth(tc.width))

			printed := strings.Split(strings.TrimSuffix(p.Print(view), "\n"), "\n")
			assert.Len(t, printed, p.Layout(view).Rows())
		})
	}
}

func TestPrinter_Layout_StyledAnnotationRows(t *testing.T) {
	t.Parallel()

	// A style with a width, vertical padding, a margin, or a border lays an
	// annotation out over more rows than the wrap gives it. The layout
	// counts and measures those rows as Print writes them, and Print puts
	// the gutter on each of them.
	tcs := map[string]struct {
		style  lipgloss.Style
		gutter printer.Gutter // Nil for no gutter.
		wrap   int
		col    int
	}{
		"width":            {style: lipgloss.NewStyle().Width(6)},
		"vertical padding": {style: lipgloss.NewStyle().PaddingTop(1)},
		"margin":           {style: lipgloss.NewStyle().MarginBottom(1)},
		"border":           {style: lipgloss.NewStyle().Border(lipgloss.NormalBorder())},
		"continuation rows": {
			style: lipgloss.NewStyle().PaddingLeft(2),
			wrap:  12,
			col:   2,
		},
		"gutter": {
			style:  lipgloss.NewStyle().Width(6),
			gutter: printer.DefaultGutter,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := niceyaml.NewSourceFromString("a: 1\n").View()
			view.Annotate(
				0,
				line.Annotation{
					Content:   "hello world foo",
					Placement: line.Below,
					Cols:      position.NewSpan(tc.col, tc.col),
				},
			)

			p := testPrinterWithStyles(style.Set(kind.UIAnnotation, tc.style)).With(
				printer.WithGutter(tc.gutter),
				printer.WithWrapWidth(tc.wrap),
			)

			printed := p.Print(view)
			l := p.Layout(view)

			assertLayoutMeasures(t, l, printed)
			assert.Greater(t, l.Rows(), 2, "the annotation takes several rows")

			gutter := strings.Repeat(" ", l.GutterWidth())
			for i, row := range strings.Split(printed, "\n")[1:] {
				assert.True(t, strings.HasPrefix(ansi.Strip(row), gutter), "row %d: %q", i+1, row)
			}
		})
	}
}

func TestPrinter_Layout_TallGutter(t *testing.T) {
	t.Parallel()

	// A gutter kind in a style with a margin, vertical padding, a border,
	// or a width narrower than its text, or a gutter that renders a
	// newline, renders over more than one row. The printer keeps only the
	// first row of the gutter, so Print writes the rows the layout counts.
	tcs := map[string]struct {
		gutter printer.Gutter
		kind   kind.Kind
		style  lipgloss.Style
	}{
		"line number margin": {
			kind:  kind.UILineNumber,
			style: lipgloss.NewStyle().MarginBottom(1),
		},
		"line number width": {
			kind:  kind.UILineNumber,
			style: lipgloss.NewStyle().Width(2),
		},
		"line number border": {
			kind:  kind.UILineNumber,
			style: lipgloss.NewStyle().Border(lipgloss.NormalBorder()),
		},
		"diff marker margin": {
			gutter: printer.DiffGutter,
			kind:   kind.GenericInserted,
			style:  lipgloss.NewStyle().MarginBottom(1),
		},
		"text padding": {
			kind:  kind.Text,
			style: lipgloss.NewStyle().PaddingTop(1),
		},
		"gutter func": {
			gutter: printer.GutterFunc(func(printer.GutterContext) string {
				return "x\ny "
			}),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "b: 2")).View()
			view.SetFlag(0, line.FlagInserted)
			view.Annotate(0, line.Annotation{Content: "note", Placement: line.Below})

			opts := []printer.Option{printer.WithContainerStyle(lipgloss.NewStyle())}
			if tc.gutter != nil {
				opts = append(opts, printer.WithGutter(tc.gutter))
			}

			if tc.kind != "" {
				opts = append(opts, printer.WithStyles(style.New(
					lipgloss.NewStyle(),
					style.Set(tc.kind, tc.style),
				)))
			}

			p := printer.New(opts...)
			printed := strings.Split(p.Print(view), "\n")
			l := p.Layout(view)

			require.Len(t, printed, l.Rows(), "printed: %q", printed)

			// Without a gutter, the view prints the same content rows, so
			// each printed row holds one of them from the gutter width on.
			bare := strings.Split(p.With(printer.WithGutter(printer.NoGutter)).Print(view), "\n")
			require.Len(t, bare, len(printed))

			for i, row := range printed {
				content := ansi.Cut(row, l.GutterWidth(), lipgloss.Width(row))
				assert.Equal(t, ansi.Strip(bare[i]), ansi.Strip(content), "row %d: %q", i, row)
			}
		})
	}
}

// transformedRun shows as "k: [ab] cd" with an overlay on [onAB], because
// the overlay style wraps its text in brackets. Every cell after the
// overlay shifts by the two brackets it adds.
const transformedRun = "k: ab cd"

// onAB is the range of "ab" in [transformedRun].
var onAB = []position.Range{yamltest.LineRange(0, 3, 5)}

func TestPrinter_Layout_CellOf(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		content  string
		overlays []position.Range
		pos      position.Position
		wrap     int
		want     int
	}{
		"plain column": {
			content: "key: value",
			pos:     position.New(0, 5),
			want:    5,
		},
		"wide runes before the column": {
			content: "k: 日本x",
			pos:     position.New(0, 5),
			want:    7,
		},
		"column inside a cluster takes the cell of its start": {
			content: "k: e\u0301x",
			pos:     position.New(0, 4),
			want:    3,
		},
		"transform before the column": {
			content:  transformedRun,
			overlays: onAB,
			pos:      position.New(0, 6),
			want:     8,
		},
		"first column of a transformed run": {
			content:  transformedRun,
			overlays: onAB,
			pos:      position.New(0, 3),
			want:     3,
		},
		"column inside a transformed run": {
			content:  transformedRun,
			overlays: onAB,
			pos:      position.New(0, 4),
			want:     5,
		},
		"end of a transformed run": {
			content:  transformedRun,
			overlays: onAB,
			pos:      position.New(0, 5),
			want:     7,
		},
		"columns past the end take a cell each": {
			content: "a: 1",
			pos:     position.New(0, 6),
			want:    6,
		},
		"wrapped row counts from its own start": {
			content: "key: aaaa bbbb cccc",
			wrap:    10,
			pos:     position.New(0, 12),
			want:    2,
		},
		"line the layout does not hold": {
			content: "a: 1",
			pos:     position.New(5, 0),
			want:    -1,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := niceyaml.NewSourceFromString(tc.content).View()
			view.AddOverlay(testOverlayHighlight, tc.overlays...)

			p := testPrinter().With(printer.WithWrapWidth(tc.wrap))

			assert.Equal(t, tc.want, p.Layout(view).CellOf(tc.pos))
		})
	}
}

func TestPrinter_Layout_Clip(t *testing.T) {
	t.Parallel()

	// The row reads "...fghijklmno...JKLMNOPQRS...", and with a width of
	// 16 it wraps after the second ellipsis.
	newView := func() *line.View {
		return newClippedView(clipLetters, 10,
			[]position.Span{position.NewSpan(15, 16), position.NewSpan(45, 46)},
			line.Annotation{Content: "first", Placement: line.Below, Cols: position.NewSpan(15, 16)},
			line.Annotation{Content: "second and widest", Placement: line.Below, Cols: position.NewSpan(45, 46)},
		)
	}

	tcs := map[string]struct {
		col      int
		wrap     int
		wantRow  int
		wantCell int
	}{
		"column before the first window takes the first ellipsis": {col: 0, wantCell: 0},
		"start of the first window":                               {col: 10, wantCell: 3},
		"first mark":                                              {col: 15, wantCell: 8},
		"column between the windows takes the ellipsis there":     {col: 25, wantCell: 13},
		"second mark":                                             {col: 45, wantCell: 21},
		"column after the last window takes the last ellipsis":    {col: 52, wantCell: 26},
		"end of the line is the end of the row":                   {col: 57, wantCell: 29},
		"column past the end keeps its distance":                  {col: 60, wantCell: 32},
		"negative column counts as the start":                     {col: -1, wantCell: 0},
		"wrapped first mark":                                      {col: 15, wrap: 16, wantRow: 0, wantCell: 8},
		"wrapped column between the windows":                      {col: 25, wrap: 16, wantRow: 0, wantCell: 13},
		"wrapped second mark":                                     {col: 45, wrap: 16, wantRow: 2, wantCell: 5},
		"wrapped column after the last window":                    {col: 52, wrap: 16, wantRow: 2, wantCell: 10},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			l := testPrinter().With(printer.WithWrapWidth(tc.wrap)).Layout(newView())
			pos := position.New(0, tc.col)

			assert.Equal(t, tc.wantRow, l.RowOf(pos), "row")
			assert.Equal(t, tc.wantCell, l.CellOf(pos), "cell")
		})
	}

	t.Run("width is that of the clipped rows", func(t *testing.T) {
		t.Parallel()

		view := newView()
		l := testPrinter().Layout(view)

		// The row of the second annotation is the widest: 21 cells of
		// padding, then "^ second and widest".
		assert.Equal(t, 40, l.Width())
		assert.Equal(t, 40, l.LineWidth(0))
		assert.Equal(t, 29, testPrinter().With(printer.WithAnnotation(printer.NoAnnotation)).Layout(view).Width())
	})

	t.Run("cell of a mark cuts its rune from the printed row", func(t *testing.T) {
		t.Parallel()

		view := newView()
		p := printer.New(printer.WithContainerStyle(lipgloss.NewStyle()))
		l := p.Layout(view)
		row, _, _ := strings.Cut(p.Print(view), "\n")

		for col, want := range map[int]string{15: "k", 45: "O", 25: ".", 0: "."} {
			cell := l.GutterWidth() + l.CellOf(position.New(0, col))

			assert.Equal(t, want, ansi.Strip(printer.Cut(row, cell, cell+1)), "column %d", col)
		}
	})
}

func TestPrinter_Layout_PositionAt(t *testing.T) {
	t.Parallel()

	const (
		// A clip of 10 columns shows this line as "k: abcdefg...".
		letters = "k: abcdefghijklmnopqrstuvwxyz"
		// A clip of 16 columns and a wrap of 8 show this line as "...
		// Cccc", "dddd", and "eeee ...", with an annotation at column 20
		// below the second.
		words = "k: aaaa bbbb cccc dddd eeee ffff gggg"
	)

	twoLines := stringtest.JoinLF("a: 1", "b: 2")

	// An annotation at column 20 moves the window of a clipped line, so
	// the letters show as "...mnopqrstuv...".
	at20 := &line.Annotation{Content: "x", Placement: line.Below, Cols: position.NewSpan(20, 20)}

	tcs := map[string]struct {
		annotation *line.Annotation
		slice      *position.Span
		want       *position.Position // Nil for a cell that shows no column.
		content    string
		overlays   []position.Range
		clip       int
		wrap       int
		row        int
		cell       int
	}{
		"first cell": {
			content: "key: value",
			want:    new(position.New(0, 0)),
		},
		"plain column": {
			content: "key: value",
			cell:    5,
			want:    new(position.New(0, 5)),
		},
		"last rune of a row": {
			content: "a: 1",
			cell:    3,
			want:    new(position.New(0, 3)),
		},
		"cell past the last rune": {
			content: "a: 1",
			cell:    4,
		},
		"negative cell": {
			content: "a: 1",
			cell:    -1,
		},
		"negative row": {
			content: "a: 1",
			row:     -1,
		},
		"row past the last": {
			content: "a: 1",
			row:     1,
		},
		"later line": {
			content: twoLines,
			row:     1,
			cell:    3,
			want:    new(position.New(1, 3)),
		},
		"blank line": {
			content: stringtest.JoinLF(
				"a: 1",
				"",
				"b: 2",
			),
			row: 1,
		},
		"first cell of a wide rune": {
			content: "k: 日本x",
			cell:    3,
			want:    new(position.New(0, 3)),
		},
		"second cell of a wide rune": {
			content: "k: 日本x",
			cell:    4,
			want:    new(position.New(0, 3)),
		},
		"rune after wide runes": {
			content: "k: 日本x",
			cell:    7,
			want:    new(position.New(0, 5)),
		},
		"cluster of two runes": {
			content: "k: e\u0301x",
			cell:    3,
			want:    new(position.New(0, 3)),
		},
		"rune after a cluster of two runes": {
			content: "k: e\u0301x",
			cell:    4,
			want:    new(position.New(0, 5)),
		},
		"rune of no width gives its cell to the next": {
			content: "k: a\u200bb",
			cell:    4,
			want:    new(position.New(0, 5)),
		},
		"rune a transform adds before its run": {
			content:  transformedRun,
			overlays: onAB,
			cell:     3,
			want:     new(position.New(0, 3)),
		},
		"first column of a transformed run": {
			content:  transformedRun,
			overlays: onAB,
			cell:     4,
			want:     new(position.New(0, 3)),
		},
		"column inside a transformed run": {
			content:  transformedRun,
			overlays: onAB,
			cell:     5,
			want:     new(position.New(0, 4)),
		},
		"rune a transform adds after its run": {
			content:  transformedRun,
			overlays: onAB,
			cell:     6,
			want:     new(position.New(0, 4)),
		},
		"column after a transformed run": {
			content:  transformedRun,
			overlays: onAB,
			cell:     8,
			want:     new(position.New(0, 6)),
		},
		"rune a transform adds at the end of a line": {
			content:  "k: ab",
			overlays: onAB,
			cell:     6,
			want:     new(position.New(0, 4)),
		},
		"wrapped row counts from its own start": {
			content: "key: aaaa bbbb cccc",
			wrap:    10,
			row:     1,
			cell:    2,
			want:    new(position.New(0, 12)),
		},
		"first cell of a wrapped row": {
			content: "key: aaaa bbbb cccc",
			wrap:    10,
			row:     1,
			want:    new(position.New(0, 10)),
		},
		"space the wrapper dropped": {
			content: "key: aaaa bbbb cccc",
			wrap:    10,
			cell:    9,
		},
		"annotation row": {
			content:    twoLines,
			annotation: &line.Annotation{Content: "bad", Placement: line.Below},
			row:        1,
		},
		"line below an annotation row": {
			content:    twoLines,
			annotation: &line.Annotation{Content: "bad", Placement: line.Below},
			row:        2,
			cell:       3,
			want:       new(position.New(1, 3)),
		},
		"line below an annotation above it": {
			content:    twoLines,
			annotation: &line.Annotation{Content: "bad", Placement: line.Above},
			row:        1,
			cell:       3,
			want:       new(position.New(0, 3)),
		},
		"line of a slice keeps its index in the content": {
			content: stringtest.JoinLF(
				"a: 1",
				"b: 2",
				"c: 3",
			),
			slice: &position.Span{Start: 2, End: 3},
			cell:  3,
			want:  new(position.New(2, 3)),
		},
		"empty view": {content: ""},
		"last column a clipped line keeps": {
			content: letters,
			clip:    10,
			cell:    9,
			want:    new(position.New(0, 9)),
		},
		"ellipsis at the end of a clipped line": {
			content: letters,
			clip:    10,
			cell:    10,
		},
		"last cell of an ellipsis": {
			content: letters,
			clip:    10,
			cell:    12,
		},
		"cell past the ellipsis that ends a clipped line": {
			content: letters,
			clip:    10,
			cell:    13,
		},
		"ellipsis at the start of a clipped line": {
			content:    letters,
			annotation: at20,
			clip:       10,
			cell:       2,
		},
		"first column of a window": {
			content:    letters,
			annotation: at20,
			clip:       10,
			cell:       3,
			want:       new(position.New(0, 15)),
		},
		"last column of a window": {
			content:    letters,
			annotation: at20,
			clip:       10,
			cell:       12,
			want:       new(position.New(0, 24)),
		},
		"annotation row of a clipped line": {
			content:    letters,
			annotation: at20,
			clip:       10,
			row:        1,
			cell:       8,
		},
		// The overlay marks columns 20 and 21, so the line shows as
		// "...mnopq[rs]tuv...".
		"rune a transform adds on a clipped line": {
			content:  letters,
			overlays: []position.Range{yamltest.LineRange(0, 20, 22)},
			clip:     10,
			cell:     8,
			want:     new(position.New(0, 20)),
		},
		"column after a transformed run on a clipped line": {
			content:  letters,
			overlays: []position.Range{yamltest.LineRange(0, 20, 22)},
			clip:     10,
			cell:     12,
			want:     new(position.New(0, 22)),
		},
		// The line shows as "...日本語日本語日本語".
		"second cell of a wide rune on a clipped line": {
			content:    "k: 日本語日本語日本語日本語",
			annotation: &line.Annotation{Content: "x", Placement: line.Below, Cols: position.NewSpan(9, 9)},
			clip:       6,
			cell:       6,
			want:       new(position.New(0, 7)),
		},
		"wrapped row of a clipped line": {
			content:    words,
			annotation: at20,
			clip:       16,
			wrap:       8,
			row:        1,
			want:       new(position.New(0, 18)),
		},
		"last wrapped row of a clipped line": {
			content:    words,
			annotation: at20,
			clip:       16,
			wrap:       8,
			row:        3,
			cell:       3,
			want:       new(position.New(0, 26)),
		},
		"ellipsis on a wrapped row of a clipped line": {
			content:    words,
			annotation: at20,
			clip:       16,
			wrap:       8,
			row:        3,
			cell:       5,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := niceyaml.NewSourceFromString(tc.content).View()
			view.AddOverlay(testOverlayHighlight, tc.overlays...)

			if tc.annotation != nil {
				view.Annotate(0, *tc.annotation)
			}

			if tc.slice != nil {
				view = view.Slice(*tc.slice)
			}

			view = view.Clip(tc.clip)

			p := testPrinter().With(printer.WithWrapWidth(tc.wrap))

			// A cell that shows no column gives the zero position.
			var want position.Position

			if tc.want != nil {
				want = *tc.want
			}

			got, ok := p.Layout(view).PositionAt(tc.row, tc.cell)
			assert.Equal(t, tc.want != nil, ok)
			assert.Equal(t, want, got)
		})
	}
}

// clipped reports whether view leaves the column of pos out of the row of
// its line, as a view from [line.View.Clip] does for a long line.
func clipped(view *line.View, pos position.Position) bool {
	windows := view.Windows(pos.Line)

	return windows != nil && !slices.ContainsFunc(windows, func(w position.Span) bool {
		return w.Contains(pos.Col)
	})
}

func TestPrinter_Layout_PositionAt_Inverse(t *testing.T) {
	t.Parallel()

	full := yamltest.ReadFile(t, "../testdata/full.yaml")
	prefixed := style.New(lipgloss.NewStyle(), style.Set(kind.LiteralString, bracketStyle("val=", "")))

	tcs := map[string]struct {
		printer  *printer.Printer
		decorate func(*line.View) // Adds overlays and annotations to the view, or nil.
		input    string
		clip     int
	}{
		"plain": {
			printer: testPrinter(),
			input:   stringtest.LinesLF("a: 1", "b:", "  - x", "", "  - y # c"),
		},
		"wide runes and clusters": {
			printer: testPrinter(),
			input:   stringtest.LinesLF("k: 日本語 e\u0301x 😀", "b: 1\ufe0f\u20e3 x"),
		},
		"wrapped": {
			printer: testPrinter().With(printer.WithWrapWidth(10)),
			input:   stringtest.LinesLF("key: aaaa bbbb cccc 日本語日本語日本語", "b: 2"),
		},
		"brackets a transform adds": {
			printer: testPrinter().With(printer.WithWrapWidth(8)),
			input:   stringtest.LinesLF("k: ab cd ef gh", "b: 2"),
			decorate: func(view *line.View) {
				view.AddOverlay(testOverlayHighlight,
					yamltest.LineRange(0, 3, 5),
					yamltest.LineRange(0, 9, 14),
				)
			},
		},
		"prefix a transform adds": {
			printer: testPrinter().With(printer.WithWrapWidth(5), printer.WithStyles(prefixed)),
			input:   stringtest.LinesLF("k: value", "b: other words"),
		},
		"annotations": {
			printer: testPrinter().With(printer.WithWrapWidth(12)),
			input:   stringtest.LinesLF("key: aaaa bbbb cccc", "b: 2"),
			decorate: func(view *line.View) {
				view.Annotate(0, line.Annotation{Content: "above", Placement: line.Above})
				view.Annotate(
					0,
					line.Annotation{Content: "below", Placement: line.Below, Cols: position.NewSpan(12, 12)},
				)
				view.Annotate(1, line.Annotation{Content: "last", Placement: line.Below})
			},
		},
		"full.yaml with a gutter": {
			printer: testPrinterWithGutter(printer.DefaultGutter).With(printer.WithWrapWidth(40)),
			input:   full,
		},
		"clipped": {
			printer: testPrinter(),
			input:   stringtest.LinesLF("k: abcdefghijklmnopqrstuvwxyz", "b: 2"),
			clip:    8,
			decorate: func(view *line.View) {
				view.Annotate(0, line.Annotation{Content: "first", Placement: line.Below, Cols: position.NewSpan(5, 5)})
				view.Annotate(
					0,
					line.Annotation{Content: "second", Placement: line.Below, Cols: position.NewSpan(24, 24)},
				)
			},
		},
		"clipped with wide runes and brackets": {
			printer: testPrinter(),
			input:   "k: 日本語日本語 abcdefgh 日本語日本語\n",
			clip:    8,
			decorate: func(view *line.View) {
				view.AddOverlay(testOverlayHighlight, yamltest.LineRange(0, 12, 15))
			},
		},
		"clipped and wrapped": {
			printer: testPrinter().With(printer.WithWrapWidth(8)),
			input:   "k: aaaa bbbb cccc dddd eeee ffff gggg\n",
			clip:    16,
			decorate: func(view *line.View) {
				view.Annotate(0, line.Annotation{Content: "x", Placement: line.Below, Cols: position.NewSpan(20, 20)})
			},
		},
		"full.yaml clipped with a gutter": {
			printer: testPrinterWithGutter(printer.DefaultGutter).With(printer.WithWrapWidth(30)),
			input:   full,
			clip:    20,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := niceyaml.NewSourceFromString(tc.input).View()
			if tc.decorate != nil {
				tc.decorate(view)
			}

			view = view.Clip(tc.clip)
			l := tc.printer.Layout(view)

			// Every position a cell gives sits on that row, at that
			// cell or in the cells before it that its cluster or its
			// run takes.
			var hits int

			for row := range l.Rows() {
				for cell := range l.Width() {
					pos, ok := l.PositionAt(row, cell)
					if !ok {
						continue
					}

					hits++

					require.Equal(t, row, l.RowOf(pos), "row %d cell %d gives %s", row, cell, pos)
					require.LessOrEqual(t, l.CellOf(pos), cell, "row %d cell %d gives %s", row, cell, pos)
				}
			}

			assert.Positive(t, hits)

			// The cell of every rune a row shows gives back a column
			// that takes the same cell: the one where the cluster of the
			// rune starts, or the next one when the rune has no width.
			for i, ln := range view.All() {
				for col, r := range ln.Runes() {
					if r == '\n' {
						continue
					}

					pos := position.New(i, col)

					row, cell := l.RowOf(pos), l.CellOf(pos)

					got, ok := l.PositionAt(row, cell)
					if !ok {
						// Only a space the wrapper dropped at a break and a
						// column a clipped line leaves out take no cell.
						if !clipped(view, pos) {
							require.Equal(t, " ", string(r), "position %s", pos)
						}

						continue
					}

					require.False(t, clipped(view, pos), "position %s gives %s", pos, got)

					require.Equal(t, pos.Line, got.Line, "position %s", pos)
					require.Equal(t, row, l.RowOf(got), "position %s gives %s", pos, got)
					require.Equal(t, cell, l.CellOf(got), "position %s gives %s", pos, got)
				}
			}
		})
	}
}
