package yamltest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macropower/niceyaml/yamltest"
)

func TestMockValidator(t *testing.T) {
	t.Parallel()

	t.Run("NewPassingValidator returns nil for any input", func(t *testing.T) {
		t.Parallel()

		v := yamltest.NewPassingValidator()

		assert.NoError(t, v.Validate("string"))
		assert.NoError(t, v.Validate(42))
		assert.NoError(t, v.Validate(nil))
		assert.NoError(t, v.Validate(struct{ Name string }{"test"}))
	})

	t.Run("NewFailingValidator returns the specified error", func(t *testing.T) {
		t.Parallel()

		expectedErr := assert.AnError
		v := yamltest.NewFailingValidator(expectedErr)

		err := v.Validate("any input")
		require.ErrorIs(t, err, expectedErr)
	})

	t.Run("NewCustomValidator calls custom function with input", func(t *testing.T) {
		t.Parallel()

		var receivedInput any

		customFn := func(input any) error {
			receivedInput = input
			return nil
		}

		v := yamltest.NewCustomValidator(customFn)
		err := v.Validate("test input")

		require.NoError(t, err)
		assert.Equal(t, "test input", receivedInput)
	})

	t.Run("NewCustomValidator passes through error", func(t *testing.T) {
		t.Parallel()

		expectedErr := assert.AnError
		customFn := func(_ any) error {
			return expectedErr
		}

		v := yamltest.NewCustomValidator(customFn)
		err := v.Validate("any")

		require.ErrorIs(t, err, expectedErr)
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
