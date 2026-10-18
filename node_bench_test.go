package niceyaml_test

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

func BenchmarkNode_PathAt(b *testing.B) {
	sizes := []struct {
		name   string
		lines  int
		nested bool
	}{
		{"small_50", 50, false},
		{"large_5000", 5000, false},
		{"xlarge_50000", 50000, false},
		{"nested_50000", 50000, true},
	}

	for _, sz := range sizes {
		yaml := yamltest.GenerateYAML(sz.lines)

		// A nested document indents each key one level deeper than the
		// line above it, ten levels at a time, so a key starts at the
		// column its indent ends.
		at := func(line int) position.Position { return position.New(line, 0) }
		if sz.nested {
			yaml = nestedYAML(sz.lines)
			at = func(line int) position.Position { return position.New(line, 2*(line%10)) }
		}

		last := at(sz.lines - 1)

		// The first call on a document reads its whole tree.
		b.Run(sz.name+"/first", func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				b.StopTimer()

				doc := yamltest.FirstDocument(b, yaml, yamltest.NoSizeLimits()...)

				b.StartTimer()

				_, ok := doc.PathAt(last)
				require.True(b, ok)
			}
		})

		b.Run(sz.name+"/later", func(b *testing.B) {
			doc := yamltest.FirstDocument(b, yaml, yamltest.NoSizeLimits()...)

			_, ok := doc.PathAt(last)
			require.True(b, ok)

			b.ReportAllocs()

			i := 0

			for b.Loop() {
				_, ok := doc.PathAt(at(i % sz.lines))
				if !ok {
					b.Fatal("no path")
				}

				i++
			}
		})
	}
}

// nestedYAML returns a document of the given number of lines, in blocks
// of mappings nested ten levels deep. The last line of each block holds a
// scalar.
func nestedYAML(lines int) string {
	var sb strings.Builder

	for i := range lines {
		depth := i % 10

		fmt.Fprintf(&sb, "%sk%d:", strings.Repeat("  ", depth), i)

		if depth == 9 {
			sb.WriteString(" v")
		}

		sb.WriteString("\n")
	}

	return sb.String()
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
		doc := yamltest.FirstDocument(
			b,
			"items:\n"+yamltest.GenerateLines(sz.items, sourceBenchItem),
			yamltest.NoSizeLimits()...,
		)

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
		errs := make([]error, 0, sz.keys)

		for i := range sz.keys {
			errs = append(errs, yamltest.ErrorAt("bad value", fmt.Sprintf("k%d", i)))
		}

		doc := yamltest.FirstDocument(b, yamltest.GenerateLines(sz.keys, "k%[1]d: v%[1]d"), yamltest.NoSizeLimits()...)

		joined := errors.Join(errs...)

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_ = doc.Bind(joined).Error()
			}
		})
	}
}

// BenchmarkNode_BindBesideReferenceMerge binds an error at each of many
// keys of a mapping whose `<<` merge key reads a reference document. The
// resolver cannot read such a merge key, so each binding looks the key up
// through the merge sources. In the own source, the keys are entries of the
// mapping before a merge of a reference anchor of as many keys. In the
// reference source, the merge of such an anchor brings in the keys. In the
// document source, the keys come from an anchor of the document that the
// merge key reads beside a small reference anchor. The joined layout binds
// every error in one call, and the each layout binds each error in a call
// of its own. The lookups keep the keys of each merge source that they
// read, so the time per error stays flat as the merge sources grow in each
// source and layout. The metric retained-B is the heap that the first call
// leaves live on a new document and its Source. The documents of each size
// read one reference Source. It keeps the keys of its anchors that the
// calls before the count read, so the count does not hold them.
func BenchmarkNode_BindBesideReferenceMerge(b *testing.B) {
	sources := []struct {
		input, ref func(keys int) string
		name, key  string
	}{
		{
			name:  "own",
			input: func(keys int) string { return "x:\n" + yamltest.GenerateLines(keys, "  a%d: 1") + "  <<: *u\n" },
			ref:   func(keys int) string { return "u: &u\n" + yamltest.GenerateLines(keys, "  k%d: 1") },
			key:   "a%d",
		},
		{
			name:  "reference",
			input: func(int) string { return "x:\n  <<: *u\n" },
			ref:   func(keys int) string { return "u: &u\n" + yamltest.GenerateLines(keys, "  k%d: 1") },
			key:   "k%d",
		},
		{
			name: "document",
			input: func(keys int) string {
				return "big: &big\n" + yamltest.GenerateLines(keys, "  k%d: 1") + "x:\n  <<: [*big, *b]\n"
			},
			ref: func(int) string { return "b: &b {zz: 1}\n" },
			key: "k%d",
		},
	}

	for _, source := range sources {
		for _, keys := range []int{1000, 4000, 16000} {
			input := source.input(keys)
			ref := yamltest.References(source.ref(keys))
			newDoc := func() *niceyaml.Node { return yamltest.FirstDocument(b, input, yamltest.NoSizeLimits(ref)...) }

			errs := make([]error, 0, keys)
			for i := range keys {
				errs = append(errs, yamltest.ErrorAt("bad value", "x", fmt.Sprintf(source.key, i)))
			}

			joined := errors.Join(errs...)

			layouts := []struct {
				bind func(doc *niceyaml.Node)
				name string
			}{
				{name: "joined", bind: func(doc *niceyaml.Node) { _ = doc.Bind(joined).Error() }},
				{name: "each", bind: func(doc *niceyaml.Node) {
					for _, err := range errs {
						_ = doc.Bind(err).Error()
					}
				}},
			}

			for _, layout := range layouts {
				b.Run(fmt.Sprintf("%s_%s_%d", source.name, layout.name, keys), func(b *testing.B) {
					b.ReportAllocs()

					doc := newDoc()

					for b.Loop() {
						layout.bind(doc)
					}

					yamltest.ReportTimePer(b, keys, "error")
					reportRetained(b, newDoc, layout.bind)
				})
			}
		}
	}
}

// BenchmarkNode_BindBesideReferenceMergeInEachDocument binds an error in
// each document of a Source of many documents. Each document is a mapping
// whose `<<` merge key reads one anchor of 1000 keys of a reference
// document. The error names a key that the anchor brings in. The
// reference Source keeps the keys of the anchor once for every document
// that reads it. So the time per document stays flat as the Source grows,
// and the heap that the bindings leave live grows with the documents
// alone. The metric retained-B is the heap that one binding in each
// document of a new Source leaves live while the Source lives.
func BenchmarkNode_BindBesideReferenceMergeInEachDocument(b *testing.B) {
	ref := yamltest.References("u: &u\n" + yamltest.GenerateLines(1000, "  k%d: 1"))
	bad := yamltest.ErrorAt("bad value", "x", "k0")

	bind := func(docs []*niceyaml.Node) {
		for _, doc := range docs {
			_ = doc.Bind(bad).Error()
		}
	}

	for _, count := range []int{1000, 4000, 16000} {
		input := strings.Repeat("---\nx: {<<: *u}\n", count)
		newDocs := func() []*niceyaml.Node {
			return yamltest.Documents(b, niceyaml.NewSourceFromString(input, yamltest.NoSizeLimits(ref)...), count)
		}

		b.Run(fmt.Sprintf("documents_%d", count), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				b.StopTimer()

				docs := newDocs()

				b.StartTimer()
				bind(docs)
			}

			yamltest.ReportTimePer(b, count, "document")
			reportRetained(b, newDocs, bind)
		})
	}
}

// BenchmarkNode_BindBesideNestedReferenceAnchors binds an error through
// each anchor of a chain that a reference document nests in place, as in
// `a0: &a0 {..., <<: &a1 {..., <<: &a2 {...}}}`. Each anchor holds 50
// keys, and the document merges each anchor in a mapping of its own. Each
// error names a key of the innermost anchor, so the binding through each
// anchor reads every level below it. The aliases layout adds an alias to
// each level.
//
// The views of the anchors share their nodes, and the reference keeps
// the keys of each level once, however many anchors hold it. So the
// bindings read the keys of each level once, and the time per error
// stays flat as the chain grows. The metric reference-B is the heap that
// a new reference Source keeps after a Source that reads it binds each
// error once and is gone. The metric reference-B/text-B divides it by the
// bytes of the text of the reference, and it stays flat too.
func BenchmarkNode_BindBesideNestedReferenceAnchors(b *testing.B) {
	layouts := []struct {
		name, alias string
	}{
		{name: "plain"},
		{name: "aliases", alias: "r%d: *z, "},
	}

	for _, layout := range layouts {
		for _, depth := range []int{10, 20, 40} {
			var chain, merges strings.Builder

			chain.WriteString("z: &z 1\na0: ")

			errs := make([]error, 0, depth)

			for i := range depth {
				fmt.Fprintf(&chain, "&a%d {", i)

				for j := range 50 {
					fmt.Fprintf(&chain, "k%d_%d: 1, ", i, j)
				}

				if layout.alias != "" {
					fmt.Fprintf(&chain, layout.alias, i)
				}

				if i < depth-1 {
					chain.WriteString("<<: ")
				}

				fmt.Fprintf(&merges, "x%d: {<<: *a%d}\n", i, i)

				errs = append(errs, yamltest.ErrorAt("bad value", fmt.Sprintf("x%d", i), fmt.Sprintf("k%d_0", depth-1)))
			}

			chain.WriteString("end: 1" + strings.Repeat("}", depth) + "\n")

			text, input := chain.String(), merges.String()
			newRef := func() *niceyaml.Source {
				src := niceyaml.NewSourceFromString(text)
				src.AllDocuments()

				return src
			}
			bind := func(ref *niceyaml.Source) {
				doc := yamltest.FirstDocument(b, input, yamltest.NoSizeLimits(niceyaml.WithReferences(ref))...)
				for _, err := range errs {
					_ = doc.Bind(err).Error()
				}
			}

			b.Run(fmt.Sprintf("%s_%d", layout.name, depth), func(b *testing.B) {
				b.ReportAllocs()

				for b.Loop() {
					b.StopTimer()

					ref := newRef()

					b.StartTimer()
					bind(ref)
				}

				yamltest.ReportTimePer(b, depth, "error")

				ref := newRef()
				before := liveHeap()

				bind(ref)

				after := liveHeap()

				runtime.KeepAlive(ref)

				kept := float64(int64(after) - int64(before))
				b.ReportMetric(kept, "reference-B")
				b.ReportMetric(kept/float64(len(text)), "reference-B/text-B")
			})
		}
	}
}

// reportRetained reports the heap that one call of run on a new value of
// newValue leaves live while the value lives, as the metric retained-B.
// The value is a document, or the documents of a new Source. The count
// holds what the documents build on the first call of its kind, such as
// the index of their lines. It also holds anything that run leaves on
// them, on their Source, or on a reference Source. A reference Source
// that every value reads keeps what the calls before the count left there,
// such as the keys of its anchors, so the count does not hold that. It
// does not hold what run returns.
func reportRetained[T any](b *testing.B, newValue func() T, run func(T)) {
	b.Helper()

	value := newValue()
	before := liveHeap()

	run(value)

	after := liveHeap()

	runtime.KeepAlive(value)

	b.ReportMetric(float64(int64(after)-int64(before)), "retained-B")
}

// liveHeap returns the bytes of the heap that stay live after a collection.
// It collects twice, because a sync.Pool, such as the one the go-yaml
// scanner keeps, holds its items through one collection.
func liveHeap() uint64 {
	runtime.GC()
	runtime.GC()

	var stats runtime.MemStats

	runtime.ReadMemStats(&stats)

	return stats.HeapAlloc
}
