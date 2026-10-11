package datapath_test

import (
	"fmt"
	"iter"
	"math"
	"reflect"
	"testing"
	"time"

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

// mappingAt returns the resolver of the first document of input and the
// mapping the path $.name selects there.
func mappingAt(tb testing.TB, input, name string) (*paths.Resolver, *ast.MappingNode) {
	tb.Helper()

	r := yamltest.FirstDocument(tb, input).Resolver()

	node, err := r.Node(paths.Doc().Child(name))
	require.NoError(tb, err)

	mapping, ok := datapath.Deref(r, node).(*ast.MappingNode)
	require.True(tb, ok)

	return r, mapping
}

// keys returns the text of the key of each entry of seq.
func keys(seq iter.Seq[*ast.MappingValueNode]) []string {
	var out []string

	for entry := range seq {
		out = append(out, entry.Key.String())
	}

	return out
}

func TestDeref(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, "a: &a !!str x\nb: *a\nc: *missing\n")
	r := doc.Resolver()

	b, err := r.Node(paths.Doc().Child("b"))
	require.NoError(t, err)

	got := datapath.Deref(r, b)
	require.NotNil(t, got)
	assert.Equal(t, "x", got.String())

	c, ok := doc.AST().(*ast.MappingNode)
	require.True(t, ok)
	assert.Nil(t, datapath.Deref(r, c.Values[2].Value))
}

func TestRead(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		name string
		want string
		ok   bool
	}{
		"value":            {name: "a", want: "x", ok: true},
		"anchored value":   {name: "b", want: "y", ok: true},
		"alias":            {name: "c", want: "y", ok: true},
		"null":             {name: "g"},
		"alias to a null":  {name: "e"},
		"alias to nothing": {name: "f"},
	}

	input := stringtest.LinesLF(
		"a: x",
		"b: &b y",
		"c: *b",
		"d: &d null",
		"e: *d",
		"f: *missing",
		"g: null",
	)

	doc := yamltest.FirstDocument(t, input)
	r := doc.Resolver()

	mapping, ok := doc.AST().(*ast.MappingNode)
	require.True(t, ok)

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var value ast.Node

			for _, entry := range mapping.Values {
				if entry.Key.String() == tc.name {
					value = entry.Value
				}
			}

			got, ok := datapath.Read(r, value)
			assert.Equal(t, tc.ok, ok)

			if tc.ok {
				assert.Equal(t, tc.want, got.String())
			}
		})
	}

	t.Run("nil", func(t *testing.T) {
		t.Parallel()

		_, ok := datapath.Read(r, nil)
		assert.False(t, ok)
	})
}

func TestParentAt(t *testing.T) {
	t.Parallel()

	input := stringtest.LinesLF(
		"a: {k: v}",
		"l: [x, y]",
	)

	r := yamltest.FirstDocument(t, input).Resolver()

	tcs := map[string]struct {
		path  paths.Path
		entry string
		index int
		seq   bool
	}{
		"root":                 {path: paths.Doc()},
		"entry":                {path: paths.Doc().Child("a"), entry: "a"},
		"entry below an entry": {path: paths.Doc().Child("a", "k"), entry: "k"},
		"element":              {path: paths.Doc().Child("l").Index(1), index: 1, seq: true},
		"key of an entry":      {path: paths.Doc().Child("a").Key()},
		"missing entry":        {path: paths.Doc().Child("z")},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := datapath.ParentAt(r, tc.path)

			if tc.entry == "" {
				assert.Nil(t, got.Entry)
			} else {
				require.NotNil(t, got.Entry)
				assert.Equal(t, tc.entry, got.Entry.Key.String())
			}

			assert.Equal(t, tc.seq, got.Sequence != nil)
			assert.Equal(t, tc.index, got.Index)
		})
	}
}

func TestMergeSources(t *testing.T) {
	t.Parallel()

	input := stringtest.LinesLF(
		"a: &a {x: 1}",
		"b: &b {y: 2}",
		"m:",
		"  <<: [*a, *b, scalar]",
		"n:",
		"  <<: *missing",
	)

	r, m := mappingAt(t, input, "m")

	sources, err := datapath.MergeSources(r, m.Values[0])
	require.NoError(t, err)
	require.Len(t, sources, 2)
	assert.Equal(t, "x", sources[0].Values[0].Key.String())
	assert.Equal(t, "y", sources[1].Values[0].Key.String())

	_, n := mappingAt(t, input, "n")

	_, err = datapath.MergeSources(r, n.Values[0])
	require.ErrorIs(t, err, paths.ErrAlias)
}

func TestMembers(t *testing.T) {
	t.Parallel()

	input := stringtest.LinesLF(
		"b: &b {x: 1, y: 2}",
		"m: &m",
		"  a: 1",
		"  <<: *b",
		"  z: 3",
		"u:",
		"  a: 1",
		"  <<: *missing",
		"  z: 3",
		"s:",
		"  <<: [*b, *b]",
	)

	tcs := map[string]struct {
		name     string
		merges   bool
		forward  []string
		backward []string
	}{
		"merges where the key stands": {
			name:     "m",
			merges:   true,
			forward:  []string{"a", "x", "y", "z"},
			backward: []string{"z", "y", "x", "a"},
		},
		"no merges": {
			name:    "m",
			forward: []string{"a", "z"},
		},
		"unresolved merge": {
			name:     "u",
			merges:   true,
			forward:  []string{"a", "z"},
			backward: []string{"z"},
		},
		"each mapping once": {
			name:     "s",
			merges:   true,
			forward:  []string{"x", "y"},
			backward: []string{"y", "x"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r, m := mappingAt(t, input, tc.name)

			assert.Equal(t, tc.forward, keys(datapath.Members(r, m, tc.merges)))

			if tc.merges {
				assert.Equal(t, tc.backward, keys(datapath.MembersBackward(r, m)))
			}
		})
	}

	t.Run("stop", func(t *testing.T) {
		t.Parallel()

		r, m := mappingAt(t, input, "m")

		for range datapath.Members(r, m, true) {
			break
		}

		for range datapath.MembersBackward(r, m) {
			break
		}
	})
}

func TestIndex_Locate(t *testing.T) {
	t.Parallel()

	input := stringtest.LinesLF(
		"ports:",
		"  0x10: {name: x}",
		"list: [a, b]",
		"python:",
		"  3.10: {image: a}",
	)

	tcs := map[string]struct {
		names []string
		key   bool
		want  string
		token string
	}{
		"member": {
			names: []string{"ports", "16", "name"},
			want:  "@.ports.0x10.name",
		},
		"key of a member": {
			names: []string{"ports", "16"},
			key:   true,
			want:  "@.ports.0x10~",
		},
		"element": {
			names: []string{"list", "1"},
			want:  "@.list[1]",
		},
		"element past the end": {
			names: []string{"list", "5", "x"},
			want:  "@.list[5].x",
		},
		"missing member": {
			names: []string{"ports", "16", "port"},
			want:  "@.ports.0x10.port",
		},
		"key that sets another member": {
			names: []string{"python", "3.10", "image"},
			want:  "@.python.'3.10'.image",
			token: "python@4:1",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, input)
			idx := datapath.NewIndex(doc.Resolver())

			path, tk := idx.Locate(datapath.Target{Path: paths.Current(), Node: doc.AST()}, tc.names, tc.key)

			assert.Equal(t, tc.want, path.String())

			if tc.token == "" {
				assert.Nil(t, tk)
			} else {
				assert.Equal(t, tc.token, yamltest.TokenPlace(tk))
			}
		})
	}
}

// keyed is a value a pointer key points to.
type keyed struct{ N int }

func TestKeyOf(t *testing.T) {
	t.Parallel()

	key := func(t *testing.T, v any) any {
		t.Helper()

		k, ok := datapath.KeyOf(reflect.ValueOf(v))
		require.True(t, ok)

		return k
	}

	t.Run("NaN keys share one value", func(t *testing.T) {
		t.Parallel()

		first, second := key(t, math.NaN()), key(t, math.NaN())

		assert.Equal(t, first, second)
		assert.NotEqual(t, first, key(t, 1.0))
	})

	t.Run("times of one instant and zone share one value", func(t *testing.T) {
		t.Parallel()

		a := time.Date(2024, 1, 2, 3, 4, 5, 6, time.FixedZone("x", 3600))
		b := time.Date(2024, 1, 2, 3, 4, 5, 6, time.FixedZone("x", 3600))

		assert.Equal(t, key(t, a), key(t, b))
		assert.NotEqual(t, key(t, a), key(t, a.UTC()))
	})

	t.Run("pointer stands for its value", func(t *testing.T) {
		t.Parallel()

		assert.Equal(t, key(t, keyed{N: 1}), key(t, &keyed{N: 1}))
	})

	t.Run("value that cannot key a map", func(t *testing.T) {
		t.Parallel()

		_, ok := datapath.KeyOf(reflect.ValueOf([]int{1}))
		assert.False(t, ok)
	})
}

func TestMapGroups(t *testing.T) {
	t.Parallel()

	segs := func(groups []datapath.MapGroup) []string {
		var out []string

		for _, g := range groups {
			out = append(out, fmt.Sprintf("%s:%d:%t", g.Seg, len(g.Entries), g.Ambiguous))
		}

		return out
	}

	tcs := map[string]struct {
		value any
		names map[any]string
		want  []string
	}{
		"keys in order": {
			value: map[string]int{"b": 1, "a": 2},
			want:  []string{"a:1:false", "b:1:false"},
		},
		"names spell the keys": {
			value: map[int]int{16: 1, 2: 2},
			names: map[any]string{16: "0x10"},
			want:  []string{"0x10:1:false", "2:1:false"},
		},
		"keys of two types share a segment": {
			value: map[any]int{1: 1, "1": 2},
			want:  []string{"1:1:true", "1:1:true"},
		},
		"keys of one value share a group": {
			value: map[float64]int{math.NaN(): 1, math.NaN(): 2},
			names: map[any]string{},
			want:  []string{"NaN:2:true"},
		},
		"pointer keys name their type": {
			value: map[*[]int]int{{1}: 1, {2}: 2},
			want:  []string{"(*[]int):2:true"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			names := tc.names
			if names == nil {
				names = map[any]string{}
			}

			assert.Equal(t, tc.want, segs(datapath.MapGroups(reflect.ValueOf(tc.value), names)))
		})
	}

	t.Run("keys of one value lose their names", func(t *testing.T) {
		t.Parallel()

		nan, ok := datapath.KeyOf(reflect.ValueOf(math.NaN()))
		require.True(t, ok)

		names := map[any]string{nan: ".nan"}
		value := map[float64]int{math.NaN(): 1, math.NaN(): 2}

		assert.Equal(t, []string{"NaN:2:true"}, segs(datapath.MapGroups(reflect.ValueOf(value), names)))
		assert.Empty(t, names)
	})
}
