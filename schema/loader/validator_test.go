package loader_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/schema/loader"
	"go.jacobcolvin.com/niceyaml/schema/validator"
)

func TestValidator(t *testing.T) {
	t.Parallel()

	schemaData := []byte(`{"type": "object"}`)
	v := validator.MustNew("test.json", schemaData)

	l := loader.Validator("test.json", v)
	result, err := l.Load(t.Context(), nil)
	require.NoError(t, err)
	assert.Nil(t, result.Data) // Pre-compiled validators return nil data.
	assert.Equal(t, "test.json", result.URL)
	assert.Equal(t, v, result.Validator)
}
