package main

import (
	"fmt"

	_ "embed"

	"github.com/macropower/niceyaml"
	"github.com/macropower/niceyaml/style/theme"
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

	fmt.Println("\nOnly render lines 2-4:")
	fmt.Println(printer.PrintSlice(source, 1, 3))
}
