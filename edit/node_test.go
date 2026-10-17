package edit_test

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

				assert.Equal(t, !tc.none, held.Exists())

				if tc.again {
					assert.True(t, root.At(paths.MustParse(tc.held)).Exists())
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

func TestNode_Text(t *testing.T) {
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
		},
		"alias of a scalar of the document": {
			before: stringtest.LinesLF("w: &w world", "a: *w"),
			want:   "world",
			kind:   niceyaml.NodeScalar,
		},
		"alias of a mapping of the document": {
			before: stringtest.LinesLF("m: &m {y: 2}", "a: *m"),
			kind:   niceyaml.NodeMapping,
		},
		"alias of a scalar of a reference document": {
			before: "a: *word\n",
			want:   "hello",
			kind:   niceyaml.NodeScalar,
		},
		"alias of a quoted scalar of a reference document": {
			before: "a: *quoted\n",
			want:   "a b",
			kind:   niceyaml.NodeScalar,
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
		},
		"literal block scalar with strip chomping": {
			before: stringtest.LinesLF("a: |-", "  y", "  z"),
			want:   "y\nz",
			kind:   niceyaml.NodeScalar,
		},
		"literal block scalar with keep chomping": {
			before: stringtest.LinesLF("a: |+", "  y", "", "b: 1"),
			want:   "y\n\n",
			kind:   niceyaml.NodeScalar,
		},
		"folded block scalar": {
			before: stringtest.LinesLF("a: >", "  y", "  z"),
			want:   "y z\n",
			kind:   niceyaml.NodeScalar,
		},
		"folded block scalar with strip chomping": {
			before: stringtest.LinesLF("a: >-", "  y", "  z"),
			want:   "y z",
			kind:   niceyaml.NodeScalar,
		},
		"block scalar with an indentation indicator": {
			before: stringtest.LinesLF("a: |2", "   y", "  z"),
			want:   " y\nz\n",
			kind:   niceyaml.NodeScalar,
		},
		"anchored block scalar": {
			before: stringtest.LinesLF("a: &x |", "  y"),
			want:   "y\n",
			kind:   niceyaml.NodeScalar,
		},
		"tagged block scalar": {
			before: stringtest.LinesLF("a: !!str |-", "  y"),
			want:   "y",
			kind:   niceyaml.NodeScalar,
		},
		"alias of a block scalar of the document": {
			before: stringtest.LinesLF("b: &b |", "  y", "  z", "a: *b"),
			want:   "y\nz\n",
			kind:   niceyaml.NodeScalar,
		},
		"alias of a block scalar of a reference document": {
			before: "a: *block\n",
			want:   "r s",
			kind:   niceyaml.NodeScalar,
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
				assert.Equal(t, tc.want, a.Text())
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
		// problem of the Decode binds at the alias.
		d := newDoc(t, stringtest.LinesLF("a: 1", "b: *q"))

		err := applyAt(t, d, paths.Doc().Child("b"), func(ctx context.Context, n *edit.Node) error {
			assert.False(t, n.Exists())
			assert.Equal(t, niceyaml.NodeNone, n.Kind())

			_, err := n.Decode[any](ctx)

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
				if n.At(paths.MustParse("$.spec.tags[*]")).Exists() {
					return errors.New("the path selects a value")
				}

				return nil
			},
			want: stringtest.LinesLF("spec:", "  tags: [a, b]"),
		},
		"edit through a path that selects more than one value": {
			before: stringtest.LinesLF("spec:", "  tags: [a, b]"),
			fn: func(_ context.Context, n *edit.Node) error {
				_ = n.At(paths.MustParse("$.spec.tags[*]")).Remove() //nolint:errcheck // The Apply fails anyway.

				return nil
			},
			err: "cafe.jacobcolvin.com/v1 removes $.spec.tags[*], which selects more than one value",
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

		ref := niceyaml.NewSourceFromString("base: &b {other: 4}\n")

		// Each case sets the value at path to 5, or moves it to move when
		// move is set.
		merges := map[string]struct {
			before string
			path   paths.Path
			move   paths.Path
			want   string
			err    string
		}{
			"key that the mapping lacks": {
				before: stringtest.LinesLF("spec:", "  <<: *b", "  cost: 3"),
				path:   spec.Child("new"),
				err: "test.yaml:2:7: $.spec: cafe.jacobcolvin.com/v1 sets $.spec.new, " +
					"which goes through <<: *b at 2:3, a merge key that reads a reference document, so that document decides which names $.spec reads",
			},
			"key before the merge key": {
				// The merge key can bring in cost, which then wins over the own
				// entry, so the reference document decides what the path selects.
				before: stringtest.LinesLF("spec:", "  cost: 3", "  <<: *b"),
				path:   spec.Child("cost"),
				err: "test.yaml:3:7: $.spec: cafe.jacobcolvin.com/v1 sets $.spec.cost, " +
					"which goes through <<: *b at 3:3, a merge key that reads a reference document, so that document decides which names $.spec reads",
			},
			"key after the merge key": {
				before: stringtest.LinesLF("spec:", "  <<: *b", "  cost: 3"),
				path:   spec.Child("cost"),
				want:   stringtest.LinesLF("spec:", "  <<: *b", "  cost: 5"),
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
		}

		for name, tc := range merges {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, tc.before,
					niceyaml.WithName("test.yaml"), niceyaml.WithReferences(ref))

				d, err := edit.New(doc, edit.WithApp("cafe"))
				require.NoError(t, err)

				err = applyAt(t, d, tc.path, func(ctx context.Context, n *edit.Node) error {
					if tc.move.IsAbsolute() {
						return n.Move(tc.move)
					}

					return n.Set(ctx, 5)
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
