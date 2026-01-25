package cafe_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"jacobcolvin.com/niceyaml"
	"jacobcolvin.com/niceyaml/examples/schemas/cafe"
)

func cafeConfig(in string) (*cafe.Config, error) {
	src := niceyaml.NewSourceFromString(in)

	f, err := src.File()
	if err != nil {
		return nil, src.WrapError(err)
	}

	c := cafe.NewConfig()

	d := niceyaml.NewDecoder(f)
	for _, doc := range d.Documents() {
		err := doc.Unmarshal(&c)
		if err != nil {
			return nil, src.WrapError(err)
		}
	}

	return &c, nil
}

func TestCafeDefaultConfig(t *testing.T) {
	t.Parallel()

	defaults, err := os.ReadFile("defaults.yaml")
	require.NoError(t, err, "read default config")

	_, err = cafeConfig(string(defaults))
	require.NoError(t, err, "load default config")
}
