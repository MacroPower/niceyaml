package taggedscalar_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/taggedscalar"
)

func TestBinary_MarshalYAML(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input taggedscalar.Binary
		want  string
	}{
		"bytes": {
			input: taggedscalar.Binary("hi"),
			want:  `!!binary "aGk="`,
		},
		"no bytes": {
			input: taggedscalar.Binary{},
			want:  `!!binary ""`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := tc.input.MarshalYAML()
			require.NoError(t, err)
			assert.Equal(t, tc.want, string(got))
		})
	}
}

func TestTimestamp_MarshalYAML(t *testing.T) {
	t.Parallel()

	at := time.Date(2020, 1, 2, 3, 4, 5, 6, time.UTC)

	got, err := taggedscalar.Timestamp(at).MarshalYAML()
	require.NoError(t, err)
	assert.Equal(t, `!!timestamp "2020-01-02T03:04:05.000000006Z"`, string(got))
}
