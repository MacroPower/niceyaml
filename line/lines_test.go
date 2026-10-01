package line_test

import (
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/tokens"
)

func TestNewLines_Roundtrip(t *testing.T) {
	t.Parallel()

	t.Run("testdata/full.yaml", func(t *testing.T) {
		t.Parallel()

		input, err := os.ReadFile(filepath.Join("..", "testdata", "full.yaml"))
		require.NoError(t, err)

		requireRoundtrip(t, string(input))
	})

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
		"single quote literal": "text: 'no \\n escape'\n",
		"quote in single":      "text: 'it''s quoted'\n",
		"quote in double":      "text: \"say \\\"hello\\\"\"\n",
		"unicode escape":       "text: \"\\u65E5\\u672C\"\n",

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
		"ampersand in value":      "text: \"Tom & Jerry\"\n",
		"asterisk in value":       "rating: \"5* rating\"\n",
		"pipe in value":           "options: \"A | B\"\n",
		"greater in value":        "compare: \"A > B\"\n",
		"question in value":       "ask: \"Why? Because!\"\n",
		"empty document":          "---\n...\n",
		"CRLF line endings":       "key: value\r\nother: data\r\n",
		"block scalar with CRLF":  "key: |\r\n  line1\r\n  line2\r\n",
		"nested with CRLF":        "parent:\r\n  child: value\r\n",
		"multiple keys CRLF":      "a: 1\r\nb: 2\r\nc: 3\r\n",
		"inline comment CRLF":     "key: value # comment\r\nnext: data\r\n",
		"folded block CRLF":       "key: >\r\n  line1\r\n  line2\r\n",
		"literal with keep CRLF":  "key: |+\r\n  content\r\n\r\n",
		"mixed LF and CRLF":       "key: value\nother: data\r\n",
		"quoted string CRLF":      "key: \"line1\r\nline2\"\r\n",
		"plain multiline CRLF":    "key: text\r\n  continued\r\n",
		"sequence CRLF":           "items:\r\n  - one\r\n  - two\r\n",
		"flow map CRLF":           "config: {a: 1, b: 2}\r\nnext: value\r\n",
		"document marker CRLF":    "---\r\nkey: value\r\n",
		"comment only CRLF":       "# comment\r\nkey: value\r\n",
		"anchor alias CRLF":       "base: &ref value\r\nuse: *ref\r\n",
		"literal indent CRLF":     "code: |2\r\n    indented\r\n",
		"literal strip CRLF":      "text: |-\r\n  content\r\n",
		"deep nesting CRLF":       "a:\r\n  b:\r\n    c: val\r\n",
		"blank line between CRLF": "key: value\r\n\r\nnext: data\r\n",
		"list of maps CRLF":       "items:\r\n  - name: a\r\n    val: 1\r\n",
		"plain multiline string":  "key: this is\n  a multiline\n  plain string\n",
		"multiple keys CR":        "a: 1\rb: 2\r",
		"block scalar with CR":    "key: |\r  line1\r  line2\r",
		"blank line between CR":   "a: 1\r\rb: 2\r",
		"mixed CR and CRLF":       "a: 1\rb: 2\r\nc: 3\n",
	}

	for name, input := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := requireRoundtrip(t, input)

			contentDiff := yamltest.CompareContent(input, lines.Content())
			require.True(t, contentDiff.Equal(), contentDiff.String())
		})
	}
}

// requireRoundtrip creates [line.Lines] from the tokens of input and
// requires [line.Lines.Tokens] to return tokens equal to those of a second
// tokenization of input. The lines hold the tokens passed to
// [line.NewLines], so comparing with those would compare each token with
// itself.
func requireRoundtrip(t *testing.T, input string) line.Lines {
	t.Helper()

	want := tokens.Tokenize(input)
	lines := line.NewLines(tokens.Tokenize(input))

	yamltest.RequireTokensEqual(t, want, lines.Tokens())

	return lines
}

// TestNewLines_DoesNotMutateInput verifies that NewLines leaves the tokens
// it cuts into parts as the lexer produced them, and that Lines.Tokens
// returns those same tokens.
func TestNewLines_DoesNotMutateInput(t *testing.T) {
	t.Parallel()

	full, err := os.ReadFile(filepath.Join("..", "testdata", "full.yaml"))
	require.NoError(t, err)

	tcs := map[string]string{
		"testdata/full.yaml":      string(full),
		"literal block":           "script: |\n  line1\n  line2\n",
		"literal keep":            "text: |+\n  line1\n  line2\n\n",
		"folded with blank lines": "text: >\n  first\n\n  second\n",
		"double quoted multiline": "key: \"line1\nline2\"\n",
		"single quoted multiline": "key: 'line1\nline2'\n",
		"plain multiline":         "key: this is\n  a multiline\n  plain string\n",
		"unicode key":             "日: value\n",
		"block scalar CRLF":       "key: |\r\n  line1\r\n  line2\r\n",
		"quoted string CRLF":      "key: \"line1\r\nline2\"\r\n",
		"plain multiline CRLF":    "key: text\r\n  continued\r\n",
		"blank line between CRLF": "key: value\r\n\r\nnext: data\r\n",
	}

	for name, input := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			want := tokens.Tokenize(input)
			tks := tokens.Tokenize(input)
			got := line.NewLines(tks).Tokens()

			yamltest.RequireTokensEqual(t, want, tks)
			require.Len(t, got, len(tks))

			for i := range tks {
				assert.Same(t, tks[i], got[i], "token %d", i)
			}
		})
	}
}

func TestNewLines_PerLine(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  []string
	}{
		"literal block": {
			input: stringtest.Input(`
				script: |
				  line1
				  line2
				  line3
			`),
			want: []string{
				"   1 | script: |",
				"   2 |   line1",
				"   3 |   line2",
				"   4 |   line3",
			},
		},
		"folded block": {
			input: stringtest.Input(`
				desc: >
				  part1
				  part2
			`),
			want: []string{
				"   1 | desc: >",
				"   2 |   part1",
				"   3 |   part2",
			},
		},
		"single key-value": {
			// The lexer drops the trailing newline on final simple values.
			input: "key: value\n",
			want: []string{
				"   1 | key: value",
			},
		},
		"multiple key-values": {
			input: stringtest.Input(`
				first: 1
				second: 2
			`),
			want: []string{
				"   1 | first: 1",
				"   2 | second: 2",
			},
		},
		"nested map": {
			input: stringtest.Input(`
				parent:
				  child: value
			`),
			want: []string{
				"   1 | parent:",
				"   2 |   child: value",
			},
		},
		"quoted with escaped newline": {
			input: stringtest.Input(`
				special: "line1\nline2"
			`),
			want: []string{
				`   1 | special: "line1\nline2"`,
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			input := tokens.Tokenize(tc.input)
			lines := line.NewLines(input)

			require.Equal(t, len(tc.want), lines.Len(), "wrong number of lines")

			for i, want := range tc.want {
				assert.Equal(t, want, lines.Line(i).String(), "line %d", i)
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
			input: stringtest.Input(`
				key: value
				other: data
			`),
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
			input: stringtest.Input(`
				parent:
				  child: value
				  sibling: another
			`),
			startLine:     50,
			wantLineNums:  []int{50, 51, 52},
			wantLineCount: 3,
		},
		"literal block starting at line 20": {
			input: stringtest.Input(`
				script: |
				  line1
				  line2
			`),
			startLine:     20,
			wantLineNums:  []int{20, 21, 22},
			wantLineCount: 3,
		},
		"folded block starting at line 30": {
			input: stringtest.Input(`
				desc: >
				  part1
				  part2
			`),
			startLine:     30,
			wantLineNums:  []int{30, 31, 32},
			wantLineCount: 3,
		},
		"list starting at line 25": {
			input: stringtest.Input(`
				items:
				  - one
				  - two
			`),
			startLine:     25,
			wantLineNums:  []int{25, 26, 27},
			wantLineCount: 3,
		},
		"comment and key at line 15": {
			input: stringtest.Input(`
				# comment
				key: value
			`),
			startLine:     15,
			wantLineNums:  []int{15, 16},
			wantLineCount: 2,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// Tokenize and adjust line numbers to simulate non-line-1 start.
			tks := tokens.Tokenize(tc.input)

			offset := tc.startLine - 1
			for _, tk := range tks {
				if tk.Position != nil {
					tk.Position.Line += offset
				}
			}

			lines := line.NewLines(tks)

			require.Equal(t, tc.wantLineCount, lines.Len(), "wrong number of lines")

			for i, wantNum := range tc.wantLineNums {
				assert.Equal(t, wantNum, lines.Line(i).Number(), "line %d has wrong number", i)
			}

			// The round-trip preserves the content of the dumped tokens.
			gotTokens := lines.Tokens()
			assert.Equal(
				t,
				yamltest.DumpTokenOrigins(tks),
				yamltest.DumpTokenOrigins(gotTokens),
				"round-trip content mismatch",
			)
		})
	}
}

func TestNewLines_DroppedNewline(t *testing.T) {
	t.Parallel()

	// The lexer drops the line ending ahead of some tokens, such as a "?"
	// after a document header or a value indicator after a directive.
	// Tokenize restores it, so the token opens a new line rather than
	// joining the one before it.
	tcs := map[string]struct {
		input string
		want  []string
	}{
		"explicit key after header": {
			input: "x: 1\n---\n? a\n: b\n",
			want:  []string{"x: 1", "---", "? a", ": b"},
		},
		"value indicator after directive": {
			input: "%YAML 1.2\n: value\n",
			want:  []string{"%YAML 1.2", ": value"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := line.NewLines(tokens.Tokenize(tc.input))
			require.Equal(t, len(tc.want), lines.Len())

			for i, want := range tc.want {
				ln := lines.Line(i)
				assert.Equal(t, i+1, ln.Number())
				assert.Equal(t, want, ln.Content())
			}
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
				tks1 := tokens.Tokenize("key1: value1\nkey2: value2\n")
				for _, tk := range tks1 {
					if tk.Position != nil {
						tk.Position.Line += 9 // Shift to lines 10, 11.
					}
				}

				// Build tokens for lines 40-41.
				tks2 := tokens.Tokenize("key3: value3\nkey4: value4\n")
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
				tks1 := tokens.Tokenize("first: value\n")

				for _, tk := range tks1 {
					if tk.Position != nil {
						tk.Position.Line += 4 // Shift to line 5.
					}
				}

				tks2 := tokens.Tokenize("second: value\n")

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
					tks := tokens.Tokenize("key: value\n")

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

			require.Equal(t, tc.wantLineCount, lines.Len(), "wrong number of lines")

			for i, wantNum := range tc.wantLineNums {
				assert.Equal(t, wantNum, lines.Line(i).Number(), "line %d has wrong number", i)
			}

			// Prev and Next link the tokens across gaps.
			gotTokens := lines.Tokens()
			require.NotEmpty(t, gotTokens, "expected non-empty tokens")

			// Forward traversal reaches every token.
			forwardCount := 0

			for tk := gotTokens[0]; tk != nil; tk = tk.Next {
				forwardCount++
			}

			assert.Equal(t, len(gotTokens), forwardCount, "forward traversal count mismatch")

			// Backward traversal reaches every token.
			lastTk := gotTokens[len(gotTokens)-1]

			backwardCount := 0

			for tk := lastTk; tk != nil; tk = tk.Prev {
				backwardCount++
			}

			assert.Equal(t, len(gotTokens), backwardCount, "backward traversal count mismatch")
		})
	}
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
			input: stringtest.Input(`
				parent:
				  child: value
				  sibling: another
			`),
		},
		"literal block": {
			input: stringtest.Input(`
				key: |
				  line1
				  line2
			`),
		},
		"list": {
			input: stringtest.Input(`
				items:
				  - one
				  - two
			`),
		},
		"deeply nested": {
			input: stringtest.Input(`
				level1:
				  level2:
				    level3:
				      level4:
				        value: deep
			`),
		},
		"anchor and alias": {
			input: stringtest.Input(`
				defaults: &defaults
				  timeout: 30
				  retries: 3
				production:
				  <<: *defaults
				  timeout: 60
			`),
		},
		"inline flow style": {
			input: stringtest.Input(`
				map: {a: 1, b: 2, c: 3}
				list: [x, y, z]
			`),
		},
		"mixed comments": {
			input: stringtest.Input(`
				# Header comment
				key1: value1  # inline comment
				# Middle comment
				key2: value2
				# Footer comment
			`),
		},
		"folded block": {
			input: stringtest.Input(`
				description: >
				  This is a long
				  description that
				  spans multiple lines.
			`),
		},
		"list of maps": {
			input: stringtest.Input(`
				items:
				  - name: first
				    value: 1
				    enabled: true
				  - name: second
				    value: 2
				    enabled: false
			`),
		},
		"complex kubernetes manifest": {
			input: stringtest.Input(`
				apiVersion: apps/v1
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
			`),
		},
		"quoted strings": {
			input: stringtest.Input(`
				double: "hello world"
				single: 'foo bar'
				special: "line1\nline2\ttab"
			`),
		},
		"multi-document": {
			input: stringtest.Input(`
				---
				doc1: value1
				---
				doc2: value2
			`),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := line.NewLines(tokens.Tokenize(tc.input))

			// The parts on each line link to their neighbors in column
			// order, and the chain stops at both ends of the line.
			for _, ln := range lines.All() {
				parts := ln.Tokens()
				require.NotEmpty(t, parts, "line %d", ln.Number())

				assert.Nil(t, parts[0].Prev, "line %d first part Prev", ln.Number())
				assert.Nil(t, parts[len(parts)-1].Next, "line %d last part Next", ln.Number())

				for j := range len(parts) - 1 {
					assert.Same(t, parts[j+1], parts[j].Next, "line %d part %d Next", ln.Number(), j)
					assert.Same(t, parts[j], parts[j+1].Prev, "line %d part %d Prev", ln.Number(), j+1)
				}
			}
		})
	}
}

func TestNewLines_LeadingNewlineTokens(t *testing.T) {
	t.Parallel()

	// Test cases for tokens with leading newlines, which NewLines must handle
	// without creating invalid column ordering.
	tcs := map[string]struct {
		input string
		want  []int
	}{
		"inline comment followed by next line": {
			input: stringtest.Input(`
				items:
				  - key: value # inline comment
				    next: data
			`),
			want: []int{1, 2, 3},
		},
		"sequence entry with merge key and comment": {
			input: stringtest.Input(`
				items:
				  - <<: *anchor # comment
				    key: value
			`),
			want: []int{1, 2, 3},
		},
		"nested map with trailing comment": {
			input: stringtest.Input(`
				parent:
				  child: value # note
				  sibling: other
			`),
			want: []int{1, 2, 3},
		},
		"multiple inline comments": {
			input: stringtest.Input(`
				a: 1 # first
				b: 2 # second
				c: 3 # third
			`),
			want: []int{1, 2, 3},
		},
		"comment block after content": {
			input: stringtest.Input(`
				key: value

				# Comment block
				# More comments
				next: data
			`),
			want: []int{1, 2, 3, 4, 5},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := tokens.Tokenize(tc.input)
			lines := line.NewLines(tks)

			// The line numbers increase strictly.
			require.NoError(t, yamltest.ValidateLines(lines), "tokens should be valid")

			// Each line carries its expected number.
			require.Equal(t, len(tc.want), lines.Len(), "wrong number of lines")

			for i, wantNum := range tc.want {
				assert.Equal(t, wantNum, lines.Line(i).Number(), "line %d has wrong number", i)
			}
		})
	}
}

func TestNewLines_PartPositionsMatchLexer(t *testing.T) {
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

			// The lines hold the tokens passed to NewLines, so the lexer's
			// positions come from a second tokenization.
			originalTks := tokens.Tokenize(input)
			lines := line.NewLines(tokens.Tokenize(input))

			// Index the lexer's positions by the (Line, Column) where the
			// text of each token starts.
			type posKey struct {
				line, col int
			}

			origByPos := make(map[posKey]*token.Position, len(originalTks))
			for _, tk := range originalTks {
				origByPos[posKey{tk.Position.Line, tk.Position.Column}] = tk.Position
			}

			require.Len(t, origByPos, len(originalTks), "lexer tokens should start at distinct positions")

			// The part that holds a token's text sits at the token's
			// position. A part that holds only a line ending or the
			// indentation cut from an Origin sits where no text starts.
			for _, ln := range lines.All() {
				for _, part := range ln.Tokens() {
					key := posKey{part.Position.Line, part.Position.Column}

					orig, ok := origByPos[key]
					if !ok {
						continue
					}

					delete(origByPos, key)

					assert.Equal(t, orig.Offset, part.Position.Offset,
						"Offset mismatch at line %d col %d", key.line, key.col)
					assert.Equal(t, orig.IndentNum, part.Position.IndentNum,
						"IndentNum mismatch at line %d col %d", key.line, key.col)
					assert.Equal(t, orig.IndentLevel, part.Position.IndentLevel,
						"IndentLevel mismatch at line %d col %d", key.line, key.col)
				}
			}

			assert.Empty(t, origByPos, "every lexer position should match a part")
		})
	}
}

func TestNewLines_SplitTokenOffsets(t *testing.T) {
	t.Parallel()

	tcs := map[string]string{
		"literal block":                    "script: |\n  line1\n  line2\n",
		"folded block":                     "text: >\n  first\n  second\n",
		"leading indent then continuation": "  b\ns:",
		"multiline string": `key: "line1
  line2"
`,
	}

	for name, input := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := line.NewLines(tokens.Tokenize(input))

			var prevOffset int

			for i := range lines.All() {
				ln := lines.Line(i)
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

	// Test that part Offsets count runes, not bytes, as the go-yaml lexer
	// does. UTF-8 chars like 日 are 3 bytes each, but 1 rune each.
	//
	// For input "日: value\n":
	//   - "日" part at offset 1 (first position)
	//   - ":" part at offset 2 (after 1 rune for 日)
	//   - "value" part at offset 4 (after 3 runes for "日: ")
	//
	// If byte-based, ":" would be at offset 4 (after 3 bytes for 日).
	input := "日: value\n"
	lines := requireRoundtrip(t, input)

	// The ":" (MappingValue) part sits at offset 2, not 4, which proves
	// rune-based counting.
	require.Equal(t, 1, lines.Len())

	parts := lines.Line(0).Tokens()
	require.Len(t, parts, 3, "expected 3 parts: key, :, value")

	// Part 0: "日" at offset 1.
	assert.Equal(t, 1, parts[0].Position.Offset, "first part offset should be 1")

	// Part 1: ":" at offset 2 (rune-based) not 4 (byte-based).
	assert.Equal(t, 2, parts[1].Position.Offset,
		"MappingValue ':' should be at offset 2 (rune-based), not 4 (byte-based)")

	// Part 2: "value" at offset 4 (after "日: " which is 3 runes).
	assert.Equal(t, 4, parts[2].Position.Offset, "value part offset should be 4")

	// The total bytes match, so the Origin content survives the split.
	var origTotalBytes, resultTotalBytes int

	for _, tk := range tokens.Tokenize(input) {
		origTotalBytes += len(tk.Origin)
	}

	for _, part := range parts {
		resultTotalBytes += len(part.Origin)
	}

	assert.Equal(t, origTotalBytes, resultTotalBytes, "total bytes should match lexer output")
}

func TestNewLines_OffsetRuneCount_Continuation(t *testing.T) {
	t.Parallel()

	// Continuation parts of a multi-line token take their offset from the
	// builder's running count, which must advance by runes like the lexer.
	// "key: héllo\n" is 11 runes, so the continuation starts at offset 12.
	input := "key: h\u00e9llo\n  w\u00f6rld\nnext: v\n"
	lines := line.NewLines(tokens.Tokenize(input))
	require.Equal(t, 3, lines.Len())

	continuation := lines.Line(1).Token(0)
	assert.Equal(t, "  w\u00f6rld\n", continuation.Origin)
	assert.Equal(t, 12, continuation.Position.Offset)

	next := lines.Line(2).Token(0)
	assert.Equal(t, "next", next.Value)
	assert.Equal(t, 20, next.Position.Offset)
}

func TestNewLines_IndentLevelProgression(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		root:
		  level1:
		    level2:
		      level3: value
		    back2: val
		  back1: val
		end: val
	`)
	lines := line.NewLines(tokens.Tokenize(input))

	// Expected indent levels per line (based on go-yaml scanner behavior):
	// Line 1: root: -> level 0.
	// Line 2:   level1: -> level 1.
	// Line 3:     level2: -> level 2.
	// Line 4:       level3: value -> level 3.
	// Line 5:     back2: val -> level 2.
	// Line 6:   back1: val -> level 1.
	// Line 7: end: val -> level 0.
	wantLevels := []int{0, 1, 2, 3, 2, 1, 0}

	require.Equal(t, len(wantLevels), lines.Len())

	for i := range lines.All() {
		ln := lines.Line(i)
		if len(ln.Tokens()) > 0 {
			firstTk := ln.Token(0)
			if firstTk.Position != nil {
				assert.Equal(t, wantLevels[i], firstTk.Position.IndentLevel,
					"IndentLevel mismatch at line %d", i+1)
			}
		}
	}
}

func TestNewLines_BlockScalars(t *testing.T) {
	t.Parallel()

	// Tests for block scalar (literal | and folded >) handling.

	t.Run("position semantics", func(t *testing.T) {
		t.Parallel()

		// Block scalar content has one part on each line it spans, from the
		// first line that holds its text. The first part keeps the content's
		// Column and Offset, and the last part that holds content carries
		// the Value, whether content follows the scalar or not.

		tcs := map[string]struct {
			input string
			line  int      // Line of the content's first part.
			want  []string // Value of the content's part on each line from line on.
		}{
			"literal two lines": {
				input: stringtest.Input(`
					key: |
					  line1
					  line2
				`),
				line: 2,
				want: []string{"", "line1\nline2"},
			},
			"literal three lines": {
				input: stringtest.Input(`
					key: |
					  a
					  b
					  c
				`),
				line: 2,
				want: []string{"", "", "a\nb\nc"},
			},
			"folded two lines": {
				input: stringtest.Input(`
					key: >
					  first
					  second
				`),
				line: 2,
				want: []string{"", "first second"},
			},
			"literal with strip": {
				input: stringtest.Input(`
					key: |-
					  line1
					  line2
				`),
				line: 2,
				want: []string{"", "line1\nline2"},
			},
			"literal with keep and trailing blank": {
				input: `key: |+
  line1
  line2

`,
				line: 2,
				want: []string{"", "line1\nline2\n\n", ""},
			},
			"literal followed by a key": {
				input: "key: |\n  line1\n  line2\nnext: 1\n",
				line:  2,
				want:  []string{"", "line1\nline2\n"},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				lines := requireRoundtrip(t, tc.input)

				// The content is the string whose text crosses a line break.
				var contentToken *token.Token

				for _, tk := range tokens.Tokenize(tc.input) {
					if tk.Type == token.StringType && strings.Contains(strings.TrimSpace(tk.Origin), "\n") {
						contentToken = tk
						break
					}
				}

				require.NotNil(t, contentToken, "expected to find block scalar content token")

				for i, want := range tc.want {
					parts := lines.Line(tc.line - 1 + i).Tokens()
					require.Len(t, parts, 1, "line %d", tc.line+i)

					part := parts[0]
					assert.Equal(t, tc.line+i, part.Position.Line, "part %q", part.Origin)
					assert.Equal(t, want, part.Value, "part %q", part.Origin)

					if i == 0 {
						assert.Equal(t, contentToken.Position.Column, part.Position.Column, "part %q", part.Origin)
						assert.Equal(t, contentToken.Position.Offset, part.Position.Offset, "part %q", part.Origin)
					}
				}
			})
		}
	})

	t.Run("empty content edge cases", func(t *testing.T) {
		t.Parallel()

		// A block scalar header can have no content, as in "key: |\n"
		// followed by another key or the end of the document.

		tcs := map[string]string{
			"literal empty followed by key": stringtest.Input(`
				key: |
				next: value
			`),
			"folded empty followed by key": stringtest.Input(`
				key: >
				next: value
			`),
			"literal empty at end": stringtest.Input(`
				key: |
			`),
			"literal strip empty": stringtest.Input(`
				key: |-
				next: value
			`),
			"literal keep empty at end":      "key: |+\n",
			"folded keep empty at end":       "- >+\n",
			"literal keep then dropped line": "key: |+\n!",
		}

		for name, input := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				lines := requireRoundtrip(t, input)

				require.NoError(t, yamltest.ValidateLines(lines))

				// The lexer places the empty content of a scalar that
				// keeps its trailing lines past the end of the source,
				// which adds no line the file does not have.
				want := strings.Count(input, "\n")
				if !strings.HasSuffix(input, "\n") {
					want++
				}

				assert.Equal(t, want, lines.Len())
			})
		}
	})

	t.Run("trailing blanks and chomping", func(t *testing.T) {
		t.Parallel()

		// Test block scalars with trailing blank lines and different chomping indicators.
		// - strip (-): Remove all trailing newlines.
		// - clip (default): Keep single trailing newline.
		// - keep (+): Keep all trailing newlines.

		tcs := map[string]struct {
			input string
			want  string // Expected Value after chomping.
		}{
			"literal keep with trailing blanks": {
				input: `key: |+
  content

`,
				want: "content\n\n",
			},
			"literal strip with content": {
				input: `key: |-
  content
`,
				want: "content",
			},
			"literal clip default": {
				input: `key: |
  content
`,
				want: "content\n",
			},
			"folded keep with trailing blanks": {
				input: `key: >+
  line1
  line2

`,
				want: "line1 line2\n\n",
			},
			"folded strip": {
				input: `key: >-
  line1
  line2
`,
				want: "line1 line2",
			},
			"literal keep multiple trailing": {
				input: `key: |+
  content


`,
				want: "content\n\n\n",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				lines := requireRoundtrip(t, tc.input)

				assert.Equal(t, strings.Count(tc.input, "\n"), lines.Len())
				assert.Equal(t, tc.want, blockScalarValue(t, lines),
					"block scalar Value should match chomping behavior")
			})
		}
	})
}

// blockScalarValue returns the Value of the one part that carries a Value on
// the lines after the first. The input behind lines holds a block scalar
// whose header ends the first line and whose content fills every line after
// it.
func blockScalarValue(t *testing.T, lines line.Lines) string {
	t.Helper()

	var values []string

	for _, ln := range lines.All(position.NewSpan(1, lines.Len())) {
		for _, part := range ln.Tokens() {
			if part.Value != "" {
				values = append(values, part.Value)
			}
		}
	}

	require.Len(t, values, 1, "one content part should carry the Value")

	return values[0]
}

func TestNewLines_PlainMultilinePositionSemantics(t *testing.T) {
	t.Parallel()

	// A plain multiline string has one part on each line it spans, and that
	// part is the last on its line. The first part keeps the string's Column
	// and Offset and carries its Value, which block scalars put on their
	// last content part instead.

	tcs := map[string]struct {
		input string
		line  int      // Line of the string's first part.
		want  []string // Value of the string's part on each line from line on.
	}{
		"plain multiline two lines": {
			input: stringtest.Input(`
				key: this is
				  continued
			`),
			line: 1,
			want: []string{"this is continued", ""},
		},
		"plain multiline three lines": {
			input: stringtest.Input(`
				key: first
				  second
				  third
			`),
			line: 1,
			want: []string{"first second third", "", ""},
		},
		"plain multiline with more indent": {
			input: stringtest.Input(`
				parent:
				  child: line one
				    continued line
			`),
			line: 2, // First line of the value.
			want: []string{"line one continued line", ""},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := requireRoundtrip(t, tc.input)

			// The string is the one whose text crosses a line break.
			var contentToken *token.Token

			for _, tk := range tokens.Tokenize(tc.input) {
				if tk.Type == token.StringType && strings.Contains(strings.TrimSpace(tk.Origin), "\n") {
					contentToken = tk
					break
				}
			}

			require.NotNil(t, contentToken, "expected to find plain multiline string token")

			for i, want := range tc.want {
				parts := lines.Line(tc.line - 1 + i).Tokens()
				require.NotEmpty(t, parts, "line %d", tc.line+i)

				part := parts[len(parts)-1]
				assert.Equal(t, tc.line+i, part.Position.Line, "part %q", part.Origin)
				assert.Equal(t, want, part.Value, "part %q", part.Origin)

				if i == 0 {
					assert.Equal(t, contentToken.Position.Column, part.Position.Column, "part %q", part.Origin)
					assert.Equal(t, contentToken.Position.Offset, part.Position.Offset, "part %q", part.Origin)
				}
			}
		})
	}
}

func TestNewLines_QuotedMultilineActualNewlines(t *testing.T) {
	t.Parallel()

	// Test quoted strings with actual newlines in the content (not escaped \n).
	// The part on the opening quote line keeps the string's Column and
	// Offset and carries its Value. Every later part starts at column 1 with
	// an empty Value.

	tcs := map[string]struct {
		input         string
		want          int // Number of lines the string spans.
		wantTokenType token.Type
	}{
		"double quoted with actual newline": {
			input:         "key: \"line1\nline2\"\n",
			want:          2,
			wantTokenType: token.DoubleQuoteType,
		},
		"double quoted with multiple newlines": {
			input:         "key: \"a\nb\nc\"\n",
			want:          3,
			wantTokenType: token.DoubleQuoteType,
		},
		"single quoted with actual newline": {
			input:         "key: 'line1\nline2'\n",
			want:          2,
			wantTokenType: token.SingleQuoteType,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := requireRoundtrip(t, tc.input)

			// Find the quoted token.
			var quotedToken *token.Token

			for _, tk := range tokens.Tokenize(tc.input) {
				if tk.Type == tc.wantTokenType {
					quotedToken = tk
					break
				}
			}

			require.NotNil(t, quotedToken, "expected to find quoted string token")
			require.Equal(t, tc.want, lines.Len())

			// The string's first part is the last part on the first line.
			parts := lines.Line(0).Tokens()
			require.NotEmpty(t, parts)

			first := parts[len(parts)-1]
			assert.Equal(t, tc.wantTokenType, first.Type)
			assert.Equal(t, 1, first.Position.Line)
			assert.Equal(t, quotedToken.Position.Column, first.Position.Column,
				"first part should keep the opening quote's Column")
			assert.Equal(t, quotedToken.Position.Offset, first.Position.Offset,
				"first part should keep the opening quote's Offset")
			assert.Equal(t, quotedToken.Value, first.Value, "first part should carry the Value")

			// Each later line holds one continuation part.
			for i, ln := range lines.All(position.NewSpan(1, lines.Len())) {
				require.Len(t, ln.Tokens(), 1, "line %d", i+1)

				part := ln.Token(0)
				assert.Equal(t, tc.wantTokenType, part.Type, "part %q", part.Origin)
				assert.Equal(t, i+1, part.Position.Line, "part %q", part.Origin)
				assert.Equal(t, 1, part.Position.Column, "part %q", part.Origin)
				assert.Empty(t, part.Value, "part %q", part.Origin)
			}
		})
	}
}

func TestNewLines_ColumnPositionAfterSplit(t *testing.T) {
	t.Parallel()

	// Test that NewLines calculates Column positions correctly when it splits
	// multiline tokens across lines.
	//
	// The first part of a block or plain multiline scalar keeps the token's
	// Column, which names where the scalar's text starts. Every later part
	// starts at column 1.

	t.Run("block scalar column positions", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			key: |
			  line1
			  line2
		`)
		lines := requireRoundtrip(t, input)

		// The input holds three lines.
		require.Equal(t, 3, lines.Len())

		// The token sits where "line1" starts, past two spaces of
		// indentation, and its first part carries that column. The second
		// line's part starts with the indentation, at column 1.
		first := lines.Line(1).Tokens()
		require.NotEmpty(t, first)
		assert.Equal(t, 3, first[0].Position.Column)

		second := lines.Line(2).Tokens()
		require.NotEmpty(t, second)
		assert.Equal(t, 1, second[0].Position.Column)
	})

	t.Run("plain multiline column positions", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			key: this is
			  continued
		`)
		lines := requireRoundtrip(t, input)

		// The input holds two lines.
		require.Equal(t, 2, lines.Len())

		// The scalar's first part follows "key: " and keeps the token's
		// column. The continuation part starts with the indentation, at
		// column 1.
		first := lines.Line(0).Tokens()
		require.Len(t, first, 3)
		assert.Equal(t, 6, first[2].Position.Column)

		second := lines.Line(1).Tokens()
		require.NotEmpty(t, second)
		assert.Equal(t, 1, second[0].Position.Column)
	})
}

func TestEmptyAndZeroValues(t *testing.T) {
	t.Parallel()

	t.Run("Line/zero value", func(t *testing.T) {
		t.Parallel()

		var l line.Line

		assert.Equal(t, 0, l.Number())
		assert.True(t, l.IsEmpty())
		assert.Empty(t, l.Content())
		assert.Nil(t, l.Tokens())
		assert.Equal(t, "     | ", l.String())
	})

	t.Run("Line/with tokens not empty", func(t *testing.T) {
		t.Parallel()

		tks := tokens.Tokenize("key: value\n")
		lines := line.NewLines(tks)

		require.Equal(t, 1, lines.Len())
		assert.False(t, lines.Line(0).IsEmpty())
	})

	t.Run("Lines/nil", func(t *testing.T) {
		t.Parallel()

		var lines line.Lines

		assert.Nil(t, lines.Tokens())
		assert.NoError(t, yamltest.ValidateLines(lines))
	})

	t.Run("Lines/empty slice", func(t *testing.T) {
		t.Parallel()

		lines := line.Lines{}
		assert.Nil(t, lines.Tokens())
		assert.NoError(t, yamltest.ValidateLines(lines))
	})

	t.Run("Lines/NewLines with nil tokens", func(t *testing.T) {
		t.Parallel()

		lines := line.NewLines(nil)
		assert.True(t, lines.IsEmpty())
		assert.Equal(t, 0, lines.Len())
	})

	t.Run("Lines/NewLines with empty tokens", func(t *testing.T) {
		t.Parallel()

		lines := line.NewLines(tokens.Tokenize(""))
		assert.True(t, lines.IsEmpty())
		assert.Equal(t, 0, lines.Len())
	})
}

func TestNewLines_BlockScalarPositionBehavior(t *testing.T) {
	t.Parallel()

	// Every part of a block scalar sits on its own line. The first part
	// carries the token's Column, which names the text past the
	// indentation, and every later part starts at column 1 with the
	// indentation inside it, whether content follows the scalar or not.
	tcs := map[string]struct {
		input string
		want  map[int]int // Column of the first part on each 1-indexed line.
	}{
		"single-line with following content": {
			input: "key: |\n  content\nnext: value\n",
			want:  map[int]int{2: 3},
		},
		"single-line standalone": {
			input: "key: |\n  content\n",
			want:  map[int]int{2: 3},
		},
		"multi-line with following content": {
			input: "key: |\n  line1\n  line2\nnext: data\n",
			want:  map[int]int{2: 3, 3: 1},
		},
		"multi-line standalone": {
			input: "key: |\n  line1\n  line2\n",
			want:  map[int]int{2: 3, 3: 1},
		},
		"three-line with following content": {
			input: "key: |\n  a\n  b\n  c\nnext: data\n",
			want:  map[int]int{2: 3, 3: 1, 4: 1},
		},
		"three-line standalone": {
			input: "key: |\n  a\n  b\n  c\n",
			want:  map[int]int{2: 3, 3: 1, 4: 1},
		},
		"deeper indentation": {
			input: "a:\n  key: |\n    line1\n    line2\n  next: 1\n",
			want:  map[int]int{3: 5, 4: 1},
		},
		"leading blank line": {
			input: "key: |\n\n  line2\nnext: 1\n",
			want:  map[int]int{2: 1, 3: 3},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := requireRoundtrip(t, tc.input)

			require.NoError(t, yamltest.ValidateLines(lines))

			for i := range lines.All() {
				ln := lines.Line(i)

				want, ok := tc.want[ln.Number()]
				if !ok {
					continue
				}

				require.NotEmpty(t, ln.Tokens(), "line %d", ln.Number())

				part := ln.Token(0)
				assert.Equal(t, token.StringType, part.Type, "line %d part %q", ln.Number(), part.Origin)
				assert.Equal(t, ln.Number(), part.Position.Line, "line %d part %q", ln.Number(), part.Origin)
				assert.Equal(t, want, part.Position.Column, "line %d part %q", ln.Number(), part.Origin)
			}
		})
	}
}

// TestNewLines_BlankLineAbsorption documents how the go-yaml lexer handles
// blank lines. The lexer absorbs a blank line into the previous token's
// Origin rather than emitting a separate token.
// NewLines still yields one Line per source line and round-trips the tokens.
func TestNewLines_BlankLineAbsorption(t *testing.T) {
	t.Parallel()

	t.Run("single blank line absorbed into previous token", func(t *testing.T) {
		t.Parallel()

		// Blank line between two key-value pairs.
		// The lexer absorbs the blank line into the first value's Origin.
		input := "key: value\n\nnext: data\n"

		lines := requireRoundtrip(t, input)

		// The value token's Origin holds the blank line, so it is
		// " value\n\n" with two newlines.
		var valueToken *token.Token

		for _, tk := range lines.Tokens() {
			if tk.Value == "value" {
				valueToken = tk
				break
			}
		}

		require.NotNil(t, valueToken, "expected to find value token")
		assert.Contains(t, valueToken.Origin, "\n\n",
			"value token Origin should contain absorbed blank line")
	})

	t.Run("multiple blank lines absorbed", func(t *testing.T) {
		t.Parallel()

		// Multiple blank lines between key-value pairs.
		input := "key: value\n\n\nnext: data\n"

		lines := requireRoundtrip(t, input)

		var valueToken *token.Token

		for _, tk := range lines.Tokens() {
			if tk.Value == "value" {
				valueToken = tk
				break
			}
		}

		require.NotNil(t, valueToken, "expected to find value token")
		assert.Contains(t, valueToken.Origin, "\n\n\n",
			"value token Origin should contain multiple absorbed blank lines")
	})

	t.Run("line numbers jump across blank lines", func(t *testing.T) {
		t.Parallel()

		// Lines tracks line numbers across gaps.
		input := "key: value\n\nnext: data\n"

		original := tokens.Tokenize(input)
		lines := line.NewLines(original)

		// The lines sit at positions 1, 2 (blank absorbed), and 3.
		require.Equal(t, 3, lines.Len(), "expected 3 lines including blank")

		// The line numbers are 1, 2, 3.
		assert.Equal(t, 1, lines.Line(0).Number(), "first line should be 1")
		assert.Equal(t, 2, lines.Line(1).Number(), "second line (blank) should be 2")
		assert.Equal(t, 3, lines.Line(2).Number(), "third line should be 3")
	})

	t.Run("a first token whose text opens on an earlier line starts there", func(t *testing.T) {
		t.Parallel()

		// The lexer folds the lines before a merge key into its token and
		// positions the token on the line of the key, so the lines start
		// that many lines earlier.
		for _, input := range []string{
			"x\nx\n<<: *an\n",
			"  text\n<<: *an\n",
			"x\n*a\n\n<<: *an\n",
			"\n\nfoo: 1\n",
			"\"a\n b\"\n",
			"|\n  a\n  b\n",
			"# c\nk: v\n",
		} {
			lines := line.NewLines(tokens.Tokenize(input))
			want := strings.Count(input, "\n")

			require.Equal(t, want, lines.Len(), "input %q", input)

			for i := range want {
				assert.Equal(t, i+1, lines.Line(i).Number(), "input %q line %d", input, i)
			}
		}
	})

	t.Run("a blank line after a bare block scalar header stays a line", func(t *testing.T) {
		t.Parallel()

		// The lexer emits the blank line as a token that is a line ending
		// alone, positioned on its own line. It ends the header's line
		// rather than syncing past it, so the blank line keeps its row.
		for _, input := range []string{">\n\na: 1\n", "|\n\nr\n", "\n>\n\n>\n# c\n"} {
			lines := line.NewLines(tokens.Tokenize(input))
			want := strings.Count(input, "\n")

			require.Equal(t, want, lines.Len(), "input %q", input)

			for i := range want {
				assert.Equal(t, i+1, lines.Line(i).Number(), "input %q line %d", input, i)
			}
		}
	})

	t.Run("line numbers stay contiguous after block scalar content", func(t *testing.T) {
		t.Parallel()

		// The lexer reports a block scalar content token at the line of its
		// last content line, so the builder must not treat that line as a
		// gap to sync forward to.
		tcs := map[string]struct {
			input string
			want  []int
		}{
			"blank content then key":    {input: "b: |\n  \nlast: 1\n# c\n", want: []int{1, 2, 3, 4}},
			"blank content then marker": {input: "b: |\n  \n---\n", want: []int{1, 2, 3}},
			"content then marker":       {input: "b: |\n   y\n---\n---\nlast: 1\n", want: []int{1, 2, 3, 4, 5}},
			"blank content then tagged": {input: "b: |\n\n!!str s: 1\n", want: []int{1, 2, 3}},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				lines := line.NewLines(tokens.Tokenize(tc.input))

				got := make([]int, 0, lines.Len())
				for _, ln := range lines.All() {
					got = append(got, ln.Number())
				}

				assert.Equal(t, tc.want, got)
			})
		}
	})
}

// TestNewLines_TrailingBlankLines verifies that a file ending in blank lines
// yields one Line per text line, including the blank ones.
func TestNewLines_TrailingBlankLines(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  int
	}{
		"no trailing newline":      {input: "a: 1", want: 1},
		"trailing newline":         {input: "a: 1\n", want: 1},
		"one trailing blank line":  {input: "a: 1\n\n", want: 2},
		"two trailing blank lines": {input: "a: 1\n\n\n", want: 3},
		"crlf trailing blank line": {input: "a: 1\r\n\r\n", want: 2},
		"comment then blank line":  {input: "a: 1\n# c\n\n", want: 3},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := line.NewLines(tokens.Tokenize(tc.input))

			assert.Equal(t, tc.want, lines.Len())
		})
	}
}

// TestNewLines_PartLinksStopAtLineBoundary verifies that the per-line part
// chain never crosses a line, including after a gap the builder flushes.
func TestNewLines_PartLinksStopAtLineBoundary(t *testing.T) {
	t.Parallel()

	tcs := map[string]string{
		"gap after multi-part token": "''\n\n:",
		"quoted scalar before a key": "s: 'q\nr'\n\r\n: v\n   \nj: 1\n",
		"sequence end before a key":  "]\n\n:",
	}

	for name, input := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			for _, ln := range line.NewLines(tokens.Tokenize(input)).All() {
				parts := ln.Tokens()
				if len(parts) == 0 {
					continue
				}

				assert.Nil(t, parts[0].Prev, "line %d first part has a Prev", ln.Number())
				assert.Nil(t, parts[len(parts)-1].Next, "line %d last part has a Next", ln.Number())
			}
		})
	}
}

// TestNewLines_FoldedBlockBlankLines verifies handling of blank lines within
// folded block scalars.
//
// In folded scalars, a blank line preserves a line break instead of folding
// to a space.
func TestNewLines_FoldedBlockBlankLines(t *testing.T) {
	t.Parallel()

	t.Run("folded block with blank line preserves break", func(t *testing.T) {
		t.Parallel()

		// In folded blocks, blank lines cause a line break in the Value.
		// A blank line separates "first" and "second", and it becomes a
		// newline in the Value instead of a space.
		input := "text: >\n  first\n\n  second\n"

		lines := requireRoundtrip(t, input)

		assert.Equal(t, 4, lines.Len())
		// The blank line causes a paragraph break in folded output.
		assert.Contains(t, blockScalarValue(t, lines), "\n",
			"folded block with blank line should have newline in Value")
	})

	t.Run("folded block without blank line folds to space", func(t *testing.T) {
		t.Parallel()

		// Without blank lines, folded content joins with spaces.
		input := "text: >\n  first\n  second\n"

		lines := requireRoundtrip(t, input)

		assert.Equal(t, 3, lines.Len())
		// Adjacent lines fold to a space, so the Value is "first second\n".
		assert.Equal(t, "first second\n", blockScalarValue(t, lines),
			"folded block without blank line should have space-joined Value")
	})

	t.Run("literal block preserves all blank lines", func(t *testing.T) {
		t.Parallel()

		// Literal blocks preserve blank lines as-is.
		input := "text: |\n  first\n\n  second\n"

		lines := requireRoundtrip(t, input)

		assert.Equal(t, 4, lines.Len())
		// Literal preserves blank line as newline.
		assert.Equal(t, "first\n\nsecond\n", blockScalarValue(t, lines),
			"literal block should preserve blank line in Value")
	})

	t.Run("folded with multiple blank lines", func(t *testing.T) {
		t.Parallel()

		// Multiple blank lines in folded content.
		input := "text: >\n  first\n\n\n  second\n"

		lines := requireRoundtrip(t, input)

		assert.Equal(t, 5, lines.Len())
		// Folding drops the break after "first" and keeps one per blank line.
		assert.Equal(t, "first\n\nsecond\n", blockScalarValue(t, lines))
	})
}

func TestLines_TokenAt(t *testing.T) {
	t.Parallel()

	t.Run("returns token at position", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := tokens.Tokenize(input)
		lines := line.NewLines(tks)

		// Get token at start of line.
		tk := lines.TokenAt(position.New(0, 0))
		require.NotNil(t, tk)
		assert.Equal(t, "key", tk.Value)
	})

	t.Run("returns token at column offset", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := tokens.Tokenize(input)
		lines := line.NewLines(tks)

		// Get token in middle of "value" (column 5 is 'v').
		tk := lines.TokenAt(position.New(0, 5))
		require.NotNil(t, tk)
		assert.Equal(t, "value", tk.Value)
	})

	t.Run("multiline token returns same source", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			key: |
			  line1
			  line2
		`)
		tks := tokens.Tokenize(input)
		lines := line.NewLines(tks)

		// Get source token from different lines of the literal block.
		tk1 := lines.TokenAt(position.New(1, 0))
		tk2 := lines.TokenAt(position.New(2, 0))

		require.NotNil(t, tk1)
		require.NotNil(t, tk2)
		// Both lines belong to the same source token, so TokenAt returns the
		// same original pointer for each.
		assert.Same(t, tk1, tk2)
	})

	t.Run("indentation returns the token after it", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input string
			line  int
		}{
			"after plain value": {
				input: "parent:\n  child: x\n  other: 1\n",
				line:  2,
			},
			"after quoted value": {
				input: "parent:\n  child: \"x\"\n  other: 1\n",
				line:  2,
			},
			"after bool value": {
				input: "parent:\n  child: true\n  other: 1\n",
				line:  2,
			},
			"after block scalar": {
				input: "parent:\n  child: |\n    a\n  other: 1\n",
				line:  3,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				lines := line.NewLines(tokens.Tokenize(tc.input))

				for col := range 2 {
					tk := lines.TokenAt(position.New(tc.line, col))
					require.NotNil(t, tk)
					assert.Equal(t, "other", tk.Value)
					assert.Equal(t,
						position.Ranges{position.NewRange(position.New(tc.line, 2), position.New(tc.line, 7))},
						lines.ContentRanges(tk))
				}
			})
		}
	})

	t.Run("out of bounds line returns nil", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := tokens.Tokenize(input)
		lines := line.NewLines(tks)

		assert.Nil(t, lines.TokenAt(position.New(-1, 0)))
		assert.Nil(t, lines.TokenAt(position.New(999, 0)))
	})

	t.Run("column outside token range returns nil", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := tokens.Tokenize(input)
		lines := line.NewLines(tks)

		assert.Nil(t, lines.TokenAt(position.New(0, 100)))
	})
}

func TestLines_TokenRanges(t *testing.T) {
	t.Parallel()

	t.Run("single token range", func(t *testing.T) {
		t.Parallel()

		tks := tokens.Tokenize("key: value\n")
		lines := line.NewLines(tks)

		// The lexer's own token matches by pointer identity.
		ranges := lines.TokenRanges(tks[0])
		assert.Equal(t, position.Ranges{
			position.NewRange(position.New(0, 0), position.New(0, 3)),
		}, ranges)
	})

	t.Run("multiline token returns one range per line", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			key: |
			  line1
			  line2
		`)
		tks := tokens.Tokenize(input)
		lines := line.NewLines(tks)

		var tk *token.Token

		for _, lexTk := range tks {
			if lexTk.Type == token.StringType && strings.Contains(lexTk.Origin, "line1") {
				tk = lexTk
				break
			}
		}

		require.NotNil(t, tk)

		ranges := lines.TokenRanges(tk)
		require.Len(t, ranges, 2)
		assert.Equal(t, 1, ranges[0].Start.Line)
		assert.Equal(t, 2, ranges[1].Start.Line)
	})

	t.Run("token from TokenAt round-trips", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			key: |
			  line1
			  line2
		`)
		lines := line.NewLines(tokens.Tokenize(input))

		// A position inside the block content resolves to the whole token.
		tk := lines.TokenAt(position.New(1, 2))
		require.NotNil(t, tk)

		ranges := lines.TokenRanges(tk)
		require.Len(t, ranges, 2)
		assert.Equal(t, 1, ranges[0].Start.Line)
		assert.Equal(t, 2, ranges[1].Start.Line)
	})

	t.Run("part token from Line.Tokens matches its own line", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			key: |
			  line1
			  line2
		`)
		lines := line.NewLines(tokens.Tokenize(input))

		part := lines.Line(2).Token(0)

		ranges := lines.TokenRanges(part)
		assert.Equal(t, position.Ranges{
			position.NewRange(position.New(2, 0), position.New(2, 7)),
		}, ranges)
	})

	t.Run("value token starts after the key", func(t *testing.T) {
		t.Parallel()

		lines := line.NewLines(tokens.Tokenize("key: value\n"))

		ranges := lines.TokenRanges(lines.TokenAt(position.New(0, 5)))
		require.Len(t, ranges, 1)
		assert.Equal(t, 4, ranges[0].Start.Col)
	})

	t.Run("nil token returns nil", func(t *testing.T) {
		t.Parallel()

		lines := line.NewLines(tokens.Tokenize("key: value\n"))

		assert.Nil(t, lines.TokenRanges(nil))
		assert.Nil(t, lines.TokenRanges(lines.TokenAt(position.New(0, 100))))
	})

	t.Run("token not in lines returns nil", func(t *testing.T) {
		t.Parallel()

		lines := line.NewLines(tokens.Tokenize("key: value\n"))
		other := tokens.Tokenize("other: data\n")

		assert.Nil(t, lines.TokenRanges(other[0]))
	})

	t.Run("a copy of a token matches by its fields", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			key: |
			  line1
			  line2
		`)
		tks := tokens.Tokenize(input)
		lines := line.NewLines(tks)

		// A parser hands out copies of the tokens the caller gave it, and a
		// copy finds the same ranges as the original.
		var tk *token.Token

		for _, lexTk := range tks {
			if lexTk.Type == token.StringType && strings.Contains(lexTk.Origin, "line1") {
				tk = lexTk
			}
		}

		require.NotNil(t, tk)
		assert.Equal(t, lines.TokenRanges(tk), lines.TokenRanges(tk.Clone()))
		assert.Equal(t, lines.ContentRanges(tk), lines.ContentRanges(tk.Clone()))
		assert.Equal(t, lines.TokenRanges(lines.Line(1).Token(0)), lines.TokenRanges(lines.Line(1).Token(0).Clone()))

		// This token from another stream sits at the same position but
		// differs in its text, so it matches nothing.
		other := tokens.Tokenize("key: |\n  other\n  lines\n")
		assert.Nil(t, lines.TokenRanges(other[len(other)-1]))
	})

	t.Run("ranges name indices in the collection", func(t *testing.T) {
		t.Parallel()

		// The two content lines of a block scalar, collected in reverse
		// order.
		src := line.NewLines(tokens.Tokenize("key: |\n  line1\n  line2\n"))
		reversed := line.Collect(src.Line(2), src.Line(1))
		block := src.TokenAt(position.New(1, 2))

		// Two sections whose line numbers jump from 11 to 40, so the line
		// numbers of the tokens differ from the indices of their lines.
		gappedTks := token.Tokens{}

		for _, tk := range tokens.Tokenize("key1: value1\nkey2: value2\n") {
			tk.Position.Line += 9
			gappedTks.Add(tk)
		}

		for _, tk := range tokens.Tokenize("key3: |\n  a\n  b\n") {
			tk.Position.Line += 39
			gappedTks.Add(tk)
		}

		gapped := line.NewLines(gappedTks)
		require.Equal(t, 5, gapped.Len())
		require.Equal(t, 40, gapped.Line(2).Number())

		// Two revisions interleaved as a diff shows them, where the deleted
		// and inserted lines each hold a "b" key at the same position.
		before := line.NewLines(tokens.Tokenize("a: 1\nb: 2\n"))
		after := line.NewLines(tokens.Tokenize("a: 1\nb: 3\n"))
		revisions := line.Collect(after.Line(0), before.Line(1), after.Line(1))

		tcs := map[string]struct {
			lines       line.Lines
			tk          *token.Token
			wantToken   position.Ranges
			wantContent position.Ranges
		}{
			"collected lexer token": {
				lines: reversed,
				tk:    block,
				wantToken: position.Ranges{
					position.NewRange(position.New(0, 0), position.New(0, 7)),
					position.NewRange(position.New(1, 0), position.New(1, 7)),
				},
				wantContent: position.Ranges{
					position.NewRange(position.New(0, 2), position.New(0, 7)),
					position.NewRange(position.New(1, 2), position.New(1, 7)),
				},
			},
			"collected part token": {
				lines: reversed,
				tk:    src.Line(1).Token(0),
				wantToken: position.Ranges{
					position.NewRange(position.New(1, 0), position.New(1, 7)),
				},
				wantContent: position.Ranges{
					position.NewRange(position.New(1, 2), position.New(1, 7)),
				},
			},
			"collected copy of a token": {
				lines: reversed,
				tk:    block.Clone(),
				wantToken: position.Ranges{
					position.NewRange(position.New(0, 0), position.New(0, 7)),
					position.NewRange(position.New(1, 0), position.New(1, 7)),
				},
				wantContent: position.Ranges{
					position.NewRange(position.New(0, 2), position.New(0, 7)),
					position.NewRange(position.New(1, 2), position.New(1, 7)),
				},
			},
			"gapped lexer token": {
				lines: gapped,
				tk:    gapped.TokenAt(position.New(3, 2)),
				wantToken: position.Ranges{
					position.NewRange(position.New(3, 0), position.New(3, 3)),
					position.NewRange(position.New(4, 0), position.New(4, 3)),
				},
				wantContent: position.Ranges{
					position.NewRange(position.New(3, 2), position.New(3, 3)),
					position.NewRange(position.New(4, 2), position.New(4, 3)),
				},
			},
			"gapped part token": {
				lines: gapped,
				tk:    gapped.Line(4).Token(0),
				wantToken: position.Ranges{
					position.NewRange(position.New(4, 0), position.New(4, 3)),
				},
				wantContent: position.Ranges{
					position.NewRange(position.New(4, 2), position.New(4, 3)),
				},
			},
			"gapped copy of a token": {
				lines: gapped,
				tk:    gapped.TokenAt(position.New(1, 6)).Clone(),
				wantToken: position.Ranges{
					position.NewRange(position.New(1, 5), position.New(1, 12)),
				},
				wantContent: position.Ranges{
					position.NewRange(position.New(1, 6), position.New(1, 12)),
				},
			},
			"inserted revision token": {
				lines: revisions,
				tk:    revisions.TokenAt(position.New(2, 0)),
				wantToken: position.Ranges{
					position.NewRange(position.New(2, 0), position.New(2, 1)),
				},
				wantContent: position.Ranges{
					position.NewRange(position.New(2, 0), position.New(2, 1)),
				},
			},
			"deleted revision token": {
				lines: revisions,
				tk:    revisions.TokenAt(position.New(1, 0)),
				wantToken: position.Ranges{
					position.NewRange(position.New(1, 0), position.New(1, 1)),
				},
				wantContent: position.Ranges{
					position.NewRange(position.New(1, 0), position.New(1, 1)),
				},
			},
			"revision part token": {
				lines: revisions,
				tk:    revisions.Line(2).Token(0),
				wantToken: position.Ranges{
					position.NewRange(position.New(2, 0), position.New(2, 1)),
				},
				wantContent: position.Ranges{
					position.NewRange(position.New(2, 0), position.New(2, 1)),
				},
			},
			"copy of a revision token": {
				// A copy holds no pointer to tell the revisions apart, so it
				// matches the token of each.
				lines: revisions,
				tk:    revisions.TokenAt(position.New(2, 0)).Clone(),
				wantToken: position.Ranges{
					position.NewRange(position.New(1, 0), position.New(1, 1)),
					position.NewRange(position.New(2, 0), position.New(2, 1)),
				},
				wantContent: position.Ranges{
					position.NewRange(position.New(1, 0), position.New(1, 1)),
					position.NewRange(position.New(2, 0), position.New(2, 1)),
				},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				require.NotNil(t, tc.tk)
				assert.Equal(t, tc.wantToken, tc.lines.TokenRanges(tc.tk))
				assert.Equal(t, tc.wantContent, tc.lines.ContentRanges(tc.tk))
			})
		}
	})
}

func TestLines_String(t *testing.T) {
	t.Parallel()

	t.Run("single line", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := tokens.Tokenize(input)
		lines := line.NewLines(tks)

		result := lines.String()
		assert.Contains(t, result, "key: value")
		// The row carries a line number prefix.
		assert.Contains(t, result, "1 |")
	})

	t.Run("multiple lines", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			key1: value1
			key2: value2
		`)
		tks := tokens.Tokenize(input)
		lines := line.NewLines(tks)

		result := lines.String()
		assert.Contains(t, result, "key1")
		assert.Contains(t, result, "value1")
		assert.Contains(t, result, "key2")
		assert.Contains(t, result, "value2")
	})

	t.Run("empty lines", func(t *testing.T) {
		t.Parallel()

		var lines line.Lines

		result := lines.String()
		assert.Empty(t, result)
	})
}

func TestLines_Content_Empty(t *testing.T) {
	t.Parallel()

	t.Run("nil lines returns empty string", func(t *testing.T) {
		t.Parallel()

		var lines line.Lines

		assert.Empty(t, lines.Content())
	})

	t.Run("empty slice returns empty string", func(t *testing.T) {
		t.Parallel()

		lines := line.Lines{}
		assert.Empty(t, lines.Content())
	})
}

func TestLine_Number_Fallbacks(t *testing.T) {
	t.Parallel()

	t.Run("line with set number returns that number", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := tokens.Tokenize(input)
		lines := line.NewLines(tks)

		require.Equal(t, 1, lines.Len())
		// The line number is 1, since the lexer counts lines from 1.
		assert.Equal(t, 1, lines.Line(0).Number())
	})

	t.Run("empty line returns zero", func(t *testing.T) {
		t.Parallel()

		// Create an empty Line directly.
		var ln line.Line

		assert.Equal(t, 0, ln.Number())
	})

	t.Run("multiple lines have correct numbers", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			key1: value1
			key2: value2
			key3: value3
		`)
		tks := tokens.Tokenize(input)
		lines := line.NewLines(tks)

		require.Equal(t, 3, lines.Len())
		assert.Equal(t, 1, lines.Line(0).Number())
		assert.Equal(t, 2, lines.Line(1).Number())
		assert.Equal(t, 3, lines.Line(2).Number())
	})

	t.Run("token with nil position numbers from one", func(t *testing.T) {
		t.Parallel()

		tks := token.Tokens{}
		tks.Add(&token.Token{
			Type:     token.StringType,
			Origin:   "test\n",
			Value:    "test",
			Position: nil, // Nil position.
		})

		lines := line.NewLines(tks)

		require.Equal(t, 1, lines.Len())

		// With no Position to read, NewLines numbers the stream from line 1.
		assert.Equal(t, 1, lines.Line(0).Number())
		assert.Equal(t, 1, lines.Line(0).Token(0).Position.Line)
	})

	t.Run("fallback to segment position line", func(t *testing.T) {
		t.Parallel()

		strTkb := yamltest.NewTokenBuilder().Type(token.StringType)

		// NewLines numbers each line from its tokens, so a lone token at
		// line 42 yields a line numbered 42.
		tks := token.Tokens{}
		tks.Add(strTkb.Clone().
			Origin("value\n").
			Value("value").
			PositionLine(42).
			PositionColumn(1).
			Build())

		lines := line.NewLines(tks)
		require.Equal(t, 1, lines.Len())

		// The line takes its number from the position of the token.
		assert.Equal(t, 42, lines.Line(0).Number())
	})

	t.Run("number field takes precedence over segment position", func(t *testing.T) {
		t.Parallel()

		strTkb := yamltest.NewTokenBuilder().Type(token.StringType)

		// Each line takes the number from the token that starts it, so a
		// gap in Position.Line leaves a gap in the line numbers.
		tks := token.Tokens{}
		// First token at line 100.
		tks.Add(strTkb.Clone().
			Origin("first\n").
			Value("first").
			PositionLine(100).
			PositionColumn(1).
			Build())
		// Second token at line 200 (gap).
		tks.Add(strTkb.Clone().
			Origin("second\n").
			Value("second").
			PositionLine(200).
			PositionColumn(1).
			Build())

		lines := line.NewLines(tks)
		require.Equal(t, 2, lines.Len())

		// Both lines keep their original line numbers.
		assert.Equal(t, 100, lines.Line(0).Number())
		assert.Equal(t, 200, lines.Line(1).Number())
	})
}

// TestNewLines_WhitespaceType verifies that NewLines assigns SpaceType to
// pure horizontal whitespace parts for correct styling.
//
// This handles cases where the go-yaml lexer bundles trailing whitespace (like
// next line's indentation) with the previous token.
func TestNewLines_WhitespaceType(t *testing.T) {
	t.Parallel()

	t.Run("trailing whitespace becomes SpaceType", func(t *testing.T) {
		t.Parallel()

		// The lexer bundles " true\n  " into one boolean token, where "  " is
		// the indentation of "other". After splitting, the "  " part on line
		// 3 is SpaceType, not BoolType.
		input := stringtest.Input(`
			parent:
			  child: true
			  other: 1
		`)
		lines := line.NewLines(tokens.Tokenize(input))
		require.Greater(t, lines.Len(), 2)

		parts := lines.Line(2).Tokens()
		require.NotEmpty(t, parts)
		assert.Equal(t, "  ", parts[0].Origin)
		assert.Equal(t, token.SpaceType, parts[0].Type)
	})

	t.Run("block scalar whitespace preserved as StringType", func(t *testing.T) {
		t.Parallel()

		// The lexer bundles the indentation of "next" into the block scalar
		// token. Whitespace parts of block scalar content keep StringType, so
		// the "  " part on line 4 is not SpaceType.
		input := stringtest.Input(`
			root:
			  text: |
			    a
			  next: x
		`)
		lines := line.NewLines(tokens.Tokenize(input))
		require.Greater(t, lines.Len(), 3)

		parts := lines.Line(3).Tokens()
		require.NotEmpty(t, parts)
		assert.Equal(t, "  ", parts[0].Origin)
		assert.Equal(t, token.StringType, parts[0].Type)
	})

	t.Run("nested indentation with boolean", func(t *testing.T) {
		t.Parallel()

		// More complex case: nested structure with boolean values.
		input := stringtest.Input(`
			root:
			  nested:
			    enabled: true
			    disabled: false
		`)
		tks := tokens.Tokenize(input)
		lines := line.NewLines(tks)

		// Every pure horizontal whitespace part is SpaceType.
		checked := 0
		for i, ln := range lines.All() {
			for _, tk := range ln.Tokens() {
				if strings.TrimSpace(tk.Origin) == "" && tk.Origin != "" && !strings.Contains(tk.Origin, "\n") {
					checked++

					assert.Equal(t, token.SpaceType, tk.Type,
						"pure horizontal whitespace should be SpaceType on line %d, got %s for Origin %q",
						i, tk.Type, tk.Origin)
				}
			}
		}

		// The indentation of "disabled" is one such part.
		require.Positive(t, checked)
	})
}

func TestLines_ContentRanges(t *testing.T) {
	t.Parallel()

	t.Run("single line token", func(t *testing.T) {
		t.Parallel()

		lines := line.NewLines(tokens.Tokenize("key: value\n"))

		ranges := lines.ContentRanges(lines.TokenAt(position.New(0, 0)))
		assert.Equal(t, position.Ranges{
			position.NewRange(position.New(0, 0), position.New(0, 3)),
		}, ranges)
	})

	t.Run("multiline token returns one range per line", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			key: |
			  line1
			  line2
		`)
		lines := line.NewLines(tokens.Tokenize(input))

		ranges := lines.ContentRanges(lines.TokenAt(position.New(1, 2)))
		assert.Equal(t, position.Ranges{
			position.NewRange(position.New(1, 2), position.New(1, 7)),
			position.NewRange(position.New(2, 2), position.New(2, 7)),
		}, ranges)
	})

	t.Run("excludes leading and trailing spaces", func(t *testing.T) {
		t.Parallel()

		tks := tokens.Tokenize("key:   value  \n")
		lines := line.NewLines(tks)
		require.Len(t, tks, 3)

		tk := lines.TokenAt(position.New(0, 6))
		require.Same(t, tks[2], tk)

		assert.Equal(t, position.Ranges{
			position.NewRange(position.New(0, 7), position.New(0, 12)),
		}, lines.ContentRanges(tk))
	})

	t.Run("excludes leading and trailing tabs", func(t *testing.T) {
		t.Parallel()

		lines := line.NewLines(tokens.Tokenize("a:\tb\t# c\n"))

		tk := lines.TokenAt(position.New(0, 3))
		require.NotNil(t, tk)
		require.Equal(t, "\tb\t", tk.Origin)

		assert.Equal(t, position.Ranges{
			position.NewRange(position.New(0, 2), position.New(0, 5)),
		}, lines.TokenRanges(tk))
		assert.Equal(t, position.Ranges{
			position.NewRange(position.New(0, 3), position.New(0, 4)),
		}, lines.ContentRanges(tk))
	})

	t.Run("space-only part contributes no range", func(t *testing.T) {
		t.Parallel()

		tks := tokens.Tokenize("a: 'x\n   \n  y'\n")
		lines := line.NewLines(tks)
		require.Len(t, tks, 3)

		tk := lines.TokenAt(position.New(1, 0))
		require.Same(t, tks[2], tk)

		assert.Contains(t, lines.TokenRanges(tk),
			position.NewRange(position.New(1, 0), position.New(1, 3)))
		assert.Equal(t, position.Ranges{
			position.NewRange(position.New(0, 3), position.New(0, 5)),
			position.NewRange(position.New(2, 2), position.New(2, 4)),
		}, lines.ContentRanges(tk))
	})

	t.Run("nil and missing tokens return nil", func(t *testing.T) {
		t.Parallel()

		lines := line.NewLines(tokens.Tokenize("key: value\n"))

		assert.Nil(t, lines.ContentRanges(nil))
		assert.Nil(t, lines.ContentRanges(lines.TokenAt(position.New(0, 100))))
		assert.Nil(t, lines.ContentRanges(lines.TokenAt(position.New(999, 0))))

		var empty line.Lines

		assert.Nil(t, empty.ContentRanges(lines.TokenAt(position.New(0, 0))))
	})
}

func TestLines_View(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		key: value
		list:
		  - one
	`)

	t.Run("Len and IsEmpty", func(t *testing.T) {
		t.Parallel()

		lines := line.NewLines(tokens.Tokenize(input))

		assert.Equal(t, 3, lines.Len())
		assert.False(t, lines.IsEmpty())
	})

	t.Run("Width is the widest line", func(t *testing.T) {
		t.Parallel()

		lines := line.NewLines(tokens.Tokenize(input))

		assert.Equal(t, len("key: value"), lines.Width())
	})

	t.Run("All yields every index and line", func(t *testing.T) {
		t.Parallel()

		lines := line.NewLines(tokens.Tokenize(input))

		var (
			indices  []int
			contents []string
		)

		for i, ln := range lines.All() {
			indices = append(indices, i)
			contents = append(contents, ln.Content())
		}

		assert.Equal(t, []int{0, 1, 2}, indices)
		assert.Equal(t, []string{"key: value", "list:", "  - one"}, contents)
	})

	t.Run("All clamps spans", func(t *testing.T) {
		t.Parallel()

		lines := line.NewLines(tokens.Tokenize(input))

		var indices []int

		for i := range lines.All(position.NewSpan(1, 99)) {
			indices = append(indices, i)
		}

		assert.Equal(t, []int{1, 2}, indices)
	})

	t.Run("All yields each line once in content order", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			spans []position.Span
			want  []int
		}{
			"overlapping": {
				spans: []position.Span{position.NewSpan(0, 2), position.NewSpan(1, 3)},
				want:  []int{0, 1, 2},
			},
			"out of order": {
				spans: []position.Span{position.NewSpan(2, 3), position.NewSpan(0, 1)},
				want:  []int{0, 2},
			},
			"repeated": {
				spans: []position.Span{position.NewSpan(1, 2), position.NewSpan(1, 2)},
				want:  []int{1},
			},
			"only empty spans": {
				spans: []position.Span{position.NewSpan(1, 1), position.NewSpan(2, 2)},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				lines := line.NewLines(tokens.Tokenize(input))

				var got, fromView []int

				for i := range lines.All(tc.spans...) {
					got = append(got, i)
				}

				for i := range line.NewView(lines).All(tc.spans...) {
					fromView = append(fromView, i)
				}

				assert.Equal(t, tc.want, got)
				assert.Equal(t, fromView, got)
			})
		}
	})

	t.Run("All yields the lines of the collection", func(t *testing.T) {
		t.Parallel()

		lines := line.NewLines(tokens.Tokenize(input))

		for i, ln := range lines.All() {
			assert.Same(t, lines.Line(i), ln)
		}
	})

	t.Run("Runes round-trips the input", func(t *testing.T) {
		t.Parallel()

		lines := line.NewLines(tokens.Tokenize(input))

		var sb strings.Builder

		for _, r := range lines.Runes() {
			sb.WriteRune(r)
		}

		assert.Equal(t, input, sb.String())
	})

	t.Run("Runes yields each rune once in content order", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			ranges []position.Range
			want   []position.Position
		}{
			"overlapping": {
				ranges: []position.Range{
					position.NewRange(position.New(0, 0), position.New(0, 3)),
					position.NewRange(position.New(0, 1), position.New(0, 4)),
				},
				want: []position.Position{
					position.New(0, 0), position.New(0, 1), position.New(0, 2), position.New(0, 3),
				},
			},
			"reverse line order": {
				ranges: []position.Range{
					position.NewRange(position.New(2, 0), position.New(2, 2)),
					position.NewRange(position.New(0, 0), position.New(0, 2)),
				},
				want: []position.Position{
					position.New(0, 0), position.New(0, 1), position.New(2, 0), position.New(2, 1),
				},
			},
			"reverse column order on one line": {
				ranges: []position.Range{
					position.NewRange(position.New(1, 3), position.New(1, 5)),
					position.NewRange(position.New(1, 0), position.New(1, 2)),
				},
				want: []position.Position{
					position.New(1, 0), position.New(1, 1), position.New(1, 3), position.New(1, 4),
				},
			},
			"across lines with the line ending": {
				ranges: []position.Range{
					position.NewRange(position.New(0, 8), position.New(1, 2)),
				},
				want: []position.Position{
					position.New(0, 8), position.New(0, 9), position.New(0, 10),
					position.New(1, 0), position.New(1, 1),
				},
			},
			"ending at the start of the next line": {
				ranges: []position.Range{
					position.NewRange(position.New(0, 9), position.New(1, 0)),
				},
				want: []position.Position{position.New(0, 9), position.New(0, 10)},
			},
			"a wide range and a narrow one that starts later": {
				ranges: []position.Range{
					position.NewRange(position.New(0, 0), position.New(2, 1)),
					position.NewRange(position.New(1, 4), position.New(2, 3)),
				},
				want: []position.Position{
					position.New(0, 0), position.New(0, 1), position.New(0, 2), position.New(0, 3),
					position.New(0, 4), position.New(0, 5), position.New(0, 6), position.New(0, 7),
					position.New(0, 8), position.New(0, 9), position.New(0, 10),
					position.New(1, 0), position.New(1, 1), position.New(1, 2), position.New(1, 3),
					position.New(1, 4), position.New(1, 5),
					position.New(2, 0), position.New(2, 1), position.New(2, 2),
				},
			},
			"negative start column": {
				ranges: []position.Range{
					position.NewRange(position.New(1, -2), position.New(1, 1)),
				},
				want: []position.Position{position.New(1, 0)},
			},
			"inverted": {
				ranges: []position.Range{
					position.NewRange(position.New(1, 3), position.New(0, 0)),
					position.NewRange(position.New(2, 3), position.New(2, 1)),
				},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				lines := line.NewLines(tokens.Tokenize(input))

				var got []position.Position

				for pos := range lines.Runes(tc.ranges...) {
					got = append(got, pos)
				}

				assert.Equal(t, tc.want, got)
			})
		}
	})

	t.Run("Runes with ranges yields the runes they contain", func(t *testing.T) {
		t.Parallel()

		lines := line.NewLines(tokens.Tokenize(input))

		// Every range of the content, from every start to every end in
		// both orders, one at a time and all together.
		var ranges []position.Range

		for startLine := range 3 {
			for endLine := range 3 {
				for startCol := -1; startCol < 12; startCol += 3 {
					for endCol := 0; endCol < 12; endCol += 4 {
						ranges = append(ranges, position.NewRange(
							position.New(startLine, startCol),
							position.New(endLine, endCol),
						))
					}
				}
			}
		}

		contained := func(rs ...position.Range) []position.Position {
			var out []position.Position

			for pos := range lines.Runes() {
				if slices.ContainsFunc(rs, func(r position.Range) bool { return r.Contains(pos) }) {
					out = append(out, pos)
				}
			}

			return out
		}

		collect := func(rs ...position.Range) []position.Position {
			var out []position.Position

			for pos := range lines.Runes(rs...) {
				out = append(out, pos)
			}

			return out
		}

		for _, r := range ranges {
			assert.Equal(t, contained(r), collect(r), "range %s", r)
		}

		assert.Equal(t, contained(ranges...), collect(ranges...), "all ranges")
	})

	t.Run("CRLF endings do not count toward width or runes", func(t *testing.T) {
		t.Parallel()

		lines := line.NewLines(tokens.Tokenize("key: value\r\nother: data\r\n"))

		assert.Equal(t, len("other: data"), lines.Width())

		var sb strings.Builder

		for pos, r := range lines.Runes() {
			if r == '\n' {
				assert.Equal(t, lines.Line(pos.Line).Width(), pos.Col)
			}

			sb.WriteRune(r)
		}

		assert.Equal(t, "key: value\nother: data\n", sb.String())
	})

	t.Run("empty", func(t *testing.T) {
		t.Parallel()

		var lines line.Lines

		assert.Equal(t, 0, lines.Len())
		assert.True(t, lines.IsEmpty())
		assert.Equal(t, 0, lines.Width())

		for range lines.All() {
			t.Fatal("expected no lines")
		}

		for range lines.Runes() {
			t.Fatal("expected no runes")
		}
	})
}

func TestCollect(t *testing.T) {
	t.Parallel()

	before := line.NewLines(tokens.Tokenize("a: 1\nb: 2\n"))
	after := line.NewLines(tokens.Tokenize("a: 1\nc: 3\n"))

	t.Run("holds the lines in the order given", func(t *testing.T) {
		t.Parallel()

		got := line.Collect(after.Line(0), before.Line(1), after.Line(1))

		require.Equal(t, 3, got.Len())
		assert.Same(t, after.Line(0), got.Line(0))
		assert.Same(t, before.Line(1), got.Line(1))
		assert.Same(t, after.Line(1), got.Line(2))
	})

	t.Run("copies the slice it is given", func(t *testing.T) {
		t.Parallel()

		ls := []*line.Line{before.Line(0), before.Line(1)}
		got := line.Collect(ls...)
		ls[0] = after.Line(0)

		assert.Same(t, before.Line(0), got.Line(0))
	})

	t.Run("no lines yields the zero value", func(t *testing.T) {
		t.Parallel()

		got := line.Collect()

		assert.True(t, got.IsEmpty())
		assert.Equal(t, 0, got.Len())
	})

	t.Run("panics on a nil line", func(t *testing.T) {
		t.Parallel()

		assert.PanicsWithValue(t, "line: Collect: line 1 is nil", func() {
			line.Collect(before.Line(0), nil)
		})
	})

	t.Run("Tokens returns a repeated line's tokens once", func(t *testing.T) {
		t.Parallel()

		got := line.Collect(before.Line(0), before.Line(1), before.Line(0)).Tokens()

		want := append(
			token.Tokens{},
			append(before.Line(0).SourceTokens(), before.Line(1).SourceTokens()...)...,
		)
		assert.Equal(t, want, got)
	})
}

// TestValidateLines_EmptyLines checks which lines with no tokens
// [yamltest.ValidateLines] accepts. Only package line can build one with a
// number, so the test lives here.
func TestValidateLines_EmptyLines(t *testing.T) {
	t.Parallel()

	// Lines numbered 1 to 3. The cases put a line with no tokens where
	// the blank line 2 stands.
	ls := line.NewLines(tokens.Tokenize("a: 1\n\nb: 2\n"))
	require.Equal(t, 3, ls.Len())

	tcs := map[string]struct {
		lines []*line.Line
		err   error
	}{
		"numberless placeholder": {
			lines: []*line.Line{ls.Line(0), {}, ls.Line(2)},
		},
		"numbered empty line": {
			lines: []*line.Line{line.NewNumberedEmptyLine(3)},
			err:   yamltest.ErrEmptyLineNumbered,
		},
		"numbered empty line in sequence": {
			lines: []*line.Line{ls.Line(0), line.NewNumberedEmptyLine(2), ls.Line(2)},
			err:   yamltest.ErrEmptyLineNumbered,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := yamltest.ValidateLines(line.Collect(tc.lines...))
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				return
			}

			require.NoError(t, err)
		})
	}
}

func TestLines_Line(t *testing.T) {
	t.Parallel()

	lines := line.NewLines(tokens.Tokenize("a: 1\nb: 2\n"))

	assert.Equal(t, "a: 1", lines.Line(0).Content())
	assert.Equal(t, "b: 2", lines.Line(1).Content())
	assert.Panics(t, func() { lines.Line(2) })
	assert.Panics(t, func() { line.Lines{}.Line(0) })
}

func TestLines_SliceLines(t *testing.T) {
	t.Parallel()

	// Widths 4, 6, and 8.
	lines := line.NewLines(tokens.Tokenize("a: 1\nbb: 22\nccc: 333\n"))

	tcs := map[string]struct {
		input position.Range
		want  position.Ranges
	}{
		"single line range": {
			input: position.NewRange(position.New(1, 1), position.New(1, 3)),
			want: position.Ranges{
				position.NewRange(position.New(1, 1), position.New(1, 3)),
			},
		},
		"every line but the last ends at its width": {
			input: position.NewRange(position.New(0, 3), position.New(2, 3)),
			want: position.Ranges{
				position.NewRange(position.New(0, 3), position.New(0, 4)),
				position.NewRange(position.New(1, 0), position.New(1, 6)),
				position.NewRange(position.New(2, 0), position.New(2, 3)),
			},
		},
		"end at column 0 stops on the line before": {
			input: position.NewRange(position.New(0, 1), position.New(2, 0)),
			want: position.Ranges{
				position.NewRange(position.New(0, 1), position.New(0, 4)),
				position.NewRange(position.New(1, 0), position.New(1, 6)),
			},
		},
		"columns clamp to the line": {
			input: position.NewRange(position.New(1, -2), position.New(1, 100)),
			want: position.Ranges{
				position.NewRange(position.New(1, 0), position.New(1, 6)),
			},
		},
		"lines outside the collection are left out": {
			input: position.NewRange(position.New(-1, 0), position.New(5, 2)),
			want: position.Ranges{
				position.NewRange(position.New(0, 0), position.New(0, 4)),
				position.NewRange(position.New(1, 0), position.New(1, 6)),
				position.NewRange(position.New(2, 0), position.New(2, 8)),
			},
		},
		"start past the width covers nothing on that line": {
			input: position.NewRange(position.New(0, 9), position.New(1, 2)),
			want: position.Ranges{
				position.NewRange(position.New(1, 0), position.New(1, 2)),
			},
		},
		"empty range": {
			input: position.NewRange(position.New(1, 3), position.New(1, 3)),
			want:  nil,
		},
		"inverted columns on one line": {
			input: position.NewRange(position.New(1, 3), position.New(1, 1)),
			want:  nil,
		},
		"inverted lines": {
			input: position.NewRange(position.New(2, 0), position.New(1, 5)),
			want:  nil,
		},
		"inverted columns at the MinInt line": {
			input: position.NewRange(position.New(math.MinInt, 5), position.New(math.MinInt, 2)),
			want:  nil,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, lines.SliceLines(tc.input))
		})
	}

	t.Run("no lines", func(t *testing.T) {
		t.Parallel()

		r := position.NewRange(position.New(0, 0), position.New(0, 3))

		assert.Nil(t, line.Lines{}.SliceLines(r))
	})
}
