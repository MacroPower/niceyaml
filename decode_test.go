package niceyaml_test

import (
	"errors"
	"strings"
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

// errorReader implements io.Reader that always returns an error.
type errorReader struct {
	err error
}

func (r *errorReader) Read(_ []byte) (int, error) {
	return 0, r.err
}

func TestNewParser(t *testing.T) {
	t.Parallel()

	t.Run("creates parser with reader", func(t *testing.T) {
		t.Parallel()

		r := strings.NewReader("key: value")
		p := niceyaml.NewParser(r)
		require.NotNil(t, p)
	})

	t.Run("creates parser with nil reader", func(t *testing.T) {
		t.Parallel()

		p := niceyaml.NewParser(nil)
		require.NotNil(t, p)
	})
}

func TestParser_Parse(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input    string
		wantDocs int
	}{
		"single document": {
			input:    "key: value",
			wantDocs: 1,
		},
		"multiple documents": {
			input: `---
key1: value1
---
key2: value2
---
key3: value3`,
			wantDocs: 3,
		},
		"empty input": {
			input:    "",
			wantDocs: 1,
		},
		"document with comments": {
			input: `# This is a comment
key: value`,
			wantDocs: 1,
		},
		"complex nested structure": {
			input: `metadata:
  name: test
  labels:
    app: myapp
spec:
  replicas: 3
  template:
    containers:
      - name: main
        image: nginx`,
			wantDocs: 1,
		},
		"document with explicit start": {
			input: `---
key: value`,
			wantDocs: 1,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := niceyaml.NewParser(strings.NewReader(tc.input))
			file, err := p.Parse()

			require.NoError(t, err)
			require.NotNil(t, file)
			assert.Len(t, file.Docs, tc.wantDocs)
		})
	}
}

func TestParser_Parse_InvalidYAML(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
	}{
		"unclosed quote": {
			input: `key: "unclosed`,
		},
		"invalid indentation": {
			input: `parent:
  child: value
 bad: indent`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := niceyaml.NewParser(strings.NewReader(tc.input))
			_, err := p.Parse()

			require.Error(t, err)

			var yamlErr *niceyaml.Error
			require.ErrorAs(t, err, &yamlErr)
		})
	}
}

func TestParser_Parse_ReaderError(t *testing.T) {
	t.Parallel()

	readErr := errors.New("read failure")
	r := &errorReader{err: readErr}
	p := niceyaml.NewParser(r)

	_, err := p.Parse()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "read")
	require.ErrorIs(t, err, readErr)
}

func TestNewDecoder(t *testing.T) {
	t.Parallel()

	t.Run("creates decoder from ast.File", func(t *testing.T) {
		t.Parallel()

		p := niceyaml.NewParser(strings.NewReader("key: value"))
		file, err := p.Parse()
		require.NoError(t, err)

		d := niceyaml.NewDecoder(file)
		require.NotNil(t, d)
	})

	t.Run("creates decoder from empty file", func(t *testing.T) {
		t.Parallel()

		p := niceyaml.NewParser(strings.NewReader(""))
		file, err := p.Parse()
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

			p := niceyaml.NewParser(strings.NewReader(tc.input))
			file, err := p.Parse()
			require.NoError(t, err)

			d := niceyaml.NewDecoder(file)
			got := d.DocumentCount()

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestDecoder_GetValue(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		wantErr error
		path    func() *yaml.Path
		input   string
		want    string
		doc     int
	}{
		"simple key": {
			input: "key: value",
			doc:   0,
			path:  func() *yaml.Path { return niceyaml.NewRootPath().Child("key").Build() },
			want:  "value",
		},
		"nested key": {
			input: `parent:
  child: nested_value`,
			doc:  0,
			path: func() *yaml.Path { return niceyaml.NewRootPath().Child("parent").Child("child").Build() },
			want: "nested_value",
		},
		"array index": {
			input: `items:
  - first
  - second
  - third`,
			doc:  0,
			path: func() *yaml.Path { return niceyaml.NewRootPath().Child("items").Index(1).Build() },
			want: "second",
		},
		"missing key returns empty": {
			input: "key: value",
			doc:   0,
			path:  func() *yaml.Path { return niceyaml.NewRootPath().Child("nonexistent").Build() },
			want:  "",
		},
		"second document": {
			input: `---
first: 1
---
second: 2`,
			doc:  1,
			path: func() *yaml.Path { return niceyaml.NewRootPath().Child("second").Build() },
			want: "2",
		},
		"document out of range": {
			input:   "key: value",
			doc:     5,
			path:    func() *yaml.Path { return niceyaml.NewRootPath().Child("key").Build() },
			wantErr: niceyaml.ErrDocumentIndexOutOfRange,
		},
		"numeric value": {
			input: "count: 42",
			doc:   0,
			path:  func() *yaml.Path { return niceyaml.NewRootPath().Child("count").Build() },
			want:  "42",
		},
		"boolean value": {
			input: "enabled: true",
			doc:   0,
			path:  func() *yaml.Path { return niceyaml.NewRootPath().Child("enabled").Build() },
			want:  "true",
		},
		"null value": {
			input: "empty: null",
			doc:   0,
			path:  func() *yaml.Path { return niceyaml.NewRootPath().Child("empty").Build() },
			want:  "null",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := niceyaml.NewParser(strings.NewReader(tc.input))
			file, err := p.Parse()
			require.NoError(t, err)

			d := niceyaml.NewDecoder(file)
			path := tc.path()
			got, err := d.GetValue(tc.doc, *path)

			if tc.wantErr != nil {
				require.Error(t, err)
				require.ErrorIs(t, err, tc.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestDecoder_Decode(t *testing.T) {
	t.Parallel()

	type testStruct struct {
		Name  string `yaml:"name"`
		Value int    `yaml:"value"`
	}

	tcs := map[string]struct {
		wantValue any
		wantErr   error
		target    func() any
		input     string
		doc       int
	}{
		"decode to map": {
			input:  "key: value",
			doc:    0,
			target: func() any { return &map[string]string{} },
			wantValue: &map[string]string{
				"key": "value",
			},
		},
		"decode to struct": {
			input: `name: test
value: 42`,
			doc:    0,
			target: func() any { return &testStruct{} },
			wantValue: &testStruct{
				Name:  "test",
				Value: 42,
			},
		},
		"decode to slice": {
			input: `- one
- two
- three`,
			doc:    0,
			target: func() any { return &[]string{} },
			wantValue: &[]string{
				"one", "two", "three",
			},
		},
		"decode second document": {
			input: `---
first: 1
---
name: second
value: 2`,
			doc:    1,
			target: func() any { return &testStruct{} },
			wantValue: &testStruct{
				Name:  "second",
				Value: 2,
			},
		},
		"document out of range": {
			input:   "key: value",
			doc:     10,
			target:  func() any { return &map[string]string{} },
			wantErr: niceyaml.ErrDocumentIndexOutOfRange,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := niceyaml.NewParser(strings.NewReader(tc.input))
			file, err := p.Parse()
			require.NoError(t, err)

			d := niceyaml.NewDecoder(file)
			target := tc.target()
			err = d.Decode(tc.doc, target)

			if tc.wantErr != nil {
				require.Error(t, err)
				require.ErrorIs(t, err, tc.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.wantValue, target)
		})
	}
}

func TestDecoder_Decode_TypeMismatch(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		target func() any
		input  string
	}{
		"string to int": {
			input:  "value: not_a_number",
			target: func() any { return &struct{ Value int }{} },
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := niceyaml.NewParser(strings.NewReader(tc.input))
			file, err := p.Parse()
			require.NoError(t, err)

			d := niceyaml.NewDecoder(file)
			target := tc.target()
			err = d.Decode(0, target)

			require.Error(t, err)

			var yamlErr *niceyaml.Error
			require.ErrorAs(t, err, &yamlErr)
		})
	}
}

func TestDecoder_DecodeContext(t *testing.T) {
	t.Parallel()

	t.Run("decode with background context", func(t *testing.T) {
		t.Parallel()

		p := niceyaml.NewParser(strings.NewReader("key: value"))
		file, err := p.Parse()
		require.NoError(t, err)

		d := niceyaml.NewDecoder(file)

		var result map[string]string

		err = d.DecodeContext(t.Context(), 0, &result)

		require.NoError(t, err)
		assert.Equal(t, "value", result["key"])
	})
}

func TestDecoder_Validate(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		validator niceyaml.Validator
		input     string
		errMsg    string
		doc       int
		wantErr   bool
	}{
		"passing validation": {
			input:     "key: value",
			doc:       0,
			validator: passingValidator(),
			wantErr:   false,
		},
		"failing validation": {
			input:     "key: value",
			doc:       0,
			validator: failingValidator(errors.New("validation failed")),
			wantErr:   true,
			errMsg:    "validation failed",
		},
		"document out of range": {
			input:     "key: value",
			doc:       5,
			validator: passingValidator(),
			wantErr:   true,
			errMsg:    "document index out of range",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := niceyaml.NewParser(strings.NewReader(tc.input))
			file, err := p.Parse()
			require.NoError(t, err)

			d := niceyaml.NewDecoder(file)
			err = d.Validate(tc.doc, tc.validator)

			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errMsg)

				return
			}

			require.NoError(t, err)
		})
	}
}

func TestDecoder_ValidateContext(t *testing.T) {
	t.Parallel()

	t.Run("validate with background context", func(t *testing.T) {
		t.Parallel()

		p := niceyaml.NewParser(strings.NewReader("key: value"))
		file, err := p.Parse()
		require.NoError(t, err)

		d := niceyaml.NewDecoder(file)
		err = d.ValidateContext(t.Context(), 0, passingValidator())

		require.NoError(t, err)
	})

	t.Run("validate with failing validator", func(t *testing.T) {
		t.Parallel()

		p := niceyaml.NewParser(strings.NewReader("key: value"))
		file, err := p.Parse()
		require.NoError(t, err)

		d := niceyaml.NewDecoder(file)
		err = d.ValidateContext(t.Context(), 0, failingValidator(errors.New("invalid")))

		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid document")
	})
}

func TestDecoder_ValidateDecode(t *testing.T) {
	t.Parallel()

	type config struct {
		Name  string `yaml:"name"`
		Value int    `yaml:"value"`
	}

	tcs := map[string]struct {
		validator niceyaml.Validator
		input     string
		errMsg    string
		want      config
		doc       int
		wantErr   bool
	}{
		"valid and decode": {
			input: `name: test
value: 42`,
			doc:       0,
			validator: passingValidator(),
			want:      config{Name: "test", Value: 42},
			wantErr:   false,
		},
		"validation fails - no decode": {
			input: `name: test
value: 42`,
			doc:       0,
			validator: failingValidator(errors.New("not allowed")),
			wantErr:   true,
			errMsg:    "not allowed",
		},
		"document out of range": {
			input:     "name: test",
			doc:       99,
			validator: passingValidator(),
			wantErr:   true,
			errMsg:    "document index out of range",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := niceyaml.NewParser(strings.NewReader(tc.input))
			file, err := p.Parse()
			require.NoError(t, err)

			d := niceyaml.NewDecoder(file)

			var result config

			err = d.ValidateDecode(tc.doc, &result, tc.validator)

			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errMsg)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, result)
		})
	}
}

func TestDecoder_ValidateDecodeContext(t *testing.T) {
	t.Parallel()

	type config struct {
		Name string `yaml:"name"`
	}

	t.Run("validates and decodes with context", func(t *testing.T) {
		t.Parallel()

		p := niceyaml.NewParser(strings.NewReader("name: contextual"))
		file, err := p.Parse()
		require.NoError(t, err)

		d := niceyaml.NewDecoder(file)

		var result config

		err = d.ValidateDecodeContext(t.Context(), 0, &result, passingValidator())

		require.NoError(t, err)
		assert.Equal(t, "contextual", result.Name)
	})

	t.Run("validation failure stops decode", func(t *testing.T) {
		t.Parallel()

		p := niceyaml.NewParser(strings.NewReader("name: contextual"))
		file, err := p.Parse()
		require.NoError(t, err)

		d := niceyaml.NewDecoder(file)

		var result config

		err = d.ValidateDecodeContext(t.Context(), 0, &result, failingValidator(errors.New("blocked")))

		require.Error(t, err)
		assert.Empty(t, result.Name)
	})
}

func TestErrDocumentIndexOutOfRange(t *testing.T) {
	t.Parallel()

	t.Run("error message format for GetValue", func(t *testing.T) {
		t.Parallel()

		p := niceyaml.NewParser(strings.NewReader("key: value"))
		file, err := p.Parse()
		require.NoError(t, err)

		d := niceyaml.NewDecoder(file)
		_, err = d.GetValue(3, *niceyaml.NewRootPath().Build())

		require.Error(t, err)
		require.ErrorIs(t, err, niceyaml.ErrDocumentIndexOutOfRange)
		assert.Contains(t, err.Error(), "4 of 1")
	})

	t.Run("error message format for Decode", func(t *testing.T) {
		t.Parallel()

		p := niceyaml.NewParser(strings.NewReader(`---
a: 1
---
b: 2`))
		file, err := p.Parse()
		require.NoError(t, err)

		d := niceyaml.NewDecoder(file)

		var result map[string]int

		err = d.Decode(5, &result)

		require.Error(t, err)
		require.ErrorIs(t, err, niceyaml.ErrDocumentIndexOutOfRange)
		assert.Contains(t, err.Error(), "6 of 2")
	})
}
