package aliasing_test

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/aliasing"
	"go.jacobcolvin.com/niceyaml/internal/aliaslimit"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

// anchoredAliasLevels returns the list [yamltest.AliasLevels] returns for
// seven levels, but with each alias in a block item under an anchor of
// its own, such as `&p1_0` above `*l0`.
func anchoredAliasLevels() string {
	var sb strings.Builder

	sb.WriteString(stringtest.LinesLF("a:", "  - &l0 [x]"))

	for level := 1; level <= 7; level++ {
		fmt.Fprintf(&sb, "  - &l%d\n", level)

		for i := range 10 {
			fmt.Fprintf(&sb, "    - &p%d_%d\n      *l%d\n", level, i, level-1)
		}
	}

	return sb.String()
}

var (
	// A line that anchors a scalar of 2000 bytes as a.
	longScalar = "a: &a " + strings.Repeat("x", 2000)

	// A binary scalar of 2000 bytes under its tag.
	binaryScalar = "!!binary " + base64.StdEncoding.EncodeToString(make([]byte, 2000))
)

// anchoredInMapping returns the lines of a mapping anchored as base that
// holds a scalar of 2000 bytes under an anchor of its own.
func anchoredInMapping() string {
	return stringtest.JoinLF(
		"base: &base",
		"  name: &n "+strings.Repeat("x", 2000),
		"  x: 1",
	)
}

// thousandEntries returns the lines of 1000 entries of a block mapping,
// each indented under a key.
func thousandEntries() string {
	return strings.TrimSuffix(yamltest.GenerateLines(1000, "  k%d: v"), "\n")
}

func TestCheckDecode(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		err   error
		input string
		path  paths.Path
		opts  []niceyaml.SourceOption
		// Whether the case checks the node through CheckDecodeText in
		// place of CheckDecode.
		text bool
	}{
		"no alias":                  {input: "a: [x, y]\n"},
		"alias to a small sequence": {input: stringtest.LinesLF("s: &s [a, b]", "t: *s")},
		"aliases to a small mapping in a long sequence": {
			// Each alias is a node of the document as well as a read of
			// its anchor, so the 150 aliases add 150 nodes to the 13 the
			// document holds without them.
			input: stringtest.LinesLF(
				"base: &b {os: linux, arch: amd64, go: stable, cgo: false}",
				"matrix: "+yamltest.FlowSequence("*b", 150),
			),
		},
		"aliases to a mapping of 200 keys under keys of their own": {
			input: yamltest.AliasedMapping(200, 150),
		},
		"aliases to a mapping of 1000 keys under keys of their own": {
			// The aliases still repeat far more than the document holds.
			input: yamltest.AliasedMapping(1000, 300),
			err:   aliaslimit.ErrExcessiveAliasing,
		},
		"alias bomb as mapping key": {
			input: yamltest.AliasLevels(7) + yamltest.AliasKey("kind", 7),
			err:   aliaslimit.ErrExcessiveAliasing,
		},
		"anchored alias bomb as mapping key": {
			input: anchoredAliasLevels() + yamltest.AliasKey("kind", 7),
			err:   aliaslimit.ErrExcessiveAliasing,
		},
		"merge key bomb": {
			input: yamltest.MergeLevels(7),
			err:   aliaslimit.ErrExcessiveAliasing,
		},
		"node holding an alias with a bomb outside it": {
			// The limit applies to the whole document, even where a
			// decode of the node would read only the anchor it needs.
			input: "k: &k a\n" + yamltest.AliasLevels(7) + yamltest.AliasKey("b", 7) + "c: [*k]\n",
			path:  paths.Current().Child("c"),
			err:   aliaslimit.ErrExcessiveAliasing,
		},
		"node without an alias beside a bomb": {
			input: yamltest.AliasLevels(7) + yamltest.AliasKey("b", 7) + "c: [x]\n",
			path:  paths.Current().Child("c"),
		},
		"aliases inside their own anchor": {
			input: "x: &x [" + strings.Repeat("a, ", 10) + strings.Repeat("*x, ", 299) + "*x]\n",
		},
		"aliases to an anchor holding an alias to the anchor around it": {
			// The decoder reads *A inside A as null, so each *B reads
			// {c: null}.
			input: stringtest.LinesLF(
				"a: &A",
				"  b: &B {c: *A}",
				thousandEntries(),
				"d: "+yamltest.FlowSequence("*B", 300),
			),
		},
		"same aliases after one inside the anchor around it": {
			// The count does not depend on which alias to B comes first.
			input: stringtest.LinesLF(
				"a: &A",
				"  b: &B {c: *A}",
				thousandEntries(),
				"  e: *B",
				"d: "+yamltest.FlowSequence("*B", 300),
			),
		},
		"aliases to an anchor on an alias to the anchor around it": {
			// P reads null, so each *Q reads [null].
			input: stringtest.LinesLF(
				"a: &A",
				"  p: &P",
				"    *A",
				"  q: &Q [*P]",
				thousandEntries(),
				"d: "+yamltest.FlowSequence("*Q", 300),
			),
		},
		"scalar aliases written out in a key": {
			// The decoder spells the key as text, with a copy of the
			// scalar for each alias.
			input: stringtest.LinesLF(
				longScalar,
				"k: &k "+yamltest.FlowSequence("*a", 500),
				"m: {? *k : 1}",
			),
			err: aliaslimit.ErrExcessiveAliasing,
		},
		"scalar aliases in a value": {
			input: stringtest.LinesLF(
				longScalar,
				"k: &k "+yamltest.FlowSequence("*a", 500),
				"m: {n: *k}",
			),
		},
		"aliases to a tag over an alias to a mapping": {
			// Each *s reads all of k through the alias under the tag.
			input: stringtest.LinesLF(
				"k: &k",
				thousandEntries(),
				"s: &s !foo *k",
				"l: "+yamltest.FlowSequence("*s", 300),
			),
			err: aliaslimit.ErrExcessiveAliasing,
		},
		"a few aliases to a tag over an alias to a mapping": {
			input: stringtest.LinesLF(
				"k: &k",
				thousandEntries(),
				"s: &s !foo *k",
				"l: "+yamltest.FlowSequence("*s", 2),
			),
		},
		"aliases to chained tags over an alias to a mapping": {
			// Each *t reads all of k through *s and then *k.
			input: stringtest.LinesLF(
				"k: &k",
				thousandEntries(),
				"s: &s !foo *k",
				"t: &t !bar *s",
				"l: "+yamltest.FlowSequence("*t", 300),
			),
			err: aliaslimit.ErrExcessiveAliasing,
		},
		"aliases to a string tag over an alias to scalar aliases": {
			// Each *s writes out k as text, with a copy of a for each *a.
			input: stringtest.LinesLF(
				longScalar,
				"k: &k "+yamltest.FlowSequence("*a", 50),
				"s: &s !!str *k",
				"l: "+yamltest.FlowSequence("*s", 300),
			),
			err: aliaslimit.ErrExcessiveAliasing,
		},
		"alias key to a tag over an alias to scalar aliases": {
			// The key decodes to a sequence, so the decoder spells it as
			// text, with a copy of the scalar for each *a.
			input: stringtest.LinesLF(
				longScalar,
				"k: &k "+yamltest.FlowSequence("*a", 500),
				"s: &s !foo *k",
				"m: {? *s : 1}",
			),
			err: aliaslimit.ErrExcessiveAliasing,
		},
		"scalar aliases under a string tag": {
			input: stringtest.LinesLF(
				longScalar,
				"k: &k "+yamltest.FlowSequence("*a", 500),
				"m: !!str *k",
			),
			err: aliaslimit.ErrExcessiveAliasing,
		},
		"aliases to a string-tagged scalar": {
			// A decode into a named string type converts the scalar
			// again at each *s, so each *s copies its text.
			input: stringtest.LinesLF(
				"s: &s !!str "+strings.Repeat("x", 2000),
				"l: "+yamltest.FlowSequence("*s", 500),
			),
			err: aliaslimit.ErrExcessiveAliasing,
		},
		"aliases to an int-tagged scalar": {
			input: stringtest.LinesLF(
				"s: &s !!int "+strings.Repeat("1", 2000),
				"l: "+yamltest.FlowSequence("*s", 500),
			),
			err: aliaslimit.ErrExcessiveAliasing,
		},
		"aliases to a string tag over an alias to a scalar": {
			// The decoder converts a again at each *s, so each *s
			// copies its text.
			input: stringtest.LinesLF(
				longScalar,
				"s: &s !!str *a",
				"l: "+yamltest.FlowSequence("*s", 500),
			),
			err: aliaslimit.ErrExcessiveAliasing,
		},
		"aliases to a custom tag under a secondary tag directive": {
			// After a %TAG directive that redefines the "!!" handle, the
			// decoder writes out the value under every tag as text, so
			// each *s copies its text.
			input: stringtest.LinesLF(
				"%TAG !! tag:example.com,2000:",
				"---",
				"s: &s !foo "+strings.Repeat("x", 2000),
				"l: "+yamltest.FlowSequence("*s", 500),
			),
			err: aliaslimit.ErrExcessiveAliasing,
		},
		"a few aliases to a string-tagged scalar": {
			input: stringtest.LinesLF(
				"s: &s !!str "+strings.Repeat("x", 2000),
				"l: "+yamltest.FlowSequence("*s", 2),
			),
		},
		"aliases to a custom tag over a string-tagged scalar": {
			// The decoder shares the value of s at each *t.
			input: stringtest.LinesLF(
				"s: &s !!str "+strings.Repeat("x", 2000),
				"t: &t !foo *s",
				"l: "+yamltest.FlowSequence("*t", 500),
			),
		},
		"alias keys to a binary scalar": {
			input: stringtest.LinesLF(
				"b: &b "+binaryScalar,
				"l: "+yamltest.FlowSequence("{? *b : 1}", 500),
			),
			err: aliaslimit.ErrExcessiveAliasing,
		},
		"alias keys to a tag over an alias to a binary scalar": {
			// Each key decodes to the bytes under b, so the decoder
			// spells it as text.
			input: stringtest.LinesLF(
				"b: &b "+binaryScalar,
				"s: &s !foo *b",
				"l: "+yamltest.FlowSequence("{? *s : 1}", 500),
			),
			err: aliaslimit.ErrExcessiveAliasing,
		},
		"alias keys to a string tag over an alias to a binary scalar": {
			// The decoder spells the bytes under b as text at each key.
			input: stringtest.LinesLF(
				"b: &b "+binaryScalar,
				"s: &s !!str *b",
				"l: "+yamltest.FlowSequence("{? *s : 1}", 500),
			),
			err: aliaslimit.ErrExcessiveAliasing,
		},
		"a few alias keys to a tag over an alias to a binary scalar": {
			input: stringtest.LinesLF(
				"b: &b "+binaryScalar,
				"s: &s !foo *b",
				"l: "+yamltest.FlowSequence("{? *s : 1}", 2),
			),
		},
		"aliases to a tag over an alias to a binary scalar": {
			// A decode shares the bytes under b between the aliases to
			// s, and the schema validator spells them as text at each.
			input: stringtest.LinesLF(
				"b: &b "+binaryScalar,
				"s: &s !foo *b",
				"l: "+yamltest.FlowSequence("*s", 500),
			),
			err: aliaslimit.ErrExcessiveAliasing,
		},
		"aliases to chained tags over an alias to a binary scalar": {
			input: stringtest.LinesLF(
				"b: &b "+binaryScalar,
				"s: &s !foo *b",
				"t: &t ! *s",
				"l: "+yamltest.FlowSequence("*t", 500),
			),
			err: aliaslimit.ErrExcessiveAliasing,
		},
		"aliases to a binary scalar under another tag and anchor": {
			input: stringtest.LinesLF(
				"s: &s !foo &b "+binaryScalar,
				"l: "+yamltest.FlowSequence("*s", 500),
			),
			err: aliaslimit.ErrExcessiveAliasing,
		},
		"a few aliases to a tag over an alias to a binary scalar": {
			input: stringtest.LinesLF(
				"b: &b "+binaryScalar,
				"s: &s !foo *b",
				"l: "+yamltest.FlowSequence("*s", 2),
			),
		},
		"alias keys to a binary scalar under another tag and anchor": {
			// Each key decodes to the bytes under b, so the decoder
			// spells it as text.
			input: stringtest.LinesLF(
				"s: &s !foo &b "+binaryScalar,
				"l: "+yamltest.FlowSequence("{? *s : 1}", 500),
			),
			err: aliaslimit.ErrExcessiveAliasing,
		},
		"alias keys to a plain scalar": {
			input: stringtest.LinesLF(
				longScalar,
				"l: "+yamltest.FlowSequence("{? *a : 1}", 500),
			),
		},
		"one scalar alias written out in a key": {
			input: stringtest.LinesLF(
				longScalar,
				"k: &k [*a]",
				"m: {? *k : 1}",
			),
		},
		"aliases to a mapping holding an anchored scalar": {
			// The decoder shares the scalar under n at each *base.
			input: stringtest.LinesLF(
				anchoredInMapping(),
				"items: "+yamltest.FlowSequence("*base", 300),
			),
		},
		"aliases to a mapping holding an anchored scalar under a string tag": {
			// Each *base writes out the mapping as text, with a copy
			// of the scalar under n.
			input: stringtest.LinesLF(
				anchoredInMapping(),
				"items: "+yamltest.FlowSequence("!!str *base", 300),
			),
			err: aliaslimit.ErrExcessiveAliasing,
		},
		"alias keys to a mapping holding an anchored scalar": {
			// Each key decodes to a mapping, so the decoder spells it
			// as text, with a copy of the scalar under n.
			input: stringtest.LinesLF(
				anchoredInMapping(),
				"items: "+yamltest.FlowSequence("{? *base : 1}", 300),
			),
			err: aliaslimit.ErrExcessiveAliasing,
		},
		"source with the limit off": {
			input: yamltest.MergeLevels(7),
			opts:  []niceyaml.SourceOption{niceyaml.WithAliasLimit(false)},
		},
		"node of a source with the limit off": {
			input: yamltest.MergeLevels(7),
			path:  paths.Current().Child("m7"),
			opts:  []niceyaml.SourceOption{niceyaml.WithAliasLimit(false)},
		},
		"text with no alias": {
			input: stringtest.LinesLF(longScalar, "kind: x"),
			path:  paths.Current().Child("kind"),
			text:  true,
		},
		"text of many aliases to a long scalar": {
			input: stringtest.LinesLF(longScalar, "kind: "+yamltest.FlowSequence("*a", 500)),
			path:  paths.Current().Child("kind"),
			text:  true,
			err:   aliaslimit.ErrExcessiveAliasing,
		},
		"text of a few aliases to a long scalar": {
			input: stringtest.LinesLF(longScalar, "kind: "+yamltest.FlowSequence("*a", 2)),
			path:  paths.Current().Child("kind"),
			text:  true,
		},
		"text of a source with the limit off": {
			input: stringtest.LinesLF(longScalar, "kind: "+yamltest.FlowSequence("*a", 500)),
			path:  paths.Current().Child("kind"),
			opts:  []niceyaml.SourceOption{niceyaml.WithAliasLimit(false)},
			text:  true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.AtOrSelf(t, yamltest.FirstDocument(t, tc.input, tc.opts...), tc.path)

			check := aliasing.CheckDecode
			if tc.text {
				check = aliasing.CheckDecodeText
			}

			require.ErrorIs(t, check(doc), tc.err)
		})
	}

	t.Run("nil node", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, aliasing.CheckDecode((*niceyaml.Node)(nil)))
		require.NoError(t, aliasing.CheckDecodeText((*niceyaml.Node)(nil)))
	})
}

func TestLimited(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		opts []niceyaml.SourceOption
		want bool
	}{
		"default":   {want: true},
		"limit on":  {opts: []niceyaml.SourceOption{niceyaml.WithAliasLimit(true)}, want: true},
		"limit off": {opts: []niceyaml.SourceOption{niceyaml.WithAliasLimit(false)}},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// The source holds the setting, so every document of it and
			// every node of a document answer alike.
			source := niceyaml.NewSourceFromString(stringtest.LinesLF("a: [x]", "---", "b: 1"), tc.opts...)
			docs := yamltest.Documents(t, source, 2)

			assert.Equal(t, tc.want, aliasing.Limited(docs[0]))
			assert.Equal(t, tc.want, aliasing.Limited(docs[1]))
			assert.Equal(t, tc.want, aliasing.Limited(yamltest.At(t, docs[0], paths.Current().Child("a"))))
		})
	}

	t.Run("nil node", func(t *testing.T) {
		t.Parallel()

		assert.False(t, aliasing.Limited((*niceyaml.Node)(nil)))
	})
}

func TestHoldsReferenceAlias(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		path  paths.Path
		want  bool
	}{
		"no alias":                              {input: "a: [x, y]\n"},
		"alias to an anchor before it":          {input: stringtest.LinesLF("s: &s [a, b]", "t: *s")},
		"alias inside its own anchor":           {input: "x: &x [a, *x]\n"},
		"alias with no anchor":                  {input: "t: *s\n", want: true},
		"alias before the anchor of its name":   {input: stringtest.LinesLF("t: *s", "s: &s [a, b]"), want: true},
		"tagged alias with no anchor":           {input: "t: !foo *s\n", want: true},
		"alias key with no anchor":              {input: "t: {? *s : 1}\n", want: true},
		"merge key alias with no anchor":        {input: stringtest.LinesLF("t:", "  <<: *s"), want: true},
		"alias with no anchor inside an anchor": {input: stringtest.LinesLF("a: &a [*s]", "t: *a"), want: true},
		"node without an alias beside an alias with no anchor": {
			input: stringtest.LinesLF("a: [x]", "t: *s"),
			path:  paths.Current().Child("a"),
			want:  true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.AtOrSelf(t, yamltest.FirstDocument(t, tc.input), tc.path)

			// The document keeps the answer, so a second call returns
			// the same one.
			assert.Equal(t, tc.want, aliasing.HoldsReferenceAlias(doc))
			assert.Equal(t, tc.want, aliasing.HoldsReferenceAlias(doc))
		})
	}

	t.Run("nil node", func(t *testing.T) {
		t.Parallel()

		assert.False(t, aliasing.HoldsReferenceAlias((*niceyaml.Node)(nil)))
	})
}
