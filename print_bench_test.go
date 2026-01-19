package niceyaml_test

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/macropower/niceyaml"
	"github.com/macropower/niceyaml/position"
	"github.com/macropower/niceyaml/style"
)

const (
	benchmarkOverlayKind style.Style = iota
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

func BenchmarkPrinterPrint_WithOverlays(b *testing.B) {
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
	highlightStyle := lipgloss.NewStyle().Background(lipgloss.Color("3"))
	overlayStyler := style.NewStyles(lipgloss.NewStyle(), style.Set(benchmarkOverlayKind, highlightStyle))

	for _, rc := range rangeCounts {
		b.Run(rc.name, func(b *testing.B) {
			source := niceyaml.NewSourceFromString(yaml)
			printer := niceyaml.NewPrinter(niceyaml.WithStyles(overlayStyler))

			// Pre-configure overlays before measurement.
			for i := range rc.ranges {
				lineNum := (i * 5) % source.Len()
				r := position.Range{
					Start: position.New(lineNum, 0),
					End:   position.New(lineNum, 10),
				}
				source.AddOverlay(benchmarkOverlayKind, r)
			}

			b.ReportAllocs()
			b.SetBytes(int64(len(yaml)))
			b.ResetTimer()

			for b.Loop() {
				_ = printer.Print(source)
			}
		})
	}
}

func BenchmarkPrinterPrint_WithOverlays_IncludingSetup(b *testing.B) {
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
	highlightStyle := lipgloss.NewStyle().Background(lipgloss.Color("3"))
	overlayStyler := style.NewStyles(lipgloss.NewStyle(), style.Set(benchmarkOverlayKind, highlightStyle))

	for _, rc := range rangeCounts {
		b.Run(rc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(yaml)))

			for b.Loop() {
				source := niceyaml.NewSourceFromString(yaml)
				printer := niceyaml.NewPrinter(niceyaml.WithStyles(overlayStyler))

				// Distribute overlays across lines.
				for i := range rc.ranges {
					lineNum := (i * 5) % source.Len()
					r := position.Range{
						Start: position.New(lineNum, 0),
						End:   position.New(lineNum, 10),
					}
					source.AddOverlay(benchmarkOverlayKind, r)
				}

				_ = printer.Print(source)
			}
		})
	}
}

func BenchmarkPrinterPrint_OverlaysDensity(b *testing.B) {
	// Tests performance with overlays concentrated on fewer lines vs spread out.
	densities := []struct {
		name         string
		lines        int
		overlaysLine int // Overlays per line.
	}{
		{"sparse_1_per_line", 100, 1},
		{"medium_5_per_line", 100, 5},
		{"dense_20_per_line", 100, 20},
	}

	yaml := generateYAML(200)
	highlightStyle := lipgloss.NewStyle().Background(lipgloss.Color("3"))
	overlayStyler := style.NewStyles(lipgloss.NewStyle(), style.Set(benchmarkOverlayKind, highlightStyle))

	for _, d := range densities {
		b.Run(d.name, func(b *testing.B) {
			source := niceyaml.NewSourceFromString(yaml)
			printer := niceyaml.NewPrinter(niceyaml.WithStyles(overlayStyler))

			// Pre-configure overlays before measurement.
			for lineNum := range d.lines {
				for o := range d.overlaysLine {
					col := o * 5
					rng := position.Range{
						Start: position.New(lineNum, col),
						End:   position.New(lineNum, col+3),
					}
					source.AddOverlay(benchmarkOverlayKind, rng)
				}
			}

			b.ReportAllocs()
			b.SetBytes(int64(len(yaml)))
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
		name string
		span position.Span
	}{
		{"first_50", position.NewSpan(0, 50)},
		{"middle_50", position.NewSpan(2475, 2525)},
		{"last_50", position.NewSpan(4950, 5000)},
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
				_ = printer.Print(source, sl.span)
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
			b.SetBytes(int64(len(yaml)))
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

func BenchmarkSourceClearOverlays(b *testing.B) {
	yaml := generateYAML(1000)
	rangeCounts := []int{10, 100, 1000}

	for _, count := range rangeCounts {
		b.Run(fmt.Sprintf("%d_overlays", count), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				source := niceyaml.NewSourceFromString(yaml)

				for i := range count {
					r := position.Range{
						Start: position.New(i, 0),
						End:   position.New(i, 10),
					}
					source.AddOverlay(benchmarkOverlayKind, r)
				}

				source.ClearOverlays()
			}
		})
	}
}

func BenchmarkSourceAddOverlay(b *testing.B) {
	yaml := generateYAML(100)

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		source := niceyaml.NewSourceFromString(yaml)

		for i := range 100 {
			r := position.Range{
				Start: position.New(i, 0),
				End:   position.New(i, 10),
			}
			source.AddOverlay(benchmarkOverlayKind, r)
		}
	}
}
