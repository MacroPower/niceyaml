package schema_test

import (
	"testing"

	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/schema"
	"go.jacobcolvin.com/niceyaml/tokens"
)

func TestParseDirective(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  *schema.Directive
	}{
		"valid file path": {
			input: " yaml-language-server: $schema=./schema.json",
			want:  &schema.Directive{Schema: "./schema.json"},
		},
		"valid absolute path": {
			input: " yaml-language-server: $schema=/path/to/schema.json",
			want:  &schema.Directive{Schema: "/path/to/schema.json"},
		},
		"valid http URL": {
			input: " yaml-language-server: $schema=http://example.com/schema.json",
			want:  &schema.Directive{Schema: "http://example.com/schema.json"},
		},
		"valid https URL": {
			input: " yaml-language-server: $schema=https://example.com/schema.json",
			want:  &schema.Directive{Schema: "https://example.com/schema.json"},
		},
		"path with spaces": {
			input: " yaml-language-server: $schema=./path with spaces/schema.json",
			want:  &schema.Directive{Schema: "./path with spaces/schema.json"},
		},
		"no spaces after colon": {
			input: " yaml-language-server:$schema=schema.json",
			want:  &schema.Directive{Schema: "schema.json"},
		},
		"extra spaces": {
			input: " yaml-language-server:   $schema=schema.json",
			want:  &schema.Directive{Schema: "schema.json"},
		},
		"trailing whitespace": {
			input: " yaml-language-server: $schema=./schema.json   ",
			want:  &schema.Directive{Schema: "./schema.json"},
		},
		"trailing tab": {
			input: " yaml-language-server: $schema=./schema.json\t",
			want:  &schema.Directive{Schema: "./schema.json"},
		},
		"only whitespace after equals": {
			input: " yaml-language-server: $schema=   ",
			want:  nil,
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

			got := schema.ParseDirective(tc.input)

			if tc.want == nil {
				assert.Nil(t, got)
			} else {
				require.NotNil(t, got)
				assert.Equal(t, tc.want.Schema, got.Schema)
			}
		})
	}
}

func TestParseDocumentDirective(t *testing.T) {
	t.Parallel()

	// Each case is a token stream that tokens.SplitDocuments divides into
	// documents; want maps a document index to the schema its directive names.
	tcs := map[string]struct {
		input string
		want  map[int]string
	}{
		"single document with directive": {
			input: stringtest.Input(`
				# yaml-language-server: $schema=./schema.json
				key: value
			`),
			want: map[int]string{0: "./schema.json"},
		},
		"single document without directive": {
			input: stringtest.Input(`
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
		"directive with trailing spaces": {
			input: "# yaml-language-server: $schema=./schema.json   \nkey: value\n",
			want:  map[int]string{0: "./schema.json"},
		},
		"directive after content is ignored": {
			input: stringtest.Input(`
				key: value
				# yaml-language-server: $schema=./schema.json
			`),
			want: map[int]string{},
		},
		"multi-document with directives in each": {
			input: stringtest.Input(`
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
			input: stringtest.Input(`
				# yaml-language-server: $schema=./schema.json
				doc1: data
				---
				doc2: data
			`),
			want: map[int]string{0: "./schema.json"},
		},
		"multi-document with directive only in second": {
			input: stringtest.Input(`
				doc1: data
				---
				# yaml-language-server: $schema=./schema.json
				doc2: data
			`),
			want: map[int]string{1: "./schema.json"},
		},
		"multi-document with directive only in middle": {
			input: stringtest.Input(`
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
			input: stringtest.Input(`
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
			input: stringtest.Input(`
				# Some description
				# yaml-language-server: $schema=./schema.json
				key: value
			`),
			want: map[int]string{0: "./schema.json"},
		},
		"first directive wins": {
			input: stringtest.Input(`
				# yaml-language-server: $schema=./first.json
				# yaml-language-server: $schema=./second.json
				key: value
			`),
			want: map[int]string{0: "./first.json"},
		},
		"explicit header in first document": {
			input: stringtest.Input(`
				---
				# yaml-language-server: $schema=./schema.json
				key: value
			`),
			want: map[int]string{0: "./schema.json"},
		},
		"comment between header and content": {
			input: stringtest.Input(`
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

			got := make(map[int]string)

			for docIdx, docTokens := range tokens.SplitDocuments(lexer.Tokenize(tc.input)) {
				directive := schema.ParseDocumentDirective(docTokens)
				if directive == nil {
					continue
				}

				got[docIdx] = directive.Schema
			}

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestParseDocumentDirective_TokenBuilder(t *testing.T) {
	t.Parallel()

	t.Run("directive position is set from token", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		tks := token.Tokens{
			tkb.Clone().Type(token.CommentType).
				Value(" yaml-language-server: $schema=./schema.json").
				PositionLine(3).
				PositionColumn(5).
				Build(),
			tkb.Clone().Type(token.StringType).Value("key").Build(),
			tkb.Clone().Type(token.MappingValueType).Value(":").Build(),
			tkb.Clone().Type(token.StringType).Value("value").Build(),
		}

		got := schema.ParseDocumentDirective(tks)

		require.NotNil(t, got)
		assert.Equal(t, "./schema.json", got.Schema)
		// The token counts from 1 and the directive from 0.
		assert.Equal(t, position.New(2, 4), got.Position)
	})

	t.Run("document header precedes the directive", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		tks := token.Tokens{
			tkb.Clone().Type(token.DocumentHeaderType).Value("---").Build(),
			tkb.Clone().Type(token.CommentType).
				Value(" yaml-language-server: $schema=./doc.json").
				Build(),
			tkb.Clone().Type(token.StringType).Value("key").Build(),
			tkb.Clone().Type(token.MappingValueType).Value(":").Build(),
			tkb.Clone().Type(token.StringType).Value("value").Build(),
		}

		got := schema.ParseDocumentDirective(tks)

		require.NotNil(t, got)
		assert.Equal(t, "./doc.json", got.Schema)
	})

	t.Run("content before the directive yields nil", func(t *testing.T) {
		t.Parallel()

		tkb := yamltest.NewTokenBuilder()
		tks := token.Tokens{
			tkb.Clone().Type(token.StringType).Value("key").Build(),
			tkb.Clone().Type(token.MappingValueType).Value(":").Build(),
			tkb.Clone().Type(token.StringType).Value("value").Build(),
			tkb.Clone().Type(token.CommentType).
				Value(" yaml-language-server: $schema=./doc.json").
				Build(),
		}

		assert.Nil(t, schema.ParseDocumentDirective(tks))
	})

	t.Run("nil tokens yield nil", func(t *testing.T) {
		t.Parallel()

		assert.Nil(t, schema.ParseDocumentDirective(nil))
	})
}
