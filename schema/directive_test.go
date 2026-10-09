package schema_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/schema"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
	"go.jacobcolvin.com/niceyaml/tokens"
)

func TestParseDirective(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  *schema.ParsedDirective
	}{
		"valid file path": {
			input: " yaml-language-server: $schema=./schema.json",
			want:  &schema.ParsedDirective{Schema: "./schema.json"},
		},
		"valid absolute path": {
			input: " yaml-language-server: $schema=/path/to/schema.json",
			want:  &schema.ParsedDirective{Schema: "/path/to/schema.json"},
		},
		"valid http URL": {
			input: " yaml-language-server: $schema=http://example.com/schema.json",
			want:  &schema.ParsedDirective{Schema: "http://example.com/schema.json"},
		},
		"valid https URL": {
			input: " yaml-language-server: $schema=https://example.com/schema.json",
			want:  &schema.ParsedDirective{Schema: "https://example.com/schema.json"},
		},
		"remark after the reference": {
			input: " yaml-language-server: $schema=./schema.json # managed by tooling",
			want:  &schema.ParsedDirective{Schema: "./schema.json"},
		},
		"path with spaces ends at the first space": {
			input: " yaml-language-server: $schema=./path with spaces/schema.json",
			want:  &schema.ParsedDirective{Schema: "./path"},
		},
		"no spaces after colon": {
			input: " yaml-language-server:$schema=schema.json",
			want:  &schema.ParsedDirective{Schema: "schema.json"},
		},
		"extra spaces": {
			input: " yaml-language-server:   $schema=schema.json",
			want:  &schema.ParsedDirective{Schema: "schema.json"},
		},
		"space before colon": {
			input: " yaml-language-server : $schema=./s.json",
			want:  &schema.ParsedDirective{Schema: "./s.json"},
		},
		"colon value form": {
			input: " yaml-language-server: $schema: ./s.json",
			want:  &schema.ParsedDirective{Schema: "./s.json"},
		},
		"other settings before schema": {
			input: " yaml-language-server: foo=bar $schema=./s.json",
			want:  &schema.ParsedDirective{Schema: "./s.json"},
		},
		"short form": {
			input: " $schema: ./s.json",
			want:  &schema.ParsedDirective{Schema: "./s.json"},
		},
		"short form without space": {
			input: " $schema:./s.json",
			want:  &schema.ParsedDirective{Schema: "./s.json"},
		},
		"short form with equals is not a directive": {
			input: " $schema=./s.json",
			want:  nil,
		},
		"short form after other text": {
			input: " see $schema: ./s.json",
			want:  nil,
		},
		"short form with nothing after the colon": {
			input: " $schema:   ",
			want:  nil,
		},
		"none is returned as written": {
			input: " yaml-language-server: $schema=none",
			want:  &schema.ParsedDirective{Schema: "none"},
		},
		"trailing whitespace": {
			input: " yaml-language-server: $schema=./schema.json   ",
			want:  &schema.ParsedDirective{Schema: "./schema.json"},
		},
		"trailing tab": {
			input: " yaml-language-server: $schema=./schema.json\t",
			want:  &schema.ParsedDirective{Schema: "./schema.json"},
		},
		"only whitespace after equals": {
			input: " yaml-language-server: $schema=   ",
			want:  nil,
		},
		// The JavaScript \s, which yaml-language-server reads a directive
		// with, matches \v and the Unicode spaces too.
		"no-break space before a remark": {
			input: " yaml-language-server: $schema=./a.json\u00a0# managed",
			want:  &schema.ParsedDirective{Schema: "./a.json"},
		},
		"vertical tab before a remark": {
			input: " yaml-language-server: $schema=./a.json\v#",
			want:  &schema.ParsedDirective{Schema: "./a.json"},
		},
		"ideographic space before a remark": {
			input: " yaml-language-server: $schema=./a.json\u3000#",
			want:  &schema.ParsedDirective{Schema: "./a.json"},
		},
		"byte order mark before a remark": {
			input: " yaml-language-server: $schema=./a.json\ufeff#",
			want:  &schema.ParsedDirective{Schema: "./a.json"},
		},
		"no-break space after the colon value form": {
			input: " yaml-language-server: $schema:\u00a0./a.json",
			want:  &schema.ParsedDirective{Schema: "./a.json"},
		},
		"no-break space after equals": {
			input: " yaml-language-server: $schema=\u00a0./a.json",
			want:  nil,
		},
		"no-break space before the marker": {
			input: "\u00a0yaml-language-server: $schema=./a.json",
			want:  &schema.ParsedDirective{Schema: "./a.json"},
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
		"marker after other text": {
			input: " see yaml-language-server: $schema=./schema.json",
			want:  nil,
		},
		"marker with a prefix": {
			input: " not-a-yaml-language-server: $schema=./schema.json",
			want:  nil,
		},
		"marker without leading space": {
			input: "yaml-language-server: $schema=./schema.json",
			want:  &schema.ParsedDirective{Schema: "./schema.json"},
		},
		"marker after a tab": {
			input: "\tyaml-language-server: $schema=./schema.json",
			want:  &schema.ParsedDirective{Schema: "./schema.json"},
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
	// documents, and want maps a document index to the schema its directive
	// names.
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
		"short form directive": {
			input: "# $schema: ./schema.json\nkey: value\n",
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
		"directive as a trailing comment on a %YAML line": {
			input: "%YAML 1.2 # yaml-language-server: $schema=./schema.json\n---\nkey: value\n",
			want:  map[int]string{0: "./schema.json"},
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

	t.Run("directive line precedes the directive", func(t *testing.T) {
		t.Parallel()

		// The parser splits "%YAML 1.2" into a directive token and the
		// tokens of its value, and neither is content.
		tkb := yamltest.NewTokenBuilder()
		tks := token.Tokens{
			tkb.Clone().Type(token.DirectiveType).Value("%").PositionLine(1).Build(),
			tkb.Clone().Type(token.FloatType).Value("1.2").PositionLine(1).Build(),
			tkb.Clone().Type(token.CommentType).
				Value(" yaml-language-server: $schema=./doc.json").
				PositionLine(2).
				Build(),
			tkb.Clone().Type(token.StringType).Value("key").PositionLine(3).Build(),
			tkb.Clone().Type(token.MappingValueType).Value(":").PositionLine(3).Build(),
			tkb.Clone().Type(token.StringType).Value("value").PositionLine(3).Build(),
		}

		got := schema.ParseDocumentDirective(tks)

		require.NotNil(t, got)
		assert.Equal(t, "./doc.json", got.Schema)
	})

	t.Run("content on the line after a directive yields nil", func(t *testing.T) {
		t.Parallel()

		// ParseDocumentDirective skips only the directive's own line.
		tkb := yamltest.NewTokenBuilder()
		tks := token.Tokens{
			tkb.Clone().Type(token.DirectiveType).Value("%").PositionLine(1).Build(),
			tkb.Clone().Type(token.StringType).Value("YAML").PositionLine(1).Build(),
			tkb.Clone().Type(token.StringType).Value("key").PositionLine(2).Build(),
			tkb.Clone().Type(token.MappingValueType).Value(":").PositionLine(2).Build(),
			tkb.Clone().Type(token.CommentType).
				Value(" yaml-language-server: $schema=./doc.json").
				PositionLine(3).
				Build(),
		}

		assert.Nil(t, schema.ParseDocumentDirective(tks))
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

func TestParseDocumentDirective_NilToken(t *testing.T) {
	t.Parallel()

	// ParseDocumentDirective skips a nil token in the stream, as every
	// other token consumer skips it, rather than dereferencing it.
	directive := schema.ParseDocumentDirective(token.Tokens{
		nil,
		tokens.Tokenize("# yaml-language-server: $schema=./schema.json\n")[0],
	})
	require.NotNil(t, directive)
	assert.Equal(t, "./schema.json", directive.Schema)
}

func TestParseDocumentDirective_AfterDocumentEnd(t *testing.T) {
	t.Parallel()

	// A "..." marker is part of the preamble, as a "---" header is, so a
	// directive after it, or in a comment that trails it, still names the
	// schema of the document.
	for _, input := range []string{
		"...\n# yaml-language-server: $schema=./schema.json\nj: 2\n",
		"---\n...\n# yaml-language-server: $schema=./schema.json\nj: 2\n",
		"... # yaml-language-server: $schema=./schema.json\nj: 2\n",
	} {
		directive := schema.ParseDocumentDirective(tokens.Tokenize(input))
		require.NotNil(t, directive, "input %q", input)
		assert.Equal(t, "./schema.json", directive.Schema, "input %q", input)
	}
}

// resolveAndLoad resolves doc through res and loads the schema it names.
// It returns the ref's Key alongside the loaded bytes. Both steps must
// succeed.
func resolveAndLoad(t *testing.T, res schema.Resolver, doc *niceyaml.Node) (string, []byte) {
	t.Helper()

	ref, err := res.Resolve(t.Context(), doc)
	require.NoError(t, err)

	data, err := schema.NewRegistry().Load(t.Context(), ref)
	require.NoError(t, err)

	return ref.Key(), data
}

func TestDirective(t *testing.T) {
	t.Parallel()

	t.Run("registry fetches a directive URL with its client", func(t *testing.T) {
		t.Parallel()

		schemaData := `{"type": "object"}`

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			//nolint:errcheck // Test helper.
			w.Write([]byte(schemaData))
		}))
		defer server.Close()

		doc := yamltest.FirstDocumentWithPath(t,
			"# yaml-language-server: $schema="+server.URL+"/schema.json\nkind: Deployment\n",
			filepath.Join(t.TempDir(), "config.yaml"),
		)

		var requests atomic.Int32

		reg := schema.NewRegistry(
			schema.WithHTTPClient(countingClient(&requests)),
			schema.WithResolvers(schema.Directive()),
		)

		err := reg.Validate(t.Context(), doc)
		require.NoError(t, err)
		assert.Equal(t, int32(1), requests.Load())
	})

	t.Run("registry loads a YAML schema beside the document", func(t *testing.T) {
		t.Parallel()

		// The schema reads the type of a name from the YAML document
		// beside it.
		dir := t.TempDir()
		writeFiles(t, dir, map[string]string{
			"schema.yaml": "required: [name]\nproperties:\n  name:\n    $ref: defs.yaml#/$defs/name\n",
			"defs.yaml":   "$defs:\n  name:\n    type: string\n  kinded:\n    required: [kind]\n",
		})

		reg := schema.NewRegistry(schema.WithResolvers(schema.Directive()))

		tcs := map[string]struct {
			input string
			// Text of the violation, or empty for a valid document.
			want string
		}{
			"valid": {
				input: "# yaml-language-server: $schema=./schema.yaml\nname: a\n",
			},
			"violates the schema": {
				input: "# yaml-language-server: $schema=./schema.yaml\nkind: Deployment\n",
				want:  `missing required property "name"`,
			},
			"violates the document the $ref names": {
				input: "# yaml-language-server: $schema=./schema.yaml\nname: 1\n",
				want:  `expected "string", got "integer"`,
			},
			"valid under a fragment": {
				input: "# yaml-language-server: $schema=./defs.yaml#/$defs/kinded\nkind: Deployment\n",
			},
			"violates a fragment": {
				input: "# yaml-language-server: $schema=./defs.yaml#/$defs/kinded\nname: a\n",
				want:  `missing required property "kind"`,
			},
			"short form": {
				input: "# $schema: schema.yaml\nname: 1\n",
				want:  `expected "string", got "integer"`,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocumentWithPath(t, tc.input, filepath.Join(dir, "config.yaml"))

				err := reg.Validate(t.Context(), doc)
				if tc.want == "" {
					require.NoError(t, err)

					return
				}

				require.ErrorContains(t, err, tc.want)
				assert.True(t, niceyaml.IsInvalid(err), "IsInvalid(%v)", err)
			})
		}
	})

	t.Run("a YAML schema that does not parse is no fault of the document", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		writeFiles(t, dir, map[string]string{"schema.yaml": "required: [name\n"})

		doc := yamltest.FirstDocumentWithPath(t,
			"# yaml-language-server: $schema=./schema.yaml\nname: a\n",
			filepath.Join(dir, "config.yaml"),
		)

		err := schema.NewRegistry(schema.WithResolvers(schema.Directive())).Validate(t.Context(), doc)
		require.ErrorIs(t, err, schema.ErrCompile)
		assert.Contains(t, err.Error(), "schema.yaml")
		assert.Contains(t, err.Error(), "YAML decode: 1:11: ")

		require.NotErrorIs(t, err, niceyaml.ErrSyntax)
		assert.False(t, niceyaml.IsInvalid(err), "IsInvalid(%v)", err)
	})
}

func TestDirective_Resolve_Match(t *testing.T) {
	t.Parallel()

	// A resolver "matches" when it reports anything other than ErrNoMatch.
	// Loading the named schema may still fail, which is a match.
	tests := map[string]struct {
		input string
		want  bool
	}{
		"returns true when document has valid schema directive": {
			input: "# yaml-language-server: $schema=./schema.json\nkind: Deployment\n",
			want:  true,
		},
		"returns false when document has no directive": {
			input: "kind: Deployment\n",
			want:  false,
		},
		"returns false when directive appears after content": {
			input: "kind: Deployment\n# yaml-language-server: $schema=./schema.json\n",
			want:  false,
		},
		"returns true when directive follows document header": {
			input: "---\n# yaml-language-server: $schema=./schema.json\nkind: Deployment\n",
			want:  true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocumentWithPath(t, tt.input, filepath.Join(t.TempDir(), "config.yaml"))
			res := schema.Directive()
			_, err := res.Resolve(t.Context(), doc)

			got := !errors.Is(err, schema.ErrNoMatch)
			assert.Equal(t, tt.want, got)

			if !tt.want {
				require.ErrorIs(t, err, schema.ErrNoDirective)
			}
		})
	}
}

func TestDirective_Resolve(t *testing.T) {
	t.Parallel()

	t.Run("successfully loads schema from file directive", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		schemaData := []byte(`{"type": "object", "properties": {"kind": {"type": "string"}}}`)
		err := os.WriteFile(filepath.Join(tmpDir, "schema.json"), schemaData, 0o600)
		require.NoError(t, err)

		doc := yamltest.FirstDocumentWithPath(t,
			"# yaml-language-server: $schema=schema.json\nkind: Deployment\n",
			filepath.Join(tmpDir, "config.yaml"),
		)
		url, data := resolveAndLoad(t, schema.Directive(), doc)
		assert.Equal(t, schemaData, data)
		assert.Equal(t, fileURL(t, filepath.Join(tmpDir, "schema.json")), url)
	})

	t.Run("trims trailing whitespace from the directive", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		schemaData := []byte(`{"type": "object"}`)
		err := os.WriteFile(filepath.Join(tmpDir, "schema.json"), schemaData, 0o600)
		require.NoError(t, err)

		doc := yamltest.FirstDocumentWithPath(t,
			"# yaml-language-server: $schema=./schema.json   \nkind: Deployment\n",
			filepath.Join(tmpDir, "config.yaml"),
		)
		url, data := resolveAndLoad(t, schema.Directive(), doc)
		assert.Equal(t, schemaData, data)
		assert.Equal(t, fileURL(t, filepath.Join(tmpDir, "schema.json")), url)
	})

	t.Run("successfully loads schema from URL directive", func(t *testing.T) {
		t.Parallel()

		schemaData := `{"type": "object"}`

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			//nolint:errcheck // Test helper.
			w.Write([]byte(schemaData))
		}))
		defer server.Close()

		doc := yamltest.FirstDocumentWithPath(t,
			"# yaml-language-server: $schema="+server.URL+"/schema.json\nkind: Deployment\n",
			filepath.Join(t.TempDir(), "config.yaml"),
		)
		url, data := resolveAndLoad(t, schema.Directive(), doc)
		assert.Equal(t, []byte(schemaData), data)
		assert.Equal(t, server.URL+"/schema.json", url)
	})

	t.Run("validates against the subschema a URL fragment names", func(t *testing.T) {
		t.Parallel()

		// The root takes any document, and Foo requires a name.
		schemaData := `{
			"$schema": "http://json-schema.org/draft-07/schema#",
			"definitions": {"Foo": {"type": "object", "required": ["name"]}}
		}`

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			//nolint:errcheck // Test helper.
			w.Write([]byte(schemaData))
		}))
		defer server.Close()

		directive := "# yaml-language-server: $schema=" + server.URL + "/defs.json#/definitions/Foo\n"
		reg := schema.NewRegistry(schema.WithResolvers(schema.Directive()))

		err := reg.Validate(t.Context(), yamltest.FirstDocument(t, directive+"name: x\n"))
		require.NoError(t, err)

		err = reg.Validate(t.Context(), yamltest.FirstDocument(t, directive+"kind: Deployment\n"))
		require.ErrorContains(t, err, `missing required property "name"`)
	})

	t.Run("validates against the subschema a path fragment names", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		schemaPath := filepath.Join(tmpDir, "schema.json")
		// The root takes any document, and Foo requires a name.
		schemaData := []byte(`{"definitions": {"Foo": {"type": "object", "required": ["name"]}}}`)
		err := os.WriteFile(schemaPath, schemaData, 0o600)
		require.NoError(t, err)

		yamlPath := filepath.Join(tmpDir, "config.yaml")

		tests := map[string]struct {
			ref string
		}{
			"relative path": {ref: "./schema.json#/definitions/Foo"},
			"absolute path": {ref: schemaPath + "#/definitions/Foo"},
		}

		for name, tt := range tests {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				directive := "# yaml-language-server: $schema=" + tt.ref + "\n"

				// The key keeps the fragment, and the load reads the whole
				// file.
				doc := yamltest.FirstDocumentWithPath(t, directive+"name: x\n", yamlPath)
				url, data := resolveAndLoad(t, schema.Directive(), doc)
				assert.Equal(t, fileURL(t, schemaPath)+"#/definitions/Foo", url)
				assert.Equal(t, schemaData, data)

				reg := schema.NewRegistry(schema.WithResolvers(schema.Directive()))

				err := reg.Validate(t.Context(), doc)
				require.NoError(t, err)

				doc = yamltest.FirstDocumentWithPath(t, directive+"kind: Deployment\n", yamlPath)
				err = reg.Validate(t.Context(), doc)
				require.ErrorContains(t, err, `missing required property "name"`)
			})
		}
	})

	t.Run("keeps a leading '#' in a path", func(t *testing.T) {
		t.Parallel()

		// A path splits only at a '#' after its first character, as in
		// yaml-language-server, so "#schema.json" names a file.
		tmpDir := t.TempDir()
		schemaPath := filepath.Join(tmpDir, "#schema.json")
		schemaData := []byte(`{"type": "object"}`)
		err := os.WriteFile(schemaPath, schemaData, 0o600)
		require.NoError(t, err)

		doc := yamltest.FirstDocumentWithPath(t,
			"# yaml-language-server: $schema=#schema.json\nkind: Deployment\n",
			filepath.Join(tmpDir, "config.yaml"),
		)
		url, data := resolveAndLoad(t, schema.Directive(), doc)
		assert.Equal(t, fileURL(t, schemaPath), url)
		assert.Equal(t, schemaData, data)
	})

	t.Run("returns ErrNoDirective when no directive present", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocumentWithPath(t, "kind: Deployment\n", filepath.Join(t.TempDir(), "config.yaml"))
		res := schema.Directive()
		_, err := res.Resolve(t.Context(), doc)
		require.ErrorIs(t, err, schema.ErrNoDirective)
		require.ErrorIs(t, err, schema.ErrNoMatch)
	})

	t.Run("returns ErrNoFilePath for a relative path without a file path", func(t *testing.T) {
		t.Parallel()

		// Create a document with a directive but no file path (from string input).
		doc := yamltest.FirstDocument(t, stringtest.Input(`
			# yaml-language-server: $schema=./schema.json
			kind: Deployment
		`))
		res := schema.Directive()
		_, err := res.Resolve(t.Context(), doc)
		require.ErrorIs(t, err, schema.ErrNoFilePath)
		require.ErrorIs(t, err, schema.ErrNoBaseDir)
		require.NotErrorIs(t, err, schema.ErrNoMatch)
	})

	t.Run("resolves a URL without a file path", func(t *testing.T) {
		t.Parallel()

		schemaData := `{"type": "object"}`

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			//nolint:errcheck // Test helper.
			w.Write([]byte(schemaData))
		}))
		defer server.Close()

		input := "# yaml-language-server: $schema=" + server.URL + "/schema.json\nkind: Deployment\n"

		doc := yamltest.FirstDocument(t, input)
		url, data := resolveAndLoad(t, schema.Directive(), doc)
		assert.Equal(t, server.URL+"/schema.json", url)
		assert.Equal(t, []byte(schemaData), data)
	})

	t.Run("resolves an absolute path without a file path", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		schemaPath := filepath.Join(tmpDir, "schema.json")
		schemaData := []byte(`{"type": "object"}`)
		err := os.WriteFile(schemaPath, schemaData, 0o600)
		require.NoError(t, err)

		doc := yamltest.FirstDocument(t, "# yaml-language-server: $schema="+schemaPath+"\nkind: Deployment\n")
		url, data := resolveAndLoad(t, schema.Directive(), doc)
		assert.Equal(t, fileURL(t, schemaPath), url)
		assert.Equal(t, schemaData, data)
	})

	t.Run("none turns validation off", func(t *testing.T) {
		t.Parallel()

		// Each level lists ten aliases of the level before it, so a decode
		// expands the last level to a million values.
		fanOut := stringtest.LinesLF(
			"a: &a [x, x, x, x, x, x, x, x, x, x]",
			"b: &b [*a, *a, *a, *a, *a, *a, *a, *a, *a, *a]",
			"c: &c [*b, *b, *b, *b, *b, *b, *b, *b, *b, *b]",
			"d: &d [*c, *c, *c, *c, *c, *c, *c, *c, *c, *c]",
			"e: &e [*d, *d, *d, *d, *d, *d, *d, *d, *d, *d]",
			"f: &f [*e, *e, *e, *e, *e, *e, *e, *e, *e, *e]",
			"g: &g [*f, *f, *f, *f, *f, *f, *f, *f, *f, *f]",
		)

		tests := map[string]struct {
			ref  string
			body string
		}{
			"lowercase":  {ref: "none", body: "kind: Deployment\n"},
			"uppercase":  {ref: "NONE", body: "kind: Deployment\n"},
			"mixed case": {ref: "None", body: "kind: Deployment\n"},
			// The document passes without a decode, which would reject an
			// alias that names no anchor.
			"undefined alias": {ref: "none", body: "a: *nope\n"},
			// The document passes without a decode, which would reject
			// aliases that expand past the limit.
			"excessive aliasing": {ref: "none", body: fanOut},
		}

		for name, tt := range tests {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, "# yaml-language-server: $schema="+tt.ref+"\n"+tt.body)

				ref, err := schema.Directive().Resolve(t.Context(), doc)
				require.NoError(t, err)
				assert.NotNil(t, ref.Schema())
				assert.Empty(t, ref.Key())

				// A schema that rejects every document follows the directive,
				// so the document passes only if the lookup ends at the
				// directive.
				reg := schema.NewRegistry(schema.WithResolvers(
					schema.Directive(),
					schema.MustCompile([]byte("false")),
				))
				require.NoError(t, reg.Validate(t.Context(), doc))
			})
		}
	})

	t.Run("./none names a file", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		schemaData := []byte(`{"type":"object"}`)
		err := os.WriteFile(filepath.Join(tmpDir, "none"), schemaData, 0o600)
		require.NoError(t, err)

		doc := yamltest.FirstDocumentWithPath(t,
			"# yaml-language-server: $schema=./none\nkind: Deployment\n",
			filepath.Join(tmpDir, "config.yaml"),
		)
		url, data := resolveAndLoad(t, schema.Directive(), doc)
		assert.Equal(t, schemaData, data)
		assert.Equal(t, fileURL(t, filepath.Join(tmpDir, "none")), url)
	})

	t.Run("resolves relative paths against document directory", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		schemaDir := filepath.Join(tmpDir, "schemas")
		err := os.MkdirAll(schemaDir, 0o755)
		require.NoError(t, err)

		schemaData := []byte(`{"type": "object"}`)
		err = os.WriteFile(filepath.Join(schemaDir, "schema.json"), schemaData, 0o600)
		require.NoError(t, err)

		// The directive climbs from configs/ to its sibling schemas/.
		doc := yamltest.FirstDocumentWithPath(t,
			"# yaml-language-server: $schema=../schemas/schema.json\nkind: Deployment\n",
			filepath.Join(tmpDir, "configs", "config.yaml"),
		)
		_, data := resolveAndLoad(t, schema.Directive(), doc)
		assert.Equal(t, schemaData, data)
	})

	t.Run("names a missing schema file and fails on load", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		doc := yamltest.FirstDocumentWithPath(t,
			"# yaml-language-server: $schema=nonexistent.json\nkind: Deployment\n",
			filepath.Join(tmpDir, "config.yaml"),
		)
		res := schema.Directive()
		ref, err := res.Resolve(t.Context(), doc)
		require.NoError(t, err)
		assert.Equal(t, fileURL(t, filepath.Join(tmpDir, "nonexistent.json")), ref.Key())

		_, err = schema.NewRegistry().Load(t.Context(), ref)
		require.ErrorIs(t, err, os.ErrNotExist)
		require.ErrorContains(t, err, "read")
		require.ErrorContains(t, err, "nonexistent.json")
	})
}

func TestDirective_EmbeddedNameMatchesPath(t *testing.T) {
	t.Parallel()

	// A document in testdata names schemas/name.json in its directive, which
	// joins to testdata/schemas/name.json. An embedded schema names its bytes
	// by their digest rather than by that path, so the two never share a cache
	// entry. The file requires a string name and the embedded schema requires
	// an integer, so each document passes only when the registry validates it
	// against its own schema.
	embedded := []byte(`{"type": "object", "properties": {"name": {"type": "integer"}}}`)

	tests := map[string]struct {
		order []string
	}{
		"embedded document first": {
			order: []string{"embedded", "directive"},
		},
		"directive document first": {
			order: []string{"directive", "embedded"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reg := schema.NewRegistry(schema.WithResolvers(
				schema.Directive(),
				schema.When(
					matcher.Content(kindPath, "Embedded"),
					schema.Embedded(embedded),
				),
			))

			docs := map[string]*niceyaml.Node{
				"embedded": yamltest.FirstDocument(t, "kind: Embedded\nname: 1\n"),
				"directive": yamltest.FirstDocumentWithPath(t,
					"# yaml-language-server: $schema=schemas/name.json\nname: text\n",
					filepath.Join("testdata", "config.yaml"),
				),
			}

			for _, key := range tt.order {
				err := reg.Validate(t.Context(), docs[key])
				require.NoError(t, err, "%s document", key)
			}
		})
	}
}

func TestDirective_LeadingCommentDocument(t *testing.T) {
	t.Parallel()

	// The comments and %YAML directives written above the first "---" are
	// the preamble of the document below them, so a directive there names
	// the schema of that document. The schema requires a "b" key, so a
	// document fails only when the registry checks it against this schema.
	//
	// A second resolver matching every document follows the directive
	// resolver, so a document with no directive reaches that resolver and
	// passes.
	const (
		valid   = "valid"   // Validated and passes.
		invalid = "invalid" // Validated against the schema and fails.
	)

	tcs := map[string]struct {
		input string
		want  []string
	}{
		"directive above the first header": {
			input: "# yaml-language-server: $schema=./schema.json\n---\nb: 2\n",
			want:  []string{valid},
		},
		"directive above the first header with invalid content": {
			input: "# yaml-language-server: $schema=./schema.json\n---\nc: 3\n",
			want:  []string{invalid},
		},
		"directive above a YAML directive": {
			input: "# yaml-language-server: $schema=./schema.json\n%YAML 1.2\n---\nb: 2\n",
			want:  []string{valid},
		},
		"directive below a YAML directive": {
			input: "%YAML 1.2\n# yaml-language-server: $schema=./schema.json\n---\nc: 3\n",
			want:  []string{invalid},
		},
		"directive does not reach past its document": {
			input: "# yaml-language-server: $schema=./schema.json\n---\nb: 2\n---\nc: 3\n",
			want:  []string{valid, valid},
		},
		"directive applies to an explicit empty document": {
			input: "# yaml-language-server: $schema=./schema.json\n---\n# note\n---\nc: 3\n",
			want:  []string{invalid, valid},
		},
		"directive above a later header": {
			input: "a: 1\n# yaml-language-server: $schema=./schema.json\n---\nc: 3\n",
			want:  []string{valid, invalid},
		},
		"directive above a later header after a value on its own line": {
			input: "a:\n  b\n# yaml-language-server: $schema=./schema.json\n---\nc: 3\n",
			want:  []string{valid, invalid},
		},
		"directive above a later header after an end marker": {
			input: "a: 1\n...\n# yaml-language-server: $schema=./schema.json\n---\nc: 3\n",
			want:  []string{valid, invalid},
		},
		"first directive in the preamble wins": {
			input: "# yaml-language-server: $schema=./schema.json\n---\n# yaml-language-server: $schema=./missing.json\nb: 2\n",
			want:  []string{valid},
		},
		"comment above the header without a directive": {
			input: "# note\n---\nc: 3\n",
			want:  []string{valid},
		},
		"comment-only file is the null document": {
			input: "# yaml-language-server: $schema=./schema.json\n",
			want:  []string{invalid},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tmpDir := t.TempDir()
			schemaData := []byte(`{"type": "object", "required": ["b"]}`)
			err := os.WriteFile(filepath.Join(tmpDir, "schema.json"), schemaData, 0o600)
			require.NoError(t, err)

			yamlPath := filepath.Join(tmpDir, "config.yaml")
			err = os.WriteFile(yamlPath, []byte(tc.input), 0o600)
			require.NoError(t, err)

			source, err := niceyaml.NewSourceFromFile(yamlPath)
			require.NoError(t, err)

			docs := source.AllDocuments()
			require.Len(t, docs, len(tc.want))

			reg := schema.NewRegistry(schema.WithResolvers(
				schema.Directive(),
				schema.Embedded([]byte(`{"type": "object"}`)),
			))

			for i, doc := range docs {
				err := reg.Validate(t.Context(), doc)

				switch tc.want[i] {
				case valid:
					require.NoError(t, err, "document %d", i)
				case invalid:
					require.Error(t, err, "document %d", i)
					require.NotErrorIs(t, err, schema.ErrNoMatch, "document %d", i)
				}
			}
		})
	}
}

// A directive resolves beside the file its document came from, wherever
// the process stands by the time the registry reads the schema, since
// NewSourceFromFile gives the document the absolute path of the file.
// The test changes the process's working directory, so it does not run
// in parallel.
//
//nolint:paralleltest // See above.
func TestDirective_ResolvesAfterChdir(t *testing.T) {
	repo := t.TempDir()

	writeFiles(t, repo, map[string]string{
		"configs/schema.json": `{"properties": {"name": {"type": "string"}}}`,
		"configs/app.yaml":    "# yaml-language-server: $schema=./schema.json\nname: 5\n",
	})

	t.Chdir(repo)

	source, err := niceyaml.NewSourceFromFile("configs/app.yaml")
	require.NoError(t, err)

	t.Chdir(t.TempDir())

	reg := schema.NewRegistry(schema.WithResolvers(schema.Directive()))

	err = source.ValidateDocuments(t.Context(), reg)
	require.ErrorContains(t, err, `configs/app.yaml:2:7: $.name: expected "string", got "integer"`)
}
