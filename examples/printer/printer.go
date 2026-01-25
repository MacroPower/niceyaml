package main

import (
	"fmt"

	_ "embed"

	"jacobcolvin.com/niceyaml"
	"jacobcolvin.com/niceyaml/position"
	"jacobcolvin.com/niceyaml/style/theme"
)

//go:embed demo.yaml
var example string

func main() {
	source := niceyaml.NewSourceFromString(example)

	printer := niceyaml.NewPrinter(
		niceyaml.WithStyles(theme.Charm()),
		niceyaml.WithGutter(niceyaml.DefaultGutter()),
	)

	fmt.Println("\nPrint with syntax highlighting:")
	fmt.Println(printer.Print(source))

	fmt.Println("\nOnly render lines 2-4, 12-13:")

	hunk1 := position.NewSpan(1, 4)
	hunk2 := position.NewSpan(11, 13)
	fmt.Println(printer.Print(source, hunk1, hunk2))
}
