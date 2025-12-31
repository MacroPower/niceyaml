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

func TestRevision_Lines(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("key: value", niceyaml.WithName("test"))
	rev := niceyaml.NewRevision(source)

	got := rev.Lines()

	assert.Equal(t, source, got)
	assert.Equal(t, 1, got.Count())
}

func TestRevision_Tip(t *testing.T) {
	t.Parallel()

	// Build chain: v0 -> v1 -> v2.
	rev0 := niceyaml.NewRevision(niceyaml.NewSourceFromString("v0: data", niceyaml.WithName("v0")))
	rev1 := rev0.Append(niceyaml.NewSourceFromString("v1: data", niceyaml.WithName("v1")))
	rev2 := rev1.Append(niceyaml.NewSourceFromString("v2: data", niceyaml.WithName("v2")))

	tcs := map[string]struct {
		startFrom *niceyaml.Revision
		want      string
	}{
		"tip from origin": {
			startFrom: rev0,
			want:      "v2",
		},
		"tip from middle": {
			startFrom: rev1,
			want:      "v2",
		},
		"tip from tip": {
			startFrom: rev2,
			want:      "v2",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.startFrom.Tip()
			assert.Equal(t, tc.want, got.Name())
		})
	}
}

func TestRevision_AtTip(t *testing.T) {
	t.Parallel()

	// Build chain: v0 -> v1 -> v2.
	rev0 := niceyaml.NewRevision(niceyaml.NewSourceFromString("v0: data", niceyaml.WithName("v0")))
	rev1 := rev0.Append(niceyaml.NewSourceFromString("v1: data", niceyaml.WithName("v1")))
	rev2 := rev1.Append(niceyaml.NewSourceFromString("v2: data", niceyaml.WithName("v2")))

	tcs := map[string]struct {
		rev  *niceyaml.Revision
		want bool
	}{
		"origin is not at tip": {
			rev:  rev0,
			want: false,
		},
		"middle is not at tip": {
			rev:  rev1,
			want: false,
		},
		"tip is at tip": {
			rev:  rev2,
			want: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.rev.AtTip()
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestRevision_AtOrigin(t *testing.T) {
	t.Parallel()

	// Build chain: v0 -> v1 -> v2.
	rev0 := niceyaml.NewRevision(niceyaml.NewSourceFromString("v0: data", niceyaml.WithName("v0")))
	rev1 := rev0.Append(niceyaml.NewSourceFromString("v1: data", niceyaml.WithName("v1")))
	rev2 := rev1.Append(niceyaml.NewSourceFromString("v2: data", niceyaml.WithName("v2")))

	tcs := map[string]struct {
		rev  *niceyaml.Revision
		want bool
	}{
		"origin is at origin": {
			rev:  rev0,
			want: true,
		},
		"middle is not at origin": {
			rev:  rev1,
			want: false,
		},
		"tip is not at origin": {
			rev:  rev2,
			want: false,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.rev.AtOrigin()
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestRevision_Names(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		revisions []string
		want      []string
		callFrom  int
	}{
		"single revision": {
			revisions: []string{"only"},
			callFrom:  0,
			want:      []string{"only"},
		},
		"multiple revisions from origin": {
			revisions: []string{"v0", "v1", "v2"},
			callFrom:  0,
			want:      []string{"v0", "v1", "v2"},
		},
		"multiple revisions from middle": {
			revisions: []string{"v0", "v1", "v2"},
			callFrom:  1,
			want:      []string{"v0", "v1", "v2"},
		},
		"multiple revisions from tip": {
			revisions: []string{"v0", "v1", "v2"},
			callFrom:  2,
			want:      []string{"v0", "v1", "v2"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// Build the revision chain.
			var revs []*niceyaml.Revision
			for i, revName := range tc.revisions {
				source := niceyaml.NewSourceFromString(revName+": data", niceyaml.WithName(revName))
				if i == 0 {
					revs = append(revs, niceyaml.NewRevision(source))
				} else {
					revs = append(revs, revs[i-1].Append(source))
				}
			}

			got := revs[tc.callFrom].Names()
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestRevision_Index(t *testing.T) {
	t.Parallel()

	// Build chain: v0 -> v1 -> v2.
	rev0 := niceyaml.NewRevision(niceyaml.NewSourceFromString("v0: data", niceyaml.WithName("v0")))
	rev1 := rev0.Append(niceyaml.NewSourceFromString("v1: data", niceyaml.WithName("v1")))
	rev2 := rev1.Append(niceyaml.NewSourceFromString("v2: data", niceyaml.WithName("v2")))

	tcs := map[string]struct {
		rev  *niceyaml.Revision
		want int
	}{
		"origin index is 0": {
			rev:  rev0,
			want: 0,
		},
		"middle index is 1": {
			rev:  rev1,
			want: 1,
		},
		"tip index is 2": {
			rev:  rev2,
			want: 2,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.rev.Index()
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestRevision_Count(t *testing.T) {
	t.Parallel()

	t.Run("single revision", func(t *testing.T) {
		t.Parallel()

		rev := niceyaml.NewRevision(niceyaml.NewSourceFromString("only: data", niceyaml.WithName("only")))

		got := rev.Count()
		assert.Equal(t, 1, got)
	})

	t.Run("multiple revisions from any position", func(t *testing.T) {
		t.Parallel()

		rev0 := niceyaml.NewRevision(niceyaml.NewSourceFromString("v0: data", niceyaml.WithName("v0")))
		rev1 := rev0.Append(niceyaml.NewSourceFromString("v1: data", niceyaml.WithName("v1")))
		rev2 := rev1.Append(niceyaml.NewSourceFromString("v2: data", niceyaml.WithName("v2")))

		// Count should be 3 from any position.
		assert.Equal(t, 3, rev0.Count())
		assert.Equal(t, 3, rev1.Count())
		assert.Equal(t, 3, rev2.Count())
	})
}

func TestRevision_Prepend(t *testing.T) {
	t.Parallel()

	t.Run("prepend to single revision", func(t *testing.T) {
		t.Parallel()

		rev1 := niceyaml.NewRevision(niceyaml.NewSourceFromString("v1: data", niceyaml.WithName("v1")))
		rev0 := rev1.Prepend(niceyaml.NewSourceFromString("v0: data", niceyaml.WithName("v0")))

		// Rev0 should be the new origin.
		assert.True(t, rev0.AtOrigin())
		assert.False(t, rev0.AtTip())

		// Rev1 should now be the tip.
		assert.False(t, rev1.AtOrigin())
		assert.True(t, rev1.AtTip())

		// Names should be in order.
		assert.Equal(t, []string{"v0", "v1"}, rev0.Names())
	})

	t.Run("prepend to middle of chain", func(t *testing.T) {
		t.Parallel()

		rev1 := niceyaml.NewRevision(niceyaml.NewSourceFromString("v1: data", niceyaml.WithName("v1")))
		rev2 := rev1.Append(niceyaml.NewSourceFromString("v2: data", niceyaml.WithName("v2")))
		rev0 := rev1.Prepend(niceyaml.NewSourceFromString("v0: data", niceyaml.WithName("v0")))

		// Verify chain order.
		assert.Equal(t, []string{"v0", "v1", "v2"}, rev0.Names())
		assert.Equal(t, 3, rev2.Count())
	})
}
