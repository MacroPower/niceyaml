package niceyaml_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/diff"
	"go.jacobcolvin.com/niceyaml/encoder"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/schema"
	"go.jacobcolvin.com/niceyaml/tokens"
)

// Test sentinel errors for mock validators.
var (
	errSchemaValidationFailed = errors.New("schema validation failed: name cannot be 'invalid'")
	errNameRequired           = errors.New("name is required")
	errDocumentRejected       = errors.New("document rejected")
	errPlainValidation        = errors.New("plain validation failure")

	// The error rejectingUnmarshaler and wrappingUnmarshaler report from
	// their own decode.
	errUnmarshal = errors.New("unmarshaler rejected the value")

	// The error decodeLevel reports for a name that is no level.
	errUnknownLevel = errors.New("unknown level")
)

func TestSource_Decoder(t *testing.T) {
	t.Parallel()

	t.Run("creates decoder from source", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("key: value")
		d := source.Documents()
		require.NotNil(t, d)
	})

	t.Run("creates decoder from empty source", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("")
		d := source.Documents()
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

	docs := source.Documents()
	require.Len(t, docs, 2)

	second := docs[1]
	assert.Equal(t, 1, second.DocumentIndex())
	assert.Equal(t, "two.yaml", second.FilePath())
	assert.Same(t, source, second.Source())
	assert.NotNil(t, second.Tokens())

	// Every call hands out the same root Node for an index, in a slice of
	// its own.
	again := source.Documents()
	assert.Same(t, second, again[1])

	again[1] = nil

	third := source.Documents()
	assert.Same(t, second, third[1])
}

func TestSource_Document(t *testing.T) {
	t.Parallel()

	t.Run("returns the single document", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("a: 1")

		doc, err := source.Document()
		require.NoError(t, err)

		docs := source.Documents()
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

	t.Run("returns the document with content beside the empty ones", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input string
			// The index in the file of the document Document returns.
			want int
		}{
			"trailing header": {
				input: "b: 2\n---\n",
				want:  0,
			},
			"comment below a trailing header": {
				input: "b: 2\n---\n# Source: t.yaml\n",
				want:  0,
			},
			// The first header holds an empty document, so the second
			// header starts another one.
			"header that follows another": {
				input: "---\n---\nb: 2\n",
				want:  1,
			},
			"empty documents on both sides": {
				input: "--- # head\n---\nb: 2\n---\n---\n",
				want:  1,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				source := niceyaml.NewSourceFromString(tc.input)

				doc, err := source.Document()
				require.NoError(t, err)
				assert.Equal(t, tc.want, doc.DocumentIndex())
				assert.Same(t, source.AllDocuments()[tc.want], doc)

				got, err := source.Decode[map[string]int](t.Context())
				require.NoError(t, err)
				assert.Equal(t, map[string]int{"b": 2}, got)
			})
		}
	})

	t.Run("rejects several documents past an empty one between them", func(t *testing.T) {
		t.Parallel()

		// The empty document between the two does not count, and the
		// error points at the header of the second document with content.
		source := niceyaml.NewSourceFromString(stringtest.Input(`
			a: 1
			---
			---
			b: 2
		`))

		_, err := source.Document()
		require.ErrorIs(t, err, niceyaml.ErrMultipleDocuments)
		assert.Equal(t, "3:1: multiple documents in source: 2 documents", err.Error())
	})

	t.Run("rejects several empty documents", func(t *testing.T) {
		t.Parallel()

		// A file of empty documents alone keeps each of them.
		source := niceyaml.NewSourceFromString(stringtest.Input(`
			---
			# a
			---
			# b
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

		docs := source.Documents()
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

		docs := source.Documents()
		assert.Same(t, docs[0], doc)

		got, err := doc.Decode[map[string]int](t.Context())
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("returns the empty document of a stream of markers alone", func(t *testing.T) {
		t.Parallel()

		// The parser finds no document in a stream of "..." markers alone,
		// and the Source gives it one empty document, as it gives an
		// empty file. The markers and any comment on their lines are the
		// preamble of that document.
		tcs := map[string]struct {
			input string
			lines int
		}{
			"lone marker": {
				input: "...\n",
				lines: 1,
			},
			"marker without a line break": {
				input: "...",
				lines: 1,
			},
			"marker with comment": {
				input: "... # license\n",
				lines: 1,
			},
			"two markers": {
				input: "...\n...\n",
				lines: 2,
			},
			"blank lines above a marker": {
				input: "\n\n...\n",
				lines: 3,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				source := niceyaml.NewSourceFromString(tc.input)

				docs := source.Documents()
				require.Len(t, docs, 1)

				doc, err := source.Document()
				require.NoError(t, err)
				assert.Same(t, docs[0], doc)

				file, err := source.File()
				require.NoError(t, err)
				require.Len(t, file.Docs, 1)
				assert.Same(t, file.Docs[0], doc.DocumentAST())

				assert.Nil(t, doc.AST())
				assert.Equal(t, source.Tokens(), doc.Tokens())
				assert.Equal(t, doc.Tokens(), doc.Preamble())
				assert.Equal(t, position.NewSpan(0, tc.lines), doc.Span())

				got, err := source.Decode[map[string]int](t.Context())
				require.NoError(t, err)
				assert.Nil(t, got)
			})
		}
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
	// go-yaml message alone, but the original error stays reachable. The
	// text of the go-yaml error holds an excerpt of the source, which no
	// message of the chain may hold.
	t.Run("decode error", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "b: notanint\n")

		_, err := dd.Decode[struct{ B int }](t.Context())
		require.Error(t, err)

		yamlErr, ok := errors.AsType[yaml.Error](err)
		require.True(t, ok, "errors.As finds no yaml.Error: %v", err)
		require.Contains(t, yamlErr.Error(), "b: notanint")

		for _, msg := range chainMessages(err) {
			assert.NotContains(t, msg, "notanint", "the go-yaml excerpt leaked into the chain")
		}
	})

	t.Run("parse error", func(t *testing.T) {
		t.Parallel()

		_, err := niceyaml.NewSourceFromString("a: [\n").File()
		require.Error(t, err)

		yamlErr, ok := errors.AsType[yaml.Error](err)
		require.True(t, ok, "errors.As finds no yaml.Error: %v", err)
		require.Contains(t, yamlErr.Error(), "a: [")

		for _, msg := range chainMessages(err) {
			assert.NotContains(t, msg, "a: [", "the go-yaml excerpt leaked into the chain")
		}
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

		dd := yamltest.FirstDocument(t, "key: value")

		result, err := dd.Decode[map[string]string](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"key": "value"}, result)
	})

	t.Run("decode to struct", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			name: test
			value: 42
		`)
		dd := yamltest.FirstDocument(t, input)

		result, err := dd.Decode[testStruct](t.Context())
		require.NoError(t, err)
		assert.Equal(t, testStruct{Name: "test", Value: 42}, result)
	})

	t.Run("decode to slice", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			- one
			- two
			- three
		`)
		dd := yamltest.FirstDocument(t, input)

		result, err := dd.Decode[[]string](t.Context())
		require.NoError(t, err)
		assert.Equal(t, []string{"one", "two", "three"}, result)
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
		d := source.Documents()

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

	t.Run("alias with a trailing comment", func(t *testing.T) {
		t.Parallel()

		// The parser attaches a comment on the line of an alias to its
		// name, which the go-yaml decoder looks the anchor up by.
		tcs := map[string]struct {
			input   string
			path    paths.Path
			want    any
			comment string
		}{
			"mapping value": {
				input:   "base: &x 1\nref: *x # same as base\n",
				path:    paths.Current(),
				want:    map[string]any{"base": uint64(1), "ref": uint64(1)},
				comment: "# same as base",
			},
			"sequence item": {
				input:   "- &x 1\n- *x # c\n",
				path:    paths.Current(),
				want:    []any{uint64(1), uint64(1)},
				comment: "# c",
			},
			"scoped node": {
				input:   "base: &x 1\nsub:\n  ref: *x # same as base\n",
				path:    paths.Current().Child("sub"),
				want:    map[string]any{"ref": uint64(1)},
				comment: "# same as base",
			},
			"scoped node through an anchor": {
				input:   "a: &a 1\nb: &b\n  - *a # c\nsub: {k: *b}\n",
				path:    paths.Current().Child("sub"),
				want:    map[string]any{"k": []any{uint64(1)}},
				comment: "# c",
			},
			"spec example 2.10": {
				input: stringtest.Input(`
					---
					hr:
					  - Mark McGwire
					  # Following node labeled SS
					  - &SS Sammy Sosa
					rbi:
					  - *SS # Subsequent occurrence
					  - Ken Griffey
				`),
				path: paths.Current(),
				want: map[string]any{
					"hr":  []any{"Mark McGwire", "Sammy Sosa"},
					"rbi": []any{"Sammy Sosa", "Ken Griffey"},
				},
				comment: "# Subsequent occurrence",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				got, err := yamltest.At(t, dd, tc.path).Decode[any](t.Context())
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)

				// The decode leaves the tree the Source shares as it was.
				assert.Contains(t, dd.DocumentAST().String(), tc.comment)
			})
		}
	})

	t.Run("comment on a line of its own after the content", func(t *testing.T) {
		t.Parallel()

		// When it keeps comments, the go-yaml parser rejects such a comment
		// in three places. One is below a root that is not a block mapping
		// or a block sequence. One is left of the first key or "-" of such
		// a root. One is between a directive and its header. Each case
		// lists the value of every document.
		tcs := map[string]struct {
			input string
			want  []any
		}{
			"plain scalar": {
				input: "x\n# c\n",
				want:  []any{"x"},
			},
			"quoted scalar on two lines": {
				input: "\"a\n b\"\n# c\n",
				want:  []any{"a b"},
			},
			"anchored scalar": {
				input: "&a x\n# c\n",
				want:  []any{"x"},
			},
			"tagged scalar": {
				input: "!!str 1\n# c\n",
				want:  []any{"1"},
			},
			"indented comment": {
				input: "x\n  # c\n",
				want:  []any{"x"},
			},
			"comment on the line and below it": {
				input: "x # same\n# c\n",
				want:  []any{"x"},
			},
			// The parser attaches a comment only to a token that starts on
			// its line, so it reads a comment on the last line of a scalar
			// on several lines as one on a line of its own.
			"comment on the last line of a plain scalar": {
				input: "a\n b # c\n",
				want:  []any{"a b"},
			},
			"comment on the last line of a single-quoted scalar": {
				input: "'x\n y' # c\n",
				want:  []any{"x y"},
			},
			"comment on the last line of a double-quoted scalar above a header": {
				input: "\"multi\n line\" # c\n---\nx\n",
				want:  []any{"multi line", "x"},
			},
			"comment on the last line of a tagged scalar": {
				input: "!!str a\n b # c\n",
				want:  []any{"a b"},
			},
			"comment on the last line of an anchored scalar": {
				input: "&x a\n b # c\n",
				want:  []any{"a b"},
			},
			"comment on the last line of a scalar above an end marker": {
				input: "--- a\n b # c\n...\n",
				want:  []any{"a b"},
			},
			"comments on and below the last line of a scalar": {
				input: "a\n b # c\n# d\n",
				want:  []any{"a b"},
			},
			"flow sequence": {
				input: "[1, 2]\n# c\n",
				want:  []any{[]any{uint64(1), uint64(2)}},
			},
			"flow mapping": {
				input: "{a: 1}\n# c\n",
				want:  []any{map[string]any{"a": uint64(1)}},
			},
			"block scalar": {
				input: "--- |\n  lit\n# c\n",
				want:  []any{"lit\n"},
			},
			"scalar above an end marker": {
				input: "x\n# c\n...\n--- y\n",
				want:  []any{"x", "y"},
			},
			"flow sequence above a header": {
				input: "--- [1, 2]\n# c\n# d\n--- [3]\n",
				want:  []any{[]any{uint64(1), uint64(2)}, []any{uint64(3)}},
			},
			"scalar after a directive": {
				input: "%YAML 1.2\n---\nx\n# c\n",
				want:  []any{"x"},
			},
			"comment between a directive and its header": {
				input: "%YAML 1.2\n# c\n---\na: 1\n",
				want:  []any{map[string]any{"a": uint64(1)}},
			},
			"comment between a tag directive and its header": {
				input: "%TAG !e! tag:example.com,2000:\n# c\n\n# d\n---\n!e!x y\n",
				want:  []any{"y"},
			},
			"comment left of an indented mapping below a header": {
				input: "---\n  apiVersion: v1\n  kind: Pod\n# end\n",
				want:  []any{map[string]any{"apiVersion": "v1", "kind": "Pod"}},
			},
			"comment left of an indented sequence above an end marker": {
				input: "  - a\n  - b\n# c\n...\n",
				want:  []any{[]any{"a", "b"}},
			},
			"comment left of a tagged indented mapping": {
				input: "!!map\n  a: 1\n# c\n",
				want:  []any{map[string]any{"a": uint64(1)}},
			},
			"comment left of an indented mapping below a tagged header": {
				input: "--- !!map\n  a: 1\n# c\n",
				want:  []any{map[string]any{"a": uint64(1)}},
			},
			"comment left of an indented mapping above a header": {
				input: "  a: 1\n# c\n---\nb: 2\n",
				want:  []any{map[string]any{"a": uint64(1)}, map[string]any{"b": uint64(2)}},
			},
			"comment left of an indented explicit key": {
				input: "  ? a\n  : 1\n# c\n",
				want:  []any{map[string]any{"a": uint64(1)}},
			},
			"comment left of an indented anchored key": {
				input: "  &x a: 1\n # c\n",
				want:  []any{map[string]any{"a": uint64(1)}},
			},
			"comment left of an indented nested mapping": {
				input: "  a:\n    b: 1\n  # c\n# d\n",
				want:  []any{map[string]any{"a": map[string]any{"b": uint64(1)}}},
			},
			"comments in and left of the column of an indented mapping": {
				input: "  a: 1\n  # c\n# d\n",
				want:  []any{map[string]any{"a": uint64(1)}},
			},
			"comment left of an indented mapping above more keys": {
				input: "  a: 1\n# c\n  b: 2\n",
				want:  []any{map[string]any{"a": uint64(1), "b": uint64(2)}},
			},
			"comment in the column of an indented mapping": {
				input: "  a: 1\n  # c\n",
				want:  []any{map[string]any{"a": uint64(1)}},
			},
			"comment in the column of a sequence in a mapping": {
				input: "a:\n- b\n# c\n",
				want:  []any{map[string]any{"a": []any{"b"}}},
			},
			// The parser takes the comment below an anchor with no value
			// that ends its document as the value of the anchor, and
			// rejects the anchor without it.
			"anchor with no value": {
				input: "&a\n# c\n",
				want:  []any{nil},
			},
			"anchor with no value above an end marker": {
				input: "--- &a\n# c\n...\n",
				want:  []any{nil},
			},
			"anchor with no value above a header": {
				input: "&a\n# c\n---\nb\n",
				want:  []any{nil, "b"},
			},
			// In an indented root, the parser takes an anchor that directly
			// follows a "-", or a key and its ":", on its line without the
			// comment when a header or an end marker follows the comment.
			// It rejects a comment left of the first key or "-" of the
			// root.
			"anchor with no value in an indented mapping above an end marker": {
				input: "  a: &x\n# c\n...\n",
				want:  []any{map[string]any{"a": nil}},
			},
			"anchor with no value in an indented mapping above a header": {
				input: "  a: &x\n# c\n---\nb: 1\n",
				want:  []any{map[string]any{"a": nil}, map[string]any{"b": uint64(1)}},
			},
			"anchor with no value in an indented sequence above a header": {
				input: "  - &x\n# c\n---\nb: 1\n",
				want:  []any{[]any{nil}, map[string]any{"b": uint64(1)}},
			},
			"anchor with no value on a later key of an indented mapping": {
				input: "  a: 1\n  b: &x\n # c\n---\nc: 1\n",
				want: []any{
					map[string]any{"a": uint64(1), "b": nil},
					map[string]any{"c": uint64(1)},
				},
			},
			"anchor with no value in an anchored indented mapping": {
				input: "&r\n  a: &x\n# c\n...\n",
				want:  []any{map[string]any{"a": nil}},
			},
			"anchor with no value above comments left of and in the column": {
				input: "  a: &x\n# c\n  # d\n...\n",
				want:  []any{map[string]any{"a": nil}},
			},
			// The parser rejects any other anchor without the comment, so
			// the comment stays in the parser input as its value even left
			// of the first key or "-" of the root. The tree holds a null in
			// its place.
			"anchor with no value after the colon of an explicit key": {
				input: "  ? a\n  : &x\n# c\n...\n",
				want:  []any{map[string]any{"a": nil}},
			},
			"anchor with no value after the colon of an explicit key above a header": {
				input: "  ? a\n  : &x\n# c\n---\nb: 2\n",
				want:  []any{map[string]any{"a": nil}, map[string]any{"b": uint64(2)}},
			},
			"anchor with no value on a line below its key": {
				input: "  a:\n    &x\n# c\n...\n",
				want:  []any{map[string]any{"a": nil}},
			},
			"anchor with no value on a line below its entry": {
				input: "  -\n    &x\n# c\n---\nb: 2\n",
				want:  []any{[]any{nil}, map[string]any{"b": uint64(2)}},
			},
			"anchor with no value after a tag": {
				input: "  a: !!str &x\n# c\n...\n",
				want:  []any{map[string]any{"a": ""}},
			},
			"anchor with no value after the colon of a nested explicit key": {
				input: "  a:\n    ? b\n    : &x\n# c\n...\n",
				want:  []any{map[string]any{"a": map[string]any{"b": nil}}},
			},
			// With the comment, the parser would take the node below it
			// as the value of the anchor.
			"anchor with no value above a mapping key": {
				input: "a: &x\n# c\nb: 1\n",
				want:  []any{map[string]any{"a": nil, "b": uint64(1)}},
			},
			"anchor with no value above a sequence entry": {
				input: "- &x\n# c\n- 1\n",
				want:  []any{[]any{nil, uint64(1)}},
			},
			"anchor with no value in a nested mapping": {
				input: "a:\n  k: &x\n  # c\n  l: 1\nm: 2\n",
				want: []any{map[string]any{
					"a": map[string]any{"k": nil, "l": uint64(1)},
					"m": uint64(2),
				}},
			},
			"anchor with no value above a dedent": {
				input: "a:\n  b: &x\n  # c\nc: 1\n",
				want: []any{map[string]any{
					"a": map[string]any{"b": nil},
					"c": uint64(1),
				}},
			},
			"anchor with no value above blank lines and comments": {
				input: "a: &x\n\n# c\n# d\n\nb: 1\n",
				want:  []any{map[string]any{"a": nil, "b": uint64(1)}},
			},
			"anchor with no value in a mapping in a sequence": {
				input: "- a: &x\n  # c\n  b: 1\n",
				want:  []any{[]any{map[string]any{"a": nil, "b": uint64(1)}}},
			},
			"anchor with no value in a nested sequence": {
				input: "- - &x\n  # c\n  - 1\n- 2\n",
				want:  []any{[]any{[]any{nil, uint64(1)}, uint64(2)}},
			},
			// Below a comment left of its "-", the parser gives the anchor
			// a null value. Without the comment, it takes the key in the
			// column of that "-" as the value of the anchor.
			"anchor with no value in a sequence in the column of its key": {
				input: "p:\n  q:\n  - &x\n# c\n  n: 1\n",
				want: []any{map[string]any{
					"p": map[string]any{"q": []any{nil}, "n": uint64(1)},
				}},
			},
			"anchor with no value in a sequence in the column of a key of an entry": {
				input: "- p:\n  - &x\n# c\n  n: 1\n",
				want:  []any{[]any{map[string]any{"p": []any{nil}, "n": uint64(1)}}},
			},
			// The anchor still takes the indented node below the comment.
			"anchor above a comment and its nested value": {
				input: "a: &x\n# c\n  b: 1\nc: *x\n",
				want: []any{map[string]any{
					"a": map[string]any{"b": uint64(1)},
					"c": map[string]any{"b": uint64(1)},
				}},
			},
			// Below a comment left of the key or "-" that the anchor
			// directly follows on its line, the parser gives the anchor a
			// null value and rejects the value of the anchor.
			"comment left of the key above a sequence in its column": {
				input: "a:\n  b: &x\n# c\n  - 1\n",
				want:  []any{map[string]any{"a": map[string]any{"b": []any{uint64(1)}}}},
			},
			"comment left of the key above an indented value": {
				input: "a:\n  b: &x\n# c\n    c: 1\n",
				want: []any{map[string]any{
					"a": map[string]any{"b": map[string]any{"c": uint64(1)}},
				}},
			},
			"comment left of the entry above an indented value": {
				input: "- - &x\n# c\n    - 1\n",
				want:  []any{[]any{[]any{[]any{uint64(1)}}}},
			},
			"comment left of the key below a comment on its line": {
				input: "a:\n  b: &x # c\n# d\n    c: 1\n",
				want: []any{map[string]any{
					"a": map[string]any{"b": map[string]any{"c": uint64(1)}},
				}},
			},
			// The first comment on a line of its own decides for the
			// comments below it.
			"comment left of the key below a comment in its column": {
				input: "a:\n  b: &x\n  # c\n# d\n    c: 1\n",
				want: []any{map[string]any{
					"a": map[string]any{"b": map[string]any{"c": uint64(1)}},
				}},
			},
			"comment in the column of the key below a comment left of it": {
				input: "a:\n  b: &x\n# c\n    # d\n    c: 1\n",
				want: []any{map[string]any{
					"a": map[string]any{"b": map[string]any{"c": uint64(1)}},
				}},
			},
			// The parser accepts a comment left of the "?" of an explicit
			// key above the value of the anchor.
			"comment left of an explicit key above an indented value": {
				input: "  ? a\n  : &x\n# c\n    b: 2\n",
				want:  []any{map[string]any{"a": map[string]any{"b": uint64(2)}}},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				docs := niceyaml.NewSourceFromString(tc.input).Documents()

				got := make([]any, len(docs))
				for i, d := range docs {
					var err error

					got[i], err = d.Decode[any](t.Context())
					require.NoError(t, err)
				}

				assert.Equal(t, tc.want, got)
			})
		}
	})

	t.Run("anchor with no value above a comment", func(t *testing.T) {
		t.Parallel()

		// The parser takes the comment as the value of the anchor. Each
		// typed decode reads the anchored node as null, as it reads "&x ~".
		type config struct {
			A int `yaml:"a"`
		}

		tcs := map[string]struct {
			input string
			path  paths.Path
		}{
			"anchored null": {
				input: "&x ~\n",
				path:  paths.Current(),
			},
			"root": {
				input: "&x\n# c\n",
				path:  paths.Current(),
			},
			"root above a header": {
				input: "--- &x\n# c\n---\nb: 1\n",
				path:  paths.Current(),
			},
			"mapping value": {
				input: "list: &d\n# end\n",
				path:  paths.Current().Child("list"),
			},
			"mapping value after a reused anchor name": {
				input: "x: &d 1\ny: *d\nlist: &d\n# end\n",
				path:  paths.Current().Child("list"),
			},
			"sequence item": {
				input: "- &d\n# end\n",
				path:  paths.Current().Index(0),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				node := yamltest.At(t, yamltest.FirstDocument(t, tc.input), tc.path)

				cfg := &config{A: 7}
				require.NoError(t, node.DecodeInto(t.Context(), &cfg))
				assert.Nil(t, cfg)

				list, err := node.Decode[[]int](t.Context())
				require.NoError(t, err)
				assert.Nil(t, list)

				str := new("keep")
				require.NoError(t, node.DecodeInto(t.Context(), &str))
				assert.Nil(t, str)
			})
		}
	})

	t.Run("anchored field with no value above a comment", func(t *testing.T) {
		t.Parallel()

		type config struct {
			List []int `yaml:"list"`
		}

		tcs := map[string]struct {
			input string
		}{
			"anchored null": {
				input: "list: &d ~\n",
			},
			"field": {
				input: "list: &d\n# end\n",
			},
			"field after a reused anchor name": {
				input: "x: &d 1\ny: *d\nlist: &d\n# end\n",
			},
			"field above a header": {
				input: "list: &d\n# end\n---\nb: 1\n",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				got, err := yamltest.FirstDocument(t, tc.input).Decode[config](t.Context())
				require.NoError(t, err)
				assert.Equal(t, config{}, got)
			})
		}
	})

	t.Run("int-tagged integer", func(t *testing.T) {
		t.Parallel()

		// The go-yaml decoder reads an integer under a !!int tag as a Go
		// int, which it rejects for every integer type.
		type config struct {
			Version int `yaml:"version"`
		}

		tcs := map[string]struct {
			input   string
			want    int64
			wantAny any
		}{
			"integer": {
				input:   "version: !!int 16\n",
				want:    16,
				wantAny: uint64(16),
			},
			"hex integer": {
				input:   "version: !!int 0x10\n",
				want:    16,
				wantAny: uint64(16),
			},
			"negative integer": {
				input:   "version: !!int -5\n",
				want:    -5,
				wantAny: int64(-5),
			},
			"anchor on the tag": {
				input:   "version: &v !!int 0x10\n",
				want:    16,
				wantAny: uint64(16),
			},
			"tag on an anchor": {
				input:   "version: !!int &v 0x10\n",
				want:    16,
				wantAny: uint64(16),
			},
			"alias to a tagged anchor": {
				input:   "base: &k !!int 16\nversion: *k\n",
				want:    16,
				wantAny: uint64(16),
			},
			"after a reused anchor name": {
				input:   "a: &x 1\nb: &x 2\nversion: !!int 16\n",
				want:    16,
				wantAny: uint64(16),
			},
			"alias to a tagged anchor of a reused name": {
				input:   "a: &x 1\nb: &x !!int 16\nversion: *x\n",
				want:    16,
				wantAny: uint64(16),
			},
			"after nested collections": {
				input:   "spec:\n  name: x\n  ports: [80, {n: !!str 1}]\nversion: !!int 0x10\n",
				want:    16,
				wantAny: uint64(16),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				got, err := dd.Decode[config](t.Context())
				require.NoError(t, err)
				assert.Equal(t, config{Version: int(tc.want)}, got)

				node := yamltest.At(t, dd, paths.Current().Child("version"))

				gotInt, err := node.Decode[int64](t.Context())
				require.NoError(t, err)
				assert.Equal(t, tc.want, gotInt)

				gotAny, err := node.Decode[any](t.Context())
				require.NoError(t, err)
				assert.Equal(t, tc.wantAny, gotAny)
			})
		}
	})

	t.Run("int-tagged integer in the text of an UnmarshalYAML method", func(t *testing.T) {
		t.Parallel()

		// The text spells the tag, and the space before it, as the
		// document does, so it parses to the same mapping again.
		type wrapper struct {
			Spec rawText `yaml:"spec"`
		}

		tcs := map[string]struct {
			input string
			want  string
		}{
			"tag": {
				input: "spec:\n  version: !!int 16\n",
				want:  "version: !!int 16\n",
			},
			"tag on an anchor": {
				input: "spec:\n  version: !!int &x 16\n",
				want:  "version: !!int &x 16\n",
			},
			"anchors on both sides of the tag": {
				input: "spec:\n  version: &x !!int &y 16\n",
				want:  "version: &x !!int &y 16\n",
			},
			"after a reused anchor name": {
				input: "a: &x 1\nb: &x 2\nspec:\n  version: !!int 16\n",
				want:  "version: !!int 16\n",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				got, err := dd.Decode[wrapper](t.Context())
				require.NoError(t, err)
				assert.Equal(t, tc.want, got.Spec.text)

				var spec map[string]any

				require.NoError(t, yaml.Unmarshal([]byte(got.Spec.text), &spec))
				assert.Equal(t, map[string]any{"version": 16}, spec)

				whole, err := dd.Decode[rawText](t.Context())
				require.NoError(t, err)
				assert.Equal(t, tc.input, whole.text)
			})
		}
	})

	t.Run("comment on a line of its own inside a flow collection", func(t *testing.T) {
		t.Parallel()

		// The go-yaml parser rejects such a comment before a key of a flow
		// mapping, and before a ",", ":", "]", or "}", when it keeps
		// comments. It reads one before any other node as the head comment
		// of that node, so the tree keeps it. Each case lists the value of
		// every document and the comment the tree keeps, if any.
		tcs := map[string]struct {
			input string
			kept  string
			want  []any
		}{
			"comment at the start of a flow mapping": {
				input: "config: {\n  # production\n  name: prod\n}\n",
				want:  []any{map[string]any{"config": map[string]any{"name": "prod"}}},
			},
			"comment between flow mapping entries": {
				input: "a: {\n  b: 1,\n  # c\n  c: 2\n}\n",
				want:  []any{map[string]any{"a": map[string]any{"b": uint64(1), "c": uint64(2)}}},
			},
			"comment before a comma in a flow mapping": {
				input: "a: {\n  b: 1\n  # c\n  , c: 2\n}\n",
				want:  []any{map[string]any{"a": map[string]any{"b": uint64(1), "c": uint64(2)}}},
			},
			"comments before a closing brace": {
				input: "a: {\n  b: 1\n  # c\n  # d\n}\n",
				want:  []any{map[string]any{"a": map[string]any{"b": uint64(1)}}},
			},
			"comment after a trailing comma in a flow mapping": {
				input: "a: {\n  b: 1,\n  # c\n}\n",
				want:  []any{map[string]any{"a": map[string]any{"b": uint64(1)}}},
			},
			"comment in an empty flow mapping": {
				input: "a: {\n  # c\n}\n",
				want:  []any{map[string]any{"a": map[string]any{}}},
			},
			"comment between an explicit key and its colon": {
				input: "a: {\n  ? b\n  # c\n  : 1\n}\n",
				want:  []any{map[string]any{"a": map[string]any{"b": uint64(1)}}},
			},
			"comment after an explicit key indicator in a flow mapping": {
				input: "a: {? \n  # c\n  b : 1, c: d}\n",
				want:  []any{map[string]any{"a": map[string]any{"b": uint64(1), "c": "d"}}},
			},
			"comment after an explicit key indicator in a flow sequence": {
				input: "a: [? \n  # c\n  b : 1, c]\n",
				want:  []any{map[string]any{"a": []any{map[string]any{"b": uint64(1)}, "c"}}},
			},
			"comments after an explicit key indicator in a root flow sequence": {
				input: "[? \n  # c\n  # d\n  b : 1, c]\n",
				want:  []any{[]any{map[string]any{"b": uint64(1)}, "c"}},
			},
			"comment before a closing bracket": {
				input: "a: [\n  1\n  # c\n]\n",
				want:  []any{map[string]any{"a": []any{uint64(1)}}},
			},
			"comment before a comma in a flow sequence": {
				input: "a: [\n  1\n  # c\n  , 2\n]\n",
				want:  []any{map[string]any{"a": []any{uint64(1), uint64(2)}}},
			},
			"comment after a trailing comma in a flow sequence": {
				input: "a: [\n  1,\n  # c\n]\n",
				want:  []any{map[string]any{"a": []any{uint64(1)}}},
			},
			"comment in an empty flow sequence": {
				input: "a: [\n  # c\n]\n",
				want:  []any{map[string]any{"a": []any{}}},
			},
			"comment below a comment on the line of an entry": {
				input: "a: [\n  1 # c\n  # d\n]\n",
				kept:  "# c",
				want:  []any{map[string]any{"a": []any{uint64(1)}}},
			},
			// The parser reads a comment on the last line of a scalar on
			// several lines as one on a line of its own.
			"comment on the last line of an entry before a closing bracket": {
				input: "[a\n b # c\n]\n",
				want:  []any{[]any{"a b"}},
			},
			"comment on the last line of an entry before a comma": {
				input: "[a\n b # c\n, d]\n",
				want:  []any{[]any{"a b", "d"}},
			},
			"comment on the last line of a value before a closing brace": {
				input: "{a: \"x\n y\" # c\n}\n",
				want:  []any{map[string]any{"a": "x y"}},
			},
			"flow mapping in a flow sequence": {
				input: "a: [1, {\n  # c\n  b: 2\n}]\n",
				want:  []any{map[string]any{"a": []any{uint64(1), map[string]any{"b": uint64(2)}}}},
			},
			"flow mapping at the root": {
				input: "{\n  # c\n  a: 1\n}\n",
				want:  []any{map[string]any{"a": uint64(1)}},
			},
			"crlf line endings": {
				input: "config: {\r\n  # production\r\n  name: prod\r\n}\r\n",
				want:  []any{map[string]any{"config": map[string]any{"name": "prod"}}},
			},
			"second document": {
				input: "a: {\n  # c\n  b: 1\n}\n---\nx: [\n  1\n  # d\n]\n",
				want: []any{
					map[string]any{"a": map[string]any{"b": uint64(1)}},
					map[string]any{"x": []any{uint64(1)}},
				},
			},
			"comment after an opening bracket": {
				input: "a: [\n  # c\n  1\n]\n",
				kept:  "# c",
				want:  []any{map[string]any{"a": []any{uint64(1)}}},
			},
			"comment between flow sequence entries": {
				input: "a: [\n  1,\n  # c\n  2\n]\n",
				kept:  "# c",
				want:  []any{map[string]any{"a": []any{uint64(1), uint64(2)}}},
			},
			"comment between a colon and its value": {
				input: "a: {\n  b:\n  # c\n  1\n}\n",
				kept:  "# c",
				want:  []any{map[string]any{"a": map[string]any{"b": uint64(1)}}},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				src := niceyaml.NewSourceFromString(tc.input)

				file, err := src.File()
				require.NoError(t, err)

				if tc.kept != "" {
					assert.Contains(t, file.String(), tc.kept)
				}

				docs := src.Documents()

				got := make([]any, len(docs))
				for i, d := range docs {
					got[i], err = d.Decode[any](t.Context())
					require.NoError(t, err)
				}

				assert.Equal(t, tc.want, got)
			})
		}
	})

	t.Run("comment on a line of its own around an explicit key", func(t *testing.T) {
		t.Parallel()

		// When it keeps comments, the go-yaml parser takes such a comment
		// after a "?" or before a ":" as a key, and rejects or misreads the
		// mapping. Each case lists the value of every document and the
		// comment the tree keeps, if any.
		tcs := map[string]struct {
			input string
			kept  string
			want  []any
		}{
			"comment between a key and its colon": {
				input: "? a\n# c\n: 1\n",
				want:  []any{map[string]any{"a": uint64(1)}},
			},
			"indented comment between a key and its colon": {
				input: "? a\n  # c\n: 1\n",
				want:  []any{map[string]any{"a": uint64(1)}},
			},
			"comments with a blank line between them": {
				input: "? a\n# c\n\n# d\n: 1\n",
				want:  []any{map[string]any{"a": uint64(1)}},
			},
			"comment before the colon of a later key": {
				input: "? a\n: 1\n? b\n# c\n: 2\n",
				want:  []any{map[string]any{"a": uint64(1), "b": uint64(2)}},
			},
			"comment in a nested mapping": {
				input: "x:\n  ? a\n  # c\n  : 1\n",
				want:  []any{map[string]any{"x": map[string]any{"a": uint64(1)}}},
			},
			"comment in a mapping in a sequence": {
				input: "- ? a\n  # c\n  : 1\n",
				want:  []any{[]any{map[string]any{"a": uint64(1)}}},
			},
			"comment below a block scalar key": {
				input: "? |\n  lit\n# c\n: 1\n",
				want:  []any{map[string]any{"lit\n": uint64(1)}},
			},
			"comment above a block sequence value": {
				input: "? a\n# c\n: - 1\n",
				want:  []any{map[string]any{"a": []any{uint64(1)}}},
			},
			"comment between an indicator and its key": {
				input: "? \n  # c\n  a\n: 1\n",
				want:  []any{map[string]any{"a": uint64(1)}},
			},
			"comment on the line of the key": {
				input: "? a # k\n# c\n: 1\n",
				kept:  "# k",
				want:  []any{map[string]any{"a": uint64(1)}},
			},
			// The parser reads a comment on the last line of a key on
			// several lines as one on a line of its own.
			"comment on the last line of a quoted key": {
				input: "? \"a\n b\" # c\n: 1\n",
				want:  []any{map[string]any{"a b": uint64(1)}},
			},
			"comment between entries": {
				input: "? a\n: 1\n# c\n? b\n: 2\n",
				kept:  "# c",
				want:  []any{map[string]any{"a": uint64(1), "b": uint64(2)}},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				src := niceyaml.NewSourceFromString(tc.input)

				file, err := src.File()
				require.NoError(t, err)

				if tc.kept != "" {
					assert.Contains(t, file.String(), tc.kept)
				}

				docs := src.Documents()

				got := make([]any, len(docs))
				for i, d := range docs {
					got[i], err = d.Decode[any](t.Context())
					require.NoError(t, err)
				}

				assert.Equal(t, tc.want, got)
			})
		}
	})
}

func TestDocument_Decode_TypeMismatch(t *testing.T) {
	t.Parallel()

	t.Run("string to int", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "value: not_a_number")

		_, err := dd.Decode[struct{ Value int }](t.Context())

		require.Error(t, err)

		var yamlErr *niceyaml.Error

		require.ErrorAs(t, err, &yamlErr)
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
		dd := yamltest.FirstDocument(t, input)

		var called bool

		result, err := dd.Decode[plainConfig](t.Context(), niceyaml.WithValidator(nameSchema(&called)))
		require.NoError(t, err)
		assert.Equal(t, "test", result.Name)
		assert.Equal(t, 42, result.Value)
		assert.True(t, called, "the validator should have been called")
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
		dd := yamltest.FirstDocument(t, input)

		_, err := dd.Decode[plainConfig](t.Context(), niceyaml.WithValidator(nameSchema(nil)))
		require.ErrorIs(t, err, errSchemaValidationFailed)
	})

	t.Run("decodes without a schema", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			name: test
			value: 42
		`)
		dd := yamltest.FirstDocument(t, input)

		result, err := dd.Decode[plainConfig](t.Context())
		require.NoError(t, err)
		assert.Equal(t, "test", result.Name)
		assert.Equal(t, 42, result.Value)
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
		"comment on the line of an end marker": {
			input: "a: 1\n... # e\nk: v\n",
			want: []doc{
				{content: "a: 1\n... # e\n"},
				{content: "k: v\n"},
			},
		},
		"comment on the line of an end marker above a header": {
			input: "a: 1\n... # e\n---\nk: v\n",
			want: []doc{
				{content: "a: 1\n... # e\n"},
				{preamble: "---\n", content: "k: v\n"},
			},
		},
		"comment between documents": {
			input: "a: 1\n...\n# note\n---\nb: 2\n",
			want: []doc{
				{content: "a: 1\n...\n"},
				{preamble: "# note\n---\n", content: "b: 2\n"},
			},
		},
		"directive below an empty document": {
			input: "---\n...\n# note\n%YAML 1.2\n---\na: 1\n",
			want: []doc{
				{preamble: "---\n...\n"},
				{preamble: "# note\n%YAML 1.2\n---\n", content: "a: 1\n"},
			},
		},
		"version directive on each side of an empty document": {
			input: "%YAML 1.2\n---\n...\n%YAML 1.2\n---\na: 1\n",
			want: []doc{
				{preamble: "%YAML 1.2\n---\n...\n"},
				{preamble: "%YAML 1.2\n---\n", content: "a: 1\n"},
			},
		},
		"tag directive below an empty document": {
			input: "%YAML 1.2\n---\n...\n%TAG ! tag:x,2000:\n---\n!a b\n",
			want: []doc{
				{preamble: "%YAML 1.2\n---\n...\n"},
				{preamble: "%TAG ! tag:x,2000:\n---\n", content: "!a b\n"},
			},
		},
		"comments on the markers of an empty document": {
			input: "--- # c\n... # d\n# e\n%YAML 1.2\n--- # f\na: 1\n",
			want: []doc{
				{preamble: "--- # c\n... # d\n"},
				{preamble: "# e\n%YAML 1.2\n--- # f\n", content: "a: 1\n"},
			},
		},
		"document without a header below an empty document": {
			input: "---\n...\n# note\nb: 1\n",
			want: []doc{
				{preamble: "---\n...\n"},
				{preamble: "# note\n", content: "b: 1\n"},
			},
		},
		"scalar document below an end marker": {
			input: "a: 1\n...\nb\n",
			want: []doc{
				{content: "a: 1\n...\n"},
				{content: "b\n"},
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
		"comment after a block scalar that opens with a blank line": {
			input: "a: |\n\n  x\n\n# note\n---\nb: 2\n",
			want: []doc{
				{content: "a: |\n\n  x\n\n"},
				{preamble: "# note\n---\n", content: "b: 2\n"},
			},
		},
		"comment after an empty block scalar": {
			input: "a: |\n# note\n---\nb: 2\n",
			want: []doc{
				{content: "a: |\n"},
				{preamble: "# note\n---\n", content: "b: 2\n"},
			},
		},
		"comment after a value on the next line": {
			input: "a:\n  b\n# note\n---\nc: 1\n",
			want: []doc{
				{content: "a:\n  b\n"},
				{preamble: "# note\n---\n", content: "c: 1\n"},
			},
		},
		"comment after a quoted value on the next line": {
			input: "a:\n  'q'\n# note\n---\nc: 1\n",
			want: []doc{
				{content: "a:\n  'q'\n"},
				{preamble: "# note\n---\n", content: "c: 1\n"},
			},
		},
		"comment after a sequence entry on the next line": {
			input: "-\n  y\n# note\n---\nc: 1\n",
			want: []doc{
				{content: "-\n  y\n"},
				{preamble: "# note\n---\n", content: "c: 1\n"},
			},
		},
		"comment after a value below blank lines": {
			input: "a:\n\n\n  x\n# note\n---\nc: 1\n",
			want: []doc{
				{content: "a:\n\n\n  x\n"},
				{preamble: "# note\n---\n", content: "c: 1\n"},
			},
		},
		"comment after a value on the next line with crlf": {
			input: "a:\r\n  b\r\n# note\r\n---\r\nb: 2\r\n",
			want: []doc{
				{content: "a:\r\n  b\r\n"},
				{preamble: "# note\r\n---\r\n", content: "b: 2\r\n"},
			},
		},
		"indented comment after a value": {
			input: "a: 1\n  # note\n---\nb: 2\n",
			want: []doc{
				{content: "a: 1\n"},
				{preamble: "# note\n---\n", content: "b: 2\n"},
			},
		},
		"indented comment after a nested value": {
			input: "a:\n  b: 1\n  # note\n---\nc: 1\n",
			want: []doc{
				{content: "a:\n  b: 1\n"},
				{preamble: "# note\n---\n", content: "c: 1\n"},
			},
		},
		"indented comment after a nested block scalar": {
			input: "a:\n  b: |\n    x\n  # note\n---\nc: 1\n",
			want: []doc{
				{content: "a:\n  b: |\n    x\n"},
				{preamble: "# note\n---\n", content: "c: 1\n"},
			},
		},
		"indented comment after a nested value with crlf": {
			input: "a:\r\n  b: 1\r\n  # note\r\n---\r\nc: 1\r\n",
			want: []doc{
				{content: "a:\r\n  b: 1\r\n"},
				{preamble: "# note\r\n---\r\n", content: "c: 1\r\n"},
			},
		},
		"comment on the line of a value on the next line": {
			input: "a:\n  b # same\n---\nc: 1\n",
			want: []doc{
				{content: "a:\n  b # same\n"},
				{preamble: "---\n", content: "c: 1\n"},
			},
		},
		"comment on the line of a no-break space value": {
			input: "a:\n  \u00a0 # same\n---\nc: 1\n",
			want: []doc{
				{content: "a:\n  \u00a0 # same\n"},
				{preamble: "---\n", content: "c: 1\n"},
			},
		},
		"comment on the last line of a value that opens with a no-break space": {
			input: "a:\n  \u00a0\n  x # same\n---\nc: 1\n",
			want: []doc{
				{content: "a:\n  \u00a0\n  x # same\n"},
				{preamble: "---\n", content: "c: 1\n"},
			},
		},
		"comment on the last line of a value that opens with an ideographic space": {
			input: "a: \u3000\n  x # same\n---\nc: 1\n",
			want: []doc{
				{content: "a: \u3000\n  x # same\n"},
				{preamble: "---\n", content: "c: 1\n"},
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
		"comment after a scalar": {
			input: "x\n# note\n",
			want:  []doc{{content: "x\n# note\n"}},
		},
		"comment after a flow sequence": {
			input: "[1, 2]\n# note\n",
			want:  []doc{{content: "[1, 2]\n# note\n"}},
		},
		"comment after a flow sequence above a later header": {
			input: "[1, 2]\n# note\n---\n[3]\n",
			want: []doc{
				{content: "[1, 2]\n"},
				{preamble: "# note\n---\n", content: "[3]\n"},
			},
		},
		"comment left of an indented mapping": {
			input: "---\n  a: 1\n# note\n",
			want:  []doc{{preamble: "---\n", content: "a: 1\n# note\n"}},
		},
		"comment left of an indented mapping above a later header": {
			input: "  a: 1\n# note\n---\nb: 2\n",
			want: []doc{
				{content: "a: 1\n"},
				{preamble: "# note\n---\n", content: "b: 2\n"},
			},
		},
		"comment between a directive and its header": {
			input: "%YAML 1.2\n# note\n---\na: 1\n",
			want:  []doc{{preamble: "%YAML 1.2\n# note\n---\n", content: "a: 1\n"}},
		},
		"comment inside a flow mapping": {
			input: "{\n  # note\n  a: 1\n}\n",
			want:  []doc{{content: "{\n  # note\n  a: 1\n}\n"}},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input)

			_, err := source.File()
			require.NoError(t, err)

			docs := source.AllDocuments()
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
		"comment on the line of an end marker": {
			input: "a: 1\n... # e\nk: v\n",
			want: []position.Span{
				position.NewSpan(0, 2),
				position.NewSpan(2, 3),
			},
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
		"directive below an empty document": {
			input: "---\n...\n# note\n%YAML 1.2\n---\na: 1\n",
			want: []position.Span{
				position.NewSpan(0, 2),
				position.NewSpan(2, 6),
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

			_, err := source.File()
			require.NoError(t, err)

			docs := source.AllDocuments()
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

		docs := source.Documents()
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

		docs := source.Documents()
		require.Len(t, docs, 2)

		view := docs[1].View()

		assert.Equal(t, source.View().Slice(docs[1].Span()).String(), view.String())
		assert.Equal(t, docs[1].Span().Len(), view.Count())
		assert.Equal(t, 2, view.Lines().Line(docs[1].Span().Start).Number())
		assert.False(t, view.Contains(0), "the view keeps the indices of the source")
	})

	t.Run("covers only the lines of a scoped Node", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(input)

		docs := source.Documents()
		require.Len(t, docs, 2)

		hours := yamltest.At(t, docs[1], paths.Current().Child("spec", "hours"))

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

		docs := source.Documents()
		require.Len(t, docs, 2)

		var bound *niceyaml.SourceError

		require.ErrorAs(
			t,
			docs[1].Bind(niceyaml.NewError("closed", niceyaml.AtPath(paths.Current().Child("b")))),
			&bound,
		)

		view := docs[1].View()
		require.True(t, bound.Annotate(view))

		assert.Contains(t, view.String(), "   7 | b: 2\n     |    ^")
	})

	t.Run("an error at the root of a scoped block mapping lies above its view", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(input)

		docs := source.Documents()
		require.Len(t, docs, 2)

		hours := yamltest.At(t, docs[1], paths.Current().Child("spec", "hours"))

		var bound *niceyaml.SourceError

		require.ErrorAs(t, hours.Bind(errors.New("closed")), &bound)
		require.EqualError(t, bound, "4:3: $.spec.hours: closed")

		// The error points at the key, on the line above the lines the
		// mapping covers.
		pos, ok := bound.Position()
		require.True(t, ok)
		assert.Equal(t, position.New(3, 2), pos)
		assert.Equal(t, position.NewSpan(4, 6), hours.Span())

		view := hours.View()
		require.False(t, bound.Annotate(view))
		assert.Equal(t, hours.View().String(), view.String())

		// A view from the line of the error holds the mark.
		view = source.View().Slice(position.NewSpan(pos.Line, hours.Span().End))
		require.True(t, bound.Annotate(view))
		assert.Equal(t, stringtest.JoinLF(
			"   4 |   hours:",
			"     |   ^^^^^ closed",
			`   5 |     open: "09:00"`,
			`   6 |     close: "17:00"`,
		), view.String())
	})

	t.Run("an error at the root of a scoped element lies in its view", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, "items:\n  - name: a\n    port: 1\n")
		item := yamltest.At(t, doc, paths.Current().Child("items").Index(0))

		var bound *niceyaml.SourceError

		require.ErrorAs(t, item.Bind(errors.New("bad item")), &bound)
		require.EqualError(t, bound, "2:3: $.items[0]: bad item")

		view := item.View()
		require.True(t, bound.Annotate(view))
		assert.Equal(t, stringtest.JoinLF(
			"   2 |   - name: a",
			"     |   ^ bad item",
			"   3 |     port: 1",
		), view.String())
	})
}

func TestDocument_View_Lines(t *testing.T) {
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

	t.Run("holds the lines of the document at their indices in the file", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(input)

		docs := source.Documents()
		require.Len(t, docs, 2)

		view := docs[1].View()
		span := docs[1].Span()

		require.Equal(t, span.Len(), view.Count())

		want := span.Start

		for i, l := range view.All() {
			assert.Equal(t, want, i)
			assert.Same(t, source.Lines().Line(i), l)

			want++
		}

		// The held lines count from zero and keep the numbers of the file.
		held := view.Held()

		require.Equal(t, span.Len(), held.Len())
		assert.Same(t, source.Lines().Line(span.Start), held.Line(0))
		assert.Equal(t, 2, held.Line(0).Number())
	})

	t.Run("holds only the lines of a scoped Node", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(input)

		docs := source.Documents()

		hours := yamltest.At(t, docs[1], paths.Current().Child("spec", "hours"))
		lines := hours.View().Held()

		require.Equal(t, 2, lines.Len())
		assert.Equal(t, `    open: "09:00"`, lines.Line(0).Content())
		assert.Equal(t, 5, lines.Line(0).Number())
	})

	t.Run("diffs one document of a file that holds several", func(t *testing.T) {
		t.Parallel()

		documents := func(t *testing.T, input string) []*niceyaml.Node {
			t.Helper()

			docs := niceyaml.NewSourceFromString(input).Documents()
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

		result := diff.Diff(before[1].View(), after[1].View())

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
	d := source.Documents()
	require.Len(t, d, 1)

	path := paths.Current().Child("key")

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

		assert.Same(t, dd.DocumentAST().Body, dd.AST())
	})

	t.Run("scope is the node the path selects", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		node := yamltest.At(t, dd, paths.Current().Child("meta")).AST()
		assert.Equal(t, "  name: app", node.String())

		want, err := paths.NewResolver(dd.DocumentAST()).Node(paths.Current().Child("meta"))
		require.NoError(t, err)
		assert.Same(t, want, node)
	})

	t.Run("nested scopes join", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		node := yamltest.At(t, yamltest.At(t, dd, paths.Current().Child("meta")), paths.Current().Child("name")).AST()
		assert.Equal(t, "app", node.String())
	})

	t.Run("scope that selects nothing binds the error", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		scoped, err := dd.At(paths.Current().Child("missing"))
		require.ErrorIs(t, err, paths.ErrNotFound)
		assert.Nil(t, scoped)

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Same(t, dd.Source(), bound.Source())
	})

	t.Run("file of whitespace decodes to nothing", func(t *testing.T) {
		t.Parallel()

		// The lexer emits nothing for the text, so the document holds no
		// value, and the decode leaves its target as it is rather than
		// reading the placeholder token as a string.
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

		assert.Nil(t, dd.AST())
	})

	t.Run("document of comments has its comment group", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "# only a comment\n")

		assert.IsType(t, &ast.CommentGroupNode{}, dd.AST())
	})

	t.Run("file of whitespace has the placeholder scalar", func(t *testing.T) {
		t.Parallel()

		for _, input := range []string{"\n", "  \n", "\n\n", "!"} {
			dd := yamltest.FirstDocument(t, input)

			scalar, ok := dd.AST().(*ast.StringNode)
			require.True(t, ok, "%q: %T", input, dd.AST())
			assert.True(t, tokens.IsPlaceholder(scalar.Token), "%q", input)
		}
	})

	t.Run("document of whitespace below a header has no body", func(t *testing.T) {
		t.Parallel()

		assert.Nil(t, yamltest.FirstDocument(t, "---\n  \n").AST())

		docs := niceyaml.NewSourceFromString("a: 1\n---\n  \n").AllDocuments()
		require.Len(t, docs, 2)
		assert.Nil(t, docs[1].AST())
	})

	t.Run("a scoped node reaches the whole document", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)
		meta := yamltest.At(t, dd, paths.Current().Child("meta"))

		assert.Same(t, dd, meta.Document())
		assert.Same(t, dd.DocumentAST(), meta.Document().DocumentAST())
		assert.Same(t, dd.Source(), meta.Source())
		assert.Equal(t, paths.Doc().Child("meta"), meta.Path())
		assert.True(t, dd.Path().IsRoot())
		assert.Equal(t, paths.Doc(), yamltest.At(t, dd, paths.Doc()).Path())
		assert.Same(t, dd, dd.Document())

		var nothing *niceyaml.Node

		assert.Nil(t, nothing.Document())
	})
}

func TestNode_Resolver(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		base: &base
		  name: app
		items:
		  - *base
		  - name: other
		---
		second: 1
	`)

	documents := func(t *testing.T) []*niceyaml.Node {
		t.Helper()

		docs := niceyaml.NewSourceFromString(input).Documents()
		require.Len(t, docs, 2)

		return docs
	}

	t.Run("every node of a document shares one resolver", func(t *testing.T) {
		t.Parallel()

		docs := documents(t)
		item := yamltest.At(t, docs[0], paths.Current().Child("items").Index(1))

		assert.Same(t, docs[0].Resolver(), item.Resolver())
		assert.Same(t, docs[0].Resolver(), item.Document().Resolver())
		assert.NotSame(t, docs[0].Resolver(), docs[1].Resolver())
	})

	t.Run("paths resolve from the document root", func(t *testing.T) {
		t.Parallel()

		item := yamltest.At(t, documents(t)[0], paths.Current().Child("items").Index(1))

		got, err := item.Resolver().Node(item.Path())
		require.NoError(t, err)
		assert.Same(t, item.AST(), got)
	})

	t.Run("an alias in a scoped node reaches its anchor", func(t *testing.T) {
		t.Parallel()

		docs := documents(t)
		item := yamltest.At(t, docs[0], paths.Current().Child("items").Index(0))

		got, err := item.Resolver().Deref(item.AST())
		require.NoError(t, err)

		want, err := paths.NewResolver(docs[0].DocumentAST()).Node(paths.Current().Child("base"))
		require.NoError(t, err)
		assert.Same(t, want, got)
		assert.Equal(t, "  name: app", got.String())
	})

	t.Run("a document that did not parse resolves no path", func(t *testing.T) {
		t.Parallel()

		// A path that lists nothing in an empty document is an error
		// here, with or without a header above the document.
		for name, input := range map[string]string{
			"no header": "items: [\n",
			"header":    "---\nitems: [\n",
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				docs := niceyaml.NewSourceFromString(input).AllDocuments()
				require.Len(t, docs, 1)
				require.ErrorIs(t, docs[0].Err(), niceyaml.ErrSyntax)

				resolver := docs[0].Resolver()

				for _, path := range []paths.Path{
					paths.Doc(),
					paths.Doc().Child("items"),
					paths.Doc().Child("items").IndexAll(),
				} {
					matches, err := resolver.Matches(path)
					require.ErrorIs(t, err, paths.ErrNoDocument, path)
					assert.Empty(t, matches, path)
				}

				_, err := resolver.Node(paths.Doc())
				require.ErrorIs(t, err, paths.ErrNoDocument)

				_, err = resolver.Token(paths.Doc().Child("items"))
				require.ErrorIs(t, err, paths.ErrNoDocument)
			})
		}
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

	hoursPath := paths.Doc().Child("spec", "hours")

	t.Run("path in a check error resolves from the scope", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)
		hours := yamltest.At(t, dd, hoursPath)

		h, err := hours.Decode[checkHours](t.Context())
		require.NoError(t, err)
		assert.Equal(t, "09:00", h.Open)

		check := func(_ *checkHours) error {
			return niceyaml.NewError("closes too early", niceyaml.AtPath(paths.Current().Child("close")))
		}

		err = hours.Bind(check(&h))

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Same(t, dd.Source(), bound.Source())

		rng, ok := bound.Range()
		require.True(t, ok)
		assert.Equal(t, position.NewRange(position.New(4, 11), position.New(4, 18)), rng)
		assert.Equal(t, "5:12: $.spec.hours.close: closes too early", err.Error())
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

func TestNode_ErrorConstructors(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString(stringtest.Input(`
		close: top
		shops:
		  - hours:
		      open: "17:00"
		      close: "09:00"
	`), niceyaml.WithName("cfg.yaml"))

	doc, err := source.Document()
	require.NoError(t, err)

	hours := yamltest.At(t, doc, paths.Current().Child("shops").Index(0).Child("hours"))
	closePath := paths.Current().Child("close")
	errStat := errors.New("stat license: permission denied")

	tcs := map[string]struct {
		// The error the method of the Node returns, and the error Bind
		// returns for the package function with the same arguments.
		got     error
		bound   error
		want    string
		invalid bool
	}{
		"NewError binds at the scope": {
			got:     hours.Bind(niceyaml.NewError("bad hours")),
			bound:   hours.Bind(niceyaml.NewError("bad hours")),
			want:    "cfg.yaml:3:5: $.shops[0].hours: bad hours",
			invalid: true,
		},
		"NewError resolves a path from the scope": {
			got:     hours.Bind(niceyaml.NewError("bad", niceyaml.AtPath(closePath))),
			bound:   hours.Bind(niceyaml.NewError("bad", niceyaml.AtPath(closePath))),
			want:    "cfg.yaml:5:14: $.shops[0].hours.close: bad",
			invalid: true,
		},
		"NewError through the root gains no location": {
			got:     doc.Bind(niceyaml.NewError("bad")),
			bound:   doc.Bind(niceyaml.NewError("bad")),
			want:    "cfg.yaml: bad",
			invalid: true,
		},
		"Invalid declares the document at fault": {
			got:     hours.Bind(niceyaml.Invalid(errStat, niceyaml.AtPath(closePath))),
			bound:   hours.Bind(niceyaml.Invalid(errStat, niceyaml.AtPath(closePath))),
			want:    "cfg.yaml:5:14: $.shops[0].hours.close: stat license: permission denied",
			invalid: true,
		},
		"Invalid with no options binds at the scope": {
			got:     hours.Bind(niceyaml.Invalid(errStat)),
			bound:   hours.Bind(niceyaml.Invalid(errStat)),
			want:    "cfg.yaml:3:5: $.shops[0].hours: stat license: permission denied",
			invalid: true,
		},
		"Place declares no fault": {
			got:   hours.Bind(niceyaml.Place(errStat, niceyaml.AtPath(closePath))),
			bound: hours.Bind(niceyaml.Place(errStat, niceyaml.AtPath(closePath))),
			want:  "cfg.yaml:5:14: $.shops[0].hours.close: stat license: permission denied",
		},
		"Place keeps the fault of the error it places": {
			got:     hours.Bind(niceyaml.Place(niceyaml.NewError("bad"), niceyaml.AtPath(closePath))),
			bound:   hours.Bind(niceyaml.Place(niceyaml.NewError("bad"), niceyaml.AtPath(closePath))),
			want:    "cfg.yaml:5:14: $.shops[0].hours.close: bad",
			invalid: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			require.EqualError(t, tc.got, tc.want)
			require.EqualError(t, tc.bound, tc.want)
			assert.Equal(t, tc.invalid, niceyaml.IsInvalid(tc.got))
			assert.Equal(t, tc.invalid, niceyaml.IsInvalid(tc.bound))
		})
	}

	t.Run("a nil error stays nil", func(t *testing.T) {
		t.Parallel()

		var typed *niceyaml.Error

		require.NoError(t, hours.Bind(niceyaml.Invalid(nil, niceyaml.AtPath(closePath))))
		require.NoError(t, hours.Bind(niceyaml.Place(nil, niceyaml.AtPath(closePath))))
		require.NoError(t, hours.Bind(niceyaml.Invalid(typed)))
		require.NoError(t, hours.Bind(niceyaml.Place(typed)))
	})
}

func TestNode_Bind_Scope(t *testing.T) {
	t.Parallel()

	// The root holds a key close of its own, so a path the binding left
	// as the check wrote it would name that key.
	source := niceyaml.NewSourceFromString(stringtest.Input(`
		close: top
		shops:
		  - hours:
		      open: "17:00"
		      close: "09:00"
	`), niceyaml.WithName("cfg.yaml"))

	doc, err := source.Document()
	require.NoError(t, err)

	hoursPath := paths.Current().Child("shops").Index(0).Child("hours")
	hours := yamltest.At(t, doc, hoursPath)

	openPath := paths.Current().Child("open")
	closePath := paths.Current().Child("close")

	tcs := map[string]struct {
		err error
		// The message of the binding, its path, when it carries one, the
		// message of each problem it heads, and of each of its details.
		want     string
		path     string
		children []string
		details  []string
	}{
		"a path joins the scope": {
			err:  niceyaml.NewError("bad", niceyaml.AtPath(closePath)),
			want: "cfg.yaml:5:14: $.shops[0].hours.close: bad",
			path: "$.shops[0].hours.close",
		},
		"the root path names the scope": {
			err:  niceyaml.NewError("bad", niceyaml.AtPath(paths.Current())),
			want: "cfg.yaml:3:5: $.shops[0].hours: bad",
			path: "$.shops[0].hours",
		},
		"a key path joins the scope": {
			err:  niceyaml.NewError("bad", niceyaml.AtPath(closePath.Key())),
			want: "cfg.yaml:5:7: $.shops[0].hours.close~: bad",
			path: "$.shops[0].hours.close~",
		},
		"a key the scope leaves out binds at the key of the scope": {
			err:  niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("nope"))),
			want: "cfg.yaml:3:5: $.shops[0].hours.nope: bad",
			path: "$.shops[0].hours.nope",
		},
		"a path that does not resolve joins the scope": {
			err:  niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("open", "nope"))),
			want: "cfg.yaml: $.shops[0].hours.open.nope: bad",
			path: "$.shops[0].hours.open.nope",
		},
		"a path beside a range joins the scope and binds at the range": {
			err: niceyaml.NewError("bad",
				niceyaml.AtPath(closePath),
				niceyaml.AtRange(position.NewRange(position.New(4, 6), position.New(4, 11))),
			),
			want: "cfg.yaml:5:7: $.shops[0].hours.close: bad",
			path: "$.shops[0].hours.close",
		},
		"a rebased error joins the scope in front of its base": {
			err:  niceyaml.Rebase(errors.New("bad"), closePath),
			want: "cfg.yaml:5:14: $.shops[0].hours.close: bad",
			path: "$.shops[0].hours.close",
		},
		"an error with no location binds at the scope": {
			err:  errors.New("bad"),
			want: "cfg.yaml:3:5: $.shops[0].hours: bad",
			path: "$.shops[0].hours",
		},
		"a wrapped error with no location binds at the scope": {
			err:  fmt.Errorf("check: %w", errors.New("bad")),
			want: "cfg.yaml:3:5: $.shops[0].hours: check: bad",
			path: "$.shops[0].hours",
		},
		"the error of a context that ended gains no location": {
			err:  context.Canceled,
			want: "cfg.yaml: context canceled",
		},
		"an error that wraps the error of a context gains no location": {
			err:  fmt.Errorf("fetch: %w", context.DeadlineExceeded),
			want: "cfg.yaml: fetch: context deadline exceeded",
		},
		"a position alone stays as it is": {
			err:  niceyaml.NewError("bad", niceyaml.AtPosition(position.New(0, 7))),
			want: "cfg.yaml:1:8: bad",
		},
		// The text a wrapper wrote holds the message alone, so the binding
		// names the joined path once, in front of it.
		"a wrapper adds no path of its own": {
			err:  fmt.Errorf("check: %w", niceyaml.NewError("bad", niceyaml.AtPath(closePath))),
			want: "cfg.yaml:5:14: $.shops[0].hours.close: check: bad",
			path: "$.shops[0].hours.close",
		},
		// A multi-error keeps the message it wrote, which names none of
		// the paths, so each branch the scope locates follows it.
		"a multi-error lists each branch below its own message": {
			err: listError{
				niceyaml.NewError("bad open", niceyaml.AtPath(openPath)),
				niceyaml.NewError("bad close", niceyaml.AtPath(closePath)),
			},
			want: stringtest.JoinLF(
				"cfg.yaml: bad open; bad close",
				"cfg.yaml:4:13: $.shops[0].hours.open: bad open",
				"cfg.yaml:5:14: $.shops[0].hours.close: bad close",
			),
			children: []string{
				"cfg.yaml:4:13: $.shops[0].hours.open: bad open",
				"cfg.yaml:5:14: $.shops[0].hours.close: bad close",
			},
		},
		"a wrapper with two %w verbs lists each branch below its own message": {
			err: fmt.Errorf("%w; %w",
				niceyaml.NewError("bad open", niceyaml.AtPath(openPath)),
				niceyaml.NewError("bad close", niceyaml.AtPath(closePath)),
			),
			want: stringtest.JoinLF(
				"cfg.yaml: bad open; bad close",
				"cfg.yaml:4:13: $.shops[0].hours.open: bad open",
				"cfg.yaml:5:14: $.shops[0].hours.close: bad close",
			),
			children: []string{
				"cfg.yaml:4:13: $.shops[0].hours.open: bad open",
				"cfg.yaml:5:14: $.shops[0].hours.close: bad close",
			},
		},
		// Each branch is a problem of its own, so the branch with no
		// location binds at the scope whatever the others carry.
		"each line of a join joins the scope": {
			err: errors.Join(
				niceyaml.NewError("early", niceyaml.AtPath(openPath)),
				errors.New("plain"),
				niceyaml.NewError("late", niceyaml.AtPath(closePath)),
			),
			want: stringtest.JoinLF(
				"cfg.yaml:3:5: $.shops[0].hours: plain",
				"cfg.yaml:4:13: $.shops[0].hours.open: early",
				"cfg.yaml:5:14: $.shops[0].hours.close: late",
			),
			children: []string{
				"cfg.yaml:4:13: $.shops[0].hours.open: early",
				"cfg.yaml:3:5: $.shops[0].hours: plain",
				"cfg.yaml:5:14: $.shops[0].hours.close: late",
			},
		},
		"each branch of a join with no location binds at the scope": {
			err: errors.Join(errors.New("one"), errors.New("two")),
			want: stringtest.JoinLF(
				"cfg.yaml:3:5: $.shops[0].hours: one",
				"cfg.yaml:3:5: $.shops[0].hours: two",
			),
			children: []string{
				"cfg.yaml:3:5: $.shops[0].hours: one",
				"cfg.yaml:3:5: $.shops[0].hours: two",
			},
		},
		// A summary is a heading, so it takes no location, and each error
		// it heads binds on its own.
		"the errors a summary heads bind at the scope and the summary binds nowhere": {
			err: niceyaml.NewSummary("2 problems",
				errors.New("one"),
				errors.New("two"),
			),
			want: stringtest.JoinLF(
				"cfg.yaml: 2 problems",
				"cfg.yaml:3:5: $.shops[0].hours: one",
				"cfg.yaml:3:5: $.shops[0].hours: two",
			),
			children: []string{
				"cfg.yaml:3:5: $.shops[0].hours: one",
				"cfg.yaml:3:5: $.shops[0].hours: two",
			},
		},
		"each error a summary heads joins the scope": {
			err: niceyaml.NewSummary("2 problems",
				niceyaml.NewError("early", niceyaml.AtPath(openPath)),
				errors.New("plain"),
			),
			want: stringtest.JoinLF(
				"cfg.yaml: 2 problems",
				"cfg.yaml:3:5: $.shops[0].hours: plain",
				"cfg.yaml:4:13: $.shops[0].hours.open: early",
			),
			children: []string{
				"cfg.yaml:4:13: $.shops[0].hours.open: early",
				"cfg.yaml:3:5: $.shops[0].hours: plain",
			},
		},
		// A problem with no location binds at the scope whatever its
		// details carry, and each detail keeps its own location.
		"a problem with no location binds at the scope above its located details": {
			err: niceyaml.NewError("hours conflict", niceyaml.WithDetails(
				niceyaml.NewError("opens here", niceyaml.AtPath(openPath)),
				niceyaml.NewError("closes here", niceyaml.AtPath(closePath)),
			)),
			want: "cfg.yaml:3:5: $.shops[0].hours: hours conflict",
			path: "$.shops[0].hours",
			details: []string{
				"cfg.yaml:4:13: $.shops[0].hours.open: opens here",
				"cfg.yaml:5:14: $.shops[0].hours.close: closes here",
			},
		},
		// A detail explains its parent, so it takes no location.
		"a detail with no location stays as it is": {
			err: niceyaml.NewError("bad",
				niceyaml.AtPath(closePath),
				niceyaml.WithDetails(errors.New("reason")),
			),
			want:    "cfg.yaml:5:14: $.shops[0].hours.close: bad",
			path:    "$.shops[0].hours.close",
			details: []string{"cfg.yaml: reason"},
		},
		"a detail with no location under a problem with none stays as it is": {
			err:     niceyaml.NewError("bad", niceyaml.WithDetails(errors.New("reason"))),
			want:    "cfg.yaml:3:5: $.shops[0].hours: bad",
			path:    "$.shops[0].hours",
			details: []string{"cfg.yaml: reason"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := hours.Bind(tc.err)
			require.EqualError(t, err, tc.want)

			var bound *niceyaml.SourceError

			require.ErrorAs(t, err, &bound)
			assert.Same(t, hours, bound.Node())

			path, ok := bound.Path()
			assert.Equal(t, tc.path != "", ok)

			if ok {
				assert.Equal(t, tc.path, path.String())
			}

			assert.Equal(t, tc.children, errorTexts(bound.Members()))
			assert.Equal(t, tc.details, errorTexts(bound.Details()))

			// A Rebase under the path of the scope binds the same way,
			// except for the error of a context that ended, which is about
			// the call, so a scoped bind gives it no location.
			if errors.Is(tc.err, context.Canceled) || errors.Is(tc.err, context.DeadlineExceeded) {
				return
			}

			var rebased *niceyaml.SourceError

			require.ErrorAs(t, doc.Bind(niceyaml.Rebase(tc.err, hoursPath)), &rebased)
			assert.Equal(t, tc.want, rebased.Error())
			assert.Equal(t, tc.children, errorTexts(rebased.Members()))
			assert.Equal(t, tc.details, errorTexts(rebased.Details()))
		})
	}

	t.Run("the path resolves from the document and cuts back to the scope", func(t *testing.T) {
		t.Parallel()

		var bound *niceyaml.SourceError

		require.ErrorAs(t, hours.Bind(niceyaml.NewError("bad", niceyaml.AtPath(closePath))), &bound)

		path, ok := bound.Path()
		require.True(t, ok)

		rng, ok := bound.Range()
		require.True(t, ok)

		ranges, err := bound.Document().Ranges(path)
		require.NoError(t, err)
		assert.Equal(t, position.Ranges{rng}, ranges)

		written, ok := path.CutPrefix(bound.Node().Path())
		require.True(t, ok)
		assert.Equal(t, closePath, written)
	})

	t.Run("a binding comes back as it is", func(t *testing.T) {
		t.Parallel()

		bound := hours.Bind(niceyaml.NewError("bad", niceyaml.AtPath(closePath)))

		assert.Same(t, bound, hours.Bind(bound))
		assert.Same(t, bound, doc.Bind(bound))
		assert.Same(t, bound, yamltest.At(t, doc, paths.Current().Child("shops")).Bind(bound))
	})

	t.Run("the root of the document binds a path as written", func(t *testing.T) {
		t.Parallel()

		err := doc.Bind(niceyaml.NewError("bad", niceyaml.AtPath(closePath)))
		require.EqualError(t, err, "cfg.yaml:1:8: $.close: bad")
	})

	t.Run("the root of the document gives an error with no location none", func(t *testing.T) {
		t.Parallel()

		err := doc.Bind(errors.New("bad"))
		require.EqualError(t, err, "cfg.yaml: bad")

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Same(t, doc, bound.Node())

		_, ok := bound.Range()
		assert.False(t, ok)
		require.NoError(t, bound.Unresolved())

		// A Node scoped to the root path is the root of the document too.
		err = yamltest.At(t, doc, paths.Current()).Bind(errors.New("bad"))
		require.EqualError(t, err, "cfg.yaml: bad")
	})

	t.Run("an error with no location marks the value of the scope", func(t *testing.T) {
		t.Parallel()

		plain := errors.New("bad")

		var bound *niceyaml.SourceError

		require.ErrorAs(t, hours.Bind(plain), &bound)
		require.ErrorIs(t, bound, plain)
		assert.Equal(t, "bad", bound.Message())

		// The binding points where an Error at the root path of the scope
		// does.
		var located *niceyaml.SourceError

		require.ErrorAs(t, hours.Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Current()))), &located)

		rng, ok := bound.Range()
		require.True(t, ok)

		want, ok := located.Range()
		require.True(t, ok)
		assert.Equal(t, want, rng)
	})

	t.Run("a validator's error with no location binds at the scope", func(t *testing.T) {
		t.Parallel()

		plain := errors.New("bad")
		want := "cfg.yaml:3:5: $.shops[0].hours: bad"

		fn := niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
			return plain
		})

		require.EqualError(t, fn.Check(t.Context(), hours), want)
		require.EqualError(t, hours.Validate(t.Context(), fn), want)

		// Validate binds the error a validator leaves unbound.
		require.EqualError(t, hours.Validate(t.Context(), &fieldValidator{err: plain}), want)

		_, err := hours.Decode[checkHours](t.Context(), niceyaml.WithValidator(&fieldValidator{err: plain}))
		require.EqualError(t, err, want)

		multi := niceyaml.MultiValidator(&fieldValidator{err: plain}, &fieldValidator{err: errors.New("worse")})
		require.EqualError(t, hours.Validate(t.Context(), multi), stringtest.JoinLF(
			"cfg.yaml:3:5: $.shops[0].hours: bad",
			"cfg.yaml:3:5: $.shops[0].hours: worse",
		))

		// The same validators on the root of the document name the source
		// alone.
		require.EqualError(t, doc.Validate(t.Context(), fn), "cfg.yaml: bad")
		require.EqualError(t, doc.Validate(t.Context(), &fieldValidator{err: plain}), "cfg.yaml: bad")

		// A context that ended is no fault of the value.
		ended := &fieldValidator{err: context.DeadlineExceeded}
		require.EqualError(t, hours.Validate(t.Context(), ended), "cfg.yaml: context deadline exceeded")
	})

	t.Run("an error of the Node's own operation gains no location", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			run  func(t *testing.T) error
			is   error
			want string
		}{
			"a decode target that is no pointer": {
				run: func(t *testing.T) error {
					t.Helper()

					return hours.DecodeInto(t.Context(), nil)
				},
				is:   niceyaml.ErrDecodeTarget,
				want: "cfg.yaml: decode target is not a non-nil pointer: got nil",
			},
			"a wildcard path given to At": {
				run: func(t *testing.T) error {
					t.Helper()

					_, err := hours.At(paths.Current().ChildAll())

					return err //nolint:wrapcheck // The test inspects the error of the call.
				},
				is:   paths.ErrWildcard,
				want: "cfg.yaml: resolve $.shops[0].hours.*: wildcard path matches any number of nodes",
			},
			"a path that names an index of a mapping": {
				run: func(t *testing.T) error {
					t.Helper()

					_, err := hours.At(paths.Current().Index(3))

					return err //nolint:wrapcheck // The test inspects the error of the call.
				},
				is:   paths.ErrNotFound,
				want: "cfg.yaml: resolve $.shops[0].hours[3]: not found",
			},
			"a wildcard path given to Ranges": {
				run: func(t *testing.T) error {
					t.Helper()

					_, err := hours.Ranges(paths.Current().ChildAll())

					return err //nolint:wrapcheck // The test inspects the error of the call.
				},
				is:   paths.ErrWildcard,
				want: "cfg.yaml: resolve $.shops[0].hours.*: wildcard path matches any number of nodes",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := tc.run(t)
				require.ErrorIs(t, err, tc.is)
				require.EqualError(t, err, tc.want)

				var bound *niceyaml.SourceError

				require.ErrorAs(t, err, &bound)
				assert.Same(t, hours, bound.Node())

				_, ok := bound.Range()
				assert.False(t, ok)
			})
		}
	})

	t.Run("an error a scoped method returns names the scope once", func(t *testing.T) {
		t.Parallel()

		_, err := hours.At(paths.Current().Child("nope"))
		require.ErrorIs(t, err, paths.ErrNotFound)
		assert.Equal(t, 1, strings.Count(err.Error(), "$.shops[0].hours.nope"))
		assert.NotContains(t, err.Error(), "$.shops[0].hours.shops")
	})

	t.Run("a scoped validator and the same check at the root agree", func(t *testing.T) {
		t.Parallel()

		// The validator writes a `$` path, which reads from the root of the
		// document, so the binding leaves the path as written.
		reject := niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
			return n.Document().Bind(niceyaml.NewError("bad", niceyaml.AtPath(n.Path().Join(closePath))))
		})

		want := "cfg.yaml:5:14: $.shops[0].hours.close: bad"

		require.EqualError(t, reject.Check(t.Context(), hours), want)
		require.EqualError(t, hours.Validate(t.Context(), reject), want)

		scoped := niceyaml.ValidatorFunc(func(_ context.Context, _ *niceyaml.Node) error {
			return niceyaml.NewError("bad", niceyaml.AtPath(closePath))
		})

		require.EqualError(t, hours.Validate(t.Context(), scoped), want)

		_, err := hours.Decode[checkHours](t.Context(), niceyaml.WithValidator(scoped))
		require.EqualError(t, err, want)
	})
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
		err = niceyaml.NewError("value is required", niceyaml.AtPath(paths.Current().Child("value")))
	}

	return err
}

func TestDocument_Decode_SchemaThenDecodeError(t *testing.T) {
	t.Parallel()

	// Test when the decode after validation fails.
	dd := yamltest.FirstDocument(t, `value: not_a_number`)

	// Schema validation passes, but the decode fails on a type mismatch.
	_, err := dd.Decode[strictValueConfig](t.Context(),
		niceyaml.WithValidator(passingValidator()),
	)

	require.Error(t, err)

	var yamlErr *niceyaml.Error

	require.ErrorAs(t, err, &yamlErr)
}

func TestDocument_Decode_SelfValidator(t *testing.T) {
	t.Parallel()

	t.Run("calls Validate on a SelfValidator struct", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			name: test
			value: 42
		`)
		dd := yamltest.FirstDocument(t, input)

		result, err := dd.Decode[validatorConfig](t.Context())
		require.NoError(t, err)
		assert.True(t, result.validated, "Validate() should have been called by Decode()")
		assert.Equal(t, "test", result.Name)
		assert.Equal(t, 42, result.Value)
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
		dd := yamltest.FirstDocument(t, input)

		result, err := dd.Decode[plainConfig](t.Context())
		require.NoError(t, err)
		assert.Equal(t, "test", result.Name)
		assert.Equal(t, 42, result.Value)
	})

	t.Run("runs schema and Validate in order", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			name: test
			value: 42
		`)
		dd := yamltest.FirstDocument(t, input)

		var called bool

		result, err := dd.Decode[bothValidatorConfig](
			t.Context(),
			niceyaml.WithValidator(nameSchema(&called)),
		)
		require.NoError(t, err)
		assert.True(t, called, "the validator should have been called")
		assert.True(t, result.validated, "Validate() should have been called after decode")
	})

	t.Run("returns SelfValidator error", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			name: ""
			value: 42
		`)
		dd := yamltest.FirstDocument(t, input)

		_, err := dd.Decode[bothValidatorConfig](t.Context())
		require.ErrorIs(t, err, errNameRequired)
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
		return niceyaml.Invalid(
			errNameRequired,
			niceyaml.AtPath(paths.Current().Child("name").Key()),
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
			return niceyaml.Invalid(
				errSchemaValidationFailed,
				niceyaml.AtPath(paths.Current().Child("name").Key()),
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
		return niceyaml.Invalid(
			errNameRequired,
			niceyaml.AtPath(paths.Current().Child("name").Key()),
		)
	}

	return nil
}

// strictValueConfig has a typed field, so decoding a mismatched value fails
// after schema validation passes.
type strictValueConfig struct {
	Value int `yaml:"value"`
}

func TestDocument_Tokens(t *testing.T) {
	t.Parallel()

	t.Run("yields the same tokens on every pass", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			---
			a: 1
			---
			b: 2
		`)
		source := niceyaml.NewSourceFromString(input)
		d := source.Documents()

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

	t.Run("pairs tokens with documents closed by an end marker", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			kind: A
			...
			kind: B
		`)
		source := niceyaml.NewSourceFromString(input)
		d := source.Documents()
		require.Len(t, d, 2)

		kindPath := paths.Current().Child("kind")

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
		d := source.Documents()
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
		d := source.Documents()
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

	t.Run("splits consecutive headers into an empty document and the next one", func(t *testing.T) {
		t.Parallel()

		// Each header starts a document, so the first header holds an empty
		// document and the second opens the document that holds the content.
		input := stringtest.Input(`
			---
			---
			b: 2
		`)
		source := niceyaml.NewSourceFromString(input)
		d := source.AllDocuments()
		require.Len(t, d, 2)

		first := d[0].Tokens()
		require.Len(t, first, 1)
		assert.Same(t, source.Tokens()[0], first[0])

		second := d[1].Tokens()
		require.NotEmpty(t, second)
		assert.Equal(t, token.DocumentHeaderType, second[0].Type)
		assert.Equal(t, input, yamltest.DumpTokenOrigins(first)+yamltest.DumpTokenOrigins(second))

		empty, err := d[0].Decode[map[string]int](t.Context())
		require.NoError(t, err)
		assert.Nil(t, empty)

		got, err := d[1].Decode[map[string]int](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]int{"b": 2}, got)
	})

	t.Run("keeps every document after consecutive headers", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(stringtest.Input(`
			a: 1
			---
			---
			b: 2
			---
			c: 3
		`))

		var got []map[string]int

		for _, dd := range source.AllDocuments() {
			v, err := dd.Decode[map[string]int](t.Context())
			require.NoError(t, err)

			got = append(got, v)
		}

		assert.Equal(t, []map[string]int{{"a": 1}, nil, {"b": 2}, {"c": 3}}, got)
	})

	t.Run("splits consecutive headers past a comment on the first", func(t *testing.T) {
		t.Parallel()

		// The parser folds the comment into the first header, so the two
		// headers still meet with nothing between them.
		source := niceyaml.NewSourceFromString(stringtest.Input(`
			--- # first
			---
			port: http
		`))
		d := source.AllDocuments()
		require.Len(t, d, 2)

		got, err := d[1].Decode[map[string]string](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"port": "http"}, got)
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
		dd := yamltest.FirstDocument(t, input)

		err := dd.Validate(t.Context(), passingValidator())
		require.NoError(t, err)
	})

	t.Run("invalid data fails schema validation", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			name: test
			count: not-a-number
		`)
		dd := yamltest.FirstDocument(t, input)

		wantErr := errors.New("validation failed")

		err := dd.Validate(t.Context(), rejectingValidator(wantErr))
		require.ErrorIs(t, err, wantErr)
	})
}

// failingValidator always fails self-validation with a path error.
type failingValidator struct {
	Name string `yaml:"name"`
}

func (failingValidator) Validate() error {
	return niceyaml.NewError("rejected", niceyaml.AtPath(paths.Current().Child("name")))
}

// fieldValidator is a [niceyaml.Validator] that returns the error its
// field holds, so a nil pointer to one panics when it runs.
type fieldValidator struct {
	err error
}

func (v *fieldValidator) Check(context.Context, *niceyaml.Node) error {
	return v.err
}

func TestDocument_Err(t *testing.T) {
	t.Parallel()

	// The second document does not parse, and the documents around it do.
	// The first one reuses an anchor name and the third holds a !!int tag,
	// so each decodes from the second parse of the source.
	const input = "a: &x 1\nb: &x 2\nc: *x\n---\nd: [\n---\ne: !!int 0x10\n"

	documents := func(t *testing.T) []*niceyaml.Node {
		t.Helper()

		docs := niceyaml.NewSourceFromString(input, niceyaml.WithName("f.yaml")).AllDocuments()
		require.Len(t, docs, 3)
		require.EqualError(t, docs[1].Err(), "f.yaml:5:4: sequence end token ']' not found")

		return docs
	}

	t.Run("a document that parsed has none", func(t *testing.T) {
		t.Parallel()

		docs := documents(t)

		require.NoError(t, docs[0].Err())
		require.NoError(t, docs[2].Err())

		first, err := docs[0].Decode[map[string]int](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]int{"a": 1, "b": 2, "c": 2}, first)

		third, err := docs[2].Decode[map[string]int](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]int{"e": 16}, third)

		require.NoError(t, docs[0].Validate(t.Context(), passingValidator()))

		scoped, err := docs[2].At(paths.Current().Child("e"))
		require.NoError(t, err)
		assert.Equal(t, 2, scoped.DocumentIndex())
		require.NoError(t, scoped.Err())
	})

	t.Run("the methods that read the tree return it", func(t *testing.T) {
		t.Parallel()

		// A validator that ran would replace the syntax error with its own.
		ran := &fieldValidator{err: errDocumentRejected}

		tcs := map[string]struct {
			// Calls the method and returns its error.
			call func(t *testing.T, doc *niceyaml.Node) error
		}{
			"Decode": {call: func(t *testing.T, doc *niceyaml.Node) error {
				t.Helper()

				got, err := doc.Decode[map[string]any](t.Context())
				assert.Nil(t, got)

				return err
			}},
			"Decode with a validator": {call: func(t *testing.T, doc *niceyaml.Node) error {
				t.Helper()

				_, err := doc.Decode[map[string]any](t.Context(), niceyaml.WithValidator(ran))

				return err
			}},
			"DecodeInto": {call: func(t *testing.T, doc *niceyaml.Node) error {
				t.Helper()

				got := map[string]int{"kept": 1}
				err := doc.DecodeInto(t.Context(), &got)
				assert.Equal(t, map[string]int{"kept": 1}, got)

				return err //nolint:wrapcheck // The test inspects the error of the call.
			}},
			"Validate with a nil validator": {call: func(t *testing.T, doc *niceyaml.Node) error {
				t.Helper()

				return doc.Validate(t.Context(), nil)
			}},
			"Validate": {call: func(t *testing.T, doc *niceyaml.Node) error {
				t.Helper()

				return doc.Validate(t.Context(), ran)
			}},
			"At": {call: func(t *testing.T, doc *niceyaml.Node) error {
				t.Helper()

				scoped, err := doc.At(paths.Current().Child("d"))
				assert.Nil(t, scoped)

				return err //nolint:wrapcheck // The test inspects the error of the call.
			}},
			"At the root": {call: func(t *testing.T, doc *niceyaml.Node) error {
				t.Helper()

				_, err := doc.At(paths.Current())

				return err //nolint:wrapcheck // The test inspects the error of the call.
			}},
			"Nodes": {call: func(t *testing.T, doc *niceyaml.Node) error {
				t.Helper()

				nodes, err := doc.Nodes(paths.Current().Child("d").IndexAll())
				assert.Nil(t, nodes)

				return err //nolint:wrapcheck // The test inspects the error of the call.
			}},
			"Ranges": {call: func(t *testing.T, doc *niceyaml.Node) error {
				t.Helper()

				ranges, err := doc.Ranges(paths.Current().Child("d"))
				assert.Nil(t, ranges)

				return err //nolint:wrapcheck // The test inspects the error of the call.
			}},
			"ValidatorFunc called directly": {call: func(t *testing.T, doc *niceyaml.Node) error {
				t.Helper()

				return niceyaml.ValidatorFunc(ran.Check).Check(t.Context(), doc)
			}},
			"MultiValidator called directly": {call: func(t *testing.T, doc *niceyaml.Node) error {
				t.Helper()

				return niceyaml.MultiValidator(ran, ran).Check(t.Context(), doc)
			}},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := documents(t)[1]

				err := tc.call(t, doc)
				require.Error(t, err)
				assert.Same(t, doc.Err(), err)
			})
		}
	})

	t.Run("the document has no tree", func(t *testing.T) {
		t.Parallel()

		doc := documents(t)[1]

		assert.Nil(t, doc.AST())
		assert.Nil(t, doc.DocumentAST())
		assert.Same(t, doc, doc.Document())
		assert.True(t, doc.Path().IsRoot())
	})

	t.Run("the methods that read the tokens work", func(t *testing.T) {
		t.Parallel()

		doc := documents(t)[1]

		assert.Equal(t, 1, doc.DocumentIndex())
		assert.Equal(t, position.NewSpan(3, 5), doc.Span())
		assert.Equal(t, "---\nd: [", doc.View().Held().Content())
		assert.Equal(t, 4, doc.View().Held().Line(0).Number())

		var values []string

		for _, tk := range doc.Tokens() {
			values = append(values, tk.Value)
		}

		assert.Equal(t, []string{"---", "d", ":", "["}, values)
		require.Len(t, doc.Preamble(), 1)
		assert.Equal(t, token.DocumentHeaderType, doc.Preamble()[0].Type)
	})

	t.Run("diffs against a revision that parses", func(t *testing.T) {
		t.Parallel()

		before := documents(t)

		after := niceyaml.NewSourceFromString(strings.Replace(input, "d: [", "d: []", 1)).Documents()
		require.Len(t, after, 3)

		result := diff.Diff(before[1].View(), after[1].View())

		assert.Equal(t, diff.Stats{Added: 1, Removed: 1}, result.Stats())

		unified := result.Unified().String()
		assert.Contains(t, unified, "d: []")
		assert.NotContains(t, unified, "a: &x 1", "the first document is not in the diff")
	})

	t.Run("the error is bound to the document", func(t *testing.T) {
		t.Parallel()

		doc := documents(t)[1]

		bound, ok := doc.Err().(*niceyaml.SourceError) //nolint:errorlint // The value itself is the bound error.
		require.True(t, ok, "want *niceyaml.SourceError, got %T", doc.Err())
		assert.Same(t, doc.Source(), bound.Source())
		assert.Same(t, doc, bound.Node())
		assert.Same(t, doc, bound.Document())

		rng, ok := bound.Range()
		require.True(t, ok)
		assert.True(t, doc.Span().Contains(rng.Start.Line))

		// File returns the same binding, so it names the document too.
		_, fileErr := doc.Source().File()
		assert.Same(t, doc.Err(), fileErr)
	})

	t.Run("a path bound through the document resolves nowhere", func(t *testing.T) {
		t.Parallel()

		doc := documents(t)[1]

		err := doc.Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("d"))))

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Same(t, doc, bound.Document())
		assert.Equal(t, "f.yaml: document 2: $.d: bad", err.Error())

		_, ok := bound.Range()
		assert.False(t, ok)

		require.ErrorIs(t, bound.Unresolved(), niceyaml.ErrPathNeedsDocument)
		require.ErrorIs(t, bound.Unresolved(), doc.Err())
	})

	t.Run("a position bound through the document resolves", func(t *testing.T) {
		t.Parallel()

		doc := documents(t)[1]

		err := doc.Bind(niceyaml.NewError("bad", niceyaml.AtPosition(position.New(4, 0))))

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Same(t, doc, bound.Document())
		assert.Equal(t, "f.yaml:5:1: bad", err.Error())
		require.NoError(t, bound.Unresolved())
	})
}

func TestDocument_ErrorsResolveInDocument(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		name: first
		---
		name: second
	`)
	namePath := paths.Current().Child("name")

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
		d := source.Documents()

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
		d := source.Documents()

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
		d := source.Documents()

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

func TestWithDisallowUnknownFields_ControlCharacters(t *testing.T) {
	t.Parallel()

	type config struct {
		Name string `yaml:"name"`
	}

	// The decoder writes the name of an unknown field into its message as
	// the document spells it. A key with a line feed or an escape sequence
	// thus reaches the message, where it must not start a line that reads
	// as another error or write to a terminal.
	tcs := map[string]struct {
		input string
		want  string
	}{
		"a key that forges a line": {
			input: "name: x\n\"a\\nother.yaml:9:9: $.secret: forged\": 1\n",
			want: `f.yaml:2:1: $.'a\nother.yaml:9:9: $.secret: forged'~: ` +
				"unknown field \"a\u240aother.yaml:9:9: $.secret: forged\"",
		},
		"a key with an escape sequence": {
			input: "name: x\n\"a\\e[31mb\": 1\n",
			want:  `f.yaml:2:1: $.'a\u001b[31mb'~: ` + "unknown field \"a\u241b[31mb\"",
		},
		"two keys": {
			input: "name: x\n\"a\\nb\": 1\n\"c\\td\": 2\n",
			want: stringtest.JoinLF(
				"f.yaml: 2 unknown fields",
				`f.yaml:2:1: $.'a\nb'~: `+"unknown field \"a\u240ab\"",
				`f.yaml:3:1: $.'c\td'~: `+"unknown field \"c\u2409d\"",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input, niceyaml.WithName("f.yaml"))

			_, err := source.Decode[config](t.Context(), niceyaml.WithDisallowUnknownFields(true))
			require.EqualError(t, err, tc.want)
			assert.NotContains(t, err.Error(), "\x1b")

			// The path reads back as the key the document holds.
			var bound *niceyaml.SourceError

			require.ErrorAs(t, err, &bound)

			for b := range niceyaml.AllBindings(err) {
				path, ok := b.Path()
				if !ok {
					continue
				}

				parsed, err := paths.Parse(path.String())
				require.NoError(t, err)
				assert.Equal(t, path, parsed)
			}
		})
	}
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
		dd := yamltest.FirstDocument(t, input)

		result, err := dd.Decode[strictConfig](t.Context())
		require.NoError(t, err)
		assert.Equal(t, "test", result.Name)
	})

	t.Run("option rejects unknown fields", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			name: test
			extra: field
		`)
		dd := yamltest.FirstDocument(t, input)

		_, err := dd.Decode[strictConfig](t.Context(), niceyaml.WithDisallowUnknownFields(true))
		require.Error(t, err)

		var yamlErr *niceyaml.Error

		require.ErrorAs(t, err, &yamlErr)
	})

	t.Run("option applies per call", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			name: test
			extra: field
		`)
		dd := yamltest.FirstDocument(t, input)

		// The same document decodes strictly on one call and loosely on the
		// next, so the option belongs to the call rather than the Source.
		_, err := dd.Decode[strictConfig](t.Context(), niceyaml.WithDisallowUnknownFields(true))
		require.Error(t, err)

		result, err := dd.Decode[strictConfig](t.Context())
		require.NoError(t, err)
		assert.Equal(t, "test", result.Name)
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
		dd := yamltest.FirstDocument(t, input)

		innerPath := paths.Current().Child("inner")

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
		d := source.Documents()

		var errCount int

		for _, dd := range d {
			_, err := dd.Decode[strictConfig](t.Context(), niceyaml.WithDisallowUnknownFields(true))
			if err != nil {
				errCount++
			}
		}

		assert.Equal(t, 2, errCount)
	})
}

func TestWithAllowedFieldPrefixes(t *testing.T) {
	t.Parallel()

	type config struct {
		Name string `yaml:"name"`
	}

	strict := niceyaml.WithDisallowUnknownFields(true)

	tcs := map[string]struct {
		input string
		err   string
		opts  []niceyaml.DecodeOption
	}{
		"allows a key under a prefix": {
			input: "name: a\nx-note: b\n",
			opts:  []niceyaml.DecodeOption{strict, niceyaml.WithAllowedFieldPrefixes("x-")},
		},
		"rejects a key under no prefix": {
			input: "name: a\nx-note: b\nnote: c\n",
			opts:  []niceyaml.DecodeOption{strict, niceyaml.WithAllowedFieldPrefixes("x-")},
			err:   `3:1: $.note~: unknown field "note"`,
		},
		"each option adds to the prefixes before it": {
			input: "name: a\nx-note: b\ny-note: c\nz-note: d\n",
			opts: []niceyaml.DecodeOption{
				strict,
				niceyaml.WithAllowedFieldPrefixes("x-"),
				niceyaml.WithAllowedFieldPrefixes("y-"),
			},
			err: `4:1: $.z-note~: unknown field "z-note"`,
		},
		"empty prefix allows every key": {
			input: "name: a\nnote: b\n",
			opts:  []niceyaml.DecodeOption{strict, niceyaml.WithAllowedFieldPrefixes("")},
		},
		"changes nothing in a decode that accepts unknown fields": {
			input: "name: a\nnote: b\n",
			opts:  []niceyaml.DecodeOption{niceyaml.WithAllowedFieldPrefixes("x-")},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := yamltest.FirstDocument(t, tc.input).Decode[config](t.Context(), tc.opts...)
			if tc.err != "" {
				require.EqualError(t, err, tc.err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, config{Name: "a"}, got)
		})
	}
}

// plainLevel has no method of its own. A function from
// niceyaml.WithCustomUnmarshaler decodes it from the name of a level.
type plainLevel int

// The levels a document names.
const (
	levelLow plainLevel = iota + 1
	levelHigh
)

// decodeLevel decodes a [plainLevel] from its name.
func decodeLevel(_ context.Context, l *plainLevel, decode func(any) error) error {
	var name string

	err := decode(&name)
	if err != nil {
		return err
	}

	switch name {
	case "low":
		*l = levelLow
	case "high":
		*l = levelHigh
	default:
		return fmt.Errorf("%w %q", errUnknownLevel, name)
	}

	return nil
}

// decodeCIDR decodes a [net.IPNet] from a string in CIDR notation, as
// the example of niceyaml.WithCustomUnmarshaler does.
func decodeCIDR(_ context.Context, n *net.IPNet, decode func(any) error) error {
	var s string

	err := decode(&s)
	if err != nil {
		return err
	}

	_, parsed, err := net.ParseCIDR(s)
	if err != nil {
		return err //nolint:wrapcheck // The test inspects the error as it is.
	}

	*n = *parsed

	return nil
}

// plainSpan has no method of its own. A function from
// niceyaml.WithCustomUnmarshaler decodes it, as decodeSpan does.
type plainSpan struct {
	From  int        `yaml:"from"`
	To    int        `yaml:"to"`
	Level plainLevel `yaml:"level"`
}

// decodeSpan decodes a [plainSpan] by its fields and reports a to below
// its from at the path of the to, which reads from the span.
func decodeSpan(_ context.Context, s *plainSpan, decode func(any) error) error {
	type fields plainSpan

	err := decode((*fields)(s))
	if err != nil {
		return err
	}

	if s.To < s.From {
		return niceyaml.NewError("to is below from", niceyaml.AtPath(paths.Current().Child("to")))
	}

	return nil
}

// levelHolder decodes itself through a second type with the same fields
// and puts text of its own in front of the error of its level.
type levelHolder struct {
	Level plainLevel `yaml:"level"`
}

func (h *levelHolder) UnmarshalYAML(unmarshal func(any) error) error {
	type plain levelHolder

	err := unmarshal((*plain)(h))
	if err != nil {
		return fmt.Errorf("holder: %w", err)
	}

	return nil
}

// methodLevel decodes itself from text, and reports an error whenever
// the decoder calls its method.
type methodLevel int

func (*methodLevel) UnmarshalText([]byte) error {
	return errors.New("the decoder called the method")
}

func TestWithCustomUnmarshaler(t *testing.T) {
	t.Parallel()

	levels := niceyaml.WithCustomUnmarshaler(decodeLevel)

	t.Run("decodes every value of the type", func(t *testing.T) {
		t.Parallel()

		type config struct {
			Pointer *plainLevel           `yaml:"pointer"`
			ByName  map[string]plainLevel `yaml:"by_name"`
			ByLevel map[plainLevel]string `yaml:"by_level"`
			List    []plainLevel          `yaml:"list"`
			Level   plainLevel            `yaml:"level"`
		}

		doc := yamltest.FirstDocument(t, stringtest.Input(`
			level: high
			pointer: low
			list: [low, high]
			by_name: {a: high}
			by_level: {low: a}
		`))

		got, err := doc.Decode[config](t.Context(), levels)
		require.NoError(t, err)

		low := levelLow

		assert.Equal(t, config{
			Level:   levelHigh,
			Pointer: &low,
			List:    []plainLevel{levelLow, levelHigh},
			ByName:  map[string]plainLevel{"a": levelHigh},
			ByLevel: map[plainLevel]string{levelLow: "a"},
		}, got)
	})

	t.Run("hands the function the context and a decode of the value", func(t *testing.T) {
		t.Parallel()

		type ctxKey struct{}

		var got []string

		record := niceyaml.WithCustomUnmarshaler(
			func(ctx context.Context, _ *plainLevel, decode func(any) error) error {
				var name string

				err := decode(&name)
				if err != nil {
					return err
				}

				got = append(got, fmt.Sprintf("%v|%s", ctx.Value(ctxKey{}), name))

				return nil
			},
		)

		// The decode reads an alias as the content of its anchor, and a
		// scalar without its quotes and the comment on its line.
		doc := yamltest.FirstDocument(t, "a: &a low\nlevels:\n  - *a\n  - \"high\" # top\n")

		_, err := doc.Decode[struct {
			Levels []plainLevel `yaml:"levels"`
		}](context.WithValue(t.Context(), ctxKey{}, "decode"), record)
		require.NoError(t, err)
		assert.Equal(t, []string{"decode|low", "decode|high"}, got)
	})

	t.Run("the decode reads a scalar however the document writes it", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input string
		}{
			"plain":          {input: "office: 10.0.0.0/8\n"},
			"double quoted":  {input: "office: \"10.0.0.0/8\"\n"},
			"single quoted":  {input: "office: '10.0.0.0/8'\n"},
			"with a comment": {input: "office: 10.0.0.0/8 # the office\n"},
			"block":          {input: "office: |-\n  10.0.0.0/8\n"},
			"folded":         {input: "office: >-\n  10.0.0.0/8\n"},
			"tagged":         {input: "office: !!str 10.0.0.0/8\n"},
			"anchored":       {input: "office: &net 10.0.0.0/8\n"},
			"alias":          {input: "net: &net 10.0.0.0/8\noffice: *net\n"},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				got, err := yamltest.FirstDocument(t, tc.input).Decode[map[string]net.IPNet](
					t.Context(), niceyaml.WithCustomUnmarshaler(decodeCIDR),
				)
				require.NoError(t, err)
				assert.Equal(t, "10.0.0.0/8", new(got["office"]).String())
			})
		}
	})

	t.Run("the decode reads the text of the value into a raw message", func(t *testing.T) {
		t.Parallel()

		var got []string

		record := niceyaml.WithCustomUnmarshaler(
			func(_ context.Context, _ *plainLevel, decode func(any) error) error {
				var text yaml.RawMessage

				err := decode(&text)
				if err != nil {
					return err
				}

				got = append(got, string(text))

				return nil
			},
		)

		// The text spells an alias as the content of its anchor, and keeps
		// the quotes and the comment of a scalar.
		doc := yamltest.FirstDocument(t, "a: &a low\nlevels:\n  - *a\n  - \"high\" # top\n  - {a: 1}\n")

		_, err := doc.Decode[struct {
			Levels []plainLevel `yaml:"levels"`
		}](t.Context(), record)
		require.NoError(t, err)
		assert.Equal(t, []string{"low\n", "\"high\" # top\n", "{a: 1}\n"}, got)
	})

	// The decode callback returns the rejection of a decode, and the
	// decode that called the function binds it in the document.
	t.Run("the error of the function binds in the document", func(t *testing.T) {
		t.Parallel()

		type config struct {
			Base    any                   `yaml:"base"`
			ByName  map[string]net.IPNet  `yaml:"by_name"`
			ByLevel map[plainLevel]string `yaml:"by_level"`
			Nets    []net.IPNet           `yaml:"nets"`
			Spans   []plainSpan           `yaml:"spans"`
			Count   int                   `yaml:"count"`
		}

		decoders := niceyaml.DecodeOptions(
			niceyaml.WithCustomUnmarshaler(decodeCIDR),
			niceyaml.WithCustomUnmarshaler(decodeSpan),
			levels,
		)

		tcs := map[string]struct {
			input string
			want  string
			opts  []niceyaml.DecodeOption
		}{
			"sequence where the function decodes a string": {
				input: "nets:\n  - 10.0.0.0/8\n  - [1, 2]\n",
				want:  "3:3: $.nets[1]: expected string, got sequence",
			},
			"mapping where the function decodes a string": {
				input: "nets:\n  - 10.0.0.0/8\n  - {a: 1}\n",
				want:  "3:3: $.nets[1]: expected string, got mapping",
			},
			"string the function rejects": {
				input: "nets:\n  - 10.0.0.0/8\n  - 10.0.0.0/33\n",
				want:  "3:5: $.nets[1]: invalid CIDR address: 10.0.0.0/33",
			},
			"map value": {
				input: "by_name:\n  a: 10.0.0.0/8\n  b: [1]\n",
				want:  "3:3: $.by_name.b: expected string, got sequence",
			},
			"map key": {
				input: "by_level:\n  low: a\n  mid: b\n",
				want:  `3:3: $.by_level.mid~: unknown level "mid"`,
			},
			"field of the value": {
				input: "spans:\n  - {from: 1, to: 5}\n  - from: nine\n    to: 3\n",
				want:  "3:11: $.spans[1].from: expected integer, got string",
			},
			// The decode of a value reports every problem of that value.
			"two fields of the value": {
				input: "spans:\n  - from: nine\n    to: [3]\n",
				want: stringtest.JoinLF(
					"2 problems",
					"2:11: $.spans[0].from: expected integer, got string",
					"3:5: $.spans[0].to: expected integer, got sequence",
				),
			},
			// The path the function writes reads from its value.
			"path the function writes": {
				input: "spans:\n  - {from: 1, to: 5}\n  - from: 9\n    to: 3\n",
				want:  "4:9: $.spans[1].to: to is below from",
			},
			// A path goes through an alias to the line that holds the
			// value.
			"path in a value an alias holds": {
				input: "base: &base {from: 9, to: 3}\nspans:\n  - *base\n",
				want:  "1:27: $.spans[0].to: to is below from",
			},
			"field of a value an alias holds": {
				input: "base: &base {from: nine}\nspans:\n  - *base\n",
				want:  "1:20: $.spans[0].from: expected integer, got string",
			},
			// The text of the value writes out what a merge key brings in,
			// so the path names the merge key, and resolves through it.
			"field a merge key brings in": {
				input: "base: &base {from: nine}\nspans:\n  - <<: *base\n    to: 3\n",
				want:  "1:20: $.spans[0].<<.from: expected integer, got string",
			},
			"field that holds an alias": {
				input: "base: &base [1]\nspans:\n  - from: *base\n    to: 3\n",
				want:  "3:11: $.spans[0].from: expected integer, got sequence",
			},
			// The decode of a value takes the options of the decode that
			// called the function, so another function decodes the level.
			"value another function decodes below the value": {
				input: "spans:\n  - {from: 1, to: 2, level: mid}\n",
				want:  `2:29: $.spans[0].level: unknown level "mid"`,
			},
			"unknown field of the value": {
				input: "spans:\n  - from: 1\n    until: 2\n",
				opts:  []niceyaml.DecodeOption{niceyaml.WithDisallowUnknownFields(true)},
				want:  `3:5: $.spans[0].until~: unknown field "until"`,
			},
			// The decoder stops at the first value a function rejects, and
			// the search for the other problems of the decode calls no
			// function again. One decode thus reports the first network
			// alone.
			"second value a function rejects": {
				input: "nets:\n  - 10.0.0.0/33\n  - [1, 2]\n",
				want:  "2:5: $.nets[0]: invalid CIDR address: 10.0.0.0/33",
			},
			// The search still finds the count, which no function decodes.
			"second value a function rejects beside another problem": {
				input: "count: x\nnets:\n  - [1, 2]\n  - {a: 1}\n",
				want: stringtest.JoinLF(
					"2 problems",
					"1:8: $.count: expected integer, got string",
					"3:3: $.nets[0]: expected string, got sequence",
				),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				_, err := yamltest.FirstDocument(t, tc.input).Decode[config](
					t.Context(), append([]niceyaml.DecodeOption{decoders}, tc.opts...)...,
				)
				require.EqualError(t, err, tc.want)
				require.ErrorIs(t, err, niceyaml.ErrDecode)
				requireInvalid(t, err, max(strings.Count(tc.want, "\n"), 1))
			})
		}
	})

	// An unmarshaler below the value writes its paths from its own value,
	// and the decode of the value puts them under the path that value has
	// below the value of the function.
	t.Run("the error of an unmarshaler below the value binds in the document", func(t *testing.T) {
		t.Parallel()

		type box struct {
			Item   reporting            `yaml:"item"`
			Placed positioned           `yaml:"placed"`
			Broken panickingUnmarshaler `yaml:"broken"`
		}

		boxes := niceyaml.WithCustomUnmarshaler(func(_ context.Context, b *box, decode func(any) error) error {
			type fields box

			return decode((*fields)(b))
		})

		tcs := map[string]struct {
			report error
			input  string
			want   string
			detail string
		}{
			"path": {
				input:  "boxes:\n  - item: {from: 9, to: 3}\n",
				report: spanError(),
				want:   "2:25: $.boxes[0].item.to: to is below from",
			},
			"path of a detail": {
				input: "boxes:\n  - item: {from: 9, to: 3}\n",
				report: niceyaml.NewError("span is odd", niceyaml.WithDetails(
					niceyaml.NewError("to set here", niceyaml.AtPath(paths.Current().Child("to"))),
				)),
				want:   "2:5: $.boxes[0].item: span is odd",
				detail: "2:25: $.boxes[0].item.to: to set here",
			},
			"join": {
				input:  "boxes:\n  - item: {from: 9, to: 3}\n",
				report: errors.Join(errors.New("from is odd"), errors.New("to is odd")),
				want: stringtest.JoinLF(
					"2:5: $.boxes[0].item: from is odd",
					"2:5: $.boxes[0].item: to is odd",
				),
			},
			// The position counts the lines of the text of the value, so
			// the error binds at the value in its place.
			"position an unmarshaler built from its node": {
				input: "boxes:\n  - name: a\n  - placed: [a, b]\n",
				want:  "3:3: $.boxes[1]: unmarshaler rejected the value",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				_, err := yamltest.FirstDocument(t, tc.input).Decode[struct {
					Boxes []box `yaml:"boxes"`
				}](context.WithValue(t.Context(), reportKey{}, tc.report), boxes)
				require.EqualError(t, err, tc.want)
				require.ErrorIs(t, err, niceyaml.ErrDecode)

				if tc.detail == "" {
					return
				}

				var srcErr *niceyaml.SourceError

				require.ErrorAs(t, err, &srcErr)
				require.Len(t, srcErr.Details(), 1)
				assert.EqualError(t, srcErr.Details()[0], tc.detail)
			})
		}

		// The decode of the value recovers the panic at the first token
		// of the text, and the decode of the document binds it at the
		// value in its place.
		t.Run("panic", func(t *testing.T) {
			t.Parallel()

			_, err := yamltest.FirstDocument(t, "boxes:\n  - name: a\n  - broken: 1\n").Decode[struct {
				Boxes []box `yaml:"boxes"`
			}](t.Context(), boxes)
			require.EqualError(t, err, "3:3: $.boxes[1]: decoder panicked: unmarshaler rejected the value")
			require.NotErrorIs(t, err, niceyaml.ErrDecode)

			value, _ := requirePanic(t, err)
			assert.Equal(t, errUnmarshal, value)
		})
	})

	// The error reaches the unmarshaler above the value inside a wrapper
	// that matches what the error matches, and the error that unmarshaler
	// returns binds at its own value.
	t.Run("an unmarshaler above the value wraps the error of the function", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			is    error
			input string
			want  string
		}{
			"error of the function": {
				input: "main:\n  level: mid\n",
				is:    errUnknownLevel,
				want:  `1:1: $.main: holder: unknown level "mid"`,
			},
			"rejection of the decode": {
				input: "main:\n  level: [a]\n",
				is:    niceyaml.ErrDecode,
				want:  "1:1: $.main: holder: expected string, got sequence",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				_, err := yamltest.FirstDocument(t, tc.input).Decode[map[string]levelHolder](t.Context(), levels)
				require.EqualError(t, err, tc.want)
				require.ErrorIs(t, err, tc.is)
			})
		}
	})

	// No value below the node reports the error, so the rejection of a
	// scalar points at the node, and that of a sequence gains no location,
	// as the error of any unmarshaler does there.
	t.Run("the error of the function for the node the decode reads", func(t *testing.T) {
		t.Parallel()

		cidr := niceyaml.WithCustomUnmarshaler(decodeCIDR)

		_, err := yamltest.FirstDocument(t, "10.0.0.0/33\n").Decode[net.IPNet](t.Context(), cidr)
		require.EqualError(t, err, "1:1: $: invalid CIDR address: 10.0.0.0/33")

		_, err = yamltest.FirstDocument(t, "[1, 2]\n").Decode[net.IPNet](t.Context(), cidr)
		require.EqualError(t, err, "expected string, got sequence")

		_, err = yamltest.FirstDocument(t, "from: nine\nto: 3\n").Decode[plainSpan](
			t.Context(), niceyaml.WithCustomUnmarshaler(decodeSpan),
		)
		require.EqualError(t, err, "1:7: $.from: expected integer, got string")
	})

	t.Run("the decode of a value below a scoped node binds in the document", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, "name: api\nnets:\n  - 10.0.0.0/8\n  - {a: 1}\n")

		_, err := doc.DecodeAt[[]net.IPNet](
			t.Context(), paths.Current().Child("nets"), niceyaml.WithCustomUnmarshaler(decodeCIDR),
		)
		require.EqualError(t, err, "4:3: $.nets[1]: expected string, got mapping")
	})

	t.Run("the decode of a value allows the duplicate keys its source allows", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, "span: {from: 1, from: 2, to: 3}\n", niceyaml.WithAllowDuplicateKeys(true))

		got, err := doc.Decode[map[string]plainSpan](t.Context(), niceyaml.WithCustomUnmarshaler(decodeSpan))
		require.NoError(t, err)
		assert.Equal(t, map[string]plainSpan{"span": {From: 2, To: 3}}, got)
	})

	t.Run("the decode takes a pointer alone", func(t *testing.T) {
		t.Parallel()

		byValue := niceyaml.WithCustomUnmarshaler(func(_ context.Context, _ *plainLevel, decode func(any) error) error {
			var name string

			return decode(name)
		})

		_, err := yamltest.FirstDocument(t, "level: high\n").Decode[map[string]plainLevel](t.Context(), byValue)
		require.EqualError(t, err, "1:8: $.level: decode target is not a non-nil pointer: got string")
		require.ErrorIs(t, err, niceyaml.ErrDecodeTarget)
	})

	// A decode into the type of the function would call the function for
	// the same value again, and so on without end.
	t.Run("the decode refuses the type its function decodes", func(t *testing.T) {
		t.Parallel()

		recursive := niceyaml.WithCustomUnmarshaler(
			func(_ context.Context, l *plainLevel, decode func(any) error) error {
				return decode(l)
			},
		)

		_, err := yamltest.FirstDocument(t, "level: 2\n").Decode[map[string]plainLevel](t.Context(), recursive)
		require.EqualError(
			t, err, "1:8: $.level: decode target is the type the function decodes: got *niceyaml_test.plainLevel",
		)
		require.ErrorIs(t, err, niceyaml.ErrDecode)
	})

	// The go-yaml decoder returns the type error it finds in the error of
	// a struct field in place of that error.
	t.Run("an error that wraps a go-yaml type error keeps its text", func(t *testing.T) {
		t.Parallel()

		counted := niceyaml.WithCustomUnmarshaler(func(_ context.Context, _ *plainLevel, decode func(any) error) error {
			var text yaml.RawMessage

			err := decode(&text)
			if err != nil {
				return err
			}

			var count int

			err = yaml.Unmarshal(text, &count)
			if err != nil {
				return fmt.Errorf("%w: %w", errUnknownLevel, err)
			}

			return nil
		})

		_, err := yamltest.FirstDocument(t, "name: api\nlevel: high\n").Decode[struct {
			Name  string     `yaml:"name"`
			Level plainLevel `yaml:"level"`
		}](t.Context(), counted)
		require.ErrorIs(t, err, errUnknownLevel)

		var typeErr *yaml.TypeError

		require.ErrorAs(t, err, &typeErr)
		require.EqualError(t, err, "2:8: $.level: unknown level: "+typeErr.Error())
	})

	t.Run("a null leaves a pointer nil without a call", func(t *testing.T) {
		t.Parallel()

		calls := 0
		count := niceyaml.WithCustomUnmarshaler(func(context.Context, *plainLevel, func(any) error) error {
			calls++

			return nil
		})

		got, err := yamltest.FirstDocument(t, "level: null\n").Decode[struct {
			Level *plainLevel `yaml:"level"`
		}](t.Context(), count)
		require.NoError(t, err)
		assert.Nil(t, got.Level)
		assert.Zero(t, calls)
	})

	t.Run("the function decodes ahead of a method of the type", func(t *testing.T) {
		t.Parallel()

		byFunction := niceyaml.WithCustomUnmarshaler(func(_ context.Context, l *methodLevel, _ func(any) error) error {
			*l = 7

			return nil
		})

		doc := yamltest.FirstDocument(t, "level: high\n")

		got, err := doc.Decode[map[string]methodLevel](t.Context(), byFunction)
		require.NoError(t, err)
		assert.Equal(t, map[string]methodLevel{"level": 7}, got)

		_, err = doc.Decode[map[string]methodLevel](t.Context())
		require.EqualError(t, err, "1:8: $.level: the decoder called the method")
	})

	t.Run("the error of the function binds at the value", func(t *testing.T) {
		t.Parallel()

		_, err := yamltest.FirstDocument(t, "name: api\nlevel: mid\n").Decode[struct {
			Name  string     `yaml:"name"`
			Level plainLevel `yaml:"level"`
		}](t.Context(), levels)
		require.EqualError(t, err, `2:8: $.level: unknown level "mid"`)
		require.ErrorIs(t, err, errUnknownLevel)
		require.ErrorIs(t, err, niceyaml.ErrDecode)
	})

	t.Run("a nil function names no type", func(t *testing.T) {
		t.Parallel()

		got, err := yamltest.FirstDocument(t, "level: 2\n").Decode[map[string]plainLevel](
			t.Context(), niceyaml.WithCustomUnmarshaler[plainLevel](nil),
		)
		require.NoError(t, err)
		assert.Equal(t, map[string]plainLevel{"level": levelHigh}, got)
	})

	t.Run("a pointer type names no type", func(t *testing.T) {
		t.Parallel()

		// The decoder decodes the value a pointer points to, so no value
		// of the document reaches a function for the pointer.
		pointers := niceyaml.WithCustomUnmarshaler(func(context.Context, **plainLevel, func(any) error) error {
			return errors.New("the decoder called the function")
		})

		got, err := yamltest.FirstDocument(t, "level: 2\n").Decode[map[string]*plainLevel](t.Context(), pointers)
		require.NoError(t, err)

		high := levelHigh

		assert.Equal(t, map[string]*plainLevel{"level": &high}, got)
	})

	t.Run("the option reaches the decode that gets it alone", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, "level: high\n")

		_, err := doc.Decode[map[string]plainLevel](t.Context(), levels)
		require.NoError(t, err)

		_, err = doc.Decode[map[string]plainLevel](t.Context())
		require.EqualError(t, err, "1:8: $.level: expected integer, got string")
	})

	t.Run("a validator decodes with the options it gives its own decode", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, "level: high\n")

		// The validator decodes the node it gets, and that decode takes
		// the options the validator passes and no option of the decode
		// that runs the validator.
		decodes := func(opts ...niceyaml.DecodeOption) niceyaml.DecodeOption {
			return niceyaml.WithValidator(niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
				_, err := n.Decode[map[string]plainLevel](ctx, opts...)

				return err
			}))
		}

		_, err := doc.Decode[map[string]plainLevel](t.Context(), levels, decodes(levels))
		require.NoError(t, err)

		_, err = doc.Decode[map[string]plainLevel](t.Context(), levels, decodes())
		require.EqualError(t, err, "1:8: $.level: expected integer, got string")
	})
}

// jsonSize has an UnmarshalJSON method that reads a size such as "4k",
// which its field does not mirror.
type jsonSize struct {
	Bytes int `yaml:"bytes"`
}

func (s *jsonSize) UnmarshalJSON(data []byte) error {
	var text string

	err := json.Unmarshal(data, &text)
	if err != nil {
		return fmt.Errorf("size: %w", err)
	}

	kilobytes, err := strconv.Atoi(strings.TrimSuffix(text, "k"))
	if err != nil {
		return fmt.Errorf("size: %w", err)
	}

	s.Bytes = kilobytes * 1024

	return nil
}

// yamlSize has an UnmarshalYAML method beside an UnmarshalJSON method
// that reports an error whenever the decoder calls it.
type yamlSize struct {
	Bytes int
}

func (s *yamlSize) UnmarshalYAML([]byte) error {
	s.Bytes = 1

	return nil
}

func (*yamlSize) UnmarshalJSON([]byte) error {
	return errors.New("the decoder called UnmarshalJSON")
}

func TestWithJSONUnmarshalers(t *testing.T) {
	t.Parallel()

	type config struct {
		Size jsonSize `yaml:"size"`
	}

	tcs := map[string]struct {
		input string
		err   string
		opts  []niceyaml.DecodeOption
		want  int
	}{
		"decodes through the method": {
			input: "size: 4k\n",
			opts:  []niceyaml.DecodeOption{niceyaml.WithJSONUnmarshalers(true)},
			want:  4096,
		},
		"reads the fields by default": {
			input: "size: {bytes: 3}\n",
			want:  3,
		},
		"a later false turns the option off": {
			input: "size: {bytes: 3}\n",
			opts: []niceyaml.DecodeOption{
				niceyaml.WithJSONUnmarshalers(true),
				niceyaml.WithJSONUnmarshalers(false),
			},
			want: 3,
		},
		"the error of the method binds at the value": {
			input: "size: big\n",
			opts:  []niceyaml.DecodeOption{niceyaml.WithJSONUnmarshalers(true)},
			err:   `1:7: $.size: size: strconv.Atoi: parsing "big": invalid syntax`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := yamltest.FirstDocument(t, tc.input).Decode[config](t.Context(), tc.opts...)
			if tc.err != "" {
				require.EqualError(t, err, tc.err)
				require.ErrorIs(t, err, niceyaml.ErrDecode)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, got.Size.Bytes)
		})
	}

	t.Run("a type with an UnmarshalYAML method keeps it", func(t *testing.T) {
		t.Parallel()

		got, err := yamltest.FirstDocument(t, "size: 4k\n").Decode[map[string]yamlSize](
			t.Context(), niceyaml.WithJSONUnmarshalers(true),
		)
		require.NoError(t, err)
		assert.Equal(t, map[string]yamlSize{"size": {Bytes: 1}}, got)
	})
}

func TestWithYAMLOrderedMaps(t *testing.T) {
	t.Parallel()

	input := "b: 1\na: {d: 2, c: 3}\n"

	tcs := map[string]struct {
		want any
		opts []niceyaml.DecodeOption
	}{
		"a mapping decodes into a map by default": {
			want: map[string]any{
				"b": uint64(1),
				"a": map[string]any{"d": uint64(2), "c": uint64(3)},
			},
		},
		"a mapping keeps the order of the document": {
			opts: []niceyaml.DecodeOption{niceyaml.WithYAMLOrderedMaps(true)},
			want: yaml.MapSlice{
				{Key: "b", Value: uint64(1)},
				{Key: "a", Value: yaml.MapSlice{
					{Key: "d", Value: uint64(2)},
					{Key: "c", Value: uint64(3)},
				}},
			},
		},
		"a later false turns the option off": {
			opts: []niceyaml.DecodeOption{
				niceyaml.WithYAMLOrderedMaps(true),
				niceyaml.WithYAMLOrderedMaps(false),
			},
			want: map[string]any{
				"b": uint64(1),
				"a": map[string]any{"d": uint64(2), "c": uint64(3)},
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := yamltest.FirstDocument(t, input).Decode[any](t.Context(), tc.opts...)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("a typed map decodes as it does without the option", func(t *testing.T) {
		t.Parallel()

		got, err := yamltest.FirstDocument(t, "b: 1\na: 2\n").Decode[map[string]int](
			t.Context(), niceyaml.WithYAMLOrderedMaps(true),
		)
		require.NoError(t, err)
		assert.Equal(t, map[string]int{"b": 1, "a": 2}, got)
	})
}

func TestNode_YAMLComments(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		# The name of the service.
		name: api # short
		items:
		  # The first item.
		  a: {name: x}
	`)

	want := yaml.CommentMap{
		"$.name": {
			yaml.HeadComment(" The name of the service."),
			yaml.LineComment(" short"),
		},
		"$.items.a": {yaml.HeadComment(" The first item.")},
	}

	anchors := stringtest.Input(`
		shared: &shared
		  size: 1 # small
		items:
		  a: *shared
		  b: 2 # two
	`)

	tcs := map[string]struct {
		want  yaml.CommentMap
		input string
		path  paths.Path
	}{
		"document": {
			input: input,
			want:  want,
		},
		"node below the root keeps the paths of the document": {
			input: input,
			path:  paths.Doc().Child("items"),
			want:  yaml.CommentMap{"$.items.a": want["$.items.a"]},
		},
		"node below the root leaves out an anchor outside it": {
			input: anchors,
			path:  paths.Doc().Child("items"),
			want:  yaml.CommentMap{"$.items.b": {yaml.LineComment(" two")}},
		},
		"document without comments": {
			input: "name: api\n",
			want:  yaml.CommentMap{},
		},
		"empty document": {
			input: "",
			want:  yaml.CommentMap{},
		},
		"document of comments": {
			input: "# note\n",
			want:  yaml.CommentMap{},
		},
		"null": {
			input: "~ # none\n",
			want:  yaml.CommentMap{"$": {yaml.LineComment(" none")}},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			n := yamltest.FirstDocument(t, tc.input)
			if tc.path.Len() > 0 {
				n = yamltest.At(t, n, tc.path)
			}

			got, err := n.YAMLComments(t.Context())
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("the encoder writes the comments back", func(t *testing.T) {
		t.Parallel()

		type service struct {
			Items map[string]map[string]string `yaml:"items"`
			Name  string                       `yaml:"name"`
		}

		doc := yamltest.FirstDocument(t, input)

		got, err := doc.Decode[service](t.Context())
		require.NoError(t, err)

		comments, err := doc.YAMLComments(t.Context())
		require.NoError(t, err)

		got.Name = "web"

		out, err := encoder.Marshal(t.Context(), got, encoder.WithYAMLComments(comments))
		require.NoError(t, err)
		assert.Equal(t, stringtest.Input(`
			items:
			  # The first item.
			  a:
			    name: x
			# The name of the service.
			name: web # short
		`)+"\n", string(out))
	})

	t.Run("each call returns a map of its own", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, input)

		first, err := doc.YAMLComments(t.Context())
		require.NoError(t, err)

		clear(first)

		second, err := doc.YAMLComments(t.Context())
		require.NoError(t, err)
		assert.Equal(t, want, second)
	})

	t.Run("one Node serves several calls at once", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, input)

		var wg sync.WaitGroup

		for range 8 {
			wg.Go(func() {
				got, err := doc.YAMLComments(t.Context())
				if assert.NoError(t, err) {
					assert.Equal(t, want, got)
				}
			})
		}

		wg.Wait()
	})
}

func TestNode_YAMLComments_Errors(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		err   error
		input string
	}{
		"document that did not parse": {
			input: "a: 1 # one\nb: [\n",
			err:   niceyaml.ErrSyntax,
		},
		// The decoder collects the comment of a before it rejects b.
		"alias with no anchor": {
			input: "a: 1 # one\nb: *missing\n",
			err:   niceyaml.ErrDecode,
		},
		"document past the alias limit": {
			input: yamltest.AliasLevels(7),
			err:   niceyaml.ErrExcessiveAliasing,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			docs := niceyaml.NewSourceFromString(tc.input).AllDocuments()
			require.Len(t, docs, 1)

			got, err := docs[0].YAMLComments(t.Context())
			require.ErrorIs(t, err, tc.err)
			assert.Nil(t, got)
		})
	}

	t.Run("context that has ended", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		got, err := yamltest.FirstDocument(t, "a: 1 # one\n").YAMLComments(ctx)
		require.ErrorIs(t, err, context.Canceled)
		assert.Nil(t, got)
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
		map:
		  x: 1
		  y: 2
		tagged:
		  !!str k: v
		items:
		  - name: a
		flow: [{a: 1}]
	`)

	tcs := map[string]struct {
		path paths.Path
		want position.Ranges
		is   error
		key  bool
	}{
		"value": {
			path: paths.Current().Child("kind"),
			want: position.Ranges{position.NewRange(position.New(0, 6), position.New(0, 16))},
		},
		"key": {
			path: paths.Current().Child("kind"),
			key:  true,
			want: position.Ranges{position.NewRange(position.New(0, 0), position.New(0, 4))},
		},
		"key of a sequence element is the element": {
			path: paths.Current().Child("list").Index(0),
			key:  true,
			want: position.Ranges{position.NewRange(position.New(8, 4), position.New(8, 5))},
		},
		"mapping with a tagged first key covers the key of its entry": {
			path: paths.Current().Child("tagged"),
			want: position.Ranges{position.NewRange(position.New(12, 0), position.New(12, 6))},
		},
		"value across lines": {
			path: paths.Current().Child("text"),
			want: position.Ranges{
				position.NewRange(position.New(1, 6), position.New(1, 11)),
				position.NewRange(position.New(2, 2), position.New(2, 8)),
			},
		},
		"block scalar covers its indicator": {
			path: paths.Current().Child("block"),
			want: position.Ranges{position.NewRange(position.New(3, 7), position.New(3, 8))},
		},
		"mapping covers the key of its entry": {
			path: paths.Current().Child("map"),
			want: position.Ranges{position.NewRange(position.New(9, 0), position.New(9, 3))},
		},
		"sequence covers the key of its entry": {
			path: paths.Current().Child("list"),
			want: position.Ranges{position.NewRange(position.New(7, 0), position.New(7, 4))},
		},
		"mapping element covers its dash": {
			path: paths.Current().Child("items").Index(0),
			want: position.Ranges{position.NewRange(position.New(15, 2), position.New(15, 3))},
		},
		"key of a mapping element is the element": {
			path: paths.Current().Child("items").Index(0),
			key:  true,
			want: position.Ranges{position.NewRange(position.New(15, 2), position.New(15, 3))},
		},
		"flow mapping in a flow sequence covers its brace": {
			path: paths.Current().Child("flow").Index(0),
			want: position.Ranges{position.NewRange(position.New(16, 7), position.New(16, 8))},
		},
		"block mapping at the root covers its first key": {
			path: paths.Current(),
			want: position.Ranges{position.NewRange(position.New(0, 0), position.New(0, 4))},
		},
		"missing path": {
			path: paths.Current().Child("missing"),
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

	t.Run("value after an escaped scalar", func(t *testing.T) {
		t.Parallel()

		// The lexer drops the code of the escape from the scalar before b,
		// and the range of b still covers the runes of its value.
		dd := yamltest.FirstDocument(t, "m: {a: \"\\u00e9\", b: xx}\n")

		got, err := dd.Ranges(paths.Current().Child("m").Child("b"))
		require.NoError(t, err)
		assert.Equal(t, position.Ranges{position.NewRange(position.New(0, 20), position.New(0, 22))}, got)
	})

	t.Run("empty value", func(t *testing.T) {
		t.Parallel()

		// The parser makes a token for an empty value, which no line
		// holds, so a comment or the spaces after the colon at its
		// column are not its content.
		tcs := map[string]struct {
			input string
			path  paths.Path
		}{
			"nothing after the colon": {input: "a:\nb: 1\n", path: paths.Current().Child("a")},
			"comment after the colon": {input: "a: # c\nb: 1\n", path: paths.Current().Child("a")},
			"spaces after the colon":  {input: "a:   \nb: 1\n", path: paths.Current().Child("a")},
			"comment after the dash":  {input: "- # c\n- 1\n", path: paths.Current().Index(0)},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				got, err := yamltest.FirstDocument(t, tc.input).Ranges(tc.path)
				require.NoError(t, err)
				assert.Nil(t, got)
			})
		}
	})

	t.Run("matches the ranges a bound error highlights", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		for _, path := range []paths.Path{
			paths.Current().Child("kind"),
			paths.Current().Child("text"),
			paths.Current().Child("block"),
			paths.Current().Child("empty"),
			paths.Current().Child("list"),
			paths.Current().Child("map"),
			paths.Current().Child("list").Index(0),
			paths.Current().Child("kind").Key(),
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

		got, err := yamltest.At(t, dd, paths.Current().Child("kind")).Decode[string](t.Context())
		require.NoError(t, err)
		assert.Equal(t, "Deployment", got)
	})

	t.Run("int", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		got, err := yamltest.At(t, dd, paths.Current().Child("version")).Decode[int](t.Context())
		require.NoError(t, err)
		assert.Equal(t, 2, got)
	})

	t.Run("key decodes as the mapping reads it", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			want  any
			input string
		}{
			"plain key": {
				input: "2: x\n",
				want:  uint64(2),
			},
			"quoted key": {
				input: "\"2\": x\n",
				want:  "2",
			},
			"tagged key": {
				input: "!!str 2: x\n",
				want:  "2",
			},
			"explicit tagged key": {
				input: "? !!str 2\n: x\n",
				want:  "2",
			},
			"anchored tagged key": {
				input: "&k !!str 2: x\n",
				want:  "2",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				whole, err := dd.Decode[map[any]any](t.Context())
				require.NoError(t, err)
				require.Contains(t, whole, tc.want)

				got, err := yamltest.At(t, dd, paths.Current().Child("2").Key()).Decode[any](t.Context())
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
			})
		}
	})

	t.Run("bool", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		got, err := yamltest.At(t, dd, paths.Current().Child("enabled")).Decode[bool](t.Context())
		require.NoError(t, err)
		assert.True(t, got)
	})

	t.Run("null decodes to zero value", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		got, err := yamltest.At(t, dd, paths.Current().Child("empty")).Decode[string](t.Context())
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("slice", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		got, err := yamltest.At(t, dd, paths.Current().Child("tags")).Decode[[]string](t.Context())
		require.NoError(t, err)
		assert.Equal(t, []string{"a", "b"}, got)
	})

	t.Run("struct", func(t *testing.T) {
		t.Parallel()

		type meta struct {
			Name string `yaml:"name"`
		}

		dd := yamltest.FirstDocument(t, input)

		got, err := yamltest.At(t, dd, paths.Current().Child("meta")).Decode[meta](t.Context())
		require.NoError(t, err)
		assert.Equal(t, meta{Name: "app"}, got)
	})

	t.Run("key below a comment under an anchor with no value", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "a: &x\n# c\nb: 1\n")

		got, err := yamltest.At(t, dd, paths.Current().Child("b")).Decode[int](t.Context())
		require.NoError(t, err)
		assert.Equal(t, 1, got)
	})

	t.Run("missing path returns ErrNotFound", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		got, err := dd.At(paths.Current().Child("nonexistent"))
		require.ErrorIs(t, err, paths.ErrNotFound)
		require.NotErrorIs(t, err, paths.ErrAlias)
		assert.Nil(t, got)
	})

	t.Run("empty document returns ErrNotFound and ErrNoDocument", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "---\n")

		got, err := dd.At(paths.Current().Child("key"))
		require.ErrorIs(t, err, paths.ErrNotFound)
		require.ErrorIs(t, err, paths.ErrNoDocument)
		assert.Contains(t, err.Error(), "$.key")
		assert.Nil(t, got)
	})

	t.Run("file of whitespace returns ErrNotFound and ErrNoDocument", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "\n")

		got, err := dd.At(paths.Current().Child("key"))
		require.ErrorIs(t, err, paths.ErrNotFound)
		require.ErrorIs(t, err, paths.ErrNoDocument)
		assert.Contains(t, err.Error(), "$.key")
		assert.Nil(t, got)
	})

	t.Run("alias without anchor returns ErrAlias", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "kind: *nope")

		got, err := dd.At(paths.Current().Child("kind"))
		require.ErrorIs(t, err, paths.ErrAlias)
		require.NotErrorIs(t, err, paths.ErrNotFound)
		assert.Contains(t, err.Error(), "*nope")
		assert.Nil(t, got)
	})

	t.Run("wildcard path returns ErrWildcard", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "tags: [a, b]")

		got, err := dd.At(paths.Current().Child("tags").IndexAll())
		require.ErrorIs(t, err, paths.ErrWildcard)
		require.NotErrorIs(t, err, paths.ErrNotFound)
		assert.Nil(t, got)
	})

	t.Run("mapping wildcard path returns ErrWildcard", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "jobs:\n  build: 1\n")

		got, err := dd.At(paths.Current().Child("jobs").ChildAll())
		require.ErrorIs(t, err, paths.ErrWildcard)
		require.NotErrorIs(t, err, paths.ErrNotFound)
		assert.Nil(t, got)
	})

	t.Run("recursive wildcard path returns ErrWildcard", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "jobs:\n  build: 1\n")

		got, err := dd.At(paths.Current().RecursiveAll())
		require.ErrorIs(t, err, paths.ErrWildcard)
		require.NotErrorIs(t, err, paths.ErrNotFound)
		assert.Nil(t, got)
	})

	t.Run("type mismatch returns Error", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		got, err := yamltest.At(t, dd, paths.Current().Child("kind")).Decode[int](t.Context())
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

		got, err := yamltest.At(t, dd, paths.Current().Child("item")).Decode[map[string]any](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"ref": map[string]any{"x": uint64(1)}}, got)

		list, err := yamltest.At(t, dd, paths.Current().Child("list")).Decode[[]map[string]int](t.Context())
		require.NoError(t, err)
		assert.Equal(t, []map[string]int{{"x": 1}}, list)
	})

	t.Run("a failure elsewhere in the document does not break Get", func(t *testing.T) {
		t.Parallel()

		// Resolving the alias under $.sub first decodes the anchors of the
		// document. The undefined alias under $.bad fails to decode, and
		// the value the caller asked for still comes back.
		tcs := map[string]struct {
			input string
		}{
			"failure after the anchor": {
				input: "a: &x 1\nsub: {k: *x}\nbad: *nope\n",
			},
			"failure before the anchor": {
				input: "bad: *nope\na: &x 1\nsub: {k: *x}\n",
			},
			"failure inside another anchor": {
				input: "b: &y [*nope]\na: &x 1\nsub: {k: *x}\n",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				got, err := yamltest.At(t, dd, paths.Current().Child("sub")).Decode[map[string]any](t.Context())
				require.NoError(t, err)
				assert.Equal(t, map[string]any{"k": uint64(1)}, got)
			})
		}
	})

	t.Run("alias resolves to the anchor before it when a later anchor reuses the name", func(t *testing.T) {
		t.Parallel()

		// The alias names the anchor defined last before it, so a path
		// through the alias reads v1, and so does a decode of the node
		// that holds the alias.
		tcs := map[string]struct {
			input string
			path  paths.Path
		}{
			"later anchor after the node": {
				input: "a: &x v1\nm:\n  k: *x\nc: &x v2\n",
				path:  paths.Current().Child("m"),
			},
			"later anchor inside an enclosing mapping": {
				input: "a: &x v1\nm:\n  s:\n    k: *x\n  c: &x v2\n",
				path:  paths.Current().Child("m", "s"),
			},
			"later anchor inside an enclosing anchor": {
				input: "m: &m\n  a: &x v1\n  s:\n    k: *x\n  c: &x v2\n",
				path:  paths.Current().Child("m", "s"),
			},
			"later anchor inside an enclosing merge": {
				input: "a: &x v1\nm:\n  <<:\n    s:\n      k: *x\n    c: &x v2\n",
				path:  paths.Current().Child("m", "s"),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				k, err := yamltest.At(t, dd, tc.path.Child("k")).Decode[string](t.Context())
				require.NoError(t, err)
				require.Equal(t, "v1", k)

				got, err := yamltest.At(t, dd, tc.path).Decode[map[string]any](t.Context())
				require.NoError(t, err)
				assert.Equal(t, map[string]any{"k": "v1"}, got)

				var scoped map[string]any

				err = yamltest.At(t, dd, tc.path).DecodeInto(t.Context(), &scoped)
				require.NoError(t, err)
				assert.Equal(t, map[string]any{"k": "v1"}, scoped)
			})
		}
	})

	t.Run("scoped decode reads the anchors its aliases need", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input string
			path  paths.Path
			want  any
		}{
			"merge key in each list entry": {
				input: "defaults: &d\n  a: 1\nitems:\n  - <<: *d\n    name: x\n  - <<: *d\n    name: y\n",
				path:  paths.Current().Child("items").Index(1),
				want:  map[string]any{"a": uint64(1), "name": "y"},
			},
			"anchor inside another anchor": {
				input: "outer: &o\n  inner: &i 1\nsub: {k: *i}\n",
				path:  paths.Current().Child("sub"),
				want:  map[string]any{"k": uint64(1)},
			},
			"anchor inside an anchor that holds the node": {
				input: "outer: &o\n  inner: &i 1\n  sub: {k: *i, s: *o}\n",
				path:  paths.Current().Child("outer", "sub"),
				want:  map[string]any{"k": uint64(1), "s": nil},
			},
			"anchor that refers to another anchor": {
				input: "a: &a 1\nb: &b [*a]\nsub: {k: *b}\n",
				path:  paths.Current().Child("sub"),
				want:  map[string]any{"k": []any{uint64(1)}},
			},
			"anchor inside the node": {
				input: "a: &x 1\nsub:\n  b: &x 2\n  k: *x\n",
				path:  paths.Current().Child("sub"),
				want:  map[string]any{"b": uint64(2), "k": uint64(2)},
			},
			"merge key that records an anchor again": {
				// The merge under q records the anchor b of m again, so p
				// reads 1, while X read b before the merge, as 2.
				input: stringtest.Input(`
					m: &m
					  x: &b 1
					b: &b 2
					X: &X
					  z: *b
					q:
					  <<: *m
					node:
					  p: *b
					  r: *X
				`),
				path: paths.Current().Child("node"),
				want: map[string]any{"p": uint64(1), "r": map[string]any{"z": uint64(2)}},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				got, err := yamltest.At(t, dd, tc.path).Decode[any](t.Context())
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
			})
		}
	})

	t.Run("alias to an anchor that holds the node reads null", func(t *testing.T) {
		t.Parallel()

		// The decoder reads an alias inside the value of its own anchor as
		// null, as a decode of the whole document does.
		tcs := map[string]struct {
			input string
			path  paths.Path
			want  any
		}{
			"value of the anchor": {
				input: "a: &a\n  - 1\n  - *a\n",
				path:  paths.Current().Child("a"),
				want:  []any{uint64(1), nil},
			},
			"node inside the anchor": {
				input: "a: &a\n  b:\n    c: *a\n",
				path:  paths.Current().Child("a", "b"),
				want:  map[string]any{"c": nil},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				got, err := yamltest.At(t, dd, tc.path).Decode[any](t.Context())
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
			})
		}
	})

	t.Run("alias inside its own anchor writes out as null", func(t *testing.T) {
		t.Parallel()

		// A value that decodes itself from YAML bytes gets the anchor
		// written out, with null for the alias inside it.
		tcs := map[string]struct {
			input string
			want  string
		}{
			"flow mapping": {
				input: "a: &a {k: *a}\nkind: {x: *a}\n",
				want:  "{k: null}",
			},
			"flow sequence": {
				input: "a: &a [1, *a]\nkind: {x: *a}\n",
				want:  "[1, null]",
			},
			"block sequence": {
				input: "a: &a\n  - 1\n  - *a\nkind: {x: *a}\n",
				want:  "- 1\n- null",
			},
			"anchor name used again later": {
				input: "a: &a {k: *a}\nkind: {x: *a}\nb: &a 1\n",
				want:  "{k: null}",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				got, err := yamltest.At(t, dd, paths.Current().Child("kind")).Decode[map[string]rawText](t.Context())
				require.NoError(t, err)
				assert.Equal(t, tc.want, got["x"].text)
			})
		}
	})

	t.Run("alias to a later anchor stays an error", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, stringtest.Input(`
			item:
			  ref: *b
			base: &b
			  x: 1
		`))

		// The document defines the anchor after the alias, so the value's
		// own decode reports the alias error, bound to the source.
		_, err := yamltest.At(t, dd, paths.Current().Child("item")).Decode[map[string]any](t.Context())
		require.Error(t, err)

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Contains(t, err.Error(), "could not find alias")
	})
}

// rawText is a value that decodes itself from the text go-yaml hands it.
type rawText struct {
	text string
}

func (r *rawText) UnmarshalYAML(data []byte) error {
	r.text = string(data)

	return nil
}

// echoingUnmarshaler reports errUnmarshal with the text go-yaml hands it,
// so the message of the decode quotes that text.
type echoingUnmarshaler struct{}

func (*echoingUnmarshaler) UnmarshalYAML(data []byte) error {
	return fmt.Errorf("%w: %q", errUnmarshal, data)
}

func TestDocument_Decode_ReusedAnchorNames(t *testing.T) {
	t.Parallel()

	t.Run("alias reads the anchor it refers to", func(t *testing.T) {
		t.Parallel()

		// Each alias reads the anchor of its name defined last before it,
		// as a path through the alias resolves, even when the node defines
		// the name again after the alias.
		tcs := map[string]struct {
			input string
			path  paths.Path
			want  any
		}{
			"sequence redefines the name after the alias": {
				input: "x0: &x 1\nl:\n  - *x\n  - &x 2\n",
				path:  paths.Current().Child("l"),
				want:  []any{uint64(1), uint64(2)},
			},
			"mapping redefines the name after the alias": {
				input: "b: &x 2\nd:\n  e: *x\n  f: &x 3\n",
				path:  paths.Current().Child("d"),
				want:  map[string]any{"e": uint64(2), "f": uint64(3)},
			},
			"aliases before and after the redefinition": {
				input: "a: &x 1\nb:\n  c: *x\n  d: &x 2\n  e: *x\n",
				path:  paths.Current().Child("b"),
				want:  map[string]any{"c": uint64(1), "d": uint64(2), "e": uint64(2)},
			},
			"sequence aliases before and after the redefinition": {
				input: "a: &x 1\nb:\n  - *x\n  - &x 2\n  - *x\n",
				path:  paths.Current().Child("b"),
				want:  []any{uint64(1), uint64(2), uint64(2)},
			},
			"each element reads the anchor before it": {
				input: "items:\n  - &x {a: 1}\n  - {b: *x}\n  - &x {a: 2}\n  - {b: *x}\n",
				path:  paths.Current().Child("items").Index(1),
				want:  map[string]any{"b": map[string]any{"a": uint64(1)}},
			},
			"merge key that records an anchor again": {
				input: "m: &m {k: &b 1}\nb: &b 2\nq: {<<: *m}\nnode:\n  p: *b\n  r: *b\n",
				path:  paths.Current().Child("node"),
				want:  map[string]any{"p": uint64(1), "r": uint64(1)},
			},
			"whole document with an alias to a sibling in an anchor": {
				input: "outer: &o\n  one: &y 1\n  two: &x {d: *y}\nuse:\n  v: *x\n",
				path:  paths.Current(),
				want: map[string]any{
					"outer": map[string]any{
						"one": uint64(1),
						"two": map[string]any{"d": uint64(1)},
					},
					"use": map[string]any{"v": map[string]any{"d": uint64(1)}},
				},
			},
			"node with an alias to a sibling in an anchor": {
				input: "outer: &o\n  one: &y 1\n  two: &x {d: *y}\nuse:\n  v: *x\n",
				path:  paths.Current().Child("use"),
				want:  map[string]any{"v": map[string]any{"d": uint64(1)}},
			},
			"node with an alias to a redefined sibling in an anchor": {
				input: "d: &x\n  &y b: &x {q: *x}\n  a: &x\n    d: *y\nc:\n  <<: *x\n  b: *x\na:\n  a: [*y, &y 5, *x]\n",
				path:  paths.Current().Child("a"),
				want: map[string]any{
					"a": []any{"b", uint64(5), map[string]any{"d": "b"}},
				},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				got, err := yamltest.At(t, dd, tc.path).Decode[any](t.Context())
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)

				var scoped any

				err = yamltest.At(t, dd, tc.path).DecodeInto(t.Context(), &scoped)
				require.NoError(t, err)
				assert.Equal(t, tc.want, scoped)
			})
		}
	})

	t.Run("typed decode reads the anchor the alias refers to", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "x0: &x 1\nl:\n  - *x\n  - &x 2\n")

		list, err := yamltest.At(t, dd, paths.Current().Child("l")).Decode[[]int](t.Context())
		require.NoError(t, err)
		assert.Equal(t, []int{1, 2}, list)

		type inner struct {
			E int `yaml:"e"`
			F int `yaml:"f"`
		}

		type outer struct {
			D inner `yaml:"d"`
		}

		// The struct has no field for b, so the decode reads the anchor of
		// b only for the alias under d.
		dd = yamltest.FirstDocument(t, "b: &x 2\nd:\n  e: *x\n  f: &x 3\n")

		got, err := dd.Decode[outer](t.Context())
		require.NoError(t, err)
		assert.Equal(t, outer{D: inner{E: 2, F: 3}}, got)
	})

	t.Run("typed decode of an anchor reads the anchors of its definition", func(t *testing.T) {
		t.Parallel()

		type base struct {
			Image string `yaml:"image"`
		}

		type svc struct {
			Web     base   `yaml:"web"`
			Sidecar string `yaml:"sidecar"`
		}

		// The alias inside base reads the first img, while the sidecar
		// reads the second.
		dd := yamltest.FirstDocument(t, stringtest.Input(`
			image: &img nginx:1.0
			base: &base
			  image: *img
			image2: &img nginx:2.0
			svc:
			  web: *base
			  sidecar: *img
		`))

		got, err := yamltest.At(t, dd, paths.Current().Child("svc")).Decode[svc](t.Context())
		require.NoError(t, err)
		assert.Equal(t, svc{Web: base{Image: "nginx:1.0"}, Sidecar: "nginx:2.0"}, got)
	})

	t.Run("failure in an anchor the node reads fails the decode", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input string
			err   string
		}{
			"anchor the node reads": {
				input: "a: &x 1\nb: &x !!bool nope\nc:\n  d: *x\n",
				err:   `2:14: $.b: cannot convert "nope" to boolean`,
			},
			"anchor another anchor reads": {
				input: "a: &x 1\nm: &m {k: *x}\nb: &x !!bool nope\nc:\n  d: *x\n  e: *m\n",
				err:   `3:14: $.b: cannot convert "nope" to boolean`,
			},
			"anchor read before a later redefinition": {
				input: "a: &x 1\nb: &x !!bool nope\nm: &m {k: *x}\nz: &x 2\nc:\n  e: *m\n  f: *x\n",
				err:   `2:14: $.b: cannot convert "nope" to boolean`,
			},
			"anchor inside a failing anchor": {
				input: "q: &i 0\no: &o {bad: !!bool nope, i: &i 1}\nc:\n  d: *i\n",
				err:   `2:20: $.o.bad: cannot convert "nope" to boolean`,
			},
			"anchor with an alias to nothing": {
				input: "a: &x 1\nb: &x [*nope]\nc:\n  d: *x\n",
				err:   `2:8: $.b[0]: could not find alias "nope"`,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				_, err := dd.Decode[any](t.Context())
				require.ErrorIs(t, err, niceyaml.ErrDecode)

				_, err = yamltest.At(t, dd, paths.Current().Child("c")).Decode[any](t.Context())
				require.EqualError(t, err, tc.err)
				require.ErrorIs(t, err, niceyaml.ErrDecode)
			})
		}
	})

	t.Run("quoted name spelled like a renamed anchor", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input string
			want  any
			err   string
		}{
			"alias reads the anchor it refers to": {
				input: "b: &x {q: 2}\na: &\"x [1]\" {p: 1}\ne: *x\nc: &x {q: 3}\n",
				want: map[string]any{
					"a": map[string]any{"p": uint64(1)},
					"b": map[string]any{"q": uint64(2)},
					"c": map[string]any{"q": uint64(3)},
					"e": map[string]any{"q": uint64(2)},
				},
			},
			"error keeps the name the alias spells": {
				input: "a: &\"x [1]\" 1\nb: &x 2\nc: &x 3\nd: *\"x [1]\"\n",
				err:   `4:4: $.d: could not find alias "\"x [1]\""`,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				got, err := dd.Decode[any](t.Context())
				if tc.err != "" {
					require.EqualError(t, err, tc.err)

					return
				}

				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
			})
		}
	})

	t.Run("text spelled like a renamed anchor", func(t *testing.T) {
		t.Parallel()

		type config struct {
			C echoingUnmarshaler `yaml:"c"`
			A int                `yaml:"a"`
			B int                `yaml:"b"`
			D time.Duration      `yaml:"d"`
		}

		// The two anchors named x get new names, and each message quotes
		// the text as the document spells it. The error of a value that
		// decodes itself keeps that text at the path of the value.
		tcs := map[string]struct {
			input string
			err   string
		}{
			"unknown key": {
				input: "a: &x 1\nb: &x 2\n\"x [1]\": 3\n",
				err:   `3:1: $.'x [1]'~: unknown field "x [1]"`,
			},
			"invalid duration": {
				input: "a: &x 1\nb: &x 2\nd: x [2]\n",
				err:   `3:4: $.d: time: invalid duration "x [2]"`,
			},
			"escaped value": {
				input: "a: &x 1\nb: &x 2\nd: \"x\\x20[1]\"\n",
				err:   `3:4: $.d: time: invalid duration "x [1]"`,
			},
			"folded value": {
				input: "a: &x 1\nb: &x 2\nd: x\n  [1]\n",
				err:   `3:4: $.d: time: invalid duration "x [1]"`,
			},
			"unmarshaler text": {
				input: "a: &x 1\nb: &x 2\nc: x [1]\n",
				err:   `3:4: $.c: unmarshaler rejected the value: "x [1]\n"`,
			},
			"unmarshaler text across tokens": {
				input: "a: &x 1\nb: &x 2\nc: !x [1]\n",
				err:   `3:1: $.c: unmarshaler rejected the value: "!x [1]\n"`,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				_, err := dd.Decode[config](t.Context(), niceyaml.WithDisallowUnknownFields(true))
				require.EqualError(t, err, tc.err)
			})
		}
	})

	t.Run("alias beside text spelled like a renamed anchor", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "a: &x 1\nb: &x 2\nc: x [1]\nd: *x\n")

		got, err := dd.Decode[any](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"a": uint64(1), "b": uint64(2), "c": "x [1]", "d": uint64(2)}, got)
	})

	t.Run("later document after a folded node", func(t *testing.T) {
		t.Parallel()

		// The comment after the "..." marker parses to a node of its own,
		// so the second document is the third node of the file.
		input := "a: 1\n...\n# note\n---\na: &x 1\nb:\n  - *x\n  - &x 2\n  - *x\n"

		docs := niceyaml.NewSourceFromString(input).Documents()
		require.Len(t, docs, 2)

		got, err := yamltest.At(t, docs[1], paths.Current().Child("b")).Decode[[]int](t.Context())
		require.NoError(t, err)
		assert.Equal(t, []int{1, 2, 2}, got)
	})

	t.Run("failure in an anchor a later one hides decodes", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "a: &x !!bool nope\nb: &x 1\nc:\n  d: *x\n")

		got, err := yamltest.At(t, dd, paths.Current().Child("c")).Decode[any](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"d": uint64(1)}, got)
	})

	t.Run("unmarshaler text spells the anchors of the document", func(t *testing.T) {
		t.Parallel()

		type wrapper struct {
			T rawText `yaml:"t"`
		}

		input := "a: &x 1\nt:\n  p: &x 2\n  q: *x\n"

		var want wrapper

		require.NoError(t, yaml.Unmarshal([]byte(input), &want))

		got, err := yamltest.FirstDocument(t, input).Decode[wrapper](t.Context())
		require.NoError(t, err)
		assert.Equal(t, want.T.text, got.T.text)
		assert.Contains(t, got.T.text, "&x 2")
	})

	t.Run("node spells an alias to a renamed anchor with its new name", func(t *testing.T) {
		t.Parallel()

		type wrapper struct {
			C ast.Node `yaml:"c"`
			D ast.Node `yaml:"d"`
		}

		dd := yamltest.FirstDocument(t, "a: &x 1\nb: &x 2\nc: [*x, 3]\nd: {p: &x 4, q: *x}\n")

		got, err := dd.Decode[wrapper](t.Context())
		require.NoError(t, err)
		require.NotNil(t, got.C)
		require.NotNil(t, got.D)
		assert.Equal(t, "[*x [2], 3]", got.C.String())
		assert.Equal(t, "{p: &x 4, q: *x [3]}", got.D.String())
	})

	t.Run("new names skip the counts the document spells", func(t *testing.T) {
		t.Parallel()

		type wrapper struct {
			C ast.Node `yaml:"c"`
		}

		tcs := map[string]struct {
			input string
			want  string
		}{
			"nothing spelled": {
				input: "a: &x 1\nb: &x 2\nc: [*x]\n",
				want:  "[*x [2]]",
			},
			"several counts spelled": {
				input: "a: &\"x [1]\" 1\nb: &x 2 # x [2]\nd: a x [3] b\ne: &x 4\nc: [*x]\n",
				want:  "[*x [5]]",
			},
			"name and count in separate values": {
				input: "a: &x 1\nb: &x 2\nd: \"z [x\"\ne: \" [2] z\"\nc: [*x]\n",
				want:  "[*x [2]]",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				got, err := yamltest.FirstDocument(t, tc.input).Decode[wrapper](t.Context())
				require.NoError(t, err)
				require.NotNil(t, got.C)
				assert.Equal(t, tc.want, got.C.String())
			})
		}
	})

	t.Run("new names skip the counts a reference document spells", func(t *testing.T) {
		t.Parallel()

		type wrapper struct {
			C ast.Node `yaml:"c"`
		}

		// The one anchor named x gets a new name, since the source has a
		// reference document.
		tcs := map[string]struct {
			reference string
			want      string
		}{
			"nothing spelled": {
				reference: "r: 1\n",
				want:      "[*x [1]]",
			},
			"count of another name": {
				reference: "r: y [1]\n",
				want:      "[*x [1]]",
			},
			"key": {
				reference: "\"x [1]\": 1\n",
				want:      "[*x [2]]",
			},
			"escaped value": {
				reference: "r: \"x\\x20[1]\"\n",
				want:      "[*x [2]]",
			},
			"tag before a flow sequence": {
				reference: "r: !x [1]\n",
				want:      "[*x [2]]",
			},
			"second reference": {
				reference: "r: x [1]\n---\nr: x [2] # x [3]\n",
				want:      "[*x [4]]",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, "a: &x 1\nc: [*x]\n",
					niceyaml.WithReferences(niceyaml.NewSourceFromString(tc.reference)))

				got, err := dd.Decode[wrapper](t.Context())
				require.NoError(t, err)
				require.NotNil(t, got.C)
				assert.Equal(t, tc.want, got.C.String())
			})
		}
	})

	t.Run("text of a reference document spelled like a renamed anchor", func(t *testing.T) {
		t.Parallel()

		type config struct {
			V struct {
				A int `yaml:"a"`
			} `yaml:"v"`
			X int `yaml:"x"`
		}

		// The anchor named x gets a new name, and the message quotes the
		// key as the reference document spells it.
		dd := yamltest.FirstDocument(t, "x: &x 9\nv: *r\n",
			niceyaml.WithReferences(niceyaml.NewSourceFromString("r: &r {\"x [1]\": 2}\n")))

		_, err := dd.Decode[config](t.Context(), niceyaml.WithDisallowUnknownFields(true))
		require.EqualError(t, err, `2:4: unknown field "x [1]"`)
	})

	t.Run("documents of one source decode at once", func(t *testing.T) {
		t.Parallel()

		type wrapper struct {
			T rawText        `yaml:"t"`
			R int            `yaml:"r"`
			S map[string]any `yaml:"s"`
		}

		// The alias inside its own anchor makes each document write a null
		// into the tokens of the second parse, which the go-yaml formatter
		// reads for rawText in every other document.
		var sb strings.Builder

		for i := range 8 {
			fmt.Fprintf(&sb, "---\na: &x %d\nt:\n  p: &x 2\n  q: *x\nr: *x\ns: &s {s: *s}\n", i)
		}

		docs := niceyaml.NewSourceFromString(sb.String()).Documents()

		var wg sync.WaitGroup

		for range 4 {
			for _, doc := range docs {
				scope := yamltest.At(t, doc, paths.Current().Child("t"))

				wg.Go(func() {
					got, err := doc.Decode[wrapper](t.Context())
					if assert.NoError(t, err) {
						assert.Equal(t, 2, got.R)
						assert.Contains(t, got.T.text, "&x 2")
						assert.Equal(t, map[string]any{"s": nil}, got.S)
					}

					scoped, err := scope.Decode[any](t.Context())
					if assert.NoError(t, err) {
						assert.Equal(t, map[string]any{"p": uint64(2), "q": uint64(2)}, scoped)
					}
				})
			}
		}

		wg.Wait()
	})

	t.Run("error names an anchor as the document does", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "a: &x 1\nb: &x {<<: *x}\n")

		_, err := dd.Decode[any](t.Context())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "alias name x")
		assert.NotContains(t, err.Error(), "[")
	})

	t.Run("error binds at the source", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "a: &x 1\nb: &x 2\nc: {k: !!bool nope}\n")

		_, err := yamltest.At(t, dd, paths.Current().Child("c")).Decode[any](t.Context())
		require.EqualError(t, err, `3:15: $.c.k: cannot convert "nope" to boolean`)
		require.ErrorIs(t, err, niceyaml.ErrDecode)

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)

		rng, ok := bound.Range()
		require.True(t, ok)
		assert.Equal(t, 2, rng.Start.Line)
	})
}

func TestDocument_Decode_ForwardAlias(t *testing.T) {
	t.Parallel()

	// An alias before every anchor of its name in the document reads the
	// anchor of a reference document, however many anchors of the name
	// follow the alias.
	refs := niceyaml.WithReferences(niceyaml.NewSourceFromString("defaults: &defaults {port: 80}\nx: &x 1\n"))

	tcs := map[string]struct {
		input  string
		path   paths.Path
		want   any
		scoped any
		err    string
	}{
		"merge key before the anchor": {
			input: "service:\n  <<: *defaults\ndefaults: &defaults\n  port: 8080\n",
			path:  paths.Current().Child("service"),
			want: map[string]any{
				"service":  map[string]any{"port": uint64(80)},
				"defaults": map[string]any{"port": uint64(8080)},
			},
			scoped: map[string]any{"port": uint64(80)},
			err:    "2:7: decoder rejected the value: cannot find anchor by alias name defaults",
		},
		"merge key before two anchors": {
			input: "service:\n  <<: *defaults\ndefaults: &defaults\n  port: 8080\nother: &defaults\n  port: 9090\n",
			path:  paths.Current().Child("service"),
			want: map[string]any{
				"service":  map[string]any{"port": uint64(80)},
				"defaults": map[string]any{"port": uint64(8080)},
				"other":    map[string]any{"port": uint64(9090)},
			},
			scoped: map[string]any{"port": uint64(80)},
			err:    "2:7: decoder rejected the value: cannot find anchor by alias name defaults",
		},
		"sequence alias before the anchor": {
			input:  "p: [*x, &x 2]\n",
			path:   paths.Current().Child("p"),
			want:   map[string]any{"p": []any{uint64(1), uint64(2)}},
			scoped: []any{uint64(1), uint64(2)},
			err:    `1:5: $.p[0]: could not find alias "x"`,
		},
		"mapping alias before the anchor": {
			input:  "p:\n  a: *x\n  b: &x 2\n",
			path:   paths.Current().Child("p"),
			want:   map[string]any{"p": map[string]any{"a": uint64(1), "b": uint64(2)}},
			scoped: map[string]any{"a": uint64(1), "b": uint64(2)},
			err:    `2:6: $.p.a: could not find alias "x"`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dd := yamltest.FirstDocument(t, tc.input, refs)

			got, err := dd.Decode[any](t.Context())
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)

			scoped, err := yamltest.At(t, dd, tc.path).Decode[any](t.Context())
			require.NoError(t, err)
			assert.Equal(t, tc.scoped, scoped)

			// Without the reference document, the alias finds no anchor.
			bare := yamltest.FirstDocument(t, tc.input)

			_, err = bare.Decode[any](t.Context())
			require.EqualError(t, err, tc.err)

			_, err = yamltest.At(t, bare, tc.path).Decode[any](t.Context())
			require.EqualError(t, err, tc.err)
		})
	}
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

	t.Run("leaves the value as it is for a tagged null", func(t *testing.T) {
		t.Parallel()

		// Each input tags a value it leaves out, which the go-yaml decoder
		// reads as null.
		tcs := map[string]struct {
			input string
		}{
			"map tag after a header": {input: "--- !!map\n"},
			"seq tag":                {input: "!!seq\n"},
			"null tag":               {input: "!!null\n"},
			"local tag":              {input: "!custom\n"},
			"anchored seq tag":       {input: "&a !!seq\n"},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				slice := []string{"default"}

				err := dd.DecodeInto(t.Context(), &slice)
				require.NoError(t, err)
				assert.Equal(t, []string{"default"}, slice)

				array := [2]string{"a", "b"}

				err = dd.DecodeInto(t.Context(), &array)
				require.NoError(t, err)
				assert.Equal(t, [2]string{"a", "b"}, array)

				config := plainConfig{Name: "default", Value: 7}

				err = dd.DecodeInto(t.Context(), &config)
				require.NoError(t, err)
				assert.Equal(t, plainConfig{Name: "default", Value: 7}, config)
			})
		}
	})

	t.Run("leaves the value as it is for a scoped tagged null", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "items: !!seq\n")
		scoped := yamltest.At(t, dd, paths.Current().Child("items"))

		got, err := scoped.Decode[[]string](t.Context())
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("leaves the value as it is for a scoped null, as a field does", func(t *testing.T) {
		t.Parallel()

		type nullFields struct {
			Map    map[string]int `yaml:"map"`
			Config plainConfig    `yaml:"config"`
			Items  []plainConfig  `yaml:"items"`
			Int    int            `yaml:"int"`
			Uint   uint           `yaml:"uint"`
		}

		defaults := func() nullFields {
			return nullFields{
				Map:    map[string]int{"k": 1},
				Config: plainConfig{Name: "default", Value: 7},
				Int:    1,
				Uint:   2,
			}
		}

		// The go-yaml decoder rejects each of these nulls at the top of a
		// decode into an int, a uint, a map, or a struct.
		tcs := map[string]struct {
			input string
		}{
			"no value": {input: "int:\nuint:\nmap:\nconfig:\nitems:\n  -\n"},
			"null":     {input: "int: null\nuint: null\nmap: null\nconfig: null\nitems:\n  - null\n"},
			"tilde":    {input: "int: ~\nuint: ~\nmap: ~\nconfig: ~\nitems:\n  - ~\n"},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				want := defaults()
				want.Items = []plainConfig{{}}

				whole := defaults()

				err := dd.DecodeInto(t.Context(), &whole)
				require.NoError(t, err)
				assert.Equal(t, want, whole)

				scoped := defaults()

				err = yamltest.At(t, dd, paths.Current().Child("int")).DecodeInto(t.Context(), &scoped.Int)
				require.NoError(t, err)

				err = yamltest.At(t, dd, paths.Current().Child("uint")).DecodeInto(t.Context(), &scoped.Uint)
				require.NoError(t, err)

				err = yamltest.At(t, dd, paths.Current().Child("map")).DecodeInto(t.Context(), &scoped.Map)
				require.NoError(t, err)

				err = yamltest.At(t, dd, paths.Current().Child("config")).DecodeInto(t.Context(), &scoped.Config)
				require.NoError(t, err)

				items, err := dd.Nodes(paths.Current().Child("items").IndexAll())
				require.NoError(t, err)

				for _, item := range items {
					got, err := item.Decode[plainConfig](t.Context())
					require.NoError(t, err)

					scoped.Items = append(scoped.Items, got)
				}

				assert.Equal(t, want, scoped)
			})
		}
	})

	t.Run("leaves the value as it is for a null document", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input string
		}{
			"null":           {input: "null\n"},
			"tilde":          {input: "~\n"},
			"anchored null":  {input: "&a null\n"},
			"anchored tilde": {input: "&a ~\n"},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				config := plainConfig{Name: "default", Value: 7}

				err := dd.DecodeInto(t.Context(), &config)
				require.NoError(t, err)
				assert.Equal(t, plainConfig{Name: "default", Value: 7}, config)

				value := 1

				err = dd.DecodeInto(t.Context(), &value)
				require.NoError(t, err)
				assert.Equal(t, 1, value)
			})
		}
	})

	t.Run("leaves the value as it is for a scoped anchored null", func(t *testing.T) {
		t.Parallel()

		type intField struct {
			Int int `yaml:"int"`
		}

		// A path through an anchor reaches the null the anchor names, so a
		// scoped decode reads it as a decode of an anchored null document
		// reads it.
		tcs := map[string]struct {
			input string
		}{
			"null":  {input: "int: &i null\nconfig: &c null\n"},
			"tilde": {input: "int: &i ~\nconfig: &c ~\n"},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				value := 1

				err := yamltest.At(t, dd, paths.Current().Child("int")).DecodeInto(t.Context(), &value)
				require.NoError(t, err)
				assert.Equal(t, 1, value)

				config := plainConfig{Name: "default", Value: 7}

				err = yamltest.At(t, dd, paths.Current().Child("config")).DecodeInto(t.Context(), &config)
				require.NoError(t, err)
				assert.Equal(t, plainConfig{Name: "default", Value: 7}, config)

				// The go-yaml decoder rejects an anchored null in an int field.
				whole := intField{Int: 1}

				err = dd.DecodeInto(t.Context(), &whole)
				require.ErrorIs(t, err, niceyaml.ErrDecode)
			})
		}
	})

	t.Run("rejects a tagged value the decoder cannot read", func(t *testing.T) {
		t.Parallel()

		type listConfig struct {
			Items []string `yaml:"items"`
		}

		// The go-yaml decoder reads a sequence out of each tagged string
		// and reports the tagged value.
		tcs := map[string]struct {
			input  string
			decode func(ctx context.Context, dd *niceyaml.Node) error
			err    string
		}{
			"str tag in a field": {
				input: "items: !!str foo\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[listConfig](ctx)

					return err
				},
				err: "1:14: $.items: expected sequence, got string",
			},
			"str tag into a slice": {
				input: "!!str foo\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[[]string](ctx)

					return err
				},
				err: "1:7: $: expected sequence, got string",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				var err error

				require.NotPanics(t, func() {
					err = tc.decode(t.Context(), dd)
				})
				require.EqualError(t, err, tc.err)
				require.ErrorIs(t, err, niceyaml.ErrDecode)

				var srcErr *niceyaml.SourceError

				require.ErrorAs(t, err, &srcErr, "the rejection is not bound to the source")
			})
		}
	})

	t.Run("decodes a seq tag without a value in a field as no value", func(t *testing.T) {
		t.Parallel()

		type listConfig struct {
			Name  string   `yaml:"name"`
			Items []string `yaml:"items"`
		}

		dd := yamltest.FirstDocument(t, "name: x\nitems: !!seq\n")

		got, err := dd.Decode[listConfig](t.Context())
		require.NoError(t, err)
		assert.Equal(t, listConfig{Name: "x"}, got)
	})

	t.Run("locates a panic at the value, not a head comment", func(t *testing.T) {
		t.Parallel()

		type listConfig struct {
			Items panickingUnmarshaler `yaml:"items"`
		}

		tcs := map[string]struct {
			input string
			path  paths.Path
			want  position.Position
		}{
			"whole document": {
				input: "# about the file\n\nname: x\nitems: [a]\n",
				path:  paths.Current(),
				want:  position.New(2, 0),
			},
			"scoped node": {
				input: "a: 1\nwrap:\n  # the list\n  items: [a]\n",
				path:  paths.Current().Child("wrap"),
				want:  position.New(3, 2),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				var err error

				require.NotPanics(t, func() {
					_, err = yamltest.At(t, dd, tc.path).Decode[listConfig](t.Context())
				})
				require.NotErrorIs(t, err, niceyaml.ErrDecode)
				require.ErrorContains(t, err, "decoder panicked: "+errUnmarshal.Error())
				requirePanic(t, err)

				_, ok := errors.AsType[yaml.Error](err)
				assert.False(t, ok, "a go-yaml error is in the chain")

				var srcErr *niceyaml.SourceError

				require.ErrorAs(t, err, &srcErr)

				rng, ok := srcErr.Range()
				require.True(t, ok, "the rejection has no location")
				assert.Equal(t, tc.want, rng.Start)
			})
		}
	})

	t.Run("decodes every document of a stream with a comment-only document", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("a: 1\n---\n# placeholder\n---\nb: 2\n")

		docs := source.AllDocuments()
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

				var srcErr *niceyaml.SourceError

				require.ErrorAs(t, err, &srcErr)
				assert.Equal(t, tc.want, err.Error())
				assert.False(t, called, "the validator should not run for a bad target")
			})
		}
	})

	t.Run("binds a rejected target to the source", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocumentWithPath(t, "name: test", "f.yaml")

		err := dd.DecodeInto(t.Context(), nil)
		require.ErrorIs(t, err, niceyaml.ErrDecodeTarget)

		var srcErr *niceyaml.SourceError

		require.ErrorAs(t, err, &srcErr)
		assert.Equal(t, "f.yaml: decode target is not a non-nil pointer: got nil", err.Error())
	})

	t.Run("decodes through a pointer target", func(t *testing.T) {
		t.Parallel()

		// A selfPointer never reaches a value that is not a pointer.
		type selfPointer *selfPointer

		tcs := map[string]struct {
			decode     func(t *testing.T, dd *niceyaml.Node) (any, error)
			want       any
			err        error
			input      string
			references string
		}{
			"Decode validates the value": {
				input: "name: a\nvalue: 1\n",
				decode: func(t *testing.T, dd *niceyaml.Node) (any, error) {
					t.Helper()

					return dd.Decode[*validatorConfig](t.Context())
				},
				want: &validatorConfig{Name: "a", Value: 1, validated: true},
			},
			"Decode returns the error of Validate": {
				input: "value: 1\n",
				decode: func(t *testing.T, dd *niceyaml.Node) (any, error) {
					t.Helper()

					return dd.Decode[*validatorConfig](t.Context())
				},
				want: (*validatorConfig)(nil),
				err:  errNameRequired,
			},
			"DecodeInto allocates a nil pointer": {
				input: "name: a\nvalue: 1\n",
				decode: func(t *testing.T, dd *niceyaml.Node) (any, error) {
					t.Helper()

					var got *validatorConfig

					err := dd.DecodeInto(t.Context(), &got)

					return got, err //nolint:wrapcheck // The test inspects the error of the decode.
				},
				want: &validatorConfig{Name: "a", Value: 1, validated: true},
			},
			"DecodeInto keeps the fields of a non-nil pointer": {
				input: "name: a\n",
				decode: func(t *testing.T, dd *niceyaml.Node) (any, error) {
					t.Helper()

					got := &plainConfig{Value: 7}
					before := got

					err := dd.DecodeInto(t.Context(), &got)
					assert.Same(t, before, got, "the decode replaced the pointer")

					return got, err //nolint:wrapcheck // The test inspects the error of the decode.
				},
				want: &plainConfig{Name: "a", Value: 7},
			},
			"null": {
				input: "null\n",
				decode: func(t *testing.T, dd *niceyaml.Node) (any, error) {
					t.Helper()

					return dd.Decode[*validatorConfig](t.Context())
				},
				want: (*validatorConfig)(nil),
			},
			"DecodeInto sets a non-nil pointer to nil for null": {
				input: "null\n",
				decode: func(t *testing.T, dd *niceyaml.Node) (any, error) {
					t.Helper()

					got := &plainConfig{Value: 7}

					err := dd.DecodeInto(t.Context(), &got)

					return got, err //nolint:wrapcheck // The test inspects the error of the decode.
				},
				want: (*plainConfig)(nil),
			},
			"DecodeInto keeps a non-nil pointer for a tagged null": {
				input: "!!null\n",
				decode: func(t *testing.T, dd *niceyaml.Node) (any, error) {
					t.Helper()

					got := &plainConfig{Value: 7}
					before := got

					err := dd.DecodeInto(t.Context(), &got)
					assert.Same(t, before, got, "the decode replaced the pointer")

					return got, err //nolint:wrapcheck // The test inspects the error of the decode.
				},
				want: &plainConfig{Value: 7},
			},
			"anchored null": {
				input: "&a null\n",
				decode: func(t *testing.T, dd *niceyaml.Node) (any, error) {
					t.Helper()

					return dd.Decode[*validatorConfig](t.Context())
				},
				want: (*validatorConfig)(nil),
			},
			"alias to a null of a reference document": {
				input:      "*x\n",
				references: "a: &x ~\n",
				decode: func(t *testing.T, dd *niceyaml.Node) (any, error) {
					t.Helper()

					return dd.Decode[*validatorConfig](t.Context())
				},
				want: (*validatorConfig)(nil),
			},
			"DecodeInto sets a non-nil pointer to nil for an alias to a null": {
				input:      "*x\n",
				references: "a: &x ~\n",
				decode: func(t *testing.T, dd *niceyaml.Node) (any, error) {
					t.Helper()

					got := &plainConfig{Value: 7}

					err := dd.DecodeInto(t.Context(), &got)

					return got, err //nolint:wrapcheck // The test inspects the error of the decode.
				},
				want: (*plainConfig)(nil),
			},
			"anchored alias to a null of a reference document": {
				input:      "&y\n*x\n",
				references: "a: &x ~\n",
				decode: func(t *testing.T, dd *niceyaml.Node) (any, error) {
					t.Helper()

					return dd.Decode[*validatorConfig](t.Context())
				},
				want: (*validatorConfig)(nil),
			},
			"alias to a tagged null of a reference document": {
				input:      "*x\n",
				references: "a: &x !!null\n",
				decode: func(t *testing.T, dd *niceyaml.Node) (any, error) {
					t.Helper()

					return dd.Decode[*string](t.Context())
				},
				want: (*string)(nil),
			},
			"alias to a scalar of a reference document": {
				input:      "*x\n",
				references: "a: &x 7\n",
				decode: func(t *testing.T, dd *niceyaml.Node) (any, error) {
					t.Helper()

					return dd.Decode[*int](t.Context())
				},
				want: new(7),
			},
			"comment only": {
				input: "# comment\n",
				decode: func(t *testing.T, dd *niceyaml.Node) (any, error) {
					t.Helper()

					return dd.Decode[*validatorConfig](t.Context())
				},
				want: (*validatorConfig)(nil),
			},
			"scalar": {
				input: "7\n",
				decode: func(t *testing.T, dd *niceyaml.Node) (any, error) {
					t.Helper()

					return dd.Decode[*int](t.Context())
				},
				want: new(7),
			},
			"string tag over no value": {
				input: "!!str\n",
				decode: func(t *testing.T, dd *niceyaml.Node) (any, error) {
					t.Helper()

					return dd.Decode[*string](t.Context())
				},
				want: new(""),
			},
			"pointer to a pointer": {
				input: "7\n",
				decode: func(t *testing.T, dd *niceyaml.Node) (any, error) {
					t.Helper()

					return dd.Decode[**int](t.Context())
				},
				want: new(new(7)),
			},
			"pointer type that refers to itself": {
				input: "a: 1\n",
				decode: func(t *testing.T, dd *niceyaml.Node) (any, error) {
					t.Helper()

					return dd.Decode[selfPointer](t.Context())
				},
				want: selfPointer(nil),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				var opts []niceyaml.SourceOption

				if tc.references != "" {
					opts = append(opts, niceyaml.WithReferences(niceyaml.NewSourceFromString(tc.references)))
				}

				dd := yamltest.FirstDocument(t, tc.input, opts...)

				got, err := tc.decode(t, dd)
				if tc.err != nil {
					require.ErrorIs(t, err, tc.err)
				} else {
					require.NoError(t, err)
				}

				assert.Equal(t, tc.want, got)
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

// mergeLeaf is a struct with two fields, so a second decode can set one
// and leave the other.
type mergeLeaf struct {
	A int `yaml:"a"`
	B int `yaml:"b"`
}

// mergeNested holds a [mergeLeaf] one struct down.
type mergeNested struct {
	Name string    `yaml:"name"`
	Leaf mergeLeaf `yaml:"leaf"`
}

// MergeInline holds the fields [mergeTarget] inlines.
type MergeInline struct {
	InA int `yaml:"in_a"`
	InB int `yaml:"in_b"`
}

// MergeShared holds the fields [mergeTarget] inlines through a pointer.
type MergeShared struct {
	SharedA int `yaml:"shared_a"`
	SharedB int `yaml:"shared_b"`
}

// mergeTarget holds a field of each kind that a second decode into one
// value merges or replaces.
type mergeTarget struct {
	*MergeShared `yaml:",inline"`
	MergeInline  `yaml:",inline"`

	Any     any                  `yaml:"any"`
	Map     map[string]mergeLeaf `yaml:"map"`
	Pointer *mergeLeaf           `yaml:"pointer"`
	Number  *int                 `yaml:"number"`
	Slice   []mergeLeaf          `yaml:"slice"`
	Nested  mergeNested          `yaml:"nested"`
	Struct  mergeLeaf            `yaml:"struct"`
	Array   [2]mergeLeaf         `yaml:"array"`
}

func TestNode_DecodeInto_Merge(t *testing.T) {
	t.Parallel()

	base := stringtest.Input(`
		in_a: 1
		in_b: 2
		shared_a: 1
		shared_b: 2
		struct: {a: 1, b: 2}
		nested: {name: n, leaf: {a: 1, b: 2}}
		pointer: {a: 1, b: 2}
		number: 1
		slice: [{a: 1, b: 2}, {a: 3, b: 4}]
		array: [{a: 1, b: 2}, {a: 3, b: 4}]
		map: {k: {a: 1, b: 2}, j: {a: 3, b: 4}}
		any: {k: v, j: w}
	`)

	// The value a decode of base fills, built anew for each case.
	decoded := func() mergeTarget {
		number := 1

		return mergeTarget{
			InA:         1,
			InB:         2,
			MergeShared: &MergeShared{SharedA: 1, SharedB: 2},
			Struct:      mergeLeaf{A: 1, B: 2},
			Nested:      mergeNested{Name: "n", Leaf: mergeLeaf{A: 1, B: 2}},
			Pointer:     &mergeLeaf{A: 1, B: 2},
			Number:      &number,
			Slice:       []mergeLeaf{{A: 1, B: 2}, {A: 3, B: 4}},
			Array:       [2]mergeLeaf{{A: 1, B: 2}, {A: 3, B: 4}},
			Map:         map[string]mergeLeaf{"k": {A: 1, B: 2}, "j": {A: 3, B: 4}},
			Any:         map[string]any{"k": "v", "j": "w"},
		}
	}

	// Each case decodes base into a value and then over into the same
	// value. The want func changes the value base filled into the value
	// both leave, and a nil one says over changed nothing. A program that
	// decodes one file over another relies on these results, and so does
	// a decode of [niceyaml.NewSourceFromLayers] into a value that holds defaults, so
	// a go-yaml release that changes one fails here first.
	tcs := map[string]struct {
		want func(v *mergeTarget)
		over string
	}{
		"a struct merges field by field": {
			over: "struct: {a: 9}\n",
			want: func(v *mergeTarget) { v.Struct.A = 9 },
		},
		"a nested struct merges at every depth": {
			over: "nested: {leaf: {a: 9}}\n",
			want: func(v *mergeTarget) { v.Nested.Leaf.A = 9 },
		},
		"an inline struct merges": {
			over: "in_a: 9\n",
			want: func(v *mergeTarget) { v.InA = 9 },
		},
		"an inline pointer to a struct merges": {
			over: "shared_a: 9\n",
			want: func(v *mergeTarget) { v.SharedA = 9 },
		},
		"a pointer to a struct merges": {
			over: "pointer: {a: 9}\n",
			want: func(v *mergeTarget) { v.Pointer.A = 9 },
		},
		"an empty mapping keeps a struct": {
			over: "struct: {}\nnested: {leaf: {}}\npointer: {}\n",
		},
		"a slice replaces": {
			over: "slice: [{a: 9}]\n",
			want: func(v *mergeTarget) { v.Slice = []mergeLeaf{{A: 9}} },
		},
		"an empty sequence replaces a slice": {
			over: "slice: []\n",
			want: func(v *mergeTarget) { v.Slice = []mergeLeaf{} },
		},
		"an array replaces": {
			over: "array: [{a: 9}]\n",
			want: func(v *mergeTarget) { v.Array = [2]mergeLeaf{{A: 9}} },
		},
		"a map replaces": {
			over: "map: {k: {a: 9}}\n",
			want: func(v *mergeTarget) { v.Map = map[string]mergeLeaf{"k": {A: 9}} },
		},
		"an empty mapping replaces a map": {
			over: "map: {}\n",
			want: func(v *mergeTarget) { v.Map = map[string]mergeLeaf{} },
		},
		"a value of an interface type replaces": {
			over: "any: {z: y}\n",
			want: func(v *mergeTarget) { v.Any = map[string]any{"z": "y"} },
		},
		"a null keeps every kind but a pointer": {
			over: "struct: {a: null}\nnested: null\nslice: null\narray: null\nmap: null\nany: null\n",
		},
		"a null sets a pointer to nil": {
			over: "pointer: null\nnumber: null\n",
			want: func(v *mergeTarget) { v.Pointer, v.Number = nil, nil },
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var got mergeTarget

			err := yamltest.FirstDocument(t, base).DecodeInto(t.Context(), &got)
			require.NoError(t, err)
			require.Equal(t, decoded(), got)

			err = yamltest.FirstDocument(t, tc.over).DecodeInto(t.Context(), &got)
			require.NoError(t, err)

			want := decoded()
			if tc.want != nil {
				tc.want(&want)
			}

			assert.Equal(t, want, got)
		})
	}
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

		_, err := niceyaml.NewSourceFromString(input).File()
		require.Error(t, err)
		assert.Contains(t, err.Error(), `mapping key "name" already defined`)
	})

	t.Run("with option the last value wins", func(t *testing.T) {
		t.Parallel()

		doc, err := niceyaml.NewSourceFromString(input, niceyaml.WithAllowDuplicateKeys(true)).Document()
		require.NoError(t, err)

		result, err := doc.Decode[config](t.Context())
		require.NoError(t, err)
		assert.Equal(t, "second", result.Name)
	})

	t.Run("At resolves the value the decode keeps", func(t *testing.T) {
		t.Parallel()

		d := niceyaml.NewSourceFromString(input, niceyaml.WithAllowDuplicateKeys(true)).Documents()
		require.Len(t, d, 1)

		name := yamltest.At(t, d[0], paths.Current().Child("name"))

		got, err := name.Decode[string](t.Context())
		require.NoError(t, err)
		assert.Equal(t, "second", got)
	})

	t.Run("a later false turns the option off", func(t *testing.T) {
		t.Parallel()

		_, err := niceyaml.NewSourceFromString(input,
			niceyaml.WithAllowDuplicateKeys(true),
			niceyaml.WithAllowDuplicateKeys(false),
		).File()
		require.Error(t, err)
		assert.Contains(t, err.Error(), `mapping key "name" already defined`)
	})

	t.Run("without option a decode keeps the later of two keys it reads as one", func(t *testing.T) {
		t.Parallel()

		aliased := yamltest.FirstDocument(t, "&k name: first\n*k : second\n")

		result, err := aliased.Decode[config](t.Context())
		require.NoError(t, err)
		assert.Equal(t, "second", result.Name)

		respelled := yamltest.FirstDocument(t, "1: first\n0x1: second\n")

		got, err := respelled.Decode[map[string]string](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"1": "second"}, got)
	})

	t.Run("without option a decode accepts a duplicate key of a reference document", func(t *testing.T) {
		t.Parallel()

		ref := niceyaml.NewSourceFromString("defaults: &defaults {name: first, name: second}\n")

		_, err := ref.File()
		require.ErrorIs(t, err, niceyaml.ErrSyntax)

		doc := yamltest.FirstDocument(t, "server: *defaults\n", niceyaml.WithReferences(ref))

		got, err := doc.Decode[map[string]config](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]config{"server": {Name: "second"}}, got)
	})
}

func TestDocument_Decode_MergeOverride(t *testing.T) {
	t.Parallel()

	type server struct {
		Host string `yaml:"host"`
		Port int    `yaml:"port"`
	}

	// Each case decodes the server of input into a struct and into a map
	// of a type, which are the targets the go-yaml decoder checks for a
	// duplicate key.
	tcs := map[string]struct {
		input string
		want  server
	}{
		"a key after the merge overrides it": {
			input: "defaults: &defaults {host: h, port: 1}\nserver:\n  <<: *defaults\n  port: 2\n",
			want:  server{Host: "h", Port: 2},
		},
		"a merge after the key overrides it": {
			input: "defaults: &defaults {host: h, port: 1}\nserver:\n  port: 2\n  <<: *defaults\n",
			want:  server{Host: "h", Port: 1},
		},
		"a key overrides a mapping the merge key holds": {
			input: "server:\n  <<: {host: h, port: 1}\n  port: 2\n",
			want:  server{Host: "h", Port: 2},
		},
		"a key overrides a merge in a flow mapping": {
			input: "defaults: &defaults {host: h, port: 1}\nserver: {<<: *defaults, port: 2}\n",
			want:  server{Host: "h", Port: 2},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			at := paths.Current().Child("server")
			doc := yamltest.FirstDocument(t, tc.input)

			got, err := doc.DecodeAt[server](t.Context(), at)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)

			whole, err := doc.Decode[map[string]server](t.Context())
			require.NoError(t, err)
			assert.Equal(t, tc.want, whole["server"])

			typed, err := doc.DecodeAt[map[string]any](t.Context(), at)
			require.NoError(t, err)
			assert.Equal(t, map[string]any{"host": tc.want.Host, "port": uint64(tc.want.Port)}, typed)

			// A path reads the entry the decode keeps.
			port, err := doc.DecodeAt[int](t.Context(), at.Child("port"))
			require.NoError(t, err)
			assert.Equal(t, tc.want.Port, port)
		})
	}
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

	namePath := paths.Current().Child("name")

	newDoc := func(t *testing.T, input string) (*niceyaml.Source, *niceyaml.Node) {
		t.Helper()

		source := niceyaml.NewSourceFromString(input)
		docs := source.Documents()

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

		_, err := doc.At(paths.Current().Child("missing"))
		require.ErrorIs(t, err, paths.ErrNotFound)
		requireBound(t, source, err)
	})
}

func TestDocument_ValidatorErrorsResolveInDocument(t *testing.T) {
	t.Parallel()

	namePath := paths.Current().Child("name")

	source := niceyaml.NewSourceFromString("name: a\n---\nname: b\n")
	docs := source.Documents()

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

	t.Run("decodes the node with the settings of the source", func(t *testing.T) {
		t.Parallel()

		// The alias names an anchor of the reference document the source
		// holds. The ordered map option reaches the decode that gets it and
		// no decode the validator runs.
		refs := niceyaml.NewSourceFromString("base: &x 1\n")
		dd := yamltest.FirstDocument(t, "b:\n  c: *x\n", niceyaml.WithReferences(refs))
		ordered := niceyaml.WithYAMLOrderedMaps(true)

		var (
			seen  []any
			nodes []*niceyaml.Node
		)

		record := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
			nodes = append(nodes, n, n.Document())

			for _, node := range []*niceyaml.Node{n, n.Document()} {
				data, err := node.Decode[any](ctx)
				if err != nil {
					return err
				}

				seen = append(seen, data)
			}

			b, err := n.At(paths.Current().Child("b"))
			if err != nil {
				return fmt.Errorf("scope b: %w", err)
			}

			data, err := b.Decode[any](ctx)
			if err != nil {
				return err
			}

			seen = append(seen, data)

			return nil
		})

		inner := map[string]any{"c": uint64(1)}
		plain := map[string]any{"b": inner}
		want := yaml.MapSlice{{Key: "b", Value: yaml.MapSlice{{Key: "c", Value: uint64(1)}}}}

		got, err := dd.Decode[any](t.Context(), ordered, niceyaml.WithValidator(record))
		require.NoError(t, err)
		assert.Equal(t, want, got)
		assert.Equal(t, []any{plain, plain, inner}, seen)

		seen = nil

		err = dd.Validate(t.Context(), record)
		require.NoError(t, err)
		assert.Equal(t, []any{plain, plain, inner}, seen)

		require.Len(t, nodes, 4)

		for _, n := range nodes {
			assert.Same(t, dd, n)
		}
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

	t.Run("a typed nil pointer is no failure and the next validator runs", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: test\n")

		result, err := dd.Decode[plainConfig](t.Context(),
			niceyaml.WithValidator(typedNilValidator()),
			niceyaml.WithValidator(rejectingValidator(errDocumentRejected)),
		)
		require.ErrorIs(t, err, errDocumentRejected)
		assert.Equal(t, plainConfig{}, result)
	})

	t.Run("skips a nil validator", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			validator niceyaml.Validator
		}{
			"nil interface": {validator: nil},
			"nil pointer":   {validator: (*fieldValidator)(nil)},
			"nil func":      {validator: niceyaml.ValidatorFunc(nil)},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, "name: test\nvalue: 42\n")

				result, err := dd.Decode[plainConfig](t.Context(), niceyaml.WithValidator(tc.validator))
				require.NoError(t, err)
				assert.Equal(t, plainConfig{Name: "test", Value: 42}, result)

				require.NoError(t, dd.Validate(t.Context(), tc.validator))
			})
		}
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
		docs := source.Documents()

		dd := docs[1]
		require.NotNil(t, dd)

		_, err := dd.Decode[plainConfig](t.Context(),
			niceyaml.WithValidator(niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
				return niceyaml.NewError("bad name", niceyaml.AtPath(paths.Current().Child("name")))
			})),
		)
		require.Error(t, err)
		assert.Equal(t, "3:7: $.name: bad name", err.Error())
	})

	t.Run("a scoped decode passes the scope", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: test\nvalue: 42\n")
		valuePath := paths.Doc().Child("value")
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

// typedNilValidator returns a [niceyaml.Validator] that accepts every
// document by returning a nil [*niceyaml.Error] pointer.
func typedNilValidator() niceyaml.Validator {
	return niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
		var e *niceyaml.Error

		return e
	})
}

func TestDocument_Bind(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("name: a\n---\nname: b\n")
	docs := source.Documents()

	second := docs[1]
	require.NotNil(t, second)

	namePath := paths.Current().Child("name")

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
		assert.Equal(t, "document 2: plain", err.Error(), "the source has no name to add")

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
		return niceyaml.NewError("open is required", niceyaml.AtPath(paths.Current().Child("open").Key()))
	}

	if h.Open >= h.Close {
		return niceyaml.NewError("open must be before close", niceyaml.AtPath(paths.Current().Child("open")))
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
	hoursPath := paths.Doc().Child("spec", "hours")

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
		assert.Equal(t, "4:11: $.spec.hours.open: open must be before close", bound.Error())
	})

	t.Run("Bind resolves a path from the node", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)
		hours := yamltest.At(t, dd, hoursPath)

		err := hours.Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("open"))))

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)

		rng, ok := bound.Range()
		require.True(t, ok)
		assert.Equal(t, openValue, rng.Start)
		assert.Same(t, hours, bound.Node())
		assert.Same(t, dd, bound.Document())

		// The whole document resolves the same path at its root.
		err = dd.Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("open"))))
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

		got, err := yamltest.At(t, dd, hoursPath).Ranges(paths.Current().Child("open"))
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
			return niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("open")))
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
			yamltest.At(t, yamltest.At(t, dd, paths.Current().Child("spec")), paths.Current().Child("hours")),
			paths.Current().Child("open"),
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
				path:   paths.Current().Child("open"),
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
		text := yamltest.At(t, dd, paths.Current().Child("text"))

		assert.Equal(t, position.NewSpan(0, 3), text.Span())
		require.Len(t, text.Tokens(), 2)
		assert.Equal(t, "|", text.Tokens()[0].Value)
		assert.Equal(t, "a\nb\n", text.Tokens()[1].Value)
	})

	t.Run("scope on an alias", func(t *testing.T) {
		t.Parallel()

		// The node of an alias is the content of its anchor, but the node
		// of a tagged alias is the tag, which sits on the line of the alias.
		tcs := map[string]struct {
			input string
			want  string
			span  position.Span
		}{
			"alias": {
				input: "x: &x {k: 1}\nmid: 2\nc: *x\n",
				want:  "{k: 1}",
				span:  position.NewSpan(0, 1),
			},
			"tagged alias": {
				input: "x: &x {k: 1}\nmid: 2\nc: !t *x\n",
				want:  "!t *x",
				span:  position.NewSpan(2, 3),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				scoped := yamltest.At(t, yamltest.FirstDocument(t, tc.input), paths.Current().Child("c"))

				assert.Equal(t, tc.span, scoped.Span())
				assert.Equal(t, tc.want, scoped.AST().String())

				got, err := scoped.Decode[map[string]int](t.Context())
				require.NoError(t, err)
				assert.Equal(t, map[string]int{"k": 1}, got)
			})
		}
	})

	t.Run("scope ends on the last line of content", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input  string
			path   paths.Path
			span   position.Span
			tokens []string
		}{
			"block scalar with trailing blank lines": {
				input:  "a: |\n  t\n\n\nb: 1\n",
				path:   paths.Current().Child("a"),
				span:   position.NewSpan(0, 2),
				tokens: []string{"|", "t\n"},
			},
			"kept block scalar with trailing blank lines": {
				input:  "a: |+\n  t\n\n\nb: 1\n",
				path:   paths.Current().Child("a"),
				span:   position.NewSpan(0, 2),
				tokens: []string{"|+", "t\n\n\n"},
			},
			"multi-line plain scalar": {
				input:  "a: one\n  two\n  three\nb: 1\n",
				path:   paths.Current().Child("a"),
				span:   position.NewSpan(0, 3),
				tokens: []string{"one two three"},
			},
			"multi-line double-quoted scalar": {
				input:  "a: \"one\n  two\"\nb: 1\n",
				path:   paths.Current().Child("a"),
				span:   position.NewSpan(0, 2),
				tokens: []string{"one two"},
			},
			"CRLF source": {
				input:  "a:\r\n  x: 1\r\n  y: |\r\n    t\r\nb: 1\r\n",
				path:   paths.Current().Child("a"),
				span:   position.NewSpan(1, 4),
				tokens: []string{"x", ":", "1", "y", ":", "|", "t\n"},
			},
			"element that ends in a trailing comment": {
				input:  "- x: 1\n  y: 2 # c\n- b\n",
				path:   paths.Current().Index(0),
				span:   position.NewSpan(0, 2),
				tokens: []string{"x", ":", "1", "y", ":", "2", " c"},
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
	})

	t.Run("scope on an anchored value covers the comment above it", func(t *testing.T) {
		t.Parallel()

		// Where the parser reads the value below a comment between an
		// anchor and that value, the tree keeps the comment, as it does
		// without the anchor.
		tcs := map[string]struct {
			input string
			path  paths.Path
			span  position.Span
		}{
			"mapping value": {
				input: "base: &base\n  # c\n  restart: always\n",
				path:  paths.Current().Child("base"),
				span:  position.NewSpan(1, 3),
			},
			"sequence value": {
				input: "base: &base\n  # c\n  - 1\n",
				path:  paths.Current().Child("base"),
				span:  position.NewSpan(1, 3),
			},
			"sequence in the column of the key": {
				input: "base: &base\n# c\n- 1\n",
				path:  paths.Current().Child("base"),
				span:  position.NewSpan(1, 3),
			},
			"value of an anchored key": {
				input: "&k a: &x\n# c\n  b: 1\n",
				path:  paths.Current().Child("a"),
				span:  position.NewSpan(1, 3),
			},
			"value of an explicit key": {
				input: "? a\n: &x\n# c\n  b: 2\n",
				path:  paths.Current().Child("a"),
				span:  position.NewSpan(2, 4),
			},
			"value in a flow mapping": {
				input: "{a: &x\n# c\n b}\n",
				path:  paths.Current().Child("a"),
				span:  position.NewSpan(1, 3),
			},
			"sequence entry": {
				input: "- &e\n  # c\n  name: x\n",
				path:  paths.Current().Index(0),
				span:  position.NewSpan(1, 3),
			},
			"root": {
				input: "&x\n# c\nb: 1\n",
				path:  paths.Current(),
				span:  position.NewSpan(1, 3),
			},
			"anchor below the key and comment left of the key": {
				input: "x:\n  a:\n    &x\n# c\n    b: 1\n",
				path:  paths.Current().Child("x", "a"),
				span:  position.NewSpan(3, 5),
			},
			"sequence below an anchor below the key": {
				input: "x:\n  a:\n    &x\n# c\n  - 1\n",
				path:  paths.Current().Child("x", "a"),
				span:  position.NewSpan(3, 5),
			},
			"anchor below the key in a sequence entry": {
				input: "- a:\n    &x\n# c\n    b: 1\n",
				path:  paths.Current().Index(0).Child("a"),
				span:  position.NewSpan(2, 4),
			},
			"anchor below the dash and comment left of the dash": {
				input: "x:\n  -\n    &x\n# c\n    b: 1\n",
				path:  paths.Current().Child("x").Index(0),
				span:  position.NewSpan(3, 5),
			},
			"anchor after a tag and comment left of the key": {
				input: "x:\n  a: !t &x\n# c\n    b: 1\n",
				path:  paths.Current().Child("x", "a"),
				span:  position.NewSpan(1, 4),
			},
			"anchor after a tag and comment left of the dash": {
				input: "x:\n  - !t &x\n# c\n    b: 1\n",
				path:  paths.Current().Child("x").Index(0),
				span:  position.NewSpan(1, 4),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				assert.Equal(t, tc.span, yamltest.At(t, dd, tc.path).Span())
				assert.Contains(t, dd.DocumentAST().String(), "# c")
			})
		}
	})

	t.Run("scopes chain to the same extent", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)
		direct := yamltest.At(t, dd, hoursPath)
		chained := yamltest.At(t, yamltest.At(t, dd, paths.Current().Child("spec")), paths.Current().Child("hours"))

		assert.Equal(t, direct.Span(), chained.Span())
		assert.Equal(t, direct.Tokens(), chained.Tokens())
	})

	t.Run("a scope that selects nothing fails at At", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		missing, err := dd.At(paths.Current().Child("spec", "missing"))
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

		excerpt, ok := bound.Excerpt(niceyaml.WithContextLines(0))
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
			scoped := yamltest.At(t, doc, paths.Current().Child("name"))

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

func TestDocument_Decode_ValidatorErrorBoundToReceiver(t *testing.T) {
	t.Parallel()

	fail := rejectingValidator(errDocumentRejected)

	// A validator gets the receiver itself, whatever options the source
	// and the decode hold. A [niceyaml.ValidatorFunc] binds the error of
	// its function through it, and the decode binds the error a
	// validator of another type leaves unbound.
	validators := map[string]niceyaml.Validator{
		"returns an unbound error":             &fieldValidator{err: errDocumentRejected},
		"returns an unbound error from a func": fail,
		"binds its own error": niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
			return n.Bind(errDocumentRejected)
		}),
		"binds at the root it reaches": niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
			return n.Document().Bind(errDocumentRejected)
		}),
		"fails a decode of its own": niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
			_, err := n.Decode[any](ctx,
				niceyaml.WithYAMLOrderedMaps(true),
				niceyaml.WithValidator(fail),
			)

			return err
		}),
	}

	tcs := map[string]struct {
		source []niceyaml.SourceOption
		opts   []niceyaml.DecodeOption
	}{
		"no options": {},
		"references": {
			source: []niceyaml.SourceOption{
				niceyaml.WithReferences(niceyaml.NewSourceFromString("base: &x 1\n")),
			},
		},
		"ordered maps": {
			opts: []niceyaml.DecodeOption{niceyaml.WithYAMLOrderedMaps(true)},
		},
	}

	for name, tc := range tcs {
		for validatorName, validator := range validators {
			t.Run(name+"/"+validatorName, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, "a: 1\n", tc.source...)

				opts := append(slices.Clone(tc.opts), niceyaml.WithValidator(validator))

				_, decodeErr := dd.Decode[any](t.Context(), opts...)
				validateErr := dd.Validate(t.Context(), validator)
				decodeIntoErr := dd.DecodeInto(t.Context(), new(any), niceyaml.DecodeOptions(opts...))

				for _, err := range []error{decodeErr, validateErr, decodeIntoErr} {
					var bound *niceyaml.SourceError

					require.ErrorAs(t, err, &bound)
					require.ErrorIs(t, err, errDocumentRejected)
					assert.Same(t, dd, bound.Node())
					assert.Same(t, dd, bound.Document())
				}
			})
		}
	}
}

func TestDocument_Decode_ScopedValidatorErrorDocument(t *testing.T) {
	t.Parallel()

	// The validator scopes the Node it gets to the second item and binds
	// its error there, so the scoped Node holds the error, and the root
	// of its document is the receiver.
	itemPath := paths.Doc().Child("items").Index(1)

	scopes := map[string]func(n *niceyaml.Node) (*niceyaml.Node, error){
		"at": func(n *niceyaml.Node) (*niceyaml.Node, error) {
			return n.At(itemPath)
		},
		"nodes": func(n *niceyaml.Node) (*niceyaml.Node, error) {
			items, err := n.Nodes(paths.Current().Child("items").IndexAll())
			if err != nil {
				return nil, fmt.Errorf("items: %w", err)
			}

			if len(items) != 2 {
				return nil, fmt.Errorf("got %d items", len(items))
			}

			return items[1], nil
		},
	}

	tcs := map[string]struct {
		source []niceyaml.SourceOption
		opts   []niceyaml.DecodeOption
	}{
		"no options": {},
		"references": {
			source: []niceyaml.SourceOption{
				niceyaml.WithReferences(niceyaml.NewSourceFromString("base: &x 1\n")),
			},
		},
		"ordered maps": {
			opts: []niceyaml.DecodeOption{niceyaml.WithYAMLOrderedMaps(true)},
		},
	}

	for name, tc := range tcs {
		for scopeName, scope := range scopes {
			t.Run(name+"/"+scopeName, func(t *testing.T) {
				t.Parallel()

				scoping := niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
					scoped, err := scope(n)
					if err != nil {
						return fmt.Errorf("scope: %w", err)
					}

					return scoped.Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("x"))))
				})

				dd := yamltest.FirstDocument(t, "x: 0\nitems:\n  - x: 1\n  - x: 2\n", tc.source...)

				opts := append(slices.Clone(tc.opts), niceyaml.WithValidator(scoping))

				_, err := dd.Decode[any](t.Context(), opts...)
				require.EqualError(t, err, "4:8: $.items[1].x: bad")

				var bound *niceyaml.SourceError

				require.ErrorAs(t, err, &bound)
				assert.Equal(t, itemPath, bound.Node().Path())
				assert.Same(t, dd, bound.Document())
			})
		}
	}
}

func TestDocument_Decode_ValidatorAmbiguousErrorBoundToReceiver(t *testing.T) {
	t.Parallel()

	// The validator decodes the Node it gets into a map whose NaN keys
	// give its errors no position. The errors bind to the Node the
	// decode runs on, whatever options the source and the decode hold.
	decoding := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
		_, err := n.Decode[map[float64]signed](ctx)

		return err
	})

	tcs := map[string]struct {
		source []niceyaml.SourceOption
		opts   []niceyaml.DecodeOption
	}{
		"no options": {},
		"references": {
			source: []niceyaml.SourceOption{
				niceyaml.WithReferences(niceyaml.NewSourceFromString("base: &x 1\n")),
			},
		},
		"ordered maps": {
			opts: []niceyaml.DecodeOption{niceyaml.WithYAMLOrderedMaps(true)},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dd := yamltest.FirstDocument(t, "NaN: {n: -1}\n.nan: {n: -2}\n", tc.source...)

			opts := append(slices.Clone(tc.opts), niceyaml.WithValidator(decoding))

			_, err := dd.Decode[any](t.Context(), opts...)
			require.Error(t, err)

			ambiguous := 0

			for bound := range niceyaml.AllBindings(err) {
				assert.Same(t, dd, bound.Node())

				if errors.Is(bound.Unresolved(), niceyaml.ErrAmbiguousPath) {
					ambiguous++
				}
			}

			assert.Equal(t, 2, ambiguous)
		})
	}
}

func TestNode_At_NotFound(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString(stringtest.Input(`
		# c
		name: s
		hours:
		  open: 9
		items:
		  - x
		  - k: 1
	`), niceyaml.WithName("c.yaml"))

	doc, err := source.Document()
	require.NoError(t, err)

	hoursPath := paths.Current().Child("hours")
	itemsPath := paths.Current().Child("items")

	tcs := map[string]struct {
		// The path of the Node that resolves path, and the path it gets.
		scope paths.Path
		path  paths.Path
		want  string
		// The path the error reports and the mapping it is bound at, or ""
		// for an error with no location.
		wantPath string
		near     string
	}{
		"a key the root leaves out binds at the first key of the root": {
			path:     paths.Current().Child("zzz"),
			want:     "c.yaml:2:1: $.zzz: not found",
			wantPath: "$.zzz",
			near:     "$",
		},
		"a key a mapping leaves out binds at the key of the mapping": {
			path:     hoursPath.Child("close"),
			want:     "c.yaml:3:1: $.hours.close: not found",
			wantPath: "$.hours.close",
			near:     "$.hours",
		},
		"a path below a key the root leaves out binds at the root": {
			path:     paths.Current().Child("zzz", "yyy"),
			want:     "c.yaml:2:1: $.zzz.yyy: not found",
			wantPath: "$.zzz.yyy",
			near:     "$",
		},
		"a key an element leaves out binds at the dash of the element": {
			path:     itemsPath.Index(1).Child("q"),
			want:     "c.yaml:7:3: $.items[1].q: not found",
			wantPath: "$.items[1].q",
			near:     "$.items[1]",
		},
		"a key the scope leaves out binds at the key of the scope": {
			scope:    hoursPath,
			path:     paths.Current().Child("close"),
			want:     "c.yaml:3:1: $.hours.close: not found",
			wantPath: "$.hours.close",
			near:     "$.hours",
		},
		"an index past the end of a sequence has no location": {
			path: itemsPath.Index(7),
			want: "c.yaml: resolve $.items[7]: not found",
		},
		"an index past the end of the scope has no location": {
			scope: itemsPath,
			path:  paths.Current().Index(7),
			want:  "c.yaml: resolve $.items[7]: not found",
		},
		"a key below a scalar has no location": {
			path: paths.Current().Child("name", "sub"),
			want: "c.yaml: resolve $.name.sub: not found",
		},
		"a key of a sequence has no location": {
			path: itemsPath.Child("k"),
			want: "c.yaml: resolve $.items.k: not found",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			node := doc
			if tc.scope.Len() > 0 {
				node = yamltest.At(t, doc, tc.scope)
			}

			// An Error at the same path binds at the same place, or at none.
			var required *niceyaml.SourceError

			require.ErrorAs(t, node.Bind(niceyaml.NewError("required", niceyaml.AtPath(tc.path))), &required)

			wantRange, located := required.Range()
			require.Equal(t, tc.wantPath != "", located)

			_, atErr := node.At(tc.path)
			_, rangesErr := node.Ranges(tc.path)

			for method, err := range map[string]error{"At": atErr, "Ranges": rangesErr} {
				require.ErrorIs(t, err, paths.ErrNotFound, method)
				require.EqualError(t, err, tc.want, method)

				var bound *niceyaml.SourceError

				require.ErrorAs(t, err, &bound, method)
				assert.Same(t, node, bound.Node(), method)

				rng, ok := bound.Range()
				assert.Equal(t, located, ok, method)
				assert.Equal(t, wantRange, rng, method)
				require.NoError(t, bound.Unresolved(), method)

				path, ok := bound.Path()
				assert.Equal(t, located, ok, method)

				near, nearOK := bound.Nearest()
				assert.Equal(t, located, nearOK, method)

				if !located {
					continue
				}

				assert.Equal(t, "not found", bound.Message(), method)
				assert.Equal(t, tc.wantPath, path.String(), method)
				assert.Equal(t, tc.near, near.String(), method)
			}
		})
	}

	t.Run("the excerpt marks the mapping that lacks the key", func(t *testing.T) {
		t.Parallel()

		_, err := doc.At(hoursPath.Child("close"))

		assert.Equal(t, stringtest.JoinLF(
			"c.yaml:3:1: $.hours.close: not found",
			"",
			"   2 | name: s",
			"   3 | hours:",
			"     | ^^^^^",
			"   4 |   open: 9",
		), niceyaml.FormatError(err, niceyaml.WithContextLines(1)))
	})

	t.Run("a document with no content has no mapping to bind at", func(t *testing.T) {
		t.Parallel()

		empty := yamltest.FirstDocumentWithPath(t, "# nothing\n", "e.yaml")
		path := paths.Current().Child("zzz")
		want := "e.yaml: resolve $.zzz: not found: document has no content"

		_, err := empty.At(path)
		require.ErrorIs(t, err, paths.ErrNoDocument)
		require.EqualError(t, err, want)

		_, err = empty.Ranges(path)
		require.ErrorIs(t, err, paths.ErrNoDocument)
		require.EqualError(t, err, want)
	})

	t.Run("Nodes returns no error for a key a mapping leaves out", func(t *testing.T) {
		t.Parallel()

		nodes, err := doc.Nodes(hoursPath.Child("close"))
		require.NoError(t, err)
		assert.Empty(t, nodes)
	})
}

func TestDocument_At_ErrorBoundToReceiver(t *testing.T) {
	t.Parallel()

	dd := yamltest.FirstDocument(t, "a:\n  b: 1\nc: 2\n")
	scoped := yamltest.At(t, dd, paths.Current().Child("a"))

	_, err := scoped.At(paths.Current().Child("missing"))
	require.ErrorIs(t, err, paths.ErrNotFound)

	// At binds the error to its receiver, not to a copy scoped to the
	// path that did not resolve.
	var bound *niceyaml.SourceError

	require.ErrorAs(t, err, &bound)
	assert.Same(t, scoped, bound.Node())
	assert.Same(t, dd, bound.Document())
}

func TestDocument_PathAnchors(t *testing.T) {
	t.Parallel()

	// The hours mapping holds a key `open`, and so does the root, so a
	// path that resolves from the wrong node selects a node on another
	// line rather than none.
	doc := yamltest.FirstDocument(t, stringtest.Input(`
		open: 1
		spec:
		  hours:
		    open: 9
		name: x
	`))

	hoursPath := paths.Doc().Child("spec", "hours")

	tcs := map[string]struct {
		scope paths.Path
		path  paths.Path
		want  string
		err   error
	}{
		"relative path from a scoped Node": {
			scope: hoursPath,
			path:  paths.Current().Child("open"),
			want:  "$.spec.hours.open",
		},
		"absolute path from a scoped Node": {
			scope: hoursPath,
			path:  paths.Doc().Child("open"),
			want:  "$.open",
		},
		"absolute path outside the scope": {
			scope: hoursPath,
			path:  paths.Doc().Child("name"),
			want:  "$.name",
		},
		"path of the scoped Node itself": {
			scope: hoursPath,
			path:  hoursPath,
			want:  "$.spec.hours",
		},
		"current node": {
			scope: hoursPath,
			path:  paths.Current(),
			want:  "$.spec.hours",
		},
		"document root from a scoped Node": {
			scope: hoursPath,
			path:  paths.Doc(),
			want:  "$",
		},
		"absolute path names no key of the root": {
			scope: hoursPath,
			path:  paths.Doc().Child("hours"),
			err:   paths.ErrNotFound,
		},
		"relative path from the root": {
			scope: paths.Doc(),
			path:  paths.Current().Child("spec", "hours", "open"),
			want:  "$.spec.hours.open",
		},
		"absolute path from the root": {
			scope: paths.Doc(),
			path:  paths.Doc().Child("spec", "hours", "open"),
			want:  "$.spec.hours.open",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			node := yamltest.At(t, doc, tc.scope)

			got, err := node.At(tc.path)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				_, err = node.Ranges(tc.path)
				require.ErrorIs(t, err, tc.err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, got.Path().String())

			want := yamltest.At(t, doc, paths.MustParse(tc.want))
			assert.Equal(t, want.Span(), got.Span())

			nodes, err := node.Nodes(tc.path)
			require.NoError(t, err)
			require.Len(t, nodes, 1)
			assert.Equal(t, tc.want, nodes[0].Path().String())

			ranges, err := node.Ranges(tc.path)
			require.NoError(t, err)

			wantRanges, err := doc.Ranges(paths.MustParse(tc.want))
			require.NoError(t, err)
			assert.Equal(t, wantRanges, ranges)

			// An Error at the path binds where the path resolves, and the
			// binding reports the path from the root of the document.
			var bound *niceyaml.SourceError

			require.ErrorAs(t, node.Bind(niceyaml.NewError("bad", niceyaml.AtPath(tc.path))), &bound)
			require.NoError(t, bound.Unresolved())

			path, ok := bound.Path()
			require.True(t, ok)
			assert.Equal(t, tc.want, path.String())

			rng, ok := bound.Range()
			require.True(t, ok)
			require.NotEmpty(t, wantRanges)
			assert.Equal(t, wantRanges[0].Start, rng.Start)
		})
	}
}

func TestDocument_Validate_NodePaths(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString(stringtest.Input(`
		spec:
		  containers:
		    - name: a
		      image: ""
		    - name: b
		      image: nginx
	`), niceyaml.WithFilePath("m.yaml"))

	doc, err := source.Document()
	require.NoError(t, err)

	// The check names each image by the path its Node reports, which starts
	// at `$`, and binds through the Node it got.
	images := func(selector paths.Path) niceyaml.Validator {
		return niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
			nodes, err := n.Nodes(selector)
			if err != nil {
				return fmt.Errorf("images: %w", err)
			}

			var errs []error

			for _, image := range nodes {
				got, err := image.Decode[string](ctx)
				if err != nil {
					return fmt.Errorf("image: %w", err)
				}

				if got == "" {
					errs = append(errs, n.Bind(niceyaml.NewError("image is empty", niceyaml.AtPath(image.Path()))))
				}
			}

			return niceyaml.NewSummary(fmt.Sprintf("%d empty images", len(errs)), errs...)
		})
	}

	const want = "m.yaml:4:14: $.spec.containers[0].image: image is empty"

	tcs := map[string]struct {
		scope    paths.Path
		selector paths.Path
	}{
		"root": {
			scope:    paths.Doc(),
			selector: paths.Current().Child("spec", "containers").IndexAll().Child("image"),
		},
		"scoped": {
			scope:    paths.Doc().Child("spec"),
			selector: paths.Current().Child("containers").IndexAll().Child("image"),
		},
		"scoped with an absolute selector": {
			scope:    paths.Doc().Child("spec"),
			selector: paths.Doc().Child("spec", "containers").IndexAll().Child("image"),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			node := yamltest.At(t, doc, tc.scope)

			err := node.Validate(t.Context(), images(tc.selector))
			require.EqualError(t, err, want)

			var bound *niceyaml.SourceError

			require.ErrorAs(t, err, &bound)

			path, ok := bound.Path()
			require.True(t, ok)
			assert.Equal(t, paths.MustParse("$.spec.containers[0].image"), path)

			pos, ok := bound.Position()
			require.True(t, ok)
			assert.Equal(t, position.New(3, 13), pos)

			_, err = node.Decode[any](t.Context(), niceyaml.WithValidator(images(tc.selector)))
			require.EqualError(t, err, want)
		})
	}
}

func TestDocument_At_FlowCollectionSpan(t *testing.T) {
	t.Parallel()

	// The token that closes a flow collection belongs to the node, so the
	// span and the tokens of a scoped Node run through it.
	tcs := map[string]struct {
		input  string
		path   paths.Path
		span   position.Span
		tokens []string
	}{
		"flow sequence over several lines": {
			input:  "a: [\n  1,\n  2,\n]\nb: 3\n",
			path:   paths.Current().Child("a"),
			span:   position.NewSpan(0, 4),
			tokens: []string{"[", "1", ",", "2", ",", "]"},
		},
		"flow mapping at the root": {
			input:  "{\n  \"a\": 1,\n  \"b\": [\n    2\n  ]\n}\n",
			path:   paths.Current(),
			span:   position.NewSpan(0, 6),
			tokens: []string{"{", "a", ":", "1", ",", "b", ":", "[", "2", "]", "}"},
		},
		"flow sequence on one line": {
			input:  "a: [1, 2]\n",
			path:   paths.Current().Child("a"),
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

func TestDocument_At_ZeroWidthBoundary(t *testing.T) {
	t.Parallel()

	// A token that holds no text can share its offset with a token of
	// another node. The empty content of a block scalar sits where the
	// next key starts, and an implicit null takes the offset of the ":"
	// or "-" before it. The span and the tokens of a scoped Node keep
	// to the tokens of the node.
	tcs := map[string]struct {
		input  string
		path   paths.Path
		span   position.Span
		tokens []string
	}{
		"empty block scalar before a sibling": {
			input:  "a: |\nb: 1\n",
			path:   paths.Current().Child("a"),
			span:   position.NewSpan(0, 1),
			tokens: []string{"|", ""},
		},
		"empty folded scalar before an indented sibling": {
			input:  "x:\n  a: >\n  b: 1\n",
			path:   paths.Current().Child("x", "a"),
			span:   position.NewSpan(1, 2),
			tokens: []string{">", ""},
		},
		"mapping that ends in an empty block scalar": {
			input:  "x:\n  a: 1\n  b: |\ny: 1\n",
			path:   paths.Current().Child("x"),
			span:   position.NewSpan(1, 3),
			tokens: []string{"a", ":", "1", "b", ":", "|", ""},
		},
		"key after an empty block scalar": {
			input:  "a: |\nb: 1\n",
			path:   paths.Current().Child("b").Key(),
			span:   position.NewSpan(1, 2),
			tokens: []string{"b"},
		},
		"implicit null in a mapping": {
			input: "a:\nb: 1\n",
			path:  paths.Current().Child("a"),
			span:  position.NewSpan(0, 1),
		},
		"implicit null in a sequence": {
			input: "- \n- 1\n",
			path:  paths.Current().Index(0),
			span:  position.NewSpan(0, 1),
		},
		"mapping that ends in an implicit null": {
			input:  "x:\n  a: 1\n  b:\ny: 2\n",
			path:   paths.Current().Child("x"),
			span:   position.NewSpan(1, 3),
			tokens: []string{"a", ":", "1", "b", ":"},
		},
		"sequence that ends in an implicit null": {
			input:  "x:\n  - a\n  -\ny: 1\n",
			path:   paths.Current().Child("x"),
			span:   position.NewSpan(1, 3),
			tokens: []string{"-", "a", "-"},
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

func ExampleDecodeOptions() {
	ctx := context.Background()

	type manifest struct {
		Kind string `yaml:"kind"`
		Name string `yaml:"name"`
	}

	manifests := schema.MustCompile([]byte(`{"type": "object", "required": ["kind", "name"]}`))

	settings := niceyaml.DecodeOptions(niceyaml.WithDisallowUnknownFields(true))
	strict := niceyaml.DecodeOptions(niceyaml.WithValidator(manifests), settings)

	docs := niceyaml.NewSourceFromString(
		"kind: Service\nname: web\n---\nkind: Deployment\nname: web\nreplicas: 3\n",
		niceyaml.WithName("app.yaml"),
	).Documents()

	// Each whole document validates against the schema and decodes with
	// the settings.
	for _, doc := range docs {
		m, err := doc.Decode[manifest](ctx, strict)
		if err != nil {
			fmt.Println(err)

			continue
		}

		fmt.Println(m.Kind, m.Name)
	}

	kindPath := paths.Current().Child("kind")

	// The validator checks the node the call decodes, and the schema of
	// the document does not describe the value at $.kind.
	_, err := docs[0].DecodeAt[string](ctx, kindPath, strict)
	fmt.Println(err)

	// A decode of one value takes the settings without that schema.
	kind, err := docs[0].DecodeAt[string](ctx, kindPath, settings)
	fmt.Println(kind, err)

	// Output:
	// Service web
	// app.yaml:6:1: $.replicas~: unknown field "replicas"
	// app.yaml:1:7: $.kind: expected "object", got "string"
	// Service <nil>
}

func TestDecodeOptions(t *testing.T) {
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

	record := func(order *[]string, name string) niceyaml.DecodeOption {
		return niceyaml.WithValidator(niceyaml.ValidatorFunc(func(_ context.Context, _ *niceyaml.Node) error {
			*order = append(*order, name)

			return nil
		}))
	}

	strict := niceyaml.WithDisallowUnknownFields(true)
	lax := niceyaml.WithDisallowUnknownFields(false)

	t.Run("decodes every document with the options stated once", func(t *testing.T) {
		t.Parallel()

		var order []string

		opts := niceyaml.DecodeOptions(record(&order, "schema"), strict)

		docs := niceyaml.NewSourceFromString(input).Documents()
		require.Len(t, docs, 2)

		for _, dd := range docs {
			_, err := dd.Decode[strictConfig](t.Context(), opts)
			require.ErrorContains(t, err, `unknown field "extra"`)

			var bound *niceyaml.SourceError

			require.ErrorAs(t, err, &bound)
			assert.Same(t, dd, bound.Node())
		}

		assert.Equal(t, []string{"schema", "schema"}, order)
	})

	t.Run("without options decodes as a call without options does", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, input)

		got, err := dd.Decode[validatorConfig](t.Context(), niceyaml.DecodeOptions())
		require.NoError(t, err)
		assert.Equal(t, "test", got.Name)
		assert.True(t, got.validated, "the value did not validate itself")
	})

	t.Run("applies its options in order among the options of the call", func(t *testing.T) {
		t.Parallel()

		var order []string

		opts := niceyaml.DecodeOptions(
			record(&order, "first"),
			niceyaml.DecodeOptions(record(&order, "nested")),
			record(&order, "last"),
		)

		dd := yamltest.FirstDocument(t, input)

		_, err := dd.Decode[strictConfig](t.Context(), record(&order, "before"), opts, record(&order, "after"))
		require.NoError(t, err)
		assert.Equal(t, []string{"before", "first", "nested", "last", "after"}, order)
	})

	t.Run("stops at the first validator that fails", func(t *testing.T) {
		t.Parallel()

		var order []string

		opts := niceyaml.DecodeOptions(
			record(&order, "first"),
			niceyaml.WithValidator(rejectingValidator(errNameRequired)),
			record(&order, "unreached"),
		)

		dd := yamltest.FirstDocument(t, input)

		got, err := dd.Decode[strictConfig](t.Context(), opts, record(&order, "after"))
		require.ErrorIs(t, err, errNameRequired)
		assert.Equal(t, strictConfig{}, got)
		assert.Equal(t, []string{"first"}, order)
	})

	t.Run("the last WithDisallowUnknownFields of the call sets the value", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			opts []niceyaml.DecodeOption
			err  string
		}{
			"the option alone": {
				opts: []niceyaml.DecodeOption{niceyaml.DecodeOptions(strict)},
				err:  `2:1: $.extra~: unknown field "extra"`,
			},
			"an option after it": {
				opts: []niceyaml.DecodeOption{niceyaml.DecodeOptions(strict), lax},
			},
			"an option before it": {
				opts: []niceyaml.DecodeOption{lax, niceyaml.DecodeOptions(strict)},
				err:  `2:1: $.extra~: unknown field "extra"`,
			},
			"two options inside it": {
				opts: []niceyaml.DecodeOption{niceyaml.DecodeOptions(strict, lax)},
			},
			"a nested value after an option": {
				opts: []niceyaml.DecodeOption{niceyaml.DecodeOptions(lax, niceyaml.DecodeOptions(strict))},
				err:  `2:1: $.extra~: unknown field "extra"`,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, input)

				got, err := dd.Decode[strictConfig](t.Context(), tc.opts...)
				if tc.err != "" {
					require.EqualError(t, err, tc.err)

					return
				}

				require.NoError(t, err)
				assert.Equal(t, strictConfig{Name: "test"}, got)
			})
		}
	})

	t.Run("the last WithSelfValidation of the call sets the value", func(t *testing.T) {
		t.Parallel()

		off := niceyaml.DecodeOptions(niceyaml.WithSelfValidation(false))

		dd := yamltest.FirstDocument(t, input)

		got, err := dd.Decode[failingValidator](t.Context(), off)
		require.NoError(t, err)
		assert.Equal(t, "test", got.Name)

		_, err = dd.Decode[failingValidator](t.Context(), off, niceyaml.WithSelfValidation(true))
		require.Error(t, err, "the value validates itself")

		_, err = dd.Decode[failingValidator](t.Context(), niceyaml.WithSelfValidation(true), off)
		require.NoError(t, err)
	})

	t.Run("the same value twice runs its validators twice", func(t *testing.T) {
		t.Parallel()

		var order []string

		opts := niceyaml.DecodeOptions(record(&order, "schema"))

		dd := yamltest.FirstDocument(t, input)

		_, err := dd.Decode[strictConfig](t.Context(), opts, opts) //nolint:gocritic // The call gets the value twice.
		require.NoError(t, err)
		assert.Equal(t, []string{"schema", "schema"}, order)
	})

	t.Run("a value built from another leaves it as it was", func(t *testing.T) {
		t.Parallel()

		var order []string

		base := niceyaml.DecodeOptions(record(&order, "base"), strict)
		derived := niceyaml.DecodeOptions(base, record(&order, "derived"), lax)

		dd := yamltest.FirstDocument(t, input)

		got, err := dd.Decode[strictConfig](t.Context(), derived)
		require.NoError(t, err)
		assert.Equal(t, "test", got.Name)
		assert.Equal(t, []string{"base", "derived"}, order)

		order = nil

		_, err = dd.Decode[strictConfig](t.Context(), base)
		require.Error(t, err, "the first value keeps its strictness")
		assert.Equal(t, []string{"base"}, order, "the first value keeps its validators")
	})

	t.Run("a later custom unmarshaler replaces the one before it", func(t *testing.T) {
		t.Parallel()

		type marker string

		type holder struct {
			M   any    `yaml:"m"`
			Tag marker `yaml:"tag"`
		}

		// The last WithCustomUnmarshaler given for a type decodes it, so
		// the marker a decode yields names the option that came last.
		setMarker := func(value marker) niceyaml.DecodeOption {
			return niceyaml.WithCustomUnmarshaler(func(_ context.Context, m *marker, _ func(any) error) error {
				*m = value

				return nil
			})
		}

		base := niceyaml.DecodeOptions(niceyaml.WithYAMLOrderedMaps(true), setMarker("base"))
		derived := niceyaml.DecodeOptions(base, setMarker("derived"))

		dd := yamltest.FirstDocument(t, "m: {b: 1, a: 2}\ntag: x\n")

		got, err := dd.Decode[holder](t.Context(), derived)
		require.NoError(t, err)
		assert.Equal(t, yaml.MapSlice{
			{Key: "b", Value: uint64(1)},
			{Key: "a", Value: uint64(2)},
		}, got.M, "the options of the first value still apply")
		assert.Equal(t, marker("derived"), got.Tag, "the later options apply after the ones of the first value")

		got, err = dd.Decode[holder](t.Context(), base)
		require.NoError(t, err)
		assert.Equal(t, marker("base"), got.Tag, "the first value keeps its options")
	})

	t.Run("keeps its options when the caller edits the slice", func(t *testing.T) {
		t.Parallel()

		var order []string

		given := []niceyaml.DecodeOption{record(&order, "given")}
		opts := niceyaml.DecodeOptions(given...)
		given[0] = record(&order, "edited")

		dd := yamltest.FirstDocument(t, input)

		_, err := dd.Decode[strictConfig](t.Context(), opts)
		require.NoError(t, err)
		assert.Equal(t, []string{"given"}, order)
	})

	t.Run("a nil option panics in the call that applies it", func(t *testing.T) {
		t.Parallel()

		var opts niceyaml.DecodeOption

		require.NotPanics(t, func() {
			opts = niceyaml.DecodeOptions(nil)
		})

		dd := yamltest.FirstDocument(t, input)

		assert.Panics(t, func() {
			_, _ = dd.Decode[strictConfig](t.Context(), opts) //nolint:errcheck // The call panics before it returns.
		})
		assert.Panics(t, func() {
			_, _ = dd.Decode[strictConfig](t.Context(), nil) //nolint:errcheck // The call panics before it returns.
		}, "a nil option given to the call panics too")
	})
}

// TestDecodeOptions_EntryPoints gives one [niceyaml.DecodeOptions] value
// to every method that takes a [niceyaml.DecodeOption], and requires what
// the same options written out in the call give.
func TestDecodeOptions_EntryPoints(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		by_grade:
		  high: {url: http://h}
		extra: 1
	`)

	byGrade := paths.Current().Child("by_grade")
	unknown := `app.yaml:3:1: $.extra~: unknown field "extra"`
	notHTTP := `app.yaml:2:15: $.by_grade.high.url: url "ftp://h" is not http`

	// The value a layer changed after the decode, which the walk rejects.
	changed := func() *gradedConfig {
		return &gradedConfig{ByGrade: map[grade]upstream{gradeHigh: {URL: "ftp://h"}}}
	}

	document := func(t *testing.T, src *niceyaml.Source) *niceyaml.Node {
		t.Helper()

		doc, err := src.Document()
		require.NoError(t, err)

		return doc
	}

	// Each option shows that it reached the call. A grade decodes only
	// under gradeNames, and the walk binds at the key of the document
	// only under it. The unknown key fails only a strict decode. The
	// validator counts its runs, and the walk runs with the option that
	// turns it off for a decode.
	tcs := map[string]struct {
		// Calls the method on src, or on its document, with opts.
		call func(t *testing.T, src *niceyaml.Source, opts ...niceyaml.DecodeOption) error
		err  string
		// How many times the validator runs.
		runs int
	}{
		"Node.Decode": {
			call: func(t *testing.T, src *niceyaml.Source, opts ...niceyaml.DecodeOption) error {
				t.Helper()

				_, err := document(t, src).Decode[gradedConfig](t.Context(), opts...)

				return err
			},
			err:  unknown,
			runs: 1,
		},
		"Node.DecodeInto": {
			call: func(t *testing.T, src *niceyaml.Source, opts ...niceyaml.DecodeOption) error {
				t.Helper()

				return document(t, src).DecodeInto(t.Context(), new(gradedConfig), opts...)
			},
			err:  unknown,
			runs: 1,
		},
		"Node.DecodeAt": {
			call: func(t *testing.T, src *niceyaml.Source, opts ...niceyaml.DecodeOption) error {
				t.Helper()

				got, err := document(t, src).DecodeAt[map[grade]upstream](t.Context(), byGrade, opts...)
				assert.Equal(t, map[grade]upstream{gradeHigh: {URL: "http://h"}}, got)

				return err
			},
			runs: 1,
		},
		"Node.DecodeIfPresent": {
			call: func(t *testing.T, src *niceyaml.Source, opts ...niceyaml.DecodeOption) error {
				t.Helper()

				var got map[grade]upstream

				present, err := document(t, src).DecodeIfPresent(t.Context(), byGrade, &got, opts...)
				assert.True(t, present)
				assert.Equal(t, map[grade]upstream{gradeHigh: {URL: "http://h"}}, got)

				return err //nolint:wrapcheck // The test inspects the error of the call.
			},
			runs: 1,
		},
		"Node.SelfValidate": {
			call: func(t *testing.T, src *niceyaml.Source, opts ...niceyaml.DecodeOption) error {
				t.Helper()

				return document(t, src).SelfValidate(t.Context(), changed(), opts...)
			},
			err: notHTTP,
		},
		"Source.Decode": {
			call: func(t *testing.T, src *niceyaml.Source, opts ...niceyaml.DecodeOption) error {
				t.Helper()

				_, err := src.Decode[gradedConfig](t.Context(), opts...)

				return err
			},
			err:  unknown,
			runs: 1,
		},
		"Source.DecodeInto": {
			call: func(t *testing.T, src *niceyaml.Source, opts ...niceyaml.DecodeOption) error {
				t.Helper()

				return src.DecodeInto(t.Context(), new(gradedConfig), opts...)
			},
			err:  unknown,
			runs: 1,
		},
		"Source.SelfValidate": {
			call: func(t *testing.T, src *niceyaml.Source, opts ...niceyaml.DecodeOption) error {
				t.Helper()

				return src.SelfValidate(t.Context(), changed(), opts...)
			},
			err: notHTTP,
		},
		"merged Source.Decode": {
			call: func(t *testing.T, src *niceyaml.Source, opts ...niceyaml.DecodeOption) error {
				t.Helper()

				_, err := niceyaml.NewSourceFromLayers(document(t, src)).Decode[gradedConfig](t.Context(), opts...)

				return err
			},
			err:  unknown,
			runs: 1,
		},
		"merged Source.DecodeInto": {
			call: func(t *testing.T, src *niceyaml.Source, opts ...niceyaml.DecodeOption) error {
				t.Helper()

				return niceyaml.NewSourceFromLayers(document(t, src)).
					DecodeInto(t.Context(), new(gradedConfig), opts...)
			},
			err:  unknown,
			runs: 1,
		},
		"merged Source.SelfValidate": {
			call: func(t *testing.T, src *niceyaml.Source, opts ...niceyaml.DecodeOption) error {
				t.Helper()

				return niceyaml.NewSourceFromLayers(document(t, src)).SelfValidate(t.Context(), changed(), opts...)
			},
			err: notHTTP,
		},
	}

	// How the call gets the options.
	forms := map[string]func(opts []niceyaml.DecodeOption) []niceyaml.DecodeOption{
		"written out": func(opts []niceyaml.DecodeOption) []niceyaml.DecodeOption {
			return opts
		},
		"as one value": func(opts []niceyaml.DecodeOption) []niceyaml.DecodeOption {
			return []niceyaml.DecodeOption{niceyaml.DecodeOptions(opts...)}
		},
	}

	for name, tc := range tcs {
		for formName, form := range forms {
			t.Run(name+"/"+formName, func(t *testing.T) {
				t.Parallel()

				runs := 0

				opts := form([]niceyaml.DecodeOption{
					gradeNames,
					niceyaml.WithValidator(niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
						runs++

						return nil
					})),
					niceyaml.WithDisallowUnknownFields(true),
					niceyaml.WithSelfValidation(false),
				})

				err := tc.call(t, niceyaml.NewSourceFromString(input, niceyaml.WithName("app.yaml")), opts...)
				if tc.err == "" {
					require.NoError(t, err)
				} else {
					require.EqualError(t, err, tc.err)
				}

				assert.Equal(t, tc.runs, runs)
			})
		}
	}
}

func TestNode_Validate(t *testing.T) {
	t.Parallel()

	record := func(order *[]string, name string) niceyaml.Validator {
		return niceyaml.ValidatorFunc(func(_ context.Context, _ *niceyaml.Node) error {
			*order = append(*order, name)

			return nil
		})
	}

	t.Run("runs the validator on the receiver", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "meta:\n  name: test\n")
		scoped := yamltest.At(t, dd, paths.Current().Child("meta"))

		var got []*niceyaml.Node

		err := scoped.Validate(t.Context(), niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
			got = append(got, n)

			return errNameRequired
		}))
		require.ErrorIs(t, err, errNameRequired)
		require.Len(t, got, 1)
		assert.Same(t, scoped, got[0])
	})

	t.Run("the validator decides how many failures the node reports", func(t *testing.T) {
		t.Parallel()

		var order []string

		doc, err := niceyaml.NewSourceFromString("meta:\n  name: test\n", niceyaml.WithName("x.yaml")).Document()
		require.NoError(t, err)

		scoped := yamltest.At(t, doc, paths.Current().Child("meta"))

		namePath := paths.Current().Child("name")
		reserved := &fieldValidator{err: niceyaml.NewError("reserved name", niceyaml.AtPath(namePath))}
		short := &fieldValidator{err: niceyaml.NewError("name is too short", niceyaml.AtPath(namePath))}

		// A ChainValidator stops at the first validator that fails.
		err = scoped.Validate(t.Context(), niceyaml.ChainValidator(
			record(&order, "first"),
			reserved,
			record(&order, "unreached"),
			short,
		))
		require.EqualError(t, err, "x.yaml:2:9: $.meta.name: reserved name")
		assert.Equal(t, []string{"first"}, order)

		order = nil

		// A MultiValidator runs every one and joins the failures.
		err = scoped.Validate(t.Context(), niceyaml.MultiValidator(
			record(&order, "first"),
			reserved,
			record(&order, "second"),
			short,
		))
		require.EqualError(t, err, stringtest.JoinLF(
			"x.yaml:2:9: $.meta.name: reserved name",
			"x.yaml:2:9: $.meta.name: name is too short",
		))
		assert.Equal(t, []string{"first", "second"}, order)
	})

	t.Run("a typed nil pointer is no failure", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: test\n")

		for _, v := range []niceyaml.Validator{
			typedNilValidator(),
			&fieldValidator{err: (*niceyaml.Error)(nil)},
			&fieldValidator{err: (*niceyaml.SourceError)(nil)},
		} {
			require.NoError(t, dd.Validate(t.Context(), v))
		}
	})

	t.Run("binds the error a validator leaves unbound", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("meta:\n  name: test\n", niceyaml.WithName("x.yaml"))

		doc, err := source.Document()
		require.NoError(t, err)

		scoped := yamltest.At(t, doc, paths.Current().Child("meta"))
		unbound := &fieldValidator{
			err: niceyaml.Invalid(errNameRequired, niceyaml.AtPath(paths.Current().Child("name"))),
		}

		// A direct call returns the error as the validator wrote it, and
		// the Node binds it from its own scope.
		require.EqualError(t, unbound.Check(t.Context(), scoped), "name is required")

		err = scoped.Validate(t.Context(), unbound)
		require.EqualError(t, err, "x.yaml:2:9: $.meta.name: name is required")

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Same(t, scoped, bound.Node())
	})

	t.Run("returns a bound error as it is", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "meta:\n  name: test\n")
		scoped := yamltest.At(t, dd, paths.Current().Child("meta"))

		want := scoped.Bind(niceyaml.Invalid(errNameRequired, niceyaml.AtPath(paths.Current().Child("name"))))

		assert.Same(t, want, dd.Validate(t.Context(), &fieldValidator{err: want}))
		assert.Same(t, want, dd.Validate(t.Context(), rejectingValidator(want)))
	})

	t.Run("a nil validator returns what Err returns", func(t *testing.T) {
		t.Parallel()

		docs := niceyaml.NewSourceFromString("name: test\n---\nname: [\n", niceyaml.WithName("f.yaml")).AllDocuments()
		require.Len(t, docs, 2)
		require.NoError(t, docs[0].Err())
		require.EqualError(t, docs[1].Err(), "f.yaml:3:7: sequence end token ']' not found")

		scoped := yamltest.At(t, docs[0], paths.Current().Child("name"))

		// A nil pointer to a fieldValidator panics if it runs.
		for _, v := range []niceyaml.Validator{nil, (*fieldValidator)(nil), niceyaml.ValidatorFunc(nil)} {
			require.NoError(t, docs[0].Validate(t.Context(), v))
			require.NoError(t, scoped.Validate(t.Context(), v))
			assert.Same(t, docs[1].Err(), docs[1].Validate(t.Context(), v))
		}

		// A context that ended changes neither result.
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		require.NoError(t, docs[0].Validate(ctx, nil))
		assert.Same(t, docs[1].Err(), docs[1].Validate(ctx, nil))
	})

	t.Run("a validator of one call reaches no other decode", func(t *testing.T) {
		t.Parallel()

		var order []string

		docs := niceyaml.NewSourceFromString("name: a\n---\nname: b\n").Documents()

		_, err := docs[0].Decode[map[string]any](t.Context(), niceyaml.WithValidator(record(&order, "call")))
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

		items, err := doc.Nodes(paths.Current().Child("items").IndexAll())
		require.NoError(t, err)
		require.Len(t, items, 2)

		assert.Equal(t, "$.items[0]", items[0].Path().String())
		assert.Equal(t, "$.items[1]", items[1].Path().String())
		assert.Equal(t, position.NewSpan(1, 3), items[0].Span())
		assert.Same(t, doc, items[1].Document())

		price, err := items[1].At(paths.Current().Child("price"))
		require.NoError(t, err)

		got, err := price.Decode[int](t.Context())
		require.NoError(t, err)
		assert.Equal(t, -1, got)

		// The error writes its path from the scope, and the message
		// carries it from the root of the document, beside the position
		// it resolved to.
		err = items[1].Bind(niceyaml.NewError("negative price", niceyaml.AtPath(paths.Current().Child("price"))))
		require.EqualError(t, err, "m.yaml:5:12: $.items[1].price: negative price")
	})

	t.Run("scopes each entry a recursive selector finds", func(t *testing.T) {
		t.Parallel()

		images, err := doc.Nodes(paths.Current().Recursive("image"))
		require.NoError(t, err)
		require.Len(t, images, 2)

		assert.Equal(t, "$.spec.image", images[0].Path().String())
		assert.Equal(t, "$.spec.nested.image", images[1].Path().String())

		got, err := images[1].Decode[string](t.Context())
		require.NoError(t, err)
		assert.Equal(t, "b", got)
	})

	t.Run("scopes every node at any depth", func(t *testing.T) {
		t.Parallel()

		config, err := niceyaml.NewSourceFromString(stringtest.Input(`
			defaults: &defaults
			  logLevel: info
			server:
			  <<: *defaults
			  listen_addr: ":80"
			  TLS:
			    cert_file: a.pem
			routes:
			  - match: /api
			    upstreamURL: http://api
			  - *defaults
		`), niceyaml.WithName("c.yaml")).Document()
		require.NoError(t, err)

		nodes, err := config.Nodes(paths.Current().RecursiveAll())
		require.NoError(t, err)

		var gotPaths []string

		for _, n := range nodes {
			gotPaths = append(gotPaths, n.Path().String())
		}

		// The merged key and the alias are listed where the source writes
		// them, not again where they are used.
		assert.Equal(t, []string{
			"$.defaults", "$.defaults.logLevel",
			"$.server", "$.server.listen_addr", "$.server.TLS", "$.server.TLS.cert_file",
			"$.routes", "$.routes[0]", "$.routes[0].match", "$.routes[0].upstreamURL", "$.routes[1]",
		}, gotPaths)

		snake := regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

		var errs []error

		for _, n := range nodes {
			sel, ok := n.Path().Last()
			if ok && sel.Kind == paths.SelectorChild && !snake.MatchString(sel.Name) {
				errs = append(errs, config.Bind(niceyaml.NewError(
					fmt.Sprintf("key %q is not snake_case", sel.Name),
					niceyaml.AtPath(n.Path().Key()),
				)))
			}
		}

		got := make([]string, 0, len(errs))
		for _, err := range errs {
			got = append(got, err.Error())
		}

		assert.Equal(t, []string{
			`c.yaml:2:3: $.defaults.logLevel~: key "logLevel" is not snake_case`,
			`c.yaml:6:3: $.server.TLS~: key "TLS" is not snake_case`,
			`c.yaml:10:5: $.routes[0].upstreamURL~: key "upstreamURL" is not snake_case`,
		}, got)
	})

	t.Run("scopes each entry of a mapping", func(t *testing.T) {
		t.Parallel()

		workflow, err := niceyaml.NewSourceFromString(stringtest.Input(`
			defaults: &defaults
			  lint:
			    runs-on: linux
			jobs:
			  <<: *defaults
			  build:
			    runs-on: mac
			  3.10:
			    runs-on: bsd
			    steps:
			      - uses: checkout
			      - run: make
		`), niceyaml.WithName("w.yaml")).Document()
		require.NoError(t, err)

		jobs, err := workflow.Nodes(paths.Current().Child("jobs").ChildAll())
		require.NoError(t, err)
		require.Len(t, jobs, 3)

		type job struct {
			RunsOn string `yaml:"runs-on"`
		}

		var gotPaths, gotNames, gotTexts, gotRunners []string

		for _, j := range jobs {
			gotPaths = append(gotPaths, j.Path().String())

			// The key decodes as the decoder reads it, so the key 3.10
			// gives 3.1, while the path keeps the text of the source.
			key, err := j.At(paths.Current().Key())
			require.NoError(t, err)

			name, err := key.Decode[string](t.Context())
			require.NoError(t, err)

			gotNames = append(gotNames, name)
			gotTexts = append(gotTexts, key.AST().GetToken().Value)

			got, err := j.Decode[job](t.Context())
			require.NoError(t, err)

			gotRunners = append(gotRunners, got.RunsOn)

			// A Node scoped to one entry is the Node its own path selects.
			single, err := workflow.At(j.Path())
			require.NoError(t, err)
			assert.Same(t, j.AST(), single.AST())
		}

		assert.Equal(t, []string{"$.jobs.lint", "$.jobs.build", "$.jobs.'3.10'"}, gotPaths)
		assert.Equal(t, []string{"lint", "build", "3.1"}, gotNames)
		assert.Equal(t, []string{"lint", "build", "3.10"}, gotTexts)
		assert.Equal(t, []string{"linux", "mac", "bsd"}, gotRunners)

		// A merged entry lies where its anchor defines it.
		assert.Equal(t, position.NewSpan(2, 3), jobs[0].Span())
		assert.Same(t, workflow, jobs[2].Document())

		err = jobs[2].Bind(niceyaml.NewError("unknown runner", niceyaml.AtPath(paths.Current().Child("runs-on"))))
		require.EqualError(t, err, "w.yaml:9:14: $.jobs.'3.10'.runs-on: unknown runner")

		uses, err := workflow.Nodes(paths.MustParse("$.jobs.*.steps[*].uses"))
		require.NoError(t, err)
		require.Len(t, uses, 1)
		assert.Equal(t, "$.jobs.'3.10'.steps[0].uses", uses[0].Path().String())

		keys, err := workflow.Nodes(paths.Current().Child("jobs").ChildAll().Key())
		require.NoError(t, err)
		require.Len(t, keys, 3)
		assert.Equal(t, "$.jobs.build~", keys[1].Path().String())
	})

	t.Run("a mapping wildcard on a sequence yields no nodes", func(t *testing.T) {
		t.Parallel()

		nodes, err := doc.Nodes(paths.Current().Child("items").ChildAll())
		require.NoError(t, err)
		assert.Empty(t, nodes)
	})

	t.Run("a merge key that does not resolve is an error bound to the source", func(t *testing.T) {
		t.Parallel()

		broken := yamltest.FirstDocument(t, "jobs:\n  <<: *missing\n  build: 1\n")

		_, err := broken.Nodes(paths.Current().Child("jobs").ChildAll())
		require.ErrorIs(t, err, paths.ErrAlias)

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Same(t, broken.Source(), bound.Source())
	})

	t.Run("keeps document order for nested recursive matches", func(t *testing.T) {
		t.Parallel()

		nested := yamltest.FirstDocument(t, "a:\n  b:\n    a:\n      c: 1\n  c: 2\n")

		// The outer entry a comes first, but its c follows the c of the
		// inner one in the source.
		nodes, err := nested.Nodes(paths.Current().Recursive("a").Child("c"))
		require.NoError(t, err)
		require.Len(t, nodes, 2)
		assert.Equal(t, "$.a.b.a.c", nodes[0].Path().String())
		assert.Equal(t, "$.a.c", nodes[1].Path().String())
	})

	t.Run("a duplicate key scopes the entry the decode keeps", func(t *testing.T) {
		t.Parallel()

		dup, err := niceyaml.NewSourceFromString("a: 1\na: 2\n", niceyaml.WithAllowDuplicateKeys(true)).Document()
		require.NoError(t, err)

		// The path $.a selects the later entry, so `..a` lists only that
		// one.
		entries, err := dup.Nodes(paths.Current().Recursive("a"))
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Equal(t, "$.a", entries[0].Path().String())

		got, err := entries[0].Decode[int](t.Context())
		require.NoError(t, err)
		assert.Equal(t, 2, got)
	})

	t.Run("resolves from the scope of the receiver", func(t *testing.T) {
		t.Parallel()

		spec := yamltest.At(t, doc, paths.Current().Child("spec"))

		images, err := spec.Nodes(paths.Current().Recursive("image"))
		require.NoError(t, err)
		require.Len(t, images, 2)
		assert.Equal(t, "$.spec.image", images[0].Path().String())
	})

	t.Run("a single path yields its one node", func(t *testing.T) {
		t.Parallel()

		nodes, err := doc.Nodes(paths.Current().Child("spec", "image"))
		require.NoError(t, err)
		require.Len(t, nodes, 1)
		assert.Equal(t, "$.spec.image", nodes[0].Path().String())
	})

	t.Run("an empty block scalar stays off the line of its sibling", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "x:\n  a: |\n  b: 1\n")

		nodes, err := dd.Nodes(paths.Current().Child("x", "a"))
		require.NoError(t, err)
		require.Len(t, nodes, 1)
		assert.Equal(t, position.NewSpan(1, 2), nodes[0].Span())
	})

	t.Run("a path that selects nothing yields no nodes", func(t *testing.T) {
		t.Parallel()

		nodes, err := doc.Nodes(paths.Current().Child("missing").IndexAll())
		require.NoError(t, err)
		assert.Empty(t, nodes)
	})

	t.Run("a node through an alias keeps the path as written", func(t *testing.T) {
		t.Parallel()

		nodes, err := doc.Nodes(paths.Current().Child("ref").IndexAll())
		require.NoError(t, err)
		require.Len(t, nodes, 1)
		assert.Equal(t, "$.ref[0]", nodes[0].Path().String())

		// The content lies at the anchor, so the lines are the anchor's.
		assert.Equal(t, position.NewSpan(10, 11), nodes[0].Span())
	})

	t.Run("an error at the root of an aliased scope binds at the alias", func(t *testing.T) {
		t.Parallel()

		ref := yamltest.At(t, doc, paths.Current().Child("ref"))
		assert.Equal(t, position.NewSpan(10, 11), ref.Span())

		// The path points at the alias, so the error binds there, where
		// the document binds the same path, and the view of the scope
		// holds no line to mark.
		err := ref.Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Current())))
		require.EqualError(t, err, "m.yaml:12:6: $.ref: bad")

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.False(t, bound.Annotate(ref.View()))

		err = doc.Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("ref"))))
		require.EqualError(t, err, "m.yaml:12:6: $.ref: bad")
	})

	t.Run("a document with no content yields no nodes", func(t *testing.T) {
		t.Parallel()

		// A document that holds null yields none either, so a loop over
		// the items of each document reads an empty one the same way.
		inputs := map[string]string{
			"empty file":           "",
			"whitespace":           "\n",
			"comments alone":       "# only a comment\n",
			"header alone":         "---\n",
			"directive and header": "%YAML 1.2\n---\n",
			"null":                 "null\n",
		}

		selectors := map[string]paths.Path{
			"every element of a key": paths.Current().Child("items").IndexAll(),
			"every element":          paths.Current().IndexAll(),
			"every entry":            paths.Current().ChildAll(),
			"every node":             paths.Current().RecursiveAll(),
			"key":                    paths.Current().Child("items"),
		}

		for name, input := range inputs {
			for selector, path := range selectors {
				t.Run(name+"/"+selector, func(t *testing.T) {
					t.Parallel()

					nodes, err := yamltest.FirstDocument(t, input).Nodes(path)
					require.NoError(t, err)
					assert.Empty(t, nodes)
				})
			}
		}
	})

	t.Run("the root path selects the null at a header", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input string
			want  int
		}{
			"empty file":           {input: ""},
			"comments alone":       {input: "# only a comment\n"},
			"header alone":         {input: "---\n", want: 1},
			"header and comment":   {input: "---\n# only a comment\n", want: 1},
			"directive and header": {input: "%YAML 1.2\n---\n", want: 1},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				nodes, err := yamltest.FirstDocument(t, tc.input).Nodes(paths.Doc())
				require.NoError(t, err)
				require.Len(t, nodes, tc.want)

				for _, n := range nodes {
					assert.Equal(t, "$", n.Path().String())
				}
			})
		}
	})

	t.Run("At keeps its error in a document with no content", func(t *testing.T) {
		t.Parallel()

		empty := yamltest.FirstDocument(t, "# only a comment\n")

		_, err := empty.At(paths.Current().Child("items"))
		require.ErrorIs(t, err, paths.ErrNotFound)
		require.ErrorIs(t, err, paths.ErrNoDocument)

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Same(t, empty.Source(), bound.Source())
	})

	t.Run("a document that did not parse returns its syntax error", func(t *testing.T) {
		t.Parallel()

		docs := niceyaml.NewSourceFromString("items: [\n").AllDocuments()
		require.Len(t, docs, 1)

		_, err := docs[0].Nodes(paths.Current().Child("items").IndexAll())
		require.ErrorIs(t, err, niceyaml.ErrSyntax)
		assert.Same(t, docs[0].Err(), err)
	})

	t.Run("a path that fans out through nested aliases is refused", func(t *testing.T) {
		t.Parallel()

		bomb := yamltest.FirstDocument(t, yamltest.AliasLevels(5))

		// Each [*] lists ten aliases to the level below, so the path would
		// select 10^5 nodes. The decoder refuses the document too.
		_, err := bomb.Nodes(paths.MustParse("$.a[5][*][*][*][*][*]"))
		require.ErrorIs(t, err, niceyaml.ErrExcessiveAliasing)

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Same(t, bomb.Source(), bound.Source())

		_, err = bomb.Decode[any](t.Context())
		require.ErrorIs(t, err, niceyaml.ErrExcessiveAliasing)
	})
}

// rejectingUnmarshaler decodes itself and reports errUnmarshal, so the
// error the decoder returns is the caller's own rather than go-yaml's.
type rejectingUnmarshaler struct{}

func (*rejectingUnmarshaler) UnmarshalYAML([]byte) error {
	return errUnmarshal
}

// panickingUnmarshaler panics with errUnmarshal when it decodes itself,
// so the decode meets a panic below the node it reads.
type panickingUnmarshaler struct{}

func (*panickingUnmarshaler) UnmarshalYAML([]byte) error {
	panic(errUnmarshal)
}

// wrappingUnmarshaler decodes itself as an int through the decoder and
// wraps the error the decoder returns in errUnmarshal, so the error
// carries go-yaml's error inside the caller's own.
type wrappingUnmarshaler int

func (u *wrappingUnmarshaler) UnmarshalYAML(unmarshal func(any) error) error {
	var n int

	err := unmarshal(&n)
	if err != nil {
		return fmt.Errorf("%w: %w", errUnmarshal, err)
	}

	*u = wrappingUnmarshaler(n)

	return nil
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

// bareReparsingUnmarshaler decodes itself by parsing the bytes it gets
// again and returns the go-yaml error of that parse unwrapped. The parse
// starts at line 1, so the token of its error can share its type, value,
// and position with a token near the top of the source.
type bareReparsingUnmarshaler struct {
	Name []int
}

func (u *bareReparsingUnmarshaler) UnmarshalYAML(data []byte) error {
	var raw struct{ Name []int }

	err := yaml.Unmarshal(data, &raw)
	if err != nil {
		return err //nolint:wrapcheck // The test needs the go-yaml error as it is.
	}

	u.Name = raw.Name

	return nil
}

// cancelAwareUnmarshaler decodes itself by wrapping the error of the
// context it gets, so a canceled context fails the decode. The go-yaml
// decoder never checks the context itself and only passes it to
// unmarshalers like this one.
type cancelAwareUnmarshaler struct{}

func (*cancelAwareUnmarshaler) UnmarshalYAML(ctx context.Context, _ []byte) error {
	err := ctx.Err()
	if err != nil {
		return fmt.Errorf("decode stopped: %w", err)
	}

	return nil
}

// deadlineUnmarshaler decodes itself by wrapping the error of a deadline
// of its own, which the context of the decode has not reached.
type deadlineUnmarshaler struct{}

func (*deadlineUnmarshaler) UnmarshalYAML([]byte) error {
	return fmt.Errorf("lookup stopped: %w", context.DeadlineExceeded)
}

// selfRejecting decodes as a struct and reports errUnmarshal when it
// validates itself.
type selfRejecting struct {
	Value int `yaml:"value"`
}

func (selfRejecting) Validate() error {
	return errUnmarshal
}

func TestNode_ConcurrentPaths(t *testing.T) {
	t.Parallel()

	// Every Node of a document resolves paths through one resolver, which
	// the first path creates, so Nodes of one document resolve paths from
	// several goroutines at once.
	doc, err := niceyaml.NewSourceFromString(stringtest.Input(`
		base: &b
		  name: x
		items: [*b, *b]
		spec:
		  replicas: 1
	`)).Document()
	require.NoError(t, err)

	spec, err := doc.At(paths.Current().Child("spec"))
	require.NoError(t, err)

	var wg sync.WaitGroup

	for range 8 {
		wg.Go(func() {
			node, err := doc.At(paths.Current().Child("items").Index(1).Child("name"))
			if assert.NoError(t, err) {
				assert.Equal(t, position.NewSpan(1, 2), node.Span())
			}

			items, err := doc.Nodes(paths.Current().Child("items").IndexAll())
			if assert.NoError(t, err) {
				assert.Len(t, items, 2)
			}

			ranges, err := spec.Ranges(paths.Current().Child("replicas"))
			if assert.NoError(t, err) {
				want := position.NewRange(position.New(4, 12), position.New(4, 13))
				assert.Equal(t, position.Ranges{want}, ranges)
			}
		})
	}

	wg.Wait()
}

func TestErrDecode(t *testing.T) {
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
		"DecodeInto": {
			input: "value: abc",
			decode: func(ctx context.Context, dd *niceyaml.Node) error {
				var v struct{ Value int }

				return dd.DecodeInto(ctx, &v)
			},
		},
		// The parser makes a null token for each value the document leaves
		// out, which the lexer never saw.
		"mapping value with only a tag": {
			input: "a: null\nc: !!int\n",
			decode: func(ctx context.Context, dd *niceyaml.Node) error {
				node, err := dd.At(paths.Current().Child("c"))
				if err != nil {
					return err //nolint:wrapcheck // The test inspects the error as it is.
				}

				_, err = node.Decode[struct{ X int }](ctx)

				return err
			},
		},
		"sequence item with only a tag": {
			input: "items:\n  - !!int\n",
			decode: func(ctx context.Context, dd *niceyaml.Node) error {
				items, err := dd.Nodes(paths.Current().Child("items").IndexAll())
				if err != nil {
					return err //nolint:wrapcheck // The test inspects the error as it is.
				}

				_, err = items[0].Decode[struct{ X int }](ctx)

				return err
			},
		},
		"tag over no value": {
			input: "a: !!bool\n",
			decode: func(ctx context.Context, dd *niceyaml.Node) error {
				_, err := dd.Decode[map[string]any](ctx)

				return err
			},
		},
	}

	for name, tc := range rejected {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dd := yamltest.FirstDocument(t, tc.input)

			err := tc.decode(t.Context(), dd)
			require.ErrorIs(t, err, niceyaml.ErrDecode)

			var srcErr *niceyaml.SourceError

			require.ErrorAs(t, err, &srcErr, "the rejection is not bound to the source")

			_, ok := errors.AsType[yaml.Error](err)
			assert.True(t, ok, "errors.As no longer finds the go-yaml error")
			assert.NotContains(t, err.Error(), "\n", "the go-yaml excerpt leaked into the message")
		})
	}

	t.Run("rejection the decoder reports without a token", func(t *testing.T) {
		t.Parallel()

		// The decoder reports these with no token of the source, so the
		// decode binds them where the document causes them.
		refs := niceyaml.WithReferences(niceyaml.NewSourceFromString("base: &base {x: 1}\n"))

		tcs := map[string]struct {
			input  string
			path   paths.Path
			source []niceyaml.SourceOption
			line   int
			msg    string
		}{
			"merge alias the references leave unbound": {
				input:  "a:\n  <<: *nope\n  b: 1\n",
				path:   paths.Current(),
				source: []niceyaml.SourceOption{refs},
				line:   1,
				msg:    "cannot find anchor by alias name nope",
			},
			"merge alias after one the references bind": {
				input:  "a:\n  <<: *base\n  b:\n    <<: *nope\n",
				path:   paths.Current(),
				source: []niceyaml.SourceOption{refs},
				line:   3,
				msg:    "cannot find anchor by alias name nope",
			},
			"merge of the mapping that holds it": {
				input: "a: &x\n  <<: *x\n  b: 1\n",
				path:  paths.Current(),
				line:  1,
				msg:   "cannot find anchor by alias name x",
			},
			"merge alias with no anchor": {
				input: "a:\n  <<: *nope\n  b: 1\n",
				path:  paths.Current(),
				line:  1,
				msg:   "cannot find anchor by alias name nope",
			},
			"merge alias before its anchor": {
				input: "a:\n  <<: *y\n  b: 1\nc: &y\n  d: 1\n",
				path:  paths.Current(),
				line:  1,
				msg:   "cannot find anchor by alias name y",
			},
			"merge alias in a list of sources": {
				input: "a: {<<: [{b: 1}, *nope]}\n",
				path:  paths.Current(),
				line:  0,
				msg:   "cannot find anchor by alias name nope",
			},
			"merge alias with no anchor in a scoped decode": {
				input: "a:\n  <<: *nope\n  b: 1\n",
				path:  paths.Current().Child("a"),
				line:  1,
				msg:   "cannot find anchor by alias name nope",
			},
			"merge alias in an anchor the node merges": {
				input: "b: &b {<<: *nope}\nc: {<<: *b}\n",
				path:  paths.Current().Child("c"),
				line:  0,
				msg:   "cannot find anchor by alias name nope",
			},
			"merge of an anchor that holds the node": {
				input: "a: &x\n  m:\n    <<: *x\n",
				path:  paths.Current().Child("a", "m"),
				line:  2,
				msg:   "cannot find anchor by alias name x",
			},
			"merge of an anchor that holds the node after one of the same name": {
				input: "a: &x {k: 1}\nb: &x\n  j: 2\n  <<: *x\n",
				path:  paths.Current().Child("b"),
				line:  3,
				msg:   "cannot find anchor by alias name x",
			},
			"depth limit": {
				input: "a: " + strings.Repeat("[", 10005) + strings.Repeat("]", 10005) + "\n",
				path:  paths.Current(),
				line:  0,
				msg:   "exceeded max depth",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input, tc.source...)

				_, err := yamltest.At(t, dd, tc.path).Decode[any](t.Context())
				require.ErrorIs(t, err, niceyaml.ErrDecode)
				assert.Contains(t, err.Error(), tc.msg)
				assert.NotContains(t, err.Error(), "\n", "the go-yaml excerpt leaked into the message")

				var srcErr *niceyaml.SourceError

				require.ErrorAs(t, err, &srcErr)

				rng, ok := srcErr.Range()
				require.True(t, ok, "the rejection carries no location")
				assert.Equal(t, tc.line, rng.Start.Line)
				assert.Empty(t, srcErr.Members(), "the rejection binds its own causes as children")
			})
		}
	})

	t.Run("rejection in a reference document", func(t *testing.T) {
		t.Parallel()

		// The decoder reports these at a token of the reference document,
		// so the decode binds them at the alias that reads it.
		type cfg struct {
			Item struct{ X int } `yaml:"item"`
		}

		ref := niceyaml.WithReferences(niceyaml.NewSourceFromString("base: &base {x: notint}\nother: &other 1\n"))

		// A line of -1 means the rejection carries no location.
		tcs := map[string]struct {
			input  string
			decode func(ctx context.Context, dd *niceyaml.Node) error
			line   int
		}{
			"merge from a reference": {
				input: "item:\n  <<: *base\ny: 1\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[cfg](ctx)

					return err
				},
				line: 1,
			},
			"alias to a reference": {
				input: "a: 1\nitem: *base\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[cfg](ctx)

					return err
				},
				line: 1,
			},
			"alias to a reference in a scoped decode": {
				input: "a: 1\nouter:\n  item: *base\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					node, err := dd.At(paths.Current().Child("outer"))
					if err != nil {
						return err //nolint:wrapcheck // The test inspects the error as it is.
					}

					_, err = node.Decode[cfg](ctx)

					return err
				},
				line: 2,
			},
			// The decode reads the reference through an anchor outside
			// the node, so it binds at the alias in that anchor.
			"alias to a reference through an anchor in a scoped decode": {
				input: "m: &m {<<: *base}\nouter:\n  item: *m\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					node, err := dd.At(paths.Current().Child("outer"))
					if err != nil {
						return err //nolint:wrapcheck // The test inspects the error as it is.
					}

					_, err = node.Decode[cfg](ctx)

					return err
				},
				line: 0,
			},
			// The token of the rejection does not tell which of the two
			// aliases led to it, so the rejection carries no location even
			// though cfg reads only *base.
			"aliases to two references": {
				input: "a: *other\nitem: *base\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[cfg](ctx)

					return err
				},
				line: -1,
			},
			// The decoder reports a key of an inline map with no token,
			// which the decode cannot tell from a token of a reference
			// document, so it binds the rejection at the alias too.
			"key of an inline map beside an alias to a reference": {
				input: "a: *other\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					var v struct {
						M map[int]int `yaml:",inline"`
					}

					return dd.DecodeInto(ctx, &v)
				},
				line: 0,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input, ref)

				err := tc.decode(t.Context(), dd)
				require.ErrorIs(t, err, niceyaml.ErrDecode)
				assert.Contains(t, err.Error(), "expected integer, got string")
				assert.NotContains(t, err.Error(), "[1:", "go-yaml's position leaked into the message")
				assert.NotContains(t, err.Error(), "base: &base", "go-yaml's excerpt leaked into the message")

				_, ok := errors.AsType[yaml.Error](err)
				assert.True(t, ok, "errors.As no longer finds the go-yaml error")

				var srcErr *niceyaml.SourceError

				require.ErrorAs(t, err, &srcErr)

				rng, ok := srcErr.Range()
				if tc.line < 0 {
					assert.False(t, ok, "the rejection binds at an alias that may not have read it")

					return
				}

				require.True(t, ok, "the rejection carries no location")
				assert.Equal(t, tc.line, rng.Start.Line)
			})
		}
	})

	t.Run("unmarshaler error matches and keeps its chain", func(t *testing.T) {
		t.Parallel()

		type item struct {
			When rejectingUnmarshaler `yaml:"when"`
			X    int                  `yaml:"x"`
		}

		tcs := map[string]struct {
			input  string
			source []niceyaml.SourceOption
			decode func(ctx context.Context, dd *niceyaml.Node) error
		}{
			"top-level value": {
				input: "value: 1",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[rejectingUnmarshaler](ctx)

					return err
				},
			},
			// The document's resolver binds no anchor for the alias, which
			// the reference document defines for the decoder.
			"merge the references resolve": {
				input: "item:\n  <<: *base\n  when: x\n",
				source: []niceyaml.SourceOption{
					niceyaml.WithReferences(niceyaml.NewSourceFromString("base: &base {x: 1}\n")),
				},
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[map[string]item](ctx)

					return err
				},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input, tc.source...)

				err := tc.decode(t.Context(), dd)
				require.ErrorIs(t, err, errUnmarshal)
				require.ErrorIs(t, err, niceyaml.ErrDecode)
			})
		}
	})

	t.Run("wrapped unmarshaler error matches and keeps its chain", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input  string
			decode func(ctx context.Context, dd *niceyaml.Node) error
		}{
			"top-level value": {
				input: "abc",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[wrappingUnmarshaler](ctx)

					return err
				},
			},
			"sequence element": {
				input: "- 1\n- abc\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[[]wrappingUnmarshaler](ctx)

					return err
				},
			},
			"mapping value": {
				input: "a: 1\nb: abc\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[map[string]wrappingUnmarshaler](ctx)

					return err
				},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				err := tc.decode(t.Context(), dd)
				require.ErrorIs(t, err, errUnmarshal)
				require.ErrorIs(t, err, niceyaml.ErrDecode)

				_, ok := errors.AsType[yaml.Error](err)
				assert.True(t, ok, "errors.As no longer finds the go-yaml error")
			})
		}
	})

	t.Run("unmarshaler parse error matches with no location", func(t *testing.T) {
		t.Parallel()

		// The error of the value's own parse carries a token of the bytes
		// it parsed, not of the source, so it comes back as the value's
		// own error rather than as a rejection at the wrong line.
		dd := yamltest.FirstDocument(t, "a: 1\nb: x\nc: y\nz:\n  n: notanumber\n")

		_, err := dd.Decode[struct{ Z reparsingUnmarshaler }](t.Context())
		require.ErrorIs(t, err, niceyaml.ErrDecode)

		var srcErr *niceyaml.SourceError

		require.ErrorAs(t, err, &srcErr)

		_, ok := srcErr.Range()
		assert.False(t, ok, "the error took a location from the value's own parse")
	})

	t.Run("unmarshaler parse error matching a source token matches with no location", func(t *testing.T) {
		t.Parallel()

		// The item's own parse fails at "abc" on its line 1, where the
		// source holds the same token at the same position.
		dd := yamltest.FirstDocument(t, "name: [abc]\nitems:\n  - name: [abc]\n")

		_, err := dd.Decode[struct {
			Name  []string
			Items []bareReparsingUnmarshaler
		}](t.Context())
		require.ErrorIs(t, err, niceyaml.ErrDecode)

		var srcErr *niceyaml.SourceError

		require.ErrorAs(t, err, &srcErr)

		_, ok := srcErr.Range()
		assert.False(t, ok, "the error took a location from the value's own parse")
	})

	t.Run("decoder error without a token matches with no location", func(t *testing.T) {
		t.Parallel()

		type inner struct {
			A int `yaml:"a"`
		}

		// The decoder reports these with no token to bind them to, and
		// none comes from a value that decodes itself, so the decode finds
		// no value to bind them at. A go-yaml release that gives one a
		// token gives its case a location.
		tcs := map[string]struct {
			input  string
			decode func(ctx context.Context, dd *niceyaml.Node) error
			msg    string
		}{
			"duplicated struct field name": {
				input: "a: 1\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					var v struct {
						A int `yaml:"a"`
						B int `yaml:"a"`
					}

					return dd.DecodeInto(ctx, &v)
				},
				msg: "duplicated struct field name a",
			},
			"unexported inline embedded struct": {
				input: "a: 1\nm: 2\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[struct {
						*inner `yaml:",inline"`

						M int `yaml:"m"`
					}](ctx)

					return err
				},
				msg: "cannot set embedded type as unexported field",
			},
			// The decoder builds the mapping of an inline field itself,
			// so the mapping and its keys carry no token.
			"key of an inline map": {
				input: "a: 1\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					var v struct {
						M map[int]int `yaml:",inline"`
					}

					return dd.DecodeInto(ctx, &v)
				},
				msg: "cannot unmarshal string into Go struct field .M of type int",
			},
			"inline field that cannot hold a mapping": {
				input: "a: 1\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					var v struct {
						N int `yaml:",inline"`
					}

					return dd.DecodeInto(ctx, &v)
				},
				msg: "cannot unmarshal map[string]interface {} into Go struct field .N of type int",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				err := tc.decode(t.Context(), dd)
				require.ErrorContains(t, err, tc.msg)
				require.ErrorIs(t, err, niceyaml.ErrDecode)

				var srcErr *niceyaml.SourceError

				require.ErrorAs(t, err, &srcErr)

				_, ok := srcErr.Range()
				assert.False(t, ok, "the error took a location")
			})
		}
	})

	t.Run("value of an inline map matches at its token", func(t *testing.T) {
		t.Parallel()

		// The mapping the decoder builds for an inline field holds the
		// values of the source, so a value keeps its token though its
		// key has none.
		dd := yamltest.FirstDocument(t, "a: 1\nb: x\n")

		var v struct {
			M map[string]int `yaml:",inline"`
		}

		err := dd.DecodeInto(t.Context(), &v)
		require.EqualError(t, err, "2:4: $.b: expected integer, got string")
		require.ErrorIs(t, err, niceyaml.ErrDecode)

		var srcErr *niceyaml.SourceError

		require.ErrorAs(t, err, &srcErr)

		rng, ok := srcErr.Range()
		require.True(t, ok, "the rejection carries no location")
		assert.Equal(t, position.New(1, 3), rng.Start)
	})

	t.Run("panic does not match", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "# note\nvalue: 1\n")

		_, err := dd.Decode[panickingUnmarshaler](t.Context())
		require.NotErrorIs(t, err, niceyaml.ErrDecode)
		require.EqualError(t, err, "2:1: decoder panicked: unmarshaler rejected the value")
		requirePanic(t, err)
	})

	t.Run("decode inside a validator matches", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "value: abc\n")

		decodes := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
			_, err := n.Decode[struct{ Value int }](ctx)

			return err
		})

		err := dd.Validate(t.Context(), decodes)
		require.ErrorIs(t, err, niceyaml.ErrDecode)

		_, err = dd.Decode[any](t.Context(), niceyaml.WithValidator(decodes))
		require.ErrorIs(t, err, niceyaml.ErrDecode)
	})

	t.Run("validator error does not match", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "value: 1\n")

		rejects := niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
			return niceyaml.NewError("too low", niceyaml.AtPath(paths.Current().Child("value")))
		})

		_, err := dd.Decode[struct{ Value int }](t.Context(), niceyaml.WithValidator(rejects))
		require.EqualError(t, err, "1:8: $.value: too low")
		require.NotErrorIs(t, err, niceyaml.ErrDecode)
	})

	t.Run("self validator error does not match", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "value: 1\n")

		_, err := dd.Decode[selfRejecting](t.Context())
		require.ErrorIs(t, err, errUnmarshal)
		require.NotErrorIs(t, err, niceyaml.ErrDecode)
	})

	t.Run("excessive aliasing does not match", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, yamltest.AliasLevels(10))

		_, err := dd.Decode[any](t.Context())
		require.ErrorIs(t, err, niceyaml.ErrExcessiveAliasing)
		require.NotErrorIs(t, err, niceyaml.ErrDecode)
	})

	t.Run("decode target does not match", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "value: 1")

		err := dd.DecodeInto(t.Context(), nil)
		require.ErrorIs(t, err, niceyaml.ErrDecodeTarget)
		require.NotErrorIs(t, err, niceyaml.ErrDecode)
	})

	t.Run("parse error does not match", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("a: [\n")

		_, err := source.File()
		require.ErrorIs(t, err, niceyaml.ErrSyntax)
		require.NotErrorIs(t, err, niceyaml.ErrDecode)

		docs := source.AllDocuments()
		require.Len(t, docs, 1)

		_, err = docs[0].Decode[any](t.Context())
		require.ErrorIs(t, err, niceyaml.ErrSyntax)
		require.NotErrorIs(t, err, niceyaml.ErrDecode)
	})

	t.Run("canceled context does not match", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "value: 1")

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := dd.Decode[cancelAwareUnmarshaler](ctx)
		require.ErrorIs(t, err, context.Canceled)
		require.NotErrorIs(t, err, niceyaml.ErrDecode)
	})

	t.Run("context error an unmarshaler wraps does not match", func(t *testing.T) {
		t.Parallel()

		// The context of the decode has not ended, and the value reports
		// the error of a deadline of its own.
		tcs := map[string]struct {
			decode func(ctx context.Context, dd *niceyaml.Node) error
			want   string
		}{
			"value the decode locates": {
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[struct{ Value deadlineUnmarshaler }](ctx)

					return err
				},
				want: "lookup stopped: context deadline exceeded",
			},
			"node the decode reads": {
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[deadlineUnmarshaler](ctx)

					return err
				},
				want: "lookup stopped: context deadline exceeded",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, "value: 1\n")

				err := tc.decode(t.Context(), dd)
				require.EqualError(t, err, tc.want)
				require.ErrorIs(t, err, context.DeadlineExceeded)
				require.NotErrorIs(t, err, niceyaml.ErrDecode)
			})
		}
	})

	t.Run("canceled context stops a decode that reads an anchor", func(t *testing.T) {
		t.Parallel()

		// The alias under b needs the anchor under a, which the decode
		// would otherwise skip and report as missing.
		dd := yamltest.FirstDocument(t, "a: &x 1\nb:\n  c: *x\n")

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		tcs := map[string]struct {
			node *niceyaml.Node
		}{
			"scoped": {node: yamltest.At(t, dd, paths.Current().Child("b"))},
			"root":   {node: dd},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				_, err := tc.node.Decode[map[string]any](ctx)
				require.ErrorIs(t, err, context.Canceled)
				require.NotErrorIs(t, err, niceyaml.ErrDecode)
			})
		}
	})
}

// TestValidator_DirectCall calls the Check method of every validator the
// library ships, which binds the error of the validator it wraps as
// [niceyaml.Node.Validate] would.
func TestValidator_DirectCall(t *testing.T) {
	t.Parallel()

	itemPath := paths.Doc().Child("items").Index(1)

	// A rule of a type of its own that leaves its error unbound, so the
	// validator around it has an error to bind.
	rule := &fieldValidator{
		err: niceyaml.NewError("reserved name", niceyaml.AtPath(paths.Current().Child("name"))),
	}

	nameSchema := schema.MustCompile([]byte(`{
		"type": "object",
		"properties": {"name": {"not": {"const": "admin"}}}
	}`))
	reg := schema.NewRegistry(schema.WithResolvers(nameSchema))

	tcs := map[string]struct {
		v niceyaml.Validator
		// The Node the validator runs on.
		path paths.Path
		want string
	}{
		"Schema": {
			v:    nameSchema,
			path: itemPath,
			want: "c.yaml:4:11: $.items[1].name: should not validate against the schema",
		},
		"Registry refuses a scoped Node": {
			v:    reg,
			path: itemPath,
			want: "c.yaml: registry needs a whole document: node is scoped to $.items[1]",
		},
		"Registry at the root": {
			v:    reg,
			path: paths.Doc(),
			want: "c.yaml:1:7: $.name: should not validate against the schema",
		},
		"ValidatorFunc": {
			v:    niceyaml.ValidatorFunc(rule.Check),
			path: itemPath,
			want: "c.yaml:4:11: $.items[1].name: reserved name",
		},
		"MultiValidator": {
			v:    niceyaml.MultiValidator(rule, nameSchema),
			path: itemPath,
			want: "c.yaml:4:11: $.items[1].name: reserved name\n" +
				"c.yaml:4:11: $.items[1].name: should not validate against the schema",
		},
		"ChainValidator": {
			v:    niceyaml.ChainValidator(rule, nameSchema),
			path: itemPath,
			want: "c.yaml:4:11: $.items[1].name: reserved name",
		},
		"SkipEmpty": {
			v:    niceyaml.SkipEmpty(rule),
			path: itemPath,
			want: "c.yaml:4:11: $.items[1].name: reserved name",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, stringtest.Input(`
				name: admin
				items:
				  - name: soup
				  - name: admin
			`), niceyaml.WithName("c.yaml"))
			node := yamltest.At(t, doc, tc.path)

			err := tc.v.Check(t.Context(), node)
			require.EqualError(t, err, tc.want)
		})
	}
}

func TestValidatorFunc_Check(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString(stringtest.Input(`
		price: 5
		items:
		  - price: 1
		  - price: -2
	`), niceyaml.WithName("menu.yaml"))

	doc, err := source.Document()
	require.NoError(t, err)

	other, err := niceyaml.NewSourceFromString("# other\nprice: 0\n", niceyaml.WithName("other.yaml")).Document()
	require.NoError(t, err)

	pricePath := paths.Current().Child("price")
	item := yamltest.At(t, doc, paths.Current().Child("items").Index(1))
	negative := niceyaml.NewError("negative price", niceyaml.AtPath(pricePath))

	var (
		nilError       *niceyaml.Error
		nilSourceError *niceyaml.SourceError
	)

	tcs := map[string]struct {
		// The error the function returns, and the node it runs on.
		returns error
		node    *niceyaml.Node
		// The message of the bound error, or none for no error.
		want string
	}{
		"an unbound error binds through the node": {
			returns: negative,
			node:    doc,
			want:    "menu.yaml:1:8: $.price: negative price",
		},
		"a scoped node puts its path in front": {
			returns: negative,
			node:    item,
			want:    "menu.yaml:4:12: $.items[1].price: negative price",
		},
		"an error with no location names the source": {
			returns: errNameRequired,
			node:    doc,
			want:    "menu.yaml: name is required",
		},
		"an error bound through another node stays as it is": {
			returns: item.Bind(negative),
			node:    doc,
			want:    "menu.yaml:4:12: $.items[1].price: negative price",
		},
		"an error bound to another source stays as it is": {
			returns: other.Bind(negative),
			node:    doc,
			want:    "other.yaml:2:8: $.price: negative price",
		},
		"no error is no error": {
			node: doc,
		},
		"a nil Error pointer is no error": {
			returns: nilError,
			node:    doc,
		},
		"a nil SourceError pointer is no error": {
			returns: nilSourceError,
			node:    doc,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			validator := rejectingValidator(tc.returns)

			err := validator.Check(t.Context(), tc.node)
			if tc.want == "" {
				require.NoError(t, err)
				require.NoError(t, tc.node.Validate(t.Context(), validator))

				return
			}

			require.EqualError(t, err, tc.want)

			var bound *niceyaml.SourceError

			require.ErrorAs(t, err, &bound)

			// The node returns the same error for the validator.
			require.EqualError(t, tc.node.Validate(t.Context(), validator), tc.want)
		})
	}

	t.Run("a validator that runs another on each element reports the element", func(t *testing.T) {
		t.Parallel()

		perItem := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
			node, err := n.At(pricePath)
			if err != nil {
				return err //nolint:wrapcheck // The test inspects the error as it is.
			}

			price, err := node.Decode[int](ctx)
			if err != nil {
				return err
			}

			if price < 0 {
				return negative
			}

			return nil
		})
		each := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
			items, err := n.Nodes(paths.Current().Child("items").IndexAll())
			if err != nil {
				return err //nolint:wrapcheck // The test inspects the error as it is.
			}

			for _, it := range items {
				err := perItem.Check(ctx, it)
				if err != nil {
					return err //nolint:wrapcheck // The test inspects the error as it is.
				}
			}

			return nil
		})

		want := "menu.yaml:4:12: $.items[1].price: negative price"

		require.EqualError(t, doc.Validate(t.Context(), each), want)

		_, err := doc.Decode[map[string]any](t.Context(), niceyaml.WithValidator(each))
		require.EqualError(t, err, want)
	})

	t.Run("a validator that checks another document names that document", func(t *testing.T) {
		t.Parallel()

		zero := niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
			return niceyaml.NewError("no price", niceyaml.AtPath(pricePath))
		})
		include := niceyaml.ValidatorFunc(func(ctx context.Context, _ *niceyaml.Node) error {
			return zero.Check(ctx, other)
		})

		require.EqualError(t, doc.Validate(t.Context(), include), "other.yaml:2:8: $.price: no price")
	})

	t.Run("context a wrapper adds stands in front of the position", func(t *testing.T) {
		t.Parallel()

		wrapped := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
			return fmt.Errorf("menu check: %w", rejectingValidator(negative).Check(ctx, n))
		})

		err := doc.Validate(t.Context(), wrapped)
		require.EqualError(t, err, "menu check: menu.yaml:1:8: $.price: negative price")

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Same(t, doc, bound.Node())
	})
}

func TestMultiValidator(t *testing.T) {
	t.Parallel()

	errB := errors.New("bad b")
	errC := errors.New("bad c")

	badB := niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
		return niceyaml.Invalid(errB, niceyaml.AtPath(paths.Current().Child("a", "b")))
	})
	badC := niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
		// A validator that binds its own error, as a schema does.
		return n.Bind(niceyaml.Invalid(errC, niceyaml.AtPath(paths.Current().Child("a", "c"))))
	})
	// A validator of a type of its own that leaves its error unbound.
	unboundB := &fieldValidator{err: niceyaml.Invalid(errB, niceyaml.AtPath(paths.Current().Child("a", "b")))}
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

	t.Run("skips a nil validator", func(t *testing.T) {
		t.Parallel()

		var order []string

		err := dd.Validate(t.Context(), niceyaml.MultiValidator(
			badB,
			nil,
			(*fieldValidator)(nil),
			niceyaml.ValidatorFunc(nil),
			record(&order, "after"),
		))
		require.ErrorIs(t, err, errB)
		assert.Equal(t, []string{"after"}, order)
	})

	t.Run("keeps its validators when the caller edits the slice", func(t *testing.T) {
		t.Parallel()

		vs := []niceyaml.Validator{badB}
		multi := niceyaml.MultiValidator(vs...)
		vs[0] = passing

		err := dd.Validate(t.Context(), multi)
		require.ErrorIs(t, err, errB)
	})

	t.Run("WithValidator carries it to a decode", func(t *testing.T) {
		t.Parallel()

		multi := niceyaml.WithValidator(niceyaml.MultiValidator(badB, badC))

		_, err := dd.Decode[map[string]any](t.Context(), multi)
		require.ErrorIs(t, err, errB)
		require.ErrorIs(t, err, errC)
	})

	t.Run("each line of the message carries its source and position", func(t *testing.T) {
		t.Parallel()

		src := niceyaml.NewSourceFromString("a:\n  b: 1\n  c: 2\n", niceyaml.WithFilePath("x.yaml"))

		doc, err := src.Document()
		require.NoError(t, err)

		want := "x.yaml:2:6: $.a.b: bad b\nx.yaml:3:6: $.a.c: bad c"

		tcs := map[string]struct {
			first niceyaml.Validator
		}{
			"a validator that binds its error":          {first: badB},
			"a validator that leaves its error unbound": {first: unboundB},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				multi := niceyaml.MultiValidator(tc.first, badC)

				require.EqualError(t, doc.Validate(t.Context(), multi), want)
				require.EqualError(t, multi.Check(t.Context(), doc), want)
			})
		}
	})

	t.Run("a lone failure keeps its position", func(t *testing.T) {
		t.Parallel()

		src := niceyaml.NewSourceFromString("a:\n  b: 1\n  c: 2\n", niceyaml.WithFilePath("x.yaml"))

		doc, err := src.Document()
		require.NoError(t, err)

		err = doc.Validate(t.Context(), niceyaml.MultiValidator(badB, passing))
		require.ErrorIs(t, err, errB)
		assert.Equal(t, "x.yaml:2:6: $.a.b: bad b", err.Error())

		var serr *niceyaml.SourceError

		require.ErrorAs(t, err, &serr)

		_, ok := serr.Range()
		assert.True(t, ok)
		assert.Empty(t, serr.Members())

		// A validator that binds its own error names the source once.
		err = doc.Validate(t.Context(), niceyaml.MultiValidator(passing, badC))
		require.ErrorIs(t, err, errC)
		assert.Equal(t, "x.yaml:3:6: $.a.c: bad c", err.Error())

		// A validator that leaves its error unbound reads the same.
		err = doc.Validate(t.Context(), niceyaml.MultiValidator(passing, unboundB))
		require.ErrorIs(t, err, errB)
		assert.Equal(t, "x.yaml:2:6: $.a.b: bad b", err.Error())
	})

	t.Run("a sentinel around a binding stands in front of its position", func(t *testing.T) {
		t.Parallel()

		src := niceyaml.NewSourceFromString("a:\n  b: 1\n  c: 2\n", niceyaml.WithFilePath("x.yaml"))

		doc, err := src.Document()
		require.NoError(t, err)

		errInvalid := errors.New("invalid")
		classified := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
			return fmt.Errorf("%w: %w", errInvalid, badC.Check(ctx, n))
		})

		err = doc.Validate(t.Context(), niceyaml.MultiValidator(classified, badB))
		require.ErrorIs(t, err, errInvalid)
		assert.Equal(t, "x.yaml:2:6: $.a.b: bad b\ninvalid: x.yaml:3:6: $.a.c: bad c", err.Error())
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

func TestChainValidator(t *testing.T) {
	t.Parallel()

	errFirst := errors.New("first rule")
	errSecond := errors.New("second rule")

	namePath := paths.Current().Child("name")

	// Both validators fail at the same value, as a schema and a check
	// that decodes the node do.
	first := niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
		return niceyaml.Invalid(errFirst, niceyaml.AtPath(namePath))
	})
	second := niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
		return niceyaml.Invalid(errSecond, niceyaml.AtPath(namePath))
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

	newDoc := func(t *testing.T) *niceyaml.Node {
		t.Helper()

		doc, err := niceyaml.NewSourceFromString("meta:\n  name: 5\nname: 7\n", niceyaml.WithName("x.yaml")).Document()
		require.NoError(t, err)

		return doc
	}

	t.Run("runs the validators in order and stops at the first that fails", func(t *testing.T) {
		t.Parallel()

		var order []string

		chain := niceyaml.ChainValidator(
			record(&order, "before"),
			first,
			record(&order, "unreached"),
			second,
		)

		err := newDoc(t).Validate(t.Context(), chain)
		require.EqualError(t, err, "x.yaml:3:7: $.name: first rule")
		require.NotErrorIs(t, err, errSecond)
		assert.Equal(t, []string{"before"}, order)
	})

	t.Run("reports the first of two failures where MultiValidator reports both", func(t *testing.T) {
		t.Parallel()

		doc := newDoc(t)

		err := doc.Validate(t.Context(), niceyaml.ChainValidator(first, second))
		require.EqualError(t, err, "x.yaml:3:7: $.name: first rule")

		err = doc.Validate(t.Context(), niceyaml.ChainValidator(second, first))
		require.EqualError(t, err, "x.yaml:3:7: $.name: second rule")

		err = doc.Validate(t.Context(), niceyaml.MultiValidator(first, second))
		require.EqualError(t, err, "x.yaml:3:7: $.name: first rule\nx.yaml:3:7: $.name: second rule")
	})

	t.Run("a later validator runs once the ones before it pass", func(t *testing.T) {
		t.Parallel()

		var order []string

		// A typed nil pointer is no failure, so the chain goes on.
		err := newDoc(t).Validate(t.Context(), niceyaml.ChainValidator(
			record(&order, "before"),
			typedNilValidator(),
			niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
				var e *niceyaml.SourceError

				return e
			}),
			record(&order, "after"),
			second,
		))
		require.EqualError(t, err, "x.yaml:3:7: $.name: second rule")
		assert.Equal(t, []string{"before", "after"}, order)
	})

	t.Run("no failure is no error", func(t *testing.T) {
		t.Parallel()

		doc := newDoc(t)

		require.NoError(t, doc.Validate(t.Context(), niceyaml.ChainValidator()))
		require.NoError(t, niceyaml.ChainValidator().Check(t.Context(), doc))
		require.NoError(t, doc.Validate(t.Context(), niceyaml.ChainValidator(passing, passing)))
		require.NoError(t, doc.Validate(t.Context(), niceyaml.ChainValidator(typedNilValidator())))
	})

	t.Run("skips a nil validator", func(t *testing.T) {
		t.Parallel()

		var order []string

		doc := newDoc(t)

		err := doc.Validate(t.Context(), niceyaml.ChainValidator(
			record(&order, "before"),
			nil,
			(*fieldValidator)(nil),
			niceyaml.ValidatorFunc(nil),
			record(&order, "after"),
			first,
		))
		require.ErrorIs(t, err, errFirst)
		assert.Equal(t, []string{"before", "after"}, order)

		require.NoError(t, doc.Validate(t.Context(), niceyaml.ChainValidator(nil)))
	})

	t.Run("keeps its validators when the caller edits the slice", func(t *testing.T) {
		t.Parallel()

		vs := []niceyaml.Validator{first}
		chain := niceyaml.ChainValidator(vs...)
		vs[0] = passing

		require.ErrorIs(t, newDoc(t).Validate(t.Context(), chain), errFirst)
	})

	t.Run("a document that did not parse returns its syntax error", func(t *testing.T) {
		t.Parallel()

		docs := niceyaml.NewSourceFromString("a: [\n").AllDocuments()
		require.Len(t, docs, 1)
		require.ErrorIs(t, docs[0].Err(), niceyaml.ErrSyntax)

		var order []string

		chain := niceyaml.ChainValidator(record(&order, "unreached"))

		assert.Same(t, docs[0].Err(), docs[0].Validate(t.Context(), chain))
		assert.Same(t, docs[0].Err(), chain.Check(t.Context(), docs[0]))
		assert.Empty(t, order)
	})

	t.Run("binds the failure as Node.Validate binds it", func(t *testing.T) {
		t.Parallel()

		doc := newDoc(t)
		meta := yamltest.At(t, doc, paths.Current().Child("meta"))
		located := niceyaml.Invalid(errFirst, niceyaml.AtPath(namePath))

		tcs := map[string]struct {
			v    niceyaml.Validator
			node *niceyaml.Node
			want string
		}{
			"a validator that binds its error": {
				v:    first,
				node: doc,
				want: "x.yaml:3:7: $.name: first rule",
			},
			"a validator that leaves its error unbound": {
				v:    &fieldValidator{err: located},
				node: doc,
				want: "x.yaml:3:7: $.name: first rule",
			},
			"an unbound error with an @ path on a scoped Node": {
				v:    &fieldValidator{err: located},
				node: meta,
				want: "x.yaml:2:9: $.meta.name: first rule",
			},
			"an unbound error with no location on a scoped Node": {
				v:    &fieldValidator{err: niceyaml.Invalid(errFirst)},
				node: meta,
				want: "x.yaml:1:1: $.meta: first rule",
			},
			"an unbound plain error on a scoped Node": {
				v:    &fieldValidator{err: errFirst},
				node: meta,
				want: "x.yaml:1:1: $.meta: first rule",
			},
			"an unbound plain error on the root": {
				v:    &fieldValidator{err: errFirst},
				node: doc,
				want: "x.yaml: first rule",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				alone := tc.node.Validate(t.Context(), tc.v)
				require.EqualError(t, alone, tc.want)

				chain := niceyaml.ChainValidator(passing, tc.v, second)

				// A direct call returns the error Node.Validate returns.
				for _, err := range []error{
					tc.node.Validate(t.Context(), chain),
					chain.Check(t.Context(), tc.node),
				} {
					require.EqualError(t, err, tc.want)
					assert.Equal(t, niceyaml.IsInvalid(alone), niceyaml.IsInvalid(err))
					assert.Equal(t, niceyaml.FormatError(alone), niceyaml.FormatError(err))

					var bound *niceyaml.SourceError

					require.ErrorAs(t, err, &bound)
					assert.Same(t, tc.node, bound.Node())
				}
			})
		}
	})

	t.Run("returns a bound error as it is", func(t *testing.T) {
		t.Parallel()

		doc := newDoc(t)
		meta := yamltest.At(t, doc, paths.Current().Child("meta"))

		want := meta.Bind(niceyaml.Invalid(errFirst, niceyaml.AtPath(namePath)))

		assert.Same(t, want, doc.Validate(t.Context(), niceyaml.ChainValidator(&fieldValidator{err: want})))
		assert.Same(t, want, doc.Validate(t.Context(), niceyaml.ChainValidator(rejectingValidator(want))))
	})

	t.Run("a decode runs repeated WithValidator options the same way", func(t *testing.T) {
		t.Parallel()

		unboundFirst := &fieldValidator{err: niceyaml.Invalid(errFirst, niceyaml.AtPath(namePath))}
		unboundSecond := &fieldValidator{err: niceyaml.Invalid(errSecond, niceyaml.AtPath(namePath))}

		tcs := map[string]struct {
			a, b niceyaml.Validator
			want string
			// Whether the Node at $.meta decodes, and not the root.
			scoped bool
		}{
			"both fail": {
				a:    first,
				b:    second,
				want: "x.yaml:3:7: $.name: first rule",
			},
			"both fail and leave their errors unbound": {
				a:    unboundFirst,
				b:    unboundSecond,
				want: "x.yaml:3:7: $.name: first rule",
			},
			"the second fails": {
				a:    passing,
				b:    second,
				want: "x.yaml:3:7: $.name: second rule",
			},
			"the second fails and leaves its error unbound": {
				a:    typedNilValidator(),
				b:    unboundSecond,
				want: "x.yaml:3:7: $.name: second rule",
			},
			"the first is nil": {
				a:    nil,
				b:    second,
				want: "x.yaml:3:7: $.name: second rule",
			},
			"an unbound error with an @ path on a scoped Node": {
				a:      unboundFirst,
				b:      unboundSecond,
				scoped: true,
				want:   "x.yaml:2:9: $.meta.name: first rule",
			},
			"an unbound plain error on a scoped Node": {
				a:      passing,
				b:      &fieldValidator{err: errSecond},
				scoped: true,
				want:   "x.yaml:1:1: $.meta: second rule",
			},
			"neither fails": {
				a: passing,
				b: typedNilValidator(),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				node := newDoc(t)
				if tc.scoped {
					node = yamltest.At(t, node, paths.Current().Child("meta"))
				}

				chain := niceyaml.ChainValidator(tc.a, tc.b)

				_, chainedDecode := node.Decode[map[string]any](t.Context(), niceyaml.WithValidator(chain))
				_, repeatedDecode := node.Decode[map[string]any](t.Context(),
					niceyaml.WithValidator(tc.a),
					niceyaml.WithValidator(tc.b),
				)

				pairs := map[string][2]error{
					"Decode":   {chainedDecode, repeatedDecode},
					"Validate": {node.Validate(t.Context(), chain), repeatedDecode},
				}

				for step, pair := range pairs {
					got, want := pair[0], pair[1]

					if tc.want == "" {
						require.NoError(t, got, step)
						require.NoError(t, want, step)

						continue
					}

					require.EqualError(t, want, tc.want, step)
					require.EqualError(t, got, tc.want, step)
					assert.Equal(t, niceyaml.FormatError(want), niceyaml.FormatError(got), step)
					assert.Equal(t, fmt.Sprintf("%+v", want), fmt.Sprintf("%+v", got), step)
					assert.Equal(t, niceyaml.IsInvalid(want), niceyaml.IsInvalid(got), step)

					var gotBound, wantBound *niceyaml.SourceError

					require.ErrorAs(t, got, &gotBound, step)
					require.ErrorAs(t, want, &wantBound, step)
					assert.Same(t, node, wantBound.Node(), step)
					assert.Same(t, node, gotBound.Node(), step)

					wantPath, wantOK := wantBound.Path()
					gotPath, gotOK := gotBound.Path()
					assert.Equal(t, wantOK, gotOK, step)
					assert.Equal(t, wantPath.String(), gotPath.String(), step)
				}
			})
		}
	})

	t.Run("takes its place inside MultiValidator and SkipEmpty", func(t *testing.T) {
		t.Parallel()

		doc := newDoc(t)

		// Inside a MultiValidator, the chain reports its first failure
		// beside the failures of the validators around it.
		multi := niceyaml.MultiValidator(niceyaml.ChainValidator(first, second), second)
		require.EqualError(t, doc.Validate(t.Context(), multi),
			"x.yaml:3:7: $.name: first rule\nx.yaml:3:7: $.name: second rule")

		// Around one, the chain stops once the MultiValidator fails.
		var order []string

		chain := niceyaml.ChainValidator(niceyaml.MultiValidator(first, second), record(&order, "unreached"))
		require.EqualError(t, doc.Validate(t.Context(), chain),
			"x.yaml:3:7: $.name: first rule\nx.yaml:3:7: $.name: second rule")
		assert.Empty(t, order)

		// A chain inside a chain runs as one chain.
		nested := niceyaml.ChainValidator(niceyaml.ChainValidator(passing, second), first)
		require.EqualError(t, doc.Validate(t.Context(), nested), "x.yaml:3:7: $.name: second rule")

		empty := yamltest.FirstDocument(t, "# only a comment\n")

		// Inside a SkipEmpty, no validator of the chain runs on a document
		// with no content.
		require.NoError(t, empty.Validate(t.Context(), niceyaml.SkipEmpty(niceyaml.ChainValidator(first, second))))
		require.ErrorIs(
			t,
			doc.Validate(t.Context(), niceyaml.SkipEmpty(niceyaml.ChainValidator(first, second))),
			errFirst,
		)

		// Around one, the validators after it still run.
		skipFirst := niceyaml.ChainValidator(niceyaml.SkipEmpty(first), second)
		require.ErrorIs(t, empty.Validate(t.Context(), skipFirst), errSecond)
		require.ErrorIs(t, doc.Validate(t.Context(), skipFirst), errFirst)
	})

	t.Run("a context that ends stops the run at the validator that reports it", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())

		var order []string

		canceling := niceyaml.ValidatorFunc(func(ctx context.Context, _ *niceyaml.Node) error {
			cancel()

			return ctx.Err()
		})

		err := newDoc(t).Validate(ctx, niceyaml.ChainValidator(canceling, record(&order, "unreached")))
		require.ErrorIs(t, err, context.Canceled)
		assert.Empty(t, order)
	})
}

func TestSkipEmpty(t *testing.T) {
	t.Parallel()

	errBad := errors.New("bad")

	// Rejecting returns a validator that fails every Node and counts its
	// runs.
	rejecting := func(calls *int) niceyaml.Validator {
		return niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
			*calls++

			return niceyaml.Invalid(errBad)
		})
	}

	t.Run("passes a document with no content", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input string
		}{
			"empty file":           {input: ""},
			"whitespace":           {input: "\n"},
			"comments alone":       {input: "# only a comment\n"},
			"header alone":         {input: "---\n"},
			"header and comment":   {input: "---\n# Source: chart/templates/empty.yaml\n"},
			"directive and header": {input: "%YAML 1.2\n---\n"},
			"end marker alone":     {input: "...\n"},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, tc.input)

				var calls int

				skip := niceyaml.SkipEmpty(rejecting(&calls))

				require.NoError(t, doc.Validate(t.Context(), skip))
				require.NoError(t, skip.Check(t.Context(), doc))
				assert.Zero(t, calls)

				// The validator alone still runs on the document.
				require.ErrorIs(t, doc.Validate(t.Context(), rejecting(&calls)), errBad)
				assert.Equal(t, 1, calls)
			})
		}
	})

	t.Run("runs the validator on a document that holds a value", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input string
		}{
			"null":                 {input: "null\n"},
			"tilde":                {input: "~\n"},
			"null below a header":  {input: "--- null\n"},
			"empty mapping":        {input: "{}\n"},
			"empty sequence":       {input: "[]\n"},
			"empty string":         {input: "\"\"\n"},
			"alias with no anchor": {input: "--- *nope\n"},
			"mapping":              {input: "a: 1\n"},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, tc.input)

				var calls int

				err := doc.Validate(t.Context(), niceyaml.SkipEmpty(rejecting(&calls)))
				require.ErrorIs(t, err, errBad)
				assert.Equal(t, 1, calls)
			})
		}
	})

	t.Run("runs the validator on a scoped Node", func(t *testing.T) {
		t.Parallel()

		// The key holds no value of its own, and its Node is no document.
		value := yamltest.At(t, yamltest.FirstDocument(t, "a:\n"), paths.Current().Child("a"))

		var calls int

		err := value.Validate(t.Context(), niceyaml.SkipEmpty(rejecting(&calls)))
		require.ErrorIs(t, err, errBad)
		assert.Equal(t, 1, calls)
	})

	t.Run("a document that did not parse returns its syntax error", func(t *testing.T) {
		t.Parallel()

		for _, input := range []string{"a: [\n", "---\na: [\n"} {
			docs := niceyaml.NewSourceFromString(input).AllDocuments()
			require.Len(t, docs, 1, input)
			require.ErrorIs(t, docs[0].Err(), niceyaml.ErrSyntax, input)

			var calls int

			skip := niceyaml.SkipEmpty(rejecting(&calls))

			assert.Same(t, docs[0].Err(), docs[0].Validate(t.Context(), skip), input)
			assert.Same(t, docs[0].Err(), skip.Check(t.Context(), docs[0]), input)
			assert.Zero(t, calls, input)
		}
	})

	t.Run("binds the error of the validator through the Node", func(t *testing.T) {
		t.Parallel()

		src := niceyaml.NewSourceFromString("a:\n  b: 1\n", niceyaml.WithFilePath("x.yaml"))

		doc, err := src.Document()
		require.NoError(t, err)

		located := niceyaml.Invalid(errBad, niceyaml.AtPath(paths.Current().Child("a", "b")))
		want := "x.yaml:2:6: $.a.b: bad"

		tcs := map[string]struct {
			v niceyaml.Validator
		}{
			"a validator that leaves its error unbound": {v: &fieldValidator{err: located}},
			"a validator that binds its error": {
				v: niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
					return located
				}),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				skip := niceyaml.SkipEmpty(tc.v)

				// A direct call returns the error Node.Validate returns.
				require.EqualError(t, skip.Check(t.Context(), doc), want)
				require.EqualError(t, doc.Validate(t.Context(), skip), want)
			})
		}
	})

	t.Run("a nil validator passes every Node", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, "a: 1\n")

		for _, v := range []niceyaml.Validator{nil, (*fieldValidator)(nil), niceyaml.ValidatorFunc(nil)} {
			require.NoError(t, doc.Validate(t.Context(), niceyaml.SkipEmpty(v)))
		}

		// A typed nil pointer reports no failure, as it does from a
		// validator given alone.
		typedNil := &fieldValidator{err: (*niceyaml.Error)(nil)}

		require.NoError(t, niceyaml.SkipEmpty(typedNil).Check(t.Context(), doc))
	})

	t.Run("a decode of an empty document returns the zero value", func(t *testing.T) {
		t.Parallel()

		type config struct {
			Name string `yaml:"name"`
		}

		named := schema.MustCompile([]byte(`{"type": "object", "required": ["name"]}`))

		for name, input := range map[string]string{
			"empty file":     "",
			"comments alone": "# Defaults apply.\n",
			"header alone":   "---\n",
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, input)

				got, err := doc.Decode[config](t.Context(), niceyaml.WithValidator(niceyaml.SkipEmpty(named)))
				require.NoError(t, err)
				assert.Equal(t, config{}, got)

				// The schema alone rejects the null the document decodes to.
				_, err = doc.Decode[config](t.Context(), niceyaml.WithValidator(named))
				require.ErrorContains(t, err, `expected "object", got "null"`)
				assert.True(t, niceyaml.IsInvalid(err))
			})
		}

		// A file with content still answers to the schema.
		_, err := yamltest.FirstDocument(t, "port: 1\n").
			Decode[config](t.Context(), niceyaml.WithValidator(niceyaml.SkipEmpty(named)))
		require.ErrorContains(t, err, `missing required property "name"`)
	})

	t.Run("wraps one validator and takes its place among several", func(t *testing.T) {
		t.Parallel()

		empty := yamltest.FirstDocument(t, "# only a comment\n")

		var first, second int

		// Around a MultiValidator, it passes the document for all of them.
		all := niceyaml.SkipEmpty(niceyaml.MultiValidator(rejecting(&first), rejecting(&second)))
		require.NoError(t, empty.Validate(t.Context(), all))
		assert.Zero(t, first)
		assert.Zero(t, second)

		// Inside one, the validators beside it still run.
		one := niceyaml.MultiValidator(niceyaml.SkipEmpty(rejecting(&first)), rejecting(&second))
		require.ErrorIs(t, empty.Validate(t.Context(), one), errBad)
		assert.Zero(t, first)
		assert.Equal(t, 1, second)
	})
}

// manyKeyAliases returns a document that anchors a mapping of keys
// entries as d and lists aliases keys under services, each an alias to d.
func manyKeyAliases(keys, aliases int) string {
	var sb strings.Builder

	sb.WriteString("defaults: &d\n")

	for i := range keys {
		fmt.Fprintf(&sb, "  k%d: v%d\n", i, i)
	}

	sb.WriteString("services:\n")

	for i := range aliases {
		fmt.Fprintf(&sb, "  s%d: *d\n", i)
	}

	return sb.String()
}

func TestDocument_Decode_ExcessiveAliasing(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		err   error
		input string
		path  paths.Path
		opts  []niceyaml.SourceOption
	}{
		"nested merge keys": {
			input: yamltest.MergeLevels(7),
			err:   niceyaml.ErrExcessiveAliasing,
		},
		"node with an alias in a document past the limit": {
			input: yamltest.MergeLevels(7),
			path:  paths.Current().Child("m7"),
			err:   niceyaml.ErrExcessiveAliasing,
		},
		"node without an alias in a document past the limit": {
			input: yamltest.MergeLevels(7),
			path:  paths.Current().Child("m0"),
		},
		"nested lists": {
			input: yamltest.AliasLevels(7),
			err:   niceyaml.ErrExcessiveAliasing,
		},
		"nested lists with the limit off": {
			// The decoder shares each list between its aliases, so it
			// decodes the document quickly.
			input: yamltest.AliasLevels(7),
			opts:  []niceyaml.SourceOption{niceyaml.WithAliasLimit(false)},
		},
		"node of nested lists with the limit off": {
			input: yamltest.AliasLevels(7),
			path:  paths.Current().Child("a").Index(7),
			opts:  []niceyaml.SourceOption{niceyaml.WithAliasLimit(false)},
		},
		"nested lists with the limit on again": {
			// The last option wins, as it does for every source option.
			input: yamltest.AliasLevels(7),
			opts: []niceyaml.SourceOption{
				niceyaml.WithAliasLimit(false),
				niceyaml.WithAliasLimit(true),
			},
			err: niceyaml.ErrExcessiveAliasing,
		},
		"a few aliases": {
			input: "base: &b {a: 1, b: 2}\nx:\n  <<: *b\ny: [*b, *b]\n",
		},
		"a small mapping aliased in each item of a long list": {
			// Each alias counts as a node of the document, so the 150
			// aliases stay a small enough share of what a decode reads.
			input: "base: &b {os: linux, arch: amd64, go: stable, cgo: false}\nmatrix:\n" +
				strings.Repeat("  - *b\n", 150),
		},
		"a mapping of 200 keys aliased under 150 keys": {
			input: manyKeyAliases(200, 150),
		},
		"a mapping of 1000 keys aliased under 300 keys": {
			input: manyKeyAliases(1000, 300),
			err:   niceyaml.ErrExcessiveAliasing,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input, tc.opts...)
			if tc.path.Len() > 0 {
				doc = yamltest.At(t, doc, tc.path)
			}

			var got any

			err := doc.DecodeInto(t.Context(), &got)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				require.NotErrorIs(t, err, niceyaml.ErrDecode)
				assert.True(t, niceyaml.IsInvalid(err))
				assert.Nil(t, got)

				return
			}

			require.NoError(t, err)
			assert.NotEmpty(t, got)
		})
	}
}

// aliasText is a value that decodes itself from the text of its node.
type aliasText string

func (a *aliasText) UnmarshalText(text []byte) error {
	*a = aliasText(text)

	return nil
}

// forwardText decodes its node into a list of [aliasText] through the
// function go-yaml hands its UnmarshalYAML, and holds the length of the
// list. Its type reaches no text type, so only its method leads the
// decoder to one.
type forwardText int

func (f *forwardText) UnmarshalYAML(unmarshal func(any) error) error {
	var items []aliasText

	err := unmarshal(&items)
	*f = forwardText(len(items))

	return err
}

// forwardTextContext is [forwardText] with an UnmarshalYAML that takes a
// context.
type forwardTextContext int

func (f *forwardTextContext) UnmarshalYAML(_ context.Context, unmarshal func(any) error) error {
	var items []aliasText

	err := unmarshal(&items)
	*f = forwardTextContext(len(items))

	return err
}

// nodeDecoded decodes itself from the node go-yaml hands its
// UnmarshalYAML. Go-yaml calls that method ahead of its UnmarshalText
// and decodes nothing into its text field.
type nodeDecoded struct {
	Addr netip.Addr
}

func (*nodeDecoded) UnmarshalYAML(ast.Node) error { return nil }

func (*nodeDecoded) UnmarshalText([]byte) error {
	return errors.New("unexpected UnmarshalText call")
}

// aliasPlain has no method of its own, so it reads text only when a
// function from niceyaml.WithCustomUnmarshaler decodes it.
type aliasPlain string

// aliasJSON has an UnmarshalJSON method, so it reads text only under
// niceyaml.WithJSONUnmarshalers.
type aliasJSON string

func (a *aliasJSON) UnmarshalJSON(data []byte) error {
	*a = aliasJSON(data)

	return nil
}

func TestDocument_Decode_ExcessiveTextAliasing(t *testing.T) {
	t.Parallel()

	// To decode a type that reads text, the decoder writes the node out
	// with a copy of the long scalar at each alias. A plain string shares
	// the scalar between the aliases, so it decodes.
	manyAliases := "a: &a " + strings.Repeat("x", 2000) + "\n" +
		"kind: [" + strings.TrimSuffix(strings.Repeat("*a, ", 500), ", ") + "]\n"
	kind := paths.Current().Child("kind")

	plainText := niceyaml.WithCustomUnmarshaler(func(_ context.Context, a *aliasPlain, decode func(any) error) error {
		return decode((*string)(a))
	})

	pointerText := niceyaml.WithCustomUnmarshaler(
		func(_ context.Context, a **aliasPlain, decode func(any) error) error {
			*a = new(aliasPlain)

			return decode((*string)(*a))
		},
	)

	tcs := map[string]struct {
		err    error
		target func() any
		input  string
		path   paths.Path
		opts   []niceyaml.SourceOption
		decode []niceyaml.DecodeOption
	}{
		"custom unmarshaler elements": {
			input:  manyAliases,
			path:   kind,
			target: func() any { return new([]aliasPlain) },
			decode: []niceyaml.DecodeOption{plainText},
			err:    niceyaml.ErrExcessiveAliasing,
		},
		"elements no custom unmarshaler decodes": {
			input:  manyAliases,
			path:   kind,
			target: func() any { return new([]aliasPlain) },
		},
		"custom unmarshaler of a type the target does not reach": {
			input:  manyAliases,
			path:   kind,
			target: func() any { return new([]string) },
			decode: []niceyaml.DecodeOption{plainText},
		},
		"custom unmarshaler of a pointer type, which names no type": {
			input:  manyAliases,
			path:   kind,
			target: func() any { return new([]*aliasPlain) },
			decode: []niceyaml.DecodeOption{pointerText},
		},
		"json unmarshaler elements under their option": {
			input:  manyAliases,
			path:   kind,
			target: func() any { return new([]aliasJSON) },
			decode: []niceyaml.DecodeOption{niceyaml.WithJSONUnmarshalers(true)},
			err:    niceyaml.ErrExcessiveAliasing,
		},
		"json unmarshaler elements without their option": {
			input:  manyAliases,
			path:   kind,
			target: func() any { return new([]aliasJSON) },
		},
		"text unmarshaler elements": {
			input:  manyAliases,
			path:   kind,
			target: func() any { return new([]aliasText) },
			err:    niceyaml.ErrExcessiveAliasing,
		},
		"bytes unmarshaler": {
			input:  manyAliases,
			path:   kind,
			target: func() any { return new(rawText) },
			err:    niceyaml.ErrExcessiveAliasing,
		},
		"pointer to a bytes unmarshaler": {
			input:  manyAliases,
			path:   kind,
			target: func() any { return new(*rawText) },
			err:    niceyaml.ErrExcessiveAliasing,
		},
		"struct field text unmarshaler": {
			input: manyAliases,
			target: func() any {
				return new(struct {
					Kind aliasText `yaml:"kind"`
				})
			},
			err: niceyaml.ErrExcessiveAliasing,
		},
		"struct field of a standard library text type": {
			input: manyAliases,
			target: func() any {
				return new(struct {
					Kind netip.Prefix `yaml:"kind"`
				})
			},
			err: niceyaml.ErrExcessiveAliasing,
		},
		"map value bytes unmarshaler": {
			input:  manyAliases,
			target: func() any { return new(map[string]rawText) },
			err:    niceyaml.ErrExcessiveAliasing,
		},
		"unmarshaler that decodes into text unmarshalers": {
			input:  manyAliases,
			path:   kind,
			target: func() any { return new(forwardText) },
			err:    niceyaml.ErrExcessiveAliasing,
		},
		"unmarshaler with a context that decodes into text unmarshalers": {
			input:  manyAliases,
			path:   kind,
			target: func() any { return new(forwardTextContext) },
			err:    niceyaml.ErrExcessiveAliasing,
		},
		"struct field unmarshaler that decodes into text unmarshalers": {
			input: manyAliases,
			target: func() any {
				return new(struct {
					Kind forwardText `yaml:"kind"`
				})
			},
			err: niceyaml.ErrExcessiveAliasing,
		},
		"node unmarshaler": {
			input:  manyAliases,
			path:   kind,
			target: func() any { return new(nodeDecoded) },
		},
		"struct field node unmarshaler": {
			input: manyAliases,
			target: func() any {
				return new(struct {
					Kind nodeDecoded `yaml:"kind"`
				})
			},
		},
		"string elements": {
			input:  manyAliases,
			path:   kind,
			target: func() any { return new([]string) },
		},
		"struct with a time field": {
			input: manyAliases,
			target: func() any {
				return new(struct {
					At   time.Time `yaml:"at"`
					Kind []string  `yaml:"kind"`
				})
			},
		},
		"struct with text fields the decoder skips": {
			input: manyAliases,
			target: func() any {
				return new(struct {
					hidden netip.Prefix
					Addr   netip.Prefix `yaml:"-"`
					Prefix netip.Prefix `json:"-"`
					Kind   []string     `yaml:"kind"`
				})
			},
		},
		"a few aliases": {
			input:  "a: &a hello\nkind: [*a, *a, *a]\n",
			path:   kind,
			target: func() any { return new([]aliasText) },
		},
		"text unmarshaler elements with the limit off": {
			input:  manyAliases,
			path:   kind,
			target: func() any { return new([]aliasText) },
			opts:   []niceyaml.SourceOption{niceyaml.WithAliasLimit(false)},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input, tc.opts...)
			if tc.path.Len() > 0 {
				doc = yamltest.At(t, doc, tc.path)
			}

			err := doc.DecodeInto(t.Context(), tc.target(), tc.decode...)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				require.NotErrorIs(t, err, niceyaml.ErrDecode)
				assert.True(t, niceyaml.IsInvalid(err))

				return
			}

			require.NoError(t, err)
		})
	}
}
