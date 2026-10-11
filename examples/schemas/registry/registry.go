package main

import (
	"context"
	"embed"
	"fmt"

	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/schema"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
)

var (
	//go:embed demo.yaml
	example string

	//go:embed *.v1.json
	schemas embed.FS
)

func main() {
	ctx := context.Background()

	// A directive names its schema beside the document, so the source
	// needs a file path and the file system that path is in.
	source := niceyaml.NewSourceFromString(example,
		niceyaml.WithFilePath("demo.yaml"), niceyaml.WithFS(schemas))

	p := printer.New()

	fmt.Println("\nPrint the stream:")
	lipgloss.Println(p.Print(source.View()))

	// A registry tries its resolvers in order and validates each document
	// against the first schema one of them names.
	resolvers := schema.WithResolvers(
		// A "# yaml-language-server: $schema=" comment comes first.
		schema.Directive(),
		// Any other document routes by its kind.
		schema.When(
			matcher.Content(paths.Doc().Child("kind"), "MenuItem"),
			schema.FileFS(schemas, "menu-item.v1.json"),
		),
	)

	reg := schema.NewRegistry(resolvers)

	fmt.Println("\nValidate each document against its schema:")

	for _, doc := range source.Documents() {
		err := doc.Validate(ctx, reg)
		if err != nil {
			fmt.Println(err)

			continue
		}

		fmt.Printf("%s: document %d: valid\n", source.Name(), doc.DocumentIndex()+1)
	}

	fmt.Println("\nReport every failure of the stream:")

	err := source.ValidateDocuments(ctx, reg)
	if err != nil {
		lipgloss.Println(p.PrintError(err))
	}

	// A registry that requires no schema passes the documents no resolver
	// applies to, so only the violation is left.
	lenient := schema.NewRegistry(resolvers, schema.WithRequireSchema(false))

	fmt.Println("\nAccept documents that match no schema:")

	err = source.ValidateDocuments(ctx, lenient)
	if err != nil {
		lipgloss.Println(p.PrintError(err))
	}
}
