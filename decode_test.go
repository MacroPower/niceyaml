package niceyaml_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/diff"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/line"
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

	// The error rejectingUnmarshaler reports from its own decode.
	errUnmarshal = errors.New("unmarshaler rejected the value")
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
	assert.Equal(t, 1, second.DocumentIndex())
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

	t.Run("rejects several documents at a second document without a header", func(t *testing.T) {
		t.Parallel()

		// A "..." marker ends the first document, so the second has no
		// header, and the error points at its first token instead.
		source := niceyaml.NewSourceFromString(stringtest.Input(`
			a: 1
			...
			b: 2
		`))

		_, err := source.Document()
		require.ErrorIs(t, err, niceyaml.ErrMultipleDocuments)
		assert.Equal(t, "3:1: multiple documents in source: 2 documents", err.Error())
	})

	t.Run("rejects several documents past the comments above the second", func(t *testing.T) {
		t.Parallel()

		// The comments after a "..." marker fold into the document below
		// them as its preamble, and the error points past them at the
		// first token of its content.
		source := niceyaml.NewSourceFromString(stringtest.Input(`
			a: 1
			...
			# tail
			b: 2
		`))

		_, err := source.Document()
		require.ErrorIs(t, err, niceyaml.ErrMultipleDocuments)
		assert.Equal(t, "4:1: multiple documents in source: 2 documents", err.Error())
	})

	t.Run("folds a comment block above the first header", func(t *testing.T) {
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
		require.Len(t, docs, 1)
		assert.Same(t, docs[0], doc)

		got, err := doc.Decode[map[string]int](t.Context())
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

		doc, err := source.Document()
		require.NoError(t, err)

		got, err := doc.Decode[map[string]int](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]int{"a": 1}, got)
	})

	t.Run("counts the documents below a comment block", func(t *testing.T) {
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

		got, err := doc.Decode[map[string]int](t.Context())
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

func TestDecode_GoYAMLErrorReachable(t *testing.T) {
	t.Parallel()

	// The binding renders the position itself, so the chain carries the
	// go-yaml message alone, but the original error stays reachable.
	t.Run("decode error", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "b: notanint\n")

		_, err := dd.Decode[struct{ B int }](t.Context())
		require.Error(t, err)

		_, ok := errors.AsType[yaml.Error](err)
		assert.True(t, ok, "yaml.Error is not in the chain: %v", err)
		assert.NotContains(t, err.Error(), "\n", "the go-yaml excerpt leaked into the message")
	})

	t.Run("parse error", func(t *testing.T) {
		t.Parallel()

		_, err := niceyaml.NewSourceFromString("a: [\n").Documents()
		require.Error(t, err)

		_, ok := errors.AsType[yaml.Error](err)
		assert.True(t, ok, "yaml.Error is not in the chain: %v", err)
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
			return niceyaml.ValidatorFunc(func(_ context.Context, _ *niceyaml.Node) error {
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

func TestDocument_Preamble(t *testing.T) {
	t.Parallel()

	// The parser cuts the comments and directives above a header into a
	// node of their own, and Documents folds it into the document below.
	// Each case lists the preamble and the content of every document, as
	// the origin text of their tokens.
	type doc struct {
		preamble string
		content  string
	}

	tcs := map[string]struct {
		input string
		want  []doc
	}{
		"mapping": {
			input: "a: 1\n",
			want:  []doc{{content: "a: 1\n"}},
		},
		"leading comment": {
			input: "# note\na: 1\n",
			want:  []doc{{preamble: "# note\n", content: "a: 1\n"}},
		},
		"comment above the header": {
			input: "# license\n---\na: 1\n",
			want:  []doc{{preamble: "# license\n---\n", content: "a: 1\n"}},
		},
		"comment below the header": {
			input: "--- # note\na: 1\n",
			want:  []doc{{preamble: "--- # note\n", content: "a: 1\n"}},
		},
		"directive above the header": {
			input: "%YAML 1.2\n---\na: 1\n",
			want:  []doc{{preamble: "%YAML 1.2\n---\n", content: "a: 1\n"}},
		},
		"explicit empty": {
			input: "---\n",
			want:  []doc{{preamble: "---\n"}},
		},
		"explicit empty with a comment": {
			input: "---\n# note\n---\nb: 2\n",
			want: []doc{
				{preamble: "---\n# note\n"},
				{preamble: "---\n", content: "b: 2\n"},
			},
		},
		"empty file": {
			input: "",
			want:  []doc{{}},
		},
		"comment only": {
			input: "# note\n",
			want:  []doc{{preamble: "# note\n"}},
		},
		"trailing comment after an end marker": {
			input: "a: 1\n...\n# trailing\n",
			want:  []doc{{content: "a: 1\n...\n# trailing\n"}},
		},
		"leading end marker": {
			input: "...\na: 1\n",
			want:  []doc{{preamble: "...\n", content: "a: 1\n"}},
		},
		"leading end marker above a header": {
			input: "...\n---\na: 1\n",
			want:  []doc{{preamble: "...\n---\n", content: "a: 1\n"}},
		},
		"comment between documents": {
			input: "a: 1\n...\n# note\n---\nb: 2\n",
			want: []doc{
				{content: "a: 1\n...\n"},
				{preamble: "# note\n---\n", content: "b: 2\n"},
			},
		},
		"headers": {
			input: "a: 1\n---\nb: 2\n",
			want: []doc{
				{content: "a: 1\n"},
				{preamble: "---\n", content: "b: 2\n"},
			},
		},
		"comment above a later header": {
			input: "a: 1\n# note\n---\nb: 2\n",
			want: []doc{
				{content: "a: 1\n"},
				{preamble: "# note\n---\n", content: "b: 2\n"},
			},
		},
		"comments and a blank line above a later header": {
			input: "a: 1\n# one\n\n# two\n---\nb: 2\n",
			want: []doc{
				{content: "a: 1\n"},
				{preamble: "# one\n\n# two\n---\n", content: "b: 2\n"},
			},
		},
		"comment above a later header with crlf": {
			input: "a: 1\r\n# note\r\n---\r\nb: 2\r\n",
			want: []doc{
				{content: "a: 1\r\n"},
				{preamble: "# note\r\n---\r\n", content: "b: 2\r\n"},
			},
		},
		"comment after a block scalar above a later header": {
			input: "a: |\n  x\n  y\n# note\n---\nb: 2\n",
			want: []doc{
				{content: "a: |\n  x\n  y\n"},
				{preamble: "# note\n---\n", content: "b: 2\n"},
			},
		},
		"comment on the content line above a later header": {
			input: "a: 1 # same line\n---\nb: 2\n",
			want: []doc{
				{content: "a: 1 # same line\n"},
				{preamble: "---\n", content: "b: 2\n"},
			},
		},
		"comment above a trailing header": {
			input: "a: 1\n# note\n---\n",
			want: []doc{
				{content: "a: 1\n"},
				{preamble: "# note\n---\n"},
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			docs, err := niceyaml.NewSourceFromString(tc.input).Documents()
			require.NoError(t, err)
			require.Len(t, docs, len(tc.want))

			for i, d := range docs {
				preamble := d.Preamble()
				all := d.Tokens()

				// The lexer gives the line ending after a "---" to the token
				// that follows it, so compare the text without the whitespace
				// around it.
				got := doc{
					preamble: strings.TrimSpace(yamltest.DumpTokenOrigins(preamble)),
					content:  strings.TrimSpace(yamltest.DumpTokenOrigins(all[len(preamble):])),
				}
				want := doc{
					preamble: strings.TrimSpace(tc.want[i].preamble),
					content:  strings.TrimSpace(tc.want[i].content),
				}

				assert.Equal(t, want, got, "document %d", i)
				assert.Equal(t, i, d.DocumentIndex())
			}
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
			want:  []position.Span{position.NewSpan(0, 3)},
		},
		"comment between documents": {
			input: "a: 1\n...\n# note\n---\nb: 2\n",
			want: []position.Span{
				position.NewSpan(0, 2),
				position.NewSpan(2, 5),
			},
		},
		"comment above a later header": {
			input: "a: 1\n# note\n---\nb: 2\n",
			want: []position.Span{
				position.NewSpan(0, 1),
				position.NewSpan(1, 4),
			},
		},
		"trailing comment": {
			input: "a: 1\n...\n# note\n",
			want:  []position.Span{position.NewSpan(0, 3)},
		},
		"leading blank lines": {
			input: "\n\na: 1\n",
			want:  []position.Span{position.NewSpan(0, 3)},
		},
		"leading comment": {
			input: "# note\na: 1\n",
			want:  []position.Span{position.NewSpan(0, 2)},
		},
		"leading blank lines before a header": {
			input: "\n---\na: 1\n---\nb: 2\n",
			want: []position.Span{
				position.NewSpan(0, 3),
				position.NewSpan(3, 5),
			},
		},
		"leading end marker": {
			input: "...\na: 1\n---\nb: 2\n",
			want: []position.Span{
				position.NewSpan(0, 2),
				position.NewSpan(2, 4),
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

func TestDocument_View(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		a: 1
		---
		spec:
		  hours:
		    open: "09:00"
		    close: "17:00"
		b: 2
	`)

	t.Run("covers the document with the file's line numbers", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(input)

		docs, err := source.Documents()
		require.NoError(t, err)
		require.Len(t, docs, 2)

		view := docs[1].View()

		assert.Equal(t, source.View().Slice(docs[1].Span()).String(), view.String())
		assert.Equal(t, docs[1].Span().Len(), view.Count())
		assert.Equal(t, 2, view.Lines().Line(docs[1].Span().Start).Number())
		assert.False(t, view.Contains(0), "the view keeps the indices of the source")
	})

	t.Run("covers the node of a scoped Document", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(input)

		docs, err := source.Documents()
		require.NoError(t, err)
		require.Len(t, docs, 2)

		hours := yamltest.At(t, docs[1], paths.Root().Child("spec", "hours"))

		assert.Equal(t, stringtest.JoinLF(
			`   5 |     open: "09:00"`,
			`   6 |     close: "17:00"`,
		), hours.View().String())
	})

	t.Run("each call returns a view of its own", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, input)

		first := doc.View()
		first.Annotate(0, line.Annotation{Content: "here", Placement: line.Below})

		assert.NotEqual(t, first.String(), doc.View().String())
		assert.Equal(t, doc.Source().View().Slice(doc.Span()).String(), doc.View().String())
	})

	t.Run("a bound error marks the view", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(input)

		docs, err := source.Documents()
		require.NoError(t, err)
		require.Len(t, docs, 2)

		var bound *niceyaml.SourceError

		require.ErrorAs(t, docs[1].Bind(niceyaml.NewError("closed", niceyaml.AtPath(paths.Root().Child("b")))), &bound)

		view := docs[1].View()
		require.True(t, bound.Annotate(view))

		assert.Contains(t, view.String(), "   7 | b: 2\n     |    ^")
	})
}

func TestDocument_Lines(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		a: 1
		---
		spec:
		  hours:
		    open: "09:00"
		    close: "17:00"
		b: 2
	`)

	t.Run("covers the document with the file's line numbers", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(input)

		docs, err := source.Documents()
		require.NoError(t, err)
		require.Len(t, docs, 2)

		lines := docs[1].Lines()
		span := docs[1].Span()

		require.Equal(t, span.Len(), lines.Len())

		for i := range lines.Len() {
			assert.Same(t, source.Lines().Line(span.Start+i), lines.Line(i))
		}

		assert.Equal(t, 2, lines.Line(0).Number())
	})

	t.Run("covers the node of a scoped Document", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(input)

		docs, err := source.Documents()
		require.NoError(t, err)

		hours := yamltest.At(t, docs[1], paths.Root().Child("spec", "hours"))
		lines := hours.Lines()

		require.Equal(t, 2, lines.Len())
		assert.Equal(t, `    open: "09:00"`, lines.Line(0).Content())
		assert.Equal(t, 5, lines.Line(0).Number())
	})

	t.Run("diffs one document of a file that holds several", func(t *testing.T) {
		t.Parallel()

		documents := func(t *testing.T, input string) []*niceyaml.Node {
			t.Helper()

			docs, err := niceyaml.NewSourceFromString(input).Documents()
			require.NoError(t, err)
			require.Len(t, docs, 2)

			return docs
		}

		before := documents(t, stringtest.Input(`
			a: 1
			---
			b: 2
			c: 3
		`))
		after := documents(t, stringtest.Input(`
			a: 9
			---
			b: 2
			c: 9
		`))

		result := diff.Diff(before[1].Lines(), after[1].Lines())

		assert.Equal(t, diff.Stats{Added: 1, Removed: 1}, result.Stats())

		unified := result.Unified().String()
		assert.Contains(t, unified, "c: 9")
		assert.NotContains(t, unified, "a: 9", "the first document is not in the diff")
	})
}

func TestDocument_At_DirectiveBody(t *testing.T) {
	t.Parallel()

	// A %YAML directive above the header is the preamble of the document
	// below it, so the path resolves in that document. A document that
	// holds only the directive has no content to resolve in.
	source := niceyaml.NewSourceFromString("%YAML 1.2\n---\nkey: value")
	d, err := source.Documents()
	require.NoError(t, err)
	require.Len(t, d, 1)

	path := paths.Root().Child("key")

	v, err := yamltest.At(t, d[0], path).Decode[string](t.Context())
	require.NoError(t, err)
	assert.Equal(t, "value", v)

	empty := yamltest.FirstDocument(t, "%YAML 1.2\n---\n")

	_, err = empty.At(path)
	require.ErrorIs(t, err, paths.ErrNotFound)
	require.ErrorIs(t, err, paths.ErrNoDocument)
}

func TestDocument_Node(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		kind: Deployment
		meta:
		  name: app
	`)

	t.Run("root scope is the body", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		node, err := dd.AST()
		require.NoError(t, err)
		assert.Same(t, dd.DocumentAST().Body, node)
	})

	t.Run("scope is the node the path selects", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		node, err := yamltest.At(t, dd, paths.Root().Child("meta")).AST()
		require.NoError(t, err)
		assert.Equal(t, "  name: app", node.String())

		want, err := paths.Root().Child("meta").Node(dd.DocumentAST())
		require.NoError(t, err)
		assert.Same(t, want, node)
	})

	t.Run("nested scopes join", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		node, err := yamltest.At(t, yamltest.At(t, dd, paths.Root().Child("meta")), paths.Root().Child("name")).AST()
		require.NoError(t, err)
		assert.Equal(t, "app", node.String())
	})

	t.Run("scope that selects nothing binds the error", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		scoped, err := dd.At(paths.Root().Child("missing"))
		require.ErrorIs(t, err, paths.ErrNotFound)
		assert.Nil(t, scoped)

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Same(t, dd.Source(), bound.Source())
	})

	t.Run("file of whitespace decodes to nothing", func(t *testing.T) {
		t.Parallel()

		// The lexer emits nothing for the text, and yaml.Unmarshal leaves
		// its target as it is, so the decode agrees rather than reading
		// the placeholder token as a string.
		for _, input := range []string{"\n", "  \n", "\t\n", "!"} {
			dd := yamltest.FirstDocument(t, input)

			got := map[string]int{"kept": 1}
			require.NoError(t, dd.DecodeInto(t.Context(), &got), "%q", input)
			assert.Equal(t, map[string]int{"kept": 1}, got, "%q", input)

			v, err := dd.Decode[any](t.Context())
			require.NoError(t, err, "%q", input)
			assert.Nil(t, v, "%q", input)
		}
	})

	t.Run("empty document has no body", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "---\n")

		node, err := dd.AST()
		require.NoError(t, err)
		assert.Nil(t, node)
	})

	t.Run("document of comments has its comment group", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "# only a comment\n")

		node, err := dd.AST()
		require.NoError(t, err)
		assert.IsType(t, &ast.CommentGroupNode{}, node)
	})

	t.Run("a scoped node reaches the whole document", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)
		meta := yamltest.At(t, dd, paths.Root().Child("meta"))

		assert.Same(t, dd, meta.Document())
		assert.Same(t, dd.DocumentAST(), meta.Document().DocumentAST())
		assert.Same(t, dd.Source(), meta.Source())
		assert.Equal(t, paths.Root().Child("meta"), meta.Path())
		assert.True(t, dd.Path().IsRoot())
		assert.Same(t, dd, dd.Document())

		var nothing *niceyaml.Node

		assert.Nil(t, nothing.Document())
	})
}

func TestDocument_Bind_Check(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		kind: Deployment
		spec:
		  hours:
		    open: "09:00"
		    close: "17:00"
	`)

	hoursPath := paths.Root().Child("spec", "hours")

	t.Run("path in a check error resolves from the scope", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)
		hours := yamltest.At(t, dd, hoursPath)

		h, err := hours.Decode[checkHours](t.Context())
		require.NoError(t, err)
		assert.Equal(t, "09:00", h.Open)

		check := func(_ *checkHours) error {
			return niceyaml.NewError("closes too early", niceyaml.AtPath(paths.Root().Child("close")))
		}

		err = hours.Bind(check(&h))

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Same(t, dd.Source(), bound.Source())

		rng, ok := bound.Range()
		require.True(t, ok)
		assert.Equal(t, position.NewRange(position.New(4, 11), position.New(4, 18)), rng)
		assert.Equal(t, "5:12: $.close: closes too early", err.Error())
	})

	t.Run("a passing check binds to nothing", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)
		hours := yamltest.At(t, dd, hoursPath)

		h, err := hours.Decode[checkHours](t.Context())
		require.NoError(t, err)

		check := func(_ *checkHours) error { return nil }

		require.NoError(t, hours.Bind(check(&h)))
	})
}

// checkHours is a value for the tests of a check bound after a decode.
type checkHours struct {
	Open  string `yaml:"open"`
	Close string `yaml:"close"`
}

// accumulatingConfig is a [niceyaml.SelfValidator] whose Validate builds
// its result in a typed pointer, as an accumulator does, so a valid value
// returns a nil [*niceyaml.Error] rather than a nil error.
type accumulatingConfig struct {
	Name  string `yaml:"name"`
	Value int    `yaml:"value"`
}

// Validate implements [niceyaml.SelfValidator].
func (c accumulatingConfig) Validate() error {
	var err *niceyaml.Error

	if c.Value == 0 {
		err = niceyaml.NewError("value is required", niceyaml.AtPath(paths.Root().Child("value")))
	}

	return err
}

func TestDocument_Decode_SchemaThenDecodeError(t *testing.T) {
	t.Parallel()

	// Test when the decode after validation fails.
	input := `value: not_a_number`
	source := niceyaml.NewSourceFromString(input)
	d, err := source.Documents()
	require.NoError(t, err)

	for _, dd := range d {
		// Schema validation passes, but the decode fails on a type mismatch.
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
		// The decode might complete before it checks the context.
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

	t.Run("a typed nil from Validate decodes without error", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("name: test\nvalue: 42\n")
		doc, err := source.Document()
		require.NoError(t, err)

		result, err := doc.Decode[accumulatingConfig](t.Context())
		require.NoError(t, err)
		assert.Equal(t, accumulatingConfig{Name: "test", Value: 42}, result)
	})

	t.Run("a typed non-nil from Validate binds to the document", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("name: test\nvalue: 0\n")
		doc, err := source.Document()
		require.NoError(t, err)

		_, err = doc.Decode[accumulatingConfig](t.Context())

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Equal(t, "2:8: $.value: value is required", bound.Error())
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
		return niceyaml.WrapError(
			errNameRequired,
			niceyaml.AtPath(paths.Root().Child("name").Key()),
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
	return niceyaml.ValidatorFunc(func(ctx context.Context, doc *niceyaml.Node) error {
		if called != nil {
			*called = true
		}

		m, err := doc.Decode[map[string]any](ctx)
		if err != nil {
			return err
		}

		if name, ok := m["name"].(string); ok && name == "invalid" {
			return niceyaml.WrapError(
				errSchemaValidationFailed,
				niceyaml.AtPath(paths.Root().Child("name").Key()),
			)
		}

		return nil
	})
}

// bothValidatorConfig implements niceyaml.SelfValidator, and tests of the full
// pipeline decode it with a schema.
type bothValidatorConfig struct {
	Name      string `yaml:"name"`
	Value     int    `yaml:"value"`
	validated bool
}

func (c *bothValidatorConfig) Validate() error {
	c.validated = true

	if c.Name == "" {
		return niceyaml.WrapError(
			errNameRequired,
			niceyaml.AtPath(paths.Root().Child("name").Key()),
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
			kind, err := yamltest.At(t, dd, kindPath).Decode[string](t.Context())
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

	t.Run("folds a leading comment into the document below it", func(t *testing.T) {
		t.Parallel()

		// The parser cuts a leading comment into a node of its own, and the
		// header that follows starts the document. The comment is the
		// preamble of that document, so its tokens open the document's.
		input := stringtest.Input(`
			# top

			---
			b: 2
		`)
		source := niceyaml.NewSourceFromString(input)
		d, err := source.Documents()
		require.NoError(t, err)
		require.Len(t, d, 1)

		var types [][]token.Type

		for _, dd := range d {
			var docTypes []token.Type

			for _, tk := range dd.Tokens() {
				docTypes = append(docTypes, tk.Type)
			}

			types = append(types, docTypes)
		}

		assert.Equal(t, [][]token.Type{
			{token.CommentType, token.DocumentHeaderType, token.StringType, token.MappingValueType, token.IntegerType},
		}, types)
	})

	t.Run("pairs by offset when the parser collapses consecutive headers", func(t *testing.T) {
		t.Parallel()

		// The go-yaml parser folds everything after consecutive headers into
		// one empty document anchored at the first header, while the
		// splitter cuts a group at each header. The document takes every
		// group from its anchor on, so its tokens cover the same lines its
		// span does.
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
			require.Len(t, tks, 5)
			assert.Equal(t, token.DocumentHeaderType, tks[0].Type)
			assert.Equal(t, token.DocumentHeaderType, tks[1].Type)
			assert.Same(t, source.Tokens()[0], tks[0])
			assert.Equal(t, input, yamltest.DumpTokenOrigins(tks))
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
	return niceyaml.NewError("rejected", niceyaml.AtPath(paths.Root().Child("name")))
}

func TestDocument_ErrorsResolveInDocument(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		name: first
		---
		name: second
	`)
	namePath := paths.Root().Child("name")

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

		validator := niceyaml.ValidatorFunc(func(_ context.Context, _ *niceyaml.Node) error {
			return niceyaml.NewError("bad name", niceyaml.AtPath(namePath))
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
			_, err := yamltest.At(t, dd, innerPath).Decode[strictConfig](
				t.Context(),
				niceyaml.WithDisallowUnknownFields(true),
			)
			require.Error(t, err)

			var yamlErr *niceyaml.Error

			require.ErrorAs(t, err, &yamlErr)

			result, err := yamltest.At(t, dd, innerPath).Decode[strictConfig](t.Context())
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

func TestDocument_Ranges(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		kind: Deployment
		text: first
		  second
		block: |
		  line1
		  line2
		empty:
		list:
		  - a
	`)

	tcs := map[string]struct {
		path paths.Path
		want position.Ranges
		is   error
		key  bool
	}{
		"value": {
			path: paths.Root().Child("kind"),
			want: position.Ranges{position.NewRange(position.New(0, 6), position.New(0, 16))},
		},
		"key": {
			path: paths.Root().Child("kind"),
			key:  true,
			want: position.Ranges{position.NewRange(position.New(0, 0), position.New(0, 4))},
		},
		"key of a sequence element is the element": {
			path: paths.Root().Child("list").Index(0),
			key:  true,
			want: position.Ranges{position.NewRange(position.New(8, 4), position.New(8, 5))},
		},
		"value across lines": {
			path: paths.Root().Child("text"),
			want: position.Ranges{
				position.NewRange(position.New(1, 6), position.New(1, 11)),
				position.NewRange(position.New(2, 2), position.New(2, 8)),
			},
		},
		"block scalar covers its indicator": {
			path: paths.Root().Child("block"),
			want: position.Ranges{position.NewRange(position.New(3, 7), position.New(3, 8))},
		},
		"missing path": {
			path: paths.Root().Child("missing"),
			is:   paths.ErrNotFound,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dd := yamltest.FirstDocument(t, input)

			path := tc.path
			if tc.key {
				path = path.Key()
			}

			got, err := dd.Ranges(path)
			if tc.is != nil {
				require.ErrorIs(t, err, tc.is)

				var bound *niceyaml.SourceError

				require.ErrorAs(t, err, &bound)
				assert.Same(t, dd.Source(), bound.Source())

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("matches the ranges a bound error highlights", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		for _, path := range []paths.Path{
			paths.Root().Child("kind"),
			paths.Root().Child("text"),
			paths.Root().Child("block"),
			paths.Root().Child("empty"),
		} {
			want, err := dd.Ranges(path)
			require.NoError(t, err)

			view := dd.Source().View()

			var bound *niceyaml.SourceError

			require.ErrorAs(t, dd.Bind(niceyaml.NewError("bad", niceyaml.AtPath(path))), &bound)
			require.True(t, bound.Annotate(view))

			var got position.Ranges

			for i := range view.All() {
				for _, o := range view.Overlays(i) {
					// A location with nothing to highlight, such as an
					// empty value, marks its line with an overlay of no
					// width, which Ranges does not report.
					if o.Cols.Len() == 0 {
						continue
					}

					got = append(got, position.NewRange(position.New(i, o.Cols.Start), position.New(i, o.Cols.End)))
				}
			}

			assert.Equal(t, want, got, "path %s", path)
		}
	})
}

func TestDocument_At(t *testing.T) {
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

		got, err := yamltest.At(t, dd, paths.Root().Child("kind")).Decode[string](t.Context())
		require.NoError(t, err)
		assert.Equal(t, "Deployment", got)
	})

	t.Run("int", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		got, err := yamltest.At(t, dd, paths.Root().Child("version")).Decode[int](t.Context())
		require.NoError(t, err)
		assert.Equal(t, 2, got)
	})

	t.Run("bool", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		got, err := yamltest.At(t, dd, paths.Root().Child("enabled")).Decode[bool](t.Context())
		require.NoError(t, err)
		assert.True(t, got)
	})

	t.Run("null decodes to zero value", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		got, err := yamltest.At(t, dd, paths.Root().Child("empty")).Decode[string](t.Context())
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("slice", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		got, err := yamltest.At(t, dd, paths.Root().Child("tags")).Decode[[]string](t.Context())
		require.NoError(t, err)
		assert.Equal(t, []string{"a", "b"}, got)
	})

	t.Run("struct", func(t *testing.T) {
		t.Parallel()

		type meta struct {
			Name string `yaml:"name"`
		}

		dd := yamltest.FirstDocument(t, input)

		got, err := yamltest.At(t, dd, paths.Root().Child("meta")).Decode[meta](t.Context())
		require.NoError(t, err)
		assert.Equal(t, meta{Name: "app"}, got)
	})

	t.Run("missing path returns ErrNotFound", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		got, err := dd.At(paths.Root().Child("nonexistent"))
		require.ErrorIs(t, err, paths.ErrNotFound)
		require.NotErrorIs(t, err, paths.ErrAlias)
		assert.Nil(t, got)
	})

	t.Run("empty document returns ErrNotFound and ErrNoDocument", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "---\n")

		got, err := dd.At(paths.Root().Child("key"))
		require.ErrorIs(t, err, paths.ErrNotFound)
		require.ErrorIs(t, err, paths.ErrNoDocument)
		assert.Contains(t, err.Error(), "$.key")
		assert.Nil(t, got)
	})

	t.Run("alias without anchor returns ErrAlias", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "kind: *nope")

		got, err := dd.At(paths.Root().Child("kind"))
		require.ErrorIs(t, err, paths.ErrAlias)
		require.NotErrorIs(t, err, paths.ErrNotFound)
		assert.Contains(t, err.Error(), "*nope")
		assert.Nil(t, got)
	})

	t.Run("wildcard path returns ErrWildcard", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "tags: [a, b]")

		got, err := dd.At(paths.Root().Child("tags").IndexAll())
		require.ErrorIs(t, err, paths.ErrWildcard)
		require.NotErrorIs(t, err, paths.ErrNotFound)
		assert.Nil(t, got)
	})

	t.Run("type mismatch returns Error", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		got, err := yamltest.At(t, dd, paths.Root().Child("kind")).Decode[int](t.Context())
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

		got, err := yamltest.At(t, dd, paths.Root().Child("item")).Decode[map[string]any](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"ref": map[string]any{"x": uint64(1)}}, got)

		list, err := yamltest.At(t, dd, paths.Root().Child("list")).Decode[[]map[string]int](t.Context())
		require.NoError(t, err)
		assert.Equal(t, []map[string]int{{"x": 1}}, list)
	})

	t.Run("a failure elsewhere in the document does not break Get", func(t *testing.T) {
		t.Parallel()

		// Resolving the alias under $.sub decodes the whole body once to
		// register its anchors. The undefined alias under $.bad fails that
		// pass, and the value the caller asked for still comes back.
		dd := yamltest.FirstDocument(t, stringtest.Input(`
			a: &x 1
			sub: {k: *x}
			bad: *nope
		`))

		got, err := yamltest.At(t, dd, paths.Root().Child("sub")).Decode[map[string]any](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"k": uint64(1)}, got)
	})

	t.Run("alias to a later anchor stays an error", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, stringtest.Input(`
			item:
			  ref: *b
			base: &b
			  x: 1
		`))

		// The anchor is defined after the alias, so the value's own decode
		// reports the alias error, bound to the source.
		_, err := yamltest.At(t, dd, paths.Root().Child("item")).Decode[map[string]any](t.Context())
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

		// Each input parses to a document whose body holds no value:
		// nothing, only comments, or only a directive above an empty
		// document.
		tcs := map[string]string{
			"empty":        "",
			"comment only": "# just a comment\n",
			"directive":    "%YAML 1.2\n---\n",
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

	newDoc := func(t *testing.T, input string) (*niceyaml.Source, *niceyaml.Node) {
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

	failing := rejectingValidator(niceyaml.NewError("bad name", niceyaml.AtPath(namePath)))

	tcs := map[string]struct {
		input string
		call  func(t *testing.T, doc *niceyaml.Node) error
	}{
		"Decode binds a decoding error": {
			input: "name: [1, 2]\n",
			call: func(t *testing.T, doc *niceyaml.Node) error {
				t.Helper()

				_, err := doc.Decode[config](t.Context())

				return err
			},
		},
		"Decode binds a schema error": {
			input: "name: a\n",
			call: func(t *testing.T, doc *niceyaml.Node) error {
				t.Helper()

				_, err := doc.Decode[config](t.Context(), niceyaml.WithValidator(failing))

				return err
			},
		},
		"DecodeInto binds a schema error": {
			input: "name: a\n",
			call: func(t *testing.T, doc *niceyaml.Node) error {
				t.Helper()

				var cfg config

				return doc.DecodeInto(t.Context(), &cfg, niceyaml.WithValidator(failing))
			},
		},
		"Get binds a decoding error": {
			input: "name: [1, 2]\n",
			call: func(t *testing.T, doc *niceyaml.Node) error {
				t.Helper()

				_, err := yamltest.At(t, doc, namePath).Decode[string](t.Context())

				return err
			},
		},
		"Validate binds a validator error": {
			input: "name: a\n",
			call: func(t *testing.T, doc *niceyaml.Node) error {
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

		validator := niceyaml.ValidatorFunc(func(_ context.Context, doc *niceyaml.Node) error {
			pre = yamltest.Bind(t, doc.Source(), niceyaml.NewError("bad name", niceyaml.AtPath(namePath)))

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

		_, err := doc.At(paths.Root().Child("missing"))
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
		validate func(doc *niceyaml.Node) error
	}{
		"an unbound path error takes the document's index": {
			validate: func(*niceyaml.Node) error {
				return niceyaml.NewError("bad name", niceyaml.AtPath(namePath))
			},
		},
		"a validator that binds its own error binds through the document": {
			validate: func(doc *niceyaml.Node) error {
				return doc.Bind(niceyaml.NewError("bad name", niceyaml.AtPath(namePath)))
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			validator := niceyaml.ValidatorFunc(func(_ context.Context, doc *niceyaml.Node) error {
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

		var got *niceyaml.Node

		capture := niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
			got = n

			return nil
		})

		result, err := dd.Decode[plainConfig](t.Context(), niceyaml.WithValidator(capture))
		require.NoError(t, err)
		assert.Same(t, dd, got)
		assert.Same(t, dd, got.Document())
		assert.Equal(t, "test", result.Name)
		assert.Equal(t, 42, result.Value)
	})

	t.Run("runs in order and stops at the first failure", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: test\n")

		var order []string

		record := func(name string, err error) niceyaml.Validator {
			return niceyaml.ValidatorFunc(func(_ context.Context, _ *niceyaml.Node) error {
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
			niceyaml.WithValidator(niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
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
			niceyaml.WithValidator(niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
				return niceyaml.NewError("bad name", niceyaml.AtPath(paths.Root().Child("name")))
			})),
		)
		require.Error(t, err)
		assert.Equal(t, "3:7: $.name: bad name", err.Error())
	})

	t.Run("a scoped decode passes the scope", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: test\nvalue: 42\n")
		valuePath := paths.Root().Child("value")
		scoped := yamltest.At(t, dd, valuePath)

		var got *niceyaml.Node

		capture := niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
			got = n

			return nil
		})

		value, err := scoped.Decode[int](t.Context(), niceyaml.WithValidator(capture))
		require.NoError(t, err)
		assert.Same(t, scoped, got)
		assert.Equal(t, valuePath, got.Path())
		assert.Equal(t, 42, value)
	})
}

// passingValidator returns a [niceyaml.Validator] that accepts every
// document.
func passingValidator() niceyaml.Validator {
	return niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
		return nil
	})
}

// rejectingValidator returns a [niceyaml.Validator] that rejects every
// document with err.
func rejectingValidator(err error) niceyaml.Validator {
	return niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
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

	namePath := paths.Root().Child("name")

	t.Run("nil comes back nil", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, second.Bind(nil))
	})

	t.Run("a nil Error pointer comes back as a nil error", func(t *testing.T) {
		t.Parallel()

		var typed *niceyaml.Error

		require.NoError(t, second.Bind(typed))
	})

	t.Run("a nil SourceError pointer comes back as a nil error", func(t *testing.T) {
		t.Parallel()

		var typed *niceyaml.SourceError

		require.NoError(t, second.Bind(typed))
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

		_, resolved := bound.Range()
		require.False(t, resolved)
		require.NoError(t, bound.Unresolved())
	})

	t.Run("binds an Error to the source and this document", func(t *testing.T) {
		t.Parallel()

		err := second.Bind(niceyaml.NewError("bad name", niceyaml.AtPath(namePath)))

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Same(t, source, bound.Source())
		assert.Equal(t, "3:7: $.name: bad name", err.Error())
	})

	t.Run("an error bound to the source comes back as it is", func(t *testing.T) {
		t.Parallel()

		pre := second.Bind(niceyaml.NewError("bad name", niceyaml.AtPath(namePath)))
		assert.Same(t, pre, second.Bind(pre))
	})
}

// hoursConfig is a value decoded from one node of a document, and its
// Validate names a path from that node.
type hoursConfig struct {
	Open  string `yaml:"open"`
	Close string `yaml:"close"`
}

func (h hoursConfig) Validate() error {
	if h.Open == "" {
		return niceyaml.NewError("open is required", niceyaml.AtPath(paths.Root().Child("open").Key()))
	}

	if h.Open >= h.Close {
		return niceyaml.NewError("open must be before close", niceyaml.AtPath(paths.Root().Child("open")))
	}

	return nil
}

func TestDocument_At_Scope(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		open: 1
		spec:
		  hours:
		    open: "17:00"
		    close: "09:00"
	`)
	hoursPath := paths.Root().Child("spec", "hours")

	// The value of spec.hours.open sits on line 4 (index 3) at column 11
	// (index 10), and the root key open on line 1.
	openValue := position.New(3, 10)

	t.Run("SelfValidator paths resolve from the node", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		_, err := yamltest.At(t, dd, hoursPath).Decode[hoursConfig](t.Context())
		require.Error(t, err)

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)

		rng, ok := bound.Range()
		require.True(t, ok)
		assert.Equal(t, openValue, rng.Start)
		assert.Equal(t, "4:11: $.open: open must be before close", bound.Error())
	})

	t.Run("Bind resolves a path from the node", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)
		hours := yamltest.At(t, dd, hoursPath)

		err := hours.Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Root().Child("open"))))

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)

		rng, ok := bound.Range()
		require.True(t, ok)
		assert.Equal(t, openValue, rng.Start)
		assert.Same(t, hours, bound.Node())
		assert.Same(t, dd, bound.Document())

		// The whole document resolves the same path at its root.
		err = dd.Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Root().Child("open"))))
		require.ErrorAs(t, err, &bound)

		rng, ok = bound.Range()
		require.True(t, ok)
		assert.Equal(t, position.New(0, 6), rng.Start)
	})

	t.Run("Ranges resolves from the node", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		want, err := dd.Ranges(hoursPath.Child("open"))
		require.NoError(t, err)

		got, err := yamltest.At(t, dd, hoursPath).Ranges(paths.Root().Child("open"))
		require.NoError(t, err)
		assert.Equal(t, want, got)
		assert.Equal(t, openValue, got[0].Start)
	})

	t.Run("a validator receives the scope", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		var seen map[string]any

		capture := niceyaml.ValidatorFunc(func(ctx context.Context, doc *niceyaml.Node) error {
			assert.Equal(t, hoursPath, doc.Path())

			var err error

			seen, err = doc.Decode[map[string]any](ctx)

			return err
		})

		h, err := yamltest.At(t, dd, hoursPath).Decode[hoursConfig](t.Context(),
			niceyaml.WithValidator(capture),
			niceyaml.WithSelfValidation(false),
		)
		require.NoError(t, err)
		assert.Equal(t, hoursConfig{Open: "17:00", Close: "09:00"}, h)
		assert.Equal(t, map[string]any{"open": "17:00", "close": "09:00"}, seen)
	})

	t.Run("a validator error resolves from the node", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		reject := niceyaml.ValidatorFunc(func(_ context.Context, _ *niceyaml.Node) error {
			return niceyaml.NewError("bad", niceyaml.AtPath(paths.Root().Child("open")))
		})

		err := yamltest.At(t, dd, hoursPath).Validate(t.Context(), reject)

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)

		rng, ok := bound.Range()
		require.True(t, ok)
		assert.Equal(t, openValue, rng.Start)
	})

	t.Run("scopes chain", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)
		assert.True(t, dd.Path().IsRoot())

		open := yamltest.At(
			t,
			yamltest.At(t, yamltest.At(t, dd, paths.Root().Child("spec")), paths.Root().Child("hours")),
			paths.Root().Child("open"),
		)
		assert.Equal(t, "$.spec.hours.open", open.Path().String())

		got, err := open.Decode[string](t.Context())
		require.NoError(t, err)
		assert.Equal(t, "17:00", got)

		// The receiver keeps its scope.
		assert.True(t, dd.Path().IsRoot())
	})

	t.Run("scope shares the enclosing document", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)
		hours := yamltest.At(t, dd, hoursPath)

		assert.Same(t, dd.Source(), hours.Source())
		assert.Same(t, dd, hours.Document())
		assert.Same(t, dd.DocumentAST(), hours.Document().DocumentAST())
		assert.Equal(t, dd.DocumentIndex(), hours.Document().DocumentIndex())
		assert.Equal(t, dd.Preamble(), hours.Document().Preamble())
		assert.Equal(t, dd.FilePath(), hours.Document().FilePath())
	})

	t.Run("scope covers the lines and tokens of the node", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		tcs := map[string]struct {
			path   paths.Path
			span   position.Span
			tokens []string
		}{
			"block mapping value": {
				path:   hoursPath,
				span:   position.NewSpan(3, 5),
				tokens: []string{"open", ":", "17:00", "close", ":", "09:00"},
			},
			"scalar on the key line": {
				path:   paths.Root().Child("open"),
				span:   position.NewSpan(0, 1),
				tokens: []string{"1"},
			},
			"key selector": {
				path:   hoursPath.Key(),
				span:   position.NewSpan(2, 3),
				tokens: []string{"hours"},
			},
			"nested scalar": {
				path:   hoursPath.Child("close"),
				span:   position.NewSpan(4, 5),
				tokens: []string{"09:00"},
			},
			"nested scalar before a sibling": {
				// The lexer folds the indentation of the next line into the
				// scalar, which does not put that line into the span.
				path:   hoursPath.Child("open"),
				span:   position.NewSpan(3, 4),
				tokens: []string{"17:00"},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				scoped := yamltest.At(t, dd, tc.path)

				assert.Equal(t, tc.span, scoped.Span())

				var got []string

				for _, tk := range scoped.Tokens() {
					got = append(got, tk.Value)
				}

				assert.Equal(t, tc.tokens, got)
			})
		}
	})

	t.Run("scope on a block scalar covers every line of it", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "text: |\n  a\n  b\nnext: 1\n")
		text := yamltest.At(t, dd, paths.Root().Child("text"))

		assert.Equal(t, position.NewSpan(0, 3), text.Span())
		require.Len(t, text.Tokens(), 2)
		assert.Equal(t, "|", text.Tokens()[0].Value)
		assert.Equal(t, "a\nb\n", text.Tokens()[1].Value)
	})

	t.Run("scopes chain to the same extent", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)
		direct := yamltest.At(t, dd, hoursPath)
		chained := yamltest.At(t, yamltest.At(t, dd, paths.Root().Child("spec")), paths.Root().Child("hours"))

		assert.Equal(t, direct.Span(), chained.Span())
		assert.Equal(t, direct.Tokens(), chained.Tokens())
	})

	t.Run("a scope that selects nothing fails at At", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		missing, err := dd.At(paths.Root().Child("spec", "missing"))
		require.ErrorIs(t, err, paths.ErrNotFound)
		assert.Contains(t, err.Error(), "$.spec.missing")
		assert.Nil(t, missing)

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Same(t, dd.Source(), bound.Source())

		// The receiver keeps its scope and its extent.
		assert.True(t, dd.Path().IsRoot())
		assert.Equal(t, dd.Span(), yamltest.FirstDocument(t, input).Span())
	})

	t.Run("excerpt marks the node", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		_, err := yamltest.At(t, dd, hoursPath).Decode[hoursConfig](t.Context())
		require.Error(t, err)

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)

		excerpt, ok := bound.Excerpt(0)
		require.True(t, ok)
		assert.Equal(t, "   4 |     open: \"17:00\"\n     |           ^^^^^^^", excerpt.String())
	})
}

func TestDocument_Decode_ValidatorDecodesWithoutHooks(t *testing.T) {
	t.Parallel()

	t.Run("a validator that decodes the document runs once", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: test\nvalue: 42\n")

		var runs int

		// A validator shaped like schema.Schema.Validate decodes the
		// document to inspect it. Its own decode must carry no hooks, or
		// the validator would run itself again on every decode it performs.
		inspect := niceyaml.ValidatorFunc(func(ctx context.Context, doc *niceyaml.Node) error {
			runs++

			_, err := doc.Decode[any](ctx)

			return err
		})

		result, err := dd.Decode[plainConfig](t.Context(), niceyaml.WithValidator(inspect))
		require.NoError(t, err)
		assert.Equal(t, 1, runs)
		assert.Equal(t, "test", result.Name)
		assert.Equal(t, 42, result.Value)
	})

	t.Run("a validator that reads one field resolves the path once", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: test\nvalue: 42\n")

		readName := niceyaml.ValidatorFunc(func(ctx context.Context, doc *niceyaml.Node) error {
			scoped := yamltest.At(t, doc, paths.Root().Child("name"))

			name, err := scoped.Decode[string](ctx)
			if err != nil {
				return err
			}

			if name != "test" {
				return errNameRequired
			}

			return nil
		})

		_, err := dd.Decode[plainConfig](t.Context(), niceyaml.WithValidator(readName))
		require.NoError(t, err)
	})
}

func TestDocument_At_ErrorBoundToReceiver(t *testing.T) {
	t.Parallel()

	dd := yamltest.FirstDocument(t, "a:\n  b: 1\nc: 2\n")
	scoped := yamltest.At(t, dd, paths.Root().Child("a"))

	_, err := scoped.At(paths.Root().Child("missing"))
	require.ErrorIs(t, err, paths.ErrNotFound)

	// At binds the error to the Node it was called on, not to a copy
	// scoped to the path that did not resolve.
	var bound *niceyaml.SourceError

	require.ErrorAs(t, err, &bound)
	assert.Same(t, scoped, bound.Node())
	assert.Same(t, dd, bound.Document())
}

func TestDocument_At_FlowCollectionSpan(t *testing.T) {
	t.Parallel()

	// The token that closes a flow collection belongs to the node, so the
	// span and the tokens of a scoped Document run through it.
	tcs := map[string]struct {
		input  string
		path   paths.Path
		span   position.Span
		tokens []string
	}{
		"flow sequence over several lines": {
			input:  "a: [\n  1,\n  2,\n]\nb: 3\n",
			path:   paths.Root().Child("a"),
			span:   position.NewSpan(0, 4),
			tokens: []string{"[", "1", ",", "2", ",", "]"},
		},
		"flow mapping at the root": {
			input:  "{\n  \"a\": 1,\n  \"b\": [\n    2\n  ]\n}\n",
			path:   paths.Root(),
			span:   position.NewSpan(0, 6),
			tokens: []string{"{", "a", ":", "1", ",", "b", ":", "[", "2", "]", "}"},
		},
		"flow sequence on one line": {
			input:  "a: [1, 2]\n",
			path:   paths.Root().Child("a"),
			span:   position.NewSpan(0, 1),
			tokens: []string{"[", "1", ",", "2", "]"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			scoped := yamltest.At(t, yamltest.FirstDocument(t, tc.input), tc.path)

			assert.Equal(t, tc.span, scoped.Span())

			var got []string

			for _, tk := range scoped.Tokens() {
				got = append(got, tk.Value)
			}

			assert.Equal(t, tc.tokens, got)
		})
	}
}

func TestDecoder(t *testing.T) {
	t.Parallel()

	type strictConfig struct {
		Name string `yaml:"name"`
	}

	input := stringtest.Input(`
		name: test
		extra: field
		---
		name: other
		extra: field
	`)

	record := func(order *[]string, name string) niceyaml.Validator {
		return niceyaml.ValidatorFunc(func(_ context.Context, _ *niceyaml.Node) error {
			*order = append(*order, name)

			return nil
		})
	}

	t.Run("decodes every node with the options stated once", func(t *testing.T) {
		t.Parallel()

		dec := niceyaml.NewDecoder(niceyaml.WithDisallowUnknownFields(true))

		docs, err := niceyaml.NewSourceFromString(input).Documents()
		require.NoError(t, err)
		require.Len(t, docs, 2)

		for _, dd := range docs {
			_, err := dec.Decode[strictConfig](t.Context(), dd)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "extra")

			var bound *niceyaml.SourceError

			require.ErrorAs(t, err, &bound)
			assert.Same(t, dd.Source(), bound.Source())
		}
	})

	t.Run("without options decodes as Node.Decode does", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: test\n")

		got, err := niceyaml.NewDecoder().Decode[failingValidator](t.Context(), dd)
		require.Error(t, err, "the value validates itself")
		assert.Equal(t, failingValidator{}, got)

		lax, err := niceyaml.NewDecoder(niceyaml.WithSelfValidation(false)).Decode[failingValidator](t.Context(), dd)
		require.NoError(t, err)
		assert.Equal(t, "test", lax.Name)
	})

	t.Run("runs the validators in order and stops at the first that fails", func(t *testing.T) {
		t.Parallel()

		var order []string

		dec := niceyaml.NewDecoder(
			niceyaml.WithValidator(record(&order, "first")),
			niceyaml.WithValidator(niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
				assert.True(t, n.Path().IsRoot())

				return errNameRequired
			})),
			niceyaml.WithValidator(record(&order, "unreached")),
		)

		dd := yamltest.FirstDocument(t, input)

		_, err := dec.Decode[strictConfig](t.Context(), dd)
		require.ErrorIs(t, err, errNameRequired)
		assert.Equal(t, []string{"first"}, order)

		order = nil

		require.ErrorIs(t, dec.Validate(t.Context(), dd), errNameRequired)
		assert.Equal(t, []string{"first"}, order)
	})

	t.Run("Validate runs the validators without decoding", func(t *testing.T) {
		t.Parallel()

		var order []string

		dec := niceyaml.NewDecoder(niceyaml.WithValidator(record(&order, "given")))

		dd := yamltest.FirstDocument(t, "name: test\n")

		require.NoError(t, dec.Validate(t.Context(), dd))
		assert.Equal(t, []string{"given"}, order)

		require.NoError(t, niceyaml.NewDecoder().Validate(t.Context(), dd))
	})

	t.Run("a validator reads the node with Decode without running itself again", func(t *testing.T) {
		t.Parallel()

		runs := 0

		reading := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
			runs++

			// A plain Decode runs no validator, this one included, so
			// the validator reads the node it checks without recursing.
			_, err := n.Decode[map[string]any](ctx)

			return err
		})

		dec := niceyaml.NewDecoder(niceyaml.WithValidator(reading))

		dd := yamltest.FirstDocument(t, input)

		_, err := dec.Decode[strictConfig](t.Context(), dd)
		require.NoError(t, err)
		assert.Equal(t, 1, runs)

		runs = 0

		_, err = dd.Decode[strictConfig](t.Context(), niceyaml.WithValidator(reading))
		require.NoError(t, err)
		assert.Equal(t, 1, runs)
	})

	t.Run("With adds validators and replaces settings on a copy", func(t *testing.T) {
		t.Parallel()

		var order []string

		base := niceyaml.NewDecoder(
			niceyaml.WithValidator(record(&order, "base")),
			niceyaml.WithDisallowUnknownFields(true),
		)
		derived := base.With(
			niceyaml.WithValidator(record(&order, "derived")),
			niceyaml.WithDisallowUnknownFields(false),
		)

		dd := yamltest.FirstDocument(t, input)

		got, err := derived.Decode[strictConfig](t.Context(), dd)
		require.NoError(t, err)
		assert.Equal(t, "test", got.Name)
		assert.Equal(t, []string{"base", "derived"}, order)

		order = nil

		_, err = base.Decode[strictConfig](t.Context(), dd)
		require.Error(t, err, "the receiver keeps its strictness")
		assert.Equal(t, []string{"base"}, order, "the receiver keeps its validators")
	})

	t.Run("a scoped node decodes with the same options", func(t *testing.T) {
		t.Parallel()

		var order []string

		dec := niceyaml.NewDecoder(niceyaml.WithValidator(record(&order, "call")))

		dd := yamltest.FirstDocument(t, input)

		name, err := dec.Decode[string](t.Context(), yamltest.At(t, dd, paths.Root().Child("name")))
		require.NoError(t, err)
		assert.Equal(t, "test", name)
		assert.Equal(t, []string{"call"}, order)
	})

	t.Run("DecodeInto keeps the fields the document leaves out", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: test\n")

		cfg := plainConfig{Value: 7}
		require.NoError(t, niceyaml.NewDecoder().DecodeInto(t.Context(), dd, &cfg))
		assert.Equal(t, plainConfig{Name: "test", Value: 7}, cfg)
	})

	t.Run("rejects a target that is not a pointer", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: test\n")

		var cfg plainConfig

		dec := niceyaml.NewDecoder()

		require.ErrorIs(t, dec.DecodeInto(t.Context(), dd, cfg), niceyaml.ErrDecodeTarget)
		require.ErrorIs(t, dec.DecodeInto(t.Context(), dd, nil), niceyaml.ErrDecodeTarget)
	})
}

func TestNode_Validate(t *testing.T) {
	t.Parallel()

	record := func(order *[]string, name string) niceyaml.Validator {
		return niceyaml.ValidatorFunc(func(_ context.Context, _ *niceyaml.Node) error {
			*order = append(*order, name)

			return nil
		})
	}

	t.Run("runs the validators on the receiver in order", func(t *testing.T) {
		t.Parallel()

		var order []string

		dd := yamltest.FirstDocument(t, "meta:\n  name: test\n")
		scoped := yamltest.At(t, dd, paths.Root().Child("meta"))

		err := scoped.Validate(t.Context(),
			record(&order, "first"),
			niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
				assert.Equal(t, "$.meta", n.Path().String())

				return errNameRequired
			}),
			record(&order, "unreached"),
		)
		require.ErrorIs(t, err, errNameRequired)
		assert.Equal(t, []string{"first"}, order)
	})

	t.Run("with no validators runs none", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: test\n")
		require.NoError(t, dd.Validate(t.Context()))
	})

	t.Run("a validator of one call reaches no other decode", func(t *testing.T) {
		t.Parallel()

		var order []string

		docs, err := niceyaml.NewSourceFromString("name: a\n---\nname: b\n").Documents()
		require.NoError(t, err)

		_, err = docs[0].Decode[map[string]any](t.Context(), niceyaml.WithValidator(record(&order, "call")))
		require.NoError(t, err)

		order = nil

		_, err = docs[1].Decode[map[string]any](t.Context())
		require.NoError(t, err)
		assert.Empty(t, order, "the validator of one call ran on another")
	})
}

func TestNode_Nodes(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString(stringtest.Input(`
		items:
		  - name: tea
		    price: 1
		  - name: coffee
		    price: -1
		spec:
		  image: a
		  nested:
		    image: b
		aliased: &items
		  - x
		ref: *items
	`), niceyaml.WithName("m.yaml"))

	doc, err := source.Document()
	require.NoError(t, err)

	t.Run("scopes each element of a sequence", func(t *testing.T) {
		t.Parallel()

		items, err := doc.Nodes(paths.Root().Child("items").IndexAll())
		require.NoError(t, err)
		require.Len(t, items, 2)

		assert.Equal(t, "$.items[0]", items[0].Path().String())
		assert.Equal(t, "$.items[1]", items[1].Path().String())
		assert.Equal(t, position.NewSpan(1, 3), items[0].Span())
		assert.Same(t, doc, items[1].Document())

		price, err := items[1].At(paths.Root().Child("price"))
		require.NoError(t, err)

		got, err := price.Decode[int](t.Context())
		require.NoError(t, err)
		assert.Equal(t, -1, got)

		// The message keeps the path as the error wrote it, from the
		// scope, and the position is the one it resolved to there.
		err = items[1].Bind(niceyaml.NewError("negative price", niceyaml.AtPath(paths.Root().Child("price"))))
		require.EqualError(t, err, "m.yaml:5:12: $.price: negative price")
	})

	t.Run("scopes each entry a recursive selector finds", func(t *testing.T) {
		t.Parallel()

		images, err := doc.Nodes(paths.Root().Recursive("image"))
		require.NoError(t, err)
		require.Len(t, images, 2)

		assert.Equal(t, "$.spec.image", images[0].Path().String())
		assert.Equal(t, "$.spec.nested.image", images[1].Path().String())

		got, err := images[1].Decode[string](t.Context())
		require.NoError(t, err)
		assert.Equal(t, "b", got)
	})

	t.Run("resolves from the scope of the receiver", func(t *testing.T) {
		t.Parallel()

		spec := yamltest.At(t, doc, paths.Root().Child("spec"))

		images, err := spec.Nodes(paths.Root().Recursive("image"))
		require.NoError(t, err)
		require.Len(t, images, 2)
		assert.Equal(t, "$.spec.image", images[0].Path().String())
	})

	t.Run("a single path yields its one node", func(t *testing.T) {
		t.Parallel()

		nodes, err := doc.Nodes(paths.Root().Child("spec", "image"))
		require.NoError(t, err)
		require.Len(t, nodes, 1)
		assert.Equal(t, "$.spec.image", nodes[0].Path().String())
	})

	t.Run("a path that selects nothing yields no nodes", func(t *testing.T) {
		t.Parallel()

		nodes, err := doc.Nodes(paths.Root().Child("missing").IndexAll())
		require.NoError(t, err)
		assert.Empty(t, nodes)
	})

	t.Run("a node through an alias keeps the path as written", func(t *testing.T) {
		t.Parallel()

		nodes, err := doc.Nodes(paths.Root().Child("ref").IndexAll())
		require.NoError(t, err)
		require.Len(t, nodes, 1)
		assert.Equal(t, "$.ref[0]", nodes[0].Path().String())

		// The content lies at the anchor, so the lines are the anchor's.
		assert.Equal(t, position.NewSpan(10, 11), nodes[0].Span())
	})

	t.Run("an empty document binds the error to the receiver", func(t *testing.T) {
		t.Parallel()

		empty := yamltest.FirstDocument(t, "# only a comment\n")

		_, err := empty.Nodes(paths.Root().IndexAll())
		require.ErrorIs(t, err, paths.ErrNoDocument)

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Same(t, empty.Source(), bound.Source())
	})
}

// rejectingUnmarshaler decodes itself and reports errUnmarshal, so the
// error the decoder returns is the caller's own rather than go-yaml's.
type rejectingUnmarshaler struct{}

func (*rejectingUnmarshaler) UnmarshalYAML([]byte) error {
	return errUnmarshal
}

// reparsingUnmarshaler decodes itself by parsing the bytes it gets again,
// so the go-yaml error it returns carries a token of that parse rather
// than one of the source.
type reparsingUnmarshaler struct {
	N int
}

func (u *reparsingUnmarshaler) UnmarshalYAML(data []byte) error {
	var raw struct{ N int }

	err := yaml.Unmarshal(data, &raw)
	if err != nil {
		return fmt.Errorf("custom context: %w", err)
	}

	u.N = raw.N

	return nil
}

func TestErrDecodeRejected(t *testing.T) {
	t.Parallel()

	rejected := map[string]struct {
		input  string
		decode func(ctx context.Context, dd *niceyaml.Node) error
	}{
		"type mismatch": {
			input: "value: abc",
			decode: func(ctx context.Context, dd *niceyaml.Node) error {
				_, err := dd.Decode[struct{ Value int }](ctx)

				return err
			},
		},
		"overflow": {
			input: "value: 300",
			decode: func(ctx context.Context, dd *niceyaml.Node) error {
				_, err := dd.Decode[struct{ Value int8 }](ctx)

				return err
			},
		},
		"unknown field": {
			input: "other: 1",
			decode: func(ctx context.Context, dd *niceyaml.Node) error {
				_, err := dd.Decode[struct{ Value int }](ctx, niceyaml.WithDisallowUnknownFields(true))

				return err
			},
		},
		"mapping into scalar": {
			input: "value:\n  a: 1",
			decode: func(ctx context.Context, dd *niceyaml.Node) error {
				_, err := dd.Decode[struct{ Value string }](ctx)

				return err
			},
		},
		"decoder": {
			input: "value: abc",
			decode: func(ctx context.Context, dd *niceyaml.Node) error {
				var v struct{ Value int }

				return niceyaml.NewDecoder().DecodeInto(ctx, dd, &v)
			},
		},
	}

	for name, tc := range rejected {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dd := yamltest.FirstDocument(t, tc.input)

			err := tc.decode(t.Context(), dd)
			require.ErrorIs(t, err, niceyaml.ErrDecodeRejected)

			var srcErr *niceyaml.SourceError

			require.ErrorAs(t, err, &srcErr, "the rejection is not bound to the source")

			_, ok := errors.AsType[yaml.Error](err)
			assert.True(t, ok, "the go-yaml error left the chain")
		})
	}

	t.Run("unmarshaler error does not match", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "value: 1")

		_, err := dd.Decode[rejectingUnmarshaler](t.Context())
		require.ErrorIs(t, err, errUnmarshal)
		require.NotErrorIs(t, err, niceyaml.ErrDecodeRejected)
	})

	t.Run("unmarshaler parse error does not match", func(t *testing.T) {
		t.Parallel()

		// The error of the value's own parse carries a token of the bytes
		// it parsed, not of the source, so it comes back as the value's
		// own error rather than as a rejection at the wrong line.
		dd := yamltest.FirstDocument(t, "a: 1\nb: x\nc: y\nz:\n  n: notanumber\n")

		_, err := dd.Decode[struct{ Z reparsingUnmarshaler }](t.Context())
		require.Error(t, err)
		require.NotErrorIs(t, err, niceyaml.ErrDecodeRejected)

		var srcErr *niceyaml.SourceError

		require.ErrorAs(t, err, &srcErr)

		_, ok := srcErr.Range()
		assert.False(t, ok, "the error took a location from the value's own parse")
	})

	t.Run("decode target does not match", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "value: 1")

		err := dd.DecodeInto(t.Context(), nil)
		require.ErrorIs(t, err, niceyaml.ErrDecodeTarget)
		require.NotErrorIs(t, err, niceyaml.ErrDecodeRejected)
	})

	t.Run("parse error does not match", func(t *testing.T) {
		t.Parallel()

		_, err := niceyaml.NewSourceFromString("a: [\n").Documents()
		require.Error(t, err)
		require.NotErrorIs(t, err, niceyaml.ErrDecodeRejected)
	})

	t.Run("canceled context does not match", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "value: 1")

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := dd.Decode[struct{ Value int }](ctx)
		if err != nil {
			require.NotErrorIs(t, err, niceyaml.ErrDecodeRejected)
		}
	})
}

func TestMultiValidator(t *testing.T) {
	t.Parallel()

	errB := errors.New("bad b")
	errC := errors.New("bad c")

	badB := niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
		return niceyaml.WrapError(errB, niceyaml.AtPath(paths.Root().Child("a", "b")))
	})
	badC := niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
		// A validator that binds its own error, as a schema does.
		return n.Bind(niceyaml.WrapError(errC, niceyaml.AtPath(paths.Root().Child("a", "c"))))
	})
	passing := niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
		return nil
	})
	record := func(order *[]string, name string) niceyaml.Validator {
		return niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
			*order = append(*order, name)

			return nil
		})
	}

	dd := yamltest.FirstDocument(t, "a:\n  b: 1\n  c: 2\n")

	t.Run("runs every validator and reports every failure in order", func(t *testing.T) {
		t.Parallel()

		var order []string

		multi := niceyaml.MultiValidator(record(&order, "first"), badB, record(&order, "second"), badC)

		err := dd.Validate(t.Context(), multi)
		require.ErrorIs(t, err, errB)
		require.ErrorIs(t, err, errC)
		assert.Equal(t, []string{"first", "second"}, order)

		var got []string

		for b := range niceyaml.AllBindings(err) {
			if rng, ok := b.Range(); ok {
				got = append(got, rng.Start.String()+" "+b.Message())
			}
		}

		assert.Equal(t, []string{"1:5 bad b", "2:5 bad c"}, got)
	})

	t.Run("renders as one tree", func(t *testing.T) {
		t.Parallel()

		err := dd.Validate(t.Context(), niceyaml.MultiValidator(badB, badC))
		require.Error(t, err)

		assert.Equal(t, "|-- 2:6: $.a.b: bad b\n`-- 3:6: $.a.c: bad c", report(err))
	})

	t.Run("no failure is no error", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, dd.Validate(t.Context(), niceyaml.MultiValidator(passing, passing)))
		require.NoError(t, dd.Validate(t.Context(), niceyaml.MultiValidator()))

		// A typed nil pointer reports no failure, as it does from a
		// validator given alone.
		typedNil := niceyaml.ValidatorFunc(func(_ context.Context, _ *niceyaml.Node) error {
			var e *niceyaml.Error

			return e
		})

		require.NoError(t, dd.Validate(t.Context(), typedNil))
		require.NoError(t, dd.Validate(t.Context(), niceyaml.MultiValidator(typedNil, passing)))
	})

	t.Run("a Decoder carries it to every decode", func(t *testing.T) {
		t.Parallel()

		dec := niceyaml.NewDecoder(niceyaml.WithValidator(niceyaml.MultiValidator(badB, badC)))

		_, err := dec.Decode[map[string]any](t.Context(), dd)
		require.ErrorIs(t, err, errB)
		require.ErrorIs(t, err, errC)
	})

	t.Run("the message names the source once", func(t *testing.T) {
		t.Parallel()

		src := niceyaml.NewSourceFromString("a:\n  b: 1\n  c: 2\n", niceyaml.WithFilePath("x.yaml"))

		doc, err := src.Document()
		require.NoError(t, err)

		err = doc.Validate(t.Context(), niceyaml.MultiValidator(badB, badB))
		require.Error(t, err)
		assert.Equal(t, "x.yaml: $.a.b: bad b\n$.a.b: bad b", err.Error())
	})

	t.Run("a context that ends stops the run", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())

		calls := 0
		canceling := niceyaml.ValidatorFunc(func(ctx context.Context, _ *niceyaml.Node) error {
			calls++

			cancel()

			return ctx.Err()
		})

		err := dd.Validate(ctx, niceyaml.MultiValidator(badB, canceling, badC))
		require.ErrorIs(t, err, context.Canceled)
		require.NotErrorIs(t, err, errB)
		assert.Equal(t, 1, calls)

		require.ErrorIs(t, dd.Validate(ctx, niceyaml.MultiValidator(canceling)), context.Canceled)
		assert.Equal(t, 1, calls, "an ended context runs no validator")
	})
}
