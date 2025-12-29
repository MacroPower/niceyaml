package niceyaml_test

import (
	"errors"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macropower/niceyaml"
)

// mockValidator implements niceyaml.Validator for testing.
type mockValidator struct {
	failErr    error
	shouldFail bool
}

func (v *mockValidator) Validate(_ any) error {
	if v.shouldFail {
		return v.failErr
	}

	return nil
}

func passingValidator() *mockValidator {
	return &mockValidator{shouldFail: false}
}

func failingValidator(err error) *mockValidator {
	return &mockValidator{shouldFail: true, failErr: err}
}

func TestNewDecoder(t *testing.T) {
	t.Parallel()

	t.Run("creates decoder from ast.File", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value")
		file, err := source.Parse()
		require.NoError(t, err)

		d := niceyaml.NewDecoder(file)
		require.NotNil(t, d)
	})

	t.Run("creates decoder from empty file", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("")
		file, err := source.Parse()
		require.NoError(t, err)

		d := niceyaml.NewDecoder(file)
		require.NotNil(t, d)
		assert.Equal(t, 1, d.DocumentCount())
	})
}

func TestDecoder_DocumentCount(t *testing.T) {
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
			input: `---
key1: value1
---
key2: value2`,
			want: 2,
		},
		"three documents": {
			input: `---
a: 1
---
b: 2
---
c: 3`,
			want: 3,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)
			file, err := source.Parse()
			require.NoError(t, err)

			d := niceyaml.NewDecoder(file)
			got := d.DocumentCount()

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
			input: `parent:
  child: nested_value`,
			path:      func() *yaml.Path { return niceyaml.NewPathBuilder().Child("parent").Child("child").Build() },
			wantVals:  []string{"nested_value"},
			wantFound: []bool{true},
		},
		"array index": {
			input: `items:
  - first
  - second
  - third`,
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
			input: `---
first: 1
---
second: 2`,
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
			file, err := source.Parse()
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
		file, err := source.Parse()
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

		input := `name: test
value: 42`
		source := niceyaml.NewSourceFromString(input)
		file, err := source.Parse()
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

		input := `- one
- two
- three`
		source := niceyaml.NewSourceFromString(input)
		file, err := source.Parse()
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

		input := `---
name: first
value: 1
---
name: second
value: 2`
		source := niceyaml.NewSourceFromString(input)
		file, err := source.Parse()
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
		file, err := source.Parse()
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
		file, err := source.Parse()
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
		file, err := source.Parse()
		require.NoError(t, err)

		d := niceyaml.NewDecoder(file)
		for _, dd := range d.Documents() {
			err := dd.Validate(passingValidator())
			require.NoError(t, err)
		}
	})

	t.Run("failing validation", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value")
		file, err := source.Parse()
		require.NoError(t, err)

		d := niceyaml.NewDecoder(file)
		for _, dd := range d.Documents() {
			err := dd.Validate(failingValidator(errors.New("validation failed")))
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
		file, err := source.Parse()
		require.NoError(t, err)

		d := niceyaml.NewDecoder(file)
		for _, dd := range d.Documents() {
			err := dd.ValidateContext(t.Context(), passingValidator())
			require.NoError(t, err)
		}
	})

	t.Run("validate with failing validator", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value")
		file, err := source.Parse()
		require.NoError(t, err)

		d := niceyaml.NewDecoder(file)
		for _, dd := range d.Documents() {
			err := dd.ValidateContext(t.Context(), failingValidator(errors.New("invalid")))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "invalid document")
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

		input := `name: test
value: 42`
		source := niceyaml.NewSourceFromString(input)
		file, err := source.Parse()
		require.NoError(t, err)

		d := niceyaml.NewDecoder(file)
		for _, dd := range d.Documents() {
			var result config

			err := dd.ValidateDecode(&result, passingValidator())
			require.NoError(t, err)
			assert.Equal(t, config{Name: "test", Value: 42}, result)
		}
	})

	t.Run("validation fails - no decode", func(t *testing.T) {
		t.Parallel()

		input := `name: test
value: 42`
		source := niceyaml.NewSourceFromString(input)
		file, err := source.Parse()
		require.NoError(t, err)

		d := niceyaml.NewDecoder(file)
		for _, dd := range d.Documents() {
			var result config

			err := dd.ValidateDecode(&result, failingValidator(errors.New("not allowed")))
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
		file, err := source.Parse()
		require.NoError(t, err)

		d := niceyaml.NewDecoder(file)
		for _, dd := range d.Documents() {
			var result config

			err := dd.ValidateDecodeContext(t.Context(), &result, passingValidator())

			require.NoError(t, err)
			assert.Equal(t, "contextual", result.Name)
		}
	})

	t.Run("validation failure stops decode", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("name: contextual")
		file, err := source.Parse()
		require.NoError(t, err)

		d := niceyaml.NewDecoder(file)
		for _, dd := range d.Documents() {
			var result config

			err := dd.ValidateDecodeContext(t.Context(), &result, failingValidator(errors.New("blocked")))

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
		file, err := source.Parse()
		require.NoError(t, err)
		require.Len(t, file.Docs, 1)

		dd := niceyaml.NewDocumentDecoder(file, file.Docs[0])
		require.NotNil(t, dd)

		var result map[string]string

		err = dd.Decode(&result)
		require.NoError(t, err)
		assert.Equal(t, "value", result["key"])
	})
}

func TestDecoder_Documents(t *testing.T) {
	t.Parallel()

	t.Run("iterates over single document", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value")
		file, err := source.Parse()
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

		input := `---
a: 1
---
b: 2
---
c: 3`
		source := niceyaml.NewSourceFromString(input)
		file, err := source.Parse()
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

		input := `---
a: 1
---
b: 2
---
c: 3`
		source := niceyaml.NewSourceFromString(input)
		file, err := source.Parse()
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
