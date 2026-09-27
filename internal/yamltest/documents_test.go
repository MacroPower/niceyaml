package yamltest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

// TestDocumentHelpers passes each helper a [testing.TB], as a benchmark
// would.
func TestDocumentHelpers(t *testing.T) {
	t.Parallel()

	t.Run("FirstDocument", func(t *testing.T) {
		t.Parallel()

		var tb testing.TB = t

		doc := yamltest.FirstDocument(tb, "a: 1\n---\nb: 2\n")

		assert.Equal(t, 0, doc.DocumentIndex())
	})

	t.Run("FirstDocumentWithPath", func(t *testing.T) {
		t.Parallel()

		var tb testing.TB = t

		doc := yamltest.FirstDocumentWithPath(tb, "a: 1\n", "config.yaml")

		assert.Equal(t, "config.yaml", doc.FilePath())
	})

	t.Run("At", func(t *testing.T) {
		t.Parallel()

		var tb testing.TB = t

		doc := yamltest.FirstDocument(tb, "a: 1\n")
		got, err := yamltest.At(tb, doc, paths.Root().Child("a")).Decode[int](t.Context())
		require.NoError(t, err)

		assert.Equal(t, 1, got)
	})

	t.Run("Bind", func(t *testing.T) {
		t.Parallel()

		var tb testing.TB = t

		source := niceyaml.NewSourceFromString("a: 1\n")
		err := yamltest.Bind(tb, source, niceyaml.NewError("bad", niceyaml.AtPath(paths.Root().Child("a"))))

		var srcErr *niceyaml.SourceError

		require.ErrorAs(t, err, &srcErr)
	})
}
