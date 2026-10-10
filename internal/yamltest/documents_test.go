package yamltest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

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

		doc := yamltest.FirstDocument(tb, stringtest.LinesLF("a: 1", "---", "b: 2"))

		assert.Equal(t, 0, doc.DocumentIndex())
	})

	t.Run("FirstDocument with options", func(t *testing.T) {
		t.Parallel()

		var tb testing.TB = t

		refs := niceyaml.NewSourceFromString("base: &x 1\n")
		doc := yamltest.FirstDocument(tb, "a: *x\n", niceyaml.WithName("app.yaml"), niceyaml.WithReferences(refs))

		got, err := doc.Decode[map[string]int](t.Context())
		require.NoError(t, err)

		assert.Equal(t, map[string]int{"a": 1}, got)
		assert.Equal(t, "app.yaml", doc.Source().Name())
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
		got, err := yamltest.At(tb, doc, paths.Current().Child("a")).Decode[int](t.Context())
		require.NoError(t, err)

		assert.Equal(t, 1, got)
	})

	t.Run("AtOrSelf", func(t *testing.T) {
		t.Parallel()

		var tb testing.TB = t

		doc := yamltest.FirstDocument(tb, "a: 1\n")
		assert.Same(t, doc, yamltest.AtOrSelf(tb, doc, paths.Path{}))
		assert.Same(t, doc, yamltest.AtOrSelf(tb, doc, paths.Current()))

		got, err := yamltest.AtOrSelf(tb, doc, paths.Current().Child("a")).Decode[int](t.Context())
		require.NoError(t, err)

		assert.Equal(t, 1, got)
	})

	t.Run("Document", func(t *testing.T) {
		t.Parallel()

		var tb testing.TB = t

		source := niceyaml.NewSourceFromString("a: 1\n")
		doc := yamltest.Document(tb, source)

		assert.Same(t, source, doc.Source())
		assert.True(t, doc.Path().IsRoot())
	})

	t.Run("Bind", func(t *testing.T) {
		t.Parallel()

		var tb testing.TB = t

		source := niceyaml.NewSourceFromString("a: 1\n")
		err := yamltest.Bind(tb, source, niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("a"))))

		var srcErr *niceyaml.SourceError

		require.ErrorAs(t, err, &srcErr)
	})

	t.Run("Layers", func(t *testing.T) {
		t.Parallel()

		var tb testing.TB = t

		base := niceyaml.NewSourceFromString("a: 1\nb: 2\n")
		prod := niceyaml.NewSourceFromString("b: 3\n")

		got, err := yamltest.Layers(tb, base, prod).Decode[map[string]int](t.Context())
		require.NoError(t, err)

		assert.Equal(t, map[string]int{"a": 1, "b": 3}, got)
	})
}
