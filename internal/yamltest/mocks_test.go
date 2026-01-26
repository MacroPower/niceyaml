package yamltest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"jacobcolvin.com/niceyaml/internal/yamltest"
)

func TestMockSchemaValidator(t *testing.T) {
	t.Parallel()

	t.Run("NewPassingSchemaValidator returns nil for any input", func(t *testing.T) {
		t.Parallel()

		v := yamltest.NewPassingSchemaValidator()

		assert.NoError(t, v.ValidateSchema("string"))
		assert.NoError(t, v.ValidateSchema(42))
		assert.NoError(t, v.ValidateSchema(nil))
		assert.NoError(t, v.ValidateSchema(struct{ Name string }{"test"}))
	})

	t.Run("NewFailingSchemaValidator returns the specified error", func(t *testing.T) {
		t.Parallel()

		wantErr := assert.AnError
		v := yamltest.NewFailingSchemaValidator(wantErr)

		err := v.ValidateSchema("any input")
		require.ErrorIs(t, err, wantErr)
	})

	t.Run("NewCustomSchemaValidator calls custom function with data", func(t *testing.T) {
		t.Parallel()

		var receivedData any

		customFn := func(data any) error {
			receivedData = data
			return nil
		}

		v := yamltest.NewCustomSchemaValidator(customFn)
		err := v.ValidateSchema("test data")

		require.NoError(t, err)
		assert.Equal(t, "test data", receivedData)
	})

	t.Run("NewCustomSchemaValidator passes through error", func(t *testing.T) {
		t.Parallel()

		wantErr := assert.AnError
		customFn := func(_ any) error {
			return wantErr
		}

		v := yamltest.NewCustomSchemaValidator(customFn)
		err := v.ValidateSchema("any")

		require.ErrorIs(t, err, wantErr)
	})
}

func TestMockNormalizer(t *testing.T) {
	t.Parallel()

	t.Run("NewIdentityNormalizer returns input unchanged", func(t *testing.T) {
		t.Parallel()

		n := yamltest.NewIdentityNormalizer()

		assert.Equal(t, "hello", n.Normalize("hello"))
		assert.Equal(t, "UPPER", n.Normalize("UPPER"))
		assert.Empty(t, n.Normalize(""))
		assert.Equal(t, "Ö", n.Normalize("Ö"))
	})

	t.Run("NewStaticNormalizer returns the specified output", func(t *testing.T) {
		t.Parallel()

		n := yamltest.NewStaticNormalizer("normalized")

		assert.Equal(t, "normalized", n.Normalize("any input"))
		assert.Equal(t, "normalized", n.Normalize("different input"))
		assert.Equal(t, "normalized", n.Normalize(""))
	})

	t.Run("NewCustomNormalizer calls custom function with input", func(t *testing.T) {
		t.Parallel()

		var receivedInput string

		customFn := func(in string) string {
			receivedInput = in
			return "custom-" + in
		}

		n := yamltest.NewCustomNormalizer(customFn)
		result := n.Normalize("test")

		assert.Equal(t, "test", receivedInput)
		assert.Equal(t, "custom-test", result)
	})
}
