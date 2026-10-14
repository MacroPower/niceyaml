package paths_test

import (
	"fmt"
	"slices"
	"testing"

	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

// generateItemsYAML creates a sequence of count items, each a mapping with
// a nested mapping below it.
func generateItemsYAML(count int) string {
	return "items:\n" + yamltest.GenerateLines(count, stringtest.JoinLF(
		"  - name: item_%[1]d",
		"    spec:",
		"      value: %[1]d",
	))
}

func BenchmarkResolver_Nodes_Recursive(b *testing.B) {
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
		doc := documentOf(b, in.yaml, yamltest.NoSizeLimits()...)

		r := paths.NewResolver(doc)

		b.Run(in.name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_, err := r.Nodes(path)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkResolver_Nodes_RecursiveChained(b *testing.B) {
	depths := []int{100, 200, 400, 3000}

	// The second ..a reaches each entry below the first from every
	// enclosing match of the first.
	path := paths.Doc().Recursive("a").Recursive("a")

	for _, depth := range depths {
		doc := documentOf(b, nestedChain(depth), yamltest.NoSizeLimits()...)

		r := paths.NewResolver(doc)

		b.Run(fmt.Sprintf("depth_%d", depth), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				nodes, err := r.Nodes(path)
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
		doc := documentOf(b, yamltest.GenerateYAML(keys), yamltest.NoSizeLimits()...)

		ps := make([]paths.Path, keys)
		for i := range keys {
			ps[i] = paths.Doc().Child(fmt.Sprintf("key_%d", i))
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

			yamltest.ReportTimePer(b, keys, "key")
		})
	}
}

// BenchmarkResolver_Node_DeepPath resolves the innermost value of a chain
// of nested mappings through one Resolver. The time per selector should
// stay flat as the chain grows.
func BenchmarkResolver_Node_DeepPath(b *testing.B) {
	for _, depth := range []int{1000, 2000, 4000} {
		doc := documentOf(b, nestedChain(depth), yamltest.NoSizeLimits()...)

		r := paths.NewResolver(doc)
		path := paths.Doc().Child(slices.Repeat([]string{"a"}, depth)...)

		b.Run(fmt.Sprintf("depth_%d", depth), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_, err := r.Node(path)
				if err != nil {
					b.Fatal(err)
				}
			}

			yamltest.ReportTimePer(b, depth, "selector")
		})
	}
}

// BenchmarkNewResolver_NestedOpenMerges binds the aliases of a chain of
// merges inside an open anchored mapping, where each bN merges the two
// mappings before it. The time per link should stay flat as the chain
// grows.
func BenchmarkNewResolver_NestedOpenMerges(b *testing.B) {
	for _, links := range []int{1000, 2000, 4000, 8000} {
		doc := documentOf(b, openMerges(links), yamltest.NoSizeLimits()...)

		b.Run(fmt.Sprintf("links_%d", links), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				paths.NewResolver(doc)
			}

			yamltest.ReportTimePer(b, links, "link")
		})
	}
}

// BenchmarkResolver_Matches_MissingMergeFirst walks a mapping whose keys come
// before a merge list that starts with an alias that does not resolve and
// then lists as many aliases as the mapping has keys. The lookup of each
// key stops at that alias, so the time per key should stay flat as the
// mapping grows.
func BenchmarkResolver_Matches_MissingMergeFirst(b *testing.B) {
	path := paths.Doc().Recursive("nope")

	for _, keys := range []int{8000, 16000, 32000, 64000} {
		doc := documentOf(b, missingFirst(keys), yamltest.NoSizeLimits()...)

		r := paths.NewResolver(doc)

		b.Run(fmt.Sprintf("keys_%d", keys), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_, err := r.Matches(path)
				if err != nil {
					b.Fatal(err)
				}
			}

			yamltest.ReportTimePer(b, keys, "key")
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
		doc := documentOf(b, mergedRecords(records, 10, 19), yamltest.NoSizeLimits()...)

		resolver := paths.NewResolver(doc)

		b.Run(fmt.Sprintf("records_%d", records), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_, err := resolver.Matches(path)
				if err != nil {
					b.Fatal(err)
				}
			}

			yamltest.ReportTimePer(b, records, "record")
		})
	}
}
