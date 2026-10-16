package cafe_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/examples/schemas/cafe"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/schema"
)

func cafeConfig(ctx context.Context, in string) (*cafe.Config, error) {
	var c cafe.Config

	err := niceyaml.NewSourceFromString(in).DecodeInto(ctx, &c, niceyaml.WithValidator(cafe.Schema))
	if err != nil {
		return nil, err
	}

	return &c, nil
}

// violationPaths returns the path of each error bound in the tree of err
// that carries one. The summary of several violations carries none, and
// neither does a form under a value that matches no branch of an anyOf.
func violationPaths(err error) []string {
	got := []string{}

	for _, bound := range yamltest.BoundIn(err) {
		if path, ok := bound.Path(); ok {
			got = append(got, path.String())
		}
	}

	return got
}

// edited returns in with its first from replaced by to. The test fails
// when in holds no from, as it would after the default config changes.
func edited(t *testing.T, in, from, to string) string {
	t.Helper()

	require.Contains(t, in, from)

	return strings.Replace(in, from, to, 1)
}

// TestCafeFields checks that gen names each property of the Config
// schema as the decoder names its key.
func TestCafeFields(t *testing.T) {
	t.Parallel()

	require.NoError(t, schema.CheckFields[cafe.Config]())
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
	require.EqualError(t, err, stringtest.JoinLF(
		"2 schema violations",
		`6:8: $.spec.sla: string does not match pattern "^([0-9]+(\\.[0-9]+)?(ns|[uµμ]s|ms|s|m|h))+$"`,
		`22:11: $.spec.hours.days: expected "array", got "string"`,
	), "broken config should fail schema validation")

	// Both bad values also fail the plain decode, so the paths confirm
	// that the schema rejected each one. The SLA schema admits a string
	// or null, and the bad string is no null, so the schema reports the
	// pattern the string breaks and nothing for the null.
	assert.ElementsMatch(t,
		[]string{"$.spec.sla", "$.spec.hours.days"},
		violationPaths(err),
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

			in := edited(t, cafe.DefaultYAML, "sla: 15m", "sla: "+tc.sla)

			_, err := cafeConfig(t.Context(), in)
			if !tc.err {
				require.NoError(t, err)

				return
			}

			// A decode failure inside UnmarshalText binds at the value too,
			// so the Violation confirms that the schema rejected it.
			var violation *schema.Violation

			require.ErrorAs(t, err, &violation)

			got := violationPaths(err)
			require.NotEmpty(t, got)

			for _, path := range got {
				assert.Equal(t, "$.spec.sla", path)
			}
		})
	}
}

func TestCafeHours(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		open  string
		close string
		err   bool
	}{
		"open before close": {open: "7:00", close: "19:00"},
		"open after close":  {open: "19:00", close: "07:00", err: true},
		"open equals close": {open: "07:00", close: "07:00", err: true},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			in := edited(t, cafe.DefaultYAML, `open: "07:00"`, `open: "`+tc.open+`"`)
			in = edited(t, in, `close: "19:00"`, `close: "`+tc.close+`"`)

			_, err := cafeConfig(t.Context(), in)
			if !tc.err {
				require.NoError(t, err)

				return
			}

			// The schema admits both times, so the failure comes from
			// Hours.Validate, and the decode reports it under the hours.
			require.ErrorContains(t, err, "open must be before close")
			assert.Equal(t, []string{"$.spec.hours.open"}, violationPaths(err))
		})
	}
}
