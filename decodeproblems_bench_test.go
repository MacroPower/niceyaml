package niceyaml_test

import (
	"errors"
	"fmt"
	"testing"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
)

// BenchmarkNode_Decode_ProblemsNested decodes a document nested many
// levels deep whose innermost value the decoder rejects. The search for
// the other problems reads each level once, so the time per level stays
// flat as the document deepens.
func BenchmarkNode_Decode_ProblemsNested(b *testing.B) {
	for _, depth := range []int{500, 1000, 2000, 4000} {
		doc := yamltest.FirstDocument(b, deepDocument(depth))

		benchRejected[problemDeep](b, fmt.Sprintf("depth_%d", depth), doc, depth, "level")
	}
}

// benchRejected runs a benchmark under name that decodes doc into a new
// T in each iteration and stops unless the decoder rejects it. It reports
// the time per unit, of which doc holds count.
func benchRejected[T any](b *testing.B, name string, doc *niceyaml.Node, count int, unit string) {
	b.Helper()

	b.Run(name, func(b *testing.B) {
		b.ReportAllocs()

		for b.Loop() {
			var v T

			err := doc.DecodeInto(b.Context(), &v)
			if !errors.Is(err, niceyaml.ErrDecode) {
				b.Fatalf("got %v, want a rejection", err)
			}
		}

		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*count), "ns/"+unit)
	})
}

// benchProblemServers returns a document that lists count servers. It
// writes the last server with the format last and each one before it
// with the format server, and each format takes the index of its server.
func benchProblemServers(count int, server, last string) string {
	return "servers:\n" + yamltest.GenerateLines(count-1, server) + fmt.Sprintf(last+"\n", count-1)
}

// BenchmarkNode_Decode_ProblemsWide decodes a list of servers. In the
// first layout the decoder rejects the last server alone, so the search
// reads every server and finds nothing more. In the second it rejects
// each server, so the search reports as many problems as the list holds.
// The time per server stays flat as the list grows.
func BenchmarkNode_Decode_ProblemsWide(b *testing.B) {
	layouts := []struct {
		name    string
		servers int
		every   bool
	}{
		{"last_of_1000", 1000, false},
		{"last_of_20000", 20000, false},
		{"each_of_1000", 1000, true},
		{"each_of_20000", 20000, true},
	}

	for _, layout := range layouts {
		server, last := "  - {name: n%d, port: 80}", "  - {name: n%d, port: http}"
		if layout.every {
			server = last
		}

		doc := yamltest.FirstDocument(b, benchProblemServers(layout.servers, server, last))

		benchRejected[problemConfig](b, layout.name, doc, layout.servers, "server")
	}
}

// BenchmarkNode_Decode_ProblemsText decodes a list of servers whose tier
// decodes itself from text. In the first layout the decoder rejects the
// port of the last server alone, so the search decodes every tier again
// and finds nothing more. In the second the tier of each server names no
// tier, so the search reports as many problems as the list holds. The
// go-yaml decoder reads every token of the file to hand one value its
// text, so the time per server grows with the list, in the decode and in
// the search alike.
func BenchmarkNode_Decode_ProblemsText(b *testing.B) {
	layouts := []struct {
		name    string
		servers int
		every   bool
	}{
		{"last_of_250", 250, false},
		{"last_of_1000", 1000, false},
		{"each_of_250", 250, true},
		{"each_of_1000", 1000, true},
	}

	for _, layout := range layouts {
		server, last := "  - {name: n%d, tier: low, port: 80}", "  - {name: n%d, tier: low, port: http}"
		if layout.every {
			server = "  - {name: n%d, tier: mid, port: 80}"
			last = server
		}

		doc := yamltest.FirstDocument(b, benchProblemServers(layout.servers, server, last))

		benchRejected[struct {
			Servers []problemTiered `yaml:"servers"`
		}](b, layout.name, doc, layout.servers, "server")
	}
}
