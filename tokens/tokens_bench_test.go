package tokens_test

import (
	"strconv"
	"strings"
	"testing"

	"go.jacobcolvin.com/niceyaml/tokens"
)

// BenchmarkTokenize_TabRepeats tokenizes double-quoted scalars that each
// have a second line a tab indents. The lexer cuts every scalar at its tab
// and reads the tab again as a token of its own, and Tokenize drops each
// of those repeats.
func BenchmarkTokenize_TabRepeats(b *testing.B) {
	for _, scalars := range []int{100, 1000, 10000} {
		src := strings.Repeat("k: \"x\n\ty\"\n", scalars)

		b.Run(strconv.Itoa(scalars), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(src)))

			for b.Loop() {
				_ = tokens.Tokenize(src)
			}
		})
	}
}

// BenchmarkTokenize_OpenFlows tokenizes documents that each leave a flow
// sequence open. The lexer reads each document after the first as the
// inside of that sequence, and Tokenize lexes the source again from the
// marker of each one.
func BenchmarkTokenize_OpenFlows(b *testing.B) {
	for _, docs := range []int{100, 1000, 10000} {
		src := strings.Repeat("k: [v,\n---\n", docs)

		b.Run(strconv.Itoa(docs), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(src)))

			for b.Loop() {
				_ = tokens.Tokenize(src)
			}
		})
	}
}
