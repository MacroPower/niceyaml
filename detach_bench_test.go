package niceyaml_test

import (
	"fmt"
	"strings"
	"testing"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

func BenchmarkDetach_OneLine(b *testing.B) {
	for _, n := range []int{500, 4000} {
		// Detach keeps the lines of every problem and does not draw any of
		// them, so its time grows with the problems alone.
		items := strings.TrimSuffix(strings.Repeat("0,", n), ",")
		doc := yamltest.FirstDocument(b, "["+items+"]\n")

		errs := make([]error, 0, n)
		for i := range n {
			errs = append(errs, niceyaml.NewError(fmt.Sprintf("v%d", i),
				niceyaml.AtPath(paths.Current().Index(i))))
		}

		err := doc.Bind(niceyaml.NewSummary("schema violations", errs...))

		b.Run(fmt.Sprintf("errors_%d", n), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_ = niceyaml.Detach(err) //nolint:errcheck // Only the call counts.
			}
		})
	}
}
