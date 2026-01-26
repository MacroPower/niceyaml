package validator_test

import (
	"errors"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"jacobcolvin.com/niceyaml"
	"jacobcolvin.com/niceyaml/paths"
	"jacobcolvin.com/niceyaml/schema/validator"
	"jacobcolvin.com/niceyaml/yamltest"
)

// mockCompiler implements validator.Compiler for testing.
type mockCompiler struct {
	addResourceCalled bool
	compileCalled     bool
	addResourceErr    error
	compileErr        error

	url    string
	schema any
}

func (m *mockCompiler) AddResource(url string, doc any) error {
	m.addResourceCalled = true
	if m.addResourceErr != nil {
		return m.addResourceErr
	}

	m.url = url
	m.schema = doc

	return nil
}

func (m *mockCompiler) Compile(_ string) (validator.Schema, error) {
	m.compileCalled = true
	if m.compileErr != nil {
		return nil, m.compileErr
	}

	c := jsonschema.NewCompiler()
	// Test helper: panics on unexpected errors since this is for controlled test scenarios.
	_ = c.AddResource(m.url, m.schema) //nolint:errcheck // test helper

	schema, _ := c.Compile(m.url) //nolint:errcheck // test helper

	return &mockSchema{schema: schema}, nil
}

// mockSchema implements validator.Schema for testing.
type mockSchema struct {
	schema *jsonschema.Schema
}

func (m *mockSchema) Validate(data any) error {
	//nolint:wrapcheck // Errors returned must be unwrapped for type assertions.
	return m.schema.Validate(data)
}

func TestValidationError_Error(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		err  error
		want string
	}{
		"with path": {
			err: niceyaml.NewError(
				"value is required",
				niceyaml.WithPath(
					paths.Root().Child("field", "subfield").Key(),
				),
			),
			want: "at $.field.subfield: value is required",
		},
		"without path": {
			err:  niceyaml.NewError("value is required"),
			want: "value is required",
		},
		"empty error": {
			err: niceyaml.NewErrorFrom(
				nil,
				niceyaml.WithPath(paths.Root().Child("field").Key()),
			),
			want: "",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.err.Error()
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestNewValidator(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input []byte
		want  error
	}{
		"valid schema": {
			input: []byte(`{
				"type": "object",
				"properties": {
					"name": {"type": "string"},
					"age": {"type": "number"}
				},
				"required": ["name"]
			}`),
			want: nil,
		},
		"invalid json": {
			input: []byte(`{"invalid": json}`),
			want:  validator.ErrUnmarshalSchema,
		},
		"invalid schema": {
			input: []byte(`{"type": "invalid_type"}`),
			want:  validator.ErrCompileSchema,
		},
		"empty schema": {
			input: []byte(`{}`),
			want:  nil,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			v, err := validator.New("test", tc.input)

			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)
				assert.Nil(t, v)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, v)
			}
		})
	}
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

	v, err := validator.New("test", schemaData)
	require.NoError(t, err)

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
						"id":    1,
						"email": "kallisto@example.com",
						"profile": map[string]any{
							"firstName": "Kallistō",
							"lastName":  "Lykaonis",
						},
					},
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
		"invalid preference in deeply nested array": {
			input: map[string]any{
				"name": "Kallistō",
				"users": []any{
					map[string]any{
						"id":    1,
						"email": "kallisto@example.com",
						"profile": map[string]any{
							"firstName": "Kallistō",
							"lastName":  "Lykaonis",
							"preferences": []any{
								"dark_mode",
								123, // Should be string.
								"notifications",
							},
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
					[]any{7, 8, 9},
				},
			},
		},
		"invalid element in 2D array": {
			input: map[string]any{
				"name": "Kallistō",
				"matrix": []any{
					[]any{1, 2, 3},
					[]any{4, "invalid", 6}, // Should be number.
					[]any{7, 8, 9},
				},
			},
			wantErr: true,
		},
		"missing email in second user": {
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
						"id": 2,
						// Missing email.
						"profile": map[string]any{
							"firstName": "Aëllo",
							"lastName":  "Thaumantias",
						},
					},
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
		"additional property in array object": {
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
						"extraUserProp": "not allowed",
					},
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

	v, err := validator.New("test", schemaData)
	require.NoError(t, err)

	tcs := map[string]struct {
		input   string
		wantErr bool
	}{
		"valid data": {
			input: yamltest.Input(`
				name: Kallisto
				age: 30
			`),
		},
		"missing required field": {
			input:   yamltest.Input(`age: 30`),
			wantErr: true,
		},
		"wrong type for name": {
			input: yamltest.Input(`
				name: 123
				age: 30
			`),
			wantErr: true,
		},
		"wrong type for age": {
			input: yamltest.Input(`
				name: Kallisto
				age: thirty
			`),
			wantErr: true,
		},
		"invalid array item": {
			input: yamltest.Input(`
				name: John
				items:
				  - valid
				  - 123
				  - also valid
			`),
			wantErr: true,
		},
		"nested object validation error": {
			input: yamltest.Input(`
				name: Kallisto
				nested:
				  notValue: something
			`),
			wantErr: true,
		},
		"valid array of objects": {
			input: yamltest.Input(`
				name: Kallisto
				users:
				  - id: 1
				    email: kallisto@example.com
				    profile:
				      firstName: Kallisto
				      lastName: Lykaonis
				  - id: 2
				    email: aello@example.com
				    profile:
				      firstName: Jane
				      lastName: Thaumantias
				      preferences:
				        - dark_mode
				        - notifications
			`),
		},
		"invalid object in array": {
			input: yamltest.Input(`
				name: Kallisto
				users:
				  - id: 1
				    email: kallisto@example.com
				    profile:
				      firstName: Kallisto
				      lastName: Lykaonis
				  - id: invalid
				    email: aello@example.com
				    profile:
				      firstName: Aello
				      lastName: Thaumantias
			`),
			wantErr: true,
		},
		"missing required field in nested object within array": {
			input: yamltest.Input(`
				name: Kallisto
				users:
				  - id: 1
				    email: kallisto@example.com
				    profile:
				      firstName: Kallisto
			`),
			wantErr: true,
		},
		"invalid preference in deeply nested array": {
			input: yamltest.Input(`
				name: Kallisto
				users:
				  - id: 1
				    email: kallisto@example.com
				    profile:
				      firstName: Kallisto
				      lastName: Lykaonis
				      preferences:
				        - dark_mode
				        - 123
				        - notifications
			`),
			wantErr: true,
		},
		"valid matrix (2D array)": {
			input: yamltest.Input(`
				name: Kallisto
				matrix:
				  - [1, 2, 3]
				  - [4, 5, 6]
				  - [7, 8, 9]
			`),
		},
		"invalid element in 2D array": {
			input: yamltest.Input(`
				name: Kallisto
				matrix:
				  - [1, 2, 3]
				  - [4, invalid, 6]
				  - [7, 8, 9]
			`),
			wantErr: true,
		},
		"missing email in second user": {
			input: yamltest.Input(`
				name: Kallisto
				users:
				  - id: 1
				    email: kallisto@example.com
				    profile:
				      firstName: Kallisto
				      lastName: Lykaonis
				  - id: 2
				    profile:
				      firstName: Aello
				      lastName: Thaumantias
			`),
			wantErr: true,
		},
		"additional property at root": {
			input: yamltest.Input(`
				name: John
				extraProp: not allowed
			`),
			wantErr: true,
		},
		"additional property in nested object": {
			input: yamltest.Input(`
				name: Kallisto
				nested:
				  value: valid
				  extraNested: not allowed
			`),
			wantErr: true,
		},
		"additional property in array object": {
			input: yamltest.Input(`
				name: Kallisto
				users:
				  - id: 1
				    email: kallisto@example.com
				    profile:
				      firstName: Kallisto
				      lastName: Lykaonis
				    extraUserProp: not allowed
			`),
			wantErr: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)
			file, err := source.File()
			require.NoError(t, err)

			d := niceyaml.NewDecoder(file)

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

func TestMustNewValidator(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input        []byte
		validateData any
		wantPanic    bool
	}{
		"valid schema returns validator": {
			input:        []byte(`{"type": "object", "properties": {"name": {"type": "string"}}}`),
			validateData: map[string]any{"name": "test"},
		},
		"panics with invalid json": {
			input:     []byte(`{"invalid": json}`),
			wantPanic: true,
		},
		"panics with invalid schema type": {
			input:     []byte(`{"type": "invalid_type"}`),
			wantPanic: true,
		},
		"empty schema does not panic": {
			input: []byte(`{}`),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if tc.wantPanic {
				assert.Panics(t, func() {
					validator.MustNew("test", tc.input)
				})

				return
			}

			v := validator.MustNew("test", tc.input)
			assert.NotNil(t, v)

			if tc.validateData != nil {
				err := v.ValidateSchema(tc.validateData)
				assert.NoError(t, err)
			}
		})
	}
}

func TestNewValidator_InvalidSchemaTypes(t *testing.T) {
	t.Parallel()

	// Test with JSON documents that are valid JSON but not valid schema types.
	// JSON Schema must be objects (maps) or booleans - not arrays, strings, numbers, or null.
	// These fail during schema compilation with metaschema validation.
	tcs := map[string]struct {
		input []byte
		want  error
	}{
		"array as schema": {
			input: []byte(`["not", "a", "schema"]`),
			want:  validator.ErrCompileSchema,
		},
		"string as schema": {
			input: []byte(`"not a schema"`),
			want:  validator.ErrCompileSchema,
		},
		"number as schema": {
			input: []byte(`42`),
			want:  validator.ErrCompileSchema,
		},
		"null as schema": {
			input: []byte(`null`),
			want:  validator.ErrCompileSchema,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			v, err := validator.New("test", tc.input)
			require.ErrorIs(t, err, tc.want)
			assert.Nil(t, v)
		})
	}
}

func TestValidator_PathTarget(t *testing.T) {
	t.Parallel()

	// This test verifies that the validator correctly chooses PathKey vs PathValue
	// based on the type of validation error.
	//
	// We verify by checking which part of the YAML gets highlighted with the error
	// overlay style.

	newXMLPrinter := func() *niceyaml.Printer {
		return niceyaml.NewPrinter(
			niceyaml.WithStyles(yamltest.NewXMLStyles()),
			niceyaml.WithGutter(niceyaml.NoGutter()),
		)
	}

	tcs := map[string]struct {
		schema       string
		input        string
		wantKeyErr   bool   // True if key should be highlighted, false if value.
		wantContains string // Substring that should appear in error output.
	}{
		"type error highlights value": {
			schema: `{
				"type": "object",
				"properties": {
					"name": {"type": "string"}
				}
			}`,
			input: yamltest.Input(`
				name: 123
			`),
			wantKeyErr:   false,
			wantContains: "<generic-error>123</generic-error>",
		},
		"additional property highlights key": {
			schema: `{
				"type": "object",
				"properties": {
					"name": {"type": "string"}
				},
				"additionalProperties": false
			}`,
			input: yamltest.Input(`
				name: valid
				extra: notAllowed
			`),
			wantKeyErr:   true,
			wantContains: "<generic-error>extra</generic-error>",
		},
		"required error highlights parent key": {
			schema: `{
				"type": "object",
				"properties": {
					"name": {"type": "string"}
				},
				"required": ["name"]
			}`,
			input: yamltest.Input(`
				other: value
			`),
			wantKeyErr: true,
		},
		"enum error highlights value": {
			schema: `{
				"type": "object",
				"properties": {
					"status": {"enum": ["active", "inactive"]}
				}
			}`,
			input: yamltest.Input(`
				status: unknown
			`),
			wantKeyErr:   false,
			wantContains: "<generic-error>unknown</generic-error>",
		},
		"minimum error highlights value": {
			schema: `{
				"type": "object",
				"properties": {
					"age": {"type": "integer", "minimum": 0}
				}
			}`,
			input: yamltest.Input(`
				age: -5
			`),
			wantKeyErr:   false,
			wantContains: "<generic-error>-5</generic-error>",
		},
		"pattern error highlights value": {
			schema: `{
				"type": "object",
				"properties": {
					"email": {"type": "string", "pattern": "^[a-z]+@[a-z]+\\.[a-z]+$"}
				}
			}`,
			input: yamltest.Input(`
				email: notanemail
			`),
			wantKeyErr:   false,
			wantContains: "<generic-error>notanemail</generic-error>",
		},
		"minItems error highlights key": {
			schema: `{
				"type": "object",
				"properties": {
					"items": {"type": "array", "minItems": 2}
				}
			}`,
			input: yamltest.Input(`
				items:
				  - one
			`),
			wantKeyErr:   true,
			wantContains: "<generic-error>items</generic-error>",
		},
		"maxItems error highlights key": {
			schema: `{
				"type": "object",
				"properties": {
					"items": {"type": "array", "maxItems": 1}
				}
			}`,
			input: yamltest.Input(`
				items:
				  - one
				  - two
			`),
			wantKeyErr:   true,
			wantContains: "<generic-error>items</generic-error>",
		},
		"nested type error highlights value": {
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
			input: yamltest.Input(`
				user:
				  age: notanumber
			`),
			wantKeyErr:   false,
			wantContains: "<generic-error>notanumber</generic-error>",
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
			input: yamltest.Input(`
				numbers:
				  - 1
				  - notanumber
				  - 3
			`),
			wantKeyErr:   false,
			wantContains: "<generic-error>notanumber</generic-error>",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			v, err := validator.New("test", []byte(tc.schema))
			require.NoError(t, err)

			source := niceyaml.NewSourceFromString(tc.input)
			file, err := source.File()
			require.NoError(t, err)

			d := niceyaml.NewDecoder(file)

			for _, dd := range d.Documents() {
				err = dd.ValidateSchema(v)
				require.Error(t, err)

				var validationErr *niceyaml.Error
				require.ErrorAs(t, err, &validationErr)

				// Apply source and printer to get annotated output.
				validationErr.SetOption(
					niceyaml.WithSource(source),
					niceyaml.WithPrinter(newXMLPrinter()),
				)

				errOutput := validationErr.Error()

				if tc.wantContains != "" {
					assert.Contains(t, errOutput, tc.wantContains,
						"expected error output to contain specific highlighting pattern")
				}
			}
		})
	}
}

func TestNewValidator_BooleanSchema(t *testing.T) {
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

			v, err := validator.New("test", tc.input)
			require.NoError(t, err)
			require.NotNil(t, v)

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

	// Test that sub-errors are rendered as annotations with their own paths.
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
			input: yamltest.Input(`
				name: 123
			`),
			wantAnnotations:  []string{"got number, want string"},
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
			input: yamltest.Input(`
				other: value
			`),
			wantAnnotations:  []string{"missing properties", "first", "second"},
			wantNestedErrors: 1,
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
			input: yamltest.Input(`
				user:
				  age: notanumber
			`),
			wantAnnotations:  []string{"got string, want integer"},
			wantNestedErrors: 1,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			v, err := validator.New("test", []byte(tc.schema))
			require.NoError(t, err)

			source := niceyaml.NewSourceFromString(tc.input)
			file, err := source.File()
			require.NoError(t, err)

			d := niceyaml.NewDecoder(file)

			for _, dd := range d.Documents() {
				err = dd.ValidateSchema(v)
				require.Error(t, err)

				var validationErr *niceyaml.Error
				require.ErrorAs(t, err, &validationErr)

				// Apply source to enable annotation rendering.
				validationErr.SetOption(niceyaml.WithSource(source))

				errOutput := validationErr.Error()

				// Verify that expected annotations appear.
				for _, annotation := range tc.wantAnnotations {
					assert.Contains(t, errOutput, annotation,
						"expected error output to contain annotation text")
				}

				// Verify nested error count via Unwrap.
				// Unwrapped includes main error + nested errors.
				unwrapped := validationErr.Unwrap()
				nestedCount := len(unwrapped) - 1
				assert.Equal(t, tc.wantNestedErrors, nestedCount,
					"expected %d nested errors, got %d", tc.wantNestedErrors, nestedCount)
			}
		})
	}
}

func TestNew_WithCompiler(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		compiler          *mockCompiler
		want              error
		wantAddResourceOK bool
		wantCompileOK     bool
	}{
		"custom compiler is used": {
			compiler:          &mockCompiler{},
			wantAddResourceOK: true,
			wantCompileOK:     true,
		},
		"custom compiler AddResource error": {
			compiler: &mockCompiler{
				addResourceErr: errors.New("add resource failed"),
			},
			want:              validator.ErrAddResource,
			wantAddResourceOK: true,
		},
		"custom compiler Compile error": {
			compiler: &mockCompiler{
				compileErr: errors.New("compile failed"),
			},
			want:              validator.ErrCompileSchema,
			wantAddResourceOK: true,
			wantCompileOK:     true,
		},
	}

	schemaData := []byte(`{"type": "object"}`)

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			v, err := validator.New("test", schemaData, validator.WithCompiler(tc.compiler))

			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)
				assert.Nil(t, v)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, v)
			}

			assert.Equal(t, tc.wantAddResourceOK, tc.compiler.addResourceCalled,
				"AddResource called mismatch")
			assert.Equal(t, tc.wantCompileOK, tc.compiler.compileCalled,
				"Compile called mismatch")
		})
	}
}

func TestNew_DefaultCompiler(t *testing.T) {
	t.Parallel()

	// Verify that without WithCompiler, the default jsonschema.NewCompiler is used.
	schemaData := []byte(`{"type": "object", "properties": {"name": {"type": "string"}}}`)

	v, err := validator.New("test", schemaData)
	require.NoError(t, err)
	require.NotNil(t, v)

	// Validate that the schema works correctly.
	err = v.ValidateSchema(map[string]any{"name": "test"})
	require.NoError(t, err)

	err = v.ValidateSchema(map[string]any{"name": 123})
	require.Error(t, err)
}

func TestMustNew_WithCompiler(t *testing.T) {
	t.Parallel()

	// Test that MustNew passes options through to New.
	compiler := &mockCompiler{}

	schemaData := []byte(`{"type": "object"}`)

	v := validator.MustNew("test", schemaData, validator.WithCompiler(compiler))
	assert.NotNil(t, v)
	assert.True(t, compiler.addResourceCalled, "AddResource should be called")
	assert.True(t, compiler.compileCalled, "Compile should be called")
}

func TestMustNew_WithCompiler_Panics(t *testing.T) {
	t.Parallel()

	// Test that MustNew panics when compiler returns an error.
	compiler := &mockCompiler{
		compileErr: errors.New("compile failed"),
	}

	schemaData := []byte(`{"type": "object"}`)

	assert.Panics(t, func() {
		validator.MustNew("test", schemaData, validator.WithCompiler(compiler))
	})
}

func TestValidator_ErrorMessages(t *testing.T) {
	t.Parallel()

	// Tests the summary message behavior of validation errors.
	// Single errors use concrete messages, multiple errors use summary format.

	t.Run("single validation error uses concrete message", func(t *testing.T) {
		t.Parallel()

		schemaData := []byte(`{
			"type": "object",
			"properties": {
				"name": {"type": "string"}
			}
		}`)

		v, err := validator.New("test", schemaData)
		require.NoError(t, err)

		err = v.ValidateSchema(map[string]any{"name": 123})
		require.Error(t, err)

		// Single error should NOT use summary message.
		assert.NotContains(t, err.Error(), "validation failed")
		// Should use the actual error message.
		assert.Contains(t, err.Error(), "got number, want string")
	})

	t.Run("multiple validation errors use summary message", func(t *testing.T) {
		t.Parallel()

		schemaData := []byte(`{
			"type": "object",
			"properties": {
				"name": {"type": "string"},
				"age": {"type": "number"}
			}
		}`)

		v, err := validator.New("test", schemaData)
		require.NoError(t, err)

		// Both fields have wrong types.
		err = v.ValidateSchema(map[string]any{"name": 123, "age": "thirty"})
		require.Error(t, err)

		// Multiple errors should use summary message.
		assert.Contains(t, err.Error(), "validation failed at 2 locations")
	})

	t.Run("multiple errors from schema-level error includes schema name", func(t *testing.T) {
		t.Parallel()

		schemaData := []byte(`{
			"type": "object",
			"properties": {
				"first": {"type": "string"},
				"second": {"type": "string"}
			},
			"required": ["first", "second"]
		}`)

		v, err := validator.New("test.schema.json", schemaData)
		require.NoError(t, err)

		// Provide wrong types for required fields.
		err = v.ValidateSchema(map[string]any{"first": 1, "second": 2})
		require.Error(t, err)

		// Should include schema name in message.
		assert.Contains(t, err.Error(), "test.schema.json")
	})

	t.Run("nested errors are included in error unwrap chain", func(t *testing.T) {
		t.Parallel()

		schemaData := []byte(`{
			"type": "object",
			"properties": {
				"name": {"type": "string"},
				"value": {"type": "number"}
			}
		}`)

		v, err := validator.New("test", schemaData)
		require.NoError(t, err)

		// Both fields have wrong types.
		err = v.ValidateSchema(map[string]any{"name": 123, "value": "string"})
		require.Error(t, err)

		var validationErr *niceyaml.Error
		require.ErrorAs(t, err, &validationErr)

		// Unwrap should include main + nested errors.
		unwrapped := validationErr.Unwrap()
		// At least 3: main error + 2 nested errors.
		assert.GreaterOrEqual(t, len(unwrapped), 3)
	})
}

func TestValidator_UnwrapSubErrorPaths(t *testing.T) {
	t.Parallel()

	// Test that paths can be obtained from sub-errors via Unwrap.
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

			v, err := validator.New("test", []byte(tc.schema))
			require.NoError(t, err)

			err = v.ValidateSchema(tc.input)
			require.Error(t, err)

			var validationErr *niceyaml.Error
			require.ErrorAs(t, err, &validationErr)

			// Top-level error should have empty path.
			assert.Empty(t, validationErr.Path())

			// Unwrap to get nested errors and check their paths.
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
