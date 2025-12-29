package niceyaml_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/macropower/niceyaml"
)

func TestRevision_At(t *testing.T) {
	t.Parallel()

	// Build a chain of 3 revisions: 0 -> 1 -> 2.
	rev0 := niceyaml.NewRevision(niceyaml.NewSourceFromString("v0: data", niceyaml.WithName("v0")))
	rev1 := rev0.Append(niceyaml.NewSourceFromString("v1: data", niceyaml.WithName("v1")))
	rev2 := rev1.Append(niceyaml.NewSourceFromString("v2: data", niceyaml.WithName("v2")))

	tcs := map[string]struct {
		startFrom *niceyaml.Revision
		want      string
		input     int
	}{
		"index 0 from origin": {
			startFrom: rev0,
			input:     0,
			want:      "v0",
		},
		"index 1 from origin": {
			startFrom: rev0,
			input:     1,
			want:      "v1",
		},
		"index 2 from origin": {
			startFrom: rev0,
			input:     2,
			want:      "v2",
		},
		"index 0 from middle": {
			startFrom: rev1,
			input:     0,
			want:      "v0",
		},
		"index 2 from middle": {
			startFrom: rev1,
			input:     2,
			want:      "v2",
		},
		"index 0 from tip": {
			startFrom: rev2,
			input:     0,
			want:      "v0",
		},
		"index 2 from tip": {
			startFrom: rev2,
			input:     2,
			want:      "v2",
		},
		"negative index stops at origin": {
			startFrom: rev1,
			input:     -5,
			want:      "v0",
		},
		"index beyond max stops at tip": {
			startFrom: rev1,
			input:     100,
			want:      "v2",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.startFrom.At(tc.input)
			assert.Equal(t, tc.want, got.Name())
		})
	}
}

func TestRevision_At_SingleRevision(t *testing.T) {
	t.Parallel()

	rev := niceyaml.NewRevision(niceyaml.NewSourceFromString("only: data", niceyaml.WithName("only")))

	tcs := map[string]struct {
		want  string
		input int
	}{
		"index 0": {
			input: 0,
			want:  "only",
		},
		"negative stops at origin": {
			input: -1,
			want:  "only",
		},
		"beyond max stops at tip": {
			input: 5,
			want:  "only",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := rev.At(tc.input)
			assert.Equal(t, tc.want, got.Name())
		})
	}
}
