package yamltest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.jacobcolvin.com/x/stringtest"

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
		"wide merge of one member": {
			generate: yamltest.WideMerge,
			levels:   1,
			want:     "a: &a {z: 0}\nm:\n  0x1000: x\n  <<: [*a]\n",
		},
		"wide merge of two members": {
			generate: yamltest.WideMerge,
			levels:   2,
			want:     "a: &a {z: 0}\nm:\n  0x1000: x\n  0x1001: x\n  <<: [*a, *a]\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.generate(tc.levels))
		})
	}
}

func TestFlowSequence(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		item  string
		want  string
		tail  []string
		count int
	}{
		"no items":            {item: "a", count: 0, want: "[]"},
		"one item":            {item: "a", count: 1, want: "[a]"},
		"repeated item":       {item: "*a", count: 3, want: "[*a, *a, *a]"},
		"tail after the item": {item: "*a", count: 2, tail: []string{"x", "y"}, want: "[*a, *a, x, y]"},
		"tail alone":          {item: "a", count: 0, tail: []string{"x"}, want: "[x]"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, yamltest.FlowSequence(tc.item, tc.count, tc.tail...))
		})
	}
}

func TestAliasedMapping(t *testing.T) {
	t.Parallel()

	want := stringtest.LinesLF(
		"defaults: &d",
		"  k0: v0",
		"  k1: v1",
		"services:",
		"  s0: *d",
		"  s1: *d",
		"  s2: *d",
	)

	assert.Equal(t, want, yamltest.AliasedMapping(2, 3))
	assert.Equal(t, "defaults: &d\nservices:\n", yamltest.AliasedMapping(0, 0))
}
