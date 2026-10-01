package printer

import (
	"strings"

	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/escape"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

// errorConnectorWidth is the width of the connector in front of each
// nested error in the tree [Printer.PrintError] draws: the three cells of
// "├──" or "└──", and the one cell of padding after them. The indent
// below a connector, "│  " and its padding, is as wide.
const errorConnectorWidth = 4

// PrintError renders err for a reader: its message as a tree, then the
// excerpts of each [*niceyaml.SourceError] that [niceyaml.Bindings] finds
// in its tree, one per source the tree of the binding touches, as
// [niceyaml.SourceError.Excerpts] yields them. Each excerpt keeps the
// context lines [WithContextLines] sets on either side of each marked
// line. An error joined from one bound error per document therefore
// prints an excerpt for each document, and a binding whose nested errors
// point into another file prints an excerpt of that file too. A location
// with no message beside it in the excerpt, such as the root of a bound
// error, gets a caret run under its range on the row below, as
// [niceyaml.FormatError] draws one.
// [niceyaml.SourceError.Excerpt] marks such a line with an annotation
// without content, and [DefaultAnnotation] draws that as the caret run, so
// the range shows its extent without color. A location that covers no
// column, such as a path to an empty value, gets a single caret at its
// column. Blank lines separate the parts. When no location in the tree
// of a SourceError resolves, PrintError prints a line starting
// "no excerpt:" in place of the excerpts, with the reason the location of
// the SourceError itself did not resolve. A SourceError that carries no
// location of its own gets no such line. A SourceError whose own
// location does not resolve but whose nested errors do gets their
// excerpts and no reason, and its message stays in the tree without a
// position.
//
// PrintError draws the message as a tree with a connector in front of each
// nested error, in the color of the gutter's line numbers, so a validator's
// report reads as its summary with one branch per violation. The root
// keeps the message as its wrappers wrote it, and each branch carries the
// "line:col:" its location resolved to, without the name the root already
// gives:
//
//	cafe.yaml: 2 schema violations
//	├── 6:8: $.spec.sla: string does not match pattern
//	└── 22:11: $.spec.hours.days: expected "array", got "string"
//
// An error with no nested errors prints as a tree of one node that holds
// its whole message, so the context a wrapper added stays in front of the
// position. PrintError draws every message of the tree, the root's
// included, with control characters as their pictures, as an excerpt
// draws them, and wraps each message to the width [WithWrap] sets less the
// connectors in front of it. Each excerpt wraps to that width less the
// horizontal frame of the container style, so an excerpt and its frame
// fit the width together.
//
// A program configures one printer and prints its errors through it:
//
//	p := printer.New(printer.WithWrap(width), printer.WithContextLines(3))
//	fmt.Println(p.PrintError(err))
//
// An error whose tree holds no SourceError prints as its tree alone,
// which for an error with no nested errors is its message. So does an
// error whose SourceErrors yield no excerpt and carry no location of
// their own that failed to resolve. A nil err prints as "". An error
// whose tree and excerpts both render nothing, such as a bound join of
// typed-nil errors, prints its message in their place, escaped and
// wrapped like any other. [niceyaml.FormatError] prints the same tree
// and excerpts as plain text.
func (p *Printer) PrintError(err error) string {
	if err == nil {
		return ""
	}

	parts := make([]string, 0, 2)

	if msg := p.renderErrorTree(niceyaml.NewErrorTree(err)); msg != "" {
		parts = append(parts, msg)
	}

	for bound := range niceyaml.Bindings(err) {
		parts = append(parts, p.details(bound)...)
	}

	// A bound join whose branches all carry nothing renders as an empty
	// tree, so the message stands in for it rather than nothing, drawn as
	// the tree of one node that a plain error with that message draws.
	if len(parts) == 0 {
		return p.renderErrorTree(niceyaml.ErrorTree{Text: err.Error()})
	}

	return strings.Join(parts, "\n\n")
}

// details renders each excerpt of bound from
// [niceyaml.SourceError.Excerpts] with the printer's context lines, one
// per source the tree of bound touches, or names the reason there is
// none: a line starting "no excerpt:" with the reason
// [niceyaml.SourceError.Unresolved] returns, and an error that carries no
// location has nothing to explain. Returns nothing when there is nothing
// to show, as [niceyaml.FormatError] does.
func (p *Printer) details(bound *niceyaml.SourceError) []string {
	// Print wraps the gutter and content to the printer's width and draws
	// the container's frame outside it, so the excerpts wrap to the width
	// less the frame.
	ex := p
	if p.wrap > 0 {
		ex = p.With(WithWrap(max(1, p.wrap-p.style.GetHorizontalFrameSize())))
	}

	var parts []string

	for _, excerpt := range bound.Excerpts(p.contextLines) {
		parts = append(parts, ex.Print(excerpt))
	}

	if len(parts) > 0 {
		return parts
	}

	// The reason names the path that did not resolve, which a key of the
	// document spells, so it gets the same treatment as a message of the
	// tree.
	reason := bound.Unresolved()
	if reason != nil {
		return []string{strings.Join(p.wrapContent(escape.Control("no excerpt: "+reason.Error()), 0), "\n")}
	}

	return nil
}

// renderErrorTree draws t with a connector in front of each child, in the
// foreground of [kind.UILineNumber], so the connectors take the color of
// the gutter's line numbers without the background of the gutter, which
// the message text beside them does not have. Control characters in a
// message render as their pictures, as they do in an excerpt, and each
// message wraps to the printer's width less the connectors in front of it.
func (p *Printer) renderErrorTree(t niceyaml.ErrorTree) string {
	branch := lipgloss.NewStyle().
		Foreground(p.styles.Style(kind.UILineNumber).GetForeground()).
		PaddingRight(1)

	var rows []string

	// A root with nothing to show, such as a join, takes no row, so its
	// children start the tree.
	if root := p.errorText(t.Text, 0); strings.Join(root, "\n") != "" {
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

		for j, row := range p.errorText(child.Text, depth) {
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

// errorText returns the rows of one message of the tree at depth
// connectors from the left edge: control characters as their pictures,
// wrapped to the width left of the connectors. A line break in the
// message stays a line break, since a wrapper around a joined error keeps
// the breaks between its branches in its own text.
func (p *Printer) errorText(text string, depth int) []string {
	return p.wrapContent(escape.Rows(text), depth*errorConnectorWidth)
}
