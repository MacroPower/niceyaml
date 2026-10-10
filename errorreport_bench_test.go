package niceyaml_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/paths"
)

func BenchmarkNewErrorReport(b *testing.B) {
	const n = 500

	var text strings.Builder

	for i := range n {
		fmt.Fprintf(&text, "k%d: v%d\n", i, i)
	}

	for _, k := range []int{1, 10, 50} {
		sources := make([]*niceyaml.Source, k)
		for i := range sources {
			sources[i] = niceyaml.NewSourceFromString(text.String(), niceyaml.WithName(fmt.Sprintf("s%d.yaml", i)))
		}

		// The children spread over the sources in turn, so the tree
		// touches every source.
		children := make([]error, 0, n)
		for i := range n {
			child := niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child(fmt.Sprintf("k%d", i))))
			children = append(children, sources[i%k].Bind(child))
		}

		var bound *niceyaml.SourceError

		require.ErrorAs(b, sources[0].Bind(niceyaml.NewError("root",
			niceyaml.AtPath(paths.Current().Child("k0")),
			niceyaml.WithDetails(children...))), &bound)

		b.Run(fmt.Sprintf("sources_%d", k), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				niceyaml.NewErrorReport(bound)
			}
		})
	}
}
