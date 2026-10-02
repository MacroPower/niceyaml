package niceyaml_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/position"
)

func BenchmarkNode_PathAt(b *testing.B) {
	sizes := []struct {
		name  string
		lines int
	}{
		{"small_50", 50},
		{"large_5000", 5000},
		{"xlarge_50000", 50000},
	}

	for _, sz := range sizes {
		yaml := yamltest.GenerateYAML(sz.lines)
		last := position.New(sz.lines-1, 0)

		// The first call on a document reads its whole tree.
		b.Run(sz.name+"/first", func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				b.StopTimer()

				doc, err := niceyaml.NewSourceFromString(yaml).Document()
				require.NoError(b, err)

				b.StartTimer()

				_, ok := doc.PathAt(last)
				require.True(b, ok)
			}
		})

		b.Run(sz.name+"/later", func(b *testing.B) {
			doc, err := niceyaml.NewSourceFromString(yaml).Document()
			require.NoError(b, err)

			_, ok := doc.PathAt(last)
			require.True(b, ok)

			b.ReportAllocs()

			i := 0

			for b.Loop() {
				_, ok := doc.PathAt(position.New(i%sz.lines, 0))
				if !ok {
					b.Fatal("no path")
				}

				i++
			}
		})
	}
}
