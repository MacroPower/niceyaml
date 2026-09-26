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
