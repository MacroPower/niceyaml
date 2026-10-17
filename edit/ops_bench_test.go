package edit_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/edit"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

func BenchmarkNode_Remove(b *testing.B) {
	sizes := []struct {
		name    string
		aliases int
	}{
		{"aliases_100", 100},
		{"aliases_1000", 1000},
		{"aliases_10000", 10000},
	}

	base := paths.MustParse("$.base")

	for _, sz := range sizes {
		// Each item reads the anchor, so the removal copies the value to the
		// first item and reads back every item.
		var text strings.Builder

		text.WriteString("base: &b\n  k: 1\nitems:\n")

		for range sz.aliases {
			text.WriteString("  - b: *b\n")
		}

		b.Run(sz.name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				b.StopTimer()

				doc := yamltest.FirstDocument(b, text.String(), yamltest.NoSizeLimits()...)

				d, err := edit.New(doc)
				require.NoError(b, err)

				b.StartTimer()

				err = d.Apply(b.Context(), paths.Doc(), func(_ context.Context, n *edit.Node) error {
					return n.At(base).Remove()
				})
				require.NoError(b, err)
			}
		})
	}
}
