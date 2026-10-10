package yamltest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/position"
)

func TestLineRange(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		want             position.Range
		line, start, end int
	}{
		"first line": {
			start: 2, end: 5,
			want: position.NewRange(position.New(0, 2), position.New(0, 5)),
		},
		"later line": {
			line: 3, start: 0, end: 1,
			want: position.NewRange(position.New(3, 0), position.New(3, 1)),
		},
		"empty range": {
			line: 1, start: 4, end: 4,
			want: position.NewRange(position.New(1, 4), position.New(1, 4)),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, yamltest.LineRange(tc.line, tc.start, tc.end))
		})
	}
}
