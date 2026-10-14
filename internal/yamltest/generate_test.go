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
			want:     stringtest.LinesLF("a:", "  - &l0 [x]"),
		},
		"one alias level": {
			generate: yamltest.AliasLevels,
			levels:   1,
			want: stringtest.LinesLF(
				"a:",
				"  - &l0 [x]",
				"  - &l1 [*l0, *l0, *l0, *l0, *l0, *l0, *l0, *l0, *l0, *l0]",
			),
		},
		"merge levels without merges": {
			generate: yamltest.MergeLevels,
			levels:   0,
			want:     "m0: &m0 {a: x}\n",
		},
		"one merge level": {
			generate: yamltest.MergeLevels,
			levels:   1,
			want: stringtest.LinesLF(
				"m0: &m0 {a: x}",
				"m1: &m1",
				"  <<: [*m0, *m0, *m0, *m0, *m0, *m0, *m0, *m0, *m0, *m0]",
			),
		},
		"wide merge of one member": {
			generate: yamltest.WideMerge,
			levels:   1,
			want: stringtest.LinesLF(
				"a: &a {z: 0}",
				"m:",
				"  0x1000: x",
				"  <<: [*a]",
			),
		},
		"wide merge of two members": {
			generate: yamltest.WideMerge,
			levels:   2,
			want: stringtest.LinesLF(
				"a: &a {z: 0}",
				"m:",
				"  0x1000: x",
				"  0x1001: x",
				"  <<: [*a, *a]",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.generate(tc.levels))
		})
	}
}

func TestGenerateLines(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		format string
		want   string
		lines  int
	}{
		"no lines":       {lines: 0, format: "k%d: v", want: ""},
		"negative count": {lines: -1, format: "k%d: v", want: ""},
		"one line":       {lines: 1, format: "k%d: v", want: "k0: v\n"},
		"index in each line": {
			lines:  3,
			format: "k%d: v",
			want:   stringtest.LinesLF("k0: v", "k1: v", "k2: v"),
		},
		"index twice in lines": {lines: 2, format: "k%[1]d: v%[1]d", want: stringtest.LinesLF("k0: v0", "k1: v1")},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, yamltest.GenerateLines(tc.lines, tc.format))
		})
	}
}

func TestGenerateYAML(t *testing.T) {
	t.Parallel()

	assert.Equal(t, stringtest.LinesLF("key_0: value_0", "key_1: value_1"), yamltest.GenerateYAML(2))
	assert.Empty(t, yamltest.GenerateYAML(0))
}

func TestWideMergeOf(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		item string
		want string
		tail []string
	}{
		"alias to the mapping a": {
			item: "*a",
			want: yamltest.WideMerge(2),
		},
		"another item": {
			item: "0",
			want: stringtest.LinesLF(
				"a: &a {z: 0}",
				"m:",
				"  0x1000: x",
				"  0x1001: x",
				"  <<: [0, 0]",
			),
		},
		"tail after the items": {
			item: "*a",
			tail: []string{"*late", "x"},
			want: stringtest.LinesLF(
				"a: &a {z: 0}",
				"m:",
				"  0x1000: x",
				"  0x1001: x",
				"  <<: [*a, *a, *late, x]",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, yamltest.WideMergeOf(2, tc.item, tc.tail...))
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
	assert.Equal(t, stringtest.LinesLF("defaults: &d", "services:"), yamltest.AliasedMapping(0, 0))
}

func TestAliasKey(t *testing.T) {
	t.Parallel()

	assert.Equal(t, stringtest.LinesLF("kind:", "  ? *l7", "  : v"), yamltest.AliasKey("kind", 7))
}

func TestSchemaDirective(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		body string
		want string
	}{
		"body": {
			body: "name: a\n",
			want: stringtest.LinesLF("# yaml-language-server: $schema=./s.json", "name: a"),
		},
		"no body": {
			want: "# yaml-language-server: $schema=./s.json\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, yamltest.SchemaDirective("./s.json", tc.body))
		})
	}
}
