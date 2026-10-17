package edit_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/edit"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

func BenchmarkNode_Exists(b *testing.B) {
	// Each Apply checks a key that the text lacks for each key of a root
	// mapping, as a loader checks optional keys. The root merges a mapping
	// of the document, or a mapping of a reference document. The reads of
	// both cost about the same, in time linear in the number of keys.
	merges := []struct {
		name, head string
		refs       []niceyaml.SourceOption
	}{
		{name: "own_merge", head: "d: &d {x: 1}\n<<: *d\n"},
		{
			name: "reference_merge",
			head: "<<: *d\n",
			refs: []niceyaml.SourceOption{
				niceyaml.WithReferences(niceyaml.NewSourceFromString("d: &d {x: 1}\n")),
			},
		},
	}

	for _, m := range merges {
		for _, keys := range []int{1000, 2000, 4000} {
			var text strings.Builder

			text.WriteString(m.head)

			for i := range keys {
				fmt.Fprintf(&text, "k%d: v%d\n", i, i)
			}

			missing := make([]paths.Path, keys)
			for i := range missing {
				missing[i] = paths.Doc().Child(fmt.Sprintf("opt%d", i))
			}

			b.Run(fmt.Sprintf("%s_keys_%d", m.name, keys), func(b *testing.B) {
				b.ReportAllocs()

				for b.Loop() {
					b.StopTimer()

					doc := yamltest.FirstDocument(b, text.String(), append(yamltest.NoSizeLimits(), m.refs...)...)

					d, err := edit.New(doc)
					require.NoError(b, err)

					b.StartTimer()

					require.NoError(b, d.Apply(b.Context(), paths.Doc(), func(_ context.Context, n *edit.Node) error {
						for _, p := range missing {
							if n.At(p).Exists() {
								return fmt.Errorf("%s exists", p)
							}
						}

						return nil
					}))
				}
			})
		}
	}
}
