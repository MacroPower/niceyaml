package edit_test

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"reflect"
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
// whose type fields are protected. The Source of the text takes opts too.
func newDoc(t *testing.T, text string, opts ...niceyaml.SourceOption) *edit.Doc {
	t.Helper()

	doc := yamltest.FirstDocument(t, text, append([]niceyaml.SourceOption{niceyaml.WithName("test.yaml")}, opts...)...)

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

// failsWith checks err, the error of an Apply of a row that expects an
// Apply to fail with an error that holds want, or every Apply to pass when
// want is "". It reports whether the Apply failed as the row expects, which
// ends the row.
func failsWith(t *testing.T, err error, want string) bool {
	t.Helper()

	if want == "" || err == nil {
		require.NoError(t, err)

		return false
	}

	assert.Contains(t, niceyaml.FormatError(err), want)

	return true
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
		// Whether niceyaml.IsInvalid reports the error, because the text of
		// the document is at fault.
		invalid bool
	}{
		"nil document": {
			err: "New takes the root of a document that holds a value, not nil",
		},
		"node below the root": {
			doc: below,
			err: "New takes the root of a document that holds a value, not the node at $.a",
		},
		"empty document": {
			doc:     niceyaml.NewSourceFromString("# only a comment\n").AllDocuments()[0],
			err:     "New takes the root of a document that holds a value, not an empty document",
			invalid: true,
		},
		"document of a tag alone": {
			// Go-yaml's parser reads no value from the document.
			doc:     niceyaml.NewSourceFromString("!!str").AllDocuments()[0],
			err:     "New takes the root of a document that holds a value, not an empty document",
			invalid: true,
		},
		"control: document of a tag after a header": {
			// A control: the tag ends the text, so go-yaml's parser reads no
			// value from the document.
			doc:     niceyaml.NewSourceFromString("--- !t").AllDocuments()[0],
			err:     "New takes the root of a document that holds a value, not an empty document",
			invalid: true,
		},
		"control: document of a tag that a tab ends": {
			// A control for the doc of New (fix25 G25-3). Go-yaml's lexer reads
			// the tab into the tag, so the tag ends the text. Go-yaml's parser
			// then reads no value from the document.
			doc:     niceyaml.NewSourceFromString("!t\t").AllDocuments()[0],
			err:     "New takes the root of a document that holds a value, not an empty document",
			invalid: true,
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
			assert.Equal(t, tc.invalid, niceyaml.IsInvalid(err))
		})
	}
}

func TestNew_TagAlone(t *testing.T) {
	t.Parallel()

	// Go-yaml's parser reads an empty value under a tag that a line break or
	// a space follows, so New takes each document. Every row is a control:
	// the doc of New states the behavior that FIX24 has (fix25 GT2-3).
	tcs := map[string]struct {
		text string
		want any
	}{
		"control: tag and a line break": {
			text: "!t\n",
		},
		"control: tag and a space": {
			text: "!t ",
		},
		"control: tag and a comment": {
			text: "!t # c\n",
		},
		"control: tag, a tab and a space": {
			// Go-yaml's lexer reads the tab into the tag, and the space ends
			// the tag (fix25 G25-3).
			text: "!t\t ",
		},
		"control: core string tag and a line break": {
			text: "!!str\n",
			want: "",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d, err := edit.New(yamltest.FirstDocument(t, tc.text))
			require.NoError(t, err)

			got, err := d.Node().Decode[any](t.Context())
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.text, d.Text())
		})
	}
}

func TestNew_Unparsed(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		text string
	}{
		"flow sequence without its end": {
			text: "a: [b\n",
		},
		"core tag alone on an empty value before a line at its column": {
			// Go-yaml's parser reads b as the value of !!str and refuses it.
			text: "a: !!str\nb: 1\n",
		},
		"tag after an anchor at the end of a text without a final line break": {
			// Go-yaml's lexer drops the tag, and its parser refuses the anchor
			// on no value.
			text: "x: 1\ns: &s !t",
		},
		"byte order mark before a comment line and the header of a later document": {
			// The tokenizer keeps the mark for go-yaml's parser, which refuses
			// it.
			text: stringtest.LinesLF("a: 1", "\ufeff# c", "---", "b: 1"),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			docs := niceyaml.NewSourceFromString(tc.text).AllDocuments()
			require.NotEmpty(t, docs)

			doc := docs[0]
			require.Error(t, doc.Err())

			// New fails with the error of the parse, as the Source gives it.
			_, err := edit.New(doc)
			require.ErrorIs(t, err, niceyaml.ErrSyntax)
			require.NotErrorIs(t, err, edit.ErrEngine)
			assert.Equal(t, doc.Err().Error(), err.Error())
		})
	}

	t.Run("reference document that does not parse", func(t *testing.T) {
		t.Parallel()

		// Each decode of the document fails with the error of the parse of
		// the reference document, so New fails with it, even where no alias
		// of the document reads it.
		ref := niceyaml.NewSourceFromString("s: &s {a: 1}\n  bad: [\n", niceyaml.WithName("ref.yaml"))
		doc := yamltest.FirstDocument(t, "x: 1\n", niceyaml.WithName("test.yaml"), niceyaml.WithReferences(ref))

		_, err := edit.New(doc)
		require.ErrorIs(t, err, niceyaml.ErrSyntax)
		require.NotErrorIs(t, err, edit.ErrEngine)
		assert.True(t, niceyaml.IsInvalid(err))
		assert.Contains(t, niceyaml.FormatError(err), "ref.yaml:2:3:")
	})
}

func TestNew_Documents(t *testing.T) {
	t.Parallel()

	// The parser reads a document before a "---" with the header after it,
	// which gives an anchor without a value at the end of the document a
	// null. Without the header, it refuses the text of the document.
	tcs := map[string]struct {
		text string
		// The path that a removal in the document takes, when set.
		remove string
		want   string
		// The index of the document among the documents of text.
		doc int
	}{}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			src := niceyaml.NewSourceFromString(tc.text, niceyaml.WithName("test.yaml"))

			d, err := edit.New(src.AllDocuments()[tc.doc])
			require.NoError(t, err)

			if tc.remove != "" {
				err = d.Apply(t.Context(), paths.MustParse(tc.remove), func(_ context.Context, n *edit.Node) error {
					return n.Remove()
				})
				require.NoError(t, err)
			}

			got, err := edit.FileText(d)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestDoc_Apply_Documents(t *testing.T) {
	t.Parallel()

	remove := func(_ context.Context, n *edit.Node) error { return n.Remove() }

	// The Apply runs at the root of the document at doc of text, and each
	// row judges the FileText of the Doc.
	tcs := map[string]struct {
		fn   edit.Func
		text string
		want string
		err  string
		doc  int
		// Whether the error matches edit.ErrEngine.
		engine bool
	}{
		"refusal of a comment line after a root flow mapping before a later document": {
			// Go-yaml's parser refuses the comment line after "{}" with its
			// comments, and the error binds at that line of the file. The
			// root holds the comment line, so the problem shows $ (fix25
			// G25-2).
			text:   stringtest.LinesLF("a: 1", "# tail", "---", "b: 2"),
			fn:     func(ctx context.Context, n *edit.Node) error { return remove(ctx, n.At(paths.Doc().Child("a"))) },
			err:    "test.yaml:2:1: $: go-yaml's parser refuses the edited text with its comments: value is not allowed in this context",
			engine: true,
		},
		"refusal of a comment line after a set in a root flow mapping before a later document": {
			// The root holds the comment line, so the problem shows $ (fix25
			// G25-2).
			text: stringtest.LinesLF("{a: 1}", "# tail", "---", "b: 2"),
			fn: func(ctx context.Context, n *edit.Node) error {
				return n.At(paths.Doc().Child("a")).Set(ctx, 2)
			},
			err:    "test.yaml:2:1: $: go-yaml's parser refuses the edited text with its comments: value is not allowed in this context",
			engine: true,
		},
		"refusal of a comment line of a document between two others": {
			// The root holds the comment line, so the problem shows $ (fix25
			// G25-2).
			text:   stringtest.LinesLF("z: 0", "---", "a: 1", "# tail", "---", "b: 2"),
			doc:    1,
			fn:     func(ctx context.Context, n *edit.Node) error { return remove(ctx, n.At(paths.Doc().Child("a"))) },
			err:    "test.yaml:4:1: $: go-yaml's parser refuses the edited text with its comments: value is not allowed in this context",
			engine: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			src := niceyaml.NewSourceFromString(tc.text, niceyaml.WithName("test.yaml"))

			d, err := edit.New(src.AllDocuments()[tc.doc], edit.WithApp("cafe"))
			require.NoError(t, err)

			err = d.Apply(t.Context(), paths.Doc(), tc.fn)
			if tc.err != "" {
				require.Error(t, err)
				assert.Equal(t, tc.engine, errors.Is(err, edit.ErrEngine))
				assert.Contains(t, niceyaml.FormatError(err), tc.err)

				return
			}

			require.NoError(t, err)

			got, err := edit.FileText(d)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestDoc_Apply_DocumentEnd(t *testing.T) {
	t.Parallel()

	at := func(path string, fn edit.Func) edit.Func {
		return func(ctx context.Context, n *edit.Node) error { return fn(ctx, n.At(paths.MustParse(path))) }
	}

	set := func(v any) edit.Func {
		return func(ctx context.Context, n *edit.Node) error { return n.Set(ctx, v) }
	}

	remove := func(_ context.Context, n *edit.Node) error { return n.Remove() }

	move := func(target string) edit.Func {
		return func(_ context.Context, n *edit.Node) error { return n.Move(paths.MustParse(target)) }
	}

	// Each row runs on text alone and on text before a later document. The
	// comment lines at the end of the document belong to it in both, so the
	// edits leave the same text of the document.
	tcs := map[string]struct {
		fn   edit.Func
		text string
		want string
	}{
		"removal of the last entry before a comment line indented past its key": {
			// The comment line belongs to d, the last entry of r0, so the
			// removal takes it.
			text: stringtest.LinesLF("r0:", "  d: 1", "   # d"),
			fn:   at("$.r0", remove),
			want: "{}\n",
		},
		"new entry after a comment line indented past the last key": {
			text: stringtest.LinesLF("r2: 1", "    # d"),
			fn:   at("$.z", set("x")),
			want: stringtest.LinesLF("r2: 1", "    # d", "z: x"),
		},
		"new block scalar after a comment line indented past the last key": {
			text: stringtest.LinesLF("r2: 1", "    # d"),
			fn:   at("$.z", set("y\n z\n")),
			want: stringtest.LinesLF("r2: 1", "    # d", "z: |", "  y", "   z"),
		},
		"block scalar in place of the last value before a comment line indented past its key": {
			// The lines of the scalar go one space past the comment line, which
			// stays with the entry.
			text: stringtest.LinesLF("a: 1", "    # d"),
			fn:   at("$.a", set("x\n")),
			want: stringtest.LinesLF("a: |", "     x", "    # d"),
		},
		"move of the last entry before a comment line at the first column": {
			// The comment line at the first column stays at the end with the
			// blank line before it.
			text: stringtest.LinesLF("a: 1", "", "b: 2", "# tail"),
			fn:   at("$.b", move("$.z")),
			want: stringtest.LinesLF("a: 1", "z: 2", "", "# tail"),
		},
	}

	for name, tc := range tcs {
		for _, tail := range []string{"", stringtest.LinesLF("---", "zz: 3")} {
			t.Run(fmt.Sprintf("%s, %q after it", name, tail), func(t *testing.T) {
				t.Parallel()

				src := niceyaml.NewSourceFromString(tc.text+tail, niceyaml.WithName("test.yaml"))

				d, err := edit.New(src.AllDocuments()[0])
				require.NoError(t, err)
				require.NoError(t, d.Apply(t.Context(), paths.Doc(), tc.fn))

				got, err := edit.FileText(d)
				require.NoError(t, err)
				assert.Equal(t, tc.want+tail, got)
			})
		}
	}
}

func TestDoc_Apply(t *testing.T) {
	t.Parallel()

	const cafe = "apiVersion: v1\nkind: Cafe\nspec:\n  cost: 45 # kr\n  tags: [a]\n"

	// Both drinks read the milk through the alias *oat.
	drinks := stringtest.LinesLF(
		"milk: &oat",
		"  name: x",
		"  cost: 5",
		"drinks:",
		"  - item: K",
		"    milk: *oat",
		"  - item: L",
		"    milk: *oat",
	)

	// The item a[1] reads the item a[0], which holds a block scalar with keep
	// chomping, through an alias, and z reads a[1].
	nested := stringtest.LinesLF("a:", "  - &x0", "    - |+", "      x", "", "", "  - &x1", "    - *x0", "z: *x1")

	var (
		cost      = paths.MustParse("$.milk.cost")
		firstCost = paths.MustParse("$.drinks[0].milk.cost")
		lastCost  = paths.MustParse("$.drinks[1].milk.cost")
	)

	// The Func that at returns runs fn on the node that path selects.
	at := func(path paths.Path, fn edit.Func) edit.Func {
		return func(ctx context.Context, n *edit.Node) error {
			return fn(ctx, n.At(path))
		}
	}

	// The Func that both returns runs each of fns on the node it gets,
	// until one fails.
	both := func(fns ...edit.Func) edit.Func {
		return func(ctx context.Context, n *edit.Node) error {
			for _, fn := range fns {
				err := fn(ctx, n)
				if err != nil {
					return err
				}
			}

			return nil
		}
	}

	set := func(v any) edit.Func {
		return func(ctx context.Context, n *edit.Node) error { return n.Set(ctx, v) }
	}

	rename := func(key string) edit.Func {
		return func(_ context.Context, n *edit.Node) error { return n.SetKey(key) }
	}

	remove := func(_ context.Context, n *edit.Node) error { return n.Remove() }

	move := func(target string) edit.Func {
		return func(_ context.Context, n *edit.Node) error { return n.Move(paths.MustParse(target)) }
	}

	// The Func that reads returns runs read on the node it gets, and fails
	// when read gives another value than want.
	reads := func(read func(*edit.Node) any, want any) edit.Func {
		return func(_ context.Context, n *edit.Node) error {
			if got := read(n); got != want {
				return fmt.Errorf("the read gives %v, not %v", got, want)
			}

			return nil
		}
	}

	text := func(n *edit.Node) any { return n.Text() }
	kind := func(n *edit.Node) any { return n.Kind() }
	exists := func(n *edit.Node) any { return n.Exists() }

	// The Func that decodes returns decodes the node it gets into an any,
	// and fails when the decode gives another value than want.
	decodes := func(want any) edit.Func {
		return func(ctx context.Context, n *edit.Node) error {
			got, err := n.Decode[any](ctx)
			if err != nil || !reflect.DeepEqual(got, want) {
				return fmt.Errorf("the decode gives %#v, not %#v: %w", got, want, err)
			}

			return nil
		}
	}

	// The Func refusedRead decodes the node it gets, and fails unless the
	// Decode fails with an error that matches paths.ErrReferenceAlias.
	refusedRead := func(ctx context.Context, n *edit.Node) error {
		_, err := n.Decode[any](ctx)
		if !errors.Is(err, paths.ErrReferenceAlias) {
			return fmt.Errorf("the decode does not fail at an alias of a reference document: %w", err)
		}

		return nil
	}

	// A string with a line of one space. A block scalar writes that line
	// with the indent of the scalar and the space after it.
	spaced := "a\n \nb\n"
	// The move of b writes no copy, so *y lands inside the value of its
	// anchor &y. The move fails with ConstructRecursiveAlias at the anchor,
	// so the text never differs from the model, and the calls after it do
	// not run.
	unread := stringtest.LinesLF("a: &y {c: &k !t 1}", "b: &x {a: *y, e: 1}", "c: !t []")
	differ, mend := at(paths.Doc().Child("b"), move("$.a.q")), at(paths.Doc().Child("a"), set(1))
	// A line that a call adds after a block scalar at the end of a text
	// without a final newline adds a line break to its string.
	endsText := "the edit adds lines after this block scalar, which ends the text without a line break, so it " +
		"changes the string of the scalar, when it "
	recursive := "test.yaml:1:4: $.a: the edit moves $.b to $.a.q, which makes a document with an alias inside the " +
		"value of the anchor that it reads, such as *y, and cafe does not edit such a document"
	// The encoder of go-yaml writes this float as 1e+06, which its decoder
	// reads as a string, so a Set of it fails with ErrEngine at its place.
	misread := 1000000.0

	tcs := map[string]struct {
		fn     edit.Func
		path   paths.Path
		before string
		want   string
		err    string
		// The sentinel that the error matches, when set.
		errIs error
		// Whether the error matches edit.ErrEngine.
		engine bool
		opts   []edit.ApplyOption
		// The paths of the nodes that fn gets, when set.
		calls []string
		// The Func of an Apply at the root that runs before the Apply of
		// fn, when set.
		earlier edit.Func
		// Whether the document allows duplicate keys, and whether the alias
		// limit does not apply to it.
		dup, unlimited bool
		// The text of a reference document of the Source, when set.
		ref string
	}{
		"value that go-yaml's encoder misreads at a new item after the removal of an earlier item": {
			// The call creates $.l[1], so the problem binds at the key of the
			// nearest value that the user wrote, under its path, with a detail
			// that names the call. The user's text spells $.l[1] for the item
			// 1, so that path would name another value.
			before: stringtest.LinesLF("l:", "  - 0", "  - 1"),
			path:   paths.Doc(),
			fn:     both(at(paths.MustParse("$.l[0]"), remove), at(paths.MustParse("$.l[1]"), set(misread))),
			err: "test.yaml:1:1: $.l: go-yaml's encoder writes the float 1e+06 as text that go-yaml's decoder reads " +
				"as \"1e+06\"\n|-- report this to the authors of cafe\n`-- the edit sets $.l[1]",
			engine: true,
		},
		"value that go-yaml's encoder misreads at a new key of an item after the removal of an earlier item": {
			before: stringtest.LinesLF("z0: 0", "l:", "  - 0", "  - x: 1"),
			path:   paths.Doc(),
			fn:     both(at(paths.MustParse("$.l[0]"), remove), at(paths.MustParse("$.l[0].new"), set(misread))),
			err: "test.yaml:4:3: $.l[1]: go-yaml's encoder writes the float 1e+06 as text that go-yaml's decoder " +
				"reads as \"1e+06\"\n|-- report this to the authors of cafe\n`-- the edit sets $.l[0].new",
			engine: true,
		},
		"string with a control character at a new key of a nested mapping": {
			before: stringtest.LinesLF("m:", "  a: 1"),
			path:   paths.Doc(),
			fn:     at(paths.MustParse("$.m.z"), set("x\x01y")),
			err: "test.yaml:1:1: $.m: go-yaml's encoder writes U+0001 as it is, and YAML does not allow that in a " +
				"text\n|-- report this to the authors of cafe\n`-- the edit sets $.m.z",
			engine: true,
		},
		"value with two keys that a decode reads as one, after the removal of an earlier item": {
			// The Set creates the keys, so the problem binds at the value that
			// it replaces, under the path that the user's text spells for it.
			before: stringtest.LinesLF("z0: 0", "l:", "  - 0", "  - x: 1"),
			path:   paths.Doc(),
			fn: both(at(paths.MustParse("$.l[0]"), remove),
				at(paths.MustParse("$.l[0].x"), set(map[any]any{1: "a", "1": "b"}))),
			err: "test.yaml:4:8: $.l[1].x: the edit sets $.l[0].x, but the value it writes holds two keys that a " +
				"decode reads as \"1\"\n|-- remove one of the two\n`-- the edit sets $.l[0].x from this value",
		},
		"value with two keys that a decode reads as one, after a rename of the mapping": {
			before: stringtest.LinesLF("z0: 0", "m:", "  x: 1"),
			path:   paths.Doc(),
			fn: both(at(paths.Doc().Child("m"), rename("m2")),
				at(paths.MustParse("$.m2.x"), set(map[any]any{1: "a", "1": "b"}))),
			err: "test.yaml:3:6: $.m.x: the edit sets $.m2.x, but the value it writes holds two keys that a decode " +
				"reads as \"1\"\n|-- remove one of the two\n`-- the edit sets $.m2.x from this value",
		},
		"value with two keys that a decode reads as one, at a key that an inline merge mapping brings in": {
			// No path names a << merge entry, so the problem binds at the value
			// that the Set replaces, under the path of the mapping that holds the
			// merge entry (fix19 P56).
			before: stringtest.LinesLF("z0: 0", "m:", "  <<: {a: 1}"),
			path:   paths.Doc(),
			fn:     at(paths.MustParse("$.m.a"), set(map[any]any{1: "a", "1": "b"})),
			err: "test.yaml:3:11: $.m: the edit sets $.m.a, but the value it writes holds two keys that a decode " +
				"reads as \"1\"\n|-- remove one of the two\n`-- the edit sets $.m.a from this value",
		},
		"value with two keys that a decode reads as one, at a key that an inline merge mapping of the root brings in": {
			before: stringtest.LinesLF("<<: {a: 1}"),
			path:   paths.Doc(),
			fn:     at(paths.MustParse("$.a"), set(map[any]any{1: "a", "1": "b"})),
			err: "test.yaml:1:9: $: the edit sets $.a, but the value it writes holds two keys that a decode " +
				"reads as \"1\"",
		},
		"set through a merge key of a reference document in an item after the removal of an earlier item": {
			// The problem binds at the alias, under the path of the item that
			// holds the merge entry in the user's text.
			before: stringtest.LinesLF("z0: 0", "l:", "  - 0", "  - x: 1", "    <<: *r"),
			ref:    "r: &r {x: 9}\n",
			path:   paths.Doc(),
			fn:     both(at(paths.MustParse("$.l[0]"), remove), at(paths.MustParse("$.l[0].x"), set(2))),
			err:    "test.yaml:5:9: $.l[1]: the edit sets $.l[0].x, which goes through <<: *r at 5:5",
			errIs:  paths.ErrReferenceAlias,
		},
		"set through a merge key of a reference document after a move of its mapping into a sequence": {
			before: stringtest.LinesLF("z0: 0", "m:", "  x: 1", "  <<: *r", "l:", "  - 0"),
			ref:    "r: &r {x: 9}\n",
			path:   paths.Doc(),
			fn:     both(at(paths.Doc().Child("m"), move("$.l[1]")), at(paths.MustParse("$.l[1].x"), set(2))),
			err:    "test.yaml:4:7: $.m: the edit sets $.l[1].x, which goes through <<: *r at 4:3",
			errIs:  paths.ErrReferenceAlias,
		},
		"set of an own entry through a mapping that merges a mapping whose merge key reads a reference document": {
			// The alias of the reference document lies in the merge entry of
			// $.b.d, so the problem binds under that path, not under the path
			// of the mapping whose lookup reads it.
			before: stringtest.LinesLF("b:", "  d: &n2 {<<: *r, k1: 1}", "q:", "  a: 1", "  <<: *n2"),
			ref:    "r: &r {a: 9, z: 9}\n",
			path:   paths.Doc(),
			fn:     at(paths.MustParse("$.q.a"), set(2)),
			err:    "test.yaml:2:15: $.b.d: the edit sets $.q.a, which goes through <<: *r at 2:11",
			errIs:  paths.ErrReferenceAlias,
		},
		"set through a node below a created key that a removal took away, after the removal of an earlier item": {
			// The removal takes away a key that the Set created. So the
			// problem binds under the path of the nearest value above it that
			// the user wrote, the item 1, with a detail that names the Set.
			before: stringtest.LinesLF("z0: 0", "l:", "  - 0", "  - x: 1"),
			path:   paths.Doc(),
			fn: func(ctx context.Context, n *edit.Node) error {
				err := both(at(paths.MustParse("$.l[0]"), remove),
					at(paths.MustParse("$.l[0].new"), set(map[string]any{"a": map[string]int{"b": 1}})))(ctx, n)
				if err != nil {
					return err
				}

				held := n.At(paths.MustParse("$.l[0].new.a.b"))

				err = n.At(paths.MustParse("$.l[0].new")).Remove()
				if err != nil {
					return err
				}

				return held.Set(ctx, 2)
			},
			err: "test.yaml:4:3: $.l[1]: the edit sets $.l[0].new.a.b through a node that lost its place to an earlier edit\n" +
				"|-- the edit sets $.l[0].new\n" +
				"`-- the edit sets $.l[0].new.a.b",
		},
		"rename onto a key that a created mapping holds": {
			// Both keys come from the Set, so the problem and its detail bind
			// at the nearest value that the user wrote, and each names the
			// edit and the path of its key.
			before: stringtest.LinesLF("z0: 0", "m:", "  x: 1"),
			path:   paths.Doc(),
			fn: both(at(paths.MustParse("$.m.new"), set(map[string]int{"a": 1, "b": 2})),
				at(paths.MustParse("$.m.new.b"), rename("a"))),
			err: "test.yaml:2:1: $.m: the edit renames \"b\" to \"a\", which already exists\n" +
				"|-- 2:1: $.m: \"a\" is defined here\n|   `-- the edit sets $.m.new\n|-- remove one of the two\n" +
				"|-- the edit sets $.m.new\n" +
				"`-- the edit renames $.m.new.b to \"a\"",
		},
		"rename onto a key of an inline merge mapping": {
			// No path names a key inside a merge entry, so the problem shows
			// the path of the mapping that holds it, without a `~`.
			before: stringtest.LinesLF("m:", "  <<: {d: 2, b: 1}"),
			path:   paths.MustParse("$.m.d"),
			fn:     rename("b"),
			err: "test.yaml:2:8: $.m: the edit renames \"d\" to \"b\", which already exists\n" +
				"|-- 2:14: $.m: \"b\" is defined here",
		},
		"rename onto a key of an inline merge mapping at the root": {
			before: "<<: {d: 2, b: 1}\n",
			path:   paths.MustParse("$.d"),
			fn:     rename("b"),
			err:    "test.yaml:1:6: $: the edit renames \"d\" to \"b\", which already exists\n|-- 1:12: $: \"b\" is defined here",
		},
		"rename onto a key of an inline merge mapping in an item": {
			before: stringtest.LinesLF("l:", "  - <<: {d: 2, b: 1}"),
			path:   paths.MustParse("$.l[0].d"),
			fn:     rename("b"),
			err:    "test.yaml:2:10: $.l[0]: the edit renames \"d\" to \"b\", which already exists",
		},
		"rename onto a key that an alias merge mapping holds": {
			// A control: the key lies in the anchored mapping, whose path names
			// it.
			before: stringtest.LinesLF("x: &x {d: 2, b: 1}", "m:", "  <<: *x"),
			path:   paths.MustParse("$.m.d"),
			fn:     rename("b"),
			err:    "test.yaml:1:8: $.x.d~: the edit renames \"d\" to \"b\", which already exists",
		},
		"move onto a key of an inline merge mapping": {
			// The key d lies inside the merge entry, so the problem shows the
			// path of the mapping that holds the entry, without a `~`, as for a
			// call before any edit (fix19 G1-2).
			before: stringtest.LinesLF("m:", "  <<: {d: 2, b: 1}"),
			path:   paths.MustParse("$.m.d"),
			fn:     func(_ context.Context, n *edit.Node) error { return n.Move(paths.MustParse("$.m.b")) },
			err:    "test.yaml:2:8: $.m: the edit moves \"d\" to $.m.b, which already exists",
		},
		"move onto a key that an alias merge mapping holds": {
			// The key d lies in the anchored mapping, whose path names it.
			before: stringtest.LinesLF("x: &x {d: 2}", "m:", "  <<: *x", "  b: 1"),
			path:   paths.MustParse("$.m.d"),
			fn:     func(_ context.Context, n *edit.Node) error { return n.Move(paths.MustParse("$.m.b")) },
			err:    "test.yaml:1:8: $.x.d~: the edit moves \"d\" to $.m.b, which already exists",
		},
		"set below a scalar that an alias merge mapping holds": {
			before: stringtest.LinesLF("x: &x {d: 2}", "m:", "  <<: *x"),
			path:   paths.Doc(),
			fn:     at(paths.MustParse("$.m.d.z"), set(1)),
			err:    "test.yaml:1:11: $.x.d: the edit sets $.m.d.z, but d holds a scalar",
		},
		"value that go-yaml's encoder misreads at a key of an inline merge mapping": {
			before: stringtest.LinesLF("z0: 0", "m:", "  <<: {a: 1}"),
			path:   paths.Doc(),
			fn:     at(paths.MustParse("$.m.a"), set(misread)),
			err: "test.yaml:3:11: $.m: go-yaml's encoder writes the float 1e+06 as text that go-yaml's decoder " +
				"reads as \"1e+06\"",
			engine: true,
		},
		"set past the end of a root sequence whose first item holds a sequence": {
			// The root binds at the first token of its first item, the "-" of
			// the sequence that the item holds.
			before: stringtest.LinesLF("-", "  - {k: x}", "- a"),
			path:   paths.Doc(),
			fn:     at(paths.MustParse("$[5].x"), set(1)),
			err:    "test.yaml:2:3: $: the edit sets $[5].x, but the document holds 2 items",
		},
		"set past the end of a root sequence whose first item holds a flow mapping": {
			before: stringtest.LinesLF("- {k: x}", "- a"),
			path:   paths.Doc(),
			fn:     at(paths.MustParse("$[5].x"), set(1)),
			err:    "test.yaml:1:3: $: the edit sets $[5].x, but the document holds 2 items",
		},
		"set past the end of a root sequence whose first item holds a scalar": {
			// A control: the root binds at the scalar of its first item.
			before: stringtest.LinesLF("- a", "- b"),
			path:   paths.Doc(),
			fn:     at(paths.MustParse("$[5].x"), set(1)),
			err:    "test.yaml:1:3: $: the edit sets $[5].x, but the document holds 2 items",
		},
		"move of an item to a later item that holds a scalar": {
			// A message names an item by its path, and a key by its quoted name.
			before: stringtest.LinesLF("l:", "  - 1", "  - 2"),
			path:   paths.MustParse("$.l[0]"),
			fn:     move("$.l[1].q"),
			err:    "test.yaml:2:5: $.l[0]: the edit moves $.l[0] to $.l[1].q, but $.l[1] holds a scalar",
		},
		"set of an int tag over null below it": {
			// A decode reads "!!int ~" as 0, so p holds a scalar.
			before: "p: !!int ~\n",
			path:   paths.Doc(),
			fn:     at(paths.MustParse("$.p.z"), set(1)),
			err:    "test.yaml:1:10: $.p: the edit sets $.p.z, but p holds a scalar",
		},
		"set of a local tag over null below it": {
			// A decode reads "!t ~" as the string "~".
			before: "p: !t ~\n",
			path:   paths.Doc(),
			fn:     at(paths.MustParse("$.p.z"), set(1)),
			err:    "test.yaml:1:7: $.p: the edit sets $.p.z, but p holds a scalar",
		},
		"removal of the last entry of an anchored mapping, then a new key through its alias": {
			// The new key goes at the anchor of the mapping, below the key that
			// holds it, and not below the key of the alias.
			before: stringtest.LinesLF("r1:", "  m: &x0", "    a: 2", "r2: *x0"),
			path:   paths.Doc(),
			fn:     both(at(paths.MustParse("$.r1.m.a"), remove), at(paths.MustParse("$.r2.z"), set(2))),
			want:   stringtest.LinesLF("r1:", "  m: &x0", "    z: 2", "r2: *x0"),
		},
		"removal of the last item of an anchored sequence, then a new item through its alias": {
			before: stringtest.LinesLF("r1:", "  m: &x0", "    - 1", "r2: *x0"),
			path:   paths.Doc(),
			fn:     both(at(paths.MustParse("$.r1.m[0]"), remove), at(paths.MustParse("$.r2[0]"), set(2))),
			want:   stringtest.LinesLF("r1:", "  m: &x0", "    - 2", "r2: *x0"),
		},
		"removal of the last entry of an anchored mapping, then a new key through its alias in a flow sequence": {
			// The mapping lies in block context, so the new key takes a line
			// of its own there.
			before: stringtest.LinesLF("r1:", "  m: &x0", "    a: 2", "r2: [*x0]"),
			path:   paths.Doc(),
			fn:     both(at(paths.MustParse("$.r1.m.a"), remove), at(paths.MustParse("$.r2[0].z"), set(2))),
			want:   stringtest.LinesLF("r1:", "  m: &x0", "    z: 2", "r2: [*x0]"),
		},
		"move of the only entry of an anchored merge value through its alias": {
			before: stringtest.LinesLF("r1:", "  <<: &x0", "    a: 2", "r2: *x0"),
			path:   paths.Doc(),
			fn:     at(paths.MustParse("$.r2.a"), move("$.r2.z")),
			err:    "test.yaml:2:3: $.r1: the edit moves $.r2.a to $.r2.z, which goes through a merge key whose value has an anchor, and cafe does not edit through such a merge key",
		},
		"rename to a key whose path the parser spells as the path of a key of another mapping": {
			// The path of 'b' under the new key 'a' repeats the path of the
			// root key a'.'b, which the parser compares across mappings.
			before: stringtest.LinesLF(`"a'.'b": 0`, "c:", `  "'b'": 1`),
			path:   paths.Doc(),
			fn:     at(paths.Doc().Child("c"), rename("'a'")),
			err: `test.yaml:3:3: $.c.'\'b\'': the edit renames $.c to "'a'", and go-yaml's parser refuses the ` +
				`edited text: mapping key "'b'" already defined at [1:1]`,
			engine: true,
		},
		"rename to a key whose path the parser spells as the path of a key in another mapping": {
			before: stringtest.LinesLF(`"'a'":`, `  "'b'": 1`, "c: 2"),
			path:   paths.Doc(),
			fn:     at(paths.Doc().Child("c"), rename("a'.'b")),
			err:    `mapping key "a'.'b" already defined at [2:3]`,
			engine: true,
		},
		"move of an entry that hides a merged key, then a set by its path": {
			// After the move, the path $.a reaches the a that the merge key
			// brings in, and the mapping reads it. The move made that change,
			// so the problem names the move.
			before: stringtest.LinesLF("<<: {a: 1}", "a: 2"),
			path:   paths.Doc(),
			fn:     both(at(paths.Doc().Child("a"), move("$.z")), at(paths.Doc().Child("a"), set(3))),
			err:    "test.yaml:1:1: $: the edit moves $.a to $.z, which changes the keys of a merge through a merge key that merges a mapping written in place, and cafe does not change the keys of such a merge",
		},
		"move of an entry that hides a merged entry, then a set elsewhere": {
			// The move makes m read the x of the merge key. The problem names
			// the move, not the later set.
			before: stringtest.LinesLF("b: &b {x: 1}", "m:", "  <<: *b", "  x: 2", "q: 1"),
			path:   paths.Doc(),
			fn:     both(at(paths.MustParse("$.m.x"), move("$.z")), at(paths.Doc().Child("q"), set(5))),
			err:    "test.yaml:4:3: $.m.x: the edit moves $.m.x to $.z, but <<: *b at 3:3 also brings in x, so $.m still reads x",
		},
		"rename of an entry that hides a merged entry, then a set elsewhere": {
			before: stringtest.LinesLF("b: &b {x: 1}", "m:", "  <<: *b", "  x: 2", "q: 1"),
			path:   paths.Doc(),
			fn:     both(at(paths.MustParse("$.m.x"), rename("y")), at(paths.Doc().Child("q"), set(5))),
			err:    `test.yaml:4:3: $.m.x: the edit renames $.m.x to "y", but <<: *b at 3:3 also brings in x, so $.m still reads x`,
		},
		"move of an entry that hides a merged entry, then a rename in the mapping": {
			before: stringtest.LinesLF("b: &b {x: 1}", "m:", "  <<: *b", "  x: 2", "  q: 1"),
			path:   paths.Doc(),
			fn:     both(at(paths.MustParse("$.m.x"), move("$.z")), at(paths.MustParse("$.m.q"), rename("r"))),
			err:    "test.yaml:4:3: $.m.x: the edit moves $.m.x to $.z, but <<: *b at 3:3 also brings in x, so $.m still reads x",
		},
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
		"entries whose values an alias shares": {
			// Each entry is a place of its own, even when its value is an
			// alias.
			before: stringtest.LinesLF("oat: &milk {price: 5}", "soy: *milk"),
			path:   paths.Doc().ChildAll(),
			fn:     func(context.Context, *edit.Node) error { return nil },
			calls:  []string{"$.oat", "$.soy"},
			want:   stringtest.LinesLF("oat: &milk {price: 5}", "soy: *milk"),
		},
		"entry that two paths reach through an alias": {
			before: stringtest.LinesLF("oat: &milk {price: 5}", "soy: *milk"),
			path:   paths.MustParse("$.*.price"),
			fn:     func(context.Context, *edit.Node) error { return nil },
			calls:  []string{"$.oat.price"},
			want:   stringtest.LinesLF("oat: &milk {price: 5}", "soy: *milk"),
		},
		"entry that a merge key brings in": {
			before: stringtest.LinesLF("base: &b {y: 2}", "m:", "  <<: *b", "  x: 1"),
			path:   paths.MustParse("$..y"),
			fn:     func(context.Context, *edit.Node) error { return nil },
			calls:  []string{"$.base.y"},
			want:   stringtest.LinesLF("base: &b {y: 2}", "m:", "  <<: *b", "  x: 1"),
		},
		"rename of each key of a mapping whose values an alias shares": {
			before: stringtest.LinesLF("a: &x 1", "b: *x", "c: 2"),
			path:   paths.Doc().ChildAll(),
			fn: func(_ context.Context, n *edit.Node) error {
				return n.SetKey(strings.ToUpper(n.Key()))
			},
			calls: []string{"$.a", "$.b", "$.c"},
			want:  stringtest.LinesLF("A: &x 1", "B: *x", "C: 2"),
		},
		"removal of each entry of a mapping whose values an alias shares": {
			// The wildcard meets the anchor first, which *x at b still reads, so
			// the first call fails.
			before: stringtest.LinesLF("a: &x 1", "b: *x", "c: 2"),
			path:   paths.Doc().ChildAll(),
			fn:     func(_ context.Context, n *edit.Node) error { return n.Remove() },
			err:    "test.yaml:2:4: $.b: the edit removes $.a, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"read after the write of an earlier call through an alias": {
			// The call on a keeps the anchor, so the call on b reads the value
			// that the call on a wrote through the alias.
			before: stringtest.LinesLF("a: &x 1", "b: *x"),
			path:   paths.Doc().ChildAll(),
			fn: func(ctx context.Context, n *edit.Node) error {
				v, err := n.Decode[int](ctx)
				if err != nil {
					return err
				}

				return n.Set(ctx, v+1)
			},
			calls: []string{"$.a", "$.b"},
			want:  stringtest.LinesLF("a: &x 2", "b: 3"),
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
		"set through an anchor, then a removal through its alias": {
			// The calls act in order, so the Remove through the alias takes
			// away the entry that the Set through the anchor created.
			before: stringtest.LinesLF("r0: &x0 {c: x}", "r1: *x0"),
			path:   paths.Doc(),
			fn: both(
				at(paths.MustParse("$.r0.z"), set([]any{map[string]any{"a": 1}})),
				at(paths.MustParse("$.r1.z"), remove),
			),
			want: stringtest.LinesLF("r0: &x0 {c: x}", "r1: *x0"),
		},
		"set through an anchor, then a removal through a node that the alias path gave before": {
			// The node takes the value that the Set writes at its place.
			before: stringtest.LinesLF("r0: &x0 {c: x}", "r1: *x0"),
			path:   paths.Doc(),
			fn: func(ctx context.Context, n *edit.Node) error {
				held := n.At(paths.MustParse("$.r1.z"))

				err := n.At(paths.MustParse("$.r0.z")).Set(ctx, 1)
				if err != nil {
					return err
				}

				return held.Remove()
			},
			want: stringtest.LinesLF("r0: &x0 {c: x}", "r1: *x0"),
		},
		"set through an anchor, then a set through a merge key that brings the entry in": {
			before: stringtest.LinesLF("base: &b {c: x}", "m:", "  <<: *b"),
			path:   paths.Doc(),
			fn:     both(at(paths.MustParse("$.base.z"), set(1)), at(paths.MustParse("$.m.z"), set(2))),
			want:   stringtest.LinesLF("base: &b {c: x, z: 2}", "m:", "  <<: *b"),
		},
		"set through a merge key, then a set through the anchor": {
			before: stringtest.LinesLF("base: &b {c: x}", "m:", "  <<: *b"),
			path:   paths.Doc(),
			// Go-yaml's encoder quotes "y", and "z" keeps those quotes.
			fn:   both(at(paths.MustParse("$.m.c"), set("y")), at(paths.MustParse("$.base.c"), set("z"))),
			want: stringtest.LinesLF(`base: &b {c: "z"}`, "m:", "  <<: *b"),
		},
		"recursive wildcard through an alias without an anchor": {
			before: stringtest.LinesLF("a: 1", "b: {c: *q}"),
			path:   paths.MustParse("$..*"),
			fn:     remove,
			err:    "test.yaml:2:8: $.b.c: the edit reads $..*, which goes through *q, an alias that does not resolve",
		},
		"recursive name through an alias without an anchor": {
			before: stringtest.LinesLF("a: 1", "b: {c: *q}"),
			path:   paths.MustParse("$..c"),
			fn:     remove,
			err:    "test.yaml:2:8: $.b.c: the edit reads $..c, which goes through *q, an alias that does not resolve",
		},
		"control: recursive wildcard through an alias without an anchor in a sequence": {
			// Control: the search for the alias reads each item.
			before: "a: [1, *q]\n",
			path:   paths.MustParse("$..*"),
			fn:     remove,
			err:    "test.yaml:1:8: $.a[1]: the edit reads $..*, which goes through *q, an alias that does not resolve",
		},
		"control: recursive name through an alias without an anchor under a tag": {
			// Control: the search for the alias reads through the tag.
			before: "a: !t {c: *q}\n",
			path:   paths.MustParse("$..c"),
			fn:     remove,
			err:    "test.yaml:1:11: $.a.c: the edit reads $..c, which goes through *q, an alias that does not resolve",
		},
		"control: recursive wildcard through an alias without an anchor under an anchor": {
			// Control: the search for the alias reads through the anchor.
			before: "a: &x {c: *q}\n",
			path:   paths.MustParse("$..*"),
			fn:     remove,
			err:    "test.yaml:1:11: $.a.c: the edit reads $..*, which goes through *q, an alias that does not resolve",
		},
		"control: recursive wildcard past a sequence and a merge key, through an alias without an anchor": {
			// Control: the search for the alias reads the items of the
			// sequence and the mapping that the merge key brings in, which hold
			// no alias, before the alias after them.
			before: stringtest.LinesLF("a: [1]", "m: {<<: {c: 1}}", "b: *q"),
			path:   paths.MustParse("$..*"),
			fn:     remove,
			err:    "test.yaml:3:4: $.b: the edit reads $..*, which goes through *q, an alias that does not resolve",
		},
		"control: recursive wildcard past an entry that a later entry hides, which holds an alias without an anchor": {
			// Control: the search for the alias skips the entry that the later
			// entry hides, and the alias there stops nothing.
			before: stringtest.LinesLF("b:", "  c: 1", "  c: *q"),
			dup:    true,
			path:   paths.MustParse("$..*"),
			fn:     remove,
			err:    "test.yaml:3:6: $.b.c: the edit reads $..*, which goes through *q, an alias that does not resolve",
		},
		"recursive wildcard through an alias without an anchor before a mapping": {
			// The search for the alias goes on past the alias to the mapping
			// after it, and that mapping holds no alias.
			before: stringtest.LinesLF("x: *q", "y: {b: 1}"),
			path:   paths.MustParse("$..*"),
			fn:     remove,
			err:    "test.yaml:1:4: $.x: the edit reads $..*, which goes through *q, an alias that does not resolve",
		},
		"wildcard over a mapping that holds an alias without an anchor": {
			before: stringtest.LinesLF("a: 1", "b: {c: *q}"),
			path:   paths.MustParse("$.b.*"),
			fn:     remove,
			err:    "test.yaml:2:8: $.b.c: the edit reads $.b.*, which goes through *q, an alias that does not resolve",
		},
		"wildcard over a sequence that holds an alias without an anchor": {
			before: stringtest.LinesLF("a: 1", "b: [*q]"),
			path:   paths.MustParse("$.b[*]"),
			fn:     remove,
			err:    "test.yaml:2:5: $.b[0]: the edit reads $.b[*], which goes through *q, an alias that does not resolve",
		},
		"wildcard over the entries that a merge key brings in, with an alias without an anchor": {
			before: stringtest.LinesLF("m: &m {a: *q}", "b:", "  <<: *m"),
			path:   paths.MustParse("$.b.*"),
			fn:     remove,
			err:    "test.yaml:1:11: $.m.a: the edit reads $.b.*, which goes through *q, an alias that does not resolve",
		},
		"set below a merge of an alias that reads a sequence with an alias without an anchor": {
			// The merge key reads a sequence of sources through *g, which the
			// subset refuses. The lookup meets that merge key before the item
			// that does not resolve.
			before: stringtest.LinesLF("k2: &g [*q]", "meta:", "  <<: *g"),
			path:   paths.Doc(),
			fn:     at(paths.MustParse("$.meta.version"), set("v2")),
			err: "test.yaml:3:3: $.meta: the edit sets $.meta.version, which goes through a merge key whose alias " +
				"reads a value other than a mapping",
		},
		"recursive wildcard through an alias without an anchor in an entry that a later entry hides": {
			before: stringtest.LinesLF("a: {b: *q}", "a: {b: 2}"),
			dup:    true,
			path:   paths.MustParse("$..*"),
			fn:     remove,
			err:    "test.yaml:1:8: $.a.b: the edit reads $..*, which goes through *q, an alias that does not resolve",
		},
		"set below each entry of a key": {
			// The node of the entry that the later entry hides reads below
			// that entry, so the set writes there.
			before: stringtest.LinesLF("services:", "  web: {replicas: 1}", "  web: {replicas: 2}"),
			dup:    true,
			path:   paths.MustParse("$.services.*"),
			fn:     at(paths.Current().Child("replicas"), set(3)),
			err:    `test.yaml:2:3: $.services.web: the edit sets $.services.web.replicas, which reaches "web", a key that its mapping repeats, and cafe does not edit the entries of a repeated key`,
		},
		"reads below each entry of a key, then a set": {
			before: stringtest.LinesLF("web: {replicas: 1, tag: a}", "web: {replicas: 2, tag: b}"),
			dup:    true,
			path:   paths.MustParse("$.*"),
			fn: func(ctx context.Context, n *edit.Node) error {
				r := n.At(paths.Current().Child("replicas"))

				v, err := r.Decode[int](ctx)
				if err != nil {
					return err
				}

				if !r.Exists() || r.Kind() != niceyaml.NodeScalar || r.Text() != fmt.Sprint(v) ||
					n.At(paths.Current().Child("none")).Exists() {
					return fmt.Errorf("the reads of %s do not agree", r.Path())
				}

				return r.Set(ctx, v+10)
			},
			err: `test.yaml:1:1: $.web: the edit sets $.web.replicas, which reaches "web", a key that its mapping repeats, and cafe does not edit the entries of a repeated key`,
		},
		"removal below each entry of a key": {
			before: stringtest.LinesLF("web:", "  replicas: 1", "  tag: a", "web:", "  replicas: 2"),
			dup:    true,
			path:   paths.MustParse("$.*"),
			fn:     at(paths.Current().Child("replicas"), remove),
			err:    `test.yaml:1:1: $.web: the edit removes $.web.replicas, which reaches "web", a key that its mapping repeats, and cafe does not edit the entries of a repeated key`,
		},
		"creation below each entry of a key": {
			before: stringtest.LinesLF("web:", "  replicas: 1", "web:", "  replicas: 2"),
			dup:    true,
			path:   paths.MustParse("$.*"),
			fn:     at(paths.Current().Child("env", "name"), set("x")),
			err:    `test.yaml:1:1: $.web: the edit sets $.web.env.name, which reaches "web", a key that its mapping repeats, and cafe does not edit the entries of a repeated key`,
		},
		"rename below each entry of a key": {
			before: stringtest.LinesLF("web:", "  b: 1", "web:", "  b: 2"),
			dup:    true,
			path:   paths.MustParse("$.*"),
			fn:     at(paths.Current().Child("b"), rename("c")),
			err:    `test.yaml:1:1: $.web: the edit renames $.web.b to "c", which reaches "web", a key that its mapping repeats, and cafe does not edit the entries of a repeated key`,
		},
		"move out of each entry of a key": {
			before: stringtest.LinesLF("web: {b: 1}", "web: {b: 2}"),
			dup:    true,
			path:   paths.MustParse("$.*"),
			fn: func(_ context.Context, n *edit.Node) error {
				b := n.At(paths.Current().Child("b"))

				return b.Move(paths.Doc().Child("b" + b.Text()))
			},
			err: `test.yaml:1:1: $.web: the edit moves $.web.b to $.b1, which reaches "web", a key that its mapping repeats, and cafe does not edit the entries of a repeated key`,
		},
		"items below each entry of a key": {
			before: stringtest.LinesLF("a: [1, 2]", "a: [3]"),
			dup:    true,
			path:   paths.MustParse("$.*[*]"),
			fn:     set(0),
			err:    `test.yaml:1:1: $.a: the edit sets $.a[0], which reaches "a", a key that its mapping repeats, and cafe does not edit the entries of a repeated key`,
		},
		"entries that later entries hide inside an entry that a later entry hides": {
			before: stringtest.LinesLF("A:", "  X: {y: 1}", "  X: {y: 2}", "A:", "  X: {y: 3}"),
			dup:    true,
			path:   paths.MustParse("$.*.*.y"),
			fn:     set(0),
			err:    `test.yaml:1:1: $.A: the edit sets $.A.X.y, which reaches "A", a key that its mapping repeats, and cafe does not edit the entries of a repeated key`,
		},
		"rename of each entry of a key under a recursive wildcard": {
			// The recursive wildcard selects each entry of A, as `.*` does, so
			// the two entries take the new key in one Apply.
			before: stringtest.LinesLF("A: 1", "A: 2", "B: 3"),
			dup:    true,
			path:   paths.MustParse("$..*"),
			fn:     func(_ context.Context, n *edit.Node) error { return n.SetKey(strings.ToLower(n.Key())) },
			err:    `test.yaml:1:1: $.A: the edit renames $.A to "a", which reaches "A", a key that its mapping repeats, and cafe does not edit the entries of a repeated key`,
		},
		"rename of each entry of a key under a recursive name": {
			before: stringtest.LinesLF("m:", "  A: 1", "  A: 2", "  B: 3"),
			dup:    true,
			path:   paths.MustParse("$..A"),
			fn:     rename("a"),
			err:    `test.yaml:2:3: $.m.A: the edit renames $.m.A to "a", which reaches "A", a key that its mapping repeats, and cafe does not edit the entries of a repeated key`,
		},
		"rename under a recursive wildcard below an entry that a later entry hides": {
			// The wildcard reads below each entry of A, and so it selects the
			// entries of X in each.
			before: stringtest.LinesLF("A:", "  X: 1", "  X: 2", "A:", "  X: 3"),
			dup:    true,
			path:   paths.MustParse("$..*"),
			fn:     func(_ context.Context, n *edit.Node) error { return n.SetKey(strings.ToLower(n.Key())) },
			err:    `test.yaml:1:1: $.A: the edit renames $.A to "a", which reaches "A", a key that its mapping repeats, and cafe does not edit the entries of a repeated key`,
		},
		"recursive wildcard over an entry that a later entry hides, which holds an alias": {
			// A recursive selector does not follow an alias below its start,
			// so it selects the entry and nothing below it.
			before: stringtest.LinesLF("k: &k {b: 1}", "x:", "  A: *k", "  A: 1"),
			dup:    true,
			path:   paths.MustParse("$..*"),
			fn:     func(context.Context, *edit.Node) error { return nil },
			calls:  []string{"$.k", "$.k.b", "$.x", "$.x.A", "$.x.A"},
			want:   stringtest.LinesLF("k: &k {b: 1}", "x:", "  A: *k", "  A: 1"),
		},
		"recursive name below an entry that a later entry hides": {
			before: stringtest.LinesLF("A:", "  name: 1", "A:", "  name: 2"),
			dup:    true,
			path:   paths.MustParse("$..name"),
			fn:     set(0),
			err:    `test.yaml:1:1: $.A: the edit sets $.A.name, which reaches "A", a key that its mapping repeats, and cafe does not edit the entries of a repeated key`,
		},
		"wildcard through an alias of a mapping with entries that later entries hide": {
			// The two paths reach each entry, which takes one call.
			before: stringtest.LinesLF("x: &x {A: {b: 1}, A: {b: 2}}", "y: *x"),
			dup:    true,
			path:   paths.MustParse("$.*.*"),
			fn:     at(paths.Current().Child("b"), set(7)),
			err:    `test.yaml:1:8: $.x.A: the edit sets $.x.A.b, which reaches "A", a key that its mapping repeats, and cafe does not edit the entries of a repeated key`,
		},
		"value that a merge key shares changes twice": {
			// The call at soy sets the price that the merge key brings in from oat,
			// after the call at oat set it.
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
			want: stringtest.LinesLF("extras:", "  oat: &milk { price: 6 }", "  soy: { <<: *milk }"),
		},
		"set value keeps its check after an earlier item leaves": {
			before: stringtest.LinesLF("l:", "  - x: 1", "  - x: 2", "  - x: 3"),
			path:   paths.Doc(),
			fn: func(ctx context.Context, n *edit.Node) error {
				err := n.At(paths.MustParse("$.l[1].x")).Set(ctx, 20)
				if err != nil {
					return err
				}

				return n.At(paths.MustParse("$.l[0]")).Remove()
			},
			want: stringtest.LinesLF("l:", "  - x: 20", "  - x: 3"),
		},
		"set value keeps its check after an earlier item moves out": {
			before: stringtest.LinesLF(
				"l:",
				"  - x: 1",
				"    y: a",
				"  - x: 2",
				"    y: b",
				"  - x: 3",
				"    y: c",
			),
			path: paths.Doc(),
			fn: func(ctx context.Context, n *edit.Node) error {
				err := n.At(paths.MustParse("$.l[1].x")).Set(ctx, 20)
				if err != nil {
					return err
				}

				return n.At(paths.MustParse("$.l[0]")).Move(paths.Doc().Child("first"))
			},
			want: stringtest.LinesLF(
				"l:",
				"  - x: 20",
				"    y: b",
				"  - x: 3",
				"    y: c",
				"first:",
				"  x: 1",
				"  y: a",
			),
		},
		"value that two aliases share changes twice": {
			// Each call sets the cost that both aliases read, in order.
			before: drinks,
			path:   paths.MustParse("$.drinks[*]"),
			opts:   []edit.ApplyOption{label},
			fn:     at(paths.Current().Child("milk", "cost"), set(6)),
			want:   strings.Replace(drinks, "cost: 5", "cost: 6", 1),
		},
		"alias then direct edit in one call": {
			// The calls act in order, so the direct Set wins.
			before: drinks,
			path:   paths.Doc(),
			opts:   []edit.ApplyOption{label},
			fn:     both(at(firstCost, set(6)), at(cost, set(7))),
			want:   strings.Replace(drinks, "cost: 5", "cost: 7", 1),
		},
		"value that an alias shares changes twice in one call": {
			// The calls act in order, so the Set through the alias wins.
			before: drinks,
			path:   paths.Doc(),
			fn:     both(at(cost, set(6)), at(firstCost, set(7))),
			want:   strings.Replace(drinks, "cost: 5", "cost: 7", 1),
		},
		"same alias path twice": {
			before: drinks,
			path:   paths.Doc(),
			fn:     at(firstCost, both(set(6), set(7))),
			want:   strings.Replace(drinks, "cost: 5", "cost: 7", 1),
		},
		"set then rename through an alias": {
			before: drinks,
			path:   paths.Doc(),
			fn:     at(firstCost, both(set(6), rename("price"))),
			want:   strings.Replace(drinks, "cost: 5", "price: 6", 1),
		},
		"rename then set through an alias": {
			before: drinks,
			path:   paths.Doc(),
			fn:     at(firstCost, both(rename("price"), set(7))),
			want:   strings.Replace(drinks, "cost: 5", "price: 7", 1),
		},
		"rename in one call and set through another alias in the next": {
			// The second call sets the key that the first call wrote, through the
			// other alias.
			before: drinks,
			path:   paths.MustParse("$.drinks[*]"),
			fn: func(ctx context.Context, n *edit.Node) error {
				if n.At(paths.Current().Child("item")).Text() == "K" {
					return n.At(paths.Current().Child("milk", "cost")).SetKey("price")
				}

				return n.At(paths.Current().Child("milk", "price")).Set(ctx, 500)
			},
			want: strings.Replace(drinks, "cost: 5", "price: 500", 1),
		},
		"direct then alias in one call": {
			// The calls act in order, so the Set through the alias wins.
			before: drinks,
			path:   paths.Doc(),
			fn:     both(at(cost, set(6)), at(lastCost, set(7))),
			want:   strings.Replace(drinks, "cost: 5", "cost: 7", 1),
		},
		"set through an alias then move directly": {
			// The Move takes the value that the Set through the alias wrote.
			before: drinks,
			path:   paths.Doc(),
			fn: both(at(firstCost, set(6)), at(cost, func(_ context.Context, n *edit.Node) error {
				return n.Move(paths.MustParse("$.milk.price"))
			})),
			want: strings.Replace(drinks, "cost: 5", "price: 6", 1),
		},
		"create through an alias, then set through another alias": {
			// The second call reads the value that the first call created through the
			// other alias.
			before: drinks,
			path:   paths.MustParse("$.drinks[*]"),
			fn: func(ctx context.Context, n *edit.Node) error {
				extra := n.At(paths.Current().Child("milk", "extra"))
				if !extra.Exists() {
					return extra.Set(ctx, 1)
				}

				v, err := extra.Decode[int](ctx)
				if err != nil {
					return err
				}

				return extra.Set(ctx, v+1)
			},
			want: strings.Replace(drinks, "cost: 5", "cost: 5\n  extra: 2", 1),
		},
		"same alias path after an earlier item leaves": {
			before: drinks,
			path:   paths.Doc(),
			fn: both(
				at(lastCost, set(6)),
				at(paths.MustParse("$.drinks[0]"), func(_ context.Context, n *edit.Node) error { return n.Remove() }),
				at(firstCost, set(7)),
			),
			want: stringtest.LinesLF(
				"milk: &oat",
				"  name: x",
				"  cost: 7",
				"drinks:",
				"  - item: L",
				"    milk: *oat",
			),
		},
		"direct edit, then a move of the anchored value, then an edit through the copy at its alias": {
			// The move writes no copy, so the first alias would come before the
			// anchor.
			before: drinks,
			path:   paths.Doc(),
			opts:   []edit.ApplyOption{label},
			fn:     both(at(cost, set(6)), at(paths.Doc().Child("milk"), move("$.z")), at(firstCost, set(7))),
			err:    "test.yaml:6:11: $.drinks[0].milk: cafe.jacobcolvin.com/v1 moves $.milk to $.z, after which this alias comes before the anchor that it reads, and cafe does not write a copy of a value at its alias",
		},
		"direct edit, then a removal of the anchored value, then an edit through the copy at its alias": {
			// The removal takes the anchor that the first alias reads.
			before: drinks,
			path:   paths.Doc(),
			opts:   []edit.ApplyOption{label},
			fn:     both(at(cost, set(6)), at(paths.Doc().Child("milk"), remove), at(firstCost, set(7))),
			err:    "test.yaml:6:11: $.drinks[0].milk: cafe.jacobcolvin.com/v1 removes $.milk, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"alias edit, then a move of the anchored value, then an edit through the alias at its target": {
			// The move writes no copy, so the first alias would come before the
			// anchor.
			before: drinks,
			path:   paths.Doc(),
			opts:   []edit.ApplyOption{label},
			fn: both(
				at(firstCost, set(6)),
				at(paths.Doc().Child("milk"), move("$.z")),
				at(paths.MustParse("$.z.cost"), set(7)),
			),
			err: "test.yaml:6:11: $.drinks[0].milk: cafe.jacobcolvin.com/v1 moves $.milk to $.z, after which this alias comes before the anchor that it reads, and cafe does not write a copy of a value at its alias",
		},
		"removal of the anchored value, then two edits through the copy at its alias": {
			before: drinks,
			path:   paths.Doc(),
			fn:     both(at(paths.Doc().Child("milk"), remove), at(firstCost, set(6)), at(firstCost, set(7))),
			err:    "test.yaml:6:11: $.drinks[0].milk: the edit removes $.milk, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"alias edit, then its item leaves, then an edit through the next item's alias": {
			// Each call doubles the cost that both aliases read before it removes its
			// item.
			before: drinks,
			path:   paths.MustParse("$.drinks[*]"),
			opts:   []edit.ApplyOption{label},
			fn: func(ctx context.Context, n *edit.Node) error {
				cost := n.At(paths.Current().Child("milk", "cost"))

				v, err := cost.Decode[int](ctx)
				if err != nil {
					return err
				}

				err = cost.Set(ctx, v*2)
				if err != nil {
					return err
				}

				return n.Remove()
			},
			want: stringtest.LinesLF("milk: &oat", "  name: x", "  cost: 20", "drinks: []"),
		},
		"alias edit, then its item moves out, then an edit through the next item's alias": {
			// The last Set reaches the milk through the alias of the item that took
			// the index of the moved one.
			before: drinks,
			path:   paths.Doc(),
			opts:   []edit.ApplyOption{label},
			fn: both(
				at(firstCost, set(6)),
				at(paths.MustParse("$.drinks[0]"), func(_ context.Context, n *edit.Node) error {
					return n.Move(paths.MustParse("$.old"))
				}),
				at(firstCost, set(7)),
			),
			want: stringtest.LinesLF(
				"milk: &oat",
				"  name: x",
				"  cost: 7",
				"drinks:",
				"  - item: L",
				"    milk: *oat",
				"old:",
				"  item: K",
				"  milk: *oat",
			),
		},
		"alias edit, then a rename above it, then an edit through another alias": {
			// The rename keeps the alias, so the last Set changes the milk that the
			// first Set changed.
			before: drinks,
			path:   paths.Doc(),
			opts:   []edit.ApplyOption{label},
			fn: both(
				at(firstCost, set(6)),
				at(paths.MustParse("$.drinks[0].milk"), rename("oat")),
				at(lastCost, set(7)),
			),
			want: stringtest.LinesLF(
				"milk: &oat",
				"  name: x",
				"  cost: 7",
				"drinks:",
				"  - item: K",
				"    oat: *oat",
				"  - item: L",
				"    milk: *oat",
			),
		},
		"one alias path after a rename above it": {
			before: drinks,
			path:   paths.Doc(),
			fn: func(ctx context.Context, n *edit.Node) error {
				cost := n.At(firstCost)

				err := cost.Set(ctx, 6)
				if err != nil {
					return err
				}

				err = n.At(paths.MustParse("$.drinks[0].milk")).SetKey("oat")
				if err != nil {
					return err
				}

				return cost.Set(ctx, 7)
			},
			want: stringtest.LinesLF(
				"milk: &oat",
				"  name: x",
				"  cost: 7",
				"drinks:",
				"  - item: K",
				"    oat: *oat",
				"  - item: L",
				"    milk: *oat",
			),
		},
		"removal of a value whose copy holds an alias that a move wrote": {
			before: stringtest.LinesLF("a:", "  x: &e 1", "  w: *e", "  y: &f [0]", "b: *f"),
			path:   paths.Doc(),
			fn:     both(at(paths.MustParse("$.a.x"), move("$.a.y[1]")), at(paths.MustParse("$.a"), remove)),
			err:    "test.yaml:3:6: $.a.w: the edit moves $.a.x to $.a.y[1], after which this alias comes before the anchor that it reads, and cafe does not write a copy of a value at its alias",
		},
		"one alias path after a move above it": {
			before: drinks,
			path:   paths.Doc(),
			fn: func(ctx context.Context, n *edit.Node) error {
				cost := n.At(firstCost)

				err := cost.Set(ctx, 6)
				if err != nil {
					return err
				}

				err = n.At(paths.MustParse("$.drinks[0]")).Move(paths.MustParse("$.first"))
				if err != nil {
					return err
				}

				return cost.Set(ctx, 7)
			},
			want: stringtest.LinesLF(
				"milk: &oat",
				"  name: x",
				"  cost: 7",
				"drinks:",
				"  - item: L",
				"    milk: *oat",
				"first:",
				"  item: K",
				"  milk: *oat",
			),
		},
		"create through an alias, then set a child through the same alias": {
			before: drinks,
			path:   paths.Doc(),
			fn: both(
				at(paths.MustParse("$.drinks[0].milk.extra"), set(map[string]int{"a": 1, "b": 2})),
				at(paths.MustParse("$.drinks[0].milk.extra.b"), set(3)),
			),
			want: stringtest.LinesLF(
				"milk: &oat",
				"  name: x",
				"  cost: 5",
				"  extra:",
				"    a: 1",
				"    b: 3",
				"drinks:",
				"  - item: K",
				"    milk: *oat",
				"  - item: L",
				"    milk: *oat",
			),
		},
		"set two entries of a value that an alias set": {
			before: drinks,
			path:   paths.Doc(),
			fn: both(
				at(firstCost, set(map[string]int{"a": 1, "b": 2})),
				at(firstCost.Child("a"), set(5)),
				at(firstCost.Child("b"), set(6)),
			),
			want: strings.Replace(drinks, "cost: 5", "cost: {a: 5, b: 6}", 1),
		},
		"value that an alias shares changes twice, block mapping": {
			// The calls act in order, so the Set through the alias wins.
			before: stringtest.LinesLF(
				"base: &b",
				"  m:",
				"    k: 1",
				"x: *b",
			),
			path: paths.Doc(),
			opts: []edit.ApplyOption{label},
			fn: func(ctx context.Context, n *edit.Node) error {
				err := n.At(paths.MustParse("$.base.m")).Set(ctx, 2)
				if err != nil {
					return err
				}

				return n.At(paths.MustParse("$.x.m")).Set(ctx, 3)
			},
			want: stringtest.LinesLF("base: &b", "  m: 3", "x: *b"),
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
			err:  `test.yaml:5:3: $.spec.closes: the edit renames $.spec.closes to "close", but <<: *summer at 4:3 also brings in closes, so $.spec still reads closes`,
		},
		"rename in a merge source that hides an entry of the mapping": {
			before: stringtest.LinesLF(
				"summer: &summer",
				`  shuts: "21:00"`,
				"spec:",
				`  closes: "19:00"`,
				"  <<: *summer",
			),
			path:   paths.MustParse("$.summer.shuts"),
			fn:     func(_ context.Context, n *edit.Node) error { return n.SetKey("closes") },
			err:    "test.yaml:4:3: $.spec.closes: go-yaml's decoder reads $.spec.closes from the merge key at 5:3 in the edited text, but the merge spec keeps the own entry written before it, and the edit renames $.summer.shuts to \"closes\"",
			engine: true,
		},
		"rename in a merge source that hides an entry of a moved mapping": {
			// The message names the entry by its path in the edited text (fix19 B7).
			before: stringtest.LinesLF(
				"summer: &summer",
				`  shuts: "21:00"`,
				"spec:",
				`  closes: "19:00"`,
				"  <<: *summer",
			),
			path:   paths.Doc(),
			fn:     both(at(spec, move("$.z")), at(paths.MustParse("$.summer.shuts"), rename("closes"))),
			err:    "test.yaml:4:3: $.spec.closes: go-yaml's decoder reads $.z.closes from the merge key at 5:3 in the edited text, but the merge spec keeps the own entry written before it, and the edit renames $.summer.shuts to \"closes\"",
			engine: true,
		},
		"rename in a merge source that hides an entry of a mapping moved in its sequence": {
			// The message names the entry by its path in the edited text (fix19 B7).
			before: stringtest.LinesLF(
				"summer: &summer",
				"  shuts: 21",
				"k:",
				"  - closes: 19",
				"    <<: *summer",
				"  - 0",
			),
			path: paths.Doc(),
			fn: both(
				at(paths.MustParse("$.k[0]"), move("$.k[2]")),
				at(paths.MustParse("$.summer.shuts"), rename("closes")),
			),
			err:    "test.yaml:4:5: $.k[0].closes: go-yaml's decoder reads $.k[1].closes from the merge key at 5:5 in the edited text, but the merge spec keeps the own entry written before it, and the edit renames $.summer.shuts to \"closes\"",
			engine: true,
		},
		"rename in a merge source that hides an entry of a mapping moved into a block sequence": {
			// The message names the entry by its path in the edited text (fix19 B7).
			before: stringtest.LinesLF(
				"summer: &summer",
				"  shuts: 21",
				"spec:",
				"  closes: 19",
				"  <<: *summer",
				"l:",
				"  - 0",
			),
			path:   paths.Doc(),
			fn:     both(at(spec, move("$.l[1]")), at(paths.MustParse("$.summer.shuts"), rename("closes"))),
			err:    "test.yaml:4:3: $.spec.closes: go-yaml's decoder reads $.l[1].closes from the merge key at 5:3 in the edited text, but the merge spec keeps the own entry written before it, and the edit renames $.summer.shuts to \"closes\"",
			engine: true,
		},
		"rename in a merge source that hides a key that starts with a dash in a mapping moved into a block sequence": {
			// The message names the entry by its path in the edited text (fix19 B7).
			before: stringtest.LinesLF(
				"summer: &summer",
				"  shuts: 21",
				"spec:",
				"  -closes: 19",
				"  <<: *summer",
				"l:",
				"  - 0",
			),
			path:   paths.Doc(),
			fn:     both(at(spec, move("$.l[1]")), at(paths.MustParse("$.summer.shuts"), rename("-closes"))),
			err:    "test.yaml:4:3: $.spec.-closes: go-yaml's decoder reads $.l[1].-closes from the merge key at 5:3 in the edited text, but the merge spec keeps the own entry written before it, and the edit renames $.summer.shuts to \"-closes\"",
			engine: true,
		},
		"rename in a merge source that hides a key that starts with a dash in a mapping moved in its sequence": {
			// The message names the entry by its path in the edited text (fix19 B7).
			before: stringtest.LinesLF(
				"summer: &summer",
				"  shuts: 21",
				"k:",
				"  - -closes: 19",
				"    <<: *summer",
				"  - 0",
			),
			path: paths.Doc(),
			fn: both(
				at(paths.MustParse("$.k[0]"), move("$.k[2]")),
				at(paths.MustParse("$.summer.shuts"), rename("-closes")),
			),
			err:    "test.yaml:4:5: $.k[0].-closes: go-yaml's decoder reads $.k[1].-closes from the merge key at 5:5 in the edited text, but the merge spec keeps the own entry written before it, and the edit renames $.summer.shuts to \"-closes\"",
			engine: true,
		},
		"rename in a merge source of a mapping that starts with its merge key, moved into a block sequence": {
			before: stringtest.LinesLF(
				"summer: &summer",
				"  shuts: 21",
				"spec:",
				"  <<: *summer",
				"  closes: 19",
				"l:",
				"  - 0",
			),
			path: paths.Doc(),
			fn:   both(at(spec, move("$.l[1]")), at(paths.MustParse("$.summer.shuts"), rename("closes"))),
			want: stringtest.LinesLF(
				"summer: &summer",
				"  closes: 21",
				"l:",
				"  - 0",
				"  - <<: *summer",
				"    closes: 19",
			),
		},
		"rename in a merge source of an entry that the mapping hides": {
			before: stringtest.LinesLF("d: &d", "  port: 80", "svc:", "  <<: *d", "  port: 8080"),
			path:   paths.MustParse("$.d.port"),
			fn:     rename("p"),
			want:   stringtest.LinesLF("d: &d", "  p: 80", "svc:", "  <<: *d", "  port: 8080"),
		},
		"move to a key of a merge source that a key which an earlier move wrote hides": {
			before:  stringtest.LinesLF("base: &b", "  x: 1", "  z: 2", "svc:", "  <<: *b", "  z: 3"),
			earlier: at(paths.MustParse("$.base.z"), move("$.svc.w0")),
			path:    paths.MustParse("$.base.x"),
			fn:      move("$.base.w0"),
			want:    stringtest.LinesLF("base: &b", "  w0: 1", "svc:", "  <<: *b", "  z: 3", "  w0: 2"),
		},
		"move to a key of a flow merge source that a key which an earlier move wrote hides": {
			before:  stringtest.LinesLF("base: &b {x: 1, z: 2}", "svc: {<<: *b, z: 3}"),
			earlier: at(paths.MustParse("$.base.z"), move("$.svc.w0")),
			path:    paths.MustParse("$.base.x"),
			fn:      move("$.base.w0"),
			want:    stringtest.LinesLF("base: &b {w0: 1}", "svc: {<<: *b, z: 3, w0: 2}"),
		},
		"rename to a key that a later merge key brings in": {
			before: stringtest.LinesLF(
				"summer: &summer",
				`  closes: "21:00"`,
				"spec:",
				`  shuts: "19:00"`,
				"  <<: *summer",
			),
			path: spec.Child("shuts"),
			fn:   func(_ context.Context, n *edit.Node) error { return n.SetKey("closes") },
			err:  `test.yaml:4:3: $.spec.shuts: the edit renames $.spec.shuts to "closes", but <<: *summer at 5:3 also brings in closes, so $.spec then hides the closes of $.summer`,
		},
		"rename to a key that an earlier merge key brings in": {
			before: stringtest.LinesLF(
				"summer: &summer",
				`  closes: "21:00"`,
				"spec:",
				"  <<: *summer",
				`  shuts: "19:00"`,
			),
			path: spec.Child("shuts"),
			fn:   func(_ context.Context, n *edit.Node) error { return n.SetKey("closes") },
			err:  `test.yaml:5:3: $.spec.shuts: the edit renames $.spec.shuts to "closes", but <<: *summer at 4:3 also brings in closes, so $.spec then hides the closes of $.summer`,
		},
		"new key through an alias that a later merge key of the mapping on the path brings in": {
			before: stringtest.LinesLF("d: &d {j: 1}", "m:", "  a:", "    s: *d", "  <<: *d"),
			path:   paths.Doc(),
			fn:     at(paths.MustParse("$.m.a.s.a"), set("q")),
			err:    "test.yaml:3:3: $.m.a: go-yaml's decoder reads $.m.a from the merge key at 5:3 in the edited text, but the merge spec keeps the own entry written before it, and the edit sets $.m.a.s.a",
			engine: true,
		},
		"new key through the anchor that a later merge key of a mapping brings in": {
			before: stringtest.LinesLF("d: &d {j: 1}", "m:", "  a:", "    s: *d", "  <<: *d"),
			path:   paths.Doc(),
			fn:     at(paths.MustParse("$.d.a"), set("q")),
			err:    "test.yaml:3:3: $.m.a: go-yaml's decoder reads $.m.a from the merge key at 5:3 in the edited text, but the merge spec keeps the own entry written before it, and the edit sets $.d.a",
			engine: true,
		},
		"new key of an anchored entry that a later merge key of its mapping brings in": {
			before: stringtest.LinesLF("r1:", "  a: &x1 {c: q}", "  <<: *x1"),
			path:   paths.Doc(),
			fn:     at(paths.MustParse("$.r1.a.a"), set("z")),
			err:    "test.yaml:2:3: $.r1.a: go-yaml's decoder reads $.r1.a from the merge key at 3:3 in the edited text, but the merge spec keeps the own entry written before it, and the edit sets $.r1.a.a",
			engine: true,
		},
		"new key through an alias that a later merge key brings in over the alias, then a rename of the key": {
			// The message names the entry by its path in the edited text (fix19 B7).
			before: stringtest.LinesLF("x: &x {b: q}", "r1: *x", "<<: *x"),
			path:   paths.Doc(),
			fn: both(
				at(paths.Doc().Child("r1"), rename("z")),
				at(paths.MustParse("$.z.z"), set(5)),
				at(paths.Doc().Child("z"), rename("n")),
			),
			err:    "2:1: $.r1: go-yaml's decoder reads $.z from the merge key at 3:1 in the edited text, but the merge spec keeps the own entry written before it, and the edit sets $.z.z",
			engine: true,
		},
		"new key of an anchored entry that a later merge key brings in over the entry, then a removal of the key": {
			// The message names the entry by its path in the edited text (fix19 B7).
			before: stringtest.LinesLF("r0: &x0", "  c: 1", "<<: *x0"),
			path:   paths.Doc(),
			fn: both(
				at(paths.Doc().Child("r0"), rename("z")),
				at(paths.MustParse("$.z.z"), set("y\n z\n")),
				at(paths.Doc().Child("z"), remove),
			),
			err:    "1:1: $.r0: go-yaml's decoder reads $.z from the merge key at 3:1 in the edited text, but the merge spec keeps the own entry written before it, and the edit sets $.z.z",
			engine: true,
		},
		"new key through a renamed alias that a later merge key brings in over the alias, with a value that go-yaml's encoder misreads": {
			// The call creates the place, so the problem binds at the nearest value above it that the user
			// wrote, under its path (fix19 G1-1): at the alias, as an error at the value of the renamed key
			// binds (fix20 B2), with the path of the place in the anchored mapping in the detail (fix19 F6).
			before: stringtest.LinesLF("x: &x", "  b: 1", "", "r1: *x", "<<: *x"),
			path:   paths.Doc(),
			fn: both(
				at(paths.Doc().Child("r1"), rename("z")),
				at(paths.MustParse("$.z.z"), set(misread)),
			),
			err: `4:5: $.r1: go-yaml's encoder writes the float 1e+06 as text that go-yaml's decoder reads as "1e+06"` +
				"\n|-- report this to the authors of cafe\n`-- the edit sets $.z.z",
			engine: true,
		},
		"new key that a later merge key brings in over the entry of an earlier set, then a rename of the key": {
			before: stringtest.LinesLF("r0: &x0", "  <<: &x1 {}", "<<: [*x0, *x1]"),
			path:   paths.Doc(),
			fn: both(
				at(paths.Doc().Child("r0"), rename("n")),
				at(paths.MustParse("$.n.z"), set(1)),
				at(paths.MustParse("$.n.n"), set("q")),
				at(paths.Doc().Child("n"), rename("k")),
			),
			err: `test.yaml:3:1: $: the edit renames $.r0 to "n", which changes the keys of a merge through a merge key that merges a sequence of aliases, and cafe does not change the keys of such a merge`,
		},
		"new key that a later merge key brings in over the entry of an earlier set, then a removal of the key": {
			before: stringtest.LinesLF("r0: &x0", "  <<: &x1 {}", "<<: [*x0, *x1]"),
			path:   paths.Doc(),
			fn: both(
				at(paths.Doc().Child("r0"), rename("n")),
				at(paths.MustParse("$.n.z"), set(1)),
				at(paths.MustParse("$.n.n"), set("q")),
				at(paths.Doc().Child("n"), remove),
			),
			err: `test.yaml:3:1: $: the edit renames $.r0 to "n", which changes the keys of a merge through a merge key that merges a sequence of aliases, and cafe does not change the keys of such a merge`,
		},
		"new key below a renamed key that a later merge key brings in, with a value that go-yaml's encoder misreads": {
			// The problem binds where the user wrote the renamed key, under the
			// path that the user's text spells.
			before: stringtest.LinesLF("r0: &x0", "  <<: &x1 {}", "", "<<: [*x0, *x1]"),
			path:   paths.Doc(),
			fn: both(
				at(paths.Doc().Child("r0"), rename("n")),
				at(paths.MustParse("$.n.z"), set(misread)),
			),
			err: `test.yaml:4:1: $: the edit renames $.r0 to "n", which changes the keys of a merge through a merge key that merges a sequence of aliases, and cafe does not change the keys of such a merge`,
		},
		"rename of a node on a merged entry to a key of the mapping, then a rename of the node": {
			before: stringtest.LinesLF("m:", "  <<: {c: 1}", "  a: 'y'"),
			path:   paths.MustParse("$.m.c"),
			fn:     both(rename("a"), rename("c")),
			err:    `test.yaml:2:3: $.m: the edit renames $.m.c to "a", which goes through a merge key that merges a mapping written in place, and cafe does not edit through such a merge key`,
		},
		"rename of a node on a merged entry to a key of the mapping, then a removal of the node": {
			before: stringtest.LinesLF("m:", "  <<: {c: 1}", "  a: 'y'"),
			path:   paths.MustParse("$.m.c"),
			fn:     both(rename("a"), remove),
			err:    `test.yaml:2:3: $.m: the edit renames $.m.c to "a", which goes through a merge key that merges a mapping written in place, and cafe does not edit through such a merge key`,
		},
		"node inside a merged entry that a rename hides, then a rename of the hiding entry and a set": {
			before: stringtest.LinesLF("m:", "  <<: {a: {d: q}}", "  c: {k: 1}"),
			path:   paths.MustParse("$.m.a.d"),
			fn: func(ctx context.Context, n *edit.Node) error {
				err := n.At(paths.MustParse("$.m.c")).SetKey("a")
				if err != nil {
					return err
				}

				err = n.At(paths.MustParse("$.m.a")).SetKey("k0")
				if err != nil {
					return err
				}

				// The node reads another value than its place after the first
				// rename, so the second rename leaves its path, which reads
				// its place again.
				return n.Set(ctx, "y")
			},
			err: `test.yaml:2:3: $.m: the edit renames $.m.c to "a", which changes the keys of a merge through a merge key that merges a mapping written in place, and cafe does not change the keys of such a merge`,
		},
		"node inside an entry that a rename lets a merged entry hide, then a removal of the merged entry and a set": {
			// After the rename, $.n reads the n that the merge brings in. The
			// removal takes that entry away, and the node keeps its place,
			// which $.n.b reads again.
			before: stringtest.LinesLF("16: &k {n: x, b: 1}", "<<: *k"),
			path:   paths.MustParse("$.16.b"),
			fn: func(ctx context.Context, n *edit.Node) error {
				err := n.At(paths.MustParse("$.16")).SetKey("n")
				if err != nil {
					return err
				}

				err = n.At(paths.MustParse("$.n")).Remove()
				if err != nil {
					return err
				}

				return n.Set(ctx, 2)
			},
			err: `test.yaml:1:1: $.16: the edit renames $.16 to "n", but <<: *k at 2:1 also brings in n, so $ then hides the n of $.16`,
		},
		"rename of a merged entry to a key of the mapping, then a rename of that key": {
			before: stringtest.LinesLF("m:", "  <<: {c: 1}", "  a: 'y'"),
			path:   paths.Doc(),
			fn:     both(at(paths.MustParse("$.m.c"), rename("a")), at(paths.MustParse("$.m.a"), rename("b"))),
			err:    `test.yaml:2:3: $.m: the edit renames $.m.c to "a", which goes through a merge key that merges a mapping written in place, and cafe does not edit through such a merge key`,
		},
		"value that a rename hides behind a key of the mapping, until a rename of that key": {
			before: stringtest.LinesLF("<<:", "  a: 1"),
			path:   paths.Doc(),
			fn: both(
				at(paths.Doc().Child("a"), set("x\n")),
				at(paths.Doc().Child("z"), set(1)),
				at(paths.Doc().Child("a"), rename("z")),
				at(paths.Doc().Child("z"), rename("n")),
			),
			err: "test.yaml:1:1: $: the edit sets $.a, which goes through a merge key that merges a mapping written in place, and cafe does not edit through such a merge key",
		},
		"value that a rename hides behind a key of the mapping, which its anchor still reads": {
			before: stringtest.LinesLF("s: &s", "  c: 1", "", "m:", "  <<: *s", "  a: y"),
			path:   paths.Doc(),
			fn: both(
				at(paths.MustParse("$.m.c"), set(spaced)),
				at(paths.MustParse("$.m.c"), rename("a")),
				at(paths.MustParse("$.m.a"), rename("b")),
			),
			err: `test.yaml:6:3: $.m.a: the edit renames $.m.a to "b", but <<: *s at 5:3 also brings in a, so $.m still reads a`,
		},
		"node on an entry that a rename of a merged entry hides, then a set of the node": {
			before: stringtest.LinesLF("m:", "  a: 1", "  <<: {b: 2}"),
			path:   paths.MustParse("$.m.a"),
			fn: func(ctx context.Context, n *edit.Node) error {
				err := n.At(paths.MustParse("$.m.b")).SetKey("a")
				if err != nil {
					return err
				}

				return n.Set(ctx, 3)
			},
			// The node keeps its own entry, so the set acts on it. The check
			// of the merges finds the rename that hides it.
			err: `test.yaml:3:3: $.m: the edit renames $.m.b to "a", which goes through a merge key that merges a mapping written in place, and cafe does not edit through such a merge key`,
		},
		"value that a rename of a merged entry hides, until a rename of that entry": {
			before: stringtest.LinesLF("r0:", `  a: "a: b"`, "  <<: {b: 2}"),
			path:   paths.Doc(),
			fn: both(
				at(paths.MustParse("$.r0.a"), set("v")),
				at(paths.MustParse("$.r0.b"), rename("a")),
				at(paths.MustParse("$.r0.a"), rename("z")),
			),
			err: `test.yaml:3:3: $.r0: the edit renames $.r0.b to "a", which goes through a merge key that merges a mapping written in place, and cafe does not edit through such a merge key`,
		},
		"value that a rename of a key of the mapping hides, until a removal of that key": {
			before: stringtest.LinesLF("<<: {b: {d: 1}}", "r1: a b"),
			path:   paths.Doc(),
			fn: both(
				at(paths.MustParse("$.b.d"), set("y\n z\n")),
				at(paths.Doc().Child("r1"), rename("b")),
				at(paths.Doc().Child("b"), remove),
			),
			err: "test.yaml:1:1: $: the edit sets $.b.d, which goes through a merge key that merges a mapping written in place, and cafe does not edit through such a merge key",
		},
		"value that a rename hides behind a later merge key, until a rename of the merged entry": {
			before: stringtest.LinesLF("r0: 1", "<<: {d: 2}"),
			path:   paths.Doc(),
			fn: both(
				at(paths.Doc().Child("r0"), set("x")),
				at(paths.Doc().Child("r0"), rename("d")),
				at(paths.Doc().Child("d"), rename("z")),
			),
			err: `test.yaml:2:1: $: the edit renames $.r0 to "d", which changes the keys of a merge through a merge key that merges a mapping written in place, and cafe does not change the keys of such a merge`,
		},
		"string with blank lines at its end, set twice over a value with a comment after it": {
			// The first Set writes the comment after the indicator of the block
			// scalar, and the second one keeps it there.
			before: stringtest.LinesLF("a: x # note", "b: 1"),
			path:   paths.Doc(),
			fn:     both(at(paths.Doc().Child("a"), set("q\n\n")), at(paths.Doc().Child("a"), set("w\n\n"))),
			want:   stringtest.LinesLF("a: |+ # note", "  w", "", "b: 1"),
		},
		"string over a block scalar that keeps its blank lines, then a new key with blank lines": {
			// The blank line of the old block scalar goes with it, so the block
			// scalar of the new entry does not read it.
			before: stringtest.LinesLF("a: |+", "  x", ""),
			path:   paths.Doc(),
			fn:     both(at(paths.Doc().Child("a"), set("y\n z\n")), at(paths.Doc().Child("m"), set("q\n\n"))),
			want:   stringtest.LinesLF("a: |", "  y", "   z", "m: |+", "  q", ""),
		},
		"string of several lines over a value with a comment after it, then renames through a merge key": {
			// The comment goes on the line of the indicator of the block
			// scalar. The last rename reaches the merged entry k0, which the
			// merge key after the own entry k0 brings in.
			before: stringtest.LinesLF("r0: [x] # f", "<<: {d: 2}"),
			path:   paths.Doc(),
			fn: both(
				at(paths.Doc().Child("r0"), set("p\nq")),
				at(paths.Doc().Child("d"), rename("k0")),
				at(paths.Doc().Child("r0"), rename("k0")),
				at(paths.Doc().Child("k0"), rename("z")),
			),
			err: `test.yaml:2:1: $: the edit renames $.d to "k0", which goes through a merge key that merges a mapping written in place, and cafe does not edit through such a merge key`,
		},
		"value that a removal leaves where a merge key hides it, until a rename of the merged entry": {
			before: stringtest.LinesLF("r: &x", "  k: 1", "al: *x", "<<: {s: 0}"),
			path:   paths.Doc(),
			fn: both(
				at(paths.MustParse("$.al.k"), set("q")),
				at(paths.Doc().Child("s"), rename("r")),
				at(paths.Doc().Child("al"), remove),
				at(paths.Doc().Child("r"), rename("s")),
			),
			err: `test.yaml:4:1: $: the edit renames $.s to "r", which goes through a merge key that merges a mapping written in place, and cafe does not edit through such a merge key`,
		},
		"node on a merged entry that a rename of another entry hides, then a removal of the node": {
			before: stringtest.LinesLF("s: &s {b: 1}", "m:", "  <<: *s", "  c: 2"),
			path:   paths.MustParse("$.s.b"),
			fn: func(ctx context.Context, n *edit.Node) error {
				err := n.At(paths.MustParse("$.m.c")).SetKey("b")
				if err != nil {
					return err
				}

				return n.Remove()
			},
			err: `test.yaml:4:3: $.m.c: the edit renames $.m.c to "b", but <<: *s at 3:3 also brings in b, so $.m then hides the b of $.s`,
		},
		"node below a moved anchored value that a rename of the alias of the value hides, then a rename of the node": {
			before: stringtest.LinesLF("r0: &x0", "  k: w", "<<: *x0"),
			path:   paths.MustParse("$.r0.k"),
			fn: func(ctx context.Context, n *edit.Node) error {
				err := n.At(paths.Doc().Child("r0")).Move(paths.Doc().Child("z"))
				if err != nil {
					return err
				}

				err = n.At(paths.Doc().Child("z")).SetKey("k")
				if err != nil {
					return err
				}

				return n.SetKey("b")
			},
			err: "test.yaml:3:5: $: the edit moves $.r0 to $.z, after which the merge key at 3:1 of $ comes before the anchor that it reads, and cafe does not write a copy of a value at its alias",
		},
		"value below an entry that a rename in a merged mapping hides, then a removal of the merged entry": {
			before: stringtest.LinesLF("n: &x", "  c: 1", "<<: *x"),
			path:   paths.Doc(),
			fn: both(
				at(paths.MustParse("$.n.z"), set(5)),
				at(paths.MustParse("$.n.c"), rename("n")),
				at(paths.Doc().Child("n"), remove),
			),
			err:    `1:1: $.n: go-yaml's decoder reads $.n from the merge key at 3:1 in the edited text, but the merge spec keeps the own entry written before it, and the edit renames $.n.c to "n"`,
			engine: true,
		},
		"value below an entry that a rename in a merged mapping hides, then a rename of the merged entry": {
			before: stringtest.LinesLF("n: &x", "  c: 1", "<<: *x"),
			path:   paths.Doc(),
			fn: both(
				at(paths.MustParse("$.n.z"), set(5)),
				at(paths.MustParse("$.n.c"), rename("n")),
				at(paths.Doc().Child("n"), rename("c")),
			),
			err:    `1:1: $.n: go-yaml's decoder reads $.n from the merge key at 3:1 in the edited text, but the merge spec keeps the own entry written before it, and the edit renames $.n.c to "n"`,
			engine: true,
		},
		"node on an entry that a rename in a merged mapping hides, then a set of the node": {
			before: stringtest.LinesLF("n: &x", "  c: 1", "<<: *x"),
			path:   paths.Doc().Child("n"),
			fn: func(ctx context.Context, n *edit.Node) error {
				err := n.At(paths.MustParse("$.n.c")).SetKey("n")
				if err != nil {
					return err
				}

				return n.Set(ctx, 3)
			},
			err:    `1:1: $.n: go-yaml's decoder reads $.n from the merge key at 3:1 in the edited text, but the merge spec keeps the own entry written before it, and the edit renames $.n.c to "n"`,
			engine: true,
		},
		"document that its own decode rejects": {
			before: mergeLists(9),
			path:   paths.Doc(),
			fn:     func(context.Context, *edit.Node) error { return nil },
			err:    "test.yaml:28:8: aliases expand the document past the alias limit",
			errIs:  niceyaml.ErrExcessiveAliasing,
		},
		"removal in a document past the alias limit": {
			// The calls do not run, so the removal of the alias that takes the
			// document past the limit fails too.
			before: mergeLists(9),
			path:   paths.MustParse("$.m9"),
			fn:     remove,
			err:    "test.yaml:28:8: aliases expand the document past the alias limit",
			errIs:  niceyaml.ErrExcessiveAliasing,
		},
		"removal in a document past the alias limit, which does not apply": {
			before:    mergeLists(9),
			path:      paths.MustParse("$.m9"),
			fn:        remove,
			want:      mergeLists(8),
			unlimited: true,
		},
		"item that a removal leaves in a copy that a merge key hides, after a rename of the merged entry": {
			before: stringtest.LinesLF("r0:", "  b: &x1", "    - 1", "", "  e: *x1", "  <<: {c: x}"),
			path:   paths.Doc(),
			fn: both(
				at(paths.MustParse("$.r0.e"), rename("c")),
				at(paths.MustParse("$.r0.b[0]"), set(spaced)),
				at(paths.MustParse("$.r0.b"), remove),
				at(paths.MustParse("$.r0.c"), rename("d")),
			),
			err: `test.yaml:6:3: $.r0: the edit renames $.r0.e to "c", which changes the keys of a merge through a merge key that merges a mapping written in place, and cafe does not change the keys of such a merge`,
		},
		"item that a removal leaves in a copy that a merge key hides, after a removal of the merged entry": {
			before: stringtest.LinesLF("r0:", "  b: &x1", "    - 1", "", "  e: *x1", "  <<: {c: x}"),
			path:   paths.Doc(),
			fn: both(
				at(paths.MustParse("$.r0.e"), rename("c")),
				at(paths.MustParse("$.r0.b[0]"), set(spaced)),
				at(paths.MustParse("$.r0.b"), remove),
				at(paths.MustParse("$.r0.c"), remove),
			),
			err: `test.yaml:6:3: $.r0: the edit renames $.r0.e to "c", which changes the keys of a merge through a merge key that merges a mapping written in place, and cafe does not change the keys of such a merge`,
		},
		"removals of anchored values whose tags come before their anchors, a flow mapping and a block sequence": {
			// The removal of a takes the anchor that c reads.
			before: stringtest.LinesLF("a: !t &x {k: 1}", "b: !u &y", "  - 1", "c: *x", "d: *y"),
			path:   paths.Doc(),
			fn:     both(at(paths.Doc().Child("a"), remove), at(paths.Doc().Child("b"), remove)),
			err:    "test.yaml:4:4: $.c: the edit removes $.a, which holds the anchor &x that this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"removals of anchored values whose tags come before their anchors, a block mapping and a flow sequence": {
			before: stringtest.LinesLF("a: !t &x", "  k: 1", "b: !u &y [1]", "c: [*x]", "d: *y"),
			path:   paths.Doc(),
			fn:     both(at(paths.Doc().Child("a"), remove), at(paths.Doc().Child("b"), remove)),
			err:    "test.yaml:4:5: $.c[0]: the edit removes $.a, which holds the anchor &x that this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"sets over anchored values whose tags come before their anchors, a flow mapping and a block sequence": {
			before: stringtest.LinesLF("a: !t &x {k: 1}", "b: !u &y", "  - 1", "c: *x", "d: *y"),
			path:   paths.Doc(),
			fn:     both(at(paths.Doc().Child("a"), set(5)), at(paths.Doc().Child("b"), set(6))),
			want:   stringtest.LinesLF("a: &x 5", "b: &y 6", "c: *x", "d: *y"),
		},
		"sets over anchored values whose tags come before their anchors, a block mapping and a flow sequence": {
			before: stringtest.LinesLF("a: !t &x", "  k: 1", "b: !u &y [1]", "c: [*x]", "d: *y"),
			path:   paths.Doc(),
			fn:     both(at(paths.Doc().Child("a"), set(5)), at(paths.Doc().Child("b"), set(6))),
			want:   stringtest.LinesLF("a: &x 5", "b: &y 6", "c: [*x]", "d: *y"),
		},
		"moves of anchored values whose tags come before their anchors, a flow mapping and a block sequence": {
			// The move writes no copy, so *x would come before its anchor.
			before: stringtest.LinesLF("a: !t &x {k: 1}", "b: !u &y", "  - 1", "c: *x", "d: *y"),
			path:   paths.Doc(),
			fn:     both(at(paths.Doc().Child("a"), move("$.z")), at(paths.Doc().Child("b"), move("$.w"))),
			err:    "test.yaml:4:4: $.c: the edit moves $.a to $.z, after which this alias comes before the anchor that it reads, and cafe does not write a copy of a value at its alias",
		},
		"moves of anchored values whose tags come before their anchors, a block mapping and a flow sequence": {
			before: stringtest.LinesLF("a: !t &x", "  k: 1", "b: !u &y [1]", "c: [*x]", "d: *y"),
			path:   paths.Doc(),
			fn:     both(at(paths.Doc().Child("a"), move("$.z")), at(paths.Doc().Child("b"), move("$.w"))),
			err:    "test.yaml:4:5: $.c[0]: the edit moves $.a to $.z, after which this alias comes before the anchor that it reads, and cafe does not write a copy of a value at its alias",
		},
		"removal of a flow item whose tag comes before its anchor, which a later item aliases": {
			before: "s: [!t &x [1], *x]\n",
			path:   paths.MustParse("$.s[0]"),
			fn:     remove,
			err:    "test.yaml:1:16: $.s[1]: the edit removes $.s[0], which holds the anchor &x that this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"set inside a value whose keys repeat a name, before renames of a merged entry onto its key and away": {
			before: stringtest.LinesLF("m:", "  k: 0", "  <<: {j: 1}"),
			path:   paths.Doc(),
			fn: both(
				at(paths.MustParse("$.m.k"), set(map[string]any{"a": map[string]any{"a": 1}})),
				at(paths.MustParse("$.m.k.a.a"), set("v")),
				at(paths.MustParse("$.m.j"), rename("k")),
				at(paths.MustParse("$.m.k"), rename("z")),
			),
			err: `test.yaml:3:3: $.m: the edit renames $.m.j to "k", which goes through a merge key that merges a mapping written in place, and cafe does not edit through such a merge key`,
		},
		"set inside items whose keys repeat names, before a rename of a merged entry onto their key and a removal": {
			before: stringtest.LinesLF("spec:", "  containers: []", "  <<: {x: 1}"),
			path:   paths.Doc(),
			fn: both(
				at(paths.MustParse("$.spec.containers"), set([]any{
					map[string]any{"name": "a", "image": "i"},
					map[string]any{"name": "b", "image": "j"},
				})),
				at(paths.MustParse("$.spec.containers[1].image"), set("k")),
				at(paths.MustParse("$.spec.x"), rename("containers")),
				at(paths.MustParse("$.spec.containers"), remove),
			),
			err: `test.yaml:3:3: $.spec: the edit renames $.spec.x to "containers", which goes through a merge key that merges a mapping written in place, and cafe does not edit through such a merge key`,
		},
		"set inside a value whose keys repeat a name, before a set and a removal of its key in a merge source": {
			before: stringtest.LinesLF("s: &s {}", "m:", "  k: 0", "  <<: *s"),
			path:   paths.Doc(),
			fn: both(
				at(paths.MustParse("$.m.k"), set(map[string]any{"a": map[string]any{"a": 1}})),
				at(paths.MustParse("$.m.k.a.a"), set("v")),
				at(paths.MustParse("$.s.k"), set(5)),
				at(paths.MustParse("$.s.k"), remove),
			),
			err:    "3:3: $.m.k: go-yaml's decoder reads $.m.k from the merge key at 4:3 in the edited text, but the merge spec keeps the own entry written before it, and the edit sets $.s.k",
			engine: true,
		},
		"set of a value whose keys repeat a name through a moved alias of a merge source, before a removal of the alias": {
			before: stringtest.JoinCRLF("<<: &x0 {}", "# c", "r1: *x0", ""),
			path:   paths.Doc(),
			fn: both(
				at(paths.MustParse("$.r1"), rename("k0")),
				at(paths.MustParse("$.k0"), move("$.z")),
				at(paths.MustParse("$.z.z"), set(map[string]any{"z": 1})),
				at(paths.MustParse("$.z"), remove),
			),
			err: `test.yaml:1:1: $: the edit renames $.r1 to "k0", which changes the keys of a merge through a merge key whose value has an anchor, and cafe does not change the keys of such a merge`,
		},
		"removal of an anchored value whose tag comes before its anchor": {
			// The removal takes the anchor that the alias reads.
			before: stringtest.LinesLF("a: !t &x", "  k: 1", "b: [*x]"),
			path:   paths.Doc().Child("a"),
			fn:     remove,
			err:    "test.yaml:3:5: $.b[0]: the edit removes $.a, which holds the anchor &x that this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"set and removal of a new key after a block scalar that a quoted text replaced": {
			// The new key gives the line of spaces at the end of the scalar a
			// line break, which changes the string of the scalar.
			before: stringtest.JoinLF("a: |+", "  x", "", "   "),
			path:   paths.Doc(),
			fn:     both(at(paths.Doc().Child("b"), set(1)), at(paths.Doc().Child("b"), remove)),
			err:    "test.yaml:1:4: $.a: " + endsText + "sets $.b",
		},
		"removal of the last key after a block scalar that a set wrote, in a text without a final newline": {
			// The removal takes the line break before the key, and the scalar
			// that the set wrote takes it back.
			before: stringtest.JoinLF("a: 1", "b: 2"),
			path:   paths.Doc(),
			fn:     both(at(paths.Doc().Child("a"), set("x\n")), at(paths.Doc().Child("b"), remove)),
			want:   stringtest.LinesLF("a: |", "  x"),
		},
		"set and removal of a new entry after a deeper comment line of the entry before it": {
			// The comment line belongs to a, so the removal of b keeps it.
			before: stringtest.LinesLF("a: x", "  # about a"),
			path:   paths.Doc(),
			fn:     both(at(paths.Doc().Child("b"), set(1)), at(paths.Doc().Child("b"), remove)),
			want:   stringtest.LinesLF("a: x", "  # about a"),
		},
		"set of an anchored quoted scalar of several lines with an empty line, whose alias is at the root": {
			before: stringtest.LinesLF("a: &x0 'p", "", "  q'", "b: *x0"),
			path:   paths.Doc().Child("a"),
			fn:     set("x"),
			want:   stringtest.LinesLF("a: &x0 'x'", "b: *x0"),
		},
		"set of an anchored double-quoted scalar of several lines with an empty line, whose alias is at the root": {
			before: stringtest.LinesLF(`a: &x0 "p`, "", `  q"`, "b: *x0"),
			path:   paths.Doc().Child("a"),
			fn:     set("x"),
			want:   stringtest.LinesLF(`a: &x0 "x"`, "b: *x0"),
		},
		"move whose edited text go-yaml reads otherwise, which does not read back": {
			before: unread,
			path:   paths.Doc(),
			fn:     differ,
			err:    recursive,
		},
		"move whose edited text go-yaml reads otherwise, which a later set mends": {
			before: unread,
			path:   paths.Doc(),
			fn:     both(differ, mend),
			err:    recursive,
		},
		"move that copies a scalar that go-yaml reads without its tag": {
			// The move writes the text of b with *y inside the value of &y.
			before: unread,
			path:   paths.Doc(),
			fn:     at(paths.Doc().Child("b"), move("$.a.q")),
			err:    recursive,
		},
		"set beside a scalar that go-yaml reads without its tag": {
			before: unread,
			path:   paths.Doc().Child("c"),
			fn:     set(2),
			want:   stringtest.LinesLF("a: &y {c: &k !t 1}", "b: &x {a: *y, e: 1}", "c: 2"),
		},
		"decode of a value that differs from the model, between a move and a set": {
			before: unread,
			path:   paths.Doc(),
			fn: both(differ, at(paths.MustParse("$.a.q.a.c"), func(ctx context.Context, n *edit.Node) error {
				_, _ = n.Decode[any](ctx) //nolint:errcheck // The Func ignores the error, as some do.

				return nil
			}), mend),
			err: recursive,
		},
		"text of a value that differs from the model": {
			before: unread,
			path:   paths.Doc(),
			fn:     both(differ, at(paths.MustParse("$.a.q.a.c"), reads(text, "")), mend),
			err:    recursive,
		},
		"kind of a value that differs from the model": {
			before: unread,
			path:   paths.Doc(),
			fn:     both(differ, at(paths.MustParse("$.a.q.a.c"), reads(kind, niceyaml.NodeNone)), mend),
			err:    recursive,
		},
		"exists of a value that differs from the model": {
			before: unread,
			path:   paths.Doc(),
			fn:     both(differ, at(paths.MustParse("$.a.q.a.c"), reads(exists, false)), mend),
			err:    recursive,
		},
		"text above a value that differs from the model": {
			before: unread,
			path:   paths.Doc(),
			fn:     both(differ, at(paths.Doc().Child("a"), reads(text, "")), mend),
			err:    recursive,
		},
		"kind above a value that differs from the model": {
			before: unread,
			path:   paths.Doc(),
			fn:     both(differ, at(paths.Doc().Child("a"), reads(kind, niceyaml.NodeNone)), mend),
			err:    recursive,
		},
		"exists above a value that differs from the model": {
			before: unread,
			path:   paths.Doc(),
			fn:     both(differ, at(paths.Doc().Child("a"), reads(exists, false)), mend),
			err:    recursive,
		},
		"exists of an alias of a value that holds a value that differs from the model": {
			before: stringtest.LinesLF("a: &y {c: &k !t 1}", "b: &x {a: *y, e: 1}", "c: !t []", "w: *y"),
			path:   paths.Doc(),
			fn:     both(differ, at(paths.Doc().Child("w"), reads(exists, false)), mend),
			err:    recursive,
		},
		"reads beside a value that differs from the model": {
			before: stringtest.LinesLF("a: &y {c: &k !t 1}", "b: &x {a: *y, e: 1}", "c: !t []", "d: 2"),
			path:   paths.Doc(),
			fn: both(
				differ,
				at(paths.Doc().Child("d"), reads(text, "2")),
				at(paths.Doc().Child("c"), reads(kind, niceyaml.NodeSequence)),
				at(paths.Doc().Child("c"), reads(exists, true)),
				mend,
			),
			err: recursive,
		},
		"decode of the root while a value differs from the model": {
			before: unread,
			path:   paths.Doc(),
			fn: both(differ, func(ctx context.Context, n *edit.Node) error {
				_, _ = n.Decode[any](ctx) //nolint:errcheck // The Apply fails with the error anyway.

				return nil
			}, mend),
			err: recursive,
		},
		"exists of the root while a value differs from the model": {
			before: unread,
			path:   paths.Doc(),
			fn:     both(differ, reads(exists, false), mend),
			err:    recursive,
		},
		"set over a block scalar with a line of spaces that an earlier set wrote": {
			// The second set replaces the scalar, and the final newline that
			// the scalar gave the text stays.
			before: "a: 1",
			path:   paths.Doc().Child("a"),
			fn:     both(set(spaced), set(5)),
			want:   "a: 5\n",
		},
		"value that go-yaml's encoder misreads after a root item without a value": {
			// The call creates the place, so the problem binds at the nearest value above it that the user
			// wrote, under its path (fix19 G1-1).
			// The first item has no text, so the problem at the root binds at
			// its "-".
			before: "-",
			path:   paths.Doc(),
			fn:     at(paths.MustParse("$[1]"), set(misread)),
			err:    `1:1: $: go-yaml's encoder writes the float 1e+06 as text that go-yaml's decoder reads as "1e+06"`,
			engine: true,
		},
		"removal through a merge list after a set copies an anchor that the list reads twice": {
			before: stringtest.LinesLF("r0: &x0", "  <<: &x1 {c: q}", "<<: [*x1, *x0]"),
			path:   paths.Doc(),
			fn:     both(at(paths.Doc().Child("r0"), set("x")), at(paths.Doc().Child("c"), remove)),
			err:    "test.yaml:3:6: $: the edit sets $.r0, which holds the anchor &x1 that the merge key at 3:1 of $ reads, and cafe does not write a copy of a value at its alias",
		},
		"removal of an alias of an anchored value, then of the value, whose copies hold an inner anchor": {
			// Probe finding #22 of verify 12. The removal of k1 takes &f, which
			// *f at k2 reads, in either order of the two removals.
			before: stringtest.LinesLF("k0: 8", "k1: &g [&f 6]", "k2: *f", "k3: *g", "k4: &e [*g]", "k5: 5"),
			path:   paths.Doc(),
			fn:     both(at(paths.Doc().Child("k3"), remove), at(paths.Doc().Child("k1"), remove)),
			err:    "test.yaml:3:5: $.k2: the edit removes $.k1, which holds the anchor &f that this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"removal of an anchored value, then of an alias of it, whose copies hold an inner anchor": {
			// The other order of the removals fails the same way.
			before: stringtest.LinesLF("k0: 8", "k1: &g [&f 6]", "k2: *f", "k3: *g", "k4: &e [*g]", "k5: 5"),
			path:   paths.Doc(),
			fn:     both(at(paths.Doc().Child("k1"), remove), at(paths.Doc().Child("k3"), remove)),
			err:    "test.yaml:3:5: $.k2: the edit removes $.k1, which holds the anchor &f that this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"set that copies an anchor that a merge list reads twice": {
			before: stringtest.LinesLF("r0: &x0", "  <<: &x1 {c: q}", "<<: [*x1, *x0]"),
			path:   paths.Doc().Child("r0"),
			fn:     set("x"),
			err:    "test.yaml:3:6: $: the edit sets $.r0, which holds the anchor &x1 that the merge key at 3:1 of $ reads, and cafe does not write a copy of a value at its alias",
		},
		"removal of an anchored value before a rename to a key that its copy at a merge key brings in": {
			before: stringtest.LinesLF("base: &b {replicas: 1}", "svc:", "  <<: *b", "  name: web"),
			path:   paths.Doc(),
			fn:     both(at(paths.Doc().Child("base"), remove), at(paths.MustParse("$.svc.name"), rename("replicas"))),
			err:    "test.yaml:3:7: $.svc: the edit removes $.base, which the merge key at 3:3 of $.svc reads, and cafe does not write a copy of a value at its alias",
		},
		"move of an anchored value before a rename to a key that its copy at a merge key brings in": {
			before: stringtest.LinesLF("x: &x {b: 1}", "<<: *x", "r3: 0"),
			path:   paths.Doc(),
			fn:     both(at(paths.Doc().Child("x"), move("$.z")), at(paths.Doc().Child("r3"), rename("b"))),
			err:    "test.yaml:2:5: $: the edit moves $.x to $.z, after which the merge key at 2:1 of $ comes before the anchor that it reads, and cafe does not write a copy of a value at its alias",
		},
		"rename of a key that hides a merged entry, before a replacement of the anchored value": {
			before: stringtest.LinesLF("r0: &x0 {c: 1}", "r1:", "  <<: *x0", "  c: 2"),
			path:   paths.Doc(),
			fn:     both(at(paths.MustParse("$.r1.c"), rename("k0")), at(paths.Doc().Child("r0"), set(5))),
			err:    `test.yaml:4:3: $.r1.c: the edit renames $.r1.c to "k0", but <<: *x0 at 3:3 also brings in c, so $.r1 still reads c`,
		},
		"rename of a key of an anchored value that merges, before a set that copies the value and a rename in the copy": {
			// The copy at r2 hides the merged entry that r0 read as b, which
			// the last rename names k0.
			before: stringtest.LinesLF("r0: &x0", "  c:", "  <<: {b: 2}", "  k: w", "r2: *x0"),
			path:   paths.Doc(),
			fn: both(
				at(paths.MustParse("$.r0.c"), rename("k0")),
				at(paths.Doc().Child("r0"), set("y")),
				at(paths.MustParse("$.r2.b"), rename("k0")),
			),
			err: `test.yaml:3:3: $.r0: the edit renames $.r0.c to "k0", which changes the keys of a merge through a merge key that merges a mapping written in place, and cafe does not change the keys of such a merge`,
		},
		"rename in a merge list whose sources hold other keys": {
			// Control: each source holds a key of its own, so the rename
			// reaches the refused form of the merge key.
			before: stringtest.LinesLF("r0: &x0 {b: 2}", "r2: &x1 {c: 1}", "r3:", "  <<: [*x1, *x0]"),
			path:   paths.Doc(),
			fn:     at(paths.MustParse("$.r0.b"), rename("k0")),
			err:    `test.yaml:4:3: $.r3: the edit renames $.r0.b to "k0", which changes the keys of a merge through a merge key that merges a sequence of aliases, and cafe does not change the keys of such a merge`,
		},
		"value that a rename of its holder takes after a set": {
			before: stringtest.LinesLF("a:", "  x: 1", "", "b: 2"),
			path:   paths.Doc(),
			fn:     both(at(paths.MustParse("$.a.y"), set(spaced)), at(paths.Doc().Child("a"), rename("c"))),
			want:   stringtest.LinesLF("c:", "  x: 1", "  \"y\": |", "    a", "     ", "    b", "", "b: 2"),
		},
		"value that a rename takes after a set": {
			before: stringtest.JoinLF("a: x", "b: 1"),
			path:   paths.Doc(),
			fn:     both(at(paths.Doc().Child("b"), set(spaced)), at(paths.Doc().Child("b"), rename("r"))),
			want:   stringtest.LinesLF("a: x", "r: |", "  a", "   ", "  b"),
		},
		"value that a move takes after a set": {
			before: stringtest.LinesLF("a:", "  x: 1", "", "b: 2"),
			path:   paths.Doc(),
			fn:     both(at(paths.MustParse("$.a.y"), set(spaced)), at(paths.MustParse("$.a.y"), move("$.z"))),
			want:   stringtest.LinesLF("a:", "  x: 1", "", "b: 2", "z: |", "  a", "   ", "  b"),
		},
		"entry that a move takes out of a mapping after a set": {
			before: stringtest.LinesLF("c: 1", ""),
			path:   paths.Doc(),
			fn: both(
				at(paths.Doc().Child("d"), set(map[string]any{"y": spaced})),
				at(paths.MustParse("$.d.y"), move("$.m")),
			),
			want: stringtest.LinesLF("c: 1", "d: {}", "m: |", "  a", "   ", "  b", ""),
		},
		"entry that a rename takes in a mapping after a set": {
			before: stringtest.LinesLF("c: 1", ""),
			path:   paths.Doc(),
			fn: both(
				at(paths.Doc().Child("d"), set(map[string]any{"y": "q\n\n", "z": 1})),
				at(paths.MustParse("$.d.y"), rename("r")),
			),
			want: stringtest.LinesLF("c: 1", "d:", `  "r": |+`, "    q", "", "  z: 1", ""),
		},
		"item that a removal before it and a move take after a set": {
			before: stringtest.LinesLF("l:", "  - 1", "  - 2"),
			path:   paths.Doc(),
			fn: both(
				at(paths.MustParse("$.l[1]"), set("q")),
				at(paths.MustParse("$.l[0]"), remove),
				at(paths.MustParse("$.l[0]"), move("$.z")),
			),
			want: stringtest.LinesLF("l: []", "z: q"),
		},
		"mapping with a string of several lines that holds a line of spaces": {
			before: stringtest.LinesLF("c: 1", ""),
			path:   paths.Doc(),
			fn:     at(paths.Doc().Child("d"), set(map[string]any{"y": spaced})),
			want:   stringtest.LinesLF("c: 1", "d:", "  \"y\": |", "    a", "     ", "    b", ""),
		},
		"item that a set through an alias wrote, which a removal through the anchor shifts": {
			before: stringtest.LinesLF("b: &x0", "  - 1", "", "al0: *x0"),
			path:   paths.Doc(),
			fn:     both(at(paths.MustParse("$.al0[1]"), set(spaced)), at(paths.MustParse("$.b[0]"), remove)),
			want:   stringtest.LinesLF("b: &x0", "  - |", "    a", "     ", "    b", "", "al0: *x0"),
		},
		"item that a set through the anchor wrote, which a removal through an alias shifts": {
			before: stringtest.LinesLF("b: &x0", "  - 1", "", "al0: *x0"),
			path:   paths.Doc(),
			fn:     both(at(paths.MustParse("$.b[1]"), set(spaced)), at(paths.MustParse("$.al0[0]"), remove)),
			want:   stringtest.LinesLF("b: &x0", "  - |", "    a", "     ", "    b", "", "al0: *x0"),
		},
		"item that a set through an alias wrote, which a move through the anchor shifts": {
			before: stringtest.LinesLF("b: &x0", "  - 1", "", "al0: *x0"),
			path:   paths.Doc(),
			fn:     both(at(paths.MustParse("$.al0[1]"), set(spaced)), at(paths.MustParse("$.b[0]"), move("$.z"))),
			want:   stringtest.LinesLF("b: &x0", "  - |", "    a", "     ", "    b", "", "al0: *x0", "z: 1"),
		},
		"item that a set through a merge key wrote, which a removal through the anchor shifts": {
			before: stringtest.LinesLF("d: &d", "  l:", "    - 1", "    - 2", "", "svc:", "  <<: *d"),
			path:   paths.Doc(),
			fn: both(
				at(paths.MustParse("$.svc.l[2]"), set(spaced)),
				at(paths.MustParse("$.d.l[0]"), remove),
			),
			want: stringtest.LinesLF(
				"d: &d",
				"  l:",
				"    - 2",
				"    - |",
				"      a",
				"       ",
				"      b",
				"",
				"svc:",
				"  <<: *d",
			),
		},
		"entry that a set through an alias wrote, which a rename through the anchor takes": {
			before: stringtest.LinesLF("b: &x0", "  k:", "    m: 1", "", "al0: *x0"),
			path:   paths.Doc(),
			fn: both(
				at(paths.MustParse("$.al0.k.n"), set(spaced)),
				at(paths.MustParse("$.b.k"), rename("j")),
			),
			want: stringtest.LinesLF(
				"b: &x0",
				"  j:",
				"    m: 1",
				"    \"n\": |",
				"      a",
				"       ",
				"      b",
				"",
				"al0: *x0",
			),
		},
		"item that a set through an alias wrote, which a removal of the alias keeps at the anchor": {
			before: stringtest.LinesLF("r1: &x1", "  - 1", "", "r2: *x1"),
			path:   paths.Doc(),
			fn:     both(at(paths.MustParse("$.r2[1]"), set(spaced)), at(paths.Doc().Child("r2"), remove)),
			want:   stringtest.LinesLF("r1: &x1", "  - 1", "  - |", "    a", "     ", "    b"),
		},
		"item that a set through an alias wrote, which a set of the alias keeps at the anchor": {
			before: stringtest.LinesLF("r1: &x1", "  - 1", "", "r2: *x1"),
			path:   paths.Doc(),
			fn:     both(at(paths.MustParse("$.r2[1]"), set(spaced)), at(paths.Doc().Child("r2"), set("x"))),
			want:   stringtest.LinesLF("r1: &x1", "  - 1", "  - |", "    a", "     ", "    b", "", "r2: x"),
		},
		"entry that a set wrote in an anchored item, which a removal of the item keeps at its alias": {
			before: stringtest.LinesLF("a:", "  - &x1", "    b: 1", "z: *x1"),
			path:   paths.Doc(),
			fn:     both(at(paths.MustParse("$.a[0].b"), set("v")), at(paths.MustParse("$.a[0]"), remove)),
			err:    "test.yaml:4:4: $.z: the edit removes $.a[0], which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"entry that a set through a merge key wrote, which a removal of the merging mapping keeps": {
			before: stringtest.LinesLF("d: &d", "  k: 1", "svc:", "  <<: *d", "z: 1"),
			path:   paths.Doc(),
			fn:     both(at(paths.MustParse("$.svc.k"), set("v")), at(paths.Doc().Child("svc"), remove)),
			want:   stringtest.LinesLF("d: &d", "  k: v", "z: 1"),
		},
		"entry that a set wrote in an anchored mapping, which a removal leaves at a merge that hides it": {
			before: stringtest.LinesLF(
				"defaults: &defaults",
				"  adapter: postgres",
				"  host: localhost",
				"development:",
				"  <<: *defaults",
				"  host: dev",
			),
			path: paths.Doc(),
			fn: both(
				at(paths.MustParse("$.defaults.host"), set("db")),
				at(paths.Doc().Child("defaults"), remove),
			),
			// The removal takes the anchor that the merge key of development
			// reads.
			err: "test.yaml:5:7: $.development: the edit removes $.defaults, which the merge key at 5:3 of $.development reads, and cafe does not write a copy of a value at its alias",
		},
		"entry that a set wrote in an anchored mapping, which a set leaves at a merge that hides it": {
			before: stringtest.LinesLF(
				"defaults: &defaults",
				"  adapter: postgres",
				"  host: localhost",
				"development:",
				"  <<: *defaults",
				"  host: dev",
			),
			path: paths.Doc(),
			fn: both(
				at(paths.MustParse("$.defaults.host"), set("db")),
				at(paths.Doc().Child("defaults"), set(map[string]any{"adapter": "mysql"})),
			),
			// The second set keeps the anchor, so the merge key of development
			// reads the new mapping, and development keeps its own host.
			want: stringtest.LinesLF(
				"defaults: &defaults",
				"  adapter: mysql",
				"development:",
				"  <<: *defaults",
				"  host: dev",
			),
		},
		"entry that a set wrote in an anchored mapping, which a removal leaves at a merge list": {
			before: stringtest.LinesLF("a: &a {host: 1}", "b: &b {port: 2}", "m:", "  <<: [*a, *b]"),
			path:   paths.Doc(),
			fn:     both(at(paths.MustParse("$.a.host"), set(5)), at(paths.Doc().Child("a"), remove)),
			err:    "test.yaml:4:8: $.m: the edit removes $.a, which the merge key at 4:3 of $.m reads, and cafe does not write a copy of a value at its alias",
		},
		"entry that a set through an alias wrote in a merge value, which a removal of the alias keeps at the merging mapping": {
			// The block scalar holds the blank line after it, and the blank
			// lines after the alias go with it.
			before: stringtest.LinesLF("<<: &x", "  a: w", "r0: *x", "", "", "z: 1"),
			path:   paths.Doc(),
			fn:     both(at(paths.MustParse("$.r0.a"), set("q\n\n")), at(paths.Doc().Child("r0"), remove)),
			err:    "test.yaml:1:1: $: the edit sets $.r0.a, which goes through a merge key whose value has an anchor, and cafe does not edit through such a merge key",
		},
		"entry that a set through an alias wrote in a merge value, which a removal of the alias keeps at another alias": {
			before: stringtest.LinesLF("<<: &x", "  a: w", "r0: *x", "", "", "r1:", "  b: *x"),
			path:   paths.Doc(),
			fn:     both(at(paths.MustParse("$.r0.a"), set("q\n\n")), at(paths.Doc().Child("r0"), remove)),
			err:    "test.yaml:1:1: $: the edit sets $.r0.a, which goes through a merge key whose value has an anchor, and cafe does not edit through such a merge key",
		},
		"item that a set through an alias wrote, which a set around the alias keeps at the copy of an anchor around it": {
			before: stringtest.LinesLF("r0:", "  - &x0", "    - 1", "", "  - &x1", "    - *x0", "r1: *x1"),
			path:   paths.Doc(),
			fn: both(
				at(paths.MustParse("$.r0[1][0][1]"), set(map[string]any{"y": spaced})),
				at(paths.Doc().Child("r0"), set(1)),
			),
			err: "test.yaml:7:5: $.r1: the edit sets $.r0, which holds the anchor &x1 that this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"item that a set through an alias wrote, whose path a later set does not reach": {
			before: stringtest.LinesLF("r0:", "  - &x0", "    - 1", "  - *x0", "r2: *x0"),
			path:   paths.Doc(),
			fn: both(
				at(paths.MustParse("$.r2[0]"), remove),
				at(paths.MustParse("$.r2[0]"), set(map[string]any{"k": "q"})),
				at(paths.Doc().Child("r2"), set(1)),
			),
			// The new item goes at the anchor of the sequence, in flow style
			// after the anchor.
			want: stringtest.LinesLF("r0:", "  - &x0 [{k: q}]", "  - *x0", "r2: 1"),
		},
		"entry that a set through an alias wrote in a merge value, which a removal of the alias keeps": {
			before: stringtest.LinesLF("<<: &x {a: w}", "r0: *x", "z: 1"),
			path:   paths.Doc(),
			fn:     both(at(paths.MustParse("$.r0.a"), set("v")), at(paths.Doc().Child("r0"), remove)),
			err:    "test.yaml:1:1: $: the edit sets $.r0.a, which goes through a merge key whose value has an anchor, and cafe does not edit through such a merge key",
		},
		"item that a set through the anchor wrote, after a removal through an alias before it": {
			before: stringtest.LinesLF("b: &x0", "  - 1", "  - 2", "  - 3", "", "al0: *x0"),
			path:   paths.Doc(),
			fn:     both(at(paths.MustParse("$.b[1]"), set("x")), at(paths.MustParse("$.al0[0]"), remove)),
			want:   stringtest.LinesLF("b: &x0", "  - x", "  - 3", "", "al0: *x0"),
		},
		"mapping that a set through an alias wrote, after a removal through the anchor inside it": {
			before: stringtest.LinesLF("b: &x0", "  k:", "    m: 1", "", "al0: *x0"),
			path:   paths.Doc(),
			fn: both(
				at(paths.MustParse("$.al0.k"), set(map[string]any{"n": "x", "o": "y"})),
				at(paths.MustParse("$.b.k.n"), remove),
			),
			want: stringtest.LinesLF("b: &x0", "  k:", `    o: "y"`, "", "al0: *x0"),
		},
		"entry that a set through an alias wrote, which a set of the alias keeps past paths that merges hide": {
			before: stringtest.LinesLF(
				"x: &x", "  j: 1", "", "a: &a {<<: *x, k: 0}", "b: &b [*a, *a, *a, *a]", "c: &c [*b, *b, *b, *b]",
				"d: [*c, *c, *c, *c]", "al: *x",
			),
			path: paths.Doc(),
			fn:   both(at(paths.MustParse("$.al.k"), set(spaced)), at(paths.Doc().Child("al"), set(5))),
			want: stringtest.LinesLF(
				"x: &x",
				"  j: 1",
				"  k: |",
				"    a",
				"     ",
				"    b",
				"",
				"a: &a {<<: *x, k: 0}",
				"b: &b [*a, *a, *a, *a]",
				"c: &c [*b, *b, *b, *b]",
				"d: [*c, *c, *c, *c]",
				"al: 5",
			),
		},
		"entry that a set through an alias wrote, which a removal of the alias keeps past paths that merges hide": {
			before: stringtest.LinesLF("x: &x", "  j: 1", "") + numbered(64, "m%d: {<<: *x, k: 0}") + "al: *x\n",
			path:   paths.Doc(),
			fn:     both(at(paths.MustParse("$.al.k"), set(spaced)), at(paths.Doc().Child("al"), remove)),
			want: stringtest.LinesLF(
				"x: &x",
				"  j: 1",
				"  k: |",
				"    a",
				"     ",
				"    b",
				"",
				"m0: {<<: *x, k: 0}",
				"m1: {<<: *x, k: 0}",
				"m2: {<<: *x, k: 0}",
				"m3: {<<: *x, k: 0}",
				"m4: {<<: *x, k: 0}",
				"m5: {<<: *x, k: 0}",
				"m6: {<<: *x, k: 0}",
				"m7: {<<: *x, k: 0}",
				"m8: {<<: *x, k: 0}",
				"m9: {<<: *x, k: 0}",
				"m10: {<<: *x, k: 0}",
				"m11: {<<: *x, k: 0}",
				"m12: {<<: *x, k: 0}",
				"m13: {<<: *x, k: 0}",
				"m14: {<<: *x, k: 0}",
				"m15: {<<: *x, k: 0}",
				"m16: {<<: *x, k: 0}",
				"m17: {<<: *x, k: 0}",
				"m18: {<<: *x, k: 0}",
				"m19: {<<: *x, k: 0}",
				"m20: {<<: *x, k: 0}",
				"m21: {<<: *x, k: 0}",
				"m22: {<<: *x, k: 0}",
				"m23: {<<: *x, k: 0}",
				"m24: {<<: *x, k: 0}",
				"m25: {<<: *x, k: 0}",
				"m26: {<<: *x, k: 0}",
				"m27: {<<: *x, k: 0}",
				"m28: {<<: *x, k: 0}",
				"m29: {<<: *x, k: 0}",
				"m30: {<<: *x, k: 0}",
				"m31: {<<: *x, k: 0}",
				"m32: {<<: *x, k: 0}",
				"m33: {<<: *x, k: 0}",
				"m34: {<<: *x, k: 0}",
				"m35: {<<: *x, k: 0}",
				"m36: {<<: *x, k: 0}",
				"m37: {<<: *x, k: 0}",
				"m38: {<<: *x, k: 0}",
				"m39: {<<: *x, k: 0}",
				"m40: {<<: *x, k: 0}",
				"m41: {<<: *x, k: 0}",
				"m42: {<<: *x, k: 0}",
				"m43: {<<: *x, k: 0}",
				"m44: {<<: *x, k: 0}",
				"m45: {<<: *x, k: 0}",
				"m46: {<<: *x, k: 0}",
				"m47: {<<: *x, k: 0}",
				"m48: {<<: *x, k: 0}",
				"m49: {<<: *x, k: 0}",
				"m50: {<<: *x, k: 0}",
				"m51: {<<: *x, k: 0}",
				"m52: {<<: *x, k: 0}",
				"m53: {<<: *x, k: 0}",
				"m54: {<<: *x, k: 0}",
				"m55: {<<: *x, k: 0}",
				"m56: {<<: *x, k: 0}",
				"m57: {<<: *x, k: 0}",
				"m58: {<<: *x, k: 0}",
				"m59: {<<: *x, k: 0}",
				"m60: {<<: *x, k: 0}",
				"m61: {<<: *x, k: 0}",
				"m62: {<<: *x, k: 0}",
				"m63: {<<: *x, k: 0}",
			),
		},
		"entry inside a removed value that nested aliases inside it read": {
			before:    "top:\n" + aliasFan("  ", 7) + "z: 0\n",
			path:      paths.Doc(),
			fn:        both(at(paths.MustParse("$.top.a.k"), set(2)), at(paths.Doc().Child("top"), remove)),
			want:      stringtest.LinesLF("z: 0"),
			unlimited: true,
		},
		"entry inside a replaced value that nested aliases inside it read": {
			before:    "top:\n" + aliasFan("  ", 7) + "z: 0\n",
			path:      paths.Doc(),
			fn:        both(at(paths.MustParse("$.top.a.k"), set(2)), at(paths.Doc().Child("top"), set(5))),
			want:      stringtest.LinesLF("top: 5", "z: 0"),
			unlimited: true,
		},
		"entry that nested aliases read at far more paths than the text holds nodes, after a removal of its anchor": {
			before: aliasFan("", 4),
			path:   paths.Doc(),
			fn:     both(at(paths.MustParse("$.a.k"), set(2)), at(paths.Doc().Child("a"), remove)),
			err:    "test.yaml:5:6: aliases expand the document past the alias limit",
			errIs:  niceyaml.ErrExcessiveAliasing,
		},
		"new key after a block scalar that ends a text without a final newline": {
			// The line break before the new key would end the text of the
			// block scalar, which adds a line break to its string.
			before: stringtest.JoinLF("a: |", "  x"),
			path:   paths.Doc(),
			fn:     at(paths.Doc().Child("b"), set(1)),
			err:    "test.yaml:1:4: $.a: " + endsText + "sets $.b",
		},
		"new key after a block scalar with keep chomping that ends a text without a final newline": {
			before: stringtest.JoinLF("a: |+ # c", "  x"),
			path:   paths.Doc(),
			fn:     at(paths.Doc().Child("b"), set(1)),
			err:    "test.yaml:1:4: $.a: " + endsText + "sets $.b",
		},
		"wildcard set of an anchored mapping before the entry that a merge key brings in from it": {
			// The set keeps the anchor, so the merge key brings in the new
			// mapping, and the visit of the entry that it brought in skips.
			before: stringtest.LinesLF("r0: &x {c: 1}", "<<: *x"),
			path:   paths.MustParse("$.*"),
			fn:     set(map[string]any{"z": 1}),
			want:   stringtest.LinesLF("r0: &x", "  z: 1", "<<: *x"),
		},
		"move into a flow sequence of a block scalar that a set wrote under the anchor of the user": {
			// The set keeps the anchor and writes the block scalar, so the
			// move writes the scalar in flow style.
			before: stringtest.LinesLF("a: &x 1", "f: []", "b: *x"),
			path:   paths.Doc(),
			fn:     both(at(paths.Doc().Child("a"), set("p\nq")), at(paths.Doc().Child("a"), move("$.f[0]"))),
			want:   stringtest.LinesLF(`f: [&x "p\nq"]`, "b: *x"),
		},
		"new key after a block scalar whose last line of spaces ends a text without a final newline": {
			before: stringtest.JoinLF("a: |+", "  x", "", "   "),
			path:   paths.Doc(),
			fn:     at(paths.Doc().Child("b"), set(1)),
			err:    "test.yaml:1:4: $.a: " + endsText + "sets $.b",
		},
		"new key after a block scalar that ends a text with a final newline": {
			before: stringtest.LinesLF("a: |", "  x"),
			path:   paths.Doc(),
			fn:     at(paths.Doc().Child("b"), set(1)),
			want:   stringtest.LinesLF("a: |", "  x", "b: 1"),
		},
		"block scalar at the end of a text without a final newline": {
			// The string of the block scalar ends with a line break, so the
			// text takes a final newline.
			before: "a: 1",
			path:   paths.Doc(),
			fn:     at(paths.Doc().Child("b"), set("q\nr\n")),
			want:   stringtest.LinesLF("a: 1", "b: |", "  q", "  r"),
		},
		"removal between a block scalar with keep chomping and a blank line": {
			// The blank line after the entry goes with it, so that the block
			// scalar does not take it.
			before: stringtest.LinesLF("a: |+", "  x", "b: 1", "", "c: 2"),
			path:   paths.Doc(),
			fn:     at(paths.Doc().Child("b"), remove),
			want:   stringtest.LinesLF("a: |+", "  x", "c: 2"),
		},
		"removal of the last line after a block scalar, without a final newline": {
			// The string of the block scalar ends with the line break of its
			// line, so the line break stays.
			before: stringtest.JoinLF("a: |", "  x", "b: 1"),
			path:   paths.Doc(),
			fn:     at(paths.Doc().Child("b"), remove),
			want:   stringtest.LinesLF("a: |", "  x"),
		},
		"removal of the last line after a folded block scalar with the strip indicator, without a final newline": {
			// The parser folds the last line of the scalar in another way
			// without its line break, so the line break stays.
			before: stringtest.JoinLF("a: >-", "  x", "  y", "b: 1"),
			path:   paths.Doc(),
			fn:     at(paths.Doc().Child("b"), remove),
			want:   stringtest.LinesLF("a: >-", "  x", "  y"),
		},
		"removal of the last line after a block scalar with keep chomping and its blank line, without a final newline": {
			before: stringtest.JoinLF("- |+", "  q", "", "- 1"),
			path:   paths.MustParse("$[1]"),
			fn:     remove,
			want:   stringtest.LinesLF("- |+", "  q", ""),
		},
		"move of the last item to its own place after a block scalar with keep chomping and its blank line": {
			// The blank line stays with the scalar, and the text ends without
			// a final newline, as the user's text does.
			before: stringtest.JoinLF("-", "- >+", "    x", "", "- w"),
			path:   paths.MustParse("$[2]"),
			fn:     move("$[3]"),
			want:   stringtest.JoinLF("-", "- >+", "    x", "", "- w"),
		},
		"removal of the last item after a folded block scalar with keep chomping, an indentation indicator, and its blank line": {
			// The scalar takes back the line break of its blank line, and
			// go-yaml's parser refuses a text that ends with an empty line
			// after such a scalar.
			before: stringtest.JoinLF("- >+2", "      z", "", "- a"),
			path:   paths.MustParse("$[1]"),
			fn:     remove,
			err: "test.yaml:2:7: $[0]: the edit removes $[1], and go-yaml's parser refuses the edited text: " +
				"invalid number of indent is specified in the multi-line header",
			engine: true,
		},
		"move to its own place of the last item after an earlier call wrote spaces on its last line": {
			// Go-yaml's parser refuses a text that ends with an empty line
			// after such a scalar.
			before: stringtest.JoinLF("- x", "- >+2", "      z", "", "- a"),
			path:   paths.Doc(),
			fn:     both(at(paths.MustParse("$[2]"), remove), at(paths.MustParse("$[1]"), move("$[2]"))),
			err: "test.yaml:3:7: $[1]: the edit removes $[2], and go-yaml's parser refuses the edited text: " +
				"invalid number of indent is specified in the multi-line header",
			engine: true,
		},
		"new key after a block scalar whose header gives the chomping indicator first": {
			before: stringtest.JoinLF("a: 9", "rz: |+2", "  x"),
			path:   paths.Doc(),
			fn:     at(paths.Doc().Child("z"), set(9)),
			err:    "test.yaml:2:5: $.rz: " + endsText + "sets $.z",
		},
		"new key at the end of a text without a final newline after a block scalar that an earlier call wrote": {
			// The block scalar needs the line break at the end of its line,
			// and the new line ends the text as the user's text ends.
			before: "a: 1",
			path:   paths.Doc(),
			fn:     both(at(paths.Doc().Child("b"), set("x\n")), at(paths.Doc().Child("c"), set(1))),
			want:   stringtest.JoinLF("a: 1", "b: |", "  x", "c: 1"),
		},
		"move to the end of a text without a final newline of a folded block scalar": {
			// The parser folds the last line of the scalar in another way
			// without its line break, so the line break stays.
			before: stringtest.JoinLF("a: >-", "  x", "  y", "b: 1"),
			path:   paths.Doc().Child("a"),
			fn:     move("$.c"),
			want:   stringtest.LinesLF("b: 1", "c: >-", "  x", "  y"),
		},
		"new key after a nested block scalar with keep chomping, an indentation indicator, and its blank line": {
			before: stringtest.LinesLF("m:", "  k:", "    a: |+2", "        x", "", "n: 1"),
			path:   paths.Doc(),
			fn:     at(paths.MustParse("$.m.c"), set(1)),
			want:   stringtest.LinesLF("m:", "  k:", "    a: |+2", "        x", "", "  c: 1", "n: 1"),
		},
		"new key after a nested block scalar with keep chomping and its blank line": {
			before: stringtest.LinesLF("m:", "  k:", "    a: |+", "      x", "", "n: 1"),
			path:   paths.Doc(),
			fn:     at(paths.MustParse("$.m.c"), set(1)),
			want:   stringtest.LinesLF("m:", "  k:", "    a: |+", "      x", "", "  c: 1", "n: 1"),
		},
		"move in flow style of a block scalar that an earlier call changed": {
			before: stringtest.JoinLF("f: {}", "s: 1"),
			path:   paths.Doc(),
			fn:     both(at(paths.Doc().Child("s"), set(spaced)), at(paths.Doc().Child("s"), move("$.f.s"))),
			// The text ends without a final newline, as the user's text does.
			want: stringtest.JoinLF("f: {s: \"a\\n \\nb\\n\"}"),
		},
		"copy in flow style of a block scalar that an earlier call changed": {
			before: stringtest.LinesLF("a: &a", "  s: 1", "", "b: [*a]"),
			path:   paths.Doc(),
			fn:     both(at(paths.MustParse("$.a.s"), set(spaced)), at(paths.Doc().Child("a"), remove)),
			err:    "test.yaml:4:5: $.b[0]: the edit removes $.a, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"removal of a new entry after a line of spaces that a block scalar holds at the end of a text without a final newline": {
			// The line of spaces holds more spaces than the lines of a, so it
			// holds text of a. The set gives it a line break, which changes the
			// string of a.
			before: stringtest.JoinLF("a: |", "  x", "   "),
			path:   paths.Doc(),
			fn:     both(at(paths.Doc().Child("b"), set(1)), at(paths.Doc().Child("b"), remove)),
			err:    "test.yaml:1:4: $.a: " + endsText + "sets $.b",
		},
		"move to the end of a block scalar that ends with a line of spaces at the end of a text without a final newline": {
			before: stringtest.JoinLF("- |+", "    x", "     "),
			path:   paths.Doc(),
			fn:     both(at(paths.MustParse("$[1]"), set("p")), at(paths.MustParse("$[0]"), move("$[2]"))),
			err:    "test.yaml:1:3: $[0]: " + endsText + "sets $[1]",
		},
		"copy that a set writes of a block scalar that an earlier call changed": {
			before: stringtest.LinesLF("a: &a", "  s: 1", "", "b: [*a]"),
			path:   paths.Doc(),
			fn:     both(at(paths.MustParse("$.a.s"), set(spaced)), at(paths.Doc().Child("a"), set(1))),
			want:   stringtest.LinesLF("a: &a 1", "", "b: [*a]"),
		},
		"set of a mapping above a block scalar with keep chomping that an earlier call wrote": {
			before: stringtest.LinesLF("r0:", "  c: x"),
			path:   paths.Doc(),
			fn: both(
				at(paths.MustParse("$.r0.c"), set("q\n\n")),
				at(paths.Doc().Child("r0"), set("q\n\n")),
			),
			want: stringtest.LinesLF("r0: |+", "  q", ""),
		},
		"set of a mapping above a block scalar with keep chomping and a comment that an earlier call wrote": {
			before: stringtest.LinesLF("r0:", "  c: [] # c"),
			path:   paths.Doc(),
			fn: both(
				at(paths.MustParse("$.r0.c"), set("q\n\n")),
				at(paths.Doc().Child("r0"), set("q\n\n")),
			),
			want: stringtest.LinesLF("r0: |+", "  q", ""),
		},
		"move to the end of a text without a final newline of a block scalar with keep chomping that an earlier call wrote": {
			before: stringtest.JoinLF("'x':", "<<: {}"),
			path:   paths.Doc(),
			fn: both(
				at(paths.Doc().Child("x"), set("q\n\n")),
				at(paths.Doc().Child("x"), move("$.z0")),
			),
			err: "test.yaml:2:1: $: the edit moves $.x to $.z0, which changes the keys of a merge through a merge key that merges a mapping written in place, and cafe does not change the keys of such a merge",
		},
		"copy of a block collection that ends with a block scalar with keep chomping, before a blank line": {
			// The removal of a takes the anchor that b reads.
			before: stringtest.LinesLF("a: &a", "  s: |+", "    x", "", "  t: 1", "", "b: *a", "c: 1"),
			path:   paths.Doc(),
			fn:     both(at(paths.MustParse("$.a.t"), remove), at(paths.Doc().Child("a"), remove)),
			err:    "test.yaml:7:4: $.b: the edit removes $.a, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"copy that a removal writes of a block scalar that an earlier call changed, for an alias inside it": {
			before: nested,
			path:   paths.Doc(),
			fn:     both(at(paths.MustParse("$.a[1][0][1]"), set(spaced)), at(paths.Doc().Child("a"), remove)),
			err:    "test.yaml:9:4: $.z: the edit removes $.a, which holds the anchor &x1 that this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"copy that a set writes of a block scalar that an earlier call changed, for an alias inside it": {
			before: nested,
			path:   paths.Doc(),
			fn:     both(at(paths.MustParse("$.a[1][0][1]"), set(spaced)), at(paths.Doc().Child("a"), set(1))),
			err:    "test.yaml:9:4: $.z: the edit sets $.a, which holds the anchor &x1 that this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"copy that a removal writes of a block scalar for an alias inside it": {
			before: nested,
			path:   paths.Doc(),
			fn:     at(paths.Doc().Child("a"), remove),
			err:    "test.yaml:9:4: $.z: the edit removes $.a, which holds the anchor &x1 that this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"block scalar that the edit sets": {
			before: stringtest.LinesLF("a: |+", "  x", "", "b: 1"),
			path:   paths.Doc(),
			fn:     at(paths.Doc().Child("a"), set("y\n")),
			// The blank line belongs to the old block scalar, so it goes with
			// it.
			want: stringtest.LinesLF("a: |", "  y", "b: 1"),
		},
		"rename that a merge key reveals, in the copy of an alias that a move writes": {
			// The copy writes the value of *x in place of the alias, so its
			// entries are copies of the entries of m.
			before: stringtest.LinesLF("t: {}", "m: &x {n: 1}", "z:", "  <<: *x", "  n: 2"),
			path:   paths.Doc(),
			fn: both(
				at(paths.Doc().Child("z"), move("$.t.w")),
				at(paths.MustParse("$.t.w.n"), func(_ context.Context, n *edit.Node) error { return n.SetKey("k") }),
			),
			err: "test.yaml:4:3: $.z: the edit moves $.z to $.t.w, which puts a block value inside a flow collection, and cafe does not move a block value into a flow collection",
		},
		"removal inside a value that a set writes again": {
			// The second set leaves the text as it finds it, and the removal
			// then edits the value that both sets wrote.
			before: stringtest.LinesLF("r0: x"),
			path:   paths.Doc(),
			fn: both(
				at(paths.Doc().Child("a"), set(map[string]any{"z": 1})),
				at(paths.Doc().Child("a"), set(map[string]int{"z": 1})),
				at(paths.MustParse("$.a.z"), remove),
			),
			want: stringtest.LinesLF("r0: x", "a: {}"),
		},
		"set of an item that a removal then copies into an entry that a merge key brings in under a hidden key": {
			// The removal takes the anchor that the merge key of m reads.
			before: stringtest.LinesLF("s: &x0", "  a:", `  - "x"`, "m:", "  <<: [*x0]", "  a: own"),
			path:   paths.Doc(),
			fn:     both(at(paths.MustParse("$.s.a[0]"), set("v")), at(paths.Doc().Child("s"), remove)),
			err:    "test.yaml:5:8: $.m: the edit removes $.s, which the merge key at 5:3 of $.m reads, and cafe does not write a copy of a value at its alias",
		},
		"set of an item that a removal then copies into an entry that a merge key brings in": {
			before: stringtest.LinesLF("s: &x0", "  a:", `  - "x"`, "m:", "  <<: [*x0]", "  b: own"),
			path:   paths.Doc(),
			fn:     both(at(paths.MustParse("$.s.a[0]"), set("v")), at(paths.Doc().Child("s"), remove)),
			err:    "test.yaml:5:8: $.m: the edit removes $.s, which the merge key at 5:3 of $.m reads, and cafe does not write a copy of a value at its alias",
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
		"new key of CRLF text that an earlier removal left on one line": {
			before:  stringtest.JoinCRLF("p: 1", "n: 2"),
			earlier: at(paths.Doc().Child("n"), remove),
			path:    paths.Doc(),
			fn:      at(paths.Doc().Child("q"), set(1)),
			want:    stringtest.JoinCRLF("p: 1", "q: 1"),
		},
		"each entry of CRLF text without a final newline": {
			before: stringtest.JoinCRLF("p: {a: 1}", "n:"),
			path:   paths.Doc(),
			fn: both(
				at(paths.Doc().Child("n"), remove),
				at(paths.Doc().Child("p"), remove),
				at(paths.Doc().Child("z"), set(1)),
				at(paths.Doc().Child("w"), set(2)),
			),
			want: stringtest.JoinCRLF("z: 1", "w: 2"),
		},
		"set of an empty item of a copy of an anchored sequence with a blank line": {
			before: stringtest.LinesLF("r0: &x0", "  -", "", "  -", "r2: *x0"),
			path:   paths.Doc(),
			fn:     both(at(paths.Doc().Child("r0"), remove), at(paths.MustParse("$.r2[0]"), set(1))),
			err:    "test.yaml:5:5: $.r2: the edit removes $.r0, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"set of an empty item of a copy of an anchored sequence with a blank line, with CRLF": {
			before: stringtest.JoinCRLF("r0: &x0", "  -", "", "  -", "r2: *x0", ""),
			path:   paths.Doc(),
			fn:     both(at(paths.Doc().Child("r0"), remove), at(paths.MustParse("$.r2[0]"), set(1))),
			err:    "test.yaml:5:5: $.r2: the edit removes $.r0, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"set of an empty item of a copy of an anchored sequence with a blank line, in a root that merges": {
			before: stringtest.LinesLF("r0: &x0", "  -", "", "  -", "r2: *x0", "<<: {q: 1}"),
			path:   paths.Doc(),
			fn:     both(at(paths.Doc().Child("r0"), remove), at(paths.MustParse("$.r2[0]"), set(1))),
			err:    "test.yaml:6:1: $: the edit removes $.r0, which changes the keys of a merge through a merge key that merges a mapping written in place, and cafe does not change the keys of such a merge",
		},
		"set of a string with a control character": {
			// Go-yaml's encoder writes U+0001 as it is, which YAML does not
			// allow in a text, so the Set fails loudly at the value.
			before: "a: 1\n",
			path:   paths.Doc().Child("a"),
			fn:     set("x\x01y"),
			err: "1:4: $.a: go-yaml's encoder writes U+0001 as it is, " +
				"and YAML does not allow that in a text",
			engine: true,
		},
		"set of a string with a next line character": {
			// YAML allows U+0085 in a text.
			before: "a: 1\n",
			path:   paths.Doc().Child("a"),
			fn:     set("x\u0085y"),
			want:   "a: x\u0085y\n",
		},
		"set of a string with a byte that is not UTF-8": {
			before: "a: 1\n",
			path:   paths.Doc().Child("a"),
			fn:     set("x\xffy"),
			err: "1:4: $.a: go-yaml's encoder writes the byte 0xff, which is not UTF-8, as it is, " +
				"and YAML does not allow that in a text",
			engine: true,
		},
		"set of a string with a control character in a new entry of a flow mapping": {
			// The call creates the place, so the problem binds at the nearest value above it that the user
			// wrote, under its path (fix19 G1-1).
			before: "a: {b: 1}\n",
			path:   paths.Doc(),
			fn:     at(paths.MustParse("$.a.c"), set("x\x01y")),
			err: "1:1: $.a: go-yaml's encoder writes U+0001 as it is, " +
				"and YAML does not allow that in a text",
			engine: true,
		},
		"rename to a key with a control character": {
			before: "a: 1\n",
			path:   paths.Doc().Child("a"),
			fn:     rename("x\x01y"),
			err: "1:1: $.a~: go-yaml's encoder writes U+0001 as it is, " +
				"and YAML does not allow that in a text",
			engine: true,
		},
		"rename to a key with a next line character": {
			before: "a: 1\n",
			path:   paths.Doc().Child("a"),
			fn:     rename("x\u0085y"),
			want:   "x\u0085y: 1\n",
		},
		"rename of a single-quoted key to a key with a control character": {
			// A single-quoted scalar has no escapes, so the key takes double
			// quotes.
			before: "'a': 1\n",
			path:   paths.Doc().Child("a"),
			fn:     rename("x\x01y"),
			want:   "\"x\\u0001y\": 1\n",
		},
		"move of a quoted string with an escaped control character into a flow sequence": {
			// The string takes the text of go-yaml's flow encoder, which writes
			// U+0001 as it is.
			before: stringtest.LinesLF(`a: "x\u0001`, `  y"`, "b: [1]"),
			path:   paths.Doc().Child("a"),
			fn:     move("$.b[1]"),
			err: "1:4: $.a: go-yaml's encoder writes U+0001 as it is, " +
				"and YAML does not allow that in a text",
			engine: true,
		},
		"move of a plain string with a control character of the user into a flow sequence": {
			// The character is the user's, so the move keeps it.
			before: "a: x\x01y\nb: [1]\n",
			path:   paths.Doc().Child("a"),
			fn:     move("$.b[1]"),
			want:   "b: [1, x\x01y]\n",
		},
		"move of CRLF text that an earlier removal left on one line": {
			before:  stringtest.JoinCRLF("n: {a: 1}", "p: 1"),
			earlier: at(paths.Doc().Child("p"), remove),
			path:    paths.Doc(),
			fn:      both(at(paths.Doc().Child("n"), move("$.q")), at(paths.Doc().Child("z"), set(1))),
			want:    stringtest.JoinCRLF("q: {a: 1}", "z: 1"),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := []niceyaml.SourceOption{
				niceyaml.WithAllowDuplicateKeys(tc.dup), niceyaml.WithAliasLimit(!tc.unlimited),
			}
			if tc.ref != "" {
				opts = append(opts, niceyaml.WithReferences(niceyaml.NewSourceFromString(tc.ref)))
			}

			d := newDoc(t, tc.before, opts...)

			if tc.earlier != nil {
				require.NoError(t, d.Apply(t.Context(), paths.Doc(), tc.earlier))
			}

			var calls []string

			err := d.Apply(t.Context(), tc.path, func(ctx context.Context, n *edit.Node) error {
				calls = append(calls, n.Path().String())

				return tc.fn(ctx, n)
			}, tc.opts...)

			if tc.calls != nil {
				assert.Equal(t, tc.calls, append([]string{}, calls...))
			}

			if tc.errIs != nil {
				require.ErrorIs(t, err, tc.errIs)
			}

			if tc.err != "" {
				assert.Equal(t, tc.engine, errors.Is(err, edit.ErrEngine))
			}

			requireEdit(t, d, err, tc.err, tc.want)
		})
	}

	t.Run("alias that reads a reference document", func(t *testing.T) {
		t.Parallel()

		ref := niceyaml.NewSourceFromString(stringtest.LinesLF(
			"kaffe: &kaffe {item: Kaffe, price: 35}",
			"codes: &codes {0x10: hex, .nan: nan, True: yes, '1e3': q}",
			"forms: &forms {v: 1.10, b: !!binary aGk=, t: True, n: ~, h: 0x10}",
			"deep: &deep {<<: *kaffe, size: L}",
			"first: &twice {n: 1}",
			"second: &twice {n: 2}",
			"---",
			"later: &later {n: 3}",
		), niceyaml.WithName("ref.yaml"))
		order := stringtest.LinesLF(
			"items:",
			"  - *kaffe # shared",
			"  - {item: Te, price: 30}",
		)
		items := paths.MustParse("$.items[*]")
		first := paths.MustParse("$.items[0]")

		// A path that ends at the alias selects it, and each edit there
		// changes the alias that the document writes.
		tcs := map[string]struct {
			fn   edit.Func
			path paths.Path
			// The text of the document, or the order when it is "".
			before string
			want   string
			err    string
			// The paths of the nodes that fn gets, when set.
			calls []string
		}{
			"every item, the alias included": {
				path:  items,
				fn:    func(context.Context, *edit.Node) error { return nil },
				want:  order,
				calls: []string{"$.items[0]", "$.items[1]"},
			},
			"Set replaces the alias": {
				path: first,
				fn:   set(map[string]any{"item": "Kaka", "price": 20}),
				want: stringtest.LinesLF("items:", "  - {item: Kaka, price: 20} # shared", "  - {item: Te, price: 30}"),
			},
			"Remove removes the item of the alias": {
				path: first,
				fn:   remove,
				want: stringtest.LinesLF("items:", "  - {item: Te, price: 30}"),
			},
			"Move moves the alias": {
				path: first,
				fn:   move("$.special"),
				want: stringtest.LinesLF("items:", "  - {item: Te, price: 30}", "special: *kaffe # shared"),
			},
			"Set below the alias fails": {
				path: items,
				fn:   at(paths.Current().Child("price"), set(1)),
				err: "test.yaml:2:5: $.items[0]: cafe.jacobcolvin.com/v1 sets $.items[0].price, " +
					"which goes through *kaffe, an alias of a reference document",
			},
			"Remove below the alias fails": {
				// The problem of the path names what the call does with it.
				path: first,
				fn:   at(paths.Current().Child("price"), remove),
				err: "test.yaml:2:5: $.items[0]: cafe.jacobcolvin.com/v1 removes $.items[0].price, " +
					"which goes through *kaffe, an alias of a reference document",
			},
			"SetKey below the alias fails": {
				path: first,
				fn:   at(paths.Current().Child("price"), rename("cost")),
				err: "test.yaml:2:5: $.items[0]: cafe.jacobcolvin.com/v1 renames $.items[0].price, " +
					"which goes through *kaffe, an alias of a reference document",
			},
			"Move below the alias fails": {
				path: first,
				fn:   at(paths.Current().Child("price"), move("$.price")),
				err: "test.yaml:2:5: $.items[0]: cafe.jacobcolvin.com/v1 moves $.items[0].price, " +
					"which goes through *kaffe, an alias of a reference document",
			},
			"error of the Func binds at the alias": {
				path: items,
				fn: func(context.Context, *edit.Node) error {
					return niceyaml.NewError("too expensive", niceyaml.AtPath(paths.Current().Child("price")))
				},
				err: "test.yaml:2:5: $.items[0].price: too expensive",
			},
			"reads below the alias that the Func ignores": {
				// The text of the document does not hold the value below the
				// alias. Decode returns the problem of the path, which matches
				// paths.ErrReferenceAlias, and Exists, Text, and Kind answer as
				// at a place without a value. Each read records nothing on the
				// Apply, so the edit after them passes.
				path: paths.Doc(),
				fn: both(
					at(paths.MustParse("$.items[0].price"), refusedRead),
					at(paths.MustParse("$.items[0].price"), reads(exists, false)),
					at(paths.MustParse("$.items[0].price"), reads(text, "")),
					at(paths.MustParse("$.items[0].price"), reads(kind, niceyaml.NodeNone)),
					at(paths.MustParse("$.items[1].price"), set(31)),
				),
				want: stringtest.LinesLF("items:", "  - *kaffe # shared", "  - {item: Te, price: 31}"),
			},
			"reads through a merge key of the alias": {
				// A name that the mapping does not write after the merge key
				// reads as a name below the alias does. A name that it writes
				// there reads its own entry, and a Decode of the mapping reads
				// the entries that the merge key brings in.
				path: paths.Doc(),
				fn: both(
					at(paths.MustParse("$.extra.item"), refusedRead),
					at(paths.MustParse("$.extra.item"), reads(exists, false)),
					at(paths.MustParse("$.extra.item"), reads(text, "")),
					at(paths.MustParse("$.extra.item"), reads(kind, niceyaml.NodeNone)),
					at(paths.MustParse("$.extra.size"), reads(text, "L")),
					at(paths.MustParse("$.extra"), decodes(map[string]any{
						"item": "Kaffe", "price": uint64(35), "size": "L",
					})),
				),
				before: stringtest.LinesLF("items:", "  - *kaffe # shared", "extra:", "  <<: *kaffe", "  size: L"),
				want:   stringtest.LinesLF("items:", "  - *kaffe # shared", "extra:", "  <<: *kaffe", "  size: L"),
			},
			"decode below the alias whose error the Func returns": {
				// The problem binds at the alias, as niceyaml's Node.At binds it.
				path: paths.Doc(),
				fn: at(paths.MustParse("$.items[0].item"), func(ctx context.Context, n *edit.Node) error {
					_, err := n.Decode[int](ctx)

					return err
				}),
				err: "test.yaml:2:5: $.items[0]: cafe.jacobcolvin.com/v1 reads $.items[0].item, " +
					"which goes through *kaffe, an alias of a reference document",
			},
			"decode through a merge key of the alias whose error the Func returns": {
				// The problem binds at the alias of the merge key, under the path
				// of its mapping.
				before: stringtest.LinesLF("extra:", "  <<: *deep", "  cost: 3"),
				path:   paths.Doc(),
				fn: at(paths.MustParse("$.extra.size"), func(ctx context.Context, n *edit.Node) error {
					_, err := n.Decode[int](ctx)

					return err
				}),
				err: "test.yaml:2:7: $.extra: cafe.jacobcolvin.com/v1 reads $.extra.size, " +
					"which goes through <<: *deep at 2:3, a merge key that reads the reference document ref.yaml, so that document decides which names $.extra reads",
			},
			"decode of the mapping of a merge key of the alias": {
				// A control: the Decode of the mapping reads the entries that the
				// merge key brings in, through the merge keys of the reference
				// document.
				before: stringtest.LinesLF("extra:", "  <<: *deep", "  cost: 3"),
				path:   paths.Doc(),
				fn: both(
					at(paths.MustParse("$.extra"), decodes(map[string]any{
						"item": "Kaffe", "price": uint64(35), "size": "L", "cost": uint64(3),
					})),
					at(paths.MustParse("$.extra.cost"), decodes(uint64(3))),
				),
				want: stringtest.LinesLF("extra:", "  <<: *deep", "  cost: 3"),
			},
			"reads an own key before a merge key of the alias": {
				// The merge key can bring in the key of the own entry, which then
				// wins over it, so the reference document decides what the path
				// selects.
				before: stringtest.LinesLF("extra:", "  size: S", "  <<: *kaffe"),
				path:   paths.Doc(),
				fn: both(
					at(paths.MustParse("$.extra.size"), refusedRead),
					at(paths.MustParse("$.extra.size"), reads(exists, false)),
					at(paths.MustParse("$.extra.size"), reads(text, "")),
					at(paths.MustParse("$.extra"), decodes(map[string]any{
						"item": "Kaffe", "price": uint64(35), "size": "S",
					})),
				),
				want: stringtest.LinesLF("extra:", "  size: S", "  <<: *kaffe"),
			},
			"set of an own key before a merge key of the alias": {
				before: stringtest.LinesLF("extra:", "  size: S", "  <<: *kaffe"),
				path:   paths.Doc(),
				fn:     at(paths.MustParse("$.extra.size"), set("M")),
				err: "test.yaml:3:7: $.extra: cafe.jacobcolvin.com/v1 sets $.extra.size, " +
					"which goes through <<: *kaffe at 3:3, a merge key that reads the reference document ref.yaml, so that document decides which names $.extra reads",
			},
			"Apply at an own key before a merge key of the alias": {
				before: stringtest.LinesLF("extra:", "  size: S", "  <<: *kaffe"),
				path:   paths.MustParse("$.extra.size"),
				fn:     set("M"),
				err: "test.yaml:3:7: $.extra: cafe.jacobcolvin.com/v1 reads $.extra.size, " +
					"which goes through <<: *kaffe at 3:3, a merge key that reads the reference document ref.yaml, so that document decides which names $.extra reads",
				calls: []string{},
			},
			"Apply at a name that a merge key of the alias brings in": {
				// A control: an edit there fails, as an edit through the alias
				// does.
				before: stringtest.LinesLF("extra:", "  size: S", "  <<: *kaffe"),
				path:   paths.MustParse("$.extra.item"),
				fn:     set("Te"),
				err: "test.yaml:3:7: $.extra: cafe.jacobcolvin.com/v1 reads $.extra.item, which goes through " +
					"<<: *kaffe at 3:3, a merge key that reads the reference document ref.yaml, so that document " +
					"decides which names $.extra reads",
			},
			"Apply of a descent below an own key before a merge key of the alias": {
				// The merge key can bring in own, so the descent fails at the
				// alias, as a path through own does.
				before: stringtest.LinesLF("m:", "  own: {x: 1}", "  <<: *kaffe"),
				path:   paths.MustParse("$..x"),
				fn:     set(5),
				err: "test.yaml:3:7: $.m: cafe.jacobcolvin.com/v1 reads $..x, which goes through <<: *kaffe at 3:3, " +
					"a merge key that reads the reference document ref.yaml, so that document decides which names " +
					"$.m reads",
				calls: []string{},
			},
			"Apply of every node with an own key before a merge key of the alias": {
				before: stringtest.LinesLF("m:", "  own: {x: 1}", "  <<: *kaffe"),
				path:   paths.MustParse("$..*"),
				fn:     reads(exists, true),
				err: "test.yaml:3:7: $.m: cafe.jacobcolvin.com/v1 reads $..*, which goes through <<: *kaffe at 3:3, " +
					"a merge key that reads the reference document ref.yaml, so that document decides which names " +
					"$.m reads",
				calls: []string{},
			},
			"Apply of a descent through services, one of which merges the alias": {
				before: stringtest.LinesLF(
					"services:",
					"  web:",
					"    environment: {A: 1}",
					"    <<: *kaffe",
					"  db:",
					"    environment: {A: 2}",
				),
				path: paths.MustParse("$..environment"),
				fn:   at(paths.Current().Child("A"), set(3)),
				err: "test.yaml:4:9: $.services.web: cafe.jacobcolvin.com/v1 reads $..environment, " +
					"which goes through <<: *kaffe at 4:5, a merge key that reads the reference document ref.yaml, so that document decides which names $.services.web reads",
				calls: []string{},
			},
			"Apply of a descent below an own key after a merge key of the alias": {
				// A control: the entry after the merge key wins over every entry
				// that the merge key brings in.
				before: stringtest.LinesLF("m:", "  <<: *kaffe", "  own: {x: 1}"),
				path:   paths.MustParse("$..x"),
				fn:     set(5),
				want:   stringtest.LinesLF("m:", "  <<: *kaffe", "  own: {x: 5}"),
				calls:  []string{"$.m.own.x"},
			},
			"Apply of a descent that passes a merge key of the alias that decides nothing": {
				// The merge key of the root comes before c, so only the merge
				// key of c decides an entry that the descent selects, and the
				// problem binds there, as niceyaml's Node.Nodes binds it.
				before: stringtest.LinesLF("<<: *codes", "c:", "  x: 1", "  <<: *kaffe"),
				path:   paths.MustParse("$..x"),
				fn:     set(5),
				err: "test.yaml:4:7: $.c: cafe.jacobcolvin.com/v1 reads $..x, which goes through <<: *kaffe at 4:3, " +
					"a merge key that reads the reference document ref.yaml, so that document decides which names " +
					"$.c reads",
				calls: []string{},
			},
			"Apply of each item, where the merge key of a later item decides": {
				before: "s: [{<<: *codes, a: 1}, {<<: *kaffe}]\n",
				path:   paths.MustParse("$.s[*].a"),
				fn:     set(5),
				err: "test.yaml:1:30: $.s[1]: cafe.jacobcolvin.com/v1 reads $.s[*].a, which goes through <<: *kaffe " +
					"at 1:26, a merge key that reads the reference document ref.yaml, so that document decides " +
					"which names $.s[1] reads",
				calls: []string{},
			},
			"Apply of each item of each entry, where an alias of an entry stops the selector": {
				// A [*] selector reads no entry of the mapping, so the alias of n
				// stops it.
				before: stringtest.LinesLF("m: {a: 1, <<: *kaffe}", "n: *forms"),
				path:   paths.MustParse("$.*[*]"),
				fn:     set(5),
				err: "test.yaml:2:4: $.n: cafe.jacobcolvin.com/v1 reads $.*[*], which goes through *forms, an alias of " +
					"a reference document",
				calls: []string{},
			},
			"Apply at a name of a mapping whose merge sequence holds the alias second": {
				// The lookup reads the merge key of h through *h, but the alias
				// of the reference document that it stops at is *kaffe.
				before: stringtest.LinesLF("h: &h", "  <<: *codes", "m:", "  <<: [*h, *kaffe]", "  x: 1"),
				path:   paths.MustParse("$.m.y"),
				fn:     set(5),
				err: "test.yaml:4:12: $.m: cafe.jacobcolvin.com/v1 reads $.m.y, which goes through <<: [*h, *kaffe] " +
					"at 4:3, a merge key that reads the reference document ref.yaml, so that document decides " +
					"which names $.m reads",
				calls: []string{},
			},
			"reads an anchor that the reference document defines twice": {
				// Niceyaml's decode reads the anchor that the reference
				// document defines last, and so does a Decode at the alias. A
				// path below the alias reads as it does below any such alias.
				before: stringtest.LinesLF("t: *twice", "u:", "  <<: *twice"),
				path:   paths.Doc(),
				fn: both(
					at(paths.MustParse("$.t"), decodes(map[string]any{"n": uint64(2)})),
					at(paths.MustParse("$.u"), decodes(map[string]any{"n": uint64(2)})),
					at(paths.MustParse("$.t.n"), reads(exists, false)),
					at(paths.MustParse("$.u.n"), refusedRead),
				),
				want: stringtest.LinesLF("t: *twice", "u:", "  <<: *twice"),
			},
			"reads an anchor of a later document of the reference Source": {
				before: stringtest.LinesLF("l: *later"),
				path:   paths.Doc(),
				fn: both(
					at(paths.MustParse("$.l"), decodes(map[string]any{"n": uint64(3)})),
					at(paths.MustParse("$.l.n"), reads(kind, niceyaml.NodeNone)),
				),
				want: stringtest.LinesLF("l: *later"),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				before := order
				if tc.before != "" {
					before = tc.before
				}

				doc := yamltest.FirstDocument(t, before, niceyaml.WithName("test.yaml"), niceyaml.WithReferences(ref))

				d, err := edit.New(doc)
				require.NoError(t, err)

				calls := []string{}

				err = d.Apply(t.Context(), tc.path, func(ctx context.Context, n *edit.Node) error {
					calls = append(calls, n.Path().String())

					return tc.fn(ctx, n)
				}, label)

				if tc.calls != nil {
					assert.Equal(t, tc.calls, calls)
				}

				requireEdit(t, d, err, tc.err, tc.want)
			})
		}

		t.Run("the alias reads as its anchor", func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, order, niceyaml.WithName("test.yaml"), niceyaml.WithReferences(ref))

			d, err := edit.New(doc)
			require.NoError(t, err)

			err = d.Apply(t.Context(), first, func(ctx context.Context, n *edit.Node) error {
				assert.Equal(t, niceyaml.NodeMapping, n.Kind())

				got, err := n.Decode[map[string]any](ctx)
				require.NoError(t, err)
				assert.Equal(t, map[string]any{"item": "Kaffe", "price": uint64(35)}, got)

				return nil
			})
			require.NoError(t, err)
		})
	})

	t.Run("merge key that reads a reference document", func(t *testing.T) {
		t.Parallel()

		const block = "spec:\n  <<: *b\n  cost: 3\n"

		// The reference document decides which names the mapping of the merge
		// key reads, so an edit that changes the names that the mapping writes
		// fails, as a path through the alias does, whatever the reference
		// document holds. So does an edit that changes the names of a mapping
		// that the mapping of the merge key reads through a merge key of its
		// own. A Set of the value of an own entry passes, and so does each
		// edit below it. An edit through a merge key that merges a mapping
		// with a merge key of its own fails, as it does in the document.
		tcs := map[string]struct {
			fn     edit.Func
			path   string
			before string
			// The reference documents, the first one alone when refs is nil.
			ref  string
			refs []string
			want string
			err  string
			// Whether the error is the refusal of a merge key of a refused
			// form, not one that matches paths.ErrReferenceAlias.
			form bool
		}{
			"rename to a key that the merge key brings in": {
				before: block,
				ref:    "base: &b {price: 4}\n",
				fn:     rename("price"),
				err: `test.yaml:3:3: $.spec.cost: cafe.jacobcolvin.com/v1 renames $.spec.cost to "price", but <<: ` +
					`*b at 2:3 reads a reference document, which decides which names $.spec reads`,
			},
			"rename of a key that the merge key brings in too": {
				before: block,
				ref:    "base: &b {cost: 4}\n",
				fn:     rename("price"),
				err: `test.yaml:3:3: $.spec.cost: cafe.jacobcolvin.com/v1 renames $.spec.cost to "price", but <<: ` +
					`*b at 2:3 reads a reference document, which decides which names $.spec reads`,
			},
			"removal of a key that the merge key brings in too": {
				before: block,
				ref:    "base: &b {cost: 4}\n",
				fn:     remove,
				err: "test.yaml:3:3: $.spec.cost: cafe.jacobcolvin.com/v1 removes $.spec.cost, but <<: *b at 2:3 " +
					"reads a reference document, which decides which names $.spec reads",
			},
			"move of a key that the merge key brings in too": {
				before: block,
				ref:    "base: &b {cost: 4}\n",
				fn:     move("$.z"),
				err: "test.yaml:3:3: $.spec.cost: cafe.jacobcolvin.com/v1 moves $.spec.cost to $.z, but <<: *b at " +
					"2:3 reads a reference document, which decides which names $.spec reads",
			},
			"removal of a key that a flow merge key brings in too": {
				before: "spec: {<<: *b, cost: 3}\n",
				ref:    "base: &b {cost: 4}\n",
				fn:     remove,
				err: "test.yaml:1:16: $.spec.cost: cafe.jacobcolvin.com/v1 removes $.spec.cost, but <<: *b at 1:8 " +
					"reads a reference document, which decides which names $.spec reads",
			},
			"rename to a key that a merge key of the reference document brings in": {
				before: block,
				ref:    "x: &x {price: 4}\nbase: &b\n  <<: *x\n",
				fn:     rename("price"),
				err: `test.yaml:3:3: $.spec.cost: cafe.jacobcolvin.com/v1 renames $.spec.cost to "price", but <<: ` +
					`*b at 2:3 reads a reference document, which decides which names $.spec reads`,
			},
			"removal of a key that a merge key of the reference document brings in": {
				before: block,
				ref:    "x: &x {cost: 4}\nbase: &b {<<: *x}\n",
				fn:     remove,
				err: "test.yaml:3:3: $.spec.cost: cafe.jacobcolvin.com/v1 removes $.spec.cost, but <<: *b at 2:3 " +
					"reads a reference document, which decides which names $.spec reads",
			},
			"removal of a key that a sequence of the reference document brings in": {
				before: block,
				ref:    "base: &b [{cost: 4}]\n",
				fn:     remove,
				err: "test.yaml:3:3: $.spec.cost: cafe.jacobcolvin.com/v1 removes $.spec.cost, but <<: *b at 2:3 " +
					"reads a reference document, which decides which names $.spec reads",
			},
			"removal of a key that the reference document writes in hexadecimal": {
				before: "spec:\n  <<: *b\n  16: 3\n",
				path:   "$.spec.16",
				ref:    "base: &b {0x10: 4}\n",
				fn:     remove,
				err: "test.yaml:3:3: $.spec.16: cafe.jacobcolvin.com/v1 removes $.spec.16, but <<: *b at 2:3 " +
					"reads a reference document, which decides which names $.spec reads",
			},
			"removal of a key that the reference document writes as True": {
				before: "spec:\n  <<: *b\n  true: 3\n",
				path:   "$.spec.true",
				ref:    "base: &b {True: 4}\n",
				fn:     remove,
				err: "test.yaml:3:3: $.spec.true: cafe.jacobcolvin.com/v1 removes $.spec.true, but <<: *b at 2:3 " +
					"reads a reference document, which decides which names $.spec reads",
			},
			"removal of a key that the reference document writes as a tilde": {
				before: "spec:\n  <<: *b\n  'null': 3\n",
				path:   "$.spec.null",
				ref:    "base: &b {~: 4}\n",
				fn:     remove,
				err: "test.yaml:3:3: $.spec.null: cafe.jacobcolvin.com/v1 removes $.spec.null, but <<: *b at 2:3 " +
					"reads a reference document, which decides which names $.spec reads",
			},
			"removal of a key that another reference document brings in": {
				before: block,
				refs:   []string{"r: &r {cost: 4}\n", "base: &b {<<: *r}\n"},
				fn:     remove,
				err: "test.yaml:3:3: $.spec.cost: cafe.jacobcolvin.com/v1 removes $.spec.cost, but <<: *b at 2:3 " +
					"reads a reference document, which decides which names $.spec reads",
			},
			"rename of a key that the merge key does not bring in": {
				// The engine does not read which names the reference document
				// holds.
				before: block,
				ref:    "base: &b {other: 4}\n",
				fn:     rename("price"),
				err: `test.yaml:3:3: $.spec.cost: cafe.jacobcolvin.com/v1 renames $.spec.cost to "price", but <<: ` +
					`*b at 2:3 reads a reference document, which decides which names $.spec reads`,
			},
			"removal of a key that the merge key does not bring in": {
				before: block,
				ref:    "base: &b {other: 4}\n",
				fn:     remove,
				err: "test.yaml:3:3: $.spec.cost: cafe.jacobcolvin.com/v1 removes $.spec.cost, but <<: *b at 2:3 " +
					"reads a reference document, which decides which names $.spec reads",
			},
			"move into the mapping of the merge key": {
				// A control: the mapping does not write z after the merge key, so
				// the path of the target fails as a path through the alias does.
				before: block + "z: 1\n",
				path:   "$.z",
				ref:    "base: &b {other: 4}\n",
				fn:     move("$.spec.z"),
				err: `test.yaml:2:7: $.spec: cafe.jacobcolvin.com/v1 moves "z" to $.spec.z, which goes through ` +
					`<<: *b at 2:3, a merge key that reads a reference document, so that document decides which ` +
					`names $.spec reads`,
			},
			"move into a mapping that the merge key merges beside the reference document": {
				// A control: the merge sequence is a refused form, which
				// refuses the call first.
				before: "l: &l {cost: 3}\nspec:\n  <<: [*l, *b]\nz: 1\n",
				path:   "$.z",
				ref:    "base: &b {other: 4}\n",
				fn:     move("$.l.z"),
				err: "test.yaml:3:3: $.spec: cafe.jacobcolvin.com/v1 moves $.z to $.l.z, which changes the keys of " +
					"a merge through a merge key that merges a sequence of aliases, and this program does not " +
					"change the keys of such a merge",
				form: true,
			},
			"removal of a key of a mapping that the merge key merges beside the reference document": {
				// A control: the merge sequence is a refused form, which
				// refuses the call first.
				before: "l: &l {cost: 3}\nspec:\n  <<: [*l, *b]\n",
				path:   "$.l.cost",
				ref:    "base: &b {other: 4}\n",
				fn:     remove,
				err: "test.yaml:3:3: $.spec: cafe.jacobcolvin.com/v1 removes $.l.cost, which changes the keys of " +
					"a merge through a merge key that merges a sequence of aliases, and this program does not " +
					"change the keys of such a merge",
				form: true,
			},
			"removal of an own key of a mapping that merges a sequence with the reference document": {
				// A control: the merge sequence is a refused form, which
				// refuses the call first.
				before: "l: &l {price: 3}\nspec:\n  <<: [*l, *b]\n  cost: 3\n",
				ref:    "base: &b {other: 4}\n",
				fn:     remove,
				err: "test.yaml:3:3: $.spec: cafe.jacobcolvin.com/v1 removes $.spec.cost, which changes the keys " +
					"of a merge through a merge key that merges a sequence of aliases, and this program does not " +
					"change the keys of such a merge",
				form: true,
			},
			"removal of an own entry of the root that merges the reference document": {
				// No call sets the root, so the fix offers a merge or the
				// entries.
				before: "<<: *b\ntop: 1\n",
				path:   "$.top",
				ref:    "base: &b {other: 4}\n",
				fn:     remove,
				err: "test.yaml:2:1: $.top: cafe.jacobcolvin.com/v1 removes $.top, but <<: *b at 1:1 reads a " +
					"reference document, which decides which names $ reads\n" +
					"|-- 1:1: $: <<: *b reads a reference document here\n" +
					"|-- merge a mapping of the document, or write the entries that the document needs in the text\n" +
					"`-- cafe.jacobcolvin.com/v1 removes $.top",
			},
			"set of the value of an own entry": {
				// A control: the Set keeps the names of the mapping.
				before: block,
				ref:    "base: &b {cost: 4}\n",
				fn:     set(5),
				want:   "spec:\n  <<: *b\n  cost: 5\n",
			},
			"removal below an own entry": {
				// A control: the mapping keeps its names.
				before: "spec:\n  <<: *b\n  cost: {a: 1, b: 2}\n",
				path:   "$.spec.cost.a",
				ref:    "base: &b {cost: 4}\n",
				fn:     remove,
				want:   "spec:\n  <<: *b\n  cost: {b: 2}\n",
			},
			"rename to a key that a document anchor brings in from the reference document": {
				before: "l: &l\n  <<: *b\nspec:\n  <<: *l\n  cost: 3\n",
				ref:    "base: &b {price: 4}\n",
				fn:     rename("price"),
				err: `test.yaml:4:3: $.spec: cafe.jacobcolvin.com/v1 renames $.spec.cost to "price", which ` +
					`changes the keys of a merge through a merge key that merges a mapping with a merge key of ` +
					`its own, and this program does not change the keys of such a merge`,
				form: true,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				refs := tc.refs
				if refs == nil {
					refs = []string{tc.ref}
				}

				sources := make([]*niceyaml.Source, 0, len(refs))
				for _, ref := range refs {
					sources = append(sources, niceyaml.NewSourceFromString(ref))
				}

				doc := yamltest.FirstDocument(t, tc.before, niceyaml.WithName("test.yaml"),
					niceyaml.WithReferences(sources...))

				d, err := edit.New(doc)
				require.NoError(t, err)

				path := spec.Child("cost")
				if tc.path != "" {
					path = paths.MustParse(tc.path)
				}

				err = d.Apply(t.Context(), path, tc.fn, label)
				if tc.err != "" {
					require.NotErrorIs(t, err, edit.ErrEngine)
					assert.Equal(t, !tc.form, errors.Is(err, paths.ErrReferenceAlias))
					assert.Equal(t, !tc.form, errors.Is(err, paths.ErrAlias),
						"every error that matches paths.ErrReferenceAlias matches paths.ErrAlias")
					assert.Equal(t, tc.form, errors.Is(err, edit.ErrUnsupported))
				}

				requireEdit(t, d, err, tc.err, tc.want)
			})
		}
	})
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

// mergeLists returns a document of depth mappings after the first, in
// which each mapping merges the mapping before it twice.
func mergeLists(depth int) string {
	var text strings.Builder

	text.WriteString("m0: &m0\n  k0: 0\n")

	for i := 1; i <= depth; i++ {
		fmt.Fprintf(&text, "m%d: &m%d\n  <<: [*m%d, *m%d]\n  k%d: %d\n", i, i, i-1, i-1, i, i)
	}

	return text.String()
}

// numbered returns n lines, each of which format gives for its index.
func numbered(n int, format string) string {
	var text strings.Builder

	for i := range n {
		fmt.Fprintf(&text, format+"\n", i)
	}

	return text.String()
}

// aliasFan returns the lines of a mapping, indented by indent, whose
// entry a holds an anchored mapping. The entries b0 to b<levels-1> follow.
// Each one is a flow sequence of ten aliases of the entry before it, and
// each one but the last has an anchor. The aliases of the last entry thus
// read a 10^levels times.
func aliasFan(indent string, levels int) string {
	var text strings.Builder

	text.WriteString(indent + "a: &a {k: 1}\n")

	prev := "a"

	for i := range levels {
		name := fmt.Sprintf("b%d", i)

		anchor := ""
		if i < levels-1 {
			anchor = "&" + name + " "
		}

		aliases := strings.TrimSuffix(strings.Repeat("*"+prev+", ", 10), ", ")
		fmt.Fprintf(&text, "%s%s: %s[%s]\n", indent, name, anchor, aliases)

		prev = name
	}

	return text.String()
}

func TestDoc_Apply_MergeLists(t *testing.T) {
	t.Parallel()

	// A walk that reads a merge source each time a merge key lists it
	// doubles its work at every level. At this depth, such a walk runs for
	// days.
	d := newDoc(t, mergeLists(40), niceyaml.WithAliasLimit(false))
	stop := errors.New("stop")

	err := d.Apply(t.Context(), paths.Doc(), func(_ context.Context, n *edit.Node) error {
		assert.True(t, n.At(paths.MustParse("$.m40.k0")).Exists())

		return stop
	})
	require.ErrorIs(t, err, stop)
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
			err: `test.yaml:5:3: $.spec.closes: the edit renames $.spec.closes to "close", but <<: *summer at 4:3 also brings in closes, so $.spec still reads closes`,
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

func TestDoc_Apply_FailedCall(t *testing.T) {
	t.Parallel()

	// Each row runs a call that fails, then reads the Doc in the same Func.
	// The reads find the text as the call found it.
	tcs := map[string]struct {
		call edit.Func
		// The path of a node that the Func takes before the call, and the
		// text that it reads after the call.
		held     string
		heldText string
		text     string
		err      string
		// Whether the error of the call matches edit.ErrEngine.
		engine bool
	}{
		"set of a control character": {
			// Go-yaml's encoder writes U+0001 as it is, so the set fails
			// after it wrote.
			text:     stringtest.LinesLF("a: 1", "z: 1"),
			call:     func(ctx context.Context, n *edit.Node) error { return n.At(paths.Doc().Child("a")).Set(ctx, "\x01") },
			held:     "$.z",
			heldText: "1",
			err:      "go-yaml's encoder writes U+0001 as it is",
			engine:   true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.text)

			err := d.Apply(t.Context(), paths.Doc(), func(ctx context.Context, n *edit.Node) error {
				held := n.At(paths.MustParse(tc.held))

				callErr := tc.call(ctx, n)
				require.Error(t, callErr)
				assert.Equal(t, tc.engine, errors.Is(callErr, edit.ErrEngine))
				assert.Contains(t, callErr.Error(), tc.err)

				assert.Equal(t, tc.text, d.Text())
				assert.Empty(t, d.Removals())
				assert.Equal(t, tc.heldText, held.Text())

				got, err := n.Decode[any](ctx)
				require.NoError(t, err)

				want, err := yamltest.FirstDocument(t, tc.text).Decode[any](ctx)
				require.NoError(t, err)
				assert.Equal(t, want, got)

				return nil
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.err)
			assert.Equal(t, tc.text, d.Text())
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

func TestDoc_Apply_Limits(t *testing.T) {
	t.Parallel()

	// The Source of each text holds at most 64 bytes and 3 levels. An Apply
	// whose last text breaks a limit fails with the error of the Source.
	// The byte limit is a limit of the whole file. Its error binds where an
	// error at the path $ binds, at the site of the root, and names no call
	// (fix27 P83). The depth-limit error binds at the token that opens the
	// first level past the limit.
	tcs := map[string]struct {
		fn     edit.Func
		before string
		want   string
		err    string
	}{
		"value that nests past the depth limit": {
			before: "a: 1\n",
			fn: func(ctx context.Context, root *edit.Node) error {
				return root.At(paths.Doc().Child("b")).Set(ctx, map[string]any{"x": map[string]any{"y": []any{1}}})
			},
			err: "test.yaml:1:1: $: document nests more than 3 levels",
		},
		"value that nests past the depth limit below a value of the user": {
			// The level past the limit lies in a value that the Set created
			// inside the mapping that it wrote over the 2. So the problem binds
			// at the site of that mapping, its key n, as an error at a created
			// value binds (fix25 P78).
			before: "a: 1\nm:\n  n: 2\n",
			fn: func(ctx context.Context, root *edit.Node) error {
				return root.At(paths.MustParse("$.m.n")).Set(ctx, map[string]any{"x": []any{1}})
			},
			err: "test.yaml:3:3: $.m.n: document nests more than 3 levels",
		},
		"value past the byte limit": {
			before: "a: 1\nb: 2\n",
			fn: func(ctx context.Context, root *edit.Node) error {
				return root.At(paths.Doc().Child("b")).Set(ctx, strings.Repeat("x", 64))
			},
			err: "test.yaml:1:1: $: file is larger than 64 bytes\n\n",
		},
		"rename past the byte limit": {
			before: "a: 1\nb: 2\n",
			fn: func(_ context.Context, root *edit.Node) error {
				return root.At(paths.Doc().Child("b")).SetKey(strings.Repeat("k", 64))
			},
			err: "test.yaml:1:1: $: file is larger than 64 bytes\n\n",
		},
		"move past the byte limit": {
			before: "o: " + strings.Repeat("x", 40) + "\nm:\n  z: 1\nn:\n  p: 1\n",
			fn: func(_ context.Context, root *edit.Node) error {
				return root.At(paths.Doc().Child("n")).Move(paths.MustParse("$.m.k"))
			},
			err: "test.yaml:1:1: $: file is larger than 64 bytes\n\n",
		},
		// Control: the move takes the key a, so the site of the root is the
		// next key that the user wrote and the edits kept, b. The check after
		// the last call names no call, so the problem has no detail that
		// names the Move that created the mapping.
		"mapping that a Move created past the byte limit": {
			before: "a: 1\nb: " + strings.Repeat("x", 56),
			fn: func(_ context.Context, root *edit.Node) error {
				return root.At(paths.Doc().Child("a")).Move(paths.MustParse("$.n.p"))
			},
			err: "test.yaml:2:1: $: file is larger than 64 bytes\n\n",
		},
		// The removal takes the key a, so the site of the root is the key b,
		// which the Set keeps.
		"value past the byte limit after a removal of the first key": {
			before: "a: 1\nb: 2\n",
			fn: func(ctx context.Context, root *edit.Node) error {
				err := root.At(paths.Doc().Child("a")).Remove()
				if err != nil {
					return err
				}

				return root.At(paths.Doc().Child("b")).Set(ctx, strings.Repeat("x", 64))
			},
			err: "test.yaml:2:1: $: file is larger than 64 bytes\n\n",
		},
		"value past the byte limit in a text with CRLF line breaks": {
			before: "a: 1\r\nb: 2\r\n",
			fn: func(ctx context.Context, root *edit.Node) error {
				return root.At(paths.Doc().Child("b")).Set(ctx, strings.Repeat("x", 64))
			},
			err: "test.yaml:1:1: $: file is larger than 64 bytes\n\n",
		},
		"value past the byte limit in a text without a final line break": {
			before: "a: 1\nb: 2",
			fn: func(ctx context.Context, root *edit.Node) error {
				return root.At(paths.Doc().Child("b")).Set(ctx, strings.Repeat("x", 64))
			},
			err: "test.yaml:1:1: $: file is larger than 64 bytes\n\n",
		},
		// The limit counts the comment line and the header, and the site of
		// the root is the first key of the document, a.
		"value past the byte limit in a document after a header": {
			before: "# c\n---\na: 1\nb: 2\n",
			fn: func(ctx context.Context, root *edit.Node) error {
				return root.At(paths.Doc().Child("b")).Set(ctx, strings.Repeat("x", 64))
			},
			err: "test.yaml:3:1: $: file is larger than 64 bytes\n\n",
		},
		// The site of a root sequence is the first token of its first item.
		"item past the byte limit in a root sequence": {
			before: "- 1\n- 2\n",
			fn: func(ctx context.Context, root *edit.Node) error {
				return root.At(paths.MustParse("$[1]")).Set(ctx, strings.Repeat("x", 64))
			},
			err: "test.yaml:1:3: $: file is larger than 64 bytes\n\n",
		},
		"value past the byte limit that a later call removes": {
			before: "a: 1\nb: 2\n",
			fn: func(ctx context.Context, root *edit.Node) error {
				err := root.At(paths.Doc().Child("b")).Set(ctx, strings.Repeat("x", 64))
				if err != nil {
					return err
				}

				return root.At(paths.Doc().Child("b")).Remove()
			},
			want: "a: 1\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before, niceyaml.WithByteLimit(64), niceyaml.WithDepthLimit(3))

			err := d.Apply(t.Context(), paths.Doc(), tc.fn)
			if tc.err != "" {
				require.ErrorIs(t, err, niceyaml.ErrLimit)
				require.NotErrorIs(t, err, edit.ErrEngine)
				assert.False(t, niceyaml.IsInvalid(err))
			}

			requireEdit(t, d, err, tc.err, tc.want)
		})
	}
}

func TestDoc_Locate(t *testing.T) {
	t.Parallel()

	d := newDoc(t, stringtest.LinesLF("spec:", "  cost: 45 # kr"))
	require.NoError(t, applyAt(t, d, spec.Child("cost"), func(_ context.Context, n *edit.Node) error {
		return n.SetKey("price")
	}))

	// An error that the caller binds keeps its path, and binds where the
	// user wrote the value at that path (fix21 P65).
	err := d.Node().Bind(niceyaml.NewError("too cheap", niceyaml.AtPath(spec.Child("price"))))
	assert.Equal(t, "test.yaml:2:9: $.spec.price: too cheap", err.Error())

	err = d.Node().Bind(niceyaml.NewError("deprecated", niceyaml.AtPath(spec.Child("price").Key())))
	assert.Equal(t, "test.yaml:2:3: $.spec.price~: deprecated", err.Error())

	err = d.Node().Bind(niceyaml.NewError("missing", niceyaml.AtPath(spec.Child("close"))))
	assert.Equal(t, "test.yaml:1:1: $.spec.close: missing", err.Error())

	require.NoError(t, applyAt(t, d, spec.Child("price"), func(ctx context.Context, n *edit.Node) error {
		return n.Set(ctx, "free")
	}))

	err = d.Node().Bind(niceyaml.NewError("not a number", niceyaml.AtPath(spec.Child("price"))))
	assert.Contains(t, niceyaml.FormatError(err), stringtest.JoinLF(
		"test.yaml:2:9: $.spec.price: not a number",
		"`-- cafe.jacobcolvin.com/v1 sets $.spec.price from this value",
	))
}

func TestDoc_Locate_Ranges(t *testing.T) {
	t.Parallel()

	b := paths.Doc().Child("b")

	tcs := map[string]struct {
		// The Func of an Apply at the root, or nil for a Doc without an
		// edit.
		edit   edit.Func
		before string
		at     paths.Path
		// The range of an error at the path at, as line:col-line:col.
		want string
	}{
		"value that ends in multi-byte characters": {
			before: stringtest.LinesLF("a: 日本", "b: 1"),
			at:     paths.Doc().Child("a"),
			want:   "0:3-0:5",
		},
		"value that ends in multi-byte characters after an edit": {
			before: stringtest.LinesLF("a: 日本", "b: 1"),
			edit:   func(ctx context.Context, n *edit.Node) error { return n.At(b).Set(ctx, 2) },
			at:     paths.Doc().Child("a"),
			want:   "0:3-0:5",
		},
		"value that ends in a multi-byte character under a renamed key": {
			before: stringtest.LinesLF("a: xé", "b: 1"),
			edit:   func(_ context.Context, n *edit.Node) error { return n.At(paths.Doc().Child("a")).SetKey("c") },
			at:     paths.Doc().Child("c"),
			want:   "0:3-0:5",
		},
		"key that ends in multi-byte characters after an edit": {
			before: stringtest.LinesLF("日本: 1", "b: 1"),
			edit:   func(ctx context.Context, n *edit.Node) error { return n.At(b).Set(ctx, 2) },
			at:     paths.MustParse("$.日本~"),
			want:   "0:0-0:2",
		},
		"value that ends in an ASCII character after an edit": {
			before: stringtest.LinesLF("a: 日本x", "b: 1"),
			edit:   func(ctx context.Context, n *edit.Node) error { return n.At(b).Set(ctx, 2) },
			at:     paths.Doc().Child("a"),
			want:   "0:3-0:6",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before)
			if tc.edit != nil {
				require.NoError(t, d.Apply(t.Context(), paths.Doc(), tc.edit))
			}

			err := d.Node().Bind(niceyaml.NewError("bad", niceyaml.AtPath(tc.at)))

			var se *niceyaml.SourceError

			require.ErrorAs(t, err, &se)

			rng, ok := se.Range()
			require.True(t, ok)

			got := fmt.Sprintf("%d:%d-%d:%d", rng.Start.Line, rng.Start.Col, rng.End.Line, rng.End.Col)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestDoc_Locate_Root(t *testing.T) {
	t.Parallel()

	set := func(path paths.Path, v any) edit.Func {
		return func(ctx context.Context, n *edit.Node) error { return n.At(path).Set(ctx, v) }
	}

	tcs := map[string]struct {
		// The Func of an Apply at the root.
		edit   edit.Func
		before string
		// The path of the error, or "" for $.
		at   string
		want string
	}{
		"mapping after a set": {
			before: stringtest.LinesLF("a: 1", "spec:", "  closes: 19"),
			edit:   set(spec.Child("closes"), 20),
			want:   "test.yaml:1:1: $: bad",
		},
		"key of the root after a set": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			edit:   set(paths.Doc().Child("b"), 3),
			at:     "$~",
			want:   "test.yaml:1:1: $~: bad",
		},
		"mapping after a rename of its first key": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			edit:   func(_ context.Context, n *edit.Node) error { return n.At(paths.Doc().Child("a")).SetKey("c") },
			want:   "test.yaml:1:1: $: bad",
		},
		"mapping after a removal of its first key": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			edit:   func(_ context.Context, n *edit.Node) error { return n.At(paths.Doc().Child("a")).Remove() },
			want:   "test.yaml:2:1: $: bad",
		},
		"mapping after a document header": {
			before: stringtest.LinesLF("# menu", "---", "a: 1", "b: 2"),
			edit:   set(paths.Doc().Child("b"), 3),
			want:   "test.yaml:3:1: $: bad",
		},
		"sequence after a set": {
			before: stringtest.LinesLF("- 1", "- 2"),
			edit:   set(paths.MustParse("$[1]"), 3),
			want:   "test.yaml:1:3: $: bad",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before)
			require.NoError(t, d.Apply(t.Context(), paths.Doc(), tc.edit))

			at := paths.Doc()
			if tc.at != "" {
				at = paths.MustParse(tc.at)
			}

			err := d.Node().Bind(niceyaml.NewError("bad", niceyaml.AtPath(at)))
			assert.Equal(t, tc.want, err.Error())
		})
	}
}

func TestDoc_Locate_Emptied(t *testing.T) {
	t.Parallel()

	remove := func(path string) edit.Func {
		return func(_ context.Context, n *edit.Node) error { return n.At(paths.MustParse(path)).Remove() }
	}

	set := func(path string, v any) edit.Func {
		return func(ctx context.Context, n *edit.Node) error { return n.At(paths.MustParse(path)).Set(ctx, v) }
	}

	move := func(from, to string) edit.Func {
		return func(_ context.Context, n *edit.Node) error {
			return n.At(paths.MustParse(from)).Move(paths.MustParse(to))
		}
	}

	tcs := map[string]struct {
		before string
		// The path of the error, and of the value that a last Apply removes.
		at   string
		want string
		// The detail of the error, or "" for an error without one.
		detail string
		// The removal that a last Apply makes at at, as Doc.Removals lists
		// it, when set.
		removal string
		// The Func of each Apply at the root, in order.
		applies []edit.Func
		// The error of the Apply that fails, when one fails. The row then
		// checks nothing else.
		err string
	}{
		"value that a set wrote over an emptied sequence": {
			before:  stringtest.LinesLF("l:", "  - 1", "f: 0"),
			applies: []edit.Func{remove("$.l[0]"), set("$.l", map[string]any{"z": 1})},
			at:      "$.l.z",
			// The Set creates the value inside the one that it writes, so a
			// caller's error there keeps its path (fix20 B1).
			want:   "test.yaml:1:1: $.l.z: bad",
			detail: "cafe.jacobcolvin.com/v1 sets $.l from this value",
		},
		"value that a set wrote over an emptied mapping": {
			before:  stringtest.LinesLF("m:", "  a: 1", "f: 0"),
			applies: []edit.Func{remove("$.m.a"), set("$.m", map[string]any{"z": map[string]any{"y": 1}})},
			at:      "$.m.z.y",
			want:    "test.yaml:1:1: $.m.z.y: bad",
			detail:  "cafe.jacobcolvin.com/v1 sets $.m from this value",
		},
		"value that a set wrote over an emptied mapping on the line of its item": {
			before:  stringtest.LinesLF("s:", "  - a: 1"),
			applies: []edit.Func{remove("$.s[0].a"), set("$.s[0]", map[string]any{"z": 1})},
			at:      "$.s[0].z",
			want:    "test.yaml:2:3: $.s[0].z: bad",
			detail:  "cafe.jacobcolvin.com/v1 sets $.s[0] from this value",
		},
		"value that a set wrote over an emptied sequence on the line of its item": {
			before:  stringtest.LinesLF("s:", "  - - 1"),
			applies: []edit.Func{remove("$.s[0][0]"), set("$.s[0]", map[string]any{"z": 1})},
			at:      "$.s[0].z",
			want:    "test.yaml:2:3: $.s[0].z: bad",
			detail:  "cafe.jacobcolvin.com/v1 sets $.s[0] from this value",
		},
		"value that a set wrote over an emptied sequence of a renamed key": {
			before: stringtest.LinesLF("l:", "  - 1", "f: 0"),
			applies: []edit.Func{
				func(_ context.Context, n *edit.Node) error { return n.At(paths.Doc().Child("l")).SetKey("m") },
				remove("$.m[0]"),
				set("$.m", map[string]any{"z": 1}),
			},
			at:     "$.m.z",
			want:   "test.yaml:1:1: $.m.z: bad",
			detail: "cafe.jacobcolvin.com/v1 sets $.m from this value",
		},
		"emptied mapping at the root": {
			before:  stringtest.LinesLF("a: 1"),
			applies: []edit.Func{remove("$.a")},
			at:      "$",
			want:    "test.yaml:1:1: $: bad",
		},
		// With no item kept, the root binds at the first byte of its
		// content in the user's text, the "-" of a block sequence (fix20 B4).
		"emptied sequence at the root": {
			before:  stringtest.LinesLF("- 1"),
			applies: []edit.Func{remove("$[0]")},
			at:      "$",
			want:    "test.yaml:1:1: $: bad",
		},
		"emptied mapping at the root on the line of its tag": {
			before:  stringtest.LinesLF("!!map a: 1"),
			applies: []edit.Func{remove("$.a")},
			at:      "$",
			want:    "test.yaml:1:7: $: bad",
		},
		"emptied mapping item of a flow sequence without braces": {
			before:  stringtest.LinesLF("f: [a: 1, 2]"),
			applies: []edit.Func{remove("$.f[0].a")},
			at:      "$.f[0]",
			err:     "test.yaml:1:5: $.f[0].a: cafe.jacobcolvin.com/v1 removes $.f[0].a, which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"mapping item of a flow sequence without braces that a move emptied": {
			before: stringtest.LinesLF("f: [a: 1, 2]"),
			applies: []edit.Func{
				func(_ context.Context, n *edit.Node) error {
					return n.At(paths.MustParse("$.f[0].a")).Move(paths.Doc().Child("q"))
				},
			},
			at:  "$.f[0]",
			err: "test.yaml:1:5: $.f[0].a: cafe.jacobcolvin.com/v1 moves $.f[0].a to $.q, which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"emptied mapping item of a flow sequence without braces moved to a new key": {
			before: stringtest.LinesLF("f: [a: 1, 2]"),
			applies: []edit.Func{
				remove("$.f[0].a"),
				func(_ context.Context, n *edit.Node) error {
					return n.At(paths.MustParse("$.f[0]")).Move(paths.Doc().Child("q"))
				},
			},
			at:  "$.q",
			err: "test.yaml:1:5: $.f[0].a: cafe.jacobcolvin.com/v1 removes $.f[0].a, which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"emptied mapping item of a flow sequence without braces with a renamed key": {
			before: stringtest.LinesLF("f: [0, a: 1]"),
			applies: []edit.Func{
				func(_ context.Context, n *edit.Node) error { return n.At(paths.MustParse("$.f[1].a")).SetKey("b") },
				remove("$.f[1].b"),
			},
			at:  "$.f[1]",
			err: `test.yaml:1:8: $.f[1].a: cafe.jacobcolvin.com/v1 renames $.f[1].a to "b", which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair`,
		},
		"value that a set wrote in an emptied mapping item of a flow sequence without braces": {
			before:  stringtest.LinesLF("f: [a: 1, 2]"),
			applies: []edit.Func{remove("$.f[0].a"), set("$.f[0].c", 3)},
			at:      "$.f[0].c",
			err:     "test.yaml:1:5: $.f[0].a: cafe.jacobcolvin.com/v1 removes $.f[0].a, which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"item that holds an emptied mapping": {
			before:  stringtest.LinesLF("l:", "  - a:", "      b: 1", "  - 2"),
			applies: []edit.Func{remove("$.l[0].a.b")},
			at:      "$.l[0]",
			want:    "test.yaml:2:3: $.l[0]: bad",
			removal: `$.l[0] "cafe.jacobcolvin.com/v1" "" key="" item=true 1:2-2:10`,
		},
		"mapping item of a flow sequence without braces moved to a new key, then emptied": {
			before: stringtest.LinesLF("f: [a: 1, 2]"),
			applies: []edit.Func{
				func(_ context.Context, n *edit.Node) error {
					return n.At(paths.MustParse("$.f[0]")).Move(paths.Doc().Child("q"))
				},
				remove("$.q.a"),
			},
			at:  "$.q",
			err: "test.yaml:1:5: $.f[0].a: cafe.jacobcolvin.com/v1 moves $.f[0] to $.q, which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"mapping item of a flow sequence without braces over two lines, moved to a new key, then emptied": {
			before: stringtest.LinesLF("f: [a:", "    1, 2]"),
			applies: []edit.Func{
				func(_ context.Context, n *edit.Node) error {
					return n.At(paths.MustParse("$.f[0]")).Move(paths.Doc().Child("q"))
				},
				remove("$.q.a"),
			},
			at:  "$.q",
			err: "test.yaml:1:5: $.f[0].a: cafe.jacobcolvin.com/v1 moves $.f[0] to $.q, which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"mapping item of a flow sequence without braces moved into a flow sequence, then emptied": {
			before: stringtest.LinesLF("f: [a: 1, 2]", "g: [0]"),
			applies: []edit.Func{
				func(_ context.Context, n *edit.Node) error {
					return n.At(paths.MustParse("$.f[0]")).Move(paths.MustParse("$.g[1]"))
				},
				remove("$.g[1].a"),
			},
			at:  "$.g[1]",
			err: "test.yaml:1:5: $.f[0].a: cafe.jacobcolvin.com/v1 moves $.f[0] to $.g[1], which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"mapping item of a flow sequence without braces moved into a flow sequence and emptied in one apply": {
			before: stringtest.LinesLF("f: [a: 1, 2]", "g: [0]"),
			applies: []edit.Func{
				func(_ context.Context, n *edit.Node) error {
					err := n.At(paths.MustParse("$.f[0]")).Move(paths.MustParse("$.g[1]"))
					if err != nil {
						return err
					}

					return n.At(paths.MustParse("$.g[1].a")).Remove()
				},
			},
			at:  "$.g[1]",
			err: "test.yaml:1:5: $.f[0].a: cafe.jacobcolvin.com/v1 moves $.f[0] to $.g[1], which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"mapping item of a flow sequence without braces moved past the last item of a block sequence, then emptied": {
			before: stringtest.LinesLF("f: [a: 1, 2]", "l:", "  - 0"),
			applies: []edit.Func{
				func(_ context.Context, n *edit.Node) error {
					return n.At(paths.MustParse("$.f[0]")).Move(paths.MustParse("$.l[1]"))
				},
				remove("$.l[1].a"),
			},
			at:  "$.l[1]",
			err: "test.yaml:1:5: $.f[0].a: cafe.jacobcolvin.com/v1 moves $.f[0] to $.l[1], which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"mapping item of a flow sequence without braces moved to a new key, emptied, then given an entry": {
			before:  stringtest.LinesLF("f: [a: 1, 2]"),
			applies: []edit.Func{move("$.f[0]", "$.q"), remove("$.q.a"), set("$.q.c", 1)},
			at:      "$.q",
			err:     "test.yaml:1:5: $.f[0].a: cafe.jacobcolvin.com/v1 moves $.f[0] to $.q, which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"mapping item of a flow sequence without braces moved to a new key, emptied by a move, then given an entry": {
			before:  stringtest.LinesLF("f: [a: 1, 2]"),
			applies: []edit.Func{move("$.f[0]", "$.q"), move("$.q.a", "$.z"), set("$.q.c", map[string]any{"d": 1})},
			at:      "$.q",
			err:     "test.yaml:1:5: $.f[0].a: cafe.jacobcolvin.com/v1 moves $.f[0] to $.q, which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"mapping item of a flow sequence without braces moved twice, emptied, then given an entry": {
			before:  stringtest.LinesLF("f: [a: 1, 2]", "m:", "  k: 0"),
			applies: []edit.Func{move("$.f[0]", "$.q"), move("$.q", "$.m.r"), remove("$.m.r.a"), set("$.m.r.c", 1)},
			at:      "$.m.r",
			err:     "test.yaml:1:5: $.f[0].a: cafe.jacobcolvin.com/v1 moves $.f[0] to $.q, which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"mapping item of a flow sequence without braces moved past the last item of a block sequence, emptied, then given an entry": {
			before:  stringtest.LinesLF("f: [a: 1, 2]", "l:", "  - 0"),
			applies: []edit.Func{move("$.f[0]", "$.l[1]"), remove("$.l[1].a"), set("$.l[1].c", 1)},
			at:      "$.l[1]",
			err:     "test.yaml:1:5: $.f[0].a: cafe.jacobcolvin.com/v1 moves $.f[0] to $.l[1], which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"mapping item of a flow sequence without braces at the root moved past its last item, emptied, then given an entry": {
			before:  stringtest.LinesLF("- [a: 1, 2]"),
			applies: []edit.Func{move("$[0][0]", "$[1]"), remove("$[1].a"), set("$[1].c", 1)},
			at:      "$[1]",
			err:     "test.yaml:1:4: $[0][0].a: cafe.jacobcolvin.com/v1 moves $[0][0] to $[1], which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"mapping item of a flow sequence without braces moved to a new key, emptied, and given an entry in one apply": {
			before: stringtest.LinesLF("f: [a: 1, 2]"),
			applies: []edit.Func{
				func(ctx context.Context, n *edit.Node) error {
					for _, fn := range []edit.Func{move("$.f[0]", "$.q"), remove("$.q.a"), set("$.q.c", 1)} {
						err := fn(ctx, n)
						if err != nil {
							return err
						}
					}

					return nil
				},
			},
			at:  "$.q",
			err: "test.yaml:1:5: $.f[0].a: cafe.jacobcolvin.com/v1 moves $.f[0] to $.q, which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"mapping item of a flow sequence without braces moved to a new key, emptied, then given an entry from after it": {
			before:  stringtest.LinesLF("f: [a: 1, 2]", "z: 5"),
			applies: []edit.Func{move("$.f[0]", "$.q"), remove("$.q.a"), move("$.z", "$.q.c")},
			at:      "$.q",
			err:     "test.yaml:1:5: $.f[0].a: cafe.jacobcolvin.com/v1 moves $.f[0] to $.q, which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"mapping item of a flow sequence without braces moved past the last item of a block sequence, emptied, then given an entry from after it": {
			before:  stringtest.LinesLF("f: [a: 1, 2]", "z: 5", "l:", "  - 0"),
			applies: []edit.Func{move("$.f[0]", "$.l[1]"), remove("$.l[1].a"), move("$.z", "$.l[1].c")},
			at:      "$.l[1]",
			err:     "test.yaml:1:5: $.f[0].a: cafe.jacobcolvin.com/v1 moves $.f[0] to $.l[1], which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"mapping item of a flow sequence without braces moved into a flow sequence, then emptied by a move": {
			before: stringtest.LinesLF("f: [a: 1, 2]", "g: [0]"),
			applies: []edit.Func{
				func(_ context.Context, n *edit.Node) error {
					return n.At(paths.MustParse("$.f[0]")).Move(paths.MustParse("$.g[1]"))
				},
				func(_ context.Context, n *edit.Node) error {
					return n.At(paths.MustParse("$.g[1].a")).Move(paths.Doc().Child("z"))
				},
			},
			at:  "$.g[1]",
			err: "test.yaml:1:5: $.f[0].a: cafe.jacobcolvin.com/v1 moves $.f[0] to $.g[1], which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"mapping item of a block sequence emptied in place": {
			before:  stringtest.LinesLF("l:", "  - a: 1", "  - 2"),
			applies: []edit.Func{remove("$.l[0].a")},
			at:      "$.l[0]",
			want:    "test.yaml:2:3: $.l[0]: bad",
			removal: `$.l[0] "cafe.jacobcolvin.com/v1" "" key="" item=true 1:2-1:8`,
		},
		"mapping item of a block sequence with two entries emptied in one apply": {
			before: stringtest.LinesLF("l:", "  - a: 1", "    b: [1, 2]", "  - 2"),
			applies: []edit.Func{
				func(ctx context.Context, n *edit.Node) error {
					err := remove("$.l[0].a")(ctx, n)
					if err != nil {
						return err
					}

					return remove("$.l[0].b")(ctx, n)
				},
			},
			at:      "$.l[0]",
			want:    "test.yaml:2:3: $.l[0]: bad",
			removal: `$.l[0] "cafe.jacobcolvin.com/v1" "" key="" item=true 1:2-2:13`,
		},
		"sequence item of a block sequence emptied in place": {
			before:  stringtest.LinesLF("l:", "  - - 1", "  - 2"),
			applies: []edit.Func{remove("$.l[0][0]")},
			at:      "$.l[0]",
			want:    "test.yaml:2:3: $.l[0]: bad",
			removal: `$.l[0] "cafe.jacobcolvin.com/v1" "" key="" item=true 1:2-1:7`,
		},
		"mapping item of a block sequence at the root emptied in place": {
			before:  stringtest.LinesLF("- a: 1", "- 2"),
			applies: []edit.Func{remove("$[0].a")},
			at:      "$[0]",
			want:    "test.yaml:1:1: $[0]: bad",
			removal: `$[0] "cafe.jacobcolvin.com/v1" "" key="" item=true 0:0-0:6`,
		},
		"mapping item of a block sequence emptied in place, then given an entry": {
			before:  stringtest.LinesLF("l:", "  - a: 1", "  - 2"),
			applies: []edit.Func{remove("$.l[0].a"), set("$.l[0].c", 1)},
			at:      "$.l[0]",
			want:    "test.yaml:2:3: $.l[0]: bad",
			removal: `$.l[0] "cafe.jacobcolvin.com/v1" "" key="" item=true 1:2-1:8`,
		},
		"mapping item of a block sequence emptied in place, then given an entry from after it": {
			before:  stringtest.LinesLF("l:", "  - a: 1", "  - 2", "z: 5"),
			applies: []edit.Func{remove("$.l[0].a"), move("$.z", "$.l[0].c")},
			at:      "$.l[0]",
			want:    "test.yaml:2:3: $.l[0]: bad",
			removal: `$.l[0] "cafe.jacobcolvin.com/v1" "" key="" item=true 1:2-1:8`,
		},
		"alias item that holds the copy of its anchored value after a removal of the anchor": {
			before:  stringtest.LinesLF("x: &x", "  a: 1", "l:", "  - *x", "  - 2"),
			applies: []edit.Func{remove("$.x")},
			at:      "$.l[0]",
			err:     "test.yaml:4:5: $.l[0]: cafe.jacobcolvin.com/v1 removes $.x, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"emptied mapping item of a flow sequence without braces that ends in a multi-byte character": {
			before:  stringtest.LinesLF("f: [a: é, 2]"),
			applies: []edit.Func{remove("$.f[0].a")},
			at:      "$.f[0]",
			err:     "test.yaml:1:5: $.f[0].a: cafe.jacobcolvin.com/v1 removes $.f[0].a, which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"emptied mapping item of a flow sequence without braces that ends in a multi-byte character, moved to a new key": {
			before:  stringtest.LinesLF("f: [a: é, 2]"),
			applies: []edit.Func{remove("$.f[0].a"), move("$.f[0]", "$.q")},
			at:      "$.q",
			err:     "test.yaml:1:5: $.f[0].a: cafe.jacobcolvin.com/v1 removes $.f[0].a, which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"mapping item of a flow sequence without braces that ends in a multi-byte character, moved to a new key, emptied, then given an entry": {
			before:  stringtest.LinesLF("f: [a: é, 2]"),
			applies: []edit.Func{move("$.f[0]", "$.q"), remove("$.q.a"), set("$.q.c", 1)},
			at:      "$.q",
			err:     "test.yaml:1:5: $.f[0].a: cafe.jacobcolvin.com/v1 moves $.f[0] to $.q, which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"mapping item of a flow sequence without braces that ends in multi-byte characters, moved past the last item of a block sequence, emptied, then given an entry": {
			before:  stringtest.LinesLF("f: [a: 日本, 2]", "l:", "  - 0"),
			applies: []edit.Func{move("$.f[0]", "$.l[1]"), remove("$.l[1].a"), set("$.l[1].c", 1)},
			at:      "$.l[1]",
			err:     "test.yaml:1:5: $.f[0].a: cafe.jacobcolvin.com/v1 moves $.f[0] to $.l[1], which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before)

			for _, fn := range tc.applies {
				if failsWith(t, d.Apply(t.Context(), paths.Doc(), fn, label), tc.err) {
					return
				}
			}

			require.Empty(t, tc.err, "every Apply passes")

			err := d.Node().Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.MustParse(tc.at))))
			assert.Equal(t, tc.want, err.Error())

			details := strings.Split(niceyaml.FormatError(err), "`-- ")[1:]
			for i, detail := range details {
				details[i], _, _ = strings.Cut(detail, "\n")
			}

			assert.Equal(t, tc.detail, strings.Join(details, "; "))

			if tc.removal == "" {
				return
			}

			require.NoError(t, d.Apply(t.Context(), paths.MustParse(tc.at), remove("@"), label))

			removals := d.Removals()
			require.NotEmpty(t, removals)
			assert.Equal(t, tc.removal, removalString(removals[len(removals)-1]))
		})
	}
}

func TestDoc_Locate_Created(t *testing.T) {
	t.Parallel()

	var (
		q    = paths.Doc().Child("q")
		text = stringtest.LinesLF("a: 1", "spec:", "  closes: 19", "l:", "  - 0")
	)

	tcs := map[string]struct {
		before string
		// The Func of an Apply at the root that runs before the Set, or nil.
		earlier edit.Func
		// The path of the value that the Set creates or replaces, and of the
		// error.
		at   paths.Path
		want string
		// The key that a later Apply gives the created value, or "".
		rename string
		// The error that binds at the key of the renamed value.
		wantKey string
	}{
		// The user's text holds no token for a value that an edit created,
		// so a caller's error there binds at the nearest value above that the
		// user wrote (fix20 P61). It keeps the path that the caller gave,
		// also where an edit renamed or moved a value on it (fix21 P65).
		"value in a mapping of the user": {
			before: text,
			at:     spec.Child("new"),
			want:   "test.yaml:2:1: $.spec.new: bad",
		},
		"value in a renamed mapping": {
			before:  text,
			earlier: func(_ context.Context, n *edit.Node) error { return n.At(spec).SetKey("q") },
			at:      q.Child("new"),
			want:    "test.yaml:2:1: $.q.new: bad",
		},
		"value in a moved mapping": {
			before:  text,
			earlier: func(_ context.Context, n *edit.Node) error { return n.At(spec).Move(q) },
			at:      q.Child("new"),
			want:    "test.yaml:2:1: $.q.new: bad",
		},
		"value in a mapping moved past the last item of a block sequence": {
			before:  text,
			earlier: func(_ context.Context, n *edit.Node) error { return n.At(spec).Move(paths.MustParse("$.l[1]")) },
			at:      paths.MustParse("$.l[1].new"),
			want:    "test.yaml:2:1: $.l[1].new: bad",
		},
		"value in a mapping item of a flow sequence moved to a new key": {
			before: stringtest.LinesLF("f: [0, {a: 1}]", "g: {k: 0}"),
			earlier: func(_ context.Context, n *edit.Node) error {
				return n.At(paths.MustParse("$.f[1]")).Move(paths.MustParse("$.g.q"))
			},
			at:   paths.MustParse("$.g.q.new"),
			want: "test.yaml:1:8: $.g.q.new: bad",
		},
		"value in a new mapping at the root": {
			before: text,
			at:     paths.MustParse("$.x.y"),
			want:   "test.yaml:1:1: $.x.y: bad",
		},
		// A value that a Set writes over a value of the user binds where
		// the old value binds, and a collection binds at its key or its "-"
		// (fix20 G1-3).
		"value over a block mapping": {
			before: text,
			at:     spec,
			want:   "test.yaml:2:1: $.spec: bad",
		},
		"value over a block sequence": {
			before: text,
			at:     paths.Doc().Child("l"),
			want:   "test.yaml:4:1: $.l: bad",
		},
		"value over a block mapping item": {
			before: stringtest.LinesLF("l:", "  - a: 1", "    b: 2"),
			at:     paths.MustParse("$.l[0]"),
			want:   "test.yaml:2:3: $.l[0]: bad",
		},
		"value at the root, then renamed": {
			before:  text,
			at:      paths.Doc().Child("x"),
			want:    "test.yaml:1:1: $.x: bad",
			rename:  "y",
			wantKey: "test.yaml:1:1: $.y~: bad",
		},
		"value in a mapping of the user, then renamed": {
			before:  text,
			at:      spec.Child("new"),
			want:    "test.yaml:2:1: $.spec.new: bad",
			rename:  "newer",
			wantKey: "test.yaml:2:1: $.spec.newer~: bad",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before)

			if tc.earlier != nil {
				require.NoError(t, d.Apply(t.Context(), paths.Doc(), tc.earlier))
			}

			require.NoError(t, applyAt(t, d, tc.at, func(ctx context.Context, n *edit.Node) error {
				return n.Set(ctx, "x")
			}))

			err := d.Node().Bind(niceyaml.NewError("bad", niceyaml.AtPath(tc.at)))
			assert.Equal(t, tc.want, err.Error())
			assert.Contains(t, niceyaml.FormatError(err), "`-- cafe.jacobcolvin.com/v1 sets "+tc.at.String())

			if tc.rename == "" {
				return
			}

			require.NoError(t, applyAt(t, d, tc.at, func(_ context.Context, n *edit.Node) error {
				return n.SetKey(tc.rename)
			}))

			parent, _ := tc.at.Parent()

			err = d.Node().Bind(niceyaml.NewError("bad", niceyaml.AtPath(parent.Child(tc.rename).Key())))
			assert.Equal(t, tc.wantKey, err.Error())
		})
	}
}

func TestDoc_Locate_Through(t *testing.T) {
	t.Parallel()

	var (
		setO = func(ctx context.Context, n *edit.Node) error { return n.At(paths.Doc().Child("o")).Set(ctx, 1) }
		setN = func(ctx context.Context, n *edit.Node) error {
			return n.At(paths.MustParse("$.m.n")).Set(ctx, map[string]any{"p": 1})
		}
		aliasEmpty   = stringtest.LinesLF("o: 0", "base: &b", "  e:", "  f: 1", "svc: *b")
		aliasMissing = stringtest.LinesLF("o: 0", "base: &b", "  sub: {k: 1}", "svc: *b")
	)

	tcs := map[string]struct {
		before string
		// The Func of an Apply at the root that runs before the error binds,
		// or nil.
		earlier edit.Func
		// The path of the error.
		at   paths.Path
		want string
		// A detail of the error, or "" for an error without a detail.
		detail string
	}{
		"empty value through an alias": {
			before: aliasEmpty,
			at:     paths.MustParse("$.svc.e"),
			want:   "test.yaml:3:5: $.svc.e: bad",
		},
		"control: empty value through an alias after an edit": {
			before:  aliasEmpty,
			earlier: setO,
			at:      paths.MustParse("$.svc.e"),
			want:    "test.yaml:3:5: $.svc.e: bad",
		},
		"empty item through an alias": {
			before: stringtest.LinesLF("o: 0", "b: &b", "  - ", "  - 1", "l: *b"),
			at:     paths.MustParse("$.l[0]"),
			want:   "test.yaml:3:4: $.l[0]: bad",
		},
		"empty value in a mapping that a merge key writes in place": {
			before: stringtest.LinesLF("o: 0", "m:", "  <<:", "    e:", "    f: 1", "  g: 2"),
			at:     paths.MustParse("$.m.e"),
			want:   "test.yaml:4:7: $.m.e: bad",
		},
		// The user's text holds no token for a missing field, so a caller's
		// error there keeps its path and binds at the nearest value above
		// that the user wrote (fix20 P61).
		"missing field below a value through an alias": {
			before: aliasMissing,
			at:     paths.MustParse("$.svc.sub.missing"),
			want:   "test.yaml:3:3: $.svc.sub.missing: bad",
		},
		"missing field below a value through an alias after an edit": {
			before:  aliasMissing,
			earlier: setO,
			at:      paths.MustParse("$.svc.sub.missing"),
			want:    "test.yaml:3:3: $.svc.sub.missing: bad",
		},
		"missing field below a value through a merge key": {
			before: stringtest.LinesLF("base: &b", "  sub: {k: 1}", "svc:", "  <<: *b", "  g: 1"),
			at:     paths.MustParse("$.svc.sub.missing"),
			want:   "test.yaml:2:3: $.svc.sub.missing: bad",
		},
		"control: field through an alias": {
			before: aliasMissing,
			at:     paths.MustParse("$.svc.sub.k"),
			want:   "test.yaml:3:12: $.svc.sub.k: bad",
		},
		"missing field below a created value": {
			before:  stringtest.LinesLF("m:", "  a: 1"),
			earlier: setN,
			at:      paths.MustParse("$.m.n.zz"),
			want:    "test.yaml:1:1: $.m.n.zz: bad",
			detail:  "`-- the edit sets $.m.n\n",
		},
		"key of a created entry": {
			before:  stringtest.LinesLF("m:", "  a: 1"),
			earlier: setN,
			at:      paths.MustParse("$.m.n.p~"),
			want:    "test.yaml:1:1: $.m.n.p~: bad",
			detail:  "test.yaml:1:1: $.m.n.p~: bad\n",
		},
		"control: created value": {
			before:  stringtest.LinesLF("m:", "  a: 1"),
			earlier: setN,
			at:      paths.MustParse("$.m.n.p"),
			want:    "test.yaml:1:1: $.m.n.p: bad",
			detail:  "test.yaml:1:1: $.m.n.p: bad\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before)

			if tc.earlier != nil {
				require.NoError(t, d.Apply(t.Context(), paths.Doc(), tc.earlier))
			}

			bad := niceyaml.NewError("bad", niceyaml.AtPath(tc.at))

			err := d.Node().Bind(bad)
			assert.Equal(t, tc.want, err.Error())

			if tc.detail != "" {
				assert.Contains(t, niceyaml.FormatError(err), tc.detail)
			}

			// An error that a Func returns binds there too.
			err = d.Apply(t.Context(), paths.Doc(), func(context.Context, *edit.Node) error { return bad })
			require.Error(t, err)
			assert.Equal(t, tc.want, err.Error())
		})
	}
}

func TestDoc_Locate_Moved(t *testing.T) {
	t.Parallel()

	// A step moves the value at from to the path to. It renames the key of
	// the value to key when key is set, sets the value to value when value
	// is set, and removes the value when remove is true.
	type step struct {
		value  any
		from   paths.Path
		to     paths.Path
		key    string
		remove bool
	}

	var (
		l0   = paths.MustParse("$.l[0]")
		l1   = paths.MustParse("$.l[1]")
		f1   = paths.MustParse("$.f[1]")
		g1   = paths.MustParse("$.g[1]")
		q    = paths.Doc().Child("q")
		aq   = paths.MustParse("$.a.q")
		gq   = paths.MustParse("$.g.q")
		text = stringtest.LinesLF("spec:", "  closes: 19", "l:", "  - 0")
		flow = stringtest.LinesLF("spec:", "  closes: 19", "f: [1]")
	)

	tcs := map[string]struct {
		before string
		// The steps of each Apply, in order.
		applies [][]step
		// The path of the error that binds after the Applies, and of the
		// value that a last Apply removes.
		at paths.Path
		// The error that binds at that path, when set.
		want string
		// The error that binds at the key of at, when set.
		wantKey string
		// The detail of the error at at that names an edit, or "" when the
		// error binds at text of the user that the edit kept.
		detail string
		// The removal that a last Apply makes at at, as Doc.Removals lists
		// it, when set.
		removal string
		// The error of the first Apply that fails, the last one included,
		// when one fails. The row checks nothing after that Apply.
		err string
	}{
		"mapping moved past the last item of a block sequence": {
			before:  text,
			applies: [][]step{{{from: spec, to: l1}}},
			at:      l1,
			want:    "test.yaml:1:1: $.l[1]: bad",
			removal: `$.spec~ "cafe.jacobcolvin.com/v1" "" key="spec" item=false 0:0-0:4`,
		},
		"entry of a mapping moved past the last item of a block sequence": {
			before:  text,
			applies: [][]step{{{from: spec, to: l1}}},
			at:      paths.MustParse("$.l[1].closes"),
			want:    "test.yaml:2:11: $.l[1].closes: bad",
			removal: `$.spec.closes~ "cafe.jacobcolvin.com/v1" "" key="closes" item=false 1:2-1:8`,
		},
		"mapping item moved past the last item of its sequence": {
			before:  stringtest.LinesLF("k:", "  - closes: 19", "  - 0"),
			applies: [][]step{{{from: paths.MustParse("$.k[0]"), to: paths.MustParse("$.k[2]")}}},
			at:      paths.MustParse("$.k[1]"),
			want:    "test.yaml:2:3: $.k[1]: bad",
			removal: `$.k[0] "cafe.jacobcolvin.com/v1" "" key="" item=true 1:2-1:14`,
		},
		"flow sequence moved past the last item of a block sequence": {
			before:  stringtest.LinesLF("spec: [1, 2]", "l:", "  - 0"),
			applies: [][]step{{{from: spec, to: l1}}},
			at:      l1,
			want:    "test.yaml:1:1: $.l[1]: bad",
			removal: `$.spec~ "cafe.jacobcolvin.com/v1" "" key="spec" item=false 0:0-0:4`,
		},
		"mapping moved to a new key": {
			before:  text,
			applies: [][]step{{{from: spec, to: q}}},
			at:      q,
			want:    "test.yaml:1:1: $.q: bad",
			wantKey: "test.yaml:1:1: $.q~: bad",
			removal: `$.spec~ "cafe.jacobcolvin.com/v1" "" key="spec" item=false 0:0-0:4`,
		},
		"mapping moved to a new key of a mapping": {
			before:  stringtest.LinesLF("a:", "  b: 1", text),
			applies: [][]step{{{from: spec, to: aq}}},
			at:      aq,
			want:    "test.yaml:3:1: $.a.q: bad",
			removal: `$.spec~ "cafe.jacobcolvin.com/v1" "" key="spec" item=false 2:0-2:4`,
		},
		"mapping moved to a new key, then past the last item of a block sequence": {
			before:  text,
			applies: [][]step{{{from: spec, to: q}}, {{from: q, to: l1}}},
			at:      l1,
			want:    "test.yaml:1:1: $.l[1]: bad",
			removal: `$.spec~ "cafe.jacobcolvin.com/v1" "" key="spec" item=false 0:0-0:4`,
		},
		"mapping moved to a new key and past the last item of a block sequence in one Apply": {
			before:  text,
			applies: [][]step{{{from: spec, to: q}, {from: q, to: l1}}},
			at:      l1,
			want:    "test.yaml:1:1: $.l[1]: bad",
			removal: `$.spec~ "cafe.jacobcolvin.com/v1" "" key="spec" item=false 0:0-0:4`,
		},
		"mapping moved to a new key of a mapping, then past the last item of a block sequence": {
			before:  stringtest.LinesLF("a:", "  b: 1", text),
			applies: [][]step{{{from: spec, to: aq}, {from: aq, to: l1}}},
			at:      l1,
			want:    "test.yaml:3:1: $.l[1]: bad",
			removal: `$.spec~ "cafe.jacobcolvin.com/v1" "" key="spec" item=false 2:0-2:4`,
		},
		"renamed mapping moved past the last item of a block sequence": {
			before:  stringtest.LinesLF("a: 1", text),
			applies: [][]step{{{from: spec, key: "q"}}, {{from: q, to: l1}}},
			at:      l1,
			want:    "test.yaml:2:1: $.l[1]: bad",
			removal: `$.spec~ "cafe.jacobcolvin.com/v1" "" key="spec" item=false 1:0-1:4`,
		},
		"renamed mapping": {
			before:  stringtest.LinesLF("a: 1", text),
			applies: [][]step{{{from: spec, key: "q"}}},
			at:      q,
			want:    "test.yaml:2:1: $.q: bad",
			wantKey: "test.yaml:2:1: $.q~: bad",
			removal: `$.spec~ "cafe.jacobcolvin.com/v1" "" key="spec" item=false 1:0-1:4`,
		},
		"missing entry of a renamed mapping": {
			before:  stringtest.LinesLF("a: 1", text),
			applies: [][]step{{{from: spec, key: "q"}}},
			at:      q.Child("missing"),
			want:    "test.yaml:2:1: $.q.missing: bad",
		},
		"missing entry of a moved mapping": {
			before:  text,
			applies: [][]step{{{from: spec, to: q}}},
			at:      q.Child("missing", "deeper"),
			want:    "test.yaml:1:1: $.q.missing.deeper: bad",
		},
		"missing entry of a mapping moved past the last item of a block sequence": {
			before:  text,
			applies: [][]step{{{from: spec, to: l1}}},
			at:      l1.Child("missing"),
			want:    "test.yaml:1:1: $.l[1].missing: bad",
		},
		"missing entry of an anchored mapping moved into a flow sequence": {
			before:  stringtest.LinesLF("d: &d", "  port: 80", "f: [1]"),
			applies: [][]step{{{from: paths.Doc().Child("d"), to: paths.MustParse("$.f[1]")}}},
			at:      paths.MustParse("$.f[1].missing"),
			err:     "test.yaml:1:4: $.d: cafe.jacobcolvin.com/v1 moves $.d to $.f[1], which puts a block value inside a flow collection, and cafe does not move a block value into a flow collection",
		},
		"entry with an explicit key moved to a new key": {
			before:  stringtest.LinesLF("spec:", "  ? b", "  : x", "c: 1"),
			applies: [][]step{{{from: spec.Child("b"), to: q}}},
			at:      q,
			err:     "test.yaml:2:3: $.spec.b: cafe.jacobcolvin.com/v1 moves $.spec.b to $.q, which reaches an entry with an explicit key, and cafe does not edit such an entry other than by removing it",
		},
		"renamed entry with an explicit key moved to a new key": {
			before:  stringtest.LinesLF("spec:", "  ? b", "  : x", "c: 1"),
			applies: [][]step{{{from: spec.Child("b"), key: "n"}}, {{from: spec.Child("n"), to: q}}},
			at:      q,
			err:     `test.yaml:2:3: $.spec.b: cafe.jacobcolvin.com/v1 renames $.spec.b to "n", which reaches an entry with an explicit key, and cafe does not edit such an entry other than by removing it`,
		},
		"renamed entry with an explicit key": {
			before:  stringtest.LinesLF("spec:", "  ? b", "  : x", "c: 1"),
			applies: [][]step{{{from: spec.Child("b"), key: "n"}}},
			at:      spec.Child("n"),
			err:     `test.yaml:2:3: $.spec.b: cafe.jacobcolvin.com/v1 renames $.spec.b to "n", which reaches an entry with an explicit key, and cafe does not edit such an entry other than by removing it`,
		},
		"renamed entry with an explicit key at the root": {
			before:  stringtest.LinesLF("? bb", ": x", "c: 1"),
			applies: [][]step{{{from: paths.Doc().Child("bb"), key: "nnnn"}}},
			at:      paths.Doc().Child("nnnn"),
			err:     `test.yaml:1:1: $.bb: cafe.jacobcolvin.com/v1 renames $.bb to "nnnn", which reaches an entry with an explicit key, and cafe does not edit such an entry other than by removing it`,
		},
		"empty value of a renamed key": {
			before:  stringtest.LinesLF("a:", "b: 1"),
			applies: [][]step{{{from: paths.Doc().Child("a"), key: "c"}}},
			at:      paths.Doc().Child("c"),
			want:    "test.yaml:1:3: $.c: bad",
			removal: `$.a~ "cafe.jacobcolvin.com/v1" "" key="a" item=false 0:0-0:1`,
		},
		"mapping moved to a key of a new mapping": {
			before:  stringtest.LinesLF("a: 1", text),
			applies: [][]step{{{from: spec, to: paths.MustParse("$.x.q")}}},
			at:      paths.Doc().Child("x"),
			// The move created $.x, so the error keeps its path (fix20 P61),
			// and the detail names the Move (fix21 P65).
			want:    "test.yaml:1:1: $.x: bad",
			detail:  "cafe.jacobcolvin.com/v1 moves $.spec to $.x.q",
			removal: `$.spec~ "cafe.jacobcolvin.com/v1" "" key="spec" item=false 1:0-1:4`,
		},
		"item of a block sequence that an entry from after it moved into": {
			before:  stringtest.LinesLF("l:", "  - a: 1", "z: 5"),
			applies: [][]step{{{from: paths.Doc().Child("z"), to: paths.MustParse("$.l[0].c")}}},
			at:      paths.MustParse("$.l[0]"),
			removal: `$.l[0] "cafe.jacobcolvin.com/v1" "" key="" item=true 1:2-1:8`,
		},
		"item of a block sequence that an entry from before it moved into": {
			before:  stringtest.LinesLF("z: 5", "l:", "  - a: 1"),
			applies: [][]step{{{from: paths.Doc().Child("z"), to: paths.MustParse("$.l[0].c")}}},
			at:      paths.MustParse("$.l[0]"),
			removal: `$.l[0] "cafe.jacobcolvin.com/v1" "" key="" item=true 2:2-2:8`,
		},
		"item of a block sequence that a mapping from after it moved into": {
			before:  stringtest.LinesLF("l:", "  - a: 1", "  - b: 2", "z:", "  x: 1", "  y: 2"),
			applies: [][]step{{{from: paths.Doc().Child("z"), to: paths.MustParse("$.l[0].c")}}},
			at:      paths.MustParse("$.l[0]"),
			removal: `$.l[0] "cafe.jacobcolvin.com/v1" "" key="" item=true 1:2-1:8`,
		},
		"item of a block sequence that an entry moved into below a mapping": {
			before:  stringtest.LinesLF("l:", "  - a: 1", "    m:", "      k: 0", "z: 5"),
			applies: [][]step{{{from: paths.Doc().Child("z"), to: paths.MustParse("$.l[0].m.c")}}},
			at:      paths.MustParse("$.l[0]"),
			removal: `$.l[0] "cafe.jacobcolvin.com/v1" "" key="" item=true 1:2-3:10`,
		},
		"item of a block sequence whose first entry moved to a new key of the item": {
			before:  stringtest.LinesLF("l:", "  - a: 1", "    b: 2"),
			applies: [][]step{{{from: paths.MustParse("$.l[0].a"), to: paths.MustParse("$.l[0].z")}}},
			at:      paths.MustParse("$.l[0]"),
			removal: `$.l[0] "cafe.jacobcolvin.com/v1" "" key="" item=true 1:2-2:8`,
		},
		"item of a block sequence with a value set over an entry that moved into it": {
			before: stringtest.LinesLF("l:", "  - a: 1", "z: 5"),
			applies: [][]step{
				{{from: paths.Doc().Child("z"), to: paths.MustParse("$.l[0].c")}},
				{{from: paths.MustParse("$.l[0].c"), value: 7}},
			},
			at:      paths.MustParse("$.l[0]"),
			removal: `$.l[0] "cafe.jacobcolvin.com/v1" "" key="" item=true 1:2-1:8`,
		},
		"item of a block sequence set over an entry that moved into it": {
			before: stringtest.LinesLF("l:", "  - a: 1", "z: 5"),
			applies: [][]step{
				{{from: paths.Doc().Child("z"), to: paths.MustParse("$.l[0].c")}},
				{{from: paths.MustParse("$.l[0]"), value: 7}},
			},
			at:      paths.MustParse("$.l[0]"),
			removal: `$.l[0] "cafe.jacobcolvin.com/v1" "" key="" item=true 1:2-1:8`,
		},
		"item of a block sequence that a mapping item of a flow sequence without braces moved into": {
			before:  stringtest.LinesLF("f: [a: 1, 2]", "l:", "  - k: 0"),
			applies: [][]step{{{from: paths.MustParse("$.f[0]"), to: paths.MustParse("$.l[0].m")}}},
			at:      paths.MustParse("$.l[0]"),
			err:     "test.yaml:1:5: $.f[0].a: cafe.jacobcolvin.com/v1 moves $.f[0] to $.l[0].m, which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"empty entry of a flow mapping moved into a flow sequence": {
			before:  stringtest.LinesLF("g: {a: 1, b:}", "f: [1]"),
			applies: [][]step{{{from: paths.MustParse("$.g.b"), to: f1}}},
			at:      f1,
			want:    "test.yaml:1:11: $.f[1]: bad",
			removal: `$.g.b~ "cafe.jacobcolvin.com/v1" "" key="b" item=false 0:10-0:11`,
		},
		"mapping moved into a flow sequence": {
			before:  flow,
			applies: [][]step{{{from: spec, to: f1}}},
			at:      f1,
			err:     "test.yaml:2:3: $.spec: cafe.jacobcolvin.com/v1 moves $.spec to $.f[1], which puts a block value inside a flow collection, and cafe does not move a block value into a flow collection",
		},
		"sequence moved into a flow sequence": {
			before:  stringtest.LinesLF("spec:", "  - 19", "  - 20", "f: [1]"),
			applies: [][]step{{{from: spec, to: f1}}},
			at:      f1,
			err:     "test.yaml:2:3: $.spec: cafe.jacobcolvin.com/v1 moves $.spec to $.f[1], which puts a block value inside a flow collection, and cafe does not move a block value into a flow collection",
		},
		"mapping item moved into a flow sequence": {
			before:  stringtest.LinesLF("l:", "  - closes: 19", "f: [1]"),
			applies: [][]step{{{from: paths.MustParse("$.l[0]"), to: f1}}},
			at:      f1,
			err:     "test.yaml:2:5: $.l[0]: cafe.jacobcolvin.com/v1 moves $.l[0] to $.f[1], which puts a block value inside a flow collection, and cafe does not move a block value into a flow collection",
		},
		"mapping moved into a flow sequence, then to a key": {
			before:  flow,
			applies: [][]step{{{from: spec, to: f1}}, {{from: f1, to: paths.Doc().Child("d")}}},
			at:      paths.Doc().Child("d"),
			err:     "test.yaml:2:3: $.spec: cafe.jacobcolvin.com/v1 moves $.spec to $.f[1], which puts a block value inside a flow collection, and cafe does not move a block value into a flow collection",
		},
		"mapping moved into a flow sequence, then to a key of a flow mapping": {
			before:  stringtest.LinesLF("spec:", "  a: 1", "f: [0]", "g: {k: 0}"),
			applies: [][]step{{{from: spec, to: f1}}, {{from: f1, to: gq}}},
			at:      gq,
			err:     "test.yaml:2:3: $.spec: cafe.jacobcolvin.com/v1 moves $.spec to $.f[1], which puts a block value inside a flow collection, and cafe does not move a block value into a flow collection",
		},
		"empty last value of a block mapping moved to a new key": {
			before:  stringtest.LinesLF("x: 1", "m:", "  a: 1", "  b:"),
			applies: [][]step{{{from: paths.MustParse("$.m"), to: q}}},
			at:      paths.MustParse("$.q.b"),
			want:    "test.yaml:4:5: $.q.b: bad",
			wantKey: "test.yaml:4:3: $.q.b~: bad",
		},
		"empty middle value of a block mapping moved to a new key": {
			before:  stringtest.LinesLF("x: 1", "m:", "  a: 1", "  b:", "  c: 2"),
			applies: [][]step{{{from: paths.MustParse("$.m"), to: q}}},
			at:      paths.MustParse("$.q.b"),
			want:    "test.yaml:4:5: $.q.b: bad",
			wantKey: "test.yaml:4:3: $.q.b~: bad",
		},
		"empty middle value of a block mapping of CRLF text moved to a new key": {
			before:  stringtest.LinesCRLF("x: 1", "m:", "  a: 1", "  b:", "  c: 2"),
			applies: [][]step{{{from: paths.MustParse("$.m"), to: q}}},
			at:      paths.MustParse("$.q.b"),
			want:    "test.yaml:4:5: $.q.b: bad",
		},
		"empty value of a block mapping moved into another mapping": {
			before:  stringtest.LinesLF("x:", "  y: 1", "m:", "  a: 1", "  b:", "  c: 2"),
			applies: [][]step{{{from: paths.MustParse("$.m"), to: paths.MustParse("$.x.n")}}},
			at:      paths.MustParse("$.x.n.b"),
			want:    "test.yaml:5:5: $.x.n.b: bad",
		},
		"empty value inside a block mapping moved past the last item of a block sequence": {
			before:  stringtest.LinesLF("m:", "  a: 1", "  b:", "  c: 2", "l:", "  - 0"),
			applies: [][]step{{{from: paths.MustParse("$.m"), to: l1}}},
			at:      paths.MustParse("$.l[1].b"),
			want:    "test.yaml:3:5: $.l[1].b: bad",
		},
		"empty value of a nested block mapping moved to a new key": {
			before:  stringtest.LinesLF("m:", "  a: 1", "  b:", "    c:", "    d: 1", "x: 1"),
			applies: [][]step{{{from: paths.MustParse("$.m.b"), to: q}}},
			at:      paths.MustParse("$.q.c"),
			want:    "test.yaml:4:7: $.q.c: bad",
		},
		"empty value of a mapping item moved to a new key": {
			before:  stringtest.LinesLF("l:", "  - a: 1", "    b:", "    c: 2", "  - 0"),
			applies: [][]step{{{from: paths.MustParse("$.l[0]"), to: q}}},
			at:      paths.MustParse("$.q.b"),
			want:    "test.yaml:3:7: $.q.b: bad",
		},
		"empty item of a block sequence moved with its sequence to a new key": {
			before:  stringtest.LinesLF("x: 1", "l:", "  - 0", "  -", "  - 2"),
			applies: [][]step{{{from: paths.MustParse("$.l"), to: q}}},
			at:      paths.MustParse("$.q[1]"),
			want:    "test.yaml:4:4: $.q[1]: bad",
		},
		"empty middle value of the copy that a removal writes at an alias": {
			before:  stringtest.LinesLF("x: &x", "  a: 1", "  b:", "  c: 2", "y: *x"),
			applies: [][]step{{{from: paths.MustParse("$.x"), remove: true}}},
			at:      paths.MustParse("$.y.b"),
			err:     "test.yaml:5:4: $.y: cafe.jacobcolvin.com/v1 removes $.x, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"empty last value of the copy that a removal writes at an alias": {
			before:  stringtest.LinesLF("x: &x", "  a: 1", "  b:", "y: *x"),
			applies: [][]step{{{from: paths.MustParse("$.x"), remove: true}}},
			at:      paths.MustParse("$.y.b"),
			err:     "test.yaml:4:4: $.y: cafe.jacobcolvin.com/v1 removes $.x, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"empty item under a renamed key": {
			before:  stringtest.LinesLF("l:", "  - 0", "  -"),
			applies: [][]step{{{from: paths.MustParse("$.l"), key: "k"}}},
			at:      paths.MustParse("$.k[1]"),
			want:    "test.yaml:3:4: $.k[1]: bad",
		},
		"empty item after a removal of the item before it": {
			before:  stringtest.LinesLF("l:", "  - 0", "  -"),
			applies: [][]step{{{from: paths.MustParse("$.l[0]"), remove: true}}},
			at:      paths.MustParse("$.l[0]"),
			want:    "test.yaml:3:4: $.l[0]: bad",
		},
		"empty value at the end of a text without a final newline": {
			before: "m:\n  a: 1\n  b:",
			at:     paths.MustParse("$.m.b"),
			want:   "test.yaml:3:5: $.m.b: bad",
		},
		"empty value at the end of a text without a final newline under a renamed key": {
			before:  "m:\n  a: 1\n  b:",
			applies: [][]step{{{from: paths.MustParse("$.m"), key: "k"}}},
			at:      paths.MustParse("$.k.b"),
			want:    "test.yaml:3:5: $.k.b: bad",
		},
		"empty value that a set replaces after a move of its mapping": {
			before: stringtest.LinesLF("x: 1", "m:", "  a: 1", "  b:", "  c: 2"),
			applies: [][]step{
				{{from: paths.MustParse("$.m"), to: q}},
				{{from: paths.MustParse("$.q.b"), value: 3}},
			},
			at:     paths.MustParse("$.q.b"),
			want:   "test.yaml:4:5: $.q.b: bad",
			detail: "cafe.jacobcolvin.com/v1 sets $.q.b from this value",
		},
		"empty last value that a set replaces after a move of its mapping": {
			before:  stringtest.LinesLF("x: 1", "m:", "  a: 1", "  b:"),
			applies: [][]step{{{from: paths.MustParse("$.m"), to: q}, {from: paths.MustParse("$.q.b"), value: 3}}},
			at:      paths.MustParse("$.q.b"),
			want:    "test.yaml:4:5: $.q.b: bad",
			detail:  "cafe.jacobcolvin.com/v1 sets $.q.b from this value",
		},
		"mapping that a set writes over an empty value after a move of its mapping": {
			before: stringtest.LinesLF("x: 1", "m:", "  a: 1", "  b:", "  c: 2"),
			applies: [][]step{
				{{from: paths.MustParse("$.m"), to: q}},
				{{from: paths.MustParse("$.q.b"), value: map[string]int{"p": 1}}},
			},
			at: paths.MustParse("$.q.b.p"),
			// The caller's error keeps the path that it gives (fix21 P65), and
			// binds at the key of the mapping that the Set wrote (fix20 B1).
			want:   "test.yaml:4:3: $.q.b.p: bad",
			detail: "cafe.jacobcolvin.com/v1 sets $.q.b from this value",
		},
		"empty item that a set replaces after a move of its sequence": {
			before: stringtest.LinesLF("x: 1", "l:", "  - 0", "  -", "  - 2"),
			applies: [][]step{
				{{from: paths.MustParse("$.l"), to: q}},
				{{from: paths.MustParse("$.q[1]"), value: 3}},
			},
			at:     paths.MustParse("$.q[1]"),
			want:   "test.yaml:4:4: $.q[1]: bad",
			detail: "cafe.jacobcolvin.com/v1 sets $.q[1] from this value",
		},
		"empty value that a set replaces in the copy that a removal writes at an alias": {
			before: stringtest.LinesLF("x: &x", "  a: 1", "  b:", "  c: 2", "y: *x"),
			applies: [][]step{
				{{from: paths.MustParse("$.x"), remove: true}},
				{{from: paths.MustParse("$.y.b"), value: 3}},
			},
			at:  paths.MustParse("$.y.b"),
			err: "test.yaml:5:4: $.y: cafe.jacobcolvin.com/v1 removes $.x, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"moved empty value that a set replaces": {
			before: stringtest.LinesLF("m:", "  a: 1", "  b:", "x: 1"),
			applies: [][]step{
				{{from: paths.MustParse("$.m.b"), to: q}},
				{{from: q, value: 3}},
			},
			at:     q,
			want:   "test.yaml:3:3: $.q: bad",
			detail: "cafe.jacobcolvin.com/v1 sets $.q from this value",
		},
		"moved empty item that a set replaces": {
			before: stringtest.LinesLF("l:", "  - 0", "  -", "x: 1"),
			applies: [][]step{
				{{from: l1, to: q}},
				{{from: q, value: 3}},
			},
			at:     q,
			want:   "test.yaml:3:3: $.q: bad",
			detail: "cafe.jacobcolvin.com/v1 sets $.q from this value",
		},
		"empty value moved into a created mapping that a set replaces": {
			before: stringtest.LinesLF("m:", "  a: 1", "  b:", "x: 1"),
			applies: [][]step{
				{{from: paths.MustParse("$.m.b"), to: paths.MustParse("$.k.q")}},
				{{from: paths.MustParse("$.k.q"), value: 3}},
			},
			at:     paths.MustParse("$.k.q"),
			want:   "test.yaml:3:3: $.k.q: bad",
			detail: "cafe.jacobcolvin.com/v1 sets $.k.q from this value",
		},
		"empty value that a set replaces after a move into a flow mapping": {
			before: stringtest.LinesLF("m:", "  a: 1", "  b:", "k: {z: 0}"),
			applies: [][]step{{
				{from: paths.MustParse("$.m.b"), to: paths.MustParse("$.k.q")},
				{from: paths.MustParse("$.k.q"), value: 3},
			}},
			at:     paths.MustParse("$.k.q"),
			want:   "test.yaml:3:3: $.k.q: bad",
			detail: "cafe.jacobcolvin.com/v1 sets $.k.q from this value",
		},
		"value that a set replaces after a move into a flow mapping": {
			before: stringtest.LinesLF("m:", "  a: 1", "  b: 0", "k: {z: 0}"),
			applies: [][]step{{
				{from: paths.MustParse("$.m.b"), to: paths.MustParse("$.k.q")},
				{from: paths.MustParse("$.k.q"), value: 3},
			}},
			at:     paths.MustParse("$.k.q"),
			want:   "test.yaml:3:6: $.k.q: bad",
			detail: "cafe.jacobcolvin.com/v1 sets $.k.q from this value",
		},
		"empty value that a set replaces after a move of its mapping into a flow mapping": {
			before: stringtest.LinesLF("m:", "  a: 1", "  b:", "k: {z: 0}"),
			applies: [][]step{
				{{from: paths.MustParse("$.m"), to: paths.MustParse("$.k.q")}},
				{{from: paths.MustParse("$.k.q.b"), value: 3}},
			},
			at:  paths.MustParse("$.k.q.b"),
			err: "test.yaml:2:3: $.m: cafe.jacobcolvin.com/v1 moves $.m to $.k.q, which puts a block value inside a flow collection, and cafe does not move a block value into a flow collection",
		},
		"empty value that a set replaces in the copy that a removal writes at an alias in a flow mapping": {
			before: stringtest.LinesLF("x: &x", "  a: 1", "  b:", "f: {k: *x}"),
			applies: [][]step{
				{{from: paths.MustParse("$.x"), remove: true}},
				{{from: paths.MustParse("$.f.k.b"), value: 3}},
			},
			at:  paths.MustParse("$.f.k.b"),
			err: "test.yaml:4:8: $.f.k: cafe.jacobcolvin.com/v1 removes $.x, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"value that a set replaces in the copy that a removal writes at an alias in a flow mapping": {
			before: stringtest.LinesLF("x: &x", "  a: 1", "  b: 0", "f: {k: *x}"),
			applies: [][]step{
				{{from: paths.MustParse("$.x"), remove: true}},
				{{from: paths.MustParse("$.f.k.b"), value: 3}},
			},
			at:  paths.MustParse("$.f.k.b"),
			err: "test.yaml:4:8: $.f.k: cafe.jacobcolvin.com/v1 removes $.x, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"empty value that a set replaces in the copy that a removal writes at an alias in a flow sequence": {
			before: stringtest.LinesLF("x: &x", "  a: 1", "  b:", "f: [*x]"),
			applies: [][]step{
				{{from: paths.MustParse("$.x"), remove: true}},
				{{from: paths.MustParse("$.f[0].b"), value: 3}},
			},
			at:  paths.MustParse("$.f[0].b"),
			err: "test.yaml:4:5: $.f[0]: cafe.jacobcolvin.com/v1 removes $.x, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"empty value of a flow mapping that a set replaces after a move of the mapping": {
			before: stringtest.LinesLF("m: {a: , b: 1}", "k: {z: 0}"),
			applies: [][]step{
				{{from: paths.MustParse("$.m"), to: paths.MustParse("$.k.np")}},
				{{from: paths.MustParse("$.k.np.a"), value: 3}},
			},
			at:     paths.MustParse("$.k.np.a"),
			want:   "test.yaml:1:5: $.k.np.a: bad",
			detail: "cafe.jacobcolvin.com/v1 sets $.k.np.a from this value",
		},
		"value of a flow mapping that a set replaces after a move of the mapping": {
			before: stringtest.LinesLF("m: {a: 0, b: 1}", "k: {z: 0}"),
			applies: [][]step{
				{{from: paths.MustParse("$.m"), to: paths.MustParse("$.k.np")}},
				{{from: paths.MustParse("$.k.np.a"), value: 3}},
			},
			at:     paths.MustParse("$.k.np.a"),
			want:   "test.yaml:1:8: $.k.np.a: bad",
			detail: "cafe.jacobcolvin.com/v1 sets $.k.np.a from this value",
		},
		"empty value that a set replaces under a renamed key": {
			before: stringtest.LinesLF("m:", "  a: 1", "  b:", "  c: 2"),
			applies: [][]step{
				{{from: paths.MustParse("$.m"), key: "k"}},
				{{from: paths.MustParse("$.k.b"), value: 3}},
			},
			at:     paths.MustParse("$.k.b"),
			want:   "test.yaml:3:5: $.k.b: bad",
			detail: "cafe.jacobcolvin.com/v1 sets $.k.b from this value",
		},
		"empty item that a set replaces under a renamed key": {
			before: stringtest.LinesLF("l:", "  - 0", "  -", "  - 2"),
			applies: [][]step{
				{{from: paths.MustParse("$.l"), key: "k"}},
				{{from: paths.MustParse("$.k[1]"), value: 3}},
			},
			at:     paths.MustParse("$.k[1]"),
			want:   "test.yaml:3:4: $.k[1]: bad",
			detail: "cafe.jacobcolvin.com/v1 sets $.k[1] from this value",
		},
		"empty value at the end of CRLF text that a set replaces": {
			before:  stringtest.LinesCRLF("m:", "  a: 1", "  b:"),
			applies: [][]step{{{from: paths.MustParse("$.m.b"), value: 3}}},
			at:      paths.MustParse("$.m.b"),
			want:    "test.yaml:3:6: $.m.b: bad",
			detail:  "cafe.jacobcolvin.com/v1 sets $.m.b from this value",
		},
		"empty value of a flow mapping that a set replaces": {
			before:  stringtest.LinesLF("f: {a: , b: 1}"),
			applies: [][]step{{{from: paths.MustParse("$.f.a"), value: 3}}},
			at:      paths.MustParse("$.f.a"),
			want:    "test.yaml:1:7: $.f.a: bad",
			detail:  "cafe.jacobcolvin.com/v1 sets $.f.a from this value",
		},
		"empty value of CRLF text that a move takes to the end of the text": {
			before:  stringtest.LinesCRLF("m:", "  a: 1", "  b:", "x: 1"),
			applies: [][]step{{{from: paths.MustParse("$.m"), to: q}}},
			at:      paths.MustParse("$.q.b"),
			want:    "test.yaml:3:5: $.q.b: bad",
		},
		"empty value of CRLF text that a move takes away from the end of the text": {
			before:  stringtest.LinesCRLF("x: 1", "m:", "  a: 1", "  b:"),
			applies: [][]step{{{from: paths.MustParse("$.m"), to: q}, {from: paths.MustParse("$.q.c"), value: 3}}},
			at:      paths.MustParse("$.q.b"),
			want:    "test.yaml:4:6: $.q.b: bad",
		},
		"empty item of CRLF text that a move takes to the end of the text": {
			before:  stringtest.LinesCRLF("l:", "  - 0", "  -", "x: 1"),
			applies: [][]step{{{from: paths.MustParse("$.l"), to: q}}},
			at:      paths.MustParse("$.q[1]"),
			want:    "test.yaml:3:4: $.q[1]: bad",
		},
		"empty value of the copy that a removal writes at an alias at the end of CRLF text": {
			before:  stringtest.LinesCRLF("x: &x", "  a: 1", "  b:", "y: *x"),
			applies: [][]step{{{from: paths.MustParse("$.x"), remove: true}}},
			at:      paths.MustParse("$.y.b"),
			err:     "test.yaml:4:4: $.y: cafe.jacobcolvin.com/v1 removes $.x, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"empty value with a comment that edits take to the end of the text": {
			before: stringtest.LinesLF("x: 1", "m:", "  a: 1", "  b: # bee", "  c: 2"),
			applies: [][]step{
				{{from: paths.MustParse("$.m"), to: q}},
				{{from: paths.MustParse("$.q.c"), remove: true}},
			},
			at:   paths.MustParse("$.q.b"),
			want: "test.yaml:4:5: $.q.b: bad",
		},
		"empty value with a comment at the end of the text that a move puts an entry after": {
			before:  stringtest.LinesLF("h: 8", "e:", "  f: 3", "  h: 3", "  d: # c"),
			applies: [][]step{{{from: paths.MustParse("$.e.h"), to: q}}},
			at:      paths.MustParse("$.e.d"),
			want:    "test.yaml:5:6: $.e.d: bad",
		},
		"empty item with a comment at the end of the text that a set puts an entry after": {
			before:  stringtest.LinesLF("x: 1", "l:", "  - 0", "  - # c"),
			applies: [][]step{{{from: paths.MustParse("$.l"), to: q}, {from: paths.MustParse("$.k"), value: 3}}},
			at:      paths.MustParse("$.q[1]"),
			want:    "test.yaml:4:5: $.q[1]: bad",
		},
		"empty value of a block mapping moved to a new key of a root that holds only that mapping": {
			before:  stringtest.LinesLF("m:", "  a: 1", "  b:"),
			applies: [][]step{{{from: paths.MustParse("$.m"), to: q}}},
			at:      paths.MustParse("$.q.b"),
			want:    "test.yaml:3:5: $.q.b: bad",
			wantKey: "test.yaml:3:3: $.q.b~: bad",
		},
		"empty value of a block mapping of CRLF text moved to a new key of a root that holds only that mapping": {
			before:  stringtest.LinesCRLF("m:", "  a: 1", "  b:"),
			applies: [][]step{{{from: paths.MustParse("$.m"), to: q}}},
			at:      paths.MustParse("$.q.b"),
			want:    "test.yaml:3:6: $.q.b: bad",
		},
		"anchored mapping moved past the last item of a block sequence": {
			before:  stringtest.LinesLF("a: &e", "  k: 1", "b: *e", "l:", "  - 0"),
			applies: [][]step{{{from: paths.Doc().Child("a"), to: l1}}},
			at:      l1,
			err:     "test.yaml:3:4: $.b: cafe.jacobcolvin.com/v1 moves $.a to $.l[1], after which this alias comes before the anchor that it reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored mapping moved to a new key": {
			before:  stringtest.LinesLF("a: &e", "  k: 1", "b: *e", "l:", "  - 0"),
			applies: [][]step{{{from: paths.Doc().Child("a"), to: q}}},
			at:      q,
			err:     "test.yaml:3:4: $.b: cafe.jacobcolvin.com/v1 moves $.a to $.q, after which this alias comes before the anchor that it reads, and cafe does not write a copy of a value at its alias",
		},
		"item moved to a new key": {
			before:  stringtest.LinesLF("l:", "  - closes: 19", "  - 5"),
			applies: [][]step{{{from: paths.MustParse("$.l[1]"), to: q}}},
			at:      q,
			want:    "test.yaml:3:5: $.q: bad",
			wantKey: "test.yaml:3:3: $.q~: bad",
			removal: `$.l[1] "cafe.jacobcolvin.com/v1" "" key="" item=true 2:2-2:5`,
		},
		"item moved to a new key, then renamed": {
			before:  stringtest.LinesLF("l:", "  - closes: 19", "  - 5"),
			applies: [][]step{{{from: paths.MustParse("$.l[1]"), to: q}}, {{from: q, key: "r"}}},
			at:      paths.Doc().Child("r"),
			want:    "test.yaml:3:5: $.r: bad",
			wantKey: "test.yaml:3:3: $.r~: bad",
			removal: `$.l[1] "cafe.jacobcolvin.com/v1" "" key="" item=true 2:2-2:5`,
		},
		"item of a flow sequence moved to a new key": {
			before:  stringtest.LinesLF("f: [0, 5]", "g: {k: 0}"),
			applies: [][]step{{{from: f1, to: gq}}},
			at:      gq,
			want:    "test.yaml:1:8: $.g.q: bad",
			wantKey: "test.yaml:1:8: $.g.q~: bad",
			removal: `$.f[1] "cafe.jacobcolvin.com/v1" "" key="" item=true 0:7-0:8`,
		},
		"tagged item of a flow sequence moved to a new key": {
			// Go-yaml reads a plain scalar under a local tag without an anchor
			// as YAML does.
			before:  stringtest.LinesLF("f: [0, !t x]"),
			applies: [][]step{{{from: f1, to: q}}},
			at:      q,
			want:    "test.yaml:1:11: $.q: bad",
			wantKey: "test.yaml:1:8: $.q~: bad",
			removal: `$.f[1] "cafe.jacobcolvin.com/v1" "" key="" item=true 0:7-0:11`,
		},
		"anchored item of a flow sequence moved to a new key": {
			before:  stringtest.LinesLF("f: [0, &a 5]"),
			applies: [][]step{{{from: f1, to: q}}},
			at:      q,
			want:    "test.yaml:1:11: $.q: bad",
			wantKey: "test.yaml:1:8: $.q~: bad",
			removal: `$.f[1] "cafe.jacobcolvin.com/v1" "" key="" item=true 0:7-0:11`,
		},
		"tagged mapping item of a flow sequence moved to an item of a block sequence": {
			before:  stringtest.LinesLF("f: [0, !!map {a: 1}]", "l:", "  - 0"),
			applies: [][]step{{{from: f1, to: l1}}},
			at:      l1,
			want:    "test.yaml:1:8: $.l[1]: bad",
			removal: `$.f[1] "cafe.jacobcolvin.com/v1" "" key="" item=true 0:7-0:19`,
		},
		"mapping item of a flow sequence without braces": {
			before:  stringtest.LinesLF("f: [0, a: 1]"),
			at:      f1,
			want:    "test.yaml:1:8: $.f[1]: bad",
			removal: `$.f[1] "cafe.jacobcolvin.com/v1" "" key="" item=true 0:7-0:11`,
		},
		"mapping item of a flow sequence without braces after another edit": {
			before:  stringtest.LinesLF("f: [0, a: 1]", "z: 1"),
			applies: [][]step{{{from: paths.Doc().Child("z"), value: 2}}},
			at:      f1,
			want:    "test.yaml:1:8: $.f[1]: bad",
			removal: `$.f[1] "cafe.jacobcolvin.com/v1" "" key="" item=true 0:7-0:11`,
		},
		"entry of a mapping item of a flow sequence without braces after another edit": {
			before:  stringtest.LinesLF("f: [0, a: {x: 1}]", "z: 1"),
			applies: [][]step{{{from: paths.Doc().Child("z"), value: 2}}},
			at:      f1.Child("a"),
			want:    "test.yaml:1:8: $.f[1].a: bad",
			wantKey: "test.yaml:1:8: $.f[1].a~: bad",
			err:     "test.yaml:1:8: $.f[1].a: cafe.jacobcolvin.com/v1 removes $.f[1].a, which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"mapping item of a flow sequence without braces that a move gave an entry": {
			before:  stringtest.LinesLF("f: [a: 1]", "z: 2"),
			applies: [][]step{{{from: paths.Doc().Child("z"), to: paths.MustParse("$.f[0].z")}}},
			at:      paths.MustParse("$.f[0]"),
			err:     "test.yaml:1:5: $.f[0].a: cafe.jacobcolvin.com/v1 moves $.z to $.f[0].z, which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"set mapping item of a flow sequence without braces": {
			before:  stringtest.LinesLF("f: [0, a: 1]"),
			applies: [][]step{{{from: f1, value: 5}}},
			at:      f1,
			want:    "test.yaml:1:8: $.f[1]: bad",
			detail:  "cafe.jacobcolvin.com/v1 sets $.f[1] from this value",
			removal: `$.f[1] "cafe.jacobcolvin.com/v1" "" key="" item=true 0:7-0:11`,
		},
		"mapping item of a flow sequence without braces with a renamed key": {
			before:  stringtest.LinesLF("f: [0, a: 1]"),
			applies: [][]step{{{from: f1.Child("a"), key: "b"}}},
			at:      f1,
			err:     `test.yaml:1:8: $.f[1].a: cafe.jacobcolvin.com/v1 renames $.f[1].a to "b", which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair`,
		},
		"renamed entry of a mapping item of a flow sequence without braces": {
			before:  stringtest.LinesLF("f: [0, a: 1, 2]"),
			applies: [][]step{{{from: f1.Child("a"), key: "b"}}},
			at:      f1.Child("b"),
			err:     `test.yaml:1:8: $.f[1].a: cafe.jacobcolvin.com/v1 renames $.f[1].a to "b", which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair`,
		},
		"missing entry of a mapping item of a flow sequence without braces with a renamed key": {
			before:  stringtest.LinesLF("f: [0, a: 1]"),
			applies: [][]step{{{from: f1.Child("a"), key: "b"}}},
			at:      f1.Child("missing"),
			err:     `test.yaml:1:8: $.f[1].a: cafe.jacobcolvin.com/v1 renames $.f[1].a to "b", which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair`,
		},
		"missing entry below the renamed key of a mapping item of a flow sequence without braces": {
			before:  stringtest.LinesLF("f: [0, a: {x: 1}]"),
			applies: [][]step{{{from: f1.Child("a"), key: "b"}}},
			at:      f1.Child("b", "missing", "deeper"),
			err:     `test.yaml:1:8: $.f[1].a: cafe.jacobcolvin.com/v1 renames $.f[1].a to "b", which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair`,
		},
		"missing entry of a mapping item of a flow sequence without braces with a renamed key that holds a mapping": {
			before:  stringtest.LinesLF("f: [0, a: {x: 1}]"),
			applies: [][]step{{{from: f1.Child("a"), key: "b"}}},
			at:      f1.Child("missing"),
			err:     `test.yaml:1:8: $.f[1].a: cafe.jacobcolvin.com/v1 renames $.f[1].a to "b", which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair`,
		},
		"missing entry of a mapping item without braces of a renamed flow sequence": {
			before:  stringtest.LinesLF("f: [0, a: 1]"),
			applies: [][]step{{{from: paths.Doc().Child("f"), key: "g"}}},
			at:      paths.MustParse("$.g[1].missing"),
			want:    "test.yaml:1:8: $.g[1].missing: bad",
		},
		"mapping item of a flow sequence without braces with a renamed key, then set": {
			before:  stringtest.LinesLF("f: [0, a: 1]"),
			applies: [][]step{{{from: f1.Child("a"), key: "b"}}, {{from: f1, value: 5}}},
			at:      f1,
			err:     `test.yaml:1:8: $.f[1].a: cafe.jacobcolvin.com/v1 renames $.f[1].a to "b", which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair`,
		},
		"mapping item of a flow sequence without braces with a renamed key, then given an entry": {
			before:  stringtest.LinesLF("f: [0, a: 1]"),
			applies: [][]step{{{from: f1.Child("a"), key: "b"}}, {{from: f1.Child("c"), value: 5}}},
			at:      f1,
			err:     `test.yaml:1:8: $.f[1].a: cafe.jacobcolvin.com/v1 renames $.f[1].a to "b", which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair`,
		},
		"entry that a set gave a mapping item of a flow sequence without braces with a renamed key": {
			before:  stringtest.LinesLF("f: [0, a: 1]"),
			applies: [][]step{{{from: f1.Child("a"), key: "b"}}, {{from: f1.Child("c"), value: 5}}},
			at:      f1.Child("c"),
			err:     `test.yaml:1:8: $.f[1].a: cafe.jacobcolvin.com/v1 renames $.f[1].a to "b", which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair`,
		},
		"mapping item of a flow sequence without braces with a renamed key, moved to a new key": {
			before:  stringtest.LinesLF("f: [0, a: 1]"),
			applies: [][]step{{{from: f1.Child("a"), key: "b"}}, {{from: f1, to: q}}},
			at:      q,
			err:     `test.yaml:1:8: $.f[1].a: cafe.jacobcolvin.com/v1 renames $.f[1].a to "b", which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair`,
		},
		"renamed entry of a mapping item of a flow sequence without braces, moved with the item to a new key": {
			before:  stringtest.LinesLF("f: [0, a: 1]"),
			applies: [][]step{{{from: f1.Child("a"), key: "b"}}, {{from: f1, to: q}}},
			at:      q.Child("b"),
			err:     `test.yaml:1:8: $.f[1].a: cafe.jacobcolvin.com/v1 renames $.f[1].a to "b", which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair`,
		},
		"mapping item of a flow sequence without braces with a renamed key, moved into a flow sequence": {
			before:  stringtest.LinesLF("f: [0, a: 1]", "g: [0]"),
			applies: [][]step{{{from: f1.Child("a"), key: "b"}}, {{from: f1, to: paths.MustParse("$.g[1]")}}},
			at:      paths.MustParse("$.g[1]"),
			err:     `test.yaml:1:8: $.f[1].a: cafe.jacobcolvin.com/v1 renames $.f[1].a to "b", which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair`,
		},
		"entry of a mapping item of a block sequence after another edit": {
			before:  stringtest.LinesLF("l:", "  - a:", "      x: 1", "z: 1"),
			applies: [][]step{{{from: paths.Doc().Child("z"), value: 2}}},
			at:      paths.MustParse("$.l[0].a"),
			want:    "test.yaml:2:5: $.l[0].a: bad",
			removal: `$.l[0].a~ "cafe.jacobcolvin.com/v1" "" key="a" item=false 1:4-1:5`,
		},
		"mapping item of a flow sequence without braces moved to a new key": {
			before:  stringtest.LinesLF("f: [0, a: 1]"),
			applies: [][]step{{{from: f1, to: q}}},
			at:      q,
			err:     "test.yaml:1:8: $.f[1].a: cafe.jacobcolvin.com/v1 moves $.f[1] to $.q, which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"mapping item of a flow sequence without braces moved into a flow sequence": {
			before:  stringtest.LinesLF("f: [a: 1]", "g: [0]"),
			applies: [][]step{{{from: paths.MustParse("$.f[0]"), to: paths.MustParse("$.g[1]")}}},
			at:      paths.MustParse("$.g[1]"),
			err:     "test.yaml:1:5: $.f[0].a: cafe.jacobcolvin.com/v1 moves $.f[0] to $.g[1], which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"mapping item of a flow sequence without braces moved into a flow sequence, then to a key": {
			before: stringtest.LinesLF("f: [a: 1]", "g: [0]"),
			applies: [][]step{
				{{from: paths.MustParse("$.f[0]"), to: paths.MustParse("$.g[1]")}},
				{{from: paths.MustParse("$.g[1]"), to: q}},
			},
			at:  q,
			err: "test.yaml:1:5: $.f[0].a: cafe.jacobcolvin.com/v1 moves $.f[0] to $.g[1], which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"mapping item of a flow sequence without braces moved into a flow sequence and set there": {
			before: stringtest.LinesLF("f: [a: 1]", "g: [0]"),
			applies: [][]step{
				{{from: paths.MustParse("$.f[0]"), to: paths.MustParse("$.g[1]")}},
				{{from: paths.MustParse("$.g[1]"), value: 3}},
			},
			at:  paths.MustParse("$.g[1]"),
			err: "test.yaml:1:5: $.f[0].a: cafe.jacobcolvin.com/v1 moves $.f[0] to $.g[1], which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"mapping item of a flow sequence without braces moved into a flow sequence, then given an entry that it loses": {
			before: stringtest.LinesLF("f: [a: 1]", "g: [5]"),
			applies: [][]step{
				{{from: paths.MustParse("$.f[0]"), to: paths.MustParse("$.g[1]")}},
				{{from: paths.MustParse("$.g[1].c"), value: 1}},
				{{from: paths.MustParse("$.g[1].c"), remove: true}},
			},
			at:  paths.MustParse("$.g[1]"),
			err: "test.yaml:1:5: $.f[0].a: cafe.jacobcolvin.com/v1 moves $.f[0] to $.g[1], which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"mapping item of a flow sequence moved to a new key of a block mapping": {
			before:  stringtest.LinesLF("f: [0, {a: 1}]", "m:", "  k: 0"),
			applies: [][]step{{{from: f1, to: paths.MustParse("$.m.q")}}},
			at:      paths.MustParse("$.m.q"),
			want:    "test.yaml:1:8: $.m.q: bad",
			wantKey: "test.yaml:1:8: $.m.q~: bad",
			removal: `$.f[1] "cafe.jacobcolvin.com/v1" "" key="" item=true 0:7-0:13`,
		},
		"mapping item of a flow sequence moved to a new key of a flow mapping": {
			before:  stringtest.LinesLF("f: [0, {a: 1}]", "g: {k: 0}"),
			applies: [][]step{{{from: f1, to: gq}}},
			at:      gq,
			want:    "test.yaml:1:8: $.g.q: bad",
			wantKey: "test.yaml:1:8: $.g.q~: bad",
			removal: `$.f[1] "cafe.jacobcolvin.com/v1" "" key="" item=true 0:7-0:13`,
		},
		"sequence item of a flow sequence moved to a new key at the root": {
			before:  stringtest.LinesLF("f: [0, [1, 2]]", "g: {k: 0}"),
			applies: [][]step{{{from: f1, to: q}}},
			at:      q,
			want:    "test.yaml:1:8: $.q: bad",
			wantKey: "test.yaml:1:8: $.q~: bad",
			removal: `$.f[1] "cafe.jacobcolvin.com/v1" "" key="" item=true 0:7-0:13`,
		},
		"missing entry of a mapping item of a flow sequence moved to a new key": {
			before:  stringtest.LinesLF("f: [0, {a: 1}]", "g: {k: 0}"),
			applies: [][]step{{{from: f1, to: gq}}},
			at:      gq.Child("missing"),
			want:    "test.yaml:1:8: $.g.q.missing: bad",
		},
		"alias item of a flow sequence moved to a new key": {
			before:  stringtest.LinesLF("a: &e 5", "f: [0, *e]"),
			applies: [][]step{{{from: f1, to: q}}},
			at:      q,
			want:    "test.yaml:2:8: $.q: bad",
			wantKey: "test.yaml:2:8: $.q~: bad",
			removal: `$.f[1] "cafe.jacobcolvin.com/v1" "" key="" item=true 1:7-1:9`,
		},
		"scalar moved into a flow sequence, then to a key": {
			before:  flow,
			applies: [][]step{{{from: spec.Child("closes"), to: f1}}, {{from: f1, to: q}}},
			at:      q,
			want:    "test.yaml:2:11: $.q: bad",
			wantKey: "test.yaml:2:3: $.q~: bad",
			removal: `$.spec.closes~ "cafe.jacobcolvin.com/v1" "" key="closes" item=false 1:2-1:8`,
		},
		"scalar moved into a flow sequence and to a key in one Apply": {
			before:  flow,
			applies: [][]step{{{from: spec.Child("closes"), to: f1}, {from: f1, to: q}}},
			at:      q,
			want:    "test.yaml:2:11: $.q: bad",
			wantKey: "test.yaml:2:3: $.q~: bad",
			removal: `$.spec.closes~ "cafe.jacobcolvin.com/v1" "" key="closes" item=false 1:2-1:8`,
		},
		"scalar item moved into a flow sequence, then to a key": {
			before:  stringtest.LinesLF("l:", "  - 0", "  - 5", "f: [0]"),
			applies: [][]step{{{from: l1, to: f1}}, {{from: f1, to: q}}},
			at:      q,
			want:    "test.yaml:3:5: $.q: bad",
			wantKey: "test.yaml:3:3: $.q~: bad",
			removal: `$.l[1] "cafe.jacobcolvin.com/v1" "" key="" item=true 2:2-2:5`,
		},
		"anchored mapping moved into a flow sequence, then to a key": {
			before:  stringtest.LinesLF("d: &d", "  port: 80", "f: [1]"),
			applies: [][]step{{{from: paths.Doc().Child("d"), to: f1}}, {{from: f1, to: q}}},
			at:      q,
			err:     "test.yaml:1:4: $.d: cafe.jacobcolvin.com/v1 moves $.d to $.f[1], which puts a block value inside a flow collection, and cafe does not move a block value into a flow collection",
		},
		"set scalar moved into a flow sequence, then to a key": {
			before: flow,
			applies: [][]step{
				{{from: spec.Child("closes"), value: 20}},
				{{from: spec.Child("closes"), to: f1}},
				{{from: f1, to: q}},
			},
			at:      q,
			want:    "test.yaml:2:11: $.q: bad",
			detail:  "cafe.jacobcolvin.com/v1 sets $.spec.closes from this value",
			wantKey: "test.yaml:2:3: $.q~: bad",
			removal: `$.spec.closes~ "cafe.jacobcolvin.com/v1" "" key="closes" item=false 1:2-1:8`,
		},
		"scalar moved into a flow sequence and set there, then moved to a key": {
			before:  flow,
			applies: [][]step{{{from: spec.Child("closes"), to: f1}}, {{from: f1, value: 21}}, {{from: f1, to: q}}},
			at:      q,
			want:    "test.yaml:2:11: $.q: bad",
			detail:  "cafe.jacobcolvin.com/v1 sets $.f[1] from this value",
			wantKey: "test.yaml:2:3: $.q~: bad",
			removal: `$.spec.closes~ "cafe.jacobcolvin.com/v1" "" key="closes" item=false 1:2-1:8`,
		},
		"set scalar item moved into a flow sequence, then to a key": {
			before:  stringtest.LinesLF("l:", "  - 0", "  - 5", "f: [0]"),
			applies: [][]step{{{from: l1, value: 6}}, {{from: l1, to: f1}}, {{from: f1, to: q}}},
			at:      q,
			want:    "test.yaml:3:5: $.q: bad",
			detail:  "cafe.jacobcolvin.com/v1 sets $.l[1] from this value",
			wantKey: "test.yaml:3:3: $.q~: bad",
			removal: `$.l[1] "cafe.jacobcolvin.com/v1" "" key="" item=true 2:2-2:5`,
		},
		"set scalar item": {
			before:  stringtest.LinesLF("l:", "  - 0", "  - 5"),
			applies: [][]step{{{from: l1, value: 6}}},
			at:      l1,
			want:    "test.yaml:3:5: $.l[1]: bad",
			detail:  "cafe.jacobcolvin.com/v1 sets $.l[1] from this value",
			removal: `$.l[1] "cafe.jacobcolvin.com/v1" "" key="" item=true 2:2-2:5`,
		},
		"set empty item": {
			before:  stringtest.LinesLF("l:", "  -", "  - 5"),
			applies: [][]step{{{from: paths.MustParse("$.l[0]"), value: 6}}},
			at:      paths.MustParse("$.l[0]"),
			want:    "test.yaml:2:4: $.l[0]: bad",
			detail:  "cafe.jacobcolvin.com/v1 sets $.l[0] from this value",
			removal: `$.l[0] "cafe.jacobcolvin.com/v1" "" key="" item=true 1:2-1:3`,
		},
		"set mapping item of a flow sequence": {
			before:  stringtest.LinesLF("f: [0, {a: 1}]"),
			applies: [][]step{{{from: f1, value: map[string]any{"b": 2}}}},
			at:      f1,
			want:    "test.yaml:1:8: $.f[1]: bad",
			detail:  "cafe.jacobcolvin.com/v1 sets $.f[1] from this value",
			removal: `$.f[1] "cafe.jacobcolvin.com/v1" "" key="" item=true 0:7-0:13`,
		},
		"set item of a flow sequence moved to a new key": {
			before:  stringtest.LinesLF("f: [0, 5]", "g: {k: 0}"),
			applies: [][]step{{{from: f1, value: 6}}, {{from: f1, to: gq}}},
			at:      gq,
			want:    "test.yaml:1:8: $.g.q: bad",
			detail:  "cafe.jacobcolvin.com/v1 sets $.f[1] from this value",
			wantKey: "test.yaml:1:8: $.g.q~: bad",
			removal: `$.f[1] "cafe.jacobcolvin.com/v1" "" key="" item=true 0:7-0:8`,
		},
		"set mapping item of a flow sequence moved to a new key": {
			before:  stringtest.LinesLF("f: [0, {a: 1}]", "g: {k: 0}"),
			applies: [][]step{{{from: f1, value: map[string]any{"b": 2}}}, {{from: f1, to: gq}}},
			at:      gq,
			want:    "test.yaml:1:8: $.g.q: bad",
			detail:  "cafe.jacobcolvin.com/v1 sets $.f[1] from this value",
			wantKey: "test.yaml:1:8: $.g.q~: bad",
			removal: `$.f[1] "cafe.jacobcolvin.com/v1" "" key="" item=true 0:7-0:13`,
		},
		"set tagged mapping item of a flow sequence moved to a new key": {
			before:  stringtest.LinesLF("f: [0, !!map {a: 1}]"),
			applies: [][]step{{{from: f1, value: map[string]any{"b": 2}}}, {{from: f1, to: q}}},
			at:      q,
			want:    "test.yaml:1:8: $.q: bad",
			detail:  "cafe.jacobcolvin.com/v1 sets $.f[1] from this value",
			wantKey: "test.yaml:1:8: $.q~: bad",
			removal: `$.f[1] "cafe.jacobcolvin.com/v1" "" key="" item=true 0:7-0:19`,
		},
		"set mapping item of a flow sequence that loses an entry": {
			before: stringtest.LinesLF("f: [0, {a: 1}]"),
			applies: [][]step{
				{{from: f1, value: map[string]any{"y": 2, "z": 1}}},
				{{from: f1.Child("z"), remove: true}},
				{{from: f1.Child("y"), key: "w"}},
				{{from: f1.Child("w"), remove: true}},
			},
			at:      f1,
			want:    "test.yaml:1:8: $.f[1]: bad",
			detail:  "cafe.jacobcolvin.com/v1 sets $.f[1] from this value",
			removal: `$.f[1] "cafe.jacobcolvin.com/v1" "" key="" item=true 0:7-0:13`,
		},
		"set sequence item of a flow sequence that loses its first and last items": {
			before: stringtest.LinesLF("f: [0, 9]"),
			applies: [][]step{
				{{from: f1, value: []int{1, 2, 3}}},
				{{from: f1.Index(2), remove: true}, {from: f1.Index(0), remove: true}},
			},
			at:      f1,
			want:    "test.yaml:1:8: $.f[1]: bad",
			detail:  "cafe.jacobcolvin.com/v1 sets $.f[1] from this value",
			removal: `$.f[1] "cafe.jacobcolvin.com/v1" "" key="" item=true 0:7-0:8`,
		},
		"item of a flow sequence moved to a new key, then set to a mapping that loses an entry": {
			before: stringtest.LinesLF("f: [0, 5]"),
			applies: [][]step{
				{{from: f1, to: q}},
				{{from: q, value: map[string]any{"c": 3, "d": 4}}},
				{{from: q.Child("d"), remove: true}},
			},
			at:      q,
			want:    "test.yaml:1:8: $.q: bad",
			detail:  "cafe.jacobcolvin.com/v1 sets $.q from this value",
			removal: `$.f[1] "cafe.jacobcolvin.com/v1" "" key="" item=true 0:7-0:8`,
		},
		"scalar moved into a flow sequence and set to a mapping that loses an entry": {
			before: stringtest.LinesLF("a: abc", "f: [0]"),
			applies: [][]step{
				{{from: paths.Doc().Child("a"), to: f1}},
				{{from: f1, value: map[string]any{"c": 3, "d": 4}}},
				{{from: f1.Child("d"), remove: true}},
			},
			at:      f1,
			want:    "test.yaml:1:4: $.f[1]: bad",
			detail:  "cafe.jacobcolvin.com/v1 sets $.f[1] from this value",
			removal: `$.a~ "cafe.jacobcolvin.com/v1" "" key="a" item=false 0:0-0:1`,
		},
		"set scalar moved into a flow sequence and to a key, then set to a mapping that loses an entry": {
			before: flow,
			applies: [][]step{
				{{from: spec.Child("closes"), value: 20}},
				{{from: spec.Child("closes"), to: f1}},
				{{from: f1, to: q}},
				{{from: q, value: map[string]any{"c": 1, "d": 2}}},
				{{from: q.Child("d"), remove: true}},
			},
			at:      q,
			want:    "test.yaml:2:3: $.q: bad",
			removal: `$.spec.closes~ "cafe.jacobcolvin.com/v1" "" key="closes" item=false 1:2-1:8`,
		},
		"mapping moved into a flow sequence and on to another, then given an entry that it loses": {
			before: stringtest.LinesLF("a:", "  x: 1", "f: [0]", "g: [5]"),
			applies: [][]step{
				{{from: paths.Doc().Child("a"), to: f1}},
				{{from: f1, to: g1}},
				{{from: g1.Child("c"), value: 1}},
				{{from: g1.Child("c"), remove: true}},
			},
			at:  g1,
			err: "test.yaml:2:3: $.a: cafe.jacobcolvin.com/v1 moves $.a to $.f[1], which puts a block value inside a flow collection, and cafe does not move a block value into a flow collection",
		},
		"mapping moved into a flow sequence and on to another in one Apply, then given a mapping that loses an entry": {
			before: stringtest.LinesLF("a:", "  x: 1", "f: [0]", "g: [5]"),
			applies: [][]step{
				{{from: paths.Doc().Child("a"), to: f1}, {from: f1, to: g1}},
				{{from: g1.Child("c"), value: map[string]any{"d": 1}}},
				{{from: g1.Child("c", "d"), remove: true}},
			},
			at:  g1,
			err: "test.yaml:2:3: $.a: cafe.jacobcolvin.com/v1 moves $.a to $.f[1], which puts a block value inside a flow collection, and cafe does not move a block value into a flow collection",
		},
		"sequence moved into a flow sequence and on to another, then given an item that it loses": {
			before: stringtest.LinesLF("a:", "  - 1", "f: [0]", "g: [5]"),
			applies: [][]step{
				{{from: paths.Doc().Child("a"), to: f1}},
				{{from: f1, to: g1}},
				{{from: g1.Index(1), value: 2}},
				{{from: g1.Index(1), remove: true}},
			},
			at:  g1,
			err: "test.yaml:2:3: $.a: cafe.jacobcolvin.com/v1 moves $.a to $.f[1], which puts a block value inside a flow collection, and cafe does not move a block value into a flow collection",
		},
		"mapping item moved into a flow sequence and on to another, then given an entry that it loses": {
			before: stringtest.LinesLF("l:", "  - x: 1", "f: [0]", "g: [5]"),
			applies: [][]step{
				{{from: paths.MustParse("$.l[0]"), to: f1}},
				{{from: f1, to: g1}},
				{{from: g1.Child("c"), value: 1}},
				{{from: g1.Child("c"), remove: true}},
			},
			at:  g1,
			err: "test.yaml:2:5: $.l[0]: cafe.jacobcolvin.com/v1 moves $.l[0] to $.f[1], which puts a block value inside a flow collection, and cafe does not move a block value into a flow collection",
		},
		"emptied mapping moved into a flow sequence and on to another, then given an entry that it loses": {
			before: stringtest.LinesLF("spec:", "  b: 1", "f: [0]", "g: [5]"),
			applies: [][]step{
				{{from: spec.Child("b"), to: paths.Doc().Child("z")}},
				{{from: spec, to: f1}},
				{{from: f1, to: g1}},
				{{from: g1.Child("c"), value: 1}},
				{{from: g1.Child("c"), remove: true}},
			},
			at:      g1,
			want:    "test.yaml:1:1: $.g[1]: bad",
			removal: `$.spec~ "cafe.jacobcolvin.com/v1" "" key="spec" item=false 0:0-0:4`,
		},
		"item of a flow sequence moved to a new key, then set to a mapping": {
			before:  stringtest.LinesLF("f: [0, 5]"),
			applies: [][]step{{{from: f1, to: q}}, {{from: q, value: map[string]any{"c": 3}}}},
			at:      q,
			want:    "test.yaml:1:8: $.q: bad",
			detail:  "cafe.jacobcolvin.com/v1 sets $.q from this value",
			wantKey: "test.yaml:1:8: $.q~: bad",
			removal: `$.f[1] "cafe.jacobcolvin.com/v1" "" key="" item=true 0:7-0:8`,
		},
		"item of a flow sequence moved past the last item of a block sequence and set to a mapping in one Apply": {
			before:  stringtest.LinesLF("f: [0, 5]", "l:", "  - 0"),
			applies: [][]step{{{from: f1, to: l1}, {from: l1, value: map[string]any{"c": 3}}}},
			at:      l1,
			want:    "test.yaml:1:8: $.l[1]: bad",
			detail:  "cafe.jacobcolvin.com/v1 sets $.l[1] from this value",
			removal: `$.f[1] "cafe.jacobcolvin.com/v1" "" key="" item=true 0:7-0:8`,
		},
		"set mapping item of a flow sequence moved to a new key of a block mapping, then renamed": {
			before: stringtest.LinesLF("f: [0, {a: 1}]", "m:", "  k: 0"),
			applies: [][]step{
				{{from: f1, value: map[string]any{"b": 2}}},
				{{from: f1, to: paths.MustParse("$.m.q")}},
				{{from: paths.MustParse("$.m.q"), key: "r"}},
			},
			at:      paths.MustParse("$.m.r"),
			want:    "test.yaml:1:8: $.m.r: bad",
			detail:  "cafe.jacobcolvin.com/v1 sets $.f[1] from this value",
			wantKey: "test.yaml:1:8: $.m.r~: bad",
		},
		"set mapping item of a flow sequence moved past the last item of a block sequence": {
			before:  stringtest.LinesLF("f: [0, {a: 1}]", "l:", "  - 0"),
			applies: [][]step{{{from: f1, value: map[string]any{"b": 2}}}, {{from: f1, to: l1}}},
			at:      l1,
			want:    "test.yaml:1:8: $.l[1]: bad",
			detail:  "cafe.jacobcolvin.com/v1 sets $.f[1] from this value",
		},
		"scalar with a flow indicator moved into a flow sequence, then to a key": {
			before:  stringtest.LinesLF("spec:", "  hours: 8,17", "f: [1]"),
			applies: [][]step{{{from: spec.Child("hours"), to: f1}}, {{from: f1, to: q}}},
			at:      q,
			want:    "test.yaml:2:10: $.q: bad",
			wantKey: "test.yaml:2:3: $.q~: bad",
			removal: `$.spec.hours~ "cafe.jacobcolvin.com/v1" "" key="hours" item=false 1:2-1:7`,
		},
		"item that a set gave a new entry": {
			before:  stringtest.LinesLF("l:", "  - closes: 19", "  - 5"),
			applies: [][]step{{{from: paths.MustParse("$.l[0].new"), value: map[string]any{"z": 1}}}},
			at:      paths.MustParse("$.l[0]"),
			want:    "test.yaml:2:3: $.l[0]: bad",
			removal: `$.l[0] "cafe.jacobcolvin.com/v1" "" key="" item=true 1:2-1:14`,
		},
		"scalar moved into a flow sequence": {
			before:  flow,
			applies: [][]step{{{from: spec.Child("closes"), to: f1}}},
			at:      f1,
			want:    "test.yaml:2:11: $.f[1]: bad",
			removal: `$.spec.closes~ "cafe.jacobcolvin.com/v1" "" key="closes" item=false 1:2-1:8`,
		},
		"empty value of a block mapping moved to a new key": {
			before:  stringtest.LinesLF("m:", "  a: 1", "  b:"),
			applies: [][]step{{{from: paths.MustParse("$.m.b"), to: q}}},
			at:      q,
			want:    "test.yaml:3:3: $.q: bad",
			wantKey: "test.yaml:3:3: $.q~: bad",
		},
		"empty value of a block mapping moved to a new key of a text without a final newline": {
			before:  "m:\n  a: 1\n  b:",
			applies: [][]step{{{from: paths.MustParse("$.m.b"), to: q}}},
			at:      q,
			want:    "test.yaml:3:3: $.q: bad",
			wantKey: "test.yaml:3:3: $.q~: bad",
		},
		"empty value of a block mapping moved past the last item of a block sequence": {
			before:  stringtest.LinesLF("m:", "  a: 1", "  b:", "l:", "  - 0"),
			applies: [][]step{{{from: paths.MustParse("$.m.b"), to: l1}}},
			at:      l1,
			want:    "test.yaml:3:3: $.l[1]: bad",
		},
		"empty value of a block mapping moved to a new key of a mapping that a removal emptied": {
			before: stringtest.LinesLF("m:", "  a: 1", "  b:", "g:", "  x: 1"),
			applies: [][]step{
				{{from: paths.MustParse("$.g.x"), remove: true}},
				{{from: paths.MustParse("$.m.b"), to: gq}},
			},
			at:      gq,
			want:    "test.yaml:3:3: $.g.q: bad",
			wantKey: "test.yaml:3:3: $.g.q~: bad",
		},
		"empty value of a block mapping moved twice": {
			before:  stringtest.LinesLF("m:", "  a: 1", "  b:"),
			applies: [][]step{{{from: paths.MustParse("$.m.b"), to: aq}}, {{from: aq, to: q}}},
			at:      q,
			want:    "test.yaml:3:3: $.q: bad",
			wantKey: "test.yaml:3:3: $.q~: bad",
		},
		"empty value of a flow mapping moved to a new key": {
			before:  stringtest.LinesLF("f: {a: 1, b:}"),
			applies: [][]step{{{from: paths.MustParse("$.f.b"), to: q}}},
			at:      q,
			want:    "test.yaml:1:11: $.q: bad",
			wantKey: "test.yaml:1:11: $.q~: bad",
		},
		"empty value of a flow mapping moved past the last item of a block sequence": {
			before:  stringtest.LinesLF("f: {a: 1, b:}", "l:", "  - 0"),
			applies: [][]step{{{from: paths.MustParse("$.f.b"), to: l1}}},
			at:      l1,
			want:    "test.yaml:1:11: $.l[1]: bad",
		},
		"empty item of a block sequence moved to a new key": {
			before:  stringtest.LinesLF("l:", "  - 0", "  -"),
			applies: [][]step{{{from: l1, to: q}}},
			at:      q,
			want:    "test.yaml:3:3: $.q: bad",
		},
		"empty value of a block mapping of CRLF text moved to a new key": {
			before:  stringtest.LinesCRLF("m:", "  a: 1", "  b:"),
			applies: [][]step{{{from: paths.MustParse("$.m.b"), to: q}}},
			at:      q,
			want:    "test.yaml:3:3: $.q: bad",
			wantKey: "test.yaml:3:3: $.q~: bad",
		},
		"empty value of a block mapping of CRLF text moved into another mapping": {
			before:  stringtest.LinesCRLF("m:", "  a: 1", "  b:", "x:", "  y: 1"),
			applies: [][]step{{{from: paths.MustParse("$.m.b"), to: paths.MustParse("$.x.n")}}},
			at:      paths.MustParse("$.x.n"),
			want:    "test.yaml:3:3: $.x.n: bad",
		},
		"empty value of a block mapping of CRLF text moved past the last item of a block sequence": {
			before:  stringtest.LinesCRLF("m:", "  a: 1", "  b:", "l:", "  - 0"),
			applies: [][]step{{{from: paths.MustParse("$.m.b"), to: l1}}},
			at:      l1,
			want:    "test.yaml:3:3: $.l[1]: bad",
		},
		"empty value of a flow mapping of CRLF text moved to a new key": {
			before:  stringtest.LinesCRLF("f: {a: 1, b:}"),
			applies: [][]step{{{from: paths.MustParse("$.f.b"), to: q}}},
			at:      q,
			want:    "test.yaml:1:11: $.q: bad",
		},
		"empty item of a block sequence of CRLF text moved to a new key": {
			before:  stringtest.LinesCRLF("l:", "  - 0", "  -"),
			applies: [][]step{{{from: l1, to: q}}},
			at:      q,
			want:    "test.yaml:3:3: $.q: bad",
		},
		"empty value with a comment moved to a new key": {
			before:  stringtest.LinesLF("m:", "  a: 1", "  b: # bee"),
			applies: [][]step{{{from: paths.MustParse("$.m.b"), to: q}}},
			at:      q,
			want:    "test.yaml:3:3: $.q: bad",
			wantKey: "test.yaml:3:3: $.q~: bad",
		},
		"empty value moved into a mapping that the move creates": {
			before:  stringtest.LinesLF("m:", "  a: 1", "  b:"),
			applies: [][]step{{{from: paths.MustParse("$.m.b"), to: paths.MustParse("$.k.q")}}},
			at:      paths.MustParse("$.k.q"),
			want:    "test.yaml:3:3: $.k.q: bad",
			wantKey: "test.yaml:3:3: $.k.q~: bad",
		},
		"empty item moved into a mapping that the move creates": {
			before:  stringtest.LinesLF("l:", "  - 0", "  -"),
			applies: [][]step{{{from: l1, to: paths.MustParse("$.k.q")}}},
			at:      paths.MustParse("$.k.q"),
			want:    "test.yaml:3:3: $.k.q: bad",
		},
		"empty value under a renamed key": {
			before:  stringtest.LinesLF("m:", "  a: 1", "  b:"),
			applies: [][]step{{{from: paths.MustParse("$.m.b"), key: "c"}}},
			at:      paths.MustParse("$.m.c"),
			want:    "test.yaml:3:5: $.m.c: bad",
			wantKey: "test.yaml:3:3: $.m.c~: bad",
		},
		"empty item of a block sequence moved into a flow mapping": {
			before:  stringtest.LinesLF("n:", "  - 5", "  -", "  - 6", "k: {a: 1}"),
			applies: [][]step{{{from: paths.MustParse("$.n"), to: paths.MustParse("$.k.q")}}},
			at:      paths.MustParse("$.k.q[1]"),
			err:     "test.yaml:2:3: $.n: cafe.jacobcolvin.com/v1 moves $.n to $.k.q, which puts a block value inside a flow collection, and cafe does not move a block value into a flow collection",
		},
		"empty item of a block sequence moved into a flow sequence": {
			before:  stringtest.LinesLF("n:", "  - 5", "  -", "k: [0]"),
			applies: [][]step{{{from: paths.MustParse("$.n"), to: paths.MustParse("$.k[1]")}}},
			at:      paths.MustParse("$.k[1][1]"),
			err:     "test.yaml:2:3: $.n: cafe.jacobcolvin.com/v1 moves $.n to $.k[1], which puts a block value inside a flow collection, and cafe does not move a block value into a flow collection",
		},
		"sequence item of a block sequence moved into a flow mapping": {
			before:  stringtest.LinesLF("n:", "  - 5", "  - - 1", "    - 2", "k: {a: 1}"),
			applies: [][]step{{{from: paths.MustParse("$.n"), to: paths.MustParse("$.k.q")}}},
			at:      paths.MustParse("$.k.q[1]"),
			err:     "test.yaml:2:3: $.n: cafe.jacobcolvin.com/v1 moves $.n to $.k.q, which puts a block value inside a flow collection, and cafe does not move a block value into a flow collection",
		},
		"flow sequence item of a block sequence moved into a flow mapping": {
			before:  stringtest.LinesLF("n:", "  - 5", "  - [3]", "k: {a: 1}"),
			applies: [][]step{{{from: paths.MustParse("$.n"), to: paths.MustParse("$.k.q")}}},
			at:      paths.MustParse("$.k.q[1]"),
			err:     "test.yaml:2:3: $.n: cafe.jacobcolvin.com/v1 moves $.n to $.k.q, which puts a block value inside a flow collection, and cafe does not move a block value into a flow collection",
		},
		"mapping item of a block sequence moved into a flow mapping": {
			before:  stringtest.LinesLF("n:", "  - 5", "  - p: 1", "k: {a: 1}"),
			applies: [][]step{{{from: paths.MustParse("$.n"), to: paths.MustParse("$.k.q")}}},
			at:      paths.MustParse("$.k.q[1]"),
			err:     "test.yaml:2:3: $.n: cafe.jacobcolvin.com/v1 moves $.n to $.k.q, which puts a block value inside a flow collection, and cafe does not move a block value into a flow collection",
		},
		"empty item of the copy that a removal writes at an alias in a flow sequence": {
			before:  stringtest.LinesLF("x: &x", "  - 0", "  -", "y: [*x]"),
			applies: [][]step{{{from: paths.MustParse("$.x"), remove: true}}},
			at:      paths.MustParse("$.y[0][1]"),
			err:     "test.yaml:4:5: $.y[0]: cafe.jacobcolvin.com/v1 removes $.x, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"sequence item of a flow sequence moved into a flow mapping": {
			before:  stringtest.LinesLF("f: [0, [1, 2]]", "k: {z: 0}"),
			applies: [][]step{{{from: paths.MustParse("$.f"), to: paths.MustParse("$.k.np")}}},
			at:      paths.MustParse("$.k.np[1]"),
			want:    "test.yaml:1:8: $.k.np[1]: bad",
			removal: `$.f[1] "cafe.jacobcolvin.com/v1" "" key="" item=true 0:7-0:13`,
		},
		"mapping item of a flow sequence moved into a flow mapping": {
			before:  stringtest.LinesLF("f: [0, 1, {a: 1}]", "k: {z: 0}"),
			applies: [][]step{{{from: paths.MustParse("$.f"), to: paths.MustParse("$.k.np")}}},
			at:      paths.MustParse("$.k.np[2]"),
			want:    "test.yaml:1:11: $.k.np[2]: bad",
			removal: `$.f[2] "cafe.jacobcolvin.com/v1" "" key="" item=true 0:10-0:16`,
		},
		"sequence item of a flow sequence of several lines moved into a flow mapping": {
			before:  stringtest.LinesLF("f: [", "  0,", "  [1]", "]", "k: {z: 0}"),
			applies: [][]step{{{from: paths.MustParse("$.f"), to: paths.MustParse("$.k.np")}}},
			at:      paths.MustParse("$.k.np[1]"),
			want:    "test.yaml:3:3: $.k.np[1]: bad",
			removal: `$.f[1] "cafe.jacobcolvin.com/v1" "" key="" item=true 2:2-2:5`,
		},
		"sequence item of a flow sequence moved into a flow sequence": {
			before:  stringtest.LinesLF("f: [0, [1]]", "g: [5]"),
			applies: [][]step{{{from: paths.MustParse("$.f"), to: g1}}},
			at:      paths.MustParse("$.g[1][1]"),
			want:    "test.yaml:1:8: $.g[1][1]: bad",
		},
		"sequence item of a flow sequence in a block item moved into a flow mapping": {
			before:  stringtest.LinesLF("f:", "  - [0, [1]]", "k: {z: 0}"),
			applies: [][]step{{{from: paths.MustParse("$.f"), to: paths.MustParse("$.k.np")}}},
			at:      paths.MustParse("$.k.np[0][1]"),
			err:     "test.yaml:2:3: $.f: cafe.jacobcolvin.com/v1 moves $.f to $.k.np, which puts a block value inside a flow collection, and cafe does not move a block value into a flow collection",
		},
		"mapping item without braces of a flow sequence that a move puts in a flow mapping": {
			before:  stringtest.LinesLF("f: [0, a: 1]", "k: {z: 0}"),
			applies: [][]step{{{from: paths.MustParse("$.f"), to: paths.MustParse("$.k.np")}}},
			at:      paths.MustParse("$.k.np[1]"),
			err:     "test.yaml:1:8: $.f[1].a: cafe.jacobcolvin.com/v1 moves $.f to $.k.np, which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"mapping item without braces of a flow sequence that a move puts in a flow sequence": {
			before:  stringtest.LinesLF("f: [0, a: 1]", "g: [5]"),
			applies: [][]step{{{from: paths.MustParse("$.f"), to: g1}}},
			at:      paths.MustParse("$.g[1][1]"),
			err:     "test.yaml:1:8: $.f[1].a: cafe.jacobcolvin.com/v1 moves $.f to $.g[1], which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"mapping item without braces under a renamed key of a flow sequence that a move puts in a flow mapping": {
			before: stringtest.LinesLF("f: [0, a: 1]", "k: {z: 0}"),
			applies: [][]step{
				{{from: paths.MustParse("$.f[1].a"), key: "b"}},
				{{from: paths.MustParse("$.f"), to: paths.MustParse("$.k.np")}},
			},
			at:  paths.MustParse("$.k.np[1]"),
			err: `test.yaml:1:8: $.f[1].a: cafe.jacobcolvin.com/v1 renames $.f[1].a to "b", which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair`,
		},
		"mapping item without braces of the copy that a removal writes at an alias in a flow mapping": {
			before:  stringtest.LinesLF("x: &x [0, a: 1]", "f: {k: *x}"),
			applies: [][]step{{{from: paths.MustParse("$.x"), remove: true}}},
			at:      paths.MustParse("$.f.k[1]"),
			err:     "test.yaml:2:8: $.f.k: cafe.jacobcolvin.com/v1 removes $.x, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"mapping item without braces of a flow sequence that a move puts in a flow mapping, then renamed": {
			before: stringtest.LinesLF("f: [0, a: 1]", "k: {z: 0}"),
			applies: [][]step{
				{{from: paths.MustParse("$.f"), to: paths.MustParse("$.k.np")}},
				{{from: paths.MustParse("$.k.np[1].a"), key: "bb"}},
			},
			at:  paths.MustParse("$.k.np[1]"),
			err: "test.yaml:1:8: $.f[1].a: cafe.jacobcolvin.com/v1 moves $.f to $.k.np, which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"mapping item without braces of a flow sequence that a move puts in a flow mapping and renamed in one apply": {
			before: stringtest.LinesLF("f: [0, a: 1]", "k: {z: 0}"),
			applies: [][]step{{
				{from: paths.MustParse("$.f"), to: paths.MustParse("$.k.np")},
				{from: paths.MustParse("$.k.np[1].a"), key: "bb"},
			}},
			at:  paths.MustParse("$.k.np[1]"),
			err: "test.yaml:1:8: $.f[1].a: cafe.jacobcolvin.com/v1 moves $.f to $.k.np, which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"mapping item without braces of a flow sequence that a move puts in a flow sequence, then renamed": {
			before: stringtest.LinesLF("f: [0, a: 1]", "g: [5]"),
			applies: [][]step{
				{{from: paths.MustParse("$.f"), to: g1}},
				{{from: paths.MustParse("$.g[1][1].a"), key: "bb"}},
			},
			at:  paths.MustParse("$.g[1][1]"),
			err: "test.yaml:1:8: $.f[1].a: cafe.jacobcolvin.com/v1 moves $.f to $.g[1], which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"mapping item without braces of a flow sequence that a move puts in a flow mapping, then emptied": {
			before: stringtest.LinesLF("f: [0, a: 1]", "k: {z: 0}"),
			applies: [][]step{
				{{from: paths.MustParse("$.f"), to: paths.MustParse("$.k.np")}},
				{{from: paths.MustParse("$.k.np[1].a"), remove: true}},
			},
			at:  paths.MustParse("$.k.np[1]"),
			err: "test.yaml:1:8: $.f[1].a: cafe.jacobcolvin.com/v1 moves $.f to $.k.np, which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"mapping item without braces of a flow sequence that a move puts in a flow mapping, then emptied by a move": {
			before: stringtest.LinesLF("f: [0, a: 1]", "k: {z: 0}"),
			applies: [][]step{
				{{from: paths.MustParse("$.f"), to: paths.MustParse("$.k.np")}},
				{{from: paths.MustParse("$.k.np[1].a"), to: q}},
			},
			at:  paths.MustParse("$.k.np[1]"),
			err: "test.yaml:1:8: $.f[1].a: cafe.jacobcolvin.com/v1 moves $.f to $.k.np, which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"mapping item without braces of a flow sequence that a move puts in a flow mapping, then left without its first entry": {
			before: stringtest.LinesLF("f: [0, a: 1, b: 2]", "k: {z: 0}"),
			applies: [][]step{
				{{from: paths.MustParse("$.f"), to: paths.MustParse("$.k.np")}},
				{{from: paths.MustParse("$.k.np[1].a"), remove: true}},
			},
			at:  paths.MustParse("$.k.np[1]"),
			err: "test.yaml:1:8: $.f[1].a: cafe.jacobcolvin.com/v1 moves $.f to $.k.np, which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"mapping item without braces of the copy that a removal writes at an alias in a flow mapping, then renamed": {
			before: stringtest.LinesLF("x: &x [0, a: 1]", "f: {k: *x}"),
			applies: [][]step{
				{{from: paths.MustParse("$.x"), remove: true}},
				{{from: paths.MustParse("$.f.k[1].a"), key: "bb"}},
			},
			at:  paths.MustParse("$.f.k[1]"),
			err: "test.yaml:2:8: $.f.k: cafe.jacobcolvin.com/v1 removes $.x, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"flow mapping at the first key of a mapping item without braces that a move puts in a flow sequence": {
			before:  stringtest.LinesLF("f: [0, a: {c: 1}]", "g: [5]"),
			applies: [][]step{{{from: paths.MustParse("$.f"), to: g1}}},
			at:      paths.MustParse("$.g[1][1].a"),
			err:     "test.yaml:1:8: $.f[1].a: cafe.jacobcolvin.com/v1 moves $.f to $.g[1], which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"value that a set wrote at the first key of a mapping item without braces of a flow sequence": {
			before:  stringtest.LinesLF("f: [0, a: 1]", "k: {z: 0}"),
			applies: [][]step{{{from: paths.MustParse("$.f[1].a"), value: map[string]int{"b": 5}}}},
			at:      paths.MustParse("$.f[1].a"),
			err:     "test.yaml:1:8: $.f[1].a: cafe.jacobcolvin.com/v1 sets $.f[1].a, which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"sequence item of the copy that a removal writes at an alias in a flow sequence": {
			before:  stringtest.LinesLF("x: &x [0, [1]]", "f: [*x]"),
			applies: [][]step{{{from: paths.MustParse("$.x"), remove: true}}},
			at:      paths.MustParse("$.f[0][1]"),
			err:     "test.yaml:2:5: $.f[0]: cafe.jacobcolvin.com/v1 removes $.x, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"block mapping that a removal copies to an alias in a flow sequence": {
			before:  stringtest.LinesLF("x: &x", "  a: 1", "f: [*x]"),
			applies: [][]step{{{from: paths.MustParse("$.x"), remove: true}}},
			at:      paths.MustParse("$.f[0]"),
			err:     "test.yaml:3:5: $.f[0]: cafe.jacobcolvin.com/v1 removes $.x, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"block sequence that a removal copies to an alias in a flow sequence": {
			before:  stringtest.LinesLF("x: &x", "  - 0", "  -", "f: [*x]"),
			applies: [][]step{{{from: paths.MustParse("$.x"), remove: true}}},
			at:      paths.MustParse("$.f[0]"),
			err:     "test.yaml:4:5: $.f[0]: cafe.jacobcolvin.com/v1 removes $.x, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"block mapping that a set copies to the second alias in a flow sequence": {
			before:  stringtest.LinesLF("x: &x", "  a: 1", "f: [0, *x]"),
			applies: [][]step{{{from: paths.MustParse("$.x"), value: 5}}},
			at:      paths.MustParse("$.f[1]"),
			want:    "test.yaml:3:8: $.f[1]: bad",
		},
		"tagged block mapping that a removal copies to an alias in a flow sequence": {
			before:  stringtest.LinesLF("x: &x !!map", "  a: 1", "f: [*x]"),
			applies: [][]step{{{from: paths.MustParse("$.x"), remove: true}}},
			at:      paths.MustParse("$.f[0]"),
			err:     "test.yaml:3:5: $.f[0]: cafe.jacobcolvin.com/v1 removes $.x, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"flow mapping that a removal copies to an alias in a flow sequence": {
			before:  stringtest.LinesLF("x: &x {a: 1}", "f: [*x]"),
			applies: [][]step{{{from: paths.MustParse("$.x"), remove: true}}},
			at:      paths.MustParse("$.f[0]"),
			err:     "test.yaml:2:5: $.f[0]: cafe.jacobcolvin.com/v1 removes $.x, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"mapping that a move put in a flow sequence moved again into a flow mapping": {
			before: stringtest.LinesLF("f:", "  - [9, 2]", "h:", "  d: {b: 5}", "k: {z: 0}"),
			applies: [][]step{
				{{from: paths.MustParse("$.h.d"), to: paths.MustParse("$.f[0][2]")}},
				{{from: paths.MustParse("$.f"), to: paths.MustParse("$.k.np")}},
			},
			at:  paths.MustParse("$.k.np[0][2]"),
			err: "test.yaml:2:3: $.f: cafe.jacobcolvin.com/v1 moves $.f to $.k.np, which puts a block value inside a flow collection, and cafe does not move a block value into a flow collection",
		},
		"mapping set over an item of a flow sequence then moved into a flow mapping": {
			before: stringtest.LinesLF("f:", "  - [9, 2]", "k: {z: 0}"),
			applies: [][]step{
				{{from: paths.MustParse("$.f[0][1]"), value: map[string]int{"b": 5}}},
				{{from: paths.MustParse("$.f"), to: paths.MustParse("$.k.np")}},
			},
			at:  paths.MustParse("$.k.np[0][1]"),
			err: "test.yaml:2:3: $.f: cafe.jacobcolvin.com/v1 moves $.f to $.k.np, which puts a block value inside a flow collection, and cafe does not move a block value into a flow collection",
		},
		"set mapping item whose last entry ends in a multi-byte character": {
			before:  stringtest.LinesLF("l:", "  - a: 1", "    b: é", "  - 2"),
			applies: [][]step{{{from: l0, value: 5}}},
			at:      l0,
			// A collection binds at its "-", so the value that replaces it
			// binds there too (fix20 G1-3).
			want:    "test.yaml:2:3: $.l[0]: bad",
			detail:  "cafe.jacobcolvin.com/v1 sets $.l[0] from this value",
			removal: `$.l[0] "cafe.jacobcolvin.com/v1" "" key="" item=true 1:2-2:8`,
		},
		"set scalar item that ends in multi-byte characters": {
			before:  stringtest.LinesLF("l:", "  - 日本", "  - 2"),
			applies: [][]step{{{from: l0, value: 5}}},
			at:      l0,
			want:    "test.yaml:2:5: $.l[0]: bad",
			detail:  "cafe.jacobcolvin.com/v1 sets $.l[0] from this value",
			removal: `$.l[0] "cafe.jacobcolvin.com/v1" "" key="" item=true 1:2-1:6`,
		},
		"mapping item whose last entry moved to a new key": {
			before:  stringtest.LinesLF("l:", "  - a: 1", "    b: 2", "  - 3"),
			applies: [][]step{{{from: paths.MustParse("$.l[0].b"), to: paths.Doc().Child("z")}}},
			at:      l0,
			want:    "test.yaml:2:3: $.l[0]: bad",
			removal: `$.l[0] "cafe.jacobcolvin.com/v1" "" key="" item=true 1:2-2:8`,
		},
		"entry moved into a mapping item that ends in multi-byte characters": {
			before:  stringtest.LinesLF("l:", "  - a: 日本", "z: 5"),
			applies: [][]step{{{from: paths.Doc().Child("z"), to: paths.MustParse("$.l[0].c")}}},
			at:      l0,
			want:    "test.yaml:2:3: $.l[0]: bad",
			removal: `$.l[0] "cafe.jacobcolvin.com/v1" "" key="" item=true 1:2-1:9`,
		},
		"scalar item moved into a flow sequence": {
			before:  stringtest.LinesLF("l:", "  - 0", "  - 5", "f: [0]"),
			applies: [][]step{{{from: l1, to: f1}}},
			at:      f1,
			want:    "test.yaml:3:5: $.f[1]: bad",
			removal: `$.l[1] "cafe.jacobcolvin.com/v1" "" key="" item=true 2:2-2:5`,
		},
		"anchored mapping moved into a flow sequence": {
			before:  stringtest.LinesLF("d: &d", "  port: 80", "f: [1]"),
			applies: [][]step{{{from: paths.Doc().Child("d"), to: f1}}},
			at:      f1,
			err:     "test.yaml:1:4: $.d: cafe.jacobcolvin.com/v1 moves $.d to $.f[1], which puts a block value inside a flow collection, and cafe does not move a block value into a flow collection",
		},
		// An item of a flow sequence has no key or "-", so a moved flow
		// collection keeps the place of the key that held it, under its
		// anchor or its tag too (fix21 B2).
		"control: flow mapping moved into a flow sequence": {
			before:  stringtest.LinesLF("o: 0", "d:", "  {k: 1}", "f: [1]"),
			applies: [][]step{{{from: paths.Doc().Child("d"), to: f1}}},
			at:      f1.Child("zz"),
			want:    "test.yaml:2:1: $.f[1].zz: bad",
		},
		"anchored flow mapping on its own line moved into a flow sequence": {
			before:  stringtest.LinesLF("o: 0", "d:", "  &x {k: 1}", "f: [1]"),
			applies: [][]step{{{from: paths.Doc().Child("d"), to: f1}}},
			at:      f1.Child("zz"),
			want:    "test.yaml:2:1: $.f[1].zz: bad",
		},
		"tagged flow mapping on the line of its key moved into a flow sequence": {
			before:  stringtest.LinesLF("o: 0", "d: !t {k: 1}", "f: [1]"),
			applies: [][]step{{{from: paths.Doc().Child("d"), to: f1}}},
			at:      f1,
			want:    "test.yaml:2:1: $.f[1]: bad",
			removal: `$.d~ "cafe.jacobcolvin.com/v1" "" key="d" item=false 1:0-1:1`,
		},
		"anchored flow sequence of a block item moved into a flow sequence": {
			before:  stringtest.LinesLF("l:", "  - 0", "  -", "    &x [1]", "f: [1]"),
			applies: [][]step{{{from: l1, to: f1}}},
			at:      f1,
			want:    "test.yaml:3:3: $.f[1]: bad",
			removal: `$.l[1] "cafe.jacobcolvin.com/v1" "" key="" item=true 2:2-3:10`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before)

			for _, steps := range tc.applies {
				err := d.Apply(t.Context(), paths.Doc(), func(ctx context.Context, root *edit.Node) error {
					for _, s := range steps {
						n := root.At(s.from)

						var err error

						switch {
						case s.value != nil:
							err = n.Set(ctx, s.value)
						case s.key != "":
							err = n.SetKey(s.key)
						case s.remove:
							err = n.Remove()
						default:
							err = n.Move(s.to)
						}

						if err != nil {
							return err
						}
					}

					return nil
				}, label)
				if failsWith(t, err, tc.err) {
					return
				}
			}

			if tc.want != "" {
				err := d.Node().Bind(niceyaml.NewError("bad", niceyaml.AtPath(tc.at)))
				assert.Equal(t, tc.want, err.Error())

				details := strings.Split(niceyaml.FormatError(err), "`-- ")[1:]
				for i, detail := range details {
					details[i], _, _ = strings.Cut(detail, "\n")
				}

				assert.Equal(t, tc.detail, strings.Join(details, "; "))
			}

			if tc.wantKey != "" {
				err := d.Node().Bind(niceyaml.NewError("bad", niceyaml.AtPath(tc.at.Key())))
				assert.Equal(t, tc.wantKey, err.Error())
			}

			if tc.removal == "" && tc.err == "" {
				return
			}

			err := applyAt(t, d, tc.at, func(_ context.Context, n *edit.Node) error {
				return n.Remove()
			})
			if failsWith(t, err, tc.err) {
				return
			}

			require.Empty(t, tc.err, "every Apply passes")

			removals := d.Removals()
			require.Len(t, removals, 1)
			assert.Equal(t, tc.removal, removalString(removals[0]))
		})
	}
}

func TestDoc_Locate_Tokenless(t *testing.T) {
	t.Parallel()

	at := func(path string, fn edit.Func) edit.Func {
		return func(ctx context.Context, n *edit.Node) error { return fn(ctx, n.At(paths.MustParse(path))) }
	}

	set := func(v any) edit.Func {
		return func(ctx context.Context, n *edit.Node) error { return n.Set(ctx, v) }
	}

	remove := func(_ context.Context, n *edit.Node) error { return n.Remove() }

	both := func(first, second edit.Func) edit.Func {
		return func(ctx context.Context, n *edit.Node) error {
			err := first(ctx, n)
			if err != nil {
				return err
			}

			return second(ctx, n)
		}
	}

	fail := func(path string) edit.Func {
		return func(context.Context, *edit.Node) error {
			return niceyaml.NewError("bad", niceyaml.AtPath(paths.MustParse(path)))
		}
	}

	unlocated := func(context.Context, *edit.Node) error { return niceyaml.NewError("bad") }

	rename := func(path, key string) edit.Func {
		return func(_ context.Context, n *edit.Node) error { return n.At(paths.MustParse(path)).SetKey(key) }
	}

	decode := func(path string) edit.Func {
		return func(ctx context.Context, n *edit.Node) error {
			_, err := n.At(paths.MustParse(path)).Decode[any](ctx)

			return err
		}
	}

	move := func(from, to string) edit.Func {
		return func(_ context.Context, n *edit.Node) error {
			return n.At(paths.MustParse(from)).Move(paths.MustParse(to))
		}
	}

	// The Func of held sets the node at path to 7 after later runs.
	held := func(path string, later edit.Func) edit.Func {
		return func(ctx context.Context, n *edit.Node) error {
			h := n.At(paths.MustParse(path))

			err := later(ctx, n)
			if err != nil {
				return err
			}

			return h.Set(ctx, 7)
		}
	}

	var (
		engine  = "go-yaml's encoder writes U+0001 as it is, and YAML does not allow that in a text"
		report  = "|-- report this to the authors of cafe"
		anchor  = "to a value whose YAML holds the anchor &q, and cafe does not write anchors or aliases"
		noAlias = "|-- return the YAML of edit_test.yamlMarshaler without anchors and aliases"
		twoKeys = `the value it writes holds two keys that a decode reads as "1"`
		dupMap  = map[any]any{1: "a", "1": "b"}
	)

	tcs := map[string]struct {
		before string
		// The text of a reference document of the Source, when set.
		ref string
		// The path of each Apply, or "" for the root.
		apply string
		// The Func of each Apply, in order. Apply i has the label vi,
		// counted from 1.
		applies []edit.Func
		// The path of an error that Doc.Node binds after the Applies, when
		// set.
		bind string
		// The error of the last Apply, as its first line and its details,
		// or "" when every Apply passes.
		err string
		// The error that binds at bind, as its first line and its details.
		bound string
		// Whether the Source takes repeated keys.
		dup bool
	}{
		// A value that a Set writes over a value of the user stands for the
		// value that it replaces, and a collection binds at its key or its
		// "-". The values that the Set creates inside the new value bind at
		// the site of the value that holds them, and a caller's error there
		// keeps its path (fix20 G1-3, B1).
		"G1-3: caller error below a scalar that a Set wrote over a mapping": {
			before:  stringtest.LinesLF("o: 0", "m:", "  s:", "    k: 1", "  t: x"),
			applies: []edit.Func{both(at("$.m.s", set(5)), fail("$.m.s.zz"))},
			err:     stringtest.JoinLF("test.yaml:3:3: $.m.s.zz: bad", "`-- v1 sets $.m.s from this value"),
		},
		"G1-3: own problem below a scalar that a Set wrote over a mapping": {
			before:  stringtest.LinesLF("o: 0", "m:", "  s:", "    k: 1", "  t: x"),
			applies: []edit.Func{both(at("$.m.s", set(5)), at("$.m.s.zz", set(1)))},
			err: stringtest.JoinLF(
				"test.yaml:3:3: $.m.s: v1 sets $.m.s.zz, but s holds a scalar",
				"|-- v1 sets $.m.s from this value",
				"`-- v1 sets $.m.s.zz",
			),
		},
		"G1-3: caller error below a scalar that a Set wrote over an item": {
			before:  stringtest.LinesLF("o: 0", "l:", "  - 0", "  - {k: 1}"),
			applies: []edit.Func{both(at("$.l[1]", set(5)), fail("$.l[1].zz"))},
			err:     stringtest.JoinLF("test.yaml:4:3: $.l[1].zz: bad", "`-- v1 sets $.l[1] from this value"),
		},
		"G1-3: caller error below a scalar that a Set wrote over a flow mapping": {
			before:  stringtest.LinesLF("o: 0", "f: {k: 1}"),
			applies: []edit.Func{both(at("$.f", set(5)), fail("$.f.zz"))},
			err:     stringtest.JoinLF("test.yaml:2:1: $.f.zz: bad", "`-- v1 sets $.f from this value"),
		},
		"G1-3 control: caller error below a mapping that a Set wrote over a mapping": {
			before:  stringtest.LinesLF("o: 0", "m:", "  s:", "    k: 1", "  t: x"),
			applies: []edit.Func{both(at("$.m.s", set(map[string]int{"j": 2})), fail("$.m.s.zz"))},
			err:     "test.yaml:3:3: $.m.s.zz: bad",
		},
		"B1: caller error at a value that a Set created inside the mapping that it wrote": {
			before:  stringtest.LinesLF("o: 0", "m:", "  a: 1"),
			applies: []edit.Func{both(at("$.m", set(map[string]int{"b": 2})), fail("$.m.b"))},
			err:     stringtest.JoinLF("test.yaml:2:1: $.m.b: bad", "`-- v1 sets $.m from this value"),
		},
		"B1: error that Doc.Node binds at an item that a Set created": {
			before:  stringtest.LinesLF("o: 0", "l:", "  - 1"),
			applies: []edit.Func{at("$.l", set([]int{5, 6}))},
			bind:    "$.l[1]",
			bound:   stringtest.JoinLF("test.yaml:2:1: $.l[1]: bad", "`-- v1 sets $.l from this value"),
		},
		"B1: engine error at a value that a Set created inside the mapping that it wrote": {
			before:  stringtest.LinesLF("o: 0", "m:", "  a: 1"),
			applies: []edit.Func{both(at("$.m", set(map[string]int{"b": 2})), at("$.m.b", set("x\x01y")))},
			err: stringtest.JoinLF(
				"test.yaml:2:1: $.m: "+engine, report, "|-- v1 sets $.m from this value", "`-- v1 sets $.m.b",
			),
		},
		"B1 control: caller error at the mapping that a Set wrote": {
			before:  stringtest.LinesLF("o: 0", "m:", "  a: 1"),
			applies: []edit.Func{both(at("$.m", set(map[string]int{"b": 2})), fail("$.m"))},
			err:     "test.yaml:2:1: $.m: bad",
		},
		// An error of the engine at a value that an earlier call created
		// names the edit that wrote the value, and then the call that failed
		// and the place that it asked for (fix22 B2, P74).
		"B2: engine error at a value that an earlier Apply created inside the mapping that it wrote": {
			before:  "b: 0\n",
			applies: []edit.Func{at("$.b", set(map[string]int{"q": 1})), at("$.b.q", set("x\x01y"))},
			err: stringtest.JoinLF(
				"test.yaml:1:1: $.b: "+engine, report, "|-- v1 sets $.b from this value", "`-- v2 sets $.b.q",
			),
		},
		"B2: engine error at an item that an earlier Apply created": {
			before:  "a: 0\n",
			applies: []edit.Func{at("$.l", set([]int{1, 2})), at("$.l[1]", set("x\x01y"))},
			err:     stringtest.JoinLF("test.yaml:1:1: $: "+engine, report, "|-- v1 sets $.l", "`-- v2 sets $.l[1]"),
		},
		"B2: engine error at a key that a rename wrote over a key that an earlier Apply created": {
			before:  "a: 0\n",
			applies: []edit.Func{at("$.m.k", set(1)), rename("$.m.k", "x\x01y")},
			err: stringtest.JoinLF(
				"test.yaml:1:1: $: "+engine, report, "|-- v1 sets $.m.k", "`-- v2 renames $.m.k to \"x\\x01y\"",
			),
		},
		// The key that a Move writes keeps the writer of the key or the "-"
		// that held the value, as the key that a rename writes keeps the
		// writer of the old key. It binds where the text that the Move
		// creates binds (fix24 G24-1).
		"G24-1: engine error at a key that a Move wrote for a key that an earlier Apply created": {
			before:  "a: 0\n",
			applies: []edit.Func{at("$.n", set(5)), move("$.n", `$.'x\u0001y'`)},
			err: stringtest.JoinLF(
				"test.yaml:1:1: $: "+engine, report, "|-- v1 sets $.n", "`-- v2 moves $.n to $.'x\\u0001y'",
			),
		},
		"G24-1: engine error at a key that a Move wrote in another mapping for a key that an earlier Apply created": {
			before:  stringtest.LinesLF("a:", "  z: 1", "b:", "  y: 1"),
			applies: []edit.Func{at("$.a.n", set(5)), move("$.a.n", `$.b.'x\u0001y'`)},
			err: stringtest.JoinLF(
				"test.yaml:3:1: $.b: "+engine, report, "|-- v1 sets $.a.n", "`-- v2 moves $.a.n to $.b.'x\\u0001y'",
			),
		},
		"G24-1: engine error at a key that a Move wrote for a key that the same call created": {
			before:  "a: 0\n",
			applies: []edit.Func{both(at("$.n", set(5)), move("$.n", `$.'x\u0001y'`))},
			err: stringtest.JoinLF(
				"test.yaml:1:1: $: "+engine, report, "|-- v1 sets $.n", "`-- v1 moves $.n to $.'x\\u0001y'",
			),
		},
		"G24-1: engine error at a key that a Move wrote for the dash of an item that an earlier Apply created": {
			before:  stringtest.LinesLF("a:", "  z: 1", "l:", "- 1"),
			applies: []edit.Func{at("$.l[1]", set(5)), move("$.l[1]", `$.a.'x\u0001y'`)},
			err: stringtest.JoinLF(
				"test.yaml:1:1: $.a: "+engine, report, "|-- v1 sets $.l[1]", "`-- v2 moves $.l[1] to $.a.'x\\u0001y'",
			),
		},
		"G24-1: engine error at a key that a Move wrote for an item of a flow sequence that an earlier Apply created": {
			before:  stringtest.LinesLF("l: [0]", "b: 1"),
			applies: []edit.Func{at("$.l[1]", set(5)), move("$.l[1]", `$.'x\u0001y'`)},
			err: stringtest.JoinLF(
				"test.yaml:1:1: $: "+engine, report, "|-- v1 sets $.l[1]", "`-- v2 moves $.l[1] to $.'x\\u0001y'",
			),
		},
		"G24-1: caller error at a mapping that an earlier Apply created and a Move took": {
			before:  "a: 0\n",
			applies: []edit.Func{at("$.n", set(map[string]int{"q": 1})), move("$.n", "$.nn"), fail("$.nn")},
			err:     stringtest.JoinLF("test.yaml:1:1: $.nn: bad", "`-- v1 sets $.n"),
		},
		"G24-1: engine error of a rename of a key that a Move wrote for a key that an earlier Apply created": {
			before:  "a: 0\n",
			applies: []edit.Func{at("$.n", set(5)), move("$.n", "$.k"), rename("$.k", "x\x01y")},
			err: stringtest.JoinLF(
				"test.yaml:1:1: $: "+engine, report, "|-- v1 sets $.n", "`-- v3 renames $.k to \"x\\x01y\"",
			),
		},
		"B2 control: engine error at a value that the same call set before": {
			before:  "a: 0\n",
			applies: []edit.Func{both(at("$.n", set(5)), at("$.n", set("x\x01y")))},
			err:     stringtest.JoinLF("test.yaml:1:1: $: "+engine, report, "`-- v1 sets $.n"),
		},
		"B2 control: engine error at a place that the call creates below a mapping that an earlier Apply wrote": {
			before:  "b: 0\n",
			applies: []edit.Func{at("$.b", set(map[string]int{"q": 1})), at("$.b.z", set("x\x01y"))},
			err:     stringtest.JoinLF("test.yaml:1:1: $.b: "+engine, report, "`-- v2 sets $.b.z"),
		},
		// A renamed key stands for the key of the user, so no call wrote its
		// token, and the detail names the rename as the call that runs
		// (fix23 G23-1).
		"G23-1: engine error at a key of the user that a rename writes": {
			before:  stringtest.LinesLF("m:", "  k: 1"),
			applies: []edit.Func{rename("$.m.k", "x\x01y")},
			err:     "test.yaml:2:3: $.m.k~: " + engine + "\n|-- report this to the authors of cafe\n`-- v1 renames $.m.k to \"x\\x01y\"",
		},
		// A Set checks its value with go-yaml's encoder before it writes.
		// Where the Set replaces a value with a token, the error binds where
		// the user error of the same Set binds, and its details name the same
		// calls: the call that wrote the value, and then the Set (fix22 GL1,
		// P74).
		"GL1: engine error at a value of the user that an earlier Apply set": {
			before:  "b: 0\n",
			applies: []edit.Func{at("$.b", set(5)), at("$.b", set("x\x01y"))},
			err: stringtest.JoinLF(
				"test.yaml:1:4: $.b: "+engine,
				report,
				"|-- v1 sets $.b from this value",
				"`-- v2 sets $.b from this value",
			),
		},
		"GL1: engine error inside a mapping that a Set writes over a value of the user that an earlier Apply set": {
			before:  "b: 0\n",
			applies: []edit.Func{at("$.b", set(5)), at("$.b", set(map[string]string{"a": "x\x01y"}))},
			err: stringtest.JoinLF(
				"test.yaml:1:4: $.b: "+engine,
				report,
				"|-- v1 sets $.b from this value",
				"`-- v2 sets $.b from this value",
			),
		},
		"GL1: float that go-yaml's encoder writes in an exponent form over a value that an earlier Apply set": {
			before:  "b: 0\n",
			applies: []edit.Func{at("$.b", set(5)), at("$.b", set(1e6))},
			err: stringtest.JoinLF(
				`test.yaml:1:4: $.b: go-yaml's encoder writes the float 1e+06 as text that go-yaml's decoder `+
					`reads as "1e+06"`,
				report,
				"|-- v1 sets $.b from this value",
				"`-- v2 sets $.b from this value",
			),
		},
		"GL1: engine error inside a mapping that a Set writes over a value of the user": {
			before:  "b: 0\n",
			applies: []edit.Func{at("$.b", set(map[string]string{"a": "x\x01y"}))},
			err:     stringtest.JoinLF("test.yaml:1:4: $.b: "+engine, report, "`-- v1 sets $.b from this value"),
		},
		"GL1: engine error inside a mapping that a Set writes over a mapping that an earlier call of its Apply set": {
			before: "b: 0\n",
			applies: []edit.Func{at("$.b", both(
				set(map[string]int{"q": 1}),
				set(map[string]string{"a": "x\x01y"}),
			))},
			err: stringtest.JoinLF("test.yaml:1:1: $.b: "+engine, report, "`-- v1 sets $.b from this value"),
		},
		"GL1: engine error at a value of the user that an earlier Apply moved": {
			before:  stringtest.LinesLF("o: 0", "a: 1"),
			applies: []edit.Func{move("$.o", "$.n.p"), at("$.n.p", set("x\x01y"))},
			err:     stringtest.JoinLF("test.yaml:1:4: $.o: "+engine, report, "`-- v2 sets $.n.p from this value"),
		},
		"GL1 control: user error of a value of the user that an earlier Apply set": {
			before:  "b: 0\n",
			applies: []edit.Func{at("$.b", set(5)), at("$.b", set(dupMap))},
			err: stringtest.JoinLF(
				"test.yaml:1:4: $.b: v2 sets $.b, but "+twoKeys,
				"|-- remove one of the two",
				"|-- v1 sets $.b from this value",
				"`-- v2 sets $.b from this value",
			),
		},
		"GL1 control: user error of a value of the user that an earlier Apply moved": {
			before:  stringtest.LinesLF("o: 0", "a: 1"),
			applies: []edit.Func{move("$.o", "$.n.p"), at("$.n.p", set(dupMap))},
			err: stringtest.JoinLF(
				"test.yaml:1:4: $.o: v2 sets $.n.p, but "+twoKeys,
				"|-- remove one of the two",
				"`-- v2 sets $.n.p from this value",
			),
		},
		"GL1 control: caller error after a Set over a value of the user that an earlier Apply set": {
			before:  "b: 0\n",
			applies: []edit.Func{at("$.b", set(5)), both(at("$.b", set(6)), fail("$.b"))},
			err:     stringtest.JoinLF("test.yaml:1:4: $.b: bad", "`-- v2 sets $.b from this value"),
		},
		// An item that holds a tag alone binds past its "-", where the user
		// error of a Set over it binds.
		"GL1: engine error at an item of the user that holds a tag alone": {
			before:  "- !!null\n",
			applies: []edit.Func{at("$[0]", set(1e6))},
			err: stringtest.JoinLF(
				`test.yaml:1:3: $[0]: go-yaml's encoder writes the float 1e+06 as text that go-yaml's decoder `+
					`reads as "1e+06"`,
				report,
				"`-- v1 sets $[0] from this value",
			),
		},
		// A mapping of the user whose first entry an earlier call created is
		// the value of the user that a later Set of the mapping replaces, so
		// the detail says "from this value" (gate23 note 3). A path through a
		// `<<` merge key names no place of the edit. The error names the Set
		// at both, as the user error of the same Set does.
		"GL1: engine error at a mapping of the user whose first entry an earlier Apply created": {
			before: stringtest.LinesLF("t:", "  b: 1", "r: 1"),
			applies: []edit.Func{
				both(at("$.t.b", remove), at("$.t.c", set(1))),
				at("$.t", set(1e6)),
			},
			err: stringtest.JoinLF(
				`test.yaml:1:1: $.t: go-yaml's encoder writes the float 1e+06 as text that go-yaml's decoder `+
					`reads as "1e+06"`,
				report,
				"`-- v2 sets $.t from this value",
			),
		},
		"GL1 control: user error of a mapping of the user whose first entry an earlier Apply created": {
			before: stringtest.LinesLF("t:", "  b: 1", "r: 1"),
			applies: []edit.Func{
				both(at("$.t.b", remove), at("$.t.c", set(1))),
				at("$.t", set(dupMap)),
			},
			err: stringtest.JoinLF(
				"test.yaml:1:1: $.t: v2 sets $.t, but "+twoKeys,
				"|-- remove one of the two",
				"`-- v2 sets $.t from this value",
			),
		},
		"GL1: engine error at a path through a merge key": {
			before:  "<<: {}\n",
			applies: []edit.Func{at("$.<<", set("x\x01y"))},
			err:     stringtest.JoinLF("test.yaml:1:1: $: "+engine, report, "`-- v1 sets $.<<"),
		},
		"GL1 control: user error at a path through a merge key": {
			before:  "<<: {}\n",
			applies: []edit.Func{at("$.<<", set(dupMap))},
			err: stringtest.JoinLF(
				"test.yaml:1:1: $: v1 sets $.<<, but "+twoKeys,
				"|-- remove one of the two",
				"`-- v1 sets $.<<",
			),
		},
		"GL1 control: user error of a Set over an item of the user that holds a tag alone": {
			before:  "- !!null\n",
			applies: []edit.Func{at("$[0]", set(dupMap))},
			err: stringtest.JoinLF(
				"test.yaml:1:3: $[0]: v1 sets $[0], but "+twoKeys,
				"|-- remove one of the two",
				"`-- v1 sets $[0] from this value",
			),
		},
		// A refusal of the parser at text that an edit created binds at the
		// site of that text under its path (fix20 G1-4).
		"G1-4: refusal of the parser at a key that a move creates at the root": {
			before:  stringtest.LinesLF("r0: 1", "r1: 2", `r2: "x"`, "r3: !!str"),
			applies: []edit.Func{move("$.r2", "$.z.y")},
			err: stringtest.JoinLF(
				"test.yaml:1:1: $: v1 moves $.r2 to $.z.y, and go-yaml's parser refuses the edited text: "+
					"unexpected scalar value",
				"|-- report this to the authors of cafe",
				"`-- v1 moves $.r2 to $.z.y",
			),
		},
		"G1-4: refusal of the parser at a key that a move creates after a removal of the first key": {
			before:  stringtest.LinesLF("r0: 1", "r1: 2", `r2: "x"`, "r3: !!str"),
			applies: []edit.Func{both(at("$.r0", remove), move("$.r2", "$.z.y"))},
			err: stringtest.JoinLF(
				"test.yaml:2:1: $: v1 moves $.r2 to $.z.y, and go-yaml's parser refuses the edited text: "+
					"unexpected scalar value",
				"|-- report this to the authors of cafe",
				"`-- v1 moves $.r2 to $.z.y",
			),
		},
		// A scalar and an alias bind at their first token under their
		// anchors and tags, as an error at the value binds, whatever binds
		// an error at a place below them (fix20 B2, G1-5).
		"B2: caller error below a tagged scalar on the line after its tag": {
			before:  stringtest.LinesLF("o: 0", "x: !!str", "  5"),
			applies: []edit.Func{fail("$.x.zz")},
			err:     "test.yaml:3:3: $.x.zz: bad",
		},
		"B2: caller error below an anchored scalar": {
			before:  stringtest.LinesLF("o: 0", "a: &q 7"),
			applies: []edit.Func{fail("$.a.x")},
			err:     "test.yaml:2:7: $.a.x: bad",
		},
		"B2 control: own problem below an anchored scalar": {
			before:  stringtest.LinesLF("o: 0", "a: &q 7"),
			applies: []edit.Func{at("$.a.x", set(1))},
			err: "test.yaml:2:7: $.a: v1 sets $.a.x, but a holds a scalar\n" +
				"`-- v1 sets $.a.x",
		},
		"B2: caller error below a tagged item": {
			before:  stringtest.LinesLF("o: 0", "l:", "  - !!int '7'"),
			applies: []edit.Func{fail("$.l[0].x")},
			err:     "test.yaml:3:11: $.l[0].x: bad",
		},
		"B2: caller error below an alias of a mapping on its own line": {
			before:  stringtest.LinesLF("base: &b", "  sub: 1", "svc:", "  *b"),
			applies: []edit.Func{fail("$.svc.zz")},
			err:     "test.yaml:4:3: $.svc.zz: bad",
		},
		"B2: engine error past the end of an alias of a sequence": {
			before:  stringtest.LinesLF("z: &an [1]", "w:", "  *an"),
			applies: []edit.Func{at("$.w[1]", set("x\x01y"))},
			err:     stringtest.JoinLF("test.yaml:3:3: $.w: "+engine, report, "`-- v1 sets $.w[1]"),
		},
		"B2: caller error below the value of an explicit key": {
			before:  stringtest.LinesLF("o: 0", "? b", ": {c: 1}"),
			applies: []edit.Func{fail("$.b.zz")},
			err:     "test.yaml:2:3: $.b.zz: bad",
		},
		"B2 control: read below an alias of a mapping on its own line": {
			before:  stringtest.LinesLF("base: &b", "  sub: 1", "svc:", "  *b"),
			applies: []edit.Func{decode("$.svc.zz")},
			err: "test.yaml:4:3: $.svc: v1 cannot decode $.svc.zz, which holds no value\n" +
				"`-- v1 reads $.svc.zz",
		},
		// With no item kept, the root binds at the first byte of its
		// content in the user's text (fix20 B4).
		"B4: caller error at the root sequence after a removal of every item": {
			before:  stringtest.LinesLF("- 1", "- 2"),
			applies: []edit.Func{both(at("$[0]", remove), both(at("$[0]", remove), fail("$[0]")))},
			err:     "test.yaml:1:1: $[0]: bad",
		},
		"B4: caller error at the root sequence after a comment line": {
			before:  stringtest.LinesLF("# c", "- 1", "- 2"),
			applies: []edit.Func{both(at("$[0]", remove), both(at("$[0]", remove), fail("$[0]")))},
			err:     "test.yaml:2:1: $[0]: bad",
		},
		// A caller's error below an item that an edit shifted keeps the
		// index that the caller gave, and binds at the item (fix21 P65).
		"B3: caller error below a pair without braces after a removal of an earlier item": {
			before:  stringtest.LinesLF("l: [x, b: 1]"),
			applies: []edit.Func{both(at("$.l[0]", remove), fail("$.l[0].zz"))},
			err:     "test.yaml:1:8: $.l[0].zz: bad",
		},
		// An error that a Func returns at a path keeps it below an entry
		// that a later entry of its key hides (fix20 B5).
		"B5: caller error at a path from a node below a hidden entry": {
			before:  stringtest.LinesLF("o: 0", "a:", "  x: {k: 1}", "a:", "  y: 2"),
			dup:     true,
			apply:   "$..x",
			applies: []edit.Func{fail("$.o")},
			err:     "test.yaml:1:4: $.o: bad",
		},
		"B5: caller error at a path from a node below a hidden entry, read from the node": {
			before:  stringtest.LinesLF("o: 0", "a:", "  x: {k: 1}", "a:", "  y: 2"),
			dup:     true,
			apply:   "$..x",
			applies: []edit.Func{fail("@.k")},
			err:     "test.yaml:3:10: $.a.x.k: bad",
		},
		"B5 control: caller error without a location at a node below a hidden entry": {
			before:  stringtest.LinesLF("o: 0", "a:", "  x: {k: 1}", "a:", "  y: 2"),
			dup:     true,
			apply:   "$..x",
			applies: []edit.Func{func(context.Context, *edit.Node) error { return niceyaml.NewError("bad") }},
			err:     "test.yaml:3:3: $.a.x: bad",
		},
		// Fix20 P61: a value that an edit creates at the root binds at the
		// first key or item of the root that the user wrote and the edits
		// kept, as a read of a missing key of the root binds.
		"engine error at a key created after a removal of the first key": {
			before:  stringtest.LinesLF("a: 1", "b: 2"),
			applies: []edit.Func{both(at("$.a", remove), at("$.z", set("x\x01y")))},
			err:     stringtest.JoinLF("test.yaml:2:1: $: "+engine, report, "`-- v1 sets $.z"),
		},
		"refusal of a value at a key created after a removal of the first key": {
			before:  stringtest.LinesLF("a: 1", "b: 2"),
			applies: []edit.Func{both(at("$.a", remove), at("$.z", set(yamlMarshaler("&q 1"))))},
			err:     stringtest.JoinLF("test.yaml:2:1: $: v1 sets $.z "+anchor, noAlias, "`-- v1 sets $.z"),
		},
		"repeated key in a value at a key created after a removal of the first key": {
			before:  stringtest.LinesLF("a: 1", "b: 2"),
			applies: []edit.Func{both(at("$.a", remove), at("$.z", set(dupMap)))},
			err: stringtest.JoinLF(
				"test.yaml:2:1: $: v1 sets $.z, but "+twoKeys,
				"|-- remove one of the two",
				"`-- v1 sets $.z",
			),
		},
		"control: read of a missing key after a removal of the first key": {
			before:  stringtest.LinesLF("a: 1", "b: 2"),
			applies: []edit.Func{both(at("$.a", remove), decode("$.zz"))},
			err: "test.yaml:2:1: $: v1 cannot decode $.zz, which holds no value\n" +
				"`-- v1 reads $.zz",
		},
		"control: engine error at a created key of the root": {
			before:  stringtest.LinesLF("a: 1", "b: 2"),
			applies: []edit.Func{at("$.z", set("x\x01y"))},
			err:     stringtest.JoinLF("test.yaml:1:1: $: "+engine, report, "`-- v1 sets $.z"),
		},
		"engine error at an item created after a removal of the first item": {
			before:  stringtest.LinesLF("- a", "- b"),
			applies: []edit.Func{both(at("$[0]", remove), at("$[1]", set("x\x01y")))},
			err:     stringtest.JoinLF("test.yaml:2:3: $: "+engine, report, "`-- v1 sets $[1]"),
		},
		"control: set past the end after a removal of the first item": {
			before:  stringtest.LinesLF("- a", "- b"),
			applies: []edit.Func{both(at("$[0]", remove), at("$[3].x", set(1)))},
			err: "test.yaml:2:3: $: v1 sets $[3].x, but the document holds 1 item\n" +
				"`-- v1 sets $[3].x",
		},
		"engine error at a key created in a flow root after a removal of its first key": {
			before:  "{a: 1, b: 2}\n",
			applies: []edit.Func{both(at("$.a", remove), at("$.z", set("x\x01y")))},
			err:     stringtest.JoinLF("test.yaml:1:8: $: "+engine, report, "`-- v1 sets $.z"),
		},
		"control: read of a missing key of a flow root after a removal of its first key": {
			before:  "{a: 1, b: 2}\n",
			applies: []edit.Func{both(at("$.a", remove), decode("$.zz"))},
			err: "test.yaml:1:8: $: v1 cannot decode $.zz, which holds no value\n" +
				"`-- v1 reads $.zz",
		},
		// Fix20 P61: for an empty flow root, the root binds past its "{" or
		// "[".
		"engine error at a key created in an empty flow mapping": {
			before:  "{}\n",
			applies: []edit.Func{at("$.a", set("x\x01y"))},
			err:     stringtest.JoinLF("test.yaml:1:2: $: "+engine, report, "`-- v1 sets $.a"),
		},
		"engine error at an item created in an empty flow sequence": {
			before:  "[]\n",
			applies: []edit.Func{at("$[0]", set("x\x01y"))},
			err:     stringtest.JoinLF("test.yaml:1:2: $: "+engine, report, "`-- v1 sets $[0]"),
		},
		"engine error at a key created in an empty flow mapping with a space": {
			before:  "{ }\n",
			applies: []edit.Func{at("$.a", set("x\x01y"))},
			err:     stringtest.JoinLF("test.yaml:1:2: $: "+engine, report, "`-- v1 sets $.a"),
		},
		"read of a missing key of an empty flow mapping": {
			before:  "{}\n",
			applies: []edit.Func{decode("$.a")},
			err: "test.yaml:1:2: $: v1 cannot decode $.a, which holds no value\n" +
				"`-- v1 reads $.a",
		},
		"control: engine error at a key created in a flow root that an edit emptied": {
			before:  "{b: 1}\n",
			applies: []edit.Func{both(at("$.b", remove), at("$.a", set("x\x01y")))},
			err:     stringtest.JoinLF("test.yaml:1:2: $: "+engine, report, "`-- v1 sets $.a"),
		},
		"control: engine error at a key created in a flow root": {
			before:  "{b: 1}\n",
			applies: []edit.Func{at("$.a", set("x\x01y"))},
			err:     stringtest.JoinLF("test.yaml:1:2: $: "+engine, report, "`-- v1 sets $.a"),
		},
		// Fix20 P61: an error that a caller binds by a path keeps the path,
		// and binds at the site of the nearest value above it that the user
		// wrote.
		"Func error at a missing field below a value of an inline merge": {
			before:  stringtest.LinesLF("o: 0", "m:", "  <<: {s: {k: 1}}", "  g: 2"),
			applies: []edit.Func{fail("$.m.s.missing")},
			bind:    "$.m.s.missing",
			err:     "test.yaml:3:8: $.m.s.missing: bad",
			bound:   "test.yaml:3:8: $.m.s.missing: bad",
		},
		"Func error at a missing field below a value of a block merge": {
			before:  stringtest.LinesLF("o: 0", "m:", "  <<:", "    s:", "      k: 1", "  g: 2"),
			applies: []edit.Func{fail("$.m.s.missing")},
			err:     "test.yaml:4:5: $.m.s.missing: bad",
		},
		"control: read of a missing field below a value of an inline merge": {
			before:  stringtest.LinesLF("o: 0", "m:", "  <<: {s: {k: 1}}", "  g: 2"),
			applies: []edit.Func{decode("$.m.s.missing")},
			err: "test.yaml:3:8: $.m: v1 cannot decode $.m.s.missing, which holds no value\n" +
				"`-- v1 reads $.m.s.missing",
		},
		"Func error at a missing field below a merge written in place": {
			before:  stringtest.LinesLF("svc:", "  <<: {sub: {k: 1}}", "  name: n"),
			applies: []edit.Func{fail("$.svc.sub.missing")},
			bind:    "$.svc.sub.missing",
			err:     "test.yaml:2:8: $.svc.sub.missing: bad",
			bound:   "test.yaml:2:8: $.svc.sub.missing: bad",
		},
		"Func error at a missing field below a merge through an alias": {
			before: stringtest.LinesLF(
				"defaults: &d",
				"  <<: {resources: {limits: {cpu: 1}}}",
				"svc:",
				"  <<: *d",
				"  name: web",
			),
			applies: []edit.Func{fail("$.svc.resources.limits.memory")},
			bind:    "$.defaults.resources.limits.memory",
			err:     "test.yaml:2:20: $.svc.resources.limits.memory: bad",
			bound:   "test.yaml:2:20: $.defaults.resources.limits.memory: bad",
		},
		"Func error at a missing field below a merge of an item": {
			before:  stringtest.LinesLF("l:", "  - <<: {sub: {k: 1}}", "    o: 1"),
			applies: []edit.Func{fail("$.l[0].sub.missing")},
			err:     "test.yaml:2:10: $.l[0].sub.missing: bad",
		},
		"Func error at a missing field below a merge of a sequence": {
			before:  stringtest.LinesLF("m:", "  <<: [{a: {k: 1}}, {b: 2}]"),
			applies: []edit.Func{fail("$.m.a.missing")},
			err:     "test.yaml:2:9: $.m.a.missing: bad",
		},
		// The alias *r is the site of the value at $.m.k, as an error at
		// $.m.k binds.
		"Func error at a missing field below a reference alias of a merge": {
			before:  stringtest.LinesLF("m:", "  <<: {k: *r}", "  o: 1"),
			ref:     "r: &r {a: 1}\n",
			applies: []edit.Func{fail("$.m.k.x")},
			err:     "test.yaml:2:11: $.m.k.x: bad",
		},
		"control: Func error at a missing field without a merge": {
			before:  stringtest.LinesLF("m:", "  s: {k: 1}"),
			applies: []edit.Func{fail("$.m.s.x")},
			err:     "test.yaml:2:3: $.m.s.x: bad",
		},
		"Func error at a missing field below an alias": {
			before:  stringtest.LinesLF("o: 0", "base: &b", "  sub: {k: 1}", "svc: *b"),
			applies: []edit.Func{fail("$.svc.sub.missing")},
			err:     "test.yaml:3:3: $.svc.sub.missing: bad",
		},
		"control: Func error at a missing field below an item that a removal moved": {
			before:  stringtest.LinesLF("- 1", "- {k: 1}"),
			applies: []edit.Func{both(at("$[0]", remove), fail("$[0].zz"))},
			err:     "test.yaml:2:1: $[0].zz: bad",
		},
		"Func error below a scalar item that a removal moved": {
			before:  stringtest.LinesLF("l:", "  - 1", "  - 2"),
			applies: []edit.Func{both(at("$.l[0]", remove), fail("$.l[0].x"))},
			err:     "test.yaml:3:5: $.l[0].x: bad",
		},
		"control: Func error at a field of an item that a removal moved": {
			before:  stringtest.LinesLF("- 1", "- {k: 1}"),
			applies: []edit.Func{both(at("$[0]", remove), fail("$[0].k"))},
			err:     "test.yaml:2:7: $[0].k: bad",
		},
		"Func error past the end of a sequence after a removal": {
			before:  stringtest.LinesLF("- 1", "- 2"),
			applies: []edit.Func{both(at("$[0]", remove), fail("$[4]"))},
			err:     "test.yaml:2:3: $[4]: bad",
		},
		"Func error below a scalar": {
			before:  stringtest.LinesLF("o: 0", "a: 1"),
			applies: []edit.Func{fail("$.a.b.c")},
			bind:    "$.a.b.c",
			err:     "test.yaml:2:4: $.a.b.c: bad",
			bound:   "test.yaml:2:4: $.a.b.c: bad",
		},
		"Func error past the end of a sequence": {
			before:  stringtest.LinesLF("o: 0", "l:", "  - 1"),
			applies: []edit.Func{fail("$.l[5]")},
			bind:    "$.l[5]",
			err:     "test.yaml:2:1: $.l[5]: bad",
			bound:   "test.yaml:2:1: $.l[5]: bad",
		},
		"control: set below a scalar": {
			before:  stringtest.LinesLF("o: 0", "a: 1"),
			applies: []edit.Func{at("$.a.b.c", set(1))},
			err: "test.yaml:2:4: $.a: v1 sets $.a.b.c, but a holds a scalar\n" +
				"`-- v1 sets $.a.b.c",
		},
		"control: Func error at a missing key of the root": {
			before:  stringtest.LinesLF("a: 1", "b: 2"),
			applies: []edit.Func{fail("$.zz")},
			err:     "test.yaml:1:1: $.zz: bad",
		},
		"control: Func error at a missing key of the root after a removal of the first key": {
			before:  stringtest.LinesLF("a: 1", "b: 2"),
			applies: []edit.Func{both(at("$.a", remove), fail("$.zz"))},
			err:     "test.yaml:2:1: $.zz: bad",
		},
		// Fix20 P61: a problem in an item binds under the path of the item,
		// a pair without braces included.
		"move of a pair item of a flow sequence onto a key that exists": {
			before:  stringtest.LinesLF("l: [a: 1, x]", "m:", "  b: 1"),
			applies: []edit.Func{move("$.l[0]", "$.m.b")},
			err: "test.yaml:1:5: $.l[0]: v1 moves $.l[0] to $.m.b, which already exists\n" +
				"`-- v1 moves $.l[0] to $.m.b",
		},
		"move of a later pair item of a flow sequence onto a key that exists": {
			before:  stringtest.LinesLF("l: [x, b: 2]", "m:", "  b: 1"),
			applies: []edit.Func{move("$.l[1]", "$.m.b")},
			err: "test.yaml:1:8: $.l[1]: v1 moves $.l[1] to $.m.b, which already exists\n" +
				"`-- v1 moves $.l[1] to $.m.b",
		},
		"set of a held pair item that an edit removed": {
			before:  stringtest.LinesLF("l: [a: 1, x]", "m: 0"),
			applies: []edit.Func{held("$.l[0]", at("$.l[0]", remove))},
			err: "test.yaml:1:5: $.l[0]: v1 sets $.l[0] through a node that lost its place to an earlier edit\n" +
				"`-- v1 sets $.l[0]",
		},
		"engine error at a key created in a pair item": {
			before:  "l: [a: 1, x]\n",
			applies: []edit.Func{at("$.l[0].z", set("x\x01y"))},
			err:     stringtest.JoinLF("test.yaml:1:5: $.l[0]: "+engine, report, "`-- v1 sets $.l[0].z"),
		},
		"control: move of a braced item of a flow sequence onto a key that exists": {
			before:  stringtest.LinesLF("l: [{a: 1}, x]", "m:", "  b: 1"),
			applies: []edit.Func{move("$.l[0]", "$.m.b")},
			err: "test.yaml:1:5: $.l[0]: v1 moves $.l[0] to $.m.b, which already exists\n" +
				"`-- v1 moves $.l[0] to $.m.b",
		},
		// Fix20 P61: a detail names the edit that writes the place it names,
		// with the path of that place after the edits.
		"refusal at a place below a value that an earlier Apply created": {
			before: stringtest.LinesLF("z0: 0", "m:", "  a: 1"),
			applies: []edit.Func{
				at("$.m.n", set(map[string]any{"p": 1})),
				at("$.m.n.z", set(yamlMarshaler("&q 1"))),
			},
			err: stringtest.JoinLF(
				"test.yaml:2:1: $.m: v2 sets $.m.n.z "+anchor,
				noAlias,
				"|-- v1 sets $.m.n",
				"`-- v2 sets $.m.n.z",
			),
		},
		"engine error at a place below a value that an earlier Apply created": {
			before: stringtest.LinesLF("z0: 0", "m:", "  a: 1"),
			applies: []edit.Func{
				at("$.m.n", set(map[string]any{"p": 1})),
				at("$.m.n.z", set("x\x01y")),
			},
			err: stringtest.JoinLF("test.yaml:2:1: $.m: "+engine, report, "|-- v1 sets $.m.n", "`-- v2 sets $.m.n.z"),
		},
		"engine error at a place below a value that an earlier Apply created through an alias": {
			before: stringtest.LinesLF("base: &b", "  a: 1", "md: *b"),
			applies: []edit.Func{
				at("$.md.n", set(map[string]any{"p": 1})),
				at("$.md.n.z", set("x\x01y")),
			},
			// The alias binds at itself, as an error at it does (fix20 B2).
			err: stringtest.JoinLF(
				"test.yaml:3:5: $.md: "+engine,
				report,
				"|-- v1 sets $.md.n",
				"`-- v2 sets $.md.n.z",
			),
		},
		"control: engine error at a place below a value that the same Apply created": {
			before: stringtest.LinesLF("z0: 0", "m:", "  a: 1"),
			applies: []edit.Func{
				both(at("$.m.n", set(map[string]any{"p": 1})), at("$.m.n.z", set("x\x01y"))),
			},
			err: stringtest.JoinLF("test.yaml:2:1: $.m: "+engine, report, "|-- v1 sets $.m.n", "`-- v1 sets $.m.n.z"),
		},
		// A problem that the edit package finds at a value that an earlier
		// Apply created shows the path of the site where it binds, and an
		// error that a caller binds there keeps its path.
		"control: engine error at a value that an earlier Apply created": {
			before: stringtest.LinesLF("z0: 0", "m:", "  a: 1"),
			applies: []edit.Func{
				at("$.m.n", set(map[string]any{"p": 1})),
				at("$.m.n.p", set("x\x01y")),
			},
			err: stringtest.JoinLF("test.yaml:2:1: $.m: "+engine, report, "|-- v1 sets $.m.n", "`-- v2 sets $.m.n.p"),
		},
		"control: set below a scalar that an earlier Apply created": {
			before: stringtest.LinesLF("z0: 0", "m:", "  a: 1"),
			applies: []edit.Func{
				at("$.m.n", set(map[string]any{"p": 1})),
				at("$.m.n.p.q", set(1)),
			},
			err: stringtest.JoinLF(
				"test.yaml:2:1: $.m: v2 sets $.m.n.p.q, but p holds a scalar",
				"|-- v1 sets $.m.n",
				"`-- v2 sets $.m.n.p.q",
			),
		},
		"Func error at a value that an earlier Apply created": {
			before: stringtest.LinesLF("z0: 0", "m:", "  a: 1"),
			applies: []edit.Func{
				at("$.m.n", set(map[string]any{"p": 1})),
				fail("$.m.n.p"),
			},
			bind:  "$.m.n.p",
			err:   stringtest.JoinLF("test.yaml:2:1: $.m.n.p: bad", "`-- v1 sets $.m.n"),
			bound: stringtest.JoinLF("test.yaml:2:1: $.m.n.p: bad", "`-- v1 sets $.m.n"),
		},
		// A detail names the call by the path that the call was given
		// (fix21 P65).
		"repeated key in a value set at a merged entry": {
			before:  stringtest.LinesLF("b: &b", "  a: 1", "m:", "  <<: *b", "  o: 1"),
			applies: []edit.Func{at("$.m.a", set(dupMap))},
			err: stringtest.JoinLF(
				"test.yaml:2:6: $.b.a: v1 sets $.m.a, but "+twoKeys,
				"|-- remove one of the two",
				"`-- v1 sets $.m.a from this value",
			),
		},
		"control: repeated key in a value set through an alias": {
			before:  stringtest.LinesLF("b: &b", "  a: 1", "m: *b"),
			applies: []edit.Func{at("$.m.a", set(dupMap))},
			err: stringtest.JoinLF(
				"test.yaml:2:6: $.b.a: v1 sets $.m.a, but "+twoKeys,
				"|-- remove one of the two",
				"`-- v1 sets $.m.a from this value",
			),
		},
		// A Set replaces an alias at its place, and the detail names the path
		// that the call was given (fix21 P65).
		"control: repeated key in a value set over an alias": {
			before:  stringtest.LinesLF("r0:", "  b: &x0 1e3", "r1: *x0"),
			applies: []edit.Func{at("$.r1", set(dupMap))},
			err: stringtest.JoinLF(
				"test.yaml:3:5: $.r1: v1 sets $.r1, but "+twoKeys,
				"|-- remove one of the two",
				"`-- v1 sets $.r1 from this value",
			),
		},
		"control: repeated key in a value set at an entry of a merge through an alias": {
			before:  stringtest.LinesLF("r0: &x0", "  <<: {p: 1}", "x: *x0"),
			applies: []edit.Func{at("$.x.p", set(dupMap))},
			err: stringtest.JoinLF(
				"test.yaml:2:11: $.r0: v1 sets $.x.p, but "+twoKeys,
				"|-- remove one of the two",
				"`-- v1 sets $.x.p from this value",
			),
		},
		"control: repeated key in a value set below a merged entry": {
			before:  stringtest.LinesLF("b: &b", "  a: {x: 1}", "m:", "  <<: *b", "  o: 1"),
			applies: []edit.Func{at("$.m.a.x", set(dupMap))},
			err: stringtest.JoinLF(
				"test.yaml:2:10: $.b.a.x: v1 sets $.m.a.x, but "+twoKeys,
				"|-- remove one of the two",
				"`-- v1 sets $.m.a.x from this value",
			),
		},
		// A collection that a Set writes binds at its key or its "-", which
		// the Set keeps. Its first key or "-" belongs to a value that the Set
		// creates inside it, so a caller's error at that value keeps its path
		// and binds at the key of the collection (fix20 G2-1, G2-2).
		"G2-1: caller error at the first key of a mapping that a Set wrote, where the key holds a mapping": {
			before:  stringtest.LinesLF("o: 0", "m:", "  a: 1"),
			applies: []edit.Func{both(at("$.m", set(map[string]any{"b": map[string]int{"c": 1}})), fail("$.m.b"))},
			err:     stringtest.JoinLF("test.yaml:2:1: $.m.b: bad", "`-- v1 sets $.m from this value"),
		},
		"G2-1: caller error at the first key of a mapping that a Set wrote, where the key holds a sequence": {
			before:  stringtest.LinesLF("o: 0", "m:", "  a: 1"),
			applies: []edit.Func{both(at("$.m", set(map[string]any{"b": []int{1}})), fail("$.m.b"))},
			err:     stringtest.JoinLF("test.yaml:2:1: $.m.b: bad", "`-- v1 sets $.m from this value"),
		},
		"G2-1: error that Doc.Node binds at the first key of a mapping that a Set wrote": {
			before:  stringtest.LinesLF("o: 0", "m:", "  a: 1"),
			applies: []edit.Func{at("$.m", set(map[string]any{"b": map[string]int{"c": 1}}))},
			bind:    "$.m.b",
			bound:   stringtest.JoinLF("test.yaml:2:1: $.m.b: bad", "`-- v1 sets $.m from this value"),
		},
		"G2-1: caller error at the first item of a sequence that a Set wrote, where the item holds a mapping": {
			before:  stringtest.LinesLF("o: 0", "l:", "  - 1"),
			applies: []edit.Func{both(at("$.l", set([]any{map[string]int{"c": 1}})), fail("$.l[0]"))},
			err:     stringtest.JoinLF("test.yaml:2:1: $.l[0]: bad", "`-- v1 sets $.l from this value"),
		},
		"G2-1 control: engine error at a scalar that a Set wrote over a sequence that an earlier Set wrote": {
			before: stringtest.LinesLF("o: 0", "r0: 1"),
			applies: []edit.Func{both(
				both(at("$.r0", set([]int{1})), at("$.r0", set("q"))),
				at("$.r0", set("x\x01y")),
			)},
			err: stringtest.JoinLF("test.yaml:2:1: $.r0: "+engine, report, "`-- v1 sets $.r0 from this value"),
		},
		"G2-2: caller error inside a mapping that a Set wrote over an anchored scalar": {
			before: stringtest.LinesLF("o: 0", "a: &q 7", "b: *q"),
			applies: []edit.Func{
				both(at("$.a", set(map[string]any{"zz": map[string]int{"yy": 1}})), fail("$.a.zz.yy")),
			},
			err: stringtest.JoinLF("test.yaml:2:1: $.a.zz.yy: bad", "`-- v1 sets $.a from this value"),
		},
		"G2-2: caller error inside a mapping that a Set wrote over an anchored scalar on its own line": {
			before: stringtest.LinesLF("o: 0", "a: &q", "  7", "b: *q"),
			applies: []edit.Func{
				both(at("$.a", set(map[string]any{"zz": map[string]int{"yy": 1}})), fail("$.a.zz.yy")),
			},
			err: stringtest.JoinLF("test.yaml:2:1: $.a.zz.yy: bad", "`-- v1 sets $.a from this value"),
		},
		"G2-2: engine error inside a mapping that a Set wrote over an anchored scalar": {
			before: stringtest.LinesLF("o: 0", "a: &q 7", "b: *q"),
			applies: []edit.Func{both(
				at("$.a", set(map[string]any{"zz": map[string]int{"yy": 1}})),
				at("$.a.zz.qq", set("x\x01y")),
			)},
			err: stringtest.JoinLF(
				"test.yaml:2:1: $.a: "+engine,
				report,
				"|-- v1 sets $.a from this value",
				"`-- v1 sets $.a.zz.qq",
			),
		},
		"G2-2 control: caller error inside a mapping that a Set wrote over a scalar": {
			before: stringtest.LinesLF("o: 0", "a: 7"),
			applies: []edit.Func{
				both(at("$.a", set(map[string]any{"zz": map[string]int{"yy": 1}})), fail("$.a.zz.yy")),
			},
			err: stringtest.JoinLF("test.yaml:2:1: $.a.zz.yy: bad", "`-- v1 sets $.a from this value"),
		},
		// The root binds at its first item, and a collection that a Set wrote
		// over that item binds at the "-" of the item.
		"G2-1: caller error past the end of a root sequence whose first item a Set replaced with a mapping": {
			before:  stringtest.LinesLF("- 1", "- 2"),
			applies: []edit.Func{both(at("$[0]", set(map[string]int{"x": 1})), fail("$[5]"))},
			err:     "test.yaml:1:1: $[5]: bad",
		},
		"G2-1: engine error past the end of a root sequence whose first item a Set replaced with a mapping": {
			before:  stringtest.LinesLF("- 1", "- 2"),
			applies: []edit.Func{both(at("$[0]", set(map[string]int{"x": 1})), at("$[5]", set(1)))},
			err: "test.yaml:1:1: $: v1 sets $[5], but the document holds 2 items\n" +
				"`-- v1 sets $[5]",
		},
		"G2-1 control: caller error past the end of a root sequence whose first item a Set replaced with a scalar": {
			before:  stringtest.LinesLF("- 1", "- 2"),
			applies: []edit.Func{both(at("$[0]", set(5)), fail("$[5]"))},
			err:     "test.yaml:1:3: $[5]: bad",
		},
		// A value that a Set writes over an item of a flow sequence stands for
		// that item, and an item that a Move takes into a flow sequence keeps
		// the place of the entry or the item that held it (fix20 F3, F4). A
		// caller's error below either keeps the path that the caller gave
		// (fix21 P65).
		"F3: caller error below a mapping that a Set wrote over an item of a flow sequence, after a removal": {
			before: "l: [x, y]\n",
			applies: []edit.Func{both(
				both(at("$.l[1]", set(map[string]int{"k": 1})), at("$.l[0]", remove)),
				fail("$.l[0].k"),
			)},
			err: stringtest.JoinLF("test.yaml:1:8: $.l[0].k: bad", "`-- v1 sets $.l[1] from this value"),
		},
		"F3: caller error below a scalar that a Set wrote over an item of a flow sequence, after a removal": {
			before:  "l: [x, y]\n",
			applies: []edit.Func{both(both(at("$.l[1]", set(5)), at("$.l[0]", remove)), fail("$.l[0].zz"))},
			err:     stringtest.JoinLF("test.yaml:1:8: $.l[0].zz: bad", "`-- v1 sets $.l[1] from this value"),
		},
		"F3: caller error below an item of a flow root that a Set wrote, after a removal": {
			before:  "[x, y]\n",
			applies: []edit.Func{both(both(at("$[1]", set(5)), at("$[0]", remove)), fail("$[0].zz"))},
			err:     stringtest.JoinLF("test.yaml:1:5: $[0].zz: bad", "`-- v1 sets $[1] from this value"),
		},
		"F3 control: caller error below an item of a flow sequence, after a removal": {
			before:  "l: [x, y]\n",
			applies: []edit.Func{both(at("$.l[0]", remove), fail("$.l[0].zz"))},
			err:     "test.yaml:1:8: $.l[0].zz: bad",
		},
		"F3 control: caller error below an item of a flow sequence that a path reaches through an alias": {
			before:  stringtest.LinesLF("a: &x [1, 2, 3]", "b: *x"),
			applies: []edit.Func{both(at("$.a[0]", remove), fail("$.b[0].zz"))},
			err:     "test.yaml:1:11: $.b[0].zz: bad",
		},
		"F3 control: caller error below an item that a Set wrote, through an alias": {
			before:  stringtest.LinesLF("a: &x [1, 2]", "b: *x"),
			applies: []edit.Func{both(at("$.a[1]", set(5)), fail("$.b[1].zz"))},
			err:     stringtest.JoinLF("test.yaml:1:11: $.b[1].zz: bad", "`-- v1 sets $.a[1] from this value"),
		},
		"F3 control: caller error below an item of a flow sequence inside a merge entry": {
			before:  stringtest.LinesLF("m:", "  <<: {l: [x, y]}", "  o: 1"),
			applies: []edit.Func{fail("$.m.l[1].zz")},
			err:     "test.yaml:2:15: $.m.l[1].zz: bad",
		},
		"F4: caller error below an item that a Move took into a flow sequence": {
			before:  stringtest.LinesLF("l: [x, y]", "m: [5]"),
			applies: []edit.Func{both(move("$.m[0]", "$.l[2]"), fail("$.l[2].zz"))},
			err:     "test.yaml:2:5: $.l[2].zz: bad",
		},
		"F4: caller error below a mapping that a Move took into a flow sequence": {
			before:  stringtest.LinesLF("l: [x, y]", "m: [{k: 1}]"),
			applies: []edit.Func{both(move("$.m[0]", "$.l[2]"), fail("$.l[2].k.zz"))},
			err:     "test.yaml:2:9: $.l[2].k.zz: bad",
		},
		"F4: caller error below the value of an entry that a Move took into a flow sequence": {
			before:  stringtest.LinesLF("l: [x, y]", "z: 5"),
			applies: []edit.Func{both(move("$.z", "$.l[2]"), fail("$.l[2].zz"))},
			err:     "test.yaml:2:4: $.l[2].zz: bad",
		},
		"F4 control: caller error below an item that a Move took into a block sequence": {
			before:  stringtest.LinesLF("l:", "  - x", "  - y", "m: [5]"),
			applies: []edit.Func{both(move("$.m[0]", "$.l[2]"), fail("$.l[2].zz"))},
			err:     "test.yaml:4:5: $.l[2].zz: bad",
		},
		// The edit does not read the names that a reference document brings
		// in, so an error at a name that a merge key of the document decides
		// binds at its alias, as a read there does (fix20 F5).
		"F5: caller error at a name beside a merge key of a reference document that lacks it": {
			before:  stringtest.LinesLF("o: 0", "m:", "  <<: *r", "  b: 1"),
			ref:     "r: &r {x: 2}\n",
			applies: []edit.Func{fail("$.m.zz")},
			err:     "test.yaml:3:7: $.m.zz: bad",
		},
		"F5: error that Doc.Node binds at a name that a merge key of a reference document lacks": {
			before:  stringtest.LinesLF("o: 0", "m:", "  <<: *r", "  b: 1"),
			ref:     "r: &r {x: 2}\n",
			applies: []edit.Func{func(context.Context, *edit.Node) error { return nil }},
			bind:    "$.m.zz",
			bound:   "test.yaml:3:7: $.m.zz: bad",
		},
		"F5 control: caller error at a name that a merge key of a reference document brings in": {
			before:  stringtest.LinesLF("o: 0", "m:", "  <<: *r", "  b: 1"),
			ref:     "r: &r {x: 2}\n",
			applies: []edit.Func{fail("$.m.x")},
			err:     "test.yaml:3:7: $.m.x: bad",
		},
		// A value that a Set created inside the value that it wrote and that
		// a Move took elsewhere binds at the site of its new place, which the
		// Set did not replace (fix20 F7).
		"F7: caller error at a value that a Set created and a Move took away": {
			before: stringtest.LinesLF("o: 0", "m:", "  a: 1", "n:", "  b: 2"),
			applies: []edit.Func{both(
				both(at("$.m", set(map[string]any{"x": map[string]int{"y": 1}})), move("$.m.x", "$.n.q")),
				fail("$.n.q.y"),
			)},
			err: stringtest.JoinLF("test.yaml:4:1: $.n.q.y: bad", "`-- v1 sets $.m"),
		},
		"F7 control: caller error at a value that a Set created": {
			before:  stringtest.LinesLF("o: 0", "m:", "  a: 1"),
			applies: []edit.Func{both(at("$.m", set(map[string]any{"x": map[string]int{"y": 1}})), fail("$.m.x.y"))},
			err:     stringtest.JoinLF("test.yaml:2:1: $.m.x.y: bad", "`-- v1 sets $.m from this value"),
		},
		// A call through a node whose place an earlier call took away binds
		// where a problem at that place binds (fix20 G2-3).
		"G2-3: call through a node whose item a removal took away": {
			before:  stringtest.LinesLF("- 1", "- 2"),
			applies: []edit.Func{held("$[0]", at("$[0]", remove))},
			err: "test.yaml:1:3: $[0]: v1 sets $[0] through a node that lost its place to an earlier edit\n" +
				"`-- v1 sets $[0]",
		},
		"G2-3: call through a node whose entry a removal took away": {
			before:  stringtest.LinesLF("a: &x 1", "b: 2"),
			applies: []edit.Func{held("$.a", at("$.a", remove))},
			err: "test.yaml:1:7: $.a: v1 sets $.a through a node that lost its place to an earlier edit\n" +
				"`-- v1 sets $.a",
		},
		// A problem at a scalar that a Set wrote binds at the scalar and names
		// the Set, so a call through a node whose place a removal then took
		// away does too.
		"G2-3: call through a node whose entry a Set wrote and a removal took away": {
			before:  stringtest.LinesLF("a: 1", "b: 2"),
			applies: []edit.Func{held("$.a", both(at("$.a", set("v")), at("$.a", remove)))},
			err: stringtest.JoinLF(
				"test.yaml:1:4: $.a: v1 sets $.a through a node that lost its place to an earlier edit",
				"`-- v1 sets $.a from this value",
			),
		},
		"G2-3 control: call through a node whose entry a Set wrote as a mapping and a removal took away": {
			before:  stringtest.LinesLF("a: 1", "b: 2"),
			applies: []edit.Func{held("$.a", both(at("$.a", set(map[string]int{"x": 1})), at("$.a", remove)))},
			err: "test.yaml:1:1: $.a: v1 sets $.a through a node that lost its place to an earlier edit\n" +
				"`-- v1 sets $.a",
		},
		// The refusal of a call through a node whose place an earlier call
		// took comes before every other check of the call, and it names the
		// place that the call asked for (fix21 P66).
		"G3-4: call through a node below an entry that a removal took away": {
			before:  stringtest.LinesLF("o: 0", "m:", "  b: 1"),
			applies: []edit.Func{held("$.m.b", at("$.m", remove))},
			err: "test.yaml:2:1: $.m: v1 sets $.m.b through a node that lost its place to an earlier edit\n" +
				"`-- v1 sets $.m.b",
		},
		"G3-4: call through a node below an entry that a Set replaced": {
			before:  stringtest.LinesLF("o: 0", "m:", "  b: 1"),
			applies: []edit.Func{held("$.m.b", at("$.m", set(5)))},
			err: "test.yaml:2:1: $.m: v1 sets $.m.b through a node that lost its place to an earlier edit\n" +
				"`-- v1 sets $.m.b",
		},
		"G3-4: call through a node at a missing place below an entry that a removal took away": {
			before:  stringtest.LinesLF("o: 0", "m:", "  b: 1"),
			applies: []edit.Func{held("$.m.zz", at("$.m", remove))},
			err: "test.yaml:2:1: $.m: v1 sets $.m.zz through a node that lost its place to an earlier edit\n" +
				"`-- v1 sets $.m.zz",
		},
		"G3-2: call through a node under a reference merge key that a removal took away": {
			before:  stringtest.LinesLF("o: 0", "m:", "  <<: *r", "  b: 1"),
			ref:     "r: &r {x: 2}\n",
			applies: []edit.Func{held("$.m.zz", at("$.m", remove))},
			err: "test.yaml:2:1: $.m: v1 sets $.m.zz through a node that lost its place to an earlier edit\n" +
				"`-- v1 sets $.m.zz",
		},
		"G3-2: call through a node under a reference merge key that a Set replaced": {
			before:  stringtest.LinesLF("o: 0", "m:", "  <<: *r", "  b: 1"),
			ref:     "r: &r {x: 2}\n",
			applies: []edit.Func{held("$.m.zz", at("$.m", set(map[string]int{"zz": 1})))},
			err: "test.yaml:2:1: $.m: v1 sets $.m.zz through a node that lost its place to an earlier edit\n" +
				"`-- v1 sets $.m.zz",
		},
		"G3-2 control: fresh call at the place after the Set": {
			before:  stringtest.LinesLF("o: 0", "m:", "  <<: *r", "  b: 1"),
			ref:     "r: &r {x: 2}\n",
			applies: []edit.Func{both(at("$.m", set(map[string]int{"zz": 1})), at("$.m.zz", set(5)))},
		},
		// A value that a Set writes over a value that an edit created is a
		// value that an edit created too. A caller's error there keeps its
		// path and binds at the nearest value above that the user wrote, and
		// the detail does not say "from this value" (fix20 C2, C3).
		"C3: caller error at a scalar that a Set wrote over a created scalar": {
			before:  stringtest.LinesLF("m:", "  a: 1"),
			applies: []edit.Func{both(both(at("$.m.n", set(9)), at("$.m.n", set(5))), fail("$.m.n"))},
			err:     stringtest.JoinLF("test.yaml:1:1: $.m.n: bad", "`-- v1 sets $.m.n"),
		},
		"C3: error that Doc.Node binds at a scalar that a Set wrote over a created scalar": {
			before:  stringtest.LinesLF("m:", "  a: 1"),
			applies: []edit.Func{both(at("$.m.n", set(9)), at("$.m.n", set(5)))},
			bind:    "$.m.n",
			bound:   stringtest.JoinLF("test.yaml:1:1: $.m.n: bad", "`-- v1 sets $.m.n"),
		},
		"C3: caller error at a created scalar that a Move took and a Set replaced": {
			before: stringtest.LinesLF("o: 0", "m:", "  a: 1"),
			applies: []edit.Func{both(
				both(at("$.n", set(9)), move("$.n", "$.m.n")),
				both(at("$.m.n", set(5)), fail("$.m.n")),
			)},
			err: stringtest.JoinLF("test.yaml:2:1: $.m.n: bad", "`-- v1 sets $.m.n"),
		},
		"C3 control: caller error at a scalar that a Set created": {
			before:  stringtest.LinesLF("m:", "  a: 1"),
			applies: []edit.Func{both(at("$.m.n", set(9)), fail("$.m.n"))},
			err:     stringtest.JoinLF("test.yaml:1:1: $.m.n: bad", "`-- v1 sets $.m.n"),
		},
		"C2: caller error below an item of a flow sequence that a Set wrote over a created item": {
			before:  "l: [x, y]\n",
			applies: []edit.Func{both(both(at("$.l[2]", set(9)), at("$.l[2]", set(5))), fail("$.l[2].zz"))},
			err:     stringtest.JoinLF("test.yaml:1:1: $.l[2].zz: bad", "`-- v1 sets $.l[2]"),
		},
		"C2: caller error below an item that a Set wrote over a created item beside a key of the user": {
			before: "m: {a: [x], zz: 1}\n",
			applies: []edit.Func{both(
				both(at("$.m.b", set([]int{1})), at("$.m.b[0]", set(2))),
				fail("$.m.b[0].zz"),
			)},
			err: stringtest.JoinLF("test.yaml:1:1: $.m.b[0].zz: bad", "`-- v1 sets $.m.b[0]"),
		},
		// A Func error reads an `@` path from its node, also after a call
		// took the place of the node away, and binds at the nearest value
		// above it (fix21 B3).
		"B3: caller error at an @ path after a Remove of the node": {
			before:  stringtest.LinesLF("o: 0", "a:", "  b:", "    x: 1", "  x: 9"),
			apply:   "$.a.b",
			applies: []edit.Func{both(remove, fail("@.x"))},
			err:     "test.yaml:2:1: $.a.b.x: bad",
		},
		"B3: caller error at an @ path after a Remove of the parent of the node": {
			before:  stringtest.LinesLF("o: 0", "a:", "  b:", "    x: 1", "x: 7"),
			apply:   "$.a.b",
			applies: []edit.Func{both(at("$.a", remove), fail("@.x"))},
			err:     "test.yaml:1:1: $.a.b.x: bad",
		},
		"B3: caller error without a location after a Remove of the node": {
			before:  stringtest.LinesLF("o: 0", "a:", "  b:", "    x: 1", "  x: 9"),
			apply:   "$.a.b",
			applies: []edit.Func{both(remove, unlocated)},
			err:     "test.yaml:2:1: $.a.b: bad",
		},
		"B3 control: caller error at an @ path": {
			before:  stringtest.LinesLF("o: 0", "a:", "  b:", "    x: 1", "  x: 9"),
			apply:   "$.a.b",
			applies: []edit.Func{fail("@.x")},
			err:     "test.yaml:4:8: $.a.b.x: bad",
		},
		// A detail at text that a Move created names the Move (fix21 B7).
		"B7: caller error below a mapping that a Move created": {
			before:  stringtest.LinesLF("o: 0", "a: 1"),
			applies: []edit.Func{both(move("$.o", "$.n.p"), fail("$.n.q"))},
			err:     stringtest.JoinLF("test.yaml:2:1: $.n.q: bad", "`-- v1 moves $.o to $.n.p"),
		},
		"B7 control: caller error below a mapping that a Set created": {
			before:  stringtest.LinesLF("o: 0", "a: 1"),
			applies: []edit.Func{both(at("$.n.p", set(0)), fail("$.n.q"))},
			err:     stringtest.JoinLF("test.yaml:1:1: $.n.q: bad", "`-- v1 sets $.n.p"),
		},
		// A Set over a mapping that a Move created writes over a value that
		// an edit created, so the detail does not say "from this value", as
		// after a Set that created the mapping (fix21 F3).
		"F3: caller error below a mapping that a Set wrote over a mapping that a Move created": {
			before: stringtest.LinesLF("o: 0", "a: 1"),
			applies: []edit.Func{both(
				both(move("$.o", "$.n.p"), at("$.n", set(map[string]int{"q": 1}))),
				fail("$.n.q"),
			)},
			err: stringtest.JoinLF("test.yaml:2:1: $.n.q: bad", "`-- v1 sets $.n"),
		},
		"F3: caller error at a scalar that a Set wrote over a mapping that a Move created": {
			before:  stringtest.LinesLF("o: 0", "a: 1"),
			applies: []edit.Func{both(both(move("$.o", "$.n.p"), at("$.n", set(5))), fail("$.n"))},
			err:     stringtest.JoinLF("test.yaml:2:1: $.n: bad", "`-- v1 sets $.n"),
		},
		"F3: error that Doc.Node binds below a mapping that a later Apply set over a mapping that a Move created": {
			before:  stringtest.LinesLF("o: 0", "a: 1"),
			applies: []edit.Func{move("$.o", "$.n.p"), at("$.n", set(map[string]int{"p": 3}))},
			bind:    "$.n.p",
			bound:   stringtest.JoinLF("test.yaml:2:1: $.n.p: bad", "`-- v2 sets $.n"),
		},
		"F3 control: caller error below a mapping that a Set wrote over a mapping that a Set created": {
			before: stringtest.LinesLF("o: 0", "a: 1"),
			applies: []edit.Func{both(
				both(at("$.n.p", set(0)), at("$.n", set(map[string]int{"q": 1}))),
				fail("$.n.q"),
			)},
			err: stringtest.JoinLF("test.yaml:1:1: $.n.q: bad", "`-- v1 sets $.n"),
		},
		"F3 control: caller error below a mapping that a Set wrote over a mapping that a Move moved": {
			before: stringtest.LinesLF("o:", "  x: 1", "a: 1"),
			applies: []edit.Func{both(
				both(move("$.o", "$.n.p"), at("$.n.p", set(map[string]int{"q": 1}))),
				fail("$.n.p.q"),
			)},
			err: stringtest.JoinLF("test.yaml:1:1: $.n.p.q: bad", "`-- v1 sets $.n.p from this value"),
		},
		// A Move writes the bracket of a flow collection that it puts into a
		// flow mapping for the key that held the collection. The value that
		// a Set writes there still stands for the user's value, as in a
		// block mapping (fix21 C4).
		"C4: caller error below a mapping that a Set wrote over a flow sequence moved into a flow mapping": {
			before: stringtest.LinesLF("o: [1]", "t: {a: 1}"),
			applies: []edit.Func{both(
				both(move("$.o", "$.t.b"), at("$.t.b", set(map[string]int{"q": 1}))),
				fail("$.t.b.q"),
			)},
			err: stringtest.JoinLF("test.yaml:1:1: $.t.b.q: bad", "`-- v1 sets $.t.b from this value"),
		},
		"C4: engine error below a mapping that a Set wrote over a flow mapping moved into a flow mapping": {
			before: stringtest.LinesLF("o: {x: 1}", "t: {a: 1}"),
			applies: []edit.Func{both(
				both(move("$.o", "$.t.b"), at("$.t.b", set(map[string]int{"q": 1}))),
				at("$.t.b.q", set("x\x01y")),
			)},
			err: stringtest.JoinLF(
				"test.yaml:1:1: $.o: "+engine, report, "|-- v1 sets $.t.b from this value", "`-- v1 sets $.t.b.q",
			),
		},
		"C4: error that Doc.Node binds below a mapping that a later Apply set over a flow mapping moved into a flow mapping": {
			before:  stringtest.LinesLF("o: {x: 1}", "t: {a: 1}"),
			applies: []edit.Func{move("$.o", "$.t.b"), at("$.t.b", set(map[string]int{"q": 1}))},
			bind:    "$.t.b.q",
			bound:   stringtest.JoinLF("test.yaml:1:1: $.t.b.q: bad", "`-- v2 sets $.t.b from this value"),
		},
		"C4 control: caller error below a mapping that a Set wrote over a flow sequence moved into a block mapping": {
			before: stringtest.LinesLF("o: [1]", "t:", "  a: 1"),
			applies: []edit.Func{both(
				both(move("$.o", "$.t.b"), at("$.t.b", set(map[string]int{"q": 1}))),
				fail("$.t.b.q"),
			)},
			err: stringtest.JoinLF("test.yaml:1:1: $.t.b.q: bad", "`-- v1 sets $.t.b from this value"),
		},
		"C4 control: caller error below a mapping that a Set wrote over a flow mapping moved within a flow mapping": {
			before: stringtest.LinesLF("t: {o: {x: 1}, a: 1}"),
			applies: []edit.Func{both(
				both(move("$.t.o", "$.t.k"), at("$.t.k", set(map[string]int{"q": 1}))),
				fail("$.t.k.q"),
			)},
			err: stringtest.JoinLF("test.yaml:1:5: $.t.k.q: bad", "`-- v1 sets $.t.k from this value"),
		},
		"C4 control: caller error at a flow sequence moved into a flow mapping": {
			before:  stringtest.LinesLF("o: [1]", "t: {a: 1}"),
			applies: []edit.Func{both(move("$.o", "$.t.b"), fail("$.t.b"))},
			err:     "test.yaml:1:1: $.t.b: bad",
		},
		"B7: rename to a key that a Move wrote from a key that a Set created": {
			before: stringtest.LinesLF("o: 0", "m:", "  a: 1"),
			applies: []edit.Func{both(
				both(at("$.m.x", set(1)), move("$.m.x", "$.m.y")),
				rename("$.m.a", "y"),
			)},
			// The key that the Move wrote keeps the writer of the key that the
			// Set created (fix24 G24-1).
			err: stringtest.JoinLF(
				`test.yaml:3:3: $.m.a~: v1 renames "a" to "y", which already exists`,
				`|-- 2:1: $.m: "y" is defined here`,
				"|   `-- v1 sets $.m.x",
				"|-- remove one of the two",
				"`-- v1 renames $.m.a to \"y\"",
			),
		},
		"G23-1: engine error at a key of the user that a Move writes": {
			before:  stringtest.LinesLF("m:", "  k: 1"),
			applies: []edit.Func{move("$.m.k", `$.m.'x\u0001y'`)},
			err: stringtest.JoinLF(
				"test.yaml:2:3: $.m.k~: go-yaml's encoder writes U+0001 as it is, and YAML does not allow that in a text",
				"|-- report this to the authors of cafe",
				"`-- v1 moves $.m.k to $.m.'x\\u0001y'",
			),
		},
		"G23-1: engine error at a key of the user that a Move writes into a mapping that it creates": {
			before:  stringtest.LinesLF("o: 0", "m:", "  k: 1"),
			applies: []edit.Func{move("$.m.k", `$.n.'x\u0001y'`)},
			err: stringtest.JoinLF(
				"test.yaml:3:3: $.m.k~: go-yaml's encoder writes U+0001 as it is, and YAML does not allow that in a text",
				"|-- report this to the authors of cafe",
				"`-- v1 moves $.m.k to $.n.'x\\u0001y'",
			),
		},
		// The key of a moved empty value stands for the value, which the
		// user wrote, so a Set at its new place replaces that value (fix23
		// G23-4).
		"G23-4: engine error at an empty value that an earlier Apply moved": {
			before:  stringtest.LinesLF("a:", "b: 1"),
			applies: []edit.Func{move("$.a", "$.nn.p"), at("$.nn.p", set("x\x01y"))},
			err: stringtest.JoinLF(
				"test.yaml:1:1: $.a: go-yaml's encoder writes U+0001 as it is, and YAML does not allow that in a text",
				"|-- report this to the authors of cafe",
				"`-- v2 sets $.nn.p from this value",
			),
		},
		"G23-4 control: user error at an empty value that an earlier Apply moved": {
			before:  stringtest.LinesLF("a:", "b: 1"),
			applies: []edit.Func{move("$.a", "$.nn.p"), at("$.nn.p", set(dupMap))},
			err: stringtest.JoinLF(
				`test.yaml:1:1: $.a: v2 sets $.nn.p, but the value it writes holds two keys that a decode reads as "1"`,
				"|-- remove one of the two",
				"`-- v2 sets $.nn.p from this value",
			),
		},
		"G23-4: Set through a node at an empty value that an earlier Apply moved, after a removal": {
			// The node lost its place, so the problem binds where an error at
			// the place of the removal bound before it ran, at the key of a.
			before:  stringtest.LinesLF("a:", "b: 1"),
			applies: []edit.Func{move("$.a", "$.nn.p"), held("$.nn.p", at("$.nn.p", remove))},
			err: stringtest.JoinLF(
				"test.yaml:1:1: $.a: v2 sets $.nn.p through a node that lost its place to an earlier edit",
				"`-- v2 sets $.nn.p",
			),
		},
		// No token of the text is the place of a merge key, so a Set of one
		// below the root binds at the site of the mapping that holds it, as
		// a place that the text does not hold does (fix23 G23-5).
		"G23-5: engine error at a merge key written in place": {
			before:  stringtest.LinesLF("m:", "  <<: {a: 1}", "b: 1"),
			applies: []edit.Func{at("$.m.<<", set("x\x01y"))},
			err: stringtest.JoinLF(
				"test.yaml:1:1: $.m: go-yaml's encoder writes U+0001 as it is, and YAML does not allow that in a text",
				"|-- report this to the authors of cafe",
				"`-- v1 sets $.m.<<",
			),
		},
		"G23-5: engine error at a merge key that reads an alias": {
			before:  stringtest.LinesLF("s: &s {a: 1}", "m:", "  <<: *s", "  b: 2"),
			applies: []edit.Func{at("$.m.<<", set("x\x01y"))},
			err: stringtest.JoinLF(
				"test.yaml:2:1: $.m: go-yaml's encoder writes U+0001 as it is, and YAML does not allow that in a text",
				"|-- report this to the authors of cafe",
				"`-- v1 sets $.m.<<",
			),
		},
		"G23-5: engine error at a merge key of a flow mapping": {
			before:  stringtest.LinesLF("s: &s {a: 1}", "m: {<<: *s, b: 2}"),
			applies: []edit.Func{at("$.m.<<", set("x\x01y"))},
			err: stringtest.JoinLF(
				"test.yaml:2:1: $.m: go-yaml's encoder writes U+0001 as it is, and YAML does not allow that in a text",
				"|-- report this to the authors of cafe",
				"`-- v1 sets $.m.<<",
			),
		},
		"G23-5 control: user error at a merge key of a flow mapping": {
			before:  stringtest.LinesLF("s: &s {a: 1}", "m: {<<: *s, b: 2}"),
			applies: []edit.Func{at("$.m.<<", set(dupMap))},
			err: stringtest.JoinLF(
				`test.yaml:2:1: $.m: v1 sets $.m.<<, but the value it writes holds two keys that a decode reads as "1"`,
				"|-- remove one of the two",
				"`-- v1 sets $.m.<<",
			),
		},
		"G23-5 control: caller error at a flow mapping with a merge key": {
			before:  stringtest.LinesLF("s: &s {a: 1}", "m: {<<: *s, b: 2}"),
			applies: []edit.Func{fail("$.m")},
			err:     "test.yaml:2:1: $.m: bad",
		},
		"G23-5 control: user error at a merge key written in place": {
			before:  stringtest.LinesLF("m:", "  <<: {a: 1}", "b: 1"),
			applies: []edit.Func{at("$.m.<<", set(dupMap))},
			err: stringtest.JoinLF(
				`test.yaml:1:1: $.m: v1 sets $.m.<<, but the value it writes holds two keys that a decode reads as "1"`,
				"|-- remove one of the two",
				"`-- v1 sets $.m.<<",
			),
		},
		// A block mapping binds at its first key, which an earlier call
		// created here, but the mapping is the value of the user that the Set
		// replaces (gate23 note 3).
		"note 3: caller error at a mapping of the user whose first entry an earlier Apply created": {
			before: stringtest.LinesLF("t:", "  b: 1", "r2: 1"),
			applies: []edit.Func{
				both(at("$.t.b", remove), at("$.t.c", set(1))),
				both(at("$.t", set(7)), fail("$.t")),
			},
			err: stringtest.JoinLF(
				"test.yaml:1:1: $.t: bad",
				"`-- v2 sets $.t from this value",
			),
		},
		"note 3 control: engine error at a flow mapping of the user whose first entry an earlier Apply created": {
			before: stringtest.LinesLF("t: {b: 1}", "r2: 1"),
			applies: []edit.Func{
				both(at("$.t.b", remove), at("$.t.c", set(1))),
				at("$.t", set(1e6)),
			},
			err: stringtest.JoinLF(
				`test.yaml:1:1: $.t: go-yaml's encoder writes the float 1e+06 as text that go-yaml's decoder reads as "1e+06"`,
				"|-- report this to the authors of cafe",
				"`-- v2 sets $.t from this value",
			),
		},
		// The parser writes the null of a value that holds a tag alone, so an
		// error at the value binds at the first byte of the tag, the token
		// that the user wrote (gate23 note 4).
		"note 4: caller error at a value that holds a tag alone": {
			before:  "a: !!null\n",
			applies: []edit.Func{fail("$.a")},
			bind:    "$.a",
			err:     "test.yaml:1:4: $.a: bad",
			bound:   "test.yaml:1:4: $.a: bad",
		},
		"note 4: caller error at an item that holds a tag alone": {
			before:  stringtest.LinesLF("l:", "- !!str"),
			applies: []edit.Func{fail("$.l[0]")},
			bind:    "$.l[0]",
			err:     "test.yaml:2:3: $.l[0]: bad",
			bound:   "test.yaml:2:3: $.l[0]: bad",
		},
		"note 4: caller error at a value of a flow mapping that holds a tag alone": {
			before:  "m: {a: !!null , b: 1}\n",
			applies: []edit.Func{fail("$.m.a")},
			bind:    "$.m.a",
			err:     "test.yaml:1:8: $.m.a: bad",
			bound:   "test.yaml:1:8: $.m.a: bad",
		},
		"note 4: caller error at an item of a flow sequence that holds a tag alone": {
			before:  "l: [!!str , 1]\n",
			applies: []edit.Func{fail("$.l[0]")},
			bind:    "$.l[0]",
			err:     "test.yaml:1:5: $.l[0]: bad",
			bound:   "test.yaml:1:5: $.l[0]: bad",
		},
		"note 4: caller error at a root sequence whose item holds a tag alone": {
			before:  "- !!null\n",
			applies: []edit.Func{fail("$")},
			bind:    "$",
			err:     "test.yaml:1:3: $: bad",
			bound:   "test.yaml:1:3: $: bad",
		},
		"note 4 control: user error at a value that holds a tag alone": {
			before:  "a: !!null\n",
			applies: []edit.Func{at("$.a", set(dupMap))},
			err: stringtest.JoinLF(
				`test.yaml:1:4: $.a: v1 sets $.a, but the value it writes holds two keys that a decode reads as "1"`,
				"|-- remove one of the two",
				"`-- v1 sets $.a from this value",
			),
		},
		// The token of a value that the YAML of a marshaler writes under a tag
		// stands for the value that it replaces, so a problem at the value
		// binds where the user wrote the old value (fix24 G24-3).
		"G24-3: engine error at a tagged scalar that a marshaler wrote over a value of the user": {
			before:  stringtest.LinesLF("m:", "  a: 1", "  b: 2"),
			applies: []edit.Func{at("$.m.b", set(yamlMarshaler("!t x"))), at("$.m.b", set("x\x01y"))},
			err: stringtest.JoinLF(
				"test.yaml:3:6: $.m.b: "+engine,
				report,
				"|-- v1 sets $.m.b from this value",
				"`-- v2 sets $.m.b from this value",
			),
		},
		"G24-3: caller error at a tagged scalar that a marshaler wrote over a value of the user": {
			before:  stringtest.LinesLF("m:", "  a: 1", "  b: 2"),
			applies: []edit.Func{at("$.m.b", set(yamlMarshaler("!t x"))), fail("$.m.b")},
			bind:    "$.m.b",
			err:     stringtest.JoinLF("test.yaml:3:6: $.m.b: bad", "`-- v1 sets $.m.b from this value"),
			bound:   stringtest.JoinLF("test.yaml:3:6: $.m.b: bad", "`-- v1 sets $.m.b from this value"),
		},
		"G24-3: caller error at an item that a marshaler wrote as a tag alone": {
			before:  stringtest.LinesLF("l:", "- 1", "- 2"),
			applies: []edit.Func{at("$.l[1]", set(yamlMarshaler("!!null\n"))), fail("$.l[1]")},
			err:     stringtest.JoinLF("test.yaml:3:3: $.l[1]: bad", "`-- v1 sets $.l[1] from this value"),
		},
		"G24-3: caller error at a tagged mapping that a marshaler wrote over an item of a flow sequence": {
			before:  stringtest.LinesLF("l: [0, 1]", "b: 1"),
			applies: []edit.Func{at("$.l[0]", set(yamlMarshaler("!t {q: 1}\n"))), fail("$.l[0]")},
			err:     stringtest.JoinLF("test.yaml:1:5: $.l[0]: bad", "`-- v1 sets $.l[0] from this value"),
		},
		"G24-3: engine error at a tagged mapping that a marshaler wrote over an item of a flow sequence and a Move took": {
			before: stringtest.LinesLF("l: [0, 1]", "b: 1"),
			applies: []edit.Func{
				at("$.l[0]", set(yamlMarshaler("!t {q: 1}\n"))),
				move("$.l[0]", "$.nn"),
				at("$.nn", set("x\x01y")),
			},
			err: stringtest.JoinLF(
				"test.yaml:1:5: $.l[0]: "+engine,
				report,
				"|-- v1 sets $.l[0] from this value",
				"`-- v3 sets $.nn from this value",
			),
		},
		"G24-3 control: caller error at a scalar that a marshaler wrote without a tag": {
			before:  stringtest.LinesLF("m:", "  a: 1", "  b: 2"),
			applies: []edit.Func{at("$.m.b", set(yamlMarshaler("x"))), fail("$.m.b")},
			err:     stringtest.JoinLF("test.yaml:3:6: $.m.b: bad", "`-- v1 sets $.m.b from this value"),
		},
		// No path selects a << merge key, so a caller error at a path that
		// names one binds at the site of the mapping that holds it, as the
		// problems of the edit at that path do (fix24 R24-1).
		"R24-1: caller error at a path that names a merge key of an alias": {
			before:  stringtest.LinesLF("s: &s {a: 1}", "m:", "  <<: *s", "  b: 2"),
			applies: []edit.Func{fail("$.m.<<")},
			bind:    "$.m.<<",
			err:     "test.yaml:2:1: $.m.<<: bad",
			bound:   "test.yaml:2:1: $.m.<<: bad",
		},
		"R24-1: caller error at a path that names a merge key of a mapping": {
			before:  stringtest.LinesLF("m:", "  <<: {a: 1}", "b: 1"),
			applies: []edit.Func{fail("$.m.<<")},
			err:     "test.yaml:1:1: $.m.<<: bad",
		},
		"R24-1 control: engine error at a path that names a merge key": {
			before:  stringtest.LinesLF("s: &s {a: 1}", "m:", "  <<: *s", "  b: 2"),
			applies: []edit.Func{at("$.m.<<", set("x\x01y"))},
			err:     stringtest.JoinLF("test.yaml:2:1: $.m: "+engine, report, "`-- v1 sets $.m.<<"),
		},
		// The key that a move writes for an item of a flow sequence stands for
		// the item. Its site names the item, and its writer is the Set that
		// wrote the item, as before the move (fix24 R24-2, R24-6).
		"R24-2: engine error below a pair without braces that a Set replaced and a Move took": {
			before: "l: [a: 0]\nb: 1\n",
			applies: []edit.Func{
				at("$.l[0]", set(map[string]int{"q": 1})),
				move("$.l[0]", "$.nn"),
				at("$.nn.zz", set("x\x01y")),
			},
			err: stringtest.JoinLF(
				"test.yaml:1:5: $.l[0]: "+engine,
				report,
				"|-- v1 sets $.l[0] from this value",
				"`-- v3 sets $.nn.zz",
			),
		},
		"R24-2 control: engine error below a pair without braces that a Set replaced": {
			before:  "l: [a: 0]\nb: 1\n",
			applies: []edit.Func{at("$.l[0]", set(map[string]int{"q": 1})), at("$.l[0].zz", set("x\x01y"))},
			err: stringtest.JoinLF(
				"test.yaml:1:5: $.l[0]: "+engine,
				report,
				"|-- v1 sets $.l[0] from this value",
				"`-- v2 sets $.l[0].zz",
			),
		},
		"R24-6: engine error at an item of a flow sequence that a Set replaced and a Move took": {
			before: stringtest.LinesLF("l: [0, 1]", "b: 1"),
			applies: []edit.Func{
				at("$.l[0]", set(map[string]int{"q": 1})),
				move("$.l[0]", "$.nn"),
				at("$.nn", set("x\x01y")),
			},
			err: stringtest.JoinLF(
				"test.yaml:1:5: $.l[0]: "+engine,
				report,
				"|-- v1 sets $.l[0] from this value",
				"`-- v3 sets $.nn from this value",
			),
		},
		"R24-6: user error at an item of a flow sequence that a Set replaced and a Move took": {
			before: stringtest.LinesLF("l: [0, 1]", "b: 1"),
			applies: []edit.Func{
				at("$.l[0]", set(map[string]int{"q": 1})),
				move("$.l[0]", "$.nn"),
				at("$.nn", set(dupMap)),
			},
			err: stringtest.JoinLF(
				"test.yaml:1:5: $.l[0]: v3 sets $.nn, but "+twoKeys,
				"|-- remove one of the two",
				"|-- v1 sets $.l[0] from this value",
				"`-- v3 sets $.nn from this value",
			),
		},
		"R24-6: caller error at a mapping that a Set wrote in a flow entry and a Move took": {
			// The key that the move writes stands for the key of the user, so
			// no call wrote its token, as before the move.
			before: "m: {a: 0, b: 1}\n",
			applies: []edit.Func{
				at("$.m.a", set(map[string]int{"q": 1})),
				move("$.m.a", "$.nn"),
				fail("$.nn"),
			},
			err: "test.yaml:1:5: $.nn: bad",
		},
		"R24-6 control: caller error at an item of a flow sequence that a Set replaced and a Move took": {
			before: stringtest.LinesLF("l: [0, 1]", "b: 1"),
			applies: []edit.Func{
				at("$.l[0]", set(map[string]int{"q": 1})),
				move("$.l[0]", "$.nn"),
				fail("$.nn"),
			},
			err: stringtest.JoinLF("test.yaml:1:5: $.nn: bad", "`-- v1 sets $.l[0] from this value"),
		},
		// A read in the MarshalYAML method of the value of a Set runs inside
		// the Set, and the problems of the Set name the Set after the read
		// (fix24 R24-5). Both rows are controls, because they pass at FIX23.
		// They guard against the regression of d1b8d652, where the read ended
		// the Set.
		"R24-5 control: engine error of a Set whose marshaler reads another node": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			applies: []edit.Func{func(ctx context.Context, n *edit.Node) error {
				return n.At(paths.MustParse("$.a")).Set(ctx, readingMarshaler{
					node: n.At(paths.MustParse("$.b")), v: "x\x01y",
				})
			}},
			err: stringtest.JoinLF("test.yaml:1:4: $.a: "+engine, report, "`-- v1 sets $.a from this value"),
		},
		"R24-5 control: user error of a Set whose marshaler reads another node": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			applies: []edit.Func{func(ctx context.Context, n *edit.Node) error {
				return n.At(paths.MustParse("$.a")).Set(ctx, readingMarshaler{
					node: n.At(paths.MustParse("$.b")), v: dupMap,
				})
			}},
			err: stringtest.JoinLF(
				"test.yaml:1:4: $.a: v1 sets $.a, but "+twoKeys,
				"|-- remove one of the two",
				"`-- v1 sets $.a from this value",
			),
		},
		// Every problem of a call at a place binds at the site of the place in
		// the current text, whatever the kind of the problem. A value that an
		// edit created has no token of the user. So it binds at the site of
		// the nearest value above it whose token the user wrote and the edits
		// kept, also after a move took it elsewhere (fix25 P78, GT2-1).
		"P78: two keys at a value that an edit created and a Move took": {
			before: stringtest.LinesLF("o: 0", "m:", "  z: 1"),
			applies: []edit.Func{
				both(at("$.n.p", set(1)), move("$.n", "$.m.k")),
				at("$.m.k.p", set(dupMap)),
			},
			err: stringtest.JoinLF(
				"test.yaml:2:1: $.m: v2 sets $.m.k.p, but "+twoKeys,
				"|-- remove one of the two",
				"|-- v1 sets $.n.p",
				"`-- v2 sets $.m.k.p",
			),
		},
		"P78 control: engine error at a value that an edit created and a Move took": {
			before: stringtest.LinesLF("o: 0", "m:", "  z: 1"),
			applies: []edit.Func{
				both(at("$.n.p", set(1)), move("$.n", "$.m.k")),
				at("$.m.k.p", set("x\x01y")),
			},
			err: stringtest.JoinLF("test.yaml:2:1: $.m: "+engine, report, "|-- v1 sets $.n.p", "`-- v2 sets $.m.k.p"),
		},
		"P78 control: caller error at a value that an edit created and a Move took": {
			before: stringtest.LinesLF("o: 0", "m:", "  z: 1"),
			applies: []edit.Func{
				both(at("$.n.p", set(1)), move("$.n", "$.m.k")),
				fail("$.m.k.p"),
			},
			err: stringtest.JoinLF("test.yaml:2:1: $.m.k.p: bad", "`-- v1 sets $.n.p"),
		},
		"P78: two keys at an item that an edit created and a Move took": {
			before: stringtest.LinesLF("o: 0", "l:", "- 1"),
			applies: []edit.Func{
				both(at("$.n", set(1)), move("$.n", "$.l[1]")),
				at("$.l[1]", set(dupMap)),
			},
			err: stringtest.JoinLF(
				"test.yaml:2:1: $.l: v2 sets $.l[1], but "+twoKeys,
				"|-- remove one of the two",
				"|-- v1 sets $.n",
				"`-- v2 sets $.l[1]",
			),
		},
		"P78: two keys at a value that a Set created inside the mapping that it wrote over a scalar": {
			before: stringtest.LinesLF("a: 0", "b: 1"),
			applies: []edit.Func{
				at("$.a", set(map[string]any{"x": map[string]int{"y": 1}})),
				at("$.a.x.y", set(dupMap)),
			},
			err: stringtest.JoinLF(
				"test.yaml:1:1: $.a: v2 sets $.a.x.y, but "+twoKeys,
				"|-- remove one of the two",
				"|-- v1 sets $.a from this value",
				"`-- v2 sets $.a.x.y",
			),
		},
		"P78 control: caller error at a value that a Set created inside the mapping that it wrote over a scalar": {
			before: stringtest.LinesLF("a: 0", "b: 1"),
			applies: []edit.Func{
				at("$.a", set(map[string]any{"x": map[string]int{"y": 1}})),
				fail("$.a.x.y"),
			},
			err: stringtest.JoinLF("test.yaml:1:1: $.a.x.y: bad", "`-- v1 sets $.a from this value"),
		},
		"P78: rename to a key that the mapping holds, at a key that an edit created and a Move took": {
			before: stringtest.LinesLF("o: 0", "m:", "  z: 1"),
			applies: []edit.Func{
				both(at("$.n", set(map[string]int{"p": 1, "q": 2})), move("$.n", "$.m.k")),
				rename("$.m.k.p", "q"),
			},
			err: stringtest.JoinLF(
				`test.yaml:2:1: $.m: v2 renames "p" to "q", which already exists`,
				`|-- 2:1: $.m: "q" is defined here`,
				"|   `-- v1 sets $.n",
				"|-- remove one of the two",
				"|-- v1 sets $.n",
				"`-- v2 renames $.m.k.p to \"q\"",
			),
		},
		"P78 control: move to a key that the mapping holds, at a key that an edit created and a Move took": {
			before: stringtest.LinesLF("o: 0", "m:", "  z: 1"),
			applies: []edit.Func{
				both(at("$.n", set(map[string]int{"p": 1, "q": 2})), move("$.n", "$.m.k")),
				move("$.m.k.p", "$.m.k.q"),
			},
			err: stringtest.JoinLF(
				`test.yaml:2:1: $.m: v2 moves "p" to $.m.k.q, which already exists`,
				"|-- v1 sets $.n",
				"`-- v2 moves $.m.k.p to $.m.k.q",
			),
		},
		"P78: engine error of a rename at a key that a Set created inside the mapping that it wrote over a scalar": {
			before: stringtest.LinesLF("a: 0", "b: 1"),
			applies: []edit.Func{
				at("$.a", set(map[string]int{"w": 1, "x": 2})),
				rename("$.a.w", "x\x01y"),
			},
			err: stringtest.JoinLF(
				"test.yaml:1:1: $.a: "+engine,
				report,
				"|-- v1 sets $.a from this value",
				"`-- v2 renames $.a.w to \"x\\x01y\"",
			),
		},
		// The key that a move writes for an item of a flow sequence keeps the
		// writer of the token of the item. A problem at the key itself names
		// that writer too (fix25 P79, GT2-2).
		"P79: rename to a key that the mapping holds, at a key that a Move wrote for an item that a Set wrote": {
			before: stringtest.LinesLF("l: [0, 1]", "b: 2"),
			applies: []edit.Func{
				at("$.l[0]", set(map[string]int{"q": 1})),
				both(move("$.l[0]", "$.n"), rename("$.n", "b")),
			},
			err: stringtest.JoinLF(
				`test.yaml:1:5: $.l[0]: v2 renames "n" to "b", which already exists`,
				`|-- 2:1: $.b~: "b" is defined here`,
				"|-- remove one of the two",
				"|-- v1 sets $.l[0] from this value",
				"`-- v2 renames $.n to \"b\"",
			),
		},
		"P79: move to a key that the mapping holds, at a key that a Move wrote for an item that a Set wrote": {
			before: stringtest.LinesLF("l: [0, 1]", "b: 2"),
			applies: []edit.Func{
				at("$.l[0]", set(5)),
				both(move("$.l[0]", "$.n"), move("$.n", "$.b")),
			},
			err: stringtest.JoinLF(
				`test.yaml:1:5: $.l[0]: v2 moves "n" to $.b, which already exists`,
				"|-- v1 sets $.l[0] from this value",
				"`-- v2 moves $.n to $.b",
			),
		},
		"P79: repeated key at a key that a Move wrote for an item that a Set wrote": {
			before: stringtest.LinesLF("l: [0, 1]", "b: 2"),
			dup:    true,
			applies: []edit.Func{
				at("$.l[0]", set(map[string]int{"q": 1})),
				both(move("$.l[0]", "$.n"), rename("$.n", "b")),
			},
			err: stringtest.JoinLF(
				`test.yaml:1:5: $.l[0]: v2 renames $.n to "b", which writes the key "b" a second time in its mapping, `+
					"and cafe does not write a key that its mapping repeats",
				`|-- 2:1: $.b: the key "b" is written here`,
				"|-- rename the key to one that its mapping does not hold",
				"|-- v1 sets $.l[0] from this value",
				"`-- v2 renames $.n to \"b\"",
			),
		},
		"P79 control: rename to a key that the mapping holds, at a key that a Move wrote for an item of the user": {
			before:  stringtest.LinesLF("l: [0, 1]", "b: 2"),
			applies: []edit.Func{both(move("$.l[0]", "$.n"), rename("$.n", "b"))},
			err: stringtest.JoinLF(
				`test.yaml:1:5: $.l[0]: v1 renames "n" to "b", which already exists`,
				`|-- 2:1: $.b~: "b" is defined here`,
				"|-- remove one of the two",
				"`-- v1 renames $.n to \"b\"",
			),
		},
		// A decode of the user's text that fails while a call runs names the
		// call, as every other problem of the call does (fix25 P79).
		"P79: decode error of the user's text while a Set runs": {
			before:  stringtest.LinesLF("a: !!bool x", "b: 1"),
			applies: []edit.Func{at("$.b", set(2))},
			err:     stringtest.JoinLF(`test.yaml:1:11: $.a: cannot convert "x" to boolean`, "`-- v1 sets $.b"),
		},
		"P79 control: decode error of the user's text after the last call": {
			before:  stringtest.LinesLF("a: !!bool x", "b: 1"),
			applies: []edit.Func{func(context.Context, *edit.Node) error { return nil }},
			err:     `test.yaml:1:11: $.a: cannot convert "x" to boolean`,
		},
		// The calls of a read name it too (P74).
		"read of a missing key": {
			before:  stringtest.LinesLF("a: 1", "b: 2"),
			applies: []edit.Func{decode("$.zz")},
			err: stringtest.JoinLF(
				"test.yaml:1:1: $: v1 cannot decode $.zz, which holds no value",
				"`-- v1 reads $.zz",
			),
		},
		"control: caller error after the last call": {
			// No call runs when the Func returns, so the problem names no call.
			before:  stringtest.LinesLF("a: 1", "b: 2"),
			applies: []edit.Func{both(at("$.a", set(5)), fail("$.b"))},
			err:     "test.yaml:2:4: $.b: bad",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var opts []niceyaml.SourceOption

			if tc.ref != "" {
				opts = append(opts, niceyaml.WithReferences(niceyaml.NewSourceFromString(tc.ref)))
			}

			if tc.dup {
				opts = append(opts, niceyaml.WithAllowDuplicateKeys(true))
			}

			d := newDoc(t, tc.before, opts...)

			apply := paths.Doc()
			if tc.apply != "" {
				apply = paths.MustParse(tc.apply)
			}

			var err error

			for i, fn := range tc.applies {
				err = d.Apply(t.Context(), apply, fn, edit.WithLabel(fmt.Sprintf("v%d", i+1)))
				if i < len(tc.applies)-1 {
					require.NoError(t, err)
				}
			}

			if tc.err == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Equal(t, tc.err, firstLines(err))
			}

			if tc.bind != "" {
				bound := d.Node().Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.MustParse(tc.bind))))
				assert.Equal(t, tc.bound, firstLines(bound))
			}
		})
	}
}

// readingMarshaler marshals v after it decodes node, as a value whose
// MarshalYAML method reads the document.
type readingMarshaler struct {
	node *edit.Node
	v    any
}

// MarshalYAML decodes the node of m and returns the value of m.
func (m readingMarshaler) MarshalYAML() (any, error) {
	_, err := m.node.Decode[any](context.Background())
	if err != nil {
		return nil, err
	}

	return m.v, nil
}

// firstLines returns the first line of the formatted err with its details,
// without the excerpts of the text.
func firstLines(err error) string {
	var lines []string

	for line := range strings.SplitSeq(niceyaml.FormatError(err), "\n") {
		if line == "" || strings.HasPrefix(line, " ") {
			continue
		}

		lines = append(lines, line)
	}

	return strings.Join(lines, "\n")
}

func TestFileText(t *testing.T) {
	t.Parallel()

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
		"documents with comment lines before a header": {
			// The comment lines belong to the document before the header.
			docs: func(t *testing.T) []*edit.Doc {
				t.Helper()

				return edited(t, niceyaml.NewSourceFromString(stringtest.LinesLF(
					"a:", "  - 1", "# c", "---", "b: 2", "# d", "", "# e", "---", "c: 3",
				)), 0, 1, 2)
			},
			want: stringtest.LinesLF("a: 20", "# c", "---", "b: 20", "# d", "", "# e", "---", "c: 20"),
		},
		"documents out of order": {
			docs: func(t *testing.T) []*edit.Doc {
				t.Helper()

				return edited(t, niceyaml.NewSourceFromString(text), 2, 0)
			},
			want: stringtest.LinesLF("a: 20 # first", "---", "b: 2 # second", "---", "c: 20 # third"),
		},
		"last document emptied without a final newline": {
			docs: func(t *testing.T) []*edit.Doc {
				t.Helper()

				docs := niceyaml.NewSourceFromString(stringtest.JoinLF("a: 0", "---", "m: 1")).Documents()
				require.Len(t, docs, 2)

				d, err := edit.New(docs[1])
				require.NoError(t, err)

				remove := func(_ context.Context, n *edit.Node) error {
					return n.Remove()
				}
				require.NoError(t, d.Apply(t.Context(), paths.Doc().Child("m"), remove))

				return []*edit.Doc{d}
			},
			want: stringtest.JoinLF("a: 0", "---", "{}"),
		},
		"byte order mark of a later document that no Doc edits": {
			// New refuses the document with the mark inside it, so FileText
			// writes that document as the user wrote it.
			docs: func(t *testing.T) []*edit.Doc {
				t.Helper()

				src := niceyaml.NewSourceFromString(stringtest.LinesLF(
					"a: 1", "k: 1", "---", "b: 2", "---", "\ufeffc: 3",
				))

				docs := src.Documents()
				require.Len(t, docs, 3)

				_, err := edit.New(docs[2])
				require.Error(t, err)
				assert.True(t, niceyaml.IsInvalid(err))

				out := make([]*edit.Doc, 0, len(docs))

				for _, doc := range docs[:2] {
					d, err := edit.New(doc)
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
			},
			want: stringtest.LinesLF("a: 20", "k: 20", "---", "b: 20", "---", "\ufeffc: 3"),
		},
		"no doc": {
			docs: func(*testing.T) []*edit.Doc { return nil },
			err:  "FileText takes at least one Doc",
		},
		"two docs of one document": {
			docs: func(t *testing.T) []*edit.Doc {
				t.Helper()

				src := niceyaml.NewSourceFromString(text)

				return slices.Concat(edited(t, src, 1), edited(t, src, 1))
			},
			err: "FileText takes each document once, not the document at line 2 twice",
		},
		"docs of two sources": {
			docs: func(t *testing.T) []*edit.Doc {
				t.Helper()

				return slices.Concat(
					edited(t, niceyaml.NewSourceFromString(text), 0),
					edited(t, niceyaml.NewSourceFromString(text), 1),
				)
			},
			err: "FileText takes Docs of one Source, not Docs of two Sources",
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

	tcs := map[string]struct {
		text string
		// The index of the document in text.
		doc    int
		before string
		want   string
		// A line of the error of New, or of the Apply, which matches
		// edit.ErrEngine, or "".
		newErr, err string
	}{
		"later document": {
			text:   stringtest.LinesLF("a: 1 # first", "---", "a: 2 # second"),
			doc:    1,
			before: stringtest.LinesLF("---", "a: 2 # second"),
			want:   stringtest.LinesLF("---", "a: 20 # second"),
		},
		"byte order mark before the header of the next document": {
			// The mark belongs to the line of the header, so the edit writes
			// after the last line of the document.
			text:   stringtest.LinesLF("a: 1", "\ufeff---", "a: 2"),
			before: "a: 1\n",
			want:   "a: 20\n",
		},
		"byte order mark after a comment line of a later document": {
			// The tokenizer drops the mark, and the text keeps it before the line
			// of the key, which the Set rewrites.
			text:   stringtest.LinesLF("a: 1", "...", "# c", "\ufeffa: 2"),
			doc:    1,
			before: stringtest.LinesLF("# c", "\ufeffa: 2"),
			want:   stringtest.LinesLF("# c", "\ufeffa: 20"),
		},
		"byte order mark after the end of a document": {
			// The mark lies in the prefix of the next document, after the
			// comment line that the first document holds.
			text:   stringtest.LinesLF("a: 1", "...", "\ufeff# c", "---", "a: 2"),
			before: stringtest.LinesLF("a: 1", "...", "\ufeff# c"),
			want:   stringtest.LinesLF("a: 20", "...", "\ufeff# c"),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			docs := niceyaml.NewSourceFromString(tc.text).Documents()
			require.Greater(t, len(docs), tc.doc)

			d, err := edit.New(docs[tc.doc])
			if tc.newErr != "" {
				require.ErrorIs(t, err, edit.ErrEngine)
				assert.Contains(t, niceyaml.FormatError(err), tc.newErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.before, d.Text())

			err = d.Apply(t.Context(), paths.Doc().Child("a"), func(ctx context.Context, n *edit.Node) error {
				return n.Set(ctx, 20)
			})
			if tc.err != "" {
				require.ErrorIs(t, err, edit.ErrEngine)
				assert.Contains(t, niceyaml.FormatError(err), tc.err)
				assert.Equal(t, tc.before, d.Text())

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, d.Text())
		})
	}
}

func TestDoc_Edited(t *testing.T) {
	t.Parallel()

	a := paths.Doc().Child("a")

	set := func(v any) edit.Func {
		return func(ctx context.Context, n *edit.Node) error { return n.Set(ctx, v) }
	}

	tcs := map[string]struct {
		fns  []edit.Func
		want bool
	}{
		"no Apply": {},
		"set of another value": {
			fns:  []edit.Func{set(2)},
			want: true,
		},
		"set of the same value": {
			fns: []edit.Func{set(1)},
		},
		"set of another value and back in one Apply": {
			fns: []edit.Func{func(ctx context.Context, n *edit.Node) error {
				err := n.Set(ctx, 2)
				if err != nil {
					return err
				}

				return n.Set(ctx, 1)
			}},
		},
		"set of another value and back in two Applies": {
			fns: []edit.Func{set(2), set(1)},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, "a: 1\n")

			for _, fn := range tc.fns {
				require.NoError(t, d.Apply(t.Context(), a, fn))
			}

			assert.Equal(t, tc.want, d.Edited())
		})
	}
}

func TestDoc_Text_DuringApply(t *testing.T) {
	t.Parallel()

	a := paths.Doc().Child("a")

	// Each read runs on the Doc between two Sets, so it reads the text that
	// the first Set wrote: a block scalar whose line of one space keeps its
	// space past the indent of the scalar.
	tcs := map[string]struct {
		read func(t *testing.T, ctx context.Context, d *edit.Doc) any
		// The value of the read.
		want any
	}{
		"text of the document": {
			read: func(_ *testing.T, _ context.Context, d *edit.Doc) any { return d.Text() },
			want: stringtest.LinesLF("a: |", "  a", "   ", "  b"),
		},
		"text of the file": {
			read: func(t *testing.T, _ context.Context, d *edit.Doc) any {
				t.Helper()

				text, err := edit.FileText(d)
				require.NoError(t, err)

				return text
			},
			want: stringtest.LinesLF("a: |", "  a", "   ", "  b"),
		},
		"decode of the document": {
			read: func(t *testing.T, ctx context.Context, d *edit.Doc) any {
				t.Helper()

				v, err := d.Node().Decode[map[string]string](ctx)
				require.NoError(t, err)

				return v["a"]
			},
			want: "a\n \nb\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, "a: 1")

			err := d.Apply(t.Context(), paths.Doc(), func(ctx context.Context, root *edit.Node) error {
				n := root.At(a)

				err := n.Set(ctx, "a\n \nb\n")
				if err != nil {
					return err
				}

				assert.Equal(t, tc.want, tc.read(t, ctx, d))

				return n.Set(ctx, "q")
			})
			require.NoError(t, err)
			assert.Equal(t, "a: q\n", d.Text())
		})
	}
}

func TestDoc_Text_Differs(t *testing.T) {
	t.Parallel()

	// Each read runs on the Doc after a move that would put *y inside the
	// value of its anchor &y. The move fails with ConstructRecursiveAlias
	// and leaves the text as it found it, which the read returns.
	before := stringtest.LinesLF("a: &y {c: &k !t 1}", "b: &x {a: *y, e: 1}", "c: !t []")

	tcs := map[string]struct {
		read func(t *testing.T, d *edit.Doc) any
		// The value of the read.
		want any
	}{
		"text of the document": {
			read: func(_ *testing.T, d *edit.Doc) any { return d.Text() },
			want: before,
		},
		"text of the file": {
			read: func(t *testing.T, d *edit.Doc) any {
				t.Helper()

				text, err := edit.FileText(d)
				require.NoError(t, err)

				return text
			},
			want: before,
		},
		"node of the document": {
			read: func(_ *testing.T, d *edit.Doc) any { return d.Node().Path().String() },
			want: "$",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, before)

			err := d.Apply(t.Context(), paths.Doc(), func(_ context.Context, root *edit.Node) error {
				err := root.At(paths.Doc().Child("b")).Move(paths.MustParse("$.a.q"))

				assert.Equal(t, tc.want, tc.read(t, d))

				return err
			})
			require.ErrorIs(t, err, edit.ErrUnsupported)
			require.NotErrorIs(t, err, edit.ErrEngine)
			assert.Contains(t, niceyaml.FormatError(err), "1:4: $.a: the edit moves $.b to $.a.q, which makes a "+
				"document with an alias inside the value of the anchor that it reads, such as *y")
			assert.Equal(t, before, d.Text())
		})
	}
}

func TestDoc_Removals(t *testing.T) {
	t.Parallel()

	d := newDoc(t, stringtest.LinesLF(
		"spec:",
		"  color: red",
		"  tags: [a, b]",
		"  seats: 4",
		"  menu:",
		"    - item: kaffe",
		"      cost: 35",
		"    - item: te",
		"      cost: 30",
		"    - - bulle",
		"      - kaka",
	))

	require.NoError(t, d.Apply(t.Context(), spec.Child("color"), func(_ context.Context, n *edit.Node) error {
		return n.Remove(edit.WithReason("menu colors moved to the theme"))
	}, label))

	remove := func(_ context.Context, n *edit.Node) error { return n.Remove() }

	require.NoError(t, d.Apply(t.Context(), spec.Child("tags").Index(1), remove))

	// The second removal at $.spec.menu[1] takes the item that the user's
	// text holds at $.spec.menu[2].
	for range 2 {
		require.NoError(t, d.Apply(t.Context(), spec.Child("menu").Index(1), remove))
	}

	assert.True(t, d.Edited())
	assert.Equal(t, stringtest.LinesLF(
		"spec:",
		"  tags: [a]",
		"  seats: 4",
		"  menu:",
		"    - item: kaffe",
		"      cost: 35",
	), d.Text())

	got := make([]string, 0, len(d.Removals()))
	for _, rm := range d.Removals() {
		got = append(got, removalString(rm))
	}

	assert.Equal(t, []string{
		`$.spec.color~ "cafe.jacobcolvin.com/v1" "menu colors moved to the theme" key="color" item=false 1:2-1:7`,
		`$.spec.tags[1] "" "" key="" item=true 2:12-2:13`,
		`$.spec.menu[1] "" "" key="" item=true 7:4-8:14`,
		`$.spec.menu[2] "" "" key="" item=true 9:4-10:12`,
	}, got)
}

func TestDoc_Removals_Items(t *testing.T) {
	t.Parallel()

	// The Func that moveTo returns moves the value at from to the path to.
	moveTo := func(from, to string) edit.Func {
		return func(_ context.Context, n *edit.Node) error {
			return n.At(paths.MustParse(from)).Move(paths.MustParse(to))
		}
	}

	// The Func that removeAt returns removes the value at path.
	removeAt := func(path string) edit.Func {
		return func(_ context.Context, n *edit.Node) error {
			return n.At(paths.MustParse(path)).Remove()
		}
	}

	tcs := map[string]struct {
		before string
		// The Func of an Apply at the root that runs before the removal,
		// when set.
		earlier edit.Func
		path    paths.Path
		// The removal of the item at path, as Doc.Removals lists it last.
		want string
		// The error of the earlier Apply, when it fails. The row then checks
		// nothing else.
		err string
	}{
		"item of a block sequence": {
			before: stringtest.LinesLF("l:", "  - 1", "  - 2"),
			path:   paths.MustParse("$.l[1]"),
			want:   `$.l[1] "cafe.jacobcolvin.com/v1" "" key="" item=true 2:2-2:5`,
		},
		"item that holds a block sequence": {
			before: stringtest.LinesLF("l:", "  - - 1", "    - 2"),
			path:   paths.MustParse("$.l[0]"),
			want:   `$.l[0] "cafe.jacobcolvin.com/v1" "" key="" item=true 1:2-2:7`,
		},
		"first item of a block sequence on the line of the item that holds it": {
			before: stringtest.LinesLF("l:", "  - - 1", "    - 2"),
			path:   paths.MustParse("$.l[0][0]"),
			want:   `$.l[0][0] "cafe.jacobcolvin.com/v1" "" key="" item=true 1:4-1:7`,
		},
		"first item of a block sequence below the item that holds it": {
			before: stringtest.LinesLF("l:", "  - # c", "    - 1"),
			path:   paths.MustParse("$.l[0][0]"),
			want:   `$.l[0][0] "cafe.jacobcolvin.com/v1" "" key="" item=true 2:4-2:7`,
		},
		"first item of a block sequence two levels down at the root": {
			before: stringtest.LinesLF("- - - 1"),
			path:   paths.MustParse("$[0][0][0]"),
			want:   `$[0][0][0] "cafe.jacobcolvin.com/v1" "" key="" item=true 0:4-0:7`,
		},
		"item of a flow sequence": {
			before: stringtest.LinesLF("l:", "  - [1, 2]"),
			path:   paths.MustParse("$.l[0][1]"),
			want:   `$.l[0][1] "cafe.jacobcolvin.com/v1" "" key="" item=true 1:8-1:9`,
		},
		"item of a block sequence that ends in multi-byte characters": {
			before: stringtest.LinesLF("l:", "  - 日本", "  - 2"),
			path:   paths.MustParse("$.l[0]"),
			want:   `$.l[0] "cafe.jacobcolvin.com/v1" "" key="" item=true 1:2-1:6`,
		},
		"mapping item of a block sequence that ends in a multi-byte character": {
			before: stringtest.LinesLF("l:", "  - a: é"),
			path:   paths.MustParse("$.l[0]"),
			want:   `$.l[0] "cafe.jacobcolvin.com/v1" "" key="" item=true 1:2-1:8`,
		},
		"item of a flow sequence that ends in multi-byte characters": {
			before: stringtest.LinesLF("f: [日本, 2]"),
			path:   paths.MustParse("$.f[0]"),
			want:   `$.f[0] "cafe.jacobcolvin.com/v1" "" key="" item=true 0:4-0:6`,
		},
		"mapping item of a flow sequence without braces that ends in multi-byte characters": {
			before: stringtest.LinesLF("f: [a: 日本, 2]"),
			path:   paths.MustParse("$.f[0]"),
			want:   `$.f[0] "cafe.jacobcolvin.com/v1" "" key="" item=true 0:4-0:9`,
		},
		"mapping item of a block sequence that a move writes in a flow mapping": {
			before:  stringtest.LinesLF("f:", "  - 0", "  - a: 1", "k: {z: 0}"),
			earlier: moveTo("$.f", "$.k.np"),
			path:    paths.MustParse("$.k.np[1]"),
			err:     "test.yaml:2:3: $.f: cafe.jacobcolvin.com/v1 moves $.f to $.k.np, which puts a block value inside a flow collection, and cafe does not move a block value into a flow collection",
		},
		"mapping item of a block sequence that a move writes in a flow sequence": {
			before:  stringtest.LinesLF("f:", "  - 0", "  - a: 1", "g: [5]"),
			earlier: moveTo("$.f", "$.g[1]"),
			path:    paths.MustParse("$.g[1][1]"),
			err:     "test.yaml:2:3: $.f: cafe.jacobcolvin.com/v1 moves $.f to $.g[1], which puts a block value inside a flow collection, and cafe does not move a block value into a flow collection",
		},
		"flow sequence item of a block sequence that a move writes in a flow mapping": {
			before:  stringtest.LinesLF("f:", "  - 0", "  - [1]", "k: {z: 0}"),
			earlier: moveTo("$.f", "$.k.np"),
			path:    paths.MustParse("$.k.np[1]"),
			err:     "test.yaml:2:3: $.f: cafe.jacobcolvin.com/v1 moves $.f to $.k.np, which puts a block value inside a flow collection, and cafe does not move a block value into a flow collection",
		},
		"block sequence item of a block sequence that a move writes in a flow mapping": {
			before:  stringtest.LinesLF("f:", "  - 0", "  - - 1", "k: {z: 0}"),
			earlier: moveTo("$.f", "$.k.np"),
			path:    paths.MustParse("$.k.np[1]"),
			err:     "test.yaml:2:3: $.f: cafe.jacobcolvin.com/v1 moves $.f to $.k.np, which puts a block value inside a flow collection, and cafe does not move a block value into a flow collection",
		},
		"empty item of a block sequence that a move writes in a flow mapping": {
			before:  stringtest.LinesLF("f:", "  - 0", "  -", "  - 2", "k: {z: 0}"),
			earlier: moveTo("$.f", "$.k.np"),
			path:    paths.MustParse("$.k.np[1]"),
			err:     "test.yaml:2:3: $.f: cafe.jacobcolvin.com/v1 moves $.f to $.k.np, which puts a block value inside a flow collection, and cafe does not move a block value into a flow collection",
		},
		"mapping item without braces of a flow sequence that a move puts in a flow mapping": {
			before:  stringtest.LinesLF("f: [0, a: 1]", "k: {z: 0}"),
			earlier: moveTo("$.f", "$.k.np"),
			path:    paths.MustParse("$.k.np[1]"),
			err:     "test.yaml:1:8: $.f[1].a: cafe.jacobcolvin.com/v1 moves $.f to $.k.np, which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"mapping item without braces with an explicit first key that a move puts in a flow mapping": {
			before:  stringtest.LinesLF("f: [0, ? a : 1]", "k: {z: 0}"),
			earlier: moveTo("$.f", "$.k.np"),
			path:    paths.MustParse("$.k.np[1]"),
			err:     "test.yaml:1:8: $.f[1].a: cafe.jacobcolvin.com/v1 moves $.f to $.k.np, which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"mapping item without braces with an explicit first key that a removal copies to an alias": {
			before:  stringtest.LinesLF("x: &x [? q : 1]", "f: {k: *x}"),
			earlier: removeAt("$.x"),
			path:    paths.MustParse("$.f.k[0]"),
			err:     "test.yaml:2:8: $.f.k: cafe.jacobcolvin.com/v1 removes $.x, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"block mapping that a removal copies to an alias in a flow sequence": {
			before:  stringtest.LinesLF("x: &x", "  a: 1", "f: [*x, 2]"),
			earlier: removeAt("$.x"),
			path:    paths.MustParse("$.f[0]"),
			err:     "test.yaml:3:5: $.f[0]: cafe.jacobcolvin.com/v1 removes $.x, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"block sequence that a removal copies to an alias in a flow sequence": {
			before:  stringtest.LinesLF("x: &x", "  - 1", "f: [*x, 2]"),
			earlier: removeAt("$.x"),
			path:    paths.MustParse("$.f[0]"),
			err:     "test.yaml:3:5: $.f[0]: cafe.jacobcolvin.com/v1 removes $.x, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"scalar that a removal copies to an alias in a flow sequence": {
			before:  stringtest.LinesLF("x: &x 1", "f: [*x, 2]"),
			earlier: removeAt("$.x"),
			path:    paths.MustParse("$.f[0]"),
			err:     "test.yaml:2:5: $.f[0]: cafe.jacobcolvin.com/v1 removes $.x, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before)

			var before int

			if tc.earlier != nil {
				if failsWith(t, d.Apply(t.Context(), paths.Doc(), tc.earlier, label), tc.err) {
					return
				}

				before = len(d.Removals())
			}

			require.Empty(t, tc.err, "the earlier Apply passes")

			require.NoError(t, applyAt(t, d, tc.path, func(_ context.Context, n *edit.Node) error {
				return n.Remove()
			}))

			removals := d.Removals()
			require.Len(t, removals, before+1)
			assert.Equal(t, tc.want, removalString(removals[before]))
		})
	}
}

func TestDoc_Removals_Keys(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		before string
		path   paths.Path
		// The removal of the entry at path, as Doc.Removals lists it.
		want string
	}{
		"key of a block mapping": {
			before: stringtest.LinesLF("ab: 1", "b: 2"),
			path:   paths.Doc().Child("ab"),
			want:   `$.ab~ "cafe.jacobcolvin.com/v1" "" key="ab" item=false 0:0-0:2`,
		},
		"key of a block mapping that ends in multi-byte characters": {
			before: stringtest.LinesLF("日本: 1", "b: 2"),
			path:   paths.Doc().Child("日本"),
			want:   `$.日本~ "cafe.jacobcolvin.com/v1" "" key="日本" item=false 0:0-0:2`,
		},
		"key of a flow mapping that ends in multi-byte characters": {
			before: stringtest.LinesLF("f: {日本: 1, b: 2}"),
			path:   paths.MustParse("$.f.日本"),
			want:   `$.f.日本~ "cafe.jacobcolvin.com/v1" "" key="日本" item=false 0:4-0:6`,
		},
		"key that ends in a multi-byte character after a multi-byte character": {
			before: stringtest.LinesLF("m:", "  éé: 1"),
			path:   paths.MustParse("$.m.éé"),
			want:   `$.m.éé~ "cafe.jacobcolvin.com/v1" "" key="éé" item=false 1:2-1:4`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before)

			require.NoError(t, applyAt(t, d, tc.path, func(_ context.Context, n *edit.Node) error {
				return n.Remove()
			}))

			removals := d.Removals()
			require.Len(t, removals, 1)
			assert.Equal(t, tc.want, removalString(removals[0]))
		})
	}
}

// removalString returns rm as a line of text, with its path, its label,
// its reason, its key, whether it removed an item, and its range.
func removalString(rm edit.Removal) string {
	return fmt.Sprintf("%s %q %q key=%q item=%t %d:%d-%d:%d", rm.Path, rm.Label, rm.Reason,
		rm.Key, rm.Item, rm.Range.Start.Line, rm.Range.Start.Col, rm.Range.End.Line, rm.Range.End.Col)
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

func TestDoc_Apply_duplicateKeys(t *testing.T) {
	t.Parallel()

	// A later entry of each key hides the entries of that key before it, so
	// a path of the key selects the last entry. A `.*` selects each entry.
	const dups = "a: 1\na: 2\nb: 3\n"

	tcs := map[string]struct {
		fn edit.Func
		// The text of the document, or dups when empty.
		before string
		want   string
		err    string
		// The texts and the decodes that the Func reads, in the order of its
		// calls, when set.
		texts []string
		ints  []int
	}{
		"reads of each entry": {
			fn:    func(context.Context, *edit.Node) error { return nil },
			want:  dups,
			texts: []string{"1", "2", "3"},
			ints:  []int{1, 2, 3},
		},
		"removal of each entry": {
			fn:  func(_ context.Context, n *edit.Node) error { return n.Remove() },
			err: `test.yaml:1:1: $.a: the edit removes $.a, which reaches "a", a key that its mapping repeats, and cafe does not edit the entries of a repeated key`,
		},
		"rename of each entry to a key of its own": {
			fn:  func(_ context.Context, n *edit.Node) error { return n.SetKey(n.Key() + n.Text()) },
			err: `test.yaml:1:1: $.a: the edit renames $.a to "a1", which reaches "a", a key that its mapping repeats, and cafe does not edit the entries of a repeated key`,
		},
		"set of each entry": {
			// The Func gets a node on the hidden entry, so it writes there
			// through that node, not through a path that selects another entry.
			fn:  func(ctx context.Context, n *edit.Node) error { return n.Set(ctx, 9) },
			err: `test.yaml:1:1: $.a: the edit sets $.a, which reaches "a", a key that its mapping repeats, and cafe does not edit the entries of a repeated key`,
		},
		"rename of each entry to upper case": {
			// The two entries of a read one key before the Apply, so they can
			// read one key again.
			fn:  func(_ context.Context, n *edit.Node) error { return n.SetKey(strings.ToUpper(n.Key())) },
			err: `test.yaml:1:1: $.a: the edit renames $.a to "A", which reaches "a", a key that its mapping repeats, and cafe does not edit the entries of a repeated key`,
		},
		"rename of an entry to the key of an entry of another text": {
			// The two entries read different keys before the Apply.
			fn: func(_ context.Context, n *edit.Node) error {
				if n.Key() != "b" {
					return nil
				}

				return n.SetKey("a")
			},
			err: `test.yaml:1:1: $.a: the edit renames $.b to "a", which reaches "a", a key that its mapping repeats, and cafe does not edit the entries of a repeated key`,
		},
		"path below an entry that a later entry hides": {
			// The path reads below the node of the entry, which holds a scalar.
			// It steps through the repeated key first, so the refusal of that
			// key decides the problem.
			fn: func(ctx context.Context, n *edit.Node) error {
				if n.Text() != "1" {
					return nil
				}

				return n.At(paths.Current().Child("x")).Set(ctx, 1)
			},
			err: `test.yaml:1:1: $.a: the edit sets $.a.x, which reaches "a", a key that its mapping repeats`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			before := cmp.Or(tc.before, dups)
			d := newDoc(t, before, niceyaml.WithAllowDuplicateKeys(true))

			var (
				texts []string
				ints  []int
			)

			err := d.Apply(t.Context(), paths.MustParse("$.*"), func(ctx context.Context, n *edit.Node) error {
				texts = append(texts, n.Text())

				v, err := n.Decode[int](ctx)
				require.NoError(t, err)

				ints = append(ints, v)

				return tc.fn(ctx, n)
			})

			requireEdit(t, d, err, tc.err, tc.want)

			if tc.texts != nil {
				assert.Equal(t, tc.texts, texts)
				assert.Equal(t, tc.ints, ints)
			}
		})
	}
}

func TestWithProtected(t *testing.T) {
	t.Parallel()

	const (
		cafe = "apiVersion: v1\nkind: Cafe\nspec:\n  seats: 4\n"
		meta = "metadata:\n  name: kaffe\n  ns: town\nspec:\n  seats: 4\n"
		// The protected version is the value that other reads through its
		// alias.
		shared = "metadata: &m {version: v1, name: n}\nother: *m\n"
		// The protected version is the value of base that the merge key of
		// metadata brings in.
		merged = "base: &m {version: v1}\nmetadata:\n  <<: *m\n"
	)

	metadata := paths.Doc().Child("metadata")
	version := []paths.Path{metadata.Child("version")}
	otherVersion := paths.MustParse("$.other.version")

	tcs := map[string]struct {
		fn     edit.Func
		path   paths.Path
		before string
		// The text after the Apply, or the error of the Apply, which leaves
		// the text as it was.
		want, err string
		// The text of a reference document, or "".
		ref       string
		protected []paths.Path
	}{
		"set of a mapping whose merge key of a reference document decides the protected path": {
			// The reference document decides $.m.a, so the mapping holds the
			// protected value, whatever the reference document holds.
			before:    stringtest.LinesLF("m:", "  a: 1", "  <<: *r"),
			ref:       "r: &r {b: 2}\n",
			protected: []paths.Path{paths.MustParse("$.m.a")},
			path:      paths.MustParse("$.m"),
			fn:        func(ctx context.Context, n *edit.Node) error { return n.Set(ctx, 5) },
			err:       "cafe.jacobcolvin.com/v1 sets $.m, which holds a protected value",
		},
		"set of a mapping value whose merge key of a reference document decides the protected path": {
			// The protected own entry is in the text, so the Set does not
			// create it.
			before:    stringtest.LinesLF("m:", "  a: 1", "  <<: *r"),
			ref:       "r: &r {b: 2}\n",
			protected: []paths.Path{paths.MustParse("$.m.a")},
			path:      paths.MustParse("$.m"),
			fn: func(ctx context.Context, n *edit.Node) error {
				return n.Set(ctx, map[string]int{"a": 5})
			},
			err: "cafe.jacobcolvin.com/v1 sets $.m, which holds a protected value",
		},
		"removal of a mapping whose merge key of a reference document decides the protected path": {
			before:    stringtest.LinesLF("m:", "  a: 1", "  <<: *r", "k: 1"),
			ref:       "r: &r {b: 2}\n",
			protected: []paths.Path{paths.MustParse("$.m.a")},
			path:      paths.MustParse("$.m"),
			fn:        func(_ context.Context, n *edit.Node) error { return n.Remove() },
			err:       "cafe.jacobcolvin.com/v1 removes $.m, which holds a protected value",
		},
		"move of a mapping whose merge key of a reference document decides the protected path": {
			before:    stringtest.LinesLF("m:", "  a: 1", "  <<: *r", "k: 1"),
			ref:       "r: &r {b: 2}\n",
			protected: []paths.Path{paths.MustParse("$.m.a")},
			path:      paths.MustParse("$.m"),
			fn:        func(_ context.Context, n *edit.Node) error { return n.Move(paths.MustParse("$.n")) },
			err:       `cafe.jacobcolvin.com/v1 moves "m", which holds a protected value`,
		},
		"set of the value above a mapping whose merge key of a reference document decides the protected path": {
			before:    stringtest.LinesLF("spec:", "  template:", "    kind: Pod", "    <<: *r"),
			ref:       "r: &r {b: 2}\n",
			protected: []paths.Path{paths.MustParse("$.spec.template.kind")},
			path:      paths.MustParse("$.spec"),
			fn: func(ctx context.Context, n *edit.Node) error {
				return n.Set(ctx, map[string]any{"template": map[string]int{"x": 1}})
			},
			err: "cafe.jacobcolvin.com/v1 sets $.spec, which holds a protected value",
		},
		"set of an alias of a reference document with a protected path below it": {
			before:    stringtest.LinesLF("m: *r", "k: 1"),
			ref:       "r: &r {a: 1}\n",
			protected: []paths.Path{paths.MustParse("$.m.a")},
			path:      paths.MustParse("$.m"),
			fn:        func(ctx context.Context, n *edit.Node) error { return n.Set(ctx, 5) },
			err:       "cafe.jacobcolvin.com/v1 sets $.m, which holds a protected value",
		},
		"set of an own entry after the merge key of a mapping with a protected path that a reference decides": {
			// A control: the edit inside the mapping keeps the merge key that
			// reads the protected value.
			before:    stringtest.LinesLF("m:", "  a: 1", "  <<: *r", "  b: 3"),
			ref:       "r: &r {c: 2}\n",
			protected: []paths.Path{paths.MustParse("$.m.a")},
			path:      paths.MustParse("$.m.b"),
			fn:        func(ctx context.Context, n *edit.Node) error { return n.Set(ctx, 4) },
			want:      stringtest.LinesLF("m:", "  a: 1", "  <<: *r", "  b: 4"),
		},
		"rename of a mapping with a protected path that a reference decides": {
			// A control: a rename of a value that holds a protected value
			// passes.
			before:    stringtest.LinesLF("m:", "  a: 1", "  <<: *r"),
			ref:       "r: &r {c: 2}\n",
			protected: []paths.Path{paths.MustParse("$.m.a")},
			path:      paths.MustParse("$.m"),
			fn:        func(_ context.Context, n *edit.Node) error { return n.SetKey("mm") },
			want:      stringtest.LinesLF("mm:", "  a: 1", "  <<: *r"),
		},
		"set of a mapping that a rename of its key moved, whose merge key of a reference document decides the protected path": {
			// The protection follows the mapping to its new key.
			before:    stringtest.LinesLF("m:", "  <<: *r", "  a: 1"),
			ref:       "r: &r {x: 2}\n",
			protected: []paths.Path{paths.MustParse("$.m.x")},
			path:      paths.Doc(),
			fn: func(ctx context.Context, n *edit.Node) error {
				err := n.At(paths.MustParse("$.m")).SetKey("q")
				if err != nil {
					return err
				}

				return n.At(paths.MustParse("$.q")).Set(ctx, 1)
			},
			err: "cafe.jacobcolvin.com/v1 sets $.q, which holds a protected value",
		},
		"removal of a mapping that a rename of its key moved, whose merge key of a reference document decides the protected path": {
			before:    stringtest.LinesLF("m:", "  <<: *r", "  a: 1", "k: 1"),
			ref:       "r: &r {x: 2}\n",
			protected: []paths.Path{paths.MustParse("$.m.x")},
			path:      paths.Doc(),
			fn: func(_ context.Context, n *edit.Node) error {
				err := n.At(paths.MustParse("$.m")).SetKey("q")
				if err != nil {
					return err
				}

				return n.At(paths.MustParse("$.q")).Remove()
			},
			err: "cafe.jacobcolvin.com/v1 removes $.q, which holds a protected value",
		},
		"set of an alias of a reference document that a rename of its key moved, with a protected path below it": {
			before:    stringtest.LinesLF("m: *r", "k: 1"),
			ref:       "r: &r {x: 2}\n",
			protected: []paths.Path{paths.MustParse("$.m.x")},
			path:      paths.Doc(),
			fn: func(ctx context.Context, n *edit.Node) error {
				err := n.At(paths.MustParse("$.m")).SetKey("q")
				if err != nil {
					return err
				}

				return n.At(paths.MustParse("$.q")).Set(ctx, 1)
			},
			err: "cafe.jacobcolvin.com/v1 sets $.q, which holds a protected value",
		},
		"set of the anchored mapping whose merge key of a reference document decides a protected path through an alias": {
			// The lookup of $.m.x reads *r at the merge key of h, so h holds
			// the protected value.
			before:    stringtest.LinesLF("h: &h", "  <<: *r", "  a: 1", "m: *h"),
			ref:       "r: &r {x: 2}\n",
			protected: []paths.Path{paths.MustParse("$.m.x")},
			path:      paths.MustParse("$.h"),
			fn:        func(ctx context.Context, n *edit.Node) error { return n.Set(ctx, map[string]int{"a": 1}) },
			err:       "cafe.jacobcolvin.com/v1 sets $.h, which holds a protected value",
		},
		"set of the anchored mapping whose merge key of a reference document decides a protected path through an alias of it": {
			before:    stringtest.LinesLF("m: &m", "  <<: *r", "  x: 1", "n: *m"),
			ref:       "r: &r {y: 1}\n",
			protected: []paths.Path{paths.MustParse("$.n.y")},
			path:      paths.MustParse("$.m"),
			fn:        func(ctx context.Context, n *edit.Node) error { return n.Set(ctx, 5) },
			err:       "cafe.jacobcolvin.com/v1 sets $.m, which holds a protected value",
		},
		"set of a source whose merge key of a reference document decides a protected path through a merge key": {
			before:    stringtest.LinesLF("h: &h", "  <<: *r", "  a: 1", "m:", "  <<: *h"),
			ref:       "r: &r {x: 2}\n",
			protected: []paths.Path{paths.MustParse("$.m.x")},
			path:      paths.MustParse("$.h"),
			fn:        func(ctx context.Context, n *edit.Node) error { return n.Set(ctx, map[string]int{"a": 1}) },
			err:       "cafe.jacobcolvin.com/v1 sets $.h, which holds a protected value",
		},
		"set of an alias that reads a mapping whose merge key of a reference document decides the protected path": {
			// The alias only reads the mapping that holds the protected value,
			// so the Set leaves that mapping in the text.
			before:    stringtest.LinesLF("h: &h", "  <<: *r", "  a: 1", "m: *h"),
			ref:       "r: &r {x: 2}\n",
			protected: []paths.Path{paths.MustParse("$.m.x")},
			path:      paths.MustParse("$.m"),
			fn:        func(ctx context.Context, n *edit.Node) error { return n.Set(ctx, map[string]int{"a": 1}) },
			want:      stringtest.LinesLF("h: &h", "  <<: *r", "  a: 1", "m: {a: 1}"),
		},
		"removal of an alias that reads a mapping whose merge key of a reference document decides the protected path": {
			before:    stringtest.LinesLF("h: &h", "  <<: *r", "  a: 1", "m: *h", "k: 1"),
			ref:       "r: &r {x: 2}\n",
			protected: []paths.Path{paths.MustParse("$.m.x")},
			path:      paths.MustParse("$.m"),
			fn:        func(_ context.Context, n *edit.Node) error { return n.Remove() },
			want:      stringtest.LinesLF("h: &h", "  <<: *r", "  a: 1", "k: 1"),
		},
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
		"set through an alias": {
			// The problem binds at the token that the call reaches, under the path of
			// the value that holds it in the user's text (fix19 P56).
			before:    shared,
			protected: version,
			path:      otherVersion,
			fn:        func(ctx context.Context, n *edit.Node) error { return n.Set(ctx, "v2") },
			err: "test.yaml:1:24: $.metadata.version: cafe.jacobcolvin.com/v1 sets $.other.version, " +
				"which is a protected value",
		},
		"removal through an alias": {
			before:    shared,
			protected: version,
			path:      otherVersion,
			fn:        func(_ context.Context, n *edit.Node) error { return n.Remove() },
			err:       "cafe.jacobcolvin.com/v1 removes $.other.version, which is a protected value",
		},
		"rename through an alias": {
			before:    shared,
			protected: version,
			path:      otherVersion,
			fn:        func(_ context.Context, n *edit.Node) error { return n.SetKey("x") },
			err:       `cafe.jacobcolvin.com/v1 renames "version", which is a protected value`,
		},
		"move through an alias": {
			before:    shared,
			protected: version,
			path:      otherVersion,
			fn:        func(_ context.Context, n *edit.Node) error { return n.Move(paths.Doc().Child("z")) },
			err:       `cafe.jacobcolvin.com/v1 moves "version", which is a protected value`,
		},
		"set of the anchored value that a merge key brings in": {
			before:    merged,
			protected: version,
			path:      paths.MustParse("$.base.version"),
			fn:        func(ctx context.Context, n *edit.Node) error { return n.Set(ctx, "v2") },
			err:       "cafe.jacobcolvin.com/v1 sets $.base.version, which is a protected value",
		},
		"set of the anchored mapping that a merge key brings in": {
			before:    merged,
			protected: version,
			path:      paths.Doc().Child("base"),
			fn:        func(ctx context.Context, n *edit.Node) error { return n.Set(ctx, 1) },
			err:       "cafe.jacobcolvin.com/v1 sets $.base, which holds a protected value",
		},
		"removal of the anchor of a protected alias": {
			// The removal takes the anchor that the protected alias reads.
			before:    shared,
			protected: []paths.Path{paths.Doc().Child("other")},
			path:      metadata,
			fn:        func(_ context.Context, n *edit.Node) error { return n.Remove() },
			err:       "test.yaml:2:8: $.other: cafe.jacobcolvin.com/v1 removes $.metadata, which this alias reads, and this program does not write a copy of a value at its alias",
		},
		"set of the path of a protected value that the text lacks": {
			before:    "a: 1\n",
			protected: []paths.Path{paths.MustParse("$.a.kind")},
			path:      paths.Doc().Child("a"),
			fn:        func(ctx context.Context, n *edit.Node) error { return n.Set(ctx, 2) },
			want:      "a: 2\n",
		},
		"set that creates a protected value": {
			before:    "a: 1\n",
			protected: []paths.Path{paths.MustParse("$.a.kind")},
			path:      paths.Doc().Child("a"),
			fn: func(ctx context.Context, n *edit.Node) error {
				return n.Set(ctx, map[string]any{"kind": "x"})
			},
			// The problem binds where a problem at $.a binds, at its value
			// (fix20 G2-3).
			err: "test.yaml:1:4: $.a: cafe.jacobcolvin.com/v1 sets $.a, which creates the protected value $.a.kind",
		},
		"set of a protected path that the text lacks": {
			// The call creates the place, so the problem binds at the nearest value above it that the user
			// wrote, under its path (fix19 G1-1).
			before:    "a: 1\n",
			protected: []paths.Path{paths.Doc().Child("kind")},
			path:      paths.Doc().Child("kind"),
			fn:        func(ctx context.Context, n *edit.Node) error { return n.Set(ctx, "x") },
			err: "test.yaml:1:1: $: cafe.jacobcolvin.com/v1 sets $.kind, " +
				"which creates the protected value $.kind",
		},
		"set of a protected path below a mapping that the text lacks": {
			// The Set creates the key, so the problem binds at the key of the
			// nearest value that the user wrote, under its path, with a
			// detail that names the call.
			before:    stringtest.LinesLF("m:", "  a: 1"),
			protected: []paths.Path{paths.MustParse("$.m.z")},
			path:      paths.MustParse("$.m"),
			fn: func(ctx context.Context, n *edit.Node) error {
				return n.At(paths.Current().Child("z")).Set(ctx, 1)
			},
			err: "test.yaml:1:1: $.m: cafe.jacobcolvin.com/v1 sets $.m.z, which creates the protected value $.m.z\n" +
				"`-- cafe.jacobcolvin.com/v1 sets $.m.z",
		},
		"set of a protected path below an item after the removal of an earlier item": {
			// The user's text spells $.l[0] for the item 0, so the problem
			// binds under the path of the item 1, which the Set extends.
			before:    stringtest.LinesLF("z0: 0", "l:", "  - 0", "  - x: 1"),
			protected: []paths.Path{paths.MustParse("$.l[0].new")},
			path:      paths.Doc(),
			fn: func(ctx context.Context, n *edit.Node) error {
				err := n.At(paths.MustParse("$.l[0]")).Remove()
				if err != nil {
					return err
				}

				return n.At(paths.MustParse("$.l[0].new")).Set(ctx, 1)
			},
			err: "test.yaml:4:3: $.l[1]: cafe.jacobcolvin.com/v1 sets $.l[0].new, which creates the protected value " +
				"$.l[0].new",
		},
		"set of a protected path below a renamed mapping": {
			before:    stringtest.LinesLF("z0: 0", "m:", "  x: 1"),
			protected: []paths.Path{paths.MustParse("$.m2.new")},
			path:      paths.Doc(),
			fn: func(ctx context.Context, n *edit.Node) error {
				err := n.At(paths.Doc().Child("m")).SetKey("m2")
				if err != nil {
					return err
				}

				return n.At(paths.MustParse("$.m2.new")).Set(ctx, 1)
			},
			err: "test.yaml:2:1: $.m: cafe.jacobcolvin.com/v1 sets $.m2.new, which creates the protected value $.m2.new",
		},
		"rename onto a protected path that the text lacks": {
			before:    "a: 1\n",
			protected: []paths.Path{paths.Doc().Child("kind")},
			path:      paths.Doc().Child("a"),
			fn:        func(_ context.Context, n *edit.Node) error { return n.SetKey("kind") },
			// The problem binds where a problem at $.a binds, at its value, as
			// the refusal of a rename of a protected value does (fix20 G2-3).
			err: "test.yaml:1:4: $.a: cafe.jacobcolvin.com/v1 renames $.a to \"kind\", " +
				"which creates the protected value $.kind",
		},
		"set of the source of a merge key that creates a protected value": {
			before:    "base: &b {x: 1}\ndoc:\n  <<: *b\n",
			protected: []paths.Path{paths.MustParse("$.doc.kind")},
			path:      paths.MustParse("$.base.kind"),
			fn:        func(ctx context.Context, n *edit.Node) error { return n.Set(ctx, "Tea") },
			err:       "which creates the protected value $.doc.kind",
		},
		"set after the removal of the item before a protected path that the text lacks": {
			// The guard of a path that holds no value stays at the path as
			// the Apply found it.
			before:    "- y\n",
			protected: []paths.Path{paths.MustParse("$[1]")},
			path:      paths.Doc(),
			fn: func(ctx context.Context, n *edit.Node) error {
				err := n.At(paths.MustParse("$[0]")).Remove()
				if err != nil {
					return err
				}

				return n.At(paths.MustParse("$[0]")).Set(ctx, "z")
			},
			want: "- z\n",
		},
		"set of a later item after the removal of the item before a protected path that the text lacks": {
			before:    "- y\n- w\n",
			protected: []paths.Path{paths.MustParse("$[2]")},
			path:      paths.Doc(),
			fn: func(ctx context.Context, n *edit.Node) error {
				err := n.At(paths.MustParse("$[0]")).Remove()
				if err != nil {
					return err
				}

				return n.At(paths.MustParse("$[1]")).Set(ctx, "z")
			},
			want: "- w\n- z\n",
		},
		"set of the item that took the index of the protected item": {
			// The protected value is the item that the Apply found at the
			// path, wherever an earlier call moves it.
			before:    stringtest.LinesLF("l:", "  - a", "  - b"),
			protected: []paths.Path{paths.MustParse("$.l[1]")},
			path:      paths.Doc(),
			fn: func(ctx context.Context, n *edit.Node) error {
				err := n.At(paths.MustParse("$.l[0]")).Remove()
				if err != nil {
					return err
				}

				return n.At(paths.MustParse("$.l[0]")).Set(ctx, "c")
			},
			err: "cafe.jacobcolvin.com/v1 sets $.l[0], which is a protected value",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			protected := tc.protected
			if protected == nil {
				protected = typeFields
			}

			opts := []niceyaml.SourceOption{niceyaml.WithName("test.yaml")}
			if tc.ref != "" {
				opts = append(opts, niceyaml.WithReferences(niceyaml.NewSourceFromString(tc.ref)))
			}

			doc := yamltest.FirstDocument(t, tc.before, opts...)

			d, err := edit.New(doc, edit.WithProtected(protected...))
			require.NoError(t, err)

			requireEdit(t, d, applyAt(t, d, tc.path, tc.fn), tc.err, tc.want)

			if tc.err != "" {
				assert.Equal(t, tc.before, d.Text())
			}
		})
	}
}

func TestWithProtected_Spellings(t *testing.T) {
	t.Parallel()

	const (
		// The paths $.base.version and $.md.version name one protected
		// value, which md reads through an alias, or through a merge key.
		aliased = "base: &m {version: v1}\nmd: *m\n"
		merged  = "base: &m {version: v1}\nmd:\n  <<: *m\n"
		// The mapping of the merge key hides the version of its merge
		// source, so only the path $.md.version names that value.
		shadowed = "a:\n  <<: &m {version: v1}\n  version: v0\nmd: *m\n"
	)

	md := paths.Doc().Child("md")
	base := paths.Doc().Child("base")

	tcs := map[string]struct {
		fn   edit.Func
		path paths.Path
		// The spellings of the protected value, or nil for $.base.version
		// and $.md.version.
		protected []paths.Path
		before    string
		want, err string
	}{
		"set of an alias that reads a value that a mapping hides": {
			before:    shadowed,
			protected: []paths.Path{md.Child("version")},
			path:      md,
			fn:        func(ctx context.Context, n *edit.Node) error { return n.Set(ctx, 1) },
			want:      "a:\n  <<: &m {version: v1}\n  version: v0\nmd: 1\n",
		},
		"removal of an alias that reads a value that a mapping hides": {
			before:    shadowed,
			protected: []paths.Path{md.Child("version")},
			path:      md,
			fn:        func(_ context.Context, n *edit.Node) error { return n.Remove() },
			want:      "a:\n  <<: &m {version: v1}\n  version: v0\n",
		},
		"rename of an alias that reads a value that a mapping hides": {
			// A control: the rename keeps the bytes of the protected value, so
			// it passes however the guard finds that value.
			before:    shadowed,
			protected: []paths.Path{md.Child("version")},
			path:      md,
			fn:        func(_ context.Context, n *edit.Node) error { return n.SetKey("mm") },
			want:      "a:\n  <<: &m {version: v1}\n  version: v0\nmm: *m\n",
		},
		"set through an alias of a value that a mapping hides": {
			// The problem binds at the token that the call reaches, under the path of
			// the value that holds it in the user's text (fix19 P56).
			before:    shadowed,
			protected: []paths.Path{md.Child("version")},
			path:      md.Child("version"),
			fn:        func(ctx context.Context, n *edit.Node) error { return n.Set(ctx, "v2") },
			err: "test.yaml:2:20: $.a: cafe.jacobcolvin.com/v1 sets $.md.version, " +
				"which is a protected value",
		},
		"removal of the mapping that merges a value that it hides": {
			before:    shadowed,
			protected: []paths.Path{md.Child("version")},
			path:      paths.Doc().Child("a"),
			fn:        func(_ context.Context, n *edit.Node) error { return n.Remove() },
			err:       "test.yaml:1:1: $.a: cafe.jacobcolvin.com/v1 removes $.a, which holds a protected value",
		},
		"set of the version that hides the value": {
			before:    shadowed,
			protected: []paths.Path{md.Child("version")},
			path:      paths.MustParse("$.a.version"),
			fn:        func(ctx context.Context, n *edit.Node) error { return n.Set(ctx, "v3") },
			want:      "a:\n  <<: &m {version: v1}\n  version: v3\nmd: *m\n",
		},
		"set of an alias that reads the value": {
			before: aliased,
			path:   md,
			fn:     func(ctx context.Context, n *edit.Node) error { return n.Set(ctx, 1) },
			want:   "base: &m {version: v1}\nmd: 1\n",
		},
		"removal of an alias that reads the value": {
			before: aliased,
			path:   md,
			fn:     func(_ context.Context, n *edit.Node) error { return n.Remove() },
			want:   "base: &m {version: v1}\n",
		},
		"rename of an alias that reads the value": {
			before: aliased,
			path:   md,
			fn:     func(_ context.Context, n *edit.Node) error { return n.SetKey("q") },
			want:   "base: &m {version: v1}\nq: *m\n",
		},
		"set of a mapping that merges the value": {
			before: merged,
			path:   md,
			fn:     func(ctx context.Context, n *edit.Node) error { return n.Set(ctx, 1) },
			want:   "base: &m {version: v1}\nmd: 1\n",
		},
		"removal of a mapping that merges the value": {
			before: merged,
			path:   md,
			fn:     func(_ context.Context, n *edit.Node) error { return n.Remove() },
			want:   "base: &m {version: v1}\n",
		},
		"rename of a mapping that merges the value": {
			before: merged,
			path:   md,
			fn:     func(_ context.Context, n *edit.Node) error { return n.SetKey("q") },
			want:   "base: &m {version: v1}\nq:\n  <<: *m\n",
		},
		"rename of the mapping that holds the value": {
			// The value keeps its text and its key at the new path.
			before: aliased,
			path:   base,
			fn:     func(_ context.Context, n *edit.Node) error { return n.SetKey("b") },
			want:   "b: &m {version: v1}\nmd: *m\n",
		},
		"set of the mapping that holds the value": {
			before: aliased,
			path:   base,
			fn:     func(ctx context.Context, n *edit.Node) error { return n.Set(ctx, 1) },
			err:    "test.yaml:1:1: $.base: cafe.jacobcolvin.com/v1 sets $.base, which holds a protected value",
		},
		"removal of the mapping that holds the value": {
			before: merged,
			path:   base,
			fn:     func(_ context.Context, n *edit.Node) error { return n.Remove() },
			err:    "test.yaml:1:1: $.base: cafe.jacobcolvin.com/v1 removes $.base, which holds a protected value",
		},
		"set through the alias": {
			// The problem binds at the token that the call reaches, under the path of
			// the value that holds it in the user's text (fix19 P56).
			before: aliased,
			path:   md.Child("version"),
			fn:     func(ctx context.Context, n *edit.Node) error { return n.Set(ctx, "v2") },
			err: "test.yaml:1:20: $.base.version: cafe.jacobcolvin.com/v1 sets $.md.version, " +
				"which is a protected value",
		},
		"set through the merge key": {
			// The problem binds at the token that the call reaches, under the path of
			// the value that holds it in the user's text (fix19 P56).
			before: merged,
			path:   md.Child("version"),
			fn:     func(ctx context.Context, n *edit.Node) error { return n.Set(ctx, "v2") },
			err: "test.yaml:1:20: $.base.version: cafe.jacobcolvin.com/v1 sets $.md.version, " +
				"which is a protected value",
		},
		"move of the mapping that holds the value": {
			before: aliased,
			path:   base,
			fn:     func(_ context.Context, n *edit.Node) error { return n.Move(paths.Doc().Child("z")) },
			err:    `test.yaml:1:1: $.base: cafe.jacobcolvin.com/v1 moves "base", which holds a protected value`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			spellings := tc.protected
			if spellings == nil {
				spellings = []paths.Path{base.Child("version"), md.Child("version")}
			}

			for _, protected := range spellings {
				doc := yamltest.FirstDocument(t, tc.before, niceyaml.WithName("test.yaml"))

				d, err := edit.New(doc, edit.WithProtected(protected))
				require.NoError(t, err)

				requireEdit(t, d, applyAt(t, d, tc.path, tc.fn), tc.err, tc.want)
			}
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

	t.Run("through an alias", func(t *testing.T) {
		t.Parallel()

		// A wildcard skips each place that reaches the protected value, so
		// it skips $.other.version, which reads $.metadata.version.
		doc := yamltest.FirstDocument(t, "metadata: &m {version: v1, name: n}\nother: *m\n")

		d, err := edit.New(doc, edit.WithProtected(paths.MustParse("$.metadata.version")))
		require.NoError(t, err)

		var calls []string

		require.NoError(
			t,
			d.Apply(t.Context(), paths.MustParse("$.other.*"), func(_ context.Context, n *edit.Node) error {
				calls = append(calls, n.Path().String())

				return n.Remove()
			}),
		)

		assert.Equal(t, []string{"$.other.name"}, calls)
		assert.Equal(t, "metadata: &m {version: v1}\nother: *m\n", d.Text())
	})
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
