package aliasing_test

import (
	"fmt"
	"strings"
	"testing"

	"go.jacobcolvin.com/niceyaml/internal/aliasing"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
)

// BenchmarkCheckDecode_TaggedAliasChain checks documents where each
// anchor holds a tag over an alias to the anchor before it, such as
// `&s2 !t *s1`, and a sequence lists the last anchor once per anchor.
// The check follows each alias of the chain once, however many aliases
// lead into it, so the time per alias stays flat as the chain grows.
func BenchmarkCheckDecode_TaggedAliasChain(b *testing.B) {
	sizes := []struct {
		name    string
		anchors int
	}{
		{"anchors_2000", 2000},
		{"anchors_8000", 8000},
	}

	for _, sz := range sizes {
		var sb strings.Builder

		sb.WriteString("s0: &s0 x\n")

		for i := 1; i <= sz.anchors; i++ {
			fmt.Fprintf(&sb, "s%d: &s%d !t *s%d\n", i, i, i-1)
		}

		fmt.Fprintf(&sb, "l: %s\n", yamltest.FlowSequence(fmt.Sprintf("*s%d", sz.anchors), sz.anchors))

		input := sb.String()

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				// A document keeps its count, so each iteration parses the
				// input anew, outside the timer.
				b.StopTimer()

				doc := yamltest.FirstDocument(b, input)

				b.StartTimer()

				err := aliasing.CheckDecode(doc)
				if err != nil {
					b.Fatal(err)
				}
			}

			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*2*sz.anchors), "ns/alias")
		})
	}
}

// BenchmarkCheckDecode_NestedAnchors checks documents that nest anchors
// many levels deep and list as many aliases at the bottom, each to one of
// the anchors around it. The decoder reads such an alias as null, and the
// check finds that in one walk, so the time per alias stays flat as the
// document deepens.
func BenchmarkCheckDecode_NestedAnchors(b *testing.B) {
	for _, depth := range []int{1000, 3000} {
		var sb strings.Builder

		for i := range depth {
			fmt.Fprintf(&sb, "{a: &a%d ", i)
		}

		aliases := make([]string, depth)
		for i := range aliases {
			aliases[i] = fmt.Sprintf("*a%d", i)
		}

		fmt.Fprintf(&sb, "[%s]", strings.Join(aliases, ", "))
		sb.WriteString(strings.Repeat("}", depth))

		input := sb.String()

		b.Run(fmt.Sprintf("anchors_%d", depth), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				// A document keeps its count, so each iteration parses the
				// input anew, outside the timer.
				b.StopTimer()

				doc := yamltest.FirstDocument(b, input)

				b.StartTimer()

				err := aliasing.CheckDecode(doc)
				if err != nil {
					b.Fatal(err)
				}
			}

			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*depth), "ns/alias")
		})
	}
}
