package line_test

import (
	"iter"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
)

// TestSequence checks how spans select lines. Lines.All and View.All yield
// the lines within any of the spans, once each and in content order, with
// the index of each in the content. NewView and View.Slice create a view
// that holds the lines the spans select, and the view still reaches every
// line of the content by its index.
func TestSequence(t *testing.T) {
	t.Parallel()

	lines := newLines(stringtest.JoinLF(letterRows(6)...))

	tcs := map[string]struct {
		spans []position.Span
		want  []int // The index in the content of each line selected.
	}{
		"no spans": {
			want: []int{0, 1, 2, 3, 4, 5},
		},
		"one span": {
			spans: []position.Span{position.NewSpan(1, 3)},
			want:  []int{1, 2},
		},
		"out of order": {
			spans: []position.Span{position.NewSpan(4, 6), position.NewSpan(0, 2)},
			want:  []int{0, 1, 4, 5},
		},
		"nested": {
			spans: []position.Span{position.NewSpan(0, 5), position.NewSpan(1, 2)},
			want:  []int{0, 1, 2, 3, 4},
		},
		"overlapping": {
			spans: []position.Span{position.NewSpan(2, 5), position.NewSpan(0, 3)},
			want:  []int{0, 1, 2, 3, 4},
		},
		"out of order and overlapping": {
			spans: []position.Span{position.NewSpan(3, 5), position.NewSpan(0, 1), position.NewSpan(3, 4)},
			want:  []int{0, 3, 4},
		},
		"touching": {
			spans: []position.Span{position.NewSpan(2, 4), position.NewSpan(0, 2)},
			want:  []int{0, 1, 2, 3},
		},
		"repeated": {
			spans: []position.Span{position.NewSpan(1, 2), position.NewSpan(1, 2)},
			want:  []int{1},
		},
		"empty span": {
			spans: []position.Span{position.NewSpan(2, 2)},
		},
		"only empty spans": {
			spans: []position.Span{position.NewSpan(1, 1), position.NewSpan(2, 2)},
		},
		"empty span among real ones": {
			spans: []position.Span{position.NewSpan(4, 5), position.NewSpan(3, 3), position.NewSpan(1, 2)},
			want:  []int{1, 4},
		},
		"inverted": {
			spans: []position.Span{position.NewSpan(3, 1)},
		},
		"negative start": {
			spans: []position.Span{position.NewSpan(-3, 2)},
			want:  []int{0, 1},
		},
		"before the start": {
			spans: []position.Span{position.NewSpan(-9, -1)},
		},
		"end past the content": {
			spans: []position.Span{position.NewSpan(4, 99)},
			want:  []int{4, 5},
		},
		"past the end": {
			spans: []position.Span{position.NewSpan(10, 20)},
		},
		"clamped at both ends": {
			spans: []position.Span{position.NewSpan(-5, 1), position.NewSpan(3, 99)},
			want:  []int{0, 3, 4, 5},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assertSelects := func(via string, seq iter.Seq2[int, *line.Line]) {
				t.Helper()

				var got []int

				for i, l := range seq {
					assert.Same(t, lines.Line(i), l, "%s: line %d", via, i)

					got = append(got, i)
				}

				assert.Equal(t, tc.want, got, via)
			}

			sequences := map[string]line.Sequence{
				"Lines": lines,
				"View":  line.NewView(lines),
			}

			for via, seq := range sequences {
				assertSelects(via+".All", seq.All(tc.spans...))
			}

			views := map[string]*line.View{
				"NewView":    line.NewView(lines, tc.spans...),
				"View.Slice": line.NewView(lines).Slice(tc.spans...),
			}

			for via, view := range views {
				assertSelects(via, view.All())
				assert.Equal(t, len(tc.want), view.Count(), via)

				for i := range lines.Len() {
					assert.Same(t, lines.Line(i), view.Line(i), "%s: content line %d", via, i)
				}

				// Held returns the lines the view holds, each with the
				// number it has in the file, and they render as the view
				// does.
				held := view.Held()
				require.Equal(t, len(tc.want), held.Len(), via)

				for i, ci := range tc.want {
					assert.Same(t, lines.Line(ci), held.Line(i), "%s: held line %d", via, i)
					assert.Equal(t, ci+1, held.Line(i).Number(), "%s: held line %d", via, i)
				}

				assert.Equal(t, held.String(), view.String(), via)
			}
		})
	}
}
