package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/schema"
)

var (
	// The tenant directory holds one upload, and the private directory
	// beside it holds a schema that only the server reads.
	//
	//go:embed server
	server embed.FS

	//go:embed menu.schema.json
	menuJSON []byte

	// The schema of the program checks each menu item.
	menuSchema = schema.MustCompile(menuJSON)
)

func main() {
	// Ctrl-C and a closed pipe each cancel ctx instead of ending the
	// process. The head command, for example, closes the pipe when it has
	// read enough. In both cases the program runs to the end of main, and
	// the deferred cleanup removes the temporary directory.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGPIPE)
	defer stop()

	p := printer.New()

	// An Access confines the reads from disk. A source from WithFS reads
	// the schema files that it names through its own file system, and the
	// Access never sees those reads. A server that keeps each upload in a
	// file system of its own, such as a zip.Reader, confines the upload
	// through that file system instead. This example confines the disk, so
	// it lays out the disk of a server: the upload of a tenant, and a
	// private schema beside it.
	dir, err := os.MkdirTemp("", "cafe-")
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}
	defer os.RemoveAll(dir)

	err = os.CopyFS(dir, server)
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	tenantDir := filepath.Join(dir, "server", "tenant")

	// The registry reads through the Root, so the program keeps the Root
	// open while it uses the registry.
	root, err := os.OpenRoot(tenantDir)
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}
	defer root.Close() //nolint:errcheck // Nothing writes through the Root.

	// The Access allows the files in the tenant directory and the URLs
	// that start with one prefix. The registry that holds it checks every
	// load: the schema that a directive names, each $ref, and each
	// redirect.
	access, err := schema.NewAccess(
		schema.AllowRoot(root),
		schema.AllowURLPrefixes("https://schemas.example.com/"),
	)
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	// The confined registry loads only the schemas that directives name.
	// Its limits bound the number of schemas it keeps and the bytes of
	// each one.
	confined := schema.NewRegistry(
		schema.WithAccess(access),
		schema.WithCacheLimit(1024),
		schema.WithLoadLimit(1<<20),
		schema.WithResolvers(schema.Directive()),
	)

	// The registry of the program asks the confined registry first. That
	// registry declines a document without a directive, so the schema of
	// the program checks it. When the Access refuses the schema that a
	// directive names, the lookup ends with a load error. The schema of
	// the program then does not run.
	//
	// Directives take the place of the schema of the program, so the
	// author of each upload can name a looser schema.
	reg := schema.NewRegistry(schema.WithResolvers(confined, menuSchema))

	// NewSourceFromFile gives each document the absolute path that the
	// Access checks. The name keeps the temporary directory out of the
	// output.
	source, err := niceyaml.NewSourceFromFile(filepath.Join(tenantDir, "menu.yaml"),
		niceyaml.WithName("menu.yaml"))
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	docs := source.Documents()

	fmt.Println("\nValidate each document of the upload:")

	// The first document names the schema of the tenant, which allows a
	// fractional price and a vegan key. The second has no directive, so
	// the schema of the program checks it. The third names a schema
	// outside the tenant directory, and the fourth names a URL outside the
	// prefix of the Access.
	for _, doc := range docs {
		fmt.Printf("\nDocument %d:\n", doc.DocumentIndex()+1)
		report(p, doc, doc.Validate(ctx, reg))
	}

	fmt.Println("\nValidate the third document in an unrestricted registry:")

	// A registry without WithAccess reads what Unrestricted allows, so it
	// reads any file that a directive names. The violation then tells the
	// author the limit that the private schema sets.
	unrestricted := schema.NewRegistry(schema.WithResolvers(schema.Directive()))
	report(p, docs[2], docs[2].Validate(ctx, unrestricted))
}

// report prints the result of a validation the way a server answers the
// author of an upload.
func report(p *printer.Printer, doc *niceyaml.Node, err error) {
	directive := schema.ParseDocumentDirective(doc.Tokens())
	loadFailed := errors.Is(err, schema.ErrLoad) || errors.Is(err, schema.ErrCompile)

	switch {
	case err == nil:
		fmt.Printf("%s: document %d: valid\n", doc.Source().Name(), doc.DocumentIndex()+1)

	// The examples/limits program shows why the switch asks IsInvalid
	// before it tests for ErrLoad and ErrCompile.
	case niceyaml.IsInvalid(err):
		lipgloss.Println(p.PrintError(err))

	// The registry returns a LoadError, which matches ErrLoad or
	// ErrCompile, for a schema that did not load or compile. The schema of
	// the program compiles at startup, so only a directive names a schema
	// that the registry loads. For a file, the text of the error names the
	// path that the registry tried on the server. The answer therefore
	// comes from the sentinels that the error matches, and it binds at the
	// directive.
	case loadFailed && directive != nil:
		answer := doc.Bind(niceyaml.NewError(
			fmt.Sprintf("schema %q: %s", directive.Schema, reason(err)),
			niceyaml.AtPosition(directive.Position),
		))
		lipgloss.Println(p.PrintError(answer))

	default:
		lipgloss.Println(p.PrintError(doc.Bind(niceyaml.NewError("validation unavailable"))))
	}
}

// reason says why the schema of a directive did not load or compile,
// without the path or URL that the registry tried.
func reason(err error) string {
	switch {
	// The registry refuses a URL outside the prefixes before it sends a
	// request, and a path outside the root before it reads anything.
	case errors.Is(err, schema.ErrURLNotAllowed):
		return "URL not allowed"
	case errors.Is(err, schema.ErrFileNotAllowed):
		return "outside the upload"

	// A missing file and a schema that does not compile get the same
	// answer.
	default:
		return "not available"
	}
}
