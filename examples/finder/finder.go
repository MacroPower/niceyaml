package main

import (
	"fmt"

	"charm.land/lipgloss/v2"

	_ "embed"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/finder"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/style/kind"
	"go.jacobcolvin.com/niceyaml/style/theme"
)

// highlightKind is a custom kind.Kind for search highlights. It sits below
// kind.GenericHighlight, so it takes the highlight color of any theme.
const highlightKind = kind.GenericHighlight + ".match"

//go:embed demo.yaml
var example string

func main() {
	source := niceyaml.NewSourceFromString(example)

	// Create a printer with a theme as it is. The box fills the rectangle
	// behind the rows with the theme's surface.
	p := printer.New(
		printer.WithStyles(theme.Charm()),
		printer.WithBox(true),
	)

	// Finders ignore case and diacritics by default.
	f := finder.New()

	// Load the source lines to build a search index.
	idx := f.Load(source.Lines())

	// Find all occurrences of "cafe" in the source.
	results := idx.Find("cafe")

	// Highlight the matches on a view of the source.
	view := source.View()
	view.AddOverlay(highlightKind, results...)

	fmt.Println("\nPrint with matches highlighted:")
	lipgloss.Println(p.Print(view))

	fmt.Println("\nMatch characters exactly:")

	// The finder.Exact option turns off every built-in fold, so "cafe"
	// does not find "Café". Only the exact spelling "Café" matches.
	exact := finder.New(finder.Exact()).Load(source.Lines())

	for _, search := range []string{"cafe", "Café"} {
		fmt.Printf("%q matches: %d\n", search, len(exact.Find(search)))
	}

	fmt.Println("\nFold case but keep diacritics:")

	// Options such as finder.WithDiacriticFold turn single built-in folds
	// on or off. Without diacritic folding, "CAFÉ" finds "Café", but
	// "cafe" does not.
	cased := finder.New(finder.WithDiacriticFold(false)).Load(source.Lines())

	for _, search := range []string{"cafe", "CAFÉ"} {
		fmt.Printf("%q matches: %d\n", search, len(cased.Find(search)))
	}
}
