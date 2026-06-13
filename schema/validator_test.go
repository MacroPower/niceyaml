package schema_test

import (
	"errors"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/jsonschema"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/schema"
)

// newValidator compiles schemaData and wraps it as a [niceyaml.SchemaValidator],
// failing the test if the schema does not compile.
func newValidator(t *testing.T, schemaData []byte) niceyaml.SchemaValidator {
	t.Helper()

	v, err := jsonschema.CompileJSON(t.Context(), schemaData)
	require.NoError(t, err)

	return schema.NewValidator(v)
}

func TestValidator_Validate(t *testing.T) {
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

	v := newValidator(t, schemaData)

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

			err := v.ValidateSchema(tc.input)

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

func TestValidator_ValidateWithDecoder(t *testing.T) {
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

	v := newValidator(t, schemaData)

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
			d, err := source.Decoder()
			require.NoError(t, err)

			for _, dd := range d.Documents() {
				err = dd.ValidateSchema(v)

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

func TestValidator_PathTarget(t *testing.T) {
	t.Parallel()

	// This test verifies that the validator correctly chooses key vs value
	// highlighting based on the type of validation error, by checking which
	// part of the YAML gets wrapped with the error overlay style.

	newXMLPrinter := func() *niceyaml.Printer {
		return niceyaml.NewPrinter(
			niceyaml.WithStyles(yamltest.NewXMLStyles()),
			niceyaml.WithGutter(niceyaml.NoGutter()),
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
			// reached through the instance segments leading to it.
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
			// property with keyword "propertyNames", so the bad key is targeted.
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

			v := newValidator(t, []byte(tc.schema))

			source := niceyaml.NewSourceFromString(tc.input)
			d, err := source.Decoder()
			require.NoError(t, err)

			for _, dd := range d.Documents() {
				err = dd.ValidateSchema(v)
				require.Error(t, err)

				var validationErr *niceyaml.Error

				require.ErrorAs(t, err, &validationErr)

				validationErr.SetOption(
					niceyaml.WithSource(source),
					niceyaml.WithPrinter(newXMLPrinter()),
				)

				assert.Contains(t, validationErr.Error(), tc.wantContains,
					"expected error output to contain specific highlighting pattern")
			}
		})
	}
}

func TestValidator_NonFiniteFloats(t *testing.T) {
	t.Parallel()

	// YAML decodes .nan/.inf into non-finite float64 values. These are not
	// JSON-encodable, but the validator treats them as numbers rather than
	// surfacing an opaque marshaling error.
	v := newValidator(t, []byte(`{
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

			err := v.ValidateSchema(map[string]any{"x": value})
			require.NoError(t, err)
			assert.NotErrorIs(t, err, schema.ErrValidate)
		})
	}
}

func TestValidator_BooleanSchema(t *testing.T) {
	t.Parallel()

	// Boolean schemas are valid in JSON Schema: true accepts everything, false
	// rejects everything.
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

			v := newValidator(t, tc.input)

			for _, data := range testData {
				err := v.ValidateSchema(data)
				if tc.wantAcceptsAll {
					assert.NoError(t, err)
				} else {
					assert.Error(t, err)
				}
			}
		})
	}
}

func TestValidator_SubErrorAnnotations(t *testing.T) {
	t.Parallel()

	// Sub-errors are rendered as annotations with their own paths.
	tcs := map[string]struct {
		schema           string
		input            string
		wantAnnotations  []string // Substrings that should appear in annotation output.
		wantNestedErrors int
	}{
		"single sub-error annotation": {
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
			wantNestedErrors: 1,
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
			wantNestedErrors: 1,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			v := newValidator(t, []byte(tc.schema))

			source := niceyaml.NewSourceFromString(tc.input)
			d, err := source.Decoder()
			require.NoError(t, err)

			for _, dd := range d.Documents() {
				err = dd.ValidateSchema(v)
				require.Error(t, err)

				var validationErr *niceyaml.Error

				require.ErrorAs(t, err, &validationErr)

				validationErr.SetOption(niceyaml.WithSource(source))

				errOutput := validationErr.Error()

				for _, annotation := range tc.wantAnnotations {
					assert.Contains(t, errOutput, annotation,
						"expected error output to contain annotation text")
				}

				// Unwrap includes the main error plus its nested errors.
				unwrapped := validationErr.Unwrap()
				nestedCount := len(unwrapped) - 1
				assert.Equal(t, tc.wantNestedErrors, nestedCount,
					"expected %d nested errors, got %d", tc.wantNestedErrors, nestedCount)
			}
		})
	}
}

func TestValidator_ErrorMessages(t *testing.T) {
	t.Parallel()

	// A single failure uses the concrete message; several use a summary.

	t.Run("single validation error uses concrete message", func(t *testing.T) {
		t.Parallel()

		v := newValidator(t, []byte(`{
			"type": "object",
			"properties": {
				"name": {"type": "string"}
			}
		}`))

		err := v.ValidateSchema(map[string]any{"name": 123})
		require.Error(t, err)

		assert.NotContains(t, err.Error(), "validation failed")
		assert.Contains(t, err.Error(), `expected "string", got "integer"`)
	})

	t.Run("multiple validation errors use summary message", func(t *testing.T) {
		t.Parallel()

		v := newValidator(t, []byte(`{
			"type": "object",
			"properties": {
				"name": {"type": "string"},
				"age": {"type": "number"}
			}
		}`))

		err := v.ValidateSchema(map[string]any{"name": 123, "age": "thirty"})
		require.Error(t, err)

		assert.Contains(t, err.Error(), "validation failed at 2 locations")
	})
}

func TestValidator_UnwrapSubErrorPaths(t *testing.T) {
	t.Parallel()

	// Paths can be obtained from sub-errors via Unwrap.
	tcs := map[string]struct {
		schema    string
		input     any
		wantPaths []string // Expected paths from nested errors.
	}{
		"type error has path on sub-error": {
			schema:    `{"type": "object", "properties": {"name": {"type": "string"}}}`,
			input:     map[string]any{"name": 123},
			wantPaths: []string{"$.name"},
		},
		"additional property has path on sub-error": {
			schema:    `{"type": "object", "properties": {"name": {"type": "string"}}, "additionalProperties": false}`,
			input:     map[string]any{"name": "valid", "extra": "invalid"},
			wantPaths: []string{"$.extra"},
		},
		"nested validation error has path on sub-error": {
			schema:    `{"type": "object", "properties": {"user": {"type": "object", "properties": {"age": {"type": "integer"}}}}}`,
			input:     map[string]any{"user": map[string]any{"age": "notanumber"}},
			wantPaths: []string{"$.user.age"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			v := newValidator(t, []byte(tc.schema))

			err := v.ValidateSchema(tc.input)
			require.Error(t, err)

			var validationErr *niceyaml.Error

			require.ErrorAs(t, err, &validationErr)

			// The top-level error carries no path; the nested errors do.
			assert.Empty(t, validationErr.Path())

			unwrapped := validationErr.Unwrap()
			require.NotEmpty(t, unwrapped)

			var gotPaths []string

			for _, uerr := range unwrapped {
				var nestedErr *niceyaml.Error

				if errors.As(uerr, &nestedErr) && nestedErr.Path() != "" {
					gotPaths = append(gotPaths, nestedErr.Path())
				}
			}

			assert.ElementsMatch(t, tc.wantPaths, gotPaths)
		})
	}
}
