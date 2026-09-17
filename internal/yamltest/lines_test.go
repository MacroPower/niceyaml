package yamltest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/line"
)

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
			// The lexer gives multi-line block scalar content that other
			// content follows Column 0.
			"block scalar column zero": {
				input: "k: >\n  a\n\n  b\nz: 1\n",
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
				assert.NoError(t, yamltest.ValidateLines(lines))
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
		require.NoError(t, yamltest.ValidateLines(lines))
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
		require.NoError(t, yamltest.ValidateLines(lines))
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

		err := yamltest.ValidateLines(lines)
		require.ErrorIs(t, err, yamltest.ErrColumnNotIncreasing)
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

		err := yamltest.ValidateLines(lines)
		require.ErrorIs(t, err, yamltest.ErrColumnNotIncreasing)
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
		require.NoError(t, yamltest.ValidateLines(lines))
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
		assert.NoError(t, yamltest.ValidateLines(lines))
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

		assert.NoError(t, yamltest.ValidateLines(lines))
	})
}

func TestLines_Validate_Testdata(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"full.yaml", "full-modified.yaml"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			src, err := os.ReadFile(filepath.Join("..", "..", "testdata", name))
			require.NoError(t, err)

			lines := line.NewLines(lexer.Tokenize(string(src)))
			require.NotEmpty(t, lines)

			assert.Len(t, lines, strings.Count(string(src), "\n"), "one line per source line")
			assert.NoError(t, yamltest.ValidateLines(lines))
		})
	}
}
