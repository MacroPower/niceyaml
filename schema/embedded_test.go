package schema_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/schema"
)

func TestEmbedded(t *testing.T) {
	t.Parallel()

	schemaData := []byte(`{"type": "object"}`)

	t.Run("serves the bytes", func(t *testing.T) {
		t.Parallel()

		key, data, err := load(t, schema.Embedded(schemaData))
		require.NoError(t, err)
		assert.Equal(t, schemaData, data)
		assert.NotEmpty(t, key)
	})

	t.Run("keys by content", func(t *testing.T) {
		t.Parallel()

		same, _, err := load(t, schema.Embedded([]byte(`{"type": "object"}`)))
		require.NoError(t, err)

		other, _, err := load(t, schema.Embedded([]byte(`{"type": "string"}`)))
		require.NoError(t, err)

		key, _, err := load(t, schema.Embedded(schemaData))
		require.NoError(t, err)

		assert.Equal(t, key, same, "equal bytes name one schema")
		assert.NotEqual(t, key, other, "different bytes name different schemas")
	})
}
