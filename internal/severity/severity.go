// Package severity orders the rows that a renderer draws below a line by
// the severity of the annotations on them.
//
// The rows of errors come first, then the rows of warnings, then the rows
// of every other kind. An error annotates its line in [kind.TextError] and
// a warning in [kind.TextWarn].
package severity

import (
	"cmp"
	"slices"

	"go.jacobcolvin.com/niceyaml/style/kind"
)

// Order returns the indices of kinds, the kinds that the groups of
// annotations below a line render in, in the order a renderer draws them.
// A [kind.TextError] group comes first and a [kind.TextWarn] group next.
// The other groups follow in the order given.
func Order(kinds []kind.Kind) []int {
	order := make([]int, len(kinds))
	for i := range order {
		order[i] = i
	}

	slices.SortStableFunc(order, func(a, b int) int {
		return cmp.Compare(rank(kinds[a]), rank(kinds[b]))
	})

	return order
}

// rank returns the sort key of the rows of k: 0 for errors, 1 for
// warnings, and 2 for every other kind.
func rank(k kind.Kind) int {
	switch k {
	case kind.TextError:
		return 0
	case kind.TextWarn:
		return 1
	default:
		return 2
	}
}
