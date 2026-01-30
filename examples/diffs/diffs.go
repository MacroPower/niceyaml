package main

import (
	"fmt"

	_ "embed"

	"jacobcolvin.com/niceyaml"
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

	printer := niceyaml.NewPrinter()

	// Create an initial revision.
	rev := niceyaml.NewRevision(before)

	// Append a new revision.
	rev = rev.Append(after)

	// Create a diff result between the two revisions.
	result := niceyaml.Diff(rev.Origin(), rev.Tip())

	fmt.Println("\nPrint the full diff:")
	fmt.Println(printer.Print(result.Unified()))

	fmt.Println("\nPrint the summary diff:")

	source, ranges := result.Hunks(2)
	fmt.Println(printer.Print(source, ranges...))
}
