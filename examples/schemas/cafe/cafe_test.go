package cafe_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/examples/schemas/cafe"
)

func cafeConfig(ctx context.Context, in string) (*cafe.Config, error) {
	doc, err := niceyaml.NewSourceFromString(in).Document()
	if err != nil {
		return nil, err
	}

	c, err := doc.Decode[cafe.Config](ctx, niceyaml.WithValidator(cafe.Schema))
	if err != nil {
		return nil, err
	}

	return &c, nil
}

// violationPaths returns the path of each violation in err, which must be
// bound to its source. A lone violation is the bound error itself.
func violationPaths(t *testing.T, err error) []string {
	t.Helper()

	var bound *niceyaml.SourceError

	require.ErrorAs(t, err, &bound)

	violations := bound.Errors()
	if len(violations) == 0 {
		violations = []*niceyaml.SourceError{bound}
	}

	got := []string{}
	for _, violation := range violations {
		path, ok := violation.Path()
		require.True(t, ok)

		got = append(got, path.String())
	}

	return got
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
	require.EqualError(t, err, "3 schema violations", "broken config should fail schema validation")

	// Both values also fail the plain decode, so the paths confirm that
	// the schema rejected each one. The SLA schema admits a string or
	// null, and the bad string fails both, so the SLA reports twice.
	assert.ElementsMatch(t,
		[]string{"$.spec.sla", "$.spec.sla", "$.spec.hours.days"},
		violationPaths(t, err),
	)
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
		"null":         {sla: "null"},
		"tilde":        {sla: "~"},
		"microseconds": {sla: "500us"},
		// MarshalText writes the micro sign, and time.ParseDuration also
		// reads the Greek mu.
		"micro sign":   {sla: "500\u00b5s"},
		"greek mu":     {sla: "500\u03bcs"},
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
			got := violationPaths(t, err)
			require.NotEmpty(t, got)

			for _, path := range got {
				assert.Equal(t, "$.spec.sla", path)
			}
		})
	}
}
