package yamltest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
)

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
