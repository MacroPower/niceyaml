package niceyaml_test

import (
	"errors"
	"fmt"
	"strings"
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

		b.Run(fmt.Sprintf("depth_%d", depth), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				var v problemDeep

				err := doc.DecodeInto(b.Context(), &v)
				if !errors.Is(err, niceyaml.ErrDecode) {
					b.Fatalf("got %v, want a rejection", err)
				}
			}

			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*depth), "ns/level")
		})
	}
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
		var sb strings.Builder

		sb.WriteString("servers:\n")

		for i := range layout.servers {
			port := "80"
			if layout.every || i == layout.servers-1 {
				port = "http"
			}

			fmt.Fprintf(&sb, "  - {name: n%d, port: %s}\n", i, port)
		}

		doc := yamltest.FirstDocument(b, sb.String())

		b.Run(layout.name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				var v problemConfig

				err := doc.DecodeInto(b.Context(), &v)
				if !errors.Is(err, niceyaml.ErrDecode) {
					b.Fatalf("got %v, want a rejection", err)
				}
			}

			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*layout.servers), "ns/server")
		})
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
		var sb strings.Builder

		sb.WriteString("servers:\n")

		for i := range layout.servers {
			tier, port := "low", "80"

			switch {
			case layout.every:
				tier = "mid"
			case i == layout.servers-1:
				port = "http"
			}

			fmt.Fprintf(&sb, "  - {name: n%d, tier: %s, port: %s}\n", i, tier, port)
		}

		doc := yamltest.FirstDocument(b, sb.String())

		b.Run(layout.name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				var v struct {
					Servers []problemTiered `yaml:"servers"`
				}

				err := doc.DecodeInto(b.Context(), &v)
				if !errors.Is(err, niceyaml.ErrDecode) {
					b.Fatalf("got %v, want a rejection", err)
				}
			}

			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*layout.servers), "ns/server")
		})
	}
}
