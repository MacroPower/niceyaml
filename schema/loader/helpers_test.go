package loader_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/schema"
	"go.jacobcolvin.com/niceyaml/schema/loader"
)

// load resolves r with no document and loads the schema it names, returning
// the ref's URL alongside the loaded bytes. Resolve itself must succeed;
// load returns only the Load error.
func load(t *testing.T, r schema.Resolver) (string, []byte, error) {
	t.Helper()

	ref, err := r.Resolve(t.Context(), nil)
	require.NoError(t, err)
	require.NotNil(t, ref.Load)

	data, err := ref.Load(t.Context())

	return ref.URL, data, err //nolint:wrapcheck // Tests inspect the loader's own error.
}

// fileURL returns the URL that [loader.File] names for path.
func fileURL(t *testing.T, path string) string {
	t.Helper()

	ref, err := loader.File(path).Resolve(t.Context(), nil)
	require.NoError(t, err)

	return ref.URL
}
