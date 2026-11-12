package manifest_test

import (
	"context"
	"errors"
	"runtime"
	"slices"
	"testing"
	"time"
	"weak"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/input"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/manifest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// loadExample loads the example directory with the registry of
// [cafeOptions].
func loadExample(t *testing.T) *manifest.Set {
	t.Helper()

	set, err := newCafeRegistry(t).Load(t.Context(), configFS(exampleFiles))
	require.NoError(t, err)

	return set
}

// loadShared loads a Deployment and a Service, two kinds that share the Go
// type map[string]any.
func loadShared(t *testing.T) *manifest.Set {
	t.Helper()

	name := manifest.NameField(paths.Doc().Child("metadata", "name"))
	reg, err := manifest.NewRegistry(manifest.WithKinds(
		manifest.NewKind[map[string]any]("Deployment", name, manifest.Singleton(), manifest.Version("apps/v1")),
		manifest.NewKind[map[string]any]("Service", name, manifest.Version("v1")),
	))
	require.NoError(t, err)

	set, err := reg.Load(t.Context(), configFS(map[string]string{
		"web.yaml": stringtest.LinesLF(
			"apiVersion: v1",
			"kind: Service",
			"metadata:",
			"  name: web",
			"---",
			"apiVersion: apps/v1",
			"kind: Deployment",
			"metadata:",
			"  name: web",
			"spec:",
			"  replicas: 2",
		),
	}))
	require.NoError(t, err)

	return set
}

func TestSet_One(t *testing.T) {
	t.Parallel()

	set := loadExample(t)

	cafe, ok := set.One[Cafe]()
	require.True(t, ok)
	assert.Equal(t, "yamull", cafe.Name)
	assert.Equal(t, 40, cafe.Value.Spec.Seats)
	assert.Equal(t, "Cafe", cafe.Kind)
	assert.Equal(t, 0, cafe.Original.DocumentIndex())

	assert.PanicsWithValue(t, "manifest: One needs a kind with manifest.Singleton, and Hours has none", func() {
		set.One[Hours]()
	})

	assert.PanicsWithValue(t, "manifest: manifest_test.CafeSpec is not the Go type of a kind", func() {
		set.One[CafeSpec]()
	})

	empty, err := newCafeRegistry(t).Load(t.Context(), input.Sources(), manifest.WithSetChecks(false))
	require.NoError(t, err)

	_, ok = empty.One[Cafe]()
	assert.False(t, ok)

	shared := loadShared(t)

	assert.PanicsWithValue(
		t,
		"manifest: One needs the Go type of one kind, and map[string]interface {} is the type of Deployment and Service",
		func() { shared.One[map[string]any]() },
	)
}

func TestSet_All(t *testing.T) {
	t.Parallel()

	set := loadExample(t)

	var names []string

	for item := range set.All[MenuItem]() {
		names = append(names, item.Name+" "+item.Version)

		if item.Name == "kanelbulle" {
			break
		}
	}

	assert.Equal(t, []string{"kanelbulle cafe.jacobcolvin.com/v1beta1"}, names)

	assert.Panics(t, func() { set.All[Metadata]() })

	var kinds []string

	for o := range loadShared(t).All[map[string]any]() {
		kinds = append(kinds, o.Kind+" "+o.Name)

		if o.Kind == "Deployment" {
			assert.Equal(t, map[string]any{"replicas": uint64(2)}, o.Value["spec"])
		}
	}

	assert.Equal(t, []string{"Service web", "Deployment web"}, kinds)
}

func TestSet_Get(t *testing.T) {
	t.Parallel()

	set := loadExample(t)

	hours, ok := set.Get[Hours]("weekday")
	require.True(t, ok)
	assert.Equal(t, "07:00", hours.Value.Spec.Open)
	assert.False(t, hours.UpToDate)

	_, ok = set.Get[Hours]("weekend")
	assert.False(t, ok)

	reg := manifest.MustNewRegistry(manifest.WithKinds(manifest.NewKind[Cafe]("Cafe",
		manifest.Version("cafe.jacobcolvin.com/v1"))))

	unnamed, err := reg.Load(t.Context(), input.Sources())
	require.NoError(t, err)

	assert.PanicsWithValue(t, "manifest: Get needs a kind with manifest.NameField, and Cafe has none", func() {
		unnamed.Get[Cafe]("yamull")
	})

	shared := loadShared(t)

	assert.PanicsWithValue(
		t,
		"manifest: Get needs the Go type of one kind, and map[string]interface {} is the type of Deployment and Service",
		func() { shared.Get[map[string]any]("web") },
	)
}

func TestSet_Objects(t *testing.T) {
	t.Parallel()

	set := loadExample(t)

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

func TestSet_Sources(t *testing.T) {
	t.Parallel()

	set := loadExample(t)

	var names []string

	for _, src := range set.Sources() {
		names = append(names, src.Name())
	}

	assert.Equal(t, []string{"config/cafe.yaml", "config/hours.yaml", "config/menu.yaml"}, names)

	// The set keeps its order when a caller reorders the slice.
	slices.Reverse(set.Sources())
	assert.Equal(t, "config/cafe.yaml", set.Sources()[0].Name())
}

func TestObject_Bind(t *testing.T) {
	t.Parallel()

	errBad := errors.New("bad")

	assert.Equal(t, errBad, manifest.Object[Cafe]{}.Bind(errBad))

	doc := yamltest.FirstDocument(t, "a: 1\n", niceyaml.WithName("a.yaml"))
	built := manifest.Object[Cafe]{Current: doc}
	assert.Equal(t, "a.yaml: bad", built.Bind(errBad).Error())

	set := loadExample(t)

	item, ok := set.Get[MenuItem]("kanelbulle")
	require.True(t, ok)

	// The migration writes a sequence over the scalar, so the item that it
	// creates binds at the key of the sequence and keeps its path (fix20
	// B1). The detail names the call that wrote the sequence by its path
	// (fix21 P65).
	err := item.Bind(niceyaml.NewError(`unknown tag "vegan"`, niceyaml.AtPath(spec.Child("tags").Index(0))))
	assert.Equal(t, stringtest.JoinLF(
		`config/menu.yaml:8:3: $.spec.tags[0]: unknown tag "vegan"`,
		"`-- cafe.jacobcolvin.com/v1 sets $.spec.tags from this value",
		"",
		"   6 | spec:",
		"   7 |   cost: 45 # kr",
		"   8 |   tags: vegan",
		"     |   ^^^^",
		"   9 | ---",
		"  10 | apiVersion: cafe.jacobcolvin.com/v1",
	), niceyaml.FormatError(err))

	// The migration renames cost to price. The error binds where the file
	// writes cost and keeps the path that the caller gave (fix21 P65).
	err = item.Bind(niceyaml.NewError("too cheap", niceyaml.AtPath(spec.Child("price"))))
	assert.Equal(t, "config/menu.yaml:7:9: $.spec.price: too cheap", err.Error())

	left, err := newCafeRegistry(t).Load(t.Context(), configFS(map[string]string{
		"cafe.yaml": stringtest.LinesLF(
			"apiVersion: cafe.jacobcolvin.com/v1",
			"kind: Cafe",
			"metadata:",
			"  name: yamull",
			"spec:",
			"  currency: SEK",
		),
		"hours.yaml": stringtest.LinesLF(
			"apiVersion: cafe.jacobcolvin.com/v1alpha1",
			"kind: Hours",
			"metadata:",
			"  name: weekday",
			"spec:",
			`  opens: "18:00"`,
		),
	}))
	require.NoError(t, err)

	cafe, ok := left.One[Cafe]()
	require.True(t, ok)

	err = cafe.Bind(niceyaml.NewError("20 seats exceed capacity 10", niceyaml.AtPath(spec.Child("seats"))))
	assert.Equal(t, stringtest.JoinLF(
		"config/cafe.yaml:5:1: $.spec.seats: 20 seats exceed capacity 10",
		"`-- the file leaves out seats, so it holds its default",
	), firstTwoLines(niceyaml.FormatError(err)))

	hours, ok := left.Get[Hours]("weekday")
	require.True(t, ok)

	err = hours.Bind(niceyaml.NewError("closes at 17:00, before it opens at 18:00",
		niceyaml.AtPath(spec.Child("close"))))
	assert.Equal(t, stringtest.JoinLF(
		"config/hours.yaml:5:1: $.spec.close: closes at 17:00, before it opens at 18:00",
		"`-- the file leaves out the field that cafe.jacobcolvin.com/v1 names close, so it holds its default",
	),
		firstTwoLines(niceyaml.FormatError(err)))

	err = hours.Bind(niceyaml.NewError("too early", niceyaml.AtPath(spec.Child("open"))))
	assert.Equal(t, "config/hours.yaml:6:10: $.spec.open: too early", firstLine(niceyaml.FormatError(err)))

	t.Run("a value behind an alias to a reference document", func(t *testing.T) {
		t.Parallel()

		ref := niceyaml.NewSourceFromString("base: &base {currency: SEK}\n")

		set, err := newCafeRegistry(t).Load(t.Context(), input.Sources(niceyaml.NewSourceFromString(
			stringtest.LinesLF(
				"apiVersion: cafe.jacobcolvin.com/v1",
				"kind: Cafe",
				"metadata:",
				"  name: yamull",
				"spec: *base",
			),
			niceyaml.WithName("cafe.yaml"),
			niceyaml.WithReferences(ref),
		)))
		require.NoError(t, err)

		cafe, ok := set.One[Cafe]()
		require.True(t, ok)
		assert.Equal(t, CafeSpec{Currency: "SEK"}, cafe.Value.Spec)

		// The file writes the spec as the alias, so a problem there or
		// below it binds at the alias, and no detail says that the file
		// leaves the spec out.
		tcs := map[string]struct {
			problem error
			want    string
		}{
			"the alias": {
				problem: niceyaml.NewError("closed", niceyaml.AtPath(spec)),
				want:    "cafe.yaml:5:7: $.spec: closed",
			},
			"a field below the alias": {
				problem: niceyaml.NewError("too few seats", niceyaml.AtPath(spec.Child("seats"))),
				want:    "cafe.yaml:5:7: $.spec.seats: too few seats",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				got := niceyaml.FormatError(cafe.Bind(tc.problem))

				// The blank line above the excerpt follows the message.
				assert.Equal(t, stringtest.JoinLF(tc.want, ""), firstTwoLines(got))
			})
		}
	})

	t.Run("a field that a merge key of a reference document brings in", func(t *testing.T) {
		t.Parallel()

		ref := niceyaml.NewSourceFromString("base: &base {seats: 4}\n")

		set, err := newCafeRegistry(t).Load(t.Context(), input.Sources(niceyaml.NewSourceFromString(
			stringtest.LinesLF(
				"apiVersion: cafe.jacobcolvin.com/v1",
				"kind: Cafe",
				"metadata:",
				"  name: yamull",
				"spec:",
				"  <<: *base",
				"  currency: SEK",
			),
			niceyaml.WithName("cafe.yaml"),
			niceyaml.WithReferences(ref),
		)))
		require.NoError(t, err)

		cafe, ok := set.One[Cafe]()
		require.True(t, ok)
		assert.Equal(t, CafeSpec{Seats: 4, Currency: "SEK"}, cafe.Value.Spec)

		// The merge key writes the seats, so no detail says that the file
		// leaves them out. The problem binds at the alias of the merge key,
		// because the reference document holds the line of the seats.
		got := niceyaml.FormatError(cafe.Bind(niceyaml.NewError("too few seats", niceyaml.AtPath(spec.Child("seats")))))
		assert.Equal(t, stringtest.JoinLF(
			"cafe.yaml:6:7: $.spec.seats: too few seats",
			"",
		), firstTwoLines(got))
	})
}

func TestObject_Layouts(t *testing.T) {
	t.Parallel()

	open := spec.Child("open")

	// The validator reads the open field of the current version and
	// rejects it.
	tooEarly := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
		_, err := n.DecodeAt[string](ctx, open)
		if err != nil {
			return err
		}

		return niceyaml.NewError("too early", niceyaml.AtPath(open))
	})

	t.Run("a document of an older version", func(t *testing.T) {
		t.Parallel()

		// The file of the hours declares v1alpha1 and writes opens, which
		// v1 names open.
		hours, ok := loadExample(t).Get[Hours]("weekday")
		require.True(t, ok)

		problem := niceyaml.NewError("too early", niceyaml.AtPath(open))

		// Current reads the layout that Value decoded from, and each of
		// its errors binds where the file writes opens.
		got, err := hours.Current.DecodeAt[string](t.Context(), open)
		require.NoError(t, err)
		assert.Equal(t, hours.Value.Spec.Open, got)

		at, err := hours.Current.At(open)
		require.NoError(t, err)
		require.EqualError(t, at.Bind(errors.New("at the node")), "config/hours.yaml:6:10: $.spec.open: at the node")

		require.EqualError(t, hours.Current.Validate(t.Context(), tooEarly),
			"config/hours.yaml:6:10: $.spec.open: too early")
		require.EqualError(t, hours.Current.Bind(problem), "config/hours.yaml:6:10: $.spec.open: too early")
		require.EqualError(t, hours.Bind(problem), "config/hours.yaml:6:10: $.spec.open: too early")

		// Original reads the layout of the version that the file
		// declares, so it does not hold the path of the current version.
		got, err = hours.Original.DecodeAt[string](t.Context(), spec.Child("opens"))
		require.NoError(t, err)
		assert.Equal(t, "07:00", got)

		_, err = hours.Original.DecodeAt[string](t.Context(), open)
		require.ErrorIs(t, err, paths.ErrNotFound)
		require.EqualError(t, hours.Original.Validate(t.Context(), tooEarly),
			"config/hours.yaml:5:1: $.spec.open: not found")
		require.EqualError(t, hours.Original.Bind(problem), "config/hours.yaml:5:1: $.spec.open: too early")
	})

	t.Run("a document of the current version", func(t *testing.T) {
		t.Parallel()

		set := loadExample(t)

		semla, ok := set.Get[MenuItem]("semla")
		require.True(t, ok)

		// The document declares the current version, so Current is
		// Original, the document of the file, and gives the place of the
		// document in the file.
		assert.Same(t, semla.Original, semla.Current)
		assert.Contains(t, set.Sources(), semla.Current.Source())
		assert.Equal(t, 1, semla.Current.DocumentIndex())
		assert.Equal(t, position.NewSpan(8, 17), semla.Current.Span())

		price := spec.Child("price")

		value, err := semla.Current.At(price)
		require.NoError(t, err)
		assert.Equal(t, "14:9-14:11", yamltest.RangeOf(t, value).String())

		bound := semla.Current.Bind(niceyaml.NewError("pricey", niceyaml.AtPath(price)))
		require.EqualError(t, bound, "config/menu.yaml:15:10: $.spec.price: pricey")
		require.EqualError(t, semla.Current.Bind(errors.New("pricey")), "config/menu.yaml:9: document 2: pricey")
		require.EqualError(t, semla.Bind(niceyaml.NewError("pricey", niceyaml.AtPath(price))),
			"config/menu.yaml:15:10: $.spec.price: pricey")

		// An Object of Objects holds the documents of its typed Object.
		for o := range set.Objects() {
			if o.Name == semla.Name {
				assert.Same(t, semla.Current, o.Current)
				assert.Same(t, semla.Original, o.Original)
			}
		}
	})

	t.Run("a Source that the program built", func(t *testing.T) {
		t.Parallel()

		set, err := newCafeRegistry(t).Load(t.Context(), input.Sources(niceyaml.NewSourceFromString(
			cafeDoc+"---\n"+hoursDoc,
			niceyaml.WithName("cafe.yaml"),
		)))
		require.NoError(t, err)

		// The registry has no unmarshalers, so the load decodes the Node
		// of the caller for a document of the current version.
		cafe, ok := set.Get[Cafe]("yamull")
		require.True(t, ok)
		assert.Same(t, cafe.Original, cafe.Current)
		assert.Contains(t, set.Sources(), cafe.Current.Source())
		assert.Equal(t, 0, cafe.Current.DocumentIndex())

		hours, ok := set.Get[Hours]("weekday")
		require.True(t, ok)

		// Original is the document of the file, so it gives the place of
		// the document in the file.
		assert.Contains(t, set.Sources(), hours.Original.Source())
		assert.Equal(t, 1, hours.Original.DocumentIndex())
		assert.Equal(t, position.NewSpan(6, 14), hours.Original.Span())

		// Current is a migrated copy that holds the lines of the document
		// alone, so its positions describe the copy.
		assert.NotContains(t, set.Sources(), hours.Current.Source())
		assert.Equal(t, "cafe.yaml", hours.Current.Source().Name())
		assert.Equal(t, 0, hours.Current.DocumentIndex())
		assert.Equal(t, position.NewSpan(0, 8), hours.Current.Span())

		value, err := hours.Current.At(open)
		require.NoError(t, err)
		assert.Equal(t, "6:8-6:15", yamltest.RangeOf(t, value).String())

		// An error that binds through Current still lands in the file,
		// and its binding holds the position there.
		bound := hours.Current.Bind(niceyaml.NewError("too early", niceyaml.AtPath(open)))
		require.EqualError(t, bound, "cafe.yaml:13:10: $.spec.open: too early")
		require.EqualError(t, hours.Current.Bind(errors.New("too early")), "cafe.yaml:7: document 2: too early")

		for problem := range niceyaml.Problems(bound) {
			assert.Equal(t, "12:9-12:16", yamltest.RangeOf(t, problem).String())
			assert.Same(t, hours.Original.Source(), problem.Source())
		}
	})

	t.Run("the unmarshalers of the registry", func(t *testing.T) {
		t.Parallel()

		reg := manifest.MustNewRegistry(
			manifest.WithTypeRules(niceyaml.CustomUnmarshaler(decodeAlarmLevel)),
			manifest.WithKinds(manifest.NewKind[Alarm]("Alarm",
				manifest.NameField(meta.Child("name")), manifest.Version("alarm.jacobcolvin.com/v1"))),
		)

		// The program built the Source without the unmarshalers of the
		// registry.
		set, err := reg.Load(t.Context(), input.Sources(niceyaml.NewSourceFromString(
			stringtest.LinesLF(
				"apiVersion: alarm.jacobcolvin.com/v1",
				"kind: Alarm",
				"metadata:",
				"  name: smoke",
				"spec:",
				"  level: high",
			),
			niceyaml.WithName("alarm.yaml"),
		)))
		require.NoError(t, err)

		alarm, ok := set.Get[Alarm]("smoke")
		require.True(t, ok)

		level := spec.Child("level")

		got, err := alarm.Current.DecodeAt[AlarmLevel](t.Context(), level)
		require.NoError(t, err)
		assert.Equal(t, alarm.Value.Spec.Level, got)

		_, err = alarm.Original.DecodeAt[AlarmLevel](t.Context(), level)
		require.EqualError(t, err, "alarm.yaml:6:10: $.spec.level: expected integer, got string")

		// Current is a copy that holds the unmarshalers of the registry.
		assert.NotSame(t, alarm.Original, alarm.Current)
		assert.NotSame(t, alarm.Original.Source(), alarm.Current.Source())
	})

	t.Run("the unmarshalers of the registry on a file that the load reads", func(t *testing.T) {
		t.Parallel()

		reg := manifest.MustNewRegistry(
			manifest.WithTypeRules(niceyaml.CustomUnmarshaler(decodeAlarmLevel)),
			manifest.WithKinds(manifest.NewKind[Alarm]("Alarm",
				manifest.NameField(meta.Child("name")), manifest.Version("alarm.jacobcolvin.com/v1"))),
		)

		set, err := reg.Load(t.Context(), configFS(map[string]string{
			"alarm.yaml": stringtest.LinesLF(
				"apiVersion: alarm.jacobcolvin.com/v1",
				"kind: Alarm",
				"metadata:",
				"  name: smoke",
				"spec:",
				"  level: high",
			),
		}))
		require.NoError(t, err)

		alarm, ok := set.Get[Alarm]("smoke")
		require.True(t, ok)

		// The load built the Source with the unmarshalers of the
		// registry, so Current is Original.
		assert.Same(t, alarm.Original, alarm.Current)

		got, err := alarm.Original.DecodeAt[AlarmLevel](t.Context(), spec.Child("level"))
		require.NoError(t, err)
		assert.Equal(t, alarm.Value.Spec.Level, got)
	})
}

// keepOne loads cafe.yaml and hours.yaml, and returns the manifest whose
// kind is kind. It also returns weak pointers to the Sources of the files
// that do not hold that manifest.
func keepOne(t *testing.T, kind string) (manifest.Object[any], []weak.Pointer[niceyaml.Source]) {
	t.Helper()

	set, err := newCafeRegistry(t).Load(t.Context(), input.Sources(
		niceyaml.NewSourceFromString(cafeDoc, niceyaml.WithName("cafe.yaml")),
		niceyaml.NewSourceFromString(hoursDoc, niceyaml.WithName("hours.yaml")),
	))
	require.NoError(t, err)

	var (
		kept  manifest.Object[any]
		found bool
	)

	for o := range set.Objects() {
		if o.Kind == kind {
			kept, found = o, true
		}
	}

	require.True(t, found)

	var others []weak.Pointer[niceyaml.Source]

	for _, src := range set.Sources() {
		if src != kept.Original.Source() {
			others = append(others, weak.Make(src))
		}
	}

	require.NotEmpty(t, others)

	return kept, others
}

func TestObject_Retention(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		problem error
		kind    string
		want    string
	}{
		"current version": {
			kind:    "Cafe",
			problem: niceyaml.NewError("no currency", niceyaml.AtPath(spec.Child("currency"))),
			want: stringtest.JoinLF(
				"cafe.yaml:5:1: $.spec.currency: no currency",
				"`-- the file leaves out currency, so it holds its default",
			),
		},
		"migrated": {
			kind:    "Hours",
			problem: niceyaml.NewError("no break", niceyaml.AtPath(spec.Child("break"))),
			want: stringtest.JoinLF(
				"hours.yaml:5:1: $.spec.break: no break",
				"`-- the file leaves out the field that cafe.jacobcolvin.com/v1 names break, so it holds its default",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			kept, others := keepOne(t, tc.kind)

			// The kept Object does not keep the other files of its load in
			// memory.
			for _, other := range others {
				assert.Eventually(t, func() bool {
					runtime.GC()

					return other.Value() == nil
				}, 5*time.Second, 10*time.Millisecond)
			}

			assert.Equal(t, tc.want, firstTwoLines(niceyaml.FormatError(kept.Bind(tc.problem))))

			// The kept Object must outlive the check, or the collector
			// frees the whole load with it.
			runtime.KeepAlive(kept)
		})
	}
}
