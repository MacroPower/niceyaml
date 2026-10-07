package yamltest_test

import (
	"errors"
	"fmt"
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

	namePath := paths.Current().Child("name")
	reserved := niceyaml.NewError("reserved name", niceyaml.AtPath(namePath))

	var nilError *niceyaml.Error

	tcs := map[string]struct {
		// Build returns the error the helper checks, given the Node the
		// helper binds it through.
		build func(item *niceyaml.Node) error
		// Whether the helper passes.
		want bool
	}{
		"no error": {
			build: func(*niceyaml.Node) error { return nil },
			want:  true,
		},
		"nil Error pointer": {
			build: func(*niceyaml.Node) error { return nilError },
			want:  true,
		},
		"bound through the node": {
			build: func(item *niceyaml.Node) error { return item.Bind(reserved) },
			want:  true,
		},
		"bound through the document": {
			build: func(item *niceyaml.Node) error { return item.Document().Bind(reserved) },
			want:  true,
		},
		"context around a bound error": {
			build: func(item *niceyaml.Node) error { return fmt.Errorf("rule: %w", item.Bind(reserved)) },
			want:  true,
		},
		"join of bound errors": {
			build: func(item *niceyaml.Node) error {
				return errors.Join(item.Bind(reserved), item.Bind(errors.New("bad item")))
			},
			want: true,
		},
		"@ path": {
			build: func(*niceyaml.Node) error { return reserved },
		},
		"$ path": {
			build: func(*niceyaml.Node) error {
				return niceyaml.NewError("reserved name", niceyaml.AtPath(paths.Doc().Child("name")))
			},
		},
		"no location": {
			build: func(*niceyaml.Node) error { return errors.New("bad item") },
		},
		"Rebase result": {
			build: func(item *niceyaml.Node) error { return niceyaml.Rebase(reserved, item.Path()) },
		},
		"join of a bound error and an unbound one": {
			build: func(item *niceyaml.Node) error { return errors.Join(item.Bind(reserved), reserved) },
		},
		"location above a bound error": {
			build: func(item *niceyaml.Node) error {
				return niceyaml.WrapError(item.Bind(reserved), niceyaml.AtPath(namePath))
			},
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

				yamltest.RequireBound(rec, item, tc.build(item))
			}()

			<-done

			if tc.want {
				assert.False(t, rec.failed, rec.msg)

				return
			}

			require.True(t, rec.failed)
			assert.Contains(t, rec.msg, "validator returned an unbound error")
			assert.Contains(t, rec.msg, "unbound.yaml")
		})
	}
}
