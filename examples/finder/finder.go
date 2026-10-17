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
	"go.jacobcolvin.com/niceyaml/style/kind"
	"go.jacobcolvin.com/niceyaml/style/theme"
)

// highlightKind is a custom kind.Kind constant for search highlights.
const highlightKind kind.Kind = "highlightCustom"

//go:embed demo.yaml
var example string

func main() {
	source := niceyaml.NewSourceFromString(example)

	// Create a printer whose styles place the highlight kind under the
	// theme's own highlight kind, so it takes that color under any theme.
	// The box fills the rectangle behind the rows with the theme's surface.
	p := printer.New(
		printer.WithStyles(theme.Charm.Styles().With(
			style.Inherit(highlightKind, kind.GenericHighlight),
		)),
		printer.WithBox(),
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

	// A nil normalizer turns off case and diacritic folding, so "cafe"
	// does not find "Café". Only the exact spelling "Café" matches.
	exact := finder.New(finder.WithNormalizer(nil)).Load(source.Lines())

	for _, search := range []string{"cafe", "Café"} {
		fmt.Printf("%q matches: %d\n", search, len(exact.Find(search)))
	}

	fmt.Println("\nFold case but keep diacritics:")

	// By default, finders normalize text with normalizer.New. Options such
	// as normalizer.WithDiacriticFold turn single stages of the
	// normalizer's pipeline on or off. Without diacritic folding, "CAFÉ"
	// finds "Café", but "cafe" does not.
	cased := finder.New(finder.WithNormalizer(
		normalizer.New(normalizer.WithDiacriticFold(false)),
	)).Load(source.Lines())

	for _, search := range []string{"cafe", "CAFÉ"} {
		fmt.Printf("%q matches: %d\n", search, len(cased.Find(search)))
	}
}
