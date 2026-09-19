// Package errortree lays the message of an error out as a tree, with one
// node per error, for a printer that draws nested errors as branches.
//
// The tree reads the errors themselves rather than their messages: a node
// is an error, and its children are the errors nested in it with
// [niceyaml.WithErrors] along its cause chain. The package reads the
// niceyaml types through their exported accessors only, so it never
// disagrees with [niceyaml.SourceError.Error] about which error a line
// belongs to.
package errortree

import (
	"slices"
	"strings"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/position"
)

// Tree is the message of an error laid out as a tree. The root holds the
// message of the error itself and each error nested in it is a child.
//
// Create instances with [New].
type Tree struct {
	// Text is the message of the node: the message of the error without
	// the lines of the errors nested in it. It is empty for a node that
	// stands for several errors and adds no message of its own, such as
	// one built from [errors.Join].
	Text string
	// Children are the nodes of the nested errors, in the order the tree
	// shows them.
	Children []Tree
}

// New creates a new [Tree] from err.
//
// The root text is the message of err without its nested errors, so
// context a wrapper added stays in front. For an error bound to a source,
// the root keeps the "name:line:col:" position [niceyaml.SourceError.Error]
// gives it, and each child carries the "line:col:" position its location
// resolved to without the name, since the root names the source already.
// The children of a node sort by position; those whose location did not
// resolve follow in the order they were given. A nested error with nested
// errors of its own is a subtree.
//
// An error that unwraps to several, such as one from [errors.Join], is a
// node with no text and one child per error, so a run over several files
// reads as one tree with a branch per file. A node with no text adds
// nothing: its children take its place in the tree above it, and one with
// a single child is that child.
//
// The children come from the errors rather than from the text of the
// message, so a wrapper that rewrites the message it wraps keeps its
// children. A nil err yields the zero Tree.
func New(err error) Tree {
	if nothing(err) {
		return Tree{}
	}

	if branches, ok := joinBranches(err); ok {
		children := make([]Tree, 0, len(branches))

		for _, branch := range branches {
			if !nothing(branch) {
				children = append(children, New(branch))
			}
		}

		return newTree("", children)
	}

	if bound, ok := err.(*niceyaml.SourceError); ok { //nolint:errorlint // The node itself, not a chain search.
		return newTree(bound.Message(), children(bound.Unwrap(), bound))
	}

	return newTree(head(err), children(err, nil))
}

// nothing reports whether err is nil or a nil pointer to an Error or a
// SourceError, which carries no message and no nested errors.
func nothing(err error) bool {
	switch x := err.(type) { //nolint:errorlint // The node itself, not a chain search.
	case nil:
		return true
	case *niceyaml.Error:
		return x == nil
	case *niceyaml.SourceError:
		return x == nil
	default:
		return false
	}
}

// joinBranches returns the errors err unwraps to when err is joined from
// several, as [errors.Join] builds one, and false for any other error. An
// Error unwraps to several too, but it is one node of the tree with its
// nested errors as children, so it is not a join.
func joinBranches(err error) ([]error, bool) {
	if _, ok := err.(*niceyaml.Error); ok { //nolint:errorlint // The node itself, not a chain search.
		return nil, false
	}

	if joined, ok := err.(interface{ Unwrap() []error }); ok { //nolint:errorlint // The node itself, not a chain search.
		return joined.Unwrap(), true
	}

	return nil, false
}

// head returns the message of err without the nested lines a binding along
// its cause chain wrote: the message of that binding is replaced by its
// [niceyaml.SourceError.Message], and the text around it stays as the
// wrappers wrote it.
func head(err error) string {
	msg := err.Error()

	inner := binding(err)
	if inner != nil {
		msg = strings.Replace(msg, inner.Error(), inner.Message(), 1)
	}

	return msg
}

// binding returns the first [*niceyaml.SourceError] along the cause chain
// of err, or nil when the chain holds none. The chain follows a wrapper to
// the error it wraps and an Error to its cause, and does not follow a
// joined error.
func binding(err error) *niceyaml.SourceError {
	for cur := err; !nothing(cur); {
		switch x := cur.(type) { //nolint:errorlint // Walks the chain one node at a time.
		case *niceyaml.SourceError:
			return x
		case *niceyaml.Error:
			cur = x.Cause()
		case interface{ Unwrap() error }:
			cur = x.Unwrap()
		default:
			return nil
		}
	}

	return nil
}

// children returns the nodes of the errors nested along the cause chain of
// err, sorted by position. The binding given resolved their positions, or
// is nil when none did; a binding met along the chain takes over for the
// errors below it, since it resolved those.
func children(err error, bound *niceyaml.SourceError) []Tree {
	var kids []positioned

	for cur := err; !nothing(cur); {
		switch x := cur.(type) { //nolint:errorlint // Walks the chain one node at a time.
		case *niceyaml.SourceError:
			bound = x
			cur = x.Unwrap()

		case *niceyaml.Error:
			for _, n := range x.Errors() {
				kids = append(kids, child(n, bound))
			}

			cur = x.Cause()

		case interface{ Unwrap() error }:
			cur = x.Unwrap()

		default:
			cur = nil
		}
	}

	if len(kids) == 0 {
		return nil
	}

	slices.SortStableFunc(kids, comparePositioned)

	trees := make([]Tree, 0, len(kids))
	for _, kid := range kids {
		trees = append(trees, kid.tree)
	}

	return trees
}

// child returns the node of the nested error n, with the "line:col:" its
// location resolved to in front when bound resolved it, and the errors
// nested in n as its children.
func child(n *niceyaml.Error, bound *niceyaml.SourceError) positioned {
	kid := positioned{tree: newTree(head(n), children(n, bound))}

	if bound == nil {
		return kid
	}

	if pos, ok := bound.PositionOf(n); ok {
		kid.located = true
		kid.pos = pos
		kid.tree.Text = prefix(pos.String()+":", kid.tree.Text)
	}

	return kid
}

// prefix returns p and msg separated by a space, or p alone when msg is
// empty.
func prefix(p, msg string) string {
	if msg == "" {
		return p
	}

	return p + " " + msg
}

// positioned is a child of a node and the position it resolved to, when
// located, for the order the children take.
type positioned struct {
	tree    Tree
	pos     position.Position
	located bool
}

// comparePositioned orders children by position, with those that have none
// after those that do, and equal among themselves so a stable sort keeps
// their order.
func comparePositioned(a, b positioned) int {
	switch {
	case !a.located && !b.located:
		return 0
	case !a.located:
		return 1
	case !b.located:
		return -1
	case a.pos.Line != b.pos.Line:
		return a.pos.Line - b.pos.Line
	default:
		return a.pos.Col - b.pos.Col
	}
}

// newTree returns the node with text and children, less the nodes that add
// nothing: a child with no text gives its place to its own children, and a
// node with no text and a single child is that child.
func newTree(text string, children []Tree) Tree {
	if len(children) == 0 {
		return Tree{Text: text}
	}

	flat := make([]Tree, 0, len(children))

	for _, child := range children {
		if child.Text == "" {
			flat = append(flat, child.Children...)
		} else {
			flat = append(flat, child)
		}
	}

	if text == "" && len(flat) == 1 {
		return flat[0]
	}

	return Tree{Text: text, Children: flat}
}
