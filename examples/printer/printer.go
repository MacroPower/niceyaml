package main

import (
	"fmt"

	"charm.land/lipgloss/v2"

	_ "embed"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/style/theme"
)

//go:embed demo.yaml
var example string

func main() {
	source := niceyaml.NewSourceFromString(example)

	p := printer.New(
		printer.WithStyles(theme.Charm),
		printer.WithGutter(printer.DefaultGutter),
	)

	fmt.Println("\nPrint with syntax highlighting:")

	// The lipgloss writers convert the colors to what the terminal
	// supports and drop them when the output is a pipe or a file.
	lipgloss.Println(p.Print(source.View()))

	fmt.Println("\nOnly render lines 2-4, 12-13:")

	hunk1 := position.NewSpan(1, 4)
	hunk2 := position.NewSpan(11, 13)
	lipgloss.Println(p.Print(source.View().Slice(hunk1, hunk2)))
}
