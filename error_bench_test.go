package niceyaml_test

import (
	"errors"
	"fmt"
	"testing"

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
