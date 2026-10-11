package niceyaml_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

// BenchmarkNode_Decode_FirstDecode decodes a new parse of a document in
// each iteration, so it measures the work a document does once, on its
// first decode, which the other benchmarks spread over many decodes. The
// document holds a map and a list of small mappings, and its type
// validates each of them, so self-validation reads the keys of the map.
// The document holds no !!int tag, no reused anchor name, and no alias,
// so its first decode should cost about as much with self-validation as
// without it.
func BenchmarkNode_Decode_FirstDecode(b *testing.B) {
	type config struct {
		M     map[string]item `yaml:"m"`
		Items []item          `yaml:"items"`
	}

	yaml := stringtest.LinesLF(
		"m:",
		"  k: {name: a, price: 1}",
		"items:",
	) + yamltest.GenerateLines(1000, "  - {name: n%[1]d, price: %[1]d}")

	for _, mode := range selfValidationModes {
		b.Run(mode.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(yaml)))

			for b.Loop() {
				b.StopTimer()

				doc := yamltest.FirstDocument(b, yaml)

				b.StartTimer()

				_, err := doc.Decode[config](b.Context(), mode.opts...)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

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

// BenchmarkNode_Decode_ProblemsMap decodes a map of servers. In the first
// layout the decoder rejects the last server alone, so the search reads
// every server and finds nothing more. In the second it rejects each
// server, so the search reports as many problems as the map holds. The
// time per server stays flat as the map grows.
func BenchmarkNode_Decode_ProblemsMap(b *testing.B) {
	layouts := []struct {
		name    string
		servers int
		every   bool
	}{
		{"last_of_2000", 2000, false},
		{"last_of_20000", 20000, false},
		{"each_of_2000", 2000, true},
		{"each_of_20000", 20000, true},
	}

	for _, layout := range layouts {
		server, last := "  s%[1]d: {name: n%[1]d, port: 80}", "  s%[1]d: {name: n%[1]d, port: http}"
		if layout.every {
			server = last
		}

		doc := yamltest.FirstDocument(b, benchProblemServers(layout.servers, server, last))

		benchRejected[struct {
			Servers map[string]problemServer `yaml:"servers"`
		}](b, layout.name, doc, layout.servers, "server")
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

func BenchmarkNode_DecodeScoped(b *testing.B) {
	type item struct {
		Name  string `yaml:"name"`
		Value int    `yaml:"value"`
	}

	// The defaults that the entries of a merge layout and of an anchored
	// layout take their value from.
	defaults := stringtest.LinesLF(
		"defaults: &d",
		"  value: 1",
	)

	// Each entry of a merge layout takes its value from the defaults
	// through a `<<` merge key.
	merge := stringtest.JoinLF(
		"  - <<: *d",
		"    name: item_%d",
	)

	// Each entry of an anchored layout also defines an anchor of its own.
	anchored := stringtest.JoinLF(
		"  - &a%[1]d",
		"    <<: *d",
		"    name: item_%[1]d",
	)

	sizes := []struct {
		name  string
		head  string
		entry string
		items int
	}{
		{"items_100", "", sourceBenchItem, 100},
		{"items_1000", "", sourceBenchItem, 1000},
		{"merge_items_100", defaults, merge, 100},
		{"merge_items_1000", defaults, merge, 1000},
		{"merge_items_4000", defaults, merge, 4000},
		{"anchored_items_4000", defaults, anchored, 4000},
	}

	for _, sz := range sizes {
		doc := yamltest.FirstDocument(b, sz.head+"items:\n"+yamltest.GenerateLines(sz.items, sz.entry))

		items, err := doc.Nodes(paths.Current().Child("items").IndexAll())
		require.NoError(b, err)

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				for _, it := range items {
					_, err := it.Decode[item](b.Context())
					if err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}

func BenchmarkNode_DecodeRejectedStream(b *testing.B) {
	sizes := []struct {
		name string
		docs int
	}{
		{"docs_1000", 1000},
		{"docs_10000", 10000},
	}

	for _, sz := range sizes {
		input := yamltest.GenerateLines(sz.docs, stringtest.JoinLF("---", "a: x%d", "b: y", "c: z"))
		docs := niceyaml.NewSourceFromString(input).Documents()

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				for _, doc := range docs {
					var v struct{ A int }

					err := doc.DecodeInto(b.Context(), &v)
					if !errors.Is(err, niceyaml.ErrDecode) {
						b.Fatalf("got %v, want a rejection", err)
					}
				}
			}

			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*sz.docs), "ns/doc")
		})
	}
}

// BenchmarkNode_DecodeReusedAnchorStream decodes every document of a
// stream whose documents each reuse an anchor name, so each document
// decodes from a tree with renamed anchors. The time per document stays
// flat as the stream grows.
func BenchmarkNode_DecodeReusedAnchorStream(b *testing.B) {
	sizes := []struct {
		name string
		docs int
	}{
		{"docs_500", 500},
		{"docs_2000", 2000},
	}

	for _, sz := range sizes {
		input := strings.Repeat(stringtest.LinesLF("---", "a: &x 1", "b: &x 2", "c: *x"), sz.docs)

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				// Each document builds its decode tree once, so each
				// iteration parses the stream anew.
				docs := niceyaml.NewSourceFromString(input).Documents()

				for _, doc := range docs {
					_, err := doc.Decode[map[string]int](b.Context())
					if err != nil {
						b.Fatal(err)
					}
				}
			}

			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*sz.docs), "ns/doc")
		})
	}
}

// BenchmarkNode_DecodeReferenceAliases decodes a document whose value
// the decoder rejects at a token of a reference document, while the
// document holds many anchors that one sequence reads through aliases.
// The rejection looks through the aliases of each anchor the decode
// reads, so the time per alias stays flat as the anchors grow.
func BenchmarkNode_DecodeReferenceAliases(b *testing.B) {
	type config struct {
		Item struct{ X int } `yaml:"item"`
		Refs []any           `yaml:"refs"`
	}

	sizes := []struct {
		name    string
		anchors int
	}{
		{"anchors_2000", 2000},
		{"anchors_8000", 8000},
	}

	ref := niceyaml.WithReferences(niceyaml.NewSourceFromString("base: &base {x: notint}\n"))

	for _, sz := range sizes {
		var sb strings.Builder

		sb.WriteString(yamltest.GenerateLines(sz.anchors, "a%[1]d: &a%[1]d {v: %[1]d}"))
		sb.WriteString("refs: [")

		for i := range sz.anchors {
			if i > 0 {
				sb.WriteString(", ")
			}

			fmt.Fprintf(&sb, "*a%d", i)
		}

		sb.WriteString("]\nitem: *base\n")

		doc := yamltest.Document(b, niceyaml.NewSourceFromString(sb.String(), ref))

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_, err := doc.Decode[config](b.Context())
				if !errors.Is(err, niceyaml.ErrDecode) {
					b.Fatalf("got %v, want a rejection", err)
				}
			}

			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*sz.anchors), "ns/alias")
		})
	}
}

// BenchmarkNode_DecodeSelfAliases decodes a document whose one anchor
// holds many aliases to itself, which the decoder reads as null. The
// decode walks the anchor once rather than once per alias, so the time
// per alias stays flat as the aliases grow.
func BenchmarkNode_DecodeSelfAliases(b *testing.B) {
	sizes := []struct {
		name    string
		aliases int
	}{
		{"aliases_2000", 2000},
		{"aliases_8000", 8000},
	}

	for _, sz := range sizes {
		input := "a: &a\n" + strings.Repeat("  - *a\n", sz.aliases)

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				// A document builds its decode tree once, so each iteration
				// parses the input anew.
				doc, err := niceyaml.NewSourceFromString(input).Document()
				if err != nil {
					b.Fatal(err)
				}

				_, err = doc.Decode[map[string]any](b.Context())
				if err != nil {
					b.Fatal(err)
				}
			}

			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*sz.aliases), "ns/alias")
		})
	}
}

// BenchmarkNode_DecodeBracketedAnchorNames decodes sequences whose quoted
// anchor names hold a count in brackets, like the new names the decode
// tree gives reused anchors. The tries sequences spell "x [1]" through
// "x [N]", so the two anchors named x skip N counts. The wide sequences
// reuse N such names, each twice. The decode finds where the document
// spells each name in one lookup rather than checking every name against
// every count, so the time per name grows little with N.
func BenchmarkNode_DecodeBracketedAnchorNames(b *testing.B) {
	sizes := []struct {
		name  string
		kind  string
		names int
	}{
		{"tries_2000", "tries", 2000},
		{"tries_8000", "tries", 8000},
		{"wide_2000", "wide", 2000},
		{"wide_8000", "wide", 8000},
	}

	for _, sz := range sizes {
		var sb strings.Builder

		for i := range sz.names {
			if sz.kind == "tries" {
				fmt.Fprintf(&sb, "- &\"x [%d]\" %d\n", i+1, i)
			} else {
				fmt.Fprintf(&sb, "- &\"n%d [1]\" 1\n- &\"n%d [1]\" 2\n", i, i)
			}
		}

		if sz.kind == "tries" {
			sb.WriteString("- &x 1\n- &x 2\n")
		}

		input := sb.String()

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				// A document builds its decode tree once, so each iteration
				// parses the input anew.
				doc, err := niceyaml.NewSourceFromString(input).Document()
				if err != nil {
					b.Fatal(err)
				}

				_, err = doc.Decode[[]any](b.Context())
				if err != nil {
					b.Fatal(err)
				}
			}

			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*sz.names), "ns/name")
		})
	}
}
