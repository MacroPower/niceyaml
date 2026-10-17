package edit_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/edit"
	"go.jacobcolvin.com/niceyaml/paths"
)

// misreadSource returns the first document of text, named test.yaml, with
// a reference document of each of refs, named ref0.yaml for the first.
func misreadSource(t *testing.T, text string, refs []string, more ...niceyaml.SourceOption) *niceyaml.Node {
	t.Helper()

	opts := append([]niceyaml.SourceOption{niceyaml.WithName("test.yaml")}, more...)
	for i, ref := range refs {
		name := niceyaml.WithName(fmt.Sprintf("ref%d.yaml", i))
		opts = append(opts, niceyaml.WithReferences(niceyaml.NewSourceFromString(ref, name)))
	}

	docs := niceyaml.NewSourceFromString(text, opts...).Documents()
	require.NotEmpty(t, docs)

	return docs[0]
}

func TestNew_Misread(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		text string
		// The texts of the reference documents of the Source.
		refs []string
		// A line of the error of New, which names the site and the misread,
		// or "" for a text that New takes.
		err string
		// The detail of that error, which tells what to write.
		fix string
		// The number of times New runs on the text, each of which must give
		// the error, or 0 for once.
		repeat int
		// Whether the Source allows duplicate keys.
		dup bool
	}{
		"own key before a merge key, then a merge key of a mapping that merges a reference document": {
			// The engine does not read the reference document, which the
			// mapping that the later merge key brings in merges, so the
			// message names no merge key that go-yaml's decoder reads.
			text: stringtest.LinesLF("x: &x {a: 2}", "y: &y {<<: *r}", "m:", "  a: 1", "  <<: *x", "  <<: *y"),
			refs: []string{"r: &r {a: 9, k: 1}\n"},
			dup:  true,
			err: "test.yaml:4:3: $.m.a: go-yaml's decoder reads $.m.a from a merge key after it, but the merge spec " +
				"keeps the own entry written before it",
		},
		"key that two mappings bring in, before a mapping that merges a reference document": {
			text: stringtest.LinesLF("x: &x {a: 1}", "y: &y {a: 2}", "z: &z {<<: *r}", "m:", "  <<: [*x, *y, *z]"),
			refs: []string{"r: &r {a: 9, k: 1}\n"},
			err: "test.yaml:5:3: $.m: go-yaml's decoder reads $.m.a from a source after the source at 1:8 of the " +
				"merge key at 5:3, but the merge spec keeps that earlier source",
		},
		"key that two mappings bring in, before a later merge key of a mapping that merges a reference document": {
			text: stringtest.LinesLF("x: &x {a: 1}", "y: &y {a: 2}", "z: &z {<<: *r}", "m:", "  <<: [*x, *y]",
				"  <<: *z"),
			refs: []string{"r: &r {a: 9, k: 1}\n"},
			dup:  true,
			err: "test.yaml:5:3: $.m: go-yaml's decoder reads $.m.a from a source after the source at 1:8 of the " +
				"merge key at 5:3, but the merge spec keeps that earlier source",
		},
		"key that two mappings bring in, before a later merge key that brings it in too": {
			// Go-yaml's decoder reads the source of the later merge key.
			text: stringtest.LinesLF("x: &x {a: 1}", "y: &y {a: 2}", "z: &z {a: 3}", "m:", "  <<: [*x, *y]",
				"  <<: *z"),
			dup: true,
			err: "test.yaml:5:3: $.m: go-yaml's decoder reads $.m.a from a source after the source at 1:8 of the " +
				"merge key at 5:3, but the merge spec keeps that earlier source",
		},
		"key that two mappings bring in, before a later merge key without it": {
			// A control: go-yaml's decoder reads the later of the two mappings.
			text: stringtest.LinesLF("x: &x {a: 1}", "y: &y {a: 2}", "z: &z {b: 3}", "m:", "  <<: [*x, *y]",
				"  <<: *z"),
			dup: true,
			err: "test.yaml:5:3: $.m: go-yaml's decoder reads $.m.a from the source at 2:8 of the merge key at 5:3, " +
				"but the merge spec keeps the earlier source at 1:8",
		},
		"key that two mappings bring in after an alias of a reference document": {
			// The reference document can hold the key, which the merge spec
			// then keeps, so the message names no source that it keeps.
			text: stringtest.LinesLF("l: &l {a: 1}", "l2: &l2 {a: 2}", "m:", "  <<: [*r, *l, *l2]"),
			refs: []string{"r: &r {a: 9, z: 9}\n"},
			err: "test.yaml:4:3: $.m: go-yaml's decoder reads $.m.a from the source at 2:10 of the merge key at 4:3, " +
				"but the merge spec keeps an earlier source",
		},
		"key that two mappings bring in after a mapping that merges a reference document": {
			text: stringtest.LinesLF("z: &z {<<: *r}", "l: &l {a: 1}", "l2: &l2 {a: 2}", "m:", "  <<: [*z, *l, *l2]"),
			refs: []string{"r: &r {a: 9}\n"},
			err: "test.yaml:5:3: $.m: go-yaml's decoder reads $.m.a from the source at 3:10 of the merge key at 5:3, " +
				"but the merge spec keeps an earlier source",
		},
		"key that two mappings bring in, with an alias of a reference document between them": {
			// A control: the reference document comes after the source that
			// the merge spec keeps.
			text: stringtest.LinesLF("l: &l {a: 1}", "l2: &l2 {a: 2}", "m:", "  <<: [*l, *r, *l2]"),
			refs: []string{"r: &r {a: 9}\n"},
			err: "test.yaml:4:3: $.m: go-yaml's decoder reads $.m.a from the source at 2:10 of the merge key at 4:3, " +
				"but the merge spec keeps the earlier source at 1:8",
		},
		"key that two mappings bring in under an alias key": {
			// No path names the mapping below the alias key, so the message
			// names the key alone, and the error binds at the root.
			text: stringtest.LinesLF("s: &s {a: 1}", "t: &t {a: 2}", "m: &m {z: 1}", "!!str *m :", "  b:",
				"    <<: [*s, *t]"),
			err: "test.yaml:6:5: $: go-yaml's decoder reads the key a from the source at 2:8 of the merge key at 6:5, " +
				"but the merge spec keeps the earlier source at 1:8",
		},
		"own key before a merge key under an alias key": {
			text: stringtest.LinesLF("s: &s {a: 1}", "m: &m {z: 1}", "*m :", "  b:", "    a: 0", "    <<: *s"),
			err: "test.yaml:5:5: $: go-yaml's decoder reads the key a from the merge key at 6:5, but the merge " +
				"spec keeps the own entry written before it",
		},
		"own key before a merge key whose alias reads a sequence of mappings": {
			// Go-yaml's decoder merges each mapping of the sequence that the
			// alias reads.
			text: stringtest.LinesLF("l: &l", "  - a: 1", "m:", "  a: 0", "  <<: *l"),
			err: "test.yaml:4:3: $.m.a: go-yaml's decoder reads $.m.a from the merge key at 5:3, but the merge spec " +
				"keeps the own entry written before it",
			fix: "write the merge key before a, where both readings agree",
		},
		"key that two mappings of a sequence that an alias of a merge key reads bring in": {
			text: stringtest.LinesLF("l: &l [{a: 1}, {a: 2}]", "m:", "  <<: *l"),
			err: "test.yaml:3:3: $.m: go-yaml's decoder reads $.m.a from the source at 1:17 of the merge key at 3:3, " +
				"but the merge spec keeps the earlier source at 1:9",
		},
		"own key of another text before a merge key that brings in the key": {
			// Go-yaml's decoder reads the key 0x1 as 1, so the merge key
			// overrides it. The misread comes before the key text that New
			// refuses.
			text: stringtest.LinesLF("s: &s {1: x}", "m:", "  0x1: y", "  <<: *s"),
			err: "test.yaml:3:3: $.m.0x1: go-yaml's decoder reads $.m.0x1 from the merge key at 4:3, but the merge " +
				"spec keeps the own entry written before it",
			fix: "write the merge key before 0x1, where both readings agree",
		},
		"own null key before a merge key that brings in a null key": {
			text: stringtest.LinesLF("s: &s {~: x}", "m:", "  null: y", "  <<: *s"),
			err: "test.yaml:3:3: $.m.null: go-yaml's decoder reads $.m.null from the merge key at 4:3, but the " +
				"merge spec keeps the own entry written before it",
		},
		"own key before a merge key whose source writes an alias of the key": {
			// Control: go-yaml's decoder reads the alias key as the key True
			// that its anchor holds, so the merge key overrides it.
			text: stringtest.LinesLF("&k True: 1", "<<: {*k : 2}"),
			err: "test.yaml:1:1: $.True: go-yaml's decoder reads $.True from the merge key at 2:1, but the merge " +
				"spec keeps the own entry written before it",
		},
		"own key before a merge key whose source holds a key of the same text that reads apart": {
			// Go-yaml's decoder reads the string "~" and the null key ~ of
			// the reference document as two keys, so the merge key keeps
			// the own entry.
			text: stringtest.LinesLF("m:", `  "~": y`, "  <<: *s"),
			refs: []string{"s: &s {~: x}\n"},
		},
		"own key before a merge key and again after it": {
			// Control: go-yaml's decoder reads the last own entry, as the
			// model reads it.
			text: stringtest.LinesLF("s: &s {a: 1}", "m:", "  a: 2", "  <<: *s", "  a: 3"),
			dup:  true,
		},
		"own key before two merge keys that bring in the key": {
			// Go-yaml's decoder reads the entry of the last merge key.
			text: stringtest.LinesLF("s: &s {a: 1}", "t: &t {a: 5}", "m:", "  a: 2", "  <<: *s", "  <<: *t"),
			dup:  true,
			err: "test.yaml:4:3: $.m.a: go-yaml's decoder reads $.m.a from the merge key at 6:3, but the merge spec " +
				"keeps the own entry written before it",
		},
		"own key written twice before a merge key that brings in the key": {
			// The misread binds at the last own entry, which the model reads.
			text: stringtest.LinesLF("s: &s {a: 1}", "m:", "  a: 2", "  a: 3", "  <<: *s"),
			dup:  true,
			err: "test.yaml:4:3: $.m.a: go-yaml's decoder reads $.m.a from the merge key at 5:3, but the merge spec " +
				"keeps the own entry written before it",
		},
		"own key before a merge key that brings in the key": {
			text: stringtest.LinesLF("s: &s {a: 1}", "m: {a: 2, <<: *s}"),
			err: "test.yaml:2:5: $.m.a: go-yaml's decoder reads $.m.a from the merge key at 2:11, but the merge spec " +
				"keeps the own entry written before it",
			fix: "write the merge key before a, where both readings agree",
		},
		"own key after a merge key that brings in the key": {
			// Control: go-yaml's decoder and the merge spec keep the own entry.
			text: stringtest.LinesLF("s: &s {a: 1}", "m: {<<: *s, a: 2}"),
		},
		"key that two sources of a merge key bring in": {
			text: stringtest.LinesLF("a: &a {k: 1}", "b: &b {k: 2}", "m:", "  <<: [*a, *b]"),
			err: "test.yaml:4:3: $.m: go-yaml's decoder reads $.m.k from the source at 2:8 of the merge key at 4:3, " +
				"but the merge spec keeps the earlier source at 1:8",
			fix: "write k in one source of the merge key, or after the merge key",
		},
		"key that two sources of a merge key bring in, which the mapping writes after it": {
			// Control: the own entry hides both sources.
			text: stringtest.LinesLF("a: &a {k: 1}", "b: &b {k: 2}", "m:", "  <<: [*a, *b]", "  k: 3"),
		},
		"own key before a merge key that brings in the own key of another mapping": {
			// The mapping k0 merges itself, so both readings keep its own p
			// there, but k1 reads p of k0 from the merge key.
			text: stringtest.LinesLF("k0: &a", "  p: 1", "  <<: *a", "k1:", "  p: 2", "  <<: *a"),
			err: "test.yaml:5:3: $.k1.p: go-yaml's decoder reads $.k1.p from the merge key at 6:3, but the merge " +
				"spec keeps the own entry written before it",
		},
		"keys that two sources of a merge key bring in, in the order of the later source": {
			text: stringtest.LinesLF("a: &a {k: 1, j: 1}", "b: &b {j: 2, k: 2}", "m:", "  <<: [*a, *b]"),
			err:  "test.yaml:4:3: $.m: go-yaml's decoder reads $.m.j from the source at 2:8 of the merge key at 4:3",
		},
		"own key before a merge key whose mapping has a tag": {
			// Go-yaml's decoder merges the mapping under the tag.
			text: "m: {a: 2, <<: !!map {a: 1}}\n",
			err: "test.yaml:1:5: $.m.a: go-yaml's decoder reads $.m.a from the merge key at 1:11, but the merge spec " +
				"keeps the own entry written before it",
		},
		"own key before a merge key whose sequence of aliases has a tag": {
			text: stringtest.LinesLF("s: &s {a: 1}", "m: {a: 2, <<: !!seq [*s]}"),
			err: "test.yaml:2:5: $.m.a: go-yaml's decoder reads $.m.a from the merge key at 2:11, but the merge spec " +
				"keeps the own entry written before it",
		},
		"key that two sources of a merge key bring in, one of them under a tag": {
			// The source *b brings in x through its own merge key at 2:8
			// (fix19 G2-3).
			text: stringtest.LinesLF("a: &a {x: 1}", "b: &b {<<: !!map {x: 2}}", "m: {<<: [*a, *b]}"),
			err: "test.yaml:3:5: $.m: go-yaml's decoder reads $.m.x from the source at 2:8 of the merge key at 3:5, " +
				"but the merge spec keeps the earlier source at 1:8",
		},
		"key that a later source of a merge key brings in through a merge key of its own": {
			// The message names the source of the merge key at 6:3, z, by its
			// merge key that brings in a (fix19 G2-3).
			text: stringtest.LinesLF(
				"x: &x {a: 1}", "y: &y {a: 2}", "v: &v {a: 3}", "z: &z {<<: *v}", "m:", "  <<: [*x, *y, *z]",
			),
			err: "test.yaml:6:3: $.m: go-yaml's decoder reads $.m.a from the source at 4:8 of the merge key at 6:3, " +
				"but the merge spec keeps the earlier source at 1:8",
		},
		"key that the first source of a merge key brings in through a merge key of its own": {
			text: stringtest.LinesLF("v: &v {a: 3}", "z: &z {<<: *v}", "y: &y {a: 2}", "m:", "  <<: [*z, *y]"),
			err: "test.yaml:5:3: $.m: go-yaml's decoder reads $.m.a from the source at 3:8 of the merge key at 5:3, " +
				"but the merge spec keeps the earlier source at 2:8",
		},
		"own key before a merge key of an anchor that a later anchor reuses": {
			// Either binding of *x0 gives a source that holds c, so the
			// misread comes before the reused anchor.
			text: stringtest.LinesLF("r0: &x0", "  <<: &x0 {c: x}", "r1:", "  c: 1", "  <<: *x0"),
			err: "test.yaml:4:3: $.r1.c: go-yaml's decoder reads $.r1.c from the merge key at 5:3, but the merge " +
				"spec keeps the own entry written before it",
		},
		"own key before a merge key of an anchor that the mapping of the anchor defines again": {
			// Go-yaml binds *k0 to the outer anchor, whose mapping holds b, so
			// its decoder reads $.b from the merge key. The misread comes before
			// the reused anchor.
			text: stringtest.LinesLF("b: &k0 {c: 1, b: &k0 ~}", "<<: *k0"),
			err: "test.yaml:1:1: $.b: go-yaml's decoder reads $.b from the merge key at 2:1, but the merge spec " +
				"keeps the own entry written before it",
		},
		"key that two sources of a merge key bring in, one an alias of an anchor that a later anchor reuses": {
			// Either binding of *x0 gives a first source that holds c. The
			// alias reads the mapping of r0, which brings in c through its
			// merge key at 2:3 (fix19 G2-3).
			text: stringtest.LinesLF("r0: &x0", "  <<: &x0 {c: x}", "r1:", "  <<: [*x0, {c: 1}]"),
			err: "test.yaml:4:3: $.r1: go-yaml's decoder reads $.r1.c from the source at 4:14 of the merge key at " +
				"4:3, but the merge spec keeps the earlier source at 2:3",
		},
		"byte order mark before the document": {
			text: "\ufeffa: 1\n",
			err:  "test.yaml:1:1: $: go-yaml reads the byte order mark at 1:1 as text of the document",
			fix:  "remove the byte order mark",
		},
		"byte order mark and an own key before a merge key at one place": {
			// The first instance by the order of the list.
			text: stringtest.LinesLF("\ufeffa: 2", "<<: {a: 1}"),
			err: "test.yaml:1:1: $.a: go-yaml's decoder reads $.a from the merge key at 2:1, but the merge spec keeps " +
				"the own entry written before it",
		},
		"two misreads on two lines": {
			// Control: the first by its line.
			text: stringtest.LinesLF("s: &s {k: 1}", "m: {k: 2, <<: *s}", "n: {k: 3, <<: *s}"),
			err: "test.yaml:2:5: $.m.k: go-yaml's decoder reads $.m.k from the merge key at 2:11, but the merge spec " +
				"keeps the own entry written before it",
		},
		"misread after a construct of the subset": {
			// Control: the misread comes before the alias inside its own
			// anchor.
			text: stringtest.LinesLF("a: &a [*a]", "s: &s {k: 1}", "m: {k: 2, <<: *s}"),
			err: "test.yaml:3:5: $.m.k: go-yaml's decoder reads $.m.k from the merge key at 3:11, but the merge spec " +
				"keeps the own entry written before it",
		},
		"own key before a merge key of a mapping that a later entry of its key hides": {
			// The path $.k.a reads the later entry of k, so the message names
			// the key alone. The problem binds under the path that the user's
			// text spells for the entry, though a call by that path reads the
			// later entry (fix19 P56).
			text: stringtest.LinesLF("s: &s {a: 2}", "k: {a: 1, <<: *s}", "k: {b: 1}"),
			dup:  true,
			err: "test.yaml:2:5: $.k.a: go-yaml's decoder reads the key a from the merge key at 2:11, but the " +
				"merge spec keeps the own entry written before it",
		},
		"own key before a merge key of a mapping written in place of a merge source": {
			// No path names the mapping under the outer merge key, so the
			// problem binds under the path of the mapping that merges it.
			text: stringtest.LinesLF("s: &s {a: 2}", "m:", "  <<: {a: 1, <<: *s}"),
			err: "test.yaml:3:8: $.m: go-yaml's decoder reads the key a from the merge key at 3:14, but the " +
				"merge spec keeps the own entry written before it",
		},
		"key that two mappings bring in, in a mapping that a later entry of its key hides": {
			// The merge key binds under the path of the mapping that holds it in
			// the user's text, the earlier k (fix19 P56).
			text: stringtest.LinesLF("s: &s {a: 2}", "t: &t {a: 3}", "k: {<<: [*s, *t]}", "k: {b: 1}"),
			dup:  true,
			err: "test.yaml:3:5: $.k: go-yaml's decoder reads the key a from the source at 2:8 of the merge key at " +
				"3:5, but the merge spec keeps the earlier source at 1:8",
		},
		"own key before a merge key of the later entry of a key": {
			// A control: the path $.k.a reads the entry of the mapping.
			text: stringtest.LinesLF("s: &s {a: 2}", "k: {b: 1}", "k: {a: 1, <<: *s}"),
			dup:  true,
			err: "test.yaml:3:5: $.k.a: go-yaml's decoder reads $.k.a from the merge key at 3:11, but the merge spec " +
				"keeps the own entry written before it",
		},
		"key that two sources of a merge key bring in, before an alias of a reference document": {
			// The engine does not read the reference document, which can bring
			// the key in again, so the message names only the source that the
			// merge spec keeps (fix19 P57).
			text: stringtest.LinesLF("l1: &l1 {a: 1}", "l2: &l2 {a: 2}", "m:", "  <<: [*l1, *l2, *r]"),
			refs: []string{"r: &r {b: 2, a: 3}\n"},
			err: "test.yaml:4:3: $.m: go-yaml's decoder reads $.m.a from a source after the source at 1:10 of the " +
				"merge key at 4:3, but the merge spec keeps that earlier source",
		},
		"key that two sources of a merge key bring in, before an alias of a reference document without the key": {
			// The engine does not read which keys the reference document holds
			// (fix19 P57).
			text: stringtest.LinesLF("l1: &l1 {a: 1}", "l2: &l2 {a: 2}", "m:", "  <<: [*l1, *l2, *r]"),
			refs: []string{"r: &r {b: 2}\n"},
			err: "test.yaml:4:3: $.m: go-yaml's decoder reads $.m.a from a source after the source at 1:10 of the " +
				"merge key at 4:3, but the merge spec keeps that earlier source",
		},
		"key that two sources of a tagged merge sequence bring in, before an alias of a reference document": {
			text: stringtest.LinesLF("l1: &l1 {a: 1}", "l2: &l2 {a: 2}", "m:", "  <<: !!seq [*l1, *l2, *r]"),
			refs: []string{"r: &r {b: 2, a: 3}\n"},
			err: "test.yaml:4:3: $.m: go-yaml's decoder reads $.m.a from a source after the source at 1:10 of the " +
				"merge key at 4:3, but the merge spec keeps that earlier source",
		},
		"key that two sources of a merge key bring in, before a merge key of a reference document": {
			// The later merge key can bring the key in again (fix19 P57).
			text: stringtest.LinesLF("l1: &l1 {a: 1}", "l2: &l2 {a: 2}", "m:", "  <<: [*l1, *l2]", "  <<: *r"),
			refs: []string{"r: &r {b: 2, a: 3}\n"},
			dup:  true,
			err: "test.yaml:4:3: $.m: go-yaml's decoder reads $.m.a from a source after the source at 1:10 of the " +
				"merge key at 4:3, but the merge spec keeps that earlier source",
		},
		"key that two sources of a merge key bring in, after an alias of a reference document": {
			// Control: the later source of the document decides, so the message
			// names it.
			text: stringtest.LinesLF("l1: &l1 {a: 1}", "l2: &l2 {a: 2}", "m:", "  <<: [*l1, *r, *l2]"),
			refs: []string{"r: &r {b: 2, a: 3}\n"},
			err: "test.yaml:4:3: $.m: go-yaml's decoder reads $.m.a from the source at 2:10 of the merge key at 4:3, " +
				"but the merge spec keeps the earlier source at 1:10",
		},
		"own key before a merge key, before a merge key of an alias of a reference document": {
			// The later merge key can bring the key in again, so the message
			// names no merge key (fix19 P57).
			text: stringtest.LinesLF("l: &l {a: 2}", "m:", "  a: 1", "  <<: *l", "  <<: *r"),
			refs: []string{"r: &r {b: 2, a: 3}\n"},
			dup:  true,
			err: "test.yaml:3:3: $.m.a: go-yaml's decoder reads $.m.a from a merge key after it, but the merge spec " +
				"keeps the own entry written before it",
		},
		"own key before a merge key whose sequence ends with an alias of a reference document": {
			// The alias of the reference document comes after the source that
			// brings in the key and can bring it in again. So the message names
			// no merge key (fix19 P57).
			text: stringtest.LinesLF("l: &l {a: 2}", "m:", "  a: 1", "  <<: [*l, *r]"),
			refs: []string{"r: &r {b: 2, a: 3}\n"},
			err: "test.yaml:3:3: $.m.a: go-yaml's decoder reads $.m.a from a merge key after it, but the merge spec " +
				"keeps the own entry written before it",
		},
		"own key before a merge key whose sequence ends with an alias of a reference document without the key": {
			// The engine does not read which keys the reference document holds
			// (fix19 P57).
			text: stringtest.LinesLF("l: &l {a: 2}", "m:", "  a: 1", "  <<: [*l, *r]"),
			refs: []string{"r: &r {b: 2}\n"},
			err: "test.yaml:3:3: $.m.a: go-yaml's decoder reads $.m.a from a merge key after it, but the merge spec " +
				"keeps the own entry written before it",
		},
		"own key before a merge key whose sequence starts with an alias of a reference document": {
			// Control: the source of the document after the alias decides, so
			// the message names its merge key.
			text: stringtest.LinesLF("l: &l {a: 2}", "m:", "  a: 1", "  <<: [*r, *l]"),
			refs: []string{"r: &r {b: 2, a: 3}\n"},
			err: "test.yaml:3:3: $.m.a: go-yaml's decoder reads $.m.a from the merge key at 4:3, but the merge spec " +
				"keeps the own entry written before it",
		},
		"own key before a merge key, after a merge key of an alias of a reference document": {
			// Control: the later merge key of the document decides.
			text: stringtest.LinesLF("l: &l {a: 2}", "m:", "  a: 1", "  <<: *r", "  <<: *l"),
			refs: []string{"r: &r {b: 2, a: 3}\n"},
			dup:  true,
			err: "test.yaml:3:3: $.m.a: go-yaml's decoder reads $.m.a from the merge key at 5:3, but the merge spec " +
				"keeps the own entry written before it",
		},
		"own key before a merge key of an alias of a reference document": {
			// New reads the kinds in the tree of the document alone, so it
			// takes the text. A path through a that a call or a read takes
			// fails as a path through the alias does.
			text: stringtest.LinesLF("m:", "  a: 2", "  <<: *r"),
			refs: []string{"r: &r {a: 1}\n"},
		},
		"key that two sources of a merge key bring in, one of them from a reference document": {
			// New does not read the keys that the reference document holds.
			text: stringtest.LinesLF("s: &s {x: 2}", "m:", "  <<: [*r, *s]"),
			refs: []string{"r: &r {x: 1}\n"},
		},
		"alias of a mapping that a reference document merges with an own key first": {
			// New does not read the tree of a reference document.
			text: "m: *s\n",
			refs: []string{"t: &t {a: 1}\ns: &s {a: 2, <<: *t}\n"},
		},
		"merge key of a reference document of another one that it reads": {
			text: "a: *s\n",
			refs: []string{"s: &s {p: 2, <<: *t}\n", "t: &t {p: 1}\n"},
		},
		"byte order mark of a reference document that an alias reads": {
			// The Source of the reference document drops the mark.
			text: "m: *r\n",
			refs: []string{"\ufeffr: &r 1\n"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var opts []niceyaml.SourceOption

			if tc.dup {
				opts = append(opts, niceyaml.WithAllowDuplicateKeys(true))
			}

			d, err := edit.New(misreadSource(t, tc.text, tc.refs, opts...), edit.WithApp("cafe"))
			if tc.err == "" {
				require.NoError(t, err)
				assert.Equal(t, tc.text, d.Text())

				return
			}

			for range tc.repeat {
				_, again := edit.New(misreadSource(t, tc.text, tc.refs, opts...), edit.WithApp("cafe"))
				require.Error(t, again)
				require.Equal(t, niceyaml.FormatError(err), niceyaml.FormatError(again))
			}

			require.ErrorIs(t, err, edit.ErrMisread)
			require.ErrorIs(t, err, edit.ErrEngine)
			require.NotErrorIs(t, err, edit.ErrUnsupported)
			assert.Contains(t, niceyaml.FormatError(err), tc.err)
			assert.NotContains(t, niceyaml.FormatError(err), "report this")

			if tc.fix != "" {
				assert.Contains(t, niceyaml.FormatError(err), "`-- "+tc.fix+"\n")
			}

			// The text is valid YAML, so neither the error nor its problem is
			// the fault of the document.
			assert.False(t, niceyaml.IsInvalid(err))

			problems := 0

			for p := range niceyaml.NewErrorTree(err).Problems() {
				problems++

				assert.False(t, p.IsInvalid())
				assert.Contains(t, niceyaml.FormatError(err), p.Message())
			}

			assert.Equal(t, 1, problems)
		})
	}
}

// wholeValue returns the value at p of v, the decode of a whole document
// into any, and whether v holds p.
func wholeValue(v any, p paths.Path) (any, bool) {
	for sel := range p.Selectors() {
		switch sel.Kind {
		case paths.SelectorChild:
			m, ok := v.(map[string]any)
			if !ok {
				return nil, false
			}

			v, ok = m[sel.Name]
			if !ok {
				return nil, false
			}

		case paths.SelectorIndex:
			s, ok := v.([]any)
			if !ok || sel.Index < 0 || sel.Index >= len(s) {
				return nil, false
			}

			v = s[sel.Index]

		default:
			return nil, false
		}
	}

	return v, true
}

// wholePaths returns the path of each key and each index of v, the decode
// of a whole document into any, below p.
func wholePaths(p paths.Path, v any) []paths.Path {
	var out []paths.Path

	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			q := p.Child(k)
			out = append(out, q)
			out = append(out, wholePaths(q, e)...)
		}

	case []any:
		for i, e := range x {
			q := p.Index(i)
			out = append(out, q)
			out = append(out, wholePaths(q, e)...)
		}
	}

	return out
}

func TestNew_GoYAMLReading(t *testing.T) {
	t.Parallel()

	// Each text is valid YAML that go-yaml reads otherwise than YAML does.
	// New takes it, and each read at a path returns what niceyaml's decode
	// of the whole document holds at that path.
	tcs := map[string]struct {
		text string
		// The texts of the reference documents of the Source.
		refs []string
	}{
		"tab after a tag": {
			text: stringtest.LinesLF("a: !!int\t5", "z: 1"),
		},
		"tag alone on an empty value before a line at the column of its key": {
			text: stringtest.LinesLF(
				"Resources:",
				"  Group:",
				"    Properties:",
				"      AvailabilityZones: !GetAZs",
				"      LaunchTemplate:",
				"        Id: x",
				"  Other: 1",
			),
		},
		"tag before an anchor on a number": {
			text: stringtest.LinesLF("a: !t &k 1", "b: *k"),
		},
		"tag before a comment and its scalar on the next line": {
			text: stringtest.LinesLF("a: !t # c", "  1"),
		},
		"verbatim tag of the core schema": {
			text: "a: !<tag:yaml.org,2002:int> 5\n",
		},
		"tag that a %TAG directive gives the prefix of the core schema": {
			text: stringtest.LinesLF("%TAG !e! tag:yaml.org,2002:", "---", "a: !e!int 5"),
		},
		"tag with an escape in its shorthand": {
			text: "a: !!b%6Fol \"true\"\n",
		},
		"core tag before an anchor that an alias reads": {
			text: stringtest.LinesLF("a: !!int &k 5", "b: *k"),
		},
		"core tag on a block scalar": {
			text: stringtest.LinesLF("a: !!int |", "  5"),
		},
		"core tag on a number with a leading zero": {
			text: "a: !!int 017\n",
		},
		"block scalar with strip chomping whose last line ends with spaces": {
			text: stringtest.LinesLF("m:", "  a: |-", "    x  ", "z: 1"),
		},
		"block scalar whose first line starts with a tab after its indentation": {
			text: stringtest.LinesLF("b: |", "  \tx", "   y", "z: 0"),
		},
		"folded block scalar at the end of a text without a line break": {
			text: "a: >\n  x\n  y",
		},
		"block scalar with keep chomping and no line at the end of the text": {
			text: "a: |+\n",
		},
		"double-quoted scalar over lines with \\r\\n line breaks": {
			text: "a: \"x\r\n  y\"\r\nz: 0\r\n",
		},
		"single-quoted scalar with a tab at the end of a line": {
			text: "a: 'x\t\n  y'\nz: 0\n",
		},
		"merge key with a tab after its colon": {
			// Go-yaml's decoder and the readers of niceyaml read the string
			// key "<<".
			text: stringtest.LinesLF("s: &s {a: 1}", "m:", "  <<:\t*s", "  b: 2"),
		},
		"merge key with a tab before its colon": {
			text: stringtest.LinesLF("s: &s {a: 1}", "m:", "  <<\t: *s", "  b: 2"),
		},
		"explicit merge key": {
			// Control: go-yaml's decoder and the readers of niceyaml read the
			// string key "<<".
			text: stringtest.LinesLF("s: &s {a: 2}", "m:", "  ? <<", "  : *s"),
		},
		"tag at the end of a text without a final line break": {
			text: "a: 1\nb: !t",
		},
		"core tag before an anchor with its scalar on the next line": {
			text: stringtest.LinesLF("a: !!int &k", "  1", "b: 2"),
		},
		"alias of a reference document that go-yaml misreads": {
			text: "m: *r\n",
			refs: []string{"r: !!int &r \"5\"\n"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := misreadSource(t, tc.text, tc.refs)

			whole, err := doc.Decode[any](t.Context())
			require.NoError(t, err)

			d, err := edit.New(doc)
			require.NoError(t, err)
			assert.Equal(t, tc.text, d.Text())

			ps := append([]paths.Path{paths.Doc()}, wholePaths(paths.Doc(), whole)...)

			err = d.Apply(t.Context(), paths.Doc(), func(ctx context.Context, root *edit.Node) error {
				for _, p := range ps {
					want, ok := wholeValue(whole, p)
					require.True(t, ok)

					n := root
					if !p.IsRoot() {
						n = root.At(p)
					}

					got, err := n.Decode[any](ctx)
					require.NoError(t, err, p.String())
					assert.Equal(t, want, got, p.String())
					assert.True(t, n.Exists(), p.String())
				}

				return nil
			})
			require.NoError(t, err)
		})
	}
}

func TestDoc_Apply_Misread(t *testing.T) {
	t.Parallel()

	set := func(path string, v any) edit.Func {
		return func(ctx context.Context, root *edit.Node) error {
			return root.At(paths.MustParse(path)).Set(ctx, v)
		}
	}

	tcs := map[string]struct {
		fn     edit.Func
		before string
		// A line of the error of the Apply, which matches edit.ErrEngine but
		// not edit.ErrMisread.
		err string
		// The detail of that error, which tells what to write, or "".
		fix string
		// The detail of that error that names the call, which comes last.
		call string
	}{
		"new key in a merge source that an own key before the merge key holds": {
			// Control: the model of the calls holds the merge key as an entry,
			// and the check of the misreads of merge keys after the call finds
			// the own key.
			before: stringtest.LinesLF("s: &s {a: 1}", "m:", "  c: 2", "  <<: *s"),
			fn:     set("$.s.c", 3),
			err: "test.yaml:3:3: $.m.c: go-yaml's decoder reads $.m.c from the merge key at 4:3 in the edited text, " +
				"but the merge spec keeps the own entry written before it, and the edit sets $.s.c",
			fix:  "write the merge key before c, where both readings agree",
			call: "the edit sets $.s.c",
		},
		"rename in a merge source to a key that an own key before the merge key holds": {
			// Control.
			before: stringtest.LinesLF("s: &s {a: 1}", "m:", "  c: 2", "  <<: *s"),
			fn: func(_ context.Context, root *edit.Node) error {
				return root.At(paths.MustParse("$.s.a")).SetKey("c")
			},
			err: "test.yaml:3:3: $.m.c: go-yaml's decoder reads $.m.c from the merge key at 4:3 in the edited text, " +
				`but the merge spec keeps the own entry written before it, and the edit renames $.s.a to "c"`,
			call: `the edit renames $.s.a to "c"`,
		},
		"set of a source of a merge sequence to a mapping that holds a key of another source": {
			// Control.
			before: stringtest.LinesLF("a: &a {k: 1}", "b: &b {j: 2}", "m:", "  <<: [*a, *b]"),
			fn:     set("$.b", map[string]int{"k": 3}),
			err: "test.yaml:4:3: $.m: go-yaml's decoder reads $.m.k from a later source of a merge key in the edited " +
				"text, but the merge spec keeps the earlier source, and the edit sets $.b",
			fix:  "write k in one source of the merge key, or after the merge key",
			call: "the edit sets $.b",
		},
		"removal that leaves a byte order mark at the start of the text": {
			// The tokenizer of the Source drops the mark that the removal leaves
			// at the start of the text, so the key would read as b.
			before: stringtest.LinesLF("a: 1", "\ufeffb: 2"),
			fn: func(_ context.Context, root *edit.Node) error {
				return root.At(paths.MustParse("$.a")).Remove()
			},
			err: "test.yaml:2:1: $: go-yaml reads the byte order mark at 2:1 as text of the document, but the " +
				"tokenizer of the Source drops it in the edited text, and the edit removes $.a",
			fix:  "remove the byte order mark",
			call: "the edit removes $.a",
		},
		"removal that leaves a byte order mark at the start of a later document": {
			before: stringtest.LinesLF("---", "a: 1", "\ufeffb: 2"),
			fn: func(_ context.Context, root *edit.Node) error {
				return root.At(paths.MustParse("$.a")).Remove()
			},
			err: "test.yaml:3:1: $: go-yaml reads the byte order mark at 3:1 as text of the document, but the " +
				"tokenizer of the Source drops it in the edited text, and the edit removes $.a",
			fix:  "remove the byte order mark",
			call: "the edit removes $.a",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before)

			err := d.Apply(t.Context(), paths.Doc(), tc.fn)

			// The engine wrote the text that go-yaml misreads, so the error
			// does not match ErrMisread, and the call leaves the text as it
			// found it.
			require.ErrorIs(t, err, edit.ErrEngine)
			require.NotErrorIs(t, err, edit.ErrMisread)
			assert.Contains(t, niceyaml.FormatError(err), tc.err)
			assert.Equal(t, tc.before, d.Text())

			if tc.fix != "" {
				assert.Contains(t, niceyaml.FormatError(err), "\n|-- "+tc.fix+"\n")
			}

			assert.Contains(t, niceyaml.FormatError(err), "\n`-- "+tc.call+"\n")
		})
	}
}
