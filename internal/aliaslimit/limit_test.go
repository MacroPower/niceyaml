package aliaslimit_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/internal/aliaslimit"
)

func TestExcessive(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		distinct int
		aliased  int
		want     bool
	}{
		"few aliased nodes":               {distinct: 10, aliased: 100},
		"small document":                  {distinct: 1, aliased: 998},
		"aliases under the larger share":  {distinct: 20, aliased: 1000},
		"aliases past the larger share":   {distinct: 5, aliased: 1000, want: true},
		"aliases past the smaller share":  {distinct: 1_000_000, aliased: 4_000_000, want: true},
		"aliases under the smaller share": {distinct: 4_000_000, aliased: 400_000},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, aliaslimit.Excessive(tc.distinct, tc.aliased))
		})
	}
}

func TestAllows(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		aliased int
		want    bool
	}{
		"no aliased nodes":     {aliased: 0, want: true},
		"at the floor":         {aliased: 100, want: true},
		"past the floor":       {aliased: 101},
		"far past the floor":   {aliased: 4_000_000},
		"the cap of the count": {aliased: aliaslimit.CountCap},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, aliaslimit.Allows(tc.aliased))

			// Excessive reports false for each count of distinct nodes beside
			// aliased nodes that the limit allows.
			if tc.want {
				for _, distinct := range []int{0, 1, 1000, 400_000, 4_000_000, aliaslimit.CountCap} {
					assert.False(t, aliaslimit.Excessive(distinct, tc.aliased), "distinct %d", distinct)
				}
			}
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
		"at the cap":    {a: aliaslimit.CountCap - 1, b: 1, want: aliaslimit.CountCap},
		"past the cap":  {a: aliaslimit.CountCap, b: aliaslimit.CountCap, want: aliaslimit.CountCap},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, aliaslimit.AddCapped(tc.a, tc.b))
		})
	}
}
