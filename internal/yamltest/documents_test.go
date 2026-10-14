package yamltest_test

import (
	"strings"
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
		err := yamltest.Bind(tb, source, yamltest.ErrorAt("bad", "a"))

		assert.Same(t, source, yamltest.RequireSourceError(t, err).Source())
	})

	t.Run("Layers", func(t *testing.T) {
		t.Parallel()

		var tb testing.TB = t

		base := niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "b: 2"))
		prod := niceyaml.NewSourceFromString("b: 3\n")

		got, err := yamltest.Layers(tb, base, prod).Decode[map[string]int](t.Context())
		require.NoError(t, err)

		assert.Equal(t, map[string]int{"a": 1, "b": 3}, got)
	})

	t.Run("Documents", func(t *testing.T) {
		t.Parallel()

		var tb testing.TB = t

		// The trailing header opens an empty document, which Documents
		// leaves out.
		source := niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "---", "b: [", "---"))
		docs := yamltest.Documents(tb, source, 2)

		require.NoError(t, docs[0].Err())
		require.ErrorIs(t, docs[1].Err(), niceyaml.ErrSyntax)

		rec := record(func(tb *recordingTB) {
			yamltest.Documents(tb, source, 3)
		})
		assert.True(t, rec.failed, "another count fails the test")
	})

	t.Run("AllDocuments", func(t *testing.T) {
		t.Parallel()

		var tb testing.TB = t

		source := niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "---", "b: [", "---"))
		docs := yamltest.AllDocuments(tb, source, 3)

		assert.True(t, docs[2].IsEmpty())

		rec := record(func(tb *recordingTB) {
			yamltest.AllDocuments(tb, source, 2)
		})
		assert.True(t, rec.failed, "another count fails the test")
	})

	t.Run("References", func(t *testing.T) {
		t.Parallel()

		var tb testing.TB = t

		// Both references define x, so the decode shows that References
		// keeps the order of its inputs.
		first, second := "x: &x 1\n", "x: &x 2\n"
		input := "a: *x\n"

		bySource := yamltest.FirstDocument(tb, input, niceyaml.WithReferences(
			niceyaml.NewSourceFromString(first),
			niceyaml.NewSourceFromString(second),
		))
		byInput := yamltest.FirstDocument(tb, input, yamltest.References(first, second))

		want, err := bySource.Decode[map[string]int](t.Context())
		require.NoError(t, err)

		got, err := byInput.Decode[map[string]int](t.Context())
		require.NoError(t, err)

		assert.Equal(t, want, got)
	})

	t.Run("NoSizeLimits", func(t *testing.T) {
		t.Parallel()

		var tb testing.TB = t

		inputs := map[string]string{
			"more bytes than the default limit":  "a: " + strings.Repeat("x", 1<<18) + "\n",
			"more levels than the default limit": "a: " + strings.Repeat("[", 200) + strings.Repeat("]", 200) + "\n",
		}

		for name, input := range inputs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				var tb testing.TB = t

				require.ErrorIs(t, yamltest.UnparsedDocument(tb, input).Err(), niceyaml.ErrLimit)

				doc := yamltest.FirstDocument(tb, input, yamltest.NoSizeLimits(niceyaml.WithName("big.yaml"))...)
				assert.Equal(t, "big.yaml", doc.Source().Name())
			})
		}

		// An option in opts comes after the two that NoSizeLimits adds.
		doc := yamltest.UnparsedDocument(tb, "a: 1\n", yamltest.NoSizeLimits(niceyaml.WithByteLimit(1))...)
		require.ErrorIs(t, doc.Err(), niceyaml.ErrLimit)
	})

	t.Run("UnparsedDocument", func(t *testing.T) {
		t.Parallel()

		var tb testing.TB = t

		doc := yamltest.UnparsedDocument(tb, "a: [\n", niceyaml.WithName("bad.yaml"))

		require.ErrorIs(t, doc.Err(), niceyaml.ErrSyntax)
		assert.Equal(t, "bad.yaml", doc.Source().Name())
	})
}
