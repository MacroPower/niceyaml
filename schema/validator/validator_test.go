package validator_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macropower/niceyaml"
	"github.com/macropower/niceyaml/schema/validator"
	"github.com/macropower/niceyaml/yamltest"
)

func TestValidationError_Error(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		err  error
		want string
	}{
		"with path": {
			err: niceyaml.NewError(
				errors.New("value is required"),
				niceyaml.WithPath(
					niceyaml.NewPathBuilder().Child("field").Child("subfield").Build(),
					niceyaml.PathKey,
				),
			),
			want: "at $.field.subfield: value is required",
		},
		"without path": {
			err:  niceyaml.NewError(errors.New("value is required")),
			want: "value is required",
		},
		"empty error": {
			err: niceyaml.NewError(
				nil,
				niceyaml.WithPath(niceyaml.NewPathBuilder().Child("field").Build(), niceyaml.PathKey),
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
		input    any
		wantPath string
		wantErr  bool
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
			wantPath: "$",
			wantErr:  true,
		},
		"wrong type for name": {
			input: map[string]any{
				"name": 123,
				"age":  30,
			},
			wantPath: "$.name",
			wantErr:  true,
		},
		"wrong type for age": {
			input: map[string]any{
				"name": "Kallistō",
				"age":  "thirty",
			},
			wantPath: "$.age",
			wantErr:  true,
		},
		"invalid array item": {
			input: map[string]any{
				"name":  "John",
				"items": []any{"valid", 123, "also valid"},
			},
			wantPath: "$.items[1]",
			wantErr:  true,
		},
		"nested object validation error": {
			input: map[string]any{
				"name": "Kallistō",
				"nested": map[string]any{
					"notValue": "something",
				},
			},
			wantPath: "$.nested.notValue",
			wantErr:  true,
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
			wantPath: "$.users[1].id",
			wantErr:  true,
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
			wantPath: "$.users[0].profile",
			wantErr:  true,
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
			wantPath: "$.users[0].profile.preferences[1]",
			wantErr:  true,
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
			wantPath: "$.matrix[1][1]",
			wantErr:  true,
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
			wantPath: "$.users[1]",
			wantErr:  true,
		},
		"additional property at root": {
			input: map[string]any{
				"name":      "John",
				"extraProp": "not allowed",
			},
			wantPath: "$.extraProp",
			wantErr:  true,
		},
		"additional property in nested object": {
			input: map[string]any{
				"name": "Kallistō",
				"nested": map[string]any{
					"value":       "valid",
					"extraNested": "not allowed",
				},
			},
			wantPath: "$.nested.extraNested",
			wantErr:  true,
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
			wantPath: "$.users[0].extraUserProp",
			wantErr:  true,
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
				assert.Equal(t, tc.wantPath, validationErr.Path())
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
		input    string
		wantPath string
		wantErr  bool
	}{
		"valid data": {
			input: yamltest.Input(`
				name: Kallisto
				age: 30
			`),
		},
		"missing required field": {
			input:    yamltest.Input(`age: 30`),
			wantPath: "$",
			wantErr:  true,
		},
		"wrong type for name": {
			input: yamltest.Input(`
				name: 123
				age: 30
			`),
			wantPath: "$.name",
			wantErr:  true,
		},
		"wrong type for age": {
			input: yamltest.Input(`
				name: Kallisto
				age: thirty
			`),
			wantPath: "$.age",
			wantErr:  true,
		},
		"invalid array item": {
			input: yamltest.Input(`
				name: John
				items:
				  - valid
				  - 123
				  - also valid
			`),
			wantPath: "$.items[1]",
			wantErr:  true,
		},
		"nested object validation error": {
			input: yamltest.Input(`
				name: Kallisto
				nested:
				  notValue: something
			`),
			wantPath: "$.nested.notValue",
			wantErr:  true,
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
			wantPath: "$.users[1].id",
			wantErr:  true,
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
			wantPath: "$.users[0].profile",
			wantErr:  true,
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
			wantPath: "$.users[0].profile.preferences[1]",
			wantErr:  true,
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
			wantPath: "$.matrix[1][1]",
			wantErr:  true,
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
			wantPath: "$.users[1]",
			wantErr:  true,
		},
		"additional property at root": {
			input: yamltest.Input(`
				name: John
				extraProp: not allowed
			`),
			wantPath: "$.extraProp",
			wantErr:  true,
		},
		"additional property in nested object": {
			input: yamltest.Input(`
				name: Kallisto
				nested:
				  value: valid
				  extraNested: not allowed
			`),
			wantPath: "$.nested.extraNested",
			wantErr:  true,
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
			wantPath: "$.users[0].extraUserProp",
			wantErr:  true,
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
					assert.Equal(t, tc.wantPath, validationErr.Path())
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

func TestNewValidator_BooleanSchema(t *testing.T) {
	t.Parallel()

	// Boolean schemas are valid in JSON Schema: true accepts everything, false rejects everything.
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
