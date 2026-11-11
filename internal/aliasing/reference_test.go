package aliasing_test

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/aliasing"
	"go.jacobcolvin.com/niceyaml/internal/aliaslimit"
	"go.jacobcolvin.com/niceyaml/internal/fault"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
)

func TestReferenceWeights(t *testing.T) {
	t.Parallel()

	// A sequence of 1000 scalars under the anchor d.
	wide := "d: &d " + yamltest.FlowSequence("x", 1000) + "\n"

	// A mapping of two entries under the anchor d.
	small := "d: &d {a: 1, b: 2}\n"

	// The anchor b of 1000 scalars inside the anchor a.
	inside := "a: &a {b: &b " + yamltest.FlowSequence("x", 1000) + "}\n"

	// The anchors n1 to n50, each inside the one before it, around a
	// sequence of 1000 scalars. The aliases name each anchor once.
	var (
		nest        = "o: "
		nestAliases = make([]string, 50)
	)

	for i := range nestAliases {
		nest += fmt.Sprintf("&n%d [", i+1)
		nestAliases[i] = fmt.Sprintf("*n%d", i+1)
	}

	nest += strings.Repeat("x, ", 999) + "x" + strings.Repeat("]", 50) + "\n"

	// The anchor b of 100 scalars inside the anchor a.
	insideSmall := "a: &a {b: &b " + yamltest.FlowSequence("x", 100) + "}\n"

	// The entries of a mapping, each under a comment line, with indent
	// before each of the 40 lines.
	commented := func(indent string) string {
		return yamltest.GenerateLines(20, indent+"# "+strings.Repeat("c", 20)+"\n"+indent+"k%d: v")
	}

	// A document whose alias *d lies two mappings deep, on a line that
	// indent spaces start.
	indented := func(indent int) string {
		return stringtest.LinesLF(
			"a:",
			strings.Repeat(" ", indent/2)+"b:",
			strings.Repeat(" ", indent)+"c: *d",
		)
	}

	const (
		// A scalar inside six sequences.
		deep = "[[[[[[x]]]]]]"

		ratio = "aliases expand the document past the alias limit"
		loop  = "x: &x [*y]\ny: &y [*x]\n"

		// The anchors x and y reach each other, and each lies on a tagged
		// alias of the other name.
		taggedLoop = "x: &x !foo *y\ny: &y !foo *x\n"

		// The anchors x and y reach each other only through the anchor x
		// inside o, which the last anchor x hides.
		hiddenLoop = "o: &o {x: &x [*y]}\ny: &y [*x]\nx: &x 1\n"

		// The anchors a and b reach each other, and b lies inside a.
		nestedLoop = "a: &a {b: &b {k: *a}, c: *b}\n"

		// The anchor m holds aliases of its own name.
		selfLoop = "m: &m {k: *m, j: *m}\n"
	)

	tcs := map[string]struct {
		// The message of the error under the line and the column of the
		// alias that it reports, or empty for a document that passes.
		want string
		// The document that reads the reference documents of refs.
		input string
		// The text of each reference document, in order.
		refs []string
		opts []niceyaml.SourceOption
		// Whether the check is CheckDecodeText.
		text bool
		// Whether one document that holds refs and then input reads the
		// anchors that input reads through the reference documents. That
		// document then gets the same verdict.
		oneFile bool
	}{
		"few aliases to a sequence": {
			refs:    []string{wide},
			input:   "v: " + yamltest.FlowSequence("*d", 3) + "\n",
			oneFile: true,
		},
		"many aliases to a sequence": {
			refs:    []string{wide},
			input:   "v: " + yamltest.FlowSequence("*d", 500) + "\n",
			want:    "1:5: " + ratio,
			oneFile: true,
		},
		"many aliases with the limit off": {
			refs:    []string{wide},
			input:   "v: " + yamltest.FlowSequence("*d", 500) + "\n",
			opts:    []niceyaml.SourceOption{niceyaml.WithAliasLimit(false)},
			oneFile: true,
		},
		"one alias to a large sequence": {
			// The read of the anchor counts once, as it does in the
			// document that defines it. One alias thus repeats half of
			// what the decode reads.
			refs:    []string{"d: &d " + yamltest.FlowSequence("x", 5000) + "\n"},
			input:   "v: *d\n",
			oneFile: true,
		},
		"merge keys that share defaults": {
			refs:    []string{small},
			input:   stringtest.LinesLF("server: {<<: *d}", "worker: {<<: *d}"),
			oneFile: true,
		},
		"many merge keys of a mapping": {
			refs: []string{"d: &d\n" + yamltest.GenerateLines(1000, "  k%d: v")},
			input: stringtest.LinesLF(
				"v:",
				"  <<: "+yamltest.FlowSequence("*d", 500),
			),
			want:    "2:8: " + ratio,
			oneFile: true,
		},
		"one alias to nested levels": {
			// The reference document does not pass the limit alone, and
			// one alias to its last level reads all of it.
			refs:    []string{yamltest.AliasLevels(7)},
			input:   "v: *l7\n",
			want:    "1:4: " + ratio,
			oneFile: true,
		},
		"one alias to the first of nested levels": {
			refs:  []string{yamltest.AliasLevels(7)},
			input: "v: *l1\n",
		},
		"anchor that repeats another anchor": {
			refs: []string{stringtest.LinesLF(
				"base: &base "+yamltest.FlowSequence("x", 100),
				"big: &big "+yamltest.FlowSequence("*base", 50),
			)},
			input:   "v: *big\n",
			oneFile: true,
		},
		"alias to the last of a chain of anchors": {
			// The decoder reads a and b before it reads c. The read of
			// each of the three anchors counts once, as its definition
			// does in one file.
			refs: []string{stringtest.LinesLF(
				"a: &a "+yamltest.FlowSequence("x", 100),
				"b: &b "+yamltest.FlowSequence("*a", 10),
				"c: &c [*b]",
			)},
			input:   "v: *c\n",
			oneFile: true,
		},
		"many aliases to a scalar": {
			// A decode into a value shares the scalar between the aliases.
			refs:    []string{"s: &s " + strings.Repeat("x", 2000) + "\n"},
			input:   "v: " + yamltest.FlowSequence("*s", 500) + "\n",
			oneFile: true,
		},
		"text of many aliases to a scalar": {
			refs:    []string{"s: &s " + strings.Repeat("x", 2000) + "\n"},
			input:   "v: " + yamltest.FlowSequence("*s", 500) + "\n",
			opts:    []niceyaml.SourceOption{niceyaml.WithDocumentByteLimit(niceyaml.NoLimit)},
			text:    true,
			want:    "1:5: " + ratio,
			oneFile: true,
		},
		"text of aliases to a sequence": {
			// The read of the anchor counts each of its nodes once for the
			// text of a decode, as its definition does in one file. The
			// 150 aliases then repeat too large a share of that text.
			refs:    []string{wide},
			input:   "v: " + yamltest.FlowSequence("*d", 150) + "\n",
			opts:    []niceyaml.SourceOption{niceyaml.WithDocumentByteLimit(niceyaml.NoLimit)},
			text:    true,
			want:    "1:5: " + ratio,
			oneFile: true,
		},
		"text of an alias to an anchor that repeats a scalar": {
			// A decode into a value shares the scalar of s between the
			// aliases inside b. The text of a decode copies the scalar at
			// each of them, and the read of b counts those copies as the
			// aliases of one file count.
			refs: []string{stringtest.LinesLF(
				"s: &s "+strings.Repeat("x", 150),
				"b: &b "+yamltest.FlowSequence("*s", 150),
			)},
			input:   "v: *b\n",
			text:    true,
			want:    "1:4: " + ratio,
			oneFile: true,
		},
		"alias to a long scalar beside aliases to a sequence": {
			// The alias *s counts as one node, and the read of the anchor
			// s counts the text of its scalar once, as its definition does
			// in one file. Beside that text, the aliases *d repeat a share
			// that the limit allows.
			refs: []string{stringtest.LinesLF(
				"s: &s "+strings.Repeat("x", 5000),
				"d: &d "+yamltest.FlowSequence("x", 1000),
			)},
			input: stringtest.LinesLF(
				"a: *s",
				"v: "+yamltest.FlowSequence("*d", 150),
			),
			oneFile: true,
		},
		"many aliases to a tagged scalar": {
			refs:    []string{"s: &s !!str " + strings.Repeat("x", 2000) + "\n"},
			input:   "v: " + yamltest.FlowSequence("*s", 500) + "\n",
			want:    "1:5: " + ratio,
			oneFile: true,
		},
		"many aliases to a binary scalar": {
			refs:    []string{"s: &s " + binaryScalar + "\n"},
			input:   "v: " + yamltest.FlowSequence("*s", 500) + "\n",
			want:    "1:5: " + ratio,
			oneFile: true,
		},
		"alias keys to a sequence": {
			// The decoder writes each key out as text.
			refs:    []string{"d: &d " + yamltest.FlowSequence(strings.Repeat("x", 100), 100) + "\n"},
			input:   "v: " + yamltest.FlowSequence("{? *d : 1}", 300) + "\n",
			want:    "1:8: " + ratio,
			oneFile: true,
		},
		"aliases to an anchor of the document on an alias": {
			refs: []string{wide},
			input: stringtest.LinesLF(
				"a: &a",
				"  *d",
				"v: "+yamltest.FlowSequence("*a", 500),
			),
			want:    "3:5: " + ratio,
			oneFile: true,
		},
		"aliases to an anchor of the document on a text tag": {
			// The decoder converts the text under the tag at each alias.
			refs: []string{"s: &s " + strings.Repeat("x", 2000) + "\n"},
			input: stringtest.LinesLF(
				"a: &a !!str *s",
				"v: "+yamltest.FlowSequence("*a", 500),
			),
			want:    "2:5: " + ratio,
			oneFile: true,
		},
		"aliases to an anchor on an alias": {
			refs:    []string{wide + "k: &k\n  *d\n"},
			input:   "v: " + yamltest.FlowSequence("*k", 500) + "\n",
			want:    "1:5: " + ratio,
			oneFile: true,
		},
		"aliases to an anchor on a tagged alias": {
			refs:    []string{wide + "k: &k !foo *d\n"},
			input:   "v: " + yamltest.FlowSequence("*k", 500) + "\n",
			want:    "1:5: " + ratio,
			oneFile: true,
		},
		"alias inside an anchor of its name": {
			// The anchor m is a loop of one name. A decode into a Go type
			// follows the alias inside it, so one alias to the anchor is
			// past the limit. One file reads the alias inside m as null.
			refs:  []string{selfLoop},
			input: "v: *m\n",
			want:  "1:4: " + ratio,
		},
		"merge key of an anchor around an alias of its name": {
			refs:  []string{selfLoop},
			input: "v: {<<: *m}\n",
			want:  "1:9: " + ratio,
		},
		"anchor beside an anchor around an alias of its name": {
			refs:  []string{selfLoop + small},
			input: "v: *d\n",
		},
		"alias to an anchor inside another around an alias to the outer": {
			// The anchor a holds an alias of its own name inside b, so
			// each of a and b reaches a loop.
			refs:  []string{"a: &a {b: &b {k: *a}}\n"},
			input: "v: *b\n",
			want:  "1:4: " + ratio,
		},
		"alias to an anchor around an alias to it inside another": {
			refs:  []string{"a: &a {b: &b {k: *a}}\n"},
			input: "v: *a\n",
			want:  "1:4: " + ratio,
		},
		"alias to an anchor on an alias of its name": {
			refs:  []string{"e: &e\n  *e\n"},
			input: "v: *e\n",
			want:  "1:4: " + ratio,
		},
		"alias inside an anchor of its name before the last of the name": {
			// The anchor a inside o holds aliases of its own name. A read
			// of o follows them to the last anchor a.
			refs:  []string{"o: &o {k: &a [*a, *a]}\nb: &a [x, y]\n"},
			input: "v: *o\n",
			want:  "1:4: " + ratio,
		},
		"aliases to an anchor on an alias without an anchor": {
			refs:    []string{"e: &e\n  *missing\n"},
			input:   "v: " + yamltest.FlowSequence("*e", 500) + "\n",
			oneFile: true,
		},
		"alias without an anchor": {
			refs:  []string{wide},
			input: "v: " + yamltest.FlowSequence("*missing", 500) + "\n",
		},
		"alias under an anchor without an anchor": {
			refs:  []string{"d: &d " + yamltest.FlowSequence("*missing", 1000) + "\n"},
			input: "v: " + yamltest.FlowSequence("*d", 500) + "\n",
			want:  "1:5: " + ratio,
		},
		"anchor of the document before the alias": {
			// The alias reads the anchor of its own document.
			refs:  []string{wide},
			input: stringtest.LinesLF("d: &d x", "v: "+yamltest.FlowSequence("*d", 500)),
		},
		"anchor of the document after the alias": {
			refs:  []string{wide},
			input: stringtest.LinesLF("v: "+yamltest.FlowSequence("*d", 500), "d: &d x"),
			want:  "1:5: " + ratio,
		},
		"last anchor of a name": {
			refs:  []string{wide + "e: &d x\n"},
			input: "v: " + yamltest.FlowSequence("*d", 500) + "\n",
		},
		"anchor of a later reference document": {
			refs:  []string{wide, "e: &d x\n"},
			input: "v: " + yamltest.FlowSequence("*d", 500) + "\n",
		},
		"anchor of an earlier reference document": {
			refs:  []string{"e: &d x\n", wide},
			input: "v: " + yamltest.FlowSequence("*d", 500) + "\n",
			want:  "1:5: " + ratio,
		},
		"anchor inside another anchor before the last of its name": {
			// A decode that reads o registers the inner anchor again, so
			// an alias can read it, and the count takes the larger.
			refs:  []string{"o: &o {" + strings.TrimSuffix(wide, "\n") + "}\ne: &d x\n"},
			input: "v: " + yamltest.FlowSequence("*d", 500) + "\n",
			want:  "1:5: " + ratio,
		},
		"anchor inside another anchor of a reference document below": {
			refs:  []string{"o: &o {" + strings.TrimSuffix(wide, "\n") + "}\n", "e: &d x\n"},
			input: "v: " + yamltest.FlowSequence("*d", 500) + "\n",
			want:  "1:5: " + ratio,
		},
		"one alias to a large last anchor after a small one inside another": {
			// The decode reads the last anchor d, and that read counts
			// once, so one alias repeats half of what the decode reads.
			refs:  []string{"o: &o {d: &d x}\ne: &d " + yamltest.FlowSequence("x", 5000) + "\n"},
			input: "v: *d\n",
		},
		"alias to a last anchor around aliases after a small one inside another": {
			// The count follows the aliases under the last anchor d to
			// base, so the read of base counts once too.
			refs: []string{
				"base: &base " + yamltest.FlowSequence("x", 1000) + "\no: &o {d: &d x}\ne: &d [*base, *base]\n",
			},
			input: "v: *d\n",
		},
		"few aliases to an anchor and to an anchor inside it": {
			refs:    []string{inside},
			input:   stringtest.LinesLF("w: *a", "v: "+yamltest.FlowSequence("*b", 100)),
			oneFile: true,
		},
		"aliases to an anchor and to an anchor inside it": {
			// The read of a holds the nodes of b, so they count once.
			refs:    []string{inside},
			input:   stringtest.LinesLF("w: *a", "v: "+yamltest.FlowSequence("*b", 150)),
			want:    "1:4: " + ratio,
			oneFile: true,
		},
		"aliases to an anchor inside another before an alias to the outer": {
			refs:    []string{inside},
			input:   stringtest.LinesLF("v: "+yamltest.FlowSequence("*b", 150), "w: *a"),
			want:    "2:4: " + ratio,
			oneFile: true,
		},
		"aliases to an anchor inside another alone": {
			refs:    []string{inside},
			input:   "v: " + yamltest.FlowSequence("*b", 150) + "\n",
			want:    "1:5: " + ratio,
			oneFile: true,
		},
		"alias to an anchor around an alias inside an anchor of its name": {
			// The anchor n inside r is a loop of one name, and a read of r
			// reaches it.
			refs:  []string{"r: &r {x: &n [y, *n]}\n"},
			input: "v: *r\n",
			want:  "1:4: " + ratio,
		},
		"aliases to each anchor of a nest of anchors": {
			refs: []string{nest},
			input: stringtest.LinesLF(
				"w: ["+strings.Join(nestAliases, ", ")+"]",
				"v: "+yamltest.FlowSequence("*n50", 100),
			),
			want:    "1:5: " + ratio,
			oneFile: true,
		},
		"loop of names": {
			refs:  []string{loop},
			input: "v: *x\n",
			want:  "1:4: " + ratio,
		},
		"loop of names beside the anchor that an alias reads": {
			refs:  []string{loop + small},
			input: "v: *d\n",
		},
		"text of an alias to a loop of names": {
			// The text of a loop does not end, so one alias passes the
			// default document byte limit.
			refs:  []string{loop},
			input: "v: *x\n",
			text:  true,
			want:  "1:4: aliases repeat more than 256 KiB of text",
		},
		"text of an alias to a loop of names without the document byte limit and the depth limit": {
			refs:  []string{loop},
			input: "v: *x\n",
			opts: []niceyaml.SourceOption{
				niceyaml.WithDocumentByteLimit(niceyaml.NoLimit),
				niceyaml.WithDepthLimit(niceyaml.NoLimit),
			},
			text: true,
			want: "1:4: " + ratio,
		},
		"loop of names through an anchor inside another anchor": {
			// A decode that reads o registers the anchor x inside it, and
			// can then follow the loop from y. The count cannot tell
			// whether a decode reads o, so every document that reaches the
			// loop is past the limit.
			refs:  []string{hiddenLoop},
			input: "v: *y\n",
			want:  "1:4: " + ratio,
		},
		"name whose anchor inside another anchor closes a loop": {
			refs:  []string{hiddenLoop},
			input: "v: *x\n",
			want:  "1:4: " + ratio,
		},
		"aliases to a small last anchor after a large one inside a hidden anchor": {
			// The anchor b hides the first anchor o, so no decode reads
			// the anchor x inside it.
			refs: []string{
				"a: &o {x: &x " + yamltest.FlowSequence("x", 1000) + "}\nb: &o 1\nc: &x 1\n",
			},
			input:   "v: " + yamltest.FlowSequence("*x", 300) + "\n",
			oneFile: true,
		},
		"loop of names through an anchor inside a hidden anchor": {
			// The loop closes only through the anchor x inside the first
			// anchor o, which the anchor b hides. A decode reads the last
			// anchor x for the alias inside y and ends.
			refs:  []string{"a: &o {x: &x [*y]}\nb: &o 1\ny: &y [*x]\nc: &x 1\n"},
			input: "v: *y\n",
		},
		"anchor beside a loop through an anchor inside another anchor": {
			refs:  []string{hiddenLoop + small},
			input: "v: *d\n",
		},
		"loop of an anchor and an anchor inside it read at the inner": {
			// The decoder reads a before b for this document, and the read
			// of a registers b. A decode into any thus ends, and a decode
			// into a recursive Go type might not end. The weights hold one
			// verdict for the loop.
			refs:  []string{nestedLoop},
			input: "v: *b\n",
			want:  "1:4: " + ratio,
		},
		"loop of an anchor and an anchor inside it read at the outer": {
			// The decoder reads b before a for this document, and rejects
			// the alias *a under b.
			refs:  []string{nestedLoop},
			input: "v: *a\n",
			want:  "1:4: " + ratio,
		},
		"text of aliases past the document byte limit": {
			// Each alias writes each of the 100 scalars of d on a line of
			// its own, so the text passes 4 KiB at the sixth alias.
			refs:    []string{"d: &d " + yamltest.FlowSequence("x", 100) + "\n"},
			input:   "v: " + yamltest.FlowSequence("*d", 20) + "\n",
			opts:    []niceyaml.SourceOption{niceyaml.WithDocumentByteLimit(4096)},
			text:    true,
			want:    "1:25: aliases repeat more than 4 KiB of text",
			oneFile: true,
		},
		"text of aliases within the document byte limit": {
			refs:  []string{"d: &d " + yamltest.FlowSequence("x", 100) + "\n"},
			input: "v: " + yamltest.FlowSequence("*d", 3) + "\n",
			opts:  []niceyaml.SourceOption{niceyaml.WithDocumentByteLimit(4096)},
			text:  true,
		},
		"text of an alias past the depth limit": {
			// The alias lies one level deep and its content nests nine.
			refs:  []string{"d: &d " + strings.Repeat("[", 9) + "x" + strings.Repeat("]", 9) + "\n"},
			input: "v: *d\n",
			opts:  []niceyaml.SourceOption{niceyaml.WithDepthLimit(8)},
			text:  true,
			want:  "1:4: aliases nest the document more than 8 levels",
		},
		"text of an alias chain past the depth limit": {
			refs:  []string{yamltest.AliasChain(20)},
			input: "v: *c20\n",
			opts:  []niceyaml.SourceOption{niceyaml.WithDepthLimit(10)},
			text:  true,
			want:  "1:4: aliases nest the document more than 10 levels",
		},
		"text of an alias at the depth limit": {
			refs:  []string{yamltest.AliasChain(8)},
			input: "v: *c8\n",
			opts:  []niceyaml.SourceOption{niceyaml.WithDepthLimit(10)},
			text:  true,
		},
		"anchor that repeats another anchor many times": {
			// The aliases inside big repeat base where the decoder reads
			// big, so they count as the aliases of one file do.
			refs: []string{stringtest.LinesLF(
				"base: &base "+yamltest.FlowSequence("x", 100),
				"big: &big "+yamltest.FlowSequence("*base", 200),
			)},
			input:   "v: *big\n",
			want:    "1:4: " + ratio,
			oneFile: true,
		},
		"text of one alias to long scalars": {
			// The read of the anchor counts each scalar by its text, as
			// the alias does, so the alias repeats half of the read.
			refs:    []string{"d: &d " + yamltest.FlowSequence(strings.Repeat("x", 200), 10) + "\n"},
			input:   "v: *d\n",
			opts:    []niceyaml.SourceOption{niceyaml.WithDocumentByteLimit(niceyaml.NoLimit)},
			text:    true,
			oneFile: true,
		},
		"alias keys to an anchor on an alias": {
			// The anchor k reads the sequence of d, so the decoder writes
			// each key out as text.
			refs:    []string{"d: &d " + yamltest.FlowSequence(strings.Repeat("x", 1000), 2) + "\nk: &k\n  *d\n"},
			input:   "v: " + yamltest.FlowSequence("{? *k : 1}", 300) + "\n",
			want:    "1:8: " + ratio,
			oneFile: true,
		},
		"alias keys to an anchor inside another before the last of its name": {
			// The larger anchor d is a sequence, so the decoder writes
			// each key out as text.
			refs: []string{
				"o: &o {d: &d " + yamltest.FlowSequence(strings.Repeat("x", 1000), 2) + "}\ne: &d x\n",
			},
			input: "v: " + yamltest.FlowSequence("{? *d : 1}", 300) + "\n",
			want:  "1:8: " + ratio,
		},
		"text of aliases to an anchor on an alias past the document byte limit": {
			refs:    []string{"d: &d " + yamltest.FlowSequence("x", 100) + "\nk: &k\n  *d\n"},
			input:   "v: " + yamltest.FlowSequence("*k", 20) + "\n",
			opts:    []niceyaml.SourceOption{niceyaml.WithDocumentByteLimit(4096)},
			text:    true,
			want:    "1:25: aliases repeat more than 4 KiB of text",
			oneFile: true,
		},
		"text of aliases to an anchor inside another before the last of its name": {
			// The document byte limit bounds the text of the larger anchor d.
			refs:  []string{"o: &o {d: &d " + yamltest.FlowSequence("x", 100) + "}\ne: &d x\n"},
			input: "v: " + yamltest.FlowSequence("*d", 20) + "\n",
			opts:  []niceyaml.SourceOption{niceyaml.WithDocumentByteLimit(4096)},
			text:  true,
			want:  "1:25: aliases repeat more than 4 KiB of text",
		},
		"alias to an anchor around an alias inside another anchor": {
			// The alias under b reads d wherever an alias reaches b.
			refs:    []string{small + "a: &a {b: &b [*d, *d]}\n"},
			input:   stringtest.LinesLF("v: *a", "w: *b"),
			oneFile: true,
		},
		"alias to an anchor around an alias to a large anchor inside another": {
			// The decoder reads d before it reads a, so the read of d
			// counts once, as its definition does in one file.
			refs:    []string{"d: &d " + yamltest.FlowSequence("x", 5000) + "\na: &a {b: &b [*d]}\n"},
			input:   "v: *a\n",
			oneFile: true,
		},
		"aliases to an anchor and to an anchor two anchors inside it": {
			// The read of a holds the nodes of b, which lies inside the
			// anchor m inside a.
			refs: []string{"a: &a {m: &m {b: &b " + yamltest.FlowSequence("x", 1000) + "}}\n"},
			input: stringtest.LinesLF(
				"w: *a",
				"v: "+yamltest.FlowSequence("*b", 150),
			),
			want:    "1:4: " + ratio,
			oneFile: true,
		},
		"text of aliases to an anchor around long scalars inside another": {
			// A decode into a value shares each scalar of b, and the text
			// of each alias copies them.
			refs:    []string{"a: &a {b: &b " + yamltest.FlowSequence(strings.Repeat("x", 200), 10) + "}\n"},
			input:   "v: " + yamltest.FlowSequence("*a", 500) + "\n",
			opts:    []niceyaml.SourceOption{niceyaml.WithDocumentByteLimit(niceyaml.NoLimit)},
			text:    true,
			want:    "1:5: " + ratio,
			oneFile: true,
		},
		"aliases to an anchor around long scalars inside another": {
			refs:    []string{"a: &a {b: &b " + yamltest.FlowSequence(strings.Repeat("x", 200), 10) + "}\n"},
			input:   "v: " + yamltest.FlowSequence("*a", 500) + "\n",
			oneFile: true,
		},
		"text of aliases to a mapping around an anchor with comments": {
			// The text of o holds the text of the anchor d inside it, as
			// the reference document spells it.
			refs: []string{
				"o: &o\n  d: &d\n" + yamltest.GenerateLines(50, "    k%d: v # "+strings.Repeat("c", 30)),
			},
			input:   "v: " + yamltest.FlowSequence("*o", 20) + "\n",
			opts:    []niceyaml.SourceOption{niceyaml.WithDocumentByteLimit(16384)},
			text:    true,
			want:    "1:33: aliases repeat more than 16 KiB of text",
			oneFile: true,
		},
		"text of aliases to a mapping with comments past the document byte limit": {
			// Each alias copies the 50 lines of d as the reference
			// document spells them, comments included. That text passes
			// 16 KiB at the eighth alias. The block style text of the 20
			// aliases, without the comments, does not.
			refs:    []string{"d: &d\n" + yamltest.GenerateLines(50, "  k%d: v # "+strings.Repeat("c", 30))},
			input:   "v: " + yamltest.FlowSequence("*d", 20) + "\n",
			opts:    []niceyaml.SourceOption{niceyaml.WithDocumentByteLimit(16384)},
			text:    true,
			want:    "1:33: aliases repeat more than 16 KiB of text",
			oneFile: true,
		},
		"text of aliases to a mapping with comments within the document byte limit": {
			refs:    []string{"d: &d\n" + yamltest.GenerateLines(50, "  k%d: v # "+strings.Repeat("c", 30))},
			input:   "v: " + yamltest.FlowSequence("*d", 7) + "\n",
			opts:    []niceyaml.SourceOption{niceyaml.WithDocumentByteLimit(16384)},
			text:    true,
			oneFile: true,
		},
		"text of an alias to an anchor and of aliases to an anchor inside it": {
			// The alias *a and the first alias *b write out fewer than
			// 2 KiB together, and the second alias *b passes the limit.
			refs:    []string{insideSmall},
			input:   "v: [*a, *b, *b, *b, *b, *b, *b, *b, *b]\n",
			opts:    []niceyaml.SourceOption{niceyaml.WithDocumentByteLimit(2048)},
			text:    true,
			want:    "1:13: aliases repeat more than 2 KiB of text",
			oneFile: true,
		},
		"text of an alias to an anchor inside another and of aliases to the outer": {
			// The text of a holds the text of b, whichever of the two
			// names the weights weigh first. The second alias *a passes
			// the limit.
			refs:    []string{insideSmall},
			input:   "v: [*b, *a, *a, *a, *a, *a, *a, *a]\n",
			opts:    []niceyaml.SourceOption{niceyaml.WithDocumentByteLimit(2048)},
			text:    true,
			want:    "1:13: aliases repeat more than 2 KiB of text",
			oneFile: true,
		},
		"text of an alias to a deeper anchor inside another before the last of its name": {
			// A decode that reads o registers the anchor x inside it, so
			// the depth limit bounds the deeper of the anchors x.
			refs:  []string{"o: &o {x: &x " + deep + "}\nx: &x 1\n"},
			input: "v: *x\n",
			opts:  []niceyaml.SourceOption{niceyaml.WithDepthLimit(5)},
			text:  true,
			want:  "1:4: aliases nest the document more than 5 levels",
		},
		"text of an alias to an anchor on an alias past the depth limit": {
			// The anchor a does not define content of its own, so its
			// alias nests what the alias *b nests.
			refs:  []string{"b: &b " + deep + "\na: &a\n  *b\n"},
			input: "v: *a\n",
			opts:  []niceyaml.SourceOption{niceyaml.WithDepthLimit(5)},
			text:  true,
			want:  "1:4: aliases nest the document more than 5 levels",
		},
		"text of an indented alias to a mapping with comments": {
			// The copy of the 40 lines of d indents each of them to the
			// line of the alias, 60 columns in.
			refs:    []string{"d: &d\n" + commented("  ")},
			input:   indented(60),
			opts:    []niceyaml.SourceOption{niceyaml.WithDocumentByteLimit(1024)},
			text:    true,
			want:    "3:64: aliases repeat more than 1 KiB of text",
			oneFile: true,
		},
		"text of an alias at a small indent to a mapping with comments": {
			// The copy of the same 40 lines fits the limit under an
			// indentation of 2 columns.
			refs:    []string{"d: &d\n" + commented("  ")},
			input:   indented(2),
			opts:    []niceyaml.SourceOption{niceyaml.WithDocumentByteLimit(1024)},
			text:    true,
			oneFile: true,
		},
		"text of an indented alias to a mapping around an anchor with comments": {
			// The text of d holds the 40 lines of the anchor e inside it.
			refs:    []string{"d: &d\n  e: &e\n" + commented("    ")},
			input:   indented(60),
			opts:    []niceyaml.SourceOption{niceyaml.WithDocumentByteLimit(1024)},
			text:    true,
			want:    "3:64: aliases repeat more than 1 KiB of text",
			oneFile: true,
		},
		"text of an alias at a small indent to a mapping around an anchor with comments": {
			refs:    []string{"d: &d\n  e: &e\n" + commented("    ")},
			input:   indented(2),
			opts:    []niceyaml.SourceOption{niceyaml.WithDocumentByteLimit(1024)},
			text:    true,
			oneFile: true,
		},
		"aliases to an anchor of the document on a tagged alias to a binary scalar": {
			// The tag over the alias *b leads to the !!binary scalar of the
			// reference document. The schema validator spells the bytes of
			// that scalar as text at each alias *s.
			refs: []string{"b: &b " + binaryScalar + "\n"},
			input: stringtest.LinesLF(
				"s: &s !foo *b",
				"v: "+yamltest.FlowSequence("*s", 500),
			),
			want:    "2:5: " + ratio,
			oneFile: true,
		},
		"text of aliases to a longer scalar inside another before the last of its name": {
			// The anchor x inside o holds one scalar of 400 bytes, and the
			// last anchor x holds 100 short scalars. The document byte limit bounds
			// the larger scalar text of the two.
			refs: []string{
				"o: &o {x: &x [" + strings.Repeat("y", 400) + "]}\nx: &x " + yamltest.FlowSequence("b", 100) + "\n",
			},
			input: "v: [*x, *x]\n",
			opts:  []niceyaml.SourceOption{niceyaml.WithDocumentByteLimit(1024)},
			text:  true,
			want:  "1:5: aliases repeat more than 1 KiB of text",
		},
		"text of aliases to an anchor with a comment inside another before the last of its name": {
			// The anchor x inside o spells a comment of 600 bytes between
			// its elements, and the last anchor x spells 3 bytes. The byte
			// limit bounds the longer spelling.
			refs: []string{stringtest.LinesLF(
				"o: &o",
				"  x: &x",
				"    - a",
				"    # "+strings.Repeat("c", 600),
				"    - b",
				"x: &x [b]",
			)},
			input: "v: [*x, *x]\n",
			opts:  []niceyaml.SourceOption{niceyaml.WithDocumentByteLimit(1024)},
			text:  true,
			want:  "1:9: aliases repeat more than 1 KiB of text",
		},
		"text of an indented alias to commented lines inside another before the last of its name": {
			// The anchor d inside o spells 40 lines, and the last anchor d
			// spells one. The copy indents each line of the longer
			// spelling to the line of the alias, 60 columns in.
			refs:  []string{"o: &o\n  d: &d\n" + commented("    ") + "d: &d 1\n"},
			input: indented(60),
			opts:  []niceyaml.SourceOption{niceyaml.WithDocumentByteLimit(1024)},
			text:  true,
			want:  "3:64: aliases repeat more than 1 KiB of text",
		},
		"text of an alias to a mapping around an alias to a sequence before a scalar of its name": {
			// The anchor x inside o is a sequence, and the last anchor x is
			// a scalar. The value of k under m thus counts as a sequence,
			// which writes each of its 200 elements on a line of its own.
			refs: []string{
				"o: &o {x: &x " + yamltest.FlowSequence("a", 200) + "}\nm: &m {k: *x}\nx: &x 1\n",
			},
			input: "v: *m\n",
			opts:  []niceyaml.SourceOption{niceyaml.WithDocumentByteLimit(1024)},
			text:  true,
			want:  "1:4: aliases repeat more than 1 KiB of text",
		},
		"loop of names through tagged aliases": {
			// Each anchor lies on a tagged alias of the other name, and
			// does not hold a collection.
			refs:  []string{taggedLoop},
			input: "v: *x\n",
			want:  "1:4: " + ratio,
		},
		"loop of names through tagged aliases read at the second name": {
			refs:  []string{taggedLoop},
			input: "v: *y\n",
			want:  "1:4: " + ratio,
		},
		"text of aliases to a mapping whose first key has a long anchor": {
			// The spelled text of d starts at the anchor of its first key,
			// so each alias copies the 300 bytes of that anchor name.
			refs:    []string{"d: &d\n  &" + strings.Repeat("n", 300) + " key: v\n"},
			input:   "v: [*d, *d, *d, *d]\n",
			opts:    []niceyaml.SourceOption{niceyaml.WithDocumentByteLimit(1024)},
			text:    true,
			want:    "1:17: aliases repeat more than 1 KiB of text",
			oneFile: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			check := aliasing.CheckDecode
			if tc.text {
				check = aliasing.CheckDecodeText
			}

			opts := append([]niceyaml.SourceOption{yamltest.References(tc.refs...)}, tc.opts...)
			doc := yamltest.FirstDocument(t, tc.input, opts...)

			// The document keeps the result, so a second check returns the
			// same error.
			err := check(doc)
			assert.Equal(t, err, check(doc))
			assert.Equal(t, tc.want, placed(t, err))

			if !tc.text {
				assert.Equal(t, tc.want != "", aliasing.PastLimit(doc))
			}

			if !tc.oneFile {
				return
			}

			// The same anchors inside the document give the same verdict.
			// The reference documents come first there, so the aliases
			// lie as many lines further down.
			whole := strings.Join(tc.refs, "") + tc.input
			wholeOpts := append(yamltest.NoSizeLimits(), tc.opts...)

			_, message, _ := strings.Cut(tc.want, ": ")
			_, wholeMessage, _ := strings.Cut(placed(t, check(yamltest.FirstDocument(t, whole, wholeOpts...))), ": ")

			assert.Equal(t, message, wholeMessage)
		})
	}
}

func TestReferenceWeights_Shared(t *testing.T) {
	t.Parallel()

	shared := niceyaml.NewSourceFromString(yamltest.AliasLevels(7))
	lower := niceyaml.NewSourceFromString("m: &m [*l2]\n", niceyaml.WithReferences(shared))

	tcs := map[string]struct {
		input string
		refs  []*niceyaml.Source
		want  bool
	}{
		"alias to a light anchor": {
			input: "v: *l1\n",
			refs:  []*niceyaml.Source{shared},
		},
		"alias to a heavy anchor": {
			input: "v: *l7\n",
			refs:  []*niceyaml.Source{shared},
			want:  true,
		},
		"alias through a reference of a reference": {
			input: "v: *m\n",
			refs:  []*niceyaml.Source{lower},
		},
		"heavy alias through a reference of a reference": {
			input: "v: *l7\n",
			refs:  []*niceyaml.Source{lower},
			want:  true,
		},
		"alias among two reference documents": {
			input: "v: *l7\n",
			refs:  []*niceyaml.Source{lower, shared},
			want:  true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// Every document reads the weights that the reference Source
			// keeps, from many goroutines at once.
			var wg sync.WaitGroup

			for range 8 {
				doc := yamltest.FirstDocument(t, tc.input, niceyaml.WithReferences(tc.refs...))

				wg.Go(func() {
					err := aliasing.CheckDecode(doc)
					if tc.want {
						assert.ErrorIs(t, err, aliaslimit.ErrExcessiveAliasing)
					} else {
						assert.NoError(t, err)
					}
				})
			}

			wg.Wait()
		})
	}
}

func TestReferenceWeights_SharedBySources(t *testing.T) {
	t.Parallel()

	shared := niceyaml.NewSourceFromString(yamltest.AliasLevels(2))
	other := niceyaml.NewSourceFromString("m: &m [x]\n")

	weights := func(refs ...*niceyaml.Source) *aliasing.ReferenceWeights {
		doc := yamltest.FirstDocument(t, "v: *l1\n", niceyaml.WithReferences(refs...))

		got := aliasing.StateOf(doc).References()
		require.NotNil(t, got)

		return got
	}

	// Every Source that reads only one reference Source reads the weights
	// that the reference Source keeps.
	first := weights(shared)
	assert.Same(t, first, weights(shared))

	// Every Source with more than one reference Source keeps weights of
	// its own.
	both := weights(shared, other)
	assert.NotSame(t, first, both)
	assert.NotSame(t, both, weights(shared, other))
}

func TestReferenceWeights_Order(t *testing.T) {
	t.Parallel()

	const past = "1:4: aliases expand the document past the alias limit"

	// The verdict of each document, by its text.
	tcs := map[string]struct {
		refs string
		want map[string]string
	}{
		"anchor inside an anchor with an alias of its name": {
			// The anchor a is a loop of one name, and f reaches it. The
			// anchor d inside a does not reach the loop.
			refs: "f: &f [&a [*a, &d [*g]]]\ng: &g [x, y]\n",
			want: map[string]string{"v: *f\n": past, "v: *a\n": past, "v: *d\n": "", "v: *g\n": ""},
		},
		"anchor inside an anchor of a loop of names": {
			refs: "f: &f {x: &x [*y, &d {k: *g}], z: 1}\ny: &y [*x]\ng: &g [p, *h]\nh: &h 1\n",
			want: map[string]string{
				"v: *f\n": past, "v: *x\n": past, "v: *y\n": past, "v: *d\n": "", "v: *g\n": "", "v: *h\n": "",
			},
		},
		"anchors inside each other that an alias inside names": {
			// Each of a, b, and c holds an alias of its own name, and the
			// anchor d inside them does not.
			refs: "a: &a {b: &b {c: &c [*b, *c, *a, &d [*g, *h]]}}\ng: &g [x, y]\nh: &h {k: *g}\n",
			want: map[string]string{
				"v: *a\n": past, "v: *b\n": past, "v: *c\n": past, "v: *d\n": "", "v: *h\n": "",
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			inputs := slices.Sorted(maps.Keys(tc.want))

			// The documents of each run read one reference Source, which
			// weighs each name for the first document that reaches it. A
			// run starts at each document in turn, so each name is the
			// first that the reference Source weighs in one run.
			for first := range inputs {
				shared := niceyaml.NewSourceFromString(tc.refs)

				for i := range inputs {
					input := inputs[(first+i)%len(inputs)]
					doc := yamltest.FirstDocument(t, input, niceyaml.WithReferences(shared))

					got := placed(t, aliasing.CheckDecode(doc))
					assert.Equal(t, tc.want[input], got, "%q after %q", input, inputs[first])
				}
			}
		})
	}
}

func TestReferenceWeights_ReferenceOptions(t *testing.T) {
	t.Parallel()

	// The alias limit of a reference Source does not change the check of
	// a document that reads it.
	for _, limit := range []bool{true, false} {
		t.Run(fmt.Sprintf("reference limit %t", limit), func(t *testing.T) {
			t.Parallel()

			shared := niceyaml.NewSourceFromString(yamltest.AliasLevels(7), niceyaml.WithAliasLimit(limit))
			doc := yamltest.FirstDocument(t, "v: *l7\n", niceyaml.WithReferences(shared))

			err := aliasing.CheckDecode(doc)
			require.ErrorIs(t, err, aliaslimit.ErrExcessiveAliasing)
			require.NotErrorIs(t, err, fault.ErrLimit)
		})
	}
}

// placed returns the message of err, an error of a check, after the line
// and the column of the alias that err reports. The result reads as
// "2:5: aliases expand the document past the alias limit". It is empty
// for a nil error.
func placed(t *testing.T, err error) string {
	t.Helper()

	if err == nil {
		return ""
	}

	require.ErrorIs(t, err, aliaslimit.ErrExcessiveAliasing)

	if expansion, ok := errors.AsType[*aliasing.ExpansionError](err); ok {
		require.ErrorIs(t, err, fault.ErrLimit)

		pos := expansion.Alias.GetToken().Position

		return fmt.Sprintf("%d:%d: %s", pos.Line, pos.Column, err)
	}

	ratio, ok := errors.AsType[*aliasing.RatioError](err)
	require.True(t, ok)
	require.NotNil(t, ratio.Alias)

	pos := ratio.Alias.GetToken().Position

	return fmt.Sprintf("%d:%d: %s", pos.Line, pos.Column, err)
}
