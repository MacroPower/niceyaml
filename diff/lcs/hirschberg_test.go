package lcs_test

import (
	"math/rand/v2"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
		"repeated_block_delete": {
			before: []string{"a", "b", "b", "a", "a", "b", "c"},
			after:  []string{"a", "b", "b", "c"},
			want: []lcs.Op{
				{Kind: lcs.OpEqual, Before: 0, After: 0},
				{Kind: lcs.OpEqual, Before: 1, After: 1},
				{Kind: lcs.OpEqual, Before: 2, After: 2},
				{Kind: lcs.OpDelete, Before: 3, After: -1},
				{Kind: lcs.OpDelete, Before: 4, After: -1},
				{Kind: lcs.OpDelete, Before: 5, After: -1},
				{Kind: lcs.OpEqual, Before: 6, After: 3},
			},
		},
		"repeated_block_insert": {
			before: []string{"a", "b", "b", "c"},
			after:  []string{"a", "b", "b", "a", "a", "b", "c"},
			want: []lcs.Op{
				{Kind: lcs.OpEqual, Before: 0, After: 0},
				{Kind: lcs.OpEqual, Before: 1, After: 1},
				{Kind: lcs.OpEqual, Before: 2, After: 2},
				{Kind: lcs.OpInsert, Before: -1, After: 3},
				{Kind: lcs.OpInsert, Before: -1, After: 4},
				{Kind: lcs.OpInsert, Before: -1, After: 5},
				{Kind: lcs.OpEqual, Before: 3, After: 6},
			},
		},
		"trailing_repeated_block_delete": {
			before: []string{"k", "a", "b", "a", "b"},
			after:  []string{"k", "a", "b"},
			want: []lcs.Op{
				{Kind: lcs.OpEqual, Before: 0, After: 0},
				{Kind: lcs.OpEqual, Before: 1, After: 1},
				{Kind: lcs.OpEqual, Before: 2, After: 2},
				{Kind: lcs.OpDelete, Before: 3, After: -1},
				{Kind: lcs.OpDelete, Before: 4, After: -1},
			},
		},
		"trailing_repeated_block_insert": {
			before: []string{"k", "a", "b"},
			after:  []string{"k", "a", "b", "a", "b"},
			want: []lcs.Op{
				{Kind: lcs.OpEqual, Before: 0, After: 0},
				{Kind: lcs.OpEqual, Before: 1, After: 1},
				{Kind: lcs.OpEqual, Before: 2, After: 2},
				{Kind: lcs.OpInsert, Before: -1, After: 3},
				{Kind: lcs.OpInsert, Before: -1, After: 4},
			},
		},
		"repeated_line_around_deletion": {
			before: []string{"a", "x", "a"},
			after:  []string{"a"},
			want: []lcs.Op{
				{Kind: lcs.OpEqual, Before: 0, After: 0},
				{Kind: lcs.OpDelete, Before: 1, After: -1},
				{Kind: lcs.OpDelete, Before: 2, After: -1},
			},
		},
		"repeated_pair_deletion": {
			before: []string{"a", "b", "a", "b"},
			after:  []string{"a", "b"},
			want: []lcs.Op{
				{Kind: lcs.OpEqual, Before: 0, After: 0},
				{Kind: lcs.OpEqual, Before: 1, After: 1},
				{Kind: lcs.OpDelete, Before: 2, After: -1},
				{Kind: lcs.OpDelete, Before: 3, After: -1},
			},
		},
		"repeated_line_deletion": {
			before: []string{"a", "a"},
			after:  []string{"a"},
			want: []lcs.Op{
				{Kind: lcs.OpEqual, Before: 0, After: 0},
				{Kind: lcs.OpDelete, Before: 1, After: -1},
			},
		},
		"shared_start_and_end": {
			before: []string{"a", "b", "c", "d", "e", "f"},
			after:  []string{"a", "b", "x", "y", "e", "f"},
			want: []lcs.Op{
				{Kind: lcs.OpEqual, Before: 0, After: 0},
				{Kind: lcs.OpEqual, Before: 1, After: 1},
				{Kind: lcs.OpDelete, Before: 2, After: -1},
				{Kind: lcs.OpDelete, Before: 3, After: -1},
				{Kind: lcs.OpInsert, Before: -1, After: 2},
				{Kind: lcs.OpInsert, Before: -1, After: 3},
				{Kind: lcs.OpEqual, Before: 4, After: 4},
				{Kind: lcs.OpEqual, Before: 5, After: 5},
			},
		},
		"shared_start_and_end_of_unequal_lengths": {
			before: []string{"a", "b", "c", "e", "f"},
			after:  []string{"a", "x", "y", "z", "e", "f"},
			want: []lcs.Op{
				{Kind: lcs.OpEqual, Before: 0, After: 0},
				{Kind: lcs.OpDelete, Before: 1, After: -1},
				{Kind: lcs.OpDelete, Before: 2, After: -1},
				{Kind: lcs.OpInsert, Before: -1, After: 1},
				{Kind: lcs.OpInsert, Before: -1, After: 2},
				{Kind: lcs.OpInsert, Before: -1, After: 3},
				{Kind: lcs.OpEqual, Before: 3, After: 4},
				{Kind: lcs.OpEqual, Before: 4, After: 5},
			},
		},
		"replace_before_repeated_line": {
			before: []string{"x", "x"},
			after:  []string{"y", "x"},
			want: []lcs.Op{
				{Kind: lcs.OpDelete, Before: 0, After: -1},
				{Kind: lcs.OpInsert, Before: -1, After: 0},
				{Kind: lcs.OpEqual, Before: 1, After: 1},
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

	// Second computation, with the instance reused.
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

func TestHirschberg_DiffIsMinimal(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		alphabet []string
		maxLen   int
	}{
		"two_symbols": {
			alphabet: []string{"a", "b"},
			maxLen:   12,
		},
		"four_symbols": {
			alphabet: []string{"a", "b", "c", "d"},
			maxLen:   16,
		},
		"eight_symbols": {
			alphabet: []string{"a", "b", "c", "d", "e", "f", "g", "h"},
			maxLen:   24,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rng := rand.New(rand.NewPCG(1, uint64(len(tc.alphabet))))
			h := lcs.NewHirschberg()

			randomLines := func() []string {
				lines := make([]string, rng.IntN(tc.maxLen+1))
				for i := range lines {
					lines[i] = tc.alphabet[rng.IntN(len(tc.alphabet))]
				}

				return lines
			}

			for range 2000 {
				before, after := randomLines(), randomLines()
				got := h.Diff(before, after)

				requireValidOps(t, before, after, got)

				equal := 0

				for _, op := range got {
					if op.Kind == lcs.OpEqual {
						equal++
					}
				}

				require.Equal(t, naiveLCSLen(before, after), equal,
					"not minimal: before=%q after=%q ops=%v", before, after, got)
			}
		})
	}
}

// requireValidOps checks that ops transform before into after. Each index
// of either input appears once and in order, each [lcs.OpEqual] pairs lines
// with the same content, and within each run of changes every
// [lcs.OpDelete] comes before every [lcs.OpInsert].
func requireValidOps(t *testing.T, before, after []string, ops []lcs.Op) {
	t.Helper()

	var nextBefore, nextAfter int

	inserting := false

	for i, op := range ops {
		switch op.Kind {
		case lcs.OpEqual:
			require.Equal(t, nextBefore, op.Before, "op %d: before=%q after=%q ops=%v", i, before, after, ops)
			require.Equal(t, nextAfter, op.After, "op %d: before=%q after=%q ops=%v", i, before, after, ops)
			require.Equal(
				t,
				before[op.Before],
				after[op.After],
				"op %d: before=%q after=%q ops=%v",
				i,
				before,
				after,
				ops,
			)

			nextBefore++
			nextAfter++
			inserting = false

		case lcs.OpDelete:
			require.Equal(t, nextBefore, op.Before, "op %d: before=%q after=%q ops=%v", i, before, after, ops)
			require.Equal(t, -1, op.After, "op %d: before=%q after=%q ops=%v", i, before, after, ops)
			require.False(
				t,
				inserting,
				"op %d deletes after an insert: before=%q after=%q ops=%v",
				i,
				before,
				after,
				ops,
			)

			nextBefore++

		case lcs.OpInsert:
			require.Equal(t, -1, op.Before, "op %d: before=%q after=%q ops=%v", i, before, after, ops)
			require.Equal(t, nextAfter, op.After, "op %d: before=%q after=%q ops=%v", i, before, after, ops)

			nextAfter++
			inserting = true

		default:
			require.Failf(t, "unknown kind", "op %d: %v", i, op)
		}
	}

	require.Equal(t, len(before), nextBefore, "before=%q after=%q ops=%v", before, after, ops)
	require.Equal(t, len(after), nextAfter, "before=%q after=%q ops=%v", before, after, ops)
}

// naiveLCSLen returns the length of the longest common subsequence of
// before and after from the full dynamic programming table.
func naiveLCSLen(before, after []string) int {
	table := make([][]int, len(before)+1)
	for i := range table {
		table[i] = make([]int, len(after)+1)
	}

	for i := range before {
		for j := range after {
			if before[i] == after[j] {
				table[i+1][j+1] = table[i][j] + 1
			} else {
				table[i+1][j+1] = max(table[i][j+1], table[i+1][j])
			}
		}
	}

	return table[len(before)][len(after)]
}
