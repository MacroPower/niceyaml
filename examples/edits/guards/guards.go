package main

import (
	"context"
	"fmt"

	"charm.land/lipgloss/v2"

	_ "embed"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/diff"
	"go.jacobcolvin.com/niceyaml/edit"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/printer"
)

//go:embed demo.yaml
var example string

func main() {
	ctx := context.Background()
	source := niceyaml.NewSourceFromString(example, niceyaml.WithName("demo.yaml"))
	p := printer.New()

	doc, err := source.Document()
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	// WithProtected protects the type fields from every Func. The type
	// fields tell a loader what the document holds.
	d, err := edit.New(doc, edit.WithProtected(paths.Doc().Child("apiVersion"), paths.Doc().Child("kind")))
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	// WithLabel names the edits of one Apply in its messages.
	label := edit.WithLabel("v2")
	drinks := paths.MustParse("$.drinks[*]")

	fmt.Println("\nRoll back an Apply that fails:")

	// The rename of the Kaffe succeeds, but the Latte holds a price
	// already, so its rename fails. Each Apply is one transaction, so the
	// Doc drops the rename of the Kaffe too.
	err = d.Apply(ctx, drinks, func(_ context.Context, n *edit.Node) error {
		return n.At(paths.Current().Child("cost")).SetKey("price")
	}, label)
	lipgloss.Println(p.PrintError(err))
	fmt.Println("Edited:", d.Edited())

	fmt.Println("\nRefuse a second edit of a shared value:")

	// Both drinks read the milk through the alias *oat, so the first call
	// raises the cost of the milk for every drink. The call for the Latte
	// reads the cost that the first call wrote, and the Apply fails before
	// it raises the cost a second time. A price rise is not a step of v2,
	// so it takes a label of its own.
	err = d.Apply(ctx, drinks, func(ctx context.Context, n *edit.Node) error {
		cost := n.At(paths.Current().Child("milk", "cost"))

		amount, err := cost.Decode[int](ctx)
		if err != nil {
			return fmt.Errorf("decode milk cost: %w", err)
		}

		return cost.Set(ctx, amount+1)
	}, edit.WithLabel("the price rise"))
	lipgloss.Println(p.PrintError(err))

	fmt.Println("\nProtect the type fields:")

	// A path never selects a protected value, so a wildcard at the root
	// skips the type fields.
	err = d.Apply(ctx, paths.Doc().ChildAll(), func(_ context.Context, n *edit.Node) error {
		fmt.Println("visit", n.Path())

		return nil
	})
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	// A Func still reaches a protected value through At, and each edit
	// there fails.
	err = d.Apply(ctx, paths.Doc(), func(ctx context.Context, root *edit.Node) error {
		return root.At(paths.Doc().Child("kind")).Set(ctx, "Meny")
	}, label)
	lipgloss.Println(p.PrintError(err))

	fmt.Println("\nRemove an anchored value:")

	// The milk entry defines the anchor that both drinks read. Remove
	// writes the value with its anchor at the first alias that reads it, so
	// the alias of the Latte still reads the same milk.
	err = d.Apply(ctx, paths.Doc().Child("milk"), func(_ context.Context, n *edit.Node) error {
		return n.Remove()
	}, label)
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	// Each Apply that failed left the Doc as it was, so the removal is the
	// only change to the file. The Kaffe keeps the key cost, and the milk
	// still costs 5.
	text, err := edit.FileText(d)
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	edited := niceyaml.NewSourceFromString(text)
	lipgloss.Println(p.Print(diff.Diff(source.Lines(), edited.Lines()).Hunks(1)))
}
