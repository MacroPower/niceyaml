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
	tcs := map[string]struct {
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

	for name, tc := range tcs {
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
	want := []lcs.Op{
		{Kind: lcs.OpEqual, Before: 0, After: 0},
		{Kind: lcs.OpDelete, Before: 1, After: -1},
		{Kind: lcs.OpInsert, Before: -1, After: 1},
	}

	first := h.Diff([]string{"a", "b"}, []string{"a", "c"})
	assert.Equal(t, want, first)

	second := h.Diff([]string{"x", "y", "z"}, []string{"x", "z"})
	assert.Equal(t, []lcs.Op{
		{Kind: lcs.OpEqual, Before: 0, After: 0},
		{Kind: lcs.OpDelete, Before: 1, After: -1},
		{Kind: lcs.OpEqual, Before: 2, After: 1},
	}, second)

	assert.Equal(t, want, first, "each call returns a fresh slice, so the second leaves the first intact")
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
	tcs := map[string]struct {
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

	for name, tc := range tcs {
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

func TestHirschberg_DiffScales(t *testing.T) {
	t.Parallel()

	// Each case holds inputs long enough that a slow search needs seconds
	// and a fast one needs milliseconds. The after func gives line i of the
	// after input from the lines of before.
	tcs := map[string]struct {
		lines     int
		after     func(before []string, i int) string
		wantEqual int
	}{
		// Changing the first and last lines leaves no shared start or end
		// to pair up. A search whose time grows with the product of the
		// input lengths is slow here, and one that scales with the number
		// of changed lines is fast.
		"far_edits": {
			lines: 40000,
			after: func(before []string, i int) string {
				switch i {
				case 0:
					return "changed first"
				case len(before) - 1:
					return "changed last"
				}

				return before[i]
			},
			wantEqual: 40000 - 2,
		},
		// A line that only one input holds is always a change. When most
		// lines are such changes, a search that walks through each of them
		// is slow, and one that leaves them out is fast.
		"no_shared_lines": {
			lines: 60000,
			after: func(_ []string, i int) string {
				return fmt.Sprintf("other%d", i)
			},
			wantEqual: 0,
		},
		"every_other_line_shared": {
			lines: 60000,
			after: func(before []string, i int) string {
				if i%2 == 0 {
					return before[i]
				}

				return fmt.Sprintf("other%d", i)
			},
			wantEqual: 60000 / 2,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			before := numberedLines(tc.lines)

			after := make([]string, tc.lines)
			for i := range after {
				after[i] = tc.after(before, i)
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

// numberedLines returns n lines that are all distinct, "key0: value0"
// through "key<n-1>: value<n-1>".
func numberedLines(n int) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("key%d: value%d", i, i)
	}

	return lines
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
		msg := []any{"op %d: before=%q after=%q ops=%v", i, before, after, ops}

		switch op.Kind {
		case lcs.OpEqual:
			require.Equal(t, nextBefore, op.Before, msg...)
			require.Equal(t, nextAfter, op.After, msg...)
			require.Equal(t, before[op.Before], after[op.After], msg...)

			nextBefore++
			nextAfter++
			inserting = false

		case lcs.OpDelete:
			require.Equal(t, nextBefore, op.Before, msg...)
			require.Equal(t, -1, op.After, msg...)
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
			require.Equal(t, -1, op.Before, msg...)
			require.Equal(t, nextAfter, op.After, msg...)

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
