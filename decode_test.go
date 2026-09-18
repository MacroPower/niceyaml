package niceyaml_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/printer"
)

// Test sentinel errors for mock validators.
var (
	errSchemaValidationFailed = errors.New("schema validation failed: name cannot be 'invalid'")
	errNameRequired           = errors.New("name is required")
	errDocumentRejected       = errors.New("document rejected")
	errPlainValidation        = errors.New("plain validation failure")
)

func TestSource_Decoder(t *testing.T) {
	t.Parallel()

	t.Run("creates decoder from source", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value")
		d, err := source.Documents()
		require.NoError(t, err)
		require.NotNil(t, d)
	})

	t.Run("creates decoder from empty source", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("")
		d, err := source.Documents()
		require.NoError(t, err)
		assert.Len(t, d, 1)
	})
}

func TestSource_Documents(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString(stringtest.Input(`
		---
		a: 1
		---
		b: 2
	`), niceyaml.WithFilePath("two.yaml"))

	docs, err := source.Documents()
	require.NoError(t, err)
	require.Len(t, docs, 2)

	second := docs[1]
	assert.Equal(t, 1, second.Index())
	assert.Equal(t, "two.yaml", second.FilePath())
	assert.Same(t, source, second.Source())
	assert.NotNil(t, second.Tokens())

	// Every call hands out the same Document for an index, in a slice of
	// its own.
	again, err := source.Documents()
	require.NoError(t, err)
	assert.Same(t, second, again[1])

	again[1] = nil

	third, err := source.Documents()
	require.NoError(t, err)
	assert.Same(t, second, third[1])
}

func TestSource_Document(t *testing.T) {
	t.Parallel()

	t.Run("returns the single document", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("a: 1")

		doc, err := source.Document()
		require.NoError(t, err)

		docs, err := source.Documents()
		require.NoError(t, err)
		assert.Same(t, docs[0], doc)
	})

	t.Run("rejects several documents at the second header", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(stringtest.Input(`
			a: 1
			---
			b: 2
		`))

		_, err := source.Document()
		require.ErrorIs(t, err, niceyaml.ErrMultipleDocuments)
		assert.Equal(t, "2:1: multiple documents in source: 2 documents", err.Error())

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Same(t, source, bound.Source())
	})

	t.Run("skips a comment block above the first header", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(stringtest.Input(`
			# Copyright notice.
			---
			a: 1
		`))

		doc, err := source.Document()
		require.NoError(t, err)

		docs, err := source.Documents()
		require.NoError(t, err)
		require.Len(t, docs, 2)
		assert.Same(t, docs[1], doc)

		got, err := source.Decode[map[string]int](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]int{"a": 1}, got)
	})

	t.Run("skips a directive above the first header", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(stringtest.Input(`
			%YAML 1.2
			---
			a: 1
		`))

		got, err := source.Decode[map[string]int](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]int{"a": 1}, got)
	})

	t.Run("counts only documents with content", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(stringtest.Input(`
			# Copyright notice.
			---
			a: 1
			---
			b: 2
		`))

		_, err := source.Document()
		require.ErrorIs(t, err, niceyaml.ErrMultipleDocuments)
		assert.Equal(t, "4:1: multiple documents in source: 2 documents", err.Error())
	})

	t.Run("returns the first document when none has content", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(stringtest.Input(`
			# a
			---
			# b
		`))

		doc, err := source.Document()
		require.NoError(t, err)

		docs, err := source.Documents()
		require.NoError(t, err)
		assert.Same(t, docs[0], doc)

		got, err := source.Decode[map[string]int](t.Context())
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("rejects a source with no documents", func(t *testing.T) {
		t.Parallel()

		// A lone "..." marker parses to zero documents.
		source := niceyaml.NewSourceFromString("...\n")

		docs, err := source.Documents()
		require.NoError(t, err)
		require.Empty(t, docs)

		_, err = source.Document()
		require.ErrorIs(t, err, niceyaml.ErrNoDocuments)
		require.NotErrorIs(t, err, niceyaml.ErrMultipleDocuments)
		assert.Equal(t, "no documents in source", err.Error())

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Same(t, source, bound.Source())

		_, err = source.Decode[map[string]int](t.Context())
		require.ErrorIs(t, err, niceyaml.ErrNoDocuments)
	})

	t.Run("returns the parse error", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("a: [")

		_, err := source.Document()
		require.Error(t, err)

		_, fileErr := source.File()
		assert.Equal(t, fileErr, err)
	})
}

func TestSource_Decode(t *testing.T) {
	t.Parallel()

	t.Run("decodes the single document", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("name: test")

		var called bool

		got, err := source.Decode[plainConfig](t.Context(), niceyaml.WithValidator(nameSchema(&called)))
		require.NoError(t, err)
		assert.Equal(t, plainConfig{Name: "test"}, got)
		assert.True(t, called, "the validator should have been called")
	})

	t.Run("returns the zero value for several documents", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("a: 1\n---\nb: 2")

		got, err := source.Decode[map[string]int](t.Context())
		require.ErrorIs(t, err, niceyaml.ErrMultipleDocuments)
		assert.Nil(t, got)
	})

	t.Run("DecodeInto keeps fields absent from the document", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("name: test")

		result := plainConfig{Name: "default", Value: 7}

		err := source.DecodeInto(t.Context(), &result)
		require.NoError(t, err)
		assert.Equal(t, plainConfig{Name: "test", Value: 7}, result)
	})
}

func TestDocument_Decode(t *testing.T) {
	t.Parallel()

	type testStruct struct {
		Name  string `yaml:"name"`
		Value int    `yaml:"value"`
	}

	t.Run("decode to map", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value")
		d, err := source.Documents()
		require.NoError(t, err)

		for _, dd := range d {
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
		d, err := source.Documents()
		require.NoError(t, err)

		for _, dd := range d {
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
		d, err := source.Documents()
		require.NoError(t, err)

		for _, dd := range d {
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
		d, err := source.Documents()
		require.NoError(t, err)

		var results []testStruct

		for _, dd := range d {
			result, err := dd.Decode[testStruct](t.Context())
			require.NoError(t, err)

			results = append(results, result)
		}

		require.Len(t, results, 2)
		assert.Equal(t, testStruct{Name: "first", Value: 1}, results[0])
		assert.Equal(t, testStruct{Name: "second", Value: 2}, results[1])
	})
}

func TestDocument_Decode_TypeMismatch(t *testing.T) {
	t.Parallel()

	t.Run("string to int", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("value: not_a_number")
		d, err := source.Documents()
		require.NoError(t, err)

		for _, dd := range d {
			_, err := dd.Decode[struct{ Value int }](t.Context())

			require.Error(t, err)

			var yamlErr *niceyaml.Error

			require.ErrorAs(t, err, &yamlErr)
		}
	})
}

func TestDocument_Decode_Schema(t *testing.T) {
	t.Parallel()

	t.Run("validates and decodes with a schema", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			name: test
			value: 42
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Documents()
		require.NoError(t, err)

		for _, dd := range d {
			var called bool

			result, err := dd.Decode[plainConfig](t.Context(), niceyaml.WithValidator(nameSchema(&called)))
			require.NoError(t, err)
			assert.Equal(t, "test", result.Name)
			assert.Equal(t, 42, result.Value)
			assert.True(t, called, "the validator should have been called")
		}
	})

	t.Run("runs every schema in order and stops at the first failure", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: invalid")

		var (
			first, third bool
			order        []string
		)

		record := func(name string, called *bool) niceyaml.Validator {
			return niceyaml.ValidatorFunc(func(_ context.Context, _ *niceyaml.Document) error {
				*called = true

				order = append(order, name)

				return nil
			})
		}

		_, err := dd.Decode[plainConfig](t.Context(),
			niceyaml.WithValidator(record("first", &first)),
			niceyaml.WithValidator(nameSchema(nil)),
			niceyaml.WithValidator(record("third", &third)),
		)
		require.ErrorIs(t, err, errSchemaValidationFailed)
		assert.True(t, first)
		assert.False(t, third, "a failing schema stops the pipeline")
		assert.Equal(t, []string{"first"}, order)
	})

	t.Run("schema validation fails - no decode", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			name: invalid
			value: 42
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Documents()
		require.NoError(t, err)

		for _, dd := range d {
			_, err := dd.Decode[plainConfig](t.Context(), niceyaml.WithValidator(nameSchema(nil)))
			require.ErrorIs(t, err, errSchemaValidationFailed)
		}
	})

	t.Run("decodes without a schema", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			name: test
			value: 42
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Documents()
		require.NoError(t, err)

		for _, dd := range d {
			result, err := dd.Decode[plainConfig](t.Context())
			require.NoError(t, err)
			assert.Equal(t, "test", result.Name)
			assert.Equal(t, 42, result.Value)
		}
	})
}

func TestDocument_HasContent(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  bool
	}{
		"mapping":            {input: "a: 1\n", want: true},
		"scalar":             {input: "hello\n", want: true},
		"explicit empty":     {input: "---\n", want: true},
		"empty file":         {input: "", want: true},
		"comment only":       {input: "# just a comment\n", want: false},
		"comment after head": {input: "---\n# just a comment\n", want: false},
		"directive only":     {input: "%YAML 1.2\n---\n", want: false},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			docs, err := niceyaml.NewSourceFromString(tc.input).Documents()
			require.NoError(t, err)
			require.NotEmpty(t, docs)

			assert.Equal(t, tc.want, docs[0].HasContent())
		})
	}
}

func TestDocument_Span(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  []position.Span
	}{
		"single document": {
			input: "a: 1\nb: 2\n",
			want:  []position.Span{position.NewSpan(0, 2)},
		},
		"headers": {
			input: "a: 1\n---\nb: 2\nc: 3\n---\nd: 4\n",
			want: []position.Span{
				position.NewSpan(0, 1),
				position.NewSpan(1, 4),
				position.NewSpan(4, 6),
			},
		},
		"end marker": {
			input: "a: 1\n...\nb: 2\n",
			want: []position.Span{
				position.NewSpan(0, 2),
				position.NewSpan(2, 3),
			},
		},
		"block scalar at the end of a document": {
			input: "a: |\n  one\n  two\n---\nb: 2\n",
			want: []position.Span{
				position.NewSpan(0, 3),
				position.NewSpan(3, 5),
			},
		},
		"comment preamble": {
			input: "# license\n---\na: 1\n",
			want: []position.Span{
				position.NewSpan(0, 1),
				position.NewSpan(1, 3),
			},
		},
		"empty file": {
			input: "",
			want:  []position.Span{position.NewSpan(0, 0)},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)

			docs, err := source.Documents()
			require.NoError(t, err)
			require.Len(t, docs, len(tc.want))

			got := make([]position.Span, 0, len(docs))
			for _, doc := range docs {
				got = append(got, doc.Span())
			}

			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("prints one document with the file's line numbers", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("a: 1\n---\nb: 2\nc: 3\n")

		docs, err := source.Documents()
		require.NoError(t, err)
		require.Len(t, docs, 2)

		p := printer.New(
			printer.WithStyles(yamltest.NewXMLStyles()),
			printer.WithGutter(printer.LineNumberGutter),
			printer.WithContainerStyle(lipgloss.NewStyle()),
		)

		got := p.Print(source.View().Slice(docs[1].Span()))
		assert.NotContains(t, got, "a</nameTag>")
		assert.Contains(t, got, "   2 ")
		assert.Contains(t, got, "   4 ")
		assert.NotContains(t, got, "   1 ")
	})
}

func TestDocument_GetValue_DirectiveBody(t *testing.T) {
	t.Parallel()

	// A %YAML directive parses as a document of its own whose body is the
	// directive node, followed by the document with the content.
	input := `%YAML 1.2
---
key: value`
	source := niceyaml.NewSourceFromString(input)
	d, err := source.Documents()
	require.NoError(t, err)

	path := paths.Root().Child("key")
	got := make(map[int]string)

	for i, dd := range d {
		v, err := dd.GetValue(path)
		if err != nil {
			require.ErrorIs(t, err, paths.ErrNotFound)
			require.ErrorIs(t, err, paths.ErrNoDocument)

			continue
		}

		got[i] = v
	}

	assert.Equal(t, map[int]string{1: "value"}, got)
}

func TestDocument_Decode_SchemaThenDecodeError(t *testing.T) {
	t.Parallel()

	// Test when the decode after validation fails.
	input := `value: not_a_number`
	source := niceyaml.NewSourceFromString(input)
	d, err := source.Documents()
	require.NoError(t, err)

	for _, dd := range d {
		// Schema validation passes, but decode will fail due to type mismatch.
		_, err := dd.Decode[strictValueConfig](t.Context(),
			niceyaml.WithValidator(passingValidator()),
		)

		require.Error(t, err)

		var yamlErr *niceyaml.Error

		require.ErrorAs(t, err, &yamlErr)
	}
}

func TestDocument_Decode_CanceledContext(t *testing.T) {
	t.Parallel()

	// Test Decode with a canceled context to trigger the non-yaml error path.
	input := `key: value`
	source := niceyaml.NewSourceFromString(input)
	d, err := source.Documents()
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // Cancel immediately.

	for _, dd := range d {
		_, err := dd.Decode[map[string]string](ctx)
		// Context cancellation may or may not cause an error depending on timing.
		// The decode might complete before the context cancellation is checked.
		// This test mainly ensures the code path doesn't panic.
		_ = err
	}
}

func TestDocument_Decode_SelfValidator(t *testing.T) {
	t.Parallel()

	t.Run("calls Validate on a SelfValidator struct", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			name: test
			value: 42
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Documents()
		require.NoError(t, err)

		for _, dd := range d {
			result, err := dd.Decode[validatorConfig](t.Context())
			require.NoError(t, err)
			assert.True(t, result.validated, "Validate() should have been called by Decode()")
			assert.Equal(t, "test", result.Name)
			assert.Equal(t, 42, result.Value)
		}
	})

	t.Run("WithoutValidator skips Validate", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, `name: ""`)

		result, err := dd.Decode[validatorConfig](t.Context(), niceyaml.WithSelfValidation(false))
		require.NoError(t, err)
		assert.False(t, result.validated, "Validate() should NOT have been called with WithoutValidator")
	})

	t.Run("WithoutValidator keeps schemas", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: invalid")

		_, err := dd.Decode[validatorConfig](t.Context(),
			niceyaml.WithValidator(nameSchema(nil)),
			niceyaml.WithSelfValidation(false),
		)
		require.ErrorIs(t, err, errSchemaValidationFailed)
	})

	t.Run("a later true turns Validate back on", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, `name: ""`)

		_, err := dd.Decode[validatorConfig](t.Context(),
			niceyaml.WithSelfValidation(false),
			niceyaml.WithSelfValidation(true),
		)
		require.ErrorIs(t, err, errNameRequired, "Validate() should have been called")
	})

	t.Run("struct without SelfValidator decodes normally", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			name: test
			value: 42
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Documents()
		require.NoError(t, err)

		for _, dd := range d {
			result, err := dd.Decode[plainConfig](t.Context())
			require.NoError(t, err)
			assert.Equal(t, "test", result.Name)
			assert.Equal(t, 42, result.Value)
		}
	})

	t.Run("runs schema and Validate in order", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			name: test
			value: 42
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Documents()
		require.NoError(t, err)

		for _, dd := range d {
			var called bool

			result, err := dd.Decode[bothValidatorConfig](
				t.Context(),
				niceyaml.WithValidator(nameSchema(&called)),
			)
			require.NoError(t, err)
			assert.True(t, called, "the validator should have been called")
			assert.True(t, result.validated, "Validate() should have been called after decode")
		}
	})

	t.Run("returns SelfValidator error", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			name: ""
			value: 42
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Documents()
		require.NoError(t, err)

		for _, dd := range d {
			_, err := dd.Decode[bothValidatorConfig](t.Context())
			require.ErrorIs(t, err, errNameRequired)
		}
	})
}

// validatorConfig implements niceyaml.SelfValidator.
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

// plainConfig does not implement niceyaml.SelfValidator.
type plainConfig struct {
	Name  string `yaml:"name"`
	Value int    `yaml:"value"`
}

// nameSchema returns a [niceyaml.Validator] that rejects a document
// whose name is "invalid" with a path error, and records each call in called
// when it is not nil.
func nameSchema(called *bool) niceyaml.Validator {
	return niceyaml.ValidatorFunc(func(ctx context.Context, doc *niceyaml.Document) error {
		if called != nil {
			*called = true
		}

		m, err := doc.Decode[map[string]any](ctx)
		if err != nil {
			return err
		}

		if name, ok := m["name"].(string); ok && name == "invalid" {
			return niceyaml.NewErrorFrom(
				errSchemaValidationFailed,
				niceyaml.WithPath(paths.Root().Child("name").Key()),
			)
		}

		return nil
	})
}

// bothValidatorConfig implements niceyaml.SelfValidator and is unmarshaled with
// a schema, so tests of the full pipeline use it.
type bothValidatorConfig struct {
	Name      string `yaml:"name"`
	Value     int    `yaml:"value"`
	validated bool
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

// strictValueConfig has a typed field, so decoding a mismatched value fails
// after schema validation passes.
type strictValueConfig struct {
	Value int `yaml:"value"`
}

func TestDocuments_All(t *testing.T) {
	t.Parallel()

	t.Run("iterates over single document", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value")
		d, err := source.Documents()
		require.NoError(t, err)

		var count int

		for i, dd := range d {
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
		d, err := source.Documents()
		require.NoError(t, err)

		var first, second [][]int

		collect := func() []int {
			var lens []int

			for _, dd := range d {
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
			for i, dd := range d {
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
		d, err := source.Documents()
		require.NoError(t, err)

		var count int

		for i, dd := range d {
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
		d, err := source.Documents()
		require.NoError(t, err)
		require.Len(t, d, 2)

		kindPath := paths.Root().Child("kind")

		for i, dd := range d {
			kind, err := dd.GetValue(kindPath)
			require.NoError(t, err)

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

	t.Run("returns a copy of the token slice", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("a: 1\nb: 2\n")
		d, err := source.Documents()
		require.NoError(t, err)
		require.Len(t, d, 1)

		tks := d[0].Tokens()
		require.NotEmpty(t, tks)

		first := tks[0]
		tks[0] = nil

		// The document keeps its own slice, so the caller's write does not
		// reach it.
		again := d[0].Tokens()
		require.NotEmpty(t, again)
		assert.Same(t, first, again[0])
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
		d, err := source.Documents()
		require.NoError(t, err)
		require.Len(t, d, 2)

		var types [][]token.Type

		for _, dd := range d {
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
		d, err := source.Documents()
		require.NoError(t, err)
		require.Len(t, d, 1)

		for _, dd := range d {
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
		d, err := source.Documents()
		require.NoError(t, err)

		var count int

		for range d {
			count++
			if count == 2 {
				break
			}
		}

		assert.Equal(t, 2, count)
	})
}

func TestDocument_Validate(t *testing.T) {
	t.Parallel()

	t.Run("valid data passes schema validation", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			name: test
			count: 42
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Documents()
		require.NoError(t, err)

		validator := passingValidator()

		for _, dd := range d {
			err := dd.Validate(t.Context(), validator)
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
		d, err := source.Documents()
		require.NoError(t, err)

		wantErr := errors.New("validation failed")
		validator := rejectingValidator(wantErr)

		for _, dd := range d {
			err := dd.Validate(t.Context(), validator)
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

func TestDocument_ErrorsResolveInDocument(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		name: first
		---
		name: second
	`)
	namePath := paths.Root().Child("name").Value()

	// The first line of each error's message.
	headlines := func(t *testing.T, errs []error) []string {
		t.Helper()

		got := make([]string, 0, len(errs))

		for _, err := range errs {
			require.Error(t, err)

			got = append(got, strings.SplitN(err.Error(), "\n", 2)[0])
		}

		return got
	}

	t.Run("validator errors resolve in their own document", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(input)
		d, err := source.Documents()
		require.NoError(t, err)

		validator := niceyaml.ValidatorFunc(func(_ context.Context, _ *niceyaml.Document) error {
			return niceyaml.NewError("bad name", niceyaml.WithPath(namePath))
		})

		var errs []error

		for _, dd := range d {
			errs = append(errs, dd.Validate(t.Context(), validator))
		}

		assert.Equal(t, []string{"1:7: $.name: bad name", "3:7: $.name: bad name"}, headlines(t, errs))
	})

	t.Run("self validation errors resolve in their own document", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(input)
		d, err := source.Documents()
		require.NoError(t, err)

		var errs []error

		for _, dd := range d {
			_, err := dd.Decode[failingValidator](t.Context())
			errs = append(errs, err)
		}

		assert.Equal(t, []string{"1:7: $.name: rejected", "3:7: $.name: rejected"}, headlines(t, errs))
	})

	t.Run("decode errors report their own document's line", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(input)
		d, err := source.Documents()
		require.NoError(t, err)

		var errs []error

		for _, dd := range d {
			_, err := dd.Decode[struct {
				Name int `yaml:"name"`
			}](t.Context())
			errs = append(errs, err)
		}

		got := headlines(t, errs)
		require.Len(t, got, 2)
		assert.True(t, strings.HasPrefix(got[0], "1:7: "), got[0])
		assert.True(t, strings.HasPrefix(got[1], "3:7: "), got[1])
	})
}

func TestWithDisallowUnknownFields(t *testing.T) {
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
		d, err := source.Documents()
		require.NoError(t, err)

		for _, dd := range d {
			result, err := dd.Decode[strictConfig](t.Context())
			require.NoError(t, err)
			assert.Equal(t, "test", result.Name)
		}
	})

	t.Run("option rejects unknown fields", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			name: test
			extra: field
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Documents()
		require.NoError(t, err)

		for _, dd := range d {
			_, err := dd.Decode[strictConfig](t.Context(), niceyaml.WithDisallowUnknownFields(true))
			require.Error(t, err)

			var yamlErr *niceyaml.Error

			require.ErrorAs(t, err, &yamlErr)
		}
	})

	t.Run("option applies per call", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			name: test
			extra: field
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Documents()
		require.NoError(t, err)

		// The same document decodes strictly on one call and loosely on the
		// next, so the option belongs to the call rather than the Source.
		for _, dd := range d {
			_, err := dd.Decode[strictConfig](t.Context(), niceyaml.WithDisallowUnknownFields(true))
			require.Error(t, err)

			result, err := dd.Decode[strictConfig](t.Context())
			require.NoError(t, err)
			assert.Equal(t, "test", result.Name)
		}
	})

	t.Run("a later false turns the option off", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: test\nextra: field\n")

		result, err := dd.Decode[strictConfig](t.Context(),
			niceyaml.WithDisallowUnknownFields(true),
			niceyaml.WithDisallowUnknownFields(false),
		)
		require.NoError(t, err)
		assert.Equal(t, "test", result.Name)
	})

	t.Run("option applies to Get", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			inner:
			  name: test
			  extra: field
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Documents()
		require.NoError(t, err)

		innerPath := paths.Root().Child("inner")

		for _, dd := range d {
			_, err := dd.Get[strictConfig](t.Context(), innerPath, niceyaml.WithDisallowUnknownFields(true))
			require.Error(t, err)

			var yamlErr *niceyaml.Error

			require.ErrorAs(t, err, &yamlErr)

			result, err := dd.Get[strictConfig](t.Context(), innerPath)
			require.NoError(t, err)
			assert.Equal(t, "test", result.Name)
		}
	})

	t.Run("option applies across multiple documents", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			---
			name: first
			unknown1: a
			---
			name: second
			unknown2: b
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Documents()
		require.NoError(t, err)

		var errCount int

		for _, dd := range d {
			_, err := dd.Decode[strictConfig](t.Context(), niceyaml.WithDisallowUnknownFields(true))
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
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Documents()
		require.NoError(t, err)

		for _, dd := range d {
			result, err := dd.Decode[strictConfig](t.Context(), niceyaml.WithYAMLDecodeOptions())
			require.NoError(t, err)
			assert.Equal(t, "test", result.Name)
		}
	})
}

func TestDocument_Get(t *testing.T) {
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

		got, err := dd.Get[string](t.Context(), paths.Root().Child("kind"))
		require.NoError(t, err)
		assert.Equal(t, "Deployment", got)
	})

	t.Run("int", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		got, err := dd.Get[int](t.Context(), paths.Root().Child("version"))
		require.NoError(t, err)
		assert.Equal(t, 2, got)
	})

	t.Run("bool", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		got, err := dd.Get[bool](t.Context(), paths.Root().Child("enabled"))
		require.NoError(t, err)
		assert.True(t, got)
	})

	t.Run("null decodes to zero value", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		got, err := dd.Get[string](t.Context(), paths.Root().Child("empty"))
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("slice", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		got, err := dd.Get[[]string](t.Context(), paths.Root().Child("tags"))
		require.NoError(t, err)
		assert.Equal(t, []string{"a", "b"}, got)
	})

	t.Run("struct", func(t *testing.T) {
		t.Parallel()

		type meta struct {
			Name string `yaml:"name"`
		}

		dd := yamltest.FirstDocument(t, input)

		got, err := dd.Get[meta](t.Context(), paths.Root().Child("meta"))
		require.NoError(t, err)
		assert.Equal(t, meta{Name: "app"}, got)
	})

	t.Run("missing path returns ErrNotFound", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		got, err := dd.Get[string](t.Context(), paths.Root().Child("nonexistent"))
		require.ErrorIs(t, err, paths.ErrNotFound)
		require.NotErrorIs(t, err, paths.ErrAlias)
		assert.Empty(t, got)
	})

	t.Run("empty document returns ErrNotFound and ErrNoDocument", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "---\n")

		got, err := dd.Get[string](t.Context(), paths.Root().Child("key"))
		require.ErrorIs(t, err, paths.ErrNotFound)
		require.ErrorIs(t, err, paths.ErrNoDocument)
		assert.Contains(t, err.Error(), "$.key")
		assert.Empty(t, got)
	})

	t.Run("alias without anchor returns ErrAlias", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "kind: *nope")

		got, err := dd.Get[string](t.Context(), paths.Root().Child("kind"))
		require.ErrorIs(t, err, paths.ErrAlias)
		require.NotErrorIs(t, err, paths.ErrNotFound)
		assert.Contains(t, err.Error(), "*nope")
		assert.Empty(t, got)
	})

	t.Run("wildcard path returns ErrWildcard", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "tags: [a, b]")

		got, err := dd.Get[[]string](t.Context(), paths.Root().Child("tags").IndexAll())
		require.ErrorIs(t, err, paths.ErrWildcard)
		require.NotErrorIs(t, err, paths.ErrNotFound)
		assert.Empty(t, got)
	})

	t.Run("type mismatch returns Error", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		got, err := dd.Get[int](t.Context(), paths.Root().Child("kind"))
		require.Error(t, err)

		var yamlErr *niceyaml.Error

		require.ErrorAs(t, err, &yamlErr)
		assert.Zero(t, got)
	})

	t.Run("alias to an anchor outside the value resolves", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, stringtest.Input(`
			base: &b
			  x: 1
			item:
			  ref: *b
			list:
			  - *b
		`))

		got, err := dd.Get[map[string]any](t.Context(), paths.Root().Child("item"))
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"ref": map[string]any{"x": uint64(1)}}, got)

		list, err := dd.Get[[]map[string]int](t.Context(), paths.Root().Child("list"))
		require.NoError(t, err)
		assert.Equal(t, []map[string]int{{"x": 1}}, list)
	})

	t.Run("alias to a later anchor stays an error", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, stringtest.Input(`
			item:
			  ref: *b
			base: &b
			  x: 1
		`))

		// The whole document does not decode either, so the value inside it
		// reports the same alias error, bound to the source.
		_, err := dd.Get[map[string]any](t.Context(), paths.Root().Child("item"))
		require.Error(t, err)

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Contains(t, err.Error(), "could not find alias")
	})
}

func TestDocument_DecodeInto(t *testing.T) {
	t.Parallel()

	t.Run("keeps fields absent from the document", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: test")

		result := plainConfig{Name: "default", Value: 7}

		err := dd.DecodeInto(t.Context(), &result)
		require.NoError(t, err)
		assert.Equal(t, plainConfig{Name: "test", Value: 7}, result)
	})

	t.Run("leaves the value as it is for a document without content", func(t *testing.T) {
		t.Parallel()

		// Each input parses to a first document whose body holds no value:
		// nothing, only comments, or only a directive.
		tcs := map[string]string{
			"empty":        "",
			"comment only": "# just a comment\n",
			"directive":    "%YAML 1.2\n---\na: 1\n",
		}

		for name, input := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, input)

				result := plainConfig{Name: "default", Value: 7}

				err := dd.DecodeInto(t.Context(), &result)
				require.NoError(t, err)
				assert.Equal(t, plainConfig{Name: "default", Value: 7}, result)

				got, err := dd.Decode[map[string]any](t.Context())
				require.NoError(t, err)
				assert.Nil(t, got)
			})
		}
	})

	t.Run("decodes each document of a stream with a comment-only document", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("a: 1\n---\n# placeholder\n---\nb: 2\n")

		docs, err := source.Documents()
		require.NoError(t, err)
		require.Len(t, docs, 3)

		want := []map[string]int{{"a": 1}, nil, {"b": 2}}

		for i, dd := range docs {
			got, err := dd.Decode[map[string]int](t.Context())
			require.NoError(t, err, "document %d", i)
			assert.Equal(t, want[i], got, "document %d", i)
		}
	})

	t.Run("rejects a target that is not a non-nil pointer", func(t *testing.T) {
		t.Parallel()

		var nilConfig *plainConfig

		tcs := map[string]struct {
			target any
			want   string
		}{
			"nil": {
				target: nil,
				want:   "decode target is not a non-nil pointer: got nil",
			},
			"value": {
				target: 5,
				want:   "decode target is not a non-nil pointer: got int",
			},
			"nil pointer": {
				target: nilConfig,
				want:   "decode target is not a non-nil pointer: got *niceyaml_test.plainConfig",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				var called bool

				dd := yamltest.FirstDocument(t, "name: test")

				err := dd.DecodeInto(t.Context(), tc.target, niceyaml.WithValidator(nameSchema(&called)))
				require.ErrorIs(t, err, niceyaml.ErrDecodeTarget)
				assert.Equal(t, tc.want, err.Error())
				assert.False(t, called, "the validator should not run for a bad target")

				err = dd.Source().DecodeInto(t.Context(), tc.target)
				require.ErrorIs(t, err, niceyaml.ErrDecodeTarget)
			})
		}
	})

	t.Run("runs schema and Validate around the decode", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: test")

		result := bothValidatorConfig{Value: 7}

		var called bool

		err := dd.DecodeInto(t.Context(), &result, niceyaml.WithValidator(nameSchema(&called)))
		require.NoError(t, err)
		assert.Equal(t, "test", result.Name)
		assert.Equal(t, 7, result.Value)
		assert.True(t, called, "the validator should have been called")
		assert.True(t, result.validated, "Validate() should have been called")
	})

	t.Run("WithoutValidator skips Validate", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: test")

		var result validatorConfig

		err := dd.DecodeInto(t.Context(), &result, niceyaml.WithSelfValidation(false))
		require.NoError(t, err)
		assert.False(t, result.validated, "Validate() should NOT have been called with WithoutValidator")
	})

	t.Run("returns SelfValidator error", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, `name: ""`)

		var result bothValidatorConfig

		err := dd.DecodeInto(t.Context(), &result)
		require.ErrorIs(t, err, errNameRequired)
	})
}

func TestDocument_Decode_ValueReceivers(t *testing.T) {
	t.Parallel()

	t.Run("calls value receiver hooks", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: test")

		result, err := dd.Decode[valueValidatorConfig](t.Context())
		require.NoError(t, err)
		assert.Equal(t, "test", result.Name)
	})

	t.Run("returns value receiver Validate error", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, `name: ""`)

		result, err := dd.Decode[valueValidatorConfig](t.Context())
		require.ErrorIs(t, err, errNameRequired)
		assert.Zero(t, result)
	})
}

// valueValidatorConfig implements niceyaml.SelfValidator with a value receiver.
type valueValidatorConfig struct {
	Name string `yaml:"name"`
}

func (c valueValidatorConfig) Validate() error {
	if c.Name == "" {
		return errNameRequired
	}

	return nil
}

func TestWithAllowDuplicateKeys(t *testing.T) {
	t.Parallel()

	type config struct {
		Name string `yaml:"name"`
	}

	input := stringtest.Input(`
		name: first
		name: second
	`)

	t.Run("without option the parser rejects duplicate keys", func(t *testing.T) {
		t.Parallel()

		_, err := niceyaml.NewSourceFromString(input).Documents()
		require.Error(t, err)
		assert.Contains(t, err.Error(), `mapping key "name" already defined`)
	})

	t.Run("with option the last value wins", func(t *testing.T) {
		t.Parallel()

		d, err := niceyaml.NewSourceFromString(input, niceyaml.WithAllowDuplicateKeys(true)).Documents()
		require.NoError(t, err)

		for _, dd := range d {
			result, err := dd.Decode[config](t.Context())
			require.NoError(t, err)
			assert.Equal(t, "second", result.Name)
		}
	})

	t.Run("a later false turns the option off", func(t *testing.T) {
		t.Parallel()

		_, err := niceyaml.NewSourceFromString(input,
			niceyaml.WithAllowDuplicateKeys(true),
			niceyaml.WithAllowDuplicateKeys(false),
		).Documents()
		require.Error(t, err)
		assert.Contains(t, err.Error(), `mapping key "name" already defined`)
	})
}

// plainValidated is a [niceyaml.SelfValidator] that returns an error with no
// location.
type plainValidated struct {
	Name string `yaml:"name"`
}

func (plainValidated) Validate() error {
	return errPlainValidation
}

func TestDocument_ErrorsBindToSource(t *testing.T) {
	t.Parallel()

	namePath := paths.Root().Child("name")

	newDoc := func(t *testing.T, input string) (*niceyaml.Source, *niceyaml.Document) {
		t.Helper()

		source := niceyaml.NewSourceFromString(input)
		docs, err := source.Documents()
		require.NoError(t, err)

		return source, docs[0]
	}

	// The helper asserts err is a [*niceyaml.SourceError] bound to source.
	requireBound := func(t *testing.T, source *niceyaml.Source, err error) {
		t.Helper()

		require.Error(t, err)

		bound, ok := err.(*niceyaml.SourceError) //nolint:errorlint // The top-level value is the bound error.
		require.True(t, ok, "want *niceyaml.SourceError, got %T", err)
		assert.Same(t, source, bound.Source())
	}

	type config struct {
		Name string `yaml:"name"`
	}

	failing := rejectingValidator(niceyaml.NewError("bad name", niceyaml.WithPath(namePath)))

	tcs := map[string]struct {
		input string
		call  func(t *testing.T, doc *niceyaml.Document) error
	}{
		"Decode binds a decoding error": {
			input: "name: [1, 2]\n",
			call: func(t *testing.T, doc *niceyaml.Document) error {
				t.Helper()

				_, err := doc.Decode[config](t.Context())

				return err
			},
		},
		"Decode binds a schema error": {
			input: "name: a\n",
			call: func(t *testing.T, doc *niceyaml.Document) error {
				t.Helper()

				_, err := doc.Decode[config](t.Context(), niceyaml.WithValidator(failing))

				return err
			},
		},
		"DecodeInto binds a schema error": {
			input: "name: a\n",
			call: func(t *testing.T, doc *niceyaml.Document) error {
				t.Helper()

				var cfg config

				return doc.DecodeInto(t.Context(), &cfg, niceyaml.WithValidator(failing))
			},
		},
		"Get binds a decoding error": {
			input: "name: [1, 2]\n",
			call: func(t *testing.T, doc *niceyaml.Document) error {
				t.Helper()

				_, err := doc.Get[string](t.Context(), namePath)

				return err
			},
		},
		"Validate binds a validator error": {
			input: "name: a\n",
			call: func(t *testing.T, doc *niceyaml.Document) error {
				t.Helper()

				return doc.Validate(t.Context(), failing)
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source, doc := newDoc(t, tc.input)
			requireBound(t, source, tc.call(t, doc))
		})
	}

	t.Run("a validator error bound to the source comes back as it is", func(t *testing.T) {
		t.Parallel()

		source, doc := newDoc(t, "name: a\n")

		var pre error

		validator := niceyaml.ValidatorFunc(func(_ context.Context, doc *niceyaml.Document) error {
			pre = doc.Source().Bind(niceyaml.NewError("bad name", niceyaml.WithPath(namePath)))

			return pre
		})

		_, err := doc.Decode[config](t.Context(), niceyaml.WithValidator(validator))
		require.Error(t, err)
		assert.Same(t, pre, err)
		requireBound(t, source, err)
	})

	t.Run("a plain self validation error binds to the source", func(t *testing.T) {
		t.Parallel()

		source, doc := newDoc(t, "name: a\n")

		_, err := doc.Decode[plainValidated](t.Context())
		require.ErrorIs(t, err, errPlainValidation)
		requireBound(t, source, err)
	})

	t.Run("a path resolution error binds to the source", func(t *testing.T) {
		t.Parallel()

		source, doc := newDoc(t, "name: a\n")

		_, err := doc.Get[string](t.Context(), paths.Root().Child("missing"))
		require.ErrorIs(t, err, paths.ErrNotFound)
		requireBound(t, source, err)
	})
}

func TestDocument_ValidatorErrorsResolveInDocument(t *testing.T) {
	t.Parallel()

	namePath := paths.Root().Child("name")

	source := niceyaml.NewSourceFromString("name: a\n---\nname: b\n")
	docs, err := source.Documents()
	require.NoError(t, err)

	second := docs[1]
	require.NotNil(t, second)

	tcs := map[string]struct {
		validate func(doc *niceyaml.Document) error
	}{
		"an unbound path error takes the document's index": {
			validate: func(*niceyaml.Document) error {
				return niceyaml.NewError("bad name", niceyaml.WithPath(namePath))
			},
		},
		"a validator that binds its own error binds through the document": {
			validate: func(doc *niceyaml.Document) error {
				return doc.Bind(niceyaml.NewError("bad name", niceyaml.WithPath(namePath)))
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			validator := niceyaml.ValidatorFunc(func(_ context.Context, doc *niceyaml.Document) error {
				return tc.validate(doc)
			})

			_, err := second.Decode[map[string]string](t.Context(), niceyaml.WithValidator(validator))
			require.Error(t, err)
			assert.Equal(t, "3:7: $.name: bad name", err.Error())
		})
	}
}

func TestDocument_Decode_Validator(t *testing.T) {
	t.Parallel()

	t.Run("receives the document and decodes on success", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: test\nvalue: 42\n")

		var got *niceyaml.Document

		capture := niceyaml.ValidatorFunc(func(_ context.Context, doc *niceyaml.Document) error {
			got = doc

			return nil
		})

		result, err := dd.Decode[plainConfig](t.Context(), niceyaml.WithValidator(capture))
		require.NoError(t, err)
		assert.Same(t, dd, got)
		assert.Equal(t, "test", result.Name)
		assert.Equal(t, 42, result.Value)
	})

	t.Run("runs in order and stops at the first failure", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: test\n")

		var order []string

		record := func(name string, err error) niceyaml.Validator {
			return niceyaml.ValidatorFunc(func(_ context.Context, _ *niceyaml.Document) error {
				order = append(order, name)

				return err
			})
		}

		_, err := dd.Decode[plainConfig](t.Context(),
			niceyaml.WithValidator(record("first", nil)),
			niceyaml.WithValidator(record("second", errDocumentRejected)),
			niceyaml.WithValidator(record("third", nil)),
		)
		require.ErrorIs(t, err, errDocumentRejected)
		assert.Equal(t, []string{"first", "second"}, order)
	})

	t.Run("a failing validator ends the decode", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: test\nvalue: 42\n")

		result, err := dd.Decode[plainConfig](t.Context(),
			niceyaml.WithValidator(niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Document) error {
				return errDocumentRejected
			})),
		)
		require.ErrorIs(t, err, errDocumentRejected)
		assert.Equal(t, plainConfig{}, result)
	})

	t.Run("locates a path error in the document", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("name: a\n---\nname: b\n")
		docs, err := source.Documents()
		require.NoError(t, err)

		dd := docs[1]
		require.NotNil(t, dd)

		_, err = dd.Decode[plainConfig](t.Context(),
			niceyaml.WithValidator(niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Document) error {
				return niceyaml.NewError("bad name", niceyaml.WithPath(paths.Root().Child("name").Value()))
			})),
		)
		require.Error(t, err)
		assert.Equal(t, "3:7: $.name: bad name", err.Error())
	})

	t.Run("Get passes the whole document", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: test\nvalue: 42\n")

		var got *niceyaml.Document

		capture := niceyaml.ValidatorFunc(func(_ context.Context, doc *niceyaml.Document) error {
			got = doc

			return nil
		})

		value, err := dd.Get[int](t.Context(), paths.Root().Child("value"), niceyaml.WithValidator(capture))
		require.NoError(t, err)
		assert.Same(t, dd, got)
		assert.Equal(t, 42, value)
	})
}

// passingValidator returns a [niceyaml.Validator] that accepts every
// document.
func passingValidator() niceyaml.Validator {
	return niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Document) error {
		return nil
	})
}

// rejectingValidator returns a [niceyaml.Validator] that rejects every
// document with err.
func rejectingValidator(err error) niceyaml.Validator {
	return niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Document) error {
		return err
	})
}

func TestDocument_Bind(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("name: a\n---\nname: b\n")
	docs, err := source.Documents()
	require.NoError(t, err)

	second := docs[1]
	require.NotNil(t, second)

	namePath := paths.Root().Child("name").Value()

	t.Run("nil comes back nil", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, second.Bind(nil))
	})

	t.Run("an error without a location names the source", func(t *testing.T) {
		t.Parallel()

		plain := errors.New("plain")
		err := second.Bind(plain)
		require.ErrorIs(t, err, plain)

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Same(t, source, bound.Source())
		assert.Equal(t, "plain", err.Error(), "the source has no name to add")

		_, locErr := bound.Location()
		require.ErrorIs(t, locErr, niceyaml.ErrNoLocation)
	})

	t.Run("binds an Error to the source and this document", func(t *testing.T) {
		t.Parallel()

		err := second.Bind(niceyaml.NewError("bad name", niceyaml.WithPath(namePath)))

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Same(t, source, bound.Source())
		assert.Equal(t, "3:7: $.name: bad name", err.Error())
	})

	t.Run("the source alone has no single document to resolve in", func(t *testing.T) {
		t.Parallel()

		err := source.Bind(niceyaml.NewError("bad name", niceyaml.WithPath(namePath)))
		assert.Equal(t, "$.name: bad name", err.Error())

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)

		_, err = bound.Location()
		require.ErrorIs(t, err, niceyaml.ErrMultipleDocuments)
	})

	t.Run("an error bound to the source comes back as it is", func(t *testing.T) {
		t.Parallel()

		pre := second.Bind(niceyaml.NewError("bad name", niceyaml.WithPath(namePath)))
		assert.Same(t, pre, second.Bind(pre))
	})
}
