package niceyaml_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macropower/niceyaml"
)

// dumpTokens concatenates all token Origins into a single string.
func dumpTokens(tks token.Tokens) string {
	var sb strings.Builder
	for _, tk := range tks {
		sb.WriteString(tk.Origin)
	}

	return sb.String()
}

func TestNewTokens_Value_Roundtrip(t *testing.T) {
	t.Parallel()

	// Read testdata file for comprehensive round-trip testing.
	fullYAML, err := os.ReadFile(filepath.Join("testdata", "full.yaml"))
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
			result := niceyaml.NewSourceFromTokens(input, niceyaml.WithName("test"))
			gotTokens := result.Tokens()
			gotContent := result.Content()

			assert.Equal(t, dumpTokens(input), dumpTokens(gotTokens))
			assert.Equal(t, strings.TrimSuffix(dumpTokens(input), "\n"), gotContent)
		})
	}
}

func TestNewTokens_PerLine(t *testing.T) {
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
			result := niceyaml.NewSourceFromTokens(input, niceyaml.WithName("test"))

			require.Equal(t, len(tc.wantLines), result.Count(), "wrong number of lines")

			for i, want := range tc.wantLines {
				assert.Equal(t, want, result.Line(i).String(), "line %d", i)
			}
		})
	}
}

func TestNewTokens_NonStandardLineNumbers(t *testing.T) {
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

			result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

			require.Equal(t, tc.wantLineCount, result.Count(), "wrong number of lines")

			for i, wantNum := range tc.wantLineNums {
				assert.Equal(t, wantNum, result.Line(i).Number(), "line %d has wrong number", i)
			}

			// Verify round-trip: dumping tokens should preserve content.
			gotTokens := result.Tokens()
			assert.Equal(t, dumpTokens(tks), dumpTokens(gotTokens), "round-trip content mismatch")
		})
	}
}

func TestNewTokens_GappedLineNumbers(t *testing.T) {
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
			result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

			require.Equal(t, tc.wantLineCount, result.Count(), "wrong number of lines")

			for i, wantNum := range tc.wantLineNums {
				assert.Equal(t, wantNum, result.Line(i).Number(), "line %d has wrong number", i)
			}

			// Verify Prev/Next linking works correctly across gaps.
			gotTokens := result.Tokens()
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
		annotation niceyaml.Annotation
	}{
		"no annotation": {
			annotation: niceyaml.Annotation{},
			want:       "   1 | key: value",
		},
		"annotation at column 1": {
			annotation: niceyaml.Annotation{Content: "error here", Column: 1},
			want: `   1 | key: value
   1 | ^ error here`,
		},
		"annotation at column 5": {
			annotation: niceyaml.Annotation{Content: "note", Column: 5},
			want: `   1 | key: value
   1 |     ^ note`,
		},
		"annotation at column 0": {
			annotation: niceyaml.Annotation{Content: "edge", Column: 0},
			want: `   1 | key: value
   1 | ^ edge`,
		},
		"large column": {
			annotation: niceyaml.Annotation{Content: "far", Column: 20},
			want: `   1 | key: value
   1 |                    ^ far`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// Create a line with a simple token.
			tks := lexer.Tokenize("key: value\n")
			result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))
			require.Equal(t, 1, result.Count())

			line := result.Line(0)
			line.Annotation = tc.annotation

			assert.Equal(t, tc.want, line.String())
		})
	}
}

func TestTokens_String_Annotation(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input       string
		annotations map[int]niceyaml.Annotation // Line index -> annotation.
		want        string
	}{
		"single line with annotation": {
			input: "key: value\n",
			annotations: map[int]niceyaml.Annotation{
				0: {Content: "error", Column: 1},
			},
			want: `   1 | key: value
   1 | ^ error`,
		},
		"multiple lines one annotation": {
			input: `first: 1
second: 2
`,
			annotations: map[int]niceyaml.Annotation{
				1: {Content: "here", Column: 1},
			},
			want: `   1 | first: 1
   2 | second: 2
   2 | ^ here`,
		},
		"multiple lines multiple annotations": {
			input: `first: 1
second: 2
third: 3
`,
			annotations: map[int]niceyaml.Annotation{
				0: {Content: "start", Column: 1},
				2: {Content: "end", Column: 1},
			},
			want: `   1 | first: 1
   1 | ^ start
   2 | second: 2
   3 | third: 3
   3 | ^ end`,
		},
		"mixed annotated and non-annotated": {
			input: `a: 1
b: 2
c: 3
d: 4
`,
			annotations: map[int]niceyaml.Annotation{
				1: {Content: "middle", Column: 3},
			},
			want: `   1 | a: 1
   2 | b: 2
   2 |   ^ middle
   3 | c: 3
   4 | d: 4`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := lexer.Tokenize(tc.input)
			result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

			// Apply annotations to specified lines.
			for idx, ann := range tc.annotations {
				require.Less(t, idx, result.Count(), "annotation index out of range")

				result.Annotate(idx, ann)
			}

			assert.Equal(t, tc.want, result.String())
		})
	}
}

func TestLine_Clone_Annotation(t *testing.T) {
	t.Parallel()

	tks := lexer.Tokenize("key: value\n")
	result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))
	require.Equal(t, 1, result.Count())

	original := result.Line(0)
	original.Annotation = niceyaml.Annotation{Content: "original note", Column: 5}

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

func TestNewTokens_Value_PrevNextLinking(t *testing.T) {
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
			result := niceyaml.NewSourceFromTokens(input, niceyaml.WithName("test"))

			// Call Value() to establish Prev/Next linking across lines.
			tks := result.Tokens()
			require.NotEmpty(t, tks, "expected non-empty tokens")

			// Count total tokens across all lines.
			totalTokens := 0
			for _, line := range result.Lines() {
				totalTokens += len(line.Tokens())
			}

			// Start from first token in Line(0).Value.
			firstToken := result.Line(0).Token(0)
			lastLine := result.Line(result.Count() - 1)
			lastToken := lastLine.Tokens()[len(lastLine.Tokens())-1]

			// First token should have no Prev.
			assert.Nil(t, firstToken.Prev, "first token Prev should be nil")

			// Last token should have no Next.
			assert.Nil(t, lastToken.Next, "last token Next should be nil")

			// Verify forward traversal from Lines[0].Value[0] reaches all tokens.
			forwardCount := 0
			for tk := firstToken; tk != nil; tk = tk.Next {
				forwardCount++
			}

			assert.Equal(t, totalTokens, forwardCount, "forward traversal count mismatch")

			// Verify backward traversal from last line's last token reaches all tokens.
			backwardCount := 0
			for tk := lastToken; tk != nil; tk = tk.Prev {
				backwardCount++
			}

			assert.Equal(t, totalTokens, backwardCount, "backward traversal count mismatch")

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

func TestNewTokens_LeadingNewlineTokens(t *testing.T) {
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
			result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

			// Verify line numbers are strictly increasing.
			require.NoError(t, result.Validate(), "tokens should be valid")

			// Verify expected line numbers.
			require.Equal(t, len(tc.wantLineNums), result.Count(), "wrong number of lines")

			for i, wantNum := range tc.wantLineNums {
				assert.Equal(t, wantNum, result.Line(i).Number(), "line %d has wrong number", i)
			}
		})
	}
}

type runePosition struct {
	R   rune
	Pos niceyaml.Position
}

func TestLines_Runes(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  []runePosition
	}{
		"simple key-value": {
			// Note: lexer strips trailing newline from final simple value.
			input: "a: b\n",
			want: []runePosition{
				{R: 'a', Pos: niceyaml.NewPosition(0, 0)},
				{R: ':', Pos: niceyaml.NewPosition(0, 1)},
				{R: ' ', Pos: niceyaml.NewPosition(0, 2)},
				{R: 'b', Pos: niceyaml.NewPosition(0, 3)},
			},
		},
		"multi-line": {
			// Note: lexer strips trailing newline from final value on each line.
			input: "a: 1\nb: 2\n",
			want: []runePosition{
				{R: 'a', Pos: niceyaml.NewPosition(0, 0)},
				{R: ':', Pos: niceyaml.NewPosition(0, 1)},
				{R: ' ', Pos: niceyaml.NewPosition(0, 2)},
				{R: '1', Pos: niceyaml.NewPosition(0, 3)},
				{R: '\n', Pos: niceyaml.NewPosition(0, 4)},
				{R: 'b', Pos: niceyaml.NewPosition(1, 0)},
				{R: ':', Pos: niceyaml.NewPosition(1, 1)},
				{R: ' ', Pos: niceyaml.NewPosition(1, 2)},
				{R: '2', Pos: niceyaml.NewPosition(1, 3)},
			},
		},
		"utf8 - multibyte char": {
			input: "k: ü\n",
			want: []runePosition{
				{R: 'k', Pos: niceyaml.NewPosition(0, 0)},
				{R: ':', Pos: niceyaml.NewPosition(0, 1)},
				{R: ' ', Pos: niceyaml.NewPosition(0, 2)},
				{R: 'ü', Pos: niceyaml.NewPosition(0, 3)},
			},
		},
		"utf8 - japanese": {
			input: "k: 日本\n",
			want: []runePosition{
				{R: 'k', Pos: niceyaml.NewPosition(0, 0)},
				{R: ':', Pos: niceyaml.NewPosition(0, 1)},
				{R: ' ', Pos: niceyaml.NewPosition(0, 2)},
				{R: '日', Pos: niceyaml.NewPosition(0, 3)},
				{R: '本', Pos: niceyaml.NewPosition(0, 4)},
			},
		},
		"nested with indent": {
			input: "p:\n  c: v\n",
			want: []runePosition{
				{R: 'p', Pos: niceyaml.NewPosition(0, 0)},
				{R: ':', Pos: niceyaml.NewPosition(0, 1)},
				{R: '\n', Pos: niceyaml.NewPosition(0, 2)},
				{R: ' ', Pos: niceyaml.NewPosition(1, 0)},
				{R: ' ', Pos: niceyaml.NewPosition(1, 1)},
				{R: 'c', Pos: niceyaml.NewPosition(1, 2)},
				{R: ':', Pos: niceyaml.NewPosition(1, 3)},
				{R: ' ', Pos: niceyaml.NewPosition(1, 4)},
				{R: 'v', Pos: niceyaml.NewPosition(1, 5)},
			},
		},
		"empty": {
			input: "",
			want:  nil,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := lexer.Tokenize(tc.input)
			lines := niceyaml.NewSourceFromTokens(tks)

			var got []runePosition

			for pos, r := range lines.Runes() {
				got = append(got, runePosition{R: r, Pos: pos})
			}

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestLines_Runes_LiteralBlock(t *testing.T) {
	t.Parallel()

	// Literal blocks have multi-line content.
	// Runes should iterate through all content with correct positions.
	input := "s: |\n  a\n  b\n"
	tks := lexer.Tokenize(input)
	lines := niceyaml.NewSourceFromTokens(tks)

	var runes []rune //nolint:prealloc // Size unknown from iterator.

	var positions []niceyaml.Position //nolint:prealloc // Size unknown from iterator.

	for pos, r := range lines.Runes() {
		runes = append(runes, r)
		positions = append(positions, pos)
	}

	// Verify we iterate through all content.
	require.NotEmpty(t, runes, "should have runes")
	require.NotEmpty(t, positions, "should have positions")

	// Verify positions start at line 0 (0-indexed).
	assert.Equal(t, 0, positions[0].Line, "should start at line 0")
	assert.Equal(t, 0, positions[0].Col, "should start at column 0")

	// Verify positions increase monotonically.
	prevLine := -1

	prevCol := -1

	for i, pos := range positions {
		if pos.Line == prevLine {
			assert.Greater(t, pos.Col, prevCol, "column should increase on same line at index %d", i)
		} else if pos.Line > prevLine {
			// Line changed, column should reset to 0.
			assert.Equal(t, 0, pos.Col, "column should reset to 0 on new line at index %d", i)
		}

		prevLine = pos.Line
		prevCol = pos.Col
	}

	// Verify the last position is on a later line (multi-line content).
	assert.Positive(t, positions[len(positions)-1].Line, "should have content on multiple lines")
}

func TestLines_Runes_DiffBuiltLines(t *testing.T) {
	t.Parallel()

	// When Lines are built from a diff, Position.Line should be based on
	// the visual line index (Line.idx), not the source token position.
	// This is critical for Finder to work correctly with diffs.

	before := "key: old\n"
	after := "key: new\n"

	beforeLines := niceyaml.NewSourceFromString(before, niceyaml.WithName("before"))
	afterLines := niceyaml.NewSourceFromString(after, niceyaml.WithName("after"))

	revBefore := niceyaml.NewRevision(beforeLines)
	revAfter := niceyaml.NewRevision(afterLines)

	diff := niceyaml.NewFullDiff(revBefore, revAfter)
	lines := diff.Lines()

	// Diff should produce two lines: deleted (old) and inserted (new).
	// Both have the same source token line (1), but different visual indices (0, 1).
	require.Equal(t, 2, lines.Count(), "diff should produce 2 lines")

	var positions []struct {
		line int
		col  int
	}

	for pos, r := range lines.Runes() {
		if r == 'k' { // First char of each line.
			positions = append(positions, struct {
				line int
				col  int
			}{line: pos.Line, col: pos.Col})
		}
	}

	// Should have 2 'k' characters, one on each visual line.
	require.Len(t, positions, 2, "should have 2 lines starting with 'k'")

	// First line should be at visual line 0.
	assert.Equal(t, 0, positions[0].line, "first line should be at visual line 0")
	assert.Equal(t, 0, positions[0].col, "first 'k' should be at column 0")

	// Second line should be at visual line 1 (not 0, even though source token line is same).
	assert.Equal(t, 1, positions[1].line, "second line should be at visual line 1")
	assert.Equal(t, 0, positions[1].col, "second 'k' should be at column 0")
}

func TestLines_Lines_EarlyBreak(t *testing.T) {
	t.Parallel()

	input := "a: 1\nb: 2\nc: 3\n"
	tks := lexer.Tokenize(input)
	lines := niceyaml.NewSourceFromTokens(tks)

	var collected []int //nolint:prealloc // Testing early break with unknown iteration count.
	for pos := range lines.Lines() {
		collected = append(collected, pos.Line)
		if pos.Line >= 1 {
			break
		}
	}

	assert.Equal(t, []int{0, 1}, collected)
}

func TestLines_Runes_EarlyBreak(t *testing.T) {
	t.Parallel()

	input := "abc\n"
	tks := lexer.Tokenize(input)
	lines := niceyaml.NewSourceFromTokens(tks)

	var collected []rune //nolint:prealloc // Testing early break with unknown iteration count.
	for _, r := range lines.Runes() {
		collected = append(collected, r)
		if r == 'b' {
			break
		}
	}

	assert.Equal(t, []rune{'a', 'b'}, collected)
}

func TestTokens_Validate(t *testing.T) {
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
				result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

				// Tokens created through NewTokensFromTokens should always be valid.
				assert.NoError(t, result.Validate())
			})
		}
	})

	t.Run("line numbers normalized - same input", func(t *testing.T) {
		t.Parallel()

		// Create tokens with same line number but separated by newlines.
		// NewTokensFromTokens normalizes them to be monotonically increasing.
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

		result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

		// After normalization, line numbers are sequential.
		require.NoError(t, result.Validate())
		require.Equal(t, 2, result.Count())
		assert.Equal(t, 5, result.Line(0).Number())
		assert.Equal(t, 6, result.Line(1).Number())
	})

	t.Run("line numbers normalized - decreasing input", func(t *testing.T) {
		t.Parallel()

		// Create tokens with decreasing line numbers.
		// NewTokensFromTokens normalizes them to be monotonically increasing.
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

		result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

		// After normalization, line numbers are sequential.
		require.NoError(t, result.Validate())
		require.Equal(t, 2, result.Count())
		assert.Equal(t, 10, result.Line(0).Number())
		assert.Equal(t, 11, result.Line(1).Number())
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

		result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

		err := result.Validate()
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

		result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

		err := result.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "column 5 not greater than previous 10")
	})

	t.Run("token line numbers normalized on same line", func(t *testing.T) {
		t.Parallel()

		// Create tokens with inconsistent position line numbers.
		// NewTokensFromTokens normalizes them to be consistent.
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

		result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

		// Both tokens end up on line 1 with normalized positions.
		require.NoError(t, result.Validate())
		require.Equal(t, 1, result.Count())

		line := result.Line(0)
		require.Len(t, line.Tokens(), 2)
		assert.Equal(t, 1, line.Token(0).Position.Line)
		assert.Equal(t, 1, line.Token(1).Position.Line)
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

		result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

		// Nil positions are skipped in validation.
		assert.NoError(t, result.Validate())
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

		result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

		assert.NoError(t, result.Validate())
	})
}

func TestNewSourceFromTokens_PositionFieldsMatchLexer(t *testing.T) {
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
			lines := niceyaml.NewSourceFromTokens(originalTks)
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

func TestNewSourceFromTokens_SplitTokenOffsets(t *testing.T) {
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

			lines := niceyaml.NewSourceFromTokens(lexer.Tokenize(input))

			var prevOffset int
			for i := range lines.Count() {
				line := lines.Line(i)
				for _, tk := range line.Tokens() {
					if tk.Position != nil {
						// Offset must be strictly increasing.
						assert.Greater(t, tk.Position.Offset, prevOffset,
							"Offset not increasing at line %d", line.Number())

						prevOffset = tk.Position.Offset
					}
				}
			}
		})
	}
}

func TestNewSourceFromTokens_OffsetByteCount(t *testing.T) {
	t.Parallel()

	// Test that Offset uses byte count, not rune count.
	// UTF-8 chars like 日 are 3 bytes each.
	input := "key: 日本語\n"
	lines := niceyaml.NewSourceFromTokens(lexer.Tokenize(input))

	// Get the original lexer tokens to verify we match.
	originalTks := lexer.Tokenize(input)
	resultTks := lines.Tokens()

	// Both should have the same total byte progression.
	// Note: The lexer drops the final trailing newline from token origins,
	// so we compare our output to lexer output, not to input length.
	var origTotalBytes, resultTotalBytes int

	for _, tk := range originalTks {
		origTotalBytes += len(tk.Origin)
	}

	for _, tk := range resultTks {
		resultTotalBytes += len(tk.Origin)
	}

	assert.Equal(t, origTotalBytes, resultTotalBytes, "total bytes should match lexer output")
}

func TestNewSourceFromTokens_IndentLevelProgression(t *testing.T) {
	t.Parallel()

	input := `root:
  level1:
    level2:
      level3: value
    back2: val
  back1: val
end: val
`
	lines := niceyaml.NewSourceFromTokens(lexer.Tokenize(input))

	// Expected indent levels per line (based on go-yaml scanner behavior):
	// Line 1: root: -> level 0.
	// Line 2:   level1: -> level 1.
	// Line 3:     level2: -> level 2.
	// Line 4:       level3: value -> level 3.
	// Line 5:     back2: val -> level 2.
	// Line 6:   back1: val -> level 1.
	// Line 7: end: val -> level 0.
	expectedLevels := []int{0, 1, 2, 3, 2, 1, 0}

	require.Equal(t, len(expectedLevels), lines.Count())

	for i := range lines.Count() {
		line := lines.Line(i)
		if len(line.Tokens()) > 0 {
			firstTk := line.Token(0)
			if firstTk.Position != nil {
				assert.Equal(t, expectedLevels[i], firstTk.Position.IndentLevel,
					"IndentLevel mismatch at line %d", i+1)
			}
		}
	}
}

func TestLines_TokenPositionRanges(t *testing.T) {
	t.Parallel()

	t.Run("literal block content - returns all joined lines", func(t *testing.T) {
		t.Parallel()

		input := `key: |
  line1
  line2
`
		tks := lexer.Tokenize(input)
		result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

		require.Equal(t, 3, result.Count())

		// Query line index 1 (second line), column 0 (start of the string token).
		// The literal block content starts at column 0 (the indentation spaces are part of Origin).
		ranges := result.TokenPositionRanges(niceyaml.Position{Line: 1, Col: 0})
		require.NotNil(t, ranges, "expected ranges for joined literal block")
		require.Len(t, ranges, 2, "expected 2 ranges for 2-line literal block content")

		// Collect line indices from ranges.
		lineIdxs := make([]int, len(ranges))
		for i, r := range ranges {
			lineIdxs[i] = r.Start.Line
			// End line should match start line for single-line ranges.
			assert.Equal(t, r.Start.Line, r.End.Line)
			// End column should be greater than start column.
			assert.Greater(t, r.End.Col, r.Start.Col)
		}

		assert.ElementsMatch(t, []int{1, 2}, lineIdxs)
	})

	t.Run("non-joined line - returns range", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := lexer.Tokenize(input)
		result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

		require.Equal(t, 1, result.Count())

		// Query line index 0, column 0 (the "key" token).
		ranges := result.TokenPositionRanges(niceyaml.Position{Line: 0, Col: 0})
		require.NotNil(t, ranges)
		require.Len(t, ranges, 1)
		assert.Equal(t, 0, ranges[0].Start.Line)
		assert.Equal(t, 0, ranges[0].Start.Col)
		assert.Equal(t, 0, ranges[0].End.Line)
		assert.Equal(t, 3, ranges[0].End.Col) // "key" is 3 chars.
	})

	t.Run("non-joined line multiple tokens - returns correct range", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := lexer.Tokenize(input)
		result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

		require.Equal(t, 1, result.Count())

		// Find where the last token ("value") actually starts.
		line := result.Line(0)
		lastTokenIdx := len(line.Tokens()) - 1
		require.Positive(t, lastTokenIdx, "expected multiple tokens")

		// Calculate where the last token starts.
		var expectedCol int
		for i := range lastTokenIdx {
			tk := line.Token(i)
			expectedCol += len([]rune(strings.TrimSuffix(tk.Origin, "\n")))
		}

		// Query a column within the last token.
		ranges := result.TokenPositionRanges(niceyaml.Position{Line: 0, Col: expectedCol})
		require.NotNil(t, ranges)
		require.Len(t, ranges, 1)
		assert.Equal(t, 0, ranges[0].Start.Line)
		assert.Equal(t, expectedCol, ranges[0].Start.Col)
		assert.Equal(t, 0, ranges[0].End.Line)
		assert.Greater(t, ranges[0].End.Col, ranges[0].Start.Col)
	})

	t.Run("query indicator line of literal block - returns range", func(t *testing.T) {
		t.Parallel()

		input := `key: |
  line1
  line2
`
		tks := lexer.Tokenize(input)
		result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

		require.Equal(t, 3, result.Count())

		// Query line index 0 (indicator line), column 0 (the "key" token).
		// The indicator line itself is not part of the join, but has tokens.
		ranges := result.TokenPositionRanges(niceyaml.Position{Line: 0, Col: 0})
		require.NotNil(t, ranges)
		require.Len(t, ranges, 1)
		assert.Equal(t, 0, ranges[0].Start.Line)
		assert.Equal(t, 0, ranges[0].Start.Col)
		assert.Equal(t, 0, ranges[0].End.Line)
		assert.Equal(t, 3, ranges[0].End.Col) // "key" is 3 chars.
	})

	t.Run("query from last line of literal block", func(t *testing.T) {
		t.Parallel()

		input := `key: |
  line1
  line2
`
		tks := lexer.Tokenize(input)
		result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

		require.Equal(t, 3, result.Count())

		// Query line index 2 (last content line), column 0.
		ranges := result.TokenPositionRanges(niceyaml.Position{Line: 2, Col: 0})
		require.NotNil(t, ranges, "expected ranges when querying last joined line")
		require.Len(t, ranges, 2)

		lineIdxs := make([]int, len(ranges))
		for i, r := range ranges {
			lineIdxs[i] = r.Start.Line
			assert.Equal(t, r.Start.Line, r.End.Line)
			assert.Greater(t, r.End.Col, r.Start.Col)
		}

		assert.ElementsMatch(t, []int{1, 2}, lineIdxs)
	})

	t.Run("line index out of bounds - returns nil", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := lexer.Tokenize(input)
		result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

		// Query non-existent line index.
		ranges := result.TokenPositionRanges(niceyaml.Position{Line: 999, Col: 0})
		assert.Nil(t, ranges)

		// Negative index.
		ranges = result.TokenPositionRanges(niceyaml.Position{Line: -1, Col: 0})
		assert.Nil(t, ranges)
	})

	t.Run("column outside token range - returns nil", func(t *testing.T) {
		t.Parallel()

		input := `key: |
  line1
  line2
`
		tks := lexer.Tokenize(input)
		result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

		require.Equal(t, 3, result.Count())

		// Query line index 1 with a column that's way beyond the token.
		ranges := result.TokenPositionRanges(niceyaml.Position{Line: 1, Col: 100})
		assert.Nil(t, ranges, "expected nil for column outside token range")
	})

	t.Run("three-line literal block", func(t *testing.T) {
		t.Parallel()

		input := `key: |
  line1
  line2
  line3
`
		tks := lexer.Tokenize(input)
		result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

		require.Equal(t, 4, result.Count())

		// Query middle line (line index 2), column 0.
		ranges := result.TokenPositionRanges(niceyaml.Position{Line: 2, Col: 0})
		require.NotNil(t, ranges)
		require.Len(t, ranges, 3, "expected 3 ranges for 3-line literal block content")

		lineIdxs := make([]int, len(ranges))
		for i, r := range ranges {
			lineIdxs[i] = r.Start.Line
			assert.Equal(t, r.Start.Line, r.End.Line)
			assert.Greater(t, r.End.Col, r.Start.Col)
		}

		assert.ElementsMatch(t, []int{1, 2, 3}, lineIdxs)
	})

	t.Run("folded block", func(t *testing.T) {
		t.Parallel()

		input := `key: >
  line1
  line2
`
		tks := lexer.Tokenize(input)
		result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

		require.Equal(t, 3, result.Count())

		// Query line index 1 (first content line), column 0.
		ranges := result.TokenPositionRanges(niceyaml.Position{Line: 1, Col: 0})
		require.NotNil(t, ranges)
		require.Len(t, ranges, 2)

		lineIdxs := make([]int, len(ranges))
		for i, r := range ranges {
			lineIdxs[i] = r.Start.Line
			assert.Equal(t, r.Start.Line, r.End.Line)
			assert.Greater(t, r.End.Col, r.Start.Col)
		}

		assert.ElementsMatch(t, []int{1, 2}, lineIdxs)
	})

	t.Run("multiple positions - combined results", func(t *testing.T) {
		t.Parallel()

		input := `first: 1
second: 2
`
		tks := lexer.Tokenize(input)
		result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

		require.Equal(t, 2, result.Count())

		// Query two different positions: "first" token on line 0, "second" token on line 1.
		ranges := result.TokenPositionRanges(
			niceyaml.Position{Line: 0, Col: 0},
			niceyaml.Position{Line: 1, Col: 0},
		)
		require.NotNil(t, ranges)
		require.Len(t, ranges, 2)

		lineIdxs := make([]int, len(ranges))
		for i, r := range ranges {
			lineIdxs[i] = r.Start.Line
		}

		assert.ElementsMatch(t, []int{0, 1}, lineIdxs)
	})

	t.Run("multiple positions - deduplication", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := lexer.Tokenize(input)
		result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

		require.Equal(t, 1, result.Count())

		// Query the same position twice.
		ranges := result.TokenPositionRanges(
			niceyaml.Position{Line: 0, Col: 0},
			niceyaml.Position{Line: 0, Col: 0},
		)
		require.NotNil(t, ranges)
		require.Len(t, ranges, 1, "expected duplicates to be removed")

		assert.Equal(t, 0, ranges[0].Start.Line)
		assert.Equal(t, 0, ranges[0].Start.Col)
		assert.Equal(t, 0, ranges[0].End.Line)
		assert.Equal(t, 3, ranges[0].End.Col)
	})

	t.Run("multiple positions - empty input", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := lexer.Tokenize(input)
		result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

		// Query with no positions.
		ranges := result.TokenPositionRanges()
		assert.Nil(t, ranges)
	})

	t.Run("multiple positions - joined lines deduplication", func(t *testing.T) {
		t.Parallel()

		input := `key: |
  line1
  line2
`
		tks := lexer.Tokenize(input)
		result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

		require.Equal(t, 3, result.Count())

		// Query both content lines of the same joined block.
		// Both positions are within the joined block, so they should return
		// the same ranges (deduplicated).
		ranges := result.TokenPositionRanges(
			niceyaml.Position{Line: 1, Col: 0},
			niceyaml.Position{Line: 2, Col: 0},
		)
		require.NotNil(t, ranges)
		require.Len(t, ranges, 2, "expected 2 unique ranges from joined block")

		lineIdxs := make([]int, len(ranges))
		for i, r := range ranges {
			lineIdxs[i] = r.Start.Line
		}

		assert.ElementsMatch(t, []int{1, 2}, lineIdxs)
	})
}

func TestLines_PositionsFromToken(t *testing.T) {
	t.Parallel()

	t.Run("single occurrence", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := lexer.Tokenize(input)
		result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

		require.Equal(t, 1, result.Count())

		// Get the "key" token.
		tk := result.Line(0).Token(0)
		positions := result.PositionsFromToken(tk)

		require.Len(t, positions, 1)
		assert.Equal(t, 0, positions[0].Line)
		assert.Equal(t, 0, positions[0].Col)
	})

	t.Run("multiple occurrences - literal block", func(t *testing.T) {
		t.Parallel()

		input := `key: |
  line1
  line2
`
		tks := lexer.Tokenize(input)
		result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

		require.Equal(t, 3, result.Count())

		// Get the content token from the first content line.
		tk := result.Line(1).Token(0)
		positions := result.PositionsFromToken(tk)

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
		result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

		positions := result.PositionsFromToken(nil)
		assert.Nil(t, positions)
	})

	t.Run("token not in lines", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := lexer.Tokenize(input)
		result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

		// Create a different token that's not in the Lines.
		otherTks := lexer.Tokenize("other: data\n")
		externalToken := otherTks[0]

		positions := result.PositionsFromToken(externalToken)
		assert.Nil(t, positions)
	})

	t.Run("token with column offset", func(t *testing.T) {
		t.Parallel()

		input := "key: value\n"
		tks := lexer.Tokenize(input)
		result := niceyaml.NewSourceFromTokens(tks, niceyaml.WithName("test"))

		// Get the last token ("value").
		line := result.Line(0)
		lastTk := line.Token(len(line.Tokens()) - 1)
		positions := result.PositionsFromToken(lastTk)

		require.Len(t, positions, 1)
		assert.Equal(t, 0, positions[0].Line)
		// Column should be > 0 since it's not the first token.
		assert.Positive(t, positions[0].Col)
	})
}
