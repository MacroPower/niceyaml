package printer_test

import (
	"testing"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/printer"
)

// oneLineLayouts are the layouts that the benchmarks of a document on a
// single line run at. A layout without a wrap width shows the line as one
// row. A wrapped layout cuts the line into rows of 80 cells.
var oneLineLayouts = []struct {
	name  string
	items int
	wrap  int
}{
	{"items_100", 100, 0},
	{"items_1000", 1000, 0},
	{"items_10000", 10000, 0},
	{"items_10000_wrapped", 10000, 80},
}

// oneLineLayout returns the layout of a flow sequence of the given number
// of mappings, all on one line. It also returns the width of that line in
// columns.
func oneLineLayout(items, wrap int) (printer.Layout, int) {
	source := niceyaml.NewSourceFromString(
		yamltest.FlowSequence(`{"id": 0, "name": "u"}`, items)+"\n",
		yamltest.NoSizeLimits()...,
	)
	p := printer.New(printer.WithWrapWidth(wrap))

	return p.Layout(source.View()), source.Lines().Line(0).Width()
}

// The time for each call stays level as the line grows when the layout
// does not measure the shown text of the line again for each column.
func BenchmarkLayout_CellOf_OneLine(b *testing.B) {
	for _, sz := range oneLineLayouts {
		layout, width := oneLineLayout(sz.items, sz.wrap)

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()

			col := 0

			for b.Loop() {
				if layout.CellOf(position.New(0, col)) < 0 {
					b.Fatal("no cell")
				}

				col = (col + 7919) % width
			}
		})
	}
}

func BenchmarkLayout_PositionAt_OneLine(b *testing.B) {
	for _, sz := range oneLineLayouts {
		layout, width := oneLineLayout(sz.items, sz.wrap)

		// Each row but the last of a wrapped layout shows at least 40
		// cells. The first 40 cells thus hold content on every row but
		// the last.
		rows, cells := 1, width
		if sz.wrap > 0 {
			rows, cells = layout.Rows()-1, 40
		}

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()

			i := 0

			for b.Loop() {
				if _, ok := layout.PositionAt(i%rows, (i*7919)%cells); !ok {
					b.Fatalf("no position at row %d", i%rows)
				}

				i++
			}
		})
	}
}
