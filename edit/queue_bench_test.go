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

func BenchmarkDoc_Apply_Queue(b *testing.B) {
	// Each Apply sets the count of replicas of each service. The Sets wait
	// for the end of the Apply, which parses and checks the text once.
	path := paths.MustParse("$.services.*.replicas")

	for _, n := range []int{50, 200, 800} {
		text := services(n)

		b.Run(fmt.Sprintf("services_%d", n), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				b.StopTimer()

				d, err := edit.New(yamltest.FirstDocument(b, text))
				require.NoError(b, err)

				b.StartTimer()

				require.NoError(b, d.Apply(b.Context(), path, func(ctx context.Context, n *edit.Node) error {
					return n.Set(ctx, 3)
				}))
			}
		})
	}
}

func BenchmarkDoc_Apply_Queue_Scalars(b *testing.B) {
	// Each Apply sets each item of a sequence of small scalars. Each Set
	// that waits costs about the same whatever the number of Sets before
	// it. So the time of the Apply grows about linearly with the number of
	// items.
	path := paths.MustParse("$.s[*]")

	for _, n := range []int{4000, 16000, 32000} {
		text := "s:\n" + strings.Repeat("  - 1\n", n)

		b.Run(fmt.Sprintf("items_%d", n), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				b.StopTimer()

				d, err := edit.New(yamltest.FirstDocument(b, text, yamltest.NoSizeLimits()...))
				require.NoError(b, err)

				b.StartTimer()

				require.NoError(b, d.Apply(b.Context(), path, func(ctx context.Context, n *edit.Node) error {
					return n.Set(ctx, 3)
				}))
			}
		})
	}
}
