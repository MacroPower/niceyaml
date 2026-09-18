package printer

import (
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/tree"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/errortree"
	"go.jacobcolvin.com/niceyaml/style"
)

// PrintError renders err for a reader: its message as a tree, then the
// [niceyaml.SourceError.Detail] of every [*niceyaml.SourceError] in its
// tree, as [niceyaml.SourceErrors] finds them, each rendered by p with the
// context lines [WithContextLines] sets on either side of each marked
// line. An error joined from one bound error per document therefore prints
// an excerpt for each document. Blank lines separate the parts.
//
// The message is drawn as a tree with a connector in front of each nested
// error, in the color of the gutter's line numbers, so a validator's
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
//	p := printer.New(printer.WithWidth(width), printer.WithContextLines(3))
//	fmt.Println(p.PrintError(err))
//
// An error whose tree holds no SourceError, or whose Details are empty,
// prints as its message alone, and a nil err prints as "". The %+v verb
// prints the message as plain lines and the same Details as plain text
// with [DefaultContextLines] lines of context.
func (p *Printer) PrintError(err error) string {
	if err == nil {
		return ""
	}

	parts := make([]string, 0, 2)

	if msg := p.renderErrorTree(errortree.New(err)); msg != "" {
		parts = append(parts, msg)
	}

	for _, bound := range niceyaml.SourceErrors(err) {
		if detail := bound.Detail(p, p.contextLines); detail != "" {
			parts = append(parts, detail)
		}
	}

	return strings.Join(parts, "\n\n")
}

// renderErrorTree draws t with a connector in front of each child, styled
// as the gutter's line numbers are.
func (p *Printer) renderErrorTree(t errortree.Tree) string {
	branch := lipgloss.NewStyle().
		Foreground(p.styles.Style(style.Comment).GetForeground()).
		PaddingRight(1)

	return errorTreeNode(t, &branch).String()
}

// errorTreeNode builds the [*tree.Tree] of t, with branch styling the
// connector and indent of every child. A child without children is a leaf.
func errorTreeNode(t errortree.Tree, branch *lipgloss.Style) *tree.Tree {
	node := tree.Root(t.Text).
		EnumeratorStyle(*branch).
		IndenterStyle(*branch)

	for _, child := range t.Children {
		if len(child.Children) == 0 {
			node.Child(child.Text)
		} else {
			node.Child(errorTreeNode(child, branch))
		}
	}

	return node
}
