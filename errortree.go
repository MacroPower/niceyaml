package niceyaml

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// ErrorTree is the message of an error laid out as a tree, with one node
// per error, for a renderer that draws nested errors as branches. The
// root holds the message of the error itself and each error nested in it
// is a child. [FormatError] renders one as plain text and
// [go.jacobcolvin.com/niceyaml/printer.Printer.PrintError] with styles,
// and a renderer of its own, such as a viewer that lists violations,
// reads the same tree so it never disagrees with them about which error
// a line belongs to.
//
// Create instances with [NewErrorTree].
type ErrorTree struct {
	// Text is the message of the node, the message of the error without
	// the errors nested in it. It is empty for a node that stands for
	// several errors and adds no message of its own, such as one built
	// from [errors.Join].
	Text string
	// Children are the nodes of the nested errors, in the order the tree
	// shows them.
	Children []ErrorTree
}

// NewErrorTree creates a new [ErrorTree] from err.
//
// The root text is the message of err, so context a wrapper added stays
// in front. For an error bound to a source, the root keeps the
// "name:line:col:" position [SourceError.Error] gives it, and
// each child, a binding of its own from [SourceError.Errors],
// carries the "line:col:" position its location resolved to without the
// name, since the root names the source already. The children of a
// binding sort by position within the source they are bound to, with the
// sources in the order they first appear, since a line number counts only
// in the source that holds it. Children whose location did not resolve
// follow the rest in the order their parent lists them. An [Error] or
// join outside any binding keeps its children in the order it lists them,
// bindings included. So a join of one binding per document shows the
// documents in the order [errors.Join] took them, as the excerpts of
// [FormatError] do. A nested error with nested errors of its own is a
// subtree.
//
// An error that unwraps to several and whose message is theirs one per
// line, such as one from [errors.Join], is a node with no text and one
// child per error, so a run over several files reads as one tree with a
// branch per file. An [Error] that only wraps such an error, with no
// location and no errors nested with [WithErrors], is the same node. A
// binding of such an error is the same node, with each child carrying
// its whole [SourceError.Error], since no root names the source for it.
// A node with no text adds nothing. Its children take its place in the
// tree above it, and one with a single child is that child.
//
// Any other error that unwraps to several keeps its text and has a child
// per branch. A wrapper that [fmt.Errorf] builds with several %w verbs
// keeps only its branches that carry a location or errors nested below
// them, since its text shows the rest already. With one such branch, its
// children are the ones along the cause chain of that branch.
//
// The children come from the errors rather than from the text of the
// message, so a wrapper that rewrites the message it wraps keeps its
// children. A nil err yields the zero ErrorTree.
func NewErrorTree(err error) ErrorTree {
	if isNothing(err) {
		return ErrorTree{}
	}

	if branches, ok := joinBranches(err); ok {
		children := make([]ErrorTree, 0, len(branches))

		for _, branch := range branches {
			if !isNothing(branch) {
				children = append(children, NewErrorTree(branch))
			}
		}

		return newTree("", children)
	}

	if bound, ok := err.(*SourceError); ok { //nolint:errorlint // The node itself, not a chain search.
		if _, joined := joinBranches(bound.Unwrap()); joined {
			return newTree("", trees(boundChildren(bound, true)))
		}
	}

	return newTree(err.Error(), children(err))
}

// joinBranches returns the errors err unwraps to when err is a join of
// several, as [errors.Join] builds one, and false for any other error. An
// Error unwraps to several too, but it is one node of the tree with its
// nested errors as children, so it is not a join. The exception is an
// Error that adds nothing to the error it wraps, as [Error.addsNothing]
// reports, which is a join when the error it wraps is one. Nor is an
// error built with several %w verbs a join, since its message is its own
// rather than its branches' messages one per line, so it keeps its text,
// and [followBranches] picks the branches that stand below it.
//
// An error [errors.Join] built is a join by its type alone, since its
// message is always its branches' messages one per line. Any other error
// that unwraps to several is a join only when its message is that text,
// as [isJoinMessage] reports.
func joinBranches(err error) ([]error, bool) {
	if x, ok := err.(*Error); ok { //nolint:errorlint // The node itself, not a chain search.
		if !x.addsNothing() {
			return nil, false
		}

		return joinBranches(x.err)
	}

	joined, ok := err.(interface{ Unwrap() []error }) //nolint:errorlint // The node itself, not a chain search.
	if !ok {
		return nil, false
	}

	branches := joined.Unwrap()
	if reflect.TypeOf(err) != joinType && !isJoinMessage(err.Error(), branches) {
		return nil, false
	}

	return branches, true
}

var (
	// The type of the errors [errors.Join] builds.
	joinType = reflect.TypeOf(errors.Join(errors.New("")))

	// The type of the errors [fmt.Errorf] builds with several %w verbs.
	wrapErrorsType = reflect.TypeOf(fmt.Errorf("%w%w", errors.New(""), errors.New("")))
)

// isJoinMessage reports whether msg is the messages of branches one per
// line, which is how [errors.Join] writes the message of the error it
// builds. A nil branch carries no message and no line of its own, so it
// takes no separator either.
func isJoinMessage(msg string, branches []error) bool {
	var (
		sb    strings.Builder
		first = true
	)

	for _, branch := range branches {
		if branch == nil {
			continue
		}

		if !first {
			sb.WriteByte('\n')
		}

		first = false

		sb.WriteString(branch.Error())
	}

	return msg == sb.String()
}

// children returns the nodes of the children [walkChildren] finds along
// the cause chain of err. A binding that ends the chain contributes its
// bound children, each with the position it resolved to, since the
// binding bound everything below it. Every other child is the tree of
// the error rebased under its base, as binding rebases it. The nested
// errors of an unbound Error and the branches of a join keep the order
// their parent lists them in, since only the children of a binding carry
// the source and position [trees] sorts by.
func children(err error) []ErrorTree {
	var kids []positioned

	walkChildren(err,
		func(x *SourceError) { kids = append(kids, boundChildren(x, false)...) },
		func(n error, base childBase) {
			kids = append(kids, positioned{tree: NewErrorTree(base.rebase(n))})
		},
	)

	return trees(kids)
}

// walkChildren walks the cause chain of err and reports the children
// along it, the ones binding and [NewErrorTree] both place under err. The
// chain follows each wrapper to the one error it wraps and each [*Error]
// to its cause. Every error nested with [WithErrors] in an Error on the
// way is a child, reported to onChild with the base of every Error from
// [Rebase] above it, the Error that nests it included. At an error that
// unwraps to several, the chain goes on as [followBranches] says. It
// follows the one branch that remains of a wrapper with several %w verbs
// and otherwise ends there, with each branch that remains a child under
// the same base. It also ends at a [*SourceError], which bound everything
// below it already, so onBinding receives it in place of its children.
func walkChildren(err error, onBinding func(*SourceError), onChild func(n error, base childBase)) {
	var base childBase

	for cur := err; !isNothing(cur); {
		switch x := cur.(type) { //nolint:errorlint // Walks the chain one node at a time.
		case *SourceError:
			onBinding(x)

			return

		case *Error:
			base = base.cross(x)

			for _, n := range x.errors {
				onChild(n, base)
			}

			cur = x.err

		case interface{ Unwrap() error }:
			cur = x.Unwrap()

		case interface{ Unwrap() []error }:
			branches, next := followBranches(cur, x.Unwrap())
			if next != nil {
				cur = next

				continue
			}

			for _, branch := range branches {
				onChild(branch, base)
			}

			return

		default:
			return
		}
	}
}

// followBranches returns the branches of err, an error that unwraps to
// branches, that stand below it in the tree. A wrapper that [fmt.Errorf]
// builds with several %w verbs, such as one from a sentinel and a cause,
// is one error that classifies another, so it keeps only the branches
// that carry a location or add errors below them. A branch with neither,
// such as the sentinel, adds nothing its text in the message of the
// wrapper does not show already. When the wrapper keeps a single branch,
// the second result is that branch, and the cause chain goes on through
// it as it does through a wrapper with one %w verb, so the wrapper binds
// where the branch does. Any other error that unwraps to several, such
// as a join or a multi-error of its own type, reports several errors, so
// it keeps each branch but the nil ones. So does a wrapper whose message
// reads as a join, as [joinBranches] finds one.
func followBranches(err error, branches []error) ([]error, error) {
	_, joined := joinBranches(err)
	wrapper := !joined && reflect.TypeOf(err) == wrapErrorsType

	var kept []error

	for _, branch := range branches {
		if isNothing(branch) || (wrapper && isLeaf(branch)) {
			continue
		}

		kept = append(kept, branch)
	}

	if wrapper && len(kept) == 1 {
		return nil, kept[0]
	}

	return kept, nil
}

// isLeaf reports whether err carries no location along its cause chain
// and has no children there, so it adds nothing to the tree beyond its
// text. It reads the chain as [anchorOf] and [walkChildren] do, in a
// single walk that ends at the first error that unwraps to several. Both
// of those walks call followBranches, and so isLeaf, at such an error.
// Running both, or walking on past a branch followBranches judged
// already, would judge the errors below twice per level, and the work
// would double with each level of nesting.
func isLeaf(err error) bool {
	for cur := err; !isNothing(cur); {
		switch x := cur.(type) { //nolint:errorlint // Walks the chain one node at a time.
		case *SourceError:
			return false

		case *Error:
			// An Error from Rebase anchors the chain at its base when
			// nothing below it carries a location.
			if x.hasLocation() || x.rebased || len(x.errors) > 0 {
				return false
			}

			cur = x.err

		case interface{ Unwrap() error }:
			cur = x.Unwrap()

		case interface{ Unwrap() []error }:
			// A branch followBranches keeps is a child, or the one branch
			// of a wrapper that is no leaf, so the error is a leaf exactly
			// when it keeps none.
			branches, next := followBranches(cur, x.Unwrap())

			return next == nil && len(branches) == 0

		default:
			return true
		}
	}

	return true
}

// childBase is the base the children along a cause chain rebase under:
// the base of every Error from [Rebase] above them, joined, and whether
// the walk met such an Error. A Rebase at the root still locates a child
// with no location at the root, so the children rebase whenever the walk
// met one, and only a chain that holds none leaves them as they are.
type childBase struct {
	path    paths.Path
	rebased bool
}

// cross returns the base below x: c joined with the base of x when x is
// an Error from [Rebase], and c as it is otherwise.
func (c childBase) cross(x *Error) childBase {
	if x.rebased {
		c.path = c.path.Join(x.base)
		c.rebased = true
	}

	return c
}

// rebase returns n rebased under the base, or n as it is when the walk met
// no Error from [Rebase].
func (c childBase) rebase(n error) error {
	if !c.rebased {
		return n
	}

	return Rebase(n, c.path)
}

// trees returns the nodes of kids in position order within the source
// each is bound to, with those whose location did not resolve after the
// rest in the order kids lists them. Returns nil when kids is empty.
func trees(kids []positioned) []ErrorTree {
	if len(kids) == 0 {
		return nil
	}

	groupSources(kids)
	slices.SortStableFunc(kids, comparePositioned)

	out := make([]ErrorTree, 0, len(kids))
	for _, kid := range kids {
		out = append(out, kid.tree)
	}

	return out
}

// boundChildren returns the nodes of the children of bound, each with its
// own children as a subtree. A child bound to another source carries its
// whole [SourceError.Error], which names that source, and so
// does a child bound to the same source when named is set, for a parent
// with no text of its own. Otherwise a child bound to the same source
// carries the "line:col:" its location resolved to in front of its
// message, without the name the parent gives already. A child that wraps
// a binding through Errors alone, which add no text, reads as that
// binding does. A child behind a wrapper that adds text of its own, such
// as [fmt.Errorf], carries the position inside that text, so it comes
// through as it is, name included. A child that binds a join has no
// text and gives its place to its branches, which keep the name of their
// source when named is set or when the join is bound to another source.
func boundChildren(bound *SourceError, named bool) []positioned {
	var kids []positioned

	for _, child := range bound.Errors() {
		// The branches sit beside the other children, so they sort by
		// position among them rather than under a node of their own.
		if _, joined := joinBranches(child.Unwrap()); joined {
			kids = append(kids, boundChildren(child, named || child.Source() != bound.Source())...)

			continue
		}

		kid := positioned{src: child.Source()}

		// A location the source does not hold resolved to nothing the
		// excerpt can mark, so the node reads as an unlocated one. A
		// located child reports the position its message carries, which
		// is inside the token its range marks when the caller gave the
		// error a position rather than a path.
		if _, ok := child.Range(); ok {
			kid.located = true
			kid.pos = child.loc.pos
		}

		// The binding put the position in front of the message it wraps,
		// so stripping it back to that message leaves the position to
		// put back without the name. A child that took a binding over
		// through Errors alone shows the text of that binding, so the
		// strip applies there. One behind a wrapper with text of its own
		// carries its position inside that text instead, and adds no
		// prefix of its own, so there is nothing to strip or put back.
		src := textBinding(child)

		text := src.Error()
		if !named && src.Source() == bound.Source() {
			if inner := src.Unwrap().Error(); inner != text {
				text = inner
				if kid.located {
					text = prefix(editorPosition(kid.pos)+":", text)
				}
			}
		}

		kid.tree = newTree(text, children(child))
		kids = append(kids, kid)
	}

	return kids
}

// textBinding returns the binding whose text e shows: the binding an
// adopted e reaches through the causes of Errors alone, which add no
// text of their own, or e itself.
func textBinding(e *SourceError) *SourceError {
	for e.adopted {
		x, ok := e.err.(*Error) //nolint:errorlint // The node itself, not a chain search.
		if !ok {
			break
		}

		inner := boundCause(x)
		if inner == nil {
			break
		}

		e = inner
	}

	return e
}

// positioned is a child of a node, the source it is bound to, and the
// position it resolved to, when located, for the order the children take.
// [groupSources] fills group in.
type positioned struct {
	src     *Source
	tree    ErrorTree
	pos     position.Position
	group   int
	located bool
}

// groupSources numbers the source of each of kids in the order the sources
// first appear, so children of one source stay together in the order kids
// lists them rather than interleaving with another source by position. A
// line number counts only in the source that holds it.
func groupSources(kids []positioned) {
	seen := make([]*Source, 0, 1)

	for i := range kids {
		group := slices.Index(seen, kids[i].src)
		if group < 0 {
			group = len(seen)
			seen = append(seen, kids[i].src)
		}

		kids[i].group = group
	}
}

// comparePositioned orders children by source group and then by position,
// with those that have no position after those that do, and equal among
// themselves so a stable sort keeps their order.
func comparePositioned(a, b positioned) int {
	switch {
	case !a.located && !b.located:
		return 0
	case !a.located:
		return 1
	case !b.located:
		return -1
	case a.group != b.group:
		return a.group - b.group
	case a.pos.Line != b.pos.Line:
		return a.pos.Line - b.pos.Line
	default:
		return a.pos.Col - b.pos.Col
	}
}

// newTree returns the node with text and children, less the nodes that add
// nothing. A child with no text gives its place to its own children, and a
// node with no text and a single child is that child.
func newTree(text string, children []ErrorTree) ErrorTree {
	if len(children) == 0 {
		return ErrorTree{Text: text}
	}

	flat := make([]ErrorTree, 0, len(children))

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

	if len(flat) == 0 {
		return ErrorTree{Text: text}
	}

	return ErrorTree{Text: text, Children: flat}
}
