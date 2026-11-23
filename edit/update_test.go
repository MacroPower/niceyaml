package edit_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/edit"
	"go.jacobcolvin.com/niceyaml/encoder"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

type updateServer struct {
	Host    string   `yaml:"host"`
	Origins []string `yaml:"origins"`
	Port    int      `yaml:"port"`
	TLS     bool     `yaml:"tls"`
}

type updateConfig struct {
	Server updateServer `yaml:"server"`
}

// ms returns a mapping of the keys and values of kvs in order.
func ms(kvs ...any) yaml.MapSlice {
	out := make(yaml.MapSlice, 0, len(kvs)/2)
	for i := 0; i+1 < len(kvs); i += 2 {
		out = append(out, yaml.MapItem{Key: kvs[i], Value: kvs[i+1]})
	}

	return out
}

func TestNode_Update(t *testing.T) {
	t.Parallel()

	config := stringtest.LinesLF(
		"# service config",
		"server:",
		"  # bind address",
		"  host: 0.0.0.0   # all interfaces",
		"  port: 8080      # behind the LB",
		"",
		"  tls: false",
		"  origins:",
		"    - https://a.example  # prod",
		"    - https://b.example",
	)

	// A sequence of more items than the alignment compares, whose new value
	// takes its last item first. So the two do not share an item at their
	// start or at their end, and each item keeps its comment at its index.
	const many = 1025

	var manyBefore, manyAfter strings.Builder

	manyItems := make([]string, 0, many)

	for i := range many {
		item := fmt.Sprintf("i%d", (i+many-1)%many)
		manyItems = append(manyItems, item)

		fmt.Fprintf(&manyBefore, "- i%d  # c%d\n", i, i)
		fmt.Fprintf(&manyAfter, "- %s  # c%d\n", item, i)
	}

	tcs := map[string]struct {
		value  any
		before string
		path   paths.Path
		opts   []edit.SetOption
		want   string
		err    string
		// The text of a reference document of the document, or "".
		ref string
		// The removals that the Update records, by path.
		removals []string
		// Whether the error is an *edit.UnsupportedError of
		// edit.ConstructMergeKeys, and whether it matches edit.ErrUsage.
		unsupported bool
		usage       bool
		// Whether a Set of the value at the path fails with the same
		// problem, which names the Set.
		likeSet bool
		// Whether the error is an engine error, which fails the Apply.
		engine bool
		// Whether the Update panics, so that the Apply fails with an error
		// that holds a niceyaml.PanicError in place of the error of the
		// Update.
		panicked bool
	}{
		"field of a section": {
			before: config,
			path:   paths.Doc().Child("server"),
			value: updateServer{
				Host: "0.0.0.0", Port: 9090,
				Origins: []string{"https://a.example", "https://b.example", "https://c.example"},
			},
			want: stringtest.LinesLF(
				"# service config",
				"server:",
				"  # bind address",
				"  host: 0.0.0.0   # all interfaces",
				"  port: 9090      # behind the LB",
				"",
				"  tls: false",
				"  origins:",
				"    - https://a.example  # prod",
				"    - https://b.example",
				"    - https://c.example",
			),
		},
		"whole document": {
			before: config,
			path:   paths.Doc(),
			value: updateConfig{Server: updateServer{
				Host: "0.0.0.0", Port: 8080, Origins: []string{"https://b.example"},
			}},
			want: stringtest.LinesLF(
				"# service config",
				"server:",
				"  # bind address",
				"  host: 0.0.0.0   # all interfaces",
				"  port: 8080      # behind the LB",
				"",
				"  tls: false",
				"  origins:",
				"    - https://b.example",
			),
			removals: []string{"$.server.origins[0]"},
		},
		"equal value": {
			before: config,
			path:   paths.Doc(),
			value: updateConfig{Server: updateServer{
				Host: "0.0.0.0", Port: 8080, Origins: []string{"https://a.example", "https://b.example"},
			}},
			want: config,
		},
		"removed key": {
			before:   stringtest.LinesLF("a: 1  # one", "# about b", "b: 2", "c: 3"),
			path:     paths.Doc(),
			value:    ms("a", 1, "c", 3),
			want:     stringtest.LinesLF("a: 1  # one", "c: 3"),
			removals: []string{"$.b~"},
		},
		"keys that the update removes with a blank line between them": {
			before:   stringtest.LinesLF("x:", "  a: 1", "", "  b: 2", "y: 3"),
			path:     paths.Doc().Child("x"),
			value:    ms(),
			want:     stringtest.LinesLF("x: {}", "y: 3"),
			removals: []string{"$.x.b~", "$.x.a~"},
		},
		"keys that the update removes after a block scalar": {
			before:   stringtest.LinesLF("x:", "  a: |", "    t", "", "  b: 2", "  c: 3"),
			path:     paths.Doc().Child("x"),
			value:    ms("c", 3),
			want:     stringtest.LinesLF("x:", "  c: 3"),
			removals: []string{"$.x.b~", "$.x.a~"},
		},
		"keys that the update removes in flow style": {
			before:   "m: {a: 1, b: 2, c: 3, d: 4}  # m\n",
			path:     paths.Doc().Child("m"),
			value:    ms("a", 1, "d", 4),
			want:     "m: {a: 1, d: 4}  # m\n",
			removals: []string{"$.m.c~", "$.m.b~"},
		},
		"items that the update removes with a blank line between them": {
			before:   stringtest.LinesLF("l:", "  - 1", "  - 2", "", "  - 3", "z: 0"),
			path:     paths.Doc().Child("l"),
			value:    []int{1},
			want:     stringtest.LinesLF("l:", "  - 1", "z: 0"),
			removals: []string{"$.l[2]", "$.l[1]"},
		},
		"comment line between keys that the update removes": {
			before:   stringtest.LinesLF("x:", "  a: 1", "# kept note", "  b: 2", "  c: 3"),
			path:     paths.Doc().Child("x"),
			value:    ms("c", 3),
			want:     stringtest.LinesLF("x:", "# kept note", "  c: 3"),
			removals: []string{"$.x.b~", "$.x.a~"},
		},
		"comment line between all the keys of a mapping": {
			before:   stringtest.LinesLF("x:", "  a: 1", "#  old: 9", "  b: 2"),
			path:     paths.Doc().Child("x"),
			value:    ms(),
			want:     stringtest.LinesLF("x: {}", "#  old: 9"),
			removals: []string{"$.x.b~", "$.x.a~"},
		},
		"comment line between items that the update removes": {
			before:   stringtest.LinesLF("l:", "  - 1", "# note", "  - 2", "  - 3"),
			path:     paths.Doc().Child("l"),
			value:    []int{3},
			want:     stringtest.LinesLF("l:", "# note", "  - 3"),
			removals: []string{"$.l[1]", "$.l[0]"},
		},
		"comment line between removed keys of a mapping on a dash": {
			before:   stringtest.LinesLF("- a: 1", "# note", "  b: 2", "  c: 3"),
			path:     paths.Doc().Index(0),
			value:    ms("c", 3),
			want:     stringtest.LinesLF("-", "# note", "  c: 3"),
			removals: []string{"$[0].b~", "$[0].a~"},
		},
		"keys that the update removes on a dash": {
			before:   stringtest.LinesLF("- a: 1", "  b: 2", "  c: 3"),
			path:     paths.Doc().Index(0),
			value:    ms("c", 3),
			want:     stringtest.LinesLF("- c: 3"),
			removals: []string{"$[0].b~", "$[0].a~"},
		},
		"all the keys of a mapping on a dash": {
			before:   stringtest.LinesLF("- a: 1", "  b: 2", "- 5"),
			path:     paths.Doc().Index(0),
			value:    ms(),
			want:     stringtest.LinesLF("- {}", "- 5"),
			removals: []string{"$[0].b~", "$[0].a~"},
		},
		"keys that the update removes in two mappings": {
			before:   stringtest.LinesLF("x:", "  a:", "    p: 1", "    q: 2", "  b: 3", "  c: 4"),
			path:     paths.Doc().Child("x"),
			value:    ms("a", ms("q", 2)),
			want:     stringtest.LinesLF("x:", "  a:", "    q: 2"),
			removals: []string{"$.x.c~", "$.x.b~", "$.x.a.p~"},
		},
		"keys that the update removes between keys that it keeps": {
			before:   stringtest.LinesLF("x:", "  a: 1", "  b: 2  # two", "  c: 3", "  d: 4", "", "  e: 5", "y: 0"),
			path:     paths.Doc().Child("x"),
			value:    ms("b", 2, "d", 4),
			want:     stringtest.LinesLF("x:", "  b: 2  # two", "  d: 4", "", "y: 0"),
			removals: []string{"$.x.e~", "$.x.c~", "$.x.a~"},
		},
		"keys that the update removes between keys that it keeps in flow style": {
			before:   "m: {a: 1, b: 2, c: 3, d: 4, e: 5}  # m\n",
			path:     paths.Doc().Child("m"),
			value:    ms("b", 2, "d", 4),
			want:     "m: {b: 2, d: 4}  # m\n",
			removals: []string{"$.m.e~", "$.m.c~", "$.m.a~"},
		},
		"items that the update removes between items that it keeps": {
			before:   stringtest.LinesLF("l:", "  - 1", "  - 2", "  - 3", "  - 4", "  - 5", "z: 0"),
			path:     paths.Doc().Child("l"),
			value:    []int{2, 4},
			want:     stringtest.LinesLF("l:", "  - 2", "  - 4", "z: 0"),
			removals: []string{"$.l[4]", "$.l[2]", "$.l[0]"},
		},
		"alias in a removed key after a key that the update keeps": {
			before:   stringtest.LinesLF("x:", "  a: &A 1", "  k: 0", "  b: *A"),
			path:     paths.Doc().Child("x"),
			value:    ms("k", 0),
			want:     stringtest.LinesLF("x:", "  k: 0"),
			removals: []string{"$.x.b~", "$.x.a~"},
		},
		"alias after the mapping that reads a removed key": {
			before: stringtest.LinesLF("x:", "  a: &A 1", "  k: 0", "  b: *A", "y: *A"),
			path:   paths.Doc().Child("x"),
			value:  ms("k", 0),
			err:    "test.yaml:5:4: $.y: cafe.jacobcolvin.com/v1 removes $.x.a, which this alias reads",
		},
		"new keys in the order of the value": {
			before: stringtest.LinesLF("# top", "b: 2  # two", "d: 4"),
			path:   paths.Doc(),
			value:  ms("a", 1, "b", 2, "c", 3, "d", 4, "e", 5),
			want:   stringtest.LinesLF("# top", "a: 1", "b: 2  # two", "c: 3", "d: 4", "e: 5"),
		},
		"scalar of another kind keeps the comment of its entry": {
			before: stringtest.LinesLF("a: 1  # tune me", "b: 2"),
			path:   paths.Doc(),
			value:  ms("a", ms("x", 1), "b", 2),
			want:   stringtest.LinesLF("a: # tune me", "  x: 1", "b: 2"),
		},
		"string keeps its quotes": {
			before: stringtest.LinesLF(`a: "foo"  # q`, "b: 'bar'"),
			path:   paths.Doc(),
			value:  ms("a", "baz", "b", "qux"),
			want:   stringtest.LinesLF(`a: "baz"  # q`, "b: 'qux'"),
		},
		"items that move": {
			before: stringtest.LinesLF("- a  # first", "- b", "- c  # third"),
			path:   paths.Doc(),
			value:  []string{"c", "a", "b"},
			want:   stringtest.LinesLF("- c", "- a  # first", "- b"),
			removals: []string{
				"$[2]",
			},
		},
		"item changed in place": {
			before: stringtest.LinesLF("- a: 1  # one", "  b: 2", "- c"),
			path:   paths.Doc(),
			value:  []any{ms("a", 1, "b", 3), "c"},
			want:   stringtest.LinesLF("- a: 1  # one", "  b: 3", "- c"),
		},
		"item inserted in a flow sequence": {
			before: "tags: [a, c]  # tags\n",
			path:   paths.Doc().Child("tags"),
			value:  []string{"a", "b", "c"},
			want:   "tags: [a, b, c]  # tags\n",
		},
		"place without a value": {
			before: "a: 1\n",
			path:   paths.Doc().Child("b"),
			value:  ms("x", 1),
			want:   stringtest.LinesLF("a: 1", "b:", "  x: 1"),
		},
		"place without a value in flow style": {
			before: "a: 1\n",
			path:   paths.Doc().Child("b"),
			opts:   []edit.SetOption{edit.Flow(), edit.First()},
			value:  ms("x", 1),
			want:   stringtest.LinesLF("b: {x: 1}", "a: 1"),
		},
		"options inside a value that the place holds": {
			before: "a: 1\n",
			path:   paths.Doc(),
			opts:   []edit.SetOption{edit.Flow(), edit.First()},
			value:  ms("a", 1, "b", ms("x", 1)),
			want:   stringtest.LinesLF("a: 1", "b:", "  x: 1"),
		},
		"anchor of a value that the update replaces": {
			// The alias reads the anchor inside the value of a, so it takes
			// its value before the Set of a takes the anchor away.
			before:   stringtest.LinesLF("a: {k: &x 1}", "b: *x"),
			path:     paths.Doc(),
			value:    ms("a", 5, "b", 1),
			want:     stringtest.LinesLF("a: 5", "b: 1"),
			removals: []string{"$.a.k~"},
		},
		"sequence that the update replaces with a scalar": {
			// A sequence is one value, so the Set of l records no removal
			// for its items.
			before: stringtest.LinesLF("l:", "  - a", "  - b: 1"),
			path:   paths.Doc(),
			value:  ms("l", 5),
			want:   "l: 5\n",
		},
		"anchored value that the update removes": {
			before:   stringtest.LinesLF("a: &x 1", "b: *x"),
			path:     paths.Doc(),
			value:    ms("b", 1),
			want:     "b: 1\n",
			removals: []string{"$.a~"},
		},
		"merge key of a reference document": {
			before: stringtest.LinesLF("m:", "  <<: *r", "  a: 1  # own"),
			ref:    "r: &r {q: 1}\n",
			path:   paths.Doc(),
			value:  ms("m", ms("q", 1, "a", 2)),
			want:   stringtest.LinesLF("m:", "  <<: *r", "  a: 2  # own"),
		},
		"merge key that reads a mapping that the update changes": {
			before:      stringtest.LinesLF("base: &b {x: 1}", "m:", "  <<: *b", "  y: 2"),
			path:        paths.Doc(),
			value:       ms("base", ms("x", 2), "m", ms("x", 1, "y", 2)),
			err:         "brings entries into $.m that the update changes",
			unsupported: true,
		},
		"block scalar": {
			before: stringtest.LinesLF("a: |  # text", "  x", "  y", "b: 1"),
			path:   paths.Doc(),
			value:  ms("a", "x\nz\n", "b", 1),
			want:   stringtest.LinesLF("a: | # text", "  x", "  z", "b: 1"),
		},
		"sequence of mappings": {
			before: stringtest.LinesLF("l:", "- [1, 2]", "- {a: 1}  # m"),
			path:   paths.Doc(),
			value:  ms("l", []any{[]int{1, 3}, ms("a", 1, "b", 2)}),
			want:   stringtest.LinesLF("l:", "- [1, 3]", "- {a: 1, b: 2}  # m"),
		},
		"empty mapping": {
			before: "m: {}  # fill\n",
			path:   paths.Doc().Child("m"),
			value:  ms("a", 1),
			want:   stringtest.LinesLF("m:  # fill", "  a: 1"),
		},
		"empty document": {
			before: "# only a comment\n",
			path:   paths.Doc(),
			value:  ms("a", 1),
			want:   stringtest.LinesLF("# only a comment", "a: 1"),
		},
		"items removed, inserted, and changed": {
			before: stringtest.LinesLF(
				"jobs:",
				"  # first",
				"  - name: a",
				"    run: x  # keep",
				"  # second",
				"  - name: b",
				"    run: y",
				"  # third",
				"  - name: c",
				"    run: z",
				"  - name: d",
				"    run: w",
			),
			path: paths.Doc().Child("jobs"),
			value: []any{
				ms("name", "n", "run", "v"),
				ms("name", "a", "run", "x"),
				ms("name", "c", "run", "q", "env", []string{"E"}),
				ms("name", "d", "run", "w"),
				ms("name", "e", "run", "u"),
			},
			// The items b and c lie between the pairs a and d, and the new
			// value holds one item there. So b takes that item, and c goes
			// with its comment.
			want: stringtest.LinesLF(
				"jobs:",
				`  - name: "n"`,
				"    run: v",
				"  # first",
				"  - name: a",
				"    run: x  # keep",
				"  # second",
				"  - name: c",
				"    run: q",
				"    env:",
				"      - E",
				"  - name: d",
				"    run: w",
				"  - name: e",
				"    run: u",
			),
			removals: []string{"$.jobs[2]"},
		},
		"new item whose last value is a block scalar": {
			before: stringtest.LinesLF("jobs:", "- name: a", "  run: x"),
			path:   paths.Doc(),
			value: ms("jobs", []any{
				ms("name", "a", "run", "x"),
				ms("name", "b", "run", "echo 1\necho 2\n"),
			}),
			want: stringtest.LinesLF(
				"jobs:", "- name: a", "  run: x", "- name: b", "  run: |", "    echo 1", "    echo 2",
			),
		},
		"new entry whose last value is a block scalar": {
			before: "a: 1\n",
			path:   paths.Doc(),
			value:  ms("a", 1, "b", ms("s", "x\ny\n")),
			want:   stringtest.LinesLF("a: 1", "b:", "  s: |", "    x", "    y"),
		},
		"scalar that becomes a mapping whose last value is a block scalar": {
			before: stringtest.LinesLF("a: 1  # one", "b: 2"),
			path:   paths.Doc(),
			value:  ms("a", ms("s", "x\ny\n"), "b", 2),
			want:   stringtest.LinesLF("a: # one", "  s: |", "    x", "    y", "b: 2"),
		},
		"block scalar with a tag": {
			before: stringtest.LinesLF("m:", "  a: 1", "  b: 2"),
			path:   paths.Doc().Child("m"),
			value:  ms("a", yamlMarshaler("!custom |\n  line1\n  line2\n"), "b", 2),
			want:   stringtest.LinesLF("m:", "  a: !custom |", "    line1", "    line2", "  b: 2"),
		},
		"new entry whose last value keeps its line breaks": {
			before: "a: 1\n",
			path:   paths.Doc(),
			value:  ms("a", 1, "b", ms("s", "x\n\n")),
			want:   stringtest.LinesLF("a: 1", "b:", "  s: |+", "    x", ""),
		},
		"existing item": {
			before: stringtest.LinesLF("l:", "- 1  # one", "- {a: 1}  # two"),
			path:   paths.Doc().Child("l").Index(1),
			value:  ms("a", 1, "b", 2),
			want:   stringtest.LinesLF("l:", "- 1  # one", "- {a: 1, b: 2}  # two"),
		},
		"value that does not encode": {
			before:  stringtest.LinesLF("m:", "  a: 1"),
			path:    paths.Doc().Child("m"),
			value:   ms("a", func() {}),
			err:     "cafe.jacobcolvin.com/v1 cannot write the value of $.m",
			likeSet: true,
		},
		"place below a scalar": {
			before: "a: 1\n",
			path:   paths.Doc().Child("a").Child("b"),
			value:  1,
			err:    "updates $.a.b, but a holds a scalar",
		},
		"entry that it removes between a blank line of a block scalar without text and a comment line": {
			// Go-yaml's parser refuses a comment line right after the blank
			// line, so the document is at fault.
			before: stringtest.LinesLF("a: |", "", "z: 1", "# c", "b: 1"),
			path:   paths.Doc(),
			value:  ms("a", "", "b", 1),
			err: "test.yaml:3:4: $.z: cafe.jacobcolvin.com/v1 removes $.z, after which a comment line goes right " +
				"after the blank lines of a block scalar without text",
		},
		"later entry that it removes between a blank line of a block scalar without text and a comment line": {
			// The problem names the entry whose removal leaves the comment
			// line after the blank line, not the first entry that the
			// Update removes.
			before: stringtest.LinesLF("x:", "  a: 1", "  s: |", "", "  b: 2", "# note", "  c: 3", "  d: 4"),
			path:   paths.Doc().Child("x"),
			value:  ms("s", "", "d", 4),
			err: "test.yaml:5:6: $.x.b: cafe.jacobcolvin.com/v1 removes $.x.b, after which a comment line goes right " +
				"after the blank lines of a block scalar without text",
		},
		"later entry that it removes before a comment line with a kept entry between": {
			before: stringtest.LinesLF("x:", "  a: 1", "  k: 0", "  s: |", "", "  b: 2", "# note", "  d: 4"),
			path:   paths.Doc().Child("x"),
			value:  ms("k", 0, "s", "", "d", 4),
			err: "test.yaml:6:6: $.x.b: cafe.jacobcolvin.com/v1 removes $.x.b, after which a comment line goes right " +
				"after the blank lines of a block scalar without text",
		},
		"block scalar without text from a marshaler after the last key before a blank line and a comment line": {
			before: stringtest.LinesLF("a: 1", "", "# c"),
			path:   paths.Doc(),
			value:  ms("a", 1, "z", yamlMarshaler("k: |\n")),
			want:   stringtest.LinesLF("a: 1", "z:", `  k: ""`, "", "# c"),
		},
		"block scalar without text from a marshaler as the value of the last key before a blank line and a comment line": {
			// The value writes as "", as it does in any place.
			before: stringtest.LinesLF("a: 1", "", "# c"),
			path:   paths.Doc(),
			value:  ms("a", 1, "z", yamlMarshaler("|\n")),
			want:   stringtest.LinesLF("a: 1", `z: ""`, "", "# c"),
		},
		"value that does not encode at a new place": {
			before:  "a: 1\n",
			path:    paths.Doc().Child("b"),
			value:   ms("x", func() {}),
			err:     "cafe.jacobcolvin.com/v1 cannot write the value of $.b",
			likeSet: true,
		},
		"value with a byte that is not UTF-8": {
			before:  stringtest.LinesLF("m:", "  a: 1"),
			path:    paths.Doc().Child("m"),
			value:   ms("a", "x\xffy"),
			err:     "value does not encode: string holds the byte 0xff, which is not UTF-8",
			likeSet: true,
		},
		"value whose marshaler writes a control character in a tag": {
			// The encoder quotes only strings, so the tag keeps U+0001 as it is.
			before:  stringtest.LinesLF("m:", "  a: 1"),
			path:    paths.Doc().Child("m"),
			value:   ms("a", yamlMarshaler("!t\x01 v")),
			err:     "go-yaml's encoder writes U+0001 as it is",
			likeSet: true,
			engine:  true,
		},
		"value that repeats a key": {
			before:  stringtest.LinesLF("m:", "  a: 1"),
			path:    paths.Doc().Child("m"),
			value:   ms("a", yamlMarshaler("x: 1\nx: 2\n")),
			err:     "writes a mapping that repeats the key x, which YAML does not allow",
			likeSet: true,
		},
		"value whose marshaler panics": {
			// The panic leaves the call of the Func, so the error binds at the
			// place of that call.
			before:   stringtest.LinesLF("m:", "  a: 1"),
			path:     paths.Doc().Child("m"),
			value:    ms("a", panicYAMLMarshaler{}),
			err:      "test.yaml:1:1: $: cafe.jacobcolvin.com/v1 panicked: edit_test.panicYAMLMarshaler",
			panicked: true,
		},
		"value whose YAML holds an anchor": {
			before:  stringtest.LinesLF("m:", "  a: 1"),
			path:    paths.Doc().Child("m"),
			value:   ms("a", yamlMarshaler("{x: &x 1, y: *x}")),
			err:     "updates $.m to a value whose YAML holds the anchor &x",
			likeSet: true,
		},
		"null that becomes a mapping": {
			before: "a: # todo\nb: 1\n",
			path:   paths.Doc().Child("a"),
			value:  ms("x", 1),
			want:   stringtest.LinesLF("a: # todo", "  x: 1", "b: 1"),
		},
		"alias whose anchor holds the new value": {
			before: stringtest.LinesLF("base: &b {x: 1}", "copy: *b"),
			path:   paths.Doc(),
			value:  ms("base", ms("x", 1), "copy", ms("x", 1)),
			want:   stringtest.LinesLF("base: &b {x: 1}", "copy: *b"),
		},
		"alias whose anchor the update changes": {
			before: stringtest.LinesLF("base: &b {x: 1}", "copy: *b  # c"),
			path:   paths.Doc(),
			value:  ms("base", ms("x", 2), "copy", ms("x", 1)),
			want:   stringtest.LinesLF("base: &b {x: 2}", "copy: # c", "  x: 1"),
		},
		"alias whose value changes": {
			before: stringtest.LinesLF("base: &b 1", "copy: *b"),
			path:   paths.Doc(),
			value:  ms("base", 1, "copy", 2),
			want:   stringtest.LinesLF("base: &b 1", "copy: 2"),
		},
		"own entry beside a merge key": {
			before: stringtest.LinesLF("base: &b {x: 1}", "m:", "  <<: *b", "  y: 2  # own"),
			path:   paths.Doc().Child("m"),
			value:  ms("x", 1, "y", 3),
			want:   stringtest.LinesLF("base: &b {x: 1}", "m:", "  <<: *b", "  y: 3  # own"),
		},
		"new entry beside a merge key": {
			before: stringtest.LinesLF("base: &b {x: 1}", "m:", "  <<: *b", "  y: 2", "z: 0"),
			path:   paths.Doc().Child("m"),
			value:  ms("w", 3, "x", 1, "y", 2),
			want:   stringtest.LinesLF("base: &b {x: 1}", "m:", "  <<: *b", "  y: 2", "  w: 3", "z: 0"),
		},
		"merge key of a changed mapping that reads a mapping that the update changes": {
			before:      stringtest.LinesLF("base: &b {x: 1}", "m:", "  <<: *b", "  y: 2"),
			path:        paths.Doc(),
			value:       ms("base", ms("x", 2), "m", ms("x", 1, "y", 3)),
			err:         "brings entries into $.m that the update changes",
			unsupported: true,
		},
		"entry that a merge key brings in": {
			before:      stringtest.LinesLF("base: &b {x: 1}", "m:", "  <<: *b", "  y: 2"),
			path:        paths.Doc().Child("m"),
			value:       ms("x", 5, "y", 2),
			err:         "test.yaml:3:3: $.m: cafe.jacobcolvin.com/v1 updates $.m, but the `<<` merge key at 3:3 brings x into $.m with another value",
			unsupported: true,
		},
		"entry that a merge key of a nested mapping brings in": {
			before:      stringtest.LinesLF("base: &b {x: 1}", "m:", "  <<: *b", "  y: 2"),
			path:        paths.Doc(),
			value:       ms("base", ms("x", 1), "m", ms("x", 5, "y", 2)),
			err:         "test.yaml:3:3: $.m: cafe.jacobcolvin.com/v1 updates $, but the `<<` merge key at 3:3 brings x into $.m with another value",
			unsupported: true,
		},
		"entry that a merge key of a mapping in a sequence brings in": {
			before:      stringtest.LinesLF("base: &b {x: 1}", "l:", "- <<: *b", "  y: 2"),
			path:        paths.Doc(),
			value:       ms("base", ms("x", 1), "l", []any{ms("x", 5, "y", 2)}),
			err:         "brings x into $.l[0] with another value",
			unsupported: true,
		},
		"entry that a merge key brings in and the value lacks": {
			before:      stringtest.LinesLF("base: &b {x: 1}", "m:", "  <<: *b", "  y: 2"),
			path:        paths.Doc().Child("m"),
			value:       ms("y", 2),
			err:         "brings x into $.m that the value does not hold",
			unsupported: true,
		},
		"anchor and alias that the update removes": {
			before:   stringtest.LinesLF("m:", "  base: &a 1", "  r: *a", "z: 0"),
			path:     paths.Doc().Child("m"),
			value:    ms(),
			want:     stringtest.LinesLF("m: {}", "z: 0"),
			removals: []string{"$.m.r~", "$.m.base~"},
		},
		"anchor and alias that the update removes beside a new entry": {
			before:   stringtest.LinesLF("m:", "  base: &a 1", "  r: *a", "z: 0"),
			path:     paths.Doc().Child("m"),
			value:    ms("k", 2),
			want:     stringtest.LinesLF("m:", "  k: 2", "z: 0"),
			removals: []string{"$.m.r~", "$.m.base~"},
		},
		"alias item that the update removes with its anchor": {
			before:   "m: {base: &a 1, l: [*a, 0]}\n",
			path:     paths.Doc().Child("m"),
			value:    ms("l", []int{0}),
			want:     "m: {l: [0]}\n",
			removals: []string{"$.m.l[0]", "$.m.base~"},
		},
		"merge key that the update replaces with its anchor": {
			before:   "m: {base: &a {x: 1}, r: {<<: *a, y: 1}}\n",
			path:     paths.Doc().Child("m"),
			value:    ms("base", 3, "r", []int{1}),
			want:     "m: {base: &a 3, r: [1]}\n",
			removals: []string{"$.m.r.y~", "$.m.base.x~"},
		},
		"merge key that the update removes with its anchor": {
			before:   "m: {base: &a {x: 1}, r: {<<: *a, y: 1}}\n",
			path:     paths.Doc().Child("m"),
			value:    ms(),
			want:     "m: {}\n",
			removals: []string{"$.m.r~", "$.m.base~"},
		},
		"alias item of a kept sequence whose anchor the update changes": {
			before: stringtest.LinesLF("a: &x 1", "l: [*x, 2]  # l"),
			path:   paths.Doc(),
			value:  ms("a", 3, "l", []int{1, 2}),
			want:   stringtest.LinesLF("a: &x 3", "l: [1, 2]  # l"),
		},
		"own entry beside a merge key of a sequence": {
			before: stringtest.LinesLF("a: &a {x: 1}", "b: &b {y: 2}", "m:", "  <<: [*a, *b]", "  z: 3  # own"),
			path:   paths.Doc().Child("m"),
			value:  ms("x", 1, "y", 2, "z", 4),
			want:   stringtest.LinesLF("a: &a {x: 1}", "b: &b {y: 2}", "m:", "  <<: [*a, *b]", "  z: 4  # own"),
		},
		"entry that a merge key of a sequence brings in": {
			before:      stringtest.LinesLF("a: &a {x: 1}", "b: &b {y: 2}", "m:", "  <<: [*a, *b]", "  z: 3"),
			path:        paths.Doc().Child("m"),
			value:       ms("x", 1, "y", 5, "z", 3),
			err:         "brings y into $.m with another value",
			unsupported: true,
		},
		"merge key of a sequence that reads a mapping that the update changes": {
			before: stringtest.LinesLF("a: &a {x: 1}", "b: &b {y: 2}", "m:", "  <<: [*a, *b]", "  z: 3"),
			path:   paths.Doc(),
			value: ms(
				"a", ms("x", 1), "b", ms("y", 5), "m", ms("x", 1, "y", 2, "z", 3),
			),
			err:         "brings entries into $.m that the update changes",
			unsupported: true,
		},
		"own entry that overrides an entry that a merge key brings in": {
			before: stringtest.LinesLF("base: &b {x: 1}", "m:", "  <<: *b", "  x: 2  # own"),
			path:   paths.Doc().Child("m"),
			value:  ms("x", 3),
			want:   stringtest.LinesLF("base: &b {x: 1}", "m:", "  <<: *b", "  x: 3  # own"),
		},
		"own entry that overrides an entry that a merge key brings in and that the update keeps": {
			before: stringtest.LinesLF("base: &b {x: 1}", "m:", "  <<: *b", "  x: 2  # own", "  y: 1"),
			path:   paths.Doc().Child("m"),
			value:  ms("x", 2, "y", 3),
			want:   stringtest.LinesLF("base: &b {x: 1}", "m:", "  <<: *b", "  x: 2  # own", "  y: 3"),
		},
		"entry of the empty key beside a changed entry": {
			before: stringtest.LinesLF("m:", "  a: 1", `  "": 2  # empty`),
			path:   paths.Doc().Child("m"),
			value:  ms("a", 2, "", 2),
			want:   stringtest.LinesLF("m:", "  a: 2", `  "": 2  # empty`),
		},
		"changed entry of the empty key": {
			before: stringtest.LinesLF("m:", "  a: 1", `  "": 2  # empty`),
			path:   paths.Doc().Child("m"),
			value:  ms("a", 1, "", 3),
			want:   stringtest.LinesLF("m:", "  a: 1", `  "": 3  # empty`),
		},
		"removed entry of the empty key": {
			before:   stringtest.LinesLF("m:", "  a: 1", `  "": 2`),
			path:     paths.Doc().Child("m"),
			value:    ms("a", 1),
			want:     stringtest.LinesLF("m:", "  a: 1"),
			removals: []string{"$.m.''~"},
		},
		"new entry of the empty key": {
			before: stringtest.LinesLF("m:", "  a: 1"),
			path:   paths.Doc().Child("m"),
			value:  ms("a", 1, "", 2),
			want:   stringtest.LinesLF("m:", "  a: 1", `  "": 2`),
		},
		"sequence of more items than the alignment compares": {
			before: manyBefore.String(),
			path:   paths.Doc(),
			value:  manyItems,
			want:   manyAfter.String(),
		},
		"root of another kind": {
			before: "a: 1\n",
			path:   paths.Doc(),
			value:  []int{1},
			err:    "cafe.jacobcolvin.com/v1 updates $, which replaces the root of the document",
			usage:  true,
		},
		"scalar root": {
			before: "1\n",
			path:   paths.Doc(),
			value:  2,
			err:    "cafe.jacobcolvin.com/v1 updates $, which replaces the root of the document",
			usage:  true,
		},
		"scalar root that the update keeps": {
			before: "1  # one\n",
			path:   paths.Doc(),
			value:  1,
			want:   "1  # one\n",
		},
		"protected value that the update keeps": {
			before: stringtest.LinesLF("apiVersion: v1", "kind: K", "spec: 1"),
			path:   paths.Doc(),
			value:  ms("apiVersion", "v1", "kind", "K", "spec", 2),
			want:   stringtest.LinesLF("apiVersion: v1", "kind: K", "spec: 2"),
		},
		"protected value that the update changes": {
			before: stringtest.LinesLF("apiVersion: v1", "kind: K", "spec: 1"),
			path:   paths.Doc(),
			value:  ms("apiVersion", "v2", "kind", "K", "spec", 1),
			err:    "cafe.jacobcolvin.com/v1 updates $",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := []niceyaml.SourceOption{}
			if tc.ref != "" {
				opts = append(opts, yamltest.References(tc.ref))
			}

			if tc.panicked {
				d := newDoc(t, tc.before, opts...)

				var err error

				require.NotPanics(t, func() {
					err = applyAt(t, d, tc.path, func(ctx context.Context, n *edit.Node) error {
						return n.Update(ctx, tc.value, tc.opts...)
					})
				})
				require.Error(t, err)
				assert.Equal(t, tc.err, err.Error())

				p, ok := errors.AsType[*niceyaml.PanicError](err)
				require.True(t, ok)
				assert.NotEmpty(t, p.Stack)
				require.NotErrorIs(t, err, edit.ErrEngine)
				require.NotErrorIs(t, err, edit.ErrValue)
				assert.False(t, niceyaml.IsInvalid(err))
				require.Equal(t, tc.before, d.Text())
				require.Empty(t, d.Removals())

				return
			}

			d, err := applyCall(t, tc.before, tc.path, opts, tc.engine, func(ctx context.Context, n *edit.Node) error {
				return n.Update(ctx, tc.value, tc.opts...)
			})

			if tc.err != "" {
				require.Error(t, err)
				assert.Contains(t, niceyaml.FormatError(err), tc.err)
				require.Equal(t, tc.before, d.Text())
				require.Empty(t, d.Removals())

				if tc.unsupported {
					var unsupported *edit.UnsupportedError

					require.ErrorAs(t, err, &unsupported)
					require.Equal(t, edit.ConstructMergeKeys, unsupported.Construct)
				}

				if tc.usage {
					require.ErrorIs(t, err, edit.ErrUsage)
				}

				if tc.likeSet {
					_, setErr := applyCall(t, tc.before, tc.path, opts, tc.engine,
						func(ctx context.Context, n *edit.Node) error {
							return n.Set(ctx, tc.value, tc.opts...)
						})
					require.Error(t, setErr)

					// The details differ: a Set replaces the value, so its
					// details name the value that it writes.
					want, _, _ := strings.Cut(niceyaml.FormatError(setErr), "\n")
					got, _, _ := strings.Cut(niceyaml.FormatError(err), "\n")
					assert.Equal(t, strings.ReplaceAll(want, "sets $", "updates $"), got)
				}

				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.want, d.Text())

			var removed []string

			for _, r := range d.Removals() {
				removed = append(removed, r.Path.String())
			}

			require.Equal(t, tc.removals, removed)

			// The document reads as after a Set of the value. A Set fails at
			// the root, which then reads as the encode of the value.
			if tc.path.Len() == 0 {
				enc, err := encoder.Marshal(t.Context(), tc.value)
				require.NoError(t, err)
				require.Equal(t, decodeText(t, string(enc), opts...), decodeText(t, d.Text(), opts...))

				return
			}

			s := newDoc(t, tc.before, opts...)

			require.NoError(t, applyAt(t, s, tc.path, func(ctx context.Context, n *edit.Node) error {
				return n.Set(ctx, tc.value, tc.opts...)
			}))
			require.Equal(t, decodeText(t, s.Text(), opts...), decodeText(t, d.Text(), opts...))
		})
	}
}

func TestNode_Update_Removals_Failed(t *testing.T) {
	t.Parallel()

	// A byte order mark at the start of the key b, which the tokenizer of
	// the Source drops in the text that an Update of $ writes.
	const bom = "\xef\xbb\xbf"

	tcs := map[string]struct {
		v      any
		before string
		path   paths.Path
		err    string
	}{
		"update whose call the check after the write undoes": {
			// The Update drops $.a and records it. The check after the
			// write then finds the byte order mark that go-yaml reads as
			// text and undoes the call. The undo drops the removal with the
			// text of the call. The Update returns nil, and the Apply fails.
			before: stringtest.LinesLF("a: 1", bom+"b: 2"),
			path:   paths.Doc(),
			v:      map[string]int{bom + "b": 2},
			err:    "go-yaml reads the byte order mark at 2:1 as text of the document",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before)

			err := applyAt(t, d, tc.path, func(ctx context.Context, n *edit.Node) error {
				err := n.Update(ctx, tc.v)
				require.NoError(t, err)
				assert.Equal(t, tc.before, d.Text())
				assert.Empty(t, d.Removals())

				return nil
			})
			require.ErrorIs(t, err, edit.ErrEngine)
			require.ErrorContains(t, err, tc.err)
			assert.Equal(t, tc.before, d.Text())
			assert.Empty(t, d.Removals())
		})
	}
}

// TestNode_Update_Scaling checks that the bytes that an Update allocates
// grow linearly with the number of entries that it changes or removes. The
// Update writes all the scalars into the text in one pass, and it removes
// the entries that follow each other in one removal. A pass or a parse for
// each entry copies the whole text for each one, which makes the bytes grow
// with the square of the number. The test reads the allocations of the
// whole process, so it does not run in parallel.
//
//nolint:paralleltest // See above.
func TestNode_Update_Scaling(t *testing.T) {
	// The block mapping at $.m of count entries, each with comments.
	block := func(count int) string {
		var text strings.Builder

		text.WriteString("m:\n")

		for i := range count {
			fmt.Fprintf(&text, "  # about k%d\n  k%d: %d  # was %d\n", i, i, i, i)
		}

		return text.String()
	}

	// The flow mapping at $.m of count entries, on one line.
	flow := func(count int) string {
		entries := make([]string, 0, count)
		for i := range count {
			entries = append(entries, fmt.Sprintf("k%d: %d", i, i))
		}

		return "m: {" + strings.Join(entries, ", ") + "}\n"
	}

	// The entries of the mapping at an even index, so that the Update
	// removes each other entry.
	everyOther := func(count int) any {
		v := make(yaml.MapSlice, 0, count/2)
		for i := 0; i < count; i += 2 {
			v = append(v, yaml.MapItem{Key: fmt.Sprintf("k%d", i), Value: i})
		}

		return v
	}

	tcs := map[string]struct {
		// Text returns the text of the document, and value returns the new
		// value at $.m, for a count of entries.
		text  func(count int) string
		value func(count int) any
	}{
		"scalars": {
			text: block,
			value: func(count int) any {
				v := make(yaml.MapSlice, 0, count)
				for i := range count {
					v = append(v, yaml.MapItem{Key: fmt.Sprintf("k%d", i), Value: i + 1})
				}

				return v
			},
		},
		"removed entries": {
			text:  block,
			value: func(int) any { return ms("k0", 0) },
		},
		"removed entries in flow style": {
			text:  flow,
			value: func(int) any { return ms("k0", 0) },
		},
		"removed entries between kept entries": {
			text:  block,
			value: everyOther,
		},
		"removed entries between kept entries in flow style": {
			text:  flow,
			value: everyOther,
		},
	}

	for name, tc := range tcs {
		//nolint:paralleltest // See above.
		t.Run(name, func(t *testing.T) {
			allocated := func(count int) uint64 {
				doc := yamltest.FirstDocument(t, tc.text(count), yamltest.NoSizeLimits()...)

				d, err := edit.New(doc)
				require.NoError(t, err)

				v := tc.value(count)

				var before, after runtime.MemStats

				runtime.ReadMemStats(&before)

				err = d.Apply(t.Context(), paths.Doc().Child("m"), func(ctx context.Context, n *edit.Node) error {
					return n.Update(ctx, v)
				})

				runtime.ReadMemStats(&after)
				require.NoError(t, err)

				return after.TotalAlloc - before.TotalAlloc
			}

			small, large := allocated(200), allocated(800)

			// Four times the entries take about four times the bytes. A copy
			// of the text for each entry takes more than ten times the bytes.
			assert.Less(t, large, 6*small, "bytes for 200 entries: %d, for 800: %d", small, large)
		})
	}
}

// applyCall applies call at path to a Doc of before, a text that a Source
// with opts reads, and returns the Doc and the error of call. The Func
// keeps that error, so the Apply keeps what call leaves. The Apply fails
// with the engine error of call when engine is true.
func applyCall(
	t *testing.T, before string, path paths.Path, opts []niceyaml.SourceOption, engine bool, call edit.Func,
) (*edit.Doc, error) {
	t.Helper()

	d := newDoc(t, before, opts...)

	var err error

	applyErr := applyAt(t, d, path, func(ctx context.Context, n *edit.Node) error {
		err = call(ctx, n)

		return nil
	})

	if engine {
		require.ErrorIs(t, applyErr, edit.ErrEngine)
		require.ErrorIs(t, err, edit.ErrEngine)
	} else {
		require.NoError(t, applyErr)
	}

	return d, err
}

// decodeText returns the decode of text into any, with the options of a
// Source in opts.
func decodeText(t *testing.T, text string, opts ...niceyaml.SourceOption) any {
	t.Helper()

	doc, err := niceyaml.NewSourceFromString(text, opts...).Document()
	require.NoError(t, err)

	v, err := doc.Decode[any](t.Context())
	require.NoError(t, err)

	return v
}

func ExampleNode_Update() {
	src := niceyaml.NewSourceFromString(stringtest.LinesLF(
		"# service config",
		"server:",
		"  # bind address",
		"  host: 0.0.0.0   # all interfaces",
		"  port: 8080      # behind the LB",
		"",
		"  tls: false",
		"  origins:",
		"    - https://a.example  # prod",
		"    - https://b.example",
	), niceyaml.WithName("config.yaml"))

	doc, err := src.Document()
	if err != nil {
		fmt.Println(err)

		return
	}

	d, err := edit.New(doc)
	if err != nil {
		fmt.Println(err)

		return
	}

	// The Func decodes the whole document, changes two fields, and writes
	// the document back. The comments and the blank line stay.
	err = d.Apply(context.Background(), paths.Doc(), func(ctx context.Context, n *edit.Node) error {
		cfg, err := n.Decode[updateConfig](ctx)
		if err != nil {
			return err
		}

		cfg.Server.Port = 9090
		cfg.Server.Origins = append(cfg.Server.Origins, "https://c.example")

		return n.Update(ctx, cfg)
	})
	if err != nil {
		fmt.Println(err)

		return
	}

	fmt.Print(d.Text())

	// Output:
	// # service config
	// server:
	//   # bind address
	//   host: 0.0.0.0   # all interfaces
	//   port: 9090      # behind the LB
	//
	//   tls: false
	//   origins:
	//     - https://a.example  # prod
	//     - https://b.example
	//     - https://c.example
}
