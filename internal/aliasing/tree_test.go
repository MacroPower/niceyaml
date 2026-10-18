package aliasing_test

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/aliasing"
	"go.jacobcolvin.com/niceyaml/internal/aliaslimit"
	"go.jacobcolvin.com/niceyaml/internal/fault"
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
		"text past the byte limit read into a value": {
			// A read into a value shares the scalar between the aliases,
			// so the byte limit does not refuse it.
			input: yamltest.AliasedScalar(100, 20),
			path:  paths.Current().Child("l"),
			opts:  []niceyaml.SourceOption{niceyaml.WithByteLimit(1024)},
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

func TestCheckDecodeText(t *testing.T) {
	t.Parallel()

	// Ten levels of flow sequences around ten scalars. Block style writes
	// each scalar on a line of its own, ten levels deeper than the alias.
	deep := strings.Repeat("[", 9) + yamltest.FlowSequence("x", 10) + strings.Repeat("]", 9)

	tcs := map[string]struct {
		// The expansion error, or nil for a node that passes. Its Alias
		// holds the line and the column of the alias, in the form "2:5".
		err   *aliasing.ExpansionError
		alias string
		input string
		opts  []niceyaml.SourceOption
	}{
		"aliases past the byte limit": {
			// Each alias writes the 100 bytes of a on a line of its own,
			// which "  - " and a newline make 105 bytes long, so the tenth
			// alias brings the text to 1050 bytes.
			input: yamltest.AliasedScalar(100, 20),
			opts:  []niceyaml.SourceOption{niceyaml.WithByteLimit(1024)},
			err:   &aliasing.ExpansionError{Limit: 1024},
			alias: "12:5",
		},
		"aliases within the byte limit": {
			input: yamltest.AliasedScalar(100, 9),
			opts:  []niceyaml.SourceOption{niceyaml.WithByteLimit(1024)},
		},
		"aliases with the byte limit off": {
			input: yamltest.AliasedScalar(100, 20),
			opts:  []niceyaml.SourceOption{niceyaml.WithByteLimit(0)},
		},
		"aliases that repeat aliases past the byte limit": {
			// Each *b repeats both copies of a, so the second *b brings
			// the text past 512 bytes.
			input: stringtest.LinesLF(
				"a: &a "+strings.Repeat("x", 100),
				"b: &b [*a, *a]",
				"l: [*b, *b, *b, *b]",
			),
			opts:  []niceyaml.SourceOption{niceyaml.WithByteLimit(512)},
			err:   &aliasing.ExpansionError{Limit: 512},
			alias: "3:9",
		},
		"aliases of deep content past the byte limit": {
			// Each alias repeats 20 nodes. Block style indents the line of
			// each x by 22 spaces, so the fourth alias brings the text past
			// 1 KiB.
			input: stringtest.LinesLF("a: &a "+deep, "l:") + strings.Repeat("  - *a\n", 20),
			opts:  []niceyaml.SourceOption{niceyaml.WithByteLimit(1024)},
			err:   &aliasing.ExpansionError{Limit: 1024},
			alias: "6:5",
		},
		"aliases of spaced content past the byte limit": {
			// A copy of the text of a repeats the 300 spaces between its
			// scalars at each alias, so the fourth alias brings the text
			// past 1 KiB.
			input: stringtest.LinesLF("a: &a [x,"+strings.Repeat(" ", 300)+"x]", "l: [*a, *a, *a, *a]"),
			opts:  []niceyaml.SourceOption{niceyaml.WithByteLimit(1024)},
			err:   &aliasing.ExpansionError{Limit: 1024},
			alias: "2:17",
		},
		"aliases of commented content past the byte limit": {
			// A copy of the text of a repeats its comment at each alias, so
			// the fourth alias brings the text past 1 KiB.
			input: stringtest.LinesLF(
				"a: &a",
				"  - x # "+strings.Repeat("c", 300),
				"  - x",
				"l: [*a, *a, *a, *a]",
			),
			opts:  []niceyaml.SourceOption{niceyaml.WithByteLimit(1024)},
			err:   &aliasing.ExpansionError{Limit: 1024},
			alias: "4:17",
		},
		"aliases on indented lines past the byte limit": {
			// A copy of the text of a indents each line of a to the line of
			// the alias, 60 spaces deep, so the second alias brings the text
			// past 1 KiB.
			input: "a: &a\n" + strings.Repeat("  - x\n", 10) + "l:\n" +
				strings.Repeat(strings.Repeat(" ", 60)+"- *a\n", 4),
			opts:  []niceyaml.SourceOption{niceyaml.WithByteLimit(1024)},
			err:   &aliasing.ExpansionError{Limit: 1024},
			alias: "14:63",
		},
		"alias chain past the depth limit": {
			// The alias *c8 writes out nine levels inside the mapping and
			// the sequence around it, so c9 nests eleven levels.
			input: yamltest.AliasChain(20),
			opts:  []niceyaml.SourceOption{niceyaml.WithDepthLimit(10)},
			err:   &aliasing.ExpansionError{Limit: 10, Depth: true},
			alias: "10:10",
		},
		"alias chain at the depth limit": {
			input: yamltest.AliasChain(8),
			opts:  []niceyaml.SourceOption{niceyaml.WithDepthLimit(10)},
		},
		"alias chain with the depth limit off": {
			input: yamltest.AliasChain(20),
			opts:  []niceyaml.SourceOption{niceyaml.WithDepthLimit(0)},
		},
		"alias chain past the default depth limit": {
			input: yamltest.AliasChain(150),
			err:   &aliasing.ExpansionError{Limit: niceyaml.DefaultDepthLimit, Depth: true},
			alias: "100:12",
		},
		"alias chain with the alias limit off": {
			input: yamltest.AliasChain(20),
			opts: []niceyaml.SourceOption{
				niceyaml.WithDepthLimit(10),
				niceyaml.WithAliasLimit(false),
			},
		},
		"alias deep in the document": {
			// The alias lies three levels deep and adds the level of x.
			input: stringtest.LinesLF("x: &x [1]", "d: {e: {f: *x}}"),
			opts:  []niceyaml.SourceOption{niceyaml.WithDepthLimit(3)},
			err:   &aliasing.ExpansionError{Limit: 3, Depth: true},
			alias: "2:12",
		},
		"alias deep in the document within the depth limit": {
			input: stringtest.LinesLF("x: &x [1]", "d: {e: {f: *x}}"),
			opts:  []niceyaml.SourceOption{niceyaml.WithDepthLimit(4)},
		},
		"alias to a tag over an alias": {
			// The alias *s nests k through the alias under the tag.
			input: stringtest.LinesLF("k: &k [[x]]", "s: &s !foo *k", "l: [*s]"),
			opts:  []niceyaml.SourceOption{niceyaml.WithDepthLimit(3)},
			err:   &aliasing.ExpansionError{Limit: 3, Depth: true},
			alias: "3:5",
		},
		"alias inside its own anchor": {
			// The decoder reads *x as null, so it nests no level.
			input: "x: &x [a, *x]\n",
			opts:  []niceyaml.SourceOption{niceyaml.WithDepthLimit(2)},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input, tc.opts...)

			// The document keeps the result, so a second check returns the
			// same error.
			err := aliasing.CheckDecodeText(doc)
			assert.Equal(t, err, aliasing.CheckDecodeText(doc))

			if tc.err == nil {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, aliaslimit.ErrExcessiveAliasing)
			require.ErrorIs(t, err, fault.ErrLimit)

			got, ok := errors.AsType[*aliasing.ExpansionError](err)
			require.True(t, ok)

			pos := got.Alias.GetToken().Position
			assert.Equal(t, tc.alias, fmt.Sprintf("%d:%d", pos.Line, pos.Column))
			assert.Equal(t, tc.err.Limit, got.Limit)
			assert.Equal(t, tc.err.Depth, got.Depth)

			// A read into a value shares each anchor between its aliases,
			// so no bound applies to it.
			require.NoError(t, aliasing.CheckDecode(doc))
		})
	}
}

func TestExpansionError(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		err  *aliasing.ExpansionError
		want string
	}{
		"byte limit": {
			err:  &aliasing.ExpansionError{Limit: 256 << 10},
			want: "aliases repeat more than 256 KiB of text",
		},
		"depth limit": {
			err:  &aliasing.ExpansionError{Limit: 100, Depth: true},
			want: "aliases nest the document more than 100 levels",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			require.EqualError(t, tc.err, tc.want)
			require.ErrorIs(t, tc.err, aliaslimit.ErrExcessiveAliasing)
			require.ErrorIs(t, tc.err, fault.ErrLimit)
			require.NotErrorIs(t, tc.err, fault.ErrInvalid)
		})
	}
}

func TestRatioError(t *testing.T) {
	t.Parallel()

	// A sequence of 1000 scalars that 200 aliases repeat, so the aliases
	// make up more than 99 percent of the read.
	repeated := stringtest.LinesLF("s: &s "+yamltest.FlowSequence("x", 1000), "l:") +
		strings.Repeat("  - *s\n", 200)

	// A mapping of 1000 entries that 200 merge keys bring in.
	merged := "base: &base\n" + yamltest.GenerateLines(1000, "  k%d: v") + "l:\n" +
		strings.Repeat("  - <<: *base\n", 200)

	tcs := map[string]struct {
		input string
		// The position of the alias that the error names, as line:col.
		want string
		opts []niceyaml.SourceOption
		// Whether the case checks the document through CheckDecodeText in
		// place of CheckDecode.
		text bool
	}{
		"alias that reads the chain": {
			// The use repeats ten times what each alias of the last level
			// repeats.
			input: yamltest.AliasLevels(7) + "use: *l7\n",
			want:  "10:6",
		},
		"chain without a use": {
			// Every alias of the last level repeats the same count, so the
			// first of them wins.
			input: yamltest.AliasLevels(7),
			want:  "9:10",
		},
		"aliases that repeat the same count": {
			input: repeated,
			want:  "3:5",
		},
		"heavier alias after the others": {
			input: repeated + stringtest.LinesLF("t: &t [*s, *s]", "u: *t"),
			want:  "204:4",
		},
		"merge keys": {
			input: merged,
			want:  "1003:9",
		},
		"aliases past the count cap": {
			// The counts of the last level and of the use stop at the cap,
			// so the use wins as the one alias outside every anchor.
			input: yamltest.AliasLevels(11) + "use: *l11\n",
			want:  "14:6",
		},
		"aliases past the count cap without a use": {
			input: yamltest.AliasLevels(11),
			want:  "13:11",
		},
		"text of many aliases to a long scalar": {
			// With the byte limit off, the text of the aliases still makes
			// up too much of the read.
			input: stringtest.LinesLF(longScalar, "kind: "+yamltest.FlowSequence("*a", 500)),
			opts:  []niceyaml.SourceOption{niceyaml.WithByteLimit(0)},
			want:  "2:8",
			text:  true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input, tc.opts...)

			check := aliasing.CheckDecode
			if tc.text {
				check = aliasing.CheckDecodeText
			}

			err := check(doc)
			require.ErrorIs(t, err, aliaslimit.ErrExcessiveAliasing)
			require.NotErrorIs(t, err, fault.ErrLimit)
			require.EqualError(t, err, "aliases expand the document past the alias limit")

			var ratio *aliasing.RatioError

			require.ErrorAs(t, err, &ratio)

			at := ratio.Alias.GetToken().Position
			assert.Equal(t, tc.want, fmt.Sprintf("%d:%d", at.Line, at.Column))
		})
	}
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

func TestPastLimit(t *testing.T) {
	t.Parallel()

	bomb := yamltest.AliasLevels(7) + yamltest.AliasKey("b", 7)

	tcs := map[string]struct {
		input string
		path  paths.Path
		opts  []niceyaml.SourceOption
		want  bool
	}{
		"no alias":               {input: "a: [x, y]\n"},
		"alias within the limit": {input: stringtest.LinesLF("s: &s [a, b]", "t: *s")},
		"alias bomb":             {input: bomb, want: true},
		// The verdict is that of the document, even for a node that does
		// not hold an alias.
		"node without an alias beside a bomb": {
			input: bomb + "c: [x]\n",
			path:  paths.Current().Child("c"),
			want:  true,
		},
		"alias bomb with the limit off": {
			input: bomb,
			opts:  []niceyaml.SourceOption{niceyaml.WithAliasLimit(false)},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input, tc.opts...)
			node := yamltest.AtOrSelf(t, doc, tc.path)

			assert.Equal(t, tc.want, aliasing.PastLimit(node))
			assert.Equal(t, tc.want, aliasing.PastLimit(node), "second call")
		})
	}

	t.Run("nil node", func(t *testing.T) {
		t.Parallel()

		assert.False(t, aliasing.PastLimit((*niceyaml.Node)(nil)))
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
