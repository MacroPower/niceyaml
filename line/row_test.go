package line_test

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

// rowSegments joins segs as "kind(text)", with the kind and the columns
// of each overlay covering a segment in brackets after it. A test thus
// reads the row at a glance.
func rowSegments(segs []line.Segment) string {
	parts := make([]string, 0, len(segs))

	for _, seg := range segs {
		part := string(seg.Kind) + "(" + seg.Text + ")"

		if len(seg.Overlays) > 0 {
			overlays := make([]string, 0, len(seg.Overlays))
			for _, o := range seg.Overlays {
				overlays = append(overlays, fmt.Sprintf("%s@%d-%d", o.Kind, o.Cols.Start, o.Cols.End))
			}

			part += "[" + strings.Join(overlays, ",") + "]"
		}

		parts = append(parts, part)
	}

	return strings.Join(parts, " ")
}

// newClippedView returns a view of input clipped to cols columns, with an
// error overlay on each of overlays and the annotations given, all on
// line i.
func newClippedView(input string, i, cols int, overlays []position.Span, annotations ...line.Annotation) *line.View {
	view := newView(input)
	view.AddLineOverlay(i, yamltest.ErrorOverlays(overlays...)...)
	view.AnnotateLine(i, annotations...)

	return view.Clip(cols)
}

func TestView_Row(t *testing.T) {
	t.Parallel()

	const letters = "key: abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

	// A document minified onto one long line, as an error excerpt clips.
	zeros := func(n int) string { return strings.Repeat("0", n) }
	long := "key: " + zeros(300)

	below := func(content string, start, end int) line.Annotation {
		return line.Annotation{
			Content:   content,
			Kind:      kind.TextError,
			Placement: line.Below,
			Cols:      position.NewSpan(start, end),
		}
	}

	tcs := map[string]struct {
		input           string
		wantContent     string
		wantSegments    string
		overlays        []position.Span
		annotations     []line.Annotation
		wantOverlays    line.Overlays
		wantAnnotations line.Annotations
		wantWindows     position.Spans
		index           int
		cols            int
	}{
		"line that fits is the line": {
			input:           "key: value",
			cols:            10,
			overlays:        []position.Span{position.NewSpan(5, 10)},
			annotations:     []line.Annotation{below("bad", 5, 10)},
			wantContent:     "key: value",
			wantSegments:    "nameTag(key) punctuationMappingValue(:) text( ) literalString(value)[genericError@5-10]",
			wantOverlays:    yamltest.ErrorOverlays(position.NewSpan(5, 10)),
			wantAnnotations: line.Annotations{below("bad", 5, 10)},
		},
		"view without a clip shows a long line whole": {
			input:        letters,
			cols:         0,
			wantContent:  letters,
			wantSegments: "nameTag(key) punctuationMappingValue(:) text( ) literalString(abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ)",
		},
		"empty line": {
			input: "a: 1\n\nb: 2",
			index: 1,
			cols:  10,
		},
		"line with no mark keeps its first columns": {
			input:        letters,
			cols:         10,
			wantContent:  "key: abcde...",
			wantSegments: "nameTag(key) punctuationMappingValue(:) text( ) literalString(abcde) uiSeparator(...)",
			wantWindows:  position.Spans{position.NewSpan(0, 10)},
		},
		"two marks far apart get a window each": {
			input:    letters,
			cols:     10,
			overlays: []position.Span{position.NewSpan(15, 16), position.NewSpan(45, 46)},
			annotations: []line.Annotation{
				below("first", 15, 16),
				below("second", 45, 46),
			},
			wantContent: "...fghijklmno...JKLMNOPQRS...",
			wantSegments: "uiSeparator(...) literalString(fghij) literalString(k)[genericError@8-9] literalString(lmno) " +
				"uiSeparator(...) literalString(JKLMN) literalString(O)[genericError@21-22] literalString(PQRS) uiSeparator(...)",
			wantOverlays: yamltest.ErrorOverlays(position.NewSpan(8, 9), position.NewSpan(21, 22)),
			wantAnnotations: line.Annotations{
				below("first", 8, 9),
				below("second", 21, 22),
			},
			wantWindows: position.Spans{position.NewSpan(10, 20), position.NewSpan(40, 50)},
		},
		"overlay across two windows keeps its part of each": {
			input:       letters,
			cols:        10,
			overlays:    []position.Span{position.NewSpan(15, 47)},
			annotations: []line.Annotation{below("long", 15, 47), below("", 45, 46)},
			wantContent: "...fghijklmno...JKLMNOPQRS...",
			wantSegments: "uiSeparator(...) literalString(fghij) literalString(klmno)[genericError@8-13] " +
				"uiSeparator(...) literalString(JKLMNOP)[genericError@16-23] literalString(QRS) uiSeparator(...)",
			wantOverlays: yamltest.ErrorOverlays(position.NewSpan(8, 13), position.NewSpan(16, 23)),
			// The run of the row that shows the long annotation holds the
			// ellipsis between its windows.
			wantAnnotations: line.Annotations{below("long", 8, 23), below("", 21, 22)},
			wantWindows:     position.Spans{position.NewSpan(10, 20), position.NewSpan(40, 50)},
		},
		"overlay past the end of the line drops": {
			input:    letters,
			cols:     10,
			overlays: []position.Span{position.NewSpan(2, 3), position.NewSpan(100, 101)},
			// The overlay past the end gets the window at the end, which
			// does not show any of its columns.
			wantContent: "key: abcde...QRSTUVWXYZ",
			wantSegments: "nameTag(ke) nameTag(y)[genericError@2-3] punctuationMappingValue(:) text( ) literalString(abcde) " +
				"uiSeparator(...) literalString(QRSTUVWXYZ)",
			wantOverlays: yamltest.ErrorOverlays(position.NewSpan(2, 3)),
			wantWindows:  position.Spans{position.NewSpan(0, 10), position.NewSpan(47, 57)},
		},
		"annotation at a column the row leaves out starts at its ellipsis": {
			input: letters,
			cols:  10,
			// A hunk header labels the line, not a column, so it does not
			// get a window.
			annotations: []line.Annotation{
				{Content: "@@", Kind: kind.UIHunkHeader, Placement: line.Above, Cols: position.NewSpan(30, 31)},
			},
			wantContent:  "key: abcde...",
			wantSegments: "nameTag(key) punctuationMappingValue(:) text( ) literalString(abcde) uiSeparator(...)",
			wantAnnotations: line.Annotations{
				{Content: "@@", Kind: kind.UIHunkHeader, Placement: line.Above, Cols: position.NewSpan(10, 10)},
			},
			wantWindows: position.Spans{position.NewSpan(0, 10)},
		},
		"long line clipped to 40 columns": {
			input:    long,
			cols:     40,
			overlays: []position.Span{position.NewSpan(100, 101), position.NewSpan(250, 251)},
			annotations: []line.Annotation{
				below("first", 100, 101),
				below("second", 250, 251),
			},
			wantContent: "..." + zeros(40) + "..." + zeros(40) + "...",
			wantSegments: "uiSeparator(...) literalNumberOct(" + zeros(
				20,
			) + ") literalNumberOct(0)[genericError@23-24] " +
				"literalNumberOct(" + zeros(
				19,
			) + ") uiSeparator(...) literalNumberOct(" + zeros(
				20,
			) + ") " +
				"literalNumberOct(0)[genericError@66-67] literalNumberOct(" + zeros(
				19,
			) + ") uiSeparator(...)",
			wantOverlays: yamltest.ErrorOverlays(position.NewSpan(23, 24), position.NewSpan(66, 67)),
			wantAnnotations: line.Annotations{
				below("first", 23, 24),
				below("second", 66, 67),
			},
			wantWindows: position.Spans{position.NewSpan(80, 120), position.NewSpan(230, 270)},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := newClippedView(tc.input, tc.index, tc.cols, tc.overlays, tc.annotations...)
			row := view.Row(tc.index)

			assert.Equal(t, tc.wantContent, row.Content)
			assert.Equal(t, utf8.RuneCountInString(tc.wantContent), row.Width)
			assert.Equal(t, tc.wantSegments, rowSegments(row.Segments))
			assert.Equal(t, tc.wantOverlays, row.Overlays)
			assert.Equal(t, tc.wantAnnotations, row.Annotations)
			assert.Equal(t, tc.wantWindows, row.Windows)
			assert.Equal(t, tc.wantWindows, view.Windows(tc.index))

			// The segments tile the content, each on the columns of its
			// text.
			col := 0

			for _, seg := range row.Segments {
				assert.Equal(
					t,
					position.NewSpan(col, col+utf8.RuneCountInString(seg.Text)),
					seg.Cols,
					"segment %q",
					seg.Text,
				)

				col = seg.Cols.End
			}

			assert.Equal(t, row.Width, col)

			// String renders the row of the line.
			assert.Contains(t, view.String(), fmt.Sprintf("%4d | %s", view.Line(tc.index).Number(), row.Content))

			// The view keeps the decoration of the whole line, and
			// Segments still covers the whole line.
			assert.ElementsMatch(t, yamltest.ErrorOverlays(tc.overlays...), view.Overlays(tc.index))
			assert.ElementsMatch(t, tc.annotations, view.Annotations(tc.index))

			var whole strings.Builder

			for seg := range view.Segments(tc.index) {
				whole.WriteString(seg.Text)
			}

			assert.Equal(t, view.Line(tc.index).Content(), whole.String())
		})
	}
}

func TestView_Row_Owned(t *testing.T) {
	t.Parallel()

	const letters = "key: abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

	annotation := line.Annotation{Content: "bad", Placement: line.Below, Cols: position.NewSpan(15, 16)}

	tcs := map[string]struct {
		cols int
	}{
		"line shown whole": {cols: 0},
		"clipped line":     {cols: 10},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := newClippedView(letters, 0, tc.cols, []position.Span{position.NewSpan(15, 16)}, annotation)
			row := view.Row(0)

			require.NotEmpty(t, row.Overlays)
			require.NotEmpty(t, row.Annotations)
			require.NotEmpty(t, row.Segments)

			row.Overlays[0].Kind = kind.GenericHighlight
			row.Annotations[0].Content = "changed"
			row.Segments[0].Text = "changed"

			assert.Equal(t, yamltest.ErrorOverlays(position.NewSpan(15, 16)), view.Overlays(0))
			assert.Equal(t, line.Annotations{annotation}, view.Annotations(0))
			assert.NotEqual(t, "changed", view.Row(0).Segments[0].Text)
		})
	}
}

func TestView_Row_Panics(t *testing.T) {
	t.Parallel()

	view := newView("a: 1")

	assert.Panics(t, func() { view.Row(1) })
	assert.Panics(t, func() { view.Row(-1) })
}
