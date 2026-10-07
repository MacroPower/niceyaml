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

// RequireBound fails the test unless err is bound, as
// [niceyaml.Validator] asks of every error a validator returns. A test
// calls the validator itself on a Node from [At] and passes the result:
//
//	item := yamltest.At(t, doc, paths.Doc().Child("items").Index(1))
//	yamltest.RequireBound(t, item, rule.Validate(t.Context(), item))
//
// A bound error keeps the place and the source its binding resolved, so
// it reads the same through any Node. RequireBound binds err through n
// and through a document of another source, and the test fails unless
// the two messages are equal. An error that is unbound, or that joins an
// unbound error with bound ones, takes the name of the other source,
// "unbound.yaml". An err that is nil, or that holds a nil
// [*niceyaml.Error] or [*niceyaml.SourceError] pointer, is no error and
// passes.
func RequireBound(tb testing.TB, n *niceyaml.Node, err error) {
	tb.Helper()

	other := FirstDocument(tb, "unbound: true\n", niceyaml.WithName("unbound.yaml"))

	bound := n.Bind(err)
	if bound == nil {
		return
	}

	require.EqualError(tb, other.Bind(err), bound.Error(), "validator returned an unbound error")
}

// FirstDocumentWithPath creates the root [*niceyaml.Node] of the first
// document of a YAML input, with file path context, for testing.
//
// If the input contains no documents, the test fails.
func FirstDocumentWithPath(tb testing.TB, input, filePath string) *niceyaml.Node {
	tb.Helper()

	return FirstDocument(tb, input, niceyaml.WithFilePath(filePath))
}
