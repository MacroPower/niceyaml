package loader_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"jacobcolvin.com/niceyaml/schema/loader"
)

func TestEmbedded(t *testing.T) {
	t.Parallel()

	t.Run("loads embedded data", func(t *testing.T) {
		t.Parallel()

		schemaData := []byte(`{"type": "object"}`)
		l := loader.Embedded("test.json", schemaData)

		result, err := l.Load(t.Context(), nil)
		require.NoError(t, err)
		assert.Equal(t, schemaData, result.Data)
		assert.Equal(t, "test.json", result.URL)
	})

	t.Run("empty data panics", func(t *testing.T) {
		t.Parallel()

		l := loader.Embedded("empty.json", []byte{})
		assert.Panics(t, func() {
			//nolint:errcheck // Testing panic, not error.
			l.Load(t.Context(), nil)
		})
	})
}
