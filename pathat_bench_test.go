package niceyaml_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
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
