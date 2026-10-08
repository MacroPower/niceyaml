package yamltest_test

import (
	"errors"
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

	t.Run("Bind", func(t *testing.T) {
		t.Parallel()

		var tb testing.TB = t

		source := niceyaml.NewSourceFromString("a: 1\n")
		err := yamltest.Bind(tb, source, niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("a"))))

		var srcErr *niceyaml.SourceError

		require.ErrorAs(t, err, &srcErr)
	})
}

func TestRequireBound(t *testing.T) {
	t.Parallel()

	reserved := niceyaml.NewError("reserved name", niceyaml.AtPath(paths.Current().Child("name")))

	tcs := map[string]struct {
		// Build returns the error the helper checks, given a Node to bind
		// it through.
		build func(item *niceyaml.Node) error
		msgs  []string // Empty when the helper passes.
	}{
		"no error": {
			build: func(*niceyaml.Node) error { return nil },
		},
		"bound through the node": {
			build: func(item *niceyaml.Node) error { return item.Bind(reserved) },
		},
		"join of bound errors": {
			build: func(item *niceyaml.Node) error {
				return errors.Join(item.Bind(reserved), item.Bind(errors.New("bad item")))
			},
		},
		"unbound": {
			build: func(*niceyaml.Node) error { return reserved },
			msgs:  []string{"validator returned an unbound error", `bound to no source: "reserved name"`},
		},
		"join of a bound error and an unbound one": {
			build: func(item *niceyaml.Node) error { return errors.Join(item.Bind(reserved), errors.New("bad item")) },
			msgs:  []string{"validator returned an unbound error", `bound to no source: "bad item"`},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(
				t,
				"name: lunch\nitems:\n  - name: soup\n  - name: admin\n",
				niceyaml.WithName("c.yaml"),
			)
			item := yamltest.At(t, doc, paths.Doc().Child("items").Index(1))

			rec := &recordingTB{}
			done := make(chan struct{})

			// FailNow ends the goroutine, as it does for a real test.
			go func() {
				defer close(done)

				yamltest.RequireBound(rec, tc.build(item))
			}()

			<-done

			if len(tc.msgs) == 0 {
				assert.False(t, rec.failed, rec.msg)

				return
			}

			require.True(t, rec.failed)

			for _, msg := range tc.msgs {
				assert.Contains(t, rec.msg, msg)
			}
		})
	}
}
