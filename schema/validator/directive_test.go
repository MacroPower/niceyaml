package validator_test

import (
	"testing"

	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macropower/niceyaml/schema/validator"
	"github.com/macropower/niceyaml/yamltest"
)

func TestParseDirective(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  *validator.Directive
	}{
		"valid file path": {
			input: " yaml-language-server: $schema=./schema.json",
			want:  &validator.Directive{Schema: "./schema.json"},
		},
		"valid absolute path": {
			input: " yaml-language-server: $schema=/path/to/schema.json",
			want:  &validator.Directive{Schema: "/path/to/schema.json"},
		},
		"valid http URL": {
			input: " yaml-language-server: $schema=http://example.com/schema.json",
			want:  &validator.Directive{Schema: "http://example.com/schema.json"},
		},
		"valid https URL": {
			input: " yaml-language-server: $schema=https://example.com/schema.json",
			want:  &validator.Directive{Schema: "https://example.com/schema.json"},
		},
		"path with spaces": {
			input: " yaml-language-server: $schema=./path with spaces/schema.json",
			want:  &validator.Directive{Schema: "./path with spaces/schema.json"},
		},
		"no spaces after colon": {
			input: " yaml-language-server:$schema=schema.json",
			want:  &validator.Directive{Schema: "schema.json"},
		},
		"extra spaces": {
			input: " yaml-language-server:   $schema=schema.json",
			want:  &validator.Directive{Schema: "schema.json"},
		},
		"empty string": {
			input: "",
			want:  nil,
		},
		"no directive": {
			input: " just a regular comment",
			want:  nil,
		},
		"partial match - missing schema": {
			input: " yaml-language-server: other=value",
			want:  nil,
		},
		"partial match - wrong prefix": {
			input: " yaml-language: $schema=schema.json",
			want:  nil,
		},
		"case sensitive - uppercase": {
			input: " YAML-LANGUAGE-SERVER: $schema=schema.json",
			want:  nil,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := validator.ParseDirective(tc.input)

			if tc.want == nil {
				assert.Nil(t, got)
			} else {
				require.NotNil(t, got)
				assert.Equal(t, tc.want.Schema, got.Schema)
			}
		})
	}
}

func TestParseDocumentDirectives(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  map[int]string
	}{
		"single document with directive": {
			input: yamltest.Input(`
				# yaml-language-server: $schema=./schema.json
				key: value
			`),
			want: map[int]string{0: "./schema.json"},
		},
		"single document without directive": {
			input: yamltest.Input(`
				# just a comment
				key: value
			`),
			want: map[int]string{},
		},
		"empty file": {
			input: "",
			want:  map[int]string{},
		},
		"only comment with directive": {
			input: "# yaml-language-server: $schema=./schema.json\n",
			want:  map[int]string{0: "./schema.json"},
		},
		"directive after content is ignored": {
			input: yamltest.Input(`
				key: value
				# yaml-language-server: $schema=./schema.json
			`),
			want: map[int]string{},
		},
		"multi-document with directives in each": {
			input: yamltest.Input(`
				# yaml-language-server: $schema=./schema1.json
				doc1: data
				---
				# yaml-language-server: $schema=./schema2.json
				doc2: data
			`),
			want: map[int]string{
				0: "./schema1.json",
				1: "./schema2.json",
			},
		},
		"multi-document with directive only in first": {
			input: yamltest.Input(`
				# yaml-language-server: $schema=./schema.json
				doc1: data
				---
				doc2: data
			`),
			want: map[int]string{0: "./schema.json"},
		},
		"multi-document with directive only in second": {
			input: yamltest.Input(`
				doc1: data
				---
				# yaml-language-server: $schema=./schema.json
				doc2: data
			`),
			want: map[int]string{1: "./schema.json"},
		},
		"multi-document with directive only in middle": {
			input: yamltest.Input(`
				doc1: data
				---
				# yaml-language-server: $schema=./schema.json
				doc2: data
				---
				doc3: data
			`),
			want: map[int]string{1: "./schema.json"},
		},
		"three documents with different schemas": {
			input: yamltest.Input(`
				# yaml-language-server: $schema=./a.json
				a: 1
				---
				# yaml-language-server: $schema=./b.json
				b: 2
				---
				# yaml-language-server: $schema=./c.json
				c: 3
			`),
			want: map[int]string{
				0: "./a.json",
				1: "./b.json",
				2: "./c.json",
			},
		},
		"directive with comment before it": {
			input: yamltest.Input(`
				# Some description
				# yaml-language-server: $schema=./schema.json
				key: value
			`),
			want: map[int]string{0: "./schema.json"},
		},
		"first directive wins": {
			input: yamltest.Input(`
				# yaml-language-server: $schema=./first.json
				# yaml-language-server: $schema=./second.json
				key: value
			`),
			want: map[int]string{0: "./first.json"},
		},
		"explicit header in first document": {
			input: yamltest.Input(`
				---
				# yaml-language-server: $schema=./schema.json
				key: value
			`),
			want: map[int]string{0: "./schema.json"},
		},
		"comment between header and content": {
			input: yamltest.Input(`
				key1: value1
				---
				# yaml-language-server: $schema=./schema.json
				# another comment
				key2: value2
			`),
			want: map[int]string{1: "./schema.json"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tks := lexer.Tokenize(tc.input)
			got := validator.ParseDocumentDirectives(tks)

			// Check expected directives exist.
			for docIdx, wantSchema := range tc.want {
				directive, ok := got[docIdx]
				require.True(t, ok, "expected directive for document %d", docIdx)
				assert.Equal(t, wantSchema, directive.Schema, "document %d schema", docIdx)
				assert.NotNil(t, directive.Position, "document %d position", docIdx)
			}

			// Check no unexpected directives.
			for docIdx := range got {
				_, ok := tc.want[docIdx]
				assert.True(t, ok, "unexpected directive for document %d", docIdx)
			}
		})
	}
}

func TestParseDocumentDirectives_TokenBuilder(t *testing.T) {
	t.Parallel()

	t.Run("directive position is set from token", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		tks := token.Tokens{
			tkb.Clone().Type(token.CommentType).
				Value(" yaml-language-server: $schema=./schema.json").
				PositionLine(1).
				PositionColumn(1).
				Build(),
			tkb.Clone().Type(token.StringType).Value("key").Build(),
			tkb.Clone().Type(token.MappingValueType).Value(":").Build(),
			tkb.Clone().Type(token.StringType).Value("value").Build(),
		}

		got := validator.ParseDocumentDirectives(tks)

		require.Len(t, got, 1)
		require.NotNil(t, got[0])
		assert.Equal(t, "./schema.json", got[0].Schema)
		assert.Equal(t, 1, got[0].Position.Line)
		assert.Equal(t, 1, got[0].Position.Column)
	})

	t.Run("document header creates new document context", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		tks := token.Tokens{
			// Document 0.
			tkb.Clone().Type(token.StringType).Value("key1").Build(),
			tkb.Clone().Type(token.MappingValueType).Value(":").Build(),
			tkb.Clone().Type(token.StringType).Value("value1").Build(),
			// Document 1.
			tkb.Clone().Type(token.DocumentHeaderType).Value("---").Build(),
			tkb.Clone().Type(token.CommentType).
				Value(" yaml-language-server: $schema=./doc2.json").
				Build(),
			tkb.Clone().Type(token.StringType).Value("key2").Build(),
			tkb.Clone().Type(token.MappingValueType).Value(":").Build(),
			tkb.Clone().Type(token.StringType).Value("value2").Build(),
		}

		got := validator.ParseDocumentDirectives(tks)

		require.Len(t, got, 1)
		require.NotNil(t, got[1])
		assert.Equal(t, "./doc2.json", got[1].Schema)
	})
}
