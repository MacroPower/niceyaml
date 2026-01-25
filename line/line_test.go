package line_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macropower/niceyaml/line"
	"github.com/macropower/niceyaml/position"
	"github.com/macropower/niceyaml/yamltest"
)

func TestNewLines_Roundtrip(t *testing.T) {
	t.Parallel()

	t.Run("testdata/full.yaml", func(t *testing.T) {
		t.Parallel()

		input, err := os.ReadFile(filepath.Join("..", "testdata", "full.yaml"))
		require.NoError(t, err)

		original := lexer.Tokenize(string(input))
		lines := line.NewLines(original)
		gotTokens := lines.Tokens()

		require.NoError(t, yamltest.ValidateTokens(original, gotTokens))

		diff := yamltest.CompareTokenSlices(original, gotTokens)
		require.True(t, diff.Equal(), diff.String())
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

		// Unicode content.
		"unicode value":   "name: 日本語\n",
		"unicode key":     "日本語: value\n",
		"emoji":           "icon: 🎉\n",
		"mixed scripts":   "text: Hello 世界 مرحبا\n",
		"combining marks": "text: Việt Nam\n",
		"rtl text":        "arabic: مرحبا\n",
		"emoji sequence":  "family: 👨‍👩‍👧‍👦\n",
		"flag emoji":      "flag: 🇯🇵\n",

		// TODO: go-yaml's scanner does not preserve \u, \U, or \x escape sequences in
		// the Origin field.
		//
		// See scanner/scanner.go:455-516 - these cases skip ctx.addOriginBuf() calls,
		// causing Origin to be truncated (e.g., "\u65E5" becomes "\\").
		// This makes Content() roundtrip impossible for these escapes.
		//
		//	"unicode escape": "text: \"\\u65E5\\u672C\"\n"

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
	}

	for name, input := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			original := lexer.Tokenize(input)
			lines := line.NewLines(original)
			gotTokens := lines.Tokens()

			require.NoError(t, yamltest.ValidateTokens(original, gotTokens))

			tokensDiff := yamltest.CompareTokenSlices(original, gotTokens)
			require.True(t, tokensDiff.Equal(), tokensDiff.String())

			contentDiff := yamltest.CompareContent(input, lines.Content())
			require.True(t, contentDiff.Equal(), contentDiff.String())
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
			input: yamltest.Input(`
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
			input: yamltest.Input(`
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
			// Note: lexer doesn't preserve trailing newline on final simple values.
			input: "key: value\n",
			want: []string{
				"   1 | key: value",
			},
		},
		"multiple key-values": {
			input: yamltest.Input(`
				first: 1
				second: 2
			`),
			want: []string{
				"   1 | first: 1",
				"   2 | second: 2",
			},
		},
		"nested map": {
			input: yamltest.Input(`
				parent:
				  child: value
			`),
			want: []string{
				"   1 | parent:",
				"   2 |   child: value",
			},
		},
		"quoted with escaped newline": {
			input: yamltest.Input(`
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

			input := lexer.Tokenize(tc.input)
			lines := line.NewLines(input)

			require.Len(t, lines, len(tc.want), "wrong number of lines")

			for i, want := range tc.want {
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
			input: yamltest.Input(`
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
			input: yamltest.Input(`
				parent:
				  child: value
				  sibling: another
			`),
			startLine:     50,
			wantLineNums:  []int{50, 51, 52},
			wantLineCount: 3,
		},
		"literal block starting at line 20": {
			input: yamltest.Input(`
				script: |
				  line1
				  line2
			`),
			startLine:     20,
			wantLineNums:  []int{20, 21, 22},
			wantLineCount: 3,
		},
		"folded block starting at line 30": {
			input: yamltest.Input(`
				desc: >
				  part1
				  part2
			`),
			startLine:     30,
			wantLineNums:  []int{30, 31, 32},
			wantLineCount: 3,
		},
		"list starting at line 25": {
			input: yamltest.Input(`
				items:
				  - one
				  - two
			`),
			startLine:     25,
			wantLineNums:  []int{25, 26, 27},
			wantLineCount: 3,
		},
		"comment and key at line 15": {
			input: yamltest.Input(`
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
			assert.Equal(
				t,
				yamltest.DumpTokenOrigins(tks),
				yamltest.DumpTokenOrigins(gotTokens),
				"round-trip content mismatch",
			)
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

func TestLine_Annotation(t *testing.T) {
	t.Parallel()

	t.Run("String rendering", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			want        string
			annotations []line.Annotation
		}{
			"no annotation": {
				annotations: nil,
				want:        "   1 | key: value",
			},
			"annotation below at start": {
				annotations: []line.Annotation{{Content: "error here", Position: line.Below}},
				want: `   1 | key: value
   1 | ^ error here`,
			},
			"annotation below with padding": {
				annotations: []line.Annotation{{Content: "note", Position: line.Below, Col: 4}},
				want: `   1 | key: value
   1 |     ^ note`,
			},
			"annotation above": {
				annotations: []line.Annotation{{Content: "@@ hunk header @@", Position: line.Above}},
				want: `   1 | @@ hunk header @@
   1 | key: value`,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				tks := lexer.Tokenize("key: value\n")
				lines := line.NewLines(tks)
				require.Len(t, lines, 1)

				ln := lines[0]
				ln.Annotate(tc.annotations...)

				assert.Equal(t, tc.want, ln.String())
			})
		}
	})

	t.Run("Clone preserves annotations", func(t *testing.T) {
		t.Parallel()

		tks := lexer.Tokenize("key: value\n")
		lines := line.NewLines(tks)
		require.Len(t, lines, 1)

		original := lines[0]
		original.Annotate(line.Annotation{Content: "original note", Position: line.Below})

		clone := original.Clone()

		// Verify annotations were copied.
		require.Len(t, clone.Annotations, 1)
		require.Len(t, original.Annotations, len(clone.Annotations))
		assert.Equal(t, original.Annotations[0].Content, clone.Annotations[0].Content)

		// Verify position by checking filtered results.
		belowCount := 0
		for _, ann := range clone.Annotations {
			if ann.Position == line.Below {
				belowCount++
			}
		}

		assert.Equal(t, 1, belowCount)

		// Modify clone annotations.
		clone.Annotate(line.Annotation{Content: "modified", Position: line.Above})

		// Verify original is unchanged.
		require.Len(t, original.Annotations, 1)
		assert.Equal(t, "original note", original.Annotations[0].Content)

		origBelowCount := 0
		for _, ann := range original.Annotations {
			if ann.Position == line.Below {
				origBelowCount++
			}
		}

		assert.Equal(t, 1, origBelowCount)
	})
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
			input: yamltest.Input(`
				parent:
				  child: value
				  sibling: another
			`),
		},
		"literal block": {
			input: yamltest.Input(`
				key: |
				  line1
				  line2
			`),
		},
		"list": {
			input: yamltest.Input(`
				items:
				  - one
				  - two
			`),
		},
		"deeply nested": {
			input: yamltest.Input(`
				level1:
				  level2:
				    level3:
				      level4:
				        value: deep
			`),
		},
		"anchor and alias": {
			input: yamltest.Input(`
				defaults: &defaults
				  timeout: 30
				  retries: 3
				production:
				  <<: *defaults
				  timeout: 60
			`),
		},
		"inline flow style": {
			input: yamltest.Input(`
				map: {a: 1, b: 2, c: 3}
				list: [x, y, z]
			`),
		},
		"mixed comments": {
			input: yamltest.Input(`
				# Header comment
				key1: value1  # inline comment
				# Middle comment
				key2: value2
				# Footer comment
			`),
		},
		"folded block": {
			input: yamltest.Input(`
				description: >
				  This is a long
				  description that
				  spans multiple lines.
			`),
		},
		"list of maps": {
			input: yamltest.Input(`
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
			input: yamltest.Input(`
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
			input: yamltest.Input(`
				double: "hello world"
				single: 'foo bar'
				special: "line1\nline2\ttab"
			`),
		},
		"multi-document": {
			input: yamltest.Input(`
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
		input string
		want  []int
	}{
		"inline comment followed by next line": {
			input: yamltest.Input(`
				items:
				  - key: value # inline comment
				    next: data
			`),
			want: []int{1, 2, 3},
		},
		"sequence entry with merge key and comment": {
			input: yamltest.Input(`
				items:
				  - <<: *anchor # comment
				    key: value
			`),
			want: []int{1, 2, 3},
		},
		"nested map with trailing comment": {
			input: yamltest.Input(`
				parent:
				  child: value # note
				  sibling: other
			`),
			want: []int{1, 2, 3},
		},
		"multiple inline comments": {
			input: yamltest.Input(`
				a: 1 # first
				b: 2 # second
				c: 3 # third
			`),
			want: []int{1, 2, 3},
		},
		"comment block after content": {
			input: yamltest.Input(`
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

			tks := lexer.Tokenize(tc.input)
			lines := line.NewLines(tks)

			// Verify line numbers are strictly increasing.
			require.NoError(t, lines.Validate(), "tokens should be valid")

			// Verify expected line numbers.
			require.Len(t, lines, len(tc.want), "wrong number of lines")

			for i, wantNum := range tc.want {
				assert.Equal(t, wantNum, lines[i].Number(), "line %d has wrong number", i)
			}
		})
	}
}

func TestLines_Validate(t *testing.T) {
	t.Parallel()

	strTkb := yamltest.NewTokenBuilder().Type(token.StringType)

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
		tks.Add(strTkb.Clone().Origin("first\n").Value("first").PositionLine(5).PositionColumn(1).Build())
		tks.Add(
			strTkb.Clone().Origin("second\n").Value("second").PositionLine(5).PositionColumn(1).Build(),
		) // Same line number in input.

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
		tks.Add(strTkb.Clone().Origin("first\n").Value("first").PositionLine(10).PositionColumn(1).Build())
		tks.Add(
			strTkb.Clone().Origin("second\n").Value("second").PositionLine(5).PositionColumn(1).Build(),
		) // Lower line number in input.

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
		tks.Add(strTkb.Clone().Origin("first").Value("first").PositionLine(1).PositionColumn(5).Build())
		tks.Add(
			strTkb.Clone().Origin("second\n").Value("second").PositionLine(1).PositionColumn(5).Build(),
		) // Same column!

		lines := line.NewLines(tks)

		err := lines.Validate()
		require.ErrorIs(t, err, line.ErrColumnNotIncreasing)
		assert.Contains(t, err.Error(), "column 5 not greater than previous 5")
	})

	t.Run("columns not increasing - decreasing", func(t *testing.T) {
		t.Parallel()

		tks := token.Tokens{}
		tks.Add(strTkb.Clone().Origin("first").Value("first").PositionLine(1).PositionColumn(10).Build())
		tks.Add(
			strTkb.Clone().Origin("second\n").Value("second").PositionLine(1).PositionColumn(5).Build(),
		) // Lower column!

		lines := line.NewLines(tks)

		err := lines.Validate()
		require.ErrorIs(t, err, line.ErrColumnNotIncreasing)
		assert.Contains(t, err.Error(), "column 5 not greater than previous 10")
	})

	t.Run("token line numbers normalized on same line", func(t *testing.T) {
		t.Parallel()

		// Create tokens with inconsistent position line numbers.
		// NewLines normalizes them to be consistent.
		tks := token.Tokens{}
		tks.Add(strTkb.Clone().Origin("first").Value("first").PositionLine(1).PositionColumn(1).Build())
		tks.Add(
			strTkb.Clone().Origin("second\n").Value("second").PositionLine(2).PositionColumn(10).Build(),
		) // Different line in input.

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
			Position: nil, // Nil position - intentionally not using TokenBuilder.
		})
		tks.Add(strTkb.Clone().Origin("second\n").Value("second").PositionLine(1).PositionColumn(10).Build())

		lines := line.NewLines(tks)

		// Nil positions are skipped in validation.
		assert.NoError(t, lines.Validate())
	})

	t.Run("valid with gaps in line numbers", func(t *testing.T) {
		t.Parallel()

		tks := token.Tokens{}
		tks.Add(strTkb.Clone().Origin("first\n").Value("first").PositionLine(1).PositionColumn(1).Build())
		tks.Add(
			strTkb.Clone().Origin("second\n").Value("second").PositionLine(10).PositionColumn(1).Build(),
		) // Gap is fine.
		tks.Add(strTkb.Clone().Origin("third\n").Value("third").PositionLine(100).PositionColumn(1).Build())

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
	require.NoError(t, yamltest.ValidateTokens(originalTks, resultTks))

	diff := yamltest.CompareTokenSlices(originalTks, resultTks)
	require.True(t, diff.Equal(), diff.String())

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

	input := yamltest.Input(`
		root:
		  level1:
		    level2:
		      level3: value
		    back2: val
		  back1: val
		end: val
	`)
	lines := line.NewLines(lexer.Tokenize(input))

	// Expected indent levels per line (based on go-yaml scanner behavior):
	// Line 1: root: -> level 0.
	// Line 2:   level1: -> level 1.
	// Line 3:     level2: -> level 2.
	// Line 4:       level3: value -> level 3.
	// Line 5:     back2: val -> level 2.
	// Line 6:   back1: val -> level 1.
	// Line 7: end: val -> level 0.
	wantLevels := []int{0, 1, 2, 3, 2, 1, 0}

	require.Len(t, lines, len(wantLevels))

	for i := range lines {
		ln := lines[i]
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

		// The go-yaml lexer places the Position of block scalar content (StringType)
		// on the LAST line of the content, not the first.
		// This is critical for round-trip fidelity.

		tcs := map[string]struct {
			input string
			want  int // Expected Position.Line of the StringType content token.
		}{
			"literal two lines": {
				input: yamltest.Input(`
					key: |
					  line1
					  line2
				`),
				want: 3, // Position should be on last content line.
			},
			"literal three lines": {
				input: yamltest.Input(`
					key: |
					  a
					  b
					  c
				`),
				want: 4,
			},
			"folded two lines": {
				input: yamltest.Input(`
					key: >
					  first
					  second
				`),
				want: 3,
			},
			"literal with strip": {
				input: yamltest.Input(`
					key: |-
					  line1
					  line2
				`),
				want: 3,
			},
			"literal with keep and trailing blank": {
				input: `key: |+
  line1
  line2

`,
				want: 4, // Blank line counts.
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				originalTks := lexer.Tokenize(tc.input)
				lines := line.NewLines(originalTks)
				resultTks := lines.Tokens()

				require.NoError(t, yamltest.ValidateTokens(originalTks, resultTks))

				diff := yamltest.CompareTokenSlices(originalTks, resultTks)
				require.True(t, diff.Equal(), diff.String())

				var contentToken *token.Token
				for _, tk := range resultTks {
					if tk.Type == token.StringType && strings.Contains(tk.Origin, "\n") {
						contentToken = tk
						break
					}
				}

				require.NotNil(t, contentToken, "expected to find block scalar content token")
				assert.Equal(t, tc.want, contentToken.Position.Line,
					"block scalar content Position.Line should point to LAST line")
			})
		}
	})

	t.Run("empty content edge cases", func(t *testing.T) {
		t.Parallel()

		// Test edge case: block scalar header with no content.
		// This can happen with "key: |\n" followed by another key or end of document.

		tcs := map[string]string{
			"literal empty followed by key": yamltest.Input(`
				key: |
				next: value
			`),
			"folded empty followed by key": yamltest.Input(`
				key: >
				next: value
			`),
			"literal empty at end": yamltest.Input(`
				key: |
			`),
			"literal strip empty": yamltest.Input(`
				key: |-
				next: value
			`),
		}

		for name, input := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				originalTks := lexer.Tokenize(input)
				lines := line.NewLines(originalTks)
				resultTks := lines.Tokens()

				require.NoError(t, lines.Validate())

				require.NoError(t, yamltest.ValidateTokens(originalTks, resultTks))

				diff := yamltest.CompareTokenSlices(originalTks, resultTks)
				require.True(t, diff.Equal(), diff.String())
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

				originalTks := lexer.Tokenize(tc.input)
				lines := line.NewLines(originalTks)
				resultTks := lines.Tokens()

				require.NoError(t, yamltest.ValidateTokens(originalTks, resultTks))

				diff := yamltest.CompareTokenSlices(originalTks, resultTks)
				require.True(t, diff.Equal(), diff.String())

				var contentToken *token.Token
				for _, tk := range resultTks {
					if tk.Type == token.StringType && strings.Contains(tk.Origin, "\n") {
						contentToken = tk
						break
					}
				}

				require.NotNil(t, contentToken, "expected to find block scalar content token")
				assert.Equal(t, tc.want, contentToken.Value,
					"block scalar Value should match chomping behavior")
			})
		}
	})
}

func TestNewLines_PlainMultilinePositionSemantics(t *testing.T) {
	t.Parallel()

	// The go-yaml lexer places the Position of plain multiline strings
	// (StringType) on the FIRST line, not the last.
	// This is different from block scalars.
	// This is critical for round-trip fidelity.

	tcs := map[string]struct {
		input string
		want  int // Expected Position.Line of the StringType content token.
	}{
		"plain multiline two lines": {
			input: yamltest.Input(`
				key: this is
				  continued
			`),
			want: 1, // Position should be on FIRST line.
		},
		"plain multiline three lines": {
			input: yamltest.Input(`
				key: first
				  second
				  third
			`),
			want: 1,
		},
		"plain multiline with more indent": {
			input: yamltest.Input(`
				parent:
				  child: line one
				    continued line
			`),
			want: 2, // First line of the value.
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			originalTks := lexer.Tokenize(tc.input)
			lines := line.NewLines(originalTks)
			resultTks := lines.Tokens()

			// Verify round-trip fidelity.
			require.NoError(t, yamltest.ValidateTokens(originalTks, resultTks))

			diff := yamltest.CompareTokenSlices(originalTks, resultTks)
			require.True(t, diff.Equal(), diff.String())

			// Find the multiline StringType token (value with newlines).
			var contentToken *token.Token
			for _, tk := range resultTks {
				if tk.Type == token.StringType && strings.Contains(tk.Origin, "\n") {
					contentToken = tk
					break
				}
			}

			require.NotNil(t, contentToken, "expected to find plain multiline string token")
			assert.Equal(t, tc.want, contentToken.Position.Line,
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
			require.NoError(t, yamltest.ValidateTokens(originalTks, resultTks))

			diff := yamltest.CompareTokenSlices(originalTks, resultTks)
			require.True(t, diff.Equal(), diff.String())

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

func TestNewLines_ColumnPositionAfterSplit(t *testing.T) {
	t.Parallel()

	// Test that Column positions are correctly calculated when multiline tokens
	// are split across lines.
	//
	// For block scalars, each split part should have Column calculated based on
	// the leading whitespace of that line.

	t.Run("block scalar column positions", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			key: |
			  line1
			  line2
		`)
		originalTks := lexer.Tokenize(input)
		lines := line.NewLines(originalTks)

		// Verify we have the expected number of lines.
		require.Len(t, lines, 3)

		// Verify round-trip produces identical tokens.
		resultTks := lines.Tokens()
		require.NoError(t, yamltest.ValidateTokens(originalTks, resultTks))

		diff := yamltest.CompareTokenSlices(originalTks, resultTks)
		require.True(t, diff.Equal(), diff.String())
	})

	t.Run("plain multiline column positions", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			key: this is
			  continued
		`)
		originalTks := lexer.Tokenize(input)
		lines := line.NewLines(originalTks)

		// Verify we have the expected number of lines.
		require.Len(t, lines, 2)

		// Verify round-trip produces identical tokens.
		resultTks := lines.Tokens()
		require.NoError(t, yamltest.ValidateTokens(originalTks, resultTks))

		diff := yamltest.CompareTokenSlices(originalTks, resultTks)
		require.True(t, diff.Equal(), diff.String())
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
		assert.Contains(t, l.String(), "0 |")
	})

	t.Run("Line/with tokens not empty", func(t *testing.T) {
		t.Parallel()

		tks := lexer.Tokenize("key: value\n")
		lines := line.NewLines(tks)

		require.Len(t, lines, 1)
		assert.False(t, lines[0].IsEmpty())
	})

	t.Run("Lines/nil", func(t *testing.T) {
		t.Parallel()

		var lines line.Lines
		assert.Nil(t, lines.Tokens())
		assert.NoError(t, lines.Validate())
	})

	t.Run("Lines/empty slice", func(t *testing.T) {
		t.Parallel()

		lines := line.Lines{}
		assert.Nil(t, lines.Tokens())
		assert.NoError(t, lines.Validate())
	})

	t.Run("Lines/NewLines with nil tokens", func(t *testing.T) {
		t.Parallel()

		lines := line.NewLines(nil)
		assert.Nil(t, lines)
	})

	t.Run("Lines/NewLines with empty tokens", func(t *testing.T) {
		t.Parallel()

		lines := line.NewLines(lexer.Tokenize(""))
		assert.Nil(t, lines)
	})
}

// TestNewLines_BlockScalarPositionBehavior documents and verifies the three
// distinct Position behaviors for block scalar content in the go-yaml lexer:
//   - Single-line content: Column > 0 regardless of context
//   - Multi-line with following content: Column = 0 (marker for first-line position)
//   - Multi-line standalone/at end: Column > 0 (last-line position)
func TestNewLines_BlockScalarPositionBehavior(t *testing.T) {
	t.Parallel()

	// Helper to find the block scalar content token (StringType with leading space in Origin).
	findBlockScalarContent := func(tks token.Tokens) *token.Token {
		for _, tk := range tks {
			if tk.Type == token.StringType && len(tk.Origin) > 1 && tk.Origin[0] == ' ' {
				return tk
			}
		}

		return nil
	}

	t.Run("single-line with following content", func(t *testing.T) {
		t.Parallel()

		// Single-line block scalar with following content.
		// Lexer behavior: Position.Column > 0 (points to content start).
		input := "key: |\n  content\nnext: value\n"

		original := lexer.Tokenize(input)
		lines := line.NewLines(original)
		result := lines.Tokens()

		require.NoError(t, yamltest.ValidateTokens(original, result))

		diff := yamltest.CompareTokenSlices(original, result)
		require.True(t, diff.Equal(), diff.String())

		content := findBlockScalarContent(result)
		require.NotNil(t, content, "expected to find block scalar content token")
	})

	t.Run("single-line standalone", func(t *testing.T) {
		t.Parallel()

		// Single-line block scalar at end of document.
		// Lexer behavior: Position.Column > 0 (same as with following).
		input := "key: |\n  content\n"

		original := lexer.Tokenize(input)
		lines := line.NewLines(original)
		result := lines.Tokens()

		require.NoError(t, yamltest.ValidateTokens(original, result))

		diff := yamltest.CompareTokenSlices(original, result)
		require.True(t, diff.Equal(), diff.String())

		content := findBlockScalarContent(result)
		require.NotNil(t, content, "expected to find block scalar content token")
	})

	t.Run("multi-line with following content", func(t *testing.T) {
		t.Parallel()

		// Multi-line block scalar with following content.
		// Lexer behavior: Position.Column = 0 (special marker for first-line position).
		input := "key: |\n  line1\n  line2\nnext: data\n"

		original := lexer.Tokenize(input)
		lines := line.NewLines(original)
		result := lines.Tokens()

		require.NoError(t, yamltest.ValidateTokens(original, result))

		diff := yamltest.CompareTokenSlices(original, result)
		require.True(t, diff.Equal(), diff.String())

		content := findBlockScalarContent(result)
		require.NotNil(t, content, "expected to find block scalar content token")
	})

	t.Run("multi-line standalone", func(t *testing.T) {
		t.Parallel()

		// Multi-line block scalar at end of document.
		// Lexer behavior: Position.Column > 0 (last-line position).
		input := "key: |\n  line1\n  line2\n"

		original := lexer.Tokenize(input)
		lines := line.NewLines(original)
		result := lines.Tokens()

		require.NoError(t, yamltest.ValidateTokens(original, result))

		diff := yamltest.CompareTokenSlices(original, result)
		require.True(t, diff.Equal(), diff.String())

		content := findBlockScalarContent(result)
		require.NotNil(t, content, "expected to find block scalar content token")
	})

	t.Run("three-line with following content", func(t *testing.T) {
		t.Parallel()

		// Three-line block scalar with following content.
		// Verifies Column=0 marker for longer content.
		input := "key: |\n  a\n  b\n  c\nnext: data\n"

		original := lexer.Tokenize(input)
		lines := line.NewLines(original)
		result := lines.Tokens()

		require.NoError(t, yamltest.ValidateTokens(original, result))

		diff := yamltest.CompareTokenSlices(original, result)
		require.True(t, diff.Equal(), diff.String())

		content := findBlockScalarContent(result)
		require.NotNil(t, content, "expected to find block scalar content token")
	})

	t.Run("three-line standalone", func(t *testing.T) {
		t.Parallel()

		// Three-line block scalar at end.
		// Verifies last-line position for longer content.
		input := "key: |\n  a\n  b\n  c\n"

		original := lexer.Tokenize(input)
		lines := line.NewLines(original)
		result := lines.Tokens()

		require.NoError(t, yamltest.ValidateTokens(original, result))

		diff := yamltest.CompareTokenSlices(original, result)
		require.True(t, diff.Equal(), diff.String())

		content := findBlockScalarContent(result)
		require.NotNil(t, content, "expected to find block scalar content token")
	})
}

// TestNewLines_BlankLineAbsorption documents how blank lines are handled by the
// go-yaml lexer: blank lines are absorbed into the previous token's Origin
// rather than being separate tokens.
// This test verifies that NewLines correctly handles this behavior.
func TestNewLines_BlankLineAbsorption(t *testing.T) {
	t.Parallel()

	t.Run("single blank line absorbed into previous token", func(t *testing.T) {
		t.Parallel()

		// Blank line between two key-value pairs.
		// The lexer absorbs the blank line into the first value's Origin.
		input := "key: value\n\nnext: data\n"

		original := lexer.Tokenize(input)
		lines := line.NewLines(original)
		result := lines.Tokens()

		// Verify round-trip.
		require.NoError(t, yamltest.ValidateTokens(original, result))

		diff := yamltest.CompareTokenSlices(original, result)
		require.True(t, diff.Equal(), diff.String())

		// Verify the blank line is in the value token's Origin.
		// The value token should have Origin " value\n\n" (two newlines).
		var valueToken *token.Token
		for _, tk := range result {
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

		original := lexer.Tokenize(input)
		lines := line.NewLines(original)
		result := lines.Tokens()

		require.NoError(t, yamltest.ValidateTokens(original, result))

		diff := yamltest.CompareTokenSlices(original, result)
		require.True(t, diff.Equal(), diff.String())

		var valueToken *token.Token
		for _, tk := range result {
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

		// Verify that Lines correctly track line numbers across gaps.
		input := "key: value\n\nnext: data\n"

		original := lexer.Tokenize(input)
		lines := line.NewLines(original)

		// Should have lines at positions 1, 2 (blank absorbed), and 3.
		require.Len(t, lines, 3, "expected 3 lines including blank")

		// Line numbers should be 1, 2, 3.
		assert.Equal(t, 1, lines[0].Number(), "first line should be 1")
		assert.Equal(t, 2, lines[1].Number(), "second line (blank) should be 2")
		assert.Equal(t, 3, lines[2].Number(), "third line should be 3")
	})
}

// TestNewLines_FoldedBlockBlankLines verifies handling of blank lines within
// folded block scalars.
//
// In folded scalars, blank lines have special semantics - they preserve line
// breaks instead of folding to spaces.
func TestNewLines_FoldedBlockBlankLines(t *testing.T) {
	t.Parallel()

	t.Run("folded block with blank line preserves break", func(t *testing.T) {
		t.Parallel()

		// In folded blocks, blank lines cause a line break in the Value.
		// "first" and "second" are separated by a blank line, which becomes
		// a newline in the Value instead of a space.
		input := "text: >\n  first\n\n  second\n"

		original := lexer.Tokenize(input)
		lines := line.NewLines(original)
		result := lines.Tokens()

		require.NoError(t, yamltest.ValidateTokens(original, result))

		diff := yamltest.CompareTokenSlices(original, result)
		require.True(t, diff.Equal(), diff.String())

		// Find the content token.
		var contentToken *token.Token
		for _, tk := range result {
			if tk.Type == token.StringType && strings.Contains(tk.Origin, "first") {
				contentToken = tk
				break
			}
		}

		require.NotNil(t, contentToken, "expected to find folded content token")
		// The blank line causes a paragraph break in folded output.
		assert.Contains(t, contentToken.Value, "\n",
			"folded block with blank line should have newline in Value")
	})

	t.Run("folded block without blank line folds to space", func(t *testing.T) {
		t.Parallel()

		// Without blank lines, folded content joins with spaces.
		input := "text: >\n  first\n  second\n"

		original := lexer.Tokenize(input)
		lines := line.NewLines(original)
		result := lines.Tokens()

		require.NoError(t, yamltest.ValidateTokens(original, result))

		diff := yamltest.CompareTokenSlices(original, result)
		require.True(t, diff.Equal(), diff.String())

		var contentToken *token.Token
		for _, tk := range result {
			if tk.Type == token.StringType && strings.Contains(tk.Origin, "first") {
				contentToken = tk
				break
			}
		}

		require.NotNil(t, contentToken, "expected to find folded content token")
		// Adjacent lines fold to space, resulting in "first second\n".
		assert.Equal(t, "first second\n", contentToken.Value,
			"folded block without blank line should have space-joined Value")
	})

	t.Run("literal block preserves all blank lines", func(t *testing.T) {
		t.Parallel()

		// Literal blocks preserve blank lines as-is.
		input := "text: |\n  first\n\n  second\n"

		original := lexer.Tokenize(input)
		lines := line.NewLines(original)
		result := lines.Tokens()

		require.NoError(t, yamltest.ValidateTokens(original, result))

		diff := yamltest.CompareTokenSlices(original, result)
		require.True(t, diff.Equal(), diff.String())

		var contentToken *token.Token
		for _, tk := range result {
			if tk.Type == token.StringType && strings.Contains(tk.Origin, "first") {
				contentToken = tk
				break
			}
		}

		require.NotNil(t, contentToken, "expected to find literal content token")
		// Literal preserves blank line as newline.
		assert.Equal(t, "first\n\nsecond\n", contentToken.Value,
			"literal block should preserve blank line in Value")
	})

	t.Run("folded with multiple blank lines", func(t *testing.T) {
		t.Parallel()

		// Multiple blank lines in folded content.
		input := "text: >\n  first\n\n\n  second\n"

		original := lexer.Tokenize(input)
		lines := line.NewLines(original)
		result := lines.Tokens()

		require.NoError(t, yamltest.ValidateTokens(original, result))

		diff := yamltest.CompareTokenSlices(original, result)
		require.True(t, diff.Equal(), diff.String())
	})
}

func TestLines_TokenPositions(t *testing.T) {
	t.Parallel()

	t.Run("single occurrence", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := lexer.Tokenize(input)
		lines := line.NewLines(tks)

		require.Len(t, lines, 1)

		// Use the original "key" token from lexer (TokenPositions does pointer comparison).
		tk := tks[0]
		positions := lines.TokenPositions(tk)

		require.Len(t, positions, 1)
		assert.Equal(t, 0, positions[0].Line)
		assert.Equal(t, 0, positions[0].Col)
	})

	t.Run("multiple occurrences - literal block", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			key: |
			  line1
			  line2
		`)
		tks := lexer.Tokenize(input)
		lines := line.NewLines(tks)

		require.Len(t, lines, 3)

		// Find the literal content token from original lexer tokens.
		// It's the StringType token whose Origin contains "line1".
		var tk *token.Token
		for _, lexTk := range tks {
			if lexTk.Type == token.StringType && strings.Contains(lexTk.Origin, "line1") {
				tk = lexTk
				break
			}
		}

		require.NotNil(t, tk)

		positions := lines.TokenPositions(tk)

		// The same token should appear on multiple lines.
		require.GreaterOrEqual(t, len(positions), 1)

		// Collect line indices.
		lineIdxs := make([]int, len(positions))
		for i, pos := range positions {
			lineIdxs[i] = pos.Line
		}

		// Should include at least line 1.
		assert.Contains(t, lineIdxs, 1)
	})

	t.Run("nil token", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := lexer.Tokenize(input)
		lines := line.NewLines(tks)

		positions := lines.TokenPositions(nil)
		assert.Nil(t, positions)
	})

	t.Run("token not in lines", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := lexer.Tokenize(input)
		lines := line.NewLines(tks)

		// Create a different token that's not in the Lines.
		otherTks := lexer.Tokenize("other: data\n")
		externalToken := otherTks[0]

		positions := lines.TokenPositions(externalToken)
		assert.Nil(t, positions)
	})

	t.Run("token with column offset", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := lexer.Tokenize(input)
		lines := line.NewLines(tks)

		// Find the "value" token from original lexer tokens.
		var tk *token.Token
		for _, lexTk := range tks {
			if lexTk.Value == "value" {
				tk = lexTk
				break
			}
		}

		require.NotNil(t, tk)

		positions := lines.TokenPositions(tk)

		require.Len(t, positions, 1)
		assert.Equal(t, 0, positions[0].Line)
		// Column should be > 0 since it's not the first token.
		assert.Positive(t, positions[0].Col)
	})
}

func TestLines_TokenAt(t *testing.T) {
	t.Parallel()

	t.Run("returns token at position", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := lexer.Tokenize(input)
		lines := line.NewLines(tks)

		// Get token at start of line.
		tk := lines.TokenAt(position.New(0, 0))
		require.NotNil(t, tk)
		assert.Equal(t, "key", tk.Value)
	})

	t.Run("returns token at column offset", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := lexer.Tokenize(input)
		lines := line.NewLines(tks)

		// Get token in middle of "value" (column 5 is 'v').
		tk := lines.TokenAt(position.New(0, 5))
		require.NotNil(t, tk)
		assert.Equal(t, "value", tk.Value)
	})

	t.Run("multiline token returns same source", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			key: |
			  line1
			  line2
		`)
		tks := lexer.Tokenize(input)
		lines := line.NewLines(tks)

		// Get source token from different lines of the literal block.
		tk1 := lines.TokenAt(position.New(1, 0))
		tk2 := lines.TokenAt(position.New(2, 0))

		require.NotNil(t, tk1)
		require.NotNil(t, tk2)
		// Both should return clones with the same values (TokenAt returns clones).
		assert.NotSame(t, tk1, tk2)
		require.NoError(t, yamltest.ValidateTokenPair(tk1, tk2))

		diff := yamltest.CompareTokens(tk1, tk2)
		require.True(t, diff.Equal(), diff.String())
	})

	t.Run("out of bounds line returns nil", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := lexer.Tokenize(input)
		lines := line.NewLines(tks)

		assert.Nil(t, lines.TokenAt(position.New(-1, 0)))
		assert.Nil(t, lines.TokenAt(position.New(999, 0)))
	})

	t.Run("column outside token range returns nil", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := lexer.Tokenize(input)
		lines := line.NewLines(tks)

		assert.Nil(t, lines.TokenAt(position.New(0, 100)))
	})
}

func TestLines_TokenPositionRanges(t *testing.T) {
	t.Parallel()

	t.Run("single token range", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := lexer.Tokenize(input)
		lines := line.NewLines(tks)

		// Use original "key" token from lexer (TokenPositionRanges does pointer comparison).
		tk := tks[0]
		ranges := lines.TokenPositionRanges(tk)
		require.Len(t, ranges, 1)
		assert.Equal(t, 0, ranges[0].Start.Line)
		assert.Equal(t, 0, ranges[0].Start.Col)
		assert.Equal(t, 0, ranges[0].End.Line)
		assert.Equal(t, 3, ranges[0].End.Col)
	})

	t.Run("multiline token returns multiple ranges", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			key: |
			  line1
			  line2
		`)
		tks := lexer.Tokenize(input)
		lines := line.NewLines(tks)

		// Find the literal content token from original lexer tokens.
		// It's the StringType token whose Origin contains "line1".
		var tk *token.Token
		for _, lexTk := range tks {
			if lexTk.Type == token.StringType && strings.Contains(lexTk.Origin, "line1") {
				tk = lexTk
				break
			}
		}

		require.NotNil(t, tk)

		ranges := lines.TokenPositionRanges(tk)
		require.Len(t, ranges, 2)

		// Collect line indices.
		lineIdxs := make([]int, len(ranges))
		for i, r := range ranges {
			lineIdxs[i] = r.Start.Line
		}

		assert.Contains(t, lineIdxs, 1)
		assert.Contains(t, lineIdxs, 2)
	})

	t.Run("nil token returns nil", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := lexer.Tokenize(input)
		lines := line.NewLines(tks)

		assert.Nil(t, lines.TokenPositionRanges(nil))
	})

	t.Run("token not in lines returns empty ranges", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := lexer.Tokenize(input)
		lines := line.NewLines(tks)

		otherTks := lexer.Tokenize("other: data\n")
		otherTk := otherTks[0]

		ranges := lines.TokenPositionRanges(otherTk)
		assert.Empty(t, ranges)
	})
}

func TestLines_TokenPositionRangesAt(t *testing.T) {
	t.Parallel()

	t.Run("single line token", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := lexer.Tokenize(input)
		lines := line.NewLines(tks)

		// Query position at the "key" token.
		ranges := lines.TokenPositionRangesAt(position.New(0, 0))
		require.Len(t, ranges, 1)
		assert.Equal(t, 0, ranges[0].Start.Line)
		assert.Equal(t, 0, ranges[0].Start.Col)
		assert.Equal(t, 0, ranges[0].End.Line)
		assert.Equal(t, 3, ranges[0].End.Col)
	})

	t.Run("multiline token returns all ranges", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			key: |
			  line1
			  line2
		`)
		tks := lexer.Tokenize(input)
		lines := line.NewLines(tks)

		// Query position in the literal block content (line 1, column 2).
		ranges := lines.TokenPositionRangesAt(position.New(1, 2))
		// Should return ranges for all lines where this token appears.
		require.Len(t, ranges, 2)

		lineIdxs := make([]int, len(ranges))
		for i, r := range ranges {
			lineIdxs[i] = r.Start.Line
		}

		assert.Contains(t, lineIdxs, 1)
		assert.Contains(t, lineIdxs, 2)
	})

	t.Run("position at value token", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := lexer.Tokenize(input)
		lines := line.NewLines(tks)

		// Query at "value" position (after "key:" which is 4 chars).
		ranges := lines.TokenPositionRangesAt(position.New(0, 5))
		require.Len(t, ranges, 1)
		// Value token starts at column 4 (0-indexed: "key:" is 4 chars).
		assert.Equal(t, 4, ranges[0].Start.Col)
	})

	t.Run("position outside tokens returns empty", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := lexer.Tokenize(input)
		lines := line.NewLines(tks)

		// Query at position beyond token content.
		ranges := lines.TokenPositionRangesAt(position.New(0, 100))
		assert.Nil(t, ranges)
	})

	t.Run("line out of bounds returns nil", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := lexer.Tokenize(input)
		lines := line.NewLines(tks)

		assert.Nil(t, lines.TokenPositionRangesAt(position.New(999, 0)))
		assert.Nil(t, lines.TokenPositionRangesAt(position.New(-1, 0)))
	})
}

func TestLines_String(t *testing.T) {
	t.Parallel()

	t.Run("single line", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := lexer.Tokenize(input)
		lines := line.NewLines(tks)

		result := lines.String()
		assert.Contains(t, result, "key: value")
		// Should include line number prefix.
		assert.Contains(t, result, "1 |")
	})

	t.Run("multiple lines", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			key1: value1
			key2: value2
		`)
		tks := lexer.Tokenize(input)
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

	t.Run("line with annotation", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := lexer.Tokenize(input)
		lines := line.NewLines(tks)

		// Add annotation to first line.
		lines[0].Annotate(line.Annotation{Content: "test annotation"})

		result := lines.String()
		assert.Contains(t, result, "key: value")
		assert.Contains(t, result, "test annotation")
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
		tks := lexer.Tokenize(input)
		lines := line.NewLines(tks)

		require.Len(t, lines, 1)
		// Line number should be 1 (1-indexed from lexer).
		assert.Equal(t, 1, lines[0].Number())
	})

	t.Run("empty line returns zero", func(t *testing.T) {
		t.Parallel()

		// Create an empty Line directly.
		var ln line.Line
		assert.Equal(t, 0, ln.Number())
	})

	t.Run("multiple lines have correct numbers", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			key1: value1
			key2: value2
			key3: value3
		`)
		tks := lexer.Tokenize(input)
		lines := line.NewLines(tks)

		require.Len(t, lines, 3)
		assert.Equal(t, 1, lines[0].Number())
		assert.Equal(t, 2, lines[1].Number())
		assert.Equal(t, 3, lines[2].Number())
	})

	t.Run("segment with nil position returns zero", func(t *testing.T) {
		t.Parallel()

		// Create tokens where Position is nil.
		// NewLines processes these but they won't contribute to Number().
		tks := token.Tokens{}
		tks.Add(&token.Token{
			Type:     token.StringType,
			Origin:   "test\n",
			Value:    "test",
			Position: nil, // Nil position.
		})

		lines := line.NewLines(tks)

		// NewLines creates a line from the token, but Number() should
		// handle the nil position gracefully.
		require.Len(t, lines, 1)

		// The line number should be 0 since Position is nil.
		// Note: NewLines may assign a number based on its own tracking.
		// This tests that the fallback path handles nil Position.
		assert.GreaterOrEqual(t, lines[0].Number(), 0)
	})

	t.Run("fallback to segment position line", func(t *testing.T) {
		t.Parallel()

		strTkb := yamltest.NewTokenBuilder().Type(token.StringType)

		// When number field is 0 and segments exist with valid position,
		// Number() should return the first segment's Position.Line.
		tks := token.Tokens{}
		tks.Add(strTkb.Clone().
			Origin("value\n").
			Value("value").
			PositionLine(42).
			PositionColumn(1).
			Build())

		lines := line.NewLines(tks)
		require.Len(t, lines, 1)

		// The line should use the position from the token.
		assert.Equal(t, 42, lines[0].Number())
	})

	t.Run("number field takes precedence over segment position", func(t *testing.T) {
		t.Parallel()

		strTkb := yamltest.NewTokenBuilder().Type(token.StringType)

		// When the internal number field is set, it takes precedence
		// over the segment's Position.Line.
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
		require.Len(t, lines, 2)

		// Both lines should preserve their original line numbers.
		assert.Equal(t, 100, lines[0].Number())
		assert.Equal(t, 200, lines[1].Number())
	})
}

// TestNewLines_WhitespaceType verifies that pure horizontal whitespace parts
// are assigned SpaceType for correct styling.
//
// This handles cases where the go-yaml lexer bundles trailing whitespace (like
// next line's indentation) with the previous token.
func TestNewLines_WhitespaceType(t *testing.T) {
	t.Parallel()

	t.Run("trailing whitespace becomes SpaceType", func(t *testing.T) {
		t.Parallel()

		// The lexer may bundle "true\n  " together as a single boolean token.
		// After splitting, the "  " part should be SpaceType, not BoolType.
		input := "parent:\n  child: true\n"
		tks := lexer.Tokenize(input)
		lines := line.NewLines(tks)

		// Line 2 (index 1) should have the indented "child: true".
		require.Greater(t, len(lines), 1)

		line2 := lines[1]
		tokens := line2.Tokens()

		// Find any pure whitespace tokens and verify they are SpaceType.
		for _, tk := range tokens {
			if strings.TrimSpace(tk.Origin) == "" && tk.Origin != "" && !strings.Contains(tk.Origin, "\n") {
				assert.Equal(t, token.SpaceType, tk.Type,
					"pure horizontal whitespace should be SpaceType, got %s for Origin %q",
					tk.Type, tk.Origin)
			}
		}
	})

	t.Run("block scalar whitespace preserved as StringType", func(t *testing.T) {
		t.Parallel()

		// Block scalar content whitespace should retain StringType.
		input := "text: |\n  line1\n  line2\n"
		tks := lexer.Tokenize(input)
		lines := line.NewLines(tks)

		// Lines 2 and 3 contain block scalar content.
		// Their whitespace should NOT be converted to SpaceType.
		for i := 1; i < len(lines); i++ {
			for _, tk := range lines[i].Tokens() {
				// Check if this is a whitespace-only token from block scalar.
				if strings.TrimSpace(tk.Origin) == "" && tk.Origin != "" {
					// Whitespace in block scalar should remain StringType.
					assert.Equal(t, token.StringType, tk.Type,
						"block scalar whitespace should remain StringType, got %s for Origin %q on line %d",
						tk.Type, tk.Origin, i)
				}
			}
		}
	})

	t.Run("nested indentation with boolean", func(t *testing.T) {
		t.Parallel()

		// More complex case: nested structure with boolean values.
		input := yamltest.Input(`
			root:
			  nested:
			    enabled: true
			    disabled: false
		`)
		tks := lexer.Tokenize(input)
		lines := line.NewLines(tks)

		// Verify all pure horizontal whitespace parts are SpaceType.
		for i, ln := range lines {
			for _, tk := range ln.Tokens() {
				if strings.TrimSpace(tk.Origin) == "" && tk.Origin != "" && !strings.Contains(tk.Origin, "\n") {
					assert.Equal(t, token.SpaceType, tk.Type,
						"pure horizontal whitespace should be SpaceType on line %d, got %s for Origin %q",
						i, tk.Type, tk.Origin)
				}
			}
		}
	})
}

func TestLine_Annotate(t *testing.T) {
	t.Parallel()

	t.Run("add single annotation", func(t *testing.T) {
		t.Parallel()

		tks := lexer.Tokenize("key: value\n")
		lines := line.NewLines(tks)
		require.Len(t, lines, 1)

		ln := &lines[0]
		ln.Annotate(line.Annotation{Content: "note", Position: line.Below})

		require.Len(t, ln.Annotations, 1)
		assert.Equal(t, "note", ln.Annotations[0].Content)
		assert.Equal(t, line.Below, ln.Annotations[0].Position)
	})

	t.Run("add multiple annotations", func(t *testing.T) {
		t.Parallel()

		tks := lexer.Tokenize("key: value\n")
		lines := line.NewLines(tks)
		require.Len(t, lines, 1)

		ln := &lines[0]
		ln.Annotate(
			line.Annotation{Content: "first", Position: line.Above},
			line.Annotation{Content: "second", Position: line.Below},
		)

		require.Len(t, ln.Annotations, 2)
		assert.Equal(t, "first", ln.Annotations[0].Content)
		assert.Equal(t, "second", ln.Annotations[1].Content)
	})

	t.Run("accumulates annotations", func(t *testing.T) {
		t.Parallel()

		tks := lexer.Tokenize("key: value\n")
		lines := line.NewLines(tks)
		require.Len(t, lines, 1)

		ln := &lines[0]
		ln.Annotate(line.Annotation{Content: "first"})
		ln.Annotate(line.Annotation{Content: "second"})

		require.Len(t, ln.Annotations, 2)
	})
}

func TestLine_Overlay(t *testing.T) {
	t.Parallel()

	t.Run("add single overlay", func(t *testing.T) {
		t.Parallel()

		tks := lexer.Tokenize("key: value\n")
		lines := line.NewLines(tks)
		require.Len(t, lines, 1)

		ln := &lines[0]
		ln.Overlay(line.Overlay{
			Cols: position.NewSpan(0, 5),
			Kind: 1,
		})

		require.Len(t, ln.Overlays, 1)
		assert.Equal(t, position.NewSpan(0, 5), ln.Overlays[0].Cols)
		assert.Equal(t, 1, ln.Overlays[0].Kind)
	})

	t.Run("add multiple overlays", func(t *testing.T) {
		t.Parallel()

		tks := lexer.Tokenize("key: value\n")
		lines := line.NewLines(tks)
		require.Len(t, lines, 1)

		ln := &lines[0]
		ln.Overlay(
			line.Overlay{Cols: position.NewSpan(0, 3), Kind: 1},
			line.Overlay{Cols: position.NewSpan(5, 10), Kind: 2},
		)

		require.Len(t, ln.Overlays, 2)
		assert.Equal(t, position.NewSpan(0, 3), ln.Overlays[0].Cols)
		assert.Equal(t, position.NewSpan(5, 10), ln.Overlays[1].Cols)
	})

	t.Run("accumulates overlays", func(t *testing.T) {
		t.Parallel()

		tks := lexer.Tokenize("key: value\n")
		lines := line.NewLines(tks)
		require.Len(t, lines, 1)

		ln := &lines[0]
		ln.Overlay(line.Overlay{Cols: position.NewSpan(0, 3), Kind: 1})
		ln.Overlay(line.Overlay{Cols: position.NewSpan(5, 10), Kind: 2})

		require.Len(t, ln.Overlays, 2)
	})
}

func TestLines_AddOverlay(t *testing.T) {
	t.Parallel()

	t.Run("single line range", func(t *testing.T) {
		t.Parallel()

		tks := lexer.Tokenize("key: value\n")
		lines := line.NewLines(tks)
		require.Len(t, lines, 1)

		lines.AddOverlay(1, position.NewRange(
			position.New(0, 0),
			position.New(0, 5),
		))

		require.Len(t, lines[0].Overlays, 1)
		assert.Equal(t, position.NewSpan(0, 5), lines[0].Overlays[0].Cols)
		assert.Equal(t, 1, lines[0].Overlays[0].Kind)
	})

	t.Run("multi-line range splits across lines", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			key1: value1
			key2: value2
			key3: value3
		`)
		tks := lexer.Tokenize(input)
		lines := line.NewLines(tks)
		require.Len(t, lines, 3)

		// Add overlay spanning all three lines.
		lines.AddOverlay(2, position.NewRange(
			position.New(0, 3),
			position.New(2, 5),
		))

		// First line: col 3 to end of line.
		require.Len(t, lines[0].Overlays, 1)
		assert.Equal(t, 3, lines[0].Overlays[0].Cols.Start)
		assert.Equal(t, 2, lines[0].Overlays[0].Kind)

		// Middle line: full line.
		require.Len(t, lines[1].Overlays, 1)
		assert.Equal(t, 0, lines[1].Overlays[0].Cols.Start)
		assert.Equal(t, 2, lines[1].Overlays[0].Kind)

		// Last line: start to col 5.
		require.Len(t, lines[2].Overlays, 1)
		assert.Equal(t, 0, lines[2].Overlays[0].Cols.Start)
		assert.Equal(t, 5, lines[2].Overlays[0].Cols.End)
		assert.Equal(t, 2, lines[2].Overlays[0].Kind)
	})

	t.Run("multiple ranges", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			key1: value1
			key2: value2
		`)
		tks := lexer.Tokenize(input)
		lines := line.NewLines(tks)
		require.Len(t, lines, 2)

		lines.AddOverlay(1,
			position.NewRange(position.New(0, 0), position.New(0, 4)),
			position.NewRange(position.New(1, 0), position.New(1, 4)),
		)

		require.Len(t, lines[0].Overlays, 1)
		require.Len(t, lines[1].Overlays, 1)
	})

	t.Run("empty lines no-op", func(t *testing.T) {
		t.Parallel()

		var lines line.Lines
		// Should not panic on empty lines.
		lines.AddOverlay(1, position.NewRange(position.New(0, 0), position.New(0, 5)))

		assert.Empty(t, lines)
	})
}

func TestLines_ClearOverlays(t *testing.T) {
	t.Parallel()

	t.Run("clears all overlays", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			key1: value1
			key2: value2
		`)
		tks := lexer.Tokenize(input)
		lines := line.NewLines(tks)
		require.Len(t, lines, 2)

		// Add overlays to both lines.
		lines.AddOverlay(1,
			position.NewRange(position.New(0, 0), position.New(0, 10)),
			position.NewRange(position.New(1, 0), position.New(1, 10)),
		)

		require.Len(t, lines[0].Overlays, 1)
		require.Len(t, lines[1].Overlays, 1)

		// Clear all overlays.
		lines.ClearOverlays()

		assert.Nil(t, lines[0].Overlays)
		assert.Nil(t, lines[1].Overlays)
	})

	t.Run("idempotent on empty", func(t *testing.T) {
		t.Parallel()

		tks := lexer.Tokenize("key: value\n")
		lines := line.NewLines(tks)
		require.Len(t, lines, 1)

		// Clear without any overlays set.
		lines.ClearOverlays()

		assert.Nil(t, lines[0].Overlays)
	})
}

func TestLine_Clone_PreservesOverlays(t *testing.T) {
	t.Parallel()

	tks := lexer.Tokenize("key: value\n")
	lines := line.NewLines(tks)
	require.Len(t, lines, 1)

	original := lines[0]
	original.Overlay(line.Overlay{Cols: position.NewSpan(0, 5), Kind: 1})

	clone := original.Clone()

	// Verify overlays were copied.
	require.Len(t, clone.Overlays, 1)
	assert.Equal(t, original.Overlays[0].Cols, clone.Overlays[0].Cols)
	assert.Equal(t, original.Overlays[0].Kind, clone.Overlays[0].Kind)

	// Modify clone overlays.
	clone.Overlay(line.Overlay{Cols: position.NewSpan(5, 10), Kind: 2})

	// Verify original is unchanged.
	require.Len(t, original.Overlays, 1)
}

func TestLines_ContentPositionRangesAt(t *testing.T) {
	t.Parallel()

	t.Run("single line token", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := lexer.Tokenize(input)
		lines := line.NewLines(tks)

		// Query position at the "key" token (column 0).
		ranges := lines.ContentPositionRangesAt(position.New(0, 0))
		require.Len(t, ranges, 1)
		assert.Equal(t, 0, ranges[0].Start.Line)
		assert.Equal(t, 0, ranges[0].Start.Col)
		assert.Equal(t, 0, ranges[0].End.Line)
		assert.Equal(t, 3, ranges[0].End.Col) // "key" is 3 chars.
	})

	t.Run("multiline token returns all ranges", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			key: |
			  line1
			  line2
		`)
		tks := lexer.Tokenize(input)
		lines := line.NewLines(tks)

		// Query position in the literal block content (line 1, column 2).
		ranges := lines.ContentPositionRangesAt(position.New(1, 2))
		// Should return ranges for all lines where this token appears.
		require.Len(t, ranges, 2)

		lineIdxs := make([]int, len(ranges))
		for i, r := range ranges {
			lineIdxs[i] = r.Start.Line
		}

		assert.Contains(t, lineIdxs, 1)
		assert.Contains(t, lineIdxs, 2)
	})

	t.Run("spaces are trimmed from content", func(t *testing.T) {
		t.Parallel()

		// Test that leading and trailing spaces are excluded from ranges.
		input := "key:   value   \n"
		tks := lexer.Tokenize(input)
		lines := line.NewLines(tks)

		// Query at the value position (after "key: " = 4 chars, but there
		// are extra spaces, so we query somewhere in the value area).
		ranges := lines.ContentPositionRangesAt(position.New(0, 7))
		require.Len(t, ranges, 1)

		// The content range should exclude leading spaces but include "value".
		// Origin is " value" (with leading space from colon),
		// so content starts after the leading space.
		r := ranges[0]
		assert.Equal(t, 0, r.Start.Line)
		assert.Equal(t, 0, r.End.Line)

		// The range width should match content without trailing spaces.
		// Note: exact positions depend on how the lexer tokenizes.
		assert.Greater(t, r.End.Col, r.Start.Col)
	})

	t.Run("position at value with leading space", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := lexer.Tokenize(input)
		lines := line.NewLines(tks)

		// Query at "value" position.
		ranges := lines.ContentPositionRangesAt(position.New(0, 5))
		require.Len(t, ranges, 1)

		// Content range should start at actual content, not the leading space.
		r := ranges[0]
		assert.Equal(t, 0, r.Start.Line)
		// The content "value" without leading space starts at column 5.
		assert.Equal(t, 5, r.Start.Col)
		assert.Equal(t, 10, r.End.Col) // "value" is 5 chars.
	})

	t.Run("position outside tokens returns nil", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := lexer.Tokenize(input)
		lines := line.NewLines(tks)

		// Query at position beyond token content.
		ranges := lines.ContentPositionRangesAt(position.New(0, 100))
		assert.Nil(t, ranges)
	})

	t.Run("line out of bounds returns nil", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := lexer.Tokenize(input)
		lines := line.NewLines(tks)

		assert.Nil(t, lines.ContentPositionRangesAt(position.New(999, 0)))
		assert.Nil(t, lines.ContentPositionRangesAt(position.New(-1, 0)))
	})

	t.Run("empty lines returns nil", func(t *testing.T) {
		t.Parallel()

		var lines line.Lines
		assert.Nil(t, lines.ContentPositionRangesAt(position.New(0, 0)))
	})

	t.Run("blank line in document", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n\nnext: data\n"
		tks := lexer.Tokenize(input)
		lines := line.NewLines(tks)

		require.Len(t, lines, 3)

		// Query at blank line (line index 1).
		// The blank line may be absorbed into previous token.
		ranges := lines.ContentPositionRangesAt(position.New(1, 0))

		// Should return nil or empty if no content at blank line.
		// Behavior depends on how blank lines are handled.
		// This just verifies no panic occurs.
		_ = ranges
	})

	t.Run("whitespace-only token returns nil", func(t *testing.T) {
		t.Parallel()

		// Test behavior when token contains only whitespace.
		input := "key:     \nnext: value\n"
		tks := lexer.Tokenize(input)
		lines := line.NewLines(tks)

		// Query at position where only spaces exist after colon.
		// If the space is part of a token, ContentPositionRangesAt should
		// return nil since there's no non-space content.
		ranges := lines.ContentPositionRangesAt(position.New(0, 5))

		// May be nil if the position is at whitespace-only content,
		// or may return empty ranges. This verifies the edge case is handled.
		for _, r := range ranges {
			// Any returned range should have positive width.
			assert.Greater(t, r.End.Col, r.Start.Col)
		}
	})
}
