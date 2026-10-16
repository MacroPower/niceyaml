package printer

import (
	"strings"

	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/escape"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

// errorConnectorWidth is the width of the connector in front of each
// error below another in the tree [Printer.PrintError] draws: the three
// cells of "├──" or "└──", and the one cell of padding after them. The
// indent below a connector, "│  " and its padding, is as wide.
const errorConnectorWidth = 4

// PrintError renders err for a reader: its message as a tree, then one
// excerpt per source the bindings in err touch, as
// [niceyaml.ErrorReport.Excerpts] holds them. The tree puts a connector
// in front of each error below another. Each excerpt keeps the context
// lines opts set on either side of each marked line, as
// [niceyaml.ExcerptOption] describes, and draws a caret run under each
// location. PrintError draws both with the styles of the printer and
// fits them to the width [WithWrapWidth] sets. A nil err prints as "". A
// program configures one printer and prints its errors through it:
//
//	p := printer.New(printer.WithWrapWidth(width))
//	lipgloss.Fprintln(os.Stderr, p.PrintError(err, niceyaml.WithContextLines(3)))
//
// # Tree
//
// PrintError draws the message as a tree with a connector in front of each
// error below another, in the color of the gutter's line numbers. A
// validator's report thus reads as its summary with one branch per
// violation, and the details of an error branch off below it. The root
// keeps the message as its wrappers wrote it, without the errors the
// branches show, and each branch carries the "line:col:" its location
// resolved to, without the name the root already gives:
//
//	cafe.yaml: 2 schema violations
//	├── 6:8: $.spec.sla: string does not match pattern
//	└── 22:11: $.spec.hours.days: expected "array", got "string"
//
// An error with nothing below it prints as a tree of one node that holds
// its whole message, so the context a wrapper added stays in front of the
// position.
//
// # Excerpts
//
// Each excerpt keeps the context lines [niceyaml.WithContextLines] sets
// on either side of each marked line. A line longer than the width
// [niceyaml.WithExcerptWidth] sets shows a window of that many columns
// around each location on it, with "..." in place of the rest. An error joined
// from one bound error per document of a file therefore prints that file
// once, with the errors of every document on it. A binding whose
// children point into another file, such as a detail that names where a
// value was first declared, prints an excerpt of that file too.
//
// # Carets
//
// Each location gets a row below its line with a caret run under its
// range, as [niceyaml.FormatError] draws one, so the range shows its
// extent without color. A location that covers no column, such as a path
// to an empty value, gets a single caret at its column instead. A range
// that runs onto later lines gets a caret run below each of them too.
// Among several bindings, the message of each sits after the carets of
// its own range. Locations that share a range share its row, with their
// messages joined by "; ", and the rows of errors come before those of
// warnings.
//
// A location with no message beside it in the excerpt, such as the root
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
// [niceyaml.Bindings] finds whose tree resolves no location, with the
// reason the location of the SourceError itself did not resolve. A
// SourceError that carries no location of its own gets no such line.
// Neither does a SourceError that carries a path into a document with no
// content, such as an empty file, since no path resolves there and the
// tree names the path already. A SourceError whose own location does not
// resolve but whose children do gets their excerpts and no reason, and
// its message stays in the tree without a position.
//
// A source with excerpts off, as [niceyaml.WithExcerpts] sets it for a
// text that holds secrets, prints no excerpt and no line in its place.
// The tree names the position, the path, and the message of each error
// bound in that source.
//
// # Control Characters
//
// PrintError draws every message of the tree, the root's included, and
// each "no excerpt:" line with control characters as their pictures, as
// an excerpt draws them. A line feed in a message of the tree starts a
// new row instead. A tab becomes four spaces wherever it falls, in the
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
// An error whose tree holds no SourceError prints as its tree alone,
// which for an error with nothing below it is its message. So does an
// error whose SourceErrors yield no excerpt and carry no location of
// their own that failed to resolve. A nil err prints as "". An error
// whose tree and excerpts both render nothing, such as a bound join of
// typed-nil errors, prints its message in their place, escaped and
// wrapped like any other.
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
// its parts or renders it as plain text too, prints that same report
// here. The zero ErrorReport prints as "".
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

// renderErrorTree draws t with a connector in front of each child, in the
// foreground of [kind.UILineNumber], so the connectors take the color of
// the gutter's line numbers without the background of the gutter, which
// the message text beside them does not have. Each message lays out as
// errorText returns it.
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

// appendErrorBranches appends the rows of children, which sit depth
// connectors from the left edge, to rows, each behind indent, the
// connectors its ancestors draw. The first row of a child starts with
// "├──", or "└──" for the last child. The child's later rows and its own
// children sit under "│  ", or blank cells for the last child, so a line
// runs down from each connector to its next sibling. Each connector
// renders with branch.
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
// connectors from the left edge, wrapped to the width left of the
// connectors. [escape.Message] lays the text out first. It replaces each
// tab with four spaces, such as the tab in front of each suggestion Cobra
// lists, and draws the other control characters as their pictures. A line
// break in the message stays a line break, since a wrapper around a
// joined error keeps the breaks between its branches in its own text.
func (p *Printer) errorText(text string, depth int) []string {
	return p.wrapTo(escape.Message(text), p.textWidth(depth*errorConnectorWidth))
}
