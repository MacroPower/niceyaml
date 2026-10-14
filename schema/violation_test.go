package schema_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/schema"
)

// violations returns the [schema.Violation] of each problem in the tree
// of err that wraps one, in the order [niceyaml.Problems] yields them.
func violations(err error) []schema.Violation {
	var got []schema.Violation

	for problem := range niceyaml.Problems(err) {
		if v, ok := errors.AsType[*schema.Violation](problem.Err); ok {
			got = append(got, *v)
		}
	}

	return got
}

// requireViolation returns the first [*schema.Violation] in the chain of
// err. The test fails when err holds none.
func requireViolation(t *testing.T, err error) *schema.Violation {
	t.Helper()

	v, ok := errors.AsType[*schema.Violation](err)
	require.True(t, ok)

	return v
}

// treeRows returns the label of each node of the tree of err, one row per
// node, each two spaces deeper than the node above it.
func treeRows(err error) []string {
	var rows []string

	var walk func(t niceyaml.ErrorTree, depth int)

	walk = func(t niceyaml.ErrorTree, depth int) {
		rows = append(rows, strings.Repeat("  ", depth)+t.Label)

		for _, child := range t.Children {
			walk(child, depth+1)
		}
	}

	walk(niceyaml.NewErrorTree(err), 0)

	return rows
}

func TestViolation(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		schema string
		input  string
		want   []schema.Violation
	}{
		"type": {
			schema: `{"properties": {"name": {"type": "string"}}}`,
			input:  "name: 123\n",
			want:   []schema.Violation{nameType},
		},
		"required": {
			schema: `{"required": ["name"]}`,
			input:  "other: 1\n",
			want: []schema.Violation{{
				Keyword:    "required",
				SchemaPath: "/required",
				Message:    `missing required property "name"`,
			}},
		},
		"additional property": {
			schema: `{"properties": {"name": {}}, "additionalProperties": false}`,
			input:  "name: a\nextra: 1\n",
			want: []schema.Violation{{
				Keyword:    "additionalProperties",
				SchemaPath: "/additionalProperties",
				Message:    "value is not allowed",
			}},
		},
		"item of a sequence": {
			schema: `{"properties": {"ports": {"items": {"minimum": 1}}}}`,
			input:  "ports: [80, 0]\n",
			want: []schema.Violation{{
				Keyword:    "minimum",
				SchemaPath: "/properties/ports/items/minimum",
				Message:    "0 is less than 1",
			}},
		},
		"keyword behind a $ref": {
			// The path steps through the $ref and never names the
			// definition under $defs.
			schema: `{
				"properties": {"spec": {"$ref": "#/$defs/Spec"}},
				"$defs": {"Spec": {"properties": {"port": {"type": "integer"}}}}
			}`,
			input: stringtest.Input(`
				spec:
				  port: http
			`),
			want: []schema.Violation{{
				Keyword:    "type",
				SchemaPath: "/properties/spec/$ref/properties/port/type",
				Message:    `expected "integer", got "string"`,
			}},
		},
		"one definition behind two references": {
			schema: `{
				"properties": {
					"a": {"$ref": "#/$defs/Port"},
					"b": {"$ref": "#/$defs/Port"}
				},
				"$defs": {"Port": {"type": "integer"}}
			}`,
			input: "a: http\nb: https\n",
			want: []schema.Violation{
				{
					Keyword:    "type",
					SchemaPath: "/properties/a/$ref/type",
					Message:    `expected "integer", got "string"`,
				},
				{
					Keyword:    "type",
					SchemaPath: "/properties/b/$ref/type",
					Message:    `expected "integer", got "string"`,
				},
			},
		},
		"several keywords": {
			schema: `{
				"properties": {"name": {"type": "string"}},
				"required": ["kind"]
			}`,
			input: "name: 123\n",
			want: []schema.Violation{
				nameType,
				{
					Keyword:    "required",
					SchemaPath: "/required",
					Message:    `missing required property "kind"`,
				},
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			v := compileSchema(t, tc.schema)

			err := yamltest.FirstDocument(t, tc.input).Validate(t.Context(), v)
			require.Error(t, err)

			// The validator reports the members of a mapping in no fixed
			// order.
			assert.ElementsMatch(t, tc.want, violations(err))
		})
	}
}

// nameType is the violation of a name that holds an integer where its
// schema takes a string.
var nameType = schema.Violation{
	Keyword:    "type",
	SchemaPath: "/properties/name/type",
	Message:    `expected "string", got "integer"`,
}

// nameAndAge is a schema that takes a string for the member name and an
// integer for the member age.
const nameAndAge = `{
	"properties": {
		"name": {"type": "string"},
		"age": {"type": "integer"}
	}
}`

func TestViolation_Binding(t *testing.T) {
	t.Parallel()

	v := compileSchema(t, nameAndAge)

	t.Run("single violation", func(t *testing.T) {
		t.Parallel()

		err := yamltest.FirstDocument(t, "name: 123\n").Validate(t.Context(), v)

		bound := yamltest.RequireSourceError(t, err)

		got := requireViolation(t, bound)

		// The violation gives the binding its message.
		assert.Equal(t, got.Message, bound.Message())
		assert.Equal(t, got.Message, got.Error())
	})

	t.Run("several violations", func(t *testing.T) {
		t.Parallel()

		err := yamltest.FirstDocument(t, stringtest.LinesLF("name: 123", "age: old")).Validate(t.Context(), v)

		bound := yamltest.RequireSourceError(t, err)
		require.Len(t, yamltest.ProblemsOf(bound), 2)

		// The summary counts the violations and is none itself, so a search
		// of the whole binding descends into the first violation under it.
		assert.Equal(t, "type", requireViolation(t, bound).Keyword)

		for _, child := range yamltest.ProblemsOf(bound) {
			got := requireViolation(t, child.Err)
			assert.Equal(t, "type", got.Keyword)
			assert.Equal(t, got.Message, child.Message())
		}
	})

	t.Run("scoped node", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.Input(`
			spec:
			  name: 123
		`))

		err := yamltest.At(t, doc, paths.Current().Child("spec")).Validate(t.Context(), v)

		assert.Equal(t, []schema.Violation{nameType}, violations(err))
	})
}

func TestViolation_Collection(t *testing.T) {
	t.Parallel()

	// A failure about a mapping or a sequence as a whole carries the path
	// of that value, and binds at the token that introduces the value. A
	// failure about the key of a member carries a path that ends in `~`.
	tcs := map[string]struct {
		schema string
		input  string
		want   string
	}{
		"maxItems of a flow sequence under a key": {
			schema: `{"properties": {"tags": {"maxItems": 2}}}`,
			input:  "name: x\ntags: [a, b, c]\n",
			want:   `2:1: $.tags: array has 3 items, maximum is 2`,
		},
		"minItems of a block sequence under a nested key": {
			schema: `{"properties": {"pool": {"properties": {"members": {"minItems": 2}}}}}`,
			input: stringtest.Input(`
				pool:
				  members:
				    - x
			`),
			want: `2:3: $.pool.members: array has 1 items, minimum is 2`,
		},
		"uniqueItems of a block sequence under a key": {
			schema: `{"properties": {"tags": {"uniqueItems": true}}}`,
			input: stringtest.Input(`
				tags:
				  - a
				  - a
			`),
			want: `1:1: $.tags: array contains duplicate items`,
		},
		"contains of a flow sequence under a key": {
			schema: `{"properties": {"tags": {"contains": {"const": "x"}}}}`,
			input:  "tags: [a]\n",
			want:   `1:1: $.tags: array has 0 matching items, minimum is 1`,
		},
		"maxProperties of a block mapping under a key": {
			schema: `{"properties": {"hours": {"maxProperties": 1}}}`,
			input: stringtest.Input(`
				hours:
				  open: 9
				  close: 5
			`),
			want: `1:1: $.hours: object has 2 properties, maximum is 1`,
		},
		"maxProperties of a block mapping element": {
			schema: `{"properties": {"items": {"items": {"maxProperties": 1}}}}`,
			input: stringtest.Input(`
				items:
				  - a: 1
				    b: 2
			`),
			want: `2:3: $.items[0]: object has 2 properties, maximum is 1`,
		},
		"maxProperties of a flow mapping in a flow sequence": {
			schema: `{"properties": {"items": {"items": {"maxProperties": 1}}}}`,
			input:  "items: [{a: 1, b: 2}]\n",
			want:   `1:9: $.items[0]: object has 2 properties, maximum is 1`,
		},
		"maxProperties of a block mapping at the root": {
			schema: `{"maxProperties": 1}`,
			input:  "a: 1\nb: 2\n",
			want:   `1:1: $: object has 2 properties, maximum is 1`,
		},
		"maxProperties of a flow mapping at the root": {
			schema: `{"maxProperties": 1}`,
			input:  "{a: 1, b: 2}\n",
			want:   `1:1: $: object has 2 properties, maximum is 1`,
		},
		"type of a block mapping under a key": {
			schema: `{"properties": {"name": {"type": "string"}}}`,
			input: stringtest.Input(`
				name:
				  first: Ada
			`),
			want: `1:1: $.name: expected "string", got "object"`,
		},
		"additionalProperties points at the key of the member": {
			schema: `{"properties": {"name": {}}, "additionalProperties": false}`,
			input:  "name: x\nextra: [1]\n",
			want:   `2:1: $.extra~: value is not allowed`,
		},
		"propertyNames points at the key of the member": {
			schema: `{"propertyNames": {"pattern": "^[a-z]+$"}}`,
			input:  "name: x\nBad: 1\n",
			want:   `2:1: $.Bad~: property name "Bad" is invalid`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input)

			err := doc.Validate(t.Context(), compileSchema(t, tc.schema))
			require.EqualError(t, err, tc.want)
		})
	}
}

func TestViolation_MissingMember(t *testing.T) {
	t.Parallel()

	server := `{"properties": {"server": {"required": ["name"]}}}`

	tcs := map[string]struct {
		schema string
		input  string
		// The bound text of each violation, in the order the validator
		// reports them.
		want []string
		// The path [niceyaml.SourceError.Nearest] reports for each
		// violation, or the empty string for one that binds at a position
		// beside its path.
		near []string
	}{
		"required": {
			schema: server,
			input: stringtest.Input(`
				a: 1
				server:
				  port: 1
			`),
			want: []string{`2:1: $.server.name: missing required property "name"`},
			near: []string{"$.server"},
		},
		"required at the root": {
			schema: `{"required": ["name", "kind"]}`,
			input:  "other: x\n",
			want: []string{
				`1:1: $.name: missing required property "name"`,
				`1:1: $.kind: missing required property "kind"`,
			},
			near: []string{"$", "$"},
		},
		"element of a sequence": {
			schema: `{"properties": {"items": {"items": {"required": ["name"]}}}}`,
			input: stringtest.Input(`
				items:
				  - name: a
				  - port: 1
			`),
			want: []string{`3:3: $.items[1].name: missing required property "name"`},
			near: []string{"$.items[1]"},
		},
		"empty mapping": {
			schema: server,
			input:  "server: {}\n",
			want:   []string{`1:1: $.server.name: missing required property "name"`},
			near:   []string{"$.server"},
		},
		"mapping behind an alias": {
			schema: server,
			input: stringtest.Input(`
				base: &b
				  port: 1
				server: *b
			`),
			want: []string{`3:1: $.server.name: missing required property "name"`},
			near: []string{"$.server"},
		},
		"mapping with a merge key": {
			schema: server,
			input: stringtest.Input(`
				base: &b {port: 1}
				server:
				  <<: *b
			`),
			want: []string{`2:1: $.server.name: missing required property "name"`},
			near: []string{"$.server"},
		},
		"dependentRequired": {
			schema: `{"properties": {"server": {"dependentRequired": {"port": ["name", "host"]}}}}`,
			input: stringtest.Input(`
				server:
				  port: 1
			`),
			want: []string{
				`1:1: $.server.name: property "port" requires property "name"`,
				`1:1: $.server.host: property "port" requires property "host"`,
			},
			near: []string{"$.server", "$.server"},
		},
		"dependencies": {
			schema: `{
				"$schema": "http://json-schema.org/draft-07/schema#",
				"properties": {"server": {"dependencies": {"port": ["name"]}}}
			}`,
			input: stringtest.Input(`
				server:
				  port: 1
			`),
			want: []string{`1:1: $.server.name: property "port" requires property "name"`},
			near: []string{"$.server"},
		},
		"names a selector quotes": {
			schema: `{"properties": {"server": {"required": ["a.b", "c d", ""]}}}`,
			input: stringtest.Input(`
				server:
				  port: 1
			`),
			want: []string{
				`1:1: $.server.'a.b': missing required property "a.b"`,
				`1:1: $.server.'c d': missing required property "c d"`,
				`1:1: $.server.'': missing required property ""`,
			},
			near: []string{"$.server", "$.server", "$.server"},
		},
		// Both names hold the quote and the words that part the two names
		// in the message.
		"names that hold a quote": {
			schema: `{"dependentRequired": {"a \" requires property \"b": ["c \" d"]}}`,
			input:  stringtest.LinesLF(`'a " requires property "b': 1`),
			want: []string{
				`1:1: $.'c " d': property "a \" requires property \"b" requires property "c \" d"`,
			},
			near: []string{"$"},
		},
		// The key 0x10 sets the member 16, so the mapping lacks the member
		// 0x10, and the path of that member selects the entry of the key.
		// The violation binds at the key of the mapping by a position.
		"key spelled as the missing member": {
			schema: `{"properties": {"server": {"required": ["0x10"]}}}`,
			input: stringtest.Input(`
				a: 1
				server:
				  0x10: 1
			`),
			want: []string{`2:1: $.server.0x10: missing required property "0x10"`},
			near: []string{""},
		},
		"merged key spelled as the missing member": {
			schema: `{"properties": {"server": {"required": ["0x10"]}}}`,
			input: stringtest.Input(`
				base: &b {0x10: 1}
				server:
				  <<: *b
				  x: 1
			`),
			want: []string{`2:1: $.server.0x10: missing required property "0x10"`},
			near: []string{""},
		},
		// The quoted "0x10" wins the selector 0x10, so no path selects the
		// mapping the key 0x10 holds, and the path names it as the decoder
		// does.
		"mapping no path selects": {
			schema: `{"properties": {"user": {"properties": {"16": {"required": ["name"]}}}}}`,
			input: stringtest.Input(`
				user:
				  <<: {0x10: {port: 1}}
				  "0x10": 1
			`),
			want: []string{`2:8: $.user.16.name: missing required property "name"`},
			near: []string{""},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input)

			err := doc.Validate(t.Context(), compileSchema(t, tc.schema))
			require.Error(t, err)

			var got, near []string

			for problem := range niceyaml.Problems(err) {
				v, ok := errors.AsType[*schema.Violation](problem.Err)
				if !ok {
					continue
				}

				bound := problem.Bound
				require.NotNil(t, bound)

				// The message stays the one the validator wrote.
				assert.Equal(t, v.Message, bound.Message())

				got = append(got, bound.Error())

				at, ok := bound.Nearest()
				if !ok {
					near = append(near, "")

					continue
				}

				near = append(near, at.String())

				// The path names a member the document leaves out.
				_, err := doc.At(yamltest.PathOf(t, bound))
				require.ErrorIs(t, err, paths.ErrNotFound)
			}

			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.near, near)
		})
	}

	t.Run("scoped node", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.Input(`
			a: 1
			server:
			  port: 1
		`))
		v := compileSchema(t, `{"required": ["name"]}`)

		err := yamltest.At(t, doc, paths.Current().Child("server")).Validate(t.Context(), v)

		bound := yamltest.RequireSourceError(t, err)
		assert.Equal(t, `2:1: $.server.name: missing required property "name"`, bound.Error())

		near, ok := bound.Nearest()
		require.True(t, ok)
		assert.Equal(t, "$.server", near.String())
	})

	t.Run("value", func(t *testing.T) {
		t.Parallel()

		v := compileSchema(t, server)

		err := v.ValidateValue(t.Context(), map[string]any{"server": map[string]any{"port": 1}})

		// The value came from no document, so the message names the field
		// and no position.
		bound := yamltest.RequireSourceError(t, err)
		assert.Equal(t, `$.server.name: missing required property "name"`, bound.Error())
		assert.Equal(t, "$.server.name", yamltest.PathOf(t, bound).String())

		_, ok := bound.Nearest()
		assert.False(t, ok)
	})
}

func TestViolation_Value(t *testing.T) {
	t.Parallel()

	v := compileSchema(t, nameAndAge)

	t.Run("single violation", func(t *testing.T) {
		t.Parallel()

		err := v.ValidateValue(t.Context(), map[string]any{"name": 123})

		assert.Equal(t, nameType, *requireViolation(t, yamltest.RequireError(t, err)))
	})

	t.Run("several violations", func(t *testing.T) {
		t.Parallel()

		err := v.ValidateValue(t.Context(), map[string]any{"name": 123, "age": "old"})

		nested := yamltest.ProblemsOf(yamltest.RequireError(t, err))
		require.Len(t, nested, 2)

		for _, n := range nested {
			assert.Equal(t, "type", requireViolation(t, yamltest.RequireError(t, n.Err)).Keyword)
		}
	})
}

func TestViolation_Alternatives(t *testing.T) {
	t.Parallel()

	// A step is a run or a copy, told apart by its kind, or the name of
	// one as a string.
	const step = `{
		"properties": {"step": {"oneOf": [
			{
				"type": "object",
				"properties": {"kind": {"const": "run"}, "cmd": {"type": "string"}},
				"required": ["kind", "cmd"],
				"additionalProperties": false
			},
			{
				"type": "object",
				"properties": {
					"kind": {"const": "copy"},
					"from": {"type": "string"},
					"to": {"type": "string"}
				},
				"required": ["kind", "from", "to"],
				"additionalProperties": false
			},
			{"type": "string"}
		]}}
	}`

	// A nullable string with a pattern, as a schema generated from a Go
	// pointer field writes one.
	const nullable = `{"anyOf": [{"type": "string", "pattern": "^[0-9]+m$"}, {"type": "null"}]}`

	tcs := map[string]struct {
		schema string
		input  string
		want   []string
	}{
		"null branch of a nullable string": {
			schema: `{"properties": {"sla": ` + nullable + `}}`,
			input:  "sla: 15M\n",
			want: []string{
				`1:6: $.sla: string does not match pattern "^[0-9]+m$"`,
			},
		},
		"nullable string beside another violation": {
			schema: `{"properties": {"sla": ` + nullable + `, "days": {"type": "array"}}}`,
			input:  "sla: 15M\ndays: Monday\n",
			want: []string{
				`2 schema violations`,
				`  1:6: $.sla: string does not match pattern "^[0-9]+m$"`,
				`  2:7: $.days: expected "array", got "string"`,
			},
		},
		"null branch of a nullable object": {
			schema: `{
				"properties": {"settings": {"anyOf": [{"$ref": "#/$defs/Settings"}, {"type": "null"}]}},
				"$defs": {"Settings": {
					"type": "object",
					"properties": {"wifi": {"type": "boolean"}, "theme": {"enum": ["light", "dark"]}},
					"additionalProperties": false
				}}
			}`,
			input: stringtest.Input(`
				settings:
				  wifi: yes please
				  theme: neon
				  extra: 1
			`),
			want: []string{
				`3 schema violations`,
				`  2:9: $.settings.wifi: expected "boolean", got "string"`,
				`  3:10: $.settings.theme: value does not match any enum member`,
				`  4:3: $.settings.extra~: value is not allowed`,
			},
		},
		"null branch behind a $ref": {
			schema: `{
				"properties": {"sla": {"anyOf": [{"type": "string", "minLength": 3}, {"$ref": "#/$defs/Null"}]}},
				"$defs": {"Null": {"type": "null"}}
			}`,
			input: "sla: ab\n",
			want: []string{
				`1:6: $.sla: string length 2 is less than 3`,
			},
		},
		"nullable items of a sequence": {
			schema: `{"properties": {"ports": {"items": {"anyOf": [{"type": "integer", "minimum": 1}, {"type": "null"}]}}}}`,
			input:  "ports: [80, 0, ~, -1]\n",
			want: []string{
				`2 schema violations`,
				`  1:13: $.ports[1]: 0 is less than 1`,
				`  1:19: $.ports[3]: -1 is less than 1`,
			},
		},
		"single branch": {
			schema: `{"properties": {"n": {"anyOf": [{"type": "integer"}]}}}`,
			input:  "n: x\n",
			want: []string{
				`1:4: $.n: expected "integer", got "string"`,
			},
		},
		"every branch fails by type": {
			// No branch says more than another, so each one stays.
			schema: `{"properties": {"port": {"anyOf": [{"type": "integer"}, {"type": "string"}, {"type": "null"}]}}}`,
			input:  "port: [1]\n",
			want: []string{
				`1:1: $.port: value matches none of the allowed forms`,
				`  form 1`,
				`    1:1: $.port: expected "integer", got "array"`,
				`  form 2`,
				`    1:1: $.port: expected "string", got "array"`,
				`  form 3`,
				`    1:1: $.port: expected "null", got "array"`,
			},
		},
		"every branch fails by type at the root": {
			schema: `{"anyOf": [{"type": "array"}, {"type": "string"}]}`,
			input:  "a: 1\n",
			want: []string{
				`1:1: $: value matches none of the allowed forms`,
				`  form 1`,
				`    1:1: $: expected "array", got "object"`,
				`  form 2`,
				`    1:1: $: expected "string", got "object"`,
			},
		},
		"several branches remain": {
			// The string branch fails by type alone and drops out. The
			// two object branches both remain, each with its own failures.
			schema: step,
			input: stringtest.Input(`
				step:
				  kind: copy
				  from: a
				  dest: b
			`),
			want: []string{
				`1:1: $.step: value matches none of the allowed forms`,
				`  form 1`,
				`    1:1: $.step.cmd: missing required property "cmd"`,
				`    2:9: $.step.kind: value does not match const`,
				`    3:3: $.step.from~: value is not allowed`,
				`    4:3: $.step.dest~: value is not allowed`,
				`  form 2`,
				`    1:1: $.step.to: missing required property "to"`,
				`    4:3: $.step.dest~: value is not allowed`,
			},
		},
		"sequence where a mapping or a string goes": {
			// Both object branches fail by type alone, as the string
			// branch does, so each one stays.
			schema: step,
			input:  "step: [run]\n",
			want: []string{
				`1:1: $.step: value matches none of the allowed forms`,
				`  form 1`,
				`    1:1: $.step: expected "object", got "array"`,
				`  form 2`,
				`    1:1: $.step: expected "object", got "array"`,
				`  form 3`,
				`    1:1: $.step: expected "string", got "array"`,
			},
		},
		"forms keep their position in the schema": {
			// The second branch drops out, and the third is still form 3.
			schema: `{"properties": {"v": {"anyOf": [
				{"type": "string", "minLength": 3},
				{"type": "integer"},
				{"type": "string", "pattern": "^x"}
			]}}}`,
			input: "v: ab\n",
			want: []string{
				`1:4: $.v: value matches none of the allowed forms`,
				`  form 1`,
				`    1:4: $.v: string length 2 is less than 3`,
				`  form 3`,
				`    1:4: $.v: string does not match pattern "^x"`,
			},
		},
		"type failure of a member beside a type failure of the value": {
			// The first branch takes the mapping and fails at a member,
			// so it remains, and the second fails by the type of the
			// value.
			schema: `{"properties": {"v": {"anyOf": [
				{"properties": {"a": {"type": "integer"}}},
				{"type": "array"}
			]}}}`,
			input: stringtest.Input(`
				v:
				  a: x
			`),
			want: []string{
				`2:6: $.v.a: expected "integer", got "string"`,
			},
		},
		"type failures of a member in two branches": {
			schema: `{"properties": {"v": {"anyOf": [
				{"properties": {"a": {"type": "integer"}}},
				{"properties": {"a": {"type": "boolean"}}}
			]}}}`,
			input: stringtest.Input(`
				v:
				  a: x
			`),
			want: []string{
				`1:1: $.v: value matches none of the allowed forms`,
				`  form 1`,
				`    2:6: $.v.a: expected "integer", got "string"`,
				`  form 2`,
				`    2:6: $.v.a: expected "boolean", got "string"`,
			},
		},
		"anyOf inside a branch of a oneOf": {
			// The array branch of the oneOf drops out, and then the null
			// branch of the anyOf below the branch that remains.
			schema: `{"properties": {"v": {"oneOf": [
				{
					"type": "object",
					"properties": {"n": {"anyOf": [{"type": "integer", "minimum": 1}, {"type": "null"}]}}
				},
				{"type": "array"}
			]}}}`,
			input: stringtest.Input(`
				v:
				  n: 0
			`),
			want: []string{
				`2:6: $.v.n: 0 is less than 1`,
			},
		},
		"oneOf that matches two branches": {
			// The value fails no branch, so the keyword itself is the
			// one failure.
			schema: `{"properties": {"n": {"oneOf": [{"type": "integer"}, {"minimum": 1}]}}}`,
			input:  "n: 5\n",
			want: []string{
				`1:4: $.n: validated against 2 subschemas, expected exactly one`,
			},
		},
		"allOf and then": {
			// Every subschema of these keywords applies, so each failure
			// is a violation.
			schema: `{"allOf": [
				{"properties": {"a": {"type": "integer"}}},
				{"if": {"properties": {"mode": {"const": "x"}}}, "then": {"required": ["extra"]}}
			]}`,
			input: "a: no\nmode: x\n",
			want: []string{
				`2 schema violations`,
				`  1:1: $.extra: missing required property "extra"`,
				`  1:4: $.a: expected "integer", got "string"`,
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			v := compileSchema(t, tc.schema)

			err := yamltest.FirstDocument(t, tc.input).Validate(t.Context(), v)
			require.Error(t, err)

			assert.Equal(t, tc.want, treeRows(err))
		})
	}
}

func TestViolation_Forms(t *testing.T) {
	t.Parallel()

	v := compileSchema(t, `{"properties": {"v": {"anyOf": [
		{"type": "string", "minLength": 3},
		{"type": "string", "pattern": "^x"}
	]}}}`)

	t.Run("bound", func(t *testing.T) {
		t.Parallel()

		err := yamltest.FirstDocument(t, "v: ab\n").Validate(t.Context(), v)

		bound := yamltest.RequireSourceError(t, err)

		// The value has one violation, which carries the path to the
		// value and names the keyword.
		assert.Equal(t, "$.v", yamltest.PathOf(t, bound).String())

		_, ok := bound.Range()
		assert.True(t, ok)

		assert.Equal(t, schema.Violation{
			Keyword:    "anyOf",
			SchemaPath: "/properties/v/anyOf",
			Message:    "value matches none of the allowed forms",
		}, *requireViolation(t, bound))

		// The value is one problem, so the binding heads none, and the
		// forms are its details. Each form carries no location and wraps
		// no Violation, and the violation of its branch carries both.
		assert.Empty(t, yamltest.ProblemsOf(bound))

		forms := yamltest.DetailsOf(bound)
		require.Len(t, forms, 2)

		want := []schema.Violation{
			{
				Keyword:    "minLength",
				SchemaPath: "/properties/v/anyOf/0/minLength",
				Message:    "string length 2 is less than 3",
			},
			{
				Keyword:    "pattern",
				SchemaPath: "/properties/v/anyOf/1/pattern",
				Message:    `string does not match pattern "^x"`,
			},
		}

		for i, form := range forms {
			assert.Equal(t, []string{"form 1", "form 2"}[i], form.Message())

			_, ok := form.Path()
			assert.False(t, ok)

			_, ok = form.Range()
			assert.False(t, ok)
			require.NoError(t, form.Bound.Unresolved())

			own, ok := errors.AsType[*schema.Violation](form.Err)
			assert.False(t, ok)
			assert.Nil(t, own)

			nested := yamltest.DetailsOf(form.Bound)
			require.Len(t, nested, 1)
			assert.Equal(t, "$.v", yamltest.PathOf(t, nested[0]).String())
			assert.Equal(t, want[i], *requireViolation(t, nested[0].Err))
		}
	})

	t.Run("value", func(t *testing.T) {
		t.Parallel()

		err := v.ValidateValue(t.Context(), map[string]any{"v": "ab"})

		bound := yamltest.RequireSourceError(t, err)
		assert.Equal(t, "$.v", yamltest.PathOf(t, bound).String())

		_, ok := bound.Range()
		assert.False(t, ok)

		assert.Equal(t, "anyOf", requireViolation(t, bound).Keyword)

		assert.Empty(t, yamltest.ProblemsOf(bound))

		forms := yamltest.DetailsOf(bound)
		require.Len(t, forms, 2)

		for i, form := range forms {
			assert.Equal(t, []string{"form 1", "form 2"}[i], form.Message())

			_, ok := form.Path()
			assert.False(t, ok)
			assert.Len(t, yamltest.DetailsOf(form.Bound), 1)
		}

		// A document places the violation, and its forms come with it.
		placed := yamltest.RequireSourceError(t, yamltest.FirstDocument(t, "v: ab\n").Bind(err))

		_, ok = placed.Range()
		assert.True(t, ok)
		assert.Len(t, yamltest.DetailsOf(placed), 2)
	})
}

func TestViolation_Problems(t *testing.T) {
	t.Parallel()

	v := compileSchema(t, `{
		"properties": {
			"v": {"anyOf": [
				{"type": "string", "minLength": 3},
				{"type": "string", "pattern": "^x"}
			]},
			"port": {"type": "integer", "minimum": 1}
		},
		"additionalProperties": false
	}`)

	// Each row is the message of a problem and the number of nodes below
	// it, which are its details.
	tcs := map[string]struct {
		input   string
		wantMsg string
		want    []string
	}{
		"a failed anyOf is one problem": {
			input:   "v: ab\n",
			wantMsg: "1:4: $.v: value matches none of the allowed forms",
			want:    []string{"value matches none of the allowed forms (2)"},
		},
		"the problems of a validation match the count of its summary": {
			input: stringtest.Input(`
				v: ab
				port: 0
				extra: 1
			`),
			wantMsg: stringtest.JoinLF(
				"3 schema violations",
				"1:4: $.v: value matches none of the allowed forms",
				"2:7: $.port: 0 is less than 1",
				"3:1: $.extra~: value is not allowed",
			),
			want: []string{
				"value matches none of the allowed forms (2)",
				"0 is less than 1 (0)",
				"value is not allowed (0)",
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := yamltest.FirstDocument(t, tc.input).Validate(t.Context(), v)
			require.EqualError(t, err, tc.wantMsg)

			var got []string

			for problem := range niceyaml.NewErrorTree(err).Problems() {
				require.NotNil(t, problem.Bound)

				got = append(got, fmt.Sprintf("%s (%d)", problem.Bound.Message(), len(problem.Children)))
			}

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestViolation_Error(t *testing.T) {
	t.Parallel()

	v := &schema.Violation{Keyword: "type", SchemaPath: "/type", Message: "bad type"}
	require.EqualError(t, v, "bad type")

	var nilViolation *schema.Violation

	assert.Empty(t, nilViolation.Error())
}

func TestViolation_Is(t *testing.T) {
	t.Parallel()

	var nilViolation *schema.Violation

	violation := &schema.Violation{Keyword: "type", SchemaPath: "/type", Message: "bad type"}

	// The module keeps the mark internal, so each case reads it through
	// niceyaml.IsInvalid.
	tcs := map[string]struct {
		input error
		want  bool
	}{
		"bare violation": {
			input: violation,
			want:  true,
		},
		"inside an error with no location": {
			input: niceyaml.Invalid(violation),
			want:  true,
		},
		"behind a wrapper": {
			input: fmt.Errorf("check config: %w", violation),
			want:  true,
		},
		"nil violation behind a wrapper": {
			input: fmt.Errorf("check config: %w", nilViolation),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, niceyaml.IsInvalid(tc.input))
		})
	}

	t.Run("matches no exported target", func(t *testing.T) {
		t.Parallel()

		require.NotErrorIs(t, violation, schema.ErrValidate)
		require.NotErrorIs(t, violation, schema.ErrNoMatch)
	})

	t.Run("every violation of a validation is invalid", func(t *testing.T) {
		t.Parallel()

		s := compileSchema(t, `{"properties": {"a": {"type": "string"}, "b": {"maximum": 1}}}`)

		err := s.Check(t.Context(), yamltest.FirstDocument(t, stringtest.LinesLF("a: 1", "b: 2")))
		require.Error(t, err)
		assert.True(t, niceyaml.IsInvalid(err))

		value := s.ValidateValue(t.Context(), map[string]any{"a": 1, "b": 2})
		require.Error(t, value)
		assert.True(t, niceyaml.IsInvalid(value))
	})

	t.Run("a validation that could not run is not invalid", func(t *testing.T) {
		t.Parallel()

		s := compileSchema(t,
			`{"properties": {"a": {"$ref": "https://example.invalid/nope.json"}}}`,
			schema.WithRequireRefs(false),
		)

		err := s.ValidateValue(t.Context(), map[string]any{"a": 1})
		require.ErrorIs(t, err, schema.ErrValidate)
		assert.False(t, niceyaml.IsInvalid(err))
	})
}
