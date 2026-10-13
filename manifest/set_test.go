package manifest_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/manifest"
)

// loadExample loads the example directory with reg.
func loadExample(t *testing.T, reg *manifest.Registry) *manifest.Set {
	t.Helper()

	set, err := reg.Load(t.Context(), manifest.FS(mapFS(map[string]string{
		"cafe.yaml":  cafeDoc,
		"hours.yaml": hoursDoc,
		"menu.yaml":  menuDoc,
	}), "config"))
	require.NoError(t, err)

	return set
}

func TestSet_One(t *testing.T) {
	t.Parallel()

	set := loadExample(t, newCafeRegistry(t))

	cafe, ok := set.One[Cafe]()
	require.True(t, ok)
	assert.Equal(t, "yamull", cafe.Name)
	assert.Equal(t, 40, cafe.Value.Spec.Seats)
	assert.Equal(t, "Cafe", cafe.Kind)
	assert.Equal(t, 0, cafe.Node.DocumentIndex())

	assert.PanicsWithValue(t, "manifest: One needs a kind with manifest.Singleton, and Hours has none", func() {
		set.One[Hours]()
	})

	assert.PanicsWithValue(t, "manifest: manifest_test.CafeSpec is not the Go type of a kind", func() {
		set.One[CafeSpec]()
	})

	empty, err := newCafeRegistry(t).Load(t.Context(), manifest.Sources(), manifest.WithSetChecks(false))
	require.NoError(t, err)

	_, ok = empty.One[Cafe]()
	assert.False(t, ok)
}

func TestSet_All(t *testing.T) {
	t.Parallel()

	set := loadExample(t, newCafeRegistry(t))

	var names []string

	for item := range set.All[MenuItem]() {
		names = append(names, item.Name+" "+item.Version)

		if item.Name == "kanelbulle" {
			break
		}
	}

	assert.Equal(t, []string{"kanelbulle cafe.jacobcolvin.com/v1beta1"}, names)

	assert.Panics(t, func() { set.All[Metadata]() })
}

func TestSet_Get(t *testing.T) {
	t.Parallel()

	set := loadExample(t, newCafeRegistry(t))

	hours, ok := set.Get[Hours]("weekday")
	require.True(t, ok)
	assert.Equal(t, "07:00", hours.Value.Spec.Open)
	assert.False(t, hours.UpToDate)

	_, ok = set.Get[Hours]("weekend")
	assert.False(t, ok)

	reg, err := manifest.NewRegistry(manifest.WithKinds(manifest.NewKind[Cafe]("Cafe",
		manifest.Version("cafe.jacobcolvin.com/v1"))))
	require.NoError(t, err)

	unnamed, err := reg.Load(t.Context(), manifest.Sources())
	require.NoError(t, err)

	assert.PanicsWithValue(t, "manifest: Get needs a registry with manifest.WithNameField", func() {
		unnamed.Get[Cafe]("yamull")
	})
}

func TestSet_Objects(t *testing.T) {
	t.Parallel()

	set := loadExample(t, newCafeRegistry(t))

	var kinds []string

	for o := range set.Objects() {
		kinds = append(kinds, o.Kind)

		if o.Kind == "Hours" {
			_, ok := o.Value.(Hours)
			assert.True(t, ok)

			break
		}
	}

	assert.Equal(t, []string{"Cafe", "Hours"}, kinds)
	assert.Len(t, set.Sources(), 3)
	assert.Error(t, set.Warnings())
}

func TestObject_Bind(t *testing.T) {
	t.Parallel()

	errBad := errors.New("bad")

	assert.Equal(t, errBad, manifest.Object[Cafe]{}.Bind(errBad))

	doc := yamltest.FirstDocument(t, "a: 1\n", niceyaml.WithName("a.yaml"))
	built := manifest.Object[Cafe]{Node: doc}
	assert.Equal(t, "a.yaml: bad", built.Bind(errBad).Error())

	set := loadExample(t, newCafeRegistry(t))

	item, ok := set.Get[MenuItem]("kanelbulle")
	require.True(t, ok)

	err := item.Bind(niceyaml.NewError(`unknown tag "vegan"`, niceyaml.AtPath(spec.Child("tags").Index(0))))
	assert.Equal(t, ""+
		"config/menu.yaml:8:9: $.spec.tags: unknown tag \"vegan\"\n"+
		"`-- cafe.jacobcolvin.com/v1 sets $.spec.tags[0] from this value\n"+
		"\n"+
		"   6 | spec:\n"+
		"   7 |   cost: 45 # kr\n"+
		"   8 |   tags: vegan\n"+
		"     |         ^^^^^\n"+
		"   9 | ---\n"+
		"  10 | apiVersion: cafe.jacobcolvin.com/v1", niceyaml.FormatError(err))

	err = item.Bind(niceyaml.NewError("too cheap", niceyaml.AtPath(spec.Child("price"))))
	assert.Equal(t, "config/menu.yaml:7:9: $.spec.cost: too cheap", err.Error())

	left, err := newCafeRegistry(t).Load(t.Context(), manifest.FS(mapFS(map[string]string{
		"cafe.yaml":  "apiVersion: cafe.jacobcolvin.com/v1\nkind: Cafe\nmetadata:\n  name: yamull\nspec:\n  currency: SEK\n",
		"hours.yaml": "apiVersion: cafe.jacobcolvin.com/v1alpha1\nkind: Hours\nmetadata:\n  name: weekday\nspec:\n  opens: \"18:00\"\n",
	}), "config"))
	require.NoError(t, err)

	cafe, ok := left.One[Cafe]()
	require.True(t, ok)

	err = cafe.Bind(niceyaml.NewError("20 seats exceed capacity 10", niceyaml.AtPath(spec.Child("seats"))))
	assert.Equal(t, ""+
		"config/cafe.yaml:5:1: $.spec.seats: 20 seats exceed capacity 10\n"+
		"`-- the file leaves out seats, so it holds its default", firstTwoLines(niceyaml.FormatError(err)))

	hours, ok := left.Get[Hours]("weekday")
	require.True(t, ok)

	err = hours.Bind(niceyaml.NewError("closes at 17:00, before it opens at 18:00",
		niceyaml.AtPath(spec.Child("close"))))
	assert.Equal(t, ""+
		"config/hours.yaml:5:1: $.spec.close: closes at 17:00, before it opens at 18:00\n"+
		"`-- the file leaves out the field that cafe.jacobcolvin.com/v1 names close, so it holds its default",
		firstTwoLines(niceyaml.FormatError(err)))

	err = hours.Bind(niceyaml.NewError("too early", niceyaml.AtPath(spec.Child("open"))))
	assert.Equal(t, "config/hours.yaml:6:10: $.spec.opens: too early", firstLine(niceyaml.FormatError(err)))
}
