package aliasing_test

import (
	"fmt"
	"strings"
	"testing"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/aliasing"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
)

// BenchmarkReferenceWeights_NestedAnchors checks a document whose aliases
// name each anchor of a reference document that nests anchors around one
// sequence of 20000 items, as in `&n0 [&n1 [&n2 [x, x, x]]]`. Each run
// reads a new reference Source, so the check weighs every anchor. The
// weights keep what they found under each anchor for the anchors around
// it, so the time per item stays flat as the anchors deepen.
func BenchmarkReferenceWeights_NestedAnchors(b *testing.B) {
	const items = 20000

	for _, depth := range []int{10, 30, 90} {
		var ref strings.Builder

		ref.WriteString("root: ")

		aliases := make([]string, depth)
		for i := range aliases {
			aliases[i] = fmt.Sprintf("*n%d", i)

			fmt.Fprintf(&ref, "&n%d [", i)
		}

		ref.WriteString(strings.Repeat("x, ", items-1) + "x" + strings.Repeat("]", depth) + "\n")

		text, input := ref.String(), "v: ["+strings.Join(aliases, ", ")+"]\n"

		b.Run(fmt.Sprintf("anchors_%d", depth), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				// The reference Source keeps its weights and the document
				// keeps its count, so each iteration parses both anew,
				// outside the timer.
				b.StopTimer()

				shared := niceyaml.NewSourceFromString(text, yamltest.NoSizeLimits()...)
				shared.AllDocuments()

				doc := yamltest.FirstDocument(b, input, yamltest.NoSizeLimits(niceyaml.WithReferences(shared))...)

				b.StartTimer()

				// The aliases repeat the sequence once for each anchor, so
				// the verdict depends on the depth.
				aliasing.PastLimit(doc)
			}

			yamltest.ReportTimePer(b, items, "item")
		})
	}
}
