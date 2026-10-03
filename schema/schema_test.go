package schema_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
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
			// A missing required property carries the path it would have,
			// which binds at the key of the mapping that lacks it.
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
	// date has without the tag, also behind an alias or a merge key.
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
		"merged date hidden by a same-spelled key stays a date": {
			// The quoted key decodes to the member 0x10, so the merge
			// still sets the member 16.
			schema: `{
				"type": "object",
				"properties": {
					"m": {
						"type": "object",
						"properties": {"16": {"type": "string", "format": "date"}}
					}
				}
			}`,
			input: stringtest.Input(`
				m: {<<: {0x10: !!timestamp 2001-12-14}, "0x10": 5}
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
				assert.Contains(t, niceyaml.FormatError(err, 0), tc.err)

			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestSchema_Validate_OrderedMapDates(t *testing.T) {
	t.Parallel()

	// The yaml.UseOrderedMap option reaches the decode that gets it and
	// not the decode the schema runs, so the schema reads plain maps, and a
	// date-only timestamp under a key the decoder respells keeps its
	// full-date spelling.
	v, err := schema.Compile(t.Context(),
		[]byte(`{"additionalProperties": {"type": "string", "format": "date"}}`),
		schema.WithJSONSchemaOptions(jsonschema.WithFormats(true)))
	require.NoError(t, err)

	tcs := map[string]struct {
		input string
	}{
		"hexadecimal key": {input: "0x10: !!timestamp 2001-12-14\n"},
		"null key":        {input: "~: !!timestamp 2001-12-14\n"},
		"bool-tagged key": {input: "!!bool yes: !!timestamp 2001-12-14\n"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input)

			_, err := doc.Decode[any](t.Context(),
				niceyaml.WithYAMLDecodeOptions(yaml.UseOrderedMap()),
				niceyaml.WithValidator(v),
			)
			require.NoError(t, err)
		})
	}
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
				input:  yamltest.AliasLevels(8),
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

		// The alias key *k spells a, so it replaces the member that holds
		// the levels, and the decoded value drops them.
		lists := "k: &k a\n" + yamltest.AliasLevels(7) + "*k : small\n"

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
				input: lists + "b:\n  ? *l7\n  : v\n",
				errs:  []error{schema.ErrValidate, schema.ErrExcessiveAliasing},
			},
			"alias bomb as flow mapping key": {
				input: lists + "b: {*l7 : v}\n",
				errs:  []error{schema.ErrValidate, schema.ErrExcessiveAliasing},
			},
			"alias bomb under a string tag": {
				input: lists + "b: !!str *l7\n",
				errs:  []error{schema.ErrValidate, schema.ErrExcessiveAliasing},
			},
			"merge key bomb": {
				input: yamltest.MergeLevels(7),
				errs:  []error{schema.ErrValidate, schema.ErrExcessiveAliasing},
			},
			"node holding an alias with a bomb outside it": {
				// A decode of a node that holds an alias reads the whole
				// document to find the anchor.
				path:  paths.Root().Child("c"),
				input: lists + "b:\n  ? *l7\n  : v\nc: [*k]\n",
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

			bomb := yamltest.FirstDocument(t, lists+"b:\n  ? *l7\n  : v\nc: [*k]\nd: [*k]\ne: [x]\n")

			// Each flow sequence from repeated lists item count times.
			repeated := func(item string, count int) string {
				return "[" + strings.TrimSuffix(strings.Repeat(item+", ", count), ", ") + "]"
			}

			// The 200 aliases in list repeat a sequence of 1000 numbers, so
			// they make up more of list than the limit allows. The 2000
			// numbers in filler keep their share of the document within it.
			diluted := yamltest.FirstDocument(t,
				"filler: "+repeated("0", 2000)+"\n"+
					"big: &big "+repeated("0", 1000)+"\n"+
					"list: "+repeated("*big", 200)+"\n",
			)

			// The 1000 aliases in list each repeat 22 KB of base64 text,
			// and other holds one more.
			binary := yamltest.FirstDocument(t,
				"bin: &bin !!binary "+base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xab}, 16<<10))+"\n"+
					"list: "+repeated("*bin", 1000)+"\n"+
					"other: [*bin]\n",
			)

			// The aliases repeat the same text through the tagged alias
			// under s.
			chained := yamltest.FirstDocument(t,
				"bin: &bin !!binary "+base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xab}, 16<<10))+"\n"+
					"s: &s !foo *bin\n"+
					"list: "+repeated("*s", 1000)+"\n"+
					"other: [*s]\n",
			)

			// The aliases repeat the same text, which lies under another
			// tag and anchor of bin.
			wrapped := yamltest.FirstDocument(t,
				"bin: &bin !foo &raw !!binary "+
					base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xab}, 16<<10))+"\n"+
					"list: "+repeated("*bin", 1000)+"\n"+
					"other: [*bin]\n",
			)

			tcs := map[string]struct {
				doc  *niceyaml.Node
				path paths.Path
				errs []error
			}{
				"first node holding an alias": {
					doc:  bomb,
					path: paths.Root().Child("c"),
					errs: []error{schema.ErrValidate, schema.ErrExcessiveAliasing},
				},
				"second node holding an alias": {
					doc:  bomb,
					path: paths.Root().Child("d"),
					errs: []error{schema.ErrValidate, schema.ErrExcessiveAliasing},
				},
				"node without an alias": {
					doc:  bomb,
					path: paths.Root().Child("e"),
				},
				"document diluting its aliases": {
					doc: diluted,
				},
				"node of a document diluting its aliases": {
					doc:  diluted,
					path: paths.Root().Child("list"),
				},
				"node with one alias to a binary aliased many times": {
					doc:  binary,
					path: paths.Root().Child("other"),
					errs: []error{schema.ErrValidate, schema.ErrExcessiveAliasing},
				},
				"document with aliases to a tag over an alias to a binary": {
					doc:  chained,
					errs: []error{schema.ErrValidate, schema.ErrExcessiveAliasing},
				},
				"node of aliases to a tag over an alias to a binary": {
					doc:  chained,
					path: paths.Root().Child("list"),
					errs: []error{schema.ErrValidate, schema.ErrExcessiveAliasing},
				},
				"node with one alias to a tag over an alias to a binary": {
					doc:  chained,
					path: paths.Root().Child("other"),
					errs: []error{schema.ErrValidate, schema.ErrExcessiveAliasing},
				},
				"document with aliases to a binary under another tag": {
					doc:  wrapped,
					errs: []error{schema.ErrValidate, schema.ErrExcessiveAliasing},
				},
				"node with one alias to a binary under another tag": {
					doc:  wrapped,
					path: paths.Root().Child("other"),
					errs: []error{schema.ErrValidate, schema.ErrExcessiveAliasing},
				},
			}

			for name, tc := range tcs {
				t.Run(name, func(t *testing.T) {
					t.Parallel()

					node := tc.doc
					if !tc.path.IsRoot() {
						node = yamltest.At(t, node, tc.path)
					}

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

	// The count of a document cannot see the anchors of a reference
	// document, so it takes each alias to one as one node. Validate then
	// applies the limit to the value the decode shares between them.
	t.Run("reference aliases", func(t *testing.T) {
		t.Parallel()

		var entries strings.Builder

		for i := range 1000 {
			fmt.Fprintf(&entries, "  k%d: v\n", i)
		}

		refs := niceyaml.WithReferences(niceyaml.NewSourceFromString("defaults: &defaults\n" + entries.String()))
		v := compileSchema(t, []byte(`{"maxProperties": 5}`))

		// Each flow sequence from repeated lists *defaults count times.
		repeated := func(count int) string {
			return "[" + strings.TrimSuffix(strings.Repeat("*defaults, ", count), ", ") + "]"
		}

		tcs := map[string]struct {
			path  paths.Path
			input string
			errs  []error
		}{
			"aliases to a mapping of a reference document": {
				input: "items: " + repeated(300) + "\n",
				errs:  []error{schema.ErrValidate, schema.ErrExcessiveAliasing},
			},
			"node of aliases to a mapping of a reference document": {
				path:  paths.Root().Child("items"),
				input: "items: " + repeated(300) + "\n",
				errs:  []error{schema.ErrValidate, schema.ErrExcessiveAliasing},
			},
			"aliases to an anchor on a tagged alias to a reference document": {
				input: "local: &local !foo *defaults\nitems: " +
					strings.ReplaceAll(repeated(300), "*defaults", "*local") + "\n",
				errs: []error{schema.ErrValidate, schema.ErrExcessiveAliasing},
			},
			"a few aliases to a mapping of a reference document": {
				input: "items: " + repeated(2) + "\n",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, tc.input, refs)
				if !tc.path.IsRoot() {
					doc = yamltest.At(t, doc, tc.path)
				}

				_, err := doc.Decode[any](t.Context(), niceyaml.WithValidator(v))
				if tc.errs == nil {
					require.NoError(t, err)

					return
				}

				for _, want := range tc.errs {
					require.ErrorIs(t, err, want)
				}
			})
		}
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
		assert.Equal(t, "3:13: $.spec.replicas: expected \"integer\", got \"string\"", bound.Error())

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
		assert.Equal(t, "3:9: $.spec.0x10: expected \"integer\", got \"string\"", bound.Error())
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
		assert.Equal(t, "1:14: $.b.c.0x10: expected \"string\", got \"integer\"", bound.Error())
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

func TestSchema_Validate_SyntaxError(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		schema string
		// The error of the document that parsed, empty when it passes.
		err string
	}{
		"schema that would reject the data": {
			schema: `{"type": "string"}`,
			err:    `1:1: $: expected "string", got "object"`,
		},
		"schema that accepts all data": {
			schema: `true`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			v := compileSchema(t, []byte(tc.schema))

			docs, err := niceyaml.NewSourceFromString("a: 1\n---\nb: [\n").Documents()
			require.Error(t, err)
			require.Len(t, docs, 2)

			// The second document did not parse, so the schema has no data
			// to check and returns the syntax error.
			assert.Same(t, docs[1].Err(), v.Validate(t.Context(), docs[1]))
			assert.Same(t, docs[1].Err(), docs[1].Validate(t.Context(), v))

			// The first document parsed, so the schema checks it.
			err = v.Validate(t.Context(), docs[0])
			if tc.err == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.err)
			}
		})
	}
}

func TestSchema_Validate_Bound(t *testing.T) {
	t.Parallel()

	v := compileSchema(t, []byte(`{
		"type": "object",
		"properties": {
			"name": {"type": "string"},
			"price": {"type": "number", "minimum": 0}
		}
	}`))

	menu := stringtest.Input(`
		price: 5
		items:
		  - price: 1
		  - price: -2
	`)
	itemPath := paths.Root().Child("items").Index(1)

	tcs := map[string]struct {
		input string
		path  paths.Path
		// The message of the error, and the errors it matches.
		want string
		errs []error
	}{
		"a document that conforms": {
			input: menu,
		},
		"a violation at the root": {
			input: "price: -1\n",
			want:  "menu.yaml:1:8: $.price: -1 is less than 0",
		},
		"a violation in a scoped node": {
			input: menu,
			path:  itemPath,
			want:  "menu.yaml:4:12: $.items[1].price: -2 is less than 0",
		},
		"several violations": {
			input: "name: 1\nprice: -1\n",
			want: stringtest.JoinLF(
				"menu.yaml: 2 schema violations",
				`menu.yaml:1:7: $.name: expected "string", got "integer"`,
				"menu.yaml:2:8: $.price: -1 is less than 0",
			),
		},
		// The summary stands above violations that say where they point,
		// so the scoped node gives it no location.
		"several violations in a scoped node": {
			input: "items:\n  - name: 1\n    price: -2\n",
			path:  paths.Root().Child("items").Index(0),
			want: stringtest.JoinLF(
				"menu.yaml: 2 schema violations",
				`menu.yaml:2:11: $.items[0].name: expected "string", got "integer"`,
				"menu.yaml:3:12: $.items[0].price: -2 is less than 0",
			),
		},
		"a decoding error": {
			input: "price: *nope\n",
			want:  "menu.yaml:1:8: $.price: could not find alias \"nope\"",
		},
		"a document past the alias limit": {
			input: yamltest.AliasLevels(7),
			want:  "menu.yaml: validate schema: excessive aliasing",
			errs:  []error{schema.ErrValidate, schema.ErrExcessiveAliasing},
		},
		// The error carries no location, so the scoped node takes it.
		"a scoped node past the alias limit": {
			input: yamltest.AliasLevels(7),
			path:  paths.Root().Child("a"),
			want:  "menu.yaml:2:10: $.a: validate schema: excessive aliasing",
			errs:  []error{schema.ErrValidate, schema.ErrExcessiveAliasing},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			node := yamltest.FirstDocumentWithPath(t, tc.input, "menu.yaml")
			if !tc.path.IsRoot() {
				node = yamltest.At(t, node, tc.path)
			}

			err := v.Validate(t.Context(), node)
			if tc.want == "" {
				require.NoError(t, err)

				return
			}

			require.EqualError(t, err, tc.want)

			for _, target := range tc.errs {
				require.ErrorIs(t, err, target)
			}

			var bound *niceyaml.SourceError

			require.ErrorAs(t, err, &bound)
			assert.Same(t, node, bound.Node())

			// The node returns the same error for the schema.
			require.EqualError(t, node.Validate(t.Context(), v), tc.want)
		})
	}

	t.Run("a validator that runs the schema on each element reports the element", func(t *testing.T) {
		t.Parallel()

		each := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
			items, err := n.Nodes(paths.Root().Child("items").IndexAll())
			if err != nil {
				return err //nolint:wrapcheck // The test inspects the error as it is.
			}

			for _, item := range items {
				err := v.Validate(ctx, item)
				if err != nil {
					return err //nolint:wrapcheck // The test inspects the error as it is.
				}
			}

			return nil
		})

		doc := yamltest.FirstDocumentWithPath(t, menu, "menu.yaml")

		err := doc.Validate(t.Context(), each)
		require.EqualError(t, err, "menu.yaml:4:12: $.items[1].price: -2 is less than 0")

		var violation *schema.Violation

		require.ErrorAs(t, err, &violation)
		assert.Equal(t, "minimum", violation.Keyword)
	})

	t.Run("a validator that checks another document names that document", func(t *testing.T) {
		t.Parallel()

		other := yamltest.FirstDocumentWithPath(t, "# other\nprice: -1\n", "other.yaml")
		include := niceyaml.ValidatorFunc(func(ctx context.Context, _ *niceyaml.Node) error {
			return v.Validate(ctx, other)
		})

		err := yamltest.FirstDocumentWithPath(t, menu, "menu.yaml").Validate(t.Context(), include)
		require.EqualError(t, err, "other.yaml:2:8: $.price: -1 is less than 0")
	})
}

func TestSchema_SourcePath(t *testing.T) {
	t.Parallel()

	// A violation's path spells each key as the source does, so a key the
	// decoder respells, such as 0x10 for the member name 16, still names
	// the member. The path prints in the error and resolves with Node.At.
	// Where the walk cannot tell which key in the source names the member,
	// the path keeps the decoded name, which Node.At does not resolve. The
	// error binds at the node of the member where the walk reached it, and
	// at the key of the mapping that holds the member where the walk did
	// not. Such a case sets unresolved.
	tcs := map[string]struct {
		schema     string
		input      string
		wantPath   string
		want       string
		unresolved bool
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
		"later key shadows a merged member": {
			// The decoder sets 0x10: x after the merge, so the member 16
			// holds x rather than the 5 the merge brings in.
			schema: `{
				"type": "object",
				"properties": {
					"m": {"additionalProperties": {"type": "integer"}}
				}
			}`,
			input:    "b: &b {16: 5}\nm: {<<: *b, 0x10: x}\n",
			wantPath: "$.m.0x10",
			want:     "2:19: $.m.0x10: expected \"integer\", got \"string\"",
		},
		"merged member hidden by a same-spelled key": {
			// The merge brings in 0x10: x as the member 16. The quoted key
			// "0x10" of m names a separate member with the same spelling,
			// and a path selector matches that key, so the path keeps the
			// name 16 and the error binds at the x.
			schema: `{
				"type": "object",
				"properties": {
					"m": {"additionalProperties": {"type": "integer"}}
				}
			}`,
			input:      "m: {<<: {0x10: x}, \"0x10\": 5}\n",
			wantPath:   "$.m.16",
			want:       "1:16: $.m.16: expected \"integer\", got \"string\"",
			unresolved: true,
		},
		"tagged alias key replaces an earlier key": {
			// The tag may change the name the alias key decodes to, so the
			// key 0x10 before it cannot name the member 16.
			schema: `{
				"type": "object",
				"properties": {
					"m": {"additionalProperties": {"type": "integer"}}
				}
			}`,
			input:    "a: &k 16\nm: {0x10: 5, ? !!str *k : x}\n",
			wantPath: "$.m.16",
			want:     "2:27: $.m.16: expected \"integer\", got \"string\"",
		},
		"merge source with a tagged alias key": {
			// A key of the merge source may decode to any name, so the
			// key 0x10 before the merge cannot name the member 16.
			schema: `{
				"type": "object",
				"properties": {
					"m": {"additionalProperties": {"type": "integer"}}
				}
			}`,
			input:    "a: &k 16\nb: &b {? !!str *k : x}\nm: {0x10: 5, <<: *b}\n",
			wantPath: "$.m.16",
			want:     "2:21: $.m.16: expected \"integer\", got \"string\"",
		},
		"unnameable alias key hides earlier keys": {
			// The alias key refers to a sequence, which the walk cannot
			// name, so the key 0x10 before it cannot name the member 16.
			// The walk finds no member there, so the error binds at m.
			schema: `{
				"type": "object",
				"properties": {
					"m": {"additionalProperties": {"type": "integer"}}
				}
			}`,
			input:      "s: &s [a]\nm: {0x10: x, ? *s : 1}\n",
			wantPath:   "$.m.16",
			want:       "2:1: $.m.16: expected \"integer\", got \"string\"",
			unresolved: true,
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
			if tc.unresolved {
				require.Error(t, err, "path from the error resolves")

				return
			}

			require.NoError(t, err, "path from the error does not resolve")
		})
	}
}

func TestSchema_SourcePath_ReferenceAlias(t *testing.T) {
	t.Parallel()

	// An alias to an anchor of a reference document resolves in the decode
	// and not in the document, so the walk cannot follow it. The path keeps
	// the decoded name of a member below the alias. A merge key that names
	// such an alias may set a member of any name, so the key 0x10 before
	// it cannot name the member 16 either.
	refs := niceyaml.WithReferences(niceyaml.NewSourceFromString("base: &base {0x10: x}\nother: &other {k: 1}\n"))

	tcs := map[string]struct {
		schema string
		input  string
		want   string
	}{
		"member below the alias": {
			schema: `{
				"type": "object",
				"properties": {
					"a": {"additionalProperties": {"type": "integer"}}
				}
			}`,
			input: "a: *base\n",
			want:  "$.a.16",
		},
		"member beside a merge of the alias": {
			schema: `{
				"type": "object",
				"additionalProperties": {"type": "integer"}
			}`,
			input: "0x10: x\n<<: *other\n",
			want:  "$.16",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			v := compileSchema(t, []byte(tc.schema))
			doc := yamltest.FirstDocument(t, tc.input, refs)

			_, err := doc.Decode[any](t.Context(), niceyaml.WithValidator(v))

			var bound *niceyaml.SourceError

			require.ErrorAs(t, err, &bound)

			gotPath, ok := bound.Path()
			require.True(t, ok, "bound error carries no path")
			assert.Equal(t, tc.want, gotPath.String())

			_, err = doc.At(gotPath)
			require.Error(t, err, "path from the error resolves")
		})
	}
}

func TestSchema_SourcePath_HiddenKey(t *testing.T) {
	t.Parallel()

	// In each case the key 0x10 sets the member 16 to x. A later entry
	// spells its key 0x10 too, and a path through 0x10 selects that entry.
	// The path of the member keeps its decoded name and selects nothing,
	// and the error binds at the x rather than at the valid 1. The quoted
	// "0x10" decodes to the member 0x10, as does an alias key whose anchor
	// holds it.
	v := compileSchema(t, []byte(`{
		"type": "object",
		"properties": {
			"user": {"additionalProperties": {"type": "integer"}}
		}
	}`))

	tcs := map[string]struct {
		input string
		// The position of the x, as the message prints it.
		want string
		opts []niceyaml.SourceOption
	}{
		"own key after the merge key": {
			input: "user:\n  <<: {0x10: x}\n  \"0x10\": 1\n",
			want:  "2:14",
		},
		"merge key after the own key": {
			input: "user:\n  0x10: x\n  <<: {\"0x10\": 1}\n",
			want:  "2:9",
		},
		"later source of one merge key": {
			input: "user:\n  <<: [{0x10: x}, {\"0x10\": 1}]\n",
			want:  "2:15",
		},
		"later aliased source of one merge key": {
			input: "a: &a {0x10: x}\nb: &b {\"0x10\": 1}\nuser:\n  <<: [*a, *b]\n",
			want:  "1:14",
		},
		"later merge key": {
			input: "user:\n  <<: {0x10: x}\n  <<: {\"0x10\": 1}\n",
			want:  "2:14",
			opts:  []niceyaml.SourceOption{niceyaml.WithAllowDuplicateKeys(true)},
		},
		"later own key": {
			input: "user:\n  0x10: x\n  \"0x10\": 1\n",
			want:  "2:9",
			opts:  []niceyaml.SourceOption{niceyaml.WithAllowDuplicateKeys(true)},
		},
		"later alias key": {
			input: "k: &k \"0x10\"\nuser:\n  0x10: x\n  *k : 1\n",
			want:  "3:9",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// FirstDocument takes no source options, and a repeated key
			// needs one.
			doc, err := niceyaml.NewSourceFromString(tc.input, tc.opts...).Document()
			require.NoError(t, err)

			err = doc.Validate(t.Context(), v)

			var bound *niceyaml.SourceError

			require.ErrorAs(t, err, &bound)
			assert.Equal(t, tc.want+": $.user.16: expected \"integer\", got \"string\"", bound.Error())

			path, ok := bound.Path()
			require.True(t, ok, "bound error carries no path")

			_, err = doc.At(path)
			require.Error(t, err, "path from the error resolves")
		})
	}
}

func TestSchema_SourcePath_BelowHiddenKey(t *testing.T) {
	t.Parallel()

	// In each case a later entry hides the key of the member that holds x,
	// so the path writes the decoded name of that member. That name
	// selects another entry, which holds a valid value under the spelling
	// 0x11, so the keys below keep their decoded names too. The path
	// selects nothing, and the error binds at the x.
	v := compileSchema(t, []byte(`{
		"type": "object",
		"properties": {
			"user": {
				"additionalProperties": {
					"additionalProperties": {"type": "integer"}
				}
			}
		}
	}`))

	tcs := map[string]struct {
		input string
		want  string
	}{
		"own key after the merge key": {
			input: "user: {16: {0x11: 2}, <<: {0x10: {0x11: x}}, \"0x10\": 1}\n",
			want:  "1:41: $.user.16.17: expected \"integer\", got \"string\"",
		},
		"block own key after the merge key": {
			input: stringtest.Input(`
				user:
				  16: {0x11: 2}
				  <<: {0x10: {0x11: x}}
				  "0x10": 1
			`),
			want: "3:21: $.user.16.17: expected \"integer\", got \"string\"",
		},
		"later source of one merge key": {
			input: "user: {16: {0x11: 2}, <<: [{0x10: {0x11: x}}, {\"0x10\": 1}]}\n",
			want:  "1:42: $.user.16.17: expected \"integer\", got \"string\"",
		},
		"merge key after the own key": {
			input: "user: {16: {0x11: 2}, 0x10: {0x11: x}, <<: {\"0x10\": 1}}\n",
			want:  "1:36: $.user.16.17: expected \"integer\", got \"string\"",
		},
		"own key spelled as its name before a merge key": {
			// The own key decodes to 0x10, and the merged 0x10 sets the
			// member 16, but a path through 0x10 selects the merged key.
			input: "user: {\"0x10\": {0x11: x}, <<: {0x10: {0x11: 1}, 16: 2}}\n",
			want:  "1:23: $.user.0x10.17: expected \"integer\", got \"string\"",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dd := yamltest.FirstDocument(t, tc.input)

			err := dd.Validate(t.Context(), v)

			var bound *niceyaml.SourceError

			require.ErrorAs(t, err, &bound)
			assert.Equal(t, tc.want, bound.Error())

			path, ok := bound.Path()
			require.True(t, ok, "bound error carries no path")

			_, err = dd.At(path)
			require.Error(t, err, "path from the error resolves")
		})
	}
}

func TestSchema_SourcePath_HiddenKeyName(t *testing.T) {
	t.Parallel()

	// The schema allows no member of user, so each violation points at a
	// key. The merge brings in 0x10 as the member 16, and the quoted key
	// "0x10" wins a path through 0x10. The path of the member 16 keeps its
	// decoded name, and its error binds at the key the merge brings in.
	v := compileSchema(t, []byte(`{
		"type": "object",
		"properties": {"user": {"additionalProperties": false}}
	}`))

	dd := yamltest.FirstDocument(t, "user:\n  <<: {0x10: x}\n  \"0x10\": 1\n")

	err := dd.Validate(t.Context(), v)

	var bound *niceyaml.SourceError

	require.ErrorAs(t, err, &bound)

	var got []string

	for _, child := range bound.Errors() {
		got = append(got, child.Error())
	}

	assert.ElementsMatch(t, []string{
		"2:8: $.user.16~: value is not allowed",
		"3:3: $.user.0x10~: value is not allowed",
	}, got)
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

// wideMerge returns a document whose mapping m holds members keys, each
// spelled in hexadecimal over the value x, and then a merge key that
// lists as many aliases to one mapping. To tell which entry the spelling
// of one of those keys selects, a lookup reads that whole list.
func wideMerge(members int) string {
	var sb strings.Builder

	sb.WriteString("a: &a {z: 0}\nm:\n")

	for i := range members {
		fmt.Fprintf(&sb, "  %#x: x\n", 0x1000+i)
	}

	sb.WriteString("  <<: [" + strings.Repeat("*a, ", members-1) + "*a]\n")

	return sb.String()
}

func TestSchema_SourcePath_MergeReads(t *testing.T) {
	t.Parallel()

	// Every member of m breaks the schema, and the violations of one
	// Validate share one limit on the nodes they read under the merge key.
	// Validate reports each violation past that limit too, with a path
	// that keeps the decoded name of its key, which selects no entry.
	v := compileSchema(t, []byte(`{
		"type": "object",
		"properties": {
			"m": {"additionalProperties": {"type": "integer"}}
		}
	}`))

	tcs := map[string]struct {
		members int
		decoded bool
	}{
		"few merge sources": {
			members: 10,
		},
		"many merge sources": {
			members: 2000,
			decoded: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dd := yamltest.FirstDocument(t, wideMerge(tc.members))

			err := v.Validate(t.Context(), dd)

			var ve *niceyaml.Error

			require.ErrorAs(t, err, &ve)

			violations := ve.Errors()
			require.Len(t, violations, tc.members)

			spelled := 0

			for _, violation := range violations {
				var child *niceyaml.Error

				require.ErrorAs(t, violation, &child)

				path, ok := child.Path()
				require.True(t, ok, "violation carries no path")

				if strings.HasPrefix(path.String(), "$.m.0x") {
					_, err := dd.At(path)
					require.NoError(t, err, "path %s does not resolve", path)

					spelled++
				}
			}

			if tc.decoded {
				assert.Positive(t, spelled)
				assert.Less(t, spelled, tc.members)

				return
			}

			assert.Equal(t, tc.members, spelled)
		})
	}
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
