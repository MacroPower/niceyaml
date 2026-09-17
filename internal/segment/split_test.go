package segment_test

import (
	"strings"
	"testing"

	"github.com/goccy/go-yaml/lexer"
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
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := segment.Split(lexer.Tokenize(tc.input))

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
	// CRLF over two tokens, the two halves stay on one line.
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

			lines := segment.Split(lexer.Tokenize(tc.input))

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
	lines := segment.Split(lexer.Tokenize("# c\r\nk: v\r\n"))
	require.Len(t, lines, 2)

	nl := part(t, lines, 0, 1)
	assert.Equal(t, "\n", nl.Origin)
	assert.Equal(t, 5, nl.Position.Offset)

	lines = segment.Split(lexer.Tokenize("a: !t\r\n  b: 1\r\nc: 'x\r\n  y'\r\n"))
	require.Len(t, lines, 4)

	dup := part(t, lines, 0, 3)
	assert.Equal(t, "\r\n", dup.Origin)
	assert.Equal(t, 7, dup.Position.Offset, "the repeat shares the tag's \\r offset")

	// "a: !t\r\n" (7) + "  b: 1\r\n" (8) + "c: 'x\r\n" (7) puts the
	// continuation at 1-indexed offset 23.
	cont := part(t, lines, 3, 0)
	assert.Equal(t, "  y'", cont.Origin)
	assert.Equal(t, 23, cont.Position.Offset)
}

func TestSplit_NewlineColumn(t *testing.T) {
	t.Parallel()

	// A pure-newline part starts in the column after the last visible rune
	// of its line, or in column 1 on a blank line.
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
			want:  7,
		},
		"repeated newline after a long tag": {
			input: "a: !!map\n  b: 1\n",
			line:  0,
			idx:   3,
			want:  10,
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

			lines := segment.Split(lexer.Tokenize(tc.input))
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
	lines := segment.Split(lexer.Tokenize("a: !t\n  b: 1\nc: \"x\n  y\"\nd: 1\n"))
	require.Len(t, lines, 5)

	dup := part(t, lines, 0, 3)
	assert.Equal(t, "\n", dup.Origin)
	assert.Equal(t, 7, dup.Position.Offset, "the repeat shares the tag newline's offset")

	// "a: !t\n" (6) + "  b: 1\n" (7) + "c: \"x\n" (6) puts the continuation
	// at 1-indexed offset 20.
	cont := part(t, lines, 3, 0)
	assert.Equal(t, "  y\"", cont.Origin)
	assert.Equal(t, 20, cont.Position.Offset)
}

func TestSplit_BlockScalarOffsets(t *testing.T) {
	t.Parallel()

	// Offsets of the parts cut from one block scalar must increase down the
	// scalar, whichever line the lexer pinned the original Position to.
	tcs := map[string]struct {
		input string
		want  []int // Offset of the first part on each block scalar line.
	}{
		"blank line inside, content follows": {
			input: "k: |\n  a\n\n  b\nz: 1\n",
			want:  []int{9, 10, 11},
		},
		"content follows": {
			input: "k: |\n  a\n  b\nz: 1\n",
			want:  []int{9, 10},
		},
		"at end of input": {
			input: "k: |\n  a\n  b\n",
			want:  []int{6, 12},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := segment.Split(lexer.Tokenize(tc.input))
			require.Greater(t, len(lines), len(tc.want))

			got := make([]int, 0, len(tc.want))
			for i := range tc.want {
				got = append(got, part(t, lines, i+1, 0).Position.Offset)
			}

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSplit_BlockScalarTrailingIndent(t *testing.T) {
	t.Parallel()

	// The lexer bundles the indentation of the line after a block scalar into
	// the scalar's Origin ("    x\n\n  "). That fragment is not content, so the
	// Value and the original Position stay on the last content line.
	lines := segment.Split(lexer.Tokenize("a:\n  k: |\n    x\n\n  # c\n  z: 1\n"))
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
