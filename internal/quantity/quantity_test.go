package quantity_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/internal/quantity"
)

func TestBytes(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		want string
		n    int
	}{
		"mebibytes":     {n: 2 << 20, want: "2 MiB"},
		"kibibytes":     {n: 256 << 10, want: "256 KiB"},
		"partial unit":  {n: 1000, want: "1000 bytes"},
		"one byte":      {n: 1, want: "1 byte"},
		"no bytes":      {n: 0, want: "0 bytes"},
		"more than KiB": {n: 1<<10 + 1, want: "1025 bytes"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, quantity.Bytes(tc.n))
		})
	}
}

func TestPlural(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		want string
		n    int
	}{
		"none": {n: 0, want: "0 errors"},
		"one":  {n: 1, want: "1 error"},
		"many": {n: 3, want: "3 errors"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, quantity.Plural(tc.n, "error"))
		})
	}
}
