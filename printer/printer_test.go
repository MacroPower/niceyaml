package printer_test

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"os"
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
	"go.jacobcolvin.com/niceyaml/normalizer"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/kind"
	"go.jacobcolvin.com/niceyaml/style/theme"
)

// testOverlayHighlight is a custom kind.Kind constant for test highlights.
const testOverlayHighlight kind.Kind = "testOverlayHighlight"

// testHighlightStyle returns a style that wraps content in brackets for easy verification.
func testHighlightStyle() lipgloss.Style {
	return lipgloss.NewStyle().Transform(func(str string) string {
		return "[" + str + "]"
	})
}

// testPrinter returns a printer without styles or padding for predictable output.
func testPrinter() *printer.Printer {
	return testPrinterWithGutter(printer.NoGutter)
}

// testPrinterWithGutter returns a printer without styles but with a custom gutter.
func testPrinterWithGutter(gutter printer.Gutter) *printer.Printer {
	return printer.New(
		printer.WithStyles(style.New(
			lipgloss.NewStyle(),
			style.Set(testOverlayHighlight, testHighlightStyle()),
		)),
		printer.WithContainerStyle(lipgloss.NewStyle()),
		printer.WithGutter(gutter),
	)
}

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

// printDiff generates a full-file diff between two YAML strings.
// It outputs the entire file with markers for inserted and deleted lines.
func printDiff(p *printer.Printer, before, after string) string {
	beforeTks := niceyaml.NewSourceFromString(before, niceyaml.WithName("before"))
	afterTks := niceyaml.NewSourceFromString(after, niceyaml.WithName("after"))

	return p.Print(diff.Diff(beforeTks.Lines(), afterTks.Lines()).Unified())
}

// printDiffSummary generates a summary diff showing only changed lines with context.
func printDiffSummary(p *printer.Printer, before, after string, context int) string {
	beforeTks := niceyaml.NewSourceFromString(before, niceyaml.WithName("before"))
	afterTks := niceyaml.NewSourceFromString(after, niceyaml.WithName("after"))

	source := diff.Diff(beforeTks.Lines(), afterTks.Lines()).Hunks(context)

	if source.Count() == 0 {
		return ""
	}

	return p.Print(source)
}

// testFinder returns a Finder configured for testing.
func testFinder(norm finder.Normalizer) *finder.Finder {
	var opts []finder.Option

	if norm != nil {
		opts = append(opts, finder.WithNormalizer(norm))
	}

	return finder.New(opts...)
}

func TestPrinter_Anchor(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		anchor: &x 1
		alias: *x
	`)
	tks := lexer.Tokenize(input)

	p := testPrinter()

	got := p.Print(niceyaml.NewSourceFromTokens(tks).View())
	assert.Equal(t, input, got)
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
			// 0-indexed: col 7-10 = 1-indexed col 8-11
			rng: position.NewRange(
				position.New(0, 7),
				position.New(0, 10),
			),
		},
		"partial token - start of value (0-indexed)": {
			input: "key: value",
			want:  "key: [val]ue",
			// 0-indexed: col 5-8 = 1-indexed col 6-9
			rng: position.NewRange(
				position.New(0, 5),
				position.New(0, 8),
			),
		},
		"full token (0-indexed)": {
			input: "key: value",
			want:  "key: [value]",
			// 0-indexed: col 5-10 = 1-indexed col 6-11
			rng: position.NewRange(
				position.New(0, 5),
				position.New(0, 10),
			),
		},
		"first character (line 0, col 0)": {
			input: "key: value",
			want:  "[k]ey: value",
			rng: position.NewRange(
				position.New(0, 0),
				position.New(0, 1),
			),
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
			// 0-indexed: line 0 col 7 to line 1 col 8
			rng: position.NewRange(
				position.New(0, 7),
				position.New(1, 8),
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := niceyaml.NewSourceFromTokens(lexer.Tokenize(tc.input)).View()
			view.AddOverlay(testOverlayHighlight, tc.rng)

			p := testPrinter()

			got := p.Print(view)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPrinter_PrintError(t *testing.T) {
	t.Parallel()

	p := printer.New(
		printer.WithStyles(yamltest.NewXMLStyles()),
		printer.WithGutter(printer.NoGutter),
		printer.WithContainerStyle(lipgloss.NewStyle()),
	)

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\n")
	bound := yamltest.Bind(t, source, niceyaml.NewError("bad", niceyaml.AtPath(paths.Root().Child("b"))))
	other := niceyaml.NewSourceFromString("c: 3\n")
	nested := niceyaml.NewSourceFromString("a:\n  b: 2\n")
	empty := niceyaml.NewSourceFromString("a:\nb: 2\n")
	flags := niceyaml.NewSourceFromString("flags: 🇺🇸🇫🇷\n")

	// The root has no message beside its range, so a caret run under the
	// range shows its extent without color.
	excerpt := stringtest.JoinLF(
		"<nameTag>a</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalNumberInteger>1</literalNumberInteger>",
		"<nameTag>b</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><genericError>2</genericError>",
		"<textError>   ^</textError>",
	)

	otherExcerpt := stringtest.JoinLF(
		"<nameTag>c</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><genericError>3</genericError>",
		"<textError>   ^</textError>",
	)

	// The style of the indent adds text in front of the second line, and
	// the caret still lands under the value.
	nestedExcerpt := stringtest.JoinLF(
		"<nameTag>a</nameTag><punctuationMappingValue>:</punctuationMappingValue>",
		"<text>  </text><nameTag>b</nameTag><punctuationMappingValue>:</punctuationMappingValue>"+
			"<text> </text><genericError>2</genericError>",
		"<textError>     ^</textError>",
	)

	// Both values marked, each with its message below it.
	annotated := stringtest.JoinLF(
		"<nameTag>a</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><genericError>1</genericError>",
		"<textError>   ^ bad a</textError>",
		"<nameTag>b</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><genericError>2</genericError>",
		"<textError>   ^ bad b</textError>",
	)

	tcs := map[string]struct {
		err  error
		want string
	}{
		"nil": {
			err:  nil,
			want: "",
		},
		"error without a source": {
			err:  errors.New("boom"),
			want: "boom",
		},
		"bound error": {
			err:  bound,
			want: "2:4: $.b: bad\n\n" + excerpt,
		},
		"wrapped bound error keeps the wrapper's context": {
			err:  fmt.Errorf("document 0: %w", bound),
			want: "document 0: 2:4: $.b: bad\n\n" + excerpt,
		},
		"bound error on an indented line": {
			err: yamltest.Bind(
				t,
				nested,
				niceyaml.NewError("bad", niceyaml.AtPath(paths.Root().Child("a").Child("b"))),
			),
			want: "2:6: $.a.b: bad\n\n" + nestedExcerpt,
		},
		"bound error without a location": {
			err:  yamltest.Bind(t, source, niceyaml.NewError("bad")),
			want: "bad",
		},
		// A location with no token under it covers no column, so a lone
		// caret marks its spot.
		"bound error at an empty value": {
			err: yamltest.Bind(t, empty, niceyaml.NewError("required", niceyaml.AtPath(paths.Root().Child("a")))),
			want: "1:3: $.a: required\n\n" + stringtest.JoinLF(
				"<nameTag>a</nameTag><punctuationMappingValue>:</punctuationMappingValue>",
				"<textError>  ^</textError>",
				"<nameTag>b</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalNumberInteger>2</literalNumberInteger>",
			),
		},
		"bound error past the end of a line": {
			err: yamltest.Bind(t, source, niceyaml.NewError("bad", niceyaml.AtPosition(position.New(0, 500)))),
			want: "1:501: bad\n\n" + stringtest.JoinLF(
				"<nameTag>a</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalNumberInteger>1</literalNumberInteger>",
				"<textError>    ^</textError>",
				"<nameTag>b</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalNumberInteger>2</literalNumberInteger>",
			),
		},
		// The caret run starts under the second flag, not under the
		// second rune of the first flag.
		"bound error after a multi-rune cluster": {
			err: yamltest.Bind(t, flags, niceyaml.NewError("", niceyaml.AtRange(
				position.NewRange(position.New(0, 9), position.New(0, 11)),
			))),
			want: "1:10:\n\n" + stringtest.JoinLF(
				"<nameTag>flags</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text>"+
					"<literalString>🇺🇸</literalString><genericError>🇫🇷</genericError>",
				"<textError>         ^^</textError>",
			),
		},
		"bound error with an empty message": {
			err:  yamltest.Bind(t, source, niceyaml.WrapError(nil, niceyaml.AtPath(paths.Root().Child("b")))),
			want: "2:4: $.b:\n\n" + excerpt,
		},
		"joined bound errors print every excerpt": {
			err: errors.Join(
				fmt.Errorf("first: %w", bound),
				fmt.Errorf("second: %w", yamltest.Bind(t, other,
					niceyaml.NewError("bad", niceyaml.AtPath(paths.Root().Child("c"))),
				)),
			),
			want: "├── first: 2:4: $.b: bad\n└── second: 1:4: $.c: bad\n\n" + excerpt + "\n\n" + otherExcerpt,
		},
		// A row drops its trailing spaces: the space after "b", and the
		// blank indent in front of the blank line of the last branch.
		"joined error with a blank line in a branch": {
			err:  errors.Join(errors.New("a"), errors.New("b \n\nc")),
			want: "├── a\n└── b\n\n    c",
		},
		"nested errors draw as branches in position order": {
			err: yamltest.Bind(t, source, niceyaml.NewError("2 problems", niceyaml.WithErrors(
				niceyaml.NewError("bad b", niceyaml.AtPath(paths.Root().Child("b"))),
				niceyaml.NewError("bad a", niceyaml.AtPath(paths.Root().Child("a"))),
			))),
			want: "2 problems\n├── 1:4: $.a: bad a\n└── 2:4: $.b: bad b\n\n" + annotated,
		},
		"joined nested errors draw as a forest of subtrees": {
			err: errors.Join(
				yamltest.Bind(t, source, niceyaml.NewError("2 problems", niceyaml.WithErrors(
					niceyaml.NewError("bad a", niceyaml.AtPath(paths.Root().Child("a"))),
					niceyaml.NewError("bad b", niceyaml.AtPath(paths.Root().Child("b"))),
				))),
				yamltest.Bind(t, other, niceyaml.NewError("bad", niceyaml.AtPath(paths.Root().Child("c")))),
			),
			want: "├── 2 problems\n│   ├── 1:4: $.a: bad a\n│   └── 2:4: $.b: bad b\n└── 1:4: $.c: bad\n\n" +
				annotated + "\n\n" + otherExcerpt,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, p.PrintError(tc.err))
		})
	}
}

func TestPrinter_PrintError_ControlCharacters(t *testing.T) {
	t.Parallel()

	p := printer.New(printer.WithStyles(yamltest.NewXMLStyles()))

	err := fmt.Errorf("outer: %w", errors.Join(
		errors.New("bad \x1b[31mred\x07 thing"),
		errors.New("second"),
	))

	got := p.PrintError(err)
	assert.NotContains(t, got, "\x1b")
	assert.NotContains(t, got, "\x07")
	assert.Contains(t, got, "bad \u241b[31mred\u2407 thing")

	// An error with no nested errors is the root of a tree of one node, so
	// its message gets the same treatment as a branch.
	assert.Equal(t, "bad \u241b[31mred\u2407 thing", p.PrintError(errors.New("bad \x1b[31mred\x07 thing")))

	// The reason a location did not resolve names the path, which spells a
	// key of the document, so it gets the same treatment.
	source := niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName("f.yaml"))
	bound := source.Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Root().Child("mi\x1b[31mss"))))

	got = p.PrintError(bound)
	assert.NotContains(t, got, "\x1b")
	assert.Contains(t, got, "no excerpt: ")
	assert.Contains(t, got, "mi\u241b[31mss")
}

func TestPrinter_PrintError_Wrap(t *testing.T) {
	t.Parallel()

	const width = 24

	p := printer.New(
		printer.WithStyles(yamltest.NewXMLStyles()),
		printer.WithWrap(width),
	)

	long := "a very long error message that certainly exceeds the width of the terminal"

	err := fmt.Errorf("%s: %w", long, errors.Join(
		errors.New(long),
		fmt.Errorf("%s: %w", long, errors.New(long)),
	))

	got := p.PrintError(err)
	rows := strings.Split(got, "\n")
	require.Greater(t, len(rows), 3, "every message wraps")

	for i, row := range rows {
		assert.LessOrEqual(t, lipgloss.Width(row), width, "row %d: %q", i, row)
	}

	// A wrapped row of a nested message stays under its connector, so the
	// second row of the first branch starts with the indent of the tree,
	// and no row carries padding after its text.
	assert.Contains(t, got, "\n\u2502   message that\n")
	assert.NotContains(t, got, " \n")

	// An error with no nested errors wraps to the full width.
	got = p.PrintError(errors.New(long))
	rows = strings.Split(got, "\n")
	require.Greater(t, len(rows), 1, "the message wraps")

	for i, row := range rows {
		assert.LessOrEqual(t, lipgloss.Width(row), width, "row %d: %q", i, row)
	}

	assert.NotContains(t, got, " \n")
}

func TestPrinter_PrintError_WrapFitsExcerpts(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\nb: " + strings.Repeat("x", 80) + "\nc: 3\n")
	bound := yamltest.Bind(t, source, niceyaml.NewError("bad", niceyaml.AtPath(paths.Root().Child("b"))))

	tcs := map[string]struct {
		opts  []printer.Option
		width int
	}{
		"default container": {
			width: 30,
		},
		"bordered container": {
			opts: []printer.Option{
				printer.WithContainerStyle(lipgloss.NewStyle().Border(lipgloss.NormalBorder())),
			},
			width: 30,
		},
		"gutterless": {
			opts:  []printer.Option{printer.WithGutter(printer.NoGutter)},
			width: 40,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := printer.New(append(tc.opts, printer.WithWrap(tc.width))...)

			got := p.PrintError(bound)
			rows := strings.Split(got, "\n")

			// The tree, the blank row after it, the three source lines, and
			// the caret row would fit in six rows without wrapping.
			require.Greater(t, len(rows), 6, "the excerpt wraps")

			// The container's frame counts toward the width, so every row of
			// the excerpt fits it.
			for i, row := range rows {
				assert.LessOrEqual(t, lipgloss.Width(row), tc.width, "row %d: %q", i, row)
			}
		})
	}
}

func TestPrinter_PrintError_MarksRangeWithoutStyles(t *testing.T) {
	t.Parallel()

	// A childless bound error has no message beside its range, so without
	// color the carets on the row below are all that shows its extent.
	source := niceyaml.NewSourceFromString("spec:\n  sla: 99\n  hours:\n")
	bound := yamltest.Bind(t, source, niceyaml.NewError("bad",
		niceyaml.AtPath(paths.Root().Child("spec").Child("sla")),
	))

	tcs := map[string]struct {
		gutter printer.Gutter
		want   string
	}{
		"no gutter": {
			gutter: printer.NoGutter,
			want: stringtest.JoinLF(
				"2:8: $.spec.sla: bad",
				"",
				"spec:",
				"  sla: 99",
				"       ^^",
				"  hours:",
			),
		},
		"line numbers": {
			gutter: printer.DefaultGutter,
			want: stringtest.JoinLF(
				"2:8: $.spec.sla: bad",
				"",
				"   1  spec:",
				"   2    sla: 99",
				"             ^^",
				"   3    hours:",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := printer.New(
				printer.WithStyles(style.Styles{}),
				printer.WithContainerStyle(lipgloss.NewStyle()),
				printer.WithGutter(tc.gutter),
			)

			assert.Equal(t, tc.want, p.PrintError(bound))
		})
	}
}

func TestPrinter_MarksRangeUnderHighlight(t *testing.T) {
	t.Parallel()

	// A search highlight blends onto a line that a bound error marks. The
	// carets below the line still mark the range of the error alone, in
	// whichever order the view gets the two.
	tcs := map[string]struct {
		source         string
		key            string
		highlight      position.Range
		highlightFirst bool
		want           string
	}{
		"highlight on the key": {
			source:    "key: value\nname: other value\n",
			key:       "name",
			highlight: position.NewRange(position.New(1, 0), position.New(1, 4)),
			want: stringtest.JoinLF(
				"key: value",
				"name: other value",
				"      ^^^^^^^^^^^",
			),
		},
		"highlight before the annotation": {
			source:         "key: value\nname: other value\n",
			key:            "name",
			highlight:      position.NewRange(position.New(1, 0), position.New(1, 4)),
			highlightFirst: true,
			want: stringtest.JoinLF(
				"key: value",
				"name: other value",
				"      ^^^^^^^^^^^",
			),
		},
		"empty value": {
			source:    "a: 1\nb:\nc: 2\n",
			key:       "b",
			highlight: position.NewRange(position.New(1, 0), position.New(1, 1)),
			want: stringtest.JoinLF(
				"a: 1",
				"b:",
				"  ^",
				"c: 2",
			),
		},
		"empty value highlighted before the annotation": {
			source:         "a: 1\nb:\nc: 2\n",
			key:            "b",
			highlight:      position.NewRange(position.New(1, 0), position.New(1, 1)),
			highlightFirst: true,
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
			bound := yamltest.Bind(t, source, niceyaml.WrapError(nil,
				niceyaml.AtPath(paths.Root().Child(tc.key)),
			))

			var se *niceyaml.SourceError

			require.ErrorAs(t, bound, &se)

			view := source.View()
			if tc.highlightFirst {
				view.BlendOverlay(kind.GenericHighlight, tc.highlight)
			}

			se.Annotate(view)

			if !tc.highlightFirst {
				view.BlendOverlay(kind.GenericHighlight, tc.highlight)
			}

			p := printer.New(
				printer.WithStyles(style.Styles{}),
				printer.WithContainerStyle(lipgloss.NewStyle()),
				printer.WithGutter(printer.NoGutter),
			)

			assert.Equal(t, tc.want, p.Print(view))
		})
	}
}

func TestPrinter_PrintError_MarksWrappedRange(t *testing.T) {
	t.Parallel()

	items := make([]string, 0, 12)
	for i := range 12 {
		items = append(items, fmt.Sprintf("item%02d", i))
	}

	// The range runs from item02 on the first row to item05 on the second,
	// so each of those rows gets carets below it under its own part of the
	// range.
	source := niceyaml.NewSourceFromString("items: [" + strings.Join(items, ", ") + "]\n")
	bound := yamltest.Bind(t, source, niceyaml.NewError("bad", niceyaml.AtRange(
		position.NewRange(position.New(0, 24), position.New(0, 54)),
	)))

	p := printer.New(
		printer.WithStyles(style.Styles{}),
		printer.WithContainerStyle(lipgloss.NewStyle()),
		printer.WithWrap(40),
	)

	want := stringtest.JoinLF(
		"1:25: bad",
		"",
		"   1  items: [item00, item01, item02,",
		strings.Repeat(" ", 30)+"^^^^^^^",
		"   -  item03, item04, item05, item06,",
		strings.Repeat(" ", 6)+strings.Repeat("^", 22),
		"   -  item07, item08, item09, item10,",
		"   -  item11]",
	)

	assert.Equal(t, want, p.PrintError(bound))
}

func TestPrinter_PrintError_MarksLongWrappedValue(t *testing.T) {
	t.Parallel()

	// A long base64 value wraps into many rows, and each gets a caret row
	// below it as wide as the part of the value it shows.
	tcs := map[string]struct {
		value string
		width int
	}{
		"ascii": {
			value: strings.Repeat("QUJD", 2000),
			width: 80,
		},
		"wide runes": {
			value: strings.Repeat("日本", 500),
			width: 41,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString("data: " + tc.value + "\n")
			err := yamltest.Bind(t, source, niceyaml.NewError("bad", niceyaml.AtPath(paths.Root().Child("data"))))

			p := printer.New(
				printer.WithStyles(style.Styles{}),
				printer.WithContainerStyle(lipgloss.NewStyle()),
				printer.WithGutter(printer.NoGutter),
				printer.WithWrap(tc.width),
			)

			rows := strings.Split(p.PrintError(err), "\n")[2:]
			carets := 0

			// A caret row follows every row that shows part of the value,
			// under all of that part. The key is not part of the range, so
			// its row gets no carets when the value wraps off it.
			for i, row := range rows {
				value := strings.TrimPrefix(row, "data:")
				if strings.Trim(value, " ") == "" || strings.Trim(row, " ^") == "" {
					continue
				}

				require.Less(t, i+1, len(rows), "row %d has no caret row", i)

				mark := rows[i+1]
				assert.Equal(t, strings.Repeat("^", lipgloss.Width(strings.TrimLeft(value, " "))),
					strings.TrimLeft(mark, " "), "row %d", i)
				assert.Equal(t, lipgloss.Width(row), lipgloss.Width(mark), "row %d", i)

				carets += strings.Count(mark, "^")
			}

			assert.Equal(t, lipgloss.Width(tc.value), carets)
		})
	}
}

func TestPrinter_WrappedMarkerRows(t *testing.T) {
	t.Parallel()

	// The carets under a wrapped line take a row below each wrapped row
	// they mark, padded from that row's start, so every caret sits under
	// the column it marks.
	tcs := map[string]struct {
		annotation *line.Annotation
		content    string
		want       string
		cols       []position.Span
		line       int
		width      int
	}{
		// The wrap drops the indent when the first word does not fit
		// beside it, so the first row starts at the word, and so does the
		// padding of the marks under it.
		"indent dropped before a long word": {
			content: "crt: |\n    LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSUMr",
			cols:    []position.Span{position.NewSpan(4, 8)},
			line:    1,
			width:   30,
			want: stringtest.JoinLF(
				"crt: |",
				"LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS",
				"^^^^",
				"0tLS0tCk1JSUMr",
			),
		},
		"indent dropped before a long word under a whole-line range": {
			content: "crt: |\n    LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSUMr",
			cols:    []position.Span{position.NewSpan(0, 48)},
			line:    1,
			width:   30,
			want: stringtest.JoinLF(
				"crt: |",
				"LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS",
				strings.Repeat("^", 30),
				"0tLS0tCk1JSUMr",
				strings.Repeat("^", 14),
			),
		},
		"indent dropped before a long word above the line": {
			content:    "crt: |\n    LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSUMr",
			annotation: &line.Annotation{Placement: line.Above, Col: 4, Content: "here"},
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
			content: "a: |\n  some text          \n  more",
			cols:    []position.Span{position.NewSpan(2, 21)},
			line:    1,
			width:   20,
			want: stringtest.JoinLF(
				"a: |",
				"  some text",
				"  ^^^^^^^^^",
				"  more",
			),
		},
		"trailing spaces dropped on the only row": {
			content: "key: value          ",
			cols:    []position.Span{position.NewSpan(0, 20)},
			width:   16,
			want: stringtest.JoinLF(
				"key: value",
				"^^^^^^^^^^",
			),
		},
		// The wrap would start a row with the accent of a decomposed "é",
		// and the accent stays with its "e" instead, so the caret under
		// the "x" after it lands under the "x".
		"decomposed accent at a wrap boundary": {
			content: "k: abcdefge\u0301xyz",
			cols:    []position.Span{position.NewSpan(12, 13)},
			width:   8,
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
			content: "k: aaaa ́bbbb",
			cols:    []position.Span{position.NewSpan(9, 13)},
			width:   8,
			want: stringtest.JoinLF(
				"k: aaaa",
				"́bbbb",
				"^^^^",
			),
		},
		"spacing mark after a space at a break": {
			content: "k: aaaa िbbbb",
			cols:    []position.Span{position.NewSpan(9, 13)},
			width:   8,
			want: stringtest.JoinLF(
				"k: aaaa",
				"िbbbb",
				" ^^^^",
			),
		},
		"runs on different rows": {
			content: "key: aaaa bbbb cccc dddd",
			cols:    []position.Span{position.NewSpan(5, 9), position.NewSpan(17, 19)},
			width:   10,
			want: stringtest.JoinLF(
				"key: aaaa",
				"     ^^^^",
				"bbbb cccc",
				"       ^^",
				"dddd",
			),
		},
		"range across a break": {
			content: "key: aaaa bbbb cccc dddd",
			cols:    []position.Span{position.NewSpan(5, 14)},
			width:   10,
			want: stringtest.JoinLF(
				"key: aaaa",
				"     ^^^^",
				"bbbb cccc",
				"^^^^",
				"dddd",
			),
		},
		"wide runes": {
			content: "k: 日本語 日本語",
			cols:    []position.Span{position.NewSpan(3, 10)},
			width:   9,
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
			content: "k: 本語x\u200b",
			cols:    []position.Span{position.NewSpan(4, 7)},
			width:   8,
			want: stringtest.JoinLF(
				"k: 本語x\u200b",
				"     ^^^^",
			),
		},
		"zero-width rune at the end of a full row under a whole-line range": {
			content: "ccca日ccca\u200b",
			cols:    []position.Span{position.NewSpan(0, 10)},
			width:   10,
			want: stringtest.JoinLF(
				"ccca日ccca\u200b",
				"^^^^^^^^^^^",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := niceyaml.NewSourceFromString(tc.content).View()
			for _, cols := range tc.cols {
				view.AddOverlay(kind.GenericError, position.NewRange(
					position.New(tc.line, cols.Start),
					position.New(tc.line, cols.End),
				))
			}

			ann := line.Annotation{Placement: line.Below}
			if tc.annotation != nil {
				ann = *tc.annotation
			}

			view.Annotate(tc.line, ann)

			p := testPrinter().With(printer.WithWrap(tc.width))

			got := p.Print(view)
			assert.Equal(t, tc.want, got)

			rows := strings.Split(got, "\n")
			l := p.Layout(view)
			assert.Len(t, rows, l.Rows())

			widest := 0
			for _, row := range rows {
				widest = max(widest, lipgloss.Width(row))
			}

			assert.Equal(t, widest, l.Width())
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
			input:       "a: b\r\nc: d\r\n",
			want:        stringtest.JoinLF("a: b", "c: d"),
			wantOverlay: stringtest.JoinLF("[a][:][ ][b]", "[c][:][ ][d]"),
		},
		"blank line": {
			input:       "a: b\r\n\r\nc: d\r\n",
			want:        stringtest.JoinLF("a: b", "", "c: d"),
			wantOverlay: stringtest.JoinLF("[a][:][ ][b]", "", "[c][:][ ][d]"),
		},
		// The lexer ends the comment token with the CR and starts the next
		// token with the LF.
		"comment ending in CR": {
			input:       "a: b # c\r\nd: e\r\n",
			want:        stringtest.JoinLF("a: b # c", "d: e"),
			wantOverlay: stringtest.JoinLF("[a][:][ ][b][ ][# c]", "[d][:][ ][e]"),
		},
		// The lexer gives the CRLF after a quoted value a token of its own.
		"line ending token": {
			input:       "a: 'x'\r\nb: 'y'\r\n",
			want:        stringtest.JoinLF("a: 'x'", "b: 'y'"),
			wantOverlay: stringtest.JoinLF("[a][:][ ]['x']", "[b][:][ ]['y']"),
		},
		"block scalar": {
			input:       "a: |\r\n  x\r\n  y\r\n",
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
				view.AddOverlay(testOverlayHighlight, position.NewRange(
					position.New(i, 0),
					position.New(i, ln.Width()),
				))
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

func TestPrinter_PrintTokens_EmptyFile(t *testing.T) {
	t.Parallel()

	// Tokenize an empty string to simulate an empty YAML file.
	tks := lexer.Tokenize("")

	p := testPrinter()
	got := p.Print(niceyaml.NewSourceFromTokens(tks).View())

	// The printer renders an empty file as empty output.
	assert.Empty(t, got)
}

func TestPrinter_Fprint(t *testing.T) {
	t.Parallel()

	t.Run("writes same output as Print", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			key: value
			list:
			  - one
			  - two
		`)
		source := niceyaml.NewSourceFromString(input)
		p := testPrinter()

		var sb strings.Builder

		n, err := p.Fprint(&sb, source.View())

		require.NoError(t, err)
		assert.Equal(t, p.Print(source.View()), sb.String())
		assert.Equal(t, len(sb.String()), n)
	})

	t.Run("returns write errors", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value")
		p := testPrinter()

		_, err := p.Fprint(failingWriter{}, source.View())

		require.ErrorIs(t, err, errWriteFailed)
	})
}

// errWriteFailed is the sentinel failingWriter returns.
var errWriteFailed = errors.New("write failed")

// failingWriter always fails, so tests can reach the error paths of Fprint.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errWriteFailed
}

func TestNewPrinter(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  string
		opts  []printer.Option
	}{
		"custom styles": {
			input: "key: value",
			want:  "<nameTag>key</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
			opts: []printer.Option{
				printer.WithStyles(yamltest.NewXMLStyles()),
				printer.WithContainerStyle(lipgloss.NewStyle()),
				printer.WithGutter(printer.NoGutter),
			},
		},
		"xml styles with token types": {
			input: stringtest.Input(`
				key: value
				number: 42
				bool: true
				# comment
			`),
			want: stringtest.JoinLF(
				"<nameTag>key</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
				"<nameTag>number</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalNumberInteger>42</literalNumberInteger>",
				"<nameTag>bool</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalBoolean>true</literalBoolean>",
				"<comment># comment</comment>",
			),
			opts: []printer.Option{
				printer.WithStyles(yamltest.NewXMLStyles()),
				printer.WithContainerStyle(lipgloss.NewStyle()),
				printer.WithGutter(printer.NoGutter),
			},
		},
		"empty styles": {
			input: "key: value",
			want:  "   1  key: value ",
			// The default gutter adds line numbers, and the default style
			// adds trailing padding.
			opts: []printer.Option{printer.WithStyles(style.Styles{})},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := lexer.Tokenize(tc.input)
			p := printer.New(tc.opts...)
			got := p.Print(niceyaml.NewSourceFromTokens(tks).View())

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPrinter_LineNumbers(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  string
	}{
		"single line": {
			input: "key: value",
			want:  "   1 key: value",
		},
		"multiple lines": {
			input: stringtest.JoinLF(
				"key: value",
				"number: 42",
			),
			want: stringtest.JoinLF(
				"   1 key: value",
				"   2 number: 42",
			),
		},
		"multi-line value": {
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
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := lexer.Tokenize(tc.input)

			p := testPrinterWithGutter(printer.LineNumberGutter)

			got := p.Print(niceyaml.NewSourceFromTokens(tks).View())
			assert.Equal(t, tc.want, got)
		})
	}
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
		gutter printer.Gutter
		want   string
		spans  position.Spans
	}{
		"full range": {
			spans:  nil, // Empty variadic prints all lines.
			gutter: printer.NoGutter,
			want: stringtest.JoinLF(
				"first: 1",
				"second: 2",
				"third: 3",
				"fourth: 4",
				"fifth: 5",
			),
		},
		"full range with line numbers": {
			spans:  nil,
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
			spans:  position.Spans{position.NewSpan(1, 4)},
			gutter: printer.NoGutter,
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
			spans:  position.Spans{position.NewSpan(0, 2)},
			gutter: printer.NoGutter,
			want: stringtest.JoinLF(
				"first: 1",
				"second: 2",
			),
		},
		"to end": {
			spans:  position.Spans{position.NewSpan(3, 5)},
			gutter: printer.NoGutter,
			want: stringtest.JoinLF(
				"fourth: 4",
				"fifth: 5",
			),
		},
		"single line": {
			spans:  position.Spans{position.NewSpan(2, 3)},
			gutter: printer.NoGutter,
			want:   "third: 3",
		},
		"single line with line numbers": {
			spans:  position.Spans{position.NewSpan(2, 3)},
			gutter: printer.LineNumberGutter,
			want:   "   3 third: 3",
		},
		"empty result": {
			spans:  position.Spans{position.NewSpan(10, 21)},
			gutter: printer.NoGutter,
			want:   "",
		},
		"two disjoint spans": {
			spans: position.Spans{
				position.NewSpan(0, 1),
				position.NewSpan(3, 5),
			},
			gutter: printer.NoGutter,
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
			gutter: printer.NoGutter,
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
			gutter: printer.NoGutter,
			want: stringtest.JoinLF(
				"first: 1",
				"second: 2",
				"third: 3",
				"fourth: 4",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := testPrinterWithGutter(tc.gutter)
			lines := niceyaml.NewSourceFromString(input)

			got := p.Print(lines.View().Slice(tc.spans...))
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPrinter_PrintTokenDiff(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		before string
		after  string
		want   string
	}{
		"no changes": {
			before: "key: value\n",
			after:  "key: value\n",
			want:   "   1  key: value",
		},
		"simple addition": {
			before: "key: value\n",
			after: stringtest.JoinLF(
				"key: value",
				"new: line",
				"",
			),
			want: stringtest.JoinLF(
				"   1  key: value",
				"   2 +new: line",
			),
		},
		"simple deletion": {
			before: stringtest.JoinLF(
				"key: value",
				"old: line",
				"",
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
			before: stringtest.JoinLF(
				"line1: a",
				"line3: c",
				"",
			),
			after: stringtest.JoinLF(
				"line1: a",
				"line2: b",
				"line3: c",
				"",
			),
			want: stringtest.JoinLF(
				"   1  line1: a",
				"   2 +line2: b",
				"   3  line3: c",
			),
		},
		"deletion with context": {
			before: stringtest.JoinLF(
				"line1: a",
				"line2: b",
				"line3: c",
				"",
			),
			after: stringtest.JoinLF(
				"line1: a",
				"line3: c",
				"",
			),
			want: stringtest.JoinLF(
				"   1  line1: a",
				"   2 -line2: b",
				"   2  line3: c",
			),
		},
		"multiline yaml modification": {
			before: stringtest.JoinLF(
				"apiVersion: v1",
				"kind: Pod",
				"metadata:",
				"  name: test",
				"",
			),
			after: stringtest.JoinLF(
				"apiVersion: v1",
				"kind: Pod",
				"metadata:",
				"  name: modified",
				"  labels:",
				"    app: test",
				"",
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
			before: stringtest.JoinLF(
				"a: 1",
				"b: 2",
				"c: 3",
				"d: 4",
				"",
			),
			after: stringtest.JoinLF(
				"a: 1",
				"b: changed",
				"c: 3",
				"d: 4",
				"e: 5",
				"",
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
			before: "key: value\r\nold: line\r\n",
			after:  "key: value\r\nnew: line\r\n",
			want: stringtest.JoinLF(
				"   1  key: value",
				"   2 -old: line",
				"   2 +new: line",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := printer.New(
				printer.WithStyles(style.Styles{}),
				printer.WithContainerStyle(lipgloss.NewStyle()),
			)
			got := printDiff(p, tc.before, tc.after)

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPrinter_PrintTokenDiff_Ordering(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		before string
		after  string
		want   []string
	}{
		"deleted lines appear inline": {
			before: stringtest.JoinLF(
				"a: 1",
				"b: 2",
				"c: 3",
				"",
			),
			after: stringtest.JoinLF(
				"a: 1",
				"c: 3",
				"",
			),
			want: []string{" ", "-", " "},
		},
		"modifications show delete before insert": {
			before: "key: old\n",
			after:  "key: new\n",
			want:   []string{"-", "+"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := testPrinterWithGutter(printer.DiffGutter)
			got := printDiff(p, tc.before, tc.after)

			lines := strings.Split(got, "\n")

			require.Len(t, lines, len(tc.want))

			for i, prefix := range tc.want {
				assert.True(t, strings.HasPrefix(lines[i], prefix),
					"line %d should have prefix %q, got %q", i, prefix, lines[i])
			}
		})
	}
}

func TestPrinter_WordWrap(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		gutter printer.Gutter
		input  string
		want   string
		width  int
	}{
		"no wrap when width is zero": {
			input:  "key: value",
			width:  0,
			gutter: printer.NoGutter,
			want:   "key: value",
		},
		"simple wrap": {
			input:  "key: this is a very long value that should wrap",
			width:  20,
			gutter: printer.NoGutter,
			want: stringtest.JoinLF(
				"key: this is a very",
				"long value that",
				"should wrap",
			),
		},
		"wrap on slash": {
			input:  "path: /usr/local/bin/something",
			width:  20,
			gutter: printer.NoGutter,
			want: stringtest.JoinLF(
				"path: /usr/local/",
				"bin/something",
			),
		},
		"wrap on hyphen": {
			input:  "name: very-long-hyphenated-name",
			width:  20,
			gutter: printer.NoGutter,
			want: stringtest.JoinLF(
				"name: very-long-",
				"hyphenated-name",
			),
		},
		"short content no wrap": {
			input:  "key: value",
			width:  50,
			gutter: printer.NoGutter,
			want:   "key: value",
		},
		"multi-line content": {
			input: stringtest.JoinLF(
				"key: value",
				"another: long value that should wrap here",
			),
			width:  20,
			gutter: printer.NoGutter,
			want: stringtest.JoinLF(
				"key: value",
				"another: long value",
				"that should wrap",
				"here",
			),
		},
		// Line number gutter tests.
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
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := lexer.Tokenize(tc.input)

			p := testPrinterWithGutter(tc.gutter).With(printer.WithWrap(tc.width))

			got := p.Print(niceyaml.NewSourceFromTokens(tks).View())
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPrinter_WordWrap_NarrowWidth(t *testing.T) {
	t.Parallel()

	// Only a width of 0 turns wrapping off. A positive width at or below the
	// gutter width still wraps, at one column of content per row.
	input := "key: this is a long value"
	view := niceyaml.NewSourceFromString(input).View()

	tcs := map[string]struct {
		width int
	}{
		"width below the gutter":    {width: 3},
		"width equal to the gutter": {width: 5},
		"width one past the gutter": {width: 6},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := testPrinterWithGutter(printer.LineNumberGutter).With(printer.WithWrap(tc.width))

			got := p.Print(view)
			rows := strings.Split(got, "\n")

			assert.Greater(t, len(rows), 1)
			assert.Equal(t, []int{len(rows)}, layoutRows(p.Layout(view)))

			for _, row := range rows {
				assert.LessOrEqual(t, lipgloss.Width(row), 6, row)
			}
		})
	}
}

func TestPrinter_WordWrap_WideAtOneColumn(t *testing.T) {
	t.Parallel()

	// The layout must map the column of each wide rune of line 0 to the
	// printed row that holds it. When the wrap drops a row that holds a
	// rune of the content, the layout matches the rows after it to the
	// wrong columns.
	assertClusterRows := func(t *testing.T, l printer.Layout, input string, rows []string) {
		t.Helper()

		for col, r := range []rune(input) {
			if lipgloss.Width(string(r)) < 2 {
				continue
			}

			row := l.RowOf(position.New(0, col))
			require.GreaterOrEqual(t, row, 0)
			require.Less(t, row, len(rows))
			assert.Contains(t, rows[row], string(r), "column %d", col)
		}
	}

	// A cluster two cells wide cannot fit in one column, so it takes a row
	// of its own and runs past the width, with no empty row before or
	// after it.
	tcs := map[string]struct {
		gutter     printer.Gutter
		input      string
		want       string
		annotation line.Annotation
		width      int
	}{
		"wide value": {
			gutter: printer.NoGutter,
			input:  "k: 日本語",
			width:  1,
			want:   stringtest.JoinLF("k", ":", "日", "本", "語"),
		},
		"wide key": {
			gutter: printer.NoGutter,
			input:  "日本: v",
			width:  1,
			want:   stringtest.JoinLF("日", "本", ":", " ", "v"),
		},
		"wide cluster between narrow ones": {
			gutter: printer.NoGutter,
			input:  "a日b: v",
			width:  1,
			want:   stringtest.JoinLF("a", "日", "b", ":", " ", "v"),
		},
		"emoji with a skin tone modifier": {
			gutter: printer.NoGutter,
			input:  "k: 👍🏽",
			width:  1,
			want:   stringtest.JoinLF("k", ":", "👍🏽"),
		},
		"no-break space ahead of a wide cluster": {
			// The wrap breaks only on an ASCII space, so the no-break space
			// is content and keeps its row.
			gutter: printer.NoGutter,
			input:  "k: a\u00a0日本語",
			width:  1,
			want:   stringtest.JoinLF("k", ":", "a", "\u00a0", "日", "本", "語"),
		},
		"zero-width space ahead of a wide cluster": {
			// The zero-width space takes no cells, but it is content, so it
			// keeps its row.
			gutter: printer.NoGutter,
			input:  "k: \u200b日本",
			width:  1,
			want:   stringtest.JoinLF("k", ":", "\u200b", "日", "本"),
		},
		"gutter leaves one column": {
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
			gutter: printer.NoGutter,
			input:  "k: v",
			width:  1,
			annotation: line.Annotation{
				Content:   "日本",
				Placement: line.Above,
			},
			want: stringtest.JoinLF("日", "本", "k", ":", "v"),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := niceyaml.NewSourceFromString(tc.input).View()
			if tc.annotation.Content != "" {
				view.Annotate(0, tc.annotation)
			}

			p := testPrinterWithGutter(tc.gutter).With(printer.WithWrap(tc.width))

			got := p.Print(view)
			assert.Equal(t, tc.want, got)

			l := p.Layout(view)
			assert.Equal(t, strings.Count(got, "\n")+1, l.Rows())
			assertClusterRows(t, l, tc.input, strings.Split(got, "\n"))
		})
	}

	t.Run("error message", func(t *testing.T) {
		t.Parallel()

		// The blank row between the message lines stays, since the message
		// asks for it.
		p := testPrinter().With(printer.WithWrap(1))

		got := p.PrintError(errors.New("日本語\n\nnext"))
		assert.Equal(t, stringtest.JoinLF("日", "本", "語", "", "n", "e", "x", "t"), got)
	})

	t.Run("default styles", func(t *testing.T) {
		t.Parallel()

		// The default styles open a style ahead of the value, and the wrap
		// then puts the spaces before a wide cluster on rows of their own.
		// Those rows go, so the rows match those of a printer without
		// styles.
		styled := map[string]struct {
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

		for name, tc := range styled {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				view := niceyaml.NewSourceFromString(tc.input).View()
				p := printer.New(printer.WithGutter(printer.NoGutter), printer.WithWrap(1))

				rows := strings.Split(p.Print(view), "\n")
				for i, row := range rows {
					// The container style pads each row on the right.
					rows[i] = strings.TrimRight(ansi.Strip(row), " ")
				}

				assert.Equal(t, tc.want, rows)

				l := p.Layout(view)
				assert.Len(t, rows, l.Rows())
				assertClusterRows(t, l, tc.input, rows)
			})
		}
	})
}

func TestPrinter_WordWrap_SpaceCluster(t *testing.T) {
	t.Parallel()

	// The wrap keeps at most the space of a cluster that starts with a
	// non-ASCII space and drops the runes after it, so the rows show no
	// mark after such a space. Every row after the cluster still starts
	// at its own column of the content.
	tcs := map[string]struct {
		input string
		want  string
		col   int
		width int
	}{
		"ideographic space with a combining mark": {
			input: "k: a\u3000\u0301b cccc dddd eeee",
			width: 9,
			col:   21,
			want:  stringtest.JoinLF("k: a\u3000b", "cccc dddd", "eeee", "   ^ x"),
		},
		"em space with a zero-width joiner": {
			input: "k: a\u2003\u200db cccc dddd eeee",
			width: 9,
			col:   21,
			want:  stringtest.JoinLF("k: a\u2003b", "cccc dddd", "eeee", "   ^ x"),
		},
		"em space with a variation selector": {
			input: "k: a\u2003\ufe0fb cccc dddd eeee",
			width: 9,
			col:   21,
			want:  stringtest.JoinLF("k: a\u2003b", "cccc dddd", "eeee", "   ^ x"),
		},
		"ideographic space with a combining mark at a break": {
			input: "k: aaaa \u3000\u0301bbbb cccc dddd",
			width: 5,
			col:   11,
			want:  stringtest.JoinLF("k:", "aaaa", "bbbb", " ^ x", "cccc", "dddd"),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := niceyaml.NewSourceFromString(tc.input).View()
			view.Annotate(0, line.Annotation{Content: "x", Placement: line.Below, Col: tc.col})

			p := testPrinter().With(printer.WithWrap(tc.width))

			got := p.Print(view)
			assert.Equal(t, tc.want, got)

			rows := strings.Split(got, "\n")
			l := p.Layout(view)
			assert.Len(t, rows, l.Rows())

			widest := 0
			for _, row := range rows {
				widest = max(widest, lipgloss.Width(row))
			}

			assert.Equal(t, widest, l.Width())

			// Each letter shows on one row, which RowOf must name.
			for col, r := range []rune(tc.input) {
				if !unicode.IsLetter(r) {
					continue
				}

				row := l.RowOf(position.New(0, col))
				require.GreaterOrEqual(t, row, 0)
				require.Less(t, row, len(rows))
				assert.Contains(t, rows[row], string(r), "column %d", col)
			}
		})
	}
}

func TestPrinter_WordWrap_BreakpointPastWidth(t *testing.T) {
	t.Parallel()

	// A breakpoint character just past the wrap width can leave a row
	// wider than the width, and every row must still fit.
	input := "name: some-very-long-name-that-will-wrap-around-the-viewport-width-for-sure\n"
	view := niceyaml.NewSourceFromString(input).View()

	for _, width := range []int{12, 13, 14, 16} {
		p := testPrinterWithGutter(printer.LineNumberGutter).With(printer.WithWrap(width))

		got := p.Print(view)
		rows := strings.Split(got, "\n")

		assert.Equal(t, []int{len(rows)}, layoutRows(p.Layout(view)))

		for _, row := range rows {
			assert.LessOrEqual(t, lipgloss.Width(row), width, "width %d: %q", width, row)
		}
	}
}

func TestPrinter_WordWrap_BreakSpaceBeforeBreakpoint(t *testing.T) {
	t.Parallel()

	// A row that fills the width and then meets a space and a breakpoint
	// drops the space at the break, as it drops the space at any other
	// break, rather than showing it on a row of its own.
	tcs := map[string]struct {
		gutter printer.Gutter
		rows   map[int]int // Column to row.
		input  string
		want   string
		width  int
	}{
		"hyphen after a full row": {
			gutter: printer.DefaultGutter,
			input:  "key: value -x",
			width:  16,
			want:   stringtest.JoinLF("   1  key: value", "   -  -x"),
			rows:   map[int]int{9: 0, 10: 0, 11: 1, 12: 1},
		},
		"slash after a full row": {
			gutter: printer.NoGutter,
			input:  "key: value /x",
			width:  10,
			want:   stringtest.JoinLF("key: value", "/x"),
			rows:   map[int]int{9: 0, 10: 0, 11: 1, 12: 1},
		},
		"two spaces before a hyphen": {
			gutter: printer.NoGutter,
			input:  "key: value  -x",
			width:  10,
			want:   stringtest.JoinLF("key: value", "-x"),
			rows:   map[int]int{10: 0, 11: 0, 12: 1},
		},
		"flags and a path": {
			gutter: printer.NoGutter,
			input:  "cmd: run --verbose --output /tmp/x",
			width:  8,
			want:   stringtest.JoinLF("cmd: run", "--verbos", "e --", "output /", "tmp/x"),
			rows:   map[int]int{7: 0, 8: 0, 9: 1},
		},
		"space before a breakpoint past a hard wrap": {
			gutter: printer.DefaultGutter,
			input:  "k: aa/-/- /b",
			width:  9,
			want:   stringtest.JoinLF("   1  k:", "   -  aa/", "   -  -/-", "   -  /b"),
			rows:   map[int]int{8: 2, 9: 2, 10: 3, 11: 3},
		},
		"space before a breakpoint past a wide cluster": {
			gutter: printer.NoGutter,
			input:  "k: 中b-   // /c",
			width:  6,
			want:   stringtest.JoinLF("k: 中b", "-   //", "/c"),
			rows:   map[int]int{10: 1, 11: 1, 12: 2, 13: 2},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := niceyaml.NewSourceFromString(tc.input).View()
			p := testPrinterWithGutter(tc.gutter).With(printer.WithWrap(tc.width))

			got := p.Print(view)
			assert.Equal(t, tc.want, got)

			l := p.Layout(view)
			assert.Equal(t, strings.Count(got, "\n")+1, l.Rows())

			for col, want := range tc.rows {
				assert.Equal(t, want, l.RowOf(position.New(0, col)), "column %d", col)
			}
		})
	}

	t.Run("error message", func(t *testing.T) {
		t.Parallel()

		p := testPrinter().With(printer.WithWrap(10))

		got := p.PrintError(errors.New("key: value -x"))
		assert.Equal(t, stringtest.JoinLF("key: value", "-x"), got)
	})
}

func TestPrinter_WordWrap_KeepsClusters(t *testing.T) {
	t.Parallel()

	// The wrap takes each ASCII byte alone, so it would start a row with
	// the runes that continue a cluster begun by an ASCII character. They
	// stay with the start of their cluster, and a styled row keeps its
	// style on both sides of the move.
	red := lipgloss.NewStyle().Foreground(lipgloss.Color("1"))

	tcs := map[string]struct {
		input  string
		want   []string
		width  int
		styled bool
	}{
		"decomposed accent": {
			input: "k: abcdefge\u0301xyz",
			width: 8,
			want:  []string{"k:", "abcdefge\u0301", "xyz"},
		},
		"decomposed accent styled": {
			input:  "k: abcdefge\u0301xyz",
			width:  8,
			styled: true,
			want:   []string{"k:", "abcdefge\u0301", "xyz"},
		},
		"combining accent after a space at a break": {
			input: "k: aaaa \u0301bbbb",
			width: 6,
			want:  []string{"k:", "aaaa", "\u0301bbbb"},
		},
		"combining accent after a space at a break styled": {
			input:  "k: aaaa \u0301bbbb",
			width:  6,
			styled: true,
			want:   []string{"k:", "aaaa", "\u0301bbbb"},
		},
		"keycap mark after a space at a break": {
			input: "k: aaaa \u20e3bbbb",
			width: 6,
			want:  []string{"k:", "aaaa", "\u20e3bbbb"},
		},
		"decomposed accent before a run of breakpoints": {
			input: "k: abbaé//----/x",
			width: 5,
			want:  []string{"k:", "abbaé", "//---", "-/x"},
		},
		"decomposed accent before a run of breakpoints styled": {
			input:  "k: abbaé//----/x",
			width:  5,
			styled: true,
			want:   []string{"k:", "abbaé", "//---", "-/x"},
		},
		"keycap before a run of breakpoints": {
			input: "k: \U0001F44D\U0001F3FD日1️⃣-----x",
			width: 5,
			want:  []string{"k:", "\U0001F44D\U0001F3FD日", "1️⃣", "-----", "x"},
		},
		"keycap wider than the width": {
			input: "1\ufe0f\u20e3: bb",
			width: 1,
			want:  []string{"1\ufe0f\u20e3", ":", "b", "b"},
		},
		"keycap styled": {
			input:  "1\ufe0f\u20e3: bb",
			width:  1,
			styled: true,
			want:   []string{"1\ufe0f\u20e3", ":", "b", "b"},
		},
		"keycaps fill a row": {
			input: "k: " + strings.Repeat("1\ufe0f\u20e3", 6),
			width: 8,
			want:  []string{"k:", strings.Repeat("1\ufe0f\u20e3", 4), strings.Repeat("1\ufe0f\u20e3", 2)},
		},
		"keycaps fill a row styled": {
			input:  "k: " + strings.Repeat("1\ufe0f\u20e3", 6),
			width:  8,
			styled: true,
			want:   []string{"k:", strings.Repeat("1\ufe0f\u20e3", 4), strings.Repeat("1\ufe0f\u20e3", 2)},
		},
		"keycap mid row": {
			input: "k: cccxx1\ufe0f\u20e3dddd",
			width: 9,
			want:  []string{"k:", "cccxx1\ufe0f\u20e3dd", "d", "d"},
		},
		"keycap mid row styled": {
			input:  "k: cccxx1\ufe0f\u20e3dddd",
			width:  9,
			styled: true,
			want:   []string{"k:", "cccxx1\ufe0f\u20e3dd", "d", "d"},
		},
		"keycap before a space": {
			input: "k: 1\ufe0f\u20e3cc --cx",
			width: 4,
			want:  []string{"k:", "1\ufe0f\u20e3cc", "--cx"},
		},
		"keycap before a space styled": {
			input:  "k: 1\ufe0f\u20e3cc --cx",
			width:  4,
			styled: true,
			want:   []string{"k:", "1\ufe0f\u20e3cc", "--cx"},
		},
		"keycap split by the wrap": {
			input: "k: ac xb1\ufe0f\u20e3a",
			width: 3,
			want:  []string{"k:", "ac", "xb", "1\ufe0f\u20e3", "a"},
		},
		"keycap split by the wrap styled": {
			input:  "k: ac xb1\ufe0f\u20e3a",
			width:  3,
			styled: true,
			want:   []string{"k:", "ac", "xb", "1\ufe0f\u20e3", "a"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var opts []style.Option

			if tc.styled {
				for _, k := range []kind.Kind{kind.Text, kind.NameTag, kind.LiteralString, kind.PunctuationMappingValue} {
					opts = append(opts, style.Set(k, red))
				}
			}

			p := printer.New(
				printer.WithStyles(style.New(lipgloss.NewStyle(), opts...)),
				printer.WithContainerStyle(lipgloss.NewStyle()),
				printer.WithGutter(printer.NoGutter),
				printer.WithWrap(tc.width),
			)

			view := niceyaml.NewSourceFromString(tc.input).View()
			got := p.Print(view)
			rows := strings.Split(got, "\n")

			plain := make([]string, len(rows))
			for i, row := range rows {
				plain[i] = ansi.Strip(row)
			}

			assert.Equal(t, tc.want, plain)
			assert.Len(t, rows, p.Layout(view).Rows())

			// A row runs past the width only when it holds a lone cluster
			// wider than the width.
			for i, row := range plain {
				if cluster, _ := ansi.FirstGraphemeCluster(row, ansi.GraphemeWidth); cluster != row {
					assert.LessOrEqual(t, lipgloss.Width(row), tc.width, "row %d: %q", i, row)
				}
			}

			if tc.styled {
				// Every row opens the style and closes it again, so no row
				// leaves it open for the next.
				for i, row := range rows {
					assert.Contains(t, row, "\x1b[31m", "row %d", i)
					assert.True(t, strings.HasSuffix(row, "\x1b[m"), "row %d: %q", i, row)
				}
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
		overlay position.Span
		want    []string
		styled  string
		width   int
	}{
		"keycap digit overlaid": {
			input:   "k: 1\ufe0f\u20e32\ufe0f\u20e33\ufe0f\u20e3",
			overlay: position.NewSpan(3, 4),
			width:   8,
			want:    []string{"k: 1\ufe0f\u20e32\ufe0f\u20e3", "3\ufe0f\u20e3"},
			styled:  "\x1b[31m1\ufe0f\u20e3\x1b[m",
		},
		"keycap mark overlaid": {
			input:   "k: 1\ufe0f\u20e32\ufe0f\u20e33\ufe0f\u20e3",
			overlay: position.NewSpan(5, 6),
			width:   8,
			want:    []string{"k: 1\ufe0f\u20e32\ufe0f\u20e3", "3\ufe0f\u20e3"},
		},
		"family member overlaid": {
			input:   "k: x" + family + " y",
			overlay: position.NewSpan(6, 7),
			width:   6,
			want:    []string{"k: x" + family, "y"},
		},
		"family member through the next letter overlaid": {
			input:   "k: x" + family + "yz",
			overlay: position.NewSpan(6, 10),
			width:   6,
			want:    []string{"k:", "x" + family + "yz"},
			styled:  family + "\x1b[31my\x1b[m",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := printer.New(
				printer.WithStyles(style.New(lipgloss.NewStyle(), style.Set(kind.GenericHighlight, red))),
				printer.WithContainerStyle(lipgloss.NewStyle()),
				printer.WithGutter(printer.NoGutter),
				printer.WithWrap(tc.width),
			)

			plainView := niceyaml.NewSourceFromString(tc.input).View()
			view := niceyaml.NewSourceFromString(tc.input).View()
			view.AddOverlay(kind.GenericHighlight, position.NewRange(
				position.New(0, tc.overlay.Start),
				position.New(0, tc.overlay.End),
			))

			got := p.Print(view)
			rows := strings.Split(got, "\n")

			plain := make([]string, len(rows))
			for i, row := range rows {
				plain[i] = ansi.Strip(row)
			}

			assert.Equal(t, tc.want, plain)
			assert.Equal(t, ansi.Strip(p.Print(plainView)), ansi.Strip(got))
			assert.Equal(t, p.Layout(plainView).Width(), p.Layout(view).Width())

			if tc.styled == "" {
				assert.Equal(t, ansi.Strip(got), got)
			} else {
				assert.Contains(t, got, tc.styled)
			}

			// A row runs past the width only when it holds a lone cluster
			// wider than the width.
			for i, row := range plain {
				if cluster, _ := ansi.FirstGraphemeCluster(row, ansi.GraphemeWidth); cluster != row {
					assert.LessOrEqual(t, lipgloss.Width(row), tc.width, "row %d: %q", i, row)
				}
			}

			// The container pads each row by its whole clusters, so every
			// row of the frame takes the same cells.
			framed := p.With(
				printer.WithWrap(0),
				printer.WithContainerStyle(lipgloss.NewStyle().Border(lipgloss.NormalBorder())),
				printer.WithContainerWidth(20),
			).Print(view)

			for i, row := range strings.Split(framed, "\n") {
				assert.Equal(t, 20, lipgloss.Width(ansi.Strip(row)), "row %d: %q", i, row)
			}
		})
	}
}

func TestPrinter_WordWrap_NoEmptyRows(t *testing.T) {
	t.Parallel()

	// The wrap emits empty rows for the word after a run of breakpoints
	// longer than the width, and for a word wider than the width after
	// leading spaces. Those rows hold no text, so they go.
	tcs := map[string]struct {
		input string
		want  string
		width int
	}{
		"word wider than the width after leading spaces": {
			input: "k: |\n    abcdefghijkl\n",
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
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := niceyaml.NewSourceFromString(tc.input).View()
			p := testPrinter().With(printer.WithWrap(tc.width))

			got := p.Print(view)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, strings.Count(got, "\n")+1, p.Layout(view).Rows())
		})
	}

	t.Run("gutter", func(t *testing.T) {
		t.Parallel()

		// The gutter leaves one column of content, and no row shows the
		// gutter alone.
		view := niceyaml.NewSourceFromString("image: nginx-alpine").View()
		p := testPrinterWithGutter(printer.DefaultGutter).With(printer.WithWrap(7))

		rows := strings.Split(p.Print(view), "\n")
		for _, row := range rows {
			assert.NotEmpty(t, strings.TrimSpace(strings.TrimPrefix(row, "   -")), "%q", rows)
		}

		assert.Len(t, rows, len("image:nginx-alpine"))
		assert.Equal(t, []int{len(rows)}, layoutRows(p.Layout(view)))
	})
}

func TestPrinter_WordWrap_BreakpointPastWidth_KeepsStyle(t *testing.T) {
	t.Parallel()

	// The row the hard wrap cuts off must open its style again, so the
	// text it carries renders like the rows around it.
	input := "name: some-very-long-name-that-will-wrap-around-the-viewport-width-for-sure\n"
	view := niceyaml.NewSourceFromString(input).View()

	p := printer.New(printer.WithWrap(13))

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

func TestPrinter_WordWrap_WideLineNumbers(t *testing.T) {
	t.Parallel()

	// With more than 9999 lines the gutter grows by a column, and the wrap
	// width must shrink with it so no rendered row exceeds the width.
	input := strings.Repeat("k: v\n", 10000) + "last: this is a long value that wraps"
	source := niceyaml.NewSourceFromString(input)

	p := testPrinterWithGutter(printer.LineNumberGutter).With(printer.WithWrap(30))

	got := p.Print(source.View().Slice(position.NewSpan(10000, 10001)))
	for row := range strings.SplitSeq(got, "\n") {
		assert.LessOrEqual(t, lipgloss.Width(row), 30, row)
	}

	assert.Equal(t, stringtest.JoinLF(
		"10001 last: this is a long",
		"    - value that wraps",
	), got)
}

func TestPrinter_PrintTokenDiff_Wrapping(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		before   string
		after    string
		want     string
		overlays []position.Range
		width    int
	}{
		// Line 1 is the inserted line. The overlay covers "dddd", which
		// wraps to the third row.
		"overlay on a continuation row": {
			before:   "key: x\n",
			after:    "key: aaaa bbbb cccc dddd\n",
			overlays: []position.Range{position.NewRange(position.New(1, 20), position.New(1, 24))},
			width:    13,
			want: stringtest.JoinLF(
				"-key: x",
				"+key: aaaa",
				" bbbb cccc",
				" [dddd]",
			),
		},
		// Each ESC byte prints as a control picture one column wide.
		"escape sequence": {
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
		"tab run": {
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

			p := testPrinterWithGutter(printer.DiffGutter).With(printer.WithWrap(tc.width))

			view := diff.Diff(
				niceyaml.NewSourceFromString(tc.before).Lines(),
				niceyaml.NewSourceFromString(tc.after).Lines(),
			).Unified()
			view.AddOverlay(testOverlayHighlight, tc.overlays...)

			got := p.Print(view)

			for row := range strings.SplitSeq(got, "\n") {
				assert.LessOrEqual(t, lipgloss.Width(row), tc.width, row)
			}

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPrinter_PrintTokenDiff_WithLineNumbers(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		before string
		after  string
		want   string
	}{
		"modification shows correct line numbers": {
			// Delete shows beforeLine (2), insert shows afterLine (2).
			before: stringtest.JoinLF(
				"key: value",
				"old: line",
				"",
			),
			after: stringtest.JoinLF(
				"key: value",
				"new: line",
				"",
			),
			want: stringtest.JoinLF(
				"   1  key: value",
				"   2 -old: line",
				"   2 +new: line",
			),
		},
		"addition shows afterLine numbers": {
			// Equal lines show afterLine, inserted line shows afterLine.
			before: stringtest.JoinLF(
				"a: 1",
				"c: 3",
				"",
			),
			after: stringtest.JoinLF(
				"a: 1",
				"b: 2",
				"c: 3",
				"",
			),
			want: stringtest.JoinLF(
				"   1  a: 1",
				"   2 +b: 2",
				"   3  c: 3",
			),
		},
		"deletion shows beforeLine numbers": {
			// Equal lines show afterLine, deleted line shows beforeLine.
			before: stringtest.JoinLF(
				"a: 1",
				"b: 2",
				"c: 3",
				"",
			),
			after: stringtest.JoinLF(
				"a: 1",
				"c: 3",
				"",
			),
			want: stringtest.JoinLF(
				"   1  a: 1",
				"   2 -b: 2",
				"   2  c: 3",
			),
		},
		"multiple changes track line numbers correctly": {
			before: stringtest.JoinLF(
				"line1: a",
				"line2: b",
				"line3: c",
				"",
			),
			after: stringtest.JoinLF(
				"line1: x",
				"line2: b",
				"line3: y",
				"",
			),
			want: stringtest.JoinLF(
				"   1 -line1: a",
				"   1 +line1: x",
				"   2  line2: b",
				"   3 -line3: c",
				"   3 +line3: y",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := printer.New(
				printer.WithStyles(style.Styles{}),
				printer.WithContainerStyle(lipgloss.NewStyle()),
			)

			got := printDiff(p, tc.before, tc.after)

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPrinter_PrintTokenDiff_CustomGutter(t *testing.T) {
	t.Parallel()

	// The helper creates a gutter with custom prefixes that declares the
	// width of the widest of them, so the printer pads the narrower ones.
	makeGutter := func(inserted, deleted, equal string) printer.Gutter {
		return prefixGutter{inserted: inserted, deleted: deleted, equal: equal}
	}

	tcs := map[string]struct {
		gutterFunc printer.Gutter
		before     string
		after      string
		want       string
	}{
		"custom inserted prefix": {
			gutterFunc: makeGutter(">>", "-", " "),
			before:     "key: old\n",
			after: stringtest.JoinLF(
				"key: old",
				"new: line",
				"",
			),
			want: stringtest.JoinLF(
				"  key: old",
				">>new: line",
			),
		},
		"custom deleted prefix": {
			gutterFunc: makeGutter("+", "<<", " "),
			before: stringtest.JoinLF(
				"key: old",
				"old: line",
				"",
			),
			after: "key: old\n",
			want: stringtest.JoinLF(
				"  key: old",
				"<<old: line",
			),
		},
		"both custom prefixes": {
			gutterFunc: makeGutter("ADD:", "DEL:", "    "),
			before:     "key: old\n",
			after:      "key: new\n",
			want: stringtest.JoinLF(
				"DEL:key: old",
				"ADD:key: new",
			),
		},
		"no gutter": {
			gutterFunc: printer.NoGutter,
			before:     "a: 1\n",
			after:      "a: 2\n",
			want: stringtest.JoinLF(
				"a: 1",
				"a: 2",
			),
		},
		"multi-character prefixes with context": {
			gutterFunc: makeGutter("[+]", "[-]", "   "),
			before: stringtest.JoinLF(
				"line1: a",
				"line2: b",
				"line3: c",
				"",
			),
			after: stringtest.JoinLF(
				"line1: a",
				"line2: x",
				"line3: c",
				"",
			),
			want: stringtest.JoinLF(
				"   line1: a",
				"[-]line2: b",
				"[+]line2: x",
				"   line3: c",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := testPrinterWithGutter(tc.gutterFunc)

			got := printDiff(p, tc.before, tc.after)

			assert.Equal(t, tc.want, got)
		})
	}
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

	view := niceyaml.NewSourceFromString("a: 1\nb: 2\n").View()
	view.SetFlag(1, line.FlagInserted)

	t.Run("pads a row that renders less than the width", func(t *testing.T) {
		t.Parallel()

		p := testPrinterWithGutter(prefixGutter{inserted: "+ "})

		assert.Equal(t, "  a: 1\n+ b: 2", p.Print(view))
		assert.Equal(t, 2, p.Layout(view).GutterWidth())
	})

	t.Run("cuts a row that renders more than the width", func(t *testing.T) {
		t.Parallel()

		p := testPrinterWithGutter(fixedGutter{width: 1, text: "abc"})

		assert.Equal(t, "aa: 1\nab: 2", p.Print(view))
	})

	t.Run("pads a row whose cut drops a wide rune", func(t *testing.T) {
		t.Parallel()

		// The two-cell rune does not fit in one cell, so the cut leaves
		// nothing and the printer pads the row back to the width.
		p := testPrinterWithGutter(fixedGutter{width: 1, text: "日"})

		assert.Equal(t, " a: 1\n b: 2", p.Print(view))
		assert.Equal(t, 1, p.Layout(view).GutterWidth())
	})

	t.Run("a negative width counts as none", func(t *testing.T) {
		t.Parallel()

		p := testPrinterWithGutter(fixedGutter{width: -3, text: "abc"})

		assert.Equal(t, "a: 1\nb: 2", p.Print(view))
		assert.Equal(t, 0, p.Layout(view).GutterWidth())
	})

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
		assert.Equal(t, "#a: 1\n+b: 2", p.Print(view))
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

	tcs := map[string]struct {
		gutterFunc printer.Gutter
		want       string
		ctx        printer.GutterContext
	}{
		// A zero context renders with the default styles.
		"zero context/default": {
			gutterFunc: printer.DefaultGutter,
			ctx:        printer.GutterContext{},
			want:       "     " + " ",
		},
		"zero context/diff": {
			gutterFunc: printer.DiffGutter,
			ctx:        printer.GutterContext{},
			want:       " ",
		},
		"zero context/line number": {
			gutterFunc: printer.LineNumberGutter,
			ctx:        printer.GutterContext{},
			want:       "     ",
		},
		// DiffGutter tests.
		"diff/default flag": {
			gutterFunc: printer.DiffGutter,
			ctx:        printer.GutterContext{Flag: line.FlagDefault, Styles: styles},
			want:       " ",
		},
		"diff/inserted flag": {
			gutterFunc: printer.DiffGutter,
			ctx:        printer.GutterContext{Flag: line.FlagInserted, Styles: styles},
			want:       "+",
		},
		"diff/deleted flag": {
			gutterFunc: printer.DiffGutter,
			ctx:        printer.GutterContext{Flag: line.FlagDeleted, Styles: styles},
			want:       "-",
		},
		"diff/soft wrap default": {
			gutterFunc: printer.DiffGutter,
			ctx:        printer.GutterContext{Flag: line.FlagDefault, Soft: true, Styles: styles},
			want:       " ",
		},
		"diff/soft wrap inserted": {
			gutterFunc: printer.DiffGutter,
			ctx:        printer.GutterContext{Flag: line.FlagInserted, Soft: true, Styles: styles},
			want:       " ",
		},
		"diff/soft wrap deleted": {
			gutterFunc: printer.DiffGutter,
			ctx:        printer.GutterContext{Flag: line.FlagDeleted, Soft: true, Styles: styles},
			want:       " ",
		},
		// DefaultGutter tests.
		"default/annotation row renders empty": {
			gutterFunc: printer.DefaultGutter,
			ctx:        printer.GutterContext{Flag: line.FlagInserted, Annotation: true, Number: 1, Styles: styles},
			want:       "      ",
		},
		"default/soft wrap renders continuation marker": {
			gutterFunc: printer.DefaultGutter,
			ctx:        printer.GutterContext{Flag: line.FlagDefault, Soft: true, Styles: styles},
			want:       "   -  ",
		},
		"default/normal line renders line number": {
			gutterFunc: printer.DefaultGutter,
			ctx:        printer.GutterContext{Flag: line.FlagDefault, Number: 1, Styles: styles},
			want:       "   1  ",
		},
		"default/inserted flag renders + marker": {
			gutterFunc: printer.DefaultGutter,
			ctx:        printer.GutterContext{Flag: line.FlagInserted, Number: 1, Styles: styles},
			want:       "   1 +",
		},
		"default/deleted flag renders - marker": {
			gutterFunc: printer.DefaultGutter,
			ctx:        printer.GutterContext{Flag: line.FlagDeleted, Number: 1, Styles: styles},
			want:       "   1 -",
		},
		// LineNumberGutter tests.
		"lineNumber/annotation row renders empty": {
			gutterFunc: printer.LineNumberGutter,
			ctx:        printer.GutterContext{Flag: line.FlagDeleted, Annotation: true, Number: 1, Styles: styles},
			want:       "     ",
		},
		"lineNumber/soft wrap renders continuation marker": {
			gutterFunc: printer.LineNumberGutter,
			ctx:        printer.GutterContext{Flag: line.FlagDefault, Soft: true, Styles: styles},
			want:       "   - ",
		},
		"lineNumber/normal line renders line number": {
			gutterFunc: printer.LineNumberGutter,
			ctx:        printer.GutterContext{Flag: line.FlagDefault, Number: 1, Styles: styles},
			want:       "   1 ",
		},
		"lineNumber/max number widens the column": {
			gutterFunc: printer.LineNumberGutter,
			ctx:        printer.GutterContext{Flag: line.FlagDefault, Number: 1, MaxNumber: 15001, Styles: styles},
			want:       "    1 ",
		},
		"lineNumber/max number widens the soft wrap marker": {
			gutterFunc: printer.LineNumberGutter,
			ctx:        printer.GutterContext{Flag: line.FlagDefault, Soft: true, MaxNumber: 15001, Styles: styles},
			want:       "    - ",
		},
		"default/max number widens the column": {
			gutterFunc: printer.DefaultGutter,
			ctx:        printer.GutterContext{Flag: line.FlagInserted, Number: 15001, MaxNumber: 15001, Styles: styles},
			want:       "15001 +",
		},
		"default/zero number renders a blank column": {
			gutterFunc: printer.DefaultGutter,
			ctx:        printer.GutterContext{Flag: line.FlagDefault, Number: 0, MaxNumber: 3, Styles: styles},
			want:       "      ",
		},
		"lineNumber/zero number renders a blank column": {
			gutterFunc: printer.LineNumberGutter,
			ctx:        printer.GutterContext{Flag: line.FlagDefault, Number: 0, MaxNumber: 15001, Styles: styles},
			want:       "      ",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.gutterFunc.Render(tc.ctx)

			// The default styles of a zero context carry colors, which the
			// text under test does not.
			assert.Equal(t, tc.want, ansi.Strip(got))
		})
	}
}

func TestDiffGutter_Styles(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		want string
		ctx  printer.GutterContext
	}{
		"default": {
			ctx:  printer.GutterContext{Flag: line.FlagDefault},
			want: "<text> </text>",
		},
		"inserted": {
			ctx:  printer.GutterContext{Flag: line.FlagInserted},
			want: "<genericInserted>+</genericInserted>",
		},
		"deleted": {
			ctx:  printer.GutterContext{Flag: line.FlagDeleted},
			want: "<genericDeleted>-</genericDeleted>",
		},
		"soft wrap default": {
			ctx:  printer.GutterContext{Flag: line.FlagDefault, Soft: true},
			want: "<text> </text>",
		},
		"soft wrap inserted keeps the flag style": {
			ctx:  printer.GutterContext{Flag: line.FlagInserted, Soft: true},
			want: "<genericInserted> </genericInserted>",
		},
		"soft wrap deleted keeps the flag style": {
			ctx:  printer.GutterContext{Flag: line.FlagDeleted, Soft: true},
			want: "<genericDeleted> </genericDeleted>",
		},
		"annotation default": {
			ctx:  printer.GutterContext{Flag: line.FlagDefault, Annotation: true},
			want: "<text> </text>",
		},
		"annotation inserted renders as text": {
			ctx:  printer.GutterContext{Flag: line.FlagInserted, Annotation: true},
			want: "<text> </text>",
		},
		"annotation deleted renders as text": {
			ctx:  printer.GutterContext{Flag: line.FlagDeleted, Annotation: true},
			want: "<text> </text>",
		},
		"soft wrap annotation inserted renders as text": {
			ctx:  printer.GutterContext{Flag: line.FlagInserted, Soft: true, Annotation: true},
			want: "<text> </text>",
		},
		"soft wrap annotation deleted renders as text": {
			ctx:  printer.GutterContext{Flag: line.FlagDeleted, Soft: true, Annotation: true},
			want: "<text> </text>",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tc.ctx.Styles = yamltest.NewXMLStyles()

			assert.Equal(t, tc.want, printer.DiffGutter.Render(tc.ctx))
		})
	}
}

func TestPrinter_NoAnnotation(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		annotation string
		want       string
		fn         printer.AnnotationFunc
	}{
		"NoAnnotation hides them": {
			fn:         printer.NoAnnotation,
			annotation: "test annotation",
			want:       "key: value",
		},
		"DefaultAnnotation shows them": {
			fn:         printer.DefaultAnnotation,
			annotation: "@@ -1 +1 @@",
			want:       "@@ -1 +1 @@\nkey: value",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := niceyaml.NewSourceFromString("key: value\n").View()
			view.Annotate(0, line.Annotation{Content: tc.annotation})

			p := testPrinter().With(printer.WithAnnotation(tc.fn))

			got := p.Print(view)

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPrinter_AnnotationPosition(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input      string
		annotation line.Annotation
		lineIndex  int
		want       string
	}{
		"above annotation at col 0": {
			input:     "key: value",
			lineIndex: 0,
			annotation: line.Annotation{
				Content:   "# comment",
				Placement: line.Above,
				Col:       0,
			},
			want: "# comment\nkey: value",
		},
		"above annotation with col padding": {
			input:     "key: value",
			lineIndex: 0,
			annotation: line.Annotation{
				Content:   "^-- error here",
				Placement: line.Above,
				Col:       5,
			},
			want: "     ^-- error here\nkey: value",
		},
		"below annotation at col 0": {
			input:     "key: value",
			lineIndex: 0,
			annotation: line.Annotation{
				Content:   "comment below",
				Placement: line.Below,
				Col:       0,
			},
			want: "key: value\n^ comment below",
		},
		"below annotation with col padding": {
			input:     "key: value",
			lineIndex: 0,
			annotation: line.Annotation{
				Content:   "error here",
				Placement: line.Below,
				Col:       5,
			},
			want: "key: value\n     ^ error here",
		},
		"below annotation under wide runes": {
			input:     "日本語: 値",
			lineIndex: 0,
			annotation: line.Annotation{
				Content:   "bad value",
				Placement: line.Below,
				Col:       5,
			},
			// The three wide runes take six cells, so the marker sits eight
			// cells in, under the value.
			want: "日本語: 値\n        ^ bad value",
		},
		"below annotation past the end of wide runes": {
			input:     "日本",
			lineIndex: 0,
			annotation: line.Annotation{
				Content:   "after",
				Placement: line.Below,
				Col:       4,
			},
			want: "日本\n      ^ after",
		},
		"below annotation on second line": {
			input: stringtest.JoinLF(
				"first: 1",
				"second: 2",
			),
			lineIndex: 1,
			annotation: line.Annotation{
				Content:   "note",
				Placement: line.Below,
				Col:       8,
			},
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
			lineIndex: 1,
			annotation: line.Annotation{
				Content:   "@@ -1 +1 @@",
				Placement: line.Above,
				Col:       0,
			},
			want: stringtest.JoinLF(
				"first: 1",
				"@@ -1 +1 @@",
				"second: 2",
			),
		},
		"below annotation with empty content marks its column": {
			input:     "key: value",
			lineIndex: 0,
			annotation: line.Annotation{
				Placement: line.Below,
				Col:       2,
			},
			want: stringtest.JoinLF(
				"key: value",
				"  ^",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := niceyaml.NewSourceFromString(tc.input).View()
			view.Annotate(tc.lineIndex, tc.annotation)

			p := testPrinter()
			got := p.Print(view)

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPrinter_AnnotationFarColumn(t *testing.T) {
	t.Parallel()

	// A column more than 1024 columns past the end of the content starts
	// 1024 columns past it, after every cell of the content.
	tcs := map[string]struct {
		input string
		want  string
		col   int
		width int
	}{
		"max column": {
			input: "k: v",
			col:   math.MaxInt,
			want:  strings.Repeat(" ", 4+1024) + "^ x",
		},
		"max column after wide runes": {
			input: "k: 日本語",
			col:   math.MaxInt,
			want:  strings.Repeat(" ", 9+1024) + "^ x",
		},
		"max column when wrapping": {
			// The column leaves the text no room within the width, so the
			// text moves below the marker.
			input: "k: v",
			col:   math.MaxInt,
			width: 10,
			want:  stringtest.JoinLF(strings.Repeat(" ", 4+1024)+"^", "x"),
		},
		"column past the bound": {
			input: "k: v",
			col:   1 << 32,
			want:  strings.Repeat(" ", 4+1024) + "^ x",
		},
		"column at the bound": {
			input: "k: v",
			col:   4 + 1024,
			want:  strings.Repeat(" ", 4+1024) + "^ x",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := niceyaml.NewSourceFromString(tc.input).View()
			view.Annotate(0, line.Annotation{Content: "x", Placement: line.Below, Col: tc.col})

			p := testPrinter().With(printer.WithWrap(tc.width))

			var got string

			require.NotPanics(t, func() { got = p.Print(view) })
			assert.Equal(t, stringtest.JoinLF(tc.input, tc.want), got)

			var l printer.Layout

			require.NotPanics(t, func() { l = p.Layout(view) })

			rows := strings.Split(got, "\n")
			widest := 0

			for _, row := range rows {
				widest = max(widest, lipgloss.Width(row))
			}

			assert.Len(t, rows, l.Rows())
			assert.Equal(t, widest, l.Width())
		})
	}
}

func TestPrinter_AnnotationPosition_WithGutter(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input      string
		annotation line.Annotation
		lineIndex  int
		want       string
	}{
		"above annotation with line numbers": {
			input:     "key: value",
			lineIndex: 0,
			annotation: line.Annotation{
				Content:   "@@ -1 +1 @@",
				Placement: line.Above,
				Col:       0,
			},
			want: "     @@ -1 +1 @@\n   1 key: value",
		},
		"below annotation with line numbers": {
			input:     "key: value",
			lineIndex: 0,
			annotation: line.Annotation{
				Content:   "error",
				Placement: line.Below,
				Col:       5,
			},
			want: "   1 key: value\n          ^ error",
		},
		"below annotation with col padding and line numbers": {
			input: stringtest.JoinLF(
				"first: 1",
				"second: 2",
			),
			lineIndex: 0,
			annotation: line.Annotation{
				Content:   "note",
				Placement: line.Below,
				Col:       7,
			},
			want: stringtest.JoinLF(
				"   1 first: 1",
				"            ^ note",
				"   2 second: 2",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := niceyaml.NewSourceFromString(tc.input).View()
			view.Annotate(tc.lineIndex, tc.annotation)

			p := testPrinterWithGutter(printer.LineNumberGutter)
			got := p.Print(view)

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPrinter_AnnotationPosition_Disabled(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		annotation line.Annotation
	}{
		"above annotation disabled": {
			annotation: line.Annotation{
				Content:   "# hidden above",
				Placement: line.Above,
				Col:       0,
			},
		},
		"below annotation disabled": {
			annotation: line.Annotation{
				Content:   "# hidden below",
				Placement: line.Below,
				Col:       5,
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := niceyaml.NewSourceFromString("key: value").View()
			view.Annotate(0, tc.annotation)

			p := testPrinter().With(printer.WithAnnotation(printer.NoAnnotation))

			got := p.Print(view)

			// With annotations disabled, the printer renders only the content.
			assert.Equal(t, "key: value", got)
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

			p := printer.New(printer.WithStyles(tc.styles))
			got := p.Style(tc.query)

			require.NotNil(t, got)
			assert.Equal(t, tc.wantBold, got.GetBold())
			assert.Equal(t, tc.wantItalic, got.GetItalic())
		})
	}
}

func TestPrinter_TokenTypes_XMLStyler(t *testing.T) {
	t.Parallel()

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
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := lexer.Tokenize(tc.input)
			p := printer.New(
				printer.WithStyles(yamltest.NewXMLStyles()),
				printer.WithContainerStyle(lipgloss.NewStyle()),
				printer.WithGutter(printer.NoGutter),
			)

			got := p.Print(niceyaml.NewSourceFromTokens(tks).View())
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPrinter_PrintFile_MultiDocument(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  string
	}{
		"two documents": {
			input: stringtest.JoinLF(
				"doc1: value1",
				"---",
				"doc2: value2",
			),
			want: stringtest.JoinLF(
				"doc1: value1",
				"---",
				"doc2: value2",
			),
		},
		"three documents": {
			input: stringtest.JoinLF(
				"first: 1",
				"---",
				"second: 2",
				"---",
				"third: 3",
			),
			want: stringtest.JoinLF(
				"first: 1",
				"---",
				"second: 2",
				"---",
				"third: 3",
			),
		},
		"documents with header": {
			input: stringtest.JoinLF(
				"---",
				"key: value",
			),
			want: stringtest.JoinLF(
				"---",
				"key: value",
			),
		},
		"documents with footer": {
			input: stringtest.JoinLF(
				"key: value",
				"...",
			),
			want: stringtest.JoinLF(
				"key: value",
				"...",
			),
		},
		"document with header and footer": {
			input: stringtest.JoinLF(
				"---",
				"key: value",
				"...",
			),
			want: stringtest.JoinLF(
				"---",
				"key: value",
				"...",
			),
		},
		"multiple docs with headers and footers": {
			input: stringtest.JoinLF(
				"---",
				"doc1: value1",
				"...",
				"---",
				"doc2: value2",
				"...",
			),
			want: stringtest.JoinLF(
				"---",
				"doc1: value1",
				"...",
				"---",
				"doc2: value2",
				"...",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := lexer.Tokenize(tc.input)
			p := testPrinter()

			got := p.Print(niceyaml.NewSourceFromTokens(tks).View())
			assert.Equal(t, tc.want, got)
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
			before: stringtest.JoinLF(
				"a: 1",
				"b: 2",
				"c: 3",
				"",
			),
			after: stringtest.JoinLF(
				"a: 1",
				"b: changed",
				"c: 3",
				"",
			),
			context: 0,
			want: stringtest.JoinLF(
				"      @@ -2 +2 @@",
				"   2 -b: 2",
				"   2 +b: changed",
			),
		},
		"simple change with context 1": {
			before: stringtest.JoinLF(
				"a: 1",
				"b: 2",
				"c: 3",
				"d: 4",
				"",
			),
			after: stringtest.JoinLF(
				"a: 1",
				"b: changed",
				"c: 3",
				"d: 4",
				"",
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
			before: stringtest.JoinLF(
				"a: 1",
				"b: 2",
				"c: 3",
				"d: 4",
				"e: 5",
				"",
			),
			after: stringtest.JoinLF(
				"a: X",
				"b: 2",
				"c: 3",
				"d: 4",
				"e: Y",
				"",
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
			before: stringtest.JoinLF(
				"line1: a",
				"line2: b",
				"line3: c",
				"line4: d",
				"line5: e",
				"line6: f",
				"",
			),
			after: stringtest.JoinLF(
				"line1: X",
				"line2: b",
				"line3: c",
				"line4: d",
				"line5: e",
				"line6: Y",
				"",
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
			before: stringtest.JoinLF(
				"a: 1",
				"c: 3",
				"",
			),
			after: stringtest.JoinLF(
				"a: 1",
				"b: 2",
				"c: 3",
				"",
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
			before: stringtest.JoinLF(
				"a: 1",
				"b: 2",
				"c: 3",
				"",
			),
			after: stringtest.JoinLF(
				"a: 1",
				"c: 3",
				"",
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
			before: stringtest.JoinLF(
				"a: 1",
				"b: 2",
				"c: 3",
				"",
			),
			after: stringtest.JoinLF(
				"a: 1",
				"b: changed",
				"c: 3",
				"",
			),
			context: 100, // Much larger than 3 lines.
			want: stringtest.JoinLF(
				"      @@ -1,3 +1,3 @@",
				"   1  a: 1",
				"   2 -b: 2",
				"   2 +b: changed",
				"   3  c: 3",
			),
		},
		"line numbers with hunk alignment": {
			before: stringtest.JoinLF(
				"a: 1",
				"b: 2",
				"c: 3",
				"d: 4",
				"e: 5",
				"",
			),
			after: stringtest.JoinLF(
				"a: 1",
				"b: changed",
				"c: 3",
				"d: 4",
				"e: 5",
				"",
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
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := printer.New(
				printer.WithStyles(style.Styles{}),
				printer.WithContainerStyle(lipgloss.NewStyle()),
			)

			got := printDiffSummary(p, tc.before, tc.after, tc.context)

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestFinderPrinter_Integration(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input        string
		search       string
		normalizer   finder.Normalizer
		want         string
		wantNoRanges bool // If true, assert no ranges found.
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
			input:  "a: test\nb: test",
			search: "test",
			want:   "a: [test]\nb: [test]",
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
			input:      "name: Thaïs",
			search:     "Thais",
			normalizer: normalizer.New(),
			want:       "name: [Thaïs]",
		},
		"utf8 - case insensitive with diacritics": {
			input:      "name: THAÏS test",
			search:     "thais",
			normalizer: normalizer.New(),
			want:       "name: [THAÏS] test",
		},
		"utf8 - search ascii finds normalized diacritic": {
			input:      "key: über",
			search:     "u",
			normalizer: normalizer.New(),
			want:       "key: [ü]ber",
		},
		"utf8 - normalizer search after multiple multibyte": {
			input:      "key: über Yamüll test",
			search:     "ya",
			normalizer: normalizer.New(),
			want:       "key: über [Ya]müll test",
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
			input:  "metadata:\n  name: Thaïs\n  namespace: über",
			search: "name",
			want:   "metadata:\n  [name]: Thaïs\n  [name]space: über",
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
			input:  "a: test\n- 寿司: 日本酒",
			search: "日本",
			want:   "a: test\n- 寿司: [日本]酒",
		},
		"utf8 - multiple japanese on different lines": {
			input:  "a: 日本\nb: 日本酒",
			search: "日本",
			want:   "a: [日本]\nb: [日本]酒",
		},
		"no match": {
			input:        "key: value",
			search:       "notfound",
			want:         "key: value",
			wantNoRanges: true,
		},
		"empty search": {
			input:        "key: value",
			search:       "",
			want:         "key: value",
			wantNoRanges: true,
		},
		"empty file": {
			input:        "",
			search:       "test",
			want:         "",
			wantNoRanges: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)
			idx := testFinder(tc.normalizer).Load(source.Lines())

			p := testPrinter()

			ranges := idx.Find(tc.search)

			if tc.wantNoRanges {
				assert.Empty(t, ranges)
			}

			view := source.View()
			view.AddOverlay(testOverlayHighlight, ranges...)

			got := p.Print(view)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPrinter_With(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("key: this is a very long value that should wrap")

	base := testPrinterWithGutter(printer.NoGutter)
	narrow := base.With(printer.WithWrap(20))

	assert.Equal(t, 0, base.Wrap())
	assert.Equal(t, 20, narrow.Wrap())

	// A negative width disables wrapping, and Wrap reports that as 0.
	assert.Equal(t, 0, base.With(printer.WithWrap(-5)).Wrap())

	// The receiver still renders on one line; the copy wraps.
	assert.Equal(t, "key: this is a very long value that should wrap", base.Print(source.View()))
	assert.Equal(t, stringtest.JoinLF(
		"key: this is a very",
		"long value that",
		"should wrap",
	), narrow.Print(source.View()))

	// A copy can switch styles and gets its own container style.
	styled := base.With(printer.WithStyles(yamltest.NewXMLStyles()))
	assert.Contains(t, styled.Print(source.View()), "<nameTag>key</nameTag>")
	assert.NotContains(t, base.Print(source.View()), "<nameTag>")
}

func TestPrinter_Golden(t *testing.T) {
	t.Parallel()

	type goldenTest struct {
		setupFunc func(*line.View)
		opts      []printer.Option
	}

	tcs := map[string]goldenTest{
		"default colors": {
			opts: []printer.Option{
				printer.WithStyles(theme.Charm.Styles()),
				printer.WithContainerStyle(lipgloss.NewStyle()),
				printer.WithGutter(printer.NoGutter),
			},
		},
		"word wrap with colors": {
			opts: []printer.Option{
				printer.WithStyles(theme.Charm.Styles()),
				printer.WithContainerStyle(lipgloss.NewStyle()),
				printer.WithWrap(40),
			},
		},
		"default colors with line numbers": {
			opts: []printer.Option{
				printer.WithStyles(theme.Charm.Styles()),
				printer.WithContainerStyle(lipgloss.NewStyle()),
			},
		},
		"no colors": {
			opts: []printer.Option{
				printer.WithStyles(style.Styles{}),
				printer.WithContainerStyle(lipgloss.NewStyle()),
				printer.WithGutter(printer.NoGutter),
			},
		},
		"no colors with line numbers": {
			opts: []printer.Option{
				printer.WithStyles(style.Styles{}),
				printer.WithContainerStyle(lipgloss.NewStyle()),
			},
		},
		"find and highlight": {
			opts: []printer.Option{
				printer.WithStyles(theme.Charm.Styles().With(
					style.Set(testOverlayHighlight, lipgloss.NewStyle().
						Background(lipgloss.Color("#FFFF00")).
						Foreground(lipgloss.Color("#000000"))),
				)),
				printer.WithContainerStyle(lipgloss.NewStyle()),
				printer.WithGutter(printer.NoGutter),
			},
			setupFunc: func(view *line.View) {
				// Search for "日本" (Japan) which appears multiple times in full.yaml.
				ranges := finder.New().Load(view.Lines()).Find("日本")
				view.AddOverlay(testOverlayHighlight, ranges...)
			},
		},
	}

	input, err := os.ReadFile("../testdata/full.yaml")
	require.NoError(t, err)

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := niceyaml.NewSourceFromString(string(input)).View()
			p := printer.New(tc.opts...)

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

	// A style from styleWithTag wraps content in XML-like tags.
	styleWithTag := func(tag string) lipgloss.Style {
		return lipgloss.NewStyle().Transform(func(s string) string {
			return "<" + tag + ">" + s + "</" + tag + ">"
		})
	}

	// The overlayRange type holds an overlay kind and its range.
	type overlayRange struct {
		kind  kind.Kind
		start position.Position
		end   position.Position
	}

	// Overlay kinds for the various test tags.
	const (
		kindHL   kind.Kind = "kindHL"
		kindAll  kind.Kind = "kindAll"
		kindA    kind.Kind = "kindA"
		kindB    kind.Kind = "kindB"
		kindC    kind.Kind = "kindC"
		kindX    kind.Kind = "kindX"
		kindY    kind.Kind = "kindY"
		kindK    kind.Kind = "kindK"
		kindVal  kind.Kind = "kindVal"
		kindSpan kind.Kind = "kindSpan"
	)

	// Overlay styler mapping kinds to tag-wrapped styles.
	testOverlayStyler := style.New(
		lipgloss.NewStyle(),
		style.Set(kindHL, styleWithTag("hl")),
		style.Set(kindAll, styleWithTag("all")),
		style.Set(kindA, styleWithTag("a")),
		style.Set(kindB, styleWithTag("b")),
		style.Set(kindC, styleWithTag("c")),
		style.Set(kindX, styleWithTag("x")),
		style.Set(kindY, styleWithTag("y")),
		style.Set(kindK, styleWithTag("k")),
		style.Set(kindVal, styleWithTag("val")),
		style.Set(kindSpan, styleWithTag("span")),
	)

	tcs := map[string]struct {
		input  string
		want   string
		ranges []overlayRange
	}{
		"single range on value": {
			input: "key: value",
			ranges: []overlayRange{
				{kindHL, position.New(0, 5), position.New(0, 10)},
			},
			want: "key: <hl>value</hl>",
		},
		"single range on key": {
			input: "key: value",
			ranges: []overlayRange{
				{kindHL, position.New(0, 0), position.New(0, 3)},
			},
			want: "<hl>key</hl>: value",
		},
		"full line range": {
			// Each token renders on its own, so transforms apply per-token.
			input: "key: value",
			ranges: []overlayRange{
				{kindAll, position.New(0, 0), position.New(0, 10)},
			},
			want: "<all>key</all><all>:</all><all> </all><all>value</all>",
		},
		"non-overlapping ranges": {
			input: "key: value",
			ranges: []overlayRange{
				{kindA, position.New(0, 0), position.New(0, 3)},
				{kindB, position.New(0, 5), position.New(0, 10)},
			},
			want: "<a>key</a>: <b>value</b>",
		},
		"adjacent ranges": {
			input: "key: value",
			ranges: []overlayRange{
				{kindA, position.New(0, 0), position.New(0, 3)},
				{kindB, position.New(0, 3), position.New(0, 4)},
			},
			want: "<a>key</a><b>:</b> value",
		},
		"overlapping ranges - transforms compose": {
			// First range [0,5) gets override, second range [2,7) blends.
			// Blending composes transforms: overlay(base(text)).
			// Each token renders on its own.
			input: "key: value",
			ranges: []overlayRange{
				{kindA, position.New(0, 0), position.New(0, 5)},
				{kindB, position.New(0, 2), position.New(0, 7)},
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
				{kindA, position.New(0, 0), position.New(0, 6)},
				{kindB, position.New(0, 2), position.New(0, 8)},
				{kindC, position.New(0, 4), position.New(0, 10)},
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
				{kindX, position.New(0, 1), position.New(0, 4)},
				{kindY, position.New(0, 3), position.New(0, 5)},
			},
			// Position 0: no style
			// Positions 1-2: only <x>
			// Position 3: <y> wraps <x>
			// Position 4: only <y> (style changes, new span)
			// Position 5: no style.
			want: "a<x>bc</x><y><x>d</x></y><y>e</y>f",
		},
		"range covers entire token": {
			input: "key: value",
			ranges: []overlayRange{
				{kindK, position.New(0, 0), position.New(0, 3)},
				{kindVal, position.New(0, 5), position.New(0, 10)},
			},
			want: "<k>key</k>: <val>value</val>",
		},
		"multi-line with ranges on each line": {
			input: stringtest.JoinLF("a: 1", "b: 2"),
			ranges: []overlayRange{
				{kindX, position.New(0, 0), position.New(0, 1)},
				{kindY, position.New(1, 0), position.New(1, 1)},
			},
			want: stringtest.JoinLF("<x>a</x>: 1", "<y>b</y>: 2"),
		},
		"range spanning multiple lines": {
			input: stringtest.JoinLF("a: 1", "b: 2"),
			ranges: []overlayRange{
				{kindSpan, position.New(0, 3), position.New(1, 1)},
			},
			// Range spans from line 0 col 3 to line 1 col 1.
			want: stringtest.JoinLF("a: <span>1</span>", "<span>b</span>: 2"),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)
			p := printer.New(
				printer.WithStyles(testOverlayStyler),
				printer.WithContainerStyle(lipgloss.NewStyle()),
				printer.WithGutter(printer.NoGutter),
			)

			view := source.View()
			for _, or := range tc.ranges {
				view.BlendOverlay(or.kind, position.NewRange(or.start, or.end))
			}

			got := p.Print(view)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPrinter_ColorBlending_Golden(t *testing.T) {
	t.Parallel()

	// These tests exercise the color blending paths with actual lipgloss
	// colors instead of transforms.

	type overlayDef struct {
		style lipgloss.Style
		start position.Position
		end   position.Position
	}

	// Overlay kinds for color blending tests.
	const (
		colorKind1 kind.Kind = "colorKind1"
		colorKind2 kind.Kind = "colorKind2"
		colorKind3 kind.Kind = "colorKind3"
	)

	tcs := map[string]struct {
		input    string
		overlays []overlayDef
	}{
		"ForegroundBlend": {
			// Where the two ranges overlap, the printer blends the colors via LAB.
			input: "key: value",
			overlays: []overlayDef{
				{lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000")), position.New(0, 0), position.New(0, 5)},
				{lipgloss.NewStyle().Foreground(lipgloss.Color("#0000FF")), position.New(0, 3), position.New(0, 10)},
			},
		},
		"BackgroundBlend": {
			input: "key: value",
			overlays: []overlayDef{
				{lipgloss.NewStyle().Background(lipgloss.Color("#00FF00")), position.New(0, 0), position.New(0, 5)},
				{lipgloss.NewStyle().Background(lipgloss.Color("#FF00FF")), position.New(0, 3), position.New(0, 10)},
			},
		},
		"FirstColorOnly": {
			// The second style has NoColor, so the printer uses the first
			// color directly.
			input: "key: value",
			overlays: []overlayDef{
				{lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000")), position.New(0, 0), position.New(0, 5)},
				{lipgloss.NewStyle(), position.New(0, 3), position.New(0, 10)},
			},
		},
		"SecondColorOnly": {
			// The first style has NoColor, so the printer uses the second color.
			input: "key: value",
			overlays: []overlayDef{
				{lipgloss.NewStyle(), position.New(0, 0), position.New(0, 5)},
				{lipgloss.NewStyle().Foreground(lipgloss.Color("#0000FF")), position.New(0, 3), position.New(0, 10)},
			},
		},
		"BothNoColor": {
			// Both styles have NoColor, so the blend yields nil and applies no color.
			input: "key: value",
			overlays: []overlayDef{
				{lipgloss.NewStyle(), position.New(0, 0), position.New(0, 5)},
				{lipgloss.NewStyle(), position.New(0, 3), position.New(0, 10)},
			},
		},
		"ThreeOverlapping": {
			// Three ranges overlap, so the printer blends all three colors.
			input: "key: value",
			overlays: []overlayDef{
				{lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000")), position.New(0, 0), position.New(0, 6)},
				{lipgloss.NewStyle().Foreground(lipgloss.Color("#00FF00")), position.New(0, 2), position.New(0, 8)},
				{lipgloss.NewStyle().Foreground(lipgloss.Color("#0000FF")), position.New(0, 4), position.New(0, 10)},
			},
		},
		"MixedFgBg": {
			// The printer blends foreground and background colors independently.
			input: "key: value",
			overlays: []overlayDef{
				{
					lipgloss.NewStyle().
						Foreground(lipgloss.Color("#FF0000")).
						Background(lipgloss.Color("#00FF00")),
					position.New(0, 0),
					position.New(0, 6),
				},
				{
					lipgloss.NewStyle().
						Foreground(lipgloss.Color("#0000FF")).
						Background(lipgloss.Color("#FFFF00")),
					position.New(0, 3),
					position.New(0, 10),
				},
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := niceyaml.NewSourceFromString(tc.input).View()

			// Build overlay styler with styles from test case.
			kinds := []kind.Kind{colorKind1, colorKind2, colorKind3}

			overlayOpts := make([]style.Option, 0, len(tc.overlays))
			for i, od := range tc.overlays {
				overlayOpts = append(overlayOpts, style.Set(kinds[i], od.style))
				view.BlendOverlay(kinds[i], position.NewRange(od.start, od.end))
			}

			p := printer.New(
				printer.WithStyles(style.New(lipgloss.NewStyle(), overlayOpts...)),
				printer.WithContainerStyle(lipgloss.NewStyle()),
				printer.WithGutter(printer.NoGutter),
			)

			got := p.Print(view)
			golden.RequireEqual(t, got)
		})
	}
}

func TestDefaultAnnotation(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		content     string
		annotations line.Annotations
		overlays    line.Overlays
		rowStarts   []int
		rowEnds     []int
		position    line.Placement
		want        []printer.AnnotationRow
	}{
		"empty annotations": {
			annotations: line.Annotations{},
			position:    line.Below,
		},
		"empty content marks the overlays": {
			content:     "  sla: 99",
			annotations: line.Annotations{{Placement: line.Below, Col: 7}},
			overlays:    line.Overlays{{Cols: position.NewSpan(7, 9)}},
			position:    line.Below,
			want:        []printer.AnnotationRow{{Col: 7, Marker: "^^"}},
		},
		"empty content marks every overlay": {
			content:     "a: 1, b: 2",
			annotations: line.Annotations{{Placement: line.Below}},
			overlays: line.Overlays{
				{Cols: position.NewSpan(9, 10)},
				{Cols: position.NewSpan(3, 4)},
			},
			position: line.Below,
			want:     []printer.AnnotationRow{{Col: 3, Marker: "^     ^"}},
		},
		"empty content marks wide runes with two carets": {
			content:     "日本語: 値",
			annotations: line.Annotations{{Placement: line.Below}},
			overlays:    line.Overlays{{Cols: position.NewSpan(0, 3)}},
			position:    line.Below,
			want:        []printer.AnnotationRow{{Col: 0, Marker: "^^^^^^"}},
		},
		"empty content marks a column after a combining mark": {
			content:     "e\u0301x",
			annotations: line.Annotations{{Placement: line.Below}},
			overlays:    line.Overlays{{Cols: position.NewSpan(2, 3)}},
			position:    line.Below,
			want:        []printer.AnnotationRow{{Col: 2, Marker: "^"}},
		},
		"empty content marks a flag after a flag": {
			content:     "flags: 🇺🇸🇫🇷",
			annotations: line.Annotations{{Placement: line.Below}},
			overlays:    line.Overlays{{Cols: position.NewSpan(9, 11)}},
			position:    line.Below,
			want:        []printer.AnnotationRow{{Col: 9, Marker: "^^"}},
		},
		"empty content marks control characters as one cell": {
			content:     "a: \x07b",
			annotations: line.Annotations{{Placement: line.Below}},
			overlays:    line.Overlays{{Cols: position.NewSpan(3, 5)}},
			position:    line.Below,
			want:        []printer.AnnotationRow{{Col: 3, Marker: "^^"}},
		},
		"empty content clamps the overlays to the content": {
			content:     "a: 1",
			annotations: line.Annotations{{Placement: line.Below}},
			overlays:    line.Overlays{{Cols: position.NewSpan(3, 9)}},
			position:    line.Below,
			want:        []printer.AnnotationRow{{Col: 3, Marker: "^"}},
		},
		"empty content starts a negative overlay at column zero": {
			content:     "a: 1",
			annotations: line.Annotations{{Placement: line.Below}},
			overlays:    line.Overlays{{Cols: position.NewSpan(-2, 2)}},
			position:    line.Below,
			want:        []printer.AnnotationRow{{Col: 0, Marker: "^^"}},
		},
		"empty content with an overlay of no width marks its column": {
			content:     "a: 1",
			annotations: line.Annotations{{Placement: line.Below, Col: 4}},
			overlays:    line.Overlays{{Cols: position.NewSpan(4, 4)}},
			position:    line.Below,
			want:        []printer.AnnotationRow{{Col: 4, Marker: "^"}},
		},
		"empty content leaves blend overlays without carets": {
			content:     "name: other value",
			annotations: line.Annotations{{Placement: line.Below, Col: 6}},
			overlays: line.Overlays{
				{Cols: position.NewSpan(6, 17)},
				{Cols: position.NewSpan(0, 4), Blend: true},
			},
			position: line.Below,
			want:     []printer.AnnotationRow{{Col: 6, Marker: "^^^^^^^^^^^"}},
		},
		"empty content with an overlay of no width ignores a blend overlay": {
			content:     "b:",
			annotations: line.Annotations{{Placement: line.Below, Col: 2}},
			overlays: line.Overlays{
				{Cols: position.NewSpan(2, 2)},
				{Cols: position.NewSpan(0, 1), Blend: true},
			},
			position: line.Below,
			want:     []printer.AnnotationRow{{Col: 2, Marker: "^"}},
		},
		"empty content with an overlay of no width marks a wide rune": {
			content:     "a: 日本",
			annotations: line.Annotations{{Placement: line.Below, Col: 3}},
			overlays:    line.Overlays{{Cols: position.NewSpan(3, 3)}},
			position:    line.Below,
			want:        []printer.AnnotationRow{{Col: 3, Marker: "^^"}},
		},
		"empty content with an overlay past the content marks the column": {
			content:     "a: 1",
			annotations: line.Annotations{{Placement: line.Below, Col: 4}},
			overlays:    line.Overlays{{Cols: position.NewSpan(4, 6)}},
			position:    line.Below,
			want:        []printer.AnnotationRow{{Col: 4, Marker: "^"}},
		},
		"empty content with a negative column marks column zero": {
			content:     "a: 1",
			annotations: line.Annotations{{Placement: line.Below, Col: -3}},
			position:    line.Below,
			want:        []printer.AnnotationRow{{Col: 0, Marker: "^"}},
		},
		"empty content above the line renders nothing": {
			content:     "a: 1",
			annotations: line.Annotations{{Placement: line.Above}},
			overlays:    line.Overlays{{Cols: position.NewSpan(3, 4)}},
			position:    line.Above,
		},
		"content beside the overlays keeps its own caret": {
			content:     "  sla: 99",
			annotations: line.Annotations{{Content: "bad", Placement: line.Below, Col: 7}},
			overlays:    line.Overlays{{Cols: position.NewSpan(7, 9)}},
			position:    line.Below,
			want:        []printer.AnnotationRow{{Col: 7, Marker: "^ ", Text: "bad"}},
		},
		"single below annotation": {
			annotations: line.Annotations{{Content: "error here", Placement: line.Below, Col: 0}},
			position:    line.Below,
			want:        []printer.AnnotationRow{{Col: 0, Marker: "^ ", Text: "error here"}},
		},
		"single below annotation at a column": {
			annotations: line.Annotations{{Content: "error", Placement: line.Below, Col: 5}},
			position:    line.Below,
			want:        []printer.AnnotationRow{{Col: 5, Marker: "^ ", Text: "error"}},
		},
		"empty content is left out of the column": {
			annotations: line.Annotations{
				{Placement: line.Below, Col: 0},
				{Content: "boom", Placement: line.Below, Col: 5},
			},
			position: line.Below,
			want:     []printer.AnnotationRow{{Col: 5, Marker: "^ ", Text: "boom"}},
		},
		"single above annotation": {
			annotations: line.Annotations{{Content: "@@ hunk @@", Placement: line.Above, Col: 0}},
			position:    line.Above,
			want:        []printer.AnnotationRow{{Col: 0, Text: "@@ hunk @@"}},
		},
		"single above annotation at a column": {
			annotations: line.Annotations{{Content: "header", Placement: line.Above, Col: 3}},
			position:    line.Above,
			want:        []printer.AnnotationRow{{Col: 3, Text: "header"}},
		},
		"multiple below annotations": {
			annotations: line.Annotations{
				{Content: "first", Placement: line.Below, Col: 0},
				{Content: "second", Placement: line.Below, Col: 5},
			},
			position: line.Below,
			want:     []printer.AnnotationRow{{Col: 0, Marker: "^ ", Text: "first; second"}},
		},
		"multiple below annotations join in column order at min col": {
			annotations: line.Annotations{
				{Content: "first", Placement: line.Below, Col: 5},
				{Content: "second", Placement: line.Below, Col: 2},
			},
			position: line.Below,
			want:     []printer.AnnotationRow{{Col: 2, Marker: "^ ", Text: "second; first"}},
		},
		"multiple above annotations": {
			annotations: line.Annotations{
				{Content: "header1", Placement: line.Above, Col: 0},
				{Content: "header2", Placement: line.Above, Col: 0},
			},
			position: line.Above,
			want:     []printer.AnnotationRow{{Col: 0, Text: "header1; header2"}},
		},
		"empty content without overlays marks its column": {
			annotations: line.Annotations{{Placement: line.Below, Col: 2}},
			position:    line.Below,
			want:        []printer.AnnotationRow{{Col: 2, Marker: "^"}},
		},
		"empty content is left out of the join": {
			annotations: line.Annotations{
				{Content: "first", Placement: line.Below, Col: 2},
				{Placement: line.Below, Col: 2},
				{Content: "third", Placement: line.Below, Col: 2},
			},
			position: line.Below,
			want:     []printer.AnnotationRow{{Col: 2, Marker: "^ ", Text: "first; third"}},
		},
		"control characters render as pictures": {
			annotations: line.Annotations{{Content: "a\nb\x1b[31m", Placement: line.Below}},
			position:    line.Below,
			want:        []printer.AnnotationRow{{Col: 0, Marker: "^ ", Text: "a\u240ab\u241b[31m"}},
		},
		"wrapped content marks each row under its own columns": {
			content:     "key: aaaa bbbb cccc dddd",
			annotations: line.Annotations{{Placement: line.Below}},
			overlays: line.Overlays{
				{Cols: position.NewSpan(5, 9)},
				{Cols: position.NewSpan(17, 19)},
			},
			rowStarts: []int{0, 10, 20},
			position:  line.Below,
			want: []printer.AnnotationRow{
				{Col: 5, Marker: "^^^^"},
				{Col: 17, Marker: "^^"},
			},
		},
		"wrapped content leaves the space dropped at a break unmarked": {
			content:     "key: aaaa bbbb",
			annotations: line.Annotations{{Placement: line.Below}},
			overlays:    line.Overlays{{Cols: position.NewSpan(5, 14)}},
			rowStarts:   []int{0, 10},
			position:    line.Below,
			want: []printer.AnnotationRow{
				{Col: 5, Marker: "^^^^"},
				{Col: 10, Marker: "^^^^"},
			},
		},
		"wrapped content leaves the spaces dropped at the end unmarked": {
			content:     "key: aaaa bb  ",
			annotations: line.Annotations{{Placement: line.Below}},
			overlays:    line.Overlays{{Cols: position.NewSpan(5, 14)}},
			rowStarts:   []int{0, 10},
			rowEnds:     []int{9, 12},
			position:    line.Below,
			want: []printer.AnnotationRow{
				{Col: 5, Marker: "^^^^"},
				{Col: 10, Marker: "^^"},
			},
		},
		"wrapped content marks a tab at a break as its picture": {
			content:     "k: ab\tcd",
			annotations: line.Annotations{{Placement: line.Below}},
			overlays:    line.Overlays{{Cols: position.NewSpan(3, 8)}},
			rowStarts:   []int{0, 6},
			position:    line.Below,
			want: []printer.AnnotationRow{
				{Col: 3, Marker: "^^^"},
				{Col: 6, Marker: "^^"},
			},
		},
		"wrapped content marks wide runes on each row": {
			content:     "k: 日本語 日本語",
			annotations: line.Annotations{{Placement: line.Below}},
			overlays:    line.Overlays{{Cols: position.NewSpan(3, 10)}},
			rowStarts:   []int{0, 7},
			position:    line.Below,
			want: []printer.AnnotationRow{
				{Col: 3, Marker: "^^^^^^"},
				{Col: 7, Marker: "^^^^^^"},
			},
		},
		"wrapped content marks combining marks on each row": {
			content:     "k: e\u0301x e\u0301y",
			annotations: line.Annotations{{Placement: line.Below}},
			overlays:    line.Overlays{{Cols: position.NewSpan(4, 10)}},
			rowStarts:   []int{0, 7},
			position:    line.Below,
			want: []printer.AnnotationRow{
				{Col: 3, Marker: "^^"},
				{Col: 7, Marker: "^^"},
			},
		},
		"wrapped content leaves out a row without covered columns": {
			content:     "key: aaaa bbbb cccc dddd",
			annotations: line.Annotations{{Placement: line.Below}},
			overlays:    line.Overlays{{Cols: position.NewSpan(20, 22)}},
			rowStarts:   []int{0, 10, 20},
			position:    line.Below,
			want:        []printer.AnnotationRow{{Col: 20, Marker: "^^"}},
		},
		"wrapped content keeps content annotations on one row": {
			content:     "key: aaaa bbbb",
			annotations: line.Annotations{{Content: "bad", Placement: line.Below, Col: 10}},
			overlays:    line.Overlays{{Cols: position.NewSpan(5, 14)}},
			rowStarts:   []int{0, 10},
			position:    line.Below,
			want:        []printer.AnnotationRow{{Col: 10, Marker: "^ ", Text: "bad"}},
		},
		"wrapped content clamps a negative row start and end": {
			content:     "abc",
			annotations: line.Annotations{{Placement: line.Below}},
			overlays:    line.Overlays{{Cols: position.NewSpan(0, 3)}},
			rowStarts:   []int{-2},
			rowEnds:     []int{-1},
			position:    line.Below,
			want:        []printer.AnnotationRow{{Col: 0, Marker: "^"}},
		},
		"wrapped content clamps negative row starts without ends": {
			content:     "abc",
			annotations: line.Annotations{{Placement: line.Below}},
			overlays:    line.Overlays{{Cols: position.NewSpan(0, 3)}},
			rowStarts:   []int{-5, -2},
			position:    line.Below,
			want:        []printer.AnnotationRow{{Col: 0, Marker: "^^^"}},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := printer.DefaultAnnotation(printer.AnnotationContext{
				Content:     tc.content,
				Annotations: tc.annotations,
				Overlays:    tc.overlays,
				RowStarts:   tc.rowStarts,
				RowEnds:     tc.rowEnds,
				Placement:   tc.position,
			})
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPrinter_WithAnnotation(t *testing.T) {
	t.Parallel()

	// Custom annotation function that uses different markers.
	customAnnotation := func(ctx printer.AnnotationContext) []printer.AnnotationRow {
		if len(ctx.Annotations) == 0 {
			return nil
		}

		row := printer.AnnotationRow{
			Col:    ctx.Annotations.Col(),
			Marker: "=== ",
			Text:   strings.Join(ctx.Annotations.Contents(), ", "),
		}

		if ctx.Placement == line.Below {
			row.Marker = ">>> "
		}

		return []printer.AnnotationRow{row}
	}

	tcs := map[string]struct {
		annotation line.Annotation
		lineIndex  int
		want       string
	}{
		"custom below annotation": {
			lineIndex: 0,
			annotation: line.Annotation{
				Content:   "custom error",
				Placement: line.Below,
				Col:       0,
			},
			want: "key: value\n>>> custom error",
		},
		"custom above annotation": {
			lineIndex: 0,
			annotation: line.Annotation{
				Content:   "custom header",
				Placement: line.Above,
				Col:       0,
			},
			want: "=== custom header\nkey: value",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := niceyaml.NewSourceFromString("key: value").View()
			view.Annotate(tc.lineIndex, tc.annotation)

			p := printer.New(
				printer.WithStyles(style.Styles{}),
				printer.WithContainerStyle(lipgloss.NewStyle()),
				printer.WithGutter(printer.NoGutter),
				printer.WithAnnotation(customAnnotation),
			)

			got := p.Print(view)
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

		p := printer.New(
			printer.WithStyles(yamltest.NewXMLStyles()),
			printer.WithAnnotation(sortByStart),
		)

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

	// Annotations of one kind share a row, and every kind renders as rows
	// of its own in its style. The zero kind renders as kind.UIAnnotation.
	view := niceyaml.NewSourceFromString("key: value").View()
	view.Annotate(0,
		line.Annotation{Content: "hunk", Placement: line.Above},
		line.Annotation{Content: "bad key", Kind: kind.TextError, Placement: line.Below, Col: 0},
		line.Annotation{Content: "note", Placement: line.Below, Col: 5},
		line.Annotation{Content: "bad value", Kind: kind.TextError, Placement: line.Below, Col: 5},
		line.Annotation{Content: "hint", Kind: kind.UIAnnotation, Placement: line.Below, Col: 5},
	)

	p := printer.New(
		printer.WithStyles(yamltest.NewXMLStyles()),
		printer.WithContainerStyle(lipgloss.NewStyle()),
		printer.WithGutter(printer.NoGutter),
	)

	want := stringtest.JoinLF(
		"<uiAnnotation>hunk</uiAnnotation>",
		"<nameTag>key</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
		"<textError>^ bad key; bad value</textError>",
		"<uiAnnotation>     ^ note; hint</uiAnnotation>",
	)

	assert.Equal(t, want, p.Print(view))
	assert.Equal(t, []int{4}, layoutRows(p.Layout(view)))
}

func TestPrinter_AnnotationFuncKind(t *testing.T) {
	t.Parallel()

	view := niceyaml.NewSourceFromString("key: value").View()
	view.Annotate(0, line.Annotation{Content: "oops \x1b[31mred", Placement: line.Below})

	styles := style.New(lipgloss.NewStyle(), style.Set(kind.TextError, lipgloss.NewStyle().Bold(true)))
	errored := func(ctx printer.AnnotationContext) []printer.AnnotationRow {
		return []printer.AnnotationRow{{
			Text: strings.Join(ctx.Annotations.Contents(), "; "),
			Kind: kind.TextError,
		}}
	}

	p := printer.New(
		printer.WithStyles(styles),
		printer.WithContainerStyle(lipgloss.NewStyle()),
		printer.WithGutter(printer.NoGutter),
		printer.WithAnnotation(errored),
	)

	// The row renders in the Kind the func chose, and the printer escapes
	// the text, so an escape sequence in a message shows as its picture
	// rather than styling the output.
	got := p.Print(view)
	assert.Contains(t, got, "\x1b[1moops \u241b[31mred")
	assert.NotContains(t, got, "\x1b[31m")
}

func TestPrinter_AnnotationWrap(t *testing.T) {
	t.Parallel()

	// Joins the contents with no column and no marker, so continuation
	// rows carry no indent.
	bare := func(ctx printer.AnnotationContext) []printer.AnnotationRow {
		return []printer.AnnotationRow{{Text: strings.Join(ctx.Annotations.Contents(), " ")}}
	}

	// Marks the annotations with a marker of its own at their column.
	arrow := func(ctx printer.AnnotationContext) []printer.AnnotationRow {
		return []printer.AnnotationRow{{
			Col:    ctx.Annotations.Col(),
			Marker: "-> ",
			Text:   strings.Join(ctx.Annotations.Contents(), " "),
		}}
	}

	tcs := map[string]struct {
		annFunc    printer.AnnotationFunc
		gutter     printer.Gutter
		input      string
		want       string
		annotation line.Annotation
		width      int
	}{
		"below rows align under the marker within the width": {
			input:  "key: value",
			gutter: printer.LineNumberGutter,
			width:  40,
			annotation: line.Annotation{
				Content:   "this is a fairly long annotation message that will wrap",
				Placement: line.Below,
				Col:       20,
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
			input:  "key: value",
			gutter: printer.NoGutter,
			width:  16,
			annotation: line.Annotation{
				Content:   "one two three four five",
				Placement: line.Above,
				Col:       4,
			},
			want: stringtest.JoinLF(
				"    one two",
				"    three four",
				"    five",
				"key: value",
			),
		},
		"column past the width keeps the marker at its column": {
			input:  "key: value",
			gutter: printer.NoGutter,
			width:  25,
			annotation: line.Annotation{
				Content:   "x",
				Placement: line.Below,
				Col:       30,
			},
			want: stringtest.JoinLF(
				"key: value",
				strings.Repeat(" ", 30)+"^",
				strings.Repeat(" ", 5)+"x",
			),
		},
		"one-cell word at the last column hangs": {
			input:  "key: aaaaaaaaaaaaaaa",
			gutter: printer.NoGutter,
			width:  20,
			annotation: line.Annotation{
				Content:   "x",
				Placement: line.Below,
				Col:       19,
			},
			want: stringtest.JoinLF(
				"key: aaaaaaaaaaaaaaa",
				strings.Repeat(" ", 19)+"^",
				"x",
			),
		},
		"one-cell words with no room beside the marker hang": {
			input:  "key: aaaaaaaaaaaaaaa",
			gutter: printer.NoGutter,
			width:  20,
			annotation: line.Annotation{
				Content:   "a b c",
				Placement: line.Below,
				Col:       18,
			},
			want: stringtest.JoinLF(
				"key: aaaaaaaaaaaaaaa",
				strings.Repeat(" ", 18)+"^",
				"a b c",
			),
		},
		"marker with empty text at the last column keeps one row": {
			input:   "key: aaaaaaaaaaaaaaa",
			gutter:  printer.NoGutter,
			width:   20,
			annFunc: arrow,
			annotation: line.Annotation{
				Placement: line.Below,
				Col:       19,
			},
			want: stringtest.JoinLF(
				"key: aaaaaaaaaaaaaaa",
				strings.Repeat(" ", 19)+"-> ",
			),
		},
		"one-cell word with one cell of room stays beside the marker": {
			input:  "key: aaaaaaaaaaaaaaa",
			gutter: printer.NoGutter,
			width:  20,
			annotation: line.Annotation{
				Content:   "x",
				Placement: line.Below,
				Col:       17,
			},
			want: stringtest.JoinLF(
				"key: aaaaaaaaaaaaaaa",
				strings.Repeat(" ", 17)+"^ x",
			),
		},
		"below column on the last wrapped row pads from that row's start": {
			input:  "key: aaaa bbbb cccc",
			gutter: printer.NoGutter,
			width:  10,
			annotation: line.Annotation{
				Content:   "x",
				Placement: line.Below,
				Col:       15,
			},
			want: stringtest.JoinLF(
				"key: aaaa",
				"bbbb cccc",
				"     ^ x",
			),
		},
		// The wrap drops the space before the accent, so the next row
		// starts with the accent alone, which takes no cell.
		"below column on a row that starts with a combining accent": {
			input:  "k: aaaa ́bbbb",
			gutter: printer.NoGutter,
			width:  8,
			annotation: line.Annotation{
				Content:   "here",
				Placement: line.Below,
				Col:       9,
			},
			want: stringtest.JoinLF(
				"k: aaaa",
				"́bbbb",
				"^ here",
			),
		},
		// A spacing mark takes a cell on its own at the start of the row.
		"below column on a row that starts with a spacing mark": {
			input:  "k: aaaa िbbbb",
			gutter: printer.NoGutter,
			width:  8,
			annotation: line.Annotation{
				Content:   "here",
				Placement: line.Below,
				Col:       9,
			},
			want: stringtest.JoinLF(
				"k: aaaa",
				"िbbbb",
				" ^ here",
			),
		},
		"above column on a wrapped row sits above that row": {
			input:  "key: aaaa bbbb cccc",
			gutter: printer.NoGutter,
			width:  10,
			annotation: line.Annotation{
				Content:   "hi",
				Placement: line.Above,
				Col:       15,
			},
			want: stringtest.JoinLF(
				"key: aaaa",
				"     hi",
				"bbbb cccc",
			),
		},
		"below column on an earlier wrapped row sits below that row": {
			input:  "key: aaaa bbbb cccc",
			gutter: printer.NoGutter,
			width:  10,
			annotation: line.Annotation{
				Content:   "x",
				Placement: line.Below,
				Col:       5,
			},
			want: stringtest.JoinLF(
				"key: aaaa",
				"     ^ x",
				"bbbb cccc",
			),
		},
		"column on a wrapped row pads from that row's start past the gutter": {
			input:  "key: aaaa bbbb cccc",
			gutter: printer.LineNumberGutter,
			width:  15,
			annotation: line.Annotation{
				Content:   "x",
				Placement: line.Below,
				Col:       15,
			},
			want: stringtest.JoinLF(
				"   1 key: aaaa",
				"   - bbbb cccc",
				"          ^ x",
			),
		},
		"column near the edge moves the text off the marker row": {
			input:  "key: value",
			gutter: printer.NoGutter,
			width:  16,
			annotation: line.Annotation{
				Content:   "string does not match pattern",
				Placement: line.Below,
				Col:       8,
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
			input:  "key: value",
			gutter: printer.NoGutter,
			width:  10,
			annotation: line.Annotation{
				Content:   "abc-def",
				Placement: line.Below,
				Col:       5,
			},
			want: stringtest.JoinLF(
				"key: value",
				strings.Repeat(" ", 5)+"^",
				"abc-def",
			),
		},
		"word with a slash wider than the room beside the marker hangs": {
			input:  "key: value",
			gutter: printer.NoGutter,
			width:  10,
			annotation: line.Annotation{
				Content:   "abc/def",
				Placement: line.Below,
				Col:       5,
			},
			want: stringtest.JoinLF(
				"key: value",
				strings.Repeat(" ", 5)+"^",
				"abc/def",
			),
		},
		"words split at ideographic spaces fit beside the marker": {
			// The wrap breaks at every Unicode space but the no-break
			// space, so each word fits the room beside the marker.
			input:  "key: value",
			gutter: printer.NoGutter,
			width:  16,
			annotation: line.Annotation{
				Content:   "aaaa　bbbb　cccc",
				Placement: line.Below,
				Col:       5,
			},
			want: stringtest.JoinLF(
				"key: value",
				strings.Repeat(" ", 5)+"^ aaaa",
				strings.Repeat(" ", 7)+"bbbb",
				strings.Repeat(" ", 7)+"cccc",
			),
		},
		"words split at em spaces fit beside the marker": {
			input:  "key: value",
			gutter: printer.NoGutter,
			width:  16,
			annotation: line.Annotation{
				Content:   "aaaa bbbb cccc",
				Placement: line.Below,
				Col:       5,
			},
			want: stringtest.JoinLF(
				"key: value",
				strings.Repeat(" ", 5)+"^ aaaa bbbb",
				strings.Repeat(" ", 7)+"cccc",
			),
		},
		"words joined by a no-break space wider than the room hang": {
			input:  "key: value",
			gutter: printer.NoGutter,
			width:  15,
			annotation: line.Annotation{
				Content:   "aaaa bbbb",
				Placement: line.Below,
				Col:       5,
			},
			want: stringtest.JoinLF(
				"key: value",
				strings.Repeat(" ", 5)+"^",
				"aaaa bbbb",
			),
		},
		"column near the edge hangs the text under a narrower indent": {
			input:  "key: value",
			gutter: printer.NoGutter,
			width:  40,
			annotation: line.Annotation{
				Content:   "string does not match pattern",
				Placement: line.Below,
				Col:       34,
			},
			want: stringtest.JoinLF(
				"key: value",
				strings.Repeat(" ", 34)+"^",
				strings.Repeat(" ", 20)+"string does not",
				strings.Repeat(" ", 20)+"match pattern",
			),
		},
		"negative column renders at column zero": {
			input:  "key: value",
			gutter: printer.NoGutter,
			width:  0,
			annotation: line.Annotation{
				Content:   "x",
				Placement: line.Below,
				Col:       -1,
			},
			want: stringtest.JoinLF(
				"key: value",
				"^ x",
			),
		},
		"negative column renders at column zero when wrapping": {
			input:  "key: value",
			gutter: printer.NoGutter,
			width:  10,
			annotation: line.Annotation{
				Content:   "x",
				Placement: line.Below,
				Col:       -1,
			},
			want: stringtest.JoinLF(
				"key: value",
				"^ x",
			),
		},
		"control characters are escaped before wrapping": {
			input:  "key: value",
			gutter: printer.NoGutter,
			width:  20,
			annotation: line.Annotation{
				Content:   "\x1b[31mred\x1b[0m w w w w w w w w",
				Placement: line.Below,
				Col:       0,
			},
			want: stringtest.JoinLF(
				"key: value",
				"^ ␛[31mred␛[0m w w w",
				"  w w w w w",
			),
		},
		"embedded newline renders as a picture without wrapping": {
			input:  "key: value",
			gutter: printer.NoGutter,
			width:  0,
			annotation: line.Annotation{
				Content:   "a\nb",
				Placement: line.Below,
				Col:       0,
			},
			want: stringtest.JoinLF(
				"key: value",
				"^ a␊b",
			),
		},
		"embedded newline renders as a picture when wrapping": {
			input:  "key: value",
			gutter: printer.NoGutter,
			width:  20,
			annotation: line.Annotation{
				Content:   "a\nb",
				Placement: line.Below,
				Col:       0,
			},
			want: stringtest.JoinLF(
				"key: value",
				"^ a␊b",
			),
		},
		"custom annotation func gets no column indent": {
			input:   "k: v",
			gutter:  printer.NoGutter,
			width:   6,
			annFunc: bare,
			annotation: line.Annotation{
				Content:   "w w w w w w",
				Placement: line.Below,
				Col:       10,
			},
			want: stringtest.JoinLF(
				"k: v",
				"w w w",
				"w w w",
			),
		},
		"custom marker rows align under the text": {
			input:   "key: value",
			gutter:  printer.NoGutter,
			width:   18,
			annFunc: arrow,
			annotation: line.Annotation{
				Content:   "alpha beta gamma delta epsilon",
				Placement: line.Below,
				Col:       5,
			},
			want: stringtest.JoinLF(
				"key: value",
				"     -> alpha beta",
				"        gamma",
				"        delta",
				"        epsilon",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := niceyaml.NewSourceFromString(tc.input).View()
			view.Annotate(0, tc.annotation)

			p := testPrinterWithGutter(tc.gutter).With(printer.WithWrap(tc.width))
			if tc.annFunc != nil {
				p = p.With(printer.WithAnnotation(tc.annFunc))
			}

			got := p.Print(view)
			assert.Equal(t, tc.want, got)

			// The layout counts the rows Print writes and measures the
			// widest of them.
			rows := strings.Split(got, "\n")
			layout := p.Layout(view)
			assert.Len(t, rows, layout.Rows())

			widest := 0
			for _, row := range rows {
				widest = max(widest, lipgloss.Width(row))
			}

			assert.Equal(t, widest, layout.Width())
		})
	}
}

func TestPrinter_PrintError_WrappedAnnotation(t *testing.T) {
	t.Parallel()

	const width = 40

	p := printer.New(
		printer.WithWrap(width),
		printer.WithContainerStyle(lipgloss.NewStyle()),
	)

	items := make([]string, 0, 20)
	for i := range 20 {
		items = append(items, fmt.Sprintf("item%02d", i))
	}

	// The sequence wraps over several rows, and item 16 sits near the
	// start of its row, so the message fits beside the caret.
	source := niceyaml.NewSourceFromString("items: [" + strings.Join(items, ", ") + "]\n")
	err := yamltest.Bind(t, source, niceyaml.NewError("outer", niceyaml.WithErrors(
		niceyaml.NewError("expected string", niceyaml.AtPath(paths.Root().Child("items").Index(16))),
	)))

	got := p.PrintError(err)

	for i, row := range strings.Split(got, "\n") {
		assert.LessOrEqual(t, lipgloss.Width(row), width, "row %d: %q", i, row)
	}

	assert.Contains(t, got, "^ expected string")
}

func TestPrinter_PrintError_AnnotationOnWrappedRow(t *testing.T) {
	t.Parallel()

	items := make([]string, 0, 20)
	for i := range 20 {
		items = append(items, fmt.Sprintf("item%02d", i))
	}

	source := niceyaml.NewSourceFromString("items: [" + strings.Join(items, ", ") + "]\n")

	rows := []string{
		"   1  items: [item00, item01, item02,",
		"   -  item03, item04, item05, item06,",
		"   -  item07, item08, item09, item10,",
		"   -  item11, item12, item13, item14,",
		"   -  item15, item16, item17, item18,",
		"   -  item19]",
	}

	// The message sits below the wrapped row that holds the item, whether
	// or not that row is the last, so the caret lands under the item.
	tcs := map[string]struct {
		want  []string
		index int
	}{
		"item on an earlier row": {
			index: 5,
			want: slices.Concat(
				[]string{"outer", "└── 1:49: $.items[5]: expected string", ""},
				rows[:2],
				[]string{strings.Repeat(" ", 22) + "^ expected string"},
				rows[2:],
			),
		},
		"item on the row before the last": {
			index: 16,
			want: slices.Concat(
				[]string{"outer", "└── 1:137: $.items[16]: expected string", ""},
				rows[:5],
				[]string{strings.Repeat(" ", 14) + "^ expected string"},
				rows[5:],
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := yamltest.Bind(t, source, niceyaml.NewError("outer", niceyaml.WithErrors(
				niceyaml.NewError("expected string", niceyaml.AtPath(paths.Root().Child("items").Index(tc.index))),
			)))

			p := printer.New(
				printer.WithStyles(style.Styles{}),
				printer.WithContainerStyle(lipgloss.NewStyle()),
				printer.WithWrap(40),
			)

			assert.Equal(t, stringtest.JoinLF(tc.want...), p.PrintError(err))
		})
	}
}

func TestPrinter_PrintError_AnnotationNearEdge(t *testing.T) {
	t.Parallel()

	const width = 80

	p := printer.New(
		printer.WithWrap(width),
		printer.WithContainerStyle(lipgloss.NewStyle()),
	)

	// The value starts near the right edge, so the message has too little
	// room beside the caret and moves to the rows below it.
	key := strings.Repeat("a", 66)
	source := niceyaml.NewSourceFromString(key + ": value\n")
	err := yamltest.Bind(t, source, niceyaml.NewError("outer", niceyaml.WithErrors(
		niceyaml.NewError("string does not match pattern", niceyaml.AtPath(paths.Root().Child(key))),
	)))

	got := p.PrintError(err)

	var trimmed []string

	for i, row := range strings.Split(got, "\n") {
		assert.LessOrEqual(t, lipgloss.Width(row), width, "row %d: %q", i, row)

		trimmed = append(trimmed, strings.TrimSpace(ansi.Strip(row)))
	}

	// The header holds the whole message on one row, so these two rows
	// come from the annotation.
	assert.Contains(t, trimmed, "string does not")
	assert.Contains(t, trimmed, "match pattern")
}

func TestPrinter_Print_EmptySpans(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		want     string
		spans    []position.Span
		wantRows []int
	}{
		"empty span first": {
			spans:    []position.Span{position.NewSpan(0, 0), position.NewSpan(0, 2)},
			want:     "a: 1\nb: 2",
			wantRows: []int{1, 1},
		},
		"out of range span first": {
			spans:    []position.Span{position.NewSpan(5, 9), position.NewSpan(0, 1)},
			want:     "a: 1",
			wantRows: []int{1},
		},
		"empty span between": {
			spans:    []position.Span{position.NewSpan(0, 1), position.NewSpan(1, 1), position.NewSpan(1, 2)},
			want:     "a: 1\nb: 2",
			wantRows: []int{1, 1},
		},
		"empty span last": {
			spans:    []position.Span{position.NewSpan(0, 2), position.NewSpan(2, 2)},
			want:     "a: 1\nb: 2",
			wantRows: []int{1, 1},
		},
		"only empty spans": {
			spans:    []position.Span{position.NewSpan(0, 0), position.NewSpan(2, 2)},
			want:     "",
			wantRows: nil,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := niceyaml.NewSourceFromString("a: 1\nb: 2").View().Slice(tc.spans...)
			p := testPrinter()

			got := p.Print(view)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.wantRows, layoutRows(p.Layout(view)))
			assert.Len(t, strings.Split(got, "\n"), max(1, len(tc.wantRows)))
		})
	}
}

func TestPrinter_LineNumbers_MaxNumber(t *testing.T) {
	t.Parallel()

	// A slice of a long document has a Len of one but a line number past
	// 9999, so the gutter must size itself from the number, and every row
	// of the line, wrapped or annotated, must share that width.
	input := strings.Repeat("k: v\n", 10000) + "last: this is a long value that wraps"
	source := niceyaml.NewSourceFromString(input)
	view := source.View().Slice(position.NewSpan(10000, source.Lines().Len()))
	view.Annotate(10000, line.Annotation{Content: "note", Placement: line.Below, Col: 6})

	p := testPrinterWithGutter(printer.LineNumberGutter).With(printer.WithWrap(30))

	got := p.Print(view)
	for row := range strings.SplitSeq(got, "\n") {
		assert.LessOrEqual(t, lipgloss.Width(row), 30, row)
	}

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
	before := niceyaml.NewSourceFromString("a: 1\nb: 2\nc: 3").Lines()
	after := niceyaml.NewSourceFromString("a: 1").Lines()

	p := testPrinterWithGutter(printer.DefaultGutter)

	assert.Equal(t, stringtest.JoinLF(
		"   1  a: 1",
		"      ",
		"      ",
	), p.Print(diff.Diff(before, after).After()))
	assert.Equal(t, stringtest.JoinLF(
		"   1  a: 1",
		"   2 -b: 2",
		"   3 -c: 3",
	), p.Print(diff.Diff(before, after).Before()))
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

	wrap := func(tag string) lipgloss.Style {
		return lipgloss.NewStyle().Transform(func(s string) string {
			return "<" + tag + ">" + s + "</" + tag + ">"
		})
	}

	view := niceyaml.NewSourceFromString("k: 1\nk: 2").View()
	view.AddLineOverlay(0, line.Overlay{Kind: ab, Cols: position.NewSpan(0, 4), Blend: true})
	view.AddLineOverlay(1,
		line.Overlay{Kind: a, Cols: position.NewSpan(0, 4), Blend: true},
		line.Overlay{Kind: b, Cols: position.NewSpan(0, 4)},
	)

	p := printer.New(
		printer.WithStyles(style.New(
			lipgloss.NewStyle(),
			style.Set(ab, wrap("AB")),
			style.Set(a, wrap("A")),
			style.Set(b, wrap("B")),
		)),
		printer.WithContainerStyle(lipgloss.NewStyle()),
		printer.WithGutter(printer.NoGutter),
	)

	assert.Equal(t, stringtest.JoinLF(
		"<AB>k</AB><AB>:</AB><AB> </AB><AB>1</AB>",
		"<B>k</B><B>:</B><B> </B><B>2</B>",
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

			p := printer.New(
				printer.WithStyles(style.New(lipgloss.NewStyle(), style.Set(underlined, st))),
				printer.WithContainerStyle(lipgloss.NewStyle()),
				printer.WithGutter(printer.NoGutter),
			)

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
		style.Set(kind.LiteralString, testHighlightStyle()),
		style.Set(kind.LiteralStringDouble, testHighlightStyle()),
		style.Set(kind.Comment, testHighlightStyle()),
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

			p := printer.New(
				printer.WithStyles(styles),
				printer.WithContainerStyle(lipgloss.NewStyle()),
				printer.WithGutter(printer.NoGutter),
			)

			assert.Equal(t, tc.want, p.Print(niceyaml.NewSourceFromString(tc.input).View()))
		})
	}
}

func TestPrinter_Layout_GutterWidth(t *testing.T) {
	t.Parallel()

	short := niceyaml.NewSourceFromString("a: 1\nb: 2\nc: 3").View()
	long := niceyaml.NewSourceFromString(strings.Repeat("k: v\n", 10000) + "last: v").View()

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
			view:   long.Slice(position.NewSpan(10000, long.Lines().Len())),
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

func TestAnnotationContext_ColWidth(t *testing.T) {
	t.Parallel()

	// The rendered row shows a control character as a one-cell picture, so
	// the width counts it as one cell rather than the zero lipgloss gives
	// the raw character.
	ctx := printer.AnnotationContext{Content: "a: \"tab\there\""}

	tcs := map[string]struct {
		col  int
		want int
	}{
		"before the control":       {col: 3, want: 3},
		"just past the control":    {col: 8, want: 8},
		"end of the content":       {col: 13, want: 13},
		"past the end":             {col: 20, want: 20},
		"negative clamps to start": {col: -1, want: 0},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, ctx.ColWidth(tc.col))
		})
	}
}

func TestAnnotationContext_ColWidth_CombiningMark(t *testing.T) {
	t.Parallel()

	// The accent is a combining mark that renders on the "e" before it, so
	// a marker at its column lands under that cell rather than the next.
	ctx := printer.AnnotationContext{Content: "k: éx"}

	assert.Equal(t, 3, ctx.ColWidth(3), "the base rune")
	assert.Equal(t, 3, ctx.ColWidth(4), "the combining mark")
	assert.Equal(t, 4, ctx.ColWidth(5), "the rune after the mark")
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

			view := niceyaml.NewSourceFromString("a: 1\nb: 2\n").View()
			view.Annotate(0, line.Annotation{Content: "one\ntwo"})

			p := testPrinter().With(printer.WithAnnotation(joined), printer.WithWrap(tc.width))

			printed := strings.Split(strings.TrimSuffix(p.Print(view), "\n"), "\n")
			assert.Len(t, printed, p.Layout(view).Rows())
		})
	}
}

func TestPrinter_WithMaxNumber(t *testing.T) {
	t.Parallel()

	short := niceyaml.NewSourceFromString("a: 1\nb: 2").View()

	t.Run("sizes the gutter for the given number", func(t *testing.T) {
		t.Parallel()

		p := testPrinterWithGutter(printer.LineNumberGutter).With(printer.WithMaxNumber(10000))

		assert.Equal(t, 10000, p.MaxNumber(short))
		assert.Equal(t, 6, p.Layout(short).GutterWidth())

		// Two views of different lengths then share a gutter width.
		long := niceyaml.NewSourceFromString(strings.Repeat("k: v\n", 10000)).View()
		assert.Equal(t, p.Layout(long).GutterWidth(), p.Layout(short).GutterWidth())
	})

	t.Run("zero takes the number from the view", func(t *testing.T) {
		t.Parallel()

		p := testPrinterWithGutter(printer.LineNumberGutter).With(printer.WithMaxNumber(0))

		assert.Equal(t, 2, p.MaxNumber(short))
		assert.Equal(t, 5, p.Layout(short).GutterWidth())
	})

	t.Run("a longer view widens the gutter past the given number", func(t *testing.T) {
		t.Parallel()

		// A number below the view's own largest would let the last rows
		// overflow the gutter the layout reports, so the view wins.
		long := niceyaml.NewSourceFromString(strings.Repeat("k: v\n", 10000)).View()
		p := testPrinterWithGutter(printer.LineNumberGutter).With(printer.WithMaxNumber(3))

		assert.Equal(t, 10000, p.MaxNumber(long))
		assert.Equal(t, 6, p.Layout(long).GutterWidth())
	})
}

func TestPrinter_Layout_Width(t *testing.T) {
	t.Parallel()

	wide := niceyaml.NewSourceFromString("k: " + strings.Repeat("\u65e5", 3) + "\nb: 2").View()

	annotated := niceyaml.NewSourceFromString("a: 1\nb: 2").View()
	annotated.Annotate(0, line.Annotation{Content: "a note wider than the lines", Placement: line.Above})

	tcs := map[string]struct {
		view   *line.View
		gutter printer.Gutter
		spans  []position.Span
		wrap   int
		want   int
	}{
		"widest line": {
			view:   niceyaml.NewSourceFromString("a: 1\nbb: 222").View(),
			gutter: printer.NoGutter,
			want:   7,
		},
		"gutter adds to every row": {
			view:   niceyaml.NewSourceFromString("a: 1\nbb: 222").View(),
			gutter: printer.DefaultGutter,
			want:   13,
		},
		"wide characters take two cells each": {
			view:   wide,
			gutter: printer.NoGutter,
			want:   9,
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
			view:   annotated,
			gutter: printer.NoGutter,
			want:   27,
		},
		"span selects the lines measured": {
			view:   niceyaml.NewSourceFromString("a: 1\nbb: 222").View(),
			gutter: printer.NoGutter,
			spans:  []position.Span{position.NewSpan(0, 1)},
			want:   4,
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

			p := testPrinterWithGutter(tc.gutter).With(printer.WithWrap(tc.wrap))
			view := tc.view.Slice(tc.spans...)
			got := p.Layout(view).Width()

			assert.Equal(t, tc.want, got)

			// The width is the widest row Print renders.
			widest := 0
			for row := range strings.SplitSeq(p.Print(view), "\n") {
				widest = max(widest, lipgloss.Width(row))
			}

			if view.Count() > 0 {
				assert.Equal(t, got, widest)
			}
		})
	}
}

func TestPrinter_Layout_LineWidth(t *testing.T) {
	t.Parallel()

	view := niceyaml.NewSourceFromString("a: 1\nbb: " + strings.Repeat("日", 3) + "\nc: 3").View()
	view.Annotate(2, line.Annotation{Content: "a note wider than the lines", Placement: line.Below})

	tcs := map[string]struct {
		spans []position.Span
		want  []int
	}{
		"every line": {
			want: []int{10, 16, 35},
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
				widest := 0
				for row := range strings.SplitSeq(p.Print(view.Slice(position.NewSpan(i, i+1))), "\n") {
					widest = max(widest, lipgloss.Width(row))
				}

				assert.Equal(t, widest, l.LineWidth(i), "line %d", i)
			}

			assert.Equal(t, 0, l.LineWidth(-1))
			assert.Equal(t, 0, l.LineWidth(len(tc.want)))
		})
	}
}

func TestPrinter_Layout_Width_StyledAnnotation(t *testing.T) {
	t.Parallel()

	view := niceyaml.NewSourceFromString("key: value\n").View()
	view.Annotate(0, line.Annotation{Content: "note", Placement: line.Below, Col: 5})

	p := printer.New(
		printer.WithGutter(printer.NoGutter),
		printer.WithContainerStyle(lipgloss.NewStyle()),
		printer.WithStyles(style.New(
			lipgloss.NewStyle(),
			style.Set(kind.UIAnnotation, lipgloss.NewStyle().PaddingLeft(6)),
		)),
	)

	widest := 0
	for row := range strings.SplitSeq(p.Print(view), "\n") {
		widest = max(widest, lipgloss.Width(row))
	}

	// The style pads the annotation row past the content, and the layout
	// measures that row styled, as Print renders it.
	assert.Greater(t, widest, lipgloss.Width("key: value"))
	assert.Equal(t, widest, p.Layout(view).Width())
}

func TestPrinter_Layout_StyledAnnotationRows(t *testing.T) {
	t.Parallel()

	// A style with a width, vertical padding, a margin, or a border lays an
	// annotation out over more rows than the wrap gives it. The layout
	// counts and measures those rows as Print writes them, and Print puts
	// the gutter on each of them.
	tcs := map[string]struct {
		style  lipgloss.Style
		gutter printer.Gutter
		wrap   int
		col    int
	}{
		"width": {
			style:  lipgloss.NewStyle().Width(6),
			gutter: printer.NoGutter,
		},
		"vertical padding": {
			style:  lipgloss.NewStyle().PaddingTop(1),
			gutter: printer.NoGutter,
		},
		"margin": {
			style:  lipgloss.NewStyle().MarginBottom(1),
			gutter: printer.NoGutter,
		},
		"border": {
			style:  lipgloss.NewStyle().Border(lipgloss.NormalBorder()),
			gutter: printer.NoGutter,
		},
		"continuation rows": {
			style:  lipgloss.NewStyle().PaddingLeft(2),
			gutter: printer.NoGutter,
			wrap:   12,
			col:    2,
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
			view.Annotate(0, line.Annotation{Content: "hello world foo", Placement: line.Below, Col: tc.col})

			p := printer.New(
				printer.WithContainerStyle(lipgloss.NewStyle()),
				printer.WithGutter(tc.gutter),
				printer.WithWrap(tc.wrap),
				printer.WithStyles(style.New(
					lipgloss.NewStyle(),
					style.Set(kind.UIAnnotation, tc.style),
				)),
			)

			printed := strings.Split(p.Print(view), "\n")
			l := p.Layout(view)

			require.Len(t, printed, l.Rows())
			assert.Greater(t, l.Rows(), 2, "the annotation takes several rows")

			widest := 0
			for _, row := range printed {
				widest = max(widest, lipgloss.Width(row))
			}

			assert.Equal(t, widest, l.Width())

			gutter := strings.Repeat(" ", l.GutterWidth())
			for i, row := range printed[1:] {
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

			view := niceyaml.NewSourceFromString("a: 1\nb: 2\n").View()
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

func TestPrinter_WithGutter_Nil(t *testing.T) {
	t.Parallel()

	view := niceyaml.NewSourceFromString("key: value").View()
	view.Annotate(0, line.Annotation{Content: "note", Placement: line.Below, Col: 5})

	p := testPrinterWithGutter(nil)

	assert.Equal(t, testPrinterWithGutter(printer.NoGutter).Print(view), p.Print(view))
	assert.Equal(t, "key: value\n     ^ note", p.Print(view))
}

func TestPrinter_WithStyles_Nil(t *testing.T) {
	t.Parallel()

	view := niceyaml.NewSourceFromString("key: value").View()

	// A nil Styler selects the default styles rather than panicking
	// in New.
	assert.Equal(t, printer.New().Print(view), printer.New(printer.WithStyles(nil)).Print(view))
}

func TestPrinter_WithAnnotation_Nil(t *testing.T) {
	t.Parallel()

	view := niceyaml.NewSourceFromString("key: value").View()
	view.Annotate(0, line.Annotation{Content: "note", Placement: line.Below, Col: 5})

	// A nil AnnotationFunc selects DefaultAnnotation rather than panicking
	// on the first annotated line.
	p := testPrinterWithGutter(nil).With(printer.WithAnnotation(nil))

	assert.Equal(t, "key: value\n     ^ note", p.Print(view))
}

func TestPrinter_Layout_CellOf(t *testing.T) {
	t.Parallel()

	// The overlay style wraps its text in brackets, so every cell after an
	// overlay shifts by the two brackets it adds.
	tcs := map[string]struct {
		overlay *position.Span
		content string
		pos     position.Position
		wrap    int
		want    int
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
			content: "k: ab cd",
			overlay: &position.Span{Start: 3, End: 5},
			pos:     position.New(0, 6),
			want:    8,
		},
		"first column of a transformed run": {
			content: "k: ab cd",
			overlay: &position.Span{Start: 3, End: 5},
			pos:     position.New(0, 3),
			want:    3,
		},
		"column inside a transformed run": {
			content: "k: ab cd",
			overlay: &position.Span{Start: 3, End: 5},
			pos:     position.New(0, 4),
			want:    5,
		},
		"end of a transformed run": {
			content: "k: ab cd",
			overlay: &position.Span{Start: 3, End: 5},
			pos:     position.New(0, 5),
			want:    7,
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
			if tc.overlay != nil {
				view.AddOverlay(testOverlayHighlight, position.NewRange(
					position.New(0, tc.overlay.Start),
					position.New(0, tc.overlay.End),
				))
			}

			p := testPrinter().With(printer.WithWrap(tc.wrap))

			assert.Equal(t, tc.want, p.Layout(view).CellOf(tc.pos))
		})
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
		view.Annotate(0, line.Annotation{Content: "below", Placement: line.Below, Col: 5})
		view.Annotate(2, line.Annotation{Content: "last", Placement: line.Below, Col: 3})

		return view
	}

	want := stringtest.JoinLF(
		"     a note above",
		"     that wraps too",
		"   1 key: this is a",
		"          ^ below",
		"   - long value that",
		"   - wraps",
		"   2 b: 2",
		"   3 c: 3",
		"        ^ last",
	)

	p := testPrinterWithGutter(printer.LineNumberGutter).With(printer.WithWrap(20))

	t.Run("rows match print", func(t *testing.T) {
		t.Parallel()

		view := newView()
		got := p.Print(view)
		require.Equal(t, want, got)

		l := p.Layout(view)

		assert.Equal(t, len(strings.Split(got, "\n")), l.Rows())
		assert.Equal(t, 3, l.Count())
		assert.Equal(t, []int{6, 1, 2}, layoutRows(l))
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

		assert.Equal(t, len(strings.Split(got, "\n")), l.Rows())
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

	t.Run("tabs and control characters keep columns aligned", func(t *testing.T) {
		t.Parallel()

		// The printer escapes each control character to a one rune picture,
		// so the escaped text keeps one rune per source column and the wrap
		// falls at the same column in both.
		view := niceyaml.NewSourceFromString("k: \"\tx\x1by zz ww\"").View()
		p := testPrinter().With(printer.WithWrap(10))

		got := p.Print(view)
		require.Equal(t, stringtest.JoinLF(
			"k: \"␉x␛y",
			"zz ww\"",
		), got)

		l := p.Layout(view)

		assert.Equal(t, 2, l.Rows())
		assert.Equal(t, 0, l.RowOf(position.New(0, 4)))
		assert.Equal(t, 0, l.RowOf(position.New(0, 6)))
		assert.Equal(t, 0, l.RowOf(position.New(0, 8)))
		assert.Equal(t, 1, l.RowOf(position.New(0, 9)))
		assert.Equal(t, 1, l.RowOf(position.New(0, 14)))
	})

	t.Run("unicode spaces the wrapper drops keep columns aligned", func(t *testing.T) {
		t.Parallel()

		// The wrapper drops a Unicode space at a break as it drops an ASCII
		// one, so each separator column belongs to the row before the
		// break and the next word starts the next row.
		tcs := map[string]struct {
			content string
			want    string
			rows    map[int]int // Column to row.
		}{
			"ideographic space": {
				content: "k: aaaa\u3000bbbb\u3000cccc\u3000dddd",
				want:    stringtest.JoinLF("k: aaaa", "bbbb", "cccc", "dddd"),
				rows:    map[int]int{6: 0, 7: 0, 8: 1, 12: 1, 13: 2, 17: 2, 18: 3},
			},
			"em space": {
				content: "k: aaaa\u2003bbbb\u2003cccc\u2003dddd",
				want:    stringtest.JoinLF("k: aaaa", "bbbb", "cccc", "dddd"),
				rows:    map[int]int{6: 0, 7: 0, 8: 1, 12: 1, 13: 2, 17: 2, 18: 3},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				view := niceyaml.NewSourceFromString(tc.content).View()
				p := testPrinter().With(printer.WithWrap(7))

				require.Equal(t, tc.want, p.Print(view))

				l := p.Layout(view)

				assert.Equal(t, 4, l.Rows())

				for col, row := range tc.rows {
					assert.Equal(t, row, l.RowOf(position.New(0, col)), "column %d", col)
				}
			})
		}
	})

	t.Run("wide characters count two cells", func(t *testing.T) {
		t.Parallel()

		view := niceyaml.NewSourceFromString("k: 日本語").View()
		p := testPrinter()

		l := p.Layout(view)

		assert.Equal(t, 9, l.Width())
		assert.Equal(t, lipgloss.Width(p.Print(view)), l.Width())
		assert.Equal(t, 0, l.RowOf(position.New(0, 5)))
	})

	t.Run("a style that rewrites runes keeps columns aligned", func(t *testing.T) {
		t.Parallel()

		// A transform may rewrite every rune of the text it styles, as
		// strings.ToUpper does, or add text of its own, as brackets do.
		// Either way, each row still starts at the column of the content
		// it shows, and an annotation indents from the start of its row.
		brackets := func(s string) string { return "<" + s + ">" }

		tcs := map[string]struct {
			styles style.Styles
			below  []line.Annotation
			want   string
			rows   map[int]int // Column to row.
		}{
			"base style uppercases every run": {
				styles: style.New(lipgloss.NewStyle().Transform(strings.ToUpper)),
				want:   stringtest.JoinLF("KEY: AAAA", "BBBB CCCC", "DDDD"),
				rows:   map[int]int{0: 0, 8: 0, 9: 0, 10: 1, 19: 1, 20: 2, 1000: 2},
			},
			"key style uppercases the first run": {
				styles: style.New(
					lipgloss.NewStyle(),
					style.Set(kind.NameTag, lipgloss.NewStyle().Transform(strings.ToUpper)),
				),
				below: []line.Annotation{{Content: "x", Placement: line.Below, Col: 20}},
				want:  stringtest.JoinLF("KEY: aaaa", "bbbb cccc", "dddd", "^ x"),
				rows:  map[int]int{0: 0, 8: 0, 9: 0, 10: 1, 19: 1, 20: 2, 1000: 2},
			},
			"value style adds brackets": {
				styles: style.New(
					lipgloss.NewStyle(),
					style.Set(kind.LiteralString, lipgloss.NewStyle().Transform(brackets)),
				),
				want: stringtest.JoinLF("key: <aaaa", "bbbb cccc", "dddd>"),
				rows: map[int]int{0: 0, 10: 1, 20: 2},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				view := niceyaml.NewSourceFromString("key: aaaa bbbb cccc dddd").View()
				view.Annotate(0, tc.below...)

				p := printer.New(
					printer.WithGutter(printer.NoGutter),
					printer.WithContainerStyle(lipgloss.NewStyle()),
					printer.WithWrap(10),
					printer.WithStyles(tc.styles),
				)

				require.Equal(t, tc.want, p.Print(view))

				l := p.Layout(view)

				for col, row := range tc.rows {
					assert.Equal(t, row, l.RowOf(position.New(0, col)), "column %d", col)
				}
			})
		}
	})

	t.Run("a transform that adds runes of the text keeps columns aligned", func(t *testing.T) {
		t.Parallel()

		// The runes a transform adds may equal runes of the text it
		// styles, as the "v" and "a" of a "val=" prefix do for "value".
		// They still count as added runes, so every column of the text
		// maps to the rune it shows.
		tcs := map[string]struct {
			transform func(string) string
			content   string
			want      string
			rows      map[int]int // Column to row.
			cells     map[int]int // Column to cell.
			wrap      int
		}{
			"prefix that wraps": {
				transform: func(s string) string { return "val=" + s },
				content:   "k: value",
				wrap:      5,
				want:      stringtest.JoinLF("k:", "val=v", "alue"),
				rows:      map[int]int{0: 0, 3: 1, 4: 2, 7: 2},
				cells:     map[int]int{3: 0, 4: 0, 5: 1},
			},
			"prefix": {
				transform: func(s string) string { return "val=" + s },
				content:   "k: value",
				want:      "k: val=value",
				cells:     map[int]int{3: 3, 4: 8, 5: 9, 7: 11, 8: 12},
			},
			"quotes around a quoted string": {
				transform: func(s string) string { return `"` + s + `"` },
				content:   `k: "abc"`,
				want:      `k: ""abc""`,
				cells:     map[int]int{3: 3, 4: 5, 5: 6, 7: 8, 8: 10},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				view := niceyaml.NewSourceFromString(tc.content).View()

				p := printer.New(
					printer.WithGutter(printer.NoGutter),
					printer.WithContainerStyle(lipgloss.NewStyle()),
					printer.WithWrap(tc.wrap),
					printer.WithStyles(style.New(
						lipgloss.NewStyle(),
						style.Set(kind.LiteralString, lipgloss.NewStyle().Transform(tc.transform)),
					)),
				)

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

		widest := 0
		for row := range strings.SplitSeq(p.Print(view), "\n") {
			widest = max(widest, lipgloss.Width(row))
			assert.GreaterOrEqual(t, lipgloss.Width(row), l.GutterWidth())
		}

		assert.Equal(t, widest, l.Width())
		assert.LessOrEqual(t, l.Width(), 20)
		assert.Equal(t, 5, l.GutterWidth())
		assert.Equal(t, lipgloss.Width("   1 "), l.GutterWidth())
	})

	t.Run("wrapping off gives one content row per line", func(t *testing.T) {
		t.Parallel()

		view := newView()
		p := p.With(printer.WithWrap(0))

		got := p.Print(view)
		require.Equal(t, stringtest.JoinLF(
			"     a note above that wraps too",
			"   1 key: this is a long value that wraps",
			"          ^ below",
			"   2 b: 2",
			"   3 c: 3",
			"        ^ last",
		), got)

		l := p.Layout(view)

		assert.Equal(t, len(strings.Split(got, "\n")), l.Rows())
		assert.Equal(t, []int{3, 1, 2}, layoutRows(l))
		assert.Equal(t, 1, l.RowOf(position.New(0, 0)))
		assert.Equal(t, 1, l.RowOf(position.New(0, 1000)))
		assert.Equal(t, lipgloss.Width("   1 key: this is a long value that wraps"), l.Width())
	})

	t.Run("annotations off counts no annotation rows", func(t *testing.T) {
		t.Parallel()

		view := newView()
		p := p.With(printer.WithAnnotation(printer.NoAnnotation))

		got := p.Print(view)
		require.Equal(t, stringtest.JoinLF(
			"   1 key: this is a",
			"   - long value that",
			"   - wraps",
			"   2 b: 2",
			"   3 c: 3",
		), got)

		l := p.Layout(view)

		assert.Equal(t, len(strings.Split(got, "\n")), l.Rows())
		assert.Equal(t, []int{3, 1, 1}, layoutRows(l))
		assert.Equal(t, 0, l.RowOf(position.New(0, 0)))
		assert.Equal(t, 1, l.RowOf(position.New(0, 15)))
		assert.Equal(t, 3, l.RowOf(position.New(1, 0)))
	})
}

func TestPrinter_ContainerWidth(t *testing.T) {
	t.Parallel()

	// Without a container width the border box shrinks to the widest row, so
	// a window of short lines draws a narrower box than a window of long
	// ones. A container width pins the box to the same columns for both.
	src := niceyaml.NewSourceFromString("a: 1\nb: this value is much longer\nc: 3\n")

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

func TestPrinter_ContainerWidth_Accessor(t *testing.T) {
	t.Parallel()

	// A negative width is no width at all, so the box keeps shrinking to the
	// widest row.
	assert.Equal(t, 0, testPrinter().ContainerWidth())
	assert.Equal(t, 40, testPrinter().With(printer.WithContainerWidth(40)).ContainerWidth())
	assert.Equal(t, 0, testPrinter().With(printer.WithContainerWidth(-1)).ContainerWidth())

	// A negative count shows the error lines alone, as 0 does, so it
	// reads back as 0.
	assert.Equal(t, printer.DefaultContextLines, testPrinter().ContextLines())
	assert.Equal(t, 3, testPrinter().With(printer.WithContextLines(3)).ContextLines())
	assert.Equal(t, 0, testPrinter().With(printer.WithContextLines(-3)).ContextLines())
}

func TestPrinter_DefaultContextLines(t *testing.T) {
	t.Parallel()

	// The marked line sits in the middle of a source with more lines on
	// each side than the default shows, so the count changes the excerpt.
	source := niceyaml.NewSourceFromString("a: 1\nb: 2\nc: 3\nd: 4\ne: 5\nf: 6\ng: 7\nh: 8\ni: 9\n")
	bound := yamltest.Bind(t, source, niceyaml.NewError("bad", niceyaml.AtPath(paths.Root().Child("e"))))

	got := fmt.Sprintf("%+v", bound)

	assert.Equal(t, niceyaml.FormatError(bound, printer.DefaultContextLines), got)
	assert.NotEqual(t, niceyaml.FormatError(bound, printer.DefaultContextLines+1), got)
}

func TestPrinter_PrintError_JoinOfNothing(t *testing.T) {
	t.Parallel()

	// A bound join whose branches all carry nothing renders as an empty
	// tree and no excerpt, so the message stands in rather than nothing.
	// It prints as the tree of one node that holds the message, with
	// control characters as their pictures, as a plain error with the
	// same message prints.
	var nilErr *niceyaml.Error

	tcs := map[string]struct {
		name string
		want string
	}{
		"plain name": {
			name: "f.yaml",
			want: "f.yaml:\n",
		},
		"name with an escape sequence": {
			name: "f\x1b[31m.yaml",
			want: "f\u241b[31m.yaml:\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName(tc.name))
			err := source.Bind(errors.Join(nilErr, nilErr))
			require.Error(t, err)

			p := testPrinter()
			got := p.PrintError(err)

			assert.Equal(t, tc.want, got)
			assert.Equal(t, p.PrintError(errors.New(err.Error())), got)
			assert.NotContains(t, got, "\x1b")
		})
	}
}

func TestPrinter_AnnotationGutterSoftAcrossKinds(t *testing.T) {
	t.Parallel()

	// Every row after the first of a line's annotation block is a
	// continuation, whichever kind group it belongs to.
	view := niceyaml.NewSourceFromString("key: value\n").View()
	view.Annotate(0,
		line.Annotation{Content: "one", Placement: line.Below, Kind: kind.UIAnnotation},
		line.Annotation{Content: "two", Placement: line.Below, Kind: kind.TextError},
	)

	p := printer.New(
		printer.WithContainerStyle(lipgloss.NewStyle()),
		printer.WithGutter(printer.GutterFunc(func(ctx printer.GutterContext) string {
			switch {
			case !ctx.Annotation:
				return "L "
			case ctx.Soft:
				return "S "
			default:
				return "F "
			}
		})),
	)

	rows := strings.Split(p.Print(view), "\n")
	require.Len(t, rows, 3)
	assert.True(t, strings.HasPrefix(rows[0], "L "), rows[0])
	assert.True(t, strings.HasPrefix(rows[1], "F "), rows[1])
	assert.True(t, strings.HasPrefix(rows[2], "S "), rows[2])
}

func TestPrinter_AnnotationGutterSoftPerWrappedRow(t *testing.T) {
	t.Parallel()

	// The annotation rows below each wrapped row form a block of their
	// own, so the first row of each block is not a continuation.
	view := niceyaml.NewSourceFromString("key: aaaa bbbb cccc dddd\n").View()
	view.AddOverlay(kind.GenericError, position.NewRange(position.New(0, 5), position.New(0, 9)))
	view.AddOverlay(kind.GenericError, position.NewRange(position.New(0, 17), position.New(0, 19)))
	view.Annotate(0,
		line.Annotation{Placement: line.Below},
		line.Annotation{Content: "n", Placement: line.Below, Kind: kind.TextError, Col: 5},
	)

	p := testPrinterWithGutter(printer.GutterFunc(func(ctx printer.GutterContext) string {
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
	})).With(printer.WithWrap(12))

	var gutters []string

	for row := range strings.SplitSeq(p.Print(view), "\n") {
		gutters = append(gutters, row[:1])
	}

	assert.Equal(t, []string{"L", "F", "S", "C", "F", "C"}, gutters)
}

func TestColWidth(t *testing.T) {
	t.Parallel()

	// The width is that of the rendered row. A control character shows as
	// a one-cell picture, a wide rune takes two cells, and every rune of a
	// grapheme cluster after its first renders within the first.
	tcs := map[string]struct {
		content string
		col     int
		want    int
	}{
		"start":                      {content: "a: b", col: 0, want: 0},
		"negative clamps to start":   {content: "a: b", col: -1, want: 0},
		"before a control character": {content: "a: \"tab\there\"", col: 3, want: 3},
		"past a control character":   {content: "a: \"tab\there\"", col: 8, want: 8},
		"past the end":               {content: "a: b", col: 10, want: 10},
		"after wide runes":           {content: "k: 日本 x", col: 5, want: 7},
		"at a wide rune":             {content: "k: 日本 x", col: 4, want: 5},
		"at a combining mark":        {content: "k: éx", col: 4, want: 3},
		"after a combining mark":     {content: "k: éx", col: 5, want: 4},
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
		})
	}
}
