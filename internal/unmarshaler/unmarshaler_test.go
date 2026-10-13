package unmarshaler_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/internal/unmarshaler"
)

// textValue decodes itself from text.
type textValue struct{}

func (*textValue) UnmarshalText([]byte) error { return nil }

// nodeValue decodes itself from its node.
type nodeValue struct{}

func (*nodeValue) UnmarshalYAML(ast.Node) error { return nil }

// jsonValue decodes itself from JSON alone.
type jsonValue struct{}

func (*jsonValue) UnmarshalJSON([]byte) error { return nil }

// textNodeValue decodes itself from text and from its node. Go-yaml
// calls its UnmarshalYAML first.
type textNodeValue struct{}

func (*textNodeValue) UnmarshalText([]byte) error { return nil }

func (*textNodeValue) UnmarshalYAML(ast.Node) error { return nil }

// plainValue has no method.
type plainValue struct {
	Name string `yaml:"name"`
}

// embedsNode gets its UnmarshalYAML from the field it embeds.
type embedsNode struct {
	Name string `yaml:"name"`

	nodeValue
}

// embedsJSON gets its UnmarshalJSON from the field it embeds.
type embedsJSON struct {
	*jsonValue

	Name string `yaml:"name"`
}

// checker declares the one method of [checks].
type checker struct{}

func (checker) Check() error { return nil }

// checks is an interface of one method.
type checks interface{ Check() error }

// embedsChecker gets its Check from the field it embeds.
type embedsChecker struct {
	checker
}

// inlineNode holds a field that decodes itself inline.
type inlineNode struct {
	Value nodeValue `yaml:",inline"`
	Name  string    `yaml:"name"`
}

// inlineDeep holds a struct inline that holds an inline field that
// decodes itself.
type inlineDeep struct {
	Inner inlineNode `yaml:",inline"`
}

// inlinePlain holds a struct inline that decodes by its fields.
type inlinePlain struct {
	Inner plainValue `yaml:",inline"`
}

// inlineSelf holds a pointer to itself inline.
type inlineSelf struct {
	Self *inlineSelf `yaml:",inline"`
}

// inlineCustom holds a plain struct inline, which a function of a decode
// can decode.
type inlineCustom struct {
	Inner plainValue `yaml:",inline"`
	Name  string     `yaml:"name"`
}

// noOption is the go-yaml option of a function no test calls.
func noOption(unmarshaler.ValueDecoder) yaml.DecodeOption { return nil }

func TestBase(t *testing.T) {
	t.Parallel()

	assert.Equal(t, reflect.TypeFor[int](), unmarshaler.Base(reflect.TypeFor[**int]()))
	assert.Equal(t, reflect.TypeFor[[]*int](), unmarshaler.Base(reflect.TypeFor[[]*int]()))
}

func TestDecodesItself(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		typ  reflect.Type
		want bool
	}{
		"text unmarshaler":        {typ: reflect.TypeFor[textValue](), want: true},
		"node unmarshaler":        {typ: reflect.TypeFor[nodeValue](), want: true},
		"embedded unmarshaler":    {typ: reflect.TypeFor[embedsNode](), want: true},
		"json alone":              {typ: reflect.TypeFor[jsonValue](), want: false},
		"plain struct":            {typ: reflect.TypeFor[plainValue](), want: false},
		"syntax node":             {typ: reflect.TypeFor[ast.StringNode](), want: true},
		"time reads from text":    {typ: reflect.TypeFor[time.Time](), want: true},
		"duration has no methods": {typ: reflect.TypeFor[time.Duration](), want: false},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, unmarshaler.DecodesItself(tc.typ))
			assert.Equal(t, tc.want, unmarshaler.DecodesItself(tc.typ), "a second call gives the same answer")
		})
	}
}

func TestIsSyntaxNode(t *testing.T) {
	t.Parallel()

	assert.True(t, unmarshaler.IsSyntaxNode(reflect.TypeFor[ast.MappingNode]()))
	assert.False(t, unmarshaler.IsSyntaxNode(reflect.TypeFor[*ast.MappingNode]()))
	assert.False(t, unmarshaler.IsSyntaxNode(reflect.TypeFor[struct{ ast.StringNode }]()))
	assert.False(t, unmarshaler.IsSyntaxNode(reflect.TypeFor[plainValue]()))
}

func TestDeclares(t *testing.T) {
	t.Parallel()

	iface := reflect.TypeFor[checks]()

	assert.True(t, unmarshaler.Declares(reflect.TypeFor[checker](), iface))
	assert.False(t, unmarshaler.Declares(reflect.TypeFor[embedsChecker](), iface))
	assert.False(t, unmarshaler.Declares(reflect.TypeFor[plainValue](), iface))
}

func TestTypeCache(t *testing.T) {
	t.Parallel()

	var (
		cache unmarshaler.TypeCache[int]
		calls int
	)

	compute := func(reflect.Type) int {
		calls++

		return calls
	}

	assert.Equal(t, 1, cache.Get(reflect.TypeFor[int](), compute))
	assert.Equal(t, 1, cache.Get(reflect.TypeFor[int](), compute))
	assert.Equal(t, 2, cache.Get(reflect.TypeFor[string](), compute))
	assert.Equal(t, 2, calls)
}

func TestOptions(t *testing.T) {
	t.Parallel()

	plain := reflect.TypeFor[plainValue]()
	custom := unmarshaler.Options{}.With(plain, noOption)

	t.Run("empty", func(t *testing.T) {
		t.Parallel()

		assert.True(t, unmarshaler.Options{}.Empty())
		assert.False(t, custom.Empty())
		assert.False(t, unmarshaler.Options{JSON: true}.Empty())
	})

	t.Run("with replaces the function of a type", func(t *testing.T) {
		t.Parallel()

		got := custom.With(plain, noOption).With(reflect.TypeFor[jsonValue](), noOption)

		assert.Len(t, got.YAMLOptions(nil), 2)
		assert.Len(t, custom.YAMLOptions(nil), 1, "the first options keep their function")
		assert.Len(t, unmarshaler.Options{JSON: true}.YAMLOptions(nil), 1)
	})

	t.Run("without removes the function of a type", func(t *testing.T) {
		t.Parallel()

		json := reflect.TypeFor[jsonValue]()
		both := custom.With(json, noOption)

		got := both.Without(plain)

		assert.False(t, got.Custom(plain))
		assert.True(t, got.Custom(json))
		assert.True(t, both.Custom(plain), "the first options keep their function")
		assert.True(t, custom.Without(json).Custom(plain), "a type with no function changes nothing")
		assert.True(t, unmarshaler.Options{}.Without(plain).Empty())
		assert.True(t, unmarshaler.Options{JSON: true}.Without(plain).JSON)
	})

	t.Run("custom", func(t *testing.T) {
		t.Parallel()

		assert.True(t, custom.Custom(plain))
		assert.False(t, custom.Custom(reflect.TypeFor[jsonValue]()))
	})
}

func TestOptions_Whole(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		typ   reflect.Type
		opts  unmarshaler.Options
		field int
		whole bool
	}{
		"plain": {
			typ:   reflect.TypeFor[plainValue](),
			field: -1,
		},
		"declared method": {
			typ:   reflect.TypeFor[nodeValue](),
			field: -1,
			whole: true,
		},
		"embedded method": {
			typ:   reflect.TypeFor[embedsNode](),
			field: 1,
			whole: true,
		},
		"custom function": {
			typ:   reflect.TypeFor[embedsNode](),
			opts:  unmarshaler.Options{}.With(reflect.TypeFor[embedsNode](), noOption),
			field: -1,
			whole: true,
		},
		"json without its option": {
			typ:   reflect.TypeFor[embedsJSON](),
			field: -1,
		},
		"embedded json under its option": {
			typ:   reflect.TypeFor[embedsJSON](),
			opts:  unmarshaler.Options{JSON: true},
			field: 0,
			whole: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			field, whole := tc.opts.Whole(tc.typ)
			assert.Equal(t, tc.field, field)
			assert.Equal(t, tc.whole, whole)
		})
	}
}

func TestOptions_ReportsOwnError(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		typ  reflect.Type
		opts unmarshaler.Options
		want bool
	}{
		"duration":         {typ: reflect.TypeFor[time.Duration](), want: true},
		"decodes itself":   {typ: reflect.TypeFor[textValue](), want: true},
		"plain":            {typ: reflect.TypeFor[plainValue]()},
		"json":             {typ: reflect.TypeFor[jsonValue]()},
		"json with option": {typ: reflect.TypeFor[jsonValue](), opts: unmarshaler.Options{JSON: true}, want: true},
		"custom": {
			typ:  reflect.TypeFor[plainValue](),
			opts: unmarshaler.Options{}.With(reflect.TypeFor[plainValue](), noOption),
			want: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.opts.ReportsOwnError(tc.typ))
		})
	}
}

func TestOptions_FromText(t *testing.T) {
	t.Parallel()

	assert.True(t, unmarshaler.Options{}.FromText(reflect.TypeFor[textValue]()))
	assert.False(t, unmarshaler.Options{}.FromText(reflect.TypeFor[textNodeValue]()), "UnmarshalYAML comes first")
	assert.False(t, unmarshaler.Options{}.FromText(reflect.TypeFor[plainValue]()))

	custom := unmarshaler.Options{}.With(reflect.TypeFor[textValue](), noOption)
	assert.False(t, custom.FromText(reflect.TypeFor[textValue]()), "a function comes first")
}

func TestOptions_RefusesForText(t *testing.T) {
	t.Parallel()

	mapping := &ast.MappingNode{}

	tcs := map[string]struct {
		typ     reflect.Type
		content ast.Node
		opts    unmarshaler.Options
		want    bool
	}{
		"text type from a mapping": {
			typ:     reflect.TypeFor[textValue](),
			content: mapping,
			want:    true,
		},
		"text type from a scalar": {
			typ:     reflect.TypeFor[textValue](),
			content: &ast.StringNode{},
		},
		"text type from nothing": {
			typ: reflect.TypeFor[textValue](),
		},
		"time from a mapping": {
			typ:     reflect.TypeFor[time.Time](),
			content: mapping,
		},
		"node type from a mapping": {
			typ:     reflect.TypeFor[nodeValue](),
			content: mapping,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.opts.RefusesForText(tc.typ, tc.content))
		})
	}
}

func TestOptions_InlinesUnmarshaler(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		typ  reflect.Type
		opts unmarshaler.Options
		want bool
	}{
		"inline field that decodes itself": {typ: reflect.TypeFor[inlineNode](), want: true},
		"inside an inline struct":          {typ: reflect.TypeFor[inlineDeep](), want: true},
		"inline struct of fields":          {typ: reflect.TypeFor[inlinePlain]()},
		"inline pointer to itself":         {typ: reflect.TypeFor[inlineSelf]()},
		"inline struct a function decodes": {
			typ:  reflect.TypeFor[inlineCustom](),
			opts: unmarshaler.Options{}.With(reflect.TypeFor[plainValue](), noOption),
			want: true,
		},
		"inline struct without its function": {typ: reflect.TypeFor[inlineCustom]()},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.opts.InlinesUnmarshaler(tc.typ))
			assert.Equal(t, tc.want, tc.opts.InlinesUnmarshaler(tc.typ), "a second call gives the same answer")
		})
	}
}
