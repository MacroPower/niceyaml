package manifest_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/manifest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/schema"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
)

const (
	cafeDoc = "apiVersion: cafe.jacobcolvin.com/v1\nkind: Cafe\nmetadata:\n  name: yamull\nspec:\n  seats: 40\n"

	hoursDoc = "apiVersion: cafe.jacobcolvin.com/v1alpha1\nkind: Hours\nmetadata:\n  name: weekday\n" +
		"spec:\n  opens: \"07:00\" # summer hours\n  closes: \"19:00\"\n"

	menuDoc = "# Fika menu, updated each season.\napiVersion: cafe.jacobcolvin.com/v1beta1\nkind: Dish\n" +
		"metadata:\n  name: kanelbulle\nspec:\n  cost: 45 # kr\n  tags: vegan\n---\n" +
		"apiVersion: cafe.jacobcolvin.com/v1\nkind: MenuItem\nmetadata:\n  name: semla\nspec:\n" +
		"  price: 65\n  tags: [seasonal]\n  legacyPricing: true\n"
)

// mapFS returns a file system that holds files under the directory config.
func mapFS(files map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{}
	for name, text := range files {
		fsys["config/"+name] = &fstest.MapFile{Data: []byte(text)}
	}

	return fsys
}

// listObjects returns a line for each object of set, in load order.
func listObjects(set *manifest.Set) []string {
	var got []string

	for o := range set.Objects() {
		version := o.Version
		if version == "" {
			version = "unversioned"
		}

		got = append(got, fmt.Sprintf("%s %q %s up-to-date=%t", o.Kind, o.Name, version, o.UpToDate))
	}

	return got
}

func TestRegistry_Load(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		files   map[string]string
		opts    []manifest.RegistryOption
		load    []manifest.LoadOption
		want    []string
		warns   string
		err     string
		matches []error
	}{
		"example directory": {
			files: map[string]string{"cafe.yaml": cafeDoc, "hours.yaml": hoursDoc, "menu.yaml": menuDoc},
			want: []string{
				`Cafe "yamull" cafe.jacobcolvin.com/v1 up-to-date=true`,
				`Hours "weekday" cafe.jacobcolvin.com/v1alpha1 up-to-date=false`,
				`MenuItem "kanelbulle" cafe.jacobcolvin.com/v1beta1 up-to-date=false`,
				`MenuItem "semla" cafe.jacobcolvin.com/v1 up-to-date=true`,
			},
			warns: "" +
				"config/hours.yaml:1:13: warning: $.apiVersion: deprecated version \"cafe.jacobcolvin.com/v1alpha1\"\n" +
				"`-- cafe 4.0 removes cafe.jacobcolvin.com/v1alpha1\n" +
				"\n" +
				"   1 | apiVersion: cafe.jacobcolvin.com/v1alpha1\n" +
				"     |             ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^\n" +
				"   2 | kind: Hours\n" +
				"   3 | metadata:",
		},
		"unknown kind close to a name": {
			files: map[string]string{
				"cafe.yaml":     cafeDoc,
				"specials.yaml": "apiVersion: cafe.jacobcolvin.com/v1\nkind: Menuitem\nmetadata:\n  name: smorgas\n",
			},
			err: "" +
				"config/specials.yaml:2:7: $.kind: unknown kind \"Menuitem\"\n" +
				"|-- did you mean \"MenuItem\"?\n" +
				"`-- a newer release of cafe might read this kind\n" +
				"\n" +
				"   1 | apiVersion: cafe.jacobcolvin.com/v1\n" +
				"   2 | kind: Menuitem\n" +
				"     |       ^^^^^^^^\n" +
				"   3 | metadata:\n" +
				"   4 |   name: smorgas",
			matches: []error{manifest.ErrUnknownKind},
		},
		"unknown kind far from every name": {
			files: map[string]string{
				"cafe.yaml":  cafeDoc,
				"other.yaml": "apiVersion: example.com/v1\nkind: Deployment\n",
			},
			err: "" +
				"config/other.yaml:2:7: $.kind: unknown kind \"Deployment\"\n" +
				"`-- cafe reads Cafe, Hours, and MenuItem\n" +
				"\n" +
				"   1 | apiVersion: example.com/v1\n" +
				"   2 | kind: Deployment\n" +
				"     |       ^^^^^^^^^^",
			matches: []error{manifest.ErrUnknownKind},
		},
		"unknown version": {
			files: map[string]string{
				"cafe.yaml":  cafeDoc,
				"hours.yaml": "apiVersion: cafe.jacobcolvin.com/v2\nkind: Hours\nmetadata:\n  name: weekday\n",
			},
			err: "" +
				"config/hours.yaml:1:13: $.apiVersion: unknown version \"cafe.jacobcolvin.com/v2\" for Hours\n" +
				"|-- cafe reads cafe.jacobcolvin.com/v1alpha1 and cafe.jacobcolvin.com/v1\n" +
				"`-- a newer release of cafe might read this version\n" +
				"\n" +
				"   1 | apiVersion: cafe.jacobcolvin.com/v2\n" +
				"     |             ^^^^^^^^^^^^^^^^^^^^^^^\n" +
				"   2 | kind: Hours\n" +
				"   3 | metadata:",
			matches: []error{manifest.ErrUnknownVersion},
		},
		"unknown version under an old kind name": {
			files: map[string]string{
				"cafe.yaml":     cafeDoc,
				"specials.yaml": "apiVersion: cafe.jacobcolvin.com/v2\nkind: Dish\nmetadata:\n  name: smorgas\n",
			},
			err: "" +
				"config/specials.yaml:1:13: $.apiVersion: unknown version \"cafe.jacobcolvin.com/v2\" for MenuItem\n" +
				"|-- cafe reads cafe.jacobcolvin.com/v1beta1 and cafe.jacobcolvin.com/v1\n" +
				"|-- a newer release of cafe might read this version\n" +
				"`-- cafe.jacobcolvin.com/v1beta1 calls MenuItem \"Dish\"\n" +
				"\n" +
				"   1 | apiVersion: cafe.jacobcolvin.com/v2\n" +
				"     |             ^^^^^^^^^^^^^^^^^^^^^^^\n" +
				"   2 | kind: Dish\n" +
				"   3 | metadata:",
			matches: []error{manifest.ErrUnknownVersion},
		},
		"kind name the version does not give": {
			files: map[string]string{
				"cafe.yaml": cafeDoc,
				"menu.yaml": "apiVersion: cafe.jacobcolvin.com/v1\nkind: Dish\nmetadata:\n  name: semla\n",
			},
			err: "" +
				"config/menu.yaml:2:7: $.kind: cafe.jacobcolvin.com/v1 has no kind \"Dish\"\n" +
				"|-- cafe.jacobcolvin.com/v1 calls it \"MenuItem\"\n" +
				"`-- set kind to \"MenuItem\", or set apiVersion to cafe.jacobcolvin.com/v1beta1, which calls it \"Dish\"\n" +
				"\n" +
				"   1 | apiVersion: cafe.jacobcolvin.com/v1\n" +
				"   2 | kind: Dish\n" +
				"     |       ^^^^\n" +
				"   3 | metadata:\n" +
				"   4 |   name: semla",
			matches: []error{manifest.ErrKindMismatch},
		},
		"removed version": {
			files: map[string]string{
				"cafe.yaml":     cafeDoc,
				"old-menu.yaml": "apiVersion: cafe.jacobcolvin.com/v1alpha1\nkind: Dish\nmetadata:\n  name: semla\n",
			},
			err: "" +
				"config/old-menu.yaml:1:13: $.apiVersion: removed version \"cafe.jacobcolvin.com/v1alpha1\" of Dish\n" +
				"`-- cafe 3.0 removed Dish v1alpha1, see https://jacobcolvin.com/cafe/upgrade/3\n" +
				"\n" +
				"   1 | apiVersion: cafe.jacobcolvin.com/v1alpha1\n" +
				"     |             ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^\n" +
				"   2 | kind: Dish\n" +
				"   3 | metadata:",
			matches: []error{manifest.ErrRemovedVersion},
		},
		"missing type fields in a walked file": {
			files: map[string]string{"cafe.yaml": cafeDoc, "notes.yaml": "- buy oat milk\n"},
			err: "" +
				"config/notes.yaml:1:1: missing apiVersion and kind\n" +
				"`-- add apiVersion and kind, or move the file out of config\n" +
				"\n" +
				"   1 | - buy oat milk\n" +
				"     | ^",
			matches: []error{manifest.ErrMissingTypeField},
		},
		"missing kind": {
			files: map[string]string{"cafe.yaml": cafeDoc, "x.yaml": "apiVersion: cafe.jacobcolvin.com/v1\na: 1\n"},
			err: "" +
				"config/x.yaml:1:1: missing kind\n" +
				"\n" +
				"   1 | apiVersion: cafe.jacobcolvin.com/v1\n" +
				"     | ^^^^^^^^^^\n" +
				"   2 | a: 1",
			matches: []error{manifest.ErrMissingTypeField},
		},
		"kind from an alias": {
			files: map[string]string{
				"cafe.yaml":  cafeDoc,
				"hours.yaml": "apiVersion: cafe.jacobcolvin.com/v1\nkind: *k\n",
			},
			err: "" +
				"config/hours.yaml:2:7: $.kind: kind must not come from an alias\n" +
				"`-- write the kind in the document itself\n" +
				"\n" +
				"   1 | apiVersion: cafe.jacobcolvin.com/v1\n" +
				"   2 | kind: *k\n" +
				"     |       ^^",
			matches: []error{manifest.ErrFieldAlias},
		},
		"name from a merge key": {
			files: map[string]string{
				"cafe.yaml": "apiVersion: cafe.jacobcolvin.com/v1\nkind: Cafe\n" +
					"metadata:\n  labels: &b {name: yamull}\n  <<: *b\n",
			},
			err: "" +
				"config/cafe.yaml:5:3: $.metadata.name: name must not come from a merge key\n" +
				"`-- write the name in the document itself\n" +
				"\n" +
				"   3 | metadata:\n" +
				"   4 |   labels: &b {name: yamull}\n" +
				"   5 |   <<: *b\n" +
				"     |   ^^",
			matches: []error{manifest.ErrFieldAlias},
		},
		"name that is no string": {
			files: map[string]string{
				"cafe.yaml": "apiVersion: cafe.jacobcolvin.com/v1\nkind: Cafe\nmetadata:\n  name: 0x10\n",
			},
			err: "" +
				"config/cafe.yaml:4:9: $.metadata.name: name must be a non-empty string\n" +
				"\n" +
				"   2 | kind: Cafe\n" +
				"   3 | metadata:\n" +
				"   4 |   name: 0x10\n" +
				"     |         ^^^^",
			matches: []error{manifest.ErrMissingName},
		},
		"duplicate name": {
			files: map[string]string{
				"cafe.yaml":       cafeDoc,
				"menu-extra.yaml": "apiVersion: cafe.jacobcolvin.com/v1beta1\nkind: Dish\nmetadata:\n  name: semla\n",
				"menu.yaml":       menuDoc,
			},
			err: "" +
				"config/menu.yaml:13:9: $.metadata.name: duplicate MenuItem \"semla\"\n" +
				"`-- config/menu-extra.yaml:4:9: $.metadata.name: first defined here\n" +
				"\n" +
				"config/menu.yaml\n" +
				"  11 | kind: MenuItem\n" +
				"  12 | metadata:\n" +
				"  13 |   name: semla\n" +
				"     |         ^^^^^\n" +
				"  14 | spec:\n" +
				"  15 |   price: 65\n" +
				"\n" +
				"config/menu-extra.yaml\n" +
				"   2 | kind: Dish\n" +
				"   3 | metadata:\n" +
				"   4 |   name: semla\n" +
				"     |         ^^^^^ first defined here",
			matches: []error{manifest.ErrDuplicateName},
		},
		"missing required kind": {
			files: map[string]string{"menu.yaml": menuDoc},
			err: "" +
				"config: missing Cafe\n" +
				"`-- add a document with apiVersion: cafe.jacobcolvin.com/v1 and kind: Cafe",
			matches: []error{manifest.ErrMissingManifest},
		},
		"missing required kind in an empty directory": {
			files: map[string]string{"cafe.YAML": cafeDoc},
			err: "" +
				"config: missing Cafe\n" +
				"|-- add a document with apiVersion: cafe.jacobcolvin.com/v1 and kind: Cafe\n" +
				"`-- config holds no file that matches *.yaml or *.yml",
			matches: []error{manifest.ErrMissingManifest},
		},
		"second singleton": {
			files: map[string]string{
				"cafe-copy.yaml": strings.Replace(cafeDoc, "yamull", "copy", 1),
				"cafe.yaml":      cafeDoc,
			},
			err: "" +
				"config/cafe.yaml:2:7: $.kind: more than one Cafe\n" +
				"`-- config/cafe-copy.yaml:2:7: $.kind: first defined here\n" +
				"\n" +
				"config/cafe.yaml\n" +
				"   1 | apiVersion: cafe.jacobcolvin.com/v1\n" +
				"   2 | kind: Cafe\n" +
				"     |       ^^^^\n" +
				"   3 | metadata:\n" +
				"   4 |   name: yamull\n" +
				"\n" +
				"config/cafe-copy.yaml\n" +
				"   1 | apiVersion: cafe.jacobcolvin.com/v1\n" +
				"   2 | kind: Cafe\n" +
				"     |       ^^^^ first defined here\n" +
				"   3 | metadata:\n" +
				"   4 |   name: copy",
			matches: []error{manifest.ErrExtraManifest},
		},
		"schema violation in a second document": {
			files: map[string]string{
				"cafe.yaml": cafeDoc,
				"menu.yaml": strings.Replace(menuDoc, "price: 65", "price: 0", 1),
			},
			err: "" +
				"config/menu.yaml:15:10: $.spec.price: 0 is less than 1\n" +
				"\n" +
				"  13 |   name: semla\n" +
				"  14 | spec:\n" +
				"  15 |   price: 0\n" +
				"     |          ^\n" +
				"  16 |   tags: [seasonal]\n" +
				"  17 |   legacyPricing: true",
		},
		"unknown field beside the type fields": {
			files: map[string]string{
				"cafe.yaml": "apiVersion: cafe.jacobcolvin.com/v1\nkind: Cafe\nmetadata:\n  name: yamull\nwifi: open\n",
			},
			err: "" +
				"config/cafe.yaml:5:1: $.wifi~: unknown field \"wifi\"\n" +
				"\n" +
				"   3 | metadata:\n" +
				"   4 |   name: yamull\n" +
				"   5 | wifi: open\n" +
				"     | ^^^^",
			matches: []error{niceyaml.ErrUnknownField},
		},
		"flow mapping with type fields": {
			files: map[string]string{
				"cafe.yaml": "{apiVersion: cafe.jacobcolvin.com/v1, kind: Cafe, metadata: {name: yamull}}\n",
			},
			want: []string{`Cafe "yamull" cafe.jacobcolvin.com/v1 up-to-date=true`},
		},
		"failing load with a warning": {
			files: map[string]string{
				"cafe.yaml":     cafeDoc,
				"hours.yaml":    hoursDoc,
				"specials.yaml": "apiVersion: cafe.jacobcolvin.com/v1\nkind: Menuitem\nmetadata:\n  name: smorgas\n",
			},
			err: "" +
				"config: 1 error, 1 warning\n" +
				"|-- config/hours.yaml:1:13: warning: $.apiVersion: deprecated version \"cafe.jacobcolvin.com/v1alpha1\"\n" +
				"|   `-- cafe 4.0 removes cafe.jacobcolvin.com/v1alpha1\n" +
				"`-- config/specials.yaml:2:7: $.kind: unknown kind \"Menuitem\"\n" +
				"    |-- did you mean \"MenuItem\"?\n" +
				"    `-- a newer release of cafe might read this kind\n" +
				"\n" +
				"config/hours.yaml\n" +
				"   1 | apiVersion: cafe.jacobcolvin.com/v1alpha1\n" +
				"     |             ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^ warning: deprecated version \"cafe.jacobcolvin.com/v1alpha1\"\n" +
				"   2 | kind: Hours\n" +
				"   3 | metadata:\n" +
				"\n" +
				"config/specials.yaml\n" +
				"   1 | apiVersion: cafe.jacobcolvin.com/v1\n" +
				"   2 | kind: Menuitem\n" +
				"     |       ^^^^^^^^ unknown kind \"Menuitem\"\n" +
				"   3 | metadata:\n" +
				"   4 |   name: smorgas",
			matches: []error{manifest.ErrUnknownKind, manifest.ErrDeprecated},
		},
		"strict mode": {
			files: map[string]string{"cafe.yaml": cafeDoc, "hours.yaml": hoursDoc},
			load:  []manifest.LoadOption{manifest.WithWarningsAsErrors(true)},
			err: "" +
				"config/hours.yaml:1:13: $.apiVersion: deprecated version \"cafe.jacobcolvin.com/v1alpha1\"\n" +
				"`-- cafe 4.0 removes cafe.jacobcolvin.com/v1alpha1\n" +
				"\n" +
				"   1 | apiVersion: cafe.jacobcolvin.com/v1alpha1\n" +
				"     |             ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^\n" +
				"   2 | kind: Hours\n" +
				"   3 | metadata:",
			matches: []error{manifest.ErrDeprecated},
		},
		"filtered warning in strict mode": {
			files: map[string]string{"cafe.yaml": cafeDoc, "hours.yaml": hoursDoc},
			load: []manifest.LoadOption{
				manifest.WithWarningsAsErrors(true),
				manifest.WithWarningFilter(func(w *manifest.Warning) bool {
					allowed := w.Kind == "Hours" && w.Version == "cafe.jacobcolvin.com/v1alpha1" &&
						w.Path.String() == "$.apiVersion" && w.File == "config/hours.yaml" &&
						errors.Is(w, manifest.ErrDeprecated)

					return !allowed
				}),
			},
			want: []string{
				`Cafe "yamull" cafe.jacobcolvin.com/v1 up-to-date=true`,
				`Hours "weekday" cafe.jacobcolvin.com/v1alpha1 up-to-date=false`,
			},
		},
		"deprecated kind": {
			files: map[string]string{
				"cafe.yaml": cafeDoc,
				"tip.yaml":  "apiVersion: tip/v1\nkind: Tip\nmetadata:\n  name: jar\n",
			},
			opts: []manifest.RegistryOption{manifest.WithKinds(manifest.NewKind[Tip]("Tip",
				manifest.Version("tip/v1", manifest.Deprecated("cafe 5.0 removes Tip")),
			))},
			want: []string{
				`Cafe "yamull" cafe.jacobcolvin.com/v1 up-to-date=true`,
				`Tip "jar" tip/v1 up-to-date=true`,
			},
			warns: "" +
				"config/tip.yaml:2:7: warning: $.kind: deprecated kind \"Tip\"\n" +
				"`-- cafe 5.0 removes Tip\n" +
				"\n" +
				"   1 | apiVersion: tip/v1\n" +
				"   2 | kind: Tip\n" +
				"     |       ^^^\n" +
				"   3 | metadata:\n" +
				"   4 |   name: jar",
		},
		"set checks off": {
			files: map[string]string{"cafe-copy.yaml": cafeDoc, "cafe.yaml": cafeDoc},
			load:  []manifest.LoadOption{manifest.WithSetChecks(false)},
			want: []string{
				`Cafe "yamull" cafe.jacobcolvin.com/v1 up-to-date=true`,
				`Cafe "yamull" cafe.jacobcolvin.com/v1 up-to-date=true`,
			},
		},
		"ignored documents": {
			files: map[string]string{
				"cafe.yaml":   cafeDoc,
				"deploy.yaml": "apiVersion: apps/v1\nkind: Deployment\n",
			},
			opts: []manifest.RegistryOption{manifest.WithIgnore(matcher.Text(paths.Doc().Child("apiVersion"),
				func(v string) bool { return !strings.HasPrefix(v, "cafe.jacobcolvin.com/") }))},
			want: []string{`Cafe "yamull" cafe.jacobcolvin.com/v1 up-to-date=true`},
		},
		"empty and comment files": {
			files: map[string]string{"cafe.yaml": cafeDoc, "empty.yaml": "", "notes.yaml": "# nothing\n"},
			want:  []string{`Cafe "yamull" cafe.jacobcolvin.com/v1 up-to-date=true`},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reg := newCafeRegistry(t, tc.opts...)

			set, err := reg.Load(t.Context(), manifest.FS(mapFS(tc.files), "config"), tc.load...)
			if tc.err != "" {
				require.Error(t, err)
				assert.Nil(t, set)
				assert.Equal(t, tc.err, niceyaml.FormatError(err))

				require.ErrorAs(t, err, new(*manifest.LoadError))

				for _, m := range tc.matches {
					require.ErrorIs(t, err, m)
				}

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, listObjects(set))

			if tc.warns == "" {
				assert.NoError(t, set.Warnings())

				return
			}

			assert.Equal(t, tc.warns, niceyaml.FormatError(set.Warnings()))
		})
	}
}

func TestRegistry_Load_Inputs(t *testing.T) {
	t.Parallel()

	reg := newCafeRegistry(t)

	cafe := niceyaml.NewSourceFromString(cafeDoc, niceyaml.WithName("cafe.yaml"))
	hours := niceyaml.NewSourceFromString(hoursDoc, niceyaml.WithName("hours.yaml"))
	nope := niceyaml.NewSourceFromString("apiVersion: x/v1\nkind: Nope\n", niceyaml.WithName("nope.yaml"))

	tcs := map[string]struct {
		in   manifest.Input
		want []string
		err  string
	}{
		"sources": {
			in: manifest.Sources(cafe, hours),
			want: []string{
				`Cafe "yamull" cafe.jacobcolvin.com/v1 up-to-date=true`,
				`Hours "weekday" cafe.jacobcolvin.com/v1alpha1 up-to-date=false`,
			},
		},
		"one source twice": {
			in:   manifest.Inputs(manifest.Sources(cafe), manifest.Sources(cafe)),
			want: []string{`Cafe "yamull" cafe.jacobcolvin.com/v1 up-to-date=true`},
		},
		"one file of a file system twice": {
			in: manifest.Inputs(
				manifest.FS(mapFS(map[string]string{"cafe.yaml": cafeDoc}), "config"),
				manifest.FS(mapFS(map[string]string{"cafe.yaml": cafeDoc}), "config/cafe.yaml"),
			),
			want: []string{
				`Cafe "yamull" cafe.jacobcolvin.com/v1 up-to-date=true`,
				`Cafe "yamull" cafe.jacobcolvin.com/v1 up-to-date=true`,
			},
			err: "more than one Cafe",
		},
		"zero input": {
			in:  manifest.Input{},
			err: "missing Cafe\n`-- add a document with apiVersion: cafe.jacobcolvin.com/v1 and kind: Cafe",
		},
		"named heading of a lone source": {
			in:  manifest.Sources(niceyaml.NewSourceFromString("", niceyaml.WithName("stdin.yaml"))),
			err: "stdin.yaml: missing Cafe",
		},
		"unnamed heading of several inputs": {
			in:  manifest.Inputs(manifest.Sources(hours), manifest.Sources(nope)),
			err: "2 errors, 1 warning\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			set, err := reg.Load(t.Context(), tc.in)
			if tc.err != "" {
				require.Error(t, err)
				assert.Contains(t, niceyaml.FormatError(err), tc.err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, listObjects(set))
		})
	}
}

func TestRegistry_Load_Context(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := newCafeRegistry(t).Load(ctx, manifest.FS(mapFS(map[string]string{"cafe.yaml": cafeDoc}), "config"))
	require.ErrorIs(t, err, context.Canceled)
}

func TestRegistry_Load_Unversioned(t *testing.T) {
	t.Parallel()

	opts := func(extra ...manifest.KindOption) []manifest.RegistryOption {
		return []manifest.RegistryOption{
			manifest.WithAppName("cafe"),
			manifest.WithKinds(manifest.NewKind[CafeSpec]("Cafe", append(extra,
				manifest.Unversioned(),
				manifest.Version("cafe.jacobcolvin.com/v1"),
			)...)),
		}
	}

	tcs := map[string]struct {
		opts  []manifest.RegistryOption
		files map[string]string
		want  []string
		err   string
	}{
		"plain document": {
			opts:  opts(),
			files: map[string]string{"cafe.yaml": "seats: 40\n"},
			want:  []string{`Cafe "" unversioned up-to-date=true`},
		},
		"plain document of the wrong layout": {
			opts:  opts(),
			files: map[string]string{"notes.yaml": "- a\n"},
			err: "" +
				"config/notes.yaml:1:3: $: expected mapping, got sequence\n" +
				"|-- the document has no apiVersion and no kind, so cafe reads it as an unversioned Cafe\n" +
				"`-- move the file out of config if it is not a Cafe\n" +
				"\n" +
				"   1 | - a\n" +
				"     |   ^",
		},
		"versioned document": {
			opts:  opts(),
			files: map[string]string{"cafe.yaml": "apiVersion: cafe.jacobcolvin.com/v1\nkind: Cafe\nseats: 1\n"},
			want:  []string{`Cafe "" cafe.jacobcolvin.com/v1 up-to-date=true`},
		},
		"claimed document with a version": {
			opts: opts(manifest.Claim(matcher.Exists(paths.Doc().Child("seats")))),
			files: map[string]string{
				"cafe.yaml": "apiVersion: cafe.jacobcolvin.com/v1\nseats: 1\n",
			},
			want: []string{`Cafe "" cafe.jacobcolvin.com/v1 up-to-date=true`},
		},
		"removed unversioned layout": {
			opts: []manifest.RegistryOption{
				manifest.WithRemoved("", "Cafe", "cafe 7.0 reads only files with apiVersion and kind"),
				manifest.WithKinds(manifest.NewKind[CafeSpec]("Cafe", manifest.Version("cafe.jacobcolvin.com/v1"))),
			},
			files: map[string]string{"cafe.yaml": "seats: 40\n"},
			err: "" +
				"config/cafe.yaml:1:1: removed unversioned layout of Cafe\n" +
				"`-- cafe 7.0 reads only files with apiVersion and kind\n" +
				"\n" +
				"   1 | seats: 40\n" +
				"     | ^^^^^",
		},
		"version only": {
			opts: []manifest.RegistryOption{
				manifest.WithAppName("cafe"),
				manifest.WithVersionOnly(paths.Doc().Child("version")),
				manifest.WithKinds(manifest.NewKind[CafeSpec]("Config",
					manifest.Version("1"), manifest.Version("2"))),
			},
			files: map[string]string{"a.yaml": "version: 2\nseats: 3\n", "b.yaml": "version: 3\n"},
			err: "" +
				"config/b.yaml:1:10: $.version: unknown version \"3\" for Config\n" +
				"|-- cafe reads version 1 and version 2\n" +
				"`-- a newer release of cafe might read this version\n" +
				"\n" +
				"   1 | version: 3\n" +
				"     |          ^",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reg, err := manifest.NewRegistry(tc.opts...)
			require.NoError(t, err)

			set, err := reg.Load(t.Context(), manifest.FS(mapFS(tc.files), "config"))
			if tc.err != "" {
				require.Error(t, err)
				assert.Equal(t, tc.err, niceyaml.FormatError(err))

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, listObjects(set))
		})
	}
}

func TestRegistry_Load_SchemaLoad(t *testing.T) {
	t.Parallel()

	reg, err := manifest.NewRegistry(manifest.WithKinds(manifest.NewKind[CafeSpec]("Cafe",
		manifest.Version("v1", manifest.Schema(schema.Embedded([]byte(`{"type": 5}`)))),
	)))
	require.NoError(t, err)

	src := niceyaml.NewSourceFromString("apiVersion: v1\nkind: Cafe\n", niceyaml.WithName("cafe.yaml"))

	_, err = reg.Load(t.Context(), manifest.Sources(src))
	require.ErrorIs(t, err, schema.ErrCompile)
	assert.False(t, niceyaml.IsInvalid(err))
	assert.Equal(t, "cafe.yaml: this program cannot load its schema of Cafe v1", firstLine(err.Error()))
}

func TestRegistry_Load_Checks(t *testing.T) {
	t.Parallel()

	errPricey := errors.New("pricey")

	tcs := map[string]struct {
		check func(ctx context.Context, set *manifest.Set) error
		err   string
		warns string
	}{
		"error bound through an object": {
			check: func(_ context.Context, set *manifest.Set) error {
				var errs []error

				for item := range set.All[MenuItem]() {
					if item.Value.Spec.Price > 50 {
						errs = append(errs, item.Bind(niceyaml.NewError("price is more than 50 kr",
							niceyaml.AtPath(paths.Doc().Child("spec", "price")))))
					}
				}

				return errors.Join(errs...)
			},
			err: "" +
				"config/menu.yaml:15:10: $.spec.price: price is more than 50 kr\n" +
				"\n" +
				"  13 |   name: semla\n" +
				"  14 | spec:\n" +
				"  15 |   price: 65\n" +
				"     |          ^^\n" +
				"  16 |   tags: [seasonal]\n" +
				"  17 |   legacyPricing: true",
		},
		"error bound in no document": {
			check: func(context.Context, *manifest.Set) error { return errPricey },
			err:   "config: pricey",
		},
		"error without a location": {
			check: func(_ context.Context, set *manifest.Set) error {
				item, ok := set.Get[MenuItem]("kanelbulle")
				require.True(t, ok)

				return item.Bind(errors.New("no Hours serve kanelbulle"))
			},
			err: "config/menu.yaml: document 1: no Hours serve kanelbulle",
		},
		"warning": {
			check: func(_ context.Context, set *manifest.Set) error {
				cafe, ok := set.One[Cafe]()
				require.True(t, ok)

				return cafe.Bind(niceyaml.NewWarning("small cafe",
					niceyaml.AtPath(paths.Doc().Child("spec", "seats"))))
			},
			warns: "" +
				"config/cafe.yaml:6:10: warning: $.spec.seats: small cafe\n" +
				"\n" +
				"   4 |   name: yamull\n" +
				"   5 | spec:\n" +
				"   6 |   seats: 40\n" +
				"     |          ^^",
		},
		"panic": {
			check: func(context.Context, *manifest.Set) error { panic("boom") },
			err:   "config: check panicked: panic: boom",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reg := newCafeRegistry(t, manifest.WithCheck(tc.check))

			set, err := reg.Load(t.Context(),
				manifest.FS(mapFS(map[string]string{"cafe.yaml": cafeDoc, "menu.yaml": menuDoc}), "config"))
			if tc.err != "" {
				require.Error(t, err)
				assert.Equal(t, tc.err, niceyaml.FormatError(err))

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.warns, niceyaml.FormatError(set.Warnings()))
		})
	}

	t.Run("warning after an empty document names its manifest", func(t *testing.T) {
		t.Parallel()

		reg := newCafeRegistry(t, manifest.WithCheck(func(_ context.Context, set *manifest.Set) error {
			cafe, ok := set.Get[Cafe]("yamull")
			require.True(t, ok)

			return cafe.Bind(niceyaml.NewWarning("small cafe", niceyaml.AtPath(paths.Doc().Child("spec", "seats"))))
		}))

		files := mapFS(map[string]string{"cafe.yaml": "---\n---\n" + cafeDoc})

		set, err := reg.Load(t.Context(), manifest.FS(files, "config"))
		require.NoError(t, err)

		w, ok := errors.AsType[*manifest.Warning](set.Warnings())
		require.True(t, ok)
		assert.Equal(t, "Cafe cafe.jacobcolvin.com/v1", w.Kind+" "+w.Version)
	})
}

func TestLoadError(t *testing.T) {
	t.Parallel()

	reg := newCafeRegistry(t)

	_, err := reg.Load(t.Context(), manifest.FS(mapFS(map[string]string{
		"cafe.yaml":     cafeDoc,
		"hours.yaml":    hoursDoc,
		"menu.yaml":     strings.Replace(menuDoc, "price: 65", "price: 0", 1),
		"specials.yaml": "apiVersion: cafe.jacobcolvin.com/v1\nkind: Menuitem\nmetadata:\n  name: smorgas\n",
	}), "config"))
	require.Error(t, err)

	assert.Equal(t, ""+
		"config: 2 errors, 1 warning\n"+
		"config/hours.yaml:1:13: warning: $.apiVersion: deprecated version \"cafe.jacobcolvin.com/v1alpha1\"\n"+
		"config/menu.yaml:15:10: $.spec.price: 0 is less than 1\n"+
		"config/specials.yaml:2:7: $.kind: unknown kind \"Menuitem\"", err.Error())

	var rows []string

	for row := range niceyaml.Problems(err) {
		level := "error"
		if row.Warning {
			level = "warning"
		}

		w, isWarning := errors.AsType[*manifest.Warning](row.Err)
		if isWarning {
			level += " " + w.Kind + " " + w.Version + " " + w.Path.String()
		}

		rows = append(rows, level+": "+row.Message())
	}

	assert.Equal(t, []string{
		`warning Hours cafe.jacobcolvin.com/v1alpha1 $.apiVersion: deprecated version "cafe.jacobcolvin.com/v1alpha1"`,
		`error: 0 is less than 1`,
		`error: unknown kind "Menuitem"`,
	}, rows)

	strict := niceyaml.AsErrors(err)

	require.ErrorAs(t, strict, new(*manifest.LoadError))
	assert.Equal(t, "config: 3 errors", firstLine(strict.Error()))
	assert.True(t, niceyaml.IsInvalid(err))

	assert.ErrorAs(t, strict, new(*manifest.Warning))
}

func TestWarning(t *testing.T) {
	t.Parallel()

	w := &manifest.Warning{Err: manifest.ErrDeprecated}
	assert.Equal(t, "deprecated", w.Error())
	require.ErrorIs(t, w, manifest.ErrDeprecated)
	assert.Empty(t, (&manifest.Warning{}).Error())
}

// firstLine returns the first line of s.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")

	return line
}

func TestRegistry_Load_Layers(t *testing.T) {
	t.Parallel()

	base := niceyaml.NewSourceFromString(
		"apiVersion: cafe.jacobcolvin.com/v1alpha1\nkind: Hours\nmetadata:\n  name: weekday\n",
		niceyaml.WithName("config/base/hours.yaml"),
	)

	tcs := map[string]struct {
		top  string
		want []string
		err  string
	}{
		"layers agree": {
			top:  "spec:\n  opens: \"08:00\"\n",
			want: []string{`Hours "weekday" cafe.jacobcolvin.com/v1alpha1 up-to-date=false`},
		},
		"layers declare different versions": {
			top: "apiVersion: cafe.jacobcolvin.com/v1\n",
			err: "" +
				"config/base/hours.yaml:1:13: $.apiVersion: layers declare different versions\n" +
				"`-- config/prod/hours.yaml:1:13: $.apiVersion: cafe.jacobcolvin.com/v1 overrides " +
				"cafe.jacobcolvin.com/v1alpha1",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			top := niceyaml.NewSourceFromString(tc.top, niceyaml.WithName("config/prod/hours.yaml"))
			merged := yamltest.Layers(t, base, top)

			set, err := newCafeRegistry(t).Load(t.Context(), manifest.Sources(merged), manifest.WithSetChecks(false))
			if tc.err != "" {
				require.ErrorIs(t, err, manifest.ErrMixedLayers)
				assert.Equal(t, tc.err, firstLines(niceyaml.FormatError(err), 2))

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, listObjects(set))
		})
	}
}

func TestRegistry_Load_Hint(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tree{files: map[string]string{"config/hours.yaml": hoursDoc}}.write(t, dir)

	reg := newCafeRegistry(t,
		manifest.WithRemoved("v0", "Tab"),
		manifest.WithKinds(manifest.NewKind[Tip]("Tab",
			manifest.Version("v0"),
			manifest.Version("v1", manifest.Written()),
		)),
	)

	set, err := reg.Load(t.Context(), manifest.Path(filepath.Join(dir, "config")), manifest.WithSetChecks(false))
	require.NoError(t, err)
	assert.Contains(t, niceyaml.FormatError(set.Warnings()),
		"|-- cafe 4.0 removes cafe.jacobcolvin.com/v1alpha1\n"+
			"`-- run \"cafe config migrate\" to update this file\n")

	tab := niceyaml.NewSourceFromString("apiVersion: v0\nkind: Tab\nmetadata:\n  name: jar\n",
		niceyaml.WithName("tab.yaml"))

	_, err = reg.Load(t.Context(), manifest.Sources(tab), manifest.WithSetChecks(false))
	require.ErrorIs(t, err, manifest.ErrRemovedVersion)
	assert.Equal(t, `tab.yaml:1:13: $.apiVersion: removed version "v0" of Tab`, err.Error())
}

// firstLines returns the first n lines of s.
func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)

	return strings.Join(lines[:min(n, len(lines))], "\n")
}
