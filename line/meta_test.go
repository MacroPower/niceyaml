package line_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"jacobcolvin.com/niceyaml/line"
)

func TestAnnotation_String(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		ann  line.Annotation
		want string
	}{
		"empty content": {
			ann:  line.Annotation{Content: "", Col: 0},
			want: "",
		},
		"content at col 0": {
			ann:  line.Annotation{Content: "error here", Col: 0},
			want: "error here",
		},
		"content with padding": {
			ann:  line.Annotation{Content: "note", Col: 5},
			want: "     note",
		},
		"large column padding": {
			ann:  line.Annotation{Content: "x", Col: 10},
			want: "          x",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.ann.String()
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestAnnotations_Col(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		anns line.Annotations
		want int
	}{
		"empty annotations": {
			anns: nil,
			want: 0,
		},
		"single annotation": {
			anns: line.Annotations{{Content: "x", Col: 5}},
			want: 5,
		},
		"multiple annotations returns minimum": {
			anns: line.Annotations{
				{Content: "first", Col: 10},
				{Content: "second", Col: 3},
				{Content: "third", Col: 7},
			},
			want: 3,
		},
		"all same column": {
			anns: line.Annotations{
				{Content: "a", Col: 4},
				{Content: "b", Col: 4},
			},
			want: 4,
		},
		"zero is minimum": {
			anns: line.Annotations{
				{Content: "a", Col: 5},
				{Content: "b", Col: 0},
				{Content: "c", Col: 3},
			},
			want: 0,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.anns.Col()
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestAnnotations_Contents(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		anns line.Annotations
		want []string
	}{
		"empty annotations": {
			anns: nil,
			want: []string{},
		},
		"single annotation": {
			anns: line.Annotations{{Content: "message"}},
			want: []string{"message"},
		},
		"multiple annotations": {
			anns: line.Annotations{
				{Content: "first"},
				{Content: "second"},
				{Content: "third"},
			},
			want: []string{"first", "second", "third"},
		},
		"with empty content": {
			anns: line.Annotations{
				{Content: "a"},
				{Content: ""},
				{Content: "b"},
			},
			want: []string{"a", "", "b"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.anns.Contents()
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestAnnotations_FilterPosition(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		anns     line.Annotations
		position line.RelativePosition
		want     line.Annotations
	}{
		"empty annotations": {
			anns:     nil,
			position: line.Above,
			want:     nil,
		},
		"filter above from mixed": {
			anns: line.Annotations{
				{Content: "above1", Position: line.Above},
				{Content: "below1", Position: line.Below},
				{Content: "above2", Position: line.Above},
			},
			position: line.Above,
			want: line.Annotations{
				{Content: "above1", Position: line.Above},
				{Content: "above2", Position: line.Above},
			},
		},
		"filter below from mixed": {
			anns: line.Annotations{
				{Content: "above1", Position: line.Above},
				{Content: "below1", Position: line.Below},
				{Content: "below2", Position: line.Below},
			},
			position: line.Below,
			want: line.Annotations{
				{Content: "below1", Position: line.Below},
				{Content: "below2", Position: line.Below},
			},
		},
		"no matches": {
			anns: line.Annotations{
				{Content: "below", Position: line.Below},
			},
			position: line.Above,
			want:     nil,
		},
		"all match": {
			anns: line.Annotations{
				{Content: "a", Position: line.Above},
				{Content: "b", Position: line.Above},
			},
			position: line.Above,
			want: line.Annotations{
				{Content: "a", Position: line.Above},
				{Content: "b", Position: line.Above},
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.anns.FilterPosition(tc.position)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestAnnotations_String(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		anns line.Annotations
		want string
	}{
		"empty annotations": {
			anns: nil,
			want: "",
		},
		"single annotation": {
			anns: line.Annotations{{Content: "message", Col: 3}},
			want: "   message",
		},
		"multiple annotations joined": {
			anns: line.Annotations{
				{Content: "first", Col: 5},
				{Content: "second", Col: 10},
			},
			want: "     first; second",
		},
		"uses minimum column": {
			anns: line.Annotations{
				{Content: "a", Col: 8},
				{Content: "b", Col: 2},
				{Content: "c", Col: 5},
			},
			want: "  a; b; c",
		},
		"zero column no padding": {
			anns: line.Annotations{
				{Content: "x", Col: 0},
				{Content: "y", Col: 5},
			},
			want: "x; y",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.anns.String()
			assert.Equal(t, tc.want, got)
		})
	}
}
