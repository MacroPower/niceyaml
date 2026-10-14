package yamltest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
)

func TestReportTimePer(t *testing.T) {
	t.Parallel()

	// The benchmark does no work, so testing.Benchmark returns at once.
	// It stops the timer first, so the metric and the result read one
	// elapsed time.
	result := testing.Benchmark(func(b *testing.B) { //nolint:thelper // The closure is the benchmark, not a helper.
		b.StopTimer()
		yamltest.ReportTimePer(b, 4, "item")
	})

	want := float64(result.T.Nanoseconds()) / float64(result.N*4)
	assert.InDelta(t, want, result.Extra["ns/item"], 1e-12)
}
