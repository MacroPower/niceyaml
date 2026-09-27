package niceyaml_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

// generateNestedYAML creates YAML nested to the given depth, where each
// level branches into itemsPerLevel keys.
func generateNestedYAML(depth, itemsPerLevel int) string {
	var sb strings.Builder

	var writeLevel func(level int)

	writeLevel = func(level int) {
		indent := strings.Repeat("  ", level)
		for i := range itemsPerLevel {
			if level < depth-1 {
				fmt.Fprintf(&sb, "%slevel%d_item%d:\n", indent, level, i)
				writeLevel(level + 1)
			} else {
				fmt.Fprintf(&sb, "%skey_%d: value_%d\n", indent, i, i)
			}
		}
	}

	sb.WriteString("root:\n")
	writeLevel(1)

	return sb.String()
}

func BenchmarkNewSourceFromString(b *testing.B) {
	sizes := []struct {
		name  string
		lines int
	}{
		{"small_50", 50},
		{"medium_500", 500},
		{"large_5000", 5000},
		{"xlarge_50000", 50000},
	}

	for _, sz := range sizes {
		yaml := yamltest.GenerateYAML(sz.lines)
		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(yaml)))

			for b.Loop() {
				_ = niceyaml.NewSourceFromString(yaml)
			}
		})
	}
}

func BenchmarkNewSourceFromString_Nested(b *testing.B) {
	sizes := []struct {
		name          string
		depth         int
		itemsPerLevel int
	}{
		{"shallow_wide", 2, 100},
		{"deep_narrow", 10, 5},
		{"balanced", 5, 20},
	}

	for _, sz := range sizes {
		yaml := generateNestedYAML(sz.depth, sz.itemsPerLevel)
		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(yaml)))

			for b.Loop() {
				_ = niceyaml.NewSourceFromString(yaml)
			}
		})
	}
}

func BenchmarkNode_DecodeScoped(b *testing.B) {
	type item struct {
		Name  string `yaml:"name"`
		Value int    `yaml:"value"`
	}

	// Each entry of a merge layout takes its value from the defaults
	// through a `<<` merge key.
	sizes := []struct {
		name  string
		items int
		merge bool
	}{
		{"items_100", 100, false},
		{"items_1000", 1000, false},
		{"merge_items_100", 100, true},
		{"merge_items_1000", 1000, true},
		{"merge_items_4000", 4000, true},
	}

	for _, sz := range sizes {
		var sb strings.Builder

		if sz.merge {
			sb.WriteString("defaults: &d\n  value: 1\nitems:\n")
		} else {
			sb.WriteString("items:\n")
		}

		for i := range sz.items {
			if sz.merge {
				fmt.Fprintf(&sb, "  - <<: *d\n    name: item_%d\n", i)
			} else {
				fmt.Fprintf(&sb, "  - name: item_%d\n    value: %d\n", i, i)
			}
		}

		doc, err := niceyaml.NewSourceFromString(sb.String()).Document()
		require.NoError(b, err)

		items, err := doc.Nodes(paths.Root().Child("items").IndexAll())
		require.NoError(b, err)

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				for _, it := range items {
					_, err := it.Decode[item](b.Context())
					if err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}

func BenchmarkNode_DecodeRejectedStream(b *testing.B) {
	sizes := []struct {
		name string
		docs int
	}{
		{"docs_1000", 1000},
		{"docs_10000", 10000},
	}

	for _, sz := range sizes {
		var sb strings.Builder

		for i := range sz.docs {
			fmt.Fprintf(&sb, "---\na: x%d\nb: y\nc: z\n", i)
		}

		docs, err := niceyaml.NewSourceFromString(sb.String()).Documents()
		require.NoError(b, err)

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				for _, doc := range docs {
					var v struct{ A int }

					err := doc.DecodeInto(b.Context(), &v)
					if !errors.Is(err, niceyaml.ErrDecodeRejected) {
						b.Fatalf("got %v, want a rejection", err)
					}
				}
			}

			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*sz.docs), "ns/doc")
		})
	}
}

func BenchmarkNode_Nodes(b *testing.B) {
	sizes := []struct {
		name  string
		items int
	}{
		{"items_100", 100},
		{"items_1000", 1000},
		{"items_10000", 10000},
	}

	path := paths.Root().Child("items").IndexAll()

	for _, sz := range sizes {
		var sb strings.Builder

		sb.WriteString("items:\n")

		for i := range sz.items {
			fmt.Fprintf(&sb, "  - name: item_%d\n    value: %d\n", i, i)
		}

		doc, err := niceyaml.NewSourceFromString(sb.String()).Document()
		require.NoError(b, err)

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				items, err := doc.Nodes(path)
				if err != nil {
					b.Fatal(err)
				}

				if len(items) != sz.items {
					b.Fatalf("got %d nodes, want %d", len(items), sz.items)
				}
			}
		})
	}
}

func BenchmarkNode_BindManyPaths(b *testing.B) {
	sizes := []struct {
		name string
		keys int
	}{
		{"keys_500", 500},
		{"keys_2000", 2000},
		{"keys_5000", 5000},
	}

	for _, sz := range sizes {
		var sb strings.Builder

		errs := make([]error, 0, sz.keys)

		for i := range sz.keys {
			fmt.Fprintf(&sb, "k%d: v%d\n", i, i)

			errs = append(errs, niceyaml.NewError("bad value",
				niceyaml.AtPath(paths.Root().Child(fmt.Sprintf("k%d", i)))))
		}

		doc, err := niceyaml.NewSourceFromString(sb.String()).Document()
		require.NoError(b, err)

		joined := errors.Join(errs...)

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_ = doc.Bind(joined).Error()
			}
		})
	}
}

func BenchmarkSourceRunes(b *testing.B) {
	sizes := []struct {
		name  string
		lines int
	}{
		{"small_50", 50},
		{"medium_500", 500},
		{"large_5000", 5000},
	}

	for _, sz := range sizes {
		yaml := yamltest.GenerateYAML(sz.lines)
		lines := niceyaml.NewSourceFromString(yaml).Lines()

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(yaml)))
			b.ResetTimer()

			for b.Loop() {
				count := 0
				for range lines.Runes() {
					count++
				}

				_ = count
			}
		})
	}
}

func BenchmarkSourceLines(b *testing.B) {
	sizes := []struct {
		name  string
		lines int
	}{
		{"small_50", 50},
		{"medium_500", 500},
		{"large_5000", 5000},
	}

	for _, sz := range sizes {
		yaml := yamltest.GenerateYAML(sz.lines)
		lines := niceyaml.NewSourceFromString(yaml).Lines()

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(yaml)))
			b.ResetTimer()

			for b.Loop() {
				count := 0
				for range lines.All() {
					count++
				}

				_ = count
			}
		})
	}
}

func BenchmarkSourceLen(b *testing.B) {
	yaml := yamltest.GenerateYAML(5000)
	lines := niceyaml.NewSourceFromString(yaml).Lines()

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_ = lines.Len()
	}
}

func BenchmarkSourceContent(b *testing.B) {
	sizes := []struct {
		name  string
		lines int
	}{
		{"small_50", 50},
		{"medium_500", 500},
		{"large_5000", 5000},
	}

	for _, sz := range sizes {
		yaml := yamltest.GenerateYAML(sz.lines)
		source := niceyaml.NewSourceFromString(yaml)

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(yaml)))
			b.ResetTimer()

			for b.Loop() {
				_ = source.Lines().Content()
			}
		})
	}
}
