package yamltest

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
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

// OnlyProblem returns the one problem that [niceyaml.Problems] yields for
// err. The test fails unless err holds exactly one problem. Problems
// walks past each summary and each join to the problems below it, and
// [ProblemsOf] reads only the children of the root.
func OnlyProblem(tb testing.TB, err error) niceyaml.ErrorTree {
	tb.Helper()

	problems := slices.Collect(niceyaml.Problems(err))
	require.Len(tb, problems, 1)

	return problems[0]
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

// RequireError returns the first [*niceyaml.Error] in the chain of err,
// as [errors.As] finds it, so a test reads the accessors of the Error.
// The test fails when err holds none.
func RequireError(tb testing.TB, err error) *niceyaml.Error {
	tb.Helper()

	var located *niceyaml.Error

	require.ErrorAs(tb, err, &located)

	return located
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

// PathOf returns the path that the Path method of x reports, such as the
// path of a [*niceyaml.Error], a [*niceyaml.SourceError], or a
// [niceyaml.ErrorTree]. The test fails when x reports no path.
func PathOf(tb testing.TB, x interface{ Path() (paths.Path, bool) }) paths.Path {
	tb.Helper()

	path, ok := x.Path()
	require.True(tb, ok, "%T reports no path", x)

	return path
}

// RangeOf returns the range that the Range method of x reports, such as
// the range of a [*niceyaml.Error], a [*niceyaml.SourceError], or a
// [niceyaml.ErrorTree]. The test fails when x reports no range.
func RangeOf(tb testing.TB, x interface{ Range() (position.Range, bool) }) position.Range {
	tb.Helper()

	rng, ok := x.Range()
	require.True(tb, ok, "%T reports no range", x)

	return rng
}

// PositionOf returns the position that the Position method of x
// reports, such as the position of a [*niceyaml.Error], a
// [*niceyaml.SourceError], or a [niceyaml.ErrorTree]. The test fails
// when x reports no position.
func PositionOf(tb testing.TB, x interface {
	Position() (position.Position, bool)
},
) position.Position {
	tb.Helper()

	pos, ok := x.Position()
	require.True(tb, ok, "%T reports no position", x)

	return pos
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

// AssertBoundTo asserts that each binding that [BoundIn] returns for err
// reports source through [niceyaml.SourceError.Source]. An error that
// holds no binding passes.
func AssertBoundTo(tb testing.TB, source *niceyaml.Source, err error) {
	tb.Helper()

	for _, bound := range BoundIn(err) {
		assert.Same(tb, source, bound.Source())
	}
}

// ErrorAt creates a [*niceyaml.Error] with the message msg at the value
// under keys. The keys form a path from the node that binds the error,
// as [paths.Current] and [paths.Path.Child] build it. With no keys, the
// error sits at that node.
func ErrorAt(msg string, keys ...string) *niceyaml.Error {
	return niceyaml.NewError(msg, niceyaml.AtPath(paths.Current().Child(keys...)))
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

// CheckError models an error type of a caller's own that wraps the error
// of a check. Its message is "check: " followed by the message of that
// error. Its methods have pointer receivers, so a test wraps an error as
// &CheckError{Err: err} and finds it through [errors.As] with a
// *CheckError target.
type CheckError struct {
	Err error
}

// Error returns "check: " followed by the message of the wrapped error.
func (e *CheckError) Error() string {
	return "check: " + e.Err.Error()
}

// Unwrap returns the wrapped error.
func (e *CheckError) Unwrap() error {
	return e.Err
}

// UpperError models a wrapper that rewrites the message of the error it
// wraps. Its message is that message in upper case.
type UpperError struct {
	Err error
}

// Error returns the message of the wrapped error in upper case.
func (e UpperError) Error() string {
	return strings.ToUpper(e.Err.Error())
}

// Unwrap returns the wrapped error.
func (e UpperError) Unwrap() error {
	return e.Err
}

// IndentError models a wrapper that puts the message of the error it
// wraps under a heading. Its message is "validation:" followed by each
// line of that message on a line of its own, indented by two spaces.
type IndentError struct {
	Err error
}

// Error returns "validation:" followed by each line of the message of
// the wrapped error, indented by two spaces.
func (e IndentError) Error() string {
	return "validation:\n  " + strings.ReplaceAll(e.Err.Error(), "\n", "\n  ")
}

// Unwrap returns the wrapped error.
func (e IndentError) Unwrap() error {
	return e.Err
}
