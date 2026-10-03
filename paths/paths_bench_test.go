package paths_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

// generateItemsYAML creates a sequence of count items, each a mapping with
// a nested mapping below it.
func generateItemsYAML(count int) string {
	var sb strings.Builder

	sb.WriteString("items:\n")

	for i := range count {
		fmt.Fprintf(&sb, "  - name: item_%d\n    spec:\n      value: %d\n", i, i)
	}

	return sb.String()
}

func BenchmarkPath_Nodes_Recursive(b *testing.B) {
	inputs := []struct {
		name string
		yaml string
	}{
		{"flat_5000", yamltest.GenerateYAML(5000)},
		{"nested_1000", generateItemsYAML(1000)},
	}

	// No entry has this key, so the walk visits every node and finds none.
	path := paths.Doc().Recursive("zzz")

	for _, in := range inputs {
		file, err := niceyaml.NewSourceFromString(in.yaml).File()
		require.NoError(b, err)

		doc := file.Docs[0]

		b.Run(in.name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_, err := path.Nodes(doc)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkPath_Nodes_RecursiveChained(b *testing.B) {
	depths := []int{100, 200, 400, 3000}

	// The second ..a reaches each entry below the first from every
	// enclosing match of the first.
	path := paths.Doc().Recursive("a").Recursive("a")

	for _, depth := range depths {
		src := strings.Repeat("{a: ", depth) + "1" + strings.Repeat("}", depth)

		file, err := niceyaml.NewSourceFromString(src).File()
		require.NoError(b, err)

		doc := file.Docs[0]

		b.Run(fmt.Sprintf("depth_%d", depth), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				nodes, err := path.Nodes(doc)
				if err != nil {
					b.Fatal(err)
				}

				if len(nodes) != depth-1 {
					b.Fatalf("got %d nodes, want %d", len(nodes), depth-1)
				}
			}
		})
	}
}

// BenchmarkResolver_Node_WideMapping resolves every key of one wide mapping
// through one Resolver. The time per key should stay flat as the mapping
// grows.
func BenchmarkResolver_Node_WideMapping(b *testing.B) {
	for _, keys := range []int{1000, 4000, 16000} {
		var sb strings.Builder

		for i := range keys {
			fmt.Fprintf(&sb, "k%d: %d\n", i, i)
		}

		file, err := niceyaml.NewSourceFromString(sb.String()).File()
		require.NoError(b, err)

		doc := file.Docs[0]

		ps := make([]paths.Path, keys)
		for i := range keys {
			ps[i] = paths.Doc().Child(fmt.Sprintf("k%d", i))
		}

		b.Run(fmt.Sprintf("keys_%d", keys), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				r := paths.NewResolver(doc)

				for _, p := range ps {
					_, err := r.Node(p)
					if err != nil {
						b.Fatal(err)
					}
				}
			}

			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*keys), "ns/key")
		})
	}
}

// BenchmarkResolver_Node_DeepPath resolves the innermost value of a chain
// of nested mappings through one Resolver. The time per selector should
// stay flat as the chain grows.
func BenchmarkResolver_Node_DeepPath(b *testing.B) {
	for _, depth := range []int{1000, 2000, 4000} {
		src := strings.Repeat("{a: ", depth) + "1" + strings.Repeat("}", depth)

		file, err := niceyaml.NewSourceFromString(src).File()
		require.NoError(b, err)

		r := paths.NewResolver(file.Docs[0])
		path := paths.Doc().Child(slices.Repeat([]string{"a"}, depth)...)

		b.Run(fmt.Sprintf("depth_%d", depth), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_, err := r.Node(path)
				if err != nil {
					b.Fatal(err)
				}
			}

			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*depth), "ns/selector")
		})
	}
}

// BenchmarkNewResolver_NestedOpenMerges binds the aliases of a chain of
// merges inside an open anchored mapping, where each bN merges the two
// mappings before it. The time per link should stay flat as the chain
// grows.
func BenchmarkNewResolver_NestedOpenMerges(b *testing.B) {
	for _, links := range []int{1000, 2000, 4000, 8000} {
		var sb strings.Builder

		sb.WriteString("a: &a\n  b0: &b0 {<<: *a, k: &x one}\n  b1: &b1 {<<: [*b0, *a]}\n")

		for i := 2; i < links; i++ {
			fmt.Fprintf(&sb, "  b%d: &b%d {<<: [*b%d, *b%d]}\n", i, i, i-1, i-2)
		}

		file, err := niceyaml.NewSourceFromString(sb.String()).File()
		require.NoError(b, err)

		doc := file.Docs[0]

		b.Run(fmt.Sprintf("links_%d", links), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				paths.NewResolver(doc)
			}

			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*links), "ns/link")
		})
	}
}

// BenchmarkPath_Matches_MissingMergeFirst walks a mapping whose keys come
// before a merge list that starts with an alias that does not resolve and
// then lists as many aliases as the mapping has keys. The lookup of each
// key stops at that alias, so the time per key should stay flat as the
// mapping grows.
func BenchmarkPath_Matches_MissingMergeFirst(b *testing.B) {
	path := paths.Doc().Recursive("nope")

	for _, keys := range []int{8000, 16000, 32000, 64000} {
		var sb strings.Builder

		sb.WriteString("a: &a {z: 0}\nm:\n")

		for i := range keys {
			fmt.Fprintf(&sb, "  k%d: %d\n", i, i)
		}

		sb.WriteString("  <<: [*missing, " + strings.Join(slices.Repeat([]string{"*a"}, keys), ", ") + "]\n")

		file, err := niceyaml.NewSourceFromString(sb.String()).File()
		require.NoError(b, err)

		doc := file.Docs[0]

		b.Run(fmt.Sprintf("keys_%d", keys), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_, err := path.Matches(doc)
				if err != nil {
					b.Fatal(err)
				}
			}

			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*keys), "ns/key")
		})
	}
}

// BenchmarkResolver_Matches_MergedRecords walks a sequence of records that
// each hold 20 keys and then merge a list of ten aliases. The lookup of
// each key reads that list, so the walk reads the same multiple of the
// nodes of the document however many records it holds. The time per
// record should stay flat as the sequence grows.
func BenchmarkResolver_Matches_MergedRecords(b *testing.B) {
	path := paths.Doc().Recursive("name")

	for _, records := range []int{500, 1000, 2000, 4000} {
		var (
			sb      strings.Builder
			sources []string
		)

		for i := range 10 {
			fmt.Fprintf(&sb, "d%d: &d%d {p%d: 0}\n", i, i, i)

			sources = append(sources, fmt.Sprintf("*d%d", i))
		}

		sb.WriteString("items:\n")

		for i := range records {
			fmt.Fprintf(&sb, "  - name: r%d\n", i)

			for k := range 19 {
				fmt.Fprintf(&sb, "    k%d: %d\n", k, k)
			}

			sb.WriteString("    <<: [" + strings.Join(sources, ", ") + "]\n")
		}

		file, err := niceyaml.NewSourceFromString(sb.String()).File()
		require.NoError(b, err)

		resolver := paths.NewResolver(file.Docs[0])

		b.Run(fmt.Sprintf("records_%d", records), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_, err := resolver.Matches(path)
				if err != nil {
					b.Fatal(err)
				}
			}

			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*records), "ns/record")
		})
	}
}
