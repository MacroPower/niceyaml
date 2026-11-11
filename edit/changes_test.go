package edit_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/edit"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// docSources holds the Source of each Doc that [newSourceDoc] makes, so
// [requireEdit] checks the Changes of the Doc against its Source.
var docSources sync.Map

// requireChanges checks that the Changes of docs, applied to the text of
// src, give the text that [edit.FileText] returns for docs. The Changes of
// each Doc must come in the order of the text and must not overlap. Two
// Changes in a row must not write back the user's text between their ends.
// A Doc whose text matches the user's text must have no Changes.
func requireChanges(t *testing.T, src *niceyaml.Source, docs ...*edit.Doc) {
	t.Helper()

	want, err := edit.FileText(src, docs...)
	require.NoError(t, err)

	type replacement struct {
		text       string
		start, end int
	}

	var all []replacement

	for _, d := range docs {
		// A Doc whose text reads as the user wrote it has no Changes.
		own, err := edit.FileText(src, d)
		require.NoError(t, err)

		if own == src.Text() {
			assert.Empty(t, d.Changes())
		}

		last := -1
		first := len(all)

		for _, c := range d.Changes() {
			start, ok := src.Offset(c.Range.Start)
			require.True(t, ok, "start of %v", c.Range)

			end, ok := src.Offset(c.Range.End)
			require.True(t, ok, "end of %v", c.Range)

			require.LessOrEqual(t, start, end, "range %v", c.Range)
			require.Less(t, last, start, "change at %v overlaps or follows the change before it", c.Range)
			require.NotEqual(t, src.Text()[start:end], c.Text, "change at %v does nothing", c.Range)

			// Two Changes in a row, with the kept text between them, must
			// not write the user's text back.
			if len(all) > first {
				prev := all[len(all)-1]
				require.NotEqual(t,
					src.Text()[prev.start:end], prev.text+src.Text()[prev.end:start]+c.Text,
					"change at %v undoes the change before it", c.Range)
			}

			all = append(all, replacement{start: start, end: end, text: c.Text})
			last = end
		}
	}

	// The Docs hold disjoint documents, so the replacements of all Docs
	// sort by their starts.
	text := src.Text()

	var (
		b   strings.Builder
		pos = 0
	)

	for len(all) > 0 {
		next := 0
		for i, r := range all {
			if r.start < all[next].start {
				next = i
			}
		}

		r := all[next]
		all = append(all[:next], all[next+1:]...)

		require.LessOrEqual(t, pos, r.start)
		b.WriteString(text[pos:r.start])
		b.WriteString(r.text)

		pos = r.end
	}

	b.WriteString(text[pos:])

	assert.Equal(t, want, b.String())
}

// change returns the [edit.Change] that replaces the range from line sl,
// column sc, to line el, column ec, with text.
func change(sl, sc, el, ec int, text string) edit.Change {
	return edit.Change{Range: position.NewRange(position.New(sl, sc), position.New(el, ec)), Text: text}
}

func TestDoc_Changes(t *testing.T) {
	t.Parallel()

	set := func(path paths.Path, v any) edit.Func {
		return func(ctx context.Context, n *edit.Node) error {
			return n.At(path).Set(ctx, v)
		}
	}

	tcs := map[string]struct {
		text string
		// The index of the document in text.
		doc int
		fn  edit.Func
		// The Changes of the Doc after an Apply of fn at the root.
		want []edit.Change
	}{
		"no edit": {
			text: stringtest.LinesLF("a: 1 # c", "b: 2"),
			fn: func(context.Context, *edit.Node) error {
				return nil
			},
		},
		"set of a scalar keeps its comment": {
			text: stringtest.LinesLF("kind: Cafe", "name: old   # the name", "b: 2"),
			fn:   set(paths.Current().Child("name"), "new"),
			want: []edit.Change{change(1, 6, 1, 9, "new")},
		},
		"sets at two places": {
			text: stringtest.LinesLF("a: x", "b: x", "c: x"),
			fn: func(ctx context.Context, n *edit.Node) error {
				err := n.At(paths.Current().Child("a")).Set(ctx, "w")
				if err != nil {
					return err
				}

				return n.At(paths.Current().Child("c")).Set(ctx, "z")
			},
			want: []edit.Change{change(0, 3, 0, 4, "w"), change(2, 3, 2, 4, "z")},
		},
		"new entry": {
			text: stringtest.LinesLF("a: 1", "b: 2"),
			fn:   set(paths.Current().Child("c"), 3),
			want: []edit.Change{change(2, 0, 2, 0, "c: 3\n")},
		},
		"removal": {
			text: stringtest.LinesLF("a: 1", "b: 2 # gone", "c: 3"),
			fn: func(_ context.Context, n *edit.Node) error {
				return n.At(paths.Current().Child("b")).Remove()
			},
			want: []edit.Change{change(1, 0, 2, 0, "")},
		},
		"move": {
			text: stringtest.LinesLF("a: 1", "b: 2", "c: 3"),
			fn: func(_ context.Context, n *edit.Node) error {
				return n.At(paths.Current().Child("a")).Move(paths.Doc().Child("a"), edit.After("c"))
			},
			want: []edit.Change{change(0, 0, 1, 0, ""), change(3, 0, 3, 0, "a: 1\n")},
		},
		"set back to the old value": {
			text: stringtest.LinesLF("a: 1", "b: 2"),
			fn: func(ctx context.Context, n *edit.Node) error {
				err := n.At(paths.Current().Child("a")).Set(ctx, 5)
				if err != nil {
					return err
				}

				return n.At(paths.Current().Child("a")).Set(ctx, 1)
			},
		},
		"edits that rebuild the text with the origins of other bytes": {
			// The new key "a" keeps the ": 1\n" of b. The new entry of b
			// writes the rest, so the text reads as the user wrote it.
			text: stringtest.LinesLF("a: 1", "b: 1"),
			fn: func(ctx context.Context, n *edit.Node) error {
				err := n.At(paths.Current().Child("a")).Remove()
				if err != nil {
					return err
				}

				err = n.At(paths.Current().Child("b")).SetKey("a")
				if err != nil {
					return err
				}

				return n.At(paths.Current().Child("b")).Set(ctx, 1)
			},
		},
		"edits that rebuild text next to another edit": {
			// The lines of a and b read as the user wrote them, so only the
			// Set of c changes the text.
			text: stringtest.LinesLF("c: 5", "a: 1", "b: 1"),
			fn: func(ctx context.Context, n *edit.Node) error {
				err := n.At(paths.Current().Child("a")).Remove()
				if err != nil {
					return err
				}

				err = n.At(paths.Current().Child("b")).SetKey("a")
				if err != nil {
					return err
				}

				err = n.At(paths.Current().Child("b")).Set(ctx, 1)
				if err != nil {
					return err
				}

				return n.At(paths.Current().Child("c")).Set(ctx, 6)
			},
			want: []edit.Change{change(0, 3, 0, 4, "6")},
		},
		"edit inside an entry that edits rebuild": {
			// The new entry of a takes the origins of the lines of b, and the
			// new entry of b writes the lines of b again. The Changes hold
			// only the bytes that read differently from the user's text.
			text: stringtest.LinesLF("a:", "  x: 1", "  y: 2", "b:", "  x: 1", "  y: 2"),
			fn: func(ctx context.Context, n *edit.Node) error {
				err := n.At(paths.Current().Child("a")).Remove()
				if err != nil {
					return err
				}

				err = n.At(paths.Current().Child("b")).SetKey("a")
				if err != nil {
					return err
				}

				err = n.At(paths.Current().Child("b")).Set(ctx, map[string]int{"x": 1, "y": 2})
				if err != nil {
					return err
				}

				return n.At(paths.Current().Child("a").Child("y")).Set(ctx, 3)
			},
			want: []edit.Change{change(2, 5, 2, 6, "3"), change(5, 2, 5, 3, `"y"`)},
		},
		"sets of both entries that edits rebuild": {
			text: stringtest.LinesLF("a: 1", "b: 1"),
			fn: func(ctx context.Context, n *edit.Node) error {
				err := n.At(paths.Current().Child("a")).Remove()
				if err != nil {
					return err
				}

				err = n.At(paths.Current().Child("b")).SetKey("a")
				if err != nil {
					return err
				}

				err = n.At(paths.Current().Child("a")).Set(ctx, 7)
				if err != nil {
					return err
				}

				return n.At(paths.Current().Child("b")).Set(ctx, 7)
			},
			want: []edit.Change{change(0, 3, 0, 4, "7"), change(1, 3, 1, 4, "7")},
		},
		"move that keeps its order": {
			// The Move writes the space after "w:" again, and the space it
			// writes matches the space of the user's text, so it stays out of
			// the Changes.
			text: stringtest.LinesLF("r:", "  x: 1", "", "w: v # c", "", "m: 2"),
			fn: func(_ context.Context, n *edit.Node) error {
				return n.At(paths.Current().Child("w")).Move(paths.Doc().Child("r").Child("w"))
			},
			want: []edit.Change{change(2, 0, 3, 0, "  ")},
		},
		"later document": {
			// The Range holds the lines of the file.
			text: stringtest.LinesLF("a: 1", "---", "a: 2 # second"),
			doc:  1,
			fn:   set(paths.Current().Child("a"), 30),
			want: []edit.Change{change(2, 3, 2, 4, "30")},
		},
		"set that keeps the first digit": {
			// A Change leaves out the bytes at its ends that it keeps.
			text: stringtest.LinesLF("a: 2 # c"),
			fn:   set(paths.Current().Child("a"), 20),
			want: []edit.Change{change(0, 4, 0, 4, "0")},
		},
		"runes of more than one byte": {
			// A column counts runes.
			text: stringtest.LinesLF("a: été # é", "b: é"),
			fn:   set(paths.Current().Child("b"), "à"),
			want: []edit.Change{change(1, 3, 1, 4, "à")},
		},
		"line breaks of two bytes": {
			text: stringtest.LinesCRLF("a: 1", "b: 2"),
			fn:   set(paths.Current().Child("c"), 3),
			want: []edit.Change{change(2, 0, 2, 0, "c: 3\r\n")},
		},
		"new entry before a byte order mark of the next document": {
			// A Range cannot point between the line break and the mark, so
			// the Change inserts before the line break.
			text: stringtest.LinesLF("a: 1", "\ufeff---", "a: 2"),
			fn:   set(paths.Current().Child("b"), 2),
			want: []edit.Change{change(0, 4, 0, 4, "\nb: 2")},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			src := niceyaml.NewSourceFromString(tc.text)
			docs := src.Documents()
			require.Greater(t, len(docs), tc.doc)

			d, err := edit.New(docs[tc.doc])
			require.NoError(t, err)

			require.NoError(t, d.Apply(t.Context(), paths.Doc(), tc.fn))

			assert.Equal(t, tc.want, d.Changes())
			requireChanges(t, src, d)
		})
	}
}

func TestDoc_Changes_ByteOrderMark(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		text string
		// The index of the document in text.
		doc int
		fn  edit.Func
	}{
		"new entry before the mark of the next document": {
			text: stringtest.LinesLF("a: 1", "\ufeff---", "a: 2"),
			fn: func(ctx context.Context, n *edit.Node) error {
				return n.At(paths.Current().Child("b")).Set(ctx, 2)
			},
		},
		"removal of the line of a mark": {
			// The mark moves to the next line that the edits kept.
			text: stringtest.LinesLF("a: 1", "...", "# c", "\ufeffa: 2", "b: 3"),
			doc:  1,
			fn: func(_ context.Context, n *edit.Node) error {
				return n.At(paths.Current().Child("a")).Remove()
			},
		},
		"set on the line of a mark": {
			text: stringtest.LinesLF("a: 1", "...", "# c", "\ufeffa: 2"),
			doc:  1,
			fn: func(ctx context.Context, n *edit.Node) error {
				return n.At(paths.Current().Child("a")).Set(ctx, map[string]int{"x": 1})
			},
		},
		"mark after the end of a document": {
			text: stringtest.LinesLF("a: 1", "...", "\ufeff# c", "---", "a: 2"),
			fn: func(ctx context.Context, n *edit.Node) error {
				return n.At(paths.Current().Child("z")).Set(ctx, 3)
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			src := niceyaml.NewSourceFromString(tc.text)
			docs := src.Documents()
			require.Greater(t, len(docs), tc.doc)

			d, err := edit.New(docs[tc.doc])
			require.NoError(t, err)

			require.NoError(t, d.Apply(t.Context(), paths.Doc(), tc.fn))
			require.True(t, d.Edited())

			assert.NotEmpty(t, d.Changes())
			requireChanges(t, src, d)
		})
	}
}

func TestDoc_Changes_Applies(t *testing.T) {
	t.Parallel()

	src := niceyaml.NewSourceFromString(stringtest.LinesLF("a: 1", "b: 2"))
	d, err := edit.New(src.Documents()[0])
	require.NoError(t, err)

	setTo := func(v any) edit.Func {
		return func(ctx context.Context, n *edit.Node) error {
			return n.Set(ctx, v)
		}
	}

	// The Changes hold the net result of the Applies that passed.
	require.NoError(t, d.Apply(t.Context(), paths.Doc().Child("b"), setTo(200)))
	require.NoError(t, d.Apply(t.Context(), paths.Doc().Child("b"), setTo(30)))

	// A failed Apply leaves the Changes as it found them.
	err = d.Apply(t.Context(), paths.Doc().Child("a"), func(ctx context.Context, n *edit.Node) error {
		err := n.Set(ctx, 40)
		if err != nil {
			return err
		}

		return assert.AnError
	})
	require.Error(t, err)

	want := []edit.Change{change(1, 3, 1, 4, "30")}
	assert.Equal(t, want, d.Changes())

	// During an Apply, Changes reads the text after the last call that
	// passed.
	var during []edit.Change

	require.NoError(t, d.Apply(t.Context(), paths.Doc().ChildAll(), func(ctx context.Context, n *edit.Node) error {
		during = d.Changes()

		return n.Set(ctx, 5)
	}))

	assert.Equal(t, []edit.Change{change(0, 3, 0, 4, "5"), change(1, 3, 1, 4, "30")}, during)
	assert.Equal(t, []edit.Change{change(0, 3, 0, 4, "5"), change(1, 3, 1, 4, "5")}, d.Changes())
	requireChanges(t, src, d)
}
