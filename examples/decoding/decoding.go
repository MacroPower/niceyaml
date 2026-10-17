package main

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"charm.land/lipgloss/v2"

	_ "embed"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/printer"
)

// guestWifi is the network of a cafe whose document does not name one.
const guestWifi = "yamull-guest"

var (
	//go:embed demo.yaml
	example string

	// Every decode in the program takes these options, so each call reads
	// the documents by the same rules.
	decoding = niceyaml.DecodeOptions(
		niceyaml.WithUnknownFields(niceyaml.UnknownFieldsReject),
		// The decode builds each field of a config from the defaults of its
		// own type, and then this function runs. The function of the parent
		// thus decides each value it writes, so the kitchen printer listens
		// on 9101 and every other printer listens on 9100.
		niceyaml.WithDefaults(func(c *config) {
			c.Cafe.Seats = 20
			c.Kitchen.Port = 9101
			c.Wifi = guestWifi
		}),
		niceyaml.WithDefaults(func(p *receiptPrinter) {
			p.Host = "localhost"
			p.Port = 9100
		}),
		niceyaml.WithDefaults(func(i *item) {
			i.Available = true
		}),
	)
)

// item is one entry of the menu.
type item struct {
	Item      string `yaml:"item"`
	Price     uint16 `yaml:"price"`
	Available bool   `yaml:"available"`
}

// receiptPrinter is a printer the cafe sends orders to.
type receiptPrinter struct {
	Host string `yaml:"host"`
	Port uint16 `yaml:"port"`
}

// config is the type each document decodes into.
type config struct {
	Printers map[string]receiptPrinter `yaml:"printers"`
	Cafe     struct {
		Name  string `yaml:"name"`
		Seats int    `yaml:"seats"`
	} `yaml:"cafe"`
	Kitchen receiptPrinter `yaml:"kitchen"`
	Wifi    string         `yaml:"wifi"`
	Menu    []item         `yaml:"menu"`
}

func main() {
	ctx := context.Background()
	source := niceyaml.NewSourceFromString(example, niceyaml.WithName("demo.yaml"))
	docs := source.Documents()
	p := printer.New()

	fmt.Println("\nFill what the document leaves out:")

	// A field that the document leaves out keeps its default. The decode
	// also starts each element of the menu and each value of the printers
	// map from the defaults of its type. A null map value, such as bar,
	// holds those defaults too.
	cfg, err := docs[0].Decode[config](ctx, decoding)
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	fmt.Printf("%s seats %d, wifi: %s\n", cfg.Cafe.Name, cfg.Cafe.Seats, cfg.Wifi)

	for _, it := range cfg.Menu {
		fmt.Printf("  %-16s %d kr, available: %t\n", it.Item, it.Price, it.Available)
	}

	fmt.Printf("  %-16s %s:%d\n", "kitchen printer", cfg.Kitchen.Host, cfg.Kitchen.Port)

	for _, name := range slices.Sorted(maps.Keys(cfg.Printers)) {
		pr := cfg.Printers[name]
		fmt.Printf("  %-16s %s:%d\n", name+" printer", pr.Host, pr.Port)
	}

	fmt.Println("\nStart DecodeInto from the defaults:")

	// DecodeInto starts from the value that the caller passes, so a zero
	// config keeps the seats, wifi, and kitchen port at zero. Each menu item
	// and printer that the decode creates still starts from its defaults.
	// Defaults returns the value that Decode starts from with the same
	// options, and DecodeInto from that value matches Decode.
	starts := []struct {
		name string
		cfg  config
	}{
		{"zero value", config{}},
		{"Defaults", niceyaml.Defaults[config](decoding)},
	}

	for _, start := range starts {
		err := docs[0].DecodeInto(ctx, &start.cfg, decoding)
		if err != nil {
			lipgloss.Println(p.PrintError(err))

			return
		}

		c := start.cfg
		fmt.Printf("%-10s seats %d, wifi: %q, kitchen port: %d\n", start.name, c.Cafe.Seats, c.Wifi, c.Kitchen.Port)
	}

	fmt.Println("\nReport every problem of a document:")

	// The go-yaml decoder stops at the first value it rejects. The decode
	// then searches the whole document for the other problems and reports
	// every problem under one heading, in the order of the source.
	_, err = docs[1].Decode[config](ctx, decoding)
	lipgloss.Println(p.PrintError(err))

	fmt.Println("\nLabel each problem:")

	// A labeled problem matches ErrDecode and a label such as ErrWrongKind.
	// A program thus distinguishes the kinds without the error types of
	// go-yaml.
	for problem := range niceyaml.Problems(err) {
		path, _ := problem.Path()
		fmt.Printf("%-24s %s\n", path, label(problem.Err))
	}

	fmt.Println("\nDecode one value of each document:")

	// DecodeIfPresent decodes the value at one path, so it reads the wifi of
	// the second document although the whole document does not decode. The
	// caller fills the variable with its default, and DecodeIfPresent keeps
	// that default when the path selects nothing.
	wifiPath := paths.Doc().Child("wifi")

	for _, doc := range docs {
		wifi := guestWifi

		found, err := doc.DecodeIfPresent(ctx, wifiPath, &wifi, decoding)
		if err != nil {
			lipgloss.Println(p.PrintError(err))

			return
		}

		fmt.Printf("document %d: wifi %s, found: %t\n", doc.DocumentIndex()+1, wifi, found)
	}

	fmt.Println("\nBind an error to one value:")

	// At scopes a Node to the menu, and the decode reads that value alone.
	menuNode, err := docs[0].At(paths.Doc().Child("menu"))
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	menu, err := menuNode.Decode[[]item](ctx, decoding)
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	// The check servesFika returns an error that is not bound to a source,
	// and menuNode.Bind binds it to the menu. Bind resolves each `@` path
	// from the menu, and the bound error names the `$` path from the root of
	// the document.
	lipgloss.Println(p.PrintError(menuNode.Bind(servesFika(menu))))
}

// servesFika rejects a menu that does not sell Kanelbulle, which a fika needs.
func servesFika(menu []item) error {
	i := slices.IndexFunc(menu, func(it item) bool { return it.Item == "Kanelbulle" })
	switch {
	case i < 0:
		// An error without a path binds at the menu.
		return niceyaml.NewError("fika needs Kanelbulle")
	case !menu[i].Available:
		// The path starts at the menu that the check reads.
		return niceyaml.NewError("fika needs Kanelbulle", niceyaml.AtPath(paths.Current().Index(i).Child("available")))
	default:
		return nil
	}
}

// label names the kind of a decode problem.
func label(err error) string {
	// Every labeled problem also matches ErrDecode, so that case comes last.
	switch {
	case errors.Is(err, niceyaml.ErrWrongKind):
		return "wrong kind"
	case errors.Is(err, niceyaml.ErrOverflow):
		return "number out of range"
	case errors.Is(err, niceyaml.ErrUnknownField):
		return "unknown field"
	case errors.Is(err, niceyaml.ErrDecode):
		return "invalid value"
	default:
		return "other"
	}
}
