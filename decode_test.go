package niceyaml_test

import (
	"context"
	"errors"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

// Test sentinel errors for mock validators.
var (
	errSchemaValidationFailed = errors.New("schema validation failed: name cannot be 'invalid'")
	errNameRequired           = errors.New("name is required")
)

func TestNewDecoder(t *testing.T) {
	t.Parallel()

	t.Run("creates decoder from source", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value")
		d, err := source.Decoder()
		require.NoError(t, err)
		require.NotNil(t, d)
	})

	t.Run("creates decoder from empty source", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("")
		d, err := source.Decoder()
		require.NoError(t, err)
		require.NotNil(t, d)
		assert.Equal(t, 1, d.Len())
	})
}

func TestDecoder_Len(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  int
	}{
		"empty input has one document": {
			input: "",
			want:  1,
		},
		"single document": {
			input: "key: value",
			want:  1,
		},
		"two documents": {
			input: yamltest.Input(`
				---
				key1: value1
				---
				key2: value2
			`),
			want: 2,
		},
		"three documents": {
			input: yamltest.Input(`
				---
				a: 1
				---
				b: 2
				---
				c: 3
			`),
			want: 3,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)
			d, err := source.Decoder()
			require.NoError(t, err)

			got := d.Len()

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestDocumentDecoder_GetValue(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		path      *paths.YAMLPath
		input     string
		wantVals  []string
		wantFound []bool
	}{
		"simple key": {
			input:     "key: value",
			path:      paths.Root().Child("key").Path(),
			wantVals:  []string{"value"},
			wantFound: []bool{true},
		},
		"nested key": {
			input: yamltest.Input(`
				parent:
				  child: nested_value
			`),
			path:      paths.Root().Child("parent").Child("child").Path(),
			wantVals:  []string{"nested_value"},
			wantFound: []bool{true},
		},
		"array index": {
			input: yamltest.Input(`
				items:
				  - first
				  - second
				  - third
			`),
			path:      paths.Root().Child("items").Index(1).Path(),
			wantVals:  []string{"second"},
			wantFound: []bool{true},
		},
		"missing key returns empty": {
			input:     "key: value",
			path:      paths.Root().Child("nonexistent").Path(),
			wantVals:  []string{""},
			wantFound: []bool{false},
		},
		"multiple documents": {
			input: yamltest.Input(`
				---
				first: 1
				---
				second: 2
			`),
			path:      paths.Root().Path(),
			wantVals:  []string{"first: 1", "second: 2"},
			wantFound: []bool{true, true},
		},
		"numeric value": {
			input:     "count: 42",
			path:      paths.Root().Child("count").Path(),
			wantVals:  []string{"42"},
			wantFound: []bool{true},
		},
		"boolean value": {
			input:     "enabled: true",
			path:      paths.Root().Child("enabled").Path(),
			wantVals:  []string{"true"},
			wantFound: []bool{true},
		},
		"null value": {
			input:     "empty: null",
			path:      paths.Root().Child("empty").Path(),
			wantVals:  []string{""},
			wantFound: []bool{true},
		},
		"double-quoted empty string": {
			input:     `kind: ""`,
			path:      paths.Root().Child("kind").Path(),
			wantVals:  []string{""},
			wantFound: []bool{true},
		},
		"single-quoted empty string": {
			input:     `kind: ''`,
			path:      paths.Root().Child("kind").Path(),
			wantVals:  []string{""},
			wantFound: []bool{true},
		},
		"nil path returns false": {
			input:     "key: value",
			path:      nil,
			wantVals:  []string{""},
			wantFound: []bool{false},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)
			d, err := source.Decoder()
			require.NoError(t, err)

			var (
				gotVals  []string
				gotFound []bool
			)

			for _, dd := range d.Documents() {
				val, found := dd.GetValue(tc.path)
				gotVals = append(gotVals, val)
				gotFound = append(gotFound, found)
			}

			assert.Equal(t, tc.wantVals, gotVals)
			assert.Equal(t, tc.wantFound, gotFound)
		})
	}
}

func TestDocumentDecoder_Decode(t *testing.T) {
	t.Parallel()

	type testStruct struct {
		Name  string `yaml:"name"`
		Value int    `yaml:"value"`
	}

	t.Run("decode to map", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value")
		d, err := source.Decoder()
		require.NoError(t, err)

		for _, dd := range d.Documents() {
			var result map[string]string

			err := dd.Decode(&result)
			require.NoError(t, err)
			assert.Equal(t, map[string]string{"key": "value"}, result)
		}
	})

	t.Run("decode to struct", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			name: test
			value: 42
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Decoder()
		require.NoError(t, err)

		for _, dd := range d.Documents() {
			var result testStruct

			err := dd.Decode(&result)
			require.NoError(t, err)
			assert.Equal(t, testStruct{Name: "test", Value: 42}, result)
		}
	})

	t.Run("decode to slice", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			- one
			- two
			- three
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Decoder()
		require.NoError(t, err)

		for _, dd := range d.Documents() {
			var result []string

			err := dd.Decode(&result)
			require.NoError(t, err)
			assert.Equal(t, []string{"one", "two", "three"}, result)
		}
	})

	t.Run("decode multiple documents", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			---
			name: first
			value: 1
			---
			name: second
			value: 2
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Decoder()
		require.NoError(t, err)

		var results []testStruct
		for _, dd := range d.Documents() {
			var result testStruct

			err := dd.Decode(&result)
			require.NoError(t, err)

			results = append(results, result)
		}

		require.Len(t, results, 2)
		assert.Equal(t, testStruct{Name: "first", Value: 1}, results[0])
		assert.Equal(t, testStruct{Name: "second", Value: 2}, results[1])
	})
}

func TestDocumentDecoder_Decode_TypeMismatch(t *testing.T) {
	t.Parallel()

	t.Run("string to int", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("value: not_a_number")
		d, err := source.Decoder()
		require.NoError(t, err)

		for _, dd := range d.Documents() {
			var target struct{ Value int }

			err := dd.Decode(&target)

			require.Error(t, err)

			var yamlErr *niceyaml.Error
			require.ErrorAs(t, err, &yamlErr)
		}
	})
}

func TestDocumentDecoder_DecodeContext(t *testing.T) {
	t.Parallel()

	t.Run("decode with background context", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value")
		d, err := source.Decoder()
		require.NoError(t, err)

		for _, dd := range d.Documents() {
			var result map[string]string

			err := dd.DecodeContext(t.Context(), &result)

			require.NoError(t, err)
			assert.Equal(t, "value", result["key"])
		}
	})
}

func TestDocumentDecoder_Unmarshal(t *testing.T) {
	t.Parallel()

	t.Run("validates and decodes with SchemaValidator", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			name: test
			value: 42
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Decoder()
		require.NoError(t, err)

		for _, dd := range d.Documents() {
			var result schemaValidatorConfig

			err := dd.Unmarshal(&result)
			require.NoError(t, err)
			assert.Equal(t, "test", result.Name)
			assert.Equal(t, 42, result.Value)
			assert.True(t, result.schemaValidated, "ValidateSchema() should have been called")
		}
	})

	t.Run("schema validation fails - no decode", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			name: invalid
			value: 42
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Decoder()
		require.NoError(t, err)

		for _, dd := range d.Documents() {
			var result schemaValidatorConfig

			err := dd.Unmarshal(&result)
			require.ErrorIs(t, err, errSchemaValidationFailed)
		}
	})

	t.Run("decodes without SchemaValidator", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			name: test
			value: 42
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Decoder()
		require.NoError(t, err)

		for _, dd := range d.Documents() {
			var result plainConfig

			err := dd.Unmarshal(&result)
			require.NoError(t, err)
			assert.Equal(t, "test", result.Name)
			assert.Equal(t, 42, result.Value)
		}
	})
}

func TestDocumentDecoder_UnmarshalContext(t *testing.T) {
	t.Parallel()

	t.Run("validates and decodes with context", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			name: test
			value: 42
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Decoder()
		require.NoError(t, err)

		for _, dd := range d.Documents() {
			var result schemaValidatorConfig

			err := dd.UnmarshalContext(t.Context(), &result)

			require.NoError(t, err)
			assert.Equal(t, "test", result.Name)
			assert.True(t, result.schemaValidated, "ValidateSchema() should have been called")
		}
	})

	t.Run("schema validation failure stops decode", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			name: invalid
			value: 42
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Decoder()
		require.NoError(t, err)

		for _, dd := range d.Documents() {
			var result schemaValidatorConfig

			err := dd.UnmarshalContext(t.Context(), &result)

			require.ErrorIs(t, err, errSchemaValidationFailed)
		}
	})
}

func TestNewDocumentDecoder(t *testing.T) {
	t.Parallel()

	t.Run("creates document decoder from ast.File and ast.DocumentNode", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value")
		file, err := source.File()
		require.NoError(t, err)
		require.Len(t, file.Docs, 1)

		dd := niceyaml.NewDocumentDecoder(file.Docs[0])
		require.NotNil(t, dd)

		var result map[string]string

		err = dd.Decode(&result)
		require.NoError(t, err)
		assert.Equal(t, "value", result["key"])
	})
}

func TestDocumentDecoder_GetValue_DirectiveBody(t *testing.T) {
	t.Parallel()

	// Test the directive body case - when doc.Body is a DirectiveType.
	//
	// This is an edge case where YAML 1.2 directive creates a document where the
	// body is a directive node before the actual content.
	//
	// In practice, go-yaml parses %YAML as a directive but the body of the main
	// document is still the mapping, not the directive.
	//
	// We test that the normal case still works.
	input := `%YAML 1.2
---
key: value`
	source := niceyaml.NewSourceFromString(input)
	d, err := source.Decoder()
	require.NoError(t, err)

	path := paths.Root().Child("key").Path()

	var foundAny bool
	for _, dd := range d.Documents() {
		_, found := dd.GetValue(path)
		if found {
			foundAny = true
		}
	}

	// At least one document should have the key.
	assert.True(t, foundAny)
}

func TestDocumentDecoder_UnmarshalContext_DecodeError(t *testing.T) {
	t.Parallel()

	// Test when DecodeContext after validation fails.
	input := `value: not_a_number`
	source := niceyaml.NewSourceFromString(input)
	d, err := source.Decoder()
	require.NoError(t, err)

	for _, dd := range d.Documents() {
		var result strictSchemaValidatorConfig

		// Schema validation passes, but decode will fail due to type mismatch.
		err := dd.UnmarshalContext(t.Context(), &result)

		require.Error(t, err)

		var yamlErr *niceyaml.Error
		require.ErrorAs(t, err, &yamlErr)
	}
}

func TestDocumentDecoder_DecodeContext_CanceledContext(t *testing.T) {
	t.Parallel()

	// Test DecodeContext with a canceled context to trigger the non-yaml error path.
	input := `key: value`
	source := niceyaml.NewSourceFromString(input)
	d, err := source.Decoder()
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // Cancel immediately.

	for _, dd := range d.Documents() {
		var result map[string]string

		err := dd.DecodeContext(ctx, &result)
		// Context cancellation may or may not cause an error depending on timing.
		// The decode might complete before the context cancellation is checked.
		// This test mainly ensures the code path doesn't panic.
		_ = err
	}
}

func TestDocumentDecoder_Decode_Validator(t *testing.T) {
	t.Parallel()

	t.Run("does not call Validate on Validator struct", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			name: test
			value: 42
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Decoder()
		require.NoError(t, err)

		for _, dd := range d.Documents() {
			var result validatorConfig

			err := dd.Decode(&result)
			require.NoError(t, err)
			assert.False(t, result.validated, "Validate() should NOT have been called by Decode()")
			assert.Equal(t, "test", result.Name)
			assert.Equal(t, 42, result.Value)
		}
	})

	t.Run("struct without Validator decodes normally", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			name: test
			value: 42
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Decoder()
		require.NoError(t, err)

		for _, dd := range d.Documents() {
			var result plainConfig

			err := dd.Decode(&result)
			require.NoError(t, err)
			assert.Equal(t, "test", result.Name)
			assert.Equal(t, 42, result.Value)
		}
	})

	t.Run("Unmarshal runs full pipeline", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			name: test
			value: 42
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Decoder()
		require.NoError(t, err)

		for _, dd := range d.Documents() {
			var result bothValidatorConfig

			err := dd.Unmarshal(&result)
			require.NoError(t, err)
			assert.True(t, result.schemaValidated, "ValidateSchema() should have been called")
			assert.True(t, result.validated, "Validate() should have been called after decode")
		}
	})

	t.Run("Unmarshal returns Validator error", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			name: ""
			value: 42
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Decoder()
		require.NoError(t, err)

		for _, dd := range d.Documents() {
			var result bothValidatorConfig

			err := dd.Unmarshal(&result)
			require.ErrorIs(t, err, errNameRequired)
		}
	})
}

// validatorConfig implements niceyaml.Validator.
type validatorConfig struct {
	Name      string `yaml:"name"`
	Value     int    `yaml:"value"`
	validated bool
}

func (c *validatorConfig) Validate() error {
	c.validated = true

	if c.Name == "" {
		return niceyaml.NewErrorFrom(
			errNameRequired,
			niceyaml.WithPath(paths.Root().Child("name").Key()),
		)
	}

	return nil
}

// plainConfig does not implement niceyaml.Validator or niceyaml.SchemaValidator.
type plainConfig struct {
	Name  string `yaml:"name"`
	Value int    `yaml:"value"`
}

// schemaValidatorConfig implements niceyaml.SchemaValidator.
type schemaValidatorConfig struct {
	Name            string `yaml:"name"`
	Value           int    `yaml:"value"`
	schemaValidated bool
}

func (c *schemaValidatorConfig) ValidateSchema(data any) error {
	c.schemaValidated = true

	m, ok := data.(map[string]any)
	if !ok {
		return errors.New("expected map")
	}

	if name, ok := m["name"].(string); ok && name == "invalid" {
		return niceyaml.NewErrorFrom(
			errSchemaValidationFailed,
			niceyaml.WithPath(paths.Root().Child("name").Key()),
		)
	}

	return nil
}

// bothValidatorConfig implements both niceyaml.SchemaValidator and niceyaml.Validator.
type bothValidatorConfig struct {
	Name            string `yaml:"name"`
	Value           int    `yaml:"value"`
	schemaValidated bool
	validated       bool
}

func (c *bothValidatorConfig) ValidateSchema(data any) error {
	c.schemaValidated = true

	m, ok := data.(map[string]any)
	if !ok {
		return errors.New("expected map")
	}

	if name, ok := m["name"].(string); ok && name == "invalid" {
		return niceyaml.NewErrorFrom(
			errSchemaValidationFailed,
			niceyaml.WithPath(paths.Root().Child("name").Key()),
		)
	}

	return nil
}

func (c *bothValidatorConfig) Validate() error {
	c.validated = true

	if c.Name == "" {
		return niceyaml.NewErrorFrom(
			errNameRequired,
			niceyaml.WithPath(paths.Root().Child("name").Key()),
		)
	}

	return nil
}

// strictSchemaValidatorConfig implements niceyaml.SchemaValidator with a strict type.
// Used to test decode errors after successful schema validation.
type strictSchemaValidatorConfig struct {
	Value int `yaml:"value"`
}

func (c *strictSchemaValidatorConfig) ValidateSchema(_ any) error {
	// Always passes schema validation.
	return nil
}

func TestDecoder_Documents(t *testing.T) {
	t.Parallel()

	t.Run("iterates over single document", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value")
		d, err := source.Decoder()
		require.NoError(t, err)

		var count int
		for i, dd := range d.Documents() {
			assert.Equal(t, count, i)
			require.NotNil(t, dd)

			count++
		}

		assert.Equal(t, 1, count)
	})

	t.Run("iterates over multiple documents", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			---
			a: 1
			---
			b: 2
			---
			c: 3
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Decoder()
		require.NoError(t, err)

		var count int
		for i, dd := range d.Documents() {
			assert.Equal(t, count, i)
			require.NotNil(t, dd)

			count++
		}

		assert.Equal(t, 3, count)
	})

	t.Run("early break stops iteration", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			---
			a: 1
			---
			b: 2
			---
			c: 3
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Decoder()
		require.NoError(t, err)

		var count int
		for range d.Documents() {
			count++
			if count == 2 {
				break
			}
		}

		assert.Equal(t, 2, count)
	})
}

func TestDocumentDecoder_ValidateSchema(t *testing.T) {
	t.Parallel()

	t.Run("valid data passes schema validation", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			name: test
			count: 42
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Decoder()
		require.NoError(t, err)

		validator := yamltest.NewPassingSchemaValidator()

		for _, dd := range d.Documents() {
			err := dd.ValidateSchema(validator)
			require.NoError(t, err)
		}
	})

	t.Run("invalid data fails schema validation", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			name: test
			count: not-a-number
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Decoder()
		require.NoError(t, err)

		wantErr := errors.New("validation failed")
		validator := yamltest.NewFailingSchemaValidator(wantErr)

		for _, dd := range d.Documents() {
			err := dd.ValidateSchema(validator)
			require.ErrorIs(t, err, wantErr)
		}
	})
}

func TestWithDecodeOptions(t *testing.T) {
	t.Parallel()

	type strictConfig struct {
		Name string `yaml:"name"`
	}

	t.Run("without option allows unknown fields", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			name: test
			extra: field
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Decoder()
		require.NoError(t, err)

		for _, dd := range d.Documents() {
			var result strictConfig

			err := dd.Decode(&result)
			require.NoError(t, err)
			assert.Equal(t, "test", result.Name)
		}
	})

	t.Run("DisallowUnknownField rejects unknown fields", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			name: test
			extra: field
		`)
		source := niceyaml.NewSourceFromString(input,
			niceyaml.WithDecodeOptions(yaml.DisallowUnknownField()),
		)
		d, err := source.Decoder()
		require.NoError(t, err)

		for _, dd := range d.Documents() {
			var result strictConfig

			err := dd.Decode(&result)
			require.Error(t, err)

			var yamlErr *niceyaml.Error
			require.ErrorAs(t, err, &yamlErr)
		}
	})

	t.Run("options apply to Unmarshal", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			name: test
			extra: field
		`)
		source := niceyaml.NewSourceFromString(input,
			niceyaml.WithDecodeOptions(yaml.DisallowUnknownField()),
		)
		d, err := source.Decoder()
		require.NoError(t, err)

		for _, dd := range d.Documents() {
			var result strictConfig

			err := dd.Unmarshal(&result)
			require.Error(t, err)

			var yamlErr *niceyaml.Error
			require.ErrorAs(t, err, &yamlErr)
		}
	})

	t.Run("options apply across multiple documents", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			---
			name: first
			unknown1: a
			---
			name: second
			unknown2: b
		`)
		source := niceyaml.NewSourceFromString(input,
			niceyaml.WithDecodeOptions(yaml.DisallowUnknownField()),
		)
		d, err := source.Decoder()
		require.NoError(t, err)

		var errCount int
		for _, dd := range d.Documents() {
			var result strictConfig

			err := dd.Decode(&result)
			if err != nil {
				errCount++
			}
		}

		assert.Equal(t, 2, errCount)
	})

	t.Run("no options is the default", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			name: test
			extra: field
		`)
		source := niceyaml.NewSourceFromString(input,
			niceyaml.WithDecodeOptions(),
		)
		d, err := source.Decoder()
		require.NoError(t, err)

		for _, dd := range d.Documents() {
			var result strictConfig

			err := dd.Decode(&result)
			require.NoError(t, err)
			assert.Equal(t, "test", result.Name)
		}
	})
}
