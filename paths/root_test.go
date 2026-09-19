package paths_test

import (
	"testing"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/paths"
)

// emptyDocument returns the last document of input, which the tests below
// write as an explicit "---" header with nothing under it.
func emptyDocument(t *testing.T, input string) *ast.DocumentNode {
	t.Helper()

	docs, err := niceyaml.NewSourceFromString(input).Documents()
	require.NoError(t, err)
	require.NotEmpty(t, docs)

	node := docs[len(docs)-1].Node()
	require.Nil(t, node.Body, "the document should have no body")

	return node
}

func TestPath_EmptyDocument(t *testing.T) {
	t.Parallel()

	t.Run("the root is the null below the header", func(t *testing.T) {
		t.Parallel()

		doc := emptyDocument(t, "a: 1\n---\n")

		node, err := paths.Root().Node(doc)
		require.NoError(t, err)
		assert.Equal(t, ast.NullType, node.Type())

		tk, err := paths.Root().Token(doc)
		require.NoError(t, err)
		assert.Equal(t, token.DocumentHeaderType, tk.Type)
		assert.Equal(t, 2, tk.Position.Line)
	})

	t.Run("a path with segments reaches nothing", func(t *testing.T) {
		t.Parallel()

		doc := emptyDocument(t, "a: 1\n---\n")

		tcs := map[string]struct {
			path paths.Path
		}{
			"child": {path: paths.Root().Child("a")},
			"index": {path: paths.Root().Index(0)},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				_, err := tc.path.Node(doc)
				require.ErrorIs(t, err, paths.ErrNoDocument)
				require.ErrorIs(t, err, paths.ErrNotFound)
			})
		}
	})

	t.Run("a document without a header reaches nothing", func(t *testing.T) {
		t.Parallel()

		_, err := paths.Root().Node(&ast.DocumentNode{})
		require.ErrorIs(t, err, paths.ErrNoDocument)
	})
}
