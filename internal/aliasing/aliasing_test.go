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
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

// aliasLevels returns a list whose last level expands to 10^7 scalars,
// since each level lists the level below ten times, with anchors l0
// through l7.
func aliasLevels() string {
	var sb strings.Builder

	sb.WriteString("a:\n  - &l0 [x]\n")

	for level := 1; level <= 7; level++ {
		aliases := strings.Repeat(fmt.Sprintf("*l%d, ", level-1), 10)
		fmt.Fprintf(&sb, "  - &l%d [%s]\n", level, strings.TrimSuffix(aliases, ", "))
	}

	return sb.String()
}

// anchoredAliasLevels returns the list [aliasLevels] returns, but with
// each alias in a block item under an anchor of its own, such as
// `&p1_0` above `*l0`.
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
			input: aliasLevels() + "kind:\n  ? *l7\n  : v\n",
			err:   aliasing.ErrExcessiveAliasing,
		},
		"anchored alias bomb as mapping key": {
			input: anchoredAliasLevels() + "kind:\n  ? *l7\n  : v\n",
			err:   aliasing.ErrExcessiveAliasing,
		},
		"merge key bomb": {
			input: mergeLevels(),
			err:   aliasing.ErrExcessiveAliasing,
		},
		"node holding an alias with a bomb outside it": {
			// The limit applies to the whole document, even where a
			// decode of the node would read only the anchor it needs.
			input: "k: &k a\n" + aliasLevels() + "b:\n  ? *l7\n  : v\nc: [*k]\n",
			path:  paths.Root().Child("c"),
			err:   aliasing.ErrExcessiveAliasing,
		},
		"node without an alias beside a bomb": {
			input: aliasLevels() + "b:\n  ? *l7\n  : v\nc: [x]\n",
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
			err:   aliasing.ErrExcessiveAliasing,
		},
		"scalar aliases in a value": {
			input: longScalar + "k: &k " + flowList("*a", 500) + "\nm: {n: *k}\n",
		},
		"scalar aliases under a string tag": {
			input: longScalar + "k: &k " + flowList("*a", 500) + "\nm: !!str *k\n",
			err:   aliasing.ErrExcessiveAliasing,
		},
		"alias keys to a binary scalar": {
			input: "b: &b !!binary " + base64.StdEncoding.EncodeToString(make([]byte, 2000)) + "\n" +
				"l: " + flowList("{? *b : 1}", 500) + "\n",
			err: aliasing.ErrExcessiveAliasing,
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
			err:   aliasing.ErrExcessiveAliasing,
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

// mergeLevels returns a document whose levels each merge the level below
// ten times, so a decode reads the first level 10^7 times.
func mergeLevels() string {
	var sb strings.Builder

	sb.WriteString("m0: &m0 {a: x}\n")

	for level := 1; level <= 7; level++ {
		aliases := strings.Repeat(fmt.Sprintf("*m%d, ", level-1), 10)
		fmt.Fprintf(&sb, "m%d: &m%d\n  <<: [%s]\n", level, level, strings.TrimSuffix(aliases, ", "))
	}

	return sb.String()
}

func TestExcessive(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		distinct int
		aliased  int
		want     bool
	}{
		"few aliased nodes": {
			distinct: 10,
			aliased:  100,
		},
		"small document": {
			distinct: 1,
			aliased:  998,
		},
		"aliases under the larger share": {
			distinct: 20,
			aliased:  1000,
		},
		"aliases past the larger share": {
			distinct: 5,
			aliased:  1000,
			want:     true,
		},
		"aliases past the smaller share": {
			distinct: 1_000_000,
			aliased:  4_000_000,
			want:     true,
		},
		"aliases under the smaller share": {
			distinct: 4_000_000,
			aliased:  400_000,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, aliasing.Excessive(tc.distinct, tc.aliased))
		})
	}
}

func TestAddCapped(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		a, b int
		want int
	}{
		"under the cap": {a: 1, b: 2, want: 3},
		"at the cap":    {a: aliasing.CountCap - 1, b: 1, want: aliasing.CountCap},
		"past the cap":  {a: aliasing.CountCap, b: aliasing.CountCap, want: aliasing.CountCap},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, aliasing.AddCapped(tc.a, tc.b))
		})
	}
}
