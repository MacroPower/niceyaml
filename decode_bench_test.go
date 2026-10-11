package niceyaml_test

import (
	"testing"

	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
)

// BenchmarkNode_Decode_FirstDecode decodes a new parse of a document in
// each iteration, so it measures the work a document does once, on its
// first decode, which the other benchmarks spread over many decodes. The
// document holds a map and a list of small mappings, and its type
// validates each of them, so self-validation reads the keys of the map.
// The document holds no !!int tag, no reused anchor name, and no alias,
// so its first decode should cost about as much with self-validation as
// without it.
func BenchmarkNode_Decode_FirstDecode(b *testing.B) {
	type config struct {
		M     map[string]item `yaml:"m"`
		Items []item          `yaml:"items"`
	}

	yaml := stringtest.LinesLF(
		"m:",
		"  k: {name: a, price: 1}",
		"items:",
	) + yamltest.GenerateLines(1000, "  - {name: n%[1]d, price: %[1]d}")

	for _, mode := range selfValidationModes {
		b.Run(mode.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(yaml)))

			for b.Loop() {
				b.StopTimer()

				doc := yamltest.FirstDocument(b, yaml)

				b.StartTimer()

				_, err := doc.Decode[config](b.Context(), mode.opts...)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
