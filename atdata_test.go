package niceyaml_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/jsonschema"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/schema"
)

var (
	// The documents the locator tests share.
	// The key 0x10 sets the member 16, and items holds an element.
	respelledDoc = stringtest.LinesLF(
		"ports:",
		"  0x10:",
		`    name: ""`,
		"items:",
		`  - name: ""`,
	)
	// The mapping under 0x10 holds no name.
	lackingDoc = stringtest.LinesLF(
		"ports:",
		"  0x10:",
		"    proto: tcp",
	)
	// The key 3.10 sets the member 3.1, so no member has the name 3.10.
	pythonDoc = stringtest.LinesLF(
		"python:",
		"  3.10:",
		"    image: a",
	)
	// Both keys of m set the member 16, and a decode keeps b.
	sameNameDoc = stringtest.LinesLF(
		"m:",
		"  16: aaaa",
		"  0x10: b",
	)
	// The merge key sets the member 16 to x. The quoted key sets the
	// member 0x10, and a path through 0x10 selects that entry.
	hiddenDoc = stringtest.LinesLF(
		"user:",
		"  <<: {0x10: x}",
		`  "0x10": 1`,
	)
	// The merge key of m sets the member true to b, the last value b1
	// gives that name.
	mergeDoc = stringtest.LinesLF(
		"b0: &b0",
		"  True: xxxx",
		"b1: &b1",
		"  <<: *b0",
		"  true: aaaa",
		"  True: b",
		"m:",
		"  <<: *b1",
	)
	// A sequence, a mapping with keys of digits, an alias to the
	// sequence, a sequence in a sequence, a scalar, and a null.
	shapesDoc = stringtest.LinesLF(
		"seq: &s",
		"  - a",
		"  - b",
		"map:",
		"  0: x",
		"  1: y",
		"al: *s",
		"nested:",
		"  - - p",
		"    - q",
		"scalar: v",
		"none: ~",
	)
	// The anchors a document of the tests refers to through
	// [niceyaml.WithReferences].
	referenceDoc = stringtest.LinesLF(
		"defaults: &defaults",
		"  timeout: 0",
		"  3.10: r",
		"list: &list [u, {0x10: v}]",
	)
	// The mapping server merges an anchor of referenceDoc, and items is
	// an alias to one.
	referringDoc = stringtest.LinesLF(
		"server:",
		"  <<: *defaults",
		"  port: 0x10",
		"  3.10: mine",
		"items: *list",
	)
	// The tagged alias key of python has no name the locator can tell, so
	// it may set a member of any name, such as one a key before it sets.
	unnamedKeyDoc = stringtest.LinesLF(
		"k: &k 0x10",
		"python:",
		"  3.10: {image: a}",
		"  a: 1",
		"  !!str *k : v",
		"  b: 2",
	)
	// The key a/b~c holds both characters a JSON Pointer escapes, and the
	// empty key takes an empty name.
	pointerDoc = stringtest.LinesLF(
		`"a/b~c":`,
		`  "": x`,
		"items:",
		"  - 0x10: y",
	)
)

// locatorDoc returns the first document of input, from a source named
// f.yaml that resolves the anchors of [referenceDoc].
func locatorDoc(tb testing.TB, input string) *niceyaml.Node {
	tb.Helper()

	return yamltest.FirstDocument(tb, input, niceyaml.WithName("f.yaml"), yamltest.References(referenceDoc))
}

// located binds an error with the location opt gives it through n. It
// returns the message of the binding, which puts the position and the
// path in front. It also returns the path of the node the error binds at
// when the document holds no value at its own path, as
// [niceyaml.SourceError.Nearest] reports it.
func located(tb testing.TB, n *niceyaml.Node, opt niceyaml.ErrorOption) (string, string) {
	tb.Helper()

	bound := yamltest.RequireSourceError(tb, n.Bind(niceyaml.NewError("x", opt)))

	near := ""
	if path, ok := bound.Nearest(); ok {
		near = path.String()
	}

	return bound.Error(), near
}

// pointerNames returns the names in a JSON Pointer, as the documentation
// of [niceyaml.AtData] splits one.
func pointerNames(pointer string) []string {
	if pointer == "" {
		return nil
	}

	unescape := strings.NewReplacer("~1", "/", "~0", "~")

	parts := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
	for i, part := range parts {
		parts[i] = unescape.Replace(part)
	}

	return parts
}

func ExampleAtData() {
	doc, err := niceyaml.NewSourceFromString(
		stringtest.LinesLF(
			"ports:",
			"  0x10:",
			`    name: ""`,
			"items:",
			`  - name: ""`,
		),
		niceyaml.WithName("cfg.yaml"),
	).Document()
	if err != nil {
		log.Fatal(err)
	}

	// A check of the decoded data reports each finding as a JSON Pointer.
	findings := []string{"/ports/16/name", "/items/0/name"}

	for _, pointer := range findings {
		fmt.Println(doc.Bind(niceyaml.NewError("must not be empty", niceyaml.AtData(pointerNames(pointer)...))))
	}

	// Output:
	// cfg.yaml:3:11: $.ports.0x10.name: must not be empty
	// cfg.yaml:5:11: $.items[0].name: must not be empty
}

func TestAtData(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		names []string
		// Whether the names go through AtDataKey, which locates the key of
		// the member they name, rather than through AtData.
		key  bool
		want string
		// The path of the node the error binds at, for names the document
		// holds no value for: the nearest node the document holds, or an
		// alias the document cannot follow.
		wantNear string
	}{
		"key the decoder respells": {
			input: respelledDoc,
			names: []string{"ports", "16", "name"},
			want:  "f.yaml:3:11: $.ports.0x10.name: x",
		},
		"element of a sequence": {
			input: respelledDoc,
			names: []string{"items", "0", "name"},
			want:  "f.yaml:5:11: $.items[0].name: x",
		},
		"no names": {
			input: respelledDoc,
			want:  "f.yaml:1:1: $: x",
		},
		"float key with a trailing zero": {
			input: pythonDoc,
			names: []string{"python", "3.1", "image"},
			want:  "f.yaml:3:12: $.python.'3.10'.image: x",
		},
		"last of two keys with one name": {
			input: sameNameDoc,
			names: []string{"m", "16"},
			want:  "f.yaml:3:9: $.m.0x10: x",
		},
		"last merge source with the name": {
			input: mergeDoc,
			names: []string{"m", "true"},
			want:  "f.yaml:6:9: $.m.True: x",
		},
		"member no path selects": {
			input: hiddenDoc,
			names: []string{"user", "16"},
			want:  "f.yaml:2:14: $.user.16: x",
		},
		"member beside one no path selects": {
			input: hiddenDoc,
			names: []string{"user", "0x10"},
			want:  "f.yaml:3:11: $.user.0x10: x",
		},
		"element of a root sequence": {
			input: `- name: ""` + "\n",
			names: []string{"0", "name"},
			want:  "f.yaml:1:9: $[0].name: x",
		},
		"element behind an alias": {
			input: shapesDoc,
			names: []string{"al", "1"},
			want:  "f.yaml:3:5: $.al[1]: x",
		},
		"element of an element": {
			input: shapesDoc,
			names: []string{"nested", "0", "1"},
			want:  "f.yaml:10:7: $.nested[0][1]: x",
		},
		"digits in a mapping name a member": {
			input: shapesDoc,
			names: []string{"map", "1"},
			want:  "f.yaml:6:6: $.map.1: x",
		},

		// A member the data lacks.
		"member a mapping lacks": {
			input:    lackingDoc,
			names:    []string{"ports", "16", "name"},
			want:     "f.yaml:2:3: $.ports.0x10.name: x",
			wantNear: "$.ports.0x10",
		},
		"names below a member a mapping lacks": {
			input:    lackingDoc,
			names:    []string{"ports", "16", "tls", "0"},
			want:     "f.yaml:2:3: $.ports.0x10.tls.0: x",
			wantNear: "$.ports.0x10",
		},
		"member the root lacks": {
			input:    lackingDoc,
			names:    []string{"nope", "x"},
			want:     "f.yaml:1:1: $.nope.x: x",
			wantNear: "$",
		},
		"member a null lacks": {
			input:    shapesDoc,
			names:    []string{"none", "x"},
			want:     "f.yaml:12:1: $.none.x: x",
			wantNear: "$.none",
		},
		"name a key spells for another member": {
			input: pythonDoc,
			names: []string{"python", "3.10"},
			want:  "f.yaml:1:1: $.python.'3.10': x",
		},
		"names below a name a key spells for another member": {
			input: pythonDoc,
			names: []string{"python", "3.10", "image"},
			want:  "f.yaml:1:1: $.python.'3.10'.image: x",
		},
		"member lacking below one no path selects": {
			input: stringtest.LinesLF("user:", "  <<: {0x10: {a: b}}", `  "0x10": 1`),
			names: []string{"user", "16", "name"},
			want:  "f.yaml:2:8: $.user.16.name: x",
		},
		"name of a decode into map[int]": {
			input: stringtest.LinesLF(
				"m:",
				"  1e3:",
				`    n: ""`,
			),
			names:    []string{"m", "1000", "n"},
			want:     "f.yaml:1:1: $.m.1000.n: x",
			wantNear: "$.m",
		},
		"name of a decode into map[string]": {
			input: stringtest.LinesLF(
				"m:",
				"  ~:",
				`    n: ""`,
			),
			names:    []string{"m", "", "n"},
			want:     "f.yaml:1:1: $.m.''.n: x",
			wantNear: "$.m",
		},

		// A name that is no index of its sequence binds at the sequence,
		// and a name below a scalar binds at the scalar.
		"index past the end": {
			input:    shapesDoc,
			names:    []string{"seq", "9"},
			want:     "f.yaml:1:1: $.seq[9]: x",
			wantNear: "$.seq",
		},
		"names below an index past the end": {
			input:    shapesDoc,
			names:    []string{"seq", "9", "x"},
			want:     "f.yaml:1:1: $.seq[9].x: x",
			wantNear: "$.seq",
		},
		"index with a leading zero": {
			input:    shapesDoc,
			names:    []string{"seq", "01"},
			want:     "f.yaml:1:1: $.seq.01: x",
			wantNear: "$.seq",
		},
		"index with a sign": {
			input:    shapesDoc,
			names:    []string{"seq", "+1"},
			want:     "f.yaml:1:1: $.seq.+1: x",
			wantNear: "$.seq",
		},
		"index too large for an int": {
			input:    shapesDoc,
			names:    []string{"seq", "99999999999999999999"},
			want:     "f.yaml:1:1: $.seq.99999999999999999999: x",
			wantNear: "$.seq",
		},
		"member name in a sequence": {
			input:    shapesDoc,
			names:    []string{"seq", "name"},
			want:     "f.yaml:1:1: $.seq.name: x",
			wantNear: "$.seq",
		},
		"name below a scalar": {
			input:    shapesDoc,
			names:    []string{"scalar", "x"},
			want:     "f.yaml:11:9: $.scalar.x: x",
			wantNear: "$.scalar",
		},
		"index past the end below a member no path selects": {
			input: stringtest.LinesLF("user:", "  <<: {0x10: [a]}", `  "0x10": 1`),
			names: []string{"user", "16", "1"},
			want:  "f.yaml:2:8: $.user.16[1]: x",
		},

		// A value only a reference document holds, which the locator
		// cannot follow. A path that enters the alias binds there.
		"member after a merge of a reference document": {
			input: referringDoc,
			names: []string{"server", "port"},
			want:  "f.yaml:3:9: $.server.port: x",
		},
		"member a merge of a reference document sets": {
			input:    referringDoc,
			names:    []string{"server", "timeout"},
			want:     "f.yaml:2:7: $.server.timeout: x",
			wantNear: "$.server",
		},
		"name a key spells beside a merge of a reference document": {
			input: referringDoc,
			names: []string{"server", "3.10"},
			want:  "f.yaml:1:1: $.server.'3.10': x",
		},
		"member below an alias to a reference document": {
			input:    referringDoc,
			names:    []string{"items", "1", "16"},
			want:     "f.yaml:5:8: $.items.1.16: x",
			wantNear: "$.items",
		},

		// A mapping with a key the locator cannot name.
		"member before a key with no name": {
			input: unnamedKeyDoc,
			names: []string{"python", "a"},
			want:  "f.yaml:4:6: $.python.a: x",
		},
		"member after a key with no name": {
			input: unnamedKeyDoc,
			names: []string{"python", "b"},
			want:  "f.yaml:6:6: $.python.b: x",
		},
		"name a key spells before a key with no name": {
			input: unnamedKeyDoc,
			names: []string{"python", "3.10"},
			want:  "f.yaml:2:1: $.python.'3.10': x",
		},

		// A document with no mapping at its root.
		"member a null root lacks": {
			input:    "~\n",
			names:    []string{"a"},
			want:     "f.yaml:1:1: $.a: x",
			wantNear: "$",
		},
		"name below a scalar root": {
			input:    "scalar\n",
			names:    []string{"a", "0"},
			want:     "f.yaml:1:1: $.a.0: x",
			wantNear: "$",
		},
		"empty document": {
			input: "",
			names: []string{"a", "0"},
			want:  "f.yaml: $.a.0: x",
		},
		"document of a comment alone": {
			input: "# empty\n",
			names: []string{"a"},
			want:  "f.yaml: $.a: x",
		},

		// The names of a JSON Pointer.
		"pointer with an escaped slash and tilde": {
			input: pointerDoc,
			names: pointerNames("/a~1b~0c"),
			want:  "f.yaml:1:1: $.'a/b~c': x",
		},
		"pointer with an empty name": {
			input: pointerDoc,
			names: pointerNames("/a~1b~0c/"),
			want:  "f.yaml:2:7: $.'a/b~c'.'': x",
		},
		"pointer with an index and a respelled key": {
			input: pointerDoc,
			names: pointerNames("/items/0/16"),
			want:  "f.yaml:4:11: $.items[0].0x10: x",
		},
		"pointer with an escape of an escape": {
			input:    pointerDoc,
			names:    pointerNames("/~01"),
			want:     "f.yaml:1:1: $.'~1': x",
			wantNear: "$",
		},

		// The key of the member the names lead to.
		"AtDataKey: key the decoder respells": {
			key:   true,
			input: respelledDoc,
			names: []string{"ports", "16"},
			want:  "f.yaml:2:3: $.ports.0x10~: x",
		},
		"AtDataKey: explicit key": {
			key:   true,
			input: stringtest.LinesLF("? 0x10", ": x"),
			names: []string{"16"},
			want:  "f.yaml:1:3: $.0x10~: x",
		},
		"AtDataKey: key of a merge source": {
			key:   true,
			input: mergeDoc,
			names: []string{"m", "true"},
			want:  "f.yaml:6:3: $.m.True~: x",
		},
		"AtDataKey: key no path selects": {
			key:   true,
			input: hiddenDoc,
			names: []string{"user", "16"},
			want:  "f.yaml:2:8: $.user.16~: x",
		},
		"AtDataKey: element has no key": {
			key:   true,
			input: respelledDoc,
			names: []string{"items", "0"},
			want:  "f.yaml:5:3: $.items[0]~: x",
		},
		"AtDataKey: no names": {
			key:   true,
			input: respelledDoc,
			want:  "f.yaml:1:1: $~: x",
		},
		"AtDataKey: member a mapping lacks": {
			key:      true,
			input:    lackingDoc,
			names:    []string{"ports", "16", "name"},
			want:     "f.yaml:2:3: $.ports.0x10.name: x",
			wantNear: "$.ports.0x10",
		},
		"AtDataKey: name a key spells for another member": {
			key:   true,
			input: pythonDoc,
			names: []string{"python", "3.10"},
			want:  "f.yaml:1:1: $.python.'3.10': x",
		},
		"AtDataKey: index past the end": {
			key:      true,
			input:    shapesDoc,
			names:    []string{"seq", "9"},
			want:     "f.yaml:1:1: $.seq[9]: x",
			wantNear: "$.seq",
		},
		"AtDataKey: member below an alias to a reference document": {
			key:      true,
			input:    referringDoc,
			names:    []string{"items", "1", "16"},
			want:     "f.yaml:5:8: $.items.1.16~: x",
			wantNear: "$.items",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := locatorDoc(t, tc.input)

			locate := niceyaml.AtData
			if tc.key {
				locate = niceyaml.AtDataKey
			}

			got, gotNear := located(t, doc, locate(tc.names...))

			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.wantNear, gotNear)
		})
	}
}

func TestAtData_Options(t *testing.T) {
	t.Parallel()

	doc := locatorDoc(t, hiddenDoc)

	other := niceyaml.AtPath(paths.Doc().Child("other"))
	marked := niceyaml.AtRange(yamltest.LineRange(0, 0, 4))

	tcs := map[string]struct {
		want string
		opts []niceyaml.ErrorOption
	}{
		"replaces a path set before it": {
			opts: []niceyaml.ErrorOption{other, niceyaml.AtData("user", "0x10")},
			want: "f.yaml:3:11: $.user.0x10: x",
		},
		"a later path replaces it": {
			opts: []niceyaml.ErrorOption{niceyaml.AtData("user", "0x10"), niceyaml.AtPath(paths.Doc().Child("user"))},
			want: "f.yaml:1:1: $.user: x",
		},
		"keeps a range set before it where the path selects the value": {
			opts: []niceyaml.ErrorOption{marked, niceyaml.AtData("user", "0x10")},
			want: "f.yaml:1:1: $.user.0x10: x",
		},
		"keeps a range set before it where no path selects the value": {
			opts: []niceyaml.ErrorOption{marked, niceyaml.AtData("user", "16")},
			want: "f.yaml:1:1: $.user.16: x",
		},
		"binds at the value where no path selects it": {
			opts: []niceyaml.ErrorOption{niceyaml.AtData("user", "16")},
			want: "f.yaml:2:14: $.user.16: x",
		},
		"a later range replaces its position": {
			opts: []niceyaml.ErrorOption{niceyaml.AtData("user", "16"), marked},
			want: "f.yaml:1:1: $.user.16: x",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := doc.Bind(niceyaml.NewError("x", tc.opts...))

			require.EqualError(t, err, tc.want)
		})
	}
}

func TestAtData_Names(t *testing.T) {
	t.Parallel()

	// The names a binding reads are those go-yaml gives the keys of a
	// decode into any, so each key here pins one of them.
	input := stringtest.LinesLF(
		"0x10: a",
		"3.10: b",
		"True: c",
		"~: d",
		"1234567.0: e",
		"!!binary aGk=: f",
		"1e3: g",
		`"0x20": h`,
	)
	doc := locatorDoc(t, input)

	// Each name, with the line of the key that sets its member.
	want := map[string]int{
		"16":           1,
		"3.1":          2,
		"true":         3,
		"null":         4,
		"1.234567e+06": 5,
		"[104 105]":    6,
		"1e3":          7,
		"0x20":         8,
	}

	data, err := doc.Decode[any](t.Context())
	require.NoError(t, err)

	members, ok := data.(map[string]any)
	require.True(t, ok, "decode into any gave %T", data)

	assert.ElementsMatch(t, slices.Collect(maps.Keys(want)), slices.Collect(maps.Keys(members)))

	for name, line := range want {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			bound := yamltest.RequireSourceError(t, doc.Bind(niceyaml.NewError("x", niceyaml.AtDataKey(name))))
			assert.Equal(t, line-1, yamltest.PositionOf(t, bound).Line)
		})
	}

	// A decode into another type names some of those keys another way,
	// so the names of such a decode lead nowhere.
	t.Run("typed decodes", func(t *testing.T) {
		t.Parallel()

		ints, err := locatorDoc(t, "1e3: a\n").Decode[map[int]string](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[int]string{1000: "a"}, ints)

		texts, err := locatorDoc(t, "~: a\n").Decode[map[string]string](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"": "a"}, texts)
	})
}

func TestAtData_Scope(t *testing.T) {
	t.Parallel()

	doc := locatorDoc(t, stringtest.LinesLF(
		`- name: ""`,
		"- spec:",
		"    ports:",
		"      0x10:",
		`        name: ""`,
	))
	spec := yamltest.At(t, doc, paths.Doc().Index(1).Child("spec"))

	// Before a binding, the error reports the names as an `@` path.
	err := niceyaml.NewError("x", niceyaml.AtData("ports", "16", "name"))
	assert.Equal(t, "@.ports.16.name", yamltest.PathOf(t, err).String())

	const want = "f.yaml:5:15: $[1].spec.ports.0x10.name: x"

	// The names read from the Node that binds the error.
	require.EqualError(t, spec.Bind(err), want)

	// A base goes in front of the names, which then read from the value
	// at the base.
	require.EqualError(t, doc.Bind(niceyaml.Rebase(err, paths.Doc().Index(1).Child("spec"))), want)
	require.EqualError(t, doc.Bind(niceyaml.Rebase(err, paths.Current().Index(1).Child("spec"))), want)
	require.EqualError(t, spec.Bind(niceyaml.Rebase(err, paths.Current())), want)

	t.Run("names of the document miss from the scope", func(t *testing.T) {
		t.Parallel()

		got, _ := located(t, spec, niceyaml.AtData("1", "spec", "ports"))

		assert.Equal(t, "f.yaml:2:3: $[1].spec.1.spec.ports: x", got)
	})

	t.Run("names of the root", func(t *testing.T) {
		t.Parallel()

		got, _ := located(t, doc, niceyaml.AtData("1", "spec", "ports", "16"))

		assert.Equal(t, "f.yaml:4:7: $[1].spec.ports.0x10: x", got)
	})

	t.Run("scope behind an alias", func(t *testing.T) {
		t.Parallel()

		aliased := locatorDoc(t, stringtest.LinesLF("base: &base", "  0x10: x", "al: *base"))
		al := yamltest.At(t, aliased, paths.Doc().Child("al"))

		got, _ := located(t, al, niceyaml.AtData("16"))

		assert.Equal(t, "f.yaml:2:9: $.al.0x10: x", got)
	})

	t.Run("a source binds through its one document", func(t *testing.T) {
		t.Parallel()

		err := niceyaml.NewError("x", niceyaml.AtData("ports", "16"))

		src := niceyaml.NewSourceFromString(stringtest.LinesLF("ports:", "  0x10: a"), niceyaml.WithName("f.yaml"))
		require.EqualError(t, src.Bind(err), "f.yaml:2:9: $.ports.0x10: x")

		// The names of a source of several documents do not say which
		// document they read from, so they keep the form the check wrote.
		multi := niceyaml.NewSourceFromString(stringtest.LinesLF("ports:", "  0x10: a", "---", "b: 1"),
			niceyaml.WithName("f.yaml"))
		require.EqualError(t, multi.Bind(err), "f.yaml: $.ports.16: x")
	})

	t.Run("base the document does not hold", func(t *testing.T) {
		t.Parallel()

		// The names cannot read from a value the document lacks, so the
		// error binds where the path of the names binds.
		err := niceyaml.Rebase(niceyaml.NewError("x", niceyaml.AtData("16")), paths.Doc().Child("nope"))
		bound := yamltest.RequireSourceError(t, doc.Bind(err))

		require.EqualError(t, bound, "f.yaml:1:3: $.nope.16: x")

		near, ok := bound.Nearest()
		require.True(t, ok)
		assert.Equal(t, "$", near.String())
	})
}

func TestAtData_SyntaxError(t *testing.T) {
	t.Parallel()

	doc := yamltest.UnparsedDocument(t, "a: [1\n", niceyaml.WithName("f.yaml"))
	require.ErrorIs(t, doc.Err(), niceyaml.ErrSyntax)

	bound := yamltest.RequireSourceError(t, doc.Bind(niceyaml.NewError("x", niceyaml.AtData("a", "0"))))
	require.EqualError(t, bound, "f.yaml: $.a.0: x")
	require.ErrorIs(t, bound.Unresolved(), niceyaml.ErrPathNeedsDocument)

	path, ok := doc.DataLocator().Path("a", "0")
	assert.False(t, ok)
	assert.Equal(t, "$.a.0", path.String())
}

// schemaLocations returns where each violation of v in doc binds: its
// position, its path, and the mapping it binds at for a member the
// mapping lacks.
func schemaLocations(t *testing.T, doc *niceyaml.Node, v *jsonschema.Validator) []string {
	t.Helper()

	err := doc.Validate(t.Context(), schema.FromJSONSchema(v))
	require.Error(t, err)
	require.True(t, niceyaml.IsInvalid(err), "validation did not run: %v", err)

	var got []string

	// The summary of several violations is no violation, so Problems
	// passes over it.
	for problem := range niceyaml.Problems(err) {
		got = append(got, describeLocation(problem.Bound))
	}

	slices.Sort(got)

	return got
}

// locatorLocations returns where doc binds an error from [niceyaml.AtData]
// for each failure v finds in the data of doc. The error takes the
// instance location of the failure as names. A failure about a missing
// member takes the name of that member too. One about a key, which
// additionalProperties and propertyNames report, goes through
// [niceyaml.AtDataKey].
func locatorLocations(t *testing.T, doc *niceyaml.Node, v *jsonschema.Validator) []string {
	t.Helper()

	data, err := doc.Decode[any](t.Context())
	require.NoError(t, err)

	var failure *jsonschema.ValidationError

	require.ErrorAs(t, v.Validate(t.Context(), data), &failure)

	var errs []error

	for _, leaf := range failure.Leaves() {
		var names []string

		for _, seg := range leaf.InstanceSegments() {
			if seg.IsIndex {
				names = append(names, strconv.Itoa(seg.Index))
			} else {
				names = append(names, seg.Key)
			}
		}

		var opt niceyaml.ErrorOption

		quoted, required := strings.CutPrefix(leaf.Message, "missing required property ")

		switch {
		case required:
			missing, err := strconv.Unquote(quoted)
			require.NoError(t, err)

			opt = niceyaml.AtData(append(names, missing)...)

		case leaf.Keyword == jsonschema.KeywordAdditionalProperties,
			leaf.Keyword == jsonschema.KeywordPropertyNames:
			opt = niceyaml.AtDataKey(names...)

		default:
			opt = niceyaml.AtData(names...)
		}

		errs = append(errs, niceyaml.NewError("x", opt))
	}

	// One bind translates the names of every failure, as the schema does,
	// so both read the merge sources of a document the same number of
	// times.
	var got []string

	for problem := range niceyaml.Problems(doc.Bind(errors.Join(errs...))) {
		got = append(got, describeLocation(problem.Bound))
	}

	slices.Sort(got)

	return got
}

// describeLocation returns where bound binds: its position, its path,
// and the mapping it binds at when its path names a key the document
// leaves out.
func describeLocation(bound *niceyaml.SourceError) string {
	at := "-"
	if pos, ok := bound.Position(); ok {
		at = fmt.Sprintf("%d:%d", pos.Line+1, pos.Col+1)
	}

	path, _ := bound.Path()

	near := "-"
	if nearest, ok := bound.Nearest(); ok {
		near = nearest.String()
	}

	return fmt.Sprintf("%s %s near %s", at, path, near)
}

func TestAtData_Schema(t *testing.T) {
	t.Parallel()

	// Each schema fails many values of a document: every scalar, every
	// key, or every mapping, for members it lacks. Some of those members
	// have the name another key is spelled with.
	schemas := map[string]string{
		"every scalar": `{
			"$ref": "#/$defs/node",
			"$defs": {"node": {
				"if": {"type": ["object", "array"]},
				"then": {
					"additionalProperties": {"$ref": "#/$defs/node"},
					"items": {"$ref": "#/$defs/node"}
				},
				"else": false
			}}
		}`,
		"every key": `{
			"$ref": "#/$defs/node",
			"$defs": {"node": {
				"propertyNames": false,
				"additionalProperties": {"$ref": "#/$defs/node"},
				"items": {"$ref": "#/$defs/node"}
			}}
		}`,
		"every mapping": `{
			"$ref": "#/$defs/node",
			"$defs": {"node": {
				"required": ["zz", "0x10", "16", "3.10", "True", "~", "0", "name"],
				"additionalProperties": {"$ref": "#/$defs/node"},
				"items": {"$ref": "#/$defs/node"}
			}}
		}`,
	}

	inputs := map[string]string{
		"respelled keys":           respelledDoc,
		"missing member":           lackingDoc,
		"key spelled as no member": pythonDoc,
		"two keys with one name":   sameNameDoc,
		"member no path selects":   hiddenDoc,
		"merge precedence":         mergeDoc,
		"sequences and scalars":    shapesDoc,
		"key with no name":         unnamedKeyDoc,
		"alias key": stringtest.LinesLF(
			"k: &k 0x10",
			"m:",
			"  *k : {0x20: v}",
			`  "16": w`,
		),
		"merge sources in a list": stringtest.LinesLF(
			"a: &a {0x10: 1, b: 2}",
			"b: &b {16: 3, 0x10: 4}",
			"m:",
			"  <<: [*a, *b]",
			"  b: 5",
			"  <<: *a",
		),
		"merge key after its keys": stringtest.LinesLF(
			"base: &base {0x10: {a: 1}}",
			"m:",
			"  0x10: {b: 2}",
			"  0o20: {c: 3}",
			"  <<: *base",
		),
		"keys below one no path selects": stringtest.LinesLF(
			"user:",
			"  <<: {0x10: {0x20: [x, {0x30: y}]}}",
			`  "0x10": 1`,
		),
		"aliased mapping in a sequence": stringtest.LinesLF(
			"base: &base {0x10: x, 3.10: y}",
			"seq:",
			"  - *base",
			"  - [*base, {True: z}]",
		),
		"null and bool keys": stringtest.LinesLF(
			"~: a",
			"null: b",
			"True: {true: c}",
			"false: [d]",
		),
		// The lookups of both runs pass the limit on the merge sources they
		// read, so each writes the same names past it.
		"more merge reads than the limit allows": yamltest.WideMerge(2000),
	}

	for schemaName, schemaJSON := range schemas {
		v, err := jsonschema.CompileJSON(t.Context(), []byte(schemaJSON))
		require.NoError(t, err)

		for inputName, input := range inputs {
			t.Run(schemaName+"/"+inputName, func(t *testing.T) {
				t.Parallel()

				// The second key of some documents repeats the member of
				// the first.
				doc := yamltest.FirstDocument(t, input, niceyaml.WithAllowDuplicateKeys(true))

				want := schemaLocations(t, doc, v)
				require.NotEmpty(t, want)

				got := locatorLocations(t, doc, v)
				require.Len(t, got, len(want))

				// A document of thousands of locations reports the first
				// few that differ.
				var differ []string

				for i := range want {
					if want[i] != got[i] && len(differ) < 10 {
						differ = append(differ, fmt.Sprintf("schema %q, locator %q", want[i], got[i]))
					}
				}

				assert.Empty(t, differ)
			})
		}
	}
}

func TestDataLocator_Path(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input  string
		names  []string
		want   string
		wantOK bool
	}{
		"key the decoder respells": {
			input:  respelledDoc,
			names:  []string{"ports", "16", "name"},
			want:   "$.ports.0x10.name",
			wantOK: true,
		},
		"element of a sequence": {
			input:  respelledDoc,
			names:  []string{"items", "0", "name"},
			want:   "$.items[0].name",
			wantOK: true,
		},
		"no names": {
			input:  respelledDoc,
			want:   "$",
			wantOK: true,
		},
		"member a mapping lacks": {
			input: lackingDoc,
			names: []string{"ports", "16", "name"},
			want:  "$.ports.0x10.name",
		},
		"member no path selects": {
			input: hiddenDoc,
			names: []string{"user", "16"},
			want:  "$.user.16",
		},
		"member beside one no path selects": {
			input:  hiddenDoc,
			names:  []string{"user", "0x10"},
			want:   "$.user.0x10",
			wantOK: true,
		},
		"name a key spells for another member": {
			input: pythonDoc,
			names: []string{"python", "3.10"},
			want:  "$.python.'3.10'",
		},
		"member below an alias to a reference document": {
			input: referringDoc,
			names: []string{"items", "1", "16"},
			want:  "$.items.1.16",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := locatorDoc(t, tc.input)

			got, ok := doc.DataLocator().Path(tc.names...)

			assert.Equal(t, tc.want, got.String())
			assert.Equal(t, tc.wantOK, ok)
		})
	}

	t.Run("scope", func(t *testing.T) {
		t.Parallel()

		doc := locatorDoc(t, stringtest.LinesLF("- spec:", "    0x10: x"))
		spec := yamltest.At(t, doc, paths.Doc().Index(0).Child("spec"))

		// The names read from the Node, and the path starts at `$`.
		got, ok := spec.DataLocator().Path("16")
		require.True(t, ok)
		assert.Equal(t, "$[0].spec.0x10", got.String())
	})
}

func ExampleDataLocator_Path() {
	doc, err := niceyaml.NewSourceFromString(
		stringtest.LinesLF(
			"ports:",
			"  0x10:",
			`    name: ""`,
		),
	).Document()
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(doc.DataLocator().Path("ports", "16", "name"))

	// Output:
	// $.ports.0x10.name true
}

func TestDataLocator_Concurrent(t *testing.T) {
	t.Parallel()

	// Each mapping of the document respells its keys, so every lookup
	// reads a mapping and asks which entry a spelling selects.
	const (
		mappings = 24
		members  = 24
	)

	var sb strings.Builder

	sb.WriteString("base: &base {0x1: x}\n")

	for m := range mappings {
		fmt.Fprintf(&sb, stringtest.LinesLF("m%d:", "  <<: *base"), m)

		for i := range members {
			fmt.Fprintf(&sb, stringtest.LinesLF(`  %#x: {name: ""}`), 0x10+i)
		}
	}

	doc := locatorDoc(t, sb.String())

	names := func(m, i int) []string {
		return []string{"m" + strconv.Itoa(m), strconv.Itoa(0x10 + i), "name"}
	}

	// One goroutine finds every location first.
	single := doc.DataLocator()
	want := make([]string, 0, mappings*members)

	for m := range mappings {
		for i := range members {
			got, ok := single.Path(names(m, i)...)
			require.True(t, ok)

			want = append(want, got.String())
		}
	}

	require.Equal(t, "$.m0.0x10.name", want[0])

	shared := doc.DataLocator()
	results := make([][]string, 8)

	var wg sync.WaitGroup

	for g := range results {
		wg.Go(func() {
			got := make([]string, 0, mappings*members)

			for m := range mappings {
				for i := range members {
					path, _ := shared.Path(names(m, i)...)
					got = append(got, path.String())
				}
			}

			// Each goroutine also reads members the others do not.
			_, _ = shared.Path("m"+strconv.Itoa(g), "1", "missing")

			results[g] = got
		})
	}

	wg.Wait()

	for _, got := range results {
		assert.Equal(t, want, got)
	}
}

func TestAtData_MergeReads(t *testing.T) {
	t.Parallel()

	// To spell a key of m, the binding reads the whole list of its merge
	// key, and the errors of one bind share one limit on those reads.
	// Past it, each path keeps the decoded name of its key, and the error
	// still binds at the value, by its position. With the alias limit off,
	// the reads have no limit, so every path spells its key.
	const members = 2000

	tcs := map[string]struct {
		opts []niceyaml.SourceOption
		all  bool
	}{
		"limit on": {},
		"limit off": {
			opts: []niceyaml.SourceOption{niceyaml.WithAliasLimit(false)},
			all:  true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := append([]niceyaml.SourceOption{
				niceyaml.WithName("f.yaml"),
				yamltest.References(referenceDoc),
			}, tc.opts...)
			doc := yamltest.FirstDocument(t, yamltest.WideMerge(members), opts...)

			errs := make([]error, 0, members)
			for i := range members {
				errs = append(errs, niceyaml.NewError(strconv.Itoa(i), niceyaml.AtData("m", strconv.Itoa(0x1000+i))))
			}

			spelled := 0

			for problem := range niceyaml.Problems(doc.Bind(errors.Join(errs...))) {
				i, err := strconv.Atoi(problem.Bound.Message())
				require.NoError(t, err)

				require.Equal(t, i+2, yamltest.PositionOf(t, problem.Bound).Line, "member %d", i)

				path, ok := problem.Bound.Path()
				require.True(t, ok)

				if strings.HasPrefix(path.String(), "$.m.0x") {
					spelled++

					continue
				}

				require.Equal(t, "$.m."+strconv.Itoa(0x1000+i), path.String())
			}

			if tc.all {
				assert.Equal(t, members, spelled)
			} else {
				assert.Positive(t, spelled)
				assert.Less(t, spelled, members)
			}

			// A DataLocator shares the limit among its lookups the same way.
			loc := doc.DataLocator()
			found := 0

			for i := range members {
				if _, ok := loc.Path("m", strconv.Itoa(0x1000+i)); ok {
					found++
				}
			}

			assert.Equal(t, spelled, found)
		})
	}
}

// dataPort is an entry of [dataPorts].
type dataPort struct {
	Name string `yaml:"name"`
}

// dataPorts checks the entries of its map in the parent, so it names each
// entry by its decoded key.
type dataPorts struct {
	Ports map[int]dataPort `yaml:"ports"`
	// The port that the check reports as unknown, by key, when it is set.
	Unknown int `yaml:"unknown"`
}

func (c dataPorts) Validate() error {
	var errs []error

	for k, p := range c.Ports {
		if p.Name == "" {
			errs = append(errs, niceyaml.NewError("name is empty", niceyaml.AtData("ports", strconv.Itoa(k), "name")))
		}
	}

	if _, ok := c.Ports[c.Unknown]; ok {
		errs = append(errs, niceyaml.NewError("unknown port", niceyaml.AtDataKey("ports", strconv.Itoa(c.Unknown))))
	}

	return errors.Join(errs...)
}

// dataDefaults decodes itself through a plain copy of its type, as a
// type that sets defaults does, and checks the entries of its map in the
// parent.
type dataDefaults struct {
	Ports map[int]dataPort `yaml:"ports"`
}

func (c *dataDefaults) UnmarshalYAML(_ context.Context, decode func(any) error) error {
	type plain dataDefaults

	return decode((*plain)(c))
}

func (c dataDefaults) Validate() error {
	var errs []error

	for k, p := range c.Ports {
		if p.Name == "" {
			errs = append(errs, niceyaml.NewError("name is empty", niceyaml.AtData("ports", strconv.Itoa(k), "name")))
		}
	}

	return errors.Join(errs...)
}

// dataNested holds a [dataDefaults] below a key.
type dataNested struct {
	Cfg dataDefaults `yaml:"cfg"`
}

// dataSelfNested decodes itself through a plain copy of its type and holds
// a [dataDefaults], which decodes itself too.
type dataSelfNested struct {
	Cfg dataDefaults `yaml:"cfg"`
}

func (c *dataSelfNested) UnmarshalYAML(_ context.Context, decode func(any) error) error {
	type plain dataSelfNested

	return decode((*plain)(c))
}

// dataCheckedPort is an entry of [dataDefaultPorts] that checks itself.
type dataCheckedPort struct {
	Name string `yaml:"name"`
}

func (p dataCheckedPort) Validate() error {
	if p.Name == "" {
		return errors.New("port name empty")
	}

	return nil
}

// dataDefaultPorts decodes itself through a plain copy of its type, as a
// type that sets defaults does, and holds entries that check themselves.
type dataDefaultPorts struct {
	Ports map[int]dataCheckedPort `yaml:"ports"`
}

func (c *dataDefaultPorts) UnmarshalYAML(_ context.Context, decode func(any) error) error {
	type plain dataDefaultPorts

	return decode((*plain)(c))
}

// dataDefaultPortsHolder holds a [dataDefaultPorts] below a key.
type dataDefaultPortsHolder struct {
	Cfg dataDefaultPorts `yaml:"cfg"`
}

// dataMid sits between two values that decode themselves.
type dataMid struct {
	Inner dataDefaults `yaml:"inner"`
}

// dataSelfMid decodes itself through a plain copy of its type and holds a
// [dataMid].
type dataSelfMid struct {
	Mid dataMid `yaml:"mid"`
}

func (c *dataSelfMid) UnmarshalYAML(_ context.Context, decode func(any) error) error {
	type plain dataSelfMid

	return decode((*plain)(c))
}

// DataPortMap holds a map whose keys a decode respells, for an inline
// field.
type DataPortMap struct {
	Ports map[int]dataPort `yaml:"ports"`
}

// dataInline holds a [DataPortMap] inline.
type dataInline struct {
	DataPortMap `yaml:",inline"`

	X int `yaml:"x"`
}

// dataInlinePointer holds a pointer to a [DataPortMap] inline.
type dataInlinePointer struct {
	*DataPortMap `yaml:",inline"`

	X int `yaml:"x"`
}

// dataSpelledPorts holds a map whose keys a decode respells, without a
// check of its own.
type dataSpelledPorts struct {
	Ports map[int]dataPort `yaml:"ports"`
}

// dataItems holds a sequence of maps whose keys a decode respells.
type dataItems struct {
	Items []map[int]string `yaml:"items"`
}

// dataSpelled holds maps whose keys a decode respells.
type dataSpelled struct {
	Ints   map[int]string    `yaml:"ints"`
	Texts  map[string]string `yaml:"texts"`
	Floats map[float64]bool  `yaml:"floats"`
}

// rebasedDefaults decodes itself through a plain copy of its type, and
// checks the entries of its map as [rebasedPorts] does.
type rebasedDefaults rebasedPorts

func (c *rebasedDefaults) UnmarshalYAML(_ context.Context, decode func(any) error) error {
	type plain rebasedDefaults

	return decode((*plain)(c))
}

func (c rebasedDefaults) Validate() error {
	return rebasedPorts(c).Validate()
}

// plainDefaults decodes itself through a plain copy of its type, without a
// check of its own.
type plainDefaults struct {
	Ports map[int]portName `yaml:"ports"`
}

func (c *plainDefaults) UnmarshalYAML(_ context.Context, decode func(any) error) error {
	type plain plainDefaults

	return decode((*plain)(c))
}

// absolutePorts decodes itself through a plain copy of its type. Its check
// places its error at a `$` path.
type absolutePorts map[int]portName

func (m *absolutePorts) UnmarshalYAML(unmarshal func(any) error) error {
	return unmarshal((*map[int]portName)(m))
}

func (absolutePorts) Validate() error {
	//nolint:wrapcheck // The walk places the error as the check returns it.
	return niceyaml.Rebase(niceyaml.NewError("other", niceyaml.AtData("16")), paths.Doc().Child("other"))
}

// dataInlineMap holds a map inline, which takes the entries that no field
// names.
type dataInlineMap struct {
	Extra map[string]dataPort `yaml:",inline"`
	Kind  string              `yaml:"kind"`
}

// pathKey is a map key that checks itself and places its error at a path
// below the key.
type pathKey int

func (pathKey) Validate() error {
	return niceyaml.NewError("key below", niceyaml.AtPath(paths.Current().Child("a")))
}

// pathKeys holds a map whose keys are [pathKey]s.
type pathKeys struct {
	M map[pathKey]int `yaml:"m"`
}

// checkedKeyHolder is a map key that holds a key that checks itself.
type checkedKeyHolder struct {
	F checkedKey `yaml:"f"`
}

// checkedElem is an element that checks itself.
type checkedElem int

func (checkedElem) Validate() error {
	return errors.New("bad element")
}

// selfArray is a map key that decodes itself and holds an element that
// checks itself.
type selfArray [1]checkedElem

func (a *selfArray) UnmarshalYAML(unmarshal func(any) error) error {
	return unmarshal((*[1]checkedElem)(a))
}

// warnedPorts checks the entries of its map in the parent. It reports a
// warning at each entry and errors at members below it, so one check
// returns both severities.
type warnedPorts struct {
	Ports map[int]dataPort `yaml:"ports"`
}

func (c warnedPorts) Validate() error {
	var errs []error

	for k := range c.Ports {
		errs = append(errs, niceyaml.NewWarning("unused", niceyaml.AtData("ports", strconv.Itoa(k))))

		for i := range 20 {
			errs = append(
				errs,
				niceyaml.NewError("bad", niceyaml.AtData("ports", strconv.Itoa(k), "x"+strconv.Itoa(i))),
			)
		}
	}

	return errors.Join(errs...)
}

func TestAtData_Decode(t *testing.T) {
	t.Parallel()

	t.Run("a goroutine reads the warnings of a check during a decode", func(t *testing.T) {
		t.Parallel()

		ws := niceyaml.NewWarnings()
		ctx := ws.Context(t.Context())

		var (
			stop atomic.Bool
			wg   sync.WaitGroup
		)

		wg.Go(func() {
			for !stop.Load() {
				for tree := range ws.All() {
					_, _ = tree.Path()
					_ = niceyaml.FormatError(niceyaml.Detach(tree.Err))
				}

				for p := range niceyaml.Problems(ws.Err()) {
					_, _ = p.Path()
				}
			}
		})

		for i := range 50 {
			doc := locatorDoc(t, stringtest.LinesLF("ports:", "  0x10:", "    name: a"))

			_, err := doc.Decode[warnedPorts](ctx)
			require.Error(t, err, "decode %d", i)
		}

		stop.Store(true)
		wg.Wait()

		var got []string

		for tree := range ws.All() {
			p, _ := tree.Path()
			got = append(got, p.String())
		}

		require.Len(t, got, 50)
		assert.Equal(t, "$.ports.0x10", got[0])
	})

	t.Run("a check places the names of SelfValidateValue under its own path", func(t *testing.T) {
		t.Parallel()

		doc := locatorDoc(t, stringtest.LinesLF("inner:", "  ports:", "    0x10:", `      name: ""`))

		const want = "f.yaml:3:5: $.inner.ports.0x10: port is empty\n" +
			"f.yaml:4:13: $.inner.ports.0x10.name: name is empty"

		// The check validates its own copy of the ports, so the walk of the
		// decode does not validate them a second time.
		ports := func(ctx context.Context, m map[int]portName) error {
			return niceyaml.SelfValidateValue(ctx, struct {
				Inner rebasedPorts `yaml:"inner"`
			}{rebasedPorts{Ports: m}})
		}

		tcs := map[string]struct {
			decode func(t *testing.T) error
		}{
			"the decoded value does not decode itself": {
				decode: func(t *testing.T) error {
					t.Helper()

					type holder struct {
						Inner dataSpelledPorts `yaml:"inner"`
					}

					checks := niceyaml.WithValueCheck(func(ctx context.Context, v *holder) error {
						m := map[int]portName{}
						for k, p := range v.Inner.Ports {
							m[k] = portName(p)
						}

						return ports(ctx, m)
					})

					_, err := doc.Decode[holder](t.Context(), checks)

					return err
				},
			},
			"the decoded value decodes itself": {
				decode: func(t *testing.T) error {
					t.Helper()

					type holder struct {
						Inner plainDefaults `yaml:"inner"`
					}

					checks := niceyaml.WithValueCheck(func(ctx context.Context, v *holder) error {
						return ports(ctx, v.Inner.Ports)
					})

					_, err := doc.Decode[holder](t.Context(), checks)

					return err
				},
			},
			"the value of SelfValidateValue decodes itself": {
				decode: func(t *testing.T) error {
					t.Helper()

					type holder struct {
						Inner plainDefaults `yaml:"inner"`
					}

					checks := niceyaml.WithValueCheck(func(ctx context.Context, v *holder) error {
						return niceyaml.SelfValidateValue(ctx, struct {
							Inner rebasedDefaults `yaml:"inner"`
						}{rebasedDefaults{Ports: v.Inner.Ports}})
					})

					_, err := doc.Decode[holder](t.Context(), checks)

					return err
				},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				require.EqualError(t, tc.decode(t), want)
			})
		}
	})

	t.Run("names through a nil pointer and a nil interface", func(t *testing.T) {
		t.Parallel()

		type holder struct {
			P *dataPort `yaml:"p"`
			A any       `yaml:"a"`
		}

		doc := locatorDoc(t, stringtest.LinesLF("p: ~", "a: ~"))

		checks := niceyaml.WithValueCheck(func(_ context.Context, _ *holder) error {
			return errors.Join(
				niceyaml.NewError("pointer", niceyaml.AtData("p", "name")),
				niceyaml.NewError("interface", niceyaml.AtData("a", "name")),
			)
		})

		_, err := doc.Decode[holder](t.Context(), checks)
		require.EqualError(t, err, stringtest.JoinLF(
			"f.yaml:1:1: $.p.name: pointer",
			"f.yaml:2:1: $.a.name: interface",
		))
	})

	t.Run("names through an inline map and a nil inline pointer", func(t *testing.T) {
		t.Parallel()

		// A decode leaves an inline map empty, so the program fills it and
		// the walk of SelfValidate stands in for the decode. The key 1.10
		// decodes to the string 1.1.
		doc := locatorDoc(t, stringtest.LinesLF("kind: a", "1.10:", "  name: x"))

		inline := niceyaml.WithValueCheck(func(_ context.Context, _ *dataInlineMap) error {
			return niceyaml.NewError("inline", niceyaml.AtData("1.1", "name"))
		})

		v := &dataInlineMap{Kind: "a", Extra: map[string]dataPort{"1.1": {Name: "x"}}}
		require.EqualError(t, doc.SelfValidate(t.Context(), v, inline), "f.yaml:3:9: $.'1.10'.name: inline")

		doc = locatorDoc(t, "x: 1")

		pointer := niceyaml.WithValueCheck(func(_ context.Context, _ *dataInlinePointer) error {
			return niceyaml.NewError("inline", niceyaml.AtData("ports", "16"))
		})

		err := doc.SelfValidate(t.Context(), &dataInlinePointer{X: 1}, pointer)
		require.EqualError(t, err, "f.yaml:1:1: $.ports.16: inline")
	})

	t.Run("names through a map with a nil pointer key", func(t *testing.T) {
		t.Parallel()

		type holder struct {
			M map[*int]int `yaml:"m"`
		}

		doc := locatorDoc(t, stringtest.LinesLF("m:", "  ~: 1", "  0x10: 2"))

		checks := niceyaml.WithValueCheck(func(_ context.Context, _ *holder) error {
			return errors.Join(
				niceyaml.NewError("null", niceyaml.AtData("m", "null")),
				niceyaml.NewError("int", niceyaml.AtData("m", "16")),
			)
		})

		_, err := doc.Decode[holder](t.Context(), checks)
		require.EqualError(t, err, stringtest.JoinLF(
			"f.yaml:2:6: $.m.'~': null",
			"f.yaml:3:9: $.m.0x10: int",
		))
	})

	t.Run("a key that checks itself places its error below the key", func(t *testing.T) {
		t.Parallel()

		doc := locatorDoc(t, stringtest.LinesLF("m:", "  0x10: 1"))

		_, err := doc.Decode[pathKeys](t.Context())
		require.EqualError(t, err, "f.yaml:2:3: $.m.0x10~.a: key below")
	})

	t.Run("names below an element the slice lacks", func(t *testing.T) {
		t.Parallel()

		type holder struct {
			Items []dataPort `yaml:"items"`
		}

		doc := locatorDoc(t, stringtest.LinesLF("items:", "  - name: a"))

		checks := niceyaml.WithValueCheck(func(_ context.Context, _ *holder) error {
			return errors.Join(
				niceyaml.NewError("past", niceyaml.AtData("items", "5", "name")),
				niceyaml.NewError("text", niceyaml.AtData("items", "x")),
			)
		})

		_, err := doc.Decode[holder](t.Context(), checks)
		require.EqualError(t, err, stringtest.JoinLF(
			"f.yaml:1:1: $.items.5.name: past",
			"f.yaml:1:1: $.items.x: text",
		))
	})

	t.Run("names under an @ base with a wildcard", func(t *testing.T) {
		t.Parallel()

		type holder struct {
			Items []dataPort `yaml:"items"`
		}

		doc := locatorDoc(t, stringtest.LinesLF("items:", "  - name: a"))

		checks := niceyaml.WithValueCheck(func(_ context.Context, _ *holder) error {
			return niceyaml.Rebase(niceyaml.NewError("wild", niceyaml.AtData("name")),
				paths.Current().Child("items").IndexAll())
		})

		_, err := doc.Decode[holder](t.Context(), checks)
		require.EqualError(t, err, "f.yaml: $.items[*].name: wild")
	})

	t.Run("a name that several keys of a typed map print", func(t *testing.T) {
		t.Parallel()

		type holder struct {
			M map[any]string `yaml:"m"`
		}

		doc := locatorDoc(t, stringtest.LinesLF("m:", "  1: a", "  1.0: b"))

		checks := niceyaml.WithValueCheck(func(_ context.Context, _ *holder) error {
			return niceyaml.NewError("shared", niceyaml.AtData("m", "1"))
		})

		_, err := doc.Decode[holder](t.Context(), checks)
		require.EqualError(t, err, "f.yaml:2:6: $.m.1: shared")
	})

	t.Run("a check below a map that decodes itself places an error at a $ path", func(t *testing.T) {
		t.Parallel()

		type holder struct {
			Ports absolutePorts `yaml:"ports"`
		}

		doc := locatorDoc(t, stringtest.LinesLF("ports:", "  0x10: {name: a}", "other:", "  0x10: b"))

		_, err := doc.Decode[holder](t.Context())
		require.EqualError(t, err, "f.yaml:4:9: $.other.0x10: other")
	})

	t.Run("a check in the parent names an entry by its decoded key", func(t *testing.T) {
		t.Parallel()

		doc := locatorDoc(t, stringtest.LinesLF(
			"ports:",
			"  0x10:",
			`    name: ""`,
			"  0o21:",
			"    name: b",
			"unknown: 17",
		))

		_, err := doc.Decode[dataPorts](t.Context())
		require.EqualError(t, err, stringtest.JoinLF(
			"f.yaml:3:11: $.ports.0x10.name: name is empty",
			"f.yaml:4:3: $.ports.0o21~: unknown port",
		))

		var cfg dataPorts

		require.NoError(t, doc.DecodeInto(t.Context(), &cfg, niceyaml.WithSelfValidation(false)))
		require.EqualError(t, doc.SelfValidate(t.Context(), &cfg), err.Error())
	})

	t.Run("names read from the typed value", func(t *testing.T) {
		t.Parallel()

		// A decode into any names the key 1e3 1e3, but a decode into a
		// map[int]string names it 1000. The key 1.10 of a map[string]string
		// decodes to 1.1.
		doc := locatorDoc(t, stringtest.LinesLF(
			"ints:",
			"  1e3: a",
			"texts:",
			"  1.10: b",
			"floats:",
			"  0x10: true",
		))

		checks := niceyaml.WithValueCheck(func(_ context.Context, v *dataSpelled) error {
			var errs []error

			for k := range v.Ints {
				errs = append(errs, niceyaml.NewError("int", niceyaml.AtData("ints", strconv.Itoa(k))))
			}

			for k := range v.Texts {
				errs = append(errs, niceyaml.NewError("text", niceyaml.AtDataKey("texts", k)))
			}

			for k := range v.Floats {
				errs = append(errs, niceyaml.NewError("float", niceyaml.AtData("floats", fmt.Sprint(k))))
			}

			return errors.Join(errs...)
		})

		_, err := doc.Decode[dataSpelled](t.Context(), checks)
		require.EqualError(t, err, stringtest.JoinLF(
			"f.yaml:2:8: $.ints.1e3: int",
			"f.yaml:4:3: $.texts.'1.10'~: text",
			"f.yaml:6:9: $.floats.0x10: float",
		))

		// A binding outside the decode reads the names as a decode into any
		// gives them.
		got, near := located(t, doc, niceyaml.AtData("ints", "1000"))
		assert.Equal(t, "f.yaml:1:1: $.ints.1000: x", got)
		assert.Equal(t, "$.ints", near)

		got, _ = located(t, doc, niceyaml.AtData("ints", "1e3"))
		assert.Equal(t, "f.yaml:2:8: $.ints.1e3: x", got)
	})

	t.Run("names past the typed value keep their form", func(t *testing.T) {
		t.Parallel()

		doc := locatorDoc(t, stringtest.LinesLF("ints:", "  0x10: a"))

		checks := niceyaml.WithValueCheck(func(_ context.Context, v *dataSpelled) error {
			return niceyaml.NewError("deep", niceyaml.AtData("ints", "16", "x", "0"))
		})

		_, err := doc.Decode[dataSpelled](t.Context(), checks)
		require.EqualError(t, err, "f.yaml:2:9: $.ints.0x10.x.0: deep")
	})

	t.Run("names below a value that decodes itself", func(t *testing.T) {
		t.Parallel()

		// The walk does not read below a value that decodes itself, so
		// the binding reads the names there from the document.
		doc := locatorDoc(t, stringtest.LinesLF("ports:", "  0x10:", `    name: ""`))

		_, err := doc.Decode[dataDefaults](t.Context())
		require.EqualError(t, err, "f.yaml:3:11: $.ports.0x10.name: name is empty")

		doc = locatorDoc(t, stringtest.LinesLF("cfg:", "  ports:", "    0x10:", `      name: ""`))

		_, err = doc.Decode[dataNested](t.Context())
		require.EqualError(t, err, "f.yaml:4:13: $.cfg.ports.0x10.name: name is empty")

		// The check lies below a value that decodes itself, and it reads
		// from another such value.
		_, err = doc.Decode[dataSelfNested](t.Context())
		require.EqualError(t, err, "f.yaml:4:13: $.cfg.ports.0x10.name: name is empty")
	})

	t.Run("names that lead from a typed value into one that decodes itself", func(t *testing.T) {
		t.Parallel()

		doc := locatorDoc(t, stringtest.LinesLF(
			"mid:",
			"  inner:",
			"    ports:",
			"      0x10:",
			"        name: a",
		))

		checks := niceyaml.WithValueCheck(func(_ context.Context, _ *dataMid) error {
			return niceyaml.NewError("mid", niceyaml.AtData("inner", "ports", "16", "name"))
		})

		_, err := doc.Decode[dataSelfMid](t.Context(), checks)
		require.EqualError(t, err, "f.yaml:5:15: $.mid.inner.ports.0x10.name: mid")
	})

	t.Run("names through an inline struct", func(t *testing.T) {
		t.Parallel()

		doc := locatorDoc(t, stringtest.LinesLF("ports:", "  0x10:", "    name: a"))

		const want = "f.yaml:3:11: $.ports.0x10.name: inline"

		inline := niceyaml.WithValueCheck(func(_ context.Context, _ *dataInline) error {
			return niceyaml.NewError("inline", niceyaml.AtData("ports", "16", "name"))
		})

		_, err := doc.Decode[dataInline](t.Context(), inline)
		require.EqualError(t, err, want)

		pointer := niceyaml.WithValueCheck(func(_ context.Context, _ *dataInlinePointer) error {
			return niceyaml.NewError("inline", niceyaml.AtData("ports", "16", "name"))
		})

		_, err = doc.Decode[dataInlinePointer](t.Context(), pointer)
		require.EqualError(t, err, want)
	})

	t.Run("names under an @ base with an index", func(t *testing.T) {
		t.Parallel()

		doc := locatorDoc(t, stringtest.LinesLF("items:", "  - 0x10: a"))

		checks := niceyaml.WithValueCheck(func(_ context.Context, _ *dataItems) error {
			return niceyaml.Rebase(niceyaml.NewError("idx", niceyaml.AtData("16")),
				paths.Current().Child("items").Index(0))
		})

		_, err := doc.Decode[dataItems](t.Context(), checks)
		require.EqualError(t, err, "f.yaml:2:11: $.items[0].0x10: idx")
	})

	t.Run("names under a base of a rebase", func(t *testing.T) {
		t.Parallel()

		// A decode into any names the key 1e3 1e3, but a decode into a
		// map[int]string names it 1000.
		doc := locatorDoc(t, stringtest.LinesLF("ints:", "  1e3: a"))

		tcs := map[string]struct {
			base paths.Path
			want string
		}{
			"an @ base reads from the typed value": {
				base: paths.Current().Child("ints"),
				want: "f.yaml:2:8: $.ints.1e3: int",
			},
			"a $ base reads as a decode into any": {
				base: paths.Doc().Child("ints"),
				want: "f.yaml:1:1: $.ints.1000: int",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				checks := niceyaml.WithValueCheck(func(_ context.Context, v *dataSpelled) error {
					var errs []error

					for k := range v.Ints {
						err := niceyaml.NewError("int", niceyaml.AtData(strconv.Itoa(k)))
						errs = append(errs, niceyaml.Rebase(err, tc.base))
					}

					return errors.Join(errs...)
				})

				_, err := doc.Decode[dataSpelled](t.Context(), checks)
				require.EqualError(t, err, tc.want)
			})
		}
	})

	t.Run("names through an interface value", func(t *testing.T) {
		t.Parallel()

		type holder struct {
			Extra any `yaml:"extra"`
		}

		doc := locatorDoc(t, stringtest.LinesLF("extra:", `  0x10: {name: ""}`))

		checks := niceyaml.WithValueCheck(func(_ context.Context, _ *holder) error {
			return niceyaml.NewError("extra", niceyaml.AtData("extra", "16", "name"))
		})

		_, err := doc.Decode[holder](t.Context(), checks)
		require.EqualError(t, err, "f.yaml:2:16: $.extra.0x10.name: extra")
	})

	t.Run("names through a map of interface keys", func(t *testing.T) {
		t.Parallel()

		type holder struct {
			M map[any]string `yaml:"m"`
		}

		doc := locatorDoc(t, stringtest.LinesLF("m:", "  0x10: a", "  1.10: b"))

		checks := niceyaml.WithValueCheck(func(_ context.Context, _ *holder) error {
			return errors.Join(
				niceyaml.NewError("int", niceyaml.AtData("m", "16")),
				niceyaml.NewError("float", niceyaml.AtData("m", "1.1")),
			)
		})

		_, err := doc.Decode[holder](t.Context(), checks)
		require.EqualError(t, err, stringtest.JoinLF(
			"f.yaml:2:9: $.m.0x10: int",
			"f.yaml:3:9: $.m.'1.10': float",
		))
	})

	t.Run("names under an @ base through an entry the document spells", func(t *testing.T) {
		t.Parallel()

		doc := locatorDoc(t, stringtest.LinesLF("ports:", "  0x10:", `    name: ""`))

		tcs := map[string]struct {
			base paths.Path
			want string
		}{
			"the base selects the entry": {
				base: paths.Current().Child("ports", "0x10"),
				want: "f.yaml:3:11: $.ports.0x10.name: base",
			},
			"the base names the entry by its decoded key": {
				base: paths.Current().Child("ports", "16"),
				want: "f.yaml:1:1: $.ports.16.name: base",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				checks := niceyaml.WithValueCheck(func(_ context.Context, _ *dataSpelledPorts) error {
					return niceyaml.Rebase(niceyaml.NewError("base", niceyaml.AtData("name")), tc.base)
				})

				_, err := doc.Decode[dataSpelledPorts](t.Context(), checks)
				require.EqualError(t, err, tc.want)
			})
		}
	})

	t.Run("a check of a map of builtin values", func(t *testing.T) {
		t.Parallel()

		doc := locatorDoc(t, stringtest.LinesLF("0x10: a", "0x20: b"))

		checks := niceyaml.WithValueCheck(func(_ context.Context, m *map[int]string) error {
			if (*m)[32] == "b" {
				return niceyaml.NewError("taken", niceyaml.AtData("32"))
			}

			return nil
		})

		_, err := doc.Decode[map[int]string](t.Context(), checks)
		require.EqualError(t, err, "f.yaml:2:7: $.0x20: taken")
	})

	t.Run("a location on a heading reaches its problems", func(t *testing.T) {
		t.Parallel()

		doc := locatorDoc(t, respelledDoc)

		heading := func() *niceyaml.Error {
			target := &niceyaml.Error{}
			_ = errors.As(niceyaml.NewSummary("port checks", errors.New("a"), errors.New("b")), &target)

			return target
		}() //nolint:errcheck,forcetypeassert // Two members give a summary.

		require.EqualError(t, doc.Bind(heading.With(niceyaml.AtData("ports", "16"))), stringtest.JoinLF(
			"f.yaml:2:3: $.ports.0x10: port checks",
			"f.yaml:2:3: $.ports.0x10: a",
			"f.yaml:2:3: $.ports.0x10: b",
		))
	})

	t.Run("a later AtPath replaces the names", func(t *testing.T) {
		t.Parallel()

		err := niceyaml.NewError("x", niceyaml.AtData("a", "b"), niceyaml.AtPath(paths.Current().Child("c")))
		assert.Equal(t, "@.c", yamltest.PathOf(t, err).String())

		err = niceyaml.NewError("x", niceyaml.AtPath(paths.Current().Child("c")), niceyaml.AtDataKey("a", "b"))
		assert.Equal(t, "@.a.b~", yamltest.PathOf(t, err).String())
	})

	t.Run("the bound error reads the same from many goroutines", func(t *testing.T) {
		t.Parallel()

		doc := locatorDoc(t, stringtest.LinesLF("ports:", "  0x10:", `    name: ""`))

		_, err := doc.Decode[dataPorts](t.Context())
		require.Error(t, err)

		const want = "f.yaml:3:11: $.ports.0x10.name: name is empty"

		var wg sync.WaitGroup

		for range 8 {
			wg.Go(func() {
				assert.Equal(t, want, err.Error())
				assert.Equal(t, want, niceyaml.FormatError(niceyaml.Detach(err))[:len(want)])
			})
		}

		wg.Wait()
	})
}
