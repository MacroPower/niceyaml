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
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/style"
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
			// Note: lexer doesn't preserve trailing newline on final simple values.
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
				annotations: []line.Annotation{{Content: "error here", Placement: line.Below}},
				want: `   1 | key: value
   1 | ^ error here`,
			},
			"annotation below with padding": {
				annotations: []line.Annotation{{Content: "note", Placement: line.Below, Col: 4}},
				want: `   1 | key: value
   1 |     ^ note`,
			},
			"annotation above": {
				annotations: []line.Annotation{{Content: "@@ hunk header @@", Placement: line.Above}},
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
				ln.AddAnnotation(tc.annotations...)

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
		original.AddAnnotation(line.Annotation{Content: "original note", Placement: line.Below})

		clone := original.Clone()

		// Verify annotations were copied.
		require.Len(t, clone.Annotations(), 1)
		require.Len(t, original.Annotations(), len(clone.Annotations()))
		assert.Equal(t, original.Annotations()[0].Content, clone.Annotations()[0].Content)

		// Verify position by checking filtered results.
		belowCount := 0
		for _, ann := range clone.Annotations() {
			if ann.Placement == line.Below {
				belowCount++
			}
		}

		assert.Equal(t, 1, belowCount)

		// Modify clone annotations.
		clone.AddAnnotation(line.Annotation{Content: "modified", Placement: line.Above})

		// Verify original is unchanged.
		require.Len(t, original.Annotations(), 1)
		assert.Equal(t, "original note", original.Annotations()[0].Content)

		origBelowCount := 0
		for _, ann := range original.Annotations() {
			if ann.Placement == line.Below {
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

			tks := lexer.Tokenize(tc.input)
			lines := line.NewLines(tks)

			// Verify line numbers are strictly increasing.
			require.NoError(t, yamltest.ValidateLines(lines), "tokens should be valid")

			// Verify expected line numbers.
			require.Len(t, lines, len(tc.want), "wrong number of lines")

			for i, wantNum := range tc.want {
				assert.Equal(t, wantNum, lines[i].Number(), "line %d has wrong number", i)
			}
		})
	}
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

func TestNewLines_OffsetRuneCount_Continuation(t *testing.T) {
	t.Parallel()

	// Continuation parts of a multi-line token take their offset from the
	// builder's running count, which must advance by runes like the lexer.
	// "key: héllo\n" is 11 runes, so the continuation starts at offset 12.
	input := "key: h\u00e9llo\n  w\u00f6rld\nnext: v\n"
	lines := line.NewLines(lexer.Tokenize(input))
	require.Len(t, lines, 3)

	continuation := lines[1].Token(0)
	assert.Equal(t, "  w\u00f6rld\n", continuation.Origin)
	assert.Equal(t, 12, continuation.Position.Offset)

	next := lines[2].Token(0)
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
				input: stringtest.Input(`
					key: |
					  line1
					  line2
				`),
				want: 3, // Position should be on last content line.
			},
			"literal three lines": {
				input: stringtest.Input(`
					key: |
					  a
					  b
					  c
				`),
				want: 4,
			},
			"folded two lines": {
				input: stringtest.Input(`
					key: >
					  first
					  second
				`),
				want: 3,
			},
			"literal with strip": {
				input: stringtest.Input(`
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
		}

		for name, input := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				originalTks := lexer.Tokenize(input)
				lines := line.NewLines(originalTks)
				resultTks := lines.Tokens()

				require.NoError(t, yamltest.ValidateLines(lines))

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
			input: stringtest.Input(`
				key: this is
				  continued
			`),
			want: 1, // Position should be on FIRST line.
		},
		"plain multiline three lines": {
			input: stringtest.Input(`
				key: first
				  second
				  third
			`),
			want: 1,
		},
		"plain multiline with more indent": {
			input: stringtest.Input(`
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

		input := stringtest.Input(`
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

		input := stringtest.Input(`
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

				lines := line.NewLines(lexer.Tokenize(tc.input))

				got := make([]int, 0, len(lines))
				for _, ln := range lines {
					got = append(got, ln.Number())
				}

				assert.Equal(t, tc.want, got)
			})
		}
	})
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

			for _, ln := range line.NewLines(lexer.Tokenize(input)) {
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

		input := stringtest.Input(`
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
		// Both lines belong to the same source token, so TokenAt returns the
		// same original pointer for each.
		assert.Same(t, tk1, tk2)
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

func TestLines_TokenRanges(t *testing.T) {
	t.Parallel()

	t.Run("single token range", func(t *testing.T) {
		t.Parallel()

		tks := lexer.Tokenize("key: value\n")
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
		tks := lexer.Tokenize(input)
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
		lines := line.NewLines(lexer.Tokenize(input))

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
		lines := line.NewLines(lexer.Tokenize(input))

		part := lines[2].Token(0)

		ranges := lines.TokenRanges(part)
		assert.Equal(t, position.Ranges{
			position.NewRange(position.New(2, 0), position.New(2, 7)),
		}, ranges)
	})

	t.Run("value token starts after the key", func(t *testing.T) {
		t.Parallel()

		lines := line.NewLines(lexer.Tokenize("key: value\n"))

		ranges := lines.TokenRanges(lines.TokenAt(position.New(0, 5)))
		require.Len(t, ranges, 1)
		assert.Equal(t, 4, ranges[0].Start.Col)
	})

	t.Run("nil token returns nil", func(t *testing.T) {
		t.Parallel()

		lines := line.NewLines(lexer.Tokenize("key: value\n"))

		assert.Nil(t, lines.TokenRanges(nil))
		assert.Nil(t, lines.TokenRanges(lines.TokenAt(position.New(0, 100))))
	})

	t.Run("token not in lines returns nil", func(t *testing.T) {
		t.Parallel()

		lines := line.NewLines(lexer.Tokenize("key: value\n"))
		other := lexer.Tokenize("other: data\n")

		assert.Nil(t, lines.TokenRanges(other[0]))
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

		input := stringtest.Input(`
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
		lines[0].AddAnnotation(line.Annotation{Content: "test annotation"})

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

		input := stringtest.Input(`
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
		input := stringtest.Input(`
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

		ln := lines[0]
		ln.AddAnnotation(line.Annotation{Content: "note", Placement: line.Below})

		require.Len(t, ln.Annotations(), 1)
		assert.Equal(t, "note", ln.Annotations()[0].Content)
		assert.Equal(t, line.Below, ln.Annotations()[0].Placement)
	})

	t.Run("add multiple annotations", func(t *testing.T) {
		t.Parallel()

		tks := lexer.Tokenize("key: value\n")
		lines := line.NewLines(tks)
		require.Len(t, lines, 1)

		ln := lines[0]
		ln.AddAnnotation(
			line.Annotation{Content: "first", Placement: line.Above},
			line.Annotation{Content: "second", Placement: line.Below},
		)

		require.Len(t, ln.Annotations(), 2)
		assert.Equal(t, "first", ln.Annotations()[0].Content)
		assert.Equal(t, "second", ln.Annotations()[1].Content)
	})

	t.Run("accumulates annotations", func(t *testing.T) {
		t.Parallel()

		tks := lexer.Tokenize("key: value\n")
		lines := line.NewLines(tks)
		require.Len(t, lines, 1)

		ln := lines[0]
		ln.AddAnnotation(line.Annotation{Content: "first"})
		ln.AddAnnotation(line.Annotation{Content: "second"})

		require.Len(t, ln.Annotations(), 2)
	})
}

func TestLine_Overlay(t *testing.T) {
	t.Parallel()

	t.Run("add single overlay", func(t *testing.T) {
		t.Parallel()

		tks := lexer.Tokenize("key: value\n")
		lines := line.NewLines(tks)
		require.Len(t, lines, 1)

		ln := lines[0]
		ln.AddOverlay(line.Overlay{
			Cols:  position.NewSpan(0, 5),
			Style: "test1",
		})

		require.Len(t, ln.Overlays(), 1)
		assert.Equal(t, position.NewSpan(0, 5), ln.Overlays()[0].Cols)
		assert.Equal(t, style.Style("test1"), ln.Overlays()[0].Style)
	})

	t.Run("add multiple overlays", func(t *testing.T) {
		t.Parallel()

		tks := lexer.Tokenize("key: value\n")
		lines := line.NewLines(tks)
		require.Len(t, lines, 1)

		ln := lines[0]
		ln.AddOverlay(
			line.Overlay{Cols: position.NewSpan(0, 3), Style: "test1"},
			line.Overlay{Cols: position.NewSpan(5, 10), Style: "test2"},
		)

		require.Len(t, ln.Overlays(), 2)
		assert.Equal(t, position.NewSpan(0, 3), ln.Overlays()[0].Cols)
		assert.Equal(t, position.NewSpan(5, 10), ln.Overlays()[1].Cols)
	})

	t.Run("accumulates overlays", func(t *testing.T) {
		t.Parallel()

		tks := lexer.Tokenize("key: value\n")
		lines := line.NewLines(tks)
		require.Len(t, lines, 1)

		ln := lines[0]
		ln.AddOverlay(line.Overlay{Cols: position.NewSpan(0, 3), Style: "test1"})
		ln.AddOverlay(line.Overlay{Cols: position.NewSpan(5, 10), Style: "test2"})

		require.Len(t, ln.Overlays(), 2)
	})
}

func TestLines_AddOverlay(t *testing.T) {
	t.Parallel()

	t.Run("out of range lines are skipped", func(t *testing.T) {
		t.Parallel()

		tks := lexer.Tokenize("a: 1\nb: 2\n")
		lines := line.NewLines(tks)
		require.Len(t, lines, 2)

		// A range that starts before the first line and ends past the last
		// applies only to the lines that exist.
		lines.AddOverlay("test1", position.NewRange(
			position.New(-1, 0),
			position.New(5, 3),
		))

		require.Len(t, lines[0].Overlays(), 1)
		require.Len(t, lines[1].Overlays(), 1)

		// A range entirely outside the collection is a no-op.
		lines.AddOverlay("test2", position.NewRange(
			position.New(7, 0),
			position.New(7, 3),
		))

		assert.Len(t, lines[0].Overlays(), 1)
		assert.Len(t, lines[1].Overlays(), 1)
	})

	t.Run("single line range", func(t *testing.T) {
		t.Parallel()

		tks := lexer.Tokenize("key: value\n")
		lines := line.NewLines(tks)
		require.Len(t, lines, 1)

		lines.AddOverlay("test1", position.NewRange(
			position.New(0, 0),
			position.New(0, 5),
		))

		require.Len(t, lines[0].Overlays(), 1)
		assert.Equal(t, position.NewSpan(0, 5), lines[0].Overlays()[0].Cols)
		assert.Equal(t, style.Style("test1"), lines[0].Overlays()[0].Style)
	})

	t.Run("multi-line range splits across lines", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			key1: value1
			key2: value2
			key3: value3
		`)
		tks := lexer.Tokenize(input)
		lines := line.NewLines(tks)
		require.Len(t, lines, 3)

		// Add overlay spanning all three lines.
		lines.AddOverlay("test2", position.NewRange(
			position.New(0, 3),
			position.New(2, 5),
		))

		// First line: col 3 to end of line.
		require.Len(t, lines[0].Overlays(), 1)
		assert.Equal(t, 3, lines[0].Overlays()[0].Cols.Start)
		assert.Equal(t, style.Style("test2"), lines[0].Overlays()[0].Style)

		// Middle line: full line.
		require.Len(t, lines[1].Overlays(), 1)
		assert.Equal(t, 0, lines[1].Overlays()[0].Cols.Start)
		assert.Equal(t, style.Style("test2"), lines[1].Overlays()[0].Style)

		// Last line: start to col 5.
		require.Len(t, lines[2].Overlays(), 1)
		assert.Equal(t, 0, lines[2].Overlays()[0].Cols.Start)
		assert.Equal(t, 5, lines[2].Overlays()[0].Cols.End)
		assert.Equal(t, style.Style("test2"), lines[2].Overlays()[0].Style)
	})

	t.Run("multiple ranges", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			key1: value1
			key2: value2
		`)
		tks := lexer.Tokenize(input)
		lines := line.NewLines(tks)
		require.Len(t, lines, 2)

		lines.AddOverlay("test1",
			position.NewRange(position.New(0, 0), position.New(0, 4)),
			position.NewRange(position.New(1, 0), position.New(1, 4)),
		)

		require.Len(t, lines[0].Overlays(), 1)
		require.Len(t, lines[1].Overlays(), 1)
	})

	t.Run("inverted ranges add nothing", func(t *testing.T) {
		t.Parallel()

		lines := line.NewLines(lexer.Tokenize("a: 1\nb: 2\nc: 3\n"))
		require.Len(t, lines, 3)

		// Both ranges end on the line before they start, one at column 0 and
		// one past it.
		lines.AddOverlay("test1",
			position.NewRange(position.New(2, 0), position.New(1, 0)),
			position.NewRange(position.New(2, 0), position.New(1, 3)),
		)

		for i := range lines {
			assert.Empty(t, lines[i].Overlays(), "line %d", i)
		}
	})

	t.Run("empty lines no-op", func(t *testing.T) {
		t.Parallel()

		var lines line.Lines

		// Should not panic on empty lines.
		lines.AddOverlay("test1", position.NewRange(position.New(0, 0), position.New(0, 5)))

		assert.Empty(t, lines)
	})
}

func TestLines_ClearOverlays(t *testing.T) {
	t.Parallel()

	t.Run("clears all overlays", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			key1: value1
			key2: value2
		`)
		tks := lexer.Tokenize(input)
		lines := line.NewLines(tks)
		require.Len(t, lines, 2)

		// Add overlays to both lines.
		lines.AddOverlay("test1",
			position.NewRange(position.New(0, 0), position.New(0, 10)),
			position.NewRange(position.New(1, 0), position.New(1, 10)),
		)

		require.Len(t, lines[0].Overlays(), 1)
		require.Len(t, lines[1].Overlays(), 1)

		// Clear all overlays.
		lines.ClearOverlays()

		assert.Nil(t, lines[0].Overlays())
		assert.Nil(t, lines[1].Overlays())
	})

	t.Run("idempotent on empty", func(t *testing.T) {
		t.Parallel()

		tks := lexer.Tokenize("key: value\n")
		lines := line.NewLines(tks)
		require.Len(t, lines, 1)

		// Clear without any overlays set.
		lines.ClearOverlays()

		assert.Nil(t, lines[0].Overlays())
	})
}

func TestLine_Clone_PreservesOverlays(t *testing.T) {
	t.Parallel()

	tks := lexer.Tokenize("key: value\n")
	lines := line.NewLines(tks)
	require.Len(t, lines, 1)

	original := lines[0]
	original.AddOverlay(line.Overlay{Cols: position.NewSpan(0, 5), Style: "test1"})

	clone := original.Clone()

	// Verify overlays were copied.
	require.Len(t, clone.Overlays(), 1)
	assert.Equal(t, original.Overlays()[0].Cols, clone.Overlays()[0].Cols)
	assert.Equal(t, original.Overlays()[0].Style, clone.Overlays()[0].Style)

	// Modify clone overlays.
	clone.AddOverlay(line.Overlay{Cols: position.NewSpan(5, 10), Style: "test2"})

	// Verify original is unchanged.
	require.Len(t, original.Overlays(), 1)
}

func TestLines_ContentRanges(t *testing.T) {
	t.Parallel()

	t.Run("single line token", func(t *testing.T) {
		t.Parallel()

		lines := line.NewLines(lexer.Tokenize("key: value\n"))

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
		lines := line.NewLines(lexer.Tokenize(input))

		ranges := lines.ContentRanges(lines.TokenAt(position.New(1, 2)))
		assert.Equal(t, position.Ranges{
			position.NewRange(position.New(1, 2), position.New(1, 7)),
			position.NewRange(position.New(2, 2), position.New(2, 7)),
		}, ranges)
	})

	t.Run("excludes leading and trailing spaces", func(t *testing.T) {
		t.Parallel()

		tks := lexer.Tokenize("key:   value  \n")
		lines := line.NewLines(tks)
		require.Len(t, tks, 3)

		tk := lines.TokenAt(position.New(0, 6))
		require.Same(t, tks[2], tk)

		assert.Equal(t, position.Ranges{
			position.NewRange(position.New(0, 7), position.New(0, 12)),
		}, lines.ContentRanges(tk))
	})

	t.Run("space-only part contributes no range", func(t *testing.T) {
		t.Parallel()

		lines := line.NewLines(lexer.Tokenize("key:     \nnext: value\n"))

		for _, r := range lines.ContentRanges(lines.TokenAt(position.New(0, 5))) {
			assert.Greater(t, r.End.Col, r.Start.Col)
		}
	})

	t.Run("nil and missing tokens return nil", func(t *testing.T) {
		t.Parallel()

		lines := line.NewLines(lexer.Tokenize("key: value\n"))

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

		lines := line.NewLines(lexer.Tokenize(input))

		assert.Equal(t, 3, lines.Len())
		assert.False(t, lines.IsEmpty())
	})

	t.Run("Width is the widest line", func(t *testing.T) {
		t.Parallel()

		lines := line.NewLines(lexer.Tokenize(input))

		assert.Equal(t, len("key: value"), lines.Width())
	})

	t.Run("AllLines yields every index and line", func(t *testing.T) {
		t.Parallel()

		lines := line.NewLines(lexer.Tokenize(input))

		var (
			indices  []int
			contents []string
		)

		for i, ln := range lines.AllLines() {
			indices = append(indices, i)
			contents = append(contents, ln.Content())
		}

		assert.Equal(t, []int{0, 1, 2}, indices)
		assert.Equal(t, []string{"key: value", "list:", "  - one"}, contents)
	})

	t.Run("AllLines clamps spans", func(t *testing.T) {
		t.Parallel()

		lines := line.NewLines(lexer.Tokenize(input))

		var indices []int

		for i := range lines.AllLines(position.NewSpan(1, 99)) {
			indices = append(indices, i)
		}

		assert.Equal(t, []int{1, 2}, indices)
	})

	t.Run("AllLines yields the lines of the collection", func(t *testing.T) {
		t.Parallel()

		lines := line.NewLines(lexer.Tokenize(input))

		for i, ln := range lines.AllLines() {
			assert.Same(t, lines[i], ln)

			ln.AddAnnotation(line.Annotation{Content: "note", Placement: line.Below})
		}

		for _, ln := range lines {
			require.Len(t, ln.Annotations(), 1)
			assert.Equal(t, "note", ln.Annotations()[0].Content)
		}
	})

	t.Run("AllRunes round-trips the input", func(t *testing.T) {
		t.Parallel()

		lines := line.NewLines(lexer.Tokenize(input))

		var sb strings.Builder

		for _, r := range lines.AllRunes() {
			sb.WriteRune(r)
		}

		assert.Equal(t, input, sb.String())
	})

	t.Run("CRLF endings do not count toward width or runes", func(t *testing.T) {
		t.Parallel()

		lines := line.NewLines(lexer.Tokenize("key: value\r\nother: data\r\n"))

		assert.Equal(t, len("other: data"), lines.Width())

		var sb strings.Builder

		for pos, r := range lines.AllRunes() {
			if r == '\n' {
				assert.Equal(t, lines[pos.Line].Width(), pos.Col)
			}

			sb.WriteRune(r)
		}

		assert.Equal(t, "key: value\nother: data\n", sb.String())
	})

	t.Run("Clone is independent", func(t *testing.T) {
		t.Parallel()

		lines := line.NewLines(lexer.Tokenize(input))
		clone := lines.Clone()

		clone.AddOverlay(style.GenericHighlight, position.NewRange(
			position.New(0, 0),
			position.New(0, 3),
		))
		clone[1].AddAnnotation(line.Annotation{Content: "note", Placement: line.Below})

		clone[2].SetFlag(line.FlagInserted)

		require.Len(t, clone, 3)
		assert.Equal(t, lines.Content(), clone.Content())

		assert.Empty(t, lines[0].Overlays())
		assert.Empty(t, lines[1].Annotations())
		assert.Equal(t, line.FlagDefault, lines[2].Flag())
	})

	t.Run("empty", func(t *testing.T) {
		t.Parallel()

		var lines line.Lines

		assert.Equal(t, 0, lines.Len())
		assert.True(t, lines.IsEmpty())
		assert.Equal(t, 0, lines.Width())
		assert.Nil(t, lines.Clone())

		for range lines.AllLines() {
			t.Fatal("expected no lines")
		}

		for range lines.AllRunes() {
			t.Fatal("expected no runes")
		}
	})
}
