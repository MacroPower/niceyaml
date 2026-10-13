package manifest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

// HoursSpec describes opening hours. It reads the keys of both versions
// until the steps of phase 3 rename them.
type HoursSpec struct {
	Open   string `json:"open,omitzero"`
	Close  string `json:"close,omitzero"`
	Opens  string `json:"opens,omitzero"`
	Closes string `json:"closes,omitzero"`
}

// MenuItem is a kind whose older version called it Dish.
type MenuItem struct {
	Metadata Metadata     `json:"metadata"`
	Spec     MenuItemSpec `json:"spec"`
}

// MenuItemSpec describes a menu item.
type MenuItemSpec struct {
	Tags          any  `json:"tags,omitzero"`
	Price         int  `json:"price,omitzero"`
	Cost          int  `json:"cost,omitzero"`
	LegacyPricing bool `json:"legacyPricing,omitzero"`
}

// Tip is a kind whose current version is deprecated.
type Tip struct {
	Metadata Metadata `json:"metadata"`
}

// Common holds fields that several specs embed.
type Common struct {
	Note string `json:"note,omitzero"`
}

// badEmbed embeds a struct without an inline option.
type badEmbed struct {
	Common

	Price int `json:"price"`
}

// badInline gives a named field an inline option.
type badInline struct {
	Extra Common `yaml:",inline"`
}

// goodEmbed embeds a struct with an inline option.
type goodEmbed struct {
	Common `yaml:",inline"`

	Price int `json:"price"`
}

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

var meta = paths.Doc().Child("metadata")

// cafeOptions returns the options of the registry that most tests load
// with, followed by extra.
func cafeOptions(extra ...manifest.RegistryOption) []manifest.RegistryOption {
	return append([]manifest.RegistryOption{
		manifest.WithNameField(meta.Child("name")),
		manifest.WithAppName("cafe"),
		manifest.WithMigrateHint(`run "cafe config migrate" to update this file`),
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
				manifest.Version("cafe.jacobcolvin.com/v1", manifest.Written()),
			),
			manifest.NewKind[MenuItem]("MenuItem",
				manifest.Version("cafe.jacobcolvin.com/v1beta1", manifest.KindName("Dish")),
				manifest.Version("cafe.jacobcolvin.com/v1",
					manifest.Written(),
					manifest.Schema(schema.Embedded([]byte(itemSchema))),
				),
			),
		),
	}, extra...)
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
			opts: []manifest.RegistryOption{manifest.WithKinds(manifest.NewKind[Cafe]("Cafe"))},
			err:  "kind Cafe has no version",
		},
		"two kinds of one name": {
			opts: []manifest.RegistryOption{manifest.WithKinds(
				manifest.NewKind[Cafe]("Cafe", v1),
				manifest.NewKind[Hours]("Cafe", v1),
			)},
			err: `kinds Cafe and Cafe both answer to the name "Cafe"`,
		},
		"two kinds of one type": {
			opts: []manifest.RegistryOption{manifest.WithKinds(
				manifest.NewKind[Cafe]("Cafe", v1),
				manifest.NewKind[Cafe]("Bistro", v1),
			)},
			err: "kinds Cafe and Bistro share the Go type manifest_test.Cafe",
		},
		"version listed twice": {
			opts: []manifest.RegistryOption{manifest.WithKinds(manifest.NewKind[Cafe]("Cafe",
				manifest.Version("v1"), manifest.Version("v1", manifest.Written())))},
			err: "kind Cafe lists v1 twice",
		},
		"two written versions": {
			opts: []manifest.RegistryOption{manifest.WithKinds(manifest.NewKind[Hours]("Hours",
				manifest.Version("cafe.jacobcolvin.com/v1", manifest.Written()),
				manifest.Version("cafe.jacobcolvin.com/v2", manifest.Written()),
			))},
			err: "Hours carries manifest.Written on cafe.jacobcolvin.com/v1 and on cafe.jacobcolvin.com/v2",
		},
		"written and loosened": {
			opts: []manifest.RegistryOption{manifest.WithKinds(manifest.NewKind[Hours]("Hours",
				manifest.Version("v1"),
				manifest.Version("v2", manifest.Written(), manifest.Loosened()),
			))},
			err: "kind Hours: v2 carries manifest.Loosened",
		},
		"kind name on current version": {
			opts: []manifest.RegistryOption{manifest.WithKinds(manifest.NewKind[MenuItem]("MenuItem",
				manifest.Version("v1", manifest.KindName("Dish")),
			))},
			err: "kind MenuItem: the current version v1 takes no manifest.KindName",
		},
		"kind name that another kind uses": {
			opts: []manifest.RegistryOption{manifest.WithKinds(
				manifest.NewKind[Cafe]("Cafe", v1),
				manifest.NewKind[MenuItem]("MenuItem", manifest.Version("v0", manifest.KindName("Cafe")), v1),
			)},
			err: `kind MenuItem: v0 gives the name "Cafe", which kind Cafe uses`,
		},
		"unversioned after a version": {
			opts: []manifest.RegistryOption{manifest.WithKinds(manifest.NewKind[Cafe]("Cafe",
				v1, manifest.Unversioned(),
			))},
			err: "kind Cafe: manifest.Unversioned comes first among the versions",
		},
		"version without a name": {
			opts: []manifest.RegistryOption{manifest.WithKinds(manifest.NewKind[Cafe]("Cafe", manifest.Version("")))},
			err:  "kind Cafe: manifest.Version needs a name",
		},
		"kind name on the unversioned layout": {
			opts: []manifest.RegistryOption{manifest.WithKinds(manifest.NewKind[Cafe]("Cafe",
				manifest.Unversioned(manifest.KindName("Bistro")), v1,
			))},
			err: "kind Cafe: the unversioned layout takes no manifest.KindName",
		},
		"zero schema ref": {
			opts: []manifest.RegistryOption{manifest.WithKinds(manifest.NewKind[Cafe]("Cafe",
				manifest.Version("v1", manifest.Schema(schema.Ref{})),
			))},
			err: "kind Cafe: v1 has a schema.Ref that names no schema",
		},
		"deprecated written version": {
			opts: []manifest.RegistryOption{manifest.WithKinds(manifest.NewKind[Hours]("Hours",
				manifest.Version("v1", manifest.Deprecated()),
				manifest.Version("v2"),
			))},
			err: "kind Hours: v1 carries manifest.Deprecated, but a rewrite does not move a document off",
		},
		"deprecated current version of a required kind": {
			opts: []manifest.RegistryOption{manifest.WithKinds(manifest.NewKind[Cafe]("Cafe",
				manifest.Required(),
				manifest.Version("v1", manifest.Deprecated()),
			))},
			err: "kind Cafe: manifest.Deprecated on the current version of a Required kind",
		},
		"deprecated removed version": {
			opts: []manifest.RegistryOption{
				manifest.WithRemoved("v1", "Hours"),
				manifest.WithKinds(manifest.NewKind[Hours]("Hours",
					manifest.Version("v1", manifest.Deprecated()),
					manifest.Version("v2", manifest.Written()),
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
		"removal under a name the version does not give": {
			opts: []manifest.RegistryOption{
				manifest.WithRemoved("v1", "Dish"),
				manifest.WithKinds(manifest.NewKind[MenuItem]("MenuItem",
					manifest.Version("v0", manifest.KindName("Dish")),
					manifest.Version("v1"),
					manifest.Version("v2", manifest.Written()),
				)),
			},
			err: `manifest.WithRemoved names v1 of Dish, but that version calls the kind "MenuItem"`,
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
		"two unversioned kinds without a claim": {
			opts: []manifest.RegistryOption{manifest.WithKinds(
				manifest.NewKind[Cafe]("Cafe", manifest.Unversioned(), v1),
				manifest.NewKind[Hours]("Hours", manifest.Unversioned(), v1),
			)},
			err: "kinds Cafe and Hours read documents without type fields",
		},
		"unversioned removal beside an unversioned kind": {
			opts: []manifest.RegistryOption{
				manifest.WithRemoved("", "Hours"),
				manifest.WithKinds(
					manifest.NewKind[Cafe]("Cafe", manifest.Unversioned(), v1),
					manifest.NewKind[Hours]("Hours", v1),
				),
			},
			err: "kinds Cafe and Hours read documents without type fields",
		},
		"embedded struct without an inline option": {
			opts: []manifest.RegistryOption{manifest.WithKinds(manifest.NewKind[badEmbed]("Item", v1))},
			err: `kind Item: badEmbed.Common: embedded struct needs yaml:",inline", ` +
				"because gen reads it inline and the decode nests it",
		},
		"named field with an inline option": {
			opts: []manifest.RegistryOption{manifest.WithKinds(manifest.NewKind[badInline]("Item", v1))},
			err: "kind Item: badInline.Extra: named field has an inline option, " +
				"which the decode reads inline and gen nests",
		},
		"embedded struct with an inline option": {
			opts: []manifest.RegistryOption{manifest.WithKinds(manifest.NewKind[goodEmbed]("Item", v1))},
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

func TestMustNewRegistry(t *testing.T) {
	t.Parallel()

	assert.NotNil(t, manifest.MustNewRegistry(cafeOptions()...))
	assert.PanicsWithError(t, "manifest: kind Cafe has no version", func() {
		manifest.MustNewRegistry(manifest.WithKinds(manifest.NewKind[Cafe]("Cafe")))
	})
}

func TestRegistry_Verify(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		schema []byte
		err    string
	}{
		"schema compiles": {
			schema: []byte(itemSchema),
		},
		"schema does not compile": {
			schema: []byte(`{"type": 5}`),
			err:    "MenuItem v1: ",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reg, err := manifest.NewRegistry(manifest.WithKinds(manifest.NewKind[MenuItem]("MenuItem",
				manifest.Version("v1", manifest.Schema(schema.Embedded(tc.schema))),
			)))
			require.NoError(t, err)

			err = reg.Verify(t.Context())
			if tc.err == "" {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, schema.ErrCompile)
			assert.Contains(t, err.Error(), tc.err)
		})
	}
}
