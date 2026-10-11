package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"charm.land/lipgloss/v2"

	_ "embed"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/encoder"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/printer"
)

var (
	//go:embed base.yaml
	exampleBase string

	//go:embed prod.yaml
	exampleProd string
)

// config is the type the merged document decodes into.
type config struct {
	Terrace *struct {
		Seats int `yaml:"seats"`
	} `yaml:"terrace"`
	Cafe struct {
		Name  string `yaml:"name"`
		Seats int    `yaml:"seats"`
	} `yaml:"cafe"`
	Hours struct {
		Open  string `yaml:"open"`
		Close string `yaml:"close"`
	} `yaml:"hours"`
	Wifi struct {
		Network  string `yaml:"network"`
		Password string `yaml:"password"`
	} `yaml:"wifi"`
	Currency string   `yaml:"currency"`
	Menu     []string `yaml:"menu"`
}

func main() {
	ctx := context.Background()
	p := printer.New()

	// NewSourceFromFile names a layer after its file. These layers come
	// from embedded strings, so WithName names each one.
	base := niceyaml.NewSourceFromString(exampleBase, niceyaml.WithName("base.yaml"))
	prod := niceyaml.NewSourceFromString(exampleProd, niceyaml.WithName("prod.yaml"))

	// Pass the layers lowest first. A mapping merges key by key, a
	// sequence or a scalar replaces what the layer below holds, and a
	// null removes its key.
	merged, err := niceyaml.NewSourceFromLayers(ctx, base, prod)
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	fmt.Println("\nPrint the merged document:")
	lipgloss.Println(p.Print(merged.View()))

	// The merged Source holds one document, as the Source of a file does.
	doc, err := merged.Document()
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	fmt.Println("\nTrace each value to the layer that set it:")

	// The recursive wildcard selects every node below the root.
	nodes, err := doc.Nodes(paths.Doc().RecursiveAll())
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	for _, node := range nodes {
		// A mapping can hold keys from several layers, so trace scalars alone.
		if node.Kind() != niceyaml.NodeScalar {
			continue
		}

		// Origin returns the Node that holds the value in the file of its layer.
		origin, err := node.Origin()
		if err != nil {
			lipgloss.Println(p.PrintError(err))

			return
		}

		fmt.Printf("%-18s %s:%d\n", node.Path(), origin.Source().Name(), origin.Span().Start+1)
	}

	fmt.Println("\nValidate the merged document:")

	// The validator reads the merged document, and its error binds in the
	// layer that set the value.
	rule := niceyaml.WithValidator(niceyaml.ValidatorFunc(closesBy8))
	_, err = merged.Decode[config](ctx, rule)
	lipgloss.Println(p.PrintError(err))

	fmt.Println("\nOverride a value from the environment:")

	late, err := environment(ctx, "23:00")
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	merged, err = niceyaml.NewSourceFromLayers(ctx, base, prod, late)
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	// The error binds in the environment, which shows no excerpt.
	_, err = merged.Decode[config](ctx, rule)
	lipgloss.Println(p.PrintError(err))

	fmt.Println("\nDecode with an optional file:")

	// No such file exists, so NewSourceFromFile returns a nil Source. A nil
	// layer adds nothing, so the Source goes in as it is.
	user, err := niceyaml.NewSourceFromFile("user.yaml")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		lipgloss.Println(p.PrintError(err))

		return
	}

	early, err := environment(ctx, "19:00")
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	// A program that collects its layers holds them in a []Layer, since Go
	// spreads neither a []*Source nor a []*Node into NewSourceFromLayers.
	layers := []niceyaml.Layer{base, prod, user, early}

	merged, err = niceyaml.NewSourceFromLayers(ctx, layers...)
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	// DecodeInto fills a value that already holds defaults, and a default
	// survives where no layer sets its field.
	cfg := config{Currency: "SEK"}

	err = merged.DecodeInto(ctx, &cfg, rule)
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	fmt.Printf("%s seats %d and closes at %s.\n", cfg.Cafe.Name, cfg.Cafe.Seats, cfg.Hours.Close)
	fmt.Printf("Terrace: %t, wifi: %s, currency: %s\n", cfg.Terrace != nil, cfg.Wifi.Network, cfg.Currency)
}

// environment returns a layer of the values the program reads from its
// environment, one of which is a secret.
func environment(ctx context.Context, closing string) (*niceyaml.Source, error) {
	// Encode a map of the keys the environment sets. A struct would write
	// each nil pointer field as null, which removes the value the files set.
	data, err := encoder.Marshal(ctx, map[string]any{
		"hours": map[string]any{"close": closing},
		"wifi":  map[string]any{"password": "kanelbulle"},
	})
	if err != nil {
		return nil, fmt.Errorf("encode environment: %w", err)
	}

	// WithExcerpts(false) keeps the lines of this layer, and the password
	// among them, out of every error.
	env := niceyaml.NewSourceFromBytes(data,
		niceyaml.WithName("environment"), niceyaml.WithExcerpts(false))

	return env, nil
}

// closesBy8 rejects a closing time past 20:00.
func closesBy8(ctx context.Context, n *niceyaml.Node) error {
	closePath := paths.Current().Child("hours", "close")

	node, err := n.At(closePath)
	if err != nil {
		return fmt.Errorf("select closing time: %w", err)
	}

	text, err := node.Decode[string](ctx)
	if err != nil {
		return fmt.Errorf("decode closing time: %w", err)
	}

	closing, err := time.Parse("15:04", text)
	if err != nil {
		// Invalid declares the document at fault for a plain error.
		return niceyaml.Invalid(err, niceyaml.AtPath(closePath))
	}

	// Parse reads a clock time as a time on January 1 of year 0.
	lastClose := time.Date(0, time.January, 1, 20, 0, 0, 0, time.UTC)
	if closing.After(lastClose) {
		return niceyaml.NewError("closing time is past 20:00", niceyaml.AtPath(closePath))
	}

	return nil
}
