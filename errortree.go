package niceyaml

import (
	"errors"
	"iter"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"go.jacobcolvin.com/niceyaml/internal/multierr"
	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// ErrorTree is the message of an error laid out as a tree, with one node
// per error, for a renderer that draws the errors below an error as
// branches. The root holds the message of the error itself, and each
// problem it heads and each of its details is a child. [FormatError]
// renders one as plain text and
// [go.jacobcolvin.com/niceyaml/printer.Printer.PrintError] with styles,
// and a renderer of its own, such as a viewer that lists violations,
// reads the same tree so it never disagrees with them about which error
// a line belongs to.
//
// Each node also holds the error it stands for and the binding that
// resolved its location. A report a program reads, such as a JSON line,
// a CI annotation, or an editor diagnostic, takes its fields from the
// node rather than from the label. It reads [ErrorTree.Message] and
// [ErrorTree.Path] from the node, and [ErrorTree.Source] and
// [ErrorTree.Position] from its binding. [Problems] yields the nodes
// such a report lists, one per problem, and [ErrorTree.All] yields every
// node of the tree. A node with Detail set explains its parent, and a
// renderer that draws details apart from problems reads the mark.
// [ErrorTree.IsInvalid] reports whether the document is at fault for the
// problems of a node.
//
// Create instances with [NewErrorTree].
type ErrorTree struct {
	// Err is the error the node stands for, for [errors.Is] and
	// [errors.As]. It is nil for a node with no text.
	Err error
	// Bound is the binding that gives the node its source and location:
	// the [*SourceError] that Err is, or the one Err wraps along its cause
	// chain. It is nil for an error bound to no source.
	Bound *SourceError
	// Label is the row a renderer draws for the node: the message of the
	// error without the errors below it, behind the location the row
	// shows, in a form relative to the row of the parent. The root of a
	// binding reads "name:line:col: $.path: msg", a child bound to the
	// same source drops the name and reads "line:col: $.path: msg", and a
	// child in another document names that document. It is empty for a
	// node that stands for several errors and adds no message of its own,
	// such as one built from [errors.Join]. [ErrorTree.Message] returns
	// the message alone, the field a report carries.
	Label string
	// Children are the nodes of the errors below the node, in the order
	// the tree shows them: the problems a heading stands over, and the
	// details that explain the node, which have Detail set.
	Children []ErrorTree
	// Detail reports whether the node explains its parent, as an error
	// from [WithDetails] does, rather than standing for a problem the
	// parent heads.
	Detail bool
	// Warning reports whether the problem of the node is a warning, as
	// [NewWarning] and [Node.Warn] mark one, rather than an error. A
	// heading sets it when every problem below it is a warning. A type that
	// [errors.As] finds in Err tells what a problem is about, never its
	// severity, so a renderer reads Warning.
	Warning bool
}

// NewErrorTree creates a new [ErrorTree] from err. The root holds the
// message of err without the errors below it, so the context a wrapper
// added stays in front and each error shows once. Each problem err heads
// and each of its details is a child, and a child with children of its
// own is a subtree. A nil err yields the zero ErrorTree.
//
// The root of a binding keeps the "name:line:col:" position
// [SourceError.Error] gives it. Each child reads "line:col:" without the
// name, or names its document when it is bound to another document than
// its parent, as "document 2: msg". The children of a binding sort by
// position within their source, with the sources in the order they first
// appear and the children whose location did not resolve last. Outside a
// binding, children keep the order the error lists them in. The node of
// an error bound to no source puts the path of its [*Error] in front of
// the message, as "@.path: msg".
//
// An error that unwraps to several and whose message is theirs one per
// line, such as one from [errors.Join], is a node with no text, and its
// children take its place in the tree above it. Any other error that
// unwraps to several keeps its text and has a child per branch. A wrapper
// that [fmt.Errorf] builds with several %w verbs is the exception. It has
// a child for each branch that is a problem and none for a sentinel, as
// the Errors section of the package documentation defines them. With one
// such branch, the wrapper reads as a wrapper with one %w verb around
// that branch.
//
// Each node holds its error in Err and its binding in Bound. The node of
// a wrapper around a binding holds the wrapper in Err and the binding in
// Bound. A located [*Error] above a binding names a location no source
// resolved, so its node has no Bound.
func NewErrorTree(err error) ErrorTree {
	return newTree(ErrorTree{}, appendTrees(nil, err))
}

// All returns an iterator over the nodes of the tree, t first and each
// node before the nodes below it, in the order [FormatError] prints their
// rows. A node with no text has no row, so All passes over it and yields
// the nodes below it. The zero ErrorTree therefore yields nothing.
//
// Each node comes with its Children, and All yields those children after
// it, so a caller reads one row per node or the whole subtree of a node:
//
//	for node := range niceyaml.NewErrorTree(err).All() {
//		if errors.Is(node.Err, fs.ErrNotExist) {
//			...
//		}
//	}
func (t ErrorTree) All() iter.Seq[ErrorTree] {
	return func(yield func(ErrorTree) bool) {
		for node := range t.nodes() {
			if node.Label != "" && !yield(node) {
				return
			}
		}
	}
}

// Problems returns an iterator over the nodes that each stand for one
// problem, in the order [ErrorTree.All] yields them. It is the walk of a
// report that lists the problems of an error as rows, such as JSON lines,
// CI annotations, or editor diagnostics. A heading, which is a summary
// from [NewSummary], a join, any other error that unwraps to several, or
// a wrapper around one, yields no node of its own, and the walk goes on to
// the problems below it. A wrapper that [fmt.Errorf] builds with several
// %w verbs is a heading only when it heads several problems, as
// [NewErrorTree] describes. Every other node with text is one problem, and
// Problems yields it with exactly its details as its Children, each with
// Detail set. A node yields whether or not it carries a location, so an
// error bound to no source, such as a file that did not read, yields a row
// too. The rows match the lines [SourceError.Error] lists.
//
// A row reads [ErrorTree.Message] and [ErrorTree.Path], which answer for
// every node, and [ErrorTree.Source] and [ErrorTree.Position], which
// return zero values for a node bound to no source. [errors.Is] and
// [errors.As] on Err search the problem and pass over its details:
//
//	for problem := range niceyaml.Problems(err) {
//		row := Row{Message: problem.Message(), Invalid: problem.IsInvalid()}
//		if rule, ok := errors.AsType[*RuleError](problem.Err); ok {
//			row.Rule = rule.ID
//		}
//
//		report(row)
//	}
func (t ErrorTree) Problems() iter.Seq[ErrorTree] {
	return func(yield func(ErrorTree) bool) {
		t.problems(yield)
	}
}

// Problems returns an iterator over the nodes of the [ErrorTree] of err
// that each stand for one problem, as [ErrorTree.Problems] yields them
// for the tree [NewErrorTree] builds. It is the walk of a report that
// lists the problems of an error as rows:
//
//	for problem := range niceyaml.Problems(err) {
//		path, _ := problem.Path()
//		pos, _ := problem.Position()
//		emit(problem.Source().Name(), pos, problem.Message(), path)
//	}
//
// A nil err has no problems.
func Problems(err error) iter.Seq[ErrorTree] {
	return NewErrorTree(err).Problems()
}

// problems yields the nodes of t that [ErrorTree.Problems] yields and
// reports whether the caller wants more.
func (t ErrorTree) problems(yield func(ErrorTree) bool) bool {
	if t.isProblem() {
		return yield(t)
	}

	for _, child := range t.Children {
		if !child.Detail && !child.problems(yield) {
			return false
		}
	}

	return true
}

// isProblem reports whether t stands for one problem: a node with text
// that heads no separate problems.
func (t ErrorTree) isProblem() bool {
	return t.Label != "" && !t.heads()
}

// heads reports whether t stands over separate problems: whether a child
// of t is no detail. A node with no text stands for several errors, so
// its children are all problems it heads.
func (t ErrorTree) heads() bool {
	return slices.ContainsFunc(t.Children, func(c ErrorTree) bool { return !c.Detail })
}

// IsInvalid reports whether the document is at fault for the problems at
// or below the node, the ones [ErrorTree.Problems] yields for it, as the
// function [IsInvalid] describes. It decides from the errors among them
// and passes over the warnings. A node whose every problem is a warning
// answers for the warnings, so the row of a deprecation reports true. A
// node that yields no problem, such as the zero ErrorTree, is not
// invalid. A heading answers through the problems it heads, whatever its
// own error declares, and the details of a problem decide nothing. A node
// with Detail set answers for its own error, and its answer changes
// nothing for the node it explains. The method reads Err, so a node built
// by hand with no Err is not invalid.
func (t ErrorTree) IsInvalid() bool {
	var errs, warns int

	errsInvalid, warnsInvalid := true, true

	for problem := range t.Problems() {
		invalid := declaresInvalid(problem.Err)

		if isWarning(problem.Err) {
			warns++
			warnsInvalid = warnsInvalid && invalid

			continue
		}

		errs++
		errsInvalid = errsInvalid && invalid
	}

	if errs > 0 {
		return errsInvalid
	}

	return warns > 0 && warnsInvalid
}

// declaresInvalid reports whether an error on the cause chain of err
// matches [errInvalid] through an Is method of its own. The walk skips
// details, which decide nothing, and stops at a [*PanicError]. An error
// with several branches declares nothing when a branch holds the error of
// a context that ended. Such a branch reports that the call stopped, not
// a fault of the document, and [Invalid] declares nothing for it either.
func declaresInvalid(err error) bool {
	for cur := err; !isNothing(cur); {
		if x, ok := cur.(interface{ Is(target error) bool }); ok && x.Is(errInvalid) {
			return true
		}

		switch x := cur.(type) { //nolint:errorlint // Walks the chain one node at a time.
		case *Error:
			cur = x.err

		case *PanicError:
			return false

		case interface{ Unwrap() error }:
			cur = x.Unwrap()

		case interface{ Unwrap() []error }:
			branches := x.Unwrap()
			if slices.ContainsFunc(branches, contextEnded) {
				return false
			}

			return slices.ContainsFunc(branches, declaresInvalid)

		default:
			return false
		}
	}

	return false
}

// Message returns the message of the node with no position, document, or
// path in front, the field a report carries beside the position and the
// path. Label holds the same message behind the location the row of the
// node shows, in a form that depends on where the node sits in the tree.
//
//   - For a node with a binding, it is the [SourceError.Message] of
//     Bound.
//   - For a node bound to no source that names a path, as
//     [ErrorTree.Path] reports, it is the message of Err without the path
//     Label puts in front of it. A binding of Err would report the same
//     message.
//   - For any other node, it is Label.
//
// A node with no label of its own, such as the root of a join, has an
// empty message. A detail answers like any other node, so a report that
// lists the related locations of a problem reads the message of each
// detail.
//
// The node of a wrapper around a binding, such as one from [fmt.Errorf],
// holds that binding in Bound, so its message is the message of the
// binding. The text the wrapper added stays in Label.
func (t ErrorTree) Message() string {
	if t.Bound != nil {
		return t.Bound.Message()
	}

	if _, ok := t.Path(); !ok {
		return t.Label
	}

	// The part of the message the children show depends on the end of the
	// cause chain, so the walk finds that end alone.
	end := walkChildren(t.Err, func(*SourceError) {}, func(error, childBase, bool) {})

	return bareMessage(t.Err, end)
}

// Path returns the [paths.Path] the node is about and true, or the zero
// Path and false when the node names none.
//
//   - For a node with a binding, it is the [SourceError.Path] of Bound.
//   - For a node bound to no source, it is the path of the first located
//     [*Error] along the cause chain of Err, with the base of every
//     [Rebase] on the way joined in front. The label of the node puts the
//     same path in front of the message.
//
// The source and the position come from Bound, which resolved them, and
// [ErrorTree.Source] and [ErrorTree.Position] read them for any node. A
// position that an [*Error] bound to no source carries points into no
// source, so the node leaves it to [Error.Position].
func (t ErrorTree) Path() (paths.Path, bool) {
	if t.Bound != nil {
		return t.Bound.Path()
	}

	a := anchorOf(t.Err)

	return a.path, a.hasPath
}

// Source returns the [*Source] the node is bound to, as [SourceError.Source]
// returns it for Bound, or nil for a node bound to no source.
func (t ErrorTree) Source() *Source {
	return t.Bound.Source()
}

// Position returns the [position.Position] the location of the node
// resolved to and true, as [SourceError.Position] returns it for Bound. It
// returns the zero Position and false for a node bound to no source, and
// for one whose location did not resolve.
func (t ErrorTree) Position() (position.Position, bool) {
	return t.Bound.Position()
}

// Range returns the [position.Range] the location of the node resolved to
// and true, as [SourceError.Range] returns it for Bound. It returns the
// zero Range and false for a node bound to no source, and for one whose
// location did not resolve.
func (t ErrorTree) Range() (position.Range, bool) {
	return t.Bound.Range()
}

// DocumentIndex returns the 0-indexed position within the file of the
// document the node is bound to and true, as [SourceError.DocumentIndex]
// returns it for Bound. It returns 0 and false for a node bound to no
// source, and for a binding that holds no document.
func (t ErrorTree) DocumentIndex() (int, bool) {
	return t.Bound.DocumentIndex()
}

// bindingOf returns the binding err is or wraps along its cause chain, or
// nil. A located [*Error] above a binding anchors the chain itself, so
// such an error has no binding.
func bindingOf(err error) *SourceError {
	if bound, ok := anchorOf(err).err.(*SourceError); ok { //nolint:errorlint // The anchor itself, found by the walk.
		return bound
	}

	return nil
}

// appendTrees appends the nodes err stands for to dst. A join appends its
// branches straight to dst, because a loop of [errors.Join] calls nests
// each join in the next, and copying each level up would be quadratic.
func appendTrees(dst []ErrorTree, err error) []ErrorTree {
	if isNothing(err) {
		return dst
	}

	if branches, base, ok := joinBranches(err); ok {
		for _, branch := range branches {
			dst = appendTrees(dst, base.rebase(branch, false))
		}

		return dst
	}

	bound := joinBinding(err)
	if bound != nil {
		for _, t := range trees(appendBoundChildren(nil, bound, true)) {
			dst = appendTree(dst, t)
		}

		return dst
	}

	kids, end := children(err)
	node := ErrorTree{Err: err, Bound: bindingOf(err)}

	x, binds := err.(*SourceError) //nolint:errorlint // The node itself, not a chain search.
	if binds {
		node.Label = x.headline()
	} else {
		node.Label, _, _, _ = end.cut(err.Error())
		node.Label = unboundText(err, node.Label)
	}

	t := newTree(node, kids)

	// A binding writes the label of a warning into its own text, so only a
	// problem that no binding holds gets it here.
	if !binds && node.Label != "" && t.Warning && t.isProblem() && !holdsBinding(err) {
		t.Label = prefix("warning:", t.Label)
	}

	return appendTree(dst, t)
}

// holdsBinding reports whether a [*SourceError] lies on the cause chain of
// err, whose text then holds the text of the binding.
func holdsBinding(err error) bool {
	for cur := err; !isNothing(cur); {
		switch x := cur.(type) { //nolint:errorlint // Walks the chain one node at a time.
		case *SourceError:
			return true

		case *Error:
			cur = x.err

		case interface{ Unwrap() error }:
			cur = x.Unwrap()

		default:
			return false
		}
	}

	return false
}

// unboundText puts the path of the first located [*Error] on the cause
// chain of err in front of text, as a binding would. A [*SourceError]
// earlier on the chain names its own path, so text then stays as it is.
func unboundText(err error, text string) string {
	if a := anchorOf(err); a.hasPath {
		return withPath(a.path, text)
	}

	return text
}

// appendTree appends t to dst and returns the extended slice. A node with
// no text adds nothing, so its children take its place.
func appendTree(dst []ErrorTree, t ErrorTree) []ErrorTree {
	if t.Label == "" {
		return append(dst, t.Children...)
	}

	return append(dst, t)
}

// joinBinding returns the binding of a join that err reads as, or nil.
// An Error that adds nothing reads as the error it wraps, and so does a
// binding that adopted one, since neither adds text or children.
func joinBinding(err error) *SourceError {
	for {
		switch x := err.(type) { //nolint:errorlint // Walks the chain one node at a time.
		case *Error:
			if !x.addsNothing() {
				return nil
			}

			err = x.err

		case *SourceError:
			if x == nil {
				return nil
			}

			if _, _, joined := joinBranches(x.err); joined {
				return x
			}

			if !x.adopted {
				return nil
			}

			err = x.err

		default:
			return nil
		}
	}
}

// joinBranches returns the branches of err and the base [peel] finds when
// err is a join, and false otherwise. A join is an [errors.Join] error or
// any error whose message is its branches' messages one per line.
func joinBranches(err error) ([]error, childBase, bool) {
	base, err := peel(err)

	// An Error left after the peel is one node of the tree.
	if _, ok := err.(*Error); ok { //nolint:errorlint // The node itself, not a chain search.
		return nil, childBase{}, false
	}

	joined, ok := err.(interface{ Unwrap() []error }) //nolint:errorlint // The node itself, not a chain search.
	if !ok {
		return nil, childBase{}, false
	}

	branches := joined.Unwrap()
	if !multierr.IsJoin(err) && !multierr.JoinMessage(err.Error(), branches) {
		return nil, childBase{}, false
	}

	return branches, base, true
}

// peel strips the [*Error]s on top of err that add nothing and returns
// what remains, with the base the errors below them take. The base takes
// the fault the stripped Errors declare, as [declaration.under] passes it
// down.
func peel(err error) (childBase, error) {
	var base childBase

	for {
		x, ok := err.(*Error) //nolint:errorlint // Walks the chain one node at a time.
		if !ok || !x.addsNothing() {
			return base, err
		}

		base.fault = base.fault.under(x)
		err = x.err
	}
}

// children returns the nodes of the children [walkChildren] finds on the
// cause chain of err, and the [chainEnd] of that chain. The children of a
// binding with no line of its own name their source, as no row above does.
func children(err error) ([]ErrorTree, chainEnd) {
	var kids []positioned

	end := walkChildren(err,
		func(x *SourceError) { kids = appendBoundChildren(kids, x, x.headline() == "") },
		func(n error, base childBase, detail bool) {
			tree := NewErrorTree(base.rebase(n, detail))
			if detail {
				tree = asDetail(tree)
			}

			kids = append(kids, positioned{tree: tree})
		},
	)

	return trees(kids), end
}

// asDetail returns t with Detail set. A node with no text gives its place
// to its children in the tree above it, so each of those children gets
// Detail set instead.
func asDetail(t ErrorTree) ErrorTree {
	if t.Label != "" {
		t.Detail = true

		return t
	}

	t.Children = slices.Clone(t.Children)
	for i := range t.Children {
		t.Children[i].Detail = true
	}

	return t
}

// chainEnd is the error that ends a cause chain as [walkChildren] walks
// it, when the errors below it are children: a [*SourceError], or an error
// whose branches the walk reported. The zero chainEnd ends at neither.
type chainEnd struct {
	err error
	// The number of branches the walk reported as children.
	branches int
	// Whether an error on the way to err added text of its own, so the
	// message the walk started at differs from the message of err.
	wrapped bool
}

// cut returns text, the message of the error the chain belongs to, without
// the part the children show. The counts are the trailing children whose
// text the result holds, then those whose text the input held. It returns
// text whole and false when a wrapper rewrote the message it wraps.
func (c chainEnd) cut(text string) (string, int, int, bool) {
	var (
		full, own   string
		shown, held int
	)

	switch x := c.err.(type) { //nolint:errorlint // The end of the chain, found by the walk.
	case nil:
		return text, 0, 0, true

	case *SourceError:
		t := x.texts()
		full, own, shown, held = t.msg, t.head, t.shown, t.shown

	case interface{ Unwrap() []error }:
		msg := text
		if c.wrapped {
			msg = c.err.Error()
		}

		branches := x.Unwrap()

		switch {
		case multierr.IsJoin(c.err) || multierr.JoinMessage(msg, branches):
			full, held = msg, c.branches

		case multierr.IsWrapErrors(c.err) || multierr.HoldsFirst(msg, branches):
			return text, c.branches, c.branches, true

		default:
			return text, 0, 0, true
		}
	}

	if full == own {
		return text, shown, held, true
	}

	before, after, ok := strings.Cut(text, full)
	if !ok {
		return text, 0, 0, false
	}

	return strings.TrimRightFunc(before+own+after, unicode.IsSpace), shown, held, true
}

// walkChildren reports to onChild the children that a binding and
// [NewErrorTree] both place under err. A [*SourceError] bound its children
// already, so it ends the walk and goes to onBinding in their place.
func walkChildren(
	err error,
	onBinding func(*SourceError),
	onChild func(n error, base childBase, detail bool),
) chainEnd {
	var (
		base    childBase
		wrapped bool
	)

	for cur := err; !isNothing(cur); {
		switch x := cur.(type) { //nolint:errorlint // Walks the chain one node at a time.
		case *SourceError:
			onBinding(x)

			return chainEnd{err: x, wrapped: wrapped}

		case *Error:
			base = base.cross(x)

			for _, n := range x.errors {
				onChild(n, base, false)
			}

			for _, n := range x.details {
				onChild(n, base, true)
			}

			// An Error that adds nothing has the message of its cause.
			wrapped = wrapped || !x.addsNothing()
			cur = x.err

		case interface{ Unwrap() error }:
			wrapped = true
			cur = x.Unwrap()

		case interface{ Unwrap() []error }:
			branches, next := followBranches(cur, x.Unwrap())
			if next != nil {
				wrapped = true
				cur = next

				continue
			}

			if len(branches) == 0 {
				return chainEnd{}
			}

			for _, branch := range branches {
				onChild(branch, base, false)
			}

			return chainEnd{err: cur, branches: len(branches), wrapped: wrapped}

		default:
			return chainEnd{}
		}
	}

	return chainEnd{}
}

// followBranches returns the branches of err that stand below it. For a
// [fmt.Errorf] wrapper with several %w verbs, it drops each branch that
// [isLeaf] reports as a sentinel, whose text the wrapper shows. It
// returns a lone kept branch as the next link.
func followBranches(err error, branches []error) ([]error, error) {
	// The type check runs first, so an error of any other type never
	// formats its message here, where the result would go unused.
	wrapper := multierr.IsWrapErrors(err) && !multierr.JoinMessage(err.Error(), branches)

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

// isLeaf reports whether err reads as a sentinel rather than a problem
// when it is a branch of a wrapper with several %w verbs. A sentinel
// carries no location and no children on its cause chain, as [anchorOf]
// and [walkChildren] find them. It holds no [*Error] whose fault or
// severity is set either, because callers build such Errors with
// [NewError], [NewWarning], [Invalid], and [Unchecked] to report
// problems. A rare sentinel built with one of them reads as a problem. An
// error from [errors.New] reads as a sentinel too. The function walks the
// chain once, because running anchorOf and walkChildren both doubles the
// work at each nesting level.
func isLeaf(err error) bool {
	for cur := err; !isNothing(cur); {
		switch x := cur.(type) { //nolint:errorlint // Walks the chain one node at a time.
		case *SourceError:
			return false

		case *Error:
			// An Error from Rebase that points at its base anchors the
			// chain there when nothing below it carries a location.
			if x.hasLocation() || (x.move != nil && !x.move.movesOnly) || x.nests() ||
				x.fault != undeclared || x.severity != severityNone {
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

// childBase is the base the children on a cause chain rebase under. Even a
// Rebase at the root locates a problem with no location, so a chain rebases
// its children whenever it holds a Rebase.
type childBase struct {
	// The move of every Error from Rebase above the children, joined, or
	// nil when the walk met none.
	move *move
	// The fault the Errors above the children declare, as
	// [declaration.under] passes it down.
	fault declaration
}

// cross returns the base below x: c with the move of x after its own, as
// [move.then] joins them, and with the fault x passes down, as
// [declaration.under] gives it.
func (c childBase) cross(x *Error) childBase {
	return childBase{move: c.move.then(x.move), fault: c.fault.under(x)}
}

// rebase returns n under the base. A detail explains the error above it,
// so the base moves its paths alone and never marks it invalid. A problem
// below an Error that matches [errInvalid] is the document's fault too,
// unless the fault is one a hook presumes and n holds an Error from
// [Unchecked].
func (c childBase) rebase(n error, detail bool) error {
	d := c.fault
	if detail {
		d = undeclared
	}

	if c.move == nil {
		return markFault(n, d)
	}

	m := *c.move
	m.movesOnly = m.movesOnly || detail

	return rebase(n, m, d)
}

// trees returns the nodes of kids in position order within the source
// each is bound to, with those whose location did not resolve after the
// rest in the order kids lists them. Returns nil when kids is empty.
func trees(kids []positioned) []ErrorTree {
	if len(kids) == 0 {
		return nil
	}

	kids = arrange(kids)

	out := make([]ErrorTree, 0, len(kids))
	for i := range kids {
		out = append(out, kids[i].tree)
	}

	return out
}

// appendBoundChildren appends the nodes of the children of bound to kids.
// A child in the source of its parent drops the name the parent row shows
// and reads "line:col: msg", unless named is set for a parent with no row.
func appendBoundChildren(kids []positioned, bound *SourceError, named bool) []positioned {
	from := len(kids)
	kids = flatten(kids, bound.below, bound, named)

	for i := from; i < len(kids); i++ {
		kid := &kids[i]
		child, parent := kid.bound, kid.parent

		// Strip the line back to the message and put the position back
		// without the name. A child behind a wrapper with text of its own
		// carries its position in that text, so there is nothing to strip.
		src := textBinding(child)

		text := src.headline()
		if !kid.named && src.Source() == parent.Source() {
			if inner := src.texts().own; inner != text {
				text = inner

				switch {
				case kid.located:
					text = prefix(editorPosition(kid.pos)+":", text)

				case src.node.Document() != parent.node.Document():
					// The parent names another document of the source, or
					// none, so the child keeps the one it wrote.
					text = prefix(suffixed(src.documentLabel()), text)
				}
			}
		}

		below, _ := children(child)

		kid.tree = newTree(ErrorTree{Err: child, Bound: child, Label: text}, below)
		if kid.detail {
			kid.tree = asDetail(kid.tree)
		}

		kid.tree.Detail = kid.tree.Detail || kid.underDetail
	}

	return kids
}

// flatten appends children, the children of the binding parent, to kids.
// A child that binds a join has no line of its own, so the children of the
// join take its place, with the join as their parent.
func flatten(kids []positioned, children []boundChild, parent *SourceError, named bool) []positioned {
	for _, c := range children {
		// The branches sit beside the other children, so they sort by
		// position among them rather than under a node of their own.
		joined := joinBinding(c.bound)
		if joined != nil {
			from := len(kids)
			kids = flatten(kids, joined.below, joined, named || joined.Source() != parent.Source())

			for i := from; c.detail && i < len(kids); i++ {
				kids[i].underDetail = true
			}

			continue
		}

		kid := placed(c.bound)
		kid.parent, kid.named, kid.detail = parent, named, c.detail
		kids = append(kids, kid)
	}

	return kids
}

// textBinding returns the binding an adopted e reaches through Errors
// alone, which add no text, or e itself. A binding that writes its source
// name in front of its message adds text, so the walk stops there.
func textBinding(e *SourceError) *SourceError {
	for e.adopted && e.keepsMessage() {
		x, ok := e.err.(*Error) //nolint:errorlint // The node itself, not a chain search.
		if !ok {
			break
		}

		inner, ok := x.textCause().(*SourceError) //nolint:errorlint // The node itself, not a chain search.
		if !ok || inner == nil {
			break
		}

		e = inner
	}

	return e
}

// positioned is a child node with the source and the position, when
// located, that [arrange] sorts it by. [arrange] fills in group.
type positioned struct {
	src *Source
	// The binding of the child, or nil for an error bound to no source.
	bound *SourceError
	// The binding whose children hold the child, as [flatten] finds it.
	parent *SourceError
	tree   ErrorTree
	pos    position.Position
	group  int
	// The child names its source in its line, as [flatten] decides.
	named   bool
	located bool
	// The child is a detail of the binding that holds it.
	detail bool
	// A detail that binds a join holds the child.
	underDetail bool
}

// placed returns child with the source and the position [trees] orders it
// by. Only a resolved range counts as located, since the excerpt marks
// nothing else, and the position is the one the message of child carries.
func placed(child *SourceError) positioned {
	kid := positioned{src: child.Source(), bound: child}

	if _, ok := child.Range(); ok {
		kid.located = true
		kid.pos = child.loc.pos
	}

	return kid
}

// arrange sorts kids in place by source, in the order the sources first
// appear, and then by position, since a line number means something only
// within its own source.
func arrange(kids []positioned) []positioned {
	seen := make([]*Source, 0, 1)

	for i := range kids {
		group := slices.Index(seen, kids[i].src)
		if group < 0 {
			group = len(seen)
			seen = append(seen, kids[i].src)
		}

		kids[i].group = group
	}

	slices.SortStableFunc(kids, comparePositioned)

	return kids
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

// newTree returns node with children below it, less the nodes that add
// nothing. A child with no text gives its place to its own children, and a
// node with no text and a single child is that child.
func newTree(node ErrorTree, children []ErrorTree) ErrorTree {
	if len(children) == 0 {
		node.Warning = node.Err != nil && isWarning(node.Err)

		return node
	}

	flat := make([]ErrorTree, 0, len(children))

	for _, child := range children {
		flat = appendTree(flat, child)
	}

	if node.Label == "" && len(flat) == 1 {
		return flat[0]
	}

	if len(flat) > 0 {
		node.Children = flat
	}

	node.Warning = treeWarning(node)

	return node
}

// treeWarning reports whether t is a warning: a heading whose every
// problem is a warning, or a problem whose error is one. The details of a
// problem decide nothing.
func treeWarning(t ErrorTree) bool {
	if !t.heads() {
		return t.Err != nil && isWarning(t.Err)
	}

	for _, child := range t.Children {
		if !child.Detail && !child.Warning {
			return false
		}
	}

	return true
}

// treeCut cuts an [ErrorTree] down to its first problems, as
// [limitTree] returns one. It holds how many more problems the walk may
// keep, and every node the walk left out.
type treeCut struct {
	left   []ErrorTree
	budget int
}

// limitTree returns t with its first limit problems and a last root child
// that counts the rest, as "and 490 more", along with the problems it
// left out. A heading stays while any problem below it does.
func limitTree(t ErrorTree, limit int) (ErrorTree, []ErrorTree) {
	c := &treeCut{budget: limit}

	cut, _ := c.cut(t)
	if len(c.left) == 0 {
		return t, nil
	}

	cut.Children = append(cut.Children, ErrorTree{Label: "and " + strconv.Itoa(len(c.left)) + " more"})

	return cut, c.left
}

// skippedBindings returns the bindings of left, the problems [limitTree]
// left out of cut, so an excerpt marks only the problems the tree shows.
// It leaves out any binding a node of cut still reaches.
func skippedBindings(cut ErrorTree, left []ErrorTree) map[*SourceError]bool {
	if len(left) == 0 {
		return nil
	}

	kept := make(map[*SourceError]bool)
	every := func(*SourceError) bool { return true }

	// A heading holds the binding that lists its problems, so its node
	// keeps that binding alone, and every other node keeps the bindings
	// below its own too, which are its details.
	for node := range cut.nodes() {
		switch {
		case node.Bound == nil:
		case node.heads():
			kept[node.Bound] = true
		default:
			node.Bound.all(kept, every)
		}
	}

	skipped := make(map[*SourceError]bool)

	for _, problem := range left {
		for node := range problem.nodes() {
			node.Bound.all(skipped, every)
		}
	}

	for bound := range kept {
		delete(skipped, bound)
	}

	return skipped
}

// cut returns t without the problems past the budget, and whether any of
// t remains. A heading keeps its details and goes only when none of its
// problems stays.
func (c *treeCut) cut(t ErrorTree) (ErrorTree, bool) {
	if t.isProblem() {
		if c.budget == 0 {
			c.left = append(c.left, t)

			return ErrorTree{}, false
		}

		c.budget--

		return t, true
	}

	out := t
	out.Children = make([]ErrorTree, 0, len(t.Children))

	stays := false

	for _, child := range t.Children {
		if child.Detail {
			out.Children = append(out.Children, child)

			continue
		}

		if kept, ok := c.cut(child); ok {
			out.Children = append(out.Children, kept)
			stays = true
		}
	}

	return out, stays
}

// nodes returns an iterator over t and every node below it, each before
// the nodes below it, whether or not it has text.
func (t ErrorTree) nodes() iter.Seq[ErrorTree] {
	return func(yield func(ErrorTree) bool) {
		t.eachNode(yield)
	}
}

// eachNode yields t, then every node below it, and reports whether the
// caller wants more.
func (t ErrorTree) eachNode(yield func(ErrorTree) bool) bool {
	if !yield(t) {
		return false
	}

	for _, child := range t.Children {
		if !child.eachNode(yield) {
			return false
		}
	}

	return true
}

// ErrorReport holds the parts a renderer draws for an error: the tree of
// its messages, one excerpt per source its bindings touch, and the
// bindings that get a reason in place of an excerpt. [FormatError] draws
// a report as plain text and
// [go.jacobcolvin.com/niceyaml/printer.Printer.PrintError] draws one with
// styles. A renderer of its own, such as one that writes HTML, draws the
// same parts:
//
//	rep := niceyaml.NewErrorReport(err)
//
//	drawTree(rep.Tree)
//
//	for _, excerpt := range rep.Excerpts {
//		drawView(excerpt.View)
//	}
//
// The report holds every text as the error or the source wrote it. A
// message from the program's own unmarshaler or validator, or the name of
// a source, can hold a control character, so a renderer escapes each
// message and each name for its output.
// [go.jacobcolvin.com/niceyaml/escape.Control] draws a line feed as its
// picture too, so a renderer that keeps the rows of a message escapes
// each row on its own.
//
// Create instances with [NewErrorReport].
type ErrorReport struct {
	// Excerpts holds one excerpt per source the bindings of the error
	// touch, in the order the bindings reach the sources, with each
	// binding [Bindings] yields and the bindings below it before the next.
	// A source none of whose locations resolve has none, and neither has
	// a source with excerpts off, as [WithExcerpts] sets it.
	Excerpts []ErrorExcerpt
	// Unresolved holds the bindings that get a reason in place of an
	// excerpt, in the order [Bindings] yields them. Each is a binding
	// Bindings finds whose tree resolves no location and whose own
	// location did not resolve, for the reason [SourceError.Unresolved]
	// returns. A binding that carries no location of its own has nothing
	// to explain and is not among them. Neither is a binding whose reason
	// wraps [paths.ErrNoDocument], since no path resolves in a document
	// with no content and the tree names the path already. A binding the
	// error reaches twice comes twice, as Bindings yields it.
	Unresolved []*SourceError
	// Tree is the [ErrorTree] of the error. Under one heading, a warning
	// whose message and details match those of an earlier warning keeps
	// its row and loses its details, so fifteen files with one deprecated
	// version print fifteen rows and one set of details. Such a warning
	// marks nothing in the excerpts either. Every other node is the node
	// that [NewErrorTree] builds, which keeps every detail. The tree has
	// no row for an error that holds nothing, such as a bound join of
	// typed-nil errors. When the report then holds no excerpt and no
	// unresolved binding either, Tree is one node with the message of the
	// error as its Label and no Err, so a renderer draws that message in
	// place of nothing.
	Tree ErrorTree
}

// ErrorExcerpt is the excerpt of one source in an [ErrorReport].
type ErrorExcerpt struct {
	// Source is the source the excerpt shows.
	Source *Source
	// View holds the lines of Source around the locations the error marks
	// in it, cut and clipped as [SourceError.Excerpt] cuts a view.
	View *line.View
	// Named reports whether the name of Source leads the excerpt on a row
	// of its own, so a reader tells the excerpts of several sources apart.
	// It is set when the bindings of the error touch more than one source
	// and Source has a name. A source counts whether or not it has an
	// excerpt, so the one excerpt of a report has Named set when the error
	// also touches a source where no location resolved.
	Named bool
}

// NewErrorReport creates a new [ErrorReport] from err. Each excerpt keeps
// the context lines and the width the options set, as [ExcerptOption]
// describes. A nil err yields the zero ErrorReport.
func NewErrorReport(err error, opts ...ExcerptOption) ErrorReport {
	return newErrorReport(err, newExcerptConfig(opts), 0)
}

// newErrorReport builds the report [NewErrorReport] documents. A limit
// above zero cuts the tree to that many problems, and the report then
// ignores the bindings of the rest, so it shows what the tree shows.
func newErrorReport(err error, cfg excerptConfig, limit int) ErrorReport {
	if err == nil {
		return ErrorReport{}
	}

	// A repeated warning still names its source, so the excerpts of a
	// report that reaches several sources keep their names.
	quiet := make(map[*SourceError]bool)
	tree := dropRepeats(NewErrorTree(err), quiet)

	var skipped map[*SourceError]bool

	if limit > 0 {
		var left []ErrorTree

		tree, left = limitTree(tree, limit)
		skipped = skippedBindings(tree, left)
	}

	bindings := slices.Collect(Bindings(err))

	// The own location of a binding carries its message only among
	// several bindings, where the message tells the carets of one excerpt
	// apart.
	sources, positions := treePositions(bindings, len(bindings) > 1, skipped, quiet)

	var rep ErrorReport

	for _, src := range sources {
		view, ok := src.excerpt(positions[src], cfg)
		if !ok {
			continue
		}

		rep.Excerpts = append(rep.Excerpts, ErrorExcerpt{
			Source: src,
			View:   view,
			Named:  len(sources) > 1 && src.Name() != "",
		})
	}

	for _, bound := range bindings {
		if skipped[bound] || bound.marks() {
			continue
		}

		// A document with no content has no line to show for any path, so
		// that reason explains nothing the tree leaves out.
		reason := bound.Unresolved()
		if reason == nil || errors.Is(reason, paths.ErrNoDocument) {
			continue
		}

		rep.Unresolved = append(rep.Unresolved, bound)
	}

	rep.Tree = tree

	// A bound join whose branches all carry nothing has an empty tree and
	// nothing else to draw, so the message stands in for it.
	if len(rep.Excerpts) == 0 && len(rep.Unresolved) == 0 {
		rep.Tree = messageTree(tree, err)
	}

	return rep
}

// dropRepeats returns t with the details of each repeated warning left
// out, and adds the bindings of such a warning to skip, so no excerpt
// marks it. A warning repeats when its message and its details match those
// of an earlier warning under the same heading. Its row stays, so each
// file with the warning still shows in the tree.
func dropRepeats(t ErrorTree, skip map[*SourceError]bool) ErrorTree {
	if !t.heads() {
		return t
	}

	every := func(*SourceError) bool { return true }
	seen := make(map[string]bool)
	children := make([]ErrorTree, 0, len(t.Children))

	for _, child := range t.Children {
		switch {
		case child.Detail:

		case child.isProblem() && child.Warning:
			key := repeatKey(child)
			if seen[key] {
				for node := range child.nodes() {
					node.Bound.all(skip, every)
				}

				child.Children = nil
			}

			seen[key] = true

		default:
			child = dropRepeats(child, skip)
		}

		children = append(children, child)
	}

	t.Children = children

	return t
}

// repeatKey returns the text that two warnings share when one repeats the
// other: the message of t and the rows of its details.
func repeatKey(t ErrorTree) string {
	var sb strings.Builder

	sb.WriteString(t.Message())

	for node := range t.nodes() {
		if node.Detail {
			sb.WriteString("\x00")
			sb.WriteString(node.Label)
		}
	}

	return sb.String()
}

// messageTree returns t, or a single node with the message of err when t
// has no row to draw, as for a join of typed-nil errors, so a renderer
// draws that message rather than nothing.
func messageTree(t ErrorTree, err error) ErrorTree {
	if t.Label != "" || len(t.Children) > 0 {
		return t
	}

	return ErrorTree{Label: err.Error()}
}
