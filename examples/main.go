// Package main is just an example.
package main

import (
	"fmt"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/charmtone"

	_ "embed"

	"github.com/macropower/niceyaml"
)

var (
	//go:embed sample.yaml
	source string

	//go:embed find.yaml
	find string

	//go:embed original.yaml
	original string

	//go:embed modified.yaml
	modified string

	segment = lipgloss.NewStyle().
		BorderForegroundBlend(charmtone.Mauve, charmtone.Bengal).
		Border(lipgloss.NormalBorder(), false).
		Height(15)
)

func main() {
	printer := niceyaml.NewPrinter()

	// Print YAML with syntax highlighting.
	syntaxDemo := printer.Print(niceyaml.NewSourceFromString(source))

	// Show diff between two YAML documents.
	beforeRev := niceyaml.NewRevision(niceyaml.NewSourceFromString(original, niceyaml.WithName("before")))
	afterRev := niceyaml.NewRevision(niceyaml.NewSourceFromString(modified, niceyaml.WithName("after")))
	diff := niceyaml.NewFullDiff(beforeRev, afterRev)
	diffDemo := printer.Print(diff.Lines())

	// Find and highlight all occurrences of "fe".
	findLines := niceyaml.NewSourceFromString(find)
	finder := niceyaml.NewFinder("fe", niceyaml.WithNormalizer(niceyaml.StandardNormalizer{}))
	matches := finder.Find(findLines)

	highlight := lipgloss.NewStyle().Background(charmtone.Mustard).Foreground(charmtone.Charcoal)
	for _, m := range matches {
		printer.AddStyleToRange(&highlight, m)
	}

	findDemo := printer.Print(findLines)
	printer.ClearStyles()

	out := lipgloss.NewStyle().
		BorderForegroundBlend(charmtone.Mauve, charmtone.Bengal).
		BorderStyle(lipgloss.NormalBorder()).
		Padding(0, 1).
		Render(
			lipgloss.JoinHorizontal(
				lipgloss.Top,
				syntaxDemo,
				segment.BorderLeft(true).Render(""),
				diffDemo,
				segment.BorderLeft(true).Render(""),
				findDemo,
			),
		)

	fmt.Println(out)
}
