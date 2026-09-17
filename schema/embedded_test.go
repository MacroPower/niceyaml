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
	r := schema.Embedded("test.json", schemaData)

	url, data, err := load(t, r)
	require.NoError(t, err)
	assert.Equal(t, schemaData, data)
	assert.Equal(t, "test.json", url)
}
