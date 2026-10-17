package edit_test

import (
	"context"
	"errors"
	"fmt"
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

func TestNode_SetKey(t *testing.T) {
	t.Parallel()

	// The text of chain(n) holds a0 and n mappings after it, each of which
	// merges the one before it twice, and a mapping m that merges the last
	// of them. A decode reads the entries of a0 again at each merge, so
	// the text passes the alias limit for n of 10.
	chain := func(n int) string {
		lines := []string{"a0: &a0 {k: 1}"}
		for i := 1; i <= n; i++ {
			lines = append(lines, fmt.Sprintf("a%d: &a%d {<<: [*a%d, *a%d]}", i, i, i-1, i-1))
		}

		return stringtest.LinesLF(append(lines, fmt.Sprintf("m: {<<: *a%d, x: 2}", n))...)
	}

	tcs := map[string]struct {
		before string
		// The text of a reference document of the document, or "".
		ref  string
		path paths.Path
		key  string
		want string
		err  string
		// Whether the Source allows duplicate keys, whether it turns the
		// alias limit off, and whether the error matches edit.ErrEngine.
		dup, unlimited, engine bool
	}{
		"rename to a key that a chain of 9 merges brings in": {
			// Control.
			before:    chain(9),
			unlimited: true,
			path:      paths.MustParse("$.m.x"),
			key:       "k",
			err:       `test.yaml:11:5: $.m: cafe.jacobcolvin.com/v1 renames $.m.x to "k", which changes the keys of a merge through a merge key that merges a mapping with a merge key of its own, and cafe does not change the keys of such a merge`,
		},
		"rename to a key that a chain of 10 merges brings in": {
			// The merge check reads the mapping whatever share of its reads
			// the aliases make.
			before:    chain(10),
			unlimited: true,
			path:      paths.MustParse("$.m.x"),
			key:       "k",
			err:       `test.yaml:12:5: $.m: cafe.jacobcolvin.com/v1 renames $.m.x to "k", which changes the keys of a merge through a merge key that merges a mapping with a merge key of its own, and cafe does not change the keys of such a merge`,
		},
		"block scalar key without a value": {
			// The key spans the lines of the block scalar.
			before: stringtest.LinesLF("a:", "  ? |", "    x", "b: 1"),
			path:   paths.MustParse(`$.a.'x\n'`),
			key:    "z",
			err:    "test.yaml:2:3: $.a.'x\\n': cafe.jacobcolvin.com/v1 renames $.a.'x\\n' to \"z\", which reaches an entry with an explicit key, and cafe does not edit such an entry other than by removing it",
		},
		"block entry keeps its comment": {
			before: stringtest.LinesLF(
				"spec:",
				"  cost: 45 # kr",
				"  tags: vegan",
			),
			path: spec.Child("cost"),
			key:  "price",
			want: stringtest.LinesLF(
				"spec:",
				"  price: 45 # kr",
				"  tags: vegan",
			),
		},
		"quoted key keeps its quotes": {
			before: stringtest.LinesLF("spec:", "  'cost': 45"),
			path:   spec.Child("cost"),
			key:    "price",
			want:   stringtest.LinesLF("spec:", "  'price': 45"),
		},
		"key that reads as another value": {
			before: stringtest.LinesLF("spec:", "  cost: 45"),
			path:   spec.Child("cost"),
			key:    "null",
			want:   stringtest.LinesLF("spec:", `  "null": 45`),
		},
		"key that holds the text of a null value": {
			before: stringtest.LinesLF("spec:", "  cost: 45"),
			path:   spec.Child("cost"),
			key:    "price: null",
			want:   stringtest.LinesLF("spec:", `  "price: null": 45`),
		},
		"flow key with a flow indicator": {
			before: "spec: {a: 5}\n",
			path:   spec.Child("a"),
			key:    "a,b",
			want:   "spec: {\"a,b\": 5}\n",
		},
		"same key": {
			before: stringtest.LinesLF("spec:", "  cost: 45"),
			path:   spec.Child("cost"),
			key:    "cost",
			want:   stringtest.LinesLF("spec:", "  cost: 45"),
		},
		"missing place": {
			before: stringtest.LinesLF("spec:", "  cost: 45"),
			path:   spec.Child("price"),
			key:    "cost",
			want:   stringtest.LinesLF("spec:", "  cost: 45"),
		},
		"key that exists": {
			before: stringtest.LinesLF(
				"spec:",
				"  extras:",
				"    oat_milk: 4",
				"    oatMilk: 5",
			),
			path: spec.Child("extras", "oatMilk"),
			key:  "oat_milk",
			err: stringtest.JoinLF(
				`test.yaml:4:5: $.spec.extras.oatMilk~: cafe.jacobcolvin.com/v1 renames "oatMilk" to "oat_milk", `+
					"which already exists",
				`|-- 3:5: $.spec.extras.oat_milk~: "oat_milk" is defined here`,
				"|-- remove one of the two",
				"`-- cafe.jacobcolvin.com/v1 renames $.spec.extras.oatMilk to \"oat_milk\"",
			),
		},
		"item": {
			before: stringtest.LinesLF("spec:", "  - a"),
			path:   spec.Index(0),
			key:    "b",
			err:    "cafe.jacobcolvin.com/v1 renames this item, but an item of a sequence has no key",
		},
		"root mapping": {
			// The root is no item, so the problem names the root.
			before: stringtest.LinesLF("a: 1"),
			path:   paths.Doc(),
			key:    "x",
			err:    "test.yaml:1:1: $: cafe.jacobcolvin.com/v1 renames the root of the document, which has no key",
		},
		"root scalar": {
			before: stringtest.LinesLF("x"),
			path:   paths.Doc(),
			key:    "x",
			err:    "test.yaml:1:1: $: cafe.jacobcolvin.com/v1 renames the root of the document, which has no key",
		},
		"root flow mapping": {
			before: stringtest.LinesLF("{a: 1}"),
			path:   paths.Doc(),
			key:    "x",
			err:    "test.yaml:1:2: $: cafe.jacobcolvin.com/v1 renames the root of the document, which has no key",
		},
		"root sequence": {
			before: stringtest.LinesLF("- 1"),
			path:   paths.Doc(),
			key:    "x",
			err:    "test.yaml:1:3: $: cafe.jacobcolvin.com/v1 renames the root of the document, which has no key",
		},
		"rename to a key that a quoted key of the root reads as": {
			// The quoted key reads its own text.
			before: stringtest.LinesLF(`"a": 1`, "z: 3"),
			path:   paths.Doc().Child("z"),
			key:    "a",
			err:    `test.yaml:2:1: $.z~: cafe.jacobcolvin.com/v1 renames "z" to "a", which already exists`,
		},
		"rename of a key that the text spells and the decode reads": {
			// The text holds the key already, so the rename leaves the text.
			before: stringtest.LinesLF("m:", `  "16": 10`, "  x: 1"),
			path:   paths.MustParse("$.m.16"),
			key:    "16",
			want:   stringtest.LinesLF("m:", `  "16": 10`, "  x: 1"),
		},
		"rename to a key that a merge key brings in through an alias key of a reference document": {
			// The reference document decides which names m reads, so the rename
			// fails whatever the alias key *x of the reference document reads.
			before: "x: &x d\nm: {<<: *r, q: 2}\n",
			ref:    "r: &r {*x : 1}\nx: &x k\n",
			path:   paths.MustParse("$.m.q"),
			key:    "k",
			err: `test.yaml:2:13: $.m.q: cafe.jacobcolvin.com/v1 renames $.m.q to "k", but <<: *r at 2:5 reads ` +
				`a reference document, which decides which names $.m reads`,
		},
		"rename in a merge source to a key that the merging mapping holds after its merge key": {
			// The own entry c of m wins over the c that the merge key brings
			// in, before the rename and after it.
			before: "s: &s {z: 1}\nm: {<<: *s, c: 2}\n",
			path:   paths.MustParse("$.s.z"),
			key:    "c",
			want:   "s: &s {c: 1}\nm: {<<: *s, c: 2}\n",
		},
		"rename to the key of the document anchor that an alias key of a reference document does not read": {
			// The engine does not read which names the reference document
			// holds, so this rename fails too.
			before: "x: &x d\nm: {<<: *r, q: 2}\n",
			ref:    "r: &r {*x : 1}\nx: &x k\n",
			path:   paths.MustParse("$.m.q"),
			key:    "d",
			err: `test.yaml:2:13: $.m.q: cafe.jacobcolvin.com/v1 renames $.m.q to "d", but <<: *r at 2:5 reads ` +
				`a reference document, which decides which names $.m reads`,
		},

		"rename of an explicit key in a mapping that merges a mapping written in place": {
			// The rename changes the keys of the merge at the root, a step
			// before the explicit key.
			before: stringtest.LinesLF("<<: {b: 1}", "? r1", ": 2"),
			path:   paths.MustParse("$.r1"),
			key:    "x",
			err: `test.yaml:1:1: $: cafe.jacobcolvin.com/v1 renames $.r1 to "x", which changes the keys of a merge ` +
				"through a merge key that merges a mapping written in place, and cafe does not change the keys of " +
				"such a merge",
		},
		"rename of an explicit key in a nested mapping that merges a mapping written in place": {
			before: stringtest.LinesLF("m:", "  <<: {b: 1}", "  ? r1", "  : 2"),
			path:   paths.MustParse("$.m.r1"),
			key:    "x",
			err: `test.yaml:2:3: $.m: cafe.jacobcolvin.com/v1 renames $.m.r1 to "x", which changes the keys of a ` +
				"merge through a merge key that merges a mapping written in place",
		},
		"rename of an explicit key before a merge key that merges a mapping written in place": {
			// Control: the rename meets the explicit key and the merge at the
			// step that looks r1 up in m, and the explicit key comes first in
			// the text.
			before: stringtest.LinesLF("m:", "  ? r1", "  : 2", "  <<: {b: 1}"),
			path:   paths.MustParse("$.m.r1"),
			key:    "x",
			err: `test.yaml:2:3: $.m.r1: cafe.jacobcolvin.com/v1 renames $.m.r1 to "x", which reaches an entry ` +
				"with an explicit key",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := []niceyaml.SourceOption{
				niceyaml.WithName("test.yaml"), niceyaml.WithAllowDuplicateKeys(tc.dup),
				niceyaml.WithAliasLimit(!tc.unlimited),
			}
			if tc.ref != "" {
				opts = append(opts, niceyaml.WithReferences(niceyaml.NewSourceFromString(tc.ref)))
			}

			d, err := edit.New(yamltest.FirstDocument(t, tc.before, opts...), edit.WithApp("cafe"))
			require.NoError(t, err)

			err = applyAt(t, d, tc.path, func(_ context.Context, n *edit.Node) error {
				return n.SetKey(tc.key)
			})

			// An error of the table is a problem of the rename that the user
			// asks for, unless go-yaml refuses the text of the rename.
			if tc.err != "" && !tc.engine {
				require.NotErrorIs(t, err, edit.ErrEngine)
			}

			if tc.engine {
				require.ErrorIs(t, err, edit.ErrEngine)
			}

			requireEdit(t, d, err, tc.err, tc.want)
		})
	}
}

func TestNode_SetKey_Kept(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		fn     edit.Func
		set    any
		before string
		apply  string
		refs   []string
		dup    bool
	}{}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := []niceyaml.SourceOption{niceyaml.WithName("test.yaml"), niceyaml.WithAllowDuplicateKeys(tc.dup)}
			for _, ref := range tc.refs {
				opts = append(opts, niceyaml.WithReferences(niceyaml.NewSourceFromString(ref)))
			}

			d, err := edit.New(yamltest.FirstDocument(t, tc.before, opts...), edit.WithApp("cafe"))
			require.NoError(t, err)

			path := paths.MustParse(tc.apply)

			if tc.set != nil {
				require.NoError(t, applyAt(t, d, path, func(ctx context.Context, n *edit.Node) error {
					return n.Set(ctx, tc.set)
				}))
			}

			want := d.Text()

			require.NoError(t, d.Apply(t.Context(), path, tc.fn, label))
			assert.Equal(t, want, d.Text())
		})
	}
}

func TestNode_Remove(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		before string
		// The text of a reference document of the document, or "".
		ref  string
		path paths.Path
		want string
		err  string
		// The value that the text decodes to, when set.
		value any
		// Whether the error matches edit.ErrEngine.
		engine bool
	}{
		"last entry of a mapping under a << key with a tab after its colon": {
			// Go-yaml reads the << key before ":" and a tab as a string key,
			// so the "{}" goes after the tab, which stays.
			before: stringtest.LinesLF("<<:\t", "  x: q"),
			path:   paths.MustParse("$.<<.x"),
			want:   "<<:\t{}\n",
		},
		"last entry of a mapping under a << key with a tab and a comment after its colon": {
			before: stringtest.LinesLF("<<:\t# c", "  x: q"),
			path:   paths.MustParse("$.<<.x"),
			want:   "<<:\t{} # c\n",
		},
		"last item of a sequence under a key with a tab after its colon": {
			before: stringtest.LinesLF("k:\t", "  - 1"),
			path:   paths.MustParse("$.k[0]"),
			want:   "k:\t[]\n",
		},
		"last entry of an anchored mapping with a tab after its anchor": {
			// Go-yaml's lexer reads a tab after an anchor into its name, so
			// the "{}" takes a space after the anchor, and the tab stays after
			// it.
			before: stringtest.LinesLF("a: &x\t", "  y: 1", "b: *x"),
			path:   paths.MustParse("$.a.y"),
			want:   stringtest.LinesLF("a: &x {}\t", "b: *x"),
		},
		"last item of an anchored sequence with a tab after its anchor": {
			before: stringtest.LinesLF("a: &x\t", "  - 1", "b: *x"),
			path:   paths.MustParse("$.a[0]"),
			want:   stringtest.LinesLF("a: &x []\t", "b: *x"),
		},
		"entry below a scalar under a local tag": {
			// A scalar holds no value below it, whatever its tag.
			before: "a: !t x\n",
			path:   paths.MustParse("$.a.k"),
			want:   "a: !t x\n",
		},
		"entry of the value of a merge key that an alias of an explicit key reads": {
			// The step of r3 meets the explicit key and the value of the merge
			// key through the alias, and the merge key comes first in the text.
			before: stringtest.LinesLF("<<: &x0 {b: 2}", "? r3", ": *x0"),
			path:   paths.MustParse("$.r3.b"),
			err:    "test.yaml:1:1: $: cafe.jacobcolvin.com/v1 removes $.r3.b, which goes through a merge key whose value has an anchor",
		},
		"entry before a merge list of a mapping and of the merge value inside it": {
			// The decoder does not find an anchor inside the value of a merge
			// key, but the check reads x1 as the decode does, so p reads the d
			// that both merge values bring in.
			before: stringtest.LinesLF("r0: &x0", "  <<: &x1 {d: q}", "p:", "  <<: [*x0, *x1]", "  d: null"),
			path:   paths.MustParse("$.p.d"),
			err:    "test.yaml:4:3: $.p: cafe.jacobcolvin.com/v1 removes $.p.d, which changes the keys of a merge through a merge key that merges a sequence of aliases, and cafe does not change the keys of such a merge",
		},
		"entry of a block scalar key without a value": {
			before: stringtest.LinesLF("a:", "  ? |", "    x", "  k: 1", "b: 1"),
			path:   paths.MustParse(`$.a.'x\n'`),
			want:   stringtest.LinesLF("a:", "  k: 1", "b: 1"),
		},
		"anchored block scalar whose copy goes before a deeper comment line": {
			// The removal takes the anchor that the alias reads.
			before: stringtest.LinesLF("a: &x |-", "  l1", "  l2", "b: *x", "  # note", "c: 1"),
			path:   paths.Doc().Child("a"),
			err:    "test.yaml:4:4: $.b: cafe.jacobcolvin.com/v1 removes $.a, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored block scalar with an indentation indicator whose copy goes before a deeper comment line": {
			before: stringtest.LinesLF("a: &x >-2", "   l1", "  l2", "b: *x", "    # note", "c: 1"),
			path:   paths.Doc().Child("a"),
			err:    "test.yaml:4:4: $.b: cafe.jacobcolvin.com/v1 removes $.a, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"entry with its comments": {
			before: stringtest.LinesLF(
				"spec:",
				"  # Matches the brand palette.",
				"  color: red # or a hex code",
				"  price: 45",
			),
			path: spec.Child("color"),
			want: stringtest.LinesLF("spec:", "  price: 45"),
		},
		"entry after a block scalar with keep chomping and its blank line": {
			before: stringtest.LinesLF("a: |+", "  w", "", "b: x"),
			path:   paths.Doc().Child("b"),
			want:   stringtest.LinesLF("a: |+", "  w", ""),
			value:  map[string]any{"a": "w\n\n"},
		},
		"item after a block scalar with keep chomping and its blank line": {
			before: stringtest.LinesLF("r1:", "  - |+", "      w", "", "  - x"),
			path:   paths.MustParse("$.r1[1]"),
			want:   stringtest.LinesLF("r1:", "  - |+", "      w", ""),
			value:  map[string]any{"r1": []any{"w\n\n"}},
		},
		"entry before a blank line that a block scalar above it would hold": {
			// The line of spaces holds more spaces than the lines of a, so the
			// parser would read it as a line of a. It stays as an empty line.
			before: stringtest.LinesLF("a: |", "  x", "b: 1", "   ", "c: 2"),
			path:   paths.Doc().Child("b"),
			want:   stringtest.LinesLF("a: |", "  x", "", "c: 2"),
			value:  map[string]any{"a": "x\n", "c": uint64(2)},
		},
		"entry after a block scalar with keep chomping, an indentation indicator, and its blank line": {
			// Go-yaml's parser refuses a text that ends with an empty line
			// after such a scalar.
			before: stringtest.LinesLF("a: |+2", "    x", "", "b: 1"),
			path:   paths.Doc().Child("b"),
			err: "test.yaml:2:5: $.a: cafe.jacobcolvin.com/v1 removes $.b, and go-yaml's parser refuses the " +
				"edited text: invalid number of indent is specified in the multi-line header",
			engine: true,
		},
		"one of the blank lines around it": {
			before: stringtest.LinesLF(
				"a: 1",
				"",
				"b: 2",
				"",
				"c: 3",
			),
			path: paths.Doc().Child("b"),
			want: stringtest.LinesLF(
				"a: 1",
				"",
				"c: 3",
			),
		},
		"last entry of the root before a comment line": {
			// The root holds the comment line, so the problem shows $ (fix25
			// G25-2).
			before: stringtest.LinesLF("a: 1", "# tail"),
			path:   paths.Doc().Child("a"),
			err: "test.yaml:2:1: $: go-yaml's parser refuses the edited text with its comments: value is not allowed in this context\n" +
				"`-- report this to the authors of cafe",
			engine: true,
		},
		"last item of the root before a comment line": {
			// The root holds the comment line, so the problem shows $ (fix25
			// G25-2).
			before: stringtest.LinesLF("- 1", "# tail"),
			path:   paths.Doc().Index(0),
			err: "test.yaml:2:1: $: go-yaml's parser refuses the edited text with its comments: value is not allowed in this context\n" +
				"`-- report this to the authors of cafe",
			engine: true,
		},
		"last entry of the root before a comment line, in a text of carriage returns and line feeds": {
			// The root holds the comment line, so the problem shows $ (fix25
			// G25-2).
			before: stringtest.LinesCRLF("a: 1", "# tail"),
			path:   paths.Doc().Child("a"),
			err:    "2:1: $: go-yaml's parser refuses the edited text with its comments: value is not allowed in this context",
			engine: true,
		},
		"last item of the root before a comment line, in a text of carriage returns and line feeds": {
			// The root holds the comment line, so the problem shows $ (fix25
			// G25-2).
			before: stringtest.LinesCRLF("- 1", "# tail"),
			path:   paths.Doc().Index(0),
			err:    "2:1: $: go-yaml's parser refuses the edited text with its comments: value is not allowed in this context",
			engine: true,
		},
		"anchored single-quoted scalar of several lines with an empty line, below a deeper alias": {
			// The removal takes the anchor that the alias reads.
			before: stringtest.LinesLF("b: &x0 'p", "", "  q'", "a:", "  d: *x0"),
			path:   paths.Doc().Child("b"),
			err:    "test.yaml:5:6: $.a.d: cafe.jacobcolvin.com/v1 removes $.b, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored double-quoted scalar of several lines with an empty line, below a deeper alias": {
			before: stringtest.LinesLF(`b: &x0 "p`, "", `  q"`, "a:", "  d: *x0"),
			path:   paths.Doc().Child("b"),
			err:    "test.yaml:5:6: $.a.d: cafe.jacobcolvin.com/v1 removes $.b, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored quoted scalar of several lines with an empty line, in a sequence": {
			before: stringtest.LinesLF("s:", "  - &x0 'p", "", "    q'", "  - *x0"),
			path:   paths.MustParse("$.s[0]"),
			err:    "test.yaml:5:5: $.s[1]: cafe.jacobcolvin.com/v1 removes $.s[0], which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored quoted scalar of several lines with an empty line, below an alias in a deeper mapping": {
			before: stringtest.LinesLF("x:", "  k: 1", "  y: &x0 'p", "", "    q'", "z:", "  w:", "    d: *x0"),
			path:   paths.MustParse("$.x.y"),
			err:    "test.yaml:8:8: $.z.w.d: cafe.jacobcolvin.com/v1 removes $.x.y, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored quoted scalar of several lines with an empty line, below a mapping whose alias is at the root": {
			before: stringtest.LinesLF("x:", "  b: &x 'p", "", "    q'", "a: *x"),
			path:   paths.Doc().Child("x"),
			err:    "test.yaml:5:4: $.a: cafe.jacobcolvin.com/v1 removes $.x, which holds the anchor &x that this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored block mapping with a key that holds a colon, whose alias is in a flow sequence": {
			// The removal takes the anchor that the alias reads.
			before: stringtest.LinesLF("z: &z", "  x:y: 2", "w: [*z]"),
			path:   paths.Doc().Child("z"),
			err:    "test.yaml:3:5: $.w[0]: cafe.jacobcolvin.com/v1 removes $.z, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"blank line before an entry in the middle stays": {
			before: stringtest.LinesLF("a: 1", "", "b: 2", "c: 3"),
			path:   paths.Doc().Child("b"),
			want:   stringtest.LinesLF("a: 1", "", "c: 3"),
		},
		"last entry after a blank line": {
			before: stringtest.LinesLF("a:", "  b: 1", "", "wifi: x"),
			path:   paths.Doc().Child("wifi"),
			want:   stringtest.LinesLF("a:", "  b: 1"),
		},
		"last entry after a blank line without a final newline": {
			before: stringtest.JoinLF("a:", "  b: 1", "", "wifi: x"),
			path:   paths.Doc().Child("wifi"),
			want:   stringtest.JoinLF("a:", "  b: 1"),
		},
		"last entry after a block scalar with strip chomping, without a final newline": {
			before: stringtest.JoinLF("a: |-", "  x", "b: 1"),
			path:   paths.Doc().Child("b"),
			want:   stringtest.JoinLF("a: |-", "  x"),
		},
		"last entry after a blank line with CRLF line endings": {
			before: stringtest.LinesCRLF("a:", "  b: 1", "", "wifi: x"),
			path:   paths.Doc().Child("wifi"),
			want:   stringtest.LinesCRLF("a:", "  b: 1"),
		},
		"last entry after two blank lines": {
			before: stringtest.LinesLF("a: 1", "", "", "wifi: x"),
			path:   paths.Doc().Child("wifi"),
			want:   stringtest.LinesLF("a: 1"),
		},
		"last entry before a document end marker": {
			before: stringtest.LinesLF("a: 1", "", "b: 2", "..."),
			path:   paths.Doc().Child("b"),
			want:   stringtest.LinesLF("a: 1", "..."),
		},
		"first entry before a blank line": {
			before: stringtest.LinesLF("wifi: x", "", "a:", "  b: 1"),
			path:   paths.Doc().Child("wifi"),
			want:   stringtest.LinesLF("a:", "  b: 1"),
		},
		"first entry after a document header": {
			before: stringtest.LinesLF("---", "wifi: x", "", "a: 1"),
			path:   paths.Doc().Child("wifi"),
			want:   stringtest.LinesLF("---", "a: 1"),
		},
		"entry after a top comment keeps the blank line": {
			before: stringtest.LinesLF("# top", "wifi: x", "", "a: 1"),
			path:   paths.Doc().Child("wifi"),
			want:   stringtest.LinesLF("# top", "", "a: 1"),
		},
		"last entry of a block mapping": {
			before: stringtest.LinesLF("wifi: &w # router", "  security: wep"),
			path:   paths.Doc().Child("wifi", "security"),
			want:   "wifi: &w {} # router\n",
		},
		"last item of a block sequence": {
			before: stringtest.LinesLF(
				"tags:",
				"  - a",
				"price: 1",
			),
			path: paths.Doc().Child("tags").Index(0),
			want: stringtest.LinesLF("tags: []", "price: 1"),
		},
		"first entry of a mapping item": {
			before: stringtest.LinesLF("l:", "  - k: v # first", "    j: w"),
			path:   paths.MustParse("$.l[0].k"),
			want:   stringtest.LinesLF("l:", "  - j: w"),
		},
		"first entry of a mapping item at the root": {
			before: stringtest.LinesLF("- k: v", "  j: w", "- x"),
			path:   paths.MustParse("$[0].k"),
			want:   stringtest.LinesLF("- j: w", "- x"),
		},
		"first entry of a mapping item before the comment of the next": {
			before: stringtest.LinesLF("l:", "  - k: v", "    # about j", "    j: w"),
			path:   paths.MustParse("$.l[0].k"),
			want:   stringtest.LinesLF("l:", "  - # about j", "    j: w"),
		},
		"first entry of a mapping item before a comment of the mapping": {
			before: stringtest.LinesLF("l:", "  - k:", "      a: 1", "    # deep", "", "    j: w"),
			path:   paths.MustParse("$.l[0].k"),
			want:   stringtest.LinesLF("l:", "  -", "    # deep", "", "    j: w"),
		},
		"only entry of a mapping item": {
			before: stringtest.LinesLF("l:", "  - k: v", "  - x"),
			path:   paths.MustParse("$.l[0].k"),
			want:   stringtest.LinesLF("l:", "  - {}", "  - x"),
		},
		"first item of a sequence item": {
			before: stringtest.LinesLF("l:", "  - - v", "    - w"),
			path:   paths.MustParse("$.l[0][0]"),
			want:   stringtest.LinesLF("l:", "  - - w"),
		},
		"only item of a sequence item": {
			before: stringtest.LinesLF("l:", "  - - v", "  - x"),
			path:   paths.MustParse("$.l[0][0]"),
			want:   stringtest.LinesLF("l:", "  - []", "  - x"),
		},
		"entry of a flow mapping": {
			before: "spec: {a: 1, b: 2, c: 3}\n",
			path:   spec.Child("b"),
			want:   "spec: {a: 1, c: 3}\n",
		},
		"last entry of a flow mapping": {
			before: "spec: {a: 1, b: 2}\n",
			path:   spec.Child("b"),
			want:   "spec: {a: 1}\n",
		},
		"first item of a flow sequence that writes each item on its own line": {
			before: stringtest.LinesLF("f: [", "  0,", "  1", "]"),
			path:   paths.MustParse("$.f[0]"),
			want:   stringtest.LinesLF("f: [", "  1", "]"),
		},
		"middle item of a flow sequence that writes each item on its own line between blank lines": {
			before: stringtest.LinesLF("f: [", "  0,", "", "  1,", "", "  2", "]"),
			path:   paths.MustParse("$.f[1]"),
			want:   stringtest.LinesLF("f: [", "  0,", "", "  2", "]"),
		},
		"only item of a flow sequence on its own line": {
			before: stringtest.LinesLF("f: [", "  1", "]"),
			path:   paths.MustParse("$.f[0]"),
			want:   stringtest.LinesLF("f: [", "]"),
		},
		"item of a flow sequence on its own line with CRLF": {
			before: stringtest.LinesCRLF("f: [", "  0,", "  1", "]"),
			path:   paths.MustParse("$.f[0]"),
			want:   stringtest.LinesCRLF("f: [", "  1", "]"),
		},
		"item of several lines of a flow sequence": {
			before: stringtest.LinesLF("f: [", "  {a: 1,", "   b: 2},", "  3", "]"),
			path:   paths.MustParse("$.f[0]"),
			want:   stringtest.LinesLF("f: [", "  3", "]"),
		},
		"item of a flow sequence that shares its line with another item": {
			before: stringtest.LinesLF("f: [", "  0, 1,", "  2", "]"),
			path:   paths.MustParse("$.f[0]"),
			want:   stringtest.LinesLF("f: [", "  1,", "  2", "]"),
		},
		"item of a flow sequence that ends its line after another item": {
			before: stringtest.LinesLF("f: [", "  0, 1,", "  2", "]"),
			path:   paths.MustParse("$.f[1]"),
			want:   stringtest.LinesLF("f: [", "  0,", "  2", "]"),
		},
		"item of a flow sequence that ends its line after another item with CRLF": {
			before: stringtest.LinesCRLF("f: [", "  0, 1,", "  2", "]"),
			path:   paths.MustParse("$.f[1]"),
			want:   stringtest.LinesCRLF("f: [", "  0,", "  2", "]"),
		},
		"item of a flow sequence that ends the line of its key": {
			before: stringtest.LinesLF("f: [0, 1,", "  2]"),
			path:   paths.MustParse("$.f[1]"),
			want:   stringtest.LinesLF("f: [0,", "  2]"),
		},
		"first item of a flow sequence that ends the line of its bracket": {
			before: stringtest.LinesLF("f: [ 0,", "  1]"),
			path:   paths.MustParse("$.f[0]"),
			want:   stringtest.LinesLF("f: [", "  1]"),
		},
		"last item of a flow sequence after another item on its line, with spaces after it": {
			before: stringtest.LinesLF("f: [", "  0 , 1  ", "]"),
			path:   paths.MustParse("$.f[1]"),
			want:   stringtest.LinesLF("f: [", "  0", "]"),
		},
		"entry of a flow mapping that ends its line after another entry": {
			before: stringtest.LinesLF("g: {", "  a: 1, b: 2,", "  c: 3", "}"),
			path:   paths.MustParse("$.g.b"),
			want:   stringtest.LinesLF("g: {", "  a: 1,", "  c: 3", "}"),
		},
		"item of a flow sequence with a comment on its line": {
			before: stringtest.LinesLF("f: [", "  0, # c", "  1", "]"),
			path:   paths.MustParse("$.f[0]"),
			err:    "test.yaml:1:4: $.f: cafe.jacobcolvin.com/v1 removes $.f[0], which reaches a flow collection with a comment inside it, and cafe does not edit inside such a collection",
		},
		"last item of a flow sequence with a comment on its line": {
			before: stringtest.LinesLF("f: [", "  0,", "  1 # one", "]"),
			path:   paths.MustParse("$.f[1]"),
			err:    "test.yaml:1:4: $.f: cafe.jacobcolvin.com/v1 removes $.f[1], which reaches a flow collection with a comment inside it, and cafe does not edit inside such a collection",
		},
		"last item of a flow sequence with a comment on its line with CRLF": {
			before: stringtest.LinesCRLF("f: [", "  0,", "  1 # one", "]"),
			path:   paths.MustParse("$.f[1]"),
			err:    "test.yaml:1:4: $.f: cafe.jacobcolvin.com/v1 removes $.f[1], which reaches a flow collection with a comment inside it, and cafe does not edit inside such a collection",
		},
		"only item of a flow sequence with a comment on its line": {
			before: stringtest.LinesLF("f: [", "  1 # one", "]"),
			path:   paths.MustParse("$.f[0]"),
			err:    "test.yaml:1:4: $.f: cafe.jacobcolvin.com/v1 removes $.f[0], which reaches a flow collection with a comment inside it, and cafe does not edit inside such a collection",
		},
		"last item of several lines of a flow sequence with a comment on its last line": {
			before: stringtest.LinesLF("f: [", "  0,", "  {a: 1,", "   b: 2} # one", "]"),
			path:   paths.MustParse("$.f[1]"),
			err:    "test.yaml:1:4: $.f: cafe.jacobcolvin.com/v1 removes $.f[1], which reaches a flow collection with a comment inside it, and cafe does not edit inside such a collection",
		},
		"last item of a flow sequence with a trailing comma and a comment on its line": {
			before: stringtest.LinesLF("f: [", "  0,", "  1, # one", "]"),
			path:   paths.MustParse("$.f[1]"),
			err:    "test.yaml:1:4: $.f: cafe.jacobcolvin.com/v1 removes $.f[1], which reaches a flow collection with a comment inside it, and cafe does not edit inside such a collection",
		},
		"last item of a flow sequence after an item with a comment on its line": {
			before: stringtest.LinesLF("f: [", "  0, # zero", "  1 # one", "]"),
			path:   paths.MustParse("$.f[1]"),
			err:    "test.yaml:1:4: $.f: cafe.jacobcolvin.com/v1 removes $.f[1], which reaches a flow collection with a comment inside it, and cafe does not edit inside such a collection",
		},
		"last item of a flow sequence after an item with a comment right after its comma": {
			before: stringtest.LinesLF("f: [", "  0,# zero", "  1", "]"),
			path:   paths.MustParse("$.f[1]"),
			want:   stringtest.LinesLF("f: [", "  0 # zero", "]"),
			value:  map[string]any{"f": []any{uint64(0)}},
		},
		"last item of a flow sequence after an alias with a comment right after its comma": {
			before: stringtest.LinesLF("a: &x 0", "f: [", "  *x,# zero", "  1", "]"),
			path:   paths.MustParse("$.f[1]"),
			want:   stringtest.LinesLF("a: &x 0", "f: [", "  *x # zero", "]"),
			value:  map[string]any{"a": uint64(0), "f": []any{uint64(0)}},
		},
		"last item of a flow sequence after an item with a comment right after its comma with CRLF": {
			before: stringtest.LinesCRLF("f: [", "  0,# zero", "  1", "]"),
			path:   paths.MustParse("$.f[1]"),
			want:   stringtest.LinesCRLF("f: [", "  0 # zero", "]"),
			value:  map[string]any{"f": []any{uint64(0)}},
		},
		"first item of a flow sequence with a comment right after its comma on the line of its bracket": {
			before: stringtest.LinesLF("f: [0,# zero", "  1]"),
			path:   paths.MustParse("$.f[0]"),
			want:   stringtest.LinesLF("f: [ # zero", "  1]"),
			value:  map[string]any{"f": []any{uint64(1)}},
		},
		"last item of a flow sequence after a comma that starts its line, with a trailing comma and a comment": {
			before: stringtest.LinesLF("f: [", "  0", "  , 1, # one", "]"),
			path:   paths.MustParse("$.f[1]"),
			err:    "test.yaml:1:4: $.f: cafe.jacobcolvin.com/v1 removes $.f[1], which reaches a flow collection with a comment inside it, and cafe does not edit inside such a collection",
		},
		"middle item of a flow sequence after a comma that starts its line, with a comma and a comment after it": {
			before: stringtest.LinesLF("f: [", "  0", "  , 1, # one", "  2", "]"),
			path:   paths.MustParse("$.f[1]"),
			err:    "test.yaml:1:4: $.f: cafe.jacobcolvin.com/v1 removes $.f[1], which reaches a flow collection with a comment inside it, and cafe does not edit inside such a collection",
		},
		"last item of a flow sequence on the line of its bracket with a comment on its line": {
			before: stringtest.LinesLF("f: [0,", "  1 # one", "  ]"),
			path:   paths.MustParse("$.f[1]"),
			err:    "test.yaml:1:4: $.f: cafe.jacobcolvin.com/v1 removes $.f[1], which reaches a flow collection with a comment inside it, and cafe does not edit inside such a collection",
		},
		"item of a flow sequence with a comment before its comma": {
			before: stringtest.LinesLF("f: [", "  0,", "  1 # one", "  , 2", "]"),
			path:   paths.MustParse("$.f[1]"),
			err:    "test.yaml:1:4: $.f: cafe.jacobcolvin.com/v1 removes $.f[1], which reaches a flow collection with a comment inside it, and cafe does not edit inside such a collection",
		},
		"item of a flow sequence with a comment on a line that another item keeps": {
			before: stringtest.LinesLF("f: [", "  0, 1, # c", "  2", "]"),
			path:   paths.MustParse("$.f[1]"),
			err:    "test.yaml:1:4: $.f: cafe.jacobcolvin.com/v1 removes $.f[1], which reaches a flow collection with a comment inside it, and cafe does not edit inside such a collection",
		},
		"last item of a flow sequence with a comment on a line that another item keeps": {
			before: stringtest.LinesLF("f: [", "  0, 1 # c", "]"),
			path:   paths.MustParse("$.f[1]"),
			err:    "test.yaml:1:4: $.f: cafe.jacobcolvin.com/v1 removes $.f[1], which reaches a flow collection with a comment inside it, and cafe does not edit inside such a collection",
		},
		"first item of a flow sequence with a comment on the line of its bracket": {
			before: stringtest.LinesLF("f: [0, # zero", "  1]"),
			path:   paths.MustParse("$.f[0]"),
			err:    "test.yaml:1:4: $.f: cafe.jacobcolvin.com/v1 removes $.f[0], which reaches a flow collection with a comment inside it, and cafe does not edit inside such a collection",
		},
		"last item of a flow sequence with a comment on its line before a comment after the bracket": {
			before: stringtest.LinesLF("f: [", "  0,", "  1 # one", "] # end"),
			path:   paths.MustParse("$.f[1]"),
			err:    "test.yaml:1:4: $.f: cafe.jacobcolvin.com/v1 removes $.f[1], which reaches a flow collection with a comment inside it, and cafe does not edit inside such a collection",
		},
		"last entry of a flow mapping with a comment on its line": {
			before: stringtest.LinesLF("g: {", "  a: 1,", "  b: 2 # bee", "}"),
			path:   paths.MustParse("$.g.b"),
			err:    "test.yaml:1:4: $.g: cafe.jacobcolvin.com/v1 removes $.g.b, which reaches a flow collection with a comment inside it, and cafe does not edit inside such a collection",
		},
		"last entry of a flow mapping after an entry with a comment on its line": {
			before: stringtest.LinesLF("g: {", "  a: 1, # one", "  b: 2", "}"),
			path:   paths.MustParse("$.g.b"),
			err:    "test.yaml:1:4: $.g: cafe.jacobcolvin.com/v1 removes $.g.b, which reaches a flow collection with a comment inside it, and cafe does not edit inside such a collection",
		},
		"last entry of a flow mapping after an entry with a comment right after its comma": {
			before: stringtest.LinesLF("g: {", "  a: abc,# one", "  b: 2", "}"),
			path:   paths.MustParse("$.g.b"),
			want:   stringtest.LinesLF("g: {", "  a: abc # one", "}"),
			value:  map[string]any{"g": map[string]any{"a": "abc"}},
		},
		"last entry of a flow mapping after a key without a value and a comment right after its comma": {
			before: stringtest.LinesLF("g: {", "  a,# one", "  b: 2", "}"),
			path:   paths.MustParse("$.g.b"),
			want:   stringtest.LinesLF("g: {", "  a # one", "}"),
			value:  map[string]any{"g": map[string]any{"a": nil}},
		},
		"last entry of a flow mapping after a comma that starts its line, with a trailing comma and a comment": {
			before: stringtest.LinesLF("g: {", "  a: 1", "  , b: 2, # bee", "}"),
			path:   paths.MustParse("$.g.b"),
			err:    "test.yaml:1:4: $.g: cafe.jacobcolvin.com/v1 removes $.g.b, which reaches a flow collection with a comment inside it, and cafe does not edit inside such a collection",
		},
		"first entry of a flow mapping that writes each entry on its own line": {
			before: stringtest.LinesLF("g: {", "  a: 1,", "  b: 2", "}"),
			path:   paths.MustParse("$.g.a"),
			want:   stringtest.LinesLF("g: {", "  b: 2", "}"),
		},
		"only entry of a flow mapping on its own line": {
			before: stringtest.LinesLF("g: {", "  a: 1", "}"),
			path:   paths.MustParse("$.g.a"),
			want:   stringtest.LinesLF("g: {", "}"),
		},
		"only mapping item without braces of a flow sequence on its own line": {
			before: stringtest.LinesLF("f: [", "  a: 1", "]"),
			path:   paths.MustParse("$.f[0]"),
			want:   stringtest.LinesLF("f: [", "]"),
		},
		"only entry of a mapping item of a flow sequence without braces": {
			before: "f: [a: 1, 2]\n",
			path:   paths.MustParse("$.f[0].a"),
			err:    "test.yaml:1:5: $.f[0].a: cafe.jacobcolvin.com/v1 removes $.f[0].a, which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"only entry of the last mapping item of a flow sequence without braces": {
			before: "f: [0, a: {b: 1}]\n",
			path:   paths.MustParse("$.f[1].a"),
			err:    "test.yaml:1:8: $.f[1].a: cafe.jacobcolvin.com/v1 removes $.f[1].a, which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"only entry of a mapping item without braces of a nested flow sequence": {
			before: stringtest.LinesLF("f: [", "  [a: 1],", "  2", "]"),
			path:   paths.MustParse("$.f[0][0].a"),
			err:    "test.yaml:2:4: $.f[0][0].a: cafe.jacobcolvin.com/v1 removes $.f[0][0].a, which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"block value": {
			before: stringtest.LinesLF(
				"spec:",
				"  extras:",
				"    oat: 5",
				"    soy: 6",
				"  price: 1",
			),
			path: spec.Child("extras"),
			want: stringtest.LinesLF("spec:", "  price: 1"),
		},
		"missing place": {
			before: "a: 1\n",
			path:   paths.Doc().Child("b"),
			want:   "a: 1\n",
		},
		"anchored value keeps its alias": {
			before: stringtest.LinesLF(
				"spec:",
				"  tags: &diet vegan",
				"  labels:",
				"    diet: *diet",
			),
			path: spec.Child("tags"),
			err:  "test.yaml:4:11: $.spec.labels.diet: cafe.jacobcolvin.com/v1 removes $.spec.tags, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored block value keeps its alias": {
			before: stringtest.LinesLF(
				"spec:",
				"  oat: &milk",
				"    price: 5",
				"  soy: *milk",
			),
			path: spec.Child("oat"),
			err:  "test.yaml:4:8: $.spec.soy: cafe.jacobcolvin.com/v1 removes $.spec.oat, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored block value with its alias as the first key of an item": {
			before: stringtest.LinesLF(
				"milk: &oat",
				"  name: x",
				"  cost: 5",
				"drinks:",
				"  - milk: *oat",
				"  - milk: *oat",
			),
			path: paths.Doc().Child("milk"),
			err:  "test.yaml:5:11: $.drinks[0].milk: cafe.jacobcolvin.com/v1 removes $.milk, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored block value with its alias in an item of an item": {
			before: stringtest.LinesLF("milk: &oat", "  name: x", "drinks:", "  - - milk: *oat"),
			path:   paths.Doc().Child("milk"),
			err:    "test.yaml:4:13: $.drinks[0][0].milk: cafe.jacobcolvin.com/v1 removes $.milk, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored value with aliases in several places": {
			before: stringtest.LinesLF(
				"milk: &oat",
				"  name: x",
				"drinks:",
				"  - milk: *oat",
				"  - milk: *oat",
				"kids: *oat",
				"flow: [*oat]",
			),
			path: paths.Doc().Child("milk"),
			err:  "test.yaml:4:11: $.drinks[0].milk: cafe.jacobcolvin.com/v1 removes $.milk, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored NaN keeps its alias": {
			before: stringtest.LinesLF("a: &x .nan", "b: *x"),
			path:   paths.Doc().Child("a"),
			err:    "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 removes $.a, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored block value keeps its inner comments at its alias": {
			before: stringtest.LinesLF(
				"milk: &oat",
				"  # Oat milk.",
				"  name: x # inner",
				"  cost: 5 # kr",
				"  # after",
				"drinks:",
				"  - milk: *oat",
				"  - milk: *oat",
			),
			path: paths.Doc().Child("milk"),
			err:  "test.yaml:7:11: $.drinks[0].milk: cafe.jacobcolvin.com/v1 removes $.milk, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"key comment of an anchored entry goes with the entry": {
			before: stringtest.LinesLF("milk: &oat # on key", "  name: x", "drinks:", "  m: *oat"),
			path:   paths.Doc().Child("milk"),
			err:    "test.yaml:4:6: $.drinks.m: cafe.jacobcolvin.com/v1 removes $.milk, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored block value with a comment after its alias": {
			before: stringtest.LinesLF("milk: &oat", "  name: x", "  cost: 5 # kr", "d:", "  m: *oat # the milk"),
			path:   paths.Doc().Child("milk"),
			err:    "test.yaml:5:6: $.d.m: cafe.jacobcolvin.com/v1 removes $.milk, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored block scalar keeps its alias": {
			before: stringtest.LinesLF("s: &q |", "  text", "  more", "d:", "  m: *q"),
			path:   paths.Doc().Child("s"),
			err:    "test.yaml:5:6: $.d.m: cafe.jacobcolvin.com/v1 removes $.s, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored block scalar with an indentation indicator": {
			before: stringtest.LinesLF("s: &q |2", "    text", "d:", "  - *q"),
			path:   paths.Doc().Child("s"),
			err:    "test.yaml:4:5: $.d[0]: cafe.jacobcolvin.com/v1 removes $.s, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored block scalar with keep chomping": {
			before: stringtest.LinesLF("s: &q |+", "  text", "", "d:", "  m: *q", "n: 1"),
			path:   paths.Doc().Child("s"),
			err:    "test.yaml:5:6: $.d.m: cafe.jacobcolvin.com/v1 removes $.s, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored block scalar with keep chomping, before a blank line": {
			// The removal takes the anchor that the alias reads.
			before: stringtest.LinesLF("a: &x |+", "  q", "", "b: *x", "", "c: 1"),
			path:   paths.Doc().Child("a"),
			err:    "test.yaml:4:4: $.b: cafe.jacobcolvin.com/v1 removes $.a, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored block mapping that ends with a block scalar with keep chomping": {
			before: stringtest.LinesLF("a: &a", "  s: |+", "    x", "", "b: *a", "c: 1"),
			path:   paths.Doc().Child("a"),
			err:    "test.yaml:5:4: $.b: cafe.jacobcolvin.com/v1 removes $.a, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored block mapping that ends with a block scalar with keep chomping, at the end without a final newline": {
			before: stringtest.JoinLF("a: &a", "  s: |+", "    x", "", "b: *a"),
			path:   paths.Doc().Child("a"),
			err:    "test.yaml:5:4: $.b: cafe.jacobcolvin.com/v1 removes $.a, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored plain scalar of two lines": {
			before: stringtest.LinesLF("s: &q foo", "  bar", "d:", "  m: *q"),
			path:   paths.Doc().Child("s"),
			err:    "test.yaml:4:6: $.d.m: cafe.jacobcolvin.com/v1 removes $.s, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"tagged anchored block mapping keeps its alias": {
			before: stringtest.LinesLF("milk: &oat !!map", "  name: x", "d:", "  m: *oat"),
			path:   paths.Doc().Child("milk"),
			err:    "test.yaml:4:6: $.d.m: cafe.jacobcolvin.com/v1 removes $.milk, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored block mapping with its alias in a flow mapping": {
			before: stringtest.LinesLF("milk: &oat", "  name: x # kind", "d: {m: *oat}"),
			path:   paths.Doc().Child("milk"),
			err:    "test.yaml:3:8: $.d.m: cafe.jacobcolvin.com/v1 removes $.milk, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored block mapping with its alias in a flow sequence": {
			before: stringtest.LinesLF("milk: &oat", "  name: x", "d: [*oat, *oat]"),
			path:   paths.Doc().Child("milk"),
			err:    "test.yaml:3:5: $.d[0]: cafe.jacobcolvin.com/v1 removes $.milk, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored block sequence with its alias in a flow sequence": {
			before: stringtest.LinesLF("s: &q", "  - a", "  - b,c", "d: [*q]"),
			path:   paths.Doc().Child("s"),
			err:    "test.yaml:4:5: $.d[0]: cafe.jacobcolvin.com/v1 removes $.s, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored block scalar with its alias in a flow sequence": {
			before: stringtest.LinesLF("s: &q |", "  text", "d: [*q]"),
			path:   paths.Doc().Child("s"),
			err:    "test.yaml:3:5: $.d[0]: cafe.jacobcolvin.com/v1 removes $.s, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored plain scalar of two lines with its alias in a flow sequence": {
			before: stringtest.LinesLF("s: &q a", "  b", "d: [*q]"),
			path:   paths.Doc().Child("s"),
			err:    "test.yaml:3:5: $.d[0]: cafe.jacobcolvin.com/v1 removes $.s, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored block mapping with an inner anchor and a tag, in a flow sequence": {
			// Go-yaml reads a quoted scalar under !!str as YAML does.
			before: stringtest.LinesLF(
				"milk: &oat",
				"  name: &n x",
				"  when: !!str 'spring'",
				"  q: 'y'",
				"d: [*oat, *n]",
			),
			path: paths.Doc().Child("milk"),
			err:  "test.yaml:5:5: $.d[0]: cafe.jacobcolvin.com/v1 removes $.milk, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored value whose copy holds an alias of another anchor of the value": {
			before: stringtest.LinesLF("a:", "  x: &e 1", "  y: &f [*e]", "b: *f"),
			path:   paths.Doc().Child("a"),
			err:    "test.yaml:4:4: $.b: cafe.jacobcolvin.com/v1 removes $.a, which holds the anchor &f that this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored value whose copy holds an alias that a later alias follows": {
			before: stringtest.LinesLF("a:", "  x: &e 1", "  y: &f", "    - *e", "b: *f", "c: *e"),
			path:   paths.Doc().Child("a"),
			err:    "test.yaml:5:4: $.b: cafe.jacobcolvin.com/v1 removes $.a, which holds the anchor &f that this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored value whose copy holds an alias of an anchored block mapping": {
			before: stringtest.LinesLF("a:", "  x: &e", "    k: 1", "  y: &f", "    - *e", "b: *f"),
			path:   paths.Doc().Child("a"),
			err:    "test.yaml:6:4: $.b: cafe.jacobcolvin.com/v1 removes $.a, which holds the anchor &f that this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"inner anchor of an anchored value with an alias before the alias of the value": {
			before: stringtest.LinesLF("a: &o", "  x: &e 1", "c: *e", "b: *o"),
			path:   paths.Doc().Child("a"),
			err:    "test.yaml:3:4: $.c: cafe.jacobcolvin.com/v1 removes $.a, which holds the anchor &e that this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored value whose copy holds an alias that an earlier copy follows": {
			before: stringtest.LinesLF("a:", "  x: &e 1", "  y: &f [*e]", "c: *e", "b: *f"),
			path:   paths.Doc().Child("a"),
			err:    "test.yaml:4:4: $.c: cafe.jacobcolvin.com/v1 removes $.a, which holds the anchor &e that this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored value with an inner anchor on a block scalar at the end of a text without a line break": {
			// The removal takes the anchor that the alias reads.
			before: stringtest.JoinLF("a: &g", "  y: &f >", "    x", "      z", "b: *g"),
			path:   paths.Doc().Child("a"),
			err:    "test.yaml:5:4: $.b: cafe.jacobcolvin.com/v1 removes $.a, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"inner anchor of a copy after an earlier copy of that anchor": {
			// Probe finding #22 of verify 12. The removal takes &f, which *f at k2
			// reads.
			before: stringtest.LinesLF("k1: &g [&f 6]", "k2: *f", "k4: *g"),
			path:   paths.Doc().Child("k1"),
			err:    "test.yaml:2:5: $.k2: cafe.jacobcolvin.com/v1 removes $.k1, which holds the anchor &f that this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"inner anchor of a copy that a later alias reads": {
			// The first reader binds the problem, and k5 reads &f too.
			before: stringtest.LinesLF("k1: &g [&f 6]", "k4: *g", "k5: *f"),
			path:   paths.Doc().Child("k1"),
			err:    "test.yaml:2:5: $.k4: cafe.jacobcolvin.com/v1 removes $.k1, which this alias reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored block mapping with its alias in a merge key list": {
			before: stringtest.LinesLF("base: &b", "  k: 1", "soy:", "  <<: [*b]", "  name: s"),
			path:   paths.Doc().Child("base"),
			err:    "test.yaml:4:8: $.soy: cafe.jacobcolvin.com/v1 removes $.base, which the merge key at 4:3 of $.soy reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored block value whose first alias is a merge key": {
			before: stringtest.LinesLF("base: &b", "  k: 1", "soy:", "  <<: *b", "  name: s"),
			path:   paths.Doc().Child("base"),
			err:    "test.yaml:4:7: $.soy: cafe.jacobcolvin.com/v1 removes $.base, which the merge key at 4:3 of $.soy reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored block value whose first alias is a merge key in an item": {
			before: stringtest.LinesLF("base: &b", "  k: 1", "l:", "  - <<: *b", "    name: s"),
			path:   paths.Doc().Child("base"),
			err:    "test.yaml:4:9: $.l[0]: cafe.jacobcolvin.com/v1 removes $.base, which the merge key at 4:5 of $.l[0] reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored entry of the mapping that merges it": {
			before: stringtest.LinesLF("soy:", "  base: &b", "    k: 1", "  <<: *b", "  name: s"),
			path:   paths.MustParse("$.soy.base"),
			err:    "test.yaml:4:7: $.soy: cafe.jacobcolvin.com/v1 removes $.soy.base, which the merge key at 4:3 of $.soy reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored flow entry of the mapping that merges it in a list": {
			before: stringtest.LinesLF("soy:", "  base: &b {k: 1}", "  <<: [*b]", "  name: s"),
			path:   paths.MustParse("$.soy.base"),
			err:    "test.yaml:3:3: $.soy: cafe.jacobcolvin.com/v1 removes $.soy.base, which changes the keys of a merge through a merge key that merges a sequence of aliases, and cafe does not change the keys of such a merge",
		},
		"anchored value deeper in the mapping that merges it": {
			before: stringtest.LinesLF("soy:", "  inner:", "    base: &b", "      k: 1", "  <<: *b", "  name: s"),
			path:   paths.MustParse("$.soy.inner.base"),
			err:    "test.yaml:5:7: $.soy: cafe.jacobcolvin.com/v1 removes $.soy.inner.base, which the merge key at 5:3 of $.soy reads, and cafe does not write a copy of a value at its alias",
		},
		"only entry of a mapping that an alias reads": {
			before: stringtest.LinesLF("a: &x", "  k: 1", "b: *x"),
			path:   paths.MustParse("$.b.k"),
			want:   stringtest.LinesLF("a: &x {}", "b: *x"),
		},
		"only item of a sequence that an alias reads": {
			before: stringtest.LinesLF("a: &x", "  - 1", "b: *x"),
			path:   paths.MustParse("$.b[0]"),
			want:   stringtest.LinesLF("a: &x []", "b: *x"),
		},
		"only entry of a mapping that an alias in a flow sequence reads": {
			before: stringtest.LinesLF("a: &x", "  k: 1", "b: [*x]"),
			path:   paths.MustParse("$.b[0].k"),
			want:   stringtest.LinesLF("a: &x {}", "b: [*x]"),
		},
		"entry that a merge key of a flow mapping brings in": {
			before: stringtest.LinesLF("d: &d", "  port: 80", "  host: h", "svc: {<<: *d, x: 1}"),
			path:   paths.MustParse("$.svc.port"),
			want:   stringtest.LinesLF("d: &d", "  host: h", "svc: {<<: *d, x: 1}"),
		},
		"only entry of a mapping that a merge key brings in": {
			before: stringtest.LinesLF("summer: &s", "  closes: 1", "spec:", "  <<: *s", "  x: 1"),
			path:   paths.MustParse("$.spec.closes"),
			want:   stringtest.LinesLF("summer: &s {}", "spec:", "  <<: *s", "  x: 1"),
		},
		"only entry of the root without a final newline": {
			before: "m: 1",
			path:   paths.Doc().Child("m"),
			want:   "{}",
			value:  map[string]any{},
		},
		"only entry of the root between blank lines": {
			before: stringtest.LinesLF("# c", "", "m: 1", "", ""),
			path:   paths.Doc().Child("m"),
			want:   stringtest.LinesLF("# c", "", "{}", "", ""),
			value:  map[string]any{},
		},
		"only entry of the root after a document header without a final newline": {
			before: stringtest.JoinLF("---", "m: 1"),
			path:   paths.Doc().Child("m"),
			want:   stringtest.JoinLF("---", "{}"),
			value:  map[string]any{},
		},
		"only entry of the root after a document header with CRLF line endings without a final newline": {
			before: stringtest.JoinCRLF("---", "m: 1"),
			path:   paths.Doc().Child("m"),
			want:   stringtest.JoinCRLF("---", "{}"),
			value:  map[string]any{},
		},
		"only entry of the root after a comment line without a final newline": {
			before: stringtest.JoinLF("# c", "m: 1"),
			path:   paths.Doc().Child("m"),
			want:   stringtest.JoinLF("# c", "{}"),
			value:  map[string]any{},
		},
		"only entry of the root after a document header with a comment without a final newline": {
			before: stringtest.JoinLF("--- # c", "m: 1"),
			path:   paths.Doc().Child("m"),
			want:   stringtest.JoinLF("--- # c", "{}"),
			value:  map[string]any{},
		},
		"only entry of the root after a directive without a final newline": {
			before: stringtest.JoinLF("%YAML 1.2", "---", "m: 1"),
			path:   paths.Doc().Child("m"),
			want:   stringtest.JoinLF("%YAML 1.2", "---", "{}"),
			value:  map[string]any{},
		},
		"only entry of the root after a blank line without a final newline": {
			before: stringtest.JoinLF("---", "", "m:", "  a: 1"),
			path:   paths.Doc().Child("m"),
			want:   stringtest.JoinLF("---", "", "{}"),
			value:  map[string]any{},
		},
		"only entry of an anchored root without a final newline": {
			before: stringtest.JoinLF("--- &a", "m: 1"),
			path:   paths.Doc().Child("m"),
			want:   stringtest.JoinLF("--- &a", "{}"),
			value:  map[string]any{},
		},
		"only entry of a tagged root without a final newline": {
			before: stringtest.JoinLF("!!map", "m: 1"),
			path:   paths.Doc().Child("m"),
			want:   stringtest.JoinLF("!!map", "{}"),
			value:  map[string]any{},
		},
		"only item of the root after a comment line without a final newline": {
			before: stringtest.JoinLF("# c", "- 1"),
			path:   paths.MustParse("$[0]"),
			want:   stringtest.JoinLF("# c", "[]"),
			value:  []any{},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var opts []niceyaml.SourceOption

			if tc.ref != "" {
				opts = append(opts, niceyaml.WithReferences(niceyaml.NewSourceFromString(tc.ref)))
			}

			d := newDoc(t, tc.before, opts...)

			err := applyAt(t, d, tc.path, func(_ context.Context, n *edit.Node) error {
				return n.Remove(edit.WithReason("gone"))
			})

			requireEdit(t, d, err, tc.err, tc.want)

			if tc.err != "" {
				assert.Equal(t, tc.engine, errors.Is(err, edit.ErrEngine))
			}

			if tc.value != nil {
				got, err := d.Node().Decode[any](t.Context())
				require.NoError(t, err)
				assert.Equal(t, tc.value, got)
			}
		})
	}
}

// aliasChain returns a document whose mapping a holds depth anchored
// sequences after the first. Each sequence holds two aliases of the
// sequence before it, and b reads the last one.
func aliasChain(depth int) string {
	var text strings.Builder

	text.WriteString("a:\n  m0: &m0 [0]\n")

	for i := 1; i <= depth; i++ {
		fmt.Fprintf(&text, "  m%d: &m%d [*m%d, *m%d]\n", i, i, i-1, i-1)
	}

	fmt.Fprintf(&text, "b: *m%d\n", depth)

	return text.String()
}

func TestNode_Remove_AliasChain(t *testing.T) {
	t.Parallel()

	// A copy of the last sequence at b reads each sequence before it once
	// for each alias of it. A walk that reads them that way doubles its
	// work at every level. So does a copy that writes each of their values
	// without its anchor. At this depth, either runs for months.
	d := newDoc(t, aliasChain(40))

	err := applyAt(t, d, paths.Doc().Child("a"), func(_ context.Context, n *edit.Node) error {
		return n.Remove()
	})
	require.ErrorIs(t, err, niceyaml.ErrExcessiveAliasing)
}

func TestDoc_Apply_EngineProblem(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		fn     edit.Func
		before string
		// The message of the one problem of the error.
		want string
		// The messages of its details, in order.
		details []string
	}{
		// The Set fails before it writes the value, at the value that it
		// replaces, so the detail that names the Set says "from this value"
		// (P74).
		"set of a float that go-yaml's encoder writes in an exponent form": {
			before: "a: 1\n",
			fn: func(ctx context.Context, root *edit.Node) error {
				return root.At(paths.MustParse("$.a")).Set(ctx, 1e6)
			},
			want:    `go-yaml's encoder writes the float 1e+06 as text that go-yaml's decoder reads as "1e+06"`,
			details: []string{"report this to the authors of this program", "the edit sets $.a from this value"},
		},
		"removal whose text go-yaml's parser refuses": {
			// The engine wrote the text that the parser refuses, so the
			// document is not at fault for the refusal.
			before: stringtest.LinesLF("a: |+2", "    x", "", "b: 1"),
			fn: func(_ context.Context, root *edit.Node) error {
				return root.At(paths.MustParse("$.b")).Remove()
			},
			want: "the edit removes $.b, and go-yaml's parser refuses the edited text: invalid number of indent is " +
				"specified in the multi-line header",
			details: []string{"report this to the authors of this program", "the edit removes $.b"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d, err := edit.New(niceyaml.NewSourceFromString(tc.before, niceyaml.WithName("test.yaml")).Documents()[0])
			require.NoError(t, err)

			err = d.Apply(t.Context(), paths.Doc(), tc.fn)
			require.ErrorIs(t, err, edit.ErrEngine)

			// A report that lists the problems of the error reads one row,
			// which matches ErrEngine and carries the fix.
			problem := yamltest.OnlyProblem(t, err)
			require.ErrorIs(t, problem.Err, edit.ErrEngine)
			assert.Equal(t, tc.want, problem.Message())
			require.Len(t, problem.Children, len(tc.details))

			for i, detail := range tc.details {
				assert.True(t, problem.Children[i].Detail)
				assert.Equal(t, detail, problem.Children[i].Message())
			}

			assert.False(t, problem.IsInvalid())
		})
	}
}
