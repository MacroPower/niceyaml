package main

import (
	"fmt"

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
		printer.WithStyles(theme.Charm()),
		printer.WithGutter(printer.DefaultGutter),
	)

	fmt.Println("\nPrint with syntax highlighting:")
	fmt.Println(p.Print(source))

	fmt.Println("\nOnly render lines 2-4, 12-13:")

	hunk1 := position.NewSpan(1, 4)
	hunk2 := position.NewSpan(11, 13)
	fmt.Println(p.Print(source, hunk1, hunk2))
}
