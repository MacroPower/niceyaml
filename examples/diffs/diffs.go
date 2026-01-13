package main

import (
	"fmt"

	_ "embed"

	"github.com/macropower/niceyaml"
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

	// Create a full diff between the two revisions.
	diff := niceyaml.NewFullDiff(rev.Origin(), rev.Tip())

	fmt.Println("\nPrint the full diff:")
	fmt.Println(printer.Print(diff.Source()))

	// Create a summary diff between the two revisions.
	linesOfContext := 2
	summaryDiff := niceyaml.NewSummaryDiff(rev.Origin(), rev.Tip(), linesOfContext)

	fmt.Println("\nPrint the summary diff:")
	fmt.Println(printer.Print(summaryDiff.Source()))
}
