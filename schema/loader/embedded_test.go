package loader_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/schema/loader"
)

func TestEmbedded(t *testing.T) {
	t.Parallel()

	schemaData := []byte(`{"type": "object"}`)
	l := loader.Embedded("test.json", schemaData)

	result, err := l.Load(t.Context(), nil)
	require.NoError(t, err)
	assert.Equal(t, schemaData, result.Data)
	assert.Equal(t, "test.json", result.URL)
}
