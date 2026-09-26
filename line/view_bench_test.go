package line_test

import (
	"testing"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/line"
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
