package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"charm.land/lipgloss/v2"

	_ "embed"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/escape"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/schema"
)

// clientMenu is a menu that a client sends to the program as JSON. The
// program decodes it with encoding/json, so the check does not have a source
// to place its warnings in.
const clientMenu = `{"name": "Café Yamüll", "currency": "SEK", "fika": [{"item": "Semla", "price": "55 kr"}]}`

var (
	//go:embed demo.yaml
	example string

	// The schema marks a field or a value with "deprecated": true and gives
	// the reason in "deprecationMessage". The generator of
	// go.jacobcolvin.com/x/jsonschema writes both keywords for a field whose
	// doc comment holds a paragraph that starts with "Deprecated:".
	//
	//go:embed menu.v1.json
	menuJSON []byte

	// A deprecated field marks its key, so the path of its warning ends in
	// "~". A deprecated branch of an anyOf warns only for a value that
	// passes that branch. A value that fails every branch is a violation and
	// does not warn.
	menuSchema = schema.MustCompile(menuJSON)
)

func main() {
	ctx := context.Background()
	source := niceyaml.NewSourceFromString(example, niceyaml.WithName("demo.yaml"))
	p := printer.New()

	doc, err := source.Document()
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	fmt.Println("\nReport the deprecations beside the violations:")

	// A deprecation does not fail the check. The schema hands each one as a
	// warning to the collector on the context and returns the violation
	// alone. Report sorts the warnings and the violation into one list. A
	// warning that repeats the message and detail of an earlier one keeps
	// its row and its mark in the excerpt but loses its detail.
	//
	// Kardemummabulle merges Kanelbulle through <<, so the check reaches the
	// vegan key of Kanelbulle at $.fika[0] and at $.fika[2]. Both paths lead
	// to one node, and the check gives each node at most one warning. The
	// report shows that warning under the path that comes first in the
	// document, $.fika[0].vegan~.
	warnings := niceyaml.NewWarnings()
	err = doc.Validate(warnings.Context(ctx), menuSchema)
	report := warnings.Report(err)
	lipgloss.Println(p.PrintError(report))

	fmt.Println("\nList the fix for each deprecation:")

	// Each deprecation matches ErrDeprecated. Problems yields each problem
	// with its details as its children, and the deprecationMessage of the
	// schema is the detail of a deprecation. Problems keeps the detail that
	// the printed report drops from a repeat, so every row names its fix.
	for problem := range niceyaml.Problems(report) {
		if !errors.Is(problem.Err, schema.ErrDeprecated) {
			continue
		}

		pos, _ := problem.Position()
		path, _ := problem.Path()

		// Each problem holds the name of its source as written, and each
		// detail holds text from the schema file. Either can hold a control
		// character, such as a line feed. Control replaces each control
		// character with one visible rune, as in examples/errors, so each fix
		// takes one row.
		name := escape.Control(problem.Name())

		for _, detail := range problem.Children {
			msg := escape.Control(detail.Message())
			fmt.Printf("%s:%d:%d\t%s\t%s\n", name, pos.Line+1, pos.Col+1, path, msg)
		}
	}

	fmt.Println("\nCheck data without a source:")

	var data any

	err = json.Unmarshal([]byte(clientMenu), &data)
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	// ValidateValue hands its deprecations to the collector as Validate
	// does. The data does not have a source, so each warning names its path
	// in the value and does not have a position. A new collector holds the
	// warnings of this check alone.
	warnings = niceyaml.NewWarnings()
	err = menuSchema.ValidateValue(warnings.Context(ctx), data)
	lipgloss.Println(p.PrintError(warnings.Report(err)))
}
