package migrate_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/manifest"
	"go.jacobcolvin.com/niceyaml/manifest/migrate"
	"go.jacobcolvin.com/niceyaml/paths"
)

var spec = paths.Doc().Child("spec")

// load loads text, a document at v1 of the kind Cafe, whose v2 runs the
// step fn on the places that p selects. It checks that the value of v2
// holds the type fields of v2, and returns the value without them.
func load(t *testing.T, text string, p paths.Path, fn migrate.Func) (map[string]any, error) {
	t.Helper()

	reg := manifest.MustNewRegistry(
		manifest.WithAppName("cafe"),
		manifest.WithKinds(manifest.NewKind[map[string]any]("Cafe",
			manifest.Singleton(),
			manifest.Version("v1"),
			manifest.Version("v2", manifest.Migrate(p, fn)),
		)),
	)

	src := niceyaml.NewSourceFromString(stringtest.LinesLF("apiVersion: v1", "kind: Cafe")+text,
		niceyaml.WithName("cafe.yaml"))

	set, err := reg.Load(t.Context(), manifest.Sources(src))
	if err != nil {
		return nil, err //nolint:wrapcheck // The test reads the error of the load.
	}

	cafe, ok := set.One[map[string]any]()
	require.True(t, ok)

	assert.Equal(t, "v2", cafe.Value["apiVersion"])
	assert.Equal(t, "Cafe", cafe.Value["kind"])

	delete(cafe.Value, "apiVersion")
	delete(cafe.Value, "kind")

	return cafe.Value, nil
}

// null reports whether n holds a null.
func null(ctx context.Context, n *migrate.Node) bool {
	if n.Kind() != niceyaml.NodeScalar {
		return false
	}

	v, err := n.Decode[any](ctx)

	return err == nil && v == nil
}

// fill returns a step that sets the value at path, read from the value it
// gets, where the document leaves it out.
func fill(path paths.Path, v any) migrate.Func {
	return func(ctx context.Context, n *migrate.Node) error {
		at := n
		for sel := range path.Selectors() {
			if null(ctx, at) {
				err := at.Set(ctx, map[string]any{})
				if err != nil {
					return err
				}
			}

			at = at.At(paths.Current().Child(sel.Name))
		}

		if at.Exists() && !null(ctx, at) {
			return nil
		}

		return at.Set(ctx, v)
	}
}

func TestNode_Read(t *testing.T) {
	t.Parallel()

	type read struct {
		Path    string
		Key     string
		Text    string
		Kind    niceyaml.NodeKind
		Exists  bool
		Missing bool
	}

	var got []read

	_, err := load(t, stringtest.LinesLF(
		"spec:",
		"  seats: 40 # inside",
		"  tags: [a]",
		`  hours: {open: "07:00"}`,
	),
		paths.MustParse("$.spec.*"),
		func(_ context.Context, n *migrate.Node) error {
			missing := n.At(paths.Current().Child("missing"))
			assert.Equal(t, niceyaml.NodeNone, missing.Kind())

			got = append(got, read{
				Path:    n.Path().String(),
				Key:     n.Key(),
				Text:    n.Text(),
				Kind:    n.Kind(),
				Exists:  n.Exists(),
				Missing: missing.Exists(),
			})

			return nil
		})
	require.NoError(t, err)

	assert.Equal(t, []read{
		{Path: "$.spec.seats", Key: "seats", Text: "40", Kind: niceyaml.NodeScalar, Exists: true},
		{Path: "$.spec.tags", Key: "tags", Kind: niceyaml.NodeSequence, Exists: true},
		{Path: "$.spec.hours", Key: "hours", Kind: niceyaml.NodeMapping, Exists: true},
	}, got)
}

func TestNode_Edit(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		want map[string]any
		fn   migrate.Func
		path paths.Path
		text string
		err  string
	}{
		"decode and set": {
			text: stringtest.LinesLF("spec:", "  position: 3"),
			path: spec.Child("position"),
			fn: func(ctx context.Context, n *migrate.Node) error {
				position, err := n.Decode[int](ctx)
				if err != nil {
					return err
				}

				err = n.SetKey("index")
				if err != nil {
					return err
				}

				return n.Set(ctx, position-1)
			},
			want: map[string]any{"spec": map[string]any{"index": uint64(2)}},
		},
		"move": {
			text: stringtest.LinesLF("name: yamull", "spec: {}"),
			path: paths.Doc().Child("name"),
			fn: func(_ context.Context, n *migrate.Node) error {
				return n.Move(paths.Doc().Child("metadata", "name"))
			},
			want: map[string]any{"metadata": map[string]any{"name": "yamull"}, "spec": map[string]any{}},
		},
		"remove": {
			text: stringtest.LinesLF(
				"spec:",
				"  color: red",
				"  seats: 4",
			),
			path: spec.Child("color"),
			fn: func(_ context.Context, n *migrate.Node) error {
				return n.Remove("menu colors moved to the theme")
			},
			want: map[string]any{"spec": map[string]any{"seats": uint64(4)}},
		},
		"fill where the file leaves the value out": {
			text: stringtest.LinesLF("metadata:", "  name: yamull"),
			path: paths.Doc(),
			fn:   fill(spec.Child("currency"), "SEK"),
			want: map[string]any{
				"metadata": map[string]any{"name": "yamull"},
				"spec":     map[string]any{"currency": "SEK"},
			},
		},
		"fill under a null": {
			text: "spec: ~\n",
			path: paths.Doc(),
			fn:   fill(spec.Child("currency"), "SEK"),
			want: map[string]any{"spec": map[string]any{"currency": "SEK"}},
		},
		"fill where the file writes the value": {
			text: stringtest.LinesLF("spec:", "  currency: EUR"),
			path: paths.Doc(),
			fn:   fill(spec.Child("currency"), "SEK"),
			want: map[string]any{"spec": map[string]any{"currency": "EUR"}},
		},
		"error of a method that the step ignores": {
			text: stringtest.LinesLF("spec:", "  - a"),
			path: spec.Index(0),
			fn: func(_ context.Context, n *migrate.Node) error {
				_ = n.SetKey("b") //nolint:errcheck // The document fails with the error anyway.

				return nil
			},
			err: "cafe.yaml:4:5: $.spec[0]: v2 renames this item, but an item of a sequence has no key",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := load(t, tc.text, tc.path, tc.fn)
			if tc.err != "" {
				require.Error(t, err)
				assert.Equal(t, tc.err, err.Error())

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
