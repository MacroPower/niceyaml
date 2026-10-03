package yamltest

import (
	"testing"

	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/paths"
)

// FirstDocument creates the root [*niceyaml.Node] of the first document
// of a YAML input for testing, from a [*niceyaml.Source] created with
// opts, such as [niceyaml.WithReferences].
//
// If the input contains no documents, the test fails.
func FirstDocument(tb testing.TB, input string, opts ...niceyaml.SourceOption) *niceyaml.Node {
	tb.Helper()

	source := niceyaml.NewSourceFromString(input, opts...)
	docs, err := source.Documents()
	require.NoError(tb, err)
	require.NotEmpty(tb, docs, "no documents found in input")

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

// Bind binds err to the single document of source through
// [niceyaml.Node.Bind]. The test fails when the source does not hold
// exactly one document.
func Bind(tb testing.TB, source *niceyaml.Source, err error) error {
	tb.Helper()

	doc, docErr := source.Document()
	require.NoError(tb, docErr)

	return doc.Bind(err)
}

// FirstDocumentWithPath creates the root [*niceyaml.Node] of the first
// document of a YAML input, with file path context, for testing.
//
// If the input contains no documents, the test fails.
func FirstDocumentWithPath(tb testing.TB, input, filePath string) *niceyaml.Node {
	tb.Helper()

	return FirstDocument(tb, input, niceyaml.WithFilePath(filePath))
}
