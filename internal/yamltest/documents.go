package yamltest

import (
	"testing"

	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/paths"
)

// FirstDocument creates the root [*niceyaml.Node] of the first document
// of a YAML input for testing, from a [*niceyaml.Source] created with
// opts, such as [niceyaml.WithReferences]. It reads the documents
// [niceyaml.Source.Documents] returns, which leaves out an empty document
// beside one with content.
//
// If the input contains no documents, the test fails.
func FirstDocument(tb testing.TB, input string, opts ...niceyaml.SourceOption) *niceyaml.Node {
	tb.Helper()

	docs := niceyaml.NewSourceFromString(input, opts...).Documents()
	require.NotEmpty(tb, docs, "no documents found in input")
	require.NoError(tb, docs[0].Err())

	return docs[0]
}

// At scopes n to the node that path selects, through [niceyaml.Node.At].
// The test fails when the path selects nothing.
func At(tb testing.TB, n *niceyaml.Node, path paths.Path) *niceyaml.Node {
	tb.Helper()

	scoped, err := n.At(path)
	require.NoError(tb, err)

	return scoped
}

// AtOrSelf returns n for a path with no selector, and the
// [*niceyaml.Node] that [At] scopes to path for any other. A table case
// then names a path only when it checks a scoped Node.
func AtOrSelf(tb testing.TB, n *niceyaml.Node, path paths.Path) *niceyaml.Node {
	tb.Helper()

	if path.Len() == 0 {
		return n
	}

	return At(tb, n, path)
}

// Document returns the root [*niceyaml.Node] of the single document of
// source, through [niceyaml.Source.Document]. The test fails when the
// source does not hold exactly one document, or when that document did
// not parse.
func Document(tb testing.TB, source *niceyaml.Source) *niceyaml.Node {
	tb.Helper()

	doc, err := source.Document()
	require.NoError(tb, err)

	return doc
}

// Bind binds err to the single document of source through
// [niceyaml.Node.Bind]. The test fails when the source does not hold
// exactly one document.
func Bind(tb testing.TB, source *niceyaml.Source, err error) error {
	tb.Helper()

	return Document(tb, source).Bind(err)
}

// Layers merges layers through [niceyaml.NewSourceFromLayers] with the
// context of the test, and returns the merged [*niceyaml.Source]. The
// test fails when a layer holds no value the merge can read.
func Layers(tb testing.TB, layers ...niceyaml.Layer) *niceyaml.Source {
	tb.Helper()

	merged, err := niceyaml.NewSourceFromLayers(tb.Context(), layers...)
	require.NoError(tb, err)

	return merged
}

// FirstDocumentWithPath creates the root [*niceyaml.Node] of the first
// document of a YAML input, with file path context, for testing.
//
// If the input contains no documents, the test fails.
func FirstDocumentWithPath(tb testing.TB, input, filePath string) *niceyaml.Node {
	tb.Helper()

	return FirstDocument(tb, input, niceyaml.WithFilePath(filePath))
}
