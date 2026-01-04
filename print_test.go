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

	"github.com/macropower/niceyaml"
	"github.com/macropower/niceyaml/line"
	"github.com/macropower/niceyaml/position"
	"github.com/macropower/niceyaml/yamltest"
)

// testHighlightStyle returns a style that wraps content in brackets for easy verification.
func testHighlightStyle() *lipgloss.Style {
	style := lipgloss.NewStyle().Transform(func(s string) string {
		return "[" + s + "]"
	})

	return &style
}

// testPrinter returns a printer without styles or padding for predictable output.
func testPrinter() *niceyaml.Printer {
	return testPrinterWithGutter(niceyaml.NoGutter)
}

// testPrinterWithGutter returns a printer without styles but with a custom gutter.
func testPrinterWithGutter(gutter niceyaml.GutterFunc) *niceyaml.Printer {
	return niceyaml.NewPrinter(
		niceyaml.WithStyles(niceyaml.Styles{}),
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

	diff := niceyaml.NewFullDiff(
		niceyaml.NewRevision(beforeTks),
		niceyaml.NewRevision(afterTks),
	)

	return p.Print(diff.Lines())
}

// printDiffSummary generates a summary diff showing only changed lines with context.
// Helper to replace the removed Printer.PrintTokenDiffSummary method in tests.
func printDiffSummary(p *niceyaml.Printer, before, after string, context int) string {
	beforeTks := niceyaml.NewSourceFromString(before, niceyaml.WithName("before"))
	afterTks := niceyaml.NewSourceFromString(after, niceyaml.WithName("after"))

	diff := niceyaml.NewSummaryDiff(
		niceyaml.NewRevision(beforeTks),
		niceyaml.NewRevision(afterTks),
		context,
	)

	result := diff.Lines()
	if result.IsEmpty() {
		return ""
	}

	return p.Print(result)
}

// testFinder returns a Finder configured for testing.
func testFinder(lines *niceyaml.Source, normalizer niceyaml.Normalizer) *niceyaml.Finder {
	var opts []niceyaml.FinderOption
	if normalizer != nil {
		opts = append(opts, niceyaml.WithNormalizer(normalizer))
	}

	return niceyaml.NewFinder(lines, opts...)
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
			p := testPrinter()
			p.AddStyleToRange(testHighlightStyle(), tc.rng)

			got := p.Print(niceyaml.NewSourceFromTokens(tks))
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPrinter_ClearStyles(t *testing.T) {
	t.Parallel()

	input := "key: value"
	tks := lexer.Tokenize(input)

	p := testPrinter()
	p.AddStyleToRange(
		testHighlightStyle(),
		position.NewRange(position.New(0, 0), position.New(0, 3)),
	)
	p.ClearStyles()

	// After clearing, no styles should be applied.
	assert.Equal(t, "key: value", p.Print(niceyaml.NewSourceFromTokens(tks)))
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
			want:  "<key>key</key><punctuation>:</punctuation><default> </default><string>value</string>",
			opts: []niceyaml.PrinterOption{
				niceyaml.WithStyles(yamltest.NewXMLStyles()),
				niceyaml.WithStyle(lipgloss.NewStyle()),
				niceyaml.WithGutter(niceyaml.NoGutter),
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
				"<key>key</key><punctuation>:</punctuation><default> </default><string>value</string>",
				"<key>number</key><punctuation>:</punctuation><default> </default><number>42</number>",
				"<key>bool</key><punctuation>:</punctuation><default> </default><bool>true</bool>",
				"<comment># comment</comment>",
			),
			opts: []niceyaml.PrinterOption{
				niceyaml.WithStyles(yamltest.NewXMLStyles()),
				niceyaml.WithStyle(lipgloss.NewStyle()),
				niceyaml.WithGutter(niceyaml.NoGutter),
			},
		},
		"empty styles": {
			input: "key: value",
			want:  "   1  key: value ",
			// Default gutter adds line numbers, default style adds trailing padding.
			opts: []niceyaml.PrinterOption{niceyaml.WithStyles(niceyaml.Styles{})},
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
		gutter  niceyaml.GutterFunc
		want    string
		minLine int
		maxLine int
	}{
		"full range": {
			minLine: -1,
			maxLine: -1,
			gutter:  niceyaml.NoGutter,
			want: yamltest.JoinLF(
				"first: 1",
				"second: 2",
				"third: 3",
				"fourth: 4",
				"fifth: 5",
			),
		},
		"full range with line numbers": {
			minLine: -1,
			maxLine: -1,
			gutter:  niceyaml.LineNumberGutter(),
			want: yamltest.JoinLF(
				"   1 first: 1",
				"   2 second: 2",
				"   3 third: 3",
				"   4 fourth: 4",
				"   5 fifth: 5",
			),
		},
		"bounded middle": {
			minLine: 1,
			maxLine: 3,
			gutter:  niceyaml.NoGutter,
			want: yamltest.JoinLF(
				"second: 2",
				"third: 3",
				"fourth: 4",
			),
		},
		"bounded middle with line numbers": {
			minLine: 1,
			maxLine: 3,
			gutter:  niceyaml.LineNumberGutter(),
			want: yamltest.JoinLF(
				"   2 second: 2",
				"   3 third: 3",
				"   4 fourth: 4",
			),
		},
		"unbounded min": {
			minLine: -1,
			maxLine: 1,
			gutter:  niceyaml.NoGutter,
			want: yamltest.JoinLF(
				"first: 1",
				"second: 2",
			),
		},
		"unbounded max": {
			minLine: 3,
			maxLine: -1,
			gutter:  niceyaml.NoGutter,
			want: yamltest.JoinLF(
				"fourth: 4",
				"fifth: 5",
			),
		},
		"single line": {
			minLine: 2,
			maxLine: 2,
			gutter:  niceyaml.NoGutter,
			want:    "third: 3",
		},
		"single line with line numbers": {
			minLine: 2,
			maxLine: 2,
			gutter:  niceyaml.LineNumberGutter(),
			want:    "   3 third: 3",
		},
		"empty result": {
			minLine: 10,
			maxLine: 20,
			gutter:  niceyaml.NoGutter,
			want:    "",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := testPrinterWithGutter(tc.gutter)
			lines := niceyaml.NewSourceFromString(input)

			got := p.PrintSlice(lines, tc.minLine, tc.maxLine)
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
				niceyaml.WithStyles(niceyaml.Styles{}),
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
		before       string
		after        string
		wantPrefixes []string
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
			wantPrefixes: []string{" ", "-", " "},
		},
		"modifications show delete before insert": {
			// Test that modifications show delete before insert.
			before:       "key: old\n",
			after:        "key: new\n",
			wantPrefixes: []string{"-", "+"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := testPrinterWithGutter(niceyaml.DiffGutter())
			got := printDiff(p, tc.before, tc.after)

			lines := strings.Split(got, "\n")

			require.Len(t, lines, len(tc.wantPrefixes))

			for i, prefix := range tc.wantPrefixes {
				assert.True(t, strings.HasPrefix(lines[i], prefix),
					"line %d should have prefix %q, got %q", i, prefix, lines[i])
			}
		})
	}
}

func TestPrinter_WordWrap(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		gutter niceyaml.GutterFunc
		input  string
		want   string
		width  int
	}{
		"no wrap when disabled": {
			input:  "key: value",
			width:  0,
			gutter: niceyaml.NoGutter,
			want:   "key: value",
		},
		"simple wrap": {
			input:  "key: this is a very long value that should wrap",
			width:  20,
			gutter: niceyaml.NoGutter,
			want: yamltest.JoinLF(
				"key: this is a very",
				"long value that",
				"should wrap",
			),
		},
		"wrap on slash": {
			input:  "path: /usr/local/bin/something",
			width:  20,
			gutter: niceyaml.NoGutter,
			want: yamltest.JoinLF(
				"path: /usr/local/",
				"bin/something",
			),
		},
		"wrap on hyphen": {
			input:  "name: very-long-hyphenated-name",
			width:  20,
			gutter: niceyaml.NoGutter,
			want: yamltest.JoinLF(
				"name: very-long-",
				"hyphenated-name",
			),
		},
		"short content no wrap": {
			input:  "key: value",
			width:  50,
			gutter: niceyaml.NoGutter,
			want:   "key: value",
		},
		"multi-line content": {
			input: yamltest.JoinLF(
				"key: value",
				"another: long value that should wrap here",
			),
			width:  20,
			gutter: niceyaml.NoGutter,
			want: yamltest.JoinLF(
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
			gutter: niceyaml.LineNumberGutter(),
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
			width:  30,
			gutter: niceyaml.LineNumberGutter(),
			// First line fits, second line wraps.
			// Width 30 - 5 (line number gutter) = 25 for content.
			want: yamltest.JoinLF(
				"   1 first: short",
				"   2 second: this is a very",
				"   - long line that wraps",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := lexer.Tokenize(tc.input)

			p := testPrinterWithGutter(tc.gutter)
			if tc.width > 0 {
				p.SetWidth(tc.width)
			}

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
		want         string
		wantContains []string
		width        int
	}{
		"diff lines wrap correctly": {
			before: "key: short\n",
			after:  "key: this is a very long value that should wrap\n",
			width:  30,
			want: yamltest.JoinLF(
				"-key: short",
				"+key: this is a very long",
				" value that should wrap",
			),
		},
		"wrapped diff continuation": {
			before: "key: original value\n",
			after:  "key: new very long value that definitely wraps\n",
			width:  25,
			want: yamltest.JoinLF(
				"-key: original value",
				"+key: new very long value",
				" that definitely wraps",
			),
		},
		"modification with wrap": {
			before: "name: old-hyphenated-name-value\n",
			after:  "name: new-hyphenated-name-value\n",
			width:  20,
			want: yamltest.JoinLF(
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

			if tc.want != "" {
				assert.Equal(t, tc.want, got)
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
				niceyaml.WithStyles(niceyaml.Styles{}),
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
			gutterFunc: niceyaml.NoGutter,
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

	styles := niceyaml.Styles{}

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
			source.Annotate(0, line.Annotation{Content: tc.annotation, Column: 1})

			p := testPrinter()
			p.SetAnnotationsEnabled(tc.enabled)

			got := p.Print(source)

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPrinter_GetStyle(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		styles     niceyaml.Styles
		query      niceyaml.Style
		wantBold   bool
		wantItalic bool
	}{
		"returns style from styles map": {
			styles: niceyaml.Styles{
				niceyaml.StyleKey: lipgloss.NewStyle().Bold(true),
			},
			query:    niceyaml.StyleKey,
			wantBold: true,
		},
		"falls back to default for unknown style": {
			styles: niceyaml.Styles{
				niceyaml.StyleDefault: lipgloss.NewStyle().Italic(true),
			},
			query:      niceyaml.StyleKey,
			wantItalic: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := niceyaml.NewPrinter(niceyaml.WithStyles(tc.styles))
			got := p.GetStyle(tc.query)

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
			want:  "<key>key</key><punctuation>:</punctuation><default> </default><string>value</string>",
		},
		"null types": {
			input: yamltest.JoinLF(
				"null: null",
				"tilde: ~",
			),
			want: yamltest.JoinLF(
				"<key>null</key><punctuation>:</punctuation><default> </default><null>null</null>",
				"<key>tilde</key><punctuation>:</punctuation><default> </default><null>~</null>",
			),
		},
		"boolean types": {
			input: yamltest.JoinLF(
				"yes: true",
				"no: false",
			),
			want: yamltest.JoinLF(
				"<key>yes</key><punctuation>:</punctuation><default> </default><bool>true</bool>",
				"<key>no</key><punctuation>:</punctuation><default> </default><bool>false</bool>",
			),
		},
		"number types": {
			input: yamltest.JoinLF(
				"int: 42",
				"float: 3.14",
			),
			want: yamltest.JoinLF(
				"<key>int</key><punctuation>:</punctuation><default> </default><number>42</number>",
				"<key>float</key><punctuation>:</punctuation><default> </default><number>3.14</number>",
			),
		},
		"anchor and alias": {
			input: yamltest.JoinLF(
				"anchor: &x 1",
				"alias: *x",
			),
			want: yamltest.JoinLF(
				"<key>anchor</key><punctuation>:</punctuation><default> </default><anchor>&</anchor><anchor>x</anchor><default> </default><number>1</number>",
				"<key>alias</key><punctuation>:</punctuation><default> </default><alias>*</alias><alias>x</alias>",
			),
		},
		"comment": {
			input: "key: value # comment",
			want:  "<key>key</key><punctuation>:</punctuation><default> </default><string>value </string><comment># comment</comment>",
		},
		"tag": {
			input: "tagged: !custom value",
			want:  "<key>tagged</key><punctuation>:</punctuation><default> </default><tag>!custom </tag><string>value</string>",
		},
		"document markers": {
			input: yamltest.JoinLF(
				"---",
				"key: value",
				"...",
			),
			want: yamltest.JoinLF(
				"<document>---</document>",
				"<key>key</key><punctuation>:</punctuation><default> </default><string>value</string>",
				"<document>...</document>",
			),
		},
		"directive": {
			input: yamltest.JoinLF(
				"%YAML 1.2",
				"---",
				"key: value",
			),
			want: yamltest.JoinLF(
				"<directive>%</directive><string>YAML</string><default> </default><number>1.2</number>",
				"<document>---</document>",
				"<key>key</key><punctuation>:</punctuation><default> </default><string>value</string>",
			),
		},
		"block scalar": {
			input: yamltest.JoinLF(
				"text: |",
				"  line1",
				"  line2",
			),
			want: yamltest.JoinLF(
				"<key>text</key><punctuation>:</punctuation><default> </default><block-scalar>|</block-scalar>",
				"<string>  line1</string>",
				"<string>  line2</string>",
			),
		},
		"punctuation": {
			input: "key: value",
			want:  "<key>key</key><punctuation>:</punctuation><default> </default><string>value</string>",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := lexer.Tokenize(tc.input)
			p := niceyaml.NewPrinter(
				niceyaml.WithStyles(yamltest.NewXMLStyles()),
				niceyaml.WithStyle(lipgloss.NewStyle()),
				niceyaml.WithGutter(niceyaml.NoGutter),
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
		want           string
		wantContains   []string
		wantNotContain []string
		context        int
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
			want: yamltest.JoinLF(
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
			want: yamltest.JoinLF(
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
			want: yamltest.JoinLF(
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
			want: yamltest.JoinLF(
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
			want: yamltest.JoinLF(
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
			want: yamltest.JoinLF(
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
			want: yamltest.JoinLF(
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
			want: yamltest.JoinLF(
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
				niceyaml.WithStyles(niceyaml.Styles{}),
				niceyaml.WithStyle(lipgloss.NewStyle()),
			)

			got := printDiffSummary(p, tc.before, tc.after, tc.context)

			if tc.wantEmpty {
				assert.Empty(t, got)
				return
			}

			if tc.want != "" {
				assert.Equal(t, tc.want, got)
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
			normalizer: niceyaml.StandardNormalizer{},
			want:       "name: [Thaïs]",
		},
		"utf8 - case insensitive with diacritics": {
			input:      "name: THAÏS test",
			search:     "thais",
			normalizer: niceyaml.StandardNormalizer{},
			want:       "name: [THAÏS] test",
		},
		"utf8 - search ascii finds normalized diacritic": {
			input:      "key: über",
			search:     "u",
			normalizer: niceyaml.StandardNormalizer{},
			want:       "key: [ü]ber",
		},
		"utf8 - normalizer search after multiple multibyte": {
			input:      "key: über Yamüll test",
			search:     "ya",
			normalizer: niceyaml.StandardNormalizer{},
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

			lines := niceyaml.NewSourceFromString(tc.input)
			finder := testFinder(lines, tc.normalizer)
			printer := testPrinter()

			ranges := finder.Find(tc.search)

			if tc.wantNoRanges {
				assert.Empty(t, ranges)
			}

			for _, rng := range ranges {
				printer.AddStyleToRange(testHighlightStyle(), rng)
			}

			got := printer.Print(lines)
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
				niceyaml.WithStyles(niceyaml.DefaultStyles()),
				niceyaml.WithStyle(lipgloss.NewStyle()),
				niceyaml.WithGutter(niceyaml.NoGutter),
			},
		},
		"default colors with line numbers": {
			opts: []niceyaml.PrinterOption{
				niceyaml.WithStyles(niceyaml.DefaultStyles()),
				niceyaml.WithStyle(lipgloss.NewStyle()),
			},
		},
		"no colors": {
			opts: []niceyaml.PrinterOption{
				niceyaml.WithStyles(niceyaml.Styles{}),
				niceyaml.WithStyle(lipgloss.NewStyle()),
				niceyaml.WithGutter(niceyaml.NoGutter),
			},
		},
		"no colors with line numbers": {
			opts: []niceyaml.PrinterOption{
				niceyaml.WithStyles(niceyaml.Styles{}),
				niceyaml.WithStyle(lipgloss.NewStyle()),
			},
		},
		"find and highlight": {
			opts: []niceyaml.PrinterOption{
				niceyaml.WithStyles(niceyaml.DefaultStyles()),
				niceyaml.WithStyle(lipgloss.NewStyle()),
				niceyaml.WithGutter(niceyaml.NoGutter),
			},
			setupFunc: func(p *niceyaml.Printer, lines *niceyaml.Source) {
				// Search for "日本" (Japan) which appears multiple times in full.yaml.
				finder := niceyaml.NewFinder(lines)
				highlightStyle := lipgloss.NewStyle().
					Background(lipgloss.Color("#FFFF00")).
					Foreground(lipgloss.Color("#000000"))

				ranges := finder.Find("日本")
				for _, rng := range ranges {
					p.AddStyleToRange(&highlightStyle, rng)
				}
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

	// StyleRange defines a style and its range.
	type styleRange struct {
		style lipgloss.Style
		start position.Position
		end   position.Position
	}

	tcs := map[string]struct {
		input  string
		want   string
		ranges []styleRange
	}{
		"single range on value": {
			input: "key: value",
			ranges: []styleRange{
				{styleWithTag("hl"), position.New(0, 5), position.New(0, 10)},
			},
			want: "key: <hl>value</hl>",
		},
		"single range on key": {
			input: "key: value",
			ranges: []styleRange{
				{styleWithTag("hl"), position.New(0, 0), position.New(0, 3)},
			},
			want: "<hl>key</hl>: value",
		},
		"full line range": {
			// Each token is styled separately, so transforms apply per-token.
			input: "key: value",
			ranges: []styleRange{
				{styleWithTag("all"), position.New(0, 0), position.New(0, 10)},
			},
			want: "<all>key</all><all>:</all><all> </all><all>value</all>",
		},
		"non-overlapping ranges": {
			input: "key: value",
			ranges: []styleRange{
				{styleWithTag("a"), position.New(0, 0), position.New(0, 3)},
				{styleWithTag("b"), position.New(0, 5), position.New(0, 10)},
			},
			want: "<a>key</a>: <b>value</b>",
		},
		"adjacent ranges": {
			input: "key: value",
			ranges: []styleRange{
				{styleWithTag("a"), position.New(0, 0), position.New(0, 3)},
				{styleWithTag("b"), position.New(0, 3), position.New(0, 4)},
			},
			want: "<a>key</a><b>:</b> value",
		},
		"overlapping ranges - transforms compose": {
			// First range [0,5) gets override, second range [2,7) blends.
			// Blending composes transforms: overlay(base(text)).
			// Each token is styled separately.
			input: "key: value",
			ranges: []styleRange{
				{styleWithTag("a"), position.New(0, 0), position.New(0, 5)},
				{styleWithTag("b"), position.New(0, 2), position.New(0, 7)},
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
			ranges: []styleRange{
				{styleWithTag("a"), position.New(0, 0), position.New(0, 6)},
				{styleWithTag("b"), position.New(0, 2), position.New(0, 8)},
				{styleWithTag("c"), position.New(0, 4), position.New(0, 10)},
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
			ranges: []styleRange{
				{styleWithTag("x"), position.New(0, 1), position.New(0, 4)},
				{styleWithTag("y"), position.New(0, 3), position.New(0, 5)},
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
			ranges: []styleRange{
				{styleWithTag("key"), position.New(0, 0), position.New(0, 3)},
				{styleWithTag("val"), position.New(0, 5), position.New(0, 10)},
			},
			want: "<key>key</key>: <val>value</val>",
		},
		"multi-line with ranges on each line": {
			input: yamltest.JoinLF("a: 1", "b: 2"),
			ranges: []styleRange{
				{styleWithTag("x"), position.New(0, 0), position.New(0, 1)},
				{styleWithTag("y"), position.New(1, 0), position.New(1, 1)},
			},
			want: yamltest.JoinLF("<x>a</x>: 1", "<y>b</y>: 2"),
		},
		"range spanning multiple lines": {
			input: yamltest.JoinLF("a: 1", "b: 2"),
			ranges: []styleRange{
				{styleWithTag("span"), position.New(0, 3), position.New(1, 1)},
			},
			// Range spans from line 0 col 3 to line 1 col 1.
			want: yamltest.JoinLF("a: <span>1</span>", "<span>b</span>: 2"),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := testPrinter()

			for _, sr := range tc.ranges {
				style := sr.style
				p.AddStyleToRange(&style, position.NewRange(sr.start, sr.end))
			}

			got := p.Print(niceyaml.NewSourceFromString(tc.input))
			assert.Equal(t, tc.want, got)
		})
	}
}
