package position_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/macropower/niceyaml/position"
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

func TestNewRanges(t *testing.T) {
	t.Parallel()

	t.Run("no arguments", func(t *testing.T) {
		t.Parallel()

		rs := position.NewRanges()
		assert.Empty(t, rs.Values())
	})

	t.Run("single range", func(t *testing.T) {
		t.Parallel()

		r := position.NewRange(position.New(0, 0), position.New(1, 0))
		rs := position.NewRanges(r)
		assert.Equal(t, []position.Range{r}, rs.Values())
	})

	t.Run("multiple ranges", func(t *testing.T) {
		t.Parallel()

		r1 := position.NewRange(position.New(0, 0), position.New(1, 0))
		r2 := position.NewRange(position.New(2, 0), position.New(3, 0))
		r3 := position.NewRange(position.New(4, 0), position.New(5, 0))
		rs := position.NewRanges(r1, r2, r3)
		assert.Equal(t, []position.Range{r1, r2, r3}, rs.Values())
	})
}

func TestRanges_Add(t *testing.T) {
	t.Parallel()

	t.Run("add to empty", func(t *testing.T) {
		t.Parallel()

		rs := position.NewRanges()
		r := position.NewRange(position.New(0, 0), position.New(1, 0))
		rs.Add(r)
		assert.Equal(t, []position.Range{r}, rs.Values())
	})

	t.Run("add multiple sequentially", func(t *testing.T) {
		t.Parallel()

		rs := position.NewRanges()
		r1 := position.NewRange(position.New(0, 0), position.New(1, 0))
		r2 := position.NewRange(position.New(2, 0), position.New(3, 0))

		rs.Add(r1)
		rs.Add(r2)
		assert.Equal(t, []position.Range{r1, r2}, rs.Values())
	})

	t.Run("add duplicates allowed", func(t *testing.T) {
		t.Parallel()

		rs := position.NewRanges()
		r := position.NewRange(position.New(0, 0), position.New(1, 0))
		rs.Add(r)
		rs.Add(r)
		assert.Equal(t, []position.Range{r, r}, rs.Values())
	})
}

func TestRanges_Values(t *testing.T) {
	t.Parallel()

	t.Run("empty ranges returns nil", func(t *testing.T) {
		t.Parallel()

		rs := position.NewRanges()
		assert.Nil(t, rs.Values())
	})

	t.Run("returns all ranges in order", func(t *testing.T) {
		t.Parallel()

		r1 := position.NewRange(position.New(0, 0), position.New(1, 0))
		r2 := position.NewRange(position.New(2, 0), position.New(3, 0))
		rs := position.NewRanges(r1, r2)
		assert.Equal(t, []position.Range{r1, r2}, rs.Values())
	})

	t.Run("returns duplicates", func(t *testing.T) {
		t.Parallel()

		r := position.NewRange(position.New(0, 0), position.New(1, 0))
		rs := position.NewRanges(r, r)
		assert.Equal(t, []position.Range{r, r}, rs.Values())
	})
}

func TestRanges_UniqueValues(t *testing.T) {
	t.Parallel()

	t.Run("empty ranges returns nil", func(t *testing.T) {
		t.Parallel()

		rs := position.NewRanges()
		assert.Nil(t, rs.UniqueValues())
	})

	t.Run("single range returns unchanged", func(t *testing.T) {
		t.Parallel()

		r := position.NewRange(position.New(0, 0), position.New(1, 0))
		rs := position.NewRanges(r)
		assert.Equal(t, []position.Range{r}, rs.UniqueValues())
	})

	t.Run("removes adjacent duplicates", func(t *testing.T) {
		t.Parallel()

		r := position.NewRange(position.New(0, 0), position.New(1, 0))
		rs := position.NewRanges(r, r, r)
		assert.Equal(t, []position.Range{r}, rs.UniqueValues())
	})

	t.Run("removes scattered duplicates", func(t *testing.T) {
		t.Parallel()

		r1 := position.NewRange(position.New(0, 0), position.New(1, 0))
		r2 := position.NewRange(position.New(2, 0), position.New(3, 0))
		rs := position.NewRanges(r1, r2, r1, r2)
		assert.Equal(t, []position.Range{r1, r2}, rs.UniqueValues())
	})

	t.Run("preserves insertion order", func(t *testing.T) {
		t.Parallel()

		r1 := position.NewRange(position.New(0, 0), position.New(1, 0))
		r2 := position.NewRange(position.New(2, 0), position.New(3, 0))
		r3 := position.NewRange(position.New(4, 0), position.New(5, 0))
		rs := position.NewRanges(r3, r1, r2, r1, r3)
		assert.Equal(t, []position.Range{r3, r1, r2}, rs.UniqueValues())
	})

	t.Run("all duplicates returns single item", func(t *testing.T) {
		t.Parallel()

		r := position.NewRange(position.New(5, 5), position.New(10, 10))
		rs := position.NewRanges(r, r, r, r, r)
		assert.Equal(t, []position.Range{r}, rs.UniqueValues())
	})
}
