package yamltest

import (
	"testing"

	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/paths"
)

// FirstDocument creates the root [*niceyaml.Node] of the first document
// of a YAML input for testing.
//
// If the input contains no documents, the test fails.
func FirstDocument(t *testing.T, input string) *niceyaml.Node {
	t.Helper()

	return FirstDocumentWithPath(t, input, "")
}

// At scopes n to the node that path selects, through [niceyaml.Node.At].
// The test fails when the path selects nothing.
func At(t *testing.T, n *niceyaml.Node, path paths.Path) *niceyaml.Node {
	t.Helper()

	scoped, err := n.At(path)
	require.NoError(t, err)

	return scoped
}

// Bind binds err to the single document of source through
// [niceyaml.Node.Bind]. The test fails when the source does not hold
// exactly one document.
func Bind(t *testing.T, source *niceyaml.Source, err error) error {
	t.Helper()

	doc, docErr := source.Document()
	require.NoError(t, docErr)

	return doc.Bind(err)
}

// FirstDocumentWithPath creates the root [*niceyaml.Node] of the first
// document of a YAML input, with file path context, for testing.
//
// If the input contains no documents, the test fails.
func FirstDocumentWithPath(t *testing.T, input, filePath string) *niceyaml.Node {
	t.Helper()

	source := niceyaml.NewSourceFromString(input, niceyaml.WithFilePath(filePath))
	docs, err := source.Documents()
	require.NoError(t, err)
	require.NotEmpty(t, docs, "no documents found in input")

	return docs[0]
}
