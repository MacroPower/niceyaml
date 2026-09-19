package cafe_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/examples/schemas/cafe"
)

func cafeConfig(ctx context.Context, in string) (*cafe.Config, error) {
	src := niceyaml.NewSourceFromString(in)

	c, err := src.Decode[cafe.Config](ctx, niceyaml.WithValidator(cafe.Schema))
	if err != nil {
		return nil, err
	}

	return &c, nil
}

func TestCafeDefaultConfig(t *testing.T) {
	t.Parallel()

	cfg, err := cafeConfig(t.Context(), cafe.DefaultYAML)
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

	_, err := cafeConfig(t.Context(), cafe.BrokenYAML)
	require.Error(t, err, "broken config should fail schema validation")
}

func TestCafeSLA(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		sla string
		err bool
	}{
		"minutes":      {sla: "15m"},
		"hours":        {sla: "1h"},
		"seconds":      {sla: "90s"},
		"compound":     {sla: "1h30m"},
		"fractional":   {sla: "1.5h"},
		"days":         {sla: "1d", err: true},
		"empty":        {sla: `""`, err: true},
		"uppercase":    {sla: "15M", err: true},
		"no unit":      {sla: "15", err: true},
		"unknown unit": {sla: "15w", err: true},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			in := strings.Replace(cafe.DefaultYAML, "sla: 15m", "sla: "+tc.sla, 1)
			require.Contains(t, in, "sla: "+tc.sla)

			_, err := cafeConfig(t.Context(), in)
			if !tc.err {
				require.NoError(t, err)

				return
			}

			// A schema violation carries the location of the value, which a
			// decode failure inside UnmarshalText would not.
			require.Error(t, err)
			require.Contains(t, err.Error(), "$.spec.sla")
		})
	}
}
