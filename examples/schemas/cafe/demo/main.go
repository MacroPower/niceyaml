// Command demo loads cafe configurations and validates them against the
// embedded JSON schema, rendering any schema or custom validation failures
// with niceyaml's source-annotated error printer.
package main

import (
	"fmt"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/examples/schemas/cafe"
)

func main() {
	fmt.Println("Validating the default cafe configuration:")

	cfg, err := load(cafe.DefaultYAML)
	if err != nil {
		fmt.Println(err)
	} else {
		fmt.Printf("valid: %q with %d menu items, open %s-%s\n",
			cfg.Metadata.Name, len(cfg.Spec.Menu.Items), cfg.Spec.Hours.Open, cfg.Spec.Hours.Close)
	}

	fmt.Println("\nValidating a broken cafe configuration:")

	_, err = load(cafe.BrokenYAML)
	if err != nil {
		fmt.Println(err)
	}
}

// load parses a cafe configuration and validates it. Because [cafe.Config]
// implements both schema and self validation, a single Unmarshal runs the JSON
// schema first, then the custom open-before-close check. Failures are wrapped
// against the source so they print with the offending lines highlighted.
func load(in string) (*cafe.Config, error) {
	source := niceyaml.NewSourceFromString(in)

	decoder, err := source.Decoder()
	if err != nil {
		return nil, err
	}

	cfg := cafe.NewConfig()

	for _, doc := range decoder.Documents() {
		err := doc.Unmarshal(&cfg)
		if err != nil {
			return nil, source.WrapError(err)
		}
	}

	return &cfg, nil
}
