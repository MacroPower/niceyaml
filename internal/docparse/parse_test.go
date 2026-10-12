package docparse_test

import (
	"testing"

	"github.com/goccy/go-yaml/ast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/docparse"
	"go.jacobcolvin.com/niceyaml/tokens"
)

func TestParse(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		// The text of each document node of the file.
		want     []string
		failures int
	}{
		"one document": {
			input: "a: 1\n",
			want:  []string{"a: 1"},
		},
		"a header after a header starts a document": {
			input: "---\n---\na: 1\n",
			want:  []string{"---", "---\na: 1"},
		},
		"a run the parser rejects keeps its place": {
			input:    "a: [\n---\nb: 1\n",
			want:     []string{"", "---\nb: 1"},
			failures: 1,
		},
		"a stream of markers holds one document": {
			input: "...\n...\n",
			want:  []string{"..."},
		},
		"an empty file holds one document": {
			input: "",
			want:  []string{""},
		},
		"a comment below a scalar root drops": {
			input: "a\n# c\n",
			want:  []string{"a"},
		},
		"a comment between a directive and its header drops": {
			input: "%YAML 1.2\n# c\n---\na: 1\n",
			want:  []string{"%YAML 1.2", "---\na: 1"},
		},
		"a comment before a ',' of a flow sequence drops": {
			input: "a: [1\n# c\n, 2]\n",
			want:  []string{"a: [1, 2]"},
		},
		"a comment before the '}' of a flow mapping drops": {
			input: "{a: 1\n# c\n}\n",
			want:  []string{"{a: 1}"},
		},
		"a comment where a flow mapping expects a key drops": {
			input: "a: {b: 1,\n# c\n  d: 2}\n",
			want:  []string{"a: {b: 1, d: 2}"},
		},
		"a comment between the entries of a flow sequence stays": {
			input: "a: [1,\n# c\n  2]\n",
			want:  []string{"a: [1, 2 # c]"},
		},
		"a comment after a '?' drops": {
			input: "? \n# c\n  a\n: 1\n",
			want:  []string{"? a: 1"},
		},
		"a comment before a ':' drops": {
			input: "? a\n# c\n: 1\n",
			want:  []string{"? a: 1"},
		},
		"a comment left of the key of an anchor above its value drops": {
			input: "m:\n  a: &x\n# c\n    1\n",
			want:  []string{"m:\n  a: &x 1"},
		},
		"a comment left of the key of an anchor above its sequence drops": {
			input: "m:\n  a: &x\n# c\n  - 1\n",
			want:  []string{"m:\n  a: &x\n  - 1"},
		},
		"a comment at the key of an anchor above its value stays": {
			input: "a: &x\n# c\n  1\n",
			want:  []string{"a: &x 1 # c"},
		},
		"a comment left of the key of an anchor above the next key stays": {
			input: "m:\n  a: &x\n# c\n  b: 1\n",
			want:  []string{"m:\n  a: &x\n  # c\n  b: 1"},
		},
		"a comment above a key left of the key of an anchor drops": {
			input: "m:\n  a: &x\n  # c\nb: 1\n",
			want:  []string{"m:\n  a: &x\nb: 1"},
		},
		"a comment between an anchor and the next key drops": {
			input: "a: &x\n# c\nb: 1\n",
			want:  []string{"a: &x\nb: 1"},
		},
		"a comment on the line of an anchor stays as the one below drops": {
			input: "a: &x # c\n# d\nb: 1\n",
			want:  []string{"a: &x # c\nb: 1"},
		},
		"a comment between an anchor in an entry and the next key drops": {
			input: "- a: &x\n  # c\n  b: 1\n",
			want:  []string{"- a: &x\n  b: 1"},
		},
		"a comment between an anchor and the next '?' key drops": {
			input: "a: &x\n# c\n? b\n: 1\n",
			want:  []string{"a: &x\n? b: 1"},
		},
		"a comment above a flow value at the key of an anchor stays": {
			input: "a: &x\n# c\n[1]\n",
			want:  []string{"a: &x [1] # c"},
		},
		"a comment above a scalar at the key of an anchor stays": {
			input: "a: &x\n# c\nb\n",
			want:  []string{"a: &x b # c"},
		},
		"a comment between an anchor and the next '-' drops": {
			input: "- &x\n# c\n- 1\n",
			want:  []string{"- &x\n- 1"},
		},
		"a comment above a scalar at the '-' of an anchor stays": {
			input: "- &x\n# c\na\n",
			want:  []string{"- &x a # c"},
		},
		"a comment below a '&' with no name stays, and the run fails": {
			input:    "a: &\n# c\nb: 1\n",
			want:     []string{""},
			failures: 1,
		},
		"a comment below an anchor at the root stays": {
			input: "&x\n# c\na: 1\n",
			want:  []string{"&x\n# c\na: 1"},
		},
		"a comment below an anchor after a tag stays": {
			input: "a: !t &x\n# c\n  b\n",
			want:  []string{"a: !t &x b # c"},
		},
		"a comment left of the root that closes the document of an anchor drops": {
			input: "  a: &x\n# c\n---\nb: 1\n",
			want:  []string{"  a: &x", "---\nb: 1"},
		},
		// The parser rejects the anchor without the comment, which it takes
		// as the value of the anchor.
		"a comment left of the root below an anchor on a line of its own stays": {
			input: "  a:\n    &x\n# c\n---\nb: 1\n",
			want:  []string{"  a: &x", "---\nb: 1"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := docparse.Parse(tokens.Tokenize(tc.input))

			got := make([]string, len(r.File.Docs))
			for i, doc := range r.File.Docs {
				got[i] = doc.String()
			}

			assert.Equal(t, tc.want, got)
			assert.Len(t, r.Failures, tc.failures)

			for _, f := range r.Failures {
				require.Error(t, f.Err)
				assert.Nil(t, f.Panic)
				assert.NotEmpty(t, f.Run)
			}
		})
	}
}

func TestParse_EmptyAnchor(t *testing.T) {
	t.Parallel()

	// The parser takes the comment below the anchor as its value.
	r := docparse.Parse(tokens.Tokenize("a: &x\n# c\n"))
	require.Empty(t, r.Failures)

	mapping, ok := r.File.Docs[0].Body.(*ast.MappingNode)
	require.True(t, ok, "got %T", r.File.Docs[0].Body)
	require.Len(t, mapping.Values, 1)

	anchor, ok := mapping.Values[0].Value.(*ast.AnchorNode)
	require.True(t, ok, "got %T", mapping.Values[0].Value)
	assert.IsType(t, &ast.NullNode{}, anchor.Value)
}

func TestParse_Index(t *testing.T) {
	t.Parallel()

	tks := tokens.Tokenize("a: 1\n# c\nb: [x, y]\n")

	r := docparse.Parse(tks)
	require.Len(t, r.Tokens, len(tks))
	require.Len(t, r.Index, len(tks))

	for i, tk := range r.Tokens {
		assert.NotSame(t, tks[i], tk)
		assert.Equal(t, i, r.Index[tk])
		assert.Equal(t, tks[i].Type, tk.Type)
		assert.Equal(t, tks[i].Origin, tk.Origin)
		assert.Equal(t, *tks[i].Position, *tk.Position)

		_, ok := r.Index[tks[i]]
		assert.False(t, ok, "token %d of the input", i)
	}
}

func TestParse_Panic(t *testing.T) {
	t.Parallel()

	// The go-yaml parser dereferences the position of this token.
	tks := tokens.Tokenize("a: 1\nb: 2\n")
	tks[2].Position = nil

	r := docparse.Parse(tks)
	require.Len(t, r.Failures, 1)

	f := r.Failures[0]
	require.NoError(t, f.Err)
	assert.NotNil(t, f.Panic)
	assert.Contains(t, string(f.Stack), "go-yaml/parser")
	assert.Len(t, f.Run, len(tks))

	// The stand-in of the run takes the place of its document.
	require.Len(t, r.File.Docs, 1)
	assert.Nil(t, r.File.Docs[0].Body)
}
