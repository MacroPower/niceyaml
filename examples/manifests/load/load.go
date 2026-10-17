package main

import (
	"context"
	"embed"
	"errors"
	"fmt"

	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/manifest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/schema"
)

// The Go types of the kinds are exported because they stand in for the
// config package of an app.

// Metadata names a manifest.
type Metadata struct {
	Name string `yaml:"name"`
}

// Cafe describes the cafe.
type Cafe struct {
	Metadata Metadata `yaml:"metadata"`
	Spec     struct {
		Title    string `yaml:"title"`
		Featured string `yaml:"featured"`
		Seats    int    `yaml:"seats"`
	} `yaml:"spec"`
}

// MenuItem is one dish on the menu.
type MenuItem struct {
	Metadata Metadata `yaml:"metadata"`
	Spec     struct {
		Dietary []string `yaml:"dietary"`
		Price   int      `yaml:"price"`
	} `yaml:"spec"`
}

// Hours holds the opening hours.
type Hours struct {
	Metadata Metadata `yaml:"metadata"`
	Spec     struct {
		Open  string `yaml:"open"`
		Close string `yaml:"close"`
	} `yaml:"spec"`
}

var (
	//go:embed config broken menu-item.v1.json
	files embed.FS

	// The registry holds the kinds of the app. A load routes each document
	// by its apiVersion and kind fields to the Go type of its kind. The
	// decode rejects each key that the Go type does not declare, apart from
	// apiVersion and kind.
	registry = manifest.MustNewRegistry(
		// The messages of a load name the app, such as the hint under an
		// unknown kind.
		manifest.WithAppName("cafe"),
		// Each manifest takes its name from this path, and two manifests of
		// one kind must not share a name.
		manifest.WithNameField(paths.Doc().Child("metadata", "name")),
		manifest.WithKinds(
			// A load fails without a Cafe, and fails with two.
			manifest.NewKind[Cafe]("Cafe",
				manifest.Required(),
				manifest.Singleton(),
				manifest.Version("cafe.jacobcolvin.com/v1"),
			),
			manifest.NewKind[MenuItem]("MenuItem",
				manifest.Version("cafe.jacobcolvin.com/v1",
					manifest.Schema(schema.FileFS(files, "menu-item.v1.json")),
				),
			),
			// A kind lists its versions oldest first, and its Go type reads
			// the last. A rewrite moves each document off the deprecated
			// version to the last version.
			manifest.NewKind[Hours]("Hours",
				manifest.Version("cafe.jacobcolvin.com/v1alpha1",
					manifest.Deprecated("cafe 2.0 removes cafe.jacobcolvin.com/v1alpha1"),
				),
				manifest.Version("cafe.jacobcolvin.com/v1"),
			),
		),
		manifest.WithCheck(featuredOnMenu),
	)
)

func main() {
	ctx := context.Background()
	p := printer.New()

	// FS reads the *.yaml and *.yml files of a directory in an fs.FS in path
	// order. A CLI passes the directory that its flag names to Path instead.
	config := manifest.FS(files, "config")

	fmt.Println("\nLoad a directory of manifests:")

	set, err := registry.Load(ctx, config)
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	// One returns the manifest of a Singleton kind, and the set that Load
	// returns holds the Cafe that the registry requires. Get finds a manifest
	// by its name, and featuredOnMenu checks that the featured item exists.
	cafe, _ := set.One[Cafe]()
	featured, _ := set.Get[MenuItem](cafe.Value.Spec.Featured)
	fmt.Printf("%s seats %d and features %s at %d kr.\n",
		cafe.Value.Spec.Title, cafe.Value.Spec.Seats, featured.Name, featured.Value.Spec.Price)

	// All yields the manifests of a kind in load order.
	for item := range set.All[MenuItem]() {
		fmt.Printf("%-12s %3d kr  %v\n", item.Name, item.Value.Spec.Price, item.Value.Spec.Dietary)
	}

	fmt.Println("\nPrint the warnings of a load that passes:")

	// The hours declare a deprecated version, so the load passes with a
	// warning. Warnings returns every warning as one error, and PrintError
	// renders it like any other.
	lipgloss.Println(p.PrintError(set.Warnings()))

	fmt.Println("\nFail on a warning in strict mode:")

	// WithWarningsAsErrors turns each warning into an error, so a strict
	// load in CI fails on the deprecated file before a release removes the
	// version. A report of one problem shows that problem alone, without a
	// heading that counts it.
	strict := manifest.WithWarningsAsErrors(true)
	_, err = registry.Load(ctx, config, strict)
	lipgloss.Println(p.PrintError(err, niceyaml.WithContextLines(0)))

	fmt.Println("\nAccept a deprecation with a filter:")

	// A team that accepts the deprecated version of the hours for a while
	// drops the warning about it with a filter. The filter matches the
	// warning about a deprecated version by its sentinel, its kind, and its
	// version. Every other warning about the hours stays, such as one about
	// a deprecated field. The filter runs before strict mode, so the strict
	// load passes and reads the hours from v1alpha1.
	acceptV1alpha1 := manifest.WithWarningFilter(func(w *manifest.Warning) bool {
		accepted := errors.Is(w, manifest.ErrDeprecatedVersion) && w.Kind == "Hours" &&
			w.Version == "cafe.jacobcolvin.com/v1alpha1"

		return !accepted
	})

	accepted, err := registry.Load(ctx, config, strict, acceptV1alpha1)
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	for hours := range accepted.All[Hours]() {
		fmt.Printf("%s hours from %s: %s to %s\n",
			hours.Name, hours.Version, hours.Value.Spec.Open, hours.Value.Spec.Close)
	}

	fmt.Println("\nReport every problem of a broken directory:")

	// A load checks every document before it returns, so one error holds
	// every problem in load order.
	broken := manifest.FS(files, "broken")
	_, err = registry.Load(ctx, broken)
	lipgloss.Println(p.PrintError(err, niceyaml.WithContextLines(0)))

	// The unknown kind, the duplicate name, and the missing Cafe each match a
	// manifest sentinel, and the schema violation matches
	// schema.ErrViolation, so a program branches on the cause without reading
	// the message. The *schema.Violation that errors.As finds names the
	// keyword that the value breaks.
	fmt.Println("Matches ErrUnknownKind:", errors.Is(err, manifest.ErrUnknownKind))
	fmt.Println("Matches ErrDuplicateName:", errors.Is(err, manifest.ErrDuplicateName))
	fmt.Println("Matches ErrMissingManifest:", errors.Is(err, manifest.ErrMissingManifest))
	fmt.Println("Matches schema.ErrViolation:", errors.Is(err, schema.ErrViolation))

	if v, ok := errors.AsType[*schema.Violation](err); ok {
		fmt.Println("Breaks the schema keyword:", v.Keyword)
	}

	fmt.Println("\nKeep the manifests that pass:")

	// Check runs the same checks and returns the manifests that pass beside
	// the problems, so an editor shows both. Checked.Set leaves out each
	// manifest whose document holds an error. The duplicate name binds in the
	// second kanelbulle, so the first stays.
	checked, err := registry.Check(ctx, broken)
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	for o := range checked.Set().Objects() {
		fmt.Printf("%s %q from %s\n", o.Kind, o.Name, o.Node.Source().Name())
	}

	fmt.Println("\nCheck a reference across manifests:")

	// A load calls featuredOnMenu only on a set whose documents and other
	// checks pass, so the broken directory never reaches it. Excluding the
	// menu leaves a set that passes those checks, but the featured item names
	// a MenuItem that the set does not hold. The filter keeps the warning
	// about the deprecated hours out of the report.
	withoutMenu := manifest.FS(files, "config", manifest.WithExclude("menu.yaml"))
	_, err = registry.Load(ctx, withoutMenu, acceptV1alpha1)
	lipgloss.Println(p.PrintError(err))
}

// featuredOnMenu fails a set whose cafe features an item that the menu does
// not hold. The Required and Singleton checks pass before it runs, so the
// set holds one Cafe.
func featuredOnMenu(_ context.Context, set *manifest.Set) error {
	cafe, _ := set.One[Cafe]()
	if _, ok := set.Get[MenuItem](cafe.Value.Spec.Featured); ok {
		return nil
	}

	// Bind places the error on the line of the cafe file.
	return cafe.Bind(niceyaml.NewError(
		fmt.Sprintf("no MenuItem %q", cafe.Value.Spec.Featured),
		niceyaml.AtPath(paths.Doc().Child("spec", "featured")),
	))
}
