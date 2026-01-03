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

func TestAssertContentEqual(t *testing.T) {
	t.Parallel()

	t.Run("equal content", func(t *testing.T) {
		t.Parallel()

		mockT := &testing.T{}
		yamltest.AssertContentEqual(mockT, "hello", "hello")
		assert.False(t, mockT.Failed())
	})

	t.Run("normalizes line endings", func(t *testing.T) {
		t.Parallel()

		mockT := &testing.T{}
		yamltest.AssertContentEqual(mockT, "line1\nline2", "line1\r\nline2")
		assert.False(t, mockT.Failed())
	})

	t.Run("trims surrounding newlines", func(t *testing.T) {
		t.Parallel()

		mockT := &testing.T{}
		yamltest.AssertContentEqual(mockT, "\nhello\n", "hello")
		assert.False(t, mockT.Failed())
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

		// The builder is mutable - setters modify internal state and return
		// the same builder. Build() returns clones of the current state.
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

func TestRequireTokenValid(t *testing.T) {
	t.Parallel()

	t.Run("valid tokens pass", func(t *testing.T) {
		t.Parallel()

		mockT := &testing.T{}
		want := &token.Token{Position: &token.Position{}}
		got := &token.Token{Position: &token.Position{}}

		yamltest.RequireTokenValid(mockT, want, got, "test")
		assert.False(t, mockT.Failed())
	})

	// Note: Failure cases for require functions cannot be tested with mock T
	// because require.NotNil calls t.FailNow() which uses runtime.Goexit().
}

func TestRequireTokensValid(t *testing.T) {
	t.Parallel()

	t.Run("equal length with valid tokens", func(t *testing.T) {
		t.Parallel()

		mockT := &testing.T{}
		want := token.Tokens{
			{Position: &token.Position{}},
			{Position: &token.Position{}},
		}
		got := token.Tokens{
			{Position: &token.Position{}},
			{Position: &token.Position{}},
		}

		yamltest.RequireTokensValid(mockT, want, got)
		assert.False(t, mockT.Failed())
	})

	t.Run("empty slices pass", func(t *testing.T) {
		t.Parallel()

		mockT := &testing.T{}
		yamltest.RequireTokensValid(mockT, token.Tokens{}, token.Tokens{})
		assert.False(t, mockT.Failed())
	})

	// Note: Failure cases for require functions cannot be tested with mock T
	// because require.Fail calls t.FailNow() which uses runtime.Goexit().
}

func TestAssertTokenEqual(t *testing.T) {
	t.Parallel()

	t.Run("equal tokens pass", func(t *testing.T) {
		t.Parallel()

		mockT := &testing.T{}
		tk := &token.Token{
			Type:   token.StringType,
			Value:  "test",
			Origin: "test\n",
			Position: &token.Position{
				Line:   1,
				Column: 1,
			},
		}

		yamltest.AssertTokenEqual(mockT, tk, tk, "test")
		assert.False(t, mockT.Failed())
	})

	t.Run("different Type fails", func(t *testing.T) {
		t.Parallel()

		mockT := &testing.T{}
		want := &token.Token{Type: token.StringType, Position: &token.Position{}}
		got := &token.Token{Type: token.IntegerType, Position: &token.Position{}}

		yamltest.AssertTokenEqual(mockT, want, got, "test")
		assert.True(t, mockT.Failed())
	})

	t.Run("different Value fails", func(t *testing.T) {
		t.Parallel()

		mockT := &testing.T{}
		want := &token.Token{Value: "a", Position: &token.Position{}}
		got := &token.Token{Value: "b", Position: &token.Position{}}

		yamltest.AssertTokenEqual(mockT, want, got, "test")
		assert.True(t, mockT.Failed())
	})

	t.Run("different Origin fails", func(t *testing.T) {
		t.Parallel()

		mockT := &testing.T{}
		want := &token.Token{Origin: "a\n", Position: &token.Position{}}
		got := &token.Token{Origin: "b\n", Position: &token.Position{}}

		yamltest.AssertTokenEqual(mockT, want, got, "test")
		assert.True(t, mockT.Failed())
	})

	t.Run("different Position.Line fails", func(t *testing.T) {
		t.Parallel()

		mockT := &testing.T{}
		want := &token.Token{Position: &token.Position{Line: 1}}
		got := &token.Token{Position: &token.Position{Line: 2}}

		yamltest.AssertTokenEqual(mockT, want, got, "test")
		assert.True(t, mockT.Failed())
	})
}

func TestAssertTokensEqual(t *testing.T) {
	t.Parallel()

	t.Run("equal token slices pass", func(t *testing.T) {
		t.Parallel()

		mockT := &testing.T{}
		tks := token.Tokens{
			{Type: token.StringType, Value: "a", Position: &token.Position{Line: 1}},
			{Type: token.StringType, Value: "b", Position: &token.Position{Line: 2}},
		}

		yamltest.AssertTokensEqual(mockT, tks, tks)
		assert.False(t, mockT.Failed())
	})

	t.Run("differences reported for each token", func(t *testing.T) {
		t.Parallel()

		mockT := &testing.T{}
		want := token.Tokens{
			{Value: "a", Position: &token.Position{}},
			{Value: "b", Position: &token.Position{}},
		}
		got := token.Tokens{
			{Value: "x", Position: &token.Position{}},
			{Value: "y", Position: &token.Position{}},
		}

		yamltest.AssertTokensEqual(mockT, want, got)
		assert.True(t, mockT.Failed())
	})
}
