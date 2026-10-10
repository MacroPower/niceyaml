package datapath_test

import (
	"testing"

	"github.com/goccy/go-yaml/ast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/internal/datapath"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

// walk steps down root from the `@` path, by a member name for each
// string of steps and by an index for each int.
func walk(tb testing.TB, idx *datapath.Index, root ast.Node, steps ...any) datapath.Target {
	tb.Helper()

	target := datapath.Target{Path: paths.Current(), Node: root}

	for _, step := range steps {
		switch s := step.(type) {
		case string:
			target = idx.Member(target, s)
		case int:
			target = idx.Element(target, s)
		default:
			require.Failf(tb, "unknown step", "%T", step)
		}
	}

	return target
}

// find walks the first document of input from its root, as [walk] does,
// and returns the target with the index that found it.
func find(tb testing.TB, input string, steps ...any) (*datapath.Index, datapath.Target) {
	tb.Helper()

	doc := yamltest.FirstDocument(tb, input)
	idx := datapath.NewIndex(doc.Resolver())

	return idx, walk(tb, idx, doc.AST(), steps...)
}

var (
	// The key 0x10 of ports sets the member 16.
	portsInput = stringtest.LinesLF("ports:", "  0x10: x")

	// The key 3.10 of python sets the member 3.1.
	pythonInput = stringtest.LinesLF("python:", "  3.10: a")

	// The key 3.10 of python sets the member 3.1. The tagged alias key has no
	// name the index can tell, so it may set a member of any name, and the
	// index leaves out the members before it.
	unnamedKeyInput = stringtest.LinesLF(
		"k: &k 0x10",
		"python:",
		"  3.10: {image: a}",
		"  a: 1",
		"  !!str *k : v",
		"  b: 2",
	)
)

func TestIndex_Member(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		steps []any
		// The path of the target, the place its node starts, and the place
		// of the token an error binds at when the path selects another
		// node.
		want      string
		wantStart string
		wantToken string
		unspelled bool
	}{
		"key spelled as its name": {
			input:     "name: x\n",
			steps:     []any{"name"},
			want:      "@.name",
			wantStart: "x@1:7",
		},
		"hexadecimal key": {
			input:     stringtest.LinesLF("ports:", "  0x10:", "    name: x"),
			steps:     []any{"ports", "16", "name"},
			want:      "@.ports.0x10.name",
			wantStart: "x@3:11",
		},
		"float key with a trailing zero": {
			input:     pythonInput,
			steps:     []any{"python", "3.1"},
			want:      "@.python.'3.10'",
			wantStart: "a@2:9",
		},
		"bool and null keys": {
			input:     "True: a\n~: b\n",
			steps:     []any{"null"},
			want:      "@.'~'",
			wantStart: "b@2:4",
		},
		"quoted key": {
			input:     "\"a b\": x\n",
			steps:     []any{"a b"},
			want:      "@.'a b'",
			wantStart: "x@1:8",
		},
		"later key sets the member again": {
			input:     stringtest.LinesLF("m:", "  16: aaaa", "  0x10: b"),
			steps:     []any{"m", "16"},
			want:      "@.m.0x10",
			wantStart: "b@3:9",
		},
		"member of a merge source": {
			input: stringtest.LinesLF(
				"base: &base",
				"  0x10: x",
				"m:",
				"  <<: *base",
			),
			steps:     []any{"m", "16"},
			want:      "@.m.0x10",
			wantStart: "x@2:9",
		},
		"later merge source wins": {
			input: stringtest.LinesLF(
				"b0: &b0 {True: xxxx}",
				"b1: &b1 {<<: *b0, true: aaaa, True: b}",
				"m: {<<: *b1}",
			),
			steps:     []any{"m", "true"},
			want:      "@.m.True",
			wantStart: "b@2:37",
		},
		"member behind an alias": {
			input:     stringtest.LinesLF("base: &base", "  0x10: x", "al: *base"),
			steps:     []any{"al", "16"},
			want:      "@.al.0x10",
			wantStart: "x@2:9",
		},
		"alias key": {
			input:     stringtest.LinesLF("k: &k 0x10", "m:", "  *k : v"),
			steps:     []any{"m", "16"},
			want:      "@.m.0x10",
			wantStart: "v@3:8",
		},
		"spelling a later key repeats": {
			input: stringtest.LinesLF(
				"user:",
				"  <<: {0x10: x}",
				`  "0x10": 1`,
			),
			steps:     []any{"user", "16"},
			want:      "@.user.16",
			wantStart: "x@2:14",
			wantToken: "x@2:14",
			unspelled: true,
		},
		"names below an unspelled name stay decoded": {
			input: stringtest.LinesLF(
				"user:",
				"  <<: {0x10: {0x20: x}}",
				`  "0x10": 1`,
			),
			steps:     []any{"user", "16", "32"},
			want:      "@.user.16.32",
			wantStart: "x@2:21",
			wantToken: "x@2:21",
			unspelled: true,
		},
		"member the mapping lacks": {
			input:     portsInput,
			steps:     []any{"ports", "0x10"},
			want:      "@.ports.0x10",
			unspelled: true,
		},
		"member before a key with no name": {
			input: stringtest.LinesLF(
				"k: &k {x: y}",
				"m:",
				"  a: 1",
				"  *k : 2",
				"  b: 3",
			),
			steps:     []any{"m", "a"},
			want:      "@.m.a",
			unspelled: true,
		},
		"member after a key with no name": {
			input: stringtest.LinesLF(
				"k: &k {x: y}",
				"m:",
				"  a: 1",
				"  *k : 2",
				"  b: 3",
			),
			steps:     []any{"m", "b"},
			want:      "@.m.b",
			wantStart: "3@5:6",
		},
		"member of an element": {
			input:     stringtest.LinesLF("items:", "  - name: x", "  - 0x10: y"),
			steps:     []any{"items", 1, "16"},
			want:      "@.items[1].0x10",
			wantStart: "y@3:11",
		},
		"name below a scalar": {
			input:     "a: 1\n",
			steps:     []any{"a", "b"},
			want:      "@.a.b",
			unspelled: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, got := find(t, tc.input, tc.steps...)

			assert.Equal(t, tc.want, got.Path.String())
			assert.Equal(t, tc.unspelled, got.Unspelled)
			assert.Equal(t, tc.wantStart, yamltest.TokenPlace(got.Start(false)))
			assert.Equal(t, tc.wantToken, yamltest.TokenPlace(got.Token(false)))
		})
	}

	t.Run("no root", func(t *testing.T) {
		t.Parallel()

		got := walk(t, datapath.NewIndex(paths.NewResolver(nil)), nil, "ports", "16", 0)

		assert.Equal(t, "@.ports.16[0]", got.Path.String())
		assert.True(t, got.Unspelled)
		assert.Nil(t, got.Node)
		assert.Nil(t, got.Token(false))
	})
}

func TestIndex_Element(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input     string
		steps     []any
		want      string
		wantStart string
	}{
		"element of a sequence": {
			input:     stringtest.LinesLF("items:", "  - name: x", "  - name: y"),
			steps:     []any{"items", 1},
			want:      "@.items[1]",
			wantStart: "-@3:3",
		},
		"element behind an alias": {
			input:     stringtest.LinesLF("base: &base [a, b]", "al: *base"),
			steps:     []any{"al", 1},
			want:      "@.al[1]",
			wantStart: "b@1:17",
		},
		"root sequence": {
			input:     "- x\n",
			steps:     []any{0},
			want:      "@[0]",
			wantStart: "x@1:3",
		},
		"index past the end": {
			input: "items: [a]\n",
			steps: []any{"items", 1},
			want:  "@.items[1]",
		},
		"index in a mapping": {
			input: "items: {a: b}\n",
			steps: []any{"items", 0},
			want:  "@.items[0]",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, got := find(t, tc.input, tc.steps...)

			assert.Equal(t, tc.want, got.Path.String())
			assert.False(t, got.Unspelled)
			assert.Nil(t, got.Parent.Entry)
			assert.Equal(t, tc.wantStart, yamltest.TokenPlace(got.Start(false)))
		})
	}
}

func TestTarget_Start(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		steps []any
		// The places of the token for the value and of the token for the
		// key.
		want    string
		wantKey string
	}{
		"entry of a mapping": {
			input:   portsInput,
			steps:   []any{"ports", "16"},
			want:    "x@2:9",
			wantKey: "0x10@2:3",
		},
		"explicit key": {
			input:   "? 0x10\n: x\n",
			steps:   []any{"16"},
			want:    "x@2:3",
			wantKey: "0x10@1:3",
		},
		"element has no key": {
			input:   "- x\n",
			steps:   []any{0},
			want:    "x@1:3",
			wantKey: "x@1:3",
		},
		"root has no key": {
			input:   "a: x\n",
			want:    "a@1:1",
			wantKey: "a@1:1",
		},
		"mapping under a key points at the key": {
			input:   stringtest.LinesLF("ports:", "  0x10:", "    name: x"),
			steps:   []any{"ports", "16"},
			want:    "0x10@2:3",
			wantKey: "0x10@2:3",
		},
		"sequence under a key points at the key": {
			input:   stringtest.LinesLF("items:", "  - x"),
			steps:   []any{"items"},
			want:    "items@1:1",
			wantKey: "items@1:1",
		},
		"mapping element points at its dash": {
			input:   stringtest.LinesLF("items:", "  - name: x"),
			steps:   []any{"items", 0},
			want:    "-@2:3",
			wantKey: "-@2:3",
		},
		"flow mapping element of a flow sequence points at its brace": {
			input:   "items: [{a: 1}, {b: 2}]\n",
			steps:   []any{"items", 1},
			want:    "{@1:17",
			wantKey: "{@1:17",
		},
		"flow sequence at the root points at its bracket": {
			input:   "[a, b]\n",
			want:    "[@1:1",
			wantKey: "[@1:1",
		},
		"member the mapping lacks": {
			input: "a: x\n",
			steps: []any{"b"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, got := find(t, tc.input, tc.steps...)

			assert.Equal(t, tc.want, yamltest.TokenPlace(got.Start(false)))
			assert.Equal(t, tc.wantKey, yamltest.TokenPlace(got.Start(true)))
		})
	}
}

func TestTarget_Token(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input   string
		steps   []any
		want    string
		wantKey string
	}{
		"path selects the member": {
			input: portsInput,
			steps: []any{"ports", "16"},
		},
		"path selects another entry": {
			input: stringtest.LinesLF(
				"user:",
				"  <<: {0x10: x}",
				`  "0x10": 1`,
			),
			steps:   []any{"user", "16"},
			want:    "x@2:14",
			wantKey: "0x10@2:8",
		},
		"walk reached no node": {
			input: stringtest.LinesLF(
				"user:",
				"  <<: {0x10: x}",
				`  "0x10": 1`,
			),
			steps: []any{"user", "16", "name"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, got := find(t, tc.input, tc.steps...)

			assert.Equal(t, tc.want, yamltest.TokenPlace(got.Token(false)))
			assert.Equal(t, tc.wantKey, yamltest.TokenPlace(got.Token(true)))
		})
	}
}

func TestIndex_Deref(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		steps []any
		want  string
	}{
		"plain value": {
			input: "a: x\n",
			steps: []any{"a"},
			want:  "x",
		},
		"anchor and tag": {
			input: "a: &a !!str x\n",
			steps: []any{"a"},
			want:  "x",
		},
		"alias": {
			input: "a: &a x\nb: *a\n",
			steps: []any{"b"},
			want:  "x",
		},
		"alias to a tagged value": {
			input: "a: &a !!str x\nb: *a\n",
			steps: []any{"b"},
			want:  "x",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			idx, target := find(t, tc.input, tc.steps...)
			got := idx.Deref(target.Node)

			require.NotNil(t, got)
			assert.Equal(t, tc.want, got.String())
		})
	}

	t.Run("no node", func(t *testing.T) {
		t.Parallel()

		assert.Nil(t, datapath.NewIndex(paths.NewResolver(nil)).Deref(nil))
	})

	t.Run("alias with no anchor", func(t *testing.T) {
		t.Parallel()

		idx, target := find(t, "a: *missing\n", "a")

		assert.Nil(t, idx.Deref(target.Node))
	})
}

func TestIndex_MemberNode(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		name  string
		want  string
	}{
		"member by its decoded name": {
			input: "0x10: x\n",
			name:  "16",
			want:  "x",
		},
		"last of two keys with one name": {
			input: "16: a\n0x10: b\n",
			name:  "16",
			want:  "b",
		},
		"member of a merge source": {
			input: "<<: {0x10: x}\n",
			name:  "16",
			want:  "x",
		},
		"source spelling": {
			input: "0x10: x\n",
			name:  "0x10",
		},
		"sequence": {
			input: "- x\n",
			name:  "0",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			idx, root := find(t, tc.input)

			got := idx.MemberNode(root.Node, tc.name)
			if tc.want == "" {
				assert.Nil(t, got)

				return
			}

			require.NotNil(t, got)
			assert.Equal(t, tc.want, got.String())
		})
	}
}

func TestElementNode(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  string
		index int
	}{
		"first element": {
			input: "[a, b]\n",
			index: 0,
			want:  "a",
		},
		"last element": {
			input: "[a, b]\n",
			index: 1,
			want:  "b",
		},
		"anchored sequence": {
			input: "&s [a, b]\n",
			index: 1,
			want:  "b",
		},
		"index past the end": {
			input: "[a, b]\n",
			index: 2,
		},
		"negative index": {
			input: "[a, b]\n",
			index: -1,
		},
		"mapping": {
			input: "a: b\n",
			index: 0,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := datapath.ElementNode(yamltest.FirstDocument(t, tc.input).AST(), tc.index)
			if tc.want == "" {
				assert.Nil(t, got)

				return
			}

			require.NotNil(t, got)
			assert.Equal(t, tc.want, got.String())
		})
	}

	t.Run("no node", func(t *testing.T) {
		t.Parallel()

		assert.Nil(t, datapath.ElementNode(nil, 0))
	})
}

func TestIndex_SelectsEntry(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		steps []any
		name  string
		want  bool
	}{
		"source spelling of a key": {
			input: pythonInput,
			steps: []any{"python"},
			name:  "3.10",
			want:  true,
		},
		"decoded name of a respelled key": {
			input: pythonInput,
			steps: []any{"python"},
			name:  "3.1",
		},
		"key of a merge source": {
			input: stringtest.LinesLF("m:", "  <<: {0x10: x}"),
			steps: []any{"m"},
			name:  "0x10",
			want:  true,
		},
		"mapping behind an alias": {
			input: stringtest.LinesLF("base: &base {a: b}", "al: *base"),
			steps: []any{"al"},
			name:  "a",
			want:  true,
		},
		"sequence": {
			input: "items: [a]\n",
			steps: []any{"items"},
			name:  "0",
		},
		"scalar": {
			input: "a: b\n",
			steps: []any{"a"},
			name:  "b",
		},
		"no node": {
			input: "a: b\n",
			steps: []any{"missing"},
			name:  "a",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			idx, target := find(t, tc.input, tc.steps...)

			assert.Equal(t, tc.want, idx.SelectsEntry(target.Node, tc.name))
		})
	}
}

func TestIndex_Lacks(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		steps []any
		name  string
		want  bool
	}{
		"member the mapping holds": {
			input: portsInput,
			steps: []any{"ports"},
			name:  "16",
		},
		"source spelling of a respelled key": {
			input: portsInput,
			steps: []any{"ports"},
			name:  "0x10",
			want:  true,
		},
		"member of no key": {
			input: portsInput,
			steps: []any{"ports"},
			name:  "name",
			want:  true,
		},
		"member a merge source holds": {
			input: stringtest.LinesLF("m:", "  <<: {0x10: x}"),
			steps: []any{"m"},
			name:  "16",
		},
		"mapping behind an alias": {
			input: stringtest.LinesLF("base: &base {a: b}", "al: *base"),
			steps: []any{"al"},
			name:  "c",
			want:  true,
		},
		"sequence": {
			input: "items: [a]\n",
			steps: []any{"items"},
			name:  "0",
			want:  true,
		},
		"scalar": {
			input: "a: b\n",
			steps: []any{"a"},
			name:  "b",
			want:  true,
		},
		"null": {
			input: "a: ~\n",
			steps: []any{"a"},
			name:  "b",
			want:  true,
		},
		"member before a key with no name": {
			input: unnamedKeyInput,
			steps: []any{"python"},
			name:  "a",
		},
		"member after a key with no name": {
			input: unnamedKeyInput,
			steps: []any{"python"},
			name:  "b",
		},
		"member of no key beside a key with no name": {
			input: unnamedKeyInput,
			steps: []any{"python"},
			name:  "zz",
		},
		"member beside a merge key that does not resolve": {
			input: stringtest.LinesLF("m:", "  a: 1", "  <<: *missing"),
			steps: []any{"m"},
			name:  "zz",
		},
		"alias that does not resolve": {
			input: "a: *missing\n",
			steps: []any{"a"},
			name:  "b",
		},
		"no node": {
			input: "a: b\n",
			steps: []any{"missing"},
			name:  "a",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			idx, target := find(t, tc.input, tc.steps...)

			assert.Equal(t, tc.want, idx.Lacks(target.Node, tc.name))
		})
	}
}

func TestIndex_SelectsOther(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		steps []any
		name  string
		want  bool
	}{
		"spelling of a key with another name": {
			input: pythonInput,
			steps: []any{"python"},
			name:  "3.10",
			want:  true,
		},
		"name of a member": {
			input: pythonInput,
			steps: []any{"python"},
			name:  "3.1",
		},
		"name no key spells": {
			input: pythonInput,
			steps: []any{"python"},
			name:  "zz",
		},
		"spelling a merge source gives a key with another name": {
			input: stringtest.LinesLF("m:", "  <<: {0x10: x}"),
			steps: []any{"m"},
			name:  "0x10",
			want:  true,
		},
		"spelling of a key with another name before a key with no name": {
			input: unnamedKeyInput,
			steps: []any{"python"},
			name:  "3.10",
			want:  true,
		},
		"key spelled as its name before a key with no name": {
			input: unnamedKeyInput,
			steps: []any{"python"},
			name:  "a",
		},
		"spelling of a key with no name": {
			input: unnamedKeyInput,
			steps: []any{"python"},
			name:  "0x10",
			want:  true,
		},
		"merge key that does not resolve": {
			input: stringtest.LinesLF("m:", "  3.10: a", "  <<: *missing"),
			steps: []any{"m"},
			name:  "3.10",
		},
		"sequence": {
			input: "items: [a]\n",
			steps: []any{"items"},
			name:  "0",
		},
		"no node": {
			input: "a: b\n",
			steps: []any{"missing"},
			name:  "a",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			idx, target := find(t, tc.input, tc.steps...)

			assert.Equal(t, tc.want, idx.SelectsOther(target.Node, tc.name))
		})
	}
}

func TestMemberName(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		key  any
		want string
	}{
		"nil":      {key: nil, want: "null"},
		"string":   {key: "0x10", want: "0x10"},
		"empty":    {key: "", want: ""},
		"int":      {key: 16, want: "16"},
		"unsigned": {key: uint64(16), want: "16"},
		"float":    {key: 3.1, want: "3.1"},
		"exponent": {key: 1234567.0, want: "1.234567e+06"},
		"bool":     {key: true, want: "true"},
		"bytes":    {key: []byte("hi"), want: "[104 105]"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, datapath.MemberName(tc.key))
		})
	}
}
