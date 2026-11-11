package line_test

import (
	"fmt"
	"testing"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
)

// benchSize names a run of a benchmark over content of a number of lines.
type benchSize struct {
	name  string
	lines int
}

var (
	// The lengths of content that most benchmarks of the package run at.
	benchSizes = []benchSize{
		{"small_50", 50},
		{"medium_500", 500},
		{"large_5000", 5000},
	}

	// The numbers of items that the benchmarks of a document on a single
	// line run at. Each item is a flow mapping of two keys.
	oneLineItems = []int{100, 1000, 10000}
)

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

// oneLine returns the lines of a flow sequence of the given number of
// items, all on one line, as a minified JSON document holds them.
func oneLine(items int) line.Lines {
	return newLines(yamltest.FlowSequence(`{"id": 0, "name": "u"}`, items) + "\n")
}

// The time for each token stays level as the line grows when a lookup
// does not walk the line.
func BenchmarkLinesContentRanges_OneLine(b *testing.B) {
	for _, items := range oneLineItems {
		lines := oneLine(items)
		tks := lines.Tokens()

		b.Run(fmt.Sprintf("items_%d", items), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				for _, tk := range tks {
					_ = lines.ContentRanges(tk)
				}
			}

			yamltest.ReportTimePer(b, len(tks), "token")
		})
	}
}

func BenchmarkLinesTokenAt_OneLine(b *testing.B) {
	for _, items := range oneLineItems {
		lines := oneLine(items)
		width := lines.Line(0).Width()

		b.Run(fmt.Sprintf("items_%d", items), func(b *testing.B) {
			b.ReportAllocs()

			col := 0

			for b.Loop() {
				if lines.TokenAt(position.New(0, col)) == nil {
					b.Fatal("no token")
				}

				col = (col + 7919) % width
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
