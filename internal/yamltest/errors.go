package yamltest

import (
	"testing"

	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
)

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

// ErrsOf returns the error of each node in nodes, or nil when nodes is
// empty. It reads the errors of what [ProblemsOf] and [DetailsOf] return.
func ErrsOf(nodes []niceyaml.ErrorTree) []error {
	var errs []error

	for _, n := range nodes {
		errs = append(errs, n.Err)
	}

	return errs
}

// RequireSourceError returns the first [*niceyaml.SourceError] in the
// chain of err, as [errors.As] finds it. The test fails when err holds
// none.
func RequireSourceError(tb testing.TB, err error) *niceyaml.SourceError {
	tb.Helper()

	var bound *niceyaml.SourceError

	require.ErrorAs(tb, err, &bound)

	return bound
}

// BoundNode returns the [*niceyaml.Node] bound is bound to, as
// [niceyaml.SourceError.Node] returns it. The test fails when bound is
// bound to none.
func BoundNode(tb testing.TB, bound *niceyaml.SourceError) *niceyaml.Node {
	tb.Helper()

	node, ok := bound.Node()
	require.True(tb, ok, "the error is bound to no Node")

	return node
}

// BoundDocument returns the root of the document bound is bound to, as
// [niceyaml.SourceError.Document] returns it. The test fails when bound
// is bound to none.
func BoundDocument(tb testing.TB, bound *niceyaml.SourceError) *niceyaml.Node {
	tb.Helper()

	doc, ok := bound.Document()
	require.True(tb, ok, "the error is bound to no document")

	return doc
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
