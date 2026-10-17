package edit_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/edit"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

func BenchmarkDoc_Apply(b *testing.B) {
	var (
		g = paths.Doc().Child("g")
		h = paths.Doc().Child("h")
	)

	// Each Apply reads a flow mapping on one line, as a minified JSON
	// document holds it. The path of the first matches the root, which
	// holds the mapping. The second removes the mapping.
	edits := []struct {
		fn   edit.Func
		name string
		at   paths.Path
	}{
		{
			name: "set_at_root",
			at:   paths.Doc(),
			fn: func(ctx context.Context, n *edit.Node) error {
				return n.At(h).Set(ctx, 2)
			},
		},
		{
			name: "remove_mapping",
			at:   h,
			fn: func(_ context.Context, n *edit.Node) error {
				return n.At(g).Remove()
			},
		},
	}

	for _, e := range edits {
		for _, entries := range []int{1000, 2000, 4000} {
			var text strings.Builder

			text.WriteString("g: {")

			for i := range entries {
				if i > 0 {
					text.WriteString(", ")
				}

				fmt.Fprintf(&text, "k%d: %d", i, i)
			}

			text.WriteString("}\nh: 1\n")

			b.Run(fmt.Sprintf("%s_one_line_entries_%d", e.name, entries), func(b *testing.B) {
				b.ReportAllocs()

				for b.Loop() {
					b.StopTimer()

					doc := yamltest.FirstDocument(b, text.String(), yamltest.NoSizeLimits()...)

					d, err := edit.New(doc)
					require.NoError(b, err)

					b.StartTimer()

					require.NoError(b, d.Apply(b.Context(), e.at, e.fn))
				}
			})
		}
	}
}

func BenchmarkDoc_Apply_NestedAliases(b *testing.B) {
	var (
		k   = paths.MustParse("$.top.a.k")
		top = paths.Doc().Child("top")
	)

	// Each Apply sets a value inside top and then removes top. The aliases
	// inside top read the value ten times as often at each level, and none
	// of them reads it after the removal.
	for _, levels := range []int{6, 7, 8} {
		text := "top:\n" + aliasFan("  ", levels) + "z: 0\n"

		b.Run(fmt.Sprintf("inside_levels_%d", levels), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				b.StopTimer()

				doc := yamltest.FirstDocument(b, text, yamltest.NoSizeLimits()...)

				d, err := edit.New(doc)
				require.NoError(b, err)

				b.StartTimer()

				require.NoError(b, d.Apply(b.Context(), paths.Doc(), func(ctx context.Context, n *edit.Node) error {
					err := n.At(k).Set(ctx, 2)
					if err != nil {
						return err
					}

					return n.At(top).Remove()
				}))
			}
		})
	}

	// Each Apply sets a value of the anchored mapping a and then removes a.
	// The aliases outside a read the value ten times as often at each
	// level, and each of them reads the copy of a after the removal.
	for _, levels := range []int{1, 2, 3} {
		text := aliasFan("", levels)

		b.Run(fmt.Sprintf("outside_levels_%d", levels), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				b.StopTimer()

				doc := yamltest.FirstDocument(b, text, yamltest.NoSizeLimits()...)

				d, err := edit.New(doc)
				require.NoError(b, err)

				b.StartTimer()

				require.NoError(b, d.Apply(b.Context(), paths.Doc(), func(ctx context.Context, n *edit.Node) error {
					err := n.At(paths.MustParse("$.a.k")).Set(ctx, 2)
					if err != nil {
						return err
					}

					return n.At(paths.Doc().Child("a")).Remove()
				}))
			}
		})
	}
}
