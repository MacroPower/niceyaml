package decodewalk_test

import (
	"reflect"
	"testing"

	"github.com/goccy/go-yaml"
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
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input)
			assert.Equal(t, tc.want, unknownKeys(t, doc, tc.typ))
		})
	}
}
