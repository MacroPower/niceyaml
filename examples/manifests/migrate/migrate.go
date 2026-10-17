package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"syscall"

	"charm.land/lipgloss/v2"

	"go.jacobcolvin.com/niceyaml/edit"
	"go.jacobcolvin.com/niceyaml/manifest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/schema"
)

// MenuItem is the Go type of v1, the last version of the kind. It is
// exported because it stands in for the config package of an app. A load
// moves each document of v1beta1 to v1 before it decodes the document, so
// the app reads only one version.
type MenuItem struct {
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Dietary []string `yaml:"dietary"`
		Price   int      `yaml:"price"`
	} `yaml:"spec"`
}

var (
	//go:embed config broken.yaml menu-item.v1.json
	files embed.FS

	spec = paths.Doc().Child("spec")

	// A kind lists its versions oldest first. The steps of each version
	// move a document from the version before it. The author of a new
	// version thus writes steps only for the previous version.
	registry = manifest.MustNewRegistry(
		manifest.WithAppName("cafe"),
		manifest.WithNameField(paths.Doc().Child("metadata", "name")),
		manifest.WithMigrateHint(`run "cafe config migrate" to update this file`),
		manifest.WithKinds(manifest.NewKind[MenuItem]("MenuItem",
			// Documents of v1beta1 write the kind as Dish, its old name.
			manifest.Version("cafe.jacobcolvin.com/v1beta1", manifest.KindName("Dish")),
			// A rewrite writes the last version. An older version that carries
			// manifest.Written keeps rewrites there while users might go back
			// to a release that does not read the last one.
			manifest.Version("cafe.jacobcolvin.com/v1",
				manifest.Schema(schema.FileFS(files, "menu-item.v1.json")),
				manifest.Migrate(spec.Child("diet"), func(_ context.Context, n *edit.Node) error {
					return n.SetKey("dietary")
				}),
				// Each step sees the edits of the steps before it, so
				// dietaryList finds the value under its new key.
				manifest.Migrate(spec.Child("dietary"), dietaryList),
				// A load warns about each value that a step removes, and the
				// reason of the step becomes a detail of the warning.
				manifest.Migrate(spec.Child("color"), func(_ context.Context, n *edit.Node) error {
					return n.Remove(edit.WithReason("the theme sets the menu colors"))
				}),
			),
		)),
	)
)

func main() {
	// Ctrl-C and a closed pipe, such as the one that head leaves when it has
	// read enough, each cancel ctx instead of ending the process. The next
	// call that takes ctx then fails, and the deferred cleanup removes the
	// copy.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGPIPE)
	defer stop()

	p := printer.New()

	// Apply rewrites only the files that a Path input reads from disk, so
	// the program works on a copy of its embedded files. Each message names
	// a file by the path that the input gives. The program thus loads
	// relative paths from inside the copy, and no message names the
	// temporary directory.
	leave, err := enterCopy(files)
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	defer leave()

	fmt.Println("\nLoad files that declare an older version:")

	// The steps run in memory, so the user's file stays as it is, and
	// kanelbulle still declares v1beta1.
	set, err := registry.Load(ctx, manifest.Path("config"))
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	listMenu(set)

	fmt.Println("\nWarn about each value that a step removes:")

	// Warnings returns the warnings of a load that passes. The hint of
	// WithMigrateHint comes last on each warning that a rewrite clears.
	lipgloss.Println(p.PrintError(set.Warnings()))
	fmt.Println("Matches ErrRemoved:", errors.Is(set.Warnings(), manifest.ErrRemoved))

	fmt.Println("\nCheck the result of the steps against the schema:")

	// The schema of v1 checks each document after the steps run, so it
	// rejects the value that dietaryList set from Lactose. The detail of
	// that error names the version of the step.
	_, err = registry.Load(ctx, manifest.Path("broken.yaml"))
	lipgloss.Println(p.PrintError(err))

	fmt.Println("\nPlan the rewrite:")

	// A plan runs the checks of a load and computes the new text of each
	// file, but it does not write a file.
	plan, err := registry.PlanMigration(ctx, manifest.Path("config"))
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	for _, f := range plan.Files() {
		fmt.Println(f.Header())

		// The rewrite writes the type fields of v1 and the changes of the
		// steps. It keeps every other byte, such as the comments and the
		// place of each key.
		lipgloss.Println(p.Print(f.Diff(1)))
	}

	fmt.Println(plan.Summary())

	fmt.Println("\nApply the plan:")

	// Apply continues past each file that it cannot write, such as one that
	// changed after the plan read it. Its error names each such file and
	// each problem that remains in the files.
	res, err := plan.Apply(ctx)
	fmt.Println(res.Summary())

	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	fmt.Println("\nLoad the rewritten files:")

	set, err = registry.Load(ctx, manifest.Path("config"))
	if err != nil {
		lipgloss.Println(p.PrintError(err))

		return
	}

	// Every file declares v1 after the rewrite, so each document is up to
	// date and the load runs no step.
	listMenu(set)
}

// dietaryList turns the single value of v1beta1 into the list of v1. A
// later change to MenuItem must not change what this step writes, so the
// step sets a []string and not a type of the app.
func dietaryList(ctx context.Context, n *edit.Node) error {
	value, err := n.Decode[string](ctx)
	if err != nil {
		return err
	}

	return n.Set(ctx, []string{value})
}

// listMenu prints each item of set with the version that its file declares.
func listMenu(set *manifest.Set) {
	for item := range set.All[MenuItem]() {
		dietary := fmt.Sprint(item.Value.Spec.Dietary)
		fmt.Printf("%-14s %3d kr  %-17s %s up-to-date=%t\n",
			item.Name, item.Value.Spec.Price, dietary, item.Version, item.UpToDate)
	}
}

// enterCopy copies fsys into a new temporary directory and makes that
// directory the working directory. The function that it returns leaves the
// directory before it removes the copy. Windows cannot remove the working
// directory of a process.
func enterCopy(fsys fs.FS) (func(), error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("find working directory: %w", err)
	}

	dir, err := os.MkdirTemp("", "cafe-")
	if err != nil {
		return nil, fmt.Errorf("create directory: %w", err)
	}

	leave := func() {
		err := os.Chdir(wd)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
		}

		os.RemoveAll(dir)
	}

	err = os.CopyFS(dir, fsys)
	if err == nil {
		err = os.Chdir(dir)
	}

	if err != nil {
		leave()

		return nil, fmt.Errorf("enter copy: %w", err)
	}

	return leave, nil
}
