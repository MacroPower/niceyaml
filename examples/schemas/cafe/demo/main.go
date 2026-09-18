// Command demo loads cafe configurations and validates them against the
// embedded JSON schema, rendering any schema or custom validation failures
// with niceyaml's source-annotated error printer.
package main

import (
	"context"
	"fmt"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/examples/schemas/cafe"
	"go.jacobcolvin.com/niceyaml/printer"
)

func main() {
	p := printer.New()

	fmt.Println("Validating the default cafe configuration:")

	cfg, err := load(cafe.DefaultYAML)
	if err != nil {
		fmt.Println(p.PrintError(err, 2))
	} else {
		fmt.Printf("valid: %q with %d menu items, open %s-%s\n",
			cfg.Metadata.Name, len(cfg.Spec.Menu.Items), cfg.Spec.Hours.Open, cfg.Spec.Hours.Close)
	}

	fmt.Println("\nValidating a broken cafe configuration:")

	_, err = load(cafe.BrokenYAML)
	if err != nil {
		fmt.Println(p.PrintError(err, 2))
	}
}

// load parses a cafe configuration and validates it. A single Decode runs
// the JSON schema from [cafe.Schema] first, then the custom open-before-close
// check that [cafe.Config] implements. Failures come back bound to the
// source, so they print with the offending lines highlighted.
func load(in string) (*cafe.Config, error) {
	source := niceyaml.NewSourceFromString(in)

	cfg, err := source.Decode[cafe.Config](context.Background(), niceyaml.WithValidator(cafe.Schema))
	if err != nil {
		return nil, err
	}

	return &cfg, nil
}
