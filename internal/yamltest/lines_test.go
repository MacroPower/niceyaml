package yamltest_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/tokens"
)

func TestLines_Validate(t *testing.T) {
	t.Parallel()

	// A str token is a string at a line and a column, whose Value is its
	// Origin without the line ending.
	str := func(origin string, line, col int) *token.Token {
		return yamltest.NewTokenBuilder().
			Type(token.StringType).
			Origin(origin).
			Value(strings.TrimSuffix(origin, "\n")).
			PositionLine(line).
			PositionColumn(col).
			Build()
	}

	// The tokens linked returns carry the links between them that Add sets.
	linked := func(tks ...*token.Token) token.Tokens {
		var out token.Tokens

		out.Add(tks...)

		return out
	}

	t.Run("valid cases", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]string{
			"valid simple":          "key: value\n",
			"valid multi-line":      stringtest.LinesLF("first: 1", "second: 2", "third: 3"),
			"valid with join flags": stringtest.LinesLF("script: |", "  line1", "  line2"),
			"block scalar with content after it": stringtest.LinesLF(
				"k: >",
				"  a",
				"",
				"  b",
				"z: 1",
			),
			"empty": "",
		}

		for name, input := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				tks := tokens.Tokenize(input)
				lines := line.NewLines(tks)

				// Every token NewLines creates is valid.
				assert.NoError(t, yamltest.ValidateLines(lines))
			})
		}
	})

	t.Run("hand-built tokens", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			err  error
			msg  string
			tks  token.Tokens
			want []int // The number NewLines gives each line.
		}{
			// NewLines normalizes the numbers so they strictly increase.
			"same line number twice": {
				tks:  linked(str("first\n", 5, 1), str("second\n", 5, 1)),
				want: []int{5, 6},
			},
			"decreasing line numbers": {
				tks:  linked(str("first\n", 10, 1), str("second\n", 5, 1)),
				want: []int{10, 11},
			},
			"gaps in line numbers": {
				tks:  linked(str("first\n", 1, 1), str("second\n", 10, 1), str("third\n", 100, 1)),
				want: []int{1, 10, 100},
			},
			"line with tokens and no number": {
				tks:  linked(str("first\n", 0, 1)),
				want: []int{0},
				err:  yamltest.ErrLineNumberNotIncreasing,
			},
			"same column twice": {
				tks:  linked(str("first", 1, 5), str("second\n", 1, 5)),
				want: []int{1},
				err:  yamltest.ErrColumnNotIncreasing,
				msg:  "column 5 not greater than previous 5",
			},
			"decreasing columns": {
				tks:  linked(str("first", 1, 10), str("second\n", 1, 5)),
				want: []int{1},
				err:  yamltest.ErrColumnNotIncreasing,
				msg:  "column 5 not greater than previous 10",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				lines := line.NewLines(tc.tks)
				assert.Equal(t, tc.want, yamltest.LineNumbers(lines))

				requireValidation(t, yamltest.ValidateLines(lines), tc.err, tc.msg)
			})
		}
	})

	t.Run("placeholder lines carry no number", func(t *testing.T) {
		t.Parallel()

		// A side-by-side diff inserts zero-value lines opposite inserted
		// and deleted lines, and they hold no tokens to number.
		require.NoError(t, yamltest.ValidateLines(line.Collect(&line.Line{}, &line.Line{})))
	})

	t.Run("token line differs from line number", func(t *testing.T) {
		t.Parallel()

		lines := line.NewLines(tokens.Tokenize(stringtest.LinesLF("a: 1", "b: 2")))
		require.Equal(t, 2, lines.Len())

		// The line shares its tokens, so renumbering them moves every token
		// on the second line away from its number, 2.
		for _, tk := range lines.Line(1).Tokens() {
			tk.Position.Line = 7
		}

		err := yamltest.ValidateLines(lines)
		requireValidation(t, err, yamltest.ErrLineNumberMismatch, "token line 7 differs from line number 2")
	})

	t.Run("token line number opens a new line", func(t *testing.T) {
		t.Parallel()

		// The lexer drops the line ending ahead of some tokens, so a
		// token whose position names the next line opens that line even
		// though the Origin before it holds no line ending.
		lines := line.NewLines(linked(str("first", 1, 1), str("second\n", 2, 1)))

		require.NoError(t, yamltest.ValidateLines(lines))
		require.Equal(t, 2, lines.Len())

		assert.Equal(t, 1, lines.Line(0).Token(0).Position.Line)
		assert.Equal(t, 2, lines.Line(1).Token(0).Position.Line)
	})

	t.Run("nil position tokens - normalized", func(t *testing.T) {
		t.Parallel()

		// TokenBuilder always sets a Position, so the test drops it by hand.
		unplaced := str("first", 0, 0)
		unplaced.Position = nil

		lines := line.NewLines(linked(unplaced, str("second\n", 1, 10)))

		// NewLines gives every token a position, so validation sees none of
		// the nil positions it rejects.
		require.NoError(t, yamltest.ValidateLines(lines))
		assert.NotNil(t, lines.Line(0).Token(0).Position)
	})
}

func TestLines_Validate_Testdata(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"full.yaml", "full-modified.yaml"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			src := yamltest.ReadFile(t, filepath.Join("..", "..", "testdata", name))

			lines := line.NewLines(tokens.Tokenize(src))
			require.NotEmpty(t, lines)

			assert.Equal(t, strings.Count(src, "\n"), lines.Len(), "one line per source line")
			assert.NoError(t, yamltest.ValidateLines(lines))
		})
	}
}

func TestLineNumbers(t *testing.T) {
	t.Parallel()

	// The unified view holds line 2 of both revisions, so two of its lines
	// have the number 2.
	unified := yamltest.Diff(stringtest.LinesLF("a: 1", "b: 2"), stringtest.LinesLF("a: 1", "b: 3")).Unified()

	tcs := map[string]struct {
		seq  line.Sequence
		want []int
	}{
		"no lines": {seq: line.Lines{}},
		"lines": {
			seq:  niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "b: 2")).Lines(),
			want: []int{1, 2},
		},
		"view": {seq: unified, want: []int{1, 2, 2}},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, yamltest.LineNumbers(tc.seq))
		})
	}
}

// excerptOf returns the excerpt of the binding of err to the one line of
// "key: value", with no line of context.
func excerptOf(t *testing.T, err error) *line.View {
	t.Helper()

	bound := yamltest.RequireSourceError(t, yamltest.Bind(t, niceyaml.NewSourceFromString("key: value\n"), err))

	excerpt, ok := bound.Excerpt(niceyaml.WithContextLines(0))
	require.True(t, ok)

	return excerpt
}

func TestErrorAnnotation(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		err  error
		want line.Annotations
	}{
		// The headline holds the message of a single error, so its line
		// carries the carets alone.
		"single error": {
			err:  yamltest.ErrorAt("bad value", "key"),
			want: line.Annotations{yamltest.ErrorAnnotation(position.NewSpan(5, 10), "")},
		},
		"summary of two problems": {
			err: niceyaml.NewSummary("2 problems",
				yamltest.ErrorAt("bad value", "key"),
				niceyaml.NewError("bad key", niceyaml.AtPosition(position.New(0, 0))),
			),
			want: line.Annotations{
				yamltest.ErrorAnnotation(position.NewSpan(5, 10), "bad value"),
				yamltest.ErrorAnnotation(position.NewSpan(0, 3), "bad key"),
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, excerptOf(t, tc.err).Annotations(0))
		})
	}
}

func TestErrorOverlays(t *testing.T) {
	t.Parallel()

	excerpt := excerptOf(t, niceyaml.NewSummary("2 problems",
		yamltest.ErrorAt("bad value", "key"),
		niceyaml.NewError("bad key", niceyaml.AtPosition(position.New(0, 0))),
	))

	assert.Equal(t,
		yamltest.ErrorOverlays(position.NewSpan(5, 10), position.NewSpan(0, 3)),
		excerpt.Overlays(0),
	)
	assert.Empty(t, yamltest.ErrorOverlays())
}
