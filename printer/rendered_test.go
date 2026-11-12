package printer_test

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/style/theme"
)

func TestPrinter_Render(t *testing.T) {
	t.Parallel()

	twoLines := stringtest.LinesLF("a: 1", "b: 2")
	view := func(input string) func() *line.View {
		return func() *line.View { return niceyaml.NewSourceFromString(input).View() }
	}
	border := lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(1, 2)

	tcs := map[string]struct {
		view func() *line.View
		opts []printer.Option
		want []string
	}{
		"rows of a view": {
			view: view(twoLines),
			want: []string{"   1  a: 1", "   2  b: 2"},
		},
		"container frame stays out of the rows": {
			view: view(twoLines),
			opts: []printer.Option{printer.WithContainerStyle(border)},
			want: []string{"   1  a: 1", "   2  b: 2"},
		},
		"container width does not pad the rows": {
			view: view(twoLines),
			opts: []printer.Option{printer.WithContainerWidth(30)},
			want: []string{"   1  a: 1", "   2  b: 2"},
		},
		"container style width does not wrap the rows": {
			view: view(stringtest.LinesLF("a: one two three four", "b: 2")),
			opts: []printer.Option{printer.WithContainerStyle(lipgloss.NewStyle().Width(10))},
			want: []string{"   1  a: one two three four", "   2  b: 2"},
		},
		"container style height and alignment do not move the rows": {
			view: view(twoLines),
			opts: []printer.Option{printer.WithContainerStyle(
				lipgloss.NewStyle().Width(40).Height(6).Align(lipgloss.Center, lipgloss.Bottom),
			)},
			want: []string{"   1  a: 1", "   2  b: 2"},
		},
		"box does not pad the rows": {
			view: view(stringtest.LinesLF("a: 1", "longer: 2")),
			opts: []printer.Option{printer.WithBox(true)},
			want: []string{"   1  a: 1", "   2  longer: 2"},
		},
		// The frame takes 6 of the 24 cells and the gutter takes 6, so
		// the content wraps to 12 cells.
		"wrapped line with annotations inside a frame": {
			view: func() *line.View {
				v := niceyaml.NewSourceFromString(stringtest.LinesLF("a: one two three", "b: 2")).View()
				v.AnnotateLine(0, yamltest.ErrorAnnotation(position.NewSpan(3, 6), "bad"))
				v.AnnotateLine(1, line.Annotation{Content: "a note", Placement: line.Above})

				return v
			},
			opts: []printer.Option{printer.WithContainerStyle(border), printer.WithWrapWidth(24)},
			want: []string{
				"   1  a: one two",
				"         ^^^ bad",
				"   -  three",
				"      a note",
				"   2  b: 2",
			},
		},
		"slice keeps its lines": {
			view: func() *line.View {
				return niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "b: 2", "c: 3")).View().
					Slice(position.NewSpan(2, 3))
			},
			want: []string{"   3  c: 3"},
		},
		"empty view has no rows": {
			view: view(""),
			opts: []printer.Option{printer.WithContainerStyle(border), printer.WithContainerWidth(20)},
			want: []string{},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := testPrinterWithGutter(printer.DefaultGutter()).With(tc.opts...)
			v := tc.view()
			got := p.Render(v)

			assert.Equal(t, tc.want, got.Rows())

			// The rows, the layout, and the framed string agree with the
			// separate calls for the same view.
			assert.Equal(t, p.Layout(v), got.Layout())
			assert.Len(t, got.Rows(), got.Layout().Rows())

			framed := got.String()
			assert.Equal(t, p.Print(v), framed)

			// String pads and frames a copy of the rows, so the rows and
			// a second String match the first.
			assert.Equal(t, tc.want, got.Rows())
			assert.Equal(t, framed, got.String())
		})
	}
}

func TestPrinter_Render_LayoutRows(t *testing.T) {
	t.Parallel()

	// Inside a frame, row n of Print is not row n of the layout. Row n of
	// the rendered rows is, so each column lands on the row that shows it.
	input := "key: one two three four five six seven"
	view := niceyaml.NewSourceFromString(input).View()
	view.AnnotateLine(0, line.Annotation{Content: "a note", Placement: line.Above})

	p := printer.New(
		printer.WithStyles(theme.Charm()),
		printer.WithContainerStyle(lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(1)),
		printer.WithWrapWidth(24),
	)

	got := p.Render(view)
	layout := got.Layout()

	require.Greater(t, layout.LineRows(0), 2)
	require.Greater(t, lipgloss.Height(got.String()), layout.Rows())
	assert.Equal(t, p.Print(view), got.String())

	assertRowOfRunes(t, layout, input, got.Rows(), func(r rune) bool { return r != ' ' })
}

func TestRendered_Rows(t *testing.T) {
	t.Parallel()

	t.Run("returns a copy", func(t *testing.T) {
		t.Parallel()

		view := niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "b: 2")).View()
		got := testPrinterWithGutter(printer.DefaultGutter()).Render(view)

		rows := got.Rows()
		rows[0] = "changed"

		assert.Equal(t, []string{"   1  a: 1", "   2  b: 2"}, got.Rows())
		assert.Equal(t, stringtest.JoinLF("   1  a: 1", "   2  b: 2"), got.String())
	})

	t.Run("zero value", func(t *testing.T) {
		t.Parallel()

		var got printer.Rendered

		assert.Empty(t, got.Rows())
		assert.Equal(t, 0, got.Layout().Rows())
		assert.Empty(t, got.String())
	})
}
