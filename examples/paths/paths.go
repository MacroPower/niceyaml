package main

import (
	"context"
	"fmt"

	"charm.land/lipgloss/v2"

	_ "embed"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/style/kind"
)

//go:embed demo.yaml
var example string

func main() {
	ctx := context.Background()
	source := niceyaml.NewSourceFromString(example)
	p := printer.New()

	// A path resolves in one document, so take the document of the source.
	doc, err := source.Document()
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	fmt.Println("\nDecode the value at a path:")

	// Build a path one selector at a time from the root of the document.
	namePath := paths.Doc().Child("restaurant", "name")

	name, err := doc.DecodeAt[string](ctx, namePath)
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	fmt.Printf("%s = %s\n", namePath, name)

	fmt.Println("\nHighlight every node a wildcard path selects:")

	// MustParse reads the same path syntax. A wildcard path selects any
	// number of nodes, and each Node reports the path that selects it alone.
	prices, err := doc.Nodes(paths.MustParse("$.menu.*[*].price"))
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	view := source.View()

	for _, price := range prices {
		fmt.Println(price.Path())

		// Ranges returns the columns of the value at a path.
		ranges, err := doc.Ranges(price.Path())
		if err != nil {
			lipgloss.Println(p.PrintError(err))

			return
		}

		view.AddOverlay(kind.GenericHighlight, ranges...)
	}

	lipgloss.Println(p.Print(view))

	fmt.Println("\nFind the path at a position:")

	// A position counts lines and columns from 0, as a cursor in a viewer
	// does. A position on a key gives a path that ends in "~".
	for _, cursor := range []position.Position{position.New(13, 6), position.New(13, 14)} {
		if path, ok := doc.PathAt(cursor); ok {
			fmt.Printf("line %d, column %d: %s\n", cursor.Line+1, cursor.Col+1, path)
		}
	}
}
