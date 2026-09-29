package lcs_test

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

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
		"unique_lines_between_shared_lines": {
			before: []string{"x", "a", "y", "b", "z"},
			after:  []string{"p", "a", "q", "b", "r"},
			want: []lcs.Op{
				{Kind: lcs.OpDelete, Before: 0, After: -1},
				{Kind: lcs.OpInsert, Before: -1, After: 0},
				{Kind: lcs.OpEqual, Before: 1, After: 1},
				{Kind: lcs.OpDelete, Before: 2, After: -1},
				{Kind: lcs.OpInsert, Before: -1, After: 2},
				{Kind: lcs.OpEqual, Before: 3, After: 3},
				{Kind: lcs.OpDelete, Before: 4, After: -1},
				{Kind: lcs.OpInsert, Before: -1, After: 4},
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

	// A case with edits builds after from before by that many random
	// deletions and insertions, so the two inputs share most lines. The
	// others draw both inputs at random.
	tests := map[string]struct {
		alphabet []string
		maxLen   int
		edits    int
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
		"four_symbols_few_edits": {
			alphabet: []string{"a", "b", "c", "d"},
			maxLen:   40,
			edits:    3,
		},
		"sixteen_symbols_few_edits": {
			alphabet: strings.Split("abcdefghijklmnop", ""),
			maxLen:   60,
			edits:    4,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rng := rand.New(rand.NewPCG(uint64(tc.edits+1), uint64(len(tc.alphabet))))
			h := lcs.NewHirschberg()

			randomLine := func() string {
				return tc.alphabet[rng.IntN(len(tc.alphabet))]
			}

			randomLines := func() []string {
				lines := make([]string, rng.IntN(tc.maxLen+1))
				for i := range lines {
					lines[i] = randomLine()
				}

				return lines
			}

			edited := func(lines []string) []string {
				lines = slices.Clone(lines)
				for range tc.edits {
					if len(lines) > 0 && rng.IntN(2) == 0 {
						i := rng.IntN(len(lines))
						lines = slices.Delete(lines, i, i+1)
					} else {
						lines = slices.Insert(lines, rng.IntN(len(lines)+1), randomLine())
					}
				}

				return lines
			}

			for range 2000 {
				var after []string

				before := randomLines()
				if tc.edits > 0 {
					after = edited(before)
				} else {
					after = randomLines()
				}

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

func TestHirschberg_DiffFarEdits(t *testing.T) {
	t.Parallel()

	// Changing the first and last lines leaves no shared start or end to
	// pair up. A search that takes time in proportion to the product of
	// the input lengths needs seconds here, while one that scales with the
	// number of changed lines needs milliseconds.
	const n = 40000

	before := make([]string, n)
	for i := range before {
		before[i] = fmt.Sprintf("key%d: value%d", i, i)
	}

	after := slices.Clone(before)
	after[0] = "changed first"
	after[n-1] = "changed last"

	want := make([]lcs.Op, 0, n+2)
	want = append(want,
		lcs.Op{Kind: lcs.OpDelete, Before: 0, After: -1},
		lcs.Op{Kind: lcs.OpInsert, Before: -1, After: 0},
	)

	for i := 1; i < n-1; i++ {
		want = append(want, lcs.Op{Kind: lcs.OpEqual, Before: i, After: i})
	}

	want = append(want,
		lcs.Op{Kind: lcs.OpDelete, Before: n - 1, After: -1},
		lcs.Op{Kind: lcs.OpInsert, Before: -1, After: n - 1},
	)

	start := time.Now()
	got := lcs.NewHirschberg().Diff(before, after)
	elapsed := time.Since(start)

	assert.Equal(t, want, got)
	assert.Less(t, elapsed, time.Second)
}

func TestHirschberg_DiffUniqueLines(t *testing.T) {
	t.Parallel()

	// A line that only one input holds is always a change. When most
	// lines are such changes, a search that walks through each of them
	// needs seconds here, while one that leaves them out needs
	// milliseconds.
	const n = 60000

	before := make([]string, n)
	for i := range before {
		before[i] = fmt.Sprintf("key%d: value%d", i, i)
	}

	tests := map[string]struct {
		after     func(i int) string
		wantEqual int
	}{
		"no_shared_lines": {
			after: func(i int) string {
				return fmt.Sprintf("other%d", i)
			},
			wantEqual: 0,
		},
		"every_other_line_shared": {
			after: func(i int) string {
				if i%2 == 0 {
					return before[i]
				}

				return fmt.Sprintf("other%d", i)
			},
			wantEqual: n / 2,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			after := make([]string, n)
			for i := range after {
				after[i] = tc.after(i)
			}

			start := time.Now()
			got := lcs.NewHirschberg().Diff(before, after)
			elapsed := time.Since(start)

			requireValidOps(t, before, after, got)

			equal := 0

			for _, op := range got {
				if op.Kind == lcs.OpEqual {
					equal++
				}
			}

			assert.Equal(t, tc.wantEqual, equal)
			assert.Less(t, elapsed, time.Second)
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

func BenchmarkHirschberg_Diff(b *testing.B) {
	for _, n := range []int{1000, 10000, 40000} {
		before := make([]string, n)
		for i := range before {
			before[i] = fmt.Sprintf("key%d: value%d", i, i)
		}

		// Changing the first and last lines leaves no shared start or end
		// to pair up, so the search covers both whole inputs.
		farEdits := slices.Clone(before)
		farEdits[0] = "changed first"
		farEdits[n-1] = "changed last"

		// Every tenth line changes, so the edits spread over the inputs.
		spread := slices.Clone(before)
		for i := 0; i < n; i += 10 {
			spread[i] = fmt.Sprintf("changed%d", i)
		}

		// No line of before appears in unrelated.
		unrelated := make([]string, n)
		for i := range unrelated {
			unrelated[i] = fmt.Sprintf("other%d", i)
		}

		cases := []struct {
			name  string
			after []string
		}{
			{"far_edits", farEdits},
			{"spread", spread},
			{"unrelated", unrelated},
		}

		for _, tc := range cases {
			b.Run(fmt.Sprintf("%s_%d", tc.name, n), func(b *testing.B) {
				h := lcs.NewHirschberg()

				b.ReportAllocs()

				for b.Loop() {
					h.Diff(before, tc.after)
				}
			})
		}
	}
}
