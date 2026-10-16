package line_test

import (
	"fmt"
	"iter"
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
)

// newView creates a view over the lines of input.
func newView(input string) *line.View {
	return line.NewView(newLines(input))
}

// indicesOf returns the index of each line seq yields.
func indicesOf(seq iter.Seq2[int, *line.Line]) []int {
	var got []int

	for i := range seq {
		got = append(got, i)
	}

	return got
}

// contentsOf returns the content of each line seq yields.
func contentsOf(seq iter.Seq2[int, *line.Line]) []string {
	var got []string

	for _, ln := range seq {
		got = append(got, ln.Content())
	}

	return got
}

// letterRows returns n rows of YAML that map the letters of the alphabet
// to their places in it, from "a: 1" on.
func letterRows(n int) []string {
	rows := make([]string, n)
	for i := range rows {
		rows[i] = fmt.Sprintf("%c: %d", 'a'+i, i+1)
	}

	return rows
}

func TestNewView(t *testing.T) {
	t.Parallel()

	t.Run("over lines", func(t *testing.T) {
		t.Parallel()

		lines := newLines(stringtest.LinesLF("key: value", "other: data"))
		view := line.NewView(lines)

		assert.Equal(t, 2, view.Count())
		assert.Same(t, lines.Line(0), view.Line(0))
		assert.Same(t, lines.Line(1), view.Line(1))
		assert.Panics(t, func() { view.Line(2) }, "the content ends where lines ends")
	})

	t.Run("zero value", func(t *testing.T) {
		t.Parallel()

		var view line.View

		assert.Equal(t, 0, view.Count())
		assert.Panics(t, func() { view.Line(0) })
		assert.Empty(t, view.String())

		// Range-taking methods are safe on an empty view.
		view.AddOverlay("test1", yamltest.LineRange(0, 0, 5))

		assert.Equal(t, 0, view.Slice(position.NewSpan(0, 5)).Count())
	})
}

func TestView_Nil(t *testing.T) {
	t.Parallel()

	var view *line.View

	assert.Equal(t, 0, view.Count())
	assert.Panics(t, func() { view.Line(0) })
	assert.Nil(t, view.Clone())
	assert.Equal(t, 0, view.Held().Len())

	for range view.All() {
		t.Fatal("expected no lines")
	}

	// Hunks and Clip return an empty view rather than nil.
	tcs := map[string]*line.View{
		"Hunks": view.Hunks(2),
		"Clip":  view.Clip(10),
	}

	for name, got := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			require.NotNil(t, got)
			assert.Equal(t, 0, got.Count())
			assert.Empty(t, got.String())
		})
	}
}

func TestView_Contains(t *testing.T) {
	t.Parallel()

	view := newView(stringtest.LinesLF(letterRows(3)...))
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

	// This view holds lines 0, 2, and 5 alone, so All yields those lines
	// with the indices they have in the content.
	view := newView(stringtest.JoinLF(letterRows(6)...)).Slice(
		position.NewSpan(0, 1),
		position.NewSpan(2, 3),
		position.NewSpan(5, 6),
	)

	assert.Equal(t, []int{0, 2, 5}, indicesOf(view.All()))
}

func TestView_Within(t *testing.T) {
	t.Parallel()

	// TestSequence checks how Within selects lines from a view of every
	// line. This view holds lines 0, 2, and 5 alone, so Within yields those
	// of them that the spans select.
	view := newView(stringtest.JoinLF(letterRows(6)...)).Slice(
		position.NewSpan(0, 1),
		position.NewSpan(2, 3),
		position.NewSpan(5, 6),
	)

	tcs := map[string]struct {
		spans []position.Span
		want  []int
	}{
		"no spans yields no line": {},
		"span over the content yields the lines the view holds": {
			spans: []position.Span{position.NewSpan(0, 6)},
			want:  []int{0, 2, 5},
		},
		"span over lines the view skips": {
			spans: []position.Span{position.NewSpan(1, 5)},
			want:  []int{2},
		},
		"overlapping spans": {
			spans: []position.Span{position.NewSpan(4, 6), position.NewSpan(2, 5), position.NewSpan(0, 3)},
			want:  []int{0, 2, 5},
		},
		"spans over lines the view does not hold": {
			spans: []position.Span{position.NewSpan(3, 5), position.NewSpan(1, 2)},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, indicesOf(view.Within(tc.spans...)))
		})
	}
}

func TestView_Flag(t *testing.T) {
	t.Parallel()

	view := newView(stringtest.LinesLF(letterRows(3)...))
	assert.Equal(t, []line.Flag{line.FlagDefault, line.FlagDefault, line.FlagDefault}, flagsOf(view))

	view.SetFlag(1, line.FlagInserted)
	view.SetFlag(1, line.FlagDeleted)
	assert.Equal(
		t,
		[]line.Flag{line.FlagDefault, line.FlagDeleted, line.FlagDefault},
		flagsOf(view),
		"the last flag set on a line replaces the one before, on that line alone",
	)

	// FlagDefault clears the flag of a line, so no line counts as decorated.
	view.SetFlag(1, line.FlagDefault)
	view.SetFlag(2, line.FlagDefault)
	assert.Equal(t, []line.Flag{line.FlagDefault, line.FlagDefault, line.FlagDefault}, flagsOf(view))
	assert.Equal(t, line.FlagDefault, view.Clone().Flag(1))
	assert.Equal(t, 0, view.Hunks(0).Count())
}

// flagsOf returns the flag of every line of the content of view.
func flagsOf(view *line.View) []line.Flag {
	flags := make([]line.Flag, view.Count())
	for i := range flags {
		flags[i] = view.Flag(i)
	}

	return flags
}

func TestView_Annotate(t *testing.T) {
	t.Parallel()

	view := newView(stringtest.LinesLF(letterRows(2)...))
	assert.Nil(t, view.Annotations(1))

	first := line.Annotation{Content: "first", Placement: line.Above}
	second := line.Annotation{Content: "second", Placement: line.Below}
	third := line.Annotation{Content: "third"}

	// Each call adds to the annotations of its line.
	view.Annotate(1, first)
	view.Annotate(1, second, third)

	assert.Equal(t, line.Annotations{first, second, third}, view.Annotations(1))
	assert.Nil(t, view.Annotations(0))
}

func TestView_AddLineOverlay(t *testing.T) {
	t.Parallel()

	view := newView(stringtest.LinesLF(letterRows(2)...))
	assert.Nil(t, view.Overlays(1))

	first := line.Overlay{Cols: position.NewSpan(0, 3), Kind: "test1"}
	second := line.Overlay{Cols: position.NewSpan(5, 10), Kind: "test2"}
	// AddOverlay would cut these columns to the width of the line.
	wide := line.Overlay{Cols: position.NewSpan(-2, 99), Kind: "test1"}

	// Each call adds to the overlays of its line and keeps their columns.
	view.AddLineOverlay(1, first)
	view.AddLineOverlay(1, second, wide)

	assert.Equal(t, line.Overlays{first, second, wide}, view.Overlays(1))
	assert.Nil(t, view.Overlays(0))
}

func TestView_AddOverlay(t *testing.T) {
	t.Parallel()

	three := stringtest.Input(`
		key1: value1
		key2: value2
		key3: value3
	`)

	tcs := map[string]struct {
		input  string
		ranges []position.Range
		want   []position.Spans // Columns of the overlays on each line.
	}{
		"single line range": {
			input:  "key: value\n",
			ranges: []position.Range{yamltest.LineRange(0, 0, 5)},
			want:   []position.Spans{{position.NewSpan(0, 5)}},
		},
		"columns are clamped to the line": {
			input:  "key: value\n",
			ranges: []position.Range{yamltest.LineRange(0, -3, 99)},
			want:   []position.Spans{{position.NewSpan(0, len("key: value"))}},
		},
		// Empty at a column inside the line, and a range starting past the
		// end.
		"range covering no columns adds nothing": {
			input:  "key: value\n",
			ranges: []position.Range{yamltest.LineRange(0, 3, 3), yamltest.LineRange(0, 50, 60)},
			want:   []position.Spans{nil},
		},
		// The first line takes column 3 to its end, the middle line all of
		// its columns, and the last line its start to column 5.
		"multi-line range splits across lines": {
			input:  three,
			ranges: []position.Range{position.NewRange(position.New(0, 3), position.New(2, 5))},
			want: []position.Spans{
				{position.NewSpan(3, len("key1: value1"))},
				{position.NewSpan(0, len("key2: value2"))},
				{position.NewSpan(0, 5)},
			},
		},
		"multiple ranges": {
			input:  three,
			ranges: []position.Range{yamltest.LineRange(0, 0, 4), yamltest.LineRange(1, 0, 4)},
			want:   []position.Spans{{position.NewSpan(0, 4)}, {position.NewSpan(0, 4)}, nil},
		},
		// A range that starts before the first line and ends past the last
		// applies only to the lines that exist, and a range entirely
		// outside the view adds nothing.
		"out of range lines are skipped": {
			input: stringtest.LinesLF(letterRows(2)...),
			ranges: []position.Range{
				position.NewRange(position.New(-1, 0), position.New(5, 3)),
				yamltest.LineRange(7, 0, 3),
			},
			want: []position.Spans{{position.NewSpan(0, 4)}, {position.NewSpan(0, 4)}},
		},
		// Both ranges end on the line before they start, one at column 0
		// and one past it.
		"inverted ranges add nothing": {
			input: stringtest.LinesLF(letterRows(3)...),
			ranges: []position.Range{
				position.NewRange(position.New(2, 0), position.New(1, 0)),
				position.NewRange(position.New(2, 0), position.New(1, 3)),
			},
			want: []position.Spans{nil, nil, nil},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := newView(tc.input)
			require.Equal(t, len(tc.want), view.Count())
			view.AddOverlay("test1", tc.ranges...)

			blended := newView(tc.input)
			blended.BlendOverlay("test1", tc.ranges...)

			for i, cols := range tc.want {
				// Each overlay carries the kind. One from AddOverlay
				// replaces the style under it, and one from BlendOverlay
				// blends with it.
				var want, wantBlended line.Overlays

				for _, span := range cols {
					want = append(want, line.Overlay{Kind: "test1", Cols: span})
					wantBlended = append(wantBlended, line.Overlay{Kind: "test1", Cols: span, Blend: true})
				}

				assert.Equal(t, want, view.Overlays(i), "line %d", i)
				assert.Equal(t, wantBlended, blended.Overlays(i), "line %d blended", i)
			}
		})
	}
}

func TestView_BlendOverlay(t *testing.T) {
	t.Parallel()

	// TestView_AddOverlay runs its ranges through BlendOverlay too. The two
	// kinds of overlay mix on one line in the order of the calls.
	view := newView("key: value\n")
	view.AddOverlay("test1", yamltest.LineRange(0, 0, 3))
	view.BlendOverlay("test2", yamltest.LineRange(0, 0, 3))

	assert.Equal(t, line.Overlays{
		{Kind: "test1", Cols: position.NewSpan(0, 3)},
		{Kind: "test2", Cols: position.NewSpan(0, 3), Blend: true},
	}, view.Overlays(0))
}

// TestView_Clone checks Clone together with Slice and Clip, which also
// return a copy of the view. A slice by a span over every line holds every
// line of the view, and a clip leaves its lines whole. Each copy shares
// the lines of the view and owns its decoration.
func TestView_Clone(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		key: value
		list:
		  - one
	`)

	tcs := map[string]func(*line.View) *line.View{
		"Clone": (*line.View).Clone,
		"Slice": func(v *line.View) *line.View { return v.Slice(position.NewSpan(0, 3)) },
		"Clip":  func(v *line.View) *line.View { return v.Clip(10) },
	}

	for name, copyOf := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			t.Run("shares lines", func(t *testing.T) {
				t.Parallel()

				view := newView(input)
				got := copyOf(view)

				require.NotSame(t, view, got)
				require.Equal(t, 3, got.Count())
				assert.Equal(t, view.Held().Content(), got.Held().Content())

				for i, ln := range view.All() {
					j, ok := got.Index(ln)
					require.True(t, ok, "line %d", i)
					assert.Equal(t, i, j)
				}
			})

			t.Run("carries decoration of its own", func(t *testing.T) {
				t.Parallel()

				overlay := line.Overlay{Cols: position.NewSpan(0, 5), Kind: "test1"}
				note := line.Annotation{Content: "original note", Placement: line.Below}

				view := newView(input)
				view.SetFlag(1, line.FlagDeleted)
				view.AddLineOverlay(1, overlay)
				view.Annotate(1, note)

				got := copyOf(view)

				assert.Equal(t, flagsOf(view), flagsOf(got))
				assert.Equal(t, line.Overlays{overlay}, got.Overlays(1))
				assert.Equal(t, line.Annotations{note}, got.Annotations(1))

				// Decoration added to the copy, on a line that has some and
				// on a line that has none, leaves the original as it was.
				got.SetFlag(1, line.FlagInserted)
				got.SetFlag(2, line.FlagInserted)
				got.AddLineOverlay(1, line.Overlay{Cols: position.NewSpan(5, 10), Kind: "test2"})
				got.Annotate(1, line.Annotation{Content: "modified", Placement: line.Above})

				assert.Equal(t, []line.Flag{line.FlagDefault, line.FlagDeleted, line.FlagDefault}, flagsOf(view))
				assert.Equal(t, line.Overlays{overlay}, view.Overlays(1))
				assert.Equal(t, line.Annotations{note}, view.Annotations(1))
				assert.Len(t, got.Overlays(1), 2)
				assert.Len(t, got.Annotations(1), 2)
			})

			t.Run("decoration is independent in both directions", func(t *testing.T) {
				t.Parallel()

				view := newView(input)
				got := copyOf(view)

				got.AddOverlay(kind.GenericHighlight, yamltest.LineRange(0, 0, 3))
				got.Annotate(1, line.Annotation{Content: "note", Placement: line.Below})
				got.SetFlag(2, line.FlagInserted)

				assert.Empty(t, view.Overlays(0))
				assert.Empty(t, view.Annotations(1))
				assert.Equal(t, line.FlagDefault, view.Flag(2))

				view.AddOverlay(kind.GenericError, yamltest.LineRange(1, 0, 3))
				view.Annotate(0, line.Annotation{Content: "other", Placement: line.Above})
				view.SetFlag(0, line.FlagDeleted)

				assert.Empty(t, got.Overlays(1))
				assert.Empty(t, got.Annotations(0))
				assert.Equal(t, line.FlagDefault, got.Flag(0))

				// Decorating one leaves the other intact.
				require.Len(t, got.Overlays(0), 1)
			})

			t.Run("undecorated", func(t *testing.T) {
				t.Parallel()

				got := copyOf(newView(input))

				for i := range got.All() {
					assert.Equal(t, line.FlagDefault, got.Flag(i))
					assert.Nil(t, got.Overlays(i))
					assert.Nil(t, got.Annotations(i))
				}
			})
		})
	}

	t.Run("empty", func(t *testing.T) {
		t.Parallel()

		clone := line.NewView(line.Lines{}).Clone()

		require.NotNil(t, clone)
		assert.Equal(t, 0, clone.Count())
	})
}

func TestView_Index(t *testing.T) {
	t.Parallel()

	input := stringtest.JoinLF(letterRows(4)...)

	view := newView(input)
	other := newView(input)

	a, b := view.Line(0), view.Line(1)
	repeated := line.NewView(line.Collect(a, b, a, b, a))

	tcs := map[string]struct {
		view *line.View
		line *line.Line
		want int
		ok   bool
	}{
		"line of the view": {
			view: view,
			line: view.Line(2),
			want: 2,
			ok:   true,
		},
		"line of a view over held lines": {
			view: line.NewView(view.Slice(position.NewSpan(1, 3)).Held()),
			line: view.Line(2),
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
			line: view.Line(2),
			want: 2,
			ok:   true,
		},
		"line of a slice that dropped it": {
			view: view.Slice(position.NewSpan(0, 2)),
			line: view.Line(2),
		},
		"line of other content with the same text": {
			view: view,
			line: other.Line(2),
		},
		"nil line": {
			view: view,
			line: nil,
		},
		"nil view": {
			view: nil,
			line: view.Line(0),
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

func TestView_Slice(t *testing.T) {
	t.Parallel()

	// TestSequence checks which lines a slice holds, and TestView_Clone
	// checks that a slice is a copy that owns its decoration.
	input := stringtest.JoinLF(letterRows(4)...)

	// Decorated returns a view with a distinct flag, overlay, and annotation
	// on every line so a test can check a slice against its source.
	decorated := func() *line.View {
		view := newView(input)

		for i, ln := range view.All() {
			view.SetFlag(i, line.Flag(i))
			view.AddLineOverlay(i, line.Overlay{Cols: position.NewSpan(0, i+1), Kind: "test"})
			view.Annotate(i, line.Annotation{Content: ln.Content(), Placement: line.Below})
		}

		return view
	}

	t.Run("carries decoration", func(t *testing.T) {
		t.Parallel()

		view := decorated()
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

	t.Run("keeps the coordinates of the content", func(t *testing.T) {
		t.Parallel()

		// Decorating before or after slicing renders the same, since a
		// slice takes ranges and indices in the coordinates of its content.
		rng := yamltest.LineRange(2, 3, 4)
		ann := line.Annotation{Content: "here", Placement: line.Below, Col: 3}

		before := newView(input)
		before.AddOverlay("test", rng)
		before.Annotate(2, ann)

		before = before.Slice(position.NewSpan(2, 4))

		after := newView(input).Slice(position.NewSpan(2, 4))
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

		view := newView(input)
		got := view.Slice(position.NewSpan(1, 4)).Slice(position.NewSpan(0, 3))

		assert.Equal(t, []string{"b: 2", "c: 3"}, contentsOf(got.All()))
		assert.True(t, got.Contains(1))
		assert.False(t, got.Contains(0))
		assert.False(t, got.Contains(3))
	})

	t.Run("decoration on a line the slice does not hold never renders", func(t *testing.T) {
		t.Parallel()

		view := newView(input).Slice(position.NewSpan(0, 2))
		view.AddOverlay("test", yamltest.LineRange(3, 0, 4))
		view.Annotate(3, line.Annotation{Content: "hidden", Placement: line.Below})

		assert.Len(t, view.Overlays(3), 1)
		assert.Equal(t, stringtest.JoinLF("   1 | a: 1", "   2 | b: 2"), view.String())
	})

	t.Run("String renders the sliced lines", func(t *testing.T) {
		t.Parallel()

		view := decorated()
		got := view.Slice(position.NewSpan(2, 3))

		assert.Equal(t, stringtest.JoinLF("   3 | c: 3", "     | ^^^ c: 3"), got.String())
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

		view := line.NewView(newLines(yamltest.GenerateYAML(n)))
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

	t.Run("multiple lines", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			key1: value1
			key2: value2
		`)
		view := newView(input)

		assert.Equal(t, view.Held().String(), view.String())
		assert.Equal(t, stringtest.JoinLF("   1 | key1: value1", "   2 | key2: value2"), view.String())
	})

	t.Run("annotations on several lines", func(t *testing.T) {
		t.Parallel()

		view := newView(stringtest.LinesLF(letterRows(3)...))
		view.Annotate(0, line.Annotation{Content: "top", Placement: line.Above})
		view.Annotate(2, line.Annotation{Content: "bottom", Placement: line.Below, Col: 3})

		want := stringtest.JoinLF(
			"     | top",
			"   1 | a: 1",
			"   2 | b: 2",
			"   3 | c: 3",
			"     |    ^ bottom",
		)

		assert.Equal(t, want, view.String())
	})

	t.Run("overlays mark their columns and flags do not render", func(t *testing.T) {
		t.Parallel()

		view := newView("key: value\n")
		view.SetFlag(0, line.FlagDeleted)
		view.AddOverlay("test1", yamltest.LineRange(0, 0, 3))
		view.BlendOverlay("test2", yamltest.LineRange(0, 5, 7))

		assert.Equal(t, stringtest.JoinLF("   1 | key: value", "     | ^^^  ^^"), view.String())
	})

	t.Run("annotations and marks on one line", func(t *testing.T) {
		t.Parallel()

		// The marks of a line that holds a price below its bound and a
		// deprecated field.
		const spec = "spec: {price: 0, legacyPricing: true}"

		errorOverlay := line.Overlay{Kind: kind.GenericError, Cols: position.NewSpan(14, 15)}
		warnOverlay := line.Overlay{Kind: kind.GenericWarn, Cols: position.NewSpan(17, 30)}
		errorAnn := line.Annotation{Content: "0 is less than 1", Kind: kind.TextError, Placement: line.Below, Col: 14}
		warnAnn := line.Annotation{
			Content:   `warning: deprecated field "legacyPricing"`,
			Kind:      kind.TextWarn,
			Placement: line.Below,
			Col:       17,
		}

		tcs := map[string]struct {
			input       string
			want        string
			ranges      []position.Range // Ranges that AddOverlay marks with the kind test1.
			overlays    line.Overlays    // Overlays that AddLineOverlay adds as they are.
			annotations []line.Annotation
		}{
			// An overlay that covers no column, such as the one a bound
			// error puts at the end of a line with no token under its
			// location, renders no caret itself, so the annotation below
			// the line marks the spot. An overlay that covers a column
			// marks the row alone.
			"annotation without content marks an empty span at the end of the line": {
				input:       "key: value",
				overlays:    line.Overlays{{Kind: "test1", Cols: position.NewSpan(10, 10)}},
				annotations: []line.Annotation{{Placement: line.Below, Col: 10}},
				want:        stringtest.JoinLF("   1 | key: value", "     |           ^"),
			},
			"annotation without content marks a span past the content": {
				input:       "key: value",
				overlays:    line.Overlays{{Kind: "test1", Cols: position.NewSpan(12, 14)}},
				annotations: []line.Annotation{{Placement: line.Below, Col: 10}},
				want:        stringtest.JoinLF("   1 | key: value", "     |           ^"),
			},
			"a span that covers a column marks the row alone": {
				input:       "key: value",
				overlays:    line.Overlays{{Kind: "test1", Cols: position.NewSpan(5, 7)}},
				annotations: []line.Annotation{{Placement: line.Below, Col: 10}},
				want:        stringtest.JoinLF("   1 | key: value", "     |      ^^"),
			},
			"each severity gets its own row, errors first": {
				input:       spec,
				overlays:    line.Overlays{warnOverlay, errorOverlay},
				annotations: []line.Annotation{warnAnn, errorAnn},
				want: stringtest.JoinLF(
					"   1 | spec: {price: 0, legacyPricing: true}",
					"     |               ^ 0 is less than 1",
					`     |                  ^^^^^^^^^^^^^ warning: deprecated field "legacyPricing"`,
				),
			},
			"a warning alone marks every overlay": {
				input:       spec,
				overlays:    line.Overlays{warnOverlay, errorOverlay},
				annotations: []line.Annotation{warnAnn},
				want: stringtest.JoinLF(
					"   1 | spec: {price: 0, legacyPricing: true}",
					`     |               ^  ^^^^^^^^^^^^^ warning: deprecated field "legacyPricing"`,
				),
			},
			"empty annotations draw the carets of their own severity": {
				input:    spec,
				overlays: line.Overlays{warnOverlay, errorOverlay},
				annotations: []line.Annotation{
					{Kind: kind.TextError, Placement: line.Below, Col: 14},
					{Kind: kind.TextWarn, Placement: line.Below, Col: 17},
				},
				want: stringtest.JoinLF(
					"   1 | spec: {price: 0, legacyPricing: true}",
					"     |               ^",
					"     |                  ^^^^^^^^^^^^^",
				),
			},
			"overlay and annotation share the marker row": {
				input:       "key: value",
				ranges:      []position.Range{yamltest.LineRange(0, 5, 10)},
				annotations: []line.Annotation{{Content: "bad value", Placement: line.Below, Col: 5}},
				want: stringtest.JoinLF(
					"   1 | key: value",
					"     |      ^^^^^ bad value",
				),
			},
			// Each CJK rune renders two cells wide, so the columns before
			// the marked ones are worth two carets each.
			"carets sit under wide runes": {
				input:       "名前: value",
				ranges:      []position.Range{yamltest.LineRange(0, 4, 9)},
				annotations: []line.Annotation{{Content: "bad", Placement: line.Below, Col: 4}},
				want: stringtest.JoinLF(
					"   1 | 名前: value",
					"     |       ^^^^^ bad",
				),
			},
			// The row above starts at the column of the annotation as the
			// content row renders it, so on wide characters it takes their
			// cells, as the marker row below does.
			"annotation above sits over wide runes": {
				input: "名前: v",
				annotations: []line.Annotation{
					{Content: "above", Placement: line.Above, Col: 2},
					{Content: "below", Placement: line.Below, Col: 2},
				},
				want: stringtest.JoinLF(
					"     |     above",
					"   1 | 名前: v",
					"     |     ^ below",
				),
			},
			// The acute accent renders on the e before it, so a mark on the
			// accent's column lands under the e, and an annotation at that
			// column starts there too.
			"carets on a combining mark sit under its base": {
				input:       "k: e\u0301x",
				ranges:      []position.Range{yamltest.LineRange(0, 4, 5)},
				annotations: []line.Annotation{{Content: "here", Placement: line.Below, Col: 4}},
				want: stringtest.JoinLF(
					"   1 | k: e\u0301x",
					"     |    ^ here",
				),
			},
			// The content row renders a ZWJ sequence or a keycap as one
			// cluster of two cells, so the columns before x are worth that
			// much and no more, and the caret lands under x as the row
			// above starts there.
			"carets sit under a zwj sequence": {
				input:  "a: \"\U0001F468\u200d\U0001F469\" x",
				ranges: []position.Range{yamltest.LineRange(0, 9, 10)},
				annotations: []line.Annotation{
					{Content: "above", Placement: line.Above, Col: 9},
					{Content: "below", Placement: line.Below, Col: 9},
				},
				want: stringtest.JoinLF(
					"     |         above",
					"   1 | a: \"\U0001F468\u200d\U0001F469\" x",
					"     |         ^ below",
				),
			},
			"carets sit under a keycap": {
				input:  "a: 1\ufe0f\u20e3 x",
				ranges: []position.Range{yamltest.LineRange(0, 7, 8)},
				annotations: []line.Annotation{
					{Content: "above", Placement: line.Above, Col: 7},
					{Content: "below", Placement: line.Below, Col: 7},
				},
				want: stringtest.JoinLF(
					"     |       above",
					"   1 | a: 1\ufe0f\u20e3 x",
					"     |       ^ below",
				),
			},
			// The joiner and the second emoji render as part of the cluster
			// the first emoji starts, so a mark on either lands under it.
			"a mark inside a grapheme cluster sits under its first rune": {
				input:  "a: \U0001F468\u200d\U0001F469 x",
				ranges: []position.Range{yamltest.LineRange(0, 5, 6)},
				want: stringtest.JoinLF(
					"   1 | a: \U0001F468\u200d\U0001F469 x",
					"     |    ^^",
				),
			},
			"control characters render as pictures": {
				input:       "a: \"x\ty\x1b\"",
				ranges:      []position.Range{yamltest.LineRange(0, 3, 9)},
				annotations: []line.Annotation{{Content: "tab\x1bhere", Placement: line.Above}},
				want: stringtest.JoinLF(
					"     | tab\u241bhere",
					"   1 | a: \"x\u2409y\u241b\"",
					"     |    ^^^^^^",
				),
			},
			"no annotation": {
				input:       "key: value",
				annotations: nil,
				want:        "   1 | key: value",
			},
			"annotation below at start": {
				input:       "key: value",
				annotations: []line.Annotation{{Content: "error here", Placement: line.Below}},
				want:        stringtest.JoinLF("   1 | key: value", "     | ^ error here"),
			},
			"annotation below with padding": {
				input:       "key: value",
				annotations: []line.Annotation{{Content: "note", Placement: line.Below, Col: 4}},
				want:        stringtest.JoinLF("   1 | key: value", "     |     ^ note"),
			},
			"annotation above": {
				input:       "key: value",
				annotations: []line.Annotation{{Content: "@@ hunk header @@", Placement: line.Above}},
				want:        stringtest.JoinLF("     | @@ hunk header @@", "   1 | key: value"),
			},
			"annotation above without content adds no row": {
				input:       "key: value",
				annotations: []line.Annotation{{Content: "", Placement: line.Above, Col: 2}},
				want:        "   1 | key: value",
			},
			"annotations below without content get one caret": {
				input: "key: value",
				annotations: []line.Annotation{
					{Content: "", Placement: line.Below, Col: 4},
					{Content: "", Placement: line.Below, Col: 2},
				},
				want: stringtest.JoinLF("   1 | key: value", "     |   ^"),
			},
			"annotations above of each kind get a row of their own": {
				input: "key: value",
				annotations: []line.Annotation{
					{Content: "@@ hunk header @@", Kind: kind.UIHunkHeader, Placement: line.Above},
					{Content: "first", Placement: line.Above, Col: 5},
					{Content: "", Kind: kind.UISeparator, Placement: line.Above},
					{Content: "second", Kind: kind.UIAnnotation, Placement: line.Above, Col: 2},
				},
				want: stringtest.JoinLF(
					"     | @@ hunk header @@",
					"     |   second; first",
					"   1 | key: value",
				),
			},
			"annotations above and below": {
				input: "key: value",
				annotations: []line.Annotation{
					{Content: "@@ hunk header @@", Placement: line.Above},
					{Content: "note", Placement: line.Below, Col: 2},
				},
				want: stringtest.JoinLF(
					"     | @@ hunk header @@",
					"   1 | key: value",
					"     |   ^ note",
				),
			},
			"multiple annotations below join in column order at the minimum column": {
				input: "key: value",
				annotations: []line.Annotation{
					{Content: "first", Placement: line.Below, Col: 5},
					{Content: "second", Placement: line.Below, Col: 2},
				},
				want: stringtest.JoinLF("   1 | key: value", "     |   ^ second; first"),
			},
			"an annotation without content does not set the column": {
				input: "key: value",
				annotations: []line.Annotation{
					{Content: "", Placement: line.Below, Col: 0},
					{Content: "boom", Placement: line.Below, Col: 5},
				},
				want: stringtest.JoinLF("   1 | key: value", "     |      ^ boom"),
			},
			"annotations below of each kind get a row of their own": {
				input: "key: value",
				annotations: []line.Annotation{
					{Content: "hint", Placement: line.Below, Col: 0},
					{Content: "bad value", Kind: kind.TextError, Placement: line.Below, Col: 5},
				},
				want: stringtest.JoinLF(
					"   1 | key: value",
					"     | ^ hint",
					"     |      ^ bad value",
				),
			},
			"a kind below without content keeps its caret beside another kind": {
				input: "key: value",
				annotations: []line.Annotation{
					{Content: "", Placement: line.Below, Col: 2},
					{Content: "bad value", Kind: kind.TextError, Placement: line.Below, Col: 5},
				},
				want: stringtest.JoinLF(
					"   1 | key: value",
					"     |   ^",
					"     |      ^ bad value",
				),
			},
			"negative column is not padded": {
				input:       "key: value",
				annotations: []line.Annotation{{Content: "note", Placement: line.Below, Col: -3}},
				want:        stringtest.JoinLF("   1 | key: value", "     | ^ note"),
			},

			// A column more than 1024 columns past the end of the content
			// starts 1024 columns past it, after every cell of the content,
			// as the printer starts it.
			"below at the max column": {
				input:       "a: 1",
				annotations: []line.Annotation{{Content: "msg", Placement: line.Below, Col: math.MaxInt}},
				want:        stringtest.JoinLF("   1 | a: 1", "     | "+strings.Repeat(" ", 4+1024)+"^ msg"),
			},
			"above at the max column": {
				input:       "a: 1",
				annotations: []line.Annotation{{Content: "msg", Placement: line.Above, Col: math.MaxInt}},
				want:        stringtest.JoinLF("     | "+strings.Repeat(" ", 4+1024)+"msg", "   1 | a: 1"),
			},
			"below past the bound": {
				input:       "a: 1",
				annotations: []line.Annotation{{Content: "msg", Placement: line.Below, Col: 1 << 32}},
				want:        stringtest.JoinLF("   1 | a: 1", "     | "+strings.Repeat(" ", 4+1024)+"^ msg"),
			},
			"above past the bound": {
				input:       "a: 1",
				annotations: []line.Annotation{{Content: "msg", Placement: line.Above, Col: 1 << 32}},
				want:        stringtest.JoinLF("     | "+strings.Repeat(" ", 4+1024)+"msg", "   1 | a: 1"),
			},
			"below at the bound": {
				input:       "a: 1",
				annotations: []line.Annotation{{Content: "msg", Placement: line.Below, Col: 4 + 1024}},
				want:        stringtest.JoinLF("   1 | a: 1", "     | "+strings.Repeat(" ", 4+1024)+"^ msg"),
			},
			"below at the max column after wide runes": {
				input:       "k: 日本語",
				annotations: []line.Annotation{{Content: "msg", Placement: line.Below, Col: math.MaxInt}},
				want:        stringtest.JoinLF("   1 | k: 日本語", "     | "+strings.Repeat(" ", 9+1024)+"^ msg"),
			},
			"above at the max column after wide runes": {
				input:       "k: 日本語",
				annotations: []line.Annotation{{Content: "msg", Placement: line.Above, Col: math.MaxInt}},
				want:        stringtest.JoinLF("     | "+strings.Repeat(" ", 9+1024)+"msg", "   1 | k: 日本語"),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				view := newView(tc.input + "\n")
				view.AddOverlay("test1", tc.ranges...)
				view.AddLineOverlay(0, tc.overlays...)
				view.Annotate(0, tc.annotations...)

				assert.Equal(t, tc.want, view.String())
			})
		}
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
		"Windows":        func(v *line.View, i int) { v.Windows(i) },
	}

	for name, call := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			for _, i := range []int{-1, 2, 99} {
				// Undecorated, so the check happens before any slice exists.
				view := newView(stringtest.LinesLF(letterRows(2)...))

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

func TestView_Hunks(t *testing.T) {
	t.Parallel()

	input := stringtest.JoinLF(letterRows(8)...)

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

		view := newView(input)
		view.SetFlag(0, line.FlagInserted)
		view.AddOverlay("test", yamltest.LineRange(7, 0, 1))
		view.Annotate(4, line.Annotation{Content: "here", Placement: line.Below})

		got := view.Hunks(1)

		assert.Equal(t, []string{"a: 1", "b: 2", "d: 4", "e: 5", "f: 6", "g: 7", "h: 8"}, contentsOf(got.All()))
		assert.Equal(t, []int{3}, separators(got), "one separator, above the second hunk")
		assert.Equal(t, line.FlagInserted, got.Flag(0))
		assert.Len(t, got.Overlays(7), 1)
		assert.Len(t, got.Annotations(4), 1)
	})

	t.Run("holds the context around each annotated line", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			held           []position.Span // Spans the view holds, or none for every line.
			annotated      []int           // Lines that get an annotation.
			want           []string
			wantSeparators []int
			context        int
		}{
			"touching windows share a hunk": {
				annotated: []int{1, 5},
				context:   2,
				want:      []string{"a: 1", "b: 2", "c: 3", "d: 4", "e: 5", "f: 6", "g: 7", "h: 8"},
			},
			"a negative context shows the decorated lines alone": {
				annotated:      []int{1, 6},
				context:        -1,
				want:           []string{"b: 2", "g: 7"},
				wantSeparators: []int{6},
			},
			"a slice yields hunks within the slice": {
				held:           []position.Span{position.NewSpan(2, 8)},
				annotated:      []int{2, 7},
				context:        1,
				want:           []string{"c: 3", "d: 4", "g: 7", "h: 8"},
				wantSeparators: []int{6},
			},
			// The view skips line d, so the second hunk's window starts on
			// a line the view does not hold.
			"the separator goes above the first line the hunk holds": {
				held:           []position.Span{position.NewSpan(0, 3), position.NewSpan(4, 8)},
				annotated:      []int{0, 4},
				context:        1,
				want:           []string{"a: 1", "b: 2", "e: 5", "f: 6"},
				wantSeparators: []int{4},
			},
			// The view skips lines c through f, so both contexts hold lines
			// a, b, g, and h. A context of 1 keeps two hunks, and a context
			// of 3 merges them into one across the gap. The same lines get
			// the same separators at either context.
			"two hunks beside a gap the view skips": {
				held:           []position.Span{position.NewSpan(0, 2), position.NewSpan(6, 8)},
				annotated:      []int{1, 6},
				context:        1,
				want:           []string{"a: 1", "b: 2", "g: 7", "h: 8"},
				wantSeparators: []int{6},
			},
			"one hunk across a gap the view skips": {
				held:           []position.Span{position.NewSpan(0, 2), position.NewSpan(6, 8)},
				annotated:      []int{1, 6},
				context:        3,
				want:           []string{"a: 1", "b: 2", "g: 7", "h: 8"},
				wantSeparators: []int{6},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				// Without held spans, the view holds every line.
				view := newView(input)
				if tc.held != nil {
					view = view.Slice(tc.held...)
				}

				for _, i := range tc.annotated {
					view.Annotate(i, line.Annotation{Content: "note", Placement: line.Below})
				}

				got := view.Hunks(tc.context)

				assert.Equal(t, tc.want, contentsOf(got.All()))
				assert.Equal(t, tc.wantSeparators, separators(got))
			})
		}
	})

	t.Run("no decoration yields no line", func(t *testing.T) {
		t.Parallel()

		view := newView(input)

		got := view.Hunks(2)

		assert.Equal(t, 0, got.Count())
		assert.Same(t, view.Line(7), got.Line(7), "the content is still the whole")
		assert.Panics(t, func() { got.Line(8) })
	})

	t.Run("hunks of hunks match hunks of the view", func(t *testing.T) {
		t.Parallel()

		// The hunks at context 2 hold a separator above line g, a context
		// line, which must not count as decoration when narrowed.
		view := newView(stringtest.JoinLF(letterRows(10)...))
		view.Annotate(1, line.Annotation{Content: "x", Placement: line.Below})
		view.Annotate(8, line.Annotation{Content: "y", Placement: line.Below})

		hunks := view.Hunks(2)
		require.Equal(t, []int{6}, separators(hunks))

		for _, context := range []int{0, 1, 2} {
			want := view.Hunks(context)
			got := hunks.Hunks(context)

			assert.Equal(t, want.String(), got.String(), "context %d", context)
			assert.Equal(t, separators(want), separators(got), "context %d", context)
		}

		assert.Equal(t, []int{6}, separators(hunks), "narrowing leaves the hunks as they were")
	})

	t.Run("renders as an excerpt", func(t *testing.T) {
		t.Parallel()

		one := line.Annotation{Content: "one", Placement: line.Below}
		two := line.Annotation{Content: "two", Placement: line.Below}

		tcs := map[string]struct {
			held        []position.Span         // Spans the view holds, or none for every line.
			annotations map[int]line.Annotation // The annotation of each annotated line.
			want        []string                // Rows of the rendered hunks.
			context     int
		}{
			"hunks with their context": {
				annotations: map[int]line.Annotation{1: one, 6: two},
				context:     1,
				want: []string{
					"   1 | a: 1",
					"   2 | b: 2",
					"     | ^ one",
					"   3 | c: 3",
					"     | ...",
					"   6 | f: 6",
					"   7 | g: 7",
					"     | ^ two",
					"   8 | h: 8",
				},
			},
			// The windows of lines c and e overlap across line d, which the
			// view skips, so they share a hunk that jumps from c to e.
			"a gap the view skips inside a hunk gets a separator": {
				held:        []position.Span{position.NewSpan(0, 3), position.NewSpan(4, 8)},
				annotations: map[int]line.Annotation{2: one, 4: two},
				context:     1,
				want: []string{
					"   2 | b: 2",
					"   3 | c: 3",
					"     | ^ one",
					"     | ...",
					"   5 | e: 5",
					"     | ^ two",
					"   6 | f: 6",
				},
			},
			"separator sits above an annotated first line": {
				annotations: map[int]line.Annotation{1: one, 6: {Content: "two", Placement: line.Above}},
				want: []string{
					"   2 | b: 2",
					"     | ^ one",
					"     | ...",
					"     | two",
					"   7 | g: 7",
				},
			},
			"separator keeps the column of an annotation above": {
				annotations: map[int]line.Annotation{1: one, 6: {Content: "hint", Placement: line.Above, Col: 3}},
				want: []string{
					"   2 | b: 2",
					"     | ^ one",
					"     | ...",
					"     |    hint",
					"   7 | g: 7",
				},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				// Without held spans, the view holds every line.
				view := newView(input)
				if tc.held != nil {
					view = view.Slice(tc.held...)
				}

				for i, ann := range tc.annotations {
					view.Annotate(i, ann)
				}

				assert.Equal(t, stringtest.JoinLF(tc.want...), view.Hunks(tc.context).String())
			})
		}
	})
}

func TestView_Clip(t *testing.T) {
	t.Parallel()

	const (
		letters = "key: abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
		couple  = "\U0001F468\u200d\U0001F469"
	)

	a := func(n int) string { return strings.Repeat("a", n) }
	b := func(n int) string { return strings.Repeat("b", n) }

	tcs := map[string]struct {
		input       string
		want        []string
		windows     position.Spans
		overlays    []position.Span
		annotations []line.Annotation
		cols        int
	}{
		"line that fits stays whole": {
			input: "key: value",
			cols:  10,
			want:  []string{"   1 | key: value"},
		},
		"line with no mark keeps its first columns": {
			input:   letters,
			cols:    10,
			windows: position.Spans{position.NewSpan(0, 10)},
			want:    []string{"   1 | key: abcde..."},
		},
		"window centers on its mark": {
			input:    letters,
			cols:     10,
			overlays: []position.Span{position.NewSpan(30, 32)},
			windows:  position.Spans{position.NewSpan(25, 35)},
			want: []string{
				"   1 | ...uvwxyzABCD...",
				"     |         ^^",
			},
		},
		"window near the start stays inside the line": {
			input:    letters,
			cols:     10,
			overlays: []position.Span{position.NewSpan(2, 3)},
			windows:  position.Spans{position.NewSpan(0, 10)},
			want: []string{
				"   1 | key: abcde...",
				"     |   ^",
			},
		},
		"window near the end stays inside the line": {
			input:    letters,
			cols:     10,
			overlays: []position.Span{position.NewSpan(55, 57)},
			windows:  position.Spans{position.NewSpan(47, 57)},
			want: []string{
				"   1 | ...QRSTUVWXYZ",
				"     |            ^^",
			},
		},
		"two marks far apart get a window each": {
			input:    letters,
			cols:     10,
			overlays: []position.Span{position.NewSpan(15, 16), position.NewSpan(45, 46)},
			annotations: []line.Annotation{
				{Content: "first", Placement: line.Below, Col: 15},
				{Content: "second", Placement: line.Below, Col: 45},
			},
			windows: position.Spans{position.NewSpan(10, 20), position.NewSpan(40, 50)},
			want: []string{
				"   1 | ...fghijklmno...JKLMNOPQRS...",
				"     |         ^            ^ first; second",
			},
		},
		"windows three columns apart join": {
			input:    letters,
			cols:     10,
			overlays: []position.Span{position.NewSpan(20, 21), position.NewSpan(33, 34)},
			windows:  position.Spans{position.NewSpan(15, 38)},
			want: []string{
				"   1 | ...klmnopqrstuvwxyzABCDEFG...",
				"     |         ^            ^",
			},
		},
		"windows four columns apart stay apart": {
			input:    letters,
			cols:     10,
			overlays: []position.Span{position.NewSpan(20, 21), position.NewSpan(34, 35)},
			windows:  position.Spans{position.NewSpan(15, 25), position.NewSpan(29, 39)},
			want: []string{
				"   1 | ...klmnopqrst...yzABCDEFGH...",
				"     |         ^            ^",
			},
		},
		"window three columns from an end reaches it": {
			input:    letters,
			cols:     10,
			overlays: []position.Span{position.NewSpan(8, 9), position.NewSpan(49, 50)},
			windows:  position.Spans{position.NewSpan(0, 13), position.NewSpan(44, 57)},
			want: []string{
				"   1 | key: abcdefgh...NOPQRSTUVWXYZ",
				"     |         ^            ^",
			},
		},
		"line three columns longer than the clip stays whole": {
			input:    "key: abcdefgh",
			cols:     10,
			overlays: []position.Span{position.NewSpan(5, 6)},
			want: []string{
				"   1 | key: abcdefgh",
				"     |      ^",
			},
		},
		"long overlay shows the columns from its start": {
			input:    letters,
			cols:     10,
			overlays: []position.Span{position.NewSpan(20, 57)},
			windows:  position.Spans{position.NewSpan(15, 25)},
			want: []string{
				"   1 | ...klmnopqrst...",
				"     |         ^^^^^",
			},
		},
		"overlay that spans two windows marks its part of each": {
			input:    letters,
			cols:     10,
			overlays: []position.Span{position.NewSpan(15, 47), position.NewSpan(45, 46)},
			windows:  position.Spans{position.NewSpan(10, 20), position.NewSpan(40, 50)},
			want: []string{
				"   1 | ...fghijklmno...JKLMNOPQRS...",
				"     |         ^^^^^   ^^^^^^^",
			},
		},
		"annotation alone marks its column": {
			input:       letters,
			cols:        10,
			annotations: []line.Annotation{{Content: "here", Placement: line.Below, Col: 40}},
			windows:     position.Spans{position.NewSpan(35, 45)},
			want: []string{
				"   1 | ...EFGHIJKLMN...",
				"     |         ^ here",
			},
		},
		"annotation above sits over its column": {
			input:       letters,
			cols:        10,
			annotations: []line.Annotation{{Content: "above", Placement: line.Above, Col: 40}},
			windows:     position.Spans{position.NewSpan(35, 45)},
			want: []string{
				"     |         above",
				"   1 | ...EFGHIJKLMN...",
			},
		},
		"annotation past the end marks the end": {
			input:       letters,
			cols:        10,
			annotations: []line.Annotation{{Content: "end", Placement: line.Below, Col: 58}},
			windows:     position.Spans{position.NewSpan(47, 57)},
			want: []string{
				"   1 | ...QRSTUVWXYZ",
				"     |               ^ end",
			},
		},
		"overlay of no width at the end marks the end": {
			input:       letters,
			cols:        10,
			overlays:    []position.Span{position.NewSpan(57, 57)},
			annotations: []line.Annotation{{Placement: line.Below, Col: 57}},
			windows:     position.Spans{position.NewSpan(47, 57)},
			want: []string{
				"   1 | ...QRSTUVWXYZ",
				"     |              ^",
			},
		},
		"separator is no mark and starts at the ellipsis": {
			input:    letters,
			cols:     10,
			overlays: []position.Span{position.NewSpan(40, 41)},
			annotations: []line.Annotation{
				{Content: "---", Kind: kind.UISeparator, Placement: line.Above, Col: 20},
			},
			windows: position.Spans{position.NewSpan(35, 45)},
			want: []string{
				"     | ---",
				"   1 | ...EFGHIJKLMN...",
				"     |         ^",
			},
		},
		"wide runes count one column and take two cells": {
			input:    "k: " + strings.Repeat("日本語", 8),
			cols:     8,
			overlays: []position.Span{position.NewSpan(15, 16)},
			windows:  position.Spans{position.NewSpan(11, 19)},
			want: []string{
				"   1 | ...語日本語日本語日...",
				"     |            ^^",
			},
		},
		"tab counts one column and shows as its picture": {
			input:    "k: a\tb\tc\td\te\tf\tg\th\ti\tj\tk\tl\tm",
			cols:     8,
			overlays: []position.Span{position.NewSpan(15, 16)},
			windows:  position.Spans{position.NewSpan(11, 19)},
			want: []string{
				"   1 | ...e␉f␉g␉h␉...",
				"     |        ^",
			},
		},
		"mark inside a grapheme cluster shows the cluster": {
			input:    "k: " + couple + a(12) + couple + b(24),
			cols:     8,
			overlays: []position.Span{position.NewSpan(20, 21)},
			windows:  position.Spans{position.NewSpan(16, 24)},
			want: []string{
				"   1 | ...aa" + couple + "bbb...",
				"     |      ^^",
			},
		},
		"window grows to hold a grapheme cluster at its end": {
			input:    "k: " + a(25) + couple + b(24),
			cols:     8,
			overlays: []position.Span{position.NewSpan(25, 26)},
			windows:  position.Spans{position.NewSpan(21, 31)},
			want: []string{
				"   1 | ...aaaaaaa" + couple + "...",
				"     |        ^",
			},
		},
		"window grows to hold a grapheme cluster at its start": {
			input:    "k: " + a(25) + couple + b(24),
			cols:     8,
			overlays: []position.Span{position.NewSpan(33, 34)},
			windows:  position.Spans{position.NewSpan(28, 37)},
			want: []string{
				"   1 | ..." + couple + "bbbbbb...",
				"     |        ^",
			},
		},
		"combining marks stay with their letters": {
			input:    "k: " + a(25) + strings.Repeat("e\u0301", 5) + b(24),
			cols:     6,
			overlays: []position.Span{position.NewSpan(31, 32)},
			windows:  position.Spans{position.NewSpan(28, 34)},
			want: []string{
				"   1 | ..." + strings.Repeat("e\u0301", 3) + "...",
				"     |     ^",
			},
		},
		"clip of one column keeps the mark": {
			input:    letters,
			cols:     1,
			overlays: []position.Span{position.NewSpan(30, 31)},
			windows:  position.Spans{position.NewSpan(30, 31)},
			want: []string{
				"   1 | ...z...",
				"     |    ^",
			},
		},
		"clip of zero shows the whole line": {
			input:    letters,
			cols:     0,
			overlays: []position.Span{position.NewSpan(30, 31)},
			want: []string{
				"   1 | " + letters,
				"     | " + strings.Repeat(" ", 30) + "^",
			},
		},
		"negative clip shows the whole line": {
			input: letters,
			cols:  -1,
			want:  []string{"   1 | " + letters},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := newView(tc.input + "\n")

			for _, cols := range tc.overlays {
				view.AddLineOverlay(0, line.Overlay{Kind: kind.GenericError, Cols: cols})
			}

			view.Annotate(0, tc.annotations...)

			clipped := view.Clip(tc.cols)

			assert.Equal(t, tc.windows, clipped.Windows(0))
			assert.Equal(t, stringtest.JoinLF(tc.want...), clipped.String())

			// The view the clip came from shows every line whole.
			assert.Nil(t, view.Windows(0))
		})
	}
}

func TestView_Clip_KeepsView(t *testing.T) {
	t.Parallel()

	// TestView_Clone checks that a clip is a copy that owns its
	// decoration.
	long := "key: " + strings.Repeat("x", 40)
	input := stringtest.LinesLF("a: 1", long, "c: 3", "d: 4")

	newMarked := func() *line.View {
		view := newView(input)
		view.SetFlag(1, line.FlagInserted)
		view.AddOverlay(kind.GenericError, yamltest.LineRange(1, 30, 32))
		view.Annotate(1, line.Annotation{Content: "bad", Placement: line.Below, Col: 30})

		return view
	}

	window := position.Spans{position.NewSpan(25, 35)}

	t.Run("mark added after the clip gets a window", func(t *testing.T) {
		t.Parallel()

		clipped := newMarked().Clip(10)
		require.Equal(t, window, clipped.Windows(1))

		clipped.Annotate(1, line.Annotation{Content: "more", Placement: line.Below, Col: 12})

		assert.Equal(t, position.Spans{position.NewSpan(7, 17), position.NewSpan(25, 35)}, clipped.Windows(1))
		assert.Equal(t, stringtest.JoinLF(
			"   1 | a: 1",
			"   2 | ...xxxxxxxxxx...xxxxxxxxxx...",
			"     |         ^            ^^ more; bad",
			"   3 | c: 3",
			"   4 | d: 4",
		), clipped.String())
	})

	t.Run("clone, slice, and hunks keep the clip", func(t *testing.T) {
		t.Parallel()

		clipped := newMarked().Clip(10)

		assert.Equal(t, window, clipped.Clone().Windows(1))
		assert.Equal(t, window, clipped.Slice(position.NewSpan(1, 2)).Windows(1))
		assert.Equal(t, stringtest.JoinLF(
			"   2 | ...xxxxxxxxxx...",
			"     |         ^^ bad",
		), clipped.Hunks(0).String())
	})

	t.Run("clip of the hunks matches the hunks of the clip", func(t *testing.T) {
		t.Parallel()

		view := newMarked()

		assert.Equal(t, view.Clip(10).Hunks(1).String(), view.Hunks(1).Clip(10).String())
	})

	t.Run("second clip replaces the first", func(t *testing.T) {
		t.Parallel()

		clipped := newMarked().Clip(10)

		assert.Equal(t, position.Spans{position.NewSpan(20, 40)}, clipped.Clip(20).Windows(1))
		assert.Nil(t, clipped.Clip(0).Windows(1))
		assert.Equal(t, window, clipped.Windows(1))
	})
}
