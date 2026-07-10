package loader_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/schema/loader"
)

// Compile-time interface satisfaction checks.
var _ loader.Loader = loader.Func(nil)

func TestFunc(t *testing.T) {
	t.Parallel()

	want := loader.Result{URL: "schema.json", Data: []byte(`{}`)}
	l := loader.Func(func(_ context.Context, _ *niceyaml.DocumentDecoder) (loader.Result, error) {
		return want, nil
	})

	result, err := l.Load(t.Context(), nil)
	require.NoError(t, err)
	assert.Equal(t, want, result)
}
