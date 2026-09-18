// Package errortree lays the message of an error out as a tree, with one
// node per error, for a printer that draws nested errors as branches.
//
// The tree splits the nested lines off a message the way the niceyaml
// package wrote them, through the [errchain] walk both share, so the tree
// and the plain message never disagree about where one ends and the other
// begins. The package reads the niceyaml types through their exported
// accessors only.
package errortree

import (
	"slices"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/errchain"
	"go.jacobcolvin.com/niceyaml/position"
)

// Tree is the message of an error laid out as a tree. The root holds the
// message of the error itself and each error nested in it is a child.
//
// Create instances with [New].
type Tree struct {
	// Text is the message of the node: the message of the error less the
	// lines of the errors nested in it. It is empty for a node that stands
	// for several errors and adds no message of its own, such as one built
	// from [errors.Join].
	Text string
	// Children are the nodes of the nested errors, in the order the tree
	// shows them.
	Children []Tree
}

// New creates a new [Tree] from err.
//
// The root text is the message of err less the lines of its nested errors,
// so context a wrapper added stays in front. For an error bound to a
// source, the root keeps the "name:line:col:" position
// [niceyaml.SourceError.Error] gives it, and each child carries the
// "line:col:" position its location resolved to without the name, since
// the root names the source already. The children of a node sort by
// position; those whose location did not resolve follow in the order they
// were given. A nested error with nested errors of its own is a subtree.
//
// An error that unwraps to several, such as one from [errors.Join], is a
// node with no text and one child per error, so a run over several files
// reads as one tree with a branch per file. A node with no text adds
// nothing: its children take its place in the tree above it, and one with
// a single child is that child.
//
// A wrapper that rewrites the message it wraps leaves nothing for the
// tree to find, so the node holds the message as it is and has no
// children, as [niceyaml.SourceError.Error] keeps such a message. A nil err
// yields the zero Tree.
func New(err error) Tree {
	if funcs.Nothing(err) {
		return Tree{}
	}

	if branches, ok := joinBranches(err); ok {
		children := make([]Tree, 0, len(branches))

		for _, branch := range branches {
			if !funcs.Nothing(branch) {
				children = append(children, New(branch))
			}
		}

		return newTree("", children)
	}

	return treeOf(err, err.Error(), nil)
}

// funcs describes the niceyaml types to the [errchain.Funcs] walk through
// their exported accessors.
var funcs = errchain.Funcs{
	Nothing: func(err error) bool {
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
	},
	Node: func(err error) (errchain.Node, bool) {
		x, ok := err.(*niceyaml.Error) //nolint:errorlint // The node itself, not a chain search.
		if !ok || x == nil {
			return errchain.Node{}, false
		}

		return errchain.Node{Cause: x.Cause(), Nested: errchain.Errors(x.Errors()), Located: x.Located()}, true
	},
	Binding: func(err error) (errchain.Binding, bool) {
		x, ok := err.(*niceyaml.SourceError) //nolint:errorlint // The node itself, not a chain search.
		if !ok || x == nil {
			return errchain.Binding{}, false
		}

		return errchain.Binding{Inner: x.Unwrap(), Name: x.Source().Name()}, true
	},
	Position: func(binding, nested error) (position.Position, bool) {
		x, ok := binding.(*niceyaml.SourceError) //nolint:errorlint // The node itself, not a chain search.
		if !ok || x == nil {
			return position.Position{}, false
		}

		n, ok := nested.(*niceyaml.Error) //nolint:errorlint // The node itself, not a chain search.
		if !ok || n == nil {
			return position.Position{}, false
		}

		return x.PositionOf(n)
	},
}

// joinBranches returns the errors err unwraps to when err is joined from
// several, as [errors.Join] builds one, and false for any other error. A
// node unwraps to several too, but it is one node of the tree with its
// nested errors as children, so it is not a join.
func joinBranches(err error) ([]error, bool) {
	if _, ok := funcs.Node(err); ok {
		return nil, false
	}

	if joined, ok := err.(interface{ Unwrap() []error }); ok { //nolint:errorlint // The node itself, not a chain search.
		return joined.Unwrap(), true
	}

	return nil, false
}

// treeOf returns the tree of err, whose message is msg, with binding the
// binding that resolved the positions of the nested errors err holds, or
// nil. The outermost error passes nil, since its own chain holds any
// binding; a nested error inherits the binding of the error above it while
// its message is its own, as [niceyaml.Error.Error] writes it.
func treeOf(err error, msg string, binding error) Tree {
	head, nested, ok := funcs.Split(err, msg, binding)
	if !ok || len(nested) == 0 {
		return Tree{Text: msg}
	}

	children := make([]positioned, 0, len(nested))

	for _, n := range nested {
		child := positioned{tree: treeOf(n.Err, n.Text, n.Binding)}

		if n.Binding != nil {
			if pos, ok := funcs.Position(n.Binding, n.Err); ok {
				child.located = true
				child.pos = pos
				child.tree.Text = errchain.Prefix(errchain.FormatPosition("", pos), child.tree.Text)
			}
		}

		children = append(children, child)
	}

	slices.SortStableFunc(children, comparePositioned)

	trees := make([]Tree, 0, len(children))
	for _, child := range children {
		trees = append(trees, child.tree)
	}

	return newTree(head, trees)
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
