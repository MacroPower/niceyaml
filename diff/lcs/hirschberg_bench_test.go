package lcs_test

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"

	"go.jacobcolvin.com/niceyaml/diff/lcs"
)

func BenchmarkHirschberg_Diff(b *testing.B) {
	type benchCase struct {
		name  string
		after []string
	}

	for _, n := range []int{1000, 10000, 40000} {
		before := numberedLines(n)

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
		before := numberedLines(n)

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
