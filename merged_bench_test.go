package niceyaml_test

import (
	"fmt"
	"testing"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
)

// BenchmarkNewSourceFromLayers merges layers of a document that holds
// 2000 keys, where each layer sets every key, so the merge replaces each
// value once per layer above the first.
func BenchmarkNewSourceFromLayers(b *testing.B) {
	for _, count := range []int{2, 8} {
		layers := make([]*niceyaml.Source, count)
		for i := range layers {
			layers[i] = niceyaml.NewSourceFromString(
				yamltest.GenerateLines(2000, fmt.Sprintf("k%%[1]d: {name: n%%[1]d, layer: %d}", i)),
				niceyaml.WithName(fmt.Sprintf("layer%d.yaml", i)),
			)
		}

		b.Run(fmt.Sprintf("layers_%d", count), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_, err := niceyaml.NewSourceFromLayers(b.Context(), layers)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
