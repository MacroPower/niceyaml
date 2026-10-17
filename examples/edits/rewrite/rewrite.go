package main

import (
	"context"
	"fmt"
	"time"

	"charm.land/lipgloss/v2"

	_ "embed"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/diff"
	"go.jacobcolvin.com/niceyaml/edit"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/printer"
)

var (
	//go:embed demo.yaml
	example string

	restaurantPath = paths.Doc().Child("restaurant")
	closePath      = restaurantPath.Child("hours", "close")
)

func main() {
	ctx := context.Background()
	source := niceyaml.NewSourceFromString(example, niceyaml.WithName("demo.yaml"))
	p := printer.New()

	// A Doc edits one document, so New takes the document of the source.
	doc, err := source.Document()
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	d, err := edit.New(doc)
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	// A label names the edits of an Apply in its messages and its
	// removals. An upgrade names its edits by the version that it brings
	// the file to.
	label := edit.WithLabel("v2")

	// Apply calls the Func once on each place that the path selects, so
	// one wildcard path renames the key of every item on the menu.
	err = d.Apply(ctx, paths.MustParse("$.menu.*[*].cost"), func(_ context.Context, n *edit.Node) error {
		return n.SetKey("price")
	}, label)
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	// An Apply at the root reaches any place through At, including a
	// place that does not hold a value yet.
	err = d.Apply(ctx, paths.Doc(), func(ctx context.Context, root *edit.Node) error {
		// Move takes the value and its comment to the new path. It also
		// deletes one of the blank lines around wifi, so restaurant and menu
		// stay one blank line apart.
		err := root.At(paths.Doc().Child("wifi")).Move(restaurantPath.Child("wifi"))
		if err != nil {
			return err
		}

		// Set creates the key at the indent of the other keys of
		// restaurant.
		err = root.At(restaurantPath.Child("currency")).Set(ctx, "SEK")
		if err != nil {
			return err
		}

		// Remove deletes the entry with its comment, and Removals returns
		// the reason to the program.
		return root.At(restaurantPath.Child("stars")).Remove(edit.WithReason("the menu board does not show ratings"))
	}, label)
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	// A command such as `cafe set` applies the value that its user typed,
	// here a closing time that does not exist. Its label names the command
	// in the errors that the value causes. A Go string keeps the quotes of
	// the scalar that it replaces, and the comment stays on its line.
	err = d.Apply(ctx, closePath, func(ctx context.Context, n *edit.Node) error {
		return n.Set(ctx, "25:00")
	}, edit.WithLabel("the cafe set command"))
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	// FileText returns the text to write back to the file. A file can hold
	// several documents, and each one that no Doc edits keeps its text.
	text, err := edit.FileText(d)
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	fmt.Println("\nPrint what the edits changed:")

	// Every line that the edits leave alone keeps the bytes that the owner
	// wrote. Hunks shows each change with one line of context around it.
	edited := niceyaml.NewSourceFromString(text)
	lipgloss.Println(p.Print(diff.Diff(source.Lines(), edited.Lines()).Hunks(1)))

	fmt.Println("\nList the removals:")

	// The removal of stars names the entry by the path that the owner's
	// text spells, so a program can warn the owner. The path of an entry
	// ends in ~, which selects its key.
	for _, rm := range d.Removals() {
		fmt.Printf("line %d: %s removes %s: %s\n", rm.Range.Start.Line+1, rm.Label, rm.Path, rm.Reason)
	}

	fmt.Println("\nValidate the edited document:")

	// Node returns the edited document, and its errors bind in the text
	// that the owner wrote. The owner's file spells each price as cost, so
	// an error at a price names the line and the path that the owner
	// knows. An error at a value that an edit wrote also names the edit.
	// Each runs affordable on every price and reports each one that fails.
	prices := paths.MustParse("$.menu.*[*].price")
	rules := niceyaml.MultiValidator(
		niceyaml.Each(prices, niceyaml.ValidatorFunc(affordable)),
		niceyaml.ValidatorFunc(clock),
	)
	err = d.Node().Validate(ctx, rules)
	lipgloss.Println(p.PrintError(err))
}

// affordable rejects a price of more than 190 kr.
func affordable(ctx context.Context, n *niceyaml.Node) error {
	kr, err := n.Decode[int](ctx)
	if err != nil {
		return fmt.Errorf("decode price: %w", err)
	}

	if kr > 190 {
		// Each binds the error at the price that it checked.
		return niceyaml.NewError("costs more than 190 kr")
	}

	return nil
}

// clock rejects a closing time that is not a time of day.
func clock(ctx context.Context, n *niceyaml.Node) error {
	text, err := n.DecodeAt[string](ctx, closePath)
	if err != nil {
		return fmt.Errorf("decode closing time: %w", err)
	}

	_, err = time.Parse("15:04", text)
	if err != nil {
		return niceyaml.Invalid(err, niceyaml.AtPath(closePath))
	}

	return nil
}
