package yamltest_test

import (
	"errors"
	"fmt"
	"runtime"
	"testing"

	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
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
		"nil token": {
			input: token.Tokens{
				{Origin: "key"},
				nil,
				{Origin: ": value\n"},
			},
			want: "key: value\n",
		},
		"only nil tokens": {
			input: token.Tokens{nil, nil},
			want:  "",
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

	tcs := map[string]struct {
		input *token.Position
		want  string
	}{
		"nil position": {
			want: "<nil>",
		},
		"valid position": {
			input: &token.Position{Line: 10, Column: 5, Offset: 100, IndentNum: 2, IndentLevel: 1},
			want:  "Line=10 Col=5 Offset=100 IndentNum=2 IndentLevel=1",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, yamltest.FormatTokenPosition(tc.input))
		})
	}
}

func TestFormatToken(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input *token.Token
		want  string
	}{
		"nil token": {
			want: "<nil>",
		},
		"valid token": {
			input: &token.Token{
				Type:          token.StringType,
				Value:         "hello",
				Origin:        "hello\n",
				Indicator:     token.NotIndicator,
				CharacterType: token.CharacterTypeMiscellaneous,
				Position:      &token.Position{Line: 1, Column: 1},
			},
			want: `Type=String Value="hello" Origin="hello\n" Indicator=NotIndicator ` +
				`CharacterType=Miscellaneous Error="" ` +
				`Position=(Line=1 Col=1 Offset=0 IndentNum=0 IndentLevel=0)`,
		},
		"token with error": {
			input: yamltest.NewTokenBuilder().Error("bad").Build(),
			want: `Type=Unknown Value="" Origin="" Indicator=NotIndicator ` +
				`CharacterType=Indicator Error="bad" ` +
				`Position=(Line=0 Col=0 Offset=0 IndentNum=0 IndentLevel=0)`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, yamltest.FormatToken(tc.input))
		})
	}
}

func TestFormatTokens(t *testing.T) {
	t.Parallel()

	first := &token.Token{Value: "first", Position: &token.Position{Line: 1}}
	second := &token.Token{Value: "second", Position: &token.Position{Line: 2}}

	tcs := map[string]struct {
		want  string
		input token.Tokens
	}{
		"nil slice": {
			want: "<empty>",
		},
		"empty slice": {
			input: token.Tokens{},
			want:  "<empty>",
		},
		"single token": {
			input: token.Tokens{first},
			want:  "[0]\n" + yamltest.FormatToken(first),
		},
		"multiple tokens": {
			input: token.Tokens{first, second},
			want:  "[0]\n" + yamltest.FormatToken(first) + "\n[1]\n" + yamltest.FormatToken(second),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, yamltest.FormatTokens(tc.input))
		})
	}
}

func TestCompareContent(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		want string
		got  string
		msgs []string // Empty when the content is equal.
	}{
		"equal content": {
			want: "hello",
			got:  "hello",
		},
		"normalizes line endings": {
			want: "line1\nline2",
			got:  "line1\r\nline2",
		},
		"normalizes bare CR line endings": {
			want: "line1\nline2\n\nline3",
			got:  "line1\rline2\r\rline3\r",
		},
		"trims surrounding newlines": {
			want: "\nhello\n",
			got:  "hello",
		},
		"different content": {
			want: "hello",
			got:  "world",
			msgs: []string{"content mismatch", "hello", "world"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			diff := yamltest.CompareContent(tc.want, tc.got)
			if len(tc.msgs) == 0 {
				assert.True(t, diff.Equal())
				assert.Equal(t, "content equal", diff.String())

				return
			}

			assert.False(t, diff.Equal())

			for _, msg := range tc.msgs {
				assert.Contains(t, diff.String(), msg)
			}
		})
	}
}

func TestTokenBuilder(t *testing.T) {
	t.Parallel()

	t.Run("a new builder holds a zero position", func(t *testing.T) {
		t.Parallel()

		assert.Equal(t, &token.Token{Position: &token.Position{}}, yamltest.NewTokenBuilder().Build())
	})

	t.Run("each setter sets its own field", func(t *testing.T) {
		t.Parallel()

		got := yamltest.NewTokenBuilder().
			Type(token.StringType).
			CharacterType(token.CharacterTypeMiscellaneous).
			Indicator(token.BlockStructureIndicator).
			Value("test").
			Origin("test\n").
			Error("some error").
			PositionLine(7).
			PositionColumn(15).
			PositionOffset(100).
			PositionIndentNum(4).
			PositionIndentLevel(2).
			Build()

		want := &token.Token{
			Type:          token.StringType,
			CharacterType: token.CharacterTypeMiscellaneous,
			Indicator:     token.BlockStructureIndicator,
			Value:         "test",
			Origin:        "test\n",
			Error:         "some error",
			Position: &token.Position{
				Line:        7,
				Column:      15,
				Offset:      100,
				IndentNum:   4,
				IndentLevel: 2,
			},
		}

		assert.Equal(t, want, got)
	})

	t.Run("Position sets every position field", func(t *testing.T) {
		t.Parallel()

		pos := token.Position{Line: 5, Column: 10, Offset: 50, IndentNum: 2, IndentLevel: 1}

		assert.Equal(t, &pos, yamltest.NewTokenBuilder().Position(pos).Build().Position)
	})

	t.Run("a setter after Build leaves the built token alone", func(t *testing.T) {
		t.Parallel()

		b := yamltest.NewTokenBuilder().Value("first").PositionLine(1)
		tk1 := b.Build()
		tk2 := b.Value("second").PositionLine(2).Build()

		assert.Equal(t, "first", tk1.Value)
		assert.Equal(t, 1, tk1.Position.Line)
		assert.Equal(t, "second", tk2.Value)
		assert.Equal(t, 2, tk2.Position.Line)
	})

	t.Run("Clone returns independent builder", func(t *testing.T) {
		t.Parallel()

		base := yamltest.NewTokenBuilder().Type(token.StringType).PositionLine(1)
		clone := base.Clone()

		base.Value("base")
		clone.Value("clone").PositionLine(2)

		tkBase := base.Build()
		tkClone := clone.Build()

		assert.Equal(t, "base", tkBase.Value)
		assert.Equal(t, "clone", tkClone.Value)
		assert.Equal(t, token.StringType, tkBase.Type)
		assert.Equal(t, token.StringType, tkClone.Type)
		assert.Equal(t, 1, tkBase.Position.Line)
		assert.Equal(t, 2, tkClone.Position.Line)
	})
}

func TestValidateTokens(t *testing.T) {
	t.Parallel()

	tk := yamltest.NewTokenBuilder().Build()

	tcs := map[string]struct {
		err  error // Nil when the tokens are valid.
		msg  string
		want token.Tokens
		got  token.Tokens
	}{
		"equal length with valid tokens": {
			want: token.Tokens{tk, tk},
			got:  token.Tokens{tk, tk},
		},
		"empty slices": {
			want: token.Tokens{},
			got:  token.Tokens{},
		},
		"count mismatch": {
			want: token.Tokens{tk},
			got:  token.Tokens{tk, tk},
			err:  yamltest.ErrTokenCountMismatch,
			msg:  "want 1, got 2",
		},
		"nil want token": {
			want: token.Tokens{nil},
			got:  token.Tokens{tk},
			err:  yamltest.ErrNilToken,
			msg:  "token 0 want: token is nil",
		},
		"nil want position": {
			want: token.Tokens{{}},
			got:  token.Tokens{tk},
			err:  yamltest.ErrNilPosition,
			msg:  "token 0 want: token position is nil",
		},
		"nil got token": {
			want: token.Tokens{tk},
			got:  token.Tokens{nil},
			err:  yamltest.ErrNilToken,
			msg:  "token 0 got: token is nil",
		},
		"nil got position": {
			want: token.Tokens{tk, tk},
			got:  token.Tokens{tk, {}},
			err:  yamltest.ErrNilPosition,
			msg:  "token 1 got: token position is nil",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := yamltest.ValidateTokens(tc.want, tc.got)
			if tc.err == nil {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, tc.err)
			assert.Contains(t, err.Error(), tc.msg)

			// A count mismatch names no token, so it alone is no
			// TokenValidationError.
			var tve *yamltest.TokenValidationError

			assert.Equal(t, !errors.Is(tc.err, yamltest.ErrTokenCountMismatch), errors.As(err, &tve))
		})
	}
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

	t.Run("one field differs", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()

		// Each case differs in the field that names it.
		tcs := map[string]struct {
			want *token.Token
			got  *token.Token
		}{
			"Type": {
				want: tkb.Clone().Type(token.StringType).Build(),
				got:  tkb.Clone().Type(token.IntegerType).Build(),
			},
			"Value": {
				want: tkb.Clone().Value("a").Build(),
				got:  tkb.Clone().Value("b").Build(),
			},
			"Origin": {
				want: tkb.Clone().Origin("a\n").Build(),
				got:  tkb.Clone().Origin("b\n").Build(),
			},
			"Error": {
				want: tkb.Clone().Error("bad").Build(),
				got:  tkb.Build(),
			},
			"Position.Line": {
				want: tkb.Clone().PositionLine(1).Build(),
				got:  tkb.Clone().PositionLine(2).Build(),
			},
		}

		for field, tc := range tcs {
			t.Run(field, func(t *testing.T) {
				t.Parallel()

				diff := yamltest.CompareTokens(tc.want, tc.got)
				assert.False(t, diff.Equal())
				assert.Equal(t, []string{field}, diff.Fields)
				assert.Equal(t, []string{field}, yamltest.DiffTokenFields(tc.want, tc.got))
			})
		}
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

	tkb := yamltest.NewTokenBuilder()
	a, b, c := tkb.Value("a").Build(), tkb.Value("b").Build(), tkb.Value("c").Build()
	x, y := tkb.Value("x").Build(), tkb.Value("y").Build()

	tcs := map[string]struct {
		str   string // What the diff prints.
		want  token.Tokens
		got   token.Tokens
		equal bool
	}{
		"equal token slices": {
			want:  token.Tokens{a, b},
			got:   token.Tokens{a, b},
			equal: true,
			str:   "token slices equal",
		},
		"empty slices": {
			want:  token.Tokens{},
			got:   token.Tokens{},
			equal: true,
			str:   "token slices equal",
		},
		"count mismatch": {
			want: token.Tokens{a},
			got:  token.Tokens{a, b},
			str:  "token count mismatch: want 1, got 2",
		},
		"only the tokens that differ are listed": {
			want: token.Tokens{a, b, c},
			got:  token.Tokens{a, x, y},
			str: "token 1: " + yamltest.CompareTokens(b, x).String() +
				"\ntoken 2: " + yamltest.CompareTokens(c, y).String(),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			diff := yamltest.CompareTokenSlices(tc.want, tc.got)

			assert.Equal(t, tc.equal, diff.Equal())
			assert.Equal(t, tc.str, diff.String())
			assert.Equal(t, len(tc.want), diff.WantCount)
			assert.Equal(t, len(tc.got), diff.GotCount)
			assert.Equal(t, len(tc.want) != len(tc.got), diff.CountMismatch())
		})
	}
}

// recordingTB is a [testing.TB] that records a reported failure instead of
// failing the test. Methods it does not override panic through the nil
// embedded TB.
type recordingTB struct {
	testing.TB

	msg    string
	failed bool
}

func (r *recordingTB) Helper() {}

// Name overrides the embedded TB because testify adds the test name to every
// failure message when t has a Name method.
func (r *recordingTB) Name() string {
	return "recordingTB"
}

func (r *recordingTB) Errorf(format string, args ...any) {
	r.failed = true
	r.msg += fmt.Sprintf(format, args...)
}

func (r *recordingTB) FailNow() {
	r.failed = true

	runtime.Goexit()
}

func TestRequireTokensEqual(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		want token.Tokens
		got  token.Tokens
		msgs []string // Empty when the helper passes.
	}{
		"equal slices pass": {
			want: token.Tokens{
				yamltest.NewTokenBuilder().Value("a").Build(),
				yamltest.NewTokenBuilder().Value("b").Build(),
			},
			got: token.Tokens{
				yamltest.NewTokenBuilder().Value("a").Build(),
				yamltest.NewTokenBuilder().Value("b").Build(),
			},
		},
		"value mismatch fails": {
			want: token.Tokens{yamltest.NewTokenBuilder().Value("a").Build()},
			got:  token.Tokens{yamltest.NewTokenBuilder().Value("b").Build()},
			msgs: []string{"token 0", "Value"},
		},
		"count mismatch fails": {
			want: token.Tokens{yamltest.NewTokenBuilder().Build()},
			got: token.Tokens{
				yamltest.NewTokenBuilder().Build(),
				yamltest.NewTokenBuilder().Build(),
			},
			msgs: []string{"token count mismatch"},
		},
		"nil got token fails": {
			want: token.Tokens{yamltest.NewTokenBuilder().Build()},
			got:  token.Tokens{nil},
			msgs: []string{"token 0 got: token is nil"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rec := &recordingTB{}
			done := make(chan struct{})

			// FailNow ends the goroutine, as it does for a real test.
			go func() {
				defer close(done)

				yamltest.RequireTokensEqual(rec, tc.want, tc.got)
			}()

			<-done

			if len(tc.msgs) == 0 {
				assert.False(t, rec.failed, rec.msg)

				return
			}

			require.True(t, rec.failed)

			for _, msg := range tc.msgs {
				assert.Contains(t, rec.msg, msg)
			}
		})
	}
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
			Error("bad").
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

		assert.ElementsMatch(t, []string{
			"Type",
			"Value",
			"Origin",
			"CharacterType",
			"Indicator",
			"Error",
			"Position.Line",
			"Position.Column",
			"Position.Offset",
			"Position.IndentNum",
			"Position.IndentLevel",
		}, yamltest.DiffTokenFields(want, got))
	})
}
