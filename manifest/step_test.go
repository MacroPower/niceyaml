package manifest_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/edit"
	"go.jacobcolvin.com/niceyaml/manifest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/schema"
)

var (
	// A Drink at cafe.jacobcolvin.com/v1beta1, with spec after it.
	drinkDoc = stringtest.LinesLF(
		"apiVersion: cafe.jacobcolvin.com/v1beta1",
		"kind: Drink",
		"metadata:",
		"  name: kaffe",
		"spec:",
	)

	// A Drink with the color field that v1 of [drinkOptions] removes.
	colorDrinkDoc = drinkDoc + "  color: red\n"

	// The size of v1 for each size of v1beta1.
	sizes = map[string]string{"S": "small", "M": "medium", "L": "large"}
)

// drinkOptions returns the options of a registry with one kind, Drink,
// whose v1 holds the steps of the size example, the color removal, and a
// step that panics. The written version is v1beta1 when lagging is set,
// and v1 otherwise.
func drinkOptions(lagging bool) []manifest.RegistryOption {
	var mark []manifest.VersionOption

	if lagging {
		mark = append(mark, manifest.Written())
	}

	return []manifest.RegistryOption{
		manifest.WithAppName("cafe"),
		manifest.WithMigrateHint(migrateHint),
		manifest.WithKinds(manifest.NewKind[map[string]any]("Drink",
			manifest.Version("cafe.jacobcolvin.com/v1beta1", mark...),
			manifest.Version("cafe.jacobcolvin.com/v1",
				manifest.Migrate(spec.Child("size"), func(ctx context.Context, n *edit.Node) error {
					size, ok := sizes[n.Text()]
					if !ok {
						return niceyaml.NewError(fmt.Sprintf("no cafe.jacobcolvin.com/v1 size for %q", n.Text()),
							niceyaml.WithDetails(errors.New("use S, M, or L")))
					}

					return n.Set(ctx, size)
				}),
				manifest.Migrate(spec.Child("color"), func(_ context.Context, n *edit.Node) error {
					return n.Remove(edit.WithReason("menu colors moved to the theme of the Cafe"))
				}),
				manifest.Migrate(spec.Child("boom"), func(context.Context, *edit.Node) error {
					panic("boom")
				}),
			),
		)),
	}
}

// adoptOptions returns the options of a registry with one kind, Cafe,
// whose v1 moves the name and the seats of an unversioned document below
// metadata and spec.
func adoptOptions() []manifest.RegistryOption {
	return []manifest.RegistryOption{
		manifest.WithAppName("cafe"),
		manifest.WithNameField(meta.Child("name")),
		manifest.WithKinds(manifest.NewKind[Cafe]("Cafe",
			manifest.Unversioned(),
			manifest.Version("cafe.jacobcolvin.com/v1",
				manifest.Migrate(paths.Doc().Child("name"), func(_ context.Context, n *edit.Node) error {
					return n.Move(meta.Child("name"))
				}),
				manifest.Migrate(paths.Doc().Child("seats"), func(_ context.Context, n *edit.Node) error {
					return n.Move(spec.Child("seats"))
				}),
			),
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

	// A price step, which reaches each price through At, so a price that a
	// merge key or an alias shares changes once for each entry.
	prices := []manifest.RegistryOption{
		manifest.WithAppName("cafe"),
		manifest.WithKinds(manifest.NewKind[map[string]any]("MenuItem",
			manifest.Version("cafe.jacobcolvin.com/v1beta1"),
			manifest.Version("cafe.jacobcolvin.com/v1",
				manifest.Migrate(paths.MustParse("$.spec.extras.*"), func(ctx context.Context, n *edit.Node) error {
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

	merged := stringtest.LinesLF(
		"apiVersion: v1",
		"kind: Hours",
		"summer: &summer",
		`  closes: "21:00"`,
		"spec:",
		"  <<: *summer",
		`  closes: "19:00"`,
	)

	// A removal of closes, which the first entry of a mapping that merges
	// writes.
	removed := []manifest.RegistryOption{
		manifest.WithAppName("cafe"),
		manifest.WithKinds(manifest.NewKind[map[string]any]("Hours",
			manifest.Version("v1"),
			manifest.Version("v2", manifest.Migrate(spec.Child("closes"), func(_ context.Context, n *edit.Node) error {
				return n.Remove(edit.WithReason("closing is up to the staff"))
			})),
		)),
	}

	// Each case loads without the checks of the set.
	tcs := map[string]loadCase{
		"schema error at the key that the file writes": {
			opts: cafeOptions(),
			files: map[string]string{
				"cafe.yaml": cafeDoc,
				"menu.yaml": stringtest.LinesLF(
					"apiVersion: cafe.jacobcolvin.com/v1beta1",
					"kind: Dish",
					"metadata:",
					"  name: kanelbulle",
					"spec:",
					"  cost: free # kr",
					"  tags: vegan",
				),
			},
			err: stringtest.JoinLF(
				`config/menu.yaml:6:9: $.spec.price: expected "integer", got "string"`,
				"",
				"   4 |   name: kanelbulle",
				"   5 | spec:",
				"   6 |   cost: free # kr",
				"     |         ^^^^",
				"   7 |   tags: vegan",
			),
		},
		"decode error of a step": {
			opts: cafeOptions(),
			files: map[string]string{
				"cafe.yaml": cafeDoc,
				"menu.yaml": stringtest.LinesLF(
					"apiVersion: cafe.jacobcolvin.com/v1beta1",
					"kind: Dish",
					"metadata:",
					"  name: kanelbulle",
					"spec:",
					"  cost: 45 # kr",
					"  tags: [a, b]",
				),
			},
			err: stringtest.JoinLF(
				"config/menu.yaml:7:3: $.spec.tags: expected string, got sequence",
				"`-- cafe.jacobcolvin.com/v1 reads $.spec.tags",
				"",
				"   5 | spec:",
				"   6 |   cost: 45 # kr",
				"   7 |   tags: [a, b]",
				"     |   ^^^^",
			),
			matches: []error{niceyaml.ErrDecode},
		},
		"version field write of an anchored version that an alias reads": {
			// The text defines &v twice, so the load refuses the document.
			opts: []manifest.RegistryOption{
				manifest.WithAppName("cafe"),
				manifest.WithKinds(manifest.NewKind[map[string]any]("Hours",
					manifest.Version("v1"),
					manifest.Version("v2"),
				)),
			},
			files: map[string]string{
				"hours.yaml": stringtest.LinesLF("apiVersion: &v v1", "kind: Hours", "m:", "  <<: &v {a: 1}", "x: *v"),
			},
			err: stringtest.JoinLF(
				"config/hours.yaml:4:7: $.m: cafe does not edit a document that defines the anchor &v twice",
				"|-- 1:13: $.apiVersion: &v is defined here",
				"`-- rename one of the two anchors and the aliases that read it",
				"",
				"   1 | apiVersion: &v v1",
				"     |             ^^ &v is defined here",
				"   2 | kind: Hours",
				"   3 | m:",
				"   4 |   <<: &v {a: 1}",
				"     |       ^^",
				"   5 | x: *v",
			),
			matches: []error{edit.ErrUnsupported},
		},
		"rename to a key that exists": {
			opts: cafeOptions(),
			files: map[string]string{
				"cafe.yaml": cafeDoc,
				"hours.yaml": stringtest.LinesLF(
					"apiVersion: cafe.jacobcolvin.com/v1alpha1",
					"kind: Hours",
					"metadata:",
					"  name: weekday",
					"spec:",
					`  open: "07:00"`,
					`  opens: "08:00"`,
				),
			},
			err: stringtest.JoinLF(
				"config: 1 error, 1 warning",
				`|-- config/hours.yaml:1:13: warning: $.apiVersion: deprecated version "cafe.jacobcolvin.com/v1alpha1"`,
				"|   `-- cafe 4.0 removes cafe.jacobcolvin.com/v1alpha1",
				"`-- config/hours.yaml:7:3: $.spec.opens~: cafe.jacobcolvin.com/v1 renames \"opens\" to \"open\", "+
					"which already exists",
				`    |-- 6:3: $.spec.open~: "open" is defined here`,
				"    |-- remove one of the two",
				"    `-- cafe.jacobcolvin.com/v1 renames $.spec.opens to \"open\"",
				"",
				"config/hours.yaml",
				"   1 | apiVersion: cafe.jacobcolvin.com/v1alpha1",
				`     |             ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^ warning: deprecated version "cafe.jacobcolvin.com/v1alpha1"`,
				"   2 | kind: Hours",
				"   3 | metadata:",
				"   4 |   name: weekday",
				"   5 | spec:",
				`   6 |   open: "07:00"`,
				`     |   ^^^^ "open" is defined here`,
				`   7 |   opens: "08:00"`,
				`     |   ^^^^^ cafe.jacobcolvin.com/v1 renames "opens" to "open", which already exists`,
			),
		},
		"error of a step": {
			opts:  drinkOptions(false),
			files: map[string]string{"drink.yaml": drinkDoc + "  size: XL\n"},
			err: stringtest.JoinLF(
				`config/drink.yaml:6:9: $.spec.size: no cafe.jacobcolvin.com/v1 size for "XL"`,
				"`-- use S, M, or L",
				"",
				"   4 |   name: kaffe",
				"   5 | spec:",
				"   6 |   size: XL",
				"     |         ^^",
			),
		},
		"panic in a step": {
			opts:  drinkOptions(false),
			files: map[string]string{"drink.yaml": drinkDoc + "  boom: 1\n"},
			err: stringtest.JoinLF(
				"config/drink.yaml:6:9: $.spec.boom: Drink cafe.jacobcolvin.com/v1 step 3 panicked: boom",
				"",
				"   4 |   name: kaffe",
				"   5 | spec:",
				"   6 |   boom: 1",
				"     |         ^",
			),
		},
		"removal by the written version in a file that a rewrite cannot write": {
			opts:  drinkOptions(false),
			files: map[string]string{"drink.yaml": drinkDoc + stringtest.LinesLF("  color: red", "  size: S")},
			want:  []string{`Drink "" cafe.jacobcolvin.com/v1beta1 up-to-date=false`},
			warns: stringtest.JoinLF(
				`config/drink.yaml:6:3: warning: $.spec.color~: cafe.jacobcolvin.com/v1 ignores field "color"`,
				"`-- menu colors moved to the theme of the Cafe",
				"",
				"   4 |   name: kaffe",
				"   5 | spec:",
				"   6 |   color: red",
				"     |   ^^^^^",
				"   7 |   size: S",
			),
			matches: []error{manifest.ErrRemoved},
		},
		"removal by the written version in strict mode": {
			opts:  drinkOptions(false),
			files: map[string]string{"drink.yaml": colorDrinkDoc},
			load:  []manifest.LoadOption{manifest.WithWarningsAsErrors(true)},
			err: stringtest.JoinLF(
				`config/drink.yaml:6:3: $.spec.color~: cafe.jacobcolvin.com/v1 ignores field "color"`,
				"`-- menu colors moved to the theme of the Cafe",
				"",
				"   4 |   name: kaffe",
				"   5 | spec:",
				"   6 |   color: red",
				"     |   ^^^^^",
			),
			matches: []error{manifest.ErrRemoved},
		},
		"removal by a version newer than the written one in strict mode": {
			opts:  drinkOptions(true),
			files: map[string]string{"drink.yaml": colorDrinkDoc},
			load:  []manifest.LoadOption{manifest.WithWarningsAsErrors(true)},
			want:  []string{`Drink "" cafe.jacobcolvin.com/v1beta1 up-to-date=true`},
			warns: stringtest.JoinLF(
				`config/drink.yaml:6:3: warning: $.spec.color~: cafe.jacobcolvin.com/v1 ignores field "color"`,
				"`-- menu colors moved to the theme of the Cafe",
				"",
				"   4 |   name: kaffe",
				"   5 | spec:",
				"   6 |   color: red",
				"     |   ^^^^^",
			),
			matches: []error{manifest.ErrRemoved},
		},
		"schema of a version on the way": {
			opts: shiftOptions(),
			files: map[string]string{
				"holiday.yaml": stringtest.LinesLF(
					"apiVersion: cafe.jacobcolvin.com/v1alpha1",
					"kind: Shift",
					"spec:",
					"  opens: 10am",
				),
			},
			err: stringtest.JoinLF(
				`config/holiday.yaml:4:10: $.spec.open: string does not match pattern "^[0-9]{2}:[0-9]{2}$"`,
				"`-- cafe.jacobcolvin.com/v1 rejects this value on the way to cafe.jacobcolvin.com/v3",
				"",
				"   2 | kind: Shift",
				"   3 | spec:",
				"   4 |   opens: 10am",
				"     |          ^^^^",
			),
		},
		"value that a merge key shares changes twice": {
			// The calls of the step act in order. The call on soy reads the price
			// that the call on oat wrote through the merge key, and raises it again.
			opts: prices,
			files: map[string]string{
				"menu.yaml": stringtest.LinesLF(
					"apiVersion: cafe.jacobcolvin.com/v1beta1",
					"kind: MenuItem",
					"spec:",
					"  extras:",
					"    oat: &milk { price: 5 }",
					"    soy: { <<: *milk }",
				),
			},
			want: []string{`MenuItem "" cafe.jacobcolvin.com/v1beta1 up-to-date=false`},
		},
		"value that two entries of a path share through an alias": {
			// Each entry takes a call of its own, and the calls act in order. The
			// call on soy reads the price that the call on oat wrote through the
			// alias, and raises it again.
			opts: prices,
			files: map[string]string{
				"menu.yaml": stringtest.LinesLF(
					"apiVersion: cafe.jacobcolvin.com/v1beta1",
					"kind: MenuItem",
					"spec:",
					"  extras:",
					"    oat: &milk { price: 5 }",
					"    soy: *milk",
				),
			},
			want: []string{`MenuItem "" cafe.jacobcolvin.com/v1beta1 up-to-date=false`},
		},
		// The write of the version field is a call too, so its problem names
		// it. The message draws the U+0001 of the label as U+2401 (fix23
		// G23-2).
		"G23-2: engine error of the write of the version field at a field of the user": {
			opts: []manifest.RegistryOption{
				manifest.WithAppName("cafe"),
				manifest.WithKinds(manifest.NewKind[map[string]any]("Hours",
					manifest.Version("v1"),
					manifest.Version("v\x012"),
				)),
			},
			files: map[string]string{"hours.yaml": stringtest.LinesLF("apiVersion: v1", "kind: Hours", "x: 1")},
			err: stringtest.JoinLF(
				"config/hours.yaml:1:13: $.apiVersion: go-yaml's encoder writes U+0001 as it is, and YAML does "+
					"not allow that in a text",
				"|-- report this to the authors of cafe",
				"`-- v\u24012 sets $.apiVersion from this value",
				"",
				"   1 | apiVersion: v1",
				"     |             ^^",
				"   2 | kind: Hours",
				"   3 | x: 1",
			),
		},
		"G23-2: engine error of the write of a version field of its own": {
			opts: []manifest.RegistryOption{
				manifest.WithAppName("cafe"),
				manifest.WithVersionOnly(paths.Doc().Child("version")),
				manifest.WithKinds(manifest.NewKind[map[string]any]("Hours",
					manifest.Version("1"),
					manifest.Version("v\x012"),
				)),
			},
			files: map[string]string{"hours.yaml": stringtest.LinesLF("version: 1", "x: 1")},
			err: stringtest.JoinLF(
				"config/hours.yaml:1:10: $.version: go-yaml's encoder writes U+0001 as it is, and YAML does "+
					"not allow that in a text",
				"|-- report this to the authors of cafe",
				"`-- version v\u24012 sets $.version from this value",
				"",
				"   1 | version: 1",
				"     |          ^",
				"   2 | x: 1",
			),
		},
		// The write of the version field over a field of the user writes the
		// token of the field, as a Set does. A later problem at the field
		// names that write, and the next write checks its text before it
		// writes, as a Set checks its value (fix24 G24-2).
		"G24-2: engine error of the write of the version field over the write of an earlier version": {
			opts: []manifest.RegistryOption{
				manifest.WithAppName("cafe"),
				manifest.WithKinds(manifest.NewKind[map[string]any]("Hours",
					manifest.Version("v1"),
					manifest.Version("v2"),
					manifest.Version("v\x013"),
				)),
			},
			files: map[string]string{"hours.yaml": stringtest.LinesLF("apiVersion: v1", "kind: Hours", "x: 1")},
			err: stringtest.JoinLF(
				"config/hours.yaml:1:13: $.apiVersion: go-yaml's encoder writes U+0001 as it is, and YAML does "+
					"not allow that in a text",
				"|-- report this to the authors of cafe",
				"|-- v2 sets $.apiVersion from this value",
				"`-- v\u24013 sets $.apiVersion from this value",
				"",
				"   1 | apiVersion: v1",
				"     |             ^^",
				"   2 | kind: Hours",
				"   3 | x: 1",
			),
		},
		"G24-2: step that sets the version field after the write of an earlier version": {
			opts: []manifest.RegistryOption{
				manifest.WithAppName("cafe"),
				manifest.WithKinds(manifest.NewKind[map[string]any]("Hours",
					manifest.Version("v1"),
					manifest.Version("v2"),
					manifest.Version("v3", manifest.Migrate(paths.Doc(), func(ctx context.Context, n *edit.Node) error {
						return n.At(paths.MustParse("$.apiVersion")).Set(ctx, "zz")
					})),
				)),
			},
			files: map[string]string{"hours.yaml": stringtest.LinesLF("apiVersion: v1", "kind: Hours", "x: 1")},
			err: stringtest.JoinLF(
				"config/hours.yaml:1:13: $.apiVersion: v3 sets $.apiVersion, which is a protected value",
				"|-- v2 sets $.apiVersion from this value",
				"`-- v3 sets $.apiVersion from this value",
				"",
				"   1 | apiVersion: v1",
				"     |             ^^",
				"   2 | kind: Hours",
				"   3 | x: 1",
			),
		},
		"step that sets the version field through an alias of its mapping": {
			// The version field is the value that the alias reads, so the
			// step cannot change it there either. The problem binds at the
			// field, under its path in the user's text (fix19 P56).
			opts: []manifest.RegistryOption{
				manifest.WithAppName("cafe"),
				manifest.WithVersionField(paths.MustParse("$.metadata.version")),
				manifest.WithKinds(manifest.NewKind[map[string]any]("Hours",
					manifest.Version("v1"),
					manifest.Version("v2", manifest.Migrate(paths.Doc(), func(ctx context.Context, n *edit.Node) error {
						return n.At(paths.MustParse("$.other.version")).Set(ctx, "v9")
					})),
				)),
			},
			files: map[string]string{"hours.yaml": stringtest.LinesLF(
				"kind: Hours",
				"metadata: &m",
				"  version: v1",
				"other: *m",
			)},
			err: stringtest.JoinLF(
				"config/hours.yaml:3:12: $.metadata.version: v2 sets $.other.version, which is a protected value",
				"`-- v2 sets $.other.version from this value",
				"",
				"   1 | kind: Hours",
				"   2 | metadata: &m",
				"   3 |   version: v1",
				"     |            ^^",
				"   4 | other: *m",
			),
		},
		"rename that a merge key undoes": {
			opts:  closes("$.spec.closes"),
			files: map[string]string{"hours.yaml": merged},
			err: stringtest.JoinLF(
				`config/hours.yaml:7:3: $.spec.closes: v2 renames $.spec.closes to "close", but <<: *summer at 6:3 `+
					"also brings in closes, so $.spec still reads closes",
				"|-- 6:3: $.spec: <<: *summer brings in closes here",
				"|-- set $.spec.closes to the value it needs, or remove closes from $.summer too",
				"`-- v2 renames $.spec.closes to \"close\"",
				"",
				`   4 |   closes: "21:00"`,
				"   5 | spec:",
				"   6 |   <<: *summer",
				"     |   ^^ <<: *summer brings in closes here",
				`   7 |   closes: "19:00"`,
				"     |   ^^^^^^",
			),
		},
		"removal that a merge key undoes": {
			opts: removed,
			files: map[string]string{"hours.yaml": stringtest.LinesLF(
				"apiVersion: v1",
				"kind: Hours",
				"summer: &summer",
				`  closes: "21:00"`,
				"spec:",
				"  <<: *summer",
				`  closes: "19:00"`,
			)},
			// The failed step removes nothing, so no removal warns.
			err: stringtest.JoinLF(
				"config/hours.yaml:7:3: $.spec.closes: v2 removes $.spec.closes, but <<: *summer at 6:3 also brings "+
					"in closes, so $.spec still reads closes",
				"|-- 6:3: $.spec: <<: *summer brings in closes here",
				"|-- set $.spec.closes to the value it needs, or remove closes from $.summer too",
				"`-- v2 removes $.spec.closes",
				"",
				`   4 |   closes: "21:00"`,
				"   5 | spec:",
				"   6 |   <<: *summer",
				"     |   ^^ <<: *summer brings in closes here",
				`   7 |   closes: "19:00"`,
				"     |   ^^^^^^",
			),
		},
		"rename of every match": {
			// The rename of the source comes first, so the rename of the
			// mapping that merges it then hides the renamed key.
			opts:  closes("$..closes"),
			files: map[string]string{"hours.yaml": merged},
			err: stringtest.JoinLF(
				`config/hours.yaml:7:3: $.spec.closes: v2 renames $.spec.closes to "close", but <<: *summer at 6:3 `+
					"also brings in close, so $.spec then hides the close of $.summer",
				"|-- 6:3: $.spec: <<: *summer brings in close here",
				"|-- write a key that $.summer does not hold, or remove close from $.summer first",
				"`-- v2 renames $.spec.closes to \"close\"",
				"",
				`   4 |   closes: "21:00"`,
				"   5 | spec:",
				"   6 |   <<: *summer",
				"     |   ^^ <<: *summer brings in close here",
				`   7 |   closes: "19:00"`,
				"     |   ^^^^^^",
			),
		},
		"unversioned document that moves its fields": {
			opts:  adoptOptions(),
			files: map[string]string{"cafe.yaml": stringtest.LinesLF("name: yamull", "seats: 40")},
			want:  []string{`Cafe "yamull" unversioned up-to-date=false`},
		},
		"rename of a key that the mapping writes twice": {
			opts: closes("$.spec.closes"),
			files: map[string]string{"hours.yaml": stringtest.LinesLF(
				"apiVersion: v1",
				"kind: Hours",
				"spec:",
				`  closes: "21:00"`,
				`  closes: "19:00"`,
			)},
			in: []manifest.InputOption{manifest.WithSourceOptions(niceyaml.WithAllowDuplicateKeys(true))},
			err: stringtest.JoinLF(
				`config/hours.yaml:4:3: $.spec.closes: v2 renames $.spec.closes to "close", which reaches "closes", `+
					`a key that its mapping repeats, and cafe does not edit the entries of a repeated key`,
				"|-- 5:3: $.spec.closes: the key \"closes\" is written again here",
				"|-- remove all but one of the entries of the key",
				"`-- v2 renames $.spec.closes to \"close\"",
				"",
				"   2 | kind: Hours",
				"   3 | spec:",
				`   4 |   closes: "21:00"`,
				"     |   ^^^^^^",
				`   5 |   closes: "19:00"`,
				`     |   ^^^^^^ the key "closes" is written again here`,
			),
		},
		"rename through an alias of a reference document": {
			opts: closes("$.spec.closes"),
			files: map[string]string{"hours.yaml": stringtest.LinesLF(
				"apiVersion: v1",
				"kind: Hours",
				"spec: *summer",
			)},
			in: []manifest.InputOption{manifest.WithSourceOptions(niceyaml.WithReferences(
				niceyaml.NewSourceFromString(stringtest.LinesLF("summer: &summer", `  closes: "21:00"`)),
			))},
			// The problem binds at the alias that the path reads through.
			err: stringtest.JoinLF(
				"config/hours.yaml:3:7: $.spec: v2 reads $.spec.closes, which goes through *summer, an alias of a reference document",
				"",
				"   1 | apiVersion: v1",
				"   2 | kind: Hours",
				"   3 | spec: *summer",
				"     |       ^^^^^^^",
			),
			matches: []error{paths.ErrReferenceAlias, paths.ErrAlias},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tc.load = append([]manifest.LoadOption{manifest.WithSetChecks(false)}, tc.load...)
			tc.run(t)
		})
	}
}

func TestRegistry_Load_StepValues(t *testing.T) {
	t.Parallel()

	set := loadExample(t)

	hours, ok := set.Get[Hours]("weekday")
	require.True(t, ok)
	assert.Equal(t, HoursSpec{Open: "07:00", Close: "19:00"}, hours.Value.Spec)

	item, ok := set.Get[MenuItem]("kanelbulle")
	require.True(t, ok)
	assert.Equal(t, MenuItemSpec{Price: 45, Tags: []string{"vegan"}}, item.Value.Spec)

	reg := manifest.MustNewRegistry(shiftOptions()...)

	src := niceyaml.NewSourceFromString(
		stringtest.LinesLF(
			"apiVersion: cafe.jacobcolvin.com/v1alpha1",
			"kind: Shift",
			"spec:",
			`  opens: "10:00"`,
		),
		niceyaml.WithName("holiday.yaml"),
	)

	shifts, err := reg.Load(t.Context(), manifest.Sources(src))
	require.NoError(t, err)

	for shift := range shifts.All[map[string]any]() {
		assert.Equal(t, map[string]any{
			"apiVersion": "cafe.jacobcolvin.com/v3",
			"kind":       "Shift",
			"spec":       map[string]any{"start": "10:00"},
		}, shift.Value)
		assert.Equal(t, "cafe.jacobcolvin.com/v1alpha1", shift.Version)
	}

	drinkReg := manifest.MustNewRegistry(drinkOptions(false)...)

	drinks, err := drinkReg.Load(t.Context(), manifest.Sources(niceyaml.NewSourceFromString(drinkDoc+"  size: M\n")))
	require.NoError(t, err)

	want := map[string]any{
		"apiVersion": "cafe.jacobcolvin.com/v1",
		"kind":       "Drink",
		"metadata":   map[string]any{"name": "kaffe"},
		"spec":       map[string]any{"size": "medium"},
	}

	for drink := range drinks.All[map[string]any]() {
		assert.Equal(t, want, drink.Value)
	}
}
