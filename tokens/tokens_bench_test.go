package tokens_test

import (
	"fmt"
	"strings"
	"testing"

	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/tokens"
)

func BenchmarkTokenize(b *testing.B) {
	// Each source repeats unit 100, 1000, or 10000 times.
	benchmarks := []struct {
		name string
		unit string
	}{
		// Each unit is a double-quoted scalar with a second line a tab
		// indents. The go-yaml lexer cuts every scalar at its tab and
		// reads the tab again as a token of its own.
		{
			name: "TabRepeats",
			unit: stringtest.LinesLF(
				`k: "x`,
				"\ty\"",
			),
		},
		// Each unit is a document that leaves a flow sequence open. The
		// go-yaml lexer reads each document after the first as the inside
		// of that sequence.
		{
			name: "OpenFlows",
			unit: stringtest.LinesLF(
				"k: [v,",
				"---",
			),
		},
	}

	for _, bm := range benchmarks {
		for _, count := range []int{100, 1000, 10000} {
			src := strings.Repeat(bm.unit, count)

			b.Run(fmt.Sprintf("%s/%d", bm.name, count), func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(src)))

				for b.Loop() {
					_ = tokens.Tokenize(src)
				}
			})
		}
	}
}
