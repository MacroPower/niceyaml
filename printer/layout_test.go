package printer_test

import (
	"os"
	"slices"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

func TestPrinter_Layout_PositionAt(t *testing.T) {
	t.Parallel()

	// The overlay style wraps its text in brackets, so "k: ab cd" with
	// an overlay on "ab" shows as "k: [ab] cd".
	tcs := map[string]struct {
		overlay    *position.Span
		annotation *line.Annotation
		slice      *position.Span
		content    string
		clip       int
		wrap       int
		row        int
		cell       int
		want       position.Position
		ok         bool
	}{
		"first cell": {
			content: "key: value",
			want:    position.New(0, 0),
			ok:      true,
		},
		"plain column": {
			content: "key: value",
			cell:    5,
			want:    position.New(0, 5),
			ok:      true,
		},
		"last rune of a row": {
			content: "a: 1",
			cell:    3,
			want:    position.New(0, 3),
			ok:      true,
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
			content: "a: 1\nb: 2",
			row:     1,
			cell:    3,
			want:    position.New(1, 3),
			ok:      true,
		},
		"blank line": {
			content: "a: 1\n\nb: 2",
			row:     1,
		},
		"first cell of a wide rune": {
			content: "k: 日本x",
			cell:    3,
			want:    position.New(0, 3),
			ok:      true,
		},
		"second cell of a wide rune": {
			content: "k: 日本x",
			cell:    4,
			want:    position.New(0, 3),
			ok:      true,
		},
		"rune after wide runes": {
			content: "k: 日本x",
			cell:    7,
			want:    position.New(0, 5),
			ok:      true,
		},
		"cluster of two runes": {
			content: "k: e\u0301x",
			cell:    3,
			want:    position.New(0, 3),
			ok:      true,
		},
		"rune after a cluster of two runes": {
			content: "k: e\u0301x",
			cell:    4,
			want:    position.New(0, 5),
			ok:      true,
		},
		"rune of no width gives its cell to the next": {
			content: "k: a\u200bb",
			cell:    4,
			want:    position.New(0, 5),
			ok:      true,
		},
		"rune a transform adds before its run": {
			content: "k: ab cd",
			overlay: &position.Span{Start: 3, End: 5},
			cell:    3,
			want:    position.New(0, 3),
			ok:      true,
		},
		"first column of a transformed run": {
			content: "k: ab cd",
			overlay: &position.Span{Start: 3, End: 5},
			cell:    4,
			want:    position.New(0, 3),
			ok:      true,
		},
		"column inside a transformed run": {
			content: "k: ab cd",
			overlay: &position.Span{Start: 3, End: 5},
			cell:    5,
			want:    position.New(0, 4),
			ok:      true,
		},
		"rune a transform adds after its run": {
			content: "k: ab cd",
			overlay: &position.Span{Start: 3, End: 5},
			cell:    6,
			want:    position.New(0, 4),
			ok:      true,
		},
		"column after a transformed run": {
			content: "k: ab cd",
			overlay: &position.Span{Start: 3, End: 5},
			cell:    8,
			want:    position.New(0, 6),
			ok:      true,
		},
		"rune a transform adds at the end of a line": {
			content: "k: ab",
			overlay: &position.Span{Start: 3, End: 5},
			cell:    6,
			want:    position.New(0, 4),
			ok:      true,
		},
		"wrapped row counts from its own start": {
			content: "key: aaaa bbbb cccc",
			wrap:    10,
			row:     1,
			cell:    2,
			want:    position.New(0, 12),
			ok:      true,
		},
		"first cell of a wrapped row": {
			content: "key: aaaa bbbb cccc",
			wrap:    10,
			row:     1,
			want:    position.New(0, 10),
			ok:      true,
		},
		"space the wrapper dropped": {
			content: "key: aaaa bbbb cccc",
			wrap:    10,
			cell:    9,
		},
		"annotation row": {
			content:    "a: 1\nb: 2",
			annotation: &line.Annotation{Content: "bad", Placement: line.Below},
			row:        1,
		},
		"line below an annotation row": {
			content:    "a: 1\nb: 2",
			annotation: &line.Annotation{Content: "bad", Placement: line.Below},
			row:        2,
			cell:       3,
			want:       position.New(1, 3),
			ok:         true,
		},
		"line below an annotation above it": {
			content:    "a: 1\nb: 2",
			annotation: &line.Annotation{Content: "bad", Placement: line.Above},
			row:        1,
			cell:       3,
			want:       position.New(0, 3),
			ok:         true,
		},
		"line of a slice keeps its index in the content": {
			content: "a: 1\nb: 2\nc: 3",
			slice:   &position.Span{Start: 2, End: 3},
			cell:    3,
			want:    position.New(2, 3),
			ok:      true,
		},
		"empty view": {
			content: "",
		},
		// A clip of 10 columns shows this line as "k: abcdefg...".
		"last column a clipped line keeps": {
			content: "k: abcdefghijklmnopqrstuvwxyz",
			clip:    10,
			cell:    9,
			want:    position.New(0, 9),
			ok:      true,
		},
		"ellipsis at the end of a clipped line": {
			content: "k: abcdefghijklmnopqrstuvwxyz",
			clip:    10,
			cell:    10,
		},
		"last cell of an ellipsis": {
			content: "k: abcdefghijklmnopqrstuvwxyz",
			clip:    10,
			cell:    12,
		},
		"cell past the ellipsis that ends a clipped line": {
			content: "k: abcdefghijklmnopqrstuvwxyz",
			clip:    10,
			cell:    13,
		},
		// An annotation at column 20 moves the window, so the line shows
		// as "...mnopqrstuv...".
		"ellipsis at the start of a clipped line": {
			content:    "k: abcdefghijklmnopqrstuvwxyz",
			annotation: &line.Annotation{Content: "x", Placement: line.Below, Col: 20},
			clip:       10,
			cell:       2,
		},
		"first column of a window": {
			content:    "k: abcdefghijklmnopqrstuvwxyz",
			annotation: &line.Annotation{Content: "x", Placement: line.Below, Col: 20},
			clip:       10,
			cell:       3,
			want:       position.New(0, 15),
			ok:         true,
		},
		"last column of a window": {
			content:    "k: abcdefghijklmnopqrstuvwxyz",
			annotation: &line.Annotation{Content: "x", Placement: line.Below, Col: 20},
			clip:       10,
			cell:       12,
			want:       position.New(0, 24),
			ok:         true,
		},
		"annotation row of a clipped line": {
			content:    "k: abcdefghijklmnopqrstuvwxyz",
			annotation: &line.Annotation{Content: "x", Placement: line.Below, Col: 20},
			clip:       10,
			row:        1,
			cell:       8,
		},
		// The overlay marks columns 20 and 21, so the line shows as
		// "...mnopq[rs]tuv...".
		"rune a transform adds on a clipped line": {
			content: "k: abcdefghijklmnopqrstuvwxyz",
			overlay: &position.Span{Start: 20, End: 22},
			clip:    10,
			cell:    8,
			want:    position.New(0, 20),
			ok:      true,
		},
		"column after a transformed run on a clipped line": {
			content: "k: abcdefghijklmnopqrstuvwxyz",
			overlay: &position.Span{Start: 20, End: 22},
			clip:    10,
			cell:    12,
			want:    position.New(0, 22),
			ok:      true,
		},
		// The line shows as "...日本語日本語日本語".
		"second cell of a wide rune on a clipped line": {
			content:    "k: 日本語日本語日本語日本語",
			annotation: &line.Annotation{Content: "x", Placement: line.Below, Col: 9},
			clip:       6,
			cell:       6,
			want:       position.New(0, 7),
			ok:         true,
		},
		// The line shows as "... Cccc", "dddd", and "eeee ...", with the
		// annotation row below the second.
		"wrapped row of a clipped line": {
			content:    "k: aaaa bbbb cccc dddd eeee ffff gggg",
			annotation: &line.Annotation{Content: "x", Placement: line.Below, Col: 20},
			clip:       16,
			wrap:       8,
			row:        1,
			want:       position.New(0, 18),
			ok:         true,
		},
		"last wrapped row of a clipped line": {
			content:    "k: aaaa bbbb cccc dddd eeee ffff gggg",
			annotation: &line.Annotation{Content: "x", Placement: line.Below, Col: 20},
			clip:       16,
			wrap:       8,
			row:        3,
			cell:       3,
			want:       position.New(0, 26),
			ok:         true,
		},
		"ellipsis on a wrapped row of a clipped line": {
			content:    "k: aaaa bbbb cccc dddd eeee ffff gggg",
			annotation: &line.Annotation{Content: "x", Placement: line.Below, Col: 20},
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
			if tc.overlay != nil {
				view.AddOverlay(testOverlayHighlight, position.NewRange(
					position.New(0, tc.overlay.Start),
					position.New(0, tc.overlay.End),
				))
			}

			if tc.annotation != nil {
				view.Annotate(0, *tc.annotation)
			}

			if tc.slice != nil {
				view = view.Slice(*tc.slice)
			}

			view = view.Clip(tc.clip)

			p := testPrinter().With(printer.WithWrap(tc.wrap))

			got, ok := p.Layout(view).PositionAt(tc.row, tc.cell)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
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

func TestPrinter_Layout_PositionAt_ZeroLayout(t *testing.T) {
	t.Parallel()

	_, ok := printer.Layout{}.PositionAt(0, 0)
	assert.False(t, ok)
}

func TestPrinter_Layout_PositionAt_Inverse(t *testing.T) {
	t.Parallel()

	full, err := os.ReadFile("../testdata/full.yaml")
	require.NoError(t, err)

	prefixed := style.New(
		lipgloss.NewStyle(),
		style.Set(kind.LiteralString, lipgloss.NewStyle().Transform(func(s string) string { return "val=" + s })),
	)

	tcs := map[string]struct {
		printer *printer.Printer
		view    func() *line.View
	}{
		"plain": {
			printer: testPrinter(),
			view: func() *line.View {
				return niceyaml.NewSourceFromString("a: 1\nb:\n  - x\n\n  - y # c\n").View()
			},
		},
		"wide runes and clusters": {
			printer: testPrinter(),
			view: func() *line.View {
				return niceyaml.NewSourceFromString("k: 日本語 e\u0301x 😀\nb: 1️⃣ x\n").View()
			},
		},
		"wrapped": {
			printer: testPrinter().With(printer.WithWrap(10)),
			view: func() *line.View {
				return niceyaml.NewSourceFromString("key: aaaa bbbb cccc 日本語日本語日本語\nb: 2\n").View()
			},
		},
		"brackets a transform adds": {
			printer: testPrinter().With(printer.WithWrap(8)),
			view: func() *line.View {
				view := niceyaml.NewSourceFromString("k: ab cd ef gh\nb: 2\n").View()
				view.AddOverlay(testOverlayHighlight,
					position.NewRange(position.New(0, 3), position.New(0, 5)),
					position.NewRange(position.New(0, 9), position.New(0, 14)),
				)

				return view
			},
		},
		"prefix a transform adds": {
			printer: printer.New(
				printer.WithGutter(printer.NoGutter),
				printer.WithContainerStyle(lipgloss.NewStyle()),
				printer.WithWrap(5),
				printer.WithStyles(prefixed),
			),
			view: func() *line.View {
				return niceyaml.NewSourceFromString("k: value\nb: other words\n").View()
			},
		},
		"annotations": {
			printer: testPrinter().With(printer.WithWrap(12)),
			view: func() *line.View {
				view := niceyaml.NewSourceFromString("key: aaaa bbbb cccc\nb: 2\n").View()
				view.Annotate(0, line.Annotation{Content: "above", Placement: line.Above})
				view.Annotate(0, line.Annotation{Content: "below", Placement: line.Below, Col: 12})
				view.Annotate(1, line.Annotation{Content: "last", Placement: line.Below})

				return view
			},
		},
		"full.yaml with a gutter": {
			printer: testPrinterWithGutter(printer.DefaultGutter).With(printer.WithWrap(40)),
			view: func() *line.View {
				return niceyaml.NewSourceFromString(string(full)).View()
			},
		},
		"clipped": {
			printer: testPrinter(),
			view: func() *line.View {
				view := niceyaml.NewSourceFromString("k: abcdefghijklmnopqrstuvwxyz\nb: 2\n").View()
				view.Annotate(0, line.Annotation{Content: "first", Placement: line.Below, Col: 5})
				view.Annotate(0, line.Annotation{Content: "second", Placement: line.Below, Col: 24})

				return view.Clip(8)
			},
		},
		"clipped with wide runes and brackets": {
			printer: testPrinter(),
			view: func() *line.View {
				view := niceyaml.NewSourceFromString("k: 日本語日本語 abcdefgh 日本語日本語\n").View()
				view.AddOverlay(testOverlayHighlight, position.NewRange(position.New(0, 12), position.New(0, 15)))

				return view.Clip(8)
			},
		},
		"clipped and wrapped": {
			printer: testPrinter().With(printer.WithWrap(8)),
			view: func() *line.View {
				view := niceyaml.NewSourceFromString("k: aaaa bbbb cccc dddd eeee ffff gggg\n").View()
				view.Annotate(0, line.Annotation{Content: "x", Placement: line.Below, Col: 20})

				return view.Clip(16)
			},
		},
		"full.yaml clipped with a gutter": {
			printer: testPrinterWithGutter(printer.DefaultGutter).With(printer.WithWrap(30)),
			view: func() *line.View {
				return niceyaml.NewSourceFromString(string(full)).View().Clip(20)
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := tc.view()
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
			for pos, r := range view.Lines().Runes() {
				if r == '\n' {
					continue
				}

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
		})
	}
}
