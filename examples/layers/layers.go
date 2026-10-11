package main

import (
	"context"
	"fmt"

	"charm.land/lipgloss/v2"

	_ "embed"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/printer"
)

var (
	//go:embed base.yaml
	exampleBase string

	//go:embed prod.yaml
	exampleProd string
)

func main() {
	ctx := context.Background()
	p := printer.New()

	// NewSourceFromFile names a layer after its file. These layers are
	// embedded, so WithName names each one.
	base := niceyaml.NewSourceFromString(exampleBase, niceyaml.WithName("base.yaml"))
	prod := niceyaml.NewSourceFromString(exampleProd, niceyaml.WithName("prod.yaml"))

	// Pass the layers lowest first. A mapping merges key by key, and a
	// sequence or a scalar replaces what the layer below holds.
	merged, err := niceyaml.NewSourceFromLayers(ctx, base, prod)
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	fmt.Println("\nPrint the merged document:")
	lipgloss.Println(p.Print(merged.View()))

	// The merged Source holds one document, as the Source of a file does.
	doc, err := merged.Document()
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	fmt.Println("\nTrace each value to the layer that set it:")

	// The recursive wildcard selects every node below the root.
	nodes, err := doc.Nodes(paths.Doc().RecursiveAll())
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	for _, node := range nodes {
		// A mapping can hold keys from several layers, so trace scalars alone.
		if node.Kind() != niceyaml.NodeScalar {
			continue
		}

		// Origin returns the Node that holds the value in the file of its layer.
		origin, err := node.Origin()
		if err != nil {
			lipgloss.Println(p.PrintError(err))

			return
		}

		fmt.Printf("%-14s %s:%d\n", node.Path(), origin.Source().Name(), origin.Span().Start+1)
	}

	fmt.Println("\nReport an error in the layer that holds its value:")

	// The merged text is no file, so an error at a path binds in the layer
	// that set the value.
	closePath := paths.Doc().Child("hours", "close")
	tooLate := niceyaml.NewError("closing time is past 20:00", niceyaml.AtPath(closePath))
	lipgloss.Println(p.PrintError(doc.Bind(tooLate)))
}
