package line_test

import (
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

func TestPlacement_String(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		want      string
		placement line.Placement
	}{
		"above":        {placement: line.Above, want: "above"},
		"below":        {placement: line.Below, want: "below"},
		"out of range": {placement: line.Placement(2), want: "Placement(2)"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.placement.String())
		})
	}
}

func TestFlag_String(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		want string
		flag line.Flag
	}{
		"default":      {flag: line.FlagDefault, want: "default"},
		"inserted":     {flag: line.FlagInserted, want: "inserted"},
		"deleted":      {flag: line.FlagDeleted, want: "deleted"},
		"out of range": {flag: line.Flag(-1), want: "Flag(-1)"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.flag.String())
		})
	}
}

func TestAnnotation_RenderKind(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		kind kind.Kind
		want kind.Kind
	}{
		"zero kind renders as annotation": {kind: "", want: kind.UIAnnotation},
		"annotation kind":                 {kind: kind.UIAnnotation, want: kind.UIAnnotation},
		"own kind":                        {kind: kind.TextError, want: kind.TextError},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ann := line.Annotation{Content: "note", Kind: tc.kind}
			assert.Equal(t, tc.want, ann.RenderKind())
		})
	}
}

func TestAnnotations_Start(t *testing.T) {
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
			anns: line.Annotations{{Content: "x", Cols: position.NewSpan(5, 5)}},
			want: 5,
		},
		"multiple annotations returns minimum": {
			anns: line.Annotations{
				{Content: "first", Cols: position.NewSpan(10, 10)},
				{Content: "second", Cols: position.NewSpan(3, 3)},
				{Content: "third", Cols: position.NewSpan(7, 7)},
			},
			want: 3,
		},
		"all same column": {
			anns: line.Annotations{
				{Content: "a", Cols: position.NewSpan(4, 4)},
				{Content: "b", Cols: position.NewSpan(4, 4)},
			},
			want: 4,
		},
		"zero is minimum": {
			anns: line.Annotations{
				{Content: "a", Cols: position.NewSpan(5, 5)},
				{Content: "b", Cols: position.NewSpan(0, 0)},
				{Content: "c", Cols: position.NewSpan(3, 3)},
			},
			want: 0,
		},
		"ends do not count": {
			anns: line.Annotations{
				{Content: "a", Cols: position.NewSpan(4, 9)},
				{Content: "b", Cols: position.NewSpan(2, 3)},
			},
			want: 2,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.anns.Start())
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
			anns: line.Annotations{{Content: "first"}, {Content: "second"}, {Content: "third"}},
			want: []string{"first", "second", "third"},
		},
		"with empty content": {
			anns: line.Annotations{{Content: "a"}, {Content: ""}, {Content: "b"}},
			want: []string{"a", "", "b"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.anns.Contents())
		})
	}
}

func TestAnnotations_WithContent(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		anns line.Annotations
		want line.Annotations
	}{
		"empty annotations": {
			anns: nil,
			want: nil,
		},
		"all have content": {
			anns: line.Annotations{
				{Content: "a", Cols: position.NewSpan(1, 1)},
				{Content: "b", Cols: position.NewSpan(2, 2)},
			},
			want: line.Annotations{
				{Content: "a", Cols: position.NewSpan(1, 1)},
				{Content: "b", Cols: position.NewSpan(2, 2)},
			},
		},
		"sorts by column and keeps the order at one column": {
			anns: line.Annotations{
				{Content: "c", Cols: position.NewSpan(4, 4)},
				{Content: "a", Cols: position.NewSpan(1, 1)},
				{Content: "d", Cols: position.NewSpan(4, 4)},
			},
			want: line.Annotations{
				{Content: "a", Cols: position.NewSpan(1, 1)},
				{Content: "c", Cols: position.NewSpan(4, 4)},
				{Content: "d", Cols: position.NewSpan(4, 4)},
			},
		},
		"drops the empty content": {
			anns: line.Annotations{
				{Content: "", Cols: position.NewSpan(0, 0)},
				{Content: "b", Cols: position.NewSpan(2, 2)},
			},
			want: line.Annotations{{Content: "b", Cols: position.NewSpan(2, 2)}},
		},
		"no content at all": {
			anns: line.Annotations{
				{Content: "", Cols: position.NewSpan(0, 0)},
				{Content: "", Cols: position.NewSpan(2, 2)},
			},
			want: nil,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.anns.WithContent()
			assert.Equal(t, tc.want, got)

			// The result is a new slice, so editing it in place leaves
			// the receiver alone.
			before := slices.Clone(tc.anns)

			for i := range got {
				got[i].Content = "edited"
			}

			assert.Equal(t, before, tc.anns)
		})
	}
}

func TestAnnotations_ByKind(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		anns line.Annotations
		want []line.Annotations
	}{
		"empty annotations": {
			anns: nil,
			want: nil,
		},
		"one kind stays one group": {
			anns: line.Annotations{{Content: "a"}, {Content: "b"}},
			want: []line.Annotations{{{Content: "a"}, {Content: "b"}}},
		},
		"groups in order of first appearance": {
			anns: line.Annotations{
				{Content: "a", Kind: kind.GenericError},
				{Content: "b"},
				{Content: "c", Kind: kind.GenericError},
				{Content: "d", Kind: kind.Comment},
			},
			want: []line.Annotations{
				{{Content: "a", Kind: kind.GenericError}, {Content: "c", Kind: kind.GenericError}},
				{{Content: "b"}},
				{{Content: "d", Kind: kind.Comment}},
			},
		},
		"zero kind groups with UIAnnotation": {
			anns: line.Annotations{
				{Content: "a"},
				{Content: "b", Kind: kind.TextError},
				{Content: "c", Kind: kind.UIAnnotation},
			},
			want: []line.Annotations{
				{{Content: "a"}, {Content: "c", Kind: kind.UIAnnotation}},
				{{Content: "b", Kind: kind.TextError}},
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.anns.ByKind())
		})
	}
}

func TestAnnotations_Filter(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		anns      line.Annotations
		placement line.Placement
		want      line.Annotations
	}{
		"empty annotations": {
			anns:      nil,
			placement: line.Above,
			want:      nil,
		},
		"filter above from mixed": {
			anns: line.Annotations{
				{Content: "above1", Placement: line.Above},
				{Content: "below1", Placement: line.Below},
				{Content: "above2", Placement: line.Above},
			},
			placement: line.Above,
			want: line.Annotations{
				{Content: "above1", Placement: line.Above},
				{Content: "above2", Placement: line.Above},
			},
		},
		"filter below from mixed": {
			anns: line.Annotations{
				{Content: "above1", Placement: line.Above},
				{Content: "below1", Placement: line.Below},
				{Content: "below2", Placement: line.Below},
			},
			placement: line.Below,
			want: line.Annotations{
				{Content: "below1", Placement: line.Below},
				{Content: "below2", Placement: line.Below},
			},
		},
		"no matches": {
			anns:      line.Annotations{{Content: "below", Placement: line.Below}},
			placement: line.Above,
			want:      nil,
		},
		"all match": {
			anns:      line.Annotations{{Content: "a", Placement: line.Above}, {Content: "b", Placement: line.Above}},
			placement: line.Above,
			want:      line.Annotations{{Content: "a", Placement: line.Above}, {Content: "b", Placement: line.Above}},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.anns.Filter(tc.placement))
		})
	}
}

// TestAnnotations_String also checks Annotation.String, which renders an
// annotation the same as a collection that holds only that annotation.
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
			anns: line.Annotations{{Content: "message", Cols: position.NewSpan(3, 3)}},
			want: "   message",
		},
		"single annotation at column zero": {
			anns: line.Annotations{{Content: "error here", Cols: position.NewSpan(0, 0)}},
			want: "error here",
		},
		"single annotation without content": {
			anns: line.Annotations{{Content: "", Cols: position.NewSpan(0, 0)}},
			want: "",
		},
		"multiple annotations joined": {
			anns: line.Annotations{
				{Content: "first", Cols: position.NewSpan(5, 5)},
				{Content: "second", Cols: position.NewSpan(10, 10)},
			},
			want: "     first; second",
		},
		"joined in column order": {
			anns: line.Annotations{
				{Content: "second", Cols: position.NewSpan(10, 10)},
				{Content: "first", Cols: position.NewSpan(5, 5)},
				{Content: "third", Cols: position.NewSpan(10, 10)},
			},
			want: "     first; second; third",
		},
		"annotations without content add nothing": {
			anns: line.Annotations{
				{Content: "", Cols: position.NewSpan(5, 5)},
				{Content: "second", Cols: position.NewSpan(10, 10)},
			},
			want: "          second",
		},
		"empty content at column zero does not pull the column left": {
			anns: line.Annotations{
				{Content: "", Cols: position.NewSpan(0, 0)},
				{Content: "boom", Cols: position.NewSpan(5, 5)},
			},
			want: "     boom",
		},
		"no content at all is empty": {
			anns: line.Annotations{
				{Content: "", Cols: position.NewSpan(5, 5)},
				{Content: "", Cols: position.NewSpan(10, 10)},
			},
			want: "",
		},
		"uses minimum column": {
			anns: line.Annotations{
				{Content: "a", Cols: position.NewSpan(8, 8)},
				{Content: "b", Cols: position.NewSpan(2, 2)},
				{Content: "c", Cols: position.NewSpan(5, 5)},
			},
			want: "  b; c; a",
		},
		"zero column no padding": {
			anns: line.Annotations{
				{Content: "x", Cols: position.NewSpan(0, 0)},
				{Content: "y", Cols: position.NewSpan(5, 5)},
			},
			want: "x; y",
		},
		"max column pads to the bound": {
			anns: line.Annotations{{Content: "x", Cols: position.NewSpan(math.MaxInt, math.MaxInt)}},
			want: strings.Repeat(" ", 1024) + "x",
		},
		"column past the bound pads to the bound": {
			anns: line.Annotations{{Content: "x", Cols: position.NewSpan(1<<32, 1<<32)}},
			want: strings.Repeat(" ", 1024) + "x",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.anns.String())

			if len(tc.anns) == 1 {
				assert.Equal(t, tc.want, tc.anns[0].String())
			}
		})
	}
}
