package niceyaml

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
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
// renders one as plain text, and
// [go.jacobcolvin.com/niceyaml/printer.Printer.PrintError] renders one
// with styles. A program can draw the tree with a renderer of its own,
// such as a viewer that lists violations. That renderer then agrees with
// the renderers of niceyaml about which error each line belongs to. Each
// node holds its row in parts, so a renderer can style the location, the
// "warning:" label, and the path of a row apart from its text, as
// [ErrorRow] describes.
//
// Each node also holds the error it stands for and the binding that
// resolved its location. Each report that a program reads takes its
// fields from the node rather than from the label. Such reports include
// JSON lines, CI annotations, and editor diagnostics. They read
// [ErrorTree.Message], [ErrorTree.Context], and [ErrorTree.Path] from the
// node, and [ErrorTree.Name], [ErrorTree.FilePath], and
// [ErrorTree.Position] from its binding. A problem bound to a whole
// document does not have a position, so such a report takes its line from
// [ErrorTree.DocumentSpan] instead. [Problems] yields the nodes that
// such a report lists, one per problem, and [ErrorTree.All] yields every
// node of the tree. [ErrorTree.MarshalJSON] encodes a node as a JSON object
// of the message, the context, the name, the file path, and the text of the
// path. The object holds [ErrorTree.Range] in place of the position. A
// report in JSON writes each node as it is.
//
// Each node with Detail set explains its parent, and a renderer that
// draws details apart from problems reads that field.
// [ErrorTree.IsInvalid] reports whether the document is at fault for the
// problems of a node.
//
// Create instances with [NewErrorTree].
type ErrorTree struct {
	// Err is the error the node stands for, for [errors.Is] and
	// [errors.As]. It is nil for a node that does not have text.
	Err error
	// Bound is the binding that gives the node its source and location.
	// It is the [*SourceError] that Err is, or the one that Err wraps along
	// its cause chain. It is nil for an error that is not bound to a
	// source.
	Bound *SourceError
	// Row is the row that a renderer draws for the node, in parts. It holds
	// the message of the error without the errors below it, behind a
	// location whose form depends on the row of the parent, as [ErrorRow]
	// describes. [ErrorTree.Label] joins the parts into one string.
	//
	// Row is zero for a node that stands for several errors and does not
	// add a message of its own, such as one built from [errors.Join]. A
	// report carries [ErrorTree.Message] and [ErrorTree.Context] instead.
	// They return the message of the problem alone and the text that the
	// wrappers above the problem added.
	Row ErrorRow
	// The text that the wrappers of the headings above the node wrote
	// around its problem. [ErrorTree.Context] puts that text around the
	// text of the wrappers of the node itself.
	above wrapperText
	// Children are the nodes of the errors below the node, in the order
	// that the tree shows them. They are the problems that a heading
	// stands over and the details that explain the node. Each detail has
	// Detail set.
	Children []ErrorTree
	// Detail reports whether the node explains its parent, as an error
	// from [WithDetails] does, rather than standing for a problem the
	// parent heads.
	Detail bool
	// Warning reports whether the problem of the node is a warning, as
	// [NewWarning] and [Node.Warn] mark one, rather than an error. For a
	// heading, Warning is true when every problem below it is a warning.
	// The types that [errors.As] finds in Err tell what a problem is about
	// and never its severity, so a renderer reads Warning.
	Warning bool
}

// ErrorRow is the row of an [ErrorTree] node in parts, in the order that
// the row shows them. A renderer that styles each part reads them here, and
// a renderer that draws the row as plain text reads [ErrorRow.String].
//
// The form of the location depends on the row of the parent:
//
//   - The root of a binding reads "name:line:col".
//   - Each child bound to the same source drops the name and reads
//     "line:col".
//   - A binding without a position reads "name". In a source of several
//     documents, it names its document behind the first line of that
//     document, as "name:4: document 2". A Source with positions off
//     leaves the line out, as in "name: document 2". A source without a
//     name drops the name, as in "4: document 2".
//   - Each child in another document of the same source names that
//     document, as "4: document 2".
//   - A node that is not bound to a source does not have a location.
//
// The line and column of a location count from 1, as an editor does.
//
// Text holds the rest of the row. It is the message of the error with the
// text that the wrappers around it added. Some rows keep the whole line of
// another binding in Text, with the location and the path of that line:
//
//   - A wrapper that rewrote the line of the binding it wraps keeps that
//     line.
//   - A located error above a binding of another source keeps the line of
//     that binding, as in "f.yaml:1:4: $.a: g.yaml:1:4: $.c: bad c".
//
// [ErrorTree.Message] returns the message of the problem alone, so a
// report reads that method and not Text.
//
// The zero ErrorRow stands for a node that does not have text.
type ErrorRow struct {
	// Location is the place of the problem as the row shows it, without the
	// colon after it, such as "cfg.yaml:3:6", "3:6", or "cfg.yaml:4:
	// document 2". It is empty for a row that does not show a location.
	Location string
	// Severity is "warning" for the row of a warning that shows the label,
	// and empty for every other row. A warning detail shows the label too.
	// A heading does not, and neither does a row whose label sits in the
	// line of another binding that Text keeps.
	Severity string
	// Path is the text of the path that the row shows, such as "$.b.c", or
	// empty for a row that does not show one.
	Path string
	// Text is the rest of the row.
	Text string
}

// String returns the row as one string, as [FormatError] draws it. Each
// part but Text ends with a colon, and a space separates the parts that are
// not empty, as in "cfg.yaml:3:6: warning: $.b.c: old c". The zero
// ErrorRow returns "".
func (r ErrorRow) String() string {
	var sb strings.Builder

	for _, part := range [...]string{r.Location, r.Severity, r.Path} {
		if part == "" {
			continue
		}

		if sb.Len() > 0 {
			sb.WriteByte(' ')
		}

		sb.WriteString(part)
		sb.WriteByte(':')
	}

	if r.Text != "" {
		if sb.Len() > 0 {
			sb.WriteByte(' ')
		}

		sb.WriteString(r.Text)
	}

	return sb.String()
}

// IsZero reports whether every part of r is empty, as for a node that does
// not have text.
func (r ErrorRow) IsZero() bool {
	return r == ErrorRow{}
}

// Label returns the row of the node as one string, as [ErrorRow.String]
// joins the parts of Row. It is empty for a node that does not have text.
func (t ErrorTree) Label() string {
	return t.Row.String()
}

// NewErrorTree creates a new [ErrorTree] from err. The root holds the
// message of err without the errors below it. The context that a wrapper
// added thus stays in front of the message, and each error shows once.
// Each problem err heads and each of its details is a child, and a child
// with children of its own is a subtree. A nil err yields the zero
// ErrorTree.
//
// The root of a binding keeps the "name:line:col:" position
// [SourceError.Error] gives it. So does the node of a wrapper around a
// binding, which reads as a binding of the wrapper reads. The text of the
// wrapper follows the position and the path, although the message that
// [fmt.Errorf] builds for the wrapper holds it in front. Each child reads
// "line:col:" without the name. A child bound to a document other than
// that of its parent names its document and the first line of it
// instead, as "4: document 2: msg".
// The children of a binding sort by position within their source, with
// the sources in the order they first appear and the children whose
// location did not resolve last. Outside a binding, children keep the
// order the error lists them in. The node of an error that is not bound to
// a source puts the path of its [*Error] in front of the message, as
// "@.path: msg".
//
// An [errors.Join] error is a node that does not have text. So is any
// other error that unwraps to several and whose message is theirs one per
// line. The children of such a node take its place in the tree above it.
// An [*Error] from [Invalid], [Unchecked], or [Locate] around such an
// error does not add text either, even with a location. Each problem
// below it that does not carry a location takes that location.
//
// Any other error that unwraps to several keeps its text and has a child
// per branch. A wrapper that [fmt.Errorf] builds with several %w verbs is
// the exception. It has a child for each branch that is a problem and
// skips each branch that is a sentinel, as the Errors section of the
// package documentation defines them. With one such branch, the wrapper
// reads as a wrapper with one %w verb around that branch.
//
// Each node holds its error in Err and its binding in Bound. The node of
// a wrapper around a binding holds the wrapper in Err and the binding in
// Bound. So does the node of a located [*Error] above a binding whose
// location resolved, because the binding keeps its own location, as
// [Locate] describes. The node of a located Error that names the path of
// a binding whose location did not resolve holds that binding too. The
// line of the binding shows that path already. A located Error between
// the two lies nearer the binding, so its location wins instead. Above
// any other binding, the Error names a location that a source never
// resolved, so Bound is nil for its node.
//
// Until a source binds the Error, the binding below it keeps its location
// whatever its source. The node of a located Error around an error of an
// included file thus holds the binding of that error and reads as it. A
// program that reports one such problem at the place of the include binds
// the Error to the including source first, as [Node.Validate] does. Each
// bound problem of a join keeps its own place after that bind too, as
// [Locate] describes.
func NewErrorTree(err error) ErrorTree {
	t := buildTree(err)
	inheritContext(&t, wrapperText{})

	return t
}

// buildTree returns the tree of err, as [NewErrorTree] documents it,
// before the nodes take the context of the headings above them.
func buildTree(err error) ErrorTree {
	return newTree(ErrorTree{}, appendTrees(nil, err))
}

// inheritContext gives t the text above, which the wrappers of the
// headings over t wrote. It gives each node below t the text of the
// headings over that node. A problem below a heading takes the text of
// that heading, which holds the text above. The problem thus has the
// context that it has when the wrappers of the heading wrap it alone. A
// detail explains one problem, so it does not take the text of that
// problem.
func inheritContext(t *ErrorTree, above wrapperText) {
	t.above = above

	var below wrapperText

	if t.heads() {
		below = t.wrapperText().under(above)
	}

	for i := range t.Children {
		child := &t.Children[i]
		if child.Detail {
			inheritContext(child, wrapperText{})

			continue
		}

		inheritContext(child, below)
	}
}

// All returns an iterator over the nodes of the tree, t first and each
// node before the nodes below it, in the order [FormatError] prints their
// rows. A node has a row only when it has text. All skips each node that
// does not have text and yields the nodes below it. The zero ErrorTree
// therefore does not yield a node.
//
// Each node comes with its Children, and All yields those children after
// it. A caller thus reads one row per node or the whole subtree of a
// node:
//
//	for node := range niceyaml.NewErrorTree(err).All() {
//		if errors.Is(node.Err, fs.ErrNotExist) {
//			...
//		}
//	}
func (t ErrorTree) All() iter.Seq[ErrorTree] {
	return func(yield func(ErrorTree) bool) {
		for node := range t.nodes() {
			if !node.Row.IsZero() && !yield(node) {
				return
			}
		}
	}
}

// Problems returns an iterator over the nodes that each stand for one
// problem, in the order [ErrorTree.All] yields them. It is the walk of a
// report that lists the problems of an error as rows, such as JSON lines,
// CI annotations, or editor diagnostics.
//
// Headings do not yield a node of their own, and the walk continues to
// the problems below them. A heading is a summary from [NewSummary], a
// join, any other error that unwraps to several, or a wrapper around one.
// A wrapper that [fmt.Errorf] builds with several %w verbs is a heading
// only when it heads several problems, as [NewErrorTree] describes.
//
// Every other node with text is one problem, and Problems yields it with
// exactly its details as its Children, each with Detail set. Problems
// yields each problem whether or not it carries a location. It thus
// yields a row for an error that is not bound to a source, such as a file
// that did not read. The rows match the lines that [SourceError.Error]
// lists.
//
// A row reads [ErrorTree.Message], [ErrorTree.Context], and
// [ErrorTree.Path], which answer for every node. The context holds the
// text of each wrapper above the problem, the wrappers that are headings
// included. A row thus does not need the headings that the walk skips. A
// row also reads [ErrorTree.Source] and [ErrorTree.Position], which return
// zero values for a node that is not bound to a source. [errors.Is] and
// [errors.As] on Err search the problem and skip its details:
//
//	for problem := range niceyaml.Problems(err) {
//		row := Row{
//			Message: problem.Message(),
//			Context: problem.Context(),
//			Invalid: problem.IsInvalid(),
//		}
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
// that each stand for one problem. It yields them as [ErrorTree.Problems]
// yields them for the tree that [NewErrorTree] builds. It is the walk of
// a report that lists the problems of an error as rows:
//
//	for problem := range niceyaml.Problems(err) {
//		path, _ := problem.Path()
//		pos, _ := problem.Position()
//		emit(problem.Name(), pos, path, problem.Context(), problem.Message())
//	}
//
// A report in JSON encodes each node instead, as [ErrorTree.MarshalJSON]
// describes. Problems does not yield a node for a nil err.
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
// that does not head separate problems.
func (t ErrorTree) isProblem() bool {
	return !t.Row.IsZero() && !t.heads()
}

// heads reports whether t stands over separate problems: whether a child
// of t is not a detail. A node that does not have text stands for
// several errors, so its children are all problems it heads.
func (t ErrorTree) heads() bool {
	return slices.ContainsFunc(t.Children, func(c ErrorTree) bool { return !c.Detail })
}

// IsInvalid reports whether the document is at fault for the problems at
// or below the node, the ones [ErrorTree.Problems] yields for it, as the
// function [IsInvalid] describes. It decides from the errors among them
// and ignores the warnings. A node whose every problem is a warning
// answers for the warnings, so the row of a deprecation reports true.
// IsInvalid reports false for a node that does not yield a problem, such
// as the zero ErrorTree.
//
// A heading answers through the problems it heads, whatever its own error
// declares, and the details of a problem do not count. A node with Detail
// set answers for its own error, and its answer does not change the
// answer for the node it explains. The method reads Err, so a node built
// by hand with a nil Err is not invalid.
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
// details, which do not count, and stops at a [*PanicError]. An error
// with several branches does not declare a fault when a branch holds the
// error of a context that ended. Such a branch reports that the call
// stopped, not a fault of the document, and [Invalid] does not declare
// one for it either.
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

// Message returns the message of the problem that the node stands for. It
// leaves out the position, the document, and the path of the node, the
// "warning:" label, and the text of each wrapper above the problem. A
// report carries that field beside the position and the path, and reads
// the text of the wrappers from [ErrorTree.Context]. The message of a
// problem thus stays the same however many other problems a wrapper holds
// beside it. Row holds the row that a renderer draws instead, in a form
// that depends on where the node sits in the tree.
//
// The problem is the innermost [*Error] or binding along the cause chain
// of Err:
//
//   - For an Error from [NewError], the message is the one NewError
//     received. A wrapper from [fmt.Errorf]("load item: %w", err) around
//     the Error leaves that message as it is, and Context returns "load
//     item".
//   - For an Error from [Invalid], [Unchecked], or [Locate] around an
//     error that holds neither an Error nor a binding, the message is the
//     whole text of that error. So is the message of a binding of such an
//     error.
//   - For an error that holds neither an Error nor a binding, the message
//     is [ErrorTree.Label], which holds the whole text of the error.
//
// A wrapper whose text does not hold the text of the error it wraps
// leaves the message as it is too, and Context returns the whole text of
// that wrapper.
//
// A [Locate], [Invalid], or [Unchecked] result can give its location to a
// binding inside it. The result is then the problem, and its message
// holds the path, the document, or the whole line of that binding, as
// [SourceError.Message] describes. The node of a result that no source
// bound reads the same way. When the source of the binding inside the
// result later binds the result, the message leaves out the name of that
// source. The node message "main.yaml: $.other.deep: bad deep" thus
// becomes "$.other.deep: bad deep".
//
// The line of that binding holds the message of its problem alone. The
// "warning:" label of the binding stays out, whether or not a source bound
// the result. The Warning field of the node reports the severity. The
// text of each wrapper that the binding binds stays out, and Context
// returns it, as it does for a wrapper above the binding. Take a check of
// an included file that wraps its error with "check:". The result then
// has the message "inc.yaml:1:6: $.key: bad key" and the context "check".
// A binding without a message, such as the binding of a wrapper around a
// join, gives the result an empty message.
//
// A heading reports its own message, as "2 violations" for a summary from
// [NewSummary]. So does any other error that heads several problems and
// keeps a text of its own, such as a wrapper that [fmt.Errorf] builds with
// several %w verbs around several problems. A heading that a wrapper makes
// of a join does not have a message, because all of its text is context.
// Neither does a node without a label, such as the root of a join. A
// detail answers like any other node, so a report that lists the related
// locations of a problem reads the message of each detail.
func (t ErrorTree) Message() string {
	_, core := problemOf(t.Err)
	inner := asBinding(core)

	switch {
	case t.Bound != nil && inner != t.Bound:
		// The walk did not reach the binding of a node built by hand, so
		// the text of its wrappers stays in its message.
		return t.rowMessage()

	case t.Bound != nil:
		return t.Bound.problemMessage()

	case inner != nil:
		// A located Error that no source bound keeps its location above
		// the binding, so the line of the binding is its message. The node
		// reports the severity in Warning, so the line leaves the label
		// out, as it does after a source binds the Error.
		return inner.problemLine()

	case core == nil:
		return t.Label()

	default:
		return ownMessage(core)
	}
}

// Context returns the text that wrappers added above the problem of the
// node, outermost first, or "" when the problem does not have a wrapper.
// A report carries that field beside [ErrorTree.Message], as in "load
// item" beside "expected integer, got string".
//
// A wrapper around one problem shares a row with that problem, and a
// wrapper around several problems is a heading above them. Context returns
// the text of the wrapper for each of those problems either way. For a
// wrapper from [fmt.Errorf]("load %s: %w", path, err), an err with one
// problem and an err with several both give each problem the context
// "load cfg.yaml". Each problem that [ErrorTree.Problems] yields thus has
// its context, although the walk skips the headings.
//
// The context is the text that the wrappers put in front of the message,
// then the text that they put behind it. Each part leaves out the white
// space and the colon that joined it to the message. A wrapper from
// [fmt.Errorf]("item %w is bad", err) thus adds "item is bad". A problem
// below a heading reads as it does when each wrapper of the heading wraps
// that problem alone. The text of those wrappers thus stands around the
// text of the wrappers of the problem itself, as in "load cfg.yaml: decode
// price".
//
// A wrapper does not record where it wrote the text of the error that it
// wraps. Context thus finds that text in the text of the wrapper. The
// words of a wrapper can repeat the message of a lone problem. Context
// then reads the problem at the end of the text of the wrapper, where most
// wrappers write it, or else at the start. White space and a colon can
// stand between the problem and that end. Take a wrapper from
// [fmt.Errorf]("%w: not found in registry", err) around the message "not
// found". It adds "not found in registry".
//
// A wrapper with several %w verbs holds the text of each of its errors in
// order. Context thus reads the problem in the part between the errors
// beside it, at the end of that part or else at its start.
//
// That rule has a limit. Some wrappers write text behind the problem and
// repeat the message of the problem in the rest of their text. Such a
// wrapper might add one context around a lone problem and another around
// several problems. Take a wrapper from [fmt.Errorf]("item %w was not
// found", err). Around a lone "not found", it adds "item not found was".
// Around several problems, it adds "item was not found". When the text of
// such a wrapper holds the message in several places and at neither end,
// the whole text of the wrapper is the context of a lone problem.
//
// The problem is an [*Error] or a binding, as [ErrorTree.Message] finds
// it. An error that holds neither is the exception to the rule that a
// wrapper reads the same around one problem and around several. Such an
// error keeps the text of each wrapper that shares its row in its message.
// Its context holds the text of the headings above it alone. A wrapper
// from [fmt.Errorf]("load: %w", err) around a lone [io.ErrUnexpectedEOF]
// thus gives the message "load: unexpected EOF" and an empty context.
// Below a heading from that wrapper, the same error has the message
// "unexpected EOF" and the context "load".
//
// A binding below a [Locate], [Invalid], or [Unchecked] result that keeps
// its location adds the text of the wrappers that it binds, as Message
// describes.
//
// A heading does not add its own message to the context of the problems
// below it. A summary from [NewSummary] and a heading that counts its
// problems thus do not add any context. Each of them reports the text of
// the wrappers above itself as its own context. A detail has the context of
// the wrappers around the detail alone, without that of the problem it
// explains.
//
// [NewErrorTree] gives each node the context of the headings above it. A
// [Warnings] lists each problem without the headings that stood above it.
// Each problem of [Warnings.Report], [Warnings.Err], and [Warnings.All]
// keeps the context that it had below those headings.
//
// A node that a program builds by hand has the context of the wrappers in
// its own Err. When that Err does not hold the Bound of the node, the node
// has the [SourceError.Message] of Bound as its message and does not have a
// context.
func (t ErrorTree) Context() string {
	return t.wrapperText().under(t.above).context()
}

// rowMessage returns the message that the row of the node shows behind
// its location. It is the message of the problem with the text that each
// wrapper around the problem added. An excerpt annotates the location of a wrapped
// binding with it.
//
//   - For a node with a binding, it is the [SourceError.Message] of
//     Bound, with the text that a wrapper around Bound added.
//   - For a node that is not bound to a source and names a path, as
//     [ErrorTree.Path] reports, it is the message of Err without the path
//     that the row puts in front of it.
//   - For any other node, it is the label of the node.
func (t ErrorTree) rowMessage() string {
	if t.Bound != nil {
		return wrapperMessage(t.Err, t.Bound)
	}

	if _, ok := t.Path(); !ok {
		return t.Label()
	}

	// The part of the message the children show depends on the end of the
	// cause chain, so the walk finds that end alone.
	end := walkChildren(t.Err, func(*SourceError, childBase) {}, func(error, childBase, bool) {})

	return bareMessage(t.Err, end)
}

// wrapperText returns the text that the wrappers above the problem of t
// wrote around its message. It leaves out the context of the headings
// above t.
func (t ErrorTree) wrapperText() wrapperText {
	wrappers, core := problemOf(t.Err)
	inner := asBinding(core)

	// The walk did not reach the binding of a node built by hand, so the
	// text of its wrappers stays in its message.
	if t.Bound != nil && inner != t.Bound {
		return wrapperText{}
	}

	// The binding that ends the walk holds the wrappers below the ones
	// above it. It is Bound, or the binding below a located Error that no
	// source bound.
	return inner.wrapperText().around(wrappers)
}

// asBinding returns err when it is a binding, and nil when it is any other
// error, such as one that wraps a binding. The error that ends the walk of
// [problemOf] is a binding when the problem stands in that binding.
func asBinding(err error) *SourceError {
	x, ok := err.(*SourceError) //nolint:errorlint // The error itself, not a chain search.
	if !ok {
		return nil
	}

	return x
}

// wrapperText holds the text that the wrappers above a problem wrote
// around the message of that problem.
type wrapperText struct {
	// The text in front of the message, outermost wrapper first.
	before string
	// The text behind the message, innermost wrapper first.
	after string
}

// around returns w with the text of wrappers around it. The wrappers
// stand above the problem of w, outermost first, as [problemOf] returns
// them.
func (w wrapperText) around(wrappers []wrapper) wrapperText {
	var outer wrapperText

	for _, x := range wrappers {
		b, a := x.added()
		outer.before += b
		outer.after = a + outer.after
	}

	return w.under(outer)
}

// under returns w with the text of outer around it. The wrappers of outer
// stand above those of w.
func (w wrapperText) under(outer wrapperText) wrapperText {
	return wrapperText{before: outer.before + w.before, after: w.after + outer.after}
}

// context returns the text of w as [ErrorTree.Context] reports it. The
// text in front of the message comes first, then the text behind it. Each
// part leaves out the white space and the colon that joined it to the
// message. The two parts join with a colon when either had one there, and
// with a space otherwise.
func (w wrapperText) context() string {
	before, after := strings.TrimSpace(w.before), strings.TrimSpace(w.after)
	colon := strings.HasSuffix(before, ":") || strings.HasPrefix(after, ":")

	before = strings.TrimSpace(strings.TrimSuffix(before, ":"))
	after = strings.TrimSpace(strings.TrimPrefix(after, ":"))

	switch {
	case before == "":
		return after
	case after == "":
		return before
	case colon:
		return before + ": " + after
	default:
		return before + " " + after
	}
}

// wrapper is an error that wraps one error and writes text of its own
// around the text of that error, as a [fmt.Errorf] call with one %w verb
// does. A [fmt.Errorf] wrapper with several %w verbs is a wrapper too when
// [followBranches] returns one of its branches as the next link. An
// [*Error] that carries the text of the headings that stood above its
// problem is a wrapper as well, as [underHeadings] builds one. It does not
// write that text, so the wrapper holds the text in carried and does not
// hold an error.
type wrapper struct {
	err, next error
	carried   wrapperText
	// The branches in front of next and those behind it, in the order that
	// err unwraps to them, for an err that holds several errors.
	front, back []error
}

// added returns the text that w wrote in front of the text of the error
// it wraps and behind it. The text of a wrapper with several %w verbs holds
// the text of each branch in order. The search for the text of the wrapped
// error thus runs between the branches beside it. A wrapper that does not
// hold the text of the error it wraps rewrote that text. Its whole text
// then stands in front, behind a colon, as the text of a wrapper from
// [fmt.Errorf]("%s: %w", text, err) does. So does the text of a wrapper
// that [cutAround] cannot split. A wrapper that carries its text returns
// that text.
func (w wrapper) added() (string, string) {
	if w.err == nil {
		return w.carried.before, w.carried.after
	}

	text, inner := w.err.Error(), w.next.Error()

	lo, hi := w.between(text)
	if before, after, ok := cutAround(text[lo:hi], inner); ok {
		return text[:lo] + before, after + text[hi:]
	}

	// The branches beside the wrapped error did not stand in order around
	// its text, so the search takes the whole text.
	if before, after, ok := cutAround(text, inner); ok {
		return before, after
	}

	return text + ": ", ""
}

// between returns the bounds of the part of text that holds the text of
// the wrapped error. The text is that of w. The part starts behind the
// text of each branch in front of the wrapped error. It ends in front of
// the text of each branch behind that error. The search for a branch in
// front starts at the end of the branch before it. The search for a branch
// behind runs from the end of text. The bounds are those of the whole text
// when w does not hold a branch beside the wrapped error. So are the
// bounds of a text that does not hold the text of each such branch in
// order.
func (w wrapper) between(text string) (int, int) {
	lo, hi := 0, len(text)

	for _, branch := range w.front {
		if isNothing(branch) {
			continue
		}

		msg := branchText(branch)

		i := strings.Index(text[lo:], msg)
		if i < 0 {
			return 0, len(text)
		}

		lo += i + len(msg)
	}

	for _, branch := range slices.Backward(w.back) {
		if isNothing(branch) {
			continue
		}

		i := strings.LastIndex(text[lo:hi], branchText(branch))
		if i < 0 {
			return 0, len(text)
		}

		hi = lo + i
	}

	return lo, hi
}

// branchText returns the text that [fmt.Errorf] writes for branch at a %w
// verb, which is the message of branch. For a nil pointer whose Error
// method panics, the fmt package writes "<nil>" in place of a message, and
// so does branchText.
func branchText(branch error) string {
	return fmt.Sprint(branch)
}

// cutAround returns the text in front of inner in text and the text behind
// it, and whether it found the place of inner. A text that holds inner in
// several places is ambiguous. Most wrappers write their text in front of
// the error that they wrap. The last inner is thus the first choice when
// only a joiner stands behind it, as [isJoiner] reports. The first inner
// is the second choice when only a joiner stands in front of it. A text
// that holds inner in several places and at neither end does not split.
func cutAround(text, inner string) (string, string, bool) {
	if i := strings.LastIndex(text, inner); i >= 0 && isJoiner(text[i+len(inner):]) {
		return text[:i], text[i+len(inner):], true
	}

	if i := strings.Index(text, inner); i >= 0 && isJoiner(text[:i]) {
		return text[:i], text[i+len(inner):], true
	}

	if strings.Count(text, inner) != 1 {
		return "", "", false
	}

	return strings.Cut(text, inner)
}

// isJoiner reports whether s holds only the text that joins the text of a
// wrapper to the message beside it. That text is white space around one
// colon at most, and [wrapperText.context] leaves it out. An empty s is a
// joiner.
func isJoiner(s string) bool {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), ":")) == ""
}

// problemOf returns the error on the cause chain of err whose text is the
// message of the problem that err stands for. It also returns the wrappers
// above that error, outermost first. That error is the innermost [*Error]
// on the chain. A [*SourceError] ends the walk, because a binding answers
// for the errors that it binds. So does an error with several problems
// below it, which is a heading. For a chain that does not hold any of
// those, the whole text is the message, so problemOf returns a nil error
// and no wrappers. An Error that carries the text of headings counts as a
// wrapper above itself, in the place that those headings had.
func problemOf(err error) ([]wrapper, error) {
	var (
		wrappers []wrapper
		core     error
		above    int
	)

	// The walk sets core at each candidate and counts the wrappers above
	// it, so a wrapper below the last candidate stays out of the result.
	// That wrapper might not wrap any error.
	follow := func(w wrapper) error {
		wrappers = append(wrappers, w)

		return w.next
	}

	for cur := err; !isNothing(cur); {
		switch x := cur.(type) { //nolint:errorlint // Walks the chain one node at a time.
		case *SourceError:
			return wrappers, cur

		case *Error:
			if x.headings != (wrapperText{}) {
				wrappers = append(wrappers, wrapper{carried: x.headings})
			}

			core, above = cur, len(wrappers)
			cur = x.err

		case interface{ Unwrap() error }:
			cur = follow(wrapper{err: cur, next: x.Unwrap()})

		case interface{ Unwrap() []error }:
			branches := x.Unwrap()

			kept, at := keepBranches(cur, branches)
			if at >= 0 {
				cur = follow(wrapper{err: cur, next: branches[at], front: branches[:at], back: branches[at+1:]})

				continue
			}

			if len(kept) > 0 {
				return wrappers, cur
			}

			return wrappers[:above], core

		default:
			return wrappers[:above], core
		}
	}

	return wrappers[:above], core
}

// underHeadings returns err with the text above carried on each of its
// problems. The wrappers of the headings that stood over err wrote that
// text. A tree that holds the result outside those headings then reports
// the context that each problem had below them. The result has the text,
// the locations, and the children of err. An err that did not stand below
// such a wrapper stays as it is.
//
// A problem gets an [*Error] above it that adds the text alone. So does a
// heading with a row of its own, such as a wrapper around a join.
// [inheritContext] gives each problem below such a heading the text that
// the heading carries, in front of the text of the heading itself. A
// binding carries the text as [SourceError.underHeadings] describes.
//
// Every other heading keeps its place and passes the text to the errors
// that it heads. A join and a binding without a row do not have a node in
// the tree that can carry the text. Neither does an Error around a
// heading, nor a wrapper that does not write text around the heading that
// it wraps.
func underHeadings(err error, above wrapperText) error {
	if above == (wrapperText{}) || isNothing(err) {
		return err
	}

	carry := func(branch error) error { return underHeadings(branch, above) }

	switch x := err.(type) { //nolint:errorlint // The node itself, not a chain search.
	case *SourceError:
		return x.underHeadings(above)

	case *Error:
		switch {
		case x.headings != (wrapperText{}):
			// The Error carries the text of a heading that stood below
			// those of above, so the text above goes on top of it.

		case heads(x.err):
			c := *x
			c.err = carry(x.err)
			c.details = slices.Clone(x.details)

			return &c
		}
	}

	joined := mapJoin(err, carry)
	if joined != nil {
		return joined
	}

	// A wrapper that does not write text around a heading does not have a
	// row either. A stand-in for it thus wraps the heading with the text.
	w, ok := err.(interface{ Unwrap() error }) //nolint:errorlint // The node itself, not a chain search.
	if ok {
		inner := w.Unwrap()

		msg := err.Error()
		if heads(inner) && msg == inner.Error() {
			return multierr.Replace(err, carry(inner), msg, nil)
		}
	}

	return &Error{err: err, headings: above}
}

// underHeadings returns a copy of e that carries the text above, as the
// function [underHeadings] carries it on an error. A binding with a row of
// its own carries the text on the error that it binds, as
// [SourceError.remarked] puts a severity there. The copy keeps the location
// and the children of e. A binding without a row gives the text to each
// problem that it heads, and its details stay as they are. The error that
// [BindValue] bound carries the text too, so a document that places that
// error later keeps the text.
func (e *SourceError) underHeadings(above wrapperText) *SourceError {
	c := e.copied()
	c.free = underHeadings(e.free, above)

	if !e.headsAlone() {
		c.err = &Error{err: e.err, headings: above}

		return c
	}

	c.below = make([]boundChild, 0, len(e.below))

	for _, child := range e.below {
		if !child.detail {
			child.bound = child.bound.underHeadings(above)
		}

		c.below = append(c.below, child)
	}

	return c
}

// ownMessage returns the text of err without the part that the errors
// below it show, as [chainEnd.cut] leaves it. A join does not have text of
// its own, so its message is empty.
func ownMessage(err error) string {
	// The part of the message the children show depends on the end of the
	// cause chain, so the walk finds that end alone.
	end := walkChildren(err, func(*SourceError, childBase) {}, func(error, childBase, bool) {})

	own, _, _, _ := end.cut(err.Error())

	return own
}

// wrapperMessage returns the message of err, which bound is or wraps along
// its cause chain. The message of a wrapper that keeps the line of bound
// is its text with the message of bound in place of that line, as
// [wrapLine] finds it. A wrapper around the binding of a join has its own
// text as its message, because the join does not have a line. Any other
// err has the message of bound.
func wrapperMessage(err error, bound *SourceError) string {
	x, self := err.(*SourceError) //nolint:errorlint // The node itself, not a chain search.
	if err == nil || (self && x == bound) {
		return bound.Message()
	}

	// The part of the message the children show depends on the end of the
	// cause chain, so the walk finds that end alone.
	end := walkChildren(err, func(*SourceError, childBase) {}, func(error, childBase, bool) {})

	own, _, _, cut := end.cut(err.Error())

	rows, msg := wrapLine(own, bound, bound.Message())
	if cut && rows != nil {
		return msg
	}

	if joinBinding(bound) != nil {
		return bareMessage(err, end)
	}

	return bound.Message()
}

// Path returns the [paths.Path] the node is about and true, or the zero
// Path and false when the node does not name one.
//
//   - For a node with a binding, it is the [SourceError.Path] of Bound.
//   - For a node that is not bound to a source, it is the path of the
//     located [*Error] nearest the problem along the cause chain of Err,
//     as [Locate] describes. Path puts the base of every [Rebase] on the
//     way in front of that path. A problem that does not carry a location
//     takes the path of the nearest heading above it that carries one,
//     unless a [Rebase] lies between them. The Errors section of the
//     package documentation describes a location on a heading. The label
//     of the node puts the same path in front of the message.
//
// The source and the position come from Bound, which resolved them, and
// [ErrorTree.Source] and [ErrorTree.Position] read them for any node. An
// [*Error] that is not bound to a source carries a position that does not
// point into any source. So the node leaves that position to
// [Error.Position].
func (t ErrorTree) Path() (paths.Path, bool) {
	if t.Bound != nil {
		return t.Bound.Path()
	}

	a := anchorOf(t.Err)

	return a.path, a.hasPath
}

// Source returns the [*Source] the node is bound to, as [SourceError.Source]
// returns it for Bound, or nil for a node that is not bound to a source.
// Source also returns nil for a node of an error that [Detach] detached,
// but [ErrorTree.Name] returns the name its source had.
func (t ErrorTree) Source() *Source {
	return t.Bound.Source()
}

// Name returns the name of the source the node is bound to, as
// [SourceError.Name] returns it for Bound, or "" for a node that is not
// bound to a source. A node of an error that [Detach] detached keeps the
// name but not the [Source], so a report reads the name of each problem
// here.
func (t ErrorTree) Name() string {
	return t.Bound.Name()
}

// FilePath returns the file path of the source the node is bound to, as
// [SourceError.FilePath] returns it for Bound, or "" for a node that is
// not bound to a source. A node of an error that [Detach] detached keeps
// the file path. [ErrorTree.Name] spells the file as the program named it,
// for a person who reads the report. FilePath is the path that
// [Source.FilePath] reports, for a tool that opens the file. It is the
// absolute path that [NewSourceFromFile] sets, the path that
// [WithFilePath] sets, as given, or the path in the file system of
// [NewSourceFromFS]. A detached node does not keep that file system.
func (t ErrorTree) FilePath() string {
	return t.Bound.FilePath()
}

// Position returns the [position.Position] the location of the node
// resolved to and true, as [SourceError.Position] returns it for Bound. It
// returns the zero Position and false for a node that is not bound to a
// source, and for one whose location did not resolve.
func (t ErrorTree) Position() (position.Position, bool) {
	return t.Bound.Position()
}

// Range returns the [position.Range] the location of the node resolved to
// and true, as [SourceError.Range] returns it for Bound. It returns the
// zero Range and false for a node that is not bound to a source, and for
// one whose location did not resolve.
func (t ErrorTree) Range() (position.Range, bool) {
	return t.Bound.Range()
}

// DocumentIndex returns the 0-indexed position within the file of the
// document the node is bound to and true, as [SourceError.DocumentIndex]
// returns it for Bound. It returns 0 and false for a node that is not
// bound to a source, and for a binding that does not hold a document.
func (t ErrorTree) DocumentIndex() (int, bool) {
	return t.Bound.DocumentIndex()
}

// DocumentSpan returns the lines of the document the node is bound to and
// true, as [SourceError.DocumentSpan] returns them for Bound. A report
// reads the first line of the span for a node that [ErrorTree.Position]
// reports false for, such as a document that no schema matches. It returns
// the zero Span and false where [ErrorTree.DocumentIndex] reports false,
// and for a binding in a Source with positions off.
func (t ErrorTree) DocumentSpan() (position.Span, bool) {
	return t.Bound.DocumentSpan()
}

// errorTreeJSON holds the fields that [ErrorTree.MarshalJSON] writes for
// one node.
type errorTreeJSON struct {
	Message  string          `json:"message"`
	Context  string          `json:"context,omitempty"`
	Name     string          `json:"name,omitempty"`
	FilePath string          `json:"filePath,omitempty"`
	Path     string          `json:"path,omitempty"`
	Range    *position.Range `json:"range,omitempty"`
	Children []ErrorTree     `json:"children,omitempty"`
	Warning  bool            `json:"warning,omitempty"`
	Detail   bool            `json:"detail,omitempty"`
}

// MarshalJSON implements [json.Marshaler]. It writes the node as an object
// whose fields come from the accessors of the node, so the object agrees
// with them:
//
//   - "message" holds [ErrorTree.Message].
//   - "context" holds [ErrorTree.Context] and is left out when empty.
//   - "name" and "filePath" hold [ErrorTree.Name] and
//     [ErrorTree.FilePath] and are each left out when empty, as for a
//     node that is not bound to a source or a source without a file
//     path.
//   - "path" holds the text of [ErrorTree.Path] and is left out when the
//     node does not name a path.
//   - "range" holds [ErrorTree.Range] and is left out when the location of
//     the node did not resolve.
//   - "children" holds the Children, each written the same way, and is
//     left out when the node does not have any.
//   - "warning" and "detail" hold the Warning and Detail fields and are
//     left out when false.
//
// A heading and a problem write the same fields. The range counts lines
// and columns from 0 and is half-open, like every [position.Range]. The
// Row counts them from 1, as an editor does, so a problem that the Row
// shows at 2:7 writes a range that starts at {"line":1,"col":6}. The
// object leaves out the Row and [ErrorTree.Position]. For a position from
// [AtPosition], the range starts where the content of the token starts. It
// can begin before the position that Position returns and the Row shows,
// so a report that needs that exact position reads Position.
//
// A program that writes each problem of an error as a line of JSON encodes
// the nodes that [Problems] yields:
//
//	enc := json.NewEncoder(w)
//	for problem := range niceyaml.Problems(err) {
//		if err := enc.Encode(problem); err != nil {
//			return err
//		}
//	}
//
// A [log/slog] handler for JSON writes a slice of nodes through
// MarshalJSON, as [Error.LogValue] describes.
func (t ErrorTree) MarshalJSON() ([]byte, error) {
	out := errorTreeJSON{
		Message:  t.Message(),
		Context:  t.Context(),
		Warning:  t.Warning,
		Detail:   t.Detail,
		Name:     t.Name(),
		FilePath: t.FilePath(),
		Children: t.Children,
	}

	if path, ok := t.Path(); ok {
		out.Path = path.String()
	}

	if rng, ok := t.Range(); ok {
		out.Range = &rng
	}

	// The encoder leaves <, >, and & as they are. The encoding/json
	// package compacts the bytes of a Marshaler with the escaping that its
	// caller set. A caller that wants them escaped still gets them escaped.
	var buf bytes.Buffer

	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)

	err := enc.Encode(out)
	if err != nil {
		return nil, fmt.Errorf("marshal tree: %w", err)
	}

	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// bindingOf returns the binding err is or wraps along its cause chain, or
// nil. A located [*Error] above a binding whose location did not resolve
// anchors the chain itself, so bindingOf returns nil for such an error.
// An Error that names the path of that binding is the exception. The line
// of the binding shows that path already, so bindingOf returns the
// binding, a summary included. Other located Errors can sit above the
// Error that anchors err, as when a helper and its caller both locate an
// error. The anchor is the located Error nearest the binding, so the
// location of each Error above it does not show, whatever it names.
func bindingOf(err error) *SourceError {
	a := anchorOf(err)
	if bound, ok := a.err.(*SourceError); ok { //nolint:errorlint // The anchor itself, found by the walk.
		return bound
	}

	located, ok := a.err.(*Error) //nolint:errorlint // The anchor itself, found by the walk.
	if !ok || !a.hasPath {
		return nil
	}

	bound := bindingBelow(located.err)
	if bound == nil {
		return nil
	}

	if path, hasPath := bound.Path(); hasPath && path.Equal(a.path) {
		return bound
	}

	return nil
}

// bindingBelow returns the binding that the cause chain of err reaches
// through the located [*Error]s along it, or nil. It looks below each
// Error that anchors the chain, whatever its location.
func bindingBelow(err error) *SourceError {
	for {
		switch x := anchorOf(err).err.(type) { //nolint:errorlint // The anchor itself, found by the walk.
		case *SourceError:
			return x
		case *Error:
			err = x.err
		default:
			return nil
		}
	}
}

// appendTrees appends the nodes that err stands for to dst. A join
// appends its branches straight to dst rather than copy them up one level
// at a time. A loop of [errors.Join] calls nests each join in the next, so
// a copy at every level takes quadratic time in total.
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

	// The Errors above a binding of a join add nothing but the fault that
	// they declare, and that fault reaches each problem below the binding.
	base, peeled := peel(err)

	bound := joinBinding(peeled)
	if bound != nil {
		for _, t := range trees(appendBoundChildren(nil, bound, true)) {
			dst = appendTree(dst, base.markTree(t))
		}

		return dst
	}

	kids, end := children(err)
	node := ErrorTree{Err: err, Bound: bindingOf(err)}

	x, binds := err.(*SourceError) //nolint:errorlint // The node itself, not a chain search.
	if binds {
		node.Row = x.headRow()
	} else {
		text, _, _, cut := end.cut(err.Error())
		node.Row = ErrorRow{Text: text}

		// A wrapper around a binding that keeps the line of the binding
		// writes that line, with its own text around the message. The
		// line holds the message of the wrapper, so it takes the severity
		// of the wrapper rather than that of the binding.
		rows, msg := wrapLine(text, node.Bound, node.Bound.Message())
		if cut && rows != nil {
			warns := isWarning(err) && !(ErrorTree{Children: kids}).heads()
			node.Row = rows.lineAs(msg, warns)
		}

		// A heading with no text of its own has no row, like its binding,
		// because each problem below it that carries no location names the
		// heading's location.
		if !node.Row.IsZero() || !(ErrorTree{Children: kids}).heads() {
			node.Row = unboundRow(err, node.Row)
		}
	}

	t := newTree(node, kids)

	// A binding writes the label of a warning into its own row, so only a
	// problem that no binding holds gets it here.
	if !binds && !node.Row.IsZero() && t.Warning && t.isProblem() && !holdsBinding(err) {
		t.Row.Severity = warningSeverity
	}

	return appendTree(dst, t)
}

// holdsBinding reports whether a [*SourceError] lies on the cause chain of
// err. When one does, the text of err holds the text of the binding.
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

// unboundRow gives row the path of the located [*Error] nearest the
// problem on the cause chain of err, as a binding does. The binding that
// [bindingOf] finds for err names its own path, so row then stays as it
// is.
func unboundRow(err error, row ErrorRow) ErrorRow {
	if bindingOf(err) != nil {
		return row
	}

	if a := anchorOf(err); a.hasPath {
		row.Path = a.path.String()
	}

	return row
}

// appendTree appends t to dst and returns the extended slice. For a node
// that does not have text, it appends the children of t in place of t.
func appendTree(dst []ErrorTree, t ErrorTree) []ErrorTree {
	if t.Row.IsZero() {
		return append(dst, t.Children...)
	}

	return append(dst, t)
}

// joinBinding returns the binding of a join that err reads as, or nil.
// A binding that heads problems and does not have a line of its own reads
// as a join too, as one of a join with a location does. An Error that
// does not add anything keeps the text and the children of the error it
// wraps, so it reads as that error. So does a binding that adopted such
// an Error.
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

			if _, _, joined := joinBranches(x.err); joined || x.headsAlone() {
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

// peel strips the [*Error]s on top of err that do not add anything and
// returns what remains, with the base the errors below them take. The
// base takes the fault the stripped Errors declare, as
// [declaration.under] passes it down.
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
// binding that does not have a line of its own name their source, because
// the rows above them do not name it. The fault of the Errors above a
// binding reaches each problem below it, as [childBase.markTree] puts it.
func children(err error) ([]ErrorTree, chainEnd) {
	var kids []positioned

	end := walkChildren(err,
		func(x *SourceError, base childBase) {
			from := len(kids)
			kids = appendBoundChildren(kids, x, x.headline() == "")

			for i := from; i < len(kids); i++ {
				kids[i].tree = base.markTree(kids[i].tree)
			}
		},
		func(n error, base childBase, detail bool) {
			tree := buildTree(base.rebase(n, detail))
			if detail {
				tree = asDetail(tree)
			}

			kids = append(kids, positioned{tree: tree})
		},
	)

	return trees(kids), end
}

// asDetail returns t with Detail set. A node that does not have text
// gives its place to its children in the tree above it. So each of those
// children gets Detail set instead.
func asDetail(t ErrorTree) ErrorTree {
	if !t.Row.IsZero() {
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
// it, when the errors below that error are children. The error is a
// [*SourceError], or an error whose branches the walk reported. The zero
// chainEnd does not end at either.
type chainEnd struct {
	err error
	// The number of branches that the walk reported as children.
	branches int
	// Whether an error on the way to err added text of its own. When one
	// did, the message that the walk started at differs from the message
	// of err.
	wrapped bool
}

// cut returns text without the part that the children show. The text is
// the message of the error that the chain belongs to. The first count is
// the number of trailing children whose text the result holds, and the
// second is the number whose text the input held. It returns text whole
// and false when a wrapper rewrote the message it wraps.
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
	onBinding func(x *SourceError, base childBase),
	onChild func(n error, base childBase, detail bool),
) chainEnd {
	var (
		base    childBase
		wrapped bool
	)

	for cur := err; !isNothing(cur); {
		switch x := cur.(type) { //nolint:errorlint // Walks the chain one node at a time.
		case *SourceError:
			onBinding(x, base)

			return chainEnd{err: x, wrapped: wrapped}

		case *Error:
			base = base.cross(x)

			for _, n := range x.errors {
				onChild(n, base, false)
			}

			for _, n := range x.details {
				onChild(n, base, true)
			}

			// The children below a located Error stand in the error that
			// it wraps.
			base.inLocated = base.inLocated || x.hasLocation()

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
	kept, at := keepBranches(err, branches)
	if at >= 0 {
		return nil, branches[at]
	}

	return kept, nil
}

// keepBranches returns the branches that [followBranches] keeps. It also
// returns the index in branches of the one that followBranches returns as
// the next link, or -1 when err does not have a next link.
func keepBranches(err error, branches []error) ([]error, int) {
	// The type check runs first, so an error of any other type never
	// formats its message here, where the result would go unused.
	wrapper := multierr.IsWrapErrors(err) && !multierr.JoinMessage(err.Error(), branches)

	var (
		kept []error
		at   int
	)

	for i, branch := range branches {
		if isNothing(branch) || (wrapper && isLeaf(branch)) {
			continue
		}

		kept, at = append(kept, branch), i
	}

	if wrapper && len(kept) == 1 {
		return kept, at
	}

	return kept, -1
}

// isLeaf reports whether err reads as a sentinel rather than a problem
// when it is a branch of a wrapper with several %w verbs. A sentinel does
// not carry a location or children on its cause chain, as [anchorOf] and
// [walkChildren] find them. Callers report problems with [NewError],
// [NewWarning], [Invalid], and [Unchecked]. Each of those sets the fault
// or the severity of the [*Error] it builds. So a sentinel does not hold
// an Error whose fault or severity is set either. A rare sentinel built
// with one of those functions reads as a problem.
//
// An error from [errors.New] reads as a sentinel too. The function walks
// the chain one time, because running anchorOf and walkChildren both
// doubles the work at each nesting level.
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

// childBase is the base the children on a cause chain rebase under. Even
// a Rebase at the root locates a problem that does not carry a location.
// So a chain rebases its children whenever it holds a Rebase. The base
// also carries the location of the nearest located heading. Each problem
// that does not carry a location takes that location.
type childBase struct {
	// The joined move of every Error from Rebase above the children, or
	// nil when the walk did not meet one.
	move *move
	// The nearest Error above the children that carries a location, or nil
	// when the walk did not meet one or met a Rebase below it. Each
	// problem below that does not carry a location of its own takes that
	// location, as [childBase.inherit] gives it.
	at *Error
	// The joined move of every Error from Rebase above at and of at
	// itself, which moves the location that the problems take from at.
	atMove *move
	// The joined move of every Error from Rebase between at and the
	// children, or nil when the walk did not meet one. Only an at that
	// replaces across a Rebase lies above one.
	under *move
	// Whether at replaces the location of each located Error below it on
	// the chain, as [errorConfig.relocates] marks it. A [Rebase] below
	// ends the replacement, unless crosses is set.
	replacing bool
	// Whether the replacement of at goes on across each Rebase below, as
	// [errorConfig.crosses] marks it.
	crosses bool
	// Whether the replacement of at also replaces the base of a Rebase
	// below, as [errorConfig.replacesBases] marks it.
	replacesBases bool
	// Whether the walk crossed a Rebase below at whose base at does not
	// replace, with no located Error between that Rebase and the
	// children. Each problem below that does not carry a location then
	// binds at the base of the Rebase.
	atBase bool
	// The fault that the Errors above the children declare, as
	// [declaration.under] passes it down.
	fault declaration
	// Whether the children stand in the error that an Error with a
	// location wraps, as the err of a [Locate] result does. The children
	// of that Error itself, such as the details of its options, do not.
	inLocated bool
}

// cross returns the base below x. The base is c with the move of x after
// its own, as [move.then] joins them. It also takes the fault that x
// passes down, as [declaration.under] gives it. A location on x becomes
// the location that the children below inherit, unless an Error above x
// replaces it. A Rebase ends the replacement, so a location on the Rebase
// itself becomes the location that the children inherit, as [findAnchor]
// lets it win for the line of the heading. A Rebase that does not carry a
// location points the children below it at its base, so it ends what
// they inherit from above. An Error with the mark of [replacing] keeps
// its location for the children across a Rebase, as findAnchor keeps it
// for the line of the heading. An Error with the mark of [relocating]
// keeps it across a Rebase only when a located Error lies below that
// Rebase. Its location replaces only the locations that it meets, so the
// children below a Rebase without one bind at its base. The base
// below x stands in the error of a located Error when c does.
func (c childBase) cross(x *Error) childBase {
	next := c
	next.move = c.move.then(x.move)
	next.fault = c.fault.under(x)

	locate := func() {
		next.at, next.atMove, next.under, next.atBase = x, next.move, nil, false
		next.replacing, next.crosses, next.replacesBases = x.relocates, x.crosses, x.replacesBases
	}

	switch {
	case c.crosses:
		if x.move != nil {
			next.under = c.under.then(x.move)
			next.atBase = next.atBase || (!x.move.movesOnly && !x.move.repeats && !c.replacesBases)
		}

		// The location of at replaces the location of x, so the base of
		// a Rebase above x does not reach the children.
		if x.hasLocation() {
			next.atBase = false
		}

	case x.move != nil:
		next.at, next.atMove, next.under, next.atBase = nil, nil, nil, false
		next.replacing, next.crosses, next.replacesBases = false, false, false

		if x.hasLocation() {
			locate()
		}

	case x.hasLocation() && !c.replacing:
		locate()

	case x.hasLocation():
		// The location of at replaces the location of x, so it also
		// replaces each location that x replaces.
		next.crosses, next.replacesBases = x.crosses, x.replacesBases
	}

	return next
}

// rebase returns n under the base. A detail explains the error above it,
// so the base moves its paths alone, never marks it invalid, and does not
// give it a location. A problem that does not carry a location takes the
// location of the heading above it, as [childBase.inherit] gives it. A
// problem below an Error that matches [errInvalid] is the document's
// fault too, unless the fault is one a hook presumes and n holds an Error
// from [Unchecked]. A problem below an Error from Unchecked takes the mark
// of Unchecked, so a fault presumed later does not reach it apart from
// that Error either.
func (c childBase) rebase(n error, detail bool) error {
	d, m := c.fault, c.move
	if detail {
		d = undeclared
	} else {
		n = c.inherit(n)

		// The moves below at applied to n already.
		if c.under != nil {
			m = c.atMove
		}
	}

	if m == nil {
		return markFault(n, d)
	}

	moved := *m
	moved.movesOnly = moved.movesOnly || detail

	return rebase(n, moved, d)
}

// markTree returns t with the declaration of the base on the error of each
// node that is not a detail, as [markFault] puts it on that error. A node
// whose error declares a fault already keeps its error. A node whose error
// holds an Error from [Unchecked] stops a presumed fault, so the nodes
// below it stay as they are, as [declaration.under] stops it on a chain.
// Below an Unchecked, each node takes its mark, so a fault presumed later
// does not reach it either.
func (c childBase) markTree(t ErrorTree) ErrorTree {
	d := c.fault.on(t.Err)
	if d == undeclared || t.Detail {
		return t
	}

	if !declaresInvalid(t.Err) {
		t.Err = markFault(t.Err, d)
	}

	if len(t.Children) > 0 {
		kids := make([]ErrorTree, len(t.Children))
		for i, kid := range t.Children {
			kids[i] = childBase{fault: d}.markTree(kid)
		}

		t.Children = kids
	}

	return t
}

// inherit returns n with the location of the heading above it on each
// problem of n for which [anchorOf] does not find a location. Each such
// problem goes inside an Error that carries only that location, as one
// from [Locate] does. A join stays a join, so the tree still reads it as
// one and each of its branches takes the location on its own. Any other
// heading n gives the location to each of its own problems that does not
// carry one. A [BindValue] binding takes the location as the error it was
// made from, as a document places it. The moves of the base above the
// heading apply to the location as they apply to the heading. Only a
// heading with the mark of [replacing] or [relocating] lies above a Rebase
// that lies above n. The moves of that Rebase apply to each problem of n.
// The Error that carries the location of the heading goes above them, so
// they do not move that location. A problem below a Rebase whose base the
// heading does not replace binds at that base instead, as
// [childBase.cross] describes.
func (c childBase) inherit(n error) error {
	if c.at == nil || isNothing(n) {
		return n
	}

	// An Error that adds nothing to the join it wraps stays around the
	// join, so the copy keeps what the Error declares.
	x, ok := n.(*Error) //nolint:errorlint // The node itself, not a chain search.
	if ok && x.addsNothing() {
		if _, _, joined := joinBranches(x.err); joined {
			kept := *x
			kept.err = c.inherit(x.err)

			return &kept
		}
	}

	joined := mapJoin(n, c.inherit)
	if joined != nil {
		return joined
	}

	placed := placeable(n)
	located := anchorOf(placed).err != nil

	if c.under != nil {
		under := *c.under
		under.repeats = true
		n, placed = rebase(n, under, undeclared), rebase(placed, under, undeclared)
	}

	if located || c.atBase {
		return n
	}

	e := &Error{
		err: placed,
		loc: c.at.loc, path: c.at.path, data: c.at.data, hasPath: c.at.hasPath,
	}

	// The location of the heading replaces the location that the Rebase
	// below it gives the problem, as it does for the heading.
	if c.under != nil {
		e.relocates, e.crosses, e.replacesBases = true, true, c.replacesBases
	}

	return e
}

// trees returns the nodes of kids in position order within the source
// that each is bound to. The nodes whose location did not resolve come
// after the rest, in the order that kids lists them. It returns nil when
// kids is empty.
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
// Each child in the source of its parent drops the name that the parent
// row shows, and reads "line:col: msg". The caller sets named for a
// parent that does not have a row, and each child then keeps the name.
func appendBoundChildren(kids []positioned, bound *SourceError, named bool) []positioned {
	from := len(kids)
	kids = flatten(kids, bound.below, bound, named, false)

	for i := from; i < len(kids); i++ {
		kid := &kids[i]
		child, parent := kid.bound, kid.parent

		// Put the position back without the name. A child behind a wrapper
		// that rewrote its line carries its position in its text, so its
		// row does not have a location to replace.
		row := child.headRow()
		if !kid.named && child.origin() == parent.origin() && row.Location != "" {
			switch {
			case kid.located:
				row.Location = kid.pos.OneBased()

			case !sameDocument(child, parent):
				// The parent names another document of the source, or
				// none, so the child keeps the one it wrote, with its line.
				row.Location = child.documentPlace()

			default:
				row.Location = ""
			}
		}

		below, _ := children(child)

		kid.tree = newTree(ErrorTree{Err: child, Bound: child, Row: row}, below)
		if kid.detail {
			kid.tree = asDetail(kid.tree)
		}

		kid.tree.Detail = kid.tree.Detail || kid.underDetail
	}

	return kids
}

// flatten appends children to kids. The slice children holds the children
// of the binding parent. A child that binds a join does not have a line
// of its own. So the children of the join take its place, with the join
// as their parent. The caller sets filed when the binding of a join of
// bindings holds parent, and each child then comes from that binding.
func flatten(kids []positioned, children []boundChild, parent *SourceError, named, filed bool) []positioned {
	filed = filed || parent.joinsBindings()

	for _, c := range children {
		// The branches sit beside the other children, so they sort by
		// position among them rather than under a node of their own.
		joined := joinBinding(c.bound)
		if joined != nil {
			from := len(kids)
			kids = flatten(kids, joined.below, joined, named || joined.origin() != parent.origin(), filed)

			for i := from; c.detail && i < len(kids); i++ {
				kids[i].underDetail = true
			}

			continue
		}

		kid := placed(c.bound)
		kid.parent, kid.named, kid.detail, kid.filed = parent, named, c.detail, filed
		kids = append(kids, kid)
	}

	return kids
}

// positioned is a child node with the source and the position, when
// located, that [arrange] sorts it by. [arrange] sets group.
type positioned struct {
	src excerptSource
	// The binding of the child, or nil for an error that is not bound to a
	// source.
	bound *SourceError
	// The binding whose children hold the child, as [flatten] finds it.
	parent *SourceError
	tree   ErrorTree
	pos    position.Position
	group  int
	// The index of the document the child falls in, when inDoc.
	doc int
	// Whether the child names its source in its line, as [flatten]
	// decides.
	named   bool
	located bool
	// Whether the child is a detail of the binding that holds it.
	detail bool
	// Whether a detail that binds a join holds the child.
	underDetail bool
	// Whether the child falls in the document at index doc.
	inDoc bool
	// Whether the binding of a join of bindings holds the child, as
	// [flatten] finds it.
	filed bool
	// Whether the children of the group sort by document, as [arrange]
	// decides.
	byDoc bool
}

// placed returns child with the source and the position that [trees]
// orders it by. A child counts as located only when its range resolves,
// because the excerpt marks only a range that resolves. The position is
// the one that the message of child carries.
func placed(child *SourceError) positioned {
	kid := positioned{src: child.origin(), bound: child}
	kid.doc, kid.inDoc = child.DocumentIndex()

	if _, ok := child.Range(); ok {
		kid.located = true
		kid.pos = child.loc.pos
	}

	return kid
}

// arrange sorts kids in place by source, in the order that the sources
// first appear, and then by position. It sorts by source first because a
// line number means something only within its own source. When each
// child of a source comes from the binding of a join of bindings, as
// [SourceError.joinsBindings] reports, and each one with a position falls
// in a document, the children of that source sort by document before
// position. A child without a position then stays in its document, after
// the children there with one. The children of a file in which several
// documents fail thus read in file order.
func arrange(kids []positioned) []positioned {
	seen := make([]excerptSource, 0, 1)
	// Whether a child of the group keeps the group from sorting by
	// document.
	outside := make([]bool, 0, 1)

	for i := range kids {
		group := slices.Index(seen, kids[i].src)
		if group < 0 {
			group = len(seen)
			seen = append(seen, kids[i].src)
			outside = append(outside, false)
		}

		kids[i].group = group

		if !kids[i].filed || (kids[i].located && !kids[i].inDoc) {
			outside[group] = true
		}
	}

	for i := range kids {
		kids[i].byDoc = !outside[kids[i].group]
	}

	slices.SortStableFunc(kids, comparePositioned)

	return kids
}

// comparePositioned orders children by source group, then by document
// where [arrange] set byDoc, and then by position. A child without a
// position sorts after those with one in its document. A child that does
// not have a position and does not sort by document comes after the rest.
// Such children compare equal among themselves, so a stable sort keeps
// their order.
func comparePositioned(a, b positioned) int {
	switch ra, rb := a.trailing(), b.trailing(); {
	case ra && rb:
		return 0
	case ra:
		return 1
	case rb:
		return -1
	}

	// The children of one group both sort by document or neither does.
	switch {
	case a.group != b.group:
		return a.group - b.group
	case a.byDoc && a.doc != b.doc:
		return a.doc - b.doc
	case !a.located && !b.located:
		return 0
	case !a.located:
		return 1
	case !b.located:
		return -1
	default:
		return a.pos.Compare(b.pos)
	}
}

// trailing reports whether the child sorts after the children that
// [comparePositioned] places, because it does not have a position and
// does not sort by document.
func (p positioned) trailing() bool {
	return !p.located && (!p.byDoc || !p.inDoc)
}

// newTree returns node with children below it. Each child that does not
// have text gives its place to its own children. A node that does not
// have text and holds a single child is that child.
func newTree(node ErrorTree, children []ErrorTree) ErrorTree {
	if len(children) == 0 {
		node.Warning = node.Err != nil && isWarning(node.Err)

		return node
	}

	flat := make([]ErrorTree, 0, len(children))

	for _, child := range children {
		flat = appendTree(flat, child)
	}

	if node.Row.IsZero() && len(flat) == 1 {
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
// problem do not count.
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
// [limitTree] returns one. It holds the number of further problems that
// the walk can keep, and every node that the walk left out.
type treeCut struct {
	left   []ErrorTree
	budget int
}

// limitTree returns t with its first limit problems, along with the
// problems it left out. A heading stays when any problem below it stays.
func limitTree(t ErrorTree, limit int) (ErrorTree, []ErrorTree) {
	c := &treeCut{budget: limit}

	cut, _ := c.cut(t)
	if len(c.left) == 0 {
		return t, nil
	}

	return cut, c.left
}

// withOmitted returns t with a last root child that counts omitted
// problems, as "and 490 more", for a renderer to draw. It returns t as it
// is when omitted is 0. The children of the result do not share an array
// with those of t.
func withOmitted(t ErrorTree, omitted int) ErrorTree {
	if omitted <= 0 {
		return t
	}

	t.Children = append(
		slices.Clip(t.Children),
		ErrorTree{Row: ErrorRow{Text: "and " + strconv.Itoa(omitted) + " more"}},
	)

	return t
}

// skippedBindings returns the bindings of left, the problems that
// [limitTree] left out of cut. An excerpt skips those bindings, so it
// marks only the problems that the tree shows. The result leaves out each
// binding that a node of cut still reaches.
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
// t remains. A heading keeps its details, and it stays when any of its
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

// ErrorReport holds the parts that a renderer draws for an error. They are
// the tree of its messages, one excerpt per source that its bindings
// touch, and the bindings that get a reason in place of an excerpt.
// [FormatError] draws a report as plain text and
// [go.jacobcolvin.com/niceyaml/printer.Printer.PrintError] draws one with
// styles. A program can draw the same parts with a renderer of its own,
// such as one that writes HTML:
//
//	rep := niceyaml.NewErrorReport(err)
//
//	drawTree(rep.Tree)
//
//	if rep.Omitted > 0 {
//		drawMore(rep.Omitted)
//	}
//
//	for _, excerpt := range rep.Excerpts {
//		drawView(excerpt.View)
//	}
//
// A renderer draws [ErrorReport.Omitted] after the tree, because the tree
// does not have a row for the problems that [WithProblemLimit] leaves out.
//
// The report holds every text as the error or the source wrote it. The
// messages from the unmarshalers and validators of a program can hold
// control characters, and so can the names of sources. A renderer thus
// escapes each message and each name for its output.
// [go.jacobcolvin.com/niceyaml/escape.Message] draws a message as
// [FormatError] draws it, with its rows kept, and
// [go.jacobcolvin.com/niceyaml/escape.Line] draws a name as a
// [*SourceError] draws it.
//
// A report keeps each Source that the bindings of its error touch, with
// every line, token, and parse of it. The report keeps them alive until
// the program drops the report. The report of an error from [Detach]
// keeps the lines that Detach kept instead. A program that keeps reports
// past their sources thus builds them from detached errors.
//
// Create instances with [NewErrorReport].
type ErrorReport struct {
	// Excerpts holds one excerpt per source that the bindings of the
	// error touch, in the order that the bindings reach the sources. The
	// bindings reach the sources in the order that [Bindings] yields them,
	// and the bindings below each one come before the next. Excerpts
	// holds an excerpt of a source only when a location in it resolves and
	// its excerpts are on, as [WithExcerpts] sets them. With
	// [WithProblemLimit], that location must belong to a problem that Tree
	// keeps. A source whose problems all fall past the limit thus does not
	// get an excerpt.
	Excerpts []ErrorExcerpt
	// Unresolved holds the bindings that get a reason in place of an
	// excerpt, in the order that [Bindings] yields them. Each is a binding
	// that Bindings finds whose tree does not resolve a location and whose
	// own location did not resolve. Its reason is the one that
	// [SourceError.Unresolved] returns. A binding that the error reaches
	// twice comes twice, as Bindings yields it.
	//
	// Unresolved leaves out each binding that does not carry a location of
	// its own, because [SourceError.Unresolved] returns nil for it. It also
	// leaves out each binding whose reason wraps [paths.ErrNoDocument],
	// because a path resolves only in a document with content, and the
	// tree names the path already. With [WithProblemLimit], it leaves out
	// the bindings of the problems that Tree leaves out.
	Unresolved []*SourceError
	// Tree is the [ErrorTree] of the error. Under one heading, a warning
	// whose message and details match those of an earlier warning keeps
	// its row and loses its details. The message is that of its binding,
	// without the text that a wrapper above the binding added, such as
	// the name of a file. That text stays out when a source binds the
	// wrapper again too. Fifteen files with one deprecated version thus
	// print fifteen rows and one set of details, and so do fifteen
	// documents of one file.
	//
	// A repeat does not mark an excerpt that nothing else in the tree
	// marks, when an earlier copy marks an excerpt already. A source whose
	// every mark would be such a repeat thus does not get an excerpt. Every
	// other excerpt marks each problem of its source, up to the number that
	// [WithExcerptMarks] sets for one line. Each node that is not a repeat
	// is the node that [NewErrorTree] builds, which keeps every detail.
	//
	// The tree does not have a row for an error that holds only nil
	// errors, such as a bound join of typed-nil errors. When Excerpts and
	// Unresolved are then empty too, Tree is one node with the message of
	// the error as the Text of its Row and a nil Err. A renderer then draws that
	// message in place of an empty report.
	//
	// With [WithProblemLimit], Tree holds the first problems of the error
	// and the headings above them, and Omitted counts the rest. Tree
	// itself does not have a row for them, so each node that
	// [ErrorTree.Problems] yields holds a problem of the error. The
	// excerpts then mark only the problems that Tree keeps.
	Tree ErrorTree
	// Omitted is the number of problems that Tree leaves out, as
	// [WithProblemLimit] cuts them. It is 0 without that option. A
	// renderer draws the count after the tree, as [FormatError] draws
	// "and 1950 more" as the last row of the root.
	Omitted int
}

// ErrorExcerpt is the excerpt of one source in an [ErrorReport]. It keeps
// Source alive, with every line, token, and parse of it, until the
// program drops the excerpt. The excerpt of an error that [Detach]
// detached keeps the lines that Detach kept but not the Source.
type ErrorExcerpt struct {
	// Source is the source the excerpt shows, or nil for the excerpt of a
	// detached error.
	Source *Source
	// View holds the lines of the source around the locations the error
	// marks in it, cut and clipped as [SourceError.Excerpt] cuts a view.
	View *line.View
	// Name is the name of the source, as [Source.Name] returns it. The
	// excerpt of a detached error has the name its Source had.
	Name string
	// FilePath is the file path of the source, as [Source.FilePath]
	// returns it. The excerpt of a detached error has the file path its
	// Source had. A path in the file system of [NewSourceFromFS] names a
	// file in that file system, which the excerpt does not keep, as
	// [ErrorTree.FilePath] describes.
	FilePath string
	// Named reports whether Name leads the excerpt on a row of its own, so
	// a reader can tell the excerpts of several sources apart. Named is
	// true when the bindings of the error touch more than one source and
	// the source has a name. A source counts whether or not it has an
	// excerpt. So the one excerpt of a report has Named set when the error
	// also touches a source where it did not resolve any location.
	Named bool
}

// NewErrorReport creates a new [ErrorReport] from err. Each excerpt keeps
// the context lines, the width, and the marks on one line that the options
// set, as [ExcerptOption] describes. An excerpt of a detached binding keeps
// those of the options [Detach] received instead. [WithProblemLimit] cuts
// the tree to its first problems, and the report then ignores the bindings
// of the rest, so it shows what the tree shows. The limit holds for the
// detached bindings too. A nil err yields the zero ErrorReport.
func NewErrorReport(err error, opts ...ExcerptOption) ErrorReport {
	if err == nil {
		return ErrorReport{}
	}

	cfg := newExcerptConfig(opts)

	// A repeated warning still names its source, so the excerpts of a
	// report that reaches several sources keep their names.
	quiet := make(map[*SourceError]bool)
	full := NewErrorTree(err)
	tree := dropRepeats(full, quiet)

	var (
		skipped map[*SourceError]bool
		left    []ErrorTree
	)

	if cfg.problems > 0 {
		tree, left = limitTree(tree, cfg.problems)
		skipped = skippedBindings(tree, left)
	}

	bindings := slices.Collect(Bindings(err))

	// The own location of a binding carries its message only among
	// several bindings, where the message tells the carets of one excerpt
	// apart.
	sources, positions := treePositions(bindings, len(bindings) > 1, skipped, quiet, wrapperMarks(full))

	var rep ErrorReport

	for _, src := range sources {
		view, ok := src.excerpt(positions[src], cfg)
		if !ok {
			continue
		}

		rep.Excerpts = append(rep.Excerpts, ErrorExcerpt{
			Source:   src.live(),
			View:     view,
			Name:     src.Name(),
			FilePath: src.FilePath(),
			Named:    len(sources) > 1 && src.Name() != "",
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
	rep.Omitted = len(left)

	// A bound join whose branches all carry nothing has an empty tree and
	// nothing else to draw, so the message stands in for it.
	if len(rep.Excerpts) == 0 && len(rep.Unresolved) == 0 {
		rep.Tree = messageTree(tree, err)
	}

	return rep
}

// wrapperMark is what the node of a wrapper around a binding gives the
// mark of that binding: the message of the node with the text of the
// wrapper, and the severity of the node.
type wrapperMark struct {
	message string
	warning bool
}

// wrapperMarks returns the mark of each node of t that wraps its binding,
// keyed by that binding. The mark of such a binding then carries the
// message of its node, with the text that the wrapper added. It also
// carries the severity of the node, so the caret of a warning that
// [AsErrors] marked above its binding reads as an error, as the row does.
// A binding that the tree reaches several times takes the mark of the
// first node that holds it.
func wrapperMarks(t ErrorTree) map[*SourceError]wrapperMark {
	var marks map[*SourceError]wrapperMark

	seen := make(map[*SourceError]bool)

	for node := range t.nodes() {
		if node.Bound == nil || node.Row.IsZero() || seen[node.Bound] {
			continue
		}

		seen[node.Bound] = true

		x, self := node.Err.(*SourceError) //nolint:errorlint // The node itself, not a chain search.
		if self && x == node.Bound {
			continue
		}

		if marks == nil {
			marks = make(map[*SourceError]wrapperMark)
		}

		// A heading is not a warning, and neither is a binding that heads.
		marks[node.Bound] = wrapperMark{message: node.rowMessage(), warning: node.Warning && !node.heads()}
	}

	return marks
}

// dropRepeats returns t without the details of each repeated warning, and
// adds the bindings that the excerpts do not mark to quiet. A warning
// repeats when its message and its details match those of an earlier
// warning under the same heading, from any source. Its row stays, so each
// file with the warning still shows in the tree.
//
// The bindings of the dropped details go into quiet. The binding of a repeat
// goes there too when an earlier copy marks an excerpt and nothing else in
// the tree marks the source of the repeat. Such a source then gets no
// excerpt, and the excerpt of every other source marks its repeats too. A
// binding that a node other than a repeat holds stays out of quiet, so a
// binding that the error reaches twice keeps its marks.
func dropRepeats(t ErrorTree, quiet map[*SourceError]bool) ErrorTree {
	if !t.heads() {
		return t
	}

	var repeats, details []*SourceError

	t = markRepeats(t, &repeats, &details)

	// The tree still holds a repeat, so the count of each binding leaves the
	// repeats out.
	held := make(map[*SourceError]int)

	for node := range t.nodes() {
		if node.Bound != nil {
			held[node.Bound]++
		}
	}

	for _, bound := range repeats {
		held[bound]--
	}

	seen := make(map[*SourceError]bool)
	silence := func(bound *SourceError) {
		bound.all(seen, func(x *SourceError) bool {
			if held[x] == 0 {
				quiet[x] = true
			}

			return true
		})
	}

	for _, bound := range details {
		silence(bound)
	}

	// A source shows when a node that is not a repeat marks it, as a
	// problem, a heading with a line of its own, or a detail does.
	shown := make(map[excerptSource]bool)

	for bound, n := range held {
		if n > 0 && marksExcerpt(bound) && !quiet[bound] {
			shown[bound.origin()] = true
		}
	}

	for _, bound := range repeats {
		if !shown[bound.origin()] {
			silence(bound)
		}
	}

	return t
}

// markRepeats returns t without the details of each warning that repeats
// an earlier one under the same heading, as [dropRepeats] describes. It
// appends the binding of each dropped detail to details. It appends the
// binding of each repeat that follows a copy that marks an excerpt to
// repeats. A repeat that follows only copies without a mark, such as
// unbound ones, marks its excerpt in their place.
func markRepeats(t ErrorTree, repeats, details *[]*SourceError) ErrorTree {
	var (
		seen   = make(map[string]bool)
		marked = make(map[string]bool)
	)

	children := make([]ErrorTree, 0, len(t.Children))

	for _, child := range t.Children {
		switch {
		case child.Detail:

		case child.isProblem() && child.Warning:
			key := repeatKey(child)

			if seen[key] {
				for node := range child.nodes() {
					if node.Detail && node.Bound != nil {
						*details = append(*details, node.Bound)
					}
				}

				child.Children = nil

				if marked[key] {
					*repeats = append(*repeats, child.Bound)
				}
			}

			seen[key] = true
			marked[key] = marked[key] || marksExcerpt(child.Bound)

		case child.isProblem():

		default:
			child = markRepeats(child, repeats, details)
		}

		children = append(children, child)
	}

	t.Children = children

	return t
}

// marksExcerpt reports whether bound marks a line in an excerpt: whether
// its location resolved and it has a line of its own. A nil bound does
// not mark a line.
func marksExcerpt(bound *SourceError) bool {
	return bound != nil && bound.locErr == nil && !bound.headsAlone()
}

// repeatKey returns the text that two warnings share when one repeats the
// other: the message of the binding of t and the rows of its details. The
// text that a wrapper above the binding adds stays out of the key, and so
// does the text of a wrapper that a binding adopted, as
// [SourceError.innermost] leaves it out. A program that wraps the error of
// each file with the name of the file thus still gets the repeats of a
// warning that the files share, whether or not a source binds the wrapper.
func repeatKey(t ErrorTree) string {
	var sb strings.Builder

	msg := t.rowMessage()
	if t.Bound != nil {
		msg = t.Bound.innermost().Message()
	}

	sb.WriteString(msg)

	for node := range t.nodes() {
		if node.Detail {
			sb.WriteString("\x00")
			sb.WriteString(node.Label())
		}
	}

	return sb.String()
}

// messageTree returns t when t has a row to draw. Otherwise it returns a
// single node with the message of err, so a renderer still has a row to
// draw. For example, the tree of a join of typed-nil errors does not have
// a row to draw.
func messageTree(t ErrorTree, err error) ErrorTree {
	if !t.Row.IsZero() || len(t.Children) > 0 {
		return t
	}

	return ErrorTree{Row: ErrorRow{Text: err.Error()}}
}
