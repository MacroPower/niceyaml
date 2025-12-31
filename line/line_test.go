package line_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macropower/niceyaml/line"
)

// dumpTokens concatenates all token Origins into a single string.
func dumpTokens(tks token.Tokens) string {
	var sb strings.Builder
	for _, tk := range tks {
		sb.WriteString(tk.Origin)
	}

	return sb.String()
}

// assertTokensEqual compares all fields of two token slices.
func assertTokensEqual(t *testing.T, want, got token.Tokens) {
	t.Helper()

	if len(want) != len(got) {
		require.Fail(t, fmt.Sprintf("token count mismatch: want %d, got %d", len(want), len(got)),
			"want tokens:\n%s\ngot tokens:\n%s", formatTokens(want), formatTokens(got))
	}

	for i := range want {
		assertTokenEqual(t, want[i], got[i], fmt.Sprintf("token %d", i))
	}
}

// assertTokenEqual compares all fields of two tokens.
func assertTokenEqual(t *testing.T, want, got *token.Token, prefix string) {
	t.Helper()

	var diffs []string

	if want.Type != got.Type {
		diffs = append(diffs, "Type")
	}
	if want.Value != got.Value {
		diffs = append(diffs, "Value")
	}
	if want.Origin != got.Origin {
		diffs = append(diffs, "Origin")
	}
	if want.CharacterType != got.CharacterType {
		diffs = append(diffs, "CharacterType")
	}
	if want.Indicator != got.Indicator {
		diffs = append(diffs, "Indicator")
	}
	if !positionsEqual(want.Position, got.Position) {
		diffs = append(diffs, "Position")
	}

	if len(diffs) > 0 {
		assert.Fail(t, prefix+" mismatch",
			"want:\n%s\ngot:\n%s\ndifferences: %s",
			formatToken(want), formatToken(got), strings.Join(diffs, ", "))
	}
}

// positionsEqual compares two token.Position values for equality.
func positionsEqual(want, got *token.Position) bool {
	if want == nil && got == nil {
		return true
	}
	if want == nil || got == nil {
		return false
	}

	return want.Line == got.Line &&
		want.Column == got.Column &&
		want.Offset == got.Offset &&
		want.IndentNum == got.IndentNum &&
		want.IndentLevel == got.IndentLevel
}

// formatPosition formats a token.Position for debug output.
func formatPosition(pos *token.Position) string {
	if pos == nil {
		return "<nil>"
	}

	return fmt.Sprintf("%d:%d (Offset: %d, Indent: %d/%d)",
		pos.Line, pos.Column, pos.Offset, pos.IndentNum, pos.IndentLevel)
}

// formatToken formats a token.Token for detailed debug output.
func formatToken(tk *token.Token) string {
	if tk == nil {
		return "<nil>"
	}

	return fmt.Sprintf(`  Type:        %s
  Value:       %q
  Origin:      %q
  Position:    %s
  Indicator:   %s
  CharType:    %s`,
		tk.Type,
		tk.Value,
		tk.Origin,
		formatPosition(tk.Position),
		tk.Indicator,
		tk.CharacterType)
}

// formatTokens formats a slice of tokens for debug output.
func formatTokens(tks token.Tokens) string {
	if len(tks) == 0 {
		return "  <empty>"
	}

	var sb strings.Builder
	for i, tk := range tks {
		if i > 0 {
			sb.WriteString("\n")
		}

		sb.WriteString(fmt.Sprintf("[%d]\n%s", i, formatToken(tk)))
	}

	return sb.String()
}

// linesContent returns the combined content of all Lines as a string.
// Lines are joined with newlines.
func linesContent(lines line.Lines) string {
	if len(lines) == 0 {
		return ""
	}

	sb := strings.Builder{}
	for i, l := range lines {
		if i > 0 {
			sb.WriteByte('\n')
		}

		sb.WriteString(l.Content())
	}

	return sb.String()
}

func TestNewLines_Value_Roundtrip(t *testing.T) {
	t.Parallel()

	// Read testdata file for comprehensive round-trip testing.
	fullYAML, err := os.ReadFile(filepath.Join("..", "testdata", "full.yaml"))
	require.NoError(t, err)

	tcs := map[string]struct {
		input string
	}{
		"testdata/full.yaml": {
			input: string(fullYAML),
		},
		"empty input": {
			input: "",
		},
		"single key-value": {
			input: "key: value\n",
		},
		"single key-value no newline": {
			input: "key: value",
		},
		"multiple key-values": {
			input: `first: 1
second: 2
third: 3
`,
		},
		"nested map": {
			input: `parent:
  child: value
  sibling: another
`,
		},
		"deeply nested": {
			input: `level1:
  level2:
    level3:
      value: deep
`,
		},
		"simple list": {
			input: `items:
  - one
  - two
  - three
`,
		},
		"list of maps": {
			input: `items:
  - name: first
    value: 1
  - name: second
    value: 2
`,
		},
		"map with list values": {
			input: `config:
  ports:
    - 8080
    - 8443
  hosts:
    - localhost
    - example.com
`,
		},
		"inline list": {
			input: "items: [a, b, c]\n",
		},
		"inline map": {
			input: "config: {key: value, other: data}\n",
		},
		"double quoted string": {
			input: `message: "hello world"
`,
		},
		"single quoted string": {
			input: `message: 'hello world'
`,
		},
		"quoted with special chars": {
			input: `special: "line1\nline2"
`,
		},
		"literal block": {
			input: `script: |
  line1
  line2
  line3
`,
		},
		"folded block": {
			input: `description: >
  This is a long
  description that
  spans multiple lines.
`,
		},
		"comment only": {
			input: "# This is a comment\n",
		},
		"inline comment": {
			input: "key: value # inline comment\n",
		},
		"multiple comments": {
			input: `# Header comment
key: value
# Middle comment
other: data
`,
		},
		"extra whitespace": {
			input: `key:   value
other:    data
`,
		},
		"indented values": {
			input: `map:
    deeply:
        indented:
            value: here
`,
		},
		"boolean values": {
			input: `enabled: true
disabled: false
`,
		},
		"numeric values": {
			input: `integer: 42
float: 3.14
negative: -10
`,
		},
		"null value": {
			input: "empty: null\n",
		},
		"anchor and alias": {
			input: `defaults: &defaults
  timeout: 30
production:
  <<: *defaults
  timeout: 60
`,
		},
		"multiline document": {
			input: `---
document: content
...
`,
		},
		"kubernetes manifest": {
			input: `apiVersion: v1
kind: ConfigMap
metadata:
  name: example
  namespace: default
data:
  key1: value1
  key2: value2
`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			input := lexer.Tokenize(tc.input)
			lines := line.NewLines(input)
			gotTokens := lines.Tokens()
			gotContent := linesContent(lines)

			assert.Equal(t, dumpTokens(input), dumpTokens(gotTokens))
			assert.Equal(t, strings.TrimSuffix(dumpTokens(input), "\n"), gotContent)
		})
	}
}

func TestNewLines_FieldRoundtrip(t *testing.T) {
	t.Parallel()

	tcs := map[string]string{
		// Simple cases.
		"simple key-value": "key: value\n",
		"multiple keys":    "first: 1\nsecond: 2\nthird: 3\n",
		"nested map":       "parent:\n  child: value\n",
		"simple list":      "items:\n  - one\n  - two\n",
		"inline map":       "config: {key: value}\n",
		"inline list":      "items: [a, b, c]\n",
		"boolean values":   "enabled: true\ndisabled: false\n",
		"numeric values":   "integer: 42\nfloat: 3.14\n",
		"double quoted":    "message: \"hello world\"\n",
		"single quoted":    "message: 'hello world'\n",
		"comment only":     "# This is a comment\n",
		"inline comment":   "key: value # inline comment\n",
		"anchor and alias": "defaults: &defaults\n  timeout: 30\nproduction:\n  <<: *defaults\n",

		// Split token cases (literal/folded blocks).
		"literal block":      "script: |\n  line1\n  line2\n",
		"folded block":       "desc: >\n  first\n  second\n",
		"literal block long": "data: |\n  a\n  b\n  c\n  d\n",

		// Block scalar variants.
		"literal strip":  "text: |-\n  line1\n  line2\n",
		"literal keep":   "text: |+\n  line1\n  line2\n\n",
		"folded strip":   "text: >-\n  line1\n  line2\n",
		"folded keep":    "text: >+\n  line1\n  line2\n\n",
		"literal indent": "code: |2\n    indented\n    content\n",

		// Document markers.
		"document start":          "---\nkey: value\n",
		"document end":            "key: value\n...\n",
		"multi-document":          "---\ndoc: 1\n...\n---\ndoc: 2\n",
		"document with directive": "%YAML 1.2\n---\nkey: value\n",

		// Tags.
		"explicit string tag": "value: !!str 12345\n",
		"explicit int tag":    "value: !!int \"42\"\n",
		"explicit float tag":  "value: !!float \"3.14\"\n",
		"explicit bool tag":   "value: !!bool \"yes\"\n",
		"explicit null tag":   "value: !!null \"\"\n",
		"custom tag":          "value: !custom data\n",
		"tag with flow map":   "price: !money {amount: 10, currency: USD}\n",

		// Numeric formats.
		"hex number":        "value: 0xFF\n",
		"octal number":      "value: 0o77\n",
		"scientific":        "value: 6.022e23\n",
		"negative float":    "value: -18.5\n",
		"infinity":          "value: .inf\n",
		"negative infinity": "value: -.inf\n",
		"not a number":      "value: .nan\n",

		// Special values.
		"null tilde":  "value: ~\n",
		"null word":   "value: null\n",
		"empty value": "key:\n",

		// Anchors and aliases.
		"anchor definition":  "base: &base\n  key: value\n",
		"alias reference":    "ref: *base\n",
		"merge key":          "merged:\n  <<: *base\n  extra: data\n",
		"anchor on sequence": "list: &items\n  - one\n  - two\n",

		// Flow styles.
		"nested flow map":       "data: {outer: {inner: value}}\n",
		"nested flow list":      "data: [[1, 2], [3, 4]]\n",
		"mixed flow":            "data: {list: [a, b], map: {k: v}}\n",
		"flow with anchor":      "data: {key: &val value, ref: *val}\n",
		"flow map in block seq": "items:\n  - {name: a, value: 1}\n",

		// Complex structures.
		"deeply nested": "a:\n  b:\n    c:\n      d: value\n",
		"list of maps":  "items:\n  - name: one\n    value: 1\n  - name: two\n    value: 2\n",
		"map of lists":  "groups:\n  a: [1, 2]\n  b: [3, 4]\n",

		// Quoted strings with escapes.
		"double quote escapes": "text: \"line1\\nline2\\ttab\"\n",
		"unicode escape":       "text: \"\\u65E5\\u672C\"\n",
		"single quote literal": "text: 'no \\n escape'\n",
		"quote in single":      "text: 'it''s quoted'\n",
		"quote in double":      "text: \"say \\\"hello\\\"\"\n",

		// Unicode content.
		"unicode value":   "name: 日本語\n",
		"unicode key":     "日本語: value\n",
		"emoji":           "icon: 🎉\n",
		"mixed scripts":   "text: Hello 世界 مرحبا\n",
		"combining marks": "text: Việt Nam\n",
		"rtl text":        "arabic: مرحبا\n",
		"emoji sequence":  "family: 👨‍👩‍👧‍👦\n",
		"flag emoji":      "flag: 🇯🇵\n",

		// Edge cases.
		"colon in value":     "text: \"Note: important\"\n",
		"hash in value":      "text: \"Item #1\"\n",
		"special yaml chars": "text: \"[not] {a} list\"\n",
		"multiline comment":  "# line 1\n# line 2\nkey: value\n",
		"blank lines":        "key1: value1\n\nkey2: value2\n",
		"trailing comment":   "key: value # comment\n",

		// Directives and custom tags.
		"tag directive":        "%TAG !custom! tag:example.com,2024:\n---\nvalue: !custom!price 10\n",
		"tag with nested flow": "price: !custom!price { amount: 12.50, currency: EUR }\n",

		// Complex keys.
		"emoji as key":           "🍣:\n  value: sushi\n",
		"quoted unicode key":     "\"東京\":\n  flagship: true\n",
		"explicit key indicator": "? complex key\n: value\n",

		// Advanced numeric formats.
		"underscore separator": "value: 1_000_000\n",
		"date":                 "date: 2024-03-15\n",
		"timestamp timezone":   "time: 2024-11-20T14:30:00+01:00\n",

		// Binary data.
		"binary tag": "data: !!binary |\n  R0lGODlhAQABAIAAAAAAAP///w==\n",

		// Merge key variants.
		"merge multiple anchors": "base1: &b1\n  a: 1\nbase2: &b2\n  b: 2\nmerged:\n  <<: [*b1, *b2]\n",

		// Advanced block scalars.
		"literal indent strip":         "code: |2-\n    indented\n    content\n",
		"folded indent":                "text: >2\n    folded\n    indented\n",
		"comment after literal header": "key: | # this is a comment\n  line1\n  line2\n",
		"comment after folded header":  "key: > # this is a comment\n  line1\n  line2\n",
		"folded with blank lines":      "text: >\n  first\n\n  second\n",

		// Complex flow structures.
		"flow map in block seq nested": "items:\n  - Pizza: { size: large, toppings: [a, b, c] }\n",
		"nested flow with anchors":     "data: { key: &v value, list: [*v, *v] }\n",
		"deep nested flow":             "a: {b: {c: {d: {e: value}}}}\n",

		// Special unicode characters.
		"zwsp":                  "text: \"foo\u200Bbar\"\n",
		"zwnj":                  "text: \"می\u200Cروم\"\n",
		"zwj emoji":             "icon: 👨\u200D👩\u200D👧\u200D👦\n",
		"box drawing":           "art: ╔═══╗\n",
		"math symbols":          "formula: \"E = mc²\"\n",
		"subscript superscript": "water: H₂O\n",
		"skin tone emoji":       "wave: 👋🏽\n",
		"keycap emoji":          "number: 1️⃣\n",
		"combining zalgo":       "text: \"H̷̭͂ë̶̬l̷̰̐l̴̮̈́o̷̱͝\"\n",

		// Bidirectional text.
		"bidi mixed":   "text: \"Hello مرحبا World\"\n",
		"rtl with ltr": "review: \"המסעדה is great!\"\n",

		// Greek letters.
		"greek text": "letters: [α, β, γ, δ]\n",
		"greek key":  "Ελληνικά: Greek\n",

		// More edge cases.
		"ampersand in value":     "text: \"Tom & Jerry\"\n",
		"asterisk in value":      "rating: \"5* rating\"\n",
		"pipe in value":          "options: \"A | B\"\n",
		"greater in value":       "compare: \"A > B\"\n",
		"question in value":      "ask: \"Why? Because!\"\n",
		"empty document":         "---\n...\n",
		"CRLF line endings":      "key: value\r\nother: data\r\n",
		"plain multiline string": "key: this is\n  a multiline\n  plain string\n",
	}

	for name, input := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			original := lexer.Tokenize(input)
			lines := line.NewLines(original)
			gotTokens := lines.Tokens()

			assertTokensEqual(t, original, gotTokens)
		})
	}
}

func TestNewLines_PerLine(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input     string
		wantLines []string
	}{
		"literal block": {
			input: `script: |
  line1
  line2
  line3
`,
			wantLines: []string{
				"   1 | script: |",
				"   2 |   line1",
				"   3 |   line2",
				"   4 |   line3",
			},
		},
		"folded block": {
			input: `desc: >
  part1
  part2
`,
			wantLines: []string{
				"   1 | desc: >",
				"   2 |   part1",
				"   3 |   part2",
			},
		},
		"single key-value": {
			// Note: lexer doesn't preserve trailing newline on final simple values.
			input: "key: value\n",
			wantLines: []string{
				"   1 | key: value",
			},
		},
		"multiple key-values": {
			input: `first: 1
second: 2
`,
			wantLines: []string{
				"   1 | first: 1",
				"   2 | second: 2",
			},
		},
		"nested map": {
			input: `parent:
  child: value
`,
			wantLines: []string{
				"   1 | parent:",
				"   2 |   child: value",
			},
		},
		"quoted with escaped newline": {
			input: `special: "line1\nline2"
`,
			wantLines: []string{
				`   1 | special: "line1\nline2"`,
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			input := lexer.Tokenize(tc.input)
			lines := line.NewLines(input)

			require.Len(t, lines, len(tc.wantLines), "wrong number of lines")

			for i, want := range tc.wantLines {
				assert.Equal(t, want, lines[i].String(), "line %d", i)
			}
		})
	}
}

func TestNewLines_NonStandardLineNumbers(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input         string
		wantLineNums  []int
		startLine     int
		wantLineCount int
	}{
		"tokens starting at line 10": {
			input: `key: value
other: data
`,
			startLine:     10,
			wantLineNums:  []int{10, 11},
			wantLineCount: 2,
		},
		"tokens starting at line 100": {
			input:         "single: line\n",
			startLine:     100,
			wantLineNums:  []int{100},
			wantLineCount: 1,
		},
		"nested map starting at line 50": {
			input: `parent:
  child: value
  sibling: another
`,
			startLine:     50,
			wantLineNums:  []int{50, 51, 52},
			wantLineCount: 3,
		},
		"literal block starting at line 20": {
			input: `script: |
  line1
  line2
`,
			startLine:     20,
			wantLineNums:  []int{20, 21, 22},
			wantLineCount: 3,
		},
		"folded block starting at line 30": {
			input: `desc: >
  part1
  part2
`,
			startLine:     30,
			wantLineNums:  []int{30, 31, 32},
			wantLineCount: 3,
		},
		"list starting at line 25": {
			input: `items:
  - one
  - two
`,
			startLine:     25,
			wantLineNums:  []int{25, 26, 27},
			wantLineCount: 3,
		},
		"comment and key at line 15": {
			input: `# comment
key: value
`,
			startLine:     15,
			wantLineNums:  []int{15, 16},
			wantLineCount: 2,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// Tokenize and adjust line numbers to simulate non-line-1 start.
			tks := lexer.Tokenize(tc.input)

			offset := tc.startLine - 1
			for _, tk := range tks {
				if tk.Position != nil {
					tk.Position.Line += offset
				}
			}

			lines := line.NewLines(tks)

			require.Len(t, lines, tc.wantLineCount, "wrong number of lines")

			for i, wantNum := range tc.wantLineNums {
				assert.Equal(t, wantNum, lines[i].Number(), "line %d has wrong number", i)
			}

			// Verify round-trip: dumping tokens should preserve content.
			gotTokens := lines.Tokens()
			assert.Equal(t, dumpTokens(tks), dumpTokens(gotTokens), "round-trip content mismatch")
		})
	}
}

func TestNewLines_GappedLineNumbers(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		buildTokens   func() token.Tokens
		wantLineNums  []int
		wantLineCount int
	}{
		"gap between two sections (lines 10-11, 40-41)": {
			buildTokens: func() token.Tokens {
				// Build tokens for lines 10-11.
				tks1 := lexer.Tokenize("key1: value1\nkey2: value2\n")
				for _, tk := range tks1 {
					if tk.Position != nil {
						tk.Position.Line += 9 // Shift to lines 10, 11.
					}
				}
				// Build tokens for lines 40-41.
				tks2 := lexer.Tokenize("key3: value3\nkey4: value4\n")
				for _, tk := range tks2 {
					if tk.Position != nil {
						tk.Position.Line += 39 // Shift to lines 40, 41.
					}
				}
				// Combine them.
				combined := token.Tokens{}

				for _, tk := range tks1 {
					combined.Add(tk)
				}

				for _, tk := range tks2 {
					combined.Add(tk)
				}

				return combined
			},
			wantLineNums:  []int{10, 11, 40, 41},
			wantLineCount: 4,
		},
		"large gap (lines 5, 100)": {
			buildTokens: func() token.Tokens {
				tks1 := lexer.Tokenize("first: value\n")

				for _, tk := range tks1 {
					if tk.Position != nil {
						tk.Position.Line += 4 // Shift to line 5.
					}
				}

				tks2 := lexer.Tokenize("second: value\n")

				for _, tk := range tks2 {
					if tk.Position != nil {
						tk.Position.Line += 99 // Shift to line 100.
					}
				}

				combined := token.Tokens{}

				for _, tk := range tks1 {
					combined.Add(tk)
				}

				for _, tk := range tks2 {
					combined.Add(tk)
				}

				return combined
			},
			wantLineNums:  []int{5, 100},
			wantLineCount: 2,
		},
		"multiple gaps (lines 10, 20, 30)": {
			buildTokens: func() token.Tokens {
				combined := token.Tokens{}

				for i, lineNum := range []int{10, 20, 30} {
					tks := lexer.Tokenize("key: value\n")

					for _, tk := range tks {
						if tk.Position != nil {
							tk.Position.Line = lineNum + (tk.Position.Line - 1)
						}
					}

					// Change the key to be unique.
					if len(tks) > 0 {
						tks[0].Value = "key" + string(rune('a'+i))
						tks[0].Origin = "key" + string(rune('a'+i))
					}

					for _, tk := range tks {
						combined.Add(tk)
					}
				}

				return combined
			},
			wantLineNums:  []int{10, 20, 30},
			wantLineCount: 3,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := tc.buildTokens()
			lines := line.NewLines(tks)

			require.Len(t, lines, tc.wantLineCount, "wrong number of lines")

			for i, wantNum := range tc.wantLineNums {
				assert.Equal(t, wantNum, lines[i].Number(), "line %d has wrong number", i)
			}

			// Verify Prev/Next linking works correctly across gaps.
			gotTokens := lines.Tokens()
			require.NotEmpty(t, gotTokens, "expected non-empty tokens")

			// Verify forward traversal works.
			forwardCount := 0

			for tk := gotTokens[0]; tk != nil; tk = tk.Next {
				forwardCount++
			}

			assert.Equal(t, len(gotTokens), forwardCount, "forward traversal count mismatch")

			// Verify backward traversal works.
			lastTk := gotTokens[len(gotTokens)-1]

			backwardCount := 0

			for tk := lastTk; tk != nil; tk = tk.Prev {
				backwardCount++
			}

			assert.Equal(t, len(gotTokens), backwardCount, "backward traversal count mismatch")
		})
	}
}

func TestLine_String_Annotation(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		want       string
		annotation line.Annotation
	}{
		"no annotation": {
			annotation: line.Annotation{},
			want:       "   1 | key: value",
		},
		"annotation at column 1": {
			annotation: line.Annotation{Content: "error here", Column: 1},
			want: `   1 | key: value
   1 | ^ error here`,
		},
		"annotation at column 5": {
			annotation: line.Annotation{Content: "note", Column: 5},
			want: `   1 | key: value
   1 |     ^ note`,
		},
		"annotation at column 0": {
			annotation: line.Annotation{Content: "edge", Column: 0},
			want: `   1 | key: value
   1 | ^ edge`,
		},
		"large column": {
			annotation: line.Annotation{Content: "far", Column: 20},
			want: `   1 | key: value
   1 |                    ^ far`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// Create a line with a simple token.
			tks := lexer.Tokenize("key: value\n")
			lines := line.NewLines(tks)
			require.Len(t, lines, 1)

			ln := lines[0]
			ln.Annotation = tc.annotation

			assert.Equal(t, tc.want, ln.String())
		})
	}
}

func TestLine_Clone_Annotation(t *testing.T) {
	t.Parallel()

	tks := lexer.Tokenize("key: value\n")
	lines := line.NewLines(tks)
	require.Len(t, lines, 1)

	original := lines[0]
	original.Annotation = line.Annotation{Content: "original note", Column: 5}

	clone := original.Clone()

	// Verify annotation was copied.
	assert.Equal(t, original.Annotation.Content, clone.Annotation.Content)
	assert.Equal(t, original.Annotation.Column, clone.Annotation.Column)

	// Modify clone and verify original is unchanged.
	clone.Annotation.Content = "modified"
	clone.Annotation.Column = 10

	assert.Equal(t, "original note", original.Annotation.Content)
	assert.Equal(t, 5, original.Annotation.Column)
}

func TestNewLines_Value_PrevNextLinking(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
	}{
		"single key-value": {
			input: "foo: bar\n",
		},
		"multi line": {
			input: "foo: bar\nbaz: qux\n",
		},
		"nested map": {
			input: `parent:
  child: value
  sibling: another
`,
		},
		"literal block": {
			input: `key: |
  line1
  line2
`,
		},
		"list": {
			input: `items:
  - one
  - two
`,
		},
		"deeply nested": {
			input: `level1:
  level2:
    level3:
      level4:
        value: deep
`,
		},
		"anchor and alias": {
			input: `defaults: &defaults
  timeout: 30
  retries: 3
production:
  <<: *defaults
  timeout: 60
`,
		},
		"inline flow style": {
			input: `map: {a: 1, b: 2, c: 3}
list: [x, y, z]
`,
		},
		"mixed comments": {
			input: `# Header comment
key1: value1  # inline comment
# Middle comment
key2: value2
# Footer comment
`,
		},
		"folded block": {
			input: `description: >
  This is a long
  description that
  spans multiple lines.
`,
		},
		"list of maps": {
			input: `items:
  - name: first
    value: 1
    enabled: true
  - name: second
    value: 2
    enabled: false
`,
		},
		"complex kubernetes manifest": {
			input: `apiVersion: apps/v1
kind: Deployment
metadata:
  name: example
  labels:
    app: test
spec:
  replicas: 3
  selector:
    matchLabels:
      app: test
  template:
    spec:
      containers:
        - name: app
          image: nginx:latest
          ports:
            - containerPort: 80
`,
		},
		"quoted strings": {
			input: `double: "hello world"
single: 'foo bar'
special: "line1\nline2\ttab"
`,
		},
		"multi-document": {
			input: `---
doc1: value1
---
doc2: value2
`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			input := lexer.Tokenize(tc.input)
			lines := line.NewLines(input)

			// Tokens() returns recombined tokens matching the original lexer output.
			tks := lines.Tokens()
			require.NotEmpty(t, tks, "expected non-empty tokens")

			// Recombined token count should match original lexer output.
			assert.Len(t, tks, len(input), "recombined token count should match original")

			firstToken := tks[0]
			lastToken := tks[len(tks)-1]

			// First token should have no Prev.
			assert.Nil(t, firstToken.Prev, "first token Prev should be nil")

			// Last token should have no Next.
			assert.Nil(t, lastToken.Next, "last token Next should be nil")

			// Verify forward traversal reaches all tokens.
			forwardCount := 0
			for tk := firstToken; tk != nil; tk = tk.Next {
				forwardCount++
			}

			assert.Equal(t, len(tks), forwardCount, "forward traversal count mismatch")

			// Verify backward traversal reaches all tokens.
			backwardCount := 0
			for tk := lastToken; tk != nil; tk = tk.Prev {
				backwardCount++
			}

			assert.Equal(t, len(tks), backwardCount, "backward traversal count mismatch")

			// Verify bidirectional linking integrity.
			for tk := firstToken; tk != nil; tk = tk.Next {
				if tk.Next != nil {
					assert.Equal(t, tk, tk.Next.Prev, "Next.Prev should point back")
				}
				if tk.Prev != nil {
					assert.Equal(t, tk, tk.Prev.Next, "Prev.Next should point forward")
				}
			}
		})
	}
}

func TestNewLines_LeadingNewlineTokens(t *testing.T) {
	t.Parallel()

	// Test cases for tokens with leading newlines, which should be handled
	// correctly without creating invalid column ordering.
	tcs := map[string]struct {
		input        string
		wantLineNums []int
	}{
		"inline comment followed by next line": {
			input: `items:
  - key: value # inline comment
    next: data
`,
			wantLineNums: []int{1, 2, 3},
		},
		"sequence entry with merge key and comment": {
			input: `items:
  - <<: *anchor # comment
    key: value
`,
			wantLineNums: []int{1, 2, 3},
		},
		"nested map with trailing comment": {
			input: `parent:
  child: value # note
  sibling: other
`,
			wantLineNums: []int{1, 2, 3},
		},
		"multiple inline comments": {
			input: `a: 1 # first
b: 2 # second
c: 3 # third
`,
			wantLineNums: []int{1, 2, 3},
		},
		"comment block after content": {
			input: `key: value

# Comment block
# More comments
next: data
`,
			wantLineNums: []int{1, 2, 3, 4, 5},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := lexer.Tokenize(tc.input)
			lines := line.NewLines(tks)

			// Verify line numbers are strictly increasing.
			require.NoError(t, lines.Validate(), "tokens should be valid")

			// Verify expected line numbers.
			require.Len(t, lines, len(tc.wantLineNums), "wrong number of lines")

			for i, wantNum := range tc.wantLineNums {
				assert.Equal(t, wantNum, lines[i].Number(), "line %d has wrong number", i)
			}
		})
	}
}

func TestLines_Validate(t *testing.T) {
	t.Parallel()

	t.Run("valid cases", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input string
		}{
			"valid simple": {
				input: "key: value\n",
			},
			"valid multi-line": {
				input: "first: 1\nsecond: 2\nthird: 3\n",
			},
			"valid with join flags": {
				input: "script: |\n  line1\n  line2\n",
			},
			"empty": {
				input: "",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				tks := lexer.Tokenize(tc.input)
				lines := line.NewLines(tks)

				// Tokens created through NewLines should always be valid.
				assert.NoError(t, lines.Validate())
			})
		}
	})

	t.Run("line numbers normalized - same input", func(t *testing.T) {
		t.Parallel()

		// Create tokens with same line number but separated by newlines.
		// NewLines normalizes them to be monotonically increasing.
		tks := token.Tokens{}
		tks.Add(&token.Token{
			Type:     token.StringType,
			Origin:   "first\n",
			Value:    "first",
			Position: &token.Position{Line: 5, Column: 1},
		})
		tks.Add(&token.Token{
			Type:     token.StringType,
			Origin:   "second\n",
			Value:    "second",
			Position: &token.Position{Line: 5, Column: 1}, // Same line number in input.
		})

		lines := line.NewLines(tks)

		// After normalization, line numbers are sequential.
		require.NoError(t, lines.Validate())
		require.Len(t, lines, 2)
		assert.Equal(t, 5, lines[0].Number())
		assert.Equal(t, 6, lines[1].Number())
	})

	t.Run("line numbers normalized - decreasing input", func(t *testing.T) {
		t.Parallel()

		// Create tokens with decreasing line numbers.
		// NewLines normalizes them to be monotonically increasing.
		tks := token.Tokens{}
		tks.Add(&token.Token{
			Type:     token.StringType,
			Origin:   "first\n",
			Value:    "first",
			Position: &token.Position{Line: 10, Column: 1},
		})
		tks.Add(&token.Token{
			Type:     token.StringType,
			Origin:   "second\n",
			Value:    "second",
			Position: &token.Position{Line: 5, Column: 1}, // Lower line number in input.
		})

		lines := line.NewLines(tks)

		// After normalization, line numbers are sequential.
		require.NoError(t, lines.Validate())
		require.Len(t, lines, 2)
		assert.Equal(t, 10, lines[0].Number())
		assert.Equal(t, 11, lines[1].Number())
	})

	t.Run("columns not increasing - same", func(t *testing.T) {
		t.Parallel()

		// Create two tokens on same line with same column.
		tks := token.Tokens{}
		tks.Add(&token.Token{
			Type:     token.StringType,
			Origin:   "first",
			Value:    "first",
			Position: &token.Position{Line: 1, Column: 5},
		})
		tks.Add(&token.Token{
			Type:     token.StringType,
			Origin:   "second\n",
			Value:    "second",
			Position: &token.Position{Line: 1, Column: 5}, // Same column!
		})

		lines := line.NewLines(tks)

		err := lines.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "column 5 not greater than previous 5")
	})

	t.Run("columns not increasing - decreasing", func(t *testing.T) {
		t.Parallel()

		tks := token.Tokens{}
		tks.Add(&token.Token{
			Type:     token.StringType,
			Origin:   "first",
			Value:    "first",
			Position: &token.Position{Line: 1, Column: 10},
		})
		tks.Add(&token.Token{
			Type:     token.StringType,
			Origin:   "second\n",
			Value:    "second",
			Position: &token.Position{Line: 1, Column: 5}, // Lower column!
		})

		lines := line.NewLines(tks)

		err := lines.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "column 5 not greater than previous 10")
	})

	t.Run("token line numbers normalized on same line", func(t *testing.T) {
		t.Parallel()

		// Create tokens with inconsistent position line numbers.
		// NewLines normalizes them to be consistent.
		tks := token.Tokens{}
		tks.Add(&token.Token{
			Type:     token.StringType,
			Origin:   "first",
			Value:    "first",
			Position: &token.Position{Line: 1, Column: 1},
		})
		tks.Add(&token.Token{
			Type:     token.StringType,
			Origin:   "second\n",
			Value:    "second",
			Position: &token.Position{Line: 2, Column: 10}, // Different line in input.
		})

		lines := line.NewLines(tks)

		// Both tokens end up on line 1 with normalized positions.
		require.NoError(t, lines.Validate())
		require.Len(t, lines, 1)

		ln := lines[0]
		require.Len(t, ln.Tokens(), 2)
		assert.Equal(t, 1, ln.Token(0).Position.Line)
		assert.Equal(t, 1, ln.Token(1).Position.Line)
	})

	t.Run("nil position tokens - valid", func(t *testing.T) {
		t.Parallel()

		tks := token.Tokens{}
		tks.Add(&token.Token{
			Type:     token.StringType,
			Origin:   "first",
			Value:    "first",
			Position: nil, // Nil position.
		})
		tks.Add(&token.Token{
			Type:     token.StringType,
			Origin:   "second\n",
			Value:    "second",
			Position: &token.Position{Line: 1, Column: 10},
		})

		lines := line.NewLines(tks)

		// Nil positions are skipped in validation.
		assert.NoError(t, lines.Validate())
	})

	t.Run("valid with gaps in line numbers", func(t *testing.T) {
		t.Parallel()

		tks := token.Tokens{}
		tks.Add(&token.Token{
			Type:     token.StringType,
			Origin:   "first\n",
			Value:    "first",
			Position: &token.Position{Line: 1, Column: 1},
		})
		tks.Add(&token.Token{
			Type:     token.StringType,
			Origin:   "second\n",
			Value:    "second",
			Position: &token.Position{Line: 10, Column: 1}, // Gap is fine.
		})
		tks.Add(&token.Token{
			Type:     token.StringType,
			Origin:   "third\n",
			Value:    "third",
			Position: &token.Position{Line: 100, Column: 1},
		})

		lines := line.NewLines(tks)

		assert.NoError(t, lines.Validate())
	})
}

func TestNewLines_PositionFieldsMatchLexer(t *testing.T) {
	t.Parallel()

	tcs := map[string]string{
		"simple key-value":    "key: value\n",
		"nested":              "parent:\n  child: value\n",
		"sequence":            "items:\n  - one\n  - two\n",
		"deep nesting":        "a:\n  b:\n    c: val\n",
		"multiple keys":       "first: 1\nsecond: 2\nthird: 3\n",
		"inline map":          "config: {key: value}\n",
		"inline list":         "items: [a, b, c]\n",
		"comment":             "key: value  # comment\n",
		"anchor and alias":    "anchor: &name value\nref: *name\n",
		"unindent after nest": "parent:\n  child: value\nsibling: other\n",
	}

	for name, input := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// Get original tokens from lexer.
			originalTks := lexer.Tokenize(input)

			// Process through Lines and reconstruct.
			lines := line.NewLines(originalTks)
			resultTks := lines.Tokens()

			// For non-split tokens, Position fields should match.
			// Build map of (Line, Column) -> original Position for comparison.
			type posKey struct {
				line, col int
			}

			origByPos := make(map[posKey]*token.Position)
			for _, tk := range originalTks {
				if tk.Position != nil {
					key := posKey{tk.Position.Line, tk.Position.Column}
					origByPos[key] = tk.Position
				}
			}

			for _, tk := range resultTks {
				if tk.Position == nil {
					continue
				}

				key := posKey{tk.Position.Line, tk.Position.Column}
				orig, ok := origByPos[key]
				if !ok {
					// Token was split, skip comparison.
					continue
				}

				assert.Equal(t, orig.Offset, tk.Position.Offset,
					"Offset mismatch at line %d col %d", key.line, key.col)
				assert.Equal(t, orig.IndentNum, tk.Position.IndentNum,
					"IndentNum mismatch at line %d col %d", key.line, key.col)
				assert.Equal(t, orig.IndentLevel, tk.Position.IndentLevel,
					"IndentLevel mismatch at line %d col %d", key.line, key.col)
			}
		})
	}
}

func TestNewLines_SplitTokenOffsets(t *testing.T) {
	t.Parallel()

	tcs := map[string]string{
		"literal block": "script: |\n  line1\n  line2\n",
		"folded block":  "text: >\n  first\n  second\n",
		"multiline string": `key: "line1
  line2"
`,
	}

	for name, input := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := line.NewLines(lexer.Tokenize(input))

			var prevOffset int
			for i := range lines {
				ln := lines[i]
				for _, tk := range ln.Tokens() {
					if tk.Position != nil {
						// Offset must be strictly increasing.
						assert.Greater(t, tk.Position.Offset, prevOffset,
							"Offset not increasing at line %d", ln.Number())

						prevOffset = tk.Position.Offset
					}
				}
			}
		})
	}
}

func TestNewLines_OffsetRuneCount(t *testing.T) {
	t.Parallel()

	// Test that Offset uses rune count, not byte count.
	// The go-yaml lexer increments offset by rune count, not byte count.
	// UTF-8 chars like 日 are 3 bytes each, but 1 rune each.
	//
	// For input "日: value\n":
	//   - "日" token at offset 1 (first position)
	//   - ":" token at offset 2 (after 1 rune for 日)
	//   - "value" token at offset 4 (after 3 runes for "日: ")
	//
	// If byte-based, ":" would be at offset 4 (after 3 bytes for 日).
	input := "日: value\n"
	originalTks := lexer.Tokenize(input)
	lines := line.NewLines(originalTks)
	resultTks := lines.Tokens()

	// Verify the round-trip preserves lexer output exactly.
	assertTokensEqual(t, originalTks, resultTks)

	// Verify specific offset values that prove rune-based counting.
	// The ":" (MappingValue) token should be at offset 2, not 4.
	require.Len(t, resultTks, 3, "expected 3 tokens: key, :, value")

	// Token 0: "日" at offset 1.
	assert.Equal(t, 1, resultTks[0].Position.Offset, "first token offset should be 1")

	// Token 1: ":" at offset 2 (rune-based) not 4 (byte-based).
	assert.Equal(t, 2, resultTks[1].Position.Offset,
		"MappingValue ':' should be at offset 2 (rune-based), not 4 (byte-based)")

	// Token 2: "value" at offset 4 (after "日: " which is 3 runes).
	assert.Equal(t, 4, resultTks[2].Position.Offset, "value token offset should be 4")

	// Also verify total bytes match for Origin content preservation.
	var origTotalBytes, resultTotalBytes int
	for _, tk := range originalTks {
		origTotalBytes += len(tk.Origin)
	}

	for _, tk := range resultTks {
		resultTotalBytes += len(tk.Origin)
	}

	assert.Equal(t, origTotalBytes, resultTotalBytes, "total bytes should match lexer output")
}

func TestNewLines_IndentLevelProgression(t *testing.T) {
	t.Parallel()

	input := `root:
  level1:
    level2:
      level3: value
    back2: val
  back1: val
end: val
`
	lines := line.NewLines(lexer.Tokenize(input))

	// Expected indent levels per line (based on go-yaml scanner behavior):
	// Line 1: root: -> level 0.
	// Line 2:   level1: -> level 1.
	// Line 3:     level2: -> level 2.
	// Line 4:       level3: value -> level 3.
	// Line 5:     back2: val -> level 2.
	// Line 6:   back1: val -> level 1.
	// Line 7: end: val -> level 0.
	expectedLevels := []int{0, 1, 2, 3, 2, 1, 0}

	require.Len(t, lines, len(expectedLevels))

	for i := range lines {
		ln := lines[i]
		if len(ln.Tokens()) > 0 {
			firstTk := ln.Token(0)
			if firstTk.Position != nil {
				assert.Equal(t, expectedLevels[i], firstTk.Position.IndentLevel,
					"IndentLevel mismatch at line %d", i+1)
			}
		}
	}
}

func TestNewLines_BlockScalarPositionSemantics(t *testing.T) {
	t.Parallel()

	// The go-yaml lexer places the Position of block scalar content (StringType)
	// on the LAST line of the content, not the first.
	// This is critical for round-trip fidelity.

	tcs := map[string]struct {
		input            string
		wantContentLine  int // Expected Position.Line of the StringType content token.
		wantContentLines int // Number of content lines.
	}{
		"literal two lines": {
			input: `key: |
  line1
  line2
`,
			wantContentLine:  3, // Position should be on last content line.
			wantContentLines: 2,
		},
		"literal three lines": {
			input: `key: |
  a
  b
  c
`,
			wantContentLine:  4,
			wantContentLines: 3,
		},
		"folded two lines": {
			input: `key: >
  first
  second
`,
			wantContentLine:  3,
			wantContentLines: 2,
		},
		"literal with strip": {
			input: `key: |-
  line1
  line2
`,
			wantContentLine:  3,
			wantContentLines: 2,
		},
		"literal with keep and trailing blank": {
			input: `key: |+
  line1
  line2

`,
			wantContentLine:  4, // Blank line counts.
			wantContentLines: 3,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			originalTks := lexer.Tokenize(tc.input)
			lines := line.NewLines(originalTks)
			resultTks := lines.Tokens()

			// Verify round-trip fidelity.
			assertTokensEqual(t, originalTks, resultTks)

			// Find the StringType token (block scalar content).
			var contentToken *token.Token
			for _, tk := range resultTks {
				if tk.Type == token.StringType && strings.Contains(tk.Origin, "\n") {
					contentToken = tk
					break
				}
			}

			require.NotNil(t, contentToken, "expected to find block scalar content token")
			assert.Equal(t, tc.wantContentLine, contentToken.Position.Line,
				"block scalar content Position.Line should point to LAST line")
		})
	}
}

func TestNewLines_PlainMultilinePositionSemantics(t *testing.T) {
	t.Parallel()

	// The go-yaml lexer places the Position of plain multiline strings (StringType)
	// on the FIRST line, not the last. This is different from block scalars.
	// This is critical for round-trip fidelity.

	tcs := map[string]struct {
		input           string
		wantContentLine int // Expected Position.Line of the StringType content token.
	}{
		"plain multiline two lines": {
			input: `key: this is
  continued
`,
			wantContentLine: 1, // Position should be on FIRST line.
		},
		"plain multiline three lines": {
			input: `key: first
  second
  third
`,
			wantContentLine: 1,
		},
		"plain multiline with more indent": {
			input: `parent:
  child: line one
    continued line
`,
			wantContentLine: 2, // First line of the value.
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			originalTks := lexer.Tokenize(tc.input)
			lines := line.NewLines(originalTks)
			resultTks := lines.Tokens()

			// Verify round-trip fidelity.
			assertTokensEqual(t, originalTks, resultTks)

			// Find the multiline StringType token (value with newlines).
			var contentToken *token.Token
			for _, tk := range resultTks {
				if tk.Type == token.StringType && strings.Contains(tk.Origin, "\n") {
					contentToken = tk
					break
				}
			}

			require.NotNil(t, contentToken, "expected to find plain multiline string token")
			assert.Equal(t, tc.wantContentLine, contentToken.Position.Line,
				"plain multiline string Position.Line should point to FIRST line")
		})
	}
}

func TestNewLines_QuotedMultilineActualNewlines(t *testing.T) {
	t.Parallel()

	// Test quoted strings with actual newlines in the content (not escaped \n).
	// The go-yaml lexer places the Position at the opening quote line.
	// The Value normalizes actual newlines to spaces in double-quoted strings.

	tcs := map[string]struct {
		input         string
		wantValue     string
		wantLine      int
		wantColumn    int
		wantTokenType token.Type
	}{
		"double quoted with actual newline": {
			input: "key: \"line1\nline2\"\n",
			// Position should be at opening quote on line 1.
			wantLine:      1,
			wantColumn:    6,             // After "key: ".
			wantValue:     "line1 line2", // Newline becomes space.
			wantTokenType: token.DoubleQuoteType,
		},
		"double quoted with multiple newlines": {
			input:         "key: \"a\nb\nc\"\n",
			wantLine:      1,
			wantColumn:    6,
			wantValue:     "a b c",
			wantTokenType: token.DoubleQuoteType,
		},
		"single quoted with actual newline": {
			input:         "key: 'line1\nline2'\n",
			wantLine:      1,
			wantColumn:    6,
			wantValue:     "line1 line2", // Newline becomes space.
			wantTokenType: token.SingleQuoteType,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			originalTks := lexer.Tokenize(tc.input)
			lines := line.NewLines(originalTks)
			resultTks := lines.Tokens()

			// Verify round-trip fidelity.
			assertTokensEqual(t, originalTks, resultTks)

			// Find the quoted token.
			var quotedToken *token.Token
			for _, tk := range resultTks {
				if tk.Type == tc.wantTokenType {
					quotedToken = tk
					break
				}
			}

			require.NotNil(t, quotedToken, "expected to find quoted string token")
			assert.Equal(t, tc.wantLine, quotedToken.Position.Line,
				"quoted string Position.Line should point to opening quote line")
			assert.Equal(t, tc.wantColumn, quotedToken.Position.Column,
				"quoted string Position.Column should point to opening quote")
			assert.Equal(t, tc.wantValue, quotedToken.Value,
				"quoted string Value should have normalized newlines")
		})
	}
}

func TestNewLines_EmptyBlockScalar(t *testing.T) {
	t.Parallel()

	// Test edge case: block scalar header with no content.
	// This can happen with "key: |\n" followed by another key or end of document.
	//
	// KNOWN LIMITATION: go-yaml lexer produces an empty StringType token
	// (Origin="", Value="") after block scalar headers when there's no content.
	// Our implementation does not preserve these empty tokens, so round-trip
	// fidelity differs from lexer output for this edge case.
	//
	// Example lexer output for "key: |\nnext: value\n":
	//   [0] String "key"
	//   [1] MappingValue ":"
	//   [2] Literal "|"
	//   [3] String "" <- empty block scalar content (Origin="", Value="")
	//   [4] String "next"
	//   ...
	//
	// Our output omits token [3] because we filter/skip empty tokens.

	tcs := map[string]struct {
		input string
	}{
		"literal empty followed by key": {
			input: `key: |
next: value
`,
		},
		"folded empty followed by key": {
			input: `key: >
next: value
`,
		},
		"literal empty at end": {
			input: `key: |
`,
		},
		"literal strip empty": {
			input: `key: |-
next: value
`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			originalTks := lexer.Tokenize(tc.input)
			lines := line.NewLines(originalTks)
			resultTks := lines.Tokens()

			// Verify Source is valid (structure integrity).
			require.NoError(t, lines.Validate())

			// Document the limitation: empty block scalar tokens are not preserved.
			// Count empty StringType tokens in original that won't be in result.
			var emptyCount int
			for _, tk := range originalTks {
				if tk.Type == token.StringType && tk.Origin == "" && tk.Value == "" {
					emptyCount++
				}
			}

			// Our implementation produces fewer tokens when empty block scalar
			// content tokens exist. This is a known limitation.
			if emptyCount > 0 {
				assert.Len(t, resultTks, len(originalTks)-emptyCount,
					"result should have %d fewer tokens due to empty block scalar limitation", emptyCount)
			} else {
				// No empty tokens, should be exact match.
				assertTokensEqual(t, originalTks, resultTks)
			}
		})
	}
}

func TestNewLines_BlockScalarTrailingBlanks(t *testing.T) {
	t.Parallel()

	// Test block scalars with trailing blank lines and different chomping indicators.
	// - strip (-): Remove all trailing newlines.
	// - clip (default): Keep single trailing newline.
	// - keep (+): Keep all trailing newlines.

	tcs := map[string]struct {
		input     string
		wantValue string // Expected Value after chomping.
	}{
		"literal keep with trailing blanks": {
			input: `key: |+
  content

`,
			wantValue: "content\n\n",
		},
		"literal strip with content": {
			input: `key: |-
  content
`,
			wantValue: "content",
		},
		"literal clip default": {
			input: `key: |
  content
`,
			wantValue: "content\n",
		},
		"folded keep with trailing blanks": {
			input: `key: >+
  line1
  line2

`,
			wantValue: "line1 line2\n\n",
		},
		"folded strip": {
			input: `key: >-
  line1
  line2
`,
			wantValue: "line1 line2",
		},
		"literal keep multiple trailing": {
			input: `key: |+
  content


`,
			wantValue: "content\n\n\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			originalTks := lexer.Tokenize(tc.input)
			lines := line.NewLines(originalTks)
			resultTks := lines.Tokens()

			// Verify round-trip fidelity.
			assertTokensEqual(t, originalTks, resultTks)

			// Find the block scalar content token.
			var contentToken *token.Token
			for _, tk := range resultTks {
				if tk.Type == token.StringType && strings.Contains(tk.Origin, "\n") {
					contentToken = tk
					break
				}
			}

			require.NotNil(t, contentToken, "expected to find block scalar content token")
			assert.Equal(t, tc.wantValue, contentToken.Value,
				"block scalar Value should match chomping behavior")
		})
	}
}

func TestNewLines_ColumnPositionAfterSplit(t *testing.T) {
	t.Parallel()

	// Test that Column positions are correctly calculated when multiline tokens
	// are split across lines. For block scalars, each split part should have
	// Column calculated based on the leading whitespace of that line.

	t.Run("block scalar column positions", func(t *testing.T) {
		t.Parallel()

		input := `key: |
  line1
  line2
`
		originalTks := lexer.Tokenize(input)
		lines := line.NewLines(originalTks)

		// Verify we have the expected number of lines.
		require.Len(t, lines, 3)

		// Line 0: "key: |" - should have key, mapping value, and literal indicator.
		line0 := lines[0]
		require.GreaterOrEqual(t, len(line0.Tokens()), 3, "line 0 should have at least 3 tokens")
		assert.Equal(t, 1, line0.Token(0).Position.Column, "key token column should be 1")

		// Line 1: "  line1" - split block scalar content, first content line.
		line1 := lines[1]
		require.NotEmpty(t, line1.Tokens(), "line 1 should have tokens")
		// Column should reflect position within line (content starts at column 1 for split tokens).
		assert.Positive(t, line1.Token(0).Position.Column, "line 1 token should have positive column")

		// Line 2: "  line2" - split block scalar content, second content line.
		line2 := lines[2]
		require.NotEmpty(t, line2.Tokens(), "line 2 should have tokens")
		assert.Positive(t, line2.Token(0).Position.Column, "line 2 token should have positive column")

		// Verify round-trip produces identical tokens.
		resultTks := lines.Tokens()
		assertTokensEqual(t, originalTks, resultTks)
	})

	t.Run("plain multiline column positions", func(t *testing.T) {
		t.Parallel()

		input := `key: this is
  continued
`
		originalTks := lexer.Tokenize(input)
		lines := line.NewLines(originalTks)

		// Verify we have the expected number of lines.
		require.Len(t, lines, 2)

		// Line 0: "key: this is" - key, mapping value, first part of string.
		line0 := lines[0]
		require.GreaterOrEqual(t, len(line0.Tokens()), 3, "line 0 should have at least 3 tokens")

		// Line 1: "  continued" - continuation of plain multiline string.
		line1 := lines[1]
		require.NotEmpty(t, line1.Tokens(), "line 1 should have tokens")
		assert.Positive(t, line1.Token(0).Position.Column, "line 1 token should have positive column")

		// Verify round-trip produces identical tokens.
		resultTks := lines.Tokens()
		assertTokensEqual(t, originalTks, resultTks)
	})
}
