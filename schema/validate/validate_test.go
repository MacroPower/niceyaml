package validate_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macropower/niceyaml"
	"github.com/macropower/niceyaml/schema/validate"
	"github.com/macropower/niceyaml/yamltest"
)

func TestValidationError_Error(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		err  error
		want string
	}{
		"with path": {
			err: niceyaml.NewError(errors.New("value is required"),
				niceyaml.WithPath(niceyaml.NewPathBuilder().Child("field").Child("subfield").Build()),
			),
			want: "error at $.field.subfield: value is required",
		},
		"without path": {
			err:  niceyaml.NewError(errors.New("value is required")),
			want: "value is required",
		},
		"empty error": {
			err:  niceyaml.NewError(nil, niceyaml.WithPath(niceyaml.NewPathBuilder().Child("field").Build())),
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
		errMsg     string
		schemaData []byte
		wantErr    bool
	}{
		"valid schema": {
			schemaData: []byte(`{
				"type": "object",
				"properties": {
					"name": {"type": "string"},
					"age": {"type": "number"}
				},
				"required": ["name"]
			}`),
			wantErr: false,
		},
		"invalid json": {
			schemaData: []byte(`{"invalid": json}`),
			wantErr:    true,
			errMsg:     "unmarshal schema",
		},
		"invalid schema": {
			schemaData: []byte(`{"type": "invalid_type"}`),
			wantErr:    true,
			errMsg:     "compile schema",
		},
		"empty schema": {
			schemaData: []byte(`{}`),
			wantErr:    false,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			validator, err := validate.NewValidator("test", tc.schemaData)

			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errMsg)
				assert.Nil(t, validator)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, validator)
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

	validator, err := validate.NewValidator("test", schemaData)
	require.NoError(t, err)

	tcs := map[string]struct {
		data         any
		expectedPath string
		wantErr      bool
	}{
		"valid data": {
			data: map[string]any{
				"name": "Kallistō",
				"age":  30,
			},
			wantErr: false,
		},
		"missing required field": {
			data: map[string]any{
				"age": 30,
			},
			wantErr:      true,
			expectedPath: "$",
		},
		"wrong type for name": {
			data: map[string]any{
				"name": 123,
				"age":  30,
			},
			wantErr:      true,
			expectedPath: "$.name",
		},
		"wrong type for age": {
			data: map[string]any{
				"name": "Kallistō",
				"age":  "thirty",
			},
			wantErr:      true,
			expectedPath: "$.age",
		},
		"invalid array item": {
			data: map[string]any{
				"name":  "John",
				"items": []any{"valid", 123, "also valid"},
			},
			wantErr:      true,
			expectedPath: "$.items[1]",
		},
		"nested object validation error": {
			data: map[string]any{
				"name": "Kallistō",
				"nested": map[string]any{
					"notValue": "something",
				},
			},
			wantErr:      true,
			expectedPath: "$.nested.notValue",
		},
		"valid array of objects": {
			data: map[string]any{
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
			wantErr: false,
		},
		"invalid object in array": {
			data: map[string]any{
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
			wantErr:      true,
			expectedPath: "$.users[1].id",
		},
		"missing required field in nested object within array": {
			data: map[string]any{
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
			wantErr:      true,
			expectedPath: "$.users[0].profile",
		},
		"invalid preference in deeply nested array": {
			data: map[string]any{
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
			wantErr:      true,
			expectedPath: "$.users[0].profile.preferences[1]",
		},
		"valid matrix (2D array)": {
			data: map[string]any{
				"name": "Kallistō",
				"matrix": []any{
					[]any{1, 2, 3},
					[]any{4, 5, 6},
					[]any{7, 8, 9},
				},
			},
			wantErr: false,
		},
		"invalid element in 2D array": {
			data: map[string]any{
				"name": "Kallistō",
				"matrix": []any{
					[]any{1, 2, 3},
					[]any{4, "invalid", 6}, // Should be number.
					[]any{7, 8, 9},
				},
			},
			wantErr:      true,
			expectedPath: "$.matrix[1][1]",
		},
		"missing email in second user": {
			data: map[string]any{
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
			wantErr:      true,
			expectedPath: "$.users[1]",
		},
		"additional property at root": {
			data: map[string]any{
				"name":      "John",
				"extraProp": "not allowed",
			},
			wantErr:      true,
			expectedPath: "$.extraProp",
		},
		"additional property in nested object": {
			data: map[string]any{
				"name": "Kallistō",
				"nested": map[string]any{
					"value":       "valid",
					"extraNested": "not allowed",
				},
			},
			wantErr:      true,
			expectedPath: "$.nested.extraNested",
		},
		"additional property in array object": {
			data: map[string]any{
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
			wantErr:      true,
			expectedPath: "$.users[0].extraUserProp",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := validator.Validate(tc.data)

			if tc.wantErr {
				require.Error(t, err)

				var validationErr *niceyaml.Error
				require.ErrorAs(t, err, &validationErr)
				assert.Equal(t, tc.expectedPath, validationErr.GetPath())
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

	validator, err := validate.NewValidator("test", schemaData)
	require.NoError(t, err)

	tcs := map[string]struct {
		input        string
		expectedPath string
		wantErr      bool
	}{
		"valid data": {
			input: yamltest.Input(`
				name: Kallisto
				age: 30
			`),
			wantErr: false,
		},
		"missing required field": {
			input:        `age: 30`,
			wantErr:      true,
			expectedPath: "$",
		},
		"wrong type for name": {
			input: yamltest.Input(`
				name: 123
				age: 30
			`),
			wantErr:      true,
			expectedPath: "$.name",
		},
		"wrong type for age": {
			input: yamltest.Input(`
				name: Kallisto
				age: thirty
			`),
			wantErr:      true,
			expectedPath: "$.age",
		},
		"invalid array item": {
			input: yamltest.Input(`
				name: John
				items:
				  - valid
				  - 123
				  - also valid
			`),
			wantErr:      true,
			expectedPath: "$.items[1]",
		},
		"nested object validation error": {
			input: yamltest.Input(`
				name: Kallisto
				nested:
				  notValue: something
			`),
			wantErr:      true,
			expectedPath: "$.nested.notValue",
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
			wantErr: false,
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
			wantErr:      true,
			expectedPath: "$.users[1].id",
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
			wantErr:      true,
			expectedPath: "$.users[0].profile",
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
			wantErr:      true,
			expectedPath: "$.users[0].profile.preferences[1]",
		},
		"valid matrix (2D array)": {
			input: yamltest.Input(`
				name: Kallisto
				matrix:
				  - [1, 2, 3]
				  - [4, 5, 6]
				  - [7, 8, 9]
			`),
			wantErr: false,
		},
		"invalid element in 2D array": {
			input: yamltest.Input(`
				name: Kallisto
				matrix:
				  - [1, 2, 3]
				  - [4, invalid, 6]
				  - [7, 8, 9]
			`),
			wantErr:      true,
			expectedPath: "$.matrix[1][1]",
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
			wantErr:      true,
			expectedPath: "$.users[1]",
		},
		"additional property at root": {
			input: yamltest.Input(`
				name: John
				extraProp: not allowed
			`),
			wantErr:      true,
			expectedPath: "$.extraProp",
		},
		"additional property in nested object": {
			input: yamltest.Input(`
				name: Kallisto
				nested:
				  value: valid
				  extraNested: not allowed
			`),
			wantErr:      true,
			expectedPath: "$.nested.extraNested",
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
			wantErr:      true,
			expectedPath: "$.users[0].extraUserProp",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)
			file, err := source.Parse()
			require.NoError(t, err)

			d := niceyaml.NewDecoder(file)
			for _, dd := range d.Documents() {
				err = dd.Validate(validator)

				if tc.wantErr {
					require.Error(t, err)

					var validationErr *niceyaml.Error
					require.ErrorAs(t, err, &validationErr)
					assert.Equal(t, tc.expectedPath, validationErr.GetPath())
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
		validateData any
		schemaData   []byte
		wantPanic    bool
	}{
		"valid schema returns validator": {
			schemaData:   []byte(`{"type": "object", "properties": {"name": {"type": "string"}}}`),
			wantPanic:    false,
			validateData: map[string]any{"name": "test"},
		},
		"panics with invalid json": {
			schemaData: []byte(`{"invalid": json}`),
			wantPanic:  true,
		},
		"panics with invalid schema type": {
			schemaData: []byte(`{"type": "invalid_type"}`),
			wantPanic:  true,
		},
		"empty schema does not panic": {
			schemaData: []byte(`{}`),
			wantPanic:  false,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if tc.wantPanic {
				assert.Panics(t, func() {
					validate.MustNewValidator("test", tc.schemaData)
				})

				return
			}

			validator := validate.MustNewValidator("test", tc.schemaData)
			assert.NotNil(t, validator)

			if tc.validateData != nil {
				err := validator.Validate(tc.validateData)
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
		errMsg     string
		schemaData []byte
	}{
		"array as schema": {
			schemaData: []byte(`["not", "a", "schema"]`),
			errMsg:     "compile schema",
		},
		"string as schema": {
			schemaData: []byte(`"not a schema"`),
			errMsg:     "compile schema",
		},
		"number as schema": {
			schemaData: []byte(`42`),
			errMsg:     "compile schema",
		},
		"null as schema": {
			schemaData: []byte(`null`),
			errMsg:     "compile schema",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			validator, err := validate.NewValidator("test", tc.schemaData)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.errMsg)
			assert.Nil(t, validator)
		})
	}
}

func TestNewValidator_BooleanSchema(t *testing.T) {
	t.Parallel()

	// Boolean schemas are valid in JSON Schema: true accepts everything, false rejects everything.
	tcs := map[string]struct {
		schemaData []byte
		acceptsAll bool
	}{
		"true schema accepts all data": {
			schemaData: []byte(`true`),
			acceptsAll: true,
		},
		"false schema rejects all data": {
			schemaData: []byte(`false`),
			acceptsAll: false,
		},
	}

	testData := []any{"anything", 42, map[string]any{"key": "value"}}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			validator, err := validate.NewValidator("test", tc.schemaData)
			require.NoError(t, err)
			require.NotNil(t, validator)

			for _, data := range testData {
				err := validator.Validate(data)
				if tc.acceptsAll {
					assert.NoError(t, err)
				} else {
					assert.Error(t, err)
				}
			}
		})
	}
}
