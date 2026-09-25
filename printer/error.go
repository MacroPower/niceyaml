package printer

import (
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/tree"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/escape"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

// errorConnectorWidth is the width of the connector in front of each
// nested error in the tree [Printer.PrintError] draws: the three cells of
// the enumerator, "├──" or "└──", and the one cell of padding after it.
// The indent below a connector, "│  " and its padding, is as wide.
const errorConnectorWidth = 4

// PrintError renders err for a reader: its message as a tree, then the
// [niceyaml.SourceError.Excerpt] of every [*niceyaml.SourceError] in its
// tree, as [niceyaml.SourceErrors] finds them, each with the context lines
// [WithContextLines] sets on either side of each marked line. An error
// joined from one bound error per document therefore prints an excerpt for
// each document. A location with no message beside it in the excerpt,
// such as the root of a bound error, gets a caret run under its range on
// the row below, as [niceyaml.FormatError] draws one, since
// [niceyaml.SourceError.Annotate] marks such a line with an annotation
// without content and [DefaultAnnotation] draws that as the caret run, so
// the range shows its extent without color. Blank lines separate the
// parts. A SourceError
// whose location does not resolve prints a line starting "no excerpt:"
// that names the reason in place of its excerpt, unless it carries no
// location at all.
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
// An error with no nested errors prints as [error.Error] as it is, so the
// context a wrapper added stays in front of the position:
//
//	p := printer.New(printer.WithWrap(width), printer.WithContextLines(3))
//	fmt.Println(p.PrintError(err))
//
// An error whose tree holds no SourceError, or whose excerpts are empty,
// prints as its message alone, and a nil err prints as "".
// [niceyaml.FormatError] prints the same tree and excerpts as plain
// text.
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
	// tree, so the message stands in for it rather than nothing.
	if len(parts) == 0 {
		return err.Error()
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
	var parts []string

	for _, excerpt := range bound.Excerpts(p.contextLines) {
		parts = append(parts, p.Print(excerpt))
	}

	if len(parts) > 0 {
		return parts
	}

	reason := bound.Unresolved()
	if reason != nil {
		return []string{"no excerpt: " + reason.Error()}
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

	rows := strings.Split(p.errorTreeNode(t, &branch, 0).String(), "\n")

	// The tree pads every row of a message to the widest one, which a
	// message that wraps would otherwise carry to the end of each row.
	for i, row := range rows {
		rows[i] = strings.TrimRight(row, " ")
	}

	return strings.Join(rows, "\n")
}

// errorTreeNode builds the [*tree.Tree] of t at depth connectors from the
// left edge, with branch styling the connector and indent of every child.
// A child without children is a leaf.
func (p *Printer) errorTreeNode(t niceyaml.ErrorTree, branch *lipgloss.Style, depth int) *tree.Tree {
	node := tree.Root(p.errorText(t.Text, depth)).
		EnumeratorStyle(*branch).
		IndenterStyle(*branch)

	for _, child := range t.Children {
		if len(child.Children) == 0 {
			node.Child(p.errorText(child.Text, depth+1))
		} else {
			node.Child(p.errorTreeNode(child, branch, depth+1))
		}
	}

	return node
}

// errorText renders one message of the tree at depth connectors from the
// left edge: control characters as their pictures, wrapped to the width
// left of the connectors. A line break in the message stays a line break,
// since a wrapper around a joined error keeps the breaks between its
// branches in its own text. The tree indents every row of a message after
// the first under the connector.
func (p *Printer) errorText(text string, depth int) string {
	lines := strings.Split(text, "\n")
	for i, ln := range lines {
		lines[i] = escape.Control(ln)
	}

	rows := p.wrapContent(strings.Join(lines, "\n"), depth*errorConnectorWidth)

	return strings.Join(rows, "\n")
}
