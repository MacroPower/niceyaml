package niceyaml_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

func TestLayers_Decode_Merge(t *testing.T) {
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
				"services:\n  web: {image: nginx, replicas: 1}\n  db: {image: postgres}\n",
				"services:\n  web: {replicas: 5}\n  api:\n    image: api\n",
			},
			want: "services:\n  web:\n    image: nginx\n    replicas: 5\n" +
				"  db:\n    image: postgres\n  api:\n    image: api\n",
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
			layers: []string{"a: 1\nb: [x, y]\nc: {k: v}\n", "a: 2\nb: z\nc: w\n"},
			want:   "a: 2\nb: z\nc: w\n",
			binds: map[string]string{
				"$.a":    "prod.yaml:1:4",
				"$.b[0]": "prod.yaml",
				"$.c.k":  "prod.yaml",
			},
		},
		"a sequence replaces a sequence whole": {
			layers: []string{
				"tags: [x, y, z]\nlist:\n  - {name: a, port: 1}\n",
				"tags: [q]\nlist:\n  - {name: b}\n",
			},
			want: "tags:\n  - q\nlist:\n  - name: b\n",
			binds: map[string]string{
				"$.tags[0]":      "prod.yaml:1:8",
				"$.tags[2]":      "prod.yaml",
				"$.list[0].name": "prod.yaml:3:12",
				"$.list[0].port": "prod.yaml:3:3",
			},
		},
		"a mapping replaces a scalar and a sequence": {
			layers: []string{"a: 1\nb: [x]\n", "a: {k: v}\nb: {l: w}\n"},
			want:   "a:\n  k: v\nb:\n  l: w\n",
		},
		"a null keeps the value below it": {
			layers: []string{
				"name: shop\nweb: {image: nginx}\ntags: [x]\n",
				"name: ~\nweb:\n  # image: other\ntags: null\n",
			},
			want: "name: shop\nweb:\n  image: nginx\ntags:\n  - x\n",
			binds: map[string]string{
				"$.name":      "base.yaml:1:7",
				"$.web.image": "base.yaml:2:14",
				"$.tags[0]":   "base.yaml:3:8",
			},
		},
		"a null stays where no layer below holds a value": {
			layers: []string{"a: 1\nd: null\n", "b: ~\nc:\nd: ~\n"},
			want:   "a: 1\nd: ~\nb: ~\nc: null\n",
			binds: map[string]string{
				"$.b": "prod.yaml:1:4",
				"$.d": "prod.yaml:3:4",
			},
		},
		"an empty mapping keeps the mapping below and an empty sequence replaces": {
			layers: []string{"m: {a: 1}\ns: [x]\n", "m: {}\ns: []\n"},
			want:   "m:\n  a: 1\ns: []\n",
			binds: map[string]string{
				"$.m":   "prod.yaml:1:1",
				"$.m.a": "base.yaml:1:8",
				"$.s":   "prod.yaml:2:1",
			},
		},
		"empty collections stay where nothing fills them": {
			layers: []string{"m: {}\ns: []\n", "n: {}\n"},
			want:   "m: {}\ns: []\nn: {}\n",
		},
		"three layers apply in order": {
			layers: []string{"a: 1\nb: 1\nc: 1\n", "b: 2\nc: 2\n", "c: 3\n"},
			want:   "a: 1\nb: 2\nc: 3\n",
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
			layers: []string{"- a\n- b\n", "- c\n"},
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
			want:   "ports:\n  \"80\": z\n  443: b\n",
			binds: map[string]string{
				"$.ports.80":  "prod.yaml:1:15",
				"$.ports.80~": "prod.yaml:1:9",
				"$.ports.443": "base.yaml:1:21",
			},
		},
		"a key keeps the spelling of the layer its value comes from": {
			layers: []string{"ports: {80: a}\n", "ports: {'80': ~}\n"},
			want:   "ports:\n  80: a\n",
			binds:  map[string]string{"$.ports.80~": "base.yaml:1:9"},
		},
		"keys of two names both stay": {
			layers: []string{"ports: {80: a}\n", "ports: {0x50: z}\n"},
			want:   "ports:\n  80: a\n  0x50: z\n",
		},
		"an alias reads the anchor of its own layer": {
			layers: []string{
				"x: &d {image: base, replicas: 1}\nweb: *d\n",
				"x: &d {image: prod}\napi: *d\n",
			},
			want: "x:\n  image: prod\n  replicas: 1\n" +
				"web:\n  image: base\n  replicas: 1\napi:\n  image: prod\n",
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
			want: "web:\n  image: nginx\n  replicas: 7\nd:\n  replicas: 7\n",
			binds: map[string]string{
				"$.web.image":    "base.yaml:1:14",
				"$.web.replicas": "prod.yaml:1:18",
				"$.web":          "prod.yaml:2:6",
			},
		},
		"an alias inside its own anchor reads as null": {
			layers: []string{"a: &a\n  b: 1\n  self: *a\nc: *a\n"},
			want:   "a:\n  b: 1\n  self: null\nc:\n  b: 1\n  self: null\n",
		},
		"an alias to a scalar copies its text": {
			layers: []string{"v: &v 1.50\nlist: [*v, &w two, *w]\n*w : key\n"},
			want:   "v: 1.50\nlist:\n  - 1.50\n  - two\n  - two\ntwo: key\n",
		},
		"a merge key in each layer": {
			layers: []string{
				"d: &d {image: nginx, replicas: 1}\nweb: {<<: *d, replicas: 2}\n",
				"p: &p {image: prod, port: 80}\nweb: {<<: *p}\n",
			},
			want: "d:\n  image: nginx\n  replicas: 1\n" +
				"web:\n  image: prod\n  replicas: 2\n  port: 80\n" +
				"p:\n  image: prod\n  port: 80\n",
			binds: map[string]string{
				"$.web.image":    "prod.yaml:1:15",
				"$.web.replicas": "base.yaml:2:25",
				"$.web.port":     "prod.yaml:1:27",
			},
		},
		"a later merge key wins over the key before it": {
			layers: []string{"d: &d {a: 1}\none: {a: 9, <<: *d}\ntwo: {<<: *d, a: 9}\n"},
			want:   "d:\n  a: 1\none:\n  a: 1\ntwo:\n  a: 9\n",
			binds: map[string]string{
				"$.one.a": "base.yaml:1:11",
				"$.two.a": "base.yaml:3:18",
			},
		},
		"the sources of a merge key apply in order": {
			layers: []string{"x: &x {a: 1, b: 1}\ny: &y {a: 2, c: 2}\nm: {<<: [*x, *y, {d: 4}]}\n"},
			want: "x:\n  a: 1\n  b: 1\ny:\n  a: 2\n  c: 2\n" +
				"m:\n  a: 2\n  b: 1\n  c: 2\n  d: 4\n",
		},
		"a merge key of a merge source brings its keys in": {
			layers: []string{"a: &a {k: 1}\nb: &b {<<: *a, l: 2}\nc: {<<: *b}\n"},
			want:   "a:\n  k: 1\nb:\n  k: 1\n  l: 2\nc:\n  k: 1\n  l: 2\n",
		},
		"a quoted key that spells a merge key is a key": {
			layers: []string{"m: {\"<<\": {a: 1}}\n", "m: {'<<': {b: 2}}\n"},
			want:   "m:\n  '<<':\n    a: 1\n    b: 2\n",
		},
		"a scalar keeps the text of its layer": {
			layers: []string{strings.Join([]string{
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
				"",
			}, "\n")},
			want: strings.Join([]string{
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
				"",
			}, "\n"),
		},
		"a string of several lines reads as one double-quoted line": {
			layers: []string{
				"lit: |\n  one\n  two\nfold: >-\n  one\n  two\nplain: one\n  two\n\n  three\n" +
					"single: 'one\n\n  two'\n? |\n  key\n: v\n",
			},
			want: `lit: "one\ntwo\n"` + "\n" + `fold: "one two"` + "\nplain: \"one two\\nthree\"\n" +
				`single: "one\ntwo"` + "\n" + `"key\n": v` + "\n",
		},
		"a plain scalar that a block line reads another way takes quotes": {
			layers: []string{"a: [-, ?, a b, -x]\nb: {k: -}\n"},
			want:   "a:\n  - \"-\"\n  - \"?\"\n  - a b\n  - -x\nb:\n  k: \"-\"\n",
		},
		"a tag stays on its value": {
			layers: []string{
				"bin: !!binary aGVsbG8=\nstr: !!str 5\nset: !!set {a, b}\nobj: !obj {k: v}\n" +
					"list: !list [x]\nitems:\n  - !obj {k: v}\n  - !list [y]\n  - !!str 7\n" +
					"none: !obj {}\n!!str 1: key\nnul: !!str ~\n",
			},
			want: "bin: !!binary aGVsbG8=\nstr: !!str 5\nset: !!set\n  a: null\n  b: null\n" +
				"obj: !obj\n  k: v\nlist: !list\n  - x\nitems:\n  - !obj\n    k: v\n  - !list\n    - y\n" +
				"  - !!str 7\nnone: !obj {}\n!!str 1: key\nnul: !!str ~\n",
		},
		"a merged mapping takes the tag of the highest layer that writes one": {
			layers: []string{"m: !a {x: 1}\nn: !a {x: 1}\n", "m: {y: 2}\nn: !b {y: 2}\n"},
			want:   "m: !a\n  x: 1\n  y: 2\nn: !b\n  x: 1\n  y: 2\n",
		},
		"a tag at the root goes above its collection": {
			layers: []string{"!obj\na: 1\n"},
			want:   "!obj\na: 1\n",
		},
		"collections nest in block style": {
			layers: []string{
				"a: [[x, y], {k: v, l: [1, 2]}, [], {}, [[z]]]\nb: {c: {d: [{e: f, g: {h: i}}]}}\n",
			},
			want: "a:\n  - - x\n    - y\n  - k: v\n    l:\n      - 1\n      - 2\n  - []\n  - {}\n  - - - z\n" +
				"b:\n  c:\n    d:\n      - e: f\n        g:\n          h: i\n",
		},
		"the preamble of the lowest layer stays and other comments go": {
			layers: []string{
				"# base header\n---\n# lead\na: 1 # trailing\n# between\nb: 2\n",
				"# prod header\n---\nc: 3\n",
			},
			want: "# base header\n---\n# lead\na: 1\nb: 2\nc: 3\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			nodes := layerNodes(t, tc.layers...)
			layers := niceyaml.NewSourceFromLayers(layersOf(nodes)...)

			assert.Equal(t, tc.want, mergedText(t, layers))

			for expr, at := range tc.binds {
				path := paths.MustParse(expr)

				err := layers.Bind(niceyaml.NewError("here", niceyaml.AtPath(path)))
				require.EqualError(t, err, fmt.Sprintf("%s: %s: here", at, path), expr)
			}

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
		input: "str: !!str 3.10\nbin: !!binary aGVsbG8=\nint: !!int 7\nfloat: !!float 1\n" +
			"bool: !!bool true\nnul: !!null null\ntime: !!timestamp 2001-12-14\nquoted: !!str \"5\"\n",
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
		input: "scalar: !custom value\nmapping: !custom {k: v}\nsequence: !custom [x, y]\n" +
			"block: !custom\n  k: v\nitems:\n  - !custom {k: v}\n  - !custom\n    - x\n  - !custom 7\n",
	},
	"a local tag over no value": {
		input: "first: 1\nlast: !custom\n",
	},
	"a mapping tag over no value": {
		input: "first: 1\nlast: &anchored !!map\n",
	},
	"a sequence tag over no value": {
		input: "first: 1\nlast:\n  - !!seq\n",
	},
	"a string tag over no value": {
		input: "first: 1\nlast: !!str\n",
	},
	"literal block scalars": {
		input: "clip: |\n  one\n  two\nstrip: |-\n  one\n  two\nkeep: |+\n  one\n  two\n\n" +
			"indent: |2\n   lead\n  rest\nblank: |\n  one\n\n  two\nlast: |\n  end\n",
		typed: func() any { return new(map[string]string) },
	},
	"folded block scalars": {
		input: "clip: >\n  one\n  two\nstrip: >-\n  one\n  two\nkeep: >+\n  one\n  two\n\n" +
			"more: >\n  one\n\n  two\n    indented\n  three\nlast: >\n  end\n",
		typed: func() any { return new(map[string]string) },
	},
	"quoted strings": {
		input: `dq: "tab\there \"quoted\" \\ back \u00e9 \x41 \n line"` + "\n" +
			"sq: 'it''s \\n kept'\nempty: \"\"\nnone: ''\npadded: \"  wide  \"\n" +
			"colon: \"a: b\"\nhash: 'a #b'\ndash: \"- item\"\nmarker: \"---\"\n" +
			"folded: \"one\n  two\"\nword: 'true'\nnumber: \"1\"\n",
		typed: func() any { return new(map[string]string) },
	},
	"plain scalars": {
		input: "int: 42\nneg: -7\nhex: 0x1F\noct: 0o17\nbin: 0b101\nfloat: 1.50\nexp: 1e3\n" +
			"inf: .inf\nunder: 1_000\nplus: +1\nversion: 1.10.0\ndate: 2001-12-14\n" +
			"stamp: 2001-12-14T21:59:43Z\nyes: yes\nbool: True\nnul: Null\ntilde: ~\nabsent:\n" +
			"url: http://example.com/a?b=c#d\nlead: -dash\ninner: a:b\nwords: one two  three\n" +
			"lines: one\n  two\n\n  three\n",
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
		input: "\"quoted key\": 1\n'single key': 2\n\"key: colon\": 3\n\"#hash\": 4\n" +
			"1: int\n1.5: float\n0x10: hex\ntrue: bool\n~: nul\n? explicit\n: value\n" +
			"\"- dash\": 5\n\"multi\\nline\": 6\n? |\n  block\n: 7\n!!str 9: tagged\n",
	},
	"flow collections": {
		input: "seq: [a, [b, c], {d: e}, [], {}]\nmap: {a: 1, b: [2, 3], c: {d: {e: f}}}\n" +
			"lines: [a,\n  b,\n  c]\nset: {a, b}\nodd: [-, ?, a b, \"x, y\"]\npair: [a: 1]\n",
	},
	"block collections": {
		input: "items:\n  - name: a\n    ports: [1, 2]\n  - name: b\n    nested:\n      - - x\n        - y\n" +
			"      - k:\n          - z\n  - plain\n  -\n  - ~\ndeep:\n  a:\n    b:\n      c: [1]\n",
	},
	"anchors and aliases": {
		input: "base: &base {a: 1, b: [x, y]}\ncopy: *base\nscalar: &s hello\nagain: *s\n" +
			"list: [*s, *base, &t tail, *t]\nnested: &n\n  inner: &i {deep: 1}\n  again: *i\n" +
			"whole: *n\n*s : key\ntagged: &g !!str 5\nread: *g\n",
	},
	"aliases inside their own anchor": {
		input: "outer: &outer\n  inner: &inner\n    self: *inner\n    list: [1, *inner]\n" +
			"  merged:\n    <<: *inner\n    more: 2\n  nested: &nested {up: *outer}\ncopy: *nested\n",
	},
	"merge keys": {
		input: "base: &base {a: 1, b: 2}\nmore: &more {c: 3}\none: {<<: *base, d: 4}\n" +
			"many: {<<: [*base, *more], e: 5}\nblock:\n  <<: *more\n  f: 6\ninline: {<<: {g: 7}, h: 8}\n" +
			"chain: &chain {<<: *base, i: 9}\nlast: {<<: *chain}\n",
	},
	"unicode": {
		input: "名前: 値\nemoji: \"😀\"\naccent: héllo wörld\nrtl: \"שלום\"\n" +
			"nbsp: \"a\\u00a0b\"\nline: \"a\\u2028b\"\nbom: \"\\ufeffx\"\nwide: ｆｕｌｌ\n",
		typed: func() any { return new(map[string]string) },
	},
	"comments": {
		input: "# head\na: 1 # trailing\n\n# middle\nb:\n  # inner\n  c: 2\n# tail\n",
	},
}

func TestLayers_Decode_Equivalence(t *testing.T) {
	t.Parallel()

	// A value decodes from a document and from layers alike.
	type decoder interface {
		DecodeInto(ctx context.Context, v any, opts ...niceyaml.DecodeOption) error
	}

	// One layer merges into what it holds, and an empty mapping above or
	// below it changes nothing, so every arrangement decodes as the
	// document alone does.
	arrangements := map[string]func(doc, empty *niceyaml.Node) *niceyaml.Source{
		"alone":                  func(doc, _ *niceyaml.Node) *niceyaml.Source { return niceyaml.NewSourceFromLayers(doc) },
		"over an empty mapping":  func(doc, empty *niceyaml.Node) *niceyaml.Source { return niceyaml.NewSourceFromLayers(empty, doc) },
		"under an empty mapping": func(doc, empty *niceyaml.Node) *niceyaml.Source { return niceyaml.NewSourceFromLayers(doc, empty) },
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

		for arrangement, layer := range arrangements {
			t.Run(name+" "+arrangement, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, tc.input, niceyaml.WithName("doc.yaml"))
				empty := yamltest.FirstDocument(t, "{}\n", niceyaml.WithName("empty.yaml"))
				layers := layer(doc, empty)

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

func TestLayers_Decode_Equivalence_Errors(t *testing.T) {
	t.Parallel()

	const input = "name: [shop]\nserver:\n  host: example.com\n  port: many\n  prot: 1\n" +
		"servers:\n  - host: a\nbackup: &b {host: b}\nnested:\n  inner: *b\n" +
		"nul: !!null null\nlimits: {timeout: ~}\n"

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
				return niceyaml.NewSummary("checks",
					niceyaml.NewError("bad host", niceyaml.AtPath(paths.Current().Child("server", "host"))),
					niceyaml.NewError("no zone", niceyaml.AtPath(paths.Doc().Child("server", "zone"))),
					n.NewError("bad inner", niceyaml.AtPath(paths.Doc().Child("nested", "inner", "host"))),
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

			_, got := niceyaml.NewSourceFromLayers(doc).Decode[strict](t.Context(), opts...)
			require.EqualError(t, got, want.Error())
			assert.Equal(t, niceyaml.FormatError(want, 0), niceyaml.FormatError(got, 0))
		})
	}

	t.Run("values that fail their own checks", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t,
			"server:\n  host: example.com\nservers:\n  - host: a\nbackup: &b {host: b}\nnested:\n  inner: *b\n",
			niceyaml.WithName("doc.yaml"))

		_, want := doc.Decode[layerConfig](t.Context())
		require.Error(t, want)

		_, got := niceyaml.NewSourceFromLayers(doc).Decode[layerConfig](t.Context())
		require.EqualError(t, got, want.Error())
	})
}

func TestLayers_Decode_Options(t *testing.T) {
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

			layers := niceyaml.NewSourceFromLayers(layersOf(layerSources(t, tc.base, "level: high\n"))...)

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

func TestLayers_Decode_Maps(t *testing.T) {
	t.Parallel()

	type service struct {
		Image    string `yaml:"image"`
		Replicas int    `yaml:"replicas"`
	}

	type config struct {
		Services map[string]service `yaml:"services"`
		Extra    map[string]any     `yaml:"extra"`
	}

	layers := niceyaml.NewSourceFromLayers(layersOf(layerSources(t,
		"services:\n  web: {image: nginx, replicas: 1}\n  db: {image: postgres, replicas: 1}\n"+
			"extra: {a: {b: 1, c: 2}, z: 1}\n",
		"services:\n  web: {replicas: 5}\nextra: {a: {b: 9}}\n",
	))...)

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

		got, err := niceyaml.NewSourceFromLayers(layersOf(nodes)...).Decode[ports](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"80": "proxy", "443": "https"}, got.Ports)

		type numbered struct {
			Ports map[int]string `yaml:"ports"`
		}

		byNumber, err := niceyaml.NewSourceFromLayers(layersOf(nodes)...).Decode[numbered](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[int]string{80: "proxy", 443: "https"}, byNumber.Ports)
	})
}

func TestLayers_Decode_Scalars(t *testing.T) {
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

	const (
		baseInput = "version: 1.10\nhex: 0x1F\ncount: 0x10\nratio: 1.50\nwhen: 2001-12-14\n"
		prodInput = "text: |\n  one\n  two\nquoted: 'true'\ndata: !!binary aGVsbG8=\n"
	)

	// The merged document spells each scalar as its layer does, so a
	// decode reads from it what it reads from one file that holds them
	// all.
	want, err := yamltest.FirstDocument(t, baseInput+prodInput).Decode[config](t.Context())
	require.NoError(t, err)

	got, err := niceyaml.NewSourceFromLayers(layersOf(layerSources(t, baseInput, prodInput))...).Decode[config](
		t.Context(),
	)
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

func TestLayers_Decode_Unmarshaler(t *testing.T) {
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

		got, err := niceyaml.NewSourceFromLayers(layersOf(nodes)...).Decode[config](t.Context())
		require.NoError(t, err)
		assert.Equal(t, mergedStore{Kind: "disk", Path: "/b"}, got.Store)
	})

	t.Run("its error binds in the highest layer that holds its mapping", func(t *testing.T) {
		t.Parallel()

		nodes := layerNodes(t, "name: base\nstore: {path: /a}\n", "store: {path: /b}\n")

		_, err := niceyaml.NewSourceFromLayers(layersOf(nodes)...).Decode[config](t.Context())
		require.EqualError(t, err, "prod.yaml:1:1: $.store: store needs a kind")

		_, err = niceyaml.NewSourceFromLayers(nodes[0], layerNodes(t, "name: prod\n")[0]).Decode[config](t.Context())
		require.EqualError(t, err, "base.yaml:2:1: $.store: store needs a kind")
	})
}

func TestLayers_Decode_SourceSettings(t *testing.T) {
	t.Parallel()

	t.Run("each layer reads its own reference documents", func(t *testing.T) {
		t.Parallel()

		shared := niceyaml.NewSourceFromString(
			"base: &base {port: 9, host: shared}\nserver: &server {<<: *base, tls: true}\n",
		)
		other := niceyaml.NewSourceFromString("base: &base {port: 1}\n")

		// Both files read an anchor named base, each from its own
		// reference document, and prod.yaml defines the name itself too.
		base := yamltest.FirstDocument(t, "db: *base\n",
			niceyaml.WithName("base.yaml"), niceyaml.WithReferences(other))
		prod := yamltest.FirstDocument(
			t,
			"web: *server\napi: {<<: *base, port: 2}\nbase: &base {port: 3}\ndb: {host: x}\n",
			niceyaml.WithName("prod.yaml"),
			niceyaml.WithReferences(shared),
		)

		layers := niceyaml.NewSourceFromLayers(base, prod)

		assert.Equal(t, "db:\n  port: 1\n  host: x\n"+
			"web:\n  port: 9\n  host: shared\n  tls: true\n"+
			"api:\n  port: 2\n  host: shared\n"+
			"base:\n  port: 3\n", mergedText(t, layers))

		// A decode of each layer alone reads the same values.
		alone, err := prod.Decode[map[string]any](t.Context())
		require.NoError(t, err)

		got, err := layers.Decode[map[string]any](t.Context())
		require.NoError(t, err)
		assert.Equal(t, alone["web"], got["web"])
		assert.Equal(t, alone["api"], got["api"])

		// A value from a reference document has no line in the layer, so
		// an error under it binds at the alias.
		err = layers.Bind(niceyaml.NewError("here", niceyaml.AtPath(paths.Doc().Child("web", "port"))))
		require.EqualError(t, err, "prod.yaml:1:6: $.web.port: here")

		err = layers.Bind(niceyaml.NewError("here", niceyaml.AtPath(paths.Doc().Child("db", "port"))))
		require.EqualError(t, err, "base.yaml:1:5: $.db.port: here")
	})

	t.Run("a layer that allows a duplicate key keeps the last one", func(t *testing.T) {
		t.Parallel()

		base := yamltest.FirstDocument(t, "a: 1\na: 2\nb: {c: 1}\nb: {d: 2}\n",
			niceyaml.WithName("base.yaml"), niceyaml.WithAllowDuplicateKeys(true))
		prod := yamltest.FirstDocument(t, "e: 5\n", niceyaml.WithName("prod.yaml"))

		layers := niceyaml.NewSourceFromLayers(base, prod)

		assert.Equal(t, "a: 2\nb:\n  d: 2\ne: 5\n", mergedText(t, layers))

		err := layers.Bind(niceyaml.NewError("here", niceyaml.AtPath(paths.Doc().Child("a"))))
		require.EqualError(t, err, "base.yaml:2:4: $.a: here")

		// A layer that does not allow one fails as it does alone.
		strict := niceyaml.NewSourceFromString("a: 1\na: 2\n", niceyaml.WithName("prod.yaml")).AllDocuments()
		require.Len(t, strict, 1)

		_, err = niceyaml.NewSourceFromLayers(base, strict[0]).Decode[any](t.Context())
		require.ErrorIs(t, err, niceyaml.ErrSyntax)
		require.ErrorIs(t, err, strict[0].Err())
	})

	t.Run("a layer past the alias limit returns the error of its decode", func(t *testing.T) {
		t.Parallel()

		input := yamltest.MergeLevels(7)

		base := yamltest.FirstDocument(t, "a: 1\n", niceyaml.WithName("base.yaml"))
		prod := yamltest.FirstDocument(t, input, niceyaml.WithName("prod.yaml"))

		_, alone := prod.Decode[any](t.Context())
		require.ErrorIs(t, alone, niceyaml.ErrExcessiveAliasing)

		layers := niceyaml.NewSourceFromLayers(base, prod)

		_, err := layers.Decode[any](t.Context())
		require.ErrorIs(t, err, niceyaml.ErrExcessiveAliasing)
		require.EqualError(t, err, alone.Error())

		// The layer holds no value, so no path resolves in the merged
		// document.
		err = layers.Bind(niceyaml.NewError("here", niceyaml.AtPath(paths.Doc().Child("a"))))
		require.EqualError(t, err, "base.yaml: $.a: here")
	})

	t.Run("a layer must pass the alias limit as text", func(t *testing.T) {
		t.Parallel()

		// Every alias copies ten thousand bytes, which a decode into an
		// any value shares and the merged text writes out.
		input := "long: &long " + strings.Repeat("x", 10_000) + "\nlist:\n" + strings.Repeat("  - *long\n", 150)

		limited := yamltest.FirstDocument(t, input, niceyaml.WithName("prod.yaml"))

		_, err := limited.Decode[any](t.Context())
		require.NoError(t, err)

		_, err = niceyaml.NewSourceFromLayers(limited).Decode[any](t.Context())
		require.ErrorIs(t, err, niceyaml.ErrExcessiveAliasing)
		require.EqualError(t, err, "prod.yaml:1:1: excessive aliasing")
		assert.True(t, niceyaml.IsInvalid(err))

		trusted := yamltest.FirstDocument(t, input, niceyaml.WithAliasLimit(false))

		got, err := niceyaml.NewSourceFromLayers(trusted).Decode[map[string]any](t.Context())
		require.NoError(t, err)
		assert.Len(t, got["list"], 150)
	})

	t.Run("a key with no name returns an error", func(t *testing.T) {
		t.Parallel()

		// The key is an alias to a mapping, which no path names.
		nodes := layerNodes(t, "a: 1\n", "x: &x {a: 1}\n? *x\n: v\n")

		_, alone := nodes[1].Decode[any](t.Context())
		require.NoError(t, alone)

		_, err := niceyaml.NewSourceFromLayers(layersOf(nodes)...).Decode[any](t.Context())
		require.EqualError(t, err, "prod.yaml:2:3: mapping key has no name")
		require.ErrorIs(t, err, niceyaml.ErrUnnamedKey)
		assert.True(t, niceyaml.IsInvalid(err))

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Same(t, nodes[1], bound.Node())
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
		_, err := niceyaml.NewSourceFromLayers(layersOf(nodes)...).Decode[any](t.Context())
		require.EqualError(t, err, "prod.yaml:1:9: $.server: could not find alias \"shared\"")

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Same(t, nodes[1], bound.Node())

		// The Source of the layer takes the reference document.
		prod := yamltest.FirstDocument(t, prodInput, niceyaml.WithName("prod.yaml"),
			niceyaml.WithReferences(niceyaml.NewSourceFromString(shared)))

		got, err := niceyaml.NewSourceFromLayers(nodes[0], prod).Decode[map[string]any](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]any{
			"server": map[string]any{"host": "shared.example.com", "port": uint64(80)},
		}, got)
	})

	t.Run("a scope that holds a marker as a key quotes it at the root", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, "a:\n  ---: 1\n  ...: 2\n", niceyaml.WithName("base.yaml"))
		scoped := yamltest.At(t, doc, paths.Doc().Child("a"))

		layers := niceyaml.NewSourceFromLayers(scoped)

		assert.Equal(t, "\"---\": 1\n\"...\": 2\n", mergedText(t, layers))

		err := layers.Bind(niceyaml.NewError("here", niceyaml.AtPath(paths.Doc().Child("..."))))
		require.EqualError(t, err, "base.yaml:3:8: $.a.'...': here")
	})
}

func TestLayers_Document_Excerpts(t *testing.T) {
	t.Parallel()

	const (
		baseInput = "db:\n  host: db.internal\n"
		prodInput = "db:\n  port: 5432\n"
		envInput  = "db:\n  password: hunter2\n"
	)

	shown := func(t *testing.T, input, name string, opts ...niceyaml.SourceOption) *niceyaml.Node {
		t.Helper()

		return yamltest.FirstDocument(t, input, append(opts, niceyaml.WithName(name))...)
	}

	off := func(t *testing.T, input, name string) *niceyaml.Node {
		t.Helper()

		return shown(t, input, name, niceyaml.WithExcerpts(false))
	}

	// The merged text holds the values of every layer, so its Source has
	// excerpts off when the Source of any layer has, whichever layer gave
	// the Source its name.
	tcs := map[string]struct {
		layers func(t *testing.T) []*niceyaml.Node
		want   bool
	}{
		"every layer has excerpts on": {
			layers: func(t *testing.T) []*niceyaml.Node {
				t.Helper()

				return []*niceyaml.Node{shown(t, baseInput, "base.yaml"), shown(t, prodInput, "prod.yaml")}
			},
			want: true,
		},
		"the highest layer has excerpts off": {
			layers: func(t *testing.T) []*niceyaml.Node {
				t.Helper()

				return []*niceyaml.Node{shown(t, baseInput, "base.yaml"), off(t, envInput, "environment")}
			},
		},
		"the lowest layer has excerpts off": {
			layers: func(t *testing.T) []*niceyaml.Node {
				t.Helper()

				return []*niceyaml.Node{off(t, envInput, "environment"), shown(t, baseInput, "base.yaml")}
			},
		},
		"a layer between two others has excerpts off": {
			layers: func(t *testing.T) []*niceyaml.Node {
				t.Helper()

				return []*niceyaml.Node{
					shown(t, baseInput, "base.yaml"),
					off(t, envInput, "environment"),
					shown(t, prodInput, "prod.yaml"),
				}
			},
		},
		"a layer scoped to a value has excerpts off": {
			layers: func(t *testing.T) []*niceyaml.Node {
				t.Helper()

				scoped := yamltest.At(t, off(t, "defaults:\n  db: {password: hunter2}\n", "environment"),
					paths.Doc().Child("defaults"))

				return []*niceyaml.Node{shown(t, baseInput, "base.yaml"), scoped}
			},
		},
		// The merged text holds the value the alias reads, which no line
		// of the layer holds.
		"a layer reads a reference document with excerpts off": {
			layers: func(t *testing.T) []*niceyaml.Node {
				t.Helper()

				refs := niceyaml.NewSourceFromString("password: &password hunter2\n", niceyaml.WithExcerpts(false))

				return []*niceyaml.Node{
					shown(t, baseInput, "base.yaml"),
					shown(t, "db:\n  password: *password\n", "prod.yaml", niceyaml.WithReferences(refs)),
				}
			},
		},
		"a layer reads a reference document through another": {
			layers: func(t *testing.T) []*niceyaml.Node {
				t.Helper()

				refs := niceyaml.NewSourceFromString("password: &password hunter2\n", niceyaml.WithExcerpts(false))
				shared := niceyaml.NewSourceFromString("db: &db {password: *password}\n", niceyaml.WithReferences(refs))

				return []*niceyaml.Node{
					shown(t, baseInput, "base.yaml"),
					shown(t, "db: *db\n", "prod.yaml", niceyaml.WithReferences(shared)),
				}
			},
		},
		"a merged layer holds a layer with excerpts off": {
			layers: func(t *testing.T) []*niceyaml.Node {
				t.Helper()

				inner := niceyaml.NewSourceFromLayers(shown(t, baseInput, "base.yaml"), off(t, envInput, "environment"))

				return []*niceyaml.Node{mergedDocument(t, inner), shown(t, prodInput, "prod.yaml")}
			},
		},
		"a merged layer holds layers with excerpts on": {
			layers: func(t *testing.T) []*niceyaml.Node {
				t.Helper()

				inner := niceyaml.NewSourceFromLayers(
					shown(t, baseInput, "base.yaml"),
					shown(t, prodInput, "prod.yaml"),
				)

				return []*niceyaml.Node{mergedDocument(t, inner), shown(t, "db:\n  name: app\n", "local.yaml")}
			},
			want: true,
		},
		"no layer": {
			layers: func(*testing.T) []*niceyaml.Node { return nil },
			want:   true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			layers := niceyaml.NewSourceFromLayers(layersOf(tc.layers(t))...)

			assert.Equal(t, tc.want, mergedDocument(t, layers).Source().Excerpts())

			// The Source has excerpts off for each merged text that holds
			// the secret.
			assert.Equal(t, !tc.want, strings.Contains(mergedText(t, layers), "hunter2"))
		})
	}
}

func TestLayers_Document_ExcerptWidth(t *testing.T) {
	t.Parallel()

	layer := func(t *testing.T, input, name string, opts ...niceyaml.SourceOption) *niceyaml.Node {
		t.Helper()

		return yamltest.FirstDocument(t, input, append(opts, niceyaml.WithName(name))...)
	}

	// The merged document takes the width of the lowest layer, as it
	// takes the other settings of that layer.
	tcs := map[string]struct {
		base []niceyaml.SourceOption
		prod []niceyaml.SourceOption
		want int
	}{
		"no layer sets a width": {
			want: niceyaml.DefaultExcerptWidth,
		},
		"the lowest layer sets a width": {
			base: []niceyaml.SourceOption{niceyaml.WithExcerptWidth(40)},
			want: 40,
		},
		"the lowest layer shows whole lines": {
			base: []niceyaml.SourceOption{niceyaml.WithExcerptWidth(0)},
			prod: []niceyaml.SourceOption{niceyaml.WithExcerptWidth(40)},
			want: 0,
		},
		"a higher layer sets a width": {
			prod: []niceyaml.SourceOption{niceyaml.WithExcerptWidth(40)},
			want: niceyaml.DefaultExcerptWidth,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			layers := niceyaml.NewSourceFromLayers(
				layer(t, "db:\n  host: db.internal\n", "base.yaml", tc.base...),
				layer(t, "db:\n  port: 5432\n", "prod.yaml", tc.prod...),
			)

			assert.Equal(t, tc.want, mergedDocument(t, layers).Source().ExcerptWidth())
		})
	}
}

func TestNewSourceFromLayers_MergedLayer(t *testing.T) {
	t.Parallel()

	// The blank line keeps the lines of base.yaml apart from the lines of
	// any document it merges into.
	const (
		baseInput = "# base\nname: shop\n\nserver:\n  host: example.com\n  port: 0\n"
		midInput  = "server:\n  host: mid.example.com\n"
		prodInput = "server:\n  tls: {cert: 1}\n"
	)

	// Each case layers base.yaml, mid.yaml, and prod.yaml in that order,
	// with some of them merged into one document first. Every error
	// binds where it binds with no layer merged.
	tcs := map[string]struct {
		layers func(t *testing.T, base, mid, prod *niceyaml.Node) *niceyaml.Source
	}{
		"no layer merged": {
			layers: func(_ *testing.T, base, mid, prod *niceyaml.Node) *niceyaml.Source {
				return niceyaml.NewSourceFromLayers(base, mid, prod)
			},
		},
		"the lower two merged": {
			layers: func(t *testing.T, base, mid, prod *niceyaml.Node) *niceyaml.Source {
				t.Helper()

				return niceyaml.NewSourceFromLayers(mergedDocument(t, niceyaml.NewSourceFromLayers(base, mid)), prod)
			},
		},
		"the upper two merged": {
			layers: func(t *testing.T, base, mid, prod *niceyaml.Node) *niceyaml.Source {
				t.Helper()

				return niceyaml.NewSourceFromLayers(base, mergedDocument(t, niceyaml.NewSourceFromLayers(mid, prod)))
			},
		},
		"all three merged": {
			layers: func(t *testing.T, base, mid, prod *niceyaml.Node) *niceyaml.Source {
				t.Helper()

				return niceyaml.NewSourceFromLayers(mergedDocument(t, niceyaml.NewSourceFromLayers(base, mid, prod)))
			},
		},
		"the lower two merged twice": {
			layers: func(t *testing.T, base, mid, prod *niceyaml.Node) *niceyaml.Source {
				t.Helper()

				lower := mergedDocument(t, niceyaml.NewSourceFromLayers(base, mid))

				return niceyaml.NewSourceFromLayers(mergedDocument(t, niceyaml.NewSourceFromLayers(lower)), prod)
			},
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

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			layers := tc.layers(t,
				yamltest.FirstDocument(t, baseInput, niceyaml.WithName("base.yaml")),
				yamltest.FirstDocument(t, midInput, niceyaml.WithName("mid.yaml")),
				yamltest.FirstDocument(t, prodInput, niceyaml.WithName("prod.yaml")),
			)

			assert.Equal(t, "# base\nname: shop\nserver:\n  host: mid.example.com\n  port: 0\n  tls:\n    cert: 1\n",
				mergedText(t, layers))

			for expr, at := range binds {
				path := paths.MustParse(expr)

				err := layers.Bind(niceyaml.NewError("here", niceyaml.AtPath(path)))
				require.EqualError(t, err, fmt.Sprintf("%s: %s: here", at, path), expr)
			}

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
			"defaults:\n  server:\n    host: example.com\n    port: 0\n",
			niceyaml.WithName("base.yaml"),
		)
		defaults := yamltest.At(t, base, paths.Doc().Child("defaults"))
		mid := yamltest.FirstDocument(t, midInput, niceyaml.WithName("mid.yaml"))
		prod := yamltest.FirstDocument(t, "tls: {cert: 1}\n", niceyaml.WithName("prod.yaml"))

		lower := mergedDocument(t, niceyaml.NewSourceFromLayers(defaults, mid))
		layers := niceyaml.NewSourceFromLayers(yamltest.At(t, lower, paths.Doc().Child("server")), prod)

		assert.Equal(t, "host: mid.example.com\nport: 0\ntls:\n  cert: 1\n", mergedText(t, layers))

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

		var bound *niceyaml.SourceError

		require.ErrorAs(t, err, &bound)
		assert.Same(t, defaults, bound.Node())
		assert.Same(t, base, bound.Document())

		path, ok := bound.Path()
		require.True(t, ok)
		assert.Equal(t, "$.defaults.server.port", path.String())
	})

	t.Run("a merged document is a layer as a file of its text is", func(t *testing.T) {
		t.Parallel()

		nodes := layerNodes(t, "x: {p: 1}\n", "x: 5\n", "x: {q: 2}\n")

		// The scalar of mid.yaml replaces the mapping of base.yaml, and
		// the mapping of prod.yaml replaces the scalar.
		assert.Equal(t, "x:\n  q: 2\n", mergedText(t, niceyaml.NewSourceFromLayers(layersOf(nodes)...)))

		// The document of mid.yaml and prod.yaml holds a mapping, which
		// merges into the mapping of base.yaml.
		upper := mergedDocument(t, niceyaml.NewSourceFromLayers(nodes[1], nodes[2]))
		layers := niceyaml.NewSourceFromLayers(nodes[0], upper)

		assert.Equal(t, "x:\n  p: 1\n  q: 2\n", mergedText(t, layers))

		err := layers.Bind(niceyaml.NewError("here", niceyaml.AtPath(paths.Doc().Child("x", "p"))))
		require.EqualError(t, err, "base.yaml:1:8: $.x.p: here")

		err = layers.Bind(niceyaml.NewError("here", niceyaml.AtPath(paths.Doc().Child("x", "q"))))
		require.EqualError(t, err, "prod.yaml:1:8: $.x.q: here")
	})
}

// mergedDocument returns the root Node of the document layers merge
// into.
func mergedDocument(t *testing.T, layers *niceyaml.Source) *niceyaml.Node {
	t.Helper()

	doc, err := layers.Document()
	require.NoError(t, err)

	return doc
}

// mergedText returns the text of the document layers merge into, with a
// line break behind each line.
func mergedText(t *testing.T, layers *niceyaml.Source) string {
	t.Helper()

	text := mergedDocument(t, layers).Source().View().Held().Content()
	if text == "" {
		return ""
	}

	return text + "\n"
}
