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

// PrintError renders err for a reader: its message as a tree, then one
// excerpt per source the bindings in err touch, as [niceyaml.Excerpts]
// yields them. Each excerpt keeps the context lines [WithContextLines]
// sets on either side of each marked line. An error joined from one
// bound error per document of a file therefore prints that file once,
// with the errors of every document on it. A binding whose nested
// errors point into another file prints an excerpt of that file too.
// Among several bindings, the message of each sits beside its caret, and
// when they touch more than one source, the name of its source leads
// each excerpt on a row of its own. A location with no message beside it
// in the excerpt, such as the root of a lone bound error, gets a caret
// run under its range on the row below, as [niceyaml.FormatError] draws
// one.
// [niceyaml.SourceError.Excerpt] marks such a line with an annotation
// without content, and [DefaultAnnotation] draws that as the caret run, so
// the range shows its extent without color. A location that covers no
// column, such as a path to an empty value, gets a single caret at its
// column. Blank lines separate the parts. A line starting "no excerpt:"
// follows the excerpts for each SourceError [niceyaml.Bindings] finds
// whose tree resolves no location, with the reason the location of the
// SourceError itself did not resolve. A SourceError that carries no
// location of its own gets no such line. A SourceError whose own
// location does not resolve but whose nested errors do gets their
// excerpts and no reason, and its message stays in the tree without a
// position.
//
// PrintError draws the message as a tree with a connector in front of each
// nested error, in the color of the gutter's line numbers, so a validator's
// report reads as its summary with one branch per violation. The root
// keeps the message as its wrappers wrote it, without the errors the
// branches show, and each branch carries the "line:col:" its location
// resolved to, without the name the root already gives:
//
//	cafe.yaml: 2 schema violations
//	├── 6:8: $.spec.sla: string does not match pattern
//	└── 22:11: $.spec.hours.days: expected "array", got "string"
//
// An error with no nested errors prints as a tree of one node that holds
// its whole message, so the context a wrapper added stays in front of the
// position. PrintError draws every message of the tree, the root's
// included, and each "no excerpt:" line with control characters as their
// pictures, as an excerpt draws them. A line feed in a message of the
// tree starts a new row instead. A tab becomes four spaces wherever it
// falls, in the tree, in a "no excerpt:" line, and in the message an
// excerpt carries beside a caret. PrintError wraps each message to the
// width [WithWrap] sets less the connectors in front of it. Each excerpt
// wraps to that width less the horizontal frame of the container style,
// so an excerpt and its frame fit the width together.
//
// A program configures one printer and prints its errors through it:
//
//	p := printer.New(printer.WithWrap(width), printer.WithContextLines(3))
//	lipgloss.Fprintln(os.Stderr, p.PrintError(err))
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

	parts = append(parts, p.details(err)...)

	// A bound join whose branches all carry nothing renders as an empty
	// tree, so the message stands in for it rather than nothing, drawn as
	// the tree of one node that a plain error with that message draws.
	if len(parts) == 0 {
		return p.renderErrorTree(niceyaml.ErrorTree{Text: err.Error()})
	}

	return strings.Join(parts, "\n\n")
}

// details renders what the tree of err leaves out, as
// [niceyaml.FormatError] lays it out. Each excerpt [niceyaml.Excerpts]
// yields comes first, with the printer's context lines, one per source
// the bindings of err touch. When those bindings touch more than one
// source, the name of its source leads each excerpt on a row of its own.
// A line starting "no excerpt:" follows for each binding
// [niceyaml.Bindings] finds whose tree marks nothing, with the reason
// [niceyaml.SourceError.Unresolved] returns, and a binding that carries
// no location has nothing to explain. Returns nothing when there is
// nothing to show.
func (p *Printer) details(err error) []string {
	// Print wraps the gutter and content to the printer's width and draws
	// the container's frame outside it, so the excerpts wrap to the width
	// less the frame.
	ex := p
	if p.wrap > 0 {
		ex = p.With(WithWrap(max(1, p.wrap-p.style.GetHorizontalFrameSize())))
	}

	named := severalSources(err)

	var parts []string

	for src, excerpt := range niceyaml.Excerpts(err, p.contextLines) {
		part := ex.Print(excerpt)

		// The name is the caller's text, so its control characters render
		// as pictures like those of the tree.
		if named && src.Name() != "" {
			name := p.wrapContent(escape.Control(escape.Tabs(src.Name())), 0)
			part = strings.Join(name, "\n") + "\n" + part
		}

		parts = append(parts, part)
	}

	for bound := range niceyaml.Bindings(err) {
		if marks(bound) {
			continue
		}

		// The reason names the path that did not resolve, which a key of
		// the document spells, so its control characters render as
		// pictures like those of the tree. A tab in the key becomes four
		// spaces, as it does in the tree.
		reason := bound.Unresolved()
		if reason != nil {
			text := escape.Control(escape.Tabs("no excerpt: " + reason.Error()))

			parts = append(parts, strings.Join(p.wrapContent(text, 0), "\n"))
		}
	}

	return parts
}

// severalSources reports whether the bindings in err, the ones
// [niceyaml.AllBindings] yields, are bound to more than one source.
func severalSources(err error) bool {
	var first *niceyaml.Source

	for bound := range niceyaml.AllBindings(err) {
		switch {
		case first == nil:
			first = bound.Source()
		case bound.Source() != first:
			return true
		}
	}

	return false
}

// marks reports whether the location of bound, or of a binding below it,
// resolved, so an excerpt marks a line for the tree of bound.
func marks(bound *niceyaml.SourceError) bool {
	for b := range niceyaml.AllBindings(bound) {
		if _, ok := b.Range(); ok {
			return true
		}
	}

	return false
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
// connectors from the left edge, wrapped to the width left of the
// connectors. [escape.Message] lays the text out first. It replaces each
// tab with four spaces, such as the tab in front of each suggestion Cobra
// lists, and draws the other control characters as their pictures. A line
// break in the message stays a line break, since a wrapper around a
// joined error keeps the breaks between its branches in its own text.
func (p *Printer) errorText(text string, depth int) []string {
	return p.wrapContent(escape.Message(text), depth*errorConnectorWidth)
}
