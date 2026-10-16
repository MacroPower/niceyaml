package edit_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/edit"
	"go.jacobcolvin.com/niceyaml/paths"
)

// null reports whether n holds a null.
func null(ctx context.Context, n *edit.Node) bool {
	if n.Kind() != niceyaml.NodeScalar {
		return false
	}

	v, err := n.Decode[any](ctx)

	return err == nil && v == nil
}

// fill returns a Func that sets the value at path, read from the node it
// gets, where the document leaves it out.
func fill(path paths.Path, v any) edit.Func {
	return func(ctx context.Context, n *edit.Node) error {
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

	d := newDoc(t, stringtest.LinesLF(
		"spec:",
		"  seats: 40 # inside",
		"  tags: [a]",
		`  hours: {open: "07:00"}`,
	))

	var got []read

	err := d.Apply(t.Context(), paths.MustParse("$.spec.*"), func(_ context.Context, n *edit.Node) error {
		missing := n.At(paths.Current().Child("missing"))
		assert.Equal(t, niceyaml.NodeNone, missing.Kind())
		assert.Empty(t, missing.Text())

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

func TestNode_Decode(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		path   paths.Path
		before string
		want   string
		err    string
	}{
		"decode and set": {
			before: stringtest.LinesLF("spec:", "  position: 3 # from 1"),
			path:   spec.Child("position"),
			want:   stringtest.LinesLF("spec:", "  index: 2 # from 1"),
		},
		"place that holds no value": {
			before: stringtest.LinesLF("spec:", "  index: 2"),
			path:   spec.Child("position"),
			err:    "cafe.jacobcolvin.com/v1 cannot decode $.spec.position, which holds no value",
		},
		"value that does not decode": {
			before: stringtest.LinesLF("spec:", "  position: third"),
			path:   spec.Child("position"),
			err:    "test.yaml:2:13: $.spec.position:",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before)

			err := applyAt(t, d, tc.path, func(ctx context.Context, n *edit.Node) error {
				position, err := n.Decode[int](ctx)
				if err != nil {
					return err
				}

				err = n.SetKey("index")
				if err != nil {
					return err
				}

				return n.Set(ctx, position-1)
			})

			requireEdit(t, d, err, tc.err, tc.want)
		})
	}
}

func TestNode_At(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		fn     edit.Func
		before string
		want   string
		err    string
	}{
		"fill where the file leaves the value out": {
			before: stringtest.LinesLF("metadata:", "  name: yamull"),
			fn:     fill(spec.Child("currency"), "SEK"),
			want: stringtest.LinesLF(
				"metadata:",
				"  name: yamull",
				"spec:",
				"  currency: SEK",
			),
		},
		"fill under a null": {
			before: "spec: ~\n",
			fn:     fill(spec.Child("currency"), "SEK"),
			want:   stringtest.LinesLF("spec:", "  currency: SEK"),
		},
		"fill where the file writes the value": {
			before: stringtest.LinesLF("spec:", "  currency: EUR"),
			fn:     fill(spec.Child("currency"), "SEK"),
			want:   stringtest.LinesLF("spec:", "  currency: EUR"),
		},
		"path that selects more than one value": {
			before: stringtest.LinesLF("spec:", "  tags: [a, b]"),
			fn: func(_ context.Context, n *edit.Node) error {
				n.At(paths.MustParse("$.spec.tags[*]"))

				return nil
			},
			err: "cafe.jacobcolvin.com/v1 reads $.spec.tags[*], which selects more than one value",
		},
		"path that selects a key": {
			before: stringtest.LinesLF("spec:", "  tags: [a, b]"),
			fn: func(_ context.Context, n *edit.Node) error {
				return n.At(spec.Child("tags").Key()).Remove()
			},
			err: "cafe.jacobcolvin.com/v1 reads $.spec.tags~, which selects a key",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before)

			requireEdit(t, d, d.Apply(t.Context(), paths.Doc(), tc.fn, label), tc.err, tc.want)
		})
	}
}
