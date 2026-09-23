package main

import (
	"fmt"

	_ "embed"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/finder"
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
	p := printer.New(
		printer.WithStyles(theme.Charm.Styles().With(
			style.Inherit(highlightKind, kind.GenericHighlight),
		)),
	)

	// A finder ignores case and diacritics by default.
	f := finder.New()

	// Load the source lines to build a search index.
	idx := f.Load(source.Lines())

	// Find all occurrences of "cafe" in the source.
	results := idx.Find("cafe")

	// Highlight the matches on a view of the source.
	view := source.View()
	view.AddOverlay(highlightKind, results...)

	fmt.Println("\nPrint with matches highlighted:")
	fmt.Println(p.Print(view))
}
