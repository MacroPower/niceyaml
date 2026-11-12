package edit_test

import (
	"context"
	"fmt"
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
