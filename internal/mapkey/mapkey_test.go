package mapkey_test

import (
	"testing"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/hook"
	"go.jacobcolvin.com/niceyaml/internal/mapkey"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
)

// newDocument returns the first document of input, with the reference
// document ref when ref is not empty.
func newDocument(t *testing.T, input, ref string) *niceyaml.Node {
	t.Helper()

	var opts []niceyaml.SourceOption

	if ref != "" {
		opts = append(opts, yamltest.References(ref))
	}

	return yamltest.FirstDocument(t, input, opts...)
}

// keyEntries returns the entries of body other than its merge keys, in the
// order of the text.
func keyEntries(body ast.Node) []*ast.MappingValueNode {
	var own []*ast.MappingValueNode

	astnode.Inspect(body, func(n ast.Node) bool {
		if e, ok := n.(*ast.MappingValueNode); ok && !astnode.IsMergeKey(e.Key) {
			own = append(own, e)
		}

		return true
	})

	return own
}

// requireDecodedKeys checks that niceyaml's decode of doc holds each key
// that r names, in the mapping that holds the key. It walks the mappings
// and the sequences that the text writes, and not those that an alias
// reads.
func requireDecodedKeys(t *testing.T, doc *niceyaml.Node, r *mapkey.Keys) {
	t.Helper()

	v, err := doc.Decode[any](t.Context())
	require.NoError(t, err)

	var walk func(node ast.Node, v any)

	walk = func(node ast.Node, v any) {
		switch n := astnode.Content(node).(type) {
		case *ast.MappingNode, *ast.MappingValueNode:
			m, ok := v.(map[string]any)
			require.True(t, ok, "%s decodes to %T", node, v)

			entries, _ := astnode.Entries(n)
			for _, e := range entries {
				if astnode.IsMergeKey(e.Key) {
					continue
				}

				key, ok := r.Key(e.Key)
				require.True(t, ok)
				require.Contains(t, m, key)

				walk(e.Value, m[key])
			}

		case *ast.SequenceNode:
			items, ok := v.([]any)
			require.True(t, ok, "%s decodes to %T", node, v)
			require.Len(t, items, len(n.Values))

			for i, item := range n.Values {
				walk(item, items[i])
			}
		}
	}

	walk(doc.AST(), v)
}

func TestKeys_Key(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		ref   string
		// The key of each entry, in the order of the text, and "!" for a
		// key that does not decode.
		want []string
	}{
		"string":                   {input: "a: 1\n", want: []string{"a"}},
		"quoted number":            {input: `"16": 1` + "\n", want: []string{"16"}},
		"hex integer":              {input: "0x10: 1\n", want: []string{"16"}},
		"float":                    {input: "1.0: 1\n", want: []string{"1"}},
		"boolean":                  {input: "True: 1\n", want: []string{"true"}},
		"null":                     {input: "~: 1\n", want: []string{"null"}},
		"string tag over a number": {input: "!!str 16: 1\n", want: []string{"16"}},
		"integer tag over a word":  {input: `!!int "17": 1` + "\n", want: []string{"17"}},
		"boolean tag over a word":  {input: "!!bool x: 1\n", want: []string{"!"}},
		"local tag":                {input: "!t 0x10: 1\n", want: []string{"0x10"}},
		"explicit key":             {input: "? 0x10\n: 1\n", want: []string{"16"}},
		"anchored key":             {input: "&k 0o20: 1\n", want: []string{"16"}},
		"alias of a key":           {input: "&k 0x10: 0\n*k : 1\n", want: []string{"16", "16"}},
		"tagged alias of a key":    {input: "&k 0x10: 0\n!!str *k : 1\n", want: []string{"16", "16"}},
		"alias of a sequence":      {input: "a: &k [1, 2]\n*k : 1\n", want: []string{"a", "[1 2]"}},
		"alias of a mapping":       {input: "a: &k {p: 1}\n*k : 1\n", want: []string{"a", "p", "map[p:1]"}},
		"integer tag over a mapping": {
			input: "a: &k {p: 1}\nm: {!!int *k : x}\n", want: []string{"a", "p", "m", "0"},
		},
		"null tag over a mapping": {
			input: "a: &k {p: 1}\nm: {!!null *k : x}\n", want: []string{"a", "p", "m", "null"},
		},
		"alias of a mapping that merges": {
			input: "b: &b {p: 1, q: 2}\na: &k {q: 3, <<: *b}\n*k : 1\n",
			want:  []string{"b", "p", "q", "a", "q", "map[p:1 q:2]"},
		},
		"alias inside its anchor": {
			input: "b: &b {*b : 1}\n", want: []string{"b", "null"},
		},
		"alias inside an anchor of its name, after an inner anchor of the name": {
			input: "x: &b {y: &b 1, *b : 2}\n", want: []string{"x", "y", "1"},
		},
		"alias after an anchor that holds an anchor of its name": {
			input: "a: &x {a: &x 1}\nb: {*x : 1}\n", want: []string{"a", "a", "b", "map[a:1]"},
		},
		"alias of an earlier anchor of its name": {
			input: "a: &x 1\nb: {*x : 1}\nc: &x 2\n", want: []string{"a", "b", "1", "c"},
		},
		"alias after an anchor on a merge value": {
			input: "a: &x 1\nm:\n  <<: &x {p: 1}\n  ? *x\n  : v\n  q: w\n", want: []string{"a", "m", "p", "1", "q"},
		},
		"alias of a sequence whose alias reads an earlier anchor": {
			input: "a: &a 1\nc: &c [*a, &a 2]\nm:\n  ? *c\n  : v\n  q: w\n",
			want:  []string{"a", "c", "m", "[1 2]", "q"},
		},
		"alias of an anchor that a merge key reads again": {
			input: "k3: &c {1.0: &c 1}\nm2:\n  <<: *c\n? *c\n: v\nq: w\n", want: []string{"k3", "1", "m2", "1", "q"},
		},
		"alias without an anchor": {input: "*k : 1\n", want: []string{"!"}},
		"alias of a reference document": {
			input: "*r : 1\n", ref: "a: &r {p: 1.0}\n", want: []string{"map[p:1]"},
		},
		"mapping with an alias of a reference document": {
			input: "c: &k {true: *r}\n*k : y\n", ref: "s: &r 1.0\n", want: []string{"c", "true", "map[true:1]"},
		},
		"anchor of the document before an alias": {
			input: "a: &r 2\n*r : 1\n", ref: "s: &r 1\n", want: []string{"a", "2"},
		},
		"string with a line break": {input: `"a\nb": 1` + "\n", want: []string{"a\nb"}},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := newDocument(t, tc.input, tc.ref)
			r := hook.Keys(doc)
			own := keyEntries(doc.AST())

			got := make([]string, 0, len(own))
			decodes := true

			for _, e := range own {
				key, ok := r.Key(e.Key)
				if !ok {
					key, decodes = "!", false
				}

				got = append(got, key)
			}

			assert.Equal(t, tc.want, got)

			if decodes {
				requireDecodedKeys(t, doc, r)
			}
		})
	}
}

func TestKeys_Share(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		// The documents whose Keys share one Plain, which name their keys in
		// order.
		inputs []string
		// The keys of the entries of each document, in the order of the
		// text.
		want [][]string
	}{
		"keys of both texts": {
			inputs: []string{"a: 1\n0x10: 2\n", "0x10: 3\nb: 4\na: 5\n"},
			want:   [][]string{{"a", "16"}, {"16", "b", "a"}},
		},
		"text under another tag": {
			inputs: []string{"~: 1\n", "!!str ~: 2\n"},
			want:   [][]string{{"null"}, {""}},
		},
		"text of another kind": {
			inputs: []string{"1.0: 1\n", "'1.0': 2\n"},
			want:   [][]string{{"1"}, {"1.0"}},
		},
		"tag of a directive": {
			inputs: []string{
				"%TAG !e! tag:yaml.org,2002:\n---\n!e!str 0x10: 1\n",
				"%TAG !e! tag:yaml.org,2002:\n---\n!e!str 0x10: 2\n",
			},
			want: [][]string{{"0x10"}, {"0x10"}},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			shared := mapkey.NewPlain()

			for i, input := range tc.inputs {
				doc := newDocument(t, input, "")
				r := hook.Keys(doc)
				r.Share(shared)

				var got []string

				for _, e := range keyEntries(doc.AST()) {
					key, ok := r.Key(e.Key)
					require.True(t, ok)

					got = append(got, key)
				}

				assert.Equal(t, tc.want[i], got)
				requireDecodedKeys(t, doc, r)
			}
		})
	}
}

func TestPlain_Value(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		// The documents whose values one Plain decodes, in order.
		inputs []string
		// The values of the entries of each document, in the order of the
		// text.
		want [][]any
		// Whether the Plain is nil.
		none bool
	}{
		"values of both texts": {
			inputs: []string{"a: 0x10\nb: x\n", "c: 0x10\nd: '0x10'\ne: x\n"},
			want:   [][]any{{uint64(16), "x"}, {uint64(16), "0x10", "x"}},
		},
		"text under another tag": {
			inputs: []string{"a: ~\n", "b: !!str ~\n"},
			want:   [][]any{{nil}, {""}},
		},
		"block scalar and string": {
			inputs: []string{"a: |\n  x\n", "b: \"x\\n\"\n"},
			want:   [][]any{{"x\n"}, {"x\n"}},
		},
		"tag of a directive": {
			inputs: []string{
				"%TAG !e! tag:yaml.org,2002:\n---\na: !e!str 0x10\n",
				"%TAG !e! tag:yaml.org,2002:\n---\nb: !e!str 0x10\n",
			},
			want: [][]any{{"0x10"}, {"0x10"}},
		},
		"nil Plain": {
			inputs: []string{"a: 0x10\n", "b: 0x10\n"},
			want:   [][]any{{uint64(16)}, {uint64(16)}},
			none:   true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			shared := mapkey.NewPlain()
			if tc.none {
				shared = nil
			}

			for i, input := range tc.inputs {
				doc := newDocument(t, input, "")

				var got []any

				for _, e := range keyEntries(doc.AST()) {
					v, err := shared.Value(e.Value)
					require.NoError(t, err)

					var want any

					require.NoError(t, astnode.NodeToValue(e.Value, &want))
					assert.Equal(t, want, v)

					got = append(got, v)
				}

				assert.Equal(t, tc.want[i], got)
			}
		})
	}
}

func TestKeys_KeyMerged(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		// The text of a reference document of the document, or "".
		ref string
		// The root key of the mapping of the last merge key of the text.
		in string
		// The keys of the entries that the last merge key of the text brings
		// in, in the order of the text.
		want []string
	}{
		"plain keys": {
			input: "a: &a {x: 1, 0x10: 2}\nm: {<<: *a}\n", in: "m", want: []string{"x", "16"},
		},
		"alias inside its anchor": {
			input: "b: &b {*b : 1}\nm: {<<: *b, x: 2}\n", in: "m", want: []string{"null"},
		},
		"anchor of its name after the source": {
			input: "a: &x 1\nb: &m {*x : 1}\nc: &x 2\nd: {<<: *m}\n", in: "d", want: []string{"1"},
		},
		"anchor of a collection after the source": {
			input: "m: &m {a: 1}\nn: &n {*m : 2}\nm2: &m {b: 1}\np: {<<: *n}\n", in: "p", want: []string{"map[a:1]"},
		},
		"anchor inside the inline mapping of the merge key": {
			input: "<<: {k: &a p, *a : x}\np: y\n", in: "", want: []string{"k", "p"},
		},
		// An alias of a reference document reads the anchors of the
		// reference documents alone, even one after it.
		"alias key of a reference document that reads an anchor before it": {
			input: "x: &x d\nm: {<<: *r}\n", ref: "x: &x k\nr: &r {*x : 1}\n", in: "m", want: []string{"k"},
		},
		"alias key of a reference document that reads an anchor after it": {
			input: "x: &x d\nm: {<<: *r}\n", ref: "r: &r {*x : 1}\nx: &x k\n", in: "m", want: []string{"k"},
		},
		"alias key of a reference document with a comment after it": {
			input: "m: {<<: *r}\n", ref: "r: &r {? *x # c\n  : 1}\nx: &x k\n", in: "m", want: []string{"k"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := newDocument(t, tc.input, tc.ref)
			r := hook.Keys(doc)

			var merges []*ast.MappingValueNode

			astnode.Inspect(doc.AST(), func(n ast.Node) bool {
				if e, ok := n.(*ast.MappingValueNode); ok && astnode.IsMergeKey(e.Key) {
					merges = append(merges, e)
				}

				return true
			})

			require.NotEmpty(t, merges)

			site := merges[len(merges)-1]

			source := site.Value
			if alias, ok := site.Value.(*ast.AliasNode); ok {
				var err error

				source, err = hook.AliasContent(doc, alias)
				require.NoError(t, err)
			}

			entries, ok := astnode.Entries(source)
			require.True(t, ok)

			got := make([]string, 0, len(entries))

			for _, e := range entries {
				key, ok := r.Key(e.Key)
				require.True(t, ok)

				got = append(got, key)
			}

			assert.Equal(t, tc.want, got)

			// The decode of the document names the entries of the merge key by
			// the same keys.
			v, err := doc.Decode[map[string]any](t.Context())
			require.NoError(t, err)

			in := v
			if tc.in != "" {
				in, ok = v[tc.in].(map[string]any)
				require.True(t, ok)
			}

			for _, key := range tc.want {
				assert.Contains(t, in, key)
			}
		})
	}
}

func TestKeys_KeyOf(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		// The YAML of the key, whose aliases read the anchors inside it and
		// those of the reference document ref.
		key  string
		ref  string
		want string
	}{
		"mapping": {key: "{p: 2, q: 3}", want: "map[p:2 q:3]"},
		"merge":   {key: "{<<: {p: 1, q: 2}, q: 5}", want: "map[p:1 q:5]"},
		"alias of an anchor inside": {
			key: "[&a 1, *a]", want: "[1 1]",
		},
		"tagged alias of an anchor inside": {
			key: "[&a {p: 1}, !!null *a]", want: "[map[p:1] <nil>]",
		},
		"alias of a reference document": {
			key: "{a: *r}", ref: "s: &r 0x10\n", want: "map[a:16]",
		},
		"scalar": {key: "b", want: "b"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := newDocument(t, "a: 1\n", tc.ref)
			r := hook.Keys(doc)

			file, err := parser.ParseBytes([]byte(tc.key), 0)
			require.NoError(t, err)

			got, ok := r.KeyOf(file.Docs[0].Body)
			require.True(t, ok)
			assert.Equal(t, tc.want, got)
			assert.Nil(t, r.Panic())
		})
	}
}

// parsedBody returns the body of the first document of text.
func parsedBody(t *testing.T, text string) ast.Node {
	t.Helper()

	file, err := parser.ParseBytes([]byte(text), 0)
	require.NoError(t, err)

	return file.Docs[0].Body
}

func TestStringOf(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		node string
		want string
		// Whether the decoder reads a string, and whether it fails.
		ok, err bool
	}{
		"plain text":        {node: "cafe", want: "cafe", ok: true},
		"number":            {node: "2", want: "2", ok: true},
		"null":              {node: "~"},
		"text under !!null": {node: "!!null cafe"},
		"text under !!int":  {node: "!!int cafe", want: "0", ok: true},
		"text under !!str":  {node: "!!str 2", want: "2", ok: true},
		"timestamp":         {node: "!!timestamp cafe", err: true},
		"mapping":           {node: "{a: 1}", err: true},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, ok, err := mapkey.StringOf(parsedBody(t, tc.node))
			if tc.err {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestName(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		value string
		want  string
	}{
		"mapping":  {value: "{p: 1}", want: "map[p:1]"},
		"sequence": {value: "[1, 2]", want: "[1 2]"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// The key is a sequence of the value and an alias of it. The value
			// takes an anchor of the name, which no text spells, and the alias
			// after it reads that anchor.
			key, ok := parsedBody(t, "["+tc.value+", null]").(*ast.SequenceNode)
			require.True(t, ok)

			key.Values[0] = mapkey.NewAnchor(mapkey.Name(1), key.Values[0])
			key.Values[1] = mapkey.NewAlias(mapkey.Name(1))

			got, ok := mapkey.NewKeys(nil, nil, nil).KeyOf(key)
			require.True(t, ok)
			assert.Equal(t, "["+tc.want+" "+tc.want+"]", got)
			assert.NotEqual(t, mapkey.Name(1), mapkey.Name(2))
		})
	}
}
