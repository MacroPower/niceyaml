package docparse_test

import (
	"fmt"
	"testing"

	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/internal/docparse"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/tokens"
)

func TestSplitGroups(t *testing.T) {
	t.Parallel()

	tkb := yamltest.NewTokenBuilder()
	str := func(value string) *token.Token {
		return tkb.Clone().Type(token.StringType).Value(value).Build()
	}
	colon := func() *token.Token {
		return tkb.Clone().Type(token.MappingValueType).Value(":").Build()
	}
	header := func() *token.Token {
		return tkb.Clone().Type(token.DocumentHeaderType).Value("---").Build()
	}
	end := func() *token.Token {
		return tkb.Clone().Type(token.DocumentEndType).Value("...").Build()
	}
	comment := func(value string) *token.Token {
		return tkb.Clone().Type(token.CommentType).Value(value).Build()
	}

	// RequireGroups requires each of groups to hold the next tokens of
	// input, as many as want gives for it, and returns the tokens of input
	// after them.
	requireGroups := func(t *testing.T, input token.Tokens, want []int, groups []token.Tokens) token.Tokens {
		t.Helper()

		require.Len(t, groups, len(want))

		for i, n := range want {
			yamltest.RequireTokensEqual(t, input[:n], groups[i])

			input = input[n:]
		}

		return input
	}

	tcs := map[string]struct {
		input token.Tokens
		want  []int // Number of tokens in each group.
	}{
		"nil input":   {input: nil},
		"empty slice": {input: token.Tokens{}},
		"single doc no header": {
			input: token.Tokens{str("key"), colon(), str("value")},
			want:  []int{3},
		},
		"single doc with header": {
			input: token.Tokens{header(), str("key"), colon(), str("value")},
			want:  []int{4},
		},
		"doc with end marker": {
			input: token.Tokens{str("key"), colon(), str("value"), end()},
			want:  []int{4},
		},
		"end marker closes a group": {
			input: token.Tokens{str("a"), colon(), str("1"), end(), str("b"), colon(), str("2")},
			want:  []int{4, 3},
		},
		"end marker followed by header": {
			input: token.Tokens{str("a"), end(), header(), str("b")},
			want:  []int{2, 2},
		},
		"two docs": {
			input: token.Tokens{str("key1"), colon(), str("v1"), header(), str("key2"), colon(), str("v2")},
			want:  []int{3, 4},
		},
		"three docs with headers": {
			input: token.Tokens{header(), str("doc1"), header(), str("doc2"), header(), str("doc3")},
			want:  []int{2, 2, 2},
		},
		// The comment above the first header forms a group of its own,
		// which the Result folds into the document below it.
		"comment above the first header": {
			input: token.Tokens{comment("c"), header(), str("a")},
			want:  []int{1, 2},
		},
		// A comment above a later header stays with the group above it.
		"comment above a later header": {
			input: token.Tokens{str("a"), comment("c"), header(), str("b")},
			want:  []int{2, 2},
		},
		"comment after an end marker": {
			input: token.Tokens{str("a"), end(), comment("c")},
			want:  []int{2, 1},
		},
		"leading end marker": {
			input: token.Tokens{end(), str("a")},
			want:  []int{1, 1},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var got []token.Tokens

			for i, group := range docparse.SplitGroups(tc.input) {
				assert.Equal(t, len(got), i, "group index")

				got = append(got, group)
			}

			rest := requireGroups(t, tc.input, tc.want, got)
			assert.Empty(t, rest, "every token belongs to a group")
		})
	}

	t.Run("stops when the caller does", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input token.Tokens
			want  []int // Number of tokens in each group the caller reads.
		}{
			"at the first group": {
				input: token.Tokens{str("doc1"), header(), str("doc2")},
				want:  []int{1},
			},
			"at the second group": {
				input: token.Tokens{str("doc1"), header(), str("doc2"), header(), str("doc3")},
				want:  []int{1, 2},
			},
			"at an end marker": {
				input: token.Tokens{str("doc1"), end(), str("doc2")},
				want:  []int{2},
			},
			"single group": {
				input: token.Tokens{str("only")},
				want:  []int{1},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				var got []token.Tokens

				for _, tks := range docparse.SplitGroups(tc.input) {
					got = append(got, tks)

					if len(got) == len(tc.want) {
						break
					}
				}

				requireGroups(t, tc.input, tc.want, got)
			})
		}
	})

	t.Run("skips nil tokens", func(t *testing.T) {
		t.Parallel()

		want := token.Tokens{str("key"), colon(), str("value")}
		input := token.Tokens{nil, want[0], want[1], nil, want[2], nil}

		var got []token.Tokens

		for _, group := range docparse.SplitGroups(input) {
			got = append(got, group)
		}

		require.Len(t, got, 1)
		yamltest.RequireTokensEqual(t, want, got[0])
	})

	t.Run("yields the tokens passed in as they are", func(t *testing.T) {
		t.Parallel()

		input := tokens.Tokenize(stringtest.LinesLF(
			"a: 1",
			"---",
			"b: 2",
		))

		var got []token.Tokens

		for _, group := range docparse.SplitGroups(input) {
			got = append(got, group)
		}

		require.Len(t, got, 2)
		require.Len(t, got[1], 4)

		// The group holds the same pointers, not clones. Its tokens keep
		// their positions and their links to the group before it.
		for i, tk := range got[1] {
			assert.Same(t, input[len(got[0])+i], tk, "token %d", i)
		}

		head := got[1][0]
		pos := head.Position
		assert.Equal(t, "2:1:6", fmt.Sprintf("%d:%d:%d", pos.Line, pos.Column, pos.Offset))
		assert.Same(t, got[0][len(got[0])-1], head.Prev)
	})
}
