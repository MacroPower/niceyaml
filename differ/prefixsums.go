package differ

import "go.jacobcolvin.com/niceyaml/position"

// prefixSums holds precomputed prefix sums for O(1) range queries over the
// line counts of a diff.
type prefixSums struct {
	sums []int // sums[i] is the sum of elements 0..i-1.
}

// newPrefixSums creates a [*prefixSums] over n elements, where valueFn returns
// the value at index i.
func newPrefixSums(n int, valueFn func(i int) int) *prefixSums {
	sums := make([]int, n+1)
	for i := range n {
		sums[i+1] = sums[i] + valueFn(i)
	}

	return &prefixSums{sums: sums}
}

// At returns the cumulative sum of elements before index i.
func (p *prefixSums) At(i int) int {
	return p.sums[i]
}

// Range returns the sum of the elements within s.
func (p *prefixSums) Range(s position.Span) int {
	return p.sums[s.End] - p.sums[s.Start]
}
