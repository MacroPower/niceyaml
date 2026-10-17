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

// files holds the documents of the café and the schemas they use.
//
//go:embed *.yaml menus schemas
var files embed.FS

func main() {
	ctx := context.Background()
	p := printer.New()

	// A registry tries its resolvers in order, and the first that does not
	// decline names the schema. A directive states what the author of the
	// file intends, so it comes before the conventions of the program.
	reg := schema.NewRegistry(schema.WithResolvers(
		schema.Directive(),
		// A relative pattern matches the relative path of a document from a
		// file system, and the braces accept either extension.
		schema.When(matcher.MustFilePath("menus/*.{yaml,yml}"),
			schema.FileFS(files, "schemas/menu.schema.yaml")),
		schema.ResolverFunc(byKind),
	))

	fmt.Println("\nName a subschema in a directive:")

	// The directive names a YAML schema by a path relative to the file, and
	// its fragment picks the hours definition from that schema.
	validate(ctx, p, reg, "hours.yaml")

	fmt.Println("\nRoute a file by its path:")

	// The path pattern matches menus/fika.yaml, so the menu schema applies.
	// That schema takes its price from cafe.schema.yaml through a $ref. The
	// registry resolves the $ref against the path of the menu schema and
	// reads cafe.schema.yaml from the same file system. The price definition
	// there accepts only whole kr, so the 64.50 of the Semla fails.
	validate(ctx, p, reg, "menus/fika.yaml")

	fmt.Println("\nTurn validation off with a directive:")

	// The path pattern matches this file too, but the directive comes first.
	// The "none" directive passes the document, so the menu schema never
	// runs.
	validate(ctx, p, reg, "menus/draft.yaml")

	fmt.Println("\nRoute a file by its kind:")

	// Neither the directive nor the path pattern applies, so the lookup
	// reaches byKind.
	validate(ctx, p, reg, "rota.yaml")

	fmt.Println("\nReport the reasons that the resolvers gave:")

	// The error lists the reason that each resolver gave, in order. The
	// When guard gives the condition of its matcher, and byKind wraps
	// ErrNoMatch with a reason of its own.
	validate(ctx, p, reg, "recipe.yaml")
}

// validate opens one embedded file and validates each of its documents
// against the schema that reg resolves for that document.
func validate(ctx context.Context, p *printer.Printer, reg *schema.Registry, name string) {
	// NewSourceFromFS gives each document the path of its file in the
	// embedded file system. The path pattern matches that path. A directive
	// names a schema relative to that path, and the registry reads the
	// schema from the same file system.
	source, err := niceyaml.NewSourceFromFS(files, name)
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	err = source.ValidateDocuments(ctx, reg)
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	fmt.Printf("%s: valid\n", name)
}

// byKind names the schema that the kind of a document picks, so
// kind: Rota names schemas/rota.schema.yaml.
func byKind(ctx context.Context, doc *niceyaml.Node) (schema.Ref, error) {
	var kind string

	found, err := doc.DecodeIfPresent(ctx, paths.Doc().Child("kind"), &kind)
	if err != nil {
		// An error that does not wrap ErrNoMatch ends the lookup, and the
		// resolvers after this one do not run.
		return schema.Ref{}, fmt.Errorf("read kind: %w", err)
	}

	// A decline wraps ErrNoMatch first, so the registry tries the next
	// resolver and lists the reason that follows the sentinel.
	if !found {
		return schema.Ref{}, fmt.Errorf("%w: document has no kind", schema.ErrNoMatch)
	}

	// The document picks the kind, so byKind names a schema only for the
	// kinds that it lists. A file name built from the kind reaches every
	// file in schemas/, and the shared definitions in cafe.schema.yaml
	// accept every document.
	switch kind {
	case "Rota":
		return schema.FileFS(files, "schemas/rota.schema.yaml"), nil
	default:
		return schema.Ref{}, fmt.Errorf("%w: no schema for kind %q", schema.ErrNoMatch, kind)
	}
}
