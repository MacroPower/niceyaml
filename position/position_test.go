package position_test

import (
	"math"
	"testing"

	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/position"
)

const (
	// MaxCol matches the internal constant in position.go.
	maxCol = 1_000_000
)

func TestNew(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		line int
		col  int
		want position.Position
	}{
		"zero values": {
			line: 0,
			col:  0,
			want: position.Position{Line: 0, Col: 0},
		},
		"positive values": {
			line: 5,
			col:  10,
			want: position.Position{Line: 5, Col: 10},
		},
		"large values": {
			line: 10000,
			col:  500,
			want: position.Position{Line: 10000, Col: 500},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := position.New(tc.line, tc.col)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestNewFromToken(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		token *token.Token
		want  position.Position
	}{
		"nil token": {
			token: nil,
			want:  position.Position{Line: 0, Col: 0},
		},
		"token with nil position": {
			token: &token.Token{Position: nil},
			want:  position.Position{Line: 0, Col: 0},
		},
		"first line first column": {
			token: &token.Token{
				Position: &token.Position{Line: 1, Column: 1},
			},
			want: position.Position{Line: 0, Col: 0},
		},
		"converts to 0-indexed": {
			token: &token.Token{
				Position: &token.Position{Line: 5, Column: 10},
			},
			want: position.Position{Line: 4, Col: 9},
		},
		"clamps negative to zero": {
			token: &token.Token{
				Position: &token.Position{Line: 0, Column: 0},
			},
			want: position.Position{Line: 0, Col: 0},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := position.NewFromToken(tc.token)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPosition_String(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		pos  position.Position
		want string
	}{
		"zero position": {
			pos:  position.New(0, 0),
			want: "1:1",
		},
		"first line tenth column": {
			pos:  position.New(0, 9),
			want: "1:10",
		},
		"line 5 col 15 (0-indexed)": {
			pos:  position.New(4, 14),
			want: "5:15",
		},
		"large values": {
			pos:  position.New(999, 499),
			want: "1000:500",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.pos.String()
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestRange_String(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		r    position.Range
		want string
	}{
		"single point": {
			r:    position.NewRange(position.New(0, 0), position.New(0, 0)),
			want: "1:1-1:1",
		},
		"single line range": {
			r:    position.NewRange(position.New(0, 5), position.New(0, 10)),
			want: "1:6-1:11",
		},
		"multi-line range": {
			r:    position.NewRange(position.New(2, 3), position.New(5, 8)),
			want: "3:4-6:9",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.r.String()
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestRanges_String(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		ranges position.Ranges
		want   string
	}{
		"empty ranges": {
			ranges: nil,
			want:   "",
		},
		"single range": {
			ranges: position.Ranges{
				position.NewRange(position.New(0, 0), position.New(0, 5)),
			},
			want: "1:1-1:6",
		},
		"multiple ranges": {
			ranges: position.Ranges{
				position.NewRange(position.New(0, 0), position.New(0, 5)),
				position.NewRange(position.New(2, 3), position.New(4, 8)),
			},
			want: "1:1-1:6, 3:4-5:9",
		},
		"three ranges": {
			ranges: position.Ranges{
				position.NewRange(position.New(0, 0), position.New(0, 1)),
				position.NewRange(position.New(1, 0), position.New(1, 1)),
				position.NewRange(position.New(2, 0), position.New(2, 1)),
			},
			want: "1:1-1:2, 2:1-2:2, 3:1-3:2",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.ranges.String()
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestNewRange(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		start position.Position
		end   position.Position
		want  position.Range
	}{
		"single line range": {
			start: position.New(1, 0),
			end:   position.New(1, 10),
			want:  position.Range{Start: position.Position{Line: 1, Col: 0}, End: position.Position{Line: 1, Col: 10}},
		},
		"multi-line range": {
			start: position.New(0, 5),
			end:   position.New(3, 15),
			want:  position.Range{Start: position.Position{Line: 0, Col: 5}, End: position.Position{Line: 3, Col: 15}},
		},
		"empty range": {
			start: position.New(2, 3),
			end:   position.New(2, 3),
			want:  position.Range{Start: position.Position{Line: 2, Col: 3}, End: position.Position{Line: 2, Col: 3}},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := position.NewRange(tc.start, tc.end)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestRange_Contains(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		r    position.Range
		pos  position.Position
		want bool
	}{
		"position at start": {
			r:    position.NewRange(position.New(1, 5), position.New(3, 10)),
			pos:  position.New(1, 5),
			want: true,
		},
		"position at end": {
			r:    position.NewRange(position.New(1, 5), position.New(3, 10)),
			pos:  position.New(3, 10),
			want: false,
		},
		"position between start and end": {
			r:    position.NewRange(position.New(1, 5), position.New(3, 10)),
			pos:  position.New(2, 0),
			want: true,
		},
		"position before start same line": {
			r:    position.NewRange(position.New(1, 5), position.New(3, 10)),
			pos:  position.New(1, 4),
			want: false,
		},
		"position before start earlier line": {
			r:    position.NewRange(position.New(1, 5), position.New(3, 10)),
			pos:  position.New(0, 10),
			want: false,
		},
		"position after end same line": {
			r:    position.NewRange(position.New(1, 5), position.New(3, 10)),
			pos:  position.New(3, 11),
			want: false,
		},
		"position after end later line": {
			r:    position.NewRange(position.New(1, 5), position.New(3, 10)),
			pos:  position.New(4, 0),
			want: false,
		},
		"single line range position inside": {
			r:    position.NewRange(position.New(5, 10), position.New(5, 20)),
			pos:  position.New(5, 15),
			want: true,
		},
		"single line range position at start col": {
			r:    position.NewRange(position.New(5, 10), position.New(5, 20)),
			pos:  position.New(5, 10),
			want: true,
		},
		"single line range position at end col": {
			r:    position.NewRange(position.New(5, 10), position.New(5, 20)),
			pos:  position.New(5, 20),
			want: false,
		},
		"multi-line range middle line any col": {
			r:    position.NewRange(position.New(1, 50), position.New(5, 2)),
			pos:  position.New(3, 999),
			want: true,
		},
		"empty range start equals end": {
			r:    position.NewRange(position.New(2, 5), position.New(2, 5)),
			pos:  position.New(2, 5),
			want: false,
		},
		"position on start line after start col": {
			r:    position.NewRange(position.New(1, 5), position.New(3, 10)),
			pos:  position.New(1, 6),
			want: true,
		},
		"position on end line before end col": {
			r:    position.NewRange(position.New(1, 5), position.New(3, 10)),
			pos:  position.New(3, 9),
			want: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.r.Contains(tc.pos)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestRange_SliceLines(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input position.Range
		want  position.Ranges
	}{
		"single line range": {
			input: position.NewRange(position.New(1, 5), position.New(1, 10)),
			want: position.Ranges{
				position.NewRange(position.New(1, 5), position.New(1, 10)),
			},
		},
		"empty range": {
			input: position.NewRange(position.New(2, 3), position.New(2, 3)),
			want: position.Ranges{
				position.NewRange(position.New(2, 3), position.New(2, 3)),
			},
		},
		"two line range": {
			input: position.NewRange(position.New(1, 5), position.New(2, 10)),
			want: position.Ranges{
				position.NewRange(position.New(1, 5), position.New(1, maxCol)),
				position.NewRange(position.New(2, 0), position.New(2, 10)),
			},
		},
		"three line range": {
			input: position.NewRange(position.New(1, 5), position.New(3, 10)),
			want: position.Ranges{
				position.NewRange(position.New(1, 5), position.New(1, maxCol)),
				position.NewRange(position.New(2, 0), position.New(2, maxCol)),
				position.NewRange(position.New(3, 0), position.New(3, 10)),
			},
		},
		"multi-line range with zero end col stops before end line": {
			input: position.NewRange(position.New(0, 10), position.New(2, 0)),
			want: position.Ranges{
				position.NewRange(position.New(0, 10), position.New(0, maxCol)),
				position.NewRange(position.New(1, 0), position.New(1, maxCol)),
			},
		},
		"two line range with zero end col": {
			input: position.NewRange(position.New(0, 10), position.New(1, 0)),
			want: position.Ranges{
				position.NewRange(position.New(0, 10), position.New(0, maxCol)),
			},
		},
		"range starting at col 0": {
			input: position.NewRange(position.New(5, 0), position.New(7, 15)),
			want: position.Ranges{
				position.NewRange(position.New(5, 0), position.New(5, maxCol)),
				position.NewRange(position.New(6, 0), position.New(6, maxCol)),
				position.NewRange(position.New(7, 0), position.New(7, 15)),
			},
		},
		"inverted range with zero end col": {
			input: position.NewRange(position.New(2, 0), position.New(1, 0)),
			want:  nil,
		},
		"inverted range with nonzero end col": {
			input: position.NewRange(position.New(2, 0), position.New(1, 5)),
			want:  nil,
		},
		"inverted range across several lines": {
			input: position.NewRange(position.New(5, 3), position.New(1, 4)),
			want:  nil,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.input.SliceLines()
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestRanges_UniqueValues(t *testing.T) {
	t.Parallel()

	t.Run("empty ranges returns nil", func(t *testing.T) {
		t.Parallel()

		var rs position.Ranges

		assert.Nil(t, rs.UniqueValues())
	})

	t.Run("single range returns unchanged", func(t *testing.T) {
		t.Parallel()

		r := position.NewRange(position.New(0, 0), position.New(1, 0))
		rs := position.Ranges{r}
		assert.Equal(t, position.Ranges{r}, rs.UniqueValues())
	})

	t.Run("removes adjacent duplicates", func(t *testing.T) {
		t.Parallel()

		r := position.NewRange(position.New(0, 0), position.New(1, 0))
		rs := position.Ranges{r, r, r}
		assert.Equal(t, position.Ranges{r}, rs.UniqueValues())
	})

	t.Run("removes scattered duplicates", func(t *testing.T) {
		t.Parallel()

		r1 := position.NewRange(position.New(0, 0), position.New(1, 0))
		r2 := position.NewRange(position.New(2, 0), position.New(3, 0))
		rs := position.Ranges{r1, r2, r1, r2}
		assert.Equal(t, position.Ranges{r1, r2}, rs.UniqueValues())
	})

	t.Run("preserves insertion order", func(t *testing.T) {
		t.Parallel()

		r1 := position.NewRange(position.New(0, 0), position.New(1, 0))
		r2 := position.NewRange(position.New(2, 0), position.New(3, 0))
		r3 := position.NewRange(position.New(4, 0), position.New(5, 0))
		rs := position.Ranges{r3, r1, r2, r1, r3}
		assert.Equal(t, position.Ranges{r3, r1, r2}, rs.UniqueValues())
	})

	t.Run("all duplicates returns single item", func(t *testing.T) {
		t.Parallel()

		r := position.NewRange(position.New(5, 5), position.New(10, 10))
		rs := position.Ranges{r, r, r, r, r}
		assert.Equal(t, position.Ranges{r}, rs.UniqueValues())
	})
}

func TestRanges_LineIndices(t *testing.T) {
	t.Parallel()

	t.Run("empty ranges returns nil", func(t *testing.T) {
		t.Parallel()

		var rs position.Ranges

		assert.Nil(t, rs.LineIndices())
	})

	t.Run("single range returns single line", func(t *testing.T) {
		t.Parallel()

		r := position.NewRange(position.New(5, 0), position.New(5, 10))
		rs := position.Ranges{r}
		assert.Equal(t, []int{5}, rs.LineIndices())
	})

	t.Run("multiple ranges returns all covered lines", func(t *testing.T) {
		t.Parallel()

		r1 := position.NewRange(position.New(0, 0), position.New(0, 5))
		r2 := position.NewRange(position.New(3, 0), position.New(4, 5)) // Spans lines 3-4.
		r3 := position.NewRange(position.New(7, 0), position.New(7, 10))
		rs := position.Ranges{r1, r2, r3}
		assert.Equal(t, []int{0, 3, 4, 7}, rs.LineIndices())
	})

	t.Run("excludes end line touched only at column 0", func(t *testing.T) {
		t.Parallel()

		r := position.NewRange(position.New(3, 2), position.New(5, 0))
		rs := position.Ranges{r}
		assert.Equal(t, []int{3, 4}, rs.LineIndices())
	})

	t.Run("inverted ranges cover no lines", func(t *testing.T) {
		t.Parallel()

		rs := position.Ranges{
			position.NewRange(position.New(2, 0), position.New(1, 0)),
			position.NewRange(position.New(2, 0), position.New(1, 5)),
		}
		assert.Empty(t, rs.LineIndices())
	})

	t.Run("includes duplicate lines from overlapping ranges", func(t *testing.T) {
		t.Parallel()

		r1 := position.NewRange(position.New(5, 0), position.New(5, 5))
		r2 := position.NewRange(position.New(5, 10), position.New(5, 15))
		rs := position.Ranges{r1, r2}
		assert.Equal(t, []int{5, 5}, rs.LineIndices())
	})
}

func TestNewSpan(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		start int
		end   int
		want  position.Span
	}{
		"zero values": {
			start: 0,
			end:   0,
			want:  position.Span{Start: 0, End: 0},
		},
		"positive values": {
			start: 5,
			end:   10,
			want:  position.Span{Start: 5, End: 10},
		},
		"large values": {
			start: 10000,
			end:   50000,
			want:  position.Span{Start: 10000, End: 50000},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := position.NewSpan(tc.start, tc.end)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSpan_Len(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input position.Span
		want  int
	}{
		"zero length": {
			input: position.NewSpan(5, 5),
			want:  0,
		},
		"positive length": {
			input: position.NewSpan(0, 10),
			want:  10,
		},
		"non-zero start": {
			input: position.NewSpan(5, 15),
			want:  10,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.input.Len()
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSpan_Contains(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input position.Span
		v     int
		want  bool
	}{
		"value at start": {
			input: position.NewSpan(5, 10),
			v:     5,
			want:  true,
		},
		"value at end": {
			input: position.NewSpan(5, 10),
			v:     10,
			want:  false,
		},
		"value inside": {
			input: position.NewSpan(5, 10),
			v:     7,
			want:  true,
		},
		"value before start": {
			input: position.NewSpan(5, 10),
			v:     4,
			want:  false,
		},
		"value after end": {
			input: position.NewSpan(5, 10),
			v:     11,
			want:  false,
		},
		"empty interval": {
			input: position.NewSpan(5, 5),
			v:     5,
			want:  false,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.input.Contains(tc.v)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSpan_Overlaps(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		s     position.Span
		other position.Span
		want  bool
	}{
		"identical spans overlap": {
			s:     position.NewSpan(5, 10),
			other: position.NewSpan(5, 10),
			want:  true,
		},
		"partial overlap at end": {
			s:     position.NewSpan(0, 10),
			other: position.NewSpan(5, 15),
			want:  true,
		},
		"partial overlap at start": {
			s:     position.NewSpan(5, 15),
			other: position.NewSpan(0, 10),
			want:  true,
		},
		"one contains other": {
			s:     position.NewSpan(0, 20),
			other: position.NewSpan(5, 10),
			want:  true,
		},
		"contained by other": {
			s:     position.NewSpan(5, 10),
			other: position.NewSpan(0, 20),
			want:  true,
		},
		"adjacent spans do not overlap": {
			s:     position.NewSpan(0, 5),
			other: position.NewSpan(5, 10),
			want:  false,
		},
		"adjacent spans reversed do not overlap": {
			s:     position.NewSpan(5, 10),
			other: position.NewSpan(0, 5),
			want:  false,
		},
		"disjoint spans before": {
			s:     position.NewSpan(0, 5),
			other: position.NewSpan(10, 15),
			want:  false,
		},
		"disjoint spans after": {
			s:     position.NewSpan(10, 15),
			other: position.NewSpan(0, 5),
			want:  false,
		},
		"empty span does not overlap": {
			s:     position.NewSpan(5, 5),
			other: position.NewSpan(0, 10),
			want:  false,
		},
		"span does not overlap with empty": {
			s:     position.NewSpan(0, 10),
			other: position.NewSpan(5, 5),
			want:  false,
		},
		"both empty at same point do not overlap": {
			s:     position.NewSpan(5, 5),
			other: position.NewSpan(5, 5),
			want:  false,
		},
		"single element spans overlap": {
			s:     position.NewSpan(5, 6),
			other: position.NewSpan(5, 6),
			want:  true,
		},
		"single element overlaps with larger": {
			s:     position.NewSpan(5, 6),
			other: position.NewSpan(0, 10),
			want:  true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.s.Overlaps(tc.other)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSpan_String(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input position.Span
		want  string
	}{
		"zero values": {
			input: position.NewSpan(0, 0),
			want:  "0-0",
		},
		"positive values": {
			input: position.NewSpan(5, 10),
			want:  "5-10",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.input.String()
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestGroupIndices(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		indices []int
		context int
		want    position.Spans
	}{
		"empty indices": {
			indices: []int{},
			context: 2,
			want:    nil,
		},
		"single index": {
			indices: []int{5},
			context: 2,
			want:    position.Spans{position.NewSpan(5, 6)},
		},
		"two indices within threshold merge": {
			// Context=2, threshold=5: indices merge if idx < lastEnd+5.
			// LastEnd=1, so merge if idx < 6.
			indices: []int{0, 4},
			context: 2,
			want:    position.Spans{position.NewSpan(0, 5)},
		},
		"two indices at threshold boundary merge": {
			// Context=2, threshold=5: indices merge if idx < lastEnd+5.
			// LastEnd=1, so merge if idx < 6, meaning 5 merges.
			indices: []int{0, 5},
			context: 2,
			want:    position.Spans{position.NewSpan(0, 6)},
		},
		"two indices beyond threshold separate": {
			// Context=2, threshold=5: indices merge if idx < lastEnd+5.
			// LastEnd=1, so merge if idx < 6, meaning 6 doesn't merge.
			indices: []int{0, 6},
			context: 2,
			want:    position.Spans{position.NewSpan(0, 1), position.NewSpan(6, 7)},
		},
		"multiple indices all merge": {
			indices: []int{0, 2, 4, 6},
			context: 2,
			want:    position.Spans{position.NewSpan(0, 7)},
		},
		"multiple indices form separate groups": {
			indices: []int{0, 2, 10, 12},
			context: 2,
			want:    position.Spans{position.NewSpan(0, 3), position.NewSpan(10, 13)},
		},
		"context zero only merges adjacent": {
			// Context=0, threshold=1: merge if idx < lastEnd+1.
			// Adjacent indices (consecutive) merge, non-adjacent don't.
			indices: []int{0, 1, 2, 5, 6},
			context: 0,
			want:    position.Spans{position.NewSpan(0, 3), position.NewSpan(5, 7)},
		},
		"context zero non-adjacent separate": {
			indices: []int{0, 2, 4},
			context: 0,
			want:    position.Spans{position.NewSpan(0, 1), position.NewSpan(2, 3), position.NewSpan(4, 5)},
		},
		"large context merges all": {
			indices: []int{0, 50, 100},
			context: 50,
			want:    position.Spans{position.NewSpan(0, 101)},
		},
		"duplicate indices handled": {
			indices: []int{5, 5, 5},
			context: 2,
			want:    position.Spans{position.NewSpan(5, 6)},
		},
		"unsorted indices are sorted first": {
			indices: []int{12, 0, 10, 2},
			context: 2,
			want:    position.Spans{position.NewSpan(0, 3), position.NewSpan(10, 13)},
		},
		"negative context counts as zero": {
			indices: []int{0, 1, 3},
			context: -1,
			want:    position.Spans{position.NewSpan(0, 2), position.NewSpan(3, 4)},
		},
		"huge context merges everything": {
			indices: []int{0, 50, 100},
			context: math.MaxInt,
			want:    position.Spans{position.NewSpan(0, 101)},
		},
		"half of max int context merges everything": {
			indices: []int{0, 50, 100},
			context: math.MaxInt / 2,
			want:    position.Spans{position.NewSpan(0, 101)},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := position.GroupIndices(tc.indices, tc.context)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSpans_Expand(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input  position.Spans
		amount int
		want   position.Spans
	}{
		"nil spans": {
			input:  nil,
			amount: 2,
			want:   nil,
		},
		"empty spans": {
			input:  position.Spans{},
			amount: 2,
			want:   nil,
		},
		"single span expand by zero": {
			input:  position.Spans{position.NewSpan(5, 10)},
			amount: 0,
			want:   position.Spans{position.NewSpan(5, 10)},
		},
		"single span expand": {
			input:  position.Spans{position.NewSpan(5, 10)},
			amount: 2,
			want:   position.Spans{position.NewSpan(3, 12)},
		},
		"multiple spans expand": {
			input:  position.Spans{position.NewSpan(5, 10), position.NewSpan(20, 25)},
			amount: 3,
			want:   position.Spans{position.NewSpan(2, 13), position.NewSpan(17, 28)},
		},
		"expand can go negative": {
			input:  position.Spans{position.NewSpan(1, 3)},
			amount: 5,
			want:   position.Spans{position.NewSpan(-4, 8)},
		},
		"huge amount saturates instead of wrapping": {
			input:  position.Spans{position.NewSpan(-2, 3)},
			amount: math.MaxInt,
			want:   position.Spans{position.NewSpan(math.MinInt, math.MaxInt)},
		},
		"negative amount shrinks": {
			input:  position.Spans{position.NewSpan(2, 8)},
			amount: -1,
			want:   position.Spans{position.NewSpan(3, 7)},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.input.Expand(tc.amount)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSpans_Clamp(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input    position.Spans
		min, max int
		want     position.Spans
	}{
		"nil spans": {
			input: nil,
			min:   0, max: 100,
			want: nil,
		},
		"empty spans": {
			input: position.Spans{},
			min:   0, max: 100,
			want: nil,
		},
		"no clamping needed": {
			input: position.Spans{position.NewSpan(5, 10)},
			min:   0, max: 100,
			want: position.Spans{position.NewSpan(5, 10)},
		},
		"clamp start below min": {
			input: position.Spans{position.NewSpan(-5, 10)},
			min:   0, max: 100,
			want: position.Spans{position.NewSpan(0, 10)},
		},
		"clamp end above max": {
			input: position.Spans{position.NewSpan(5, 150)},
			min:   0, max: 100,
			want: position.Spans{position.NewSpan(5, 100)},
		},
		"clamp both": {
			input: position.Spans{position.NewSpan(-10, 200)},
			min:   0, max: 100,
			want: position.Spans{position.NewSpan(0, 100)},
		},
		"multiple spans clamp": {
			input: position.Spans{position.NewSpan(-5, 10), position.NewSpan(90, 150)},
			min:   0, max: 100,
			want: position.Spans{position.NewSpan(0, 10), position.NewSpan(90, 100)},
		},
		"span past the upper bound is dropped": {
			input: position.Spans{position.NewSpan(9, 11)},
			min:   0, max: 5,
			want: nil,
		},
		"span below the lower bound is dropped": {
			input: position.Spans{position.NewSpan(-4, -1)},
			min:   0, max: 5,
			want: nil,
		},
		"inverted span is dropped": {
			input: position.Spans{position.NewSpan(3, 1)},
			min:   0, max: 5,
			want: nil,
		},
		"empty span is dropped": {
			input: position.Spans{position.NewSpan(2, 2)},
			min:   0, max: 5,
			want: nil,
		},
		"dropped spans leave the rest in order": {
			input: position.Spans{position.NewSpan(0, 2), position.NewSpan(7, 9), position.NewSpan(3, 5)},
			min:   0, max: 5,
			want: position.Spans{position.NewSpan(0, 2), position.NewSpan(3, 5)},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.input.Clamp(tc.min, tc.max)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSpans_Chained(t *testing.T) {
	t.Parallel()

	// Test the typical usage pattern: GroupIndices -> Expand -> Clamp.
	indices := []int{5, 15}
	context := 2

	spans := position.GroupIndices(indices, context)
	assert.Equal(t, position.Spans{
		position.NewSpan(5, 6),
		position.NewSpan(15, 16),
	}, spans)

	expanded := spans.Expand(context)
	assert.Equal(t, position.Spans{
		position.NewSpan(3, 8),
		position.NewSpan(13, 18),
	}, expanded)

	clamped := expanded.Clamp(0, 20)
	assert.Equal(t, position.Spans{
		position.NewSpan(3, 8),
		position.NewSpan(13, 18),
	}, clamped)

	// Test full chain in one call.
	got := position.GroupIndices(indices, context).Expand(context).Clamp(0, 20)
	assert.Equal(t, clamped, got)
}

func TestContextSpans(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		indices []int
		context int
		total   int
		want    position.Spans
	}{
		"empty indices": {
			indices: nil,
			context: 2,
			total:   10,
			want:    nil,
		},
		"single index with context": {
			indices: []int{5},
			context: 2,
			total:   10,
			want:    position.Spans{position.NewSpan(3, 8)},
		},
		"context clamped at both ends": {
			indices: []int{0, 9},
			context: 2,
			total:   10,
			want:    position.Spans{position.NewSpan(0, 3), position.NewSpan(7, 10)},
		},
		"windows that touch merge": {
			indices: []int{2, 7},
			context: 2,
			total:   20,
			want:    position.Spans{position.NewSpan(0, 10)},
		},
		"distant indices stay apart": {
			indices: []int{2, 12},
			context: 2,
			total:   20,
			want:    position.Spans{position.NewSpan(0, 5), position.NewSpan(10, 15)},
		},
		"zero context": {
			indices: []int{4, 5, 9},
			context: 0,
			total:   20,
			want:    position.Spans{position.NewSpan(4, 6), position.NewSpan(9, 10)},
		},
		"unsorted indices": {
			indices: []int{12, 2},
			context: 1,
			total:   20,
			want:    position.Spans{position.NewSpan(1, 4), position.NewSpan(11, 14)},
		},
		"negative context counts as zero": {
			indices: []int{0, 9},
			context: -1,
			total:   10,
			want:    position.Spans{position.NewSpan(0, 1), position.NewSpan(9, 10)},
		},
		"index at total contributes nothing": {
			indices: []int{10},
			context: 1,
			total:   5,
			want:    nil,
		},
		"index past total is dropped and the rest remain": {
			indices: []int{1, 10},
			context: 1,
			total:   5,
			want:    position.Spans{position.NewSpan(0, 3)},
		},
		"negative index contributes nothing": {
			indices: []int{-3},
			context: 1,
			total:   5,
			want:    nil,
		},
		"context at total covers every line": {
			indices: []int{2},
			context: 10,
			total:   10,
			want:    position.Spans{position.NewSpan(0, 10)},
		},
		"huge context covers every line once": {
			indices: []int{0, 5},
			context: math.MaxInt,
			total:   6,
			want:    position.Spans{position.NewSpan(0, 6)},
		},
		"half of max int context covers every line once": {
			indices: []int{0, 5},
			context: math.MaxInt / 2,
			total:   6,
			want:    position.Spans{position.NewSpan(0, 6)},
		},
		"zero total yields nothing": {
			indices: []int{0},
			context: 2,
			total:   0,
			want:    nil,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := position.ContextSpans(tc.indices, tc.context, tc.total)

			assert.Equal(t, tc.want, got)
		})
	}
}
