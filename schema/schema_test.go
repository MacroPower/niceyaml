package schema_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/jsonschema"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/schema"
)

// compileSchema compiles schemaData into a [*schema.Schema] with opts and
// fails the test if the schema does not compile.
func compileSchema(tb testing.TB, schemaData string, opts ...schema.CompileOption) *schema.Schema {
	tb.Helper()

	v, err := schema.Compile(tb.Context(), []byte(schemaData), opts...)
	require.NoError(tb, err)

	return v
}

// assertBoundTo fails the test unless every binding in the tree of err
// names source.
func assertBoundTo(t *testing.T, source *niceyaml.Source, err error) {
	t.Helper()

	for _, b := range yamltest.BoundIn(err) {
		assert.Same(t, source, b.Source())
	}
}

// requireUnresolved returns the path of bound, which selects nothing in
// doc. The test fails when bound carries no path or when the path
// resolves.
func requireUnresolved(t *testing.T, doc *niceyaml.Node, bound *niceyaml.SourceError) paths.Path {
	t.Helper()

	path, ok := bound.Path()
	require.True(t, ok, "bound error carries no path")

	_, err := doc.At(path)
	require.Error(t, err, "path from the error resolves")

	return path
}

// requireExcessiveAliasing fails the test unless err is a refusal of the
// alias limit. Such an error matches [schema.ErrExcessiveAliasing], the
// document or the value is at fault for it, and it does not wrap
// [schema.ErrValidate].
func requireExcessiveAliasing(t *testing.T, err error) {
	t.Helper()

	require.ErrorIs(t, err, schema.ErrExcessiveAliasing)
	require.NotErrorIs(t, err, schema.ErrValidate)
	assert.True(t, niceyaml.IsInvalid(err), "IsInvalid(%v)", err)
}

// Schemas that several tests share. A stringMembers document holds only
// strings, and a closedName document holds only a name, which is a string.
// The integersInM and integersInUser schemas take integers for the members
// of the mapping under m or under user.
const (
	stringMembers = `{
		"type": "object",
		"additionalProperties": {"type": "string"}
	}`
	closedName = `{
		"type": "object",
		"properties": {"name": {"type": "string"}},
		"additionalProperties": false
	}`
	integersInM = `{
		"type": "object",
		"properties": {
			"m": {"additionalProperties": {"type": "integer"}}
		}
	}`
	integersInUser = `{
		"type": "object",
		"properties": {
			"user": {"additionalProperties": {"type": "integer"}}
		}
	}`
)

func TestSchema_Check(t *testing.T) {
	t.Parallel()

	v := compileSchema(t, `{
		"type": "object",
		"properties": {
			"name": {"type": "string"},
			"age": {"type": "number"},
			"items": {
				"type": "array",
				"items": {"type": "string"}
			},
			"nested": {
				"type": "object",
				"properties": {
					"value": {"type": "string"}
				},
				"required": ["value"],
				"additionalProperties": false
			},
			"users": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {
						"id": {"type": "number"},
						"email": {"type": "string"},
						"profile": {
							"type": "object",
							"properties": {
								"firstName": {"type": "string"},
								"lastName": {"type": "string"},
								"preferences": {
									"type": "array",
									"items": {"type": "string"}
								}
							},
							"required": ["firstName", "lastName"],
							"additionalProperties": false
						}
					},
					"required": ["id", "email", "profile"],
					"additionalProperties": false
				}
			},
			"matrix": {
				"type": "array",
				"items": {
					"type": "array",
					"items": {"type": "number"}
				}
			}
		},
		"required": ["name"],
		"additionalProperties": false
	}`)

	tcs := map[string]struct {
		input   any
		wantErr bool
	}{
		"valid data": {
			input: map[string]any{
				"name": "Kallistō",
				"age":  30,
			},
		},
		"missing required field": {
			input: map[string]any{
				"age": 30,
			},
			wantErr: true,
		},
		"wrong type for name": {
			input: map[string]any{
				"name": 123,
				"age":  30,
			},
			wantErr: true,
		},
		"wrong type for age": {
			input: map[string]any{
				"name": "Kallistō",
				"age":  "thirty",
			},
			wantErr: true,
		},
		"invalid array item": {
			input: map[string]any{
				"name":  "John",
				"items": []any{"valid", 123, "also valid"},
			},
			wantErr: true,
		},
		"nested object validation error": {
			input: map[string]any{
				"name": "Kallistō",
				"nested": map[string]any{
					"notValue": "something",
				},
			},
			wantErr: true,
		},
		"valid array of objects": {
			input: map[string]any{
				"name": "Kallistō",
				"users": []any{
					map[string]any{
						"id":    1,
						"email": "kallisto@example.com",
						"profile": map[string]any{
							"firstName": "Kallistō",
							"lastName":  "Lykaonis",
						},
					},
					map[string]any{
						"id":    2,
						"email": "aello@example.com",
						"profile": map[string]any{
							"firstName":   "Jane",
							"lastName":    "Thaumantias",
							"preferences": []any{"dark_mode", "notifications"},
						},
					},
				},
			},
		},
		"invalid object in array": {
			input: map[string]any{
				"name": "Kallistō",
				"users": []any{
					map[string]any{
						"id":    "invalid", // Should be number.
						"email": "aello@example.com",
						"profile": map[string]any{
							"firstName": "Aëllo",
							"lastName":  "Thaumantias",
						},
					},
				},
			},
			wantErr: true,
		},
		"missing required field in nested object within array": {
			input: map[string]any{
				"name": "Kallistō",
				"users": []any{
					map[string]any{
						"id":    1,
						"email": "kallisto@example.com",
						"profile": map[string]any{
							"firstName": "Kallistō",
							// Missing lastName.
						},
					},
				},
			},
			wantErr: true,
		},
		"valid matrix (2D array)": {
			input: map[string]any{
				"name": "Kallistō",
				"matrix": []any{
					[]any{1, 2, 3},
					[]any{4, 5, 6},
				},
			},
		},
		"invalid element in 2D array": {
			input: map[string]any{
				"name": "Kallistō",
				"matrix": []any{
					[]any{1, 2, 3},
					[]any{4, "invalid", 6}, // Should be number.
				},
			},
			wantErr: true,
		},
		"additional property at root": {
			input: map[string]any{
				"name":      "John",
				"extraProp": "not allowed",
			},
			wantErr: true,
		},
		"additional property in nested object": {
			input: map[string]any{
				"name": "Kallistō",
				"nested": map[string]any{
					"value":       "valid",
					"extraNested": "not allowed",
				},
			},
			wantErr: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := v.ValidateValue(t.Context(), tc.input)

			if tc.wantErr {
				var validationErr *niceyaml.Error

				require.ErrorAs(t, err, &validationErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestCompile_RequireRefs(t *testing.T) {
	t.Parallel()

	const nope = "https://example.invalid/nope.json"

	// The message of the compile error for a $ref to document.
	unresolved := func(document string) string {
		return fmt.Sprintf("no ref resolver for $ref to %q: schema URI not resolved", document)
	}

	serving := schema.WithJSONSchemaOptions(jsonschema.WithRefResolver(jsonschema.SchemaMap{
		nope: {},
	}))

	tcs := map[string]struct {
		schema string
		// The text of the compile error, or empty for a schema that
		// compiles.
		want string
		opts []schema.CompileOption
	}{
		"ref to another document": {
			schema: `{"properties": {"a": {"$ref": "` + nope + `#/$defs/a"}}}`,
			want:   "compile schema: " + unresolved(nope),
		},
		"relative ref": {
			schema: `{"properties": {"a": {"$ref": "defs.json#/$defs/port"}}}`,
			want:   "compile schema: " + unresolved("defs.json"),
		},
		"dynamic ref to another document": {
			schema: `{"properties": {"a": {"$dynamicRef": "` + nope + `#node"}}}`,
			want:   "compile schema: " + unresolved(nope),
		},
		"under a branch no document takes": {
			schema: `{"if": false, "then": {"$ref": "` + nope + `"}}`,
			want:   "compile schema: " + unresolved(nope),
		},
		"in a definition nothing refers to": {
			schema: `{"$defs": {"unused": {"$ref": "` + nope + `"}}}`,
			want:   "compile schema: " + unresolved(nope),
		},
		"two refs to one document": {
			schema: `{"allOf": [{"$ref": "` + nope + `#/$defs/a"}, {"$ref": "` + nope + `#/$defs/b"}]}`,
			want:   "compile schema: " + unresolved(nope),
		},
		"refs to two documents": {
			schema: `{"allOf": [{"$ref": "a.json"}, {"$ref": "b.json"}]}`,
			want: stringtest.JoinLF(
				"compile schema: "+unresolved("a.json"),
				unresolved("b.json"),
			),
		},
		"password in the ref": {
			schema: `{"$ref": "https://user:secret@example.invalid/nope.json"}`,
			want:   "compile schema: " + unresolved("https://user:xxxxx@example.invalid/nope.json"),
		},
		"required again after not required": {
			opts:   []schema.CompileOption{schema.WithRequireRefs(false), schema.WithRequireRefs(true)},
			schema: `{"$ref": "` + nope + `"}`,
			want:   "compile schema: " + unresolved(nope),
		},
		"refs not required": {
			opts:   []schema.CompileOption{schema.WithRequireRefs(false)},
			schema: `{"properties": {"a": {"$ref": "` + nope + `"}}}`,
		},
		"ref inside the schema": {
			schema: `{"properties": {"a": {"$ref": "#/$defs/a"}}, "$defs": {"a": {"type": "string"}}}`,
		},
		"ref to a document the schema holds": {
			schema: `{
				"properties": {"a": {"$ref": "` + nope + `"}},
				"$defs": {"held": {"$id": "` + nope + `", "type": "string"}}
			}`,
		},
		"ref to the schema by its own id": {
			schema: `{
				"$id": "` + nope + `",
				"properties": {"a": {"$ref": "` + nope + `#/$defs/a"}},
				"$defs": {"a": {"type": "string"}}
			}`,
		},
		"resolver that serves the document": {
			opts:   []schema.CompileOption{serving},
			schema: `{"properties": {"a": {"$ref": "` + nope + `"}}}`,
		},
		// The compile cannot tell which documents a resolver of the
		// caller's serves, so the validation reports the ref.
		"resolver that does not serve the document": {
			opts:   []schema.CompileOption{serving},
			schema: `{"properties": {"a": {"$ref": "https://example.invalid/other.json"}}}`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s, err := schema.Compile(t.Context(), []byte(tc.schema), tc.opts...)
			if tc.want == "" {
				require.NoError(t, err)
				assert.NotNil(t, s)

				return
			}

			require.ErrorIs(t, err, schema.ErrCompile)
			require.ErrorIs(t, err, jsonschema.ErrNotResolved)
			assert.Equal(t, tc.want, err.Error())
			assert.Nil(t, s)

			assert.PanicsWithError(t, tc.want, func() {
				schema.MustCompile([]byte(tc.schema), tc.opts...)
			})
		})
	}
}

func TestCompile_Document(t *testing.T) {
	t.Parallel()

	// Each schema that compiles requires a "name" key, unless the case
	// says what it accepts.
	tcs := map[string]struct {
		// Wrapped by the compile error, if any.
		err error
		// The schema document, in JSON or YAML.
		schema string
		// Text the compile error holds, or empty for a schema that
		// compiles.
		want string
		// A document the schema accepts and one it rejects, or empty for
		// a schema with no such document.
		valid   string
		invalid string
	}{
		"JSON object": {
			schema:  `{"required": ["name"]}`,
			valid:   "name: a\n",
			invalid: "value: 1\n",
		},
		"JSON object after white space": {
			schema:  "\r\n\t {\"required\": [\"name\"]}",
			valid:   "name: a\n",
			invalid: "value: 1\n",
		},
		"YAML mapping": {
			schema:  "type: object\nrequired: [name]\n",
			valid:   "name: a\n",
			invalid: "value: 1\n",
		},
		"YAML after a comment": {
			schema:  "# Every document names itself.\nrequired: [name]\n",
			valid:   "name: a\n",
			invalid: "value: 1\n",
		},
		"YAML after a document start": {
			schema:  "---\nrequired: [name]\n",
			valid:   "name: a\n",
			invalid: "value: 1\n",
		},
		"YAML with a merge key": {
			schema: stringtest.Input(`
				$defs:
				  named: &named
				    required: [name]
				allOf:
				  - <<: *named
			`),
			valid:   "name: a\n",
			invalid: "value: 1\n",
		},
		"YAML with a $ref inside the schema": {
			schema: stringtest.Input(`
				$ref: '#/$defs/named'
				$defs:
				  named:
				    required: [name]
			`),
			valid:   "name: a\n",
			invalid: "value: 1\n",
		},
		"true": {
			schema: "true",
			valid:  "value: 1\n",
		},
		"false": {
			schema:  "false\n",
			invalid: "value: 1\n",
		},
		// The first byte picks the JSON reader, and nothing reads the
		// data as YAML after the JSON reader refuses it.
		"YAML flow mapping": {
			schema: "{required: [name]}",
			want:   "compile schema: JSON decode: ",
		},
		"JSON with a trailing comma": {
			schema: `{"required": ["name"],}`,
			want:   "compile schema: JSON decode: ",
		},
		"JSON with single quotes": {
			schema: `{'required': ['name']}`,
			want:   "compile schema: JSON decode: ",
		},
		// Read as YAML, the member is null, and the schema would accept
		// every document.
		"JSON member with no value": {
			schema: `{"required": }`,
			want:   "compile schema: JSON decode: ",
		},
		"JSON cut short": {
			schema: `{"required": ["name"]`,
			want:   "compile schema: JSON decode: ",
		},
		"YAML that does not parse": {
			schema: stringtest.Input(`
				properties:
				  name: {type: string
				required: [name]
			`),
			want: "compile schema: YAML decode: 3:1: ",
		},
		"YAML duplicate key": {
			schema: "type: object\ntype: string\n",
			want:   `compile schema: YAML decode: 2:1: mapping key "type" already defined`,
		},
		"two YAML documents": {
			schema: stringtest.Input(`
				type: object
				---
				type: string
			`),
			want: "compile schema: YAML decode: 2:1: multiple documents in source: 2 documents",
		},
		"YAML aliases past the limit": {
			schema: yamltest.AliasLevels(7),
			want:   "compile schema: YAML decode: 1:1: excessive aliasing",
		},
		"YAML infinity": {
			schema: "maximum: .inf\n",
			want:   "compile schema: encode schema document: ",
		},
		"YAML sequence": {
			schema: "- required: [name]\n",
			want:   "compile schema: schema document must be a JSON object or boolean",
			err:    jsonschema.ErrInvalidSchemaDocument,
		},
		"text": {
			schema: "Not Found",
			want:   "compile schema: schema document must be a JSON object or boolean",
			err:    jsonschema.ErrInvalidSchemaDocument,
		},
		"null": {
			schema: "null",
			want:   "compile schema: schema document must be a JSON object or boolean",
			err:    jsonschema.ErrInvalidSchemaDocument,
		},
		"only a comment": {
			schema: "# No schema yet.\n",
			want:   "compile schema: schema document must be a JSON object or boolean",
			err:    jsonschema.ErrInvalidSchemaDocument,
		},
		"empty": {
			schema: "",
			want:   "compile schema: schema document must be a JSON object or boolean",
			err:    jsonschema.ErrInvalidSchemaDocument,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s, err := schema.Compile(t.Context(), []byte(tc.schema))
			if tc.want != "" {
				require.ErrorIs(t, err, schema.ErrCompile)
				require.ErrorContains(t, err, tc.want)
				assert.Nil(t, s)

				if tc.err != nil {
					require.ErrorIs(t, err, tc.err)
				}

				// The text of the schema is at fault, and no document is.
				require.NotErrorIs(t, err, niceyaml.ErrSyntax)
				require.NotErrorIs(t, err, schema.ErrExcessiveAliasing)
				assert.False(t, niceyaml.IsInvalid(err), "IsInvalid(%v)", err)

				assert.Panics(t, func() { schema.MustCompile([]byte(tc.schema)) })

				return
			}

			require.NoError(t, err)

			if tc.valid != "" {
				require.NoError(t, yamltest.FirstDocument(t, tc.valid).Validate(t.Context(), s))
			}

			if tc.invalid != "" {
				err := yamltest.FirstDocument(t, tc.invalid).Validate(t.Context(), s)
				require.Error(t, err)
				assert.True(t, niceyaml.IsInvalid(err), "IsInvalid(%v)", err)
			}
		})
	}
}

func TestCompile_YAMLNumbers(t *testing.T) {
	t.Parallel()

	// The schema of each YAML case holds one keyword for the member v.
	forV := func(keyword string) string {
		return stringtest.LinesLF("properties:", "  v:", "    "+keyword)
	}

	// A schema in YAML reads each number as a decode into any reads it,
	// and a document reads its numbers the same way.
	tcs := map[string]struct {
		// The schema document, in JSON or YAML.
		schema string
		// Text the compile error holds, or empty for a schema that
		// compiles.
		want string
		// Documents the schema accepts and documents it rejects.
		valid   []string
		invalid []string
	}{
		"exponent without a point is text": {
			schema:  forV("const: 1e3"),
			valid:   []string{"v: 1e3\n", "v: '1e3'\n"},
			invalid: []string{"v: 1000\n", "v: 1.0e3\n"},
		},
		"exponent with a point is a number": {
			schema:  forV("const: 1.0e3"),
			valid:   []string{"v: 1000\n", "v: 1.0e3\n"},
			invalid: []string{"v: 1e3\n"},
		},
		"exponent in JSON is a number": {
			schema:  `{"properties": {"v": {"const": 1e3}}}`,
			valid:   []string{"v: 1000\n", "v: 1.0e3\n"},
			invalid: []string{"v: 1e3\n"},
		},
		"leading zero is octal": {
			schema:  forV("const: 010"),
			valid:   []string{"v: 8\n", "v: 010\n"},
			invalid: []string{"v: 10\n"},
		},
		"leading zero in a count is octal": {
			schema:  forV("maxLength: 010"),
			valid:   []string{"v: 12345678\n"},
			invalid: []string{"v: '123456789'\n"},
		},
		"integer past 64 bits is text": {
			schema:  forV("const: 18446744073709551616"),
			valid:   []string{"v: 18446744073709551616\n", "v: '18446744073709551616'\n"},
			invalid: []string{"v: 1.8446744073709551616e+19\n"},
		},
		"integer past 2^53 stays exact": {
			schema:  forV("const: 9007199254740993"),
			valid:   []string{"v: 9007199254740993\n"},
			invalid: []string{"v: 9007199254740992\n"},
		},
		"text under a keyword that takes a number": {
			schema: "maximum: 1e3\n",
			want:   "compile schema: keyword value of a type the schema field cannot hold",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s, err := schema.Compile(t.Context(), []byte(tc.schema))
			if tc.want != "" {
				require.ErrorIs(t, err, schema.ErrCompile)
				require.ErrorContains(t, err, tc.want)

				return
			}

			require.NoError(t, err)

			for _, doc := range tc.valid {
				require.NoError(t, yamltest.FirstDocument(t, doc).Validate(t.Context(), s), doc)
			}

			for _, doc := range tc.invalid {
				require.Error(t, yamltest.FirstDocument(t, doc).Validate(t.Context(), s), doc)
			}
		})
	}
}

func TestSchema_UnresolvableRef(t *testing.T) {
	t.Parallel()

	unreachable := errors.New("host unreachable")
	refusing := jsonschema.RefResolverFunc(func(_ context.Context, _ string) (*jsonschema.Schema, error) {
		return nil, unreachable
	})

	// Each schema compiles with a $ref that does not resolve, either
	// because the compile does not require it to or because a resolver
	// of the caller's stands in for the check. The validator reports the
	// ref when it walks to it, so the failure arrives at the location of
	// the value that referenced it. The schema is at fault, not the
	// document, so every case wraps ErrValidate.
	tcs := map[string]struct {
		data   map[string]any
		err    error
		schema string
		want   string
		opts   []schema.CompileOption
	}{
		"no resolver": {
			opts:   []schema.CompileOption{schema.WithRequireRefs(false)},
			schema: `{"properties": {"a": {"$ref": "https://example.invalid/nope.json"}}}`,
			data:   map[string]any{"a": 1},
			want:   `cannot resolve $ref "https://example.invalid/nope.json"`,
		},
		"resolver answering not resolved": {
			opts: []schema.CompileOption{schema.WithJSONSchemaOptions(
				jsonschema.WithRefResolver(jsonschema.SchemaMap{}),
			)},
			schema: `{"properties": {"a": {"$ref": "https://example.invalid/nope.json"}}}`,
			data:   map[string]any{"a": 1},
			want:   `cannot resolve $ref "https://example.invalid/nope.json"`,
		},
		"resolver returning an error": {
			opts: []schema.CompileOption{schema.WithJSONSchemaOptions(
				jsonschema.WithRefResolver(refusing),
			)},
			schema: `{"properties": {"a": {"$ref": "https://example.invalid/nope.json"}}}`,
			data:   map[string]any{"a": 1},
			err:    unreachable,
			want:   "host unreachable",
		},
		"beside a violation": {
			opts: []schema.CompileOption{schema.WithRequireRefs(false)},
			schema: `{"properties": {
				"a": {"$ref": "https://example.invalid/nope.json"},
				"b": {"type": "string"}
			}}`,
			data: map[string]any{"a": 1, "b": 2},
			want: `cannot resolve $ref "https://example.invalid/nope.json"`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			v := compileSchema(t, tc.schema, tc.opts...)

			err := v.ValidateValue(t.Context(), tc.data)
			require.ErrorIs(t, err, schema.ErrValidate)
			assert.Contains(t, err.Error(), tc.want)

			if tc.err != nil {
				require.ErrorIs(t, err, jsonschema.ErrRefResolve)
				require.ErrorIs(t, err, tc.err)
			}

			// The failure states the error of the resolver, so the tree
			// shows the two in one row.
			assert.Equal(t, 1, strings.Count(niceyaml.FormatError(err, niceyaml.WithContextLines(0)), tc.want))

			var failure *jsonschema.ValidationError

			require.ErrorAs(t, err, &failure)
			assert.Equal(t, jsonschema.KeywordRef, failure.Keyword)

			var nerr *niceyaml.Error

			require.NotErrorAs(t, err, &nerr)
		})
	}
}

func TestSchema_PathTarget(t *testing.T) {
	t.Parallel()

	// The validator chooses key vs value highlighting based on the type of
	// validation error. Each case checks which part of the YAML the error
	// overlay style wraps.
	tcs := map[string]struct {
		schema string
		input  string
		want   string // The text the error style wraps.
	}{
		"type error highlights value": {
			schema: `{
				"type": "object",
				"properties": {
					"name": {"type": "string"}
				}
			}`,
			input: "name: 123",
			want:  "123",
		},
		"additional property highlights key": {
			schema: closedName,
			input: stringtest.Input(`
				name: valid
				extra: notAllowed
			`),
			want: "extra",
		},
		"required error highlights the parent key": {
			// A missing required property carries the path it would have,
			// which binds at the key of the mapping that lacks it.
			schema: `{
				"type": "object",
				"properties": {
					"user": {
						"type": "object",
						"properties": {
							"name": {"type": "string"}
						},
						"required": ["name"]
					}
				}
			}`,
			input: stringtest.Input(`
				user:
				  age: 30
			`),
			want: "user",
		},
		"error behind a merge key highlights the anchored value": {
			// The validator sees merged data, so the path leads through the
			// merge key to the anchor that defines the offending value.
			schema: `{
				"type": "object",
				"properties": {
					"user": {
						"type": "object",
						"properties": {
							"age": {"type": "integer"}
						}
					}
				}
			}`,
			input: stringtest.Input(`
				base: &b
				  age: old
				user:
				  <<: *b
				  name: x
			`),
			want: "old",
		},
		"required error on the root highlights the first key": {
			schema: `{
				"type": "object",
				"required": ["name"]
			}`,
			input: "other: x",
			want:  "other",
		},
		"enum error highlights value": {
			schema: `{
				"type": "object",
				"properties": {
					"status": {"enum": ["active", "inactive"]}
				}
			}`,
			input: "status: unknown",
			want:  "unknown",
		},
		"minimum error highlights value": {
			schema: `{
				"type": "object",
				"properties": {
					"age": {"type": "integer", "minimum": 0}
				}
			}`,
			input: "age: -5",
			want:  "-5",
		},
		"pattern error highlights value": {
			schema: `{
				"type": "object",
				"properties": {
					"email": {"type": "string", "pattern": "^[a-z]+@[a-z]+\\.[a-z]+$"}
				}
			}`,
			input: "email: notanemail",
			want:  "notanemail",
		},
		"minItems error highlights key": {
			schema: `{
				"type": "object",
				"properties": {
					"items": {"type": "array", "minItems": 2}
				}
			}`,
			input: stringtest.Input(`
				items:
				  - one
			`),
			want: "items",
		},
		"array item type error highlights value": {
			schema: `{
				"type": "object",
				"properties": {
					"numbers": {
						"type": "array",
						"items": {"type": "integer"}
					}
				}
			}`,
			input: stringtest.Input(`
				numbers:
				  - 1
				  - notanumber
				  - 3
			`),
			want: "notanumber",
		},
		"propertyNames error highlights the offending key": {
			// The library reports a propertyNames violation at the offending
			// property with keyword "propertyNames", so the validator targets
			// the bad key.
			schema: `{
				"type": "object",
				"properties": {
					"config": {
						"type": "object",
						"propertyNames": {"pattern": "^[a-z]+$"}
					}
				}
			}`,
			input: stringtest.Input(`
				config:
				  BadKey: 1
			`),
			want: "BadKey",
		},
		"hexadecimal key highlights value": {
			// The key decodes to the member name 16, which the source spells
			// 0x10, so the path names the member by its source spelling.
			schema: `{
				"type": "object",
				"properties": {
					"16": {"type": "integer"}
				}
			}`,
			input: "0x10: hello",
			want:  "hello",
		},
		"tilde null key highlights value": {
			// The key decodes to the member name null, which the source
			// spells ~, so the path names the member by its source spelling.
			schema: stringMembers,
			input:  "~: 5",
			want:   "5",
		},
		"spelled-out null key highlights value": {
			schema: stringMembers,
			input:  "NULL: 5",
			want:   "5",
		},
		"boolean key highlights the key": {
			schema: closedName,
			input:  "True: nope",
			want:   "True",
		},
		"false subschema on a keyword-named property highlights value": {
			// A property named like a keyword that constrains a key
			// ("additionalProperties") must still highlight the value, not
			// the key.
			schema: `{
				"type": "object",
				"properties": {
					"additionalProperties": false
				}
			}`,
			input: "additionalProperties: 5",
			want:  "5",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			v := compileSchema(t, tc.schema)

			err := yamltest.FirstDocument(t, tc.input).Validate(t.Context(), v)
			bound := yamltest.RequireSourceError(t, err)

			assert.Contains(t, yamltest.NewXMLPrinter().PrintError(bound),
				"<genericError>"+tc.want+"</genericError>")
		})
	}
}

func TestSchema_NonFiniteFloats(t *testing.T) {
	t.Parallel()

	// YAML decodes .nan/.inf into non-finite float64 values. These are not
	// JSON-encodable, but the validator treats them as numbers rather than
	// surfacing an opaque marshaling error.
	v := compileSchema(t, `{
		"type": "object",
		"properties": {"x": {"type": "number"}}
	}`)

	for name, value := range map[string]float64{
		"NaN":          math.NaN(),
		"positive inf": math.Inf(1),
		"negative inf": math.Inf(-1),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			require.NoError(t, v.ValidateValue(t.Context(), map[string]any{"x": value}))
		})
	}
}

func TestSchema_YAMLNativeTypes(t *testing.T) {
	t.Parallel()

	// YAML decodes !!binary into []byte and !!timestamp into time.Time,
	// neither of which the JSON Schema validator accepts. The validator
	// converts them to their JSON spellings, so a tagged scalar does not
	// suppress every other constraint in the document. A !!timestamp the
	// source wrote as a bare date becomes a full-date, the spelling the same
	// date has without the tag, also behind an alias or a merge key.
	//
	// Several cases share a schema, which takes a date-time or a date for
	// d, or a date for the d of the mapping under m.
	const (
		dateTime = `{
			"type": "object",
			"properties": {"d": {"type": "string", "format": "date-time"}}
		}`
		date = `{
			"type": "object",
			"properties": {"d": {"type": "string", "format": "date"}}
		}`
		dateInM = `{
			"type": "object",
			"properties": {
				"m": {
					"type": "object",
					"properties": {"d": {"type": "string", "format": "date"}}
				}
			}
		}`
	)

	tcs := map[string]struct {
		schema string
		input  string
		err    string
		opts   []niceyaml.SourceOption
	}{
		"binary scalar": {
			schema: `{
				"type": "object",
				"properties": {"b": {"type": "string"}}
			}`,
			input: "b: !!binary aGk=",
		},
		"timestamp scalar": {
			schema: `{
				"type": "object",
				"properties": {"d": {"type": "string"}}
			}`,
			input: "d: !!timestamp 2024-01-01T00:00:00Z",
		},
		"date-only timestamp matches format date": {
			schema: date,
			input:  "d: !!timestamp 2001-12-14",
		},
		"date-only timestamp is no date-time": {
			schema: dateTime,
			input:  "d: !!timestamp 2001-12-14",
			err:    `1:16: $.d: string does not match format "date-time"`,
		},
		"midnight date-time timestamp keeps its time": {
			schema: dateTime,
			input:  "d: !!timestamp 2001-12-14T00:00:00Z",
		},
		"date-only timestamp in a sequence": {
			schema: `{
				"type": "object",
				"properties": {
					"d": {
						"type": "array",
						"items": {"type": "string", "format": "date"}
					}
				}
			}`,
			input: "d: [!!timestamp 2001-12-14]",
		},
		"date-only block scalar timestamp matches format date": {
			schema: date,
			input: stringtest.Input(`
				d: !!timestamp >-
				  2001-12-14
			`),
		},
		"repeated key keeps the later midnight date-time": {
			schema: dateTime,
			input: stringtest.Input(`
				d: !!timestamp 2001-12-14
				d: !!timestamp 2001-12-14T00:00:00Z
			`),
			opts: []niceyaml.SourceOption{niceyaml.WithAllowDuplicateKeys(true)},
		},
		"repeated key keeps the later date": {
			schema: date,
			input: stringtest.Input(`
				d: !!timestamp 2001-12-14T00:00:00Z
				d: !!timestamp 2001-12-14
			`),
			opts: []niceyaml.SourceOption{niceyaml.WithAllowDuplicateKeys(true)},
		},
		"tagged key keeps the later midnight date-time": {
			// The !!null tag makes the key x name the member null.
			schema: `{
				"type": "object",
				"properties": {"null": {"type": "string", "format": "date-time"}}
			}`,
			input: stringtest.Input(`
				null: !!timestamp 2001-12-14
				!!null x: !!timestamp 2001-12-14T00:00:00Z
			`),
		},
		"alias key keeps the later midnight date-time": {
			// The alias key names the member d, as its anchored value does.
			schema: dateTime,
			input: stringtest.Input(`
				a: &k d
				d: !!timestamp 2001-12-14
				*k : !!timestamp 2001-12-14T00:00:00Z
			`),
		},
		"merge key keeps the merged midnight date-time": {
			// The merge key sets d after the member written before it.
			schema: dateTime,
			input: stringtest.Input(`
				base: &base
				  d: !!timestamp 2001-12-14T00:00:00Z
				d: !!timestamp 2001-12-14
				<<: *base
			`),
		},
		"date-only timestamp behind an alias matches format date": {
			schema: `{
				"type": "object",
				"properties": {
					"a": {"type": "string", "format": "date"},
					"d": {"type": "string", "format": "date"}
				}
			}`,
			input: stringtest.Input(`
				a: &x !!timestamp 2001-12-14
				d: *x
			`),
		},
		"date-only timestamp in an aliased mapping matches format date": {
			schema: dateInM,
			input: stringtest.Input(`
				base: &b {d: !!timestamp 2001-12-14}
				m: *b
			`),
		},
		"merged date-only timestamp matches format date": {
			schema: dateInM,
			input: stringtest.Input(`
				base: &b {d: !!timestamp 2001-12-14}
				m: {<<: *b}
			`),
		},
		"merged date hidden by a same-spelled key stays a date": {
			// The quoted key decodes to the member 0x10, so the merge
			// still sets the member 16.
			schema: `{
				"type": "object",
				"properties": {
					"m": {
						"type": "object",
						"properties": {"16": {"type": "string", "format": "date"}}
					}
				}
			}`,
			input: `m: {<<: {0x10: !!timestamp 2001-12-14}, "0x10": 5}`,
		},
		"merge sequence keeps the later source's date": {
			// A later merge source sets d over an earlier one.
			schema: dateInM,
			input: stringtest.Input(`
				b1: &b1
				  d: !!timestamp 2001-12-14T00:00:00Z
				b2: &b2
				  d: !!timestamp 2001-12-14
				m: {<<: [*b1, *b2]}
			`),
		},
		"merge sequence keeps the later source's midnight date-time": {
			schema: `{
				"type": "object",
				"properties": {
					"m": {
						"type": "object",
						"properties": {"d": {"type": "string", "format": "date-time"}}
					}
				}
			}`,
			input: stringtest.Input(`
				b1: &b1
				  d: !!timestamp 2001-12-14
				b2: &b2
				  d: !!timestamp 2001-12-14T00:00:00Z
				m: {<<: [*b1, *b2]}
			`),
		},
		"date under a second tag keeps the date-time": {
			// The !!int tag hands the !!timestamp tag a number rather than
			// the text, so the decode yields the zero time.
			schema: dateTime,
			input:  "d: !!timestamp !!int 2001-12-14",
		},
		"binary scalar violating a constraint": {
			schema: `{
				"type": "object",
				"properties": {"n": {"type": "integer"}}
			}`,
			input: stringtest.Input(`
				b: !!binary aGk=
				n: notint
			`),
			err: `2:4: $.n: expected "integer", got "string"`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			v := compileSchema(t, tc.schema,
				schema.WithJSONSchemaOptions(jsonschema.WithFormats(true)))

			err := yamltest.FirstDocument(t, tc.input, tc.opts...).Validate(t.Context(), v)
			if tc.err == "" {
				require.NoError(t, err)

				return
			}

			require.ErrorContains(t, err, tc.err)
			require.NotErrorIs(t, err, schema.ErrValidate)
		})
	}
}

// requestSchema is a schema for the body of a request, which comes from
// no document. A port of 0 breaks its minimum, and a body with no name
// breaks its required.
const requestSchema = `{
	"type": "object",
	"properties": {
		"port": {"type": "integer", "minimum": 1},
		"name": {"type": "string"}
	},
	"required": ["name"]
}`

var (
	// A document that holds a request with a port of 0 under the key
	// request.
	requestDocument = stringtest.LinesLF(
		"request:",
		"  port: 0",
	)

	// A sentinel a caller wraps beside the error of a check.
	errPlaceCheck = errors.New("check")
)

func TestSchema_ValidateValue(t *testing.T) {
	t.Parallel()

	v := compileSchema(t, requestSchema)

	// The value came from no document, so the error is bound to a source
	// with no text and no name. Its text then names the path of each
	// violation wherever the error prints.
	tcs := map[string]struct {
		data       map[string]any
		want       string
		wantFormat string
		wantPaths  []string
	}{
		"one violation": {
			data:       map[string]any{"port": 0, "name": "x"},
			want:       "$.port: 0 is less than 1",
			wantFormat: "$.port: 0 is less than 1",
			wantPaths:  []string{"$.port"},
		},
		"two violations": {
			data: map[string]any{"port": 0},
			want: stringtest.JoinLF(
				"2 schema violations",
				"$.port: 0 is less than 1",
				`$.name: missing required property "name"`,
			),
			wantFormat: stringtest.JoinLF(
				"2 schema violations",
				"|-- $.port: 0 is less than 1",
				"`-- $.name: missing required property \"name\"",
			),
			wantPaths: []string{"$.port", "$.name"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := v.ValidateValue(t.Context(), tc.data)
			require.EqualError(t, err, tc.want)

			// A wrapper and a join keep the text of the error they hold,
			// so each path shows through both.
			assert.Equal(t, tc.want, fmt.Sprintf("%v", err))
			require.EqualError(t, fmt.Errorf("invalid request: %w", err), "invalid request: "+tc.want)
			require.EqualError(t, errors.Join(errors.New("other failure"), err), "other failure\n"+tc.want)

			// The value is at fault, and the first violation is in reach.
			assert.True(t, niceyaml.IsInvalid(err))
			require.NotErrorIs(t, err, schema.ErrValidate)

			assert.Equal(t, jsonschema.KeywordMinimum, requireViolation(t, err).Keyword)

			// The source holds no line to excerpt, so the tree stands
			// alone, with no line that says so.
			assert.Equal(t, tc.wantFormat, niceyaml.FormatError(err))
			assert.Equal(t, tc.wantFormat, fmt.Sprintf("%+v", err))

			bound := yamltest.RequireSourceError(t, err)
			assert.Empty(t, bound.Source().Name())
			assert.Zero(t, bound.Source().Lines().Len())

			var gotPaths []string

			for problem := range niceyaml.NewErrorTree(err).Problems() {
				require.NotNil(t, problem.Bound)

				_, ok := problem.Bound.Position()
				assert.False(t, ok)

				path, ok := problem.Path()
				require.True(t, ok)

				gotPaths = append(gotPaths, path.String())
			}

			assert.Equal(t, tc.wantPaths, gotPaths)

			// The value came from no document, so no binding of the
			// result names a Node, a document, or the index of one.
			for _, b := range yamltest.BoundIn(err) {
				assert.Nil(t, b.Node())
				assert.Nil(t, b.Document())

				_, ok := b.DocumentIndex()
				assert.False(t, ok)

				// The reason for a path is the one of a document with no
				// content, which FormatError leaves out.
				if _, hasPath := b.Path(); hasPath {
					require.ErrorIs(t, b.Unresolved(), paths.ErrNoDocument)
				}
			}
		})
	}

	t.Run("conforming value", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, v.ValidateValue(t.Context(), map[string]any{"port": 80, "name": "x"}))
	})

	t.Run("the result reads the same once a document placed it", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, requestDocument, niceyaml.WithName("app.yaml"))
		base := paths.Doc().Child("request")

		err := v.ValidateValue(t.Context(), map[string]any{"port": 0, "name": "x"})
		require.EqualError(t, err, "$.port: 0 is less than 1")

		got := doc.Bind(niceyaml.Rebase(err, base))
		require.EqualError(t, got, "app.yaml:2:9: $.request.port: 0 is less than 1")
		assert.NotSame(t, err, got)

		bound := yamltest.RequireSourceError(t, got)
		assert.Same(t, doc.Source(), bound.Source())

		// Placing the result builds a new error and leaves the result as
		// it was.
		require.EqualError(t, err, "$.port: 0 is less than 1")

		require.ErrorAs(t, err, &bound)
		assert.NotSame(t, doc.Source(), bound.Source())
	})
}

func TestSchema_ValidateValue_Place(t *testing.T) {
	t.Parallel()

	v := compileSchema(t, requestSchema)

	doc := yamltest.FirstDocument(t, requestDocument, niceyaml.WithName("app.yaml"))
	base := paths.Doc().Child("request")

	// A port of 0 breaks the schema once, and a request with no name
	// breaks it a second time.
	one := v.ValidateValue(t.Context(), map[string]any{"port": 0, "name": "x"})
	two := v.ValidateValue(t.Context(), map[string]any{"port": 0})

	// The result stands in no document, so Rebase and Bind place it as
	// they place an error that no source bound yet. Each path then shows
	// once, with or without a wrapper around the result.
	tcs := map[string]struct {
		result      error
		wantBound   string
		wantWrapped string
		// The result bound through the root with no Rebase, where each
		// path reads from the root of the document.
		wantRoot string
	}{
		"one violation": {
			result:      one,
			wantBound:   "app.yaml:2:9: $.request.port: 0 is less than 1",
			wantWrapped: "app.yaml:2:9: $.request.port: check: 0 is less than 1",
			wantRoot:    "app.yaml:1:1: $.port: 0 is less than 1",
		},
		"two violations": {
			result: two,
			wantBound: stringtest.JoinLF(
				"app.yaml: 2 schema violations",
				`app.yaml:1:1: $.request.name: missing required property "name"`,
				"app.yaml:2:9: $.request.port: 0 is less than 1",
			),
			wantWrapped: stringtest.JoinLF(
				"app.yaml: check: 2 schema violations",
				`app.yaml:1:1: $.request.name: missing required property "name"`,
				"app.yaml:2:9: $.request.port: 0 is less than 1",
			),
			wantRoot: stringtest.JoinLF(
				"app.yaml: 2 schema violations",
				"app.yaml:1:1: $.port: 0 is less than 1",
				`app.yaml:1:1: $.name: missing required property "name"`,
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := tc.result

			placed := doc.Bind(niceyaml.Rebase(err, base))
			require.EqualError(t, placed, tc.wantBound)
			assert.True(t, niceyaml.IsInvalid(placed))

			var violation *schema.Violation

			require.ErrorAs(t, placed, &violation)
			assertBoundTo(t, doc.Source(), placed)

			require.EqualError(t,
				doc.Bind(niceyaml.Rebase(fmt.Errorf("check: %w", err), base)),
				tc.wantWrapped,
			)
			require.EqualError(t,
				doc.Bind(niceyaml.Rebase(fmt.Errorf("check: %w", fmt.Errorf("body: %w", err)), base)),
				strings.Replace(tc.wantWrapped, "check: ", "check: body: ", 1),
			)

			// A wrapper that names a sentinel beside the result reads as
			// a wrapper around the result alone.
			require.EqualError(t,
				doc.Bind(niceyaml.Rebase(fmt.Errorf("%w: %w", errPlaceCheck, err), base)),
				tc.wantWrapped,
			)

			// A Node scoped to the value puts its own path in front, and
			// the root reads each path from the root of the document.
			require.EqualError(t, yamltest.At(t, doc, base).Bind(err), tc.wantBound)
			require.EqualError(t, doc.Bind(err), tc.wantRoot)

			// A summary and a join place each result they hold.
			require.EqualError(t,
				doc.Bind(niceyaml.Rebase(errors.Join(err), base)),
				tc.wantBound,
			)
		})
	}

	t.Run("a wrapper keeps what it matches", func(t *testing.T) {
		t.Parallel()

		wrapped := &placeCheckError{err: one}
		require.EqualError(t, wrapped, "check: $.port: 0 is less than 1")

		placed := doc.Bind(niceyaml.Rebase(wrapped, base))
		require.EqualError(t, placed, "app.yaml:2:9: $.request.port: check: 0 is less than 1")
		require.ErrorIs(t, placed, wrapped)

		var got *placeCheckError

		require.ErrorAs(t, placed, &got)
		assert.Same(t, wrapped, got)
	})

	t.Run("a wrapper keeps the errors beside the result", func(t *testing.T) {
		t.Parallel()

		typed := &placeCheckError{err: errPlaceCheck}

		placed := doc.Bind(niceyaml.Rebase(fmt.Errorf("%w: %w", typed, one), base))
		require.EqualError(t, placed, "app.yaml:2:9: $.request.port: check: check: 0 is less than 1")
		require.ErrorIs(t, placed, errPlaceCheck)
		assert.True(t, niceyaml.IsInvalid(placed))

		var got *placeCheckError

		require.ErrorAs(t, placed, &got)
		assert.Same(t, typed, got)

		var violation *schema.Violation

		require.ErrorAs(t, placed, &violation)
	})

	t.Run("a wrapper that rewrites the text keeps its text", func(t *testing.T) {
		t.Parallel()

		// The text of each wrapper holds the text of the result nowhere, or
		// twice, so no path could leave it. The document places every
		// violation all the same, and the wrapper keeps the text it wrote.
		tcs := map[string]struct {
			err  error
			want string
		}{
			"message of its own": {
				err:  placeFixedError{err: one},
				want: "app.yaml:2:9: $.request.port: invalid body",
			},
			"message of its own above two violations": {
				err: placeFixedError{err: two},
				want: stringtest.JoinLF(
					"app.yaml: invalid body",
					`app.yaml:1:1: $.request.name: missing required property "name"`,
					"app.yaml:2:9: $.request.port: 0 is less than 1",
				),
			},
			"message of its own beside a sentinel": {
				err:  fmt.Errorf("%w: %w", errPlaceCheck, placeFixedError{err: one}),
				want: "app.yaml:2:9: $.request.port: check: invalid body",
			},
			"upper case": {
				err:  placeShoutError{err: one},
				want: "app.yaml:2:9: $.request.port: $.PORT: 0 IS LESS THAN 1",
			},
			"indented lines": {
				err: placeIndentError{err: two},
				want: stringtest.JoinLF(
					"app.yaml: validation:",
					"  2 schema violations",
					"  $.port: 0 is less than 1",
					`  $.name: missing required property "name"`,
					`app.yaml:1:1: $.request.name: missing required property "name"`,
					"app.yaml:2:9: $.request.port: 0 is less than 1",
				),
			},
			"text held twice": {
				err:  fmt.Errorf("%w (again: %s)", one, one.Error()),
				want: "app.yaml:2:9: $.request.port: $.port: 0 is less than 1 (again: $.port: 0 is less than 1)",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				placed := doc.Bind(niceyaml.Rebase(tc.err, base))
				require.EqualError(t, placed, tc.want)
				require.ErrorIs(t, placed, tc.err)
				assert.True(t, niceyaml.IsInvalid(placed))

				var violation *schema.Violation

				require.ErrorAs(t, placed, &violation)
				assertBoundTo(t, doc.Source(), placed)
			})
		}
	})

	t.Run("a Validate method returns the result", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			request placeRequest
			want    string
		}{
			"as it is": {
				request: placeRequest{schema: v},
				want:    "app.yaml:2:9: $.request.port: 0 is less than 1",
			},
			"under a message of its own": {
				request: placeRequest{schema: v, fixed: true},
				want:    "app.yaml:2:9: $.request.port: invalid body",
			},
			"inside Invalid": {
				request: placeRequest{schema: v, invalid: true},
				want:    "app.yaml:2:9: $.request.port: 0 is less than 1",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				cfg := placeConfig{Request: tc.request}

				err := doc.DecodeInto(t.Context(), &cfg)
				require.EqualError(t, err, tc.want)
				assert.True(t, niceyaml.IsInvalid(err))
			})
		}
	})

	t.Run("an Error with no option places the result", func(t *testing.T) {
		t.Parallel()

		request := yamltest.At(t, doc, base)

		const want = "app.yaml:2:9: $.request.port: 0 is less than 1"

		// Invalid and Place with no option add nothing to the result, so
		// it places as it does alone.
		require.EqualError(t, request.Bind(niceyaml.Invalid(one)), want)
		require.EqualError(t, request.Bind(niceyaml.Place(one)), want)
		require.EqualError(t, doc.Bind(niceyaml.Rebase(niceyaml.Invalid(one), base)), want)
		require.EqualError(t,
			doc.Bind(niceyaml.Rebase(fmt.Errorf("check: %w", niceyaml.Invalid(one)), base)),
			"app.yaml:2:9: $.request.port: check: 0 is less than 1",
		)
		require.EqualError(t,
			doc.Bind(niceyaml.Rebase(fmt.Errorf("%w: %w", errPlaceCheck, niceyaml.Place(one)), base)),
			"app.yaml:2:9: $.request.port: check: 0 is less than 1",
		)

		assertBoundTo(t, doc.Source(), request.Bind(niceyaml.Invalid(one)))
	})

	t.Run("an Error with a location or details places the result", func(t *testing.T) {
		t.Parallel()

		at := niceyaml.AtPath(paths.Current())
		why := niceyaml.WithDetails(errors.New("the body of the request"))

		// An Error writes the message of the error it wraps, so the Error
		// binds around the violations as it binds around errors that no
		// source bound yet. No line keeps a path from the value.
		tcs := map[string]struct {
			err         error
			want        string
			wantDetails []string
		}{
			"location above one violation": {
				err:  niceyaml.Invalid(one, at),
				want: "app.yaml:1:1: $.request: 0 is less than 1",
			},
			"location above two violations": {
				err: niceyaml.Invalid(two, at),
				want: stringtest.JoinLF(
					"app.yaml:1:1: $.request: 2 schema violations",
					`app.yaml:1:1: $.request.name: missing required property "name"`,
					"app.yaml:2:9: $.request.port: 0 is less than 1",
				),
			},
			"wrapper around a location": {
				err: fmt.Errorf("check: %w", niceyaml.Place(two, at)),
				want: stringtest.JoinLF(
					"app.yaml:1:1: $.request: check: 2 schema violations",
					`app.yaml:1:1: $.request.name: missing required property "name"`,
					"app.yaml:2:9: $.request.port: 0 is less than 1",
				),
			},
			"details beside one violation": {
				err:         niceyaml.Place(one, why),
				want:        "app.yaml:2:9: $.request.port: 0 is less than 1",
				wantDetails: []string{"the body of the request"},
			},
			"details beside two violations": {
				err: niceyaml.Invalid(two, why),
				want: stringtest.JoinLF(
					"app.yaml: 2 schema violations",
					`app.yaml:1:1: $.request.name: missing required property "name"`,
					"app.yaml:2:9: $.request.port: 0 is less than 1",
				),
				wantDetails: []string{"the body of the request"},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				placed := doc.Bind(niceyaml.Rebase(tc.err, base))
				require.EqualError(t, placed, tc.want)
				assert.True(t, niceyaml.IsInvalid(placed))

				bound := yamltest.RequireSourceError(t, placed)

				var gotDetails []string

				for _, detail := range yamltest.DetailsOf(bound) {
					gotDetails = append(gotDetails, detail.Message())
				}

				assert.Equal(t, tc.wantDetails, gotDetails)

				for _, problem := range yamltest.ProblemsOf(bound) {
					assert.Same(t, doc.Source(), problem.Source())
				}
			})
		}
	})

	t.Run("a Validator returns the result", func(t *testing.T) {
		t.Parallel()

		check := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
			return v.ValidateValue(ctx, map[string]any{"port": 0, "name": "x"})
		})

		err := yamltest.At(t, doc, base).Validate(t.Context(), check)
		require.EqualError(t, err, "app.yaml:2:9: $.request.port: 0 is less than 1")
	})

	t.Run("a summary and a detail place the results they hold", func(t *testing.T) {
		t.Parallel()

		unnamed := v.ValidateValue(t.Context(), map[string]any{"port": 80})

		summary := niceyaml.NewSummary("request checks", one, unnamed)
		require.EqualError(t, doc.Bind(niceyaml.Rebase(summary, base)), stringtest.JoinLF(
			"app.yaml: request checks",
			`app.yaml:1:1: $.request.name: missing required property "name"`,
			"app.yaml:2:9: $.request.port: 0 is less than 1",
		))

		detailed := niceyaml.NewError(
			"bad request",
			niceyaml.AtPath(base),
			niceyaml.WithDetails(niceyaml.Rebase(one, base)),
		)

		bound := yamltest.RequireSourceError(t, doc.Bind(detailed))
		require.Len(t, yamltest.DetailsOf(bound), 1)
		assert.Same(t, doc.Source(), yamltest.DetailsOf(bound)[0].Source())
	})

	t.Run("each violation places on its own", func(t *testing.T) {
		t.Parallel()

		result := yamltest.RequireSourceError(t, two)

		violations := yamltest.ProblemsOf(result)
		require.Len(t, violations, 2)

		// A caller that keeps only some of the violations places the
		// ones it keeps, and each stands in the document as it does
		// when the caller places the whole result.
		want := []string{
			"app.yaml:2:9: $.request.port: 0 is less than 1",
			`app.yaml:1:1: $.request.name: missing required property "name"`,
		}

		for i, violation := range violations {
			placed := doc.Bind(niceyaml.Rebase(violation.Err, base))
			require.EqualError(t, placed, want[i])
			assert.True(t, niceyaml.IsInvalid(placed))
			assertBoundTo(t, doc.Source(), placed)

			// The violation itself stays where the result put it.
			assert.NotSame(t, doc.Source(), violation.Source())
		}

		kept := errors.Join(violations[1].Err, violations[0].Err)
		require.EqualError(t, yamltest.At(t, doc, base).Bind(kept), stringtest.JoinLF(want[1], want[0]))
	})

	t.Run("path names a key as the decoder does", func(t *testing.T) {
		t.Parallel()

		ports := compileSchema(t, `{"properties": {"ports": {"additionalProperties": {"minimum": 1}}}}`)
		hex := yamltest.FirstDocument(t, stringtest.LinesLF("ports:", "  0x10: 0"), niceyaml.WithName("app.yaml"))

		data, err := hex.Decode[any](t.Context())
		require.NoError(t, err)

		// The decoder names the key 0x10 as 16, and ValidateValue reads no
		// source that spells it another way.
		err = ports.ValidateValue(t.Context(), data)

		located := yamltest.RequireSourceError(t, err)

		path, ok := located.Path()
		require.True(t, ok)
		assert.Equal(t, "$.ports.16", path.String())

		// No key of the document has that spelling, so the path binds at
		// the key of the mapping.
		require.EqualError(t, hex.Bind(err), "app.yaml:1:1: $.ports.16: 0 is less than 1")

		// A DataLocator reads the names as the decoder does, so it finds
		// the entry.
		var names []string

		for sel := range path.Selectors() {
			names = append(names, sel.Name)
		}

		found := niceyaml.NewError(located.Message(), hex.DataLocator().At(names...))
		require.EqualError(t, hex.Bind(found), "app.yaml:2:9: $.ports.0x10: 0 is less than 1")
	})
}

// placeCheckError wraps the error of a check with a prefix, as a caller's
// own error type does.
type placeCheckError struct {
	err error
}

func (e *placeCheckError) Error() string {
	return "check: " + e.err.Error()
}

func (e *placeCheckError) Unwrap() error {
	return e.err
}

// placeShoutError wraps an error and rewrites its text.
type placeShoutError struct {
	err error
}

func (e placeShoutError) Error() string {
	return strings.ToUpper(e.err.Error())
}

func (e placeShoutError) Unwrap() error {
	return e.err
}

// placeFixedError wraps an error under a message of its own, as an error
// type that maps a failure to a status does.
type placeFixedError struct {
	err error
}

func (e placeFixedError) Error() string {
	return "invalid body"
}

func (e placeFixedError) Unwrap() error {
	return e.err
}

// placeIndentError wraps an error and indents each line of its text.
type placeIndentError struct {
	err error
}

func (e placeIndentError) Error() string {
	return "validation:\n  " + strings.ReplaceAll(e.err.Error(), "\n", "\n  ")
}

func (e placeIndentError) Unwrap() error {
	return e.err
}

// placeConfig holds a [placeRequest] under the key request.
type placeConfig struct {
	Request placeRequest `yaml:"request"`
}

// placeRequest checks itself against a schema the test sets before the
// decode, and returns what ValidateValue returns. With invalid, it
// returns that result inside [niceyaml.Invalid] with no option. With
// fixed, it returns that result inside a [placeFixedError].
type placeRequest struct {
	schema *schema.Schema

	Port int `yaml:"port"`

	invalid bool
	fixed   bool
}

func (r placeRequest) Validate() error {
	err := r.schema.ValidateValue(context.Background(), map[string]any{"port": r.Port, "name": "x"})

	switch {
	case r.invalid:
		return niceyaml.Invalid(err)

	case r.fixed:
		return placeFixedError{err: err}
	}

	//nolint:wrapcheck // The test checks where a decode places the result as it is.
	return err
}

func TestSchema_ValidateValue_OrderedMap(t *testing.T) {
	t.Parallel()

	// A decode with niceyaml.WithYAMLOrderedMaps yields yaml.MapSlice for
	// every mapping, which the JSON Schema validator does not accept.
	// ValidateValue converts each one to a map with the same members, so
	// the schema checks the data as it checks a plain decode.
	//
	// Each level of the bomb maps ten keys to the level below, so the last
	// level expands to 10^4 mappings.
	var bomb strings.Builder

	bomb.WriteString("l0: &l0 {a: x}\n")

	for level := 1; level <= 4; level++ {
		members := make([]string, 0, 10)
		for i := range 10 {
			members = append(members, fmt.Sprintf("k%d: *l%d", i, level-1))
		}

		fmt.Fprintf(&bomb, "l%d: &l%d {%s}\n", level, level, strings.Join(members, ", "))
	}

	// Two cases share a schema, which takes a string for the member a.
	const stringA = `{
		"type": "object",
		"properties": {"a": {"type": "string"}}
	}`

	tcs := map[string]struct {
		schema    string
		input     string
		err       string
		excessive bool
	}{
		"conforming ordered mapping": {
			schema: stringA,
			input:  "a: x",
		},
		"top-level violation": {
			schema: stringA,
			input:  "a: 1",
			err:    `$.a: expected "string", got "integer"`,
		},
		"nested ordered mapping": {
			schema: `{
				"type": "object",
				"properties": {
					"n": {
						"type": "object",
						"properties": {"b": {"type": "integer"}}
					}
				}
			}`,
			input: "n: {b: notint}",
			err:   `$.n.b: expected "integer", got "string"`,
		},
		"ordered mapping inside a sequence": {
			schema: `{
				"type": "object",
				"properties": {
					"items": {
						"type": "array",
						"items": {
							"type": "object",
							"properties": {"b": {"type": "integer"}}
						}
					}
				}
			}`,
			input: "items: [{b: notint}]",
			err:   `$.items[0].b: expected "integer", got "string"`,
		},
		"binary scalar inside ordered mapping": {
			schema: `{
				"type": "object",
				"properties": {"b": {"type": "string"}}
			}`,
			input: "b: !!binary aGk=",
		},
		"alias bomb of ordered mappings": {
			schema:    `{"type": "object"}`,
			input:     bomb.String(),
			excessive: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			v := compileSchema(t, tc.schema)

			// The source turns the alias limit off, so the bomb decodes.
			// ValidateValue takes no source and applies the limit to the
			// value all the same.
			doc := yamltest.FirstDocument(t, tc.input, niceyaml.WithAliasLimit(false))

			data, err := doc.Decode[any](t.Context(), niceyaml.WithYAMLOrderedMaps(true))
			require.NoError(t, err)

			err = v.ValidateValue(t.Context(), data)

			switch {
			case tc.excessive:
				requireExcessiveAliasing(t, err)

			case tc.err != "":
				require.EqualError(t, err, tc.err)
				require.NotErrorIs(t, err, schema.ErrValidate)

			default:
				require.NoError(t, err)
			}
		})
	}
}

func TestSchema_Check_OrderedMapDates(t *testing.T) {
	t.Parallel()

	// The niceyaml.WithYAMLOrderedMaps option reaches the decode that
	// gets it and not the decode the schema runs, so the schema reads
	// plain maps, and a date-only timestamp under a key the decoder
	// respells keeps its full-date spelling.
	v := compileSchema(t,
		`{"additionalProperties": {"type": "string", "format": "date"}}`,
		schema.WithJSONSchemaOptions(jsonschema.WithFormats(true)))

	tcs := map[string]struct {
		input string
	}{
		"hexadecimal key": {input: "0x10: !!timestamp 2001-12-14\n"},
		"null key":        {input: "~: !!timestamp 2001-12-14\n"},
		"bool-tagged key": {input: "!!bool yes: !!timestamp 2001-12-14\n"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input)

			_, err := doc.Decode[any](t.Context(),
				niceyaml.WithYAMLOrderedMaps(true),
				niceyaml.WithValidator(v),
			)
			require.NoError(t, err)
		})
	}
}

func TestSchema_AliasExpansion(t *testing.T) {
	t.Parallel()

	// A decode shares an anchored map, slice, or !!binary between its
	// aliases, so each alias adds a full copy of the anchored value to the
	// data the validator would read, and each level of aliases nested in
	// aliases multiplies it. Validation rejects such a value before
	// checking it, and rejects a value that contains itself.
	t.Run("documents", func(t *testing.T) {
		t.Parallel()

		// Each document from binaryAliases anchors size bytes as a
		// !!binary and lists count aliases of it.
		binaryAliases := func(size, count int) string {
			text := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xab}, size))

			return stringtest.LinesLF(
				"bin: &bin !!binary "+text,
				"list: "+yamltest.FlowSequence("*bin", count),
			)
		}

		tcs := map[string]struct {
			schema    string
			input     string
			err       string
			excessive bool
		}{
			"alias bomb": {
				schema:    `{"type": "object"}`,
				input:     yamltest.AliasLevels(8),
				excessive: true,
			},
			"binary aliased many times": {
				// Each alias would add the 22 KB of base64 text again.
				schema:    `{"type": "object"}`,
				input:     binaryAliases(16<<10, 1000),
				excessive: true,
			},
			"binary aliased a few times": {
				// The aliases make up nine tenths of the base64 text, which
				// stays within the limit.
				schema: `{"properties": {"list": {"items": {"type": "string"}}}}`,
				input:  binaryAliases(4<<10, 10),
			},
			"small mapping aliased in each item of a long list": {
				// Each alias counts as a node of the document, so the 150
				// aliases stay a small enough share of what a decode reads.
				schema: `{"properties": {"matrix": {"items": {"required": ["os"]}}}}`,
				input: stringtest.LinesLF(
					"base: &b {os: linux, arch: amd64, go: stable, cgo: false}",
					"matrix:",
				) + strings.Repeat("  - *b\n", 150),
			},
			"anchor reused a few times": {
				schema: `{
					"type": "object",
					"additionalProperties": {
						"type": "object",
						"properties": {"a": {"type": "string"}}
					}
				}`,
				input: stringtest.Input(`
					base: &b {a: 1}
					x1: *b
					x2: *b
					x3: *b
				`),
				err: "4 schema violations",
			},
			"alias to a sibling in an anchor": {
				schema: `{
					"properties": {
						"base": {"properties": {"health": {"type": "integer"}}}
					}
				}`,
				input: stringtest.Input(`
					base: &b
					  port: &p 80
					  health: *p
				`),
			},
			"merge keys": {
				schema: `{
					"type": "object",
					"additionalProperties": {
						"type": "object",
						"required": ["name", "replicas"]
					}
				}`,
				input: stringtest.Input(`
					defaults: &defaults {name: app, replicas: 1}
					dev:
					  <<: *defaults
					staging:
					  <<: *defaults
					  replicas: 2
					prod:
					  <<: *defaults
					  replicas: 3
				`),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				v := compileSchema(t, tc.schema)
				doc := yamltest.FirstDocument(t, tc.input)

				// A source with the alias limit off decodes the value
				// ValidateValue checks, which applies the limit to it all
				// the same.
				trusted := yamltest.FirstDocument(t, tc.input, niceyaml.WithAliasLimit(false))

				data, err := trusted.Decode[any](t.Context())
				require.NoError(t, err)

				for _, err := range []error{
					doc.Validate(t.Context(), v),
					v.ValidateValue(t.Context(), data),
				} {
					switch {
					case tc.excessive:
						requireExcessiveAliasing(t, err)

					case tc.err != "":
						require.ErrorContains(t, err, tc.err)
						require.NotErrorIs(t, err, schema.ErrValidate)

					default:
						require.NoError(t, err)
					}
				}
			})
		}
	})

	// The decoder writes out in full what an alias refers to when it
	// spells a key or a !!str value that holds one, and it reads a mapping
	// a merge key brings in again at every merge. Such a document costs
	// its expanded size while it decodes, and the decoded value may share
	// nothing, as when a later key replaces the member that held the
	// anchors. Each case runs Check alone, since a decode of the
	// document would pay that cost.
	t.Run("decoded aliases", func(t *testing.T) {
		t.Parallel()

		// The alias key *k spells a, so it replaces the member that holds
		// the levels, and the decoded value drops them.
		lists := "k: &k a\n" + yamltest.AliasLevels(7) + "*k : small\n"

		// The mapping under b spells the last level out as a key.
		keyed := lists + stringtest.LinesLF(
			"b:",
			"  ? *l7",
			"  : v",
		)

		// The sequence k lists 500 aliases to a scalar of 2000 bytes.
		scalarAliases := stringtest.LinesLF(
			"a: &a "+strings.Repeat("x", 2000),
			"k: &k "+yamltest.FlowSequence("*a", 500),
		)

		v := compileSchema(t, `{"maxProperties": 5}`)

		tcs := map[string]struct {
			path      paths.Path
			input     string
			excessive bool
		}{
			"alias bomb as mapping key": {
				input:     keyed,
				excessive: true,
			},
			"alias bomb as flow mapping key": {
				input:     lists + "b: {*l7 : v}\n",
				excessive: true,
			},
			"alias bomb under a string tag": {
				input:     lists + "b: !!str *l7\n",
				excessive: true,
			},
			"merge key bomb": {
				input:     yamltest.MergeLevels(7),
				excessive: true,
			},
			"node holding an alias with a bomb outside it": {
				// A decode of a node that holds an alias reads the whole
				// document to find the anchor.
				path:      paths.Current().Child("c"),
				input:     keyed + "c: [*k]\n",
				excessive: true,
			},
			"alias to a small sequence as mapping key": {input: "s: &s [a, b]\n*s : v\n"},
			// The key holds a copy of the long scalar for each alias.
			"scalar aliases written out in a key": {
				input:     scalarAliases + "m: {? *k : v}\n",
				excessive: true,
			},
			"scalar aliases in a value": {input: scalarAliases + "m: {n: *k}\n"},
			// The decoder reads an alias inside the content of its own
			// anchor as null, so each such alias reads one node.
			"aliases inside their own anchor": {
				input: "x: &x [" + strings.Repeat("a, ", 10) + strings.Repeat("*x, ", 299) + "*x]\n",
			},
			"aliases to the anchor of a mapping inside it": {
				input: stringtest.LinesLF(
					"tree: &t",
					"  name: root",
					"  children:",
				) + strings.Repeat("    - {name: c, parent: *t}\n", 120),
			},
			"aliases to an enclosing anchor inside a nested one": {
				input: "a: &A {inner: &B " + yamltest.FlowSequence("*A", 300) + "}\n",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.AtOrSelf(t, yamltest.FirstDocument(t, tc.input), tc.path)

				err := doc.Validate(t.Context(), v)
				if !tc.excessive {
					require.NoError(t, err)

					return
				}

				requireExcessiveAliasing(t, err)
			})
		}

		// The document keeps its count for every Node of it, so each
		// Node that holds an alias gets the verdict of the whole document,
		// even when several check at once, and a Node without an alias
		// decodes on its own.
		t.Run("nodes of one document", func(t *testing.T) {
			t.Parallel()

			bomb := yamltest.FirstDocument(t, keyed+stringtest.LinesLF(
				"c: [*k]",
				"d: [*k]",
				"e: [x]",
			))

			// The 200 aliases in list repeat a sequence of 1000 numbers, so
			// they make up more of list than the limit allows. The 2000
			// numbers in filler keep their share of the document within it.
			diluted := yamltest.FirstDocument(t, stringtest.LinesLF(
				"filler: "+yamltest.FlowSequence("0", 2000),
				"big: &big "+yamltest.FlowSequence("0", 1000),
				"list: "+yamltest.FlowSequence("*big", 200),
			))

			// The base64 text of 16 KB of bytes, which takes 22 KB.
			text := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xab}, 16<<10))

			// The 1000 aliases in list each repeat the base64 text, and
			// other holds one more.
			binary := yamltest.FirstDocument(t, stringtest.LinesLF(
				"bin: &bin !!binary "+text,
				"list: "+yamltest.FlowSequence("*bin", 1000),
				"other: [*bin]",
			))

			// The aliases repeat the same text through the tagged alias
			// under s.
			chained := yamltest.FirstDocument(t, stringtest.LinesLF(
				"bin: &bin !!binary "+text,
				"s: &s !foo *bin",
				"list: "+yamltest.FlowSequence("*s", 1000),
				"other: [*s]",
			))

			// The aliases repeat the same text, which lies under another
			// tag and anchor of bin.
			wrapped := yamltest.FirstDocument(t, stringtest.LinesLF(
				"bin: &bin !foo &raw !!binary "+text,
				"list: "+yamltest.FlowSequence("*bin", 1000),
				"other: [*bin]",
			))

			tcs := map[string]struct {
				doc       *niceyaml.Node
				path      paths.Path
				excessive bool
			}{
				"first node holding an alias": {
					doc:       bomb,
					path:      paths.Current().Child("c"),
					excessive: true,
				},
				"second node holding an alias": {
					doc:       bomb,
					path:      paths.Current().Child("d"),
					excessive: true,
				},
				"node without an alias": {
					doc:  bomb,
					path: paths.Current().Child("e"),
				},
				"document diluting its aliases": {doc: diluted},
				"node of a document diluting its aliases": {
					doc:  diluted,
					path: paths.Current().Child("list"),
				},
				"node with one alias to a binary aliased many times": {
					doc:       binary,
					path:      paths.Current().Child("other"),
					excessive: true,
				},
				"document with aliases to a tag over an alias to a binary": {
					doc:       chained,
					excessive: true,
				},
				"node of aliases to a tag over an alias to a binary": {
					doc:       chained,
					path:      paths.Current().Child("list"),
					excessive: true,
				},
				"node with one alias to a tag over an alias to a binary": {
					doc:       chained,
					path:      paths.Current().Child("other"),
					excessive: true,
				},
				"document with aliases to a binary under another tag": {
					doc:       wrapped,
					excessive: true,
				},
				"node with one alias to a binary under another tag": {
					doc:       wrapped,
					path:      paths.Current().Child("other"),
					excessive: true,
				},
			}

			for name, tc := range tcs {
				t.Run(name, func(t *testing.T) {
					t.Parallel()

					node := yamltest.AtOrSelf(t, tc.doc, tc.path)

					for range 2 {
						err := node.Validate(t.Context(), v)
						if !tc.excessive {
							require.NoError(t, err)

							continue
						}

						requireExcessiveAliasing(t, err)
					}
				})
			}
		})
	})

	// A source with the alias limit off decodes and validates a document
	// the limit refuses. The schema then reads every use of every alias,
	// so each case stays small.
	t.Run("sources with the limit off", func(t *testing.T) {
		t.Parallel()

		v := compileSchema(t, `{"type": ["object", "array"]}`)

		tcs := map[string]struct {
			path  paths.Path
			input string
		}{
			"alias bomb": {input: yamltest.AliasLevels(4)},
			"node of an alias bomb": {
				path:  paths.Current().Child("a").Index(4),
				input: yamltest.AliasLevels(4),
			},
			"merge key bomb": {input: yamltest.MergeLevels(4)},
			"alias bomb as mapping key": {
				input: yamltest.AliasLevels(4) + stringtest.LinesLF(
					"b:",
					"  ? *l4",
					"  : v",
				),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.AtOrSelf(t, yamltest.FirstDocument(t, tc.input), tc.path)

				requireExcessiveAliasing(t, doc.Validate(t.Context(), v))

				trusted := yamltest.FirstDocument(t, tc.input, niceyaml.WithAliasLimit(false))
				trusted = yamltest.AtOrSelf(t, trusted, tc.path)

				require.NoError(t, trusted.Validate(t.Context(), v))

				// The schema passes as a validator of a decode too, and
				// the decode then reads the node.
				data, err := trusted.Decode[any](t.Context(), niceyaml.WithValidator(v))
				require.NoError(t, err)
				assert.NotEmpty(t, data)
			})
		}
	})

	// The count of a document cannot see the anchors of a reference
	// document, so it takes each alias to one as one node. Check then
	// applies the limit to the value the decode shares between them.
	t.Run("reference aliases", func(t *testing.T) {
		t.Parallel()

		// The anchored mapping of the reference document holds 1000 entries.
		refs := niceyaml.WithReferences(niceyaml.NewSourceFromString(
			"defaults: &defaults\n" + yamltest.GenerateLines(1000, "  k%d: v"),
		))
		v := compileSchema(t, `{"maxProperties": 5}`)

		// The refusal carries no location, so each case names where the
		// node that binds it puts it.
		tcs := map[string]struct {
			path  paths.Path
			input string
			// The message of the refusal, or empty for a document
			// within the limit.
			want string
		}{
			"aliases to a mapping of a reference document": {
				input: "items: " + yamltest.FlowSequence("*defaults", 300) + "\n",
				want:  "app.yaml: excessive aliasing",
			},
			"node of aliases to a mapping of a reference document": {
				path:  paths.Current().Child("items"),
				input: "items: " + yamltest.FlowSequence("*defaults", 300) + "\n",
				want:  "app.yaml:1:1: $.items: excessive aliasing",
			},
			"aliases to an anchor on a tagged alias to a reference document": {
				input: stringtest.LinesLF(
					"local: &local !foo *defaults",
					"items: "+yamltest.FlowSequence("*local", 300),
				),
				want: "app.yaml: excessive aliasing",
			},
			"a few aliases to a mapping of a reference document": {
				input: "items: " + yamltest.FlowSequence("*defaults", 2) + "\n",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, tc.input, refs, niceyaml.WithName("app.yaml"))
				doc = yamltest.AtOrSelf(t, doc, tc.path)

				_, err := doc.Decode[any](t.Context(), niceyaml.WithValidator(v))
				if tc.want == "" {
					require.NoError(t, err)

					return
				}

				require.EqualError(t, err, tc.want)
				requireExcessiveAliasing(t, err)

				// With the alias limit off, the schema reads every use of
				// the mapping and the document conforms.
				trusted := yamltest.FirstDocument(t, tc.input, refs, niceyaml.WithAliasLimit(false))
				trusted = yamltest.AtOrSelf(t, trusted, tc.path)

				_, err = trusted.Decode[any](t.Context(), niceyaml.WithValidator(v))
				require.NoError(t, err)
			})
		}
	})

	t.Run("values", func(t *testing.T) {
		t.Parallel()

		v := compileSchema(t, `{
			"properties": {
				"a": {"type": "string"},
				"b": {"type": "string"}
			}
		}`)

		selfMap := map[string]any{}
		selfMap["self"] = selfMap

		selfSlice := make([]any, 1)
		selfSlice[0] = selfSlice

		outerMap := map[string]any{}
		outerMap["list"] = []any{outerMap}

		bin := []byte("hi")

		// Each level lists the level below ten times, so the last level
		// expands to 10^6 scalars.
		var shared any = []any{"x"}

		for range 6 {
			level := make([]any, 10)
			for i := range level {
				level[i] = shared
			}

			shared = level
		}

		tcs := map[string]struct {
			data      any
			err       error
			excessive bool
		}{
			"map containing itself": {
				data: selfMap,
				err:  schema.ErrValidate,
			},
			"slice containing itself": {
				data: selfSlice,
				err:  schema.ErrValidate,
			},
			"map containing itself through a slice": {
				data: outerMap,
				err:  schema.ErrValidate,
			},
			"bytes under two keys": {data: map[string]any{"a": bin, "b": bin}},
			// A key that is no string prints in full as the member name,
			// so it counts as a value does.
			"ordered mapping key containing itself": {
				data: yaml.MapSlice{{Key: selfSlice, Value: 1}},
				err:  schema.ErrValidate,
			},
			"ordered mapping key sharing a slice": {
				data:      yaml.MapSlice{{Key: shared, Value: 1}},
				excessive: true,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := v.ValidateValue(t.Context(), tc.data)

				switch {
				case tc.excessive:
					requireExcessiveAliasing(t, err)

				case tc.err != nil:
					// A value that contains itself is no fault of a
					// document, since no document decodes to one.
					require.ErrorIs(t, err, tc.err)
					require.NotErrorIs(t, err, schema.ErrExcessiveAliasing)
					assert.False(t, niceyaml.IsInvalid(err))

				default:
					require.NoError(t, err)
				}
			})
		}
	})
}

func TestSchema_BooleanSchema(t *testing.T) {
	t.Parallel()

	// Boolean schemas are valid in JSON Schema. A true schema accepts
	// everything, and a false schema rejects everything.
	tcs := map[string]struct {
		input          string
		wantAcceptsAll bool
	}{
		"true schema accepts all data": {
			input:          `true`,
			wantAcceptsAll: true,
		},
		"false schema rejects all data": {input: `false`},
	}

	testData := []any{"anything", 42, map[string]any{"key": "value"}}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			v := compileSchema(t, tc.input)

			for _, data := range testData {
				err := v.ValidateValue(t.Context(), data)
				if tc.wantAcceptsAll {
					assert.NoError(t, err)
				} else {
					assert.Error(t, err)
				}
			}
		})
	}
}

func TestSchema_SubErrorAnnotations(t *testing.T) {
	t.Parallel()

	// A single violation is the main error itself, and several violations
	// render as annotations with their own paths.
	tcs := map[string]struct {
		schema           string
		input            string
		wantAnnotations  []string // Substrings that should appear in annotation output.
		wantNestedErrors int
	}{
		"single violation has no nested errors": {
			schema: `{
				"type": "object",
				"properties": {
					"name": {"type": "string"}
				}
			}`,
			input:           "name: 123",
			wantAnnotations: []string{`expected "string", got "integer"`},
		},
		"multiple sub-errors from required fields": {
			schema: `{
				"type": "object",
				"properties": {
					"first": {"type": "string"},
					"second": {"type": "string"}
				},
				"required": ["first", "second"]
			}`,
			input:            "other: value",
			wantAnnotations:  []string{"missing required property", "first", "second"},
			wantNestedErrors: 2,
		},
		"nested object sub-error": {
			schema: `{
				"type": "object",
				"properties": {
					"user": {
						"type": "object",
						"properties": {
							"age": {"type": "integer"}
						}
					}
				}
			}`,
			input: stringtest.Input(`
				user:
				  age: notanumber
			`),
			wantAnnotations: []string{`expected "integer", got "string"`},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			v := compileSchema(t, tc.schema)

			err := yamltest.FirstDocument(t, tc.input).Validate(t.Context(), v)

			errOutput := fmt.Sprintf("%+v", yamltest.RequireSourceError(t, err))

			for _, annotation := range tc.wantAnnotations {
				assert.Contains(t, errOutput, annotation)
			}

			var validationErr *niceyaml.Error

			require.ErrorAs(t, err, &validationErr)

			// Unwrap includes the main error plus its nested errors.
			assert.Len(t, validationErr.Unwrap(), tc.wantNestedErrors+1)
		})
	}
}

func TestSchema_PathAnchors(t *testing.T) {
	t.Parallel()

	whole := compileSchema(t, `{"properties": {"server": {"properties": {"port": {"minimum": 1}}}}}`)
	server := compileSchema(t, `{"properties": {"port": {"minimum": 1}}}`)

	doc := yamltest.FirstDocument(t, stringtest.LinesLF("server:", "  port: 0"))
	serverNode := yamltest.At(t, doc, paths.Doc().Child("server"))

	// A binding reports the `$` path from the root of the document.
	// ValidateValue binds to a source that holds no document, so its `$`
	// is the value. Each case holds the result of one validation.
	tcs := map[string]struct {
		result   error
		want     string
		wantPath string
	}{
		"value of a document": {
			result:   whole.ValidateValue(t.Context(), map[string]any{"server": map[string]any{"port": 0}}),
			want:     "$.server.port: 0 is less than 1",
			wantPath: "$.server.port",
		},
		"value": {
			result:   server.ValidateValue(t.Context(), map[string]any{"port": 0}),
			want:     "$.port: 0 is less than 1",
			wantPath: "$.port",
		},
		"document": {
			result:   doc.Validate(t.Context(), whole),
			want:     "2:9: $.server.port: 0 is less than 1",
			wantPath: "$.server.port",
		},
		"scoped Node": {
			result:   serverNode.Validate(t.Context(), server),
			want:     "2:9: $.server.port: 0 is less than 1",
			wantPath: "$.server.port",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := tc.result
			require.Error(t, err)

			assert.Equal(
				t,
				tc.want,
				strings.SplitN(niceyaml.FormatError(err, niceyaml.WithContextLines(0)), "\n", 2)[0],
			)

			path, ok := niceyaml.NewErrorTree(err).Path()
			require.True(t, ok)
			assert.Equal(t, tc.wantPath, path.String())
		})
	}
}

func TestSchema_ErrorPaths(t *testing.T) {
	t.Parallel()

	// A single violation puts its path on the main error. Several violations
	// leave the main error without a path and expose one path per problem
	// below it. ValidateValue writes each path from `$`, the value.
	tcs := map[string]struct {
		schema          string
		input           any
		wantPath        string   // Expected path on the main error.
		wantNestedPaths []string // Expected paths from nested errors.
	}{
		"type error has path on main error": {
			schema:   `{"type": "object", "properties": {"name": {"type": "string"}}}`,
			input:    map[string]any{"name": 123},
			wantPath: "$.name",
		},
		"additional property has key path on main error": {
			schema:   `{"type": "object", "properties": {"name": {"type": "string"}}, "additionalProperties": false}`,
			input:    map[string]any{"name": "valid", "extra": "invalid"},
			wantPath: "$.extra~",
		},
		"nested validation error has path on main error": {
			schema:   `{"type": "object", "properties": {"user": {"type": "object", "properties": {"age": {"type": "integer"}}}}}`,
			input:    map[string]any{"user": map[string]any{"age": "notanumber"}},
			wantPath: "$.user.age",
		},
		"several violations have paths on nested errors": {
			schema:          `{"type": "object", "properties": {"name": {"type": "string"}, "age": {"type": "number"}}}`,
			input:           map[string]any{"name": 123, "age": "thirty"},
			wantNestedPaths: []string{"$.name", "$.age"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			v := compileSchema(t, tc.schema)

			err := v.ValidateValue(t.Context(), tc.input)
			require.Error(t, err)

			tree := niceyaml.NewErrorTree(err)

			gotPath, ok := tree.Path()
			assert.Equal(t, tc.wantPath != "", ok)

			if ok {
				assert.Equal(t, tc.wantPath, gotPath.String())
			}

			var gotNestedPaths []string

			for _, child := range tree.Children {
				if nestedPath, ok := child.Path(); ok {
					gotNestedPaths = append(gotNestedPaths, nestedPath.String())
				}
			}

			assert.ElementsMatch(t, tc.wantNestedPaths, gotNestedPaths)
		})
	}
}

func TestSchema_Check_Scope(t *testing.T) {
	t.Parallel()

	v := compileSchema(t, `{
		"type": "object",
		"properties": {
			"replicas": {"type": "integer"},
			"16": {"type": "integer"}
		},
		"additionalProperties": false
	}`)

	aliased := compileSchema(t, `{
		"type": "object",
		"properties": {
			"c": {"additionalProperties": {"type": "string"}}
		}
	}`)

	spec := paths.Current().Child("spec")

	// Each case validates the node at path, and the violation resolves
	// from that node.
	tcs := map[string]struct {
		schema *schema.Schema
		path   paths.Path
		input  string
		want   string
		// Where the range of the error starts, counted from 0.
		wantStart position.Position
	}{
		"violation": {
			schema: v,
			path:   spec,
			input: stringtest.Input(`
				replicas: 1
				spec:
				  replicas: many
			`),
			want:      `3:13: $.spec.replicas: expected "integer", got "string"`,
			wantStart: position.New(2, 12),
		},
		// The key decodes to the member name 16, which the source spells
		// 0x10, so the path names the member by its source spelling.
		"decoded key": {
			schema: v,
			path:   spec,
			input: stringtest.Input(`
				0x10: 1
				spec:
				  0x10: hello
			`),
			want:      `3:9: $.spec.0x10: expected "integer", got "string"`,
			wantStart: position.New(2, 8),
		},
		// The alias inside the node refers to an anchor outside it, and
		// the path still names the member by its source spelling.
		"decoded key behind an alias": {
			schema: aliased,
			path:   paths.Current().Child("b"),
			input: stringtest.Input(`
				a: &a {0x10: 1}
				b:
				  c: *a
			`),
			want:      `1:14: $.b.c.0x10: expected "string", got "integer"`,
			wantStart: position.New(0, 13),
		},
		"additional property": {
			schema: v,
			path:   spec,
			input: stringtest.Input(`
				extra: 1
				spec:
				  extra: 1
			`),
			want:      `3:3: $.spec.extra~: value is not allowed`,
			wantStart: position.New(2, 2),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dd := yamltest.FirstDocument(t, tc.input)

			err := yamltest.At(t, dd, tc.path).Validate(t.Context(), tc.schema)

			bound := yamltest.RequireSourceError(t, err)

			rng, ok := bound.Range()
			require.True(t, ok)
			assert.Equal(t, tc.wantStart, rng.Start)
			assert.Equal(t, tc.want, bound.Error())
		})
	}

	t.Run("the whole document conforms where the node does not", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, stringtest.Input(`
			replicas: 1
			spec:
			  replicas: many
		`))

		require.Error(t, yamltest.At(t, dd, spec).Validate(t.Context(), v))
		require.NoError(t, dd.Validate(t.Context(), compileSchema(t, `{
			"type": "object",
			"properties": {"replicas": {"type": "integer"}}
		}`)))
	})
}

func TestSchema_Check_SyntaxError(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		schema string
		// The error of the document that parsed, empty when it passes.
		err string
	}{
		"schema that would reject the data": {
			schema: `{"type": "string"}`,
			err:    `1:1: $: expected "string", got "object"`,
		},
		"schema that accepts all data": {schema: `true`},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			v := compileSchema(t, tc.schema)

			docs := niceyaml.NewSourceFromString(stringtest.Input(`
				a: 1
				---
				b: [
			`)).AllDocuments()
			require.Len(t, docs, 2)
			require.ErrorIs(t, docs[1].Err(), niceyaml.ErrSyntax)

			// The second document did not parse, so the schema has no data
			// to check and returns the syntax error.
			assert.Same(t, docs[1].Err(), v.Check(t.Context(), docs[1]))
			assert.Same(t, docs[1].Err(), docs[1].Validate(t.Context(), v))

			// The first document parsed, so the schema checks it.
			err := docs[0].Validate(t.Context(), v)
			if tc.err == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.err)
			}
		})
	}
}

func TestSchema_Check_Bound(t *testing.T) {
	t.Parallel()

	v := compileSchema(t, `{
		"type": "object",
		"properties": {
			"name": {"type": "string"},
			"price": {"type": "number", "minimum": 0}
		}
	}`)

	menu := stringtest.Input(`
		price: 5
		items:
		  - price: 1
		  - price: -2
	`)
	itemPath := paths.Current().Child("items").Index(1)

	tcs := map[string]struct {
		input string
		path  paths.Path
		// The message of the error.
		want string
		// The error is the refusal of the alias limit.
		excessive bool
	}{
		"a document that conforms": {input: menu},
		"a violation at the root": {
			input: "price: -1\n",
			want:  "menu.yaml:1:8: $.price: -1 is less than 0",
		},
		"a violation in a scoped node": {
			input: menu,
			path:  itemPath,
			want:  "menu.yaml:4:12: $.items[1].price: -2 is less than 0",
		},
		"several violations": {
			input: "name: 1\nprice: -1\n",
			want: stringtest.JoinLF(
				"menu.yaml: 2 schema violations",
				`menu.yaml:1:7: $.name: expected "string", got "integer"`,
				"menu.yaml:2:8: $.price: -1 is less than 0",
			),
		},
		// The summary stands above violations that say where they point,
		// so the scoped node gives it no location.
		"several violations in a scoped node": {
			input: stringtest.Input(`
				items:
				  - name: 1
				    price: -2
			`),
			path: paths.Current().Child("items").Index(0),
			want: stringtest.JoinLF(
				"menu.yaml: 2 schema violations",
				`menu.yaml:2:11: $.items[0].name: expected "string", got "integer"`,
				"menu.yaml:3:12: $.items[0].price: -2 is less than 0",
			),
		},
		"a decoding error": {
			input: "price: *nope\n",
			want:  `menu.yaml:1:8: $.price: could not find alias "nope"`,
		},
		// The decode the schema runs refuses the document, and binds the
		// error at the first token of the node.
		"a document past the alias limit": {
			input:     yamltest.AliasLevels(7),
			want:      "menu.yaml:1:1: excessive aliasing",
			excessive: true,
		},
		// The error carries a position already, so the scoped node adds
		// no path to it.
		"a scoped node past the alias limit": {
			input:     yamltest.AliasLevels(7),
			path:      paths.Current().Child("a"),
			want:      "menu.yaml:2:3: excessive aliasing",
			excessive: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			node := yamltest.AtOrSelf(t, yamltest.FirstDocumentWithPath(t, tc.input, "menu.yaml"), tc.path)

			err := node.Validate(t.Context(), v)
			if tc.want == "" {
				require.NoError(t, err)

				return
			}

			require.EqualError(t, err, tc.want)

			if tc.excessive {
				requireExcessiveAliasing(t, err)
			}

			bound := yamltest.RequireSourceError(t, err)
			assert.Same(t, node, bound.Node())
		})
	}

	t.Run("a direct call returns a violation as the schema wrote it", func(t *testing.T) {
		t.Parallel()

		item := yamltest.At(t, yamltest.FirstDocumentWithPath(t, menu, "menu.yaml"), itemPath)

		err := v.Check(t.Context(), item)
		require.EqualError(t, err, "-2 is less than 0")

		var bound *niceyaml.SourceError

		require.NotErrorAs(t, err, &bound)

		// The path reads from the node the schema got, and the node
		// resolves it when it binds the error.
		var violation *niceyaml.Error

		require.ErrorAs(t, err, &violation)

		path, ok := violation.Path()
		require.True(t, ok)
		assert.Equal(t, "@.price", path.String())

		require.EqualError(t, item.Validate(t.Context(), v), "menu.yaml:4:12: $.items[1].price: -2 is less than 0")
	})

	t.Run("a validator that runs the schema on each element reports the element", func(t *testing.T) {
		t.Parallel()

		each := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
			items, err := n.Nodes(paths.Current().Child("items").IndexAll())
			if err != nil {
				return err //nolint:wrapcheck // The test inspects the error as it is.
			}

			for _, item := range items {
				err := item.Validate(ctx, v)
				if err != nil {
					return err //nolint:wrapcheck // The test inspects the error as it is.
				}
			}

			return nil
		})

		doc := yamltest.FirstDocumentWithPath(t, menu, "menu.yaml")

		err := doc.Validate(t.Context(), each)
		require.EqualError(t, err, "menu.yaml:4:12: $.items[1].price: -2 is less than 0")

		assert.Equal(t, "minimum", requireViolation(t, err).Keyword)
	})

	t.Run("a validator that checks another document names that document", func(t *testing.T) {
		t.Parallel()

		other := yamltest.FirstDocumentWithPath(t, "# other\nprice: -1\n", "other.yaml")
		include := niceyaml.ValidatorFunc(func(ctx context.Context, _ *niceyaml.Node) error {
			return other.Validate(ctx, v)
		})

		err := yamltest.FirstDocumentWithPath(t, menu, "menu.yaml").Validate(t.Context(), include)
		require.EqualError(t, err, "other.yaml:2:8: $.price: -1 is less than 0")
	})
}

func TestSchema_SourcePath(t *testing.T) {
	t.Parallel()

	// A violation's path spells each key as the source does, so a key the
	// decoder respells, such as 0x10 for the member name 16, still names
	// the member. The path prints in the error and resolves with Node.At.
	// Where the walk cannot tell which key in the source names the member,
	// the path keeps the decoded name, which Node.At does not resolve. The
	// error binds at the node of the member where the walk reached it, and
	// at the key of the mapping that holds the member where the walk did
	// not. Such a case sets unresolved.
	//
	// Several cases share a schema, which takes strings for the members of
	// the document, or integers for the members of the mapping under m or
	// under user. Others take an integer for the member 16, a string or a
	// mapping for each member, or strings for the members of the mapping
	// under b.
	const (
		integer16 = `{
			"type": "object",
			"properties": {"16": {"type": "integer"}}
		}`
		stringOrObjectMembers = `{
			"type": "object",
			"additionalProperties": {"type": ["string", "object"]}
		}`
		stringsInB = `{
			"type": "object",
			"properties": {
				"b": {"additionalProperties": {"type": "string"}}
			}
		}`
	)

	tcs := map[string]struct {
		schema     string
		input      string
		want       string
		unresolved bool
	}{
		"hexadecimal key": {
			schema: integer16,
			input:  "0x10: hello\n",
			want:   `1:7: $.0x10: expected "integer", got "string"`,
		},
		"tilde null key": {
			schema: stringMembers,
			input:  "~: 5\n",
			want:   `1:4: $.'~': expected "string", got "integer"`,
		},
		"merge key ahead of a null key": {
			schema: stringOrObjectMembers,
			input:  "<<: {a: x}\n~: 5\n",
			want:   `2:4: $.'~': expected ["string", "object"], got "integer"`,
		},
		"merge key ahead of an empty key": {
			schema: stringOrObjectMembers,
			input: stringtest.Input(`
				<<: {a: x}
				"": 5
			`),
			want: `2:5: $.'': expected ["string", "object"], got "integer"`,
		},
		"merge key overrides an earlier respelled key": {
			// The decoder applies the merge after 0x10: 1, so the member
			// 16 holds x, and the path names the key the merge brings in.
			schema: integersInUser,
			input: stringtest.Input(`
				user:
				  0x10: 1
				  <<: {16: x}
			`),
			want: `3:12: $.user.16: expected "integer", got "string"`,
		},
		"merge key overrides an earlier key of the same spelling": {
			// A path through 0x10 selects the later entry, which the
			// merge brings in.
			schema: integersInUser,
			input: stringtest.Input(`
				user:
				  0x10: 1
				  <<: {0x10: x}
			`),
			want: `3:14: $.user.0x10: expected "integer", got "string"`,
		},
		"respelled key a merge key brings in": {
			schema: integersInUser,
			input: stringtest.LinesLF(
				"user:",
				"  <<: {0x10: x}",
			),
			want: `2:14: $.user.0x10: expected "integer", got "string"`,
		},
		"later key shadows a merged member": {
			// The decoder sets 0x10: x after the merge, so the member 16
			// holds x rather than the 5 the merge brings in.
			schema: integersInM,
			input:  "b: &b {16: 5}\nm: {<<: *b, 0x10: x}\n",
			want:   `2:19: $.m.0x10: expected "integer", got "string"`,
		},
		"merged member hidden by a same-spelled key": {
			// The merge brings in 0x10: x as the member 16. The quoted key
			// "0x10" of m names a separate member with the same spelling,
			// and a path selector matches that key, so the path keeps the
			// name 16 and the error binds at the x.
			schema:     integersInM,
			input:      "m: {<<: {0x10: x}, \"0x10\": 5}\n",
			want:       `1:16: $.m.16: expected "integer", got "string"`,
			unresolved: true,
		},
		"tagged alias key replaces an earlier key": {
			// The tag may change the name the alias key decodes to, so the
			// key 0x10 before it cannot name the member 16.
			schema: integersInM,
			input:  "a: &k 16\nm: {0x10: 5, ? !!str *k : x}\n",
			want:   `2:27: $.m.16: expected "integer", got "string"`,
		},
		"merge source with a tagged alias key": {
			// A key of the merge source may decode to any name, so the
			// key 0x10 before the merge cannot name the member 16.
			schema: integersInM,
			input: stringtest.Input(`
				a: &k 16
				b: &b {? !!str *k : x}
				m: {0x10: 5, <<: *b}
			`),
			want: `2:21: $.m.16: expected "integer", got "string"`,
		},
		"unnameable alias key hides earlier keys": {
			// The alias key refers to a sequence, which the walk cannot
			// name, so the key 0x10 before it cannot name the member 16.
			// The walk finds no member there, so the error binds at m.
			schema:     integersInM,
			input:      "s: &s [a]\nm: {0x10: x, ? *s : 1}\n",
			want:       `2:1: $.m.16: expected "integer", got "string"`,
			unresolved: true,
		},
		"spelled-out null key": {
			schema: stringMembers,
			input:  "NULL: 5\n",
			want:   `1:7: $.NULL: expected "string", got "integer"`,
		},
		"boolean key targeted by the failure": {
			schema: closedName,
			input:  "True: nope\n",
			want:   "1:1: $.True~: value is not allowed",
		},
		"bool-tagged key": {
			schema: stringMembers,
			input:  "!!bool yes: 5\n",
			want:   `1:13: $.yes: expected "string", got "integer"`,
		},
		"null-tagged key": {
			schema: stringMembers,
			input:  "!!null x: 5\n",
			want:   `1:11: $.x: expected "string", got "integer"`,
		},
		"null-tagged empty key": {
			// The key decodes to null, and the path spells it as the
			// empty key the source writes.
			schema: stringMembers,
			input:  "!!null \"\": 5\n",
			want:   `1:12: $.'': expected "string", got "integer"`,
		},
		"int-tagged quoted key": {
			schema: stringMembers,
			input:  "!!int \"0x10\": 5\n",
			want:   `1:15: $.0x10: expected "string", got "integer"`,
		},
		"timestamp-tagged key": {
			schema: stringMembers,
			input:  "? !!timestamp 2001-01-01\n: 5\n",
			want:   `2:3: $.2001-01-01: expected "string", got "integer"`,
		},
		"float key": {
			schema: stringMembers,
			input:  "1.5: 5\n",
			want:   `1:6: $.'1.5': expected "string", got "integer"`,
		},
		"two keys decoding to one name": {
			// Both keys decode to 1, and the decoder keeps the later
			// member, so the path names the later key.
			schema: stringMembers,
			input:  "1.0: x\n1: 2\n",
			want:   `2:4: $.1: expected "string", got "integer"`,
		},
		"key with a dot": {
			schema: stringMembers,
			input:  "a.b: 5\n",
			want:   `1:6: $.'a.b': expected "string", got "integer"`,
		},
		"key with a space": {
			schema: stringMembers,
			input:  "a b: 5\n",
			want:   `1:6: $.'a b': expected "string", got "integer"`,
		},
		"nested respelled key": {
			schema: `{
				"type": "object",
				"properties": {
					"spec": {
						"type": "object",
						"properties": {"16": {"type": "integer"}}
					}
				}
			}`,
			input: stringtest.LinesLF(
				"spec:",
				"  0x10: hello",
			),
			want: `2:9: $.spec.0x10: expected "integer", got "string"`,
		},
		"respelled key inside a sequence": {
			schema: `{
				"type": "object",
				"properties": {
					"items": {
						"type": "array",
						"items": {
							"type": "object",
							"properties": {"16": {"type": "integer"}}
						}
					}
				}
			}`,
			input: stringtest.Input(`
				items:
				  - 0x10: 1
				  - 0x10: hello
			`),
			want: `3:11: $.items[1].0x10: expected "integer", got "string"`,
		},
		"alias key": {
			schema: stringMembers,
			input:  "a: &k name\n*k : 5\n",
			want:   `2:6: $.name: expected "string", got "integer"`,
		},
		"key named like an alias": {
			// The alias *k decodes to the key name, so the member k is
			// the entry k: 1.
			schema: `{
				"type": "object",
				"properties": {"k": {"type": "string"}}
			}`,
			input: stringtest.Input(`
				a: &k name
				k: 1
				*k : v
			`),
			want: `2:4: $.k: expected "string", got "integer"`,
		},
		"alias key overrides an earlier key": {
			// The decoder keeps the member the alias key sets, so the
			// path spells the keys below it.
			schema: `{
				"type": "object",
				"properties": {
					"name": {"additionalProperties": {"type": "integer"}}
				}
			}`,
			input: stringtest.Input(`
				a: &k name
				name: {16: x}
				*k : {0x10: y}
			`),
			want: `3:13: $.name.0x10: expected "integer", got "string"`,
		},
		"alias key overrides a respelled key": {
			schema: integer16,
			input: stringtest.Input(`
				a: &k 16
				0x10: hello
				*k : [1]
			`),
			want: `3:1: $.16: expected "integer", got "array"`,
		},
		"block scalar key": {
			schema: stringMembers,
			input: stringtest.Input(`
				? |-
				  n
				: 5
			`),
			want: `3:3: $.n: expected "string", got "integer"`,
		},
		"respelled key behind an alias": {
			schema: stringsInB,
			input:  "a: &a {0x10: 1}\nb: *a\n",
			want:   `1:14: $.b.0x10: expected "string", got "integer"`,
		},
		"respelled key behind an alias to a block mapping": {
			schema: stringsInB,
			input: stringtest.Input(`
				a: &a
				  0x10: 1
				b: *a
			`),
			want: `2:9: $.b.0x10: expected "string", got "integer"`,
		},
		"respelled key behind a redefined anchor": {
			// The alias refers to the last anchor of its name before it.
			schema: `{
				"type": "object",
				"properties": {
					"c": {"additionalProperties": {"type": "string"}}
				}
			}`,
			input: stringtest.Input(`
				a: &a {16: x}
				b: &a {0x10: 1}
				c: *a
			`),
			want: `2:14: $.c.0x10: expected "string", got "integer"`,
		},
		"respelled key behind an anchor a merge brings in again": {
			// The decoder reads the anchors of a merged mapping again at
			// the merge key, so *x refers to the &x inside base.
			schema: `{
				"type": "object",
				"properties": {
					"m": {
						"properties": {
							"v": {"additionalProperties": {"type": "string"}}
						}
					}
				}
			}`,
			input: stringtest.Input(`
				base: &b
				  k: &x {0x10: 1}
				other: &x {16: x}
				m:
				  <<: *b
				  v: *x
			`),
			want: `2:16: $.m.v.0x10: expected "string", got "integer"`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			v := compileSchema(t, tc.schema)
			dd := yamltest.FirstDocument(t, tc.input)

			err := dd.Validate(t.Context(), v)

			bound := yamltest.RequireSourceError(t, err)

			gotPath, ok := bound.Path()
			require.True(t, ok, "bound error carries no path")
			assert.Equal(t, tc.want, bound.Error())
			assert.Contains(t, tc.want, ": "+gotPath.String()+": ", "the message spells another path")

			_, err = dd.At(gotPath)
			if tc.unresolved {
				require.Error(t, err, "path from the error resolves")

				return
			}

			require.NoError(t, err, "path from the error does not resolve")
		})
	}
}

func TestSchema_SourcePath_ReferenceAlias(t *testing.T) {
	t.Parallel()

	// An alias to an anchor of a reference document resolves in the decode
	// and not in the document, so the walk cannot follow it. The path keeps
	// the decoded name of a member below the alias. A merge key that names
	// such an alias may set a member of any name, so the key 0x10 before
	// it cannot name the member 16 either.
	refs := niceyaml.WithReferences(niceyaml.NewSourceFromString("base: &base {0x10: x}\nother: &other {k: 1}\n"))

	tcs := map[string]struct {
		schema string
		input  string
		want   string
	}{
		"member below the alias": {
			schema: `{
				"type": "object",
				"properties": {
					"a": {"additionalProperties": {"type": "integer"}}
				}
			}`,
			input: "a: *base\n",
			want:  "$.a.16",
		},
		"member beside a merge of the alias": {
			schema: `{
				"type": "object",
				"additionalProperties": {"type": "integer"}
			}`,
			input: "0x10: x\n<<: *other\n",
			want:  "$.16",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			v := compileSchema(t, tc.schema)
			doc := yamltest.FirstDocument(t, tc.input, refs)

			_, err := doc.Decode[any](t.Context(), niceyaml.WithValidator(v))

			bound := yamltest.RequireSourceError(t, err)
			assert.Equal(t, tc.want, requireUnresolved(t, doc, bound).String())
		})
	}
}

func TestSchema_SourcePath_HiddenKey(t *testing.T) {
	t.Parallel()

	// In each case the key 0x10 sets the member 16 to x. A later entry
	// spells its key 0x10 too, and a path through 0x10 selects that entry.
	// The path of the member keeps its decoded name and selects nothing,
	// and the error binds at the x rather than at the valid 1. The quoted
	// "0x10" decodes to the member 0x10, as does an alias key whose anchor
	// holds it.
	v := compileSchema(t, integersInUser)

	tcs := map[string]struct {
		input string
		// The position of the x, as the message prints it.
		want string
		opts []niceyaml.SourceOption
	}{
		"own key after the merge key": {
			input: stringtest.Input(`
				user:
				  <<: {0x10: x}
				  "0x10": 1
			`),
			want: "2:14",
		},
		"merge key after the own key": {
			input: stringtest.Input(`
				user:
				  0x10: x
				  <<: {"0x10": 1}
			`),
			want: "2:9",
		},
		"later source of one merge key": {
			input: stringtest.Input(`
				user:
				  <<: [{0x10: x}, {"0x10": 1}]
			`),
			want: "2:15",
		},
		"later aliased source of one merge key": {
			input: stringtest.Input(`
				a: &a {0x10: x}
				b: &b {"0x10": 1}
				user:
				  <<: [*a, *b]
			`),
			want: "1:14",
		},
		"later merge key": {
			input: stringtest.Input(`
				user:
				  <<: {0x10: x}
				  <<: {"0x10": 1}
			`),
			want: "2:14",
			opts: []niceyaml.SourceOption{niceyaml.WithAllowDuplicateKeys(true)},
		},
		"later own key": {
			input: stringtest.Input(`
				user:
				  0x10: x
				  "0x10": 1
			`),
			want: "2:9",
			opts: []niceyaml.SourceOption{niceyaml.WithAllowDuplicateKeys(true)},
		},
		"later alias key": {
			input: stringtest.Input(`
				k: &k "0x10"
				user:
				  0x10: x
				  *k : 1
			`),
			want: "3:9",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input, tc.opts...)

			err := doc.Validate(t.Context(), v)

			bound := yamltest.RequireSourceError(t, err)
			assert.Equal(t, tc.want+`: $.user.16: expected "integer", got "string"`, bound.Error())
			requireUnresolved(t, doc, bound)
		})
	}
}

func TestSchema_SourcePath_BelowHiddenKey(t *testing.T) {
	t.Parallel()

	// In each case a later entry hides the key of the member that holds x,
	// so the path writes the decoded name of that member. That name
	// selects another entry, which holds a valid value under the spelling
	// 0x11, so the keys below keep their decoded names too. The path
	// selects nothing, and the error binds at the x.
	v := compileSchema(t, `{
		"type": "object",
		"properties": {
			"user": {
				"additionalProperties": {
					"additionalProperties": {"type": "integer"}
				}
			}
		}
	}`)

	tcs := map[string]struct {
		input string
		want  string
	}{
		"own key after the merge key": {
			input: "user: {16: {0x11: 2}, <<: {0x10: {0x11: x}}, \"0x10\": 1}\n",
			want:  `1:41: $.user.16.17: expected "integer", got "string"`,
		},
		"block own key after the merge key": {
			input: stringtest.Input(`
				user:
				  16: {0x11: 2}
				  <<: {0x10: {0x11: x}}
				  "0x10": 1
			`),
			want: `3:21: $.user.16.17: expected "integer", got "string"`,
		},
		"later source of one merge key": {
			input: "user: {16: {0x11: 2}, <<: [{0x10: {0x11: x}}, {\"0x10\": 1}]}\n",
			want:  `1:42: $.user.16.17: expected "integer", got "string"`,
		},
		"merge key after the own key": {
			input: "user: {16: {0x11: 2}, 0x10: {0x11: x}, <<: {\"0x10\": 1}}\n",
			want:  `1:36: $.user.16.17: expected "integer", got "string"`,
		},
		"own key spelled as its name before a merge key": {
			// The own key decodes to 0x10, and the merged 0x10 sets the
			// member 16, but a path through 0x10 selects the merged key.
			input: "user: {\"0x10\": {0x11: x}, <<: {0x10: {0x11: 1}, 16: 2}}\n",
			want:  `1:23: $.user.0x10.17: expected "integer", got "string"`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dd := yamltest.FirstDocument(t, tc.input)

			err := dd.Validate(t.Context(), v)

			bound := yamltest.RequireSourceError(t, err)
			assert.Equal(t, tc.want, bound.Error())
			requireUnresolved(t, dd, bound)
		})
	}
}

func TestSchema_SourcePath_HiddenKeyName(t *testing.T) {
	t.Parallel()

	// The schema allows no member of user, so each violation points at a
	// key. The merge brings in 0x10 as the member 16, and the quoted key
	// "0x10" wins a path through 0x10. The path of the member 16 keeps its
	// decoded name, and its error binds at the key the merge brings in.
	v := compileSchema(t, `{
		"type": "object",
		"properties": {"user": {"additionalProperties": false}}
	}`)

	dd := yamltest.FirstDocument(t, stringtest.Input(`
		user:
		  <<: {0x10: x}
		  "0x10": 1
	`))

	err := dd.Validate(t.Context(), v)

	bound := yamltest.RequireSourceError(t, err)

	var got []string

	for _, child := range yamltest.ProblemsOf(bound) {
		got = append(got, child.Bound.Error())
	}

	assert.ElementsMatch(t, []string{
		"2:8: $.user.16~: value is not allowed",
		"3:3: $.user.0x10~: value is not allowed",
	}, got)
}

func TestSchema_SourcePath_SeveralViolations(t *testing.T) {
	t.Parallel()

	// Several violations lie under one mapping, and each path spells the
	// key of the member the decode keeps. The cases cover the later of two
	// keys that decode to 1, an explicit key written before a merge key, and
	// an alias key, which the path spells by the content of its anchor.
	v := compileSchema(t, `{"additionalProperties": {"type": "integer"}}`)

	dd := yamltest.FirstDocument(t, stringtest.Input(`
		base: &b {m: 1}
		0x10: a
		<<: *b
		1.0: b
		1: c
		a: &k name
		*k : d
	`))

	err := dd.Validate(t.Context(), v)

	bound := yamltest.RequireSourceError(t, err)

	var got []string

	for _, child := range yamltest.ProblemsOf(bound) {
		path, ok := child.Path()
		require.True(t, ok, "violation carries no path")

		_, err := dd.At(path)
		require.NoError(t, err, "path %s does not resolve", path)

		got = append(got, path.String())
	}

	assert.ElementsMatch(t, []string{"$.base", "$.0x10", "$.1", "$.a", "$.name"}, got)
}

func TestSchema_SourcePath_MergeReads(t *testing.T) {
	t.Parallel()

	// Every member of m breaks the schema, and the violations of one
	// Check share one limit on the nodes they read under the merge key.
	// Check reports each violation past that limit too, with a path
	// that keeps the decoded name of its key, which selects no entry.
	v := compileSchema(t, integersInM)

	tcs := map[string]struct {
		members int
		decoded bool
	}{
		"few merge sources": {members: 10},
		"many merge sources": {
			members: 2000,
			decoded: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dd := yamltest.FirstDocument(t, yamltest.WideMerge(tc.members))

			err := v.Check(t.Context(), dd)

			var ve *niceyaml.Error

			require.ErrorAs(t, err, &ve)

			violations := yamltest.ProblemsOf(ve)
			require.Len(t, violations, tc.members)

			spelled := 0

			for _, violation := range violations {
				var child *niceyaml.Error

				require.ErrorAs(t, violation.Err, &child)

				path, ok := child.Path()
				require.True(t, ok, "violation carries no path")

				if strings.HasPrefix(path.String(), "@.m.0x") {
					_, err := dd.At(path)
					require.NoError(t, err, "path %s does not resolve", path)

					spelled++
				}
			}

			if tc.decoded {
				assert.Positive(t, spelled)
				assert.Less(t, spelled, tc.members)

				return
			}

			assert.Equal(t, tc.members, spelled)
		})
	}
}

func TestSchema_RefToRejectingSchema(t *testing.T) {
	t.Parallel()

	// A $ref that resolves to a schema allowing nothing fails as a
	// violation of the document, at the value the reference applies to,
	// and not as a reference the validator could not resolve.
	tcs := map[string]string{
		"false":  `{"$defs": {"never": false}, "properties": {"a": {"$ref": "#/$defs/never"}}}`,
		"not":    `{"$defs": {"never": {"not": {}}}, "properties": {"a": {"$ref": "#/$defs/never"}}}`,
		"chain":  `{"$defs": {"never": false, "via": {"$ref": "#/$defs/never"}}, "properties": {"a": {"$ref": "#/$defs/via"}}}`,
		"direct": `{"properties": {"a": false}}`,
	}

	for name, schemaData := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			v := compileSchema(t, schemaData)

			err := v.Check(t.Context(), yamltest.FirstDocument(t, "a: 1\n"))
			require.NotErrorIs(t, err, schema.ErrValidate)

			var nerr *niceyaml.Error

			require.ErrorAs(t, err, &nerr)

			// Check writes the path from the node it got.
			path, ok := nerr.Path()
			require.True(t, ok)
			assert.Equal(t, "@.a", path.String())
		})
	}
}

func TestFromJSONSchema(t *testing.T) {
	t.Parallel()

	t.Run("wraps a compiled validator", func(t *testing.T) {
		t.Parallel()

		s := schema.FromJSONSchema(jsonschema.MustCompileJSON([]byte(`{"type": "object"}`)))

		require.NoError(t, s.ValidateValue(t.Context(), map[string]any{"key": "value"}))
		require.Error(t, s.ValidateValue(t.Context(), "value"))
	})

	t.Run("nil validator panics", func(t *testing.T) {
		t.Parallel()

		assert.PanicsWithValue(t, "schema.FromJSONSchema: validator is nil", func() {
			schema.FromJSONSchema(nil)
		})
	})
}

func TestSchema_Ref(t *testing.T) {
	t.Parallel()

	s := schema.MustCompile([]byte(`{"type": "object"}`))

	ref, err := s.Resolve(t.Context(), yamltest.FirstDocument(t, "key: value\n"))
	require.NoError(t, err)
	assert.Same(t, s, ref.Schema())
	assert.Equal(t, s.Ref(), ref)
	assert.Empty(t, ref.Key())

	assert.Nil(t, schema.Embedded([]byte(`{}`)).Schema())
	assert.Nil(t, schema.Ref{}.Schema())
}
