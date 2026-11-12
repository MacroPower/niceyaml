package edit_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/edit"
	"go.jacobcolvin.com/niceyaml/encoder"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/schema"
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

		ok, err := at.Exists()
		if err != nil {
			return err
		}

		if ok && !null(ctx, at) {
			return nil
		}

		return at.Set(ctx, v)
	}
}

// scalar returns the text that [edit.Node.Scalar] returns for n, and fails
// the test when n does not hold a scalar with a text.
func scalar(t *testing.T, n *edit.Node) string {
	t.Helper()

	text, ok := n.Scalar()
	require.True(t, ok)

	return text
}

// exists returns what [edit.Node.Exists] reports for n, and fails the test
// when Exists returns an error.
func exists(t *testing.T, n *edit.Node) bool {
	t.Helper()

	ok, err := n.Exists()
	require.NoError(t, err)

	return ok
}

// existsErr returns the error of [edit.Node.Exists] for n.
func existsErr(n *edit.Node) error {
	_, err := n.Exists()

	return err
}

func TestNode_Read(t *testing.T) {
	t.Parallel()

	type read struct {
		Path    string
		Key     string
		Text    string
		Kind    niceyaml.NodeKind
		IsText  bool
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

		missingText, missingIsText := missing.Scalar()
		assert.Empty(t, missingText)
		assert.False(t, missingIsText)

		text, isText := n.Scalar()

		got = append(got, read{
			Path:    n.Path().String(),
			Key:     n.Key(),
			Text:    text,
			Kind:    n.Kind(),
			IsText:  isText,
			Exists:  exists(t, n),
			Missing: exists(t, missing),
		})

		return nil
	})
	require.NoError(t, err)

	assert.Equal(t, []read{
		{Path: "$.spec.seats", Key: "seats", Text: "40", Kind: niceyaml.NodeScalar, IsText: true, Exists: true},
		{Path: "$.spec.tags", Key: "tags", Kind: niceyaml.NodeSequence, Exists: true},
		{Path: "$.spec.hours", Key: "hours", Kind: niceyaml.NodeMapping, Exists: true},
	}, got)
}

func TestNode_Exists(t *testing.T) {
	t.Parallel()

	ref := niceyaml.NewSourceFromString("defaults: &d\n  cost: 5\n", niceyaml.WithName("ref.yaml"))

	tcs := map[string]struct {
		before string
		// The node whose Exists the test reads, from the root.
		node func(ctx context.Context, root *edit.Node) *edit.Node
		// The sentinel that the error of Exists matches, or nil.
		is error
		// The message of the error of Exists, or "" for nil.
		err  string
		ref  bool
		want bool
	}{
		"place that holds a value": {
			before: "a: 1\n",
			node:   func(_ context.Context, root *edit.Node) *edit.Node { return root.At(paths.Doc().Child("a")) },
			want:   true,
		},
		"place that holds no value": {
			before: "a: 1\n",
			node:   func(_ context.Context, root *edit.Node) *edit.Node { return root.At(paths.Doc().Child("z")) },
		},
		"root": {
			before: "a: 1\n",
			node:   func(_ context.Context, root *edit.Node) *edit.Node { return root },
			want:   true,
		},
		"root of an empty document": {
			before: "# only a comment\n",
			node:   func(_ context.Context, root *edit.Node) *edit.Node { return root },
		},
		"path that selects more than one value": {
			before: "items: [1, 2]\n",
			node: func(_ context.Context, root *edit.Node) *edit.Node {
				return root.At(paths.MustParse("$.items[*]"))
			},
			is:  edit.ErrUsage,
			err: "test.yaml:1:1: $: the edit reads $.items[*], which selects more than one value",
		},
		"node that lost its place": {
			before: stringtest.LinesLF("m:", "  a: 1"),
			node: func(_ context.Context, root *edit.Node) *edit.Node {
				at := root.At(paths.Doc().Child("m").Child("a"))
				require.NoError(t, root.At(paths.Doc().Child("m")).Remove())

				return at
			},
			is:  edit.ErrDisplaced,
			err: "test.yaml:1:1: $.m: the edit reads $.m.a through a node that lost its place to an earlier edit",
		},
		"node that lost its place to a Set of its parent": {
			// The Set writes the place again, but the node does not take it
			// back.
			before: stringtest.LinesLF("m:", "  port: 1"),
			node: func(ctx context.Context, root *edit.Node) *edit.Node {
				port := root.At(paths.MustParse("$.m.port"))
				require.NoError(t, root.At(paths.Doc().Child("m")).Set(ctx, map[string]int{"port": 2}))
				assert.True(t, exists(t, root.At(paths.MustParse("$.m.port"))))

				return port
			},
			is:  edit.ErrDisplaced,
			err: "through a node that lost its place to an earlier edit",
		},
		"node whose path reads another value": {
			// The rename gives the node the path $.m.a.d, which reads the
			// value at $.other.d.
			before: stringtest.LinesLF(
				"other: &other {d: 5}",
				"base: &base {b: {d: q}}",
				"m: {<<: *base, a: *other}",
			),
			node: func(_ context.Context, root *edit.Node) *edit.Node {
				held := root.At(paths.MustParse("$.m.b.d"))
				require.NoError(t, root.At(paths.MustParse("$.base.b")).SetKey("a"))

				return held
			},
			is:  edit.ErrDisplaced,
			err: "the edit cannot read this value, because an earlier edit made $.m.a.d read another value",
		},
		"entry that a merge key of a reference document brings in": {
			before: stringtest.LinesLF("svc:", "  <<: *d", "  name: web"),
			ref:    true,
			node: func(_ context.Context, root *edit.Node) *edit.Node {
				return root.At(paths.MustParse("$.svc.cost"))
			},
			is: paths.ErrReferenceAlias,
			err: "test.yaml:2:7: $.svc: the edit reads $.svc.cost, which goes through <<: *d at 2:3, " +
				"a merge key that reads the reference document ref.yaml, so that document decides which names $.svc reads",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var opts []niceyaml.SourceOption

			if tc.ref {
				opts = append(opts, niceyaml.WithReferences(ref))
			}

			d := newDoc(t, tc.before, opts...)

			var (
				got       bool
				gotErr    error
				decodeErr error
			)

			// Exists is a read, so the Apply passes whatever it returns.
			err := d.Apply(t.Context(), paths.Doc(), func(ctx context.Context, root *edit.Node) error {
				n := tc.node(ctx, root)
				got, gotErr = n.Exists()
				_, decodeErr = n.Decode[any](ctx)

				return nil
			})
			require.NoError(t, err)

			assert.Equal(t, tc.want, got)

			if tc.err == "" {
				require.NoError(t, gotErr)

				return
			}

			require.ErrorContains(t, gotErr, tc.err)
			require.NotErrorIs(t, gotErr, edit.ErrEnded)

			// The problem names the read in its details, as the problem of a
			// Decode through the node does.
			assert.Contains(t, niceyaml.FormatError(gotErr), "`-- the edit reads ")
			assert.Equal(t, niceyaml.FormatError(decodeErr), niceyaml.FormatError(gotErr))

			if tc.is != nil {
				require.ErrorIs(t, gotErr, tc.is)
			}

			if !errors.Is(tc.is, edit.ErrDisplaced) {
				require.NotErrorIs(t, gotErr, edit.ErrDisplaced)
			}
		})
	}

	t.Run("node after its Apply", func(t *testing.T) {
		t.Parallel()

		d := newDoc(t, "a: 1\n")

		var kept *edit.Node

		require.NoError(t, d.Apply(t.Context(), paths.Doc(), func(_ context.Context, root *edit.Node) error {
			kept = root

			assert.True(t, exists(t, kept))

			return nil
		}))

		ok, err := kept.Exists()
		assert.False(t, ok)
		require.ErrorIs(t, err, edit.ErrEnded)
		assert.False(t, niceyaml.IsInvalid(err))
	})

	t.Run("guarded Func returns the problem of the path", func(t *testing.T) {
		t.Parallel()

		d := newDoc(t, stringtest.LinesLF("svc:", "  <<: *d", "  name: web"), niceyaml.WithReferences(ref))

		calls := 0

		err := d.Apply(t.Context(), paths.MustParse("$.svc"), func(_ context.Context, n *edit.Node) error {
			calls++

			cost := n.At(paths.Current().Child("cost"))

			ok, err := cost.Exists()
			if err != nil || !ok {
				return err
			}

			return cost.SetKey("price")
		})
		require.ErrorIs(t, err, paths.ErrReferenceAlias)
		assert.Equal(t, 1, calls)
	})

	t.Run("calls through a node that lost its place", func(t *testing.T) {
		t.Parallel()

		lost := map[string]struct {
			before string
			// The path of the node.
			node string
			// The edit that takes the place of the node.
			lose func(ctx context.Context, root *edit.Node) error
			// The location and the path of each problem.
			at string
		}{
			"place that an edit removed": {
				before: stringtest.LinesLF("m:", "  a: 1"),
				node:   "$.m.a",
				lose: func(_ context.Context, root *edit.Node) error {
					return root.At(paths.MustParse("$.m")).Remove()
				},
				at: "test.yaml:1:1: $.m:",
			},
			"path that an edit made read another value": {
				before: stringtest.LinesLF(
					"other: &other {d: 5}",
					"base: &base {b: {d: q}}",
					"m: {<<: *base, a: *other}",
				),
				node: "$.m.b.d",
				lose: func(_ context.Context, root *edit.Node) error {
					return root.At(paths.MustParse("$.base.b")).SetKey("a")
				},
				at: "test.yaml:2:21: $.base.b.d:",
			},
		}

		for name, tc := range lost {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				d := newDoc(t, tc.before)

				var errs map[string]error

				err := d.Apply(t.Context(), paths.Doc(), func(ctx context.Context, root *edit.Node) error {
					n := root.At(paths.MustParse(tc.node))
					require.NoError(t, tc.lose(ctx, root))

					_, decodeErr := n.Decode[any](ctx)
					_, existsErr := n.Exists()

					errs = map[string]error{
						"Decode": decodeErr,
						"Set":    n.Set(ctx, 2),
						"SetKey": n.SetKey("z"),
						"Move":   n.Move(paths.MustParse("$.z")),
						"Remove": n.Remove(),
						"Exists": existsErr,
					}

					// A Func that guards its edits with Exists fails the
					// Apply with the problem of the node.
					return existsErr
				})
				require.ErrorIs(t, err, edit.ErrDisplaced)
				require.ErrorContains(t, err, tc.at)
				assert.True(t, niceyaml.IsInvalid(err))

				for call, err := range errs {
					require.ErrorIs(t, err, edit.ErrDisplaced, call)
					require.ErrorContains(t, err, tc.at, call)
				}
			})
		}
	})
}

func TestNode_Path(t *testing.T) {
	t.Parallel()

	// The entry al0 reads the list of b through an alias.
	list := stringtest.LinesLF("b: &x0", "  - 1", "  - 2", "  - 3", "al0: *x0")

	removeAt := func(path string) edit.Func {
		return func(_ context.Context, root *edit.Node) error { return root.At(paths.MustParse(path)).Remove() }
	}

	setAt := func(path string, v any) edit.Func {
		return func(ctx context.Context, root *edit.Node) error { return root.At(paths.MustParse(path)).Set(ctx, v) }
	}

	// The Func that both returns runs first and then second.
	both := func(first, second edit.Func) edit.Func {
		return func(ctx context.Context, root *edit.Node) error {
			err := first(ctx, root)
			if err != nil {
				return err
			}

			return second(ctx, root)
		}
	}

	renameAt := func(path, key string) edit.Func {
		return func(_ context.Context, root *edit.Node) error { return root.At(paths.MustParse(path)).SetKey(key) }
	}

	moveAt := func(path, to string) edit.Func {
		return func(_ context.Context, root *edit.Node) error {
			return root.At(paths.MustParse(path)).Move(paths.MustParse(to))
		}
	}

	// A call through a node whose place an earlier call took names the
	// place that it asked for (fix21 P66).
	gone := func(path string) string {
		return ": the edit sets " + path + " through a node that lost its place to an earlier edit"
	}

	// Each case holds the node at held, runs edit on the root, and then
	// sets the held node to x.
	tcs := map[string]struct {
		edit   edit.Func
		before string
		held   string
		// The path of the held node after edit.
		path string
		want string
		err  string
		// Whether the held node reads no value after edit, as Exists and
		// Kind report it.
		none bool
		// Whether a node that At gives at held after edit reads a value,
		// which the held node does not take back.
		again bool
	}{
		"item after a removal through an alias before it": {
			before: list,
			held:   "$.b[2]",
			edit:   removeAt("$.al0[0]"),
			path:   "$.b[1]",
			want:   stringtest.LinesLF("b: &x0", "  - 2", "  - x", "al0: *x0"),
		},
		"item through an alias after a removal through the anchor before it": {
			before: list,
			held:   "$.al0[2]",
			edit:   removeAt("$.b[0]"),
			path:   "$.al0[1]",
			want:   stringtest.LinesLF("b: &x0", "  - 2", "  - x", "al0: *x0"),
		},
		"item through an alias after a move through the anchor": {
			before: list,
			held:   "$.al0[0]",
			edit: func(_ context.Context, root *edit.Node) error {
				return root.At(paths.MustParse("$.b[0]")).Move(paths.MustParse("$.z"))
			},
			path: "$.z",
			want: stringtest.LinesLF("b: &x0", "  - 2", "  - 3", "al0: *x0", "z: x"),
		},
		"item through a merge key after a removal through the anchor before it": {
			before: stringtest.LinesLF("d: &d", "  l: [1, 2, 3]", "svc:", "  <<: *d"),
			held:   "$.svc.l[2]",
			edit:   removeAt("$.d.l[0]"),
			path:   "$.svc.l[1]",
			want:   stringtest.LinesLF("d: &d", "  l: [2, x]", "svc:", "  <<: *d"),
		},
		"entry below a value that a move copies": {
			// At $.t.w, the alias *x comes before its anchor, so the move
			// writes a copy, and the held node takes its place in the copy.
			before: stringtest.LinesLF("t: {}", "m: &x 5", "z:", "  k: a", "  b: *x"),
			held:   "$.z.k",
			edit: func(_ context.Context, root *edit.Node) error {
				return root.At(paths.MustParse("$.z")).Move(paths.MustParse("$.t.w"))
			},
			path: "$.t.w.k",
			err:  "test.yaml:4:3: $.z: the edit moves $.z to $.t.w, which puts a block value inside a flow collection, and cafe does not move a block value into a flow collection",
		},
		"entry through an alias after a rename through the anchor": {
			before: stringtest.LinesLF("b: &x0", "  k:", "    m: 1", "al0: *x0"),
			held:   "$.al0.k.m",
			edit: func(_ context.Context, root *edit.Node) error {
				return root.At(paths.MustParse("$.b.k")).SetKey("j")
			},
			path: "$.al0.j.m",
			want: stringtest.LinesLF("b: &x0", "  j:", "    m: x", "al0: *x0"),
		},
		"item through an alias that a removal through the anchor takes away": {
			// The problem binds where a problem at the item that the removal
			// took away binds, at its value (fix20 G2-3).
			before: list,
			held:   "$.al0[0]",
			edit:   removeAt("$.b[0]"),
			err:    "test.yaml:2:5: $.b[0]: the edit sets $.al0[0] through a node that lost its place to an earlier edit",
			none:   true,
		},
		"renamed entry below a mapping that a set replaces": {
			// The problem binds at the key of the place of the set, under the
			// path of that place (fix19 G2-2).
			before: stringtest.LinesLF("r0:", "  t: x"),
			held:   "$.r0.t",
			edit:   both(renameAt("$.r0.t", "n"), setAt("$.r0", map[string]any{"c": 1})),
			path:   "$.r0.n",
			err:    "test.yaml:1:1: $.r0" + gone("$.r0.n"),
			none:   true,
		},
		"item moved into an item that a removal takes away": {
			// The path is that of the removed item (fix19 G2-2).
			before: stringtest.LinesLF("- {p: x}", "- y"),
			held:   "$[1]",
			edit:   both(moveAt("$[1]", "$[0].z1"), removeAt("$[0]")),
			path:   "$[0].z1",
			err:    "test.yaml:1:1: $[0]" + gone("$[0].z1"),
			none:   true,
		},
		"entry moved out of a mapping, then into one that a removal takes away": {
			before: stringtest.LinesLF("m:", "  a: 1", "k: {}"),
			held:   "$.m.a",
			edit:   both(moveAt("$.m.a", "$.k.b"), removeAt("$.k")),
			path:   "$.k.b",
			err:    "test.yaml:3:1: $.k" + gone("$.k.b"),
			none:   true,
		},
		"entry below a later item that a removal of an earlier item shifts, then a removal of the list": {
			// The path is that of the removed list, not $.m.x[0].y, which
			// names the scalar 0 of the user's text (fix19 F5).
			before: stringtest.LinesLF("m:", "  x:", "    - 0", "    - y: 1"),
			held:   "$.m.x[1].y",
			edit:   both(removeAt("$.m.x[0]"), removeAt("$.m.x")),
			path:   "$.m.x[0].y",
			err:    "test.yaml:2:3: $.m.x" + gone("$.m.x[0].y"),
			none:   true,
		},
		"renamed entry below a mapping that a removal takes away": {
			before: stringtest.LinesLF("m:", "  x:", "    y: 1"),
			held:   "$.m.x.y",
			edit:   both(renameAt("$.m.x.y", "w"), removeAt("$.m.x")),
			path:   "$.m.x.w",
			err:    "test.yaml:2:3: $.m.x" + gone("$.m.x.w"),
			none:   true,
		},
		"entry moved within a mapping that a removal takes away": {
			before: stringtest.LinesLF("m:", "  x:", "    y: 1", "    z: {}"),
			held:   "$.m.x.y",
			edit:   both(moveAt("$.m.x.y", "$.m.x.z.q"), removeAt("$.m.x")),
			path:   "$.m.x.z.q",
			err:    "test.yaml:2:3: $.m.x" + gone("$.m.x.z.q"),
			none:   true,
		},
		"entry below a mapping that a removal takes away": {
			before: stringtest.LinesLF("m:", "  x:", "    y: 1"),
			held:   "$.m.x.y",
			edit:   removeAt("$.m.x"),
			path:   "$.m.x.y",
			err:    "test.yaml:2:3: $.m.x" + gone("$.m.x.y"),
			none:   true,
		},
		"entry through an alias below a value that a set through the anchor replaces": {
			before: stringtest.LinesLF("b: &x0", "  k:", "    m: 1", "al0: *x0"),
			held:   "$.al0.k.m",
			edit: func(ctx context.Context, root *edit.Node) error {
				return root.At(paths.MustParse("$.b.k")).Set(ctx, 1)
			},
			err:  "test.yaml:2:3: $.b.k: the edit sets $.al0.k.m through a node that lost its place to an earlier edit",
			none: true,
		},
		"entry through an alias that a set of the alias replaces": {
			// Read fuzz case 28709/5709. The anchored value keeps its entry k,
			// but the node reached it through the alias that the set replaced.
			before: stringtest.LinesLF("b: &x0", "  k: 1", "al0: *x0"),
			held:   "$.al0.k",
			edit:   setAt("$.al0", map[string]any{"k": 2}),
			// The path is that of the place of the set (fix19 G2-2), and the
			// problem binds where a problem at that place binds, at the alias
			// (fix20 G2-3).
			err:  "test.yaml:3:6: $.al0: the edit sets $.al0.k through a node that lost its place to an earlier edit",
			none: true,
		},
		"entry through an alias of a mapping that a set through its anchor replaces": {
			// The set keeps the anchor, so the alias reads the new mapping, and
			// the entry that the node reached through the alias is gone.
			before: stringtest.LinesLF("b: &x0", "  k: 1", "al0: *x0"),
			held:   "$.al0.k",
			edit:   setAt("$.b", map[string]any{"k": 2}),
			err:    "test.yaml:1:1: $.b: the edit sets $.al0.k through a node that lost its place to an earlier edit",
			none:   true,
			again:  true,
		},
		"entry that a set of the mapping above writes again": {
			// The text holds the place again, but the node does not take it
			// back, so the problem says that the node lost its place (fix21
			// B8).
			before: stringtest.LinesLF("o: 0", "m:", "  b: 1"),
			held:   "$.m.b",
			edit:   setAt("$.m", map[string]any{"b": 1}),
			err:    "test.yaml:2:1: $.m" + gone("$.m.b"),
			none:   true,
			again:  true,
		},
		"entry that a removal takes away and a later set writes again": {
			before: stringtest.LinesLF("o: 0", "m:", "  b: 1"),
			held:   "$.m.b",
			edit:   both(removeAt("$.m"), setAt("$.m", map[string]any{"b": 2})),
			err:    "test.yaml:2:1: $.m" + gone("$.m.b"),
			none:   true,
			again:  true,
		},
		"entry through a merge key whose mapping a set replaces": {
			before: stringtest.LinesLF("b: &x0 {k: 1}", "m: {<<: *x0}"),
			held:   "$.m.k",
			edit:   setAt("$.m", map[string]any{"c": 1}),
			// The path is that of the place of the set (fix19 G2-2).
			err:  "test.yaml:2:1: $.m: the edit sets $.m.k through a node that lost its place to an earlier edit",
			none: true,
		},
		"entry through the anchor that a set replaces, where an alias keeps a copy": {
			// The copy at al0 keeps the place that a path through al0 reaches.
			// The node reached its place through the anchor.
			before: stringtest.LinesLF("b: &x0", "  k: 1", "al0: *x0"),
			held:   "$.b.k",
			edit:   setAt("$.b", 5),
			err:    "test.yaml:1:1: $.b: the edit sets $.b.k through a node that lost its place to an earlier edit",
			none:   true,
		},
		"entry through an alias whose anchor a removal takes away": {
			// The removal takes the anchor that the alias reads.
			before: stringtest.LinesLF("b: &x0", "  k: 1", "al0: *x0"),
			held:   "$.al0.k",
			edit:   removeAt("$.b"),
			path:   "$.al0.k",
			err:    "test.yaml:3:6: $.al0: the edit removes $.b, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"entry of the root that a removal takes away": {
			// The root holds no key or "-", so the problem binds at the value
			// that the removal took away, not at the root, where a problem at
			// that value binds (fix20 G2-3).
			before: stringtest.LinesLF("a: 1", "b: 2"),
			held:   "$.a",
			edit:   removeAt("$.a"),
			err:    "test.yaml:1:4: $.a: the edit sets $.a through a node that lost its place to an earlier edit",
			none:   true,
		},
		"entry below a value of the root that a set replaces": {
			// The path of the problem is that of the place of the set, the
			// value that holds the key where the problem binds (fix19 G2-2).
			before: stringtest.LinesLF("a:", "  c: 1", "b: 2"),
			held:   "$.a.c",
			edit: func(ctx context.Context, root *edit.Node) error {
				return root.At(paths.MustParse("$.a")).Set(ctx, 5)
			},
			err:  "test.yaml:1:1: $.a: the edit sets $.a.c through a node that lost its place to an earlier edit",
			none: true,
		},
		"created entry that a removal of the created value above it takes away": {
			// An earlier call created the place, so the problem shows the path of the nearest value above it
			// that the user wrote, not the part that the removed value created (fix19 A2).
			before: stringtest.LinesLF("a: 1"),
			held:   "$.n.m",
			edit:   both(setAt("$.n.m", 1), removeAt("$.n")),
			err:    "test.yaml:1:1: $: the edit sets $.n.m through a node that lost its place to an earlier edit",
			none:   true,
		},
		"created entry below the created value that a removal takes away": {
			// An earlier call created the place, so the problem shows the path of the nearest value above it
			// that the user wrote (fix19 A2).
			before: stringtest.LinesLF("a: 1"),
			held:   "$.n.m.o",
			edit:   both(setAt("$.n.m.o", 1), removeAt("$.n.m")),
			err:    "test.yaml:1:1: $: the edit sets $.n.m.o through a node that lost its place to an earlier edit",
			none:   true,
		},
		"created item below a created value that a removal takes away": {
			// An earlier call created the place, so the problem shows the path of the nearest value above it
			// that the user wrote (fix19 A2).
			before: stringtest.LinesLF("a: 1"),
			held:   "$.n[0]",
			edit:   both(setAt("$.n", []any{1}), removeAt("$.n")),
			err:    "test.yaml:1:1: $: the edit sets $.n[0] through a node that lost its place to an earlier edit",
			none:   true,
		},
		"created entry of a created item that a removal takes away": {
			// The removal takes away an item that the Set created. So the
			// problem binds under the path of the nearest value above it that
			// the user wrote. Its details name the Set that wrote the item and
			// the Set through the held node.
			before: stringtest.LinesLF("z0: 0", "l:", "  - 0"),
			held:   "$.l[1].a.b",
			edit:   both(setAt("$.l[1]", map[string]any{"a": map[string]int{"b": 1}}), removeAt("$.l[1]")),
			err: "test.yaml:2:1: $.l: the edit sets $.l[1].a.b through a node that lost its place to an earlier edit\n" +
				"|-- the edit sets $.l[1]\n" +
				"`-- the edit sets $.l[1].a.b",
			none: true,
		},
		"entry that a rename of a merged entry to its key hides": {
			// The held node keeps its own entry, though $.m.a reads the merged
			// entry, so the set acts on it. The rename hides the entry, which
			// the check of the merges finds.
			before: stringtest.LinesLF("m:", "  a: 1", "  <<: {b: 2}"),
			held:   "$.m.a",
			edit: func(_ context.Context, root *edit.Node) error {
				return root.At(paths.MustParse("$.m.b")).SetKey("a")
			},
			path: "$.m.a",
			err:  `test.yaml:3:3: $.m: the edit renames $.m.b to "a", which goes through a merge key that merges a mapping written in place, and cafe does not edit through such a merge key`,
		},
		"merged entry that a rename hides in the mapping that merges it": {
			before: stringtest.LinesLF("s: &s {b: 1}", "m:", "  <<: *s", "  c: 2"),
			held:   "$.s.b",
			edit: func(_ context.Context, root *edit.Node) error {
				return root.At(paths.MustParse("$.m.c")).SetKey("b")
			},
			path: "$.s.b",
			// The rename made m hide the b of s, so the problem names the
			// rename, not the later set.
			err: `test.yaml:4:3: $.m.c: the edit renames $.m.c to "b", but <<: *s at 3:3 also brings in b, so $.m then hides the b of $.s`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before)

			err := d.Apply(t.Context(), paths.Doc(), func(ctx context.Context, root *edit.Node) error {
				held := root.At(paths.MustParse(tc.held))

				err := tc.edit(ctx, root)
				if err != nil {
					return err
				}

				if tc.path != "" {
					assert.Equal(t, tc.path, held.Path().String())
				}

				ok, err := held.Exists()
				assert.Equal(t, !tc.none, ok)

				if !tc.none {
					require.NoError(t, err)
				}

				if tc.again {
					assert.True(t, exists(t, root.At(paths.MustParse(tc.held))))
				}

				if tc.none {
					assert.Equal(t, niceyaml.NodeNone, held.Kind())
				}

				return held.Set(ctx, "x")
			})

			requireEdit(t, d, err, tc.err, tc.want)
		})
	}
}

func TestNode_Path_Renamed(t *testing.T) {
	t.Parallel()

	// Each case holds the node at held and runs fn with it. A rename can
	// make the path of the held node read another entry, which a later `<<`
	// merge key brings in. The node keeps its own entry, so each call
	// through it reads and edits that entry.
	tcs := map[string]struct {
		fn     func(ctx context.Context, root, held *edit.Node) error
		before string
		held   string
		want   string
		err    string
	}{
		"rename and removal of an anchored entry that a later merge key reads": {
			// The removal writes the anchored value at the alias of the merge
			// key.
			before: stringtest.LinesLF("r0: a b", "r1: &x0 {c: 2, b: q}", "<<: *x0"),
			held:   "$.r1",
			fn: func(_ context.Context, _, held *edit.Node) error {
				err := held.SetKey("b")
				if err != nil {
					return err
				}

				return held.Remove()
			},
			err: `test.yaml:2:1: $.r1: the edit renames $.r1 to "b", but <<: *x0 at 3:1 also brings in b, so $ then hides the b of $.r1`,
		},
		"rename and removal of the first entry, which a later merge key reads": {
			before: stringtest.LinesLF("r1: &x0 {c: 2, b: q}", "<<: *x0"),
			held:   "$.r1",
			fn: func(_ context.Context, _, held *edit.Node) error {
				err := held.SetKey("b")
				if err != nil {
					return err
				}

				return held.Remove()
			},
			err: `test.yaml:1:1: $.r1: the edit renames $.r1 to "b", but <<: *x0 at 2:1 also brings in b, so $ then hides the b of $.r1`,
		},
		"two renames of an entry, the first to a key that a later merge key brings in": {
			// The second rename reveals the merged entry again, so the merge
			// check finds no change.
			before: stringtest.LinesLF("m:", "  b: 2", "  <<: {a: 1}"),
			held:   "$.m.b",
			fn: func(_ context.Context, _, held *edit.Node) error {
				err := held.SetKey("a")
				if err != nil {
					return err
				}

				return held.SetKey("z")
			},
			err: `test.yaml:3:3: $.m: the edit renames $.m.b to "a", which changes the keys of a merge through a merge key that merges a mapping written in place, and cafe does not change the keys of such a merge`,
		},
		"read of an entry that a rename gives the key of a later merged entry": {
			// The decode reads the held entry, though $.m.a reads the merged
			// entry. The check of the merges then finds the rename.
			before: stringtest.LinesLF("m:", "  b: 2", "  <<: {a: 1}"),
			held:   "$.m.b",
			fn: func(ctx context.Context, root, held *edit.Node) error {
				err := root.At(paths.MustParse("$.m.b")).SetKey("a")
				if err != nil {
					return err
				}

				got, err := held.Decode[int](ctx)
				if err != nil {
					return err
				}

				if got != 2 {
					return fmt.Errorf("the held node reads %d, not 2", got)
				}

				return nil
			},
			err: `test.yaml:3:3: $.m: the edit renames $.m.b to "a", which changes the keys of a merge through a merge key that merges a mapping written in place, and cafe does not change the keys of such a merge`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before)

			err := d.Apply(t.Context(), paths.Doc(), func(ctx context.Context, root *edit.Node) error {
				return tc.fn(ctx, root, root.At(paths.MustParse(tc.held)))
			})

			requireEdit(t, d, err, tc.err, tc.want)
		})
	}
}

func TestNode_Own(t *testing.T) {
	t.Parallel()

	// The jobs a and b merge the anchored mapping of x-defaults, and c is
	// an alias of it.
	jobs := stringtest.LinesLF(
		"x-defaults: &d {retries: 1, image: alpine}",
		"jobs:",
		"  a: {<<: *d, script: build}",
		"  b: {<<: *d, script: test}",
		"c: *d",
	)

	// The entry m is an alias of the sequence of l.
	list := stringtest.LinesLF("l: &l [1, {k: 2}]", "m: *l")

	// The mapping of b merges the anchored mapping of a, and the mappings
	// of c and e read both.
	nested := stringtest.LinesLF(
		"a: &a {k: 1}",
		"b: &b {<<: *a, j: 2}",
		"c: {<<: [*b, {z: 9}]}",
		"d: {<<: {y: 1}}",
		"e: [*a, *b]",
	)

	// The mapping of c writes the anchored mapping of m in place at its
	// merge key. The mapping of f writes the anchored mapping of n in the
	// sequence of its merge key. The entries e and g are aliases of those
	// mappings.
	written := stringtest.LinesLF(
		"c: {<<: &m {z: 9}}",
		"e: *m",
		"f: {<<: [&n {y: 8}]}",
		"g: *n",
	)

	// The mapping of c writes the entry z itself, so it hides the entry z of
	// the anchored mapping of m at its merge key. The mapping of f writes the
	// entry y itself, so it hides the entry y at its merge key, whose value
	// is the anchored mapping of n. The entries e and g are aliases of those
	// mappings.
	mergeHidden := stringtest.LinesLF(
		"c: {<<: &m {z: {w: 1}}, z: 1}",
		"e: *m",
		"f: {<<: {y: &n {w: 8}}, y: 1}",
		"g: *n",
	)

	// The last entry of k hides the first one, whose value holds the
	// anchored mapping of m. The last entry of j hides the anchored mapping
	// of n.
	hidden := stringtest.LinesLF(
		"k: {i: &m {x: 1}}",
		"k: 2",
		"j: &n {y: 1}",
		"j: 3",
		"c: *m",
		"e: *n",
	)

	// The last entry of k hides the first one, whose value is the anchored
	// sequence of s.
	hiddenList := stringtest.LinesLF("k: &s [1, 2]", "k: [5, 6]", "c: *s")

	// The last entry of k hides the first one, whose value holds the
	// anchored sequence of s.
	hiddenInner := stringtest.LinesLF("k: {i: &s [1, 2]}", "k: {i: [5, 6]}", "c: *s")

	tcs := map[string]struct {
		before string
		path   string
		want   string
		dup    bool
	}{
		"root": {before: jobs, path: "$", want: "$"},
		"entry of the anchored mapping": {
			before: jobs, path: "$.x-defaults.retries", want: "$.x-defaults.retries",
		},
		"entry that a mapping with a merge key writes": {
			before: jobs, path: "$.jobs.a.script", want: "$.jobs.a.script",
		},
		"mapping with a merge key": {before: jobs, path: "$.jobs.a", want: "$.jobs.a"},
		"entry that a merge key brings in": {
			before: jobs, path: "$.jobs.a.retries", want: "$.x-defaults.retries",
		},
		"entry that a second merge key brings in": {
			before: jobs, path: "$.jobs.b.retries", want: "$.x-defaults.retries",
		},
		"entry whose value is an alias": {before: jobs, path: "$.c", want: "$.c"},
		"entry below an alias":          {before: jobs, path: "$.c.image", want: "$.x-defaults.image"},
		"place without a value below an alias": {
			before: jobs, path: "$.c.owner", want: "$.x-defaults.owner",
		},
		"place without a value two steps below an alias": {
			before: jobs, path: "$.c.owner.name", want: "$.x-defaults.owner.name",
		},
		"place without a value below an entry that a merge key brings in": {
			before: jobs, path: "$.jobs.a.retries.max", want: "$.x-defaults.retries.max",
		},
		"place without a value in a mapping with a merge key": {
			before: jobs, path: "$.jobs.a.owner", want: "$.jobs.a.owner",
		},
		"place without a value on a path without an alias": {
			before: jobs, path: "$.owner.name", want: "$.owner.name",
		},
		"item below an alias":             {before: list, path: "$.m[0]", want: "$.l[0]"},
		"entry of an item below an alias": {before: list, path: "$.m[1].k", want: "$.l[1].k"},
		"item past the end of a sequence below an alias": {
			before: list, path: "$.m[5]", want: "$.l[5]",
		},
		"place without a value in an item below an alias": {
			before: list, path: "$.m[1].z", want: "$.l[1].z",
		},
		"entry that a merge key of a merged mapping brings in": {
			before: nested, path: "$.b.k", want: "$.a.k",
		},
		"entry that a merge key of a sequence brings in": {
			before: nested, path: "$.c.j", want: "$.b.j",
		},
		"entry of a merged mapping that a merge key of a sequence brings in": {
			before: nested, path: "$.c.k", want: "$.a.k",
		},
		"entry of a mapping written in a merge key of a sequence": {
			before: nested, path: "$.c.z", want: "$.c.z",
		},
		"entry of a mapping written at a merge key": {before: nested, path: "$.d.y", want: "$.d.y"},
		"item whose value is an alias":              {before: nested, path: "$.e[1]", want: "$.e[1]"},
		"entry through an alias and its merge key": {
			before: nested, path: "$.e[1].k", want: "$.a.k",
		},
		"place without a value below an alias of a mapping with a merge key": {
			before: nested, path: "$.e[1].owner", want: "$.b.owner",
		},
		"entry of an anchored mapping written at a merge key, below an alias": {
			before: written, path: "$.e.z", want: "$.c.z",
		},
		"place without a value in an anchored mapping written at a merge key, below an alias": {
			// The path $.c.q names a place in the mapping of c, so no path
			// without an alias names the place.
			before: written, path: "$.e.q", want: "$.e.q",
		},
		"entry of an anchored mapping written in a merge key of a sequence, below an alias": {
			before: written, path: "$.g.y", want: "$.f.y",
		},
		"place without a value in an anchored mapping written in a merge key of a sequence, below an alias": {
			before: written, path: "$.g.q", want: "$.g.q",
		},
		"entry at a merge key that its mapping hides, below an alias": {
			// The path $.c.z names the entry z that the mapping of c writes
			// itself, so no path without an alias names the entry.
			before: mergeHidden, path: "$.e.z", want: "$.e.z",
		},
		"entry below an entry at a merge key that its mapping hides, below an alias": {
			before: mergeHidden, path: "$.e.z.w", want: "$.e.z.w",
		},
		"place without a value below an entry at a merge key that its mapping hides, below an alias": {
			before: mergeHidden, path: "$.e.z.q", want: "$.e.z.q",
		},
		"entry below an alias of an anchor inside an entry at a merge key that its mapping hides": {
			before: mergeHidden, path: "$.g.w", want: "$.g.w",
		},
		"place without a value below an alias of an anchor inside an entry at a merge key that its mapping hides": {
			before: mergeHidden, path: "$.g.q", want: "$.g.q",
		},
		"entry below an alias of an anchor inside an entry that a later entry of its key hides": {
			before: hidden, path: "$.c.x", want: "$.c.x", dup: true,
		},
		"place without a value below an alias of an anchor inside an entry that a later entry of its key hides": {
			before: hidden, path: "$.c.q", want: "$.c.q", dup: true,
		},
		"entry below an alias of the anchor of an entry that a later entry of its key hides": {
			before: hidden, path: "$.e.y", want: "$.e.y", dup: true,
		},
		"place without a value below an alias of the anchor of an entry that a later entry of its key hides": {
			before: hidden, path: "$.e.q", want: "$.e.q", dup: true,
		},
		"item below an alias of the anchor of an entry that a later entry of its key hides": {
			// The path $.k[0] names the item 5 of the last entry of k, so
			// no path without an alias names the item 1.
			before: hiddenList, path: "$.c[0]", want: "$.c[0]", dup: true,
		},
		"last item below an alias of the anchor of an entry that a later entry of its key hides": {
			before: hiddenList, path: "$.c[1]", want: "$.c[1]", dup: true,
		},
		"item below an alias of an anchor inside an entry that a later entry of its key hides": {
			before: hiddenInner, path: "$.c[0]", want: "$.c[0]", dup: true,
		},
		"path that selects more than one value": {
			before: jobs, path: "$.jobs.*.retries", want: "$.jobs.*.retries",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before, niceyaml.WithAllowDuplicateKeys(tc.dup))

			var ended *edit.Node

			err := applyAt(t, d, paths.MustParse(tc.path), func(_ context.Context, n *edit.Node) error {
				ended = n

				assert.Equal(t, tc.path, n.Path().String())
				assert.Equal(t, tc.want, n.Own().String())

				return nil
			})
			require.NoError(t, err)

			assert.Equal(t, tc.path, ended.Own().String(), "after the Apply returned")
		})
	}
}

func TestNode_Own_Selected(t *testing.T) {
	t.Parallel()

	type selected struct {
		Path string
		Own  string
	}

	// Each case reads the node that the Apply at path gives, or the node at
	// rel below it.
	tcs := map[string]struct {
		before string
		path   string
		rel    string
		dup    bool
		want   []selected
	}{
		"entry that two merge keys bring in takes one call": {
			before: stringtest.LinesLF("base: &b {k: 1}", "jobs:", "  a: {<<: *b}", "  b: {<<: *b}"),
			path:   "$.jobs.*.k",
			want:   []selected{{Path: "$.jobs.a.k", Own: "$.base.k"}},
		},
		"entry of the anchored mapping that a recursive selector selects": {
			// The path reads no alias, so the own path equals it, though
			// both merge keys read an edit there.
			before: stringtest.LinesLF("base: &b {k: 1}", "jobs:", "  a: {<<: *b}", "  b: {<<: *b}"),
			path:   "$..k",
			want:   []selected{{Path: "$.base.k", Own: "$.base.k"}},
		},
		"entry that a later entry of its key hides below an alias": {
			// No path selects the first entry of k, so its node gives its
			// path.
			before: stringtest.LinesLF("base: &b", "  k: 1", "  k: 2", "c: *b"),
			path:   "$.c.*",
			dup:    true,
			want:   []selected{{Path: "$.c.k", Own: "$.c.k"}, {Path: "$.c.k", Own: "$.base.k"}},
		},
		"place without a value below an entry that a later entry of its key hides below an alias": {
			// The path $.base.k.q names a place in the last entry of k, so
			// the node below the first entry gives its path.
			before: stringtest.LinesLF("base: &b", "  k: {x: 1}", "  k: {x: 2}", "c: *b"),
			path:   "$.c.*",
			rel:    "@.q",
			dup:    true,
			want:   []selected{{Path: "$.c.k.q", Own: "$.c.k.q"}, {Path: "$.c.k.q", Own: "$.base.k.q"}},
		},
		"entries of one key on a path without an alias": {
			// The nodes read different entries, and they give one path and
			// one own path.
			before: stringtest.LinesLF("k: 1", "k: 2"),
			path:   "$.*",
			dup:    true,
			want:   []selected{{Path: "$.k", Own: "$.k"}, {Path: "$.k", Own: "$.k"}},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before, niceyaml.WithAllowDuplicateKeys(tc.dup))

			var got []selected

			err := d.Apply(t.Context(), paths.MustParse(tc.path), func(_ context.Context, n *edit.Node) error {
				if tc.rel != "" {
					n = n.At(paths.MustParse(tc.rel))
				}

				got = append(got, selected{Path: n.Path().String(), Own: n.Own().String()})

				return nil
			})
			require.NoError(t, err)

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestNode_Own_Edited(t *testing.T) {
	t.Parallel()

	jobs := stringtest.LinesLF(
		"x-defaults: &d {retries: 1, image: alpine}",
		"jobs:",
		"  a: {<<: *d, script: build}",
		"  b: {<<: *d, script: test}",
		"c: *d",
	)

	list := stringtest.LinesLF("l: &l", "  - 1", "  - 2", "m: *l")

	// The mapping of m merges the anchored mapping of base, and its entry a
	// is an alias of the anchored mapping of other.
	renamed := stringtest.LinesLF(
		"other: &other {d: 5}",
		"base: &base {b: {d: q}}",
		"m: {<<: *base, a: *other}",
	)

	// The document of the cases that set ref reads this reference document.
	ref := niceyaml.NewSourceFromString("r: &r {x: 1}\n", niceyaml.WithName("ref.yaml"))

	// The entry v is an alias of the anchored mapping of the reference
	// document. The entry c is an alias of the anchored mapping of base.
	moved := stringtest.LinesLF("v: *r", "base: &b {k: 1}", "c: *b")

	// Each case holds the node at held, checks its own path, runs fn, and
	// checks its path and its own path again.
	tcs := map[string]struct {
		fn     func(ctx context.Context, root, held *edit.Node) error
		before string
		held   string
		// The own path of the held node before fn.
		own string
		// The path and the own path of the held node after fn.
		path  string
		after string
		// A part of the message of the error of the held node after fn,
		// or "" for nil.
		err string
		// The error that the error of the held node matches after fn, or
		// nil for none.
		is   error
		want string
		// Whether the Source reads the reference document.
		ref bool
	}{
		"set of an entry that a merge key brings in": {
			before: jobs,
			held:   "$.jobs.a.retries",
			own:    "$.x-defaults.retries",
			fn:     func(ctx context.Context, _, held *edit.Node) error { return held.Set(ctx, 5) },
			path:   "$.jobs.a.retries",
			after:  "$.x-defaults.retries",
			want: stringtest.LinesLF(
				"x-defaults: &d {retries: 5, image: alpine}",
				"jobs:",
				"  a: {<<: *d, script: build}",
				"  b: {<<: *d, script: test}",
				"c: *d",
			),
		},
		"set that creates an entry below an alias": {
			before: jobs,
			held:   "$.c.owner",
			own:    "$.x-defaults.owner",
			fn:     func(ctx context.Context, _, held *edit.Node) error { return held.Set(ctx, "ops") },
			path:   "$.c.owner",
			after:  "$.x-defaults.owner",
			want: stringtest.LinesLF(
				"x-defaults: &d {retries: 1, image: alpine, owner: ops}",
				"jobs:",
				"  a: {<<: *d, script: build}",
				"  b: {<<: *d, script: test}",
				"c: *d",
			),
		},
		"set that creates a mapping and its entry below an alias": {
			before: jobs,
			held:   "$.c.owner.name",
			own:    "$.x-defaults.owner.name",
			fn:     func(ctx context.Context, _, held *edit.Node) error { return held.Set(ctx, "ops") },
			path:   "$.c.owner.name",
			after:  "$.x-defaults.owner.name",
			want: stringtest.LinesLF(
				"x-defaults: &d {retries: 1, image: alpine, owner: {name: ops}}",
				"jobs:",
				"  a: {<<: *d, script: build}",
				"  b: {<<: *d, script: test}",
				"c: *d",
			),
		},
		"set that appends an item below an alias": {
			before: list,
			held:   "$.m[2]",
			own:    "$.l[2]",
			fn:     func(ctx context.Context, _, held *edit.Node) error { return held.Set(ctx, 3) },
			path:   "$.m[2]",
			after:  "$.l[2]",
			want:   stringtest.LinesLF("l: &l", "  - 1", "  - 2", "  - 3", "m: *l"),
		},
		"rename of an entry below an alias": {
			before: jobs,
			held:   "$.c.image",
			own:    "$.x-defaults.image",
			fn:     func(_ context.Context, _, held *edit.Node) error { return held.SetKey("img") },
			path:   "$.c.img",
			after:  "$.x-defaults.img",
			want: stringtest.LinesLF(
				"x-defaults: &d {retries: 1, img: alpine}",
				"jobs:",
				"  a: {<<: *d, script: build}",
				"  b: {<<: *d, script: test}",
				"c: *d",
			),
		},
		"move of an entry below an alias out of the anchored value": {
			before: jobs,
			held:   "$.c.image",
			own:    "$.x-defaults.image",
			fn: func(_ context.Context, _, held *edit.Node) error {
				return held.Move(paths.MustParse("$.image"))
			},
			path:  "$.image",
			after: "$.image",
			want: stringtest.LinesLF(
				"x-defaults: &d {retries: 1}",
				"jobs:",
				"  a: {<<: *d, script: build}",
				"  b: {<<: *d, script: test}",
				"c: *d",
				"image: alpine",
			),
		},
		"insert before an item below an alias": {
			before: list,
			held:   "$.m[1]",
			own:    "$.l[1]",
			fn: func(ctx context.Context, root, _ *edit.Node) error {
				return root.At(paths.MustParse("$.l[0]")).Insert(ctx, 0)
			},
			path:  "$.m[2]",
			after: "$.l[2]",
			want:  stringtest.LinesLF("l: &l", "  - 0", "  - 1", "  - 2", "m: *l"),
		},
		"remove of an entry below an alias through the anchored value": {
			// The held node lost its place, so it gives its path, though
			// that path still reads the alias.
			before: jobs,
			held:   "$.c.image",
			own:    "$.x-defaults.image",
			fn: func(_ context.Context, root, _ *edit.Node) error {
				return root.At(paths.MustParse("$.x-defaults.image")).Remove()
			},
			path:  "$.c.image",
			after: "$.c.image",
			err:   "the edit reads $.c.image through a node that lost its place to an earlier edit",
			want: stringtest.LinesLF(
				"x-defaults: &d {retries: 1}",
				"jobs:",
				"  a: {<<: *d, script: build}",
				"  b: {<<: *d, script: test}",
				"c: *d",
			),
		},
		"set of the alias above an entry": {
			// The scalar at $.c leaves no alias on the path of the held
			// node, so the node gives its path.
			before: jobs,
			held:   "$.c.image",
			own:    "$.x-defaults.image",
			fn: func(ctx context.Context, root, _ *edit.Node) error {
				return root.At(paths.MustParse("$.c")).Set(ctx, 1)
			},
			path:  "$.c.image",
			after: "$.c.image",
			err:   "the edit reads $.c.image through a node that lost its place to an earlier edit",
			want: stringtest.LinesLF(
				"x-defaults: &d {retries: 1, image: alpine}",
				"jobs:",
				"  a: {<<: *d, script: build}",
				"  b: {<<: *d, script: test}",
				"c: 1",
			),
		},
		"rename that makes the path of a node read another value": {
			// The rename gives the node the path $.m.a.d. The mapping of m
			// writes an entry a, so that path reads the value at $.other.d,
			// and the node cannot read its place.
			before: renamed,
			held:   "$.m.b.d",
			own:    "$.base.b.d",
			fn: func(_ context.Context, root, _ *edit.Node) error {
				return root.At(paths.MustParse("$.base.b")).SetKey("a")
			},
			path:  "$.m.a.d",
			after: "$.m.a.d",
			err:   "the edit cannot read this value, because an earlier edit made $.m.a.d read another value",
			want: stringtest.LinesLF(
				"other: &other {d: 5}",
				"base: &base {a: {d: q}}",
				"m: {<<: *base, a: *other}",
			),
		},
		"move that puts an alias of a reference document on the path of a node": {
			// The move leaves the alias *r at $.base.b, so the path of the
			// held node reads the reference document. The node cannot read
			// its place, so it gives its path.
			before: moved,
			ref:    true,
			held:   "$.c.b.c",
			own:    "$.base.b.c",
			fn: func(_ context.Context, root, _ *edit.Node) error {
				return root.At(paths.MustParse("$.v")).Move(paths.MustParse("$.base.b"))
			},
			path:  "$.c.b.c",
			after: "$.c.b.c",
			err:   "the edit reads $.c.b.c, which goes through *r, an alias of a reference document",
			is:    paths.ErrReferenceAlias,
			want:  stringtest.LinesLF("base: &b {k: 1, b: *r}", "c: *b"),
		},
		"rename that puts an alias of a reference document on the path of a node": {
			// The rename leaves the alias *r at $.base.w, so the path of the
			// held node reads the reference document.
			before: stringtest.LinesLF("base: &b {k: 1, v: *r}", "c: *b"),
			ref:    true,
			held:   "$.c.w.z",
			own:    "$.base.w.z",
			fn: func(_ context.Context, root, _ *edit.Node) error {
				return root.At(paths.MustParse("$.base.v")).SetKey("w")
			},
			path:  "$.c.w.z",
			after: "$.c.w.z",
			err:   "the edit reads $.c.w.z, which goes through *r, an alias of a reference document",
			is:    paths.ErrReferenceAlias,
			want:  stringtest.LinesLF("base: &b {k: 1, w: *r}", "c: *b"),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var opts []niceyaml.SourceOption

			if tc.ref {
				opts = append(opts, niceyaml.WithReferences(ref))
			}

			d := newDoc(t, tc.before, opts...)

			err := d.Apply(t.Context(), paths.Doc(), func(ctx context.Context, root *edit.Node) error {
				held := root.At(paths.MustParse(tc.held))
				assert.Equal(t, tc.own, held.Own().String())
				require.NoError(t, existsErr(held))

				err := tc.fn(ctx, root, held)
				if err != nil {
					return err
				}

				assert.Equal(t, tc.path, held.Path().String())
				assert.Equal(t, tc.after, held.Own().String())

				if tc.is != nil {
					require.ErrorIs(t, existsErr(held), tc.is)
				}

				if tc.err == "" {
					assert.NoError(t, existsErr(held))
				} else {
					assert.ErrorContains(t, existsErr(held), tc.err)
				}

				return nil
			})

			requireEdit(t, d, err, "", tc.want)
		})
	}
}

func TestNode_Own_FailedCall(t *testing.T) {
	t.Parallel()

	// Go-yaml reads a new line after the tag of a as the value of the tag,
	// so a Set of a new key differs from the model after it wrote. The Set
	// leaves the Doc as it found it and returns nil, so each node reads its
	// place again. The Apply fails with ErrEngine.
	before := stringtest.LinesLF("x: &d {k: 1}", "c: *d", "a: !t")

	d := newDoc(t, before)

	err := d.Apply(t.Context(), paths.Doc(), func(ctx context.Context, root *edit.Node) error {
		held := root.At(paths.MustParse("$.c.k"))
		assert.Equal(t, "$.x.k", held.Own().String())

		err := root.At(paths.Doc().Child("b")).Set(ctx, 9)
		require.NoError(t, err)

		require.NoError(t, existsErr(held))
		assert.Equal(t, "$.c.k", held.Path().String())
		assert.Equal(t, "$.x.k", held.Own().String())
		assert.Equal(t, before, d.Text())

		return nil
	})
	require.ErrorIs(t, err, edit.ErrEngine)

	assert.Equal(t, before, d.Text())
}

func TestNode_Own_UnresolvedAlias(t *testing.T) {
	t.Parallel()

	// Exists returns the problem of an alias that does not resolve, but Own
	// still returns the path where the text writes the alias.
	tcs := map[string]struct {
		before string
		path   string
		want   string
	}{
		"alias at the place": {
			before: "a: *nope\n",
			path:   "$.a",
			want:   "$.a",
		},
		"alias at the place, below an alias": {
			before: stringtest.LinesLF("x: &x {b: *nope}", "y: *x"),
			path:   "$.y.b",
			want:   "$.x.b",
		},
		"alias at the place, below an alias of a block mapping": {
			before: stringtest.LinesLF("x: &x", "  b: *nope", "y: *x"),
			path:   "$.y.b",
			want:   "$.x.b",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before)

			// The decode of the document after the Func fails the Apply.
			err := applyAt(t, d, paths.MustParse(tc.path), func(_ context.Context, n *edit.Node) error {
				ok, err := n.Exists()
				require.Error(t, err)
				assert.False(t, ok)
				assert.Contains(t, err.Error(), "*nope, an alias that does not resolve")

				assert.Equal(t, tc.path, n.Path().String())
				assert.Equal(t, tc.want, n.Own().String())

				return nil
			})
			require.ErrorIs(t, err, niceyaml.ErrDecode)
		})
	}
}

func TestNode_Own_Reference(t *testing.T) {
	t.Parallel()

	// The mapping of the reference document holds the names x, k, and j.
	ref := niceyaml.NewSourceFromString("r: &r {x: 1, k: 2, j: 3}\n", niceyaml.WithName("ref.yaml"))

	// The mapping of x writes the entry k before its merge key of the
	// reference document, so that document decides the path $.x.k. The
	// entry c is an alias of the anchored mapping of k, and the mapping of d
	// merges it.
	before := stringtest.LinesLF(
		"x:",
		"  k: &m",
		"    a: 1",
		"    l: [1, {z: 2}]",
		"  <<: *r",
		"c: *m",
		"d: {<<: *m}",
	)

	// The mapping of x writes the entry k after its merge key, so the
	// reference document does not decide the path $.x.k.
	after := stringtest.LinesLF("x:", "  <<: *r", "  k: &m", "    a: 1", "c: *m")

	// The reference document decides the path $.top.x, two steps above the
	// anchored mapping.
	above := stringtest.LinesLF("top:", "  x:", "    k: &m {a: 1}", "  <<: *r", "c: *m")

	// The last entry of k hides the first one, which holds the anchored
	// mapping.
	hidden := stringtest.LinesLF("x:", "  k: &m {a: 1}", "  k: 2", "  <<: *r", "c: *m")

	// The mapping of x writes the anchored mapping in the sequence of its
	// merge key, before the alias of the reference document. That document
	// does not hold the name a, so the path $.x.a selects the entry of the
	// anchored mapping.
	listed := stringtest.LinesLF("x: {<<: [&m {a: 1}, *r]}", "c: *m")

	// The mapping of x and the mapping of top both write an entry before
	// their merge key of the reference document, so that document decides
	// the paths $.top.x and $.top.x.k.
	twice := stringtest.LinesLF("top:", "  x:", "    k: &m {a: 1}", "    <<: *r", "  <<: *r", "c: *m")

	// The mapping of x writes the entry k in a mapping in the sequence of its
	// merge key, so it writes no entry of the name k itself.
	listedEntry := stringtest.LinesLF("x: {<<: [{k: &m {a: 1}}, *r]}", "c: *m")

	// The entry k holds an anchored sequence.
	listedItems := stringtest.LinesLF("x: {<<: [{k: &m [1, 2]}, *r]}", "c: *m")

	// The reference document decides the path $.top.x, and the mapping of x
	// writes no entry of the name k itself.
	aboveListed := stringtest.LinesLF("top: {x: {<<: [{k: &m {a: 1}}, *r]}, <<: *r}", "c: *m")

	// The mapping of j writes the anchored mapping in place at its merge
	// key, and the reference document decides the path $.x.j.
	written := stringtest.LinesLF("x: {j: {<<: &m {a: 1}}, <<: *r}", "c: *m")

	tcs := map[string]struct {
		before string
		path   string
		want   string
		dup    bool
		// Whether the reference document decides the own path, so At
		// fails there.
		decided bool
	}{
		"entry below an alias": {
			before: before, path: "$.c.a", want: "$.x.k.a", decided: true,
		},
		"entry that a merge key brings in": {
			before: before, path: "$.d.a", want: "$.x.k.a", decided: true,
		},
		"item below an alias": {
			before: before, path: "$.c.l[0]", want: "$.x.k.l[0]", decided: true,
		},
		"entry of an item below an alias": {
			before: before, path: "$.c.l[1].z", want: "$.x.k.l[1].z", decided: true,
		},
		"place without a value below an alias": {
			before: before, path: "$.c.q", want: "$.x.k.q", decided: true,
		},
		"place without a value two steps below an alias": {
			before: before, path: "$.c.q.r", want: "$.x.k.q.r", decided: true,
		},
		"item past the end of a sequence below an alias": {
			before: before, path: "$.c.l[5]", want: "$.x.k.l[5]", decided: true,
		},
		"place without a value in an item below an alias": {
			before: before, path: "$.c.l[1].q", want: "$.x.k.l[1].q", decided: true,
		},
		"place without a value in the mapping with the merge key": {
			before: before, path: "$.d.q", want: "$.d.q",
		},
		"entry whose value is an alias": {before: before, path: "$.c", want: "$.c"},
		"entry below an alias of an anchor after the merge key": {
			before: after, path: "$.c.a", want: "$.x.k.a",
		},
		"place without a value below an alias of an anchor after the merge key": {
			before: after, path: "$.c.q", want: "$.x.k.q",
		},
		"entry below an alias of an anchor two steps below the decided mapping": {
			before: above, path: "$.c.a", want: "$.top.x.k.a", decided: true,
		},
		"place without a value below an alias of an anchor two steps below the decided mapping": {
			before: above, path: "$.c.q", want: "$.top.x.k.q", decided: true,
		},
		"entry below an alias of the anchor of an entry that a later entry of its key hides": {
			before: hidden, path: "$.c.a", want: "$.c.a", dup: true,
		},
		"place without a value below an alias of the anchor of an entry that a later entry of its key hides": {
			before: hidden, path: "$.c.q", want: "$.c.q", dup: true,
		},
		"entry of an anchored mapping written in a merge key of a sequence, below an alias": {
			before: listed, path: "$.c.a", want: "$.x.a",
		},
		"entry below an alias of an anchor below two decided mappings": {
			before: twice, path: "$.c.a", want: "$.top.x.k.a", decided: true,
		},
		"place without a value below an alias of an anchor below two decided mappings": {
			before: twice, path: "$.c.q", want: "$.top.x.k.q", decided: true,
		},
		"entry below an alias of an anchor in a mapping in a merge key of a sequence": {
			before: listedEntry, path: "$.c.a", want: "$.c.a",
		},
		"place without a value below an alias of an anchor in a mapping in a merge key of a sequence": {
			before: listedEntry, path: "$.c.q", want: "$.c.q",
		},
		"place without a value two steps below an alias of an anchor in a mapping in a merge key of a sequence": {
			before: listedEntry, path: "$.c.q.w", want: "$.c.q.w",
		},
		"item below an alias of an anchor in a mapping in a merge key of a sequence": {
			before: listedItems, path: "$.c[0]", want: "$.c[0]",
		},
		"item past the end below an alias of an anchor in a mapping in a merge key of a sequence": {
			before: listedItems, path: "$.c[2]", want: "$.c[2]",
		},
		"entry below an alias of an anchor in a merge key of a sequence, below a decided mapping": {
			before: aboveListed, path: "$.c.a", want: "$.c.a",
		},
		"place without a value below an alias of an anchor in a merge key of a sequence, below a decided mapping": {
			before: aboveListed, path: "$.c.q", want: "$.c.q",
		},
		"entry of an anchored mapping written at a merge key, below an alias": {
			before: written, path: "$.c.a", want: "$.x.j.a", decided: true,
		},
		"place without a value in an anchored mapping written at a merge key, below an alias": {
			before: written, path: "$.c.q", want: "$.c.q",
		},
		"path through the name that the reference document decides": {
			// The node cannot read its place, so it gives its path.
			before: before, path: "$.x.k.a", want: "$.x.k.a", decided: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before, niceyaml.WithReferences(ref), niceyaml.WithAllowDuplicateKeys(tc.dup))

			err := d.Apply(t.Context(), paths.Doc(), func(_ context.Context, root *edit.Node) error {
				own := root.At(paths.MustParse(tc.path)).Own()
				assert.Equal(t, tc.want, own.String())

				if tc.decided {
					require.ErrorIs(t, existsErr(root.At(own)), paths.ErrReferenceAlias)
				} else {
					require.NoError(t, existsErr(root.At(own)))
				}

				return nil
			})
			require.NoError(t, err)
		})
	}
}

func TestNode_Own_Shared(t *testing.T) {
	t.Parallel()

	before := stringtest.LinesLF(
		"x-defaults: &d {retries: 1, image: alpine}",
		"jobs:",
		"  a: {<<: *d, script: build}",
		"  b: {<<: *d, script: test}",
		"  c: {retries: 2, image: busybox}",
	)

	// The mapping of x-defaults writes the anchored mapping before its
	// merge key of the reference document, so that document decides the
	// path $.x-defaults.base.
	decided := stringtest.LinesLF(
		"x-defaults:",
		"  base: &d {retries: 1, image: alpine}",
		"  <<: *r",
		"jobs:",
		"  a: {<<: *d, script: build}",
		"  b: {<<: *d, script: test}",
		"  c: {retries: 2, image: busybox}",
	)

	// The mapping of the reference document holds the name base.
	ref := niceyaml.NewSourceFromString("r: &r {x: 1, base: 2}\n", niceyaml.WithName("ref.yaml"))

	// Once returns a Func that runs fn on the node at rel below each node
	// that it gets, one time for each own path.
	once := func(rel paths.Path, fn edit.Func) edit.Func {
		seen := map[string]bool{}

		return func(ctx context.Context, n *edit.Node) error {
			at := n.At(rel)

			ok, err := at.Exists()
			if err != nil {
				return err
			}

			own := at.Own().String()
			if !ok || seen[own] {
				return nil
			}

			seen[own] = true

			return fn(ctx, at)
		}
	}

	tenfold := func(ctx context.Context, n *edit.Node) error {
		v, err := n.Decode[int](ctx)
		if err != nil {
			return err
		}

		return n.Set(ctx, v*10)
	}

	toImg := func(_ context.Context, n *edit.Node) error {
		holder, _ := n.Own().Parent()

		return n.Move(holder.Child("img"))
	}

	unshared := func(ctx context.Context, n *edit.Node) error {
		at := n.At(paths.Current().Child("retries"))
		if !at.Own().Equal(at.Path()) {
			return nil
		}

		return at.Set(ctx, 5)
	}

	tcs := map[string]struct {
		fn     edit.Func
		before string
		// The text after the Apply, or the error of the Apply.
		want, err string
	}{
		"set that reads the value runs one time for each anchored value": {
			before: before,
			fn:     once(paths.Current().Child("retries"), tenfold),
			want: stringtest.LinesLF(
				"x-defaults: &d {retries: 10, image: alpine}",
				"jobs:",
				"  a: {<<: *d, script: build}",
				"  b: {<<: *d, script: test}",
				"  c: {retries: 20, image: busybox}",
			),
		},
		"move to the parent of the own path stays in the anchored value": {
			before: before,
			fn:     once(paths.Current().Child("image"), toImg),
			want: stringtest.LinesLF(
				"x-defaults: &d {retries: 1, img: alpine}",
				"jobs:",
				"  a: {<<: *d, script: build}",
				"  b: {<<: *d, script: test}",
				"  c: {retries: 2, img: busybox}",
			),
		},
		"set of only the entries that the mapping writes itself": {
			before: before,
			fn:     unshared,
			want: stringtest.LinesLF(
				"x-defaults: &d {retries: 1, image: alpine}",
				"jobs:",
				"  a: {<<: *d, script: build}",
				"  b: {<<: *d, script: test}",
				"  c: {retries: 5, image: busybox}",
			),
		},
		"set that reads the value runs one time for an anchored value at a path that a reference document decides": {
			before: decided,
			fn:     once(paths.Current().Child("retries"), tenfold),
			want: stringtest.LinesLF(
				"x-defaults:",
				"  base: &d {retries: 10, image: alpine}",
				"  <<: *r",
				"jobs:",
				"  a: {<<: *d, script: build}",
				"  b: {<<: *d, script: test}",
				"  c: {retries: 20, image: busybox}",
			),
		},
		"set of only the entries that the mapping writes itself, with an anchored value at a path that a reference document decides": {
			before: decided,
			fn:     unshared,
			want: stringtest.LinesLF(
				"x-defaults:",
				"  base: &d {retries: 1, image: alpine}",
				"  <<: *r",
				"jobs:",
				"  a: {<<: *d, script: build}",
				"  b: {<<: *d, script: test}",
				"  c: {retries: 5, image: busybox}",
			),
		},
		"move to the parent of an own path that a reference document decides": {
			before: decided,
			fn:     once(paths.Current().Child("image"), toImg),
			err: `the edit moves "image" to $.x-defaults.base.img, which goes through <<: *r at 3:3, ` +
				"a merge key that reads the reference document ref.yaml",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before, niceyaml.WithReferences(ref))

			err := d.Apply(t.Context(), paths.MustParse("$.jobs.*"), tc.fn)

			requireEdit(t, d, err, tc.err, tc.want)

			if tc.err != "" {
				require.ErrorIs(t, err, paths.ErrReferenceAlias)
				assert.Equal(t, tc.before, d.Text())
			}
		})
	}
}

func TestNode_Scalar(t *testing.T) {
	t.Parallel()

	refs := niceyaml.NewSourceFromString(stringtest.LinesLF(
		"word: &word hello",
		`quoted: &quoted "a b"`,
		"base: &base {x: 1}",
		"block: &block >-",
		"  r",
		"  s",
	))

	tcs := map[string]struct {
		err error
		// The path of the place that the reads take, or $.a when unset.
		at     paths.Path
		before string
		want   string
		kind   niceyaml.NodeKind
		ok     bool
	}{
		"merge key": {
			// A path never selects a merge key, so it reads no value there.
			before: stringtest.LinesLF("b: &b {x: 1}", "a:", "  <<: *b"),
			at:     paths.MustParse("$.a.<<"),
			kind:   niceyaml.NodeNone,
		},
		"scalar": {
			before: "a: hello\n",
			want:   "hello",
			kind:   niceyaml.NodeScalar,
			ok:     true,
		},
		"hexadecimal integer": {
			// The text keeps the spelling that a decode into a string drops.
			before: "a: 0x10\n",
			want:   "0x10",
			kind:   niceyaml.NodeScalar,
			ok:     true,
		},
		"empty string": {
			before: "a: \"\"\n",
			kind:   niceyaml.NodeScalar,
			ok:     true,
		},
		"entry without a value": {
			// The decode reads a null, but the document does not write one.
			before: "a:\n",
			kind:   niceyaml.NodeScalar,
		},
		"written null": {
			before: "a: ~\n",
			want:   "~",
			kind:   niceyaml.NodeScalar,
			ok:     true,
		},
		"alias of a scalar of the document": {
			before: stringtest.LinesLF("w: &w world", "a: *w"),
			want:   "world",
			kind:   niceyaml.NodeScalar,
			ok:     true,
		},
		"alias of a mapping of the document": {
			before: stringtest.LinesLF("m: &m {y: 2}", "a: *m"),
			kind:   niceyaml.NodeMapping,
		},
		"alias of a scalar of a reference document": {
			before: "a: *word\n",
			want:   "hello",
			kind:   niceyaml.NodeScalar,
			ok:     true,
		},
		"alias of a quoted scalar of a reference document": {
			before: "a: *quoted\n",
			want:   "a b",
			kind:   niceyaml.NodeScalar,
			ok:     true,
		},
		"alias of a mapping of a reference document": {
			before: "a: *base\n",
			kind:   niceyaml.NodeMapping,
		},
		"literal block scalar": {
			// The text is the string that a decode reads, not the header.
			before: stringtest.LinesLF("a: |", "  y", "  z"),
			want:   "y\nz\n",
			kind:   niceyaml.NodeScalar,
			ok:     true,
		},
		"literal block scalar with strip chomping": {
			before: stringtest.LinesLF("a: |-", "  y", "  z"),
			want:   "y\nz",
			kind:   niceyaml.NodeScalar,
			ok:     true,
		},
		"literal block scalar with keep chomping": {
			before: stringtest.LinesLF("a: |+", "  y", "", "b: 1"),
			want:   "y\n\n",
			kind:   niceyaml.NodeScalar,
			ok:     true,
		},
		"folded block scalar": {
			before: stringtest.LinesLF("a: >", "  y", "  z"),
			want:   "y z\n",
			kind:   niceyaml.NodeScalar,
			ok:     true,
		},
		"folded block scalar with strip chomping": {
			before: stringtest.LinesLF("a: >-", "  y", "  z"),
			want:   "y z",
			kind:   niceyaml.NodeScalar,
			ok:     true,
		},
		"block scalar with an indentation indicator": {
			before: stringtest.LinesLF("a: |2", "   y", "  z"),
			want:   " y\nz\n",
			kind:   niceyaml.NodeScalar,
			ok:     true,
		},
		"anchored block scalar": {
			before: stringtest.LinesLF("a: &x |", "  y"),
			want:   "y\n",
			kind:   niceyaml.NodeScalar,
			ok:     true,
		},
		"tagged block scalar": {
			before: stringtest.LinesLF("a: !!str |-", "  y"),
			want:   "y",
			kind:   niceyaml.NodeScalar,
			ok:     true,
		},
		"alias of a block scalar of the document": {
			before: stringtest.LinesLF("b: &b |", "  y", "  z", "a: *b"),
			want:   "y\nz\n",
			kind:   niceyaml.NodeScalar,
			ok:     true,
		},
		"alias of a block scalar of a reference document": {
			before: "a: *block\n",
			want:   "r s",
			kind:   niceyaml.NodeScalar,
			ok:     true,
		},
		"alias that does not resolve": {
			// The Func reads the place, and the decode of the document
			// after it fails the Apply.
			before: "a: *nope\n",
			kind:   niceyaml.NodeNone,
			err:    niceyaml.ErrDecode,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.before, niceyaml.WithName("test.yaml"), niceyaml.WithReferences(refs))

			d, err := edit.New(doc)
			require.NoError(t, err)

			at := tc.at
			if at.Len() == 0 {
				at = paths.Doc().Child("a")
			}

			err = d.Apply(t.Context(), paths.Doc(), func(_ context.Context, n *edit.Node) error {
				a := n.At(at)

				text, ok := a.Scalar()
				assert.Equal(t, tc.want, text)
				assert.Equal(t, tc.ok, ok)
				assert.Equal(t, tc.kind, a.Kind())

				return nil
			})
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				return
			}

			require.NoError(t, err)
		})
	}
}

func TestNode_Decode_Ignored(t *testing.T) {
	t.Parallel()

	// Each case decodes the node at read, ignores the error of the
	// decode, and then sets $.b. The error of a read belongs to the
	// Func, so the Apply passes.
	tcs := map[string]struct {
		before string
		read   string
		want   string
	}{
		"place that holds no value": {
			before: "a: 1\n",
			read:   "$.nope",
			want:   "a: 1\nb: 2\n",
		},
		"value that does not decode as an int": {
			before: "a: x\n",
			read:   "$.a",
			want:   "a: x\nb: 2\n",
		},
		"path through an item that the text lacks": {
			before: "a: [1]\n",
			read:   "$.a[3].c",
			want:   "a: [1]\nb: 2\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before)

			err := d.Apply(t.Context(), paths.Doc(), func(ctx context.Context, root *edit.Node) error {
				_, err := root.At(paths.MustParse(tc.read)).Decode[int](ctx)
				require.Error(t, err)

				return root.At(paths.Doc().Child("b")).Set(ctx, 2)
			}, label)

			requireEdit(t, d, err, "", tc.want)
		})
	}
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
		"path through the key of a merge key": {
			// No path selects a merge entry, so the problem binds at the deepest
			// value that the path holds, the mapping of the merge key.
			before: stringtest.LinesLF("s: &s {position: 1}", "spec:", "  <<: *s", "  b: 2"),
			path:   spec.Child("<<").Child("position"),
			err:    "test.yaml:2:1: $.spec: cafe.jacobcolvin.com/v1 cannot decode $.spec.<<.position, which holds no value",
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

	t.Run("alias without an anchor", func(t *testing.T) {
		t.Parallel()

		// No decode reads the alias, so the place holds no value, and the
		// problems of Exists and of the Decode bind at the alias.
		d := newDoc(t, stringtest.LinesLF("a: 1", "b: *q"))

		err := applyAt(t, d, paths.Doc().Child("b"), func(ctx context.Context, n *edit.Node) error {
			ok, err := n.Exists()
			assert.False(t, ok)
			assert.Contains(t, niceyaml.FormatError(err),
				"test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 reads $.b, which goes through *q, "+
					"an alias that does not resolve")
			assert.Equal(t, niceyaml.NodeNone, n.Kind())

			_, err = n.Decode[any](ctx)

			return err
		})
		require.Error(t, err)
		assert.Contains(t, niceyaml.FormatError(err),
			"test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 cannot decode $.b, which goes through *q, "+
				"an alias that does not resolve")
	})

	t.Run("node that an earlier edit took away or displaced", func(t *testing.T) {
		t.Parallel()

		// Each case holds the node at held, runs edit on the root, and then
		// decodes the held node. The problem names the read.
		tcs := map[string]struct {
			edit   edit.Func
			before string
			held   string
			err    string
		}{
			"removed node": {
				before: stringtest.LinesLF("a: 1", "b: 2"),
				held:   "$.a",
				edit: func(_ context.Context, root *edit.Node) error {
					return root.At(paths.Doc().Child("a")).Remove()
				},
				err: "test.yaml:1:4: $.a: cafe.jacobcolvin.com/v1 reads $.a through a node that lost its place to an earlier edit",
			},
			"node on an entry that a rename hides": {
				// The held node reads its own entry, though $.m.a reads the
				// entry that the merge key brings in. The check of the merges
				// then finds the rename.
				before: stringtest.LinesLF("x: &x {a: 1}", "m:", "  b: 2", "  <<: *x"),
				held:   "$.m.b",
				edit: func(_ context.Context, root *edit.Node) error {
					return root.At(paths.MustParse("$.m.b")).SetKey("a")
				},
				err: `test.yaml:3:3: $.m.b: cafe.jacobcolvin.com/v1 renames $.m.b to "a", but <<: *x at 4:3 also brings in a, so $.m then hides the a of $.x`,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				d := newDoc(t, tc.before)

				err := d.Apply(t.Context(), paths.Doc(), func(ctx context.Context, root *edit.Node) error {
					held := root.At(paths.MustParse(tc.held))

					err := tc.edit(ctx, root)
					if err != nil {
						return err
					}

					_, err = held.Decode[any](ctx)

					return err
				}, label)

				requireEdit(t, d, err, tc.err, "")
			})
		}
	})

	t.Run("value whose alias reads an anchor that reads a reference document", func(t *testing.T) {
		t.Parallel()

		// The decode of $.b reads the anchor k4 inside k0, and k0 holds an
		// alias of the reference document.
		ref := niceyaml.NewSourceFromString("s2: &r2 a b\n")
		before := stringtest.LinesLF("c: &k0", "  a: *r2", "  d: &k4 1", "b: [*k4]")

		doc := yamltest.FirstDocument(t, before, niceyaml.WithName("test.yaml"), niceyaml.WithReferences(ref))

		d, err := edit.New(doc)
		require.NoError(t, err)

		var got any

		err = applyAt(t, d, paths.Doc().Child("b"), func(ctx context.Context, n *edit.Node) error {
			got, err = n.Decode[any](ctx)

			return err
		})
		require.NoError(t, err)
		assert.Equal(t, []any{uint64(1)}, got)
	})
}

func TestNode_Decode_Renamed(t *testing.T) {
	t.Parallel()

	type renamed struct {
		Q struct {
			Price int `yaml:"price"`
		} `yaml:"q"`
	}

	type flag struct {
		Price bool `yaml:"price"`
	}

	q := paths.Doc().Child("q")
	price := schema.MustCompile([]byte(`{"properties":{"q":{"properties":{"price":{"type":"string"}}}}}`))

	// After a rename of spec to q, each error of a decode keeps the path
	// that it names in the current text, whoever raised it, and binds where
	// the user wrote the price (fix21 B1).
	tcs := map[string]struct {
		decode func(ctx context.Context, n *edit.Node) error
		want   string
	}{
		"error of a value check": {
			decode: func(ctx context.Context, n *edit.Node) error {
				_, err := n.Decode[renamed](ctx, niceyaml.WithValueCheck(func(_ context.Context, _ *renamed) error {
					return niceyaml.NewError("too low", niceyaml.AtPath(q.Child("price")))
				}))

				return err
			},
			want: "test.yaml:3:10: $.q.price: too low",
		},
		"error of a schema": {
			decode: func(ctx context.Context, n *edit.Node) error {
				_, err := n.Decode[any](ctx, niceyaml.WithValidator(price))

				return err
			},
			want: `test.yaml:3:10: $.q.price: expected "string", got "integer"`,
		},
		"error of the decoder": {
			decode: func(ctx context.Context, n *edit.Node) error {
				_, err := n.At(q).Decode[flag](ctx)

				return err
			},
			want: "test.yaml:3:10: $.q.price:",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, stringtest.LinesLF("o: 0", "spec:", "  price: 5"))

			err := d.Apply(t.Context(), paths.Doc(), func(ctx context.Context, root *edit.Node) error {
				err := root.At(spec).SetKey("q")
				if err != nil {
					return err
				}

				return tc.decode(ctx, root)
			}, label)

			requireEdit(t, d, err, tc.want, "")
		})
	}
}

func TestNode_DecodeInto(t *testing.T) {
	t.Parallel()

	type alarm struct {
		Sound  string `yaml:"sound"`
		Volume int    `yaml:"volume"`
	}

	// A reader written against DecodeInto takes the nodes of each package.
	type decoder interface {
		DecodeInto(ctx context.Context, v any, opts ...niceyaml.DecodeOption) error
	}

	var (
		_ decoder = (*niceyaml.Node)(nil)
		_ decoder = (*niceyaml.Source)(nil)
		_ decoder = (*edit.Node)(nil)
	)

	t.Run("value of a type known at run time", func(t *testing.T) {
		t.Parallel()

		typ := reflect.TypeFor[alarm]()
		d := newDoc(t, stringtest.LinesLF("spec:", "  sound: bell", "  volume: 2"))

		err := applyAt(t, d, spec, func(ctx context.Context, n *edit.Node) error {
			v := reflect.New(typ)

			err := n.DecodeInto(ctx, v.Interface())
			if err != nil {
				return err
			}

			v.Elem().FieldByName("Volume").SetInt(11)

			return n.Set(ctx, v.Interface())
		})

		requireEdit(t, d, err, "", stringtest.LinesLF("spec:", "  sound: bell", "  volume: 11"))
	})

	t.Run("field that the place leaves out", func(t *testing.T) {
		t.Parallel()

		d := newDoc(t, stringtest.LinesLF("spec:", "  sound: bell"))

		got := alarm{Sound: "horn", Volume: 3}

		err := applyAt(t, d, spec, func(ctx context.Context, n *edit.Node) error {
			return n.DecodeInto(ctx, &got)
		})
		require.NoError(t, err)
		assert.Equal(t, alarm{Sound: "bell", Volume: 3}, got)
	})

	t.Run("defaults of the type rules", func(t *testing.T) {
		t.Parallel()

		rules := []niceyaml.TypeRule{niceyaml.TypeDefaults(func(a *alarm) { a.Volume = 7 })}
		_, d := newSourceDocWith(t, stringtest.LinesLF("spec:", "  sound: bell"),
			[]edit.Option{edit.WithTypeRules(rules...)})

		type outer struct {
			Spec *alarm `yaml:"spec"`
		}

		typ := reflect.TypeFor[alarm]()

		// DefaultsInto fills filled as Decode starts its value, but zero
		// keeps the zero value.
		zero, filled := reflect.New(typ).Interface(), reflect.New(typ).Interface()
		require.NoError(t, niceyaml.DefaultsInto(filled, rules...))

		var (
			decoded alarm
			nested  outer
		)

		err := d.Apply(t.Context(), paths.Doc(), func(ctx context.Context, root *edit.Node) error {
			n := root.At(spec)

			var err error

			decoded, err = n.Decode[alarm](ctx)
			if err != nil {
				return err
			}

			err = n.DecodeInto(ctx, zero)
			if err != nil {
				return err
			}

			err = n.DecodeInto(ctx, filled)
			if err != nil {
				return err
			}

			// The value of a nil pointer inside v starts from the defaults.
			return root.DecodeInto(ctx, &nested)
		}, label)
		require.NoError(t, err)

		assert.Equal(t, alarm{Sound: "bell", Volume: 7}, decoded)
		assert.Equal(t, &alarm{Sound: "bell"}, zero)
		assert.Equal(t, &decoded, filled)
		assert.Equal(t, &alarm{Sound: "bell", Volume: 7}, nested.Spec)
	})

	t.Run("type rules of each source", func(t *testing.T) {
		t.Parallel()

		// The function of each later list replaces the one before it.
		own := []niceyaml.TypeRule{niceyaml.TypeDefaults(func(a *alarm) { a.Volume = 7 })}
		src := []niceyaml.TypeRule{niceyaml.TypeDefaults(func(a *alarm) { a.Volume = 8 })}
		over := []niceyaml.TypeRule{niceyaml.TypeDefaults(func(a *alarm) { a.Volume = 9 })}
		opt := niceyaml.OverrideTypeRules(over...)

		source, d := newSourceDocWith(t, stringtest.LinesLF("spec:", "  sound: bell"),
			[]edit.Option{edit.WithTypeRules(own...)}, niceyaml.WithTypeRules(src...))

		// The Func fills v with each list in the order that the decode
		// applies them.
		got := reflect.New(reflect.TypeFor[alarm]()).Interface()
		require.NoError(t, niceyaml.DefaultsInto(got, slices.Concat(own, source.TypeRules(), over)...))

		var want alarm

		err := applyAt(t, d, spec, func(ctx context.Context, n *edit.Node) error {
			var err error

			want, err = n.Decode[alarm](ctx, opt)
			if err != nil {
				return err
			}

			return n.DecodeInto(ctx, got, opt)
		})
		require.NoError(t, err)

		assert.Equal(t, alarm{Sound: "bell", Volume: 9}, want)
		assert.Equal(t, &want, got)
	})

	t.Run("root of an empty document", func(t *testing.T) {
		t.Parallel()

		_, d := newSourceDocWith(t, "",
			[]edit.Option{edit.WithTypeRules(niceyaml.TypeDefaults(func(a *alarm) { a.Volume = 7 }))})

		var decoded alarm

		got := alarm{Sound: "horn", Volume: 3}

		err := d.Apply(t.Context(), paths.Doc(), func(ctx context.Context, root *edit.Node) error {
			var err error

			// Decode returns the defaults of the type, where DecodeInto
			// leaves v as it is.
			decoded, err = root.Decode[alarm](ctx)
			if err != nil {
				return err
			}

			return root.DecodeInto(ctx, &got)
		}, label)
		require.NoError(t, err)
		assert.Equal(t, alarm{Volume: 7}, decoded)
		assert.Equal(t, alarm{Sound: "horn", Volume: 3}, got)
	})

	t.Run("target that is not a pointer", func(t *testing.T) {
		t.Parallel()

		d := newDoc(t, stringtest.LinesLF("spec:", "  sound: bell"))

		err := applyAt(t, d, spec, func(ctx context.Context, n *edit.Node) error {
			return n.DecodeInto(ctx, alarm{})
		})
		require.ErrorIs(t, err, niceyaml.ErrDecodeTarget)
	})

	t.Run("node that cannot decode", func(t *testing.T) {
		t.Parallel()

		// Each case holds the node at held, runs edit on the root, and then
		// decodes into a value through the held node.
		tcs := map[string]struct {
			edit   edit.Func
			is     error
			before string
			held   string
			err    string
		}{
			"place that holds no value": {
				before: stringtest.LinesLF("spec:", "  sound: bell"),
				held:   "$.spec.alarm",
				is:     paths.ErrNotFound,
				err:    "cafe.jacobcolvin.com/v1 cannot decode $.spec.alarm, which holds no value",
			},
			"value that does not decode": {
				before: stringtest.LinesLF("spec:", "  volume: loud"),
				held:   "$.spec",
				err:    "test.yaml:2:11: $.spec.volume:",
			},
			"removed node": {
				before: stringtest.LinesLF("a: 1", "b: 2"),
				held:   "$.a",
				edit: func(_ context.Context, root *edit.Node) error {
					return root.At(paths.Doc().Child("a")).Remove()
				},
				is:  edit.ErrDisplaced,
				err: "test.yaml:1:4: $.a: cafe.jacobcolvin.com/v1 reads $.a through a node that lost its place to an earlier edit",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				d := newDoc(t, tc.before)

				err := d.Apply(t.Context(), paths.Doc(), func(ctx context.Context, root *edit.Node) error {
					held := root.At(paths.MustParse(tc.held))

					if tc.edit != nil {
						err := tc.edit(ctx, root)
						if err != nil {
							return err
						}
					}

					var v alarm

					return held.DecodeInto(ctx, &v)
				}, label)

				if tc.is != nil {
					require.ErrorIs(t, err, tc.is)
				}

				requireEdit(t, d, err, tc.err, "")
			})
		}
	})
}

func TestNode_MarshalYAML(t *testing.T) {
	t.Parallel()

	// Each case sets the place at to with the node at from as the value.
	tcs := map[string]struct {
		before string
		from   string
		to     string
		want   string
		err    string
	}{
		"mapping keeps the order of its keys": {
			before: stringtest.LinesLF("src: {k: 1, j: 2}", "dst: old"),
			from:   "$.src",
			to:     "$.dst",
			want:   stringtest.LinesLF("src: {k: 1, j: 2}", "dst:", "  k: 1", "  j: 2"),
		},
		"block mapping into a new entry": {
			before: stringtest.LinesLF("src:", "  k: 1 # one", "  j: [a, b]"),
			from:   "$.src",
			to:     "$.dst",
			want: stringtest.LinesLF(
				"src:", "  k: 1 # one", "  j: [a, b]",
				"dst:", "  k: 1", "  j:", "    - a", "    - b",
			),
		},
		"scalar": {
			before: stringtest.LinesLF("src: 'x' # note", "dst: old"),
			from:   "$.src",
			to:     "$.dst",
			want:   stringtest.LinesLF("src: 'x' # note", "dst: x"),
		},
		"alias and merge key write their content": {
			before: stringtest.LinesLF("base: &base {x: 1}", "src: {k: *base, <<: *base}", "dst: old"),
			from:   "$.src",
			to:     "$.dst",
			want: stringtest.LinesLF(
				"base: &base {x: 1}", "src: {k: *base, <<: *base}", "dst:", "  k:", "    x: 1", "  x: 1",
			),
		},
		"key after the merge key that holds it": {
			before: stringtest.LinesLF("base: &base {x: 1, w: 2}", "src: {<<: *base, x: 9}", "dst: old"),
			from:   "$.src",
			to:     "$.dst",
			want: stringtest.LinesLF(
				"base: &base {x: 1, w: 2}", "src: {<<: *base, x: 9}", "dst:", "  x: 9", "  w: 2",
			),
		},
		"block mapping with a key after its merge key": {
			before: stringtest.LinesLF("base: &base", "  x: 1", "  w: 2", "src:", "  <<: *base", "  x: 9"),
			from:   "$.src",
			to:     "$.dst",
			want: stringtest.LinesLF(
				"base: &base", "  x: 1", "  w: 2", "src:", "  <<: *base", "  x: 9",
				"dst:", "  x: 9", "  w: 2",
			),
		},
		"binary scalar keeps its tag": {
			before: stringtest.LinesLF("src: {a: !!binary aGk=}", "dst: old"),
			from:   "$.src",
			to:     "$.dst",
			want:   stringtest.LinesLF("src: {a: !!binary aGk=}", "dst:", `  a: !!binary "aGk="`),
		},
		"timestamp scalar keeps its tag": {
			before: stringtest.LinesLF("src: !!timestamp 2001-12-14", "dst: old"),
			from:   "$.src",
			to:     "$.dst",
			want:   stringtest.LinesLF("src: !!timestamp 2001-12-14", `dst: !!timestamp "2001-12-14T00:00:00Z"`),
		},
		"block mapping with a timestamp entry over a scalar": {
			before: stringtest.LinesLF("meta:", "  created: !!timestamp 2001-12-14", "  name: x", "backup: old"),
			from:   "$.meta",
			to:     "$.backup",
			want: stringtest.LinesLF(
				"meta:", "  created: !!timestamp 2001-12-14", "  name: x",
				"backup:", `  created: !!timestamp "2001-12-14T00:00:00Z"`, "  name: x",
			),
		},
		"flow mapping with a timestamp entry": {
			before: stringtest.LinesLF("src: {a: !!timestamp 2001-12-14}", "dst: old"),
			from:   "$.src",
			to:     "$.dst",
			want: stringtest.LinesLF(
				"src: {a: !!timestamp 2001-12-14}", "dst:", `  a: !!timestamp "2001-12-14T00:00:00Z"`,
			),
		},
		"timestamp scalar over an entry of a flow mapping": {
			before: stringtest.LinesLF("src: !!timestamp 2001-12-14t21:59:43.5-05:00", "dst: {k: old}"),
			from:   "$.src",
			to:     "$.dst.k",
			want: stringtest.LinesLF(
				"src: !!timestamp 2001-12-14t21:59:43.5-05:00",
				`dst: {k: !!timestamp "2001-12-14T21:59:43.5-05:00"}`,
			),
		},
		"timestamp scalar into a new entry of a flow mapping": {
			before: stringtest.LinesLF("src: !!timestamp 2001-12-14", "dst: {k: old}"),
			from:   "$.src",
			to:     "$.dst.new",
			want: stringtest.LinesLF(
				"src: !!timestamp 2001-12-14", `dst: {k: old, new: !!timestamp "2001-12-14T00:00:00Z"}`,
			),
		},
		"timestamp scalar over an item of a flow sequence": {
			before: stringtest.LinesLF("src: !!timestamp 2001-12-14", "dst: [old]"),
			from:   "$.src",
			to:     "$.dst[0]",
			want: stringtest.LinesLF(
				"src: !!timestamp 2001-12-14", `dst: [!!timestamp "2001-12-14T00:00:00Z"]`,
			),
		},
		"value into a place inside it": {
			before: "src: {a: 1}\n",
			from:   "$.src",
			to:     "$.src.b",
			want:   "src: {a: 1, b: {a: 1}}\n",
		},
		"place that holds no value": {
			before: stringtest.LinesLF("src: 1", "dst: old"),
			from:   "$.missing",
			to:     "$.dst",
			err: "test.yaml:2:6: $.dst: cafe.jacobcolvin.com/v1 cannot write the value of $.dst: " +
				"value does not encode: ",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before)

			err := d.Apply(t.Context(), paths.Doc(), func(ctx context.Context, root *edit.Node) error {
				return root.At(paths.MustParse(tc.to)).Set(ctx, root.At(paths.MustParse(tc.from)))
			}, label)

			requireEdit(t, d, err, tc.err, tc.want)

			if tc.err != "" {
				assert.Contains(t, niceyaml.FormatError(err), "cannot decode "+tc.from+", which holds no value")
				assert.Equal(t, tc.before, d.Text())
				// The node reads the document, so the document is at fault, as
				// for the error of the decode that it holds.
				require.NotErrorIs(t, err, edit.ErrValue)
				assert.True(t, niceyaml.IsInvalid(err))
			}
		})
	}

	t.Run("value that an earlier call of the Func wrote", func(t *testing.T) {
		t.Parallel()

		d := newDoc(t, stringtest.LinesLF("src: 1", "dst: old"))

		err := d.Apply(t.Context(), paths.Doc(), func(ctx context.Context, root *edit.Node) error {
			from := root.At(paths.Doc().Child("src"))

			err := from.Set(ctx, []string{"a", "b"})
			if err != nil {
				return err
			}

			return root.At(paths.Doc().Child("dst")).Set(ctx, from)
		}, label)

		requireEdit(t, d, err, "", stringtest.LinesLF("src:", "  - a", "  - b", "dst:", "  - a", "  - b"))
	})

	t.Run("node that lost its place to an earlier edit", func(t *testing.T) {
		t.Parallel()

		before := stringtest.LinesLF("src: 1", "dst: old")
		d := newDoc(t, before)

		err := d.Apply(t.Context(), paths.Doc(), func(ctx context.Context, root *edit.Node) error {
			from := root.At(paths.Doc().Child("src"))

			err := from.Remove()
			if err != nil {
				return err
			}

			return root.At(paths.Doc().Child("dst")).Set(ctx, from)
		}, label)

		requireEdit(t, d, err, "reads $.src through a node that lost its place to an earlier edit", "")
		assert.Equal(t, before, d.Text())
	})

	t.Run("float that the encoder writes without a fraction", func(t *testing.T) {
		t.Parallel()

		before := stringtest.LinesLF("src: {lr: 0.00001}", "dst: old")
		d := newDoc(t, before)

		err := d.Apply(t.Context(), paths.Doc(), func(ctx context.Context, root *edit.Node) error {
			return root.At(paths.Doc().Child("dst")).Set(ctx, root.At(paths.Doc().Child("src")))
		}, label)

		requireEdit(t, d, err,
			`test.yaml:2:6: $.dst: go-yaml's encoder writes the float 1e-05 as text that `+
				`go-yaml's decoder reads as "1e-05"`, "")
		require.ErrorIs(t, err, edit.ErrEngine)
		assert.Equal(t, before, d.Text())
	})

	t.Run("item that a Func inserts", func(t *testing.T) {
		t.Parallel()

		d := newDoc(t, stringtest.LinesLF("src: {k: 1}", "items:", "  - a"))

		err := d.Apply(t.Context(), paths.Doc(), func(ctx context.Context, root *edit.Node) error {
			return root.At(paths.Doc().Child("items").Index(0)).Insert(ctx, root.At(paths.Doc().Child("src")))
		}, label)

		requireEdit(t, d, err, "", stringtest.LinesLF("src: {k: 1}", "items:", "  - k: 1", "  - a"))
	})

	t.Run("value that holds nodes", func(t *testing.T) {
		t.Parallel()

		d := newDoc(t, stringtest.LinesLF("a: 1", "b: [x]", "dst: old"))

		err := d.Apply(t.Context(), paths.Doc(), func(ctx context.Context, root *edit.Node) error {
			var none *edit.Node

			return root.At(paths.Doc().Child("dst")).Set(ctx, []any{
				root.At(paths.Doc().Child("a")),
				root.At(paths.Doc().Child("b")),
				none,
			})
		}, label)

		requireEdit(t, d, err, "", stringtest.LinesLF("a: 1", "b: [x]", "dst:", "  - 1", "  - - x", "  - null"))
	})

	t.Run("encode outside a Set", func(t *testing.T) {
		t.Parallel()

		d := newDoc(t, stringtest.LinesLF("src: {k: 1, j: 2}"))

		var got []byte

		err := applyAt(t, d, paths.Doc().Child("src"), func(ctx context.Context, n *edit.Node) error {
			var err error

			got, err = encoder.Marshal(ctx, n)

			return err //nolint:wrapcheck // The test reads the error of the encode.
		})
		require.NoError(t, err)
		assert.Equal(t, stringtest.LinesLF("k: 1", "j: 2"), string(got))
	})
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
			// A read returns its answer to the Func and records nothing on the
			// Apply.
			before: stringtest.LinesLF("spec:", "  tags: [a, b]"),
			fn: func(_ context.Context, n *edit.Node) error {
				_, err := n.At(paths.MustParse("$.spec.tags[*]")).Exists()
				if !errors.Is(err, edit.ErrUsage) {
					return errors.New("the read takes the path")
				}

				return nil
			},
			want: stringtest.LinesLF("spec:", "  tags: [a, b]"),
		},
		"edit through a path that selects more than one value": {
			before: stringtest.LinesLF("spec:", "  tags: [a, b]"),
			fn: func(_ context.Context, n *edit.Node) error {
				return n.At(paths.MustParse("$.spec.tags[*]")).Remove()
			},
			err: "cafe.jacobcolvin.com/v1 removes $.spec.tags[*], which selects more than one value",
		},
		"edit through a path that selects more than one value, whose error the Func ignores": {
			before: stringtest.LinesLF("spec:", "  tags: [a, b]"),
			fn: func(ctx context.Context, n *edit.Node) error {
				_ = n.At(paths.MustParse("$.spec.tags[*]")).Remove() //nolint:errcheck // The test ignores the error.

				return n.At(spec.Child("tags").Index(0)).Set(ctx, "c")
			},
			want: stringtest.LinesLF("spec:", "  tags: [c, b]"),
		},
		"decode through a path that selects a key, whose error the Func ignores": {
			before: stringtest.LinesLF("spec:", "  tags: [a, b]"),
			fn: func(ctx context.Context, n *edit.Node) error {
				_, err := n.At(spec.Child("tags").Key()).Decode[any](ctx)
				if err == nil {
					return errors.New("the decode passes")
				}

				return n.At(spec.Child("tags").Index(0)).Set(ctx, "c")
			},
			want: stringtest.LinesLF("spec:", "  tags: [c, b]"),
		},
		"path that selects a key": {
			before: stringtest.LinesLF("spec:", "  tags: [a, b]"),
			fn: func(_ context.Context, n *edit.Node) error {
				return n.At(spec.Child("tags").Key()).Remove()
			},
			err: "cafe.jacobcolvin.com/v1 removes $.spec.tags~, which selects a key",
		},
		"path through an alias that does not resolve": {
			before: stringtest.LinesLF("spec: *nope", "other: 1"),
			fn: func(ctx context.Context, n *edit.Node) error {
				return n.At(spec.Child("price")).Set(ctx, 5)
			},
			err: "cafe.jacobcolvin.com/v1 sets $.spec.price, which goes through *nope, an alias that does not resolve",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before)

			requireEdit(t, d, d.Apply(t.Context(), paths.Doc(), tc.fn, label), tc.err, tc.want)
		})
	}

	t.Run("path through an alias of a reference document", func(t *testing.T) {
		t.Parallel()

		ref := niceyaml.NewSourceFromString(stringtest.LinesLF("base: &b", "  price: 4"))

		reads := map[string]struct {
			fn  edit.Func
			err string
		}{
			"Set below the alias": {
				fn: func(ctx context.Context, n *edit.Node) error {
					return n.At(spec.Child("price")).Set(ctx, 5)
				},
				err: "cafe.jacobcolvin.com/v1 sets $.spec.price, which goes through *b, an alias of a reference document",
			},
			"Move below the alias": {
				fn: func(_ context.Context, n *edit.Node) error {
					return n.At(paths.Doc().Child("other")).Move(spec.Child("other"))
				},
				err: `cafe.jacobcolvin.com/v1 moves "other" to $.spec.other, ` +
					"which goes through *b, an alias of a reference document",
			},
			"Set where a Move put the alias above the place": {
				fn: func(ctx context.Context, n *edit.Node) error {
					price := n.At(paths.MustParse("$.moved.price"))

					err := n.At(spec).Move(paths.Doc().Child("moved"))
					if err != nil {
						return err
					}

					return price.Set(ctx, 5)
				},
				err: "cafe.jacobcolvin.com/v1 sets $.moved.price, which goes through *b, an alias of a reference document",
			},
		}

		for name, tc := range reads {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, stringtest.LinesLF("spec: *b", "other: 1"),
					niceyaml.WithName("test.yaml"), niceyaml.WithReferences(ref))

				d, err := edit.New(doc, edit.WithApp("cafe"))
				require.NoError(t, err)

				err = d.Apply(t.Context(), paths.Doc(), tc.fn, label)
				require.Error(t, err)
				assert.Contains(t, niceyaml.FormatError(err), tc.err)
				assert.NotContains(t, niceyaml.FormatError(err), "does not resolve")
				assert.NotContains(t, niceyaml.FormatError(err), "alias reads a reference document",
					"the message names the cause once")
			})
		}
	})

	t.Run("wildcard over a merge key of a reference document", func(t *testing.T) {
		t.Parallel()

		ref := niceyaml.NewSourceFromString("base: &b {other: 4}\n")

		// Each case runs an Apply at path, whose matches read through the
		// merge key, and sets each value to 5.
		wildcards := map[string]struct {
			before string
			path   paths.Path
			err    string
		}{
			"each entry of the root": {
				before: stringtest.LinesLF("a: 1", "<<: *b", "c: 2"),
				path:   paths.Doc().ChildAll(),
				err: "test.yaml:2:5: $: cafe.jacobcolvin.com/v1 reads $.*, " +
					"which goes through <<: *b at 2:1, a merge key that reads a reference document, so that document decides which names $ reads",
			},
			"each entry of a nested mapping": {
				before: stringtest.LinesLF("spec:", "  cost: 3", "  <<: *b"),
				path:   spec.ChildAll(),
				err: "test.yaml:3:7: $.spec: cafe.jacobcolvin.com/v1 reads $.spec.*, " +
					"which goes through <<: *b at 3:3, a merge key that reads a reference document, so that document decides which names $.spec reads",
			},
			"each entry of a mapping that merges a reference document and that the root merges": {
				// The problem shows the path of the value that holds the alias (fix19 B6).
				// The lookup of k in the root reads the merge key of k through
				// the merge key of the root, and the problem binds at the inner
				// merge key.
				before: stringtest.LinesLF("k: &k0", "  <<: *b", "  n: 1", "<<: *k0"),
				path:   paths.Doc().Child("k").ChildAll(),
				err: "test.yaml:2:7: $.k: cafe.jacobcolvin.com/v1 reads $.k.*, " +
					"which goes through <<: *b at 2:3, a merge key that reads a reference document, so that document decides which names $.k reads",
			},
			"each entry of a mapping that merges a mapping that merges a reference document": {
				before: stringtest.LinesLF("k: &k0", "  <<: *b", "  n: 1", "j:", "  <<: *k0"),
				path:   paths.Doc().Child("j").ChildAll(),
				err: "test.yaml:2:7: $.k: cafe.jacobcolvin.com/v1 reads $.j.*, " +
					"which goes through <<: *b at 2:3, a merge key that reads a reference document, so that document decides which names $.k reads",
			},
			"each entry of a merge key with a list": {
				before: stringtest.LinesLF("x: &x {a: 1}", "spec:", "  <<: [*x, *b]"),
				path:   spec.ChildAll(),
				err: "test.yaml:3:12: $.spec: cafe.jacobcolvin.com/v1 reads $.spec.*, " +
					"which goes through <<: [*x, *b] at 3:3, a merge key that reads a reference document, so that document decides which names $.spec reads",
			},
		}

		for name, tc := range wildcards {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, tc.before,
					niceyaml.WithName("test.yaml"), niceyaml.WithReferences(ref))

				d, err := edit.New(doc, edit.WithApp("cafe"))
				require.NoError(t, err)

				err = d.Apply(t.Context(), tc.path, func(ctx context.Context, n *edit.Node) error {
					return n.Set(ctx, 5)
				}, label)
				require.ErrorIs(t, err, paths.ErrReferenceAlias)
				assert.Equal(t, tc.err, err.Error())
			})
		}
	})

	t.Run("path through a merge key of a reference document", func(t *testing.T) {
		t.Parallel()

		// The mapping of the reference document holds new, cost, and z.
		ref := niceyaml.NewSourceFromString("base: &b {other: 4, new: 1, cost: 7, z: 9}\n")

		// Each case sets the value at path to 5, or moves it to move when
		// move is set, with the options opts.
		merges := map[string]struct {
			before string
			ref    string
			path   paths.Path
			move   paths.Path
			opts   []edit.SetOption
			want   string
			err    string
		}{
			"new key first that the reference document writes as another key": {
				// The new entry goes before the merge key, which brings in
				// 0x10. A decode reads 0x10 as 16, so it replaces the new entry.
				before: stringtest.LinesLF("spec:", "  cost: 3", "  <<: *b"),
				ref:    "base: &b {0x10: 9}\n",
				path:   spec.Child("16"),
				opts:   []edit.SetOption{edit.First()},
				err: "test.yaml:3:7: $.spec: cafe.jacobcolvin.com/v1 sets $.spec.16, " +
					"which goes through <<: *b at 3:3, a merge key that reads a reference document, so that document decides which names $.spec reads",
			},
			"new key before an entry that the reference document writes as another key": {
				before: stringtest.LinesLF("spec:", "  cost: 3", "  <<: *b"),
				ref:    "base: &b {0x10: 9}\n",
				path:   spec.Child("16"),
				opts:   []edit.SetOption{edit.Before("cost")},
				err: "test.yaml:3:7: $.spec: cafe.jacobcolvin.com/v1 sets $.spec.16, " +
					"which goes through <<: *b at 3:3, a merge key that reads a reference document, so that document decides which names $.spec reads",
			},
			"new key after an entry before the merge key that the reference document writes as another key": {
				// The new entry goes after cost, so before the merge key, which
				// brings in 0x10.
				before: stringtest.LinesLF("spec:", "  cost: 3", "  <<: *b"),
				ref:    "base: &b {0x10: 9}\n",
				path:   spec.Child("16"),
				opts:   []edit.SetOption{edit.After("cost")},
				err: "test.yaml:3:7: $.spec: cafe.jacobcolvin.com/v1 sets $.spec.16, " +
					"which goes through <<: *b at 3:3, a merge key that reads a reference document, so that document decides which names $.spec reads",
			},
			"new key after an entry after the merge key that the reference document writes as another key": {
				before: stringtest.LinesLF("spec:", "  <<: *b", "  x: 3"),
				ref:    "base: &b {0x10: 9}\n",
				path:   spec.Child("16"),
				opts:   []edit.SetOption{edit.After("x")},
				want:   stringtest.LinesLF("spec:", "  <<: *b", "  x: 3", "  \"16\": 5"),
			},
			"move after an entry before the merge key by a name that the reference document writes as another key": {
				before: stringtest.LinesLF("spec:", "  cost: 3", "  <<: *b", "\"16\": 1"),
				ref:    "base: &b {0x10: 9}\n",
				path:   paths.Doc().Child("16"),
				move:   spec.Child("16"),
				opts:   []edit.SetOption{edit.After("cost")},
				err: "test.yaml:3:7: $.spec: cafe.jacobcolvin.com/v1 moves $.16 to $.spec.16, " +
					"which goes through <<: *b at 3:3, a merge key that reads a reference document, so that document decides which names $.spec reads",
			},
			"reorder of an entry to before the merge key": {
				// A decode reads 0x10 as 16, so 0x10 replaces the entry at its
				// new place. A reorder of an own entry fails whatever the
				// reference document holds.
				before: stringtest.LinesLF("spec:", "  cost: 3", "  <<: *b", "  16: 1"),
				ref:    "base: &b {0x10: 9}\n",
				path:   spec.Child("16"),
				move:   spec.Child("16"),
				opts:   []edit.SetOption{edit.After("cost")},
				err: "test.yaml:4:3: $.spec.16: cafe.jacobcolvin.com/v1 moves $.spec.16 to $.spec.16, " +
					"but <<: *b at 3:3 reads a reference document, which decides which names $.spec reads",
			},
			"new key first with a reference key that does not decode": {
				before: stringtest.LinesLF("spec:", "  cost: 3", "  <<: *b"),
				ref:    "base: &b {? !!bool x : 9}\n",
				path:   spec.Child("16"),
				opts:   []edit.SetOption{edit.First()},
				err: "test.yaml:3:7: $.spec: cafe.jacobcolvin.com/v1 sets $.spec.16, " +
					"which goes through <<: *b at 3:3, a merge key that reads a reference document, so that document decides which names $.spec reads",
			},
			"new key last that the reference document writes as another key": {
				before: stringtest.LinesLF("spec:", "  cost: 3", "  <<: *b"),
				ref:    "base: &b {0x10: 9}\n",
				path:   spec.Child("16"),
				want:   stringtest.LinesLF("spec:", "  cost: 3", "  <<: *b", "  \"16\": 5"),
			},
			"new key first that the reference document lacks": {
				before: stringtest.LinesLF("spec:", "  cost: 3", "  <<: *b"),
				ref:    "base: &b {0x10: 9}\n",
				path:   spec.Child("tls"),
				opts:   []edit.SetOption{edit.First()},
				want:   stringtest.LinesLF("spec:", "  tls: 5", "  cost: 3", "  <<: *b"),
			},
			"new key after the merge key that the reference document writes as another key": {
				before: stringtest.LinesLF("spec:", "  <<: *b", "  cost: 3"),
				ref:    "base: &b {0x10: 9}\n",
				path:   spec.Child("16"),
				opts:   []edit.SetOption{edit.Before("cost")},
				want:   stringtest.LinesLF("spec:", "  <<: *b", "  \"16\": 5", "  cost: 3"),
			},
			"move first into the mapping by a name that the reference document writes as another key": {
				before: stringtest.LinesLF("spec:", "  cost: 3", "  <<: *b", "\"16\": 1"),
				ref:    "base: &b {0x10: 9}\n",
				path:   paths.Doc().Child("16"),
				move:   spec.Child("16"),
				opts:   []edit.SetOption{edit.First()},
				err: "test.yaml:3:7: $.spec: cafe.jacobcolvin.com/v1 moves $.16 to $.spec.16, " +
					"which goes through <<: *b at 3:3, a merge key that reads a reference document, so that document decides which names $.spec reads",
			},
			"key before a merge of a reference anchor that merges another": {
				before: stringtest.LinesLF("spec: {16: 3, <<: *b}"),
				ref:    stringtest.LinesLF("i: &i {0x10: 9}", "base: &b {<<: *i}"),
				path:   spec.Child("16"),
				err: "test.yaml:1:19: $.spec: cafe.jacobcolvin.com/v1 sets $.spec.16, " +
					"which goes through <<: *b at 1:15, a merge key that reads a reference document, so that document decides which names $.spec reads",
			},
			"key before a merge of a reference anchor that merges a sequence": {
				before: stringtest.LinesLF("spec: {16: 3, <<: *b}"),
				ref:    stringtest.LinesLF("i: &i {0x10: 9}", "j: &j {z: 1}", "base: &b {<<: [*i, *j]}"),
				path:   spec.Child("16"),
				err: "test.yaml:1:19: $.spec: cafe.jacobcolvin.com/v1 sets $.spec.16, " +
					"which goes through <<: *b at 1:15, a merge key that reads a reference document, so that document decides which names $.spec reads",
			},
			"key before a merge of a local anchor that merges a reference document": {
				before: stringtest.LinesLF("l: &l {<<: *b}", "spec: {16: 3, <<: *l}"),
				ref:    "base: &b {0x10: 9}\n",
				path:   spec.Child("16"),
				err: "test.yaml:1:12: $.l: cafe.jacobcolvin.com/v1 sets $.spec.16, " +
					"which goes through <<: *b at 1:8, a merge key that reads a reference document, so that document decides which names $.l reads",
			},
			"key that the merge key brings in": {
				before: stringtest.LinesLF("spec:", "  <<: *b", "  cost: 3"),
				path:   spec.Child("new"),
				err: "test.yaml:2:7: $.spec: cafe.jacobcolvin.com/v1 sets $.spec.new, " +
					"which goes through <<: *b at 2:3, a merge key that reads a reference document, so that document decides which names $.spec reads",
			},
			"key that neither the mapping nor the reference document holds": {
				before: stringtest.LinesLF("spec:", "  <<: *b", "  cost: 3"),
				path:   spec.Child("tls"),
				want:   stringtest.LinesLF("spec:", "  <<: *b", "  cost: 3", "  tls: 5"),
			},
			"key before the merge key": {
				// The merge key brings in cost, which then wins over the own
				// entry, so the path selects an entry of the reference document.
				before: stringtest.LinesLF("spec:", "  cost: 3", "  <<: *b"),
				path:   spec.Child("cost"),
				err: "test.yaml:3:7: $.spec: cafe.jacobcolvin.com/v1 sets $.spec.cost, " +
					"which goes through <<: *b at 3:3, a merge key that reads a reference document, so that document decides which names $.spec reads",
			},
			"key before the merge key that the reference document lacks": {
				before: stringtest.LinesLF("spec:", "  seats: 3", "  <<: *b"),
				path:   spec.Child("seats"),
				want:   stringtest.LinesLF("spec:", "  seats: 5", "  <<: *b"),
			},
			"key after the merge key": {
				before: stringtest.LinesLF("spec:", "  <<: *b", "  cost: 3"),
				path:   spec.Child("cost"),
				want:   stringtest.LinesLF("spec:", "  <<: *b", "  cost: 5"),
			},
			"key before the merge key that the reference document writes as another key": {
				// A decode reads 0x10 as 16, so the entry of the reference
				// document wins over the own entry, though the text of its key
				// differs.
				before: stringtest.LinesLF("spec:", "  16: 3", "  <<: *b"),
				ref:    "base: &b {0x10: 9}\n",
				path:   spec.Child("16"),
				err: "test.yaml:3:7: $.spec: cafe.jacobcolvin.com/v1 sets $.spec.16, " +
					"which goes through <<: *b at 3:3, a merge key that reads a reference document, so that document decides which names $.spec reads",
			},
			"key before the merge key with a reference key that does not decode": {
				// A decode cannot read the key !!bool x, so it might read it as
				// 16.
				before: stringtest.LinesLF("spec:", "  16: 3", "  <<: *b"),
				ref:    "base: &b {? !!bool x : 9}\n",
				path:   spec.Child("16"),
				err: "test.yaml:3:7: $.spec: cafe.jacobcolvin.com/v1 sets $.spec.16, " +
					"which goes through <<: *b at 3:3, a merge key that reads a reference document, so that document decides which names $.spec reads",
			},
			"key after the merge key that the reference document writes as another key": {
				before: stringtest.LinesLF("spec:", "  <<: *b", "  16: 3"),
				ref:    "base: &b {0x10: 9}\n",
				path:   spec.Child("16"),
				want:   stringtest.LinesLF("spec:", "  <<: *b", "  16: 5"),
			},
			"move to a key that the mapping lacks": {
				before: stringtest.LinesLF("spec:", "  <<: *b", "  cost: 3"),
				path:   spec.Child("cost"),
				move:   spec.Child("new"),
				err: `test.yaml:2:7: $.spec: cafe.jacobcolvin.com/v1 moves "cost" to $.spec.new, ` +
					"which goes through <<: *b at 2:3, a merge key that reads a reference document, so that document decides which names $.spec reads",
			},
			"move into the mapping": {
				before: stringtest.LinesLF("spec:", "  <<: *b", "z: 1"),
				path:   paths.Doc().Child("z"),
				move:   spec.Child("z"),
				err: `test.yaml:2:7: $.spec: cafe.jacobcolvin.com/v1 moves "z" to $.spec.z, ` +
					"which goes through <<: *b at 2:3, a merge key that reads a reference document, so that document decides which names $.spec reads",
			},
			"move into the mapping by a name that the reference document lacks": {
				before: stringtest.LinesLF("spec:", "  <<: *b", "q: 1"),
				path:   paths.Doc().Child("q"),
				move:   spec.Child("q"),
				want:   stringtest.LinesLF("spec:", "  <<: *b", "  q: 1"),
			},
		}

		for name, tc := range merges {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				refs := ref
				if tc.ref != "" {
					refs = niceyaml.NewSourceFromString(tc.ref)
				}

				doc := yamltest.FirstDocument(t, tc.before,
					niceyaml.WithName("test.yaml"), niceyaml.WithReferences(refs))

				d, err := edit.New(doc, edit.WithApp("cafe"))
				require.NoError(t, err)

				err = applyAt(t, d, tc.path, func(ctx context.Context, n *edit.Node) error {
					if tc.move.IsAbsolute() {
						return n.Move(tc.move, tc.opts...)
					}

					return n.Set(ctx, 5, tc.opts...)
				})
				if tc.err == "" {
					require.NoError(t, err)
					assert.Equal(t, tc.want, d.Text())

					return
				}

				require.ErrorIs(t, err, paths.ErrReferenceAlias)
				assert.Equal(t, tc.err, err.Error())
			})
		}
	})
}

func ExampleNode_Own() {
	src := niceyaml.NewSourceFromString(stringtest.LinesLF(
		"x-defaults: &defaults",
		"  timeout: 30",
		"jobs:",
		"  build: {<<: *defaults}",
		"  test: {<<: *defaults}",
		"  lint: {timeout: 5}",
	), niceyaml.WithName("jobs.yaml"))

	doc, err := src.Document()
	if err != nil {
		fmt.Println(err)

		return
	}

	d, err := edit.New(doc)
	if err != nil {
		fmt.Println(err)

		return
	}

	// The jobs build and test read one timeout entry. The call for test
	// finds the own path of that entry in done and returns, so the entry
	// changes from seconds to milliseconds one time.
	done := map[string]bool{}

	err = d.Apply(context.Background(), paths.MustParse("$.jobs.*"),
		func(ctx context.Context, n *edit.Node) error {
			timeout := n.At(paths.Current().Child("timeout"))

			ok, err := timeout.Exists()
			if err != nil {
				return err
			}

			own := timeout.Own()
			if !ok || done[own.String()] {
				return nil
			}

			fmt.Println(timeout.Path(), "is", own)

			done[own.String()] = true

			seconds, err := timeout.Decode[int](ctx)
			if err != nil {
				return err
			}

			return timeout.Set(ctx, seconds*1000)
		})
	if err != nil {
		fmt.Println(err)

		return
	}

	text, err := edit.FileText(src, d)
	if err != nil {
		fmt.Println(err)

		return
	}

	fmt.Print(text)

	// Output:
	// $.jobs.build.timeout is $.x-defaults.timeout
	// $.jobs.lint.timeout is $.jobs.lint.timeout
	// x-defaults: &defaults
	//   timeout: 30000
	// jobs:
	//   build: {<<: *defaults}
	//   test: {<<: *defaults}
	//   lint: {timeout: 5000}
}

func TestNode_Len(t *testing.T) {
	t.Parallel()

	ref := niceyaml.NewSourceFromString("r: &r [x, y, z]\n", niceyaml.WithName("ref.yaml"))

	tcs := map[string]struct {
		before string
		path   paths.Path
		want   int
	}{
		"block sequence": {
			before: stringtest.LinesLF("s:", "  - a", "  - b", "  - c"),
			path:   paths.Doc().Child("s"),
			want:   3,
		},
		"flow sequence": {
			before: "s: [a, b]\n",
			path:   paths.Doc().Child("s"),
			want:   2,
		},
		"empty flow sequence": {
			before: "s: []\n",
			path:   paths.Doc().Child("s"),
		},
		"alias of a sequence": {
			before: stringtest.LinesLF("s: &s [a, b]", "t: *s"),
			path:   paths.Doc().Child("t"),
			want:   2,
		},
		"alias of a sequence of a reference document": {
			before: "a: *r\n",
			path:   paths.Doc().Child("a"),
			want:   3,
		},
		"sequence under a tag": {
			before: "s: !!seq [a]\n",
			path:   paths.Doc().Child("s"),
			want:   1,
		},
		"mapping": {
			before: "m: {a: 1, b: 2}\n",
			path:   paths.Doc().Child("m"),
		},
		"scalar": {
			before: "a: 1\n",
			path:   paths.Doc().Child("a"),
		},
		"null": {
			before: "s:\n",
			path:   paths.Doc().Child("s"),
		},
		"place without a value": {
			before: "a: 1\n",
			path:   paths.Doc().Child("s"),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before, niceyaml.WithReferences(ref))

			err := applyAt(t, d, tc.path, func(_ context.Context, n *edit.Node) error {
				assert.Equal(t, tc.want, n.Len())

				return nil
			})
			require.NoError(t, err)
		})
	}

	t.Run("after an edit", func(t *testing.T) {
		t.Parallel()

		d := newDoc(t, "s: [a]\n")

		err := applyAt(t, d, paths.Doc().Child("s"), func(ctx context.Context, n *edit.Node) error {
			err := n.Append(ctx, "b")
			if err != nil {
				return err
			}

			assert.Equal(t, 2, n.Len())

			return nil
		})
		require.NoError(t, err)
	})

	t.Run("after its Apply", func(t *testing.T) {
		t.Parallel()

		d := newDoc(t, "s: [a, b]\n")

		var kept *edit.Node

		err := applyAt(t, d, paths.Doc().Child("s"), func(_ context.Context, n *edit.Node) error {
			kept = n
			assert.Equal(t, 2, n.Len())

			return nil
		})
		require.NoError(t, err)

		assert.Zero(t, kept.Len())
		require.ErrorIs(t, existsErr(kept), edit.ErrEnded)
	})
}

func TestNode_Append(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		value any
		// The edit runs after the Func takes the node at path and before the
		// Append through that node.
		edit   edit.Func
		before string
		path   paths.Path
		want   string
		err    string
		// Whether the error matches edit.ErrProtected.
		protected bool
	}{
		"block sequence": {
			before: stringtest.LinesLF("args:", "  - --quiet # first", "  - --color"),
			path:   paths.Doc().Child("args"),
			value:  "--verbose",
			want:   stringtest.LinesLF("args:", "  - --quiet # first", "  - --color", "  - --verbose"),
		},
		"flow sequence": {
			before: "args: [a, b]\n",
			path:   paths.Doc().Child("args"),
			value:  "c",
			want:   "args: [a, b, c]\n",
		},
		"empty flow sequence": {
			// An empty sequence has no style of its own, so the item goes on
			// a block line.
			before: "args: []\n",
			path:   paths.Doc().Child("args"),
			value:  "a",
			want:   stringtest.LinesLF("args:", "  - a"),
		},
		"mapping value": {
			before: stringtest.LinesLF("steps:", "  - run: a"),
			path:   paths.Doc().Child("steps"),
			value:  map[string]any{"run": "b"},
			want:   stringtest.LinesLF("steps:", "  - run: a", "  - run: b"),
		},
		"alias of a sequence": {
			before: stringtest.LinesLF("s: &s [a]", "t: *s"),
			path:   paths.Doc().Child("t"),
			value:  "b",
			want:   stringtest.LinesLF("s: &s [a, b]", "t: *s"),
		},
		"mapping": {
			before: "m: {a: 1}\n",
			path:   paths.Doc().Child("m"),
			value:  "b",
			err:    "cafe.jacobcolvin.com/v1 inserts $.m[0], but m holds a mapping",
		},
		"place without a value": {
			// The Append creates the sequence, as an Insert at the index 0
			// does.
			before: "a: 1\n",
			path:   paths.Doc().Child("s"),
			value:  "b",
			want:   stringtest.LinesLF("a: 1", "s:", "  - b"),
		},
		"null": {
			before: stringtest.LinesLF("s: ~", "a: 1"),
			path:   paths.Doc().Child("s"),
			value:  "b",
			want:   stringtest.LinesLF("s:", "  - b", "a: 1"),
		},
		"root of an empty document": {
			before: "# c\n",
			path:   paths.Doc(),
			value:  "b",
			want:   "# c\n- b\n",
		},
		"null in a flow mapping with a comment inside it": {
			before: stringtest.LinesLF("a: {", "  b: ~, # c", "  d: 1", "}"),
			path:   paths.MustParse("$.a.b"),
			value:  1,
			err:    "test.yaml:1:4: $.a: cafe.jacobcolvin.com/v1 inserts $.a.b[0], which reaches a flow collection with a comment inside it, and cafe does not edit inside such a collection",
		},
		"null in a pair without braces": {
			before: "- [a: ~]\n",
			path:   paths.MustParse("$[0][0].a"),
			value:  1,
			err:    "test.yaml:1:4: $[0][0].a: cafe.jacobcolvin.com/v1 inserts $[0][0].a[0], which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"null in an entry with an explicit key": {
			before: stringtest.LinesLF("? a", ": ~", "b: 1"),
			path:   paths.MustParse("$.a"),
			value:  1,
			err:    "test.yaml:1:1: $.a: cafe.jacobcolvin.com/v1 inserts $.a[0], which reaches an entry with an explicit key, and cafe does not edit such an entry other than by removing it",
		},
		"explicit key without a value": {
			before: stringtest.LinesLF("? a", "b: 1"),
			path:   paths.MustParse("$.a"),
			value:  1,
			err:    "test.yaml:1:1: $.a: cafe.jacobcolvin.com/v1 inserts $.a[0], which reaches an entry with an explicit key, and cafe does not edit such an entry other than by removing it",
		},
		"place without a value inside an anchored mapping that a protected value reads": {
			before:    stringtest.LinesLF("x: &a {}", "kind: *a"),
			path:      paths.MustParse("$.x.y"),
			value:     1,
			err:       "test.yaml:1:1: $.x: cafe.jacobcolvin.com/v1 inserts $.x.y[0], which lies inside a protected value",
			protected: true,
		},
		"anchored null inside a mapping that a protected value reads": {
			before:    stringtest.LinesLF("x: &a ~", "kind: *a"),
			path:      paths.MustParse("$.x.y"),
			value:     1,
			err:       "test.yaml:1:7: $.x: cafe.jacobcolvin.com/v1 inserts $.x.y[0], which lies inside a protected value",
			protected: true,
		},
		"anchored null that a protected value reads": {
			before:    stringtest.LinesLF("a: &x ~", "kind: *x"),
			path:      paths.MustParse("$.a"),
			value:     1,
			err:       "test.yaml:1:7: $.a: cafe.jacobcolvin.com/v1 inserts $.a[0], which lies inside a protected value",
			protected: true,
		},
		"node whose place an earlier remove took away": {
			// A node on the same path reads the new sequence, but the held
			// node does not append to it.
			before: "s: [a]\n",
			path:   paths.Doc().Child("s"),
			edit: func(ctx context.Context, root *edit.Node) error {
				err := root.At(paths.Doc().Child("s")).Remove()
				if err != nil {
					return err
				}

				return root.At(paths.Doc().Child("s")).Set(ctx, []string{"b", "c"})
			},
			value: "x",
			err:   "test.yaml:1:1: $.s: cafe.jacobcolvin.com/v1 appends to $.s through a node that lost its place to an earlier edit",
		},
		"node on a key that an earlier rename gives another entry": {
			before: stringtest.LinesLF("a: [1]", "b: [2, 3]"),
			path:   paths.Doc().Child("a"),
			edit: func(ctx context.Context, root *edit.Node) error {
				err := root.At(paths.Doc().Child("a")).Remove()
				if err != nil {
					return err
				}

				return root.At(paths.Doc().Child("b")).SetKey("a")
			},
			value: "x",
			err:   "test.yaml:1:1: $.a: cafe.jacobcolvin.com/v1 appends to $.a through a node that lost its place to an earlier edit",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before)

			err := d.Apply(t.Context(), paths.Doc(), func(ctx context.Context, root *edit.Node) error {
				n := root.At(tc.path)

				if tc.edit != nil {
					err := tc.edit(ctx, root)
					if err != nil {
						return err
					}
				}

				return n.Append(ctx, tc.value)
			}, label)

			requireEdit(t, d, err, tc.err, tc.want)

			if tc.err != "" {
				assert.Equal(t, tc.before, d.Text())
			}

			if tc.protected {
				require.ErrorIs(t, err, edit.ErrProtected)
			}
		})
	}
}

func TestNode_Nodes(t *testing.T) {
	t.Parallel()

	ref := niceyaml.NewSourceFromString("r: &r {x: 1}\n", niceyaml.WithName("ref.yaml"))

	tcs := map[string]struct {
		before string
		// The path of the node whose Nodes the Func calls.
		from paths.Path
		path paths.Path
		want []string
		err  string
		// Whether the error matches edit.ErrUsage.
		usage bool
		// Whether the Source allows duplicate keys.
		dups bool
	}{
		"children in document order": {
			before: stringtest.LinesLF("spec:", "  b: 1", "  a: 2", "  c: [x, y]"),
			path:   paths.MustParse("$.spec.*"),
			want:   []string{"$.spec.b", "$.spec.a", "$.spec.c"},
		},
		"items": {
			before: "s: [x, y, z]\n",
			path:   paths.MustParse("$.s[*]"),
			want:   []string{"$.s[0]", "$.s[1]", "$.s[2]"},
		},
		"recursive name": {
			before: stringtest.LinesLF("a:", "  x: 1", "  b:", "    x: 2", "x: 3"),
			path:   paths.MustParse("$..x"),
			want:   []string{"$.a.x", "$.a.b.x", "$.x"},
		},
		"@ path from a node": {
			before: stringtest.LinesLF("a: {x: 1, y: 2}", "b: {x: 3}"),
			from:   paths.MustParse("$.a"),
			path:   paths.MustParse("@.*"),
			want:   []string{"$.a.x", "$.a.y"},
		},
		"$ path from a node": {
			before: stringtest.LinesLF("a: {x: 1}", "b: {x: 3}"),
			from:   paths.MustParse("$.a"),
			path:   paths.MustParse("$.*.x"),
			want:   []string{"$.a.x", "$.b.x"},
		},
		"@ path that selects the node": {
			before: "a: 1\n",
			from:   paths.MustParse("$.a"),
			path:   paths.Current(),
			want:   []string{"$.a"},
		},
		"single place": {
			before: "a: {b: 1}\n",
			path:   paths.MustParse("$.a.b"),
			want:   []string{"$.a.b"},
		},
		"place without a value": {
			before: "a: 1\n",
			path:   paths.MustParse("$.b[*]"),
		},
		"@ path from a place without a value": {
			before: "a: 1\n",
			from:   paths.MustParse("$.b"),
			path:   paths.MustParse("@.*"),
		},
		"root of an empty document": {
			before: "# c\n",
			path:   paths.Doc(),
			want:   []string{"$"},
		},
		"entry that an alias reaches too": {
			// The alias reaches the entry of the anchor, so the entry takes
			// one node.
			before: stringtest.LinesLF("a: &a {x: 1}", "b: *a"),
			path:   paths.MustParse("$.*.x"),
			want:   []string{"$.a.x"},
		},
		"entry that a merge key reaches too": {
			before: stringtest.LinesLF("base: &b {x: 1}", "c: {<<: *b, y: 2}"),
			path:   paths.MustParse("$.*.*"),
			want:   []string{"$.base.x", "$.c.y"},
		},
		"protected values": {
			before: stringtest.LinesLF("apiVersion: v1", "kind: Cafe", "spec: {}"),
			path:   paths.MustParse("$.*"),
			want:   []string{"$.spec"},
		},
		"protected value on a path of names": {
			before: stringtest.LinesLF("apiVersion: v1", "kind: Cafe", "spec: {}"),
			path:   paths.MustParse("$.apiVersion"),
			want:   []string{"$.apiVersion"},
		},
		"entries of a repeated key": {
			before: stringtest.LinesLF("a: 1", "a: 2", "b: 3"),
			path:   paths.MustParse("$.*"),
			want:   []string{"$.a", "$.a", "$.b"},
			dups:   true,
		},
		"path that selects a key": {
			before: "a: 1\n",
			path:   paths.MustParse("$.a~"),
			err:    "test.yaml:1:1: $: cafe.jacobcolvin.com/v1 reads $.a~, which selects a key",
			usage:  true,
		},
		"@ path from a node whose path At refused": {
			before: "a: [{x: 1}]\n",
			from:   paths.MustParse("$.a[*]"),
			path:   paths.MustParse("@.x"),
			err:    "cafe.jacobcolvin.com/v1 reads $.a[*], which selects more than one value",
			usage:  true,
		},
		"path through an alias of a reference document at a merge key": {
			before: "a: {<<: *r}\n",
			path:   paths.MustParse("$.*.*"),
			err:    "test.yaml:1:9: $.a: cafe.jacobcolvin.com/v1 reads $.*.*, which goes through <<: *r at 1:5, a merge key that reads the reference document ref.yaml",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before, niceyaml.WithReferences(ref), niceyaml.WithAllowDuplicateKeys(tc.dups))

			var (
				got     []string
				nodeErr error
			)

			err := d.Apply(t.Context(), paths.Doc(), func(_ context.Context, root *edit.Node) error {
				n := root
				if tc.from.Len() > 0 {
					n = root.At(tc.from)
				}

				nodes, err := n.Nodes(tc.path)
				nodeErr = err

				for _, m := range nodes {
					got = append(got, m.Path().String())
				}

				return nil
			}, label)
			require.NoError(t, err)

			if tc.err != "" {
				require.Error(t, nodeErr)
				assert.Contains(t, niceyaml.FormatError(nodeErr), tc.err)
				assert.Equal(t, tc.usage, errors.Is(nodeErr, edit.ErrUsage))

				return
			}

			require.NoError(t, nodeErr)
			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("@ path from an entry that a later entry hides", func(t *testing.T) {
		t.Parallel()

		d := newDoc(t, stringtest.LinesLF("a: {x: 1}", "a: {x: 2}"), niceyaml.WithAllowDuplicateKeys(true))

		var got []string

		err := d.Apply(t.Context(), paths.MustParse("$.*"), func(_ context.Context, n *edit.Node) error {
			nodes, err := n.Nodes(paths.MustParse("@.*"))
			if err != nil {
				return err
			}

			for _, m := range nodes {
				got = append(got, m.Path().String()+"="+scalar(t, m))
			}

			return nil
		})
		require.NoError(t, err)

		assert.Equal(t, []string{"$.a.x=1", "$.a.x=2"}, got)
	})

	t.Run("@ path from a place without a value below an entry that a later entry hides", func(t *testing.T) {
		t.Parallel()

		d := newDoc(t, stringtest.LinesLF("a: {x: 1}", "a: {x: 2}", "zz: {x: 3}"),
			niceyaml.WithAllowDuplicateKeys(true))

		var (
			hidden bool
			got    []string
		)

		err := d.Apply(t.Context(), paths.MustParse("$.*"), func(_ context.Context, n *edit.Node) error {
			// The first call reads the hidden entry of a.
			if x, _ := n.At(paths.MustParse("@.x")).Scalar(); hidden || x != "1" {
				return nil
			}

			hidden = true
			below := n.At(paths.MustParse("@.zz"))

			for _, path := range []string{"@", "@.*", "@..*", "@..x", "@.zz.x"} {
				nodes, err := below.Nodes(paths.MustParse(path))
				if err != nil {
					return err
				}

				for _, m := range nodes {
					got = append(got, path+": "+m.Path().String())
				}
			}

			return nil
		})
		require.NoError(t, err)

		assert.True(t, hidden)
		assert.Empty(t, got)
	})

	t.Run("edits of several paths in one Apply", func(t *testing.T) {
		t.Parallel()

		d := newDoc(t, stringtest.LinesLF(
			"legacy:",
			"  - x",
			"  - y",
			"  - z",
			"spec:",
			"  a: {cost: 1}",
			"  b: {cost: 2}",
		))

		err := d.Apply(t.Context(), paths.Doc(), func(ctx context.Context, root *edit.Node) error {
			legacy, err := root.Nodes(paths.MustParse("$.legacy[*]"))
			if err != nil {
				return err
			}

			// Each node follows the removals before it.
			for _, n := range legacy {
				err := n.Remove()
				if err != nil {
					return err
				}
			}

			costs, err := root.Nodes(paths.MustParse("$.spec.*.cost"))
			if err != nil {
				return err
			}

			for _, n := range costs {
				err := n.SetKey("price")
				if err != nil {
					return err
				}
			}

			return root.At(paths.MustParse("$.env")).Append(ctx, "two")
		}, label)

		requireEdit(t, d, err, "", stringtest.LinesLF(
			"legacy: []",
			"spec:",
			"  a: {price: 1}",
			"  b: {price: 2}",
			"env:",
			"  - two",
		))
	})

	t.Run("node whose place an earlier edit took away", func(t *testing.T) {
		t.Parallel()

		d := newDoc(t, stringtest.LinesLF("a:", "  x:", "    x: 1", "b: 2"))

		err := d.Apply(t.Context(), paths.Doc(), func(_ context.Context, root *edit.Node) error {
			nodes, err := root.Nodes(paths.MustParse("$..x"))
			require.NoError(t, err)
			require.Len(t, nodes, 2)

			err = nodes[0].Remove()
			if err != nil {
				return err
			}

			// The removal of $.a.x took $.a.x.x away.
			ok, err := nodes[1].Exists()
			assert.False(t, ok)
			require.ErrorIs(t, err, edit.ErrDisplaced)
			require.ErrorIs(t, nodes[1].Remove(), edit.ErrDisplaced)

			// An @ path from such a node returns the problem of the node.
			_, err = nodes[1].Nodes(paths.Current())
			require.ErrorIs(t, err, edit.ErrDisplaced)

			return nil
		}, label)

		requireEdit(t, d, err, "", stringtest.LinesLF("a: {}", "b: 2"))
	})

	t.Run("removal of each place of a recursive descent", func(t *testing.T) {
		t.Parallel()

		d := newDoc(t, stringtest.LinesLF("a:", "  x:", "    x: 1", "b: 2"))

		var skipped int

		err := d.Apply(t.Context(), paths.Doc(), func(_ context.Context, root *edit.Node) error {
			nodes, err := root.Nodes(paths.MustParse("$..x"))
			if err != nil {
				return err
			}

			for _, n := range nodes {
				_, err := n.Exists()
				if errors.Is(err, edit.ErrDisplaced) {
					skipped++

					continue
				}

				if err != nil {
					return err
				}

				err = n.Remove()
				if err != nil {
					return err
				}
			}

			return nil
		}, label)

		requireEdit(t, d, err, "", stringtest.LinesLF("a: {}", "b: 2"))
		assert.Equal(t, 1, skipped)
	})

	t.Run("after its Apply", func(t *testing.T) {
		t.Parallel()

		d := newDoc(t, "s: [a, b]\n")

		var kept *edit.Node

		err := d.Apply(t.Context(), paths.Doc(), func(_ context.Context, root *edit.Node) error {
			kept = root

			return nil
		})
		require.NoError(t, err)

		_, err = kept.Nodes(paths.MustParse("$.s[*]"))
		require.ErrorIs(t, err, edit.ErrEnded)
	})

	t.Run("after a call that leaves the text apart from the model", func(t *testing.T) {
		t.Parallel()

		// As in TestNode_Own_FailedCall, the Set of a new key differs from
		// the model after it wrote. Nodes then reads the text as the Set
		// found it, and the Apply fails with ErrEngine.
		before := stringtest.LinesLF("x: &d {k: 1}", "c: *d", "a: !t")

		d := newDoc(t, before)

		err := d.Apply(t.Context(), paths.Doc(), func(ctx context.Context, root *edit.Node) error {
			err := root.At(paths.Doc().Child("b")).Set(ctx, 9)
			require.NoError(t, err)

			for _, path := range []string{"$.*", "@.*"} {
				nodes, err := root.Nodes(paths.MustParse(path))
				require.NoError(t, err, path)
				assert.Len(t, nodes, 3, path)
			}

			return nil
		})
		require.ErrorIs(t, err, edit.ErrEngine)

		assert.Equal(t, before, d.Text())
	})
}
