package manifest_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/manifest"
	"go.jacobcolvin.com/niceyaml/manifest/migrate"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/printer"
)

// planOf plans the migration of files in the directory config of a file
// system, with the registry of opts.
func planOf(t *testing.T, opts []manifest.RegistryOption, files map[string]string,
	load ...manifest.LoadOption,
) *manifest.Plan {
	t.Helper()

	reg, err := manifest.NewRegistry(opts...)
	require.NoError(t, err)

	plan, err := reg.PlanMigration(t.Context(), manifest.FS(mapFS(files), "config"), load...)
	require.NoError(t, err)

	return plan
}

// headers returns the header of each file of plan.
func headers(plan *manifest.Plan) []string {
	var got []string

	for _, f := range plan.Files() {
		got = append(got, f.Header())
	}

	return got
}

// afters returns the new text of each file of plan.
func afters(plan *manifest.Plan) []string {
	var got []string

	for _, f := range plan.Files() {
		got = append(got, string(f.After()))
	}

	return got
}

func TestRegistry_PlanMigration(t *testing.T) {
	t.Parallel()

	adopt := []manifest.RegistryOption{
		manifest.WithAppName("cafe"),
		manifest.WithNameField(meta.Child("name")),
		manifest.WithKinds(manifest.NewKind[Cafe]("Cafe",
			manifest.Unversioned(),
			manifest.Version("cafe.jacobcolvin.com/v1",
				manifest.Written(),
				manifest.Migrate(paths.Doc().Child("name"), func(_ context.Context, n *migrate.Node) error {
					return n.Move(meta.Child("name"))
				}),
				manifest.Migrate(paths.Doc().Child("seats"), func(_ context.Context, n *migrate.Node) error {
					return n.Move(spec.Child("seats"))
				}),
			),
		)),
	}

	tcs := map[string]struct {
		files   map[string]string
		opts    []manifest.RegistryOption
		load    []manifest.LoadOption
		headers []string
		afters  []string
		blocked []string
		summary string
		err     string
		warns   string
	}{
		"example directory": {
			opts:  cafeOptions(),
			files: map[string]string{"cafe.yaml": cafeDoc, "hours.yaml": hoursDoc, "menu.yaml": menuDoc},
			headers: []string{
				"config/hours.yaml: Hours \"weekday\" from cafe.jacobcolvin.com/v1alpha1 to cafe.jacobcolvin.com/v1",
				"config/menu.yaml: MenuItem \"kanelbulle\" from cafe.jacobcolvin.com/v1beta1 to cafe.jacobcolvin.com/v1",
			},
			afters: []string{
				"apiVersion: cafe.jacobcolvin.com/v1\nkind: Hours\nmetadata:\n  name: weekday\n" +
					"spec:\n  open: \"07:00\" # summer hours\n  close: \"19:00\"\n",
				"# Fika menu, updated each season.\napiVersion: cafe.jacobcolvin.com/v1\nkind: MenuItem\n" +
					"metadata:\n  name: kanelbulle\nspec:\n  price: 45 # kr\n  tags: [vegan]\n---\n" +
					"apiVersion: cafe.jacobcolvin.com/v1\nkind: MenuItem\nmetadata:\n  name: semla\nspec:\n" +
					"  price: 65\n  tags: [seasonal]\n  legacyPricing: true\n",
			},
			summary: "2 files to migrate, 1 up to date, nothing written",
		},
		"byte order mark that opens a later document": {
			opts:  cafeOptions(),
			files: map[string]string{"hours.yaml": cafeDoc + "---\n\ufeff" + hoursDoc},
			headers: []string{
				`config/hours.yaml: Hours "weekday" from cafe.jacobcolvin.com/v1alpha1 to cafe.jacobcolvin.com/v1`,
			},
			afters: []string{cafeDoc + "---\napiVersion: cafe.jacobcolvin.com/v1\nkind: Hours\nmetadata:\n" +
				"  name: weekday\nspec:\n  open: \"07:00\" # summer hours\n  close: \"19:00\"\n"},
			summary: "1 file to migrate, nothing written",
		},
		"removal": {
			opts: drinkOptions(true),
			files: map[string]string{
				"drink.yaml": drinkDoc + "  # Matches the brand palette.\n  color: red # or hex\n  size: S\n",
			},
			headers: []string{
				"config/drink.yaml: Drink from cafe.jacobcolvin.com/v1beta1 to cafe.jacobcolvin.com/v1\n" +
					"  removes $.spec.color: menu colors moved to the theme of the Cafe",
			},
			afters: []string{"apiVersion: cafe.jacobcolvin.com/v1\nkind: Drink\nmetadata:\n  name: kaffe\n" +
				"spec:\n  size: small\n"},
			summary: "1 file to migrate, nothing written",
		},
		"step error blocks the file": {
			opts:    drinkOptions(true),
			files:   map[string]string{"drink.yaml": drinkDoc + "  size: XL\n"},
			blocked: []string{"config/drink.yaml"},
			summary: "1 file not migrated, nothing written",
			err:     "config/drink.yaml:6:9: $.spec.size: no cafe.jacobcolvin.com/v1 size for \"XL\"",
		},
		"routing failure keeps its document": {
			opts: cafeOptions(),
			files: map[string]string{
				"menu.yaml": menuDoc + "---\napiVersion: cafe.jacobcolvin.com/v1\nkind: Menuitem\n",
			},
			headers: []string{
				"config/menu.yaml: MenuItem \"kanelbulle\" from cafe.jacobcolvin.com/v1beta1 to cafe.jacobcolvin.com/v1\n" +
					"config/menu.yaml: 1 document not migrated",
			},
			afters: []string{
				"# Fika menu, updated each season.\napiVersion: cafe.jacobcolvin.com/v1\nkind: MenuItem\n" +
					"metadata:\n  name: kanelbulle\nspec:\n  price: 45 # kr\n  tags: [vegan]\n---\n" +
					"apiVersion: cafe.jacobcolvin.com/v1\nkind: MenuItem\nmetadata:\n  name: semla\nspec:\n" +
					"  price: 65\n  tags: [seasonal]\n  legacyPricing: true\n" +
					"---\napiVersion: cafe.jacobcolvin.com/v1\nkind: Menuitem\n",
			},
			summary: "1 file to migrate, 1 document not migrated, nothing written",
			err:     "config/menu.yaml:20:7: $.kind: unknown kind \"Menuitem\"",
		},
		"up to date": {
			opts:    cafeOptions(),
			files:   map[string]string{"cafe.yaml": cafeDoc},
			summary: "1 file up to date, nothing written",
		},
		"removed version that the kind still lists": {
			opts:    configOptions(manifest.WithRemoved("1", "Config", "app 2.0 removed version 1")),
			files:   map[string]string{"config.yaml": "version: 1\nport: 80\n"},
			headers: []string{"config/config.yaml: Config from version 1 to version 2"},
			afters:  []string{"version: 2\nlisten: 80\n"},
			summary: "1 file to migrate, nothing written",
		},
		"type fields of an unversioned document": {
			opts:  adopt,
			files: map[string]string{"cafe.yaml": "# Café Yamüll, the main room.\nname: yamull\nseats: 40\n"},
			headers: []string{
				"config/cafe.yaml: Cafe \"yamull\" from the unversioned layout of Cafe to cafe.jacobcolvin.com/v1",
			},
			afters: []string{"# Café Yamüll, the main room.\napiVersion: cafe.jacobcolvin.com/v1\nkind: Cafe\n" +
				"metadata:\n  name: yamull\nspec:\n  seats: 40\n"},
			summary: "1 file to migrate, nothing written",
		},
		"documents without names": {
			opts:    configOptions(),
			files:   map[string]string{"config.yaml": "version: 2\nlisten: 80\n---\nversion: 1\nport: 80\n"},
			headers: []string{"config/config.yaml: document 2: Config from version 1 to version 2"},
			afters:  []string{"version: 2\nlisten: 80\n---\nversion: 2\nlisten: 80\n"},
			summary: "1 file to migrate, nothing written",
		},
		"removal by a version newer than the written one in strict mode": {
			opts:    drinkOptions(false),
			files:   map[string]string{"drink.yaml": drinkDoc + "  color: red\n"},
			load:    []manifest.LoadOption{manifest.WithWarningsAsErrors(true)},
			summary: "1 file up to date, nothing written",
			warns:   "config/drink.yaml:6:3: warning: $.spec.color~: cafe.jacobcolvin.com/v1 ignores field \"color\"",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			plan := planOf(t, tc.opts, tc.files, tc.load...)

			assert.Equal(t, tc.headers, headers(plan))
			assert.Equal(t, tc.afters, afters(plan))
			assert.Equal(t, tc.blocked, plan.Blocked())
			assert.Equal(t, tc.summary, plan.Summary())

			if tc.err != "" {
				require.Error(t, plan.Err())
				assert.Equal(t, tc.err, firstLine(plan.Err().Error()))
				require.NoError(t, plan.Warnings())
			} else {
				require.NoError(t, plan.Err())
			}

			if tc.warns != "" {
				require.Error(t, plan.Warnings())
				assert.Equal(t, tc.warns, firstLine(plan.Warnings().Error()))
			} else {
				require.NoError(t, plan.Warnings())
			}
		})
	}
}

func TestRegistry_PlanMigration_Limits(t *testing.T) {
	t.Parallel()

	reg, err := manifest.NewRegistry(configOptions()...)
	require.NoError(t, err)

	// The rename makes the text two bytes longer.
	text := "version: 1\nport: 80\n"

	in := manifest.FS(mapFS(map[string]string{"config.yaml": text}), "config",
		manifest.WithSourceOptions(niceyaml.WithByteLimit(len(text)+1)))

	plan, err := reg.PlanMigration(t.Context(), in)
	require.NoError(t, err)
	assert.Empty(t, plan.Files())
	assert.Equal(t, []string{"config/config.yaml"}, plan.Blocked())
	require.ErrorIs(t, plan.Err(), niceyaml.ErrLimit)
	assert.False(t, niceyaml.IsInvalid(plan.Err()))
	assert.Equal(t, fmt.Sprintf("config/config.yaml: cannot rewrite a file whose new text holds more than %d bytes",
		len(text)+1), plan.Err().Error())
}

func TestRegistry_PlanMigration_DepthLimit(t *testing.T) {
	t.Parallel()

	reg, err := manifest.NewRegistry(
		manifest.WithVersionOnly(paths.Doc().Child("version")),
		manifest.WithKinds(manifest.NewKind[map[string]any]("Config",
			manifest.Version("1"),
			manifest.Version("2",
				manifest.Written(),
				manifest.Migrate(paths.Doc().Child("port"), func(ctx context.Context, n *migrate.Node) error {
					return n.Set(ctx, map[string]any{"listen": map[string]any{"port": 80}})
				}),
			),
		)),
	)
	require.NoError(t, err)

	in := manifest.FS(mapFS(map[string]string{"config.yaml": "version: 1\nport: 80\n"}), "config",
		manifest.WithSourceOptions(niceyaml.WithDepthLimit(2)))

	plan, err := reg.PlanMigration(t.Context(), in)
	require.NoError(t, err)
	assert.Equal(t, []string{"config/config.yaml"}, plan.Blocked())
	require.ErrorIs(t, plan.Err(), niceyaml.ErrLimit)
	assert.Equal(t, "config/config.yaml: cannot rewrite a document whose new text nests more than 2 levels",
		plan.Err().Error())
}

func TestPlan_StepCalls(t *testing.T) {
	t.Parallel()

	plan := planOf(t, cafeOptions(), map[string]string{"hours.yaml": hoursDoc})

	assert.Equal(t, map[manifest.StepID]int{
		{Kind: "Hours", Version: "cafe.jacobcolvin.com/v1", Step: 1}:    1,
		{Kind: "Hours", Version: "cafe.jacobcolvin.com/v1", Step: 2}:    1,
		{Kind: "MenuItem", Version: "cafe.jacobcolvin.com/v1", Step: 1}: 0,
		{Kind: "MenuItem", Version: "cafe.jacobcolvin.com/v1", Step: 2}: 0,
	}, plan.StepCalls())
}

func TestPlan_Sources(t *testing.T) {
	t.Parallel()

	plan := planOf(t, cafeOptions(), map[string]string{"cafe.yaml": cafeDoc, "hours.yaml": hoursDoc})

	var names []string

	for _, src := range plan.Sources() {
		names = append(names, src.Name())
	}

	assert.Equal(t, []string{"config/cafe.yaml", "config/hours.yaml"}, names)
}

func TestPlanFile_Diff(t *testing.T) {
	t.Parallel()

	reg := newCafeRegistry(t)
	p := yamltest.NewPlainPrinter(printer.WithGutter(printer.DefaultGutter))

	plan, err := reg.PlanMigration(t.Context(), manifest.FS(mapFS(map[string]string{"hours.yaml": hoursDoc}), "config"))
	require.NoError(t, err)
	require.Len(t, plan.Files(), 1)

	assert.Equal(t, ""+
		"      @@ -1,7 +1,7 @@\n"+
		"   1 -apiVersion: cafe.jacobcolvin.com/v1alpha1\n"+
		"   1 +apiVersion: cafe.jacobcolvin.com/v1\n"+
		"   2  kind: Hours\n"+
		"   3  metadata:\n"+
		"   4    name: weekday\n"+
		"   5  spec:\n"+
		"   6 -  opens: \"07:00\" # summer hours\n"+
		"   7 -  closes: \"19:00\"\n"+
		"   6 +  open: \"07:00\" # summer hours\n"+
		"   7 +  close: \"19:00\"", trimLines(p.Print(plan.Files()[0].Diff(3))))

	fsys := mapFS(map[string]string{"hours.yaml": hoursDoc})

	hidden, err := reg.PlanMigration(t.Context(),
		manifest.FS(fsys, "config", manifest.WithSourceOptions(niceyaml.WithExcerpts(false))))
	require.NoError(t, err)
	require.Len(t, hidden.Files(), 1)
	assert.Equal(t, "   1  3 lines removed, 3 added", trimLines(p.Print(hidden.Files()[0].Diff(3))))
}

func TestPlanFile_Removals(t *testing.T) {
	t.Parallel()

	plan := planOf(t, drinkOptions(true), map[string]string{"drink.yaml": drinkDoc + "  color: red\n"},
		manifest.WithWarningsAsErrors(true), manifest.WithWarningFilter(func(*manifest.Warning) bool { return false }))
	require.Len(t, plan.Files(), 1)

	removals := plan.Files()[0].Removals()
	require.ErrorIs(t, removals, manifest.ErrRemoved)
	assert.Equal(t, ""+
		"config/drink.yaml:6:3: warning: $.spec.color~: cafe.jacobcolvin.com/v1 ignores field \"color\"\n"+
		"`-- menu colors moved to the theme of the Cafe\n"+
		"\n"+
		"   4 |   name: kaffe\n"+
		"   5 | spec:\n"+
		"   6 |   color: red\n"+
		"     |   ^^^^^", niceyaml.FormatError(removals))

	var w *manifest.Warning

	require.ErrorAs(t, removals, &w)
	assert.Equal(t, "Drink", w.Kind)

	assert.NoError(t, planOf(t, cafeOptions(), map[string]string{"hours.yaml": hoursDoc}).Files()[0].Removals())
}

// configOptions returns the options of a registry of one kind, Config,
// whose documents write a version field alone, followed by extra. Version
// 2 renames port to listen.
func configOptions(extra ...manifest.RegistryOption) []manifest.RegistryOption {
	return append([]manifest.RegistryOption{
		manifest.WithVersionOnly(paths.Doc().Child("version")),
		manifest.WithKinds(manifest.NewKind[map[string]any]("Config",
			manifest.Version("1"),
			manifest.Version("2", manifest.Written(), manifest.Migrate(paths.Doc().Child("port"), rename("listen"))),
		)),
	}, extra...)
}

// trimLines returns s without the spaces at the end of each line.
func trimLines(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}

	return strings.Join(lines, "\n")
}
