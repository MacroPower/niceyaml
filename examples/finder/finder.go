package main

import (
	"fmt"

	"charm.land/lipgloss/v2"

	_ "embed"

	"github.com/macropower/niceyaml"
)

var (
	//go:embed demo.yaml
	example string

	highlight = lipgloss.NewStyle().Background(lipgloss.Color("3"))
)

func main() {
	source := niceyaml.NewSourceFromString(example)

	printer := niceyaml.NewPrinter()

	// Create a finder with standard normalization.
	// The standard normalizer ignores case and diacritics.
	finder := niceyaml.NewFinder(
		niceyaml.WithNormalizer(niceyaml.NewStandardNormalizer()),
	)

	// Load the source to build an internal index.
	finder.Load(source)

	// Find all occurrences of "cafe" in the source.
	results := finder.Find("cafe")

	// Highlight all results in the printer.
	printer.AddStyleToRange(&highlight, results...)

	fmt.Println("\nPrint with matches highlighted:")
	fmt.Println(printer.Print(source))
}
