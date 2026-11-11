package manifest_test

import (
	"reflect"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/manifest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/schema"
)

func TestRegistry_Kinds(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{"item.json": {Data: []byte(itemSchema)}}
	itemRef := schema.FileFS(fsys, "item.json")

	reg := manifest.MustNewRegistry(
		manifest.WithKinds(
			manifest.NewKind[Cafe]("Cafe",
				manifest.NameField(meta.Child("name")),
				manifest.Required(),
				manifest.Singleton(),
				manifest.Unversioned(),
				manifest.Version("v1"),
			),
			manifest.NewKind[MenuItem]("MenuItem",
				manifest.Version("v1", manifest.KindName("Dish"), manifest.Deprecated("use v2")),
				manifest.Version("v2",
					manifest.Written(),
					manifest.Migrate(spec.Child("cost"), rename("price")),
					manifest.Migrate(spec.Child("labels"), rename("tags")),
				),
				manifest.Version("v3", manifest.Schema(itemRef)),
			),
			manifest.NewKind[Hours]("Hours",
				manifest.Version("v1"),
				manifest.Version("v2", manifest.Migrate(spec.Child("opens"), rename("open"))),
			),
			manifest.NewKind[Hours]("Closures", manifest.DeprecatedKind("use Hours"), manifest.Version("v1")),
		),
		manifest.WithRemoved("", "Cafe"),
		manifest.WithRemoved("v1", "Hours", "v1 was removed in 2.0"),
		manifest.WithRemoved("v0", "Hours"),
		manifest.WithRemoved("v1", "Legacy"),
	)

	got := reg.Kinds()
	require.Len(t, got, 4)

	// A schema.Ref holds functions, which never compare equal, so the
	// test compares the key of each ref and then clears it.
	assert.Equal(t, itemRef.Key(), got[1].Versions[2].Schema.Key())

	got[1].Versions[2].Schema = schema.Ref{}

	hours := reflect.TypeFor[Hours]()
	want := []manifest.KindInfo{
		{
			Type:         reflect.TypeFor[Cafe](),
			Name:         "Cafe",
			NameField:    meta.Child("name"),
			HasNameField: true,
			Required:     true,
			Singleton:    true,
			Versions: []manifest.VersionInfo{
				{Name: "", KindName: "Cafe", Removed: true},
				{Name: "v1", KindName: "Cafe", Written: true},
			},
		},
		{
			Type: reflect.TypeFor[MenuItem](),
			Name: "MenuItem",
			Versions: []manifest.VersionInfo{
				{Name: "v1", KindName: "Dish", Deprecated: true},
				{Name: "v2", KindName: "MenuItem", Steps: 2, Written: true},
				{Name: "v3", KindName: "MenuItem", HasSchema: true},
			},
		},
		{
			Type: hours,
			Name: "Hours",
			Versions: []manifest.VersionInfo{
				{Name: "v1", KindName: "Hours", Removed: true},
				{Name: "v2", KindName: "Hours", Steps: 1, Written: true},
			},
		},
		{
			Type:       hours,
			Name:       "Closures",
			Deprecated: true,
			Versions: []manifest.VersionInfo{
				{Name: "v1", KindName: "Closures", Written: true},
			},
		},
	}
	assert.Equal(t, want, got)

	// Each call returns a new list, so a change to one list leaves the
	// registry and the next list alone.
	got[0].Name = "Changed"
	got[0].Versions[0].Name = "changed"

	again := reg.Kinds()
	assert.Equal(t, "Cafe", again[0].Name)
	assert.Empty(t, again[0].Versions[0].Name)
}

func TestRegistry_Kinds_Empty(t *testing.T) {
	t.Parallel()

	reg := manifest.MustNewRegistry()
	assert.Empty(t, reg.Kinds())
}

func TestRegistry_TypeFields(t *testing.T) {
	t.Parallel()

	item := manifest.NewKind[MenuItem]("MenuItem", manifest.Version("v1"))

	tcs := map[string]struct {
		wantVersion string
		wantKind    string
		opts        []manifest.RegistryOption
		hasKind     bool
	}{
		"defaults": {
			opts:        []manifest.RegistryOption{manifest.WithKinds(item)},
			wantVersion: "$.apiVersion",
			wantKind:    "$.kind",
			hasKind:     true,
		},
		"fields of their own": {
			opts: []manifest.RegistryOption{
				manifest.WithKinds(item),
				manifest.WithVersionField(meta.Child("version")),
				manifest.WithKindField(meta.Child("type")),
			},
			wantVersion: "$.metadata.version",
			wantKind:    "$.metadata.type",
			hasKind:     true,
		},
		"version only": {
			opts: []manifest.RegistryOption{
				manifest.WithKinds(item),
				manifest.WithVersionOnly(paths.Doc().Child("version")),
			},
			wantVersion: "$.version",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reg, err := manifest.NewRegistry(tc.opts...)
			require.NoError(t, err)

			assert.Equal(t, tc.wantVersion, reg.VersionField().String())

			kind, ok := reg.KindField()
			assert.Equal(t, tc.hasKind, ok)

			if tc.hasKind {
				assert.Equal(t, tc.wantKind, kind.String())
			}

			// The name of the kind stays in the list whether or not
			// documents write a kind field.
			kinds := reg.Kinds()
			require.Len(t, kinds, 1)
			require.Len(t, kinds[0].Versions, 1)
			assert.Equal(t, "MenuItem", kinds[0].Versions[0].KindName)
		})
	}
}
