package spec_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/examples/schemas/cafe/spec"
)

func TestDurationMarshalText(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		duration time.Duration
		want     string
	}{
		"minutes":      {duration: 15 * time.Minute, want: "15m0s"},
		"compound":     {duration: 90 * time.Minute, want: "1h30m0s"},
		"microseconds": {duration: 500 * time.Microsecond, want: "500µs"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := spec.Duration(tc.duration).MarshalText()
			require.NoError(t, err)
			assert.Equal(t, tc.want, string(got))
		})
	}
}
