package astnode_test

import (
	"testing"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
)

func TestDropped(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		// The value of each tag and of the token that the parse leaves out
		// after it.
		want []string
	}{
		"null tag on an anchored flow mapping": {
			input: stringtest.LinesLF("c: !!null &k1 {B: &k0 {x: 1}}", "d: *k0", "a: 1"),
			want:  []string{"!!null d"},
		},
		"string tag on an anchored flow sequence": {
			input: stringtest.LinesLF("c: !!str &a [1]", "<<: *a", "a: 1"),
			want:  []string{"!!str <<"},
		},
		"string tag on an anchored block mapping": {
			input: stringtest.LinesLF("c: !!str &a", "  k: 1", "d: 2", "a: 1"),
			want:  []string{"!!str d"},
		},
		"nested mapping": {
			input: stringtest.LinesLF("a:", "  c: !!int &a [1]", "  d: 2", "e: 3"),
			want:  []string{"!!int d"},
		},
		"each tag": {
			input: stringtest.LinesLF("c: !!str &a [1]", "d: 2", "e: 3", "f: !!str &b [2]", "g: 4", "h: 5"),
			want:  []string{"!!str d", "!!str g"},
		},
		"comment line after the collection": {
			input: stringtest.LinesLF("p: !!str &k [1]", "# l", "q: 1"),
		},
		"line comment after the collection": {
			// The parser takes the comment as the line comment of the
			// collection, so it skips the entry below it.
			input: stringtest.LinesLF("p: !!str &k [1] # l", "q: 1", "r: 2"),
			want:  []string{"!!str q"},
		},
		"collection at the end": {
			input: stringtest.LinesLF("a: 1", "p: !!str &k {a: 1}"),
		},
		"anchored scalar on the next line": {
			input: stringtest.LinesLF("c: !!int &a", "  3", "d: 2"),
			want:  []string{"!!int d"},
		},
		"anchored scalar": {
			input: stringtest.LinesLF("c: !!null &a 3", "d: 2"),
		},
		"local tag on an anchored flow mapping": {
			input: stringtest.LinesLF("a: !t &x {k: 1}", "b: 2"),
		},
		"string tag on an anchored scalar in a flow mapping": {
			// The tree does not hold the "," of a flow mapping.
			input: "{x: !!str &k a, y: 2}",
		},
		"local tag on an anchored flow sequence in a flow mapping": {
			input: "{x: !t &k [1], y: 2}",
		},
		"null tag on an anchored scalar in a nested flow mapping": {
			input: stringtest.LinesLF("a: {x: !!null &k ~, y: 2}", "b: 3"),
		},
		"string tag on an anchored scalar in a block sequence": {
			input: stringtest.LinesLF("- !!str &k a", "- 2"),
		},
		"string tag on an anchored scalar in a flow sequence": {
			input: "[!!str &k a, 2]",
		},
		"anchor before the tag": {
			input: stringtest.LinesLF("a: &x !t {k: 1}", "b: 2"),
		},
		"document after the collection": {
			input: stringtest.LinesLF("p: !!str &k [1]", "# l", "---", "q: 1"),
		},
		"marker after the collection": {
			input: stringtest.LinesLF("p: !!str &k [1]", "...", "---", "q: 1"),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			body, tks := parseTokens(t, tc.input)

			var got []string

			stopped := astnode.Dropped(body, tks, func(tag *ast.TagNode, tk *token.Token) bool {
				got = append(got, tag.GetToken().Value+" "+tk.Value)

				return true
			})

			assert.False(t, stopped)
			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("stop", func(t *testing.T) {
		t.Parallel()

		body, tks := parseTokens(t,
			stringtest.LinesLF("c: !!str &a [1]", "d: 2", "e: 3", "f: !!str &b [2]", "g: 4", "h: 5"))

		calls := 0

		stopped := astnode.Dropped(body, tks, func(*ast.TagNode, *token.Token) bool {
			calls++

			return false
		})

		assert.True(t, stopped)
		assert.Equal(t, 1, calls)
	})

	t.Run("nil node", func(t *testing.T) {
		t.Parallel()

		stopped := astnode.Dropped(nil, nil, func(*ast.TagNode, *token.Token) bool {
			t.Fatal("found called")

			return false
		})

		assert.False(t, stopped)
	})
}

// parseTokens returns the body of the first document of input, comments
// included, and the tokens that the parser read.
func parseTokens(t *testing.T, input string) (ast.Node, token.Tokens) {
	t.Helper()

	tks := lexer.Tokenize(input)

	file, err := parser.Parse(tks, parser.ParseComments)
	require.NoError(t, err)
	require.NotEmpty(t, file.Docs)

	return file.Docs[0].Body, tks
}
