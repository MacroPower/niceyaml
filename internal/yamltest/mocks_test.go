package yamltest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/finder"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
)

func TestNormalizerFunc(t *testing.T) {
	t.Parallel()

	var _ finder.Normalizer = yamltest.NormalizerFunc(nil)

	var receivedInput string

	n := yamltest.NormalizerFunc(func(in string) string {
		receivedInput = in
		return "custom-" + in
	})

	result := n.Normalize("test")

	assert.Equal(t, "test", receivedInput)
	assert.Equal(t, "custom-test", result)
}
