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

	// Each letter of before and after is one line. Each rune of want is the
	// kind of one op: "=" keeps a line, "-" deletes one, and "+" inserts
	// one. The kinds fix the indices of the ops, and requireValidOps checks
	// those.
	tests := map[string]struct {
		before string
		after  string
		want   string
	}{
		"empty_both":                              {before: "", after: "", want: ""},
		"empty_before":                            {before: "", after: "ab", want: "++"},
		"empty_after":                             {before: "ab", after: "", want: "--"},
		"identical":                               {before: "abc", after: "abc", want: "==="},
		"all_different":                           {before: "ab", after: "cd", want: "--++"},
		"single_insert_at_start":                  {before: "bc", after: "abc", want: "+=="},
		"single_insert_at_end":                    {before: "ab", after: "abc", want: "==+"},
		"single_delete_at_start":                  {before: "abc", after: "bc", want: "-=="},
		"single_delete_at_end":                    {before: "abc", after: "ab", want: "==-"},
		"interleaved_changes":                     {before: "abcd", after: "axcy", want: "=-+=-+"},
		"replace_in_middle":                       {before: "abc", after: "axc", want: "=-+="},
		"single_element_before_match":             {before: "a", after: "xay", want: "+=+"},
		"single_element_before_no_match":          {before: "a", after: "xyz", want: "-+++"},
		"lcs_non_contiguous":                      {before: "axbyc", after: "abc", want: "=-=-="},
		"complex_diff":                            {before: "abcde", after: "xbcye", want: "-+==-+="},
		"repeated_block_delete":                   {before: "abbaabc", after: "abbc", want: "===---="},
		"repeated_block_insert":                   {before: "abbc", after: "abbaabc", want: "===+++="},
		"trailing_repeated_block_delete":          {before: "kabab", after: "kab", want: "===--"},
		"trailing_repeated_block_insert":          {before: "kab", after: "kabab", want: "===++"},
		"repeated_line_around_deletion":           {before: "axa", after: "a", want: "=--"},
		"repeated_pair_deletion":                  {before: "abab", after: "ab", want: "==--"},
		"repeated_line_deletion":                  {before: "aa", after: "a", want: "=-"},
		"shared_start_and_end":                    {before: "abcdef", after: "abxyef", want: "==--++=="},
		"shared_start_and_end_of_unequal_lengths": {before: "abcef", after: "axyzef", want: "=--+++=="},
		"unique_lines_between_shared_lines":       {before: "xaybz", after: "paqbr", want: "-+=-+=-+"},
		"replace_before_repeated_line":            {before: "xx", after: "yx", want: "-+="},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			before, after := strings.Split(tc.before, ""), strings.Split(tc.after, "")
			got := lcs.NewHirschberg().Diff(before, after)

			requireValidOps(t, before, after, got)
			assert.Equal(t, tc.want, kindsOf(got))
			assert.Equal(t, tc.want == "", got == nil, "only a diff of no lines is nil")
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

	// Each call returns a fresh slice, so the second call left the first
	// result intact.
	assert.Equal(t, []lcs.Op{
		{Kind: lcs.OpEqual, Before: 0, After: 0},
		{Kind: lcs.OpDelete, Before: 1, After: -1},
		{Kind: lcs.OpInsert, Before: -1, After: 1},
	}, ops1)
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
				require.Equal(t, naiveLCSLen(before, after), equalOps(got),
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
			assert.Equal(t, tc.wantEqual, equalOps(got))
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

// kindsOf returns the kinds of ops as one rune each: "=" for [lcs.OpEqual],
// "-" for [lcs.OpDelete], and "+" for [lcs.OpInsert].
func kindsOf(ops []lcs.Op) string {
	var sb strings.Builder

	for _, op := range ops {
		switch op.Kind {
		case lcs.OpEqual:
			sb.WriteByte('=')
		case lcs.OpDelete:
			sb.WriteByte('-')
		case lcs.OpInsert:
			sb.WriteByte('+')
		default:
			sb.WriteByte('?')
		}
	}

	return sb.String()
}

// equalOps returns the number of [lcs.OpEqual] ops in ops.
func equalOps(ops []lcs.Op) int {
	equal := 0

	for _, op := range ops {
		if op.Kind == lcs.OpEqual {
			equal++
		}
	}

	return equal
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
	type benchCase struct {
		name  string
		after []string
	}

	for _, n := range []int{1000, 10000, 40000} {
		before := make([]string, n)
		for i := range before {
			before[i] = fmt.Sprintf("key%d: value%d", i, i)
		}

		// Swapping the first and last lines leaves no shared start or end
		// to pair up, and both lines stay in both inputs, so the search
		// covers both whole inputs with only a few edits.
		farSwap := slices.Clone(before)
		farSwap[0], farSwap[n-1] = farSwap[n-1], farSwap[0]

		// Every tenth line changes, so the edits spread over the inputs.
		// Each changed line appears in only one input, so the search
		// leaves it out.
		spread := slices.Clone(before)
		for i := 0; i < n; i += 10 {
			spread[i] = fmt.Sprintf("changed%d", i)
		}

		// No line of before appears in unrelated.
		unrelated := make([]string, n)
		for i := range unrelated {
			unrelated[i] = fmt.Sprintf("other%d", i)
		}

		cases := []benchCase{
			{"far_swap", farSwap},
			{"spread", spread},
			{"unrelated", unrelated},
		}

		// The same lines in a different order keep every line in the
		// search and take time close to the square of n, so these cases
		// skip the largest n.
		if n <= 10000 {
			reversed := slices.Clone(before)
			slices.Reverse(reversed)

			shuffled := slices.Clone(before)
			rng := rand.New(rand.NewPCG(1, 2))
			rng.Shuffle(n, func(i, j int) {
				shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
			})

			cases = append(cases,
				benchCase{"reversed", reversed},
				benchCase{"shuffled", shuffled},
			)
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

func BenchmarkHirschberg_DiffAfterLarge(b *testing.B) {
	// Each call returns n lines and a copy with every tenth line changed.
	spreadInputs := func(n int) ([]string, []string) {
		before := make([]string, n)
		for i := range before {
			before[i] = fmt.Sprintf("key%d: value%d", i, i)
		}

		after := slices.Clone(before)
		for i := 0; i < n; i += 10 {
			after[i] = fmt.Sprintf("changed%d", i)
		}

		return before, after
	}

	// Each call returns n lines that alternate between two values, with a
	// first and last line that differ between the inputs. The line map
	// that intern makes for them has room for n lines but holds only six.
	repeatedInputs := func(n int) ([]string, []string) {
		before := make([]string, n)
		for i := range before {
			before[i] = []string{"a", "b"}[i%2]
		}

		after := slices.Clone(before)
		before[0], before[n-1] = "first before", "last before"
		after[0], after[n-1] = "first after", "last after"

		return before, after
	}

	before, after := spreadInputs(200)

	cases := []struct {
		name  string
		large func(int) ([]string, []string)
	}{
		{"fresh", nil},
		{"after_100000", spreadInputs},
		{"after_100000_repeated", repeatedInputs},
	}

	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			h := lcs.NewHirschberg()

			// A large diff leaves its buffers in the pool, and the small
			// diffs after it should run as fast as on a fresh instance.
			if tc.large != nil {
				h.Diff(tc.large(100000))
			}

			b.ReportAllocs()

			for b.Loop() {
				h.Diff(before, after)
			}
		})
	}
}
