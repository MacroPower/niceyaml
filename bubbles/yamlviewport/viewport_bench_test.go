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

func BenchmarkViewport_SideBySideView(b *testing.B) {
	for _, n := range []int{1_000, 100_000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			var before, after strings.Builder

			for i := range n {
				fmt.Fprintf(&before, "k%d: v%d\n", i, i)
				fmt.Fprintf(&after, "k%d: w%d\n", i, i)
			}

			m := yamlviewport.New()
			m.SetWidth(120)
			m.SetHeight(40)
			m.SetViewMode(yamlviewport.ViewModeSideBySide)
			m.AddRevision(niceyaml.NewSourceFromString(before.String(), niceyaml.WithName("v1")))
			m.AddRevision(niceyaml.NewSourceFromString(after.String(), niceyaml.WithName("v2")))
			m.SetSearchTerm("v1")

			_ = m.View()

			b.ReportAllocs()

			for b.Loop() {
				_ = m.View()
			}
		})
	}
}
