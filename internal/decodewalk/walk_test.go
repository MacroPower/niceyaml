package decodewalk_test

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/stretchr/testify/assert"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/astnode"
	"go.jacobcolvin.com/niceyaml/internal/decodewalk"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
)

// rules returns the rules of a walk of doc, for a decoder with opts that
// has read the anchors of doc, as the decode of doc did. A panic of the
// decoder comes back as an error.
func rules(tb testing.TB, doc *niceyaml.Node, opts ...yaml.DecodeOption) decodewalk.Rules {
	tb.Helper()

	dec := yaml.NewDecoder(bytes.NewReader(nil), opts...)

	var sink any

	_ = dec.DecodeFromNode(doc.AST(), &sink) //nolint:errcheck // The walk sees what the decode saw.

	return decodewalk.Rules{
		Resolver: doc.Resolver(),
		View:     func(node ast.Node) ast.Node { return node },
		Decode: func(node ast.Node, v any) (err error) { //nolint:nonamedreturns // The recover sets it.
			// The decoder panics on some nodes, as on an inline map under a
			// decoder that rejects unknown fields.
			defer func() {
				if p := recover(); p != nil {
					err = fmt.Errorf("panic: %v", p)
				}
			}()

			return dec.DecodeFromNode(node, v)
		},
	}
}

var (
	// Kinds spells each [decodewalk.Kind].
	kinds = map[decodewalk.Kind]string{
		decodewalk.Root:    "root",
		decodewalk.Field:   "field",
		decodewalk.Inline:  "inline",
		decodewalk.Element: "element",
		decodewalk.Key:     "key",
		decodewalk.Member:  "member",
	}

	// Reads spells each [decodewalk.Reads].
	reads = map[decodewalk.Reads]string{
		decodewalk.Whole:    "whole",
		decodewalk.Nothing:  "nothing",
		decodewalk.Fields:   "fields",
		decodewalk.Members:  "members",
		decodewalk.Elements: "elements",
	}
)

// walked returns a line for each value a walk of doc as a value of type
// t visits, with the kind, the path, the type, and what it reads, where
// visit decides where the walk goes.
func walked(
	tb testing.TB, doc *niceyaml.Node, t reflect.Type, visit func(*decodewalk.Value) decodewalk.Action,
) []string {
	tb.Helper()

	var lines []string

	decodewalk.Walk(tb.Context(), rules(tb, doc), t, doc.AST(), astnode.Parent{},
		func(v *decodewalk.Value) decodewalk.Action {
			lines = append(lines, fmt.Sprintf("%s %s %s %s", kinds[v.Kind], v.Path(), v.Type, reads[v.Reads]))

			return visit(v)
		})

	return lines
}

// descend has a walk go below every value.
func descend(*decodewalk.Value) decodewalk.Action { return decodewalk.Descend }

type pair struct {
	A int `yaml:"a"`
	B int `yaml:"b"`
}

type withList struct {
	List []pair `yaml:"list"`
	Name string `yaml:"name"`
}

type withMap struct {
	Pairs map[string]pair `yaml:"pairs"`
}

// Inner is a struct that [withInline] holds inline.
type Inner struct {
	C int `yaml:"c"`
}

type withInline struct {
	A     int `yaml:"a"`
	Inner `yaml:",inline"`
}

type withInlineMap struct {
	Rest map[string]int `yaml:",inline"`
	A    int            `yaml:"a"`
}

type withAny struct {
	Any  any           `yaml:"any"`
	Pair pair          `yaml:"pair"`
	Item yaml.MapSlice `yaml:"item"`
}

// Looped holds itself inline.
type Looped struct {
	*Looped `yaml:",inline"`

	A int `yaml:"a"`
}

// pairHolder holds a pair under the key v.
type pairHolder struct {
	V pair `yaml:"v"`
}

// ownHolder holds an [ownMerges] under the key v.
type ownHolder struct {
	V ownMerges `yaml:"v"`
}

// ownMerges reads only its own entries, since its inline field reads the
// anchor of a merge key.
type ownMerges struct {
	*pair `yaml:",omitempty,inline,alias"` //nolint:unused // The decoder reads the anchor into it.

	B int `yaml:"b"`
}

func TestWalk(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		typ   reflect.Type
		visit func(*decodewalk.Value) decodewalk.Action
		input string
		opts  []niceyaml.SourceOption
		want  []string
	}{
		"fields in the order the struct declares them": {
			input: stringtest.LinesLF("b: 1", "a: 2"),
			typ:   reflect.TypeFor[pair](),
			want: []string{
				"root @ decodewalk_test.pair fields",
				"field @.a int whole",
				"field @.b int whole",
			},
		},
		"no null and no missing field": {
			input: "name: ~\n",
			typ:   reflect.TypeFor[withList](),
			want:  []string{"root @ decodewalk_test.withList fields"},
		},
		"the elements of a list": {
			input: "list: [{a: 1}, ~, {b: 2}]\n",
			typ:   reflect.TypeFor[withList](),
			want: []string{
				"root @ decodewalk_test.withList fields",
				"field @.list []decodewalk_test.pair elements",
				"element @.list[0] decodewalk_test.pair fields",
				"field @.list[0].a int whole",
				"element @.list[2] decodewalk_test.pair fields",
				"field @.list[2].b int whole",
			},
		},
		"each key before its value, with merges in place": {
			input: stringtest.LinesLF(
				"base: &base {y: {a: 1}}",
				"pairs: {x: {b: 2}, <<: *base}",
			),
			typ: reflect.TypeFor[withMap](),
			want: []string{
				"root @ decodewalk_test.withMap fields",
				"field @.pairs map[string]decodewalk_test.pair members",
				"key @.pairs.x~ string whole",
				"member @.pairs.x decodewalk_test.pair fields",
				"field @.pairs.x.b int whole",
				"key @.pairs.y~ string whole",
				"member @.pairs.y decodewalk_test.pair fields",
				"field @.pairs.y.a int whole",
			},
		},
		"an inline struct where it stands": {
			input: stringtest.LinesLF("c: 1", "a: 2"),
			typ:   reflect.TypeFor[withInline](),
			want: []string{
				"root @ decodewalk_test.withInline fields",
				"field @.a int whole",
				"inline @ decodewalk_test.Inner fields",
				"field @.c int whole",
			},
		},
		"each entry an inline map reads": {
			input: stringtest.LinesLF("x: 1", "a: 2", "x: 3"),
			opts:  []niceyaml.SourceOption{niceyaml.WithAllowDuplicateKeys(true)},
			typ:   reflect.TypeFor[withInlineMap](),
			want: []string{
				"root @ decodewalk_test.withInlineMap fields",
				"key @.a~ string whole",
				"member @.a int whole",
				"key @.x~ string whole",
				"member @.x int whole",
				"field @.a int whole",
			},
		},
		"nothing below an interface or a map slice": {
			input: stringtest.LinesLF("any: {a: 1}", "item: {a: 1}", "pair: [1]"),
			typ:   reflect.TypeFor[withAny](),
			want: []string{
				"root @ decodewalk_test.withAny fields",
				"field @.any interface {} nothing",
				"field @.pair decodewalk_test.pair whole",
				"field @.item yaml.MapSlice nothing",
			},
		},
		"no field of a mapping with a key of another kind": {
			input: stringtest.LinesLF("a: 1", "2: 3"),
			typ:   reflect.TypeFor[pair](),
			want:  []string{"root @ decodewalk_test.pair fields"},
		},
		"no field of a mapping with a key under an !!int tag": {
			input: stringtest.LinesLF("!!int a: 1", "b: 2"),
			typ:   reflect.TypeFor[pair](),
			want:  []string{"root @ decodewalk_test.pair fields"},
		},
		"the fields of a mapping with a key under a !!str tag": {
			input: stringtest.LinesLF("!!str a: 1", "b: 2"),
			typ:   reflect.TypeFor[pair](),
			want: []string{
				"root @ decodewalk_test.pair fields",
				"field @.a int whole",
				"field @.b int whole",
			},
		},
		"the fields of a mapping with a key under another tag": {
			input: stringtest.LinesLF("!t x: 1", "a: 2"),
			typ:   reflect.TypeFor[pair](),
			want: []string{
				"root @ decodewalk_test.pair fields",
				"field @.a int whole",
			},
		},
		"the fields of a mapping with a key under a !!int tag a directive renames": {
			input: stringtest.LinesLF("%TAG !! tag:example.com,2000:", "---", "!!int a: 1", "b: 2"),
			typ:   reflect.TypeFor[pair](),
			want: []string{
				"root @ decodewalk_test.pair fields",
				"field @.a int whole",
				"field @.b int whole",
			},
		},
		"no field of a mapping with a key of a tag alone": {
			input: stringtest.LinesLF("? !t", ": 1", "a: 2"),
			typ:   reflect.TypeFor[pair](),
			want:  []string{"root @ decodewalk_test.pair fields"},
		},
		"the fields of a mapping with explicit, anchored, and literal keys": {
			input: stringtest.LinesLF("? |", "  x", ": 1", "&k a: 2", "? b", ": 3"),
			typ:   reflect.TypeFor[pair](),
			want: []string{
				"root @ decodewalk_test.pair fields",
				"field @.a int whole",
				"field @.b int whole",
			},
		},
		"the fields of a mapping with an alias to a string key": {
			input: stringtest.LinesLF("x: &k a", "v: {*k : 1, b: 2}"),
			typ:   reflect.TypeFor[pairHolder](),
			want: []string{
				"root @ decodewalk_test.pairHolder fields",
				"field @.v decodewalk_test.pair fields",
				"field @.v.a int whole",
				"field @.v.b int whole",
			},
		},
		"no field of a mapping with an alias to an integer key": {
			input: stringtest.LinesLF("n: &n 1", "v: {*n : 2, a: 3}"),
			typ:   reflect.TypeFor[pairHolder](),
			want: []string{
				"root @ decodewalk_test.pairHolder fields",
				"field @.v decodewalk_test.pair fields",
			},
		},
		// The decoder rejects the alias before it reads the kind of its key.
		"the fields of a mapping with an alias that does not resolve": {
			input: "v: {*missing : 1, a: 2}\n",
			typ:   reflect.TypeFor[pairHolder](),
			want: []string{
				"root @ decodewalk_test.pairHolder fields",
				"field @.v decodewalk_test.pair fields",
				"field @.v.a int whole",
			},
		},
		"no merged entry from a source with a key of another kind": {
			input: stringtest.LinesLF("base: &base {1: x, b: 2}", "v: {<<: *base, a: 1}"),
			typ:   reflect.TypeFor[pairHolder](),
			want: []string{
				"root @ decodewalk_test.pairHolder fields",
				"field @.v decodewalk_test.pair fields",
				"field @.v.a int whole",
			},
		},
		"no field of a mapping with a refused merge": {
			input: stringtest.LinesLF("s: &s 1", "v: {a: 1, <<: *s}"),
			typ:   reflect.TypeFor[pairHolder](),
			want: []string{
				"root @ decodewalk_test.pairHolder fields",
				"field @.v decodewalk_test.pair fields",
			},
		},
		"a merged entry for a struct": {
			input: stringtest.LinesLF("base: &base {b: 1}", "v: {<<: *base}"),
			typ:   reflect.TypeFor[pairHolder](),
			want: []string{
				"root @ decodewalk_test.pairHolder fields",
				"field @.v decodewalk_test.pair fields",
				"field @.v.b int whole",
			},
		},
		"no merged entry for a struct that ignores merges": {
			input: stringtest.LinesLF("base: &base {b: 1}", "v: {<<: *base}"),
			typ:   reflect.TypeFor[ownHolder](),
			want: []string{
				"root @ decodewalk_test.ownHolder fields",
				"field @.v decodewalk_test.ownMerges fields",
			},
		},
		"a node that two aliases reach, below the first": {
			input: "list: [&p {a: 1}, *p]\n",
			typ:   reflect.TypeFor[withList](),
			want: []string{
				"root @ decodewalk_test.withList fields",
				"field @.list []decodewalk_test.pair elements",
				"element @.list[0] decodewalk_test.pair fields",
				"field @.list[0].a int whole",
				"element @.list[1] decodewalk_test.pair fields",
			},
		},
		"a struct that holds itself inline, once": {
			input: "a: 1\n",
			typ:   reflect.TypeFor[Looped](),
			want: []string{
				"root @ decodewalk_test.Looped fields",
				"inline @ decodewalk_test.Looped fields",
				"field @.a int whole",
				"field @.a int whole",
			},
		},
		"nothing below a skipped value": {
			input: stringtest.LinesLF("list: [{a: 1}]", "name: x"),
			typ:   reflect.TypeFor[withList](),
			visit: func(v *decodewalk.Value) decodewalk.Action {
				if v.Kind == decodewalk.Field {
					return decodewalk.Skip
				}

				return decodewalk.Descend
			},
			want: []string{
				"root @ decodewalk_test.withList fields",
				"field @.list []decodewalk_test.pair elements",
				"field @.name string whole",
			},
		},
		"nothing after a stop": {
			input: stringtest.LinesLF("list: [{a: 1}, {a: 2}]", "name: x"),
			typ:   reflect.TypeFor[withList](),
			visit: func(v *decodewalk.Value) decodewalk.Action {
				if v.Kind == decodewalk.Element {
					return decodewalk.Stop
				}

				return decodewalk.Descend
			},
			want: []string{
				"root @ decodewalk_test.withList fields",
				"field @.list []decodewalk_test.pair elements",
				"element @.list[0] decodewalk_test.pair fields",
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			visit := tc.visit
			if visit == nil {
				visit = descend
			}

			doc := yamltest.FirstDocument(t, tc.input, tc.opts...)
			assert.Equal(t, tc.want, walked(t, doc, tc.typ, visit))
		})
	}
}

func TestWalk_Value(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, stringtest.LinesLF("pairs: {x: {a: 1}, x: 2, ? *k : 3}", "k: &k [1]"),
		niceyaml.WithAllowDuplicateKeys(true))

	type holder struct {
		Pairs map[string]any `yaml:"pairs"`
	}

	var got []string

	decodewalk.Walk(t.Context(), rules(t, doc), reflect.TypeFor[holder](), doc.AST(), astnode.Parent{},
		func(v *decodewalk.Value) decodewalk.Action {
			if v.Kind == decodewalk.Key || v.Kind == decodewalk.Member {
				got = append(got, fmt.Sprintf("%s %q depth=%d named=%t selected=%t held=%s",
					kinds[v.Kind], v.Name, v.Depth, v.Named, v.Selected(), strings.TrimSpace(v.Held.String())))
			}

			return decodewalk.Descend
		})

	assert.Equal(t, []string{
		`key "x" depth=2 named=true selected=false held=x`,
		`member "x" depth=2 named=true selected=false held={a: 1}`,
		`key "x" depth=2 named=true selected=true held=x`,
		`member "x" depth=2 named=true selected=true held=2`,
		`key "" depth=2 named=false selected=false held=? *k`,
		`member "" depth=2 named=false selected=false held=3`,
	}, got)
}

func TestWalk_EndedContext(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, "a: 1\n")

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	visited := 0

	decodewalk.Walk(ctx, rules(t, doc), reflect.TypeFor[pair](), doc.AST(), astnode.Parent{},
		func(*decodewalk.Value) decodewalk.Action {
			visited++

			return decodewalk.Descend
		})

	assert.Zero(t, visited)
}
