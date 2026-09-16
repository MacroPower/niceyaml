package main

import (
	"fmt"

	"charm.land/lipgloss/v2"

	_ "embed"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/finder"
	"go.jacobcolvin.com/niceyaml/normalizer"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/theme"
)

// highlightKind is a custom style.Style constant for search highlights.
const highlightKind style.Style = "highlightCustom"

var (
	//go:embed demo.yaml
	example string

	highlight = lipgloss.NewStyle().Background(lipgloss.Color("3"))
)

func main() {
	source := niceyaml.NewSourceFromString(example)

	// Create a printer with styles that include the highlight overlay style.
	p := printer.New(
		printer.WithStyles(theme.Charm().With(
			style.Set(highlightKind, highlight),
		)),
	)

	// Create a finder with standard normalization.
	// The standard normalizer ignores case and diacritics.
	f := finder.New(
		finder.WithNormalizer(normalizer.New()),
	)

	// Load the source to build an internal index.
	f.Load(source)

	// Find all occurrences of "cafe" in the source.
	results := f.Find("cafe")

	// Highlight the matches on a view of the source.
	view := source.Lines()
	view.AddOverlay(highlightKind, results...)

	fmt.Println("\nPrint with matches highlighted:")
	fmt.Println(p.Print(view))
}
