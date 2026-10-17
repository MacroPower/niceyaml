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
	"go.jacobcolvin.com/niceyaml/paths"
)

func TestNew_Unsupported(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		before string
		// The texts of the reference documents of the document, each named
		// ref.yaml.
		refs []string
		// The options of New.
		opts []edit.Option
		// The lines of the error of New before its excerpt, or "" for a
		// document that New takes.
		err string
		// The paths of the other sites of the construct.
		related []string
		// The document of the text that New takes.
		doc       int
		construct edit.Construct
		// Whether the Source allows duplicate keys.
		dup bool
	}{
		"mixed line breaks": {
			before:    "a: 1\r\nb: 2\n",
			construct: edit.ConstructLineBreaks,
			err: stringtest.JoinLF(
				"test.yaml:2:5: $: this program does not edit a text with both \"\\n\" and \"\\r\\n\" line breaks",
				"`-- write each line break as \"\\n\", or each one as \"\\r\\n\"",
			),
		},
		"block scalar with strip chomping in text of lone carriage returns": {
			// Control: the last line of the scalar ends without a space, and
			// the spaces after the last line break indent the next entry.
			before:    "x:\r  a: |- # m\r    y z\r  p: 1\r",
			construct: edit.ConstructLineBreaks,
			err: stringtest.JoinLF(
				"test.yaml:1:3: $: this program does not edit a text with a \"\\r\" alone as a line break",
				"`-- write each line break as \"\\n\", or each one as \"\\r\\n\"",
			),
		},
		"lone carriage return": {
			before:    "a: 1\rb: 2\r",
			construct: edit.ConstructLineBreaks,
			err: stringtest.JoinLF(
				"test.yaml:1:5: $: this program does not edit a text with a \"\\r\" alone as a line break",
				"`-- write each line break as \"\\n\", or each one as \"\\r\\n\"",
			),
		},
		"lone carriage return in a quoted scalar": {
			before:    "a: \"x\ry\"\n",
			construct: edit.ConstructLineBreaks,
			err: stringtest.JoinLF(
				"test.yaml:1:6: $: this program does not edit a text with a \"\\r\" alone as a line break",
				"`-- write each line break as \"\\n\", or each one as \"\\r\\n\"",
			),
		},
		"carriage return and line feed in a block scalar of a text of line feeds": {
			before:    "a: |\n  x\r\n  y\nb: 1\n",
			construct: edit.ConstructLineBreaks,
			err: stringtest.JoinLF(
				"test.yaml:2:4: $: this program does not edit a text with both \"\\n\" and \"\\r\\n\" line breaks",
				"`-- write each line break as \"\\n\", or each one as \"\\r\\n\"",
			),
		},
		"line break of another document": {
			before:    "a: 1\r\n---\nb: 2\n",
			construct: edit.ConstructLineBreaks,
			err: stringtest.JoinLF(
				"test.yaml:2:4: $: this program does not edit a text with both \"\\n\" and \"\\r\\n\" line breaks",
				"`-- write each line break as \"\\n\", or each one as \"\\r\\n\"",
			),
		},
		"carriage returns and line feeds": {
			// Control.
			before: "a: 1\r\nb: 2\r\n",
		},
		"tag on an alias": {
			before:    stringtest.LinesLF("x: &x 1", "a: !t *x"),
			construct: edit.ConstructAliasProperties,
			err: stringtest.JoinLF(
				"test.yaml:2:4: $.a: this program does not edit a document with a tag or an anchor on an alias, such as !t *x",
				"`-- remove the tag !t from *x, or write the value in place of the alias",
			),
		},
		"core tag on an alias": {
			before:    stringtest.LinesLF("x: &x 1", "a: !!str *x"),
			construct: edit.ConstructAliasProperties,
			err: stringtest.JoinLF(
				"test.yaml:2:4: $.a: this program does not edit a document with a tag or an anchor on an alias, such as !!str *x",
				"`-- remove the tag !!str from *x, or write the value in place of the alias",
			),
		},
		"anchor and tag on an alias": {
			before:    stringtest.LinesLF("x: &x 1", "a: &b !t *x", "c: *b"),
			construct: edit.ConstructAliasProperties,
			err: stringtest.JoinLF(
				"test.yaml:2:4: $.a: this program does not edit a document with a tag or an anchor on an alias, such as &b !t *x",
				"`-- remove the anchor &b and the tag !t from *x, or write the value in place of the alias",
			),
		},
		"tag on an alias in a flow sequence": {
			before:    stringtest.LinesLF("a: &x 1", "b: [!t *x]"),
			construct: edit.ConstructAliasProperties,
			err: stringtest.JoinLF(
				"test.yaml:2:5: $.b[0]: this program does not edit a document with a tag or an anchor on an alias, such as !t *x",
				"`-- remove the tag !t from *x, or write the value in place of the alias",
			),
		},
		"tag on an alias item": {
			before:    stringtest.LinesLF("a: &x 1", "b:", "- !!str *x"),
			construct: edit.ConstructAliasProperties,
			err: stringtest.JoinLF(
				"test.yaml:3:3: $.b[0]: this program does not edit a document with a tag or an anchor on an alias, such as !!str *x",
				"`-- remove the tag !!str from *x, or write the value in place of the alias",
			),
		},
		"tag on an alias key": {
			before:    stringtest.LinesLF("k: &k a", "!t *k : v"),
			construct: edit.ConstructAliasProperties,
			err: stringtest.JoinLF(
				"test.yaml:2:1: $.a: this program does not edit a document with a tag or an anchor on an alias, such as !t *k",
				"`-- remove the tag !t from *k, or write the value in place of the alias",
			),
		},
		"tag on the alias of a merge key": {
			before:    stringtest.LinesLF("s: &s {a: 1}", "m:", "  <<: !t *s"),
			construct: edit.ConstructAliasProperties,
			err: stringtest.JoinLF(
				"test.yaml:3:7: $.m: this program does not edit a document with a tag or an anchor on an alias, such as !t *s",
				"`-- remove the tag !t from *s, or write the value in place of the alias",
			),
		},
		"alias": {
			// Control.
			before: stringtest.LinesLF("x: &x 1", "a: *x"),
		},
		"string tag on an anchored mapping": {
			before:    stringtest.LinesLF("a: !!str &x {a: 1}", "b: 1"),
			construct: edit.ConstructConvertingTag,
			err: stringtest.JoinLF(
				"test.yaml:1:4: $.a: this program does not edit a document with a tag that converts a collection, such as !!str",
				"`-- remove the tag !!str, or write the value as a quoted string",
			),
		},
		"verbatim local tag on a mapping": {
			// Control.
			before: stringtest.LinesLF("a: !<tag:example.com,2000:str> {k: 1}"),
		},
		"int tag on an anchored sequence": {
			before:    stringtest.LinesLF("a: !!int &x [1]", "b: 1"),
			construct: edit.ConstructConvertingTag,
			err: stringtest.JoinLF(
				"test.yaml:1:4: $.a: this program does not edit a document with a tag that converts a collection, such as !!int",
				"`-- remove the tag !!int, or write the value as a quoted string",
			),
		},
		"binary tag on an anchored mapping": {
			before:    stringtest.LinesLF("a: !!binary &x {x: 1}", "b: 1"),
			construct: edit.ConstructConvertingTag,
			err: stringtest.JoinLF(
				"test.yaml:1:4: $.a: this program does not edit a document with a tag that converts a collection, such as !!binary",
				"`-- remove the tag !!binary, or write the value as a quoted string",
			),
		},
		"null tag on an anchored mapping": {
			before:    stringtest.LinesLF("a: !!null &x {x: 1}", "b: 1"),
			construct: edit.ConstructConvertingTag,
			err: stringtest.JoinLF(
				"test.yaml:1:4: $.a: this program does not edit a document with a tag that converts a collection, such as !!null",
				"`-- remove the tag !!null, or write the value as a quoted string",
			),
		},
		"local tag on an anchored mapping": {
			// Control.
			before: stringtest.LinesLF("a: !t &x {k: 1}", "b: 2"),
		},
		"unknown core tag on an anchored mapping": {
			// Control.
			before: stringtest.LinesLF("a: !!foo &x {k: 1}", "b: 2"),
		},
		"map tag on a mapping": {
			// Control.
			before: stringtest.LinesLF("a: !!map {a: 1}"),
		},
		"seq tag on a sequence": {
			// Control.
			before: stringtest.LinesLF("a: !!seq [1]"),
		},
		"non-specific tag": {
			before:    "a: ! b\n",
			construct: edit.ConstructTagKind,
			err: stringtest.JoinLF(
				"test.yaml:1:4: $.a: this program does not edit a document with the tag !",
				"`-- remove the tag !",
			),
		},
		"non-specific tag on a mapping": {
			before:    stringtest.LinesLF("a: ! {x: 1}"),
			construct: edit.ConstructTagKind,
			err: stringtest.JoinLF(
				"test.yaml:1:4: $.a: this program does not edit a document with the tag !",
				"`-- remove the tag !",
			),
		},
		"set tag": {
			before:    stringtest.LinesLF("a: !!set {x: null}"),
			construct: edit.ConstructTagKind,
			err: stringtest.JoinLF(
				"test.yaml:1:4: $.a: this program does not edit a document with the tag !!set",
				"`-- remove the tag !!set",
			),
		},
		"verbatim set tag": {
			before:    stringtest.LinesLF("a: !<tag:yaml.org,2002:set> {x: null}"),
			construct: edit.ConstructTagKind,
			err: stringtest.JoinLF(
				"test.yaml:1:4: $.a: this program does not edit a document with the tag !<tag:yaml.org,2002:set>",
				"`-- remove the tag !<tag:yaml.org,2002:set>",
			),
		},
		"set tag on a block mapping": {
			before:    stringtest.LinesLF("a: !!set", "  ? x", "  ? y"),
			construct: edit.ConstructTagKind,
			err: stringtest.JoinLF(
				"test.yaml:1:4: $.a: this program does not edit a document with the tag !!set",
				"`-- remove the tag !!set",
			),
		},
		"omap tag": {
			before:    stringtest.LinesLF("a: !!omap [{a: 1}]"),
			construct: edit.ConstructTagKind,
			err: stringtest.JoinLF(
				"test.yaml:1:4: $.a: this program does not edit a document with the tag !!omap",
				"`-- remove the tag !!omap",
			),
		},
		"pairs tag": {
			before:    stringtest.LinesLF("a: !!pairs [{a: 1}]"),
			construct: edit.ConstructTagKind,
			err: stringtest.JoinLF(
				"test.yaml:1:4: $.a: this program does not edit a document with the tag !!pairs",
				"`-- remove the tag !!pairs",
			),
		},
		"string tag": {
			// Control: go-yaml reads a quoted scalar under !!str as YAML does.
			before: "a: !!str 'b'\n",
		},
		"alias key": {
			before:    stringtest.LinesLF("k: &k a", "*k : v"),
			construct: edit.ConstructKeyProperties,
			err: stringtest.JoinLF(
				"test.yaml:2:1: $: this program does not edit a document with an alias as a mapping key, such as *k",
				"`-- write the key out in place of *k",
			),
		},
		"alias key in a flow mapping": {
			before:    stringtest.LinesLF("k: &k a", "m: {*k : 1}"),
			construct: edit.ConstructKeyProperties,
			err: stringtest.JoinLF(
				"test.yaml:2:5: $.m: this program does not edit a document with an alias as a mapping key, such as *k",
				"`-- write the key out in place of *k",
			),
		},
		"anchor on a key": {
			before:    stringtest.LinesLF("&k a: v", "b: *k"),
			construct: edit.ConstructKeyProperties,
			err: stringtest.JoinLF(
				"test.yaml:1:1: $: this program does not edit a document with a tag or an anchor on a mapping key, such as &k",
				"`-- remove &k from the key, and write the key out in place of each alias of it",
			),
		},
		"anchor on a merge key": {
			before:    stringtest.LinesLF("s: &s {a: 1}", "m:", "  &q <<: *s", "n: *q"),
			construct: edit.ConstructKeyProperties,
			err: stringtest.JoinLF(
				"test.yaml:3:3: $.m: this program does not edit a document with a tag or an anchor on a mapping key, such as &q",
				"`-- remove &q from the key, and write the key out in place of each alias of it",
			),
		},
		"anchor after the merge tag on a merge key": {
			before:    stringtest.LinesLF("s: &s {a: 1}", "m:", "  !!merge &q <<: *s"),
			construct: edit.ConstructKeyProperties,
			err: stringtest.JoinLF(
				"test.yaml:3:3: $.m: this program does not edit a document with a tag or an anchor on a mapping key, such as &q",
				"`-- remove &q from the key",
			),
		},
		"anchor before the merge tag on a merge key": {
			// The key keeps the tag !!merge, so the fix names the anchor alone.
			before:    stringtest.LinesLF("s: &s {a: 1}", "m:", "  &q !!merge <<: *s"),
			construct: edit.ConstructKeyProperties,
			err: stringtest.JoinLF(
				"test.yaml:3:3: $.m: this program does not edit a document with a tag or an anchor on a mapping key, such as &q",
				"`-- remove &q from the key",
			),
		},
		"anchor on a merge key of an item": {
			before:    stringtest.LinesLF("s: &s {a: 1}", "l:", "- &q <<: *s"),
			construct: edit.ConstructKeyProperties,
			err: stringtest.JoinLF(
				"test.yaml:3:3: $.l[0]: this program does not edit a document with a tag or an anchor on a mapping key, such as &q",
				"`-- remove &q from the key",
			),
		},
		"anchor on the line before a mapping with a merge key": {
			before: stringtest.LinesLF("s: &s {a: 1}", "m:", "  &q", "  <<: *s", "n: *q"),
		},
		"tag and anchor on a key": {
			before:    stringtest.LinesLF("!t &k a: 1", "b: 2"),
			construct: edit.ConstructKeyProperties,
			err: stringtest.JoinLF(
				"test.yaml:1:1: $: this program does not edit a document with a tag or an anchor on a mapping key, such as !t",
				"`-- remove !t and &k from the key",
			),
		},
		"anchor on a key of a flow mapping": {
			before:    stringtest.LinesLF("m: {&k a: 1}"),
			construct: edit.ConstructKeyProperties,
			err: stringtest.JoinLF(
				"test.yaml:1:5: $.m: this program does not edit a document with a tag or an anchor on a mapping key, such as &k",
				"`-- remove &k from the key",
			),
		},
		"tag on a key": {
			before:    "!t a: v\n",
			construct: edit.ConstructKeyProperties,
			err: stringtest.JoinLF(
				"test.yaml:1:1: $: this program does not edit a document with a tag or an anchor on a mapping key, such as !t",
				"`-- remove !t from the key",
			),
		},
		"tag on a key of a flow mapping": {
			before:    stringtest.LinesLF("m: {!t a: v}"),
			construct: edit.ConstructKeyProperties,
			err: stringtest.JoinLF(
				"test.yaml:1:5: $.m: this program does not edit a document with a tag or an anchor on a mapping key, such as !t",
				"`-- remove !t from the key",
			),
		},
		"core tag on a key": {
			// Go-yaml reads a quoted key under the tag as YAML does, so the
			// text holds no misread.
			before:    "!!str 'a': v\n",
			construct: edit.ConstructKeyProperties,
			err: stringtest.JoinLF(
				"test.yaml:1:1: $: this program does not edit a document with a tag or an anchor on a mapping key, such as !!str",
				"`-- remove !!str from the key",
			),
		},
		"binary tag on a key": {
			before:    stringtest.LinesLF("labels:", "  valid_label: foo", "  !!binary \"/w==\": bar"),
			construct: edit.ConstructKeyProperties,
			err: stringtest.JoinLF(
				"test.yaml:3:3: $.labels: this program does not edit a document with a tag or an anchor on a mapping key, such as !!binary",
				"`-- remove !!binary from the key",
			),
		},
		"tag on an explicit key": {
			before:    stringtest.LinesLF("? !t a", ": 1"),
			construct: edit.ConstructKeyProperties,
			err: stringtest.JoinLF(
				"test.yaml:1:3: $: this program does not edit a document with a tag or an anchor on a mapping key, such as !t",
				"`-- remove !t from the key",
			),
		},
		"tag before an anchor on a key": {
			before:    stringtest.LinesLF("!t &k a: 1", "b: *k"),
			construct: edit.ConstructKeyProperties,
			err: stringtest.JoinLF(
				"test.yaml:1:1: $: this program does not edit a document with a tag or an anchor on a mapping key, such as !t",
				"`-- remove !t and &k from the key, and write the key out in place of each alias of it",
			),
		},
		"local tag on a merge key": {
			before:    stringtest.LinesLF("s: &s {a: 1}", "m:", "  !t <<: *s"),
			construct: edit.ConstructKeyProperties,
			err: stringtest.JoinLF(
				"test.yaml:3:3: $.m: this program does not edit a document with a tag or an anchor on a mapping key, such as !t",
				"`-- remove !t from the key",
			),
		},
		"merge tag on a merge key": {
			// Control.
			before: stringtest.LinesLF("s: &s {a: 1}", "m:", "  !!merge <<: *s"),
		},
		"verbatim merge tag on a merge key": {
			before: stringtest.LinesLF("s: &s {a: 1}", "m:", "  !<tag:yaml.org,2002:merge> <<: *s"),
		},
		"anchor on an empty value": {
			before:    stringtest.LinesLF("a: &x", "b: 1"),
			construct: edit.ConstructAnchoredEmpty,
			err: stringtest.JoinLF(
				"test.yaml:1:4: $.a: this program does not edit a document with an anchor on an empty value, such as &x",
				"`-- write a value after &x, such as null",
			),
		},
		"anchor on an empty item": {
			before:    stringtest.LinesLF("- &x", "- 1"),
			construct: edit.ConstructAnchoredEmpty,
			err: stringtest.JoinLF(
				"test.yaml:1:3: $[0]: this program does not edit a document with an anchor on an empty value, such as &x",
				"`-- write a value after &x, such as null",
			),
		},
		"anchor and tag on an empty value": {
			// No line follows the tag, which go-yaml would read as its value,
			// and go-yaml reads the empty string under the tag as YAML does.
			before:    stringtest.LinesLF("b: 1", "a: &x !!str"),
			construct: edit.ConstructAnchoredEmpty,
			err: stringtest.JoinLF(
				"test.yaml:2:4: $.a: this program does not edit a document with an anchor on an empty value, such as &x",
				"`-- write a value after &x, such as null",
			),
		},
		"anchor on an empty value of a mapping item": {
			before:    stringtest.LinesLF("- a: &x", "  b: 1"),
			construct: edit.ConstructAnchoredEmpty,
			err: stringtest.JoinLF(
				"test.yaml:1:6: $[0].a: this program does not edit a document with an anchor on an empty value, such as &x",
				"`-- write a value after &x, such as null",
			),
		},
		"anchor on an empty item before an entry": {
			before:    stringtest.LinesLF("a: 1", "r0:", "- &x0", "r1: 1"),
			construct: edit.ConstructAnchoredEmpty,
			err: stringtest.JoinLF(
				"test.yaml:3:3: $.r0[0]: this program does not edit a document with an anchor on an empty value, such as &x0",
				"`-- write a value after &x0, such as null",
			),
		},
		"anchor on an empty value of an explicit key": {
			before:    stringtest.LinesLF("r1:", "  ? a", "  : &x1", "b: 1"),
			construct: edit.ConstructAnchoredEmpty,
			err: stringtest.JoinLF(
				"test.yaml:3:5: $.r1.a: this program does not edit a document with an anchor on an empty value, such as &x1",
				"`-- write a value after &x1, such as null",
			),
		},
		"anchor on an empty value before a later document": {
			before:    stringtest.LinesLF("a: &x", "---", "b: 1"),
			construct: edit.ConstructAnchoredEmpty,
			err: stringtest.JoinLF(
				"test.yaml:1:4: $.a: this program does not edit a document with an anchor on an empty value, such as &x",
				"`-- write a value after &x, such as null",
			),
		},
		"anchor on a null": {
			// Control.
			before: stringtest.LinesLF("a: &x null", "b: *x"),
		},
		"anchor on a tilde": {
			// Control.
			before: stringtest.LinesLF("a: &x ~", "b: *x"),
		},
		"anchor on an empty quoted string": {
			// Control.
			before: stringtest.LinesLF("a: &x ''", "b: *x"),
		},
		"anchor name defined twice": {
			before:    stringtest.LinesLF("a: &x 1", "b: &x 2", "c: *x"),
			construct: edit.ConstructReusedAnchor,
			related:   []string{"$.a"},
			err: stringtest.JoinLF(
				"test.yaml:2:4: $.b: this program does not edit a document that defines the anchor &x twice",
				"|-- 1:4: $.a: &x is defined here",
				"`-- rename one of the two anchors and the aliases that read it",
			),
		},
		"anchor name defined twice without an alias": {
			before:    stringtest.LinesLF("a: &x 1", "b: &x 2"),
			construct: edit.ConstructReusedAnchor,
			related:   []string{"$.a"},
			err: stringtest.JoinLF(
				"test.yaml:2:4: $.b: this program does not edit a document that defines the anchor &x twice",
				"|-- 1:4: $.a: &x is defined here",
				"`-- rename one of the two anchors and the aliases that read it",
			),
		},
		"anchor name defined three times": {
			before:    stringtest.LinesLF("a: &x 1", "b: &x 2", "c: &x 3"),
			construct: edit.ConstructReusedAnchor,
			related:   []string{"$.a"},
			err: stringtest.JoinLF(
				"test.yaml:2:4: $.b: this program does not edit a document that defines the anchor &x twice",
				"|-- 1:4: $.a: &x is defined here",
				"`-- rename one of the two anchors and the aliases that read it",
			),
		},
		"anchor name defined twice in the selected document": {
			before:    stringtest.LinesLF("a: 1", "---", "b: &x 2", "c: &x 3"),
			doc:       1,
			construct: edit.ConstructReusedAnchor,
			related:   []string{"$.b"},
			err: stringtest.JoinLF(
				"test.yaml:4:4: $.c: this program does not edit a document that defines the anchor &x twice",
				"|-- 3:4: $.b: &x is defined here",
				"`-- rename one of the two anchors and the aliases that read it",
			),
		},
		"anchor name defined again on the merge value inside its anchor": {
			// The alias reads the anchor of the merge value, which does not hold
			// it, so the text holds no alias inside its own anchor.
			before:    stringtest.LinesLF("r1: &x0", "  <<: &x0 {p: 2}", "  q: *x0"),
			construct: edit.ConstructReusedAnchor,
			related:   []string{"$.r1"},
			err: "test.yaml:2:7: $.r1: this program does not edit a document that defines the anchor &x0 " +
				"twice",
		},
		"alias inside its anchor before the anchor name is defined again": {
			// Control: the alias comes before the second anchor of its name.
			before:    stringtest.LinesLF("a: &x [*x]", "b: &x 1"),
			construct: edit.ConstructRecursiveAlias,
			related:   []string{"$.a[0]"},
			err: "test.yaml:1:4: $.a: this program does not edit a document with an alias inside the value of the " +
				"anchor that it reads, such as *x",
		},
		"mapping that merges itself after an own key": {
			// Control: the source of the merge key holds the own entry p
			// itself, which both readings keep, so the text holds no misread.
			before:    stringtest.LinesLF("k0: &a", "  p: 1", "  <<: *a"),
			construct: edit.ConstructRecursiveAlias,
			related:   []string{"$.k0"},
			err: "test.yaml:1:5: $.k0: this program does not edit a document with an alias inside the value of the " +
				"anchor that it reads, such as *a",
		},
		"anchor name that a reference document defines too": {
			before:    stringtest.LinesLF("a: &n 1", "b: *n"),
			refs:      []string{"n: &n 2\n"},
			construct: edit.ConstructReusedAnchor,
			related:   []string{"$.b"},
			err: stringtest.JoinLF(
				"test.yaml:1:4: $.a: this program does not edit a document that aliases the anchor &n, which the reference document ref.yaml also defines",
				"|-- 2:4: $.b: *n reads &n here",
				"`-- rename &n and its aliases",
			),
		},
		"anchor name that a reference document defines, aliased before the anchor": {
			before:    stringtest.LinesLF("a: *n", "b: &n 1"),
			refs:      []string{"n: &n 2\n"},
			construct: edit.ConstructReusedAnchor,
			related:   []string{"$.a"},
			err: stringtest.JoinLF(
				"test.yaml:2:4: $.b: this program does not edit a document that aliases the anchor &n, which the reference document ref.yaml also defines",
				"|-- 1:4: $.a: *n reads &n here",
				"`-- rename &n and its aliases",
			),
		},
		"anchor name that a reference document defines, in a flow sequence": {
			before:    stringtest.LinesLF("x: [&n 1, *n]"),
			refs:      []string{"n: &n 2\n"},
			construct: edit.ConstructReusedAnchor,
			related:   []string{"$.x[1]"},
			err: stringtest.JoinLF(
				"test.yaml:1:5: $.x[0]: this program does not edit a document that aliases the anchor &n, which the reference document ref.yaml also defines",
				"|-- 1:11: $.x[1]: *n reads &n here",
				"`-- rename &n and its aliases",
			),
		},
		"anchor name that a reference document defines, without an alias": {
			// Control.
			before: stringtest.LinesLF("a: &n 1", "b: 2"),
			refs:   []string{"n: &n 2\n"},
		},
		"alias of a reference document": {
			// Control.
			before: stringtest.LinesLF("a: *n", "b: 1"),
			refs:   []string{"n: &n 2\n"},
		},
		"anchor name of another document": {
			// Control.
			before: stringtest.LinesLF("a: &x 1", "---", "b: &x 2", "c: *x"),
			doc:    1,
		},
		"alias inside its own anchored sequence": {
			before:    stringtest.LinesLF("a: &x [*x]", "b: 1"),
			construct: edit.ConstructRecursiveAlias,
			related:   []string{"$.a[0]"},
			err: stringtest.JoinLF(
				"test.yaml:1:4: $.a: this program does not edit a document with an alias inside the value of the anchor that it reads, such as *x",
				"|-- 1:8: $.a[0]: *x is here",
				"`-- remove *x from the value of &x",
			),
		},
		"alias inside its own anchored mapping": {
			before:    stringtest.LinesLF("a: &x {b: *x}"),
			construct: edit.ConstructRecursiveAlias,
			related:   []string{"$.a.b"},
			err: stringtest.JoinLF(
				"test.yaml:1:4: $.a: this program does not edit a document with an alias inside the value of the anchor that it reads, such as *x",
				"|-- 1:11: $.a.b: *x is here",
				"`-- remove *x from the value of &x",
			),
		},
		"alias deep inside its own anchor": {
			before:    stringtest.LinesLF("a: &x", "  b:", "    c: [*x]"),
			construct: edit.ConstructRecursiveAlias,
			related:   []string{"$.a.b.c[0]"},
			err: stringtest.JoinLF(
				"test.yaml:1:4: $.a: this program does not edit a document with an alias inside the value of the anchor that it reads, such as *x",
				"|-- 3:9: $.a.b.c[0]: *x is here",
				"`-- remove *x from the value of &x",
			),
		},
		"alias inside its own anchor through an inner anchor": {
			before:    stringtest.LinesLF("a: &x {b: &y {c: *x}}"),
			construct: edit.ConstructRecursiveAlias,
			related:   []string{"$.a.b.c"},
			err: stringtest.JoinLF(
				"test.yaml:1:4: $.a: this program does not edit a document with an alias inside the value of the anchor that it reads, such as *x",
				"|-- 1:18: $.a.b.c: *x is here",
				"`-- remove *x from the value of &x",
			),
		},
		"alias after an anchored empty value inside its anchor": {
			// Go-yaml nests r1 in the value of &x1, and so in &x0, but YAML
			// reads r1 at the root. The anchored empty value is the first
			// instance.
			before:    stringtest.LinesLF("r0: &x0", "  ? a", "  : &x1", "r1:", "  d: *x0"),
			construct: edit.ConstructAnchoredEmpty,
			err: stringtest.JoinLF(
				"test.yaml:3:5: $.r0.a: this program does not edit a document with an anchor on an empty value, such as &x1",
				"`-- write a value after &x1, such as null",
			),
		},
		"alias inside its own anchored merge value": {
			before:    stringtest.LinesLF("k:", "  <<: &g [*g]"),
			construct: edit.ConstructRecursiveAlias,
			related:   []string{"$.k"},
			err: stringtest.JoinLF(
				"test.yaml:2:7: $.k: this program does not edit a document with an alias inside the value of the anchor that it reads, such as *g",
				"|-- 2:11: $.k: *g is here",
				"`-- remove *g from the value of &g",
			),
		},
		"alias after its anchor": {
			// Control.
			before: stringtest.LinesLF("a: &x [1]", "b: [*x]"),
		},
		"alias of an inner anchor inside an outer one": {
			// Control.
			before: stringtest.LinesLF("a: &x {b: &y 1, c: *y}"),
		},
		"hex key": {
			before:    "0x10: a\n",
			construct: edit.ConstructKeyText,
			err: stringtest.JoinLF(
				"test.yaml:1:1: $: this program does not edit a key that reads as another value than its text, such as 0x10, which reads as 16",
				"`-- quote the key, as in \"0x10\"",
			),
		},
		"octal key": {
			before:    "010: a\n",
			construct: edit.ConstructKeyText,
			err: stringtest.JoinLF(
				"test.yaml:1:1: $: this program does not edit a key that reads as another value than its text, such as 010, which reads as 8",
				"`-- quote the key, as in \"010\"",
			),
		},
		"key with an underscore": {
			before:    "18_24: a\n",
			construct: edit.ConstructKeyText,
			err: stringtest.JoinLF(
				"test.yaml:1:1: $: this program does not edit a key that reads as another value than its text, such as 18_24, which reads as 1824",
				"`-- quote the key, as in \"18_24\"",
			),
		},
		"capitalized boolean key": {
			before:    "True: a\n",
			construct: edit.ConstructKeyText,
			err: stringtest.JoinLF(
				"test.yaml:1:1: $: this program does not edit a key that reads as another value than its text, such as True, which reads as true",
				"`-- quote the key, as in \"True\"",
			),
		},
		"float key with a zero fraction": {
			before:    "1.0: a\n",
			construct: edit.ConstructKeyText,
			err: stringtest.JoinLF(
				"test.yaml:1:1: $: this program does not edit a key that reads as another value than its text, such as 1.0, which reads as 1",
				"`-- quote the key, as in \"1.0\"",
			),
		},
		"tilde key": {
			before:    "~: a\n",
			construct: edit.ConstructKeyText,
			err: stringtest.JoinLF(
				"test.yaml:1:1: $: this program does not edit a key that reads as another value than its text, such as ~, which reads as the null value",
				"`-- quote the key, as in \"~\"",
			),
		},
		// The key null reads as the null value, which the Supported YAML
		// section of the package documentation lists apart (fix21 G1-5).
		"null key": {
			before:    "null: a\n",
			construct: edit.ConstructKeyText,
			err: stringtest.JoinLF(
				"test.yaml:1:1: $: this program does not edit a key that reads as the null value, such as null",
				"`-- quote the key, as in \"null\"",
			),
		},
		"infinity key": {
			before:    ".inf: a\n",
			construct: edit.ConstructKeyText,
			err: stringtest.JoinLF(
				"test.yaml:1:1: $: this program does not edit a key that reads as another value than its text, such as .inf, which reads as the float .inf",
				"`-- quote the key, as in \".inf\"",
			),
		},
		"key with a plus sign": {
			before:    "+1: a\n",
			construct: edit.ConstructKeyText,
			err: stringtest.JoinLF(
				"test.yaml:1:1: $: this program does not edit a key that reads as another value than its text, such as +1, which reads as 1",
				"`-- quote the key, as in \"+1\"",
			),
		},
		"key of a nested mapping": {
			before:    stringtest.LinesLF("properties:", "  18_24: x"),
			construct: edit.ConstructKeyText,
			err: stringtest.JoinLF(
				"test.yaml:2:3: $.properties: this program does not edit a key that reads as another value than its text, such as 18_24, which reads as 1824",
				"`-- quote the key, as in \"18_24\"",
			),
		},
		"hex key of a flow mapping": {
			before:    stringtest.LinesLF("m: {0x10: a}"),
			construct: edit.ConstructKeyText,
			err: stringtest.JoinLF(
				"test.yaml:1:5: $.m: this program does not edit a key that reads as another value than its text, such as 0x10, which reads as 16",
				"`-- quote the key, as in \"0x10\"",
			),
		},
		"binary key of a flow mapping in a sequence": {
			before:    stringtest.LinesLF("l:", "- {a: 1, 0b101: 2}"),
			construct: edit.ConstructKeyText,
			err: stringtest.JoinLF(
				"test.yaml:2:10: $.l[0]: this program does not edit a key that reads as another value than its text, such as 0b101, which reads as 5",
				"`-- quote the key, as in \"0b101\"",
			),
		},
		"integer key": {
			// Control.
			before: "200: a\n",
		},
		"boolean key": {
			// Control.
			before: "true: a\n",
		},
		"float key": {
			// Control.
			before: "0.5: a\n",
		},
		"on key": {
			// Control.
			before: "on: a\n",
		},
		"yes key": {
			// Control.
			before: "yes: a\n",
		},
		"date key": {
			// Control.
			before: stringtest.LinesLF("2023-01-21: a"),
		},
		"quoted hex key": {
			// Control.
			before: "\"0x10\": a\n",
		},
		"block scalar key": {
			// Control.
			before: stringtest.LinesLF("x:", "  ? |", "    a", "  : 1"),
		},
		"program that WithApp names": {
			before:    "a: &x 1\nb: &x 2\n",
			opts:      []edit.Option{edit.WithApp("cafe")},
			construct: edit.ConstructReusedAnchor,
			related:   []string{"$.a"},
			err: stringtest.JoinLF(
				"test.yaml:2:4: $.b: cafe does not edit a document that defines the anchor &x twice",
				"|-- 1:4: $.a: &x is defined here",
				"`-- rename one of the two anchors and the aliases that read it",
			),
		},
		"alias key of a reference document before a block scalar": {
			before:    stringtest.LinesLF("&k0 !t *q0 : *rk"),
			refs:      []string{"&rk name: 1\n", "t0: &q0 'q'\n"},
			construct: edit.ConstructAliasProperties,
			err: stringtest.JoinLF(
				"test.yaml:1:1: $.q: this program does not edit a document with a tag or an anchor on an alias, such as &k0 !t *q0",
				"`-- remove the anchor &k0 and the tag !t from *q0, or write the value in place of the alias",
			),
		},
		"anchored and tagged alias of a reference document that another alias reads": {
			before:    stringtest.LinesLF("m: {t: &k !!str *r0}", "a: *k"),
			refs:      []string{"s0: &r0 q\n"},
			construct: edit.ConstructAliasProperties,
			err: stringtest.JoinLF(
				"test.yaml:1:8: $.m.t: this program does not edit a document with a tag or an anchor on an alias, such as &k !!str *r0",
				"`-- remove the anchor &k and the tag !!str from *r0, or write the value in place of the alias",
			),
		},
		"anchored mapping with an alias key of a reference document under a local tag, before an anchor of its name": {
			before:    stringtest.LinesLF("a: &k0 {!t *r0 : v}", "c: &r0 q", "b: *k0"),
			refs:      []string{"s: &r0 1\n"},
			construct: edit.ConstructAliasProperties,
			err: stringtest.JoinLF(
				"test.yaml:1:9: $.a.1: this program does not edit a document with a tag or an anchor on an alias, such as !t *r0",
				"`-- remove the tag !t from *r0, or write the value in place of the alias",
			),
		},
		"anchored scalar at an alias with an anchor on the line before it": {
			before:    stringtest.LinesLF("b: &b 5", "c: &a", "  *b", "d: *a"),
			construct: edit.ConstructAliasProperties,
			err: stringtest.JoinLF(
				"test.yaml:2:4: $.c: this program does not edit a document with a tag or an anchor on an alias, such as &a *b",
				"`-- remove the anchor &a from *b, or write the value in place of the alias",
			),
		},
		"anchored tagged sequence at an item with another custom tag in a flow sequence": {
			before:    stringtest.LinesLF("b: &a !t [1]", "c: [x, !u *a]"),
			construct: edit.ConstructAliasProperties,
			err: stringtest.JoinLF(
				"test.yaml:2:8: $.c[1]: this program does not edit a document with a tag or an anchor on an alias, such as !u *a",
				"`-- remove the tag !u from *a, or write the value in place of the alias",
			),
		},
		"entry below an alias of an anchor on a str-tagged alias inside its own anchor": {
			before:    stringtest.LinesLF("a: &x !!str *x", "b: *x"),
			construct: edit.ConstructAliasProperties,
			err: stringtest.JoinLF(
				"test.yaml:1:4: $.a: this program does not edit a document with a tag or an anchor on an alias, such as &x !!str *x",
				"`-- remove the anchor &x and the tag !!str from *x, or write the value in place of the alias",
			),
		},
		"entry below an alias of an anchor on a tagged alias inside its own anchor": {
			before:    stringtest.LinesLF("a: {k: &x !t *x}", "b: *x"),
			construct: edit.ConstructAliasProperties,
			err: stringtest.JoinLF(
				"test.yaml:1:8: $.a.k: this program does not edit a document with a tag or an anchor on an alias, such as &x !t *x",
				"`-- remove the anchor &x and the tag !t from *x, or write the value in place of the alias",
			),
		},
		"key that a merge key brings in through a tagged alias key of an anchor beside it": {
			before:    stringtest.LinesLF("<<: {k: &a 1, !t *a : x}", "1: y"),
			construct: edit.ConstructAliasProperties,
			err: stringtest.JoinLF(
				"test.yaml:1:15: $: this program does not edit a document with a tag or an anchor on an alias, such as !t *a",
				"`-- remove the tag !t from *a, or write the value in place of the alias",
			),
		},
		"move that leaves an alias key of a reference first, before a quoted key": {
			before:    stringtest.LinesLF("n: {k: 16}", "!!str *r0 : &k0 True"),
			refs:      []string{"t0: &r0 True\n"},
			construct: edit.ConstructAliasProperties,
			err: stringtest.JoinLF(
				"test.yaml:2:1: $.True: this program does not edit a document with a tag or an anchor on an alias, such as !!str *r0",
				"`-- remove the tag !!str from *r0, or write the value in place of the alias",
			),
		},
		"tagged alias key of a reference document whose value a set replaced": {
			before:    stringtest.LinesLF("&k0 !!str *r2 : {<<: *rm0}"),
			refs:      []string{"s2: &r2 a b\nm0: &rm0 {c: 0x10, false: ~, m: x}\n"},
			dup:       true,
			construct: edit.ConstructAliasProperties,
			err: stringtest.JoinLF(
				"test.yaml:1:1: $.'a b': this program does not edit a document with a tag or an anchor on an alias, such as &k0 !!str *r2",
				"`-- remove the anchor &k0 and the tag !!str from *r2, or write the value in place of the alias",
			),
		},
		"tagged alias of a reference document": {
			before:    stringtest.LinesLF("a: !!str *word"),
			construct: edit.ConstructAliasProperties,
			err: stringtest.JoinLF(
				"test.yaml:1:4: $.a: this program does not edit a document with a tag or an anchor on an alias, such as !!str *word",
				"`-- remove the tag !!str from *word, or write the value in place of the alias",
			),
		},
		"walk of k: <<: &g !!str *g": {
			before:    stringtest.LinesLF("k:", "  <<: &g !!str *g"),
			construct: edit.ConstructAliasProperties,
			err: stringtest.JoinLF(
				"test.yaml:2:7: $.k: this program does not edit a document with a tag or an anchor on an alias, such as &g !!str *g",
				"`-- remove the anchor &g and the tag !!str from *g, or write the value in place of the alias",
			),
		},
		"walk of k: {<<: &g !!str *g}": {
			before:    stringtest.LinesLF("k: {<<: &g !!str *g}"),
			construct: edit.ConstructAliasProperties,
			err: stringtest.JoinLF(
				"test.yaml:1:9: $.k: this program does not edit a document with a tag or an anchor on an alias, such as &g !!str *g",
				"`-- remove the anchor &g and the tag !!str from *g, or write the value in place of the alias",
			),
		},
		"entry below a null tag on an anchored block mapping": {
			before:    stringtest.LinesLF("p: !!null &k", "  a: 1"),
			construct: edit.ConstructConvertingTag,
			err: stringtest.JoinLF(
				"test.yaml:1:4: $.p: this program does not edit a document with a tag that converts a collection, such as !!null",
				"`-- remove the tag !!null, or write the value as a quoted string",
			),
		},
		"entry below a string tag on an anchored root mapping": {
			before:    stringtest.LinesLF("!!str &k {}"),
			construct: edit.ConstructConvertingTag,
			err: stringtest.JoinLF(
				"test.yaml:1:1: $: this program does not edit a document with a tag that converts a collection, such as !!str",
				"`-- remove the tag !!str, or write the value as a quoted string",
			),
		},
		"removal after a comment line that follows a converting tag before an anchor": {
			before:    stringtest.LinesLF("p: !!int &k [1]", "# l", "q: 1"),
			construct: edit.ConstructConvertingTag,
			err: stringtest.JoinLF(
				"test.yaml:1:4: $.p: this program does not edit a document with a tag that converts a collection, such as !!int",
				"`-- remove the tag !!int, or write the value as a quoted string",
			),
		},
		"alias before an anchor on a merge value": {
			before:    stringtest.LinesLF("b: &x 2", "n: {}", "m: {<<: &x {p: 1}}", "c: *x"),
			construct: edit.ConstructReusedAnchor,
			related:   []string{"$.b"},
			err: stringtest.JoinLF(
				"test.yaml:3:9: $.m: this program does not edit a document that defines the anchor &x twice",
				"|-- 1:4: $.b: &x is defined here",
				"`-- rename one of the two anchors and the aliases that read it",
			),
		},
		"alias of an inner anchor of a flow sequence out of it": {
			before:    stringtest.LinesLF("a: &y [&y v, *y]"),
			construct: edit.ConstructReusedAnchor,
			related:   []string{"$.a"},
			err: stringtest.JoinLF(
				"test.yaml:1:8: $.a[0]: this program does not edit a document that defines the anchor &y twice",
				"|-- 1:4: $.a: &y is defined here",
				"`-- rename one of the two anchors and the aliases that read it",
			),
		},
		"alias past another anchor of its name": {
			before:    stringtest.LinesLF("a: &x 1", "b: *x", "c: &x 2"),
			construct: edit.ConstructReusedAnchor,
			related:   []string{"$.a"},
			err: stringtest.JoinLF(
				"test.yaml:3:4: $.c: this program does not edit a document that defines the anchor &x twice",
				"|-- 1:4: $.a: &x is defined here",
				"`-- rename one of the two anchors and the aliases that read it",
			),
		},
		"anchored mapping with an alias of a reference document, before an anchor of its name": {
			before:    stringtest.LinesLF("a: &k0 {p: *r0}", "c: &r0 q", "b: *k0"),
			refs:      []string{"s: &r0 1\n"},
			construct: edit.ConstructReusedAnchor,
			related:   []string{"$.a.p"},
			err: stringtest.JoinLF(
				"test.yaml:2:4: $.c: this program does not edit a document that aliases the anchor &r0, which the reference document ref.yaml also defines",
				"|-- 1:12: $.a.p: *r0 reads &r0 here",
				"`-- rename &r0 and its aliases",
			),
		},
		"removal of an anchored value whose merge value holds another anchor of its name": {
			before:    stringtest.LinesLF("r0: &x0", "  <<: &x0 {}", "r2: *x0"),
			construct: edit.ConstructReusedAnchor,
			related:   []string{"$.r0"},
			err: stringtest.JoinLF(
				"test.yaml:2:7: $.r0: this program does not edit a document that defines the anchor &x0 twice",
				"|-- 1:5: $.r0: &x0 is defined here",
				"`-- rename one of the two anchors and the aliases that read it",
			),
		},
		"walk of k: <<: &g [*g]": {
			before:    stringtest.LinesLF("k:", "  <<: &g [*g]"),
			construct: edit.ConstructRecursiveAlias,
			related:   []string{"$.k"},
			err: stringtest.JoinLF(
				"test.yaml:2:7: $.k: this program does not edit a document with an alias inside the value of the anchor that it reads, such as *g",
				"|-- 2:11: $.k: *g is here",
				"`-- remove *g from the value of &g",
			),
		},
		"walk of k: {<<: &g [*g]}": {
			before:    stringtest.LinesLF("k: {<<: &g [*g]}"),
			construct: edit.ConstructRecursiveAlias,
			related:   []string{"$.k"},
			err: stringtest.JoinLF(
				"test.yaml:1:9: $.k: this program does not edit a document with an alias inside the value of the anchor that it reads, such as *g",
				"|-- 1:13: $.k: *g is here",
				"`-- remove *g from the value of &g",
			),
		},
		"wildcard over the items of a sequence that holds an alias of itself": {
			before:    stringtest.LinesLF("s: &x0", "  - *x0"),
			construct: edit.ConstructRecursiveAlias,
			related:   []string{"$.s[0]"},
			err: stringtest.JoinLF(
				"test.yaml:1:4: $.s: this program does not edit a document with an alias inside the value of the anchor that it reads, such as *x0",
				"|-- 2:5: $.s[0]: *x0 is here",
				"`-- remove *x0 from the value of &x0",
			),
		},
		"anchored value past an alias key that reads it": {
			before:    stringtest.LinesLF("m: &k0 1", "*k0 : 1"),
			construct: edit.ConstructKeyProperties,
			err: stringtest.JoinLF(
				"test.yaml:2:1: $: this program does not edit a document with an alias as a mapping key, such as *k0",
				"`-- write the key out in place of *k0",
			),
		},
		"decode of a misread before a set that fails, whose error the Func returns": {
			before:    stringtest.LinesLF("a: {!t &k TRUE: x}", "b: 1"),
			construct: edit.ConstructKeyProperties,
			err: stringtest.JoinLF(
				"test.yaml:1:5: $.a: this program does not edit a document with a tag or an anchor on a mapping key, such as !t",
				"`-- remove !t and &k from the key",
			),
		},
		"item after an anchored empty item, from sweep seed 27327 case 2971": {
			// The local tag on the first key stands for the !!str of the sweep
			// case, which go-yaml's decoder reads otherwise on a plain key.
			before:    "!t false:\r- &x0\r-\r      +1: 'p\r\r        q'\r      b: {.inf: x}\r      <<: {}\r<<: {!t 16: 2, !!null null: 1}\rr2: *x0 # f\r",
			dup:       true,
			construct: edit.ConstructKeyProperties,
			err: stringtest.JoinLF(
				"test.yaml:1:1: $: this program does not edit a document with a tag or an anchor on a mapping key, such as !t",
				"`-- remove !t from the key",
			),
		},
		"move that leaves an alias key of a reference first, before a moved quoted key": {
			before:    stringtest.LinesLF("&k0 !t m:", "  - name: x", "    a:", "    b: 1", "*rk : 16"),
			refs:      []string{"&rk !!bool true: 1\n"},
			construct: edit.ConstructKeyProperties,
			err: stringtest.JoinLF(
				"test.yaml:1:1: $: this program does not edit a document with a tag or an anchor on a mapping key, such as &k0",
				"`-- remove &k0 and !t from the key",
			),
		},
		"move to its name": {
			before:    stringtest.LinesLF("m: {*k : 2}", "z: 1"),
			refs:      []string{"&k name: 1\n"},
			construct: edit.ConstructKeyProperties,
			err: stringtest.JoinLF(
				"test.yaml:1:5: $.m: this program does not edit a document with an alias as a mapping key, such as *k",
				"`-- write the key out in place of *k",
			),
		},
		"read of an alias of a reference document after a scalar whose tag go-yaml drops": {
			before:    stringtest.LinesLF("!t &k0 1000.0: 1", "k: *r0"),
			refs:      []string{"s0: &r0 x\n"},
			construct: edit.ConstructKeyProperties,
			err: stringtest.JoinLF(
				"test.yaml:1:1: $: this program does not edit a document with a tag or an anchor on a mapping key, such as !t",
				"`-- remove !t and &k0 from the key",
			),
		},
		"removal of an anchored value, then a move of the flow entry whose alias key took its copy": {
			before:    stringtest.LinesLF("x: &k a", "m: {*k : *k}"),
			construct: edit.ConstructKeyProperties,
			err: stringtest.JoinLF(
				"test.yaml:2:5: $.m: this program does not edit a document with an alias as a mapping key, such as *k",
				"`-- write the key out in place of *k",
			),
		},
		"rename of a key that a merge key brings in through an alias key of an anchor beside it": {
			before:    stringtest.LinesLF("<<: {k: &a p, *a : x}", "p: y"),
			construct: edit.ConstructKeyProperties,
			err: stringtest.JoinLF(
				"test.yaml:1:15: $: this program does not edit a document with an alias as a mapping key, such as *a",
				"`-- write the key out in place of *a",
			),
		},
		"rename of a key with a local tag": {
			before:    stringtest.LinesLF("m:", "  !t a: 1"),
			construct: edit.ConstructKeyProperties,
			err: stringtest.JoinLF(
				"test.yaml:2:3: $.m: this program does not edit a document with a tag or an anchor on a mapping key, such as !t",
				"`-- remove !t from the key",
			),
		},
		"rename of an alias key that reads a reference document": {
			before:    stringtest.LinesLF("m:", "  *k : 2"),
			refs:      []string{"&k name: 1\n"},
			construct: edit.ConstructKeyProperties,
			err: stringtest.JoinLF(
				"test.yaml:2:3: $.m: this program does not edit a document with an alias as a mapping key, such as *k",
				"`-- write the key out in place of *k",
			),
		},
		"rename of an anchored key that its own value reads to its own name, then a rename": {
			before:    "&k z: *k\n",
			construct: edit.ConstructKeyProperties,
			err: stringtest.JoinLF(
				"test.yaml:1:1: $: this program does not edit a document with a tag or an anchor on a mapping key, such as &k",
				"`-- remove &k from the key, and write the key out in place of each alias of it",
			),
		},
		"rename that drops a tag in an inline merge value of a flow mapping": {
			before:    stringtest.LinesLF("k: {<<: {!!int 8: q}, b: x}"),
			construct: edit.ConstructKeyProperties,
			err: stringtest.JoinLF(
				"test.yaml:1:10: $.k: this program does not edit a document with a tag or an anchor on a mapping key, such as !!int",
				"`-- remove !!int from the key",
			),
		},
		"value that holds two keys that a decode reads as one, at a flow root whose first key has an anchor": {
			before:    stringtest.LinesLF("{&k z: 1, *k : 2}"),
			construct: edit.ConstructKeyProperties,
			err: stringtest.JoinLF(
				"test.yaml:1:2: $: this program does not edit a document with a tag or an anchor on a mapping key, such as &k",
				"`-- remove &k from the key, and write the key out in place of each alias of it",
			),
		},
		"empty anchored value past its alias to a new key": {
			before:    stringtest.LinesLF("a: &e", "b: *e"),
			construct: edit.ConstructAnchoredEmpty,
			err: stringtest.JoinLF(
				"test.yaml:1:4: $.a: this program does not edit a document with an anchor on an empty value, such as &e",
				"`-- write a value after &e, such as null",
			),
		},
		"entry after an anchored empty item before a comment line at the end of the text": {
			before:    stringtest.LinesLF("r0:", "- &x", "# c"),
			construct: edit.ConstructAnchoredEmpty,
			err: stringtest.JoinLF(
				"test.yaml:2:3: $.r0[0]: this program does not edit a document with an anchor on an empty value, such as &x",
				"`-- write a value after &x, such as null",
			),
		},
		"removal that leaves an anchor without a value before a comment and a later document": {
			before:    stringtest.LinesLF("a: 1", "---", "b: &x", "c: 1", "# end", "---", "z: 3"),
			doc:       1,
			construct: edit.ConstructAnchoredEmpty,
			err: stringtest.JoinLF(
				"test.yaml:3:4: $.b: this program does not edit a document with an anchor on an empty value, such as &x",
				"`-- write a value after &x, such as null",
			),
		},
		"removal that leaves an anchor without a value before a comment line, in text of lone carriage returns": {
			// The local tag on the key of the flow mapping stands for the !!str
			// of the sweep case, which go-yaml's decoder reads otherwise on a
			// plain key.
			before:    "r0: &x0 # al\r  # d\rr1: &x1 # al\r  c:\r   k: !t *x0\r\r   <<: {!t true: x}\r  <<: {} # f\r  p: *x0 # al\r",
			construct: edit.ConstructAnchoredEmpty,
			err: stringtest.JoinLF(
				"test.yaml:1:5: $.r0: this program does not edit a document with an anchor on an empty value, such as &x0",
				"`-- write a value after &x0, such as null",
			),
		},
		"anchors and aliases": {
			before:    "f: &x\r  - 1\rp: &p\r  a: 1\ry: [*x]\rq:\r  <<: *p\r  b: 2\r",
			construct: edit.ConstructLineBreaks,
			err: stringtest.JoinLF(
				"test.yaml:1:6: $: this program does not edit a text with a \"\\r\" alone as a line break",
				"`-- write each line break as \"\\n\", or each one as \"\\r\\n\"",
			),
		},
		"last entry of the root before a comment line, after a line that ends in a carriage return and a line feed": {
			before:    "a: 1\r\n# tail\n",
			construct: edit.ConstructLineBreaks,
			err: stringtest.JoinLF(
				"test.yaml:2:7: $: this program does not edit a text with both \"\\n\" and \"\\r\\n\" line breaks",
				"`-- write each line break as \"\\n\", or each one as \"\\r\\n\"",
			),
		},
		"new key of text with lone carriage returns that an earlier removal left on one line": {
			before:    "p: 1\rn: 2",
			construct: edit.ConstructLineBreaks,
			err: stringtest.JoinLF(
				"test.yaml:1:5: $: this program does not edit a text with a \"\\r\" alone as a line break",
				"`-- write each line break as \"\\n\", or each one as \"\\r\\n\"",
			),
		},
		"only item of a flow sequence on lines that end with a lone carriage return": {
			before:    "g: [\r\r\n  0\r\r\n]\r\r\nk: [5]\r\n",
			construct: edit.ConstructLineBreaks,
			err: stringtest.JoinLF(
				"test.yaml:1:5: $: this program does not edit a text with a \"\\r\" alone as a line break",
				"`-- write each line break as \"\\n\", or each one as \"\\r\\n\"",
			),
		},
		"key that a decode reads as a boolean key of the mapping": {
			before:    stringtest.LinesLF("m:", "  True: a"),
			construct: edit.ConstructKeyText,
			err: stringtest.JoinLF(
				"test.yaml:2:3: $.m: this program does not edit a key that reads as another value than its text, such as True, which reads as true",
				"`-- quote the key, as in \"True\"",
			),
		},
		"key that a decode reads as a hex key of a flow mapping": {
			before:    stringtest.LinesLF("m: {0x10: a}"),
			construct: edit.ConstructKeyText,
			err: stringtest.JoinLF(
				"test.yaml:1:5: $.m: this program does not edit a key that reads as another value than its text, such as 0x10, which reads as 16",
				"`-- quote the key, as in \"0x10\"",
			),
		},
		"key that a decode reads as a hex key of the mapping": {
			before:    stringtest.LinesLF("m:", "  0x10: a"),
			construct: edit.ConstructKeyText,
			err: stringtest.JoinLF(
				"test.yaml:2:3: $.m: this program does not edit a key that reads as another value than its text, such as 0x10, which reads as 16",
				"`-- quote the key, as in \"0x10\"",
			),
		},
		"key that a decode reads as a null key of the mapping": {
			before:    stringtest.LinesLF("m:", "  ~: a"),
			construct: edit.ConstructKeyText,
			err: stringtest.JoinLF(
				"test.yaml:2:3: $.m: this program does not edit a key that reads as another value than its text, such as ~, which reads as the null value",
				"`-- quote the key, as in \"~\"",
			),
		},
		"removal of a key at the root that hides a key of its decoded key, then of the first key": {
			before:    stringtest.LinesLF("r0: x", "1.0: a", "1: b"),
			construct: edit.ConstructKeyText,
			err: stringtest.JoinLF(
				"test.yaml:2:1: $: this program does not edit a key that reads as another value than its text, such as 1.0, which reads as 1",
				"`-- quote the key, as in \"1.0\"",
			),
		},
		"rename to the text of a float key of a flow mapping, with duplicate keys": {
			before:    stringtest.LinesLF("m: {1.0: a, c: b}"),
			dup:       true,
			construct: edit.ConstructKeyText,
			err: stringtest.JoinLF(
				"test.yaml:1:5: $.m: this program does not edit a key that reads as another value than its text, such as 1.0, which reads as 1",
				"`-- quote the key, as in \"1.0\"",
			),
		},
		"set of a key that a merge key brings in, then a move of it": {
			before:    stringtest.LinesLF("<<: {0o20: 2}", "a: 1"),
			construct: edit.ConstructKeyText,
			err: stringtest.JoinLF(
				"test.yaml:1:6: $: this program does not edit a key that reads as another value than its text, such as 0o20, which reads as 16",
				"`-- quote the key, as in \"0o20\"",
			),
		},
		"value at a bool entry that a merge key brings in, which an own key hides": {
			before:    stringtest.LinesLF("<<: {TRUE: 2, b: q}", "True: a b"),
			construct: edit.ConstructKeyText,
			err: stringtest.JoinLF(
				"test.yaml:1:6: $: this program does not edit a key that reads as another value than its text, such as TRUE, which reads as true",
				"`-- quote the key, as in \"TRUE\"",
			),
		},
		"value at a null entry that a merge key brings in, which an own key hides": {
			before:    stringtest.LinesLF("<<: {k: 2, Null: q}", "!!null null: {}"),
			construct: edit.ConstructKeyText,
			err: stringtest.JoinLF(
				"test.yaml:1:12: $: this program does not edit a key that reads as another value than its text, such as Null, which reads as the null value",
				"`-- quote the key, as in \"Null\"",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := []niceyaml.SourceOption{niceyaml.WithName("test.yaml"), niceyaml.WithAllowDuplicateKeys(tc.dup)}
			for _, ref := range tc.refs {
				opts = append(
					opts,
					niceyaml.WithReferences(niceyaml.NewSourceFromString(ref, niceyaml.WithName("ref.yaml"))),
				)
			}

			docs := niceyaml.NewSourceFromString(tc.before, opts...).AllDocuments()
			require.Greater(t, len(docs), tc.doc)

			_, err := edit.New(docs[tc.doc], tc.opts...)
			if tc.err == "" {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, edit.ErrUnsupported)
			require.NotErrorIs(t, err, edit.ErrEngine)

			var unsupported *edit.UnsupportedError

			require.ErrorAs(t, err, &unsupported)

			related := make([]string, 0, len(unsupported.Related))
			for _, p := range unsupported.Related {
				related = append(related, p.String())
			}

			assert.Equal(t, tc.construct, unsupported.Construct)
			assert.False(t, unsupported.InValue)
			assert.Equal(t, append([]string{}, tc.related...), related)
			assert.True(t, niceyaml.IsInvalid(err))
			assert.Contains(t, niceyaml.FormatError(err), tc.err)
		})
	}
}

func TestNew_ByteOrderMark(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		text string
		// A line of the error of New, which matches edit.ErrMisread, or the
		// user error of a mark that YAML does not allow, or "" for a
		// document that New takes.
		err string
		// The document of the text that New takes.
		doc int
		// Whether YAML does not allow the mark, so the error is a user error.
		invalid bool
	}{
		"mark after the header of the document": {
			// YAML does not allow a mark inside a document, and the tokenizer
			// drops the mark that go-yaml reads as part of the key.
			text:    "---\n\ufeffa: 1\n",
			err:     "2:1: $: a byte order mark at 2:1 inside the document, where YAML does not allow it",
			invalid: true,
		},
		"mark after the header of a later document": {
			text:    stringtest.LinesLF("a: 1", "---", "\ufeffb: 1"),
			doc:     1,
			err:     "3:1: $: a byte order mark at 3:1 inside the document, where YAML does not allow it",
			invalid: true,
		},
		"mark alone on the line after the header of a later document": {
			text:    stringtest.LinesLF("a: 1", "---", "\ufeff", "b: 1"),
			doc:     1,
			err:     "3:1: $: a byte order mark at 3:1 inside the document, where YAML does not allow it",
			invalid: true,
		},
		"mark before a comment line": {
			// The tokenizer drops the mark, and go-yaml reads it as text.
			text: "\ufeff# c\na: 1\n",
			err:  "1:1: $: go-yaml reads the byte order mark at 1:1 as text of the document",
		},
		"mark before the header of the document": {
			text: "\ufeff---\na: 1\n",
			err:  "1:1: $: go-yaml reads the byte order mark at 1:1 as text of the document",
		},
		"mark before a key that the text repeats with a mark": {
			text: "\ufeffa: 1\n\"\ufeffa\": 2\n",
			err:  "1:1: $: go-yaml reads the byte order mark at 1:1 as text of the document",
		},
		"mark before a key": {
			// Go-yaml's parser takes the text, and it reads the mark as part of
			// the key.
			text: "\ufeffa: 1\n",
			err:  "1:1: $: go-yaml reads the byte order mark at 1:1 as text of the document",
		},
		"mark before a flow mapping": {
			text: "\ufeff{a: 1}\n",
			err:  "1:1: $: go-yaml reads the byte order mark at 1:1 as text of the document",
		},

		"mark after a comment line before a key": {
			// YAML allows the mark in the prefix of the document, after its
			// comment lines.
			text: "# c\n\ufeffa: 1\n",
			err:  "2:1: $: go-yaml reads the byte order mark at 2:1 as text of the document",
		},
		"two marks before a key": {
			text: "\ufeff\ufeffa: 1\n",
			err:  "1:1: $: go-yaml reads the byte order mark at 1:1 as text of the document",
		},
		"mark after the end of a document before a comment line and the header of the next one": {
			// The mark lies in the prefix of the next document. The text of the
			// first document holds the comment line, and FileText keeps the
			// mark.
			text: stringtest.LinesLF("a: 1", "...", "\ufeff# c", "---", "b: 2"),
		},
		"mark alone after the end of a document": {
			text: stringtest.LinesLF("a: 1", "...", "\ufeff", "b: 2"),
		},
		"mark after a directive before the header of a later document": {
			text: stringtest.LinesLF("a: 1", "...", "%YAML 1.2", "\ufeff---", "b: 2"),
			doc:  1,
		},
		"mark at the start of a later document": {
			// The mark stands outside the text of the document, and FileText
			// keeps it.
			text: stringtest.LinesLF("a: 1", "...", "\ufeffb: 2"),
			doc:  1,
		},
		"mark before the header of a later document": {
			text: stringtest.LinesLF("a: 1", "...", "\ufeff---", "b: 2"),
			doc:  1,
		},
		"mark after a comment line of a later document": {
			// The mark lies in the prefix of the document, which is go-yaml's
			// reading in a later document.
			text: stringtest.LinesLF("a: 1", "...", "# c", "\ufeffb: 2"),
			doc:  1,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			docs := niceyaml.NewSourceFromString(tc.text, niceyaml.WithName("test.yaml")).Documents()
			require.Greater(t, len(docs), tc.doc)

			d, err := edit.New(docs[tc.doc])
			if tc.err == "" {
				require.NoError(t, err)

				text, err := edit.FileText(d)
				require.NoError(t, err)
				assert.Equal(t, tc.text, text)

				return
			}

			if tc.invalid {
				require.Error(t, err)
				require.NotErrorIs(t, err, edit.ErrEngine)
				assert.True(t, niceyaml.IsInvalid(err))
				assert.Contains(t, niceyaml.FormatError(err), tc.err)
				assert.Contains(t, niceyaml.FormatError(err), "`-- remove the byte order mark")

				return
			}

			require.ErrorIs(t, err, edit.ErrMisread)
			require.ErrorIs(t, err, edit.ErrEngine)
			require.NotErrorIs(t, err, edit.ErrUnsupported)
			assert.False(t, niceyaml.IsInvalid(err))
			assert.Contains(t, niceyaml.FormatError(err), tc.err)
			assert.Contains(t, niceyaml.FormatError(err), "`-- remove the byte order mark")
		})
	}
}

func TestDoc_Apply_Unsupported(t *testing.T) {
	t.Parallel()

	set := func(path string, v any) edit.Func {
		return func(ctx context.Context, root *edit.Node) error {
			return root.At(paths.MustParse(path)).Set(ctx, v)
		}
	}

	rename := func(path, key string) edit.Func {
		return func(_ context.Context, root *edit.Node) error {
			return root.At(paths.MustParse(path)).SetKey(key)
		}
	}

	remove := func(path string) edit.Func {
		return func(_ context.Context, root *edit.Node) error {
			return root.At(paths.MustParse(path)).Remove()
		}
	}

	move := func(path, target string) edit.Func {
		return func(_ context.Context, root *edit.Node) error {
			return root.At(paths.MustParse(path)).Move(paths.MustParse(target))
		}
	}

	// The Func that both returns runs first, then second.
	both := func(first, second edit.Func) edit.Func {
		return func(ctx context.Context, root *edit.Node) error {
			err := first(ctx, root)
			if err != nil {
				return err
			}

			return second(ctx, root)
		}
	}

	// The Func that read returns reads the text at path, then runs then.
	read := func(path string, then edit.Func) edit.Func {
		return func(ctx context.Context, root *edit.Node) error {
			root.At(paths.MustParse(path)).Text()

			return then(ctx, root)
		}
	}

	var (
		inline   = stringtest.LinesLF("m:", "  <<: {a: 1}", "  b: 2")
		seq      = stringtest.LinesLF("s: &s {a: 1}", "t: &t {c: 1}", "m:", "  <<: [*s, *t]", "  b: 2")
		repeated = stringtest.LinesLF("a: 1", "a: 2", "b: 3")
		explicit = stringtest.LinesLF("spec:", "  ? b", "  : x", "c: 1")
		comment  = stringtest.LinesLF("f: [1, # c", "  2]", "z: 1")
		bare     = stringtest.LinesLF("f: [0, a: 1]", "g: [5]")
	)

	tcs := map[string]struct {
		fn     edit.Func
		before string
		// The text of a reference document of the document, or "".
		ref string
		// The text of the Doc after an Apply that passes.
		want string
		// The lines of the error of the Apply before its excerpt, or "" for
		// an Apply that passes.
		err string
		// The paths of the other sites of the construct.
		related []string
		// The construct of the error, or 0 for an ordinary error.
		construct edit.Construct
		// Whether the Source allows duplicate keys.
		dup bool
		// Whether the construct lies in the key that the program passed.
		inValue bool
		// Whether the error matches paths.ErrReferenceAlias.
		refAlias bool
	}{
		"move of an item onto a key that a merge key brings in": {
			// The key that the move writes stands for the "-" that held the
			// value, so the problem binds where the user wrote that "-", with
			// a detail at the merge key.
			before:    stringtest.LinesLF("s: &s {b: 2}", "m:", "  <<: *s", "  o: 1", "l:", "  - x"),
			fn:        move("$.l[0]", "$.m.b"),
			construct: edit.ConstructMergeKeys,
			related:   []string{"$.m"},
			err: "test.yaml:6:3: $.l[0]: the edit moves $.l[0] to $.m.b, but <<: *s at 3:3 also brings in b, so $.m " +
				"then hides the b of $.s\n|-- 3:3: $.m: <<: *s brings in b here",
		},
		"move of an item of a flow sequence onto a key that a merge key brings in": {
			before:    stringtest.LinesLF("s: &s {b: 2}", "m:", "  <<: *s", "  o: 1", "l: [x]"),
			fn:        move("$.l[0]", "$.m.b"),
			construct: edit.ConstructMergeKeys,
			related:   []string{"$.m"},
			err: "test.yaml:5:5: $.l[0]: the edit moves $.l[0] to $.m.b, but <<: *s at 3:3 also brings in b, so $.m " +
				"then hides the b of $.s",
		},
		"move of an entry onto a key that a merge key brings in": {
			// A control: the problem binds where the user wrote the key.
			before:    stringtest.LinesLF("s: &s {b: 2}", "m:", "  <<: *s", "  o: 1", "n:", "  c: x"),
			fn:        move("$.n.c", "$.m.b"),
			construct: edit.ConstructMergeKeys,
			related:   []string{"$.m"},
			err: "test.yaml:6:3: $.n.c: the edit moves $.n.c to $.m.b, but <<: *s at 3:3 also brings in b, so $.m " +
				"then hides the b of $.s",
		},
		"removal in a mapping whose first merge key reads a reference document and whose second merges a sequence": {
			// A mapping with more than one merge key binds at its first, whatever
			// each merge key reads.
			before: stringtest.LinesLF("x: &x {p: 1}", "y: &y {q: 1}", "m:", "  <<: *r", "  <<: [*x, *y]",
				"  o2: 1"),
			ref:       "r: &r {a: 9}\n",
			dup:       true,
			fn:        remove("$.m.o2"),
			construct: edit.ConstructMergeForm,
			related:   []string{"$.x", "$.y"},
			err: "test.yaml:4:3: $.m: the edit removes $.m.o2, which changes the keys of a merge through a merge key " +
				"that merges a sequence of aliases, and this program does not change the keys of such a merge",
		},
		"removal in a mapping whose first merge key reads a reference document and whose second has a tag": {
			before:    stringtest.LinesLF("x: &x {p: 1}", "m:", "  <<: *r", "  !!merge <<: *x", "  o2: 1"),
			ref:       "r: &r {a: 9}\n",
			dup:       true,
			fn:        remove("$.m.o2"),
			construct: edit.ConstructMergeForm,
			related:   []string{"$.x"},
			err: "test.yaml:3:3: $.m: the edit removes $.m.o2, which changes the keys of a merge through a merge key " +
				"with the tag !!merge",
		},
		"set below a scalar that a merge sequence brings in": {
			// The path looks b up through the refused merge key and then steps
			// into the scalar, so the refusal comes first (P38).
			before:    stringtest.LinesLF("x: &x {b: 1}", "m:", "  d: 0", "  <<: [*x, {c: 2}]"),
			fn:        set("$.m.b.z", 3),
			construct: edit.ConstructMergeForm,
			related:   []string{"$.x"},
			err: "test.yaml:4:3: $.m: the edit sets $.m.b.z, which goes through a merge key that merges a sequence " +
				"that holds a mapping, and this program does not edit through such a merge key",
		},
		"set through a merge key that merges a sequence of a mapping": {
			// The message names the form that the text holds.
			before:    stringtest.LinesLF("m:", "  <<: [{w: 4}]"),
			fn:        set("$.m.w", 2),
			construct: edit.ConstructMergeForm,
			err: "test.yaml:2:3: $.m: the edit sets $.m.w, which goes through a merge key that merges a sequence that " +
				"holds a mapping, and this program does not edit through such a merge key",
		},
		"set through a merge key that merges a sequence of scalars": {
			before:    stringtest.LinesLF("m:", "  <<: [1]"),
			fn:        set("$.m.w", 2),
			construct: edit.ConstructMergeForm,
			err: "test.yaml:2:3: $.m: the edit sets $.m.w, which goes through a merge key that merges a sequence, " +
				"and this program does not edit through such a merge key",
		},
		"set below a key that neither a mapping nor its merge sequence holds": {
			// The lookup of q goes through the merge key, before the index of
			// the path meets the missing value.
			before:    stringtest.LinesLF("x: &x {b: 1}", "m:", "  d: 0", "  <<: [*x, {c: 2}]"),
			fn:        set("$.m.q[3]", 3),
			construct: edit.ConstructMergeForm,
			related:   []string{"$.x"},
			err: "test.yaml:4:3: $.m: the edit sets $.m.q[3], which goes through a merge key that merges a " +
				"sequence that holds a mapping",
		},
		"set below an own scalar of a mapping with a merge sequence": {
			// Control: the path finds d in the mapping itself.
			before: stringtest.LinesLF("x: &x {b: 1}", "m:", "  <<: [*x, {c: 2}]", "  d: 0"),
			fn:     set("$.m.d.z", 3),
			err:    "test.yaml:4:6: $.m.d: the edit sets $.m.d.z, but d holds a scalar",
		},
		"removal below a scalar that a merge sequence brings in": {
			// The removal of a place below a scalar does nothing, but the path
			// looks b up through the refused merge key first (P38).
			before:    stringtest.LinesLF("x: &x {b: 1}", "m:", "  d: 0", "  <<: [*x, {c: 2}]"),
			fn:        remove("$.m.b.z"),
			construct: edit.ConstructMergeForm,
			related:   []string{"$.x"},
			err: "test.yaml:4:3: $.m: the edit removes $.m.b.z, which goes through a merge key that merges a " +
				"sequence that holds a mapping",
		},
		"removal of a key that neither a mapping nor its merge sequence holds": {
			before:    stringtest.LinesLF("x: &x {b: 1}", "m:", "  d: 0", "  <<: [*x, {c: 2}]"),
			fn:        remove("$.m.q"),
			construct: edit.ConstructMergeForm,
			related:   []string{"$.x"},
			err: "test.yaml:4:3: $.m: the edit removes $.m.q, which goes through a merge key that merges a " +
				"sequence that holds a mapping",
		},
		"removal of the merge key of a mapping with a merge sequence": {
			// No path selects a << merge key, so the path names a key that the
			// mapping lacks, and its lookup goes through the merge key, as the
			// lookup of any other missing key does.
			before:    stringtest.LinesLF("x: &x {b: 1}", "m:", "  d: 0", "  <<: [*x, {c: 2}]"),
			fn:        remove("$.m.<<"),
			construct: edit.ConstructMergeForm,
			related:   []string{"$.x"},
			err: "test.yaml:4:3: $.m: the edit removes $.m.<<, which goes through a merge key that merges a " +
				"sequence that holds a mapping, and this program does not edit through such a merge key",
		},
		"rename of the merge key of a mapping with a tagged merge key": {
			before:    stringtest.LinesLF("s: &s {a: 1}", "m:", "  !!merge <<: *s", "  b: 2"),
			fn:        rename("$.m.<<", "z"),
			construct: edit.ConstructMergeForm,
			related:   []string{"$.s"},
			err: "test.yaml:3:3: $.m: the edit renames $.m.<< to \"z\", which goes through a merge key with the " +
				"tag !!merge, and this program does not edit through such a merge key",
		},
		"move of the merge key of a mapping whose merge key reads a scalar under a tag": {
			before:    stringtest.LinesLF("m:", "  <<: !include base.yaml", "  b: 2"),
			fn:        move("$.m.<<", "$.z"),
			construct: edit.ConstructMergeForm,
			err: "test.yaml:2:3: $.m: the edit moves $.m.<< to $.z, which goes through a merge key whose value has " +
				"the tag !include, and this program does not edit through such a merge key",
		},
		"removal of the merge key of a mapping with a merge key of one alias": {
			// Control: the merge key reads one alias of a mapping, so the lookup
			// of the missing key passes.
			before: stringtest.LinesLF("s: &s {a: 1}", "m:", "  <<: *s", "  b: 2"),
			fn:     remove("$.m.<<"),
			want:   stringtest.LinesLF("s: &s {a: 1}", "m:", "  <<: *s", "  b: 2"),
		},
		"removal below an own scalar of a mapping with a merge sequence": {
			// Control: the path finds d in the mapping itself.
			before: stringtest.LinesLF("x: &x {b: 1}", "m:", "  <<: [*x, {c: 2}]", "  d: 0"),
			fn:     remove("$.m.d.z"),
			want:   stringtest.LinesLF("x: &x {b: 1}", "m:", "  <<: [*x, {c: 2}]", "  d: 0"),
		},
		"rename through a merge sequence of reference aliases after an own entry": {
			// The own entry lies outside the zone of the refused form, so no
			// step before the merge step lies inside the zone. The merge step
			// meets the alias of the reference document first (fix17 ruling
			// P50, Ambiguity 37 of the oracle).
			before:   stringtest.LinesLF("a: 1", "<<: [*rm0, *rm1]"),
			ref:      stringtest.LinesLF("m0: &rm0 {k: 1}", "m1: &rm1 {b: 2}"),
			fn:       rename("$.b", "c"),
			refAlias: true,
			err:      "test.yaml:2:6: $: the edit renames $.b, which goes through <<: [*rm0, *rm1] at 2:1",
		},
		"set through a merge sequence of a local and a reference alias, before an own entry": {
			before:   stringtest.LinesLF("l: &l {q: 1}", "m:", "  <<: [*l, *rm0]", "  b: 1"),
			ref:      "m0: &rm0 {y: 1}\n",
			fn:       set("$.m.new", 1),
			refAlias: true,
			err:      "test.yaml:3:12: $.m: the edit sets $.m.new, which goes through <<: [*l, *rm0] at 3:3",
		},
		"set through a merge sequence of a local and a reference alias, after an own entry": {
			before:   stringtest.LinesLF("l: &l {q: 1}", "m:", "  b: 1", "  <<: [*l, *rm0]"),
			ref:      "m0: &rm0 {y: 1}\n",
			fn:       set("$.m.new", 1),
			refAlias: true,
			err:      "test.yaml:4:12: $.m: the edit sets $.m.new, which goes through <<: [*l, *rm0] at 4:3",
		},
		"set through a merge sequence of a local and a reference alias in a flow mapping": {
			// The braces of the mapping lie outside the zone of the merge key.
			before:   stringtest.LinesLF("l: &l {q: 1}", "m: {<<: [*l, *rm0]}"),
			ref:      "m0: &rm0 {y: 1}\n",
			fn:       set("$.m.new", 1),
			refAlias: true,
			err:      "test.yaml:2:14: $.m: the edit sets $.m.new, which goes through <<: [*l, *rm0] at 2:5",
		},
		"set through a merge sequence of one reference alias, before an own entry": {
			before:   stringtest.LinesLF("m:", "  <<: [*rm0]", "  b: 1"),
			ref:      "m0: &rm0 {y: 1}\n",
			fn:       set("$.m.new", 1),
			refAlias: true,
			err:      "test.yaml:2:8: $.m: the edit sets $.m.new, which goes through <<: [*rm0] at 2:3",
		},
		"set through a merge sequence of a local and a reference alias, the only entry": {
			// Control: the mapping is the merge entry, which the zone holds, so
			// the step into the mapping lies inside the zone, before the merge
			// step.
			before:    stringtest.LinesLF("l: &l {q: 1}", "m:", "  <<: [*l, *rm0]"),
			ref:       "m0: &rm0 {y: 1}\n",
			fn:        set("$.m.new", 1),
			construct: edit.ConstructMergeForm,
			related:   []string{"$.l"},
			err: "test.yaml:3:3: $.m: the edit sets $.m.new, which goes through a merge key that merges a " +
				"sequence of aliases",
		},
		"set through a merge sequence of a local and a reference alias, the only entry of an anchored mapping": {
			// The anchor of the mapping lies outside the zone, but the mapping
			// that the step looks the name up in lies inside it.
			before:    stringtest.LinesLF("l: &l {q: 1}", "m: &m", "  <<: [*l, *rm0]"),
			ref:       "m0: &rm0 {y: 1}\n",
			fn:        set("$.m.new", 1),
			construct: edit.ConstructMergeForm,
			related:   []string{"$.l"},
			err: "test.yaml:3:3: $.m: the edit sets $.m.new, which goes through a merge key that merges a " +
				"sequence of aliases",
		},
		"set through a merge of a mapping whose merge key reads a reference document, after an own entry": {
			// The problem shows the path of the value that holds the alias (fix19 B6).
			// The lookup of the name goes through the refused merge key of m
			// and stops at the alias of the reference document in its source,
			// and no step before it lies inside the zone.
			before:   stringtest.LinesLF("l: &l", "  <<: *rm0", "m:", "  a: 1", "  <<: *l"),
			ref:      "m0: &rm0 {b: 2, a: 3}\n",
			fn:       set("$.m.zz9", 7),
			refAlias: true,
			err:      "test.yaml:2:7: $.l: the edit sets $.m.zz9, which goes through <<: *rm0 at 2:3",
		},
		"set in an anchored source whose merge key of a reference document is its only entry": {
			// The zone of the merge key of m holds the merge entry of its
			// source l whole, which is all that l holds, so the step that looks
			// the name up in l lies inside the zone.
			before:    stringtest.LinesLF("l: &l", "  <<: *rm0", "m:", "  a: 1", "  <<: *l"),
			ref:       "m0: &rm0 {b: 2, a: 3}\n",
			fn:        set("$.l.zz9", 7),
			construct: edit.ConstructMergeForm,
			related:   []string{"$.l"},
			err: "test.yaml:5:3: $.m: the edit sets $.l.zz9, which goes through a merge key that merges a " +
				"mapping with a merge key of its own",
		},
		"set in a source of a mapping whose merge key merges a mapping that reads a reference document": {
			// The path of the Set reads the merge key of the source, not the
			// merge key of the mapping that merges the source, so the refused
			// form of that merge key does not meet the call.
			before:   stringtest.LinesLF("s: &s", "  <<: *rm0", "  a: 1", "m:", "  <<: *s", "  b: 2"),
			ref:      "m0: &rm0 {b: 2, a: 3}\n",
			fn:       set("$.s.zz9", 7),
			refAlias: true,
			err:      "test.yaml:2:7: $.s: the edit sets $.s.zz9, which goes through <<: *rm0 at 2:3",
		},
		"move of a mapping with two merge keys of reference aliases": {
			// Neither merge key is a refused form, but the mapping repeats the
			// key <<, and the Move writes both entries again at its target.
			before:    stringtest.LinesLF("m:", "  <<: *rm0", "  <<: *rm1", "k: 1"),
			ref:       stringtest.LinesLF("m0: &rm0 {b: 2}", "m1: &rm1 {c: 4}"),
			dup:       true,
			fn:        move("$.m", "$.zz9"),
			construct: edit.ConstructRepeatedKey,
			related:   []string{"$.m"},
			err: `test.yaml:2:3: $.m: the edit moves $.m to $.zz9, which reaches "<<", a key that its mapping ` +
				"repeats, and this program does not edit the entries of a repeated key\n|-- 3:3: $.m: the key " +
				`"<<" is written again here`,
		},
		"removal of an own entry of a mapping with two merge keys of reference aliases": {
			// Control: the repeated key << stays, and the reference documents
			// decide which names the mapping reads.
			before:   stringtest.LinesLF("m:", "  <<: *rm0", "  <<: *rm1", "  a: 1"),
			ref:      stringtest.LinesLF("m0: &rm0 {b: 2}", "m1: &rm1 {c: 4}"),
			dup:      true,
			fn:       remove("$.m.a"),
			refAlias: true,
			err: "test.yaml:4:3: $.m.a: the edit removes $.m.a, but <<: *rm0 at 2:3 reads a reference document, " +
				"which decides which names $.m reads",
		},
		"set of an own entry of a mapping with two merge keys of reference aliases": {
			// Control: a Set of the value of an own entry keeps the keys.
			before: stringtest.LinesLF("m:", "  <<: *rm0", "  <<: *rm1", "  a: 1"),
			ref:    stringtest.LinesLF("m0: &rm0 {b: 2}", "m1: &rm1 {c: 4}"),
			dup:    true,
			fn:     set("$.m.a", 5),
			want:   stringtest.LinesLF("m:", "  <<: *rm0", "  <<: *rm1", "  a: 5"),
		},
		"rename onto an own key of a mapping that merges an alias of a reference document": {
			// The key that the rename repeats refuses the call before the
			// reference document, which decides which names the mapping
			// reads.
			before:    stringtest.LinesLF("m:", "  <<: *rm0", "  a: 1", "  b: 2"),
			ref:       "m0: &rm0 {x: 2}\n",
			dup:       true,
			fn:        rename("$.m.a", "b"),
			construct: edit.ConstructRepeatedKey,
			inValue:   true,
			related:   []string{"$.m.b"},
			err: `test.yaml:3:3: $.m.a: the edit renames $.m.a to "b", which writes the key "b" a second time ` +
				"in its mapping",
		},
		"rename onto a key of the mapping after a rename of the entry": {
			// The key that the first rename wrote stands for the key c of the
			// user, so the problem binds where the user wrote c, under the path
			// that the user's text spells for it (fix19 P56).
			before: stringtest.LinesLF("b: 1", "c: x"),
			dup:    true,
			fn: func(ctx context.Context, root *edit.Node) error {
				err := rename("$.c", "d")(ctx, root)
				if err != nil {
					return err
				}

				return rename("$.d", "b")(ctx, root)
			},
			construct: edit.ConstructRepeatedKey,
			inValue:   true,
			related:   []string{"$.b"},
			err: `test.yaml:2:1: $.c: the edit renames $.d to "b", which writes the key "b" a second time in its ` +
				"mapping",
		},
		"rename onto a key of the mapping after a move of the entry": {
			// The key that the Move wrote stands for the key c of the user, so
			// the problem binds where the user wrote c, under $.n.c (fix19 P56).
			before: stringtest.LinesLF("m:", "  b: 1", "n:", "  c: x"),
			dup:    true,
			fn: func(ctx context.Context, root *edit.Node) error {
				err := move("$.n.c", "$.m.d")(ctx, root)
				if err != nil {
					return err
				}

				return rename("$.m.d", "b")(ctx, root)
			},
			construct: edit.ConstructRepeatedKey,
			inValue:   true,
			related:   []string{"$.m.b"},
			err: `test.yaml:4:3: $.n.c: the edit renames $.m.d to "b", which writes the key "b" a second time in ` +
				"its mapping",
		},
		"rename onto a key of the mapping after a rename of the entry, without duplicate keys": {
			// Control: the error of the rename binds where the user wrote the
			// key that the first rename renamed.
			before: stringtest.LinesLF("b: 1", "c: x"),
			fn:     both(rename("$.c", "d"), rename("$.d", "b")),
			err:    `test.yaml:2:1: $.c~: the edit renames "d" to "b", which already exists`,
		},
		"rename of a created key onto a key of its mapping": {
			// The key that the Set created binds as a created value does, at the
			// key of the value above it under the path of that value (fix19
			// P56).
			before: stringtest.LinesLF("m:", "  b: 1"),
			fn:     both(set("$.m.c", 1), rename("$.m.c", "b")),
			err: `test.yaml:1:1: $.m: the edit renames "c" to "b", which already exists` + "\n" +
				`|-- 2:3: $.m.b~: "b" is defined here`,
		},
		"rename onto a created key": {
			// The detail at the created key binds as a created value does (fix19
			// P56).
			before: stringtest.LinesLF("m:", "  b: 1"),
			fn:     both(set("$.m.c", 1), rename("$.m.b", "c")),
			err: `test.yaml:2:3: $.m.b~: the edit renames "b" to "c", which already exists` + "\n" +
				`|-- 1:1: $.m: "c" is defined here`,
		},
		"rename of a created key onto a key of its mapping, with duplicate keys": {
			before:    stringtest.LinesLF("m:", "  b: 1"),
			dup:       true,
			fn:        both(set("$.m.c", 1), rename("$.m.c", "b")),
			construct: edit.ConstructRepeatedKey,
			inValue:   true,
			related:   []string{"$.m.b"},
			err: `test.yaml:1:1: $.m: the edit renames $.m.c to "b", which writes the key "b" a second time in its ` +
				"mapping",
		},
		"rename onto a created key, with duplicate keys": {
			before:    stringtest.LinesLF("m:", "  b: 1"),
			dup:       true,
			fn:        both(set("$.m.c", 1), rename("$.m.b", "c")),
			construct: edit.ConstructRepeatedKey,
			inValue:   true,
			related:   []string{"$.m"},
			err: `test.yaml:2:3: $.m.b: the edit renames $.m.b to "c", which writes the key "c" a second time in its ` +
				"mapping, and this program does not write a key that its mapping repeats\n|-- 1:1: $.m: the key " +
				`"c" is written here`,
		},
		"rename onto a key of the mapping": {
			// Control: one rename binds at the key that it renames.
			before:    stringtest.LinesLF("b: 1", "c: x"),
			dup:       true,
			fn:        rename("$.c", "b"),
			construct: edit.ConstructRepeatedKey,
			inValue:   true,
			related:   []string{"$.b"},
			err: `test.yaml:2:1: $.c: the edit renames $.c to "b", which writes the key "b" a second time in its ` +
				"mapping",
		},
		"rename onto a merged key after a rename of the entry": {
			// The key that the first rename wrote stands for the key c of the
			// user, so the problem binds where the user wrote c (fix19 P56).
			before:    stringtest.LinesLF("l: &l {b: 1}", "m:", "  <<: *l", "  c: x"),
			fn:        both(rename("$.m.c", "d"), rename("$.m.d", "b")),
			construct: edit.ConstructMergeKeys,
			related:   []string{"$.m"},
			err: `test.yaml:4:3: $.m.c: the edit renames $.m.d to "b", but <<: *l at 3:3 also brings in b, so ` +
				"$.m then hides the b of $.l\n|-- 3:3: $.m: <<: *l brings in b here",
		},
		"rename onto a merged key": {
			// Control: one rename binds at the key that it renames.
			before:    stringtest.LinesLF("l: &l {b: 1}", "m:", "  <<: *l", "  c: x"),
			fn:        rename("$.m.c", "b"),
			construct: edit.ConstructMergeKeys,
			related:   []string{"$.m"},
			err: `test.yaml:4:3: $.m.c: the edit renames $.m.c to "b", but <<: *l at 3:3 also brings in b, so ` +
				"$.m then hides the b of $.l",
		},
		"rename onto a merged key after a move of the entry": {
			// The key that the Move wrote stands for the key c of the user under
			// $.n (fix19 P56).
			before:    stringtest.LinesLF("l: &l {b: 1}", "m:", "  <<: *l", "n:", "  c: x"),
			fn:        both(move("$.n.c", "$.m.d"), rename("$.m.d", "b")),
			construct: edit.ConstructMergeKeys,
			related:   []string{"$.m"},
			err: `test.yaml:5:3: $.n.c: the edit renames $.m.d to "b", but <<: *l at 3:3 also brings in b, so ` +
				"$.m then hides the b of $.l",
		},
		"rename onto a key that a merge key of the root brings in, after a rename of the entry": {
			// The problem binds where the user wrote the key c, under $.c
			// (fix19 P56).
			before:    stringtest.LinesLF("l: &l {b: 1}", "<<: *l", "c: x"),
			fn:        both(rename("$.c", "d"), rename("$.d", "b")),
			construct: edit.ConstructMergeKeys,
			related:   []string{"$"},
			err: `test.yaml:3:1: $.c: the edit renames $.d to "b", but <<: *l at 2:1 also brings in b, so $ then ` +
				"hides the b of $.l\n|-- 2:1: $: <<: *l brings in b here",
		},
		"move of a block value into a flow mapping after a rename of its key": {
			// Control: the block value that the user wrote binds at its first
			// key, under the path that the user's text spells for it.
			before:    stringtest.LinesLF("f: {}", "c:", "  k: 1"),
			fn:        both(rename("$.c", "d"), move("$.d", "$.f.d")),
			construct: edit.ConstructFlowLayout,
			err: "test.yaml:3:3: $.c: the edit moves $.d to $.f.d, which puts a block value inside a flow " +
				"collection",
		},
		"move of a sequence whose item holds two merge keys of reference aliases": {
			// The merge keys bind under the path of the item that holds them
			// (fix19 P56).
			before:    stringtest.LinesLF("l:", "  - 1", "  - <<: *rm0", "    <<: *rm1"),
			ref:       stringtest.LinesLF("m0: &rm0 {y: 1}", "m1: &rm1 {w: 1}"),
			dup:       true,
			fn:        move("$.l", "$.q"),
			construct: edit.ConstructRepeatedKey,
			related:   []string{"$.l[1]"},
			err: `test.yaml:3:5: $.l[1]: the edit moves $.l to $.q, which reaches "<<", a key that its mapping ` +
				"repeats, and this program does not edit the entries of a repeated key\n|-- 4:5: $.l[1]: the key " +
				`"<<" is written again here`,
		},
		"move of a mapping whose entry holds two merge keys of reference aliases": {
			// Control: the merge keys bind under the path of the mapping.
			before:    stringtest.LinesLF("l:", "  m:", "    <<: *rm0", "    <<: *rm1"),
			ref:       stringtest.LinesLF("m0: &rm0 {y: 1}", "m1: &rm1 {w: 1}"),
			dup:       true,
			fn:        move("$.l", "$.q"),
			construct: edit.ConstructRepeatedKey,
			related:   []string{"$.l.m"},
			err: `test.yaml:3:5: $.l.m: the edit moves $.l to $.q, which reaches "<<", a key that its mapping ` +
				"repeats",
		},
		"removal in an item whose merge key merges a sequence with a reference alias": {
			// The merge key binds under the path of the item (fix19 P56).
			before:    stringtest.LinesLF("x: &x {q: 1}", "l:", "  - 1", "  - <<: [*x, *rm0]", "    a: 1"),
			ref:       "m0: &rm0 {y: 1}\n",
			fn:        remove("$.l[1].a"),
			construct: edit.ConstructMergeForm,
			related:   []string{"$.x"},
			err: "test.yaml:4:5: $.l[1]: the edit removes $.l[1].a, which changes the keys of a merge through a " +
				"merge key that merges a sequence of aliases",
		},
		"removal in an item whose merge key merges a sequence of local aliases": {
			// Control: the same refused form without a reference document.
			before:    stringtest.LinesLF("x: &x {q: 1}", "l:", "  - 1", "  - <<: [*x, *x]", "    a: 1"),
			fn:        remove("$.l[1].a"),
			construct: edit.ConstructMergeForm,
			related:   []string{"$.x"},
			err: "test.yaml:4:5: $.l[1]: the edit removes $.l[1].a, which changes the keys of a merge through a " +
				"merge key that merges a sequence of aliases",
		},
		"removal in an item whose merge key reads a reference document": {
			// The detail at the merge key binds under the path of the item
			// (fix19 P56).
			before:   stringtest.LinesLF("l:", "  - 1", "  - <<: *rm0", "    a: 1"),
			ref:      "m0: &rm0 {y: 1}\n",
			fn:       remove("$.l[1].a"),
			refAlias: true,
			err: "test.yaml:4:5: $.l[1].a: the edit removes $.l[1].a, but <<: *rm0 at 3:5 reads a reference " +
				"document, which decides which names $.l[1] reads\n|-- 3:5: $.l[1]: <<: *rm0 reads a reference " +
				"document here",
		},
		"removal in an item of a root sequence whose merge key reads a reference document": {
			before:   stringtest.LinesLF("- 1", "- <<: *rm0", "  a: 1"),
			ref:      "m0: &rm0 {y: 1}\n",
			fn:       remove("$[1].a"),
			refAlias: true,
			err: "test.yaml:3:3: $[1].a: the edit removes $[1].a, but <<: *rm0 at 2:3 reads a reference " +
				"document, which decides which names $[1] reads\n|-- 2:3: $[1]: <<: *rm0 reads a reference " +
				"document here",
		},
		"removal after a move into a sequence of a mapping whose merge key reads a reference document": {
			// The detail at the merge key binds under the path that the user's
			// text spells for the mapping (fix19 P56).
			before:   stringtest.LinesLF("l:", "  - 1", "m:", "  <<: *rm0", "  a: 1"),
			ref:      "m0: &rm0 {y: 1}\n",
			fn:       both(move("$.m", "$.l[1]"), remove("$.l[1].a")),
			refAlias: true,
			err: "test.yaml:5:3: $.m.a: the edit removes $.l[1].a, but <<: *rm0 at 4:3 reads a reference " +
				"document, which decides which names $.l[1] reads\n|-- 4:3: $.m: <<: *rm0 reads a reference " +
				"document here",
		},
		"removal in an entry whose merge key reads a reference document": {
			// Control: the detail binds under the path of the mapping.
			before:   stringtest.LinesLF("l:", "  m:", "    <<: *rm0", "    a: 1"),
			ref:      "m0: &rm0 {y: 1}\n",
			fn:       remove("$.l.m.a"),
			refAlias: true,
			err: "test.yaml:4:5: $.l.m.a: the edit removes $.l.m.a, but <<: *rm0 at 3:5 reads a reference " +
				"document, which decides which names $.l.m reads\n|-- 3:5: $.l.m: <<: *rm0 reads a reference " +
				"document here",
		},
		"set in an item whose mapping repeats the key": {
			// The first entry binds under the path of the item and the key,
			// though a call by that path reads the later entry (fix19 P56).
			before:    stringtest.LinesLF("l:", "  - 1", "  - a: 1", "    a: 2"),
			dup:       true,
			fn:        set("$.l[1].a", 5),
			construct: edit.ConstructRepeatedKey,
			related:   []string{"$.l[1].a"},
			err: `test.yaml:3:5: $.l[1].a: the edit sets $.l[1].a, which reaches "a", a key that its mapping ` +
				"repeats, and this program does not edit the entries of a repeated key\n|-- 4:5: $.l[1].a: the key " +
				`"a" is written again here`,
		},
		"set in an item of a root sequence whose mapping repeats the key": {
			before:    stringtest.LinesLF("- a: 1", "  a: 2"),
			dup:       true,
			fn:        set("$[0].a", 5),
			construct: edit.ConstructRepeatedKey,
			related:   []string{"$[0].a"},
			err: `test.yaml:1:3: $[0].a: the edit sets $[0].a, which reaches "a", a key that its mapping ` +
				"repeats",
		},
		"set in an entry whose mapping repeats the key": {
			// Control.
			before:    stringtest.LinesLF("l:", "  m:", "    a: 1", "    a: 2"),
			dup:       true,
			fn:        set("$.l.m.a", 5),
			construct: edit.ConstructRepeatedKey,
			related:   []string{"$.l.m.a"},
			err: `test.yaml:3:5: $.l.m.a: the edit sets $.l.m.a, which reaches "a", a key that its mapping ` +
				"repeats",
		},
		"move of a mapping whose first merge key reads a reference document and whose second is local": {
			// A mapping with more than one merge key binds at its first merge
			// key, whatever each merge key reads (fix19 P56).
			before:    stringtest.LinesLF("x: &x {b: 2}", "m:", "  <<: *rm0", "  <<: *x"),
			ref:       "m0: &rm0 {c: 4}\n",
			dup:       true,
			fn:        move("$.m", "$.n"),
			construct: edit.ConstructMergeForm,
			related:   []string{"$.x"},
			err: "test.yaml:3:3: $.m: the edit moves $.m to $.n, which goes through a mapping with more than one " +
				"merge key",
		},
		"move of a mapping whose first merge key is local and whose second reads a reference document": {
			// Control.
			before:    stringtest.LinesLF("x: &x {b: 2}", "m:", "  <<: *x", "  <<: *rm0"),
			ref:       "m0: &rm0 {c: 4}\n",
			dup:       true,
			fn:        move("$.m", "$.n"),
			construct: edit.ConstructMergeForm,
			related:   []string{"$.x"},
			err: "test.yaml:3:3: $.m: the edit moves $.m to $.n, which goes through a mapping with more than one " +
				"merge key",
		},
		"move of a mapping with two local merge keys": {
			// Control.
			before:    stringtest.LinesLF("x: &x {b: 2}", "m:", "  <<: *x", "  <<: *x"),
			dup:       true,
			fn:        move("$.m", "$.n"),
			construct: edit.ConstructMergeForm,
			related:   []string{"$.x"},
			err: "test.yaml:3:3: $.m: the edit moves $.m to $.n, which goes through a mapping with more than one " +
				"merge key",
		},
		"removal after a move of a mapping that merges an alias of a reference document": {
			// The detail at the merge key names the mapping as the user's text
			// names it, as the error does.
			before: stringtest.LinesLF("m:", "  <<: *rm0", "  b: 1", "n:", "  k: 1"),
			ref:    "m0: &rm0 {x: 2}\n",
			fn: func(ctx context.Context, root *edit.Node) error {
				err := move("$.m", "$.n.m")(ctx, root)
				if err != nil {
					return err
				}

				return remove("$.n.m.b")(ctx, root)
			},
			refAlias: true,
			err: "test.yaml:3:3: $.m.b: the edit removes $.n.m.b, but <<: *rm0 at 2:3 reads a reference " +
				"document, which decides which names $.n.m reads\n|-- 2:3: $.m: <<: *rm0 reads a reference " +
				"document here",
		},
		"removal after a move to the root of a mapping that merges an alias of a reference document": {
			before: stringtest.LinesLF("n:", "  m:", "    <<: *rm0", "    b: 1"),
			ref:    "m0: &rm0 {x: 2}\n",
			fn: func(ctx context.Context, root *edit.Node) error {
				err := move("$.n.m", "$.m")(ctx, root)
				if err != nil {
					return err
				}

				return remove("$.m.b")(ctx, root)
			},
			refAlias: true,
			err: "test.yaml:4:5: $.n.m.b: the edit removes $.m.b, but <<: *rm0 at 3:5 reads a reference " +
				"document, which decides which names $.m reads\n|-- 3:5: $.n.m: <<: *rm0 reads a reference " +
				"document here",
		},
		"set below a scalar that a mapping written in place merges": {
			before:    inline,
			fn:        set("$.m.a.z", 3),
			construct: edit.ConstructMergeForm,
			err: "test.yaml:2:3: $.m: the edit sets $.m.a.z, which goes through a merge key that merges a mapping " +
				"written in place",
		},
		"set below a scalar that a kept merge key brings in": {
			// The kept merge key is no refusal, so the scalar decides. The problem
			// binds at the scalar, under the path of the value that holds it in
			// the user's text (fix19 P56).
			before: stringtest.LinesLF("x: &x {b: 1}", "m:", "  d: 0", "  <<: *x"),
			fn:     set("$.m.b.z", 3),
			err:    "test.yaml:1:11: $.x.b: the edit sets $.m.b.z, but b holds a scalar",
		},
		"set through a merge key whose alias reads a sequence with an alias without an anchor": {
			// The lookup goes through the refused merge key before it reads the
			// alias that does not resolve.
			before:    stringtest.LinesLF("k2: &g [*q]", "meta:", "  x: 1", "  <<: *g"),
			fn:        set("$.meta.version", "v2"),
			construct: edit.ConstructMergeForm,
			related:   []string{"$.k2"},
			err: "test.yaml:4:3: $.meta: the edit sets $.meta.version, which goes through a merge key whose alias " +
				"reads a value other than a mapping",
		},
		"move to a target below a scalar that a merge sequence brings in": {
			before:    stringtest.LinesLF("x: &x {b: 1}", "m:", "  d: 0", "  <<: [*x, {c: 2}]", "e: 5"),
			fn:        move("$.e", "$.m.b.z"),
			construct: edit.ConstructMergeForm,
			related:   []string{"$.x"},
			err: "test.yaml:4:3: $.m: the edit moves $.e to $.m.b.z, which goes through a merge key that merges a " +
				"sequence that holds a mapping",
		},
		"set below an entry of a repeated key": {
			// The path steps through the repeated key a before it meets the
			// scalar.
			before:    repeated,
			dup:       true,
			fn:        set("$.a.x", 1),
			construct: edit.ConstructRepeatedKey,
			related:   []string{"$.a"},
			err:       `test.yaml:1:1: $.a: the edit sets $.a.x, which reaches "a", a key that its mapping repeats`,
		},
		"set of an entry that a mapping written in place merges": {
			before:    inline,
			fn:        set("$.m.a", 5),
			construct: edit.ConstructMergeForm,
			err: stringtest.JoinLF(
				"test.yaml:2:3: $.m: the edit sets $.m.a, which goes through a merge key that merges a mapping "+
					"written in place, and this program does not edit through such a merge key",
				"|-- write the entries of the mapping in place of the merge key",
				"`-- the edit sets $.m.a",
			),
		},
		"set of an own entry of a mapping that merges a mapping written in place": {
			// Control: the Set changes no key of the mapping.
			before: inline,
			fn:     set("$.m.b", 5),
			want:   stringtest.LinesLF("m:", "  <<: {a: 1}", "  b: 5"),
		},
		"set of a new key of a source of a merge sequence under a tag, before which an own key holds the key": {
			// Go-yaml's decoder merges the sequence under the tag, so the Set
			// would make a precedence misread.
			before:    stringtest.LinesLF("s: &s {a: 1}", "m:", "  x: 2", "  <<: !!seq [*s]"),
			fn:        set("$.s.x", 1),
			construct: edit.ConstructMergeForm,
			related:   []string{"$.s"},
			err: stringtest.JoinLF(
				"test.yaml:4:3: $.m: the edit sets $.s.x, which changes the keys of a merge through a merge key "+
					"whose value has the tag !!seq, and this program does not change the keys of such a merge",
				"|-- merge an alias of a mapping of the document",
				"`-- the edit sets $.s.x",
			),
		},
		"rename in a source of a merge sequence under a tag": {
			before:    stringtest.LinesLF("s: &s {a: 1}", "m:", "  x: 2", "  <<: !!seq [*s]"),
			fn:        rename("$.s.a", "x"),
			construct: edit.ConstructMergeForm,
			related:   []string{"$.s"},
			err: stringtest.JoinLF(
				`test.yaml:4:3: $.m: the edit renames $.s.a to "x", which changes the keys of a merge through a `+
					"merge key whose value has the tag !!seq, and this program does not change the keys of such a merge",
				"|-- merge an alias of a mapping of the document",
				"`-- the edit renames $.s.a to \"x\"",
			),
		},
		"set of a key of the later source of a merge sequence under a tag": {
			// The Set would make an order misread.
			before:    stringtest.LinesLF("s: &s {a: 1}", "t: &t {b: 1}", "m:", "  <<: !!seq [*s, *t]"),
			fn:        set("$.t.a", 2),
			construct: edit.ConstructMergeForm,
			related:   []string{"$.s", "$.t"},
			err: stringtest.JoinLF(
				"test.yaml:4:3: $.m: the edit sets $.t.a, which changes the keys of a merge through a merge key "+
					"whose value has the tag !!seq, and this program does not change the keys of such a merge",
				"|-- merge an alias of a mapping of the document",
				"`-- the edit sets $.t.a",
			),
		},
		"set through an alias of a flow sequence of a pair, in a flow mapping with a comment inside it": {
			// The first step of the path meets the comment inside m, before
			// the pair in the source of the alias (P38).
			before:    stringtest.LinesLF("s: &s [a: 1]", "m: {k: *s, # c", "  }"),
			fn:        set("$.m.k[0].a", 2),
			construct: edit.ConstructFlowLayout,
			related:   []string{"$.m"},
			err: stringtest.JoinLF(
				"test.yaml:2:4: $.m: the edit sets $.m.k[0].a, which reaches a flow collection with a comment "+
					"inside it, and this program does not edit inside such a collection",
				"|-- 2:12: $.m: the comment is here",
				"|-- move the comment out of the flow collection",
				"`-- the edit sets $.m.k[0].a",
			),
		},
		"set through the alias of a repeated key, whose source holds an explicit key": {
			// The second step meets the repeated key r, before the explicit
			// key of the source (P38).
			before:    stringtest.LinesLF("s: &s", "  ? x", "  : 1", "m:", "  r: 1", "  r: *s"),
			fn:        set("$.m.r.x", 2),
			dup:       true,
			construct: edit.ConstructRepeatedKey,
			related:   []string{"$.m.r"},
			err: stringtest.JoinLF(
				`test.yaml:5:3: $.m.r: the edit sets $.m.r.x, which reaches "r", a key that its mapping repeats, `+
					"and this program does not edit the entries of a repeated key",
				`|-- 6:3: $.m.r: the key "r" is written again here`,
				"|-- remove all but one of the entries of the key",
				"`-- the edit sets $.m.r.x",
			),
		},
		"set through the alias of an explicit key, whose source is a flow mapping with a comment inside it": {
			before:    stringtest.LinesLF("s: &s {x: 1, # c", "  }", "m:", "  ? k", "  : *s"),
			fn:        set("$.m.k.x", 2),
			construct: edit.ConstructExplicitKey,
			err: stringtest.JoinLF(
				"test.yaml:4:3: $.m.k: the edit sets $.m.k.x, which reaches an entry with an explicit key, and "+
					"this program does not edit such an entry other than by removing it",
				`|-- write the key without the "?"`,
				"`-- the edit sets $.m.k.x",
			),
		},
		"set of a new key of a source of a merge sequence": {
			before:    seq,
			fn:        set("$.s.q", 5),
			construct: edit.ConstructMergeForm,
			related:   []string{"$.s", "$.t"},
			err: stringtest.JoinLF(
				"test.yaml:4:3: $.m: the edit sets $.s.q, which changes the keys of a merge through a merge key that merges a sequence of aliases, and this program does not change the keys of such a merge",
				"|-- merge one alias",
				"`-- the edit sets $.s.q",
			),
		},
		"set through a merge sequence of mappings written in place": {
			// No path reaches the mappings of the sequence, so the user writes
			// their entries in place of the merge key.
			before:    stringtest.LinesLF("m:", "  <<: [{w: 4}]"),
			fn:        set("$.m.w", 2),
			construct: edit.ConstructMergeForm,
			err: stringtest.JoinLF(
				"test.yaml:2:3: $.m: the edit sets $.m.w, which goes through a merge key that merges a sequence "+
					"that holds a mapping, and this program does not edit through such a merge key",
				"|-- write the entries of the mappings in place of the merge key",
				"`-- the edit sets $.m.w",
			),
		},
		"removal through a merge key whose alias reads a sequence, in a mapping with an own key": {
			// The part of the merge key holds the keys of each mapping of the
			// sequence, so the removal changes the keys of the merge.
			before:    stringtest.LinesLF("l: &l [{a: 1}, {b: 2}]", "m:", "  <<: *l", "  c: 3"),
			fn:        remove("$.m.a"),
			construct: edit.ConstructMergeForm,
			related:   []string{"$.l"},
			err: "test.yaml:3:3: $.m: the edit removes $.m.a, which changes the keys of a merge through a merge " +
				"key whose alias reads a value other than a mapping, and this program does not change the keys of " +
				"such a merge",
		},
		"new key in a mapping of a sequence that an alias of a merge key reads": {
			// Go-yaml's decoder would read the new key from the merge key in
			// place of the own key c, so the call changes the keys of the
			// merge.
			before:    stringtest.LinesLF("l: &l", "  - a: 1", "m:", "  c: 3", "  <<: *l"),
			fn:        set("$.l[0].c", 9),
			construct: edit.ConstructMergeForm,
			related:   []string{"$.l"},
			err: "test.yaml:5:3: $.m: the edit sets $.l[0].c, which changes the keys of a merge through a merge " +
				"key whose alias reads a value other than a mapping, and this program does not change the keys of " +
				"such a merge",
		},
		"set through a merge sequence": {
			// The walk takes a merge step out of m, so the Set goes through
			// the merge key, and it can set the key in a source instead.
			before:    seq,
			fn:        set("$.m.a", 5),
			construct: edit.ConstructMergeForm,
			related:   []string{"$.s", "$.t"},
			err: stringtest.JoinLF(
				"test.yaml:4:3: $.m: the edit sets $.m.a, which goes through a merge key that merges a sequence "+
					"of aliases, and this program does not edit through such a merge key",
				"|-- merge one alias, or set the key in a source",
				"`-- the edit sets $.m.a",
			),
		},
		"set of an entry of a source of a merge sequence": {
			// Control: the Set changes no key of the source.
			before: seq,
			fn:     set("$.s.a", 5),
			want:   stringtest.LinesLF("s: &s {a: 5}", "t: &t {c: 1}", "m:", "  <<: [*s, *t]", "  b: 2"),
		},
		"rename in a mapping that merges an anchored value": {
			before:    stringtest.LinesLF("m:", "  <<: &s {a: 1}", "  b: 2", "n: *s"),
			fn:        rename("$.m.b", "c"),
			construct: edit.ConstructMergeForm,
			err: stringtest.JoinLF(
				`test.yaml:2:3: $.m: the edit renames $.m.b to "c", which changes the keys of a merge through a merge key whose value has an anchor, and this program does not change the keys of such a merge`,
				"|-- write the mapping apart with its anchor, and merge an alias of it",
				"`-- the edit renames $.m.b to \"c\"",
			),
		},
		"removal in a mapping with a merge key without a value": {
			before:    stringtest.LinesLF("m:", "  <<:", "  b: 2"),
			fn:        remove("$.m.b"),
			construct: edit.ConstructMergeForm,
			err: stringtest.JoinLF(
				"test.yaml:2:3: $.m: the edit removes $.m.b, which changes the keys of a merge through a merge key without a value, and this program does not change the keys of such a merge",
				"|-- merge an alias of a mapping, or remove the merge key",
				"`-- the edit removes $.m.b",
			),
		},
		"move out of a mapping that merges a mapping with a merge key": {
			before:    stringtest.LinesLF("r: &r {z: 1}", "s: &s {<<: *r, a: 1}", "m:", "  <<: *s", "  b: 2"),
			fn:        move("$.m.b", "$.q"),
			construct: edit.ConstructMergeForm,
			related:   []string{"$.s"},
			err: stringtest.JoinLF(
				"test.yaml:4:3: $.m: the edit moves $.m.b to $.q, which changes the keys of a merge through a merge key that merges a mapping with a merge key of its own, and this program does not change the keys of such a merge",
				"|-- merge a mapping without a merge key",
				"`-- the edit moves $.m.b to $.q",
			),
		},
		"rename in a mapping with a tagged merge key": {
			before:    stringtest.LinesLF("s: &s {a: 1}", "m:", "  !!merge <<: *s", "  b: 2"),
			fn:        rename("$.m.b", "c"),
			construct: edit.ConstructMergeForm,
			related:   []string{"$.s"},
			err: stringtest.JoinLF(
				`test.yaml:3:3: $.m: the edit renames $.m.b to "c", which changes the keys of a merge through a merge key with the tag !!merge, and this program does not change the keys of such a merge`,
				"|-- remove the tag !!merge",
				"`-- the edit renames $.m.b to \"c\"",
			),
		},
		"rename in a mapping with a verbatim merge tag": {
			before:    stringtest.LinesLF("s: &s {a: 1}", "m:", "  !<tag:yaml.org,2002:merge> <<: *s", "  b: 2"),
			fn:        rename("$.m.b", "c"),
			construct: edit.ConstructMergeForm,
			related:   []string{"$.s"},
			err: stringtest.JoinLF(
				`test.yaml:3:3: $.m: the edit renames $.m.b to "c", which changes the keys of a merge through a merge key with the tag !<tag:yaml.org,2002:merge>, and this program does not change the keys of such a merge`,
				"|-- remove the tag !<tag:yaml.org,2002:merge>",
				"`-- the edit renames $.m.b to \"c\"",
			),
		},
		"set through a mapping with two merge keys": {
			before:    stringtest.LinesLF("s: &s {a: 1}", "t: &t {c: 1}", "m:", "  <<: *s", "  <<: *t", "  b: 2"),
			dup:       true,
			fn:        set("$.m.c", 5),
			construct: edit.ConstructMergeForm,
			related:   []string{"$.s"},
			err: stringtest.JoinLF(
				"test.yaml:4:3: $.m: the edit sets $.m.c, which goes through a mapping with more than one merge "+
					"key, and this program does not edit through such a merge key",
				"|-- merge one alias",
				"`-- the edit sets $.m.c",
			),
		},
		"rename in a mapping that merges an alias of a reference document": {
			// The merge key is no refused form, but the reference document
			// decides which names the mapping reads, so the rename fails with
			// an error that matches paths.ErrReferenceAlias.
			before:   stringtest.LinesLF("m:", "  <<: *base", "  b: 2"),
			ref:      "base: &base {a: 1}\n",
			fn:       rename("$.m.b", "c"),
			refAlias: true,
			err: `test.yaml:3:3: $.m.b: the edit renames $.m.b to "c", but <<: *base at 2:3 reads a reference ` +
				`document, which decides which names $.m reads`,
		},
		"rename in a mapping that merges one alias": {
			// Control.
			before: stringtest.LinesLF("s: &s {a: 1}", "m:", "  <<: *s", "  b: 2"),
			fn:     rename("$.m.b", "c"),
			want:   stringtest.LinesLF("s: &s {a: 1}", "m:", "  <<: *s", "  c: 2"),
		},
		"set of a repeated key": {
			before:    repeated,
			dup:       true,
			fn:        set("$.a", 5),
			construct: edit.ConstructRepeatedKey,
			related:   []string{"$.a"},
			err: stringtest.JoinLF(
				`test.yaml:1:1: $.a: the edit sets $.a, which reaches "a", a key that its mapping repeats, and `+
					"this program does not edit the entries of a repeated key",
				`|-- 2:1: $.a: the key "a" is written again here`,
				"|-- remove all but one of the entries of the key",
				"`-- the edit sets $.a",
			),
		},
		"removal of a repeated key": {
			before:    repeated,
			dup:       true,
			fn:        remove("$.a"),
			construct: edit.ConstructRepeatedKey,
			related:   []string{"$.a"},
			err: `test.yaml:1:1: $.a: the edit removes $.a, which reaches "a", a key that its mapping repeats, and ` +
				"this program does not edit the entries of a repeated key",
		},
		"set of another key of a mapping that repeats a key": {
			// Control.
			before: repeated,
			dup:    true,
			fn:     set("$.b", 5),
			want:   stringtest.LinesLF("a: 1", "a: 2", "b: 5"),
		},
		"read of a repeated key": {
			// Control: a read records nothing.
			before: repeated,
			dup:    true,
			fn:     read("$.a", set("$.b", 5)),
			want:   stringtest.LinesLF("a: 1", "a: 2", "b: 5"),
		},
		"rename to a key of the mapping where the Source allows duplicate keys": {
			before:    stringtest.LinesLF("a: 1", "b: 2"),
			dup:       true,
			fn:        rename("$.b", "a"),
			construct: edit.ConstructRepeatedKey,
			related:   []string{"$.a"},
			inValue:   true,
			err: stringtest.JoinLF(
				`test.yaml:2:1: $.b: the edit renames $.b to "a", which writes the key "a" a second time in its `+
					"mapping, and this program does not write a key that its mapping repeats",
				`|-- 1:1: $.a: the key "a" is written here`,
				"|-- rename the key to one that its mapping does not hold",
				"`-- the edit renames $.b to \"a\"",
			),
		},
		"rename to a key of the mapping": {
			// Control: a Source that refuses duplicate keys gives the
			// ordinary error.
			before: stringtest.LinesLF("a: 1", "b: 2"),
			fn:     rename("$.b", "a"),
			err:    `test.yaml:2:1: $.b~: the edit renames "b" to "a", which already exists`,
		},
		"set of an entry with an explicit key": {
			before:    explicit,
			fn:        set("$.spec.b", 5),
			construct: edit.ConstructExplicitKey,
			err: stringtest.JoinLF(
				"test.yaml:2:3: $.spec.b: the edit sets $.spec.b, which reaches an entry with an explicit key, "+
					"and this program does not edit such an entry other than by removing it",
				`|-- write the key without the "?"`,
				"`-- the edit sets $.spec.b",
			),
		},
		"rename of an entry with an explicit key": {
			before:    explicit,
			fn:        rename("$.spec.b", "n"),
			construct: edit.ConstructExplicitKey,
			err: `test.yaml:2:3: $.spec.b: the edit renames $.spec.b to "n", which reaches an entry with an ` +
				"explicit key, and this program does not edit such an entry other than by removing it",
		},
		"removal of an entry with an explicit key": {
			// Control.
			before: explicit,
			fn:     remove("$.spec.b"),
			want:   stringtest.LinesLF("spec: {}", "c: 1"),
		},
		"read of an entry with an explicit key": {
			// Control: a read records nothing.
			before: explicit,
			fn:     read("$.spec.b", set("$.c", 5)),
			want:   stringtest.LinesLF("spec:", "  ? b", "  : x", "c: 5"),
		},
		"set inside a flow sequence with a comment": {
			before:    comment,
			fn:        set("$.f[0]", 5),
			construct: edit.ConstructFlowLayout,
			related:   []string{"$.f"},
			err: stringtest.JoinLF(
				"test.yaml:1:4: $.f: the edit sets $.f[0], which reaches a flow collection with a comment inside "+
					"it, and this program does not edit inside such a collection",
				"|-- 1:8: $.f: the comment is here",
				"|-- move the comment out of the flow collection",
				"`-- the edit sets $.f[0]",
			),
		},
		"set of a flow sequence with a comment": {
			// Control: the Set takes the collection whole.
			before: comment,
			fn:     set("$.f", 5),
			want:   stringtest.LinesLF("f: 5", "z: 1"),
		},
		"set past a flow sequence with a comment": {
			// Control.
			before: comment,
			fn:     set("$.z", 5),
			want:   stringtest.LinesLF("f: [1, # c", "  2]", "z: 5"),
		},
		"set inside a flow sequence before one with a comment": {
			// Control: the comment after the "]" of $.g lies outside it.
			before: stringtest.LinesLF("g: [1] # c", "f: [1, # c", "  2]"),
			fn:     set("$.g[0]", 5),
			want:   stringtest.LinesLF("g: [5] # c", "f: [1, # c", "  2]"),
		},
		"set inside the second flow sequence of two with comments": {
			before:    stringtest.LinesLF("g: [1] # c", "f: [1, # c", "  2]"),
			fn:        set("$.f[1]", 5),
			construct: edit.ConstructFlowLayout,
			related:   []string{"$.f"},
			err: stringtest.JoinLF(
				"test.yaml:2:4: $.f: the edit sets $.f[1], which reaches a flow collection with a comment inside "+
					"it, and this program does not edit inside such a collection",
				"|-- 2:8: $.f: the comment is here",
				"|-- move the comment out of the flow collection",
				"`-- the edit sets $.f[1]",
			),
		},
		"set inside a flow sequence with a # right after a comma": {
			// Control: YAML opens a comment only after white space.
			before: stringtest.LinesLF("f: [1,# c", "  2]", "z: 1"),
			fn:     set("$.f[0]", 5),
			want:   stringtest.LinesLF("f: [5,# c", "  2]", "z: 1"),
		},
		"rename inside a pair without braces in a flow sequence": {
			before:    bare,
			fn:        rename("$.f[1].a", "b"),
			construct: edit.ConstructFlowLayout,
			err: stringtest.JoinLF(
				`test.yaml:1:8: $.f[1].a: the edit renames $.f[1].a to "b", which reaches a pair without braces `+
					"in a flow sequence, and this program does not edit inside such a pair",
				"|-- write braces around the pair, as in [{a: 1}]",
				"`-- the edit renames $.f[1].a to \"b\"",
			),
		},
		"move of a flow sequence with a pair without braces": {
			// A Move of a value that holds the pair reaches it.
			before:    bare,
			fn:        move("$.f", "$.h"),
			construct: edit.ConstructFlowLayout,
			err: "test.yaml:1:8: $.f[1].a: the edit moves $.f to $.h, which reaches a pair without braces in a " +
				"flow sequence, and this program does not edit inside such a pair",
		},
		"removal of a pair without braces in a flow sequence": {
			// Control: the Remove takes the pair whole.
			before: bare,
			fn:     remove("$.f[1]"),
			want:   stringtest.LinesLF("f: [0]", "g: [5]"),
		},
		"move of a block mapping into a flow sequence": {
			before:    stringtest.LinesLF("a:", "  x: 1", "f: [1]"),
			fn:        move("$.a", "$.f[1]"),
			construct: edit.ConstructFlowLayout,
			err: stringtest.JoinLF(
				"test.yaml:2:3: $.a: the edit moves $.a to $.f[1], which puts a block value inside a flow "+
					"collection, and this program does not move a block value into a flow collection",
				"|-- write the value in flow style first, or move it into a block collection",
				"`-- the edit moves $.a to $.f[1]",
			),
		},
		"move of a block scalar into a flow mapping": {
			before:    stringtest.LinesLF("a: |", "  x", "f: {k: 1}"),
			fn:        move("$.a", "$.f.a"),
			construct: edit.ConstructFlowLayout,
			err: "test.yaml:1:4: $.a: the edit moves $.a to $.f.a, which puts a block value inside a flow " +
				"collection, and this program does not move a block value into a flow collection",
		},
		"move of a flow mapping into a flow sequence": {
			// Control.
			before: stringtest.LinesLF("a: {x: 1}", "f: [1]"),
			fn:     move("$.a", "$.f[1]"),
			want:   stringtest.LinesLF("f: [1, {x: 1}]"),
		},
		"move into a flow sequence of a mapping that the Apply set": {
			// Control: the engine writes the value, so the Move drops nothing
			// of the user's text.
			before: stringtest.LinesLF("a: 1", "f: [1]"),
			fn: func(ctx context.Context, root *edit.Node) error {
				err := set("$.a", map[string]any{"x": []int{1, 2}})(ctx, root)
				if err != nil {
					return err
				}

				return move("$.a", "$.f[1]")(ctx, root)
			},
			want: stringtest.LinesLF("f: [1, {x: [1, 2]}]"),
		},
		"set of a new key after a block scalar that ends a text without a line break": {
			before:    stringtest.JoinLF("a: |", "  x"),
			fn:        set("$.b", 1),
			construct: edit.ConstructBlockScalar,
			err: stringtest.JoinLF(
				"test.yaml:1:4: $.a: the edit adds lines after this block scalar, which ends the text without a "+
					"line break, so it changes the string of the scalar, when it sets $.b",
				"|-- end the scalar with a line break, which adds one to its string, or edit another place",
				"`-- the edit sets $.b",
			),
		},
		"set of a new key after a block scalar with keep chomping that ends a text without a line break": {
			before:    stringtest.JoinLF("a: |+", "  x"),
			fn:        set("$.b", 1),
			construct: edit.ConstructBlockScalar,
			err: "test.yaml:1:4: $.a: the edit adds lines after this block scalar, which ends the text without a " +
				"line break, so it changes the string of the scalar, when it sets $.b",
		},
		"set of a new key after a block scalar with strip chomping that ends a text without a line break": {
			// Control: the string of the scalar does not hold its last line
			// break.
			before: stringtest.JoinLF("a: |-", "  x"),
			fn:     set("$.b", 1),
			want:   stringtest.JoinLF("a: |-", "  x", "b: 1"),
		},
		"move of a block scalar that ends a text without a line break, into a mapping": {
			before:    stringtest.JoinLF("m:", "  k: 1", "n: 2", "c: |", "  p"),
			fn:        move("$.c", "$.m.c"),
			construct: edit.ConstructBlockScalar,
			err: "test.yaml:4:4: $.c: the edit adds lines after this block scalar, which ends the text without a " +
				"line break, so it changes the string of the scalar, when it moves $.c to $.m.c",
		},
		"removal of the last key after a block scalar, in a text without a line break": {
			// Control: the removal takes the line break before the key, which
			// the scalar takes back.
			before: stringtest.JoinLF("a: |", "  x", "b: 1"),
			fn:     remove("$.b"),
			want:   stringtest.LinesLF("a: |", "  x"),
		},
		"removal of an entry with a comment line above it after a move of its mapping": {
			// The removal takes the comment line with the entry, as without
			// the move.
			before: stringtest.LinesLF("p:", "  d: x", "  # c", "  b: 1"),
			fn: func(ctx context.Context, root *edit.Node) error {
				err := move("$.p", "$.z")(ctx, root)
				if err != nil {
					return err
				}

				return remove("$.z.b")(ctx, root)
			},
			want: stringtest.LinesLF("z:", "  d: x"),
		},
		"removal of an entry with a comment line above it": {
			// Control.
			before: stringtest.LinesLF("p:", "  d: x", "  # c", "  b: 1"),
			fn:     remove("$.p.b"),
			want:   stringtest.LinesLF("p:", "  d: x"),
		},
		"move of an alias into the value of the anchor that it reads": {
			before:    stringtest.LinesLF("a: &x", "  k: 1", "b: *x"),
			fn:        move("$.b", "$.a.z"),
			construct: edit.ConstructRecursiveAlias,
			related:   []string{"$.b"},
			err: stringtest.JoinLF(
				"test.yaml:1:4: $.a: the edit moves $.b to $.a.z, which makes a document with an alias inside the "+
					"value of the anchor that it reads, such as *x, and this program does not edit such a document",
				"|-- 3:4: $.b: *x is here",
				"|-- remove *x from the value of &x",
				"`-- the edit moves $.b to $.a.z",
			),
		},
		"move of a mapping that merges an anchored mapping into that mapping": {
			before:    stringtest.LinesLF("s: &s", "  a: 1", "m:", "  <<: *s"),
			fn:        move("$.m", "$.s.m"),
			construct: edit.ConstructRecursiveAlias,
			related:   []string{"$.m"},
			err: "test.yaml:1:4: $.s: the edit moves $.m to $.s.m, which makes a document with an alias inside " +
				"the value of the anchor that it reads, such as *s, and this program does not edit such a document",
		},
		"move of an alias into an anchored value that it does not read": {
			// Control.
			before: stringtest.LinesLF("a: &x", "  k: 1", "c: &y {}", "b: *x"),
			fn:     move("$.b", "$.c.z"),
			want:   stringtest.LinesLF("a: &x", "  k: 1", "c: &y {z: *x}"),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := []niceyaml.SourceOption{niceyaml.WithName("test.yaml"), niceyaml.WithAllowDuplicateKeys(tc.dup)}
			if tc.ref != "" {
				opts = append(opts, niceyaml.WithReferences(niceyaml.NewSourceFromString(tc.ref)))
			}

			d, err := edit.New(niceyaml.NewSourceFromString(tc.before, opts...).Documents()[0])
			require.NoError(t, err)

			err = d.Apply(t.Context(), paths.Doc(), tc.fn)
			if tc.err == "" {
				require.NoError(t, err)
				assert.Equal(t, tc.want, d.Text())

				return
			}

			assert.Contains(t, niceyaml.FormatError(err), tc.err)
			assert.Equal(t, tc.before, d.Text())
			require.NotErrorIs(t, err, edit.ErrEngine)

			assert.Equal(t, tc.refAlias, errors.Is(err, paths.ErrReferenceAlias))

			if tc.construct == 0 {
				require.NotErrorIs(t, err, edit.ErrUnsupported)

				return
			}

			require.ErrorIs(t, err, edit.ErrUnsupported)

			var unsupported *edit.UnsupportedError

			require.ErrorAs(t, err, &unsupported)

			related := make([]string, 0, len(unsupported.Related))
			for _, p := range unsupported.Related {
				related = append(related, p.String())
			}

			assert.Equal(t, tc.construct, unsupported.Construct)
			assert.Equal(t, tc.inValue, unsupported.InValue)
			assert.Equal(t, append([]string{}, tc.related...), related)
			assert.Equal(t, !tc.inValue, niceyaml.IsInvalid(err))
		})
	}
}

func TestDoc_Apply_Merge(t *testing.T) {
	t.Parallel()

	set := func(path string, v any) edit.Func {
		return func(ctx context.Context, root *edit.Node) error {
			return root.At(paths.MustParse(path)).Set(ctx, v)
		}
	}

	rename := func(path, key string) edit.Func {
		return func(_ context.Context, root *edit.Node) error {
			return root.At(paths.MustParse(path)).SetKey(key)
		}
	}

	remove := func(path string) edit.Func {
		return func(_ context.Context, root *edit.Node) error {
			return root.At(paths.MustParse(path)).Remove()
		}
	}

	move := func(path, target string) edit.Func {
		return func(_ context.Context, root *edit.Node) error {
			return root.At(paths.MustParse(path)).Move(paths.MustParse(target))
		}
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

	// The Func that decode returns decodes the value at path, and returns
	// the problem of the read.
	decode := func(path string) edit.Func {
		return func(ctx context.Context, root *edit.Node) error {
			_, err := root.At(paths.MustParse(path)).Decode[any](ctx)

			return err
		}
	}

	var (
		after   = stringtest.LinesLF("s: &s", "  a: 1", "m:", "  <<: *s", "  b: 2")
		hides   = stringtest.LinesLF("s: &s", "  a: 1", "m:", "  <<: *s", "  a: 2")
		flow    = stringtest.LinesLF("s: &s {a: 1}", "m: {<<: *s, a: 2}")
		base    = stringtest.LinesLF(".base: &base", "  image: x", "job:", "  <<: *base", "  image: y")
		airflow = stringtest.LinesLF(
			"x-airflow-common: &airflow-common",
			"  image: apache/airflow:2",
			"services:",
			"  web:",
			"    <<: *airflow-common",
			"    command: webserver",
			"  sched:",
			"    <<: *airflow-common",
			"    command: scheduler",
		)
	)

	tcs := map[string]struct {
		fn     edit.Func
		before string
		// The path of the Apply, or $ when empty.
		apply string
		// The text of the Doc after an Apply that passes.
		want string
		// The lines of the error of the Apply before its excerpt, or "" for
		// an Apply that passes.
		err string
		// The paths of the other sites of a refused construct.
		related   []string
		construct edit.Construct
		// Whether the error matches edit.ErrEngine.
		engine bool
		// Whether the construct lies in the key that the program passed.
		inValue bool
	}{
		"read of an own entry after a merge list whose sources hold its key": {
			// Control: both readings take the own entry.
			before: stringtest.LinesLF("a: &a {y: 1}", "b: &b {y: 2}", "c:", "  <<: [*a, *b]", "  y: 3"),
			fn:     decode("$.c.y"),
			want:   stringtest.LinesLF("a: &a {y: 1}", "b: &b {y: 2}", "c:", "  <<: [*a, *b]", "  y: 3"),
		},
		"read of a key that one source of a merge list holds": {
			// Control.
			before: stringtest.LinesLF("a: &a {y: 1}", "b: &b {z: 2}", "c:", "  <<: [*a, *b]"),
			fn:     both(decode("$.c.y"), decode("$.c.z")),
			want:   stringtest.LinesLF("a: &a {y: 1}", "b: &b {z: 2}", "c:", "  <<: [*a, *b]"),
		},
		"read of a key of a merge list that names one source twice": {
			// Control: both readings take the entry of a.
			before: stringtest.LinesLF("a: &a {y: 1}", "b: &b {y: 2}", "c:", "  <<: [*a, *b, *a]"),
			fn:     decode("$.c.y"),
			want:   stringtest.LinesLF("a: &a {y: 1}", "b: &b {y: 2}", "c:", "  <<: [*a, *b, *a]"),
		},
		"set of an entry that a merge key brings in": {
			// Control: the Set writes the entry of the source.
			before: after,
			fn:     set("$.m.a", 5),
			want:   stringtest.LinesLF("s: &s", "  a: 5", "m:", "  <<: *s", "  b: 2"),
		},
		"set of an entry that a merge key brings in, in flow style": {
			// Control.
			before: stringtest.LinesLF("s: &s {a: 1}", "m: {<<: *s, b: 2}"),
			fn:     set("$.m.a", 5),
			want:   stringtest.LinesLF("s: &s {a: 5}", "m: {<<: *s, b: 2}"),
		},
		"removal of an own entry whose key the source holds": {
			before:    hides,
			fn:        remove("$.m.a"),
			construct: edit.ConstructMergeKeys,
			related:   []string{"$.m"},
			err: stringtest.JoinLF(
				"test.yaml:5:3: $.m.a: the edit removes $.m.a, but <<: *s at 4:3 also brings in a, so $.m still reads a",
				"|-- 4:3: $.m: <<: *s brings in a here",
				"|-- set $.m.a to the value it needs, or remove a from $.s too",
				"`-- the edit removes $.m.a",
			),
		},
		"removal of an own entry whose key the source holds, in flow style": {
			before:    flow,
			fn:        remove("$.m.a"),
			construct: edit.ConstructMergeKeys,
			related:   []string{"$.m"},
			err:       "test.yaml:2:13: $.m.a: the edit removes $.m.a, but <<: *s at 2:5 also brings in a, so $.m still reads a",
		},
		"removal of an own entry whose key the source does not hold": {
			// Control.
			before: after,
			fn:     remove("$.m.b"),
			want:   stringtest.LinesLF("s: &s", "  a: 1", "m:", "  <<: *s"),
		},
		"rename to a key that the source holds": {
			before:    after,
			fn:        rename("$.m.b", "a"),
			construct: edit.ConstructMergeKeys,
			related:   []string{"$.m"},
			err: stringtest.JoinLF(
				`test.yaml:5:3: $.m.b: the edit renames $.m.b to "a", but <<: *s at 4:3 also brings in a, so $.m `+
					"then hides the a of $.s",
				"|-- 4:3: $.m: <<: *s brings in a here",
				"|-- write a key that $.s does not hold, or remove a from $.s first",
				"`-- the edit renames $.m.b to \"a\"",
			),
		},
		"rename of an own entry whose key the source holds": {
			before:    hides,
			fn:        rename("$.m.a", "z"),
			construct: edit.ConstructMergeKeys,
			related:   []string{"$.m"},
			err: `test.yaml:5:3: $.m.a: the edit renames $.m.a to "z", but <<: *s at 4:3 also brings in a, so $.m ` +
				"still reads a",
		},
		"rename to a key that the source does not hold": {
			// Control.
			before: after,
			fn:     rename("$.m.b", "c"),
			want:   stringtest.LinesLF("s: &s", "  a: 1", "m:", "  <<: *s", "  c: 2"),
		},
		"move out of an own entry whose key the source holds": {
			before:    hides,
			fn:        move("$.m.a", "$.z"),
			construct: edit.ConstructMergeKeys,
			related:   []string{"$.m"},
			err:       "test.yaml:5:3: $.m.a: the edit moves $.m.a to $.z, but <<: *s at 4:3 also brings in a, so $.m still reads a",
		},
		"move out of an own entry whose key the source does not hold": {
			// Control.
			before: after,
			fn:     move("$.m.b", "$.z"),
			want:   stringtest.LinesLF("s: &s", "  a: 1", "m:", "  <<: *s", "z: 2"),
		},
		"move into an entry that a merge key brings in": {
			// The target holds the entry of the source, so the Move fails
			// before it finds the target taken. The problem binds at the
			// moved entry, as the problem of a removal binds at the entry.
			before:    stringtest.LinesLF("s: &s", "  a: 1", "m:", "  <<: *s", "x:", "  a: 5"),
			fn:        move("$.x.a", "$.m.a"),
			construct: edit.ConstructMergeKeys,
			related:   []string{"$.m"},
			err: stringtest.JoinLF(
				"test.yaml:6:3: $.x.a: the edit moves $.x.a to $.m.a, but <<: *s at 4:3 also brings in a, so $.m "+
					"then hides the a of $.s",
				"|-- 4:3: $.m: <<: *s brings in a here",
			),
		},
		"rename in a mapping after renames of its source and of a key there": {
			// The words after the name of the call name the source and the key
			// in the current text, as the call names its place.
			before: stringtest.LinesLF("S: &s", "  Image: x", "m:", "  <<: *s", "  Image: y"),
			fn: both(
				both(rename("$.S", "s"), rename("$.s.Image", "image")),
				rename("$.m.Image", "image"),
			),
			construct: edit.ConstructMergeKeys,
			related:   []string{"$.m"},
			err: stringtest.JoinLF(
				`test.yaml:5:3: $.m.Image: the edit renames $.m.Image to "image", but <<: *s at 4:3 also brings `+
					"in image, so $.m then hides the image of $.s",
				"|-- 4:3: $.m: <<: *s brings in image here",
				"|-- write a key that $.s does not hold, or remove image from $.s first",
				"`-- the edit renames $.m.Image to \"image\"",
			),
		},
		"move of an own entry into the source of its merge key": {
			// The mapping reads the entry of the source after the Move. The
			// problem binds at the entry that the Move took, as a Remove of
			// it binds, and its detail marks the merge key.
			before:    stringtest.LinesLF("s: &s", "  b: 1", "m:", "  <<: *s", "  a: 5"),
			fn:        move("$.m.a", "$.s.a"),
			construct: edit.ConstructMergeKeys,
			related:   []string{"$.m"},
			err: stringtest.JoinLF(
				"test.yaml:5:3: $.m.a: the edit moves $.m.a to $.s.a, but <<: *s at 4:3 also brings in a, so $.m "+
					"still reads a",
				"|-- 4:3: $.m: <<: *s brings in a here",
				"|-- set $.m.a to the value it needs, or remove a from $.s too",
				"`-- the edit moves $.m.a to $.s.a",
			),
		},
		"move of an own entry of a flow mapping into the source of its merge key": {
			before:    stringtest.LinesLF("s: &s {b: 1}", "m: {a: 2, <<: *s}"),
			fn:        move("$.m.a", "$.s.a"),
			construct: edit.ConstructMergeKeys,
			related:   []string{"$.m"},
			err: stringtest.JoinLF(
				"test.yaml:2:5: $.m.a: the edit moves $.m.a to $.s.a, but <<: *s at 2:11 also brings in a, so $.m "+
					"still reads a",
				"|-- 2:11: $.m: <<: *s brings in a here",
				"|-- set $.m.a to the value it needs, or remove a from $.s too",
				"`-- the edit moves $.m.a to $.s.a",
			),
		},
		"rename of each image of a normalizer, the source first": {
			// The rename of the source passes, and the rename of the mapping
			// then hides the renamed key.
			before:    base,
			fn:        both(rename("$.'.base'.image", "IMAGE"), rename("$.job.image", "IMAGE")),
			construct: edit.ConstructMergeKeys,
			related:   []string{"$.job"},
			err: `test.yaml:5:3: $.job.image: the edit renames $.job.image to "IMAGE", but <<: *base at 4:3 also ` +
				"brings in IMAGE, so $.job then hides the IMAGE of $.'.base'",
		},
		"rename of each image of a normalizer, the mapping first": {
			before:    base,
			fn:        both(rename("$.job.image", "IMAGE"), rename("$.'.base'.image", "IMAGE")),
			construct: edit.ConstructMergeKeys,
			related:   []string{"$.job"},
			err: `test.yaml:5:3: $.job.image: the edit renames $.job.image to "IMAGE", but <<: *base at 4:3 also ` +
				"brings in image, so $.job still reads image",
		},
		"rename to a merge key": {
			// Niceyaml's encoder quotes the key, so go-yaml reads it as the
			// string "<<".
			before: stringtest.LinesLF("m:", "  b: 2"),
			fn:     rename("$.m.b", "<<"),
			want:   stringtest.LinesLF("m:", `  "<<": 2`),
		},
		"rename of a quoted key to a merge key": {
			// Control: the key keeps its quotes, so it reads as a string.
			before: stringtest.LinesLF("m:", `  "b": 2`),
			fn:     rename("$.m.b", "<<"),
			want:   stringtest.LinesLF("m:", `  "<<": 2`),
		},
		"rename to a merge key beside a merge key": {
			// Go-yaml's parser compares the keys by their text, so it refuses
			// the quoted key beside the merge key. The problem names the call,
			// and its detail asks for another key.
			before: stringtest.LinesLF("s: &s {a: 1}", "m:", "  <<: *s", "  b: 2"),
			fn:     rename("$.m.b", "<<"),
			engine: true,
			err: stringtest.JoinLF(
				`test.yaml:4:3: $.m.b: the edit renames $.m.b to "<<", and go-yaml's parser refuses the edited text: `+
					`mapping key "<<" already defined at [3:3]`,
				`|-- write a key other than "<<"`,
				"`-- the edit renames $.m.b to \"<<\"",
			),
		},
		"set of a new merge key": {
			before: stringtest.LinesLF("m:", "  b: 2"),
			fn:     set("$.m.<<", map[string]any{"a": 1}),
			want:   stringtest.LinesLF("m:", "  b: 2", `  "<<":`, "    a: 1"),
		},
		"move to a new merge key": {
			before: stringtest.LinesLF("m:", "  b: 2", "x: {a: 3}"),
			fn:     move("$.x", "$.m.<<"),
			want:   stringtest.LinesLF("m:", "  b: 2", `  "<<": {a: 3}`),
		},
		"removal of a mapping whose merge key merges a mapping with a merge key of its own": {
			// The Remove takes the mapping whole, so it does not reach the merge
			// key in its source.
			before: stringtest.LinesLF("s: &s", "  c: 1", "  <<: {}", "r1:", "  <<: *s", "b: 2"),
			fn:     remove("$.r1"),
			want:   stringtest.LinesLF("s: &s", "  c: 1", "  <<: {}", "b: 2"),
		},
		"set of a mapping whose merge key merges a mapping with a merge key of its own": {
			before: stringtest.LinesLF("s: &s", "  c: 1", "  <<: {}", "r1:", "  <<: *s", "b: 2"),
			fn:     set("$.r1", 1),
			want:   stringtest.LinesLF("s: &s", "  c: 1", "  <<: {}", "r1: 1", "b: 2"),
		},
		"removal inside a mapping whose merge key merges a mapping with a merge key of its own": {
			// Control.
			before:    stringtest.LinesLF("s: &s", "  c: 1", "  <<: {}", "r1:", "  <<: *s", "  d: 1", "b: 2"),
			fn:        remove("$.r1.d"),
			construct: edit.ConstructMergeForm,
			related:   []string{"$.s"},
			err:       "test.yaml:5:3: $.r1: the edit removes $.r1.d, which changes the keys of a merge through a merge key that merges a mapping with a merge key of its own, and this program does not change the keys of such a merge",
		},
		"set of a scalar over a merged mapping": {
			before:    stringtest.LinesLF("s: &s", "  a: 1", "m:", "  <<: *s"),
			fn:        set("$.s", 5),
			construct: edit.ConstructMergeForm,
			related:   []string{"$.m"},
			err: stringtest.JoinLF(
				"test.yaml:4:3: $.m: the edit sets $.s, which leaves <<: *s at 4:3 reading a value other than a "+
					"mapping, and this program does not merge a value other than a mapping",
				"|-- 3:1: $.m: this mapping merges the value",
				"|-- set a mapping there, or set the entries of the mapping one at a time",
				"`-- the edit sets $.s",
			),
		},
		"set of a sequence over a merged mapping in flow style": {
			before:    stringtest.LinesLF("s: &s {a: 1}", "m: {<<: *s, b: 2}"),
			fn:        set("$.s", []int{1}),
			construct: edit.ConstructMergeForm,
			related:   []string{"$.m"},
			err: "test.yaml:2:5: $.m: the edit sets $.s, which leaves <<: *s at 2:5 reading a value other than a " +
				"mapping, and this program does not merge a value other than a mapping",
		},
		"set of a scalar over a mapping that a merge sequence reads": {
			// Go-yaml's decoder refuses a merge of a scalar, as the merge spec
			// does, so the Set fails as for a merge of one alias.
			before:    stringtest.LinesLF("s: &s", "  a: 1", "m:", "  <<: [*s]"),
			fn:        set("$.s", 5),
			construct: edit.ConstructMergeForm,
			related:   []string{"$.m"},
			err: "test.yaml:4:3: $.m: the edit sets $.s, which leaves <<: [*s] at 4:3 reading a value other than a " +
				"mapping, and this program does not merge a value other than a mapping",
		},
		"removal of a key that a Set wrote in a source of a merge sequence": {
			// The merge key reads the mapping that the Set writes under the
			// anchor, so the zone of the merge key holds its keys.
			before:    stringtest.LinesLF("s: &s", "  a: 1", "<<: [*s]", "t: *s"),
			fn:        both(set("$.s", map[string]int{"b": 2, "c": 3}), remove("$.s.b")),
			construct: edit.ConstructMergeForm,
			related:   []string{"$.s"},
			err:       "test.yaml:3:1: $: the edit removes $.s.b, which changes the keys of a merge through a merge key that merges a sequence of aliases, and this program does not change the keys of such a merge",
		},
		"removal in a mapping whose merge sequence names one source twice": {
			// Related names the source once.
			before:    stringtest.LinesLF("s: &s {a: 1}", "m:", "  <<: [*s, *s]", "  c: 1"),
			fn:        remove("$.m.c"),
			construct: edit.ConstructMergeForm,
			related:   []string{"$.s"},
			err:       "test.yaml:3:3: $.m: the edit removes $.m.c, which changes the keys of a merge through a merge key that merges a sequence of aliases, and this program does not change the keys of such a merge",
		},
		"removal in a mapping whose merge sequence names its sources out of the order of the text": {
			// Related names the sources in the order of the text.
			before:    stringtest.LinesLF("a: &a {x: 1}", "b: &b {y: 1}", "m:", "  <<: [*b, *a]", "  c: 1"),
			fn:        remove("$.m.c"),
			construct: edit.ConstructMergeForm,
			related:   []string{"$.a", "$.b"},
			err:       "test.yaml:4:3: $.m: the edit removes $.m.c, which changes the keys of a merge through a merge key that merges a sequence of aliases, and this program does not change the keys of such a merge",
		},
		"set in a source of a merge sequence after a removal of the mapping that merges it": {
			// Control: the removal takes the merge key, so the zone goes with it.
			before: stringtest.LinesLF("s: &s", "  a: 1", "m:", "  <<: [*s]", "  b: 2"),
			fn:     both(remove("$.m"), set("$.s.z", 1)),
			want:   stringtest.LinesLF("s: &s", "  a: 1", "  z: 1"),
		},
		"set in a mapping after a Set drops the merge key of its source": {
			// Control: the merge key then merges one alias of a mapping without
			// a merge key, which the engine edits.
			before: stringtest.LinesLF("s: &s", "  a: 1", "  <<: {c: 1}", "<<: *s"),
			fn:     both(set("$.s", map[string]int{"k": 1}), set("$.z", 1)),
			want:   stringtest.LinesLF("s: &s", "  k: 1", "<<: *s", "z: 1"),
		},
		"removal inside a value of a source that a Set wrote for a merge sequence": {
			// The Set writes the mapping of m with the bytes of the source,
			// and the key of m is no key of the source.
			before: stringtest.LinesLF("s: &s", "  a: 1", "<<: [*s]"),
			fn:     both(set("$.s", map[string]any{"m": map[string]int{"a": 1}}), remove("$.s.m.a")),
			want:   stringtest.LinesLF("s: &s", "  m: {}", "<<: [*s]"),
		},
		"set of an own entry before a merge key that does not bring in its key": {
			// Control.
			before: stringtest.LinesLF("s: &s", "  a: 1", "m:", "  b: 2", "  <<: *s"),
			fn:     set("$.m.b", 3),
			want:   stringtest.LinesLF("s: &s", "  a: 1", "m:", "  b: 3", "  <<: *s"),
		},
		"set of the image of each service that merges a template": {
			// Control: the template's image is one node, which the wildcard
			// visits at the first service that merges it.
			before: airflow,
			apply:  "$.services.*.image",
			fn: func(ctx context.Context, n *edit.Node) error {
				return n.Set(ctx, "apache/airflow:3")
			},
			want: stringtest.LinesLF(
				"x-airflow-common: &airflow-common",
				`  image: "apache/airflow:3"`,
				"services:",
				"  web:",
				"    <<: *airflow-common",
				"    command: webserver",
				"  sched:",
				"    <<: *airflow-common",
				"    command: scheduler",
			),
		},
		"read of each entry of a merging mapping": {
			// Control.
			before: after,
			apply:  "$.m.*",
			fn: func(ctx context.Context, n *edit.Node) error {
				_, err := n.Decode[any](ctx)

				return err
			},
			want: after,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d, err := edit.New(niceyaml.NewSourceFromString(tc.before, niceyaml.WithName("test.yaml")).Documents()[0])
			require.NoError(t, err)

			apply := paths.Doc()
			if tc.apply != "" {
				apply = paths.MustParse(tc.apply)
			}

			err = d.Apply(t.Context(), apply, tc.fn)
			if tc.err == "" {
				require.NoError(t, err)
				assert.Equal(t, tc.want, d.Text())

				return
			}

			assert.Contains(t, niceyaml.FormatError(err), tc.err)
			assert.Equal(t, tc.before, d.Text())

			if tc.engine {
				require.ErrorIs(t, err, edit.ErrEngine)
				require.NotErrorIs(t, err, edit.ErrUnsupported)

				return
			}

			require.ErrorIs(t, err, edit.ErrUnsupported)
			require.NotErrorIs(t, err, edit.ErrEngine)

			var unsupported *edit.UnsupportedError

			require.ErrorAs(t, err, &unsupported)

			related := make([]string, 0, len(unsupported.Related))
			for _, p := range unsupported.Related {
				related = append(related, p.String())
			}

			assert.Equal(t, tc.construct, unsupported.Construct)
			assert.Equal(t, tc.inValue, unsupported.InValue)
			assert.Equal(t, tc.related, related)
			assert.Equal(t, !tc.inValue, niceyaml.IsInvalid(err))
		})
	}
}

func TestDoc_Apply_AliasCopy(t *testing.T) {
	t.Parallel()

	set := func(path string, v any) edit.Func {
		return func(ctx context.Context, root *edit.Node) error {
			return root.At(paths.MustParse(path)).Set(ctx, v)
		}
	}

	remove := func(path string) edit.Func {
		return func(_ context.Context, root *edit.Node) error {
			return root.At(paths.MustParse(path)).Remove()
		}
	}

	move := func(path, target string) edit.Func {
		return func(_ context.Context, root *edit.Node) error {
			return root.At(paths.MustParse(path)).Move(paths.MustParse(target))
		}
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

	var (
		anchored = stringtest.LinesLF("a: &x 1", "b: *x", "c: 2")
		inner    = stringtest.LinesLF("a:", "  x: &v 1", "b: *v")
		merged   = stringtest.LinesLF("d: &d", "  x: 1", "m:", "  <<: *d")
	)

	tcs := map[string]struct {
		fn     edit.Func
		before string
		// The path of the Apply, or $ when empty.
		apply string
		// The text of the Doc after an Apply that passes.
		want string
		// The first line of the error of the Apply, or "" for an Apply that
		// passes.
		err string
		// The fix that the error gives.
		fix string
		// The detail of the error that names the call, which comes last.
		call string
		// The paths of the other sites of the refusal.
		related []string
	}{
		"removal of an anchored value that an alias reads": {
			before:  anchored,
			fn:      remove("$.a"),
			err:     "test.yaml:2:4: $.b: the edit removes $.a, which this alias reads, and this program does not write a copy of a value at its alias",
			fix:     "set $.b to the value it reads first, or keep $.a",
			related: []string{"$.b"},
			call:    "the edit removes $.a",
		},
		"removal of an anchored value marks the alias that reads it": {
			// The site marks the "*" of the alias and its name.
			before: anchored,
			fn:     remove("$.a"),
			err: stringtest.JoinLF(
				"|-- set $.b to the value it reads first, or keep $.a",
				"`-- the edit removes $.a",
				"",
				"   1 | a: &x 1",
				"   2 | b: *x",
				"     |    ^^",
			),
			related: []string{"$.b"},
			call:    "the edit removes $.a",
		},
		"removal of the alias, then of the anchored value": {
			// Control: the alias goes first, so no alias reads the anchor.
			before: anchored,
			fn:     both(remove("$.b"), remove("$.a")),
			want:   "c: 2\n",
		},
		"removal of a mapping whose inner anchor an alias reads": {
			before:  inner,
			fn:      remove("$.a"),
			err:     "test.yaml:3:4: $.b: the edit removes $.a, which holds the anchor &v that this alias reads, and this program does not write a copy of a value at its alias",
			related: []string{"$.b"},
			call:    "the edit removes $.a",
		},
		"removal of a value that two aliases read": {
			// The problem binds at the first alias, and the other comes as a
			// detail.
			before:  stringtest.LinesLF("a: &v 1", "b: *v", "c: [*v]"),
			fn:      remove("$.a"),
			err:     "test.yaml:2:4: $.b: the edit removes $.a, which this alias reads, and this program does not write a copy of a value at its alias",
			fix:     "set $.b and $.c[0] to the values that they read first, or keep $.a",
			related: []string{"$.b", "$.c[0]"},
			call:    "the edit removes $.a",
		},
		"removal of a mapping that a merge key reads": {
			before:  merged,
			fn:      remove("$.d"),
			err:     "test.yaml:4:7: $.m: the edit removes $.d, which the merge key at 4:3 of $.m reads, and this program does not write a copy of a value at its alias",
			fix:     "set $.m to the value it reads first, which writes the merged entries in place of the merge key, or keep $.d",
			related: []string{"$.m"},
			call:    "the edit removes $.d",
		},
		"removal of a mapping that a merge key of the root reads": {
			// No path selects the root to set, so the fix asks for the merged
			// entries in place of the merge key.
			before:  stringtest.LinesLF("base: &b {img: x}", "<<: *b", "run: y"),
			fn:      remove("$.base"),
			err:     "test.yaml:2:5: $: the edit removes $.base, which the merge key at 2:1 of $ reads, and this program does not write a copy of a value at its alias",
			fix:     "write the merged entries in place of the merge key at 2:1, or keep $.base",
			related: []string{"$"},
			call:    "the edit removes $.base",
		},
		"removal of a mapping that an alias and a merge key read": {
			before:  stringtest.LinesLF("base: &b {img: x}", "other: *b", "job:", "  <<: *b", "  run: y"),
			fn:      remove("$.base"),
			err:     "test.yaml:2:8: $.other: the edit removes $.base, which this alias reads, and this program does not write a copy of a value at its alias",
			fix:     "set $.other and $.job to the values that they read first, or keep $.base",
			related: []string{"$.other", "$.job"},
			call:    "the edit removes $.base",
		},
		"removal of a mapping that two merge keys read": {
			before:  stringtest.LinesLF("base: &b {img: x}", "job:", "  <<: *b", "  run: y", "job2:", "  <<: *b"),
			fn:      remove("$.base"),
			err:     "test.yaml:3:7: $.job: the edit removes $.base, which the merge key at 3:3 of $.job reads, and this program does not write a copy of a value at its alias",
			fix:     "set $.job and $.job2 to the values that they read first, or keep $.base",
			related: []string{"$.job", "$.job2"},
			call:    "the edit removes $.base",
		},
		"removal of a mapping that a merge list reads": {
			before:  stringtest.LinesLF("d: &d", "  x: 1", "e: &e", "  y: 1", "m:", "  <<: [*e, *d]"),
			fn:      remove("$.d"),
			err:     "test.yaml:6:12: $.m: the edit removes $.d, which the merge key at 6:3 of $.m reads, and this program does not write a copy of a value at its alias",
			related: []string{"$.m"},
			call:    "the edit removes $.d",
		},
		"set over a mapping whose inner anchor an alias reads": {
			before:  inner,
			fn:      set("$.a", 5),
			err:     "test.yaml:3:4: $.b: the edit sets $.a, which holds the anchor &v that this alias reads, and this program does not write a copy of a value at its alias",
			related: []string{"$.b"},
			call:    "the edit sets $.a",
		},
		"set over an anchored mapping whose inner anchor an alias reads": {
			// The Set keeps &o, which *o then reads, but not &v.
			before:  stringtest.LinesLF("a: &o", "  x: &v 1", "b: *v", "c: *o"),
			fn:      set("$.a", 5),
			err:     "test.yaml:3:4: $.b: the edit sets $.a, which holds the anchor &v that this alias reads, and this program does not write a copy of a value at its alias",
			related: []string{"$.b"},
			call:    "the edit sets $.a",
		},
		"set over an anchored mapping that an alias reads": {
			// The Set keeps the anchor that the alias reads.
			before: stringtest.LinesLF("a: &o", "  x: 1", "c: *o"),
			fn:     set("$.a", 5),
			want:   stringtest.LinesLF("a: &o 5", "c: *o"),
		},
		"wildcard removal over an anchor and its alias": {
			// The wildcard meets the anchor first, so its first call fails.
			before:  anchored,
			apply:   "$.*",
			fn:      func(_ context.Context, n *edit.Node) error { return n.Remove() },
			err:     "test.yaml:2:4: $.b: the edit removes $.a, which this alias reads, and this program does not write a copy of a value at its alias",
			related: []string{"$.b"},
			call:    "the edit removes $.a",
		},
		"wildcard set over an anchor and its alias": {
			// The Set at a keeps the anchor, and the Set at b replaces the
			// alias.
			before: anchored,
			apply:  "$.*",
			fn:     func(ctx context.Context, n *edit.Node) error { return n.Set(ctx, 5) },
			want:   stringtest.LinesLF("a: &x 5", "b: 5", "c: 5"),
		},
		"set of the alias, then of the anchored value": {
			before: anchored,
			fn:     both(set("$.b", 5), set("$.a", 5)),
			want:   stringtest.LinesLF("a: &x 5", "b: 5", "c: 2"),
		},
		"move of an anchored value past its alias": {
			// The Move writes no copy, so the alias would come before its
			// anchor.
			before: stringtest.LinesLF("a: &x 1", "b: *x"),
			fn:     move("$.a", "$.z"),
			err:    "test.yaml:2:4: $.b: the edit moves $.a to $.z, after which this alias comes before the anchor that it reads, and this program does not write a copy of a value at its alias",
			fix:    "move the alias after the anchor first, or set it to the value it reads",
			call:   "the edit moves $.a to $.z",
		},
		"move of an alias past its anchor": {
			// Control: the alias stays after the anchor.
			before: stringtest.LinesLF("a: &x 1", "b: *x"),
			fn:     move("$.b", "$.c"),
			want:   stringtest.LinesLF("a: &x 1", "c: *x"),
		},
		"move of a mapping past the merge key that reads it": {
			before: merged,
			fn:     move("$.d", "$.z"),
			err:    "test.yaml:4:7: $.m: the edit moves $.d to $.z, after which the merge key at 4:3 of $.m comes before the anchor that it reads, and this program does not write a copy of a value at its alias",
			call:   "the edit moves $.d to $.z",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d, err := edit.New(niceyaml.NewSourceFromString(tc.before, niceyaml.WithName("test.yaml")).Documents()[0])
			require.NoError(t, err)

			apply := paths.Doc()
			if tc.apply != "" {
				apply = paths.MustParse(tc.apply)
			}

			err = d.Apply(t.Context(), apply, tc.fn)
			if tc.err == "" {
				require.NoError(t, err)
				assert.Equal(t, tc.want, d.Text())

				return
			}

			require.ErrorIs(t, err, edit.ErrUnsupported)
			require.NotErrorIs(t, err, edit.ErrEngine)
			assert.Contains(t, niceyaml.FormatError(err), tc.err)
			assert.Equal(t, tc.before, d.Text())

			if tc.fix != "" {
				assert.Contains(t, niceyaml.FormatError(err), "\n|-- "+tc.fix+"\n")
			}

			assert.Contains(t, niceyaml.FormatError(err), "\n`-- "+tc.call+"\n")

			var unsupported *edit.UnsupportedError

			require.ErrorAs(t, err, &unsupported)

			related := make([]string, 0, len(unsupported.Related))
			for _, p := range unsupported.Related {
				related = append(related, p.String())
			}

			assert.Equal(t, edit.ConstructAliasCopy, unsupported.Construct)
			assert.False(t, unsupported.InValue)
			assert.Equal(t, append([]string{}, tc.related...), related)
			assert.True(t, niceyaml.IsInvalid(err))
		})
	}
}
