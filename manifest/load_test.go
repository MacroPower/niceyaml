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
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/manifest"
	"go.jacobcolvin.com/niceyaml/manifest/migrate"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/schema"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
)

var (
	cafeDoc = stringtest.LinesLF(
		"apiVersion: cafe.jacobcolvin.com/v1",
		"kind: Cafe",
		"metadata:",
		"  name: yamull",
		"spec:",
		"  seats: 40",
	)

	hoursDoc = stringtest.LinesLF(
		"apiVersion: cafe.jacobcolvin.com/v1alpha1",
		"kind: Hours",
		"metadata:",
		"  name: weekday",
		"spec:",
		`  opens: "07:00" # summer hours`,
		`  closes: "19:00"`,
	)

	menuDoc = stringtest.LinesLF(
		"# Fika menu, updated each season.",
		"apiVersion: cafe.jacobcolvin.com/v1beta1",
		"kind: Dish",
		"metadata:",
		"  name: kanelbulle",
		"spec:",
		"  cost: 45 # kr",
		"  tags: vegan",
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

	// A MenuItem whose kind is misspelled.
	specialsDoc = stringtest.LinesLF(
		"apiVersion: cafe.jacobcolvin.com/v1",
		"kind: Menuitem",
		"metadata:",
		"  name: smorgas",
	)

	// A Tab at v0, which [tabOptions] removes while the kind still lists
	// it.
	tabDoc = stringtest.LinesLF(
		"apiVersion: v0",
		"kind: Tab",
		"metadata:",
		"  name: jar",
	)

	// The files of the example directory.
	exampleFiles = map[string]string{"cafe.yaml": cafeDoc, "hours.yaml": hoursDoc, "menu.yaml": menuDoc}

	// A MenuItem at each version, each of which sets the fields that the
	// schemas of [deprecationOptions] deprecate.
	deprecatedMenu = stringtest.LinesLF(
		"apiVersion: cafe.jacobcolvin.com/v1beta1",
		"kind: Dish",
		"metadata:",
		"  name: kanelbulle",
		"spec:",
		"  cost: 45",
		"  legacyPricing: true",
		"---",
		"apiVersion: cafe.jacobcolvin.com/v1",
		"kind: MenuItem",
		"metadata:",
		"  name: semla",
		"spec:",
		"  price: 65",
		"  legacyPricing: true",
	)
)

// mapFS returns a file system that holds files under the directory config.
func mapFS(files map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{}
	for name, text := range files {
		fsys["config/"+name] = &fstest.MapFile{Data: []byte(text)}
	}

	return fsys
}

// configFS returns the input of the directory config of a file system
// that holds files there, read with opts.
func configFS(files map[string]string, opts ...manifest.InputOption) manifest.Input {
	return manifest.FS(mapFS(files), "config", opts...)
}

// tabOptions returns the options that add the kind Tab and remove its
// v0, a version that the kind still lists.
func tabOptions() []manifest.RegistryOption {
	return []manifest.RegistryOption{
		manifest.WithRemoved("v0", "Tab"),
		manifest.WithKinds(manifest.NewKind[Tip]("Tab",
			manifest.Version("v0"),
			manifest.Version("v1", manifest.Written()),
		)),
	}
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

// loadCase is a case of a table that loads files from the directory
// config of a file system.
type loadCase struct {
	files map[string]string
	opts  []manifest.RegistryOption
	in    []manifest.InputOption
	load  []manifest.LoadOption
	want  []string
	warns string
	err   string
	// Matches lists the errors that the error of the load, or its
	// warnings when the load succeeds, must match.
	matches []error
}

// run loads the files of tc with the registry of its opts, and compares
// the objects and the warnings of the set, or the error of the load.
func (tc loadCase) run(t *testing.T) {
	t.Helper()

	set, err := manifest.MustNewRegistry(tc.opts...).Load(t.Context(), configFS(tc.files, tc.in...), tc.load...)
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
		require.NoError(t, set.Warnings())

		return
	}

	assert.Equal(t, tc.warns, niceyaml.FormatError(set.Warnings()))

	for _, m := range tc.matches {
		require.ErrorIs(t, set.Warnings(), m)
	}
}

func TestRegistry_Load(t *testing.T) {
	t.Parallel()

	// The opts of each case come after those of [cafeOptions].
	tcs := map[string]loadCase{
		"example directory": {
			files: exampleFiles,
			want: []string{
				`Cafe "yamull" cafe.jacobcolvin.com/v1 up-to-date=true`,
				`Hours "weekday" cafe.jacobcolvin.com/v1alpha1 up-to-date=false`,
				`MenuItem "kanelbulle" cafe.jacobcolvin.com/v1beta1 up-to-date=false`,
				`MenuItem "semla" cafe.jacobcolvin.com/v1 up-to-date=true`,
			},
			warns: stringtest.JoinLF(
				`config/hours.yaml:1:13: warning: $.apiVersion: deprecated version "cafe.jacobcolvin.com/v1alpha1"`,
				"`-- cafe 4.0 removes cafe.jacobcolvin.com/v1alpha1",
				"",
				"   1 | apiVersion: cafe.jacobcolvin.com/v1alpha1",
				"     |             ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^",
				"   2 | kind: Hours",
				"   3 | metadata:",
			),
		},
		"unknown kind close to a name": {
			files: map[string]string{
				"cafe.yaml":     cafeDoc,
				"specials.yaml": specialsDoc,
			},
			err: stringtest.JoinLF(
				`config/specials.yaml:2:7: $.kind: unknown kind "Menuitem"`,
				`|-- did you mean "MenuItem"?`,
				"`-- a newer release of cafe might read this kind",
				"",
				"   1 | apiVersion: cafe.jacobcolvin.com/v1",
				"   2 | kind: Menuitem",
				"     |       ^^^^^^^^",
				"   3 | metadata:",
				"   4 |   name: smorgas",
			),
			matches: []error{manifest.ErrUnknownKind},
		},
		"unknown kind far from every name": {
			files: map[string]string{
				"cafe.yaml":  cafeDoc,
				"other.yaml": stringtest.LinesLF("apiVersion: example.com/v1", "kind: Deployment"),
			},
			err: stringtest.JoinLF(
				`config/other.yaml:2:7: $.kind: unknown kind "Deployment"`,
				"`-- cafe reads Cafe, Hours, and MenuItem",
				"",
				"   1 | apiVersion: example.com/v1",
				"   2 | kind: Deployment",
				"     |       ^^^^^^^^^^",
			),
			matches: []error{manifest.ErrUnknownKind},
		},
		"unknown version": {
			files: map[string]string{
				"cafe.yaml": cafeDoc,
				"hours.yaml": stringtest.LinesLF(
					"apiVersion: cafe.jacobcolvin.com/v2",
					"kind: Hours",
					"metadata:",
					"  name: weekday",
				),
			},
			err: stringtest.JoinLF(
				`config/hours.yaml:1:13: $.apiVersion: unknown version "cafe.jacobcolvin.com/v2" for Hours`,
				"|-- cafe reads cafe.jacobcolvin.com/v1alpha1 and cafe.jacobcolvin.com/v1",
				"`-- a newer release of cafe might read this version",
				"",
				"   1 | apiVersion: cafe.jacobcolvin.com/v2",
				"     |             ^^^^^^^^^^^^^^^^^^^^^^^",
				"   2 | kind: Hours",
				"   3 | metadata:",
			),
			matches: []error{manifest.ErrUnknownVersion},
		},
		"unknown version under an old kind name": {
			files: map[string]string{
				"cafe.yaml": cafeDoc,
				"specials.yaml": stringtest.LinesLF(
					"apiVersion: cafe.jacobcolvin.com/v2",
					"kind: Dish",
					"metadata:",
					"  name: smorgas",
				),
			},
			err: stringtest.JoinLF(
				`config/specials.yaml:1:13: $.apiVersion: unknown version "cafe.jacobcolvin.com/v2" for MenuItem`,
				"|-- cafe reads cafe.jacobcolvin.com/v1beta1 and cafe.jacobcolvin.com/v1",
				"|-- a newer release of cafe might read this version",
				"`-- cafe.jacobcolvin.com/v1beta1 calls MenuItem \"Dish\"",
				"",
				"   1 | apiVersion: cafe.jacobcolvin.com/v2",
				"     |             ^^^^^^^^^^^^^^^^^^^^^^^",
				"   2 | kind: Dish",
				"   3 | metadata:",
			),
			matches: []error{manifest.ErrUnknownVersion},
		},
		"kind name the version does not give": {
			files: map[string]string{
				"cafe.yaml": cafeDoc,
				"menu.yaml": stringtest.LinesLF(
					"apiVersion: cafe.jacobcolvin.com/v1",
					"kind: Dish",
					"metadata:",
					"  name: semla",
				),
			},
			err: stringtest.JoinLF(
				`config/menu.yaml:2:7: $.kind: cafe.jacobcolvin.com/v1 has no kind "Dish"`,
				`|-- cafe.jacobcolvin.com/v1 calls it "MenuItem"`,
				"`-- set kind to \"MenuItem\", or set apiVersion to cafe.jacobcolvin.com/v1beta1, which calls it \"Dish\"",
				"",
				"   1 | apiVersion: cafe.jacobcolvin.com/v1",
				"   2 | kind: Dish",
				"     |       ^^^^",
				"   3 | metadata:",
				"   4 |   name: semla",
			),
			matches: []error{manifest.ErrKindMismatch},
		},
		"removed version": {
			files: map[string]string{
				"cafe.yaml": cafeDoc,
				"old-menu.yaml": stringtest.LinesLF(
					"apiVersion: cafe.jacobcolvin.com/v1alpha1",
					"kind: Dish",
					"metadata:",
					"  name: semla",
				),
			},
			err: stringtest.JoinLF(
				`config/old-menu.yaml:1:13: $.apiVersion: removed version "cafe.jacobcolvin.com/v1alpha1" of Dish`,
				"`-- cafe 3.0 removed Dish v1alpha1, see https://jacobcolvin.com/cafe/upgrade/3",
				"",
				"   1 | apiVersion: cafe.jacobcolvin.com/v1alpha1",
				"     |             ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^",
				"   2 | kind: Dish",
				"   3 | metadata:",
			),
			matches: []error{manifest.ErrRemovedVersion},
		},
		"removed version that the kind still lists": {
			files: map[string]string{"cafe.yaml": cafeDoc, "tab.yaml": tabDoc},
			opts:  tabOptions(),
			err: stringtest.JoinLF(
				`config/tab.yaml:1:13: $.apiVersion: removed version "v0" of Tab`,
				"",
				"   1 | apiVersion: v0",
				"     |             ^^",
				"   2 | kind: Tab",
				"   3 | metadata:",
			),
			matches: []error{manifest.ErrRemovedVersion},
		},
		"missing type fields in a walked file": {
			files: map[string]string{"cafe.yaml": cafeDoc, "notes.yaml": "- buy oat milk\n"},
			err: stringtest.JoinLF(
				"config/notes.yaml:1:1: missing apiVersion and kind",
				"`-- add apiVersion and kind, or move the file out of config",
				"",
				"   1 | - buy oat milk",
				"     | ^",
			),
			matches: []error{manifest.ErrMissingTypeField},
		},
		"missing kind": {
			files: map[string]string{
				"cafe.yaml": cafeDoc,
				"x.yaml":    stringtest.LinesLF("apiVersion: cafe.jacobcolvin.com/v1", "a: 1"),
			},
			err: stringtest.JoinLF(
				"config/x.yaml:1:1: missing kind",
				"",
				"   1 | apiVersion: cafe.jacobcolvin.com/v1",
				"     | ^^^^^^^^^^",
				"   2 | a: 1",
			),
			matches: []error{manifest.ErrMissingTypeField},
		},
		"kind from an alias": {
			files: map[string]string{
				"cafe.yaml":  cafeDoc,
				"hours.yaml": stringtest.LinesLF("apiVersion: cafe.jacobcolvin.com/v1", "kind: *k"),
			},
			err: stringtest.JoinLF(
				"config/hours.yaml:2:7: $.kind: kind must not come from an alias",
				"`-- write the kind in the document itself",
				"",
				"   1 | apiVersion: cafe.jacobcolvin.com/v1",
				"   2 | kind: *k",
				"     |       ^^",
			),
			matches: []error{manifest.ErrFieldAlias},
		},
		"name from a merge key": {
			files: map[string]string{
				"cafe.yaml": stringtest.LinesLF(
					"apiVersion: cafe.jacobcolvin.com/v1",
					"kind: Cafe",
					"metadata:",
					"  labels: &b {name: yamull}",
					"  <<: *b",
				),
			},
			err: stringtest.JoinLF(
				"config/cafe.yaml:5:3: $.metadata.name: name must not come from a merge key",
				"`-- write the name in the document itself",
				"",
				"   3 | metadata:",
				"   4 |   labels: &b {name: yamull}",
				"   5 |   <<: *b",
				"     |   ^^",
			),
			matches: []error{manifest.ErrFieldAlias},
		},
		"name that is no string": {
			files: map[string]string{
				"cafe.yaml": stringtest.LinesLF(
					"apiVersion: cafe.jacobcolvin.com/v1",
					"kind: Cafe",
					"metadata:",
					"  name: 0x10",
				),
			},
			err: stringtest.JoinLF(
				"config/cafe.yaml:4:9: $.metadata.name: name must be a non-empty string",
				"",
				"   2 | kind: Cafe",
				"   3 | metadata:",
				"   4 |   name: 0x10",
				"     |         ^^^^",
			),
			matches: []error{manifest.ErrMissingName},
		},
		"duplicate name": {
			files: map[string]string{
				"cafe.yaml": cafeDoc,
				"menu-extra.yaml": stringtest.LinesLF(
					"apiVersion: cafe.jacobcolvin.com/v1beta1",
					"kind: Dish",
					"metadata:",
					"  name: semla",
				),
				"menu.yaml": menuDoc,
			},
			err: stringtest.JoinLF(
				`config/menu.yaml:13:9: $.metadata.name: duplicate MenuItem "semla"`,
				"`-- config/menu-extra.yaml:4:9: $.metadata.name: first defined here",
				"",
				"config/menu.yaml",
				"  11 | kind: MenuItem",
				"  12 | metadata:",
				"  13 |   name: semla",
				"     |         ^^^^^",
				"  14 | spec:",
				"  15 |   price: 65",
				"",
				"config/menu-extra.yaml",
				"   2 | kind: Dish",
				"   3 | metadata:",
				"   4 |   name: semla",
				"     |         ^^^^^ first defined here",
			),
			matches: []error{manifest.ErrDuplicateName},
		},
		"missing required kind": {
			files: map[string]string{"menu.yaml": menuDoc},
			err: stringtest.JoinLF(
				"config: missing Cafe",
				"`-- add a document with apiVersion: cafe.jacobcolvin.com/v1 and kind: Cafe",
			),
			matches: []error{manifest.ErrMissingManifest},
		},
		"missing required kind in an empty directory": {
			files: map[string]string{"cafe.YAML": cafeDoc},
			err: stringtest.JoinLF(
				"config: missing Cafe",
				"|-- add a document with apiVersion: cafe.jacobcolvin.com/v1 and kind: Cafe",
				"`-- config holds no file that matches *.yaml or *.yml",
			),
			matches: []error{manifest.ErrMissingManifest},
		},
		"second singleton": {
			files: map[string]string{
				"cafe-copy.yaml": strings.Replace(cafeDoc, "yamull", "copy", 1),
				"cafe.yaml":      cafeDoc,
			},
			err: stringtest.JoinLF(
				"config/cafe.yaml:2:7: $.kind: more than one Cafe",
				"`-- config/cafe-copy.yaml:2:7: $.kind: first defined here",
				"",
				"config/cafe.yaml",
				"   1 | apiVersion: cafe.jacobcolvin.com/v1",
				"   2 | kind: Cafe",
				"     |       ^^^^",
				"   3 | metadata:",
				"   4 |   name: yamull",
				"",
				"config/cafe-copy.yaml",
				"   1 | apiVersion: cafe.jacobcolvin.com/v1",
				"   2 | kind: Cafe",
				"     |       ^^^^ first defined here",
				"   3 | metadata:",
				"   4 |   name: copy",
			),
			matches: []error{manifest.ErrExtraManifest},
		},
		"schema violation in a second document": {
			files: map[string]string{
				"cafe.yaml": cafeDoc,
				"menu.yaml": strings.Replace(menuDoc, "price: 65", "price: 0", 1),
			},
			err: stringtest.JoinLF(
				"config/menu.yaml:15:10: $.spec.price: 0 is less than 1",
				"",
				"  13 |   name: semla",
				"  14 | spec:",
				"  15 |   price: 0",
				"     |          ^",
				"  16 |   tags: [seasonal]",
				"  17 |   legacyPricing: true",
			),
		},
		"unknown field beside the type fields": {
			files: map[string]string{
				"cafe.yaml": stringtest.LinesLF(
					"apiVersion: cafe.jacobcolvin.com/v1",
					"kind: Cafe",
					"metadata:",
					"  name: yamull",
					"wifi: open",
				),
			},
			err: stringtest.JoinLF(
				`config/cafe.yaml:5:1: $.wifi~: unknown field "wifi"`,
				"",
				"   3 | metadata:",
				"   4 |   name: yamull",
				"   5 | wifi: open",
				"     | ^^^^",
			),
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
				"specials.yaml": specialsDoc,
			},
			err: stringtest.JoinLF(
				"config: 1 error, 1 warning",
				`|-- config/hours.yaml:1:13: warning: $.apiVersion: deprecated version "cafe.jacobcolvin.com/v1alpha1"`,
				"|   `-- cafe 4.0 removes cafe.jacobcolvin.com/v1alpha1",
				"`-- config/specials.yaml:2:7: $.kind: unknown kind \"Menuitem\"",
				`    |-- did you mean "MenuItem"?`,
				"    `-- a newer release of cafe might read this kind",
				"",
				"config/hours.yaml",
				"   1 | apiVersion: cafe.jacobcolvin.com/v1alpha1",
				`     |             ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^ warning: deprecated version "cafe.jacobcolvin.com/v1alpha1"`,
				"   2 | kind: Hours",
				"   3 | metadata:",
				"",
				"config/specials.yaml",
				"   1 | apiVersion: cafe.jacobcolvin.com/v1",
				"   2 | kind: Menuitem",
				`     |       ^^^^^^^^ unknown kind "Menuitem"`,
				"   3 | metadata:",
				"   4 |   name: smorgas",
			),
			matches: []error{manifest.ErrUnknownKind, manifest.ErrDeprecated},
		},
		"strict mode": {
			files: map[string]string{"cafe.yaml": cafeDoc, "hours.yaml": hoursDoc},
			load:  []manifest.LoadOption{manifest.WithWarningsAsErrors(true)},
			err: stringtest.JoinLF(
				`config/hours.yaml:1:13: $.apiVersion: deprecated version "cafe.jacobcolvin.com/v1alpha1"`,
				"`-- cafe 4.0 removes cafe.jacobcolvin.com/v1alpha1",
				"",
				"   1 | apiVersion: cafe.jacobcolvin.com/v1alpha1",
				"     |             ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^",
				"   2 | kind: Hours",
				"   3 | metadata:",
			),
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
				"tip.yaml": stringtest.LinesLF(
					"apiVersion: tip/v1",
					"kind: Tip",
					"metadata:",
					"  name: jar",
				),
			},
			opts: withKinds(manifest.NewKind[Tip]("Tip",
				manifest.Version("tip/v1", manifest.Deprecated("cafe 5.0 removes Tip")),
			)),
			want: []string{
				`Cafe "yamull" cafe.jacobcolvin.com/v1 up-to-date=true`,
				`Tip "jar" tip/v1 up-to-date=true`,
			},
			warns: stringtest.JoinLF(
				`config/tip.yaml:2:7: warning: $.kind: deprecated kind "Tip"`,
				"`-- cafe 5.0 removes Tip",
				"",
				"   1 | apiVersion: tip/v1",
				"   2 | kind: Tip",
				"     |       ^^^",
				"   3 | metadata:",
				"   4 |   name: jar",
			),
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
				"deploy.yaml": stringtest.LinesLF("apiVersion: apps/v1", "kind: Deployment"),
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

			tc.opts = cafeOptions(tc.opts...)
			tc.run(t)
		})
	}
}

func TestRegistry_Load_Inputs(t *testing.T) {
	t.Parallel()

	reg := newCafeRegistry(t)

	cafe := niceyaml.NewSourceFromString(cafeDoc, niceyaml.WithName("cafe.yaml"))
	hours := niceyaml.NewSourceFromString(hoursDoc, niceyaml.WithName("hours.yaml"))
	nope := niceyaml.NewSourceFromString(stringtest.LinesLF("apiVersion: x/v1", "kind: Nope"),
		niceyaml.WithName("nope.yaml"))
	cafePath := yamltest.WriteFile(t, t.TempDir(), "config/cafe.yaml", cafeDoc)

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
		"directory and a file inside it": {
			in:   manifest.Inputs(manifest.Path(filepath.Dir(cafePath)), manifest.Path(cafePath)),
			want: []string{`Cafe "yamull" cafe.jacobcolvin.com/v1 up-to-date=true`},
		},
		"one source twice": {
			in:   manifest.Inputs(manifest.Sources(cafe), manifest.Sources(cafe)),
			want: []string{`Cafe "yamull" cafe.jacobcolvin.com/v1 up-to-date=true`},
		},
		"one file of a file system twice": {
			in: manifest.Inputs(
				configFS(map[string]string{"cafe.yaml": cafeDoc}),
				manifest.FS(mapFS(map[string]string{"cafe.yaml": cafeDoc}), "config/cafe.yaml"),
			),
			want: []string{
				`Cafe "yamull" cafe.jacobcolvin.com/v1 up-to-date=true`,
				`Cafe "yamull" cafe.jacobcolvin.com/v1 up-to-date=true`,
			},
			err: "more than one Cafe",
		},
		"zero input": {
			in: manifest.Input{},
			err: stringtest.JoinLF(
				"missing Cafe",
				"`-- add a document with apiVersion: cafe.jacobcolvin.com/v1 and kind: Cafe",
			),
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

	_, err := newCafeRegistry(t).Load(ctx, configFS(map[string]string{"cafe.yaml": cafeDoc}))
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

	tcs := map[string]loadCase{
		"plain document": {
			opts:  opts(),
			files: map[string]string{"cafe.yaml": "seats: 40\n"},
			want:  []string{`Cafe "" unversioned up-to-date=true`},
		},
		"plain document of the wrong layout": {
			opts:  opts(),
			files: map[string]string{"notes.yaml": "- a\n"},
			err: stringtest.JoinLF(
				"config/notes.yaml:1:3: $: expected mapping, got sequence",
				"|-- the document has no apiVersion and no kind, so cafe reads it as an unversioned Cafe",
				"`-- move the file out of config if it is not a Cafe",
				"",
				"   1 | - a",
				"     |   ^",
			),
		},
		"versioned document": {
			opts: opts(),
			files: map[string]string{"cafe.yaml": stringtest.LinesLF(
				"apiVersion: cafe.jacobcolvin.com/v1",
				"kind: Cafe",
				"seats: 1",
			)},
			want: []string{`Cafe "" cafe.jacobcolvin.com/v1 up-to-date=true`},
		},
		"claimed document with a version": {
			opts: opts(manifest.Claim(matcher.Exists(paths.Doc().Child("seats")))),
			files: map[string]string{
				"cafe.yaml": stringtest.LinesLF("apiVersion: cafe.jacobcolvin.com/v1", "seats: 1"),
			},
			want: []string{`Cafe "" cafe.jacobcolvin.com/v1 up-to-date=true`},
		},
		"removed unversioned layout": {
			opts: []manifest.RegistryOption{
				manifest.WithRemoved("", "Cafe", "cafe 7.0 reads only files with apiVersion and kind"),
				manifest.WithKinds(manifest.NewKind[CafeSpec]("Cafe", manifest.Version("cafe.jacobcolvin.com/v1"))),
			},
			files: map[string]string{"cafe.yaml": "seats: 40\n"},
			err: stringtest.JoinLF(
				"config/cafe.yaml:1:1: removed unversioned layout of Cafe",
				"`-- cafe 7.0 reads only files with apiVersion and kind",
				"",
				"   1 | seats: 40",
				"     | ^^^^^",
			),
		},
		"version only": {
			opts: []manifest.RegistryOption{
				manifest.WithAppName("cafe"),
				manifest.WithVersionOnly(paths.Doc().Child("version")),
				manifest.WithKinds(manifest.NewKind[CafeSpec]("Config",
					manifest.Version("1"), manifest.Version("2"))),
			},
			files: map[string]string{"a.yaml": stringtest.LinesLF("version: 2", "seats: 3"), "b.yaml": "version: 3\n"},
			err: stringtest.JoinLF(
				`config/b.yaml:1:10: $.version: unknown version "3" for Config`,
				"|-- cafe reads version 1 and version 2",
				"`-- a newer release of cafe might read this version",
				"",
				"   1 | version: 3",
				"     |          ^",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tc.run(t)
		})
	}
}

func TestRegistry_Load_SchemaLoad(t *testing.T) {
	t.Parallel()

	reg := manifest.MustNewRegistry(manifest.WithKinds(manifest.NewKind[CafeSpec]("Cafe",
		manifest.Version("v1", manifest.Schema(schema.Embedded([]byte(`{"type": 5}`)))),
	)))

	src := niceyaml.NewSourceFromString(
		stringtest.LinesLF("apiVersion: v1", "kind: Cafe"),
		niceyaml.WithName("cafe.yaml"),
	)

	_, err := reg.Load(t.Context(), manifest.Sources(src))
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
			err: stringtest.JoinLF(
				"config/menu.yaml:15:10: $.spec.price: price is more than 50 kr",
				"",
				"  13 |   name: semla",
				"  14 | spec:",
				"  15 |   price: 65",
				"     |          ^^",
				"  16 |   tags: [seasonal]",
				"  17 |   legacyPricing: true",
			),
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
				cafe, ok := set.Get[Cafe]("yamull")
				require.True(t, ok)

				return cafe.Bind(niceyaml.NewWarning("small cafe",
					niceyaml.AtPath(paths.Doc().Child("spec", "seats"))))
			},
			warns: stringtest.JoinLF(
				"config/cafe.yaml:6:10: warning: $.spec.seats: small cafe",
				"",
				"   4 |   name: yamull",
				"   5 | spec:",
				"   6 |   seats: 40",
				"     |          ^^",
			),
		},
		"panic": {
			check: func(context.Context, *manifest.Set) error { panic("boom") },
			err:   "config: check panicked: boom",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reg := newCafeRegistry(t, manifest.WithCheck(tc.check))

			set, err := reg.Load(t.Context(),
				configFS(map[string]string{"cafe.yaml": cafeDoc, "menu.yaml": menuDoc}))
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

		set, err := reg.Load(t.Context(), configFS(map[string]string{"cafe.yaml": "---\n---\n" + cafeDoc}))
		require.NoError(t, err)

		w, ok := errors.AsType[*manifest.Warning](set.Warnings())
		require.True(t, ok)
		assert.Equal(t, "Cafe cafe.jacobcolvin.com/v1", w.Kind+" "+w.Version)
	})
}

// deprecationOptions returns the options of a registry whose schema of
// each version of MenuItem deprecates a field: cost at v1beta1, and
// legacyPricing at v1.
func deprecationOptions() []manifest.RegistryOption {
	older := `{"type": "object", "properties": {"spec": {"type": "object", "properties": {
  "cost": {"deprecated": true, "deprecationMessage": "write price"}}}}}`
	current := `{"type": "object", "properties": {"spec": {"type": "object", "properties": {
  "price": {"type": "integer", "minimum": 1},
  "legacyPricing": {"deprecated": true, "deprecationMessage": "legacyPricing has no effect. Remove it."}}}}}`

	return []manifest.RegistryOption{
		manifest.WithNameField(meta.Child("name")),
		manifest.WithAppName("cafe"),
		manifest.WithKinds(manifest.NewKind[MenuItem]("MenuItem",
			manifest.Version("cafe.jacobcolvin.com/v1beta1",
				manifest.KindName("Dish"),
				manifest.Schema(schema.Embedded([]byte(older))),
			),
			manifest.Version("cafe.jacobcolvin.com/v1",
				manifest.Written(),
				manifest.Schema(schema.Embedded([]byte(current))),
				manifest.Migrate(spec.Child("cost"), rename("price")),
			),
		)),
	}
}

func TestRegistry_Load_Deprecations(t *testing.T) {
	t.Parallel()

	reg := manifest.MustNewRegistry(deprecationOptions()...)

	set, err := reg.Load(t.Context(), configFS(map[string]string{"menu.yaml": deprecatedMenu}))
	require.NoError(t, err)

	// The schema of v1beta1 deprecates cost, but only the schema of the
	// current version warns.
	assert.Equal(t, stringtest.JoinLF(
		"config: 2 warnings",
		`|-- config/menu.yaml:7:3: warning: $.spec.legacyPricing~: deprecated field "legacyPricing"`,
		"|   `-- legacyPricing has no effect. Remove it.",
		"`-- config/menu.yaml:15:3: warning: $.spec.legacyPricing~: deprecated field \"legacyPricing\"",
		"",
		"config/menu.yaml",
		"   5 | spec:",
		"   6 |   cost: 45",
		"   7 |   legacyPricing: true",
		`     |   ^^^^^^^^^^^^^ warning: deprecated field "legacyPricing"`,
		"   8 | ---",
		"   9 | apiVersion: cafe.jacobcolvin.com/v1",
	),
		niceyaml.FormatError(set.Warnings()))

	var rows []string

	for problem := range niceyaml.Problems(set.Warnings()) {
		require.ErrorIs(t, problem.Err, manifest.ErrDeprecated)

		w, ok := errors.AsType[*manifest.Warning](problem.Err)
		require.True(t, ok)

		rows = append(rows, fmt.Sprintf("%s %s %s %s", w.File, w.Kind, w.Version, w.Path))
	}

	assert.Equal(t, []string{
		"config/menu.yaml MenuItem cafe.jacobcolvin.com/v1beta1 $.spec.legacyPricing~",
		"config/menu.yaml MenuItem cafe.jacobcolvin.com/v1 $.spec.legacyPricing~",
	}, rows)

	// A document that fails the schema reports its deprecations beside its
	// violations.
	_, err = reg.Load(t.Context(), configFS(map[string]string{"menu.yaml": stringtest.LinesLF(
		"apiVersion: cafe.jacobcolvin.com/v1",
		"kind: MenuItem",
		"metadata:",
		"  name: semla",
		"spec:",
		"  price: 0",
		"  legacyPricing: true",
	)}))
	require.Error(t, err)
	assert.Equal(t, stringtest.JoinLF(
		"config: 1 error, 1 warning",
		"config/menu.yaml:6:10: $.spec.price: 0 is less than 1",
		`config/menu.yaml:7:3: warning: $.spec.legacyPricing~: deprecated field "legacyPricing"`,
	), err.Error())
}

func TestRegistry_Load_Defaults(t *testing.T) {
	t.Parallel()

	reg := manifest.MustNewRegistry(
		manifest.WithNameField(meta.Child("name")),
		manifest.WithDecodeOptions(niceyaml.WithDefaults(func(s *CafeSpec) {
			s.Seats = 20
			s.Currency = "SEK"
		})),
		manifest.WithKinds(manifest.NewKind[Cafe]("Cafe",
			manifest.DecodeOptions(niceyaml.WithDefaults(func(s *CafeSpec) { s.Currency = "EUR" })),
			manifest.Version("cafe.jacobcolvin.com/v1"),
		)),
	)

	set, err := reg.Load(t.Context(), configFS(map[string]string{"cafe.yaml": cafeDoc}))
	require.NoError(t, err)

	// The defaults of the kind replace those of the registry for one type.
	cafe, ok := set.Get[Cafe]("yamull")
	require.True(t, ok)
	assert.Equal(t, CafeSpec{Seats: 40, Currency: "EUR"}, cafe.Value.Spec)
}

func TestLoadError(t *testing.T) {
	t.Parallel()

	reg := newCafeRegistry(t)

	_, err := reg.Load(t.Context(), configFS(map[string]string{
		"cafe.yaml":     cafeDoc,
		"hours.yaml":    hoursDoc,
		"menu.yaml":     strings.Replace(menuDoc, "price: 65", "price: 0", 1),
		"specials.yaml": specialsDoc,
	}))
	require.Error(t, err)

	assert.Equal(t, stringtest.JoinLF(
		"config: 2 errors, 1 warning",
		`config/hours.yaml:1:13: warning: $.apiVersion: deprecated version "cafe.jacobcolvin.com/v1alpha1"`,
		"config/menu.yaml:15:10: $.spec.price: 0 is less than 1",
		`config/specials.yaml:2:7: $.kind: unknown kind "Menuitem"`,
	), err.Error())

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
		stringtest.LinesLF(
			"apiVersion: cafe.jacobcolvin.com/v1alpha1",
			"kind: Hours",
			"metadata:",
			"  name: weekday",
		),
		niceyaml.WithName("config/base/hours.yaml"),
	)

	tcs := map[string]struct {
		top  string
		want []string
		err  string
	}{
		"layers agree": {
			top:  stringtest.LinesLF("spec:", `  opens: "08:00"`),
			want: []string{`Hours "weekday" cafe.jacobcolvin.com/v1alpha1 up-to-date=false`},
		},
		"layers declare different versions": {
			top: "apiVersion: cafe.jacobcolvin.com/v1\n",
			err: stringtest.JoinLF(
				"config/base/hours.yaml:1:13: $.apiVersion: layers declare different versions",
				"`-- config/prod/hours.yaml:1:13: $.apiVersion: cafe.jacobcolvin.com/v1 overrides cafe.jacobcolvin.com/v1alpha1",
			),
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
				assert.Equal(t, tc.err, firstTwoLines(niceyaml.FormatError(err)))

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, listObjects(set))
		})
	}
}

func TestRegistry_Load_Hint(t *testing.T) {
	t.Parallel()

	hint := "`-- " + migrateHint

	tcs := map[string]struct {
		opts  []manifest.RegistryOption
		files map[string]string
		// Err is the error of the load, whose formatted text the case
		// reads in place of the warnings.
		err error
		// Want is text that the formatted warnings or error hold.
		want string
		// Hint reports whether the problems end their details with the
		// migrate hint.
		hint bool
	}{
		"deprecated version": {
			opts:  cafeOptions(),
			files: map[string]string{"hours.yaml": hoursDoc},
			want:  stringtest.LinesLF("|-- cafe 4.0 removes cafe.jacobcolvin.com/v1alpha1", hint),
			hint:  true,
		},
		"removed version that the kind still lists": {
			opts:  cafeOptions(tabOptions()...),
			files: map[string]string{"tab.yaml": tabDoc},
			err:   manifest.ErrRemovedVersion,
			want:  stringtest.LinesLF(`$.apiVersion: removed version "v0" of Tab`, hint),
			hint:  true,
		},
		"removal by the written version": {
			opts:  drinkOptions(true),
			files: map[string]string{"drink.yaml": colorDrinkDoc},
			want:  stringtest.LinesLF("|-- menu colors moved to the theme of the Cafe", hint),
			hint:  true,
		},
		"removal by a version newer than the written one": {
			opts:  drinkOptions(false),
			files: map[string]string{"drink.yaml": colorDrinkDoc},
			want:  "`-- menu colors moved to the theme of the Cafe\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			config := filepath.Join(t.TempDir(), "config")
			yamltest.WriteFiles(t, config, tc.files)

			set, err := manifest.MustNewRegistry(tc.opts...).Load(t.Context(), manifest.Path(config),
				manifest.WithSetChecks(false))

			var got string

			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				got = niceyaml.FormatError(err)
			} else {
				require.NoError(t, err)

				got = niceyaml.FormatError(set.Warnings())
			}

			assert.Contains(t, got, tc.want)

			if tc.hint {
				assert.Contains(t, got, hint)
			} else {
				assert.NotContains(t, got, hint)
			}
		})
	}
}

// firstTwoLines returns the first two lines of s: the headline of a
// problem and its first detail.
func firstTwoLines(s string) string {
	lines := strings.SplitN(s, "\n", 3)

	return strings.Join(lines[:min(2, len(lines))], "\n")
}

// Shift is a kind whose spec checks its hours.
type Shift struct {
	Spec ShiftSpec `json:"spec"`
}

// ShiftSpec holds the hours of a shift, which must not close before it
// opens, and warns about a shift that opens before six.
type ShiftSpec struct {
	Open  string `json:"open,omitzero"`
	Close string `json:"close,omitzero"`
}

// Validate reports a shift that closes before it opens, where a shift
// without a close closes at 17:00, and warns about a shift that opens
// before six.
func (s ShiftSpec) Validate() error {
	var errs []error

	if s.Open != "" && s.Open < "06:00" {
		errs = append(errs, niceyaml.NewWarning("opens before six", niceyaml.AtPath(paths.Current().Child("open"))))
	}

	closes := s.Close
	if closes == "" {
		closes = "17:00"
	}

	if closes < s.Open {
		errs = append(errs, niceyaml.NewError(fmt.Sprintf("closes at %s, before it opens at %s", closes, s.Open),
			niceyaml.AtPath(paths.Current().Child("close"))))
	}

	return errors.Join(errs...)
}

func TestRegistry_Load_Warnings(t *testing.T) {
	t.Parallel()

	// The validator warns about the open field of the spec of a Shift.
	openWarning := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
		n.Warn(ctx, niceyaml.NewWarning("open moves to hours", niceyaml.AtPath(spec.Child("open"))))

		return nil
	})

	// Each registry holds the kind Shift, with extra on the kind and its
	// versions after it.
	shiftOptions := func(extra ...manifest.KindOption) []manifest.RegistryOption {
		return []manifest.RegistryOption{
			manifest.WithAppName("cafe"),
			manifest.WithKinds(manifest.NewKind[Shift]("Shift", extra...)),
		}
	}
	v1 := manifest.Version("cafe.jacobcolvin.com/v1")

	shift := func(version, open, closes string) string {
		return stringtest.LinesLF(
			"apiVersion: "+version,
			"kind: Shift",
			"spec:",
			fmt.Sprintf("  open: %q", open),
			fmt.Sprintf("  close: %q", closes),
		)
	}

	// Each case loads shift.yaml, and the load reports the warnings of
	// each check that the case runs.
	tcs := map[string]struct {
		opts  []manifest.RegistryOption
		file  string
		err   string
		warns string
	}{
		"a Validate method warns in a document that passes": {
			opts:  shiftOptions(v1),
			file:  shift("cafe.jacobcolvin.com/v1", "05:00", "12:00"),
			warns: "config/shift.yaml:4:9: warning: $.spec.open: opens before six",
		},
		"a Validate method warns in a document that fails": {
			opts: shiftOptions(v1),
			file: shift("cafe.jacobcolvin.com/v1", "05:00", "04:00"),
			err: stringtest.JoinLF(
				"config: 1 error, 1 warning",
				"config/shift.yaml:4:9: warning: $.spec.open: opens before six",
				"config/shift.yaml:5:10: $.spec.close: closes at 04:00, before it opens at 05:00",
			),
		},
		"a claim whose match warns": {
			opts: shiftOptions(
				manifest.Claim(matcher.Func(func(ctx context.Context, doc *niceyaml.Node) (bool, error) {
					return true, doc.Validate(ctx, openWarning)
				})),
				manifest.Unversioned(),
				v1,
			),
			file:  stringtest.LinesLF("spec:", `  open: "07:00"`),
			warns: "config/shift.yaml:2:9: warning: $.spec.open: open moves to hours",
		},
		"a step whose decode warns": {
			opts: shiftOptions(
				manifest.Version("cafe.jacobcolvin.com/v1alpha1"),
				manifest.Version("cafe.jacobcolvin.com/v1",
					manifest.Migrate(spec.Child("open"), func(ctx context.Context, n *migrate.Node) error {
						_, err := n.Decode[string](ctx, niceyaml.WithValidator(openWarning))

						return err //nolint:wrapcheck // The load binds the error of a step.
					}),
				),
			),
			file:  shift("cafe.jacobcolvin.com/v1alpha1", "07:00", "12:00"),
			warns: "config/shift.yaml:4:9: warning: $.spec.open: open moves to hours",
		},
		"a check that runs a validator with its context": {
			opts: append(shiftOptions(v1), manifest.WithCheck(func(ctx context.Context, set *manifest.Set) error {
				var errs []error

				for o := range set.All[Shift]() {
					errs = append(errs, o.Node.Validate(ctx, openWarning))
				}

				return errors.Join(errs...)
			})),
			file:  shift("cafe.jacobcolvin.com/v1", "07:00", "12:00"),
			warns: "config/shift.yaml:4:9: warning: $.spec.open: open moves to hours",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reg := manifest.MustNewRegistry(tc.opts...)

			set, err := reg.Load(t.Context(), configFS(map[string]string{"shift.yaml": tc.file}))
			if tc.err != "" {
				require.EqualError(t, err, tc.err)

				return
			}

			require.NoError(t, err)
			require.Error(t, set.Warnings())
			assert.Equal(t, tc.warns, set.Warnings().Error())
		})
	}
}

func TestRegistry_Load_LeftOut(t *testing.T) {
	t.Parallel()

	reg := manifest.MustNewRegistry(
		manifest.WithAppName("cafe"),
		manifest.WithKinds(manifest.NewKind[Shift]("Shift",
			manifest.Version("cafe.jacobcolvin.com/v1alpha1"),
			manifest.Version("cafe.jacobcolvin.com/v1", manifest.Migrate(spec.Child("opens"), rename("open"))),
		)),
	)

	src := niceyaml.NewSourceFromString(
		stringtest.LinesLF(
			"apiVersion: cafe.jacobcolvin.com/v1alpha1",
			"kind: Shift",
			"spec:",
			`  opens: "18:00"`,
		),
		niceyaml.WithName("hours.yaml"),
	)

	_, err := reg.Load(t.Context(), manifest.Sources(src))
	require.Error(t, err)
	assert.Equal(t, stringtest.JoinLF(
		"hours.yaml:3:1: $.spec.close: closes at 17:00, before it opens at 18:00",
		"`-- the file leaves out the field that cafe.jacobcolvin.com/v1 names close, so it holds its default",
	),
		firstTwoLines(niceyaml.FormatError(err)))
}
