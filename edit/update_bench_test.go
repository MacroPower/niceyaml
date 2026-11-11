package edit_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/edit"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

// updateBench is a case of [BenchmarkNode_Update]. For a count of entries
// or items, text returns the text of the value at path, and value returns
// the new value. Leaf makes the leaf calls that write that value, or it is
// nil.
type updateBench struct {
	text  func(count int) string
	value func(count int) any
	leaf  func(ctx context.Context, n *edit.Node, v any) error
	path  paths.Path
}

func BenchmarkNode_Update(b *testing.B) {
	// The Update of the change case replaces scalars with scalars and parses
	// the text once for all of them. So its time grows about linearly with
	// the number of entries, as the time of the Set does. The time of the
	// leaf calls grows with the square of that number. Each remove and each
	// insert of an Update parses the text. So the time of the remove and
	// append cases grows with the square of the number of entries or items.
	comments := func(count int) string {
		var text strings.Builder

		text.WriteString("m:\n")

		for i := range count {
			fmt.Fprintf(&text, "  # about k%d\n  k%d: %d  # was %d\n", i, i, i, i)
		}

		return text.String()
	}

	cases := map[string]updateBench{
		"change": {
			path: paths.Doc().Child("m"),
			text: comments,
			value: func(count int) any {
				v := make(yaml.MapSlice, 0, count)
				for i := range count {
					v = append(v, yaml.MapItem{Key: fmt.Sprintf("k%d", i), Value: i + 1})
				}

				return v
			},
			leaf: func(ctx context.Context, n *edit.Node, v any) error {
				items, _ := v.(yaml.MapSlice) //nolint:errcheck // The value of the case is a MapSlice.
				for _, item := range items {
					key, _ := item.Key.(string) //nolint:errcheck // Each key of the value is a string.

					err := n.At(paths.Current().Child(key)).Set(ctx, item.Value)
					if err != nil {
						return err
					}
				}

				return nil
			},
		},
		"remove": {
			path:  paths.Doc().Child("m"),
			text:  comments,
			value: func(int) any { return yaml.MapSlice{{Key: "k0", Value: 0}} },
		},
		"append": {
			path: paths.Doc().Child("l"),
			text: func(count int) string {
				var text strings.Builder

				text.WriteString("l:\n")

				for i := range count {
					fmt.Fprintf(&text, "  - i%d  # c%d\n", i, i)
				}

				return text.String()
			},
			value: func(count int) any {
				v := make([]string, 0, 2*count)
				for i := range 2 * count {
					v = append(v, fmt.Sprintf("i%d", i))
				}

				return v
			},
		},
	}

	for name, bc := range cases {
		calls := map[string]func(ctx context.Context, n *edit.Node, v any) error{
			"update": func(ctx context.Context, n *edit.Node, v any) error { return n.Update(ctx, v) },
			"set":    func(ctx context.Context, n *edit.Node, v any) error { return n.Set(ctx, v) },
		}

		if bc.leaf != nil {
			calls["leaf_calls"] = bc.leaf
		}

		for callName, call := range calls {
			for _, count := range []int{50, 100, 200, 400} {
				text, v := bc.text(count), bc.value(count)

				b.Run(fmt.Sprintf("%s_%s_%d", name, callName, count), func(b *testing.B) {
					b.ReportAllocs()

					for b.Loop() {
						b.StopTimer()

						doc := yamltest.FirstDocument(b, text, yamltest.NoSizeLimits()...)

						d, err := edit.New(doc)
						require.NoError(b, err)

						b.StartTimer()

						require.NoError(b, d.Apply(b.Context(), bc.path,
							func(ctx context.Context, n *edit.Node) error {
								return call(ctx, n, v)
							}))
					}
				})
			}
		}
	}
}
