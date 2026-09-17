package line_test

import (
	"testing"

	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
)

func TestSplit(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input       string
		wantContent []string
		wantNumbers []int
	}{
		"empty": {
			input: "",
		},
		"single line": {
			input:       "key: value",
			wantContent: []string{"key: value"},
			wantNumbers: []int{1},
		},
		"block scalar splits per line": {
			input:       "foo: |-\n  hello\n  world\n",
			wantContent: []string{"foo: |-", "  hello", "  world"},
			wantNumbers: []int{1, 2, 3},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := line.NewLines(lexer.Tokenize(tc.input))
			require.Len(t, lines, len(tc.wantContent))

			for i, l := range lines {
				assert.Equal(t, tc.wantContent[i], l.Content(), "line %d content", i)
				assert.Equal(t, tc.wantNumbers[i], l.Number(), "line %d number", i)
			}
		})
	}

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
	})
}

func TestLine_Runes(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  []string
	}{
		"lf ending":                        {input: "a: b\nc: d\n", want: []string{"a: b\n", "c: d"}},
		"crlf ending collapses to newline": {input: "a: b\r\nc: d\r\n", want: []string{"a: b\n", "c: d\n"}},
		"no ending at end of input":        {input: "ab", want: []string{"ab"}},
		"multibyte runes":                  {input: "k: héllo\nz: 1", want: []string{"k: héllo\n", "z: 1"}},
		// The lexer repeats the newline after a tag at the start of the next
		// token; the line still yields it once.
		"newline repeated after tag": {input: "a: !!map\n  b: 1\n", want: []string{"a: !!map\n", "  b: 1"}},
		"crlf repeated after tag":    {input: "a: !t\r\n  b: 1\r\n", want: []string{"a: !t\n", "  b: 1\n"}},
		"crlf cut after comment":     {input: "a: b # c\r\nd: e\r\n", want: []string{"a: b # c\n", "d: e\n"}},
		"bare cr ending":             {input: "a: 1\rb: 2\r", want: []string{"a: 1\n", "b: 2"}},
		"blank line after tag":       {input: "a: !t\n\n  b: 1\n", want: []string{"a: !t\n", "\n", "  b: 1"}},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lines := line.NewLines(lexer.Tokenize(tc.input))
			require.Len(t, lines, len(tc.want))

			for i, l := range lines {
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

		lines := line.NewLines(lexer.Tokenize("abc: def\n"))
		require.Len(t, lines, 1)

		count := 0
		for range lines[0].Runes() {
			count++
			if count == 2 {
				break
			}
		}

		assert.Equal(t, 2, count)
	})

	t.Run("zero value yields nothing", func(t *testing.T) {
		t.Parallel()

		var l line.Line

		for range l.Runes() {
			t.Fatal("zero value must not yield")
		}
	})
}

func TestLine_Tokens(t *testing.T) {
	t.Parallel()

	src := lexer.Tokenize("foo: |-\n  hello\n  world\nbar: baz\n")
	lines := line.NewLines(src)
	require.Len(t, lines, 4)

	// The block scalar content is a single lexer token spanning lines 1 and 2.
	content := lines[1].TokenAt(2)
	require.NotNil(t, content)
	assert.Equal(t, "hello\nworld", content.Value)
	assert.Same(t, content, lines[2].TokenAt(0), "both lines resolve to the same source token")

	t.Run("SourceTokens returns each original once per line", func(t *testing.T) {
		t.Parallel()

		assert.Equal(t, token.Tokens{content}, lines[1].SourceTokens())
		assert.Equal(t, token.Tokens{content}, lines[2].SourceTokens())

		first := lines[0].SourceTokens()
		require.Len(t, first, 3, "key, colon, and block header")

		for _, tk := range first {
			assert.Contains(t, src, tk, "source tokens are the lexer's originals")
		}
	})

	t.Run("Tokens returns per-line parts", func(t *testing.T) {
		t.Parallel()

		parts := lines[1].Tokens()
		require.Len(t, parts, 1)
		assert.NotSame(t, content, parts[0], "the part is distinct from its source")
		assert.Equal(t, "  hello\n", parts[0].Origin)
		assert.Equal(t, 2, parts[0].Position.Line)
		assert.Same(t, parts[0], lines[1].Token(0))
	})

	t.Run("TokenAt out of range", func(t *testing.T) {
		t.Parallel()

		assert.Nil(t, lines[3].TokenAt(-1))
		assert.Nil(t, lines[3].TokenAt(lines[3].Width()))
	})
}

func TestLine_TokenSpan(t *testing.T) {
	t.Parallel()

	src := lexer.Tokenize("foo: |-\n  hello\n  world\nbar:   baz\n")
	lines := line.NewLines(src)
	require.Len(t, lines, 4)

	content := lines[1].TokenAt(2)
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
			tk:          lines[2].Token(0),
			wantSpan:    position.NewSpan(0, 7),
			wantContent: position.NewSpan(2, 7),
			wantFound:   true,
		},
		"value after padding": {
			line:        3,
			tk:          lines[3].TokenAt(7),
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

			l := lines[tc.line]

			span, ok := l.TokenSpan(tc.tk)
			assert.Equal(t, tc.wantFound, ok)
			assert.Equal(t, tc.wantSpan, span)

			cspan, ok := l.ContentSpan(tc.tk)
			assert.Equal(t, tc.wantFound, ok)
			assert.Equal(t, tc.wantContent, cspan)
		})
	}
}
