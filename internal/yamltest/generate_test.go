package yamltest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
)

func TestAliasBombs(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		generate func(levels int) string
		want     string
		levels   int
	}{
		"alias levels without aliases": {
			generate: yamltest.AliasLevels,
			levels:   0,
			want:     "a:\n  - &l0 [x]\n",
		},
		"one alias level": {
			generate: yamltest.AliasLevels,
			levels:   1,
			want: "a:\n  - &l0 [x]\n" +
				"  - &l1 [*l0, *l0, *l0, *l0, *l0, *l0, *l0, *l0, *l0, *l0]\n",
		},
		"merge levels without merges": {
			generate: yamltest.MergeLevels,
			levels:   0,
			want:     "m0: &m0 {a: x}\n",
		},
		"one merge level": {
			generate: yamltest.MergeLevels,
			levels:   1,
			want: "m0: &m0 {a: x}\n" +
				"m1: &m1\n  <<: [*m0, *m0, *m0, *m0, *m0, *m0, *m0, *m0, *m0, *m0]\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.generate(tc.levels))
		})
	}
}
