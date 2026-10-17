package main

import (
	"context"
	"errors"
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

// milk is a milk of the menu, which both drinks read through an alias.
type milk struct {
	Name string `yaml:"name"`
	Cost int    `yaml:"cost"`
}

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

	// Each call of show prints the changes of the Doc to the file.
	show := func() {
		text, err := edit.FileText(d)
		if err != nil {
			lipgloss.Println(p.PrintError(err))

			return
		}

		edited := niceyaml.NewSourceFromString(text)
		lipgloss.Println(p.Print(diff.Diff(source.Lines(), edited.Lines()).Hunks(1)))
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

	fmt.Println("\nEdit a shared value through each alias:")

	// Both drinks read the milk through the alias *oat, so the first call
	// raises the cost of the milk for every drink. The calls act in order,
	// so the call for the Latte reads the cost that the first call wrote
	// and raises it again. A price rise is not a step of v2, so it takes a
	// label of its own.
	err = d.Apply(ctx, drinks, func(ctx context.Context, n *edit.Node) error {
		cost := n.At(paths.Current().Child("milk", "cost"))

		amount, err := cost.Decode[int](ctx)
		if err != nil {
			return fmt.Errorf("decode milk cost: %w", err)
		}

		return cost.Set(ctx, amount+1)
	}, edit.WithLabel("the price rise"))
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	cost, err := d.Node().At(paths.MustParse("$.milk.cost"))
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	amount, err := cost.Decode[int](ctx)
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	fmt.Println("The milk costs", amount)

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

	fmt.Println("\nReplace an anchored value:")

	// The milk entry defines the anchor that both drinks read. A Set there
	// keeps the anchor and writes the new value after it, so both drinks
	// read the new milk. The Set writes the whole value, so the comment
	// inside the milk goes.
	err = d.Apply(ctx, paths.Doc().Child("milk"), func(ctx context.Context, n *edit.Node) error {
		m, err := n.Decode[milk](ctx)
		if err != nil {
			return fmt.Errorf("decode milk: %w", err)
		}

		m.Name = "Havremjölk Barista"

		return n.Set(ctx, m)
	}, label)
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	name, err := d.Node().At(paths.MustParse("$.drinks[1].milk.name"))
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	latte, err := name.Decode[string](ctx)
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	fmt.Println("The Latte takes", latte)
	show()

	fmt.Println("\nRemove an anchored value:")

	// Both drinks still read the milk through their aliases, and the engine
	// does not write a copy of the milk at an alias. So the Remove fails
	// with ErrUnsupported, and the Doc stays as it was.
	remove := func(_ context.Context, n *edit.Node) error {
		return n.Remove()
	}

	err = d.Apply(ctx, paths.Doc().Child("milk"), remove, label)
	lipgloss.Println(p.PrintError(err))
	fmt.Println("Unsupported:", errors.Is(err, edit.ErrUnsupported))

	// The error asks for a Set of each alias to the value that it reads
	// first. A Set at an alias replaces the alias, so after those Sets no
	// alias reads the milk, and the Remove passes.
	err = d.Apply(ctx, paths.MustParse("$.drinks[*].milk"), func(ctx context.Context, n *edit.Node) error {
		m, err := n.Decode[milk](ctx)
		if err != nil {
			return fmt.Errorf("decode milk: %w", err)
		}

		return n.Set(ctx, m)
	}, label)
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	err = d.Apply(ctx, paths.Doc().Child("milk"), remove, label)
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	// Each Apply that failed left the Doc as it was. So the price rise, the
	// new milk, the Sets of the aliases, and the removal are the only
	// changes to the file. The Kaffe keeps the key cost, and the milk costs
	// 7.
	show()
}
