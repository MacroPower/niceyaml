package main

import (
	"fmt"

	"charm.land/lipgloss/v2"

	_ "embed"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/normalizer"
	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/theme"
)

// highlightKind is a custom style.Style constant for search highlights.
const highlightKind style.Style = "highlightDim"

var (
	//go:embed demo.yaml
	example string

	highlight = lipgloss.NewStyle().Background(lipgloss.Color("3"))
)

func main() {
	source := niceyaml.NewSourceFromString(example)

	// Create a printer with styles that include the highlight overlay style.
	printer := niceyaml.NewPrinter(
		niceyaml.WithStyles(theme.Charm().With(
			style.Set(highlightKind, highlight),
		)),
	)

	// Create a finder with standard normalization.
	// The standard normalizer ignores case and diacritics.
	finder := niceyaml.NewFinder(
		niceyaml.WithNormalizer(normalizer.New()),
	)

	// Load the source to build an internal index.
	finder.Load(source)

	// Find all occurrences of "cafe" in the source.
	results := finder.Find("cafe")

	// Add overlays for the found ranges.
	source.AddOverlay(highlightKind, results...)

	fmt.Println("\nPrint with matches highlighted:")
	fmt.Println(printer.Print(source))
}
