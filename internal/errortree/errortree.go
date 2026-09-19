// Package errortree lays the message of an error out as a tree, with one
// node per error, for a printer that draws nested errors as branches.
//
// The tree reads the errors themselves rather than their messages: a node
// is an error, and its children are the errors nested in it with
// [niceyaml.WithErrors], which a [niceyaml.SourceError] binds as children
// of its own. The package reads the niceyaml types through their exported
// accessors only, so it never disagrees with [niceyaml.SourceError.Error]
// about which error a line belongs to.
package errortree

import (
	"slices"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/position"
)

// Tree is the message of an error laid out as a tree. The root holds the
// message of the error itself and each error nested in it is a child.
//
// Create instances with [New].
type Tree struct {
	// Text is the message of the node: the message of the error without
	// the errors nested in it. It is empty for a node that stands for
	// several errors and adds no message of its own, such as one built
	// from [errors.Join].
	Text string
	// Children are the nodes of the nested errors, in the order the tree
	// shows them.
	Children []Tree
}

// New creates a new [Tree] from err.
//
// The root text is the message of err, so context a wrapper added stays
// in front. For an error bound to a source, the root keeps the
// "name:line:col:" position [niceyaml.SourceError.Error] gives it, and
// each child, a binding of its own from [niceyaml.SourceError.Errors],
// carries the "line:col:" position its location resolved to without the
// name, since the root names the source already. The children of a node
// sort by position; those whose location did not resolve follow in the
// order they were given. A nested error with nested errors of its own is
// a subtree.
//
// An error that unwraps to several, such as one from [errors.Join], is a
// node with no text and one child per error, so a run over several files
// reads as one tree with a branch per file. A binding of such an error is
// the same node, with each child carrying its whole
// [niceyaml.SourceError.Error], since no root names the source for it. A
// node with no text adds nothing: its children take its place in the tree
// above it, and one with a single child is that child.
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
		if _, joined := joinBranches(bound.Unwrap()); joined {
			return newTree("", trees(boundChildren(bound, true)))
		}
	}

	return newTree(err.Error(), children(err))
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

// children returns the nodes of the errors nested along the cause chain of
// err. A binding met along the chain contributes its bound children, each
// with the position it resolved to, and the chain ends there, since the
// binding bound everything below it. An unbound Error contributes its
// nested errors as they are.
func children(err error) []Tree {
	var kids []positioned

	for cur := err; !nothing(cur); {
		switch x := cur.(type) { //nolint:errorlint // Walks the chain one node at a time.
		case *niceyaml.SourceError:
			kids = append(kids, boundChildren(x, false)...)
			cur = nil

		case *niceyaml.Error:
			for _, n := range x.Errors() {
				kids = append(kids, positioned{tree: New(n)})
			}

			cur = x.Cause()

		case interface{ Unwrap() error }:
			cur = x.Unwrap()

		default:
			cur = nil
		}
	}

	return trees(kids)
}

// trees returns the nodes of kids in position order, with those whose
// location did not resolve after the rest in the order they were given.
// Returns nil when kids is empty.
func trees(kids []positioned) []Tree {
	if len(kids) == 0 {
		return nil
	}

	slices.SortStableFunc(kids, comparePositioned)

	out := make([]Tree, 0, len(kids))
	for _, kid := range kids {
		out = append(out, kid.tree)
	}

	return out
}

// boundChildren returns the nodes of the children of bound, each with its
// own children as a subtree. A child bound to another source carries its
// whole [niceyaml.SourceError.Error], which names that source, and so
// does a child bound to the same source when named is set, for a parent
// with no text of its own. Otherwise a child bound to the same source
// carries the "line:col:" its location resolved to in front of its
// message, without the name the parent gives already.
func boundChildren(bound *niceyaml.SourceError, named bool) []positioned {
	var kids []positioned

	for _, child := range bound.Errors() {
		kid := positioned{}

		// A location the source does not hold resolved to nothing the
		// excerpt can mark, so the node reads as an unlocated one.
		rng, err := child.Range()
		if err == nil {
			kid.located = true
			kid.pos = rng.Start
		}

		text := child.Error()
		if !named && child.Source() == bound.Source() {
			text = child.Unwrap().Error()
			if kid.located {
				text = prefix(kid.pos.String()+":", text)
			}
		}

		kid.tree = newTree(text, children(child))
		kids = append(kids, kid)
	}

	return kids
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
