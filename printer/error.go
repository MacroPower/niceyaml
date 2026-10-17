package printer

import (
	"strings"

	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/escape"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

// errorConnectorWidth is the width of the connector in front of each
// error below another in the tree that [Printer.PrintError] draws. The
// connector takes the three cells of "├──" or "└──" and one cell of
// padding after them. The indent below a connector, "│  " and its
// padding, is as wide.
const errorConnectorWidth = 4

// PrintError renders err for a reader: its message as a tree, then one
// excerpt per source the bindings in err touch, as
// [niceyaml.ErrorReport.Excerpts] holds them. The tree puts a connector
// in front of each error below another. Each excerpt keeps the context
// lines that opts set on either side of each marked line, as
// [niceyaml.ExcerptOption] describes. It draws a caret run under each
// location.
//
// PrintError draws the tree and the excerpts with the styles of the
// printer and fits them to the width [WithWrapWidth] sets. A nil err
// prints as "". A program configures one printer and prints its errors
// through it:
//
//	p := printer.New(printer.WithWrapWidth(width))
//	lipgloss.Fprintln(os.Stderr, p.PrintError(err, niceyaml.WithContextLines(3)))
//
// # Tree
//
// PrintError draws the message as a tree with a connector in front of each
// error below another, in the color of the gutter's line numbers. A
// validator's report thus reads as its summary with one branch per
// violation, and each detail of an error sits on a branch below it. The
// root keeps the message as its wrappers wrote it, without the errors the
// branches show. Each branch carries the "line:col:" its location
// resolved to, without the name the root already gives:
//
//	cafe.yaml: 2 schema violations
//	├── 6:8: $.spec.sla: string does not match pattern
//	└── 22:11: $.spec.hours.days: expected "array", got "string"
//
// An error without errors below it prints as a tree of one node that holds
// its whole message. The context a wrapper added thus stays in front of
// the position.
//
// # Excerpts
//
// Each excerpt keeps the context lines [niceyaml.WithContextLines] sets
// on either side of each marked line. A line longer than the width
// [niceyaml.WithExcerptWidth] sets shows a window of that many columns
// around each location on it, with "..." in place of the rest. Each
// source gets one excerpt. An error joined from one bound error per
// document of a file thus prints that file once, with the errors of every
// document on it. A binding whose children point into another file, such
// as a detail that names where a value was first declared, prints an
// excerpt of that file too.
//
// # Carets
//
// Each location gets a row below its line with a caret run under its
// range, as [niceyaml.FormatError] draws one. The range thus shows its
// extent without color. A location that does not cover a column, such as
// a path to an empty value, gets a single caret at its column instead. A
// range that runs onto later lines gets a caret run below each of them
// too.
//
// Among several bindings, the message of each sits after the carets of
// its own range. Locations that share a range share its row, with their
// messages joined by "; ". The rows of errors come before those of
// warnings.
//
// A location without a message beside it in the excerpt, such as the root
// of a lone bound error, gets its carets alone.
// [niceyaml.SourceError.Excerpt] marks such a line with an annotation
// without content, and [DefaultAnnotation] draws the carets for it.
//
// When the bindings touch more than one source, the name of its source
// leads each excerpt on a row of its own. Blank lines separate the parts.
//
// # Missing Excerpts
//
// A line starting "no excerpt:" follows the excerpts for each SourceError
// that [niceyaml.Bindings] finds whose tree does not resolve any location.
// The line gives the reason the location of the SourceError itself did
// not resolve. A SourceError without a location of its own does not get
// such a line. A SourceError that carries a path into a document without
// content, such as an empty file, does not get one either. A path cannot
// resolve in such a document, and the tree already names the path.
//
// A SourceError whose own location does not resolve but whose children do
// gets their excerpts without a reason. Its message stays in the tree
// without a position.
//
// A source with excerpts off, as [niceyaml.WithExcerpts] sets it for a
// text that holds secrets, does not print an excerpt or a line in its
// place. The tree names the position, the path, and the message of each
// error bound in that source.
//
// # Control Characters
//
// In every message of the tree, the root's included, and in each
// "no excerpt:" line, PrintError draws control characters as their
// pictures. An excerpt draws them the same way. A line feed in a message
// of the tree starts a new row instead. A tab becomes four spaces in the
// tree, in a "no excerpt:" line, and in the message an excerpt carries
// beside its carets.
//
// # Wrapping
//
// PrintError wraps each message to the width [WithWrapWidth] sets less
// the connectors in front of it. Each excerpt fits that width with the
// frame of its container, as [Printer.Print] fits a view.
//
// # Unbound Errors
//
// An error whose tree does not hold a SourceError prints as its tree
// alone. For an error without errors below it, the tree is its message.
// An error also prints as its tree alone when its SourceErrors do not
// yield an excerpt and do not carry a location of their own that failed
// to resolve. A nil err prints as "". When neither the tree nor the
// excerpts of an error render any text, as for a bound join of typed-nil
// errors, PrintError prints the message of the error in their place. It
// escapes and wraps that message like any other.
//
// # Other Renderers
//
// PrintError draws the [niceyaml.ErrorReport] that
// [niceyaml.NewErrorReport] builds for err with opts, as
// [Printer.PrintReport] draws one, and [niceyaml.FormatError] draws the
// same report as plain text.
func (p *Printer) PrintError(err error, opts ...niceyaml.ExcerptOption) string {
	return p.PrintReport(niceyaml.NewErrorReport(err, opts...))
}

// PrintReport renders rep as [Printer.PrintError] renders the report it
// builds for an error. The tree comes first, then each excerpt below the
// name of its source when the report names it, then each "no excerpt:"
// line. PrintReport draws them with the styles and the width of the
// printer and puts a blank line between the parts. A program that builds
// a report once with [niceyaml.NewErrorReport], such as one that reads
// its parts or also renders it as plain text, prints that same report
// with PrintReport. The zero ErrorReport prints as "".
func (p *Printer) PrintReport(rep niceyaml.ErrorReport) string {
	parts := make([]string, 0, 1+len(rep.Excerpts)+len(rep.Unresolved))

	if msg := p.renderErrorTree(rep.Tree); msg != "" {
		parts = append(parts, msg)
	}

	for _, excerpt := range rep.Excerpts {
		part := p.Print(excerpt.View)

		// The name is the caller's text, so its control characters render
		// as pictures like those of the tree. It sits outside the
		// container, so it wraps to the whole width.
		if excerpt.Named {
			name := p.wrapTo(escape.Line(excerpt.Name), p.textWidth(0))
			part = strings.Join(name, "\n") + "\n" + part
		}

		parts = append(parts, part)
	}

	for _, bound := range rep.Unresolved {
		// The reason names the path that did not resolve, which a key of
		// the document spells, so its control characters render as
		// pictures like those of the tree. A tab in the key becomes four
		// spaces, as it does in the tree.
		text := escape.Line("no excerpt: " + bound.Unresolved().Error())

		parts = append(parts, strings.Join(p.wrapTo(text, p.textWidth(0)), "\n"))
	}

	return strings.Join(parts, "\n\n")
}

// renderErrorTree draws t with a connector in front of each child. The
// connectors render in the foreground of [kind.UILineNumber], so they
// take the color of the gutter's line numbers. They leave out the
// background of the gutter, because the message text beside them does
// not have it. Each message takes the rows errorText returns for it.
func (p *Printer) renderErrorTree(t niceyaml.ErrorTree) string {
	branch := lipgloss.NewStyle().
		Foreground(p.styles.Style(kind.UILineNumber).GetForeground()).
		PaddingRight(1)

	var rows []string

	// A root with nothing to show, such as a join, takes no row, so its
	// children start the tree.
	if root := p.errorRows(t, 0); strings.Join(root, "\n") != "" {
		rows = root
	}

	rows = p.appendErrorBranches(rows, t.Children, "", 1, &branch)

	// The padding after a connector is a space outside its color, so a row
	// with no text after its connector would end in that space, and a
	// message can end a row with spaces of its own.
	for i, row := range rows {
		rows[i] = strings.TrimRight(row, " ")
	}

	return strings.Join(rows, "\n")
}

// appendErrorBranches appends the rows of children to rows. The children
// sit depth connectors from the left edge. Each row of a child starts
// with indent, the connectors that the ancestors of the child draw. After
// indent, the first row of a child takes "├──", or "└──" for the last
// child. The child's later rows and its own children sit under "│  ", or
// under blank cells for the last child. A line thus runs down from each
// connector to its next sibling. Each connector renders with branch.
func (p *Printer) appendErrorBranches(
	rows []string,
	children []niceyaml.ErrorTree,
	indent string,
	depth int,
	branch *lipgloss.Style,
) []string {
	for i, child := range children {
		connector, below := "├──", "│  "
		if i == len(children)-1 {
			connector, below = "└──", "   "
		}

		first, rest := indent+branch.Render(connector), indent+branch.Render(below)

		for j, row := range p.errorRows(child, depth) {
			if j == 0 {
				rows = append(rows, first+row)
			} else {
				rows = append(rows, rest+row)
			}
		}

		rows = p.appendErrorBranches(rows, child.Children, rest, depth+1, branch)
	}

	return rows
}

// errorRows returns the rows of the message of t at depth connectors from
// the left edge, as errorText lays them out. The "warning:" label of a
// warning renders in [kind.TextWarn].
func (p *Printer) errorRows(t niceyaml.ErrorTree, depth int) []string {
	rows := p.errorText(t.Label, depth)

	if t.Warning && !t.Detail && len(rows) > 0 {
		label := p.styles.Style(kind.TextWarn).Render(warningLabel)
		rows[0] = strings.Replace(rows[0], warningLabel, label, 1)
	}

	return rows
}

// warningLabel is the label that the row of a warning carries after its
// position.
const warningLabel = "warning:"

// errorText returns the rows of one message of the tree at depth
// connectors from the left edge, wrapped to the width that the
// connectors leave. [escape.Message] lays the text out first. It replaces
// each tab with four spaces, such as the tab in front of each suggestion
// that Cobra lists. It draws the other control characters as their
// pictures. A line break in the message stays a line break, because a
// wrapper around a joined error keeps the breaks between its branches in
// its own text.
func (p *Printer) errorText(text string, depth int) []string {
	return p.wrapTo(escape.Message(text), p.textWidth(depth*errorConnectorWidth))
}
