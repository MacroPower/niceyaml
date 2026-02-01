package loader_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/schema/loader"
	"go.jacobcolvin.com/niceyaml/schema/validator"
)

// Compile-time interface satisfaction checks.
var _ loader.Loader = loader.Func(nil)

func TestNewResult(t *testing.T) {
	t.Parallel()

	t.Run("valid data", func(t *testing.T) {
		t.Parallel()

		result := loader.NewResult("schema.json", []byte(`{}`))

		assert.Equal(t, "schema.json", result.URL)
		assert.Equal(t, []byte(`{}`), result.Data)
		assert.Nil(t, result.Validator)
	})

	t.Run("panics on nil data", func(t *testing.T) {
		t.Parallel()

		assert.PanicsWithValue(t,
			"loader.NewResult: data is required when validator is nil",
			func() { loader.NewResult("schema.json", nil) },
		)
	})

	t.Run("panics on empty data", func(t *testing.T) {
		t.Parallel()

		assert.PanicsWithValue(t,
			"loader.NewResult: data is required when validator is nil",
			func() { loader.NewResult("schema.json", []byte{}) },
		)
	})
}

func TestNewResultWithValidator(t *testing.T) {
	t.Parallel()

	t.Run("valid validator", func(t *testing.T) {
		t.Parallel()

		v := validator.MustNew("test", []byte(`{}`))
		result := loader.NewResultWithValidator("schema.json", v)

		assert.Equal(t, "schema.json", result.URL)
		assert.Equal(t, v, result.Validator)
		assert.Nil(t, result.Data)
	})

	t.Run("panics on nil validator", func(t *testing.T) {
		t.Parallel()

		assert.PanicsWithValue(t,
			"loader.NewResultWithValidator: validator is required",
			func() { loader.NewResultWithValidator("schema.json", nil) },
		)
	})
}
