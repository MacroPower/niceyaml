package yamlviewport_test

import (
	"fmt"
	"strings"
	"testing"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/bubbles/yamlviewport"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/style/theme"
)

// benchmarkSource returns a source of n lines that each hold one match of
// the term "name".
func benchmarkSource(n int) *niceyaml.Source {
	var src strings.Builder

	for i := range n {
		fmt.Fprintf(&src, "key%d: value %d name foo\n", i, i)
	}

	return niceyaml.NewSourceFromString(src.String())
}

func BenchmarkViewport_SearchNext(b *testing.B) {
	for _, n := range []int{1_000, 20_000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			m := yamlviewport.New(
				yamlviewport.WithPrinter(printer.New(printer.WithStyles(theme.Charm.Styles()))),
			)
			m.SetWidth(120)
			m.SetHeight(40)
			m.SetRevision(benchmarkSource(n))
			m.SetSearchTerm("name")

			_ = m.View()

			b.ReportAllocs()

			for b.Loop() {
				m.SearchNext()

				_ = m.View()
			}
		})
	}
}
