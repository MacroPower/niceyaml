package aliasing_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/aliasing"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

// aliasLevels returns a list whose last level expands to 10^7 scalars,
// since each level lists the level below ten times, with anchors l0
// through l7.
func aliasLevels() string {
	var sb strings.Builder

	sb.WriteString("a:\n  - &l0 [x]\n")

	for level := 1; level <= 7; level++ {
		aliases := strings.Repeat(fmt.Sprintf("*l%d, ", level-1), 10)
		fmt.Fprintf(&sb, "  - &l%d [%s]\n", level, strings.TrimSuffix(aliases, ", "))
	}

	return sb.String()
}

func TestCheckDecode(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		err   error
		input string
		path  paths.Path
	}{
		"no alias": {
			input: "a: [x, y]\n",
		},
		"alias to a small sequence": {
			input: "s: &s [a, b]\nt: *s\n",
		},
		"alias bomb as mapping key": {
			input: aliasLevels() + "kind:\n  ? *l7\n  : v\n",
			err:   aliasing.ErrExcessiveAliasing,
		},
		"merge key bomb": {
			input: mergeLevels(),
			err:   aliasing.ErrExcessiveAliasing,
		},
		"node holding an alias with a bomb outside it": {
			// A decode of a node that holds an alias reads the whole
			// document to find the anchor.
			input: "k: &k a\n" + aliasLevels() + "b:\n  ? *l7\n  : v\nc: [*k]\n",
			path:  paths.Root().Child("c"),
			err:   aliasing.ErrExcessiveAliasing,
		},
		"node without an alias beside a bomb": {
			input: aliasLevels() + "b:\n  ? *l7\n  : v\nc: [x]\n",
			path:  paths.Root().Child("c"),
		},
		"aliases inside their own anchor": {
			input: "x: &x [" + strings.Repeat("a, ", 10) + strings.Repeat("*x, ", 299) + "*x]\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input)
			if !tc.path.IsRoot() {
				doc = yamltest.At(t, doc, tc.path)
			}

			err := aliasing.CheckDecode(doc)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				return
			}

			require.NoError(t, err)
		})
	}

	t.Run("nil node", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, aliasing.CheckDecode((*niceyaml.Node)(nil)))
	})
}

// mergeLevels returns a document whose levels each merge the level below
// ten times, so a decode reads the first level 10^7 times.
func mergeLevels() string {
	var sb strings.Builder

	sb.WriteString("m0: &m0 {a: x}\n")

	for level := 1; level <= 7; level++ {
		aliases := strings.Repeat(fmt.Sprintf("*m%d, ", level-1), 10)
		fmt.Fprintf(&sb, "m%d: &m%d\n  <<: [%s]\n", level, level, strings.TrimSuffix(aliases, ", "))
	}

	return sb.String()
}

func TestExcessive(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		distinct int
		aliased  int
		want     bool
	}{
		"few aliased nodes": {
			distinct: 10,
			aliased:  100,
		},
		"small document": {
			distinct: 1,
			aliased:  998,
		},
		"aliases under the larger share": {
			distinct: 20,
			aliased:  1000,
		},
		"aliases past the larger share": {
			distinct: 5,
			aliased:  1000,
			want:     true,
		},
		"aliases past the smaller share": {
			distinct: 1_000_000,
			aliased:  4_000_000,
			want:     true,
		},
		"aliases under the smaller share": {
			distinct: 4_000_000,
			aliased:  400_000,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, aliasing.Excessive(tc.distinct, tc.aliased))
		})
	}
}

func TestAddCapped(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		a, b int
		want int
	}{
		"under the cap": {a: 1, b: 2, want: 3},
		"at the cap":    {a: aliasing.CountCap - 1, b: 1, want: aliasing.CountCap},
		"past the cap":  {a: aliasing.CountCap, b: aliasing.CountCap, want: aliasing.CountCap},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, aliasing.AddCapped(tc.a, tc.b))
		})
	}
}
