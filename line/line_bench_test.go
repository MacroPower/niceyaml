package line_test

import (
	"fmt"
	"testing"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/line"
)

// The tokens of the line do not have positions, so they share one offset
// and a lookup reads them in the order of the line. Each token equals a
// token of the first item, so the lookup stops there. The time for each
// token thus stays level as the line grows.
func BenchmarkLineContentSpan_NoPositions(b *testing.B) {
	for _, items := range oneLineItems {
		tks := oneLine(items).Tokens()
		for i, tk := range tks {
			tks[i] = tk.Clone()
			tks[i].Position = nil
		}

		l := line.NewLines(tks).Line(0)

		b.Run(fmt.Sprintf("items_%d", items), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				for _, tk := range tks {
					if _, ok := l.ContentSpan(tk); !ok {
						b.Fatal("no span")
					}
				}
			}

			yamltest.ReportTimePer(b, len(tks), "token")
		})
	}
}
