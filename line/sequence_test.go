package line_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/tokens"
)

func TestSequence(t *testing.T) {
	t.Parallel()

	lines := line.NewLines(tokens.Tokenize(stringtest.Input(`
		a: 1
		b: 2
		c: 3
		d: 4
	`)))
	require.Equal(t, 4, lines.Len())

	view := line.NewView(lines)
	gapped := view.Slice(position.NewSpan(0, 1), position.NewSpan(2, 4))

	tcs := map[string]struct {
		seq   line.Sequence
		spans []position.Span
		want  []int // The index in the content of each line yielded.
	}{
		"lines": {
			seq:  lines,
			want: []int{0, 1, 2, 3},
		},
		"lines within a span": {
			seq:   lines,
			spans: []position.Span{position.NewSpan(1, 3)},
			want:  []int{1, 2},
		},
		"zero lines": {
			seq: line.Lines{},
		},
		"view of every line": {
			seq:  view,
			want: []int{0, 1, 2, 3},
		},
		"view of later lines keeps the indices of the content": {
			seq:  view.Slice(position.NewSpan(2, 4)),
			want: []int{2, 3},
		},
		"view with a gap skips the indices it does not hold": {
			seq:  gapped,
			want: []int{0, 2, 3},
		},
		"view with a gap within a span": {
			seq:   gapped,
			spans: []position.Span{position.NewSpan(1, 3)},
			want:  []int{2},
		},
		"nil view": {
			seq: (*line.View)(nil),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var got []int

			for i, l := range tc.seq.All(tc.spans...) {
				assert.Same(t, lines.Line(i), l, "line %d", i)

				got = append(got, i)
			}

			assert.Equal(t, tc.want, got)
		})
	}
}
