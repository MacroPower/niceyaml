package niceyaml_test

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macropower/niceyaml"
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
	return niceyaml.NewPrinter(
		niceyaml.WithStyles(niceyaml.Styles{}),
		niceyaml.WithStyle(lipgloss.NewStyle()),
		niceyaml.WithLinePrefix(""),
	)
}

// parseFile parses tokens into an ast.File for testing PrintFile.
func parseFile(t *testing.T, tokens token.Tokens) *ast.File {
	t.Helper()

	file, err := parser.Parse(tokens, 0)
	require.NoError(t, err)

	return file
}

func Test_PrinterErrorToken(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input      string
		want       string
		tokenIndex int
		wantLine   int
	}{
		"basic yaml tokens[3]": {
			input: `---
text: aaaa
text2: aaaa
 bbbb
 cccc
 dddd
 eeee
text3: ffff
 gggg
 hhhh
 iiii
 jjjj
bool: true
number: 10
anchor: &x 1
alias: *x
`,
			tokenIndex: 3,
			want: `
---
text: aaaa
text2: aaaa
 bbbb
 cccc
 dddd
 eeee
`,
			wantLine: 1,
		},
		"basic yaml tokens[4]": {
			input: `---
text: aaaa
text2: aaaa
 bbbb
 cccc
 dddd
 eeee
text3: ffff
 gggg
 hhhh
 iiii
 jjjj
bool: true
number: 10
anchor: &x 1
alias: *x
`,
			tokenIndex: 4,
			want: `
---
text: aaaa
text2: aaaa
 bbbb
 cccc
 dddd
 eeee
`,
			wantLine: 1,
		},
		"basic yaml tokens[6]": {
			input: `---
text: aaaa
text2: aaaa
 bbbb
 cccc
 dddd
 eeee
text3: ffff
 gggg
 hhhh
 iiii
 jjjj
bool: true
number: 10
anchor: &x 1
alias: *x
`,
			tokenIndex: 6,
			want: `
---
text: aaaa
text2: aaaa
 bbbb
 cccc
 dddd
 eeee
text3: ffff
 gggg
 hhhh
 iiii
 jjjj
`,
			wantLine: 1,
		},
		"document header tokens[12]": {
			input: `---
a:
 b:
  c:
   d: e
   f: g
   h: i

---
`,
			tokenIndex: 12,
			want: `
 b:
  c:
   d: e
   f: g
   h: i

---`,
			wantLine: 3,
		},
		"multiline strings tokens[2]": {
			input: `
text1: 'aaaa
 bbbb
 cccc'
text2: "ffff
 gggg
 hhhh"
text3: hello
`,
			tokenIndex: 2,
			want: `
text1: 'aaaa
 bbbb
 cccc'
text2: "ffff
 gggg
 hhhh"`,
			wantLine: 1,
		},
		"multiline strings tokens[3]": {
			input: `
text1: 'aaaa
 bbbb
 cccc'
text2: "ffff
 gggg
 hhhh"
text3: hello
`,
			tokenIndex: 3,
			want: `
text1: 'aaaa
 bbbb
 cccc'
text2: "ffff
 gggg
 hhhh"
text3: hello`,
			wantLine: 2,
		},
		"multiline strings tokens[5]": {
			input: `
text1: 'aaaa
 bbbb
 cccc'
text2: "ffff
 gggg
 hhhh"
text3: hello
`,
			tokenIndex: 5,
			want: `
text1: 'aaaa
 bbbb
 cccc'
text2: "ffff
 gggg
 hhhh"
text3: hello`,
			wantLine: 2,
		},
		"multiline strings tokens[6]": {
			input: `
text1: 'aaaa
 bbbb
 cccc'
text2: "ffff
 gggg
 hhhh"
text3: hello
`,
			tokenIndex: 6,
			want: `
text2: "ffff
 gggg
 hhhh"
text3: hello`,
			wantLine: 5,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tokens := lexer.Tokenize(tc.input)

			p := testPrinter()

			got, gotLine := p.PrintErrorToken(tokens[tc.tokenIndex], 3)
			got = "\n" + got

			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.wantLine, gotLine)
		})
	}
}

func TestPrinter_Anchor(t *testing.T) {
	t.Parallel()

	input := `
anchor: &x 1
alias: *x`
	tokens := lexer.Tokenize(input)

	p := testPrinter()

	got := p.PrintTokens(tokens)
	assert.Equal(t, input, got)

	file := parseFile(t, tokens)
	gotFile := p.PrintFile(file)
	assert.Equal(t, got, gotFile)
}

func TestPrinter_Highlight(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input     string
		findToken string
		want      string
	}{
		"value token": {
			input:     "key: value\nnumber: 42",
			findToken: "value",
			want:      "key: [value]\nnumber: 42",
		},
		"key token": {
			input:     "first: 1\nsecond: 2",
			findToken: "second",
			want:      "first: 1\n[second]: 2",
		},
		"multi-line token": {
			input:     "key: |\n  line1\n  line2",
			findToken: "line1",
			want:      "key: |\n[  line1]\n[  line2]",
		},
		"indented key": {
			input:     "root:\n  nested: value",
			findToken: "nested",
			want:      "root:\n  [nested]: value",
		},
		"unicode key": {
			input:     "日本語: value",
			findToken: "value",
			want:      "日本語: [value]",
		},
		"unicode value": {
			input:     "key: 日本語",
			findToken: "日本語",
			want:      "key: [日本語]",
		},
		"mixed unicode": {
			input:     "日本語: 中文\nenglish: test",
			findToken: "test",
			want:      "日本語: 中文\nenglish: [test]",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tokens := lexer.Tokenize(tc.input)

			var line, column int
			for _, tk := range tokens {
				if tk.Value == tc.findToken || strings.Contains(tk.Value, tc.findToken) {
					line = tk.Position.Line
					column = tk.Position.Column

					break
				}
			}

			require.NotZero(t, line, "should find token %q", tc.findToken)

			p := testPrinter()
			p.AddStyleToToken(testHighlightStyle(), niceyaml.Position{Line: line, Col: column})

			got := p.PrintTokens(tokens)
			assert.Equal(t, tc.want, got)

			file := parseFile(t, tokens)
			gotFile := p.PrintFile(file)
			assert.Equal(t, got, gotFile)
		})
	}
}

func TestPrinter_AddStyleToRange(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  string
		rng   niceyaml.PositionRange
	}{
		"partial token - middle of value": {
			input: "key: value",
			rng: niceyaml.PositionRange{
				Start: niceyaml.Position{Line: 1, Col: 8},
				End:   niceyaml.Position{Line: 1, Col: 11},
			},
			want: "key: va[lue]",
		},
		"partial token - start of value": {
			input: "key: value",
			rng: niceyaml.PositionRange{
				Start: niceyaml.Position{Line: 1, Col: 6},
				End:   niceyaml.Position{Line: 1, Col: 9},
			},
			want: "key: [val]ue",
		},
		"full token": {
			input: "key: value",
			rng: niceyaml.PositionRange{
				Start: niceyaml.Position{Line: 1, Col: 6},
				End:   niceyaml.Position{Line: 1, Col: 11},
			},
			want: "key: [value]",
		},
		"spanning key and value": {
			input: "key: value",
			rng: niceyaml.PositionRange{
				Start: niceyaml.Position{Line: 1, Col: 2},
				End:   niceyaml.Position{Line: 1, Col: 9},
			},
			// Each token portion is styled separately.
			want: "k[ey][:][ ][val]ue",
		},
		"multi-line range": {
			input: "first: 1\nsecond: 2",
			rng: niceyaml.PositionRange{
				Start: niceyaml.Position{Line: 1, Col: 8},
				End:   niceyaml.Position{Line: 2, Col: 9},
			},
			// Col 9 on line 2 is after the space (exclusive end).
			want: "first: [1]\n[second][:][ ]2",
		},
		"single character": {
			input: "key: value",
			rng: niceyaml.PositionRange{
				Start: niceyaml.Position{Line: 1, Col: 6},
				End:   niceyaml.Position{Line: 1, Col: 7},
			},
			want: "key: [v]alue",
		},
		"range including colon": {
			input: "key: value",
			rng: niceyaml.PositionRange{
				Start: niceyaml.Position{Line: 1, Col: 4},
				End:   niceyaml.Position{Line: 1, Col: 6},
			},
			// Colon and space are separate tokens.
			want: "key[:][ ]value",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tokens := lexer.Tokenize(tc.input)
			p := testPrinter()
			p.AddStyleToRange(testHighlightStyle(), tc.rng)

			got := p.PrintTokens(tokens)
			assert.Equal(t, tc.want, got)

			file := parseFile(t, tokens)
			gotFile := p.PrintFile(file)
			assert.Equal(t, got, gotFile)
		})
	}
}

func TestPrinter_AddStyleToRange_Overlapping(t *testing.T) {
	t.Parallel()

	input := "key: value"
	tokens := lexer.Tokenize(input)

	innerStyle := lipgloss.NewStyle().Transform(func(s string) string {
		return "<" + s + ">"
	})
	outerStyle := lipgloss.NewStyle().Transform(func(s string) string {
		return "[" + s + "]"
	})

	p := testPrinter()
	// Inner range: "val" (cols 6-8, exclusive end 9).
	p.AddStyleToRange(&innerStyle, niceyaml.PositionRange{
		Start: niceyaml.Position{Line: 1, Col: 6},
		End:   niceyaml.Position{Line: 1, Col: 9},
	})
	// Outer range: "alu" (cols 7-9, exclusive end 10).
	p.AddStyleToRange(&outerStyle, niceyaml.PositionRange{
		Start: niceyaml.Position{Line: 1, Col: 7},
		End:   niceyaml.Position{Line: 1, Col: 10},
	})

	got := p.PrintTokens(tokens)

	// Overlapping ranges compose transforms.
	// Col 6: inner only -> <v>.
	// Cols 7-8: both (inner first, outer wraps) -> [<al>].
	// Col 9: outer only -> [u].
	// Col 10: neither -> e.
	assert.Equal(t, "key: <v>[<al>][u]e", got)
}

func TestPrinter_AddStyleToRange_WithLineNumbers(t *testing.T) {
	t.Parallel()

	input := "first: 1\nsecond: 2"
	tokens := lexer.Tokenize(input)

	p := niceyaml.NewPrinter(
		niceyaml.WithStyles(niceyaml.Styles{}),
		niceyaml.WithStyle(lipgloss.NewStyle()),
		niceyaml.WithLineNumbers(),
		niceyaml.WithLineNumberStyle(lipgloss.NewStyle()),
		niceyaml.WithLinePrefix(""),
	)
	p.AddStyleToRange(testHighlightStyle(), niceyaml.PositionRange{
		Start: niceyaml.Position{Line: 1, Col: 8},
		End:   niceyaml.Position{Line: 1, Col: 9},
	})

	got := p.PrintTokens(tokens)

	// Line numbers added (no padding from empty style), range works.
	assert.Equal(t, "   1first: [1]\n   2second: 2", got)
}

func TestPrinter_ClearStyles_IncludesRanges(t *testing.T) {
	t.Parallel()

	input := "key: value"
	tokens := lexer.Tokenize(input)

	p := testPrinter()
	p.AddStyleToRange(testHighlightStyle(), niceyaml.PositionRange{
		Start: niceyaml.Position{Line: 1, Col: 6},
		End:   niceyaml.Position{Line: 1, Col: 11},
	})
	p.ClearStyles()

	// After clearing, no styles should be applied.
	assert.Equal(t, "key: value", p.PrintTokens(tokens))
}

func TestPrinter_PrintTokens_EmptyFile(t *testing.T) {
	t.Parallel()

	// Tokenize an empty string to simulate an empty YAML file.
	tokens := lexer.Tokenize("")

	p := testPrinter()
	got := p.PrintTokens(tokens)

	// Empty file should produce empty output.
	assert.Empty(t, got)

	file := parseFile(t, tokens)
	gotFile := p.PrintFile(file)
	assert.Equal(t, got, gotFile)
}

func TestNewPrinter(t *testing.T) {
	t.Parallel()

	input := `key: value
number: 42
bool: true
# comment`

	tokens := lexer.Tokenize(input)
	p := niceyaml.NewPrinter()
	got := p.PrintTokens(tokens)

	// Should contain ANSI escape codes.
	assert.Contains(t, got, "\x1b[")
	// Should contain original content.
	assert.Contains(t, got, "key")
	assert.Contains(t, got, "value")

	file := parseFile(t, tokens)
	gotFile := p.PrintFile(file)
	assert.Equal(t, got, gotFile)
}

func TestNewPrinter_WithStyles(t *testing.T) {
	t.Parallel()

	input := `key: value`
	tokens := lexer.Tokenize(input)

	s := niceyaml.Styles{
		niceyaml.StyleKey: lipgloss.NewStyle().Transform(func(s string) string {
			return "<key>" + s + "</key>"
		}),
		niceyaml.StyleString: lipgloss.NewStyle().Transform(func(s string) string {
			return "<str>" + s + "</str>"
		}),
	}
	p := niceyaml.NewPrinter(
		niceyaml.WithStyles(s),
		niceyaml.WithStyle(lipgloss.NewStyle()),
		niceyaml.WithLinePrefix(""),
	)

	got := p.PrintTokens(tokens)
	assert.Equal(t, "<key>key</key>: <str>value</str>", got)

	file := parseFile(t, tokens)
	gotFile := p.PrintFile(file)
	assert.Equal(t, got, gotFile)
}

func TestNewPrinter_EmptyStyles(t *testing.T) {
	t.Parallel()

	input := `key: value`
	tokens := lexer.Tokenize(input)

	// Empty Styles should not panic.
	s := niceyaml.Styles{}
	p := niceyaml.NewPrinter(niceyaml.WithStyles(s))
	got := p.PrintTokens(tokens)

	// Should still contain original content.
	assert.Contains(t, got, "key")
	assert.Contains(t, got, "value")

	file := parseFile(t, tokens)
	gotFile := p.PrintFile(file)
	assert.Equal(t, got, gotFile)
}

func TestPrinter_BlendColors_OverlayNoColor(t *testing.T) {
	t.Parallel()

	input := `key: value`
	tokens := lexer.Tokenize(input)

	// Use Styles with actual colors.
	s := niceyaml.Styles{
		niceyaml.StyleKey:    lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000")),
		niceyaml.StyleString: lipgloss.NewStyle().Foreground(lipgloss.Color("#00FF00")),
	}
	p := niceyaml.NewPrinter(
		niceyaml.WithStyles(s),
		niceyaml.WithStyle(lipgloss.NewStyle()),
		niceyaml.WithLinePrefix(""),
	)

	// Add a range style with NO colors (only a transform).
	// This tests blendColors when overlay (c2) has NoColor but base (c1) has color.
	transformOnlyStyle := lipgloss.NewStyle().Transform(func(s string) string {
		return "[" + s + "]"
	})
	p.AddStyleToRange(&transformOnlyStyle, niceyaml.PositionRange{
		Start: niceyaml.Position{Line: 1, Col: 6},
		End:   niceyaml.Position{Line: 1, Col: 11},
	})

	got := p.PrintTokens(tokens)
	// The value should be wrapped in brackets from the transform.
	assert.Contains(t, got, "[value]")
}

func TestPrinter_LineNumbers(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input             string
		want              string
		initialLineNumber int
	}{
		"single line": {
			input: "key: value",
			want:  "   1 key: value",
		},
		"multiple lines": {
			input: "key: value\nnumber: 42",
			want:  "   1 key: value\n   2 number: 42",
		},
		"custom initial line": {
			input:             "key: value\nnumber: 42",
			initialLineNumber: 10,
			want:              "  10 key: value\n  11 number: 42",
		},
		"multi-line value": {
			input: "key: |\n  line1\n  line2",
			want:  "   1 key: |\n   2   line1\n   3   line2",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tokens := lexer.Tokenize(tc.input)

			opts := []niceyaml.PrinterOption{
				niceyaml.WithStyles(niceyaml.Styles{}),
				niceyaml.WithStyle(lipgloss.NewStyle()),
				niceyaml.WithLineNumbers(),
				niceyaml.WithLinePrefix(""),
			}
			if tc.initialLineNumber > 0 {
				opts = append(opts, niceyaml.WithInitialLineNumber(tc.initialLineNumber))
			}

			p := niceyaml.NewPrinter(opts...)

			got := p.PrintTokens(tokens)
			assert.Equal(t, tc.want, got)

			file := parseFile(t, tokens)
			gotFile := p.PrintFile(file)
			assert.Equal(t, got, gotFile)
		})
	}
}

func TestPrinter_LineNumbers_ErrorToken(t *testing.T) {
	t.Parallel()

	input := `---
text: aaaa
text2: aaaa
 bbbb
 cccc
 dddd
 eeee
text3: ffff`

	tokens := lexer.Tokenize(input)

	p := niceyaml.NewPrinter(
		niceyaml.WithStyles(niceyaml.Styles{}),
		niceyaml.WithStyle(lipgloss.NewStyle()),
		niceyaml.WithLineNumbers(),
	)

	got, gotLine := p.PrintErrorToken(tokens[3], 3)

	// Should start from line 1.
	assert.Equal(t, 1, gotLine)
	// Should contain line numbers.
	assert.Contains(t, got, "   1  ")
	assert.Contains(t, got, "   2  ")
}

func TestPrinter_PrintTokenDiff(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		before       string
		after        string
		wantContains []string
		wantEmpty    bool
	}{
		"no changes": {
			before:       "key: value\n",
			after:        "key: value\n",
			wantContains: []string{"key: value"},
		},
		"simple addition": {
			before:       "key: value\n",
			after:        "key: value\nnew: line\n",
			wantContains: []string{"+new: line"},
		},
		"simple deletion": {
			before:       "key: value\nold: line\n",
			after:        "key: value\n",
			wantContains: []string{"-old: line"},
		},
		"modification": {
			before:       "key: old\n",
			after:        "key: new\n",
			wantContains: []string{"-key: old", "+key: new"},
		},
		"addition with context": {
			before: "line1: a\nline3: c\n",
			after:  "line1: a\nline2: b\nline3: c\n",
			wantContains: []string{
				" line1: a",
				"+line2: b",
				" line3: c",
			},
		},
		"deletion with context": {
			before: "line1: a\nline2: b\nline3: c\n",
			after:  "line1: a\nline3: c\n",
			wantContains: []string{
				" line1: a",
				"-line2: b",
				" line3: c",
			},
		},
		"multiline yaml modification": {
			before: "apiVersion: v1\nkind: Pod\nmetadata:\n  name: test\n",
			after:  "apiVersion: v1\nkind: Pod\nmetadata:\n  name: modified\n  labels:\n    app: test\n",
			wantContains: []string{
				" apiVersion: v1",
				" kind: Pod",
				" metadata:",
				"-  name: test",
				"+  name: modified",
				"+  labels:",
				"+    app: test",
			},
		},
		"multiple scattered changes": {
			before: "a: 1\nb: 2\nc: 3\nd: 4\n",
			after:  "a: 1\nb: changed\nc: 3\nd: 4\ne: 5\n",
			wantContains: []string{
				" a: 1",
				"-b: 2",
				"+b: changed",
				" c: 3",
				" d: 4",
				"+e: 5",
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := niceyaml.NewPrinter(
				niceyaml.WithStyles(niceyaml.Styles{}),
				niceyaml.WithStyle(lipgloss.NewStyle()),
			)
			before := lexer.Tokenize(tc.before)
			after := lexer.Tokenize(tc.after)

			got := p.PrintTokenDiff(before, after)

			for _, want := range tc.wantContains {
				assert.Contains(t, got, want)
			}
		})
	}
}

func TestPrinter_PrintTokenDiff_LineOrder(t *testing.T) {
	t.Parallel()

	// Test that deleted lines appear inline where they were removed.
	before := "a: 1\nb: 2\nc: 3\n"
	after := "a: 1\nc: 3\n"

	p := niceyaml.NewPrinter(
		niceyaml.WithStyles(niceyaml.Styles{}),
		niceyaml.WithStyle(lipgloss.NewStyle()),
	)
	got := p.PrintTokenDiff(lexer.Tokenize(before), lexer.Tokenize(after))

	lines := strings.Split(got, "\n")

	// Verify line order: " a: 1", "-b: 2", " c: 3".
	require.Len(t, lines, 3)
	assert.True(t, strings.HasPrefix(lines[0], " "), "first line should be unchanged")
	assert.True(t, strings.HasPrefix(lines[1], "-"), "second line should be deleted")
	assert.True(t, strings.HasPrefix(lines[2], " "), "third line should be unchanged")
}

func TestPrinter_PrintTokenDiff_ModificationOrder(t *testing.T) {
	t.Parallel()

	// Test that modifications show delete before insert.
	before := "key: old\n"
	after := "key: new\n"

	p := niceyaml.NewPrinter(
		niceyaml.WithStyles(niceyaml.Styles{}),
		niceyaml.WithStyle(lipgloss.NewStyle()),
	)
	got := p.PrintTokenDiff(lexer.Tokenize(before), lexer.Tokenize(after))

	lines := strings.Split(got, "\n")

	// Verify delete comes before insert.
	require.Len(t, lines, 2)
	assert.True(t, strings.HasPrefix(lines[0], "-"), "first line should be deleted")
	assert.True(t, strings.HasPrefix(lines[1], "+"), "second line should be inserted")
}

func TestPrinter_PrintTokenDiff_EmptyFiles(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		before       string
		after        string
		wantContains []string
		wantEmpty    bool
	}{
		"both empty": {
			before:    "",
			after:     "",
			wantEmpty: true,
		},
		"empty before, content after": {
			before:       "",
			after:        "key: value\n",
			wantContains: []string{"+key: value"},
		},
		"content before, empty after": {
			before:       "key: value\n",
			after:        "",
			wantContains: []string{"-key: value"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := niceyaml.NewPrinter(
				niceyaml.WithStyles(niceyaml.Styles{}),
				niceyaml.WithStyle(lipgloss.NewStyle()),
			)
			before := lexer.Tokenize(tc.before)
			after := lexer.Tokenize(tc.after)

			got := p.PrintTokenDiff(before, after)

			if tc.wantEmpty {
				assert.Empty(t, got)
				return
			}

			require.NotEmpty(t, tc.wantContains, "test case must specify wantContains")

			for _, want := range tc.wantContains {
				assert.Contains(t, got, want)
			}
		})
	}
}

func TestPrinter_WordWrap(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  string
		width int
	}{
		"no wrap when disabled": {
			input: "key: value",
			width: 0,
			want:  "key: value",
		},
		"simple wrap": {
			input: "key: this is a very long value that should wrap",
			width: 20,
			want:  "key: this is a very\nlong value that\nshould wrap",
		},
		"wrap on slash": {
			input: "path: /usr/local/bin/something",
			width: 20,
			want:  "path: /usr/local/\nbin/something",
		},
		"wrap on hyphen": {
			input: "name: very-long-hyphenated-name",
			width: 20,
			want:  "name: very-long-\nhyphenated-name",
		},
		"short content no wrap": {
			input: "key: value",
			width: 50,
			want:  "key: value",
		},
		"multi-line content": {
			input: "key: value\nanother: long value that should wrap here",
			width: 20,
			want:  "key: value\nanother: long value\nthat should wrap\nhere",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tokens := lexer.Tokenize(tc.input)

			p := niceyaml.NewPrinter(
				niceyaml.WithStyles(niceyaml.Styles{}),
				niceyaml.WithStyle(lipgloss.NewStyle()),
				niceyaml.WithLinePrefix(""),
			)
			if tc.width > 0 {
				p.SetWidth(tc.width)
			}

			got := p.PrintTokens(tokens)
			assert.Equal(t, tc.want, got)

			file := parseFile(t, tokens)
			gotFile := p.PrintFile(file)
			assert.Equal(t, got, gotFile)
		})
	}
}

func TestPrinter_WordWrap_WithLineNumbers(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  string
		width int
	}{
		"wrapped line continuation marker": {
			input: "key: this is a very long value",
			width: 22,
			// Wraps at word boundaries within width.
			// Width 22 - 6 (line number) = 16 for content.
			want: "   1key: this is a\n   -very long value",
		},
		"multiple wrapped lines": {
			input: "first: short\nsecond: this is a very long line that wraps",
			width: 30,
			// First line fits, second line wraps.
			// Width 30 - 6 (line number) = 24 for content.
			want: "   1first: short\n   2second: this is a very\n   -long line that wraps",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tokens := lexer.Tokenize(tc.input)

			p := niceyaml.NewPrinter(
				niceyaml.WithStyles(niceyaml.Styles{}),
				niceyaml.WithStyle(lipgloss.NewStyle()),
				niceyaml.WithLineNumbers(),
				niceyaml.WithLineNumberStyle(lipgloss.NewStyle()),
				niceyaml.WithLinePrefix(""),
			)
			p.SetWidth(tc.width)

			got := p.PrintTokens(tokens)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPrinter_PrintTokenDiff_WithWordWrap(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		before       string
		after        string
		wantContains []string
		width        int
	}{
		"diff lines wrap correctly": {
			before: "key: short\n",
			after:  "key: this is a very long value that should wrap\n",
			width:  30,
			wantContains: []string{
				"-key: short",
				"+key: this is a very long",
				" value that should wrap",
			},
		},
		"wrapped diff continuation": {
			before: "key: original value\n",
			after:  "key: new very long value that definitely wraps\n",
			width:  25,
			wantContains: []string{
				"-key: original value",
				"+key: new very long value",
				" that definitely wraps",
			},
		},
		"modification with wrap": {
			before: "name: old-hyphenated-name-value\n",
			after:  "name: new-hyphenated-name-value\n",
			width:  20,
			wantContains: []string{
				"-name: old-",
				"+name: new-",
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := niceyaml.NewPrinter(
				niceyaml.WithStyles(niceyaml.Styles{}),
				niceyaml.WithStyle(lipgloss.NewStyle()),
			)
			p.SetWidth(tc.width)

			before := lexer.Tokenize(tc.before)
			after := lexer.Tokenize(tc.after)

			got := p.PrintTokenDiff(before, after)

			for _, want := range tc.wantContains {
				assert.Contains(t, got, want)
			}
		})
	}
}

func TestPrinter_PrintTokenDiff_WithLineNumbers(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		before       string
		after        string
		wantContains []string
	}{
		"modification shows correct line numbers": {
			// Delete shows beforeLine (2), insert shows afterLine (2).
			before: "key: value\nold: line\n",
			after:  "key: value\nnew: line\n",
			wantContains: []string{
				"   1  key: value",
				"   2 -old: line",
				"   2 +new: line",
			},
		},
		"addition shows afterLine numbers": {
			// Equal lines show afterLine, inserted line shows afterLine.
			before: "a: 1\nc: 3\n",
			after:  "a: 1\nb: 2\nc: 3\n",
			wantContains: []string{
				"   1  a: 1",
				"   2 +b: 2",
				"   3  c: 3",
			},
		},
		"deletion shows beforeLine numbers": {
			// Equal lines show afterLine, deleted line shows beforeLine.
			before: "a: 1\nb: 2\nc: 3\n",
			after:  "a: 1\nc: 3\n",
			wantContains: []string{
				"   1  a: 1",
				"   2 -b: 2",
				"   2  c: 3",
			},
		},
		"multiple changes track line numbers correctly": {
			before: "line1: a\nline2: b\nline3: c\n",
			after:  "line1: x\nline2: b\nline3: y\n",
			wantContains: []string{
				"   1 -line1: a",
				"   1 +line1: x",
				"   2  line2: b",
				"   3 -line3: c",
				"   3 +line3: y",
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := niceyaml.NewPrinter(
				niceyaml.WithStyles(niceyaml.Styles{}),
				niceyaml.WithStyle(lipgloss.NewStyle()),
				niceyaml.WithLineNumbers(),
			)

			before := lexer.Tokenize(tc.before)
			after := lexer.Tokenize(tc.after)

			got := p.PrintTokenDiff(before, after)

			for _, want := range tc.wantContains {
				assert.Contains(t, got, want)
			}
		})
	}
}

func TestPrinter_PrintTokenDiff_CustomPrefixes(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		insertedPrefix string
		deletedPrefix  string
		before         string
		after          string
		wantContains   []string
		wantNotContain []string
	}{
		"custom inserted prefix": {
			insertedPrefix: ">>",
			deletedPrefix:  "-",
			before:         "key: old\n",
			after:          "key: old\nnew: line\n",
			wantContains:   []string{">>new: line"},
			wantNotContain: []string{"+new: line"},
		},
		"custom deleted prefix": {
			insertedPrefix: "+",
			deletedPrefix:  "<<",
			before:         "key: old\nold: line\n",
			after:          "key: old\n",
			wantContains:   []string{"<<old: line"},
			wantNotContain: []string{"-old: line"},
		},
		"both custom prefixes": {
			insertedPrefix: "ADD:",
			deletedPrefix:  "DEL:",
			before:         "key: old\n",
			after:          "key: new\n",
			wantContains:   []string{"DEL:key: old", "ADD:key: new"},
			wantNotContain: []string{"-key: old", "+key: new"},
		},
		"empty prefixes": {
			insertedPrefix: "",
			deletedPrefix:  "",
			before:         "a: 1\n",
			after:          "a: 2\n",
			wantContains:   []string{"a: 1", "a: 2"},
		},
		"multi-character prefixes with context": {
			insertedPrefix: "[+]",
			deletedPrefix:  "[-]",
			before:         "line1: a\nline2: b\nline3: c\n",
			after:          "line1: a\nline2: x\nline3: c\n",
			wantContains: []string{
				" line1: a",
				"[-]line2: b",
				"[+]line2: x",
				" line3: c",
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := niceyaml.NewPrinter(
				niceyaml.WithStyles(niceyaml.Styles{}),
				niceyaml.WithStyle(lipgloss.NewStyle()),
				niceyaml.WithLineInsertedPrefix(tc.insertedPrefix),
				niceyaml.WithLineDeletedPrefix(tc.deletedPrefix),
			)

			before := lexer.Tokenize(tc.before)
			after := lexer.Tokenize(tc.after)

			got := p.PrintTokenDiff(before, after)

			for _, want := range tc.wantContains {
				assert.Contains(t, got, want)
			}

			for _, notWant := range tc.wantNotContain {
				assert.NotContains(t, got, notWant)
			}
		})
	}
}

func TestPrinter_TokenTypes(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input        string
		wantContains []string
	}{
		"null type": {
			input:        "value: null",
			wantContains: []string{"value", "null"},
		},
		"tilde null": {
			input:        "value: ~",
			wantContains: []string{"value", "~"},
		},
		"implicit null": {
			input:        "value:",
			wantContains: []string{"value", ":"},
		},
		"directive": {
			input:        "%YAML 1.2\n---\nkey: value",
			wantContains: []string{"%YAML 1.2", "---", "key", "value"},
		},
		"tag": {
			input:        "tagged: !custom value",
			wantContains: []string{"tagged", "!custom", "value"},
		},
		"merge key": {
			input:        "base: &base\n  a: 1\nmerged:\n  <<: *base\n  b: 2",
			wantContains: []string{"base", "&base", "<<", "*base", "merged"},
		},
		"document end": {
			input:        "key: value\n...",
			wantContains: []string{"key", "value", "..."},
		},
		"block scalar literal": {
			input:        "text: |\n  line1\n  line2",
			wantContains: []string{"text", "|", "line1", "line2"},
		},
		"block scalar folded": {
			input:        "text: >\n  line1\n  line2",
			wantContains: []string{"text", ">", "line1", "line2"},
		},
		"mapping key indicator": {
			input:        "? explicit key\n: value",
			wantContains: []string{"?", "explicit key", ":", "value"},
		},
		"flow sequence": {
			input:        "items: [a, b, c]",
			wantContains: []string{"items", "[", "a", "b", "c", "]"},
		},
		"flow mapping": {
			input:        "map: {a: 1, b: 2}",
			wantContains: []string{"map", "{", "a", "b", "}"},
		},
		"integer types": {
			input:        "decimal: 42\noctal: 0o77\nhex: 0xFF\nbinary: 0b1010",
			wantContains: []string{"42", "0o77", "0xFF", "0b1010"},
		},
		"float types": {
			input:        "float: 3.14\ninf: .inf\nnan: .nan",
			wantContains: []string{"3.14", ".inf", ".nan"},
		},
		"comment": {
			input:        "key: value # comment",
			wantContains: []string{"key", "value", "# comment"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tokens := lexer.Tokenize(tc.input)
			p := testPrinter()

			got := p.PrintTokens(tokens)

			for _, want := range tc.wantContains {
				assert.Contains(t, got, want)
			}

			file := parseFile(t, tokens)
			gotFile := p.PrintFile(file)
			assert.Equal(t, got, gotFile)
		})
	}
}

func TestPrinter_PrintErrorToken_InitialLineNumberZero(t *testing.T) {
	t.Parallel()

	input := "line1: a\nline2: b\nline3: c"
	tokens := lexer.Tokenize(input)

	p := niceyaml.NewPrinter(
		niceyaml.WithInitialLineNumber(0),
		niceyaml.WithLineNumbers(),
		niceyaml.WithStyles(niceyaml.Styles{}),
		niceyaml.WithStyle(lipgloss.NewStyle()),
		niceyaml.WithLinePrefix(""),
	)

	// Error on line 2, show 1 line of context.
	got, minLine := p.PrintErrorToken(tokens[2], 1)

	// When initialLineNumber < 1, it should fall back to minLine.
	assert.Equal(t, 1, minLine)
	// Line numbers should start from minLine (1), not 0.
	assert.Contains(t, got, "   1")
}

func TestPrinter_PrintErrorToken_FirstToken(t *testing.T) {
	t.Parallel()

	input := "key: value\nanother: line"
	tokens := lexer.Tokenize(input)

	p := testPrinter()

	// Call on the first token - tests extractTokensInRange when Prev is nil.
	got, minLine := p.PrintErrorToken(tokens[0], 1)

	assert.Equal(t, 1, minLine)
	assert.Contains(t, got, "key")
	assert.Contains(t, got, "value")
}

func TestPrinter_PrintFile_MultiDocument(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  string
	}{
		"two documents": {
			input: "doc1: value1\n---\ndoc2: value2",
			want:  "doc1: value1\n---\ndoc2: value2",
		},
		"three documents": {
			input: "first: 1\n---\nsecond: 2\n---\nthird: 3",
			want:  "first: 1\n---\nsecond: 2\n---\nthird: 3",
		},
		"documents with header": {
			input: "---\nkey: value",
			want:  "---\nkey: value",
		},
		"documents with footer": {
			input: "key: value\n...",
			want:  "key: value\n...",
		},
		"document with header and footer": {
			input: "---\nkey: value\n...",
			want:  "---\nkey: value\n...",
		},
		"multiple docs with headers and footers": {
			input: "---\ndoc1: value1\n...\n---\ndoc2: value2\n...",
			want:  "---\ndoc1: value1\n...\n---\ndoc2: value2\n...",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tokens := lexer.Tokenize(tc.input)
			p := testPrinter()

			got := p.PrintTokens(tokens)
			assert.Equal(t, tc.want, got)

			file := parseFile(t, tokens)
			gotFile := p.PrintFile(file)
			assert.Equal(t, got, gotFile)
		})
	}
}
