package printer_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

const benchmarkOverlayKind kind.Kind = "benchmarkOverlayKind"

var (
	// The overlay counts the overlay benchmarks run at.
	benchmarkRangeCounts = []struct {
		name   string
		ranges int
	}{
		{"0_ranges", 0},
		{"10_ranges", 10},
		{"50_ranges", 50},
		{"100_ranges", 100},
		{"500_ranges", 500},
	}

	// Styles that give benchmarkOverlayKind a background color.
	benchmarkOverlayStyles = style.New(
		lipgloss.NewStyle(),
		style.Set(benchmarkOverlayKind, lipgloss.NewStyle().Background(lipgloss.Color("3"))),
	)
)

// addBenchmarkOverlays adds n overlays of ten columns to view, one on every
// fifth line.
func addBenchmarkOverlays(view *line.View, n int) {
	for i := range n {
		lineNum := (i * 5) % view.Count()
		view.AddOverlay(benchmarkOverlayKind, yamltest.LineRange(lineNum, 0, 10))
	}
}

// benchmarkPrint measures p printing a view of source, which holds yaml,
// and reports the throughput in bytes of yaml.
func benchmarkPrint(b *testing.B, p *printer.Printer, source *niceyaml.Source, yaml string) {
	b.Helper()

	b.ReportAllocs()
	b.SetBytes(int64(len(yaml)))

	for b.Loop() {
		_ = p.Print(source.View())
	}
}

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
		yaml := yamltest.GenerateYAML(sz.lines)
		source := niceyaml.NewSourceFromString(yaml)

		b.Run(sz.name, func(b *testing.B) {
			benchmarkPrint(b, printer.New(), source, yaml)
		})
	}
}

func BenchmarkPrinterPrint_WithOverlays(b *testing.B) {
	yaml := yamltest.GenerateYAML(500)

	for _, rc := range benchmarkRangeCounts {
		b.Run(rc.name, func(b *testing.B) {
			source := niceyaml.NewSourceFromString(yaml).View()
			p := printer.New(printer.WithStyles(benchmarkOverlayStyles))

			// Pre-configure overlays before measurement.
			addBenchmarkOverlays(source, rc.ranges)

			b.ReportAllocs()
			b.SetBytes(int64(len(yaml)))

			for b.Loop() {
				_ = p.Print(source)
			}
		})
	}
}

func BenchmarkPrinterPrint_WithOverlays_IncludingSetup(b *testing.B) {
	yaml := yamltest.GenerateYAML(500)

	for _, rc := range benchmarkRangeCounts {
		b.Run(rc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(yaml)))

			for b.Loop() {
				source := niceyaml.NewSourceFromString(yaml).View()
				p := printer.New(printer.WithStyles(benchmarkOverlayStyles))

				addBenchmarkOverlays(source, rc.ranges)

				_ = p.Print(source)
			}
		})
	}
}

func BenchmarkPrinterPrint_OverlaysDensity(b *testing.B) {
	// Measures print cost as the overlays on each line rise from 1 to 20.
	densities := []struct {
		name         string
		lines        int
		overlaysLine int // Overlays per line.
	}{
		{"sparse_1_per_line", 100, 1},
		{"medium_5_per_line", 100, 5},
		{"dense_20_per_line", 100, 20},
	}

	// AddOverlay drops a range that starts past the end of its line, so
	// every line must be wide enough to hold 20 overlays at a stride of 5.
	yaml := yamltest.GenerateLines(200, "key_%d: "+strings.Repeat("v", 100))

	for _, d := range densities {
		b.Run(d.name, func(b *testing.B) {
			source := niceyaml.NewSourceFromString(yaml).View()
			p := printer.New(printer.WithStyles(benchmarkOverlayStyles))

			// Pre-configure overlays before measurement.
			for lineNum := range d.lines {
				for o := range d.overlaysLine {
					col := o * 5
					source.AddOverlay(benchmarkOverlayKind, yamltest.LineRange(lineNum, col, col+3))
				}
			}

			kept := 0
			for lineNum := range d.lines {
				kept += len(source.Overlays(lineNum))
			}

			if want := d.lines * d.overlaysLine; kept != want {
				b.Fatalf("kept %d overlays, want %d", kept, want)
			}

			b.ReportAllocs()
			b.SetBytes(int64(len(yaml)))

			for b.Loop() {
				_ = p.Print(source)
			}
		})
	}
}

func BenchmarkPrinterPrintSlice(b *testing.B) {
	yaml := yamltest.GenerateYAML(5000)
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
			p := printer.New()

			b.ReportAllocs()
			b.SetBytes(sliceBytes)

			for b.Loop() {
				_ = p.Print(source.View().Slice(sl.span))
			}
		})
	}
}

func BenchmarkPrinterWithGutter(b *testing.B) {
	gutters := []struct {
		gutter printer.Gutter
		name   string
	}{
		{printer.NoGutter, "no_gutter"},
		{printer.DefaultGutter, "default_gutter"},
		{printer.DiffGutter, "diff_gutter"},
		{printer.LineNumberGutter, "line_number_gutter"},
	}

	yaml := yamltest.GenerateYAML(500)
	source := niceyaml.NewSourceFromString(yaml)

	for _, g := range gutters {
		b.Run(g.name, func(b *testing.B) {
			benchmarkPrint(b, printer.New(printer.WithGutter(g.gutter)), source, yaml)
		})
	}
}

func BenchmarkPrinterWithWrapping(b *testing.B) {
	yaml := yamltest.GenerateLines(200, `long_key_%d: "`+strings.Repeat("x", 200)+`"`)
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
			benchmarkPrint(b, printer.New(printer.WithWrapWidth(w.width)), source, yaml)
		})
	}
}

func BenchmarkLinesAddOverlay(b *testing.B) {
	yaml := yamltest.GenerateYAML(100)

	b.ReportAllocs()

	for b.Loop() {
		source := niceyaml.NewSourceFromString(yaml).View()

		for i := range 100 {
			source.AddOverlay(benchmarkOverlayKind, yamltest.LineRange(i, 0, 10))
		}
	}
}

func BenchmarkPrinterLayout_LongTransformedValue(b *testing.B) {
	// A transform that adds runes around a long value makes the shown text
	// of its run longer than the content, and the value wraps into many
	// rows that each map back to a column of the content.
	styles := style.New(lipgloss.NewStyle(), style.Set(kind.LiteralString, bracketStyle("[", "]")))

	for _, n := range []int{8000, 32000} {
		view := niceyaml.NewSourceFromString("data: " + strings.Repeat("QUJD", n/4) + "\n").View()

		b.Run(fmt.Sprintf("chars_%d", n), func(b *testing.B) {
			p := printer.New(printer.WithWrapWidth(80), printer.WithStyles(styles))

			b.ReportAllocs()

			for b.Loop() {
				_ = p.Layout(view)
			}
		})
	}
}

func BenchmarkPrinterPrintError_LongWrappedValue(b *testing.B) {
	// A long base64 value, as a Kubernetes Secret holds, wraps into many
	// rows, and the caret run under it takes a row below each one.
	for _, n := range []int{8000, 32000} {
		source := niceyaml.NewSourceFromString("data: " + strings.Repeat("QUJD", n/4) + "\n")
		err := source.Bind(yamltest.ErrorAt("bad", "data"))

		b.Run(fmt.Sprintf("chars_%d", n), func(b *testing.B) {
			p := printer.New(printer.WithWrapWidth(80))

			b.ReportAllocs()

			for b.Loop() {
				_ = p.PrintError(err)
			}
		})
	}
}

func BenchmarkPrinterPrintError_LongNestedMessage(b *testing.B) {
	// A branch whose message quotes a long value wraps into many rows
	// behind its connector.
	for _, n := range []int{1600, 12800} {
		err := errors.Join(errors.New("first"), errors.New(strings.Repeat("word ", n)))

		b.Run(fmt.Sprintf("words_%d", n), func(b *testing.B) {
			p := printer.New(printer.WithWrapWidth(80))

			b.ReportAllocs()

			for b.Loop() {
				_ = p.PrintError(err)
			}
		})
	}
}
