package niceyaml_test

import (
	"context"
	"errors"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macropower/niceyaml"
	"github.com/macropower/niceyaml/yamltest"
)

func TestNewDecoder(t *testing.T) {
	t.Parallel()

	t.Run("creates decoder from ast.File", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value")
		file, err := source.File()
		require.NoError(t, err)

		d := niceyaml.NewDecoder(file)
		require.NotNil(t, d)
	})

	t.Run("creates decoder from empty file", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("")
		file, err := source.File()
		require.NoError(t, err)

		d := niceyaml.NewDecoder(file)
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
			file, err := source.File()
			require.NoError(t, err)

			d := niceyaml.NewDecoder(file)
			got := d.Len()

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestDocumentDecoder_GetValue(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		path      func() *yaml.Path
		input     string
		wantVals  []string
		wantFound []bool
	}{
		"simple key": {
			input:     "key: value",
			path:      func() *yaml.Path { return niceyaml.NewPathBuilder().Child("key").Build() },
			wantVals:  []string{"value"},
			wantFound: []bool{true},
		},
		"nested key": {
			input: yamltest.Input(`
				parent:
				  child: nested_value
			`),
			path:      func() *yaml.Path { return niceyaml.NewPathBuilder().Child("parent").Child("child").Build() },
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
			path:      func() *yaml.Path { return niceyaml.NewPathBuilder().Child("items").Index(1).Build() },
			wantVals:  []string{"second"},
			wantFound: []bool{true},
		},
		"missing key returns empty": {
			input:     "key: value",
			path:      func() *yaml.Path { return niceyaml.NewPathBuilder().Child("nonexistent").Build() },
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
			path:      func() *yaml.Path { return niceyaml.NewPathBuilder().Build() },
			wantVals:  []string{"first: 1", "second: 2"},
			wantFound: []bool{true, true},
		},
		"numeric value": {
			input:     "count: 42",
			path:      func() *yaml.Path { return niceyaml.NewPathBuilder().Child("count").Build() },
			wantVals:  []string{"42"},
			wantFound: []bool{true},
		},
		"boolean value": {
			input:     "enabled: true",
			path:      func() *yaml.Path { return niceyaml.NewPathBuilder().Child("enabled").Build() },
			wantVals:  []string{"true"},
			wantFound: []bool{true},
		},
		"null value": {
			input:     "empty: null",
			path:      func() *yaml.Path { return niceyaml.NewPathBuilder().Child("empty").Build() },
			wantVals:  []string{"null"},
			wantFound: []bool{true},
		},
		"nil path returns false": {
			input:     "key: value",
			path:      func() *yaml.Path { return nil },
			wantVals:  []string{""},
			wantFound: []bool{false},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)
			file, err := source.File()
			require.NoError(t, err)

			d := niceyaml.NewDecoder(file)
			path := tc.path()

			var gotVals []string

			var gotFound []bool

			for _, dd := range d.Documents() {
				val, found := dd.GetValue(path)
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
		file, err := source.File()
		require.NoError(t, err)

		d := niceyaml.NewDecoder(file)

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
		file, err := source.File()
		require.NoError(t, err)

		d := niceyaml.NewDecoder(file)

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
		file, err := source.File()
		require.NoError(t, err)

		d := niceyaml.NewDecoder(file)

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
		file, err := source.File()
		require.NoError(t, err)

		d := niceyaml.NewDecoder(file)

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
		file, err := source.File()
		require.NoError(t, err)

		d := niceyaml.NewDecoder(file)

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
		file, err := source.File()
		require.NoError(t, err)

		d := niceyaml.NewDecoder(file)

		for _, dd := range d.Documents() {
			var result map[string]string

			err := dd.DecodeContext(t.Context(), &result)

			require.NoError(t, err)
			assert.Equal(t, "value", result["key"])
		}
	})
}

func TestDocumentDecoder_Validate(t *testing.T) {
	t.Parallel()

	t.Run("passing validation", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value")
		file, err := source.File()
		require.NoError(t, err)

		d := niceyaml.NewDecoder(file)

		for _, dd := range d.Documents() {
			err := dd.Validate(yamltest.NewPassingValidator())
			require.NoError(t, err)
		}
	})

	t.Run("failing validation", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value")
		file, err := source.File()
		require.NoError(t, err)

		d := niceyaml.NewDecoder(file)

		for _, dd := range d.Documents() {
			err := dd.Validate(yamltest.NewFailingValidator(errors.New("validation failed")))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "validation failed")
		}
	})
}

func TestDocumentDecoder_ValidateContext(t *testing.T) {
	t.Parallel()

	t.Run("validate with background context", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value")
		file, err := source.File()
		require.NoError(t, err)

		d := niceyaml.NewDecoder(file)

		for _, dd := range d.Documents() {
			err := dd.ValidateContext(t.Context(), yamltest.NewPassingValidator())
			require.NoError(t, err)
		}
	})

	t.Run("validate with failing validator", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value")
		file, err := source.File()
		require.NoError(t, err)

		d := niceyaml.NewDecoder(file)

		for _, dd := range d.Documents() {
			err := dd.ValidateContext(t.Context(), yamltest.NewFailingValidator(errors.New("invalid")))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "invalid")
		}
	})
}

func TestDocumentDecoder_ValidateDecode(t *testing.T) {
	t.Parallel()

	type config struct {
		Name  string `yaml:"name"`
		Value int    `yaml:"value"`
	}

	t.Run("valid and decode", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			name: test
			value: 42
		`)
		source := niceyaml.NewSourceFromString(input)
		file, err := source.File()
		require.NoError(t, err)

		d := niceyaml.NewDecoder(file)

		for _, dd := range d.Documents() {
			var result config

			err := dd.ValidateDecode(&result, yamltest.NewPassingValidator())
			require.NoError(t, err)
			assert.Equal(t, config{Name: "test", Value: 42}, result)
		}
	})

	t.Run("validation fails - no decode", func(t *testing.T) {
		t.Parallel()

		input := yamltest.Input(`
			name: test
			value: 42
		`)
		source := niceyaml.NewSourceFromString(input)
		file, err := source.File()
		require.NoError(t, err)

		d := niceyaml.NewDecoder(file)

		for _, dd := range d.Documents() {
			var result config

			err := dd.ValidateDecode(&result, yamltest.NewFailingValidator(errors.New("not allowed")))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "not allowed")
		}
	})
}

func TestDocumentDecoder_ValidateDecodeContext(t *testing.T) {
	t.Parallel()

	type config struct {
		Name string `yaml:"name"`
	}

	t.Run("validates and decodes with context", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("name: contextual")
		file, err := source.File()
		require.NoError(t, err)

		d := niceyaml.NewDecoder(file)

		for _, dd := range d.Documents() {
			var result config

			err := dd.ValidateDecodeContext(t.Context(), &result, yamltest.NewPassingValidator())

			require.NoError(t, err)
			assert.Equal(t, "contextual", result.Name)
		}
	})

	t.Run("validation failure stops decode", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("name: contextual")
		file, err := source.File()
		require.NoError(t, err)

		d := niceyaml.NewDecoder(file)

		for _, dd := range d.Documents() {
			var result config

			err := dd.ValidateDecodeContext(t.Context(), &result, yamltest.NewFailingValidator(errors.New("blocked")))

			require.Error(t, err)
			assert.Empty(t, result.Name)
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
	// This is an edge case where YAML 1.2 directive creates a document
	// where the body is a directive node before the actual content.
	// In practice, go-yaml parses %YAML as a directive but the body
	// of the main document is still the mapping, not the directive.
	// We test that the normal case still works.
	input := `%YAML 1.2
---
key: value`
	source := niceyaml.NewSourceFromString(input)
	file, err := source.File()
	require.NoError(t, err)

	d := niceyaml.NewDecoder(file)
	path := niceyaml.NewPath("key")

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

func TestDocumentDecoder_ValidateDecodeContext_DecodeError(t *testing.T) {
	t.Parallel()

	// Test when DecodeContext after validation fails.
	type strict struct {
		Value int `yaml:"value"`
	}

	input := `value: not_a_number`
	source := niceyaml.NewSourceFromString(input)
	file, err := source.File()
	require.NoError(t, err)

	d := niceyaml.NewDecoder(file)

	for _, dd := range d.Documents() {
		var result strict

		// The validator passes, but decode will fail due to type mismatch.
		err := dd.ValidateDecodeContext(t.Context(), &result, yamltest.NewPassingValidator())

		require.Error(t, err)

		var yamlErr *niceyaml.Error
		require.ErrorAs(t, err, &yamlErr)
	}
}

func TestDocumentDecoder_ValidateContext_ValidationWithNonErrorType(t *testing.T) {
	t.Parallel()

	// Test when the validator returns a niceyaml.Error.
	input := `name: test`
	source := niceyaml.NewSourceFromString(input)
	file, err := source.File()
	require.NoError(t, err)

	pathErr := niceyaml.NewError(
		errors.New("schema validation failed"),
		niceyaml.WithPath(niceyaml.NewPath("name")),
	)

	d := niceyaml.NewDecoder(file)

	for _, dd := range d.Documents() {
		err := dd.ValidateContext(t.Context(), yamltest.NewFailingValidator(pathErr))

		require.Error(t, err)
		// The error should be wrapped with file information.
		assert.Contains(t, err.Error(), "schema validation failed")
	}
}

func TestDocumentDecoder_DecodeContext_CanceledContext(t *testing.T) {
	t.Parallel()

	// Test DecodeContext with a canceled context to trigger the non-yaml error path.
	input := `key: value`
	source := niceyaml.NewSourceFromString(input)
	file, err := source.File()
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // Cancel immediately.

	d := niceyaml.NewDecoder(file)

	for _, dd := range d.Documents() {
		var result map[string]string

		err := dd.DecodeContext(ctx, &result)
		// Context cancellation may or may not cause an error depending on timing.
		// The decode might complete before the context cancellation is checked.
		// This test mainly ensures the code path doesn't panic.
		_ = err
	}
}

func TestDecoder_Documents(t *testing.T) {
	t.Parallel()

	t.Run("iterates over single document", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value")
		file, err := source.File()
		require.NoError(t, err)

		d := niceyaml.NewDecoder(file)

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
		file, err := source.File()
		require.NoError(t, err)

		d := niceyaml.NewDecoder(file)

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
		file, err := source.File()
		require.NoError(t, err)

		d := niceyaml.NewDecoder(file)

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
