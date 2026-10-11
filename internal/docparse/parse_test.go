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
