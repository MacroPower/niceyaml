package diff_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"jacobcolvin.com/niceyaml/diff"
)

func TestHirschberg_Diff(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		before []string
		after  []string
		want   []diff.Op
	}{
		"empty_both": {
			before: []string{},
			after:  []string{},
			want:   nil,
		},
		"empty_before": {
			before: []string{},
			after:  []string{"a", "b"},
			want: []diff.Op{
				{Kind: diff.OpInsert, Index: 0},
				{Kind: diff.OpInsert, Index: 1},
			},
		},
		"empty_after": {
			before: []string{"a", "b"},
			after:  []string{},
			want: []diff.Op{
				{Kind: diff.OpDelete, Index: 0},
				{Kind: diff.OpDelete, Index: 1},
			},
		},
		"identical": {
			before: []string{"a", "b", "c"},
			after:  []string{"a", "b", "c"},
			want: []diff.Op{
				{Kind: diff.OpEqual, Index: 0},
				{Kind: diff.OpEqual, Index: 1},
				{Kind: diff.OpEqual, Index: 2},
			},
		},
		"all_different": {
			before: []string{"a", "b"},
			after:  []string{"c", "d"},
			want: []diff.Op{
				{Kind: diff.OpDelete, Index: 0},
				{Kind: diff.OpDelete, Index: 1},
				{Kind: diff.OpInsert, Index: 0},
				{Kind: diff.OpInsert, Index: 1},
			},
		},
		"single_insert_at_start": {
			before: []string{"b", "c"},
			after:  []string{"a", "b", "c"},
			want: []diff.Op{
				{Kind: diff.OpInsert, Index: 0},
				{Kind: diff.OpEqual, Index: 1},
				{Kind: diff.OpEqual, Index: 2},
			},
		},
		"single_insert_at_end": {
			before: []string{"a", "b"},
			after:  []string{"a", "b", "c"},
			want: []diff.Op{
				{Kind: diff.OpEqual, Index: 0},
				{Kind: diff.OpEqual, Index: 1},
				{Kind: diff.OpInsert, Index: 2},
			},
		},
		"single_delete_at_start": {
			before: []string{"a", "b", "c"},
			after:  []string{"b", "c"},
			want: []diff.Op{
				{Kind: diff.OpDelete, Index: 0},
				{Kind: diff.OpEqual, Index: 0},
				{Kind: diff.OpEqual, Index: 1},
			},
		},
		"single_delete_at_end": {
			before: []string{"a", "b", "c"},
			after:  []string{"a", "b"},
			want: []diff.Op{
				{Kind: diff.OpEqual, Index: 0},
				{Kind: diff.OpEqual, Index: 1},
				{Kind: diff.OpDelete, Index: 2},
			},
		},
		"interleaved_changes": {
			before: []string{"a", "b", "c", "d"},
			after:  []string{"a", "x", "c", "y"},
			want: []diff.Op{
				{Kind: diff.OpEqual, Index: 0},
				{Kind: diff.OpDelete, Index: 1},
				{Kind: diff.OpInsert, Index: 1},
				{Kind: diff.OpEqual, Index: 2},
				{Kind: diff.OpDelete, Index: 3},
				{Kind: diff.OpInsert, Index: 3},
			},
		},
		"replace_in_middle": {
			before: []string{"a", "b", "c"},
			after:  []string{"a", "x", "c"},
			want: []diff.Op{
				{Kind: diff.OpEqual, Index: 0},
				{Kind: diff.OpDelete, Index: 1},
				{Kind: diff.OpInsert, Index: 1},
				{Kind: diff.OpEqual, Index: 2},
			},
		},
		"single_element_before_match": {
			before: []string{"a"},
			after:  []string{"x", "a", "y"},
			want: []diff.Op{
				{Kind: diff.OpInsert, Index: 0},
				{Kind: diff.OpEqual, Index: 1},
				{Kind: diff.OpInsert, Index: 2},
			},
		},
		"single_element_before_no_match": {
			before: []string{"a"},
			after:  []string{"x", "y", "z"},
			want: []diff.Op{
				{Kind: diff.OpDelete, Index: 0},
				{Kind: diff.OpInsert, Index: 0},
				{Kind: diff.OpInsert, Index: 1},
				{Kind: diff.OpInsert, Index: 2},
			},
		},
		"lcs_non_contiguous": {
			before: []string{"a", "x", "b", "y", "c"},
			after:  []string{"a", "b", "c"},
			want: []diff.Op{
				{Kind: diff.OpEqual, Index: 0},
				{Kind: diff.OpDelete, Index: 1},
				{Kind: diff.OpEqual, Index: 1},
				{Kind: diff.OpDelete, Index: 3},
				{Kind: diff.OpEqual, Index: 2},
			},
		},
		"complex_diff": {
			before: []string{"a", "b", "c", "d", "e"},
			after:  []string{"x", "b", "c", "y", "e"},
			want: []diff.Op{
				{Kind: diff.OpDelete, Index: 0},
				{Kind: diff.OpInsert, Index: 0},
				{Kind: diff.OpEqual, Index: 1},
				{Kind: diff.OpEqual, Index: 2},
				{Kind: diff.OpDelete, Index: 3},
				{Kind: diff.OpInsert, Index: 3},
				{Kind: diff.OpEqual, Index: 4},
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := diff.NewHirschberg()
			h.Init(len(tc.before), len(tc.after))

			got := h.Diff(tc.before, tc.after)

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestHirschberg_Reuse(t *testing.T) {
	t.Parallel()

	h := diff.NewHirschberg()

	// First computation.
	ops1 := h.Diff([]string{"a", "b"}, []string{"a", "c"})
	assert.Equal(t, []diff.Op{
		{Kind: diff.OpEqual, Index: 0},
		{Kind: diff.OpDelete, Index: 1},
		{Kind: diff.OpInsert, Index: 1},
	}, ops1)

	// Second computation should work correctly with reused instance.
	ops2 := h.Diff([]string{"x", "y", "z"}, []string{"x", "z"})
	assert.Equal(t, []diff.Op{
		{Kind: diff.OpEqual, Index: 0},
		{Kind: diff.OpDelete, Index: 1},
		{Kind: diff.OpEqual, Index: 1},
	}, ops2)
}

func TestHirschberg_BufferGrowth(t *testing.T) {
	t.Parallel()

	// Test that larger inputs work when buffers are initially empty.
	h := diff.NewHirschberg()

	before := []string{"a", "b", "c", "d", "e"}
	after := []string{"a", "x", "c", "y", "e"}

	got := h.Diff(before, after)

	want := []diff.Op{
		{Kind: diff.OpEqual, Index: 0},
		{Kind: diff.OpDelete, Index: 1},
		{Kind: diff.OpInsert, Index: 1},
		{Kind: diff.OpEqual, Index: 2},
		{Kind: diff.OpDelete, Index: 3},
		{Kind: diff.OpInsert, Index: 3},
		{Kind: diff.OpEqual, Index: 4},
	}

	assert.Equal(t, want, got)
}
