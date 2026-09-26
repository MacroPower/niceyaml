package line_test

import (
	"testing"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/style/kind"
	"go.jacobcolvin.com/niceyaml/tokens"
)

func BenchmarkViewIndex(b *testing.B) {
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

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				view := line.NewView(lines)
				for _, l := range lines.All() {
					_, _ = view.Index(l)
				}
			}
		})
	}
}

func BenchmarkViewHunks(b *testing.B) {
	sizes := []struct {
		name  string
		lines int
	}{
		{"medium_2000", 2000},
		{"large_20000", 20000},
	}

	for _, sz := range sizes {
		lines := line.NewLines(tokens.Tokenize(yamltest.GenerateYAML(sz.lines)))

		// An overlay on every third line gives Hunks(0) one span per
		// overlay, as a search with many matches does.
		view := line.NewView(lines)
		for i := 0; i < lines.Len(); i += 3 {
			view.AddLineOverlay(i, line.Overlay{Cols: position.NewSpan(0, 3), Kind: kind.GenericHighlight})
		}

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_ = view.Hunks(0)
			}
		})
	}
}
