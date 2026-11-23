package tokens_test

import (
	"fmt"
	"strings"
	"testing"

	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/tokens"
)

func BenchmarkTokenize(b *testing.B) {
	// Each source repeats unit 100, 1000, or 10000 times between head
	// and tail.
	benchmarks := []struct {
		name string
		head string
		unit string
		tail string
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
		// Each unit ends a key on the line of a value and goes on to the
		// next line, inside a flow mapping in a flow sequence. YAML folds
		// the two lines into one key, so the lexer checks at each ":"
		// which kind of flow collection holds it.
		{
			name: "FoldedKeys",
			head: "[{k",
			unit: stringtest.JoinLF("", " x: v, k"),
			tail: stringtest.LinesLF("", " x: v}]"),
		},
		// Each unit is a tab that opens the line of a value of a flow
		// mapping. The lexer checks at each tab whether the line folds
		// into the value.
		{
			name: "LeadingTabs",
			head: stringtest.LinesLF("{k: v"),
			unit: "\t",
			tail: stringtest.LinesLF("x}"),
		},
		// Each unit is a key with an empty flow sequence on one line. The
		// lexer finds the indent of the key at each "[".
		{
			name: "FlowValuesOnALine",
			head: "k: ",
			unit: "x: [] ",
			tail: "\n",
		},
	}

	for _, bm := range benchmarks {
		for _, count := range []int{100, 1000, 10000} {
			src := bm.head + strings.Repeat(bm.unit, count) + bm.tail

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
