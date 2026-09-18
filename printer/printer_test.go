package printer_test

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
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
	"go.jacobcolvin.com/niceyaml/style/theme"
)

// testOverlayHighlight is a custom style.Kind constant for test highlights.
const testOverlayHighlight style.Kind = "testOverlayHighlight"

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
func testPrinterWithGutter(gutter printer.GutterFunc) *printer.Printer {
	return printer.New(
		printer.WithStyles(style.NewStyles(
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

	for i := range l.Len() {
		rows = append(rows, l.LineRows(i))
	}

	return rows
}

// printDiff generates a full-file diff between two YAML strings.
// It outputs the entire file with markers for inserted and deleted lines.
// Helper to replace the removed Printer.PrintTokenDiff method in tests.
func printDiff(p *printer.Printer, before, after string) string {
	beforeTks := niceyaml.NewSourceFromString(before, niceyaml.WithName("before"))
	afterTks := niceyaml.NewSourceFromString(after, niceyaml.WithName("after"))

	return p.Print(diff.Diff(beforeTks.Lines(), afterTks.Lines()).Unified())
}

// printDiffSummary generates a summary diff showing only changed lines with context.
// Helper to replace the removed Printer.PrintTokenDiffSummary method in tests.
func printDiffSummary(p *printer.Printer, before, after string, context int) string {
	beforeTks := niceyaml.NewSourceFromString(before, niceyaml.WithName("before"))
	afterTks := niceyaml.NewSourceFromString(after, niceyaml.WithName("after"))

	source := diff.Diff(beforeTks.Lines(), afterTks.Lines()).Hunks(context)

	if source.Len() == 0 {
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
	bound := source.Bind(niceyaml.NewError("bad", niceyaml.WithPath(paths.Root().Child("b"))))
	other := niceyaml.NewSourceFromString("c: 3\n")

	excerpt := stringtest.JoinLF(
		"<nameTag>a</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalNumberInteger>1</literalNumberInteger>",
		"<nameTag>b</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><genericError>2</genericError>",
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
		"bound error without a location": {
			err:  source.Bind(niceyaml.NewError("bad")),
			want: "bad",
		},
		"bound error with an empty message": {
			err:  source.Bind(niceyaml.NewErrorFrom(nil, niceyaml.WithPath(paths.Root().Child("b")))),
			want: "2:4: $.b:\n\n" + excerpt,
		},
		"joined bound errors print every excerpt": {
			err: errors.Join(
				fmt.Errorf("first: %w", bound),
				fmt.Errorf("second: %w", other.Bind(
					niceyaml.NewError("bad", niceyaml.WithPath(paths.Root().Child("c"))),
				)),
			),
			want: "first: 2:4: $.b: bad\nsecond: 1:4: $.c: bad\n\n" + excerpt + "\n\n" +
				"<nameTag>c</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><genericError>3</genericError>",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, p.PrintError(tc.err))
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
			wantOverlay: stringtest.JoinLF("[a][:][ ][b ][# c]", "[d][:][ ][e]"),
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
			for i := range view.Len() {
				view.AddOverlay(testOverlayHighlight, position.NewRange(
					position.New(i, 0),
					position.New(i, view.Line(i).Width()),
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

	// Empty file should produce empty output.
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

// errWriteFailed is the sentinel returned by failingWriter.
var errWriteFailed = errors.New("write failed")

// failingWriter always fails, for exercising Fprint error paths.
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
			// Default gutter adds line numbers, default style adds trailing padding.
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
		gutter printer.GutterFunc
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
			// Test that deleted lines appear inline where they were removed.
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
			// Test that modifications show delete before insert.
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
		gutter printer.GutterFunc
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

			p := testPrinterWithGutter(tc.gutter).With(printer.WithWidth(tc.width))

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

			p := testPrinterWithGutter(printer.LineNumberGutter).With(printer.WithWidth(tc.width))

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

func TestPrinter_WordWrap_WideLineNumbers(t *testing.T) {
	t.Parallel()

	// With more than 9999 lines the gutter grows by a column, and the wrap
	// width must shrink with it so no rendered row exceeds the width.
	input := strings.Repeat("k: v\n", 10000) + "last: this is a long value that wraps"
	source := niceyaml.NewSourceFromString(input)

	p := testPrinterWithGutter(printer.LineNumberGutter).With(printer.WithWidth(30))

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
		before       string
		after        string
		wantExact    string
		wantContains []string
		overlays     []position.Range
		width        int
	}{
		// Line 1 is the inserted line. The overlay covers "dddd", which
		// wraps to the third row.
		"overlay on a continuation row": {
			before:   "key: x\n",
			after:    "key: aaaa bbbb cccc dddd\n",
			overlays: []position.Range{position.NewRange(position.New(1, 20), position.New(1, 24))},
			width:    13,
			wantExact: stringtest.JoinLF(
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
			wantExact: stringtest.JoinLF(
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
			wantExact: stringtest.JoinLF(
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
			wantExact: stringtest.JoinLF(
				"-key: short",
				"+key: this is a very long",
				" value that should wrap",
			),
		},
		"wrapped diff continuation": {
			before: "key: original value\n",
			after:  "key: new very long value that definitely wraps\n",
			width:  25,
			wantExact: stringtest.JoinLF(
				"-key: original value",
				"+key: new very long value",
				" that definitely wraps",
			),
		},
		"modification with wrap": {
			before: "name: old-hyphenated-name-value\n",
			after:  "name: new-hyphenated-name-value\n",
			width:  20,
			wantExact: stringtest.JoinLF(
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

			p := testPrinterWithGutter(printer.DiffGutter).With(printer.WithWidth(tc.width))

			view := diff.Diff(
				niceyaml.NewSourceFromString(tc.before).Lines(),
				niceyaml.NewSourceFromString(tc.after).Lines(),
			).Unified()
			view.AddOverlay(testOverlayHighlight, tc.overlays...)

			got := p.Print(view)

			for row := range strings.SplitSeq(got, "\n") {
				assert.LessOrEqual(t, lipgloss.Width(row), tc.width, row)
			}

			if tc.wantExact != "" {
				assert.Equal(t, tc.wantExact, got)

				return
			}

			for _, want := range tc.wantContains {
				assert.Contains(t, got, want)
			}
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

	// Helper to create a gutter function with custom prefixes.
	makeGutter := func(inserted, deleted, equal string) printer.GutterFunc {
		return func(ctx printer.GutterContext) string {
			if ctx.Soft {
				return strings.Repeat(" ", len(equal))
			}

			switch ctx.Flag {
			case line.FlagInserted:
				return inserted
			case line.FlagDeleted:
				return deleted
			default:
				return equal
			}
		}
	}

	tcs := map[string]struct {
		gutterFunc printer.GutterFunc
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
				" key: old",
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
				" key: old",
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

func TestGutterFunctions(t *testing.T) {
	t.Parallel()

	styles := style.Styles{}

	tcs := map[string]struct {
		gutterFunc printer.GutterFunc
		want       string
		ctx        printer.GutterContext
	}{
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

			gutter := tc.gutterFunc
			got := gutter(tc.ctx)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPrinter_WithAnnotations(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		annotation string
		want       string
		enabled    bool
	}{
		"disabled annotations hides them": {
			enabled:    false,
			annotation: "test annotation",
			want:       "key: value",
		},
		"enabled annotations shows them": {
			enabled:    true,
			annotation: "@@ -1 +1 @@",
			want:       "@@ -1 +1 @@\nkey: value",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := niceyaml.NewSourceFromString("key: value\n").View()
			view.Annotate(0, line.Annotation{Content: tc.annotation})

			p := testPrinter().With(printer.WithAnnotations(tc.enabled))

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
		"below annotation with empty content renders no row": {
			input:     "key: value",
			lineIndex: 0,
			annotation: line.Annotation{
				Placement: line.Below,
				Col:       2,
			},
			want: "key: value",
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

			p := testPrinter().With(printer.WithAnnotations(false))

			got := p.Print(view)

			// With annotations disabled, only the content should be rendered.
			assert.Equal(t, "key: value", got)
		})
	}
}

func TestPrinter_Style(t *testing.T) {
	t.Parallel()

	base := lipgloss.NewStyle()

	tcs := map[string]struct {
		styles     style.Styles
		query      style.Kind
		wantBold   bool
		wantItalic bool
	}{
		"returns style from styles map": {
			styles: style.NewStyles(
				base,
				style.Set(style.NameTag, base.Bold(true)),
			),
			query:    style.NameTag,
			wantBold: true,
		},
		"child inherits from parent": {
			styles:     style.NewStyles(base.Italic(true)),
			query:      style.NameTag,
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

func TestPrinter_TokenTypes_XMLStyleGetter(t *testing.T) {
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
				"<nameTag>anchor</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><nameAnchor>&</nameAnchor><nameAnchor>x</nameAnchor><text> </text><literalNumberInteger>1</literalNumberInteger>",
				"<nameTag>alias</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><nameAlias>*</nameAlias><nameAlias>x</nameAlias>",
			),
		},
		"comment": {
			input: "key: value # comment",
			want:  "<nameTag>key</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value </literalString><comment># comment</comment>",
		},
		"tag": {
			input: "tagged: !custom value",
			want:  "<nameTag>tagged</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><nameDecorator>!custom </nameDecorator><literalString>value</literalString>",
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
		"punctuation": {
			input: "key: value",
			want:  "<nameTag>key</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
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
		before         string
		after          string
		context        int
		wantExact      string
		wantContains   []string
		wantNotContain []string
		wantEmpty      bool
	}{
		"no changes returns empty": {
			before:    "key: value\n",
			after:     "key: value\n",
			context:   1,
			wantEmpty: true,
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
			wantExact: stringtest.JoinLF(
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
			wantExact: stringtest.JoinLF(
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
			wantExact: stringtest.JoinLF(
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
			wantExact: stringtest.JoinLF(
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
			wantExact: stringtest.JoinLF(
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
			wantExact: stringtest.JoinLF(
				"      @@ -1,3 +1,2 @@",
				"   1  a: 1",
				"   2 -b: 2",
				"   2  c: 3",
			),
		},
		"empty files": {
			before:    "",
			after:     "",
			context:   1,
			wantEmpty: true,
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
			wantExact: stringtest.JoinLF(
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
			wantExact: stringtest.JoinLF(
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

			if tc.wantEmpty {
				assert.Empty(t, got)

				return
			}

			if tc.wantExact != "" {
				assert.Equal(t, tc.wantExact, got)

				return
			}

			for _, want := range tc.wantContains {
				assert.Contains(t, got, want)
			}

			for _, notWant := range tc.wantNotContain {
				assert.NotContains(t, got, notWant)
			}
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
			// Adjacent styled ranges naturally merge in the Printer.
			want: "f[oo]bar",
		},
		"multiple matches same line": {
			input:  "key: abcabc",
			search: "abc",
			// Adjacent styled ranges naturally merge in the Printer.
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
	narrow := base.With(printer.WithWidth(20))

	assert.Equal(t, 0, base.Width())
	assert.Equal(t, 20, narrow.Width())

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
				printer.WithWidth(40),
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

	// StyleWithTag creates a style that wraps content in XML-like tags.
	styleWithTag := func(tag string) lipgloss.Style {
		return lipgloss.NewStyle().Transform(func(s string) string {
			return "<" + tag + ">" + s + "</" + tag + ">"
		})
	}

	// OverlayRange defines an overlay kind and its range.
	type overlayRange struct {
		kind  style.Kind
		start position.Position
		end   position.Position
	}

	// Overlay kinds for the various test tags.
	const (
		kindHL   style.Kind = "kindHL"
		kindAll  style.Kind = "kindAll"
		kindA    style.Kind = "kindA"
		kindB    style.Kind = "kindB"
		kindC    style.Kind = "kindC"
		kindX    style.Kind = "kindX"
		kindY    style.Kind = "kindY"
		kindK    style.Kind = "kindK"
		kindVal  style.Kind = "kindVal"
		kindSpan style.Kind = "kindSpan"
	)

	// Overlay styler mapping kinds to tag-wrapped styles.
	testOverlayStyler := style.NewStyles(
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
			// Each token is styled separately, so transforms apply per-token.
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
			// Each token is styled separately.
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
			// Each token styled separately with overlapping transforms.
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
			// "abcdef" is a single token, styled character-by-character.
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

	// These tests exercise the color blending code paths in blendColors/blendStyles
	// by using actual lipgloss colors instead of transforms.

	type overlayDef struct {
		style lipgloss.Style
		start position.Position
		end   position.Position
	}

	// Overlay kinds for color blending tests.
	const (
		colorKind1 style.Kind = "colorKind1"
		colorKind2 style.Kind = "colorKind2"
		colorKind3 style.Kind = "colorKind3"
	)

	tcs := map[string]struct {
		input    string
		overlays []overlayDef
	}{
		"ForegroundBlend": {
			// Two ranges overlap - the overlapping region should blend colors via LAB.
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
			// Second style has NoColor - first color should be used directly.
			input: "key: value",
			overlays: []overlayDef{
				{lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000")), position.New(0, 0), position.New(0, 5)},
				{lipgloss.NewStyle(), position.New(0, 3), position.New(0, 10)},
			},
		},
		"SecondColorOnly": {
			// First style has NoColor - second color should be used.
			input: "key: value",
			overlays: []overlayDef{
				{lipgloss.NewStyle(), position.New(0, 0), position.New(0, 5)},
				{lipgloss.NewStyle().Foreground(lipgloss.Color("#0000FF")), position.New(0, 3), position.New(0, 10)},
			},
		},
		"BothNoColor": {
			// Both styles have NoColor - should result in nil (no color applied).
			input: "key: value",
			overlays: []overlayDef{
				{lipgloss.NewStyle(), position.New(0, 0), position.New(0, 5)},
				{lipgloss.NewStyle(), position.New(0, 3), position.New(0, 10)},
			},
		},
		"ThreeOverlapping": {
			// Three ranges overlap - all colors should blend together.
			input: "key: value",
			overlays: []overlayDef{
				{lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000")), position.New(0, 0), position.New(0, 6)},
				{lipgloss.NewStyle().Foreground(lipgloss.Color("#00FF00")), position.New(0, 2), position.New(0, 8)},
				{lipgloss.NewStyle().Foreground(lipgloss.Color("#0000FF")), position.New(0, 4), position.New(0, 10)},
			},
		},
		"MixedFgBg": {
			// Foreground and background colors should blend independently.
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
			kinds := []style.Kind{colorKind1, colorKind2, colorKind3}

			overlayOpts := make([]style.StylesOption, 0, len(tc.overlays))
			for i, od := range tc.overlays {
				overlayOpts = append(overlayOpts, style.Set(kinds[i], od.style))
				view.BlendOverlay(kinds[i], position.NewRange(od.start, od.end))
			}

			p := printer.New(
				printer.WithStyles(style.NewStyles(lipgloss.NewStyle(), overlayOpts...)),
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
		annotations line.Annotations
		position    line.Placement
		want        string
	}{
		"empty annotations": {
			annotations: line.Annotations{},
			position:    line.Below,
			want:        "",
		},
		"single below annotation": {
			annotations: line.Annotations{{Content: "error here", Placement: line.Below, Col: 0}},
			position:    line.Below,
			want:        "^ error here",
		},
		"single below annotation with padding": {
			annotations: line.Annotations{{Content: "error", Placement: line.Below, Col: 5}},
			position:    line.Below,
			want:        "     ^ error",
		},
		"empty content is left out of the column": {
			annotations: line.Annotations{
				{Placement: line.Below, Col: 0},
				{Content: "boom", Placement: line.Below, Col: 5},
			},
			position: line.Below,
			want:     "     ^ boom",
		},
		"single above annotation": {
			annotations: line.Annotations{{Content: "@@ hunk @@", Placement: line.Above, Col: 0}},
			position:    line.Above,
			want:        "@@ hunk @@",
		},
		"single above annotation with padding": {
			annotations: line.Annotations{{Content: "header", Placement: line.Above, Col: 3}},
			position:    line.Above,
			want:        "   header",
		},
		"multiple below annotations": {
			annotations: line.Annotations{
				{Content: "first", Placement: line.Below, Col: 0},
				{Content: "second", Placement: line.Below, Col: 5},
			},
			position: line.Below,
			want:     "^ first; second",
		},
		"multiple below annotations uses min col": {
			annotations: line.Annotations{
				{Content: "first", Placement: line.Below, Col: 5},
				{Content: "second", Placement: line.Below, Col: 2},
			},
			position: line.Below,
			want:     "  ^ first; second",
		},
		"multiple above annotations": {
			annotations: line.Annotations{
				{Content: "header1", Placement: line.Above, Col: 0},
				{Content: "header2", Placement: line.Above, Col: 0},
			},
			position: line.Above,
			want:     "header1; header2",
		},
		"empty content renders nothing": {
			annotations: line.Annotations{{Placement: line.Below, Col: 2}},
			position:    line.Below,
			want:        "",
		},
		"empty content is left out of the join": {
			annotations: line.Annotations{
				{Content: "first", Placement: line.Below, Col: 2},
				{Placement: line.Below, Col: 2},
				{Content: "third", Placement: line.Below, Col: 2},
			},
			position: line.Below,
			want:     "  ^ first; third",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fn := printer.DefaultAnnotation
			ctx := printer.AnnotationContext{
				Annotations: tc.annotations,
				Placement:   tc.position,
				Styles:      style.Styles{},
			}

			got := fn(ctx)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPrinter_WithAnnotationFunc(t *testing.T) {
	t.Parallel()

	// Custom annotation function that uses different prefixes.
	customAnnotation := func(ctx printer.AnnotationContext) string {
		if len(ctx.Annotations) == 0 {
			return ""
		}

		contents := ctx.Annotations.Contents()

		if ctx.Placement == line.Below {
			return ">>> " + strings.Join(contents, ", ")
		}

		return "=== " + strings.Join(contents, ", ")
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
				printer.WithAnnotationFunc(customAnnotation),
			)

			got := p.Print(view)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPrinter_AnnotationKind(t *testing.T) {
	t.Parallel()

	// Annotations of one kind share a row, and every kind renders as rows
	// of its own in its style. The zero kind renders as a comment.
	view := niceyaml.NewSourceFromString("key: value").View()
	view.Annotate(0,
		line.Annotation{Content: "hunk", Placement: line.Above},
		line.Annotation{Content: "bad key", Kind: style.TextError, Placement: line.Below, Col: 0},
		line.Annotation{Content: "note", Placement: line.Below, Col: 5},
		line.Annotation{Content: "bad value", Kind: style.TextError, Placement: line.Below, Col: 5},
	)

	p := printer.New(
		printer.WithStyles(yamltest.NewXMLStyles()),
		printer.WithContainerStyle(lipgloss.NewStyle()),
		printer.WithGutter(printer.NoGutter),
	)

	want := stringtest.JoinLF(
		"<comment>hunk</comment>",
		"<nameTag>key</nameTag><punctuationMappingValue>:</punctuationMappingValue><text> </text><literalString>value</literalString>",
		"<textError>^ bad key; bad value</textError>",
		"<comment>     ^ note</comment>",
	)

	assert.Equal(t, want, p.Print(view))
	assert.Equal(t, []int{4}, layoutRows(p.Layout(view)))
}

func TestPrinter_AnnotationFuncKeepsStyling(t *testing.T) {
	t.Parallel()

	view := niceyaml.NewSourceFromString("key: value").View()
	view.Annotate(0, line.Annotation{Content: "oops", Placement: line.Below})

	styles := style.NewStyles(lipgloss.NewStyle(), style.Set(style.TextError, lipgloss.NewStyle().Bold(true)))
	styled := func(ctx printer.AnnotationContext) string {
		return ctx.Styles.Style(style.TextError).Render(strings.Join(ctx.Annotations.Contents(), "; "))
	}

	p := printer.New(
		printer.WithStyles(styles),
		printer.WithContainerStyle(lipgloss.NewStyle()),
		printer.WithGutter(printer.NoGutter),
		printer.WithAnnotationFunc(styled),
	)

	// The styling the func applied reaches the output as escape sequences
	// rather than as control pictures.
	got := p.Print(view)
	assert.Contains(t, got, "\x1b[1moops")
	assert.NotContains(t, got, "\u241b")
}

func TestPrinter_AnnotationWrap(t *testing.T) {
	t.Parallel()

	// Joins the contents with no padding and no marker, so continuation
	// rows carry no indent.
	bare := func(ctx printer.AnnotationContext) string {
		return strings.Join(ctx.Annotations.Contents(), " ")
	}

	tcs := map[string]struct {
		annFunc    printer.AnnotationFunc
		gutter     printer.GutterFunc
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
				strings.Repeat(" ", 30)+"^ x",
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
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := niceyaml.NewSourceFromString(tc.input).View()
			view.Annotate(0, tc.annotation)

			p := testPrinterWithGutter(tc.gutter).With(printer.WithWidth(tc.width))
			if tc.annFunc != nil {
				p = p.With(printer.WithAnnotationFunc(tc.annFunc))
			}

			got := p.Print(view)
			assert.Equal(t, tc.want, got)
		})
	}
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
	view.Annotate(0, line.Annotation{Content: "note", Placement: line.Below, Col: 6})

	p := testPrinterWithGutter(printer.LineNumberGutter).With(printer.WithWidth(30))

	got := p.Print(view)
	for row := range strings.SplitSeq(got, "\n") {
		assert.LessOrEqual(t, lipgloss.Width(row), 30, row)
	}

	assert.Equal(t, stringtest.JoinLF(
		"10001 last: this is a long",
		"    - value that wraps",
		"            ^ note",
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
	// "blend a, then replace with b", which the key separators once spelled
	// the same way.
	const (
		ab style.Kind = "a!b"
		a  style.Kind = "a"
		b  style.Kind = "b"
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
		printer.WithStyles(style.NewStyles(
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
	const underlined style.Kind = "underlined"

	st := lipgloss.NewStyle().Underline(true).Bold(true)

	// Each token renders on its own, so the expected output styles them one
	// at a time.
	want := st.Render("k") + st.Render(":") + st.Render(" ") + st.Render("v")

	tcs := map[string]struct {
		blend bool
	}{
		"replace": {blend: false},
		"blend":   {blend: true},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := niceyaml.NewSourceFromString("k: v").View()
			view.AddLineOverlay(0, line.Overlay{Kind: underlined, Cols: position.NewSpan(0, 4), Blend: tc.blend})

			p := printer.New(
				printer.WithStyles(style.NewStyles(lipgloss.NewStyle(), style.Set(underlined, st))),
				printer.WithContainerStyle(lipgloss.NewStyle()),
				printer.WithGutter(printer.NoGutter),
			)

			assert.Equal(t, want, p.Print(view))
		})
	}
}

func TestPrinter_SeparatorStyle(t *testing.T) {
	t.Parallel()

	// The whitespace a token carries before its text renders in style.Text,
	// whatever kind of token follows it. Brackets mark the styled tokens.
	styles := style.NewStyles(
		lipgloss.NewStyle(),
		style.Set(style.LiteralString, testHighlightStyle()),
		style.Set(style.LiteralStringDouble, testHighlightStyle()),
		style.Set(style.Comment, testHighlightStyle()),
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
		gutter printer.GutterFunc
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
			view:   long.Slice(position.NewSpan(10000, long.Len())),
			gutter: printer.LineNumberGutter,
			want:   6,
		},
		"empty view": {
			view:   line.NewView(nil),
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
			if tc.view.Len() > 0 {
				first, _, _ := strings.Cut(p.Print(tc.view), "\n")
				assert.GreaterOrEqual(t, lipgloss.Width(first), got)
			}
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
}

func TestPrinter_Layout_Width(t *testing.T) {
	t.Parallel()

	wide := niceyaml.NewSourceFromString("k: " + strings.Repeat("\u65e5", 3) + "\nb: 2").View()

	annotated := niceyaml.NewSourceFromString("a: 1\nb: 2").View()
	annotated.Annotate(0, line.Annotation{Content: "a note wider than the lines", Placement: line.Above})

	tcs := map[string]struct {
		view   *line.View
		gutter printer.GutterFunc
		spans  []position.Span
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
			view:   line.NewView(nil),
			gutter: printer.DefaultGutter,
			want:   0,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := testPrinterWithGutter(tc.gutter)
			view := tc.view.Slice(tc.spans...)
			got := p.Layout(view).Width()

			assert.Equal(t, tc.want, got)

			// The width is the widest row Print renders.
			widest := 0
			for row := range strings.SplitSeq(p.Print(view), "\n") {
				widest = max(widest, lipgloss.Width(row))
			}

			if view.Len() > 0 {
				assert.Equal(t, got, widest)
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

	// A nil StyleGetter selects the default styles rather than panicking
	// in New.
	assert.Equal(t, printer.New().Print(view), printer.New(printer.WithStyles(nil)).Print(view))
}

func TestPrinter_WithAnnotationFunc_Nil(t *testing.T) {
	t.Parallel()

	view := niceyaml.NewSourceFromString("key: value").View()
	view.Annotate(0, line.Annotation{Content: "note", Placement: line.Below, Col: 5})

	// A nil AnnotationFunc selects DefaultAnnotation rather than panicking
	// on the first annotated line.
	p := testPrinterWithGutter(nil).With(printer.WithAnnotationFunc(nil))

	assert.Equal(t, "key: value\n     ^ note", p.Print(view))
}

func TestPrinter_Layout(t *testing.T) {
	t.Parallel()

	// Width 20 with a five column line number gutter leaves 15 columns of
	// content, so the first line wraps into three pieces that start at
	// columns 0, 15, and 31, and its annotation above wraps into two rows.
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
		"   - long value that",
		"   - wraps",
		"          ^ below",
		"   2 b: 2",
		"   3 c: 3",
		"        ^ last",
	)

	p := testPrinterWithGutter(printer.LineNumberGutter).With(printer.WithWidth(20))

	t.Run("rows match print", func(t *testing.T) {
		t.Parallel()

		view := newView()
		got := p.Print(view)
		require.Equal(t, want, got)

		l := p.Layout(view)

		assert.Equal(t, len(strings.Split(got, "\n")), l.Rows())
		assert.Equal(t, 3, l.Len())
		assert.Equal(t, []int{6, 1, 2}, layoutRows(l))
	})

	t.Run("line start is the prefix sum of line rows", func(t *testing.T) {
		t.Parallel()

		l := p.Layout(newView())

		start := 0
		for i := range l.Len() {
			assert.Equal(t, start, l.LineStart(i), "line %d", i)

			start += l.LineRows(i)
		}

		assert.Equal(t, start, l.Rows())
	})

	t.Run("line at maps every row back to its line", func(t *testing.T) {
		t.Parallel()

		l := p.Layout(newView())

		for i := range l.Len() {
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
		assert.Equal(t, 2, l.LineAt(l.Rows()))
		assert.Equal(t, 2, l.LineAt(l.Rows()+100))
	})

	t.Run("empty view", func(t *testing.T) {
		t.Parallel()

		l := p.Layout(line.NewView(nil))

		assert.Equal(t, 0, l.Rows())
		assert.Equal(t, 0, l.Len())
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
			"first column of the second piece":                    {pos: position.New(0, 15), want: 3},
			"space dropped at the second break":                   {pos: position.New(0, 30), want: 3},
			"first column of the third piece":                     {pos: position.New(0, 31), want: 4},
			"column past the end lands on the last content row":   {pos: position.New(0, 1000), want: 4},
			"negative column lands on the first content row":      {pos: position.New(0, -1), want: 2},
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

	t.Run("slice in a non-identity order", func(t *testing.T) {
		t.Parallel()

		view := newView().Slice(position.NewSpan(2, 3), position.NewSpan(0, 1))

		got := p.Print(view)
		require.Equal(t, stringtest.JoinLF(
			"   3 c: 3",
			"        ^ last",
			"     a note above",
			"     that wraps too",
			"   1 key: this is a",
			"   - long value that",
			"   - wraps",
			"          ^ below",
		), got)

		l := p.Layout(view)

		assert.Equal(t, len(strings.Split(got, "\n")), l.Rows())
		assert.Equal(t, []int{2, 6}, layoutRows(l))
		assert.Equal(t, 2, l.LineStart(1))

		// Lines are numbered as the slice orders them, not as the source does.
		assert.Equal(t, 0, l.LineAt(0))
		assert.Equal(t, 0, l.LineAt(1))
		assert.Equal(t, 1, l.LineAt(2))
		assert.Equal(t, 1, l.LineAt(7))
		assert.Equal(t, 1, l.LineAt(100))
		assert.Equal(t, 0, l.LineAt(-1))

		assert.Equal(t, 0, l.RowOf(position.New(0, 0)))
		assert.Equal(t, 4, l.RowOf(position.New(1, 0)))
		assert.Equal(t, 5, l.RowOf(position.New(1, 15)))
		assert.Equal(t, -1, l.RowOf(position.New(2, 0)))
	})

	t.Run("tabs and control characters keep columns aligned", func(t *testing.T) {
		t.Parallel()

		// The printer escapes each control character to a one rune picture,
		// so the escaped text keeps one rune per source column and the wrap
		// falls at the same column in both.
		view := niceyaml.NewSourceFromString("k: \"\tx\x1by zz ww\"").View()
		p := testPrinter().With(printer.WithWidth(10))

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

	t.Run("wide characters count two cells", func(t *testing.T) {
		t.Parallel()

		view := niceyaml.NewSourceFromString("k: 日本語").View()
		p := testPrinter()

		l := p.Layout(view)

		assert.Equal(t, 9, l.Width())
		assert.Equal(t, lipgloss.Width(p.Print(view)), l.Width())
		assert.Equal(t, 0, l.RowOf(position.New(0, 5)))
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
		p := p.With(printer.WithWidth(0))

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
		p := p.With(printer.WithAnnotations(false))

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
