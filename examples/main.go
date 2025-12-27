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
	printer := niceyaml.NewPrinter(niceyaml.WithLineNumbers())

	red := lipgloss.NewStyle().Background(charmtone.Sapphire).Foreground(charmtone.Salt)
	printer.AddStyleToRange(&red, niceyaml.PositionRange{
		Start: niceyaml.Position{Line: 10, Col: 5},
		End:   niceyaml.Position{Line: 11, Col: 22},
	})

	blue := lipgloss.NewStyle().Background(charmtone.Cherry).Foreground(charmtone.Salt)
	printer.AddStyleToRange(&blue, niceyaml.PositionRange{
		Start: niceyaml.Position{Line: 11, Col: 13},
		End:   niceyaml.Position{Line: 11, Col: 31},
	})

	rangeDemo := printer.PrintTokens(niceyaml.NewLinesFromString(source))
	printer.ClearStyles()

	// Show diff between two YAML documents.
	beforeRev := niceyaml.NewRevision(niceyaml.NewLinesFromString(original, niceyaml.WithName("before")))
	afterRev := niceyaml.NewRevision(niceyaml.NewLinesFromString(modified, niceyaml.WithName("after")))
	diff := niceyaml.NewFullDiff(beforeRev, afterRev)
	diffDemo := printer.PrintTokens(diff.Lines())
	printer.ClearStyles()

	// Find and highlight all occurrences of "fe".
	findLines := niceyaml.NewLinesFromString(find)
	finder := niceyaml.NewFinder("fe", niceyaml.WithNormalizer(niceyaml.StandardNormalizer{}))
	matches := finder.Find(findLines)

	highlight := lipgloss.NewStyle().Background(charmtone.Mustard).Foreground(charmtone.Charcoal)
	for _, m := range matches {
		printer.AddStyleToRange(&highlight, m)
	}

	findDemo := printer.PrintTokens(findLines)
	printer.ClearStyles()

	out := lipgloss.NewStyle().
		BorderForegroundBlend(charmtone.Mauve, charmtone.Bengal).
		BorderStyle(lipgloss.NormalBorder()).
		Padding(0, 1).
		Render(
			lipgloss.JoinHorizontal(
				lipgloss.Top,
				rangeDemo,
				segment.BorderLeft(true).Render(""),
				diffDemo,
				segment.BorderLeft(true).Render(""),
				findDemo,
			),
		)

	fmt.Println(out)
}
