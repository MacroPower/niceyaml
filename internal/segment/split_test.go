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
