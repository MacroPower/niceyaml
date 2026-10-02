package schema_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/schema"
)

// violations returns the [schema.Violation] of each binding in the tree
// of err that reports one, in the order [niceyaml.AllBindings] yields them.
func violations(err error) []schema.Violation {
	var got []schema.Violation

	for bound := range niceyaml.AllBindings(err) {
		if v, ok := errors.AsType[*schema.Violation](bound.Cause()); ok {
			got = append(got, *v)
		}
	}

	return got
}

// treeText returns the tree of err as text, one node per line, each two
// spaces deeper than the node above it.
func treeText(err error) string {
	var sb strings.Builder

	var write func(t niceyaml.ErrorTree, depth int)

	write = func(t niceyaml.ErrorTree, depth int) {
		sb.WriteString(strings.Repeat("  ", depth) + t.Text + "\n")

		for _, child := range t.Children {
			write(child, depth+1)
		}
	}

	write(niceyaml.NewErrorTree(err), 0)

	return sb.String()
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
			want: []schema.Violation{{
				Keyword:    "type",
				SchemaPath: "/properties/name/type",
				Message:    `expected "string", got "integer"`,
			}},
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
			input: "spec:\n  port: http\n",
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
				{
					Keyword:    "type",
					SchemaPath: "/properties/name/type",
					Message:    `expected "string", got "integer"`,
				},
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

			v := compileSchema(t, []byte(tc.schema))

			err := yamltest.FirstDocument(t, tc.input).Validate(t.Context(), v)
			require.Error(t, err)

			// The validator reports the members of a mapping in no fixed
			// order.
			assert.ElementsMatch(t, tc.want, violations(err))
		})
	}
}

func TestViolation_Binding(t *testing.T) {
	t.Parallel()

	v := compileSchema(t, []byte(`{
		"properties": {
			"name": {"type": "string"},
			"age": {"type": "integer"}
		}
	}`))

	t.Run("single violation", func(t *testing.T) {
		t.Parallel()

		err := yamltest.FirstDocument(t, "name: 123\n").Validate(t.Context(), v)

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)

		got, ok := errors.AsType[*schema.Violation](bound.Cause())
		require.True(t, ok)

		// The violation gives the binding its message.
		assert.Equal(t, got.Message, bound.Message())
		assert.Equal(t, got.Message, got.Error())
	})

	t.Run("several violations", func(t *testing.T) {
		t.Parallel()

		err := yamltest.FirstDocument(t, "name: 123\nage: old\n").Validate(t.Context(), v)

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		require.Len(t, bound.Errors(), 2)

		// The summary counts the violations and is none itself, so its own
		// cause is no Violation, while a search of the whole binding
		// descends into the first violation under it.
		own, ok := errors.AsType[*schema.Violation](bound.Cause())
		assert.False(t, ok)
		assert.Nil(t, own)

		first, ok := errors.AsType[*schema.Violation](bound)
		require.True(t, ok)
		assert.Equal(t, "type", first.Keyword)

		for _, child := range bound.Errors() {
			got, ok := errors.AsType[*schema.Violation](child.Cause())
			require.True(t, ok)
			assert.Equal(t, "type", got.Keyword)
			assert.Equal(t, got.Message, child.Message())
		}
	})

	t.Run("scoped node", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, "spec:\n  name: 123\n")

		err := yamltest.At(t, doc, paths.Root().Child("spec")).Validate(t.Context(), v)

		assert.Equal(t, []schema.Violation{{
			Keyword:    "type",
			SchemaPath: "/properties/name/type",
			Message:    `expected "string", got "integer"`,
		}}, violations(err))
	})
}

func TestViolation_Unbound(t *testing.T) {
	t.Parallel()

	v := compileSchema(t, []byte(`{
		"properties": {
			"name": {"type": "string"},
			"age": {"type": "integer"}
		}
	}`))

	t.Run("single violation", func(t *testing.T) {
		t.Parallel()

		err := v.ValidateValue(t.Context(), map[string]any{"name": 123})

		var located *niceyaml.Error

		require.ErrorAs(t, err, &located)

		got, ok := errors.AsType[*schema.Violation](located.Cause())
		require.True(t, ok)
		assert.Equal(t, schema.Violation{
			Keyword:    "type",
			SchemaPath: "/properties/name/type",
			Message:    `expected "string", got "integer"`,
		}, *got)
	})

	t.Run("several violations", func(t *testing.T) {
		t.Parallel()

		err := v.ValidateValue(t.Context(), map[string]any{"name": 123, "age": "old"})

		var summary *niceyaml.Error

		require.ErrorAs(t, err, &summary)

		own, ok := errors.AsType[*schema.Violation](summary.Cause())
		assert.False(t, ok)
		assert.Nil(t, own)

		nested := summary.Errors()
		require.Len(t, nested, 2)

		for _, n := range nested {
			var located *niceyaml.Error

			require.ErrorAs(t, n, &located)

			got, ok := errors.AsType[*schema.Violation](located.Cause())
			require.True(t, ok)
			assert.Equal(t, "type", got.Keyword)
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
			input: "settings:\n  wifi: yes please\n  theme: neon\n  extra: 1\n",
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
				`1:8: $.port: value matches none of the allowed forms`,
				`  form 1`,
				`    1:8: $.port: expected "integer", got "array"`,
				`  form 2`,
				`    1:8: $.port: expected "string", got "array"`,
				`  form 3`,
				`    1:8: $.port: expected "null", got "array"`,
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
			input:  "step:\n  kind: copy\n  from: a\n  dest: b\n",
			want: []string{
				`2:3: $.step: value matches none of the allowed forms`,
				`  form 1`,
				`    1:1: $.step~: missing required property "cmd"`,
				`    2:9: $.step.kind: value does not match const`,
				`    3:3: $.step.from~: value is not allowed`,
				`    4:3: $.step.dest~: value is not allowed`,
				`  form 2`,
				`    1:1: $.step~: missing required property "to"`,
				`    4:3: $.step.dest~: value is not allowed`,
			},
		},
		"sequence where a mapping or a string goes": {
			// Both object branches fail by type alone, as the string
			// branch does, so each one stays.
			schema: step,
			input:  "step: [run]\n",
			want: []string{
				`1:8: $.step: value matches none of the allowed forms`,
				`  form 1`,
				`    1:8: $.step: expected "object", got "array"`,
				`  form 2`,
				`    1:8: $.step: expected "object", got "array"`,
				`  form 3`,
				`    1:8: $.step: expected "string", got "array"`,
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
			input: "v:\n  a: x\n",
			want: []string{
				`2:6: $.v.a: expected "integer", got "string"`,
			},
		},
		"type failures of a member in two branches": {
			schema: `{"properties": {"v": {"anyOf": [
				{"properties": {"a": {"type": "integer"}}},
				{"properties": {"a": {"type": "boolean"}}}
			]}}}`,
			input: "v:\n  a: x\n",
			want: []string{
				`2:3: $.v: value matches none of the allowed forms`,
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
			input: "v:\n  n: 0\n",
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
				`  1:1: $~: missing required property "extra"`,
				`  1:4: $.a: expected "integer", got "string"`,
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			v := compileSchema(t, []byte(tc.schema))

			err := yamltest.FirstDocument(t, tc.input).Validate(t.Context(), v)
			require.Error(t, err)

			assert.Equal(t, strings.Join(tc.want, "\n")+"\n", treeText(err))
		})
	}
}

func TestViolation_Forms(t *testing.T) {
	t.Parallel()

	v := compileSchema(t, []byte(`{"properties": {"v": {"anyOf": [
		{"type": "string", "minLength": 3},
		{"type": "string", "pattern": "^x"}
	]}}}`))

	value := paths.Root().Child("v")

	t.Run("bound", func(t *testing.T) {
		t.Parallel()

		err := yamltest.FirstDocument(t, "v: ab\n").Validate(t.Context(), v)

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)

		// The value has one violation, which carries the path to the
		// value and names the keyword.
		path, ok := bound.Path()
		require.True(t, ok)
		assert.Equal(t, value.String(), path.String())

		_, ok = bound.Range()
		assert.True(t, ok)

		got, ok := errors.AsType[*schema.Violation](bound.Cause())
		require.True(t, ok)
		assert.Equal(t, schema.Violation{
			Keyword:    "anyOf",
			SchemaPath: "/properties/v/anyOf",
			Message:    "value matches none of the allowed forms",
		}, *got)

		// Each form carries no location and wraps no Violation, and the
		// violation of its branch carries both.
		forms := bound.Errors()
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
			require.NoError(t, form.Unresolved())

			own, ok := errors.AsType[*schema.Violation](form.Cause())
			assert.False(t, ok)
			assert.Nil(t, own)

			nested := form.Errors()
			require.Len(t, nested, 1)

			path, ok := nested[0].Path()
			require.True(t, ok)
			assert.Equal(t, value.String(), path.String())

			got, ok := errors.AsType[*schema.Violation](nested[0].Cause())
			require.True(t, ok)
			assert.Equal(t, want[i], *got)
		}
	})

	t.Run("unbound", func(t *testing.T) {
		t.Parallel()

		err := v.ValidateValue(t.Context(), map[string]any{"v": "ab"})

		var located *niceyaml.Error

		require.ErrorAs(t, err, &located)

		path, ok := located.Path()
		require.True(t, ok)
		assert.Equal(t, value.String(), path.String())

		got, ok := errors.AsType[*schema.Violation](located.Cause())
		require.True(t, ok)
		assert.Equal(t, "anyOf", got.Keyword)

		forms := located.Errors()
		require.Len(t, forms, 2)

		for i, form := range forms {
			var unlocated *niceyaml.Error

			require.ErrorAs(t, form, &unlocated)
			require.EqualError(t, unlocated, []string{"form 1", "form 2"}[i])

			_, ok := unlocated.Path()
			assert.False(t, ok)
			assert.Len(t, unlocated.Errors(), 1)
		}
	})
}

func TestViolation_Error(t *testing.T) {
	t.Parallel()

	v := &schema.Violation{Keyword: "type", SchemaPath: "/type", Message: "bad type"}
	require.EqualError(t, v, "bad type")

	var nilViolation *schema.Violation

	assert.Empty(t, nilViolation.Error())
}
