package yamltest

import (
	"testing"

	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/niceyamltest"
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

// Bind binds err to the single document of source through
// [niceyaml.Node.Bind]. The test fails when the source does not hold
// exactly one document.
func Bind(tb testing.TB, source *niceyaml.Source, err error) error {
	tb.Helper()

	doc, docErr := source.Document()
	require.NoError(tb, docErr)

	return doc.Bind(err)
}

// RequireBound fails the test unless err is bound, as
// [niceyaml.Validator] asks of every error a validator returns. A test
// calls the validator itself on a Node from [At] and passes the result:
//
//	item := yamltest.At(t, doc, paths.Doc().Child("items").Index(1))
//	yamltest.RequireBound(t, rule.Validate(t.Context(), item))
//
// [niceyamltest.CheckBound] decides whether err is bound, so a test
// outside the module holds its validators to the same check.
func RequireBound(tb testing.TB, err error) {
	tb.Helper()

	require.NoError(tb, niceyamltest.CheckBound(err), "validator returned an unbound error")
}

// FirstDocumentWithPath creates the root [*niceyaml.Node] of the first
// document of a YAML input, with file path context, for testing.
//
// If the input contains no documents, the test fails.
func FirstDocumentWithPath(tb testing.TB, input, filePath string) *niceyaml.Node {
	tb.Helper()

	return FirstDocument(tb, input, niceyaml.WithFilePath(filePath))
}
