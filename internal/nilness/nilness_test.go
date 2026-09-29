package nilness_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/internal/nilness"
)

type pointee struct{}

func TestIsNil(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		v    any
		want bool
	}{
		"nil interface":     {v: nil, want: true},
		"nil pointer":       {v: (*pointee)(nil), want: true},
		"nil func":          {v: (func())(nil), want: true},
		"non-nil pointer":   {v: &pointee{}, want: false},
		"non-nil func":      {v: func() {}, want: false},
		"nil map":           {v: map[string]int(nil), want: false},
		"nil slice":         {v: []int(nil), want: false},
		"zero struct value": {v: pointee{}, want: false},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, nilness.IsNil(tc.v))
		})
	}
}
