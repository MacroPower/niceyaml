package genfield_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.jacobcolvin.com/x/jsonschema"

	"go.jacobcolvin.com/niceyaml/internal/genfield"
)

type inner struct {
	Note string `json:"note"`
}

// mixed holds a field that breaks a rule for inline fields, and one that
// breaks a rule for names.
type mixed struct {
	inner //nolint:unused // Only reflect reads the field.

	Port int
}

// provided supplies its own schema, so gen does not read its fields.
type provided struct {
	inner //nolint:unused // Only reflect reads the field.
}

func (provided) JSONSchema(context.Context, jsonschema.TypeContext) (jsonschema.TypeSchema, error) {
	return jsonschema.TypeSchema{}, nil
}

const (
	embedWant = `mixed.inner: embedded struct needs yaml:",inline", because gen reads it inline ` +
		`and the decode nests it`
	portWant = `mixed.Port: field needs json:"port", because gen names it "Port" and the decode reads "port"`
)

func TestInline(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		typ  reflect.Type
		want []string
	}{
		"rules for inline fields alone": {
			typ:  reflect.TypeFor[mixed](),
			want: []string{embedWant},
		},
		"embed option": {
			typ: reflect.TypeFor[struct {
				Extra inner `json:",embed"`
			}](),
			want: []string{`struct { Extra genfield_test.inner "json:\",embed\"" }.Extra: ` +
				`field with the embed option needs yaml:",inline", because gen reads it inline ` +
				`and the decode nests it`},
		},
		"embed option with an inline option": {
			typ: reflect.TypeFor[struct {
				Extra inner `json:",embed" yaml:",inline"`
			}](),
		},
		"named field with an inline option": {
			typ: reflect.TypeFor[struct {
				Extra inner `yaml:",inline"`
			}](),
			want: []string{`struct { Extra genfield_test.inner "yaml:\",inline\"" }.Extra: ` +
				`named field has an inline option, which the decode reads inline and gen nests`},
		},
		"type that supplies its own schema": {
			typ: reflect.TypeFor[provided](),
			want: []string{`provided.inner: embedded struct needs yaml:",inline", because gen reads it inline ` +
				`and the decode nests it`},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, genfield.Inline(tc.typ))
		})
	}
}

func TestCheck(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		typ  reflect.Type
		want []string
	}{
		"rules for inline fields and names": {
			typ:  reflect.TypeFor[mixed](),
			want: []string{embedWant, portWant},
		},
		"fields that agree": {
			typ: reflect.TypeFor[struct {
				inner `yaml:",inline"`

				Port int `json:"port"`
			}](),
		},
		"type that supplies its own schema": {
			typ: reflect.TypeFor[provided](),
		},
		"map with the embed option that the decoder skips": {
			typ: reflect.TypeFor[struct {
				Extra map[string]string `json:",embed" yaml:"-"`
			}](),
			want: []string{`struct { Extra map[string]string "json:\",embed\" yaml:\"-\"" }.Extra: ` +
				`field with the embed option needs json:"-", because the decode skips it ` +
				`and gen merges its entries into the parent`},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, genfield.Check(tc.typ))
		})
	}
}
