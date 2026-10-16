package edit_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/edit"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

var (
	spec       = paths.Doc().Child("spec")
	typeFields = []paths.Path{paths.Doc().Child("apiVersion"), paths.Doc().Child("kind")}
	// The label that the tests give their edits.
	label = edit.WithLabel("cafe.jacobcolvin.com/v1")
)

// newDoc returns a Doc of the first document of text, named test.yaml,
// whose type fields are protected.
func newDoc(t *testing.T, text string) *edit.Doc {
	t.Helper()

	doc := yamltest.FirstDocument(t, text, niceyaml.WithName("test.yaml"))

	d, err := edit.New(doc, edit.WithApp("cafe"), edit.WithProtected(typeFields...))
	require.NoError(t, err)

	return d
}

// applyAt runs fn on the node at path, which an Apply at the root of d
// with the label of the tests reaches through At.
func applyAt(t *testing.T, d *edit.Doc, path paths.Path, fn edit.Func) error {
	t.Helper()

	err := d.Apply(t.Context(), paths.Doc(), func(ctx context.Context, root *edit.Node) error {
		return fn(ctx, root.At(path))
	}, label)

	return err //nolint:wrapcheck // The test reads the error of the Apply.
}

// requireEdit checks the outcome of an Apply of d that returned err. With
// wantErr set, the formatted err must hold wantErr. Otherwise the Apply
// must pass and leave d holding the text want.
func requireEdit(t *testing.T, d *edit.Doc, err error, wantErr, want string) {
	t.Helper()

	if wantErr != "" {
		require.Error(t, err)
		assert.Contains(t, niceyaml.FormatError(err), wantErr)

		return
	}

	require.NoError(t, err)
	assert.Equal(t, want, d.Text())
}

func TestNew(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, "a:\n  b: 1\n")

	below, err := doc.At(paths.Doc().Child("a"))
	require.NoError(t, err)

	tcs := map[string]struct {
		doc  *niceyaml.Node
		opts []edit.Option
		err  string
	}{
		"nil document": {
			err: "New takes the root of a document that holds a value, not nil",
		},
		"node below the root": {
			doc: below,
			err: "New takes the root of a document that holds a value, not the node at $.a",
		},
		"empty document": {
			doc: niceyaml.NewSourceFromString("# only a comment\n").AllDocuments()[0],
			err: "New takes the root of a document that holds a value, not an empty document",
		},
		"protected path that selects more than one value": {
			doc:  doc,
			opts: []edit.Option{edit.WithProtected(paths.Doc().ChildAll())},
			err:  "WithProtected takes a $ path of names and indexes, not $.*",
		},
		"relative protected path": {
			doc:  doc,
			opts: []edit.Option{edit.WithProtected(paths.Current().Child("a"))},
			err:  "WithProtected takes a $ path of names and indexes, not @.a",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := edit.New(tc.doc, tc.opts...)
			require.Error(t, err)
			assert.Equal(t, tc.err, err.Error())
		})
	}
}

func TestNew_Unparsed(t *testing.T) {
	t.Parallel()

	doc := yamltest.UnparsedDocument(t, "a: [b\n")

	_, err := edit.New(doc)
	require.Error(t, err)
	assert.Equal(t, doc.Err().Error(), err.Error())
}

func TestDoc_Apply(t *testing.T) {
	t.Parallel()

	const cafe = "apiVersion: v1\nkind: Cafe\nspec:\n  cost: 45 # kr\n  tags: [a]\n"

	tcs := map[string]struct {
		fn     edit.Func
		path   paths.Path
		before string
		want   string
		err    string
		opts   []edit.ApplyOption
		// The paths of the nodes that fn gets, when set.
		calls []string
	}{
		"each place that the path selects": {
			before: cafe,
			path:   paths.Doc().ChildAll(),
			fn:     func(context.Context, *edit.Node) error { return nil },
			calls:  []string{"$.spec"},
			want:   cafe,
		},
		"path that selects nothing": {
			before: cafe,
			path:   spec.Child("seats"),
			fn:     func(context.Context, *edit.Node) error { return errors.New("called") },
			calls:  []string{},
			want:   cafe,
		},
		"places that an alias shares": {
			before: stringtest.LinesLF("oat: &milk {price: 5}", "soy: *milk"),
			path:   paths.Doc().ChildAll(),
			fn:     func(context.Context, *edit.Node) error { return nil },
			calls:  []string{"$.oat"},
			want:   stringtest.LinesLF("oat: &milk {price: 5}", "soy: *milk"),
		},
		"place that an earlier call took away": {
			before: stringtest.LinesLF("a:", "  b: 1", "c: 2"),
			path:   paths.MustParse("$..*"),
			fn: func(ctx context.Context, n *edit.Node) error {
				if n.Key() != "a" {
					return nil
				}

				return n.Set(ctx, 5)
			},
			calls: []string{"$.a", "$.c"},
			want:  stringtest.LinesLF("a: 5", "c: 2"),
		},
		"error of the func": {
			before: cafe,
			path:   spec.Child("cost"),
			fn: func(context.Context, *edit.Node) error {
				return niceyaml.NewError("too cheap")
			},
			err: "test.yaml:4:9: $.spec.cost: too cheap",
		},
		"error of a method that the func ignores": {
			before: cafe,
			path:   spec.Child("tags").IndexAll(),
			opts:   []edit.ApplyOption{label},
			fn: func(_ context.Context, n *edit.Node) error {
				_ = n.SetKey("b") //nolint:errcheck // The Apply fails with the error anyway.

				return nil
			},
			err: "test.yaml:5:10: $.spec.tags[0]: cafe.jacobcolvin.com/v1 renames this item, " +
				"but an item of a sequence has no key",
		},
		"message without a label": {
			before: cafe,
			path:   spec.Child("tags").Index(0),
			fn:     func(_ context.Context, n *edit.Node) error { return n.SetKey("b") },
			err:    "the edit renames this item, but an item of a sequence has no key",
		},
		"value that a merge key shares changes twice": {
			before: stringtest.LinesLF(
				"extras:",
				"  oat: &milk { price: 5 }",
				"  soy: { <<: *milk }",
			),
			path: paths.MustParse("$.extras.*"),
			opts: []edit.ApplyOption{label},
			fn: func(ctx context.Context, n *edit.Node) error {
				return n.At(paths.Current().Child("price")).Set(ctx, 6)
			},
			err: stringtest.JoinLF(
				"test.yaml:3:14: $.extras.soy.price: cafe.jacobcolvin.com/v1 changes this value a second time",
				"|-- 2:23: $.extras.oat.price: cafe.jacobcolvin.com/v1 changes it here first",
				"`-- write the value out in place of *milk",
			),
		},
		"rename that a merge key undoes": {
			before: stringtest.LinesLF(
				"summer: &summer",
				`  closes: "21:00"`,
				"spec:",
				"  <<: *summer",
				`  closes: "19:00"`,
			),
			path: spec.Child("closes"),
			fn:   func(_ context.Context, n *edit.Node) error { return n.SetKey("close") },
			err: `test.yaml:5:3: $.spec.closes~: the edit renames "closes" to "close", ` +
				`but <<: *summer also brings in "closes"`,
		},
		"relative path": {
			before: cafe,
			path:   paths.Current().Child("spec"),
			fn:     func(context.Context, *edit.Node) error { return nil },
			err:    "Apply takes a $ path without ~, not @.spec",
		},
		"path with a key": {
			before: cafe,
			path:   spec.Key(),
			fn:     func(context.Context, *edit.Node) error { return nil },
			err:    "Apply takes a $ path without ~, not $.spec~",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before)

			var calls []string

			err := d.Apply(t.Context(), tc.path, func(ctx context.Context, n *edit.Node) error {
				calls = append(calls, n.Path().String())

				return tc.fn(ctx, n)
			}, tc.opts...)

			if tc.calls != nil {
				assert.Equal(t, tc.calls, append([]string{}, calls...))
			}

			requireEdit(t, d, err, tc.err, tc.want)
		})
	}
}

func TestDoc_Apply_Nested(t *testing.T) {
	t.Parallel()

	const nested = "Apply runs inside another Apply of the Doc"

	tcs := map[string]struct {
		// Whether the outer Func returns the error of the nested Apply.
		returns bool
	}{
		"error that the func returns": {returns: true},
		"error that the func ignores": {returns: false},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, "a: 1\n")

			calls := 0

			err := d.Apply(t.Context(), paths.Doc(), func(ctx context.Context, n *edit.Node) error {
				err := n.At(paths.Current().Child("a")).Set(ctx, 2)
				if err != nil {
					return err
				}

				nestedErr := d.Apply(ctx, paths.Doc(), func(context.Context, *edit.Node) error {
					calls++

					return nil
				})
				if tc.returns {
					return nestedErr //nolint:wrapcheck // The test reads the error of the nested Apply.
				}

				return nil
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), nested)
			assert.Zero(t, calls)
			assert.Equal(t, "a: 1\n", d.Text())

			require.NoError(
				t,
				d.Apply(t.Context(), paths.Doc(), func(context.Context, *edit.Node) error { return nil }),
			)
		})
	}
}

func TestDoc_Apply_Failed(t *testing.T) {
	t.Parallel()

	const before = "summer: &summer\n  closes: \"21:00\"\nspec:\n  <<: *summer\n  closes: \"19:00\" # late\n  color: red\n"

	tcs := map[string]struct {
		fn  edit.Func
		err string
	}{
		"rename that a merge key undoes": {
			fn: func(_ context.Context, n *edit.Node) error {
				err := n.At(spec.Child("color")).Remove()
				if err != nil {
					return err
				}

				return n.At(spec.Child("closes")).SetKey("close")
			},
			err: `the edit renames "closes" to "close", but <<: *summer also brings in "closes"`,
		},
		"error of the func after an edit": {
			fn: func(_ context.Context, n *edit.Node) error {
				err := n.At(spec.Child("color")).Remove()
				if err != nil {
					return err
				}

				return errors.New("too late")
			},
			err: "too late",
		},
		"panic in the func after an edit": {
			fn: func(_ context.Context, n *edit.Node) error {
				err := n.At(spec.Child("color")).Remove()
				if err != nil {
					return err
				}

				panic("too late")
			},
			err: "panic: too late",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, before)

			err := func() (err error) {
				defer func() {
					if v := recover(); v != nil {
						err = fmt.Errorf("panic: %v", v)
					}
				}()

				return d.Apply(t.Context(), paths.Doc(), tc.fn)
			}()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.err)

			assert.Equal(t, before, d.Text())
			assert.False(t, d.Edited())
			assert.Empty(t, d.Removals())

			text, err := edit.FileText(d)
			require.NoError(t, err)
			assert.Equal(t, before, text)

			// The next Apply starts from the text before the failed one.
			require.NoError(t, d.Apply(t.Context(), spec.Child("color"), func(_ context.Context, n *edit.Node) error {
				return n.Remove()
			}))
			assert.Equal(
				t,
				"summer: &summer\n  closes: \"21:00\"\nspec:\n  <<: *summer\n  closes: \"19:00\" # late\n",
				d.Text(),
			)
			assert.Len(t, d.Removals(), 1)
		})
	}
}

func TestDoc_Apply_NodeAfterItsApply(t *testing.T) {
	t.Parallel()

	d := newDoc(t, "a: 1\n")

	var kept *edit.Node

	require.NoError(t, d.Apply(t.Context(), paths.Doc().Child("a"), func(_ context.Context, n *edit.Node) error {
		kept = n

		return nil
	}))

	assert.Equal(t, paths.Doc().Child("a"), kept.Path())
	assert.False(t, kept.Exists())
	assert.Equal(t, niceyaml.NodeNone, kept.Kind())
	assert.Empty(t, kept.Text())
	require.Error(t, kept.Set(t.Context(), 2))
	require.Error(t, kept.At(paths.Current().Child("b")).Set(t.Context(), 2))

	_, err := kept.Decode[int](t.Context())
	require.Error(t, err)

	assert.Equal(t, "a: 1\n", d.Text())
	assert.False(t, d.Edited())
}

func TestDoc_Locate(t *testing.T) {
	t.Parallel()

	d := newDoc(t, stringtest.LinesLF("spec:", "  cost: 45 # kr"))
	require.NoError(t, applyAt(t, d, spec.Child("cost"), func(_ context.Context, n *edit.Node) error {
		return n.SetKey("price")
	}))

	err := d.Node().Bind(niceyaml.NewError("too cheap", niceyaml.AtPath(spec.Child("price"))))
	assert.Equal(t, "test.yaml:2:9: $.spec.cost: too cheap", err.Error())

	err = d.Node().Bind(niceyaml.NewError("deprecated", niceyaml.AtPath(spec.Child("price").Key())))
	assert.Equal(t, "test.yaml:2:3: $.spec.cost~: deprecated", err.Error())

	err = d.Node().Bind(niceyaml.NewError("missing", niceyaml.AtPath(spec.Child("close"))))
	assert.Equal(t, "test.yaml:1:1: $.spec.close: missing", err.Error())

	require.NoError(t, applyAt(t, d, spec.Child("price"), func(ctx context.Context, n *edit.Node) error {
		return n.Set(ctx, "free")
	}))

	err = d.Node().Bind(niceyaml.NewError("not a number", niceyaml.AtPath(spec.Child("price"))))
	assert.Contains(t, niceyaml.FormatError(err), stringtest.JoinLF(
		"test.yaml:2:9: $.spec.cost: not a number",
		"`-- cafe.jacobcolvin.com/v1 sets $.spec.price from this value",
	))
}

func TestFileText(t *testing.T) {
	t.Parallel()

	const fileText = "FileText takes Docs of one Source, each of a different document"

	text := stringtest.LinesLF(
		"a: 1 # first",
		"---",
		"b: 2 # second",
		"---",
		"c: 3 # third",
	)

	// Edited returns a Doc of each of the documents at indexes of a
	// Source of text, each with its value set to 20.
	edited := func(t *testing.T, src *niceyaml.Source, indexes ...int) []*edit.Doc {
		t.Helper()

		docs := src.Documents()
		require.Len(t, docs, 3)

		out := make([]*edit.Doc, 0, len(indexes))

		for _, i := range indexes {
			d, err := edit.New(docs[i])
			require.NoError(t, err)

			require.NoError(
				t,
				d.Apply(t.Context(), paths.Doc().ChildAll(), func(ctx context.Context, n *edit.Node) error {
					return n.Set(ctx, 20)
				}),
			)

			out = append(out, d)
		}

		return out
	}

	tcs := map[string]struct {
		docs func(t *testing.T) []*edit.Doc
		want string
		err  string
	}{
		"one document": {
			docs: func(t *testing.T) []*edit.Doc {
				t.Helper()

				return edited(t, niceyaml.NewSourceFromString(text), 1)
			},
			want: stringtest.LinesLF("a: 1 # first", "---", "b: 20 # second", "---", "c: 3 # third"),
		},
		"documents out of order": {
			docs: func(t *testing.T) []*edit.Doc {
				t.Helper()

				return edited(t, niceyaml.NewSourceFromString(text), 2, 0)
			},
			want: stringtest.LinesLF("a: 20 # first", "---", "b: 2 # second", "---", "c: 20 # third"),
		},
		"no doc": {
			docs: func(*testing.T) []*edit.Doc { return nil },
			err:  fileText,
		},
		"two docs of one document": {
			docs: func(t *testing.T) []*edit.Doc {
				t.Helper()

				src := niceyaml.NewSourceFromString(text)

				return slices.Concat(edited(t, src, 1), edited(t, src, 1))
			},
			err: fileText,
		},
		"docs of two sources": {
			docs: func(t *testing.T) []*edit.Doc {
				t.Helper()

				return slices.Concat(
					edited(t, niceyaml.NewSourceFromString(text), 0),
					edited(t, niceyaml.NewSourceFromString(text), 1),
				)
			},
			err: fileText,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := edit.FileText(tc.docs(t)...)
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

func TestDoc_Text(t *testing.T) {
	t.Parallel()

	src := niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1 # first", "---", "b: 2 # second"))

	d, err := edit.New(src.Documents()[1])
	require.NoError(t, err)

	require.NoError(t, d.Apply(t.Context(), paths.Doc().Child("b"), func(ctx context.Context, n *edit.Node) error {
		return n.Set(ctx, 20)
	}))

	assert.Equal(t, stringtest.LinesLF("---", "b: 20 # second"), d.Text())
}

func TestDoc_Removals(t *testing.T) {
	t.Parallel()

	d := newDoc(t, stringtest.LinesLF(
		"spec:",
		"  color: red",
		"  tags: [a, b]",
		"  seats: 4",
	))

	require.NoError(t, d.Apply(t.Context(), spec.Child("color"), func(_ context.Context, n *edit.Node) error {
		return n.Remove(edit.WithReason("menu colors moved to the theme"))
	}, label))
	require.NoError(t, d.Apply(t.Context(), spec.Child("tags").Index(1), func(_ context.Context, n *edit.Node) error {
		return n.Remove()
	}))

	assert.True(t, d.Edited())
	assert.Equal(t, stringtest.LinesLF("spec:", "  tags: [a]", "  seats: 4"), d.Text())

	got := make([]string, 0, len(d.Removals()))
	for _, rm := range d.Removals() {
		got = append(got, fmt.Sprintf("%s %q %q key=%q item=%t %d:%d-%d:%d", rm.Path, rm.Label, rm.Reason,
			rm.Key, rm.Item, rm.Range.Start.Line, rm.Range.Start.Col, rm.Range.End.Line, rm.Range.End.Col))
	}

	assert.Equal(t, []string{
		`$.spec.color~ "cafe.jacobcolvin.com/v1" "menu colors moved to the theme" key="color" item=false 1:2-1:7`,
		`$.spec.tags[1] "" "" key="" item=true 2:12-2:13`,
	}, got)
}

func TestWithUnmarshalers(t *testing.T) {
	t.Parallel()

	type level int

	names := niceyaml.CustomUnmarshaler(func(_ context.Context, l *level, decode func(any) error) error {
		var s string

		err := decode(&s)
		if err != nil {
			return err
		}

		*l = level(strings.Count(s, "!"))

		return nil
	})

	doc := yamltest.FirstDocument(t, "alarm: loud!!\n")

	d, err := edit.New(doc, edit.WithUnmarshalers(names))
	require.NoError(t, err)

	var got level

	require.NoError(t, d.Apply(t.Context(), paths.Doc().Child("alarm"), func(ctx context.Context, n *edit.Node) error {
		got, err = n.Decode[level](ctx)

		return err
	}))

	assert.Equal(t, level(2), got)

	read, err := d.Node().Decode[map[string]level](t.Context())
	require.NoError(t, err)
	assert.Equal(t, map[string]level{"alarm": 2}, read)
}

func TestWithProtected(t *testing.T) {
	t.Parallel()

	const (
		cafe = "apiVersion: v1\nkind: Cafe\nspec:\n  seats: 4\n"
		meta = "metadata:\n  name: kaffe\n  ns: town\nspec:\n  seats: 4\n"
	)

	metadata := paths.Doc().Child("metadata")

	tcs := map[string]struct {
		fn        edit.Func
		path      paths.Path
		before    string
		err       string
		protected []paths.Path
	}{
		"set": {
			before: cafe,
			path:   paths.Doc().Child("kind"),
			fn:     func(ctx context.Context, n *edit.Node) error { return n.Set(ctx, "Bistro") },
			err:    "cafe.jacobcolvin.com/v1 sets $.kind, which is a protected value",
		},
		"rename": {
			before: cafe,
			path:   paths.Doc().Child("kind"),
			fn:     func(_ context.Context, n *edit.Node) error { return n.SetKey("type") },
			err:    `cafe.jacobcolvin.com/v1 renames "kind", which is a protected value`,
		},
		"remove": {
			before: cafe,
			path:   paths.Doc().Child("apiVersion"),
			fn:     func(_ context.Context, n *edit.Node) error { return n.Remove() },
			err:    "cafe.jacobcolvin.com/v1 removes $.apiVersion, which is a protected value",
		},
		"move": {
			before: cafe,
			path:   paths.Doc().Child("kind"),
			fn:     func(_ context.Context, n *edit.Node) error { return n.Move(spec.Child("kind")) },
			err:    `cafe.jacobcolvin.com/v1 moves "kind", which is a protected value`,
		},
		"move onto": {
			before: cafe,
			path:   spec.Child("seats"),
			fn:     func(_ context.Context, n *edit.Node) error { return n.Move(paths.Doc().Child("kind")) },
			err:    `cafe.jacobcolvin.com/v1 moves "seats" to $.kind, which is a protected value`,
		},
		"set of a value that holds one": {
			before:    meta,
			protected: []paths.Path{metadata.Child("name")},
			path:      metadata,
			fn:        func(ctx context.Context, n *edit.Node) error { return n.Set(ctx, "none") },
			err:       "cafe.jacobcolvin.com/v1 sets $.metadata, which holds a protected value",
		},
		"set inside": {
			before:    meta,
			protected: []paths.Path{metadata},
			path:      metadata.Child("name"),
			fn:        func(ctx context.Context, n *edit.Node) error { return n.Set(ctx, "bistro") },
			err:       "cafe.jacobcolvin.com/v1 sets $.metadata.name, which lies inside a protected value",
		},
		"create inside": {
			before:    meta,
			protected: []paths.Path{metadata},
			path:      metadata.Child("extra"),
			fn:        func(ctx context.Context, n *edit.Node) error { return n.Set(ctx, true) },
			err:       "cafe.jacobcolvin.com/v1 sets $.metadata.extra, which lies inside a protected value",
		},
		"rename inside": {
			before:    meta,
			protected: []paths.Path{metadata},
			path:      metadata.Child("ns"),
			fn:        func(_ context.Context, n *edit.Node) error { return n.SetKey("namespace") },
			err:       `cafe.jacobcolvin.com/v1 renames "ns", which lies inside a protected value`,
		},
		"remove inside": {
			before:    meta,
			protected: []paths.Path{metadata},
			path:      metadata.Child("ns"),
			fn:        func(_ context.Context, n *edit.Node) error { return n.Remove() },
			err:       "cafe.jacobcolvin.com/v1 removes $.metadata.ns, which lies inside a protected value",
		},
		"move out": {
			before:    meta,
			protected: []paths.Path{metadata},
			path:      metadata.Child("ns"),
			fn:        func(_ context.Context, n *edit.Node) error { return n.Move(spec.Child("ns")) },
			err:       `cafe.jacobcolvin.com/v1 moves "ns", which lies inside a protected value`,
		},
		"move in": {
			before:    meta,
			protected: []paths.Path{metadata},
			path:      spec.Child("seats"),
			fn:        func(_ context.Context, n *edit.Node) error { return n.Move(metadata.Child("seats")) },
			err: `cafe.jacobcolvin.com/v1 moves "seats" to $.metadata.seats, ` +
				"which lies inside a protected value",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			protected := tc.protected
			if protected == nil {
				protected = typeFields
			}

			doc := yamltest.FirstDocument(t, tc.before, niceyaml.WithName("test.yaml"))

			d, err := edit.New(doc, edit.WithProtected(protected...))
			require.NoError(t, err)

			requireEdit(t, d, applyAt(t, d, tc.path, tc.fn), tc.err, "")
			assert.Equal(t, tc.before, d.Text())
		})
	}
}

func TestWithProtected_Matches(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocument(t, "metadata:\n  name: kaffe\n  ns: town\nspec:\n  seats: 4\n")

	d, err := edit.New(doc, edit.WithProtected(paths.Doc().Child("metadata")))
	require.NoError(t, err)

	var calls []string

	require.NoError(t, d.Apply(t.Context(), paths.MustParse("$..*"), func(_ context.Context, n *edit.Node) error {
		calls = append(calls, n.Path().String())

		return nil
	}))

	assert.Equal(t, []string{"$.spec", "$.spec.seats"}, calls)
}

func ExampleDoc_Apply() {
	src := niceyaml.NewSourceFromString(stringtest.LinesLF(
		"spec:",
		"  extras:",
		"    oat: # Ask the barista.",
		"      cost: 5 # kr",
		"    soy: {cost: 6}",
	), niceyaml.WithName("menu.yaml"))

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

	err = d.Apply(context.Background(), paths.MustParse("$.spec.extras.*"),
		func(ctx context.Context, n *edit.Node) error {
			cost := n.At(paths.Current().Child("cost"))
			if !cost.Exists() {
				return nil
			}

			return cost.SetKey("price")
		})
	if err != nil {
		fmt.Println(err)

		return
	}

	text, err := edit.FileText(d)
	if err != nil {
		fmt.Println(err)

		return
	}

	fmt.Print(text)

	// Output:
	// spec:
	//   extras:
	//     oat: # Ask the barista.
	//       price: 5 # kr
	//     soy: {price: 6}
}
