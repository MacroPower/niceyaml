package main

import (
	"fmt"

	_ "embed"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/differ"
	"go.jacobcolvin.com/niceyaml/printer"
)

var (
	//go:embed before.yaml
	exampleBefore string

	//go:embed after.yaml
	exampleAfter string
)

func main() {
	before := niceyaml.NewSourceFromString(exampleBefore)
	after := niceyaml.NewSourceFromString(exampleAfter)

	p := printer.New()

	// Create a diff result between the two sources.
	result := differ.Diff(before, after)

	fmt.Println("\nPrint the full diff:")
	fmt.Println(p.Print(result.Unified()))

	fmt.Println("\nPrint the summary diff:")

	fmt.Println(p.Print(result.Hunks(2)))
}
