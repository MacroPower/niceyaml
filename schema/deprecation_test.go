package schema_test

import (
	"cmp"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/schema"
)

// cafeSchema marks fields and values of a cafe deprecated in each way the
// tests of deprecations read.
const cafeSchema = `{
  "type": "object",
  "properties": {
    "legacyPricing": {"deprecated": true, "deprecationMessage": "legacyPricing has no effect. Remove it."},
    "notes": {"deprecated": true, "description": "Notes.\n\nDeprecated: keep notes in the wiki."},
    "seats": {"type": "integer", "minimum": 1},
    "wifi": {
      "type": "object",
      "properties": {
        "security": {
          "$ref": "#/$defs/security",
          "if": {"const": "wep"},
          "then": {"deprecated": true, "deprecationMessage": "WEP is not secure. Set security to wpa3."}
        }
      }
    },
    "printer": {"$ref": "#/$defs/printer"},
    "tags": {"type": "array", "items": {"enum": ["seasonal", "vegan", "old"], "if": {"const": "old"}, "then": {"deprecated": true}}},
    "dishes": {"type": "object", "additionalProperties": {"$ref": "#/$defs/dish"}},
    "milks": {
      "type": "object",
      "propertyNames": {"anyOf": [{"pattern": "^[a-z]+$"}, {"pattern": "^[A-Z]", "deprecated": true, "deprecationMessage": "write the milk in lowercase"}]}
    },
    "sizes": {"anyOf": [{"type": "string", "deprecated": true}, {"type": "integer"}]}
  },
  "$defs": {
    "security": {"deprecated": true, "deprecationMessage": "cafe 5.0 replaces security with wifi.mode."},
    "printer": {"type": "object", "deprecated": true, "deprecationMessage": "cafe 5.0 drops printers."},
    "dish": {"type": "object", "properties": {"cost": {"deprecated": true, "deprecationMessage": "write price"}, "price": {"type": "integer"}}}
  }
}`

// warningRows returns the text of each problem of err on a line, with the
// text of each of its details on a line below it, indented.
func warningRows(err error) string {
	var rows []string

	for problem := range niceyaml.Problems(err) {
		rows = append(rows, problem.Err.Error())

		for _, detail := range problem.Children {
			if detail.Detail {
				rows = append(rows, "  "+detail.Message())
			}
		}
	}

	return stringtest.JoinLF(rows...)
}

func TestSchema_Check_Deprecations(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		schema string
		input  string
		want   string
	}{
		"field": {
			input: "legacyPricing: true\n",
			want: stringtest.JoinLF(
				`cafe.yaml:1:1: warning: $.legacyPricing~: deprecated field "legacyPricing"`,
				"  legacyPricing has no effect. Remove it.",
			),
		},
		"field without a message": {
			input: "notes: call the roaster\n",
			want:  `cafe.yaml:1:1: warning: $.notes~: deprecated field "notes"`,
		},
		"scalar value": {
			input: stringtest.LinesLF("wifi:", "  security: wpa3"),
			want: stringtest.JoinLF(
				`cafe.yaml:2:13: warning: $.wifi.security: deprecated value "wpa3"`,
				"  cafe 5.0 replaces security with wifi.mode.",
			),
		},
		"two schemas at one value": {
			input: stringtest.LinesLF("wifi:", "  security: wep"),
			want: stringtest.JoinLF(
				`cafe.yaml:2:13: warning: $.wifi.security: deprecated value "wep"`,
				"  cafe 5.0 replaces security with wifi.mode.",
				"  WEP is not secure. Set security to wpa3.",
			),
		},
		"field of a definition": {
			input: stringtest.LinesLF(
				"dishes:",
				"  semla:",
				"    cost: 45",
			),
			want: stringtest.JoinLF(
				`cafe.yaml:3:5: warning: $.dishes.semla.cost~: deprecated field "cost"`,
				"  write price",
			),
		},
		"each item": {
			input: "tags: [old, vegan, old]\n",
			want: stringtest.JoinLF(
				`cafe.yaml:1:8: warning: $.tags[0]: deprecated value "old"`,
				`cafe.yaml:1:20: warning: $.tags[2]: deprecated value "old"`,
			),
		},
		"key under propertyNames": {
			input: stringtest.LinesLF(
				"milks:",
				"  Oat: 1",
				"  soy: 2",
			),
			want: stringtest.JoinLF(
				`cafe.yaml:2:3: warning: $.milks.Oat~: deprecated field "Oat"`,
				"  write the milk in lowercase",
			),
		},
		"branch that the value fails": {
			input: "sizes: 3\n",
		},
		"branch that the value passes": {
			input: "sizes: large\n",
			want:  `cafe.yaml:1:8: warning: $.sizes: deprecated value "large"`,
		},
		"mapping": {
			input: stringtest.LinesLF("printer:", "  host: kitchen"),
			want: stringtest.JoinLF(
				`cafe.yaml:1:1: warning: $.printer: deprecated value`,
				"  cafe 5.0 drops printers.",
			),
		},
		"alias": {
			input: stringtest.LinesLF(
				"dishes:",
				"  semla: &dish",
				"    cost: 45",
				"  bulle: *dish",
			),
			want: stringtest.JoinLF(
				`cafe.yaml:3:5: warning: $.dishes.semla.cost~: deprecated field "cost"`,
				"  write price",
			),
		},
		"merge key": {
			input: stringtest.LinesLF(
				"dishes:",
				"  semla: &dish",
				"    cost: 45",
				"  bulle:",
				"    <<: *dish",
				"    price: 40",
			),
			want: stringtest.JoinLF(
				`cafe.yaml:3:5: warning: $.dishes.semla.cost~: deprecated field "cost"`,
				"  write price",
			),
		},
		// Two warnings with one message stay apart, because each binds at
		// its own key.
		"one message at two keys": {
			input: stringtest.LinesLF(
				"dishes:",
				"  semla:",
				"    cost: 45",
				"  bulle:",
				"    cost: 40",
			),
			want: stringtest.JoinLF(
				`cafe.yaml:3:5: warning: $.dishes.semla.cost~: deprecated field "cost"`,
				"  write price",
				`cafe.yaml:5:5: warning: $.dishes.bulle.cost~: deprecated field "cost"`,
				"  write price",
			),
		},
		"root": {
			schema: `{"deprecated": true, "deprecationMessage": "cafe 5.0 reads cafe.toml"}`,
			input:  "seats: 40\n",
			want: stringtest.JoinLF(
				`cafe.yaml:1:1: warning: $: deprecated value`,
				"  cafe 5.0 reads cafe.toml",
			),
		},
		"no deprecation": {
			input: "seats: 40\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s := compileSchema(t, cmp.Or(tc.schema, cafeSchema))
			doc := yamltest.FirstDocument(t, tc.input, niceyaml.WithName("cafe.yaml"))
			warnings := niceyaml.NewWarnings()

			require.NoError(t, doc.Validate(warnings.Context(t.Context()), s))

			warned := warnings.Err()
			if tc.want == "" {
				assert.NoError(t, warned)

				return
			}

			require.Error(t, warned)
			assert.Equal(t, tc.want, warningRows(warned))
			assert.False(t, niceyaml.HasErrors(warned))
			assert.True(t, niceyaml.IsInvalid(warned))

			for problem := range niceyaml.Problems(warned) {
				require.ErrorIs(t, problem.Err, schema.ErrDeprecated)
				assert.True(t, problem.Warning)
			}
		})
	}
}

func TestSchema_Check_DeprecationsOfAFailure(t *testing.T) {
	t.Parallel()

	s := compileSchema(t, cafeSchema)
	doc := yamltest.FirstDocument(
		t,
		stringtest.LinesLF("legacyPricing: true", "seats: 0"),
		niceyaml.WithName("cafe.yaml"),
	)
	warnings := niceyaml.NewWarnings()

	// The check returns its violations alone and hands its warnings over.
	err := doc.Validate(warnings.Context(t.Context()), s)
	require.EqualError(t, err, "cafe.yaml:2:8: $.seats: 0 is less than 1")
	require.NotErrorIs(t, err, schema.ErrDeprecated)
	require.ErrorIs(t, warnings.Err(), schema.ErrDeprecated)

	assert.Equal(t, stringtest.JoinLF(
		"cafe.yaml: 1 error, 1 warning",
		`cafe.yaml:1:1: warning: $.legacyPricing~: deprecated field "legacyPricing"`,
		"cafe.yaml:2:8: $.seats: 0 is less than 1",
	), warnings.Report(err).Error())

	// A validator that tries a second schema after the first fails still
	// hands over the warnings of the first.
	plain := compileSchema(t, `{"type": "object"}`)
	fallback := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
		err := s.Check(ctx, n)
		if err != nil {
			return plain.Check(ctx, n)
		}

		return nil
	})

	warnings = niceyaml.NewWarnings()
	require.NoError(t, doc.Validate(warnings.Context(t.Context()), fallback))
	assert.Equal(t, `cafe.yaml:1:1: warning: $.legacyPricing~: deprecated field "legacyPricing"`,
		warnings.Err().Error())
}

func TestSchema_ValidateValue_Deprecations(t *testing.T) {
	t.Parallel()

	s := compileSchema(t, cafeSchema)

	t.Run("a value that passes hands its warnings over", func(t *testing.T) {
		t.Parallel()

		warnings := niceyaml.NewWarnings()

		require.NoError(t, s.ValidateValue(warnings.Context(t.Context()), map[string]any{
			"legacyPricing": true,
			"tags":          []any{"old"},
			"wifi":          map[string]any{"security": "wep"},
		}))
		assert.Equal(t, stringtest.JoinLF(
			"3 warnings",
			`warning: $.legacyPricing~: deprecated field "legacyPricing"`,
			`warning: $.tags[0]: deprecated value "old"`,
			`warning: $.wifi.security: deprecated value "wep"`,
		), warnings.Err().Error())
		require.ErrorIs(t, warnings.Err(), schema.ErrDeprecated)
	})

	t.Run("a value that fails hands its warnings over and returns its violations", func(t *testing.T) {
		t.Parallel()

		warnings := niceyaml.NewWarnings()

		err := s.ValidateValue(warnings.Context(t.Context()), map[string]any{"legacyPricing": true, "seats": 0})
		require.EqualError(t, err, "$.seats: 0 is less than 1")
		require.NotErrorIs(t, err, schema.ErrDeprecated)
		assert.Equal(t, `warning: $.legacyPricing~: deprecated field "legacyPricing"`, warnings.Err().Error())
		assert.Equal(t, stringtest.JoinLF(
			"1 error, 1 warning",
			"$.seats: 0 is less than 1",
			`warning: $.legacyPricing~: deprecated field "legacyPricing"`,
		), warnings.Report(err).Error())
	})

	t.Run("a value from no document quotes its JSON text", func(t *testing.T) {
		t.Parallel()

		warnings := niceyaml.NewWarnings()

		require.NoError(t, s.ValidateValue(warnings.Context(t.Context()),
			map[string]any{"sizes": "large", "printer": map[string]any{}}))
		assert.Equal(t, stringtest.JoinLF(
			"2 warnings",
			`warning: $.printer: deprecated value`,
			`warning: $.sizes: deprecated value "large"`,
		), warnings.Err().Error())
	})
}
