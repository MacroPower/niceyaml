package niceyaml

import (
	"errors"
	"slices"

	"go.jacobcolvin.com/niceyaml/line"
	"go.jacobcolvin.com/niceyaml/paths"
)

// ErrorReport holds the parts a renderer draws for an error: the tree of
// its messages, one excerpt per source its bindings touch, and the
// bindings that get a reason in place of an excerpt. [FormatError] draws
// a report as plain text and
// [go.jacobcolvin.com/niceyaml/printer.Printer.PrintError] draws one with
// styles. A renderer of its own, such as one that writes HTML, draws the
// same parts, so it shows the excerpts, the names, and the reasons they
// show:
//
//	rep := niceyaml.NewErrorReport(err, 2)
//
//	drawTree(rep.Tree)
//
//	for _, excerpt := range rep.Excerpts {
//		if excerpt.Named {
//			drawName(excerpt.Source.Name())
//		}
//
//		drawView(excerpt.View)
//	}
//
//	for _, bound := range rep.Unresolved {
//		drawReason(bound.Unresolved())
//	}
//
// The report holds every text as the error or the source wrote it. A
// message, the name of a source, and a reason can each hold control
// characters, such as an escape sequence in a key of the document, so a
// renderer escapes them for its output. FormatError draws them as their
// pictures.
//
// A report a program reads, such as JSON lines or editor diagnostics,
// lists the problems [ErrorTree.Problems] yields for [NewErrorTree]
// instead.
//
// Create instances with [NewErrorReport].
type ErrorReport struct {
	// Excerpts holds one excerpt per source the bindings of the error
	// touch, in the order [Excerpts] yields them. A source none of whose
	// locations resolve has none, and neither has a source with excerpts
	// off, as [WithExcerpts] sets it.
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
	// Tree is the [ErrorTree] of the error, as [NewErrorTree] builds it.
	// That tree has no row for an error that holds nothing, such as a
	// bound join of typed-nil errors. When the report then holds no
	// excerpt and no unresolved binding either, Tree is one node with the
	// message of the error as its Text and no Err, so a renderer draws
	// that message in place of nothing.
	Tree ErrorTree
}

// ErrorExcerpt is the excerpt of one source in an [ErrorReport].
type ErrorExcerpt struct {
	// Source is the source the excerpt shows.
	Source *Source
	// View holds the lines of Source around the locations the error marks
	// in it, as [Excerpts] yields the view.
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
// context lines of unchanged content on either side of each marked line,
// and a negative context keeps the marked lines alone, as 0 does. A nil
// err yields the zero ErrorReport.
func NewErrorReport(err error, context int) ErrorReport {
	return newErrorReport(err, context, 0)
}

// newErrorReport builds the report [NewErrorReport] documents. With a
// limit above zero, the tree shows that many problems at most and counts
// the rest, as [limitTree] cuts it. The bindings of the problems it
// leaves out, as [skippedBindings] finds them, then mark no excerpt,
// count toward no source, and get no reason, so the report shows the
// problems its tree shows.
func newErrorReport(err error, context, limit int) ErrorReport {
	if err == nil {
		return ErrorReport{}
	}

	tree := NewErrorTree(err)

	var skipped map[*SourceError]bool

	if limit > 0 {
		var left []ErrorTree

		tree, left = limitTree(tree, limit)
		skipped = skippedBindings(tree, left)
	}

	bindings := slices.Collect(Bindings(err))
	sources, positions := treePositions(bindings, len(bindings) > 1, skipped)

	var rep ErrorReport

	yieldExcerpts(sources, positions, context, func(src *Source, view *line.View) bool {
		rep.Excerpts = append(rep.Excerpts, ErrorExcerpt{
			Source: src,
			View:   view,
			Named:  len(sources) > 1 && src.Name() != "",
		})

		return true
	})

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

// messageTree returns t, or the tree of one node that holds the message
// of err when t has no row to draw, which is how a plain error with that
// message lays out. An error that holds nothing, such as a join of
// typed-nil errors, has such a tree, and a renderer draws its message
// rather than nothing.
func messageTree(t ErrorTree, err error) ErrorTree {
	if t.Text != "" || len(t.Children) > 0 {
		return t
	}

	return ErrorTree{Text: err.Error()}
}
