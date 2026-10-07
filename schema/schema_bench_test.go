package schema_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/schema"
)

// BenchmarkSchema_Validate checks a list of small mappings against a
// schema that matches it, so the time goes to reading the document and
// to the schema check.
func BenchmarkSchema_Validate(b *testing.B) {
	sizes := []struct {
		name  string
		items int
	}{
		{"items_1000", 1000},
		{"items_4000", 4000},
	}

	s, err := schema.Compile(b.Context(), []byte(`{
		"type": "object",
		"properties": {
			"items": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {
						"name": {"type": "string"},
						"value": {"type": "integer"},
						"tags": {"type": "object"}
					}
				}
			}
		}
	}`))
	require.NoError(b, err)

	for _, sz := range sizes {
		var sb strings.Builder

		sb.WriteString("items:\n")

		for range sz.items {
			sb.WriteString("  - {name: x, value: 1, tags: {a: 1}}\n")
		}

		yaml := sb.String()

		doc, err := niceyaml.NewSourceFromString(yaml).Document()
		require.NoError(b, err)

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(yaml)))

			for b.Loop() {
				err := s.Validate(b.Context(), doc)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkSchema_Validate_AliasedItems decodes each item of a list with
// the schema as its validator, where every item holds an alias. The
// alias count covers the whole document, so each item should reuse the
// count of the first instead of walking the document again.
func BenchmarkSchema_Validate_AliasedItems(b *testing.B) {
	s := schema.MustCompile([]byte(`{"type": "object", "properties": {"name": {"type": "string"}}}`))

	for _, items := range []int{1000, 4000} {
		var sb strings.Builder

		sb.WriteString("tags: &t [a, b]\nitems:\n")

		for i := range items {
			fmt.Fprintf(&sb, "  - {name: n%d, tags: *t}\n", i)
		}

		doc, err := niceyaml.NewSourceFromString(sb.String()).Document()
		require.NoError(b, err)

		nodes, err := doc.Nodes(paths.Current().Child("items").IndexAll())
		require.NoError(b, err)

		validated := niceyaml.WithValidator(s)

		b.Run(fmt.Sprintf("items_%d", items), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				for _, node := range nodes {
					_, err := node.Decode[any](b.Context(), validated)
					if err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}

func BenchmarkSchema_Validate_ManyViolations(b *testing.B) {
	// Every member breaks the schema, so each violation's path steps
	// through the same mapping.
	v := schema.MustCompile([]byte(`{"additionalProperties": {"type": "integer"}}`))

	for _, members := range []int{1000, 4000} {
		var sb strings.Builder

		for i := range members {
			fmt.Fprintf(&sb, "k%d: x\n", i)
		}

		doc, err := niceyaml.NewSourceFromString(sb.String()).Document()
		require.NoError(b, err)

		b.Run(fmt.Sprintf("members_%d", members), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				err := doc.Validate(b.Context(), v)
				if err == nil {
					b.Fatal("want violations")
				}
			}
		})
	}
}

// BenchmarkSchema_Validate_WideMerge breaks the schema at every member
// of a mapping that ends in a merge key with as many sources. The path
// of each violation reads those sources to spell its key, until the
// violations reach the limit on those reads, so the time should grow
// with the document and not with its square.
func BenchmarkSchema_Validate_WideMerge(b *testing.B) {
	v := schema.MustCompile([]byte(`{
		"type": "object",
		"properties": {
			"m": {"additionalProperties": {"type": "integer"}}
		}
	}`))

	for _, members := range []int{1000, 4000} {
		doc, err := niceyaml.NewSourceFromString(wideMerge(members)).Document()
		require.NoError(b, err)

		b.Run(fmt.Sprintf("members_%d", members), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				err := v.Validate(b.Context(), doc)
				if err == nil {
					b.Fatal("want violations")
				}
			}
		})
	}
}
