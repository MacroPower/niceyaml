package yamlviewport_test

import (
	"fmt"
	"testing"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/bubbles/yamlviewport"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/style/theme"
)

// benchmarkSource returns a source of n lines that each hold one match of
// the term "name".
func benchmarkSource(n int) *niceyaml.Source {
	return niceyaml.NewSourceFromString(numbered(n, "key%[1]d: value %[1]d name foo"))
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

func BenchmarkViewport_SetSearchTerm(b *testing.B) {
	// Typing a word that one line at the end holds, so each term restyles
	// that line alone.
	terms := []string{"z", "ze", "zeb", "zebr", "zebra"}

	for _, n := range []int{1_000, 20_000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			src := numbered(n, "key%[1]d: value %[1]d") + "zebra: here\n"

			m := yamlviewport.New(
				yamlviewport.WithPrinter(printer.New(printer.WithStyles(theme.Charm.Styles()))),
			)
			m.SetWidth(120)
			m.SetHeight(40)
			m.SetRevision(niceyaml.NewSourceFromString(src))

			_ = m.View()

			b.ReportAllocs()

			i := 0
			for b.Loop() {
				m.SetSearchTerm(terms[i%len(terms)])

				_ = m.View()

				i++
			}
		})
	}
}

func BenchmarkViewport_SideBySideView(b *testing.B) {
	for _, n := range []int{1_000, 100_000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			m := yamlviewport.New()
			m.SetWidth(120)
			m.SetHeight(40)
			m.SetViewMode(yamlviewport.ViewModeSideBySide)
			addRevisions(&m, numbered(n, "k%[1]d: v%[1]d"), numbered(n, "k%[1]d: w%[1]d"))
			m.SetSearchTerm("v1")

			_ = m.View()

			b.ReportAllocs()

			for b.Loop() {
				_ = m.View()
			}
		})
	}
}
