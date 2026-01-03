package niceyaml_test

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/macropower/niceyaml"
	"github.com/macropower/niceyaml/position"
)

func BenchmarkPrinterPrint(b *testing.B) {
	sizes := []struct {
		name  string
		lines int
	}{
		{"small_50", 50},
		{"medium_500", 500},
		{"large_5000", 5000},
	}

	for _, sz := range sizes {
		yaml := generateYAML(sz.lines)
		source := niceyaml.NewSourceFromString(yaml)

		b.Run(sz.name, func(b *testing.B) {
			printer := niceyaml.NewPrinter()

			b.ReportAllocs()
			b.SetBytes(int64(len(yaml)))
			b.ResetTimer()

			for b.Loop() {
				_ = printer.Print(source)
			}
		})
	}
}

func BenchmarkPrinterPrint_WithRanges(b *testing.B) {
	rangeCounts := []struct {
		name   string
		ranges int
	}{
		{"0_ranges", 0},
		{"10_ranges", 10},
		{"50_ranges", 50},
		{"100_ranges", 100},
		{"500_ranges", 500},
	}

	yaml := generateYAML(500)
	source := niceyaml.NewSourceFromString(yaml)
	highlightStyle := lipgloss.NewStyle().Background(lipgloss.Color("3"))

	for _, rc := range rangeCounts {
		b.Run(rc.name, func(b *testing.B) {
			printer := niceyaml.NewPrinter()

			// Pre-configure ranges before measurement.
			for i := range rc.ranges {
				lineNum := (i * 5) % source.Count()
				r := position.Range{
					Start: position.New(lineNum, 0),
					End:   position.New(lineNum, 10),
				}
				printer.AddStyleToRange(&highlightStyle, r)
			}

			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				_ = printer.Print(source)
			}
		})
	}
}

func BenchmarkPrinterPrint_WithRanges_IncludingSetup(b *testing.B) {
	rangeCounts := []struct {
		name   string
		ranges int
	}{
		{"0_ranges", 0},
		{"10_ranges", 10},
		{"50_ranges", 50},
		{"100_ranges", 100},
		{"500_ranges", 500},
	}

	yaml := generateYAML(500)
	source := niceyaml.NewSourceFromString(yaml)
	highlightStyle := lipgloss.NewStyle().Background(lipgloss.Color("3"))

	for _, rc := range rangeCounts {
		b.Run(rc.name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				printer := niceyaml.NewPrinter()

				// Distribute ranges across lines.
				for i := range rc.ranges {
					lineNum := (i * 5) % source.Count()
					r := position.Range{
						Start: position.New(lineNum, 0),
						End:   position.New(lineNum, 10),
					}
					printer.AddStyleToRange(&highlightStyle, r)
				}

				_ = printer.Print(source)
			}
		})
	}
}

func BenchmarkPrinterPrint_RangesDensity(b *testing.B) {
	// Tests performance with ranges concentrated on fewer lines vs spread out.
	densities := []struct {
		name       string
		lines      int
		rangesLine int // Ranges per line.
	}{
		{"sparse_1_per_line", 100, 1},
		{"medium_5_per_line", 100, 5},
		{"dense_20_per_line", 100, 20},
	}

	yaml := generateYAML(200)
	source := niceyaml.NewSourceFromString(yaml)
	highlightStyle := lipgloss.NewStyle().Background(lipgloss.Color("3"))

	for _, d := range densities {
		b.Run(d.name, func(b *testing.B) {
			printer := niceyaml.NewPrinter()

			// Pre-configure ranges before measurement.
			for lineNum := range d.lines {
				for r := range d.rangesLine {
					col := r * 5
					rng := position.Range{
						Start: position.New(lineNum, col),
						End:   position.New(lineNum, col+3),
					}
					printer.AddStyleToRange(&highlightStyle, rng)
				}
			}

			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				_ = printer.Print(source)
			}
		})
	}
}

func BenchmarkPrinterPrintSlice(b *testing.B) {
	yaml := generateYAML(5000)
	source := niceyaml.NewSourceFromString(yaml)

	slices := []struct {
		name    string
		minLine int
		maxLine int
	}{
		{"first_50", 0, 49},
		{"middle_50", 2475, 2524},
		{"last_50", 4950, 4999},
	}

	// Approximate bytes per slice (50 lines).
	sliceBytes := int64(len(yaml) / 100)

	for _, sl := range slices {
		b.Run(sl.name, func(b *testing.B) {
			printer := niceyaml.NewPrinter()

			b.ReportAllocs()
			b.SetBytes(sliceBytes)
			b.ResetTimer()

			for b.Loop() {
				_ = printer.PrintSlice(source, sl.minLine, sl.maxLine)
			}
		})
	}
}

func BenchmarkPrinterWithGutter(b *testing.B) {
	gutters := []struct {
		gutter niceyaml.GutterFunc
		name   string
	}{
		{niceyaml.NoGutter, "no_gutter"},
		{niceyaml.DefaultGutter(), "default_gutter"},
		{niceyaml.DiffGutter(), "diff_gutter"},
		{niceyaml.LineNumberGutter(), "line_number_gutter"},
	}

	yaml := generateYAML(500)
	source := niceyaml.NewSourceFromString(yaml)

	for _, g := range gutters {
		b.Run(g.name, func(b *testing.B) {
			printer := niceyaml.NewPrinter(niceyaml.WithGutter(g.gutter))

			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				_ = printer.Print(source)
			}
		})
	}
}

func BenchmarkPrinterWithWrapping(b *testing.B) {
	// Generate YAML with long lines.
	var sb strings.Builder
	for i := range 200 {
		fmt.Fprintf(&sb, "long_key_%d: \"%s\"\n", i, strings.Repeat("x", 200))
	}

	yaml := sb.String()
	source := niceyaml.NewSourceFromString(yaml)

	widths := []struct {
		name  string
		width int
	}{
		{"no_wrap", 0},
		{"wrap_80", 80},
		{"wrap_120", 120},
		{"wrap_40", 40},
	}

	for _, w := range widths {
		b.Run(w.name, func(b *testing.B) {
			printer := niceyaml.NewPrinter()
			printer.SetWidth(w.width)

			b.ReportAllocs()
			b.SetBytes(int64(len(yaml)))
			b.ResetTimer()

			for b.Loop() {
				_ = printer.Print(source)
			}
		})
	}
}

func BenchmarkPrinterClearStyles(b *testing.B) {
	highlightStyle := lipgloss.NewStyle().Background(lipgloss.Color("3"))

	rangeCounts := []int{10, 100, 1000}

	for _, count := range rangeCounts {
		b.Run(fmt.Sprintf("%d_ranges", count), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				printer := niceyaml.NewPrinter()

				for i := range count {
					r := position.Range{
						Start: position.New(i, 0),
						End:   position.New(i, 10),
					}
					printer.AddStyleToRange(&highlightStyle, r)
				}

				printer.ClearStyles()
			}
		})
	}
}

func BenchmarkPrinterAddStyleToRange(b *testing.B) {
	highlightStyle := lipgloss.NewStyle().Background(lipgloss.Color("3"))

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		printer := niceyaml.NewPrinter()

		for i := range 100 {
			r := position.Range{
				Start: position.New(i, 0),
				End:   position.New(i, 10),
			}
			printer.AddStyleToRange(&highlightStyle, r)
		}
	}
}
