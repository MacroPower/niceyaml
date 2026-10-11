package main

import (
	"context"
	"fmt"

	"charm.land/lipgloss/v2"

	_ "embed"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/printer"
)

//go:embed demo.yaml
var example string

// menu is the type the document decodes into.
type menu struct {
	Breakfast []struct {
		Item    string   `yaml:"item"`
		Dietary []string `yaml:"dietary"`
		Price   int      `yaml:"price"`
	} `yaml:"breakfast"`
}

func main() {
	ctx := context.Background()
	source := niceyaml.NewSourceFromString(example, niceyaml.WithName("demo.yaml"))
	p := printer.New()

	fmt.Println("\nReject unknown fields:")

	// Without the option the decoder skips a key that no field reads, so
	// the misspelled key on line 6 would pass.
	strict := niceyaml.WithDisallowUnknownFields(true)
	_, err := source.Decode[menu](ctx, strict)
	lipgloss.Println(p.PrintError(err))

	// ValidatorFunc adapts a function to the Validator interface.
	rules := []niceyaml.Validator{niceyaml.ValidatorFunc(capped), niceyaml.ValidatorFunc(distinct)}

	fmt.Println("\nReport every failure with MultiValidator:")

	// Validators run before the decoder, so their failures end the decode
	// before it reaches the unknown field.
	_, err = source.Decode[menu](ctx, strict, niceyaml.WithValidator(niceyaml.MultiValidator(rules...)))
	lipgloss.Println(p.PrintError(err))

	fmt.Println("\nStop at the first failure with ChainValidator:")

	// Repeated WithValidator options chain their validators the same way.
	_, err = source.Decode[menu](ctx, strict, niceyaml.WithValidator(niceyaml.ChainValidator(rules...)))
	lipgloss.Println(p.PrintError(err))
}

// capped rejects a menu that lists more than two breakfasts.
func capped(ctx context.Context, n *niceyaml.Node) error {
	m, err := n.Decode[menu](ctx)
	if err != nil {
		return fmt.Errorf("decode menu: %w", err)
	}

	if len(m.Breakfast) > 2 {
		// The path starts at the node the validator checks.
		return niceyaml.NewError("more than two breakfasts", niceyaml.AtPath(paths.Current().Child("breakfast")))
	}

	return nil
}

// distinct rejects a menu that lists an item twice.
func distinct(ctx context.Context, n *niceyaml.Node) error {
	m, err := n.Decode[menu](ctx)
	if err != nil {
		return fmt.Errorf("decode menu: %w", err)
	}

	seen := map[string]bool{}

	for i, it := range m.Breakfast {
		if seen[it.Item] {
			at := paths.Current().Child("breakfast").Index(i).Child("item")

			return niceyaml.NewError("item is listed twice", niceyaml.AtPath(at))
		}

		seen[it.Item] = true
	}

	return nil
}
