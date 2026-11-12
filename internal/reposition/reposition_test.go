package reposition_test

import (
	"fmt"
	"iter"
	"slices"
	"testing"

	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/docparse"
	"go.jacobcolvin.com/niceyaml/internal/reposition"
	"go.jacobcolvin.com/niceyaml/tokens"
)

func TestReset(t *testing.T) {
	t.Parallel()

	// Each document of a text resets in place to the tokens and the links
	// that ResetPositions gives its clones. The Origins of the tokens hold
	// the text of the document.
	tcs := map[string]struct {
		input string
	}{
		"whole stream": {
			input: "a: 1\nb:\n  - c\n",
		},
		"swallowed indicator": {
			input: "\t- a\nb: 1\n",
		},
		"documents cut from a stream": {
			input: "a: 1\n---\nb: 2\n...\nc: |\n---\nd: 3\n",
		},
		"block scalar before a header": {
			input: "a: |+\n---\nb: 1\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			want := resetClones(docparse.SplitGroups(tokens.Tokenize(tc.input)))

			var got []token.Tokens

			for _, doc := range docparse.SplitGroups(tokens.Tokenize(tc.input)) {
				reset := reposition.Reset(doc)

				// The result holds the tokens of the document.
				require.Len(t, reset, len(doc))

				for i, tk := range reset {
					require.Same(t, doc[i], tk)
				}

				got = append(got, reset)
			}

			require.Len(t, got, len(want))

			for i := range want {
				assert.Equal(t, positionsOf(want[i]), positionsOf(got[i]))
				assert.Equal(t, origins(want[i]), origins(got[i]))
				assertLinked(t, got[i])
			}
		})
	}

	t.Run("drops nil tokens", func(t *testing.T) {
		t.Parallel()

		tks := tokens.Tokenize("a: 1\n")
		input := slices.Insert(slices.Clone(tks), 1, nil)

		got := reposition.Reset(input)

		require.Len(t, got, len(tks))
		assert.Equal(t, positionsOf(tokens.ResetPositions(tokens.Tokenize("a: 1\n"))), positionsOf(got))
		assertLinked(t, got)
	})
}

// origins returns the Origin of each token of tks.
func origins(tks token.Tokens) []string {
	got := make([]string, 0, len(tks))
	for _, tk := range tks {
		got = append(got, tk.Origin)
	}

	return got
}

// assertLinked asserts that the tokens of tks link to each other in order
// through Next and Prev, and to no token outside tks.
func assertLinked(t *testing.T, tks token.Tokens) {
	t.Helper()

	for i, tk := range tks {
		var prev, next *token.Token

		if i > 0 {
			prev = tks[i-1]
		}

		if i < len(tks)-1 {
			next = tks[i+1]
		}

		assert.Same(t, prev, tk.Prev, "token %d", i)
		assert.Same(t, next, tk.Next, "token %d", i)
	}
}

// resetClones collects the groups of seq as [tokens.ResetPositions] resets
// their clones.
func resetClones(seq iter.Seq2[int, token.Tokens]) []token.Tokens {
	var result []token.Tokens

	for _, tks := range seq {
		result = append(result, tokens.ResetPositions(tks))
	}

	return result
}

// positionsOf returns the position of each token as "line:column:offset".
func positionsOf(tks token.Tokens) []string {
	got := make([]string, 0, len(tks))
	for _, tk := range tks {
		got = append(got, fmt.Sprintf("%d:%d:%d", tk.Position.Line, tk.Position.Column, tk.Position.Offset))
	}

	return got
}
