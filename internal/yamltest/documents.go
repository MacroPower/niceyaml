package yamltest

import (
	"testing"

	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
)

// FirstDocument creates a [*niceyaml.Document] from YAML input for
// testing. It returns the first document in the input.
//
// If the input contains no documents, the test fails.
func FirstDocument(t *testing.T, input string) *niceyaml.Document {
	t.Helper()

	return FirstDocumentWithPath(t, input, "")
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

	for _, doc := range docs.All() {
		return doc
	}

	t.Fatal("no documents found in input")

	return nil // Unreachable, but required for compilation.
}
