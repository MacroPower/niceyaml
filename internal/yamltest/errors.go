package yamltest

import "go.jacobcolvin.com/niceyaml"

// ProblemsOf returns the children of the node of err in the tree
// [niceyaml.NewErrorTree] builds that stand for the problems err heads,
// in the order the tree shows them.
func ProblemsOf(err error) []niceyaml.ErrorTree {
	return childrenOf(err, false)
}

// DetailsOf returns the children of the node of err in the tree
// [niceyaml.NewErrorTree] builds that explain it, in the order the tree
// shows them.
func DetailsOf(err error) []niceyaml.ErrorTree {
	return childrenOf(err, true)
}

// childrenOf returns the children of the node of err whose Detail is
// detail.
func childrenOf(err error, detail bool) []niceyaml.ErrorTree {
	var out []niceyaml.ErrorTree

	for _, c := range niceyaml.NewErrorTree(err).Children {
		if c.Detail == detail {
			out = append(out, c)
		}
	}

	return out
}

// BoundIn returns each distinct binding in the tree of err, a node before
// the nodes below it, as [niceyaml.ErrorTree.All] yields them.
func BoundIn(err error) []*niceyaml.SourceError {
	var (
		out  []*niceyaml.SourceError
		seen = map[*niceyaml.SourceError]bool{}
	)

	for n := range niceyaml.NewErrorTree(err).All() {
		if n.Bound != nil && !seen[n.Bound] {
			seen[n.Bound] = true
			out = append(out, n.Bound)
		}
	}

	return out
}

// RewriteError models a wrapper that discards the text of the error it
// wraps. Its message is "rewritten".
type RewriteError struct {
	Err error
}

// Error returns "rewritten".
func (e RewriteError) Error() string {
	return "rewritten"
}

// Unwrap returns the wrapped error.
func (e RewriteError) Unwrap() error {
	return e.Err
}
