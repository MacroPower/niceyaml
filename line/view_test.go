package line_test

import (
	"math"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/style/kind"
	"go.jacobcolvin.com/niceyaml/tokens"
)

// newTestView creates a view over the lines of input.
func newTestView(t *testing.T, input string, wantLen int) *line.View {
	t.Helper()

	lines := line.NewLines(tokens.Tokenize(input))
	require.Equal(t, wantLen, lines.Len())

	return line.NewView(lines)
}

func TestNewView(t *testing.T) {
	t.Parallel()

	t.Run("over lines", func(t *testing.T) {
		t.Parallel()

		lines := line.NewLines(tokens.Tokenize("key: value\nother: data\n"))
		view := line.NewView(lines)

		assert.Equal(t, 2, view.Count())
		assert.Equal(t, lines, view.Lines())
		assert.Equal(t, 2, view.Lines().Len())
	})

	t.Run("over nil lines", func(t *testing.T) {
		t.Parallel()

		view := line.NewView(line.Lines{})

		assert.Equal(t, 0, view.Count())
		assert.True(t, view.Lines().IsEmpty())
		assert.Empty(t, view.String())

		for range view.All() {
			t.Fatal("expected no lines")
		}
	})

	t.Run("over empty lines", func(t *testing.T) {
		t.Parallel()

		view := line.NewView(line.Lines{})

		assert.Equal(t, 0, view.Count())
		assert.Empty(t, view.Lines())
		assert.Empty(t, view.String())
	})

	t.Run("zero value", func(t *testing.T) {
		t.Parallel()

		var view line.View

		assert.Equal(t, 0, view.Count())
		assert.True(t, view.Lines().IsEmpty())
		assert.Empty(t, view.String())

		// Range-taking methods are safe on an empty view.
		view.AddOverlay("test1", position.NewRange(position.New(0, 0), position.New(0, 5)))

		assert.Equal(t, 0, view.Slice(position.NewSpan(0, 5)).Count())
	})

	t.Run("nil view", func(t *testing.T) {
		t.Parallel()

		var view *line.View

		assert.Equal(t, 0, view.Count())
		assert.True(t, view.Lines().IsEmpty())
		assert.Nil(t, view.Clone())

		for range view.All() {
			t.Fatal("expected no lines")
		}
	})
}

func TestNewView_Spans(t *testing.T) {
	t.Parallel()

	lines := line.NewLines(tokens.Tokenize("a: 1\nb: 2\nc: 3\nd: 4\ne: 5\n"))

	tcs := map[string]struct {
		spans []position.Span
		want  []int
	}{
		"no spans": {
			want: []int{0, 1, 2, 3, 4},
		},
		"one span": {
			spans: []position.Span{position.NewSpan(1, 3)},
			want:  []int{1, 2},
		},
		"spans out of order and overlapping": {
			spans: []position.Span{position.NewSpan(3, 5), position.NewSpan(0, 1), position.NewSpan(3, 4)},
			want:  []int{0, 3, 4},
		},
		"span past the end": {
			spans: []position.Span{position.NewSpan(4, 9)},
			want:  []int{4},
		},
		"empty span": {
			spans: []position.Span{position.NewSpan(2, 2)},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := line.NewView(lines, tc.spans...)

			var got []int

			for i := range view.All() {
				got = append(got, i)
			}

			assert.Equal(t, tc.want, got)
			assert.Equal(t, lines, view.Lines())

			// The view holds what a slice of a view over every line holds.
			var sliced []int

			for i := range line.NewView(lines).Slice(tc.spans...).All() {
				sliced = append(sliced, i)
			}

			assert.Equal(t, sliced, got)
		})
	}
}

func TestView_Contains(t *testing.T) {
	t.Parallel()

	view := newTestView(t, "a: 1\nb: 2\nc: 3\n", 3)
	sliced := view.Slice(position.NewSpan(1, 3))

	var nilView *line.View

	tcs := map[string]struct {
		view *line.View
		i    int
		want bool
	}{
		"held by the whole view":  {view: view, i: 0, want: true},
		"held by a slice":         {view: sliced, i: 1, want: true},
		"dropped by a slice":      {view: sliced, i: 0, want: false},
		"before the content":      {view: view, i: -1, want: false},
		"past the content":        {view: view, i: 3, want: false},
		"nil view":                {view: nilView, i: 0, want: false},
		"zero view":               {view: &line.View{}, i: 0, want: false},
		"empty slice of the view": {view: view.Slice(position.NewSpan(0, 0)), i: 0, want: false},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.view.Contains(tc.i))
		})
	}
}

func TestView_All(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		key: value
		list:
		  - one
	`)

	t.Run("yields every index and line", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, input, 3)

		var (
			indices  []int
			contents []string
		)

		for i, ln := range view.All() {
			indices = append(indices, i)
			contents = append(contents, ln.Content())

			assert.Same(t, view.Lines().Line(i), ln)
		}

		assert.Equal(t, []int{0, 1, 2}, indices)
		assert.Equal(t, []string{"key: value", "list:", "  - one"}, contents)
	})

	t.Run("clamps spans", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, input, 3)

		var indices []int

		for i := range view.All(position.NewSpan(1, 99)) {
			indices = append(indices, i)
		}

		assert.Equal(t, []int{1, 2}, indices)
	})

	t.Run("spans select held lines in content order", func(t *testing.T) {
		t.Parallel()

		six := stringtest.Input(`
			a: 1
			b: 2
			c: 3
			d: 4
			e: 5
			f: 6
		`)

		// Sparse slices the view to lines 0, 2, and 5 before selecting.
		sparse := []position.Span{position.NewSpan(0, 1), position.NewSpan(2, 3), position.NewSpan(5, 6)}

		tcs := map[string]struct {
			held  []position.Span
			spans []position.Span
			want  []int
		}{
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
			"touching": {
				spans: []position.Span{position.NewSpan(2, 4), position.NewSpan(0, 2)},
				want:  []int{0, 1, 2, 3},
			},
			"empty span among real ones": {
				spans: []position.Span{position.NewSpan(4, 5), position.NewSpan(3, 3), position.NewSpan(1, 2)},
				want:  []int{1, 4},
			},
			"negative start": {
				spans: []position.Span{position.NewSpan(-3, 2)},
				want:  []int{0, 1},
			},
			"sparse view": {
				held:  sparse,
				spans: []position.Span{position.NewSpan(1, 5)},
				want:  []int{2},
			},
			"sparse view with overlapping spans": {
				held:  sparse,
				spans: []position.Span{position.NewSpan(4, 6), position.NewSpan(2, 5), position.NewSpan(0, 3)},
				want:  []int{0, 2, 5},
			},
			"spans over lines the view does not hold": {
				held:  sparse,
				spans: []position.Span{position.NewSpan(3, 5), position.NewSpan(1, 2)},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				view := newTestView(t, six, 6)
				if tc.held != nil {
					view = view.Slice(tc.held...)
				}

				var got []int

				for i := range view.All(tc.spans...) {
					got = append(got, i)
				}

				assert.Equal(t, tc.want, got)
			})
		}
	})

	t.Run("index reaches decoration", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, input, 3)
		view.SetFlag(1, line.FlagInserted)

		var flags []line.Flag

		for i := range view.All() {
			flags = append(flags, view.Flag(i))
		}

		assert.Equal(t, []line.Flag{line.FlagDefault, line.FlagInserted, line.FlagDefault}, flags)
	})
}

func TestView_Flag(t *testing.T) {
	t.Parallel()

	t.Run("defaults to FlagDefault", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "a: 1\nb: 2\n", 2)

		assert.Equal(t, line.FlagDefault, view.Flag(0))
		assert.Equal(t, line.FlagDefault, view.Flag(1))
	})

	t.Run("SetFlag sets one line only", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "a: 1\nb: 2\nc: 3\n", 3)
		view.SetFlag(1, line.FlagDeleted)

		assert.Equal(t, line.FlagDefault, view.Flag(0))
		assert.Equal(t, line.FlagDeleted, view.Flag(1))
		assert.Equal(t, line.FlagDefault, view.Flag(2))
	})

	t.Run("SetFlag overwrites", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "a: 1\n", 1)
		view.SetFlag(0, line.FlagInserted)
		view.SetFlag(0, line.FlagDeleted)

		assert.Equal(t, line.FlagDeleted, view.Flag(0))
	})

	t.Run("SetFlag to FlagDefault clears the flag", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "a: 1\nb: 2\n", 2)
		view.SetFlag(0, line.FlagInserted)
		view.SetFlag(0, line.FlagDefault)
		view.SetFlag(1, line.FlagDefault)

		assert.Equal(t, line.FlagDefault, view.Flag(0))
		assert.Equal(t, line.FlagDefault, view.Flag(1))
		assert.Equal(t, line.FlagDefault, view.Slice().Flag(0))
		assert.Equal(t, 0, view.Hunks(0).Count())
	})
}

func TestView_Annotate(t *testing.T) {
	t.Parallel()

	t.Run("no annotations by default", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "key: value\n", 1)

		assert.Nil(t, view.Annotations(0))
	})

	t.Run("add single annotation", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "key: value\n", 1)
		view.Annotate(0, line.Annotation{Content: "note", Placement: line.Below})

		require.Len(t, view.Annotations(0), 1)
		assert.Equal(t, "note", view.Annotations(0)[0].Content)
		assert.Equal(t, line.Below, view.Annotations(0)[0].Placement)
	})

	t.Run("add multiple annotations", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "key: value\n", 1)
		view.Annotate(0,
			line.Annotation{Content: "first", Placement: line.Above},
			line.Annotation{Content: "second", Placement: line.Below},
		)

		require.Len(t, view.Annotations(0), 2)
		assert.Equal(t, "first", view.Annotations(0)[0].Content)
		assert.Equal(t, "second", view.Annotations(0)[1].Content)
	})

	t.Run("accumulates annotations", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "key: value\n", 1)
		view.Annotate(0, line.Annotation{Content: "first"})
		view.Annotate(0, line.Annotation{Content: "second"})

		require.Len(t, view.Annotations(0), 2)
	})

	t.Run("annotates one line only", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "a: 1\nb: 2\n", 2)
		view.Annotate(1, line.Annotation{Content: "note"})

		assert.Empty(t, view.Annotations(0))
		require.Len(t, view.Annotations(1), 1)
	})
}

func TestView_AddLineOverlay(t *testing.T) {
	t.Parallel()

	t.Run("no overlays by default", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "key: value\n", 1)

		assert.Nil(t, view.Overlays(0))
	})

	t.Run("add single overlay", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "key: value\n", 1)
		view.AddLineOverlay(0, line.Overlay{
			Cols: position.NewSpan(0, 5),
			Kind: "test1",
		})

		require.Len(t, view.Overlays(0), 1)
		assert.Equal(t, position.NewSpan(0, 5), view.Overlays(0)[0].Cols)
		assert.Equal(t, kind.Kind("test1"), view.Overlays(0)[0].Kind)
	})

	t.Run("add multiple overlays", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "key: value\n", 1)
		view.AddLineOverlay(0,
			line.Overlay{Cols: position.NewSpan(0, 3), Kind: "test1"},
			line.Overlay{Cols: position.NewSpan(5, 10), Kind: "test2"},
		)

		require.Len(t, view.Overlays(0), 2)
		assert.Equal(t, position.NewSpan(0, 3), view.Overlays(0)[0].Cols)
		assert.Equal(t, position.NewSpan(5, 10), view.Overlays(0)[1].Cols)
	})

	t.Run("accumulates overlays", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "key: value\n", 1)
		view.AddLineOverlay(0, line.Overlay{Cols: position.NewSpan(0, 3), Kind: "test1"})
		view.AddLineOverlay(0, line.Overlay{Cols: position.NewSpan(5, 10), Kind: "test2"})

		require.Len(t, view.Overlays(0), 2)
	})

	t.Run("does not clamp", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "key: value\n", 1)

		// The clamped AddOverlay would cut this to the line width; the raw
		// method keeps the columns as given.
		view.AddLineOverlay(0, line.Overlay{Cols: position.NewSpan(-2, 99), Kind: "test1"})

		require.Len(t, view.Overlays(0), 1)
		assert.Equal(t, position.NewSpan(-2, 99), view.Overlays(0)[0].Cols)
	})

	t.Run("overlays one line only", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "a: 1\nb: 2\n", 2)
		view.AddLineOverlay(1, line.Overlay{Cols: position.NewSpan(0, 1), Kind: "test1"})

		assert.Empty(t, view.Overlays(0))
		require.Len(t, view.Overlays(1), 1)
	})
}

func TestView_AddOverlay(t *testing.T) {
	t.Parallel()

	t.Run("out of range lines are skipped", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "a: 1\nb: 2\n", 2)

		// A range that starts before the first line and ends past the last
		// applies only to the lines that exist.
		view.AddOverlay("test1", position.NewRange(
			position.New(-1, 0),
			position.New(5, 3),
		))

		require.Len(t, view.Overlays(0), 1)
		require.Len(t, view.Overlays(1), 1)

		// A range entirely outside the view is a no-op.
		view.AddOverlay("test2", position.NewRange(
			position.New(7, 0),
			position.New(7, 3),
		))

		assert.Len(t, view.Overlays(0), 1)
		assert.Len(t, view.Overlays(1), 1)
	})

	t.Run("single line range", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "key: value\n", 1)
		view.AddOverlay("test1", position.NewRange(
			position.New(0, 0),
			position.New(0, 5),
		))

		require.Len(t, view.Overlays(0), 1)
		assert.Equal(t, position.NewSpan(0, 5), view.Overlays(0)[0].Cols)
		assert.Equal(t, kind.Kind("test1"), view.Overlays(0)[0].Kind)
		assert.False(t, view.Overlays(0)[0].Blend)
	})

	t.Run("columns are clamped to the line", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "key: value\n", 1)
		view.AddOverlay("test1", position.NewRange(
			position.New(0, -3),
			position.New(0, 99),
		))

		require.Len(t, view.Overlays(0), 1)
		assert.Equal(t, position.NewSpan(0, len("key: value")), view.Overlays(0)[0].Cols)
	})

	t.Run("range covering no columns adds nothing", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "key: value\n", 1)

		// Empty at a column inside the line, and a range starting past the end.
		view.AddOverlay("test1",
			position.NewRange(position.New(0, 3), position.New(0, 3)),
			position.NewRange(position.New(0, 50), position.New(0, 60)),
		)

		assert.Empty(t, view.Overlays(0))
	})

	t.Run("multi-line range splits across lines", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			key1: value1
			key2: value2
			key3: value3
		`)
		view := newTestView(t, input, 3)

		// Add overlay spanning all three lines.
		view.AddOverlay("test2", position.NewRange(
			position.New(0, 3),
			position.New(2, 5),
		))

		// First line: col 3 to end of line.
		require.Len(t, view.Overlays(0), 1)
		assert.Equal(t, position.NewSpan(3, len("key1: value1")), view.Overlays(0)[0].Cols)
		assert.Equal(t, kind.Kind("test2"), view.Overlays(0)[0].Kind)

		// Middle line: full line.
		require.Len(t, view.Overlays(1), 1)
		assert.Equal(t, position.NewSpan(0, len("key2: value2")), view.Overlays(1)[0].Cols)
		assert.Equal(t, kind.Kind("test2"), view.Overlays(1)[0].Kind)

		// Last line: start to col 5.
		require.Len(t, view.Overlays(2), 1)
		assert.Equal(t, position.NewSpan(0, 5), view.Overlays(2)[0].Cols)
		assert.Equal(t, kind.Kind("test2"), view.Overlays(2)[0].Kind)
	})

	t.Run("multiple ranges", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			key1: value1
			key2: value2
		`)
		view := newTestView(t, input, 2)

		view.AddOverlay("test1",
			position.NewRange(position.New(0, 0), position.New(0, 4)),
			position.NewRange(position.New(1, 0), position.New(1, 4)),
		)

		require.Len(t, view.Overlays(0), 1)
		require.Len(t, view.Overlays(1), 1)
	})

	t.Run("inverted ranges add nothing", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "a: 1\nb: 2\nc: 3\n", 3)

		// Both ranges end on the line before they start, one at column 0 and
		// one past it.
		view.AddOverlay("test1",
			position.NewRange(position.New(2, 0), position.New(1, 0)),
			position.NewRange(position.New(2, 0), position.New(1, 3)),
		)

		for i := range view.All() {
			assert.Empty(t, view.Overlays(i), "line %d", i)
		}
	})

	t.Run("empty view no-op", func(t *testing.T) {
		t.Parallel()

		view := line.NewView(line.Lines{})

		// Should not panic on an empty view.
		view.AddOverlay("test1", position.NewRange(position.New(0, 0), position.New(0, 5)))

		assert.Equal(t, 0, view.Count())
	})
}

func TestView_BlendOverlay(t *testing.T) {
	t.Parallel()

	t.Run("marks overlays as blended", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "a: 1\nb: 2\n", 2)
		view.BlendOverlay("test1", position.NewRange(
			position.New(0, 0),
			position.New(1, 2),
		))

		require.Len(t, view.Overlays(0), 1)
		require.Len(t, view.Overlays(1), 1)
		assert.True(t, view.Overlays(0)[0].Blend)
		assert.True(t, view.Overlays(1)[0].Blend)
		assert.Equal(t, kind.Kind("test1"), view.Overlays(0)[0].Kind)
	})

	t.Run("clamps like AddOverlay", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "key: value\n", 1)
		view.BlendOverlay("test1",
			position.NewRange(position.New(-1, -5), position.New(9, 99)),
			position.NewRange(position.New(0, 50), position.New(0, 60)),
		)

		require.Len(t, view.Overlays(0), 1)
		assert.Equal(t, position.NewSpan(0, len("key: value")), view.Overlays(0)[0].Cols)
	})

	t.Run("mixes with replacing overlays in order", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "key: value\n", 1)
		view.AddOverlay("test1", position.NewRange(position.New(0, 0), position.New(0, 3)))
		view.BlendOverlay("test2", position.NewRange(position.New(0, 0), position.New(0, 3)))

		require.Len(t, view.Overlays(0), 2)
		assert.False(t, view.Overlays(0)[0].Blend)
		assert.True(t, view.Overlays(0)[1].Blend)
	})
}

func TestView_Clone(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		key: value
		list:
		  - one
	`)

	t.Run("shares lines", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, input, 3)
		clone := view.Clone()

		require.NotSame(t, view, clone)
		require.Equal(t, 3, clone.Count())
		assert.Equal(t, view.Lines().Content(), clone.Lines().Content())

		for i, ln := range view.All() {
			j, ok := clone.Index(ln)
			require.True(t, ok, "line %d", i)
			assert.Equal(t, i, j)
		}
	})

	t.Run("preserves annotations", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "key: value\n", 1)
		view.Annotate(0, line.Annotation{Content: "original note", Placement: line.Below})

		clone := view.Clone()

		// Verify annotations were copied.
		require.Len(t, clone.Annotations(0), 1)
		require.Len(t, view.Annotations(0), len(clone.Annotations(0)))
		assert.Equal(t, view.Annotations(0)[0].Content, clone.Annotations(0)[0].Content)
		assert.Len(t, clone.Annotations(0).Filter(line.Below), 1)

		// Modify clone annotations.
		clone.Annotate(0, line.Annotation{Content: "modified", Placement: line.Above})

		// Verify original is unchanged.
		require.Len(t, view.Annotations(0), 1)
		assert.Equal(t, "original note", view.Annotations(0)[0].Content)
		assert.Len(t, view.Annotations(0).Filter(line.Below), 1)
	})

	t.Run("preserves overlays", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "key: value\n", 1)
		view.AddLineOverlay(0, line.Overlay{Cols: position.NewSpan(0, 5), Kind: "test1"})

		clone := view.Clone()

		// Verify overlays were copied.
		require.Len(t, clone.Overlays(0), 1)
		assert.Equal(t, view.Overlays(0)[0].Cols, clone.Overlays(0)[0].Cols)
		assert.Equal(t, view.Overlays(0)[0].Kind, clone.Overlays(0)[0].Kind)

		// Modify clone overlays.
		clone.AddLineOverlay(0, line.Overlay{Cols: position.NewSpan(5, 10), Kind: "test2"})

		// Verify original is unchanged.
		require.Len(t, view.Overlays(0), 1)
	})

	t.Run("preserves flags", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, input, 3)
		view.SetFlag(1, line.FlagDeleted)

		clone := view.Clone()

		assert.Equal(t, line.FlagDeleted, clone.Flag(1))

		clone.SetFlag(1, line.FlagInserted)
		clone.SetFlag(2, line.FlagInserted)

		assert.Equal(t, line.FlagDeleted, view.Flag(1))
		assert.Equal(t, line.FlagDefault, view.Flag(2))
	})

	t.Run("decoration is independent in both directions", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, input, 3)
		clone := view.Clone()

		clone.AddOverlay(kind.GenericHighlight, position.NewRange(
			position.New(0, 0),
			position.New(0, 3),
		))
		clone.Annotate(1, line.Annotation{Content: "note", Placement: line.Below})
		clone.SetFlag(2, line.FlagInserted)

		assert.Empty(t, view.Overlays(0))
		assert.Empty(t, view.Annotations(1))
		assert.Equal(t, line.FlagDefault, view.Flag(2))

		view.AddOverlay(kind.GenericError, position.NewRange(
			position.New(1, 0),
			position.New(1, 3),
		))
		view.Annotate(0, line.Annotation{Content: "other", Placement: line.Above})
		view.SetFlag(0, line.FlagDeleted)

		assert.Empty(t, clone.Overlays(1))
		assert.Empty(t, clone.Annotations(0))
		assert.Equal(t, line.FlagDefault, clone.Flag(0))

		// Decorating one leaves the other intact.
		require.Len(t, clone.Overlays(0), 1)
	})

	t.Run("undecorated", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, input, 3)
		clone := view.Clone()

		for i := range clone.All() {
			assert.Equal(t, line.FlagDefault, clone.Flag(i))
			assert.Nil(t, clone.Overlays(i))
			assert.Nil(t, clone.Annotations(i))
		}
	})

	t.Run("empty", func(t *testing.T) {
		t.Parallel()

		clone := line.NewView(line.Lines{}).Clone()

		require.NotNil(t, clone)
		assert.Equal(t, 0, clone.Count())
	})
}

func TestView_Index(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		a: 1
		b: 2
		c: 3
		d: 4
	`)

	view := newTestView(t, input, 4)
	other := newTestView(t, input, 4)

	a, b := view.Lines().Line(0), view.Lines().Line(1)
	repeated := line.NewView(line.Collect(a, b, a, b, a))

	tcs := map[string]struct {
		view *line.View
		line *line.Line
		want int
		ok   bool
	}{
		"line of the view": {
			view: view,
			line: view.Lines().Line(2),
			want: 2,
			ok:   true,
		},
		"line of a view over held lines": {
			view: line.NewView(view.Slice(position.NewSpan(1, 3)).Held()),
			line: view.Lines().Line(2),
			want: 1,
			ok:   true,
		},
		"first copy of a repeated line": {
			view: repeated,
			line: a,
			want: 0,
			ok:   true,
		},
		"later copy of a repeated line a slice holds": {
			view: repeated.Slice(position.NewSpan(1, 5)),
			line: a,
			want: 2,
			ok:   true,
		},
		"last copy of a repeated line a slice holds": {
			view: repeated.Slice(position.NewSpan(3, 5)),
			line: a,
			want: 4,
			ok:   true,
		},
		"repeated line a slice holds no copy of": {
			view: repeated.Slice(position.NewSpan(1, 2), position.NewSpan(3, 4)),
			line: a,
		},
		"line a slice holds through overlapping spans": {
			view: view.Slice(position.NewSpan(2, 4), position.NewSpan(2, 3)),
			line: view.Lines().Line(2),
			want: 2,
			ok:   true,
		},
		"line of a slice that dropped it": {
			view: view.Slice(position.NewSpan(0, 2)),
			line: view.Lines().Line(2),
		},
		"line of other content with the same text": {
			view: view,
			line: other.Lines().Line(2),
		},
		"nil line": {
			view: view,
			line: nil,
		},
		"nil view": {
			view: nil,
			line: view.Lines().Line(0),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, ok := tc.view.Index(tc.line)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestView_Held(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		a: 1
		b: 2
		c: 3
		d: 4
	`)

	view := newTestView(t, input, 4)

	tcs := map[string]struct {
		view *line.View
		want []int // Indices in the content of the lines held.
	}{
		"every line of the content": {
			view: view,
			want: []int{0, 1, 2, 3},
		},
		"the lines a slice holds": {
			view: view.Slice(position.NewSpan(1, 3)),
			want: []int{1, 2},
		},
		"content order through overlapping spans": {
			view: view.Slice(position.NewSpan(2, 4), position.NewSpan(0, 1)),
			want: []int{0, 2, 3},
		},
		"a slice that holds nothing": {
			view: view.Slice(position.NewSpan(2, 2)),
			want: nil,
		},
		"nil view": {
			view: nil,
			want: nil,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.view.Held()
			require.Equal(t, len(tc.want), got.Len())
			assert.Equal(t, tc.view.Count(), got.Len())

			for i, ci := range tc.want {
				assert.Same(t, view.Lines().Line(ci), got.Line(i), "held line %d shares content line %d", i, ci)
				assert.Equal(t, ci+1, got.Line(i).Number(), "held line %d keeps the file's number", i)
			}
		})
	}
}

func TestView_Slice(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		a: 1
		b: 2
		c: 3
		d: 4
	`)

	// Decorated returns a view with a distinct flag, overlay, and annotation
	// on every line so a test can check a slice against its source.
	decorated := func(t *testing.T) *line.View {
		t.Helper()

		view := newTestView(t, input, 4)

		for i, ln := range view.All() {
			view.SetFlag(i, line.Flag(i))
			view.AddLineOverlay(i, line.Overlay{Cols: position.NewSpan(0, i+1), Kind: "test"})
			view.Annotate(i, line.Annotation{Content: ln.Content(), Placement: line.Below})
		}

		return view
	}

	contents := func(v *line.View) []string {
		var out []string

		for _, ln := range v.All() {
			out = append(out, ln.Content())
		}

		return out
	}

	t.Run("no spans yields every line", func(t *testing.T) {
		t.Parallel()

		view := decorated(t)
		got := view.Slice()

		require.NotSame(t, view, got)
		assert.Equal(t, []string{"a: 1", "b: 2", "c: 3", "d: 4"}, contents(got))
	})

	t.Run("single span", func(t *testing.T) {
		t.Parallel()

		view := decorated(t)
		got := view.Slice(position.NewSpan(1, 3))

		assert.Equal(t, []string{"b: 2", "c: 3"}, contents(got))
	})

	t.Run("spans in any order yield content order", func(t *testing.T) {
		t.Parallel()

		view := decorated(t)
		got := view.Slice(position.NewSpan(3, 4), position.NewSpan(0, 2))

		assert.Equal(t, []string{"a: 1", "b: 2", "d: 4"}, contents(got))
	})

	t.Run("overlapping spans hold each line once", func(t *testing.T) {
		t.Parallel()

		view := decorated(t)
		got := view.Slice(position.NewSpan(0, 2), position.NewSpan(1, 3))

		assert.Equal(t, []string{"a: 1", "b: 2", "c: 3"}, contents(got))
		assert.Equal(t, 3, got.Count())
	})

	t.Run("spans are clamped", func(t *testing.T) {
		t.Parallel()

		view := decorated(t)
		got := view.Slice(position.NewSpan(-5, 1), position.NewSpan(3, 99))

		assert.Equal(t, []string{"a: 1", "d: 4"}, contents(got))
	})

	t.Run("empty and out of range spans yield nothing", func(t *testing.T) {
		t.Parallel()

		view := decorated(t)

		for name, span := range map[string]position.Span{
			"empty":        position.NewSpan(2, 2),
			"inverted":     position.NewSpan(3, 1),
			"past the end": position.NewSpan(10, 20),
			"before start": position.NewSpan(-9, -1),
		} {
			got := view.Slice(span)

			assert.Equal(t, 0, got.Count(), name)
			assert.Equal(t, view.Lines(), got.Lines(), name)
			assert.Empty(t, got.String(), name)
		}
	})

	t.Run("shares lines", func(t *testing.T) {
		t.Parallel()

		view := decorated(t)
		got := view.Slice(position.NewSpan(2, 4))

		require.Equal(t, 2, got.Count())
		assert.Equal(t, view.Lines(), got.Lines())

		for i, ln := range got.All() {
			assert.Same(t, view.Lines().Line(i), ln)
		}
	})

	t.Run("carries decoration", func(t *testing.T) {
		t.Parallel()

		view := decorated(t)
		got := view.Slice(position.NewSpan(3, 4), position.NewSpan(1, 2))

		require.Equal(t, 2, got.Count())

		// The slice keeps the indices of the source.
		assert.Equal(t, line.Flag(3), got.Flag(3))
		require.Len(t, got.Overlays(3), 1)
		assert.Equal(t, position.NewSpan(0, 4), got.Overlays(3)[0].Cols)
		require.Len(t, got.Annotations(3), 1)
		assert.Equal(t, "d: 4", got.Annotations(3)[0].Content)

		assert.Equal(t, line.Flag(1), got.Flag(1))
		require.Len(t, got.Overlays(1), 1)
		assert.Equal(t, position.NewSpan(0, 2), got.Overlays(1)[0].Cols)
		require.Len(t, got.Annotations(1), 1)
		assert.Equal(t, "b: 2", got.Annotations(1)[0].Content)

		// The slice leaves behind the decoration of a line it does not hold.
		assert.Equal(t, line.FlagDefault, got.Flag(2))
		assert.Empty(t, got.Overlays(2))
		assert.Empty(t, got.Annotations(2))
	})

	t.Run("decoration is independent", func(t *testing.T) {
		t.Parallel()

		view := decorated(t)
		got := view.Slice(position.NewSpan(1, 2))

		got.SetFlag(1, line.FlagDefault)
		got.AddLineOverlay(1, line.Overlay{Cols: position.NewSpan(2, 3), Kind: "extra"})
		got.Annotate(1, line.Annotation{Content: "extra"})

		assert.Equal(t, line.Flag(1), view.Flag(1))
		assert.Len(t, view.Overlays(1), 1)
		assert.Len(t, view.Annotations(1), 1)
		assert.Len(t, got.Overlays(1), 2)
	})

	t.Run("keeps the coordinates of the content", func(t *testing.T) {
		t.Parallel()

		// Decorating before or after slicing renders the same, since a
		// slice takes ranges and indices in the coordinates of its content.
		rng := position.NewRange(position.New(2, 3), position.New(2, 4))
		ann := line.Annotation{Content: "here", Placement: line.Below, Col: 3}

		before := newTestView(t, input, 4)
		before.AddOverlay("test", rng)
		before.Annotate(2, ann)

		before = before.Slice(position.NewSpan(2, 4))

		after := newTestView(t, input, 4).Slice(position.NewSpan(2, 4))
		after.AddOverlay("test", rng)
		after.Annotate(2, ann)

		want := stringtest.JoinLF(
			"   3 | c: 3",
			"     |    ^ here",
			"   4 | d: 4",
		)
		assert.Equal(t, want, before.String())
		assert.Equal(t, want, after.String())
	})

	t.Run("a slice of a slice narrows it", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, input, 4)
		got := view.Slice(position.NewSpan(1, 4)).Slice(position.NewSpan(0, 3))

		assert.Equal(t, []string{"b: 2", "c: 3"}, contents(got))
		assert.True(t, got.Contains(1))
		assert.False(t, got.Contains(0))
		assert.False(t, got.Contains(3))
	})

	t.Run("decoration on a line the slice does not hold never renders", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, input, 4).Slice(position.NewSpan(0, 2))
		view.AddOverlay("test", position.NewRange(position.New(3, 0), position.New(3, 4)))
		view.Annotate(3, line.Annotation{Content: "hidden", Placement: line.Below})

		assert.Len(t, view.Overlays(3), 1)
		assert.Equal(t, "   1 | a: 1\n   2 | b: 2", view.String())
	})

	t.Run("undecorated source", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, input, 4)
		got := view.Slice(position.NewSpan(1, 3))

		require.Equal(t, 2, got.Count())

		for i := range got.All() {
			assert.Equal(t, line.FlagDefault, got.Flag(i))
			assert.Nil(t, got.Overlays(i))
			assert.Nil(t, got.Annotations(i))
		}
	})

	t.Run("String renders the sliced lines", func(t *testing.T) {
		t.Parallel()

		view := decorated(t)
		got := view.Slice(position.NewSpan(2, 3))

		assert.Equal(t, "   3 | c: 3\n     | ^^^ c: 3", got.String())
	})
}

func TestView_Slice_CostIndependentOfContent(t *testing.T) {
	t.Parallel()

	const (
		smallLen = 1000
		largeLen = 20000
		runs     = 1000
	)

	// SliceBytes returns the bytes one slice of the same 50-line window of
	// a decorated view over n lines allocates, averaged over many runs so
	// that allocations by tests running alongside spread thin.
	sliceBytes := func(t *testing.T, n int) int64 {
		t.Helper()

		view := line.NewView(line.NewLines(tokens.Tokenize(yamltest.GenerateYAML(n))))
		view.SetFlag(110, line.FlagInserted)
		view.AddLineOverlay(120, line.Overlay{Cols: position.NewSpan(0, 3), Kind: kind.GenericHighlight})
		view.Annotate(130, line.Annotation{Content: "note", Placement: line.Below})

		var (
			before, after runtime.MemStats
			got           *line.View
		)

		runtime.ReadMemStats(&before)

		for range runs {
			got = view.Slice(position.NewSpan(100, 150))
		}

		runtime.ReadMemStats(&after)

		require.Equal(t, 50, got.Count())
		assert.Equal(t, line.FlagInserted, got.Flag(110))
		assert.Len(t, got.Overlays(120), 1)
		assert.Len(t, got.Annotations(130), 1)

		return int64(after.TotalAlloc-before.TotalAlloc) / runs
	}

	small := sliceBytes(t, smallLen)
	large := sliceBytes(t, largeLen)

	// A slice that allocated even one byte per line of content would grow
	// by the number of lines the larger content adds.
	assert.Less(t, large-small, int64(largeLen-smallLen),
		"slice of %d lines allocates %d B, slice of %d lines allocates %d B",
		smallLen, small, largeLen, large)
}

func TestView_String(t *testing.T) {
	t.Parallel()

	t.Run("annotation rendering", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			want        string
			annotations []line.Annotation
		}{
			"no annotation": {
				annotations: nil,
				want:        "   1 | key: value",
			},
			"annotation below at start": {
				annotations: []line.Annotation{{Content: "error here", Placement: line.Below}},
				want: `   1 | key: value
     | ^ error here`,
			},
			"annotation below with padding": {
				annotations: []line.Annotation{{Content: "note", Placement: line.Below, Col: 4}},
				want: `   1 | key: value
     |     ^ note`,
			},
			"annotation above": {
				annotations: []line.Annotation{{Content: "@@ hunk header @@", Placement: line.Above}},
				want: `     | @@ hunk header @@
   1 | key: value`,
			},
			"annotations without content add no row": {
				annotations: []line.Annotation{
					{Content: "", Placement: line.Above},
					{Content: "", Placement: line.Below, Col: 2},
				},
				want: "   1 | key: value",
			},
			"annotations above and below": {
				annotations: []line.Annotation{
					{Content: "@@ hunk header @@", Placement: line.Above},
					{Content: "note", Placement: line.Below, Col: 2},
				},
				want: `     | @@ hunk header @@
   1 | key: value
     |   ^ note`,
			},
			"multiple annotations below join at the minimum column": {
				annotations: []line.Annotation{
					{Content: "first", Placement: line.Below, Col: 5},
					{Content: "second", Placement: line.Below, Col: 2},
				},
				want: `   1 | key: value
     |   ^ first; second`,
			},
			"an annotation without content does not set the column": {
				annotations: []line.Annotation{
					{Content: "", Placement: line.Below, Col: 0},
					{Content: "boom", Placement: line.Below, Col: 5},
				},
				want: `   1 | key: value
     |      ^ boom`,
			},
			"negative column is not padded": {
				annotations: []line.Annotation{{Content: "note", Placement: line.Below, Col: -3}},
				want: `   1 | key: value
     | ^ note`,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				view := newTestView(t, "key: value\n", 1)
				view.Annotate(0, tc.annotations...)

				assert.Equal(t, tc.want, view.String())
			})
		}
	})

	t.Run("multiple lines", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			key1: value1
			key2: value2
		`)
		view := newTestView(t, input, 2)

		assert.Equal(t, view.Lines().String(), view.String())
		assert.Equal(t, "   1 | key1: value1\n   2 | key2: value2", view.String())
	})

	t.Run("annotations on several lines", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "a: 1\nb: 2\nc: 3\n", 3)
		view.Annotate(0, line.Annotation{Content: "top", Placement: line.Above})
		view.Annotate(2, line.Annotation{Content: "bottom", Placement: line.Below, Col: 3})

		want := strings.Join([]string{
			"     | top",
			"   1 | a: 1",
			"   2 | b: 2",
			"   3 | c: 3",
			"     |    ^ bottom",
		}, "\n")

		assert.Equal(t, want, view.String())
	})

	t.Run("overlays mark their columns and flags do not render", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "key: value\n", 1)
		view.SetFlag(0, line.FlagDeleted)
		view.AddOverlay("test1", position.NewRange(position.New(0, 0), position.New(0, 3)))
		view.BlendOverlay("test2", position.NewRange(position.New(0, 5), position.New(0, 7)))

		assert.Equal(t, "   1 | key: value\n     | ^^^  ^^", view.String())
	})

	t.Run("overlay and annotation share the marker row", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "key: value\n", 1)
		view.AddOverlay("test1", position.NewRange(position.New(0, 5), position.New(0, 10)))
		view.Annotate(0, line.Annotation{Content: "bad value", Placement: line.Below, Col: 5})

		assert.Equal(t, "   1 | key: value\n     |      ^^^^^ bad value", view.String())
	})

	t.Run("overlay past the width stops at the content", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "key: value\n", 1)
		view.AddLineOverlay(0, line.Overlay{Kind: "test1", Cols: position.NewSpan(8, 20)})

		assert.Equal(t, "   1 | key: value\n     |         ^^", view.String())
	})

	t.Run("carets sit under wide runes", func(t *testing.T) {
		t.Parallel()

		// Each CJK rune renders two cells wide, so the columns before the
		// marked ones are worth two carets each.
		view := newTestView(t, "名前: value\n", 1)
		view.AddOverlay("test1", position.NewRange(position.New(0, 4), position.New(0, 9)))
		view.Annotate(0, line.Annotation{Content: "bad", Placement: line.Below, Col: 4})

		assert.Equal(t, "   1 | 名前: value\n     |       ^^^^^ bad", view.String())
	})

	t.Run("carets on a combining mark sit under its base", func(t *testing.T) {
		t.Parallel()

		// The acute accent renders on the e before it, so a mark on the
		// accent's column lands under the e, and an annotation at that
		// column starts there too.
		view := newTestView(t, "k: e\u0301x\n", 1)
		view.AddOverlay("test1", position.NewRange(position.New(0, 4), position.New(0, 5)))
		view.Annotate(0, line.Annotation{Content: "here", Placement: line.Below, Col: 4})

		assert.Equal(t, "   1 | k: e\u0301x\n     |    ^ here", view.String())
	})

	t.Run("carets sit under grapheme clusters", func(t *testing.T) {
		t.Parallel()

		// The content row renders a ZWJ sequence or a keycap as one
		// cluster of two cells, so the columns before x are worth that
		// much and no more, and the caret lands under x as the row above
		// starts there.
		tcs := map[string]struct {
			content string
			col     int
			want    string
		}{
			"zwj sequence": {
				content: "a: \"\U0001F468\u200d\U0001F469\" x",
				col:     9,
				want: stringtest.JoinLF(
					"     |         above",
					"   1 | a: \"\U0001F468\u200d\U0001F469\" x",
					"     |         ^ below",
				),
			},
			"keycap": {
				content: "a: 1\ufe0f\u20e3 x",
				col:     7,
				want: stringtest.JoinLF(
					"     |       above",
					"   1 | a: 1\ufe0f\u20e3 x",
					"     |       ^ below",
				),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				view := newTestView(t, tc.content+"\n", 1)
				view.AddOverlay("test1", position.NewRange(position.New(0, tc.col), position.New(0, tc.col+1)))
				view.Annotate(0,
					line.Annotation{Content: "above", Placement: line.Above, Col: tc.col},
					line.Annotation{Content: "below", Placement: line.Below, Col: tc.col},
				)

				assert.Equal(t, tc.want, view.String())
			})
		}
	})

	t.Run("a mark inside a grapheme cluster sits under its first rune", func(t *testing.T) {
		t.Parallel()

		// The joiner and the second emoji render as part of the cluster
		// the first emoji starts, so a mark on either lands under it.
		view := newTestView(t, "a: \U0001F468\u200d\U0001F469 x\n", 1)
		view.AddOverlay("test1", position.NewRange(position.New(0, 5), position.New(0, 6)))

		assert.Equal(t, stringtest.JoinLF(
			"   1 | a: \U0001F468\u200d\U0001F469 x",
			"     |    ^^",
		), view.String())
	})

	t.Run("control characters render as pictures", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "a: \"x\ty\x1b\"\n", 1)
		view.AddOverlay("test1", position.NewRange(position.New(0, 3), position.New(0, 9)))
		view.Annotate(0, line.Annotation{Content: "tab\x1bhere", Placement: line.Above})

		assert.Equal(t, stringtest.JoinLF(
			"     | tab\u241bhere",
			"   1 | a: \"x\u2409y\u241b\"",
			"     |    ^^^^^^",
		), view.String())
	})

	t.Run("empty", func(t *testing.T) {
		t.Parallel()

		assert.Empty(t, line.NewView(line.Lines{}).String())
	})
}

func TestView_OutOfRange(t *testing.T) {
	t.Parallel()

	tcs := map[string]func(v *line.View, i int){
		"Flag":           func(v *line.View, i int) { v.Flag(i) },
		"SetFlag":        func(v *line.View, i int) { v.SetFlag(i, line.FlagInserted) },
		"Annotations":    func(v *line.View, i int) { v.Annotations(i) },
		"Annotate":       func(v *line.View, i int) { v.Annotate(i, line.Annotation{Content: "x"}) },
		"Overlays":       func(v *line.View, i int) { v.Overlays(i) },
		"AddLineOverlay": func(v *line.View, i int) { v.AddLineOverlay(i, line.Overlay{}) },
	}

	for name, call := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			for _, i := range []int{-1, 2, 99} {
				// Undecorated, so the check happens before any slice exists.
				view := newTestView(t, "a: 1\nb: 2\n", 2)

				assert.Panics(t, func() { call(view, i) }, "index %d on undecorated view", i)

				// Decorated, so every slice exists and is bounds-checked.
				view.SetFlag(0, line.FlagInserted)
				view.Annotate(0, line.Annotation{Content: "x"})
				view.AddLineOverlay(0, line.Overlay{})

				assert.Panics(t, func() { call(view, i) }, "index %d on decorated view", i)
			}

			assert.Panics(t, func() { call(line.NewView(line.Lines{}), 0) }, "index 0 on empty view")
		})
	}
}

func TestView_String_AboveAnnotationOnWideContent(t *testing.T) {
	t.Parallel()

	// The row above starts at the column of the annotation as the content
	// row renders it, so on wide characters it takes their cells, as the
	// marker row below does.
	view := newTestView(t, "名前: v\n", 1)
	view.Annotate(0,
		line.Annotation{Content: "above", Placement: line.Above, Col: 2},
		line.Annotation{Content: "below", Placement: line.Below, Col: 2},
	)

	assert.Equal(t, stringtest.JoinLF(
		"     |     above",
		"   1 | 名前: v",
		"     |     ^ below",
	), view.String())
}

func TestView_String_PlaceholderLine(t *testing.T) {
	t.Parallel()

	// A zero Line, which a diff puts opposite an inserted or deleted line,
	// has no number, and its gutter is blank rather than 0.
	view := line.NewView(line.Collect(&line.Line{}))

	assert.Equal(t, "     | ", view.String())
}

func TestView_String_FarAnnotationColumn(t *testing.T) {
	t.Parallel()

	// A column more than 1024 columns past the end of the content starts
	// 1024 columns past it, after every cell of the content, as the
	// printer starts it.
	tcs := map[string]struct {
		input     string
		want      string
		col       int
		placement line.Placement
	}{
		"below at the max column": {
			input:     "a: 1",
			col:       math.MaxInt,
			placement: line.Below,
			want:      stringtest.JoinLF("   1 | a: 1", "     | "+strings.Repeat(" ", 4+1024)+"^ msg"),
		},
		"above at the max column": {
			input:     "a: 1",
			col:       math.MaxInt,
			placement: line.Above,
			want:      stringtest.JoinLF("     | "+strings.Repeat(" ", 4+1024)+"msg", "   1 | a: 1"),
		},
		"below past the bound": {
			input:     "a: 1",
			col:       1 << 32,
			placement: line.Below,
			want:      stringtest.JoinLF("   1 | a: 1", "     | "+strings.Repeat(" ", 4+1024)+"^ msg"),
		},
		"above past the bound": {
			input:     "a: 1",
			col:       1 << 32,
			placement: line.Above,
			want:      stringtest.JoinLF("     | "+strings.Repeat(" ", 4+1024)+"msg", "   1 | a: 1"),
		},
		"below at the bound": {
			input:     "a: 1",
			col:       4 + 1024,
			placement: line.Below,
			want:      stringtest.JoinLF("   1 | a: 1", "     | "+strings.Repeat(" ", 4+1024)+"^ msg"),
		},
		"below at the max column after wide runes": {
			input:     "k: 日本語",
			col:       math.MaxInt,
			placement: line.Below,
			want:      stringtest.JoinLF("   1 | k: 日本語", "     | "+strings.Repeat(" ", 9+1024)+"^ msg"),
		},
		"above at the max column after wide runes": {
			input:     "k: 日本語",
			col:       math.MaxInt,
			placement: line.Above,
			want:      stringtest.JoinLF("     | "+strings.Repeat(" ", 9+1024)+"msg", "   1 | k: 日本語"),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := newTestView(t, tc.input+"\n", 1)
			view.Annotate(0, line.Annotation{Content: "msg", Placement: tc.placement, Col: tc.col})

			var got string

			require.NotPanics(t, func() { got = view.String() })
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestOverlays_MarkerRow(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		content  string
		want     string
		overlays line.Overlays
	}{
		"no overlays": {
			content: "key: v",
			want:    "",
		},
		"negative start clamps to the first column": {
			content:  "key: v",
			overlays: line.Overlays{{Cols: position.NewSpan(-2, 3)}},
			want:     "^^^",
		},
		"span past the end stops at the content": {
			content:  "key: v",
			overlays: line.Overlays{{Cols: position.NewSpan(3, 10)}},
			want:     "   ^^^",
		},
		"span past the content marks nothing": {
			content:  "key: v",
			overlays: line.Overlays{{Cols: position.NewSpan(8, 12)}},
			want:     "",
		},
		"empty span marks nothing": {
			content:  "key: v",
			overlays: line.Overlays{{Cols: position.NewSpan(2, 2)}},
			want:     "",
		},
		"overlapping overlays": {
			content: "key: v",
			overlays: line.Overlays{
				{Cols: position.NewSpan(1, 3)},
				{Cols: position.NewSpan(2, 5)},
			},
			want: " ^^^^",
		},
		"wide rune gets two carets": {
			content:  "名前: v",
			overlays: line.Overlays{{Cols: position.NewSpan(1, 2)}},
			want:     "  ^^",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.overlays.MarkerRow(tc.content))
		})
	}
}

func TestView_Hunks(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		a: 1
		b: 2
		c: 3
		d: 4
		e: 5
		f: 6
		g: 7
		h: 8
	`)

	contents := func(v *line.View) []string {
		var out []string

		for _, ln := range v.All() {
			out = append(out, ln.Content())
		}

		return out
	}

	separators := func(v *line.View) []int {
		var out []int

		for i := range v.All() {
			for _, ann := range v.Annotations(i) {
				if ann.Kind == kind.UISeparator {
					out = append(out, i)
				}
			}
		}

		return out
	}

	t.Run("keeps context around every kind of decoration", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, input, 8)
		view.SetFlag(0, line.FlagInserted)
		view.AddOverlay("test", position.NewRange(position.New(7, 0), position.New(7, 1)))
		view.Annotate(4, line.Annotation{Content: "here", Placement: line.Below})

		got := view.Hunks(1)

		assert.Equal(t, []string{"a: 1", "b: 2", "d: 4", "e: 5", "f: 6", "g: 7", "h: 8"}, contents(got))
		assert.Equal(t, []int{3}, separators(got), "one separator, above the second hunk")
		assert.Equal(t, line.FlagInserted, got.Flag(0))
		assert.Len(t, got.Overlays(7), 1)
		assert.Len(t, got.Annotations(4), 1)
	})

	t.Run("touching windows share a hunk", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, input, 8)
		view.Annotate(1, line.Annotation{Content: "one", Placement: line.Below})
		view.Annotate(5, line.Annotation{Content: "two", Placement: line.Below})

		got := view.Hunks(2)

		assert.Equal(t, []string{"a: 1", "b: 2", "c: 3", "d: 4", "e: 5", "f: 6", "g: 7", "h: 8"}, contents(got))
		assert.Empty(t, separators(got))
	})

	t.Run("a negative context shows the decorated lines alone", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, input, 8)
		view.Annotate(1, line.Annotation{Content: "one", Placement: line.Below})
		view.Annotate(6, line.Annotation{Content: "two", Placement: line.Below})

		got := view.Hunks(-1)

		assert.Equal(t, []string{"b: 2", "g: 7"}, contents(got))
		assert.Equal(t, []int{6}, separators(got))
	})

	t.Run("no decoration yields no line", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, input, 8)

		got := view.Hunks(2)

		assert.Equal(t, 0, got.Count())
		assert.Equal(t, 8, got.Lines().Len(), "the content is still the whole")
	})

	t.Run("a slice yields hunks within the slice", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, input, 8).Slice(position.NewSpan(2, 8))
		view.Annotate(2, line.Annotation{Content: "edge", Placement: line.Below})
		view.Annotate(7, line.Annotation{Content: "end", Placement: line.Below})

		got := view.Hunks(1)

		assert.Equal(t, []string{"c: 3", "d: 4", "g: 7", "h: 8"}, contents(got))
		assert.Equal(t, []int{6}, separators(got))
	})

	t.Run("the separator goes above the first line the hunk holds", func(t *testing.T) {
		t.Parallel()

		// The view skips line d, so the second hunk's window starts on a
		// line the view does not hold.
		view := newTestView(t, input, 8).Slice(position.NewSpan(0, 3), position.NewSpan(4, 8))
		view.Annotate(0, line.Annotation{Content: "one", Placement: line.Below})
		view.Annotate(4, line.Annotation{Content: "two", Placement: line.Below})

		got := view.Hunks(1)

		assert.Equal(t, []string{"a: 1", "b: 2", "e: 5", "f: 6"}, contents(got))
		assert.Equal(t, []int{4}, separators(got))
	})

	t.Run("renders as an excerpt", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, input, 8)
		view.Annotate(1, line.Annotation{Content: "one", Placement: line.Below})
		view.Annotate(6, line.Annotation{Content: "two", Placement: line.Below})

		want := stringtest.JoinLF(
			"   1 | a: 1",
			"   2 | b: 2",
			"     | ^ one",
			"   3 | c: 3",
			"     | ...",
			"   6 | f: 6",
			"   7 | g: 7",
			"     | ^ two",
			"   8 | h: 8",
		)
		assert.Equal(t, want, view.Hunks(1).String())
	})

	t.Run("separator sits above an annotated first line", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, input, 8)
		view.Annotate(1, line.Annotation{Content: "one", Placement: line.Below})
		view.Annotate(6, line.Annotation{Content: "two", Placement: line.Above})

		want := stringtest.JoinLF(
			"   2 | b: 2",
			"     | ^ one",
			"     | ...; two",
			"   7 | g: 7",
		)
		assert.Equal(t, want, view.Hunks(0).String())
	})
}
