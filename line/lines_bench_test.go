package line_test

import (
	"testing"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/tokens"
)

func BenchmarkLinesContentRanges(b *testing.B) {
	sizes := []struct {
		name  string
		lines int
	}{
		{"small_50", 50},
		{"medium_500", 500},
		{"large_5000", 5000},
	}

	for _, sz := range sizes {
		tks := tokens.Tokenize(yamltest.GenerateYAML(sz.lines))
		lines := line.NewLines(tks)

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

func BenchmarkLinesRunes(b *testing.B) {
	sizes := []struct {
		name  string
		lines int
	}{
		{"small_50", 50},
		{"medium_500", 500},
		{"large_5000", 5000},
	}

	for _, sz := range sizes {
		lines := line.NewLines(tokens.Tokenize(yamltest.GenerateYAML(sz.lines)))

		// One short range per line, as a search yields a match on each.
		ranges := make([]position.Range, 0, lines.Len())
		for i := range lines.Len() {
			ranges = append(ranges, position.NewRange(position.New(i, 0), position.New(i, 3)))
		}

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				for range lines.Runes(ranges...) {
				}
			}
		})
	}
}
