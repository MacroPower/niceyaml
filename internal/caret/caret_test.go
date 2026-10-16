package caret_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/internal/caret"
	"go.jacobcolvin.com/niceyaml/position"
)

// label returns a label that marks the columns from start up to end with
// text.
func label(start, end int, text string) caret.Label {
	return caret.Label{Text: text, Cols: position.NewSpan(start, end)}
}

func TestRows(t *testing.T) {
	t.Parallel()

	// The line "key: value other" wraps after "value", and the wrap drops
	// the space at column 10.
	const wrappedContent = "key: value other"

	wrapped := []position.Span{position.NewSpan(0, 10), position.NewSpan(11, 16)}

	tcs := map[string]struct {
		content string
		labels  []caret.Label
		wrapped []position.Span
		want    []caret.Row
	}{
		"no labels": {
			content: "key: value",
		},
		"text follows the carets of its run": {
			content: "key: value",
			labels:  []caret.Label{label(5, 10, "bad")},
			want:    []caret.Row{{Col: 5, Marker: "^^^^^ ", Text: "bad"}},
		},
		"a run without text gets its carets alone": {
			content: "key: value",
			labels:  []caret.Label{label(0, 3, "")},
			want:    []caret.Row{{Col: 0, Marker: "^^^"}},
		},
		"labels of one run share its row": {
			content: "key: value",
			labels:  []caret.Label{label(5, 10, "a"), label(5, 10, ""), label(5, 10, "b")},
			want:    []caret.Row{{Col: 5, Marker: "^^^^^ ", Text: "a; b"}},
		},
		"runs come in the order they start, shorter first": {
			content: "key: value",
			labels:  []caret.Label{label(5, 10, "long"), label(5, 7, "short"), label(0, 3, "key")},
			want: []caret.Row{
				{Col: 0, Marker: "^^^ ", Text: "key"},
				{Col: 5, Marker: "^^ ", Text: "short"},
				{Col: 5, Marker: "^^^^^ ", Text: "long"},
			},
		},
		"spans that mark the same columns share a row": {
			content: "key: value",
			labels:  []caret.Label{label(5, 100, "a"), label(5, 50, "b")},
			want:    []caret.Row{{Col: 5, Marker: "^^^^^ ", Text: "a; b"}},
		},
		"an empty span shares the row of the column it marks": {
			content: "key: value",
			labels:  []caret.Label{label(5, 5, "a"), label(5, 6, "b")},
			want:    []caret.Row{{Col: 5, Marker: "^ ", Text: "a; b"}},
		},
		"a start before the first column sorts as zero": {
			content: "key: value",
			labels:  []caret.Label{label(-2, 3, "long"), label(0, 2, "short")},
			want: []caret.Row{
				{Col: 0, Marker: "^^ ", Text: "short"},
				{Col: 0, Marker: "^^^ ", Text: "long"},
			},
		},
		"an empty span marks its column": {
			content: "key: value",
			labels:  []caret.Label{label(10, 10, "")},
			want:    []caret.Row{{Col: 10, Marker: "^"}},
		},
		"a span past the content marks its start": {
			content: "key: value",
			labels:  []caret.Label{label(12, 14, "far")},
			want:    []caret.Row{{Col: 12, Marker: "^ ", Text: "far"}},
		},
		"a start before the first column counts as zero": {
			content: "key: value",
			labels:  []caret.Label{label(-2, 3, "")},
			want:    []caret.Row{{Col: 0, Marker: "^^^"}},
		},
		"a wide rune gets two carets": {
			content: "名前: v",
			labels:  []caret.Label{label(1, 2, "")},
			want:    []caret.Row{{Col: 1, Marker: "^^"}},
		},
		"control characters in text render as pictures": {
			content: "key: value",
			labels:  []caret.Label{label(0, 3, "a\x1bb")},
			want:    []caret.Row{{Col: 0, Marker: "^^^ ", Text: "a\u241bb"}},
		},
		"a run across a wrap gets a row beside each wrapped row": {
			content: wrappedContent,
			wrapped: wrapped,
			labels:  []caret.Label{label(5, 13, "x")},
			want: []caret.Row{
				{Col: 5, Marker: "^^^^^ ", Text: "x"},
				{Col: 11, Marker: "^^"},
			},
		},
		"text sits beside the wrapped row that holds the start": {
			content: wrappedContent,
			wrapped: wrapped,
			labels:  []caret.Label{label(11, 16, "z")},
			want:    []caret.Row{{Col: 11, Marker: "^^^^^ ", Text: "z"}},
		},
		"a start on a dropped space gets a single caret": {
			content: wrappedContent,
			wrapped: wrapped,
			labels:  []caret.Label{label(10, 10, "y")},
			want:    []caret.Row{{Col: 10, Marker: "^ ", Text: "y"}},
		},
		"a run from a dropped space puts its text on a row of its own": {
			content: wrappedContent,
			wrapped: wrapped,
			labels:  []caret.Label{label(10, 13, "w")},
			want: []caret.Row{
				{Col: 10, Marker: "^ ", Text: "w"},
				{Col: 11, Marker: "^^"},
			},
		},
		"a run without text on a dropped space gets a single caret": {
			content: wrappedContent,
			wrapped: wrapped,
			labels:  []caret.Label{label(10, 10, "")},
			want:    []caret.Row{{Col: 10, Marker: "^"}},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, caret.Rows(tc.content, tc.labels, tc.wrapped))
		})
	}
}
