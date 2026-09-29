package aliasing

import (
	"errors"
	"math"
)

// ErrExcessiveAliasing indicates a document or a value whose aliases make
// up too large a share of what a reader of it reads. The niceyaml and
// schema packages export the same error value.
var ErrExcessiveAliasing = errors.New("excessive aliasing")

// The limits on shared values follow the rule gopkg.in/yaml.v3 applies
// to the aliases in a document it decodes.
const (
	// A value's aliases count as excessive only once the value passes
	// both of these node counts, for aliased nodes and for all nodes.
	minAliased  = 100
	minExpanded = 1000

	// Aliases may make up the larger share of the nodes in a value of up
	// to the lower count, and the smaller share in a value of the higher
	// count or more. The share falls in a straight line between them.
	maxAliasRatio       = 0.99
	minAliasRatio       = 0.10
	aliasRatioRangeLow  = 400_000
	aliasRatioRangeHigh = 4_000_000

	// CountCap is where a node count stops growing, far past every limit
	// above, so a chain of nested aliases cannot overflow it.
	CountCap = math.MaxInt32
)

// Excessive reports whether aliased nodes make up too large a share of a
// value that holds distinct nodes and repeats aliased more through its
// aliases.
func Excessive(distinct, aliased int) bool {
	expanded := AddCapped(distinct, aliased)

	return aliased > minAliased && expanded > minExpanded &&
		float64(aliased)/float64(expanded) > allowedAliasRatio(expanded)
}

// AddCapped returns a+b, or [CountCap] when the sum would pass it. Both a
// and b fall between zero and CountCap.
func AddCapped(a, b int) int {
	if a > CountCap-b {
		return CountCap
	}

	return a + b
}

// allowedAliasRatio returns the share of the expanded nodes that aliases
// may make up in a value of expanded nodes.
func allowedAliasRatio(expanded int) float64 {
	switch {
	case expanded <= aliasRatioRangeLow:
		return maxAliasRatio
	case expanded >= aliasRatioRangeHigh:
		return minAliasRatio
	default:
		progress := float64(expanded-aliasRatioRangeLow) / float64(aliasRatioRangeHigh-aliasRatioRangeLow)

		return maxAliasRatio - (maxAliasRatio-minAliasRatio)*progress
	}
}
