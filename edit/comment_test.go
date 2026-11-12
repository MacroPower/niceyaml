package edit_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/edit"
	"go.jacobcolvin.com/niceyaml/paths"
)

func TestNode_HeadComment(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		before string
		path   paths.Path
		want   []string
	}{
		"lines above an entry": {
			before: stringtest.LinesLF("spec:", "  # one", "  #two", "  #", "  wait: 30s"),
			path:   spec.Child("wait"),
			want:   []string{"one", "two", ""},
		},
		"blank line ends the lines": {
			before: stringtest.LinesLF("spec:", "  # far", "", "  # near", "  wait: 30s"),
			path:   spec.Child("wait"),
			want:   []string{"near"},
		},
		"line in another column": {
			before: stringtest.LinesLF("spec:", "# other", "  wait: 30s"),
			path:   spec.Child("wait"),
		},
		"lines above an item": {
			before: stringtest.LinesLF("l:", "  - a", "  # note", "  - b"),
			path:   paths.MustParse("$.l[1]"),
			want:   []string{"note"},
		},
		"first entry of an item reads the lines of the item": {
			before: stringtest.LinesLF("l:", "  # item", "  - name: a", "    v: 1"),
			path:   paths.MustParse("$.l[0].name"),
			want:   []string{"item"},
		},
		"first entry of an item that a merge of an alias brings in": {
			before: stringtest.LinesLF(
				"z: 1", "s: &s", "  # item", "  - x: 1", "    w: 3", "# m", "m:", "  <<: *s", "  y: 2",
			),
			path: paths.MustParse("$.m.x"),
			want: []string{"item"},
		},
		"first entry of an item that an inline merge value brings in": {
			before: stringtest.LinesLF("z: 1", "# m", "m:", "  <<:", "    # item", "    - x: 1", "  y: 2"),
			path:   paths.MustParse("$.m.x"),
			want:   []string{"item"},
		},
		"lines at the top belong to the document": {
			before: stringtest.LinesLF("# doc", "a: 1"),
			path:   paths.MustParse("$.a"),
		},
		"place without a value": {
			before: stringtest.LinesLF("# doc", "a: 1"),
			path:   paths.MustParse("$.b"),
		},
		"root": {
			before: stringtest.LinesLF("# doc", "a: 1"),
			path:   paths.Doc(),
		},
		"entry of a flow mapping": {
			before: stringtest.LinesLF("a: 1", "b: {c: 1}"),
			path:   paths.MustParse("$.b.c"),
		},
		"line above an entry of a flow mapping of many lines": {
			before: stringtest.LinesLF("a: 1", "b: {", "  # x", "  c: 1}"),
			path:   paths.MustParse("$.b.c"),
		},
		"line above an item of a flow sequence of many lines": {
			before: stringtest.LinesLF("a: 1", "b: [", "  # x", "  c]"),
			path:   paths.MustParse("$.b[0]"),
		},
		"mapping on the line of the colon of an explicit key": {
			before: stringtest.LinesLF("? a", ": b: 1"),
			path:   paths.MustParse("$.a.b"),
		},
		"sequence on the line of the colon of an explicit key": {
			before: stringtest.LinesLF("? a", ": - x"),
			path:   paths.MustParse("$.a[0]"),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before)

			var got []string

			require.NoError(t, applyAt(t, d, tc.path, func(_ context.Context, n *edit.Node) error {
				got = n.HeadComment()

				return nil
			}))

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestNode_LineComment(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		before string
		path   paths.Path
		want   string
	}{
		"scalar": {
			before: stringtest.LinesLF("port: 80   # plain http", "b: 1"),
			path:   paths.MustParse("$.port"),
			want:   "plain http",
		},
		"without a space": {
			before: "port: 80 #http\n",
			path:   paths.MustParse("$.port"),
			want:   "http",
		},
		"none": {
			before: "port: 80\n",
			path:   paths.MustParse("$.port"),
		},
		"collection below its key": {
			before: stringtest.LinesLF("spec: # the spec", "  a: 1"),
			path:   spec,
			want:   "the spec",
		},
		"anchored collection below its key": {
			before: stringtest.LinesLF("spec: &s # the spec", "  a: 1"),
			path:   spec,
			want:   "the spec",
		},
		"block scalar": {
			before: stringtest.LinesLF("a: |- # text", "  x"),
			path:   paths.MustParse("$.a"),
			want:   "text",
		},
		"anchored block scalar": {
			before: stringtest.LinesLF("a: &x |- # text", "  x"),
			path:   paths.MustParse("$.a"),
			want:   "text",
		},
		"anchored scalar below its key": {
			before: stringtest.LinesLF("a: &x # c", "  text"),
			path:   paths.MustParse("$.a"),
			want:   "c",
		},
		"tagged scalar below its key": {
			before: stringtest.LinesLF("a: !!str # c", "  text"),
			path:   paths.MustParse("$.a"),
			want:   "c",
		},
		"anchored scalar below its dash": {
			before: stringtest.LinesLF("- &x # c", "  text"),
			path:   paths.MustParse("$[0]"),
			want:   "c",
		},
		"item holding a mapping reads its first entry": {
			before: stringtest.LinesLF("l:", "  - name: a # first", "    v: 1"),
			path:   paths.MustParse("$.l[0]"),
			want:   "first",
		},
		"item holding a sequence reads its first item": {
			before: stringtest.LinesLF("l:", "  - - a # c", "    - b"),
			path:   paths.MustParse("$.l[0]"),
			want:   "c",
		},
		"empty item": {
			before: stringtest.LinesLF("l:", "  - # empty"),
			path:   paths.MustParse("$.l[0]"),
			want:   "empty",
		},
		"flow mapping on the line of its key": {
			before: "a: {b: 1} # flow\n",
			path:   paths.MustParse("$.a"),
			want:   "flow",
		},
		"entry of a flow mapping": {
			before: "a: {b: 1} # flow\n",
			path:   paths.MustParse("$.a.b"),
		},
		"item of a flow sequence of many lines": {
			before: stringtest.LinesLF("a: 1", "b: [", "  c, # x", "  d # y", "  ]"),
			path:   paths.MustParse("$.b[1]"),
		},
		"entry of a flow mapping of many lines": {
			before: "b: {\n  c: 1, # x\n  d: 2 # y\n  }",
			path:   paths.MustParse("$.b.d"),
		},
		"item holding a sequence of a sequence reads its first item": {
			before: stringtest.LinesLF("l:", "  - - - a # c"),
			path:   paths.MustParse("$.l[0]"),
			want:   "c",
		},
		"item holding a sequence of a mapping reads its first entry": {
			before: stringtest.LinesLF("l:", "  - - a: 1 # c", "      b: 2"),
			path:   paths.MustParse("$.l[0]"),
			want:   "c",
		},
		"root": {
			before: "a: 1 # one\n",
			path:   paths.Doc(),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before)

			var got string

			require.NoError(t, applyAt(t, d, tc.path, func(_ context.Context, n *edit.Node) error {
				got = n.LineComment()

				return nil
			}))

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestNode_SetHeadComment(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		before string
		path   paths.Path
		lines  []string
		want   string
		err    string
		// The sentinel that the error matches, or nil.
		is error
	}{
		"add above an entry": {
			before: stringtest.LinesLF("spec:", "  wait: 30s"),
			path:   spec.Child("wait"),
			lines:  []string{"moved from spec.wait", ""},
			want:   stringtest.LinesLF("spec:", "  # moved from spec.wait", "  #", "  wait: 30s"),
		},
		"replace the lines": {
			before: stringtest.LinesLF("spec:", "  # old", "  #  two", "  wait: 30s"),
			path:   spec.Child("wait"),
			lines:  []string{"new"},
			want:   stringtest.LinesLF("spec:", "  # new", "  wait: 30s"),
		},
		"remove the lines": {
			before: stringtest.LinesLF("spec:", "  # far", "", "  # old", "  wait: 30s"),
			path:   spec.Child("wait"),
			want:   stringtest.LinesLF("spec:", "  # far", "", "  wait: 30s"),
		},
		"equal lines keep their bytes": {
			before: stringtest.LinesLF("spec:", "  #same", "  wait: 30s"),
			path:   spec.Child("wait"),
			lines:  []string{"same"},
			want:   stringtest.LinesLF("spec:", "  #same", "  wait: 30s"),
		},
		"item": {
			before: stringtest.LinesLF("l:", "  - a", "  - b"),
			path:   paths.MustParse("$.l[1]"),
			lines:  []string{"second"},
			want:   stringtest.LinesLF("l:", "  - a", "  # second", "  - b"),
		},
		"first entry of an item writes the lines of the item": {
			before: stringtest.LinesLF("l:", "  - name: a", "    v: 1"),
			path:   paths.MustParse("$.l[0].name"),
			lines:  []string{"item"},
			want:   stringtest.LinesLF("l:", "  # item", "  - name: a", "    v: 1"),
		},
		"first entry of the document": {
			before: stringtest.LinesLF("# doc", "a: 1"),
			path:   paths.MustParse("$.a"),
			lines:  []string{"a"},
			want:   stringtest.LinesLF("# doc", "# a", "a: 1"),
		},
		"CRLF line breaks": {
			before: "a: 1\r\nb: 2\r\n",
			path:   paths.MustParse("$.b"),
			lines:  []string{"b"},
			want:   "a: 1\r\n# b\r\nb: 2\r\n",
		},
		"after a block scalar": {
			before: stringtest.LinesLF("a:", "  s: |", "    text", "  b: 1"),
			path:   paths.MustParse("$.a.b"),
			lines:  []string{"b"},
			want:   stringtest.LinesLF("a:", "  s: |", "    text", "  # b", "  b: 1"),
		},
		"place without a value": {
			before: "a: 1\n",
			path:   paths.MustParse("$.b"),
			lines:  []string{"b"},
			want:   "a: 1\n",
		},
		"after a block scalar without text": {
			before: stringtest.LinesLF("a: |", "b: 1"),
			path:   paths.MustParse("$.b"),
			lines:  []string{"b"},
			want:   stringtest.LinesLF("a: |", "# b", "b: 1"),
		},
		"after a blank line of a block scalar without text": {
			// Go-yaml's parser refuses a comment line after the blank line.
			before: stringtest.LinesLF("a: |", "", "b: 1"),
			path:   paths.MustParse("$.b"),
			lines:  []string{"b"},
			err: "test.yaml:3:4: $.b: cafe.jacobcolvin.com/v1 sets the head comment of $.b, " +
				"whose comment lines go after the blank lines of a block scalar without text",
		},
		"after a line of spaces of a folded scalar without text": {
			before: stringtest.LinesLF("a: >-", "  ", "b: 1"),
			path:   paths.MustParse("$.b"),
			lines:  []string{"b"},
			err:    "whose comment lines go after the blank lines of a block scalar without text",
		},
		"after blank lines of a nested block scalar without text": {
			before: stringtest.LinesLF("m:", "  a: | # c", "", "", "  b: 1"),
			path:   paths.MustParse("$.m.b"),
			lines:  []string{"b"},
			err:    "whose comment lines go after the blank lines of a block scalar without text",
		},
		"removal after a blank line of a block scalar without text": {
			before: stringtest.LinesLF("a: |", "", "b: 1"),
			path:   paths.MustParse("$.b"),
			want:   stringtest.LinesLF("a: |", "", "b: 1"),
		},
		"after a line of spaces that a block scalar with an indent holds": {
			before: stringtest.LinesLF("a: |1", "  ", "b: 1"),
			path:   paths.MustParse("$.b"),
			lines:  []string{"b"},
			want:   stringtest.LinesLF("a: |1", "  ", "# b", "b: 1"),
		},
		"after a line of spaces that a kept block scalar holds": {
			before: stringtest.LinesLF("a: |+", "  ", "b: 1"),
			path:   paths.MustParse("$.b"),
			lines:  []string{"b"},
			want:   stringtest.LinesLF("a: |+", "  ", "# b", "b: 1"),
		},
		"after a line of spaces of a block scalar with an indent without text": {
			before: stringtest.LinesLF("a: |1", " ", "b: 1"),
			path:   paths.MustParse("$.b"),
			lines:  []string{"b"},
			err:    "whose comment lines go after the blank lines of a block scalar without text",
		},
		"after a blank line of a block scalar with text": {
			before: stringtest.LinesLF("a: |", "  x", "", "b: 1"),
			path:   paths.MustParse("$.b"),
			lines:  []string{"b"},
			want:   stringtest.LinesLF("a: |", "  x", "", "# b", "b: 1"),
		},
		"after a blank line of a block scalar without text below one with text": {
			before: stringtest.LinesLF("a: |", "  x", "b: |", "", "c: 1"),
			path:   paths.MustParse("$.c"),
			lines:  []string{"n"},
			err:    "whose comment lines go after the blank lines of a block scalar without text",
		},
		"after a blank line below a block scalar without text": {
			before: stringtest.LinesLF("a: |", "b: 1", "", "c: 1"),
			path:   paths.MustParse("$.c"),
			lines:  []string{"n"},
			want:   stringtest.LinesLF("a: |", "b: 1", "", "# n", "c: 1"),
		},
		"root": {
			before: "a: 1\n",
			path:   paths.Doc(),
			lines:  []string{"doc"},
			err:    "test.yaml:1:1: $: cafe.jacobcolvin.com/v1 sets a comment of the root of the document",
			is:     edit.ErrUsage,
		},
		"line break": {
			before: "a: 1\n",
			path:   paths.MustParse("$.a"),
			lines:  []string{"x\ny"},
			err:    `sets the head comment of $.a to "x\ny", which holds a line break`,
			is:     edit.ErrUsage,
		},
		"control character": {
			before: "a: 1\n",
			path:   paths.MustParse("$.a"),
			lines:  []string{"x\x01"},
			err:    "which holds U+0001 and YAML does not allow that in a text",
			is:     edit.ErrUsage,
		},
		"byte that is not UTF-8": {
			before: "a: 1\n",
			path:   paths.MustParse("$.a"),
			lines:  []string{"x\xff"},
			err: `sets the head comment of $.a to "x\xff", ` +
				"which holds the byte 0xff, which is not UTF-8, and YAML does not allow that in a text",
			is: edit.ErrUsage,
		},
		"entry of a flow mapping": {
			before: "a: {b: 1}\n",
			path:   paths.MustParse("$.a.b"),
			lines:  []string{"b"},
			err:    "sets the head comment of $.a.b, which lies inside a flow collection",
			is:     edit.ErrUnsupported,
		},
		"protected value": {
			before: stringtest.LinesLF("apiVersion: v1", "kind: Cafe"),
			path:   paths.MustParse("$.kind"),
			lines:  []string{"kind"},
			err:    "sets the head comment of $.kind, which is a protected value",
			is:     edit.ErrProtected,
		},
		"item of an item": {
			before: stringtest.LinesLF("l:", "  - - name: a"),
			path:   paths.MustParse("$.l[0][0].name"),
			lines:  []string{"outer"},
			want:   stringtest.LinesLF("l:", "  # outer", "  - - name: a"),
		},
		"through an alias": {
			// The lines go above the entry of the anchored value.
			before: stringtest.LinesLF("base: &b", "  x: 1", "m: *b"),
			path:   paths.MustParse("$.m.x"),
			lines:  []string{"x"},
			want:   stringtest.LinesLF("base: &b", "  # x", "  x: 1", "m: *b"),
		},
		"key that reads as another value": {
			before: stringtest.LinesLF("python:", "  3.10: x"),
			path:   paths.MustParse("$.python.'3.10'"),
			lines:  []string{"x"},
			err:    "a key that reads as 3.1 rather than as its text",
			is:     edit.ErrUnsupported,
		},
		"explicit key": {
			before: stringtest.LinesLF("a: 1", "? b", ": 2"),
			path:   paths.MustParse("$.b"),
			lines:  []string{"b"},
			err:    "which reaches an entry with an explicit key",
			is:     edit.ErrUnsupported,
		},
		"place without a value below an explicit key": {
			before: stringtest.LinesLF("a: 1", "? b", ": 2"),
			path:   paths.MustParse("$.b.c"),
			lines:  []string{"c"},
			err:    "which reaches an entry with an explicit key",
			is:     edit.ErrUnsupported,
		},
		"place without a value below a key that reads as another value": {
			before: stringtest.LinesLF("python:", "  3.10:", "    k: 1"),
			path:   paths.MustParse("$.python.'3.10'.z"),
			lines:  []string{"z"},
			err:    "a key that reads as 3.1 rather than as its text",
			is:     edit.ErrUnsupported,
		},
		"entry whose value holds an explicit key": {
			// The call reaches the entry and not its value, as SetKey does.
			before: stringtest.LinesLF("z: 1", "a:", "  ? k", "  : 1", "b: 1"),
			path:   paths.MustParse("$.a"),
			lines:  []string{"n"},
			want:   stringtest.LinesLF("z: 1", "# n", "a:", "  ? k", "  : 1", "b: 1"),
		},
		"mapping on the line of the colon of an explicit key": {
			before: stringtest.LinesLF("? a", ": b: 1"),
			path:   paths.MustParse("$.a.b"),
			lines:  []string{"x"},
			err:    "which reaches an entry with an explicit key",
			is:     edit.ErrUnsupported,
		},
		"removal on a mapping on the line of the colon of an explicit key": {
			before: stringtest.LinesLF("? a", ": b: 1"),
			path:   paths.MustParse("$.a.b"),
			err:    "which reaches an entry with an explicit key",
			is:     edit.ErrUnsupported,
		},
		"sequence on the line of the colon of an explicit key": {
			before: stringtest.LinesLF("? a", ": - x", "  - y"),
			path:   paths.MustParse("$.a[0]"),
			lines:  []string{"c"},
			err:    "which reaches an entry with an explicit key",
			is:     edit.ErrUnsupported,
		},
		"first entry of an item that a merge of an alias brings in": {
			// The lines go above the "-" of the item of the anchored value.
			before: stringtest.LinesLF(
				"z: 1", "s: &s", "  # old", "  - x: 1", "    w: 3", "# m", "m:", "  <<: *s", "  y: 2",
			),
			path:  paths.MustParse("$.m.x"),
			lines: []string{"new"},
			want: stringtest.LinesLF(
				"z: 1", "s: &s", "  # new", "  - x: 1", "    w: 3", "# m", "m:", "  <<: *s", "  y: 2",
			),
		},
		"first entry of an item that an inline merge value brings in": {
			before: stringtest.LinesLF("z: 1", "# m", "m:", "  <<:", "    # old", "    - x: 1", "  y: 2"),
			path:   paths.MustParse("$.m.x"),
			lines:  []string{"new"},
			err:    "which goes through a merge key that merges a sequence that holds a mapping",
			is:     edit.ErrUnsupported,
		},
		"entry beside a merge of a sequence of aliases": {
			// A SetKey of the entry changes the keys of the merge and
			// fails, but the comment changes no key.
			before: stringtest.LinesLF("x: &a {k: 1}", "m:", "  <<: [*a]", "  b: 3"),
			path:   paths.MustParse("$.m.b"),
			lines:  []string{"b"},
			want:   stringtest.LinesLF("x: &a {k: 1}", "m:", "  <<: [*a]", "  # b", "  b: 3"),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before)

			err := applyAt(t, d, tc.path, func(_ context.Context, n *edit.Node) error {
				return n.SetHeadComment(tc.lines...)
			})

			if tc.is != nil {
				require.ErrorIs(t, err, tc.is)
			}

			if tc.err != "" {
				require.NotErrorIs(t, err, edit.ErrEngine)
			}

			requireEdit(t, d, err, tc.err, tc.want)

			if tc.err == "" {
				assert.Equal(t, tc.before == tc.want, d.Changes() == nil)
			}
		})
	}
}

func TestNode_SetLineComment(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		before string
		path   paths.Path
		text   string
		want   string
		err    string
		is     error
		opts   []edit.Option
	}{
		"add after a scalar": {
			before: "port: 80   \nb: 1\n",
			path:   paths.MustParse("$.port"),
			text:   "plain http",
			want:   "port: 80 # plain http\nb: 1\n",
		},
		"replace keeps the spaces before the comment": {
			before: "port: 443    # plain http\n",
			path:   paths.MustParse("$.port"),
			text:   "tls",
			want:   "port: 443    # tls\n",
		},
		"remove": {
			before: "port: 443    # plain http\n",
			path:   paths.MustParse("$.port"),
			want:   "port: 443\n",
		},
		"equal text keeps the bytes": {
			before: "port: 443 #tls\n",
			path:   paths.MustParse("$.port"),
			text:   "tls",
			want:   "port: 443 #tls\n",
		},
		"empty text on a comment": {
			before: "port: 443 #\n",
			path:   paths.MustParse("$.port"),
			want:   "port: 443\n",
		},
		"collection below its key": {
			before: stringtest.LinesLF("spec:", "  a: 1"),
			path:   spec,
			text:   "the spec",
			want:   stringtest.LinesLF("spec: # the spec", "  a: 1"),
		},
		"anchored collection below its key": {
			before: stringtest.LinesLF("spec: &s", "  a: 1", "b: *s"),
			path:   spec,
			text:   "the spec",
			want:   stringtest.LinesLF("spec: &s # the spec", "  a: 1", "b: *s"),
		},
		"block scalar": {
			before: stringtest.LinesLF("a: |-", "  x"),
			path:   paths.MustParse("$.a"),
			text:   "text",
			want:   stringtest.LinesLF("a: |- # text", "  x"),
		},
		"add after the anchor of a scalar below its key": {
			before: stringtest.LinesLF("a: &x", "  text", "b: *x"),
			path:   paths.MustParse("$.a"),
			text:   "c",
			want:   stringtest.LinesLF("a: &x # c", "  text", "b: *x"),
		},
		"add after the tag of a scalar below its key": {
			before: stringtest.LinesLF("a: !!str", "  text"),
			path:   paths.MustParse("$.a"),
			text:   "c",
			want:   stringtest.LinesLF("a: !!str # c", "  text"),
		},
		"replace after the anchor of a scalar below its key": {
			before: stringtest.LinesLF("a: &x # c", "  text", "b: *x"),
			path:   paths.MustParse("$.a"),
			text:   "d",
			want:   stringtest.LinesLF("a: &x # d", "  text", "b: *x"),
		},
		"remove after the tag of a scalar below its key": {
			before: stringtest.LinesLF("a: !!str # c", "  text"),
			path:   paths.MustParse("$.a"),
			want:   stringtest.LinesLF("a: !!str", "  text"),
		},
		"replace after the anchor of a scalar below its dash": {
			before: stringtest.LinesLF("- &x # c", "  text", "- *x"),
			path:   paths.MustParse("$[0]"),
			text:   "d",
			want:   stringtest.LinesLF("- &x # d", "  text", "- *x"),
		},
		"remove after the anchor of a scalar below its dash": {
			before: stringtest.LinesLF("- &x # c", "  text", "- *x"),
			path:   paths.MustParse("$[0]"),
			want:   stringtest.LinesLF("- &x", "  text", "- *x"),
		},
		"empty value": {
			before: stringtest.LinesLF("a:", "b: 1"),
			path:   paths.MustParse("$.a"),
			text:   "empty",
			want:   stringtest.LinesLF("a: # empty", "b: 1"),
		},
		"item holding a mapping writes its first entry": {
			before: stringtest.LinesLF("l:", "  - name: a", "    v: 1"),
			path:   paths.MustParse("$.l[0]"),
			text:   "first",
			want:   stringtest.LinesLF("l:", "  - name: a # first", "    v: 1"),
		},
		"item holding a sequence writes its first item": {
			before: stringtest.LinesLF("l:", "  - - a # c", "    - b"),
			path:   paths.MustParse("$.l[0]"),
			text:   "x",
			want:   stringtest.LinesLF("l:", "  - - a # x", "    - b"),
		},
		"item holding a sequence removes the comment of its first item": {
			before: stringtest.LinesLF("l:", "  - - a # c", "    - b"),
			path:   paths.MustParse("$.l[0]"),
			want:   stringtest.LinesLF("l:", "  - - a", "    - b"),
		},
		"entry beside a merge of a sequence of aliases": {
			// A SetKey of the entry changes the keys of the merge and
			// fails, but the comment changes no key.
			before: stringtest.LinesLF("x: &a {k: 1}", "m:", "  <<: [*a]", "  b: 3"),
			path:   paths.MustParse("$.m.b"),
			text:   "b",
			want:   stringtest.LinesLF("x: &a {k: 1}", "m:", "  <<: [*a]", "  b: 3 # b"),
		},
		"last line without a line break": {
			before: "a: 1",
			path:   paths.MustParse("$.a"),
			text:   "one",
			want:   "a: 1 # one",
		},
		"tag that ends the text": {
			// Go-yaml drops the tag, but it reads the tag before a comment.
			before: "z: 1\na: !!str",
			path:   paths.MustParse("$.a"),
			text:   "d",
			err: "test.yaml:2:4: $.a: cafe.jacobcolvin.com/v1 sets the line comment of $.a, " +
				"whose tag ends the text without a line break",
		},
		"empty text on a tag that ends the text": {
			before: "z: 1\na: !!str",
			path:   paths.MustParse("$.a"),
			want:   "z: 1\na: !!str",
		},
		"remove after a tag at the end of the text": {
			// The line break keeps the tag that the comment kept.
			before: "z: 1\na: !!str # c",
			path:   paths.MustParse("$.a"),
			want:   "z: 1\na: !!str\n",
		},
		"remove after the tag of an item at the end of the text": {
			before: "z: 1\r\nl:\r\n  - !!str # c",
			path:   paths.MustParse("$.l[0]"),
			want:   "z: 1\r\nl:\r\n  - !!str\r\n",
		},
		"plain scalar of two lines": {
			before: stringtest.LinesLF("a: one", "  two"),
			path:   paths.MustParse("$.a"),
			text:   "x",
			err:    "sets the line comment of $.a, whose value goes on past the line of its key",
		},
		"place without a value": {
			before: "a: 1\n",
			path:   paths.MustParse("$.b"),
			text:   "b",
			want:   "a: 1\n",
		},
		"root": {
			before: "a: 1\n",
			path:   paths.Doc(),
			text:   "doc",
			is:     edit.ErrUsage,
			err:    "sets a comment of the root of the document",
		},
		"line break": {
			before: "a: 1\n",
			path:   paths.MustParse("$.a"),
			text:   "x\r",
			is:     edit.ErrUsage,
			err:    "which holds a line break",
		},
		"byte that is not UTF-8": {
			before: "a: 1\nb: 2\n",
			path:   paths.MustParse("$.b"),
			text:   "\xff",
			is:     edit.ErrUsage,
			err: `sets the line comment of $.b to "\xff", ` +
				"which holds the byte 0xff, which is not UTF-8, and YAML does not allow that in a text",
		},
		"entry of a flow mapping": {
			before: "a: {b: 1}\n",
			path:   paths.MustParse("$.a.b"),
			text:   "b",
			is:     edit.ErrUnsupported,
			err:    "test.yaml:1:4: $.a: cafe.jacobcolvin.com/v1 sets the line comment of $.a.b, which lies inside a flow collection",
		},
		"protected value": {
			before: stringtest.LinesLF("apiVersion: v1", "kind: Cafe"),
			path:   paths.MustParse("$.kind"),
			text:   "kind",
			is:     edit.ErrProtected,
			err:    "which is a protected value",
		},
		"protected first entry of an item": {
			before: stringtest.LinesLF("l:", "  - a: 1", "    b: 2"),
			path:   paths.MustParse("$.l[0]"),
			text:   "x",
			opts:   []edit.Option{edit.WithProtected(paths.MustParse("$.l[0].a"))},
			is:     edit.ErrProtected,
			err:    "sets the line comment of $.l[0], whose comment belongs to $.l[0].a, which is a protected value",
		},
		"empty text on a plain scalar of two lines": {
			before: stringtest.LinesLF("a: one", "  two"),
			path:   paths.MustParse("$.a"),
			want:   stringtest.LinesLF("a: one", "  two"),
		},
		"key that reads as another value": {
			before: stringtest.LinesLF("python:", "  3.10: x"),
			path:   paths.MustParse("$.python.'3.10'"),
			text:   "x",
			is:     edit.ErrUnsupported,
			err:    "a key that reads as 3.1 rather than as its text",
		},
		"item whose first entry has a key that reads as another value": {
			before: stringtest.LinesLF("l:", "  - 3.10: x"),
			path:   paths.MustParse("$.l[0]"),
			text:   "x",
			is:     edit.ErrUnsupported,
			err:    "a key that reads as 3.1 rather than as its text",
		},
		"explicit key on the line of its value": {
			before: stringtest.LinesLF("a: 1", "? b : 2"),
			path:   paths.MustParse("$.b"),
			text:   "b",
			is:     edit.ErrUnsupported,
			err:    "which reaches an entry with an explicit key",
		},
		"explicit key above its value": {
			before: stringtest.LinesLF("a: 1", "? b", ": 2"),
			path:   paths.MustParse("$.b"),
			text:   "b",
			is:     edit.ErrUnsupported,
			err:    "which reaches an entry with an explicit key",
		},
		"place without a value below an explicit key": {
			before: stringtest.LinesLF("a: 1", "? b", ": 2"),
			path:   paths.MustParse("$.b.c"),
			text:   "c",
			is:     edit.ErrUnsupported,
			err:    "which reaches an entry with an explicit key",
		},
		"place without a value below a key that reads as another value": {
			before: stringtest.LinesLF("python:", "  3.10:", "    k: 1"),
			path:   paths.MustParse("$.python.'3.10'.z"),
			text:   "z",
			is:     edit.ErrUnsupported,
			err:    "a key that reads as 3.1 rather than as its text",
		},
		"entry whose value holds an explicit key": {
			// The call reaches the entry and not its value, as SetKey does.
			before: stringtest.LinesLF("z: 1", "a:", "  ? k", "  : 1", "b: 1"),
			path:   paths.MustParse("$.a"),
			text:   "n",
			want:   stringtest.LinesLF("z: 1", "a: # n", "  ? k", "  : 1", "b: 1"),
		},
		"item holding a sequence of a mapping writes its first entry": {
			before: stringtest.LinesLF("l:", "  - - a: 1 # c", "      b: 2"),
			path:   paths.MustParse("$.l[0]"),
			text:   "d",
			want:   stringtest.LinesLF("l:", "  - - a: 1 # d", "      b: 2"),
		},
		"remove a comment that ends the text after an empty value": {
			before: "b: 1\na: #",
			path:   paths.MustParse("$.a"),
			want:   "b: 1\na:",
		},
		"remove a comment that ends the text after a scalar": {
			before: "b: 1\na: x #",
			path:   paths.MustParse("$.a"),
			want:   "b: 1\na: x",
		},
		"empty text on an explicit key above its value": {
			before: stringtest.LinesLF("a: 1", "? b", ": 2"),
			path:   paths.MustParse("$.b"),
			want:   stringtest.LinesLF("a: 1", "? b", ": 2"),
		},
		"item whose first entry has an explicit key": {
			before: stringtest.LinesLF("l:", "  - ? a", "    : 1"),
			path:   paths.MustParse("$.l[0]"),
			text:   "x",
			is:     edit.ErrUnsupported,
			err:    "which reaches an entry with an explicit key",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, d := newSourceDocWith(t, tc.before, tc.opts)

			err := applyAt(t, d, tc.path, func(_ context.Context, n *edit.Node) error {
				return n.SetLineComment(tc.text)
			})

			if tc.is != nil {
				require.ErrorIs(t, err, tc.is)
			}

			requireEdit(t, d, err, tc.err, tc.want)
		})
	}
}

func TestNode_SetLineComment_AfterRemove(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		write func(n *edit.Node) error
		err   string
	}{
		"head comment": {
			write: func(n *edit.Node) error { return n.SetHeadComment("h") },
			err:   "sets the head comment of $.b through a node that lost its place to an earlier edit",
		},
		"line comment": {
			write: func(n *edit.Node) error { return n.SetLineComment("l") },
			err:   "sets the line comment of $.b through a node that lost its place to an earlier edit",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			before := stringtest.LinesLF("a: 1", "b: 2")
			d := newDoc(t, before)

			err := applyAt(t, d, paths.MustParse("$.b"), func(_ context.Context, n *edit.Node) error {
				err := n.Remove()
				if err != nil {
					return err
				}

				return tc.write(n)
			})

			requireEdit(t, d, err, tc.err, "")
			assert.Equal(t, before, d.Text())
		})
	}
}

func TestNode_SetHeadComment_Func(t *testing.T) {
	t.Parallel()

	d := newDoc(t, stringtest.LinesLF("spec:", "  wait: 30s # seconds"))

	fn := func(_ context.Context, n *edit.Node) error {
		err := n.SetKey("timeout")
		if err != nil {
			return err
		}

		if n.LineComment() == "seconds" {
			err = n.SetLineComment("a Go duration")
			if err != nil {
				return err
			}
		}

		if len(n.HeadComment()) > 0 {
			return nil
		}

		return n.SetHeadComment("moved from spec.wait by v2")
	}

	require.NoError(t, d.Apply(t.Context(), spec.Child("wait"), fn, label))

	want := stringtest.LinesLF("spec:", "  # moved from spec.wait by v2", "  timeout: 30s # a Go duration")
	assert.Equal(t, want, d.Text())

	// The Func adds the note once.
	require.NoError(t, d.Apply(t.Context(), spec.Child("timeout"), func(_ context.Context, n *edit.Node) error {
		if len(n.HeadComment()) > 0 {
			return nil
		}

		return n.SetHeadComment("again")
	}, label))

	assert.Equal(t, want, d.Text())
}

func TestNode_SetLineComment_AfterSets(t *testing.T) {
	t.Parallel()

	// The Sets of the wildcard wait for the end of the Apply, and the
	// comments of each call read and write the text after them.
	text := stringtest.LinesLF("ports:", "  a: 80 # http", "  b: 81", "  c: 82 # http")
	path := paths.MustParse("$.ports.*")

	run := func(d *edit.Doc) error {
		return d.Apply(t.Context(), path, func(ctx context.Context, n *edit.Node) error {
			err := n.Set(ctx, 443)
			if err != nil {
				return err
			}

			if n.LineComment() == "http" {
				return n.SetLineComment("https")
			}

			return nil
		}, label)
	}

	d := newDoc(t, text)
	err := run(d)

	require.NoError(t, err)
	assert.Equal(t, stringtest.LinesLF("ports:", "  a: 443 # https", "  b: 443", "  c: 443 # https"), d.Text())
	requireEachCall(t, d, err, text, nil, run)
}

func TestNode_LineComment_BeforeSets(t *testing.T) {
	t.Parallel()

	// Each call reads the comment before its Set, so the read finds no
	// waiting Set of its value, and the Sets wait for the end of the Apply.
	text := stringtest.LinesLF("ports:", "  # web", "  a: 80 # http", "  b: 81", "  c: 82 # http")
	path := paths.MustParse("$.ports.*")

	run := func(d *edit.Doc) error {
		return d.Apply(t.Context(), path, func(ctx context.Context, n *edit.Node) error {
			if n.LineComment() != "http" && len(n.HeadComment()) == 0 {
				return nil
			}

			return n.Set(ctx, 443)
		}, label)
	}

	d := newDoc(t, text)
	err := run(d)

	require.NoError(t, err)
	assert.Equal(t, stringtest.LinesLF("ports:", "  # web", "  a: 443 # http", "  b: 81", "  c: 443 # http"), d.Text())
	requireEachCall(t, d, err, text, nil, run)
}
