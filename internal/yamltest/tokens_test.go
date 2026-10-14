package yamltest_test

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
)

func TestDumpTokenOrigins(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		want  string
		input token.Tokens
	}{
		"empty slice": {input: token.Tokens{}, want: ""},
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
		"only nil tokens": {input: token.Tokens{nil, nil}, want: ""},
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
		"nil position": {want: "<nil>"},
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
		"nil token": {want: "<nil>"},
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
		"nil slice":   {want: "<empty>"},
		"empty slice": {input: token.Tokens{}, want: "<empty>"},
		"single token": {
			input: token.Tokens{first},
			want:  stringtest.JoinLF("[0]", yamltest.FormatToken(first)),
		},
		"multiple tokens": {
			input: token.Tokens{first, second},
			want: stringtest.JoinLF(
				"[0]",
				yamltest.FormatToken(first),
				"[1]",
				yamltest.FormatToken(second),
			),
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
		"equal content":           {want: "hello", got: "hello"},
		"normalizes line endings": {want: "line1\nline2", got: "line1\r\nline2"},
		"normalizes bare CR line endings": {
			want: stringtest.JoinLF("line1", "line2", "", "line3"),
			got:  "line1\rline2\r\rline3\r",
		},
		"trims surrounding newlines": {want: "\nhello\n", got: "hello"},
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

// requireValidation requires err to be nil when want is nil, and to wrap
// want and hold msg in its text otherwise.
func requireValidation(tb testing.TB, err, want error, msg string) {
	tb.Helper()

	if want == nil {
		require.NoError(tb, err)

		return
	}

	require.ErrorIs(tb, err, want)
	assert.Contains(tb, err.Error(), msg)
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
		"equal length with valid tokens": {want: token.Tokens{tk, tk}, got: token.Tokens{tk, tk}},
		"empty slices":                   {want: token.Tokens{}, got: token.Tokens{}},
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
			requireValidation(t, err, tc.err, tc.msg)

			// A count mismatch names no token, so it alone is no
			// TokenValidationError.
			var tve *yamltest.TokenValidationError

			assert.Equal(t, tc.err != nil && !errors.Is(tc.err, yamltest.ErrTokenCountMismatch), errors.As(err, &tve))
		})
	}
}

func TestValidateTokenLinks(t *testing.T) {
	t.Parallel()

	// Chain returns three tokens that Prev and Next link in order.
	chain := func() token.Tokens {
		var tks token.Tokens

		for _, origin := range []string{"a", "b", "c"} {
			tks.Add(yamltest.NewTokenBuilder().Origin(origin).Build())
		}

		return tks
	}

	other := yamltest.NewTokenBuilder().Origin("x").Build()

	tcs := map[string]struct {
		// Breaks a link of the chain, or drops a token from it.
		edit func(tks token.Tokens)
		err  error // Nil when every link fits.
		msg  string
	}{
		"linked chain": {edit: func(token.Tokens) {}},
		"first token with a Prev": {
			edit: func(tks token.Tokens) { tks[0].Prev = other },
			err:  yamltest.ErrTokenLink,
			msg:  `token 0 "a" Prev`,
		},
		"last token with a Next": {
			edit: func(tks token.Tokens) { tks[2].Next = other },
			err:  yamltest.ErrTokenLink,
			msg:  `token 2 "c" Next`,
		},
		"Prev that skips a token": {
			edit: func(tks token.Tokens) { tks[2].Prev = tks[0] },
			err:  yamltest.ErrTokenLink,
			msg:  `token 2 "c" Prev`,
		},
		"Next that is nil inside the chain": {
			edit: func(tks token.Tokens) { tks[1].Next = nil },
			err:  yamltest.ErrTokenLink,
			msg:  `token 1 "b" Next`,
		},
		"nil token": {
			edit: func(tks token.Tokens) { tks[1] = nil },
			err:  yamltest.ErrNilToken,
			msg:  "token 1 got",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := chain()
			tc.edit(tks)

			requireValidation(t, yamltest.ValidateTokenLinks(tks), tc.err, tc.msg)
		})
	}

	t.Run("no tokens", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, yamltest.ValidateTokenLinks(nil))
	})
}

func TestCompareTokens(t *testing.T) {
	t.Parallel()

	tkb := yamltest.NewTokenBuilder()
	tk := tkb.Clone().Type(token.StringType).Value("test").Origin("test\n").PositionLine(1).PositionColumn(1).Build()

	tcs := map[string]struct {
		want   *token.Token
		got    *token.Token
		fields []string // The fields that differ, empty when the tokens are equal.
	}{
		"equal tokens": {want: tk, got: tk},
		"Type differs": {
			want:   tkb.Clone().Type(token.StringType).Build(),
			got:    tkb.Clone().Type(token.IntegerType).Build(),
			fields: []string{"Type"},
		},
		"Value differs": {
			want:   tkb.Clone().Value("a").Build(),
			got:    tkb.Clone().Value("b").Build(),
			fields: []string{"Value"},
		},
		"Origin differs": {
			want:   tkb.Clone().Origin("a\n").Build(),
			got:    tkb.Clone().Origin("b\n").Build(),
			fields: []string{"Origin"},
		},
		"Error differs": {
			want:   tkb.Clone().Error("bad").Build(),
			got:    tkb.Build(),
			fields: []string{"Error"},
		},
		"Position.Line differs": {
			want:   tkb.Clone().PositionLine(1).Build(),
			got:    tkb.Clone().PositionLine(2).Build(),
			fields: []string{"Position.Line"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			diff := yamltest.CompareTokens(tc.want, tc.got)
			assert.Equal(t, tc.fields, diff.Fields)
			assert.Equal(t, tc.fields, yamltest.DiffTokenFields(tc.want, tc.got))

			if len(tc.fields) == 0 {
				assert.True(t, diff.Equal())
				assert.Equal(t, "tokens equal", diff.String())

				return
			}

			assert.False(t, diff.Equal())

			// The text shows both tokens and names the fields that differ.
			str := diff.String()
			for _, msg := range []string{"token mismatch", "want", "got"} {
				assert.Contains(t, str, msg)
			}

			assert.Contains(t, str, "differences: "+strings.Join(tc.fields, ", "))
		})
	}
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
			str: stringtest.JoinLF(
				"token 1: "+yamltest.CompareTokens(b, x).String(),
				"token 2: "+yamltest.CompareTokens(c, y).String(),
			),
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

// record returns a new [recordingTB] after a call of fn with it. The call
// runs on a goroutine of its own, because FailNow ends the goroutine it
// runs on, as it does for a real test.
func record(fn func(tb *recordingTB)) *recordingTB {
	rec := &recordingTB{}
	done := make(chan struct{})

	go func() {
		defer close(done)

		fn(rec)
	}()

	<-done

	return rec
}

func TestRequireTokensEqual(t *testing.T) {
	t.Parallel()

	// The clones hold the fields of a and b under pointers of their own.
	a := yamltest.NewTokenBuilder().Value("a").Build()
	b := yamltest.NewTokenBuilder().Value("b").Build()

	tcs := map[string]struct {
		want token.Tokens
		got  token.Tokens
		msgs []string // Empty when the helper passes.
	}{
		"equal slices pass": {want: token.Tokens{a, b}, got: token.Tokens{a.Clone(), b.Clone()}},
		"value mismatch fails": {
			want: token.Tokens{a},
			got:  token.Tokens{b},
			msgs: []string{"token 0", "Value"},
		},
		"count mismatch fails": {
			want: token.Tokens{a},
			got:  token.Tokens{a, a},
			msgs: []string{"token count mismatch"},
		},
		"nil got token fails": {
			want: token.Tokens{a},
			got:  token.Tokens{nil},
			msgs: []string{"token 0 got: token is nil"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rec := record(func(tb *recordingTB) {
				yamltest.RequireTokensEqual(tb, tc.want, tc.got)
			})

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

	// Every field differs between the two tokens.
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
}

func TestTokenPlace(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input *token.Token
		want  string
	}{
		"nil token":                {},
		"token without a position": {input: &token.Token{Value: "a"}},
		"token": {
			input: yamltest.NewTokenBuilder().Value("a").PositionLine(2).PositionColumn(3).Build(),
			want:  "a@2:3",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, yamltest.TokenPlace(tc.input))
		})
	}
}

func TestTokenValues(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input token.Tokens
		want  []string
	}{
		"no tokens": {},
		"tokens": {
			input: token.Tokens{{Value: "a"}, {Value: ""}, {Value: "b"}},
			want:  []string{"a", "", "b"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, yamltest.TokenValues(tc.input))
		})
	}
}
