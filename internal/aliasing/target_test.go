package aliasing_test

import (
	"context"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"github.com/goccy/go-yaml/ast"
	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/internal/aliasing"
)

// textValue reads the text of its node.
type textValue struct{}

func (*textValue) UnmarshalText([]byte) error { return nil }

// bytesValue reads the YAML bytes of its node.
type bytesValue struct{}

func (*bytesValue) UnmarshalYAML([]byte) error { return nil }

// bytesContextValue reads the YAML bytes of its node with a context.
type bytesContextValue struct{}

func (*bytesContextValue) UnmarshalYAML(context.Context, []byte) error { return nil }

// funcValue decodes its node through the function go-yaml hands it.
type funcValue struct{}

func (*funcValue) UnmarshalYAML(func(any) error) error { return nil }

// funcContextValue decodes its node through the function go-yaml hands
// it with a context.
type funcContextValue struct{}

func (*funcContextValue) UnmarshalYAML(context.Context, func(any) error) error { return nil }

// nodeValue decodes itself from its node and holds a text field that
// go-yaml never decodes into.
type nodeValue struct {
	Addr netip.Addr
}

func (*nodeValue) UnmarshalYAML(ast.Node) error { return nil }

// nodeContextValue is [nodeValue] with an UnmarshalYAML that takes a
// context.
type nodeContextValue struct {
	Addr netip.Addr
}

func (*nodeContextValue) UnmarshalYAML(context.Context, ast.Node) error { return nil }

// nodeTextValue decodes itself from its node. Go-yaml calls its
// UnmarshalYAML ahead of its UnmarshalText.
type nodeTextValue struct{}

func (*nodeTextValue) UnmarshalYAML(ast.Node) error { return nil }

func (*nodeTextValue) UnmarshalText([]byte) error { return nil }

// textChain refers to itself and holds a text field below the cycle.
type textChain struct {
	Next *textChain
	Text []textValue
}

// plainChain refers to itself and holds no text field.
type plainChain struct {
	Next *plainChain
	Name string
}

// embeddedTime gets UnmarshalText from the time it embeds.
type embeddedTime struct {
	time.Time
}

// prefixHolder holds a text field. Go-yaml decodes into its fields when
// another struct embeds it, though its name is unexported.
type prefixHolder struct {
	Addr netip.Prefix
}

func TestDecodesText(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		typ  reflect.Type
		want bool
	}{
		"text unmarshaler": {
			typ:  reflect.TypeFor[textValue](),
			want: true,
		},
		"bytes unmarshaler": {
			typ:  reflect.TypeFor[bytesValue](),
			want: true,
		},
		"bytes unmarshaler with a context": {
			typ:  reflect.TypeFor[bytesContextValue](),
			want: true,
		},
		"unmarshaler with a decode function": {
			typ:  reflect.TypeFor[funcValue](),
			want: true,
		},
		"unmarshaler with a decode function and a context": {
			typ:  reflect.TypeFor[funcContextValue](),
			want: true,
		},
		"pointer": {
			typ:  reflect.TypeFor[*textValue](),
			want: true,
		},
		"struct field": {
			typ: reflect.TypeFor[struct {
				Name string
				Addr netip.Prefix
			}](),
			want: true,
		},
		"text field tagged -,": {
			typ: reflect.TypeFor[struct {
				Addr netip.Prefix `yaml:"-,"`
			}](),
			want: true,
		},
		"text field with a json - tag and a yaml tag": {
			typ: reflect.TypeFor[struct {
				Addr netip.Prefix `json:"-" yaml:"addr"`
			}](),
			want: true,
		},
		"embedded unexported struct with a text field": {
			typ: reflect.TypeFor[struct {
				prefixHolder
			}](),
			want: true,
		},
		"array element": {
			typ:  reflect.TypeFor[[2]bytesValue](),
			want: true,
		},
		"map key": {
			typ:  reflect.TypeFor[map[textValue]int](),
			want: true,
		},
		"map value": {
			typ:  reflect.TypeFor[map[string][]textValue](),
			want: true,
		},
		"recursive type with a text field": {
			typ:  reflect.TypeFor[textChain](),
			want: true,
		},
		"embedded time": {
			typ:  reflect.TypeFor[embeddedTime](),
			want: true,
		},
		"time": {
			typ: reflect.TypeFor[time.Time](),
		},
		"struct with time fields": {
			typ: reflect.TypeFor[struct {
				At      time.Time
				Expires *time.Time
				Timeout time.Duration
			}](),
		},
		"recursive type without a text field": {
			typ: reflect.TypeFor[plainChain](),
		},
		"node unmarshaler with a text field": {
			typ: reflect.TypeFor[nodeValue](),
		},
		"node unmarshaler with a context and a text field": {
			typ: reflect.TypeFor[nodeContextValue](),
		},
		"node and text unmarshaler": {
			typ: reflect.TypeFor[nodeTextValue](),
		},
		"struct field node unmarshaler": {
			typ: reflect.TypeFor[struct {
				Name string
				Node *nodeTextValue
			}](),
		},
		"unexported text field": {
			typ: reflect.TypeFor[struct {
				Name string
				addr netip.Prefix
			}](),
		},
		"text field tagged -": {
			typ: reflect.TypeFor[struct {
				Addr netip.Prefix `yaml:"-"`
			}](),
		},
		"text field with a json - tag": {
			typ: reflect.TypeFor[struct {
				Addr netip.Prefix `json:"-"`
			}](),
		},
		"plain types": {
			typ: reflect.TypeFor[map[string][]string](),
		},
		"interface": {
			typ: reflect.TypeFor[any](),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, aliasing.DecodesText(tc.typ))
			assert.Equal(t, tc.want, aliasing.DecodesText(tc.typ), "a second call gives the same answer")
		})
	}
}
