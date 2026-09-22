package yamltest

import (
	"testing"

	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/paths"
)

// FirstDocument creates a [*niceyaml.Document] from YAML input for
// testing. It returns the first document in the input.
//
// If the input contains no documents, the test fails.
func FirstDocument(t *testing.T, input string) *niceyaml.Document {
	t.Helper()

	return FirstDocumentWithPath(t, input, "")
}

// Scope is a [*niceyaml.Node] or a [*niceyaml.Document], whose root Node
// resolves paths, for [At] to scope.
type Scope interface {
	At(path paths.Path) (*niceyaml.Node, error)
}

// At scopes s to the node that path selects, through [niceyaml.Node.At].
// The test fails when the path selects nothing.
func At(t *testing.T, s Scope, path paths.Path) *niceyaml.Node {
	t.Helper()

	scoped, err := s.At(path)
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

// FirstDocumentWithPath creates a [*niceyaml.Document] with file path
// context for testing. It returns the first document in the input.
//
// If the input contains no documents, the test fails.
func FirstDocumentWithPath(t *testing.T, input, filePath string) *niceyaml.Document {
	t.Helper()

	var opts []niceyaml.SourceOption

	if filePath != "" {
		opts = append(opts, niceyaml.WithFilePath(filePath))
	}

	source := niceyaml.NewSourceFromString(input, opts...)
	docs, err := source.Documents()
	require.NoError(t, err)
	require.NotEmpty(t, docs, "no documents found in input")

	return docs[0]
}
