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

func TestRevision_Source(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("key: value", niceyaml.WithName("test"))
	rev := niceyaml.NewRevision(source)

	got := rev.Source()

	assert.Equal(t, source, got)
	assert.Equal(t, 1, got.Len())
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

func TestRevision_Len(t *testing.T) {
	t.Parallel()

	t.Run("single revision", func(t *testing.T) {
		t.Parallel()

		rev := niceyaml.NewRevision(niceyaml.NewSourceFromString("only: data", niceyaml.WithName("only")))

		got := rev.Len()
		assert.Equal(t, 1, got)
	})

	t.Run("multiple revisions from any position", func(t *testing.T) {
		t.Parallel()

		rev0 := niceyaml.NewRevision(niceyaml.NewSourceFromString("v0: data", niceyaml.WithName("v0")))
		rev1 := rev0.Append(niceyaml.NewSourceFromString("v1: data", niceyaml.WithName("v1")))
		rev2 := rev1.Append(niceyaml.NewSourceFromString("v2: data", niceyaml.WithName("v2")))

		// Count should be 3 from any position.
		assert.Equal(t, 3, rev0.Len())
		assert.Equal(t, 3, rev1.Len())
		assert.Equal(t, 3, rev2.Len())
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
		assert.Equal(t, 3, rev2.Len())
	})
}

func TestRevision_Seek(t *testing.T) {
	t.Parallel()

	// Build chain: v0 -> v1 -> v2.
	rev0 := niceyaml.NewRevision(niceyaml.NewSourceFromString("v0: data", niceyaml.WithName("v0")))
	rev1 := rev0.Append(niceyaml.NewSourceFromString("v1: data", niceyaml.WithName("v1")))
	rev2 := rev1.Append(niceyaml.NewSourceFromString("v2: data", niceyaml.WithName("v2")))

	tcs := map[string]struct {
		startFrom *niceyaml.Revision
		n         int
		want      string
	}{
		"seek forward from origin": {
			startFrom: rev0,
			n:         1,
			want:      "v1",
		},
		"seek forward by two": {
			startFrom: rev0,
			n:         2,
			want:      "v2",
		},
		"seek backward from tip": {
			startFrom: rev2,
			n:         -1,
			want:      "v1",
		},
		"seek backward by two": {
			startFrom: rev2,
			n:         -2,
			want:      "v0",
		},
		"seek by zero stays same": {
			startFrom: rev1,
			n:         0,
			want:      "v1",
		},
		"seek beyond tip clamps": {
			startFrom: rev1,
			n:         100,
			want:      "v2",
		},
		"seek before origin clamps": {
			startFrom: rev1,
			n:         -100,
			want:      "v0",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.startFrom.Seek(tc.n)
			assert.Equal(t, tc.want, got.Name())
		})
	}
}

func TestRevision_Origin(t *testing.T) {
	t.Parallel()

	// Build chain: v0 -> v1 -> v2.
	rev0 := niceyaml.NewRevision(niceyaml.NewSourceFromString("v0: data", niceyaml.WithName("v0")))
	rev1 := rev0.Append(niceyaml.NewSourceFromString("v1: data", niceyaml.WithName("v1")))
	rev2 := rev1.Append(niceyaml.NewSourceFromString("v2: data", niceyaml.WithName("v2")))

	tcs := map[string]struct {
		startFrom *niceyaml.Revision
		want      string
	}{
		"origin from origin": {
			startFrom: rev0,
			want:      "v0",
		},
		"origin from middle": {
			startFrom: rev1,
			want:      "v0",
		},
		"origin from tip": {
			startFrom: rev2,
			want:      "v0",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.startFrom.Origin()
			assert.Equal(t, tc.want, got.Name())
		})
	}
}

func TestRevision_Append(t *testing.T) {
	t.Parallel()

	t.Run("append to single revision", func(t *testing.T) {
		t.Parallel()

		rev0 := niceyaml.NewRevision(niceyaml.NewSourceFromString("v0: data", niceyaml.WithName("v0")))
		rev1 := rev0.Append(niceyaml.NewSourceFromString("v1: data", niceyaml.WithName("v1")))

		// Rev0 should still be the origin.
		assert.True(t, rev0.AtOrigin())
		assert.False(t, rev0.AtTip())

		// Rev1 should be the new tip.
		assert.False(t, rev1.AtOrigin())
		assert.True(t, rev1.AtTip())

		// Verify chain.
		assert.Equal(t, []string{"v0", "v1"}, rev0.Names())
		assert.Equal(t, 2, rev0.Len())
	})

	t.Run("append to tip of chain", func(t *testing.T) {
		t.Parallel()

		rev0 := niceyaml.NewRevision(niceyaml.NewSourceFromString("v0: data", niceyaml.WithName("v0")))
		rev1 := rev0.Append(niceyaml.NewSourceFromString("v1: data", niceyaml.WithName("v1")))
		rev2 := rev1.Append(niceyaml.NewSourceFromString("v2: data", niceyaml.WithName("v2")))

		// Rev1 should no longer be at tip.
		assert.False(t, rev1.AtTip())

		// Rev2 should be the new tip.
		assert.True(t, rev2.AtTip())

		// Verify chain.
		assert.Equal(t, []string{"v0", "v1", "v2"}, rev0.Names())
	})

	t.Run("append returns new tip", func(t *testing.T) {
		t.Parallel()

		rev0 := niceyaml.NewRevision(niceyaml.NewSourceFromString("v0: data", niceyaml.WithName("v0")))
		rev1 := rev0.Append(niceyaml.NewSourceFromString("v1: data", niceyaml.WithName("v1")))

		// The returned revision is the new tip.
		assert.Same(t, rev0.Tip(), rev1)
	})
}

func TestRevision_Name(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		name string
		want string
	}{
		"simple name": {
			name: "test",
			want: "test",
		},
		"empty name": {
			name: "",
			want: "",
		},
		"path-like name": {
			name: "/path/to/file.yaml",
			want: "/path/to/file.yaml",
		},
		"name with special chars": {
			name: "file-v1.2.3_draft",
			want: "file-v1.2.3_draft",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString("key: value", niceyaml.WithName(tc.name))
			rev := niceyaml.NewRevision(source)

			assert.Equal(t, tc.want, rev.Name())
		})
	}
}

func TestRevision_LongChain(t *testing.T) {
	t.Parallel()

	// Build a chain of 10 revisions.
	const chainLen = 10

	var revs []*niceyaml.Revision

	for i := range chainLen {
		name := "v" + string(rune('0'+i))
		source := niceyaml.NewSourceFromString(name+": data", niceyaml.WithName(name))

		if i == 0 {
			revs = append(revs, niceyaml.NewRevision(source))
		} else {
			revs = append(revs, revs[i-1].Append(source))
		}
	}

	// Verify Len works from any position.
	for i, rev := range revs {
		assert.Equal(t, chainLen, rev.Len(), "Len() from revision %d", i)
	}

	// Verify At works from any position.
	for i := range chainLen {
		for j := range chainLen {
			got := revs[i].At(j)
			want := "v" + string(rune('0'+j))
			assert.Equal(t, want, got.Name(), "At(%d) from revision %d", j, i)
		}
	}

	// Verify Seek works correctly.
	middle := revs[5]
	assert.Equal(t, "v3", middle.Seek(-2).Name())
	assert.Equal(t, "v7", middle.Seek(2).Name())
	assert.Equal(t, "v0", middle.Seek(-100).Name())
	assert.Equal(t, "v9", middle.Seek(100).Name())
}
