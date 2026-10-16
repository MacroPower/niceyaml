package niceyaml_test

import (
	"context"
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

				doc := yamltest.FirstDocument(b, yaml, yamltest.NoSizeLimits()...)

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
		doc := yamltest.FirstDocument(b, deepDocument(depth), yamltest.NoSizeLimits()...)

		benchRejected(b, fmt.Sprintf("depth_%d", depth), depth, "level", decodeIntoAs[problemDeep](), doc)
	}
}

// benchRejected runs a benchmark under name that runs decode on each of
// docs in each iteration and stops unless the decoder rejects each one.
// It reports the time per unit, of which docs hold count.
func benchRejected(
	b *testing.B,
	name string,
	count int,
	unit string,
	decode func(context.Context, *niceyaml.Node) error,
	docs ...*niceyaml.Node,
) {
	b.Helper()

	b.Run(name, func(b *testing.B) {
		b.ReportAllocs()

		for b.Loop() {
			for _, doc := range docs {
				err := decode(b.Context(), doc)
				if !errors.Is(err, niceyaml.ErrDecode) {
					b.Fatalf("got %v, want a rejection", err)
				}
			}
		}

		yamltest.ReportTimePer(b, count, unit)
	})
}

// benchProblemServers returns a document that lists count servers. It
// writes the last server with the format last and each one before it
// with the format server, and each format takes the index of its server.
func benchProblemServers(count int, server, last string) string {
	return "servers:\n" + yamltest.GenerateLines(count-1, server) + fmt.Sprintf(last+"\n", count-1)
}

// benchProblemLayouts runs [benchRejected] for a decode into T on two
// layouts of a list of servers, at each count in counts. The layout
// last_of_N writes N-1 servers with the format server and the last with
// the format last, so the decoder rejects the last server alone. The
// layout each_of_N writes all N with the format each, so the decoder
// rejects each server.
func benchProblemLayouts[T any](b *testing.B, counts []int, server, last, each string) {
	b.Helper()

	layouts := []struct{ name, server, last string }{
		{"last_of", server, last},
		{"each_of", each, each},
	}

	for _, layout := range layouts {
		for _, count := range counts {
			doc := yamltest.FirstDocument(
				b,
				benchProblemServers(count, layout.server, layout.last),
				yamltest.NoSizeLimits()...,
			)

			benchRejected(b, fmt.Sprintf("%s_%d", layout.name, count), count, "server", decodeIntoAs[T](), doc)
		}
	}
}

// BenchmarkNode_Decode_ProblemsWide decodes a list of servers. In the
// first layout the decoder rejects the last server alone, so the search
// reads every server and finds nothing more. In the second it rejects
// each server, so the search reports as many problems as the list holds.
// The time per server stays flat as the list grows.
func BenchmarkNode_Decode_ProblemsWide(b *testing.B) {
	bad := "  - {name: n%d, port: http}"

	benchProblemLayouts[problemConfig](b, []int{1000, 20000}, "  - {name: n%d, port: 80}", bad, bad)
}

// BenchmarkNode_Decode_ProblemsMap decodes a map of servers. In the first
// layout the decoder rejects the last server alone, so the search reads
// every server and finds nothing more. In the second it rejects each
// server, so the search reports as many problems as the map holds. The
// time per server stays flat as the map grows.
func BenchmarkNode_Decode_ProblemsMap(b *testing.B) {
	bad := "  s%[1]d: {name: n%[1]d, port: http}"

	benchProblemLayouts[struct {
		Servers map[string]problemServer `yaml:"servers"`
	}](b, []int{2000, 20000}, "  s%[1]d: {name: n%[1]d, port: 80}", bad, bad)
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
	benchProblemLayouts[struct {
		Servers []problemTiered `yaml:"servers"`
	}](
		b,
		[]int{250, 1000},
		"  - {name: n%d, tier: low, port: 80}",
		"  - {name: n%d, tier: low, port: http}",
		"  - {name: n%d, tier: mid, port: 80}",
	)
}

// BenchmarkNode_DecodeScoped decodes each item of a list through the
// node of the item. The items of a merge layout take their value from
// defaults through a merge key, and the items of the anchored layout
// also define an anchor each.
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
		doc := yamltest.FirstDocument(
			b,
			sz.head+"items:\n"+yamltest.GenerateLines(sz.items, sz.entry),
			yamltest.NoSizeLimits()...,
		)

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

// BenchmarkNode_DecodeRejectedStream decodes every document of a stream
// whose value at a is no int, so the decoder rejects each document. It
// reports the time per document.
func BenchmarkNode_DecodeRejectedStream(b *testing.B) {
	for _, count := range []int{1000, 10000} {
		input := yamltest.GenerateLines(count, stringtest.JoinLF("---", "a: x%d", "b: y", "c: z"))
		docs := yamltest.Documents(b, niceyaml.NewSourceFromString(input, yamltest.NoSizeLimits()...), count)

		benchRejected(b, fmt.Sprintf("docs_%d", count), count, "doc", decodeIntoAs[struct{ A int }](), docs...)
	}
}

// benchFreshDecode runs a benchmark under name that parses input anew in
// each iteration and decodes each of its documents into a T. A document
// builds its decode tree once, so only a new parse measures that work in
// every iteration. It reports the time per unit, of which input holds
// count.
func benchFreshDecode[T any](b *testing.B, name, input string, count int, unit string) {
	b.Helper()

	b.Run(name, func(b *testing.B) {
		b.ReportAllocs()

		for b.Loop() {
			for _, doc := range niceyaml.NewSourceFromString(input, yamltest.NoSizeLimits()...).Documents() {
				_, err := doc.Decode[T](b.Context())
				if err != nil {
					b.Fatal(err)
				}
			}
		}

		yamltest.ReportTimePer(b, count, unit)
	})
}

// BenchmarkNode_DecodeReusedAnchorStream decodes every document of a
// stream whose documents each reuse an anchor name, so each document
// decodes from a tree with renamed anchors. The time per document stays
// flat as the stream grows.
func BenchmarkNode_DecodeReusedAnchorStream(b *testing.B) {
	for _, count := range []int{500, 2000} {
		input := strings.Repeat(stringtest.LinesLF("---", "a: &x 1", "b: &x 2", "c: *x"), count)

		benchFreshDecode[map[string]int](b, fmt.Sprintf("docs_%d", count), input, count, "doc")
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

	ref := yamltest.References("base: &base {x: notint}\n")

	for _, count := range []int{2000, 8000} {
		aliases := make([]string, count)
		for i := range aliases {
			aliases[i] = fmt.Sprintf("*a%d", i)
		}

		input := yamltest.GenerateLines(count, "a%[1]d: &a%[1]d {v: %[1]d}") +
			"refs: [" + strings.Join(aliases, ", ") + "]\n" +
			"item: *base\n"

		doc := yamltest.Document(b, niceyaml.NewSourceFromString(input, yamltest.NoSizeLimits(ref)...))

		benchRejected(b, fmt.Sprintf("anchors_%d", count), count, "alias", decodeAs[config](), doc)
	}
}

// BenchmarkNode_DecodeOverReferences decodes many small files that each
// read one anchor of a shared file of defaults, each file from a Source
// of its own. A validator decodes each file too, so each file runs two
// decodes. Every Source shares the parse of the defaults, and each decode
// reads the anchors its aliases reach. The time per file thus stays flat
// as the defaults grow.
func BenchmarkNode_DecodeOverReferences(b *testing.B) {
	type config struct {
		Server struct {
			Host string `yaml:"host"`
			Port int    `yaml:"port"`
		} `yaml:"server"`
		Local struct {
			Port int `yaml:"port"`
		} `yaml:"local"`
		Name string `yaml:"name"`
	}

	const files = 200

	untyped := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
		_, err := n.Decode[any](ctx)

		return err
	})

	for _, anchors := range []int{100, 1000} {
		defaults := niceyaml.NewSourceFromString(
			yamltest.GenerateLines(anchors, "a%[1]d: &a%[1]d {host: host%[1]d.example.internal, port: %[1]d}"),
			yamltest.NoSizeLimits()...,
		)

		inputs := make([]string, files)
		for i := range inputs {
			inputs[i] = fmt.Sprintf("name: n%d\nlocal: &local {port: 1}\nserver: *a%d\nother: *local\n", i, i%anchors)
		}

		b.Run(fmt.Sprintf("defaults_%dKiB", len(defaults.Text())>>10), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				for _, input := range inputs {
					doc := yamltest.Document(b, niceyaml.NewSourceFromString(input, niceyaml.WithReferences(defaults)))

					_, err := doc.Decode[config](b.Context(), niceyaml.WithValidator(untyped))
					if err != nil {
						b.Fatal(err)
					}
				}
			}

			yamltest.ReportTimePer(b, files, "file")
		})
	}
}

// BenchmarkNode_DecodeSelfAliases decodes a document whose one anchor
// holds many aliases to itself, which the decoder reads as null. The
// decode walks the anchor once rather than once per alias, so the time
// per alias stays flat as the aliases grow.
func BenchmarkNode_DecodeSelfAliases(b *testing.B) {
	for _, count := range []int{2000, 8000} {
		input := "a: &a\n" + strings.Repeat("  - *a\n", count)

		benchFreshDecode[map[string]any](b, fmt.Sprintf("aliases_%d", count), input, count, "alias")
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
	for _, count := range []int{2000, 8000} {
		var tries strings.Builder

		for i := range count {
			fmt.Fprintf(&tries, `- &"x [%d]" %d`+"\n", i+1, i)
		}

		tries.WriteString(stringtest.LinesLF("- &x 1", "- &x 2"))

		benchFreshDecode[[]any](b, fmt.Sprintf("tries_%d", count), tries.String(), count, "name")
	}

	for _, count := range []int{2000, 8000} {
		wide := yamltest.GenerateLines(count, stringtest.JoinLF(`- &"n%[1]d [1]" 1`, `- &"n%[1]d [1]" 2`))

		benchFreshDecode[[]any](b, fmt.Sprintf("wide_%d", count), wide, count, "name")
	}
}
