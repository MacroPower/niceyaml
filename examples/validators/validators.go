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
	strict := niceyaml.WithUnknownFields(niceyaml.UnknownFieldsReject)
	_, err := source.Decode[menu](ctx, strict)
	lipgloss.Println(p.PrintError(err))

	// ValidatorFunc adapts a function to the Validator interface, and Each
	// runs a rule on every breakfast.
	rules := []niceyaml.Validator{
		niceyaml.ValidatorFunc(capped),
		niceyaml.ValidatorFunc(distinct),
		niceyaml.Each(paths.Current().Child("breakfast").IndexAll(), niceyaml.ValidatorFunc(affordable)),
	}

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
func capped(_ context.Context, n *niceyaml.Node) error {
	// A validator selects the nodes it checks, and the decode that runs
	// it reads the menu.
	breakfasts, err := n.Nodes(paths.Current().Child("breakfast").IndexAll())
	if err != nil {
		return fmt.Errorf("select breakfasts: %w", err)
	}

	if len(breakfasts) > 2 {
		// The path starts at the node the validator checks.
		return niceyaml.NewError("more than two breakfasts", niceyaml.AtPath(paths.Current().Child("breakfast")))
	}

	return nil
}

// distinct rejects a menu that lists an item twice.
func distinct(ctx context.Context, n *niceyaml.Node) error {
	items, err := n.Nodes(paths.Current().Child("breakfast").IndexAll().Child("item"))
	if err != nil {
		return fmt.Errorf("select items: %w", err)
	}

	seen := map[string]bool{}

	for _, item := range items {
		// A scoped decode reads this one value.
		name, err := item.Decode[string](ctx)
		if err != nil {
			return fmt.Errorf("decode item: %w", err)
		}

		if seen[name] {
			// The path of a Node starts at the root of the document.
			return niceyaml.NewError("item is listed twice", niceyaml.AtPath(item.Path()))
		}

		seen[name] = true
	}

	return nil
}

// affordable rejects a breakfast that costs more than 190 kr.
func affordable(ctx context.Context, n *niceyaml.Node) error {
	// The path starts at the breakfast that Each runs the rule on.
	pricePath := paths.Current().Child("price")

	price, err := n.DecodeAt[int](ctx, pricePath)
	if err != nil {
		return fmt.Errorf("decode price: %w", err)
	}

	if price > 190 {
		return niceyaml.NewError("costs more than 190 kr", niceyaml.AtPath(pricePath))
	}

	return nil
}
