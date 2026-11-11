package manifest_test

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/edit"
	"go.jacobcolvin.com/niceyaml/encoder"
	"go.jacobcolvin.com/niceyaml/manifest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/schema"
	"go.jacobcolvin.com/niceyaml/schema/matcher"
)

// Metadata names a manifest.
type Metadata struct {
	Labels map[string]string `json:"labels,omitzero"`
	Name   string            `json:"name"`
}

// Cafe is the singleton kind of the test registry.
type Cafe struct {
	Metadata Metadata `json:"metadata"`
	Spec     CafeSpec `json:"spec"`
}

// CafeSpec describes the café.
type CafeSpec struct {
	Currency string `json:"currency,omitzero"`
	Seats    int    `json:"seats,omitzero"`
}

// Hours is a kind with a deprecated version.
type Hours struct {
	Metadata Metadata  `json:"metadata"`
	Spec     HoursSpec `json:"spec"`
}

// HoursSpec describes opening hours.
type HoursSpec struct {
	Open  string `json:"open,omitzero"`
	Close string `json:"close,omitzero"`
}

// MenuItem is a kind whose older version called it Dish.
type MenuItem struct {
	Metadata Metadata     `json:"metadata"`
	Spec     MenuItemSpec `json:"spec"`
}

// MenuItemSpec describes a menu item.
type MenuItemSpec struct {
	Tags          []string `json:"tags,omitzero"`
	Price         int      `json:"price,omitzero"`
	LegacyPricing bool     `json:"legacyPricing,omitzero"`
}

// Tip is a kind whose current version is deprecated.
type Tip struct {
	Metadata Metadata `json:"metadata"`
}

// Common holds fields that several specs embed.
type Common struct {
	Note string `json:"note,omitzero"`
}

// plainEmbed embeds a struct without an inline option, so documents nest
// its fields under common.
type plainEmbed struct {
	Common

	Price int `json:"price"`
}

// namedInline gives a named field an inline option.
type namedInline struct {
	Extra Common `yaml:",inline"`
}

// looseItem holds each key that no other field reads in an inline map.
type looseItem struct {
	Extra map[string]any `yaml:",inline"`
	Price int            `json:"price"`
}

// migrateHint is the migrate hint of the test registries.
const migrateHint = `run "cafe config migrate" to update this file`

// itemSchema requires a positive price.
const itemSchema = `{
  "type": "object",
  "properties": {
    "metadata": {"type": "object"},
    "spec": {
      "type": "object",
      "properties": {"price": {"type": "integer", "minimum": 1}}
    }
  }
}`

var (
	meta = paths.Doc().Child("metadata")
	spec = paths.Doc().Child("spec")
)

// rename returns a step that renames the key of each value it gets.
func rename(key string) edit.Func {
	return func(_ context.Context, n *edit.Node) error {
		return n.SetKey(key)
	}
}

// tagList is a step that turns a single tag into a list of one.
func tagList(ctx context.Context, n *edit.Node) error {
	tag, err := n.Decode[string](ctx)
	if err != nil {
		return err
	}

	return n.Set(ctx, []string{tag})
}

// cafeOptions returns the options of the registry that most tests load
// with, followed by extra.
func cafeOptions(extra ...manifest.RegistryOption) []manifest.RegistryOption {
	return append([]manifest.RegistryOption{
		manifest.WithNameField(meta.Child("name")),
		manifest.WithAppName("cafe"),
		manifest.WithMigrateHint(migrateHint),
		manifest.WithRemoved("cafe.jacobcolvin.com/v1alpha1", "Dish",
			"cafe 3.0 removed Dish v1alpha1, see https://jacobcolvin.com/cafe/upgrade/3"),
		manifest.WithKinds(
			manifest.NewKind[Cafe]("Cafe",
				manifest.Required(),
				manifest.Singleton(),
				manifest.Version("cafe.jacobcolvin.com/v1"),
			),
			manifest.NewKind[Hours]("Hours",
				manifest.Version("cafe.jacobcolvin.com/v1alpha1",
					manifest.Deprecated("cafe 4.0 removes cafe.jacobcolvin.com/v1alpha1"),
				),
				manifest.Version("cafe.jacobcolvin.com/v1",
					manifest.Migrate(spec.Child("opens"), rename("open")),
					manifest.Migrate(spec.Child("closes"), rename("close")),
				),
			),
			manifest.NewKind[MenuItem]("MenuItem",
				manifest.Version("cafe.jacobcolvin.com/v1beta1", manifest.KindName("Dish")),
				manifest.Version("cafe.jacobcolvin.com/v1",
					manifest.Schema(schema.Embedded([]byte(itemSchema))),
					manifest.Migrate(spec.Child("cost"), rename("price")),
					manifest.Migrate(spec.Child("tags"), tagList),
				),
			),
		),
	}, extra...)
}

// withKinds returns the options of a registry that holds kinds and
// nothing else.
func withKinds(kinds ...*manifest.Kind) []manifest.RegistryOption {
	return []manifest.RegistryOption{manifest.WithKinds(kinds...)}
}

// newCafeRegistry returns the registry of [cafeOptions].
func newCafeRegistry(t *testing.T, extra ...manifest.RegistryOption) *manifest.Registry {
	t.Helper()

	reg, err := manifest.NewRegistry(cafeOptions(extra...)...)
	require.NoError(t, err)

	return reg
}

func TestNewRegistry(t *testing.T) {
	t.Parallel()

	v1 := manifest.Version("v1")

	tcs := map[string]struct {
		opts []manifest.RegistryOption
		err  string
	}{
		"valid": {
			opts: cafeOptions(),
		},
		"empty registry": {},
		"kind without versions": {
			opts: withKinds(manifest.NewKind[Cafe]("Cafe")),
			err:  "kind Cafe has no version",
		},
		"two kinds of one name": {
			opts: withKinds(
				manifest.NewKind[Cafe]("Cafe", v1),
				manifest.NewKind[Hours]("Cafe", v1),
			),
			err: `kinds Cafe and Cafe both answer to the name "Cafe"`,
		},
		"two kinds of one type": {
			opts: withKinds(
				manifest.NewKind[Cafe]("Cafe", v1),
				manifest.NewKind[Cafe]("Bistro", v1),
			),
			err: "kinds Cafe and Bistro share the Go type manifest_test.Cafe",
		},
		"version listed twice": {
			opts: withKinds(manifest.NewKind[Cafe]("Cafe",
				v1, manifest.Version("v1"))),
			err: "kind Cafe lists v1 twice",
		},
		"two written versions": {
			opts: withKinds(manifest.NewKind[Hours]("Hours",
				manifest.Version("cafe.jacobcolvin.com/v1", manifest.Written()),
				manifest.Version("cafe.jacobcolvin.com/v2", manifest.Written()),
				manifest.Version("cafe.jacobcolvin.com/v3"),
			)),
			err: "Hours carries manifest.Written on cafe.jacobcolvin.com/v1 and on cafe.jacobcolvin.com/v2",
		},
		"written and loosened": {
			opts: withKinds(manifest.NewKind[Hours]("Hours",
				manifest.Version("v1"),
				manifest.Version("v2", manifest.Written(), manifest.Loosened()),
				manifest.Version("v3"),
			)),
			err: "kind Hours: v2 carries manifest.Loosened",
		},
		"step on the oldest version": {
			opts: withKinds(manifest.NewKind[Hours]("Hours",
				manifest.Version("v1", manifest.Migrate(spec.Child("opens"), rename("open"))),
			)),
			err: "kind Hours: v1 is the oldest version, so it takes no manifest.Migrate",
		},
		"step on the unversioned layout": {
			opts: withKinds(manifest.NewKind[Hours]("Hours",
				manifest.Unversioned(manifest.Migrate(spec.Child("opens"), rename("open"))),
			)),
			err: "kind Hours: the unversioned layout takes no manifest.Migrate, because no version comes before it",
		},
		"step with a relative path": {
			opts: withKinds(manifest.NewKind[Hours]("Hours",
				manifest.Version("v1"),
				manifest.Version("v2", manifest.Migrate(paths.Current().Child("opens"), rename("open"))),
			)),
			err: "kind Hours: step 1 of v2 takes a $ path, not @.opens",
		},
		"step with a key path": {
			opts: withKinds(manifest.NewKind[Hours]("Hours",
				manifest.Version("v1"),
				manifest.Version("v2", manifest.Migrate(spec.Child("opens").Key(), rename("open"))),
			)),
			err: "kind Hours: step 1 of v2 takes a path without ~, not $.spec.opens~",
		},
		"step without a function": {
			opts: withKinds(manifest.NewKind[Hours]("Hours",
				manifest.Version("v1"),
				manifest.Version("v2", manifest.Migrate(spec.Child("opens"), nil)),
			)),
			err: "kind Hours: step 1 of v2 has a nil function",
		},
		"kind name on current version": {
			opts: withKinds(manifest.NewKind[MenuItem]("MenuItem",
				manifest.Version("v1", manifest.KindName("Dish")),
			)),
			err: "kind MenuItem: the current version v1 takes no manifest.KindName",
		},
		"kind name that another kind uses": {
			opts: withKinds(
				manifest.NewKind[Cafe]("Cafe", v1),
				manifest.NewKind[MenuItem]("MenuItem", manifest.Version("v0", manifest.KindName("Cafe")), v1),
			),
			err: `kind MenuItem: v0 gives the name "Cafe", which kind Cafe uses`,
		},
		"unversioned after a version": {
			opts: withKinds(manifest.NewKind[Cafe]("Cafe",
				v1, manifest.Unversioned(),
			)),
			err: "kind Cafe: manifest.Unversioned comes first among the versions",
		},
		"version without a name": {
			opts: withKinds(manifest.NewKind[Cafe]("Cafe", manifest.Version(""))),
			err:  "kind Cafe: manifest.Version needs a name",
		},
		"kind name on the unversioned layout": {
			opts: withKinds(manifest.NewKind[Cafe]("Cafe",
				manifest.Unversioned(manifest.KindName("Bistro")), v1,
			)),
			err: "kind Cafe: the unversioned layout takes no manifest.KindName",
		},
		"zero schema ref": {
			opts: withKinds(manifest.NewKind[Cafe]("Cafe",
				manifest.Version("v1", manifest.Schema(schema.Ref{})),
			)),
			err: "kind Cafe: v1 has a schema.Ref that names no schema",
		},
		"schema type fields without a schema": {
			opts: withKinds(manifest.NewKind[Cafe]("Cafe",
				manifest.Version("v1", manifest.SchemaTypeFields(true)),
			)),
			err: "kind Cafe: v1 takes no manifest.SchemaTypeFields without manifest.Schema",
		},
		"schema type fields on the unversioned layout": {
			opts: withKinds(manifest.NewKind[Cafe]("Cafe",
				manifest.Unversioned(manifest.Schema(schema.Embedded([]byte(`{}`))), manifest.SchemaTypeFields(false)),
				v1,
			)),
		},
		"schema type fields on the unversioned layout without a schema": {
			opts: withKinds(manifest.NewKind[Cafe]("Cafe",
				manifest.Unversioned(manifest.SchemaTypeFields(true)),
				v1,
			)),
			err: "kind Cafe: the unversioned layout takes no manifest.SchemaTypeFields without manifest.Schema",
		},
		"schema type fields beside a schema": {
			opts: withKinds(manifest.NewKind[Cafe](
				"Cafe",
				manifest.Version(
					"v1",
					manifest.Schema(schema.Embedded([]byte(`{}`))),
					manifest.SchemaTypeFields(false),
				),
			)),
		},
		"deprecated older version without written": {
			opts: withKinds(manifest.NewKind[Hours]("Hours",
				manifest.Version("v1", manifest.Deprecated()),
				manifest.Version("v2"),
			)),
		},
		"loosened only version": {
			opts: withKinds(manifest.NewKind[Hours]("Hours", manifest.Version("v2", manifest.Loosened()))),
		},
		"loosened only version beside a removal of the old one": {
			opts: []manifest.RegistryOption{
				manifest.WithRemoved("v1", "Hours"),
				manifest.WithKinds(manifest.NewKind[Hours]("Hours", manifest.Version("v2", manifest.Loosened()))),
			},
		},
		"deprecated current version of a required kind": {
			opts: withKinds(manifest.NewKind[Cafe]("Cafe",
				manifest.Required(),
				manifest.Version("v1", manifest.Deprecated()),
			)),
			err: "kind Cafe: manifest.Deprecated on the current version of a Required kind",
		},
		"deprecated removed version": {
			opts: []manifest.RegistryOption{
				manifest.WithRemoved("v1", "Hours"),
				manifest.WithKinds(manifest.NewKind[Hours]("Hours",
					manifest.Version("v1", manifest.Deprecated()),
					manifest.Version("v2"),
				)),
			},
			err: "kind Hours: v1 carries manifest.Deprecated, but manifest.WithRemoved names it",
		},
		"two removals of one pair": {
			opts: []manifest.RegistryOption{
				manifest.WithRemoved("v0", "Hours"),
				manifest.WithRemoved("v0", "Hours"),
				manifest.WithKinds(manifest.NewKind[Hours]("Hours", v1)),
			},
			err: "manifest.WithRemoved names v0 of Hours twice",
		},
		"removal of the written version": {
			opts: []manifest.RegistryOption{
				manifest.WithRemoved("v1", "Hours"),
				manifest.WithKinds(manifest.NewKind[Hours]("Hours", v1)),
			},
			err: "manifest.WithRemoved names v1 of Hours, which is the written version of Hours or newer",
		},
		"removal of the current version": {
			opts: []manifest.RegistryOption{
				manifest.WithRemoved("v2", "Hours"),
				manifest.WithKinds(manifest.NewKind[Hours]("Hours", v1, manifest.Version("v2"))),
			},
			err: "manifest.WithRemoved names v2 of Hours, which is the written version of Hours or newer",
		},
		"removal of an older version that the kind lists": {
			opts: []manifest.RegistryOption{
				manifest.WithRemoved("v1", "Hours"),
				manifest.WithKinds(manifest.NewKind[Hours]("Hours", v1, manifest.Version("v2"))),
			},
		},
		"removal under a name the version does not give": {
			opts: []manifest.RegistryOption{
				manifest.WithRemoved("v1", "Dish"),
				manifest.WithKinds(manifest.NewKind[MenuItem]("MenuItem",
					manifest.Version("v0", manifest.KindName("Dish")),
					manifest.Version("v1"),
					manifest.Version("v2"),
				)),
			},
			err: `manifest.WithRemoved names v1 of Dish, but that version calls the kind "MenuItem"`,
		},
		"nil ignore": {
			opts: []manifest.RegistryOption{
				manifest.WithIgnore(nil),
				manifest.WithKinds(manifest.NewKind[Cafe]("Cafe", v1)),
			},
			err: "manifest.WithIgnore got a nil matcher",
		},
		"nil check": {
			opts: []manifest.RegistryOption{
				manifest.WithCheck(nil),
				manifest.WithKinds(manifest.NewKind[Cafe]("Cafe", v1)),
			},
			err: "manifest.WithCheck got a nil function",
		},
		"version only beside a kind field": {
			opts: []manifest.RegistryOption{
				manifest.WithVersionOnly(paths.Doc().Child("version")),
				manifest.WithKindField(paths.Doc().Child("type")),
				manifest.WithKinds(manifest.NewKind[Cafe]("Cafe", v1)),
			},
			err: "manifest.WithVersionOnly names the only type field, so it takes no manifest.WithKindField",
		},
		"version only with two kinds": {
			opts: []manifest.RegistryOption{
				manifest.WithVersionOnly(paths.Doc().Child("version")),
				manifest.WithKinds(manifest.NewKind[Cafe]("Cafe", v1), manifest.NewKind[Hours]("Hours", v1)),
			},
			err: "so the registry holds one kind, not 2",
		},
		"version only beside a claim": {
			opts: []manifest.RegistryOption{
				manifest.WithVersionOnly(paths.Doc().Child("version")),
				manifest.WithKinds(manifest.NewKind[Cafe]("Cafe", manifest.Claim(matcher.All()), v1)),
			},
			err: "kind Cafe: manifest.WithVersionOnly names the only type field, so it takes no manifest.Claim",
		},
		"one path for two fields": {
			opts: []manifest.RegistryOption{
				manifest.WithNameField(paths.Doc().Child("kind")),
				manifest.WithKinds(manifest.NewKind[Cafe]("Cafe", v1)),
			},
			err: "manifest.WithKindField and manifest.WithNameField name the same path $.kind",
		},
		"field path with a wildcard": {
			opts: []manifest.RegistryOption{
				manifest.WithNameField(paths.Doc().Child("metadata").ChildAll()),
				manifest.WithKinds(manifest.NewKind[Cafe]("Cafe", v1)),
			},
			err: "manifest.WithNameField takes a $ path of names and indexes, not $.metadata.*",
		},
		"nil claim": {
			opts: withKinds(manifest.NewKind[Cafe]("Cafe", manifest.Claim(nil), v1)),
			err:  "kind Cafe: manifest.Claim got a nil matcher",
		},
		"two unversioned kinds without a claim": {
			// The kind field picks between them.
			opts: withKinds(
				manifest.NewKind[Cafe]("Cafe", manifest.Unversioned(), v1),
				manifest.NewKind[Hours]("Hours", manifest.Unversioned(), v1),
			),
		},
		"unversioned removal beside an unversioned kind": {
			opts: []manifest.RegistryOption{
				manifest.WithRemoved("", "Hours"),
				manifest.WithKinds(
					manifest.NewKind[Cafe]("Cafe", manifest.Unversioned(), v1),
					manifest.NewKind[Hours]("Hours", v1),
				),
			},
		},
		"kinds without a named version": {
			opts: withKinds(
				manifest.NewKind[Cafe]("Cafe", manifest.Unversioned()),
				manifest.NewKind[Hours]("Hours", manifest.Unversioned()),
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reg, err := manifest.NewRegistry(tc.opts...)
			if tc.err == "" {
				require.NoError(t, err)
				assert.NotNil(t, reg)

				return
			}

			require.Error(t, err)
			assert.Nil(t, reg)
			assert.Contains(t, err.Error(), tc.err)
		})
	}
}

func TestNewRegistry_Layouts(t *testing.T) {
	t.Parallel()

	v1 := manifest.Version("v1")

	tcs := map[string]struct {
		// The value of the one manifest of the load, which get returns.
		want any
		get  func(set *manifest.Set) (any, bool)
		// The text of the manifest.
		input string
		opts  []manifest.RegistryOption
	}{
		"embedded struct without an inline option": {
			opts:  withKinds(manifest.NewKind[plainEmbed]("Item", manifest.Singleton(), v1)),
			input: "apiVersion: v1\nkind: Item\ncommon:\n  note: hot\nprice: 3\n",
			get:   oneValue[plainEmbed],
			want:  plainEmbed{Common: Common{Note: "hot"}, Price: 3},
		},
		"named field with an inline option": {
			opts:  withKinds(manifest.NewKind[namedInline]("Item", manifest.Singleton(), v1)),
			input: "apiVersion: v1\nkind: Item\nnote: hot\n",
			get:   oneValue[namedInline],
			want:  namedInline{Extra: Common{Note: "hot"}},
		},
		// The go-yaml decoder panics on an inline map when it rejects
		// unknown fields, so these registries warn about unknown fields
		// or ignore them. The search for unknown fields reads each key of
		// the map as known. Under UnknownFieldsWarn, the decode cuts the
		// type fields out of its view, so the map leaves out apiVersion
		// and kind. Under UnknownFieldsIgnore, the decode cuts nothing,
		// so the map holds them.
		"inline map, unknown fields warn": {
			opts: append(
				withKinds(manifest.NewKind[looseItem]("Item", manifest.Singleton(), v1)),
				manifest.WithDecodeOptions(niceyaml.WithUnknownFields(niceyaml.UnknownFieldsWarn)),
			),
			input: "apiVersion: v1\nkind: Item\nprice: 3\nsize: large\n",
			get:   oneValue[looseItem],
			want:  looseItem{Price: 3, Extra: map[string]any{"price": uint64(3), "size": "large"}},
		},
		"inline map, unknown fields ignore": {
			opts: append(
				withKinds(manifest.NewKind[looseItem]("Item", manifest.Singleton(), v1)),
				manifest.WithDecodeOptions(niceyaml.WithUnknownFields(niceyaml.UnknownFieldsIgnore)),
			),
			input: "apiVersion: v1\nkind: Item\nprice: 3\nsize: large\n",
			get:   oneValue[looseItem],
			want: looseItem{Price: 3, Extra: map[string]any{
				"apiVersion": "v1", "kind": "Item", "price": uint64(3), "size": "large",
			}},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reg, err := manifest.NewRegistry(tc.opts...)
			require.NoError(t, err)

			set, err := reg.Load(t.Context(), configFS(map[string]string{"item.yaml": tc.input}))
			require.NoError(t, err)
			require.NoError(t, set.Warnings())

			got, ok := tc.get(set)
			require.True(t, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

// oneValue returns the value of the one manifest of set whose kind has
// the Go type T.
func oneValue[T any](set *manifest.Set) (any, bool) {
	obj, ok := set.One[T]()

	return obj.Value, ok
}

func TestNewRegistry_Advice(t *testing.T) {
	t.Parallel()

	const (
		deprecated = "carries manifest.Deprecated, but a rewrite does not move a document off the written " +
			"version or a newer one; "
		loosened = "carries manifest.Loosened, so an earlier release rejects what a rewrite writes there; "
		current  = "is the current version, which a rewrite writes without manifest.Written; "
	)

	tcs := map[string]struct {
		opts []manifest.RegistryOption
		err  string
	}{
		"written on the current version": {
			opts: withKinds(manifest.NewKind[Hours]("Hours",
				manifest.Version("v1"),
				manifest.Version("v2", manifest.Written()),
			)),
			err: "manifest: kind Hours: v2 " + current + "drop manifest.Written, or move it to v1",
		},
		"written on the only version": {
			opts: withKinds(manifest.NewKind[Hours]("Hours", manifest.Version("v1", manifest.Written()))),
			err:  "manifest: kind Hours: v1 " + current + "drop manifest.Written",
		},
		"written on the current version after a loosened version": {
			opts: withKinds(manifest.NewKind[Hours]("Hours",
				manifest.Version("v1", manifest.Loosened()),
				manifest.Version("v2", manifest.Written()),
			)),
			err: "manifest: kind Hours: v2 " + current + "drop manifest.Written",
		},
		"written on the current version beside a removal of the older one": {
			opts: []manifest.RegistryOption{
				manifest.WithRemoved("v1", "Hours"),
				manifest.WithKinds(manifest.NewKind[Hours]("Hours",
					manifest.Version("v1"),
					manifest.Version("v2", manifest.Written()),
				)),
			},
			err: "manifest: kind Hours: v2 " + current + "drop manifest.Written",
		},
		"written on a loosened current version": {
			opts: withKinds(manifest.NewKind[Hours]("Hours",
				manifest.Version("v1"),
				manifest.Version("v2", manifest.Written(), manifest.Loosened()),
			)),
			err: "manifest: kind Hours: v2 " + current + "move manifest.Written to v1",
		},
		"written on a loosened current version after a loosened version": {
			opts: withKinds(manifest.NewKind[Hours]("Hours",
				manifest.Version("v1", manifest.Loosened()),
				manifest.Version("v2", manifest.Written(), manifest.Loosened()),
			)),
			err: "manifest: kind Hours: v2 " + current + "drop manifest.Written and manifest.Loosened from v2",
		},
		"written on the only version, which carries loosened": {
			opts: withKinds(manifest.NewKind[Hours]("Hours",
				manifest.Version("v1", manifest.Written(), manifest.Loosened()),
			)),
			err: "manifest: kind Hours: v1 " + current + "drop manifest.Written",
		},
		"written and loosened after a loosened version": {
			opts: withKinds(manifest.NewKind[Hours]("Hours",
				manifest.Version("v1", manifest.Loosened()),
				manifest.Version("v2", manifest.Written(), manifest.Loosened()),
				manifest.Version("v3"),
			)),
			err: "manifest: kind Hours: v2 " + loosened + "drop manifest.Written",
		},
		"written and loosened between versions that Written fits": {
			opts: withKinds(manifest.NewKind[Hours]("Hours",
				manifest.Version("v1"),
				manifest.Version("v2", manifest.Written(), manifest.Loosened()),
				manifest.Version("v3"),
				manifest.Version("v4"),
			)),
			err: "manifest: kind Hours: v2 " + loosened + "drop manifest.Written, or move it to v1 or v3",
		},
		"written and loosened before a loosened current version": {
			opts: withKinds(manifest.NewKind[Hours]("Hours",
				manifest.Version("v1"),
				manifest.Version("v2", manifest.Written(), manifest.Loosened()),
				manifest.Version("v3", manifest.Loosened()),
			)),
			err: "manifest: kind Hours: v2 " + loosened + "move manifest.Written to v1",
		},
		"written and loosened beside a removal before a loosened current version": {
			opts: []manifest.RegistryOption{
				manifest.WithRemoved("v1", "Hours"),
				manifest.WithKinds(manifest.NewKind[Hours]("Hours",
					manifest.Version("v1"),
					manifest.Version("v2", manifest.Written(), manifest.Loosened()),
					manifest.Version("v3", manifest.Loosened()),
				)),
			},
			err: "manifest: kind Hours: v2 " + loosened + "drop manifest.Written from v2 and manifest.Loosened from v3",
		},
		"deprecated written version": {
			opts: withKinds(manifest.NewKind[Hours]("Hours",
				manifest.Version("v1", manifest.Deprecated(), manifest.Written()),
				manifest.Version("v2"),
			)),
			err: "manifest: kind Hours: v1 " + deprecated + "drop manifest.Written",
		},
		"deprecated written version before two newer ones": {
			opts: withKinds(manifest.NewKind[Hours]("Hours",
				manifest.Version("v1", manifest.Deprecated(), manifest.Written()),
				manifest.Version("v2"),
				manifest.Version("v3"),
			)),
			err: "manifest: kind Hours: v1 " + deprecated + "drop manifest.Written, or move it to v2",
		},
		"deprecated written version before a loosened current version": {
			opts: withKinds(manifest.NewKind[Hours]("Hours",
				manifest.Version("v1", manifest.Deprecated(), manifest.Written()),
				manifest.Version("v2", manifest.Loosened()),
			)),
			err: "manifest: kind Hours: v1 " + deprecated +
				"drop manifest.Deprecated from v1, or drop manifest.Written from v1 and manifest.Loosened from v2",
		},
		"deprecated version after the written one before a loosened current version": {
			opts: withKinds(manifest.NewKind[Hours]("Hours",
				manifest.Version("v1", manifest.Written()),
				manifest.Version("v2", manifest.Deprecated()),
				manifest.Version("v3", manifest.Loosened()),
			)),
			err: "manifest: kind Hours: v2 " + deprecated +
				"drop manifest.Deprecated from v2, or drop manifest.Written from v1 and manifest.Loosened from v3",
		},
		"deprecated written version before a loosened version": {
			opts: withKinds(manifest.NewKind[Hours]("Hours",
				manifest.Version("v1", manifest.Deprecated(), manifest.Written()),
				manifest.Version("v2", manifest.Loosened()),
				manifest.Version("v3"),
			)),
			err: "manifest: kind Hours: v1 " + deprecated + "drop manifest.Written",
		},
		"deprecated written version before a version that Written fits and a loosened current version": {
			opts: withKinds(manifest.NewKind[Hours]("Hours",
				manifest.Version("v1", manifest.Deprecated(), manifest.Written()),
				manifest.Version("v2"),
				manifest.Version("v3", manifest.Loosened()),
			)),
			err: "manifest: kind Hours: v1 " + deprecated + "move manifest.Written to v2",
		},
		"loosened current version without written": {
			opts: withKinds(manifest.NewKind[Hours]("Hours",
				manifest.Version("v1"),
				manifest.Version("v2", manifest.Loosened()),
			)),
			err: "manifest: kind Hours: v2 " + loosened + "mark v1 with manifest.Written",
		},
		"loosened current version after two versions": {
			opts: withKinds(manifest.NewKind[Hours]("Hours",
				manifest.Version("v1"),
				manifest.Version("v2"),
				manifest.Version("v3", manifest.Loosened()),
			)),
			err: "manifest: kind Hours: v3 " + loosened + "mark v1 or v2 with manifest.Written",
		},
		"loosened current version beside a removal of the older one": {
			opts: []manifest.RegistryOption{
				manifest.WithRemoved("v1", "Hours"),
				manifest.WithKinds(manifest.NewKind[Hours]("Hours",
					manifest.Version("v1"),
					manifest.Version("v2", manifest.Loosened()),
				)),
			},
			err: "manifest: kind Hours: v2 " + loosened +
				"stop listing the versions that manifest.WithRemoved names, or drop manifest.Loosened",
		},
		"loosened current version with steps beside a removal of the older one": {
			opts: []manifest.RegistryOption{
				manifest.WithRemoved("v1", "Hours"),
				manifest.WithKinds(manifest.NewKind[Hours]("Hours",
					manifest.Version("v1"),
					manifest.Version("v2", manifest.Loosened(), manifest.Migrate(spec.Child("shut"), rename("close"))),
				)),
			},
			err: "manifest: kind Hours: v2 " + loosened +
				"stop listing the versions that manifest.WithRemoved names and drop the manifest.Migrate steps of v2, " +
				"or drop manifest.Loosened",
		},
		"loosened current version beside a removal of the version before it": {
			opts: []manifest.RegistryOption{
				manifest.WithRemoved("v2", "Hours"),
				manifest.WithKinds(manifest.NewKind[Hours]("Hours",
					manifest.Version("v1"),
					manifest.Version("v2"),
					manifest.Version("v3", manifest.Loosened()),
				)),
			},
			err: "manifest: kind Hours: v3 " + loosened +
				"stop listing v2 and mark v1 with manifest.Written, or drop manifest.Loosened",
		},
		"loosened current version beside removals of the versions before it": {
			opts: []manifest.RegistryOption{
				manifest.WithRemoved("v2", "Hours"),
				manifest.WithRemoved("v3", "Hours"),
				manifest.WithKinds(manifest.NewKind[Hours]("Hours",
					manifest.Version("v1"),
					manifest.Version("v2"),
					manifest.Version("v3"),
					manifest.Version("v4", manifest.Loosened()),
				)),
			},
			err: "manifest: kind Hours: v4 " + loosened +
				"stop listing v2 and v3 and mark v1 with manifest.Written, or drop manifest.Loosened",
		},
		"loosened current version beside a removal of a version with steps": {
			opts: []manifest.RegistryOption{
				manifest.WithRemoved("v2", "Hours"),
				manifest.WithKinds(manifest.NewKind[Hours]("Hours",
					manifest.Version("v1"),
					manifest.Version("v2", manifest.Migrate(spec.Child("shut"), rename("close"))),
					manifest.Version("v3", manifest.Loosened()),
				)),
			},
			err: "manifest: kind Hours: v3 " + loosened + "drop manifest.Loosened",
		},
		"loosened current version beside a removal of a version with a schema": {
			opts: []manifest.RegistryOption{
				manifest.WithRemoved("v2", "Hours"),
				manifest.WithKinds(manifest.NewKind[Hours]("Hours",
					manifest.Version("v1"),
					manifest.Version("v2", manifest.Schema(schema.Embedded([]byte(`{"type": "object"}`)))),
					manifest.Version("v3", manifest.Loosened()),
				)),
			},
			err: "manifest: kind Hours: v3 " + loosened + "drop manifest.Loosened",
		},
		"loosened current version beside removals around a version with steps": {
			opts: []manifest.RegistryOption{
				manifest.WithRemoved("v1", "Hours"),
				manifest.WithRemoved("v3", "Hours"),
				manifest.WithKinds(manifest.NewKind[Hours]("Hours",
					manifest.Version("v1"),
					manifest.Version("v2", manifest.Migrate(spec.Child("shut"), rename("close"))),
					manifest.Version("v3"),
					manifest.Version("v4", manifest.Loosened()),
				)),
			},
			err: "manifest: kind Hours: v4 " + loosened +
				"stop listing v3 and mark v2 with manifest.Written, or drop manifest.Loosened",
		},
		"loosened current version beside a removal with steps and a removal without": {
			opts: []manifest.RegistryOption{
				manifest.WithRemoved("v2", "Hours"),
				manifest.WithRemoved("v4", "Hours"),
				manifest.WithKinds(manifest.NewKind[Hours]("Hours",
					manifest.Version("v1"),
					manifest.Version("v2", manifest.Migrate(spec.Child("shut"), rename("close"))),
					manifest.Version("v3"),
					manifest.Version("v4"),
					manifest.Version("v5", manifest.Loosened()),
				)),
			},
			err: "manifest: kind Hours: v5 " + loosened +
				"stop listing v4 and mark v3 with manifest.Written, or drop manifest.Loosened",
		},
		"loosened current version beside a removal of the oldest version": {
			opts: []manifest.RegistryOption{
				manifest.WithRemoved("v1", "Hours"),
				manifest.WithKinds(manifest.NewKind[Hours]("Hours",
					manifest.Version("v1"),
					manifest.Version("v2"),
					manifest.Version("v3", manifest.Loosened()),
				)),
			},
			err: "manifest: kind Hours: v3 " + loosened + "mark v2 with manifest.Written",
		},
		"loosened current version after a removal and a loosened version": {
			opts: []manifest.RegistryOption{
				manifest.WithRemoved("v1", "Hours"),
				manifest.WithKinds(manifest.NewKind[Hours]("Hours",
					manifest.Version("v1"),
					manifest.Version("v2", manifest.Loosened()),
					manifest.Version("v3", manifest.Loosened()),
				)),
			},
			err: "manifest: kind Hours: v3 " + loosened + "drop manifest.Loosened",
		},
		"loosened current version after a deprecated version": {
			opts: withKinds(manifest.NewKind[Hours]("Hours",
				manifest.Version("v1"),
				manifest.Version("v2", manifest.Deprecated()),
				manifest.Version("v3", manifest.Loosened()),
			)),
			err: "manifest: kind Hours: v3 " + loosened + "drop manifest.Loosened",
		},
		"loosened current version after a deprecated oldest version": {
			opts: withKinds(manifest.NewKind[Hours]("Hours",
				manifest.Version("v1", manifest.Deprecated()),
				manifest.Version("v2", manifest.Loosened()),
			)),
			err: "manifest: kind Hours: v2 " + loosened + "drop manifest.Loosened",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reg, err := manifest.NewRegistry(tc.opts...)
			require.EqualError(t, err, tc.err)
			assert.Nil(t, reg)
		})
	}
}

func TestMustNewRegistry(t *testing.T) {
	t.Parallel()

	assert.NotNil(t, manifest.MustNewRegistry(cafeOptions()...))
	assert.PanicsWithError(t, "manifest: kind Cafe has no version", func() {
		manifest.MustNewRegistry(manifest.WithKinds(manifest.NewKind[Cafe]("Cafe")))
	})
}

func TestWithSchemaRegistry(t *testing.T) {
	t.Parallel()

	const (
		priceSchema = `{"properties": {"spec": {"properties": {"price": {"minimum": 1}}}}}`
		kindSchema  = `{"properties": {"kind": {"const": "Other"}}}`
	)

	menu := stringtest.LinesLF(
		"apiVersion: cafe.jacobcolvin.com/v1",
		"kind: MenuItem",
		"metadata:",
		"  name: semla",
		"spec:",
		"  price: 0",
	)

	// NewRegistry returns a registry whose MenuItem v1 reads its schema
	// from schema.json in fsys, through schemas.
	newRegistry := func(t *testing.T, fsys fstest.MapFS, schemas *schema.Registry) *manifest.Registry {
		t.Helper()

		return manifest.MustNewRegistry(
			manifest.WithSchemaRegistry(schemas),
			manifest.WithKinds(manifest.NewKind[MenuItem]("MenuItem",
				manifest.Version("cafe.jacobcolvin.com/v1", manifest.Schema(schema.FileFS(fsys, "schema.json"))),
			)),
		)
	}

	t.Run("loads and caches through the registry", func(t *testing.T) {
		t.Parallel()

		fsys := fstest.MapFS{"schema.json": {Data: []byte(priceSchema)}}
		schemas := schema.NewRegistry()
		reg := newRegistry(t, fsys, schemas)
		in := configFS(map[string]string{"menu.yaml": menu})

		_, err := reg.Load(t.Context(), in)
		require.EqualError(t, err, "config/menu.yaml:6:10: $.spec.price: 0 is less than 1")
		require.ErrorIs(t, err, schema.ErrViolation)

		// The schema of the file lives in the cache of the caller's
		// registry. So a change to the file reaches a load only after
		// the caller invalidates it.
		fsys["schema.json"] = &fstest.MapFile{Data: []byte(`{}`)}

		_, err = reg.Load(t.Context(), in)
		require.ErrorIs(t, err, schema.ErrViolation)

		schemas.Invalidate(schema.FileFS(fsys, "schema.json"))

		_, err = reg.Load(t.Context(), in)
		require.NoError(t, err)

		// The check sees the type fields that the new schema declares, so
		// the fields that the old schema hid do not stay hidden.
		fsys["schema.json"] = &fstest.MapFile{Data: []byte(kindSchema)}
		schemas.Invalidate(schema.FileFS(fsys, "schema.json"))

		_, err = reg.Load(t.Context(), in)
		require.EqualError(t, err, "config/menu.yaml:2:7: $.kind: value does not match const")
		require.ErrorIs(t, err, schema.ErrViolation)
	})

	t.Run("shares the registry", func(t *testing.T) {
		t.Parallel()

		fsys := fstest.MapFS{"schema.json": {Data: []byte(priceSchema)}}
		schemas := schema.NewRegistry()
		reg := newRegistry(t, fsys, schemas)

		require.NoError(t, reg.Verify(t.Context()))

		// Verify compiled the schema into the cache of the caller's
		// registry, so the caller finds it there.
		sch, err := schemas.Schema(t.Context(), schema.FileFS(fsys, "schema.json"))
		require.NoError(t, err)
		require.NotNil(t, sch.JSONSchema())
	})

	t.Run("nil keeps the default", func(t *testing.T) {
		t.Parallel()

		fsys := fstest.MapFS{"schema.json": {Data: []byte(priceSchema)}}
		reg := newRegistry(t, fsys, nil)

		_, err := reg.Load(t.Context(), configFS(map[string]string{"menu.yaml": menu}))
		require.ErrorIs(t, err, schema.ErrViolation)
	})
}

func TestWithMarshalers(t *testing.T) {
	t.Parallel()

	levelText := encoder.CustomMarshaler(func(_ context.Context, l AlarmLevel) (any, error) {
		return map[AlarmLevel]string{alarmLow: "low", alarmHigh: "high"}[l], nil
	})

	tcs := map[string]struct {
		err  error
		want string
		opts []manifest.RegistryOption
	}{
		"a step writes the level by name": {
			opts: []manifest.RegistryOption{manifest.WithMarshalers(levelText)},
			want: "high",
		},
		"without marshalers a step writes the level as a number": {
			// The load then cannot read the level that the step wrote.
			err: errUnknownAlarmLevel,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			level := spec.Child("level")
			reg := manifest.MustNewRegistry(append([]manifest.RegistryOption{
				manifest.WithNameField(meta.Child("name")),
				manifest.WithUnmarshalers(niceyaml.CustomUnmarshaler(decodeAlarmLevel)),
				manifest.WithKinds(manifest.NewKind[Alarm]("Alarm",
					manifest.Version("alarm.jacobcolvin.com/v1"),
					manifest.Version("alarm.jacobcolvin.com/v2",
						// The step decodes the level through the unmarshalers of
						// the registry and writes it back one level higher.
						manifest.Migrate(level, func(ctx context.Context, n *edit.Node) error {
							l, err := n.Decode[AlarmLevel](ctx)
							if err != nil {
								return err
							}

							return n.Set(ctx, l+1)
						}),
					),
				)),
			}, tc.opts...)...)

			set, err := reg.Load(t.Context(), manifest.Sources(niceyaml.NewSourceFromString(stringtest.LinesLF(
				"apiVersion: alarm.jacobcolvin.com/v1",
				"kind: Alarm",
				"metadata:",
				"  name: smoke",
				"spec:",
				"  level: low",
			))))
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				return
			}

			require.NoError(t, err)

			alarm, ok := set.Get[Alarm]("smoke")
			require.True(t, ok)

			got, err := alarm.Current.DecodeAt[string](t.Context(), level)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
