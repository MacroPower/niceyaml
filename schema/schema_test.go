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

// requireExcessiveAliasing fails the test unless err is a refusal of the
// alias limit. Such an error matches [schema.ErrExcessiveAliasing], the
// document or the value is at fault for it, and it does not wrap
// [schema.ErrValidate].
func requireExcessiveAliasing(t *testing.T, err error) {
	t.Helper()

	require.ErrorIs(t, err, schema.ErrExcessiveAliasing)
	require.NotErrorIs(t, err, schema.ErrValidate)
	assert.True(t, niceyaml.IsInvalid(err), "IsInvalid(%v)", err)
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

func TestCompile_RequireRefs(t *testing.T) {
	t.Parallel()

	const nope = "https://example.invalid/nope.json"

	// The message of the compile error for a $ref to document.
	unresolved := func(document string) string {
		return fmt.Sprintf("no ref resolver for $ref to %q: schema URI not resolved", document)
	}

	serving := schema.WithJSONSchemaOptions(jsonschema.WithRefResolver(jsonschema.SchemaMap{
		nope: {},
	}))

	tcs := map[string]struct {
		schema string
		// The text of the compile error, or empty for a schema that
		// compiles.
		want string
		opts []schema.CompileOption
	}{
		"ref to another document": {
			schema: `{"properties": {"a": {"$ref": "` + nope + `#/$defs/a"}}}`,
			want:   "compile schema: " + unresolved(nope),
		},
		"relative ref": {
			schema: `{"properties": {"a": {"$ref": "defs.json#/$defs/port"}}}`,
			want:   "compile schema: " + unresolved("defs.json"),
		},
		"dynamic ref to another document": {
			schema: `{"properties": {"a": {"$dynamicRef": "` + nope + `#node"}}}`,
			want:   "compile schema: " + unresolved(nope),
		},
		"under a branch no document takes": {
			schema: `{"if": false, "then": {"$ref": "` + nope + `"}}`,
			want:   "compile schema: " + unresolved(nope),
		},
		"in a definition nothing refers to": {
			schema: `{"$defs": {"unused": {"$ref": "` + nope + `"}}}`,
			want:   "compile schema: " + unresolved(nope),
		},
		"two refs to one document": {
			schema: `{"allOf": [{"$ref": "` + nope + `#/$defs/a"}, {"$ref": "` + nope + `#/$defs/b"}]}`,
			want:   "compile schema: " + unresolved(nope),
		},
		"refs to two documents": {
			schema: `{"allOf": [{"$ref": "a.json"}, {"$ref": "b.json"}]}`,
			want:   "compile schema: " + unresolved("a.json") + "\n" + unresolved("b.json"),
		},
		"password in the ref": {
			schema: `{"$ref": "https://user:secret@example.invalid/nope.json"}`,
			want:   "compile schema: " + unresolved("https://user:xxxxx@example.invalid/nope.json"),
		},
		"required again after not required": {
			opts:   []schema.CompileOption{schema.WithRequireRefs(false), schema.WithRequireRefs(true)},
			schema: `{"$ref": "` + nope + `"}`,
			want:   "compile schema: " + unresolved(nope),
		},
		"refs not required": {
			opts:   []schema.CompileOption{schema.WithRequireRefs(false)},
			schema: `{"properties": {"a": {"$ref": "` + nope + `"}}}`,
		},
		"ref inside the schema": {
			schema: `{"properties": {"a": {"$ref": "#/$defs/a"}}, "$defs": {"a": {"type": "string"}}}`,
		},
		"ref to a document the schema holds": {
			schema: `{
				"properties": {"a": {"$ref": "` + nope + `"}},
				"$defs": {"held": {"$id": "` + nope + `", "type": "string"}}
			}`,
		},
		"ref to the schema by its own id": {
			schema: `{
				"$id": "` + nope + `",
				"properties": {"a": {"$ref": "` + nope + `#/$defs/a"}},
				"$defs": {"a": {"type": "string"}}
			}`,
		},
		"resolver that serves the document": {
			opts:   []schema.CompileOption{serving},
			schema: `{"properties": {"a": {"$ref": "` + nope + `"}}}`,
		},
		// The compile cannot tell which documents a resolver of the
		// caller's serves, so the validation reports the ref.
		"resolver that does not serve the document": {
			opts:   []schema.CompileOption{serving},
			schema: `{"properties": {"a": {"$ref": "https://example.invalid/other.json"}}}`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s, err := schema.Compile(t.Context(), []byte(tc.schema), tc.opts...)
			if tc.want == "" {
				require.NoError(t, err)
				assert.NotNil(t, s)

				return
			}

			require.ErrorIs(t, err, schema.ErrCompile)
			require.ErrorIs(t, err, jsonschema.ErrNotResolved)
			assert.Equal(t, tc.want, err.Error())
			assert.Nil(t, s)

			assert.PanicsWithError(t, tc.want, func() {
				schema.MustCompile([]byte(tc.schema), tc.opts...)
			})
		})
	}
}

func TestSchema_UnresolvableRef(t *testing.T) {
	t.Parallel()

	unreachable := errors.New("host unreachable")
	refusing := jsonschema.RefResolverFunc(func(_ context.Context, _ string) (*jsonschema.Schema, error) {
		return nil, unreachable
	})

	// Each schema compiles with a $ref that does not resolve, either
	// because the compile does not require it to or because a resolver
	// of the caller's stands in for the check. The validator reports the
	// ref when it walks to it, so the failure arrives at the location of
	// the value that referenced it. The schema is at fault, not the
	// document, so every case wraps ErrValidate.
	tcs := map[string]struct {
		data   map[string]any
		err    error
		schema string
		want   string
		opts   []schema.CompileOption
	}{
		"no resolver": {
			opts:   []schema.CompileOption{schema.WithRequireRefs(false)},
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
			opts: []schema.CompileOption{schema.WithRequireRefs(false)},
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

			// The failure states the error of the resolver, so the tree
			// shows the two in one row.
			assert.Equal(t, 1, strings.Count(niceyaml.FormatError(err, 0), tc.want))

			var failure *jsonschema.ValidationError

			require.ErrorAs(t, err, &failure)
			assert.Equal(t, jsonschema.KeywordRef, failure.Keyword)

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

// requestSchema is a schema for the body of a request, which comes from
// no document. A port of 0 breaks its minimum, and a body with no name
// breaks its required.
const requestSchema = `{
	"type": "object",
	"properties": {
		"port": {"type": "integer", "minimum": 1},
		"name": {"type": "string"}
	},
	"required": ["name"]
}`

func TestSchema_ValidateValue(t *testing.T) {
	t.Parallel()

	v := compileSchema(t, []byte(requestSchema))

	// The value came from no document, so the error is bound to a source
	// with no text and no name. Its text then names the path of each
	// violation wherever the error prints.
	tcs := map[string]struct {
		data       map[string]any
		want       string
		wantFormat string
		wantPaths  []string
	}{
		"one violation": {
			data:       map[string]any{"port": 0, "name": "x"},
			want:       "$.port: 0 is less than 1",
			wantFormat: "$.port: 0 is less than 1",
			wantPaths:  []string{"$.port"},
		},
		"two violations": {
			data: map[string]any{"port": 0},
			want: stringtest.JoinLF(
				"2 schema violations",
				"$.port: 0 is less than 1",
				`$.name: missing required property "name"`,
			),
			wantFormat: stringtest.JoinLF(
				"2 schema violations",
				"|-- $.port: 0 is less than 1",
				"`-- $.name: missing required property \"name\"",
			),
			wantPaths: []string{"$.port", "$.name"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := v.ValidateValue(t.Context(), tc.data)
			require.EqualError(t, err, tc.want)

			// A wrapper and a join keep the text of the error they hold,
			// so each path shows through both.
			assert.Equal(t, tc.want, fmt.Sprintf("%v", err))
			require.EqualError(t, fmt.Errorf("invalid request: %w", err), "invalid request: "+tc.want)
			require.EqualError(t, errors.Join(errors.New("other failure"), err), "other failure\n"+tc.want)

			// The value is at fault, and the first violation is in reach.
			assert.True(t, niceyaml.IsInvalid(err))
			require.NotErrorIs(t, err, schema.ErrValidate)

			var violation *schema.Violation

			require.ErrorAs(t, err, &violation)
			assert.Equal(t, jsonschema.KeywordMinimum, violation.Keyword)

			// The source holds no line to excerpt, so the tree stands
			// alone, with no line that says so.
			assert.Equal(t, tc.wantFormat, niceyaml.FormatError(err, 2))
			assert.Equal(t, tc.wantFormat, fmt.Sprintf("%+v", err))

			var bound *niceyaml.SourceError

			require.ErrorAs(t, err, &bound)
			assert.Empty(t, bound.Source().Name())
			assert.Zero(t, bound.Source().Lines().Len())

			var gotPaths []string

			for problem := range niceyaml.NewErrorTree(err).Problems() {
				require.NotNil(t, problem.Bound)

				_, ok := problem.Bound.Position()
				assert.False(t, ok)

				path, ok := problem.Path()
				require.True(t, ok)

				gotPaths = append(gotPaths, path.String())
			}

			assert.Equal(t, tc.wantPaths, gotPaths)

			// The value came from no document, so no binding of the
			// result names a Node, a document, or the index of one.
			for b := range niceyaml.AllBindings(err) {
				assert.Nil(t, b.Node())
				assert.Nil(t, b.Document())

				_, ok := b.DocumentIndex()
				assert.False(t, ok)

				// The reason for a path is the one of a document with no
				// content, which FormatError leaves out.
				if _, hasPath := b.Path(); hasPath {
					require.ErrorIs(t, b.Unresolved(), paths.ErrNoDocument)
				}
			}
		})
	}

	t.Run("conforming value", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, v.ValidateValue(t.Context(), map[string]any{"port": 80, "name": "x"}))
	})

	t.Run("the result reads the same once a document placed it", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, "request:\n  port: 0\n", niceyaml.WithName("app.yaml"))
		base := paths.Doc().Child("request")

		err := v.ValidateValue(t.Context(), map[string]any{"port": 0, "name": "x"})
		require.EqualError(t, err, "$.port: 0 is less than 1")

		got := doc.Bind(niceyaml.Rebase(err, base))
		require.EqualError(t, got, "app.yaml:2:9: $.request.port: 0 is less than 1")
		assert.NotSame(t, err, got)

		var bound *niceyaml.SourceError

		require.ErrorAs(t, got, &bound)
		assert.Same(t, doc.Source(), bound.Source())

		// Placing the result builds a new error and leaves the result as
		// it was.
		require.EqualError(t, err, "$.port: 0 is less than 1")

		require.ErrorAs(t, err, &bound)
		assert.NotSame(t, doc.Source(), bound.Source())
	})
}

func TestSchema_ValidateValue_Place(t *testing.T) {
	t.Parallel()

	v := compileSchema(t, []byte(requestSchema))

	doc := yamltest.FirstDocument(t, "request:\n  port: 0\n", niceyaml.WithName("app.yaml"))
	base := paths.Doc().Child("request")

	// The result stands in no document, so Rebase and Bind place it as
	// they place an error that no source bound yet. Each path then shows
	// once, with or without a wrapper around the result.
	tcs := map[string]struct {
		data        map[string]any
		wantBound   string
		wantWrapped string
		// The result bound through the root with no Rebase, where each
		// path reads from the root of the document.
		wantRoot string
	}{
		"one violation": {
			data:        map[string]any{"port": 0, "name": "x"},
			wantBound:   "app.yaml:2:9: $.request.port: 0 is less than 1",
			wantWrapped: "app.yaml:2:9: $.request.port: check: 0 is less than 1",
			wantRoot:    "app.yaml:1:1: $.port: 0 is less than 1",
		},
		"two violations": {
			data: map[string]any{"port": 0},
			wantBound: stringtest.JoinLF(
				"app.yaml: 2 schema violations",
				`app.yaml:1:1: $.request.name: missing required property "name"`,
				"app.yaml:2:9: $.request.port: 0 is less than 1",
			),
			wantWrapped: stringtest.JoinLF(
				"app.yaml: check: 2 schema violations",
				`app.yaml:1:1: $.request.name: missing required property "name"`,
				"app.yaml:2:9: $.request.port: 0 is less than 1",
			),
			wantRoot: stringtest.JoinLF(
				"app.yaml: 2 schema violations",
				"app.yaml:1:1: $.port: 0 is less than 1",
				`app.yaml:1:1: $.name: missing required property "name"`,
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := v.ValidateValue(t.Context(), tc.data)

			placed := doc.Bind(niceyaml.Rebase(err, base))
			require.EqualError(t, placed, tc.wantBound)
			assert.True(t, niceyaml.IsInvalid(placed))

			var violation *schema.Violation

			require.ErrorAs(t, placed, &violation)

			for b := range niceyaml.AllBindings(placed) {
				assert.Same(t, doc.Source(), b.Source())
			}

			require.EqualError(t,
				doc.Bind(niceyaml.Rebase(fmt.Errorf("check: %w", err), base)),
				tc.wantWrapped,
			)
			require.EqualError(t,
				doc.Bind(niceyaml.Rebase(fmt.Errorf("check: %w", fmt.Errorf("body: %w", err)), base)),
				strings.Replace(tc.wantWrapped, "check: ", "check: body: ", 1),
			)

			// A wrapper that names a sentinel beside the result reads as
			// a wrapper around the result alone.
			require.EqualError(t,
				doc.Bind(niceyaml.Rebase(fmt.Errorf("%w: %w", errPlaceCheck, err), base)),
				tc.wantWrapped,
			)

			// A Node scoped to the value puts its own path in front, and
			// the root reads each path from the root of the document.
			require.EqualError(t, yamltest.At(t, doc, base).Bind(err), tc.wantBound)
			require.EqualError(t, doc.Bind(err), tc.wantRoot)

			// A summary and a join place each result they hold.
			require.EqualError(t,
				doc.Bind(niceyaml.Rebase(errors.Join(err), base)),
				tc.wantBound,
			)
		})
	}

	t.Run("conforming value", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, v.ValidateValue(t.Context(), map[string]any{"port": 80, "name": "x"}))
	})

	t.Run("a wrapper keeps what it matches", func(t *testing.T) {
		t.Parallel()

		wrapped := &placeCheckError{err: v.ValidateValue(t.Context(), map[string]any{"port": 0, "name": "x"})}
		require.EqualError(t, wrapped, "check: $.port: 0 is less than 1")

		placed := doc.Bind(niceyaml.Rebase(wrapped, base))
		require.EqualError(t, placed, "app.yaml:2:9: $.request.port: check: 0 is less than 1")
		require.ErrorIs(t, placed, wrapped)

		var got *placeCheckError

		require.ErrorAs(t, placed, &got)
		assert.Same(t, wrapped, got)
	})

	t.Run("a wrapper keeps the errors beside the result", func(t *testing.T) {
		t.Parallel()

		typed := &placeCheckError{err: errPlaceCheck}

		err := v.ValidateValue(t.Context(), map[string]any{"port": 0, "name": "x"})

		placed := doc.Bind(niceyaml.Rebase(fmt.Errorf("%w: %w", typed, err), base))
		require.EqualError(t, placed, "app.yaml:2:9: $.request.port: check: check: 0 is less than 1")
		require.ErrorIs(t, placed, errPlaceCheck)
		assert.True(t, niceyaml.IsInvalid(placed))

		var got *placeCheckError

		require.ErrorAs(t, placed, &got)
		assert.Same(t, typed, got)

		var violation *schema.Violation

		require.ErrorAs(t, placed, &violation)
	})

	t.Run("a wrapper that rewrites the text keeps its text", func(t *testing.T) {
		t.Parallel()

		one := v.ValidateValue(t.Context(), map[string]any{"port": 0, "name": "x"})
		two := v.ValidateValue(t.Context(), map[string]any{"port": 0})

		// The text of each wrapper holds the text of the result nowhere, or
		// twice, so no path could leave it. The document places every
		// violation all the same, and the wrapper keeps the text it wrote.
		tcs := map[string]struct {
			err  error
			want string
		}{
			"message of its own": {
				err:  placeFixedError{err: one},
				want: "app.yaml:2:9: $.request.port: invalid body",
			},
			"message of its own above two violations": {
				err: placeFixedError{err: two},
				want: stringtest.JoinLF(
					"app.yaml: invalid body",
					`app.yaml:1:1: $.request.name: missing required property "name"`,
					"app.yaml:2:9: $.request.port: 0 is less than 1",
				),
			},
			"message of its own beside a sentinel": {
				err:  fmt.Errorf("%w: %w", errPlaceCheck, placeFixedError{err: one}),
				want: "app.yaml:2:9: $.request.port: check: invalid body",
			},
			"upper case": {
				err:  placeShoutError{err: one},
				want: "app.yaml:2:9: $.request.port: $.PORT: 0 IS LESS THAN 1",
			},
			"indented lines": {
				err: placeIndentError{err: two},
				want: stringtest.JoinLF(
					"app.yaml: validation:",
					"  2 schema violations",
					"  $.port: 0 is less than 1",
					`  $.name: missing required property "name"`,
					`app.yaml:1:1: $.request.name: missing required property "name"`,
					"app.yaml:2:9: $.request.port: 0 is less than 1",
				),
			},
			"text held twice": {
				err:  fmt.Errorf("%w (again: %s)", one, one.Error()),
				want: "app.yaml:2:9: $.request.port: $.port: 0 is less than 1 (again: $.port: 0 is less than 1)",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				placed := doc.Bind(niceyaml.Rebase(tc.err, base))
				require.EqualError(t, placed, tc.want)
				require.ErrorIs(t, placed, tc.err)
				assert.True(t, niceyaml.IsInvalid(placed))

				var violation *schema.Violation

				require.ErrorAs(t, placed, &violation)

				for b := range niceyaml.AllBindings(placed) {
					assert.Same(t, doc.Source(), b.Source())
				}
			})
		}
	})

	t.Run("a Validate method returns the result", func(t *testing.T) {
		t.Parallel()

		cfg := placeConfig{Request: placeRequest{schema: v}}

		err := doc.DecodeInto(t.Context(), &cfg)
		require.EqualError(t, err, "app.yaml:2:9: $.request.port: 0 is less than 1")
		assert.True(t, niceyaml.IsInvalid(err))
	})

	t.Run("a Validate method returns the result under a message of its own", func(t *testing.T) {
		t.Parallel()

		cfg := placeConfig{Request: placeRequest{schema: v, fixed: true}}

		err := doc.DecodeInto(t.Context(), &cfg)
		require.EqualError(t, err, "app.yaml:2:9: $.request.port: invalid body")
		assert.True(t, niceyaml.IsInvalid(err))
	})

	t.Run("a Validate method returns the result inside Invalid", func(t *testing.T) {
		t.Parallel()

		cfg := placeConfig{Request: placeRequest{schema: v, invalid: true}}

		err := doc.DecodeInto(t.Context(), &cfg)
		require.EqualError(t, err, "app.yaml:2:9: $.request.port: 0 is less than 1")
		assert.True(t, niceyaml.IsInvalid(err))
	})

	t.Run("an Error with no option places the result", func(t *testing.T) {
		t.Parallel()

		err := v.ValidateValue(t.Context(), map[string]any{"port": 0, "name": "x"})
		request := yamltest.At(t, doc, base)

		const want = "app.yaml:2:9: $.request.port: 0 is less than 1"

		// Invalid and Place with no option add nothing to the result, so
		// it places as it does alone.
		require.EqualError(t, request.Invalid(err), want)
		require.EqualError(t, request.Place(err), want)
		require.EqualError(t, doc.Bind(niceyaml.Rebase(niceyaml.Invalid(err), base)), want)
		require.EqualError(t,
			doc.Bind(niceyaml.Rebase(fmt.Errorf("check: %w", niceyaml.Invalid(err)), base)),
			"app.yaml:2:9: $.request.port: check: 0 is less than 1",
		)
		require.EqualError(t,
			doc.Bind(niceyaml.Rebase(fmt.Errorf("%w: %w", errPlaceCheck, niceyaml.Place(err)), base)),
			"app.yaml:2:9: $.request.port: check: 0 is less than 1",
		)

		for b := range niceyaml.AllBindings(request.Invalid(err)) {
			assert.Same(t, doc.Source(), b.Source())
		}
	})

	t.Run("an Error with a location or details places the result", func(t *testing.T) {
		t.Parallel()

		one := v.ValidateValue(t.Context(), map[string]any{"port": 0, "name": "x"})
		two := v.ValidateValue(t.Context(), map[string]any{"port": 0})
		at := niceyaml.AtPath(paths.Current())
		why := niceyaml.WithDetails(errors.New("the body of the request"))

		// An Error writes the message of the error it wraps, so the Error
		// binds around the violations as it binds around errors that no
		// source bound yet. No line keeps a path from the value.
		tcs := map[string]struct {
			err         error
			want        string
			wantDetails []string
		}{
			"location above one violation": {
				err:  niceyaml.Invalid(one, at),
				want: "app.yaml:2:3: $.request: 0 is less than 1",
			},
			"location above two violations": {
				err: niceyaml.Invalid(two, at),
				want: stringtest.JoinLF(
					"app.yaml:2:3: $.request: 2 schema violations",
					`app.yaml:1:1: $.request.name: missing required property "name"`,
					"app.yaml:2:9: $.request.port: 0 is less than 1",
				),
			},
			"wrapper around a location": {
				err: fmt.Errorf("check: %w", niceyaml.Place(two, at)),
				want: stringtest.JoinLF(
					"app.yaml:2:3: $.request: check: 2 schema violations",
					`app.yaml:1:1: $.request.name: missing required property "name"`,
					"app.yaml:2:9: $.request.port: 0 is less than 1",
				),
			},
			"details beside one violation": {
				err:         niceyaml.Place(one, why),
				want:        "app.yaml:2:9: $.request.port: 0 is less than 1",
				wantDetails: []string{"the body of the request"},
			},
			"details beside two violations": {
				err: niceyaml.Invalid(two, why),
				want: stringtest.JoinLF(
					"app.yaml: 2 schema violations",
					`app.yaml:1:1: $.request.name: missing required property "name"`,
					"app.yaml:2:9: $.request.port: 0 is less than 1",
				),
				wantDetails: []string{"the body of the request"},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				placed := doc.Bind(niceyaml.Rebase(tc.err, base))
				require.EqualError(t, placed, tc.want)
				assert.True(t, niceyaml.IsInvalid(placed))

				var bound *niceyaml.SourceError

				require.ErrorAs(t, placed, &bound)

				var gotDetails []string

				for _, detail := range bound.Details() {
					gotDetails = append(gotDetails, detail.Message())
				}

				assert.Equal(t, tc.wantDetails, gotDetails)

				for _, problem := range bound.Members() {
					assert.Same(t, doc.Source(), problem.Source())
				}
			})
		}
	})

	t.Run("a Validator returns the result", func(t *testing.T) {
		t.Parallel()

		check := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
			return v.ValidateValue(ctx, map[string]any{"port": 0, "name": "x"})
		})

		err := yamltest.At(t, doc, base).Validate(t.Context(), check)
		require.EqualError(t, err, "app.yaml:2:9: $.request.port: 0 is less than 1")
	})

	t.Run("a summary and a detail place the results they hold", func(t *testing.T) {
		t.Parallel()

		err := v.ValidateValue(t.Context(), map[string]any{"port": 0, "name": "x"})

		unnamed := v.ValidateValue(t.Context(), map[string]any{"port": 80})

		summary := niceyaml.NewSummary("request checks", err, unnamed)
		require.EqualError(t, doc.Bind(niceyaml.Rebase(summary, base)), stringtest.JoinLF(
			"app.yaml: request checks",
			`app.yaml:1:1: $.request.name: missing required property "name"`,
			"app.yaml:2:9: $.request.port: 0 is less than 1",
		))

		detailed := niceyaml.NewError(
			"bad request",
			niceyaml.AtPath(base),
			niceyaml.WithDetails(niceyaml.Rebase(err, base)),
		)

		var bound *niceyaml.SourceError

		require.ErrorAs(t, doc.Bind(detailed), &bound)
		require.Len(t, bound.Details(), 1)
		assert.Same(t, doc.Source(), bound.Details()[0].Source())
	})

	t.Run("each violation places on its own", func(t *testing.T) {
		t.Parallel()

		err := v.ValidateValue(t.Context(), map[string]any{"port": 0})

		var result *niceyaml.SourceError

		require.ErrorAs(t, err, &result)
		require.Len(t, result.Members(), 2)

		// A caller that keeps only some of the violations places the
		// ones it keeps, and each stands in the document as it does
		// when the caller places the whole result.
		want := []string{
			"app.yaml:2:9: $.request.port: 0 is less than 1",
			`app.yaml:1:1: $.request.name: missing required property "name"`,
		}

		for i, violation := range result.Members() {
			placed := doc.Bind(niceyaml.Rebase(violation, base))
			require.EqualError(t, placed, want[i])
			assert.True(t, niceyaml.IsInvalid(placed))

			for b := range niceyaml.AllBindings(placed) {
				assert.Same(t, doc.Source(), b.Source())
			}

			// The violation itself stays where the result put it.
			assert.NotSame(t, doc.Source(), violation.Source())
		}

		kept := errors.Join(result.Members()[1], result.Members()[0])
		require.EqualError(t, yamltest.At(t, doc, base).Bind(kept), stringtest.JoinLF(want[1], want[0]))
	})

	t.Run("path names a key as the decoder does", func(t *testing.T) {
		t.Parallel()

		ports := compileSchema(t, []byte(`{"properties": {"ports": {"additionalProperties": {"minimum": 1}}}}`))
		hex := yamltest.FirstDocument(t, "ports:\n  0x10: 0\n", niceyaml.WithName("app.yaml"))

		data, err := hex.Decode[any](t.Context())
		require.NoError(t, err)

		// The decoder names the key 0x10 as 16, and ValidateValue reads no
		// source that spells it another way.
		err = ports.ValidateValue(t.Context(), data)

		var located *niceyaml.SourceError

		require.ErrorAs(t, err, &located)

		path, ok := located.Path()
		require.True(t, ok)
		assert.Equal(t, "$.ports.16", path.String())

		// No key of the document has that spelling, so the path binds at
		// the key of the mapping.
		require.EqualError(t, hex.Bind(err), "app.yaml:1:1: $.ports.16: 0 is less than 1")

		// A DataLocator reads the names as the decoder does, so it finds
		// the entry.
		var names []string

		for sel := range path.Selectors() {
			names = append(names, sel.Name)
		}

		found := niceyaml.NewError(located.Message(), hex.DataLocator().At(names...))
		require.EqualError(t, hex.Bind(found), "app.yaml:2:9: $.ports.0x10: 0 is less than 1")
	})
}

// errPlaceCheck is a sentinel a caller wraps beside the error of a check.
var errPlaceCheck = errors.New("check")

// placeCheckError wraps the error of a check with a prefix, as a caller's
// own error type does.
type placeCheckError struct {
	err error
}

func (e *placeCheckError) Error() string {
	return "check: " + e.err.Error()
}

func (e *placeCheckError) Unwrap() error {
	return e.err
}

// placeShoutError wraps an error and rewrites its text.
type placeShoutError struct {
	err error
}

func (e placeShoutError) Error() string {
	return strings.ToUpper(e.err.Error())
}

func (e placeShoutError) Unwrap() error {
	return e.err
}

// placeFixedError wraps an error under a message of its own, as an error
// type that maps a failure to a status does.
type placeFixedError struct {
	err error
}

func (e placeFixedError) Error() string {
	return "invalid body"
}

func (e placeFixedError) Unwrap() error {
	return e.err
}

// placeIndentError wraps an error and indents each line of its text.
type placeIndentError struct {
	err error
}

func (e placeIndentError) Error() string {
	return "validation:\n  " + strings.ReplaceAll(e.err.Error(), "\n", "\n  ")
}

func (e placeIndentError) Unwrap() error {
	return e.err
}

// placeConfig holds a [placeRequest] under the key request.
type placeConfig struct {
	Request placeRequest `yaml:"request"`
}

// placeRequest checks itself against a schema the test sets before the
// decode, and returns what ValidateValue returns. With invalid, it
// returns that result inside [niceyaml.Invalid] with no option. With
// fixed, it returns that result inside a [placeFixedError].
type placeRequest struct {
	schema *schema.Schema

	Port int `yaml:"port"`

	invalid bool
	fixed   bool
}

func (r placeRequest) Validate() error {
	err := r.schema.ValidateValue(context.Background(), map[string]any{"port": r.Port, "name": "x"})

	switch {
	case r.invalid:
		return niceyaml.Invalid(err)

	case r.fixed:
		return placeFixedError{err: err}
	}

	//nolint:wrapcheck // The test checks where a decode places the result as it is.
	return err
}

func TestSchema_ValidateValue_OrderedMap(t *testing.T) {
	t.Parallel()

	// A decode with niceyaml.WithYAMLOrderedMaps yields yaml.MapSlice for
	// every mapping, which the JSON Schema validator does not accept.
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
		schema    string
		input     string
		err       string
		excessive bool
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
			err: `$.n.b: expected "integer", got "string"`,
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
			err: `$.items[0].b: expected "integer", got "string"`,
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
			schema:    `{"type": "object"}`,
			input:     bomb.String(),
			excessive: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			v := compileSchema(t, []byte(tc.schema))

			// The source turns the alias limit off, so the bomb decodes.
			// ValidateValue takes no source and applies the limit to the
			// value all the same.
			doc := yamltest.FirstDocument(t, tc.input, niceyaml.WithAliasLimit(false))

			data, err := doc.Decode[any](t.Context(), niceyaml.WithYAMLOrderedMaps(true))
			require.NoError(t, err)

			err = v.ValidateValue(t.Context(), data)

			switch {
			case tc.excessive:
				requireExcessiveAliasing(t, err)

			case tc.err != "":
				require.EqualError(t, err, tc.err)
				require.NotErrorIs(t, err, schema.ErrValidate)

			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestSchema_Validate_OrderedMapDates(t *testing.T) {
	t.Parallel()

	// The niceyaml.WithYAMLOrderedMaps option reaches the decode that
	// gets it and not the decode the schema runs, so the schema reads
	// plain maps, and a date-only timestamp under a key the decoder
	// respells keeps its full-date spelling.
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
				niceyaml.WithYAMLOrderedMaps(true),
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
			schema    string
			input     string
			err       string
			excessive bool
		}{
			"alias bomb": {
				schema:    `{"type": "object"}`,
				input:     yamltest.AliasLevels(8),
				excessive: true,
			},
			"binary aliased many times": {
				// Each alias would add the 22 KB of base64 text again.
				schema:    `{"type": "object"}`,
				input:     binaryAliases(16<<10, 1000),
				excessive: true,
			},
			"binary aliased a few times": {
				// The aliases make up nine tenths of the base64 text, which
				// stays within the limit.
				schema: `{"properties": {"list": {"items": {"type": "string"}}}}`,
				input:  binaryAliases(4<<10, 10),
			},
			"small mapping aliased in each item of a long list": {
				// Each alias counts as a node of the document, so the 150
				// aliases stay a small enough share of what a decode reads.
				schema: `{"properties": {"matrix": {"items": {"required": ["os"]}}}}`,
				input: "base: &b {os: linux, arch: amd64, go: stable, cgo: false}\nmatrix:\n" +
					strings.Repeat("  - *b\n", 150),
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

				// A source with the alias limit off decodes the value
				// ValidateValue checks, which applies the limit to it all
				// the same.
				trusted := yamltest.FirstDocument(t, tc.input, niceyaml.WithAliasLimit(false))

				data, err := trusted.Decode[any](t.Context())
				require.NoError(t, err)

				for _, err := range []error{
					doc.Validate(t.Context(), v),
					v.ValidateValue(t.Context(), data),
				} {
					switch {
					case tc.excessive:
						requireExcessiveAliasing(t, err)

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
			path      paths.Path
			input     string
			excessive bool
		}{
			"alias bomb as mapping key": {
				input:     lists + "b:\n  ? *l7\n  : v\n",
				excessive: true,
			},
			"alias bomb as flow mapping key": {
				input:     lists + "b: {*l7 : v}\n",
				excessive: true,
			},
			"alias bomb under a string tag": {
				input:     lists + "b: !!str *l7\n",
				excessive: true,
			},
			"merge key bomb": {
				input:     yamltest.MergeLevels(7),
				excessive: true,
			},
			"node holding an alias with a bomb outside it": {
				// A decode of a node that holds an alias reads the whole
				// document to find the anchor.
				path:      paths.Current().Child("c"),
				input:     lists + "b:\n  ? *l7\n  : v\nc: [*k]\n",
				excessive: true,
			},
			"alias to a small sequence as mapping key": {
				input: "s: &s [a, b]\n*s : v\n",
			},
			// The key holds a copy of the long scalar for each alias.
			"scalar aliases written out in a key": {
				input:     scalarAliases + "m: {? *k : v}\n",
				excessive: true,
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
				if tc.path.Len() > 0 {
					doc = yamltest.At(t, doc, tc.path)
				}

				err := doc.Validate(t.Context(), v)
				if !tc.excessive {
					require.NoError(t, err)

					return
				}

				requireExcessiveAliasing(t, err)
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
				doc       *niceyaml.Node
				path      paths.Path
				excessive bool
			}{
				"first node holding an alias": {
					doc:       bomb,
					path:      paths.Current().Child("c"),
					excessive: true,
				},
				"second node holding an alias": {
					doc:       bomb,
					path:      paths.Current().Child("d"),
					excessive: true,
				},
				"node without an alias": {
					doc:  bomb,
					path: paths.Current().Child("e"),
				},
				"document diluting its aliases": {
					doc: diluted,
				},
				"node of a document diluting its aliases": {
					doc:  diluted,
					path: paths.Current().Child("list"),
				},
				"node with one alias to a binary aliased many times": {
					doc:       binary,
					path:      paths.Current().Child("other"),
					excessive: true,
				},
				"document with aliases to a tag over an alias to a binary": {
					doc:       chained,
					excessive: true,
				},
				"node of aliases to a tag over an alias to a binary": {
					doc:       chained,
					path:      paths.Current().Child("list"),
					excessive: true,
				},
				"node with one alias to a tag over an alias to a binary": {
					doc:       chained,
					path:      paths.Current().Child("other"),
					excessive: true,
				},
				"document with aliases to a binary under another tag": {
					doc:       wrapped,
					excessive: true,
				},
				"node with one alias to a binary under another tag": {
					doc:       wrapped,
					path:      paths.Current().Child("other"),
					excessive: true,
				},
			}

			for name, tc := range tcs {
				t.Run(name, func(t *testing.T) {
					t.Parallel()

					node := tc.doc
					if tc.path.Len() > 0 {
						node = yamltest.At(t, node, tc.path)
					}

					for range 2 {
						err := node.Validate(t.Context(), v)
						if !tc.excessive {
							require.NoError(t, err)

							continue
						}

						requireExcessiveAliasing(t, err)
					}
				})
			}
		})
	})

	// A source with the alias limit off decodes and validates a document
	// the limit refuses. The schema then reads every use of every alias,
	// so each case stays small.
	t.Run("sources with the limit off", func(t *testing.T) {
		t.Parallel()

		v := compileSchema(t, []byte(`{"type": ["object", "array"]}`))

		tcs := map[string]struct {
			path  paths.Path
			input string
		}{
			"alias bomb": {
				input: yamltest.AliasLevels(4),
			},
			"node of an alias bomb": {
				path:  paths.Current().Child("a").Index(4),
				input: yamltest.AliasLevels(4),
			},
			"merge key bomb": {
				input: yamltest.MergeLevels(4),
			},
			"alias bomb as mapping key": {
				input: yamltest.AliasLevels(4) + "b:\n  ? *l4\n  : v\n",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, tc.input)
				if tc.path.Len() > 0 {
					doc = yamltest.At(t, doc, tc.path)
				}

				requireExcessiveAliasing(t, doc.Validate(t.Context(), v))

				trusted := yamltest.FirstDocument(t, tc.input, niceyaml.WithAliasLimit(false))
				if tc.path.Len() > 0 {
					trusted = yamltest.At(t, trusted, tc.path)
				}

				require.NoError(t, trusted.Validate(t.Context(), v))

				// The schema passes as a validator of a decode too, and
				// the decode then reads the node.
				data, err := trusted.Decode[any](t.Context(), niceyaml.WithValidator(v))
				require.NoError(t, err)
				assert.NotEmpty(t, data)
			})
		}
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

		// The refusal carries no location, so each case names where the
		// node that binds it puts it.
		tcs := map[string]struct {
			path  paths.Path
			input string
			// The message of the refusal, or empty for a document
			// within the limit.
			want string
		}{
			"aliases to a mapping of a reference document": {
				input: "items: " + repeated(300) + "\n",
				want:  "app.yaml: excessive aliasing",
			},
			"node of aliases to a mapping of a reference document": {
				path:  paths.Current().Child("items"),
				input: "items: " + repeated(300) + "\n",
				want:  "app.yaml:1:9: $.items: excessive aliasing",
			},
			"aliases to an anchor on a tagged alias to a reference document": {
				input: "local: &local !foo *defaults\nitems: " +
					strings.ReplaceAll(repeated(300), "*defaults", "*local") + "\n",
				want: "app.yaml: excessive aliasing",
			},
			"a few aliases to a mapping of a reference document": {
				input: "items: " + repeated(2) + "\n",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, tc.input, refs, niceyaml.WithName("app.yaml"))
				if tc.path.Len() > 0 {
					doc = yamltest.At(t, doc, tc.path)
				}

				_, err := doc.Decode[any](t.Context(), niceyaml.WithValidator(v))
				if tc.want == "" {
					require.NoError(t, err)

					return
				}

				require.EqualError(t, err, tc.want)
				requireExcessiveAliasing(t, err)

				// With the alias limit off, the schema reads every use of
				// the mapping and the document conforms.
				trusted := yamltest.FirstDocument(t, tc.input, refs, niceyaml.WithAliasLimit(false))
				if tc.path.Len() > 0 {
					trusted = yamltest.At(t, trusted, tc.path)
				}

				_, err = trusted.Decode[any](t.Context(), niceyaml.WithValidator(v))
				require.NoError(t, err)
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
				excessive: true,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := v.ValidateValue(t.Context(), tc.data)

				switch {
				case tc.excessive:
					requireExcessiveAliasing(t, err)

				case tc.err != nil:
					// A value that contains itself is no fault of a
					// document, since no document decodes to one.
					require.ErrorIs(t, err, tc.err)
					require.NotErrorIs(t, err, schema.ErrExcessiveAliasing)
					assert.False(t, niceyaml.IsInvalid(err))

				default:
					require.NoError(t, err)
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

func TestSchema_PathAnchors(t *testing.T) {
	t.Parallel()

	whole := compileSchema(t, []byte(`{"properties": {"server": {"properties": {"port": {"minimum": 1}}}}}`))
	server := compileSchema(t, []byte(`{"properties": {"port": {"minimum": 1}}}`))

	doc := yamltest.FirstDocument(t, "server:\n  port: 0\n")
	serverNode := yamltest.At(t, doc, paths.Doc().Child("server"))

	// A binding reports the `$` path from the root of the document.
	// ValidateValue binds to a source that holds no document, so its `$`
	// is the value.
	tcs := map[string]struct {
		validate func(t *testing.T) error
		want     string
		wantPath string
	}{
		"value of a document": {
			validate: func(t *testing.T) error {
				t.Helper()

				return whole.ValidateValue(t.Context(), map[string]any{"server": map[string]any{"port": 0}})
			},
			want:     "$.server.port: 0 is less than 1",
			wantPath: "$.server.port",
		},
		"value": {
			validate: func(t *testing.T) error {
				t.Helper()

				return server.ValidateValue(t.Context(), map[string]any{"port": 0})
			},
			want:     "$.port: 0 is less than 1",
			wantPath: "$.port",
		},
		"document": {
			validate: func(t *testing.T) error {
				t.Helper()

				return doc.Validate(t.Context(), whole)
			},
			want:     "2:9: $.server.port: 0 is less than 1",
			wantPath: "$.server.port",
		},
		"scoped Node": {
			validate: func(t *testing.T) error {
				t.Helper()

				return serverNode.Validate(t.Context(), server)
			},
			want:     "2:9: $.server.port: 0 is less than 1",
			wantPath: "$.server.port",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := tc.validate(t)
			require.Error(t, err)

			assert.Equal(t, tc.want, strings.SplitN(niceyaml.FormatError(err, 0), "\n", 2)[0])

			path, ok := niceyaml.NewErrorTree(err).Path()
			require.True(t, ok)
			assert.Equal(t, tc.wantPath, path.String())
		})
	}
}

func TestSchema_ErrorPaths(t *testing.T) {
	t.Parallel()

	// A single violation puts its path on the main error. Several violations
	// leave the main error without a path and expose one path per problem
	// below it. ValidateValue writes each path from `$`, the value.
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

			tree := niceyaml.NewErrorTree(err)

			gotPath, ok := tree.Path()
			assert.Equal(t, tc.wantPath != "", ok)

			if ok {
				assert.Equal(t, tc.wantPath, gotPath.String())
			}

			var gotNestedPaths []string

			for _, child := range tree.Children {
				if nestedPath, ok := child.Path(); ok {
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

	spec := paths.Current().Child("spec")

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

		err := yamltest.At(t, dd, paths.Current().Child("b")).Validate(t.Context(), aliased)

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

			docs := niceyaml.NewSourceFromString("a: 1\n---\nb: [\n").AllDocuments()
			require.Len(t, docs, 2)
			require.ErrorIs(t, docs[1].Err(), niceyaml.ErrSyntax)

			// The second document did not parse, so the schema has no data
			// to check and returns the syntax error.
			assert.Same(t, docs[1].Err(), v.Validate(t.Context(), docs[1]))
			assert.Same(t, docs[1].Err(), docs[1].Validate(t.Context(), v))

			// The first document parsed, so the schema checks it.
			err := v.Validate(t.Context(), docs[0])
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
	itemPath := paths.Current().Child("items").Index(1)

	tcs := map[string]struct {
		input string
		path  paths.Path
		// The message of the error.
		want string
		// The error is the refusal of the alias limit.
		excessive bool
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
			path:  paths.Current().Child("items").Index(0),
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
		// The decode the schema runs refuses the document, and binds the
		// error at the first token of the node.
		"a document past the alias limit": {
			input:     yamltest.AliasLevels(7),
			want:      "menu.yaml:1:1: excessive aliasing",
			excessive: true,
		},
		// The error carries a position already, so the scoped node adds
		// no path to it.
		"a scoped node past the alias limit": {
			input:     yamltest.AliasLevels(7),
			path:      paths.Current().Child("a"),
			want:      "menu.yaml:2:3: excessive aliasing",
			excessive: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			node := yamltest.FirstDocumentWithPath(t, tc.input, "menu.yaml")
			if tc.path.Len() > 0 {
				node = yamltest.At(t, node, tc.path)
			}

			err := v.Validate(t.Context(), node)
			if tc.want == "" {
				require.NoError(t, err)

				return
			}

			require.EqualError(t, err, tc.want)

			if tc.excessive {
				requireExcessiveAliasing(t, err)
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
			items, err := n.Nodes(paths.Current().Child("items").IndexAll())
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

	for _, child := range bound.Members() {
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

	for _, child := range bound.Members() {
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

			violations := ve.Members()
			require.Len(t, violations, tc.members)

			spelled := 0

			for _, violation := range violations {
				var child *niceyaml.Error

				require.ErrorAs(t, violation, &child)

				path, ok := child.Path()
				require.True(t, ok, "violation carries no path")

				if strings.HasPrefix(path.String(), "@.m.0x") {
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
