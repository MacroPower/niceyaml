package manifest_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/manifest"
	"go.jacobcolvin.com/niceyaml/manifest/migrate"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/schema"
)

// drinkDoc is a Drink at cafe.jacobcolvin.com/v1beta1, with spec after it.
const drinkDoc = "apiVersion: cafe.jacobcolvin.com/v1beta1\nkind: Drink\nmetadata:\n  name: kaffe\nspec:\n"

// sizes maps the sizes of v1beta1 to those of v1.
var sizes = map[string]string{"S": "small", "M": "medium", "L": "large"}

// drinkOptions returns the options of a registry with one kind, Drink,
// whose v1 holds the steps of the size example, the color removal, and a
// step that panics. The written version is v1 when written is set.
func drinkOptions(written bool) []manifest.RegistryOption {
	var mark []manifest.VersionOption

	if written {
		mark = append(mark, manifest.Written())
	}

	return []manifest.RegistryOption{
		manifest.WithAppName("cafe"),
		manifest.WithMigrateHint(`run "cafe config migrate" to update this file`),
		manifest.WithKinds(manifest.NewKind[map[string]any]("Drink",
			manifest.Version("cafe.jacobcolvin.com/v1beta1"),
			manifest.Version("cafe.jacobcolvin.com/v1", append(mark,
				manifest.Migrate(spec.Child("size"), func(ctx context.Context, n *migrate.Node) error {
					size, ok := sizes[n.Text()]
					if !ok {
						return niceyaml.NewError(fmt.Sprintf("no cafe.jacobcolvin.com/v1 size for %q", n.Text()),
							niceyaml.WithDetails(errors.New("use S, M, or L")))
					}

					return n.Set(ctx, size)
				}),
				manifest.Migrate(spec.Child("color"), func(_ context.Context, n *migrate.Node) error {
					return n.Remove("menu colors moved to the theme of the Cafe")
				}),
				manifest.Migrate(spec.Child("boom"), func(context.Context, *migrate.Node) error {
					panic("boom")
				}),
			)...),
		)),
	}
}

// shiftOptions returns the options of a registry with one kind, Shift,
// whose v1 keeps a schema that requires each time to match HH:MM, and
// whose v2 renames open to start.
func shiftOptions() []manifest.RegistryOption {
	const v1Schema = `{"properties": {"spec": {"properties": {"open": {"pattern": "^[0-9]{2}:[0-9]{2}$"}}}}}`

	return []manifest.RegistryOption{
		manifest.WithAppName("cafe"),
		manifest.WithKinds(manifest.NewKind[map[string]any]("Shift",
			manifest.Version("cafe.jacobcolvin.com/v1alpha1"),
			manifest.Version("cafe.jacobcolvin.com/v1",
				manifest.Schema(schema.Embedded([]byte(v1Schema))),
				manifest.Migrate(spec.Child("opens"), rename("open")),
			),
			manifest.Version("cafe.jacobcolvin.com/v2",
				manifest.Written(),
				manifest.Migrate(spec.Child("open"), rename("start")),
			),
			manifest.Version("cafe.jacobcolvin.com/v3"),
		)),
	}
}

func TestStepID_String(t *testing.T) {
	t.Parallel()

	id := manifest.StepID{Kind: "Hours", Version: "cafe.jacobcolvin.com/v2", Step: 3}
	assert.Equal(t, "Hours cafe.jacobcolvin.com/v2 step 3", id.String())
}

func TestRegistry_Load_Steps(t *testing.T) {
	t.Parallel()

	adopt := []manifest.RegistryOption{
		manifest.WithAppName("cafe"),
		manifest.WithNameField(meta.Child("name")),
		manifest.WithKinds(manifest.NewKind[Cafe]("Cafe",
			manifest.Unversioned(),
			manifest.Version("cafe.jacobcolvin.com/v1",
				manifest.Migrate(paths.Doc().Child("name"), func(_ context.Context, n *migrate.Node) error {
					return n.Move(meta.Child("name"))
				}),
				manifest.Migrate(paths.Doc().Child("seats"), func(_ context.Context, n *migrate.Node) error {
					return n.Move(spec.Child("seats"))
				}),
			),
		)),
	}

	// The price step of "Anchors and Aliases", which reaches each price
	// through At, so a price that a merge key shares changes twice.
	prices := []manifest.RegistryOption{
		manifest.WithAppName("cafe"),
		manifest.WithKinds(manifest.NewKind[map[string]any]("MenuItem",
			manifest.Version("cafe.jacobcolvin.com/v1beta1"),
			manifest.Version("cafe.jacobcolvin.com/v1",
				manifest.Migrate(paths.MustParse("$.spec.extras.*"), func(ctx context.Context, n *migrate.Node) error {
					price := n.At(paths.Current().Child("price"))
					if !price.Exists() {
						return nil
					}

					kr, err := price.Decode[int](ctx)
					if err != nil {
						return err
					}

					return price.Set(ctx, kr*100)
				}),
			),
		)),
	}

	// A rename of closes, where a merge key also brings in closes.
	closes := func(p string) []manifest.RegistryOption {
		return []manifest.RegistryOption{
			manifest.WithAppName("cafe"),
			manifest.WithKinds(manifest.NewKind[map[string]any]("Hours",
				manifest.Version("v1"),
				manifest.Version("v2", manifest.Migrate(paths.MustParse(p), rename("close"))),
			)),
		}
	}

	merged := "apiVersion: v1\nkind: Hours\nsummer: &summer\n  closes: \"21:00\"\n" +
		"spec:\n  <<: *summer\n  closes: \"19:00\"\n"

	// A removal of closes, which the first entry of a mapping that merges
	// writes.
	removed := []manifest.RegistryOption{
		manifest.WithAppName("cafe"),
		manifest.WithKinds(manifest.NewKind[map[string]any]("Hours",
			manifest.Version("v1"),
			manifest.Version("v2", manifest.Migrate(spec.Child("closes"),
				func(_ context.Context, n *migrate.Node) error { return n.Remove("closing is up to the staff") })),
		)),
	}

	tcs := map[string]struct {
		files   map[string]string
		opts    []manifest.RegistryOption
		load    []manifest.LoadOption
		want    []string
		warns   string
		err     string
		matches []error
	}{
		"schema error at the key that the file writes": {
			opts: cafeOptions(),
			files: map[string]string{
				"cafe.yaml": cafeDoc,
				"menu.yaml": "apiVersion: cafe.jacobcolvin.com/v1beta1\nkind: Dish\nmetadata:\n  name: kanelbulle\n" +
					"spec:\n  cost: free # kr\n  tags: vegan\n",
			},
			err: "" +
				"config/menu.yaml:6:9: $.spec.cost: expected \"integer\", got \"string\"\n" +
				"\n" +
				"   4 |   name: kanelbulle\n" +
				"   5 | spec:\n" +
				"   6 |   cost: free # kr\n" +
				"     |         ^^^^\n" +
				"   7 |   tags: vegan",
		},
		"decode error of a step": {
			opts: cafeOptions(),
			files: map[string]string{
				"cafe.yaml": cafeDoc,
				"menu.yaml": "apiVersion: cafe.jacobcolvin.com/v1beta1\nkind: Dish\nmetadata:\n  name: kanelbulle\n" +
					"spec:\n  cost: 45 # kr\n  tags: [a, b]\n",
			},
			err: "" +
				"config/menu.yaml:7:3: $.spec.tags: expected string, got sequence\n" +
				"\n" +
				"   5 | spec:\n" +
				"   6 |   cost: 45 # kr\n" +
				"   7 |   tags: [a, b]\n" +
				"     |   ^^^^",
			matches: []error{niceyaml.ErrDecode},
		},
		"rename to a key that exists": {
			opts: cafeOptions(),
			files: map[string]string{
				"cafe.yaml": cafeDoc,
				"hours.yaml": "apiVersion: cafe.jacobcolvin.com/v1alpha1\nkind: Hours\nmetadata:\n  name: weekday\n" +
					"spec:\n  open: \"07:00\"\n  opens: \"08:00\"\n",
			},
			err: "" +
				"config: 1 error, 1 warning\n" +
				"|-- config/hours.yaml:1:13: warning: $.apiVersion: deprecated version " +
				"\"cafe.jacobcolvin.com/v1alpha1\"\n" +
				"|   `-- cafe 4.0 removes cafe.jacobcolvin.com/v1alpha1\n" +
				"`-- config/hours.yaml:7:3: $.spec.opens~: cafe.jacobcolvin.com/v1 renames \"opens\" to \"open\", " +
				"which already exists\n" +
				"    |-- 6:3: $.spec.open~: \"open\" is defined here\n" +
				"    `-- remove one of the two\n" +
				"\n" +
				"config/hours.yaml\n" +
				"   1 | apiVersion: cafe.jacobcolvin.com/v1alpha1\n" +
				"     |             ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^ warning: deprecated version " +
				"\"cafe.jacobcolvin.com/v1alpha1\"\n" +
				"   2 | kind: Hours\n" +
				"   3 | metadata:\n" +
				"   4 |   name: weekday\n" +
				"   5 | spec:\n" +
				"   6 |   open: \"07:00\"\n" +
				"     |   ^^^^ \"open\" is defined here\n" +
				"   7 |   opens: \"08:00\"\n" +
				"     |   ^^^^^ cafe.jacobcolvin.com/v1 renames \"opens\" to \"open\", which already exists",
		},
		"error of a step": {
			opts:  drinkOptions(true),
			files: map[string]string{"drink.yaml": drinkDoc + "  size: XL\n"},
			err: "" +
				"config/drink.yaml:6:9: $.spec.size: no cafe.jacobcolvin.com/v1 size for \"XL\"\n" +
				"`-- use S, M, or L\n" +
				"\n" +
				"   4 |   name: kaffe\n" +
				"   5 | spec:\n" +
				"   6 |   size: XL\n" +
				"     |         ^^",
		},
		"panic in a step": {
			opts:  drinkOptions(true),
			files: map[string]string{"drink.yaml": drinkDoc + "  boom: 1\n"},
			err: "" +
				"config/drink.yaml:6:9: $.spec.boom: Drink cafe.jacobcolvin.com/v1 step 3 panicked: boom\n" +
				"\n" +
				"   4 |   name: kaffe\n" +
				"   5 | spec:\n" +
				"   6 |   boom: 1\n" +
				"     |         ^",
		},
		"removal by the written version in a file that a rewrite cannot write": {
			opts:  drinkOptions(true),
			files: map[string]string{"drink.yaml": drinkDoc + "  color: red\n  size: S\n"},
			want:  []string{`Drink "" cafe.jacobcolvin.com/v1beta1 up-to-date=false`},
			warns: "" +
				"config/drink.yaml:6:3: warning: $.spec.color~: cafe.jacobcolvin.com/v1 ignores field \"color\"\n" +
				"`-- menu colors moved to the theme of the Cafe\n" +
				"\n" +
				"   4 |   name: kaffe\n" +
				"   5 | spec:\n" +
				"   6 |   color: red\n" +
				"     |   ^^^^^\n" +
				"   7 |   size: S",
			matches: []error{manifest.ErrRemoved},
		},
		"removal by the written version in strict mode": {
			opts:  drinkOptions(true),
			files: map[string]string{"drink.yaml": drinkDoc + "  color: red\n"},
			load:  []manifest.LoadOption{manifest.WithWarningsAsErrors(true)},
			err: "" +
				"config/drink.yaml:6:3: $.spec.color~: cafe.jacobcolvin.com/v1 ignores field \"color\"\n" +
				"`-- menu colors moved to the theme of the Cafe\n" +
				"\n" +
				"   4 |   name: kaffe\n" +
				"   5 | spec:\n" +
				"   6 |   color: red\n" +
				"     |   ^^^^^",
			matches: []error{manifest.ErrRemoved},
		},
		"removal by a version newer than the written one in strict mode": {
			opts:  drinkOptions(false),
			files: map[string]string{"drink.yaml": drinkDoc + "  color: red\n"},
			load:  []manifest.LoadOption{manifest.WithWarningsAsErrors(true)},
			want:  []string{`Drink "" cafe.jacobcolvin.com/v1beta1 up-to-date=true`},
			warns: "" +
				"config/drink.yaml:6:3: warning: $.spec.color~: cafe.jacobcolvin.com/v1 ignores field \"color\"\n" +
				"`-- menu colors moved to the theme of the Cafe\n" +
				"\n" +
				"   4 |   name: kaffe\n" +
				"   5 | spec:\n" +
				"   6 |   color: red\n" +
				"     |   ^^^^^",
			matches: []error{manifest.ErrRemoved},
		},
		"schema of a version on the way": {
			opts: shiftOptions(),
			files: map[string]string{
				"holiday.yaml": "apiVersion: cafe.jacobcolvin.com/v1alpha1\nkind: Shift\nspec:\n  opens: 10am\n",
			},
			err: "" +
				"config/holiday.yaml:4:10: $.spec.opens: string does not match pattern \"^[0-9]{2}:[0-9]{2}$\"\n" +
				"`-- cafe.jacobcolvin.com/v1 rejects this value on the way to cafe.jacobcolvin.com/v3\n" +
				"\n" +
				"   2 | kind: Shift\n" +
				"   3 | spec:\n" +
				"   4 |   opens: 10am\n" +
				"     |          ^^^^",
		},
		"value that a merge key shares changes twice": {
			opts: prices,
			files: map[string]string{
				"menu.yaml": "apiVersion: cafe.jacobcolvin.com/v1beta1\nkind: MenuItem\nspec:\n  extras:\n" +
					"    oat: &milk { price: 5 }\n    soy: { <<: *milk }\n",
			},
			err: "" +
				"config/menu.yaml:6:16: $.spec.extras.soy.price: cafe.jacobcolvin.com/v1 changes this value a second time\n" +
				"|-- 5:25: $.spec.extras.oat.price: cafe.jacobcolvin.com/v1 changes it here first\n" +
				"`-- write the value out in place of *milk\n" +
				"\n" +
				"   3 | spec:\n" +
				"   4 |   extras:\n" +
				"   5 |     oat: &milk { price: 5 }\n" +
				"     |                         ^ cafe.jacobcolvin.com/v1 changes it here first\n" +
				"   6 |     soy: { <<: *milk }\n" +
				"     |                ^^^^^",
		},
		"value that a path selects once": {
			opts: prices,
			files: map[string]string{
				"menu.yaml": "apiVersion: cafe.jacobcolvin.com/v1beta1\nkind: MenuItem\nspec:\n  extras:\n" +
					"    oat: &milk { price: 5 }\n    soy: *milk\n",
			},
			want: []string{`MenuItem "" cafe.jacobcolvin.com/v1beta1 up-to-date=true`},
		},
		"rename that a merge key undoes": {
			opts:  closes("$.spec.closes"),
			files: map[string]string{"hours.yaml": merged},
			err: "" +
				"config/hours.yaml:7:3: $.spec.closes~: v2 renames \"closes\" to \"close\", " +
				"but <<: *summer also brings in \"closes\"\n" +
				"`-- write the value out in place of *summer\n" +
				"\n" +
				"   5 | spec:\n" +
				"   6 |   <<: *summer\n" +
				"   7 |   closes: \"19:00\"\n" +
				"     |   ^^^^^^",
		},
		"removal that a merge key undoes": {
			opts: removed,
			files: map[string]string{"hours.yaml": "apiVersion: v1\nkind: Hours\nsummer: &summer\n" +
				"  closes: \"21:00\"\nspec:\n  closes: \"19:00\"\n  <<: *summer\n"},
			err: "" +
				"config: 1 error, 1 warning\n" +
				"|-- config/hours.yaml:6:3: $.spec.closes~: v2 removes \"closes\", " +
				"but <<: *summer also brings in \"closes\"\n" +
				"|   `-- write the value out in place of *summer\n" +
				"`-- config/hours.yaml:6:3: warning: $.spec.closes~: v2 ignores field \"closes\"\n" +
				"    `-- closing is up to the staff\n" +
				"\n" +
				"config/hours.yaml\n" +
				"   4 |   closes: \"21:00\"\n" +
				"   5 | spec:\n" +
				"   6 |   closes: \"19:00\"\n" +
				"     |   ^^^^^^ v2 removes \"closes\", but <<: *summer also brings in \"closes\"\n" +
				"     |   ^^^^^^ warning: v2 ignores field \"closes\"\n" +
				"   7 |   <<: *summer",
		},
		"rename of every match": {
			opts:  closes("$..closes"),
			files: map[string]string{"hours.yaml": merged},
			want:  []string{`Hours "" v1 up-to-date=true`},
		},
		"unversioned document that moves its fields": {
			opts:  adopt,
			files: map[string]string{"cafe.yaml": "name: yamull\nseats: 40\n"},
			want:  []string{`Cafe "yamull" unversioned up-to-date=true`},
		},
		"unversioned document of the wrong layout": {
			opts:  adopt,
			files: map[string]string{"notes.yaml": "- a\n"},
			err: "" +
				"config/notes.yaml:1:3: $: expected mapping, got sequence\n" +
				"|-- the document has no apiVersion and no kind, so cafe reads it as an unversioned Cafe\n" +
				"`-- move the file out of config if it is not a Cafe\n" +
				"\n" +
				"   1 | - a\n" +
				"     |   ^",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reg, err := manifest.NewRegistry(tc.opts...)
			require.NoError(t, err)

			set, err := reg.Load(t.Context(), manifest.FS(mapFS(tc.files), "config"),
				append([]manifest.LoadOption{manifest.WithSetChecks(false)}, tc.load...)...)
			if tc.err != "" {
				require.Error(t, err)
				assert.Equal(t, tc.err, niceyaml.FormatError(err))

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
		})
	}
}

func TestRegistry_Load_RemovalHint(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tree{files: map[string]string{"config/drink.yaml": drinkDoc + "  color: red\n"}}.write(t, dir)

	for _, written := range []bool{true, false} {
		reg, err := manifest.NewRegistry(drinkOptions(written)...)
		require.NoError(t, err)

		set, err := reg.Load(t.Context(), manifest.Path(filepath.Join(dir, "config")))
		require.NoError(t, err)

		hint := "`-- run \"cafe config migrate\" to update this file"
		if written {
			assert.Contains(t, niceyaml.FormatError(set.Warnings()), hint)
		} else {
			assert.NotContains(t, niceyaml.FormatError(set.Warnings()), hint)
		}
	}
}

func TestRegistry_Load_StepValues(t *testing.T) {
	t.Parallel()

	set := loadExample(t, newCafeRegistry(t))

	hours, ok := set.Get[Hours]("weekday")
	require.True(t, ok)
	assert.Equal(t, HoursSpec{Open: "07:00", Close: "19:00"}, hours.Value.Spec)

	item, ok := set.Get[MenuItem]("kanelbulle")
	require.True(t, ok)
	assert.Equal(t, MenuItemSpec{Price: 45, Tags: []string{"vegan"}}, item.Value.Spec)

	reg, err := manifest.NewRegistry(shiftOptions()...)
	require.NoError(t, err)

	src := niceyaml.NewSourceFromString(
		"apiVersion: cafe.jacobcolvin.com/v1alpha1\nkind: Shift\nspec:\n  opens: \"10:00\"\n",
		niceyaml.WithName("holiday.yaml"),
	)

	shifts, err := reg.Load(t.Context(), manifest.Sources(src))
	require.NoError(t, err)

	for shift := range shifts.All[map[string]any]() {
		assert.Equal(t, map[string]any{"spec": map[string]any{"start": "10:00"}}, shift.Value)
		assert.Equal(t, "cafe.jacobcolvin.com/v1alpha1", shift.Version)
	}

	adopt, err := manifest.NewRegistry(drinkOptions(true)...)
	require.NoError(t, err)

	drinks, err := adopt.Load(t.Context(), manifest.Sources(niceyaml.NewSourceFromString(drinkDoc+"  size: M\n")))
	require.NoError(t, err)

	want := map[string]any{"metadata": map[string]any{"name": "kaffe"}, "spec": map[string]any{"size": "medium"}}

	for drink := range drinks.All[map[string]any]() {
		assert.Equal(t, want, drink.Value)
	}
}

func TestRegistry_Load_StepRepeatedKey(t *testing.T) {
	t.Parallel()

	reg, err := manifest.NewRegistry(
		manifest.WithAppName("cafe"),
		manifest.WithKinds(manifest.NewKind[map[string]any]("Hours",
			manifest.Version("v1"),
			manifest.Version("v2", manifest.Migrate(spec.Child("closes"), rename("close"))),
		)),
	)
	require.NoError(t, err)

	src := niceyaml.NewSourceFromString(
		"apiVersion: v1\nkind: Hours\nspec:\n  closes: \"21:00\"\n  closes: \"19:00\"\n",
		niceyaml.WithName("hours.yaml"), niceyaml.WithAllowDuplicateKeys(true),
	)

	_, err = reg.Load(t.Context(), manifest.Sources(src))
	require.Error(t, err)
	assert.Equal(t, ""+
		"hours.yaml:5:3: $.spec.closes~: v2 renames \"closes\" to \"close\", but the mapping also writes \"closes\"\n"+
		"`-- remove one of the two", firstTwoLines(niceyaml.FormatError(err)))
}
