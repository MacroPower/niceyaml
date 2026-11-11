package main

import (
	"context"
	"fmt"
	"time"

	"charm.land/lipgloss/v2"

	_ "embed"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/diff"
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

	// Create a diff result between the two sources. A deadline on the
	// context bounds the time the diff takes, and the diff returns the
	// error of the context when the deadline passes.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	result, err := diff.Diff(ctx, before.Lines(), after.Lines())
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	fmt.Println("\nPrint the full diff:")
	lipgloss.Println(p.Print(result.Unified()))

	fmt.Println("\nPrint the summary diff:")

	lipgloss.Println(p.Print(result.Hunks(2)))

	// String renders a view without styles, as a log or a golden file
	// wants it. It writes the marker of each deleted or inserted line in
	// place of the bar that follows the number.
	fmt.Println("\nPrint the summary diff as plain text:")
	fmt.Println(result.Hunks(2).String())

	// Each change spans the lines it deletes and inserts. The spans are
	// half-open and count lines from zero, so a program can report or apply
	// the changes without printing the diff.
	fmt.Println("\nList the changes:")

	for _, c := range result.Changes() {
		fmt.Printf("lines [%d, %d) of before become lines [%d, %d) of after\n",
			c.Before.Start, c.Before.End, c.After.Start, c.After.End)
	}
}
