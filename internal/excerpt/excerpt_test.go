package excerpt_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/excerpt"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/style/kind"
	"go.jacobcolvin.com/niceyaml/tokens"
)

const doc = "a: 1\nbb: 22\n"

// mark returns the mark of an error at pos in lines, with the ranges
// [excerpt.Ranges] gives the token there.
func mark(lines line.Lines, pos position.Position, msg string) excerpt.Mark {
	return excerpt.Mark{Message: msg, Ranges: excerpt.Ranges(lines, nil, nil, pos), Pos: pos}
}

func below(col int, content string) line.Annotation {
	return line.Annotation{Content: content, Kind: kind.TextError, Placement: line.Below, Col: col}
}

// warning returns m as the mark of a warning.
func warning(m excerpt.Mark) excerpt.Mark {
	m.Warning = true

	return m
}

func overlay(cols position.Span) line.Overlay {
	return line.Overlay{Cols: cols, Kind: kind.GenericError}
}

func TestAnnotate(t *testing.T) {
	t.Parallel()

	lines := line.NewLines(tokens.Tokenize(doc))

	tcs := map[string]struct {
		marks       []excerpt.Mark
		overlays    line.Overlays
		annotations line.Annotations
		want        bool
	}{
		"message under its token": {
			marks:       []excerpt.Mark{mark(lines, position.New(1, 4), "too big")},
			overlays:    line.Overlays{overlay(position.NewSpan(4, 6))},
			annotations: line.Annotations{below(4, "too big")},
			want:        true,
		},
		"message on the spaces before a token": {
			marks:       []excerpt.Mark{mark(lines, position.New(1, 3), "too big")},
			overlays:    line.Overlays{overlay(position.NewSpan(4, 6))},
			annotations: line.Annotations{below(4, "too big")},
			want:        true,
		},
		"no message gets a caret run": {
			marks:       []excerpt.Mark{mark(lines, position.New(1, 4), "")},
			overlays:    line.Overlays{overlay(position.NewSpan(4, 6))},
			annotations: line.Annotations{below(4, "")},
			want:        true,
		},
		"a warning marks in the warning kinds": {
			marks: []excerpt.Mark{warning(mark(lines, position.New(1, 4), "warning: big"))},
			overlays: line.Overlays{
				{Cols: position.NewSpan(4, 6), Kind: kind.GenericWarn},
			},
			annotations: line.Annotations{
				{Content: "warning: big", Kind: kind.TextWarn, Placement: line.Below, Col: 4},
			},
			want: true,
		},
		"a line of both severities without messages gets a caret run for each": {
			marks: []excerpt.Mark{
				mark(lines, position.New(1, 0), ""),
				warning(mark(lines, position.New(1, 4), "")),
			},
			overlays: line.Overlays{
				overlay(position.NewSpan(0, 2)),
				{Cols: position.NewSpan(4, 6), Kind: kind.GenericWarn},
			},
			annotations: line.Annotations{
				below(0, ""),
				{Kind: kind.TextWarn, Placement: line.Below, Col: 4},
			},
			want: true,
		},
		"repeats read once": {
			marks: []excerpt.Mark{
				mark(lines, position.New(1, 4), "too big"),
				mark(lines, position.New(1, 4), "too big"),
			},
			overlays:    line.Overlays{overlay(position.NewSpan(4, 6))},
			annotations: line.Annotations{below(4, "too big")},
			want:        true,
		},
		"tabs become spaces": {
			marks:       []excerpt.Mark{mark(lines, position.New(1, 4), "a\tb")},
			overlays:    line.Overlays{overlay(position.NewSpan(4, 6))},
			annotations: line.Annotations{below(4, "a    b")},
			want:        true,
		},
		"column past the end of its line": {
			marks:       []excerpt.Mark{{Message: "far", Pos: position.New(1, 99)}},
			overlays:    line.Overlays{overlay(position.NewSpan(6, 6))},
			annotations: line.Annotations{below(6, "far")},
			want:        true,
		},
		"line outside the source": {
			marks: []excerpt.Mark{{Message: "gone", Pos: position.New(9, 0)}},
		},
		"no marks": {},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := line.NewView(lines)

			assert.Equal(t, tc.want, excerpt.Annotate(view, lines, tc.marks))
			assert.Equal(t, tc.overlays, view.Overlays(1))
			assert.Equal(t, tc.annotations, view.Annotations(1))
		})
	}

	t.Run("view without the line", func(t *testing.T) {
		t.Parallel()

		view := line.NewView(lines, position.NewSpan(0, 1))
		require.Equal(t, 1, view.Count())

		assert.False(t, excerpt.Annotate(view, lines, []excerpt.Mark{mark(lines, position.New(1, 4), "x")}))
		assert.Empty(t, view.Annotations(0))
	})
}

func TestRanges(t *testing.T) {
	t.Parallel()

	lines := line.NewLines(tokens.Tokenize(doc))
	value := lines.TokenAt(position.New(1, 4))
	require.NotNil(t, value)

	rng := func(sl, sc, el, ec int) *position.Range {
		r := position.NewRange(position.New(sl, sc), position.New(el, ec))

		return &r
	}

	tcs := map[string]struct {
		rng  *position.Range
		pos  position.Position
		want position.Ranges
	}{
		"token at the position": {
			pos:  position.New(0, 3),
			want: position.Ranges{*rng(0, 3, 0, 4)},
		},
		"range": {
			rng:  rng(0, 0, 1, 2),
			want: position.Ranges{*rng(0, 0, 1, 2)},
		},
		"range past the last line": {
			rng:  rng(1, 0, 7, 3),
			want: position.Ranges{*rng(1, 0, 1, 6)},
		},
		"range that ends before it starts": {
			rng:  rng(1, 4, 1, 2),
			want: position.Ranges{*rng(1, 4, 1, 4)},
		},
		"end before the first column": {
			rng:  rng(0, 1, 1, -3),
			want: position.Ranges{*rng(0, 1, 1, 0)},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, excerpt.Ranges(lines, tc.rng, nil, tc.pos))
		})
	}

	t.Run("token", func(t *testing.T) {
		t.Parallel()

		assert.Equal(t, position.Ranges{*rng(1, 4, 1, 6)}, excerpt.Ranges(lines, nil, value, position.New(0, 0)))
	})
}

func TestCover(t *testing.T) {
	t.Parallel()

	at := position.New(2, 5)

	assert.Equal(t, position.NewRange(at, at), excerpt.Cover(nil, at))
	assert.Equal(t,
		position.NewRange(position.New(0, 1), position.New(3, 2)),
		excerpt.Cover(position.Ranges{
			position.NewRange(position.New(0, 1), position.New(0, 4)),
			position.NewRange(position.New(3, 0), position.New(3, 2)),
		}, at),
	)
}
