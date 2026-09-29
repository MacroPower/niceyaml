package niceyaml_test

import (
	"errors"
	"fmt"
	"testing"

	"go.jacobcolvin.com/niceyaml"
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
