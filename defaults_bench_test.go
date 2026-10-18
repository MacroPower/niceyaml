package niceyaml_test

import (
	"fmt"
	"strings"
	"testing"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
)

// BenchmarkWithDefaults_ReferenceMergeInEachDocument decodes each
// document of a Source of many documents with defaults. Each document is
// a mapping whose `<<` merge key reads one anchor of 200 keys of a
// reference document. The default sets a field whose entry the anchor
// brings in. So the fill of the defaults looks that key up through the
// anchor, and it keeps the value of the merge key. The reference Source
// keeps the keys of the anchor once for every document that reads it. So
// the heap that the decodes leave live grows with the documents alone.
// The go-yaml decoder reads each key of the anchor for each document, and
// the time per document stays flat as the Source grows. The metric
// retained-B is the heap that one decode of each document of a new Source
// leaves live while the Source lives.
func BenchmarkWithDefaults_ReferenceMergeInEachDocument(b *testing.B) {
	type config struct {
		X struct {
			K0 int `yaml:"k0"`
		} `yaml:"x"`
	}

	ref := yamltest.References("u: &u\n" + yamltest.GenerateLines(200, "  k%d: 1"))
	defaults := niceyaml.WithDefaults(func(c *config) { c.X.K0 = 7 })

	decode := func(docs []*niceyaml.Node) {
		for _, doc := range docs {
			got, err := doc.Decode[config](b.Context(), defaults)
			if err != nil || got.X.K0 != 1 {
				b.Fatalf("got %+v and %v, want the value of the merge key", got, err)
			}
		}
	}

	for _, count := range []int{1000, 4000, 16000} {
		input := strings.Repeat("---\nx: {<<: *u}\n", count)
		newDocs := func() []*niceyaml.Node {
			return yamltest.Documents(b, niceyaml.NewSourceFromString(input, yamltest.NoSizeLimits(ref)...), count)
		}

		b.Run(fmt.Sprintf("documents_%d", count), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				b.StopTimer()

				docs := newDocs()

				b.StartTimer()
				decode(docs)
			}

			yamltest.ReportTimePer(b, count, "document")
			reportRetained(b, newDocs, decode)
		})
	}
}
