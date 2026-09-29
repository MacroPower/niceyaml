package niceyaml_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/paths"
)

func BenchmarkError_Unwrap(b *testing.B) {
	nested := make([]error, 50)
	for i := range nested {
		nested[i] = fmt.Errorf("violation %d", i)
	}

	err := niceyaml.WrapError(errors.New("schema"), niceyaml.WithErrors(nested...))

	b.ReportAllocs()

	for b.Loop() {
		_ = err.Unwrap()
	}
}

func BenchmarkSourceError_ExcerptOneLine(b *testing.B) {
	for _, n := range []int{500, 4000} {
		// A flow sequence on one line, with a violation at each item, puts
		// every location of the tree on line 0.
		items := strings.TrimSuffix(strings.Repeat("0,", n), ",")
		source := niceyaml.NewSourceFromString("[" + items + "]\n")

		doc, err := source.Document()
		require.NoError(b, err)

		errs := make([]error, 0, n)
		for i := range n {
			errs = append(errs, niceyaml.NewError(fmt.Sprintf("v%d", i),
				niceyaml.AtPath(paths.Root().Index(i))))
		}

		var bound *niceyaml.SourceError

		require.ErrorAs(b, doc.Bind(niceyaml.NewError("schema violations", niceyaml.WithErrors(errs...))), &bound)

		b.Run(fmt.Sprintf("errors_%d", n), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_, _ = bound.Excerpt(2)
			}
		})
	}
}

func BenchmarkSourceError_Excerpts(b *testing.B) {
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
			child := niceyaml.NewError("bad", niceyaml.AtPath(paths.Root().Child(fmt.Sprintf("k%d", i))))
			children = append(children, sources[i%k].Bind(child))
		}

		var bound *niceyaml.SourceError

		require.ErrorAs(b, sources[0].Bind(niceyaml.NewError("root",
			niceyaml.AtPath(paths.Root().Child("k0")),
			niceyaml.WithErrors(children...))), &bound)

		b.Run(fmt.Sprintf("sources_%d", k), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				for range bound.Excerpts(2) {
				}
			}
		})
	}
}

func BenchmarkErrorTree_LeftDeepJoin(b *testing.B) {
	const n = 10000

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("f.yaml"))

	// A loop that joins each new error onto the ones before nests each
	// join in the next.
	var joined error

	for i := range n {
		joined = errors.Join(joined, niceyaml.NewError(fmt.Sprintf("error %d", i),
			niceyaml.AtPath(paths.Root().Child("a"))))
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

func BenchmarkSourceBind_SentinelChain(b *testing.B) {
	source := niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("f.yaml"))
	sentinel := errors.New("invalid")

	for _, depth := range []int{400, 1600} {
		// Each level classifies the one below it with the sentinel, as a
		// caller does with fmt.Errorf("%w: %w", ErrSentinel, err).
		var err error = niceyaml.NewError("bad", niceyaml.AtPath(paths.Root().Child("a")))

		for range depth - 1 {
			err = fmt.Errorf("%w: %w", sentinel, err)
		}

		b.Run(fmt.Sprintf("depth_%d", depth), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_ = source.Bind(err).Error()
			}
		})
	}
}
