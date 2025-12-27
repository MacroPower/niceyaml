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
			result := niceyaml.NewLinesFromTokens(input, niceyaml.WithName("test"))
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
			result := niceyaml.NewLinesFromTokens(input, niceyaml.WithName("test"))

			require.Equal(t, len(tc.wantLines), result.LineCount(), "wrong number of lines")

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

			result := niceyaml.NewLinesFromTokens(tks, niceyaml.WithName("test"))

			require.Equal(t, tc.wantLineCount, result.LineCount(), "wrong number of lines")

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
			result := niceyaml.NewLinesFromTokens(tks, niceyaml.WithName("test"))

			require.Equal(t, tc.wantLineCount, result.LineCount(), "wrong number of lines")

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
			result := niceyaml.NewLinesFromTokens(tks, niceyaml.WithName("test"))
			require.Equal(t, 1, result.LineCount())

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
			result := niceyaml.NewLinesFromTokens(tks, niceyaml.WithName("test"))

			// Apply annotations to specified lines.
			for idx, ann := range tc.annotations {
				require.Less(t, idx, result.LineCount(), "annotation index out of range")

				result.Annotate(idx, ann)
			}

			assert.Equal(t, tc.want, result.String())
		})
	}
}

func TestLine_Clone_Annotation(t *testing.T) {
	t.Parallel()

	tks := lexer.Tokenize("key: value\n")
	result := niceyaml.NewLinesFromTokens(tks, niceyaml.WithName("test"))
	require.Equal(t, 1, result.LineCount())

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
			result := niceyaml.NewLinesFromTokens(input, niceyaml.WithName("test"))

			// Call Value() to establish Prev/Next linking across lines.
			tks := result.Tokens()
			require.NotEmpty(t, tks, "expected non-empty tokens")

			// Count total tokens across all lines.
			totalTokens := 0
			result.EachLine(func(_ int, line niceyaml.Line) {
				totalTokens += len(line.Tokens())
			})

			// Start from first token in Line(0).Value.
			firstToken := result.Line(0).Token(0)
			lastLine := result.Line(result.LineCount() - 1)
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

func TestTokens_Slice(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input        string
		wantContent  string
		wantLineNums []int
		minLine      int
		maxLine      int
	}{
		"unbounded - all lines": {
			input: `first: 1
second: 2
third: 3
`,
			minLine:      -1,
			maxLine:      -1,
			wantLineNums: []int{1, 2, 3},
			wantContent:  "first: 1\nsecond: 2\nthird: 3",
		},
		"bounded first half": {
			input: `first: 1
second: 2
third: 3
fourth: 4
`,
			minLine:      1,
			maxLine:      2,
			wantLineNums: []int{1, 2},
			wantContent:  "first: 1\nsecond: 2",
		},
		"bounded second half": {
			input: `first: 1
second: 2
third: 3
fourth: 4
`,
			minLine:      3,
			maxLine:      4,
			wantLineNums: []int{3, 4},
			wantContent:  "third: 3\nfourth: 4",
		},
		"bounded middle": {
			input: `first: 1
second: 2
third: 3
fourth: 4
fifth: 5
`,
			minLine:      2,
			maxLine:      4,
			wantLineNums: []int{2, 3, 4},
			wantContent:  "second: 2\nthird: 3\nfourth: 4",
		},
		"unbounded min": {
			input: `first: 1
second: 2
third: 3
`,
			minLine:      -1,
			maxLine:      2,
			wantLineNums: []int{1, 2},
			wantContent:  "first: 1\nsecond: 2",
		},
		"unbounded max": {
			input: `first: 1
second: 2
third: 3
`,
			minLine:      2,
			maxLine:      -1,
			wantLineNums: []int{2, 3},
			wantContent:  "second: 2\nthird: 3",
		},
		"single line": {
			input: `first: 1
second: 2
third: 3
`,
			minLine:      2,
			maxLine:      2,
			wantLineNums: []int{2},
			wantContent:  "second: 2",
		},
		"no lines in range": {
			input: `first: 1
second: 2
`,
			minLine:      10,
			maxLine:      20,
			wantLineNums: []int{},
			wantContent:  "",
		},
		"range beyond end": {
			input: `first: 1
second: 2
`,
			minLine:      1,
			maxLine:      100,
			wantLineNums: []int{1, 2},
			wantContent:  "first: 1\nsecond: 2",
		},
		"literal block sliced": {
			input: `before: key
script: |
  line1
  line2
after: key
`,
			minLine:      2,
			maxLine:      4,
			wantLineNums: []int{2, 3, 4},
			wantContent:  "script: |\n  line1\n  line2",
		},
		"nested structure sliced": {
			input: `parent:
  child1: val1
  child2: val2
  child3: val3
`,
			minLine:      2,
			maxLine:      3,
			wantLineNums: []int{2, 3},
			wantContent:  "  child1: val1\n  child2: val2",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := lexer.Tokenize(tc.input)
			full := niceyaml.NewLinesFromTokens(tks, niceyaml.WithName("test"))

			sliced := full.Slice(tc.minLine, tc.maxLine)

			// Verify line numbers.
			gotLineNums := make([]int, sliced.LineCount())
			sliced.EachLine(func(i int, line niceyaml.Line) {
				gotLineNums[i] = line.Number()
			})

			assert.Equal(t, tc.wantLineNums, gotLineNums)

			// Verify content.
			assert.Equal(t, tc.wantContent, sliced.Content())

			// Verify name is preserved.
			assert.Equal(t, "test", sliced.Name)
		})
	}
}

func TestTokens_Slice_Clones(t *testing.T) {
	t.Parallel()

	tks := lexer.Tokenize("key: value\n")
	original := niceyaml.NewLinesFromTokens(tks, niceyaml.WithName("test"))

	sliced := original.Slice(-1, -1)

	require.Equal(t, 1, sliced.LineCount())

	// Verify that sliced is independent - modifying a cloned line doesn't affect original.
	// Since Line() returns a copy, we verify by checking the lines are separate objects.
	slicedLine := sliced.Line(0)
	originalLine := original.Line(0)

	// Content should match.
	assert.Equal(t, originalLine.Content(), slicedLine.Content())
	assert.Equal(t, originalLine.Number(), slicedLine.Number())
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
			result := niceyaml.NewLinesFromTokens(tks, niceyaml.WithName("test"))

			// Verify line numbers are strictly increasing.
			require.NoError(t, result.Validate(), "tokens should be valid")

			// Verify expected line numbers.
			require.Equal(t, len(tc.wantLineNums), result.LineCount(), "wrong number of lines")

			for i, wantNum := range tc.wantLineNums {
				assert.Equal(t, wantNum, result.Line(i).Number(), "line %d has wrong number", i)
			}
		})
	}
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
				result := niceyaml.NewLinesFromTokens(tks, niceyaml.WithName("test"))

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

		result := niceyaml.NewLinesFromTokens(tks, niceyaml.WithName("test"))

		// After normalization, line numbers are sequential.
		require.NoError(t, result.Validate())
		require.Equal(t, 2, result.LineCount())
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

		result := niceyaml.NewLinesFromTokens(tks, niceyaml.WithName("test"))

		// After normalization, line numbers are sequential.
		require.NoError(t, result.Validate())
		require.Equal(t, 2, result.LineCount())
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

		result := niceyaml.NewLinesFromTokens(tks, niceyaml.WithName("test"))

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

		result := niceyaml.NewLinesFromTokens(tks, niceyaml.WithName("test"))

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

		result := niceyaml.NewLinesFromTokens(tks, niceyaml.WithName("test"))

		// Both tokens end up on line 1 with normalized positions.
		require.NoError(t, result.Validate())
		require.Equal(t, 1, result.LineCount())

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

		result := niceyaml.NewLinesFromTokens(tks, niceyaml.WithName("test"))

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

		result := niceyaml.NewLinesFromTokens(tks, niceyaml.WithName("test"))

		assert.NoError(t, result.Validate())
	})
}
