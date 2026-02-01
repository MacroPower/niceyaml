package yamltest

import (
	"testing"

	"github.com/stretchr/testify/require"

	"jacobcolvin.com/niceyaml"
)

// FirstDocument creates a [*niceyaml.DocumentDecoder] from YAML input for
// testing. It returns the first document in the input.
//
// If the input contains no documents, the test fails.
func FirstDocument(t *testing.T, input string) *niceyaml.DocumentDecoder {
	t.Helper()

	return FirstDocumentWithPath(t, input, "")
}

// FirstDocumentWithPath creates a [*niceyaml.DocumentDecoder] with file path
// context for testing. It returns the first document in the input.
//
// If the input contains no documents, the test fails.
func FirstDocumentWithPath(t *testing.T, input, filePath string) *niceyaml.DocumentDecoder {
	t.Helper()

	var opts []niceyaml.SourceOption
	if filePath != "" {
		opts = append(opts, niceyaml.WithFilePath(filePath))
	}

	source := niceyaml.NewSourceFromString(input, opts...)
	decoder, err := source.Decoder()
	require.NoError(t, err)

	for _, doc := range decoder.Documents() {
		return doc
	}

	t.Fatal("no documents found in input")

	return nil // Unreachable, but required for compilation.
}
