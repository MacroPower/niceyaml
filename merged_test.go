package niceyaml_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

func TestNewSourceFromLayers_Decode_Merge(t *testing.T) {
	t.Parallel()

	// Each case merges its layers and reads the text of the merged
	// document. The files are base.yaml and prod.yaml, with mid.yaml
	// between them when the case has three.
	tcs := map[string]struct {
		// Where an error at each path binds, as "file:line:col", or as the
		// name of the file for one that binds with no position.
		binds map[string]string
		// The text of the merged document.
		want string
		// The documents of the layers, lowest first.
		layers []string
	}{
		"mappings merge key by key at every depth": {
			layers: []string{
				stringtest.LinesLF(
					"services:",
					"  web: {image: nginx, replicas: 1}",
					"  db: {image: postgres}",
				),
				stringtest.LinesLF(
					"services:",
					"  web: {replicas: 5}",
					"  api:",
					"    image: api",
				),
			},
			want: stringtest.LinesLF(
				"services:",
				"  web:",
				"    image: nginx",
				"    replicas: 5",
				"  db:",
				"    image: postgres",
				"  api:",
				"    image: api",
			),
			binds: map[string]string{
				"$.services.web.image":    "base.yaml:2:16",
				"$.services.web.replicas": "prod.yaml:2:19",
				"$.services.db.image":     "base.yaml:3:15",
				"$.services.api.image":    "prod.yaml:4:12",
				"$.services.web":          "prod.yaml:2:3",
				"$.services.db":           "base.yaml:3:3",
				"$.services":              "prod.yaml:1:1",
				"$.services.web.port":     "prod.yaml:2:3",
			},
		},
		"a scalar replaces a scalar, a sequence, and a mapping": {
			layers: []string{
				stringtest.LinesLF("a: 1", "b: [x, y]", "c: {k: v}"),
				stringtest.LinesLF("a: 2", "b: z", "c: w"),
			},
			want: stringtest.LinesLF("a: 2", "b: z", "c: w"),
			binds: map[string]string{
				"$.a":    "prod.yaml:1:4",
				"$.b[0]": "prod.yaml",
				"$.c.k":  "prod.yaml",
			},
		},
		"a sequence replaces a sequence whole": {
			layers: []string{
				stringtest.LinesLF("tags: [x, y, z]", "list:", "  - {name: a, port: 1}"),
				stringtest.LinesLF("tags: [q]", "list:", "  - {name: b}"),
			},
			want: stringtest.LinesLF(
				"tags:",
				"  - q",
				"list:",
				"  - name: b",
			),
			binds: map[string]string{
				"$.tags[0]":      "prod.yaml:1:8",
				"$.tags[2]":      "prod.yaml",
				"$.list[0].name": "prod.yaml:3:12",
				"$.list[0].port": "prod.yaml:3:3",
			},
		},
		"a mapping replaces a scalar and a sequence": {
			layers: []string{"a: 1\nb: [x]\n", "a: {k: v}\nb: {l: w}\n"},
			want: stringtest.LinesLF(
				"a:",
				"  k: v",
				"b:",
				"  l: w",
			),
		},
		"a null keeps the value below it": {
			layers: []string{
				stringtest.LinesLF("name: shop", "web: {image: nginx}", "tags: [x]"),
				stringtest.LinesLF(
					"name: ~",
					"web:",
					"  # image: other",
					"tags: null",
				),
			},
			want: stringtest.LinesLF(
				"name: shop",
				"web:",
				"  image: nginx",
				"tags:",
				"  - x",
			),
			binds: map[string]string{
				"$.name":      "base.yaml:1:7",
				"$.web.image": "base.yaml:2:14",
				"$.tags[0]":   "base.yaml:3:8",
			},
		},
		"a null stays where no layer below holds a value": {
			layers: []string{"a: 1\nd: null\n", stringtest.LinesLF("b: ~", "c:", "d: ~")},
			want: stringtest.LinesLF(
				"a: 1",
				"d: ~",
				"b: ~",
				"c: null",
			),
			binds: map[string]string{
				"$.b": "prod.yaml:1:4",
				"$.d": "prod.yaml:3:4",
			},
		},
		"an empty mapping keeps the mapping below and an empty sequence replaces": {
			layers: []string{"m: {a: 1}\ns: [x]\n", "m: {}\ns: []\n"},
			want:   stringtest.LinesLF("m:", "  a: 1", "s: []"),
			binds: map[string]string{
				"$.m":   "prod.yaml:1:1",
				"$.m.a": "base.yaml:1:8",
				"$.s":   "prod.yaml:2:1",
			},
		},
		"empty collections stay where nothing fills them": {
			layers: []string{"m: {}\ns: []\n", "n: {}\n"},
			want:   stringtest.LinesLF("m: {}", "s: []", "n: {}"),
		},
		"three layers apply in order": {
			layers: []string{stringtest.LinesLF("a: 1", "b: 1", "c: 1"), "b: 2\nc: 2\n", "c: 3\n"},
			want:   stringtest.LinesLF("a: 1", "b: 2", "c: 3"),
			binds: map[string]string{
				"$.a": "base.yaml:1:4",
				"$.b": "mid.yaml:1:4",
				"$.c": "prod.yaml:1:4",
			},
		},
		"a document with no content adds nothing": {
			layers: []string{"a: 1\n", "# nothing\n", ""},
			want:   "a: 1\n",
			binds:  map[string]string{"$.b": "base.yaml:1:1"},
		},
		"layers with no content merge into an empty document": {
			layers: []string{"# nothing\n", ""},
			want:   "# nothing\n",
			binds:  map[string]string{"$.a": "prod.yaml"},
		},
		"a sequence at the root replaces": {
			layers: []string{stringtest.LinesLF("- a", "- b"), "- c\n"},
			want:   "- c\n",
			binds:  map[string]string{"$[0]": "prod.yaml:1:3"},
		},
		"a scalar at the root replaces a mapping": {
			layers: []string{"a: 1\n", "hello\n"},
			want:   "hello\n",
			binds:  map[string]string{"$": "prod.yaml:1:1"},
		},
		"a null at the root keeps the layer below": {
			layers: []string{"a: 1\n", "~\n"},
			want:   "a: 1\n",
		},
		"keys of one name merge, and the higher layer spells the key": {
			layers: []string{"ports: {80: a, 443: b}\n", "ports: {\"80\": z}\n"},
			want:   stringtest.LinesLF("ports:", "  \"80\": z", "  443: b"),
			binds: map[string]string{
				"$.ports.80":  "prod.yaml:1:15",
				"$.ports.80~": "prod.yaml:1:9",
				"$.ports.443": "base.yaml:1:21",
			},
		},
		"a key keeps the spelling of the layer its value comes from": {
			layers: []string{"ports: {80: a}\n", "ports: {'80': ~}\n"},
			want:   stringtest.LinesLF("ports:", "  80: a"),
			binds:  map[string]string{"$.ports.80~": "base.yaml:1:9"},
		},
		"keys of two names both stay": {
			layers: []string{"ports: {80: a}\n", "ports: {0x50: z}\n"},
			want:   stringtest.LinesLF("ports:", "  80: a", "  0x50: z"),
		},
		"an alias reads the anchor of its own layer": {
			layers: []string{
				stringtest.LinesLF("x: &d {image: base, replicas: 1}", "web: *d"),
				"x: &d {image: prod}\napi: *d\n",
			},
			want: stringtest.LinesLF(
				"x:",
				"  image: prod",
				"  replicas: 1",
				"web:",
				"  image: base",
				"  replicas: 1",
				"api:",
				"  image: prod",
			),
			binds: map[string]string{
				"$.x.image":      "prod.yaml:1:15",
				"$.x.replicas":   "base.yaml:1:31",
				"$.web.image":    "base.yaml:1:15",
				"$.web.replicas": "base.yaml:1:31",
				"$.api.image":    "prod.yaml:1:15",
				"$.web":          "base.yaml:2:6",
			},
		},
		"a value an alias brings in merges as one written in its place": {
			layers: []string{
				"web: {image: nginx, replicas: 1}\n",
				"d: &d {replicas: 7}\nweb: *d\n",
			},
			want: stringtest.LinesLF(
				"web:",
				"  image: nginx",
				"  replicas: 7",
				"d:",
				"  replicas: 7",
			),
			binds: map[string]string{
				"$.web.image":    "base.yaml:1:14",
				"$.web.replicas": "prod.yaml:1:18",
				"$.web":          "prod.yaml:2:6",
			},
		},
		"an alias inside its own anchor reads as null": {
			layers: []string{stringtest.LinesLF(
				"a: &a",
				"  b: 1",
				"  self: *a",
				"c: *a",
			)},
			want: stringtest.LinesLF(
				"a:",
				"  b: 1",
				"  self: null",
				"c:",
				"  b: 1",
				"  self: null",
			),
		},
		"an alias to a scalar copies its text": {
			layers: []string{stringtest.LinesLF("v: &v 1.50", "list: [*v, &w two, *w]", "*w : key")},
			want: stringtest.LinesLF(
				"v: 1.50",
				"list:",
				"  - 1.50",
				"  - two",
				"  - two",
				"two: key",
			),
		},
		"a merge key in each layer": {
			layers: []string{
				stringtest.LinesLF("d: &d {image: nginx, replicas: 1}", "web: {<<: *d, replicas: 2}"),
				stringtest.LinesLF("p: &p {image: prod, port: 80}", "web: {<<: *p}"),
			},
			want: stringtest.LinesLF(
				"d:",
				"  image: nginx",
				"  replicas: 1",
				"web:",
				"  image: prod",
				"  replicas: 2",
				"  port: 80",
				"p:",
				"  image: prod",
				"  port: 80",
			),
			binds: map[string]string{
				"$.web.image":    "prod.yaml:1:15",
				"$.web.replicas": "base.yaml:2:25",
				"$.web.port":     "prod.yaml:1:27",
			},
		},
		"a later merge key wins over the key before it": {
			layers: []string{stringtest.LinesLF("d: &d {a: 1}", "one: {a: 9, <<: *d}", "two: {<<: *d, a: 9}")},
			want: stringtest.LinesLF(
				"d:",
				"  a: 1",
				"one:",
				"  a: 1",
				"two:",
				"  a: 9",
			),
			binds: map[string]string{
				"$.one.a": "base.yaml:1:11",
				"$.two.a": "base.yaml:3:18",
			},
		},
		"the sources of a merge key apply in order": {
			layers: []string{
				stringtest.LinesLF("x: &x {a: 1, b: 1}", "y: &y {a: 2, c: 2}", "m: {<<: [*x, *y, {d: 4}]}"),
			},
			want: stringtest.LinesLF(
				"x:",
				"  a: 1",
				"  b: 1",
				"y:",
				"  a: 2",
				"  c: 2",
				"m:",
				"  a: 2",
				"  b: 1",
				"  c: 2",
				"  d: 4",
			),
		},
		"a merge key of a merge source brings its keys in": {
			layers: []string{stringtest.LinesLF("a: &a {k: 1}", "b: &b {<<: *a, l: 2}", "c: {<<: *b}")},
			want: stringtest.LinesLF(
				"a:",
				"  k: 1",
				"b:",
				"  k: 1",
				"  l: 2",
				"c:",
				"  k: 1",
				"  l: 2",
			),
		},
		"a quoted key that spells a merge key is a key": {
			layers: []string{"m: {\"<<\": {a: 1}}\n", "m: {'<<': {b: 2}}\n"},
			want: stringtest.LinesLF(
				"m:",
				"  '<<':",
				"    a: 1",
				"    b: 2",
			),
		},
		"a scalar keeps the text of its layer": {
			layers: []string{stringtest.LinesLF(
				"plain: hello world",
				"single: 'it''s'",
				`double: "a\tb"`,
				"hex: 0x10",
				"float: 1.50",
				"date: 2001-12-14",
				"bool: True",
				"nul: Null",
				"url: http://example.com/a?b=c#d",
				"dash: -x",
				"colon: a:b",
				"0x10: key",
				"1.50: key",
				"true: key",
				"~: key",
				`"quoted: key": v`,
				"? explicit",
				": v",
			)},
			want: stringtest.LinesLF(
				"plain: hello world",
				"single: 'it''s'",
				`double: "a\tb"`,
				"hex: 0x10",
				"float: 1.50",
				"date: 2001-12-14",
				"bool: True",
				"nul: Null",
				"url: http://example.com/a?b=c#d",
				"dash: -x",
				"colon: a:b",
				"0x10: key",
				"1.50: key",
				"true: key",
				"~: key",
				`"quoted: key": v`,
				"explicit: v",
			),
		},
		"a string of several lines reads as one double-quoted line": {
			layers: []string{
				stringtest.LinesLF(
					"lit: |",
					"  one",
					"  two",
					"fold: >-",
					"  one",
					"  two",
					"plain: one",
					"  two",
					"",
					"  three",
					"single: 'one",
					"",
					"  two'",
					"? |",
					"  key",
					": v",
				),
			},
			want: stringtest.LinesLF(
				`lit: "one\ntwo\n"`,
				`fold: "one two"`,
				`plain: "one two\nthree"`,
				`single: "one\ntwo"`,
				`"key\n": v`,
			),
		},
		"a plain scalar that a block line reads another way takes quotes": {
			layers: []string{"a: [-, ?, a b, -x]\nb: {k: -}\n"},
			want: stringtest.LinesLF(
				"a:",
				`  - "-"`,
				`  - "?"`,
				"  - a b",
				"  - -x",
				"b:",
				`  k: "-"`,
			),
		},
		"a tag stays on its value": {
			layers: []string{
				stringtest.LinesLF(
					"bin: !!binary aGVsbG8=",
					"str: !!str 5",
					"set: !!set {a, b}",
					"obj: !obj {k: v}",
					"list: !list [x]",
					"items:",
					"  - !obj {k: v}",
					"  - !list [y]",
					"  - !!str 7",
					"none: !obj {}",
					"!!str 1: key",
					"nul: !!str ~",
				),
			},
			want: stringtest.LinesLF(
				"bin: !!binary aGVsbG8=",
				"str: !!str 5",
				"set: !!set",
				"  a: null",
				"  b: null",
				"obj: !obj",
				"  k: v",
				"list: !list",
				"  - x",
				"items:",
				"  - !obj",
				"    k: v",
				"  - !list",
				"    - y",
				"  - !!str 7",
				"none: !obj {}",
				"!!str 1: key",
				"nul: !!str ~",
			),
		},
		"a merged mapping takes the tag of the highest layer that writes one": {
			layers: []string{"m: !a {x: 1}\nn: !a {x: 1}\n", "m: {y: 2}\nn: !b {y: 2}\n"},
			want: stringtest.LinesLF(
				"m: !a",
				"  x: 1",
				"  y: 2",
				"n: !b",
				"  x: 1",
				"  y: 2",
			),
		},
		"a tag at the root goes above its collection": {
			layers: []string{"!obj\na: 1\n"},
			want:   "!obj\na: 1\n",
		},
		"collections nest in block style": {
			layers: []string{
				stringtest.LinesLF("a: [[x, y], {k: v, l: [1, 2]}, [], {}, [[z]]]", "b: {c: {d: [{e: f, g: {h: i}}]}}"),
			},
			want: stringtest.LinesLF(
				"a:",
				"  - - x",
				"    - y",
				"  - k: v",
				"    l:",
				"      - 1",
				"      - 2",
				"  - []",
				"  - {}",
				"  - - - z",
				"b:",
				"  c:",
				"    d:",
				"      - e: f",
				"        g:",
				"          h: i",
			),
		},
		"the preamble of the lowest layer stays and other comments go": {
			layers: []string{
				stringtest.LinesLF(
					"# base header",
					"---",
					"# lead",
					"a: 1 # trailing",
					"# between",
					"b: 2",
				),
				stringtest.LinesLF("# prod header", "---", "c: 3"),
			},
			want: stringtest.LinesLF(
				"# base header",
				"---",
				"# lead",
				"a: 1",
				"b: 2",
				"c: 3",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			nodes := layerNodes(t, tc.layers...)
			layers := yamltest.Layers(t, layersOf(nodes)...)

			assert.Equal(t, tc.want, mergedText(t, layers))

			requireLayerBinds(t, layers, tc.binds)

			// A decode of the merged document reads what a decode of its
			// text reads.
			want, err := niceyaml.NewSourceFromString(tc.want).Decode[any](t.Context())
			require.NoError(t, err)

			got, err := layers.Decode[any](t.Context())
			require.NoError(t, err)
			assert.Equal(t, want, got)

			// One layer merges into what it holds.
			if len(nodes) == 1 {
				alone, err := nodes[0].Decode[any](t.Context())
				require.NoError(t, err)
				assert.Equal(t, alone, got)
			}
		})
	}
}

// mergedCorpus holds documents that spell their values in the ways the
// merged document must write again. Each has a mapping at its root, so
// an empty mapping above or below it changes nothing.
var mergedCorpus = map[string]struct {
	// Returns a pointer to a new value of a Go type the document decodes
	// into, or nil for a document with none.
	typed func() any
	input string
}{
	"tagged scalars": {
		input: stringtest.LinesLF(
			"str: !!str 3.10",
			"bin: !!binary aGVsbG8=",
			"int: !!int 7",
			"float: !!float 1",
			"bool: !!bool true",
			"nul: !!null null",
			"time: !!timestamp 2001-12-14",
			`quoted: !!str "5"`,
		),
		typed: func() any {
			return new(struct {
				Time  time.Time `yaml:"time"`
				Str   string    `yaml:"str"`
				Bin   string    `yaml:"bin"`
				Int   int       `yaml:"int"`
				Float float64   `yaml:"float"`
				Bool  bool      `yaml:"bool"`
			})
		},
	},
	"custom tags": {
		input: stringtest.LinesLF(
			"scalar: !custom value",
			"mapping: !custom {k: v}",
			"sequence: !custom [x, y]",
			"block: !custom",
			"  k: v",
			"items:",
			"  - !custom {k: v}",
			"  - !custom",
			"    - x",
			"  - !custom 7",
		),
	},
	"a local tag over no value":    {input: "first: 1\nlast: !custom\n"},
	"a mapping tag over no value":  {input: "first: 1\nlast: &anchored !!map\n"},
	"a sequence tag over no value": {input: stringtest.LinesLF("first: 1", "last:", "  - !!seq")},
	"a string tag over no value":   {input: "first: 1\nlast: !!str\n"},
	"literal block scalars": {
		input: stringtest.LinesLF(
			"clip: |",
			"  one",
			"  two",
			"strip: |-",
			"  one",
			"  two",
			"keep: |+",
			"  one",
			"  two",
			"",
			"indent: |2",
			"   lead",
			"  rest",
			"blank: |",
			"  one",
			"",
			"  two",
			"last: |",
			"  end",
		),
		typed: func() any { return new(map[string]string) },
	},
	"folded block scalars": {
		input: stringtest.LinesLF(
			"clip: >",
			"  one",
			"  two",
			"strip: >-",
			"  one",
			"  two",
			"keep: >+",
			"  one",
			"  two",
			"",
			"more: >",
			"  one",
			"",
			"  two",
			"    indented",
			"  three",
			"last: >",
			"  end",
		),
		typed: func() any { return new(map[string]string) },
	},
	"quoted strings": {
		input: stringtest.LinesLF(
			`dq: "tab\there \"quoted\" \\ back \u00e9 \x41 \n line"`,
			`sq: 'it''s \n kept'`,
			`empty: ""`,
			"none: ''",
			`padded: "  wide  "`,
			`colon: "a: b"`,
			"hash: 'a #b'",
			`dash: "- item"`,
			`marker: "---"`,
			`folded: "one`,
			`  two"`,
			"word: 'true'",
			`number: "1"`,
		),
		typed: func() any { return new(map[string]string) },
	},
	"plain scalars": {
		input: stringtest.LinesLF(
			"int: 42",
			"neg: -7",
			"hex: 0x1F",
			"oct: 0o17",
			"bin: 0b101",
			"float: 1.50",
			"exp: 1e3",
			"inf: .inf",
			"under: 1_000",
			"plus: +1",
			"version: 1.10.0",
			"date: 2001-12-14",
			"stamp: 2001-12-14T21:59:43Z",
			"yes: yes",
			"bool: True",
			"nul: Null",
			"tilde: ~",
			"absent:",
			"url: http://example.com/a?b=c#d",
			"lead: -dash",
			"inner: a:b",
			"words: one two  three",
			"lines: one",
			"  two",
			"",
			"  three",
		),
		typed: func() any {
			return new(struct {
				Stamp   time.Time `yaml:"stamp"`
				Tilde   *string   `yaml:"tilde"`
				Version string    `yaml:"version"`
				Hex     string    `yaml:"hex"`
				URL     string    `yaml:"url"`
				Lines   string    `yaml:"lines"`
				Float   string    `yaml:"float"`
				Int     int       `yaml:"int"`
				Neg     int       `yaml:"neg"`
				Oct     int       `yaml:"oct"`
				Bin     int       `yaml:"bin"`
				Under   int       `yaml:"under"`
				Exp     float64   `yaml:"exp"`
				Inf     float64   `yaml:"inf"`
				Bool    bool      `yaml:"bool"`
			})
		},
	},
	"keys": {
		input: stringtest.LinesLF(
			`"quoted key": 1`,
			"'single key': 2",
			`"key: colon": 3`,
			`"#hash": 4`,
			"1: int",
			"1.5: float",
			"0x10: hex",
			"true: bool",
			"~: nul",
			"? explicit",
			": value",
			`"- dash": 5`,
			`"multi\nline": 6`,
			"? |",
			"  block",
			": 7",
			"!!str 9: tagged",
		),
	},
	"flow collections": {
		input: stringtest.LinesLF(
			"seq: [a, [b, c], {d: e}, [], {}]",
			"map: {a: 1, b: [2, 3], c: {d: {e: f}}}",
			"lines: [a,",
			"  b,",
			"  c]",
			"set: {a, b}",
			`odd: [-, ?, a b, "x, y"]`,
			"pair: [a: 1]",
		),
	},
	"block collections": {
		input: stringtest.LinesLF(
			"items:",
			"  - name: a",
			"    ports: [1, 2]",
			"  - name: b",
			"    nested:",
			"      - - x",
			"        - y",
			"      - k:",
			"          - z",
			"  - plain",
			"  -",
			"  - ~",
			"deep:",
			"  a:",
			"    b:",
			"      c: [1]",
		),
	},
	"anchors and aliases": {
		input: stringtest.LinesLF(
			"base: &base {a: 1, b: [x, y]}",
			"copy: *base",
			"scalar: &s hello",
			"again: *s",
			"list: [*s, *base, &t tail, *t]",
			"nested: &n",
			"  inner: &i {deep: 1}",
			"  again: *i",
			"whole: *n",
			"*s : key",
			"tagged: &g !!str 5",
			"read: *g",
		),
	},
	"aliases inside their own anchor": {
		input: stringtest.LinesLF(
			"outer: &outer",
			"  inner: &inner",
			"    self: *inner",
			"    list: [1, *inner]",
			"  merged:",
			"    <<: *inner",
			"    more: 2",
			"  nested: &nested {up: *outer}",
			"copy: *nested",
		),
	},
	"merge keys": {
		input: stringtest.LinesLF(
			"base: &base {a: 1, b: 2}",
			"more: &more {c: 3}",
			"one: {<<: *base, d: 4}",
			"many: {<<: [*base, *more], e: 5}",
			"block:",
			"  <<: *more",
			"  f: 6",
			"inline: {<<: {g: 7}, h: 8}",
			"chain: &chain {<<: *base, i: 9}",
			"last: {<<: *chain}",
		),
	},
	"unicode": {
		input: stringtest.LinesLF(
			"名前: 値",
			`emoji: "😀"`,
			"accent: héllo wörld",
			`rtl: "שלום"`,
			`nbsp: "a\u00a0b"`,
			`line: "a\u2028b"`,
			`bom: "\ufeffx"`,
			"wide: ｆｕｌｌ",
		),
		typed: func() any { return new(map[string]string) },
	},
	"comments": {input: stringtest.LinesLF(
		"# head",
		"a: 1 # trailing",
		"",
		"# middle",
		"b:",
		"  # inner",
		"  c: 2",
		"# tail",
	)},
}

func TestNewSourceFromLayers_Decode_Equivalence(t *testing.T) {
	t.Parallel()

	// A value decodes from a document and from layers alike.
	type decoder interface {
		DecodeInto(ctx context.Context, v any, opts ...niceyaml.DecodeOption) error
	}

	// One layer merges into what it holds, and an empty mapping above or
	// below it changes nothing, so every arrangement decodes as the
	// document alone does.
	arrangements := map[string]func(doc, empty *niceyaml.Node) []niceyaml.Layer{
		"alone":                  func(doc, _ *niceyaml.Node) []niceyaml.Layer { return []niceyaml.Layer{doc} },
		"over an empty mapping":  func(doc, empty *niceyaml.Node) []niceyaml.Layer { return []niceyaml.Layer{empty, doc} },
		"under an empty mapping": func(doc, empty *niceyaml.Node) []niceyaml.Layer { return []niceyaml.Layer{doc, empty} },
	}

	for name, tc := range mergedCorpus {
		targets := map[string]func() any{
			"any":            func() any { return new(any) },
			"an untyped map": func() any { return new(map[string]any) },
			"an ordered map": func() any { return new(yaml.MapSlice) },
		}

		if tc.typed != nil {
			targets["a typed value"] = tc.typed
		}

		for arrangement, arrange := range arrangements {
			t.Run(name+" "+arrangement, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, tc.input, niceyaml.WithName("doc.yaml"))
				empty := yamltest.FirstDocument(t, "{}\n", niceyaml.WithName("empty.yaml"))
				layers := yamltest.Layers(t, arrange(doc, empty)...)

				for target, zero := range targets {
					want, got := zero(), zero()

					var alone, merged decoder = doc, layers

					require.NoError(t, alone.DecodeInto(t.Context(), want), target)
					require.NoError(t, merged.DecodeInto(t.Context(), got), target)
					assert.Equal(t, want, got, target)
				}
			})
		}
	}
}

func TestNewSourceFromLayers_Decode_Equivalence_Errors(t *testing.T) {
	t.Parallel()

	input := stringtest.LinesLF(
		"name: [shop]",
		"server:",
		"  host: example.com",
		"  port: many",
		"  prot: 1",
		"servers:",
		"  - host: a",
		"backup: &b {host: b}",
		"nested:",
		"  inner: *b",
		"nul: !!null null",
		"limits: {timeout: ~}",
	)

	type strict struct {
		layerConfig `yaml:",inline"`

		Nul *int `yaml:"nul"`
	}

	// A single layer reports each error of a decode where its document
	// reports it, with the same text.
	tcs := map[string][]niceyaml.DecodeOption{
		"values the decoder rejects": nil,
		"keys no field reads":        {niceyaml.WithDisallowUnknownFields(true)},
		"a validator that fails": {niceyaml.WithValidator(niceyaml.ValidatorFunc(
			func(_ context.Context, n *niceyaml.Node) error {
				return niceyaml.NewSummary(
					"checks",
					niceyaml.NewError("bad host", niceyaml.AtPath(paths.Current().Child("server", "host"))),
					niceyaml.NewError("no zone", niceyaml.AtPath(paths.Doc().Child("server", "zone"))),
					n.Bind(
						niceyaml.NewError("bad inner", niceyaml.AtPath(paths.Doc().Child("nested", "inner", "host"))),
					),
				)
			},
		))},
	}

	for name, opts := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, input, niceyaml.WithName("doc.yaml"))

			_, want := doc.Decode[strict](t.Context(), opts...)
			require.Error(t, want)

			_, got := yamltest.Layers(t, doc).Decode[strict](t.Context(), opts...)
			require.EqualError(t, got, want.Error())
			assert.Equal(
				t,
				niceyaml.FormatError(want, niceyaml.WithContextLines(0)),
				niceyaml.FormatError(got, niceyaml.WithContextLines(0)),
			)
		})
	}

	t.Run("values that fail their own checks", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t,
			stringtest.LinesLF(
				"server:",
				"  host: example.com",
				"servers:",
				"  - host: a",
				"backup: &b {host: b}",
				"nested:",
				"  inner: *b",
			),
			niceyaml.WithName("doc.yaml"))

		_, want := doc.Decode[layerConfig](t.Context())
		require.Error(t, want)

		_, got := yamltest.Layers(t, doc).Decode[layerConfig](t.Context())
		require.EqualError(t, got, want.Error())
	})
}

func TestNewSourceFromLayers_Decode_Options(t *testing.T) {
	t.Parallel()

	type config struct {
		Level  plainLevel `yaml:"level"`
		Backup plainLevel `yaml:"backup"`
	}

	levels := niceyaml.WithCustomUnmarshaler(decodeLevel)

	// The options reach the one decode of the merged document, so the
	// function decodes a level whichever layer holds it, and its error
	// binds in that layer.
	tcs := map[string]struct {
		base string
		err  string
		want config
	}{
		"function decodes the value of each layer": {
			base: "level: low\nbackup: low\n",
			want: config{Level: levelHigh, Backup: levelLow},
		},
		"error of the function binds in the layer of its value": {
			base: "level: low\nbackup: mid\n",
			err:  `base.yaml:2:9: $.backup: unknown level "mid"`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			layers := layerFiles(t, tc.base, "level: high\n")

			got, err := layers.Decode[config](t.Context(), levels)
			if tc.err != "" {
				require.EqualError(t, err, tc.err)
				require.ErrorIs(t, err, errUnknownLevel)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestNewSourceFromLayers_Decode_Maps(t *testing.T) {
	t.Parallel()

	type service struct {
		Image    string `yaml:"image"`
		Replicas int    `yaml:"replicas"`
	}

	type config struct {
		Services map[string]service `yaml:"services"`
		Extra    map[string]any     `yaml:"extra"`
	}

	layers := layerFiles(t,
		stringtest.LinesLF(
			"services:",
			"  web: {image: nginx, replicas: 1}",
			"  db: {image: postgres, replicas: 1}",
			"extra: {a: {b: 1, c: 2}, z: 1}",
		),
		stringtest.LinesLF("services:", "  web: {replicas: 5}", "extra: {a: {b: 9}}"),
	)

	t.Run("a map field keeps the entries of every layer", func(t *testing.T) {
		t.Parallel()

		cfg, err := layers.Decode[config](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]service{
			"web": {Image: "nginx", Replicas: 5},
			"db":  {Image: "postgres", Replicas: 1},
		}, cfg.Services)
		assert.Equal(t, map[string]any{
			"a": map[string]any{"b": uint64(9), "c": uint64(2)},
			"z": uint64(1),
		}, cfg.Extra)
	})

	t.Run("an untyped value holds every layer", func(t *testing.T) {
		t.Parallel()

		got, err := layers.Decode[map[string]any](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]any{
			"services": map[string]any{
				"web": map[string]any{"image": "nginx", "replicas": uint64(5)},
				"db":  map[string]any{"image": "postgres", "replicas": uint64(1)},
			},
			"extra": map[string]any{
				"a": map[string]any{"b": uint64(9), "c": uint64(2)},
				"z": uint64(1),
			},
		}, got)
	})

	t.Run("keys of one name fill one entry of a typed map", func(t *testing.T) {
		t.Parallel()

		nodes := layerNodes(t, "ports: {80: http, 443: https}\n", "ports: {\"80\": proxy}\n")

		type ports struct {
			Ports map[string]string `yaml:"ports"`
		}

		got, err := yamltest.Layers(t, layersOf(nodes)...).Decode[ports](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"80": "proxy", "443": "https"}, got.Ports)

		type numbered struct {
			Ports map[int]string `yaml:"ports"`
		}

		byNumber, err := yamltest.Layers(t, layersOf(nodes)...).Decode[numbered](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[int]string{80: "proxy", 443: "https"}, byNumber.Ports)
	})
}

func TestNewSourceFromLayers_Decode_Scalars(t *testing.T) {
	t.Parallel()

	type config struct {
		When    time.Time `yaml:"when"`
		Version string    `yaml:"version"`
		Hex     string    `yaml:"hex"`
		Text    string    `yaml:"text"`
		Quoted  string    `yaml:"quoted"`
		Data    string    `yaml:"data"`
		Count   int       `yaml:"count"`
		Ratio   float64   `yaml:"ratio"`
	}

	baseInput := stringtest.LinesLF(
		"version: 1.10",
		"hex: 0x1F",
		"count: 0x10",
		"ratio: 1.50",
		"when: 2001-12-14",
	)
	prodInput := stringtest.LinesLF(
		"text: |",
		"  one",
		"  two",
		"quoted: 'true'",
		"data: !!binary aGVsbG8=",
	)

	// The merged document spells each scalar as its layer does, so a
	// decode reads from it what it reads from one file that holds them
	// all.
	want, err := yamltest.FirstDocument(t, baseInput+prodInput).Decode[config](t.Context())
	require.NoError(t, err)

	got, err := layerFiles(t, baseInput, prodInput).Decode[config](t.Context())
	require.NoError(t, err)
	assert.Equal(t, want, got)

	assert.Equal(t, 16, got.Count)
	assert.InDelta(t, 1.5, got.Ratio, 0)
	assert.Equal(t, time.Date(2001, time.December, 14, 0, 0, 0, 0, time.UTC), got.When)
	assert.Equal(t, "one\ntwo\n", got.Text)
	assert.Equal(t, "true", got.Quoted)
	assert.Equal(t, "hello", got.Data)
}

// mergedStore decodes itself from YAML bytes and needs its kind.
type mergedStore struct {
	Kind string
	Path string
}

func (s *mergedStore) UnmarshalYAML(data []byte) error {
	var raw struct {
		Kind string `yaml:"kind"`
		Path string `yaml:"path"`
	}

	err := yaml.Unmarshal(data, &raw)
	if err != nil {
		return fmt.Errorf("read store: %w", err)
	}

	if raw.Kind == "" {
		return errors.New("store needs a kind")
	}

	*s = mergedStore{Kind: raw.Kind, Path: raw.Path}

	return nil
}

func TestNewSourceFromLayers_Decode_Unmarshaler(t *testing.T) {
	t.Parallel()

	type config struct {
		Store mergedStore `yaml:"store"`
	}

	t.Run("a type that decodes itself reads what the layers hold together", func(t *testing.T) {
		t.Parallel()

		nodes := layerNodes(t, "store: {kind: disk, path: /a}\n", "store: {path: /b}\n")

		// The layer on its own lacks the kind the type needs.
		_, err := nodes[1].Decode[config](t.Context())
		require.EqualError(t, err, "prod.yaml:1:1: $.store: store needs a kind")

		got, err := yamltest.Layers(t, layersOf(nodes)...).Decode[config](t.Context())
		require.NoError(t, err)
		assert.Equal(t, mergedStore{Kind: "disk", Path: "/b"}, got.Store)
	})

	t.Run("its error binds in the highest layer that holds its mapping", func(t *testing.T) {
		t.Parallel()

		nodes := layerNodes(t, "name: base\nstore: {path: /a}\n", "store: {path: /b}\n")

		_, err := yamltest.Layers(t, layersOf(nodes)...).Decode[config](t.Context())
		require.EqualError(t, err, "prod.yaml:1:1: $.store: store needs a kind")

		_, err = yamltest.Layers(t, nodes[0], layerNodes(t, "name: prod\n")[0]).Decode[config](t.Context())
		require.EqualError(t, err, "base.yaml:2:1: $.store: store needs a kind")
	})
}

func TestNewSourceFromLayers_Decode_SourceSettings(t *testing.T) {
	t.Parallel()

	t.Run("each layer reads its own reference documents", func(t *testing.T) {
		t.Parallel()

		shared := niceyaml.NewSourceFromString(
			stringtest.LinesLF("base: &base {port: 9, host: shared}", "server: &server {<<: *base, tls: true}"),
		)
		other := niceyaml.NewSourceFromString("base: &base {port: 1}\n")

		// Both files read an anchor named base, each from its own
		// reference document, and prod.yaml defines the name itself too.
		base := yamltest.FirstDocument(t, "db: *base\n",
			niceyaml.WithName("base.yaml"), niceyaml.WithReferences(other))
		prod := yamltest.FirstDocument(
			t,
			stringtest.LinesLF(
				"web: *server",
				"api: {<<: *base, port: 2}",
				"base: &base {port: 3}",
				"db: {host: x}",
			),
			niceyaml.WithName("prod.yaml"),
			niceyaml.WithReferences(shared),
		)

		layers := yamltest.Layers(t, base, prod)

		assert.Equal(t, stringtest.LinesLF(
			"db:",
			"  port: 1",
			"  host: x",
			"web:",
			"  port: 9",
			"  host: shared",
			"  tls: true",
			"api:",
			"  port: 2",
			"  host: shared",
			"base:",
			"  port: 3",
		), mergedText(t, layers))

		// A decode of each layer alone reads the same values.
		alone, err := prod.Decode[map[string]any](t.Context())
		require.NoError(t, err)

		got, err := layers.Decode[map[string]any](t.Context())
		require.NoError(t, err)
		assert.Equal(t, alone["web"], got["web"])
		assert.Equal(t, alone["api"], got["api"])

		// A value from a reference document has no line in the layer, so
		// an error under it binds at the alias.
		requireLayerBinds(t, layers, map[string]string{
			"$.web.port": "prod.yaml:1:6",
			"$.db.port":  "base.yaml:1:5",
		})
	})

	t.Run("a layer that allows a duplicate key keeps the last one", func(t *testing.T) {
		t.Parallel()

		base := yamltest.FirstDocument(t,
			stringtest.LinesLF(
				"a: 1",
				"a: 2",
				"b: {c: 1}",
				"b: {d: 2}",
			),
			niceyaml.WithName("base.yaml"), niceyaml.WithAllowDuplicateKeys(true))
		prod := yamltest.FirstDocument(t, "e: 5\n", niceyaml.WithName("prod.yaml"))

		layers := yamltest.Layers(t, base, prod)

		assert.Equal(t,
			stringtest.LinesLF(
				"a: 2",
				"b:",
				"  d: 2",
				"e: 5",
			),
			mergedText(t, layers),
		)

		requireLayerBinds(t, layers, map[string]string{"$.a": "base.yaml:2:4"})

		// A layer that does not allow one fails as it does alone.
		strict := yamltest.UnparsedDocument(t, "a: 1\na: 2\n", niceyaml.WithName("prod.yaml"))

		_, err := niceyaml.NewSourceFromLayers(t.Context(), base, strict)
		require.ErrorIs(t, err, niceyaml.ErrSyntax)
		require.ErrorIs(t, err, strict.Err())
	})

	t.Run("a layer past the alias limit returns the error of its decode", func(t *testing.T) {
		t.Parallel()

		input := yamltest.MergeLevels(7)

		base := yamltest.FirstDocument(t, "a: 1\n", niceyaml.WithName("base.yaml"))
		prod := yamltest.FirstDocument(t, input, niceyaml.WithName("prod.yaml"))

		_, alone := prod.Decode[any](t.Context())
		require.ErrorIs(t, alone, niceyaml.ErrExcessiveAliasing)

		_, err := niceyaml.NewSourceFromLayers(t.Context(), base, prod)
		require.ErrorIs(t, err, niceyaml.ErrExcessiveAliasing)
		require.EqualError(t, err, alone.Error())
	})

	t.Run("a layer must pass the alias limit as text", func(t *testing.T) {
		t.Parallel()

		// Every alias copies ten thousand bytes, which a decode into an
		// any value shares and the merged text writes out.
		input := stringtest.LinesLF("long: &long "+strings.Repeat("x", 10_000), "list:") +
			strings.Repeat("  - *long\n", 150)

		limited := yamltest.FirstDocument(t, input, niceyaml.WithName("prod.yaml"))

		_, err := limited.Decode[any](t.Context())
		require.NoError(t, err)

		_, err = niceyaml.NewSourceFromLayers(t.Context(), limited)
		require.ErrorIs(t, err, niceyaml.ErrExcessiveAliasing)
		require.EqualError(t, err, "prod.yaml:1:1: excessive aliasing")
		assert.True(t, niceyaml.IsInvalid(err))

		trusted := yamltest.FirstDocument(t, input, niceyaml.WithAliasLimit(false))

		got, err := yamltest.Layers(t, trusted).Decode[map[string]any](t.Context())
		require.NoError(t, err)
		assert.Len(t, got["list"], 150)
	})

	t.Run("a key with no name returns an error", func(t *testing.T) {
		t.Parallel()

		// The key is an alias to a mapping, which no path names.
		nodes := layerNodes(t, "a: 1\n", stringtest.LinesLF("x: &x {a: 1}", "? *x", ": v"))

		_, alone := nodes[1].Decode[any](t.Context())
		require.NoError(t, alone)

		_, err := niceyaml.NewSourceFromLayers(t.Context(), layersOf(nodes)...)
		require.EqualError(t, err, "prod.yaml:2:3: mapping key has no name")
		require.ErrorIs(t, err, niceyaml.ErrUnnamedKey)
		assert.True(t, niceyaml.IsInvalid(err))

		bound := yamltest.RequireSourceError(t, err)
		assert.Same(t, nodes[1], yamltest.BoundNode(t, bound))
	})

	t.Run("a layer reads a reference document from its own Source", func(t *testing.T) {
		t.Parallel()

		const (
			shared    = "shared: &shared {host: shared.example.com}\n"
			baseInput = "server: {port: 80}\n"
			prodInput = "server: *shared\n"
		)

		nodes := layerNodes(t, baseInput, prodInput)

		// A layer whose Source holds no reference document fails as it
		// does alone.
		_, err := niceyaml.NewSourceFromLayers(t.Context(), layersOf(nodes)...)
		require.EqualError(t, err, "prod.yaml:1:9: $.server: could not find alias \"shared\"")

		bound := yamltest.RequireSourceError(t, err)
		assert.Same(t, nodes[1], yamltest.BoundNode(t, bound))

		// The Source of the layer takes the reference document.
		prod := yamltest.FirstDocument(t, prodInput, niceyaml.WithName("prod.yaml"),
			niceyaml.WithReferences(niceyaml.NewSourceFromString(shared)))

		got, err := yamltest.Layers(t, nodes[0], prod).Decode[map[string]any](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]any{
			"server": map[string]any{"host": "shared.example.com", "port": uint64(80)},
		}, got)
	})

	t.Run("a scope that holds a marker as a key quotes it at the root", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t,
			stringtest.LinesLF("a:", "  ---: 1", "  ...: 2"),
			niceyaml.WithName("base.yaml"),
		)
		scoped := yamltest.At(t, doc, paths.Doc().Child("a"))

		layers := yamltest.Layers(t, scoped)

		assert.Equal(t, "\"---\": 1\n\"...\": 2\n", mergedText(t, layers))

		err := layers.Bind(niceyaml.NewError("here", niceyaml.AtPath(paths.Doc().Child("..."))))
		require.EqualError(t, err, "base.yaml:3:8: $.a.'...': here")
	})
}

func TestNewSourceFromLayers_Document_Excerpts(t *testing.T) {
	t.Parallel()

	var (
		baseInput = stringtest.LinesLF("db:", "  host: db.internal")
		prodInput = stringtest.LinesLF("db:", "  port: 5432")
		envInput  = stringtest.LinesLF("db:", "  password: hunter2")
	)

	shown := func(input, name string, opts ...niceyaml.SourceOption) *niceyaml.Node {
		return yamltest.FirstDocument(t, input, append(opts, niceyaml.WithName(name))...)
	}

	off := func(input, name string) *niceyaml.Node {
		return shown(input, name, niceyaml.WithExcerpts(false))
	}

	// A reference document that holds the secret, and one that reads it.
	refs := niceyaml.NewSourceFromString("password: &password hunter2\n", niceyaml.WithExcerpts(false))
	shared := niceyaml.NewSourceFromString("db: &db {password: *password}\n", niceyaml.WithReferences(refs))

	// The merged text holds the values of every layer, so its Source has
	// excerpts off when the Source of any layer has, whichever layer gave
	// the Source its name.
	tcs := map[string]struct {
		layers []*niceyaml.Node
		want   bool
	}{
		"every layer has excerpts on": {
			layers: []*niceyaml.Node{shown(baseInput, "base.yaml"), shown(prodInput, "prod.yaml")},
			want:   true,
		},
		"the highest layer has excerpts off": {
			layers: []*niceyaml.Node{shown(baseInput, "base.yaml"), off(envInput, "environment")},
		},
		"the lowest layer has excerpts off": {
			layers: []*niceyaml.Node{off(envInput, "environment"), shown(baseInput, "base.yaml")},
		},
		"a layer between two others has excerpts off": {
			layers: []*niceyaml.Node{
				shown(baseInput, "base.yaml"),
				off(envInput, "environment"),
				shown(prodInput, "prod.yaml"),
			},
		},
		"a layer scoped to a value has excerpts off": {
			layers: []*niceyaml.Node{
				shown(baseInput, "base.yaml"),
				yamltest.At(
					t,
					off(stringtest.LinesLF("defaults:", "  db: {password: hunter2}"), "environment"),
					paths.Doc().Child("defaults"),
				),
			},
		},
		// The merged text holds the value the alias reads, which no line
		// of the layer holds.
		"a layer reads a reference document with excerpts off": {
			layers: []*niceyaml.Node{
				shown(baseInput, "base.yaml"),
				shown(stringtest.LinesLF("db:", "  password: *password"), "prod.yaml", niceyaml.WithReferences(refs)),
			},
		},
		"a layer reads a reference document through another": {
			layers: []*niceyaml.Node{
				shown(baseInput, "base.yaml"),
				shown("db: *db\n", "prod.yaml", niceyaml.WithReferences(shared)),
			},
		},
		"a merged layer holds a layer with excerpts off": {
			layers: []*niceyaml.Node{
				yamltest.Document(t, yamltest.Layers(t, shown(baseInput, "base.yaml"), off(envInput, "environment"))),
				shown(prodInput, "prod.yaml"),
			},
		},
		"a merged layer holds layers with excerpts on": {
			layers: []*niceyaml.Node{
				yamltest.Document(t, yamltest.Layers(t, shown(baseInput, "base.yaml"), shown(prodInput, "prod.yaml"))),
				shown(stringtest.LinesLF("db:", "  name: app"), "local.yaml"),
			},
			want: true,
		},
		"no layer": {want: true},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			layers := yamltest.Layers(t, layersOf(tc.layers)...)

			assert.Equal(t, tc.want, yamltest.Document(t, layers).Source().ShowsExcerpts())

			// The Source has excerpts off for each merged text that holds
			// the secret.
			assert.Equal(t, !tc.want, strings.Contains(mergedText(t, layers), "hunter2"))
		})
	}
}

func TestNewSourceFromLayers_MergedLayer(t *testing.T) {
	t.Parallel()

	// The blank line keeps the lines of base.yaml apart from the lines of
	// any document it merges into.
	var (
		baseInput = stringtest.LinesLF(
			"# base",
			"name: shop",
			"",
			"server:",
			"  host: example.com",
			"  port: 0",
		)
		midInput  = stringtest.LinesLF("server:", "  host: mid.example.com")
		prodInput = stringtest.LinesLF("server:", "  tls: {cert: 1}")
	)

	// Each case layers base.yaml, mid.yaml, and prod.yaml in that order,
	// with some of them merged into one document first. Every error
	// binds where it binds with no layer merged.
	tcs := map[string]func(t *testing.T, base, mid, prod *niceyaml.Node) []niceyaml.Layer{
		"no layer merged": func(_ *testing.T, base, mid, prod *niceyaml.Node) []niceyaml.Layer {
			return []niceyaml.Layer{base, mid, prod}
		},
		"the lower two merged": func(t *testing.T, base, mid, prod *niceyaml.Node) []niceyaml.Layer {
			t.Helper()

			return []niceyaml.Layer{yamltest.Document(t, yamltest.Layers(t, base, mid)), prod}
		},
		"the upper two merged": func(t *testing.T, base, mid, prod *niceyaml.Node) []niceyaml.Layer {
			t.Helper()

			return []niceyaml.Layer{base, yamltest.Document(t, yamltest.Layers(t, mid, prod))}
		},
		"all three merged": func(t *testing.T, base, mid, prod *niceyaml.Node) []niceyaml.Layer {
			t.Helper()

			return []niceyaml.Layer{yamltest.Document(t, yamltest.Layers(t, base, mid, prod))}
		},
		"the lower two merged twice": func(t *testing.T, base, mid, prod *niceyaml.Node) []niceyaml.Layer {
			t.Helper()

			lower := yamltest.Document(t, yamltest.Layers(t, base, mid))

			return []niceyaml.Layer{yamltest.Document(t, yamltest.Layers(t, lower)), prod}
		},
	}

	binds := map[string]string{
		"$.name":            "base.yaml:2:7",
		"$.server.port":     "base.yaml:6:9",
		"$.server.host":     "mid.yaml:2:9",
		"$.server.tls.cert": "prod.yaml:2:15",
		"$.server":          "prod.yaml:1:1",
		"$.server.debug":    "prod.yaml:1:1",
	}

	for name, arrange := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			nodes := layerNodes(t, baseInput, midInput, prodInput)
			layers := yamltest.Layers(t, arrange(t, nodes[0], nodes[1], nodes[2])...)

			assert.Equal(t, stringtest.LinesLF(
				"# base",
				"name: shop",
				"server:",
				"  host: mid.example.com",
				"  port: 0",
				"  tls:",
				"    cert: 1",
			),
				mergedText(t, layers))

			requireLayerBinds(t, layers, binds)

			err := layers.Bind(errors.New("quota service: connection refused"))
			require.EqualError(t, err, "base.yaml: quota service: connection refused")

			// The validator reads a position of the merged text, which is
			// no line of base.yaml.
			err = layers.ValidateDocuments(t.Context(), niceyaml.ValidatorFunc(
				func(_ context.Context, n *niceyaml.Node) error {
					port, err := n.Ranges(paths.Doc().Child("server", "port"))
					require.NoError(t, err)

					return niceyaml.NewError("at a position", niceyaml.AtPosition(port[0].Start))
				},
			))
			require.EqualError(t, err, "base.yaml:6:9: at a position")

			_, err = layers.Decode[layerConfig](t.Context())
			require.EqualError(t, err, "base.yaml:6:9: $.server.port: port must be at least 1")

			_, err = layers.Decode[struct {
				Server struct {
					Port bool `yaml:"port"`
				} `yaml:"server"`
			}](t.Context())
			require.EqualError(t, err, "base.yaml:6:9: $.server.port: expected boolean, got integer")
		})
	}

	t.Run("a scoped Node of a merged document binds in its files with their paths", func(t *testing.T) {
		t.Parallel()

		base := yamltest.FirstDocument(t,
			stringtest.LinesLF(
				"defaults:",
				"  server:",
				"    host: example.com",
				"    port: 0",
			),
			niceyaml.WithName("base.yaml"),
		)
		defaults := yamltest.At(t, base, paths.Doc().Child("defaults"))
		mid := yamltest.FirstDocument(t, midInput, niceyaml.WithName("mid.yaml"))
		prod := yamltest.FirstDocument(t, "tls: {cert: 1}\n", niceyaml.WithName("prod.yaml"))

		lower := yamltest.Document(t, yamltest.Layers(t, defaults, mid))
		layers := yamltest.Layers(t, yamltest.At(t, lower, paths.Doc().Child("server")), prod)

		assert.Equal(t,
			stringtest.LinesLF(
				"host: mid.example.com",
				"port: 0",
				"tls:",
				"  cert: 1",
			),
			mergedText(t, layers),
		)

		// The merged document holds the port at $.port, and each file
		// holds its value under a path of its own.
		scoped := map[string]struct {
			err  error
			want string
		}{
			"a value of the lowest file": {
				err:  niceyaml.NewError("here", niceyaml.AtPath(paths.Doc().Child("port"))),
				want: "base.yaml:4:11: $.defaults.server.port: here",
			},
			"a value of the file above it": {
				err:  niceyaml.NewError("here", niceyaml.AtPath(paths.Current().Child("host"))),
				want: "mid.yaml:2:9: $.server.host: here",
			},
			"a value of the highest file": {
				err:  niceyaml.NewError("here", niceyaml.AtPath(paths.Doc().Child("tls", "cert"))),
				want: "prod.yaml:1:13: $.tls.cert: here",
			},
			"a key": {
				err:  niceyaml.NewError("here", niceyaml.AtPath(paths.Doc().Child("port").Key())),
				want: "base.yaml:4:5: $.defaults.server.port~: here",
			},
			"an error with no location": {
				err:  errors.New("quota service: connection refused"),
				want: "base.yaml: quota service: connection refused",
			},
		}

		for name, tc := range scoped {
			require.EqualError(t, layers.Bind(tc.err), tc.want, name)
		}

		_, err := layers.Decode[layerServer](t.Context())
		require.EqualError(t, err, "base.yaml:4:11: $.defaults.server.port: port must be at least 1")

		bound := yamltest.RequireSourceError(t, err)
		assert.Same(t, defaults, yamltest.BoundNode(t, bound))
		assert.Same(t, base, yamltest.BoundDocument(t, bound))

		path, ok := bound.Path()
		require.True(t, ok)
		assert.Equal(t, "$.defaults.server.port", path.String())
	})

	t.Run("a merged document is a layer as a file of its text is", func(t *testing.T) {
		t.Parallel()

		nodes := layerNodes(t, "x: {p: 1}\n", "x: 5\n", "x: {q: 2}\n")

		// The scalar of mid.yaml replaces the mapping of base.yaml, and
		// the mapping of prod.yaml replaces the scalar.
		assert.Equal(t, stringtest.LinesLF("x:", "  q: 2"), mergedText(t, yamltest.Layers(t, layersOf(nodes)...)))

		// The document of mid.yaml and prod.yaml holds a mapping, which
		// merges into the mapping of base.yaml.
		upper := yamltest.Document(t, yamltest.Layers(t, nodes[1], nodes[2]))
		layers := yamltest.Layers(t, nodes[0], upper)

		assert.Equal(t, stringtest.LinesLF("x:", "  p: 1", "  q: 2"), mergedText(t, layers))

		requireLayerBinds(t, layers, map[string]string{
			"$.x.p": "base.yaml:1:8",
			"$.x.q": "prod.yaml:1:8",
		})
	})
}

// endingContext is a context that ends once Err has answered lasts
// calls, so a test ends it between two steps of the call that reads it.
type endingContext struct {
	context.Context

	calls atomic.Int64
	lasts int64
}

func (c *endingContext) Err() error {
	if c.calls.Add(1) > c.lasts {
		return context.Canceled
	}

	return nil
}

func TestNewSourceFromLayers_Context(t *testing.T) {
	t.Parallel()

	// Each alias writes the hundred items of its anchor out, so the merge
	// writes far more values than the layer spells.
	input := "items: &items " + yamltest.FlowSequence("1", 100) + "\n" +
		yamltest.GenerateLines(50, "copy%d: *items")

	base := niceyaml.NewSourceFromString("name: base\n", niceyaml.WithName("base.yaml"))
	prod := niceyaml.NewSourceFromString(input,
		niceyaml.WithName("prod.yaml"), niceyaml.WithAliasLimit(false))

	t.Run("a context that ends while a layer writes its aliases out returns its error", func(t *testing.T) {
		t.Parallel()

		// The decode of each layer reads the context a few times, so the
		// context ends among the values the aliases of prod.yaml write.
		ctx := &endingContext{Context: t.Context(), lasts: 100}

		merged, err := niceyaml.NewSourceFromLayers(ctx, base, prod)
		require.EqualError(t, err, "prod.yaml: context canceled")
		require.ErrorIs(t, err, context.Canceled)
		assert.Nil(t, merged)

		bound := yamltest.RequireSourceError(t, err)
		assert.Same(t, prod, bound.Source())
	})

	t.Run("a context that ends once every layer is read returns its error", func(t *testing.T) {
		t.Parallel()

		whole := &endingContext{Context: t.Context(), lasts: math.MaxInt64}

		_, err := niceyaml.NewSourceFromLayers(whole, base, prod)
		require.NoError(t, err)

		// The merge reads the context a last time before it parses the
		// merged text, and binds that error in the lowest layer.
		ctx := &endingContext{Context: t.Context(), lasts: whole.calls.Load() - 1}

		merged, err := niceyaml.NewSourceFromLayers(ctx, base, prod)
		require.EqualError(t, err, "base.yaml: context canceled")
		require.ErrorIs(t, err, context.Canceled)
		assert.Nil(t, merged)

		bound := yamltest.RequireSourceError(t, err)
		assert.Same(t, base, bound.Source())
	})
}

// requireLayerBinds requires an error at each path of binds to bind in
// layers at the place the path maps to. A place is "file:line:col", or
// the name of the file for an error that binds with no position.
func requireLayerBinds(t *testing.T, layers *niceyaml.Source, binds map[string]string) {
	t.Helper()

	for expr, at := range binds {
		path := paths.MustParse(expr)

		err := layers.Bind(niceyaml.NewError("here", niceyaml.AtPath(path)))
		require.EqualError(t, err, fmt.Sprintf("%s: %s: here", at, path), expr)
	}
}

// mergedText returns the text of the document layers merge into, with a
// line break behind each line.
func mergedText(t *testing.T, layers *niceyaml.Source) string {
	t.Helper()

	text := yamltest.Document(t, layers).Source().View().Held().Content()
	if text == "" {
		return ""
	}

	return text + "\n"
}
