package lcs_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/diff/lcs"
)

func TestHirschberg_Diff(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		before []string
		after  []string
		want   []lcs.Op
	}{
		"empty_both": {
			before: []string{},
			after:  []string{},
			want:   nil,
		},
		"empty_before": {
			before: []string{},
			after:  []string{"a", "b"},
			want: []lcs.Op{
				{Kind: lcs.OpInsert, Before: -1, After: 0},
				{Kind: lcs.OpInsert, Before: -1, After: 1},
			},
		},
		"empty_after": {
			before: []string{"a", "b"},
			after:  []string{},
			want: []lcs.Op{
				{Kind: lcs.OpDelete, Before: 0, After: -1},
				{Kind: lcs.OpDelete, Before: 1, After: -1},
			},
		},
		"identical": {
			before: []string{"a", "b", "c"},
			after:  []string{"a", "b", "c"},
			want: []lcs.Op{
				{Kind: lcs.OpEqual, Before: 0, After: 0},
				{Kind: lcs.OpEqual, Before: 1, After: 1},
				{Kind: lcs.OpEqual, Before: 2, After: 2},
			},
		},
		"all_different": {
			before: []string{"a", "b"},
			after:  []string{"c", "d"},
			want: []lcs.Op{
				{Kind: lcs.OpDelete, Before: 0, After: -1},
				{Kind: lcs.OpDelete, Before: 1, After: -1},
				{Kind: lcs.OpInsert, Before: -1, After: 0},
				{Kind: lcs.OpInsert, Before: -1, After: 1},
			},
		},
		"single_insert_at_start": {
			before: []string{"b", "c"},
			after:  []string{"a", "b", "c"},
			want: []lcs.Op{
				{Kind: lcs.OpInsert, Before: -1, After: 0},
				{Kind: lcs.OpEqual, Before: 0, After: 1},
				{Kind: lcs.OpEqual, Before: 1, After: 2},
			},
		},
		"single_insert_at_end": {
			before: []string{"a", "b"},
			after:  []string{"a", "b", "c"},
			want: []lcs.Op{
				{Kind: lcs.OpEqual, Before: 0, After: 0},
				{Kind: lcs.OpEqual, Before: 1, After: 1},
				{Kind: lcs.OpInsert, Before: -1, After: 2},
			},
		},
		"single_delete_at_start": {
			before: []string{"a", "b", "c"},
			after:  []string{"b", "c"},
			want: []lcs.Op{
				{Kind: lcs.OpDelete, Before: 0, After: -1},
				{Kind: lcs.OpEqual, Before: 1, After: 0},
				{Kind: lcs.OpEqual, Before: 2, After: 1},
			},
		},
		"single_delete_at_end": {
			before: []string{"a", "b", "c"},
			after:  []string{"a", "b"},
			want: []lcs.Op{
				{Kind: lcs.OpEqual, Before: 0, After: 0},
				{Kind: lcs.OpEqual, Before: 1, After: 1},
				{Kind: lcs.OpDelete, Before: 2, After: -1},
			},
		},
		"interleaved_changes": {
			before: []string{"a", "b", "c", "d"},
			after:  []string{"a", "x", "c", "y"},
			want: []lcs.Op{
				{Kind: lcs.OpEqual, Before: 0, After: 0},
				{Kind: lcs.OpDelete, Before: 1, After: -1},
				{Kind: lcs.OpInsert, Before: -1, After: 1},
				{Kind: lcs.OpEqual, Before: 2, After: 2},
				{Kind: lcs.OpDelete, Before: 3, After: -1},
				{Kind: lcs.OpInsert, Before: -1, After: 3},
			},
		},
		"replace_in_middle": {
			before: []string{"a", "b", "c"},
			after:  []string{"a", "x", "c"},
			want: []lcs.Op{
				{Kind: lcs.OpEqual, Before: 0, After: 0},
				{Kind: lcs.OpDelete, Before: 1, After: -1},
				{Kind: lcs.OpInsert, Before: -1, After: 1},
				{Kind: lcs.OpEqual, Before: 2, After: 2},
			},
		},
		"single_element_before_match": {
			before: []string{"a"},
			after:  []string{"x", "a", "y"},
			want: []lcs.Op{
				{Kind: lcs.OpInsert, Before: -1, After: 0},
				{Kind: lcs.OpEqual, Before: 0, After: 1},
				{Kind: lcs.OpInsert, Before: -1, After: 2},
			},
		},
		"single_element_before_no_match": {
			before: []string{"a"},
			after:  []string{"x", "y", "z"},
			want: []lcs.Op{
				{Kind: lcs.OpDelete, Before: 0, After: -1},
				{Kind: lcs.OpInsert, Before: -1, After: 0},
				{Kind: lcs.OpInsert, Before: -1, After: 1},
				{Kind: lcs.OpInsert, Before: -1, After: 2},
			},
		},
		"lcs_non_contiguous": {
			before: []string{"a", "x", "b", "y", "c"},
			after:  []string{"a", "b", "c"},
			want: []lcs.Op{
				{Kind: lcs.OpEqual, Before: 0, After: 0},
				{Kind: lcs.OpDelete, Before: 1, After: -1},
				{Kind: lcs.OpEqual, Before: 2, After: 1},
				{Kind: lcs.OpDelete, Before: 3, After: -1},
				{Kind: lcs.OpEqual, Before: 4, After: 2},
			},
		},
		"complex_diff": {
			before: []string{"a", "b", "c", "d", "e"},
			after:  []string{"x", "b", "c", "y", "e"},
			want: []lcs.Op{
				{Kind: lcs.OpDelete, Before: 0, After: -1},
				{Kind: lcs.OpInsert, Before: -1, After: 0},
				{Kind: lcs.OpEqual, Before: 1, After: 1},
				{Kind: lcs.OpEqual, Before: 2, After: 2},
				{Kind: lcs.OpDelete, Before: 3, After: -1},
				{Kind: lcs.OpInsert, Before: -1, After: 3},
				{Kind: lcs.OpEqual, Before: 4, After: 4},
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := lcs.NewHirschberg()

			got := h.Diff(tc.before, tc.after)

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestHirschberg_Reuse(t *testing.T) {
	t.Parallel()

	h := lcs.NewHirschberg()

	// First computation.
	ops1 := h.Diff([]string{"a", "b"}, []string{"a", "c"})
	assert.Equal(t, []lcs.Op{
		{Kind: lcs.OpEqual, Before: 0, After: 0},
		{Kind: lcs.OpDelete, Before: 1, After: -1},
		{Kind: lcs.OpInsert, Before: -1, After: 1},
	}, ops1)

	// Second computation should work correctly with reused instance.
	ops2 := h.Diff([]string{"x", "y", "z"}, []string{"x", "z"})
	assert.Equal(t, []lcs.Op{
		{Kind: lcs.OpEqual, Before: 0, After: 0},
		{Kind: lcs.OpDelete, Before: 1, After: -1},
		{Kind: lcs.OpEqual, Before: 2, After: 1},
	}, ops2)

	// The first result is a copy, so the second call left it intact.
	assert.Equal(t, []lcs.Op{
		{Kind: lcs.OpEqual, Before: 0, After: 0},
		{Kind: lcs.OpDelete, Before: 1, After: -1},
		{Kind: lcs.OpInsert, Before: -1, After: 1},
	}, ops1)
}

func TestHirschberg_BufferGrowth(t *testing.T) {
	t.Parallel()

	// Test that larger inputs work when buffers are initially empty.
	h := lcs.NewHirschberg()

	before := []string{"a", "b", "c", "d", "e"}
	after := []string{"a", "x", "c", "y", "e"}

	got := h.Diff(before, after)

	want := []lcs.Op{
		{Kind: lcs.OpEqual, Before: 0, After: 0},
		{Kind: lcs.OpDelete, Before: 1, After: -1},
		{Kind: lcs.OpInsert, Before: -1, After: 1},
		{Kind: lcs.OpEqual, Before: 2, After: 2},
		{Kind: lcs.OpDelete, Before: 3, After: -1},
		{Kind: lcs.OpInsert, Before: -1, After: 3},
		{Kind: lcs.OpEqual, Before: 4, After: 4},
	}

	assert.Equal(t, want, got)
}

func TestHirschberg_Concurrent(t *testing.T) {
	t.Parallel()

	h := lcs.NewHirschberg()

	before := []string{"a", "b", "c", "d", "e"}
	after := []string{"a", "x", "c", "y", "e"}
	want := h.Diff(before, after)

	var wg sync.WaitGroup

	for range 16 {
		wg.Go(func() {
			for range 50 {
				assert.Equal(t, want, h.Diff(before, after))
				assert.Equal(t, []lcs.Op{
					{Kind: lcs.OpEqual, Before: 0, After: 0},
					{Kind: lcs.OpInsert, Before: -1, After: 1},
				}, h.Diff([]string{"a"}, []string{"a", "b"}))
			}
		})
	}

	wg.Wait()
}
