package cafe_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/examples/schemas/cafe"
)

func cafeConfig(in string) (*cafe.Config, error) {
	src := niceyaml.NewSourceFromString(in)

	d, err := src.Decoder()
	if err != nil {
		return nil, err
	}

	c := cafe.NewConfig()

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

	cfg, err := cafeConfig(cafe.DefaultYAML)
	require.NoError(t, err, "load default config")
	require.NotNil(t, cfg)

	// Validate basic config structure.
	require.Equal(t, "Config", cfg.Kind)
	require.Equal(t, "Downtown Cafe", cfg.Metadata.Name)
	require.NotEmpty(t, cfg.Spec.Menu.Items)
	require.Equal(t, "07:00", cfg.Spec.Hours.Open)
	require.Equal(t, "19:00", cfg.Spec.Hours.Close)
}

func TestCafeBrokenConfig(t *testing.T) {
	t.Parallel()

	_, err := cafeConfig(cafe.BrokenYAML)
	require.Error(t, err, "broken config should fail schema validation")
}
