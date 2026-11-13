package edit_test

import (
	"context"
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
			before: stringtest.LinesLF("a: {k: &x 1}", "b: *x"),
			path:   paths.Doc(),
			value:  ms("a", 5, "b", 1),
			want:   stringtest.LinesLF("a: 5", "b: 1"),
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
			want:   stringtest.LinesLF("a: 1", "b:", `  s: "x\n\n"`),
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
		"value that does not encode at a new place": {
			before:  "a: 1\n",
			path:    paths.Doc().Child("b"),
			value:   ms("x", func() {}),
			err:     "cafe.jacobcolvin.com/v1 cannot write the value of $.b",
			likeSet: true,
		},
		"value with a character that the encoder writes as it is": {
			before:  stringtest.LinesLF("m:", "  a: 1"),
			path:    paths.Doc().Child("m"),
			value:   ms("a", "x\x01y"),
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
			before: "m: {base: &a {x: 1}, r: {<<: *a, y: 1}}\n",
			path:   paths.Doc().Child("m"),
			value:  ms("base", 3, "r", []int{1}),
			want:   "m: {base: &a 3, r: [1]}\n",
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

// TestNode_Update_Scaling checks that the bytes that an Update of many
// scalars allocates grow linearly with the number of scalars. The Update
// writes all the scalars into the text in one pass. A pass for each
// scalar copies the whole text for each one, which makes the bytes grow
// with the square of the number. The test reads the allocations of the
// whole process, so it does not run in parallel.
//
//nolint:paralleltest // See above.
func TestNode_Update_Scaling(t *testing.T) {
	allocated := func(count int) uint64 {
		var text strings.Builder

		text.WriteString("m:\n")

		v := make(yaml.MapSlice, 0, count)

		for i := range count {
			fmt.Fprintf(&text, "  # about k%d\n  k%d: %d  # was %d\n", i, i, i, i)

			v = append(v, yaml.MapItem{Key: fmt.Sprintf("k%d", i), Value: i + 1})
		}

		doc := yamltest.FirstDocument(t, text.String(), yamltest.NoSizeLimits()...)

		d, err := edit.New(doc)
		require.NoError(t, err)

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

	// Four times the scalars take about four times the bytes. A copy of
	// the text for each scalar takes more than ten times the bytes.
	assert.Less(t, large, 6*small, "bytes for 200 scalars: %d, for 800: %d", small, large)
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
