package schema_test

import (
	"cmp"
	"errors"
	"net/http"
	"os"
	"path/filepath"
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

// withDirective returns the document body under a yaml-language-server
// comment that names ref as the schema of the document.
func withDirective(ref, body string) string {
	return "# yaml-language-server: $schema=" + ref + "\n" + body
}

func TestParseDirective(t *testing.T) {
	t.Parallel()

	// Each want is the schema the directive names, or empty for a comment
	// that holds no directive.
	tcs := map[string]struct {
		input string
		want  string
	}{
		"valid file path": {
			input: " yaml-language-server: $schema=./schema.json",
			want:  "./schema.json",
		},
		"valid absolute path": {
			input: " yaml-language-server: $schema=/path/to/schema.json",
			want:  "/path/to/schema.json",
		},
		"valid http URL": {
			input: " yaml-language-server: $schema=http://example.com/schema.json",
			want:  "http://example.com/schema.json",
		},
		"valid https URL": {
			input: " yaml-language-server: $schema=https://example.com/schema.json",
			want:  "https://example.com/schema.json",
		},
		"remark after the reference": {
			input: " yaml-language-server: $schema=./schema.json # managed by tooling",
			want:  "./schema.json",
		},
		"path with spaces ends at the first space": {
			input: " yaml-language-server: $schema=./path with spaces/schema.json",
			want:  "./path",
		},
		"no spaces after colon": {
			input: " yaml-language-server:$schema=schema.json",
			want:  "schema.json",
		},
		"extra spaces": {
			input: " yaml-language-server:   $schema=schema.json",
			want:  "schema.json",
		},
		"space before colon": {
			input: " yaml-language-server : $schema=./s.json",
			want:  "./s.json",
		},
		"colon value form": {
			input: " yaml-language-server: $schema: ./s.json",
			want:  "./s.json",
		},
		"other settings before schema": {
			input: " yaml-language-server: foo=bar $schema=./s.json",
			want:  "./s.json",
		},
		"short form": {
			input: " $schema: ./s.json",
			want:  "./s.json",
		},
		"short form without space": {
			input: " $schema:./s.json",
			want:  "./s.json",
		},
		"short form with equals is not a directive": {input: " $schema=./s.json"},
		"short form after other text":               {input: " see $schema: ./s.json"},
		"short form with nothing after the colon":   {input: " $schema:   "},
		"none is returned as written": {
			input: " yaml-language-server: $schema=none",
			want:  "none",
		},
		"trailing whitespace": {
			input: " yaml-language-server: $schema=./schema.json   ",
			want:  "./schema.json",
		},
		"trailing tab": {
			input: " yaml-language-server: $schema=./schema.json\t",
			want:  "./schema.json",
		},
		"only whitespace after equals": {input: " yaml-language-server: $schema=   "},
		// The JavaScript \s, which yaml-language-server reads a directive
		// with, matches \v and the Unicode spaces too.
		"no-break space before a remark": {
			input: " yaml-language-server: $schema=./a.json\u00a0# managed",
			want:  "./a.json",
		},
		"vertical tab before a remark": {
			input: " yaml-language-server: $schema=./a.json\v#",
			want:  "./a.json",
		},
		"ideographic space before a remark": {
			input: " yaml-language-server: $schema=./a.json\u3000#",
			want:  "./a.json",
		},
		"byte order mark before a remark": {
			input: " yaml-language-server: $schema=./a.json\ufeff#",
			want:  "./a.json",
		},
		"no-break space after the colon value form": {
			input: " yaml-language-server: $schema:\u00a0./a.json",
			want:  "./a.json",
		},
		"no-break space after equals": {input: " yaml-language-server: $schema=\u00a0./a.json"},
		"no-break space before the marker": {
			input: "\u00a0yaml-language-server: $schema=./a.json",
			want:  "./a.json",
		},
		"empty string":                   {input: ""},
		"no directive":                   {input: " just a regular comment"},
		"partial match - missing schema": {input: " yaml-language-server: other=value"},
		"partial match - wrong prefix":   {input: " yaml-language: $schema=schema.json"},
		"case sensitive - uppercase":     {input: " YAML-LANGUAGE-SERVER: $schema=schema.json"},
		"marker after other text":        {input: " see yaml-language-server: $schema=./schema.json"},
		"marker with a prefix":           {input: " not-a-yaml-language-server: $schema=./schema.json"},
		"marker without leading space": {
			input: "yaml-language-server: $schema=./schema.json",
			want:  "./schema.json",
		},
		"marker after a tab": {
			input: "\tyaml-language-server: $schema=./schema.json",
			want:  "./schema.json",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := schema.ParseDirective(tc.input)
			if tc.want == "" {
				assert.Nil(t, got)

				return
			}

			require.NotNil(t, got)
			assert.Equal(t, tc.want, got.Schema)
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
			input: withDirective("./schema.json   ", "key: value\n"),
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
			input: stringtest.Input(`
				%YAML 1.2 # yaml-language-server: $schema=./schema.json
				---
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

func TestParseDocumentDirective_Tokens(t *testing.T) {
	t.Parallel()

	const comment = " yaml-language-server: $schema=./doc.json"

	tkb := yamltest.NewTokenBuilder()

	// Each case is a token stream that ParseDocumentDirective reads as it
	// is, and want is the schema its directive names, or empty for a
	// stream with no directive.
	tcs := map[string]struct {
		want   string
		tokens token.Tokens
	}{
		"document header precedes the directive": {
			tokens: token.Tokens{
				tkb.Clone().Type(token.DocumentHeaderType).Value("---").Build(),
				tkb.Clone().Type(token.CommentType).Value(comment).Build(),
				tkb.Clone().Type(token.StringType).Value("key").Build(),
				tkb.Clone().Type(token.MappingValueType).Value(":").Build(),
				tkb.Clone().Type(token.StringType).Value("value").Build(),
			},
			want: "./doc.json",
		},
		// The parser splits "%YAML 1.2" into a directive token and the
		// tokens of its value, and neither is content.
		"directive line precedes the directive": {
			tokens: token.Tokens{
				tkb.Clone().Type(token.DirectiveType).Value("%").PositionLine(1).Build(),
				tkb.Clone().Type(token.FloatType).Value("1.2").PositionLine(1).Build(),
				tkb.Clone().Type(token.CommentType).Value(comment).PositionLine(2).Build(),
				tkb.Clone().Type(token.StringType).Value("key").PositionLine(3).Build(),
				tkb.Clone().Type(token.MappingValueType).Value(":").PositionLine(3).Build(),
				tkb.Clone().Type(token.StringType).Value("value").PositionLine(3).Build(),
			},
			want: "./doc.json",
		},
		// ParseDocumentDirective skips only the directive's own line.
		"content on the line after a directive line": {
			tokens: token.Tokens{
				tkb.Clone().Type(token.DirectiveType).Value("%").PositionLine(1).Build(),
				tkb.Clone().Type(token.StringType).Value("YAML").PositionLine(1).Build(),
				tkb.Clone().Type(token.StringType).Value("key").PositionLine(2).Build(),
				tkb.Clone().Type(token.MappingValueType).Value(":").PositionLine(2).Build(),
				tkb.Clone().Type(token.CommentType).Value(comment).PositionLine(3).Build(),
			},
		},
		"content before the directive": {
			tokens: token.Tokens{
				tkb.Clone().Type(token.StringType).Value("key").Build(),
				tkb.Clone().Type(token.MappingValueType).Value(":").Build(),
				tkb.Clone().Type(token.StringType).Value("value").Build(),
				tkb.Clone().Type(token.CommentType).Value(comment).Build(),
			},
		},
		"nil tokens": {},
		// ParseDocumentDirective skips a nil token in the stream, as every
		// other token consumer skips it, rather than dereferencing it.
		"nil token in the stream": {
			tokens: token.Tokens{nil, tokens.Tokenize("#" + comment + "\n")[0]},
			want:   "./doc.json",
		},
		// A "..." marker is part of the preamble, as a "---" header is, so
		// a directive after it, or in a comment that trails it, still
		// names the schema of the document.
		"directive after a document end": {
			tokens: tokens.Tokenize(stringtest.LinesLF("...", "#"+comment, "j: 2")),
			want:   "./doc.json",
		},
		"directive after a header and a document end": {
			tokens: tokens.Tokenize(stringtest.LinesLF("---", "...", "#"+comment, "j: 2")),
			want:   "./doc.json",
		},
		"directive in a comment that trails a document end": {
			tokens: tokens.Tokenize(stringtest.LinesLF("... #"+comment, "j: 2")),
			want:   "./doc.json",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := schema.ParseDocumentDirective(tc.tokens)
			if tc.want == "" {
				assert.Nil(t, got)

				return
			}

			require.NotNil(t, got)
			assert.Equal(t, tc.want, got.Schema)
		})
	}

	t.Run("directive position is set from token", func(t *testing.T) {
		t.Parallel()

		got := schema.ParseDocumentDirective(token.Tokens{
			tkb.Clone().Type(token.CommentType).Value(comment).PositionLine(3).PositionColumn(5).Build(),
			tkb.Clone().Type(token.StringType).Value("key").Build(),
			tkb.Clone().Type(token.MappingValueType).Value(":").Build(),
			tkb.Clone().Type(token.StringType).Value("value").Build(),
		})

		require.NotNil(t, got)
		assert.Equal(t, "./doc.json", got.Schema)
		// The token counts from 1 and the directive from 0.
		assert.Equal(t, position.New(2, 4), got.Position)
	})
}

func TestDirective(t *testing.T) {
	t.Parallel()

	t.Run("registry fetches a directive URL with its client", func(t *testing.T) {
		t.Parallel()

		base := serve(t, map[string]string{"/schema.json": `{"type": "object"}`})

		doc := yamltest.FirstDocumentWithPath(t,
			withDirective(base+"/schema.json", "kind: Deployment\n"),
			filepath.Join(t.TempDir(), "config.yaml"),
		)

		client, requests := yamltest.CountingClient(http.DefaultTransport)

		reg := schema.NewRegistry(
			schema.WithHTTPClient(client),
			schema.WithResolvers(schema.Directive()),
		)

		err := reg.Check(t.Context(), doc)
		require.NoError(t, err)
		assert.Equal(t, int32(1), requests.Load())
	})

	t.Run("registry loads a YAML schema beside the document", func(t *testing.T) {
		t.Parallel()

		// The schema reads the type of a name from the YAML document
		// beside it.
		dir := t.TempDir()
		writeFiles(t, dir, map[string]string{
			"schema.yaml": stringtest.Input(`
				required: [name]
				properties:
				  name:
				    $ref: defs.yaml#/$defs/name
			`),
			"defs.yaml": stringtest.Input(`
				$defs:
				  name:
				    type: string
				  kinded:
				    required: [kind]
			`),
		})

		reg := schema.NewRegistry(schema.WithResolvers(schema.Directive()))

		tcs := map[string]struct {
			input string
			// Text of the violation, or empty for a valid document.
			want string
		}{
			"valid": {
				input: withDirective("./schema.yaml", "name: a\n"),
			},
			"violates the schema": {
				input: withDirective("./schema.yaml", "kind: Deployment\n"),
				want:  `missing required property "name"`,
			},
			"violates the document the $ref names": {
				input: withDirective("./schema.yaml", "name: 1\n"),
				want:  `expected "string", got "integer"`,
			},
			"valid under a fragment": {
				input: withDirective("./defs.yaml#/$defs/kinded", "kind: Deployment\n"),
			},
			"violates a fragment": {
				input: withDirective("./defs.yaml#/$defs/kinded", "name: a\n"),
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

				err := reg.Check(t.Context(), doc)
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
			withDirective("./schema.yaml", "name: a\n"),
			filepath.Join(dir, "config.yaml"),
		)

		err := schema.NewRegistry(schema.WithResolvers(schema.Directive())).Check(t.Context(), doc)
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
	tcs := map[string]struct {
		input string
		want  bool
	}{
		"returns true when document has valid schema directive": {
			input: withDirective("./schema.json", "kind: Deployment\n"),
			want:  true,
		},
		"returns false when document has no directive": {
			input: "kind: Deployment\n",
			want:  false,
		},
		"returns false when directive appears after content": {
			input: stringtest.LinesLF(
				"kind: Deployment",
				"# yaml-language-server: $schema=./schema.json",
			),
			want: false,
		},
		"returns true when directive follows document header": {
			input: stringtest.Input(`
				---
				# yaml-language-server: $schema=./schema.json
				kind: Deployment
			`),
			want: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocumentWithPath(t, tc.input, filepath.Join(t.TempDir(), "config.yaml"))
			res := schema.Directive()
			_, err := res.Resolve(t.Context(), doc)

			got := !errors.Is(err, schema.ErrNoMatch)
			assert.Equal(t, tc.want, got)

			if !tc.want {
				require.ErrorIs(t, err, schema.ErrNoDirective)
			}
		})
	}
}

func TestDirective_Resolve(t *testing.T) {
	t.Parallel()

	schemaData := []byte(`{"type": "object"}`)

	t.Run("successfully loads schema from file directive", func(t *testing.T) {
		t.Parallel()

		// Each case writes the schema to file under a temporary directory
		// and names it by a directive in a document at doc there, or at
		// config.yaml when it names no doc.
		tcs := map[string]struct {
			file      string
			doc       string
			directive string
		}{
			"file beside the document": {
				file:      "schema.json",
				directive: "schema.json",
			},
			"trailing whitespace": {
				file:      "schema.json",
				directive: "./schema.json   ",
			},
			// A path splits only at a '#' after its first character, as in
			// yaml-language-server, so "#schema.json" names a file.
			"leading '#' in a path": {
				file:      "#schema.json",
				directive: "#schema.json",
			},
			"./none names a file": {
				file:      "none",
				directive: "./none",
			},
			// The directive climbs from configs/ to its sibling schemas/.
			"path through the parent of the document": {
				file:      "schemas/schema.json",
				doc:       "configs/config.yaml",
				directive: "../schemas/schema.json",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				tmpDir := t.TempDir()
				writeFiles(t, tmpDir, map[string]string{tc.file: string(schemaData)})

				doc := yamltest.FirstDocumentWithPath(t,
					withDirective(tc.directive, "kind: Deployment\n"),
					filepath.Join(tmpDir, filepath.FromSlash(cmp.Or(tc.doc, "config.yaml"))),
				)
				url, data, err := loadFor(t, schema.Directive(), doc)
				require.NoError(t, err)
				assert.Equal(t, schemaData, data)
				assert.Equal(t, fileURL(t, filepath.Join(tmpDir, filepath.FromSlash(tc.file))), url)
			})
		}
	})

	t.Run("successfully loads schema from URL directive", func(t *testing.T) {
		t.Parallel()

		// A URL needs no directory to resolve against.
		tcs := map[string]struct {
			opts []niceyaml.SourceOption
		}{
			"document with a file path": {
				opts: []niceyaml.SourceOption{niceyaml.WithFilePath(filepath.Join(t.TempDir(), "config.yaml"))},
			},
			"document without a file path": {},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				schemaURL := serve(t, map[string]string{"/schema.json": string(schemaData)}) + "/schema.json"

				doc := yamltest.FirstDocument(t, withDirective(schemaURL, "kind: Deployment\n"), tc.opts...)
				url, data, err := loadFor(t, schema.Directive(), doc)
				require.NoError(t, err)
				assert.Equal(t, schemaData, data)
				assert.Equal(t, schemaURL, url)
			})
		}
	})

	t.Run("validates against the subschema a URL fragment names", func(t *testing.T) {
		t.Parallel()

		// The root takes any document, and Foo requires a name.
		base := serve(t, map[string]string{"/defs.json": `{
			"$schema": "http://json-schema.org/draft-07/schema#",
			"definitions": {"Foo": {"type": "object", "required": ["name"]}}
		}`})

		ref := base + "/defs.json#/definitions/Foo"
		reg := schema.NewRegistry(schema.WithResolvers(schema.Directive()))

		requireAccepts(t, reg, withDirective(ref, "name: x\n"))
		requireRejects(t, reg, withDirective(ref, "kind: Deployment\n"), `missing required property "name"`)
	})

	t.Run("validates against the subschema a path fragment names", func(t *testing.T) {
		t.Parallel()

		// The root takes any document, and Foo requires a name.
		schemaData := []byte(`{"definitions": {"Foo": {"type": "object", "required": ["name"]}}}`)
		schemaPath := writeSchema(t, "schema.json", schemaData)
		yamlPath := filepath.Join(filepath.Dir(schemaPath), "config.yaml")

		tcs := map[string]struct {
			ref string
		}{
			"relative path": {ref: "./schema.json#/definitions/Foo"},
			"absolute path": {ref: schemaPath + "#/definitions/Foo"},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				// The key keeps the fragment, and the load reads the whole
				// file.
				doc := yamltest.FirstDocumentWithPath(t, withDirective(tc.ref, "name: x\n"), yamlPath)
				url, data, err := loadFor(t, schema.Directive(), doc)
				require.NoError(t, err)
				assert.Equal(t, fileURL(t, schemaPath)+"#/definitions/Foo", url)
				assert.Equal(t, schemaData, data)

				reg := schema.NewRegistry(schema.WithResolvers(schema.Directive()))

				err = reg.Check(t.Context(), doc)
				require.NoError(t, err)

				doc = yamltest.FirstDocumentWithPath(t, withDirective(tc.ref, "kind: Deployment\n"), yamlPath)
				err = reg.Check(t.Context(), doc)
				require.ErrorContains(t, err, `missing required property "name"`)
			})
		}
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

	t.Run("resolves an absolute path without a file path", func(t *testing.T) {
		t.Parallel()

		schemaPath := writeSchema(t, "schema.json", schemaData)

		doc := yamltest.FirstDocument(t, withDirective(schemaPath, "kind: Deployment\n"))
		url, data, err := loadFor(t, schema.Directive(), doc)
		require.NoError(t, err)
		assert.Equal(t, fileURL(t, schemaPath), url)
		assert.Equal(t, schemaData, data)
	})

	t.Run("none turns validation off", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
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
			"excessive aliasing": {ref: "none", body: yamltest.AliasLevels(7)},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, withDirective(tc.ref, tc.body))

				ref, err := schema.Directive().Resolve(t.Context(), doc)
				require.NoError(t, err)
				assert.NotNil(t, ref.Schema())
				assert.Empty(t, ref.Key())

				// A schema that rejects every document follows the directive,
				// so the document passes only if the lookup ends at the
				// directive.
				reg := schema.NewRegistry(schema.WithResolvers(
					schema.Directive(),
					compileSchema(t, "false"),
				))
				require.NoError(t, reg.Check(t.Context(), doc))
			})
		}
	})

	t.Run("names a missing schema file and fails on load", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		doc := yamltest.FirstDocumentWithPath(t,
			withDirective("nonexistent.json", "kind: Deployment\n"),
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

	tcs := map[string]struct {
		order []string
	}{
		"embedded document first": {
			order: []string{"embedded", "directive"},
		},
		"directive document first": {
			order: []string{"directive", "embedded"},
		},
	}

	for name, tc := range tcs {
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
					withDirective("schemas/name.json", "name: text\n"),
					filepath.Join("testdata", "config.yaml"),
				),
			}

			for _, key := range tc.order {
				err := reg.Check(t.Context(), docs[key])
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
			input: stringtest.Input(`
				# yaml-language-server: $schema=./schema.json
				---
				b: 2
			`),
			want: []string{valid},
		},
		"directive above the first header with invalid content": {
			input: stringtest.Input(`
				# yaml-language-server: $schema=./schema.json
				---
				c: 3
			`),
			want: []string{invalid},
		},
		"directive above a YAML directive": {
			input: stringtest.Input(`
				# yaml-language-server: $schema=./schema.json
				%YAML 1.2
				---
				b: 2
			`),
			want: []string{valid},
		},
		"directive below a YAML directive": {
			input: stringtest.Input(`
				%YAML 1.2
				# yaml-language-server: $schema=./schema.json
				---
				c: 3
			`),
			want: []string{invalid},
		},
		"directive does not reach past its document": {
			input: stringtest.Input(`
				# yaml-language-server: $schema=./schema.json
				---
				b: 2
				---
				c: 3
			`),
			want: []string{valid, valid},
		},
		"directive applies to an explicit empty document": {
			input: stringtest.Input(`
				# yaml-language-server: $schema=./schema.json
				---
				# note
				---
				c: 3
			`),
			want: []string{invalid, valid},
		},
		"directive above a later header": {
			input: stringtest.Input(`
				a: 1
				# yaml-language-server: $schema=./schema.json
				---
				c: 3
			`),
			want: []string{valid, invalid},
		},
		"directive above a later header after a value on its own line": {
			input: stringtest.Input(`
				a:
				  b
				# yaml-language-server: $schema=./schema.json
				---
				c: 3
			`),
			want: []string{valid, invalid},
		},
		"directive above a later header after an end marker": {
			input: stringtest.Input(`
				a: 1
				...
				# yaml-language-server: $schema=./schema.json
				---
				c: 3
			`),
			want: []string{valid, invalid},
		},
		"first directive in the preamble wins": {
			input: stringtest.Input(`
				# yaml-language-server: $schema=./schema.json
				---
				# yaml-language-server: $schema=./missing.json
				b: 2
			`),
			want: []string{valid},
		},
		"comment above the header without a directive": {
			input: stringtest.Input(`
				# note
				---
				c: 3
			`),
			want: []string{valid},
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
			writeFiles(t, tmpDir, map[string]string{
				"schema.json": `{"type": "object", "required": ["b"]}`,
				"config.yaml": tc.input,
			})

			source, err := niceyaml.NewSourceFromFile(filepath.Join(tmpDir, "config.yaml"))
			require.NoError(t, err)

			docs := source.AllDocuments()
			require.Len(t, docs, len(tc.want))

			reg := schema.NewRegistry(schema.WithResolvers(
				schema.Directive(),
				schema.Embedded([]byte(`{"type": "object"}`)),
			))

			for i, doc := range docs {
				err := reg.Check(t.Context(), doc)

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
		"configs/app.yaml":    withDirective("./schema.json", "name: 5\n"),
	})

	t.Chdir(repo)

	source, err := niceyaml.NewSourceFromFile("configs/app.yaml")
	require.NoError(t, err)

	t.Chdir(t.TempDir())

	reg := schema.NewRegistry(schema.WithResolvers(schema.Directive()))

	err = source.ValidateDocuments(t.Context(), reg)
	require.ErrorContains(t, err, `configs/app.yaml:2:7: $.name: expected "string", got "integer"`)
}
