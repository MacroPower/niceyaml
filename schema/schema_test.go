package schema_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/jsonschema"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/schema"
)

// compileSchema compiles schemaData into a [*schema.Schema] and fails the
// test if the schema does not compile.
func compileSchema(t *testing.T, schemaData []byte) *schema.Schema {
	t.Helper()

	v, err := schema.Compile(t.Context(), schemaData)
	require.NoError(t, err)

	return v
}

func TestSchema_Validate(t *testing.T) {
	t.Parallel()

	schemaData := []byte(`{
		"type": "object",
		"properties": {
			"name": {"type": "string"},
			"age": {"type": "number"},
			"items": {
				"type": "array",
				"items": {"type": "string"}
			},
			"nested": {
				"type": "object",
				"properties": {
					"value": {"type": "string"}
				},
				"required": ["value"],
				"additionalProperties": false
			},
			"users": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {
						"id": {"type": "number"},
						"email": {"type": "string"},
						"profile": {
							"type": "object",
							"properties": {
								"firstName": {"type": "string"},
								"lastName": {"type": "string"},
								"preferences": {
									"type": "array",
									"items": {"type": "string"}
								}
							},
							"required": ["firstName", "lastName"],
							"additionalProperties": false
						}
					},
					"required": ["id", "email", "profile"],
					"additionalProperties": false
				}
			},
			"matrix": {
				"type": "array",
				"items": {
					"type": "array",
					"items": {"type": "number"}
				}
			}
		},
		"required": ["name"],
		"additionalProperties": false
	}`)

	v := compileSchema(t, schemaData)

	tcs := map[string]struct {
		input   any
		wantErr bool
	}{
		"valid data": {
			input: map[string]any{
				"name": "Kallistō",
				"age":  30,
			},
		},
		"missing required field": {
			input: map[string]any{
				"age": 30,
			},
			wantErr: true,
		},
		"wrong type for name": {
			input: map[string]any{
				"name": 123,
				"age":  30,
			},
			wantErr: true,
		},
		"wrong type for age": {
			input: map[string]any{
				"name": "Kallistō",
				"age":  "thirty",
			},
			wantErr: true,
		},
		"invalid array item": {
			input: map[string]any{
				"name":  "John",
				"items": []any{"valid", 123, "also valid"},
			},
			wantErr: true,
		},
		"nested object validation error": {
			input: map[string]any{
				"name": "Kallistō",
				"nested": map[string]any{
					"notValue": "something",
				},
			},
			wantErr: true,
		},
		"valid array of objects": {
			input: map[string]any{
				"name": "Kallistō",
				"users": []any{
					map[string]any{
						"id":    1,
						"email": "kallisto@example.com",
						"profile": map[string]any{
							"firstName": "Kallistō",
							"lastName":  "Lykaonis",
						},
					},
					map[string]any{
						"id":    2,
						"email": "aello@example.com",
						"profile": map[string]any{
							"firstName":   "Jane",
							"lastName":    "Thaumantias",
							"preferences": []any{"dark_mode", "notifications"},
						},
					},
				},
			},
		},
		"invalid object in array": {
			input: map[string]any{
				"name": "Kallistō",
				"users": []any{
					map[string]any{
						"id":    "invalid", // Should be number.
						"email": "aello@example.com",
						"profile": map[string]any{
							"firstName": "Aëllo",
							"lastName":  "Thaumantias",
						},
					},
				},
			},
			wantErr: true,
		},
		"missing required field in nested object within array": {
			input: map[string]any{
				"name": "Kallistō",
				"users": []any{
					map[string]any{
						"id":    1,
						"email": "kallisto@example.com",
						"profile": map[string]any{
							"firstName": "Kallistō",
							// Missing lastName.
						},
					},
				},
			},
			wantErr: true,
		},
		"valid matrix (2D array)": {
			input: map[string]any{
				"name": "Kallistō",
				"matrix": []any{
					[]any{1, 2, 3},
					[]any{4, 5, 6},
				},
			},
		},
		"invalid element in 2D array": {
			input: map[string]any{
				"name": "Kallistō",
				"matrix": []any{
					[]any{1, 2, 3},
					[]any{4, "invalid", 6}, // Should be number.
				},
			},
			wantErr: true,
		},
		"additional property at root": {
			input: map[string]any{
				"name":      "John",
				"extraProp": "not allowed",
			},
			wantErr: true,
		},
		"additional property in nested object": {
			input: map[string]any{
				"name": "Kallistō",
				"nested": map[string]any{
					"value":       "valid",
					"extraNested": "not allowed",
				},
			},
			wantErr: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := v.ValidateValue(t.Context(), tc.input)

			if tc.wantErr {
				require.Error(t, err)

				var validationErr *niceyaml.Error

				require.ErrorAs(t, err, &validationErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestSchema_UnresolvableRef(t *testing.T) {
	t.Parallel()

	unreachable := errors.New("host unreachable")
	refusing := jsonschema.RefResolverFunc(func(_ context.Context, _ string) (*jsonschema.Schema, error) {
		return nil, unreachable
	})

	// The ref resolves when the validator walks to it, so the failure
	// arrives at the location of the value that referenced it. The schema
	// is at fault, not the document, so every case wraps ErrValidate.
	tcs := map[string]struct {
		data   map[string]any
		err    error
		schema string
		want   string
		opts   []schema.CompileOption
	}{
		"no resolver": {
			schema: `{"properties": {"a": {"$ref": "https://example.invalid/nope.json"}}}`,
			data:   map[string]any{"a": 1},
			want:   `cannot resolve $ref "https://example.invalid/nope.json"`,
		},
		"resolver answering not resolved": {
			opts: []schema.CompileOption{schema.WithJSONSchemaOptions(
				jsonschema.WithRefResolver(jsonschema.SchemaMap{}),
			)},
			schema: `{"properties": {"a": {"$ref": "https://example.invalid/nope.json"}}}`,
			data:   map[string]any{"a": 1},
			want:   `cannot resolve $ref "https://example.invalid/nope.json"`,
		},
		"resolver returning an error": {
			opts: []schema.CompileOption{schema.WithJSONSchemaOptions(
				jsonschema.WithRefResolver(refusing),
			)},
			schema: `{"properties": {"a": {"$ref": "https://example.invalid/nope.json"}}}`,
			data:   map[string]any{"a": 1},
			err:    unreachable,
			want:   "host unreachable",
		},
		"beside a violation": {
			schema: `{"properties": {
				"a": {"$ref": "https://example.invalid/nope.json"},
				"b": {"type": "string"}
			}}`,
			data: map[string]any{"a": 1, "b": 2},
			want: `cannot resolve $ref "https://example.invalid/nope.json"`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			v, err := schema.Compile(t.Context(), []byte(tc.schema), tc.opts...)
			require.NoError(t, err)

			err = v.ValidateValue(t.Context(), tc.data)
			require.ErrorIs(t, err, schema.ErrValidate)
			assert.Contains(t, err.Error(), tc.want)

			if tc.err != nil {
				require.ErrorIs(t, err, jsonschema.ErrRefResolve)
				require.ErrorIs(t, err, tc.err)
			}

			var nerr *niceyaml.Error

			require.NotErrorAs(t, err, &nerr)
		})
	}
}

func TestSchema_ValidateWithDecoder(t *testing.T) {
	t.Parallel()

	schemaData := []byte(`{
		"type": "object",
		"properties": {
			"name": {"type": "string"},
			"age": {"type": "number"},
			"nested": {
				"type": "object",
				"properties": {
					"value": {"type": "string"}
				},
				"required": ["value"],
				"additionalProperties": false
			}
		},
		"required": ["name"],
		"additionalProperties": false
	}`)

	v := compileSchema(t, schemaData)

	tcs := map[string]struct {
		input   string
		wantErr bool
	}{
		"valid data": {
			input: stringtest.Input(`
				name: Kallisto
				age: 30
			`),
		},
		"missing required field": {
			input:   stringtest.Input(`age: 30`),
			wantErr: true,
		},
		"wrong type for name": {
			input: stringtest.Input(`
				name: 123
				age: 30
			`),
			wantErr: true,
		},
		"nested object validation error": {
			input: stringtest.Input(`
				name: Kallisto
				nested:
				  notValue: something
			`),
			wantErr: true,
		},
		"additional property at root": {
			input: stringtest.Input(`
				name: John
				extraProp: not allowed
			`),
			wantErr: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)
			d, err := source.Documents()
			require.NoError(t, err)

			for _, dd := range d {
				err = dd.Validate(t.Context(), v)

				if tc.wantErr {
					require.Error(t, err)

					var validationErr *niceyaml.Error

					require.ErrorAs(t, err, &validationErr)
				} else {
					require.NoError(t, err)
				}
			}
		})
	}
}

func TestSchema_PathTarget(t *testing.T) {
	t.Parallel()

	// The validator chooses key vs value highlighting based on the type of
	// validation error. Each case checks which part of the YAML the error
	// overlay style wraps.

	newXMLPrinter := func() *printer.Printer {
		return printer.New(
			printer.WithStyles(yamltest.NewXMLStyles()),
			printer.WithGutter(printer.NoGutter),
		)
	}

	tcs := map[string]struct {
		schema       string
		input        string
		wantContains string // Substring that should appear in error output.
	}{
		"type error highlights value": {
			schema: `{
				"type": "object",
				"properties": {
					"name": {"type": "string"}
				}
			}`,
			input: stringtest.Input(`
				name: 123
			`),
			wantContains: "<genericError>123</genericError>",
		},
		"additional property highlights key": {
			schema: `{
				"type": "object",
				"properties": {
					"name": {"type": "string"}
				},
				"additionalProperties": false
			}`,
			input: stringtest.Input(`
				name: valid
				extra: notAllowed
			`),
			wantContains: "<genericError>extra</genericError>",
		},
		"required error highlights the parent key": {
			// A missing required property targets the containing object's key,
			// and the instance segments lead to it.
			schema: `{
				"type": "object",
				"properties": {
					"user": {
						"type": "object",
						"properties": {
							"name": {"type": "string"}
						},
						"required": ["name"]
					}
				}
			}`,
			input: stringtest.Input(`
				user:
				  age: 30
			`),
			wantContains: "<genericError>user</genericError>",
		},
		"error behind a merge key highlights the anchored value": {
			// The validator sees merged data, so the path leads through the
			// merge key to the anchor that defines the offending value.
			schema: `{
				"type": "object",
				"properties": {
					"user": {
						"type": "object",
						"properties": {
							"age": {"type": "integer"}
						}
					}
				}
			}`,
			input: stringtest.Input(`
				base: &b
				  age: old
				user:
				  <<: *b
				  name: x
			`),
			wantContains: "<genericError>old</genericError>",
		},
		"required error on the root highlights the first key": {
			schema: `{
				"type": "object",
				"required": ["name"]
			}`,
			input: stringtest.Input(`
				other: x
			`),
			wantContains: "<genericError>other</genericError>",
		},
		"enum error highlights value": {
			schema: `{
				"type": "object",
				"properties": {
					"status": {"enum": ["active", "inactive"]}
				}
			}`,
			input: stringtest.Input(`
				status: unknown
			`),
			wantContains: "<genericError>unknown</genericError>",
		},
		"minimum error highlights value": {
			schema: `{
				"type": "object",
				"properties": {
					"age": {"type": "integer", "minimum": 0}
				}
			}`,
			input: stringtest.Input(`
				age: -5
			`),
			wantContains: "<genericError>-5</genericError>",
		},
		"pattern error highlights value": {
			schema: `{
				"type": "object",
				"properties": {
					"email": {"type": "string", "pattern": "^[a-z]+@[a-z]+\\.[a-z]+$"}
				}
			}`,
			input: stringtest.Input(`
				email: notanemail
			`),
			wantContains: "<genericError>notanemail</genericError>",
		},
		"minItems error highlights key": {
			schema: `{
				"type": "object",
				"properties": {
					"items": {"type": "array", "minItems": 2}
				}
			}`,
			input: stringtest.Input(`
				items:
				  - one
			`),
			wantContains: "<genericError>items</genericError>",
		},
		"array item type error highlights value": {
			schema: `{
				"type": "object",
				"properties": {
					"numbers": {
						"type": "array",
						"items": {"type": "integer"}
					}
				}
			}`,
			input: stringtest.Input(`
				numbers:
				  - 1
				  - notanumber
				  - 3
			`),
			wantContains: "<genericError>notanumber</genericError>",
		},
		"propertyNames error highlights the offending key": {
			// The library reports a propertyNames violation at the offending
			// property with keyword "propertyNames", so the validator targets
			// the bad key.
			schema: `{
				"type": "object",
				"properties": {
					"config": {
						"type": "object",
						"propertyNames": {"pattern": "^[a-z]+$"}
					}
				}
			}`,
			input: stringtest.Input(`
				config:
				  BadKey: 1
			`),
			wantContains: "<genericError>BadKey</genericError>",
		},
		"hexadecimal key highlights value": {
			// The key decodes to the member name 16, which the source spells
			// 0x10, so the path names the member by its source spelling.
			schema: `{
				"type": "object",
				"properties": {
					"16": {"type": "integer"}
				}
			}`,
			input: stringtest.Input(`
				0x10: hello
			`),
			wantContains: "<genericError>hello</genericError>",
		},
		"tilde null key highlights value": {
			// The key decodes to the member name null, which the source
			// spells ~, so the path names the member by its source spelling.
			schema: `{
				"type": "object",
				"additionalProperties": {"type": "string"}
			}`,
			input: stringtest.Input(`
				~: 5
			`),
			wantContains: "<genericError>5</genericError>",
		},
		"spelled-out null key highlights value": {
			schema: `{
				"type": "object",
				"additionalProperties": {"type": "string"}
			}`,
			input: stringtest.Input(`
				NULL: 5
			`),
			wantContains: "<genericError>5</genericError>",
		},
		"boolean key highlights the key": {
			schema: `{
				"type": "object",
				"properties": {
					"name": {"type": "string"}
				},
				"additionalProperties": false
			}`,
			input: stringtest.Input(`
				True: nope
			`),
			wantContains: "<genericError>True</genericError>",
		},
		"false subschema on a keyword-named property highlights value": {
			// A property named like a key-targeting keyword ("contains") must
			// still highlight the value, not the key.
			schema: `{
				"type": "object",
				"properties": {
					"contains": false
				}
			}`,
			input: stringtest.Input(`
				contains: 5
			`),
			wantContains: "<genericError>5</genericError>",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			v := compileSchema(t, []byte(tc.schema))

			source := niceyaml.NewSourceFromString(tc.input)
			d, err := source.Documents()
			require.NoError(t, err)

			for _, dd := range d {
				err = dd.Validate(t.Context(), v)
				require.Error(t, err)

				var bound *niceyaml.SourceError

				require.ErrorAs(t, err, &bound)

				assert.Contains(t, newXMLPrinter().PrintError(bound), tc.wantContains,
					"expected error output to contain specific highlighting pattern")
			}
		})
	}
}

func TestSourcePath_TypedNilNode(t *testing.T) {
	t.Parallel()

	// The parser always puts a node where these trees hold a typed nil,
	// but a tree built or rewritten by hand may not, and the walk keeps
	// the decoded name for it rather than panicking. An alias that does
	// not resolve, or one that leads back to itself, keeps the decoded
	// name as well.
	name := &ast.StringNode{Token: &token.Token{Value: "a"}, Value: "a"}

	tcs := map[string]struct {
		root     ast.Node
		segments []jsonschema.Segment
		want     string
	}{
		"mapping behind an anchor": {
			root:     &ast.AnchorNode{Value: (*ast.MappingNode)(nil)},
			segments: []jsonschema.Segment{{Key: "name"}},
			want:     "$.name",
		},
		"mapping value behind an anchor": {
			root:     &ast.AnchorNode{Value: (*ast.MappingValueNode)(nil)},
			segments: []jsonschema.Segment{{Key: "name"}},
			want:     "$.name",
		},
		"sequence behind an anchor": {
			root:     &ast.AnchorNode{Value: (*ast.SequenceNode)(nil)},
			segments: []jsonschema.Segment{{Index: 0, IsIndex: true}},
			want:     "$[0]",
		},
		"tag behind an anchor": {
			root:     &ast.AnchorNode{Value: (*ast.TagNode)(nil)},
			segments: []jsonschema.Segment{{Key: "name"}},
			want:     "$.name",
		},
		"anchor behind an anchor": {
			root:     &ast.AnchorNode{Value: (*ast.AnchorNode)(nil)},
			segments: []jsonschema.Segment{{Key: "name"}},
			want:     "$.name",
		},
		"document behind an anchor": {
			root:     &ast.AnchorNode{Value: (*ast.DocumentNode)(nil)},
			segments: []jsonschema.Segment{{Key: "name"}},
			want:     "$.name",
		},
		"mapping key behind an anchor": {
			root:     &ast.AnchorNode{Value: (*ast.MappingKeyNode)(nil)},
			segments: []jsonschema.Segment{{Key: "name"}},
			want:     "$.name",
		},
		"alias behind an anchor": {
			root:     &ast.AnchorNode{Value: (*ast.AliasNode)(nil)},
			segments: []jsonschema.Segment{{Key: "name"}},
			want:     "$.name",
		},
		"alias with no target keeps the decoded name": {
			root: &ast.MappingNode{Values: []*ast.MappingValueNode{{
				Key:   &ast.StringNode{Value: "a"},
				Value: &ast.AliasNode{Value: &ast.StringNode{Value: "b"}},
			}}},
			segments: []jsonschema.Segment{{Key: "a"}, {Key: "16"}},
			want:     "$.a.16",
		},
		"alias that leads back to itself": {
			// The anchor holds the alias, so the alias refers to itself.
			root:     &ast.AnchorNode{Name: name, Value: &ast.AliasNode{Value: name}},
			segments: []jsonschema.Segment{{Key: "name"}},
			want:     "$.name",
		},
		"alias that leads back to itself through a tag": {
			root: &ast.AnchorNode{
				Name:  name,
				Value: &ast.TagNode{Value: &ast.AliasNode{Value: name}},
			},
			segments: []jsonschema.Segment{{Key: "name"}},
			want:     "$.name",
		},
		"key with no token keeps the decoded name": {
			root: &ast.MappingNode{Values: []*ast.MappingValueNode{{
				Key:   &ast.IntegerNode{Value: 16},
				Value: &ast.StringNode{Value: "x"},
			}}},
			segments: []jsonschema.Segment{{Key: "16"}},
			want:     "$.16",
		},
		"nil member": {
			root:     &ast.MappingNode{Values: []*ast.MappingValueNode{nil}},
			segments: []jsonschema.Segment{{Key: "a"}},
			want:     "$.a",
		},
		"typed-nil string key": {
			root: &ast.MappingNode{Values: []*ast.MappingValueNode{{
				Key:   (*ast.StringNode)(nil),
				Value: &ast.StringNode{Value: "x"},
			}}},
			segments: []jsonschema.Segment{{Key: "a"}},
			want:     "$.a",
		},
		"typed-nil integer key": {
			root: &ast.MappingNode{Values: []*ast.MappingValueNode{{
				Key:   (*ast.IntegerNode)(nil),
				Value: &ast.StringNode{Value: "x"},
			}}},
			segments: []jsonschema.Segment{{Key: "a"}},
			want:     "$.a",
		},
		"typed-nil key behind a tag": {
			root: &ast.MappingNode{Values: []*ast.MappingValueNode{{
				Key:   &ast.TagNode{Value: (*ast.StringNode)(nil)},
				Value: &ast.StringNode{Value: "x"},
			}}},
			segments: []jsonschema.Segment{{Key: "a"}},
			want:     "$.a",
		},
		"no tree": {
			root:     nil,
			segments: []jsonschema.Segment{{Key: "items"}, {Index: 1, IsIndex: true}, {Key: "16"}},
			want:     "$.items[1].16",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := paths.NewResolver(&ast.DocumentNode{Body: tc.root})

			assert.Equal(t, tc.want, schema.SourcePath(tc.root, r, tc.segments).String())
		})
	}
}

func TestSchema_NonFiniteFloats(t *testing.T) {
	t.Parallel()

	// YAML decodes .nan/.inf into non-finite float64 values. These are not
	// JSON-encodable, but the validator treats them as numbers rather than
	// surfacing an opaque marshaling error.
	v := compileSchema(t, []byte(`{
		"type": "object",
		"properties": {"x": {"type": "number"}}
	}`))

	for name, value := range map[string]float64{
		"NaN":          math.NaN(),
		"positive inf": math.Inf(1),
		"negative inf": math.Inf(-1),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := v.ValidateValue(t.Context(), map[string]any{"x": value})
			require.NoError(t, err)
			assert.NotErrorIs(t, err, schema.ErrValidate)
		})
	}
}

func TestSchema_YAMLNativeTypes(t *testing.T) {
	t.Parallel()

	// YAML decodes !!binary into []byte and !!timestamp into time.Time,
	// neither of which the JSON Schema validator accepts. The validator
	// converts them to their JSON spellings, so a tagged scalar does not
	// suppress every other constraint in the document. A !!timestamp the
	// source wrote as a bare date becomes a full-date, the spelling the same
	// date has without the tag, also behind an alias or a merge key. Where
	// Validate cannot tell which scalar a timestamp came from, as for a
	// member a merge key brings in when the mapping has a key of its own
	// with the same spelling, the timestamp becomes a date-time.
	tcs := map[string]struct {
		schema string
		input  string
		err    string
		opts   []niceyaml.SourceOption
	}{
		"binary scalar": {
			schema: `{
				"type": "object",
				"properties": {"b": {"type": "string"}}
			}`,
			input: stringtest.Input(`
				b: !!binary aGk=
			`),
		},
		"timestamp scalar": {
			schema: `{
				"type": "object",
				"properties": {"d": {"type": "string"}}
			}`,
			input: stringtest.Input(`
				d: !!timestamp 2024-01-01T00:00:00Z
			`),
		},
		"date-only timestamp matches format date": {
			schema: `{
				"type": "object",
				"properties": {"d": {"type": "string", "format": "date"}}
			}`,
			input: stringtest.Input(`
				d: !!timestamp 2001-12-14
			`),
		},
		"date-only timestamp is no date-time": {
			schema: `{
				"type": "object",
				"properties": {"d": {"type": "string", "format": "date-time"}}
			}`,
			input: stringtest.Input(`
				d: !!timestamp 2001-12-14
			`),
			err: `1:16: $.d: string does not match format "date-time"`,
		},
		"midnight date-time timestamp keeps its time": {
			schema: `{
				"type": "object",
				"properties": {"d": {"type": "string", "format": "date-time"}}
			}`,
			input: stringtest.Input(`
				d: !!timestamp 2001-12-14T00:00:00Z
			`),
		},
		"date-only timestamp in a sequence": {
			schema: `{
				"type": "object",
				"properties": {
					"d": {
						"type": "array",
						"items": {"type": "string", "format": "date"}
					}
				}
			}`,
			input: stringtest.Input(`
				d: [!!timestamp 2001-12-14]
			`),
		},
		"date-only block scalar timestamp matches format date": {
			schema: `{
				"type": "object",
				"properties": {"d": {"type": "string", "format": "date"}}
			}`,
			input: stringtest.Input(`
				d: !!timestamp >-
				  2001-12-14
			`),
		},
		"repeated key keeps the later midnight date-time": {
			schema: `{
				"type": "object",
				"properties": {"d": {"type": "string", "format": "date-time"}}
			}`,
			input: stringtest.Input(`
				d: !!timestamp 2001-12-14
				d: !!timestamp 2001-12-14T00:00:00Z
			`),
			opts: []niceyaml.SourceOption{niceyaml.WithAllowDuplicateKeys(true)},
		},
		"repeated key keeps the later date": {
			schema: `{
				"type": "object",
				"properties": {"d": {"type": "string", "format": "date"}}
			}`,
			input: stringtest.Input(`
				d: !!timestamp 2001-12-14T00:00:00Z
				d: !!timestamp 2001-12-14
			`),
			opts: []niceyaml.SourceOption{niceyaml.WithAllowDuplicateKeys(true)},
		},
		"tagged key keeps the later midnight date-time": {
			// The !!null tag makes the key x name the member null.
			schema: `{
				"type": "object",
				"properties": {"null": {"type": "string", "format": "date-time"}}
			}`,
			input: stringtest.Input(`
				null: !!timestamp 2001-12-14
				!!null x: !!timestamp 2001-12-14T00:00:00Z
			`),
		},
		"alias key keeps the later midnight date-time": {
			// The alias key names the member d, as its anchored value does.
			schema: `{
				"type": "object",
				"properties": {"d": {"type": "string", "format": "date-time"}}
			}`,
			input: stringtest.Input(`
				a: &k d
				d: !!timestamp 2001-12-14
				*k : !!timestamp 2001-12-14T00:00:00Z
			`),
		},
		"merge key keeps the merged midnight date-time": {
			// The merge key sets d after the member written before it.
			schema: `{
				"type": "object",
				"properties": {"d": {"type": "string", "format": "date-time"}}
			}`,
			input: stringtest.Input(`
				base: &base
				  d: !!timestamp 2001-12-14T00:00:00Z
				d: !!timestamp 2001-12-14
				<<: *base
			`),
		},
		"date-only timestamp behind an alias matches format date": {
			schema: `{
				"type": "object",
				"properties": {
					"a": {"type": "string", "format": "date"},
					"d": {"type": "string", "format": "date"}
				}
			}`,
			input: stringtest.Input(`
				a: &x !!timestamp 2001-12-14
				d: *x
			`),
		},
		"date-only timestamp in an aliased mapping matches format date": {
			schema: `{
				"type": "object",
				"properties": {
					"m": {
						"type": "object",
						"properties": {"d": {"type": "string", "format": "date"}}
					}
				}
			}`,
			input: stringtest.Input(`
				base: &b {d: !!timestamp 2001-12-14}
				m: *b
			`),
		},
		"merged date-only timestamp matches format date": {
			schema: `{
				"type": "object",
				"properties": {
					"m": {
						"type": "object",
						"properties": {"d": {"type": "string", "format": "date"}}
					}
				}
			}`,
			input: stringtest.Input(`
				base: &b {d: !!timestamp 2001-12-14}
				m: {<<: *b}
			`),
		},
		"merge sequence keeps the later source's date": {
			// A later merge source sets d over an earlier one.
			schema: `{
				"type": "object",
				"properties": {
					"m": {
						"type": "object",
						"properties": {"d": {"type": "string", "format": "date"}}
					}
				}
			}`,
			input: stringtest.Input(`
				b1: &b1
				  d: !!timestamp 2001-12-14T00:00:00Z
				b2: &b2
				  d: !!timestamp 2001-12-14
				m: {<<: [*b1, *b2]}
			`),
		},
		"merge sequence keeps the later source's midnight date-time": {
			schema: `{
				"type": "object",
				"properties": {
					"m": {
						"type": "object",
						"properties": {"d": {"type": "string", "format": "date-time"}}
					}
				}
			}`,
			input: stringtest.Input(`
				b1: &b1
				  d: !!timestamp 2001-12-14
				b2: &b2
				  d: !!timestamp 2001-12-14T00:00:00Z
				m: {<<: [*b1, *b2]}
			`),
		},
		"date under a second tag keeps the date-time": {
			// The !!int tag hands the !!timestamp tag a number rather than
			// the text, so the decode yields the zero time.
			schema: `{
				"type": "object",
				"properties": {"d": {"type": "string", "format": "date-time"}}
			}`,
			input: stringtest.Input(`
				d: !!timestamp !!int 2001-12-14
			`),
		},
		"binary scalar violating a constraint": {
			schema: `{
				"type": "object",
				"properties": {"n": {"type": "integer"}}
			}`,
			input: stringtest.Input(`
				b: !!binary aGk=
				n: notint
			`),
			err: `2:4: $.n: expected "integer", got "string"`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			v, err := schema.Compile(t.Context(), []byte(tc.schema),
				schema.WithJSONSchemaOptions(jsonschema.WithFormats(true)))
			require.NoError(t, err)

			// FirstDocument takes no source options, and a repeated key
			// needs one.
			doc, err := niceyaml.NewSourceFromString(tc.input, tc.opts...).Document()
			require.NoError(t, err)

			err = doc.Validate(t.Context(), v)
			if tc.err == "" {
				require.NoError(t, err)

				return
			}

			require.Error(t, err)
			require.NotErrorIs(t, err, schema.ErrValidate)
			assert.Contains(t, err.Error(), tc.err)
		})
	}
}

func TestSchema_ValidateValue_OrderedMap(t *testing.T) {
	t.Parallel()

	// A decode with yaml.UseOrderedMap yields yaml.MapSlice for every
	// mapping, which the JSON Schema validator does not accept.
	// ValidateValue converts each one to a map with the same members, so
	// the schema checks the data as it checks a plain decode.
	//
	// Each level of the bomb maps ten keys to the level below, so the last
	// level expands to 10^4 mappings.
	var bomb strings.Builder

	bomb.WriteString("l0: &l0 {a: x}\n")

	for level := 1; level <= 4; level++ {
		members := make([]string, 0, 10)
		for i := range 10 {
			members = append(members, fmt.Sprintf("k%d: *l%d", i, level-1))
		}

		fmt.Fprintf(&bomb, "l%d: &l%d {%s}\n", level, level, strings.Join(members, ", "))
	}

	tcs := map[string]struct {
		schema string
		input  string
		err    string
		errs   []error
	}{
		"conforming ordered mapping": {
			schema: `{
				"type": "object",
				"properties": {"a": {"type": "string"}}
			}`,
			input: stringtest.Input(`
				a: x
			`),
		},
		"top-level violation": {
			schema: `{
				"type": "object",
				"properties": {"a": {"type": "string"}}
			}`,
			input: stringtest.Input(`
				a: 1
			`),
			err: `$.a: expected "string", got "integer"`,
		},
		"nested ordered mapping": {
			schema: `{
				"type": "object",
				"properties": {
					"n": {
						"type": "object",
						"properties": {"b": {"type": "integer"}}
					}
				}
			}`,
			input: stringtest.Input(`
				n: {b: notint}
			`),
			err: `$.n.b`,
		},
		"ordered mapping inside a sequence": {
			schema: `{
				"type": "object",
				"properties": {
					"items": {
						"type": "array",
						"items": {
							"type": "object",
							"properties": {"b": {"type": "integer"}}
						}
					}
				}
			}`,
			input: stringtest.Input(`
				items: [{b: notint}]
			`),
			err: `$.items[0].b`,
		},
		"binary scalar inside ordered mapping": {
			schema: `{
				"type": "object",
				"properties": {"b": {"type": "string"}}
			}`,
			input: stringtest.Input(`
				b: !!binary aGk=
			`),
		},
		"alias bomb of ordered mappings": {
			schema: `{"type": "object"}`,
			input:  bomb.String(),
			errs:   []error{schema.ErrValidate, schema.ErrExcessiveAliasing},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			v := compileSchema(t, []byte(tc.schema))
			doc := yamltest.FirstDocument(t, tc.input)

			data, err := doc.Decode[any](t.Context(),
				niceyaml.WithYAMLDecodeOptions(yaml.UseOrderedMap()),
				niceyaml.WithAliasLimit(false),
			)
			require.NoError(t, err)

			err = v.ValidateValue(t.Context(), data)

			switch {
			case tc.errs != nil:
				for _, want := range tc.errs {
					require.ErrorIs(t, err, want)
				}

			case tc.err != "":
				require.Error(t, err)
				require.NotErrorIs(t, err, schema.ErrValidate)
				assert.Contains(t, err.Error(), tc.err)

			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestNormalizeJSON(t *testing.T) {
	t.Parallel()

	// NormalizeJSON copies a map or slice only when a !!binary, a
	// !!timestamp, or an ordered mapping sits somewhere under it, and it
	// never writes into the caller's data. Each input builds a fresh value,
	// so a second call yields the original to compare against.
	stamp := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)

	tcs := map[string]struct {
		input func() any
		want  any
	}{
		"plain nested containers": {
			input: func() any {
				return map[string]any{
					"list": []any{1, "x"},
					"map":  map[string]any{"ok": true},
				}
			},
			want: map[string]any{
				"list": []any{1, "x"},
				"map":  map[string]any{"ok": true},
			},
		},
		"binary two levels deep beside a plain map": {
			input: func() any {
				return map[string]any{
					"outer":   map[string]any{"b": []byte("hi")},
					"sibling": map[string]any{"k": "v"},
				}
			},
			want: map[string]any{
				"outer":   map[string]any{"b": "aGk="},
				"sibling": map[string]any{"k": "v"},
			},
		},
		"timestamp in a slice beside a plain map": {
			input: func() any {
				return []any{stamp, map[string]any{"k": "v"}}
			},
			want: []any{"2024-01-02T03:04:05Z", map[string]any{"k": "v"}},
		},
		"ordered mapping beside a plain map": {
			// Each key reads as a decode into a map names it, and the later
			// of two items with the same key wins.
			input: func() any {
				return map[string]any{
					"ordered": yaml.MapSlice{
						{Key: "a", Value: 1},
						{Key: nil, Value: []byte("hi")},
						{Key: 16, Value: yaml.MapSlice{{Key: "k", Value: "v"}}},
						{Key: "a", Value: 2},
					},
					"sibling": map[string]any{"k": "v"},
				}
			},
			want: map[string]any{
				"ordered": map[string]any{
					"a":    2,
					"null": "aGk=",
					"16":   map[string]any{"k": "v"},
				},
				"sibling": map[string]any{"k": "v"},
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			input := tc.input()
			got := schema.NormalizeJSON(input, nil)
			require.Equal(t, tc.want, got)
			assert.Equal(t, tc.input(), input)
			assertCopiedOnChange(t, input, got)
		})
	}
}

func TestNormalizeJSON_TypedNilNode(t *testing.T) {
	t.Parallel()

	// The lookup of the scalar a timestamp came from reads a tree built by
	// hand that holds a nil member or a typed-nil key without panicking.
	// A nil member sets nothing. A typed-nil key has no name, so it may
	// set a member of any name, and a timestamp under a member before it
	// becomes a date-time.
	date := &ast.TagNode{Value: &ast.StringNode{Value: "2001-12-14"}}
	stamp := time.Date(2001, 12, 14, 0, 0, 0, 0, time.UTC)

	tcs := map[string]struct {
		root *ast.MappingNode
		want string
	}{
		"nil member": {
			root: &ast.MappingNode{Values: []*ast.MappingValueNode{
				{Key: &ast.StringNode{Value: "a"}, Value: date},
				nil,
			}},
			want: "2001-12-14",
		},
		"typed-nil key": {
			root: &ast.MappingNode{Values: []*ast.MappingValueNode{
				{Key: &ast.StringNode{Value: "a"}, Value: date},
				{Key: (*ast.StringNode)(nil), Value: date},
			}},
			want: "2001-12-14T00:00:00Z",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := schema.NormalizeJSON(map[string]any{"a": stamp}, tc.root)
			assert.Equal(t, map[string]any{"a": tc.want}, got)
		})
	}
}

func TestNormalizeJSON_OrderedMapDates(t *testing.T) {
	t.Parallel()

	// A decode with yaml.UseOrderedMap names each member by the key of its
	// yaml.MapItem, and the lookup of the scalar a timestamp came from
	// names each key node the same way, so a date-only timestamp under a
	// key the decoder respells keeps its full-date spelling.
	tcs := map[string]struct {
		input string
		want  any
	}{
		"hexadecimal key": {
			input: "0x10: !!timestamp 2001-12-14\n",
			want:  map[string]any{"16": "2001-12-14"},
		},
		"null key": {
			input: "~: !!timestamp 2001-12-14\n",
			want:  map[string]any{"null": "2001-12-14"},
		},
		"bool-tagged key": {
			input: "!!bool yes: !!timestamp 2001-12-14\n",
			want:  map[string]any{"true": "2001-12-14"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input)

			data, err := doc.Decode[any](t.Context(), niceyaml.WithYAMLDecodeOptions(yaml.UseOrderedMap()))
			require.NoError(t, err)

			assert.Equal(t, tc.want, schema.NormalizeJSON(data, doc.AST()))
		})
	}
}

// assertCopiedOnChange asserts that each map and slice in input comes
// back at the same place in got as the same container when nothing under
// it holds a []byte, time.Time, or yaml.MapSlice, and as a different one
// otherwise. It reports whether input holds any of these types.
func assertCopiedOnChange(t *testing.T, input, got any) bool {
	t.Helper()

	changed := false

	switch v := input.(type) {
	case []byte, time.Time, yaml.MapSlice:
		return true

	case map[string]any:
		out, ok := got.(map[string]any)
		require.True(t, ok, "got %T for a map", got)

		for key, elem := range v {
			changed = assertCopiedOnChange(t, elem, out[key]) || changed
		}

	case []any:
		out, ok := got.([]any)
		require.True(t, ok, "got %T for a slice", got)
		require.Len(t, out, len(v))

		for i, elem := range v {
			changed = assertCopiedOnChange(t, elem, out[i]) || changed
		}

	default:
		return false
	}

	same := reflect.ValueOf(input).Pointer() == reflect.ValueOf(got).Pointer()
	assert.Equal(t, !changed, same, "whether %v comes back as the same container", input)

	return changed
}

func TestSchema_AliasExpansion(t *testing.T) {
	t.Parallel()

	// A decode shares an anchored map, slice, or !!binary between its
	// aliases, so each alias adds a full copy of the anchored value to the
	// data the validator would read, and each level of aliases nested in
	// aliases multiplies it. Validation rejects such a value before
	// checking it, and rejects a value that contains itself.
	t.Run("documents", func(t *testing.T) {
		t.Parallel()

		// Each level lists the level below ten times, so the last level
		// expands to 10^8 scalars.
		var bomb strings.Builder

		bomb.WriteString("l0: &l0 [x]\n")

		for level := 1; level <= 8; level++ {
			aliases := strings.Repeat(fmt.Sprintf("*l%d, ", level-1), 10)
			fmt.Fprintf(&bomb, "l%d: &l%d [%s]\n", level, level, strings.TrimSuffix(aliases, ", "))
		}

		// Each document from binaryAliases anchors size bytes as a
		// !!binary and lists count aliases of it.
		binaryAliases := func(size, count int) string {
			text := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xab}, size))
			list := strings.TrimSuffix(strings.Repeat("*bin, ", count), ", ")

			return fmt.Sprintf("bin: &bin !!binary %s\nlist: [%s]\n", text, list)
		}

		tcs := map[string]struct {
			schema string
			input  string
			err    string
			errs   []error
		}{
			"alias bomb": {
				schema: `{"type": "object"}`,
				input:  bomb.String(),
				errs:   []error{schema.ErrValidate, schema.ErrExcessiveAliasing},
			},
			"binary aliased many times": {
				// Each alias would add the 22 KB of base64 text again.
				schema: `{"type": "object"}`,
				input:  binaryAliases(16<<10, 1000),
				errs:   []error{schema.ErrValidate, schema.ErrExcessiveAliasing},
			},
			"binary aliased a few times": {
				// The aliases make up nine tenths of the base64 text, which
				// stays within the limit.
				schema: `{"properties": {"list": {"items": {"type": "string"}}}}`,
				input:  binaryAliases(4<<10, 10),
			},
			"anchor reused a few times": {
				schema: `{
					"type": "object",
					"additionalProperties": {
						"type": "object",
						"properties": {"a": {"type": "string"}}
					}
				}`,
				input: stringtest.Input(`
					base: &b {a: 1}
					x1: *b
					x2: *b
					x3: *b
				`),
				err: "4 schema violations",
			},
			"alias to a sibling in an anchor": {
				schema: `{
					"properties": {
						"base": {"properties": {"health": {"type": "integer"}}}
					}
				}`,
				input: "base: &b\n  port: &p 80\n  health: *p\n",
			},
			"merge keys": {
				schema: `{
					"type": "object",
					"additionalProperties": {
						"type": "object",
						"required": ["name", "replicas"]
					}
				}`,
				input: stringtest.Input(`
					defaults: &defaults {name: app, replicas: 1}
					dev:
					  <<: *defaults
					staging:
					  <<: *defaults
					  replicas: 2
					prod:
					  <<: *defaults
					  replicas: 3
				`),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				v := compileSchema(t, []byte(tc.schema))
				doc := yamltest.FirstDocument(t, tc.input)

				data, err := doc.Decode[any](t.Context(), niceyaml.WithAliasLimit(false))
				require.NoError(t, err)

				for _, err := range []error{doc.Validate(t.Context(), v), v.ValidateValue(t.Context(), data)} {
					switch {
					case tc.errs != nil:
						for _, want := range tc.errs {
							require.ErrorIs(t, err, want)
						}

					case tc.err != "":
						require.Error(t, err)
						require.NotErrorIs(t, err, schema.ErrValidate)
						assert.Contains(t, err.Error(), tc.err)

					default:
						require.NoError(t, err)
					}
				}
			})
		}
	})

	// The decoder writes out in full what an alias refers to when it
	// spells a key or a !!str value that holds one, and it reads a mapping
	// a merge key brings in again at every merge. Such a document costs
	// its expanded size while it decodes, and the decoded value may share
	// nothing, as when a later key replaces the member that held the
	// anchors. Each case runs Validate alone, since a decode of the
	// document would pay that cost.
	t.Run("decoded aliases", func(t *testing.T) {
		t.Parallel()

		// Each level lists the level below ten times, so the last level
		// expands to 10^7 scalars. The alias key replaces the member that
		// holds the levels, so the decoded value drops them.
		var lists strings.Builder

		lists.WriteString("k: &k a\na:\n  - &l0 [x]\n")

		for level := 1; level <= 7; level++ {
			aliases := strings.Repeat(fmt.Sprintf("*l%d, ", level-1), 10)
			fmt.Fprintf(&lists, "  - &l%d [%s]\n", level, strings.TrimSuffix(aliases, ", "))
		}

		lists.WriteString("*k : small\n")

		// Each level merges the level below ten times, so a decode reads
		// the first level 10^7 times.
		var merges strings.Builder

		merges.WriteString("m0: &m0 {a: x}\n")

		for level := 1; level <= 7; level++ {
			aliases := strings.Repeat(fmt.Sprintf("*m%d, ", level-1), 10)
			fmt.Fprintf(&merges, "m%d: &m%d\n  <<: [%s]\n", level, level, strings.TrimSuffix(aliases, ", "))
		}

		// The sequence k lists 500 aliases to a scalar of 2000 bytes.
		scalarAliases := "a: &a " + strings.Repeat("x", 2000) + "\n" +
			"k: &k [" + strings.TrimSuffix(strings.Repeat("*a, ", 500), ", ") + "]\n"

		v := compileSchema(t, []byte(`{"maxProperties": 5}`))

		tcs := map[string]struct {
			path  paths.Path
			input string
			errs  []error
		}{
			"alias bomb as mapping key": {
				input: lists.String() + "b:\n  ? *l7\n  : v\n",
				errs:  []error{schema.ErrValidate, schema.ErrExcessiveAliasing},
			},
			"alias bomb as flow mapping key": {
				input: lists.String() + "b: {*l7 : v}\n",
				errs:  []error{schema.ErrValidate, schema.ErrExcessiveAliasing},
			},
			"alias bomb under a string tag": {
				input: lists.String() + "b: !!str *l7\n",
				errs:  []error{schema.ErrValidate, schema.ErrExcessiveAliasing},
			},
			"merge key bomb": {
				input: merges.String(),
				errs:  []error{schema.ErrValidate, schema.ErrExcessiveAliasing},
			},
			"node holding an alias with a bomb outside it": {
				// A decode of a node that holds an alias reads the whole
				// document to find the anchor.
				path:  paths.Root().Child("c"),
				input: lists.String() + "b:\n  ? *l7\n  : v\nc: [*k]\n",
				errs:  []error{schema.ErrValidate, schema.ErrExcessiveAliasing},
			},
			"alias to a small sequence as mapping key": {
				input: "s: &s [a, b]\n*s : v\n",
			},
			// The key holds a copy of the long scalar for each alias.
			"scalar aliases written out in a key": {
				input: scalarAliases + "m: {? *k : v}\n",
				errs:  []error{schema.ErrValidate, schema.ErrExcessiveAliasing},
			},
			"scalar aliases in a value": {
				input: scalarAliases + "m: {n: *k}\n",
			},
			// The decoder reads an alias inside the content of its own
			// anchor as null, so each such alias reads one node.
			"aliases inside their own anchor": {
				input: "x: &x [" + strings.Repeat("a, ", 10) + strings.Repeat("*x, ", 299) + "*x]\n",
			},
			"aliases to the anchor of a mapping inside it": {
				input: "tree: &t\n  name: root\n  children:\n" +
					strings.Repeat("    - {name: c, parent: *t}\n", 120),
			},
			"aliases to an enclosing anchor inside a nested one": {
				input: "a: &A {inner: &B [" + strings.Repeat("*A, ", 299) + "*A]}\n",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, tc.input)
				if !tc.path.IsRoot() {
					doc = yamltest.At(t, doc, tc.path)
				}

				err := doc.Validate(t.Context(), v)
				if tc.errs == nil {
					require.NoError(t, err)

					return
				}

				for _, want := range tc.errs {
					require.ErrorIs(t, err, want)
				}
			})
		}

		// The document keeps its count for every Node of it, so each
		// Node that holds an alias gets the verdict of the whole document,
		// even when several check at once, and a Node without an alias
		// decodes on its own.
		t.Run("nodes of one document", func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, lists.String()+"b:\n  ? *l7\n  : v\nc: [*k]\nd: [*k]\ne: [x]\n")

			tcs := map[string]struct {
				path paths.Path
				errs []error
			}{
				"first node holding an alias": {
					path: paths.Root().Child("c"),
					errs: []error{schema.ErrValidate, schema.ErrExcessiveAliasing},
				},
				"second node holding an alias": {
					path: paths.Root().Child("d"),
					errs: []error{schema.ErrValidate, schema.ErrExcessiveAliasing},
				},
				"node without an alias": {
					path: paths.Root().Child("e"),
				},
			}

			for name, tc := range tcs {
				t.Run(name, func(t *testing.T) {
					t.Parallel()

					node := yamltest.At(t, doc, tc.path)

					for range 2 {
						err := node.Validate(t.Context(), v)
						if tc.errs == nil {
							require.NoError(t, err)

							continue
						}

						for _, want := range tc.errs {
							require.ErrorIs(t, err, want)
						}
					}
				})
			}
		})
	})

	t.Run("values", func(t *testing.T) {
		t.Parallel()

		v := compileSchema(t, []byte(`{
			"properties": {
				"a": {"type": "string"},
				"b": {"type": "string"}
			}
		}`))

		selfMap := map[string]any{}
		selfMap["self"] = selfMap

		selfSlice := make([]any, 1)
		selfSlice[0] = selfSlice

		outerMap := map[string]any{}
		outerMap["list"] = []any{outerMap}

		bin := []byte("hi")

		// Each level lists the level below ten times, so the last level
		// expands to 10^6 scalars.
		var shared any = []any{"x"}

		for range 6 {
			level := make([]any, 10)
			for i := range level {
				level[i] = shared
			}

			shared = level
		}

		tcs := map[string]struct {
			data      any
			err       error
			excessive bool
		}{
			"map containing itself": {
				data: selfMap,
				err:  schema.ErrValidate,
			},
			"slice containing itself": {
				data: selfSlice,
				err:  schema.ErrValidate,
			},
			"map containing itself through a slice": {
				data: outerMap,
				err:  schema.ErrValidate,
			},
			"bytes under two keys": {
				data: map[string]any{"a": bin, "b": bin},
			},
			// A key that is no string prints in full as the member name,
			// so it counts as a value does.
			"ordered mapping key containing itself": {
				data: yaml.MapSlice{{Key: selfSlice, Value: 1}},
				err:  schema.ErrValidate,
			},
			"ordered mapping key sharing a slice": {
				data:      yaml.MapSlice{{Key: shared, Value: 1}},
				err:       schema.ErrValidate,
				excessive: true,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := v.ValidateValue(t.Context(), tc.data)
				if tc.err == nil {
					require.NoError(t, err)

					return
				}

				require.ErrorIs(t, err, tc.err)

				if tc.excessive {
					require.ErrorIs(t, err, schema.ErrExcessiveAliasing)
				} else {
					assert.NotErrorIs(t, err, schema.ErrExcessiveAliasing)
				}
			})
		}
	})
}

func TestSchema_BooleanSchema(t *testing.T) {
	t.Parallel()

	// Boolean schemas are valid in JSON Schema. A true schema accepts
	// everything, and a false schema rejects everything.
	tcs := map[string]struct {
		input          []byte
		wantAcceptsAll bool
	}{
		"true schema accepts all data": {
			input:          []byte(`true`),
			wantAcceptsAll: true,
		},
		"false schema rejects all data": {
			input: []byte(`false`),
		},
	}

	testData := []any{"anything", 42, map[string]any{"key": "value"}}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			v := compileSchema(t, tc.input)

			for _, data := range testData {
				err := v.ValidateValue(t.Context(), data)
				if tc.wantAcceptsAll {
					assert.NoError(t, err)
				} else {
					assert.Error(t, err)
				}
			}
		})
	}
}

func TestSchema_SubErrorAnnotations(t *testing.T) {
	t.Parallel()

	// A single violation is the main error itself, and several violations
	// render as annotations with their own paths.
	tcs := map[string]struct {
		schema           string
		input            string
		wantAnnotations  []string // Substrings that should appear in annotation output.
		wantNestedErrors int
	}{
		"single violation has no nested errors": {
			schema: `{
				"type": "object",
				"properties": {
					"name": {"type": "string"}
				}
			}`,
			input: stringtest.Input(`
				name: 123
			`),
			wantAnnotations:  []string{`expected "string", got "integer"`},
			wantNestedErrors: 0,
		},
		"multiple sub-errors from required fields": {
			schema: `{
				"type": "object",
				"properties": {
					"first": {"type": "string"},
					"second": {"type": "string"}
				},
				"required": ["first", "second"]
			}`,
			input: stringtest.Input(`
				other: value
			`),
			wantAnnotations:  []string{"missing required property", "first", "second"},
			wantNestedErrors: 2,
		},
		"nested object sub-error": {
			schema: `{
				"type": "object",
				"properties": {
					"user": {
						"type": "object",
						"properties": {
							"age": {"type": "integer"}
						}
					}
				}
			}`,
			input: stringtest.Input(`
				user:
				  age: notanumber
			`),
			wantAnnotations:  []string{`expected "integer", got "string"`},
			wantNestedErrors: 0,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			v := compileSchema(t, []byte(tc.schema))

			source := niceyaml.NewSourceFromString(tc.input)
			d, err := source.Documents()
			require.NoError(t, err)

			for _, dd := range d {
				err = dd.Validate(t.Context(), v)
				require.Error(t, err)

				var bound *niceyaml.SourceError

				require.ErrorAs(t, err, &bound)

				errOutput := fmt.Sprintf("%+v", bound)

				for _, annotation := range tc.wantAnnotations {
					assert.Contains(t, errOutput, annotation,
						"expected error output to contain annotation text")
				}

				var validationErr *niceyaml.Error

				require.ErrorAs(t, err, &validationErr)

				// Unwrap includes the main error plus its nested errors.
				unwrapped := validationErr.Unwrap()
				nestedCount := len(unwrapped) - 1
				assert.Equal(t, tc.wantNestedErrors, nestedCount,
					"expected %d nested errors, got %d", tc.wantNestedErrors, nestedCount)
			}
		})
	}
}

func TestSchema_ErrorMessages(t *testing.T) {
	t.Parallel()

	// A single failure uses the concrete message once, and several use a summary.

	t.Run("single validation error uses concrete message once", func(t *testing.T) {
		t.Parallel()

		v := compileSchema(t, []byte(`{
			"type": "object",
			"properties": {
				"name": {"type": "string"}
			}
		}`))

		err := v.ValidateValue(t.Context(), map[string]any{"name": 123})
		require.Error(t, err)

		const msg = `expected "string", got "integer"`

		assert.Equal(t, 1, strings.Count(err.Error(), msg), "message should appear once: %q", err.Error())
		assert.NotContains(t, err.Error(), "violations")
	})

	t.Run("multiple validation errors use summary message", func(t *testing.T) {
		t.Parallel()

		v := compileSchema(t, []byte(`{
			"type": "object",
			"properties": {
				"name": {"type": "string"},
				"age": {"type": "number"}
			}
		}`))

		err := v.ValidateValue(t.Context(), map[string]any{"name": 123, "age": "thirty"})
		require.Error(t, err)

		assert.Contains(t, err.Error(), "2 schema violations")
		assert.NotContains(t, err.Error(), "failed")
	})
}

func TestSchema_ErrorPaths(t *testing.T) {
	t.Parallel()

	// A single violation puts its path on the main error. Several violations
	// leave the main error without a path and expose one path per nested
	// error through Unwrap.
	tcs := map[string]struct {
		schema          string
		input           any
		wantPath        string   // Expected path on the main error.
		wantNestedPaths []string // Expected paths from nested errors.
	}{
		"type error has path on main error": {
			schema:   `{"type": "object", "properties": {"name": {"type": "string"}}}`,
			input:    map[string]any{"name": 123},
			wantPath: "$.name",
		},
		"additional property has key path on main error": {
			schema:   `{"type": "object", "properties": {"name": {"type": "string"}}, "additionalProperties": false}`,
			input:    map[string]any{"name": "valid", "extra": "invalid"},
			wantPath: "$.extra~",
		},
		"nested validation error has path on main error": {
			schema:   `{"type": "object", "properties": {"user": {"type": "object", "properties": {"age": {"type": "integer"}}}}}`,
			input:    map[string]any{"user": map[string]any{"age": "notanumber"}},
			wantPath: "$.user.age",
		},
		"several violations have paths on nested errors": {
			schema:          `{"type": "object", "properties": {"name": {"type": "string"}, "age": {"type": "number"}}}`,
			input:           map[string]any{"name": 123, "age": "thirty"},
			wantNestedPaths: []string{"$.name", "$.age"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			v := compileSchema(t, []byte(tc.schema))

			err := v.ValidateValue(t.Context(), tc.input)
			require.Error(t, err)

			var validationErr *niceyaml.Error

			require.ErrorAs(t, err, &validationErr)

			gotPath, ok := validationErr.Path()
			assert.Equal(t, tc.wantPath != "", ok)

			if ok {
				assert.Equal(t, tc.wantPath, gotPath.String())
			}

			var gotNestedPaths []string

			for _, uerr := range validationErr.Unwrap() {
				nestedErr, ok := errors.AsType[*niceyaml.Error](uerr)
				if !ok {
					continue
				}

				if nestedPath, ok := nestedErr.Path(); ok {
					gotNestedPaths = append(gotNestedPaths, nestedPath.String())
				}
			}

			assert.ElementsMatch(t, tc.wantNestedPaths, gotNestedPaths)
		})
	}
}

func TestSchema_Validate_Scope(t *testing.T) {
	t.Parallel()

	v := compileSchema(t, []byte(`{
		"type": "object",
		"properties": {
			"replicas": {"type": "integer"},
			"16": {"type": "integer"}
		},
		"additionalProperties": false
	}`))

	spec := paths.Root().Child("spec")

	t.Run("violation resolves from the node", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, stringtest.Input(`
			replicas: 1
			spec:
			  replicas: many
		`))

		err := yamltest.At(t, dd, spec).Validate(t.Context(), v)

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)

		rng, ok := bound.Range()
		require.True(t, ok)
		assert.Equal(t, position.New(2, 12), rng.Start)
		assert.Equal(t, "3:13: $.replicas: expected \"integer\", got \"string\"", bound.Error())

		// The whole document conforms where the node does not.
		require.NoError(t, dd.Validate(t.Context(), compileSchema(t, []byte(`{
			"type": "object",
			"properties": {"replicas": {"type": "integer"}}
		}`))))
	})

	t.Run("decoded key resolves from the node", func(t *testing.T) {
		t.Parallel()

		// The key decodes to the member name 16, which the source spells
		// 0x10, so the path names the member by its source spelling and
		// resolves from the node.
		dd := yamltest.FirstDocument(t, stringtest.Input(`
			0x10: 1
			spec:
			  0x10: hello
		`))

		err := yamltest.At(t, dd, spec).Validate(t.Context(), v)

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)

		rng, ok := bound.Range()
		require.True(t, ok)
		assert.Equal(t, position.New(2, 8), rng.Start)
		assert.Equal(t, "3:9: $.0x10: expected \"integer\", got \"string\"", bound.Error())
	})

	t.Run("decoded key behind an alias resolves from the node", func(t *testing.T) {
		t.Parallel()

		// The alias inside the node refers to an anchor outside it, and
		// the path still names the member by its source spelling.
		dd := yamltest.FirstDocument(t, stringtest.Input(`
			a: &a {0x10: 1}
			b:
			  c: *a
		`))

		aliased := compileSchema(t, []byte(`{
			"type": "object",
			"properties": {
				"c": {"additionalProperties": {"type": "string"}}
			}
		}`))

		err := yamltest.At(t, dd, paths.Root().Child("b")).Validate(t.Context(), aliased)

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)

		rng, ok := bound.Range()
		require.True(t, ok)
		assert.Equal(t, position.New(0, 13), rng.Start)
		assert.Equal(t, "1:14: $.c.0x10: expected \"string\", got \"integer\"", bound.Error())
	})

	t.Run("additional property resolves from the node", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, stringtest.Input(`
			extra: 1
			spec:
			  extra: 1
		`))

		err := yamltest.At(t, dd, spec).Validate(t.Context(), v)

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)

		rng, ok := bound.Range()
		require.True(t, ok)
		assert.Equal(t, position.New(2, 2), rng.Start)
	})
}

func TestSchema_SourcePath(t *testing.T) {
	t.Parallel()

	// A violation's path spells each key as the source does, so a key the
	// decoder respells, such as 0x10 for the member name 16, still names
	// the member. The path prints in the error and resolves with Node.At.
	tcs := map[string]struct {
		schema   string
		input    string
		wantPath string
		want     string
	}{
		"hexadecimal key": {
			schema: `{
				"type": "object",
				"properties": {"16": {"type": "integer"}}
			}`,
			input:    "0x10: hello\n",
			wantPath: "$.0x10",
			want:     "1:7: $.0x10: expected \"integer\", got \"string\"",
		},
		"tilde null key": {
			schema: `{
				"type": "object",
				"additionalProperties": {"type": "string"}
			}`,
			input:    "~: 5\n",
			wantPath: "$.'~'",
			want:     "1:4: $.'~': expected \"string\", got \"integer\"",
		},
		"merge key ahead of a null key": {
			schema: `{
				"type": "object",
				"additionalProperties": {"type": ["string", "object"]}
			}`,
			input:    "<<: {a: x}\n~: 5\n",
			wantPath: "$.'~'",
			want:     "2:4: $.'~': expected [\"string\", \"object\"], got \"integer\"",
		},
		"merge key ahead of an empty key": {
			schema: `{
				"type": "object",
				"additionalProperties": {"type": ["string", "object"]}
			}`,
			input:    "<<: {a: x}\n\"\": 5\n",
			wantPath: "$.''",
			want:     "2:5: $.'': expected [\"string\", \"object\"], got \"integer\"",
		},
		"merge key overrides an earlier respelled key": {
			// The decoder applies the merge after 0x10: 1, so the member
			// 16 holds x, and the path names the key the merge brings in.
			schema: `{
				"type": "object",
				"properties": {
					"user": {"additionalProperties": {"type": "integer"}}
				}
			}`,
			input:    "user:\n  0x10: 1\n  <<: {16: x}\n",
			wantPath: "$.user.16",
			want:     "3:12: $.user.16: expected \"integer\", got \"string\"",
		},
		"merge key overrides an earlier key of the same spelling": {
			// A path through 0x10 selects the later entry, which the
			// merge brings in.
			schema: `{
				"type": "object",
				"properties": {
					"user": {"additionalProperties": {"type": "integer"}}
				}
			}`,
			input:    "user:\n  0x10: 1\n  <<: {0x10: x}\n",
			wantPath: "$.user.0x10",
			want:     "3:14: $.user.0x10: expected \"integer\", got \"string\"",
		},
		"respelled key a merge key brings in": {
			schema: `{
				"type": "object",
				"properties": {
					"user": {"additionalProperties": {"type": "integer"}}
				}
			}`,
			input:    "user:\n  <<: {0x10: x}\n",
			wantPath: "$.user.0x10",
			want:     "2:14: $.user.0x10: expected \"integer\", got \"string\"",
		},
		"spelled-out null key": {
			schema: `{
				"type": "object",
				"additionalProperties": {"type": "string"}
			}`,
			input:    "NULL: 5\n",
			wantPath: "$.NULL",
			want:     "1:7: $.NULL: expected \"string\", got \"integer\"",
		},
		"boolean key targeted by the failure": {
			schema: `{
				"type": "object",
				"properties": {"name": {"type": "string"}},
				"additionalProperties": false
			}`,
			input:    "True: nope\n",
			wantPath: "$.True~",
			want:     "1:1: $.True~: value is not allowed",
		},
		"bool-tagged key": {
			schema: `{
				"type": "object",
				"additionalProperties": {"type": "string"}
			}`,
			input:    "!!bool yes: 5\n",
			wantPath: "$.yes",
			want:     "1:13: $.yes: expected \"string\", got \"integer\"",
		},
		"null-tagged key": {
			schema: `{
				"type": "object",
				"additionalProperties": {"type": "string"}
			}`,
			input:    "!!null x: 5\n",
			wantPath: "$.x",
			want:     "1:11: $.x: expected \"string\", got \"integer\"",
		},
		"null-tagged empty key": {
			// The key decodes to null, and the path spells it as the
			// empty key the source writes.
			schema: `{
				"type": "object",
				"additionalProperties": {"type": "string"}
			}`,
			input:    "!!null \"\": 5\n",
			wantPath: "$.''",
			want:     "1:12: $.'': expected \"string\", got \"integer\"",
		},
		"int-tagged quoted key": {
			schema: `{
				"type": "object",
				"additionalProperties": {"type": "string"}
			}`,
			input:    "!!int \"0x10\": 5\n",
			wantPath: "$.0x10",
			want:     "1:15: $.0x10: expected \"string\", got \"integer\"",
		},
		"timestamp-tagged key": {
			schema: `{
				"type": "object",
				"additionalProperties": {"type": "string"}
			}`,
			input:    "? !!timestamp 2001-01-01\n: 5\n",
			wantPath: "$.2001-01-01",
			want:     "2:3: $.2001-01-01: expected \"string\", got \"integer\"",
		},
		"float key": {
			schema: `{
				"type": "object",
				"additionalProperties": {"type": "string"}
			}`,
			input:    "1.5: 5\n",
			wantPath: "$.'1.5'",
			want:     "1:6: $.'1.5': expected \"string\", got \"integer\"",
		},
		"two keys decoding to one name": {
			// Both keys decode to 1, and the decoder keeps the later
			// member, so the path names the later key.
			schema: `{
				"type": "object",
				"additionalProperties": {"type": "string"}
			}`,
			input:    "1.0: x\n1: 2\n",
			wantPath: "$.1",
			want:     "2:4: $.1: expected \"string\", got \"integer\"",
		},
		"key with a dot": {
			schema: `{
				"type": "object",
				"additionalProperties": {"type": "string"}
			}`,
			input:    "a.b: 5\n",
			wantPath: "$.'a.b'",
			want:     "1:6: $.'a.b': expected \"string\", got \"integer\"",
		},
		"key with a space": {
			schema: `{
				"type": "object",
				"additionalProperties": {"type": "string"}
			}`,
			input:    "a b: 5\n",
			wantPath: "$.'a b'",
			want:     "1:6: $.'a b': expected \"string\", got \"integer\"",
		},
		"nested respelled key": {
			schema: `{
				"type": "object",
				"properties": {
					"spec": {
						"type": "object",
						"properties": {"16": {"type": "integer"}}
					}
				}
			}`,
			input:    "spec:\n  0x10: hello\n",
			wantPath: "$.spec.0x10",
			want:     "2:9: $.spec.0x10: expected \"integer\", got \"string\"",
		},
		"respelled key inside a sequence": {
			schema: `{
				"type": "object",
				"properties": {
					"items": {
						"type": "array",
						"items": {
							"type": "object",
							"properties": {"16": {"type": "integer"}}
						}
					}
				}
			}`,
			input:    "items:\n  - 0x10: 1\n  - 0x10: hello\n",
			wantPath: "$.items[1].0x10",
			want:     "3:11: $.items[1].0x10: expected \"integer\", got \"string\"",
		},
		"alias key": {
			schema: `{
				"type": "object",
				"additionalProperties": {"type": "string"}
			}`,
			input:    "a: &k name\n*k : 5\n",
			wantPath: "$.name",
			want:     "2:6: $.name: expected \"string\", got \"integer\"",
		},
		"key named like an alias": {
			// The alias *k decodes to the key name, so the member k is
			// the entry k: 1.
			schema: `{
				"type": "object",
				"properties": {"k": {"type": "string"}}
			}`,
			input:    "a: &k name\nk: 1\n*k : v\n",
			wantPath: "$.k",
			want:     "2:4: $.k: expected \"string\", got \"integer\"",
		},
		"alias key overrides an earlier key": {
			// The decoder keeps the member the alias key sets, so the
			// path spells the keys below it.
			schema: `{
				"type": "object",
				"properties": {
					"name": {"additionalProperties": {"type": "integer"}}
				}
			}`,
			input:    "a: &k name\nname: {16: x}\n*k : {0x10: y}\n",
			wantPath: "$.name.0x10",
			want:     "3:13: $.name.0x10: expected \"integer\", got \"string\"",
		},
		"alias key overrides a respelled key": {
			schema: `{
				"type": "object",
				"properties": {"16": {"type": "integer"}}
			}`,
			input:    "a: &k 16\n0x10: hello\n*k : [1]\n",
			wantPath: "$.16",
			want:     "3:7: $.16: expected \"integer\", got \"array\"",
		},
		"block scalar key": {
			schema: `{
				"type": "object",
				"additionalProperties": {"type": "string"}
			}`,
			input:    "? |-\n  n\n: 5\n",
			wantPath: "$.n",
			want:     "3:3: $.n: expected \"string\", got \"integer\"",
		},
		"respelled key behind an alias": {
			schema: `{
				"type": "object",
				"properties": {
					"b": {"additionalProperties": {"type": "string"}}
				}
			}`,
			input:    "a: &a {0x10: 1}\nb: *a\n",
			wantPath: "$.b.0x10",
			want:     "1:14: $.b.0x10: expected \"string\", got \"integer\"",
		},
		"respelled key behind an alias to a block mapping": {
			schema: `{
				"type": "object",
				"properties": {
					"b": {"additionalProperties": {"type": "string"}}
				}
			}`,
			input:    "a: &a\n  0x10: 1\nb: *a\n",
			wantPath: "$.b.0x10",
			want:     "2:9: $.b.0x10: expected \"string\", got \"integer\"",
		},
		"respelled key behind a redefined anchor": {
			// The alias refers to the last anchor of its name before it.
			schema: `{
				"type": "object",
				"properties": {
					"c": {"additionalProperties": {"type": "string"}}
				}
			}`,
			input:    "a: &a {16: x}\nb: &a {0x10: 1}\nc: *a\n",
			wantPath: "$.c.0x10",
			want:     "2:14: $.c.0x10: expected \"string\", got \"integer\"",
		},
		"respelled key behind an anchor a merge brings in again": {
			// The decoder reads the anchors of a merged mapping again at
			// the merge key, so *x refers to the &x inside base.
			schema: `{
				"type": "object",
				"properties": {
					"m": {
						"properties": {
							"v": {"additionalProperties": {"type": "string"}}
						}
					}
				}
			}`,
			input:    "base: &b\n  k: &x {0x10: 1}\nother: &x {16: x}\nm:\n  <<: *b\n  v: *x\n",
			wantPath: "$.m.v.0x10",
			want:     "2:16: $.m.v.0x10: expected \"string\", got \"integer\"",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			v := compileSchema(t, []byte(tc.schema))
			dd := yamltest.FirstDocument(t, tc.input)

			err := dd.Validate(t.Context(), v)

			var bound *niceyaml.SourceError

			require.ErrorAs(t, err, &bound)

			gotPath, ok := bound.Path()
			require.True(t, ok, "bound error carries no path")
			assert.Equal(t, tc.wantPath, gotPath.String())
			assert.Equal(t, tc.want, bound.Error())

			_, err = dd.At(gotPath)
			require.NoError(t, err, "path from the error does not resolve")
		})
	}
}

func TestSchema_SourcePath_HiddenMergedKey(t *testing.T) {
	t.Parallel()

	// The quoted key decodes to 0x10 rather than 16, and a path through
	// 0x10 selects it rather than the merged key, so the path of the
	// merged member keeps its decoded name and points nowhere.
	v := compileSchema(t, []byte(`{
		"type": "object",
		"properties": {
			"user": {"additionalProperties": {"type": "integer"}}
		}
	}`))
	dd := yamltest.FirstDocument(t, "user:\n  <<: {0x10: x}\n  \"0x10\": 1\n")

	err := dd.Validate(t.Context(), v)

	var bound *niceyaml.SourceError

	require.ErrorAs(t, err, &bound)
	assert.Equal(t, "$.user.16: expected \"integer\", got \"string\"", bound.Error())
}

func TestSchema_SourcePath_SeveralViolations(t *testing.T) {
	t.Parallel()

	// Several violations lie under one mapping, and each path spells the
	// key of the member the decode keeps. The cases cover the later of two
	// keys that decode to 1, an explicit key written before a merge key, and
	// an alias key, which the path spells by the content of its anchor.
	v := compileSchema(t, []byte(`{"additionalProperties": {"type": "integer"}}`))

	dd := yamltest.FirstDocument(t, stringtest.Input(`
		base: &b {m: 1}
		0x10: a
		<<: *b
		1.0: b
		1: c
		a: &k name
		*k : d
	`))

	err := dd.Validate(t.Context(), v)

	var bound *niceyaml.SourceError

	require.ErrorAs(t, err, &bound)

	var got []string

	for _, child := range bound.Errors() {
		path, ok := child.Path()
		require.True(t, ok, "violation carries no path")

		_, err := dd.At(path)
		require.NoError(t, err, "path %s does not resolve", path)

		got = append(got, path.String())
	}

	assert.ElementsMatch(t, []string{"$.base", "$.0x10", "$.1", "$.a", "$.name"}, got)
}

func TestSchema_RefToRejectingSchema(t *testing.T) {
	t.Parallel()

	// A $ref that resolves to a schema allowing nothing fails as a
	// violation of the document, at the value the reference applies to,
	// and not as a reference the validator could not resolve.
	tcs := map[string]string{
		"false":  `{"$defs": {"never": false}, "properties": {"a": {"$ref": "#/$defs/never"}}}`,
		"not":    `{"$defs": {"never": {"not": {}}}, "properties": {"a": {"$ref": "#/$defs/never"}}}`,
		"chain":  `{"$defs": {"never": false, "via": {"$ref": "#/$defs/never"}}, "properties": {"a": {"$ref": "#/$defs/via"}}}`,
		"direct": `{"properties": {"a": false}}`,
	}

	for name, schemaData := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			v, err := schema.Compile(t.Context(), []byte(schemaData))
			require.NoError(t, err)

			err = v.Validate(t.Context(), yamltest.FirstDocument(t, "a: 1\n"))
			require.NotErrorIs(t, err, schema.ErrValidate)

			var nerr *niceyaml.Error

			require.ErrorAs(t, err, &nerr)

			path, ok := nerr.Path()
			require.True(t, ok)
			assert.Equal(t, "$.a", path.String())
		})
	}
}

func TestFromJSONSchema(t *testing.T) {
	t.Parallel()

	t.Run("wraps a compiled validator", func(t *testing.T) {
		t.Parallel()

		s := schema.FromJSONSchema(jsonschema.MustCompileJSON([]byte(`{"type": "object"}`)))

		require.NoError(t, s.ValidateValue(t.Context(), map[string]any{"key": "value"}))
		require.Error(t, s.ValidateValue(t.Context(), "value"))
	})

	t.Run("nil validator panics", func(t *testing.T) {
		t.Parallel()

		assert.PanicsWithValue(t, "schema.FromJSONSchema: validator is nil", func() {
			schema.FromJSONSchema(nil)
		})
	})
}

func TestSchema_Ref(t *testing.T) {
	t.Parallel()

	s := schema.MustCompile([]byte(`{"type": "object"}`))

	ref, err := s.Resolve(t.Context(), yamltest.FirstDocument(t, "key: value\n"))
	require.NoError(t, err)
	assert.Same(t, s, ref.Schema())
	assert.Equal(t, s.Ref(), ref)
	assert.Empty(t, ref.Key())

	_, err = schema.NewRegistry().Load(t.Context(), ref)
	require.ErrorIs(t, err, schema.ErrLoad)

	assert.Nil(t, schema.Embedded([]byte(`{}`)).Schema())
	assert.Nil(t, schema.Ref{}.Schema())
}
