package datapath_test

import (
	"fmt"
	"testing"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

// at returns the line and column of tk, counted from 1, or the empty
// string for no token.
func at(tk *token.Token) string {
	if tk == nil || tk.Position == nil {
		return ""
	}

	return fmt.Sprintf("%d:%d", tk.Position.Line, tk.Position.Column)
}

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
			wantStart: "1:7",
		},
		"hexadecimal key": {
			input:     "ports:\n  0x10:\n    name: x\n",
			steps:     []any{"ports", "16", "name"},
			want:      "@.ports.0x10.name",
			wantStart: "3:11",
		},
		"float key with a trailing zero": {
			input:     "python:\n  3.10: a\n",
			steps:     []any{"python", "3.1"},
			want:      "@.python.'3.10'",
			wantStart: "2:9",
		},
		"bool and null keys": {
			input:     "True: a\n~: b\n",
			steps:     []any{"null"},
			want:      "@.'~'",
			wantStart: "2:4",
		},
		"quoted key": {
			input:     "\"a b\": x\n",
			steps:     []any{"a b"},
			want:      "@.'a b'",
			wantStart: "1:8",
		},
		"later key sets the member again": {
			input:     "m:\n  16: aaaa\n  0x10: b\n",
			steps:     []any{"m", "16"},
			want:      "@.m.0x10",
			wantStart: "3:9",
		},
		"member of a merge source": {
			input:     "base: &base\n  0x10: x\nm:\n  <<: *base\n",
			steps:     []any{"m", "16"},
			want:      "@.m.0x10",
			wantStart: "2:9",
		},
		"later merge source wins": {
			input:     "b0: &b0 {True: xxxx}\nb1: &b1 {<<: *b0, true: aaaa, True: b}\nm: {<<: *b1}\n",
			steps:     []any{"m", "true"},
			want:      "@.m.True",
			wantStart: "2:37",
		},
		"member behind an alias": {
			input:     "base: &base\n  0x10: x\nal: *base\n",
			steps:     []any{"al", "16"},
			want:      "@.al.0x10",
			wantStart: "2:9",
		},
		"alias key": {
			input:     "k: &k 0x10\nm:\n  *k : v\n",
			steps:     []any{"m", "16"},
			want:      "@.m.0x10",
			wantStart: "3:8",
		},
		"spelling a later key repeats": {
			input:     "user:\n  <<: {0x10: x}\n  \"0x10\": 1\n",
			steps:     []any{"user", "16"},
			want:      "@.user.16",
			wantStart: "2:14",
			wantToken: "2:14",
			unspelled: true,
		},
		"names below an unspelled name stay decoded": {
			input:     "user:\n  <<: {0x10: {0x20: x}}\n  \"0x10\": 1\n",
			steps:     []any{"user", "16", "32"},
			want:      "@.user.16.32",
			wantStart: "2:21",
			wantToken: "2:21",
			unspelled: true,
		},
		"member the mapping lacks": {
			input:     "ports:\n  0x10: x\n",
			steps:     []any{"ports", "0x10"},
			want:      "@.ports.0x10",
			unspelled: true,
		},
		"member before a key with no name": {
			input:     "k: &k {x: y}\nm:\n  a: 1\n  *k : 2\n  b: 3\n",
			steps:     []any{"m", "a"},
			want:      "@.m.a",
			unspelled: true,
		},
		"member after a key with no name": {
			input:     "k: &k {x: y}\nm:\n  a: 1\n  *k : 2\n  b: 3\n",
			steps:     []any{"m", "b"},
			want:      "@.m.b",
			wantStart: "5:6",
		},
		"member of an element": {
			input:     "items:\n  - name: x\n  - 0x10: y\n",
			steps:     []any{"items", 1, "16"},
			want:      "@.items[1].0x10",
			wantStart: "3:11",
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

			doc := yamltest.FirstDocument(t, tc.input)
			got := walk(t, datapath.NewIndex(doc.Resolver()), doc.AST(), tc.steps...)

			assert.Equal(t, tc.want, got.Path.String())
			assert.Equal(t, tc.unspelled, got.Unspelled)
			assert.Equal(t, tc.wantStart, at(got.Start(false)))
			assert.Equal(t, tc.wantToken, at(got.Token(false)))
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
			input:     "items:\n  - name: x\n  - name: y\n",
			steps:     []any{"items", 1},
			want:      "@.items[1]",
			wantStart: "3:5",
		},
		"element behind an alias": {
			input:     "base: &base [a, b]\nal: *base\n",
			steps:     []any{"al", 1},
			want:      "@.al[1]",
			wantStart: "1:17",
		},
		"root sequence": {
			input:     "- x\n",
			steps:     []any{0},
			want:      "@[0]",
			wantStart: "1:3",
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

			doc := yamltest.FirstDocument(t, tc.input)
			got := walk(t, datapath.NewIndex(doc.Resolver()), doc.AST(), tc.steps...)

			assert.Equal(t, tc.want, got.Path.String())
			assert.False(t, got.Unspelled)
			assert.Nil(t, got.Entry)
			assert.Equal(t, tc.wantStart, at(got.Start(false)))
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
			input:   "ports:\n  0x10: x\n",
			steps:   []any{"ports", "16"},
			want:    "2:9",
			wantKey: "2:3",
		},
		"explicit key": {
			input:   "? 0x10\n: x\n",
			steps:   []any{"16"},
			want:    "2:3",
			wantKey: "1:3",
		},
		"element has no key": {
			input:   "- x\n",
			steps:   []any{0},
			want:    "1:3",
			wantKey: "1:3",
		},
		"root has no key": {
			input:   "a: x\n",
			want:    "1:1",
			wantKey: "1:1",
		},
		"member the mapping lacks": {
			input: "a: x\n",
			steps: []any{"b"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input)
			got := walk(t, datapath.NewIndex(doc.Resolver()), doc.AST(), tc.steps...)

			assert.Equal(t, tc.want, at(got.Start(false)))
			assert.Equal(t, tc.wantKey, at(got.Start(true)))
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
			input: "ports:\n  0x10: x\n",
			steps: []any{"ports", "16"},
		},
		"path selects another entry": {
			input:   "user:\n  <<: {0x10: x}\n  \"0x10\": 1\n",
			steps:   []any{"user", "16"},
			want:    "2:14",
			wantKey: "2:8",
		},
		"walk reached no node": {
			input: "user:\n  <<: {0x10: x}\n  \"0x10\": 1\n",
			steps: []any{"user", "16", "name"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input)
			got := walk(t, datapath.NewIndex(doc.Resolver()), doc.AST(), tc.steps...)

			assert.Equal(t, tc.want, at(got.Token(false)))
			assert.Equal(t, tc.wantKey, at(got.Token(true)))
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

			doc := yamltest.FirstDocument(t, tc.input)
			idx := datapath.NewIndex(doc.Resolver())

			got := idx.Deref(walk(t, idx, doc.AST(), tc.steps...).Node)

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

		doc := yamltest.FirstDocument(t, "a: *missing\n")
		idx := datapath.NewIndex(doc.Resolver())

		assert.Nil(t, idx.Deref(walk(t, idx, doc.AST(), "a").Node))
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

			doc := yamltest.FirstDocument(t, tc.input)

			got := datapath.NewIndex(doc.Resolver()).MemberNode(doc.AST(), tc.name)
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
			input: "python:\n  3.10: a\n",
			steps: []any{"python"},
			name:  "3.10",
			want:  true,
		},
		"decoded name of a respelled key": {
			input: "python:\n  3.10: a\n",
			steps: []any{"python"},
			name:  "3.1",
		},
		"key of a merge source": {
			input: "m:\n  <<: {0x10: x}\n",
			steps: []any{"m"},
			name:  "0x10",
			want:  true,
		},
		"mapping behind an alias": {
			input: "base: &base {a: b}\nal: *base\n",
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

			doc := yamltest.FirstDocument(t, tc.input)
			idx := datapath.NewIndex(doc.Resolver())

			got := idx.SelectsEntry(walk(t, idx, doc.AST(), tc.steps...).Node, tc.name)

			assert.Equal(t, tc.want, got)
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
