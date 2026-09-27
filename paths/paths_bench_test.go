package paths_test

import (
	"fmt"
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
	path := paths.Root().Recursive("zzz")

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
	path := paths.Root().Recursive("a").Recursive("a")

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
