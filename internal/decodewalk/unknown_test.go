package decodewalk_test

import (
	"reflect"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/stretchr/testify/assert"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/decodewalk"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
)

// unknownKeys returns the key of each unknown field a decode of doc as a
// value of type t rejects, for a decoder that rejects unknown fields.
func unknownKeys(tb testing.TB, doc *niceyaml.Node, t reflect.Type) []string {
	tb.Helper()

	r := rules(tb, doc, yaml.DisallowUnknownField())

	var keys []string

	for _, field := range decodewalk.UnknownFields(tb.Context(), r, t, doc.AST()) {
		keys = append(keys, yamltest.TokenPlace(field.Token))
	}

	return keys
}

// plainSelf decodes itself through a second type with the same fields,
// so the decoder checks those fields.
type plainSelf struct {
	In pair `yaml:"in"`
	X  int  `yaml:"x"`
}

func (s *plainSelf) UnmarshalYAML(unmarshal func(any) error) error {
	type plain plainSelf

	return unmarshal((*plain)(s))
}

// acceptsAll decodes itself and accepts every key.
type acceptsAll struct {
	In pair `yaml:"in"`
}

func (*acceptsAll) UnmarshalYAML(func(any) error) error { return nil }

// pickedSelf decodes itself into the type that its kind key picks. A
// mapping of the kind a decodes into a type with the field x, and any
// other mapping into one with the field y.
type pickedSelf struct {
	X int
	Y int
}

func (s *pickedSelf) UnmarshalYAML(unmarshal func(any) error) error {
	var fields map[string]any

	err := unmarshal(&fields)
	if err != nil {
		return err
	}

	if fields["kind"] == "a" {
		var a struct {
			Kind string `yaml:"kind"`
			X    int    `yaml:"x"`
		}

		err = unmarshal(&a)
		s.X = a.X

		return err
	}

	var b struct {
		Kind string `yaml:"kind"`
		Y    int    `yaml:"y"`
	}

	err = unmarshal(&b)
	s.Y = b.Y

	return err
}

// pickedHolder holds a [pickedSelf].
type pickedHolder struct {
	Self pickedSelf `yaml:"self"`
}

// mergeHolder holds a map of pairs beside the anchor its entries merge.
type mergeHolder struct {
	Base  any             `yaml:"base"`
	Pairs map[string]pair `yaml:"pairs"`
}

// inlineMapHolder holds a map of pairs inline.
type inlineMapHolder struct {
	Rest map[string]pair `yaml:",inline"`
	A    int             `yaml:"a"`
}

// selfHolder holds a [plainSelf].
type selfHolder struct {
	Self plainSelf `yaml:"self"`
}

// acceptsHolder holds an [acceptsAll].
type acceptsHolder struct {
	Self acceptsAll `yaml:"self"`
}

// selfListHolder holds a [plainSelf] and a list of them.
type selfListHolder struct {
	Self plainSelf   `yaml:"self"`
	List []plainSelf `yaml:"list"`
}

func TestUnknownFields(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		typ   reflect.Type
		input string
		want  []string
	}{
		"each unknown key, in the order of the source": {
			input: stringtest.LinesLF("z: 1", "a: 2", "y: 3"),
			typ:   reflect.TypeFor[pair](),
			want:  []string{"z@1:1", "y@3:1"},
		},
		"no field of an inline struct": {
			input: stringtest.LinesLF("c: 1", "a: 2"),
			typ:   reflect.TypeFor[withInline](),
		},
		"a key below a list": {
			input: "list: [{a: 1}, {q: 2}]\n",
			typ:   reflect.TypeFor[withList](),
			want:  []string{"q@1:17"},
		},
		"a key a merge brings in": {
			input: stringtest.LinesLF(
				"base: &base {q: 1}",
				"pairs: {x: {<<: *base}}",
			),
			typ:  reflect.TypeFor[mergeHolder](),
			want: []string{"q@1:14"},
		},
		"no key under an inline map": {
			input: stringtest.LinesLF("x: {q: 1}", "a: 1"),
			typ:   reflect.TypeFor[inlineMapHolder](),
		},
		"a key a struct that decodes itself rejects": {
			input: "self: {in: {q: 1}, r: 2}\n",
			typ:   reflect.TypeFor[selfHolder](),
			want:  []string{"q@1:13", "r@1:20"},
		},
		"no key a struct that decodes itself accepts": {
			input: "self: {in: {q: 1}, r: 2}\n",
			typ:   reflect.TypeFor[acceptsHolder](),
		},
		"no key a struct that picks its type accepts": {
			// The decoder accepts the whole struct, so the search does not
			// decode x without the kind that picks the type with x.
			input: "self: {kind: a, x: 1}\n",
			typ:   reflect.TypeFor[pickedHolder](),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input)
			assert.Equal(t, tc.want, unknownKeys(t, doc, tc.typ))
		})
	}
}

func TestUnknownFields_Aliases(t *testing.T) {
	t.Parallel()

	// The search decodes the content of the anchor once for all its
	// aliases, as it reads the keys below it once.
	tcs := map[string]struct {
		input string
		want  []string
		// The decodes that the search runs.
		decodes int
	}{
		"an anchor that the decoder accepts": {
			// The one decode of the whole struct.
			input:   stringtest.LinesLF("self: &s {in: {a: 1}, x: 1}", "list: [*s, *s, *s]"),
			decodes: 1,
		},
		"an anchor with an unknown key": {
			// The decode of the whole struct, and one decode to confirm
			// each candidate: in, x, and q.
			input:   stringtest.LinesLF("self: &s {in: {q: 1}, x: 1}", "list: [*s, *s, *s]"),
			want:    []string{"q@1:16"},
			decodes: 4,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input)
			r := rules(t, doc, yaml.DisallowUnknownField())
			decode := r.Decode
			decodes := 0
			r.Decode = func(node ast.Node, v any) error {
				decodes++

				return decode(node, v)
			}

			var keys []string

			for _, field := range decodewalk.UnknownFields(t.Context(), r, reflect.TypeFor[selfListHolder](), doc.AST()) {
				keys = append(keys, yamltest.TokenPlace(field.Token))
			}

			assert.Equal(t, tc.want, keys)
			assert.Equal(t, tc.decodes, decodes)
		})
	}
}

func TestUnknownFields_Merges(t *testing.T) {
	t.Parallel()

	const reference = "ref: &ref {a: 1, z: 3}\n"

	tcs := map[string]struct {
		input string
		// The place of the key of each unknown field.
		want []string
	}{
		// The search does not report a key that a merge key brings in from a
		// reference document, such as the z of ref.
		"entry before a merge key of a reference document": {
			input: stringtest.LinesLF("x: 1", "<<: *ref", "y: 2"),
			want:  []string{"x@1:1", "y@3:1"},
		},
		// The decoder reads the z of the merge key, not the z of the mapping.
		"entry that a merge key of a reference document replaces": {
			input: stringtest.LinesLF("z: 1", "<<: *ref"),
		},
		"entry after a merge key of a reference document": {
			input: stringtest.LinesLF("<<: *ref", "z: 1"),
			want:  []string{"z@2:1"},
		},
		"entry before a merge key of the document": {
			input: stringtest.LinesLF("base: &base {a: 2}", "v:", "  x: 1", "  <<: *base"),
			want:  []string{"base@1:1", "x@3:3"},
		},
		"entry of a nested mapping": {
			input: "v: {x: 1, <<: *ref}\n",
			want:  []string{"x@1:5"},
		},
		"merge key of the document that reads a reference document": {
			input: stringtest.LinesLF("base: &base {<<: *ref}", "v:", "  z: 1", "  <<: *base"),
			want:  []string{"base@1:1"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input, yamltest.References(reference))
			r := referenceRules(t, doc, yamltest.FirstDocument(t, reference), yaml.DisallowUnknownField())

			type holder struct {
				V pair `yaml:"v"`
				A int  `yaml:"a"`
			}

			var got []string

			for _, field := range decodewalk.UnknownFields(t.Context(), r, reflect.TypeFor[holder](), doc.AST()) {
				got = append(got, yamltest.TokenPlace(field.Token))
			}

			assert.Equal(t, tc.want, got)
		})
	}
}
