package manifest_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/edit"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/manifest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/printer"
)

// menuV1Doc holds the documents of menuDoc at cafe.jacobcolvin.com/v1.
var menuV1Doc = stringtest.LinesLF(
	"# Fika menu, updated each season.",
	"apiVersion: cafe.jacobcolvin.com/v1",
	"kind: MenuItem",
	"metadata:",
	"  name: kanelbulle",
	"spec:",
	"  price: 45 # kr",
	"  tags: [vegan]",
	"---",
	"apiVersion: cafe.jacobcolvin.com/v1",
	"kind: MenuItem",
	"metadata:",
	"  name: semla",
	"spec:",
	"  price: 65",
	"  tags: [seasonal]",
	"  legacyPricing: true",
)

// planOf plans the migration of files in the directory config of a file
// system, with the registry of opts.
func planOf(t *testing.T, opts []manifest.RegistryOption, files map[string]string,
	load ...manifest.LoadOption,
) *manifest.Plan {
	t.Helper()

	reg := manifest.MustNewRegistry(opts...)

	plan, err := reg.PlanMigration(t.Context(), configFS(files), load...)
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

	// A document whose kind is misspelled, after the documents of a file.
	misspelled := stringtest.LinesLF("---", "apiVersion: cafe.jacobcolvin.com/v1", "kind: Menuitem")

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
			files: exampleFiles,
			headers: []string{
				`config/hours.yaml: Hours "weekday" from cafe.jacobcolvin.com/v1alpha1 to cafe.jacobcolvin.com/v1`,
				`config/menu.yaml: MenuItem "kanelbulle" from cafe.jacobcolvin.com/v1beta1 to cafe.jacobcolvin.com/v1`,
			},
			afters: []string{
				stringtest.LinesLF(
					"apiVersion: cafe.jacobcolvin.com/v1",
					"kind: Hours",
					"metadata:",
					"  name: weekday",
					"spec:",
					`  open: "07:00" # summer hours`,
					`  close: "19:00"`,
				),
				menuV1Doc,
			},
			summary: "2 files to migrate, 1 up to date, nothing written",
		},
		"byte order mark that opens a later document": {
			opts:  cafeOptions(),
			files: map[string]string{"hours.yaml": cafeDoc + "---\n\ufeff" + hoursDoc},
			headers: []string{
				`config/hours.yaml: Hours "weekday" from cafe.jacobcolvin.com/v1alpha1 to cafe.jacobcolvin.com/v1`,
			},
			afters: []string{cafeDoc + stringtest.LinesLF(
				"---",
				"apiVersion: cafe.jacobcolvin.com/v1",
				"kind: Hours",
				"metadata:",
				"  name: weekday",
				"spec:",
				`  open: "07:00" # summer hours`,
				`  close: "19:00"`,
			)},
			summary: "1 file to migrate, nothing written",
		},
		"removal": {
			opts: drinkOptions(false),
			files: map[string]string{
				"drink.yaml": drinkDoc + stringtest.LinesLF(
					"  # Matches the brand palette.",
					"  color: red # or hex",
					"  size: S",
				),
			},
			headers: []string{
				stringtest.JoinLF(
					"config/drink.yaml: Drink from cafe.jacobcolvin.com/v1beta1 to cafe.jacobcolvin.com/v1",
					"  removes $.spec.color: menu colors moved to the theme of the Cafe",
				),
			},
			afters: []string{stringtest.LinesLF(
				"apiVersion: cafe.jacobcolvin.com/v1",
				"kind: Drink",
				"metadata:",
				"  name: kaffe",
				"spec:",
				"  size: small",
			)},
			summary: "1 file to migrate, nothing written",
		},
		"quoted type fields": {
			opts: drinkOptions(false),
			files: map[string]string{
				"drink.yaml": stringtest.LinesLF(
					`apiVersion: "cafe.jacobcolvin.com/v1beta1" # since 2024`,
					"kind: 'Drink'",
					"spec:",
					"  size: S",
				),
			},
			headers: []string{
				"config/drink.yaml: Drink from cafe.jacobcolvin.com/v1beta1 to cafe.jacobcolvin.com/v1",
			},
			afters: []string{stringtest.LinesLF(
				`apiVersion: "cafe.jacobcolvin.com/v1" # since 2024`,
				"kind: 'Drink'",
				"spec:",
				"  size: small",
			)},
			summary: "1 file to migrate, nothing written",
		},
		"deprecations that no step fixes": {
			opts:  deprecationOptions(),
			files: map[string]string{"menu.yaml": deprecatedMenu},
			headers: []string{
				`config/menu.yaml: MenuItem "kanelbulle" from cafe.jacobcolvin.com/v1beta1 to cafe.jacobcolvin.com/v1`,
			},
			afters: []string{
				stringtest.LinesLF(
					"apiVersion: cafe.jacobcolvin.com/v1",
					"kind: MenuItem",
					"metadata:",
					"  name: kanelbulle",
					"spec:",
					"  price: 45",
					"  legacyPricing: true",
					"---",
					"apiVersion: cafe.jacobcolvin.com/v1",
					"kind: MenuItem",
					"metadata:",
					"  name: semla",
					"spec:",
					"  price: 65",
					"  legacyPricing: true",
				),
			},
			summary: "1 file to migrate, nothing written",
			warns:   "config: 2 warnings",
		},
		"step error blocks the file": {
			opts:    drinkOptions(false),
			files:   map[string]string{"drink.yaml": drinkDoc + "  size: XL\n"},
			blocked: []string{"config/drink.yaml"},
			summary: "1 file not migrated, nothing written",
			err:     `config/drink.yaml:6:9: $.spec.size: no cafe.jacobcolvin.com/v1 size for "XL"`,
		},
		"routing failure keeps its document": {
			opts: cafeOptions(),
			files: map[string]string{
				"menu.yaml": menuDoc + misspelled,
			},
			headers: []string{
				stringtest.JoinLF(
					`config/menu.yaml: MenuItem "kanelbulle" from cafe.jacobcolvin.com/v1beta1 to cafe.jacobcolvin.com/v1`,
					"config/menu.yaml: 1 document not migrated",
				),
			},
			afters: []string{
				menuV1Doc + misspelled,
			},
			summary: "1 file to migrate, 1 document not migrated, nothing written",
			err:     `config/menu.yaml:20:7: $.kind: unknown kind "Menuitem"`,
		},
		"up-to-date document beside one that migrates": {
			opts: drinkOptions(false),
			files: map[string]string{
				"drink.yaml": drinkDoc + stringtest.LinesLF(
					"  size: S",
					"---",
					"apiVersion: cafe.jacobcolvin.com/v1",
					"kind: Drink",
				),
			},
			headers: []string{
				"config/drink.yaml: document 1: Drink from cafe.jacobcolvin.com/v1beta1 to cafe.jacobcolvin.com/v1",
			},
			afters: []string{stringtest.LinesLF(
				"apiVersion: cafe.jacobcolvin.com/v1",
				"kind: Drink",
				"metadata:",
				"  name: kaffe",
				"spec:",
				"  size: small",
				"---",
				"apiVersion: cafe.jacobcolvin.com/v1",
				"kind: Drink",
			)},
			summary: "1 file to migrate, nothing written",
		},
		"blocked file beside a document that fails to route": {
			opts: drinkOptions(false),
			files: map[string]string{
				"a.yaml": drinkDoc + "  size: XL\n",
				"b.yaml": drinkDoc + stringtest.LinesLF(
					"  size: S",
					"---",
					"apiVersion: cafe.jacobcolvin.com/v9",
					"kind: Drink",
				),
			},
			headers: []string{
				stringtest.JoinLF(
					"config/b.yaml: document 1: Drink from cafe.jacobcolvin.com/v1beta1 to cafe.jacobcolvin.com/v1",
					"config/b.yaml: 1 document not migrated",
				),
			},
			afters: []string{stringtest.LinesLF(
				"apiVersion: cafe.jacobcolvin.com/v1",
				"kind: Drink",
				"metadata:",
				"  name: kaffe",
				"spec:",
				"  size: small",
				"---",
				"apiVersion: cafe.jacobcolvin.com/v9",
				"kind: Drink",
			)},
			blocked: []string{"config/a.yaml"},
			summary: "1 file to migrate, 1 document not migrated, 1 file not migrated, nothing written",
			err:     "config: 2 errors",
		},
		"up to date": {
			opts:    cafeOptions(),
			files:   map[string]string{"cafe.yaml": cafeDoc},
			summary: "1 file up to date, nothing written",
		},
		"removed version that the kind still lists": {
			opts:    configOptions(manifest.WithRemoved("1", "Config", "app 2.0 removed version 1")),
			files:   map[string]string{"config.yaml": stringtest.LinesLF("version: 1", "port: 80")},
			headers: []string{"config/config.yaml: Config from version 1 to version 2"},
			afters:  []string{stringtest.LinesLF("version: 2", "listen: 80")},
			summary: "1 file to migrate, nothing written",
		},
		"type fields of an unversioned document": {
			opts: adoptOptions(),
			files: map[string]string{"cafe.yaml": stringtest.LinesLF(
				"# Café Yamüll, the main room.",
				"name: yamull",
				"seats: 40",
			)},
			headers: []string{
				`config/cafe.yaml: Cafe "yamull" from the unversioned layout of Cafe to cafe.jacobcolvin.com/v1`,
			},
			afters: []string{stringtest.LinesLF(
				"# Café Yamüll, the main room.",
				"apiVersion: cafe.jacobcolvin.com/v1",
				"kind: Cafe",
				"metadata:",
				"  name: yamull",
				"spec:",
				"  seats: 40",
			)},
			summary: "1 file to migrate, nothing written",
		},
		"documents without names": {
			opts: configOptions(),
			files: map[string]string{"config.yaml": stringtest.LinesLF(
				"version: 2",
				"listen: 80",
				"---",
				"version: 1",
				"port: 80",
			)},
			headers: []string{"config/config.yaml: document 2: Config from version 1 to version 2"},
			afters: []string{stringtest.LinesLF(
				"version: 2",
				"listen: 80",
				"---",
				"version: 2",
				"listen: 80",
			)},
			summary: "1 file to migrate, nothing written",
		},
		"removal by a version newer than the written one in strict mode": {
			opts:    drinkOptions(true),
			files:   map[string]string{"drink.yaml": colorDrinkDoc},
			load:    []manifest.LoadOption{manifest.WithWarningsAsErrors(true)},
			summary: "1 file up to date, nothing written",
			warns:   `config/drink.yaml:6:3: warning: $.spec.color~: cafe.jacobcolvin.com/v1 ignores field "color"`,
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

func TestRegistry_PlanMigration_WarningFilter(t *testing.T) {
	t.Parallel()

	var (
		mu       sync.Mutex
		versions []string
	)

	// The filter gets the version that each document declares once
	// Apply writes it.
	keep := manifest.WithWarningFilter(func(w *manifest.Warning) bool {
		mu.Lock()
		defer mu.Unlock()

		versions = append(versions, w.Kind+" "+w.Version)

		return true
	})

	plan := planOf(t, deprecationOptions(), map[string]string{"menu.yaml": deprecatedMenu}, keep)
	require.Error(t, plan.Warnings())

	want := "MenuItem cafe.jacobcolvin.com/v1"
	assert.Equal(t, []string{want, want}, versions)
}

func TestRegistry_PlanMigration_Limits(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		opts  []manifest.RegistryOption
		limit niceyaml.SourceOption
		want  string
	}{
		// The rename makes the 20 bytes of the text two bytes longer.
		"new text past the byte limit": {
			opts:  configOptions(),
			limit: niceyaml.WithByteLimit(21),
			want:  "config/config.yaml: cannot rewrite a file whose new text holds more than 21 bytes",
		},
		"new text past the depth limit": {
			opts: []manifest.RegistryOption{
				manifest.WithVersionOnly(paths.Doc().Child("version")),
				manifest.WithKinds(manifest.NewKind[map[string]any]("Config",
					manifest.Version("1"),
					manifest.Version("2",
						manifest.Migrate(paths.Doc().Child("port"), func(ctx context.Context, n *edit.Node) error {
							return n.Set(ctx, map[string]any{"listen": map[string]any{"port": 80}})
						}),
					),
				)),
			},
			limit: niceyaml.WithDepthLimit(2),
			want:  "config/config.yaml: cannot rewrite a document whose new text nests more than 2 levels",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			in := configFS(map[string]string{"config.yaml": stringtest.LinesLF("version: 1", "port: 80")},
				manifest.WithSourceOptions(tc.limit))

			plan, err := manifest.MustNewRegistry(tc.opts...).PlanMigration(t.Context(), in)
			require.NoError(t, err)
			assert.Empty(t, plan.Files())
			assert.Equal(t, []string{"config/config.yaml"}, plan.Blocked())
			require.ErrorIs(t, plan.Err(), niceyaml.ErrLimit)
			assert.False(t, niceyaml.IsInvalid(plan.Err()))
			assert.Equal(t, tc.want, plan.Err().Error())
		})
	}
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

	p := yamltest.NewPlainPrinter(printer.WithGutter(printer.DefaultGutter))
	files := map[string]string{"hours.yaml": hoursDoc}

	plan := planOf(t, cafeOptions(), files)
	require.Len(t, plan.Files(), 1)

	assert.Equal(t, stringtest.JoinLF(
		"      @@ -1,7 +1,7 @@",
		"   1 -apiVersion: cafe.jacobcolvin.com/v1alpha1",
		"   1 +apiVersion: cafe.jacobcolvin.com/v1",
		"   2  kind: Hours",
		"   3  metadata:",
		"   4    name: weekday",
		"   5  spec:",
		`   6 -  opens: "07:00" # summer hours`,
		`   7 -  closes: "19:00"`,
		`   6 +  open: "07:00" # summer hours`,
		`   7 +  close: "19:00"`,
	), stringtest.TrimLineEnds(p.Print(plan.Files()[0].Diff(3))))

	hidden, err := newCafeRegistry(t).PlanMigration(t.Context(),
		configFS(files, manifest.WithSourceOptions(niceyaml.WithExcerpts(false))))
	require.NoError(t, err)
	require.Len(t, hidden.Files(), 1)
	assert.Equal(t, "   1  3 lines removed, 3 added", stringtest.TrimLineEnds(p.Print(hidden.Files()[0].Diff(3))))
}

func TestPlanFile_Removals(t *testing.T) {
	t.Parallel()

	plan := planOf(t, drinkOptions(false), map[string]string{"drink.yaml": colorDrinkDoc},
		manifest.WithWarningsAsErrors(true), manifest.WithWarningFilter(func(*manifest.Warning) bool { return false }))
	require.Len(t, plan.Files(), 1)

	removals := plan.Files()[0].Removals()
	require.ErrorIs(t, removals, manifest.ErrRemoved)
	assert.Equal(t, stringtest.JoinLF(
		`config/drink.yaml:6:3: warning: $.spec.color~: cafe.jacobcolvin.com/v1 ignores field "color"`,
		"`-- menu colors moved to the theme of the Cafe",
		"",
		"   4 |   name: kaffe",
		"   5 | spec:",
		"   6 |   color: red",
		"     |   ^^^^^",
	), niceyaml.FormatError(removals))

	var w *manifest.Warning

	require.ErrorAs(t, removals, &w)
	assert.Equal(t, "Drink", w.Kind)

	assert.NoError(t, planOf(t, cafeOptions(), map[string]string{"hours.yaml": hoursDoc}).Files()[0].Removals())
}

func TestPlanFile_After(t *testing.T) {
	t.Parallel()

	reg := manifest.MustNewRegistry(drinkOptions(false)...)
	files := map[string]string{"drink.yaml": drinkDoc + stringtest.LinesLF("  color: red", "  size: S")}

	tcs := map[string]struct {
		// In returns the input of files and the name that messages give the
		// file.
		in func(t *testing.T) (manifest.Input, string)
		// Hint reports whether the removal carries the migrate hint.
		hint bool
	}{
		"file of a file system": {
			in: func(*testing.T) (manifest.Input, string) {
				return configFS(files), "config/drink.yaml"
			},
		},
		"file of a directory": {
			in: func(t *testing.T) (manifest.Input, string) {
				t.Helper()

				config := filepath.Join(t.TempDir(), "config")
				yamltest.WriteFiles(t, config, files)

				return manifest.Path(config), filepath.Join(config, "drink.yaml")
			},
			hint: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			in, file := tc.in(t)

			plan, err := reg.PlanMigration(t.Context(), in)
			require.NoError(t, err)
			require.Len(t, plan.Files(), 1)

			f := plan.Files()[0]
			assert.Equal(t, file, f.Source().Name())
			assert.Equal(t, stringtest.LinesLF(
				"apiVersion: cafe.jacobcolvin.com/v1",
				"kind: Drink",
				"metadata:",
				"  name: kaffe",
				"spec:",
				"  size: small",
			), string(f.After()))

			removals := niceyaml.FormatError(f.Removals())
			if tc.hint {
				assert.Contains(t, removals, migrateHint)
			} else {
				assert.NotContains(t, removals, migrateHint)
			}
		})
	}
}

// configOptions returns the options of a registry of one kind, Config,
// whose documents write a version field alone, followed by extra. Version
// 2 renames port to listen.
func configOptions(extra ...manifest.RegistryOption) []manifest.RegistryOption {
	return append([]manifest.RegistryOption{
		manifest.WithVersionOnly(paths.Doc().Child("version")),
		manifest.WithKinds(manifest.NewKind[map[string]any]("Config",
			manifest.Version("1"),
			manifest.Version("2", manifest.Migrate(paths.Doc().Child("port"), rename("listen"))),
		)),
	}, extra...)
}
