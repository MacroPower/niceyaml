package yamltest_test

import (
	"testing"

	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macropower/niceyaml/yamltest"
)

func TestDumpTokenOrigins(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		want  string
		input token.Tokens
	}{
		"empty slice": {
			input: token.Tokens{},
			want:  "",
		},
		"single token": {
			input: token.Tokens{
				{Origin: "hello"},
			},
			want: "hello",
		},
		"multiple tokens": {
			input: token.Tokens{
				{Origin: "key"},
				{Origin: ": "},
				{Origin: "value\n"},
			},
			want: "key: value\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := yamltest.DumpTokenOrigins(tc.input)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestFormatTokenPosition(t *testing.T) {
	t.Parallel()

	t.Run("nil position", func(t *testing.T) {
		t.Parallel()

		got := yamltest.FormatTokenPosition(nil)
		assert.Equal(t, "<nil>", got)
	})

	t.Run("valid position", func(t *testing.T) {
		t.Parallel()

		pos := &token.Position{
			Line:        10,
			Column:      5,
			Offset:      100,
			IndentNum:   2,
			IndentLevel: 1,
		}
		got := yamltest.FormatTokenPosition(pos)
		assert.Equal(t, "Line=10 Col=5 Offset=100 IndentNum=2 IndentLevel=1", got)
	})
}

func TestFormatToken(t *testing.T) {
	t.Parallel()

	t.Run("nil token", func(t *testing.T) {
		t.Parallel()

		got := yamltest.FormatToken(nil)
		assert.Equal(t, "<nil>", got)
	})

	t.Run("valid token", func(t *testing.T) {
		t.Parallel()

		tk := &token.Token{
			Type:          token.StringType,
			Value:         "hello",
			Origin:        "hello\n",
			Indicator:     token.NotIndicator,
			CharacterType: token.CharacterTypeMiscellaneous,
			Position: &token.Position{
				Line:        1,
				Column:      1,
				Offset:      0,
				IndentNum:   0,
				IndentLevel: 0,
			},
		}
		got := yamltest.FormatToken(tk)
		assert.Contains(t, got, "Type=String")
		assert.Contains(t, got, `Value="hello"`)
		assert.Contains(t, got, `Origin="hello\n"`)
		assert.Contains(t, got, "Line=1")
		assert.Contains(t, got, "Col=1")
	})
}

func TestFormatTokens(t *testing.T) {
	t.Parallel()

	t.Run("empty slice", func(t *testing.T) {
		t.Parallel()

		got := yamltest.FormatTokens(token.Tokens{})
		assert.Equal(t, "<empty>", got)
	})

	t.Run("nil slice", func(t *testing.T) {
		t.Parallel()

		got := yamltest.FormatTokens(nil)
		assert.Equal(t, "<empty>", got)
	})

	t.Run("single token", func(t *testing.T) {
		t.Parallel()

		tks := token.Tokens{
			{Value: "test", Position: &token.Position{Line: 1}},
		}
		got := yamltest.FormatTokens(tks)
		assert.Contains(t, got, "[0]")
		assert.Contains(t, got, `Value="test"`)
	})

	t.Run("multiple tokens", func(t *testing.T) {
		t.Parallel()

		tks := token.Tokens{
			{Value: "first", Position: &token.Position{Line: 1}},
			{Value: "second", Position: &token.Position{Line: 2}},
		}
		got := yamltest.FormatTokens(tks)
		assert.Contains(t, got, "[0]")
		assert.Contains(t, got, "[1]")
		assert.Contains(t, got, `Value="first"`)
		assert.Contains(t, got, `Value="second"`)
	})
}

func TestCompareContent(t *testing.T) {
	t.Parallel()

	t.Run("equal content", func(t *testing.T) {
		t.Parallel()

		diff := yamltest.CompareContent("hello", "hello")
		assert.True(t, diff.Equal())
		assert.Equal(t, "content equal", diff.String())
	})

	t.Run("normalizes line endings", func(t *testing.T) {
		t.Parallel()

		diff := yamltest.CompareContent("line1\nline2", "line1\r\nline2")
		assert.True(t, diff.Equal())
	})

	t.Run("trims surrounding newlines", func(t *testing.T) {
		t.Parallel()

		diff := yamltest.CompareContent("\nhello\n", "hello")
		assert.True(t, diff.Equal())
	})

	t.Run("different content", func(t *testing.T) {
		t.Parallel()

		diff := yamltest.CompareContent("hello", "world")
		assert.False(t, diff.Equal())
		assert.Contains(t, diff.String(), "content mismatch")
		assert.Contains(t, diff.String(), "hello")
		assert.Contains(t, diff.String(), "world")
	})
}

func TestTokenBuilder(t *testing.T) {
	t.Parallel()

	t.Run("NewTokenBuilder creates builder with non-nil position", func(t *testing.T) {
		t.Parallel()

		b := yamltest.NewTokenBuilder()
		tk := b.Build()
		require.NotNil(t, tk)
		require.NotNil(t, tk.Position)
	})

	t.Run("Type setter", func(t *testing.T) {
		t.Parallel()

		tk := yamltest.NewTokenBuilder().Type(token.StringType).Build()
		assert.Equal(t, token.StringType, tk.Type)
	})

	t.Run("CharacterType setter", func(t *testing.T) {
		t.Parallel()

		tk := yamltest.NewTokenBuilder().CharacterType(token.CharacterTypeMiscellaneous).Build()
		assert.Equal(t, token.CharacterTypeMiscellaneous, tk.CharacterType)
	})

	t.Run("Indicator setter", func(t *testing.T) {
		t.Parallel()

		tk := yamltest.NewTokenBuilder().Indicator(token.BlockStructureIndicator).Build()
		assert.Equal(t, token.BlockStructureIndicator, tk.Indicator)
	})

	t.Run("Value setter", func(t *testing.T) {
		t.Parallel()

		tk := yamltest.NewTokenBuilder().Value("test").Build()
		assert.Equal(t, "test", tk.Value)
	})

	t.Run("Origin setter", func(t *testing.T) {
		t.Parallel()

		tk := yamltest.NewTokenBuilder().Origin("test\n").Build()
		assert.Equal(t, "test\n", tk.Origin)
	})

	t.Run("Error setter", func(t *testing.T) {
		t.Parallel()

		tk := yamltest.NewTokenBuilder().Error("some error").Build()
		assert.Equal(t, "some error", tk.Error)
	})

	t.Run("Position setter", func(t *testing.T) {
		t.Parallel()

		pos := token.Position{Line: 5, Column: 10, Offset: 50}
		tk := yamltest.NewTokenBuilder().Position(pos).Build()
		assert.Equal(t, 5, tk.Position.Line)
		assert.Equal(t, 10, tk.Position.Column)
		assert.Equal(t, 50, tk.Position.Offset)
	})

	t.Run("PositionLine setter", func(t *testing.T) {
		t.Parallel()

		tk := yamltest.NewTokenBuilder().PositionLine(7).Build()
		assert.Equal(t, 7, tk.Position.Line)
	})

	t.Run("PositionColumn setter", func(t *testing.T) {
		t.Parallel()

		tk := yamltest.NewTokenBuilder().PositionColumn(15).Build()
		assert.Equal(t, 15, tk.Position.Column)
	})

	t.Run("PositionOffset setter", func(t *testing.T) {
		t.Parallel()

		tk := yamltest.NewTokenBuilder().PositionOffset(100).Build()
		assert.Equal(t, 100, tk.Position.Offset)
	})

	t.Run("PositionIndentNum setter", func(t *testing.T) {
		t.Parallel()

		tk := yamltest.NewTokenBuilder().PositionIndentNum(4).Build()
		assert.Equal(t, 4, tk.Position.IndentNum)
	})

	t.Run("PositionIndentLevel setter", func(t *testing.T) {
		t.Parallel()

		tk := yamltest.NewTokenBuilder().PositionIndentLevel(2).Build()
		assert.Equal(t, 2, tk.Position.IndentLevel)
	})

	t.Run("chaining modifies builder", func(t *testing.T) {
		t.Parallel()

		b := yamltest.NewTokenBuilder()
		b.Value("first")

		tk1 := b.Build()
		b.Value("second")

		tk2 := b.Build()

		assert.Equal(t, "first", tk1.Value)
		assert.Equal(t, "second", tk2.Value)
	})

	t.Run("Build returns cloned token", func(t *testing.T) {
		t.Parallel()

		b := yamltest.NewTokenBuilder().Value("test")
		tk1 := b.Build()
		tk2 := b.Build()

		assert.NotSame(t, tk1, tk2)
		assert.Equal(t, tk1.Value, tk2.Value)
	})

	t.Run("Clone returns independent builder", func(t *testing.T) {
		t.Parallel()

		base := yamltest.NewTokenBuilder().Type(token.StringType)
		clone := base.Clone()

		base.Value("base")
		clone.Value("clone")

		tkBase := base.Build()
		tkClone := clone.Build()

		assert.Equal(t, "base", tkBase.Value)
		assert.Equal(t, "clone", tkClone.Value)
		assert.Equal(t, token.StringType, tkBase.Type)
		assert.Equal(t, token.StringType, tkClone.Type)
	})
}

func TestValidateTokenPair(t *testing.T) {
	t.Parallel()

	t.Run("valid tokens pass", func(t *testing.T) {
		t.Parallel()

		want := yamltest.NewTokenBuilder().Build()
		got := yamltest.NewTokenBuilder().Build()

		err := yamltest.ValidateTokenPair(want, got)
		assert.NoError(t, err)
	})

	t.Run("nil want token fails", func(t *testing.T) {
		t.Parallel()

		got := yamltest.NewTokenBuilder().Build()

		err := yamltest.ValidateTokenPair(nil, got)
		require.Error(t, err)
		require.ErrorIs(t, err, yamltest.ErrNilToken)
		assert.Contains(t, err.Error(), "want")
	})

	t.Run("nil want position fails", func(t *testing.T) {
		t.Parallel()

		want := &token.Token{}
		got := yamltest.NewTokenBuilder().Build()

		err := yamltest.ValidateTokenPair(want, got)
		require.Error(t, err)
		require.ErrorIs(t, err, yamltest.ErrNilPosition)
		assert.Contains(t, err.Error(), "want")
	})

	t.Run("nil got token fails", func(t *testing.T) {
		t.Parallel()

		want := yamltest.NewTokenBuilder().Build()

		err := yamltest.ValidateTokenPair(want, nil)
		require.Error(t, err)
		require.ErrorIs(t, err, yamltest.ErrNilToken)
		assert.Contains(t, err.Error(), "got")
	})

	t.Run("nil got position fails", func(t *testing.T) {
		t.Parallel()

		want := yamltest.NewTokenBuilder().Build()
		got := &token.Token{}

		err := yamltest.ValidateTokenPair(want, got)
		require.Error(t, err)
		require.ErrorIs(t, err, yamltest.ErrNilPosition)
		assert.Contains(t, err.Error(), "got")
	})
}

func TestValidateTokens(t *testing.T) {
	t.Parallel()

	t.Run("equal length with valid tokens", func(t *testing.T) {
		t.Parallel()

		tb := yamltest.NewTokenBuilder()
		want := token.Tokens{tb.Build(), tb.Build()}
		got := token.Tokens{tb.Build(), tb.Build()}

		err := yamltest.ValidateTokens(want, got)
		assert.NoError(t, err)
	})

	t.Run("empty slices pass", func(t *testing.T) {
		t.Parallel()

		err := yamltest.ValidateTokens(token.Tokens{}, token.Tokens{})
		assert.NoError(t, err)
	})

	t.Run("count mismatch fails", func(t *testing.T) {
		t.Parallel()

		tb := yamltest.NewTokenBuilder()
		want := token.Tokens{tb.Build()}
		got := token.Tokens{tb.Build(), tb.Build()}

		err := yamltest.ValidateTokens(want, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "count mismatch")
	})

	t.Run("nil want token fails", func(t *testing.T) {
		t.Parallel()

		tb := yamltest.NewTokenBuilder()
		want := token.Tokens{nil}
		got := token.Tokens{tb.Build()}

		err := yamltest.ValidateTokens(want, got)
		require.Error(t, err)
		require.ErrorIs(t, err, yamltest.ErrNilToken)
		assert.Contains(t, err.Error(), "token 0 want")
	})

	t.Run("nil want position fails", func(t *testing.T) {
		t.Parallel()

		tb := yamltest.NewTokenBuilder()
		want := token.Tokens{&token.Token{}}
		got := token.Tokens{tb.Build()}

		err := yamltest.ValidateTokens(want, got)
		require.Error(t, err)
		require.ErrorIs(t, err, yamltest.ErrNilPosition)
		assert.Contains(t, err.Error(), "token 0 want")
	})

	t.Run("nil got token fails", func(t *testing.T) {
		t.Parallel()

		tb := yamltest.NewTokenBuilder()
		want := token.Tokens{tb.Build()}
		got := token.Tokens{nil}

		err := yamltest.ValidateTokens(want, got)
		require.Error(t, err)
		require.ErrorIs(t, err, yamltest.ErrNilToken)
		assert.Contains(t, err.Error(), "token 0 got")
	})
}

func TestCompareTokens(t *testing.T) {
	t.Parallel()

	t.Run("equal tokens", func(t *testing.T) {
		t.Parallel()

		tk := yamltest.NewTokenBuilder().
			Type(token.StringType).
			Value("test").
			Origin("test\n").
			PositionLine(1).
			PositionColumn(1).
			Build()

		diff := yamltest.CompareTokens(tk, tk)
		assert.True(t, diff.Equal())
		assert.Equal(t, "tokens equal", diff.String())
	})

	t.Run("different Type", func(t *testing.T) {
		t.Parallel()

		want := yamltest.NewTokenBuilder().Type(token.StringType).Build()
		got := yamltest.NewTokenBuilder().Type(token.IntegerType).Build()

		diff := yamltest.CompareTokens(want, got)
		assert.False(t, diff.Equal())
		assert.Contains(t, diff.Fields, "Type")
	})

	t.Run("different Value", func(t *testing.T) {
		t.Parallel()

		want := yamltest.NewTokenBuilder().Value("a").Build()
		got := yamltest.NewTokenBuilder().Value("b").Build()

		diff := yamltest.CompareTokens(want, got)
		assert.False(t, diff.Equal())
		assert.Contains(t, diff.Fields, "Value")
	})

	t.Run("different Origin", func(t *testing.T) {
		t.Parallel()

		want := yamltest.NewTokenBuilder().Origin("a\n").Build()
		got := yamltest.NewTokenBuilder().Origin("b\n").Build()

		diff := yamltest.CompareTokens(want, got)
		assert.False(t, diff.Equal())
		assert.Contains(t, diff.Fields, "Origin")
	})

	t.Run("different Position.Line", func(t *testing.T) {
		t.Parallel()

		want := yamltest.NewTokenBuilder().PositionLine(1).Build()
		got := yamltest.NewTokenBuilder().PositionLine(2).Build()

		diff := yamltest.CompareTokens(want, got)
		assert.False(t, diff.Equal())
		assert.Contains(t, diff.Fields, "Position.Line")
	})

	t.Run("String() includes details", func(t *testing.T) {
		t.Parallel()

		want := yamltest.NewTokenBuilder().Value("a").Build()
		got := yamltest.NewTokenBuilder().Value("b").Build()

		diff := yamltest.CompareTokens(want, got)
		str := diff.String()
		assert.Contains(t, str, "token mismatch")
		assert.Contains(t, str, "want")
		assert.Contains(t, str, "got")
		assert.Contains(t, str, "Value")
	})
}

func TestCompareTokenSlices(t *testing.T) {
	t.Parallel()

	t.Run("equal token slices", func(t *testing.T) {
		t.Parallel()

		tb := yamltest.NewTokenBuilder().Type(token.StringType)
		tks := token.Tokens{
			tb.Value("a").PositionLine(1).Build(),
			tb.Value("b").PositionLine(2).Build(),
		}

		diff := yamltest.CompareTokenSlices(tks, tks)
		assert.True(t, diff.Equal())
		assert.Equal(t, "token slices equal", diff.String())
	})

	t.Run("count mismatch", func(t *testing.T) {
		t.Parallel()

		tb := yamltest.NewTokenBuilder()
		want := token.Tokens{tb.Build()}
		got := token.Tokens{tb.Build(), tb.Build()}

		diff := yamltest.CompareTokenSlices(want, got)
		assert.False(t, diff.Equal())
		assert.True(t, diff.CountMismatch)
		assert.Equal(t, 1, diff.WantCount)
		assert.Equal(t, 2, diff.GotCount)
		assert.Contains(t, diff.String(), "count mismatch")
	})

	t.Run("differences reported for each token", func(t *testing.T) {
		t.Parallel()

		tb := yamltest.NewTokenBuilder()
		want := token.Tokens{
			tb.Value("a").Build(),
			tb.Value("b").Build(),
		}
		got := token.Tokens{
			tb.Value("x").Build(),
			tb.Value("y").Build(),
		}

		diff := yamltest.CompareTokenSlices(want, got)
		assert.False(t, diff.Equal())
		require.Len(t, diff.Diffs, 2)
		assert.False(t, diff.Diffs[0].Equal())
		assert.False(t, diff.Diffs[1].Equal())
	})

	t.Run("empty slices equal", func(t *testing.T) {
		t.Parallel()

		diff := yamltest.CompareTokenSlices(token.Tokens{}, token.Tokens{})
		assert.True(t, diff.Equal())
	})
}

func TestDiffTokenFields(t *testing.T) {
	t.Parallel()

	t.Run("all fields equal returns empty", func(t *testing.T) {
		t.Parallel()

		tk := yamltest.NewTokenBuilder().
			Type(token.StringType).
			Value("test").
			Origin("test\n").
			CharacterType(token.CharacterTypeMiscellaneous).
			Indicator(token.NotIndicator).
			PositionLine(1).
			PositionColumn(1).
			PositionOffset(0).
			PositionIndentNum(0).
			PositionIndentLevel(0).
			Build()

		diffs := yamltest.DiffTokenFields(tk, tk)
		assert.Empty(t, diffs)
	})

	t.Run("reports all differing fields", func(t *testing.T) {
		t.Parallel()

		want := yamltest.NewTokenBuilder().
			Type(token.StringType).
			Value("a").
			Origin("a\n").
			CharacterType(token.CharacterTypeMiscellaneous).
			Indicator(token.NotIndicator).
			PositionLine(1).
			PositionColumn(1).
			PositionOffset(0).
			PositionIndentNum(2).
			PositionIndentLevel(1).
			Build()

		got := yamltest.NewTokenBuilder().
			Type(token.IntegerType).
			Value("b").
			Origin("b\n").
			CharacterType(token.CharacterTypeIndicator).
			Indicator(token.BlockStructureIndicator).
			PositionLine(2).
			PositionColumn(2).
			PositionOffset(10).
			PositionIndentNum(4).
			PositionIndentLevel(2).
			Build()

		diffs := yamltest.DiffTokenFields(want, got)
		assert.Contains(t, diffs, "Type")
		assert.Contains(t, diffs, "Value")
		assert.Contains(t, diffs, "Origin")
		assert.Contains(t, diffs, "CharacterType")
		assert.Contains(t, diffs, "Indicator")
		assert.Contains(t, diffs, "Position.Line")
		assert.Contains(t, diffs, "Position.Column")
		assert.Contains(t, diffs, "Position.Offset")
		assert.Contains(t, diffs, "Position.IndentNum")
		assert.Contains(t, diffs, "Position.IndentLevel")
	})
}

func TestTokenValidationError(t *testing.T) {
	t.Parallel()

	t.Run("single token error message", func(t *testing.T) {
		t.Parallel()

		err := &yamltest.TokenValidationError{
			Index:  -1,
			Which:  "want",
			Reason: yamltest.ErrNilToken,
		}
		assert.Equal(t, "want: token is nil", err.Error())
	})

	t.Run("indexed token error message", func(t *testing.T) {
		t.Parallel()

		err := &yamltest.TokenValidationError{
			Index:  5,
			Which:  "got",
			Reason: yamltest.ErrNilPosition,
		}
		assert.Equal(t, "token 5 got: token position is nil", err.Error())
	})

	t.Run("Unwrap returns reason", func(t *testing.T) {
		t.Parallel()

		err := &yamltest.TokenValidationError{
			Index:  -1,
			Which:  "want",
			Reason: yamltest.ErrNilToken,
		}
		assert.Equal(t, yamltest.ErrNilToken, err.Unwrap())
	})
}

func TestTokensDiff_String(t *testing.T) {
	t.Parallel()

	t.Run("shows all mismatches", func(t *testing.T) {
		t.Parallel()

		diff := yamltest.TokensDiff{
			WantCount: 3,
			GotCount:  3,
			Diffs: []yamltest.TokenDiff{
				{Fields: nil}, // Equal.
				{Fields: []string{"Value"}},
				{Fields: []string{"Type", "Origin"}},
			},
		}

		str := diff.String()
		assert.Contains(t, str, "token 1")
		assert.Contains(t, str, "token 2")
		assert.NotContains(t, str, "token 0") // Equal token not shown.
	})
}
