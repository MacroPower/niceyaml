package niceyaml_test

import (
	"os"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/golden"
	"github.com/goccy/go-yaml/lexer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"jacobcolvin.com/niceyaml"
	"jacobcolvin.com/niceyaml/internal/yamltest"
	"jacobcolvin.com/niceyaml/line"
	"jacobcolvin.com/niceyaml/position"
	"jacobcolvin.com/niceyaml/style"
	"jacobcolvin.com/niceyaml/style/theme"
)

// testOverlayHighlight is a custom style.Style constant for test highlights.
const testOverlayHighlight style.Style = iota

// testHighlightStyle returns a style that wraps content in brackets for easy verification.
func testHighlightStyle() *lipgloss.Style {
	s := lipgloss.NewStyle().Transform(func(str string) string {
		return "[" + str + "]"
	})

	return &s
}

// testPrinter returns a printer without styles or padding for predictable output.
func testPrinter() *niceyaml.Printer {
	return testPrinterWithGutter(niceyaml.NoGutter())
}

// testPrinterWithGutter returns a printer without styles but with a custom gutter.
func testPrinterWithGutter(gutter niceyaml.GutterFunc) *niceyaml.Printer {
	return niceyaml.NewPrinter(
		niceyaml.WithStyles(style.NewStyles(
			lipgloss.NewStyle(),
			style.Set(testOverlayHighlight, *testHighlightStyle()),
		)),
		niceyaml.WithStyle(lipgloss.NewStyle()),
		niceyaml.WithGutter(gutter),
	)
}

// printDiff generates a full-file diff between two YAML strings.
// It outputs the entire file with markers for inserted and deleted lines.
// Helper to replace the removed Printer.PrintTokenDiff method in tests.
func printDiff(p *niceyaml.Printer, before, after string) string {
	beforeTks := niceyaml.NewSourceFromString(before, niceyaml.WithName("before"))
	afterTks := niceyaml.NewSourceFromString(after, niceyaml.WithName("after"))

	return p.Print(niceyaml.Diff(
		niceyaml.NewRevision(beforeTks),
		niceyaml.NewRevision(afterTks),
	).Full())
}

// printDiffSummary generates a summary diff showing only changed lines with context.
// Helper to replace the removed Printer.PrintTokenDiffSummary method in tests.
func printDiffSummary(p *niceyaml.Printer, before, after string, context int) string {
	beforeTks := niceyaml.NewSourceFromString(before, niceyaml.WithName("before"))
	afterTks := niceyaml.NewSourceFromString(after, niceyaml.WithName("after"))

	source, ranges := niceyaml.Diff(
		niceyaml.NewRevision(beforeTks),
		niceyaml.NewRevision(afterTks),
	).Hunks(context)

	if source.IsEmpty() {
		return ""
	}

	return p.Print(source, ranges...)
}

// testFinder returns a Finder configured for testing.
func testFinder(normalizer niceyaml.Normalizer) *niceyaml.Finder {
	var opts []niceyaml.FinderOption
	if normalizer != nil {
		opts = append(opts, niceyaml.WithNormalizer(normalizer))
	}

	return niceyaml.NewFinder(opts...)
}

func TestPrinter_Anchor(t *testing.T) {
	t.Parallel()

	input := yamltest.Input(`
		anchor: &x 1
		alias: *x
	`)
	tks := lexer.Tokenize(input)

	p := testPrinter()

	got := p.Print(niceyaml.NewSourceFromTokens(tks))
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
			input: yamltest.JoinLF(
				"first: 1",
				"second: 2",
			),
			want: yamltest.JoinLF(
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

			tks := lexer.Tokenize(tc.input)
			source := niceyaml.NewSourceFromTokens(tks)
			source.AddOverlay(testOverlayHighlight, tc.rng)

			p := testPrinter()

			got := p.Print(source)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPrinter_ClearOverlays(t *testing.T) {
	t.Parallel()

	input := "key: value"
	tks := lexer.Tokenize(input)

	source := niceyaml.NewSourceFromTokens(tks)
	source.AddOverlay(
		testOverlayHighlight,
		position.NewRange(position.New(0, 0), position.New(0, 3)),
	)
	source.ClearOverlays()

	p := testPrinter()

	// After clearing, no styles should be applied.
	assert.Equal(t, "key: value", p.Print(source))
}

func TestPrinter_PrintTokens_EmptyFile(t *testing.T) {
	t.Parallel()

	// Tokenize an empty string to simulate an empty YAML file.
	tks := lexer.Tokenize("")

	p := testPrinter()
	got := p.Print(niceyaml.NewSourceFromTokens(tks))

	// Empty file should produce empty output.
	assert.Empty(t, got)
}

func TestNewPrinter(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  string
		opts  []niceyaml.PrinterOption
	}{
		"custom styles": {
			input: "key: value",
			want:  "<name-tag>key</name-tag><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><literal-string>value</literal-string>",
			opts: []niceyaml.PrinterOption{
				niceyaml.WithStyles(yamltest.NewXMLStyles()),
				niceyaml.WithStyle(lipgloss.NewStyle()),
				niceyaml.WithGutter(niceyaml.NoGutter()),
			},
		},
		"xml styles with token types": {
			input: yamltest.Input(`
				key: value
				number: 42
				bool: true
				# comment
			`),
			want: yamltest.JoinLF(
				"<name-tag>key</name-tag><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><literal-string>value</literal-string>",
				"<name-tag>number</name-tag><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><literal-number-integer>42</literal-number-integer>",
				"<name-tag>bool</name-tag><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><literal-boolean>true</literal-boolean>",
				"<comment># comment</comment>",
			),
			opts: []niceyaml.PrinterOption{
				niceyaml.WithStyles(yamltest.NewXMLStyles()),
				niceyaml.WithStyle(lipgloss.NewStyle()),
				niceyaml.WithGutter(niceyaml.NoGutter()),
			},
		},
		"empty styles": {
			input: "key: value",
			want:  "   1  key: value ",
			// Default gutter adds line numbers, default style adds trailing padding.
			opts: []niceyaml.PrinterOption{niceyaml.WithStyles(style.Styles{})},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := lexer.Tokenize(tc.input)
			p := niceyaml.NewPrinter(tc.opts...)
			got := p.Print(niceyaml.NewSourceFromTokens(tks))

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
			input: yamltest.JoinLF(
				"key: value",
				"number: 42",
			),
			want: yamltest.JoinLF(
				"   1 key: value",
				"   2 number: 42",
			),
		},
		"multi-line value": {
			input: yamltest.JoinLF(
				"key: |",
				"  line1",
				"  line2",
			),
			want: yamltest.JoinLF(
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

			p := testPrinterWithGutter(niceyaml.LineNumberGutter())

			got := p.Print(niceyaml.NewSourceFromTokens(tks))
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPrinter_PrintSlice(t *testing.T) {
	t.Parallel()

	input := yamltest.Input(`
		first: 1
		second: 2
		third: 3
		fourth: 4
		fifth: 5
	`)

	tcs := map[string]struct {
		gutter niceyaml.GutterFunc
		want   string
		spans  position.Spans
	}{
		"full range": {
			spans:  nil, // Empty variadic prints all lines.
			gutter: niceyaml.NoGutter(),
			want: yamltest.JoinLF(
				"first: 1",
				"second: 2",
				"third: 3",
				"fourth: 4",
				"fifth: 5",
			),
		},
		"full range with line numbers": {
			spans:  nil,
			gutter: niceyaml.LineNumberGutter(),
			want: yamltest.JoinLF(
				"   1 first: 1",
				"   2 second: 2",
				"   3 third: 3",
				"   4 fourth: 4",
				"   5 fifth: 5",
			),
		},
		"bounded middle": {
			spans:  position.Spans{position.NewSpan(1, 4)},
			gutter: niceyaml.NoGutter(),
			want: yamltest.JoinLF(
				"second: 2",
				"third: 3",
				"fourth: 4",
			),
		},
		"bounded middle with line numbers": {
			spans:  position.Spans{position.NewSpan(1, 4)},
			gutter: niceyaml.LineNumberGutter(),
			want: yamltest.JoinLF(
				"   2 second: 2",
				"   3 third: 3",
				"   4 fourth: 4",
			),
		},
		"from start": {
			spans:  position.Spans{position.NewSpan(0, 2)},
			gutter: niceyaml.NoGutter(),
			want: yamltest.JoinLF(
				"first: 1",
				"second: 2",
			),
		},
		"to end": {
			spans:  position.Spans{position.NewSpan(3, 5)},
			gutter: niceyaml.NoGutter(),
			want: yamltest.JoinLF(
				"fourth: 4",
				"fifth: 5",
			),
		},
		"single line": {
			spans:  position.Spans{position.NewSpan(2, 3)},
			gutter: niceyaml.NoGutter(),
			want:   "third: 3",
		},
		"single line with line numbers": {
			spans:  position.Spans{position.NewSpan(2, 3)},
			gutter: niceyaml.LineNumberGutter(),
			want:   "   3 third: 3",
		},
		"empty result": {
			spans:  position.Spans{position.NewSpan(10, 21)},
			gutter: niceyaml.NoGutter(),
			want:   "",
		},
		"two disjoint spans": {
			spans: position.Spans{
				position.NewSpan(0, 1),
				position.NewSpan(3, 5),
			},
			gutter: niceyaml.NoGutter(),
			want: yamltest.JoinLF(
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
			gutter: niceyaml.LineNumberGutter(),
			want: yamltest.JoinLF(
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
			gutter: niceyaml.NoGutter(),
			want: yamltest.JoinLF(
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
			gutter: niceyaml.NoGutter(),
			want: yamltest.JoinLF(
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

			got := p.Print(lines, tc.spans...)
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
			after: yamltest.JoinLF(
				"key: value",
				"new: line",
				"",
			),
			want: yamltest.JoinLF(
				"   1  key: value",
				"   2 +new: line",
			),
		},
		"simple deletion": {
			before: yamltest.JoinLF(
				"key: value",
				"old: line",
				"",
			),
			after: "key: value\n",
			want: yamltest.JoinLF(
				"   1  key: value",
				"   2 -old: line",
			),
		},
		"modification": {
			before: "key: old\n",
			after:  "key: new\n",
			want: yamltest.JoinLF(
				"   1 -key: old",
				"   1 +key: new",
			),
		},
		"addition with context": {
			before: yamltest.JoinLF(
				"line1: a",
				"line3: c",
				"",
			),
			after: yamltest.JoinLF(
				"line1: a",
				"line2: b",
				"line3: c",
				"",
			),
			want: yamltest.JoinLF(
				"   1  line1: a",
				"   2 +line2: b",
				"   3  line3: c",
			),
		},
		"deletion with context": {
			before: yamltest.JoinLF(
				"line1: a",
				"line2: b",
				"line3: c",
				"",
			),
			after: yamltest.JoinLF(
				"line1: a",
				"line3: c",
				"",
			),
			want: yamltest.JoinLF(
				"   1  line1: a",
				"   2 -line2: b",
				"   2  line3: c",
			),
		},
		"multiline yaml modification": {
			before: yamltest.JoinLF(
				"apiVersion: v1",
				"kind: Pod",
				"metadata:",
				"  name: test",
				"",
			),
			after: yamltest.JoinLF(
				"apiVersion: v1",
				"kind: Pod",
				"metadata:",
				"  name: modified",
				"  labels:",
				"    app: test",
				"",
			),
			want: yamltest.JoinLF(
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
			before: yamltest.JoinLF(
				"a: 1",
				"b: 2",
				"c: 3",
				"d: 4",
				"",
			),
			after: yamltest.JoinLF(
				"a: 1",
				"b: changed",
				"c: 3",
				"d: 4",
				"e: 5",
				"",
			),
			want: yamltest.JoinLF(
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
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := niceyaml.NewPrinter(
				niceyaml.WithStyles(style.Styles{}),
				niceyaml.WithStyle(lipgloss.NewStyle()),
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
			before: yamltest.JoinLF(
				"a: 1",
				"b: 2",
				"c: 3",
				"",
			),
			after: yamltest.JoinLF(
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

			p := testPrinterWithGutter(niceyaml.DiffGutter())
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
		gutter   niceyaml.GutterFunc
		input    string
		want     string
		width    int
		wordWrap bool
	}{
		"no wrap when width is zero": {
			input:    "key: value",
			width:    0,
			gutter:   niceyaml.NoGutter(),
			wordWrap: true,
			want:     "key: value",
		},
		"simple wrap": {
			input:    "key: this is a very long value that should wrap",
			width:    20,
			gutter:   niceyaml.NoGutter(),
			wordWrap: true,
			want: yamltest.JoinLF(
				"key: this is a very",
				"long value that",
				"should wrap",
			),
		},
		"wrap on slash": {
			input:    "path: /usr/local/bin/something",
			width:    20,
			gutter:   niceyaml.NoGutter(),
			wordWrap: true,
			want: yamltest.JoinLF(
				"path: /usr/local/",
				"bin/something",
			),
		},
		"wrap on hyphen": {
			input:    "name: very-long-hyphenated-name",
			width:    20,
			gutter:   niceyaml.NoGutter(),
			wordWrap: true,
			want: yamltest.JoinLF(
				"name: very-long-",
				"hyphenated-name",
			),
		},
		"short content no wrap": {
			input:    "key: value",
			width:    50,
			gutter:   niceyaml.NoGutter(),
			wordWrap: true,
			want:     "key: value",
		},
		"multi-line content": {
			input: yamltest.JoinLF(
				"key: value",
				"another: long value that should wrap here",
			),
			width:    20,
			gutter:   niceyaml.NoGutter(),
			wordWrap: true,
			want: yamltest.JoinLF(
				"key: value",
				"another: long value",
				"that should wrap",
				"here",
			),
		},
		// Line number gutter tests.
		"wrapped line continuation marker": {
			input:    "key: this is a very long value",
			width:    22,
			gutter:   niceyaml.LineNumberGutter(),
			wordWrap: true,
			// Wraps at word boundaries within width.
			// Width 22 - 5 (line number gutter) = 17 for content.
			want: yamltest.JoinLF(
				"   1 key: this is a",
				"   - very long value",
			),
		},
		"multiple wrapped lines": {
			input: yamltest.JoinLF(
				"first: short",
				"second: this is a very long line that wraps",
			),
			width:    30,
			gutter:   niceyaml.LineNumberGutter(),
			wordWrap: true,
			// First line fits, second line wraps.
			// Width 30 - 5 (line number gutter) = 25 for content.
			want: yamltest.JoinLF(
				"   1 first: short",
				"   2 second: this is a very",
				"   - long line that wraps",
			),
		},
		// SetWordWrap(false) tests.
		"wordWrap disabled with NoGutter": {
			input:    "key: this is a very long value that should not wrap",
			width:    25,
			gutter:   niceyaml.NoGutter(),
			wordWrap: false,
			want:     "key: this is a very long value that should not wrap",
		},
		"wordWrap disabled with LineNumberGutter": {
			input:    "key: this is a very long value that should not wrap",
			width:    30,
			gutter:   niceyaml.LineNumberGutter(),
			wordWrap: false,
			want:     "   1 key: this is a very long value that should not wrap",
		},
		"wordWrap disabled with DefaultGutter": {
			input:    "key: this is a very long value that should not wrap",
			width:    30,
			gutter:   niceyaml.DefaultGutter(),
			wordWrap: false,
			want:     "   1  key: this is a very long value that should not wrap",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := lexer.Tokenize(tc.input)

			p := testPrinterWithGutter(tc.gutter)
			p.SetWidth(tc.width)
			p.SetWordWrap(tc.wordWrap)

			got := p.Print(niceyaml.NewSourceFromTokens(tks))
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPrinter_PrintTokenDiff_WithWordWrap(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		before       string
		after        string
		wantExact    string
		wantContains []string
		width        int
	}{
		"diff lines wrap correctly": {
			before: "key: short\n",
			after:  "key: this is a very long value that should wrap\n",
			width:  30,
			wantExact: yamltest.JoinLF(
				"-key: short",
				"+key: this is a very long",
				" value that should wrap",
			),
		},
		"wrapped diff continuation": {
			before: "key: original value\n",
			after:  "key: new very long value that definitely wraps\n",
			width:  25,
			wantExact: yamltest.JoinLF(
				"-key: original value",
				"+key: new very long value",
				" that definitely wraps",
			),
		},
		"modification with wrap": {
			before: "name: old-hyphenated-name-value\n",
			after:  "name: new-hyphenated-name-value\n",
			width:  20,
			wantExact: yamltest.JoinLF(
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

			p := testPrinterWithGutter(niceyaml.DiffGutter())
			p.SetWidth(tc.width)

			got := printDiff(p, tc.before, tc.after)

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
			before: yamltest.JoinLF(
				"key: value",
				"old: line",
				"",
			),
			after: yamltest.JoinLF(
				"key: value",
				"new: line",
				"",
			),
			want: yamltest.JoinLF(
				"   1  key: value",
				"   2 -old: line",
				"   2 +new: line",
			),
		},
		"addition shows afterLine numbers": {
			// Equal lines show afterLine, inserted line shows afterLine.
			before: yamltest.JoinLF(
				"a: 1",
				"c: 3",
				"",
			),
			after: yamltest.JoinLF(
				"a: 1",
				"b: 2",
				"c: 3",
				"",
			),
			want: yamltest.JoinLF(
				"   1  a: 1",
				"   2 +b: 2",
				"   3  c: 3",
			),
		},
		"deletion shows beforeLine numbers": {
			// Equal lines show afterLine, deleted line shows beforeLine.
			before: yamltest.JoinLF(
				"a: 1",
				"b: 2",
				"c: 3",
				"",
			),
			after: yamltest.JoinLF(
				"a: 1",
				"c: 3",
				"",
			),
			want: yamltest.JoinLF(
				"   1  a: 1",
				"   2 -b: 2",
				"   2  c: 3",
			),
		},
		"multiple changes track line numbers correctly": {
			before: yamltest.JoinLF(
				"line1: a",
				"line2: b",
				"line3: c",
				"",
			),
			after: yamltest.JoinLF(
				"line1: x",
				"line2: b",
				"line3: y",
				"",
			),
			want: yamltest.JoinLF(
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

			p := niceyaml.NewPrinter(
				niceyaml.WithStyles(style.Styles{}),
				niceyaml.WithStyle(lipgloss.NewStyle()),
			)

			got := printDiff(p, tc.before, tc.after)

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPrinter_PrintTokenDiff_CustomGutter(t *testing.T) {
	t.Parallel()

	// Helper to create a gutter function with custom prefixes.
	makeGutter := func(inserted, deleted, equal string) niceyaml.GutterFunc {
		return func(ctx niceyaml.GutterContext) string {
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
		gutterFunc niceyaml.GutterFunc
		before     string
		after      string
		want       string
	}{
		"custom inserted prefix": {
			gutterFunc: makeGutter(">>", "-", " "),
			before:     "key: old\n",
			after: yamltest.JoinLF(
				"key: old",
				"new: line",
				"",
			),
			want: yamltest.JoinLF(
				" key: old",
				">>new: line",
			),
		},
		"custom deleted prefix": {
			gutterFunc: makeGutter("+", "<<", " "),
			before: yamltest.JoinLF(
				"key: old",
				"old: line",
				"",
			),
			after: "key: old\n",
			want: yamltest.JoinLF(
				" key: old",
				"<<old: line",
			),
		},
		"both custom prefixes": {
			gutterFunc: makeGutter("ADD:", "DEL:", "    "),
			before:     "key: old\n",
			after:      "key: new\n",
			want: yamltest.JoinLF(
				"DEL:key: old",
				"ADD:key: new",
			),
		},
		"no gutter": {
			gutterFunc: niceyaml.NoGutter(),
			before:     "a: 1\n",
			after:      "a: 2\n",
			want: yamltest.JoinLF(
				"a: 1",
				"a: 2",
			),
		},
		"multi-character prefixes with context": {
			gutterFunc: makeGutter("[+]", "[-]", "   "),
			before: yamltest.JoinLF(
				"line1: a",
				"line2: b",
				"line3: c",
				"",
			),
			after: yamltest.JoinLF(
				"line1: a",
				"line2: x",
				"line3: c",
				"",
			),
			want: yamltest.JoinLF(
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
		gutterFunc func() niceyaml.GutterFunc
		want       string
		ctx        niceyaml.GutterContext
	}{
		// DiffGutter tests.
		"diff/default flag": {
			gutterFunc: niceyaml.DiffGutter,
			ctx:        niceyaml.GutterContext{Flag: line.FlagDefault, Styles: styles},
			want:       " ",
		},
		"diff/inserted flag": {
			gutterFunc: niceyaml.DiffGutter,
			ctx:        niceyaml.GutterContext{Flag: line.FlagInserted, Styles: styles},
			want:       "+",
		},
		"diff/deleted flag": {
			gutterFunc: niceyaml.DiffGutter,
			ctx:        niceyaml.GutterContext{Flag: line.FlagDeleted, Styles: styles},
			want:       "-",
		},
		"diff/soft wrap default": {
			gutterFunc: niceyaml.DiffGutter,
			ctx:        niceyaml.GutterContext{Flag: line.FlagDefault, Soft: true, Styles: styles},
			want:       " ",
		},
		"diff/soft wrap inserted": {
			gutterFunc: niceyaml.DiffGutter,
			ctx:        niceyaml.GutterContext{Flag: line.FlagInserted, Soft: true, Styles: styles},
			want:       " ",
		},
		"diff/soft wrap deleted": {
			gutterFunc: niceyaml.DiffGutter,
			ctx:        niceyaml.GutterContext{Flag: line.FlagDeleted, Soft: true, Styles: styles},
			want:       " ",
		},
		// DefaultGutter tests.
		"default/annotation flag renders empty": {
			gutterFunc: niceyaml.DefaultGutter,
			ctx:        niceyaml.GutterContext{Flag: line.FlagAnnotation, Number: 1, Styles: styles},
			want:       "      ",
		},
		"default/soft wrap renders continuation marker": {
			gutterFunc: niceyaml.DefaultGutter,
			ctx:        niceyaml.GutterContext{Flag: line.FlagDefault, Soft: true, Styles: styles},
			want:       "   -  ",
		},
		"default/normal line renders line number": {
			gutterFunc: niceyaml.DefaultGutter,
			ctx:        niceyaml.GutterContext{Flag: line.FlagDefault, Number: 1, Styles: styles},
			want:       "   1  ",
		},
		"default/inserted flag renders + marker": {
			gutterFunc: niceyaml.DefaultGutter,
			ctx:        niceyaml.GutterContext{Flag: line.FlagInserted, Number: 1, Styles: styles},
			want:       "   1 +",
		},
		"default/deleted flag renders - marker": {
			gutterFunc: niceyaml.DefaultGutter,
			ctx:        niceyaml.GutterContext{Flag: line.FlagDeleted, Number: 1, Styles: styles},
			want:       "   1 -",
		},
		// LineNumberGutter tests.
		"lineNumber/annotation flag renders empty": {
			gutterFunc: niceyaml.LineNumberGutter,
			ctx:        niceyaml.GutterContext{Flag: line.FlagAnnotation, Number: 1, Styles: styles},
			want:       "     ",
		},
		"lineNumber/soft wrap renders continuation marker": {
			gutterFunc: niceyaml.LineNumberGutter,
			ctx:        niceyaml.GutterContext{Flag: line.FlagDefault, Soft: true, Styles: styles},
			want:       "   - ",
		},
		"lineNumber/normal line renders line number": {
			gutterFunc: niceyaml.LineNumberGutter,
			ctx:        niceyaml.GutterContext{Flag: line.FlagDefault, Number: 1, Styles: styles},
			want:       "   1 ",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			gutter := tc.gutterFunc()
			got := gutter(tc.ctx)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPrinter_SetAnnotationsEnabled(t *testing.T) {
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

			input := "key: value\n"
			source := niceyaml.NewSourceFromString(input)
			source.Line(0).AddAnnotation(line.Annotation{Content: tc.annotation})

			p := testPrinter()
			p.SetAnnotationsEnabled(tc.enabled)

			got := p.Print(source)

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
				Content:  "# comment",
				Position: line.Above,
				Col:      0,
			},
			want: "# comment\nkey: value",
		},
		"above annotation with col padding": {
			input:     "key: value",
			lineIndex: 0,
			annotation: line.Annotation{
				Content:  "^-- error here",
				Position: line.Above,
				Col:      5,
			},
			want: "     ^-- error here\nkey: value",
		},
		"below annotation at col 0": {
			input:     "key: value",
			lineIndex: 0,
			annotation: line.Annotation{
				Content:  "comment below",
				Position: line.Below,
				Col:      0,
			},
			want: "key: value\n^ comment below",
		},
		"below annotation with col padding": {
			input:     "key: value",
			lineIndex: 0,
			annotation: line.Annotation{
				Content:  "error here",
				Position: line.Below,
				Col:      5,
			},
			want: "key: value\n     ^ error here",
		},
		"below annotation on second line": {
			input: yamltest.JoinLF(
				"first: 1",
				"second: 2",
			),
			lineIndex: 1,
			annotation: line.Annotation{
				Content:  "note",
				Position: line.Below,
				Col:      8,
			},
			want: yamltest.JoinLF(
				"first: 1",
				"second: 2",
				"        ^ note",
			),
		},
		"above annotation on second line": {
			input: yamltest.JoinLF(
				"first: 1",
				"second: 2",
			),
			lineIndex: 1,
			annotation: line.Annotation{
				Content:  "@@ -1 +1 @@",
				Position: line.Above,
				Col:      0,
			},
			want: yamltest.JoinLF(
				"first: 1",
				"@@ -1 +1 @@",
				"second: 2",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)
			source.Line(tc.lineIndex).AddAnnotation(tc.annotation)

			p := testPrinter()
			got := p.Print(source)

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
				Content:  "@@ -1 +1 @@",
				Position: line.Above,
				Col:      0,
			},
			want: "     @@ -1 +1 @@\n   1 key: value",
		},
		"below annotation with line numbers": {
			input:     "key: value",
			lineIndex: 0,
			annotation: line.Annotation{
				Content:  "error",
				Position: line.Below,
				Col:      5,
			},
			want: "   1 key: value\n          ^ error",
		},
		"below annotation with col padding and line numbers": {
			input: yamltest.JoinLF(
				"first: 1",
				"second: 2",
			),
			lineIndex: 0,
			annotation: line.Annotation{
				Content:  "note",
				Position: line.Below,
				Col:      7,
			},
			want: yamltest.JoinLF(
				"   1 first: 1",
				"            ^ note",
				"   2 second: 2",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)
			source.Line(tc.lineIndex).AddAnnotation(tc.annotation)

			p := testPrinterWithGutter(niceyaml.LineNumberGutter())
			got := p.Print(source)

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
				Content:  "# hidden above",
				Position: line.Above,
				Col:      0,
			},
		},
		"below annotation disabled": {
			annotation: line.Annotation{
				Content:  "# hidden below",
				Position: line.Below,
				Col:      5,
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			input := "key: value"
			source := niceyaml.NewSourceFromString(input)
			source.Line(0).AddAnnotation(tc.annotation)

			p := testPrinter()
			p.SetAnnotationsEnabled(false)

			got := p.Print(source)

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
		query      style.Style
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

			p := niceyaml.NewPrinter(niceyaml.WithStyles(tc.styles))
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
			want:  "<name-tag>key</name-tag><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><literal-string>value</literal-string>",
		},
		"null types": {
			input: yamltest.JoinLF(
				"null: null",
				"tilde: ~",
			),
			want: yamltest.JoinLF(
				"<name-tag>null</name-tag><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><literal-null>null</literal-null>",
				"<name-tag>tilde</name-tag><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><literal-null>~</literal-null>",
			),
		},
		"boolean types": {
			input: yamltest.JoinLF(
				"yes: true",
				"no: false",
			),
			want: yamltest.JoinLF(
				"<name-tag>yes</name-tag><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><literal-boolean>true</literal-boolean>",
				"<name-tag>no</name-tag><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><literal-boolean>false</literal-boolean>",
			),
		},
		"number types": {
			input: yamltest.JoinLF(
				"int: 42",
				"float: 3.14",
			),
			want: yamltest.JoinLF(
				"<name-tag>int</name-tag><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><literal-number-integer>42</literal-number-integer>",
				"<name-tag>float</name-tag><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><literal-number-float>3.14</literal-number-float>",
			),
		},
		"anchor and alias": {
			input: yamltest.JoinLF(
				"anchor: &x 1",
				"alias: *x",
			),
			want: yamltest.JoinLF(
				"<name-tag>anchor</name-tag><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><name-anchor>&</name-anchor><name-anchor>x</name-anchor><text> </text><literal-number-integer>1</literal-number-integer>",
				"<name-tag>alias</name-tag><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><name-alias>*</name-alias><name-alias>x</name-alias>",
			),
		},
		"comment": {
			input: "key: value # comment",
			want:  "<name-tag>key</name-tag><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><literal-string>value </literal-string><comment># comment</comment>",
		},
		"tag": {
			input: "tagged: !custom value",
			want:  "<name-tag>tagged</name-tag><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><name-decorator>!custom </name-decorator><literal-string>value</literal-string>",
		},
		"document markers": {
			input: yamltest.JoinLF(
				"---",
				"key: value",
				"...",
			),
			want: yamltest.JoinLF(
				"<punctuation-heading>---</punctuation-heading>",
				"<name-tag>key</name-tag><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><literal-string>value</literal-string>",
				"<punctuation-heading>...</punctuation-heading>",
			),
		},
		"directive": {
			input: yamltest.JoinLF(
				"%YAML 1.2",
				"---",
				"key: value",
			),
			want: yamltest.JoinLF(
				"<comment-preproc>%</comment-preproc><literal-string>YAML</literal-string><text> </text><literal-number-float>1.2</literal-number-float>",
				"<punctuation-heading>---</punctuation-heading>",
				"<name-tag>key</name-tag><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><literal-string>value</literal-string>",
			),
		},
		"block scalar": {
			input: yamltest.JoinLF(
				"text: |",
				"  line1",
				"  line2",
			),
			want: yamltest.JoinLF(
				"<name-tag>text</name-tag><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><punctuation-block-literal>|</punctuation-block-literal>",
				"<literal-string>  line1</literal-string>",
				"<literal-string>  line2</literal-string>",
			),
		},
		"punctuation": {
			input: "key: value",
			want:  "<name-tag>key</name-tag><punctuation-mapping-value>:</punctuation-mapping-value><text> </text><literal-string>value</literal-string>",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := lexer.Tokenize(tc.input)
			p := niceyaml.NewPrinter(
				niceyaml.WithStyles(yamltest.NewXMLStyles()),
				niceyaml.WithStyle(lipgloss.NewStyle()),
				niceyaml.WithGutter(niceyaml.NoGutter()),
			)

			got := p.Print(niceyaml.NewSourceFromTokens(tks))
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
			input: yamltest.JoinLF(
				"doc1: value1",
				"---",
				"doc2: value2",
			),
			want: yamltest.JoinLF(
				"doc1: value1",
				"---",
				"doc2: value2",
			),
		},
		"three documents": {
			input: yamltest.JoinLF(
				"first: 1",
				"---",
				"second: 2",
				"---",
				"third: 3",
			),
			want: yamltest.JoinLF(
				"first: 1",
				"---",
				"second: 2",
				"---",
				"third: 3",
			),
		},
		"documents with header": {
			input: yamltest.JoinLF(
				"---",
				"key: value",
			),
			want: yamltest.JoinLF(
				"---",
				"key: value",
			),
		},
		"documents with footer": {
			input: yamltest.JoinLF(
				"key: value",
				"...",
			),
			want: yamltest.JoinLF(
				"key: value",
				"...",
			),
		},
		"document with header and footer": {
			input: yamltest.JoinLF(
				"---",
				"key: value",
				"...",
			),
			want: yamltest.JoinLF(
				"---",
				"key: value",
				"...",
			),
		},
		"multiple docs with headers and footers": {
			input: yamltest.JoinLF(
				"---",
				"doc1: value1",
				"...",
				"---",
				"doc2: value2",
				"...",
			),
			want: yamltest.JoinLF(
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

			got := p.Print(niceyaml.NewSourceFromTokens(tks))
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
			before: yamltest.JoinLF(
				"a: 1",
				"b: 2",
				"c: 3",
				"",
			),
			after: yamltest.JoinLF(
				"a: 1",
				"b: changed",
				"c: 3",
				"",
			),
			context: 0,
			wantExact: yamltest.JoinLF(
				"      @@ -2 +2 @@",
				"   2 -b: 2",
				"   2 +b: changed",
			),
		},
		"simple change with context 1": {
			before: yamltest.JoinLF(
				"a: 1",
				"b: 2",
				"c: 3",
				"d: 4",
				"",
			),
			after: yamltest.JoinLF(
				"a: 1",
				"b: changed",
				"c: 3",
				"d: 4",
				"",
			),
			context: 1,
			wantExact: yamltest.JoinLF(
				"      @@ -1,3 +1,3 @@",
				"   1  a: 1",
				"   2 -b: 2",
				"   2 +b: changed",
				"   3  c: 3",
			),
		},
		"multiple scattered changes with context": {
			before: yamltest.JoinLF(
				"a: 1",
				"b: 2",
				"c: 3",
				"d: 4",
				"e: 5",
				"",
			),
			after: yamltest.JoinLF(
				"a: X",
				"b: 2",
				"c: 3",
				"d: 4",
				"e: Y",
				"",
			),
			context: 1,
			wantExact: yamltest.JoinLF(
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
			before: yamltest.JoinLF(
				"line1: a",
				"line2: b",
				"line3: c",
				"line4: d",
				"line5: e",
				"line6: f",
				"",
			),
			after: yamltest.JoinLF(
				"line1: X",
				"line2: b",
				"line3: c",
				"line4: d",
				"line5: e",
				"line6: Y",
				"",
			),
			context: 0,
			wantExact: yamltest.JoinLF(
				"      @@ -1 +1 @@",
				"   1 -line1: a",
				"   1 +line1: X",
				"      @@ -6 +6 @@",
				"   6 -line6: f",
				"   6 +line6: Y",
			),
		},
		"addition only": {
			before: yamltest.JoinLF(
				"a: 1",
				"c: 3",
				"",
			),
			after: yamltest.JoinLF(
				"a: 1",
				"b: 2",
				"c: 3",
				"",
			),
			context: 1,
			wantExact: yamltest.JoinLF(
				"      @@ -1,2 +1,3 @@",
				"   1  a: 1",
				"   2 +b: 2",
				"   3  c: 3",
			),
		},
		"deletion only": {
			before: yamltest.JoinLF(
				"a: 1",
				"b: 2",
				"c: 3",
				"",
			),
			after: yamltest.JoinLF(
				"a: 1",
				"c: 3",
				"",
			),
			context: 1,
			wantExact: yamltest.JoinLF(
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
			before: yamltest.JoinLF(
				"a: 1",
				"b: 2",
				"c: 3",
				"",
			),
			after: yamltest.JoinLF(
				"a: 1",
				"b: changed",
				"c: 3",
				"",
			),
			context: 100, // Much larger than 3 lines.
			wantExact: yamltest.JoinLF(
				"      @@ -1,3 +1,3 @@",
				"   1  a: 1",
				"   2 -b: 2",
				"   2 +b: changed",
				"   3  c: 3",
			),
		},
		"line numbers with hunk alignment": {
			before: yamltest.JoinLF(
				"a: 1",
				"b: 2",
				"c: 3",
				"d: 4",
				"e: 5",
				"",
			),
			after: yamltest.JoinLF(
				"a: 1",
				"b: changed",
				"c: 3",
				"d: 4",
				"e: 5",
				"",
			),
			context: 1,
			wantExact: yamltest.JoinLF(
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

			p := niceyaml.NewPrinter(
				niceyaml.WithStyles(style.Styles{}),
				niceyaml.WithStyle(lipgloss.NewStyle()),
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
		normalizer   niceyaml.Normalizer
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
			normalizer: niceyaml.NewStandardNormalizer(),
			want:       "name: [Thaïs]",
		},
		"utf8 - case insensitive with diacritics": {
			input:      "name: THAÏS test",
			search:     "thais",
			normalizer: niceyaml.NewStandardNormalizer(),
			want:       "name: [THAÏS] test",
		},
		"utf8 - search ascii finds normalized diacritic": {
			input:      "key: über",
			search:     "u",
			normalizer: niceyaml.NewStandardNormalizer(),
			want:       "key: [ü]ber",
		},
		"utf8 - normalizer search after multiple multibyte": {
			input:      "key: über Yamüll test",
			search:     "ya",
			normalizer: niceyaml.NewStandardNormalizer(),
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
			finder := testFinder(tc.normalizer)
			finder.Load(source)

			printer := testPrinter()

			ranges := finder.Find(tc.search)

			if tc.wantNoRanges {
				assert.Empty(t, ranges)
			}

			source.AddOverlay(testOverlayHighlight, ranges...)

			got := printer.Print(source)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPrinter_Golden(t *testing.T) {
	t.Parallel()

	type goldenTest struct {
		setupFunc func(*niceyaml.Printer, *niceyaml.Source)
		opts      []niceyaml.PrinterOption
	}

	tcs := map[string]goldenTest{
		"default colors": {
			opts: []niceyaml.PrinterOption{
				niceyaml.WithStyles(theme.Charm()),
				niceyaml.WithStyle(lipgloss.NewStyle()),
				niceyaml.WithGutter(niceyaml.NoGutter()),
			},
		},
		"default colors with line numbers": {
			opts: []niceyaml.PrinterOption{
				niceyaml.WithStyles(theme.Charm()),
				niceyaml.WithStyle(lipgloss.NewStyle()),
			},
		},
		"no colors": {
			opts: []niceyaml.PrinterOption{
				niceyaml.WithStyles(style.Styles{}),
				niceyaml.WithStyle(lipgloss.NewStyle()),
				niceyaml.WithGutter(niceyaml.NoGutter()),
			},
		},
		"no colors with line numbers": {
			opts: []niceyaml.PrinterOption{
				niceyaml.WithStyles(style.Styles{}),
				niceyaml.WithStyle(lipgloss.NewStyle()),
			},
		},
		"find and highlight": {
			opts: []niceyaml.PrinterOption{
				niceyaml.WithStyles(theme.Charm().With(
					style.Set(testOverlayHighlight, lipgloss.NewStyle().
						Background(lipgloss.Color("#FFFF00")).
						Foreground(lipgloss.Color("#000000"))),
				)),
				niceyaml.WithStyle(lipgloss.NewStyle()),
				niceyaml.WithGutter(niceyaml.NoGutter()),
			},
			setupFunc: func(_ *niceyaml.Printer, source *niceyaml.Source) {
				// Search for "日本" (Japan) which appears multiple times in full.yaml.
				finder := niceyaml.NewFinder()
				finder.Load(source)

				ranges := finder.Find("日本")
				source.AddOverlay(testOverlayHighlight, ranges...)
			},
		},
	}

	input, err := os.ReadFile("testdata/full.yaml")
	require.NoError(t, err)

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := niceyaml.NewSourceFromString(string(input))
			printer := niceyaml.NewPrinter(tc.opts...)

			if tc.setupFunc != nil {
				tc.setupFunc(printer, lines)
			}

			output := printer.Print(lines)
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
		kind  style.Style
		start position.Position
		end   position.Position
	}

	// Overlay kinds for the various test tags.
	const (
		kindHL style.Style = testOverlayHighlight + iota + 1
		kindAll
		kindA
		kindB
		kindC
		kindX
		kindY
		kindK
		kindVal
		kindSpan
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
			input: yamltest.JoinLF("a: 1", "b: 2"),
			ranges: []overlayRange{
				{kindX, position.New(0, 0), position.New(0, 1)},
				{kindY, position.New(1, 0), position.New(1, 1)},
			},
			want: yamltest.JoinLF("<x>a</x>: 1", "<y>b</y>: 2"),
		},
		"range spanning multiple lines": {
			input: yamltest.JoinLF("a: 1", "b: 2"),
			ranges: []overlayRange{
				{kindSpan, position.New(0, 3), position.New(1, 1)},
			},
			// Range spans from line 0 col 3 to line 1 col 1.
			want: yamltest.JoinLF("a: <span>1</span>", "<span>b</span>: 2"),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)
			p := niceyaml.NewPrinter(
				niceyaml.WithStyles(testOverlayStyler),
				niceyaml.WithStyle(lipgloss.NewStyle()),
				niceyaml.WithGutter(niceyaml.NoGutter()),
			)

			for _, or := range tc.ranges {
				source.AddOverlay(or.kind, position.NewRange(or.start, or.end))
			}

			got := p.Print(source)
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
		colorKind1 style.Style = testOverlayHighlight + 100 + iota
		colorKind2
		colorKind3
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

			source := niceyaml.NewSourceFromString(tc.input)

			// Build overlay styler with styles from test case.
			kinds := []style.Style{colorKind1, colorKind2, colorKind3}

			overlayOpts := make([]style.StylesOption, 0, len(tc.overlays))
			for i, od := range tc.overlays {
				overlayOpts = append(overlayOpts, style.Set(kinds[i], od.style))
				source.AddOverlay(kinds[i], position.NewRange(od.start, od.end))
			}

			p := niceyaml.NewPrinter(
				niceyaml.WithStyles(style.NewStyles(lipgloss.NewStyle(), overlayOpts...)),
				niceyaml.WithStyle(lipgloss.NewStyle()),
				niceyaml.WithGutter(niceyaml.NoGutter()),
			)

			got := p.Print(source)
			golden.RequireEqual(t, got)
		})
	}
}

func TestDefaultAnnotation(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		annotations line.Annotations
		position    line.RelativePosition
		want        string
	}{
		"empty annotations": {
			annotations: line.Annotations{},
			position:    line.Below,
			want:        "",
		},
		"single below annotation": {
			annotations: line.Annotations{{Content: "error here", Position: line.Below, Col: 0}},
			position:    line.Below,
			want:        "^ error here",
		},
		"single below annotation with padding": {
			annotations: line.Annotations{{Content: "error", Position: line.Below, Col: 5}},
			position:    line.Below,
			want:        "     ^ error",
		},
		"single above annotation": {
			annotations: line.Annotations{{Content: "@@ hunk @@", Position: line.Above, Col: 0}},
			position:    line.Above,
			want:        "@@ hunk @@",
		},
		"single above annotation with padding": {
			annotations: line.Annotations{{Content: "header", Position: line.Above, Col: 3}},
			position:    line.Above,
			want:        "   header",
		},
		"multiple below annotations": {
			annotations: line.Annotations{
				{Content: "first", Position: line.Below, Col: 0},
				{Content: "second", Position: line.Below, Col: 5},
			},
			position: line.Below,
			want:     "^ first; second",
		},
		"multiple below annotations uses min col": {
			annotations: line.Annotations{
				{Content: "first", Position: line.Below, Col: 5},
				{Content: "second", Position: line.Below, Col: 2},
			},
			position: line.Below,
			want:     "  ^ first; second",
		},
		"multiple above annotations": {
			annotations: line.Annotations{
				{Content: "header1", Position: line.Above, Col: 0},
				{Content: "header2", Position: line.Above, Col: 0},
			},
			position: line.Above,
			want:     "header1; header2",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fn := niceyaml.DefaultAnnotation()
			ctx := niceyaml.AnnotationContext{
				Annotations: tc.annotations,
				Position:    tc.position,
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
	customAnnotation := func(ctx niceyaml.AnnotationContext) string {
		if len(ctx.Annotations) == 0 {
			return ""
		}

		contents := ctx.Annotations.Contents()

		if ctx.Position == line.Below {
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
				Content:  "custom error",
				Position: line.Below,
				Col:      0,
			},
			want: "key: value\n>>> custom error",
		},
		"custom above annotation": {
			lineIndex: 0,
			annotation: line.Annotation{
				Content:  "custom header",
				Position: line.Above,
				Col:      0,
			},
			want: "=== custom header\nkey: value",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			input := "key: value"
			source := niceyaml.NewSourceFromString(input)
			source.Line(tc.lineIndex).AddAnnotation(tc.annotation)

			p := niceyaml.NewPrinter(
				niceyaml.WithStyles(style.Styles{}),
				niceyaml.WithStyle(lipgloss.NewStyle()),
				niceyaml.WithGutter(niceyaml.NoGutter()),
				niceyaml.WithAnnotationFunc(customAnnotation),
			)

			got := p.Print(source)
			assert.Equal(t, tc.want, got)
		})
	}
}
