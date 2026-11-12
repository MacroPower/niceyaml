package genfield_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.jacobcolvin.com/x/jsonschema"

	"go.jacobcolvin.com/niceyaml/internal/genfield"
	"go.jacobcolvin.com/niceyaml/internal/unmarshaler"
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

// quantity decodes itself through UnmarshalJSON when a decode turns on
// those methods.
type quantity struct {
	Value int
}

func (q *quantity) UnmarshalJSON(b []byte) error {
	return json.Unmarshal(b, &q.Value) //nolint:wrapcheck // The test returns the error as it is.
}

const (
	embedWant = `mixed.inner: embedded struct needs yaml:",inline", because gen reads it inline ` +
		`and the decode nests it`
	portWant = `mixed.Port: field needs json:"port", because gen names it "Port" and the decode reads "port"`
)

func TestCheck(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		typ          reflect.Type
		unmarshalers unmarshaler.Options
		want         []string
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
		},
		"type that the unmarshalers decode whole": {
			typ:          reflect.TypeFor[struct{ Q quantity }](),
			unmarshalers: unmarshaler.Options{JSON: true},
			want: []string{`struct { Q genfield_test.quantity }.Q: ` +
				`field needs json:"q", because gen names it "Q" and the decode reads "q"`},
		},
		"type that the zero unmarshalers decode by field": {
			typ: reflect.TypeFor[struct {
				Q quantity `json:"q"`
			}](),
			want: []string{`quantity.Value: field needs json:"value", because gen names it "Value" ` +
				`and the decode reads "value"`},
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

			assert.Equal(t, tc.want, genfield.Check(tc.typ, tc.unmarshalers))
		})
	}
}
