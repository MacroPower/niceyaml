package niceyaml_test

import (
	"fmt"
	"log"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/jsonschema"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/schema"
)

// The documents the locator tests share.
const (
	// The key 0x10 sets the member 16, and items holds an element.
	respelledDoc = "ports:\n  0x10:\n    name: \"\"\nitems:\n  - name: \"\"\n"
	// The mapping under 0x10 holds no name.
	lackingDoc = "ports:\n  0x10:\n    proto: tcp\n"
	// The key 3.10 sets the member 3.1, so no member has the name 3.10.
	pythonDoc = "python:\n  3.10:\n    image: a\n"
	// Both keys of m set the member 16, and a decode keeps b.
	sameNameDoc = "m:\n  16: aaaa\n  0x10: b\n"
	// The merge key sets the member 16 to x. The quoted key sets the
	// member 0x10, and a path through 0x10 selects that entry.
	hiddenDoc = "user:\n  <<: {0x10: x}\n  \"0x10\": 1\n"
	// The merge key of m sets the member true to b, the last value b1
	// gives that name.
	mergeDoc = "b0: &b0\n  True: xxxx\nb1: &b1\n  <<: *b0\n  true: aaaa\n  True: b\nm:\n  <<: *b1\n"
	// A sequence, a mapping with keys of digits, an alias to the
	// sequence, a sequence in a sequence, a scalar, and a null.
	shapesDoc = "seq: &s\n  - a\n  - b\nmap:\n  0: x\n  1: y\nal: *s\n" +
		"nested:\n  - - p\n    - q\nscalar: v\nnone: ~\n"
	// The anchors a document of the tests refers to through
	// [niceyaml.WithReferences].
	referenceDoc = "defaults: &defaults\n  timeout: 0\n  3.10: r\nlist: &list [u, {0x10: v}]\n"
	// The mapping server merges an anchor of referenceDoc, and items is
	// an alias to one.
	referringDoc = "server:\n  <<: *defaults\n  port: 0x10\n  3.10: mine\nitems: *list\n"
	// The tagged alias key of python has no name the locator can tell, so
	// it may set a member of any name, such as one a key before it sets.
	unnamedKeyDoc = "k: &k 0x10\npython:\n  3.10: {image: a}\n  a: 1\n  !!str *k : v\n  b: 2\n"
)

// locatorDoc returns the first document of input, from a source named
// f.yaml that resolves the anchors of [referenceDoc].
func locatorDoc(tb testing.TB, input string) *niceyaml.Node {
	tb.Helper()

	return yamltest.FirstDocument(tb, input,
		niceyaml.WithName("f.yaml"),
		niceyaml.WithReferences(niceyaml.NewSourceFromString(referenceDoc)),
	)
}

// located binds an error with the location opt gives it through n. It
// returns the message of the binding, which puts the position and the
// path in front. It also returns the path of the node the error binds at
// when the document holds no value at its own path, as
// [niceyaml.SourceError.Nearest] reports it.
func located(tb testing.TB, n *niceyaml.Node, opt niceyaml.ErrorOption) (string, string) {
	tb.Helper()

	var bound *niceyaml.SourceError

	require.ErrorAs(tb, n.Bind(niceyaml.NewError("x", opt)), &bound)

	near := ""
	if path, ok := bound.Nearest(); ok {
		near = path.String()
	}

	return bound.Error(), near
}

// pointerNames returns the names in a JSON Pointer, as the documentation
// of [niceyaml.DataLocator] splits one.
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

func ExampleDataLocator() {
	doc, err := niceyaml.NewSourceFromString(
		"ports:\n  0x10:\n    name: \"\"\nitems:\n  - name: \"\"\n",
		niceyaml.WithName("cfg.yaml"),
	).Document()
	if err != nil {
		log.Fatal(err)
	}

	// A check of the decoded data reports each finding as a JSON Pointer.
	findings := []string{"/ports/16/name", "/items/0/name"}

	loc := doc.DataLocator()

	for _, pointer := range findings {
		fmt.Println(doc.Bind(niceyaml.NewError("must not be empty", loc.At(pointerNames(pointer)...))))
	}

	// Output:
	// cfg.yaml:3:11: $.ports.0x10.name: must not be empty
	// cfg.yaml:5:11: $.items[0].name: must not be empty
}

func TestDataLocator_At(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		names []string
		want  string
		// The path of the node the error binds at, for names the document
		// holds no value for: the mapping that lacks a member, or an alias
		// the document cannot follow.
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
			input: "- name: \"\"\n",
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
			input: "user:\n  <<: {0x10: {a: b}}\n  \"0x10\": 1\n",
			names: []string{"user", "16", "name"},
			want:  "f.yaml:2:8: $.user.16.name: x",
		},
		"name of a decode into map[int]": {
			input:    "m:\n  1e3:\n    n: \"\"\n",
			names:    []string{"m", "1000", "n"},
			want:     "f.yaml:1:1: $.m.1000.n: x",
			wantNear: "$.m",
		},
		"name of a decode into map[string]": {
			input:    "m:\n  ~:\n    n: \"\"\n",
			names:    []string{"m", "", "n"},
			want:     "f.yaml:1:1: $.m.''.n: x",
			wantNear: "$.m",
		},

		// A name that is no index of its sequence, or lies below a scalar.
		"index past the end": {
			input: shapesDoc,
			names: []string{"seq", "9"},
			want:  "f.yaml: $.seq[9]: x",
		},
		"names below an index past the end": {
			input: shapesDoc,
			names: []string{"seq", "9", "x"},
			want:  "f.yaml: $.seq[9].x: x",
		},
		"index with a leading zero": {
			input: shapesDoc,
			names: []string{"seq", "01"},
			want:  "f.yaml: $.seq.01: x",
		},
		"index with a sign": {
			input: shapesDoc,
			names: []string{"seq", "+1"},
			want:  "f.yaml: $.seq.+1: x",
		},
		"index too large for an int": {
			input: shapesDoc,
			names: []string{"seq", "99999999999999999999"},
			want:  "f.yaml: $.seq.99999999999999999999: x",
		},
		"member name in a sequence": {
			input: shapesDoc,
			names: []string{"seq", "name"},
			want:  "f.yaml: $.seq.name: x",
		},
		"name below a scalar": {
			input: shapesDoc,
			names: []string{"scalar", "x"},
			want:  "f.yaml: $.scalar.x: x",
		},
		"index past the end below a member no path selects": {
			input: "user:\n  <<: {0x10: [a]}\n  \"0x10\": 1\n",
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
			input: referringDoc,
			names: []string{"server", "timeout"},
			want:  "f.yaml: $.server.timeout: x",
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
			input: "scalar\n",
			names: []string{"a", "0"},
			want:  "f.yaml: $.a.0: x",
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
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := locatorDoc(t, tc.input)

			got, gotNear := located(t, doc, doc.DataLocator().At(tc.names...))

			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.wantNear, gotNear)
		})
	}
}

func TestDataLocator_AtKey(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input    string
		names    []string
		want     string
		wantNear string
	}{
		"key the decoder respells": {
			input: respelledDoc,
			names: []string{"ports", "16"},
			want:  "f.yaml:2:3: $.ports.0x10~: x",
		},
		"explicit key": {
			input: "? 0x10\n: x\n",
			names: []string{"16"},
			want:  "f.yaml:1:3: $.0x10~: x",
		},
		"key of a merge source": {
			input: mergeDoc,
			names: []string{"m", "true"},
			want:  "f.yaml:6:3: $.m.True~: x",
		},
		"key no path selects": {
			input: hiddenDoc,
			names: []string{"user", "16"},
			want:  "f.yaml:2:8: $.user.16~: x",
		},
		"element has no key": {
			input: respelledDoc,
			names: []string{"items", "0"},
			want:  "f.yaml:5:5: $.items[0]~: x",
		},
		"no names": {
			input: respelledDoc,
			want:  "f.yaml:1:1: $~: x",
		},
		"member a mapping lacks": {
			input:    lackingDoc,
			names:    []string{"ports", "16", "name"},
			want:     "f.yaml:2:3: $.ports.0x10.name: x",
			wantNear: "$.ports.0x10",
		},
		"name a key spells for another member": {
			input: pythonDoc,
			names: []string{"python", "3.10"},
			want:  "f.yaml:1:1: $.python.'3.10': x",
		},
		"index past the end": {
			input: shapesDoc,
			names: []string{"seq", "9"},
			want:  "f.yaml: $.seq[9]: x",
		},
		"member below an alias to a reference document": {
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

			got, gotNear := located(t, doc, doc.DataLocator().AtKey(tc.names...))

			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.wantNear, gotNear)
		})
	}
}

func TestDataLocator_At_Options(t *testing.T) {
	t.Parallel()

	doc := locatorDoc(t, hiddenDoc)
	loc := doc.DataLocator()

	other := niceyaml.AtPath(paths.Doc().Child("other"))
	marked := niceyaml.AtRange(position.NewRange(position.New(0, 0), position.New(0, 4)))

	tcs := map[string]struct {
		want string
		opts []niceyaml.ErrorOption
	}{
		"replaces a path set before it": {
			opts: []niceyaml.ErrorOption{other, loc.At("user", "0x10")},
			want: "f.yaml:3:11: $.user.0x10: x",
		},
		"a later path replaces it": {
			opts: []niceyaml.ErrorOption{loc.At("user", "0x10"), niceyaml.AtPath(paths.Doc().Child("user"))},
			want: "f.yaml:2:3: $.user: x",
		},
		"keeps a range set before it where the path selects the value": {
			opts: []niceyaml.ErrorOption{marked, loc.At("user", "0x10")},
			want: "f.yaml:1:1: $.user.0x10: x",
		},
		"replaces a range set before it where no path selects the value": {
			opts: []niceyaml.ErrorOption{marked, loc.At("user", "16")},
			want: "f.yaml:2:14: $.user.16: x",
		},
		"a later range replaces its position": {
			opts: []niceyaml.ErrorOption{loc.At("user", "16"), marked},
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

func TestDataLocator_Names(t *testing.T) {
	t.Parallel()

	// The names a locator reads are those go-yaml gives the keys of a
	// decode into any, so each key here pins one of them.
	input := "0x10: a\n3.10: b\nTrue: c\n~: d\n1234567.0: e\n!!binary aGk=: f\n1e3: g\n\"0x20\": h\n"
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

	got := make([]string, 0, len(members))
	for name := range members {
		got = append(got, name)
	}

	wantNames := make([]string, 0, len(want))
	for name := range want {
		wantNames = append(wantNames, name)
	}

	assert.ElementsMatch(t, wantNames, got)

	loc := doc.DataLocator()

	for name, line := range want {
		var bound *niceyaml.SourceError

		require.ErrorAs(t, doc.Bind(niceyaml.NewError("x", loc.AtKey(name))), &bound)

		pos, ok := bound.Position()
		require.True(t, ok, "name %q binds with no position", name)
		assert.Equal(t, line-1, pos.Line, "name %q", name)
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

func TestDataLocator_Pointer(t *testing.T) {
	t.Parallel()

	// The key a/b~c holds both characters a JSON Pointer escapes, and the
	// empty key takes an empty name.
	doc := locatorDoc(t, "\"a/b~c\":\n  \"\": x\nitems:\n  - 0x10: y\n")
	loc := doc.DataLocator()

	tcs := map[string]struct {
		pointer string
		want    string
	}{
		"whole document": {
			pointer: "",
			want:    "f.yaml:1:1: $: x",
		},
		"escaped slash and tilde": {
			pointer: "/a~1b~0c",
			want:    "f.yaml:2:3: $.'a/b~c': x",
		},
		"empty name": {
			pointer: "/a~1b~0c/",
			want:    "f.yaml:2:7: $.'a/b~c'.'': x",
		},
		"index and respelled key": {
			pointer: "/items/0/16",
			want:    "f.yaml:4:11: $.items[0].0x10: x",
		},
		"escape of an escape": {
			pointer: "/~01",
			want:    "f.yaml:1:1: $.'~1': x",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, _ := located(t, doc, loc.At(pointerNames(tc.pointer)...))

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestDataLocator_Scope(t *testing.T) {
	t.Parallel()

	doc := locatorDoc(t, "- name: \"\"\n- spec:\n    ports:\n      0x10:\n        name: \"\"\n")
	spec := yamltest.At(t, doc, paths.Doc().Index(1).Child("spec"))

	// The names read from the scoped Node, and the path starts at the root
	// of the document.
	err := niceyaml.NewError("x", spec.DataLocator().At("ports", "16", "name"))

	path, ok := err.Path()
	require.True(t, ok)
	assert.Equal(t, "$[1].spec.ports.0x10.name", path.String())

	const want = "f.yaml:5:15: $[1].spec.ports.0x10.name: x"

	require.EqualError(t, spec.Bind(err), want)
	require.EqualError(t, doc.Bind(err), want)

	// A base moves `@` paths alone, so the error binds at the same value
	// under any base.
	require.EqualError(t, doc.Bind(niceyaml.Rebase(err, paths.Doc().Child("other"))), want)

	t.Run("names of the document miss from the scope", func(t *testing.T) {
		t.Parallel()

		got, _ := located(t, spec, spec.DataLocator().At("1", "spec", "ports"))

		assert.Equal(t, "f.yaml:2:3: $[1].spec.1.spec.ports: x", got)
	})

	t.Run("scope behind an alias", func(t *testing.T) {
		t.Parallel()

		aliased := locatorDoc(t, "base: &base\n  0x10: x\nal: *base\n")
		al := yamltest.At(t, aliased, paths.Doc().Child("al"))

		got, _ := located(t, al, al.DataLocator().At("16"))

		assert.Equal(t, "f.yaml:2:9: $.al.0x10: x", got)
	})
}

func TestDataLocator_SyntaxError(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: [1\n", niceyaml.WithName("f.yaml"))

	// The document comes back beside its syntax error.
	docs, err := source.Documents()
	require.ErrorIs(t, err, niceyaml.ErrSyntax)
	require.Len(t, docs, 1)

	doc := docs[0]

	var bound *niceyaml.SourceError

	require.ErrorAs(t, doc.Bind(niceyaml.NewError("x", doc.DataLocator().At("a", "0"))), &bound)
	require.EqualError(t, bound, "f.yaml: $.a.0: x")
	require.ErrorIs(t, bound.Unresolved(), niceyaml.ErrPathNeedsDocument)
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

	for bound := range niceyaml.AllBindings(err) {
		// The summary of several violations is no violation.
		if len(bound.Errors()) == 0 {
			got = append(got, describeLocation(bound))
		}
	}

	slices.Sort(got)

	return got
}

// locatorLocations returns where a [niceyaml.DataLocator] of doc binds an
// error for each failure v finds in the data of doc. The locator takes
// the instance location of the failure as names. A failure about a
// missing member takes the name of that member too, and one about a key
// goes through [niceyaml.DataLocator.AtKey].
func locatorLocations(t *testing.T, doc *niceyaml.Node, v *jsonschema.Validator) []string {
	t.Helper()

	data, err := doc.Decode[any](t.Context())
	require.NoError(t, err)

	var failure *jsonschema.ValidationError

	require.ErrorAs(t, v.Validate(t.Context(), data), &failure)

	loc := doc.DataLocator()

	var got []string

	for _, leaf := range failure.Leaves() {
		var names []string

		for _, seg := range leaf.InstanceSegments() {
			if seg.IsIndex {
				names = append(names, strconv.Itoa(seg.Index))
			} else {
				names = append(names, seg.Key)
			}
		}

		// Each failure takes one lookup, as it does in the schema, so both
		// read the merge sources of a document the same number of times.
		var opt niceyaml.ErrorOption

		quoted, required := strings.CutPrefix(leaf.Message, "missing required property ")

		switch {
		case required:
			missing, err := strconv.Unquote(quoted)
			require.NoError(t, err)

			opt = loc.At(append(names, missing)...)

		case leaf.TargetsKey():
			opt = loc.AtKey(names...)

		default:
			opt = loc.At(names...)
		}

		var bound *niceyaml.SourceError

		require.ErrorAs(t, doc.Bind(niceyaml.NewError("x", opt)), &bound)

		got = append(got, describeLocation(bound))
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

// wideMergeDoc returns a document whose mapping m holds members keys,
// each spelled in hexadecimal, and then a merge key that lists as many
// aliases to one mapping. To tell which entry the spelling of one of
// those keys selects, a lookup reads that whole list.
func wideMergeDoc(members int) string {
	var sb strings.Builder

	sb.WriteString("a: &a {z: 0}\nm:\n")

	for i := range members {
		fmt.Fprintf(&sb, "  %#x: x\n", 0x1000+i)
	}

	sb.WriteString("  <<: [" + strings.Repeat("*a, ", members-1) + "*a]\n")

	return sb.String()
}

func TestDataLocator_Schema(t *testing.T) {
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
		"alias key":                "k: &k 0x10\nm:\n  *k : {0x20: v}\n  \"16\": w\n",
		"merge sources in a list": "a: &a {0x10: 1, b: 2}\nb: &b {16: 3, 0x10: 4}\n" +
			"m:\n  <<: [*a, *b]\n  b: 5\n  <<: *a\n",
		"merge key after its keys":       "base: &base {0x10: {a: 1}}\nm:\n  0x10: {b: 2}\n  0o20: {c: 3}\n  <<: *base\n",
		"keys below one no path selects": "user:\n  <<: {0x10: {0x20: [x, {0x30: y}]}}\n  \"0x10\": 1\n",
		"aliased mapping in a sequence":  "base: &base {0x10: x, 3.10: y}\nseq:\n  - *base\n  - [*base, {True: z}]\n",
		"null and bool keys":             "~: a\nnull: b\nTrue: {true: c}\nfalse: [d]\n",
		// The lookups of both runs pass the limit on the merge sources they
		// read, so each writes the same names past it.
		"more merge reads than the limit allows": wideMergeDoc(2000),
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
		fmt.Fprintf(&sb, "m%d:\n  <<: *base\n", m)

		for i := range members {
			fmt.Fprintf(&sb, "  %#x: {name: \"\"}\n", 0x10+i)
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
			got, _ := located(t, doc, single.At(names(m, i)...))
			want = append(want, got)
		}
	}

	require.Equal(t, "f.yaml:4:16: $.m0.0x10.name: x", want[0])

	shared := doc.DataLocator()
	results := make([][]niceyaml.ErrorOption, 8)

	var wg sync.WaitGroup

	for g := range results {
		wg.Go(func() {
			opts := make([]niceyaml.ErrorOption, 0, mappings*members)

			for m := range mappings {
				for i := range members {
					opts = append(opts, shared.At(names(m, i)...))
				}
			}

			// Each goroutine also reads members the others do not.
			_ = shared.AtKey(names(g, g)...)
			_ = shared.At("m"+strconv.Itoa(g), "1", "missing")

			results[g] = opts
		})
	}

	wg.Wait()

	for _, opts := range results {
		got := make([]string, 0, len(opts))

		for _, opt := range opts {
			message, _ := located(t, doc, opt)
			got = append(got, message)
		}

		assert.Equal(t, want, got)
	}
}

func TestDataLocator_MergeReads(t *testing.T) {
	t.Parallel()

	// To spell a key of m, the locator reads the whole list of its merge
	// key, and the locations of one locator share one limit on those
	// reads. Past it, each path keeps the decoded name of its key, and the
	// error still binds at the value, by its position.
	const members = 2000

	doc := locatorDoc(t, wideMergeDoc(members))
	loc := doc.DataLocator()

	spelled := 0

	for i := range members {
		err := niceyaml.NewError("x", loc.At("m", strconv.Itoa(0x1000+i)))

		path, ok := err.Path()
		require.True(t, ok)

		var bound *niceyaml.SourceError

		require.ErrorAs(t, doc.Bind(err), &bound)

		pos, ok := bound.Position()
		require.True(t, ok, "member %d binds with no position", i)
		require.Equal(t, i+2, pos.Line, "member %d", i)

		if strings.HasPrefix(path.String(), "$.m.0x") {
			spelled++

			continue
		}

		require.Equal(t, "$.m."+strconv.Itoa(0x1000+i), path.String())
	}

	assert.Positive(t, spelled)
	assert.Less(t, spelled, members)
}
