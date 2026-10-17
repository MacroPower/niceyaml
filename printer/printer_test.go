package printer_test

import (
	"cmp"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"
	"github.com/goccy/go-yaml/lexer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/diff"
	"go.jacobcolvin.com/niceyaml/finder"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/kind"
	"go.jacobcolvin.com/niceyaml/style/theme"
)

// testOverlayHighlight is a custom kind.Kind constant for test highlights.
const testOverlayHighlight kind.Kind = "testOverlayHighlight"

// bracketStyle returns a style that wraps content in open and end for
// easy verification.
func bracketStyle(open, end string) lipgloss.Style {
	return lipgloss.NewStyle().Transform(func(s string) string { return open + s + end })
}

// tenThousandLines holds 10000 lines, so the number of the line after them
// takes five digits.
var tenThousandLines = strings.Repeat("k: v\n", 10000)

// testPrinter returns a printer without styles or padding for predictable output.
func testPrinter() *printer.Printer {
	return testPrinterWithGutter(printer.NoGutter)
}

// testPrinterWithGutter returns a printer without styles but with a custom gutter.
func testPrinterWithGutter(gutter printer.Gutter) *printer.Printer {
	return yamltest.NewPlainPrinter(
		printer.WithStyles(style.New(
			lipgloss.NewStyle(),
			style.Set(testOverlayHighlight, bracketStyle("[", "]")),
		)),
		printer.WithGutter(gutter),
	)
}

// testPrinterWithStyles returns a printer without a gutter or padding whose
// styles are the empty style with opts applied.
func testPrinterWithStyles(opts ...style.Option) *printer.Printer {
	return yamltest.NewPlainPrinter(printer.WithStyles(style.New(lipgloss.NewStyle(), opts...)))
}

// assertRowsFit asserts that no row of printed is wider than width.
func assertRowsFit(t *testing.T, printed string, width int) {
	t.Helper()

	for i, row := range strings.Split(printed, "\n") {
		assert.LessOrEqual(t, lipgloss.Width(row), width, "row %d: %q", i, row)
	}
}

// assertRowsFitClusters asserts that no row of rows is wider than width,
// unless the row holds a lone cluster wider than the width.
func assertRowsFitClusters(t *testing.T, rows []string, width int) {
	t.Helper()

	for i, row := range rows {
		if cluster, _ := ansi.FirstGraphemeCluster(row, ansi.GraphemeWidth); cluster != row {
			assert.LessOrEqual(t, lipgloss.Width(row), width, "row %d: %q", i, row)
		}
	}
}

func TestPrinter_AddStyleToRange(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  string
		rng   position.Range
	}{
		"partial token - middle of value (0-indexed)": {
			input: "key: value",
			want:  "key: va[lue]",
			rng:   yamltest.LineRange(0, 7, 10),
		},
		"partial token - start of value (0-indexed)": {
			input: "key: value",
			want:  "key: [val]ue",
			rng:   yamltest.LineRange(0, 5, 8),
		},
		"full token (0-indexed)": {
			input: "key: value",
			want:  "key: [value]",
			rng:   yamltest.LineRange(0, 5, 10),
		},
		"first character (line 0, col 0)": {
			input: "key: value",
			want:  "[k]ey: value",
			rng:   yamltest.LineRange(0, 0, 1),
		},
		"multi-line range (0-indexed)": {
			input: stringtest.JoinLF(
				"first: 1",
				"second: 2",
			),
			want: stringtest.JoinLF(
				"first: [1]",
				"[second][:][ ]2",
			),
			rng: position.NewRange(position.New(0, 7), position.New(1, 8)),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := niceyaml.NewSourceFromTokens(lexer.Tokenize(tc.input)).View()
			view.AddOverlay(testOverlayHighlight, tc.rng)

			assert.Equal(t, tc.want, testPrinter().Print(view))
		})
	}
}

func TestPrinter_MarksRangeUnderHighlight(t *testing.T) {
	t.Parallel()

	// A search highlight blends onto a line that a bound error marks. The
	// carets below the line still mark the range of the error alone, in
	// whichever order the view gets the two.
	tcs := map[string]struct {
		source    string
		key       string
		highlight position.Range
		want      string
	}{
		"highlight on the key": {
			source:    stringtest.LinesLF("key: value", "name: other value"),
			key:       "name",
			highlight: yamltest.LineRange(1, 0, 4),
			want: stringtest.JoinLF(
				"key: value",
				"name: other value",
				"      ^^^^^^^^^^^",
			),
		},
		"empty value": {
			source: stringtest.LinesLF(
				"a: 1",
				"b:",
				"c: 2",
			),
			key:       "b",
			highlight: yamltest.LineRange(1, 0, 1),
			want: stringtest.JoinLF(
				"a: 1",
				"b:",
				"  ^",
				"c: 2",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.source)
			bound := yamltest.Bind(t, source, yamltest.ErrorAt("", tc.key))

			se := yamltest.RequireSourceError(t, bound)

			highlighted := source.View()
			highlighted.BlendOverlay(kind.GenericHighlight, tc.highlight)
			se.Annotate(highlighted)

			annotated := source.View()
			se.Annotate(annotated)
			annotated.BlendOverlay(kind.GenericHighlight, tc.highlight)

			p := testPrinter()

			assert.Equal(t, tc.want, p.Print(highlighted), "highlight before the annotation")
			assert.Equal(t, tc.want, p.Print(annotated), "annotation before the highlight")
		})
	}
}

func TestPrinter_CRLF(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input       string
		want        string
		wantOverlay string
	}{
		"mapping": {
			input:       stringtest.LinesCRLF("a: b", "c: d"),
			want:        stringtest.JoinLF("a: b", "c: d"),
			wantOverlay: stringtest.JoinLF("[a][:][ ][b]", "[c][:][ ][d]"),
		},
		"blank line": {
			input: stringtest.LinesCRLF(
				"a: b",
				"",
				"c: d",
			),
			want:        stringtest.JoinLF("a: b", "", "c: d"),
			wantOverlay: stringtest.JoinLF("[a][:][ ][b]", "", "[c][:][ ][d]"),
		},
		// The lexer ends the comment token with the CR and starts the next
		// token with the LF.
		"comment ending in CR": {
			input:       stringtest.LinesCRLF("a: b # c", "d: e"),
			want:        stringtest.JoinLF("a: b # c", "d: e"),
			wantOverlay: stringtest.JoinLF("[a][:][ ][b][ ][# c]", "[d][:][ ][e]"),
		},
		// The lexer gives the CRLF after a quoted value a token of its own.
		"line ending token": {
			input:       stringtest.LinesCRLF("a: 'x'", "b: 'y'"),
			want:        stringtest.JoinLF("a: 'x'", "b: 'y'"),
			wantOverlay: stringtest.JoinLF("[a][:][ ]['x']", "[b][:][ ]['y']"),
		},
		"block scalar": {
			input: stringtest.LinesCRLF(
				"a: |",
				"  x",
				"  y",
			),
			want:        stringtest.JoinLF("a: |", "  x", "  y"),
			wantOverlay: stringtest.JoinLF("[a][:][ ][|]", "[  ][x]", "[  ][y]"),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := testPrinter()
			view := niceyaml.NewSourceFromString(tc.input).View()

			got := p.Print(view)
			assert.Equal(t, tc.want, got)

			// Highlight every visible column of every line.
			for i, ln := range view.All() {
				view.AddOverlay(testOverlayHighlight, yamltest.LineRange(i, 0, ln.Width()))
			}

			gotOverlay := p.Print(view)
			assert.Equal(t, tc.wantOverlay, gotOverlay)

			for _, out := range []string{got, gotOverlay} {
				assert.NotContains(t, out, "\r")
				assert.NotContains(t, out, "␍", "CR control picture")
			}
		})
	}
}

func TestNewPrinter(t *testing.T) {
	t.Parallel()

	// The default gutter adds line numbers, and the default container
	// adds nothing around the rows.
	p := printer.New(printer.WithStyles(style.Styles{}))
	view := niceyaml.NewSourceFromTokens(lexer.Tokenize("key: value")).View()

	assert.Equal(t, "   1  key: value", p.Print(view))
	assert.Equal(t, lipgloss.NewStyle(), p.ContainerStyle())

	// The default styles set no surface, so the output paints no
	// background and each row ends where its content ends.
	src := niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "b: a longer value", "c: [3]"))
	got := printer.New().Print(src.View())

	assert.NotContains(t, got, "48;")
	assert.Equal(t, stringtest.JoinLF("   1  a: 1", "   2  b: a longer value", "   3  c: [3]"), ansi.Strip(got))
}

func TestWithBox(t *testing.T) {
	t.Parallel()

	src := niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "b: longer", "c: 3"))
	border := lipgloss.NewStyle().Border(lipgloss.NormalBorder())

	tcs := map[string]struct {
		opts []printer.Option
		want []string
	}{
		"plain container leaves each row as wide as its content": {
			want: []string{"a: 1", "b: longer", "c: 3"},
		},
		"box pads every row to the widest and one cell more": {
			opts: []printer.Option{printer.WithBox()},
			want: []string{"a: 1      ", "b: longer ", "c: 3      "},
		},
		"box pads every row to the container width": {
			opts: []printer.Option{printer.WithBox(), printer.WithContainerWidth(12)},
			want: []string{"a: 1        ", "b: longer   ", "c: 3        "},
		},
		"later container style replaces the box": {
			opts: []printer.Option{printer.WithBox(), printer.WithContainerStyle(lipgloss.NewStyle())},
			want: []string{"a: 1", "b: longer", "c: 3"},
		},
		"later box replaces the container style": {
			opts: []printer.Option{printer.WithContainerStyle(border), printer.WithBox()},
			want: []string{"a: 1      ", "b: longer ", "c: 3      "},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := testPrinter().With(tc.opts...)

			assert.Equal(t, stringtest.JoinLF(tc.want...), p.Print(src.View()))
		})
	}

	t.Run("box takes the text style of the final styles", func(t *testing.T) {
		t.Parallel()

		first := style.New(lipgloss.NewStyle().Foreground(lipgloss.Color("#ff0000")))
		second := style.New(lipgloss.NewStyle().Foreground(lipgloss.Color("#00ff00")))

		for _, p := range []*printer.Printer{
			printer.New(printer.WithStyles(first), printer.WithBox()),
			printer.New(printer.WithBox(), printer.WithStyles(first)),
		} {
			assert.Equal(t, first.Style(kind.Text).PaddingRight(1), p.ContainerStyle())

			// A copy with other styles takes their text style.
			assert.Equal(t, second.Style(kind.Text).PaddingRight(1),
				p.With(printer.WithStyles(second)).ContainerStyle())
		}
	})

	t.Run("box fills with the surface of the styles", func(t *testing.T) {
		t.Parallel()

		surface := style.New(
			lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff")),
			style.WithSurface(lipgloss.Color("#202020")),
			style.Set(kind.GenericHighlight, lipgloss.NewStyle().Background(lipgloss.Color("#ff00ff"))),
		)
		view := src.View()
		view.AddOverlay(kind.GenericHighlight, yamltest.LineRange(1, 3, 9))

		painted := printer.New(printer.WithStyles(surface), printer.WithBox()).Print(view)
		plain := printer.New(printer.WithStyles(surface.With(style.WithSurface(nil))), printer.WithBox()).Print(view)

		// Both draw the same box, and only the painted one fills it.
		assert.Equal(t, ansi.Strip(painted), ansi.Strip(plain))
		assert.Contains(t, painted, "48;2;32;32;32")
		assert.NotContains(t, plain, "48;2;32;32;32")

		// The highlight keeps its fill without the surface.
		assert.Contains(t, plain, "48;2;255;0;255")
	})
}

func TestPrinter_PrintSlice(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		first: 1
		second: 2
		third: 3
		fourth: 4
		fifth: 5
	`)

	tcs := map[string]struct {
		gutter printer.Gutter // Nil for no gutter.
		want   string
		spans  position.Spans
	}{
		// Without a span, the view holds no line.
		"no span": {
			want: "",
		},
		"full range": {
			spans: position.Spans{position.NewSpan(0, 5)},
			want: stringtest.JoinLF(
				"first: 1",
				"second: 2",
				"third: 3",
				"fourth: 4",
				"fifth: 5",
			),
		},
		"full range with line numbers": {
			spans:  position.Spans{position.NewSpan(0, 5)},
			gutter: printer.LineNumberGutter,
			want: stringtest.JoinLF(
				"   1 first: 1",
				"   2 second: 2",
				"   3 third: 3",
				"   4 fourth: 4",
				"   5 fifth: 5",
			),
		},
		"bounded middle": {
			spans: position.Spans{position.NewSpan(1, 4)},
			want: stringtest.JoinLF(
				"second: 2",
				"third: 3",
				"fourth: 4",
			),
		},
		"bounded middle with line numbers": {
			spans:  position.Spans{position.NewSpan(1, 4)},
			gutter: printer.LineNumberGutter,
			want: stringtest.JoinLF(
				"   2 second: 2",
				"   3 third: 3",
				"   4 fourth: 4",
			),
		},
		"from start": {
			spans: position.Spans{position.NewSpan(0, 2)},
			want: stringtest.JoinLF(
				"first: 1",
				"second: 2",
			),
		},
		"to end": {
			spans: position.Spans{position.NewSpan(3, 5)},
			want: stringtest.JoinLF(
				"fourth: 4",
				"fifth: 5",
			),
		},
		"single line": {
			spans: position.Spans{position.NewSpan(2, 3)},
			want:  "third: 3",
		},
		"single line with line numbers": {
			spans:  position.Spans{position.NewSpan(2, 3)},
			gutter: printer.LineNumberGutter,
			want:   "   3 third: 3",
		},
		"empty result": {
			spans: position.Spans{position.NewSpan(10, 21)},
			want:  "",
		},
		"two disjoint spans": {
			spans: position.Spans{
				position.NewSpan(0, 1),
				position.NewSpan(3, 5),
			},
			want: stringtest.JoinLF(
				"first: 1",
				"fourth: 4",
				"fifth: 5",
			),
		},
		"two disjoint spans with line numbers": {
			spans: position.Spans{
				position.NewSpan(0, 1),
				position.NewSpan(3, 5),
			},
			gutter: printer.LineNumberGutter,
			want: stringtest.JoinLF(
				"   1 first: 1",
				"   4 fourth: 4",
				"   5 fifth: 5",
			),
		},
		"three spans": {
			spans: position.Spans{
				position.NewSpan(0, 1),
				position.NewSpan(2, 3),
				position.NewSpan(4, 5),
			},
			want: stringtest.JoinLF(
				"first: 1",
				"third: 3",
				"fifth: 5",
			),
		},
		"adjacent spans": {
			spans: position.Spans{
				position.NewSpan(0, 2),
				position.NewSpan(2, 4),
			},
			want: stringtest.JoinLF(
				"first: 1",
				"second: 2",
				"third: 3",
				"fourth: 4",
			),
		},
		"empty span first": {
			spans: position.Spans{position.NewSpan(0, 0), position.NewSpan(0, 2)},
			want:  stringtest.JoinLF("first: 1", "second: 2"),
		},
		"out of range span first": {
			spans: position.Spans{position.NewSpan(5, 9), position.NewSpan(0, 1)},
			want:  "first: 1",
		},
		"empty span between": {
			spans: position.Spans{position.NewSpan(0, 1), position.NewSpan(1, 1), position.NewSpan(1, 2)},
			want:  stringtest.JoinLF("first: 1", "second: 2"),
		},
		"empty span last": {
			spans: position.Spans{position.NewSpan(0, 2), position.NewSpan(2, 2)},
			want:  stringtest.JoinLF("first: 1", "second: 2"),
		},
		"only empty spans": {
			spans: position.Spans{position.NewSpan(0, 0), position.NewSpan(2, 2)},
			want:  "",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := niceyaml.NewSourceFromString(input).View().Slice(tc.spans...)
			p := testPrinterWithGutter(tc.gutter)

			got := p.Print(view)
			assert.Equal(t, tc.want, got)

			// Each line the view holds takes one row of the layout, and a
			// view that holds none still prints one empty row.
			l := p.Layout(view)
			assert.Len(t, layoutRows(l), view.Count())
			assert.Equal(t, view.Count(), l.Rows())
			assert.Len(t, strings.Split(got, "\n"), max(1, l.Rows()))
		})
	}
}

func TestPrinter_PrintTokenDiff(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		gutter   printer.Gutter // Nil for DefaultGutter.
		before   string
		after    string
		want     string
		overlays []position.Range
		width    int
	}{
		"no changes": {
			before: "key: value\n",
			after:  "key: value\n",
			want:   "   1  key: value",
		},
		"simple addition": {
			before: "key: value\n",
			after: stringtest.LinesLF(
				"key: value",
				"new: line",
			),
			want: stringtest.JoinLF(
				"   1  key: value",
				"   2 +new: line",
			),
		},
		"simple deletion": {
			before: stringtest.LinesLF(
				"key: value",
				"old: line",
			),
			after: "key: value\n",
			want: stringtest.JoinLF(
				"   1  key: value",
				"   2 -old: line",
			),
		},
		"modification": {
			before: "key: old\n",
			after:  "key: new\n",
			want: stringtest.JoinLF(
				"   1 -key: old",
				"   1 +key: new",
			),
		},
		"addition with context": {
			// Equal lines show afterLine, inserted line shows afterLine.
			before: stringtest.LinesLF(
				"line1: a",
				"line3: c",
			),
			after: stringtest.LinesLF(
				"line1: a",
				"line2: b",
				"line3: c",
			),
			want: stringtest.JoinLF(
				"   1  line1: a",
				"   2 +line2: b",
				"   3  line3: c",
			),
		},
		"deletion with context": {
			// Equal lines show afterLine, deleted line shows beforeLine.
			before: stringtest.LinesLF(
				"line1: a",
				"line2: b",
				"line3: c",
			),
			after: stringtest.LinesLF(
				"line1: a",
				"line3: c",
			),
			want: stringtest.JoinLF(
				"   1  line1: a",
				"   2 -line2: b",
				"   2  line3: c",
			),
		},
		"modifications around an equal line": {
			before: stringtest.LinesLF(
				"line1: a",
				"line2: b",
				"line3: c",
			),
			after: stringtest.LinesLF(
				"line1: x",
				"line2: b",
				"line3: y",
			),
			want: stringtest.JoinLF(
				"   1 -line1: a",
				"   1 +line1: x",
				"   2  line2: b",
				"   3 -line3: c",
				"   3 +line3: y",
			),
		},
		"multiline yaml modification": {
			before: stringtest.LinesLF(
				"apiVersion: v1",
				"kind: Pod",
				"metadata:",
				"  name: test",
			),
			after: stringtest.LinesLF(
				"apiVersion: v1",
				"kind: Pod",
				"metadata:",
				"  name: modified",
				"  labels:",
				"    app: test",
			),
			want: stringtest.JoinLF(
				"   1  apiVersion: v1",
				"   2  kind: Pod",
				"   3  metadata:",
				"   4 -  name: test",
				"   4 +  name: modified",
				"   5 +  labels:",
				"   6 +    app: test",
			),
		},
		"multiple scattered changes": {
			// Delete shows beforeLine (2), insert shows afterLine (2).
			before: stringtest.LinesLF(
				"a: 1",
				"b: 2",
				"c: 3",
				"d: 4",
			),
			after: stringtest.LinesLF(
				"a: 1",
				"b: changed",
				"c: 3",
				"d: 4",
				"e: 5",
			),
			want: stringtest.JoinLF(
				"   1  a: 1",
				"   2 -b: 2",
				"   2 +b: changed",
				"   3  c: 3",
				"   4  d: 4",
				"   5 +e: 5",
			),
		},
		"both empty": {
			before: "",
			after:  "",
			want:   "",
		},
		"empty before, content after": {
			before: "",
			after:  "key: value\n",
			want:   "   1 +key: value",
		},
		"content before, empty after": {
			before: "key: value\n",
			after:  "",
			want:   "   1 -key: value",
		},
		"CRLF line endings": {
			before: stringtest.LinesCRLF("key: value", "old: line"),
			after:  stringtest.LinesCRLF("key: value", "new: line"),
			want: stringtest.JoinLF(
				"   1  key: value",
				"   2 -old: line",
				"   2 +new: line",
			),
		},
		// A prefixGutter declares the width of its widest prefix, so the
		// printer pads the narrower ones.
		"custom inserted prefix": {
			gutter: prefixGutter{inserted: ">>", deleted: "-", equal: " "},
			before: "key: old\n",
			after: stringtest.LinesLF(
				"key: old",
				"new: line",
			),
			want: stringtest.JoinLF(
				"  key: old",
				">>new: line",
			),
		},
		"custom deleted prefix": {
			gutter: prefixGutter{inserted: "+", deleted: "<<", equal: " "},
			before: stringtest.LinesLF(
				"key: old",
				"old: line",
			),
			after: "key: old\n",
			want: stringtest.JoinLF(
				"  key: old",
				"<<old: line",
			),
		},
		"both custom prefixes": {
			gutter: prefixGutter{inserted: "ADD:", deleted: "DEL:", equal: "    "},
			before: "key: old\n",
			after:  "key: new\n",
			want: stringtest.JoinLF(
				"DEL:key: old",
				"ADD:key: new",
			),
		},
		"no gutter": {
			gutter: printer.NoGutter,
			before: "a: 1\n",
			after:  "a: 2\n",
			want: stringtest.JoinLF(
				"a: 1",
				"a: 2",
			),
		},
		"diff gutter keeps a deleted line inline": {
			gutter: printer.DiffGutter,
			before: stringtest.LinesLF(
				"a: 1",
				"b: 2",
				"c: 3",
			),
			after: stringtest.LinesLF(
				"a: 1",
				"c: 3",
			),
			want: stringtest.JoinLF(
				" a: 1",
				"-b: 2",
				" c: 3",
			),
		},
		"diff gutter shows the delete before the insert": {
			gutter: printer.DiffGutter,
			before: "key: old\n",
			after:  "key: new\n",
			want: stringtest.JoinLF(
				"-key: old",
				"+key: new",
			),
		},
		"multi-character prefixes with context": {
			gutter: prefixGutter{inserted: "[+]", deleted: "[-]", equal: "   "},
			before: stringtest.LinesLF(
				"line1: a",
				"line2: b",
				"line3: c",
			),
			after: stringtest.LinesLF(
				"line1: a",
				"line2: x",
				"line3: c",
			),
			want: stringtest.JoinLF(
				"   line1: a",
				"[-]line2: b",
				"[+]line2: x",
				"   line3: c",
			),
		},
		// Line 1 is the inserted line. The overlay covers "dddd", which
		// wraps to the third row.
		"overlay on a continuation row": {
			gutter:   printer.DiffGutter,
			before:   "key: x\n",
			after:    "key: aaaa bbbb cccc dddd\n",
			overlays: []position.Range{yamltest.LineRange(1, 20, 24)},
			width:    13,
			want: stringtest.JoinLF(
				"-key: x",
				"+key: aaaa",
				" bbbb cccc",
				" [dddd]",
			),
		},
		// Each ESC byte prints as a control picture one column wide.
		"wrapped escape sequence": {
			gutter: printer.DiffGutter,
			before: "b: x\n",
			after:  "b: \"\x1b[31mred\x1b[0m and more words here\"\n",
			width:  13,
			want: stringtest.JoinLF(
				"-b: x",
				"+b:",
				" \"␛[31mred␛[0",
				" m and more",
				" words here\"",
			),
		},
		// Each tab prints as a control picture, so a wrap point cannot drop
		// the run as whitespace.
		"wrapped tab run": {
			gutter: printer.DiffGutter,
			before: "b: x\n",
			after:  "b: \"x\t\t\t\t\t\t\t\t\t\ty\tz\tw\"\n",
			width:  13,
			want: stringtest.JoinLF(
				"-b: x",
				"+b:",
				" \"x␉␉␉␉␉␉␉␉␉␉",
				" y␉z␉w\"",
			),
		},
		"diff lines wrap correctly": {
			gutter: printer.DiffGutter,
			before: "key: short\n",
			after:  "key: this is a very long value that should wrap\n",
			width:  30,
			want: stringtest.JoinLF(
				"-key: short",
				"+key: this is a very long",
				" value that should wrap",
			),
		},
		"wrapped diff continuation": {
			gutter: printer.DiffGutter,
			before: "key: original value\n",
			after:  "key: new very long value that definitely wraps\n",
			width:  25,
			want: stringtest.JoinLF(
				"-key: original value",
				"+key: new very long value",
				" that definitely wraps",
			),
		},
		"modification with wrap": {
			gutter: printer.DiffGutter,
			before: "name: old-hyphenated-name-value\n",
			after:  "name: new-hyphenated-name-value\n",
			width:  20,
			want: stringtest.JoinLF(
				"-name: old-",
				" hyphenated-name-",
				" value",
				"+name: new-",
				" hyphenated-name-",
				" value",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			gutter := tc.gutter
			if gutter == nil {
				gutter = printer.DefaultGutter
			}

			p := testPrinterWithGutter(gutter).With(printer.WithWrapWidth(tc.width))

			view := yamltest.Diff(tc.before, tc.after).Unified()
			view.AddOverlay(testOverlayHighlight, tc.overlays...)

			got := p.Print(view)
			assert.Equal(t, tc.want, got)

			if tc.width > 0 {
				assertRowsFit(t, got, tc.width)
			}
		})
	}
}

func TestPrinter_WordWrap(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		gutter printer.Gutter // Nil for no gutter.
		rows   map[int]int    // Column of line 0 to the row that shows it.
		input  string
		want   string
		width  int
	}{
		"simple wrap": {
			input: "key: this is a very long value that should wrap",
			width: 20,
			want: stringtest.JoinLF(
				"key: this is a very",
				"long value that",
				"should wrap",
			),
		},
		"wrap on slash": {
			input: "path: /usr/local/bin/something",
			width: 20,
			want: stringtest.JoinLF(
				"path: /usr/local/",
				"bin/something",
			),
		},
		"wrap on hyphen": {
			input: "name: very-long-hyphenated-name",
			width: 20,
			want: stringtest.JoinLF(
				"name: very-long-",
				"hyphenated-name",
			),
		},
		"short content no wrap": {
			input: "key: value",
			width: 50,
			want:  "key: value",
		},
		"multi-line content": {
			input: stringtest.JoinLF(
				"key: value",
				"another: long value that should wrap here",
			),
			width: 20,
			want: stringtest.JoinLF(
				"key: value",
				"another: long value",
				"that should wrap",
				"here",
			),
		},
		"wrapped line continuation marker": {
			input:  "key: this is a very long value",
			width:  22,
			gutter: printer.LineNumberGutter,
			// Wraps at word boundaries within width.
			// Width 22 - 5 (line number gutter) = 17 for content.
			want: stringtest.JoinLF(
				"   1 key: this is a",
				"   - very long value",
			),
		},
		"multiple wrapped lines": {
			input: stringtest.JoinLF(
				"first: short",
				"second: this is a very long line that wraps",
			),
			width:  30,
			gutter: printer.LineNumberGutter,
			// First line fits, second line wraps.
			// Width 30 - 5 (line number gutter) = 25 for content.
			want: stringtest.JoinLF(
				"   1 first: short",
				"   2 second: this is a very",
				"   - long line that wraps",
			),
		},
		// A width of zero disables wrapping.
		"zero width with NoGutter": {
			input:  "key: this is a very long value that should not wrap",
			width:  0,
			gutter: printer.NoGutter,
			want:   "key: this is a very long value that should not wrap",
		},
		"zero width with LineNumberGutter": {
			input:  "key: this is a very long value that should not wrap",
			width:  0,
			gutter: printer.LineNumberGutter,
			want:   "   1 key: this is a very long value that should not wrap",
		},
		"zero width with DefaultGutter": {
			input:  "key: this is a very long value that should not wrap",
			width:  0,
			gutter: printer.DefaultGutter,
			want:   "   1  key: this is a very long value that should not wrap",
		},
		"line numbers on a multi-line value": {
			gutter: printer.LineNumberGutter,
			input: stringtest.JoinLF(
				"key: |",
				"  line1",
				"  line2",
			),
			want: stringtest.JoinLF(
				"   1 key: |",
				"   2   line1",
				"   3   line2",
			),
		},
		"wide characters count two cells": {
			input: "k: 日本語",
			want:  "k: 日本語",
			rows:  map[int]int{5: 0},
		},

		// The wrap emits empty rows for the word after a run of breakpoints
		// longer than the width, and for a word wider than the width after
		// leading spaces. Those rows hold no text, so they go.
		"word wider than the width after leading spaces": {
			input: stringtest.LinesLF(
				"k: |",
				"    abcdefghijkl",
			),
			width: 5,
			want:  stringtest.JoinLF("k: |", "abcde", "fghij", "kl"),
		},
		"hyphenated value at one column": {
			input: "key: long-word-here",
			width: 1,
			want:  stringtest.JoinLF(strings.Split("key:long-word-here", "")...),
		},
		"url at two columns": {
			input: "u: https://x",
			width: 2,
			want:  stringtest.JoinLF("u:", "ht", "tp", "s:", "//", "x"),
		},
		"run of hyphens longer than the width": {
			input: "x: " + strings.Repeat("-", 25) + "abc",
			width: 10,
			want:  stringtest.JoinLF("x: -------", "----------", "--------ab", "c"),
		},

		// A row that fills the width and then meets a space and a breakpoint
		// drops the space at the break, as it drops the space at any other
		// break, rather than showing it on a row of its own.
		"hyphen after a full row": {
			gutter: printer.DefaultGutter,
			input:  "key: value -x",
			width:  16,
			want:   stringtest.JoinLF("   1  key: value", "   -  -x"),
			rows:   map[int]int{9: 0, 10: 0, 11: 1, 12: 1},
		},
		"slash after a full row": {
			input: "key: value /x",
			width: 10,
			want:  stringtest.JoinLF("key: value", "/x"),
			rows:  map[int]int{9: 0, 10: 0, 11: 1, 12: 1},
		},
		"two spaces before a hyphen": {
			input: "key: value  -x",
			width: 10,
			want:  stringtest.JoinLF("key: value", "-x"),
			rows:  map[int]int{10: 0, 11: 0, 12: 1},
		},
		"flags and a path": {
			input: "cmd: run --verbose --output /tmp/x",
			width: 8,
			want:  stringtest.JoinLF("cmd: run", "--verbos", "e --", "output /", "tmp/x"),
			rows:  map[int]int{7: 0, 8: 0, 9: 1},
		},
		"space before a breakpoint past a hard wrap": {
			gutter: printer.DefaultGutter,
			input:  "k: aa/-/- /b",
			width:  9,
			want:   stringtest.JoinLF("   1  k:", "   -  aa/", "   -  -/-", "   -  /b"),
			rows:   map[int]int{8: 2, 9: 2, 10: 3, 11: 3},
		},
		"space before a breakpoint past a wide cluster": {
			input: "k: 中b-   // /c",
			width: 6,
			want:  stringtest.JoinLF("k: 中b", "-   //", "/c"),
			rows:  map[int]int{10: 1, 11: 1, 12: 2, 13: 2},
		},

		// The printer escapes each control character to one rune, so the
		// escaped text keeps one rune per source column and the wrap falls
		// at the same column in both.
		"tabs and control characters keep columns aligned": {
			input: "k: \"\tx\x1by zz ww\"",
			width: 10,
			want: stringtest.JoinLF(
				"k: \"␉x␛y",
				"zz ww\"",
			),
			rows: map[int]int{4: 0, 6: 0, 8: 0, 9: 1, 14: 1},
		},
		// The wrapper drops a Unicode space at a break as it drops an ASCII
		// one, so each separator column belongs to the row before the break
		// and the next word starts the next row.
		"ideographic spaces dropped at each break": {
			input: "k: aaaa\u3000bbbb\u3000cccc\u3000dddd",
			width: 7,
			want:  stringtest.JoinLF("k: aaaa", "bbbb", "cccc", "dddd"),
			rows:  map[int]int{6: 0, 7: 0, 8: 1, 12: 1, 13: 2, 17: 2, 18: 3},
		},
		"em spaces dropped at each break": {
			input: "k: aaaa\u2003bbbb\u2003cccc\u2003dddd",
			width: 7,
			want:  stringtest.JoinLF("k: aaaa", "bbbb", "cccc", "dddd"),
			rows:  map[int]int{6: 0, 7: 0, 8: 1, 12: 1, 13: 2, 17: 2, 18: 3},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := niceyaml.NewSourceFromString(tc.input).View()
			p := testPrinterWithGutter(tc.gutter).With(printer.WithWrapWidth(tc.width))

			got := p.Print(view)
			assert.Equal(t, tc.want, got)

			// The layout counts the rows Print writes and measures the
			// widest, and it names the row that shows each column.
			l := p.Layout(view)
			assertLayoutMeasures(t, l, got)

			for col, want := range tc.rows {
				assert.Equal(t, want, l.RowOf(position.New(0, col)), "column %d", col)
			}
		})
	}

	t.Run("no row shows the gutter alone", func(t *testing.T) {
		t.Parallel()

		// The gutter leaves one column of content.
		view := niceyaml.NewSourceFromString("image: nginx-alpine").View()
		p := testPrinterWithGutter(printer.DefaultGutter).With(printer.WithWrapWidth(7))

		rows := strings.Split(p.Print(view), "\n")
		for _, row := range rows {
			assert.NotEmpty(t, strings.TrimSpace(strings.TrimPrefix(row, "   -")), "%q", rows)
		}

		assert.Len(t, rows, len("image:nginx-alpine"))
		assert.Equal(t, []int{len(rows)}, layoutRows(p.Layout(view)))
	})
}

func TestPrinter_WordWrap_RowsFit(t *testing.T) {
	t.Parallel()

	const (
		short = "key: this is a long value"
		long  = "name: some-very-long-name-that-will-wrap-around-the-viewport-width-for-sure\n"
	)

	tcs := map[string]struct {
		input string
		width int
		want  int // Width every row fits.
	}{
		// Only a width of 0 turns wrapping off. A positive width at or below
		// the gutter width still wraps, at one column of content per row.
		"width below the gutter":    {input: short, width: 3, want: 6},
		"width equal to the gutter": {input: short, width: 5, want: 6},
		"width one past the gutter": {input: short, width: 6, want: 6},
		// A breakpoint character just past the wrap width can leave a row
		// wider than the width, and every row must still fit.
		"breakpoint past a width of 12": {input: long, width: 12, want: 12},
		"breakpoint past a width of 13": {input: long, width: 13, want: 13},
		"breakpoint past a width of 14": {input: long, width: 14, want: 14},
		"breakpoint past a width of 16": {input: long, width: 16, want: 16},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := niceyaml.NewSourceFromString(tc.input).View()
			p := testPrinterWithGutter(printer.LineNumberGutter).With(printer.WithWrapWidth(tc.width))

			got := p.Print(view)
			rows := strings.Split(got, "\n")

			assert.Greater(t, len(rows), 1)
			assert.Equal(t, []int{len(rows)}, layoutRows(p.Layout(view)))
			assertRowsFit(t, got, tc.want)
		})
	}
}

func TestPrinter_WordWrap_RowOfRunes(t *testing.T) {
	t.Parallel()

	// The layout must map the column of each rune a case keeps, such as
	// each wide rune of line 0, to the printed row that holds it. When
	// the wrap drops a row that holds a rune of the content, the layout
	// matches the rows after it to the wrong columns.
	wide := func(r rune) bool { return lipgloss.Width(string(r)) >= 2 }

	// A cluster two cells wide cannot fit in one column, so it takes a row
	// of its own and runs past the width, with no empty row before or
	// after it.
	tcs := map[string]struct {
		gutter      printer.Gutter  // Nil for no gutter.
		keep        func(rune) bool // Selects the runes whose row the layout must name.
		input       string
		want        string
		annotations []line.Annotation
		width       int
	}{
		"wide value": {
			keep:  wide,
			input: "k: 日本語",
			width: 1,
			want:  stringtest.JoinLF("k", ":", "日", "本", "語"),
		},
		"wide key": {
			keep:  wide,
			input: "日本: v",
			width: 1,
			want:  stringtest.JoinLF("日", "本", ":", " ", "v"),
		},
		"wide cluster between narrow ones": {
			keep:  wide,
			input: "a日b: v",
			width: 1,
			want:  stringtest.JoinLF("a", "日", "b", ":", " ", "v"),
		},
		"emoji with a skin tone modifier": {
			keep:  wide,
			input: "k: 👍🏽",
			width: 1,
			want:  stringtest.JoinLF("k", ":", "👍🏽"),
		},
		"no-break space ahead of a wide cluster": {
			keep: wide,
			// The wrap breaks only on an ASCII space, so the no-break space
			// is content and keeps its row.
			input: "k: a\u00a0日本語",
			width: 1,
			want:  stringtest.JoinLF("k", ":", "a", "\u00a0", "日", "本", "語"),
		},
		"zero-width space ahead of a wide cluster": {
			keep: wide,
			// The zero-width space takes no cells, but it is content, so it
			// keeps its row.
			input: "k: \u200b日本",
			width: 1,
			want:  stringtest.JoinLF("k", ":", "\u200b", "日", "本"),
		},
		"gutter leaves one column": {
			keep:   wide,
			gutter: printer.DefaultGutter,
			input:  "k: 日本語",
			width:  7,
			want: stringtest.JoinLF(
				"   1  k",
				"   -  :",
				"   -  日",
				"   -  本",
				"   -  語",
			),
		},
		"wide annotation above": {
			keep:        wide,
			input:       "k: v",
			width:       1,
			annotations: []line.Annotation{{Content: "日本", Placement: line.Above}},
			want:        stringtest.JoinLF("日", "本", "k", ":", "v"),
		},
		// The wrap keeps at most the space of a cluster that starts with a
		// non-ASCII space and drops the runes after it, so the rows show no
		// mark after such a space. Every row after the cluster still starts
		// at its own column of the content, so each letter shows on the row
		// RowOf names.
		"ideographic space with a combining mark": {
			input:       "k: a\u3000\u0301b cccc dddd eeee",
			width:       9,
			annotations: []line.Annotation{{Content: "x", Placement: line.Below, Cols: position.NewSpan(21, 21)}},
			keep:        unicode.IsLetter,
			want:        stringtest.JoinLF("k: a\u3000b", "cccc dddd", "eeee", "   ^ x"),
		},
		"em space with a zero-width joiner": {
			input:       "k: a\u2003\u200db cccc dddd eeee",
			width:       9,
			annotations: []line.Annotation{{Content: "x", Placement: line.Below, Cols: position.NewSpan(21, 21)}},
			keep:        unicode.IsLetter,
			want:        stringtest.JoinLF("k: a\u2003b", "cccc dddd", "eeee", "   ^ x"),
		},
		"em space with a variation selector": {
			input:       "k: a\u2003\ufe0fb cccc dddd eeee",
			width:       9,
			annotations: []line.Annotation{{Content: "x", Placement: line.Below, Cols: position.NewSpan(21, 21)}},
			keep:        unicode.IsLetter,
			want:        stringtest.JoinLF("k: a\u2003b", "cccc dddd", "eeee", "   ^ x"),
		},
		"ideographic space with a combining mark at a break": {
			input:       "k: aaaa \u3000\u0301bbbb cccc dddd",
			width:       5,
			annotations: []line.Annotation{{Content: "x", Placement: line.Below, Cols: position.NewSpan(11, 11)}},
			keep:        unicode.IsLetter,
			want:        stringtest.JoinLF("k:", "aaaa", "bbbb", " ^ x", "cccc", "dddd"),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := niceyaml.NewSourceFromString(tc.input).View()
			view.Annotate(0, tc.annotations...)

			p := testPrinterWithGutter(tc.gutter).With(printer.WithWrapWidth(tc.width))

			got := p.Print(view)
			assert.Equal(t, tc.want, got)

			l := p.Layout(view)
			assertLayoutMeasures(t, l, got)
			assertRowOfRunes(t, l, tc.input, strings.Split(got, "\n"), tc.keep)
		})
	}

	t.Run("default styles", func(t *testing.T) {
		t.Parallel()

		// The default styles open a style ahead of the value, and the wrap
		// then puts the spaces before a wide cluster on rows of their own.
		// Those rows go, so the rows match those of a printer without
		// styles.
		tcs := map[string]struct {
			input string
			want  []string
		}{
			"wide value": {
				input: "k: 日本語",
				want:  []string{"k", ":", "日", "本", "語"},
			},
			"wide sequence item": {
				input: "- 日本",
				want:  []string{"-", "日", "本"},
			},
			"two spaces ahead of a wide value": {
				input: "i:  日",
				want:  []string{"i", ":", "日"},
			},
			"no-break spaces ahead of a wide value": {
				input: "k:\u00a0\u00a0日",
				want:  []string{"k", ":", "\u00a0", "\u00a0", "日"},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				view := niceyaml.NewSourceFromString(tc.input).View()
				p := printer.New(printer.WithGutter(printer.NoGutter), printer.WithWrapWidth(1))

				rows := strings.Split(ansi.Strip(p.Print(view)), "\n")

				assert.Equal(t, tc.want, rows)

				l := p.Layout(view)
				assert.Len(t, rows, l.Rows())
				assertRowOfRunes(t, l, tc.input, rows, wide)
			})
		}
	})
}

func TestPrinter_WordWrap_KeepsClusters(t *testing.T) {
	t.Parallel()

	// The wrap takes each ASCII byte alone, so it would start a row with
	// the runes that continue a cluster begun by an ASCII character. They
	// stay with the start of their cluster, and a styled row keeps its
	// style on both sides of the move, so every case prints the same rows
	// with styles and without.
	red := lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	styled := testPrinterWithStyles(
		style.Set(kind.Text, red),
		style.Set(kind.NameTag, red),
		style.Set(kind.LiteralString, red),
		style.Set(kind.PunctuationMappingValue, red),
	)

	tcs := map[string]struct {
		input string
		want  []string
		width int
	}{
		"decomposed accent": {
			input: "k: abcdefge\u0301xyz",
			width: 8,
			want:  []string{"k:", "abcdefge\u0301", "xyz"},
		},
		"combining accent after a space at a break": {
			input: "k: aaaa \u0301bbbb",
			width: 6,
			want:  []string{"k:", "aaaa", "\u0301bbbb"},
		},
		"keycap mark after a space at a break": {
			input: "k: aaaa \u20e3bbbb",
			width: 6,
			want:  []string{"k:", "aaaa", "\u20e3bbbb"},
		},
		"decomposed accent before a run of breakpoints": {
			input: "k: abbae\u0301//----/x",
			width: 5,
			want:  []string{"k:", "abbae\u0301", "//---", "-/x"},
		},
		"keycap before a run of breakpoints": {
			input: "k: \U0001F44D\U0001F3FD日1\ufe0f\u20e3-----x",
			width: 5,
			want:  []string{"k:", "\U0001F44D\U0001F3FD日", "1\ufe0f\u20e3", "-----", "x"},
		},
		"keycap wider than the width": {
			input: "1\ufe0f\u20e3: bb",
			width: 1,
			want:  []string{"1\ufe0f\u20e3", ":", "b", "b"},
		},
		"keycaps fill a row": {
			input: "k: " + strings.Repeat("1\ufe0f\u20e3", 6),
			width: 8,
			want:  []string{"k:", strings.Repeat("1\ufe0f\u20e3", 4), strings.Repeat("1\ufe0f\u20e3", 2)},
		},
		"keycap mid row": {
			input: "k: cccxx1\ufe0f\u20e3dddd",
			width: 9,
			want:  []string{"k:", "cccxx1\ufe0f\u20e3dd", "d", "d"},
		},
		"keycap before a space": {
			input: "k: 1\ufe0f\u20e3cc --cx",
			width: 4,
			want:  []string{"k:", "1\ufe0f\u20e3cc", "--cx"},
		},
		"keycap split by the wrap": {
			input: "k: ac xb1\ufe0f\u20e3a",
			width: 3,
			want:  []string{"k:", "ac", "xb", "1\ufe0f\u20e3", "a"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := niceyaml.NewSourceFromString(tc.input).View()
			plain := testPrinter().With(printer.WithWrapWidth(tc.width))

			got := strings.Split(plain.Print(view), "\n")
			assert.Equal(t, tc.want, got)
			assert.Len(t, got, plain.Layout(view).Rows())

			assertRowsFitClusters(t, got, tc.width)

			p := styled.With(printer.WithWrapWidth(tc.width))

			printed := p.Print(view)
			rows := strings.Split(printed, "\n")
			assert.Equal(t, tc.want, strings.Split(ansi.Strip(printed), "\n"))
			assert.Len(t, rows, p.Layout(view).Rows())

			// Every row opens the style and closes it again, so no row
			// leaves it open for the next.
			for i, row := range rows {
				assert.Contains(t, row, "\x1b[31m", "row %d", i)
				assert.True(t, strings.HasSuffix(row, "\x1b[m"), "row %d: %q", i, row)
			}
		})
	}
}

func TestPrinter_Overlay_KeepsClusters(t *testing.T) {
	t.Parallel()

	// A grapheme cluster that an overlay covers only in part renders whole
	// in the style of the rune that starts it, so no escape sequence
	// splits the cluster. The rows and their widths then match those of
	// the same line without the overlay. An empty styled means the overlay
	// styles no rune.
	red := lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	family := "\U0001F468\u200d\U0001F469\u200d\U0001F467"

	tcs := map[string]struct {
		input   string
		overlay position.Range
		want    []string
		styled  string
		width   int
	}{
		"keycap digit overlaid": {
			input:   "k: 1\ufe0f\u20e32\ufe0f\u20e33\ufe0f\u20e3",
			overlay: yamltest.LineRange(0, 3, 4),
			width:   8,
			want:    []string{"k: 1\ufe0f\u20e32\ufe0f\u20e3", "3\ufe0f\u20e3"},
			styled:  "\x1b[31m1\ufe0f\u20e3\x1b[m",
		},
		"keycap mark overlaid": {
			input:   "k: 1\ufe0f\u20e32\ufe0f\u20e33\ufe0f\u20e3",
			overlay: yamltest.LineRange(0, 5, 6),
			width:   8,
			want:    []string{"k: 1\ufe0f\u20e32\ufe0f\u20e3", "3\ufe0f\u20e3"},
		},
		"family member overlaid": {
			input:   "k: x" + family + " y",
			overlay: yamltest.LineRange(0, 6, 7),
			width:   6,
			want:    []string{"k: x" + family, "y"},
		},
		"family member through the next letter overlaid": {
			input:   "k: x" + family + "yz",
			overlay: yamltest.LineRange(0, 6, 10),
			width:   6,
			want:    []string{"k:", "x" + family + "yz"},
			styled:  family + "\x1b[31my\x1b[m",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := testPrinterWithStyles(style.Set(kind.GenericHighlight, red)).With(printer.WithWrapWidth(tc.width))

			plainView := niceyaml.NewSourceFromString(tc.input).View()
			view := niceyaml.NewSourceFromString(tc.input).View()
			view.AddOverlay(kind.GenericHighlight, tc.overlay)

			got := p.Print(view)
			plain := strings.Split(ansi.Strip(got), "\n")

			assert.Equal(t, tc.want, plain)
			assert.Equal(t, ansi.Strip(p.Print(plainView)), ansi.Strip(got))
			assert.Equal(t, p.Layout(plainView).Width(), p.Layout(view).Width())

			if tc.styled == "" {
				assert.Equal(t, ansi.Strip(got), got)
			} else {
				assert.Contains(t, got, tc.styled)
			}

			assertRowsFitClusters(t, plain, tc.width)

			// The container pads each row by its whole clusters, so every
			// row of the frame takes the same cells.
			framed := p.With(
				printer.WithWrapWidth(0),
				printer.WithContainerStyle(lipgloss.NewStyle().Border(lipgloss.NormalBorder())),
				printer.WithContainerWidth(20),
			).Print(view)

			for i, row := range strings.Split(framed, "\n") {
				assert.Equal(t, 20, lipgloss.Width(ansi.Strip(row)), "row %d: %q", i, row)
			}
		})
	}
}

func TestPrinter_WordWrap_BreakpointPastWidth_KeepsStyle(t *testing.T) {
	t.Parallel()

	// The row the hard wrap cuts off must open its style again, so the
	// text it carries renders like the rows around it. The width leaves
	// seven columns after the gutter.
	input := "name: some-very-long-name-that-will-wrap-around-the-viewport-width-for-sure\n"
	view := niceyaml.NewSourceFromString(input).View()

	p := printer.New(printer.WithWrapWidth(13))

	rows := strings.Split(p.Print(view), "\n")

	var found bool

	for _, row := range rows {
		plain := ansi.Strip(row)
		if !strings.HasSuffix(strings.TrimRight(plain, " "), "-f") {
			continue
		}

		found = true

		// The escape sequence right before the text sets a color rather
		// than resetting the style the row before it opened.
		i := strings.Index(row, "-f")
		require.Positive(t, i)

		j := strings.LastIndex(row[:i], "\x1b[")
		require.GreaterOrEqual(t, j, 0, "%q", row)

		seq := row[j:i]
		assert.NotEqual(t, "\x1b[m", seq, "%q", row)
		assert.Contains(t, seq, "38;", "%q", row)
	}

	require.True(t, found, "no row ends in the cut-off text: %q", rows)
}

// prefixGutter is a [printer.Gutter] that renders one prefix per flag and
// declares the width of the widest.
type prefixGutter struct {
	inserted, deleted, equal string
}

func (g prefixGutter) Width(printer.GutterContext) int {
	return max(len(g.inserted), len(g.deleted), len(g.equal))
}

func (g prefixGutter) Render(ctx printer.GutterContext) string {
	if ctx.Soft {
		return ""
	}

	switch ctx.Flag {
	case line.FlagInserted:
		return g.inserted
	case line.FlagDeleted:
		return g.deleted
	default:
		return g.equal
	}
}

func TestGutter(t *testing.T) {
	t.Parallel()

	view := niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "b: 2")).View()
	view.SetFlag(1, line.FlagInserted)

	tcs := map[string]struct {
		gutter    printer.Gutter
		want      string
		wantWidth int
	}{
		"pads a row that renders less than the width": {
			gutter: prefixGutter{inserted: "+ "},
			want: stringtest.JoinLF(
				"  a: 1",
				"+ b: 2",
			),
			wantWidth: 2,
		},
		"cuts a row that renders more than the width": {
			gutter:    fixedGutter{width: 1, text: "abc"},
			want:      stringtest.JoinLF("aa: 1", "ab: 2"),
			wantWidth: 1,
		},
		// The two-cell rune does not fit in one cell. The cut shows the
		// cell inside the width as a space, so the row reaches the width
		// without a pad.
		"fills a wide rune the width splits": {
			gutter: fixedGutter{width: 1, text: "日"},
			want: stringtest.JoinLF(
				" a: 1",
				" b: 2",
			),
			wantWidth: 1,
		},
		// Two keycaps take four cells, so a cut to three keeps the first
		// and shows the cell of the second inside the width as a space.
		"cuts a keycap by the two cells it takes": {
			gutter:    fixedGutter{width: 3, text: strings.Repeat("1\ufe0f\u20e3", 2)},
			want:      stringtest.JoinLF("1\ufe0f\u20e3 a: 1", "1\ufe0f\u20e3 b: 2"),
			wantWidth: 3,
		},
		// The zero-width space starts at the gutter width, past the last
		// column the gutter holds, so the cut drops it whether or not the
		// text before it runs over.
		"cuts a zero-width cluster at the edge from text that fits": {
			gutter:    fixedGutter{width: 2, text: "ab\u200b"},
			want:      stringtest.JoinLF("aba: 1", "abb: 2"),
			wantWidth: 2,
		},
		"a negative width counts as none": {
			gutter:    fixedGutter{width: -3, text: "abc"},
			want:      stringtest.JoinLF("a: 1", "b: 2"),
			wantWidth: 0,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := testPrinterWithGutter(tc.gutter)

			assert.Equal(t, tc.want, p.Print(view))
			assert.Equal(t, tc.wantWidth, p.Layout(view).GutterWidth())
		})
	}

	t.Run("a func measures what it renders for the largest number", func(t *testing.T) {
		t.Parallel()

		g := printer.GutterFunc(func(ctx printer.GutterContext) string {
			if ctx.Flag == line.FlagInserted {
				return "++"
			}

			return strings.Repeat("#", len(strconv.Itoa(ctx.MaxNumber)))
		})

		assert.Equal(t, 1, g.Width(printer.GutterContext{MaxNumber: 2}))
		assert.Equal(t, 3, g.Width(printer.GutterContext{MaxNumber: 100}))

		// The func renders two cells for the inserted line and the printer
		// cuts it to the one cell it measured.
		p := testPrinterWithGutter(g)
		assert.Equal(t, stringtest.JoinLF("#a: 1", "+b: 2"), p.Print(view))
	})

	t.Run("the built-in gutters render every row at the width they declare", func(t *testing.T) {
		t.Parallel()

		for name, g := range map[string]printer.Gutter{
			"default": printer.DefaultGutter,
			"diff":    printer.DiffGutter,
			"number":  printer.LineNumberGutter,
			"none":    printer.NoGutter,
		} {
			for _, maxNumber := range []int{0, 1, 9999, 10000, 123456} {
				sample := printer.GutterContext{Number: maxNumber, MaxNumber: maxNumber, Styles: style.Styles{}}
				width := g.Width(sample)

				for _, flag := range []line.Flag{line.FlagDefault, line.FlagInserted, line.FlagDeleted} {
					for _, soft := range []bool{false, true} {
						for _, annotation := range []bool{false, true} {
							ctx := sample
							ctx.Number, ctx.Flag, ctx.Soft, ctx.Annotation = 1, flag, soft, annotation

							got := lipgloss.Width(g.Render(ctx))
							assert.Equal(t, width, got, "%s gutter at %d: %+v", name, maxNumber, ctx)
						}
					}
				}
			}
		}
	})
}

// fixedGutter is a [printer.Gutter] that declares one width and renders
// one text, whatever the row.
type fixedGutter struct {
	text  string
	width int
}

func (g fixedGutter) Width(printer.GutterContext) int {
	return g.width
}

func (g fixedGutter) Render(printer.GutterContext) string {
	return g.text
}

func TestGutterFunctions(t *testing.T) {
	t.Parallel()

	styles := style.Styles{}
	tagged := yamltest.NewXMLStyles()

	type gutterCase struct {
		ctx  printer.GutterContext
		want string
	}

	tcs := map[string]struct {
		gutter printer.Gutter
		cases  map[string]gutterCase
	}{
		"default": {
			gutter: printer.DefaultGutter,
			cases: map[string]gutterCase{
				// A zero context renders with the default styles.
				"zero context": {
					ctx:  printer.GutterContext{},
					want: "     " + " ",
				},
				// A context holding a nil pointer renders like a zero context.
				"nil pointer styles": {
					ctx:  printer.GutterContext{Styles: (*style.Styles)(nil)},
					want: "     " + " ",
				},
				"annotation row renders empty": {
					ctx:  printer.GutterContext{Flag: line.FlagInserted, Annotation: true, Number: 1, Styles: styles},
					want: "      ",
				},
				"soft wrap renders continuation marker": {
					ctx:  printer.GutterContext{Flag: line.FlagDefault, Soft: true, Styles: styles},
					want: "   -  ",
				},
				"normal line renders line number": {
					ctx:  printer.GutterContext{Flag: line.FlagDefault, Number: 1, Styles: styles},
					want: "   1  ",
				},
				"inserted flag renders + marker": {
					ctx:  printer.GutterContext{Flag: line.FlagInserted, Number: 1, Styles: styles},
					want: "   1 +",
				},
				"deleted flag renders - marker": {
					ctx:  printer.GutterContext{Flag: line.FlagDeleted, Number: 1, Styles: styles},
					want: "   1 -",
				},
				"max number widens the column": {
					ctx: printer.GutterContext{
						Flag:      line.FlagInserted,
						Number:    15001,
						MaxNumber: 15001,
						Styles:    styles,
					},
					want: "15001 +",
				},
				"zero number renders a blank column": {
					ctx:  printer.GutterContext{Flag: line.FlagDefault, Number: 0, MaxNumber: 3, Styles: styles},
					want: "      ",
				},
			},
		},
		"diff": {
			gutter: printer.DiffGutter,
			cases: map[string]gutterCase{
				"zero context": {
					ctx:  printer.GutterContext{},
					want: " ",
				},
				"nil pointer styles": {
					ctx:  printer.GutterContext{Styles: (*theme.Theme)(nil)},
					want: " ",
				},
				"default flag": {
					ctx:  printer.GutterContext{Flag: line.FlagDefault, Styles: styles},
					want: " ",
				},
				"inserted flag": {
					ctx:  printer.GutterContext{Flag: line.FlagInserted, Styles: styles},
					want: "+",
				},
				"deleted flag": {
					ctx:  printer.GutterContext{Flag: line.FlagDeleted, Styles: styles},
					want: "-",
				},
				"soft wrap default": {
					ctx:  printer.GutterContext{Flag: line.FlagDefault, Soft: true, Styles: styles},
					want: " ",
				},
				"soft wrap inserted": {
					ctx:  printer.GutterContext{Flag: line.FlagInserted, Soft: true, Styles: styles},
					want: " ",
				},
				"soft wrap deleted": {
					ctx:  printer.GutterContext{Flag: line.FlagDeleted, Soft: true, Styles: styles},
					want: " ",
				},
				// With tagged styles, each row names the kind it renders in.
				"tagged default": {
					ctx:  printer.GutterContext{Flag: line.FlagDefault, Styles: tagged},
					want: "<text> </text>",
				},
				"tagged inserted": {
					ctx:  printer.GutterContext{Flag: line.FlagInserted, Styles: tagged},
					want: "<genericInserted>+</genericInserted>",
				},
				"tagged deleted": {
					ctx:  printer.GutterContext{Flag: line.FlagDeleted, Styles: tagged},
					want: "<genericDeleted>-</genericDeleted>",
				},
				"tagged soft wrap default": {
					ctx:  printer.GutterContext{Flag: line.FlagDefault, Soft: true, Styles: tagged},
					want: "<text> </text>",
				},
				"tagged soft wrap inserted keeps the flag style": {
					ctx:  printer.GutterContext{Flag: line.FlagInserted, Soft: true, Styles: tagged},
					want: "<genericInserted> </genericInserted>",
				},
				"tagged soft wrap deleted keeps the flag style": {
					ctx:  printer.GutterContext{Flag: line.FlagDeleted, Soft: true, Styles: tagged},
					want: "<genericDeleted> </genericDeleted>",
				},
				"tagged annotation default": {
					ctx:  printer.GutterContext{Flag: line.FlagDefault, Annotation: true, Styles: tagged},
					want: "<text> </text>",
				},
				"tagged annotation inserted renders as text": {
					ctx:  printer.GutterContext{Flag: line.FlagInserted, Annotation: true, Styles: tagged},
					want: "<text> </text>",
				},
				"tagged annotation deleted renders as text": {
					ctx:  printer.GutterContext{Flag: line.FlagDeleted, Annotation: true, Styles: tagged},
					want: "<text> </text>",
				},
				"tagged soft wrap annotation inserted renders as text": {
					ctx:  printer.GutterContext{Flag: line.FlagInserted, Soft: true, Annotation: true, Styles: tagged},
					want: "<text> </text>",
				},
				"tagged soft wrap annotation deleted renders as text": {
					ctx:  printer.GutterContext{Flag: line.FlagDeleted, Soft: true, Annotation: true, Styles: tagged},
					want: "<text> </text>",
				},
			},
		},
		"lineNumber": {
			gutter: printer.LineNumberGutter,
			cases: map[string]gutterCase{
				"zero context": {
					ctx:  printer.GutterContext{},
					want: "     ",
				},
				"annotation row renders empty": {
					ctx:  printer.GutterContext{Flag: line.FlagDeleted, Annotation: true, Number: 1, Styles: styles},
					want: "     ",
				},
				"soft wrap renders continuation marker": {
					ctx:  printer.GutterContext{Flag: line.FlagDefault, Soft: true, Styles: styles},
					want: "   - ",
				},
				"normal line renders line number": {
					ctx:  printer.GutterContext{Flag: line.FlagDefault, Number: 1, Styles: styles},
					want: "   1 ",
				},
				"max number widens the column": {
					ctx:  printer.GutterContext{Flag: line.FlagDefault, Number: 1, MaxNumber: 15001, Styles: styles},
					want: "    1 ",
				},
				"max number widens the soft wrap marker": {
					ctx:  printer.GutterContext{Flag: line.FlagDefault, Soft: true, MaxNumber: 15001, Styles: styles},
					want: "    - ",
				},
				"zero number renders a blank column": {
					ctx:  printer.GutterContext{Flag: line.FlagDefault, Number: 0, MaxNumber: 15001, Styles: styles},
					want: "      ",
				},
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			for name, cc := range tc.cases {
				t.Run(name, func(t *testing.T) {
					t.Parallel()

					got := tc.gutter.Render(cc.ctx)

					// The default styles of a zero context carry colors, which
					// the text under test does not.
					assert.Equal(t, cc.want, ansi.Strip(got))
				})
			}
		})
	}
}

func TestPrinter_Style(t *testing.T) {
	t.Parallel()

	base := lipgloss.NewStyle()

	tcs := map[string]struct {
		styles     style.Styles
		query      kind.Kind
		wantBold   bool
		wantItalic bool
	}{
		"returns style from styles map": {
			styles: style.New(
				base,
				style.Set(kind.NameTag, base.Bold(true)),
			),
			query:    kind.NameTag,
			wantBold: true,
		},
		"child inherits from parent": {
			styles:     style.New(base.Italic(true)),
			query:      kind.NameTag,
			wantItalic: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := printer.New(printer.WithStyles(tc.styles)).Style(tc.query)

			assert.Equal(t, tc.wantBold, got.GetBold())
			assert.Equal(t, tc.wantItalic, got.GetItalic())
		})
	}
}

func TestPrinter_PrintSideBySide(t *testing.T) {
	t.Parallel()

	// Each case prints the panes that views returns. The plain printer
	// numbers the lines and marks the changes of a diff.
	twoLines := stringtest.LinesLF("a: 1", "b: 2")
	view := func(input string) *line.View { return niceyaml.NewSourceFromString(input).View() }
	panes := func(left, right string) func() (*line.View, *line.View) {
		return func() (*line.View, *line.View) { return view(left), view(right) }
	}

	tcs := map[string]struct {
		views func() (*line.View, *line.View)
		want  string
		opts  []printer.Option
	}{
		"equal panes join row by row": {
			views: panes(twoLines, twoLines),
			want: stringtest.JoinLF(
				"   1  a: 1 │    1  a: 1",
				"   2  b: 2 │    2  b: 2",
			),
		},
		"annotation above a line of the right pane": {
			views: func() (*line.View, *line.View) {
				left, right := view(twoLines), view(twoLines)
				right.Annotate(1, line.Annotation{Content: "a note", Placement: line.Above})

				return left, right
			},
			want: stringtest.JoinLF(
				"   1  a: 1 │    1  a: 1",
				"           │       a note",
				"   2  b: 2 │    2  b: 2",
			),
		},
		"annotation below a line of the left pane": {
			views: func() (*line.View, *line.View) {
				left, right := view(twoLines), view(twoLines)
				left.Annotate(0, yamltest.ErrorAnnotation(position.NewSpan(3, 4), "bad"))

				return left, right
			},
			want: stringtest.JoinLF(
				"   1  a: 1     │    1  a: 1",
				"         ^ bad │       ",
				"   2  b: 2     │    2  b: 2",
			),
		},
		// Each pane wraps to (33-3)/2 = 15 cells, and the left pane pads
		// its rows to that width.
		"line that wraps taller in one pane": {
			views: panes(stringtest.LinesLF("a: one two three", "b: 2"), twoLines),
			opts:  []printer.Option{printer.WithWrapWidth(33)},
			want: stringtest.JoinLF(
				"   1  a: one    │    1  a: 1",
				"   -  two three │       ",
				"   2  b: 2      │    2  b: 2",
			),
		},
		// The left pane has two rows above the content row, and the right
		// pane has three rows after it. The line thus takes six rows (two
		// above, the content row, and three after), more than either pane
		// takes alone.
		"line with more rows above in one pane and after in the other": {
			views: func() (*line.View, *line.View) {
				left, right := view(twoLines), view(stringtest.LinesLF("a: one two three four", "b: 2"))
				left.Annotate(0,
					line.Annotation{Content: "one", Kind: kind.TextError, Placement: line.Above},
					line.Annotation{Content: "two", Kind: kind.TextWarn, Placement: line.Above},
				)

				return left, right
			},
			opts: []printer.Option{printer.WithWrapWidth(31)},
			want: stringtest.JoinLF(
				"      one      │       ",
				"      two      │       ",
				"   1  a: 1     │    1  a: one",
				"               │    -  two",
				"               │    -  three",
				"               │    -  four",
				"   2  b: 2     │    2  b: 2",
			),
		},
		"diff placeholders print blank gutters": {
			views: func() (*line.View, *line.View) {
				result := yamltest.Diff("a: 1\n", twoLines)

				return result.Before(), result.After()
			},
			want: stringtest.JoinLF(
				"   1  a: 1 │    1  a: 1",
				"           │    2 +b: 2",
			),
		},
		"large number widens both gutters": {
			views: func() (*line.View, *line.View) {
				long := view(tenThousandLines + "z: 9\n")

				return long.Slice(position.NewSpan(0, 1), position.NewSpan(10000, 10001)), view(twoLines)
			},
			want: stringtest.JoinLF(
				"    1  k: v │     1  a: 1",
				"            │     2  b: 2",
				"10001  z: 9 │        ",
			),
		},
		"line that only one pane holds": {
			views: func() (*line.View, *line.View) {
				return view(twoLines).Slice(position.NewSpan(0, 1)), view(twoLines)
			},
			want: stringtest.JoinLF(
				"   1  a: 1 │    1  a: 1",
				"           │    2  b: 2",
			),
		},
		"left row past the pane width": {
			views: func() (*line.View, *line.View) {
				left, right := view(twoLines), view(twoLines)
				left.Annotate(0, yamltest.ErrorAnnotation(position.NewSpan(20, 21), "far"))

				return left, right
			},
			opts: []printer.Option{printer.WithWrapWidth(33)},
			want: stringtest.JoinLF(
				"   1  a: 1                  │    1  a: 1",
				"                          ^ │       ",
				"      far                   │       ",
				"   2  b: 2                  │    2  b: 2",
			),
		},
		"container style wraps the joined rows": {
			views: panes(twoLines, twoLines),
			opts:  []printer.Option{printer.WithContainerStyle(lipgloss.NewStyle().Border(lipgloss.NormalBorder()))},
			want: stringtest.JoinLF(
				"┌───────────────────────┐",
				"│   1  a: 1 │    1  a: 1│",
				"│   2  b: 2 │    2  b: 2│",
				"└───────────────────────┘",
			),
		},
		"container width pads the joined rows": {
			views: panes(twoLines, twoLines),
			opts:  []printer.Option{printer.WithContainerWidth(26)},
			want: stringtest.JoinLF(
				"   1  a: 1 │    1  a: 1   ",
				"   2  b: 2 │    2  b: 2   ",
			),
		},
		"empty views": {
			views: panes("", ""),
			want:  "",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := testPrinterWithGutter(printer.DefaultGutter).With(tc.opts...)
			left, right := tc.views()
			got := p.PrintSideBySide(left, right)

			assert.Equal(t, tc.want, got)

			// The separator keeps one cell on every row.
			sep := -1

			for row := range strings.SplitSeq(got, "\n") {
				before, _, ok := strings.Cut(row, "│")
				if !ok || strings.HasPrefix(row, "│") {
					continue
				}

				if sep < 0 {
					sep = lipgloss.Width(before)
				}

				assert.Equal(t, sep, lipgloss.Width(before), "row %q", row)
			}

			// A row fits the wrap width unless a row of the left pane runs
			// past its half.
			if w := p.WrapWidth(); w > 0 && name != "left row past the pane width" {
				for row := range strings.SplitSeq(got, "\n") {
					assert.LessOrEqual(t, lipgloss.Width(row), w, "row %q", row)
				}
			}
		})
	}
}

func TestPrinter_PrintSideBySide_PaneLayout(t *testing.T) {
	t.Parallel()

	// A viewer zips the panes from a Layout of each pane by the printer
	// that PrintSideBySide draws the pane with. Each line then starts on
	// the row the zip gives it, and its first content row sits below the
	// larger block of rows above it.
	left := niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "b: 2", "c: 3")).View()
	left.Annotate(0,
		line.Annotation{Content: "one", Kind: kind.TextError, Placement: line.Above},
		line.Annotation{Content: "two", Kind: kind.TextWarn, Placement: line.Above},
	)

	right := niceyaml.NewSourceFromString(stringtest.LinesLF("a: one two three four", "b: 2", "c: 3")).View()
	right.Annotate(2, line.Annotation{Content: "a note", Placement: line.Above})

	p := testPrinterWithGutter(printer.DefaultGutter).With(
		printer.WithWrapWidth(33),
		printer.WithContainerStyle(lipgloss.NewStyle().Border(lipgloss.NormalBorder())),
	)

	// The pane width is half of what the frame and the separator leave.
	paneWidth := (p.WrapWidth() - p.ContainerStyle().GetHorizontalFrameSize() - 3) / 2
	pane := p.With(
		printer.WithContainerStyle(lipgloss.NewStyle()),
		printer.WithContainerWidth(0),
		printer.WithMaxNumber(max(p.MaxNumber(left), p.MaxNumber(right))),
		printer.WithWrapWidth(paneWidth),
	)
	layouts := [2]printer.Layout{pane.Layout(left), pane.Layout(right)}

	// The rows inside the top and the bottom border.
	rows := strings.Split(p.PrintSideBySide(left, right), "\n")
	rows = rows[1 : len(rows)-1]

	start := 0

	for i := range 3 {
		var above, after [2]int

		for k, l := range layouts {
			above[k] = l.RowOf(position.New(i, 0)) - l.LineStart(i)
			after[k] = l.LineRows(i) - above[k]
		}

		top := max(above[0], above[1])
		require.Less(t, start+top, len(rows))

		leftHalf, rightHalf, ok := strings.Cut(rows[start+top], " │ ")
		require.True(t, ok, "row %q", rows[start+top])

		number := " " + strconv.Itoa(i+1) + "  "
		assert.Contains(t, leftHalf, number, "line %d", i)
		assert.Contains(t, rightHalf, number, "line %d", i)

		start += top + max(after[0], after[1])
	}

	assert.Len(t, rows, start)
}

func TestPrinter_PrintSideBySide_Golden(t *testing.T) {
	t.Parallel()

	// The diff changes one price and adds an item, and each revision marks
	// the prices that are not in kr.
	before := stringtest.LinesLF(
		"menu:",
		"  - item: Kanelbulle",
		"    price: 45 SEK",
		"  - item: Semla",
		"    price: 65 kr",
	)
	after := stringtest.LinesLF(
		"menu:",
		"  - item: Kanelbulle",
		"    price: 45 kr",
		"  - item: Semla",
		"    price: 65 kr",
		"  - item: Prinsesstårta",
		"    price: 8 EUR",
	)

	tcs := map[string]struct {
		wrap int
	}{
		"diff with marks":         {},
		"wrapped diff with marks": {wrap: 40},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			viewBefore := niceyaml.NewSourceFromString(before).View()
			viewBefore.AddOverlay(kind.GenericError, yamltest.LineRange(2, 11, 17))
			viewBefore.Annotate(2, yamltest.ErrorAnnotation(position.NewSpan(11, 17), "price is not in kr"))

			viewAfter := niceyaml.NewSourceFromString(after).View()
			viewAfter.AddOverlay(kind.GenericError, yamltest.LineRange(6, 11, 16))
			viewAfter.Annotate(6, yamltest.ErrorAnnotation(position.NewSpan(11, 16), "price is not in kr"))
			viewAfter.Annotate(5, line.Annotation{Content: "new item", Kind: kind.TextOK, Placement: line.Above})

			result := diff.Diff(viewBefore, viewAfter)
			left, right := result.Before(), result.After()
			result.Carry(left, viewBefore, viewAfter)
			result.Carry(right, viewBefore, viewAfter)

			p := printer.New(printer.WithStyles(theme.Charm), printer.WithWrapWidth(tc.wrap))
			golden.RequireEqual(t, p.PrintSideBySide(left, right))
		})
	}
}

func TestPrinter_Pad(t *testing.T) {
	t.Parallel()

	// The Charm theme sets a surface, so the spaces of the padding carry
	// its background.
	p := printer.New(printer.WithStyles(theme.Charm))
	text := p.Style(kind.Text)

	tcs := map[string]struct {
		row   string
		want  string
		width int
	}{
		"pads a short row":          {row: "ab", width: 5, want: "ab" + text.Render("   ")},
		"row at the width":          {row: "abcde", width: 5, want: "abcde"},
		"row past the width":        {row: "abcdefg", width: 5, want: "abcdefg"},
		"zero width":                {row: "ab", width: 0, want: "ab"},
		"negative width":            {row: "ab", width: -3, want: "ab"},
		"empty row":                 {row: "", width: 2, want: text.Render("  ")},
		"wide rune takes two cells": {row: "日", width: 3, want: "日" + text.Render(" ")},
		"styled row counts its text": {
			row:   "\x1b[31mab\x1b[0m",
			width: 4,
			want:  "\x1b[31mab\x1b[0m" + text.Render("  "),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, p.Pad(tc.row, tc.width))
		})
	}

	// A plain space leaves a hole in the surface.
	assert.NotEqual(t, "ab   ", p.Pad("ab", 5))
}

func TestPrinter_Segments(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		decorate func(view *line.View)
		// The kind each segment of the line renders in, by its text.
		want map[string]kind.Kind
	}{
		"plain line takes the kind of each segment": {
			want: map[string]kind.Kind{
				"name":  kind.NameTag,
				":":     kind.PunctuationMappingValue,
				" ":     kind.Text,
				"kaffe": kind.LiteralString,
			},
		},
		"inserted line takes the inserted kind": {
			decorate: func(view *line.View) { view.SetFlag(0, line.FlagInserted) },
			want: map[string]kind.Kind{
				"name":  kind.GenericInserted,
				":":     kind.GenericInserted,
				" ":     kind.GenericInserted,
				"kaffe": kind.GenericInserted,
			},
		},
		"deleted line takes the deleted kind": {
			decorate: func(view *line.View) { view.SetFlag(0, line.FlagDeleted) },
			want: map[string]kind.Kind{
				"name":  kind.GenericDeleted,
				"kaffe": kind.GenericDeleted,
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := niceyaml.NewSourceFromString("name: kaffe\n").View()
			if tc.decorate != nil {
				tc.decorate(view)
			}

			p := printer.New(printer.WithStyles(theme.Charm))

			// The segments come as line.View.Segments yields them, with
			// their own kinds.
			var segs []line.Segment

			for seg, st := range p.Segments(view, 0) {
				segs = append(segs, seg)

				if k, ok := tc.want[seg.Text]; ok {
					assert.Equal(t, p.Style(k).Render(seg.Text), st.Render(seg.Text), "segment %q", seg.Text)
				}
			}

			assert.Equal(t, slices.Collect(view.Segments(0)), segs)
		})
	}
}

func TestPrinter_Segments_MatchPrint(t *testing.T) {
	t.Parallel()

	// The value "kaffe" takes columns 6 through 11, and the segments
	// around it render in other styles, so Print draws it as a run of its
	// own.
	value := position.NewRange(position.New(0, 6), position.New(0, 11))

	tcs := map[string]struct {
		decorate func(view *line.View)
		// The foreground the value takes, or "" when the case does not
		// check it.
		wantForeground kind.Kind
	}{
		"blended highlight": {
			decorate: func(view *line.View) { view.BlendOverlay(kind.GenericHighlight, value) },
		},
		"replacing overlay": {
			decorate:       func(view *line.View) { view.AddOverlay(kind.GenericError, value) },
			wantForeground: kind.GenericError,
		},
		"overlays apply in order": {
			decorate: func(view *line.View) {
				view.AddOverlay(kind.GenericError, value)
				view.AddOverlay(kind.TextOK, value)
			},
			wantForeground: kind.TextOK,
		},
		"blended highlight on an inserted line": {
			decorate: func(view *line.View) {
				view.SetFlag(0, line.FlagInserted)
				view.BlendOverlay(kind.GenericHighlight, value)
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := niceyaml.NewSourceFromString("name: kaffe # x\n").View()
			tc.decorate(view)

			p := printer.New(printer.WithStyles(theme.Charm))
			printed := p.Print(view)

			var found bool

			for seg, st := range p.Segments(view, 0) {
				if seg.Text != "kaffe" {
					continue
				}

				found = true

				// The overlays change the style of the value, and Print
				// draws the value in the style that Segments yields.
				assert.NotEqual(t, p.Style(seg.Kind).Render(seg.Text), st.Render(seg.Text))
				assert.Contains(t, printed, st.Render(seg.Text))

				if tc.wantForeground != "" {
					assert.Equal(t, p.Style(tc.wantForeground).GetForeground(), st.GetForeground())
				}
			}

			assert.True(t, found, "the line holds a segment of the value")
		})
	}
}

func TestPrinter_Segments_Stop(t *testing.T) {
	t.Parallel()

	view := niceyaml.NewSourceFromString("name: kaffe\n").View()

	var n int

	for range printer.New().Segments(view, 0) {
		n++

		break
	}

	assert.Equal(t, 1, n)
}

func TestPrinter_Print_Tokens(t *testing.T) {
	t.Parallel()

	// A printer with tagged styles wraps each token in the tag of its kind,
	// and a printer without styles prints the text of the tokens it gets.

	tcs := map[string]struct {
		input string
		want  string
	}{
		"key and string": {
			input: "key: value",
			want:  "<nameTag>key</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
		},
		"key with the colon on the next line": {
			input: stringtest.JoinLF(
				"{",
				"  k",
				"  : v",
				"}",
			),
			want: stringtest.JoinLF(
				"<punctuationMappingStart>{</punctuationMappingStart>",
				"<text>  </text><nameTag>k</nameTag>",
				"<text>  </text><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>v</literalString>",
				"<punctuationMappingEnd>}</punctuationMappingEnd>",
			),
		},
		"null types": {
			input: stringtest.JoinLF(
				"null: null",
				"tilde: ~",
			),
			want: stringtest.JoinLF(
				"<nameTag>null</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalNull>null</literalNull>",
				"<nameTag>tilde</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalNull>~</literalNull>",
			),
		},
		"boolean types": {
			input: stringtest.JoinLF(
				"yes: true",
				"no: false",
			),
			want: stringtest.JoinLF(
				"<nameTag>yes</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalBoolean>true</literalBoolean>",
				"<nameTag>no</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalBoolean>false</literalBoolean>",
			),
		},
		"number types": {
			input: stringtest.JoinLF(
				"int: 42",
				"float: 3.14",
			),
			want: stringtest.JoinLF(
				"<nameTag>int</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalNumberInteger>42</literalNumberInteger>",
				"<nameTag>float</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalNumberFloat>3.14</literalNumberFloat>",
			),
		},
		"anchor and alias": {
			input: stringtest.JoinLF(
				"anchor: &x 1",
				"alias: *x",
			),
			want: stringtest.JoinLF(
				"<nameTag>anchor</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><nameAnchor>&x</nameAnchor><text> </text><literalNumberInteger>1</literalNumberInteger>",
				"<nameTag>alias</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><nameAlias>*x</nameAlias>",
			),
		},
		"comment": {
			input: "key: value # comment",
			want:  "<nameTag>key</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString><text> </text><comment># comment</comment>",
		},
		"comment line": {
			input: stringtest.JoinLF(
				"bool: true",
				"# comment",
			),
			want: stringtest.JoinLF(
				"<nameTag>bool</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalBoolean>true</literalBoolean>",
				"<comment># comment</comment>",
			),
		},
		"tag": {
			input: "tagged: !custom value",
			want:  "<nameTag>tagged</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><nameDecorator>!custom</nameDecorator><text> </text><literalString>value</literalString>",
		},
		"document markers": {
			input: stringtest.JoinLF(
				"---",
				"key: value",
				"...",
			),
			want: stringtest.JoinLF(
				"<punctuationHeading>---</punctuationHeading>",
				"<nameTag>key</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
				"<punctuationHeading>...</punctuationHeading>",
			),
		},
		"directive": {
			input: stringtest.JoinLF(
				"%YAML 1.2",
				"---",
				"key: value",
			),
			want: stringtest.JoinLF(
				"<commentPreproc>%</commentPreproc><literalString>YAML</literalString><text> </text><literalNumberFloat>1.2</literalNumberFloat>",
				"<punctuationHeading>---</punctuationHeading>",
				"<nameTag>key</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
			),
		},
		"block scalar": {
			input: stringtest.JoinLF(
				"text: |",
				"  line1",
				"  line2",
			),
			want: stringtest.JoinLF(
				"<nameTag>text</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><punctuationBlockLiteral>|</punctuationBlockLiteral>",
				"<text>  </text><literalString>line1</literalString>",
				"<text>  </text><literalString>line2</literalString>",
			),
		},
		"flow sequence punctuation": {
			input: "k: [a, b]",
			want:  "<nameTag>k</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><punctuationSequenceStart>[</punctuationSequenceStart><literalString>a</literalString><punctuationCollectEntry>,</punctuationCollectEntry><text> </text><literalString>b</literalString><punctuationSequenceEnd>]</punctuationSequenceEnd>",
		},
		"block sequence entry": {
			input: stringtest.JoinLF(
				"- a",
				"- b",
			),
			want: stringtest.JoinLF(
				"<punctuationSequenceEntry>-</punctuationSequenceEntry><text> </text><literalString>a</literalString>",
				"<punctuationSequenceEntry>-</punctuationSequenceEntry><text> </text><literalString>b</literalString>",
			),
		},
		// The lexer hands the whole gap before a comment to the value
		// token, and every cell of it renders unstyled.
		"gap before a comment": {
			input: "key: value   # comment",
			want:  "<nameTag>key</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString><text>   </text><comment># comment</comment>",
		},
		"empty file": {
			input: "",
			want:  "",
		},
		"two documents": {
			input: stringtest.JoinLF(
				"doc1: value1",
				"---",
				"doc2: value2",
			),
			want: stringtest.JoinLF(
				"<nameTag>doc1</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value1</literalString>",
				"<punctuationHeading>---</punctuationHeading>",
				"<nameTag>doc2</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value2</literalString>",
			),
		},
		"two documents with headers and footers": {
			input: stringtest.JoinLF(
				"---",
				"doc1: value1",
				"...",
				"---",
				"doc2: value2",
				"...",
			),
			want: stringtest.JoinLF(
				"<punctuationHeading>---</punctuationHeading>",
				"<nameTag>doc1</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value1</literalString>",
				"<punctuationHeading>...</punctuationHeading>",
				"<punctuationHeading>---</punctuationHeading>",
				"<nameTag>doc2</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value2</literalString>",
				"<punctuationHeading>...</punctuationHeading>",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := niceyaml.NewSourceFromTokens(lexer.Tokenize(tc.input)).View()

			assert.Equal(t, tc.want, yamltest.NewXMLPrinter().Print(view))
			assert.Equal(t, tc.input, testPrinter().Print(view))
		})
	}
}

func TestPrinter_PrintTokenDiffSummary(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		before  string
		after   string
		want    string
		context int
	}{
		"no changes returns empty": {
			before:  "key: value\n",
			after:   "key: value\n",
			context: 1,
		},
		"simple change no context": {
			before: stringtest.LinesLF(
				"a: 1",
				"b: 2",
				"c: 3",
			),
			after: stringtest.LinesLF(
				"a: 1",
				"b: changed",
				"c: 3",
			),
			context: 0,
			want: stringtest.JoinLF(
				"      @@ -2 +2 @@",
				"   2 -b: 2",
				"   2 +b: changed",
			),
		},
		"simple change with context 1": {
			before: stringtest.LinesLF(
				"a: 1",
				"b: 2",
				"c: 3",
				"d: 4",
			),
			after: stringtest.LinesLF(
				"a: 1",
				"b: changed",
				"c: 3",
				"d: 4",
			),
			context: 1,
			want: stringtest.JoinLF(
				"      @@ -1,3 +1,3 @@",
				"   1  a: 1",
				"   2 -b: 2",
				"   2 +b: changed",
				"   3  c: 3",
			),
		},
		"multiple scattered changes with context": {
			before: stringtest.LinesLF(
				"a: 1",
				"b: 2",
				"c: 3",
				"d: 4",
				"e: 5",
			),
			after: stringtest.LinesLF(
				"a: X",
				"b: 2",
				"c: 3",
				"d: 4",
				"e: Y",
			),
			context: 1,
			want: stringtest.JoinLF(
				"      @@ -1,2 +1,2 @@",
				"   1 -a: 1",
				"   1 +a: X",
				"   2  b: 2",
				"      @@ -4,2 +4,2 @@",
				"   4  d: 4",
				"   5 -e: 5",
				"   5 +e: Y",
			),
		},
		"gap separator between non-adjacent changes": {
			before: stringtest.LinesLF(
				"line1: a",
				"line2: b",
				"line3: c",
				"line4: d",
				"line5: e",
				"line6: f",
			),
			after: stringtest.LinesLF(
				"line1: X",
				"line2: b",
				"line3: c",
				"line4: d",
				"line5: e",
				"line6: Y",
			),
			context: 0,
			want: stringtest.JoinLF(
				"      @@ -1 +1 @@",
				"   1 -line1: a",
				"   1 +line1: X",
				"      @@ -6 +6 @@",
				"   6 -line6: f",
				"   6 +line6: Y",
			),
		},
		"addition only": {
			before: stringtest.LinesLF(
				"a: 1",
				"c: 3",
			),
			after: stringtest.LinesLF(
				"a: 1",
				"b: 2",
				"c: 3",
			),
			context: 1,
			want: stringtest.JoinLF(
				"      @@ -1,2 +1,3 @@",
				"   1  a: 1",
				"   2 +b: 2",
				"   3  c: 3",
			),
		},
		"deletion only": {
			before: stringtest.LinesLF(
				"a: 1",
				"b: 2",
				"c: 3",
			),
			after: stringtest.LinesLF(
				"a: 1",
				"c: 3",
			),
			context: 1,
			want: stringtest.JoinLF(
				"      @@ -1,3 +1,2 @@",
				"   1  a: 1",
				"   2 -b: 2",
				"   2  c: 3",
			),
		},
		"empty files": {
			before:  "",
			after:   "",
			context: 1,
		},
		"context larger than ops length includes all lines": {
			before: stringtest.LinesLF(
				"a: 1",
				"b: 2",
				"c: 3",
			),
			after: stringtest.LinesLF(
				"a: 1",
				"b: changed",
				"c: 3",
			),
			context: 100,
			want: stringtest.JoinLF(
				"      @@ -1,3 +1,3 @@",
				"   1  a: 1",
				"   2 -b: 2",
				"   2 +b: changed",
				"   3  c: 3",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := testPrinterWithGutter(printer.DefaultGutter)

			assert.Equal(t, tc.want, p.Print(yamltest.Diff(tc.before, tc.after).Hunks(tc.context)))
		})
	}
}

func TestFinderPrinter_Integration(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input  string
		search string
		want   string
	}{
		"simple match": {
			input:  "key: value",
			search: "value",
			want:   "key: [value]",
		},
		"single character in word": {
			input:  "foobar",
			search: "o",
			// Adjacent styled ranges merge in the Printer.
			want: "f[oo]bar",
		},
		"multiple matches same line": {
			input:  "key: abcabc",
			search: "abc",
			// Adjacent styled ranges merge in the Printer.
			want: "key: [abcabc]",
		},
		"match at start": {
			input:  "key: value",
			search: "key",
			want:   "[key]: value",
		},
		"match spanning tokens": {
			input:  "key: value",
			search: ": v",
			want:   "key[:][ ][v]alue",
		},
		"multi-line matches": {
			input:  stringtest.JoinLF("a: test", "b: test"),
			search: "test",
			want:   stringtest.JoinLF("a: [test]", "b: [test]"),
		},
		"utf8 - search after multibyte char": {
			input:  "name: Thaïs test",
			search: "test",
			want:   "name: Thaïs [test]",
		},
		"utf8 - search for multibyte char": {
			input:  "name: Thaïs",
			search: "ï",
			want:   "name: Tha[ï]s",
		},
		"utf8 - search spanning multibyte": {
			input:  "name: Thaïs",
			search: "ïs",
			want:   "name: Tha[ïs]",
		},
		"utf8 - multiple multibyte chars": {
			input:  "key: über öffentlich",
			search: "ö",
			want:   "key: über [ö]ffentlich",
		},
		"utf8 - text after multiple multibyte": {
			input:  "key: über öffentlich test",
			search: "test",
			want:   "key: über öffentlich [test]",
		},
		"utf8 - normalizer diacritic to ascii": {
			input:  "name: Thaïs",
			search: "Thais",
			want:   "name: [Thaïs]",
		},
		"utf8 - case insensitive with diacritics": {
			input:  "name: THAÏS test",
			search: "thais",
			want:   "name: [THAÏS] test",
		},
		"utf8 - search ascii finds normalized diacritic": {
			input:  "key: über",
			search: "u",
			want:   "key: [ü]ber",
		},
		"utf8 - normalizer search after multiple multibyte": {
			input:  "key: über Yamüll test",
			search: "ya",
			want:   "key: über [Ya]müll test",
		},
		"utf8 - japanese characters": {
			input:  "名前: テスト value",
			search: "value",
			want:   "名前: テスト [value]",
		},
		"utf8 - emoji": {
			input:  "status: ✓ done",
			search: "done",
			want:   "status: ✓ [done]",
		},
		"complex yaml with utf8": {
			input:  stringtest.JoinLF("metadata:", "  name: Thaïs", "  namespace: über"),
			search: "name",
			want:   stringtest.JoinLF("metadata:", "  [name]: Thaïs", "  [name]space: über"),
		},
		"utf8 - japanese partial match": {
			input:  "key: 日本酒",
			search: "日本",
			want:   "key: [日本]酒",
		},
		"utf8 - japanese after other japanese": {
			input:  "- 寿司: 日本酒",
			search: "日本",
			want:   "- 寿司: [日本]酒",
		},
		"utf8 - multiline with japanese": {
			input:  stringtest.JoinLF("a: test", "- 寿司: 日本酒"),
			search: "日本",
			want:   stringtest.JoinLF("a: test", "- 寿司: [日本]酒"),
		},
		"utf8 - multiple japanese on different lines": {
			input:  stringtest.JoinLF("a: 日本", "b: 日本酒"),
			search: "日本",
			want:   stringtest.JoinLF("a: [日本]", "b: [日本]酒"),
		},
		"no match": {
			input:  "key: value",
			search: "notfound",
			want:   "key: value",
		},
		"empty search": {
			input:  "key: value",
			search: "",
			want:   "key: value",
		},
		"empty file": {
			input:  "",
			search: "test",
			want:   "",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)
			ranges := finder.New().Load(source.Lines()).Find(tc.search)

			// A search finds no range exactly when the print leaves the
			// input as it was.
			assert.Equal(t, tc.want == tc.input, len(ranges) == 0, "ranges: %v", ranges)

			view := source.View()
			view.AddOverlay(testOverlayHighlight, ranges...)

			assert.Equal(t, tc.want, testPrinter().Print(view))
		})
	}
}

func TestPrinter_With(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("key: this is a very long value that should wrap")

	base := testPrinterWithGutter(printer.NoGutter)
	narrow := base.With(printer.WithWrapWidth(20))

	assert.Equal(t, 0, base.WrapWidth())
	assert.Equal(t, 20, narrow.WrapWidth())

	// A negative width disables wrapping, and WrapWidth reports that as 0.
	assert.Equal(t, 0, base.With(printer.WithWrapWidth(-5)).WrapWidth())

	// A negative container width pins no width, so ContainerWidth reports
	// 0 for it.
	assert.Equal(t, 0, base.ContainerWidth())
	assert.Equal(t, 40, base.With(printer.WithContainerWidth(40)).ContainerWidth())
	assert.Equal(t, 0, base.With(printer.WithContainerWidth(-1)).ContainerWidth())

	// The receiver still renders on one line.
	assert.Equal(t, "key: this is a very long value that should wrap", base.Print(source.View()))

	// A copy can switch styles and gets its own container style.
	styled := base.With(printer.WithStyles(yamltest.NewXMLStyles()))
	assert.Contains(t, styled.Print(source.View()), "<nameTag>key</nameTag>")
	assert.NotContains(t, base.Print(source.View()), "<nameTag>")
}

func TestPrinter_Golden(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		styles    style.Styler
		gutter    printer.Gutter
		setupFunc func(*line.View)
		wrap      int
	}{
		"default colors": {
			styles: theme.Charm.Styles(),
			gutter: printer.NoGutter,
		},
		"word wrap with colors": {
			styles: theme.Charm.Styles(),
			gutter: printer.DefaultGutter,
			wrap:   40,
		},
		"default colors with line numbers": {
			styles: theme.Charm.Styles(),
			gutter: printer.DefaultGutter,
		},
		"no colors": {
			styles: style.Styles{},
			gutter: printer.NoGutter,
		},
		"no colors with line numbers": {
			styles: style.Styles{},
			gutter: printer.DefaultGutter,
		},
		"find and highlight": {
			styles: theme.Charm.Styles().With(
				style.Set(testOverlayHighlight, lipgloss.NewStyle().
					Background(lipgloss.Color("#FFFF00")).
					Foreground(lipgloss.Color("#000000"))),
			),
			gutter: printer.NoGutter,
			setupFunc: func(view *line.View) {
				// Search for "日本" (Japan) which appears multiple times in full.yaml.
				ranges := finder.New().Load(view).Find("日本")
				view.AddOverlay(testOverlayHighlight, ranges...)
			},
		},
	}

	input := yamltest.ReadFile(t, "../testdata/full.yaml")

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := niceyaml.NewSourceFromString(input).View()
			p := testPrinterWithGutter(tc.gutter).With(
				printer.WithStyles(tc.styles),
				printer.WithWrapWidth(tc.wrap),
			)

			if tc.setupFunc != nil {
				tc.setupFunc(lines)
			}

			output := p.Print(lines)
			golden.RequireEqual(t, output)
		})
	}
}

func TestPrinter_BlendStyles(t *testing.T) {
	t.Parallel()

	type overlayRange struct {
		kind kind.Kind
		rng  position.Range
	}

	// Overlay kinds, each named for the tag it wraps its text in.
	const (
		kindHL   kind.Kind = "hl"
		kindAll  kind.Kind = "all"
		kindA    kind.Kind = "a"
		kindB    kind.Kind = "b"
		kindC    kind.Kind = "c"
		kindX    kind.Kind = "x"
		kindY    kind.Kind = "y"
		kindSpan kind.Kind = "span"
	)

	// Only the overlay kinds get tags, so the tokens under them show as
	// plain text.
	styles := yamltest.NewXMLStyles(yamltest.XMLStyleInclude(
		kindHL, kindAll, kindA, kindB, kindC, kindX, kindY, kindSpan,
	))

	tcs := map[string]struct {
		input  string
		want   string
		ranges []overlayRange
	}{
		"single range on value": {
			input: "key: value",
			ranges: []overlayRange{
				{kindHL, yamltest.LineRange(0, 5, 10)},
			},
			want: "key: <hl>value</hl>",
		},
		"single range on key": {
			input: "key: value",
			ranges: []overlayRange{
				{kindHL, yamltest.LineRange(0, 0, 3)},
			},
			want: "<hl>key</hl>: value",
		},
		"full line range": {
			// Each token renders on its own, so transforms apply per-token.
			input: "key: value",
			ranges: []overlayRange{
				{kindAll, yamltest.LineRange(0, 0, 10)},
			},
			want: "<all>key</all><all>:</all><all> </all><all>value</all>",
		},
		"non-overlapping ranges": {
			input: "key: value",
			ranges: []overlayRange{
				{kindA, yamltest.LineRange(0, 0, 3)},
				{kindB, yamltest.LineRange(0, 5, 10)},
			},
			want: "<a>key</a>: <b>value</b>",
		},
		"adjacent ranges": {
			input: "key: value",
			ranges: []overlayRange{
				{kindA, yamltest.LineRange(0, 0, 3)},
				{kindB, yamltest.LineRange(0, 3, 4)},
			},
			want: "<a>key</a><b>:</b> value",
		},
		"overlapping ranges - transforms compose": {
			// First range [0,5) gets override, second range [2,7) blends.
			// Blending composes transforms: overlay(base(text)).
			// Each token renders on its own.
			input: "key: value",
			ranges: []overlayRange{
				{kindA, yamltest.LineRange(0, 0, 5)},
				{kindB, yamltest.LineRange(0, 2, 7)},
			},
			// Token "key" [0,3): positions 0-1 only <a>, position 2 both <b><a>
			// Token ":" [3,4): both <b><a>
			// Token " " [4,5): both <b><a>
			// Token "value" [5,10): positions 5-6 only <b>, positions 7-9 none.
			want: "<a>ke</a><b><a>y</a></b><b><a>:</a></b><b><a> </a></b><b>va</b>lue",
		},
		"three overlapping ranges": {
			// Ranges: a=[0,6), b=[2,8), c=[4,10)
			// Each token renders on its own with overlapping transforms.
			input: "key: value",
			ranges: []overlayRange{
				{kindA, yamltest.LineRange(0, 0, 6)},
				{kindB, yamltest.LineRange(0, 2, 8)},
				{kindC, yamltest.LineRange(0, 4, 10)},
			},
			// Token "key" [0,3): 0-1 <a>, 2 <b><a>
			// Token ":" [3,4): <b><a>
			// Token " " [4,5): <c><b><a>
			// Token "value" [5,10): 5 <c><b><a>, 6-7 <c><b>, 8-9 <c>.
			want: "<a>ke</a><b><a>y</a></b><b><a>:</a></b><c><b><a> </a></b></c><c><b><a>v</a></b></c><c><b>al</b></c><c>ue</c>",
		},
		"partial character overlap": {
			// "abcdef" is a single token that the printer styles character-by-character.
			input: "abcdef",
			ranges: []overlayRange{
				{kindX, yamltest.LineRange(0, 1, 4)},
				{kindY, yamltest.LineRange(0, 3, 5)},
			},
			// Position 0: no style
			// Positions 1-2: only <x>
			// Position 3: <y> wraps <x>
			// Position 4: only <y> (style changes, new span)
			// Position 5: no style.
			want: "a<x>bc</x><y><x>d</x></y><y>e</y>f",
		},
		"multi-line with ranges on each line": {
			input: stringtest.JoinLF("a: 1", "b: 2"),
			ranges: []overlayRange{
				{kindX, yamltest.LineRange(0, 0, 1)},
				{kindY, yamltest.LineRange(1, 0, 1)},
			},
			want: stringtest.JoinLF("<x>a</x>: 1", "<y>b</y>: 2"),
		},
		"range spanning multiple lines": {
			input: stringtest.JoinLF("a: 1", "b: 2"),
			ranges: []overlayRange{
				{kindSpan, position.NewRange(position.New(0, 3), position.New(1, 1))},
			},
			want: stringtest.JoinLF("a: <span>1</span>", "<span>b</span>: 2"),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := niceyaml.NewSourceFromString(tc.input).View()
			for _, or := range tc.ranges {
				view.BlendOverlay(or.kind, or.rng)
			}

			assert.Equal(t, tc.want, yamltest.NewXMLPrinter(printer.WithStyles(styles)).Print(view))
		})
	}
}

func TestPrinter_ColorBlending_Golden(t *testing.T) {
	t.Parallel()

	// These tests exercise the color blending paths with actual lipgloss
	// colors instead of transforms.

	type overlayDef struct {
		style lipgloss.Style
		rng   position.Range
	}

	const (
		colorKind1 kind.Kind = "colorKind1"
		colorKind2 kind.Kind = "colorKind2"
		colorKind3 kind.Kind = "colorKind3"
	)

	tcs := map[string]struct {
		overlays []overlayDef
	}{
		"ForegroundBlend": {
			// Where the two ranges overlap, the printer blends the colors via LAB.
			overlays: []overlayDef{
				{lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000")), yamltest.LineRange(0, 0, 5)},
				{lipgloss.NewStyle().Foreground(lipgloss.Color("#0000FF")), yamltest.LineRange(0, 3, 10)},
			},
		},
		"BackgroundBlend": {
			overlays: []overlayDef{
				{lipgloss.NewStyle().Background(lipgloss.Color("#00FF00")), yamltest.LineRange(0, 0, 5)},
				{lipgloss.NewStyle().Background(lipgloss.Color("#FF00FF")), yamltest.LineRange(0, 3, 10)},
			},
		},
		"FirstColorOnly": {
			// The second style has NoColor, so the printer uses the first
			// color directly.
			overlays: []overlayDef{
				{lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000")), yamltest.LineRange(0, 0, 5)},
				{lipgloss.NewStyle(), yamltest.LineRange(0, 3, 10)},
			},
		},
		"SecondColorOnly": {
			// The first style has NoColor, so the printer uses the second color.
			overlays: []overlayDef{
				{lipgloss.NewStyle(), yamltest.LineRange(0, 0, 5)},
				{lipgloss.NewStyle().Foreground(lipgloss.Color("#0000FF")), yamltest.LineRange(0, 3, 10)},
			},
		},
		"BothNoColor": {
			// Both styles have NoColor, so the blend yields nil and applies no color.
			overlays: []overlayDef{
				{lipgloss.NewStyle(), yamltest.LineRange(0, 0, 5)},
				{lipgloss.NewStyle(), yamltest.LineRange(0, 3, 10)},
			},
		},
		"ThreeOverlapping": {
			// Three ranges overlap, so the printer blends all three colors.
			overlays: []overlayDef{
				{lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000")), yamltest.LineRange(0, 0, 6)},
				{lipgloss.NewStyle().Foreground(lipgloss.Color("#00FF00")), yamltest.LineRange(0, 2, 8)},
				{lipgloss.NewStyle().Foreground(lipgloss.Color("#0000FF")), yamltest.LineRange(0, 4, 10)},
			},
		},
		"MixedFgBg": {
			// The printer blends foreground and background colors independently.
			overlays: []overlayDef{
				{
					lipgloss.NewStyle().
						Foreground(lipgloss.Color("#FF0000")).
						Background(lipgloss.Color("#00FF00")),
					yamltest.LineRange(0, 0, 6),
				},
				{
					lipgloss.NewStyle().
						Foreground(lipgloss.Color("#0000FF")).
						Background(lipgloss.Color("#FFFF00")),
					yamltest.LineRange(0, 3, 10),
				},
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := niceyaml.NewSourceFromString("key: value").View()

			kinds := []kind.Kind{colorKind1, colorKind2, colorKind3}

			overlayOpts := make([]style.Option, 0, len(tc.overlays))
			for i, od := range tc.overlays {
				overlayOpts = append(overlayOpts, style.Set(kinds[i], od.style))
				view.BlendOverlay(kinds[i], od.rng)
			}

			golden.RequireEqual(t, testPrinterWithStyles(overlayOpts...).Print(view))
		})
	}
}

func TestDefaultAnnotation(t *testing.T) {
	t.Parallel()

	// Below returns an annotation below the line that labels the columns
	// from start up to end with msg.
	below := func(start, end int, msg string) line.Annotation {
		return line.Annotation{Content: msg, Placement: line.Below, Cols: position.NewSpan(start, end)}
	}

	tcs := map[string]struct {
		content     string
		annotations line.Annotations
		overlays    line.Overlays
		rowStarts   []int
		rowEnds     []int
		above       bool // The context sits above the line rather than below it.
		want        []printer.AnnotationRow
	}{
		"empty annotations": {
			annotations: line.Annotations{},
		},
		"empty content marks its columns": {
			content:     "  sla: 99",
			annotations: line.Annotations{below(7, 9, "")},
			want:        []printer.AnnotationRow{{Col: 7, Marker: "^^"}},
		},
		// The overlays style the line and the annotations alone draw
		// carets, so a blend and a replacing overlay alike draw none.
		"overlays draw no carets": {
			content:     "name: other value",
			annotations: line.Annotations{below(6, 6, "")},
			overlays: line.Overlays{
				{Cols: position.NewSpan(6, 17)},
				{Cols: position.NewSpan(0, 4), Blend: true},
			},
			want: []printer.AnnotationRow{{Col: 6, Marker: "^"}},
		},
		"runs without content get a row each": {
			content:     "a: 1, b: 2",
			annotations: line.Annotations{below(9, 10, ""), below(3, 4, "")},
			want: []printer.AnnotationRow{
				{Col: 3, Marker: "^"},
				{Col: 9, Marker: "^"},
			},
		},
		"empty content marks wide runes with two carets": {
			content:     "日本語: 値",
			annotations: line.Annotations{below(0, 3, "")},
			want:        []printer.AnnotationRow{{Col: 0, Marker: "^^^^^^"}},
		},
		"empty content marks a column after a combining mark": {
			content:     "e\u0301x",
			annotations: line.Annotations{below(2, 3, "")},
			want:        []printer.AnnotationRow{{Col: 2, Marker: "^"}},
		},
		"empty content marks a flag after a flag": {
			content:     "flags: 🇺🇸🇫🇷",
			annotations: line.Annotations{below(9, 11, "")},
			want:        []printer.AnnotationRow{{Col: 9, Marker: "^^"}},
		},
		"empty content marks control characters as one cell": {
			content:     "a: \x07b",
			annotations: line.Annotations{below(3, 5, "")},
			want:        []printer.AnnotationRow{{Col: 3, Marker: "^^"}},
		},
		"a run past the content stops at its end": {
			content:     "a: 1",
			annotations: line.Annotations{below(3, 9, "")},
			want:        []printer.AnnotationRow{{Col: 3, Marker: "^"}},
		},
		"a run that starts before column zero starts at it": {
			content:     "a: 1",
			annotations: line.Annotations{below(-2, 2, "")},
			want:        []printer.AnnotationRow{{Col: 0, Marker: "^^"}},
		},
		"an empty run marks its column": {
			content:     "a: 1",
			annotations: line.Annotations{below(4, 4, "")},
			want:        []printer.AnnotationRow{{Col: 4, Marker: "^"}},
		},
		"an empty run marks a wide rune": {
			content:     "a: 日本",
			annotations: line.Annotations{below(3, 3, "")},
			want:        []printer.AnnotationRow{{Col: 3, Marker: "^^"}},
		},
		"a run wholly past the content marks its start": {
			content:     "a: 1",
			annotations: line.Annotations{below(4, 6, "")},
			want:        []printer.AnnotationRow{{Col: 4, Marker: "^"}},
		},
		"empty content with a negative column marks column zero": {
			content:     "a: 1",
			annotations: line.Annotations{below(-3, -3, "")},
			want:        []printer.AnnotationRow{{Col: 0, Marker: "^"}},
		},
		"empty content above the line renders nothing": {
			content:     "a: 1",
			annotations: line.Annotations{{Placement: line.Above, Cols: position.NewSpan(3, 4)}},
			above:       true,
		},
		"content follows the carets of its run": {
			content:     "  sla: 99",
			annotations: line.Annotations{below(7, 9, "bad")},
			want:        []printer.AnnotationRow{{Col: 7, Marker: "^^ ", Text: "bad"}},
		},
		"each run gets a row of its own in column order": {
			content:     "a: one, b: two",
			annotations: line.Annotations{below(11, 14, "second"), below(3, 6, "first")},
			want: []printer.AnnotationRow{
				{Col: 3, Marker: "^^^ ", Text: "first"},
				{Col: 11, Marker: "^^^ ", Text: "second"},
			},
		},
		"runs that start at one column come shorter first": {
			content:     "a: one, b: two",
			annotations: line.Annotations{below(3, 14, "long"), below(3, 6, "short")},
			want: []printer.AnnotationRow{
				{Col: 3, Marker: "^^^ ", Text: "short"},
				{Col: 3, Marker: "^^^^^^^^^^^ ", Text: "long"},
			},
		},
		"content marks its column past the end of the line": {
			content:     "a: 1",
			annotations: line.Annotations{below(6, 6, "here")},
			want:        []printer.AnnotationRow{{Col: 6, Marker: "^ ", Text: "here"}},
		},
		"content marks a far column at the bound past the end of the line": {
			content:     "k: v",
			annotations: line.Annotations{below(math.MaxInt, math.MaxInt, "x")},
			want:        []printer.AnnotationRow{{Col: 4 + line.MaxColPastEnd, Marker: "^ ", Text: "x"}},
		},
		"content marks wide runes with two carets each": {
			content:     "k: 日本, v: 語",
			annotations: line.Annotations{below(3, 5, "bad")},
			want:        []printer.AnnotationRow{{Col: 3, Marker: "^^^^ ", Text: "bad"}},
		},
		"single below annotation": {
			annotations: line.Annotations{below(0, 0, "error here")},
			want:        []printer.AnnotationRow{{Col: 0, Marker: "^ ", Text: "error here"}},
		},
		"single below annotation at a column": {
			annotations: line.Annotations{below(5, 5, "error")},
			want:        []printer.AnnotationRow{{Col: 5, Marker: "^ ", Text: "error"}},
		},
		"empty content keeps its caret beside a message on another run": {
			annotations: line.Annotations{below(0, 0, ""), below(5, 5, "boom")},
			want: []printer.AnnotationRow{
				{Col: 0, Marker: "^"},
				{Col: 5, Marker: "^ ", Text: "boom"},
			},
		},
		"single above annotation": {
			annotations: line.Annotations{{Content: "@@ hunk @@", Placement: line.Above}},
			above:       true,
			want:        []printer.AnnotationRow{{Col: 0, Text: "@@ hunk @@"}},
		},
		"above clamps a negative start": {
			content:     "abc",
			annotations: line.Annotations{{Content: "header", Placement: line.Above, Cols: position.NewSpan(-5, 2)}},
			above:       true,
			want:        []printer.AnnotationRow{{Col: 0, Text: "header"}},
		},
		"above clamps a start far past the end": {
			content: "abc",
			annotations: line.Annotations{
				{Content: "header", Placement: line.Above, Cols: position.NewSpan(math.MaxInt, math.MaxInt)},
			},
			above: true,
			want:  []printer.AnnotationRow{{Col: 3 + line.MaxColPastEnd, Text: "header"}},
		},
		"single above annotation at a column": {
			annotations: line.Annotations{{Content: "header", Placement: line.Above, Cols: position.NewSpan(3, 9)}},
			above:       true,
			want:        []printer.AnnotationRow{{Col: 3, Text: "header"}},
		},
		"messages on separate runs get a row each": {
			annotations: line.Annotations{below(5, 5, "first"), below(2, 2, "second")},
			want: []printer.AnnotationRow{
				{Col: 2, Marker: "^ ", Text: "second"},
				{Col: 5, Marker: "^ ", Text: "first"},
			},
		},
		"multiple above annotations": {
			annotations: line.Annotations{
				{Content: "header1", Placement: line.Above},
				{Content: "header2", Placement: line.Above},
			},
			above: true,
			want:  []printer.AnnotationRow{{Col: 0, Text: "header1; header2"}},
		},
		"empty content without overlays marks its column": {
			annotations: line.Annotations{below(2, 2, "")},
			want:        []printer.AnnotationRow{{Col: 2, Marker: "^"}},
		},
		"messages on one run share its row without the empty content": {
			annotations: line.Annotations{below(2, 2, "first"), below(2, 2, ""), below(2, 2, "third")},
			want:        []printer.AnnotationRow{{Col: 2, Marker: "^ ", Text: "first; third"}},
		},
		"control characters render as pictures": {
			annotations: line.Annotations{{Content: "a\nb\x1b[31m", Placement: line.Below}},
			want:        []printer.AnnotationRow{{Col: 0, Marker: "^ ", Text: "a\u240ab\u241b[31m"}},
		},
		"wrapped content marks each run under its own columns": {
			content:     "key: aaaa bbbb cccc dddd",
			annotations: line.Annotations{below(5, 9, ""), below(17, 19, "")},
			rowStarts:   []int{0, 10, 20},
			want: []printer.AnnotationRow{
				{Col: 5, Marker: "^^^^"},
				{Col: 17, Marker: "^^"},
			},
		},
		"wrapped content leaves the space dropped at a break unmarked": {
			content:     "key: aaaa bbbb",
			annotations: line.Annotations{below(5, 14, "")},
			rowStarts:   []int{0, 10},
			want: []printer.AnnotationRow{
				{Col: 5, Marker: "^^^^"},
				{Col: 10, Marker: "^^^^"},
			},
		},
		"wrapped content leaves the spaces dropped at the end unmarked": {
			content:     "key: aaaa bb  ",
			annotations: line.Annotations{below(5, 14, "")},
			rowStarts:   []int{0, 10},
			rowEnds:     []int{9, 12},
			want: []printer.AnnotationRow{
				{Col: 5, Marker: "^^^^"},
				{Col: 10, Marker: "^^"},
			},
		},
		"wrapped content marks a tab at a break as its picture": {
			content:     "k: ab\tcd",
			annotations: line.Annotations{below(3, 8, "")},
			rowStarts:   []int{0, 6},
			want: []printer.AnnotationRow{
				{Col: 3, Marker: "^^^"},
				{Col: 6, Marker: "^^"},
			},
		},
		"wrapped content marks wide runes on each row": {
			content:     "k: 日本語 日本語",
			annotations: line.Annotations{below(3, 10, "")},
			rowStarts:   []int{0, 7},
			want: []printer.AnnotationRow{
				{Col: 3, Marker: "^^^^^^"},
				{Col: 7, Marker: "^^^^^^"},
			},
		},
		"wrapped content marks combining marks on each row": {
			content:     "k: e\u0301x e\u0301y",
			annotations: line.Annotations{below(4, 10, "")},
			rowStarts:   []int{0, 7},
			want: []printer.AnnotationRow{
				{Col: 3, Marker: "^^"},
				{Col: 7, Marker: "^^"},
			},
		},
		"wrapped content leaves out a row without covered columns": {
			content:     "key: aaaa bbbb cccc dddd",
			annotations: line.Annotations{below(20, 22, "")},
			rowStarts:   []int{0, 10, 20},
			want:        []printer.AnnotationRow{{Col: 20, Marker: "^^"}},
		},
		"wrapped content puts the text beside the row that holds the start of its run": {
			content:     "key: aaaa bbbb",
			annotations: line.Annotations{below(10, 14, "bad")},
			rowStarts:   []int{0, 10},
			want:        []printer.AnnotationRow{{Col: 10, Marker: "^^^^ ", Text: "bad"}},
		},
		"wrapped content puts the text on the first row of a range across a break": {
			content:     "key: aaaa bbbb cccc dddd",
			annotations: line.Annotations{below(5, 24, "bad")},
			rowStarts:   []int{0, 10, 20},
			want: []printer.AnnotationRow{
				{Col: 5, Marker: "^^^^ ", Text: "bad"},
				{Col: 10, Marker: "^^^^^^^^^"},
				{Col: 20, Marker: "^^^^"},
			},
		},
		// A message sits beside the wrapped row that holds the start of its
		// own run, so each message of a wrapped line reads under its run.
		"wrapped content puts each message beside its own row": {
			content:     "key: aaaa bbbb cccc dddd",
			annotations: line.Annotations{below(20, 24, "third"), below(5, 9, "first"), below(10, 14, "second")},
			rowStarts:   []int{0, 10, 20},
			want: []printer.AnnotationRow{
				{Col: 5, Marker: "^^^^ ", Text: "first"},
				{Col: 10, Marker: "^^^^ ", Text: "second"},
				{Col: 20, Marker: "^^^^ ", Text: "third"},
			},
		},
		"wrapped content keeps a single caret for a run that starts on a dropped space": {
			content:     "key: aaaa bbbb",
			annotations: line.Annotations{below(9, 14, "bad")},
			rowStarts:   []int{0, 10},
			want: []printer.AnnotationRow{
				{Col: 9, Marker: "^ ", Text: "bad"},
				{Col: 10, Marker: "^^^^"},
			},
		},
		"wrapped content clamps a negative row start and end": {
			content:     "abc",
			annotations: line.Annotations{below(0, 3, "")},
			rowStarts:   []int{-2},
			rowEnds:     []int{-1},
			want:        []printer.AnnotationRow{{Col: 0, Marker: "^"}},
		},
		"wrapped content clamps negative row starts without ends": {
			content:     "abc",
			annotations: line.Annotations{below(0, 3, "")},
			rowStarts:   []int{-5, -2},
			want:        []printer.AnnotationRow{{Col: 0, Marker: "^^^"}},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			placement := line.Below
			if tc.above {
				placement = line.Above
			}

			got := printer.DefaultAnnotation(printer.AnnotationContext{
				Content:     tc.content,
				Annotations: tc.annotations,
				Overlays:    tc.overlays,
				RowStarts:   tc.rowStarts,
				RowEnds:     tc.rowEnds,
				Placement:   placement,
			})
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPrinter_AnnotationFuncOverlays(t *testing.T) {
	t.Parallel()

	// An AnnotationFunc may change the overlays its context holds without
	// changing the view, the next print, or the context of another group.
	newView := func(anns ...line.Annotation) *line.View {
		view := niceyaml.NewSourceFromString("key: value\n").View()
		view.AddLineOverlay(0,
			line.Overlay{Kind: kind.GenericHeadingOK, Cols: position.NewSpan(5, 7)},
			line.Overlay{Kind: kind.GenericHeadingWarn, Cols: position.NewSpan(0, 10)},
		)
		view.Annotate(0, anns...)

		return view
	}

	t.Run("sorting leaves the view as it was", func(t *testing.T) {
		t.Parallel()

		view := newView(line.Annotation{Content: "x", Placement: line.Below})
		want := slices.Clone(view.Overlays(0))

		sortByStart := func(ctx printer.AnnotationContext) []printer.AnnotationRow {
			slices.SortFunc(ctx.Overlays, func(a, b line.Overlay) int {
				return cmp.Compare(a.Cols.Start, b.Cols.Start)
			})

			return printer.DefaultAnnotation(ctx)
		}

		p := yamltest.NewXMLPrinter(printer.WithAnnotation(sortByStart))

		first := p.Print(view)
		assert.Equal(t, first, p.Print(view))
		assert.Equal(t, want, view.Overlays(0))
	})

	t.Run("each group gets its own copy", func(t *testing.T) {
		t.Parallel()

		view := newView(
			line.Annotation{Content: "a", Placement: line.Below},
			line.Annotation{Content: "b", Kind: kind.TextError, Placement: line.Below},
		)
		want := slices.Clone(view.Overlays(0))

		var seen []line.Overlays

		clearOverlays := func(ctx printer.AnnotationContext) []printer.AnnotationRow {
			seen = append(seen, slices.Clone(ctx.Overlays))
			clear(ctx.Overlays)

			return printer.DefaultAnnotation(ctx)
		}

		printer.New(printer.WithAnnotation(clearOverlays)).Print(view)

		assert.Equal(t, []line.Overlays{want, want}, seen)
		assert.Equal(t, want, view.Overlays(0))
	})
}

func TestPrinter_AnnotationKind(t *testing.T) {
	t.Parallel()

	// Annotations of one kind on one run share a row, and every kind
	// renders as rows of its own in its style. Below the line, the rows of
	// errors come first, then those of warnings, then those of the other
	// kinds. The zero kind renders as kind.UIAnnotation.
	view := niceyaml.NewSourceFromString("key: value").View()
	view.Annotate(
		0,
		line.Annotation{Content: "hunk", Placement: line.Above},
		line.Annotation{Content: "note", Placement: line.Below, Cols: position.NewSpan(5, 5)},
		line.Annotation{Content: "old key", Kind: kind.TextWarn, Placement: line.Below, Cols: position.NewSpan(0, 3)},
		line.Annotation{Content: "bad key", Kind: kind.TextError, Placement: line.Below, Cols: position.NewSpan(0, 0)},
		line.Annotation{
			Content:   "bad value",
			Kind:      kind.TextError,
			Placement: line.Below,
			Cols:      position.NewSpan(5, 5),
		},
		line.Annotation{Content: "hint", Kind: kind.UIAnnotation, Placement: line.Below, Cols: position.NewSpan(5, 5)},
	)

	p := yamltest.NewXMLPrinter()

	want := stringtest.JoinLF(
		"<uiAnnotation>hunk</uiAnnotation>",
		"<nameTag>key</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
		"<textError>^ bad key</textError>",
		"<textError>     ^ bad value</textError>",
		"<textWarn>^^^ old key</textWarn>",
		"<uiAnnotation>     ^ note; hint</uiAnnotation>",
	)

	assert.Equal(t, want, p.Print(view))
	assert.Equal(t, []int{6}, layoutRows(p.Layout(view)))
}

func TestPrinter_AnnotationKindAbove(t *testing.T) {
	t.Parallel()

	// Above the line, the kinds come in the order they first appear, as
	// View.String renders them. A warning added before an error thus stays
	// above it.
	view := niceyaml.NewSourceFromString("key: value").View()
	view.Annotate(
		0,
		line.Annotation{Content: "old key", Kind: kind.TextWarn, Placement: line.Above},
		line.Annotation{Content: "bad key", Kind: kind.TextError, Placement: line.Above},
	)

	p := yamltest.NewXMLPrinter()

	want := stringtest.JoinLF(
		"<textWarn>old key</textWarn>",
		"<textError>bad key</textError>",
		"<nameTag>key</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
	)

	assert.Equal(t, want, p.Print(view))
	assert.Equal(t, stringtest.JoinLF(
		"     | old key",
		"     | bad key",
		"   1 | key: value",
	), view.String())
}

func TestPrinter_AnnotationFuncKind(t *testing.T) {
	t.Parallel()

	view := niceyaml.NewSourceFromString("key: value").View()
	view.Annotate(0, line.Annotation{Content: "oops \x1b[31mred", Placement: line.Below})

	errored := func(ctx printer.AnnotationContext) []printer.AnnotationRow {
		return []printer.AnnotationRow{{
			Text: strings.Join(ctx.Annotations.Contents(), "; "),
			Kind: kind.TextError,
		}}
	}

	p := testPrinterWithStyles(style.Set(kind.TextError, lipgloss.NewStyle().Bold(true))).
		With(printer.WithAnnotation(errored))

	// The row renders in the Kind the func chose, and the printer escapes
	// the text, so an escape sequence in a message shows as its picture
	// rather than styling the output.
	got := p.Print(view)
	assert.Contains(t, got, "\x1b[1moops \u241b[31mred")
	assert.NotContains(t, got, "\x1b[31m")
}

func TestPrinter_Annotation(t *testing.T) {
	t.Parallel()

	// Joins the contents with no column and no marker, so continuation
	// rows carry no indent.
	bare := func(ctx printer.AnnotationContext) []printer.AnnotationRow {
		return []printer.AnnotationRow{{Text: strings.Join(ctx.Annotations.Contents(), " ")}}
	}

	// Marks the annotations with a marker of its own at their column.
	arrow := func(ctx printer.AnnotationContext) []printer.AnnotationRow {
		return []printer.AnnotationRow{{
			Col:    ctx.Annotations.Start(),
			Marker: "-> ",
			Text:   strings.Join(ctx.Annotations.Contents(), " "),
		}}
	}

	// A func that draws a marker of its own for each placement.
	custom := func(ctx printer.AnnotationContext) []printer.AnnotationRow {
		row := printer.AnnotationRow{
			Col:    ctx.Annotations.Start(),
			Marker: "=== ",
			Text:   strings.Join(ctx.Annotations.Contents(), ", "),
		}

		if ctx.Placement == line.Below {
			row.Marker = ">>> "
		}

		return []printer.AnnotationRow{row}
	}

	// A block scalar whose one content line is a word wider than the wrap.
	crt := stringtest.JoinLF(
		"crt: |",
		"    LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSUMr",
	)

	tcs := map[string]struct {
		annFunc    printer.AnnotationFunc
		gutter     printer.Gutter // Nil for no gutter.
		input      string
		want       string
		annotation line.Annotation // The zero Annotation adds none.
		// Ranges that each get an error overlay and an annotation without
		// content below their line that labels the same columns, as an
		// excerpt marks a location.
		marks []position.Range
		line  int // Index of the line the annotation is on.
		width int
	}{
		"below rows align under the marker within the width": {
			input:  "key: value",
			gutter: printer.LineNumberGutter,
			width:  40,
			annotation: line.Annotation{
				Content:   "this is a fairly long annotation message that will wrap",
				Placement: line.Below,
				Cols:      position.NewSpan(20, 20),
			},
			want: stringtest.JoinLF(
				"   1 key: value",
				strings.Repeat(" ", 25)+"^ this is a",
				strings.Repeat(" ", 27)+"fairly long",
				strings.Repeat(" ", 27)+"annotation",
				strings.Repeat(" ", 27)+"message that",
				strings.Repeat(" ", 27)+"will wrap",
			),
		},
		"above rows align under the column": {
			input: "key: value",
			width: 16,
			annotation: line.Annotation{
				Content:   "one two three four five",
				Placement: line.Above,
				Cols:      position.NewSpan(4, 4),
			},
			want: stringtest.JoinLF(
				"    one two",
				"    three four",
				"    five",
				"key: value",
			),
		},
		"column past the width keeps the marker at its column": {
			input:      "key: value",
			width:      25,
			annotation: line.Annotation{Content: "x", Placement: line.Below, Cols: position.NewSpan(30, 30)},
			want: stringtest.JoinLF(
				"key: value",
				strings.Repeat(" ", 30)+"^",
				strings.Repeat(" ", 5)+"x",
			),
		},
		"one-cell word at the last column hangs": {
			input:      "key: aaaaaaaaaaaaaaa",
			width:      20,
			annotation: line.Annotation{Content: "x", Placement: line.Below, Cols: position.NewSpan(19, 19)},
			want: stringtest.JoinLF(
				"key: aaaaaaaaaaaaaaa",
				strings.Repeat(" ", 19)+"^",
				"x",
			),
		},
		"one-cell words with no room beside the marker hang": {
			input:      "key: aaaaaaaaaaaaaaa",
			width:      20,
			annotation: line.Annotation{Content: "a b c", Placement: line.Below, Cols: position.NewSpan(18, 18)},
			want: stringtest.JoinLF(
				"key: aaaaaaaaaaaaaaa",
				strings.Repeat(" ", 18)+"^",
				"a b c",
			),
		},
		"marker with empty text at the last column keeps one row": {
			input:      "key: aaaaaaaaaaaaaaa",
			width:      20,
			annFunc:    arrow,
			annotation: line.Annotation{Placement: line.Below, Cols: position.NewSpan(19, 19)},
			want: stringtest.JoinLF(
				"key: aaaaaaaaaaaaaaa",
				strings.Repeat(" ", 19)+"-> ",
			),
		},
		"one-cell word with one cell of room stays beside the marker": {
			input:      "key: aaaaaaaaaaaaaaa",
			width:      20,
			annotation: line.Annotation{Content: "x", Placement: line.Below, Cols: position.NewSpan(17, 17)},
			want: stringtest.JoinLF(
				"key: aaaaaaaaaaaaaaa",
				strings.Repeat(" ", 17)+"^ x",
			),
		},
		"below column on the last wrapped row pads from that row's start": {
			input:      "key: aaaa bbbb cccc",
			width:      10,
			annotation: line.Annotation{Content: "x", Placement: line.Below, Cols: position.NewSpan(15, 15)},
			want: stringtest.JoinLF(
				"key: aaaa",
				"bbbb cccc",
				"     ^ x",
			),
		},
		// The wrap drops the space before the accent, so the next row
		// starts with the accent alone, which takes no cell.
		"below column on a row that starts with a combining accent": {
			input:      "k: aaaa \u0301bbbb",
			width:      8,
			annotation: line.Annotation{Content: "here", Placement: line.Below, Cols: position.NewSpan(9, 9)},
			want: stringtest.JoinLF(
				"k: aaaa",
				"\u0301bbbb",
				"^ here",
			),
		},
		// A spacing mark takes a cell on its own at the start of the row.
		"below column on a row that starts with a spacing mark": {
			input:      "k: aaaa \u093fbbbb",
			width:      8,
			annotation: line.Annotation{Content: "here", Placement: line.Below, Cols: position.NewSpan(9, 9)},
			want: stringtest.JoinLF(
				"k: aaaa",
				"\u093fbbbb",
				" ^ here",
			),
		},
		"above column on a wrapped row sits above that row": {
			input:      "key: aaaa bbbb cccc",
			width:      10,
			annotation: line.Annotation{Content: "hi", Placement: line.Above, Cols: position.NewSpan(15, 15)},
			want: stringtest.JoinLF(
				"key: aaaa",
				"     hi",
				"bbbb cccc",
			),
		},
		"below column on an earlier wrapped row sits below that row": {
			input:      "key: aaaa bbbb cccc",
			width:      10,
			annotation: line.Annotation{Content: "x", Placement: line.Below, Cols: position.NewSpan(5, 5)},
			want: stringtest.JoinLF(
				"key: aaaa",
				"     ^ x",
				"bbbb cccc",
			),
		},
		"column on a wrapped row pads from that row's start past the gutter": {
			input:      "key: aaaa bbbb cccc",
			gutter:     printer.LineNumberGutter,
			width:      15,
			annotation: line.Annotation{Content: "x", Placement: line.Below, Cols: position.NewSpan(15, 15)},
			want: stringtest.JoinLF(
				"   1 key: aaaa",
				"   - bbbb cccc",
				"          ^ x",
			),
		},
		"column near the edge moves the text off the marker row": {
			input: "key: value",
			width: 16,
			annotation: line.Annotation{
				Content:   "string does not match pattern",
				Placement: line.Below,
				Cols:      position.NewSpan(8, 8),
			},
			want: stringtest.JoinLF(
				"key: value",
				strings.Repeat(" ", 8)+"^",
				"string does not",
				"match pattern",
			),
		},
		"hyphenated word wider than the room beside the marker hangs": {
			// The wrap keeps the hyphen with the word before it, so
			// "abc-" needs four cells and the three beside the marker
			// would split "def".
			input:      "key: value",
			width:      10,
			annotation: line.Annotation{Content: "abc-def", Placement: line.Below, Cols: position.NewSpan(5, 5)},
			want: stringtest.JoinLF(
				"key: value",
				strings.Repeat(" ", 5)+"^",
				"abc-def",
			),
		},
		"word with a slash wider than the room beside the marker hangs": {
			input:      "key: value",
			width:      10,
			annotation: line.Annotation{Content: "abc/def", Placement: line.Below, Cols: position.NewSpan(5, 5)},
			want: stringtest.JoinLF(
				"key: value",
				strings.Repeat(" ", 5)+"^",
				"abc/def",
			),
		},
		"words split at ideographic spaces fit beside the marker": {
			// The wrap breaks at every Unicode space but the no-break
			// space, so each word fits the room beside the marker.
			input: "key: value",
			width: 16,
			annotation: line.Annotation{
				Content:   "aaaa\u3000bbbb\u3000cccc",
				Placement: line.Below,
				Cols:      position.NewSpan(5, 5),
			},
			want: stringtest.JoinLF(
				"key: value",
				strings.Repeat(" ", 5)+"^ aaaa",
				strings.Repeat(" ", 7)+"bbbb",
				strings.Repeat(" ", 7)+"cccc",
			),
		},
		"words split at em spaces fit beside the marker": {
			input: "key: value",
			width: 16,
			annotation: line.Annotation{
				Content:   "aaaa\u2003bbbb\u2003cccc",
				Placement: line.Below,
				Cols:      position.NewSpan(5, 5),
			},
			want: stringtest.JoinLF(
				"key: value",
				strings.Repeat(" ", 5)+"^ aaaa\u2003bbbb",
				strings.Repeat(" ", 7)+"cccc",
			),
		},
		"words joined by a no-break space wider than the room hang": {
			input:      "key: value",
			width:      15,
			annotation: line.Annotation{Content: "aaaa\u00a0bbbb", Placement: line.Below, Cols: position.NewSpan(5, 5)},
			want: stringtest.JoinLF(
				"key: value",
				strings.Repeat(" ", 5)+"^",
				"aaaa\u00a0bbbb",
			),
		},
		"column near the edge hangs the text under a narrower indent": {
			input: "key: value",
			width: 40,
			annotation: line.Annotation{
				Content:   "string does not match pattern",
				Placement: line.Below,
				Cols:      position.NewSpan(34, 34),
			},
			want: stringtest.JoinLF(
				"key: value",
				strings.Repeat(" ", 34)+"^",
				strings.Repeat(" ", 20)+"string does not",
				strings.Repeat(" ", 20)+"match pattern",
			),
		},
		"negative column renders at column zero": {
			input:      "key: value",
			width:      0,
			annotation: line.Annotation{Content: "x", Placement: line.Below, Cols: position.NewSpan(-1, -1)},
			want: stringtest.JoinLF(
				"key: value",
				"^ x",
			),
		},
		"negative column renders at column zero when wrapping": {
			input:      "key: value",
			width:      10,
			annotation: line.Annotation{Content: "x", Placement: line.Below, Cols: position.NewSpan(-1, -1)},
			want: stringtest.JoinLF(
				"key: value",
				"^ x",
			),
		},
		"control characters are escaped before wrapping": {
			input:      "key: value",
			width:      20,
			annotation: line.Annotation{Content: "\x1b[31mred\x1b[0m w w w w w w w w", Placement: line.Below},
			want: stringtest.JoinLF(
				"key: value",
				"^ ␛[31mred␛[0m w w w",
				"  w w w w w",
			),
		},
		"embedded newline renders as a picture without wrapping": {
			input:      "key: value",
			width:      0,
			annotation: line.Annotation{Content: "a\nb", Placement: line.Below},
			want: stringtest.JoinLF(
				"key: value",
				"^ a␊b",
			),
		},
		"embedded newline renders as a picture when wrapping": {
			input:      "key: value",
			width:      20,
			annotation: line.Annotation{Content: "a\nb", Placement: line.Below},
			want: stringtest.JoinLF(
				"key: value",
				"^ a␊b",
			),
		},
		"custom annotation func gets no column indent": {
			input:      "k: v",
			width:      6,
			annFunc:    bare,
			annotation: line.Annotation{Content: "w w w w w w", Placement: line.Below, Cols: position.NewSpan(10, 10)},
			want: stringtest.JoinLF(
				"k: v",
				"w w w",
				"w w w",
			),
		},
		"custom marker rows align under the text": {
			input:   "key: value",
			width:   18,
			annFunc: arrow,
			annotation: line.Annotation{
				Content:   "alpha beta gamma delta epsilon",
				Placement: line.Below,
				Cols:      position.NewSpan(5, 5),
			},
			want: stringtest.JoinLF(
				"key: value",
				"     -> alpha beta",
				"        gamma",
				"        delta",
				"        epsilon",
			),
		},
		"above annotation at col 0": {
			input:      "key: value",
			annotation: line.Annotation{Content: "# comment", Placement: line.Above},
			want:       stringtest.JoinLF("# comment", "key: value"),
		},
		"above annotation with col padding": {
			input:      "key: value",
			annotation: line.Annotation{Content: "^-- error here", Placement: line.Above, Cols: position.NewSpan(5, 5)},
			want: stringtest.JoinLF(
				"     ^-- error here",
				"key: value",
			),
		},
		"below annotation at col 0": {
			input:      "key: value",
			annotation: line.Annotation{Content: "comment below", Placement: line.Below},
			want:       stringtest.JoinLF("key: value", "^ comment below"),
		},
		"below annotation with col padding": {
			input:      "key: value",
			annotation: line.Annotation{Content: "error here", Placement: line.Below, Cols: position.NewSpan(5, 5)},
			want: stringtest.JoinLF(
				"key: value",
				"     ^ error here",
			),
		},
		"below annotation under wide runes": {
			input:      "日本語: 値",
			annotation: line.Annotation{Content: "bad value", Placement: line.Below, Cols: position.NewSpan(5, 5)},
			// The three wide runes take six cells, so the marker sits eight
			// cells in, under the value, as wide as the value.
			want: stringtest.JoinLF(
				"日本語: 値",
				"        ^^ bad value",
			),
		},
		"below annotation past the end of wide runes": {
			input:      "日本",
			annotation: line.Annotation{Content: "after", Placement: line.Below, Cols: position.NewSpan(4, 4)},
			want: stringtest.JoinLF(
				"日本",
				"      ^ after",
			),
		},
		"below annotation on second line": {
			input: stringtest.JoinLF(
				"first: 1",
				"second: 2",
			),
			line:       1,
			annotation: line.Annotation{Content: "note", Placement: line.Below, Cols: position.NewSpan(8, 8)},
			want: stringtest.JoinLF(
				"first: 1",
				"second: 2",
				"        ^ note",
			),
		},
		"above annotation on second line": {
			input: stringtest.JoinLF(
				"first: 1",
				"second: 2",
			),
			line:       1,
			annotation: line.Annotation{Content: "@@ -1 +1 @@", Placement: line.Above},
			want: stringtest.JoinLF(
				"first: 1",
				"@@ -1 +1 @@",
				"second: 2",
			),
		},
		"below annotation with empty content marks its column": {
			input:      "key: value",
			annotation: line.Annotation{Placement: line.Below, Cols: position.NewSpan(2, 2)},
			want: stringtest.JoinLF(
				"key: value",
				"  ^",
			),
		},
		"above annotation with line numbers": {
			gutter:     printer.LineNumberGutter,
			input:      "key: value",
			annotation: line.Annotation{Content: "@@ -1 +1 @@", Placement: line.Above},
			want: stringtest.JoinLF(
				"     @@ -1 +1 @@",
				"   1 key: value",
			),
		},
		"below annotation with line numbers": {
			gutter:     printer.LineNumberGutter,
			input:      "key: value",
			annotation: line.Annotation{Content: "error", Placement: line.Below, Cols: position.NewSpan(5, 5)},
			want: stringtest.JoinLF(
				"   1 key: value",
				"          ^ error",
			),
		},
		"below annotation with col padding and line numbers": {
			gutter: printer.LineNumberGutter,
			input: stringtest.JoinLF(
				"first: 1",
				"second: 2",
			),
			annotation: line.Annotation{Content: "note", Placement: line.Below, Cols: position.NewSpan(7, 7)},
			want: stringtest.JoinLF(
				"   1 first: 1",
				"            ^ note",
				"   2 second: 2",
			),
		},
		// With annotations disabled, the printer renders only the content.
		"NoAnnotation hides one above": {
			input:      "key: value\n",
			annFunc:    printer.NoAnnotation,
			annotation: line.Annotation{Content: "# hidden above", Placement: line.Above},
			want:       "key: value",
		},
		"NoAnnotation hides one below": {
			input:      "key: value\n",
			annFunc:    printer.NoAnnotation,
			annotation: line.Annotation{Content: "# hidden below", Placement: line.Below, Cols: position.NewSpan(5, 5)},
			want:       "key: value",
		},
		"DefaultAnnotation shows them": {
			input:      "key: value\n",
			annFunc:    printer.DefaultAnnotation,
			annotation: line.Annotation{Content: "@@ -1 +1 @@"},
			want:       stringtest.JoinLF("@@ -1 +1 @@", "key: value"),
		},
		"custom func marks one below": {
			input:      "key: value\n",
			annFunc:    custom,
			annotation: line.Annotation{Content: "custom error", Placement: line.Below},
			want:       stringtest.JoinLF("key: value", ">>> custom error"),
		},
		"custom func marks one above": {
			input:      "key: value\n",
			annFunc:    custom,
			annotation: line.Annotation{Content: "custom header", Placement: line.Above},
			want:       stringtest.JoinLF("=== custom header", "key: value"),
		},
		// A column more than 1024 columns past the end of the content starts
		// 1024 columns past it, after every cell of the content.
		"max column": {
			input: "k: v",
			annotation: line.Annotation{
				Content:   "x",
				Placement: line.Below,
				Cols:      position.NewSpan(math.MaxInt, math.MaxInt),
			},
			want: stringtest.JoinLF("k: v", strings.Repeat(" ", 4+1024)+"^ x"),
		},
		"max column after wide runes": {
			input: "k: 日本語",
			annotation: line.Annotation{
				Content:   "x",
				Placement: line.Below,
				Cols:      position.NewSpan(math.MaxInt, math.MaxInt),
			},
			want: stringtest.JoinLF("k: 日本語", strings.Repeat(" ", 9+1024)+"^ x"),
		},
		"max column when wrapping": {
			// The column leaves the text no room within the width, so the
			// text moves below the marker.
			input: "k: v",
			width: 10,
			annotation: line.Annotation{
				Content:   "x",
				Placement: line.Below,
				Cols:      position.NewSpan(math.MaxInt, math.MaxInt),
			},
			want: stringtest.JoinLF("k: v", strings.Repeat(" ", 4+1024)+"^", "x"),
		},
		// A large column that fits in an int takes the same bound, so the
		// printer does not pad the row out to the column.
		"column past the bound": {
			input:      "k: v",
			annotation: line.Annotation{Content: "x", Placement: line.Below, Cols: position.NewSpan(1<<32, 1<<32)},
			want:       stringtest.JoinLF("k: v", strings.Repeat(" ", 4+1024)+"^ x"),
		},
		"column at the bound": {
			input:      "k: v",
			annotation: line.Annotation{Content: "x", Placement: line.Below, Cols: position.NewSpan(4+1024, 4+1024)},
			want:       stringtest.JoinLF("k: v", strings.Repeat(" ", 4+1024)+"^ x"),
		},

		// The carets under a wrapped line take a row below each wrapped row
		// they mark, padded from that row's start, so every caret sits under
		// the column it marks.
		//
		// The wrap drops the indent when the first word does not fit
		// beside it, so the first row starts at the word, and so does the
		// padding of the marks under it.
		"indent dropped before a long word": {
			input: crt,
			marks: []position.Range{yamltest.LineRange(1, 4, 8)},
			line:  1,
			width: 30,
			want: stringtest.JoinLF(
				"crt: |",
				"LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS",
				"^^^^",
				"0tLS0tCk1JSUMr",
			),
		},
		"indent dropped before a long word under a whole-line range": {
			input: crt,
			marks: []position.Range{yamltest.LineRange(1, 0, 48)},
			line:  1,
			width: 30,
			want: stringtest.JoinLF(
				"crt: |",
				"LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS",
				strings.Repeat("^", 30),
				"0tLS0tCk1JSUMr",
				strings.Repeat("^", 14),
			),
		},
		"indent dropped before a long word above the line": {
			input:      crt,
			annotation: line.Annotation{Placement: line.Above, Cols: position.NewSpan(4, 4), Content: "here"},
			line:       1,
			width:      30,
			want: stringtest.JoinLF(
				"crt: |",
				"here",
				"LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS",
				"0tLS0tCk1JSUMr",
			),
		},
		// The wrap drops the spaces at the end of the line when they run
		// past the width, so they get no caret either.
		"trailing spaces dropped on the last row": {
			input: stringtest.JoinLF(
				"a: |",
				"  some text          ",
				"  more",
			),
			marks: []position.Range{yamltest.LineRange(1, 2, 21)},
			line:  1,
			width: 20,
			want: stringtest.JoinLF(
				"a: |",
				"  some text",
				"  ^^^^^^^^^",
				"  more",
			),
		},
		"trailing spaces dropped on the only row": {
			input: "key: value          ",
			marks: []position.Range{yamltest.LineRange(0, 0, 20)},
			width: 16,
			want: stringtest.JoinLF(
				"key: value",
				"^^^^^^^^^^",
			),
		},
		// The wrap would start a row with the accent of a decomposed "é",
		// and the accent stays with its "e" instead, so the caret under
		// the "x" after it lands under the "x".
		"decomposed accent at a wrap boundary": {
			input: "k: abcdefge\u0301xyz",
			marks: []position.Range{yamltest.LineRange(0, 12, 13)},
			width: 8,
			want: stringtest.JoinLF(
				"k:",
				"abcdefge\u0301",
				"xyz",
				"^",
			),
		},
		// The wrap drops the space before the accent, so the row after
		// the break starts with the accent alone, which takes no cell,
		// and the carets start under the first "b".
		"combining accent after a space at a break": {
			input: "k: aaaa \u0301bbbb",
			marks: []position.Range{yamltest.LineRange(0, 9, 13)},
			width: 8,
			want: stringtest.JoinLF(
				"k: aaaa",
				"\u0301bbbb",
				"^^^^",
			),
		},
		// The caret row draws the dropped space and the accent as one
		// cluster at the space, which no row shows, so a range over
		// either still starts its carets under the first "b".
		"combining accent after a space at a break under a range over the space": {
			input: "k: aaaa \u0301bbbb",
			marks: []position.Range{yamltest.LineRange(0, 7, 13)},
			width: 8,
			want: stringtest.JoinLF(
				"k: aaaa",
				"\u0301bbbb",
				"^^^^",
			),
		},
		"combining accent after a space at a break under a range over the accent": {
			input: "k: aaaa \u0301bbbb",
			marks: []position.Range{yamltest.LineRange(0, 8, 13)},
			width: 8,
			want: stringtest.JoinLF(
				"k: aaaa",
				"\u0301bbbb",
				"^^^^",
			),
		},
		"spacing mark after a space at a break": {
			input: "k: aaaa \u093fbbbb",
			marks: []position.Range{yamltest.LineRange(0, 9, 13)},
			width: 8,
			want: stringtest.JoinLF(
				"k: aaaa",
				"\u093fbbbb",
				" ^^^^",
			),
		},
		"runs on different rows": {
			input: "key: aaaa bbbb cccc dddd",
			marks: []position.Range{yamltest.LineRange(0, 5, 9), yamltest.LineRange(0, 17, 19)},
			width: 10,
			want: stringtest.JoinLF(
				"key: aaaa",
				"     ^^^^",
				"bbbb cccc",
				"       ^^",
				"dddd",
			),
		},
		"range across a break": {
			input: "key: aaaa bbbb cccc dddd",
			marks: []position.Range{yamltest.LineRange(0, 5, 14)},
			width: 10,
			want: stringtest.JoinLF(
				"key: aaaa",
				"     ^^^^",
				"bbbb cccc",
				"^^^^",
				"dddd",
			),
		},
		"wide runes": {
			input: "k: 日本語 日本語",
			marks: []position.Range{yamltest.LineRange(0, 3, 10)},
			width: 9,
			want: stringtest.JoinLF(
				"k: 日本語",
				"   ^^^^^^",
				"日本語",
				"^^^^^^",
			),
		},
		// A zero-width rune at the end of a row that fills the width gets
		// its caret one cell past the width, and the carets stay on one
		// row rather than wrapping under the wide rune.
		"zero-width rune at the end of a full row": {
			input: "k: 本語x\u200b",
			marks: []position.Range{yamltest.LineRange(0, 4, 7)},
			width: 8,
			want: stringtest.JoinLF(
				"k: 本語x\u200b",
				"     ^^^^",
			),
		},
		"zero-width rune at the end of a full row under a whole-line range": {
			input: "ccca日ccca\u200b",
			marks: []position.Range{yamltest.LineRange(0, 0, 10)},
			width: 10,
			want: stringtest.JoinLF(
				"ccca日ccca\u200b",
				"^^^^^^^^^^^",
			),
		},
		// The message sits beside the carets of the wrapped row that holds
		// the start of its run, and the other runs get their carets alone.
		"message beside the carets of a later row": {
			input:      "key: aaaa bbbb cccc dddd",
			marks:      []position.Range{yamltest.LineRange(0, 5, 9), yamltest.LineRange(0, 20, 24)},
			annotation: line.Annotation{Placement: line.Below, Cols: position.NewSpan(20, 24), Content: "bad"},
			width:      20,
			want: stringtest.JoinLF(
				"key: aaaa bbbb cccc",
				"     ^^^^",
				"dddd",
				"^^^^ bad",
			),
		},
		// Carets that run to the end of a row leave the message no room
		// beside them, so it moves to the row below the carets.
		"message below carets that fill the row": {
			input:      "key: aaaa bbbb cccc dddd",
			marks:      []position.Range{yamltest.LineRange(0, 5, 24)},
			annotation: line.Annotation{Placement: line.Below, Cols: position.NewSpan(5, 24), Content: "bad"},
			width:      20,
			want: stringtest.JoinLF(
				"key: aaaa bbbb cccc",
				"     ^^^^^^^^^^^^^^",
				"bad",
				"dddd",
				"^^^^",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := niceyaml.NewSourceFromString(tc.input).View()
			view.AddOverlay(kind.GenericError, tc.marks...)

			for _, r := range tc.marks {
				view.Annotate(r.Start.Line, line.Annotation{
					Placement: line.Below,
					Cols:      position.NewSpan(r.Start.Col, r.End.Col),
				})
			}

			if tc.annotation != (line.Annotation{}) {
				view.Annotate(tc.line, tc.annotation)
			}

			// A nil func selects DefaultAnnotation rather than panicking on
			// the first annotated line.
			p := testPrinterWithGutter(tc.gutter).With(
				printer.WithWrapWidth(tc.width),
				printer.WithAnnotation(tc.annFunc),
			)

			got := p.Print(view)
			assert.Equal(t, tc.want, got)

			assertLayoutMeasures(t, p.Layout(view), got)
		})
	}
}

func TestPrinter_AnnotationWrap_ContainerFrame(t *testing.T) {
	t.Parallel()

	// The room beside a marker is what the frame of the container leaves
	// of the width. A word that fits that room stays beside the marker,
	// and a word one cell wider moves below it whole, whatever the frame.
	beside := []string{
		"key: value",
		"        ^ abcdefg",
	}
	below := []string{
		"key: value",
		"        ^",
		"abcdefg",
	}

	tcs := map[string]struct {
		container lipgloss.Style
		want      []string
		width     int
	}{
		"no frame, word fits": {
			container: lipgloss.NewStyle(),
			width:     17,
			want:      beside,
		},
		"no frame, word one cell too wide": {
			container: lipgloss.NewStyle(),
			width:     16,
			want:      below,
		},
		"padding on the right, word fits": {
			container: lipgloss.NewStyle().PaddingRight(1),
			width:     18,
			want:      beside,
		},
		"padding on the right, word one cell too wide": {
			container: lipgloss.NewStyle().PaddingRight(1),
			width:     17,
			want:      below,
		},
		"border and padding, word fits": {
			container: lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(0, 1),
			width:     21,
			want:      beside,
		},
		"border and padding, word one cell too wide": {
			container: lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(0, 1),
			width:     20,
			want:      below,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := niceyaml.NewSourceFromString("key: value\n").View()
			view.Annotate(0, line.Annotation{Content: "abcdefg", Placement: line.Below, Cols: position.NewSpan(8, 8)})

			p := testPrinter().With(
				printer.WithContainerStyle(tc.container),
				printer.WithWrapWidth(tc.width),
			)

			assert.Equal(t, tc.container.Render(stringtest.JoinLF(tc.want...)), p.Print(view))
		})
	}
}

func TestPrinter_LineNumbers_MaxNumber(t *testing.T) {
	t.Parallel()

	// A slice of a long document has a Len of one but a line number past
	// 9999, so the gutter must size itself from the number, and every row
	// of the line, wrapped or annotated, must share that width. The gutter
	// grows by a column, and the wrap width must shrink with it so no
	// rendered row exceeds the width.
	input := tenThousandLines + "last: this is a long value that wraps"
	source := niceyaml.NewSourceFromString(input)
	view := source.View().Slice(position.NewSpan(10000, source.Lines().Len()))
	view.Annotate(10000, line.Annotation{Content: "note", Placement: line.Below, Cols: position.NewSpan(6, 6)})

	p := testPrinterWithGutter(printer.LineNumberGutter).With(printer.WithWrapWidth(30))

	got := p.Print(view)
	assertRowsFit(t, got, 30)
	assert.Equal(t, stringtest.JoinLF(
		"10001 last: this is a long",
		"            ^ note",
		"    - value that wraps",
	), got)
	assert.Equal(t, []int{3}, layoutRows(p.Layout(view)))
}

func TestPrinter_LineNumbers_Placeholder(t *testing.T) {
	t.Parallel()

	// A side-by-side diff pads the shorter side with zero-value lines, which
	// have no number. The gutter renders a blank column for them.
	result := yamltest.Diff(stringtest.JoinLF(
		"a: 1",
		"b: 2",
		"c: 3",
	), "a: 1")
	p := testPrinterWithGutter(printer.DefaultGutter)

	assert.Equal(t, stringtest.JoinLF(
		"   1  a: 1",
		"      ",
		"      ",
	), p.Print(result.After()))
	assert.Equal(t, stringtest.JoinLF(
		"   1  a: 1",
		"   2 -b: 2",
		"   3 -c: 3",
	), p.Print(result.Before()))
}

func TestPrinter_BlendKey_StyleNames(t *testing.T) {
	t.Parallel()

	// A style named "a!b" must not share a cache entry with the sequence
	// "blend a, then replace with b", which unquoted key separators would
	// spell the same way.
	const (
		ab kind.Kind = "a!b"
		a  kind.Kind = "a"
		b  kind.Kind = "b"
	)

	view := niceyaml.NewSourceFromString(stringtest.JoinLF("k: 1", "k: 2")).View()
	view.AddLineOverlay(0, line.Overlay{Kind: ab, Cols: position.NewSpan(0, 4), Blend: true})
	view.AddLineOverlay(1,
		line.Overlay{Kind: a, Cols: position.NewSpan(0, 4), Blend: true},
		line.Overlay{Kind: b, Cols: position.NewSpan(0, 4)},
	)

	// Each kind wraps its text in a tag of its name.
	p := yamltest.NewXMLPrinter(printer.WithStyles(
		yamltest.NewXMLStyles(yamltest.XMLStyleInclude(ab, a, b)),
	))

	assert.Equal(t, stringtest.JoinLF(
		"<a!b>k</a!b><a!b>:</a!b><a!b> </a!b><a!b>1</a!b>",
		"<b>k</b><b>:</b><b> </b><b>2</b>",
	), p.Print(view))
}

func TestPrinter_Overlay_Attributes(t *testing.T) {
	t.Parallel()

	// An overlay style that sets only text attributes must still change the
	// rendered output, whether it replaces or blends with the style beneath.
	const underlined kind.Kind = "underlined"

	single := lipgloss.NewStyle().Underline(true).Bold(true)
	curly := lipgloss.NewStyle().
		UnderlineStyle(lipgloss.UnderlineCurly).
		UnderlineColor(lipgloss.Color("#FF0000"))

	tcs := map[string]struct {
		style lipgloss.Style
		blend bool
	}{
		"replace":       {style: single, blend: false},
		"blend":         {style: single, blend: true},
		"replace curly": {style: curly, blend: false},
		"blend curly":   {style: curly, blend: true},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// Each token renders on its own, so the expected output styles
			// them one at a time.
			st := tc.style
			want := st.Render("k") + st.Render(":") + st.Render(" ") + st.Render("v")

			view := niceyaml.NewSourceFromString("k: v").View()
			view.AddLineOverlay(0, line.Overlay{Kind: underlined, Cols: position.NewSpan(0, 4), Blend: tc.blend})

			p := testPrinterWithStyles(style.Set(underlined, st))

			assert.Equal(t, want, p.Print(view))
		})
	}
}

func TestPrinter_SeparatorStyle(t *testing.T) {
	t.Parallel()

	// The whitespace a token carries before its text renders in kind.Text,
	// whatever kind of token follows it. Brackets mark the styled tokens.
	styles := style.New(
		lipgloss.NewStyle(),
		style.Set(kind.LiteralString, bracketStyle("[", "]")),
		style.Set(kind.LiteralStringDouble, bracketStyle("[", "]")),
		style.Set(kind.Comment, bracketStyle("[", "]")),
	)

	tcs := map[string]struct {
		input string
		want  string
	}{
		"plain scalar": {
			input: "k:   v",
			want:  "k:   [v]",
		},
		"double quoted scalar": {
			input: `k:   "v"`,
			want:  `k:   ["v"]`,
		},
		"indented comment": {
			input: stringtest.JoinLF("a:", "  # note", "  b: 1"),
			want:  stringtest.JoinLF("a:", "  [# note]", "  b: 1"),
		},
		"multiline plain scalar": {
			input: stringtest.JoinLF("k: plain", "  multi"),
			want:  stringtest.JoinLF("k: [plain]", "  [multi]"),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := testPrinter().With(printer.WithStyles(styles))

			assert.Equal(t, tc.want, p.Print(niceyaml.NewSourceFromString(tc.input).View()))
		})
	}
}

func TestPrinter_WithMaxNumber(t *testing.T) {
	t.Parallel()

	short := niceyaml.NewSourceFromString(stringtest.JoinLF("a: 1", "b: 2")).View()
	long := niceyaml.NewSourceFromString(tenThousandLines).View()

	tcs := map[string]struct {
		view      *line.View
		given     int
		want      int
		wantWidth int
	}{
		"sizes the gutter for the given number": {view: short, given: 10000, want: 10000, wantWidth: 6},
		// Two views of different lengths then share a gutter width.
		"a view as long as the given number shares that width": {view: long, given: 10000, want: 10000, wantWidth: 6},
		"zero takes the number from the view":                  {view: short, given: 0, want: 2, wantWidth: 5},
		// A number below the view's own largest would let the last rows
		// overflow the gutter the layout reports, so the view wins.
		"a longer view widens the gutter past the given number": {view: long, given: 3, want: 10000, wantWidth: 6},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := testPrinterWithGutter(printer.LineNumberGutter).With(printer.WithMaxNumber(tc.given))

			assert.Equal(t, tc.want, p.MaxNumber(tc.view))
			assert.Equal(t, tc.wantWidth, p.Layout(tc.view).GutterWidth())
		})
	}
}

func TestPrinter_WithGutter_Nil(t *testing.T) {
	t.Parallel()

	view := niceyaml.NewSourceFromString("key: value").View()
	view.Annotate(0, line.Annotation{Content: "note", Placement: line.Below, Cols: position.NewSpan(5, 5)})

	want := testPrinterWithGutter(printer.NoGutter).Print(view)

	// A nil Gutter, or one holding a nil func or pointer, selects NoGutter
	// rather than panicking on the first Width or Render call.
	tcs := map[string]struct {
		gutter printer.Gutter
	}{
		"nil":            {gutter: nil},
		"nil GutterFunc": {gutter: printer.GutterFunc(nil)},
		"nil pointer":    {gutter: (*fixedGutter)(nil)},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var got string

			require.NotPanics(t, func() {
				got = testPrinterWithGutter(tc.gutter).Print(view)
			})
			assert.Equal(t, want, got)
			assert.Equal(t, stringtest.JoinLF(
				"key: value",
				"     ^ note",
			), got)
		})
	}
}

func TestPrinter_WithStyles_Nil(t *testing.T) {
	t.Parallel()

	view := niceyaml.NewSourceFromString("key: value").View()
	want := printer.New().Print(view)

	// A nil Styler, or one holding a nil pointer, selects the default
	// styles rather than panicking in New.
	tcs := map[string]struct {
		styles style.Styler
	}{
		"nil":               {styles: nil},
		"nil *style.Styles": {styles: (*style.Styles)(nil)},
		"nil *theme.Theme":  {styles: (*theme.Theme)(nil)},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var got string

			require.NotPanics(t, func() {
				got = printer.New(printer.WithStyles(tc.styles)).Print(view)
			})
			assert.Equal(t, want, got)
		})
	}
}

func TestPrinter_ContainerWidth(t *testing.T) {
	t.Parallel()

	// Without a container width the border box shrinks to the widest row, so
	// a window of short lines draws a narrower box than a window of long
	// ones. A container width pins the box to the same columns for both.
	src := niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "b: this value is much longer", "c: 3"))

	border := printer.WithContainerStyle(lipgloss.NewStyle().Border(lipgloss.NormalBorder()))

	tcs := map[string]struct {
		opts []printer.Option
		span position.Span
		want []string
	}{
		"unpinned box shrinks to the widest row": {
			opts: []printer.Option{border},
			span: position.NewSpan(0, 1),
			want: []string{
				"┌────┐",
				"│a: 1│",
				"└────┘",
			},
		},
		"pinned box keeps its columns": {
			opts: []printer.Option{border, printer.WithContainerWidth(12)},
			span: position.NewSpan(0, 1),
			want: []string{
				"┌──────────┐",
				"│a: 1      │",
				"└──────────┘",
			},
		},
		"a row past the width is padded by nothing": {
			opts: []printer.Option{border, printer.WithContainerWidth(12)},
			span: position.NewSpan(1, 2),
			want: []string{
				"┌────────────────────────────┐",
				"│b: this value is much longer│",
				"└────────────────────────────┘",
			},
		},
		"an empty view renders one padded row": {
			opts: []printer.Option{border, printer.WithContainerWidth(12)},
			span: position.NewSpan(0, 0),
			want: []string{
				"┌──────────┐",
				"│          │",
				"└──────────┘",
			},
		},
		"no container frame takes the whole width": {
			opts: []printer.Option{printer.WithContainerWidth(12)},
			span: position.NewSpan(0, 1),
			want: []string{"a: 1        "},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := testPrinter().With(tc.opts...)

			assert.Equal(t, stringtest.JoinLF(tc.want...), p.Print(src.View().Slice(tc.span)))
		})
	}
}

func TestPrinter_WrapWidth_FitsFrame(t *testing.T) {
	t.Parallel()

	// The width counts the frame of the container, so every row of Print
	// and of PrintError fits it whatever the container draws around the
	// content. The box pads each row on the right, and a border adds a
	// column on each side.
	named := niceyaml.NewSourceFromString(
		"name: a-very-long-name-that-will-wrap-around-the-width-for-sure\n",
		niceyaml.WithName("long.yaml"),
	)
	longMessage := yamltest.Bind(t, named, yamltest.ErrorAt(
		"a message long enough to wrap around the width of the printer too", "name",
	))

	// A marked line longer than every width, between two short lines.
	source := niceyaml.NewSourceFromString(stringtest.LinesLF(
		"a: 1",
		"b: "+strings.Repeat("x", 80),
		"c: 3",
	))
	longLine := yamltest.Bind(t, source, yamltest.ErrorAt("bad", "b"))

	tcs := map[string]struct {
		opts  []printer.Option
		width int
	}{
		"default container":        {width: 40},
		"narrow default container": {width: 30},
		"box": {
			opts:  []printer.Option{printer.WithBox()},
			width: 40,
		},
		"narrow box": {
			opts:  []printer.Option{printer.WithBox()},
			width: 30,
		},
		"bordered container": {
			opts: []printer.Option{
				printer.WithContainerStyle(lipgloss.NewStyle().Border(lipgloss.NormalBorder())),
			},
			width: 30,
		},
		"bordered and padded container": {
			opts: []printer.Option{printer.WithContainerStyle(
				lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(0, 1),
			)},
			width: 40,
		},
		"gutterless": {
			opts:  []printer.Option{printer.WithGutter(printer.NoGutter)},
			width: 40,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := printer.New(printer.WithWrapWidth(tc.width)).With(tc.opts...)

			assertRowsFit(t, p.Print(named.View()), tc.width)
			assertRowsFit(t, p.PrintError(longMessage), tc.width)

			got := p.PrintError(longLine)

			// The tree, the blank row after it, the three source lines, and
			// the caret row would fit in six rows without wrapping.
			require.Greater(t, lipgloss.Height(got), 6, "the excerpt wraps")
			assertRowsFit(t, got, tc.width)
		})
	}
}

func TestPrinter_AnnotationGutterSoft(t *testing.T) {
	t.Parallel()

	gutter := printer.WithGutter(printer.GutterFunc(rowLetterGutter))

	tcs := map[string]struct {
		p           *printer.Printer
		input       string
		overlays    []position.Range
		annotations []line.Annotation
		want        []string // Gutter of each row.
	}{
		// Every row after the first of a line's annotation block is a
		// continuation, whichever kind group it belongs to.
		"across kinds": {
			p:     printer.New(printer.WithContainerStyle(lipgloss.NewStyle()), gutter),
			input: "key: value\n",
			annotations: []line.Annotation{
				{Content: "one", Placement: line.Below, Kind: kind.UIAnnotation},
				{Content: "two", Placement: line.Below, Kind: kind.TextError},
			},
			want: []string{"L ", "F ", "S "},
		},
		// The annotation rows below each wrapped row form a block of their
		// own, so the first row of each block is not a continuation. Each
		// run draws its carets below the rows it covers, and the message
		// of the error moves to a row of its own, because its carets leave
		// it no room.
		"per wrapped row": {
			p:        testPrinter().With(gutter, printer.WithWrapWidth(12)),
			input:    "key: aaaa bbbb cccc dddd\n",
			overlays: []position.Range{yamltest.LineRange(0, 5, 9), yamltest.LineRange(0, 17, 19)},
			annotations: []line.Annotation{
				{Placement: line.Below, Cols: position.NewSpan(5, 9)},
				{Content: "n", Placement: line.Below, Kind: kind.TextError, Cols: position.NewSpan(5, 9)},
				{Placement: line.Below, Cols: position.NewSpan(17, 19)},
			},
			want: []string{"L ", "F ", "S ", "S ", "C ", "F ", "C "},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := niceyaml.NewSourceFromString(tc.input).View()
			view.AddOverlay(kind.GenericError, tc.overlays...)
			view.Annotate(0, tc.annotations...)

			var gutters []string

			for row := range strings.SplitSeq(tc.p.Print(view), "\n") {
				gutters = append(gutters, row[:2])
			}

			assert.Equal(t, tc.want, gutters)
		})
	}
}

// rowLetterGutter is a gutter func that names each row by a letter. The
// first row of a line gets L, and a row the line wraps onto gets C. The
// first row of an annotation block gets F, and a later one gets S.
func rowLetterGutter(ctx printer.GutterContext) string {
	switch {
	case !ctx.Annotation && ctx.Soft:
		return "C "
	case !ctx.Annotation:
		return "L "
	case ctx.Soft:
		return "S "
	default:
		return "F "
	}
}

func TestColWidth(t *testing.T) {
	t.Parallel()

	// The width is that of the rendered row. A control character shows in
	// one cell, a wide rune takes two cells, and every rune of a grapheme
	// cluster after its first renders within the first. An
	// AnnotationContext measures its content the same way.
	tcs := map[string]struct {
		content string
		col     int
		want    int
	}{
		"start":                      {content: "a: b", col: 0, want: 0},
		"negative clamps to start":   {content: "a: b", col: -1, want: 0},
		"before a control character": {content: "a: \"tab\there\"", col: 3, want: 3},
		"past a control character":   {content: "a: \"tab\there\"", col: 8, want: 8},
		"end after a control":        {content: "a: \"tab\there\"", col: 13, want: 13},
		"past the end":               {content: "a: b", col: 10, want: 10},
		"past end after a control":   {content: "a: \"tab\there\"", col: 20, want: 20},
		"base of a combining mark":   {content: "k: e\u0301x", col: 3, want: 3},
		"after wide runes":           {content: "k: 日本 x", col: 5, want: 7},
		"at a wide rune":             {content: "k: 日本 x", col: 4, want: 5},
		"at a combining mark":        {content: "k: e\u0301x", col: 4, want: 3},
		"after a combining mark":     {content: "k: e\u0301x", col: 5, want: 4},
		"at a zwj joiner":            {content: "a: \U0001F468\u200d\U0001F469 x", col: 4, want: 3},
		"at the second zwj emoji":    {content: "a: \U0001F468\u200d\U0001F469 x", col: 5, want: 3},
		"after a zwj sequence":       {content: "a: \U0001F468\u200d\U0001F469 x", col: 7, want: 6},
		"inside a keycap":            {content: "a: 1\ufe0f\u20e3 x", col: 5, want: 3},
		"after a keycap":             {content: "a: 1\ufe0f\u20e3 x", col: 7, want: 6},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, printer.ColWidth(tc.content, tc.col))
			assert.Equal(t, tc.want, printer.AnnotationContext{Content: tc.content}.ColWidth(tc.col))
		})
	}
}

func TestAnnotationContext_ColWidth(t *testing.T) {
	t.Parallel()

	// The context pads a row as the printer does, so a column far past
	// the end stops at line.MaxColPastEnd columns past it. TestColWidth
	// covers the columns within that bound.
	tcs := map[string]struct {
		content string
		col     int
		want    int
	}{
		"negative":             {content: "abc", col: -5, want: 0},
		"in range":             {content: "k: 日本 x", col: 5, want: 7},
		"at the bound":         {content: "abc", col: 3 + line.MaxColPastEnd, want: 3 + line.MaxColPastEnd},
		"past the bound":       {content: "abc", col: 2000, want: 3 + line.MaxColPastEnd},
		"max int":              {content: "abc", col: math.MaxInt, want: 3 + line.MaxColPastEnd},
		"past a wide rune end": {content: "日", col: math.MaxInt, want: 2 + line.MaxColPastEnd},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, printer.AnnotationContext{Content: tc.content}.ColWidth(tc.col))
		})
	}
}

func TestCut(t *testing.T) {
	t.Parallel()

	keycap := "ab1\ufe0f\u20e3cd"

	tcs := map[string]struct {
		row       string
		left      int
		right     int
		want      string
		wantWidth int
	}{
		"ascii":                    {row: "abcdef", left: 1, right: 4, want: "bcd", wantWidth: 3},
		"empty range":              {row: "\x1b[31mabcdef\x1b[0m", left: 2, right: 2, want: "", wantWidth: 0},
		"range past the end":       {row: "ab", left: 1, right: 9, want: "b", wantWidth: 1},
		"negative left":            {row: "abcdef", left: -2, right: 3, want: "abc", wantWidth: 3},
		"keycap whole":             {row: keycap, left: 0, right: 4, want: "ab1\ufe0f\u20e3", wantWidth: 4},
		"right edge inside keycap": {row: keycap, left: 0, right: 3, want: "ab ", wantWidth: 3},
		"left edge inside keycap":  {row: keycap, left: 3, right: 6, want: " cd", wantWidth: 3},
		"past a keycap":            {row: keycap, left: 4, right: 6, want: "cd", wantWidth: 2},
		"edges inside a wide rune": {row: "a日b", left: 2, right: 4, want: " b", wantWidth: 2},
		"wide rune wider than it":  {row: "a日b", left: 2, right: 3, want: " ", wantWidth: 1},
		"styled keeps escapes": {
			row:       "\x1b[31m" + keycap + "\x1b[0m",
			left:      1,
			right:     3,
			want:      "\x1b[31mb \x1b[0m",
			wantWidth: 2,
		},
		"filler keeps the style": {
			row:       "\x1b[41m日\x1b[0m",
			left:      1,
			right:     2,
			want:      "\x1b[41m \x1b[0m",
			wantWidth: 1,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, printer.Cut(tc.row, tc.left, tc.right))

			got, width := printer.CutWidth(tc.row, tc.left, tc.right)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.wantWidth, width)
		})
	}
}

func TestCut_LayoutCells(t *testing.T) {
	t.Parallel()

	// A keycap takes two cells in the printed row, so the cell of each
	// column after it is one more than ansi.Cut counts.
	keycap := "1\ufe0f\u20e3"
	content := "k: " + keycap + " x"

	tcs := map[string]struct {
		gutter printer.Gutter
	}{
		"no gutter":      {gutter: printer.NoGutter},
		"default gutter": {gutter: printer.DefaultGutter},
	}

	// Each column that starts a cluster, with the cluster it starts.
	clusters := map[int]string{0: "k", 1: ":", 2: " ", 3: keycap, 6: " ", 7: "x"}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := niceyaml.NewSourceFromString(content).View()
			p := printer.New(
				printer.WithGutter(tc.gutter),
				printer.WithContainerStyle(lipgloss.NewStyle()),
			)
			l := p.Layout(view)
			row := p.Print(view)

			require.NotEqual(t, ansi.Strip(row), row, "the row carries styles")

			for col, want := range clusters {
				cell := l.GutterWidth() + l.CellOf(position.New(0, col))
				wantWidth := ansi.StringWidth(want)

				got, width := printer.CutWidth(row, cell, cell+wantWidth)
				assert.Equal(t, want, ansi.Strip(got), "column %d", col)
				assert.Equal(t, wantWidth, width, "column %d", col)
			}
		})
	}
}

func TestCut_KeepsCells(t *testing.T) {
	t.Parallel()

	// The row holds a wide rune, a keycap, and a highlight. Every window of
	// it keeps each whole cluster at the cell the layout gives it, less the
	// left edge of the window. The window takes right-left cells up to the
	// end of the row.
	keycap := "1\ufe0f\u20e3"
	content := "k: ab日本" + keycap + "c🥒x"
	runes := []rune(content)

	view := niceyaml.NewSourceFromString(content + "\n").View()
	view.BlendOverlay(kind.GenericHighlight, position.NewRange(position.New(0, 5), position.New(0, 7)))

	p := printer.New(printer.WithGutter(printer.NoGutter), printer.WithStyles(theme.Charm))
	l := p.Layout(view)
	row := p.Print(view)
	rowWidth := ansi.StringWidth(row)

	// Each column that starts a cluster, with the cluster it starts.
	clusters := map[int]string{}

	for col := 0; col < len(runes); {
		cluster, _ := ansi.FirstGraphemeCluster(string(runes[col:]), ansi.GraphemeWidth)
		clusters[col] = cluster
		col += len([]rune(cluster))
	}

	for left := 0; left <= rowWidth; left++ {
		for right := left; right <= rowWidth+2; right++ {
			cut, width := printer.CutWidth(row, left, right)
			require.Equal(t, max(0, min(right, rowWidth)-left), width, "window [%d, %d)", left, right)

			for col, want := range clusters {
				cell := l.CellOf(position.New(0, col))
				w := ansi.StringWidth(want)

				if cell < left || cell+w > right {
					continue
				}

				got := ansi.Strip(printer.Cut(cut, cell-left, cell-left+w))
				require.Equal(t, want, got, "window [%d, %d), column %d", left, right, col)
			}
		}
	}

	// A window can show half of 日. The space in its place sits where the
	// rune sits in a window that shows all of it, so it keeps the
	// background of the highlight.
	assert.Equal(t, strings.Replace(printer.Cut(row, 5, 7), "日", " ", 1), printer.Cut(row, 6, 7))
}

// clipLetters is a line long enough for a clip of ten columns to cut it
// at both ends and between two marks.
const clipLetters = "key: abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

// newClippedView returns a view of input, one line, clipped to cols
// columns, with an error overlay on each of overlays and the annotations
// given.
func newClippedView(input string, cols int, overlays []position.Span, annotations ...line.Annotation) *line.View {
	view := niceyaml.NewSourceFromString(input + "\n").View()
	view.AddLineOverlay(0, yamltest.ErrorOverlays(overlays...)...)
	view.Annotate(0, annotations...)

	return view.Clip(cols)
}

func TestPrinter_Clip(t *testing.T) {
	t.Parallel()

	couple := "\U0001F468\u200d\U0001F469"

	tcs := map[string]struct {
		input       string
		want        []string
		overlays    []position.Span
		annotations []line.Annotation
		cols        int
		wrap        int
	}{
		"line that fits stays whole": {
			input: "key: value",
			cols:  10,
			want:  []string{"key: value"},
		},
		"line with no mark keeps its first columns": {
			input: clipLetters,
			cols:  10,
			want:  []string{"key: abcde..."},
		},
		"two marks far apart get a window each": {
			input:    clipLetters,
			cols:     10,
			overlays: []position.Span{position.NewSpan(15, 16), position.NewSpan(45, 46)},
			annotations: []line.Annotation{
				{Content: "first", Placement: line.Below, Cols: position.NewSpan(15, 16)},
				{Content: "second", Placement: line.Below, Cols: position.NewSpan(45, 46)},
			},
			want: []string{
				"...fghijklmno...JKLMNOPQRS...",
				"        ^ first",
				"                     ^ second",
			},
		},
		"long mark keeps its carets inside the window": {
			input:       clipLetters,
			cols:        10,
			overlays:    []position.Span{position.NewSpan(20, 57)},
			annotations: []line.Annotation{{Content: "long", Placement: line.Below, Cols: position.NewSpan(20, 57)}},
			want: []string{
				"...klmnopqrst...",
				"        ^^^^^ long",
			},
		},
		"annotation above sits over its column": {
			input:       clipLetters,
			cols:        10,
			annotations: []line.Annotation{{Content: "above", Placement: line.Above, Cols: position.NewSpan(40, 40)}},
			want: []string{
				"        above",
				"...EFGHIJKLMN...",
			},
		},
		"annotation without content gets its caret": {
			input:       clipLetters,
			cols:        10,
			overlays:    []position.Span{position.NewSpan(57, 57)},
			annotations: []line.Annotation{{Placement: line.Below, Cols: position.NewSpan(57, 57)}},
			want: []string{
				"...QRSTUVWXYZ",
				"             ^",
			},
		},
		"wide runes take two cells each": {
			input:       "k: " + strings.Repeat("日本語", 8),
			cols:        8,
			overlays:    []position.Span{position.NewSpan(15, 16)},
			annotations: []line.Annotation{{Content: "wide", Placement: line.Below, Cols: position.NewSpan(15, 16)}},
			want: []string{
				"...語日本語日本語日...",
				"           ^^ wide",
			},
		},
		"tab shows as its picture": {
			input:       "k: a\tb\tc\td\te\tf\tg\th\ti\tj\tk\tl\tm",
			cols:        8,
			overlays:    []position.Span{position.NewSpan(15, 16)},
			annotations: []line.Annotation{{Content: "tab", Placement: line.Below, Cols: position.NewSpan(15, 16)}},
			want: []string{
				"...e␉f␉g␉h␉...",
				"       ^ tab",
			},
		},
		"mark inside a grapheme cluster shows the cluster": {
			input:       "k: " + couple + strings.Repeat("a", 12) + couple + strings.Repeat("b", 24),
			cols:        8,
			overlays:    []position.Span{position.NewSpan(20, 21)},
			annotations: []line.Annotation{{Content: "couple", Placement: line.Below, Cols: position.NewSpan(20, 21)}},
			want: []string{
				"...aa" + couple + "bbb...",
				"     ^^ couple",
			},
		},
		// Each message sits beside the wrapped row that holds its run.
		"clipped row wraps to the width": {
			input:    clipLetters,
			cols:     10,
			wrap:     16,
			overlays: []position.Span{position.NewSpan(15, 16), position.NewSpan(45, 46)},
			annotations: []line.Annotation{
				{Content: "first", Placement: line.Below, Cols: position.NewSpan(15, 16)},
				{Content: "second", Placement: line.Below, Cols: position.NewSpan(45, 46)},
			},
			want: []string{
				"...fghijklmno...",
				"        ^ first",
				"JKLMNOPQRS...",
				"     ^ second",
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := newClippedView(tc.input, tc.cols, tc.overlays, tc.annotations...)
			p := testPrinter().With(printer.WithWrapWidth(tc.wrap))

			assert.Equal(t, stringtest.JoinLF(tc.want...), p.Print(view))

			// The layout counts the rows Print writes.
			assert.Equal(t, len(tc.want), p.Layout(view).Rows())
		})
	}
}

func TestPrinter_Clip_Styles(t *testing.T) {
	t.Parallel()

	// The ellipsis renders in kind.UISeparator, and an overlay keeps its
	// style on the columns the window shows.
	p := testPrinterWithStyles(
		style.Set(kind.UISeparator, bracketStyle("<", ">")),
		style.Set(kind.GenericError, bracketStyle("[", "]")),
	).With(printer.WithAnnotation(printer.NoAnnotation))

	tcs := map[string]struct {
		want        string
		overlays    []position.Span
		annotations []line.Annotation
		flag        line.Flag
	}{
		"ellipsis at the end": {want: "key: abcde<...>"},
		"ellipsis at both ends of a marked window": {
			overlays: []position.Span{position.NewSpan(30, 32)},
			want:     "<...>uvwxy[zA]BCD<...>",
		},
		"overlay cut at the end of its window": {
			overlays: []position.Span{position.NewSpan(20, 57)},
			want:     "<...>klmno[pqrst]<...>",
		},
		"overlay across two windows styles its part of each": {
			overlays:    []position.Span{position.NewSpan(15, 47)},
			annotations: []line.Annotation{{Placement: line.Below, Cols: position.NewSpan(45, 45)}},
			want:        "<...>fghij[klmno]<...>[JKLMNOP]QRS<...>",
		},
		"ellipsis of a deleted line takes its diff style": {
			flag: line.FlagDeleted,
			want: "key: abcde...",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := newClippedView(clipLetters, 10, tc.overlays, tc.annotations...)
			view.SetFlag(0, tc.flag)

			assert.Equal(t, tc.want, p.Print(view))
		})
	}
}

func TestPrinter_Clip_AnnotationContext(t *testing.T) {
	t.Parallel()

	// An annotation func gets the row of a clipped line as its content,
	// with every column counted in that row.
	var got []printer.AnnotationContext

	p := testPrinter().With(printer.WithAnnotation(func(ctx printer.AnnotationContext) []printer.AnnotationRow {
		got = append(got, ctx)

		col := ctx.Annotations.Start()

		return []printer.AnnotationRow{{Col: col, Marker: "^ ", Text: "at " + strconv.Itoa(ctx.ColWidth(col))}}
	}))

	view := newClippedView(clipLetters, 10,
		[]position.Span{position.NewSpan(15, 47), position.NewSpan(45, 46)},
		line.Annotation{Content: "first", Placement: line.Below, Cols: position.NewSpan(45, 46)},
		line.Annotation{Content: "second", Placement: line.Below, Cols: position.NewSpan(15, 47)},
	)

	assert.Equal(t, stringtest.JoinLF(
		"...fghijklmno...JKLMNOPQRS...",
		"        ^ at 8",
	), p.Print(view))

	require.Len(t, got, 1)

	ctx := got[0]

	assert.Equal(t, "...fghijklmno...JKLMNOPQRS...", ctx.Content)
	assert.Equal(t, []int{0}, ctx.RowStarts)
	assert.Equal(t, []int{29}, ctx.RowEnds)
	assert.Equal(t, yamltest.ErrorOverlays(
		position.NewSpan(8, 13),
		position.NewSpan(16, 23),
		position.NewSpan(21, 22),
	), ctx.Overlays)
	// An annotation labels the run of the row that shows its columns,
	// with the ellipsis inside it.
	assert.Equal(t, line.Annotations{
		{Content: "first", Placement: line.Below, Cols: position.NewSpan(21, 22)},
		{Content: "second", Placement: line.Below, Cols: position.NewSpan(8, 23)},
	}, ctx.Annotations)

	// The view keeps the columns of its content.
	assert.Equal(t, position.NewSpan(45, 46), view.Annotations(0)[0].Cols)
	assert.Equal(t, position.NewSpan(15, 47), view.Overlays(0)[0].Cols)
}
