package manifest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/edit"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/manifest"
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

	// The error of an alarm level that a document misspells.
	errUnknownAlarmLevel = errors.New("unknown level")
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
		"unknown field and a rejected value beside the type fields": {
			files: map[string]string{
				"cafe.yaml": stringtest.LinesLF(
					"apiVersion: cafe.jacobcolvin.com/v1",
					"kind: Cafe",
					"metadata:",
					"  name: yamull",
					"spec:",
					"  seats: many",
					"wifi: open",
				),
			},
			err: stringtest.JoinLF(
				"config: 2 errors",
				"|-- config/cafe.yaml:6:10: $.spec.seats: expected integer, got string",
				"`-- config/cafe.yaml:7:1: $.wifi~: unknown field \"wifi\"",
				"",
				"config/cafe.yaml",
				"   4 |   name: yamull",
				"   5 | spec:",
				"   6 |   seats: many",
				"     |          ^^^^ expected integer, got string",
				"   7 | wifi: open",
				"     | ^^^^ unknown field \"wifi\"",
			),
			matches: []error{niceyaml.ErrUnknownField},
		},
		"flow mapping with type fields": {
			files: map[string]string{
				"cafe.yaml": "{apiVersion: cafe.jacobcolvin.com/v1, kind: Cafe, metadata: {name: yamull}}\n",
			},
			want: []string{`Cafe "yamull" cafe.jacobcolvin.com/v1 up-to-date=true`},
		},
		"anchored mapping with type fields": {
			files: map[string]string{
				"cafe.yaml": stringtest.LinesLF(
					"&cafe",
					"apiVersion: cafe.jacobcolvin.com/v1",
					"kind: Cafe",
					"metadata:",
					"  name: yamull",
				),
			},
			want: []string{`Cafe "yamull" cafe.jacobcolvin.com/v1 up-to-date=true`},
		},
		"tagged mapping with type fields": {
			files: map[string]string{
				"cafe.yaml": stringtest.LinesLF(
					"!!map",
					"apiVersion: cafe.jacobcolvin.com/v1",
					"kind: Cafe",
					"metadata:",
					"  name: yamull",
				),
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
		"named heading of a lone reader": {
			in:  manifest.Reader("body.yaml", strings.NewReader("")),
			err: "body.yaml: missing Cafe",
		},
		"path of a dash": {
			in:  manifest.Path("-"),
			err: "-: " + notExistReason(t),
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

func TestRegistry_Check(t *testing.T) {
	t.Parallel()

	errPricey := errors.New("pricey")

	// A check that binds an error in the MenuItem kanelbulle through its
	// Object. Kanelbulle migrates, so the error tests the bind of a
	// migrated document.
	failKanelbulle := manifest.WithCheck(func(_ context.Context, set *manifest.Set) error {
		item, ok := set.Get[MenuItem]("kanelbulle")
		if !ok {
			return errors.New("no kanelbulle")
		}

		return item.Bind(niceyaml.NewError("too cheap", niceyaml.AtPath(spec.Child("price"))))
	})

	menuExtra := stringtest.LinesLF(
		"apiVersion: cafe.jacobcolvin.com/v1beta1",
		"kind: Dish",
		"metadata:",
		"  name: semla",
	)

	// The opts of each case come after those of [cafeOptions]. The err and
	// warns fields hold the first line of each error.
	tcs := map[string]struct {
		files   map[string]string
		opts    []manifest.RegistryOption
		load    []manifest.LoadOption
		want    []string
		warns   string
		err     string
		matches []error
	}{
		"passing load": {
			files: exampleFiles,
			want: []string{
				`Cafe "yamull" cafe.jacobcolvin.com/v1 up-to-date=true`,
				`Hours "weekday" cafe.jacobcolvin.com/v1alpha1 up-to-date=false`,
				`MenuItem "kanelbulle" cafe.jacobcolvin.com/v1beta1 up-to-date=false`,
				`MenuItem "semla" cafe.jacobcolvin.com/v1 up-to-date=true`,
			},
			warns:   `config/hours.yaml:1:13: warning: $.apiVersion: deprecated version "cafe.jacobcolvin.com/v1alpha1"`,
			matches: []error{manifest.ErrDeprecated},
		},
		"failing document beside a warning": {
			files: map[string]string{"cafe.yaml": cafeDoc, "hours.yaml": hoursDoc, "specials.yaml": specialsDoc},
			want: []string{
				`Cafe "yamull" cafe.jacobcolvin.com/v1 up-to-date=true`,
				`Hours "weekday" cafe.jacobcolvin.com/v1alpha1 up-to-date=false`,
			},
			err:     "config: 1 error, 1 warning",
			matches: []error{manifest.ErrUnknownKind, manifest.ErrDeprecated},
		},
		"schema violation in a second document": {
			files: map[string]string{
				"cafe.yaml": cafeDoc,
				"menu.yaml": strings.Replace(menuDoc, "price: 65", "price: 0", 1),
			},
			want: []string{
				`Cafe "yamull" cafe.jacobcolvin.com/v1 up-to-date=true`,
				`MenuItem "kanelbulle" cafe.jacobcolvin.com/v1beta1 up-to-date=false`,
			},
			err: "config/menu.yaml:15:10: $.spec.price: 0 is less than 1",
		},
		"duplicate name": {
			files: map[string]string{"cafe.yaml": cafeDoc, "menu-extra.yaml": menuExtra, "menu.yaml": menuDoc},
			want: []string{
				`Cafe "yamull" cafe.jacobcolvin.com/v1 up-to-date=true`,
				`MenuItem "semla" cafe.jacobcolvin.com/v1beta1 up-to-date=false`,
				`MenuItem "kanelbulle" cafe.jacobcolvin.com/v1beta1 up-to-date=false`,
			},
			err:     `config/menu.yaml:13:9: $.metadata.name: duplicate MenuItem "semla"`,
			matches: []error{manifest.ErrDuplicateName},
		},
		"second singleton": {
			files: map[string]string{
				"cafe-copy.yaml": strings.Replace(cafeDoc, "yamull", "copy", 1),
				"cafe.yaml":      cafeDoc,
			},
			want:    []string{`Cafe "copy" cafe.jacobcolvin.com/v1 up-to-date=true`},
			err:     "config/cafe.yaml:2:7: $.kind: more than one Cafe",
			matches: []error{manifest.ErrExtraManifest},
		},
		"missing required kind": {
			files: map[string]string{"menu.yaml": menuDoc},
			want: []string{
				`MenuItem "kanelbulle" cafe.jacobcolvin.com/v1beta1 up-to-date=false`,
				`MenuItem "semla" cafe.jacobcolvin.com/v1 up-to-date=true`,
			},
			err:     "config: missing Cafe",
			matches: []error{manifest.ErrMissingManifest},
		},
		"strict mode": {
			files:   map[string]string{"cafe.yaml": cafeDoc, "hours.yaml": hoursDoc},
			load:    []manifest.LoadOption{manifest.WithWarningsAsErrors(true)},
			want:    []string{`Cafe "yamull" cafe.jacobcolvin.com/v1 up-to-date=true`},
			err:     `config/hours.yaml:1:13: $.apiVersion: deprecated version "cafe.jacobcolvin.com/v1alpha1"`,
			matches: []error{manifest.ErrDeprecated},
		},
		"strict mode with every warning dropped": {
			files: map[string]string{"cafe.yaml": cafeDoc, "hours.yaml": hoursDoc},
			load: []manifest.LoadOption{
				manifest.WithWarningsAsErrors(true),
				manifest.WithWarningFilter(func(*manifest.Warning) bool { return false }),
			},
			want: []string{
				`Cafe "yamull" cafe.jacobcolvin.com/v1 up-to-date=true`,
				`Hours "weekday" cafe.jacobcolvin.com/v1alpha1 up-to-date=false`,
			},
		},
		"set checks off": {
			files: map[string]string{
				"cafe-copy.yaml": cafeDoc,
				"cafe.yaml":      cafeDoc,
				"menu.yaml":      strings.Replace(menuDoc, "price: 65", "price: 0", 1),
			},
			load: []manifest.LoadOption{manifest.WithSetChecks(false)},
			want: []string{
				`Cafe "yamull" cafe.jacobcolvin.com/v1 up-to-date=true`,
				`Cafe "yamull" cafe.jacobcolvin.com/v1 up-to-date=true`,
				`MenuItem "kanelbulle" cafe.jacobcolvin.com/v1beta1 up-to-date=false`,
			},
			err: "config/menu.yaml:15:10: $.spec.price: 0 is less than 1",
		},
		"check error bound through an object": {
			files: map[string]string{"cafe.yaml": cafeDoc, "menu.yaml": menuDoc},
			opts:  []manifest.RegistryOption{failKanelbulle},
			want: []string{
				`Cafe "yamull" cafe.jacobcolvin.com/v1 up-to-date=true`,
				`MenuItem "semla" cafe.jacobcolvin.com/v1 up-to-date=true`,
			},
			err: "config/menu.yaml:7:9: $.spec.cost: too cheap",
		},
		"check error bound in no document": {
			files: map[string]string{"cafe.yaml": cafeDoc, "menu.yaml": menuDoc},
			opts: []manifest.RegistryOption{manifest.WithCheck(func(context.Context, *manifest.Set) error {
				return errPricey
			})},
			want: []string{
				`Cafe "yamull" cafe.jacobcolvin.com/v1 up-to-date=true`,
				`MenuItem "kanelbulle" cafe.jacobcolvin.com/v1beta1 up-to-date=false`,
				`MenuItem "semla" cafe.jacobcolvin.com/v1 up-to-date=true`,
			},
			err:     "config: pricey",
			matches: []error{errPricey},
		},
		"check error bound in a whole file": {
			files: map[string]string{"cafe.yaml": cafeDoc, "menu.yaml": menuDoc},
			opts: []manifest.RegistryOption{manifest.WithCheck(func(_ context.Context, set *manifest.Set) error {
				semla, ok := set.Get[MenuItem]("semla")
				if !ok {
					return errors.New("no semla")
				}

				return semla.Node.Source().Bind(errors.New("whole file"))
			})},
			want: []string{
				`Cafe "yamull" cafe.jacobcolvin.com/v1 up-to-date=true`,
				`MenuItem "kanelbulle" cafe.jacobcolvin.com/v1beta1 up-to-date=false`,
				`MenuItem "semla" cafe.jacobcolvin.com/v1 up-to-date=true`,
			},
			err: "config/menu.yaml: whole file",
		},
		"failing document skips the checks of the set": {
			files: map[string]string{"cafe.yaml": cafeDoc, "menu.yaml": menuDoc, "specials.yaml": specialsDoc},
			opts:  []manifest.RegistryOption{failKanelbulle},
			want: []string{
				`Cafe "yamull" cafe.jacobcolvin.com/v1 up-to-date=true`,
				`MenuItem "kanelbulle" cafe.jacobcolvin.com/v1beta1 up-to-date=false`,
				`MenuItem "semla" cafe.jacobcolvin.com/v1 up-to-date=true`,
			},
			err:     `config/specials.yaml:2:7: $.kind: unknown kind "Menuitem"`,
			matches: []error{manifest.ErrUnknownKind},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reg := newCafeRegistry(t, tc.opts...)
			in := configFS(tc.files)

			checked, err := reg.Check(t.Context(), in, tc.load...)
			require.NoError(t, err)
			assert.Equal(t, tc.want, listObjects(checked.Set()))
			assert.Equal(t, checked.Warnings(), checked.Set().Warnings())

			set, loadErr := reg.Load(t.Context(), in, tc.load...)

			if tc.err != "" {
				require.ErrorAs(t, checked.Err(), new(*manifest.LoadError))
				assert.Equal(t, tc.err, firstLine(niceyaml.FormatError(checked.Err())))
				require.NoError(t, checked.Warnings())

				for _, m := range tc.matches {
					require.ErrorIs(t, checked.Err(), m)
				}

				// Load returns the same error, and no set.
				require.Error(t, loadErr)
				assert.Nil(t, set)
				assert.Equal(t, niceyaml.FormatError(checked.Err()), niceyaml.FormatError(loadErr))

				return
			}

			require.NoError(t, checked.Err())
			assert.Equal(t, tc.warns, firstLine(niceyaml.FormatError(checked.Warnings())))

			for _, m := range tc.matches {
				require.ErrorIs(t, checked.Warnings(), m)
			}

			require.NoError(t, loadErr)
			assert.Equal(t, tc.want, listObjects(set))
		})
	}
}

func TestRegistry_Check_Fails(t *testing.T) {
	t.Parallel()

	files := map[string]string{"cafe.yaml": cafeDoc}

	tcs := map[string]struct {
		in     manifest.Input
		err    error
		cancel bool
	}{
		"ended context": {
			in:     configFS(files),
			cancel: true,
			err:    context.Canceled,
		},
		"bad pattern": {
			in:  configFS(files, manifest.WithExclude("[")),
			err: matcher.ErrInvalidPattern,
		},
		"input path that does not exist": {
			in:  manifest.Path(filepath.Join(t.TempDir(), "confg")),
			err: fs.ErrNotExist,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(t.Context())
			if tc.cancel {
				cancel()
			}

			defer cancel()

			checked, err := newCafeRegistry(t).Check(ctx, tc.in)
			require.ErrorIs(t, err, tc.err)
			assert.Nil(t, checked)
		})
	}
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

func TestRegistry_Load_UnknownFieldsWarn(t *testing.T) {
	t.Parallel()

	// The load warns about the unknown key and still admits the type
	// fields, which the Go type does not declare.
	reg := newCafeRegistry(t, manifest.WithDecodeOptions(niceyaml.WithUnknownFields(niceyaml.UnknownFieldsWarn)))

	set, err := reg.Load(t.Context(), configFS(map[string]string{"cafe.yaml": stringtest.LinesLF(
		"apiVersion: cafe.jacobcolvin.com/v1",
		"kind: Cafe",
		"metadata:",
		"  name: yamull",
		"wifi: open",
	)}))
	require.NoError(t, err)

	warnings := set.Warnings()
	require.ErrorIs(t, warnings, niceyaml.ErrUnknownField)
	require.EqualError(t, warnings, `config/cafe.yaml:5:1: warning: $.wifi~: unknown field "wifi"`)

	_, ok := set.Get[Cafe]("yamull")
	assert.True(t, ok)
}

// Alarm is a kind whose level a document writes by name, which a
// function from niceyaml.CustomUnmarshaler reads.
type Alarm struct {
	Metadata Metadata  `json:"metadata"`
	Spec     AlarmSpec `json:"spec"`
}

// AlarmSpec describes an alarm.
type AlarmSpec struct {
	Level AlarmLevel `json:"level"`
}

// AlarmLevel is the level of an alarm. It has no method of its own.
type AlarmLevel int

// The levels of an alarm.
const (
	alarmLow AlarmLevel = iota + 1
	alarmHigh
)

// decodeAlarmLevel decodes an [AlarmLevel] from its name.
func decodeAlarmLevel(_ context.Context, l *AlarmLevel, decode func(any) error) error {
	var name string

	err := decode(&name)
	if err != nil {
		return err
	}

	switch name {
	case "low":
		*l = alarmLow
	case "high":
		*l = alarmHigh
	default:
		return fmt.Errorf("%w %q", errUnknownAlarmLevel, name)
	}

	return nil
}

func TestRegistry_Load_Unmarshalers(t *testing.T) {
	t.Parallel()

	levelNames := niceyaml.CustomUnmarshaler(decodeAlarmLevel)

	seven := niceyaml.CustomUnmarshaler(func(_ context.Context, l *AlarmLevel, _ func(any) error) error {
		*l = 7

		return nil
	})

	levelPath := paths.Current().Child("spec", "level")

	// Neither the validator nor the step passes an option to its decode,
	// so each reads a level name only through the unmarshalers of the
	// load.
	options := func(us []niceyaml.Unmarshaler, decodeOpts []niceyaml.DecodeOption) []manifest.RegistryOption {
		readLevel := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
			_, err := n.DecodeAt[AlarmLevel](ctx, levelPath)

			return err
		})

		return []manifest.RegistryOption{
			manifest.WithNameField(meta.Child("name")),
			manifest.WithUnmarshalers(us...),
			manifest.WithDecodeOptions(niceyaml.WithValidator(readLevel)),
			manifest.WithDecodeOptions(decodeOpts...),
			manifest.WithKinds(manifest.NewKind[Alarm]("Alarm",
				manifest.Version("alarm.jacobcolvin.com/v1alpha1"),
				manifest.Version("alarm.jacobcolvin.com/v1",
					manifest.Migrate(spec.Child("severity"), func(ctx context.Context, n *edit.Node) error {
						_, err := n.Decode[AlarmLevel](ctx)
						if err != nil {
							return err
						}

						return n.SetKey("level")
					}),
				),
			)),
		}
	}

	current := stringtest.LinesLF(
		"apiVersion: alarm.jacobcolvin.com/v1",
		"kind: Alarm",
		"metadata:",
		"  name: smoke",
		"spec:",
		"  level: high",
	)

	older := stringtest.LinesLF(
		"apiVersion: alarm.jacobcolvin.com/v1alpha1",
		"kind: Alarm",
		"metadata:",
		"  name: smoke",
		"spec:",
		"  severity: high",
	)

	tcs := map[string]struct {
		in         manifest.Input
		us         []niceyaml.Unmarshaler
		decodeOpts []niceyaml.DecodeOption
		err        string
		want       AlarmLevel
	}{
		"a file the load reads": {
			in:   configFS(map[string]string{"alarm.yaml": current}),
			us:   []niceyaml.Unmarshaler{levelNames},
			want: alarmHigh,
		},
		"a Source the program built": {
			in:   manifest.Sources(niceyaml.NewSourceFromString(current, niceyaml.WithName("alarm.yaml"))),
			us:   []niceyaml.Unmarshaler{levelNames},
			want: alarmHigh,
		},
		"the step of a file the load reads": {
			in:   configFS(map[string]string{"alarm.yaml": older}),
			us:   []niceyaml.Unmarshaler{levelNames},
			want: alarmHigh,
		},
		"the step of a Source the program built": {
			in:   manifest.Sources(niceyaml.NewSourceFromString(older, niceyaml.WithName("alarm.yaml"))),
			us:   []niceyaml.Unmarshaler{levelNames},
			want: alarmHigh,
		},
		"the source options of an input apply over them": {
			in: configFS(map[string]string{"alarm.yaml": current},
				manifest.WithSourceOptions(niceyaml.WithUnmarshalers(seven))),
			us:   []niceyaml.Unmarshaler{levelNames},
			want: 7,
		},
		"the unmarshalers of a Source the program built apply over them": {
			in: manifest.Sources(niceyaml.NewSourceFromString(current,
				niceyaml.WithName("alarm.yaml"), niceyaml.WithUnmarshalers(seven))),
			us:   []niceyaml.Unmarshaler{levelNames},
			want: 7,
		},
		"a later function for a type replaces an earlier one": {
			in:   configFS(map[string]string{"alarm.yaml": current}),
			us:   []niceyaml.Unmarshaler{levelNames, seven},
			want: 7,
		},
		"the decode options of the registry apply over them": {
			in:         configFS(map[string]string{"alarm.yaml": current}),
			us:         []niceyaml.Unmarshaler{levelNames},
			decodeOpts: []niceyaml.DecodeOption{niceyaml.WithUnmarshaler(seven)},
			want:       7,
		},
		"without them, a level reads as an integer": {
			in:  configFS(map[string]string{"alarm.yaml": current}),
			err: "config/alarm.yaml:6:10: $.spec.level: expected integer, got string",
		},
		"without them, the step reads a level as an integer": {
			in:  manifest.Sources(niceyaml.NewSourceFromString(older, niceyaml.WithName("alarm.yaml"))),
			err: "alarm.yaml:6:13: $.spec.severity: expected integer, got string",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reg, err := manifest.NewRegistry(options(tc.us, tc.decodeOpts)...)
			require.NoError(t, err)

			set, err := reg.Load(t.Context(), tc.in)
			if tc.err != "" {
				require.Error(t, err)
				assert.Contains(t, niceyaml.FormatError(err), tc.err)

				return
			}

			require.NoError(t, err)

			alarm, ok := set.Get[Alarm]("smoke")
			require.True(t, ok)
			assert.Equal(t, tc.want, alarm.Value.Spec.Level)
		})
	}
}

func TestRegistry_Load_AliasExpansion(t *testing.T) {
	t.Parallel()

	// The level reads text through a function from
	// niceyaml.CustomUnmarshaler. The decode of the working copy thus
	// writes each alias in the labels out in full, the 100 bytes of the
	// anchor a at each of 20 aliases.
	doc := stringtest.LinesLF(
		"apiVersion: alarm.jacobcolvin.com/v1",
		"kind: Alarm",
		"metadata:",
		"  name: smoke",
		"  labels:",
		"    a: &a "+strings.Repeat("x", 100),
	) + yamltest.GenerateLines(20, "    l%d: *a") + stringtest.LinesLF("spec:", "  level: high")

	reg := manifest.MustNewRegistry(
		manifest.WithNameField(meta.Child("name")),
		manifest.WithUnmarshalers(niceyaml.CustomUnmarshaler(decodeAlarmLevel)),
		manifest.WithKinds(manifest.NewKind[Alarm]("Alarm", manifest.Version("alarm.jacobcolvin.com/v1"))),
	)

	tcs := map[string]struct {
		err  string
		opts []niceyaml.SourceOption
	}{
		"aliases past the byte limit": {
			// The ninth alias brings the text past 1 KiB.
			opts: []niceyaml.SourceOption{niceyaml.WithByteLimit(1024)},
			err:  "config/alarm.yaml:15:9: aliases repeat more than 1 KiB of text",
		},
		"aliases within a raised byte limit": {
			opts: []niceyaml.SourceOption{niceyaml.WithByteLimit(4096)},
		},
		"aliases with the alias limit off": {
			opts: []niceyaml.SourceOption{niceyaml.WithByteLimit(1024), niceyaml.WithAliasLimit(false)},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			in := configFS(map[string]string{"alarm.yaml": doc}, manifest.WithSourceOptions(tc.opts...))

			set, err := reg.Load(t.Context(), in)
			if tc.err == "" {
				require.NoError(t, err)

				alarm, ok := set.Get[Alarm]("smoke")
				require.True(t, ok)
				assert.Len(t, alarm.Value.Metadata.Labels, 21)

				return
			}

			require.Error(t, err)
			assert.Contains(t, niceyaml.FormatError(err), tc.err)
			require.ErrorIs(t, err, niceyaml.ErrLimit)
			require.ErrorIs(t, err, niceyaml.ErrExcessiveAliasing)
		})
	}
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
					manifest.Migrate(spec.Child("open"), func(ctx context.Context, n *edit.Node) error {
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

// TypeMeta holds the type fields, as the TypeMeta of Kubernetes does.
type TypeMeta struct {
	APIVersion string `json:"apiVersion,omitzero"`
	Kind       string `json:"kind,omitzero"`
}

// Brew is the Go type of a kind, and declares the type fields through
// its TypeMeta.
type Brew struct {
	TypeMeta `json:",inline"`

	Metadata Metadata `json:"metadata"`
}

// brewSchema returns a closed schema that requires and declares the type
// fields, and admits only version and kind as their values.
func brewSchema(version, kind string) string {
	return fmt.Sprintf(`{
  "type": "object",
  "required": ["apiVersion", "kind"],
  "properties": {
    "apiVersion": {"const": %q},
    "kind": {"const": %q},
    "metadata": {"type": "object"}
  },
  "additionalProperties": false
}`, version, kind)
}

func TestRegistry_Load_TypeFields(t *testing.T) {
	t.Parallel()

	coffee := stringtest.LinesLF("apiVersion: v1", "kind: Coffee", "metadata:", "  name: bryggkaffe")
	brew := stringtest.LinesLF("apiVersion: v2", "kind: Brew", "metadata:", "  name: bryggkaffe")

	// A closed schema that leaves the type fields out.
	closed := `{"type": "object", "properties": {"metadata": {"type": "object"}}, "additionalProperties": false}`

	tcs := map[string]struct {
		// The schemas of v1 and v2, where "" gives the version none.
		v1, v2 string
		doc    string
		decode []niceyaml.DecodeOption
		err    string
	}{
		"current version": {
			doc: brew,
		},
		"older version": {
			doc: coffee,
		},
		"schemas that declare the type fields": {
			v1:  brewSchema("v1", "Coffee"),
			v2:  brewSchema("v2", "Brew"),
			doc: coffee,
		},
		"closed schema that leaves the type fields out": {
			v1:  closed,
			v2:  closed,
			doc: coffee,
		},
		"closed schema and a flow mapping": {
			v2:  closed,
			doc: "{apiVersion: v2, kind: Brew, metadata: {name: bryggkaffe}}\n",
		},
		"closed schema and a flow mapping that ends in the type fields": {
			v2:  closed,
			doc: "{metadata: {name: bryggkaffe}, apiVersion: v2, kind: Brew}\n",
		},
		"closed schema and a document of type fields alone": {
			v2:  closed,
			doc: stringtest.LinesLF("apiVersion: v2", "kind: Brew"),
			// The schema check passes, so the check of the name fails.
			err: stringtest.JoinLF(
				"brew.yaml:1:1: $.metadata.name: name must be a non-empty string",
				"",
				"   1 | apiVersion: v2",
				"     | ^^^^^^^^^^",
				"   2 | kind: Brew",
			),
		},
		"schema that rejects a type field": {
			v2:  brewSchema("v2", "Tea"),
			doc: brew,
			err: stringtest.JoinLF(
				"brew.yaml:2:7: $.kind: value does not match const",
				"",
				"   1 | apiVersion: v2",
				"   2 | kind: Brew",
				"     |       ^^^^",
				"   3 | metadata:",
				"   4 |   name: bryggkaffe",
			),
		},
		"validator that reads a type field": {
			doc: coffee,
			decode: []niceyaml.DecodeOption{niceyaml.WithValidator(niceyaml.ValidatorFunc(
				func(ctx context.Context, n *niceyaml.Node) error {
					kind, err := n.DecodeAt[string](ctx, paths.Doc().Child("kind"))
					if err != nil {
						return err
					}

					if kind != "Brew" {
						return niceyaml.NewError("kind is " + kind)
					}

					return nil
				},
			))},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var v1, v2 []manifest.VersionOption

			if tc.v1 != "" {
				v1 = append(v1, manifest.Schema(schema.Embedded([]byte(tc.v1))))
			}

			if tc.v2 != "" {
				v2 = append(v2, manifest.Schema(schema.Embedded([]byte(tc.v2))))
			}

			reg := manifest.MustNewRegistry(
				manifest.WithNameField(meta.Child("name")),
				manifest.WithDecodeOptions(tc.decode...),
				manifest.WithKinds(manifest.NewKind[Brew]("Brew",
					manifest.Version("v1", append(v1, manifest.KindName("Coffee"))...),
					manifest.Version("v2", v2...),
				)),
			)

			set, err := reg.Load(t.Context(),
				manifest.Sources(niceyaml.NewSourceFromString(tc.doc, niceyaml.WithName("brew.yaml"))))
			if tc.err != "" {
				require.Error(t, err)
				assert.Equal(t, tc.err, niceyaml.FormatError(err))

				return
			}

			require.NoError(t, err)

			got, ok := set.Get[Brew]("bryggkaffe")
			require.True(t, ok)
			assert.Equal(t, TypeMeta{APIVersion: "v2", Kind: "Brew"}, got.Value.TypeMeta)
		})
	}
}

func TestRegistry_Load_MergedTypeFields(t *testing.T) {
	t.Parallel()

	// The type fields of the document come after the merge key, so they
	// hide each type field that the merge key brings in.
	typeFields := stringtest.LinesLF(
		"apiVersion: cafe.jacobcolvin.com/v1",
		"kind: Cafe",
		"metadata:",
		"  name: yamull",
	)

	tcs := map[string]struct {
		doc  string
		ref  string
		mode *niceyaml.UnknownFieldsMode
		want Cafe
		err  string
	}{
		"reference document that writes the kind": {
			doc:  "<<: *b\n" + typeFields,
			ref:  "base: &b {kind: Hours, spec: {seats: 2}}\n",
			want: Cafe{Metadata: Metadata{Name: "yamull"}, Spec: CafeSpec{Seats: 2}},
		},
		"reference document that writes the version": {
			doc:  "<<: *b\n" + typeFields,
			ref:  "base: &b {apiVersion: v0, spec: {seats: 2}}\n",
			want: Cafe{Metadata: Metadata{Name: "yamull"}, Spec: CafeSpec{Seats: 2}},
		},
		"merge key of a reference document that brings in the kind": {
			doc:  "<<: *b\n" + typeFields,
			ref:  stringtest.LinesLF("hours: &h {kind: Hours}", "base: &b {<<: *h, spec: {seats: 2}}"),
			want: Cafe{Metadata: Metadata{Name: "yamull"}, Spec: CafeSpec{Seats: 2}},
		},
		"reference document that writes the kind under the warn mode": {
			doc:  "<<: *b\n" + typeFields,
			ref:  "base: &b {kind: Hours, spec: {seats: 2}}\n",
			mode: new(niceyaml.UnknownFieldsWarn),
			want: Cafe{Metadata: Metadata{Name: "yamull"}, Spec: CafeSpec{Seats: 2}},
		},
		"mapping of the document that writes the kind": {
			doc:  "<<: {kind: Hours, spec: {seats: 2}}\n" + typeFields,
			want: Cafe{Metadata: Metadata{Name: "yamull"}, Spec: CafeSpec{Seats: 2}},
		},
		"anchored mapping that another alias reads": {
			// The labels read the anchor as the document writes it, with
			// the kind.
			doc:  "<<: &extra {kind: Hours}\n" + typeFields + "  labels: *extra\n",
			want: Cafe{Metadata: Metadata{Name: "yamull", Labels: map[string]string{"kind": "Hours"}}},
		},
		// The decode leaves out the kind that the merge key brings in, but
		// the labels read the anchor on it.
		"anchor on the kind that a merge key brings in": {
			doc: "<<: {kind: &k Hours, spec: {seats: 2}}\n" + typeFields + "  labels: {a: *k}\n",
			want: Cafe{
				Metadata: Metadata{Name: "yamull", Labels: map[string]string{"a": "Hours"}},
				Spec:     CafeSpec{Seats: 2},
			},
		},
		"anchor on the kind that a merge key brings in under the warn mode": {
			doc:  "<<: {kind: &k Hours, spec: {seats: 2}}\n" + typeFields + "  labels: {a: *k}\n",
			mode: new(niceyaml.UnknownFieldsWarn),
			want: Cafe{
				Metadata: Metadata{Name: "yamull", Labels: map[string]string{"a": "Hours"}},
				Spec:     CafeSpec{Seats: 2},
			},
		},
		// The merged anchor reads an anchor that the document writes
		// before it.
		"merged anchor that reads an earlier anchor": {
			doc: stringtest.LinesLF(
				"metadata:",
				"  name: yamull",
				"  labels: {a: &z '5'}",
				"<<: &a {kind: Hours, spec: {currency: *z}}",
				"apiVersion: cafe.jacobcolvin.com/v1",
				"kind: Cafe",
			),
			want: Cafe{
				Metadata: Metadata{Name: "yamull", Labels: map[string]string{"a": "5"}},
				Spec:     CafeSpec{Currency: "5"},
			},
		},
		"merged anchor that reads an earlier anchor under the warn mode": {
			doc: stringtest.LinesLF(
				"metadata:",
				"  name: yamull",
				"  labels: {a: &z '5'}",
				"<<: &a {kind: Hours, spec: {currency: *z}}",
				"apiVersion: cafe.jacobcolvin.com/v1",
				"kind: Cafe",
			),
			mode: new(niceyaml.UnknownFieldsWarn),
			want: Cafe{
				Metadata: Metadata{Name: "yamull", Labels: map[string]string{"a": "5"}},
				Spec:     CafeSpec{Currency: "5"},
			},
		},
		// The decode leaves out the kind of the document, but the labels
		// read the anchor on it.
		"anchor on the kind of the document": {
			doc: stringtest.LinesLF(
				"apiVersion: cafe.jacobcolvin.com/v1",
				"kind: &k Cafe",
				"metadata:",
				"  name: yamull",
				"  labels: {a: *k}",
			),
			want: Cafe{Metadata: Metadata{Name: "yamull", Labels: map[string]string{"a": "Cafe"}}},
		},
		"anchor on the kind of the document under the warn mode": {
			doc: stringtest.LinesLF(
				"apiVersion: cafe.jacobcolvin.com/v1",
				"kind: &k Cafe",
				"metadata:",
				"  name: yamull",
				"  labels: {a: *k}",
			),
			mode: new(niceyaml.UnknownFieldsWarn),
			want: Cafe{Metadata: Metadata{Name: "yamull", Labels: map[string]string{"a": "Cafe"}}},
		},
		"unknown field of a reference document": {
			doc: "<<: *b\n" + typeFields,
			ref: "base: &b {kind: Hours, seat: 2}\n",
			err: stringtest.JoinLF(
				`config/cafe.yaml:1:5: unknown field "seat"`,
				"",
				"   1 | <<: *b",
				"     |     ^",
				"   2 | apiVersion: cafe.jacobcolvin.com/v1",
				"   3 | kind: Cafe",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var opts []manifest.RegistryOption

			if tc.mode != nil {
				opts = append(opts, manifest.WithDecodeOptions(niceyaml.WithUnknownFields(*tc.mode)))
			}

			var in []manifest.InputOption

			if tc.ref != "" {
				ref := niceyaml.NewSourceFromString(tc.ref)
				in = append(in, manifest.WithSourceOptions(niceyaml.WithReferences(ref)))
			}

			set, err := manifest.MustNewRegistry(cafeOptions(opts...)...).Load(t.Context(),
				configFS(map[string]string{"cafe.yaml": tc.doc}, in...))
			if tc.err != "" {
				require.Error(t, err)
				assert.Equal(t, tc.err, niceyaml.FormatError(err))
				require.ErrorIs(t, err, niceyaml.ErrUnknownField)

				return
			}

			require.NoError(t, err)
			require.NoError(t, set.Warnings())

			got, ok := set.Get[Cafe]("yamull")
			require.True(t, ok)
			assert.Equal(t, tc.want, got.Value)
		})
	}
}

// cafeFields holds the fields of a Cafe, for an unmarshaler of Cafe that
// decodes it field by field.
type cafeFields Cafe

// brewFields holds the fields of a Brew, for an unmarshaler of Brew that
// decodes it field by field.
type brewFields Brew

// metadataFields holds the fields of a Metadata, for an unmarshaler of
// Metadata that decodes it field by field.
type metadataFields Metadata

// jsonCafe is the Go type of a kind that reads its JSON through a method
// of its own, which rejects unknown fields.
type jsonCafe Cafe

// UnmarshalJSON decodes c from data field by field, and rejects each key
// that no field reads.
func (c *jsonCafe) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	var f cafeFields

	err := dec.Decode(&f)
	if err != nil {
		return fmt.Errorf("decode cafe: %w", err)
	}

	*c = jsonCafe(f)

	return nil
}

func TestRegistry_Load_SelfDecodingTypes(t *testing.T) {
	t.Parallel()

	yamull := Metadata{Name: "yamull"}

	tcs := map[string]struct {
		// The options of the registry, whose unmarshaler counts each of
		// its calls in calls. The registry holds one kind, of version v1.
		opts func(calls *atomic.Int32) []manifest.RegistryOption
		// The value of the manifest.
		want any
		// The name of the kind.
		kind string
		// The calls that the load makes to the unmarshaler of opts.
		calls int32
	}{
		"custom unmarshaler of the kind": {
			kind: "Cafe",
			opts: func(calls *atomic.Int32) []manifest.RegistryOption {
				return []manifest.RegistryOption{
					manifest.WithUnmarshalers(niceyaml.CustomUnmarshaler(
						func(_ context.Context, c *Cafe, decode func(any) error) error {
							calls.Add(1)

							var f cafeFields

							err := decode(&f)
							*c = Cafe(f)

							return err
						},
					)),
					manifest.WithKinds(manifest.NewKind[Cafe]("Cafe", manifest.Version("v1"))),
				}
			},
			want:  Cafe{Metadata: yamull},
			calls: 1,
		},
		"custom unmarshaler of a kind that declares the type fields": {
			kind: "Brew",
			opts: func(calls *atomic.Int32) []manifest.RegistryOption {
				return []manifest.RegistryOption{
					manifest.WithUnmarshalers(niceyaml.CustomUnmarshaler(
						func(_ context.Context, b *Brew, decode func(any) error) error {
							calls.Add(1)

							var f brewFields

							err := decode(&f)
							*b = Brew(f)

							return err
						},
					)),
					manifest.WithKinds(manifest.NewKind[Brew]("Brew", manifest.Version("v1"))),
				}
			},
			// The unmarshaler does not see the type fields.
			want:  Brew{Metadata: yamull},
			calls: 1,
		},
		"custom unmarshaler below the kind": {
			kind: "Brew",
			opts: func(calls *atomic.Int32) []manifest.RegistryOption {
				return []manifest.RegistryOption{
					manifest.WithUnmarshalers(niceyaml.CustomUnmarshaler(
						func(_ context.Context, m *Metadata, decode func(any) error) error {
							calls.Add(1)

							var f metadataFields

							err := decode(&f)
							*m = Metadata(f)

							return err
						},
					)),
					manifest.WithKinds(manifest.NewKind[Brew]("Brew", manifest.Version("v1"))),
				}
			},
			want:  Brew{TypeMeta: TypeMeta{APIVersion: "v1", Kind: "Brew"}, Metadata: yamull},
			calls: 1,
		},
		"JSON unmarshaler of the kind": {
			kind: "Cafe",
			opts: func(*atomic.Int32) []manifest.RegistryOption {
				return []manifest.RegistryOption{
					manifest.WithUnmarshalers(niceyaml.JSONUnmarshalers(true)),
					manifest.WithKinds(manifest.NewKind[jsonCafe]("Cafe", manifest.Version("v1"))),
				}
			},
			want: jsonCafe{Metadata: yamull},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var calls atomic.Int32

			reg := manifest.MustNewRegistry(append(tc.opts(&calls), manifest.WithNameField(meta.Child("name")))...)

			doc := stringtest.LinesLF("apiVersion: v1", "kind: "+tc.kind, "metadata:", "  name: yamull")

			set, err := reg.Load(t.Context(),
				manifest.Sources(niceyaml.NewSourceFromString(doc, niceyaml.WithName("cafe.yaml"))))
			require.NoError(t, err)

			var got []any

			for o := range set.Objects() {
				got = append(got, o.Value)
			}

			assert.Equal(t, []any{tc.want}, got)
			assert.Equal(t, tc.calls, calls.Load())
		})
	}
}

func TestRegistry_Load_NestedTypeField(t *testing.T) {
	t.Parallel()

	doc := stringtest.LinesLF("apiVersion: v1", "metadata:", "  kind: Cafe", "  name: yamull")

	tcs := map[string]struct {
		schema string
		err    string
	}{
		"schema that declares the type field": {
			schema: `{"properties": {"metadata": {
  "required": ["kind"],
  "properties": {"kind": {"const": "Cafe"}, "name": {"type": "string"}},
  "additionalProperties": false}}}`,
		},
		"closed schema that leaves the type field out": {
			schema: `{"properties": {"metadata": {
  "properties": {"name": {"type": "string"}},
  "additionalProperties": false}}}`,
		},
		"schema that rejects the type field": {
			schema: `{"properties": {"metadata": {"properties": {"kind": {"const": "Bar"}}}}}`,
			err:    "cafe.yaml:3:9: $.metadata.kind: value does not match const",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reg := manifest.MustNewRegistry(
				manifest.WithKindField(meta.Child("kind")),
				manifest.WithNameField(meta.Child("name")),
				manifest.WithKinds(manifest.NewKind[Cafe]("Cafe",
					manifest.Version("v1", manifest.Schema(schema.Embedded([]byte(tc.schema)))),
				)),
			)

			set, err := reg.Load(t.Context(),
				manifest.Sources(niceyaml.NewSourceFromString(doc, niceyaml.WithName("cafe.yaml"))))
			if tc.err != "" {
				require.Error(t, err)
				assert.Equal(t, tc.err, firstLine(niceyaml.FormatError(err)))

				return
			}

			require.NoError(t, err)

			_, ok := set.Get[Cafe]("yamull")
			assert.True(t, ok)
		})
	}
}
