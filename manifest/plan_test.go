package manifest_test

import (
	"context"
	"path/filepath"
	"strings"
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
	requireReloads(t, reg, plan, nil, load...)

	return plan
}

// requireReloads loads the new text of each file of plan, with the text of
// each other file that the plan read, through reg with in and load, but
// without the checks of the set. A rewrite never writes a document that a
// load then rejects, so the load of a plan without problems passes, and
// each object is up to date. Files of the directory config load as
// [configFS] reads them, so in applies to them.
func requireReloads(t *testing.T, reg *manifest.Registry, plan *manifest.Plan, in []manifest.InputOption,
	load ...manifest.LoadOption,
) {
	t.Helper()

	if plan.Err() != nil || len(plan.Files()) == 0 {
		return
	}

	after := make(map[string]string)
	for _, f := range plan.Files() {
		after[f.Source().Name()] = string(f.After())
	}

	var (
		srcs  []*niceyaml.Source
		files = make(map[string]string)
	)

	for _, src := range plan.Sources() {
		text, ok := after[src.Name()]
		if !ok {
			text = src.Text()
		}

		srcs = append(srcs, niceyaml.NewSourceFromString(text, niceyaml.WithName(src.Name())))

		if name, ok := strings.CutPrefix(src.Name(), "config/"); ok {
			files[name] = text
		}
	}

	input := manifest.Sources(srcs...)
	if len(files) == len(srcs) {
		input = configFS(files, in...)
	}

	set, err := reg.Load(t.Context(), input, append(load, manifest.WithSetChecks(false))...)
	require.NoError(t, err)

	for o := range set.Objects() {
		assert.True(t, o.UpToDate, "%s %q is up to date", o.Kind, o.Name)
	}
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

	// A registry that reads plain documents as Cafe, with the kind field at
	// kind.
	unversioned := func(opts ...manifest.RegistryOption) []manifest.RegistryOption {
		return append(opts, manifest.WithAppName("cafe"), manifest.WithKinds(manifest.NewKind[Cafe]("Cafe",
			manifest.Unversioned(),
			manifest.Version("cafe.jacobcolvin.com/v1"),
		)))
	}

	// A registry that reads plain documents as Cafe, with the version field
	// at meta.version and the kind field at kind.
	metaVersion := []manifest.RegistryOption{
		manifest.WithAppName("cafe"),
		manifest.WithVersionField(paths.MustParse("$.meta.version")),
		manifest.WithKindField(paths.Doc().Child("kind")),
		manifest.WithKinds(manifest.NewKind[map[string]any]("Cafe",
			manifest.Unversioned(),
			manifest.Version("v1"),
			manifest.Version("v2"),
		)),
	}

	// The input of a reference document whose anchor b holds a spec.
	baseReference := []manifest.InputOption{
		manifest.WithSourceOptions(niceyaml.WithReferences(
			niceyaml.NewSourceFromString("base: &b {spec: {seats: 5}}\n"),
		)),
	}

	tcs := map[string]struct {
		files   map[string]string
		opts    []manifest.RegistryOption
		in      []manifest.InputOption
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
		"byte order mark after the header of a later document": {
			// YAML does not allow a mark inside a document.
			opts:    cafeOptions(),
			files:   map[string]string{"hours.yaml": cafeDoc + "---\n\ufeff" + hoursDoc},
			blocked: []string{"config/hours.yaml"},
			summary: "1 file not migrated, nothing written",
			// The error of the mark, and the warning of the old version.
			err: "config: 1 error, 1 warning",
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
		"version field below a scalar": {
			// No mapping can hold the version field, so the plan blocks the
			// file rather than write a file that a load refuses.
			opts:    metaVersion,
			files:   map[string]string{"cafe.yaml": stringtest.LinesLF("meta: 1", "x: 1")},
			blocked: []string{"config/cafe.yaml"},
			summary: "1 file not migrated, nothing written",
			err:     "config/cafe.yaml:1:7: $.meta: v1 sets $.meta.version, but meta holds a scalar",
		},
		"version field below null": {
			opts:    metaVersion,
			files:   map[string]string{"cafe.yaml": stringtest.LinesLF("meta:", "x: 1")},
			blocked: []string{"config/cafe.yaml"},
			summary: "1 file not migrated, nothing written",
			err:     "config/cafe.yaml:1:6: $.meta: v1 sets $.meta.version, but meta holds null",
		},
		"version field below a sequence": {
			opts:    metaVersion,
			files:   map[string]string{"cafe.yaml": stringtest.LinesLF("meta: [1]", "x: 1")},
			blocked: []string{"config/cafe.yaml"},
			summary: "1 file not migrated, nothing written",
			err:     "config/cafe.yaml:1:1: $.meta: v1 sets $.meta.version, but meta holds a sequence",
		},
		"merge value that holds its alias": {
			// The alias lies inside the value of its own anchor, so the plan
			// blocks the file.
			opts:    metaVersion,
			files:   map[string]string{"cafe.yaml": stringtest.LinesLF("meta:", "  <<: &g [*g]")},
			blocked: []string{"config/cafe.yaml"},
			summary: "1 file not migrated, nothing written",
			err: "config/cafe.yaml:2:7: $.meta: cafe does not edit a document with an alias inside the value of " +
				"the anchor that it reads, such as *g",
		},
		"merge of an alias whose value holds the alias": {
			// The alias lies inside the value of its own anchor, so the plan
			// blocks the file.
			opts:    metaVersion,
			files:   map[string]string{"cafe.yaml": stringtest.LinesLF("k2: &g [*g]", "meta:", "  <<: *g")},
			blocked: []string{"config/cafe.yaml"},
			summary: "1 file not migrated, nothing written",
			err: "config/cafe.yaml:1:5: $.k2: cafe does not edit a document with an alias inside the value of " +
				"the anchor that it reads, such as *g",
		},
		"version field in a new mapping": {
			opts:    metaVersion,
			files:   map[string]string{"cafe.yaml": "x: 1\n"},
			headers: []string{`config/cafe.yaml: Cafe from the unversioned layout of Cafe to v2`},
			afters:  []string{stringtest.LinesLF("kind: Cafe", "x: 1", "meta:", "  version: v2")},
			summary: "1 file to migrate, nothing written",
		},
		"version field in an empty mapping": {
			opts:    metaVersion,
			files:   map[string]string{"cafe.yaml": stringtest.LinesLF("meta: {}", "x: 1")},
			headers: []string{`config/cafe.yaml: Cafe from the unversioned layout of Cafe to v2`},
			afters:  []string{stringtest.LinesLF("kind: Cafe", "meta: {version: v2}", "x: 1")},
			summary: "1 file to migrate, nothing written",
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
		// The lookup of a type field goes through a merge key whose alias
		// reads a reference document, so the plan blocks the file.
		"plain document with a merge key that reads a reference document": {
			opts:    unversioned(),
			in:      baseReference,
			files:   map[string]string{"cafe.yaml": stringtest.LinesLF("# Cafe", "<<: *b # base", "", "metadata: {}")},
			blocked: []string{"config/cafe.yaml"},
			summary: "1 file not migrated, nothing written",
			err: "config/cafe.yaml:2:5: $: cafe.jacobcolvin.com/v1 sets $.kind, " +
				"which goes through <<: *b at 2:1, a merge key that reads a reference document, so that document decides which names $ reads",
		},
		"plain flow document with a merge key that reads a reference document": {
			opts: unversioned(),
			in:   baseReference,
			files: map[string]string{
				"a.yaml": "{<<: *b, metadata: {}}\n",
				"b.yaml": "{metadata: {}, <<: *b}\n",
				"c.yaml": "{metadata: {}, <<: *b,}\n",
			},
			blocked: []string{"config/a.yaml", "config/b.yaml", "config/c.yaml"},
			summary: "3 files not migrated, nothing written",
			err:     "config: 3 errors",
		},
		"plain document with a merge key that reads a reference document, with a nested kind field": {
			opts:    unversioned(manifest.WithKindField(meta.Child("kind"))),
			in:      baseReference,
			files:   map[string]string{"cafe.yaml": "<<: *b\n"},
			blocked: []string{"config/cafe.yaml"},
			summary: "1 file not migrated, nothing written",
			err: "config/cafe.yaml:1:5: $: cafe.jacobcolvin.com/v1 sets $.metadata.kind, " +
				"which goes through <<: *b at 1:1, a merge key that reads a reference document, so that document decides which names $ reads",
		},
		"version field with an anchor that an alias reads": {
			// The field keeps its anchor, so the alias reads the new version.
			opts: metaVersion,
			files: map[string]string{
				"cafe.yaml": stringtest.LinesLF("kind: Cafe", "meta:", "  version: &v v1", "x: *v"),
			},
			headers: []string{`config/cafe.yaml: Cafe from v1 to v2`},
			afters:  []string{stringtest.LinesLF("kind: Cafe", "meta:", "  version: &v v2", "x: *v")},
			summary: "1 file to migrate, nothing written",
		},
		"version field with a tag after its anchor": {
			// The tag goes with the old value, the field keeps the anchor, and the
			// new value keeps the quotes of the old one.
			opts: metaVersion,
			files: map[string]string{
				"cafe.yaml": stringtest.LinesLF("kind: Cafe", "meta:", "  version: &v !!str 'v1'", "x: *v"),
			},
			headers: []string{`config/cafe.yaml: Cafe from v1 to v2`},
			afters:  []string{stringtest.LinesLF("kind: Cafe", "meta:", "  version: &v 'v2'", "x: *v")},
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

			reg := manifest.MustNewRegistry(tc.opts...)

			plan, err := reg.PlanMigration(t.Context(), configFS(tc.files, tc.in...), tc.load...)
			require.NoError(t, err)
			requireReloads(t, reg, plan, tc.in, tc.load...)

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

	reg := manifest.MustNewRegistry(deprecationOptions()...)

	plan, err := reg.PlanMigration(t.Context(), configFS(map[string]string{"menu.yaml": deprecatedMenu}), keep)
	require.NoError(t, err)
	require.Error(t, plan.Warnings())

	want := "MenuItem cafe.jacobcolvin.com/v1"
	assert.Equal(t, []string{want, want}, versions)
	requireReloads(t, reg, plan, nil, keep)
}

func TestRegistry_PlanMigration_VersionText(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		// The versions of the kind Config, the oldest first. Unversioned
		// gives the kind the layout without a version field before them.
		versions    []string
		unversioned bool
		text        string
		want        string
	}{
		"float with a zero": {
			versions: []string{"0.9", "1.0"},
			text:     stringtest.LinesLF("version: 0.9", "x: 1"),
			want:     stringtest.LinesLF(`version: "1.0"`, "x: 1"),
		},
		"float with a zero after the point": {
			versions: []string{"1.9", "1.10"},
			text:     "version: 1.9\n",
			want:     "version: \"1.10\"\n",
		},
		"float after a text": {
			versions: []string{"v1", "2.0"},
			text:     "version: v1\n",
			want:     "version: \"2.0\"\n",
		},
		"hex integer": {
			versions: []string{"v1", "0x10"},
			text:     "version: v1\n",
			want:     "version: \"0x10\"\n",
		},
		"null word": {
			versions: []string{"v1", "null"},
			text:     "version: v1\n",
			want:     "version: \"null\"\n",
		},
		"tilde": {
			versions: []string{"v1", "~"},
			text:     "version: v1\n",
			want:     "version: \"~\"\n",
		},
		"integer": {
			// Go-yaml's decoder reads the plain 2 into a string as "2".
			versions: []string{"1", "2"},
			text:     "version: 1\n",
			want:     "version: 2\n",
		},
		"float in a flow mapping": {
			versions: []string{"v1", "2.0"},
			text:     "{version: v1, x: 1}\n",
			want:     "{version: \"2.0\", x: 1}\n",
		},
		"float in single quotes": {
			versions: []string{"v1", "2.0"},
			text:     "version: 'v1'\n",
			want:     "version: '2.0'\n",
		},
		"new float field": {
			versions:    []string{"1.0"},
			unversioned: true,
			text:        "x: 1\n",
			want:        stringtest.LinesLF(`version: "1.0"`, "x: 1"),
		},
		"new float field in a flow mapping": {
			versions:    []string{"1.0"},
			unversioned: true,
			text:        "{x: 1}\n",
			want:        "{version: \"1.0\", x: 1}\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var kopts []manifest.KindOption

			if tc.unversioned {
				kopts = append(kopts, manifest.Unversioned())
			}

			for _, v := range tc.versions {
				kopts = append(kopts, manifest.Version(v))
			}

			reg := manifest.MustNewRegistry(
				manifest.WithVersionOnly(paths.Doc().Child("version")),
				manifest.WithKinds(manifest.NewKind[map[string]any]("Config", kopts...)),
			)

			plan, err := reg.PlanMigration(t.Context(), configFS(map[string]string{"config.yaml": tc.text}))
			require.NoError(t, err)
			require.NoError(t, plan.Err())
			assert.Equal(t, []string{tc.want}, afters(plan))
			requireReloads(t, reg, plan, nil)
		})
	}
}

func TestRegistry_PlanMigration_Misread(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		text string
		// The text of the file after the plan, or "".
		want string
		// The error of the plan, which matches edit.ErrMisread, or "".
		err string
	}{
		"own key before a merge key that brings in the key": {
			// Control: go-yaml's decoder reads the merged entry, so the
			// migration of the document fails at New.
			text: stringtest.LinesLF("a:", "  b: 1", "s: &s {x: 1}", "m:", "  x: 2", "  <<: *s"),
			err: "config/config.yaml:5:3: $.m.x: go-yaml's decoder reads $.m.x from the merge key at 6:3, but the " +
				"merge spec keeps the own entry written before it",
		},
		"tag alone on an empty value before a line at the column of its key": {
			// Go-yaml reads "x: 1" as part of the empty value of !t, and the
			// plan migrates the document as go-yaml reads it.
			text: stringtest.LinesLF("a:", "  b: 1", "meta: !t", "x: 1"),
			want: stringtest.LinesLF("a:", "  version: v1", "  b: 1", "meta: !t", "x: 1"),
		},
		"value after the tag": {
			// Control.
			text: stringtest.LinesLF("a:", "  b: 1", "meta: !t ''", "x: 1"),
			want: stringtest.LinesLF("a:", "  version: v1", "  b: 1", "meta: !t ''", "x: 1"),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reg := manifest.MustNewRegistry(
				manifest.WithVersionOnly(paths.MustParse("$.a.version")),
				manifest.WithKinds(manifest.NewKind[map[string]any]("Config",
					manifest.Unversioned(), manifest.Version("v1"))),
			)

			plan, err := reg.PlanMigration(t.Context(), configFS(map[string]string{"config.yaml": tc.text}))
			require.NoError(t, err)

			if tc.err != "" {
				require.ErrorIs(t, plan.Err(), edit.ErrMisread)
				require.ErrorIs(t, plan.Err(), edit.ErrEngine)
				assert.Contains(t, plan.Err().Error(), tc.err)
				assert.False(t, niceyaml.IsInvalid(plan.Err()))

				return
			}

			require.NoError(t, plan.Err())
			assert.Equal(t, []string{tc.want}, afters(plan))
		})
	}
}

func TestRegistry_PlanMigration_Limits(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		opts  []manifest.RegistryOption
		limit niceyaml.SourceOption
		text  string
		want  string
	}{
		// The rename makes the 20 bytes of the text two bytes longer, so the
		// edit of the document fails. The byte limit is a limit of the whole
		// file, so the error binds at the site of the root (fix27 P83).
		"new text past the byte limit": {
			opts:  configOptions(),
			limit: niceyaml.WithByteLimit(21),
			want:  "config/config.yaml:1:1: $: file is larger than 21 bytes",
		},
		// Each rename makes the 44 bytes of the file two bytes longer, so the
		// edit of each document keeps the limit, and the file does not.
		"new texts of two documents that together pass the byte limit": {
			opts:  configOptions(),
			limit: niceyaml.WithByteLimit(46),
			text:  stringtest.LinesLF("version: 1", "port: 80", "---", "version: 1", "port: 80"),
			want:  "config/config.yaml: cannot rewrite a file whose new text holds more than 46 bytes",
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
			// The level past the limit lies in a value that the Set created
			// inside the mapping that it wrote over the port. So the problem
			// binds at the key of that mapping (fix25 P78).
			want: "config/config.yaml:2:1: $.port: document nests more than 2 levels",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			text := tc.text
			if text == "" {
				text = stringtest.LinesLF("version: 1", "port: 80")
			}

			in := configFS(map[string]string{"config.yaml": text}, manifest.WithSourceOptions(tc.limit))

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
			requireReloads(t, reg, plan, nil)

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
