package segment_test

import (
	"math"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/segment"
	"go.jacobcolvin.com/niceyaml/tokens"
)

// lineContents returns the content of every line without line endings.
func lineContents(lines []segment.Line) []string {
	result := make([]string, 0, len(lines))

	for _, l := range lines {
		var sb strings.Builder

		for _, seg := range l.Segments {
			sb.WriteString(tokens.TrimLineEnding(seg.Part().Origin))
		}

		result = append(result, sb.String())
	}

	return result
}

// lineNumbers returns the Number of every line.
func lineNumbers(lines []segment.Line) []int {
	result := make([]int, 0, len(lines))

	for _, l := range lines {
		result = append(result, l.Number)
	}

	return result
}

// part returns the part token at index idx on line li.
func part(t *testing.T, lines []segment.Line, li, idx int) *token.Token {
	t.Helper()

	require.Less(t, li, len(lines), "line index")
	require.Less(t, idx, len(lines[li].Segments), "segment index on line %d", li)

	return lines[li].Segments[idx].Part()
}

func TestSplit_DuplicateNewline(t *testing.T) {
	t.Parallel()

	// The lexer repeats the newline that ends a tag at the start of the next
	// token. Split must attach the repeat to the finished line and still keep
	// one line per source line, including real blank lines that follow it.
	// The lone newline an empty block scalar holds is no such repeat, so it
	// opens a line of its own.
	tcs := map[string]struct {
		input       string
		wantContent []string
		wantNumbers []int
	}{
		"tag then indented key": {
			input:       "a: !t\n  b: 1\n",
			wantContent: []string{"a: !t", "  b: 1"},
			wantNumbers: []int{1, 2},
		},
		"tag then blank line then sequence": {
			input:       "a: !!seq\n\n  - b\n",
			wantContent: []string{"a: !!seq", "", "  - b"},
			wantNumbers: []int{1, 2, 3},
		},
		"document header tag then blank line": {
			input:       "--- !!map\n\na: 1\n",
			wantContent: []string{"--- !!map", "", "a: 1"},
			wantNumbers: []int{1, 2, 3},
		},
		"nested tag then blank line": {
			input:       "a:\n  t: !custom!i18n\n\n    en: x\n",
			wantContent: []string{"a:", "  t: !custom!i18n", "", "    en: x"},
			wantNumbers: []int{1, 2, 3, 4},
		},
		"two blank lines after tag": {
			input:       "a: !t\n\n\n  b: 1\n",
			wantContent: []string{"a: !t", "", "", "  b: 1"},
			wantNumbers: []int{1, 2, 3, 4},
		},
		"folded scalar with content after it": {
			input:       "a: >-\n  long\n  folded\nb: 2\n",
			wantContent: []string{"a: >-", "  long", "  folded", "b: 2"},
			wantNumbers: []int{1, 2, 3, 4},
		},
		"literal scalar at the end": {
			input:       "a: |-\n  x\n  y\n",
			wantContent: []string{"a: |-", "  x", "  y"},
			wantNumbers: []int{1, 2, 3},
		},
		"empty literal scalar": {
			input:       "a: |\n\n",
			wantContent: []string{"a: |", ""},
			wantNumbers: []int{1, 2},
		},
		"empty strip literal scalar": {
			input:       "a: |-\n\n\n",
			wantContent: []string{"a: |-", "", ""},
			wantNumbers: []int{1, 2, 3},
		},
		"empty folded scalar": {
			input:       "a: >\n\n",
			wantContent: []string{"a: >", ""},
			wantNumbers: []int{1, 2},
		},
		"empty literal scalar with indentation indicator": {
			input:       "a: |2\n\n\n",
			wantContent: []string{"a: |2", "", ""},
			wantNumbers: []int{1, 2, 3},
		},
		"empty strip literal scalar with indentation indicator": {
			input:       "- |1-\n\n",
			wantContent: []string{"- |1-", ""},
			wantNumbers: []int{1, 2},
		},
		"empty literal scalar with indentation indicator and crlf": {
			input:       ":\n|1\r\n\r\n",
			wantContent: []string{":", "|1", ""},
			wantNumbers: []int{1, 2, 3},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := segment.Split(tokens.Tokenize(tc.input))

			assert.Equal(t, tc.wantContent, lineContents(lines))
			assert.Equal(t, tc.wantNumbers, lineNumbers(lines))

			// Every part on a line reports that line's number, so the repeated
			// newline landed on the line it belongs to.
			for _, l := range lines {
				for _, seg := range l.Segments {
					assert.Equal(t, l.Number, seg.Part().Position.Line, "part %q", seg.Part().Origin)
				}
			}
		})
	}
}

func TestSplit_LineEndings(t *testing.T) {
	t.Parallel()

	// The lexer advances Position.Line on "\n", "\r\n", and a bare "\r", and
	// Split cuts lines at the same three endings. Where the lexer spreads one
	// CRLF over two tokens, the two halves stay on one line, and the line
	// the lexer skipped there stays out of every later line number, however
	// many such splits a file holds.
	tcs := map[string]struct {
		input       string
		wantContent []string
		wantNumbers []int
	}{
		"bare cr between keys": {
			input:       "a: 1\rb: 2\r",
			wantContent: []string{"a: 1", "b: 2"},
			wantNumbers: []int{1, 2},
		},
		"bare cr blank line": {
			input:       "a: 1\r\rb: 2\r",
			wantContent: []string{"a: 1", "", "b: 2"},
			wantNumbers: []int{1, 2, 3},
		},
		"bare cr block scalar": {
			input:       "k: |\r  a\r  b\rz: 1\r",
			wantContent: []string{"k: |", "  a", "  b", "z: 1"},
			wantNumbers: []int{1, 2, 3, 4},
		},
		"bare cr after comment": {
			input:       "# c\rk: v\r",
			wantContent: []string{"# c", "k: v"},
			wantNumbers: []int{1, 2},
		},
		"crlf split after comment": {
			input:       "a: b # c\r\nd: e\r\n",
			wantContent: []string{"a: b # c", "d: e"},
			wantNumbers: []int{1, 2},
		},
		"crlf between two comments": {
			input:       "# a\r\n# b\r\nc: 1\r\n",
			wantContent: []string{"# a", "# b", "c: 1"},
			wantNumbers: []int{1, 2, 3},
		},
		"crlf between three comments": {
			input:       "# a\r\n# b\r\n# c\r\nd: 1\r\n",
			wantContent: []string{"# a", "# b", "# c", "d: 1"},
			wantNumbers: []int{1, 2, 3, 4},
		},
		"crlf blank line between comments": {
			input:       "# a\r\n\r\n# b\r\nc: 1\r\n",
			wantContent: []string{"# a", "", "# b", "c: 1"},
			wantNumbers: []int{1, 2, 3, 4},
		},
		"crlf repeated after tag": {
			input:       "a: !t\r\n  b: 1\r\n",
			wantContent: []string{"a: !t", "  b: 1"},
			wantNumbers: []int{1, 2},
		},
		"crlf blank line": {
			input:       "key: value\r\n\r\nnext: data\r\n",
			wantContent: []string{"key: value", "", "next: data"},
			wantNumbers: []int{1, 2, 3},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := segment.Split(tokens.Tokenize(tc.input))

			assert.Equal(t, tc.wantContent, lineContents(lines))
			assert.Equal(t, tc.wantNumbers, lineNumbers(lines))

			// Every part on a line reports that line's number.
			for _, l := range lines {
				for _, seg := range l.Segments {
					assert.Equal(t, l.Number, seg.Part().Position.Line, "part %q", seg.Part().Origin)
				}
			}
		})
	}
}

func TestSplit_BlankLinesBeforeExplicitKey(t *testing.T) {
	t.Parallel()

	// The lexer drops the blank lines between a comment, a quoted scalar,
	// or a flow collection and a "?" or ":" indicator after it, and a
	// blank line from a run of them after a double-quoted scalar that
	// holds a tab. Tokenize gives them back with the spaces they hold, so
	// Split keeps every line, its number, and its content.
	tcs := map[string]struct {
		input       string
		wantContent []string
		wantNumbers []int
	}{
		"key after a comment": {
			input:       "# c\n\n? b\n: c\n",
			wantContent: []string{"# c", "", "? b", ": c"},
			wantNumbers: []int{1, 2, 3, 4},
		},
		"key after a quoted scalar": {
			input:       "a: 'x'\n\n? b\n: c\n",
			wantContent: []string{"a: 'x'", "", "? b", ": c"},
			wantNumbers: []int{1, 2, 3, 4},
		},
		"key after a flow sequence": {
			input:       "tags: [a, b]\n\n? key\n: value\n",
			wantContent: []string{"tags: [a, b]", "", "? key", ": value"},
			wantNumbers: []int{1, 2, 3, 4},
		},
		"nested key after a flow sequence": {
			input:       "a:\n  b: [x]\n\n  ? k\n  : v\n",
			wantContent: []string{"a:", "  b: [x]", "", "  ? k", "  : v"},
			wantNumbers: []int{1, 2, 3, 4, 5},
		},
		"key after blank lines": {
			input:       "\n\n? b\n: c\n",
			wantContent: []string{"", "", "? b", ": c"},
			wantNumbers: []int{1, 2, 3, 4},
		},
		"crlf key after a comment": {
			input:       "# c\r\n\r\n? b\r\n",
			wantContent: []string{"# c", "", "? b"},
			wantNumbers: []int{1, 2, 3},
		},
		"crlf key after a blank line the lexer rewrites": {
			input:       "x:\r\n  a: \"t\tb\"\r\n \t\r\n  \r\n? b\r\n",
			wantContent: []string{"x:", "  a: \"t\tb\"", " \t", "", "? b"},
			wantNumbers: []int{1, 2, 3, 4, 5},
		},
		"plain scalar after a blank line of spaces": {
			input:       "t: \"a\tb\"\n  \n  r'\n- e\n",
			wantContent: []string{"t: \"a\tb\"", "  ", "  r'", "- e"},
			wantNumbers: []int{1, 2, 3, 4},
		},
		"plain scalar after blank lines of spaces": {
			input:       "t: \"a\tb\"\n  \n  \n  r'\n- e\n",
			wantContent: []string{"t: \"a\tb\"", "  ", "  ", "  r'", "- e"},
			wantNumbers: []int{1, 2, 3, 4, 5},
		},
		"key after blank lines of spaces": {
			input:       "a: \"t\tb\"\n  \n  \nc: 1\n",
			wantContent: []string{"a: \"t\tb\"", "  ", "  ", "c: 1"},
			wantNumbers: []int{1, 2, 3, 4},
		},
		"comment after blank lines of spaces": {
			input:       "a: \"t\tb\"\n  \n  \n# c\nd: 1\ne: 2\n",
			wantContent: []string{"a: \"t\tb\"", "  ", "  ", "# c", "d: 1", "e: 2"},
			wantNumbers: []int{1, 2, 3, 4, 5, 6},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := segment.Split(tokens.Tokenize(tc.input))

			assert.Equal(t, tc.wantContent, lineContents(lines))
			assert.Equal(t, tc.wantNumbers, lineNumbers(lines))

			// Every part on a line reports that line's number.
			for _, l := range lines {
				for _, seg := range l.Segments {
					assert.Equal(t, l.Number, seg.Part().Position.Line, "part %q", seg.Part().Origin)
				}
			}
		})
	}
}

func TestSplit_CRLFSplitOffset(t *testing.T) {
	t.Parallel()

	// The lexer closes a comment with "\r" and opens the next token with
	// "\n". That "\n" is a new rune, so it advances the offset by one, while
	// the "\r" a tag repeats before "\r\n" does not.
	lines := segment.Split(tokens.Tokenize("# c\r\nk: v\r\n"))
	require.Len(t, lines, 2)

	nl := part(t, lines, 0, 1)
	assert.Equal(t, "\n", nl.Origin)
	assert.Equal(t, 5, nl.Position.Offset)

	lines = segment.Split(tokens.Tokenize("a: !t\r\n  b: 1\r\nc: 'x\r\n  y'\r\n"))
	require.Len(t, lines, 4)

	dup := part(t, lines, 0, 3)
	assert.Equal(t, "\r\n", dup.Origin)
	assert.Equal(t, 6, dup.Position.Offset, "the repeat shares the tag's \\r offset")

	// "a: !t\r\n" (7) + "  b: 1\r\n" (8) + "c: 'x\r\n" (7) puts the
	// continuation at 1-indexed offset 23. Tokenize gives the final line
	// ending back to the last token, so the part carries it.
	cont := part(t, lines, 3, 0)
	assert.Equal(t, "  y'\r\n", cont.Origin)
	assert.Equal(t, 23, cont.Position.Offset)
}

func TestSplit_NewlineColumn(t *testing.T) {
	t.Parallel()

	// A pure-newline part starts just past the parts on its line, so the
	// newline a tag repeats sits one column past the tag, or in column 1
	// on a blank line.
	tcs := map[string]struct {
		input string
		line  int // 0-indexed line holding the newline part.
		idx   int // Segment index of the newline part on that line.
		want  int
	}{
		"repeated newline after a tag": {
			input: "a: !t\n  b: 1\n",
			line:  0,
			idx:   3,
			want:  6,
		},
		"repeated newline after a long tag": {
			input: "a: !!map\n  b: 1\n",
			line:  0,
			idx:   3,
			want:  9,
		},
		"blank line inside a block scalar": {
			input: "k: |\n  a\n\n  b\nz: 1\n",
			line:  2,
			idx:   0,
			want:  1,
		},
		"blank line after a tag": {
			input: "a: !!seq\n\n  - b\n",
			line:  1,
			idx:   0,
			want:  1,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := segment.Split(tokens.Tokenize(tc.input))
			nl := part(t, lines, tc.line, tc.idx)

			require.Equal(t, "\n", nl.Origin)
			assert.Equal(t, tc.want, nl.Position.Column)

			// The newline follows every other part on its line.
			for _, seg := range lines[tc.line].Segments[:tc.idx] {
				assert.Less(t, seg.Part().Position.Column, nl.Position.Column, "part %q", seg.Part().Origin)
			}
		})
	}
}

func TestSplit_DuplicateNewlineOffset(t *testing.T) {
	t.Parallel()

	// The repeated newline is the same rune the previous token already
	// counted, so it must not push later offsets forward.
	lines := segment.Split(tokens.Tokenize("a: !t\n  b: 1\nc: \"x\n  y\"\nd: 1\n"))
	require.Len(t, lines, 5)

	dup := part(t, lines, 0, 3)
	assert.Equal(t, "\n", dup.Origin)
	assert.Equal(t, 6, dup.Position.Offset, "the repeat shares the tag newline's offset")

	// "a: !t\n" (6) + "  b: 1\n" (7) + "c: \"x\n" (6) puts the continuation
	// at 1-indexed offset 20.
	cont := part(t, lines, 3, 0)
	assert.Equal(t, "  y\"", cont.Origin)
	assert.Equal(t, 20, cont.Position.Offset)
}

func TestSplit_MovedPartOffset(t *testing.T) {
	t.Parallel()

	// A part Split moves onto the line finished last names the rune it
	// starts with, the same rune its Line and Column name, even when the
	// previous token already counted that rune. The parts after it keep
	// the offsets the source gives them.
	tcs := map[string]struct {
		input  string
		origin string // Origin of the part.
		line   int    // 0-indexed line holding the part.
		idx    int    // Segment index of the part on that line.
		want   int
	}{
		"newline repeated after a tag": {
			input:  "a: !t\n\n\n  b: 1\n",
			origin: "\n",
			line:   0,
			idx:    3,
			want:   6,
		},
		"blank line after a repeated newline": {
			input:  "a: !t\n\n\n  b: 1\n",
			origin: "\n",
			line:   1,
			idx:    0,
			want:   7,
		},
		"crlf repeated after a tag": {
			input:  "a: !t\r\n  b: 1\r\n",
			origin: "\r\n",
			line:   0,
			idx:    3,
			want:   6,
		},
		"empty content of a keep block scalar": {
			input:  "a: |+\n",
			origin: "",
			line:   0,
			idx:    3,
			want:   6,
		},
		"newline of a crlf cut after a comment": {
			input:  "# c\r\nk: v\r\n",
			origin: "\n",
			line:   0,
			idx:    1,
			want:   5,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := segment.Split(tokens.Tokenize(tc.input))
			p := part(t, lines, tc.line, tc.idx)

			require.Equal(t, tc.origin, p.Origin)
			assert.Equal(t, tc.want, p.Position.Offset)
			assert.LessOrEqual(t, p.Position.Offset, utf8.RuneCountInString(tc.input))
		})
	}
}

func TestSplit_BlockScalarOffsets(t *testing.T) {
	t.Parallel()

	// The first part of a block scalar names the rune where its text
	// starts, and every later part the rune where the line starts, so the
	// offsets increase down the scalar whether content follows it or not.
	tcs := map[string]struct {
		input string
		want  []int // Offset of the first part on each block scalar line.
	}{
		"blank line inside, content follows": {
			input: "k: |\n  a\n\n  b\nz: 1\n",
			want:  []int{8, 10, 11},
		},
		"content follows": {
			input: "k: |\n  a\n  b\nz: 1\n",
			want:  []int{8, 10},
		},
		"at end of input": {
			input: "k: |\n  a\n  b\n",
			want:  []int{8, 10},
		},
		"three lines at end of input": {
			input: "k: |\n  a\n  b\n  c\n",
			want:  []int{8, 10, 14},
		},
		"three lines, content follows": {
			input: "k: |\n  a\n  b\n  c\nz: 1\n",
			want:  []int{8, 10, 14},
		},
		"quoted, three lines": {
			input: "k: \"a\n  b\n  c\"\n",
			want:  []int{7, 11},
		},
		"plain, three lines": {
			input: "k: a\n  b\n  c\n",
			want:  []int{6, 10},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := segment.Split(tokens.Tokenize(tc.input))
			require.Greater(t, len(lines), len(tc.want))

			got := make([]int, 0, len(tc.want))
			for i := range tc.want {
				got = append(got, part(t, lines, i+1, 0).Position.Offset)
			}

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSplit_OffsetsIncrease(t *testing.T) {
	t.Parallel()

	// Offsets grow from part to part through the whole stream, across the
	// tokens the lexer positions short, such as those after a comment or a
	// tag, and across a CRLF it cuts between two tokens.
	tcs := map[string]string{
		"comments":          "# head\na: 1 # line\n# foot\nb: 2\n",
		"tags":              "t: !!str s\nu: !t\n  v: 1\nw: !!seq\n\n  - x\n",
		"crlf":              "key: value\r\n# c\r\nnext: 1\r\n",
		"crlf tag":          "a: !t\r\n  b: 1\r\nc: 'x\r\n  y'\r\n",
		"block scalars":     "k: |\n  a\n\n  b\nz: >\n  c\n  d\n",
		"trailing spaces":   "a: 1   \nb: 2  \n",
		"folded blank line": "a:\n   \n  b: 1\n",
	}

	for name, input := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := segment.Split(tokens.Tokenize(input))
			require.NotEmpty(t, lines)

			prev := 0

			for _, l := range lines {
				for _, seg := range l.Segments {
					p := seg.Part()

					// The newline a tag repeats and the "\n" of a cut CRLF
					// hold no new rune, so they may share an offset.
					if isPureNewline(p.Origin) {
						assert.GreaterOrEqual(t, p.Position.Offset, prev, "part %q on line %d", p.Origin, l.Number)
					} else {
						assert.Greater(t, p.Position.Offset, prev, "part %q on line %d", p.Origin, l.Number)
					}

					prev = p.Position.Offset
				}
			}
		})
	}
}

func isPureNewline(s string) bool {
	return s == "\n" || s == "\r\n" || s == "\r"
}

func TestSplit_FirstTextPartKeepsPosition(t *testing.T) {
	t.Parallel()

	// The first part of a token that holds text carries the token's own
	// Line, Column, and Offset, whatever whitespace and line endings the
	// Origin opens with and whatever the lexer dropped before it.
	tcs := map[string]string{
		"trailing spaces":      "a: 1   \nb: 2\n",
		"comment then header":  "# comment\n---\nb: two\n",
		"tag":                  "t: !!str s\nu: !t\n  v: 1\n",
		"block scalar":         "k: |\n    hello\n    world\nz: 1\n",
		"leading blank line":   "k: |\n\n  b\nz: 1\n",
		"collapsed blank line": "a:\n   \n  b: 1\n",
		"crlf":                 "key: value\r\n# c\r\nnext: 1\r\n",
		"quoted multi-line":    "a: 'x\n\n  y'\nb: 1\n",
	}

	for name, input := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			seen := map[*token.Token]bool{}

			for _, l := range segment.Split(tokens.Tokenize(input)) {
				for _, seg := range l.Segments {
					src, p := seg.Source(), seg.Part()
					if seen[src] || strings.TrimSpace(p.Origin) == "" {
						continue
					}

					seen[src] = true

					assert.Equal(t, src.Position.Line, p.Position.Line, "part %q line", p.Origin)
					assert.Equal(t, src.Position.Column, p.Position.Column, "part %q column", p.Origin)
					assert.Equal(t, src.Position.Offset, p.Position.Offset, "part %q offset", p.Origin)
				}
			}
		})
	}
}

func TestSplit_PartsTakeLineIndent(t *testing.T) {
	t.Parallel()

	// Every part of a line carries the indentation of that line, whatever
	// its token carries. The empty content of a block scalar holds no rune,
	// so the part after it sets the indentation of the line it opens.
	tcs := map[string]struct {
		input           string
		line            int
		wantOrigins     []string
		wantIndentNum   int
		wantIndentLevel int
	}{
		"comment after multiline quoted scalar": {
			// The lexer gives the comment an IndentNum of 1 and an
			// IndentLevel of 0, which match no line of the source.
			input:           "a:\n  k: 'a\n    b' # c\n  z: 1\n",
			line:            2,
			wantOrigins:     []string{"    b'", " # c\n"},
			wantIndentNum:   4,
			wantIndentLevel: 2,
		},
		"empty block scalar before key": {
			input:           "x:\n  a: |\n  b: 1\n",
			line:            2,
			wantOrigins:     []string{"", "  b", ":", " 1\n"},
			wantIndentNum:   2,
			wantIndentLevel: 1,
		},
		"empty block scalar before comment": {
			input:           "x:\n  a: |\n  # c\n  b: 1\n",
			line:            2,
			wantOrigins:     []string{"", "  # c\n"},
			wantIndentNum:   2,
			wantIndentLevel: 1,
		},
		"empty block scalar before sequence entry": {
			input:           "x:\n  - a: |-\n  - b\n",
			line:            2,
			wantOrigins:     []string{"", "  -", " b\n"},
			wantIndentNum:   2,
			wantIndentLevel: 1,
		},
		"empty block scalar past end of source": {
			// The empty content joins the last line of the source.
			input:           "x:\n  a: |+\n",
			line:            1,
			wantOrigins:     []string{"  a", ":", " |+\n", ""},
			wantIndentNum:   2,
			wantIndentLevel: 1,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := segment.Split(tokens.Tokenize(tc.input))
			require.Greater(t, len(lines), tc.line)

			origins := make([]string, 0, len(lines[tc.line].Segments))
			for _, seg := range lines[tc.line].Segments {
				p := seg.Part()
				origins = append(origins, p.Origin)

				assert.Equal(t, tc.wantIndentNum, p.Position.IndentNum, "part %q IndentNum", p.Origin)
				assert.Equal(t, tc.wantIndentLevel, p.Position.IndentLevel, "part %q IndentLevel", p.Origin)
			}

			assert.Equal(t, tc.wantOrigins, origins)
		})
	}
}

func TestSplit_BlockScalarTrailingIndent(t *testing.T) {
	t.Parallel()

	// The lexer bundles the indentation of the line after a block scalar into
	// the scalar's Origin ("    x\n\n  "). That fragment is not content, so the
	// Value and the original Position stay on the last content line.
	lines := segment.Split(tokens.Tokenize("a:\n  k: |\n    x\n\n  # c\n  z: 1\n"))
	require.Equal(t, []string{"a:", "  k: |", "    x", "", "  # c", "  z: 1"}, lineContents(lines))

	content := part(t, lines, 2, 0)
	assert.Equal(t, "    x\n", content.Origin)
	assert.Equal(t, "x\n", content.Value)
	assert.Equal(t, 3, content.Position.Line)
	assert.Equal(t, 5, content.Position.Column)

	indent := part(t, lines, 4, 0)
	assert.Equal(t, "  ", indent.Origin)
	assert.Empty(t, indent.Value)
	assert.Equal(t, 5, indent.Position.Line)

	comment := part(t, lines, 4, 1)
	assert.Equal(t, token.CommentType, comment.Type)
	assert.Greater(t, comment.Position.Column, indent.Position.Column)
}

func TestSplit_HandBuiltStream(t *testing.T) {
	t.Parallel()

	// A caller can compose a stream itself rather than take one from the
	// lexer, so Split tolerates what the lexer never emits.
	pair := tokens.Tokenize("a: 1\nb: 2\n")
	require.NotEmpty(t, pair)

	tcs := map[string]struct {
		input       token.Tokens
		wantContent []string
		wantNumbers []int
	}{
		"nil alone": {
			input:       token.Tokens{nil},
			wantContent: []string{},
			wantNumbers: []int{},
		},
		"nil tokens only": {
			input:       token.Tokens{nil, nil},
			wantContent: []string{},
			wantNumbers: []int{},
		},
		"nil before the stream": {
			input:       append(token.Tokens{nil}, pair...),
			wantContent: []string{"a: 1", "b: 2"},
			wantNumbers: []int{1, 2},
		},
		"nil after the stream": {
			input:       append(slices.Clone(pair), nil),
			wantContent: []string{"a: 1", "b: 2"},
			wantNumbers: []int{1, 2},
		},
		"nil inside the stream": {
			input:       slices.Insert(slices.Clone(pair), 2, nil),
			wantContent: []string{"a: 1", "b: 2"},
			wantNumbers: []int{1, 2},
		},
		"line number below one": {
			input: token.Tokens{{
				Type:     token.StringType,
				Value:    "a",
				Origin:   "a",
				Position: &token.Position{Line: -5, Column: 1, Offset: 1},
			}},
			wantContent: []string{"a"},
			wantNumbers: []int{-5},
		},
		"line number at the int limit": {
			input: token.Tokens{
				{
					Type:     token.StringType,
					Value:    "a",
					Origin:   "a\n",
					Position: &token.Position{Line: 1, Column: 1, Offset: 1},
				},
				{
					Type:     token.StringType,
					Value:    "b",
					Origin:   "b",
					Position: &token.Position{Line: math.MaxInt, Column: 1, Offset: 3},
				},
			},
			wantContent: []string{"a", "b"},
			wantNumbers: []int{1, math.MaxInt},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := segment.Split(tc.input)

			assert.Equal(t, tc.wantContent, lineContents(lines))
			assert.Equal(t, tc.wantNumbers, lineNumbers(lines))
		})
	}
}
