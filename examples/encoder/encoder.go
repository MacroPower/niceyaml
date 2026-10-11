package main

import (
	"context"
	"fmt"

	"charm.land/lipgloss/v2"
	"github.com/goccy/go-yaml"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/encoder"
	"go.jacobcolvin.com/niceyaml/printer"
)

// hours is when the cafe opens and closes.
type hours struct {
	Open  string `yaml:"open"`
	Close string `yaml:"close"`
}

// cafe is the value the example writes as YAML.
type cafe struct {
	Name  string   `yaml:"name"`
	Hours hours    `yaml:"hours"`
	Menu  []string `yaml:"menu"`
}

func main() {
	ctx := context.Background()
	p := printer.New()

	value := cafe{
		Name:  "Café Yamüll",
		Hours: hours{Open: "07:00", Close: "17:00"},
		Menu:  []string{"Kaffe", "Kanelbulle"},
	}

	fmt.Println("\nMarshal a Go value:")

	// Pretty writes the layout that prettier writes. The keys follow the
	// order of the struct fields, and the encoder quotes a string that a
	// parser could read as another type, such as a time.
	data, err := encoder.Marshal(ctx, value, encoder.Pretty())
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	// The bytes are a YAML document, so a Source over them prints as the
	// Source of a file does.
	lipgloss.Println(p.Print(niceyaml.NewSourceFromBytes(data).View()))

	fmt.Println("\nMarshal it with comments:")

	// Each key is a YAML path, and its comments go on the node it selects.
	comments := yaml.CommentMap{
		"$.name":        {yaml.HeadComment(" The name on the sign.")},
		"$.hours.close": {yaml.LineComment(" Fika is over by then.")},
	}

	data, err = encoder.Marshal(ctx, value, encoder.Pretty(), encoder.WithYAMLComments(comments))
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	lipgloss.Println(p.Print(niceyaml.NewSourceFromBytes(data).View()))
}
