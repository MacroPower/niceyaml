package niceyaml_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// generateNestedYAML creates YAML nested to the given depth, where each
// level branches into itemsPerLevel keys.
func generateNestedYAML(depth, itemsPerLevel int) string {
	var sb strings.Builder

	var writeLevel func(level int)

	writeLevel = func(level int) {
		indent := strings.Repeat("  ", level)
		for i := range itemsPerLevel {
			if level < depth-1 {
				fmt.Fprintf(&sb, "%slevel%d_item%d:\n", indent, level, i)
				writeLevel(level + 1)
			} else {
				fmt.Fprintf(&sb, "%skey_%d: value_%d\n", indent, i, i)
			}
		}
	}

	sb.WriteString("root:\n")
	writeLevel(1)

	return sb.String()
}

func BenchmarkNewSourceFromString(b *testing.B) {
	sizes := []struct {
		name  string
		lines int
	}{
		{"small_50", 50},
		{"medium_500", 500},
		{"large_5000", 5000},
		{"xlarge_50000", 50000},
	}

	for _, sz := range sizes {
		yaml := yamltest.GenerateYAML(sz.lines)
		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(yaml)))

			for b.Loop() {
				_ = niceyaml.NewSourceFromString(yaml)
			}
		})
	}
}

func BenchmarkNewSourceFromString_Nested(b *testing.B) {
	sizes := []struct {
		name          string
		depth         int
		itemsPerLevel int
	}{
		{"shallow_wide", 2, 100},
		{"deep_narrow", 10, 5},
		{"balanced", 5, 20},
	}

	for _, sz := range sizes {
		yaml := generateNestedYAML(sz.depth, sz.itemsPerLevel)
		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(yaml)))

			for b.Loop() {
				_ = niceyaml.NewSourceFromString(yaml)
			}
		})
	}
}

func BenchmarkNode_DecodeScoped(b *testing.B) {
	type item struct {
		Name  string `yaml:"name"`
		Value int    `yaml:"value"`
	}

	// Each entry of a merge layout takes its value from the defaults
	// through a `<<` merge key. Each entry of an anchored layout also
	// defines an anchor of its own.
	sizes := []struct {
		name     string
		items    int
		merge    bool
		anchored bool
	}{
		{"items_100", 100, false, false},
		{"items_1000", 1000, false, false},
		{"merge_items_100", 100, true, false},
		{"merge_items_1000", 1000, true, false},
		{"merge_items_4000", 4000, true, false},
		{"anchored_items_4000", 4000, true, true},
	}

	for _, sz := range sizes {
		var sb strings.Builder

		if sz.merge {
			sb.WriteString("defaults: &d\n  value: 1\nitems:\n")
		} else {
			sb.WriteString("items:\n")
		}

		for i := range sz.items {
			switch {
			case sz.anchored:
				fmt.Fprintf(&sb, "  - &a%d\n    <<: *d\n    name: item_%d\n", i, i)
			case sz.merge:
				fmt.Fprintf(&sb, "  - <<: *d\n    name: item_%d\n", i)
			default:
				fmt.Fprintf(&sb, "  - name: item_%d\n    value: %d\n", i, i)
			}
		}

		doc := yamltest.FirstDocument(b, sb.String())

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
		var sb strings.Builder

		for i := range sz.docs {
			fmt.Fprintf(&sb, "---\na: x%d\nb: y\nc: z\n", i)
		}

		docs, err := niceyaml.NewSourceFromString(sb.String()).Documents()
		require.NoError(b, err)

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
		input := strings.Repeat("---\na: &x 1\nb: &x 2\nc: *x\n", sz.docs)

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				// Each document builds its decode tree once, so each
				// iteration parses the stream anew.
				docs, err := niceyaml.NewSourceFromString(input).Documents()
				if err != nil {
					b.Fatal(err)
				}

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

		for i := range sz.anchors {
			fmt.Fprintf(&sb, "a%d: &a%d {v: %d}\n", i, i, i)
		}

		sb.WriteString("refs: [")

		for i := range sz.anchors {
			if i > 0 {
				sb.WriteString(", ")
			}

			fmt.Fprintf(&sb, "*a%d", i)
		}

		sb.WriteString("]\nitem: *base\n")

		doc, err := niceyaml.NewSourceFromString(sb.String(), ref).Document()
		require.NoError(b, err)

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

func BenchmarkNode_Nodes(b *testing.B) {
	sizes := []struct {
		name  string
		items int
	}{
		{"items_100", 100},
		{"items_1000", 1000},
		{"items_10000", 10000},
	}

	path := paths.Current().Child("items").IndexAll()

	for _, sz := range sizes {
		var sb strings.Builder

		sb.WriteString("items:\n")

		for i := range sz.items {
			fmt.Fprintf(&sb, "  - name: item_%d\n    value: %d\n", i, i)
		}

		doc := yamltest.FirstDocument(b, sb.String())

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				items, err := doc.Nodes(path)
				if err != nil {
					b.Fatal(err)
				}

				if len(items) != sz.items {
					b.Fatalf("got %d nodes, want %d", len(items), sz.items)
				}
			}
		})
	}
}

func BenchmarkNode_BindManyPaths(b *testing.B) {
	sizes := []struct {
		name string
		keys int
	}{
		{"keys_500", 500},
		{"keys_2000", 2000},
		{"keys_5000", 5000},
	}

	for _, sz := range sizes {
		var sb strings.Builder

		errs := make([]error, 0, sz.keys)

		for i := range sz.keys {
			fmt.Fprintf(&sb, "k%d: v%d\n", i, i)

			errs = append(errs, niceyaml.NewError("bad value",
				niceyaml.AtPath(paths.Current().Child(fmt.Sprintf("k%d", i)))))
		}

		doc, err := niceyaml.NewSourceFromString(sb.String()).Document()
		require.NoError(b, err)

		joined := errors.Join(errs...)

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_ = doc.Bind(joined).Error()
			}
		})
	}
}

func BenchmarkSourceRunes(b *testing.B) {
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
		lines := niceyaml.NewSourceFromString(yaml).Lines()

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(yaml)))

			for b.Loop() {
				count := 0
				for range lines.Runes() {
					count++
				}

				_ = count
			}
		})
	}
}

func BenchmarkSourceLines(b *testing.B) {
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
		lines := niceyaml.NewSourceFromString(yaml).Lines()

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(yaml)))

			for b.Loop() {
				count := 0
				for range lines.All() {
					count++
				}

				_ = count
			}
		})
	}
}

func BenchmarkSourceLen(b *testing.B) {
	yaml := yamltest.GenerateYAML(5000)
	lines := niceyaml.NewSourceFromString(yaml).Lines()

	b.ReportAllocs()

	for b.Loop() {
		_ = lines.Len()
	}
}

func BenchmarkSourceContent(b *testing.B) {
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
			b.ReportAllocs()
			b.SetBytes(int64(len(yaml)))

			for b.Loop() {
				_ = source.Lines().Content()
			}
		})
	}
}

func BenchmarkSourceBind_ManyDocuments(b *testing.B) {
	sizes := []struct {
		name string
		docs int
	}{
		{"docs_100", 100},
		{"docs_1000", 1000},
	}

	for _, sz := range sizes {
		// Each document spans three lines, and the error joins one
		// finding per line, as a line-oriented lint reports them.
		source := niceyaml.NewSourceFromString(strings.Repeat("---\na: 1\nb: 2\n", sz.docs))

		_, err := source.Documents()
		require.NoError(b, err)

		total := source.Lines().Len()
		findings := make([]error, 0, total)

		for i := range total {
			rng := position.NewRange(position.New(i, 0), position.New(i, 1))
			findings = append(findings, niceyaml.NewError("finding", niceyaml.AtRange(rng)))
		}

		joined := errors.Join(findings...)

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				require.Error(b, source.Bind(joined))
			}
		})
	}
}

func BenchmarkSource_File_ManyDocuments(b *testing.B) {
	sizes := []struct {
		name string
		docs int
	}{
		{"docs_1000", 1000},
		{"docs_8000", 8000},
	}

	for _, sz := range sizes {
		var sb strings.Builder

		for i := range sz.docs {
			fmt.Fprintf(&sb, "---\nk%d: v%d\n", i, i)
		}

		input := sb.String()

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(input)))

			for b.Loop() {
				// File parses once per Source, so each iteration lexes a
				// new one outside the timer.
				b.StopTimer()

				source := niceyaml.NewSourceFromString(input)
				source.Tokens()

				b.StartTimer()

				file, err := source.File()
				require.NoError(b, err)
				require.Len(b, file.Docs, sz.docs)
			}
		})
	}
}
