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

				doc := yamltest.FirstDocument(b, yaml)

				b.StartTimer()

				_, ok := doc.PathAt(last)
				require.True(b, ok)
			}
		})

		b.Run(sz.name+"/later", func(b *testing.B) {
			doc := yamltest.FirstDocument(b, yaml)

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
		doc := yamltest.FirstDocument(b, "items:\n"+yamltest.GenerateLines(sz.items, sourceBenchItem))

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
			errs = append(errs, niceyaml.NewError("bad value",
				niceyaml.AtPath(paths.Current().Child(fmt.Sprintf("k%d", i)))))
		}

		doc := yamltest.Document(b, niceyaml.NewSourceFromString(yamltest.GenerateLines(sz.keys, "k%[1]d: v%[1]d")))

		joined := errors.Join(errs...)

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_ = doc.Bind(joined).Error()
			}
		})
	}
}
