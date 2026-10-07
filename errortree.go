package niceyaml

import (
	"errors"
	"fmt"
	"iter"
	"reflect"
	"slices"
	"strings"
	"unicode"

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
// node rather than from the text. It reads [ErrorTree.Message] and
// [ErrorTree.Path] from the node, and the source and the position from
// its binding. [ErrorTree.Problems] yields the nodes such a report
// lists, one per problem, and [ErrorTree.All] yields every node of the
// tree. A node with Detail set explains its parent, and a renderer that
// draws details apart from problems reads the mark. [ErrorTree.Invalid]
// reports whether the document is at fault for the problems of a node.
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
	// Text is the message of the node, the message of the error without
	// the errors below it. It is empty for a node that stands for several
	// errors and adds no message of its own, such as one built from
	// [errors.Join]. It carries the location the row of the node shows,
	// in a form that depends on where the node sits in the tree, so a
	// report reads the message alone from [ErrorTree.Message].
	Text string
	// Children are the nodes of the errors below the node, in the order
	// the tree shows them: the problems a heading stands over, and the
	// details that explain the node, which have Detail set.
	Children []ErrorTree
	// Detail reports whether the node explains its parent, as an error
	// from [WithDetails] does, rather than standing for a problem the
	// parent heads.
	Detail bool
}

// NewErrorTree creates a new [ErrorTree] from err.
//
// The root text is the message of err without the errors below it, so
// context a wrapper added stays in front. The message of a wrapper around
// a join holds every branch of the join, and the message of one around a
// binding holds the errors [SourceError.Error] lists. Those errors are
// the children of the node, so each shows once. The root of a wrapper
// around a binding keeps the text of the wrapper and the line of the
// binding, as "load config: cafe.yaml: 2 schema violations" does. The
// root of a wrapper around a join keeps the text of the wrapper alone,
// such as "load config:". A wrapper that rewrites the message it wraps
// leaves nothing to tell apart, so its text stays whole above its
// children.
//
// For an error bound to a source, the root keeps the "name:line:col:"
// position [SourceError.Error] gives it, and each child, a binding of its
// own from [SourceError.Errors] or [SourceError.Details], carries the
// "line:col:" position its location resolved to without the name, since
// the root names the source already. A child with no
// position likewise drops the document its message names, unless it is
// bound to another document than its parent, and then it reads as
// "document 2: msg". The children of a
// binding sort by position within the source they are bound to, with the
// sources in the order they first appear, since a line number counts only
// in the source that holds it. Children whose location did not resolve
// follow the rest in the order their parent lists them. An [Error] or
// join outside any binding keeps its children in the order it lists them,
// bindings included. So a join of one binding per document shows the
// documents in the order [errors.Join] took them. A child with children
// of its own is a subtree.
//
// An error bound to no source has no position to show. When its cause
// chain reaches a located [*Error] that carries a path before any
// binding, the text of its node has that path in front, as the Error
// wrote it, such as "@.path: msg" for a check of a value. A binding puts
// the path there the same way, as a `$` path from the root of the
// document, so the row names the value whatever wraps the Error.
//
// An error that unwraps to several and whose message is theirs one per
// line, such as one from [errors.Join], is a node with no text and one
// child per error, so a run over several files reads as one tree with a
// branch per file. An [Error] that only wraps such an error, with no
// location and no details, is the same node. A
// binding of such an error is the same node, with each child carrying
// the name of its source in front of its position, since no root names
// the source for it. So is an Error that only wraps such a binding, and
// so are the children below a wrapper that keeps no line of the binding.
// A node with no text adds nothing. Its children take its place in the
// tree above it, and one with a single child is that child.
//
// Any other error that unwraps to several keeps its text and has a child
// per branch. A wrapper that [fmt.Errorf] builds with several %w verbs
// keeps only its branches that carry a location or errors below them,
// since its text shows the rest already. With one such branch, its
// children are the ones along the cause chain of that branch.
//
// The children come from the errors rather than from the text of the
// message, so a wrapper that rewrites the message it wraps keeps its
// children. Only the text of a node reads the message, to leave out what
// the children show.
//
// Each node holds its error in Err and its binding in Bound. The node of
// a binding holds that binding in both. The node of a wrapper around a
// binding, such as one from [fmt.Errorf], holds the wrapper in Err and
// the binding in Bound, so the node reports the location its text shows.
// So does the node of an [Error] with details around a binding. A
// located Error above a binding names a location no source resolved, so
// its node has no Bound, as the node of any other error bound to no
// source. A nil err yields the zero ErrorTree.
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
		t.all(yield)
	}
}

// all yields t when it has text, then every node below it, each before the
// nodes below it, and reports whether the caller wants more.
func (t ErrorTree) all(yield func(ErrorTree) bool) bool {
	if t.Text != "" && !yield(t) {
		return false
	}

	for _, child := range t.Children {
		if !child.all(yield) {
			return false
		}
	}

	return true
}

// Problems returns an iterator over the nodes that each stand for one
// problem, in the order [ErrorTree.All] yields them. It is the walk of a
// report that lists the problems of an error as rows, such as JSON lines,
// CI annotations, or editor diagnostics.
//
// The errors declare which nodes are problems, so the walk reads no shape
// of the tree:
//
//   - A summary from [NewSummary], a join from [errors.Join], and any
//     other error that unwraps to several errors head separate problems.
//     So does a wrapper around one, such as one from [fmt.Errorf].
//     Problems never yields a heading and walks on to the problems below
//     it.
//   - Every other node with text stands for one problem, and Problems
//     yields it. The walk goes no further below a node it yields, whose
//     Children are exactly the details [WithDetails] added to its error,
//     each with Detail set.
//
// A value that fails an anyOf of a schema therefore yields one row, with
// the forms it failed below it, and a validation whose message counts
// three violations yields three rows. The rows match the lines of the
// problems [SourceError.Error] lists below each heading. Whether a node
// carries a location plays no part, so an error bound to no source, such
// as a file that failed to read, yields by the same rule, and the report
// holds every problem.
//
// A row reads its message from [ErrorTree.Message] and its path from
// [ErrorTree.Path], which answer for a node bound to no source as well,
// and its file and position from the binding. The Children of a node
// Problems yields are its details, such as the reasons for the problem
// or the related locations it names, and each answers Message the same
// way:
//
//	for problem := range niceyaml.NewErrorTree(err).Problems() {
//		row := Row{Message: problem.Message()}
//
//		if path, ok := problem.Path(); ok {
//			row.Path = path.String()
//		}
//
//		if bound := problem.Bound; bound != nil {
//			row.File = bound.Source().FilePath()
//
//			if pos, ok := bound.Position(); ok {
//				row.Line, row.Column = pos.Line+1, pos.Col+1
//			}
//		}
//
//		for _, detail := range problem.Children {
//			row.Related = append(row.Related, detail.Message())
//		}
//
//		report(row)
//	}
//
// A row tells a fault of the document from a check that could not run
// with [ErrorTree.Invalid], and [IsInvalid] asks the same of every row.
// The document is at fault for each problem an [*Error] from [Invalid]
// heads, so each branch of a join that Invalid wraps yields a row that
// is invalid, while its text stays its own.
func (t ErrorTree) Problems() iter.Seq[ErrorTree] {
	return func(yield func(ErrorTree) bool) {
		t.problems(yield)
	}
}

// problems yields the nodes of t that [ErrorTree.Problems] yields and
// reports whether the caller wants more.
func (t ErrorTree) problems(yield func(ErrorTree) bool) bool {
	if t.Text != "" && !t.heads() {
		return yield(t)
	}

	for _, child := range t.Children {
		if !child.Detail && !child.problems(yield) {
			return false
		}
	}

	return true
}

// heads reports whether t stands over separate problems: whether a child
// of t is no detail. A node with no text stands for several errors, so
// its children are all problems it heads.
func (t ErrorTree) heads() bool {
	return slices.ContainsFunc(t.Children, func(c ErrorTree) bool { return !c.Detail })
}

// Invalid reports whether the document is at fault for every problem at
// or below the node, the ones [ErrorTree.Problems] yields for it. A node
// that yields no problem, such as the zero ErrorTree, is not invalid.
// [IsInvalid] answers for an error as the root of its tree does, and it
// describes which problems the document is at fault for.
//
// A report that lists the problems of an error as rows asks each row, to
// tell a fault of the document from a check that could not run:
//
//	for problem := range niceyaml.NewErrorTree(err).Problems() {
//		row := Row{Message: problem.Message(), Invalid: problem.Invalid()}
//		...
//	}
//
// A heading answers through the problems it heads, whatever its own
// error declares. The root of a join of a violation and a read error is
// not invalid, and neither is a summary or a wrapper above that pair. The
// details of a problem explain it and decide nothing, so a read error
// that names a located detail from [NewError] is not invalid. A node with
// Detail set answers as any other node does, for its own error or for the
// problems it heads, and its answer changes nothing for the node it
// explains.
//
// Invalid reads Err, so a node built by hand with no Err is not invalid.
func (t ErrorTree) Invalid() bool {
	found := false

	for problem := range t.Problems() {
		if !declaresInvalid(problem.Err) {
			return false
		}

		found = true
	}

	return found
}

// declaresInvalid reports whether err, the error of one problem, declares
// the document at fault: whether an error along its cause chain matches
// [errInvalid] from an Is method of its own. The chain follows an
// [*Error] to its cause and never to its details, which explain the
// problem and decide nothing, and a wrapper to the error it wraps. A
// wrapper with several %w verbs is one problem, so it declares the fault
// when any of its branches does.
func declaresInvalid(err error) bool {
	for cur := err; !isNothing(cur); {
		if x, ok := cur.(interface{ Is(target error) bool }); ok && x.Is(errInvalid) {
			return true
		}

		switch x := cur.(type) { //nolint:errorlint // Walks the chain one node at a time.
		case *Error:
			cur = x.err

		case interface{ Unwrap() error }:
			cur = x.Unwrap()

		case interface{ Unwrap() []error }:
			return slices.ContainsFunc(x.Unwrap(), declaresInvalid)

		default:
			return false
		}
	}

	return false
}

// Message returns the message of the node with no position, document, or
// path in front, the field a report carries beside the position and the
// path. Text holds the same message behind the location the row of the
// node shows, in a form that depends on where the node sits in the tree.
//
//   - For a node with a binding, it is the [SourceError.Message] of
//     Bound.
//   - For a node bound to no source that names a path, as
//     [ErrorTree.Path] reports, it is the message of Err without the path
//     Text puts in front of it. A binding of Err would report the same
//     message.
//   - For any other node, it is Text.
//
// A node with no text of its own, such as the root of a join, has an
// empty message. A detail answers like any other node, so a report that
// lists the related locations of a problem reads the message of each
// detail.
//
// The node of a wrapper around a binding, such as one from [fmt.Errorf],
// holds that binding in Bound, so its message is the message of the
// binding. The text the wrapper added stays in Text.
func (t ErrorTree) Message() string {
	if t.Bound != nil {
		return t.Bound.Message()
	}

	if _, ok := t.Path(); !ok {
		return t.Text
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
//     [Rebase] on the way joined in front. The text of the node puts the
//     same path in front of the message.
//
// The source and the position come from Bound, which resolved them. The
// methods of a nil [*SourceError] return zero values, so a report reads
// [SourceError.Source] and [SourceError.Position] from Bound for any
// node. A position that an [*Error] bound to no source carries points
// into no source, so the node leaves it to [Error.Position].
func (t ErrorTree) Path() (paths.Path, bool) {
	if t.Bound != nil {
		return t.Bound.Path()
	}

	a := anchorOf(t.Err)

	return a.path, a.hasPath
}

// bindingOf returns the binding err is or wraps along its cause chain, as
// [anchorOf] follows it, or nil when the chain holds none. A located
// [Error] above a binding anchors the chain itself, so such an error has
// none.
func bindingOf(err error) *SourceError {
	if bound, ok := anchorOf(err).err.(*SourceError); ok { //nolint:errorlint // The anchor itself, found by the walk.
		return bound
	}

	return nil
}

// appendTrees appends the nodes err stands for to dst and returns the
// extended slice. Most errors stand for one node, which [appendTree]
// appends. A join, as [joinBranches] finds one, stands for the nodes of
// its branches, and a binding of one, as [joinBinding] finds one, for its
// bound children in the order [trees] gives them. A join nested in another
// appends its branches to the same dst rather than building a node of its
// own. A loop that joins each new error onto the ones before with
// [errors.Join] nests each join in the next, and copying the nodes of each
// level into the level above would take time and memory quadratic in
// their number. A join that an [*Error] matching [errInvalid] wraps, as
// one from [Invalid] does, marks each branch as [markInvalid] does. A
// nil err appends nothing.
func appendTrees(dst []ErrorTree, err error) []ErrorTree {
	if isNothing(err) {
		return dst
	}

	if branches, ok := joinBranches(err); ok {
		invalid := headsInvalid(err)

		for _, branch := range branches {
			if invalid {
				branch = markInvalid(branch)
			}

			dst = appendTrees(dst, branch)
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

	if x, ok := err.(*SourceError); ok { //nolint:errorlint // The node itself, not a chain search.
		node.Text = x.headline()
	} else {
		node.Text, _, _ = end.cut(err.Error())
		node.Text = unboundText(err, node.Text)
	}

	return appendTree(dst, newTree(node, kids))
}

// unboundText returns text, the message of err, behind the path of the
// first located [*Error] along the cause chain of err, as [anchorOf]
// finds it, when that Error carries a path. A binding puts the path there
// the same way, so the row of an error no binding holds yet names the
// value it is about, however the wrappers above that Error nest. The text
// comes back as it is when the chain carries no path, or when it reaches
// a [*SourceError] first, since that binding names its own path.
func unboundText(err error, text string) string {
	if a := anchorOf(err); a.hasPath {
		return withPath(a.path, text)
	}

	return text
}

// appendTree appends t to dst and returns the extended slice. A node with
// no text adds nothing, so its children take its place.
func appendTree(dst []ErrorTree, t ErrorTree) []ErrorTree {
	if t.Text == "" {
		return append(dst, t.Children...)
	}

	return append(dst, t)
}

// joinBinding returns the binding of a join that err reads as, or nil
// when err reads as none. A [*SourceError] whose bound error is a join,
// as [joinBranches] finds one, is such a binding. An Error that adds
// nothing, as [Error.addsNothing] reports, reads as the error it wraps,
// and so does a binding that adopted such an Error, since neither adds
// text or children to the binding below it.
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

			if _, joined := joinBranches(x.err); joined {
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

// joinBranches returns the errors err unwraps to when err is a join of
// several, as [errors.Join] builds one, and false for any other error. An
// Error unwraps to several too, but it is one node of the tree with the
// errors a summary heads, or its details, as children, so it is not a
// join. The exception is an
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
	return joinBranchesOf(err, func() string { return err.Error() })
}

// joinBranchesOf is [joinBranches] for a caller that reads the message of
// err itself. It calls msg for that message, and only for an error whose
// type does not say whether it is a join, so a caller that keeps the
// message reads it once. An Error that adds nothing has the message of
// the error it wraps, so msg serves for that error too.
func joinBranchesOf(err error, msg func() string) ([]error, bool) {
	if x, ok := err.(*Error); ok { //nolint:errorlint // The node itself, not a chain search.
		if !x.addsNothing() {
			return nil, false
		}

		return joinBranchesOf(x.err, msg)
	}

	joined, ok := err.(interface{ Unwrap() []error }) //nolint:errorlint // The node itself, not a chain search.
	if !ok {
		return nil, false
	}

	branches := joined.Unwrap()
	if !isJoinError(err) && !isJoinMessage(msg(), branches) {
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

// isJoinError reports whether err is an error that [errors.Join] builds.
func isJoinError(err error) bool {
	return reflect.TypeOf(err) == joinType
}

// isWrapErrors reports whether err is a wrapper that [fmt.Errorf] builds
// with several %w verbs.
func isWrapErrors(err error) bool {
	return reflect.TypeOf(err) == wrapErrorsType
}

// isJoinMessage reports whether msg is the messages of branches one per
// line, which is how [errors.Join] writes the message of the error it
// builds. A nil branch carries no message and no line of its own, so it
// takes no separator either.
//
// It matches msg against the branches in place and stops at the first
// mismatch. [followBranches] runs this check at every level of a chain
// of wrappers, and building the joined text at each level would copy
// the message below it once per level.
func isJoinMessage(msg string, branches []error) bool {
	first := true

	for _, branch := range branches {
		if branch == nil {
			continue
		}

		var ok bool

		if !first {
			if msg, ok = strings.CutPrefix(msg, "\n"); !ok {
				return false
			}
		}

		first = false

		if msg, ok = strings.CutPrefix(msg, branch.Error()); !ok {
			return false
		}
	}

	return msg == ""
}

// children returns the nodes of the children [walkChildren] finds along
// the cause chain of err, and the [chainEnd] of that chain. A binding
// that ends the chain contributes its bound children, each with the
// position it resolved to, since the binding bound everything below it.
// Those children name their source when the binding has no line of its
// own to name it. Every other child is the tree of the error rebased
// under its base, as binding rebases it, with Detail set on a detail. The
// children of an unbound Error and the branches of a join keep the order
// their parent lists them in, since only the children of a binding carry
// the source and position [trees] sorts by.
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
	if t.Text != "" {
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
// it, when the errors below it are children of the error the walk started
// at: a [*SourceError], or an error that unwraps to the branches the walk
// reported. The zero chainEnd is a chain that ends at neither.
type chainEnd struct {
	err error
	// The number of branches the walk reported as children.
	branches int
	// Whether an error on the way to err added text of its own, so the
	// message the walk started at differs from the message of err.
	wrapped bool
}

// cut returns text, the message of the error the chain belongs to,
// without the part the children show. The message of a wrapper holds the
// message of the error it wraps, so the message of a wrapper around a
// join holds every branch of the join, and one around a binding holds the
// errors [SourceError.Error] lists. Those errors are children, so a join
// leaves nothing of its message and a binding leaves its own line. Space
// a cut leaves at the end of the text goes with it, so "load: " in front
// of a join reads "load:".
//
// Any other error that unwraps to several keeps its message. The second
// result is the number of children at the end of the list of children
// whose text the message holds already. They are the branches of such an
// error when its message holds the message of its first branch, as a
// wrapper [fmt.Errorf] builds with several %w verbs does and a count of
// the branches does not, and the ones a binding reports the same of. The
// message holds the text of a branch and never the location a binding
// gives it, so [SourceError.Error] still lists each of them that the
// binding located.
//
// A wrapper that rewrites the message it wraps leaves nothing to find.
// The text then comes back whole, and the last result is false.
func (c chainEnd) cut(text string) (string, int, bool) {
	var (
		full, own string
		shown     int
	)

	switch x := c.err.(type) { //nolint:errorlint // The end of the chain, found by the walk.
	case nil:
		return text, 0, true

	case *SourceError:
		t := x.texts()
		full, own, shown = t.msg, t.head, t.shown

	case interface{ Unwrap() []error }:
		msg := text
		if c.wrapped {
			msg = c.err.Error()
		}

		branches := x.Unwrap()

		switch {
		case isJoinError(c.err) || isJoinMessage(msg, branches):
			full = msg

		case isWrapErrors(c.err) || holdsFirst(msg, branches):
			return text, c.branches, true

		default:
			return text, 0, true
		}
	}

	if full == own {
		return text, shown, true
	}

	before, after, ok := strings.Cut(text, full)
	if !ok {
		return text, 0, false
	}

	return strings.TrimRightFunc(before+own+after, unicode.IsSpace), shown, true
}

// holdsFirst reports whether msg holds the message of the first of
// branches that is not a nil interface.
func holdsFirst(msg string, branches []error) bool {
	first := firstBranch(branches)

	return first != nil && strings.Contains(msg, first.Error())
}

// walkChildren walks the cause chain of err and reports the children
// along it, the ones binding and [NewErrorTree] both place under err. The
// chain follows each wrapper to the one error it wraps and each [*Error]
// to its cause. Every error a summary on the way heads, and every detail
// of an Error on the way, is a child. The walk reports it to onChild with
// whether it is a detail and the base of every Error from [Rebase] above
// it, the Error that holds it included. At an error that unwraps to
// several, the chain goes on as [followBranches] says. It follows the one
// branch that remains of a wrapper with several %w verbs and otherwise
// ends there, with each branch that remains a child under the same base,
// which the error heads. It also ends at a [*SourceError], which bound
// everything below it already, so onBinding receives it in place of its
// children. The result is the [chainEnd] of the chain: that binding, or
// the error whose branches the walk reported.
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
// reads as a join, as [isJoinMessage] reports.
func followBranches(err error, branches []error) ([]error, error) {
	// The type check runs first, so an error of any other type never
	// formats its message here, where the result would go unused.
	wrapper := isWrapErrors(err) && !isJoinMessage(err.Error(), branches)

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
			// An Error from Rebase that points at its base anchors the
			// chain there when nothing below it carries a location.
			if x.hasLocation() || (x.rebased && !x.movesOnly) || x.nests() {
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
// the base of every Error from [Rebase] above them, joined, whether the
// walk met such an Error, and whether one of them moves paths alone. It
// also records whether an Error above them matches [errInvalid]. A Rebase
// at the root still locates a problem with no location at the root, so
// the children rebase whenever the walk met one, and only a chain that
// holds none leaves them as they are.
type childBase struct {
	path      paths.Path
	rebased   bool
	movesOnly bool
	invalid   bool
}

// cross returns the base below x: c joined with the base of x when x is
// an Error from [Rebase], and c as it is otherwise, marked invalid when x
// matches [errInvalid].
func (c childBase) cross(x *Error) childBase {
	if x.rebased {
		c.path = c.path.Join(x.base)
		c.rebased = true
		c.movesOnly = c.movesOnly || x.movesOnly
	}

	c.invalid = c.invalid || x.invalid

	return c
}

// rebase returns n rebased under the base, or n as it is when the walk met
// no Error from [Rebase]. A detail explains the error above it, so it
// takes no location from the base, and the base moves its paths alone. So
// does every child below a detail, or below any other Rebase that moves
// paths alone. A problem below an Error that matches [errInvalid] matches
// too, as [markInvalid] marks it, so each error a summary of a
// [SelfValidator] heads, and each branch of a join that [Invalid]
// wraps, is the document's fault. A detail is no problem, so it gains no
// mark.
func (c childBase) rebase(n error, detail bool) error {
	invalid := c.invalid && !detail

	if !c.rebased {
		if invalid {
			return markInvalid(n)
		}

		return n
	}

	return rebase(n, c.path, detail || c.movesOnly, invalid)
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

// appendBoundChildren appends the nodes of the children of bound to kids,
// each with its own children as a subtree, and returns the extended
// slice. A child bound to another source carries its whole line, as
// [SourceError.headline] returns it, which names that source. So does a
// child bound to the same source when named is set, for a parent with no
// text of its own. Otherwise a child bound to the same source carries the
// "line:col:" its location resolved to in front of its message, without
// the name the parent gives already. A child with no position carries
// the document [SourceError.documentLabel] returns when it is bound to
// another document than bound, or to one where bound has none. A child
// that wraps a binding through
// Errors alone, which add no text, reads as that binding does, unless the
// child puts the name of its source in front, as [textBinding] describes.
// A child behind a wrapper that adds text of its own, such as
// [fmt.Errorf], carries the position inside that text, so it comes
// through as it is, name included. A child that binds a join, or wraps
// such a binding through Errors that add nothing, has no text and gives
// its place to the branches of the join. Those branches keep the name of
// their source when named is set or when the join is bound to another
// source. They go straight into kids, as [appendTrees] appends the
// branches of a join. The node of a detail has Detail set, as [asDetail]
// sets it, and so do the branches that take the place of a detail.
func appendBoundChildren(kids []positioned, bound *SourceError, named bool) []positioned {
	for _, c := range bound.below {
		child, detail := c.bound, c.detail

		// The branches sit beside the other children, so they sort by
		// position among them rather than under a node of their own.
		joined := joinBinding(child)
		if joined != nil {
			from := len(kids)
			kids = appendBoundChildren(kids, joined, named || joined.Source() != bound.Source())

			for i := from; detail && i < len(kids); i++ {
				kids[i].tree.Detail = true
			}

			continue
		}

		kid := placed(child)

		// The binding put the position in front of the message it wraps,
		// so stripping it back to that message leaves the position to
		// put back without the name. A child that took a binding over
		// through Errors alone shows the text of the binding [textBinding]
		// finds, so the strip applies there. One behind a wrapper with
		// text of its own carries its position inside that text instead,
		// and adds no prefix of its own, so there is nothing to strip or
		// put back.
		src := textBinding(child)

		text := src.headline()
		if !named && src.Source() == bound.Source() {
			if inner := src.texts().own; inner != text {
				text = inner

				switch {
				case kid.located:
					text = prefix(editorPosition(kid.pos)+":", text)

				case src.Document() != bound.Document():
					// The parent names another document of the source, or
					// none, so the child keeps the one it wrote.
					text = prefix(suffixed(src.documentLabel()), text)
				}
			}
		}

		below, _ := children(child)

		kid.tree = newTree(ErrorTree{Err: child, Bound: child, Text: text}, below)
		if detail {
			kid.tree = asDetail(kid.tree)
		}

		kids = append(kids, kid)
	}

	return kids
}

// textBinding returns the binding whose text e shows: the binding an
// adopted e reaches through the causes of Errors alone, which add no
// text of their own, as [Error.textCause] walks them, or e itself. An
// adopted binding that puts the name of its source in front of its
// message, rather than keeping the message as [SourceError.keepsMessage]
// reports, adds that name as text of its own, so the walk stops there.
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

// positioned is a child of a node, the source it is bound to, and the
// position it resolved to, when located, for the order the children take.
// The child is a binding, with the node [appendBoundChildren] builds for
// it, or the node of an error bound to no source. [groupSources] fills
// group in.
type positioned struct {
	src     *Source
	bound   *SourceError
	tree    ErrorTree
	pos     position.Position
	group   int
	located bool
}

// placed returns child with the source and the position [trees] orders it
// by. A location the source does not hold resolved to nothing the excerpt
// can mark, so the child reads as an unlocated one. A located child
// reports the position its message carries, which is inside the token its
// range marks when the caller gave the error a position rather than a
// path.
func placed(child *SourceError) positioned {
	kid := positioned{src: child.Source(), bound: child}

	if _, ok := child.Range(); ok {
		kid.located = true
		kid.pos = child.loc.pos
	}

	return kid
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

// newTree returns node with children below it, less the nodes that add
// nothing. A child with no text gives its place to its own children, and a
// node with no text and a single child is that child.
func newTree(node ErrorTree, children []ErrorTree) ErrorTree {
	if len(children) == 0 {
		return node
	}

	flat := make([]ErrorTree, 0, len(children))

	for _, child := range children {
		flat = appendTree(flat, child)
	}

	if node.Text == "" && len(flat) == 1 {
		return flat[0]
	}

	if len(flat) > 0 {
		node.Children = flat
	}

	return node
}
