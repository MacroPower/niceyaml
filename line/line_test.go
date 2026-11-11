package line_test

import (
	"fmt"
	"math"
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

func TestLine(t *testing.T) {
	t.Parallel()

	t.Run("zero value", func(t *testing.T) {
		t.Parallel()

		var l line.Line

		assert.True(t, l.IsEmpty())
		assert.Equal(t, 0, l.Number())
		assert.Equal(t, 0, l.Width())
		assert.Empty(t, l.Content())
		assert.Nil(t, l.Tokens())
		assert.Nil(t, l.SourceTokens())
		assert.Nil(t, l.TokenAt(0))

		for range l.Runes() {
			t.Fatal("zero value must not yield")
		}

		// The zero value does not hold a token, with or without a position.
		for _, tk := range []*token.Token{
			nil,
			{Type: token.StringType, Value: "key", Origin: "key"},
			{
				Type: token.StringType, Value: "key", Origin: "key",
				Position: &token.Position{Line: 1, Column: 1, Offset: 1},
			},
		} {
			span, ok := l.TokenSpan(tk)
			assert.False(t, ok)
			assert.Equal(t, position.Span{}, span)

			span, ok = l.ContentSpan(tk)
			assert.False(t, ok)
			assert.Equal(t, position.Span{}, span)
		}
	})

	t.Run("a line with tokens is not empty", func(t *testing.T) {
		t.Parallel()

		assert.False(t, newLines("key: value\n").Line(0).IsEmpty())
	})
}

func TestLine_Runes(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  []string
	}{
		"lf ending": {input: stringtest.LinesLF("a: b", "c: d"), want: []string{"a: b\n", "c: d\n"}},
		"crlf ending collapses to newline": {
			input: stringtest.LinesCRLF("a: b", "c: d"),
			want:  []string{"a: b\n", "c: d\n"},
		},
		"no ending at end of input": {input: "ab", want: []string{"ab"}},
		"multibyte runes": {
			input: stringtest.JoinLF("k: héllo", "z: 1"),
			want:  []string{"k: héllo\n", "z: 1"},
		},
		// The lexer repeats the newline after a tag at the start of the next
		// token. The line still yields it once.
		"newline repeated after tag": {
			input: stringtest.LinesLF(
				"a: !!map",
				"  b: 1",
			),
			want: []string{"a: !!map\n", "  b: 1\n"},
		},
		"crlf repeated after tag": {
			input: stringtest.LinesCRLF(
				"a: !t",
				"  b: 1",
			),
			want: []string{"a: !t\n", "  b: 1\n"},
		},
		"crlf cut after comment": {
			input: stringtest.LinesCRLF("a: b # c", "d: e"),
			want:  []string{"a: b # c\n", "d: e\n"},
		},
		"bare cr ending": {input: "a: 1\rb: 2\r", want: []string{"a: 1\n", "b: 2\n"}},
		"blank line after tag": {
			input: stringtest.LinesLF(
				"a: !t",
				"",
				"  b: 1",
			),
			want: []string{"a: !t\n", "\n", "  b: 1\n"},
		},
		// The empty content of a block scalar that keeps its trailing
		// lines joins the last line after its ending. The line still
		// yields the ending.
		"keep block scalar at the end": {input: "a: |+\n", want: []string{"a: |+\n"}},
		"keep block scalar with crlf":  {input: "a: |+\r\n", want: []string{"a: |+\n"}},
		"nested keep block scalar at the end": {
			input: stringtest.LinesLF(
				"a:",
				"  b: |+",
			),
			want: []string{"a:\n", "  b: |+\n"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := newLines(tc.input)
			require.Equal(t, len(tc.want), lines.Len())

			for i, l := range lines.All() {
				var got []rune

				for col, r := range l.Runes() {
					assert.Equal(t, len(got), col, "line %d: columns are sequential", i)

					got = append(got, r)
				}

				assert.Equal(t, tc.want[i], string(got), "line %d", i)

				visible := len(got)
				if got[len(got)-1] == '\n' {
					visible--
				}

				assert.Equal(t, visible, l.Width(), "line %d: visible columns match Width", i)
			}
		})
	}

	t.Run("stops when yield returns false", func(t *testing.T) {
		t.Parallel()

		lines := newLines("abc: def\n")
		require.Equal(t, 1, lines.Len())

		count := 0
		for range lines.Line(0).Runes() {
			count++
			if count == 2 {
				break
			}
		}

		assert.Equal(t, 2, count)
	})
}

func TestLine_Tokens(t *testing.T) {
	t.Parallel()

	src := tokens.Tokenize(stringtest.LinesLF(
		"foo: |-",
		"  hello",
		"  world",
		"bar: baz",
	))
	lines := line.NewLines(src)
	require.Equal(t, 4, lines.Len())

	// The block scalar content is a single lexer token spanning lines 1 and 2.
	content := lines.Line(1).TokenAt(2)
	require.NotNil(t, content)
	assert.Equal(t, "hello\nworld", content.Value)
	assert.Same(t, content, lines.Line(2).TokenAt(0), "both lines resolve to the same source token")

	t.Run("SourceTokens returns each original once per line", func(t *testing.T) {
		t.Parallel()

		assert.Equal(t, token.Tokens{content}, lines.Line(1).SourceTokens())
		assert.Equal(t, token.Tokens{content}, lines.Line(2).SourceTokens())

		first := lines.Line(0).SourceTokens()
		require.Len(t, first, 3, "key, colon, and block header")

		for _, tk := range first {
			assert.Contains(t, src, tk, "source tokens are the lexer's originals")
		}
	})

	t.Run("Tokens returns per-line parts", func(t *testing.T) {
		t.Parallel()

		parts := lines.Line(1).Tokens()
		require.Len(t, parts, 1)
		assert.NotSame(t, content, parts[0], "the part is distinct from its source")
		assert.Equal(t, "  hello\n", parts[0].Origin)
		assert.Equal(t, 2, parts[0].Position.Line)
		assert.Same(t, parts[0], lines.Line(1).Token(0))
	})

	t.Run("TokenAt out of range", func(t *testing.T) {
		t.Parallel()

		assert.Nil(t, lines.Line(3).TokenAt(-1))
		assert.Nil(t, lines.Line(3).TokenAt(lines.Line(3).Width()))
	})
}

func TestLine_TokenSpan(t *testing.T) {
	t.Parallel()

	lines := newLines(stringtest.LinesLF(
		"foo: |-",
		"  hello",
		"  world",
		"bar:   baz",
	))
	require.Equal(t, 4, lines.Len())

	content := lines.Line(1).TokenAt(2)
	require.NotNil(t, content)

	tcs := map[string]struct {
		line        int
		tk          *token.Token
		wantSpan    position.Span
		wantContent position.Span
		wantFound   bool
	}{
		"source token on first content line": {
			line:        1,
			tk:          content,
			wantSpan:    position.NewSpan(0, 7),
			wantContent: position.NewSpan(2, 7),
			wantFound:   true,
		},
		"source token on second content line": {
			line:        2,
			tk:          content,
			wantSpan:    position.NewSpan(0, 7),
			wantContent: position.NewSpan(2, 7),
			wantFound:   true,
		},
		"per-line part": {
			line:        2,
			tk:          lines.Line(2).Token(0),
			wantSpan:    position.NewSpan(0, 7),
			wantContent: position.NewSpan(2, 7),
			wantFound:   true,
		},
		"value after padding": {
			line:        3,
			tk:          lines.Line(3).TokenAt(7),
			wantSpan:    position.NewSpan(4, 10),
			wantContent: position.NewSpan(7, 10),
			wantFound:   true,
		},
		"token on another line": {
			line:      0,
			tk:        content,
			wantFound: false,
		},
		"nil token": {
			line:      0,
			tk:        nil,
			wantFound: false,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			l := lines.Line(tc.line)

			span, ok := l.TokenSpan(tc.tk)
			assert.Equal(t, tc.wantFound, ok)
			assert.Equal(t, tc.wantSpan, span)

			cspan, ok := l.ContentSpan(tc.tk)
			assert.Equal(t, tc.wantFound, ok)
			assert.Equal(t, tc.wantContent, cspan)
		})
	}
}

// TestLine_TokenSpan_Offsets verifies that a line finds each of its tokens
// whatever offsets the tokens carry. A stream built by hand can leave its
// positions out, give every token one offset, or give offsets that do not
// follow the order of the line.
//
// A line reads a few tokens one by one and indexes a larger number of
// them. Each case thus runs on lines of several lengths. Two of them are
// the longest line that does not index its tokens and a line of one token
// more.
func TestLine_TokenSpan_Offsets(t *testing.T) {
	t.Parallel()

	at := func(offset int) *token.Position {
		return &token.Position{Line: 1, Column: 1, Offset: offset}
	}

	// The numbers of tokens that each case adds to the end of its line.
	// The tokens do not have positions and do not equal a token of a case.
	fills := []int{0, 4, 5, 60}

	fill := func(tks token.Tokens, n int) token.Tokens {
		for range n {
			tks = append(tks, &token.Token{Type: token.SequenceEndType, Value: "]", Origin: "]"})
		}

		return tks
	}

	tcs := map[string]struct {
		positions [4]*token.Position
	}{
		"without positions":       {},
		"one offset for each":     {positions: [4]*token.Position{at(5), at(5), at(5), at(5)}},
		"descending offsets":      {positions: [4]*token.Position{at(40), at(30), at(20), at(10)}},
		"offsets out of order":    {positions: [4]*token.Position{at(20), at(40), at(10), at(30)}},
		"positions on some alone": {positions: [4]*token.Position{nil, at(7), nil, at(3)}},
		"negative offsets":        {positions: [4]*token.Position{at(-5), at(-1), at(-5), at(-9)}},
		"the largest offset for each": {
			positions: [4]*token.Position{at(math.MaxInt), at(math.MaxInt), at(math.MaxInt), at(math.MaxInt)},
		},
		"the largest and the smallest offsets": {
			positions: [4]*token.Position{at(math.MaxInt), at(math.MinInt), at(math.MaxInt - 1), at(math.MinInt)},
		},
	}

	for name, tc := range tcs {
		for _, n := range fills {
			t.Run(fmt.Sprintf("%s/%d tokens", name, 4+n), func(t *testing.T) {
				t.Parallel()

				tks := fill(token.Tokens{
					{Type: token.StringType, Value: "key", Origin: "key", Position: tc.positions[0]},
					{Type: token.MappingValueType, Value: ":", Origin: ":", Position: tc.positions[1]},
					{Type: token.StringType, Value: "one", Origin: "  one ", Position: tc.positions[2]},
					{Type: token.CommentType, Value: " two", Origin: "# two", Position: tc.positions[3]},
				}, n)

				lines := line.NewLines(tks)
				require.Equal(t, 1, lines.Len())

				l := lines.Line(0)
				require.Equal(t, "key:  one # two"+strings.Repeat("]", n), l.Content())

				wantSpans := []position.Span{
					position.NewSpan(0, 3),
					position.NewSpan(3, 4),
					position.NewSpan(4, 10),
					position.NewSpan(10, 15),
				}
				wantContent := []position.Span{
					position.NewSpan(0, 3),
					position.NewSpan(3, 4),
					position.NewSpan(6, 9),
					position.NewSpan(10, 15),
				}

				for i, tk := range tks[:4] {
					// The lexer token, the part of it on the line, and a copy
					// of each find the same columns.
					for _, found := range []*token.Token{tk, l.Token(i), tk.Clone(), l.Token(i).Clone()} {
						span, ok := l.TokenSpan(found)
						require.True(t, ok, "token %d", i)
						assert.Equal(t, wantSpans[i], span, "token %d", i)

						content, ok := l.ContentSpan(found)
						require.True(t, ok, "token %d", i)
						assert.Equal(t, wantContent[i], content, "token %d", i)
					}

					assert.Equal(t, position.Ranges{yamltest.LineRange(0, wantSpans[i].Start, wantSpans[i].End)},
						lines.TokenRanges(tk), "token %d", i)
					assert.Equal(t, position.Ranges{yamltest.LineRange(0, wantContent[i].Start, wantContent[i].End)},
						lines.ContentRanges(tk.Clone()), "token %d", i)
				}

				absent := &token.Token{Type: token.StringType, Value: "key", Origin: "key", Position: at(99)}

				_, ok := l.TokenSpan(absent)
				assert.False(t, ok, "a token at an offset that the line does not hold")
				assert.Nil(t, lines.TokenRanges(absent))
			})
		}
	}

	// A token matches by its type, value, origin, and position, so the
	// second of two tokens that agree in all four finds the first.
	for _, n := range fills {
		t.Run(fmt.Sprintf("equal tokens find the first of them/%d tokens", 3+n), func(t *testing.T) {
			t.Parallel()

			tks := fill(token.Tokens{
				{Type: token.StringType, Value: "x", Origin: "x"},
				{Type: token.CollectEntryType, Value: ",", Origin: ","},
				{Type: token.StringType, Value: "x", Origin: "x"},
			}, n)

			lines := line.NewLines(tks)
			l := lines.Line(0)

			for _, tk := range []*token.Token{tks[0], tks[2], tks[2].Clone()} {
				span, ok := l.TokenSpan(tk)
				require.True(t, ok)
				assert.Equal(t, position.NewSpan(0, 1), span)

				assert.Equal(t, position.Ranges{yamltest.LineRange(0, 0, 1)}, lines.TokenRanges(tk))
				assert.Equal(t, position.Ranges{yamltest.LineRange(0, 0, 1)}, lines.ContentRanges(tk))
			}
		})
	}
}

// TestLine_TokenSpan_LongLine verifies that the tokens of a line that
// holds thousands of them each find their own columns. The spans follow
// each other from column 0 to the width of the line, and each column
// resolves to the token whose span holds it.
func TestLine_TokenSpan_LongLine(t *testing.T) {
	t.Parallel()

	lines := oneLine(500)
	require.Equal(t, 1, lines.Len())

	l := lines.Line(0)
	content := []rune(l.Content())
	require.Len(t, content, l.Width())

	tks := l.SourceTokens()
	require.Len(t, tks, 500*10+1)

	col := 0

	for i, tk := range tks {
		span, ok := l.TokenSpan(tk)
		require.True(t, ok, "token %d", i)
		require.Equal(t, col, span.Start, "token %d", i)
		require.Equal(t, tokens.TrimLineEnding(tk.Origin), string(content[span.Start:span.End]), "token %d", i)

		got, ok := l.ContentSpan(tk.Clone())
		require.True(t, ok, "token %d", i)
		require.Equal(t, strings.Trim(tk.Origin, " \n"), string(content[got.Start:got.End]), "token %d", i)

		for c := span.Start; c < span.End; c++ {
			require.Same(t, tk, l.TokenAt(c), "column %d", c)
		}

		require.Equal(t, position.Ranges{yamltest.LineRange(0, got.Start, got.End)}, lines.ContentRanges(tk))

		col = span.End
	}

	assert.Equal(t, l.Width(), col)
	assert.Equal(t, l.Width(), lines.Width())
	assert.Nil(t, l.TokenAt(col))
}

func TestLine_String(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		line *line.Line
		want string
	}{
		"numbered": {
			line: newLines("key: value\n").Line(0),
			want: "   1 | key: value",
		},
		"control characters": {
			line: newLines("a: \"x\ty\"\n").Line(0),
			want: "   1 | a: \"x\u2409y\"",
		},
		// A zero Line, which a diff puts opposite an inserted or deleted
		// line, has no number, and its gutter is blank rather than 0.
		"placeholder": {
			line: &line.Line{},
			want: "     | ",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.line.String())
			assert.Equal(t, line.NewView(line.Collect(tc.line)).String(), tc.line.String())
		})
	}
}
