package excerpt_test

import (
	"slices"
	"testing"

	"github.com/goccy/go-yaml/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/internal/excerpt"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/style/kind"
	"go.jacobcolvin.com/niceyaml/tokens"
)

// docLines holds the lines of the document every test marks.
var docLines = line.NewLines(tokens.Tokenize(stringtest.LinesLF("a: 1", "bb: 22")))

// mark returns the mark of an error at pos in docLines, with the ranges
// [excerpt.Ranges] gives the token there.
func mark(pos position.Position, msg string) excerpt.Mark {
	return excerpt.Mark{Message: msg, Ranges: excerpt.Ranges(docLines, nil, nil, pos), Pos: pos}
}

// warning returns m as the mark of a warning.
func warning(m excerpt.Mark) excerpt.Mark {
	m.Warning = true

	return m
}

func TestAnnotate(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		marks       []excerpt.Mark
		overlays    line.Overlays
		annotations line.Annotations
		want        bool
	}{
		"message under its token": {
			marks:       []excerpt.Mark{mark(position.New(1, 4), "too big")},
			overlays:    yamltest.ErrorOverlays(position.NewSpan(4, 6)),
			annotations: line.Annotations{yamltest.ErrorAnnotation(position.NewSpan(4, 6), "too big")},
			want:        true,
		},
		"message on the spaces before a token": {
			marks:       []excerpt.Mark{mark(position.New(1, 3), "too big")},
			overlays:    yamltest.ErrorOverlays(position.NewSpan(4, 6)),
			annotations: line.Annotations{yamltest.ErrorAnnotation(position.NewSpan(4, 6), "too big")},
			want:        true,
		},
		"no message gets a caret run": {
			marks:       []excerpt.Mark{mark(position.New(1, 4), "")},
			overlays:    yamltest.ErrorOverlays(position.NewSpan(4, 6)),
			annotations: line.Annotations{yamltest.ErrorAnnotation(position.NewSpan(4, 6), "")},
			want:        true,
		},
		"a warning marks in the warning kinds": {
			marks: []excerpt.Mark{warning(mark(position.New(1, 4), "warning: big"))},
			overlays: line.Overlays{
				{Cols: position.NewSpan(4, 6), Kind: kind.GenericWarn},
			},
			annotations: line.Annotations{
				{Content: "warning: big", Kind: kind.TextWarn, Placement: line.Below, Cols: position.NewSpan(4, 6)},
			},
			want: true,
		},
		"a line of both severities without messages gets a caret run for each": {
			marks: []excerpt.Mark{
				mark(position.New(1, 0), ""),
				warning(mark(position.New(1, 4), "")),
			},
			overlays: line.Overlays{
				{Cols: position.NewSpan(0, 2), Kind: kind.GenericError},
				{Cols: position.NewSpan(4, 6), Kind: kind.GenericWarn},
			},
			annotations: line.Annotations{
				yamltest.ErrorAnnotation(position.NewSpan(0, 2), ""),
				{Kind: kind.TextWarn, Placement: line.Below, Cols: position.NewSpan(4, 6)},
			},
			want: true,
		},
		"repeats read once": {
			marks: []excerpt.Mark{
				mark(position.New(1, 4), "too big"),
				mark(position.New(1, 4), "too big"),
			},
			overlays:    yamltest.ErrorOverlays(position.NewSpan(4, 6)),
			annotations: line.Annotations{yamltest.ErrorAnnotation(position.NewSpan(4, 6), "too big")},
			want:        true,
		},
		"tabs become spaces": {
			marks:       []excerpt.Mark{mark(position.New(1, 4), "a\tb")},
			overlays:    yamltest.ErrorOverlays(position.NewSpan(4, 6)),
			annotations: line.Annotations{yamltest.ErrorAnnotation(position.NewSpan(4, 6), "a    b")},
			want:        true,
		},
		"messages on one line get an annotation each": {
			marks: []excerpt.Mark{
				mark(position.New(1, 4), "too big"),
				mark(position.New(1, 0), "bad key"),
			},
			overlays: yamltest.ErrorOverlays(position.NewSpan(4, 6), position.NewSpan(0, 2)),
			annotations: line.Annotations{
				yamltest.ErrorAnnotation(position.NewSpan(4, 6), "too big"),
				yamltest.ErrorAnnotation(position.NewSpan(0, 2), "bad key"),
			},
			want: true,
		},
		// A location with no highlight on its line gets no overlay, and the
		// annotation of no width at its column marks the line.
		"column past the end of its line": {
			marks:       []excerpt.Mark{{Message: "far", Pos: position.New(1, 99)}},
			annotations: line.Annotations{yamltest.ErrorAnnotation(position.NewSpan(6, 6), "far")},
			want:        true,
		},
		"column past the end of its line without a message": {
			marks:       []excerpt.Mark{{Pos: position.New(1, 99)}},
			annotations: line.Annotations{yamltest.ErrorAnnotation(position.NewSpan(6, 6), "")},
			want:        true,
		},
		"a count labels the line below its carets": {
			marks: []excerpt.Mark{
				mark(position.New(1, 4), "too big"),
				{Pos: position.New(1, 0), More: 5},
				{Pos: position.New(1, 0), More: 5},
			},
			overlays: yamltest.ErrorOverlays(position.NewSpan(4, 6)),
			annotations: line.Annotations{
				yamltest.ErrorAnnotation(position.NewSpan(4, 6), "too big"),
				{Content: "and 5 more on this line", Kind: kind.UISeparator, Placement: line.Below},
			},
			want: true,
		},
		"a count alone does not mark a line": {
			marks: []excerpt.Mark{{Pos: position.New(1, 0), More: 5}},
			annotations: line.Annotations{
				{Content: "and 5 more on this line", Kind: kind.UISeparator, Placement: line.Below},
			},
		},
		"line outside the source": {
			marks: []excerpt.Mark{{Message: "gone", Pos: position.New(9, 0)}, {Pos: position.New(9, 0), More: 2}},
		},
		"no marks": {},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := line.NewView(docLines)

			assert.Equal(t, tc.want, excerpt.Annotate(view, docLines, tc.marks))
			assert.Equal(t, tc.overlays, view.Overlays(1))
			assert.Equal(t, tc.annotations, view.Annotations(1))
		})
	}

	t.Run("range across lines labels each line it covers", func(t *testing.T) {
		t.Parallel()

		view := line.NewView(docLines)
		rng := position.NewRange(position.New(0, 3), position.New(1, 2))

		assert.True(t, excerpt.Annotate(view, docLines, []excerpt.Mark{{
			Message: "spans lines",
			Ranges:  position.Ranges{rng},
			Pos:     rng.Start,
		}}))

		// The message goes on the line of the mark, and the line the range
		// runs on to gets a caret run alone.
		assert.Equal(t, yamltest.ErrorOverlays(position.NewSpan(3, 4)), view.Overlays(0))
		assert.Equal(
			t,
			line.Annotations{yamltest.ErrorAnnotation(position.NewSpan(3, 4), "spans lines")},
			view.Annotations(0),
		)
		assert.Equal(t, yamltest.ErrorOverlays(position.NewSpan(0, 2)), view.Overlays(1))
		assert.Equal(t, line.Annotations{yamltest.ErrorAnnotation(position.NewSpan(0, 2), "")}, view.Annotations(1))
	})

	t.Run("view without the line", func(t *testing.T) {
		t.Parallel()

		view := line.NewView(docLines).Slice(position.NewSpan(0, 1))
		require.Equal(t, 1, view.Count())

		assert.False(t, excerpt.Annotate(view, docLines, []excerpt.Mark{mark(position.New(1, 4), "x")}))
		assert.Empty(t, view.Annotations(0))
	})
}

func TestLimit(t *testing.T) {
	t.Parallel()

	// At returns a mark at col of line ln with a message that names both.
	at := func(ln, col int) excerpt.Mark {
		pos := position.New(ln, col)

		return excerpt.Mark{Message: pos.String(), Pos: pos, Ranges: position.Ranges{position.NewRange(pos, pos)}}
	}

	more := func(ln, n int) excerpt.Mark {
		return excerpt.Mark{Pos: position.New(ln, 0), More: n}
	}

	tcs := map[string]struct {
		marks []excerpt.Mark
		want  []excerpt.Mark
		n     int
	}{
		"no marks": {
			n: 2,
		},
		"a line at the limit keeps every mark": {
			marks: []excerpt.Mark{at(0, 4), at(0, 2), at(1, 0)},
			n:     2,
			want:  []excerpt.Mark{at(0, 4), at(0, 2), at(1, 0)},
		},
		"a line past the limit keeps its first columns in the order given": {
			marks: []excerpt.Mark{at(0, 6), at(0, 4), at(0, 2), at(0, 0)},
			n:     2,
			want:  []excerpt.Mark{at(0, 2), at(0, 0), more(0, 2)},
		},
		"each line counts its own marks": {
			marks: []excerpt.Mark{at(2, 0), at(0, 0), at(2, 1), at(0, 1), at(2, 2), at(1, 0), at(0, 2), at(2, 3)},
			n:     2,
			want:  []excerpt.Mark{at(2, 0), at(0, 0), at(2, 1), at(0, 1), at(1, 0), more(0, 1), more(2, 2)},
		},
		"marks at one column keep the order given": {
			marks: []excerpt.Mark{
				{Message: "a", Pos: position.New(0, 1)},
				{Message: "b", Pos: position.New(0, 1)},
				{Message: "c", Pos: position.New(0, 1)},
				{Message: "d", Pos: position.New(0, 0)},
			},
			n: 2,
			want: []excerpt.Mark{
				{Message: "a", Pos: position.New(0, 1)},
				{Message: "d", Pos: position.New(0, 0)},
				more(0, 2),
			},
		},
		"a repeat does not count": {
			marks: []excerpt.Mark{at(0, 0), at(0, 0), at(0, 2), at(0, 2), at(0, 4), at(0, 4)},
			n:     2,
			want:  []excerpt.Mark{at(0, 0), at(0, 2), more(0, 1)},
		},
		"repeats alone do not add a count": {
			marks: []excerpt.Mark{at(0, 0), at(0, 0), at(0, 2)},
			n:     2,
			want:  []excerpt.Mark{at(0, 0), at(0, 2)},
		},
		"marks that differ in one field do not repeat": {
			marks: []excerpt.Mark{
				at(0, 0),
				warning(at(0, 0)),
				{Message: "other", Pos: position.New(0, 0), Ranges: at(0, 0).Ranges},
				{Message: at(0, 0).Message, Pos: position.New(0, 0)},
			},
			n:    1,
			want: []excerpt.Mark{at(0, 0), more(0, 3)},
		},
		"zero does not limit": {
			marks: []excerpt.Mark{at(0, 0), at(0, 2), at(0, 4)},
			want:  []excerpt.Mark{at(0, 0), at(0, 2), at(0, 4)},
		},
		"a negative limit does not limit": {
			marks: []excerpt.Mark{at(0, 0), at(0, 2), at(0, 4)},
			n:     -1,
			want:  []excerpt.Mark{at(0, 0), at(0, 2), at(0, 4)},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			given := slices.Clone(tc.marks)

			assert.Equal(t, tc.want, excerpt.Limit(tc.marks, tc.n))
			assert.Equal(t, given, tc.marks, "the marks given stay as they were")
		})
	}
}

func TestLines(t *testing.T) {
	t.Parallel()

	lines := line.NewLines(tokens.Tokenize(stringtest.LinesLF("a: 1", "b: 2", "", "c: 3", "d: 4")))

	// Over returns a mark at the start of a range that runs from one
	// position to the other.
	over := func(start, end position.Position) excerpt.Mark {
		return excerpt.Mark{Pos: start, Ranges: position.Ranges{position.NewRange(start, end)}}
	}

	tcs := map[string]struct {
		marks []excerpt.Mark
		want  []int
	}{
		"no marks": {},
		"the line of each mark, ascending": {
			marks: []excerpt.Mark{
				over(position.New(3, 0), position.New(3, 1)),
				over(position.New(0, 0), position.New(0, 1)),
			},
			want: []int{0, 3},
		},
		"a line that several marks share comes one time": {
			marks: []excerpt.Mark{
				over(position.New(1, 0), position.New(1, 1)),
				over(position.New(1, 3), position.New(1, 4)),
				over(position.New(1, 0), position.New(1, 1)),
			},
			want: []int{1},
		},
		"each line that a range covers": {
			marks: []excerpt.Mark{over(position.New(0, 3), position.New(3, 2))},
			want:  []int{0, 1, 3},
		},
		"a mark without ranges": {
			marks: []excerpt.Mark{{Pos: position.New(2, 0)}},
			want:  []int{2},
		},
		"ranges away from the line of the mark": {
			marks: []excerpt.Mark{{
				Pos:    position.New(0, 0),
				Ranges: position.Ranges{yamltest.LineRange(3, 0, 1), yamltest.LineRange(4, 0, 1)},
			}},
			want: []int{0, 3, 4},
		},
		"a mark outside the lines": {
			marks: []excerpt.Mark{
				{Pos: position.New(-1, 0)},
				over(position.New(9, 0), position.New(9, 1)),
				over(position.New(4, 0), position.New(9, 1)),
			},
			want: []int{4},
		},
		"a mark that counts the rest of a line": {
			marks: []excerpt.Mark{
				over(position.New(0, 0), position.New(0, 1)),
				{Pos: position.New(1, 0), More: 3},
			},
			want: []int{0},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := excerpt.Lines(lines, tc.marks)
			assert.Equal(t, tc.want, got)

			// The lines are those that Annotate decorates on a view of
			// every line.
			view := line.NewView(lines)
			excerpt.Annotate(view, lines, tc.marks)

			var decorated []int

			for i := range view.Hunks(0).All() {
				decorated = append(decorated, i)
			}

			assert.Equal(t, decorated, got)
		})
	}
}

func TestRanges(t *testing.T) {
	t.Parallel()

	value := docLines.TokenAt(position.New(1, 4))
	require.NotNil(t, value)

	rng := func(sl, sc, el, ec int) *position.Range {
		r := position.NewRange(position.New(sl, sc), position.New(el, ec))

		return &r
	}

	tcs := map[string]struct {
		rng  *position.Range
		tk   *token.Token
		pos  position.Position
		want position.Ranges
	}{
		"token at the position": {
			pos:  position.New(0, 3),
			want: position.Ranges{yamltest.LineRange(0, 3, 4)},
		},
		"a given token wins over the position": {
			tk:   value,
			pos:  position.New(0, 0),
			want: position.Ranges{yamltest.LineRange(1, 4, 6)},
		},
		"range": {
			rng:  rng(0, 0, 1, 2),
			want: position.Ranges{*rng(0, 0, 1, 2)},
		},
		"range past the last line": {
			rng:  rng(1, 0, 7, 3),
			want: position.Ranges{yamltest.LineRange(1, 0, 6)},
		},
		"range that ends before it starts": {
			rng:  rng(1, 4, 1, 2),
			want: position.Ranges{yamltest.LineRange(1, 4, 4)},
		},
		"end before the first column": {
			rng:  rng(0, 1, 1, -3),
			want: position.Ranges{*rng(0, 1, 1, 0)},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, excerpt.Ranges(docLines, tc.rng, tc.tk, tc.pos))
		})
	}
}

func TestCover(t *testing.T) {
	t.Parallel()

	at := position.New(2, 5)

	assert.Equal(t, position.NewRange(at, at), excerpt.Cover(nil, at))
	assert.Equal(t,
		position.NewRange(position.New(0, 1), position.New(3, 2)),
		excerpt.Cover(position.Ranges{yamltest.LineRange(0, 1, 4), yamltest.LineRange(3, 0, 2)}, at),
	)
}
