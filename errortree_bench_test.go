package niceyaml_test

import (
	"errors"
	"fmt"
	"testing"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

func BenchmarkNewErrorReport(b *testing.B) {
	const n = 500

	text := yamltest.GenerateYAML(n)

	for _, k := range []int{1, 10, 50} {
		sources := make([]*niceyaml.Source, k)
		for i := range sources {
			sources[i] = niceyaml.NewSourceFromString(text, niceyaml.WithName(fmt.Sprintf("s%d.yaml", i)))
		}

		// The children spread over the sources in turn, so the tree
		// touches every source.
		children := make([]error, 0, n)
		for i := range n {
			children = append(children, sources[i%k].Bind(yamltest.ErrorAt("bad", fmt.Sprintf("key_%d", i))))
		}

		bound := yamltest.RequireSourceError(b, sources[0].Bind(niceyaml.NewError("root",
			niceyaml.AtPath(paths.Current().Child("key_0")),
			niceyaml.WithDetails(children...))))

		b.Run(fmt.Sprintf("sources_%d", k), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				niceyaml.NewErrorReport(bound)
			}
		})
	}
}

func BenchmarkErrorTree_LeftDeepJoin(b *testing.B) {
	const n = 10000

	source := errorTreeSource()

	// A loop that joins each new error onto the ones before nests each
	// join in the next.
	var joined error

	for i := range n {
		joined = errors.Join(joined, yamltest.ErrorAt(fmt.Sprintf("error %d", i), "a"))
	}

	tcs := map[string]error{
		"unbound": joined,
		"bound":   source.Bind(joined),
	}

	for name, err := range tcs {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_ = niceyaml.NewErrorTree(err)
			}
		})
	}
}
