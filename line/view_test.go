package line_test

import (
	"strings"
	"testing"

	"github.com/goccy/go-yaml/lexer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/style"
)

// newTestView creates a view over the lines of input.
func newTestView(t *testing.T, input string, wantLen int) *line.View {
	t.Helper()

	lines := line.NewLines(lexer.Tokenize(input))
	require.Len(t, lines, wantLen)

	return line.NewView(lines)
}

func TestNewView(t *testing.T) {
	t.Parallel()

	t.Run("over lines", func(t *testing.T) {
		t.Parallel()

		lines := line.NewLines(lexer.Tokenize("key: value\nother: data\n"))
		view := line.NewView(lines)

		assert.Equal(t, 2, view.Len())
		assert.Equal(t, lines, view.Lines())
		assert.Same(t, lines[0], view.Line(0))
		assert.Same(t, lines[1], view.Line(1))
	})

	t.Run("over nil lines", func(t *testing.T) {
		t.Parallel()

		view := line.NewView(nil)

		assert.Equal(t, 0, view.Len())
		assert.Nil(t, view.Lines())
		assert.Empty(t, view.String())

		for range view.AllLines() {
			t.Fatal("expected no lines")
		}
	})

	t.Run("over empty lines", func(t *testing.T) {
		t.Parallel()

		view := line.NewView(line.Lines{})

		assert.Equal(t, 0, view.Len())
		assert.Empty(t, view.Lines())
		assert.Empty(t, view.String())
	})

	t.Run("zero value", func(t *testing.T) {
		t.Parallel()

		var view line.View

		assert.Equal(t, 0, view.Len())
		assert.Nil(t, view.Lines())
		assert.Empty(t, view.String())

		// Range-taking methods are safe on an empty view.
		view.AddOverlay("test1", position.NewRange(position.New(0, 0), position.New(0, 5)))

		assert.Equal(t, 0, view.Slice(position.NewSpan(0, 5)).Len())
	})

	t.Run("nil view", func(t *testing.T) {
		t.Parallel()

		var view *line.View

		assert.Equal(t, 0, view.Len())
		assert.Nil(t, view.Lines())
		assert.Nil(t, view.Clone())

		for range view.AllLines() {
			t.Fatal("expected no lines")
		}
	})
}

func TestView_AllLines(t *testing.T) {
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

		for i, ln := range view.AllLines() {
			indices = append(indices, i)
			contents = append(contents, ln.Content())

			assert.Same(t, view.Line(i), ln)
		}

		assert.Equal(t, []int{0, 1, 2}, indices)
		assert.Equal(t, []string{"key: value", "list:", "  - one"}, contents)
	})

	t.Run("clamps spans", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, input, 3)

		var indices []int

		for i := range view.AllLines(position.NewSpan(1, 99)) {
			indices = append(indices, i)
		}

		assert.Equal(t, []int{1, 2}, indices)
	})

	t.Run("index reaches decoration", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, input, 3)
		view.SetFlag(1, line.FlagInserted)

		var flags []line.Flag

		for i := range view.AllLines() {
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
			Cols:  position.NewSpan(0, 5),
			Style: "test1",
		})

		require.Len(t, view.Overlays(0), 1)
		assert.Equal(t, position.NewSpan(0, 5), view.Overlays(0)[0].Cols)
		assert.Equal(t, style.Style("test1"), view.Overlays(0)[0].Style)
	})

	t.Run("add multiple overlays", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "key: value\n", 1)
		view.AddLineOverlay(0,
			line.Overlay{Cols: position.NewSpan(0, 3), Style: "test1"},
			line.Overlay{Cols: position.NewSpan(5, 10), Style: "test2"},
		)

		require.Len(t, view.Overlays(0), 2)
		assert.Equal(t, position.NewSpan(0, 3), view.Overlays(0)[0].Cols)
		assert.Equal(t, position.NewSpan(5, 10), view.Overlays(0)[1].Cols)
	})

	t.Run("accumulates overlays", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "key: value\n", 1)
		view.AddLineOverlay(0, line.Overlay{Cols: position.NewSpan(0, 3), Style: "test1"})
		view.AddLineOverlay(0, line.Overlay{Cols: position.NewSpan(5, 10), Style: "test2"})

		require.Len(t, view.Overlays(0), 2)
	})

	t.Run("does not clamp", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "key: value\n", 1)

		// The clamped AddOverlay would cut this to the line width; the raw
		// method keeps the columns as given.
		view.AddLineOverlay(0, line.Overlay{Cols: position.NewSpan(-2, 99), Style: "test1"})

		require.Len(t, view.Overlays(0), 1)
		assert.Equal(t, position.NewSpan(-2, 99), view.Overlays(0)[0].Cols)
	})

	t.Run("overlays one line only", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "a: 1\nb: 2\n", 2)
		view.AddLineOverlay(1, line.Overlay{Cols: position.NewSpan(0, 1), Style: "test1"})

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
		assert.Equal(t, style.Style("test1"), view.Overlays(0)[0].Style)
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
		assert.Equal(t, style.Style("test2"), view.Overlays(0)[0].Style)

		// Middle line: full line.
		require.Len(t, view.Overlays(1), 1)
		assert.Equal(t, position.NewSpan(0, len("key2: value2")), view.Overlays(1)[0].Cols)
		assert.Equal(t, style.Style("test2"), view.Overlays(1)[0].Style)

		// Last line: start to col 5.
		require.Len(t, view.Overlays(2), 1)
		assert.Equal(t, position.NewSpan(0, 5), view.Overlays(2)[0].Cols)
		assert.Equal(t, style.Style("test2"), view.Overlays(2)[0].Style)
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

		for i := range view.Len() {
			assert.Empty(t, view.Overlays(i), "line %d", i)
		}
	})

	t.Run("empty view no-op", func(t *testing.T) {
		t.Parallel()

		view := line.NewView(nil)

		// Should not panic on an empty view.
		view.AddOverlay("test1", position.NewRange(position.New(0, 0), position.New(0, 5)))

		assert.Equal(t, 0, view.Len())
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
		assert.Equal(t, style.Style("test1"), view.Overlays(0)[0].Style)
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
		require.Equal(t, 3, clone.Len())
		assert.Equal(t, view.Lines().Content(), clone.Lines().Content())

		for i := range view.Len() {
			assert.Same(t, view.Line(i), clone.Line(i), "line %d", i)
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
		view.AddLineOverlay(0, line.Overlay{Cols: position.NewSpan(0, 5), Style: "test1"})

		clone := view.Clone()

		// Verify overlays were copied.
		require.Len(t, clone.Overlays(0), 1)
		assert.Equal(t, view.Overlays(0)[0].Cols, clone.Overlays(0)[0].Cols)
		assert.Equal(t, view.Overlays(0)[0].Style, clone.Overlays(0)[0].Style)

		// Modify clone overlays.
		clone.AddLineOverlay(0, line.Overlay{Cols: position.NewSpan(5, 10), Style: "test2"})

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

		clone.AddOverlay(style.GenericHighlight, position.NewRange(
			position.New(0, 0),
			position.New(0, 3),
		))
		clone.Annotate(1, line.Annotation{Content: "note", Placement: line.Below})
		clone.SetFlag(2, line.FlagInserted)

		assert.Empty(t, view.Overlays(0))
		assert.Empty(t, view.Annotations(1))
		assert.Equal(t, line.FlagDefault, view.Flag(2))

		view.AddOverlay(style.GenericError, position.NewRange(
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

		for i := range clone.Len() {
			assert.Equal(t, line.FlagDefault, clone.Flag(i))
			assert.Nil(t, clone.Overlays(i))
			assert.Nil(t, clone.Annotations(i))
		}
	})

	t.Run("empty", func(t *testing.T) {
		t.Parallel()

		clone := line.NewView(nil).Clone()

		require.NotNil(t, clone)
		assert.Equal(t, 0, clone.Len())
	})
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
	// on every line so a slice can be checked against its source.
	decorated := func(t *testing.T) *line.View {
		t.Helper()

		view := newTestView(t, input, 4)

		for i := range view.Len() {
			view.SetFlag(i, line.Flag(i))
			view.AddLineOverlay(i, line.Overlay{Cols: position.NewSpan(0, i+1), Style: "test"})
			view.Annotate(i, line.Annotation{Content: view.Line(i).Content(), Placement: line.Below})
		}

		return view
	}

	contents := func(v *line.View) []string {
		var out []string

		for _, ln := range v.AllLines() {
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

	t.Run("spans in supplied order", func(t *testing.T) {
		t.Parallel()

		view := decorated(t)
		got := view.Slice(position.NewSpan(3, 4), position.NewSpan(0, 2))

		assert.Equal(t, []string{"d: 4", "a: 1", "b: 2"}, contents(got))
	})

	t.Run("overlapping spans repeat lines", func(t *testing.T) {
		t.Parallel()

		view := decorated(t)
		got := view.Slice(position.NewSpan(0, 2), position.NewSpan(1, 3))

		assert.Equal(t, []string{"a: 1", "b: 2", "b: 2", "c: 3"}, contents(got))
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

			assert.Equal(t, 0, got.Len(), name)
			assert.Empty(t, got.Lines(), name)
			assert.Empty(t, got.String(), name)
		}
	})

	t.Run("shares lines", func(t *testing.T) {
		t.Parallel()

		view := decorated(t)
		got := view.Slice(position.NewSpan(2, 4))

		require.Equal(t, 2, got.Len())
		assert.Same(t, view.Line(2), got.Line(0))
		assert.Same(t, view.Line(3), got.Line(1))
	})

	t.Run("carries decoration", func(t *testing.T) {
		t.Parallel()

		view := decorated(t)
		got := view.Slice(position.NewSpan(3, 4), position.NewSpan(1, 2))

		require.Equal(t, 2, got.Len())

		// Line 3 of the source is line 0 of the slice.
		assert.Equal(t, line.Flag(3), got.Flag(0))
		require.Len(t, got.Overlays(0), 1)
		assert.Equal(t, position.NewSpan(0, 4), got.Overlays(0)[0].Cols)
		require.Len(t, got.Annotations(0), 1)
		assert.Equal(t, "d: 4", got.Annotations(0)[0].Content)

		// Line 1 of the source is line 1 of the slice.
		assert.Equal(t, line.Flag(1), got.Flag(1))
		require.Len(t, got.Overlays(1), 1)
		assert.Equal(t, position.NewSpan(0, 2), got.Overlays(1)[0].Cols)
		require.Len(t, got.Annotations(1), 1)
		assert.Equal(t, "b: 2", got.Annotations(1)[0].Content)
	})

	t.Run("decoration is independent", func(t *testing.T) {
		t.Parallel()

		view := decorated(t)
		got := view.Slice(position.NewSpan(1, 2))

		got.SetFlag(0, line.FlagDefault)
		got.AddLineOverlay(0, line.Overlay{Cols: position.NewSpan(2, 3), Style: "extra"})
		got.Annotate(0, line.Annotation{Content: "extra"})

		assert.Equal(t, line.Flag(1), view.Flag(1))
		assert.Len(t, view.Overlays(1), 1)
		assert.Len(t, view.Annotations(1), 1)
		assert.Len(t, got.Overlays(0), 2)
	})

	t.Run("undecorated source", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, input, 4)
		got := view.Slice(position.NewSpan(1, 3))

		require.Equal(t, 2, got.Len())

		for i := range got.Len() {
			assert.Equal(t, line.FlagDefault, got.Flag(i))
			assert.Nil(t, got.Overlays(i))
			assert.Nil(t, got.Annotations(i))
		}
	})

	t.Run("String renders the sliced lines", func(t *testing.T) {
		t.Parallel()

		view := decorated(t)
		got := view.Slice(position.NewSpan(2, 3))

		assert.Equal(t, "   3 | c: 3\n   3 | ^ c: 3", got.String())
	})
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
   1 | ^ error here`,
			},
			"annotation below with padding": {
				annotations: []line.Annotation{{Content: "note", Placement: line.Below, Col: 4}},
				want: `   1 | key: value
   1 |     ^ note`,
			},
			"annotation above": {
				annotations: []line.Annotation{{Content: "@@ hunk header @@", Placement: line.Above}},
				want: `   1 | @@ hunk header @@
   1 | key: value`,
			},
			"annotations above and below": {
				annotations: []line.Annotation{
					{Content: "@@ hunk header @@", Placement: line.Above},
					{Content: "note", Placement: line.Below, Col: 2},
				},
				want: `   1 | @@ hunk header @@
   1 | key: value
   1 |   ^ note`,
			},
			"multiple annotations below join at the minimum column": {
				annotations: []line.Annotation{
					{Content: "first", Placement: line.Below, Col: 5},
					{Content: "second", Placement: line.Below, Col: 2},
				},
				want: `   1 | key: value
   1 |   ^ first; second`,
			},
			"negative column is not padded": {
				annotations: []line.Annotation{{Content: "note", Placement: line.Below, Col: -3}},
				want: `   1 | key: value
   1 | ^ note`,
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
			"   1 | top",
			"   1 | a: 1",
			"   2 | b: 2",
			"   3 | c: 3",
			"   3 |    ^ bottom",
		}, "\n")

		assert.Equal(t, want, view.String())
	})

	t.Run("flags and overlays do not render", func(t *testing.T) {
		t.Parallel()

		view := newTestView(t, "key: value\n", 1)
		view.SetFlag(0, line.FlagDeleted)
		view.AddOverlay("test1", position.NewRange(position.New(0, 0), position.New(0, 3)))

		assert.Equal(t, "   1 | key: value", view.String())
	})

	t.Run("empty", func(t *testing.T) {
		t.Parallel()

		assert.Empty(t, line.NewView(nil).String())
	})
}

func TestView_OutOfRange(t *testing.T) {
	t.Parallel()

	tcs := map[string]func(v *line.View, i int){
		"Line":           func(v *line.View, i int) { v.Line(i) },
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

			assert.Panics(t, func() { call(line.NewView(nil), 0) }, "index 0 on empty view")
		})
	}
}
