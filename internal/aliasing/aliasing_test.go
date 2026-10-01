package aliasing_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

	sb.WriteString("a:\n  - &l0 [x]\n")

	for level := 1; level <= 7; level++ {
		fmt.Fprintf(&sb, "  - &l%d\n", level)

		for i := range 10 {
			fmt.Fprintf(&sb, "    - &p%d_%d\n      *l%d\n", level, i, level-1)
		}
	}

	return sb.String()
}

// longScalar anchors a scalar of 2000 bytes as a.
var longScalar = "a: &a " + strings.Repeat("x", 2000) + "\n"

// flowList returns a flow sequence that lists item count times.
func flowList(item string, count int) string {
	return "[" + strings.TrimSuffix(strings.Repeat(item+", ", count), ", ") + "]"
}

// blockEntries returns 1000 entries of a block mapping, each indented
// under a key.
func blockEntries() string {
	var sb strings.Builder

	for i := range 1000 {
		fmt.Fprintf(&sb, "  k%d: v\n", i)
	}

	return sb.String()
}

func TestCheckDecode(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		err   error
		input string
		path  paths.Path
	}{
		"no alias": {
			input: "a: [x, y]\n",
		},
		"alias to a small sequence": {
			input: "s: &s [a, b]\nt: *s\n",
		},
		"alias bomb as mapping key": {
			input: yamltest.AliasLevels(7) + "kind:\n  ? *l7\n  : v\n",
			err:   aliaslimit.ErrExcessiveAliasing,
		},
		"anchored alias bomb as mapping key": {
			input: anchoredAliasLevels() + "kind:\n  ? *l7\n  : v\n",
			err:   aliaslimit.ErrExcessiveAliasing,
		},
		"merge key bomb": {
			input: yamltest.MergeLevels(7),
			err:   aliaslimit.ErrExcessiveAliasing,
		},
		"node holding an alias with a bomb outside it": {
			// The limit applies to the whole document, even where a
			// decode of the node would read only the anchor it needs.
			input: "k: &k a\n" + yamltest.AliasLevels(7) + "b:\n  ? *l7\n  : v\nc: [*k]\n",
			path:  paths.Root().Child("c"),
			err:   aliaslimit.ErrExcessiveAliasing,
		},
		"node without an alias beside a bomb": {
			input: yamltest.AliasLevels(7) + "b:\n  ? *l7\n  : v\nc: [x]\n",
			path:  paths.Root().Child("c"),
		},
		"aliases inside their own anchor": {
			input: "x: &x [" + strings.Repeat("a, ", 10) + strings.Repeat("*x, ", 299) + "*x]\n",
		},
		"aliases to an anchor holding an alias to the anchor around it": {
			// The decoder reads *A inside A as null, so each *B reads
			// {c: null}.
			input: "a: &A\n  b: &B {c: *A}\n" + blockEntries() + "d: " + flowList("*B", 300) + "\n",
		},
		"same aliases after one inside the anchor around it": {
			// The count does not depend on which alias to B comes first.
			input: "a: &A\n  b: &B {c: *A}\n" + blockEntries() + "  e: *B\nd: " + flowList("*B", 300) + "\n",
		},
		"aliases to an anchor on an alias to the anchor around it": {
			// P reads null, so each *Q reads [null].
			input: "a: &A\n  p: &P\n    *A\n  q: &Q [*P]\n" + blockEntries() + "d: " + flowList("*Q", 300) + "\n",
		},
		"scalar aliases written out in a key": {
			// The decoder spells the key as text, with a copy of the
			// scalar for each alias.
			input: longScalar + "k: &k " + flowList("*a", 500) + "\nm: {? *k : 1}\n",
			err:   aliaslimit.ErrExcessiveAliasing,
		},
		"scalar aliases in a value": {
			input: longScalar + "k: &k " + flowList("*a", 500) + "\nm: {n: *k}\n",
		},
		"aliases to a tag over an alias to a mapping": {
			// Each *s reads all of k through the alias under the tag.
			input: "k: &k\n" + blockEntries() + "s: &s !foo *k\nl: " + flowList("*s", 300) + "\n",
			err:   aliaslimit.ErrExcessiveAliasing,
		},
		"a few aliases to a tag over an alias to a mapping": {
			input: "k: &k\n" + blockEntries() + "s: &s !foo *k\nl: " + flowList("*s", 2) + "\n",
		},
		"aliases to chained tags over an alias to a mapping": {
			// Each *t reads all of k through *s and then *k.
			input: "k: &k\n" + blockEntries() + "s: &s !foo *k\nt: &t !bar *s\nl: " + flowList("*t", 300) + "\n",
			err:   aliaslimit.ErrExcessiveAliasing,
		},
		"aliases to a string tag over an alias to scalar aliases": {
			// Each *s writes out k as text, with a copy of a for each *a.
			input: longScalar + "k: &k " + flowList("*a", 50) + "\ns: &s !!str *k\nl: " + flowList("*s", 300) + "\n",
			err:   aliaslimit.ErrExcessiveAliasing,
		},
		"alias key to a tag over an alias to scalar aliases": {
			// The key decodes to a sequence, so the decoder spells it as
			// text, with a copy of the scalar for each *a.
			input: longScalar + "k: &k " + flowList("*a", 500) + "\ns: &s !foo *k\nm: {? *s : 1}\n",
			err:   aliaslimit.ErrExcessiveAliasing,
		},
		"scalar aliases under a string tag": {
			input: longScalar + "k: &k " + flowList("*a", 500) + "\nm: !!str *k\n",
			err:   aliaslimit.ErrExcessiveAliasing,
		},
		"aliases to a string-tagged scalar": {
			// A decode into a named string type converts the scalar
			// again at each *s, so each *s copies its text.
			input: "s: &s !!str " + strings.Repeat("x", 2000) + "\nl: " + flowList("*s", 500) + "\n",
			err:   aliaslimit.ErrExcessiveAliasing,
		},
		"aliases to an int-tagged scalar": {
			input: "s: &s !!int " + strings.Repeat("1", 2000) + "\nl: " + flowList("*s", 500) + "\n",
			err:   aliaslimit.ErrExcessiveAliasing,
		},
		"aliases to a string tag over an alias to a scalar": {
			// The decoder converts a again at each *s, so each *s
			// copies its text.
			input: longScalar + "s: &s !!str *a\nl: " + flowList("*s", 500) + "\n",
			err:   aliaslimit.ErrExcessiveAliasing,
		},
		"aliases to a custom tag under a secondary tag directive": {
			// After a %TAG directive that redefines the "!!" handle, the
			// decoder writes out the value under every tag as text, so
			// each *s copies its text.
			input: "%TAG !! tag:example.com,2000:\n---\ns: &s !foo " + strings.Repeat("x", 2000) +
				"\nl: " + flowList("*s", 500) + "\n",
			err: aliaslimit.ErrExcessiveAliasing,
		},
		"a few aliases to a string-tagged scalar": {
			input: "s: &s !!str " + strings.Repeat("x", 2000) + "\nl: " + flowList("*s", 2) + "\n",
		},
		"aliases to a custom tag over a string-tagged scalar": {
			// The decoder shares the value of s at each *t.
			input: "s: &s !!str " + strings.Repeat("x", 2000) + "\nt: &t !foo *s\nl: " + flowList("*t", 500) + "\n",
		},
		"alias keys to a binary scalar": {
			input: "b: &b !!binary " + base64.StdEncoding.EncodeToString(make([]byte, 2000)) + "\n" +
				"l: " + flowList("{? *b : 1}", 500) + "\n",
			err: aliaslimit.ErrExcessiveAliasing,
		},
		"alias keys to a tag over an alias to a binary scalar": {
			// Each key decodes to the bytes under b, so the decoder
			// spells it as text.
			input: "b: &b !!binary " + base64.StdEncoding.EncodeToString(make([]byte, 2000)) + "\n" +
				"s: &s !foo *b\nl: " + flowList("{? *s : 1}", 500) + "\n",
			err: aliaslimit.ErrExcessiveAliasing,
		},
		"alias keys to a string tag over an alias to a binary scalar": {
			// The decoder spells the bytes under b as text at each key.
			input: "b: &b !!binary " + base64.StdEncoding.EncodeToString(make([]byte, 2000)) + "\n" +
				"s: &s !!str *b\nl: " + flowList("{? *s : 1}", 500) + "\n",
			err: aliaslimit.ErrExcessiveAliasing,
		},
		"a few alias keys to a tag over an alias to a binary scalar": {
			input: "b: &b !!binary " + base64.StdEncoding.EncodeToString(make([]byte, 2000)) + "\n" +
				"s: &s !foo *b\nl: " + flowList("{? *s : 1}", 2) + "\n",
		},
		"alias keys to a plain scalar": {
			input: longScalar + "l: " + flowList("{? *a : 1}", 500) + "\n",
		},
		"one scalar alias written out in a key": {
			input: longScalar + "k: &k [*a]\nm: {? *k : 1}\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input)
			if !tc.path.IsRoot() {
				doc = yamltest.At(t, doc, tc.path)
			}

			err := aliasing.CheckDecode(doc)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				return
			}

			require.NoError(t, err)
		})
	}

	t.Run("nil node", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, aliasing.CheckDecode((*niceyaml.Node)(nil)))
	})
}

func TestCheckDecodeText(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		err   error
		input string
	}{
		"no alias": {
			input: longScalar + "kind: x\n",
		},
		"many aliases to a long scalar": {
			input: longScalar + "kind: " + flowList("*a", 500) + "\n",
			err:   aliaslimit.ErrExcessiveAliasing,
		},
		"a few aliases to a long scalar": {
			input: longScalar + "kind: " + flowList("*a", 2) + "\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.At(t, yamltest.FirstDocument(t, tc.input), paths.Root().Child("kind"))

			err := aliasing.CheckDecodeText(doc)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				return
			}

			require.NoError(t, err)
		})
	}

	t.Run("nil node", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, aliasing.CheckDecodeText((*niceyaml.Node)(nil)))
	})
}

// textValue reads the text of its node.
type textValue struct{}

func (*textValue) UnmarshalText([]byte) error { return nil }

// bytesValue reads the YAML bytes of its node.
type bytesValue struct{}

func (*bytesValue) UnmarshalYAML([]byte) error { return nil }

// bytesContextValue reads the YAML bytes of its node with a context.
type bytesContextValue struct{}

func (*bytesContextValue) UnmarshalYAML(context.Context, []byte) error { return nil }

// textChain refers to itself and holds a text field below the cycle.
type textChain struct {
	Next *textChain
	Text []textValue
}

// plainChain refers to itself and holds no text field.
type plainChain struct {
	Next *plainChain
	Name string
}

// embeddedTime gets UnmarshalText from the time it embeds.
type embeddedTime struct {
	time.Time
}

func TestDecodesText(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		typ  reflect.Type
		want bool
	}{
		"text unmarshaler": {
			typ:  reflect.TypeFor[textValue](),
			want: true,
		},
		"bytes unmarshaler": {
			typ:  reflect.TypeFor[bytesValue](),
			want: true,
		},
		"bytes unmarshaler with a context": {
			typ:  reflect.TypeFor[bytesContextValue](),
			want: true,
		},
		"pointer": {
			typ:  reflect.TypeFor[*textValue](),
			want: true,
		},
		"struct field": {
			typ: reflect.TypeFor[struct {
				Name string
				Addr netip.Prefix
			}](),
			want: true,
		},
		"array element": {
			typ:  reflect.TypeFor[[2]bytesValue](),
			want: true,
		},
		"map key": {
			typ:  reflect.TypeFor[map[textValue]int](),
			want: true,
		},
		"map value": {
			typ:  reflect.TypeFor[map[string][]textValue](),
			want: true,
		},
		"recursive type with a text field": {
			typ:  reflect.TypeFor[textChain](),
			want: true,
		},
		"embedded time": {
			typ:  reflect.TypeFor[embeddedTime](),
			want: true,
		},
		"time": {
			typ: reflect.TypeFor[time.Time](),
		},
		"struct with time fields": {
			typ: reflect.TypeFor[struct {
				At      time.Time
				Expires *time.Time
				Timeout time.Duration
			}](),
		},
		"recursive type without a text field": {
			typ: reflect.TypeFor[plainChain](),
		},
		"plain types": {
			typ: reflect.TypeFor[map[string][]string](),
		},
		"interface": {
			typ: reflect.TypeFor[any](),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, aliasing.DecodesText(tc.typ))
			assert.Equal(t, tc.want, aliasing.DecodesText(tc.typ), "a second call gives the same answer")
		})
	}
}
