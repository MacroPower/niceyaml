package niceyaml_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

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
			input: stringtest.Input(`
				---
				key1: value1
				---
				key2: value2
			`),
			want: 2,
		},
		"three documents": {
			input: stringtest.Input(`
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
		path      *paths.Path
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
			input: stringtest.Input(`
				parent:
				  child: nested_value
			`),
			path:      paths.Root().Child("parent").Child("child").Path(),
			wantVals:  []string{"nested_value"},
			wantFound: []bool{true},
		},
		"array index": {
			input: stringtest.Input(`
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
			input: stringtest.Input(`
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
		"anchored value": {
			input:     "kind: &k Pod",
			path:      paths.Root().Child("kind").Path(),
			wantVals:  []string{"Pod"},
			wantFound: []bool{true},
		},
		"aliased value": {
			input: stringtest.Input(`
				base: &b Pod
				kind: *b
			`),
			path:      paths.Root().Child("kind").Path(),
			wantVals:  []string{"Pod"},
			wantFound: []bool{true},
		},
		"key through alias": {
			input: stringtest.Input(`
				base: &b {kind: Pod}
				spec: *b
			`),
			path:      paths.Root().Child("spec", "kind").Path(),
			wantVals:  []string{"Pod"},
			wantFound: []bool{true},
		},
		"key through merge": {
			input: stringtest.Input(`
				base: &b {kind: Pod}
				spec:
				  <<: *b
				  name: x
			`),
			path:      paths.Root().Child("spec", "kind").Path(),
			wantVals:  []string{"Pod"},
			wantFound: []bool{true},
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
			result, err := dd.Decode[map[string]string](t.Context())
			require.NoError(t, err)
			assert.Equal(t, map[string]string{"key": "value"}, result)
		}
	})

	t.Run("decode to struct", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			name: test
			value: 42
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Decoder()
		require.NoError(t, err)

		for _, dd := range d.Documents() {
			result, err := dd.Decode[testStruct](t.Context())
			require.NoError(t, err)
			assert.Equal(t, testStruct{Name: "test", Value: 42}, result)
		}
	})

	t.Run("decode to slice", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			- one
			- two
			- three
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Decoder()
		require.NoError(t, err)

		for _, dd := range d.Documents() {
			result, err := dd.Decode[[]string](t.Context())
			require.NoError(t, err)
			assert.Equal(t, []string{"one", "two", "three"}, result)
		}
	})

	t.Run("decode multiple documents", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
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
			result, err := dd.Decode[testStruct](t.Context())
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
			_, err := dd.Decode[struct{ Value int }](t.Context())

			require.Error(t, err)

			var yamlErr *niceyaml.Error

			require.ErrorAs(t, err, &yamlErr)
		}
	})
}

func TestDocumentDecoder_Unmarshal(t *testing.T) {
	t.Parallel()

	t.Run("validates and decodes with SchemaValidator", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			name: test
			value: 42
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Decoder()
		require.NoError(t, err)

		for _, dd := range d.Documents() {
			result, err := dd.Unmarshal[schemaValidatorConfig](t.Context())
			require.NoError(t, err)
			assert.Equal(t, "test", result.Name)
			assert.Equal(t, 42, result.Value)
			assert.True(t, result.schemaValidated, "ValidateSchema() should have been called")
		}
	})

	t.Run("schema validation fails - no decode", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			name: invalid
			value: 42
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Decoder()
		require.NoError(t, err)

		for _, dd := range d.Documents() {
			_, err := dd.Unmarshal[schemaValidatorConfig](t.Context())
			require.ErrorIs(t, err, errSchemaValidationFailed)
		}
	})

	t.Run("decodes without SchemaValidator", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			name: test
			value: 42
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Decoder()
		require.NoError(t, err)

		for _, dd := range d.Documents() {
			result, err := dd.Unmarshal[plainConfig](t.Context())
			require.NoError(t, err)
			assert.Equal(t, "test", result.Name)
			assert.Equal(t, 42, result.Value)
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

		result, err := dd.Decode[map[string]string](t.Context())
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

func TestDocumentDecoder_Unmarshal_DecodeError(t *testing.T) {
	t.Parallel()

	// Test when the decode after validation fails.
	input := `value: not_a_number`
	source := niceyaml.NewSourceFromString(input)
	d, err := source.Decoder()
	require.NoError(t, err)

	for _, dd := range d.Documents() {
		// Schema validation passes, but decode will fail due to type mismatch.
		_, err := dd.Unmarshal[strictSchemaValidatorConfig](t.Context())

		require.Error(t, err)

		var yamlErr *niceyaml.Error

		require.ErrorAs(t, err, &yamlErr)
	}
}

func TestDocumentDecoder_Decode_CanceledContext(t *testing.T) {
	t.Parallel()

	// Test Decode with a canceled context to trigger the non-yaml error path.
	input := `key: value`
	source := niceyaml.NewSourceFromString(input)
	d, err := source.Decoder()
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // Cancel immediately.

	for _, dd := range d.Documents() {
		_, err := dd.Decode[map[string]string](ctx)
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

		input := stringtest.Input(`
			name: test
			value: 42
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Decoder()
		require.NoError(t, err)

		for _, dd := range d.Documents() {
			result, err := dd.Decode[validatorConfig](t.Context())
			require.NoError(t, err)
			assert.False(t, result.validated, "Validate() should NOT have been called by Decode()")
			assert.Equal(t, "test", result.Name)
			assert.Equal(t, 42, result.Value)
		}
	})

	t.Run("struct without Validator decodes normally", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			name: test
			value: 42
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Decoder()
		require.NoError(t, err)

		for _, dd := range d.Documents() {
			result, err := dd.Decode[plainConfig](t.Context())
			require.NoError(t, err)
			assert.Equal(t, "test", result.Name)
			assert.Equal(t, 42, result.Value)
		}
	})

	t.Run("Unmarshal runs full pipeline", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			name: test
			value: 42
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Decoder()
		require.NoError(t, err)

		for _, dd := range d.Documents() {
			result, err := dd.Unmarshal[bothValidatorConfig](t.Context())
			require.NoError(t, err)
			assert.True(t, result.schemaValidated, "ValidateSchema() should have been called")
			assert.True(t, result.validated, "Validate() should have been called after decode")
		}
	})

	t.Run("Unmarshal returns Validator error", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			name: ""
			value: 42
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Decoder()
		require.NoError(t, err)

		for _, dd := range d.Documents() {
			_, err := dd.Unmarshal[bothValidatorConfig](t.Context())
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

func (c *schemaValidatorConfig) ValidateSchema(_ context.Context, data any) error {
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

func (c *bothValidatorConfig) ValidateSchema(_ context.Context, data any) error {
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

func (c *strictSchemaValidatorConfig) ValidateSchema(_ context.Context, _ any) error {
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

	t.Run("yields the same tokens on every pass", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			---
			a: 1
			---
			b: 2
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Decoder()
		require.NoError(t, err)

		var first, second [][]int

		collect := func() []int {
			var lens []int

			for _, dd := range d.Documents() {
				lens = append(lens, len(dd.Tokens()))
			}

			return lens
		}

		first = append(first, collect())
		second = append(second, collect())
		assert.Equal(t, first, second)
		assert.Equal(t, [][]int{{4, 4}}, first)

		// The tokens are the source's originals, not copies, so the first token
		// of the second document is the same pointer on each pass.
		var passes []*token.Token

		for range 2 {
			for i, dd := range d.Documents() {
				if i == 1 {
					passes = append(passes, dd.Tokens()[0])
				}
			}
		}

		require.Len(t, passes, 2)
		assert.Same(t, passes[0], passes[1])
		assert.Same(t, source.Tokens()[4], passes[0])
	})

	t.Run("iterates over multiple documents", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
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

	t.Run("pairs tokens with documents closed by an end marker", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			kind: A
			...
			kind: B
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Decoder()
		require.NoError(t, err)
		require.Equal(t, 2, d.Len())

		kindPath := paths.Root().Child("kind").Path()

		for i, dd := range d.Documents() {
			kind, ok := dd.GetValue(kindPath)
			require.True(t, ok)

			tks := dd.Tokens()
			require.NotEmpty(t, tks, "document %d has no tokens", i)

			var want string

			switch i {
			case 0:
				want = "A"

				assert.Equal(t, token.DocumentEndType, tks[len(tks)-1].Type)

			case 1:
				want = "B"

				assert.NotEqual(t, token.DocumentEndType, tks[len(tks)-1].Type)
			}

			assert.Equal(t, want, kind)
			assert.Contains(t, tks[0].Origin, "kind")
		}
	})

	t.Run("pairs each document with the token group it starts in", func(t *testing.T) {
		t.Parallel()

		// A leading comment forms its own document, and the header that follows
		// starts the second. Each document's tokens begin at its own anchor.
		input := stringtest.Input(`
			# top

			---
			b: 2
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Decoder()
		require.NoError(t, err)
		require.Equal(t, 2, d.Len())

		var types [][]token.Type

		for _, dd := range d.Documents() {
			var docTypes []token.Type

			for _, tk := range dd.Tokens() {
				docTypes = append(docTypes, tk.Type)
			}

			types = append(types, docTypes)
		}

		assert.Equal(t, [][]token.Type{
			{token.CommentType},
			{token.DocumentHeaderType, token.StringType, token.MappingValueType, token.IntegerType},
		}, types)
	})

	t.Run("pairs by offset when the parser collapses consecutive headers", func(t *testing.T) {
		t.Parallel()

		// The go-yaml parser folds everything after consecutive headers into
		// one empty document anchored at the first header, so that document
		// takes only the first header's token group.
		input := stringtest.Input(`
			---
			---
			b: 2
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Decoder()
		require.NoError(t, err)
		require.Equal(t, 1, d.Len())

		for _, dd := range d.Documents() {
			tks := dd.Tokens()
			require.Len(t, tks, 1)
			assert.Equal(t, token.DocumentHeaderType, tks[0].Type)
			assert.Same(t, source.Tokens()[0], tks[0])
		}
	})

	t.Run("early break stops iteration", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
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

		input := stringtest.Input(`
			name: test
			count: 42
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Decoder()
		require.NoError(t, err)

		validator := yamltest.NewPassingSchemaValidator()

		for _, dd := range d.Documents() {
			err := dd.ValidateSchema(t.Context(), validator)
			require.NoError(t, err)
		}
	})

	t.Run("invalid data fails schema validation", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			name: test
			count: not-a-number
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Decoder()
		require.NoError(t, err)

		wantErr := errors.New("validation failed")
		validator := yamltest.NewFailingSchemaValidator(wantErr)

		for _, dd := range d.Documents() {
			err := dd.ValidateSchema(t.Context(), validator)
			require.ErrorIs(t, err, wantErr)
		}
	})
}

// failingValidator always fails self-validation with a path error.
type failingValidator struct {
	Name string `yaml:"name"`
}

func (failingValidator) Validate() error {
	return niceyaml.NewError("rejected", niceyaml.WithPath(paths.Root().Child("name").Value()))
}

func TestDocumentDecoder_DocumentIndex(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		name: first
		---
		name: second
	`)
	namePath := paths.Root().Child("name").Value()

	requireIndex := func(t *testing.T, err error, want int) {
		t.Helper()

		yamlErr, ok := errors.AsType[*niceyaml.Error](err)
		require.True(t, ok, "want *niceyaml.Error, got %T", err)

		got, set := yamlErr.DocumentIndex()
		require.True(t, set)
		assert.Equal(t, want, got)
	}

	t.Run("schema validation errors carry the document index", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(input)
		d, err := source.Decoder()
		require.NoError(t, err)

		validator := yamltest.NewCustomSchemaValidator(func(_ context.Context, _ any) error {
			return niceyaml.NewError("bad name", niceyaml.WithPath(namePath))
		})

		for i, dd := range d.Documents() {
			requireIndex(t, dd.ValidateSchema(t.Context(), validator), i)
		}
	})

	t.Run("self validation errors carry the document index", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(input)
		d, err := source.Decoder()
		require.NoError(t, err)

		for i, dd := range d.Documents() {
			_, err := dd.Unmarshal[failingValidator](t.Context())
			requireIndex(t, err, i)
		}
	})

	t.Run("decode errors carry the document index", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(input)
		d, err := source.Decoder()
		require.NoError(t, err)

		for i, dd := range d.Documents() {
			_, err := dd.Decode[struct {
				Name int `yaml:"name"`
			}](t.Context())
			requireIndex(t, err, i)
		}
	})

	t.Run("an explicit index is kept", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(input)
		d, err := source.Decoder()
		require.NoError(t, err)

		validator := yamltest.NewCustomSchemaValidator(func(_ context.Context, _ any) error {
			return niceyaml.NewError("bad name", niceyaml.WithDocumentIndex(7))
		})

		for _, dd := range d.Documents() {
			requireIndex(t, dd.ValidateSchema(t.Context(), validator), 7)
		}
	})

	t.Run("wrapped errors resolve in their own document", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(input)
		d, err := source.Decoder()
		require.NoError(t, err)

		validator := yamltest.NewCustomSchemaValidator(func(_ context.Context, _ any) error {
			return niceyaml.NewError("bad name", niceyaml.WithPath(namePath))
		})

		var got []string

		for _, dd := range d.Documents() {
			err := source.WrapError(dd.ValidateSchema(t.Context(), validator))
			got = append(got, strings.SplitN(err.Error(), "\n", 2)[0])
		}

		assert.Equal(t, []string{"[1:7] bad name", "[3:7] bad name"}, got)
	})
}

func TestWithDecodeOptions(t *testing.T) {
	t.Parallel()

	type strictConfig struct {
		Name string `yaml:"name"`
	}

	t.Run("without option allows unknown fields", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			name: test
			extra: field
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Decoder()
		require.NoError(t, err)

		for _, dd := range d.Documents() {
			result, err := dd.Decode[strictConfig](t.Context())
			require.NoError(t, err)
			assert.Equal(t, "test", result.Name)
		}
	})

	t.Run("DisallowUnknownField rejects unknown fields", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			name: test
			extra: field
		`)
		source := niceyaml.NewSourceFromString(input,
			niceyaml.WithDecodeOptions(yaml.DisallowUnknownField()),
		)
		d, err := source.Decoder()
		require.NoError(t, err)

		for _, dd := range d.Documents() {
			_, err := dd.Decode[strictConfig](t.Context())
			require.Error(t, err)

			var yamlErr *niceyaml.Error

			require.ErrorAs(t, err, &yamlErr)
		}
	})

	t.Run("options apply to Unmarshal", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			name: test
			extra: field
		`)
		source := niceyaml.NewSourceFromString(input,
			niceyaml.WithDecodeOptions(yaml.DisallowUnknownField()),
		)
		d, err := source.Decoder()
		require.NoError(t, err)

		for _, dd := range d.Documents() {
			_, err := dd.Unmarshal[strictConfig](t.Context())
			require.Error(t, err)

			var yamlErr *niceyaml.Error

			require.ErrorAs(t, err, &yamlErr)
		}
	})

	t.Run("options apply across multiple documents", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
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
			_, err := dd.Decode[strictConfig](t.Context())
			if err != nil {
				errCount++
			}
		}

		assert.Equal(t, 2, errCount)
	})

	t.Run("no options is the default", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			name: test
			extra: field
		`)
		source := niceyaml.NewSourceFromString(input,
			niceyaml.WithDecodeOptions(),
		)
		d, err := source.Decoder()
		require.NoError(t, err)

		for _, dd := range d.Documents() {
			result, err := dd.Decode[strictConfig](t.Context())
			require.NoError(t, err)
			assert.Equal(t, "test", result.Name)
		}
	})
}

func TestDocumentDecoder_Get(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		kind: Deployment
		version: 2
		enabled: true
		empty: null
		tags:
		  - a
		  - b
		meta:
		  name: app
	`)

	t.Run("string", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		got, err := dd.Get[string](t.Context(), paths.Root().Child("kind").Path())
		require.NoError(t, err)
		assert.Equal(t, "Deployment", got)
	})

	t.Run("int", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		got, err := dd.Get[int](t.Context(), paths.Root().Child("version").Path())
		require.NoError(t, err)
		assert.Equal(t, 2, got)
	})

	t.Run("bool", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		got, err := dd.Get[bool](t.Context(), paths.Root().Child("enabled").Path())
		require.NoError(t, err)
		assert.True(t, got)
	})

	t.Run("null decodes to zero value", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		got, err := dd.Get[string](t.Context(), paths.Root().Child("empty").Path())
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("slice", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		got, err := dd.Get[[]string](t.Context(), paths.Root().Child("tags").Path())
		require.NoError(t, err)
		assert.Equal(t, []string{"a", "b"}, got)
	})

	t.Run("struct", func(t *testing.T) {
		t.Parallel()

		type meta struct {
			Name string `yaml:"name"`
		}

		dd := yamltest.FirstDocument(t, input)

		got, err := dd.Get[meta](t.Context(), paths.Root().Child("meta").Path())
		require.NoError(t, err)
		assert.Equal(t, meta{Name: "app"}, got)
	})

	t.Run("missing path returns ErrValueNotFound", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		got, err := dd.Get[string](t.Context(), paths.Root().Child("nonexistent").Path())
		require.ErrorIs(t, err, niceyaml.ErrValueNotFound)
		assert.Empty(t, got)
	})

	t.Run("nil path returns ErrValueNotFound", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		got, err := dd.Get[string](t.Context(), nil)
		require.ErrorIs(t, err, niceyaml.ErrValueNotFound)
		assert.Empty(t, got)
	})

	t.Run("type mismatch returns Error", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		got, err := dd.Get[int](t.Context(), paths.Root().Child("kind").Path())
		require.Error(t, err)

		var yamlErr *niceyaml.Error

		require.ErrorAs(t, err, &yamlErr)
		assert.Zero(t, got)
	})
}

func TestDocumentDecoder_DecodeInto(t *testing.T) {
	t.Parallel()

	t.Run("keeps fields absent from the document", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: test")

		result := plainConfig{Name: "default", Value: 7}

		err := dd.DecodeInto(t.Context(), &result)
		require.NoError(t, err)
		assert.Equal(t, plainConfig{Name: "test", Value: 7}, result)
	})

	t.Run("does not call Validate", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: test")

		var result validatorConfig

		err := dd.DecodeInto(t.Context(), &result)
		require.NoError(t, err)
		assert.False(t, result.validated, "Validate() should NOT have been called by DecodeInto()")
	})
}

func TestDocumentDecoder_UnmarshalInto(t *testing.T) {
	t.Parallel()

	t.Run("keeps fields absent from the document", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: test")

		result := bothValidatorConfig{Value: 7}

		err := dd.UnmarshalInto(t.Context(), &result)
		require.NoError(t, err)
		assert.Equal(t, "test", result.Name)
		assert.Equal(t, 7, result.Value)
		assert.True(t, result.schemaValidated, "ValidateSchema() should have been called")
		assert.True(t, result.validated, "Validate() should have been called")
	})

	t.Run("returns Validator error", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, `name: ""`)

		var result bothValidatorConfig

		err := dd.UnmarshalInto(t.Context(), &result)
		require.ErrorIs(t, err, errNameRequired)
	})
}

func TestDocumentDecoder_Unmarshal_ValueReceivers(t *testing.T) {
	t.Parallel()

	t.Run("calls value receiver hooks", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: test")

		result, err := dd.Unmarshal[valueValidatorConfig](t.Context())
		require.NoError(t, err)
		assert.Equal(t, "test", result.Name)
	})

	t.Run("returns value receiver Validate error", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, `name: ""`)

		result, err := dd.Unmarshal[valueValidatorConfig](t.Context())
		require.ErrorIs(t, err, errNameRequired)
		assert.Zero(t, result)
	})

	t.Run("returns value receiver ValidateSchema error", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: invalid")

		result, err := dd.Unmarshal[valueValidatorConfig](t.Context())
		require.ErrorIs(t, err, errSchemaValidationFailed)
		assert.Zero(t, result)
	})
}

// valueValidatorConfig implements niceyaml.SchemaValidator and
// niceyaml.Validator with value receivers.
type valueValidatorConfig struct {
	Name string `yaml:"name"`
}

func (c valueValidatorConfig) ValidateSchema(_ context.Context, data any) error {
	m, ok := data.(map[string]any)
	if !ok {
		return errors.New("expected map")
	}

	if name, ok := m["name"].(string); ok && name == "invalid" {
		return errSchemaValidationFailed
	}

	return nil
}

func (c valueValidatorConfig) Validate() error {
	if c.Name == "" {
		return errNameRequired
	}

	return nil
}
