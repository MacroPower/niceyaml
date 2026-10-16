package line_test

import (
	"testing"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/position"
)

// benchSize names a run of a benchmark over content of a number of lines.
type benchSize struct {
	name  string
	lines int
}

// benchSizes are the lengths of content that most benchmarks of the
// package run at.
var benchSizes = []benchSize{
	{"small_50", 50},
	{"medium_500", 500},
	{"large_5000", 5000},
}

func BenchmarkLinesContentRanges(b *testing.B) {
	for _, sz := range benchSizes {
		lines := newLines(yamltest.GenerateYAML(sz.lines))
		tks := lines.Tokens()

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				for _, tk := range tks {
					_ = lines.ContentRanges(tk)
				}
			}
		})
	}
}

func BenchmarkLinesRunesIn(b *testing.B) {
	for _, sz := range benchSizes {
		lines := newLines(yamltest.GenerateYAML(sz.lines))

		// One short range per line, as a search yields a match on each.
		ranges := make([]position.Range, 0, lines.Len())
		for i := range lines.Len() {
			ranges = append(ranges, yamltest.LineRange(i, 0, 3))
		}

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				for range lines.RunesIn(ranges...) {
				}
			}
		})
	}
}
