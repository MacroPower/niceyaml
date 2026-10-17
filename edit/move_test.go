package edit_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/edit"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

func TestNode_Move(t *testing.T) {
	t.Parallel()

	var (
		meta     = paths.Doc().Child("metadata")
		milk     = paths.Doc().Child("milk")
		defaults = paths.Doc().Child("defaults")
	)

	tcs := map[string]struct {
		before string
		// The value that the text decodes to, when set.
		value any
		moves [][2]paths.Path
		want  string
		err   string
		// Whether the error matches edit.ErrEngine.
		engine bool
		// Whether the Source allows duplicate keys.
		dup bool
	}{
		"value that holds an explicit key out of a mapping whose merge key comes after it": {
			// Control: the Move changes the keys of the root and reaches the
			// explicit key inside the value at the step of its place, and the
			// explicit key comes first in the text.
			before: stringtest.LinesLF("r1:", "  ? c", "  : 1", "<<: {a: q}"),
			moves:  [][2]paths.Path{{paths.MustParse("$.r1"), paths.MustParse("$.z")}},
			err: "test.yaml:2:3: $.r1.c: cafe.jacobcolvin.com/v1 moves $.r1 to $.z, which reaches an entry with an " +
				"explicit key",
		},
		"entry of a flow mapping with a comment to a new key of a mapping that merges a mapping written in place": {
			// The path meets the flow collection at its second step, so the
			// Move fails there before it checks its target, which changes the
			// keys of the root, whose merge key comes first in the text (P38).
			before: stringtest.LinesLF("<<: {k: 1}", "r1:", "  x: {a: 1, # c", "    b: 2}"),
			moves:  [][2]paths.Path{{paths.MustParse("$.r1.x.b"), paths.MustParse("$.z")}},
			err: "test.yaml:3:6: $.r1.x: cafe.jacobcolvin.com/v1 moves $.r1.x.b to $.z, which reaches a flow " +
				"collection with a comment inside it",
		},
		"entry below an explicit key to a new key of a mapping whose merge key comes first": {
			// The path meets the explicit key at its first step, so the Move
			// fails there before it checks its target, though the merge key of
			// the target comes first in the text (P38).
			before: stringtest.LinesLF("r0:", "  <<: {d: 2}", "? r1", ": {k: x}"),
			moves:  [][2]paths.Path{{paths.MustParse("$.r1.k"), paths.MustParse("$.r0.z")}},
			err: "test.yaml:3:1: $.r1: cafe.jacobcolvin.com/v1 moves $.r1.k to $.r0.z, which reaches an entry with an " +
				"explicit key",
		},
		"empty value under a string tag into a flow mapping beside a block scalar that go-yaml misreads": {
			// The Move writes the empty string that the empty value under !!str
			// reads, in quotes after the tag, which YAML reads alike. New takes
			// the strip block scalar whose last line ends with spaces, as
			// go-yaml reads it, and the Move keeps it.
			before: stringtest.LinesLF("r0: [{a: 1}]", "r1: |-", "  y z", "   ", "a: !!str"),
			moves:  [][2]paths.Path{{paths.MustParse("$.a"), paths.MustParse("$.r0[0].z")}},
			want:   stringtest.LinesLF("r0: [{a: 1, z: !!str ''}]", "r1: |-", "  y z", "   "),
		},
		"entry with an explicit key to a flow mapping with a comment before it": {
			// The first step of the path meets the explicit key, and the target
			// meets the flow collection at its second step (P38).
			before: stringtest.LinesLF("a:", "  b: {p: 1, # c", "    q: 2}", "? e", ": 1", "z: 0"),
			moves:  [][2]paths.Path{{paths.MustParse("$.e"), paths.MustParse("$.a.b.r")}},
			err: "test.yaml:4:1: $.e: cafe.jacobcolvin.com/v1 moves $.e to $.a.b.r, which reaches an entry with an " +
				"explicit key",
		},
		"entry with an explicit key to a new key of a mapping whose merge key comes after it": {
			// Control: the explicit key comes first in the text.
			before: stringtest.LinesLF("r0:", "  - ? k", "    : 1", "r1:", "  <<: {}"),
			moves:  [][2]paths.Path{{paths.MustParse("$.r0[0].k"), paths.MustParse("$.r1.z")}},
			err: "test.yaml:2:5: $.r0[0].k: cafe.jacobcolvin.com/v1 moves $.r0[0].k to $.r1.z, which reaches an entry " +
				"with an explicit key",
		},
		"entry through a merge key to a pair without braces before it": {
			// The path looks a up through the merge key at its first step, so
			// the Move fails there, though the pair of the target comes first
			// in the text (P38).
			before: stringtest.LinesLF("r2: [a: 1]", "<<: {a: 1}"),
			moves:  [][2]paths.Path{{paths.MustParse("$.a"), paths.MustParse("$.r2[0].z")}},
			err: "test.yaml:2:1: $: cafe.jacobcolvin.com/v1 moves $.a to $.r2[0].z, which goes through a merge key " +
				"that merges a mapping written in place",
		},
		"entry with an explicit key to an entry that a merge key brings in": {
			before: stringtest.LinesLF("s: &s", "  a: 1", "m:", "  <<: *s", "? x", ": 5"),
			moves:  [][2]paths.Path{{paths.MustParse("$.x"), paths.MustParse("$.m.a")}},
			err:    "which reaches an entry with an explicit key",
		},
		"entry to an entry that a merge key brings in": {
			// Control.
			before: stringtest.LinesLF("s: &s", "  a: 1", "m:", "  <<: *s", "x: 5"),
			moves:  [][2]paths.Path{{paths.MustParse("$.x"), paths.MustParse("$.m.a")}},
			err:    "but <<: *s at 4:3 also brings in a, so $.m then hides the a of $.s",
		},
		"mapping with a repeated key through an alias": {
			// The alias would come before its anchor at target, and the move
			// writes no copy.
			before: stringtest.LinesLF("t:", "  q: 1", "a: &x {k: 1, k: 2}", "m:", "  d: *x"),
			moves:  [][2]paths.Path{{paths.Doc().Child("m"), paths.MustParse("$.t.z")}},
			dup:    true,
			err:    "test.yaml:5:6: $.m.d: cafe.jacobcolvin.com/v1 moves $.m to $.t.z, after which this alias comes before the anchor that it reads, and cafe does not write a copy of a value at its alias",
		},
		"entry below a scalar under a local tag": {
			// A scalar holds no value below it, whatever its tag, so the text
			// stays, as below an untagged scalar.
			before: "- !t x\n",
			moves:  [][2]paths.Path{{paths.MustParse("$[0].k"), paths.MustParse("$[0].z")}},
			want:   "- !t x\n",
		},
		"entry below a moved scalar under a local tag": {
			before: "a: !t x\n",
			moves: [][2]paths.Path{
				{paths.MustParse("$.a"), paths.MustParse("$.c")},
				{paths.MustParse("$.c.k"), paths.MustParse("$.c.z")},
			},
			want: "c: !t x\n",
		},
		"entry of a block scalar key without a value": {
			// The lines of the key stay with the key, so the move takes the
			// null of the entry.
			before: stringtest.LinesLF("a:", "  ? |", "    x", "  k: 1", "b: 1"),
			moves:  [][2]paths.Path{{paths.MustParse(`$.a.'x\n'`), paths.Doc().Child("c")}},
			err:    "test.yaml:2:3: $.a.'x\\n': cafe.jacobcolvin.com/v1 moves $.a.'x\\n' to $.c, which reaches an entry with an explicit key, and cafe does not edit such an entry other than by removing it",
		},
		"block scalar to a new key after a deeper comment line of the entry before it": {
			// The comment line that the user indented past the key of a belongs
			// to a, so the moved entry goes after it.
			before: stringtest.LinesLF("s: |-", "  l1", "  l2", "m:", "  a: x", "    # note", "b: 1"),
			moves:  [][2]paths.Path{{paths.Doc().Child("s"), paths.MustParse("$.m.z")}},
			want:   stringtest.LinesLF("m:", "  a: x", "    # note", "  z: |-", "    l1", "    l2", "b: 1"),
		},
		"block scalar with an indentation indicator to a new key after a deeper comment line of the entry before it": {
			// The string keeps the space at its start.
			before: stringtest.LinesLF("s: |2-", "   l1", "  l2", "m:", "  a: x", "      # note", "b: 1"),
			moves:  [][2]paths.Path{{paths.Doc().Child("s"), paths.MustParse("$.m.z")}},
			want:   stringtest.LinesLF("m:", "  a: x", "      # note", "  z: |2-", "     l1", "    l2", "b: 1"),
			value:  map[string]any{"m": map[string]any{"a": "x", "z": " l1\nl2"}, "b": uint64(1)},
		},
		"block scalar with an indentation indicator to a new key after a deep comment line of the entry before it": {
			// The comment line belongs to k, so the scalar goes after it, and
			// the comment stays.
			before: stringtest.LinesLF("a: >2", "   x", "m:", "  k: 1", "           # d"),
			moves:  [][2]paths.Path{{paths.Doc().Child("a"), paths.MustParse("$.m.z")}},
			want:   stringtest.LinesLF("m:", "  k: 1", "           # d", "  z: >2", "     x"),
		},
		"block scalar to a new key after the comment line of the entry before it": {
			// The comment line belongs to c, so a later removal of z keeps it.
			before: stringtest.LinesLF("a: x", "b: |-", "  l1", "  l2", "c: y", "  # d"),
			moves:  [][2]paths.Path{{paths.Doc().Child("b"), paths.Doc().Child("z")}},
			want:   stringtest.LinesLF("a: x", "c: y", "  # d", "z: |-", "  l1", "  l2"),
		},
		"anchored mapping into a merged entry, after which a merge list reads its anchor in place of a copy": {
			// Sweep case 24152/19354. The anchor x0 goes to the first alias
			// that reads it, under the merge key of r1. Target keeps its anchor
			// too, so the merge list of the root reads the d of target in place
			// of the d of the copy that r1 merges. Both read the d of a.
			before: stringtest.LinesLF("a: &x0", "  d: []", "r1: &x1", "  <<: *x0", "  k: {}", "<<: [*x0, *x1]"),
			moves:  [][2]paths.Path{{paths.Doc().Child("a"), paths.MustParse("$.k.n")}},
			err:    "test.yaml:6:1: $: cafe.jacobcolvin.com/v1 moves $.a to $.k.n, which goes through a merge key that merges a sequence of aliases, and cafe does not edit through such a merge key",
		},
		"anchored mapping into a merged block entry, after which a merge list reads its anchor in place of a copy": {
			before: stringtest.LinesLF(
				"a: &x0",
				"  d: []",
				"r1: &x1",
				"  <<: *x0",
				"  k:",
				"    q: 1",
				"<<: [*x0, *x1]",
			),
			moves: [][2]paths.Path{{paths.Doc().Child("a"), paths.MustParse("$.k.n")}},
			err:   "test.yaml:7:1: $: cafe.jacobcolvin.com/v1 moves $.a to $.k.n, which goes through a merge key that merges a sequence of aliases, and cafe does not edit through such a merge key",
		},
		"block scalar to the end of a document before a comment line deeper than its text": {
			// The comment line between the document and the next "---" belongs
			// to k, as at the end of a file, so the moved entry goes after it.
			before: stringtest.LinesLF("m: |", "  x", "z:", "  k: 1", "    # d", "---", "y: 3"),
			moves:  [][2]paths.Path{{paths.Doc().Child("m"), paths.MustParse("$.z.n")}},
			want:   stringtest.LinesLF("z:", "  k: 1", "    # d", `  "n": |`, "    x"),
		},
		"entry that hides a merged key": {
			// After the move, the mapping reads the a that the merge key brings
			// in, so the check after the last call fails the Apply.
			before: stringtest.LinesLF("<<: {a: 1}", "a: 2"),
			moves:  [][2]paths.Path{{paths.Doc().Child("a"), paths.Doc().Child("z")}},
			err:    "test.yaml:1:1: $: cafe.jacobcolvin.com/v1 moves $.a to $.z, which changes the keys of a merge through a merge key that merges a mapping written in place, and cafe does not change the keys of such a merge",
		},
		"anchored value past its alias, into the mapping of an alias": {
			// Target reads the mapping of r through *x1, so the value goes
			// there, after *x0, which reads its anchor.
			before: stringtest.LinesLF("v: [&x0 {c: 1}]", "r: &x1", "  m: *x0", "  k: {}", "s: *x1"),
			moves:  [][2]paths.Path{{paths.Doc().Child("v"), paths.MustParse("$.s.k.z")}},
			err:    "test.yaml:3:6: $.r.m: cafe.jacobcolvin.com/v1 moves $.v to $.s.k.z, after which this alias comes before the anchor that it reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored value to a target before a merge key of the root that reads it": {
			// The path to target reads the merge key, so the value goes in at
			// target while the anchor stays in place.
			before: stringtest.LinesLF("r0: &x0 {a: 2}", "r1:", "  b: 1", "<<: *x0"),
			moves:  [][2]paths.Path{{paths.Doc().Child("r0"), paths.MustParse("$.r1.z0")}},
			want:   stringtest.LinesLF("r1:", "  b: 1", "  z0: &x0 {a: 2}", "<<: *x0"),
			value: map[string]any{
				"a":  uint64(2),
				"r1": map[string]any{"b": uint64(1), "z0": map[string]any{"a": uint64(2)}},
			},
		},
		"into new mappings": {
			before: stringtest.LinesLF(
				"# Café Yamüll, the main room.",
				"name: yamull",
				"seats: 40",
			),
			moves: [][2]paths.Path{
				{paths.Doc().Child("name"), meta.Child("name")},
				{paths.Doc().Child("seats"), spec.Child("seats")},
			},
			want: stringtest.LinesLF(
				"# Café Yamüll, the main room.",
				"metadata:",
				"  name: yamull",
				"spec:",
				"  seats: 40",
			),
		},
		"with comments and a block value": {
			before: stringtest.LinesLF(
				"listen:",
				"  host: x",
				"# The port.",
				"port: # p",
				"  number: 80",
			),
			moves: [][2]paths.Path{{paths.Doc().Child("port"), paths.Doc().Child("listen", "port")}},
			want: stringtest.LinesLF(
				"listen:",
				"  host: x",
				"  # The port.",
				"  port: # p",
				"    number: 80",
			),
		},
		"block scalar after a block scalar, without a final newline": {
			before: stringtest.JoinLF("a: |", "  x", "b: |", "  y"),
			moves:  [][2]paths.Path{{paths.Doc().Child("b"), paths.Doc().Child("c")}},
			want:   stringtest.JoinLF("a: |", "  x", "c: |", "  y"),
		},
		"item of block scalars after a block scalar, without a final newline": {
			before: stringtest.JoinLF("l:", "- |", "  x", "- |", "  y"),
			moves:  [][2]paths.Path{{paths.MustParse("$.l[1]"), paths.Doc().Child("z")}},
			want:   stringtest.JoinLF("l:", "- |", "  x", "z: |", "  y"),
		},
		"block scalar with an indentation indicator, after a deep comment line of the entry before it": {
			// The comment line belongs to c, so the moved scalar goes after it
			// and keeps its indicator.
			before: stringtest.LinesLF("a:", "  - >2", "      x", "  - y", "b:", "  c: 1", "              # d"),
			moves:  [][2]paths.Path{{paths.MustParse("$.a[0]"), paths.MustParse("$.b.d")}},
			want:   stringtest.LinesLF("a:", "  - y", "b:", "  c: 1", "              # d", "  d: >2", "      x"),
		},
		"entry after a block scalar, without a final newline": {
			before: stringtest.JoinLF("a: |", "  x", "b: 1"),
			moves:  [][2]paths.Path{{paths.Doc().Child("b"), paths.Doc().Child("c")}},
			want:   stringtest.JoinLF("a: |", "  x", "c: 1"),
		},
		"block scalar with keep chomping, into a mapping before a blank line": {
			// The blank line after the block scalar goes with it, and it takes
			// the place of the blank line where it goes.
			before: stringtest.LinesLF("a: |+", "  x", "", "b:", "  k: 1", "", "c: 2"),
			moves:  [][2]paths.Path{{paths.Doc().Child("a"), paths.MustParse("$.b.a")}},
			want:   stringtest.LinesLF("b:", "  k: 1", "  a: |+", "    x", "", "c: 2"),
		},
		"item that ends with a block scalar with keep chomping, into a sequence before a blank line": {
			before: stringtest.LinesLF("s:", "  - |+", "    x", "", "  - 2", "t:", "  - 1", "", "c: 1"),
			moves:  [][2]paths.Path{{paths.MustParse("$.s[0]"), paths.MustParse("$.t[1]")}},
			want:   stringtest.LinesLF("s:", "  - 2", "t:", "  - 1", "  - |+", "    x", "", "c: 1"),
		},
		"entry that ends with a block scalar with keep chomping and a comment, into a mapping before a blank line": {
			before: stringtest.LinesLF("a:", "  s: |+", "    x", "", "  # c", "b:", "  k: 1", "", "c: 2"),
			moves:  [][2]paths.Path{{paths.Doc().Child("a"), paths.MustParse("$.b.a")}},
			want:   stringtest.LinesLF("b:", "  k: 1", "  a:", "    s: |+", "      x", "", "    # c", "", "c: 2"),
		},
		"block scalar with keep chomping to the end of a text without a final newline": {
			// The blank line that the string of the block scalar holds keeps its
			// line break, so the text takes a final newline.
			before: stringtest.JoinLF("x: |+", "  q", "", "<<: {}"),
			moves:  [][2]paths.Path{{paths.Doc().Child("x"), paths.Doc().Child("z0")}},
			err:    "test.yaml:4:1: $: cafe.jacobcolvin.com/v1 moves $.x to $.z0, which changes the keys of a merge through a merge key that merges a mapping written in place, and cafe does not change the keys of such a merge",
		},
		"entry after a block scalar with keep chomping and an indentation indicator, before a blank line": {
			// The blank line after the entry goes with it, so that the block
			// scalar does not take it.
			before: stringtest.LinesLF("r0:", "  b:", "    - |+2", "        w", "  p:", "", "r1: []"),
			moves:  [][2]paths.Path{{paths.MustParse("$.r0.p"), paths.MustParse("$.r0.b[1]")}},
			want:   stringtest.LinesLF("r0:", "  b:", "    - |+2", "        w", "    -", "r1: []"),
		},
		"folded block scalar with strip chomping before a last line of spaces, out of its mapping": {
			// The line of spaces holds more spaces than the lines of the scalar
			// indent by where it goes, so it does not stay after the scalar.
			before: stringtest.JoinLF("r1:", "  p: >-", "      z", "     "),
			moves:  [][2]paths.Path{{paths.MustParse("$.r1.p"), paths.Doc().Child("z")}},
			want:   stringtest.LinesLF("r1: {}", "z: >-", "    z"),
		},
		"entry that ends with a block scalar with keep chomping and an indentation indicator, to the end of the text": {
			// Go-yaml's parser refuses a text that ends with an empty line
			// after such a scalar.
			before: stringtest.LinesLF("a:", "  s: |+2", "      x", "", "b: 1"),
			moves:  [][2]paths.Path{{paths.Doc().Child("a"), paths.Doc().Child("c")}},
			err: "test.yaml:3:7: $.a.s: cafe.jacobcolvin.com/v1 moves $.a to $.c, and go-yaml's parser " +
				"refuses the edited text: invalid number of indent is specified in the multi-line header",
			engine: true,
		},
		"block scalar with strip chomping to the end of a text without a final newline": {
			before: stringtest.JoinLF("x: |-", "  q", "<<: {}"),
			moves:  [][2]paths.Path{{paths.Doc().Child("x"), paths.Doc().Child("z0")}},
			err:    "test.yaml:3:1: $: cafe.jacobcolvin.com/v1 moves $.x to $.z0, which changes the keys of a merge through a merge key that merges a mapping written in place, and cafe does not change the keys of such a merge",
		},
		"entry that ends with a block scalar with keep chomping, into a mapping before a blank line": {
			before: stringtest.LinesLF("a:", "  s: |+", "    x", "", "b:", "  k: 1", "", "c: 2"),
			moves:  [][2]paths.Path{{paths.Doc().Child("a"), paths.MustParse("$.b.a")}},
			want:   stringtest.LinesLF("b:", "  k: 1", "  a:", "    s: |+", "      x", "", "c: 2"),
		},
		"last entry after a blank line": {
			before: stringtest.LinesLF("a:", "  b: 1", "", "wifi: x"),
			moves:  [][2]paths.Path{{paths.Doc().Child("wifi"), paths.Doc().Child("a", "wifi")}},
			want:   stringtest.LinesLF("a:", "  b: 1", "  wifi: x"),
		},
		"first entry of a mapping item": {
			before: stringtest.LinesLF("l:", "  - k: v", "    j: w", "m: 1"),
			moves:  [][2]paths.Path{{paths.MustParse("$.l[0].k"), paths.Doc().Child("k")}},
			want:   stringtest.LinesLF("l:", "  - j: w", "m: 1", "k: v"),
		},
		"target that exists": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			moves:  [][2]paths.Path{{paths.Doc().Child("a"), paths.Doc().Child("b")}},
			err:    `cafe.jacobcolvin.com/v1 moves "a" to $.b, which already exists`,
		},
		"item to a target that exists": {
			// The problem is about the key that the move writes for the "-"
			// that held the value, so it binds at the "-" (fix19 F7).
			before: stringtest.LinesLF("l:", "  - x", "m:", "  b: 1"),
			moves:  [][2]paths.Path{{paths.MustParse("$.l[0]"), paths.MustParse("$.m.b")}},
			err:    "test.yaml:2:3: $.l[0]: cafe.jacobcolvin.com/v1 moves $.l[0] to $.m.b, which already exists",
		},
		"item that a move wrote to a target that exists": {
			// The "-" that the first move wrote binds where the user wrote the
			// key that held the value (fix19 F7).
			before: stringtest.LinesLF("l:", "  - x", "m:", "  b: 1", "  c: 2"),
			moves: [][2]paths.Path{
				{paths.MustParse("$.m.c"), paths.MustParse("$.l[1]")},
				{paths.MustParse("$.l[1]"), paths.MustParse("$.m.b")},
			},
			err: "test.yaml:5:3: $.m.c: cafe.jacobcolvin.com/v1 moves $.l[1] to $.m.b, which already exists",
		},
		"control: item of a flow sequence to a target that exists": {
			before: stringtest.LinesLF("l: [x, y]", "m:", "  b: 1"),
			moves:  [][2]paths.Path{{paths.MustParse("$.l[1]"), paths.MustParse("$.m.b")}},
			err:    "test.yaml:1:8: $.l[1]: cafe.jacobcolvin.com/v1 moves $.l[1] to $.m.b, which already exists",
		},
		"target inside the value": {
			before: stringtest.LinesLF("a:", "  b: 1"),
			moves:  [][2]paths.Path{{paths.Doc().Child("a"), paths.Doc().Child("a", "c")}},
			err:    `cafe.jacobcolvin.com/v1 moves "a" to $.a.c, which lies inside it`,
		},
		"past the last item of a block sequence": {
			before: stringtest.LinesLF(
				"spec:",
				"  # The house blend.",
				"  blend: kaffe # dark",
				"  items:",
				"    - te",
			),
			moves: [][2]paths.Path{{spec.Child("blend"), spec.Child("items").Index(1)}},
			want: stringtest.LinesLF(
				"spec:",
				"  items:",
				"    - te",
				"    # The house blend.",
				"    - kaffe # dark",
			),
		},
		"block mapping past the last item": {
			before: stringtest.LinesLF(
				"wifi:",
				"  ssid: fika",
				"  mode: wpa3",
				"networks:",
				"- ssid: guest",
			),
			moves: [][2]paths.Path{{paths.Doc().Child("wifi"), paths.Doc().Child("networks").Index(1)}},
			want: stringtest.LinesLF(
				"networks:",
				"- ssid: guest",
				"- ssid: fika",
				"  mode: wpa3",
			),
		},
		"entry with an explicit key": {
			before: stringtest.LinesLF("spec:", "  # c", "  ? b # kc", "  : x # vc"),
			moves:  [][2]paths.Path{{spec.Child("b"), spec.Child("n")}},
			err:    "test.yaml:3:3: $.spec.b: cafe.jacobcolvin.com/v1 moves $.spec.b to $.spec.n, which reaches an entry with an explicit key, and cafe does not edit such an entry other than by removing it",
		},
		"entry with an explicit key and a block scalar at the root": {
			before: stringtest.LinesLF("? b", ": |", "  text", "c: 1"),
			moves:  [][2]paths.Path{{paths.Doc().Child("b"), paths.Doc().Child("n")}},
			err:    "test.yaml:1:1: $.b: cafe.jacobcolvin.com/v1 moves $.b to $.n, which reaches an entry with an explicit key, and cafe does not edit such an entry other than by removing it",
		},
		"entry with an explicit key and a block mapping into another mapping": {
			before: stringtest.LinesLF("spec:", "  ? b", "  : c: 1", "    d: 2", "m:", "  k: 1"),
			moves:  [][2]paths.Path{{spec.Child("b"), paths.MustParse("$.m.n")}},
			err:    "test.yaml:2:3: $.spec.b: cafe.jacobcolvin.com/v1 moves $.spec.b to $.m.n, which reaches an entry with an explicit key, and cafe does not edit such an entry other than by removing it",
		},
		"entry with an explicit key past the last item of a block sequence": {
			before: stringtest.LinesLF("spec:", "  ? b", "  : x", "l:", "  - 1"),
			moves:  [][2]paths.Path{{spec.Child("b"), paths.MustParse("$.l[1]")}},
			err:    "test.yaml:2:3: $.spec.b: cafe.jacobcolvin.com/v1 moves $.spec.b to $.l[1], which reaches an entry with an explicit key, and cafe does not edit such an entry other than by removing it",
		},
		"past the last item of a flow sequence": {
			before: stringtest.LinesLF("blend: kaffe", "items: [te]"),
			moves:  [][2]paths.Path{{paths.Doc().Child("blend"), paths.Doc().Child("items").Index(1)}},
			want:   "items: [te, kaffe]\n",
		},
		"only entry of the root to a new key": {
			before: stringtest.LinesLF("b: text # t"),
			moves:  [][2]paths.Path{{paths.Doc().Child("b"), paths.Doc().Child("n")}},
			want:   stringtest.LinesLF(`"n": text # t`),
		},
		"only item of the root past the last item": {
			before: stringtest.LinesLF("- 1 # c"),
			moves:  [][2]paths.Path{{paths.MustParse("$[0]"), paths.MustParse("$[1]")}},
			want:   stringtest.LinesLF("- 1 # c"),
		},
		"into a mapping item of a flow sequence without braces": {
			before: stringtest.LinesLF("f: [a: 1]", "z: 2 # z"),
			moves:  [][2]paths.Path{{paths.Doc().Child("z"), paths.MustParse("$.f[0].z")}},
			err:    "test.yaml:1:5: $.f[0].a: cafe.jacobcolvin.com/v1 moves $.z to $.f[0].z, which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"only entry of an anchored root to a new key": {
			before: stringtest.LinesLF("&r", "b: text # t"),
			moves:  [][2]paths.Path{{paths.Doc().Child("b"), paths.Doc().Child("n")}},
			want:   stringtest.LinesLF("&r", `"n": text # t`),
		},
		"only entry of a root with a tag on its line to a new key": {
			before: stringtest.LinesLF("!!map b: text # t"),
			moves:  [][2]paths.Path{{paths.Doc().Child("b"), paths.Doc().Child("n")}},
			want:   stringtest.LinesLF(`!!map {"n": text}`),
		},
		"only entry of an anchored mapping item to a new key of the item": {
			before: stringtest.LinesLF("l:", "  - &x", "    b: text # t"),
			moves:  [][2]paths.Path{{paths.MustParse("$.l[0].b"), paths.MustParse("$.l[0].n")}},
			want:   stringtest.LinesLF("l:", `  - &x {"n": text}`),
		},
		"only entry of a mapping item to a new key of the item": {
			before: stringtest.LinesLF("l:", "  - b: |", "      text"),
			moves:  [][2]paths.Path{{paths.MustParse("$.l[0].b"), paths.MustParse("$.l[0].n")}},
			want:   stringtest.LinesLF("l:", `  - "n": |`, "      text"),
		},
		"only entry of a mapping item with a block mapping to a new key of the item": {
			before: stringtest.LinesLF("l:", "  - a: # c", "      x: 1"),
			moves:  [][2]paths.Path{{paths.MustParse("$.l[0].a"), paths.MustParse("$.l[0].b")}},
			want:   stringtest.LinesLF("l:", "  - b: # c", "      x: 1"),
		},
		"only item of a sequence item past the last item": {
			before: stringtest.LinesLF("l:", "  - - 1 # c"),
			moves:  [][2]paths.Path{{paths.MustParse("$.l[0][0]"), paths.MustParse("$.l[0][1]")}},
			want:   stringtest.LinesLF("l:", "  - - 1 # c"),
		},
		"only entry of a mapping item with a comment on its dash line, to a new key of the item": {
			before: stringtest.LinesLF("l:", "  - # dc", "    b: text # t"),
			moves:  [][2]paths.Path{{paths.MustParse("$.l[0].b"), paths.MustParse("$.l[0].q")}},
			want:   stringtest.LinesLF("l:", "  - # dc", "    q: text # t"),
		},
		"only entry of a mapping item of the root with a comment on its dash line, to a new key of the item": {
			before: stringtest.LinesLF("- # dc", "  b: 1", "- 2"),
			moves:  [][2]paths.Path{{paths.MustParse("$[0].b"), paths.MustParse("$[0].q")}},
			want:   stringtest.LinesLF("- # dc", "  q: 1", "- 2"),
		},
		"only item of a sequence item with a comment on its dash line, past the last item": {
			before: stringtest.LinesLF("l:", "  - # dc", "    - |", "      text"),
			moves:  [][2]paths.Path{{paths.MustParse("$.l[0][0]"), paths.MustParse("$.l[0][1]")}},
			want:   stringtest.LinesLF("l:", "  - # dc", "    - |", "      text"),
		},
		"first item of a flow sequence that writes each item on its own line to a new key": {
			before: stringtest.LinesLF("f: [", "  0,", "  1", "]"),
			moves:  [][2]paths.Path{{paths.MustParse("$.f[0]"), paths.Doc().Child("q")}},
			want:   stringtest.LinesLF("f: [", "  1", "]", "q: 0"),
		},
		"only item of a flow sequence on its own line to a new key": {
			before: stringtest.LinesLF("f: [", "  1", "]"),
			moves:  [][2]paths.Path{{paths.MustParse("$.f[0]"), paths.Doc().Child("q")}},
			want:   stringtest.LinesLF("f: [", "]", "q: 1"),
		},
		"first entry of a flow mapping that writes each entry on its own line to a new key": {
			before: stringtest.LinesLF("g: {", "  a: 1,", "  b: 2", "}"),
			moves:  [][2]paths.Path{{paths.MustParse("$.g.a"), paths.Doc().Child("q")}},
			want:   stringtest.LinesLF("g: {", "  b: 2", "}", "q: 1"),
		},
		"only entry of a flow mapping on its own line to a new key": {
			before: stringtest.LinesLF("g: {", "  a: 1", "}"),
			moves:  [][2]paths.Path{{paths.MustParse("$.g.a"), paths.Doc().Child("q")}},
			want:   stringtest.LinesLF("g: {", "}", "q: 1"),
		},
		"item of a flow sequence that ends its line after another item to a new key": {
			before: stringtest.LinesLF("f: [", "  0, 1,", "  2", "]"),
			moves:  [][2]paths.Path{{paths.MustParse("$.f[1]"), paths.Doc().Child("q")}},
			want:   stringtest.LinesLF("f: [", "  0,", "  2", "]", "q: 1"),
		},
		"entry of a flow mapping that ends its line after another entry to a new key": {
			before: stringtest.LinesLF("g: {", "  a: 1, b: 2,", "  c: 3", "}"),
			moves:  [][2]paths.Path{{paths.MustParse("$.g.b"), paths.Doc().Child("q")}},
			want:   stringtest.LinesLF("g: {", "  a: 1,", "  c: 3", "}", "q: 2"),
		},
		"last item of a flow sequence with a comment on its line to a new key": {
			before: stringtest.LinesLF("f: [", "  0,", "  1 # one", "]"),
			moves:  [][2]paths.Path{{paths.MustParse("$.f[1]"), paths.Doc().Child("q")}},
			err:    "test.yaml:1:4: $.f: cafe.jacobcolvin.com/v1 moves $.f[1] to $.q, which reaches a flow collection with a comment inside it, and cafe does not edit inside such a collection",
		},
		"first item of a flow sequence with a comment on its line to a new key": {
			before: stringtest.LinesLF("f: [", "  0, # zero", "  1", "]"),
			moves:  [][2]paths.Path{{paths.MustParse("$.f[0]"), paths.Doc().Child("q")}},
			err:    "test.yaml:1:4: $.f: cafe.jacobcolvin.com/v1 moves $.f[0] to $.q, which reaches a flow collection with a comment inside it, and cafe does not edit inside such a collection",
		},
		"last item of a flow sequence with a comment on its line, after an item with a comment, to a new key": {
			before: stringtest.LinesLF("f: [", "  0, # zero", "  1 # one", "]"),
			moves:  [][2]paths.Path{{paths.MustParse("$.f[1]"), paths.Doc().Child("q")}},
			err:    "test.yaml:1:4: $.f: cafe.jacobcolvin.com/v1 moves $.f[1] to $.q, which reaches a flow collection with a comment inside it, and cafe does not edit inside such a collection",
		},
		"last item of a flow sequence after an item with a comment right after its comma, to a new key": {
			before: stringtest.LinesLF("f: [", "  0,# zero", "  1", "]"),
			moves:  [][2]paths.Path{{paths.MustParse("$.f[1]"), paths.Doc().Child("q")}},
			want:   stringtest.LinesLF("f: [", "  0 # zero", "]", "q: 1"),
			value:  map[string]any{"f": []any{uint64(0)}, "q": uint64(1)},
		},
		"last entry of a flow mapping after an entry with a comment right after its comma, to a new key": {
			before: stringtest.LinesLF("g: {", "  a: abc,# one", "  b: 2", "}"),
			moves:  [][2]paths.Path{{paths.MustParse("$.g.b"), paths.Doc().Child("q")}},
			want:   stringtest.LinesLF("g: {", "  a: abc # one", "}", "q: 2"),
			value:  map[string]any{"g": map[string]any{"a": "abc"}, "q": uint64(2)},
		},
		"last item of a flow sequence after a comma that starts its line, with a trailing comma and a comment, to a new key": {
			before: stringtest.LinesLF("f: [", "  0", "  , 1, # one", "]"),
			moves:  [][2]paths.Path{{paths.MustParse("$.f[1]"), paths.Doc().Child("q")}},
			err:    "test.yaml:1:4: $.f: cafe.jacobcolvin.com/v1 moves $.f[1] to $.q, which reaches a flow collection with a comment inside it, and cafe does not edit inside such a collection",
		},
		"middle entry of a flow mapping after a comma that starts its line, with a comma and a comment after it, to a new key": {
			before: stringtest.LinesLF("g: {", "  a: 1", "  , b: 2, # bee", "  c: 3", "}"),
			moves:  [][2]paths.Path{{paths.MustParse("$.g.b"), paths.Doc().Child("q")}},
			err:    "test.yaml:1:4: $.g: cafe.jacobcolvin.com/v1 moves $.g.b to $.q, which reaches a flow collection with a comment inside it, and cafe does not edit inside such a collection",
		},
		"item of a flow sequence with a comment before its comma to a new key": {
			before: stringtest.LinesLF("f: [", "  0,", "  1 # one", "  , 2", "]"),
			moves:  [][2]paths.Path{{paths.MustParse("$.f[1]"), paths.Doc().Child("q")}},
			err:    "test.yaml:1:4: $.f: cafe.jacobcolvin.com/v1 moves $.f[1] to $.q, which reaches a flow collection with a comment inside it, and cafe does not edit inside such a collection",
		},
		"last item of a flow sequence with a comment on its line past the last item of a block sequence": {
			before: stringtest.LinesLF("f: [", "  0,", "  1 # one", "]", "l:", "  - 9"),
			moves:  [][2]paths.Path{{paths.MustParse("$.f[1]"), paths.MustParse("$.l[1]")}},
			err:    "test.yaml:1:4: $.f: cafe.jacobcolvin.com/v1 moves $.f[1] to $.l[1], which reaches a flow collection with a comment inside it, and cafe does not edit inside such a collection",
		},
		"last item of a flow sequence with a comment on its line to a new key below a mapping": {
			before: stringtest.LinesLF("f: [", "  0,", "  1 # one", "]", "m:", "  k: 0"),
			moves:  [][2]paths.Path{{paths.MustParse("$.f[1]"), paths.MustParse("$.m.q.r")}},
			err:    "test.yaml:1:4: $.f: cafe.jacobcolvin.com/v1 moves $.f[1] to $.m.q.r, which reaches a flow collection with a comment inside it, and cafe does not edit inside such a collection",
		},
		"last item of a flow sequence with a comment on its line into a flow sequence": {
			before: stringtest.LinesLF("f: [", "  0,", "  1 # one", "]", "g: [9]"),
			moves:  [][2]paths.Path{{paths.MustParse("$.f[1]"), paths.MustParse("$.g[1]")}},
			err:    "test.yaml:1:4: $.f: cafe.jacobcolvin.com/v1 moves $.f[1] to $.g[1], which reaches a flow collection with a comment inside it, and cafe does not edit inside such a collection",
		},
		"item of a flow sequence with a comment on a line that another item keeps, to a new key": {
			before: stringtest.LinesLF("f: [", "  0, 1 # c", "]"),
			moves:  [][2]paths.Path{{paths.MustParse("$.f[1]"), paths.Doc().Child("q")}},
			err:    "test.yaml:1:4: $.f: cafe.jacobcolvin.com/v1 moves $.f[1] to $.q, which reaches a flow collection with a comment inside it, and cafe does not edit inside such a collection",
		},
		"last entry of a flow mapping with a comment on its line to a new key": {
			before: stringtest.LinesLF("g: {", "  a: 1,", "  b: 2 # bee", "}"),
			moves:  [][2]paths.Path{{paths.MustParse("$.g.b"), paths.Doc().Child("q")}},
			err:    "test.yaml:1:4: $.g: cafe.jacobcolvin.com/v1 moves $.g.b to $.q, which reaches a flow collection with a comment inside it, and cafe does not edit inside such a collection",
		},
		"only entry of a mapping of one entry without braces in a flow sequence to a new key": {
			before: stringtest.LinesLF("f: [a: 1, 2]"),
			moves:  [][2]paths.Path{{paths.MustParse("$.f[0].a"), paths.Doc().Child("q")}},
			err:    "test.yaml:1:5: $.f[0].a: cafe.jacobcolvin.com/v1 moves $.f[0].a to $.q, which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"only entry of a mapping of one entry without braces in a flow sequence to a new key there": {
			before: stringtest.LinesLF("f: [a: 1, 2]"),
			moves:  [][2]paths.Path{{paths.MustParse("$.f[0].a"), paths.MustParse("$.f[0].q")}},
			err:    "test.yaml:1:5: $.f[0].a: cafe.jacobcolvin.com/v1 moves $.f[0].a to $.f[0].q, which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"mapping of one entry without braces in a flow sequence into a flow mapping": {
			before: stringtest.LinesLF("f: [a: 1]", "g: {k: 0}"),
			moves:  [][2]paths.Path{{paths.MustParse("$.f[0]"), paths.MustParse("$.g.q")}},
			err:    "test.yaml:1:5: $.f[0].a: cafe.jacobcolvin.com/v1 moves $.f[0] to $.g.q, which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"mapping of one entry without braces in a flow sequence to a new key": {
			before: stringtest.LinesLF("f: [0, a: 1]"),
			moves:  [][2]paths.Path{{paths.MustParse("$.f[1]"), paths.Doc().Child("q")}},
			err:    "test.yaml:1:8: $.f[1].a: cafe.jacobcolvin.com/v1 moves $.f[1] to $.q, which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"mapping of one entry without braces in a flow sequence past the last item of a block sequence": {
			before: stringtest.LinesLF("f: [0, a: 1]", "l:", "  - 1"),
			moves:  [][2]paths.Path{{paths.MustParse("$.f[1]"), paths.MustParse("$.l[1]")}},
			err:    "test.yaml:1:8: $.f[1].a: cafe.jacobcolvin.com/v1 moves $.f[1] to $.l[1], which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"past the last item of a flow sequence with a trailing comma": {
			before: stringtest.LinesLF("blend: kaffe", "items: [te,]"),
			moves:  [][2]paths.Path{{paths.Doc().Child("blend"), paths.Doc().Child("items").Index(1)}},
			want:   "items: [te, kaffe,]\n",
		},
		"into a new key of a flow mapping with a trailing comma": {
			before: stringtest.LinesLF("blend: kaffe", "menu: {te: 30,}"),
			moves:  [][2]paths.Path{{paths.Doc().Child("blend"), paths.MustParse("$.menu.blend")}},
			want:   "menu: {te: 30, blend: kaffe,}\n",
		},
		"block mapping into a flow sequence keeps its text": {
			// Go-yaml reads a quoted scalar under !!str as YAML does.
			before: stringtest.LinesLF("milk: # oat", "  when: !!str 'spring'", "  q: 'x'", "  none:", "l: []"),
			moves:  [][2]paths.Path{{milk, paths.MustParse("$.l[0]")}},
			err:    "test.yaml:2:3: $.milk: cafe.jacobcolvin.com/v1 moves $.milk to $.l[0], which puts a block value inside a flow collection, and cafe does not move a block value into a flow collection",
		},
		"block mapping with a merge key and an alias into a flow sequence": {
			before: stringtest.LinesLF("base: &b", "  k: 1", "milk:", "  <<: *b", "  j: *b", "l: []"),
			moves:  [][2]paths.Path{{milk, paths.MustParse("$.l[0]")}},
			err:    "test.yaml:4:3: $.milk: cafe.jacobcolvin.com/v1 moves $.milk to $.l[0], which puts a block value inside a flow collection, and cafe does not move a block value into a flow collection",
		},
		"block sequence into a new key of a flow mapping": {
			before: stringtest.LinesLF("milk:", "  - 1", "  - a,b", "f: {a: 1}"),
			moves:  [][2]paths.Path{{milk, paths.MustParse("$.f.b.c")}},
			err:    "test.yaml:2:3: $.milk: cafe.jacobcolvin.com/v1 moves $.milk to $.f.b.c, which puts a block value inside a flow collection, and cafe does not move a block value into a flow collection",
		},
		"block scalar into a flow sequence": {
			before: stringtest.LinesLF("milk: |", "  text", "l: [a]"),
			moves:  [][2]paths.Path{{milk, paths.MustParse("$.l[1]")}},
			err:    "test.yaml:1:7: $.milk: cafe.jacobcolvin.com/v1 moves $.milk to $.l[1], which puts a block value inside a flow collection, and cafe does not move a block value into a flow collection",
		},
		"empty entry of a flow mapping to a new key": {
			before: stringtest.LinesLF("f: {a: 1, b:}"),
			moves:  [][2]paths.Path{{paths.MustParse("$.f.b"), paths.Doc().Child("q")}},
			want:   stringtest.LinesLF("f: {a: 1}", "q:"),
		},
		"first empty entry of a flow mapping to a new key": {
			before: stringtest.LinesLF("g: {b:, a: 1}"),
			moves:  [][2]paths.Path{{paths.MustParse("$.g.b"), paths.Doc().Child("q")}},
			want:   stringtest.LinesLF("g: {a: 1}", "q:"),
		},
		"empty entry of a mapping item of a flow sequence to a new key": {
			before: stringtest.LinesLF("f: [{b:}, 1]"),
			moves:  [][2]paths.Path{{paths.MustParse("$.f[0].b"), paths.Doc().Child("q")}},
			want:   stringtest.LinesLF("f: [{}, 1]", "q:"),
		},
		"empty entry of a flow mapping with a comment to a new key": {
			before: stringtest.LinesLF("f: {", "  a: 1,", "  b: # bee", "}"),
			moves:  [][2]paths.Path{{paths.MustParse("$.f.b"), paths.Doc().Child("q")}},
			err:    "test.yaml:1:4: $.f: cafe.jacobcolvin.com/v1 moves $.f.b to $.q, which reaches a flow collection with a comment inside it, and cafe does not edit inside such a collection",
		},
		"empty entry of a flow mapping to a new key below a mapping": {
			before: stringtest.LinesLF("f: {a: 1, b:}", "m:", "  k: 0"),
			moves:  [][2]paths.Path{{paths.MustParse("$.f.b"), paths.MustParse("$.m.q.r")}},
			want:   stringtest.LinesLF("f: {a: 1}", "m:", "  k: 0", "  q:", "    r:"),
		},
		"empty entry of a flow mapping past the last item of a block sequence": {
			before: stringtest.LinesLF("f: {a: 1, b:}", "l:", "  - 0"),
			moves:  [][2]paths.Path{{paths.MustParse("$.f.b"), paths.MustParse("$.l[1]")}},
			want:   stringtest.LinesLF("f: {a: 1}", "l:", "  - 0", "  -"),
		},
		"empty entry of a flow mapping with a comment past the last item of a block sequence": {
			before: stringtest.LinesLF("f: {", "  a: 1,", "  b: # bee", "}", "l:", "  - 0"),
			moves:  [][2]paths.Path{{paths.MustParse("$.f.b"), paths.MustParse("$.l[1]")}},
			err:    "test.yaml:1:4: $.f: cafe.jacobcolvin.com/v1 moves $.f.b to $.l[1], which reaches a flow collection with a comment inside it, and cafe does not edit inside such a collection",
		},
		"empty entry of a block mapping with a comment past the last item of a block sequence": {
			before: stringtest.LinesLF("m:", "  a: 1", "  b: # bee", "l:", "  - 0"),
			moves:  [][2]paths.Path{{paths.MustParse("$.m.b"), paths.MustParse("$.l[1]")}},
			want:   stringtest.LinesLF("m:", "  a: 1", "l:", "  - 0", "  - # bee"),
		},
		"empty item of a block sequence past the last item of a block sequence": {
			before: stringtest.LinesLF("l:", "  - 0", "  -", "k:", "  - 1"),
			moves:  [][2]paths.Path{{paths.MustParse("$.l[1]"), paths.MustParse("$.k[1]")}},
			want:   stringtest.LinesLF("l:", "  - 0", "k:", "  - 1", "  -"),
		},
		"empty entry of a flow mapping into a flow mapping": {
			before: stringtest.LinesLF("f: {a: 1, b:}", "g: {k: 0}"),
			moves:  [][2]paths.Path{{paths.MustParse("$.f.b"), paths.MustParse("$.g.q")}},
			want:   stringtest.LinesLF("f: {a: 1}", "g: {k: 0, q: null}"),
		},
		"empty entry of a flow mapping into a flow sequence": {
			before: stringtest.LinesLF("f: {a: 1, b:}", "g: [0]"),
			moves:  [][2]paths.Path{{paths.MustParse("$.f.b"), paths.MustParse("$.g[1]")}},
			want:   stringtest.LinesLF("f: {a: 1}", "g: [0, null]"),
		},
		"empty entry of a flow mapping into an empty flow sequence": {
			before: stringtest.LinesLF("f: {a: 1, b:}", "g: []"),
			moves:  [][2]paths.Path{{paths.MustParse("$.f.b"), paths.MustParse("$.g[0]")}},
			want:   stringtest.LinesLF("f: {a: 1}", "g: [null]"),
		},
		"empty entry of a flow mapping into a mapping item of a flow sequence without braces": {
			before: stringtest.LinesLF("f: {a: 1, b:}", "g: [k: 0]"),
			moves:  [][2]paths.Path{{paths.MustParse("$.f.b"), paths.MustParse("$.g[0].q")}},
			err:    "test.yaml:2:5: $.g[0].k: cafe.jacobcolvin.com/v1 moves $.f.b to $.g[0].q, which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"far past the last item": {
			before: stringtest.LinesLF("blend: kaffe", "items: [te]"),
			moves:  [][2]paths.Path{{paths.Doc().Child("blend"), paths.Doc().Child("items").Index(3)}},
			err:    `test.yaml:1:8: $.blend: cafe.jacobcolvin.com/v1 moves "blend" to $.items[3], but items holds 1 item`,
		},
		"to the root of a mapping": {
			before: "a: 1\n",
			moves:  [][2]paths.Path{{paths.Doc().Child("a"), paths.Doc()}},
			err:    `test.yaml:1:4: $.a: cafe.jacobcolvin.com/v1 moves "a" to $, but a value cannot move to the document root`,
		},
		"item to the root of a sequence": {
			before: stringtest.LinesLF("- 1", "- 2"),
			moves:  [][2]paths.Path{{paths.MustParse("$[0]"), paths.Doc()}},
			err:    `test.yaml:1:3: $[0]: cafe.jacobcolvin.com/v1 moves $[0] to $, but a value cannot move to the document root`,
		},
		"item far past the last item of its sequence": {
			before: stringtest.LinesLF("l:", "  - 1", "  - 2"),
			moves:  [][2]paths.Path{{paths.MustParse("$.l[0]"), paths.MustParse("$.l[3]")}},
			err:    `test.yaml:2:5: $.l[0]: cafe.jacobcolvin.com/v1 moves $.l[0] to $.l[3], but l holds 2 items`,
		},
		"entry with a comment line deeper than its key": {
			// The comment line belongs to the entry, as a removal of the entry
			// takes it, so it moves with the entry.
			before: stringtest.LinesLF("a: 1", "b: 2", "   # c", "d: 3"),
			moves:  [][2]paths.Path{{paths.Doc().Child("b"), paths.Doc().Child("z")}},
			want:   stringtest.LinesLF("a: 1", "d: 3", "z: 2", "   # c"),
		},
		"entry with a comment line at the column of its key": {
			// The comment line does not belong to the entry, so it stays.
			before: stringtest.LinesLF("a: 1", "b: 2", "# c", "d: 3"),
			moves:  [][2]paths.Path{{paths.Doc().Child("b"), paths.Doc().Child("z")}},
			want:   stringtest.LinesLF("a: 1", "# c", "d: 3", "z: 2"),
		},
		"entry with comment lines deeper than its key into a new mapping": {
			before: stringtest.LinesLF("a: 1", "b: 2 # t", "   # c", "", "   # e", "d: 3"),
			moves:  [][2]paths.Path{{paths.Doc().Child("b"), paths.MustParse("$.m.z")}},
			want:   stringtest.LinesLF("a: 1", "d: 3", "m:", "  z: 2 # t", "     # c", "", "     # e"),
		},
		"item with a comment line deeper than its dash": {
			before: stringtest.LinesLF("l:", "- 1", "  # c", "- 2"),
			moves:  [][2]paths.Path{{paths.MustParse("$.l[0]"), paths.Doc().Child("z")}},
			want:   stringtest.LinesLF("l:", "- 2", "z: 1", "  # c"),
		},
		"entry with a comment line deeper than its key into a flow sequence": {
			// A flow sequence drops the comments of the entry.
			before: stringtest.LinesLF("a: 1", "b: 2", "   # c", "d: [3]"),
			moves:  [][2]paths.Path{{paths.Doc().Child("b"), paths.MustParse("$.d[1]")}},
			want:   stringtest.LinesLF("a: 1", "d: [3, 2]"),
		},
		"to an item of a sequence past the item after its last": {
			// The move names the place as Set names it.
			before: stringtest.LinesLF("s: [1]", "x: 5"),
			moves:  [][2]paths.Path{{paths.MustParse("$.x"), paths.MustParse("$.s[2]")}},
			err:    `test.yaml:2:4: $.x: cafe.jacobcolvin.com/v1 moves "x" to $.s[2], but s holds 1 item`,
		},
		"to an item of null": {
			before: stringtest.LinesLF("a:", "x: 5"),
			moves:  [][2]paths.Path{{paths.MustParse("$.x"), paths.MustParse("$.a[0]")}},
			err:    `test.yaml:2:4: $.x: cafe.jacobcolvin.com/v1 moves "x" to $.a[0], but a holds null`,
		},
		"to an item of a mapping": {
			before: stringtest.LinesLF("b: {k: 1}", "x: 5"),
			moves:  [][2]paths.Path{{paths.MustParse("$.x"), paths.MustParse("$.b[0]")}},
			err:    `test.yaml:2:4: $.x: cafe.jacobcolvin.com/v1 moves "x" to $.b[0], but b holds a mapping`,
		},
		"to a path through an index below a missing entry": {
			before: stringtest.LinesLF("b: {k: 1}", "x: 5"),
			moves:  [][2]paths.Path{{paths.MustParse("$.x"), paths.MustParse("$.b.s[0]")}},
			err: `test.yaml:2:4: $.x: cafe.jacobcolvin.com/v1 moves "x" to $.b.s[0], ` +
				"but the document holds no $.b.s",
		},
		"item to a key of a sequence": {
			before: stringtest.LinesLF("l:", "  - 1", "f: [0]"),
			moves:  [][2]paths.Path{{paths.MustParse("$.l[0]"), paths.MustParse("$.f.q")}},
			err:    `test.yaml:2:5: $.l[0]: cafe.jacobcolvin.com/v1 moves $.l[0] to $.f.q, but f holds a sequence`,
		},
		"item to a key of a later item that holds a scalar": {
			before: stringtest.LinesLF("l:", "  - 1", "  - 2"),
			moves:  [][2]paths.Path{{paths.MustParse("$.l[0]"), paths.MustParse("$.l[1].q")}},
			err: `test.yaml:2:5: $.l[0]: cafe.jacobcolvin.com/v1 moves $.l[0] to $.l[1].q, ` +
				"but $.l[1] holds a scalar",
		},
		"entry to a key of a scalar": {
			before: stringtest.LinesLF("z: 0", "a: 1", "f: 3"),
			moves:  [][2]paths.Path{{paths.Doc().Child("a"), paths.MustParse("$.f.q")}},
			err:    `test.yaml:2:4: $.a: cafe.jacobcolvin.com/v1 moves "a" to $.f.q, but f holds a scalar`,
		},
		"item into a later item": {
			before: stringtest.LinesLF(
				"items:",
				"  - kaffe",
				"  - name: te",
				"  - name: saft",
			),
			moves: [][2]paths.Path{{
				paths.Doc().Child("items").Index(0),
				paths.Doc().Child("items").Index(1).Child("blend"),
			}},
			want: stringtest.LinesLF(
				"items:",
				"  - name: te",
				"    blend: kaffe",
				"  - name: saft",
			),
		},
		"anchored block mapping into a new mapping": {
			before: stringtest.LinesLF("milk: &oat", "  name: x", "  cost: 5", "other: 1"),
			moves:  [][2]paths.Path{{milk, defaults.Child("milk")}},
			want:   stringtest.LinesLF("other: 1", "defaults:", "  milk: &oat", "    name: x", "    cost: 5"),
		},
		"anchored block mapping to the root": {
			before: stringtest.LinesLF("milk: &oat", "  name: x", "other: 1"),
			moves:  [][2]paths.Path{{milk, paths.Doc().Child("oat")}},
			want:   stringtest.LinesLF("other: 1", "oat: &oat", "  name: x"),
		},
		"anchored block mapping with a comment on its key line": {
			before: stringtest.LinesLF("milk: &oat # top", "  name: x", "other: 1"),
			moves:  [][2]paths.Path{{milk, defaults.Child("milk")}},
			want:   stringtest.LinesLF("other: 1", "defaults:", "  milk: &oat # top", "    name: x"),
		},
		"tagged block mapping": {
			before: stringtest.LinesLF("milk: !!map", "  name: x", "other: 1"),
			moves:  [][2]paths.Path{{milk, defaults.Child("milk")}},
			want:   stringtest.LinesLF("other: 1", "defaults:", "  milk: !!map", "    name: x"),
		},
		"anchored block sequence": {
			before: stringtest.LinesLF("milk: &oat", "  - a", "  - b", "other: 1"),
			moves:  [][2]paths.Path{{milk, defaults.Child("milk")}},
			want:   stringtest.LinesLF("other: 1", "defaults:", "  milk: &oat", "    - a", "    - b"),
		},
		"anchored block mapping past the last item": {
			before: stringtest.LinesLF("milk: &oat", "  name: x", "l:", "  - a"),
			moves:  [][2]paths.Path{{milk, paths.MustParse("$.l[1]")}},
			want:   stringtest.LinesLF("l:", "  - a", "  - &oat", "    name: x"),
		},
		"anchored block sequence without an indent past the last item": {
			before: stringtest.LinesLF("milk: &oat", "- a", "- b", "l:", "- x"),
			moves:  [][2]paths.Path{{milk, paths.MustParse("$.l[1]")}},
			want:   stringtest.LinesLF("l:", "- x", "- &oat", "  - a", "  - b"),
		},
		"tagged block sequence without an indent past the last item": {
			before: stringtest.LinesLF("milk: !!seq # c", "- a", "- b", "l:", "  - x"),
			moves:  [][2]paths.Path{{milk, paths.MustParse("$.l[1]")}},
			want:   stringtest.LinesLF("l:", "  - x", "  - !!seq # c", "    - a", "    - b"),
		},
		"only entry of a mapping with a comment on its key line, to a new key there": {
			before: stringtest.LinesLF("spec: # c", "  b: |", "    text"),
			moves:  [][2]paths.Path{{spec.Child("b"), spec.Child("n")}},
			want:   stringtest.LinesLF("spec: # c", `  "n": |`, "    text"),
		},
		"only entry of a mapping with a comment on its key line, to a new key below it": {
			before: stringtest.LinesLF("spec: # c", "  # The text.", "  b: text # t"),
			moves:  [][2]paths.Path{{spec.Child("b"), spec.Child("x", "n")}},
			want:   stringtest.LinesLF("spec: # c", "  x:", "    # The text.", `    "n": text # t`),
		},
		"only item of a sequence with a comment on its key line, past the last item": {
			before: stringtest.LinesLF("l: # c", "  - |", "    text"),
			moves:  [][2]paths.Path{{paths.MustParse("$.l[0]"), paths.MustParse("$.l[1]")}},
			want:   stringtest.LinesLF("l: # c", "  - |", "    text"),
		},
		"only entry of a mapping with a comment on its key line, with CRLF": {
			before: stringtest.LinesCRLF("spec: # c", "  b: >", "    text"),
			moves:  [][2]paths.Path{{spec.Child("b"), spec.Child("n")}},
			want:   stringtest.LinesCRLF("spec: # c", `  "n": >`, "    text"),
		},
		"block mapping with a key comment past the last item": {
			before: stringtest.LinesLF("milk: # oat", "  name: x", "l:", "  - a"),
			moves:  [][2]paths.Path{{milk, paths.MustParse("$.l[1]")}},
			want:   stringtest.LinesLF("l:", "  - a", "  - # oat", "    name: x"),
		},
		"anchored item to an entry": {
			before: stringtest.LinesLF("l:", "  - &a", "    k: v", "  - b"),
			moves:  [][2]paths.Path{{paths.MustParse("$.l[0]"), milk}},
			want:   stringtest.LinesLF("l:", "  - b", "milk: &a", "  k: v"),
		},
		"one-entry mapping item to an entry": {
			before: stringtest.LinesLF("l:", "  - x: 1", "  - x: 2 # two", "m: 1"),
			moves:  [][2]paths.Path{{paths.MustParse("$.l[1]"), milk}},
			want:   stringtest.LinesLF("l:", "  - x: 1", "m: 1", "milk:", "  x: 2 # two"),
		},
		"one-entry mapping item to a new mapping": {
			before: stringtest.LinesLF("l:", "  - x: 1", "  - x: 2", "m: 1"),
			moves:  [][2]paths.Path{{paths.MustParse("$.l[1]"), defaults.Child("milk")}},
			want:   stringtest.LinesLF("l:", "  - x: 1", "m: 1", "defaults:", "  milk:", "    x: 2"),
		},
		"one-item sequence item to an entry": {
			before: stringtest.LinesLF("l:", "  - - a", "  - x"),
			moves:  [][2]paths.Path{{paths.MustParse("$.l[0]"), milk}},
			want:   stringtest.LinesLF("l:", "  - x", "milk:", "  - a"),
		},
		"block scalar into a new mapping": {
			before: stringtest.LinesLF("milk: |", "  oat", "other: 1"),
			moves:  [][2]paths.Path{{milk, defaults.Child("milk")}},
			want:   stringtest.LinesLF("other: 1", "defaults:", "  milk: |", "    oat"),
		},
		"anchored block scalar with a comment": {
			before: stringtest.LinesLF("milk: &m | # kind", "  oat", "other: 1"),
			moves:  [][2]paths.Path{{milk, defaults.Child("milk")}},
			want:   stringtest.LinesLF("other: 1", "defaults:", "  milk: &m | # kind", "    oat"),
		},
		"block scalar past the last item": {
			before: stringtest.LinesLF("milk: |", "  oat", "l:", "  - a"),
			moves:  [][2]paths.Path{{milk, paths.MustParse("$.l[1]")}},
			want:   stringtest.LinesLF("l:", "  - a", "  - |", "    oat"),
		},
		"block scalar with an indentation indicator past the last item": {
			before: stringtest.LinesLF("milk: |2", "    oat", "l:", "  - a"),
			moves:  [][2]paths.Path{{milk, paths.MustParse("$.l[1]")}},
			want:   stringtest.LinesLF("l:", "  - a", "  - |2", "      oat"),
		},
		"plain scalar of two lines into a new mapping": {
			before: stringtest.LinesLF("milk: oat", "  drink", "other: 1"),
			moves:  [][2]paths.Path{{milk, defaults.Child("milk")}},
			want:   stringtest.LinesLF("other: 1", "defaults:", "  milk: oat", "    drink"),
		},
		"anchor past its alias": {
			before: stringtest.LinesLF(
				"name: &n yamull",
				"spec:",
				"  title: *n",
			),
			moves: [][2]paths.Path{{paths.Doc().Child("name"), meta.Child("name")}},
			err:   "test.yaml:3:10: $.spec.title: cafe.jacobcolvin.com/v1 moves $.name to $.metadata.name, after which this alias comes before the anchor that it reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored block mapping past its alias": {
			before: stringtest.LinesLF("milk: &oat", "  name: x # n", "  cost: 5 # kr", "d:", "  m: *oat"),
			moves:  [][2]paths.Path{{milk, defaults.Child("milk")}},
			err:    "test.yaml:5:6: $.d.m: cafe.jacobcolvin.com/v1 moves $.milk to $.defaults.milk, after which this alias comes before the anchor that it reads, and cafe does not write a copy of a value at its alias",
		},
		"inner anchor past its alias": {
			before: stringtest.LinesLF("milk:", "  inner: &i", "    k: 1", "  j: 2", "d: *i"),
			moves:  [][2]paths.Path{{milk, defaults}},
			err:    "test.yaml:5:4: $.d: cafe.jacobcolvin.com/v1 moves $.milk to $.defaults, after which this alias comes before the anchor that it reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored item past its aliases": {
			before: stringtest.LinesLF("l:", "  - &a {k: 1}", "  - *a", "  - x: *a"),
			moves:  [][2]paths.Path{{paths.MustParse("$.l[0]"), milk}},
			err:    "test.yaml:3:5: $.l[1]: cafe.jacobcolvin.com/v1 moves $.l[0] to $.milk, after which this alias comes before the anchor that it reads, and cafe does not write a copy of a value at its alias",
		},
		"value through an alias": {
			// The move takes cost out of the anchored value, as a removal
			// through the alias does.
			before: stringtest.LinesLF("milk: &oat", "  cost: 5", "d:", "  m: *oat"),
			moves:  [][2]paths.Path{{paths.MustParse("$.d.m.cost"), paths.MustParse("$.d.price")}},
			want:   stringtest.LinesLF("milk: &oat {}", "d:", "  m: *oat", "  price: 5"),
		},
		"value through a merge key": {
			before: stringtest.LinesLF("base: &b", "  cost: 5", "soy:", "  <<: *b"),
			moves:  [][2]paths.Path{{paths.MustParse("$.soy.cost"), paths.MustParse("$.soy.price")}},
			want:   stringtest.LinesLF("base: &b {}", "soy:", "  <<: *b", "  price: 5"),
		},
		"target through an alias": {
			// The move writes x into the anchored value, as a Set through the
			// alias does.
			before: stringtest.LinesLF("milk: &oat", "  cost: 5", "d:", "  m: *oat", "x: 1"),
			moves:  [][2]paths.Path{{paths.Doc().Child("x"), paths.MustParse("$.d.m.x")}},
			want:   stringtest.LinesLF("milk: &oat", "  cost: 5", "  x: 1", "d:", "  m: *oat"),
		},
		"target through a merge key": {
			before: stringtest.LinesLF("base: &b", "  k:", "    a: 1", "soy:", "  <<: *b", "x: 1"),
			moves:  [][2]paths.Path{{paths.Doc().Child("x"), paths.MustParse("$.soy.k.x")}},
			want:   stringtest.LinesLF("base: &b", "  k:", "    a: 1", "    x: 1", "soy:", "  <<: *b"),
		},
		"entry into a mapping that an alias reads": {
			// A removal of c and a Set at $.b.q write the same text.
			before: stringtest.JoinLF("a: &x {p: 1}", "b: *x", "c: 2"),
			moves:  [][2]paths.Path{{paths.Doc().Child("c"), paths.MustParse("$.b.q")}},
			want:   stringtest.JoinLF("a: &x {p: 1, q: 2}", "b: *x"),
		},
		"block scalar into a mapping that an inline merge key brings in": {
			before: stringtest.LinesLF("<<: {p: {}}", "r1: |+", "  x # t"),
			moves:  [][2]paths.Path{{paths.Doc().Child("r1"), paths.MustParse("$.p.'16.0'.y")}},
			err:    "test.yaml:1:1: $: cafe.jacobcolvin.com/v1 moves $.r1 to $.p.'16.0'.y, which goes through a merge key that merges a mapping written in place, and cafe does not edit through such a merge key",
		},
		"value into itself through an alias": {
			before: stringtest.LinesLF("a: &x {p: 1}", "b: *x"),
			moves:  [][2]paths.Path{{paths.Doc().Child("a"), paths.MustParse("$.b.q")}},
			err:    `test.yaml:1:1: $.a: cafe.jacobcolvin.com/v1 moves "a" to $.b.q, which lies inside it`,
		},
		"alias before its anchor": {
			// The alias would come before its anchor at target, and the move
			// writes no copy.
			before: stringtest.LinesLF("z:", "  q: 1", "a: &x 1", "b: *x"),
			moves:  [][2]paths.Path{{paths.Doc().Child("b"), paths.MustParse("$.z.b")}},
			err:    "test.yaml:4:4: $.b: cafe.jacobcolvin.com/v1 moves $.b to $.z.b, after which this alias comes before the anchor that it reads, and cafe does not write a copy of a value at its alias",
		},
		"merge key into the anchor that it merges": {
			// At target, the merge key reads a value that holds it.
			before: stringtest.LinesLF("base: &b", "  x: 6", "svc:", "  <<: *b", "  y: 5"),
			moves:  [][2]paths.Path{{paths.Doc().Child("svc"), paths.MustParse("$.base.w0")}},
			err:    "test.yaml:1:7: $.base: cafe.jacobcolvin.com/v1 moves $.svc to $.base.w0, which makes a document with an alias inside the value of the anchor that it reads, such as *b, and cafe does not edit such a document",
		},
		"alias into the anchor that it reads": {
			before: stringtest.LinesLF("base: &b", "  x: 6", "svc:", "  k: *b"),
			moves:  [][2]paths.Path{{paths.Doc().Child("svc"), paths.MustParse("$.base.w0")}},
			err:    "test.yaml:1:7: $.base: cafe.jacobcolvin.com/v1 moves $.svc to $.base.w0, which makes a document with an alias inside the value of the anchor that it reads, such as *b, and cafe does not edit such a document",
		},
		"alias into a mapping inside the anchor that it reads": {
			before: stringtest.LinesLF("base: &b", "  x:", "    z: 6", "svc: [*b]"),
			moves:  [][2]paths.Path{{paths.Doc().Child("svc"), paths.MustParse("$.base.x.w0")}},
			err:    "test.yaml:1:7: $.base: cafe.jacobcolvin.com/v1 moves $.svc to $.base.x.w0, which makes a document with an alias inside the value of the anchor that it reads, such as *b, and cafe does not edit such a document",
		},
		"alias into an anchored value that it does not read": {
			before: stringtest.LinesLF("base:", "  x: &c 6", "svc:", "  k: *c"),
			moves:  [][2]paths.Path{{paths.Doc().Child("svc"), paths.MustParse("$.base.w0")}},
			want:   stringtest.LinesLF("base:", "  x: &c 6", "  w0:", "    k: *c"),
		},
		"NaN": {
			before: stringtest.LinesLF("a: .nan", "b: 1"),
			moves:  [][2]paths.Path{{paths.Doc().Child("a"), defaults.Child("a")}},
			want:   stringtest.LinesLF("b: 1", "defaults:", "  a: .nan"),
		},
		"anchored entry of the mapping that merges it": {
			before: stringtest.LinesLF("soy:", "  base: &b", "    k: 1", "  <<: *b", "  name: s"),
			moves:  [][2]paths.Path{{paths.MustParse("$.soy.base"), paths.MustParse("$.z")}},
			err:    "test.yaml:4:7: $.soy: cafe.jacobcolvin.com/v1 moves $.soy.base to $.z, after which the merge key at 4:3 of $.soy comes before the anchor that it reads, and cafe does not write a copy of a value at its alias",
		},
		"entry that an earlier merge key also brings in": {
			before: stringtest.LinesLF("d: &d", "  port: 80", "svc:", "  <<: *d", "  port: 8080", "l: []"),
			moves:  [][2]paths.Path{{paths.MustParse("$.svc.port"), paths.MustParse("$.l[0]")}},
			err:    "test.yaml:5:3: $.svc.port: cafe.jacobcolvin.com/v1 moves $.svc.port to $.l[0], but <<: *d at 4:3 also brings in port, so $.svc still reads port",
		},
		"tagged empty value before a less indented key": {
			// The parser of go-yaml refuses the text of the value at z.
			before: stringtest.LinesLF("r0:", "  a: 1", "b: 2", "x: !!str"),
			moves:  [][2]paths.Path{{paths.Doc().Child("x"), paths.MustParse("$.r0.z")}},
			err: "test.yaml:3:1: $.b: cafe.jacobcolvin.com/v1 moves $.x to $.r0.z, and go-yaml's parser " +
				"refuses the edited text: unexpected scalar value",
			engine: true,
		},
		"anchored value of a deeper step past its alias": {
			before: stringtest.LinesLF("a: &e", "    k: 1", "b: *e", "m:", "  - 1"),
			moves:  [][2]paths.Path{{paths.Doc().Child("a"), paths.MustParse("$.m[1]")}},
			err:    "test.yaml:3:4: $.b: cafe.jacobcolvin.com/v1 moves $.a to $.m[1], after which this alias comes before the anchor that it reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored value whose copy holds an alias of another anchor of the value": {
			before: stringtest.LinesLF("a:", "  x: &e 1", "  y: &f [*e]", "b: *f", "c: *e"),
			moves:  [][2]paths.Path{{paths.Doc().Child("a"), paths.Doc().Child("z")}},
			err:    "test.yaml:4:4: $.b: cafe.jacobcolvin.com/v1 moves $.a to $.z, after which this alias comes before the anchor that it reads, and cafe does not write a copy of a value at its alias",
		},
		"inner anchor of an anchored value with an alias before the alias of the value": {
			before: stringtest.LinesLF("a: &o", "  x: &e 1", "c: *e", "b: *o", "m:", "  - 1"),
			moves:  [][2]paths.Path{{paths.Doc().Child("a"), paths.MustParse("$.m[1]")}},
			err:    "test.yaml:3:4: $.c: cafe.jacobcolvin.com/v1 moves $.a to $.m[1], after which this alias comes before the anchor that it reads, and cafe does not write a copy of a value at its alias",
		},
		"inner anchor of an anchored value with an alias after the alias of the value": {
			before: stringtest.LinesLF("a: &o", "  x: &e 1", "b: *o", "c: *e", "m:", "  - 1"),
			moves:  [][2]paths.Path{{paths.Doc().Child("a"), paths.MustParse("$.m[1]")}},
			err:    "test.yaml:3:4: $.b: cafe.jacobcolvin.com/v1 moves $.a to $.m[1], after which this alias comes before the anchor that it reads, and cafe does not write a copy of a value at its alias",
		},
		"anchored value with an anchor that the copy defines for target": {
			before: stringtest.LinesLF("a:", "  m1: &f [&h 3]", "  m2: *h", "b: *f", "l:", "  - 0"),
			moves:  [][2]paths.Path{{paths.Doc().Child("a"), paths.MustParse("$.l[1]")}},
			// The alias at b would come before the anchor &f at target.
			err: "test.yaml:4:4: $.b: cafe.jacobcolvin.com/v1 moves $.a to $.l[1], after which this alias comes before the anchor that it reads, and cafe does not write a copy of a value at its alias",
		},
		"anchor before its alias stays at the target": {
			before: stringtest.LinesLF("d:", "  m: 1", "milk: &oat", "  name: x", "l:", "  - *oat"),
			moves:  [][2]paths.Path{{milk, paths.MustParse("$.d.milk")}},
			want:   stringtest.LinesLF("d:", "  m: 1", "  milk: &oat", "    name: x", "l:", "  - *oat"),
		},
		"only entry of the root after a document header without a final newline": {
			before: stringtest.JoinLF("---", "m: 1"),
			moves:  [][2]paths.Path{{paths.Doc().Child("m"), paths.Doc().Child("q")}},
			want:   stringtest.JoinLF("---", "q: 1"),
			value:  map[string]any{"q": uint64(1)},
		},
		"only entry of the root after a comment line without a final newline": {
			before: stringtest.JoinLF("# c", "m: 1"),
			moves:  [][2]paths.Path{{paths.Doc().Child("m"), paths.Doc().Child("q")}},
			want:   stringtest.JoinLF("# c", "q: 1"),
			value:  map[string]any{"q": uint64(1)},
		},
		"only entry of the root, a block scalar without a final newline": {
			before: stringtest.JoinLF("m: |2", "    x"),
			moves:  [][2]paths.Path{{paths.Doc().Child("m"), paths.Doc().Child("q")}},
			want:   stringtest.JoinLF("q: |2", "    x"),
			value:  map[string]any{"q": "  x"},
		},
		"only entry of the root that ends with a block scalar with keep chomping": {
			before: stringtest.LinesLF("m:", "  - |+", "    x", "", ""),
			moves:  [][2]paths.Path{{paths.Doc().Child("m"), paths.Doc().Child("q")}},
			want:   stringtest.LinesLF("q:", "  - |+", "    x", "", ""),
			value:  map[string]any{"q": []any{"x\n\n\n"}},
		},
		"block mapping with a key that holds a colon into a flow mapping": {
			// The key takes the text of go-yaml's flow encoder, which quotes
			// it.
			before: stringtest.LinesLF("m: {a: 1}", "z:", "  x:y: 2"),
			moves:  [][2]paths.Path{{paths.Doc().Child("z"), paths.MustParse("$.m.q")}},
			err:    "test.yaml:3:3: $.z: cafe.jacobcolvin.com/v1 moves $.z to $.m.q, which puts a block value inside a flow collection, and cafe does not move a block value into a flow collection",
		},
		"anchored block mapping with a key that holds a colon into a flow mapping": {
			before: stringtest.LinesLF("m: {a: 1}", "z: &z", "  x:y: 2", "w: *z"),
			moves:  [][2]paths.Path{{paths.Doc().Child("z"), paths.MustParse("$.m.q")}},
			err:    "test.yaml:2:4: $.z: cafe.jacobcolvin.com/v1 moves $.z to $.m.q, which puts a block value inside a flow collection, and cafe does not move a block value into a flow collection",
		},

		"entry to a key that the move writes in quotes on a line of its own in a text of \r\n line breaks": {
			// Control: the token of the quoted key starts with the line break
			// before it, and the key spans no line break.
			before: "r0:\r\n  z: 1\r\n",
			moves:  [][2]paths.Path{{paths.MustParse("$.r0.z"), paths.MustParse("$.-0")}},
			want:   "r0: {}\r\n\"-0\": 1\r\n",
		},
		"empty value under a core tag into a flow mapping": {
			// The move writes the empty string in quotes, which go-yaml and
			// YAML read alike under the tag.
			before: stringtest.LinesLF("r0: [{a: 1}]", "a: !!str"),
			moves:  [][2]paths.Path{{paths.MustParse("$.a"), paths.MustParse("$.r0[0].z")}},
			want:   "r0: [{a: 1, z: !!str ''}]\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before, niceyaml.WithAllowDuplicateKeys(tc.dup))

			err := d.Apply(t.Context(), paths.Doc(), func(_ context.Context, root *edit.Node) error {
				for _, m := range tc.moves {
					err := root.At(m[0]).Move(m[1])
					if err != nil {
						return err
					}
				}

				return nil
			}, label)

			if tc.err != "" {
				assert.Equal(t, tc.engine, errors.Is(err, edit.ErrEngine))
			}

			requireEdit(t, d, err, tc.err, tc.want)

			if tc.value != nil {
				got, err := d.Node().Decode[any](t.Context())
				require.NoError(t, err)
				assert.Equal(t, tc.value, got)
			}
		})
	}

	t.Run("alias that reads a reference document", func(t *testing.T) {
		t.Parallel()

		ref := niceyaml.NewSourceFromString("base: &base {port: 80}\nport: &port 80\n")

		// An anchor of the document that comes before such an alias takes
		// the place of the reference.
		tcs := map[string]struct {
			before string
			from   paths.Path
			to     paths.Path
			want   string
			err    string
		}{
			"alias to a new place": {
				before: stringtest.LinesLF("server: *base # shared", "other: 1"),
				from:   paths.Doc().Child("server"),
				to:     paths.Doc().Child("backend"),
				want:   stringtest.LinesLF("other: 1", "backend: *base # shared"),
			},
			"value that holds the alias to a new place": {
				before: stringtest.LinesLF("svc: {backend: *base} # shared", "other: 1"),
				from:   paths.Doc().Child("svc"),
				to:     paths.Doc().Child("api"),
				want:   stringtest.LinesLF("other: 1", "api: {backend: *base} # shared"),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, tc.before,
					niceyaml.WithName("test.yaml"), niceyaml.WithReferences(ref))

				d, err := edit.New(doc)
				require.NoError(t, err)

				err = d.Apply(t.Context(), tc.from, func(_ context.Context, n *edit.Node) error {
					return n.Move(tc.to)
				}, label)

				requireEdit(t, d, err, tc.err, tc.want)
			})
		}
	})
}

func TestNode_Move_Items(t *testing.T) {
	t.Parallel()

	d := newDoc(t, stringtest.LinesLF("items:", "  - kaffe", "  - te", "  - saft"))

	var nodes []*edit.Node

	// Each item moves after the item before it left the sequence.
	targets := []paths.Path{paths.Doc().Child("first"), paths.Doc().Child("second")}

	err := d.Apply(t.Context(), paths.Doc().Child("items").IndexAll(), func(_ context.Context, n *edit.Node) error {
		nodes = append(nodes, n)

		if len(nodes) > len(targets) {
			return nil
		}

		return n.Move(targets[len(nodes)-1])
	})

	requireEdit(t, d, err, "", stringtest.LinesLF(
		"items:",
		"  - saft",
		"first: kaffe",
		"second: te",
	))

	require.Len(t, nodes, 3)
	assert.Equal(t, paths.Doc().Child("first"), nodes[0].Path())
	assert.Equal(t, paths.Doc().Child("second"), nodes[1].Path())
	assert.Equal(t, paths.Doc().Child("items").Index(0), nodes[2].Path())
}

func TestNode_Move_EmptyIntoFlow(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		before string
		target paths.Path
		want   string
	}{
		"empty value into a flow sequence": {
			before: stringtest.LinesLF("a: 1", "spec:", "f: [1]"),
			target: paths.MustParse("$.f[1]"),
			want:   "test.yaml:2:1: $.f[1]: bad",
		},
		"empty value into a flow mapping": {
			before: stringtest.LinesLF("a: 1", "spec:", "f: {x: 1}"),
			target: paths.MustParse("$.f.y"),
			want:   "test.yaml:2:1: $.f.y: bad",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before)

			// The Set of the first Apply writes a note of its own, which an
			// error at the moved value must not name.
			require.NoError(t, applyAt(t, d, paths.Doc().Child("a"), func(ctx context.Context, n *edit.Node) error {
				return n.Set(ctx, 2)
			}))

			require.NoError(t, applyAt(t, d, spec, func(_ context.Context, n *edit.Node) error {
				return n.Move(tc.target)
			}))

			err := d.Node().Bind(niceyaml.NewError("bad", niceyaml.AtPath(tc.target)))
			assert.Equal(t, tc.want, err.Error())
			assert.NotContains(t, niceyaml.FormatError(err), "sets")
		})
	}
}
