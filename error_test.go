package niceyaml_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/macropower/niceyaml"
)

func TestYAMLError(t *testing.T) {
	t.Parallel()

	err := niceyaml.NewError(
		errors.New("test error"),
		niceyaml.WithPath(niceyaml.NewPathBuilder().Root().Child("key").Build()),
		niceyaml.WithSourceLines(2),
		niceyaml.WithSource([]byte(`a: b
b: c
foo: "bar"
key: value
baz: 5
c: d
e: f`)),
	)

	require.Error(t, err)
}
