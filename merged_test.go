package niceyaml_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"math"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/encoder"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
	"go.jacobcolvin.com/niceyaml/schema"
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
			layers: []string{stringtest.LinesLF("a: 1", "b: [x]"), stringtest.LinesLF("a: {k: v}", "b: {l: w}")},
			want: stringtest.LinesLF(
				"a:",
				"  k: v",
				"b:",
				"  l: w",
			),
		},
		"an unwritten null keeps the value below it": {
			layers: []string{
				stringtest.LinesLF("name: shop", "web: {image: nginx}", "tags: [x]"),
				stringtest.LinesLF(
					"name:",
					"web:",
					"  # image: other",
					"tags: !!null",
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
		"a written null removes the key below it": {
			layers: []string{
				stringtest.LinesLF(
					"name: shop",
					"proxy:",
					"  url: http://proxy:3128",
					"tags: [x]",
					"port: 8080",
				),
				stringtest.LinesLF("name: ~", "proxy: null", "tags: Null"),
			},
			want: "port: 8080\n",
			binds: map[string]string{
				"$.name":      "prod.yaml:1:7",
				"$.proxy":     "prod.yaml:2:8",
				"$.proxy~":    "prod.yaml:2:1",
				"$.proxy.url": "prod.yaml:2:1",
				"$.tags":      "prod.yaml:3:7",
				"$.port":      "base.yaml:5:7",
			},
		},
		"a written null removes a key at any depth": {
			layers: []string{"env: {A: 1, B: 2}\n", "env: {B: ~}\n"},
			want:   stringtest.LinesLF("env:", "  A: 1"),
			binds: map[string]string{
				"$.env":   "prod.yaml:1:1",
				"$.env.A": "base.yaml:1:10",
				"$.env.B": "prod.yaml:1:10",
			},
		},
		"a mapping stays once a layer removed each of its keys": {
			layers: []string{"env: {B: 2}\n", "env: {B: ~}\n"},
			want:   "env: {}\n",
			binds:  map[string]string{"$.env.B": "prod.yaml:1:10"},
		},
		"a value over a removed key holds none of the value below": {
			layers: []string{
				stringtest.LinesLF("proxy: {url: x, port: 1}", "name: shop"),
				"proxy: ~\n",
				"proxy: {port: 2}\n",
			},
			want: stringtest.LinesLF("proxy:", "  port: 2", "name: shop"),
			binds: map[string]string{
				"$.proxy.port": "prod.yaml:1:15",
				"$.proxy.url":  "prod.yaml:1:1",
			},
		},
		"a null stays where no layer below holds a value": {
			layers: []string{stringtest.LinesLF("a: 1", "d: null"), stringtest.LinesLF("b: ~", "c:", "d: ~")},
			want: stringtest.LinesLF(
				"a: 1",
				"d: ~",
				"b: ~",
				"c:",
			),
			binds: map[string]string{
				"$.b": "prod.yaml:1:4",
				"$.d": "prod.yaml:3:4",
			},
		},
		"an unwritten null keeps the written null below it": {
			layers: []string{stringtest.LinesLF("d: null", "e:"), stringtest.LinesLF("d:", "e:")},
			want:   stringtest.LinesLF("d: null", "e:"),
			binds: map[string]string{
				"$.d": "base.yaml:1:4",
				"$.e": "prod.yaml:2:4",
			},
		},
		"a null in a sequence stays, since the sequence replaces": {
			layers: []string{"list: [{a: 1}, x]\n", "list: [{a: ~}, ~]\n"},
			want:   stringtest.LinesLF("list:", "  - a: ~", "  - ~"),
		},
		"an empty mapping keeps the mapping below and an empty sequence replaces": {
			layers: []string{stringtest.LinesLF("m: {a: 1}", "s: [x]"), stringtest.LinesLF("m: {}", "s: []")},
			want:   stringtest.LinesLF("m:", "  a: 1", "s: []"),
			binds: map[string]string{
				"$.m":   "prod.yaml:1:1",
				"$.m.a": "base.yaml:1:8",
				"$.s":   "prod.yaml:2:1",
			},
		},
		"empty collections stay where nothing fills them": {
			layers: []string{stringtest.LinesLF("m: {}", "s: []"), "n: {}\n"},
			want:   stringtest.LinesLF("m: {}", "s: []", "n: {}"),
		},
		"three layers apply in order": {
			layers: []string{stringtest.LinesLF("a: 1", "b: 1", "c: 1"), stringtest.LinesLF("b: 2", "c: 2"), "c: 3\n"},
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
			want:   stringtest.LinesLF("ports:", `  "80": z`, "  443: b"),
			binds: map[string]string{
				"$.ports.80":  "prod.yaml:1:15",
				"$.ports.80~": "prod.yaml:1:9",
				"$.ports.443": "base.yaml:1:21",
			},
		},
		"a key keeps the spelling of the layer its value comes from": {
			layers: []string{"ports: {80: a}\n", "ports: {'80': }\n"},
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
				stringtest.LinesLF("x: &d {image: prod}", "api: *d"),
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
				stringtest.LinesLF("d: &d {replicas: 7}", "web: *d"),
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
				"  self:",
				"c:",
				"  b: 1",
				"  self:",
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
			layers: []string{stringtest.LinesLF("a: [-, ?, a b, -x]", "b: {k: -}")},
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
					"blank: !!str",
				),
			},
			want: stringtest.LinesLF(
				"bin: !!binary aGVsbG8=",
				"str: !!str 5",
				"set: !!set",
				"  a:",
				"  b:",
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
				"blank: !!str null",
			),
		},
		"a merged mapping takes the tag of the highest layer that writes one": {
			layers: []string{
				stringtest.LinesLF("m: !a {x: 1}", "n: !a {x: 1}"),
				stringtest.LinesLF("m: {y: 2}", "n: !b {y: 2}"),
			},
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
			layers: []string{stringtest.LinesLF("!obj", "a: 1")},
			want:   stringtest.LinesLF("!obj", "a: 1"),
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
			layers := mergedLayers(t, nodes)

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

func TestNewSourceFromLayers_Decode_Nulls(t *testing.T) {
	t.Parallel()

	var (
		// The document of base.yaml, which is the merged text where its
		// proxy stays.
		kept = stringtest.LinesLF("proxy:", "  url: http://proxy:3128", "port: 8080")
		// The merged text where prod.yaml removed the proxy.
		removed = "port: 8080\n"
	)

	// Each case puts prod.yaml over a base.yaml that sets a proxy and a
	// port, and reads the text of the merged document. A null that
	// prod.yaml writes removes the proxy, and one it leaves unwritten
	// keeps the proxy, whatever layout holds the null.
	tcs := map[string]struct {
		prod string
		want string
	}{
		"the word null removes":                {prod: "proxy: null\n", want: removed},
		"a tilde removes":                      {prod: "proxy: ~\n", want: removed},
		"a capitalized null removes":           {prod: "proxy: Null\n", want: removed},
		"an upper-case null removes":           {prod: "proxy: NULL\n", want: removed},
		"a null before a comment removes":      {prod: "proxy: null # none\n", want: removed},
		"a null below its key removes":         {prod: stringtest.LinesLF("proxy:", "  ~"), want: removed},
		"a null in a flow mapping removes":     {prod: "{proxy: null}\n", want: removed},
		"a null under a quoted key removes":    {prod: "\"proxy\": ~\n", want: removed},
		"a null under an explicit key removes": {prod: "? proxy\n: ~\n", want: removed},
		"a null before a CRLF removes":         {prod: "proxy: ~\r\n", want: removed},
		"a null under a null tag removes":      {prod: "proxy: !!null ~\n", want: removed},
		"an anchored null removes":             {prod: "proxy: &none ~\n", want: removed},
		"an alias to a written null removes": {
			prod: stringtest.LinesLF("none: &none ~", "proxy: *none"),
			want: removed + "none: ~\n",
		},
		"a written null that a merge key brings in removes": {
			prod: stringtest.LinesLF("none: &none {proxy: ~}", "<<: *none"),
			want: removed + stringtest.LinesLF("none:", "  proxy: ~"),
		},
		"a key with nothing after it keeps": {prod: "proxy:\n", want: kept},
		"a key before a comment keeps":      {prod: "proxy: # none\n", want: kept},
		"a key above comments alone keeps": {
			prod: stringtest.LinesLF("proxy:", "  # url: http://other:3128"),
			want: kept,
		},
		"a key with no value in a flow mapping keeps": {prod: "{proxy: }\n", want: kept},
		"a key alone in a flow mapping keeps":         {prod: "{proxy}\n", want: kept},
		"an explicit key with no value keeps":         {prod: "? proxy\n", want: kept},
		"a key before a CRLF keeps":                   {prod: "proxy:\r\n", want: kept},
		"a tag with no value keeps":                   {prod: "proxy: !!null\n", want: kept},
		"an anchor with no value keeps": {
			prod: stringtest.LinesLF("proxy: &none", "other: *none"),
			want: kept + "other:\n",
		},
		"an alias to an unwritten null keeps": {
			prod: stringtest.LinesLF("none: &none", "proxy: *none"),
			want: kept + "none:\n",
		},
		"an unwritten null that a merge key brings in keeps": {
			prod: stringtest.LinesLF("none: &none", "  proxy:", "<<: *none"),
			want: kept + stringtest.LinesLF("none:", "  proxy:"),
		},
		// A tag that converts a null stays on the null, which then keeps
		// the value below it whether prod.yaml writes it or not.
		"a written null under a string tag keeps":    {prod: "proxy: !!str ~\n", want: kept},
		"an unwritten null under a string tag keeps": {prod: "proxy: !!str\n", want: kept},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			layers := layerFiles(t, kept, tc.prod)

			assert.Equal(t, tc.want, mergedText(t, layers))
		})
	}
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
		"keys no field reads":        {niceyaml.WithUnknownFields(niceyaml.UnknownFieldsReject)},
		"a validator that fails": {niceyaml.WithValidator(niceyaml.ValidatorFunc(
			func(_ context.Context, n *niceyaml.Node) error {
				return niceyaml.NewSummary(
					"checks",
					yamltest.ErrorAt("bad host", "server", "host"),
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

	levels := niceyaml.WithUnmarshaler(niceyaml.CustomUnmarshaler(decodeLevel))

	// The options reach the one decode of the merged document, so the
	// function decodes a level whichever layer holds it, and its error
	// binds in that layer.
	tcs := map[string]struct {
		base string
		err  string
		want config
	}{
		"function decodes the value of each layer": {
			base: stringtest.LinesLF("level: low", "backup: low"),
			want: config{Level: levelHigh, Backup: levelLow},
		},
		"error of the function binds in the layer of its value": {
			base: stringtest.LinesLF("level: low", "backup: mid"),
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

		got, err := mergedLayers(t, nodes).Decode[ports](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"80": "proxy", "443": "https"}, got.Ports)

		type numbered struct {
			Ports map[int]string `yaml:"ports"`
		}

		byNumber, err := mergedLayers(t, nodes).Decode[numbered](t.Context())
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

		got, err := mergedLayers(t, nodes).Decode[config](t.Context())
		require.NoError(t, err)
		assert.Equal(t, mergedStore{Kind: "disk", Path: "/b"}, got.Store)
	})

	t.Run("its error binds in the highest layer that holds its mapping", func(t *testing.T) {
		t.Parallel()

		nodes := layerNodes(t, stringtest.LinesLF("name: base", "store: {path: /a}"), "store: {path: /b}\n")

		_, err := mergedLayers(t, nodes).Decode[config](t.Context())
		require.EqualError(t, err, "prod.yaml:1:1: $.store: store needs a kind")

		_, err = yamltest.Layers(t, nodes[0], layerNodes(t, "name: prod\n")[0]).Decode[config](t.Context())
		require.EqualError(t, err, "base.yaml:2:1: $.store: store needs a kind")
	})
}

func TestNewSourceFromLayers_Decode_SourceSettings(t *testing.T) {
	t.Parallel()

	t.Run("each layer reads its own reference documents", func(t *testing.T) {
		t.Parallel()

		// Both files read an anchor named base, each from its own
		// reference document, and prod.yaml defines the name itself too.
		base := yamltest.FirstDocument(t, "db: *base\n",
			niceyaml.WithName("base.yaml"), yamltest.References("base: &base {port: 1}\n"))
		prod := yamltest.FirstDocument(
			t,
			stringtest.LinesLF(
				"web: *server",
				"api: {<<: *base, port: 2}",
				"base: &base {port: 3}",
				"db: {host: x}",
			),
			niceyaml.WithName("prod.yaml"),
			yamltest.References(
				stringtest.LinesLF("base: &base {port: 9, host: shared}", "server: &server {<<: *base, tls: true}"),
			),
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
		strict := yamltest.UnparsedDocument(t, stringtest.LinesLF("a: 1", "a: 2"), niceyaml.WithName("prod.yaml"))

		_, err := niceyaml.NewSourceFromLayers(t.Context(), []*niceyaml.Node{base, strict})
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

		_, err := niceyaml.NewSourceFromLayers(t.Context(), []*niceyaml.Node{base, prod})
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

		// The 27th alias takes the text the aliases repeat past the byte
		// limit, so the error binds there.
		_, err = niceyaml.NewSourceFromLayers(t.Context(), []*niceyaml.Node{limited})
		require.ErrorIs(t, err, niceyaml.ErrExcessiveAliasing)
		require.ErrorIs(t, err, niceyaml.ErrLimit)
		require.EqualError(t, err, "prod.yaml:29:5: aliases repeat more than 256 KiB of text")
		assert.True(t, niceyaml.IsInvalid(err))

		// With the byte limit off, the aliases still make up too much of
		// the text. Every alias repeats the same text, so the error binds
		// at the first.
		unbounded := yamltest.FirstDocument(t, input, niceyaml.WithName("prod.yaml"), niceyaml.WithByteLimit(0))

		_, err = niceyaml.NewSourceFromLayers(t.Context(), []*niceyaml.Node{unbounded})
		require.ErrorIs(t, err, niceyaml.ErrExcessiveAliasing)
		require.NotErrorIs(t, err, niceyaml.ErrLimit)
		require.EqualError(t, err, "prod.yaml:3:5: aliases expand the document past the alias limit")

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

		_, err := niceyaml.NewSourceFromLayers(t.Context(), nodes)
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
		_, err := niceyaml.NewSourceFromLayers(t.Context(), nodes)
		require.EqualError(t, err, "prod.yaml:1:9: $.server: could not find alias \"shared\"")

		bound := yamltest.RequireSourceError(t, err)
		assert.Same(t, nodes[1], yamltest.BoundNode(t, bound))

		// The Source of the layer takes the reference document.
		prod := yamltest.FirstDocument(t, prodInput, niceyaml.WithName("prod.yaml"), yamltest.References(shared))

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

		assert.Equal(t, stringtest.LinesLF(`"---": 1`, `"...": 2`), mergedText(t, layers))

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
				layersDocument(t, shown(baseInput, "base.yaml"), off(envInput, "environment")),
				shown(prodInput, "prod.yaml"),
			},
		},
		"a merged layer holds layers with excerpts on": {
			layers: []*niceyaml.Node{
				layersDocument(t, shown(baseInput, "base.yaml"), shown(prodInput, "prod.yaml")),
				shown(stringtest.LinesLF("db:", "  name: app"), "local.yaml"),
			},
			want: true,
		},
		"no layer": {want: true},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			layers := mergedLayers(t, tc.layers)

			assert.Equal(t, tc.want, yamltest.Document(t, layers).Source().ShowsExcerpts())

			// The Source has excerpts off for each merged text that holds
			// the secret.
			assert.Equal(t, !tc.want, strings.Contains(mergedText(t, layers), "hunter2"))
		})
	}
}

func TestNewSourceFromLayers_MergedLayer(t *testing.T) {
	t.Parallel()

	var (
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

			return []niceyaml.Layer{layersDocument(t, base, mid), prod}
		},
		"the upper two merged": func(t *testing.T, base, mid, prod *niceyaml.Node) []niceyaml.Layer {
			t.Helper()

			return []niceyaml.Layer{base, layersDocument(t, mid, prod)}
		},
		"all three merged": func(t *testing.T, base, mid, prod *niceyaml.Node) []niceyaml.Layer {
			t.Helper()

			return []niceyaml.Layer{layersDocument(t, base, mid, prod)}
		},
		"the lower two merged twice": func(t *testing.T, base, mid, prod *niceyaml.Node) []niceyaml.Layer {
			t.Helper()

			lower := layersDocument(t, base, mid)

			return []niceyaml.Layer{layersDocument(t, lower), prod}
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

			nodes := layerNodes(t, layerShop, midInput, prodInput)
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

	// Each case layers its three files in every arrangement of tcs. A key
	// that a file removes stays out of the merged text and binds at the
	// null that removed it, whichever layers merged first.
	removals := map[string]struct {
		binds map[string]string
		base  string
		mid   string
		prod  string
		want  string
	}{
		"a key that a file removed stays removed": {
			base: stringtest.LinesLF("name: shop", "proxy: {url: x}", "env: {A: 1, B: 2}"),
			mid:  stringtest.LinesLF("proxy: ~", "env: {B: ~}"),
			prod: "name: prod\n",
			want: stringtest.LinesLF("name: prod", "env:", "  A: 1"),
			binds: map[string]string{
				"$.proxy": "mid.yaml:1:8",
				"$.env.B": "mid.yaml:2:10",
				"$.env.A": "base.yaml:3:10",
			},
		},
		"a second written null keeps the key removed": {
			base: stringtest.LinesLF("name: shop", "proxy: {url: x}"),
			mid:  "proxy: ~\n",
			prod: "proxy: null\n",
			want: "name: shop\n",
			binds: map[string]string{
				"$.proxy": "prod.yaml:1:8",
			},
		},
		"an unwritten null removes nothing": {
			base: stringtest.LinesLF("name: shop", "server: {port: 80}"),
			mid:  stringtest.LinesLF("server:", "  # port: 9090"),
			prod: "name: prod\n",
			want: stringtest.LinesLF("name: prod", "server:", "  port: 80"),
			binds: map[string]string{
				"$.server.port": "base.yaml:2:16",
			},
		},
		"an unwritten null above a written one removes": {
			base: stringtest.LinesLF("name: shop", "proxy: {url: x}"),
			mid:  "proxy: ~\n",
			prod: "proxy:\n",
			want: "name: shop\n",
			binds: map[string]string{
				"$.proxy": "mid.yaml:1:8",
			},
		},
		// The merged text spells the null of mid.yaml as a written one
		// behind its tag.
		"an unwritten null under a string tag removes nothing": {
			base: stringtest.LinesLF("name: shop", "proxy: {url: x}"),
			mid:  "proxy: !!str\n",
			prod: "name: prod\n",
			want: stringtest.LinesLF("name: prod", "proxy:", "  url: x"),
			binds: map[string]string{
				"$.proxy.url": "base.yaml:2:14",
			},
		},
	}

	for name, tc := range removals {
		for arrangement, arrange := range tcs {
			t.Run(name+", "+arrangement, func(t *testing.T) {
				t.Parallel()

				nodes := layerNodes(t, tc.base, tc.mid, tc.prod)
				layers := yamltest.Layers(t, arrange(t, nodes[0], nodes[1], nodes[2])...)

				assert.Equal(t, tc.want, mergedText(t, layers))

				requireLayerBinds(t, layers, tc.binds)
			})
		}
	}

	t.Run("a value over a key that a merged layer removed stands where the key stood", func(t *testing.T) {
		t.Parallel()

		nodes := layerNodes(t,
			stringtest.LinesLF("proxy: {url: x, port: 1}", "name: shop"),
			"proxy: ~\n",
			"proxy: {port: 2}\n",
		)

		// The document of base.yaml and mid.yaml holds no proxy, and the
		// mapping of prod.yaml takes the place the removed key had, as it
		// does with no layer merged (see the Decode_Merge case "a value
		// over a removed key holds none of the value below").
		lower := layersDocument(t, nodes[0], nodes[1])
		layers := yamltest.Layers(t, lower, nodes[2])

		assert.Equal(t, stringtest.LinesLF("proxy:", "  port: 2", "name: shop"), mergedText(t, layers))

		requireLayerBinds(t, layers, map[string]string{
			"$.proxy.port": "prod.yaml:1:15",
			"$.proxy.url":  "prod.yaml:1:1",
		})

		// The document of mid.yaml and prod.yaml holds the mapping of
		// prod.yaml and no null, so it merges into the mapping of
		// base.yaml as a file of its text does.
		upper := layersDocument(t, nodes[1], nodes[2])

		assert.Equal(t,
			stringtest.LinesLF("proxy:", "  url: x", "  port: 2", "name: shop"),
			mergedText(t, yamltest.Layers(t, nodes[0], upper)),
		)
	})

	t.Run("a scoped Node of a merged document removes what its layers removed", func(t *testing.T) {
		t.Parallel()

		nodes := layerNodes(t,
			"proxy: {url: default}\n",
			stringtest.LinesLF("app:", "  name: shop", "  proxy: {url: x}"),
			stringtest.LinesLF("app:", "  proxy: ~"),
		)

		// The merged text of the upper two files holds no proxy under app.
		upper := layersDocument(t, nodes[1], nodes[2])
		layers := yamltest.Layers(t, nodes[0], yamltest.At(t, upper, paths.Doc().Child("app")))

		assert.Equal(t, "name: shop\n", mergedText(t, layers))

		err := layers.Bind(niceyaml.NewError("here", niceyaml.AtPath(paths.Doc().Child("proxy"))))
		require.EqualError(t, err, "prod.yaml:2:10: $.app.proxy: here")
	})

	t.Run("a scoped Node of a merged document binds in its files with their paths", func(t *testing.T) {
		t.Parallel()

		base := yamltest.FirstDocument(t, layerDefaults, niceyaml.WithName("base.yaml"))
		defaults := yamltest.At(t, base, paths.Doc().Child("defaults"))
		mid := yamltest.FirstDocument(t, midInput, niceyaml.WithName("mid.yaml"))
		prod := yamltest.FirstDocument(t, "tls: {cert: 1}\n", niceyaml.WithName("prod.yaml"))

		lower := layersDocument(t, defaults, mid)
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
		tcs := map[string]struct {
			err  error
			want string
		}{
			"a value of the lowest file": {
				err:  niceyaml.NewError("here", niceyaml.AtPath(paths.Doc().Child("port"))),
				want: "base.yaml:4:11: $.defaults.server.port: here",
			},
			"a value of the file above it": {
				err:  yamltest.ErrorAt("here", "host"),
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

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				require.EqualError(t, layers.Bind(tc.err), tc.want)
			})
		}

		_, err := layers.Decode[layerServer](t.Context())
		require.EqualError(t, err, "base.yaml:4:11: $.defaults.server.port: port must be at least 1")

		bound := yamltest.RequireSourceError(t, err)
		assert.Same(t, defaults, yamltest.BoundNode(t, bound))
		assert.Same(t, base, yamltest.BoundDocument(t, bound))
		assert.Equal(t, "$.defaults.server.port", yamltest.PathOf(t, bound).String())
	})

	t.Run("a merged document is a layer as a file of its text is", func(t *testing.T) {
		t.Parallel()

		nodes := layerNodes(t, "x: {p: 1}\n", "x: 5\n", "x: {q: 2}\n")

		// The scalar of mid.yaml replaces the mapping of base.yaml, and
		// the mapping of prod.yaml replaces the scalar.
		assert.Equal(t, stringtest.LinesLF("x:", "  q: 2"), mergedText(t, mergedLayers(t, nodes)))

		// The document of mid.yaml and prod.yaml holds a mapping, which
		// merges into the mapping of base.yaml.
		upper := layersDocument(t, nodes[1], nodes[2])
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

	t.Run("a context that ended returns its error", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		sources := layerSources(t, layerBadPort, layerProdHost)

		// The error binds in the layer the merge was reading.
		merged, err := niceyaml.NewSourceFromLayers(ctx, sources)
		require.EqualError(t, err, "base.yaml: context canceled")
		require.ErrorIs(t, err, context.Canceled)
		assert.Nil(t, merged)

		bound := yamltest.RequireSourceError(t, err)
		assert.Same(t, sources[0], bound.Source())

		// A merge of no layer reads nothing.
		merged, err = niceyaml.NewSourceFromLayers(ctx, []niceyaml.Layer{})
		require.NoError(t, err)
		require.NotNil(t, merged)
	})

	t.Run("a context that ends while a layer writes its aliases out returns its error", func(t *testing.T) {
		t.Parallel()

		// The decode of each layer reads the context a few times, so the
		// context ends among the values the aliases of prod.yaml write.
		ctx := &endingContext{Context: t.Context(), lasts: 100}

		merged, err := niceyaml.NewSourceFromLayers(ctx, []*niceyaml.Source{base, prod})
		require.EqualError(t, err, "prod.yaml: context canceled")
		require.ErrorIs(t, err, context.Canceled)
		assert.Nil(t, merged)

		bound := yamltest.RequireSourceError(t, err)
		assert.Same(t, prod, bound.Source())
	})

	t.Run("a context that ends once every layer is read returns its error", func(t *testing.T) {
		t.Parallel()

		whole := &endingContext{Context: t.Context(), lasts: math.MaxInt64}

		_, err := niceyaml.NewSourceFromLayers(whole, []*niceyaml.Source{base, prod})
		require.NoError(t, err)

		// The merge reads the context a last time before it parses the
		// merged text, and binds that error in the lowest layer.
		ctx := &endingContext{Context: t.Context(), lasts: whole.calls.Load() - 1}

		merged, err := niceyaml.NewSourceFromLayers(ctx, []*niceyaml.Source{base, prod})
		require.EqualError(t, err, "base.yaml: context canceled")
		require.ErrorIs(t, err, context.Canceled)
		assert.Nil(t, merged)

		bound := yamltest.RequireSourceError(t, err)
		assert.Same(t, base, bound.Source())

		// With MergeAs, that error binds in the layer it names.
		ctx = &endingContext{Context: t.Context(), lasts: whole.calls.Load() - 1}

		merged, err = niceyaml.NewSourceFromLayers(ctx, []*niceyaml.Source{base, prod}, niceyaml.MergeAs(prod))
		require.EqualError(t, err, "prod.yaml: context canceled")
		require.ErrorIs(t, err, context.Canceled)
		assert.Nil(t, merged)

		bound = yamltest.RequireSourceError(t, err)
		assert.Same(t, prod, bound.Source())
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

func ExampleNewSourceFromLayers() {
	ctx := context.Background()

	base := niceyaml.NewSourceFromString(`server:
  host: example.com
  port: 0
`, niceyaml.WithName("base.yaml"))

	prod := niceyaml.NewSourceFromString(`server:
  host: prod.example.com
servers:
  - host: cache.internal
`, niceyaml.WithName("prod.yaml"))

	merged, err := niceyaml.NewSourceFromLayers(ctx, []*niceyaml.Source{base, prod})
	if err != nil {
		log.Fatal(err)
	}

	// The port of the server comes from base.yaml, and the list of servers
	// from prod.yaml.
	_, err = merged.Decode[layerConfig](ctx)
	fmt.Println(niceyaml.FormatError(err, niceyaml.WithContextLines(0)))

	// Output:
	// |-- prod.yaml:4:3: $.servers[0].port: port must be at least 1
	// `-- base.yaml:3:9: $.server.port: port must be at least 1
	//
	// base.yaml
	//    3 |   port: 0
	//      |         ^ port must be at least 1
	//
	// prod.yaml
	//    4 |   - host: cache.internal
	//      |   ^ port must be at least 1
}

func ExampleNewSourceFromLayers_document() {
	ctx := context.Background()

	base := niceyaml.NewSourceFromString(`kind: Service

server:
  host: example.com
  port: 0
`, niceyaml.WithName("base.yaml"))

	prod := niceyaml.NewSourceFromString(`server:
  host: prod.example.com
`, niceyaml.WithName("prod.yaml"))

	merged, err := niceyaml.NewSourceFromLayers(ctx, []*niceyaml.Source{base, prod})
	if err != nil {
		log.Fatal(err)
	}

	doc, err := merged.Document()
	if err != nil {
		log.Fatal(err)
	}

	// One value of the layers decodes on its own.
	host, err := doc.DecodeAt[string](ctx, paths.Doc().Child("server", "host"))
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(host)

	// The view holds the text the layers merge into, which is no file.
	fmt.Println(doc.View().Held().Content())

	// An error binds in the file that holds its value.
	fmt.Println(
		doc.Bind(niceyaml.NewError("port must be at least 1", niceyaml.AtPath(paths.Doc().Child("server", "port")))),
	)

	// Output:
	// prod.example.com
	// kind: Service
	// server:
	//   host: prod.example.com
	//   port: 0
	// base.yaml:5:9: $.server.port: port must be at least 1
}

func ExampleNewSourceFromLayers_environment() {
	ctx := context.Background()

	// The file leaves the port to the environment.
	base := niceyaml.NewSourceFromString(`server:
  host: example.com
`, niceyaml.WithName("base.yaml"))

	// The program read APP_SERVER_PORT=0. The map holds the keys the
	// environment set and no other, under the names the YAML uses, and the
	// port as the integer a file would write.
	overrides := map[string]any{
		"server": map[string]any{"port": 0},
	}

	// A comment names the variable beside its value.
	data, err := encoder.Marshal(ctx, overrides, encoder.WithYAMLComments(yaml.CommentMap{
		"$.server.port": {yaml.LineComment(" APP_SERVER_PORT")},
	}))
	if err != nil {
		log.Fatal(err)
	}

	env := niceyaml.NewSourceFromBytes(data, niceyaml.WithName("environment"))

	requires := schema.MustCompile([]byte(`{
		"properties": {"server": {"required": ["host", "port"]}}
	}`))

	merged, err := niceyaml.NewSourceFromLayers(ctx, []*niceyaml.Source{base, env})
	if err != nil {
		log.Fatal(err)
	}

	// The schema finds the port it requires in the environment, and the
	// check of the server reports the port in that layer.
	_, err = merged.Decode[layerConfig](ctx, niceyaml.WithValidator(requires))
	fmt.Println(niceyaml.FormatError(err, niceyaml.WithContextLines(0)))

	// Output:
	// environment:2:9: $.server.port: port must be at least 1
	//
	//    2 |   port: 0 # APP_SERVER_PORT
	//      |         ^
}

func ExampleMergeAs() {
	ctx := context.Background()

	// The user edits config.yaml, whose directive names its schema.
	fsys := fstest.MapFS{
		"config.yaml": {Data: []byte(`# yaml-language-server: $schema=./config.schema.json
server:
  port: 99999
log:
  level: loud
`)},
		"config.schema.json": {Data: []byte(`{
			"properties": {
				"server": {"properties": {"port": {"maximum": 65535}}},
				"log": {"properties": {"level": {"enum": ["debug", "info"]}}}
			}
		}`)},
	}

	user, err := niceyaml.NewSourceFromFS(fsys, "config.yaml")
	if err != nil {
		log.Fatal(err)
	}

	// The program embeds its defaults below the file of the user.
	defaults := niceyaml.NewSourceFromString(`server:
  host: localhost
  port: 8080
`, niceyaml.WithName("defaults.yaml"))

	// The merged Source stands for config.yaml, so the directive of that
	// file picks the schema, and the heading names the file.
	merged, err := niceyaml.NewSourceFromLayers(ctx,
		[]*niceyaml.Source{defaults, user}, niceyaml.MergeAs(user))
	if err != nil {
		log.Fatal(err)
	}

	registry := schema.NewRegistry(schema.WithResolvers(schema.Directive()))
	fmt.Println(merged.ValidateDocuments(ctx, registry))

	// Output:
	// config.yaml: 2 schema violations
	// config.yaml:3:9: $.server.port: 99999 is greater than 65535
	// config.yaml:5:10: $.log.level: value does not match any enum member
}

func ExampleMergeUnmarshalers() {
	ctx := context.Background()

	// The program embeds its defaults below an optional file of the user.
	defaults := niceyaml.NewSourceFromString("allow: 10.0.0.0/8\n", niceyaml.WithName("defaults.yaml"))

	// The user has no config.yaml on this host.
	var user *niceyaml.Source

	cidr := niceyaml.CustomUnmarshaler(func(_ context.Context, n *net.IPNet, decode func(any) error) error {
		var s string

		err := decode(&s)
		if err != nil {
			return err
		}

		_, parsed, err := net.ParseCIDR(s)
		if err != nil {
			return err //nolint:wrapcheck // The decode binds the error at the value.
		}

		*n = *parsed

		return nil
	})

	// The unmarshalers belong to the merge, so the decode reads the
	// network whichever files exist.
	merged, err := niceyaml.NewSourceFromLayers(ctx, []*niceyaml.Source{defaults, user},
		niceyaml.MergeAs(user),
		niceyaml.MergeUnmarshalers(cidr),
	)
	if err != nil {
		log.Fatal(err)
	}

	type Config struct {
		Allow net.IPNet `yaml:"allow"`
	}

	cfg, err := merged.Decode[Config](ctx)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(cfg.Allow.String())

	// Output:
	// 10.0.0.0/8
}

func ExampleNode_Origin() {
	base := niceyaml.NewSourceFromString(`name: shop
license: LICENSE
`, niceyaml.WithFilePath("conf/base.yaml"))

	prod := niceyaml.NewSourceFromString(`# Production.

license: LICENSE.prod
`, niceyaml.WithFilePath("conf/prod/app.yaml"))

	merged, err := niceyaml.NewSourceFromLayers(context.Background(), []*niceyaml.Source{base, prod})
	if err != nil {
		log.Fatal(err)
	}

	doc, err := merged.Document()
	if err != nil {
		log.Fatal(err)
	}

	license, err := doc.At(paths.Doc().Child("license"))
	if err != nil {
		log.Fatal(err)
	}

	// The Node reads the merged text, under the file path of base.yaml.
	fmt.Printf("%s:%d\n", license.FilePath(), license.Span().Start+1)

	// Its origin reads the file that sets the license.
	origin, err := license.Origin()
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("%s:%d\n", origin.FilePath(), origin.Span().Start+1)

	// A Node of a file is its own origin.
	own, err := origin.Origin()
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(own == origin)

	// Output:
	// conf/base.yaml:2
	// conf/prod/app.yaml:3
	// true
}

func TestNewSourceFromLayers(t *testing.T) {
	t.Parallel()

	const want = "base.yaml:3:9: $.server.port: port must be at least 1"

	t.Run("a Source is the layer its one document is", func(t *testing.T) {
		t.Parallel()

		sources := layerSources(t, layerBadPort, layerProdHost)

		base := yamltest.Document(t, sources[0])
		prod := yamltest.Document(t, sources[1])

		tcs := map[string]*niceyaml.Source{
			"two Sources":           yamltest.Layers(t, sources[0], sources[1]),
			"two Nodes":             yamltest.Layers(t, base, prod),
			"a Source under a Node": yamltest.Layers(t, sources[0], prod),
			"a Node under a Source": yamltest.Layers(t, base, sources[1]),
		}

		for name, layers := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				var cfg layerConfig

				// The decode validates once and binds in the layer below.
				err := layers.DecodeInto(t.Context(), &cfg)
				require.EqualError(t, err, want)
				assert.True(t, niceyaml.IsInvalid(err))
				assert.Equal(t, "prod.example.com", cfg.Server.Host)

				bound := yamltest.RequireSourceError(t, err)
				assert.Same(t, base, yamltest.BoundNode(t, bound))
				assert.Same(t, base, yamltest.BoundDocument(t, bound))
				assert.Same(t, sources[0], bound.Source())
				assert.Equal(t, "$.server.port", yamltest.PathOf(t, bound).String())

				assert.Equal(t,
					stringtest.LinesLF("server:", "  host: prod.example.com", "  port: 0"),
					mergedText(t, layers),
				)
			})
		}
	})

	t.Run("a Source layers beside a Node of another document", func(t *testing.T) {
		t.Parallel()

		base := layerSources(t, layerBadPort)[0]
		profiles := yamltest.FirstDocument(t,
			stringtest.LinesLF(
				"profiles:",
				"  prod:",
				"    server:",
				"      host: prod.example.com",
			),
			niceyaml.WithName("profiles.yaml"),
		)
		prod := yamltest.At(t, profiles, paths.Doc().Child("profiles", "prod"))

		layers := yamltest.Layers(t, base, prod)

		_, err := layers.Decode[layerConfig](t.Context())
		require.EqualError(t, err, want)

		err = layers.Bind(niceyaml.NewError("unknown host", niceyaml.AtPath(paths.Doc().Child("server", "host"))))
		require.EqualError(t, err, "profiles.yaml:4:13: $.profiles.prod.server.host: unknown host")
	})

	t.Run("a nil layer adds nothing", func(t *testing.T) {
		t.Parallel()

		var (
			source *niceyaml.Source
			node   *niceyaml.Node
			layer  niceyaml.Layer
		)

		sources := layerSources(t, layerBadPort, layerProdHost)
		base, prod := sources[0], sources[1]

		tcs := map[string]*niceyaml.Source{
			"a nil Layer":  yamltest.Layers(t, nil, base, layer, prod, nil),
			"a nil Source": yamltest.Layers(t, source, base, source, prod, source),
			"a nil Node":   yamltest.Layers(t, node, base, node, prod, node),
		}

		for name, layers := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				_, err := layers.Decode[layerConfig](t.Context())
				require.EqualError(t, err, want)

				// The lowest layer is the first one that is not nil.
				err = layers.Bind(errors.New("quota service: connection refused"))
				require.EqualError(t, err, "base.yaml: quota service: connection refused")
			})
		}
	})

	t.Run("a file that does not exist adds nothing", func(t *testing.T) {
		t.Parallel()

		fsys := yamltest.MapFS(map[string]string{"base.yaml": layerBadPort})

		base, err := niceyaml.NewSourceFromFS(fsys, "base.yaml")
		require.NoError(t, err)

		user, err := niceyaml.NewSourceFromFS(fsys, "user.yaml")
		require.ErrorIs(t, err, fs.ErrNotExist)

		_, err = yamltest.Layers(t, base, user).Decode[layerConfig](t.Context())
		require.EqualError(t, err, want)
	})

	t.Run("a Source that ends in a header holds one document", func(t *testing.T) {
		t.Parallel()

		base := niceyaml.NewSourceFromString(
			stringtest.LinesLF(
				"server:",
				"  host: example.com",
				"  port: 80",
				"---",
			),
			niceyaml.WithName("base.yaml"),
		)

		cfg, err := yamltest.Layers(t, base).Decode[layerConfig](t.Context())
		require.NoError(t, err)
		assert.Equal(t, layerServer{Host: "example.com", Port: 80}, cfg.Server)
	})

	t.Run("a Source of empty documents alone adds nothing", func(t *testing.T) {
		t.Parallel()

		// An overlay that a template comments out renders as headers
		// alone, and it layers as an empty file does.
		base := layerSources(t, layerBadPort)[0]
		overlay := niceyaml.NewSourceFromString(
			stringtest.LinesLF("---", "# off", "---"),
			niceyaml.WithName("overlay.yaml"),
		)

		placements := map[string][]niceyaml.Layer{
			"on top": {base, overlay},
			"lowest": {overlay, base},
		}

		for placement, layers := range placements {
			t.Run(placement, func(t *testing.T) {
				t.Parallel()

				_, err := yamltest.Layers(t, layers...).Decode[layerConfig](t.Context())
				require.EqualError(t, err, want)
			})
		}
	})

	t.Run("the documents of one file layer in order", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(
			layerBadPort+stringtest.LinesLF("---", "server:", "  port: 80"),
			niceyaml.WithName("app.yaml"),
		)

		docs := yamltest.Documents(t, source, 2)

		cfg, err := mergedLayers(t, docs).Decode[layerConfig](t.Context())
		require.NoError(t, err)
		assert.Equal(t, layerServer{Host: "example.com", Port: 80}, cfg.Server)
	})

	t.Run("a change of the slice of the caller reaches no layer", func(t *testing.T) {
		t.Parallel()

		given := layersOf(layerSources(t, layerBadPort, layerProdHost))
		layers := yamltest.Layers(t, given...)

		clear(given)

		_, err := layers.Decode[layerConfig](t.Context())
		require.EqualError(t, err, want)
	})

	t.Run("a Source that holds no one document returns its error", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			err   error
			input string
			want  string
		}{
			"several documents": {
				input: stringtest.LinesLF(
					"server:",
					"  port: 1",
					"---",
					"server:",
					"  port: 2",
				),
				want: "prod.yaml:3:1: multiple documents in source: 2 documents",
				err:  niceyaml.ErrMultipleDocuments,
			},
			"a syntax error": {
				input: "server: [\n",
				want:  "prod.yaml:1:9: " + unclosedSequence,
				err:   niceyaml.ErrSyntax,
			},
			"a syntax error below a document that parses": {
				input: stringtest.LinesLF(
					"server:",
					"  port: 1",
					"---",
					"server: [",
				),
				want: "prod.yaml:4:9: " + unclosedSequence,
				err:  niceyaml.ErrSyntax,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				base := layerSources(t, layerBadPort)[0]
				broken := niceyaml.NewSourceFromString(tc.input, niceyaml.WithName("prod.yaml"))

				_, err := broken.Document()
				require.EqualError(t, err, tc.want)

				// The merge returns the error the Source returns for its one
				// document, wherever the layer lies.
				placements := map[string][]niceyaml.Layer{
					"on top": {base, broken},
					"lowest": {broken, base},
					"alone":  {broken},
				}

				for placement, layers := range placements {
					t.Run(placement, func(t *testing.T) {
						t.Parallel()

						merged, err := niceyaml.NewSourceFromLayers(t.Context(), layers)
						require.EqualError(t, err, tc.want)
						require.ErrorIs(t, err, tc.err)
						assert.Nil(t, merged)

						assert.Same(t, broken, yamltest.RequireSourceError(t, err).Source())
					})
				}
			})
		}
	})

	t.Run("a Node whose document did not parse returns its error", func(t *testing.T) {
		t.Parallel()

		broken := yamltest.UnparsedDocument(t, "server: [\n", niceyaml.WithName("prod.yaml"))
		require.ErrorIs(t, broken.Err(), niceyaml.ErrSyntax)

		merged, err := niceyaml.NewSourceFromLayers(
			t.Context(),
			[]*niceyaml.Node{layerNodes(t, layerBadPort)[0], broken},
		)
		require.EqualError(t, err, "prod.yaml:1:9: "+unclosedSequence)
		require.ErrorIs(t, err, broken.Err())
		assert.Nil(t, merged)
	})

	t.Run("the lowest layer that holds no value names the error", func(t *testing.T) {
		t.Parallel()

		// Each outer layer fails as a decode of it alone does.
		nodes := layerNodes(t, "server: *missing\n", layerBadPort, "name: *gone\n")

		_, alone := nodes[0].Decode[any](t.Context())
		require.EqualError(t, alone, "base.yaml:1:9: $.server: could not find alias \"missing\"")

		_, err := niceyaml.NewSourceFromLayers(t.Context(), nodes)
		require.EqualError(t, err, alone.Error())

		bound := yamltest.RequireSourceError(t, err)
		assert.Same(t, nodes[0], yamltest.BoundNode(t, bound))
	})
}

func TestMergeAs(t *testing.T) {
	t.Parallel()

	// Each layer has a name, a file path, a file system, and a directive of
	// its own. The directive names a schema beside the file that requires a
	// key named after the layer.
	defaults := identityLayer(t, "usr/share/app", "defaults", stringtest.LinesLF(
		"server:",
		"  host: localhost",
		"  port: 8080",
		"log: info",
	))
	user := identityLayer(t, "etc/app", "user", stringtest.LinesLF("server:", "  port: 9090"))
	env := identityLayer(t, "run/app", "env", "log: debug\n")
	stranger := identityLayer(t, "tmp", "stranger", "log: warn\n")

	sources := []*niceyaml.Source{defaults, user, env}

	registry := schema.NewRegistry(schema.WithResolvers(schema.Directive()))

	t.Run("the merged Source stands for one layer", func(t *testing.T) {
		t.Parallel()

		var (
			source *niceyaml.Source
			node   *niceyaml.Node
		)

		tcs := map[string]struct {
			want *niceyaml.Source
			opts []niceyaml.MergeOption
		}{
			"no option keeps the lowest layer": {
				want: defaults,
			},
			"a middle layer": {
				opts: []niceyaml.MergeOption{niceyaml.MergeAs(user)},
				want: user,
			},
			"the top layer": {
				opts: []niceyaml.MergeOption{niceyaml.MergeAs(env)},
				want: env,
			},
			"a nil Layer keeps the lowest layer": {
				opts: []niceyaml.MergeOption{niceyaml.MergeAs(nil)},
				want: defaults,
			},
			"a nil Source keeps the lowest layer": {
				opts: []niceyaml.MergeOption{niceyaml.MergeAs(source)},
				want: defaults,
			},
			"a nil Node keeps the lowest layer": {
				opts: []niceyaml.MergeOption{niceyaml.MergeAs(node)},
				want: defaults,
			},
			"the last of several options applies": {
				opts: []niceyaml.MergeOption{niceyaml.MergeAs(env), niceyaml.MergeAs(user)},
				want: user,
			},
			"a last nil option keeps the lowest layer": {
				opts: []niceyaml.MergeOption{niceyaml.MergeAs(user), niceyaml.MergeAs(nil)},
				want: defaults,
			},
			"a last option replaces a layer the layers do not hold": {
				opts: []niceyaml.MergeOption{niceyaml.MergeAs(stranger), niceyaml.MergeAs(env)},
				want: env,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				merged, err := niceyaml.NewSourceFromLayers(t.Context(), sources, tc.opts...)
				require.NoError(t, err)

				doc := yamltest.Document(t, merged)
				key := strings.TrimSuffix(tc.want.Name(), ".yaml")

				assert.Equal(t, tc.want.Name(), merged.Name())
				assert.Equal(t, tc.want.FilePath(), doc.FilePath())
				assert.Equal(t, tc.want.FS(), doc.FS())

				directive := schema.ParseDocumentDirective(doc.Preamble())
				require.NotNil(t, directive)
				assert.Equal(t, "./"+key+".json", directive.Schema)

				// The directive resolves beside the file of the layer.
				err = merged.ValidateDocuments(t.Context(), registry)
				require.ErrorContains(t, err, fmt.Sprintf("missing required property %q", key))

				// An error with no location binds in the layer.
				err = merged.Bind(errors.New("quota service: connection refused"))
				require.EqualError(t, err, tc.want.Name()+": quota service: connection refused")
				assert.Same(t, tc.want, yamltest.RequireSourceError(t, err).Source())

				// The layers merge in the order given, whichever layer the
				// Source stands for.
				got, err := merged.Decode[map[string]any](t.Context())
				require.NoError(t, err)
				assert.Equal(t, map[string]any{
					"server": map[string]any{"host": "localhost", "port": uint64(9090)},
					"log":    "debug",
				}, got)
			})
		}
	})

	t.Run("a layer the layers do not hold returns ErrUnknownLayer", func(t *testing.T) {
		t.Parallel()

		base := yamltest.Document(t, defaults)
		scoped := yamltest.At(t, base, paths.Doc().Child("server"))
		again := yamltest.At(t, base, paths.Doc().Child("server"))
		broken := niceyaml.NewSourceFromString("server: [\n", niceyaml.WithName("broken.yaml"))

		tcs := map[string]func(ctx context.Context) (*niceyaml.Source, error){
			"another Source": func(ctx context.Context) (*niceyaml.Source, error) {
				return niceyaml.NewSourceFromLayers(ctx, sources, niceyaml.MergeAs(stranger))
			},
			"the Source of a Node layer": func(ctx context.Context) (*niceyaml.Source, error) {
				return niceyaml.NewSourceFromLayers(ctx, []*niceyaml.Node{base}, niceyaml.MergeAs(defaults))
			},
			"the Node of a Source layer": func(ctx context.Context) (*niceyaml.Source, error) {
				return niceyaml.NewSourceFromLayers(ctx, []*niceyaml.Source{defaults}, niceyaml.MergeAs(base))
			},
			"a Node another call of At returns": func(ctx context.Context) (*niceyaml.Source, error) {
				return niceyaml.NewSourceFromLayers(ctx, []*niceyaml.Node{scoped}, niceyaml.MergeAs(again))
			},
			"no layer": func(ctx context.Context) (*niceyaml.Source, error) {
				return niceyaml.NewSourceFromLayers(ctx, []niceyaml.Layer{}, niceyaml.MergeAs(stranger))
			},
			"nil layers alone": func(ctx context.Context) (*niceyaml.Source, error) {
				return niceyaml.NewSourceFromLayers(ctx, []*niceyaml.Source{nil, nil}, niceyaml.MergeAs(stranger))
			},
			"a layer that holds no value": func(ctx context.Context) (*niceyaml.Source, error) {
				return niceyaml.NewSourceFromLayers(ctx, []*niceyaml.Source{broken}, niceyaml.MergeAs(stranger))
			},
		}

		for name, merge := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				merged, err := merge(t.Context())
				require.ErrorIs(t, err, niceyaml.ErrUnknownLayer)
				require.NotErrorIs(t, err, niceyaml.ErrSyntax)
				assert.False(t, niceyaml.IsInvalid(err))
				assert.Nil(t, merged)
			})
		}

		_, err := niceyaml.NewSourceFromLayers(t.Context(), sources, niceyaml.MergeAs(stranger))
		require.EqualError(t, err, `merge as "stranger.yaml": layer is not among the merged layers`)
	})

	t.Run("each kind of slice takes MergeAs", func(t *testing.T) {
		t.Parallel()

		docs := []*niceyaml.Node{
			yamltest.Document(t, defaults),
			yamltest.Document(t, user),
			yamltest.Document(t, env),
		}

		// A nil element of each slice adds nothing.
		tcs := map[string]func(ctx context.Context) (*niceyaml.Source, error){
			"Sources": func(ctx context.Context) (*niceyaml.Source, error) {
				layers := []*niceyaml.Source{nil, defaults, user, nil, env}

				return niceyaml.NewSourceFromLayers(ctx, layers, niceyaml.MergeAs(user))
			},
			"Nodes": func(ctx context.Context) (*niceyaml.Source, error) {
				layers := []*niceyaml.Node{nil, docs[0], docs[1], nil, docs[2]}

				return niceyaml.NewSourceFromLayers(ctx, layers, niceyaml.MergeAs(docs[1]))
			},
			"Layers": func(ctx context.Context) (*niceyaml.Source, error) {
				layers := []niceyaml.Layer{nil, (*niceyaml.Source)(nil), defaults, docs[1], (*niceyaml.Node)(nil), env}

				return niceyaml.NewSourceFromLayers(ctx, layers, niceyaml.MergeAs(docs[1]))
			},
		}

		for name, merge := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				merged, err := merge(t.Context())
				require.NoError(t, err)

				assert.Equal(t, "user.yaml", merged.Name())
				assert.Equal(t, user.FilePath(), yamltest.Document(t, merged).FilePath())

				err = merged.Bind(errors.New("quota service: connection refused"))
				require.EqualError(t, err, "user.yaml: quota service: connection refused")
				assert.Same(t, docs[1], yamltest.BoundNode(t, yamltest.RequireSourceError(t, err)))

				got, err := merged.Decode[map[string]any](t.Context())
				require.NoError(t, err)
				assert.Equal(t, "debug", got["log"])
			})
		}
	})

	t.Run("the documents of one file take the preamble of the one MergeAs names", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString(
			stringtest.LinesLF(
				"# yaml-language-server: $schema=./one.json",
				"a: 1",
				"---",
				"# yaml-language-server: $schema=./two.json",
				"b: 2",
			),
			niceyaml.WithName("app.yaml"),
		)

		docs := source.Documents()
		require.Len(t, docs, 2)

		tcs := map[string]struct {
			want string
			text string
			opts []niceyaml.MergeOption
		}{
			"the first document": {
				want: "./one.json",
				text: stringtest.LinesLF("# yaml-language-server: $schema=./one.json", "a: 1", "b: 2"),
			},
			"the second document": {
				opts: []niceyaml.MergeOption{niceyaml.MergeAs(docs[1])},
				want: "./two.json",
				// The preamble of the second document holds its header.
				text: stringtest.LinesLF("---", "# yaml-language-server: $schema=./two.json", "a: 1", "b: 2"),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				merged, err := niceyaml.NewSourceFromLayers(t.Context(), docs, tc.opts...)
				require.NoError(t, err)

				directive := schema.ParseDocumentDirective(yamltest.Document(t, merged).Preamble())
				require.NotNil(t, directive)
				assert.Equal(t, tc.want, directive.Schema)
				assert.Equal(t, tc.text, mergedText(t, merged))
			})
		}
	})

	t.Run("a merged layer passes on the layer it stands for", func(t *testing.T) {
		t.Parallel()

		inner, err := niceyaml.NewSourceFromLayers(t.Context(),
			[]*niceyaml.Source{defaults, user}, niceyaml.MergeAs(user))
		require.NoError(t, err)

		tcs := map[string]func(ctx context.Context) (*niceyaml.Source, error){
			"as the lowest layer": func(ctx context.Context) (*niceyaml.Source, error) {
				return niceyaml.NewSourceFromLayers(ctx, []*niceyaml.Source{inner, env})
			},
			"named by MergeAs": func(ctx context.Context) (*niceyaml.Source, error) {
				return niceyaml.NewSourceFromLayers(ctx, []*niceyaml.Source{env, inner}, niceyaml.MergeAs(inner))
			},
		}

		for name, merge := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				merged, err := merge(t.Context())
				require.NoError(t, err)

				assert.Equal(t, "user.yaml", merged.Name())
				assert.Equal(t, user.FilePath(), yamltest.Document(t, merged).FilePath())

				err = merged.Bind(errors.New("quota service: connection refused"))
				require.EqualError(t, err, "user.yaml: quota service: connection refused")
				assert.Same(t, user, yamltest.RequireSourceError(t, err).Source())
			})
		}
	})

	t.Run(
		"with no MergeUnmarshalers the merged Source takes the unmarshalers of the layer it stands for",
		func(t *testing.T) {
			t.Parallel()

			type config struct {
				Name  string     `yaml:"name"`
				Level plainLevel `yaml:"level"`
			}

			levels := niceyaml.WithUnmarshalers(niceyaml.CustomUnmarshaler(decodeLevel))

			// The level lies in the lowest layer, and decodes from its name
			// only through the function.
			plainBase := niceyaml.NewSourceFromString("level: low\n", niceyaml.WithName("base.yaml"))
			levelsBase := niceyaml.NewSourceFromString("level: low\n", niceyaml.WithName("base.yaml"), levels)
			plainUser := niceyaml.NewSourceFromString("name: api\n", niceyaml.WithName("user.yaml"))
			levelsUser := niceyaml.NewSourceFromString("name: api\n", niceyaml.WithName("user.yaml"), levels)

			const rejected = "base.yaml:1:8: $.level: expected integer, got string"

			tcs := map[string]struct {
				err    string
				layers []*niceyaml.Source
				opts   []niceyaml.MergeOption
			}{
				"the layer MergeAs names": {
					layers: []*niceyaml.Source{plainBase, levelsUser},
					opts:   []niceyaml.MergeOption{niceyaml.MergeAs(levelsUser)},
				},
				"the lowest layer with no MergeAs": {
					layers: []*niceyaml.Source{levelsBase, plainUser},
				},
				"no other layer with no MergeAs": {
					layers: []*niceyaml.Source{plainBase, levelsUser},
					err:    rejected,
				},
				"no layer but the one MergeAs names": {
					layers: []*niceyaml.Source{levelsBase, plainUser},
					opts:   []niceyaml.MergeOption{niceyaml.MergeAs(plainUser)},
					err:    rejected,
				},
			}

			for name, tc := range tcs {
				t.Run(name, func(t *testing.T) {
					t.Parallel()

					merged, err := niceyaml.NewSourceFromLayers(t.Context(), tc.layers, tc.opts...)
					require.NoError(t, err)

					got, err := merged.Decode[config](t.Context())

					_, atErr := yamltest.Document(t, merged).DecodeAt[plainLevel](
						t.Context(),
						paths.Doc().Child("level"),
					)

					if tc.err != "" {
						require.EqualError(t, err, tc.err)
						require.EqualError(t, atErr, tc.err)

						return
					}

					require.NoError(t, err)
					require.NoError(t, atErr)
					assert.Equal(t, config{Name: "api", Level: levelLow}, got)
				})
			}
		},
	)
}

// identityLayer returns a Source of input, named name.yaml in dir of a file
// system of its own. Its directive names name.json beside it, a schema
// that requires the key name.
func identityLayer(t *testing.T, dir, name, input string) *niceyaml.Source {
	t.Helper()

	path := dir + "/" + name + ".yaml"
	fsys := fstest.MapFS{
		path:                       {Data: []byte("# yaml-language-server: $schema=./" + name + ".json\n" + input)},
		dir + "/" + name + ".json": {Data: []byte(`{"required": ["` + name + `"]}`)},
	}

	source, err := niceyaml.NewSourceFromFS(fsys, path, niceyaml.WithName(name+".yaml"))
	require.NoError(t, err)

	return source
}

func TestMergeUnmarshalers(t *testing.T) {
	t.Parallel()

	type config struct {
		Name  string     `yaml:"name"`
		Level plainLevel `yaml:"level"`
	}

	levels := niceyaml.CustomUnmarshaler(decodeLevel)

	// Removes the function for plainLevel, so a decode reads the level as
	// an integer.
	plain := niceyaml.CustomUnmarshaler[plainLevel](nil)

	// The level lies in the lowest layer, and decodes from its name only
	// through the function.
	base := niceyaml.NewSourceFromString("level: low\n", niceyaml.WithName("base.yaml"))
	levelsBase := niceyaml.NewSourceFromString("level: low\n",
		niceyaml.WithName("base.yaml"), niceyaml.WithUnmarshalers(levels))
	user := niceyaml.NewSourceFromString("name: api\n", niceyaml.WithName("user.yaml"))
	levelsUser := niceyaml.NewSourceFromString("name: api\n",
		niceyaml.WithName("user.yaml"), niceyaml.WithUnmarshalers(levels))

	// An optional file that does not exist.
	var missing *niceyaml.Source

	levelPath := paths.Doc().Child("level")

	const rejected = "base.yaml:1:8: $.level: expected integer, got string"

	t.Run("every decode of the merged Source applies them", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			err    string
			layers []*niceyaml.Source
			opts   []niceyaml.MergeOption
			want   config
		}{
			"with the optional file": {
				layers: []*niceyaml.Source{base, user},
				opts:   []niceyaml.MergeOption{niceyaml.MergeAs(user), niceyaml.MergeUnmarshalers(levels)},
				want:   config{Name: "api", Level: levelLow},
			},
			"without the optional file": {
				layers: []*niceyaml.Source{base, missing},
				opts:   []niceyaml.MergeOption{niceyaml.MergeAs(missing), niceyaml.MergeUnmarshalers(levels)},
				want:   config{Level: levelLow},
			},
			"with no MergeAs": {
				layers: []*niceyaml.Source{base, user},
				opts:   []niceyaml.MergeOption{niceyaml.MergeUnmarshalers(levels)},
				want:   config{Name: "api", Level: levelLow},
			},
			"in place of those of the layer MergeAs names": {
				layers: []*niceyaml.Source{base, levelsUser},
				opts:   []niceyaml.MergeOption{niceyaml.MergeAs(levelsUser), niceyaml.MergeUnmarshalers(plain)},
				err:    rejected,
			},
			"in place of those of the lowest layer": {
				layers: []*niceyaml.Source{levelsBase, user},
				opts:   []niceyaml.MergeOption{niceyaml.MergeUnmarshalers(plain)},
				err:    rejected,
			},
			"none from any layer when the option holds none": {
				layers: []*niceyaml.Source{levelsBase, user},
				opts:   []niceyaml.MergeOption{niceyaml.MergeUnmarshalers()},
				err:    rejected,
			},
			"a later option adds to an earlier one": {
				layers: []*niceyaml.Source{base, user},
				opts: []niceyaml.MergeOption{
					niceyaml.MergeUnmarshalers(levels),
					niceyaml.MergeUnmarshalers(niceyaml.JSONUnmarshalers(true)),
				},
				want: config{Name: "api", Level: levelLow},
			},
			"a later function for a type replaces an earlier one": {
				layers: []*niceyaml.Source{base, user},
				opts:   []niceyaml.MergeOption{niceyaml.MergeUnmarshalers(levels), niceyaml.MergeUnmarshalers(plain)},
				err:    rejected,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				merged, err := niceyaml.NewSourceFromLayers(t.Context(), tc.layers, tc.opts...)
				require.NoError(t, err)

				got, err := merged.Decode[config](t.Context())

				// The decode inside a validator gets no option of the call.
				var checked plainLevel

				checkErr := merged.ValidateDocuments(t.Context(), niceyaml.ValidatorFunc(
					func(ctx context.Context, n *niceyaml.Node) error {
						var err error

						checked, err = n.DecodeAt[plainLevel](ctx, levelPath)

						return err
					},
				))

				if tc.err != "" {
					require.EqualError(t, err, tc.err)
					require.EqualError(t, checkErr, tc.err)

					return
				}

				require.NoError(t, err)
				require.NoError(t, checkErr)
				assert.Equal(t, tc.want, got)
				assert.Equal(t, levelLow, checked)
			})
		}
	})

	t.Run("the merged Source gives them to a merge that stands for it", func(t *testing.T) {
		t.Parallel()

		tcs := map[string][]*niceyaml.Source{
			"a merge of layers":   {base, user},
			"a merge of no layer": {missing},
		}

		for name, inner := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				lower, err := niceyaml.NewSourceFromLayers(t.Context(), inner, niceyaml.MergeUnmarshalers(levels))
				require.NoError(t, err)

				merged, err := niceyaml.NewSourceFromLayers(t.Context(), []*niceyaml.Source{lower, base, user})
				require.NoError(t, err)

				got, err := merged.Decode[config](t.Context())
				require.NoError(t, err)
				assert.Equal(t, config{Name: "api", Level: levelLow}, got)
			})
		}
	})

	t.Run("the origin of a value applies the unmarshalers of its layer", func(t *testing.T) {
		t.Parallel()

		merged, err := niceyaml.NewSourceFromLayers(t.Context(),
			[]*niceyaml.Source{base, user}, niceyaml.MergeUnmarshalers(levels))
		require.NoError(t, err)

		level, err := yamltest.Document(t, merged).At(levelPath)
		require.NoError(t, err)

		got, err := level.Decode[plainLevel](t.Context())
		require.NoError(t, err)
		assert.Equal(t, levelLow, got)

		origin, err := level.Origin()
		require.NoError(t, err)

		_, err = origin.Decode[plainLevel](t.Context())
		require.EqualError(t, err, rejected)
	})

	t.Run("the option keeps its unmarshalers when the caller changes the slice", func(t *testing.T) {
		t.Parallel()

		us := []niceyaml.Unmarshaler{levels}
		opt := niceyaml.MergeUnmarshalers(us...)
		us[0] = plain

		merged, err := niceyaml.NewSourceFromLayers(t.Context(), []*niceyaml.Source{base, user}, opt)
		require.NoError(t, err)

		got, err := merged.Decode[config](t.Context())
		require.NoError(t, err)
		assert.Equal(t, config{Name: "api", Level: levelLow}, got)
	})
}

func TestNewSourceFromLayers_SelfValidate(t *testing.T) {
	t.Parallel()

	const portMessage = "port must be at least 1"

	// Each case decodes its layers into one value and validates the
	// value through them. The files are base.yaml and prod.yaml, with
	// mid.yaml between them when the case has three.
	tcs := map[string]struct {
		// Changes the value once the layers have set it, as the
		// environment of a program does.
		change func(cfg *layerConfig)
		// The error of the layers.
		err string
		// The documents of the layers, lowest first.
		layers []string
		// The paths the value itself reports, as a parent that checks the
		// values below it does.
		odd []paths.Path
	}{
		"a field binds in the layer that sets it": {
			layers: []string{layerBadPort, layerProdHost},
			err:    "base.yaml:3:9: $.server.port: " + portMessage,
		},
		"the top layer keeps a field it sets": {
			layers: []string{layerPort80, stringtest.LinesLF("server:", "  port: 0")},
			err:    "prod.yaml:2:9: $.server.port: " + portMessage,
		},
		"an element binds in the layer that holds the sequence": {
			layers: []string{
				stringtest.LinesLF("servers:", "  - {host: a, port: 80}"),
				stringtest.LinesLF("servers:", "  - {host: x}"),
			},
			err: "prod.yaml:2:3: $.servers[0].port: " + portMessage,
		},
		"an element of an array binds in the layer that holds the sequence": {
			layers: []string{
				stringtest.LinesLF("pair:", "  - {host: a, port: 80}"),
				stringtest.LinesLF("pair:", "  - {host: x}"),
			},
			err: "prod.yaml:2:3: $.pair[0].port: " + portMessage,
		},
		"a field of a map entry binds in the layer that sets it": {
			layers: []string{
				stringtest.LinesLF("backends:", "  a: {host: a, port: 0}"),
				stringtest.LinesLF("backends:", "  a: {host: x}", "  b: {host: b, port: 1}"),
			},
			err: "base.yaml:2:22: $.backends.a.port: " + portMessage,
		},
		"a key of an untyped mapping binds in the layer that sets it": {
			layers: []string{"extra: {k: v}\n", "extra: {z: y}\n"},
			odd:    []paths.Path{paths.Current().Child("extra", "k")},
			err:    "base.yaml:1:12: $.extra.k: odd value",
		},
		"a key of a value that decodes itself binds in the layer that sets it": {
			layers: []string{"switch: {on: true, mode: fast}\n", "switch: {on: false}\n"},
			odd:    []paths.Path{paths.Current().Child("switch", "mode")},
			err:    "base.yaml:1:26: $.switch.mode: odd value",
		},
		"an unwritten null keeps the field of the layer below": {
			layers: []string{layerBadPort, stringtest.LinesLF("server:", "  host: x", "  port:")},
			err:    "base.yaml:3:9: $.server.port: " + portMessage,
		},
		"a key above comments alone keeps the mapping of the layer below": {
			layers: []string{layerBadPort, stringtest.LinesLF("server:", "  # port: 80")},
			err:    "base.yaml:3:9: $.server.port: " + portMessage,
		},
		"a field that a written null removed binds at the null": {
			layers: []string{layerPort80, stringtest.LinesLF("server:", "  host: x", "  port: null")},
			err:    "prod.yaml:3:9: $.server.port: " + portMessage,
		},
		"a pointer field that a written null removed binds at the null": {
			layers: []string{
				stringtest.LinesLF("limits:", "  timeout: 5"),
				stringtest.LinesLF("limits:", "  timeout: ~"),
			},
			err: "prod.yaml:2:12: $.limits.timeout: timeout is required",
		},
		"a removed field binds in the highest layer that writes a null": {
			layers: []string{
				stringtest.LinesLF("limits:", "  timeout: 5"),
				stringtest.LinesLF("limits:", "  timeout: null"),
				stringtest.LinesLF("limits:", "  timeout: ~"),
			},
			err: "prod.yaml:2:12: $.limits.timeout: timeout is required",
		},
		"a removed field binds below an unwritten null": {
			layers: []string{
				stringtest.LinesLF("limits:", "  timeout: 5"),
				stringtest.LinesLF("limits:", "  timeout: null"),
				stringtest.LinesLF("limits:", "  timeout:"),
			},
			err: "mid.yaml:2:12: $.limits.timeout: timeout is required",
		},
		"an empty mapping keeps the value of the layer below": {
			layers: []string{layerBadPort, "server: {}\n"},
			err:    "base.yaml:3:9: $.server.port: " + portMessage,
		},
		"a null that no layer sets a value under binds in the highest layer": {
			layers: []string{
				stringtest.LinesLF("limits:", "  timeout: null"),
				stringtest.LinesLF("limits:", "  timeout: ~"),
			},
			err: "prod.yaml:2:12: $.limits.timeout: timeout is required",
		},
		"the middle of three layers sets the value": {
			layers: []string{
				layerPort80,
				stringtest.LinesLF("server:", "  port: 0"),
				layerProdHost,
			},
			err: "mid.yaml:2:9: $.server.port: " + portMessage,
		},
		"the search passes over an unwritten null in a higher layer": {
			layers: []string{layerBadPort, "name: mid\n", stringtest.LinesLF("server:", "  port:")},
			err:    "base.yaml:3:9: $.server.port: " + portMessage,
		},
		"an inline struct binds in the layer that sets its field": {
			layers: []string{stringtest.LinesLF("name: base", "region: nowhere"), "name: prod\n"},
			err:    "base.yaml:2:9: $.region: unknown region",
		},
		"a pointer to a struct binds in the layer that sets its field": {
			layers: []string{
				stringtest.LinesLF("backup:", "  host: example.com", "  port: 0"),
				stringtest.LinesLF("backup:", "  host: x"),
			},
			err: "base.yaml:3:9: $.backup.port: " + portMessage,
		},
		"a struct two fields down binds in the layer that sets its field": {
			layers: []string{
				stringtest.LinesLF(
					"nested:",
					"  inner:",
					"    host: example.com",
					"    port: 0",
				),
				"name: prod\n",
			},
			err: "base.yaml:4:11: $.nested.inner.port: " + portMessage,
		},
		"a sequence that only a lower layer holds binds there": {
			layers: []string{stringtest.LinesLF("name: base", "servers:", "  - {host: a, port: 0}"), "name: prod\n"},
			err:    "base.yaml:3:21: $.servers[0].port: " + portMessage,
		},
		"a mapping that only a lower layer holds binds there": {
			layers: []string{stringtest.LinesLF("name: base", "backends:", "  a: {host: a, port: 0}"), "name: prod\n"},
			err:    "base.yaml:3:22: $.backends.a.port: " + portMessage,
		},
		"each problem of a summary binds in its own layer": {
			layers: []string{
				stringtest.LinesLF("server: {host: example.com, port: 1}", "backends:", "  a: {host: a, port: 80}"),
				stringtest.LinesLF("backends:", "  a: {host: x, port: 2}"),
			},
			odd: []paths.Path{
				paths.Current().Child("server", "host"),
				paths.Current().Child("backends", "a", "port"),
			},
			err: stringtest.JoinLF(
				"base.yaml: config checks",
				"base.yaml:1:16: $.server.host: odd value",
				"prod.yaml:2:22: $.backends.a.port: odd value",
			),
		},
		"a value no layer sets binds at the deepest mapping that lacks it": {
			layers: []string{stringtest.LinesLF("name: base", "server:", "  host: example.com"), "name: prod\n"},
			err:    "base.yaml:2:1: $.server.port: " + portMessage,
		},
		"the top layer wins among mappings of one depth": {
			layers: []string{"name: base\n", "name: prod\n"},
			odd:    []paths.Path{paths.Current().Child("server", "host")},
			err:    "prod.yaml:1:1: $.server.host: odd value",
		},
		"a key no field reads binds in the layer that sets it": {
			layers: []string{"unknown: {k: v}\n", "name: prod\n"},
			odd:    []paths.Path{paths.Current().Child("unknown", "k")},
			err:    "base.yaml:1:14: $.unknown.k: odd value",
		},
		// The alias brings in a host and no port, so the port of the base
		// file stays, and only the backup lacks one.
		"a value an alias brings in merges as one written in its place": {
			layers: []string{
				layerPort80,
				stringtest.LinesLF("backup: &b {host: x}", "server: *b"),
			},
			err: "prod.yaml:1:1: $.backup.port: " + portMessage,
		},
		"a value under an alias binds at the content of the anchor": {
			layers: []string{
				layerPort80,
				stringtest.LinesLF("extra: &s {host: x, port: 0}", "server: *s"),
			},
			err: "prod.yaml:1:27: $.server.port: " + portMessage,
		},
		"a key spelled unlike its Go value binds at its entry": {
			layers: []string{stringtest.LinesLF("name: base", "rates:", "  1.50: {limit: 0}"), "name: prod\n"},
			err:    "base.yaml:3:17: $.rates.'1.50'.limit: limit must be at least 1",
		},
		// The limit that NewSourceFromLayers documents.
		"a value the caller set binds at what a file holds": {
			layers: []string{
				stringtest.LinesLF("server:", "  host: example.com", "  port: 8080"),
				stringtest.LinesLF("server:", "  host: x"),
			},
			change: func(cfg *layerConfig) {
				cfg.Server.Port = 0
			},
			err: "base.yaml:3:9: $.server.port: " + portMessage,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var cfg layerConfig

			layers := layerFiles(t, tc.layers...)

			err := layers.DecodeInto(t.Context(), &cfg, niceyaml.WithSelfValidation(false))
			require.NoError(t, err)

			cfg.odd = tc.odd

			if tc.change != nil {
				tc.change(&cfg)
			}

			err = layers.SelfValidate(t.Context(), &cfg)
			require.EqualError(t, err, tc.err)
		})
	}
}

func TestNewSourceFromLayers_DecodeInto(t *testing.T) {
	t.Parallel()

	const want = "base.yaml:3:9: $.server.port: port must be at least 1"

	t.Run("a layer that fails alone passes once a layer above completes it", func(t *testing.T) {
		t.Parallel()

		// The base names a host and no port, which its own decode rejects.
		nodes := layerNodes(t,
			stringtest.LinesLF("server:", "  host: example.com"),
			stringtest.LinesLF("server:", "  port: 80"),
		)

		var alone layerConfig

		err := nodes[0].DecodeInto(t.Context(), &alone)
		require.EqualError(t, err, "base.yaml:1:1: $.server.port: port must be at least 1")

		cfg, err := mergedLayers(t, nodes).Decode[layerConfig](t.Context())
		require.NoError(t, err)
		assert.Equal(t, layerServer{Host: "example.com", Port: 80}, cfg.Server)
	})

	t.Run("the nearest layer below binds", func(t *testing.T) {
		t.Parallel()

		// Both layers below the top one set the port, and the nearer one
		// holds the value the merge kept.
		nodes := layerNodes(t, stringtest.LinesLF("server:", "  port: 0"), layerBadPort, layerProdHost)

		_, err := mergedLayers(t, nodes).Decode[layerConfig](t.Context())
		require.EqualError(t, err, "mid.yaml:3:9: $.server.port: port must be at least 1")

		_, err = yamltest.Layers(t, nodes[1], nodes[0], nodes[2]).Decode[layerConfig](t.Context())
		require.EqualError(t, err, "base.yaml:2:9: $.server.port: port must be at least 1")
	})

	t.Run("self-validation off leaves the value to the caller", func(t *testing.T) {
		t.Parallel()

		layers := layerFiles(t, layerBadPort, layerProdHost)

		var cfg layerConfig

		err := layers.DecodeInto(t.Context(), &cfg, niceyaml.WithSelfValidation(false))
		require.NoError(t, err)

		err = layers.SelfValidate(t.Context(), &cfg, niceyaml.WithSelfValidation(false))
		require.EqualError(t, err, want)

		cfg.Server.Port = 8080

		err = layers.SelfValidate(t.Context(), &cfg)
		require.NoError(t, err)
	})

	t.Run("an unwritten null keeps the value below it in a pointer field too", func(t *testing.T) {
		t.Parallel()

		nodes := layerNodes(t,
			stringtest.LinesLF("limits:", "  timeout: 5"),
			stringtest.LinesLF("limits:", "  timeout:"),
		)

		cfg, err := mergedLayers(t, nodes).Decode[layerConfig](t.Context())
		require.NoError(t, err)
		require.NotNil(t, cfg.Limits)
		require.NotNil(t, cfg.Limits.Timeout)
		assert.Equal(t, 5, *cfg.Limits.Timeout)
	})

	t.Run("a written null removes the value below it", func(t *testing.T) {
		t.Parallel()

		layers := layerFiles(t,
			stringtest.LinesLF("limits:", "  timeout: 5"),
			stringtest.LinesLF("limits:", "  timeout: null"),
		)

		cfg, err := layers.Decode[layerConfig](t.Context(), niceyaml.WithSelfValidation(false))
		require.NoError(t, err)
		require.NotNil(t, cfg.Limits)
		assert.Nil(t, cfg.Limits.Timeout)

		// The limits need a timeout, and the error for the one prod.yaml
		// removed binds at the null that removed it.
		_, err = layers.Decode[layerConfig](t.Context())
		require.EqualError(t, err, "prod.yaml:2:12: $.limits.timeout: timeout is required")
	})

	t.Run("a default of the value survives the removal of its key", func(t *testing.T) {
		t.Parallel()

		layers := layerFiles(t,
			stringtest.LinesLF("name: shop", "backup: {host: b, port: 1}"),
			stringtest.LinesLF("name: ~", "backup: null"),
		)

		// The merged document holds neither key, so the decode leaves both
		// fields as the program set them.
		cfg := layerConfig{Name: "default", Backup: &layerServer{Host: "d", Port: 9}}

		err := layers.DecodeInto(t.Context(), &cfg)
		require.NoError(t, err)
		assert.Equal(t, "default", cfg.Name)
		assert.Equal(t, &layerServer{Host: "d", Port: 9}, cfg.Backup)
	})

	t.Run("a layer encoded from a struct removes the value under a nil pointer field", func(t *testing.T) {
		t.Parallel()

		type overrides struct {
			Backup *layerServer `yaml:"backup"`
			Name   string       `yaml:"name"`
		}

		// The encoder writes the nil pointer as a null.
		data, err := encoder.Marshal(t.Context(), overrides{Name: "env"})
		require.NoError(t, err)
		assert.Equal(t, stringtest.LinesLF("backup: null", "name: env"), string(data))

		base := niceyaml.NewSourceFromString(
			stringtest.LinesLF("name: shop", "backup: {host: b, port: 1}"),
			niceyaml.WithName("base.yaml"),
		)
		env := niceyaml.NewSourceFromBytes(data, niceyaml.WithName("environment"))

		layers := yamltest.Layers(t, base, env)

		assert.Equal(t, "name: env\n", mergedText(t, layers))

		cfg, err := layers.Decode[layerConfig](t.Context())
		require.NoError(t, err)
		assert.Nil(t, cfg.Backup)
	})

	t.Run("one decode reports the values every layer gets wrong", func(t *testing.T) {
		t.Parallel()

		nodes := layerNodes(t, stringtest.LinesLF("server:", "  port: many"), "name: [prod]\n")

		var cfg layerConfig

		err := mergedLayers(t, nodes).DecodeInto(t.Context(), &cfg)
		require.EqualError(t, err, stringtest.JoinLF(
			"base.yaml: 2 problems",
			"base.yaml:2:9: $.server.port: expected integer, got string",
			"prod.yaml:1:1: $.name: expected string, got sequence",
		))
		require.ErrorIs(t, err, niceyaml.ErrDecode)
	})

	t.Run("a rejection with no token reports beside the other problems", func(t *testing.T) {
		t.Parallel()

		// The decoder returns the error of a duration, and of a value that
		// decodes itself from text, with no token of the document. It
		// rejects such a value first in each case.
		type config struct {
			Server problemTimed `yaml:"server"`
			Tier   tier         `yaml:"tier"`
		}

		tcs := map[string]struct {
			want   string
			layers []string
		}{
			"the other problems lie in the layer above": {
				layers: []string{
					stringtest.LinesLF("server:", "  timeout: soon"),
					stringtest.LinesLF(
						"server:",
						"  port: 70000",
						"tier: mid",
						"zone: prod",
					),
				},
				want: stringtest.JoinLF(
					"base.yaml: 4 problems",
					`base.yaml:2:12: $.server.timeout: time: invalid duration "soon"`,
					"prod.yaml:2:9: $.server.port: expected integer from 0 to 65535, got 70000",
					`prod.yaml:3:7: $.tier: unknown tier "mid"`,
					`prod.yaml:4:1: $.zone~: unknown field "zone"`,
				),
			},
			"the other problems lie in the layer below": {
				layers: []string{
					stringtest.LinesLF("server:", "  port: 70000", "zone: base"),
					stringtest.LinesLF("server:", "  tier: mid"),
				},
				want: stringtest.JoinLF(
					"base.yaml: 3 problems",
					"base.yaml:2:9: $.server.port: expected integer from 0 to 65535, got 70000",
					`base.yaml:3:1: $.zone~: unknown field "zone"`,
					`prod.yaml:2:9: $.server.tier: unknown tier "mid"`,
				),
			},
			// The merged text is block style, so it holds no value at the
			// position either file has for it, and each value reports once.
			"flow style": {
				layers: []string{
					"{server: {timeout: soon}}\n",
					stringtest.LinesLF("# prod", "", "{server: {port: 70000}, tier: mid, zone: prod}"),
				},
				want: stringtest.JoinLF(
					"base.yaml: 4 problems",
					`base.yaml:1:20: $.server.timeout: time: invalid duration "soon"`,
					"prod.yaml:3:17: $.server.port: expected integer from 0 to 65535, got 70000",
					`prod.yaml:3:31: $.tier: unknown tier "mid"`,
					`prod.yaml:3:36: $.zone~: unknown field "zone"`,
				),
			},
			"a layer above replaces the value": {
				layers: []string{
					stringtest.LinesLF("server:", "  timeout: soon", "  port: 1"),
					stringtest.LinesLF("server:", "  timeout: later", "  port: 70000"),
				},
				want: stringtest.JoinLF(
					"base.yaml: 2 problems",
					`prod.yaml:2:12: $.server.timeout: time: invalid duration "later"`,
					"prod.yaml:3:9: $.server.port: expected integer from 0 to 65535, got 70000",
				),
			},
			"three layers": {
				layers: []string{
					stringtest.LinesLF("server:", "  timeout: soon"),
					stringtest.LinesLF("server:", "  tier: mid"),
					stringtest.LinesLF("server:", "  port: 70000"),
				},
				want: stringtest.JoinLF(
					"base.yaml: 3 problems",
					`base.yaml:2:12: $.server.timeout: time: invalid duration "soon"`,
					`mid.yaml:2:9: $.server.tier: unknown tier "mid"`,
					"prod.yaml:2:9: $.server.port: expected integer from 0 to 65535, got 70000",
				),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				nodes := layerNodes(t, tc.layers...)

				_, err := mergedLayers(t, nodes).Decode[config](
					t.Context(),
					niceyaml.WithUnknownFields(niceyaml.UnknownFieldsReject),
				)
				require.EqualError(t, err, tc.want)
				require.ErrorIs(t, err, niceyaml.ErrDecode)

				// The merged text has the name of the base file, and no
				// problem binds in it.
				bound := yamltest.RequireSourceError(t, err)
				assert.Same(t, nodes[0], yamltest.BoundNode(t, bound))

				for _, problem := range yamltest.ProblemsOf(bound) {
					assert.Contains(t, nodes, yamltest.BoundNode(t, problem.Bound))
				}
			})
		}
	})

	t.Run("a key no field reads reports in the layer that holds it", func(t *testing.T) {
		t.Parallel()

		nodes := layerNodes(t,
			stringtest.LinesLF("server:", "  prot: 80"),
			stringtest.LinesLF(
				"server:",
				"  host: x",
				"  port: 1",
				"zone: prod",
			),
		)

		_, err := mergedLayers(t, nodes).Decode[layerConfig](
			t.Context(),
			niceyaml.WithUnknownFields(niceyaml.UnknownFieldsReject),
		)
		require.EqualError(t, err, stringtest.JoinLF(
			"base.yaml: 2 unknown fields",
			`base.yaml:2:3: $.server.prot~: unknown field "prot"`,
			`prod.yaml:4:1: $.zone~: unknown field "zone"`,
		))
	})

	t.Run("a strict decode reads no key that a layer removed", func(t *testing.T) {
		t.Parallel()

		base := stringtest.LinesLF(
			"server:",
			"  host: example.com",
			"  port: 80",
			"  prot: 80",
			"zone: base",
		)

		strict := niceyaml.WithUnknownFields(niceyaml.UnknownFieldsReject)

		_, err := layerFiles(t, base, "name: prod\n").Decode[layerConfig](t.Context(), strict)
		require.EqualError(t, err, stringtest.JoinLF(
			"base.yaml: 2 unknown fields",
			`base.yaml:4:3: $.server.prot~: unknown field "prot"`,
			`base.yaml:5:1: $.zone~: unknown field "zone"`,
		))

		// The merged document holds neither key once prod.yaml removes
		// both.
		layers := layerFiles(t, base, stringtest.LinesLF("server:", "  prot: ~", "zone: null"))

		cfg, err := layers.Decode[layerConfig](t.Context(), strict)
		require.NoError(t, err)
		assert.Equal(t, layerServer{Host: "example.com", Port: 80}, cfg.Server)
		assert.Equal(t,
			stringtest.LinesLF("server:", "  host: example.com", "  port: 80"),
			mergedText(t, layers),
		)
	})

	t.Run("a value that holds defaults follows the rule of a decode", func(t *testing.T) {
		t.Parallel()

		nodes := layerNodes(t,
			stringtest.LinesLF("backends:", "  a: {host: a, port: 1}"),
			stringtest.LinesLF("backends:", "  b: {host: b, port: 2}"),
		)

		// A field the layers leave out keeps its default. The mapping of
		// the merged document replaces the map the value holds, so the
		// default entry is gone, where the entry of the base file stays.
		cfg := layerConfig{
			Name:     "default",
			Backends: map[string]layerServer{"d": {Host: "d", Port: 9}},
			Servers:  []layerServer{{Host: "s", Port: 9}},
		}

		err := mergedLayers(t, nodes).DecodeInto(t.Context(), &cfg)
		require.NoError(t, err)
		assert.Equal(t, "default", cfg.Name)
		assert.Equal(t, []layerServer{{Host: "s", Port: 9}}, cfg.Servers)
		assert.Equal(t, map[string]layerServer{
			"a": {Host: "a", Port: 1},
			"b": {Host: "b", Port: 2},
		}, cfg.Backends)
	})

	t.Run("scoped Nodes bind in their files with the paths of those files", func(t *testing.T) {
		t.Parallel()

		base := yamltest.FirstDocument(t, layerDefaults, niceyaml.WithName("base.yaml"))
		prod := yamltest.FirstDocument(t, layerProdHost, niceyaml.WithName("prod.yaml"))

		baseServer := yamltest.At(t, base, paths.Current().Child("defaults", "server"))
		prodServer := yamltest.At(t, prod, paths.Current().Child("server"))

		layers := yamltest.Layers(t, baseServer, prodServer)

		server, err := layers.Decode[layerServer](t.Context(), niceyaml.WithSelfValidation(false))
		require.NoError(t, err)
		assert.Equal(t, layerServer{Host: "prod.example.com"}, server)

		// The merged document holds the port at $.port, and the base
		// file holds it under its own path.
		_, err = layers.Decode[layerServer](t.Context())
		require.EqualError(t, err, "base.yaml:4:11: $.defaults.server.port: port must be at least 1")

		bound := yamltest.RequireSourceError(t, err)
		assert.Same(t, baseServer, yamltest.BoundNode(t, bound))
		assert.Same(t, base, yamltest.BoundDocument(t, bound))
		assert.Equal(t, "$.defaults.server.port", yamltest.PathOf(t, bound).String())
		assert.Equal(t, "$.defaults.server.port", yamltest.PathOf(t, yamltest.RequireError(t, err)).String())

		// A path reads from the merged value whether it starts at `$` or
		// at `@`, and each error reports the path of its own file.
		tcs := map[string]struct {
			err  error
			want string
		}{
			"a path from the root": {
				err:  niceyaml.NewError("unknown host", niceyaml.AtPath(paths.Doc().Child("host"))),
				want: "prod.yaml:2:9: $.server.host: unknown host",
			},
			"a path from the value": {
				err:  yamltest.ErrorAt("unknown host", "host"),
				want: "prod.yaml:2:9: $.server.host: unknown host",
			},
			"a key": {
				err:  niceyaml.NewError("bad key", niceyaml.AtPath(paths.Doc().Child("port").Key())),
				want: "base.yaml:4:5: $.defaults.server.port~: bad key",
			},
			"the value itself": {
				err:  niceyaml.NewError("bad server", niceyaml.AtPath(paths.Doc())),
				want: "prod.yaml:1:1: $.server: bad server",
			},
			"a key no layer holds": {
				err:  niceyaml.NewError("tls is required", niceyaml.AtPath(paths.Doc().Child("tls"))),
				want: "prod.yaml:1:1: $.server.tls: tls is required",
			},
			"a wrapped error": {
				err:  fmt.Errorf("check: %w", yamltest.ErrorAt("unknown host", "host")),
				want: "prod.yaml:2:9: $.server.host: check: unknown host",
			},
			"a rebased error": {
				err:  niceyaml.Rebase(yamltest.ErrorAt("bad port"), paths.Current().Child("port")),
				want: "base.yaml:4:11: $.defaults.server.port: bad port",
			},
			"an error with no location": {
				err:  errors.New("quota service: connection refused"),
				want: "base.yaml: quota service: connection refused",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				require.EqualError(t, layers.Bind(tc.err), tc.want)
			})
		}
	})

	t.Run("a validator runs once, on the merged document", func(t *testing.T) {
		t.Parallel()

		nodes := layerNodes(t, layerBadPort, stringtest.LinesLF("server:", "  port: 80"))

		var seen []*niceyaml.Node

		record := niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
			seen = append(seen, n)

			return nil
		})

		_, err := mergedLayers(t, nodes).Decode[layerConfig](t.Context(), niceyaml.WithValidator(record))
		require.NoError(t, err)
		require.Len(t, seen, 1)
		assert.NotContains(t, nodes, seen[0])
		assert.Equal(t,
			stringtest.JoinLF("server:", "  host: example.com", "  port: 80"),
			seen[0].View().Held().Content(),
		)
	})

	t.Run("a context that ended stops the decode", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		layers := layerFiles(t, layerBadPort, layerProdHost)

		_, err := layers.Decode[layerConfig](ctx)
		require.ErrorIs(t, err, context.Canceled)

		// The layers merged all the same, so a later call decodes them.
		_, err = layers.Decode[layerConfig](t.Context())
		require.EqualError(t, err, want)
	})

	t.Run("a target that is no pointer returns an error", func(t *testing.T) {
		t.Parallel()

		layers := layerFiles(t, layerBadPort, layerProdHost)

		err := layers.DecodeInto(t.Context(), layerConfig{})
		require.ErrorIs(t, err, niceyaml.ErrDecodeTarget)

		err = yamltest.Layers(t).DecodeInto(t.Context(), nil)
		require.ErrorIs(t, err, niceyaml.ErrDecodeTarget)
	})
}

func TestNewSourceFromLayers_Validate(t *testing.T) {
	t.Parallel()

	baseInput := yamltest.SchemaDirective("app.json", stringtest.LinesLF(
		"name: shop",
		"server:",
		"  host: example.com",
		"  port: 0",
	))

	requires := schema.MustCompile([]byte(`{
		"type": "object",
		"required": ["name", "server"],
		"properties": {
			"server": {
				"type": "object",
				"required": ["host", "port", "tls"],
				"properties": {"port": {"type": "integer", "minimum": 1}}
			}
		}
	}`))

	t.Run("a schema checks what the layers hold together", func(t *testing.T) {
		t.Parallel()

		nodes := layerNodes(t, baseInput, layerProdHost)

		// The name is required, and prod.yaml leaves it to base.yaml.
		err := nodes[1].Validate(t.Context(), requires)
		require.ErrorContains(t, err, `prod.yaml:1:1: $.name: missing required property "name"`)

		// Each violation binds in the layer that holds its value, and the
		// key no layer holds in the highest layer that holds its mapping.
		err = mergedLayers(t, nodes).ValidateDocuments(t.Context(), requires)
		require.EqualError(t, err, stringtest.JoinLF(
			"base.yaml: 2 schema violations",
			"base.yaml:5:9: $.server.port: 0 is less than 1",
			`prod.yaml:1:1: $.server.tls: missing required property "tls"`,
		))
		assert.True(t, niceyaml.IsInvalid(err))

		_, err = mergedLayers(t, nodes).Decode[layerConfig](t.Context(), niceyaml.WithValidator(requires))
		require.ErrorContains(t, err, "base.yaml:5:9: $.server.port: 0 is less than 1")
	})

	t.Run("a key the schema requires reports at the null that removed it", func(t *testing.T) {
		t.Parallel()

		layers := layerFiles(t, baseInput, stringtest.LinesLF("name: ~", "server:", "  port: null", "  tls: on"))

		// The merged document lacks the name and the port, which base.yaml
		// sets and prod.yaml removes.
		err := layers.ValidateDocuments(t.Context(), requires)
		require.EqualError(t, err, stringtest.JoinLF(
			"base.yaml: 2 schema violations",
			`prod.yaml:1:7: $.name: missing required property "name"`,
			`prod.yaml:3:9: $.server.port: missing required property "port"`,
		))
		assert.True(t, niceyaml.IsInvalid(err))
	})

	t.Run("the merged document takes the name and the preamble of the lowest layer", func(t *testing.T) {
		t.Parallel()

		fsys := fstest.MapFS{}
		base := yamltest.FirstDocument(t, baseInput,
			niceyaml.WithName("base"), niceyaml.WithFilePath("conf/base.yaml"), niceyaml.WithFS(fsys))
		prod := yamltest.FirstDocument(t, yamltest.SchemaDirective("other.json", layerProdHost),
			niceyaml.WithFilePath("conf/prod.yaml"))

		var seen *niceyaml.Node

		err := yamltest.Layers(t, base, prod).ValidateDocuments(t.Context(), niceyaml.ValidatorFunc(
			func(_ context.Context, n *niceyaml.Node) error {
				seen = n

				return nil
			},
		))
		require.NoError(t, err)
		require.NotNil(t, seen)

		assert.Equal(t, "base", seen.Source().Name())
		assert.Equal(t, "conf/base.yaml", seen.FilePath())
		assert.Equal(t, fsys, seen.FS())
		assert.True(t, seen.Path().IsRoot())
		assert.Equal(t, 0, seen.DocumentIndex())

		directive := schema.ParseDocumentDirective(seen.Preamble())
		require.NotNil(t, directive)
		assert.Equal(t, "app.json", directive.Schema)

		assert.Equal(t,
			stringtest.JoinLF(
				"# yaml-language-server: $schema=app.json",
				"name: shop",
				"server:",
				"  host: prod.example.com",
				"  port: 0",
			),
			seen.Source().View().Held().Content())
	})

	t.Run("the heading reports the layer MergeAs names", func(t *testing.T) {
		t.Parallel()

		nodes := layerNodes(t, baseInput, layerProdHost)

		merged, err := niceyaml.NewSourceFromLayers(t.Context(), nodes, niceyaml.MergeAs(nodes[1]))
		require.NoError(t, err)

		// Each violation still binds in the file that holds its value.
		err = merged.ValidateDocuments(t.Context(), requires)
		require.EqualError(t, err, stringtest.JoinLF(
			"prod.yaml: 2 schema violations",
			"base.yaml:5:9: $.server.port: 0 is less than 1",
			`prod.yaml:1:1: $.server.tls: missing required property "tls"`,
		))
		assert.Same(t, nodes[1].Source(), yamltest.RequireSourceError(t, err).Source())
	})

	t.Run("an error with a position in the merged text binds at the value there", func(t *testing.T) {
		t.Parallel()

		nodes := layerNodes(t, baseInput, layerProdHost)

		// The validator reads the positions of the merged text, which is
		// no file, and each binds in the file that holds the value.
		byPosition := niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
			port, err := n.Ranges(paths.Doc().Child("server", "port"))
			require.NoError(t, err)

			host, err := n.Ranges(paths.Doc().Child("server", "host").Key())
			require.NoError(t, err)

			return errors.Join(
				niceyaml.NewError("at a position", niceyaml.AtPosition(port[0].Start)),
				niceyaml.NewError("at a range", niceyaml.AtRange(port[0])),
				niceyaml.NewError("at a key", niceyaml.AtPosition(host[0].Start)),
				niceyaml.NewError("beside a path",
					niceyaml.AtPath(paths.Doc().Child("server")), niceyaml.AtPosition(port[0].Start)),
				niceyaml.NewError("in the preamble",
					niceyaml.AtPath(paths.Doc().Child("name")), niceyaml.AtPosition(position.New(0, 2))),
				niceyaml.NewError("in the preamble alone", niceyaml.AtPosition(position.New(0, 2))),
				n.Bind(yamltest.ErrorAt("through the Node", "server", "host")),
			)
		})

		err := mergedLayers(t, nodes).ValidateDocuments(t.Context(), byPosition)
		require.EqualError(t, err, stringtest.JoinLF(
			"base.yaml:2:7: $.name: in the preamble",
			"base.yaml:5:9: at a position",
			"base.yaml:5:9: at a range",
			"base.yaml:5:9: $.server: beside a path",
			"prod.yaml:2:3: at a key",
			"prod.yaml:2:9: $.server.host: through the Node",
			"base.yaml: in the preamble alone",
		))
	})

	t.Run("a nil validator runs nothing", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, layerFiles(t, baseInput, layerProdHost).ValidateDocuments(t.Context(), nil))
	})
}

func TestNewSourceFromLayers_Document(t *testing.T) {
	t.Parallel()

	var (
		host = paths.Doc().Child("server", "host")
		port = paths.Doc().Child("server", "port")
	)

	t.Run("the Node is the root of the merged document", func(t *testing.T) {
		t.Parallel()

		nodes := layerNodes(t, layerShop, layerProdHost)
		layers := mergedLayers(t, nodes)

		doc := yamltest.Document(t, layers)

		assert.NotContains(t, nodes, doc)
		assert.True(t, doc.Path().IsRoot())
		assert.Same(t, doc, doc.Document())
		assert.Equal(t, "base.yaml", doc.Source().Name())
		assert.Equal(t, niceyaml.NodeMapping, doc.Kind())
		assert.Equal(t, stringtest.JoinLF(
			"# base",
			"name: shop",
			"server:",
			"  host: prod.example.com",
			"  port: 0",
		),
			doc.View().Held().Content())

		// Every call returns the Node a validator gets.
		assert.Same(t, doc, yamltest.Document(t, layers))

		ran := false

		err := layers.ValidateDocuments(t.Context(), niceyaml.ValidatorFunc(
			func(_ context.Context, n *niceyaml.Node) error {
				ran = true

				assert.Same(t, doc, n)

				return nil
			},
		))
		require.NoError(t, err)
		assert.True(t, ran)
	})

	t.Run("a caller reads one value of the layers", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.Document(t, layerFiles(t, layerShop, layerProdHost))

		got, err := doc.DecodeAt[string](t.Context(), host)
		require.NoError(t, err)
		assert.Equal(t, "prod.example.com", got)

		server, err := yamltest.At(t, doc, paths.Doc().Child("server")).Decode[layerServer](
			t.Context(), niceyaml.WithSelfValidation(false),
		)
		require.NoError(t, err)
		assert.Equal(t, layerServer{Host: "prod.example.com"}, server)

		var tls string

		found, err := doc.DecodeIfPresent(t.Context(), paths.Doc().Child("server", "tls"), &tls)
		require.NoError(t, err)
		assert.False(t, found)

		fields, err := doc.Nodes(paths.Doc().Child("server").ChildAll())
		require.NoError(t, err)
		require.Len(t, fields, 2)
		assert.Equal(t, "$.server.host", fields[0].Path().String())
		assert.Equal(t, "$.server.port", fields[1].Path().String())
	})

	t.Run("a decode of one value reports every problem below it", func(t *testing.T) {
		t.Parallel()

		server := paths.Doc().Child("server")

		nodes := layerNodes(t,
			stringtest.LinesLF("server:", "  timeout: soon"),
			stringtest.LinesLF("server:", "  port: 70000", "name: [prod]"),
		)

		doc := yamltest.Document(t, mergedLayers(t, nodes))

		// The decoder rejects the timeout first, with no token of the
		// document, and the port lies in the other file.
		_, err := doc.DecodeAt[problemTimed](t.Context(), server)
		require.EqualError(t, err, stringtest.JoinLF(
			"base.yaml: 2 problems",
			`base.yaml:2:12: $.server.timeout: time: invalid duration "soon"`,
			"prod.yaml:2:9: $.server.port: expected integer from 0 to 65535, got 70000",
		))
		require.ErrorIs(t, err, niceyaml.ErrDecode)

		// One layer reports what a decode of its file reports.
		file := yamltest.FirstDocument(t,
			stringtest.LinesLF("name: [base]", "server: {port: 70000, tier: mid, timeout: soon}"),
			niceyaml.WithName("base.yaml"))

		_, want := file.DecodeAt[problemTimed](t.Context(), server)
		require.EqualError(t, want, stringtest.JoinLF(
			"base.yaml: 3 problems",
			"base.yaml:2:16: $.server.port: expected integer from 0 to 65535, got 70000",
			`base.yaml:2:29: $.server.tier: unknown tier "mid"`,
			`base.yaml:2:43: $.server.timeout: time: invalid duration "soon"`,
		))

		_, got := layersDocument(t, file).DecodeAt[problemTimed](t.Context(), server)
		require.EqualError(t, got, want.Error())
		assert.Equal(
			t,
			niceyaml.FormatError(want, niceyaml.WithContextLines(0)),
			niceyaml.FormatError(got, niceyaml.WithContextLines(0)),
		)
	})

	t.Run("an error binds in the file of a layer", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.Document(t, layerFiles(t, layerShop, layerProdHost))
		server := yamltest.At(t, doc, paths.Doc().Child("server"))

		_, wrongType := doc.DecodeAt[int](t.Context(), host)
		_, missing := doc.At(paths.Doc().Child("server", "tls"))

		tcs := map[string]struct {
			err  error
			want string
		}{
			"at a path": {
				err:  doc.Bind(niceyaml.NewError("here", niceyaml.AtPath(port))),
				want: "base.yaml:6:9: $.server.port: here",
			},
			"at a path from a scoped Node": {
				err:  server.Bind(yamltest.ErrorAt("here", "host")),
				want: "prod.yaml:2:9: $.server.host: here",
			},
			"at a scoped Node": {
				err:  server.Bind(niceyaml.NewError("here")),
				want: "prod.yaml:1:1: $.server: here",
			},
			"of a decode of one value": {
				err:  wrongType,
				want: "prod.yaml:2:9: $.server.host: expected integer, got string",
			},
			"of a path no layer holds": {
				err:  missing,
				want: "prod.yaml:1:1: $.server.tls: not found",
			},
			"with no location": {
				err:  doc.Bind(errors.New("quota service: connection refused")),
				want: "base.yaml: quota service: connection refused",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				require.EqualError(t, tc.err, tc.want)
			})
		}
	})

	t.Run("the positions of the Node read the merged text", func(t *testing.T) {
		t.Parallel()

		nodes := layerNodes(t, layerShop, layerProdHost)

		doc := yamltest.Document(t, mergedLayers(t, nodes))

		merged, err := doc.Ranges(port)
		require.NoError(t, err)

		inBase, err := nodes[0].Ranges(port)
		require.NoError(t, err)

		assert.Equal(t, position.New(4, 8), merged[0].Start)
		assert.Equal(t, position.New(5, 8), inBase[0].Start)

		// The error binds in base.yaml, at the range that file has for
		// the value.
		err = doc.Bind(niceyaml.NewError("here", niceyaml.AtPath(port)))

		bound := yamltest.RequireSourceError(t, err)
		assert.Same(t, nodes[0], yamltest.BoundNode(t, bound))
		assert.Same(t, nodes[0].Source(), bound.Source())

		assert.Equal(t, inBase[0], yamltest.RangeOf(t, bound))

		assert.False(t, niceyaml.Annotate(err, doc.View()))
		assert.True(t, niceyaml.Annotate(err, nodes[0].View()))

		// A position reads the merged text, so the one base.yaml has for
		// the port names no value of the merged document.
		err = doc.Bind(niceyaml.NewError("here", niceyaml.AtPosition(merged[0].Start)))
		require.EqualError(t, err, "base.yaml:6:9: here")

		err = doc.Bind(niceyaml.NewError("here", niceyaml.AtPosition(inBase[0].Start)))
		require.EqualError(t, err, "base.yaml: here")
	})
}

func TestNode_Origin(t *testing.T) {
	t.Parallel()

	baseInput := stringtest.LinesLF(
		"name: shop",
		"server:",
		"  host: example.com",
		"  port: 80",
		"tags: [a, b]",
	)

	var (
		server = paths.Doc().Child("server")
		host   = server.Child("host")
		port   = server.Child("port")
		tags   = paths.Doc().Child("tags")
	)

	// The layers above the base file that several cases share.
	var (
		port8080 = stringtest.LinesLF("server:", "  port: 8080")
		port8443 = stringtest.LinesLF("server:", "  port: 8443")
		midHost  = stringtest.LinesLF("server:", "  host: mid.example.com")
		newTags  = stringtest.LinesLF("tags:", "  - c", "  - d")
		aliased  = stringtest.LinesLF("defaults: &defaults", "  port: 9", "server: *defaults")
	)

	// Each case reads the origin of the Node at path in the layers that
	// inputs merge into, which [layerSources] names.
	tcs := map[string]struct {
		path paths.Path
		// The name of the layer of the origin.
		wantLayer string
		// The path of the origin in the document of that layer.
		wantPath string
		// The lines of that document the origin covers.
		want   string
		inputs []string
	}{
		"a scalar comes from the highest layer that sets it": {
			inputs:    []string{baseInput, port8080, port8443},
			path:      port,
			wantLayer: "prod.yaml",
			wantPath:  "$.server.port",
			want:      "  port: 8443",
		},
		"a scalar no higher layer sets comes from the layer below": {
			inputs:    []string{baseInput, midHost, port8443},
			path:      host,
			wantLayer: "mid.yaml",
			wantPath:  "$.server.host",
			want:      "  host: mid.example.com",
		},
		"an unwritten null in a higher layer holds no value": {
			inputs:    []string{baseInput, stringtest.LinesLF("server:", "  port:")},
			path:      port,
			wantLayer: "base.yaml",
			wantPath:  "$.server.port",
			want:      "  port: 80",
		},
		"a mapping comes from the highest layer that holds it, with the keys of that layer alone": {
			inputs:    []string{baseInput, port8443},
			path:      server,
			wantLayer: "prod.yaml",
			wantPath:  "$.server",
			want:      stringtest.JoinLF("server:", "  port: 8443"),
		},
		"a mapping one layer holds comes from that layer whole": {
			inputs:    []string{baseInput, "name: prod\n"},
			path:      server,
			wantLayer: "base.yaml",
			wantPath:  "$.server",
			want:      stringtest.JoinLF("server:", "  host: example.com", "  port: 80"),
		},
		"a sequence comes whole from the layer that replaces it": {
			inputs:    []string{baseInput, newTags},
			path:      tags,
			wantLayer: "prod.yaml",
			wantPath:  "$.tags",
			want:      stringtest.JoinLF("tags:", "  - c", "  - d"),
		},
		"an element comes from the layer of its sequence": {
			inputs:    []string{baseInput, newTags},
			path:      tags.Index(1),
			wantLayer: "prod.yaml",
			wantPath:  "$.tags[1]",
			want:      "  - d",
		},
		"a sequence no higher layer holds comes from the layer below": {
			inputs:    []string{baseInput, port8443},
			path:      tags.Index(0),
			wantLayer: "base.yaml",
			wantPath:  "$.tags[0]",
			want:      "tags: [a, b]",
		},
		"a value read through an alias comes from the content of the anchor": {
			inputs:    []string{baseInput, aliased},
			path:      port,
			wantLayer: "prod.yaml",
			wantPath:  "$.server.port",
			want:      "  port: 9",
		},
		"a mapping read through an alias comes from the content of the anchor": {
			inputs:    []string{baseInput, aliased},
			path:      server,
			wantLayer: "prod.yaml",
			wantPath:  "$.server",
			want:      "  port: 9",
		},
		"a value a merge key brings in comes from the content of the anchor": {
			inputs: []string{
				baseInput,
				stringtest.LinesLF(
					"defaults: &defaults",
					"  port: 9",
					"server:",
					"  <<: *defaults",
					"  host: prod.example.com",
				),
			},
			path:      port,
			wantLayer: "prod.yaml",
			wantPath:  "$.server.port",
			want:      "  port: 9",
		},
		"a key comes from the layer of its value": {
			inputs:    []string{baseInput, port8443},
			path:      port.Key(),
			wantLayer: "prod.yaml",
			wantPath:  "$.server.port~",
			want:      "  port: 8443",
		},
		"the root comes from the highest layer that holds a value": {
			inputs:    []string{baseInput, port8080, port8443},
			path:      paths.Doc(),
			wantLayer: "prod.yaml",
			wantPath:  "$",
			want:      stringtest.JoinLF("server:", "  port: 8443"),
		},
		"the root passes over a layer that holds no value": {
			inputs:    []string{baseInput, port8080, "# nothing yet\n"},
			path:      paths.Doc(),
			wantLayer: "mid.yaml",
			wantPath:  "$",
			want:      stringtest.JoinLF("server:", "  port: 8080"),
		},
		"the root of layers that hold no value comes from the highest layer": {
			inputs:    []string{"# base\n", "# prod\n"},
			path:      paths.Doc(),
			wantLayer: "prod.yaml",
			wantPath:  "$",
			want:      "# prod",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			node := yamltest.AtOrSelf(t, yamltest.Document(t, layerFiles(t, tc.inputs...)), tc.path)

			origin, err := node.Origin()
			require.NoError(t, err)

			assert.Equal(t, tc.wantLayer, origin.Source().Name())
			assert.Equal(t, tc.wantPath, origin.Path().String())
			assert.Equal(t, tc.want, origin.View().Held().Content())

			// An error at the Node binds in the layer of its origin.
			bound := yamltest.RequireSourceError(t, node.Bind(yamltest.ErrorAt("bad value")))
			assert.Same(t, origin.Source(), bound.Source())

			// The origin belongs to a file, so it is its own origin.
			again, err := origin.Origin()
			require.NoError(t, err)
			assert.Same(t, origin, again)
		})
	}

	t.Run("an error at a merged Node marks the view of its origin", func(t *testing.T) {
		t.Parallel()

		// Each case binds an error at the Node at path in the layers that
		// inputs merge into, and lists the view of the origin of that
		// Node with the error marked on it.
		tcs := map[string]struct {
			path   paths.Path
			want   string
			inputs []string
		}{
			"mapping": {
				inputs: []string{baseInput, port8443},
				path:   server,
				want: stringtest.JoinLF(
					"   1 | server:",
					"     | ^^^^^^ bad value",
					"   2 |   port: 8443",
				),
			},
			"sequence": {
				inputs: []string{baseInput, newTags},
				path:   tags,
				want: stringtest.JoinLF(
					"   1 | tags:",
					"     | ^^^^ bad value",
					"   2 |   - c",
					"   3 |   - d",
				),
			},
			"scalar": {
				inputs: []string{baseInput, port8443},
				path:   port,
				want: stringtest.JoinLF(
					"   2 |   port: 8443",
					"     |         ^^^^ bad value",
				),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				node := yamltest.At(t, yamltest.Document(t, layerFiles(t, tc.inputs...)), tc.path)

				bound := yamltest.RequireSourceError(t, node.Bind(errors.New("bad value")))

				// The error binds in the file of a layer, so the view of
				// the merged text holds no line of it.
				assert.False(t, bound.Annotate(node.View()))

				origin, err := node.Origin()
				require.NoError(t, err)

				view := origin.View()
				require.True(t, bound.Annotate(view))
				assert.Equal(t, tc.want, view.String())
			})
		}
	})

	t.Run("a Node of a plain file is its own origin", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, baseInput, niceyaml.WithName("base.yaml"))

		origin, err := doc.Origin()
		require.NoError(t, err)
		assert.Same(t, doc, origin)

		scoped := yamltest.At(t, doc, port)

		origin, err = scoped.Origin()
		require.NoError(t, err)
		assert.Same(t, scoped, origin)

		// A document that did not parse is no merged document either.
		broken := yamltest.UnparsedDocument(t, "server: [\n")

		origin, err = broken.Origin()
		require.NoError(t, err)
		assert.Same(t, broken, origin)
	})

	t.Run("the root returns the Node of its layer", func(t *testing.T) {
		t.Parallel()

		base := niceyaml.NewSourceFromString(baseInput, niceyaml.WithFilePath("conf/base.yaml"))
		prod := niceyaml.NewSourceFromString(port8443, niceyaml.WithFilePath("conf/prod.yaml"))

		prodDoc := yamltest.Document(t, prod)

		// A Source gives the Node its Document method returns, and a Node
		// gives itself.
		for name, top := range map[string]niceyaml.Layer{"source": prod, "node": prodDoc} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := layersDocument(t, base, top)

				origin, err := doc.Origin()
				require.NoError(t, err)
				assert.Same(t, prodDoc, origin)

				// The merged document keeps the file path of the lowest layer.
				assert.Equal(t, "conf/base.yaml", doc.FilePath())
				assert.Equal(t, "conf/prod.yaml", origin.FilePath())
			})
		}
	})

	t.Run("a validator reads the file of a value from its origin", func(t *testing.T) {
		t.Parallel()

		license := paths.Current().Child("license")

		baseFS := fstest.MapFS{"conf/base.yaml": &fstest.MapFile{}}
		prodFS := fstest.MapFS{"conf/sub/prod.yaml": &fstest.MapFile{}}

		base := niceyaml.NewSourceFromString(stringtest.LinesLF("name: shop", "license: ./LICENSE"),
			niceyaml.WithFilePath("conf/base.yaml"), niceyaml.WithFS(baseFS))
		prod := niceyaml.NewSourceFromString("license: ./LICENSE.prod\n",
			niceyaml.WithFilePath("conf/sub/prod.yaml"), niceyaml.WithFS(prodFS))

		var values, origins []*niceyaml.Node

		beside := niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
			value, err := n.At(license)
			if err != nil {
				return err //nolint:wrapcheck // The validator returns the error of the read as it is.
			}

			origin, err := value.Origin()
			if err != nil {
				return err //nolint:wrapcheck // The validator returns the error of the read as it is.
			}

			values = append(values, value)
			origins = append(origins, origin)

			return nil
		})

		// The same validator runs on the layers and on one file.
		require.NoError(t, yamltest.Layers(t, base, prod).ValidateDocuments(t.Context(), beside))
		require.NoError(t, base.ValidateDocuments(t.Context(), beside))
		require.Len(t, origins, 2)

		// Under the layers, the Node the validator read names the lowest
		// layer, and its origin names the layer that sets the license.
		assert.Equal(t, "conf/base.yaml", values[0].FilePath())
		assert.Equal(t, baseFS, values[0].FS())
		assert.Equal(t, "conf/sub/prod.yaml", origins[0].FilePath())
		assert.Equal(t, prodFS, origins[0].FS())
		assert.Equal(t, "license: ./LICENSE.prod", origins[0].View().Held().Content())

		// On one file, that Node is its own origin.
		assert.Same(t, values[1], origins[1])
		assert.Equal(t, "conf/base.yaml", origins[1].FilePath())
	})

	t.Run("a scoped layer gives the path its file has for the value", func(t *testing.T) {
		t.Parallel()

		envs := yamltest.FirstDocument(t,
			stringtest.LinesLF(
				"defaults:",
				"  server:",
				"    host: example.com",
				"    port: 80",
				"prod:",
				"  server:",
				"    port: 8443",
			),
			niceyaml.WithName("envs.yaml"))

		defaults := yamltest.At(t, envs, paths.Doc().Child("defaults"))
		prod := yamltest.At(t, envs, paths.Doc().Child("prod"))

		doc := layersDocument(t, defaults, prod)

		origin, err := yamltest.At(t, doc, port).Origin()
		require.NoError(t, err)
		assert.Same(t, envs.Source(), origin.Source())
		assert.Equal(t, "$.prod.server.port", origin.Path().String())
		assert.Equal(t, "    port: 8443", origin.View().Held().Content())

		origin, err = yamltest.At(t, doc, host).Origin()
		require.NoError(t, err)
		assert.Equal(t, "$.defaults.server.host", origin.Path().String())
		assert.Equal(t, "    host: example.com", origin.View().Held().Content())

		// The root of the merged document reads from the scoped Node.
		origin, err = doc.Origin()
		require.NoError(t, err)
		assert.Same(t, prod, origin)
	})

	t.Run("a layer a merge built gives a Node of a file", func(t *testing.T) {
		t.Parallel()

		sources := layerSources(t, baseInput, midHost, port8443)

		inner := layersDocument(t, sources[0], sources[1])

		below := []niceyaml.Layer{inner, sources[2]}
		above := []niceyaml.Layer{sources[2], inner}

		tcs := map[string]struct {
			path      paths.Path
			wantLayer string
			want      string
			layers    []niceyaml.Layer
		}{
			"below a file, a value of its lower layer": {
				layers: below, path: tags, wantLayer: "base.yaml", want: "tags: [a, b]",
			},
			"below a file, a value of its higher layer": {
				layers: below, path: host, wantLayer: "mid.yaml", want: "  host: mid.example.com",
			},
			"below a file, a value of the file": {
				layers: below, path: port, wantLayer: "prod.yaml", want: "  port: 8443",
			},
			"below a file, the root": {
				layers: below, path: paths.Doc(), wantLayer: "prod.yaml",
				want: stringtest.JoinLF("server:", "  port: 8443"),
			},
			"above a file, a value of its lower layer": {
				layers: above, path: tags, wantLayer: "base.yaml", want: "tags: [a, b]",
			},
			"above a file, a value of its higher layer": {
				layers: above, path: host, wantLayer: "mid.yaml", want: "  host: mid.example.com",
			},
			"above a file, a value the file sets too": {
				layers: above, path: port, wantLayer: "base.yaml", want: "  port: 80",
			},
			"above a file, the root": {
				layers: above, path: paths.Doc(), wantLayer: "mid.yaml",
				want: stringtest.JoinLF("server:", "  host: mid.example.com"),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				node := yamltest.AtOrSelf(t, layersDocument(t, tc.layers...), tc.path)

				origin, err := node.Origin()
				require.NoError(t, err)
				assert.Equal(t, tc.wantLayer, origin.Source().Name())
				assert.Equal(t, tc.want, origin.View().Held().Content())
			})
		}
	})

	t.Run("a value that a layer reads from a reference document", func(t *testing.T) {
		t.Parallel()

		shared := niceyaml.NewSourceFromString(
			"shared: &shared {host: shared.example.com, port: 9}\n", niceyaml.WithName("shared.yaml"),
		)
		base := niceyaml.NewSourceFromString(baseInput, niceyaml.WithName("base.yaml"))

		tcs := map[string]struct {
			prod string
			path paths.Path
			err  string
		}{
			"a value below the alias": {
				prod: "server: *shared\n",
				path: port,
				err:  "prod.yaml:1:9: $.server: resolve $.server.port: alias reads a reference document: *shared",
			},
			"a value a merge key reads": {
				prod: stringtest.LinesLF("server:", "  <<: *shared"),
				path: port,
				err:  "prod.yaml:2:7: $.server: resolve $.server.port: alias reads a reference document: *shared",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				prod := niceyaml.NewSourceFromString(
					tc.prod,
					niceyaml.WithName("prod.yaml"),
					niceyaml.WithReferences(shared),
				)

				// The merged document holds the value the alias reads.
				node := yamltest.At(t, layersDocument(t, base, prod), tc.path)

				origin, err := node.Origin()
				require.EqualError(t, err, tc.err)
				require.ErrorIs(t, err, paths.ErrAlias)
				assert.Nil(t, origin)

				// The error names the layer that holds the alias.
				assert.Same(t, prod, yamltest.RequireSourceError(t, err).Source())
			})
		}

		// The mapping that holds the merge key has a Node in its layer.
		prod := niceyaml.NewSourceFromString(
			stringtest.LinesLF("server:", "  <<: *shared"),
			niceyaml.WithName("prod.yaml"),
			niceyaml.WithReferences(shared),
		)

		origin, err := yamltest.At(t, layersDocument(t, base, prod), server).Origin()
		require.NoError(t, err)
		assert.Same(t, prod, origin.Source())
		assert.Equal(t, stringtest.JoinLF("server:", "  <<: *shared"), origin.View().Held().Content())

		t.Run("the alias", func(t *testing.T) {
			t.Parallel()

			// The alias has a Node in its layer, which reads the anchor of
			// the reference.
			prod := niceyaml.NewSourceFromString(
				"server: *shared\n",
				niceyaml.WithName("prod.yaml"),
				niceyaml.WithReferences(shared),
			)

			origin, err := yamltest.At(t, layersDocument(t, base, prod), server).Origin()
			require.NoError(t, err)
			assert.Same(t, prod, origin.Source())
			assert.Equal(t, server, origin.Path())
			assert.Equal(t, "*shared", origin.AST().String())
			assert.Equal(t, niceyaml.NodeMapping, origin.Kind())

			got, err := origin.Decode[map[string]any](t.Context())
			require.NoError(t, err)
			assert.Equal(t, map[string]any{"host": "shared.example.com", "port": uint64(9)}, got)
		})
	})
}

func TestNewSourceFromLayers_Empty(t *testing.T) {
	t.Parallel()

	const want = "$.server.port: port must be at least 1"

	tcs := map[string]*niceyaml.Source{
		"no layers":       yamltest.Layers(t),
		"only nil layers": yamltest.Layers(t, nil, (*niceyaml.Source)(nil), (*niceyaml.Node)(nil)),
	}

	for name, layers := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// The value came from no file, as defaults do.
			cfg := layerConfig{Server: layerServer{Host: "example.com"}}

			err := layers.DecodeInto(t.Context(), &cfg)
			require.EqualError(t, err, want)
			assert.Equal(t, "example.com", cfg.Server.Host)

			err = layers.SelfValidate(t.Context(), &cfg)
			require.EqualError(t, err, want)

			err = layers.Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Doc().Child("server"))))
			require.EqualError(t, err, "$.server: bad")

			// The layers merge into an empty document with no name.
			doc := yamltest.Document(t, layers)
			require.NotNil(t, doc)
			assert.True(t, doc.IsEmpty())
			assert.Empty(t, doc.Source().Name())

			// The document is its own origin.
			origin, err := doc.Origin()
			require.NoError(t, err)
			assert.Same(t, doc, origin)

			err = doc.Bind(niceyaml.NewError("port is required", niceyaml.AtPath(paths.Doc().Child("server", "port"))))
			require.EqualError(t, err, "$.server.port: port is required")

			ran := false

			err = layers.ValidateDocuments(t.Context(), niceyaml.ValidatorFunc(
				func(_ context.Context, n *niceyaml.Node) error {
					ran = true

					assert.Empty(t, n.Source().Name())
					assert.True(t, n.IsEmpty())

					return nil
				},
			))
			require.NoError(t, err)
			assert.True(t, ran)

			cfg.Server.Port = 80

			got, err := layers.Decode[layerConfig](t.Context())
			require.NoError(t, err)
			assert.Empty(t, got.Server)

			require.NoError(t, layers.SelfValidate(t.Context(), &cfg))
		})
	}
}

func TestNewSourceFromLayers_Bind(t *testing.T) {
	t.Parallel()

	nodes := layerNodes(t,
		stringtest.LinesLF(
			"server:",
			"  host: example.com",
			"  port: 8080",
			"servers:",
			"  - {host: a, port: 80}",
		),
		stringtest.LinesLF(
			"server:",
			"  host: prod.example.com",
			"backends:",
			"  a: {host: x, port: 80}",
		),
	)
	layers := mergedLayers(t, nodes)

	portPath := paths.Doc().Child("server", "port")

	tcs := map[string]struct {
		err  error
		want string
	}{
		"a field binds in the layer that sets it": {
			err:  niceyaml.NewError("port is taken", niceyaml.AtPath(portPath)),
			want: "base.yaml:3:9: $.server.port: port is taken",
		},
		"a field the top layer sets binds there": {
			err:  niceyaml.NewError("unknown host", niceyaml.AtPath(paths.Doc().Child("server", "host"))),
			want: "prod.yaml:2:9: $.server.host: unknown host",
		},
		"a path from the value reads as one from the root": {
			err:  yamltest.ErrorAt("unknown host", "server", "host"),
			want: "prod.yaml:2:9: $.server.host: unknown host",
		},
		"a mapping that both layers hold binds in the higher one": {
			err:  niceyaml.NewError("bad server", niceyaml.AtPath(paths.Doc().Child("server"))),
			want: "prod.yaml:1:1: $.server: bad server",
		},
		"a key binds in the layer of its value": {
			err:  niceyaml.NewError("bad key", niceyaml.AtPath(portPath.Key())),
			want: "base.yaml:3:3: $.server.port~: bad key",
		},
		"an element binds in the layer that holds the sequence": {
			err:  niceyaml.NewError("bad", niceyaml.AtPath(paths.Doc().Child("servers").Index(0).Child("port"))),
			want: "base.yaml:5:21: $.servers[0].port: bad",
		},
		"an element the sequence lacks binds in its layer with no position": {
			err:  niceyaml.NewError("bad", niceyaml.AtPath(paths.Doc().Child("servers").Index(3).Child("port"))),
			want: "base.yaml: $.servers[3].port: bad",
		},
		"a path below a scalar binds in the layer of the scalar": {
			err:  niceyaml.NewError("bad", niceyaml.AtPath(portPath.Child("unit"))),
			want: "base.yaml: $.server.port.unit: bad",
		},
		"an entry binds in the layer that holds it": {
			err:  niceyaml.NewError("bad", niceyaml.AtPath(paths.Doc().Child("backends", "a", "port"))),
			want: "prod.yaml:4:22: $.backends.a.port: bad",
		},
		"the root binds in the highest layer": {
			err:  niceyaml.NewError("bad config", niceyaml.AtPath(paths.Doc())),
			want: "prod.yaml:1:1: $: bad config",
		},
		"a plain error binds in the lowest layer with no position": {
			err:  errors.New("quota service: connection refused"),
			want: "base.yaml: quota service: connection refused",
		},
		"a join binds each branch in its layer": {
			err: errors.Join(
				niceyaml.NewError("port is taken", niceyaml.AtPath(portPath)),
				niceyaml.NewError("unknown host", niceyaml.AtPath(paths.Doc().Child("server", "host"))),
			),
			want: stringtest.JoinLF(
				"base.yaml:3:9: $.server.port: port is taken",
				"prod.yaml:2:9: $.server.host: unknown host",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			require.EqualError(t, layers.Bind(tc.err), tc.want)
		})
	}

	t.Run("a nil error stays nil", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, layers.Bind(nil))
	})

	t.Run("a position outside the merged text holds no value", func(t *testing.T) {
		t.Parallel()

		// The merged text keeps no comment of base.yaml, so it ends
		// above the last lines of that file.
		long := layerNodes(t,
			stringtest.LinesLF(
				"server:",
				"  host: example.com",
				"",
				"  # one",
				"  # two",
				"  port: 8080",
			),
			stringtest.LinesLF("server:", "  port: 80"),
		)
		merged := mergedLayers(t, long)
		require.Equal(t, 3, merged.Lines().Len())

		doc := yamltest.Document(t, merged)
		inBase := position.New(4, 2)

		tcs := map[string]struct {
			err  error
			want string
		}{
			"a line of the lowest layer": {
				err:  niceyaml.NewError("bad", niceyaml.AtPosition(inBase)),
				want: "base.yaml: bad",
			},
			"a line no layer holds": {
				err:  niceyaml.NewError("bad", niceyaml.AtPosition(position.New(20, 0))),
				want: "base.yaml: bad",
			},
			"a range": {
				err:  niceyaml.NewError("bad", niceyaml.AtRange(yamltest.LineRange(4, 2, 7))),
				want: "base.yaml: bad",
			},
			"a path beside the position locates the error": {
				err:  niceyaml.NewError("bad", niceyaml.AtPath(portPath), niceyaml.AtPosition(inBase)),
				want: "prod.yaml:2:9: $.server.port: bad",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				// The Source binds the error as its one document does.
				require.EqualError(t, merged.Bind(tc.err), tc.want)
				require.EqualError(t, doc.Bind(tc.err), tc.want)

				bound := yamltest.RequireSourceError(t, merged.Bind(tc.err))
				require.NoError(t, bound.Unresolved())
			})
		}
	})

	t.Run("no error binds in the merged document", func(t *testing.T) {
		t.Parallel()

		// An error with no location belongs to the lowest layer.
		err := layers.Bind(errors.New("quota service: connection refused"))
		bound := yamltest.RequireSourceError(t, err)
		assert.Same(t, nodes[0], yamltest.BoundNode(t, bound))
		assert.Same(t, nodes[0].Source(), bound.Source())

		// A located one belongs to the layer that holds its value.
		err = layers.Bind(niceyaml.NewError("unknown host", niceyaml.AtPath(paths.Doc().Child("server", "host"))))
		bound = yamltest.RequireSourceError(t, err)
		assert.Same(t, nodes[1], yamltest.BoundNode(t, bound))
		assert.Same(t, nodes[1].Source(), bound.Source())
	})
}

func TestNewSourceFromLayers_ExcerptsOff(t *testing.T) {
	t.Parallel()

	// The environment sets a password alone, and its text must not show.
	base := yamltest.FirstDocument(t,
		stringtest.LinesLF("db:", "  host: db.internal", "  port: 0"),
		niceyaml.WithName("base.yaml"),
	)
	env := yamltest.FirstDocument(t, stringtest.LinesLF("db:", "  password: hunter2"),
		niceyaml.WithName("environment"), niceyaml.WithExcerpts(false))

	layers := yamltest.Layers(t, base, env)
	db := paths.Doc().Child("db")

	requiresName := schema.MustCompile([]byte(`{
		"properties": {"db": {"required": ["name"]}}
	}`))
	requiresPort := schema.MustCompile([]byte(`{
		"properties": {"db": {"required": ["name"], "properties": {"port": {"minimum": 1}}}}
	}`))

	validate := func(v niceyaml.Validator) func(t *testing.T) error {
		return func(t *testing.T) error {
			t.Helper()

			_, err := layers.Decode[map[string]any](t.Context(), niceyaml.WithValidator(v))

			return err
		}
	}

	bind := func(err error) func(t *testing.T) error {
		return func(*testing.T) error { return layers.Bind(err) }
	}

	tcs := map[string]struct {
		fail func(t *testing.T) error
		want string
	}{
		// No layer holds the name, so its error binds in the highest
		// layer that holds the mapping, which is the environment.
		"a key no layer holds prints no line of the environment": {
			fail: validate(requiresName),
			want: `environment:1:1: $.db.name: missing required property "name"`,
		},
		"a value of the environment prints no line": {
			fail: bind(niceyaml.NewError("too short", niceyaml.AtPath(db.Child("password")))),
			want: "environment:2:13: $.db.password: too short",
		},
		"a value of a file keeps its excerpt": {
			fail: bind(niceyaml.NewError("port must be at least 1", niceyaml.AtPath(db.Child("port")))),
			want: stringtest.JoinLF(
				"base.yaml:3:9: $.db.port: port must be at least 1",
				"",
				"   1 | db:",
				"   2 |   host: db.internal",
				"   3 |   port: 0",
				"     |         ^",
			),
		},
		"errors in both layers print the excerpt of the file alone": {
			fail: validate(requiresPort),
			want: stringtest.JoinLF(
				"base.yaml: 2 schema violations",
				"|-- 3:9: $.db.port: 0 is less than 1",
				"`-- environment:1:1: $.db.name: missing required property \"name\"",
				"",
				"base.yaml",
				"   1 | db:",
				"   2 |   host: db.internal",
				"   3 |   port: 0",
				"     |         ^ 0 is less than 1",
			),
		},
		// The decoder reads the merged text, which holds the password, and
		// the go-yaml error it returns shows that text in an excerpt of its
		// own. [errors.As] finds that error, and no error of the chain
		// unwraps to it.
		"a value of a file the decoder rejects keeps its excerpt": {
			fail: func(t *testing.T) error {
				t.Helper()

				_, err := layers.Decode[struct {
					DB struct {
						Host int `yaml:"host"`
					} `yaml:"db"`
				}](t.Context())

				yamlErr, ok := errors.AsType[yaml.Error](err)
				require.True(t, ok, "errors.As finds no yaml.Error: %v", err)
				require.Contains(t, yamlErr.Error(), excerptSecret)

				return err
			},
			want: stringtest.JoinLF(
				"base.yaml:2:9: $.db.host: expected integer, got string",
				"",
				"   1 | db:",
				"   2 |   host: db.internal",
				"     |         ^^^^^^^^^^^",
				"   3 |   port: 0",
			),
		},
		// The position names the password in the merged text, whose error
		// binds at the value in its layer.
		"a position in the merged text prints no line": {
			fail: func(t *testing.T) error {
				t.Helper()

				return yamltest.Document(t, layers).
					Bind(niceyaml.NewError("too short", niceyaml.AtPosition(position.New(3, 12))))
			},
			want: "environment:2:13: too short",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := tc.fail(t)
			require.Error(t, err)

			assert.Equal(t, tc.want, niceyaml.FormatError(err))

			for renderer, got := range renderings(err) {
				assert.NotContains(t, got, excerptSecret, renderer)
			}
		})
	}
}

func TestNewSourceFromLayers_Alias(t *testing.T) {
	t.Parallel()

	// The top layer reads its server from a reference document, which a
	// path cannot follow. The merge reads it, so each error binds in the
	// layer that holds its value.
	tcs := map[string]struct {
		shared string
		prod   string
		err    string
		// Whether the path of the error enters the alias.
		unresolved bool
	}{
		"a value the reference document sets binds at the alias": {
			shared:     "shared: &shared {host: shared.example.com, port: -1}\n",
			prod:       "server: *shared\n",
			err:        "prod.yaml:1:9: $.server.port: port must be at least 1",
			unresolved: true,
		},
		"a value a merge key reads from the reference document binds at the alias": {
			shared:     "shared: &shared {host: shared.example.com, port: -1}\n",
			prod:       stringtest.LinesLF("server:", "  <<: *shared"),
			err:        "prod.yaml:2:7: $.server.port: port must be at least 1",
			unresolved: true,
		},
		"a value the reference document leaves out binds in the layer below": {
			shared: "shared: &shared {host: shared.example.com}\n",
			prod:   "server: *shared\n",
			err:    "base.yaml:3:9: $.server.port: port must be at least 1",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			shared := niceyaml.NewSourceFromString(tc.shared, niceyaml.WithName("shared.yaml"))
			base := yamltest.FirstDocument(t, layerBadPort, niceyaml.WithName("base.yaml"))
			prod := yamltest.FirstDocument(t, tc.prod, niceyaml.WithName("prod.yaml"), niceyaml.WithReferences(shared))

			var cfg layerConfig

			err := yamltest.Layers(t, base, prod).DecodeInto(t.Context(), &cfg)
			require.EqualError(t, err, tc.err)
			assert.Equal(t, "shared.example.com", cfg.Server.Host)

			bound := yamltest.RequireSourceError(t, err)

			if tc.unresolved {
				require.ErrorIs(t, bound.Unresolved(), paths.ErrAlias)
			}
		})
	}
}

func TestNewSourceFromLayers_AliasExpansion(t *testing.T) {
	t.Parallel()

	// The merge writes l out with the 100 bytes of the anchor a at each of
	// its 20 aliases.
	repeated := yamltest.AliasedScalar(100, 20)

	// The merge writes ten levels of sequences out at each alias, in block
	// style, which indents each level more deeply.
	deep := stringtest.LinesLF(
		"a: &a "+strings.Repeat("[", 9)+yamltest.FlowSequence("x", 10)+strings.Repeat("]", 9),
		"l:",
	) + strings.Repeat("  - *a\n", 20)

	tcs := map[string]struct {
		input string
		err   string
		opts  []niceyaml.SourceOption
	}{
		"aliases past the byte limit": {
			// The tenth alias brings the text the aliases repeat to 1050
			// bytes.
			input: repeated,
			opts:  []niceyaml.SourceOption{niceyaml.WithByteLimit(1024)},
			err:   "prod.yaml:12:5: aliases repeat more than 1 KiB of text",
		},
		"aliases of deep content past the byte limit": {
			// The fourth alias brings the indentation of block style past
			// 1 KiB.
			input: deep,
			opts:  []niceyaml.SourceOption{niceyaml.WithByteLimit(1024)},
			err:   "prod.yaml:6:5: aliases repeat more than 1 KiB of text",
		},
		"aliases of deep content within a raised byte limit": {
			input: deep,
			opts:  []niceyaml.SourceOption{niceyaml.WithByteLimit(8192)},
		},
		"aliases within a raised byte limit": {
			input: repeated,
			opts:  []niceyaml.SourceOption{niceyaml.WithByteLimit(4096)},
		},
		"aliases with the byte limit off": {
			input: repeated,
			opts:  []niceyaml.SourceOption{niceyaml.WithByteLimit(0)},
		},
		"aliases with the alias limit off": {
			input: repeated,
			opts: []niceyaml.SourceOption{
				niceyaml.WithByteLimit(1024),
				niceyaml.WithAliasLimit(false),
			},
		},
		"alias chain past the depth limit": {
			// The alias *c98 nests c99 more than 100 levels deep.
			input: yamltest.AliasChain(150),
			err:   "prod.yaml:100:12: aliases nest the document more than 100 levels",
		},
		"alias chain past a lower depth limit": {
			input: yamltest.AliasChain(20),
			opts:  []niceyaml.SourceOption{niceyaml.WithDepthLimit(10)},
			err:   "prod.yaml:10:10: aliases nest the document more than 10 levels",
		},
		"alias chain within the depth limit": {
			input: yamltest.AliasChain(98),
		},
		"alias chain with the depth limit off": {
			input: yamltest.AliasChain(150),
			opts:  []niceyaml.SourceOption{niceyaml.WithDepthLimit(0)},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			base := yamltest.FirstDocument(t, "base: 1\n", niceyaml.WithName("base.yaml"))
			prod := yamltest.FirstDocument(t, tc.input,
				append([]niceyaml.SourceOption{niceyaml.WithName("prod.yaml")}, tc.opts...)...)

			// A decode into an any value shares each anchor between its
			// aliases, so the layer decodes alone.
			_, err := prod.Decode[any](t.Context())
			require.NoError(t, err)

			merged, err := niceyaml.NewSourceFromLayers(t.Context(), []*niceyaml.Node{base, prod})
			if tc.err == "" {
				require.NoError(t, err)
				assert.NotNil(t, merged)

				return
			}

			require.EqualError(t, err, tc.err)
			require.ErrorIs(t, err, niceyaml.ErrLimit)
			require.ErrorIs(t, err, niceyaml.ErrExcessiveAliasing)
			assert.True(t, niceyaml.IsInvalid(err))
			assert.Nil(t, merged)

			bound := yamltest.RequireSourceError(t, err)
			assert.Same(t, prod, yamltest.BoundNode(t, bound))
		})
	}

	t.Run("layers without aliases merge past the byte limit", func(t *testing.T) {
		t.Parallel()

		// Each layer holds its own keys and passes its own limit, so the
		// merged text holds the keys of both.
		var layers []*niceyaml.Source

		for _, prefix := range []string{"base", "prod"} {
			input := yamltest.GenerateLines(40, prefix+"%d: "+strings.Repeat("v", 10))
			require.Less(t, len(input), 1024)

			layers = append(layers, niceyaml.NewSourceFromString(input,
				niceyaml.WithName(prefix+".yaml"), niceyaml.WithByteLimit(1024)))
		}

		merged, err := niceyaml.NewSourceFromLayers(t.Context(), layers)
		require.NoError(t, err)
		assert.Greater(t, len(merged.Text()), 1024)

		got, err := merged.Decode[map[string]string](t.Context())
		require.NoError(t, err)
		assert.Len(t, got, 80)
	})
}

func TestNewSourceFromLayers_Concurrent(t *testing.T) {
	t.Parallel()

	layers := layerFiles(t, layerBadPort, layerProdHost)

	// NewSourceFromLayers merged and parsed the layers already, so
	// every call reads the one merged document.
	const calls = 8

	var wg sync.WaitGroup

	decoded, bound := make([]error, calls), make([]error, calls)
	docs, merged := make([]*niceyaml.Node, calls), make([]error, calls)

	for i := range calls {
		wg.Go(func() {
			docs[i], merged[i] = layers.Document()

			_, decoded[i] = layers.Decode[layerConfig](t.Context())

			bound[i] = layers.Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Doc().Child("server", "host"))))
		})
	}

	wg.Wait()

	for i := range calls {
		require.NoError(t, merged[i])
		assert.Same(t, docs[0], docs[i])
		require.EqualError(t, decoded[i], "base.yaml:3:9: $.server.port: port must be at least 1")
		require.EqualError(t, bound[i], "prod.yaml:2:9: $.server.host: bad")
	}
}

// The documents most tests of the layers merge.
var (
	// A base file whose server names a host and an invalid port.
	layerBadPort = stringtest.LinesLF(
		"server:",
		"  host: example.com",
		"  port: 0",
	)

	// A base file whose server passes its check.
	layerPort80 = stringtest.LinesLF(
		"server:",
		"  host: example.com",
		"  port: 80",
	)

	// A file that replaces the host and leaves the port to the layer below.
	layerProdHost = stringtest.LinesLF("server:", "  host: prod.example.com")

	// A base file that names a shop above the server of [layerBadPort]. The
	// blank line keeps the lines of base.yaml apart from the lines of any
	// document it merges into.
	layerShop = stringtest.LinesLF(
		"# base",
		"name: shop",
		"",
		"server:",
		"  host: example.com",
		"  port: 0",
	)

	// A base file that holds the server of [layerBadPort] under defaults.
	layerDefaults = stringtest.LinesLF(
		"defaults:",
		"  server:",
		"    host: example.com",
		"    port: 0",
	)

	// MergedCorpus holds documents that spell their values in the ways the
	// merged document must write again. Each has a mapping at its root, so
	// an empty mapping above or below it changes nothing.
	mergedCorpus = map[string]struct {
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
		"a local tag over no value":    {input: stringtest.LinesLF("first: 1", "last: !custom")},
		"a mapping tag over no value":  {input: stringtest.LinesLF("first: 1", "last: &anchored !!map")},
		"a sequence tag over no value": {input: stringtest.LinesLF("first: 1", "last:", "  - !!seq")},
		"a string tag over no value":   {input: stringtest.LinesLF("first: 1", "last: !!str")},
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
)

// layerNodes returns the root Node of each of inputs, in the order they
// apply. The first input is base.yaml and the last is prod.yaml, with
// mid.yaml between them. A single input is base.yaml.
func layerNodes(t *testing.T, inputs ...string) []*niceyaml.Node {
	t.Helper()

	nodes := make([]*niceyaml.Node, len(inputs))

	for i, input := range inputs {
		name := "mid.yaml"

		switch i {
		case 0:
			name = "base.yaml"
		case len(inputs) - 1:
			name = "prod.yaml"
		}

		nodes[i] = yamltest.FirstDocument(t, input, niceyaml.WithName(name))
	}

	return nodes
}

// layerSources returns a Source of each of inputs, in the order they
// apply, under the names [layerNodes] gives them.
func layerSources(t *testing.T, inputs ...string) []*niceyaml.Source {
	t.Helper()

	nodes := layerNodes(t, inputs...)
	sources := make([]*niceyaml.Source, len(nodes))

	for i, node := range nodes {
		sources[i] = node.Source()
	}

	return sources
}

// layerFiles merges a Source of each of inputs, in the order they apply
// and under the names [layerNodes] gives them.
func layerFiles(t *testing.T, inputs ...string) *niceyaml.Source {
	t.Helper()

	return mergedLayers(t, layerSources(t, inputs...))
}

// mergedLayers merges given through [yamltest.Layers], in the order they
// apply.
func mergedLayers[L niceyaml.Layer](t *testing.T, given []L) *niceyaml.Source {
	t.Helper()

	return yamltest.Layers(t, layersOf(given)...)
}

// layersDocument returns the root of the document that layers merge
// into through [yamltest.Layers].
func layersDocument(t *testing.T, layers ...niceyaml.Layer) *niceyaml.Node {
	t.Helper()

	return yamltest.Document(t, yamltest.Layers(t, layers...))
}

// layersOf returns given as a []niceyaml.Layer, which spreads into the
// variadic parameter of [yamltest.Layers]. Go spreads neither a
// []*niceyaml.Node nor a []*niceyaml.Source into it.
func layersOf[L niceyaml.Layer](given []L) []niceyaml.Layer {
	layers := make([]niceyaml.Layer, len(given))

	for i, layer := range given {
		layers[i] = layer
	}

	return layers
}

// layerServer is a server that needs a port once it names a host, so
// a server no layer sets passes.
type layerServer struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

func (s layerServer) Validate() error {
	if s.Host != "" && s.Port < 1 {
		return yamltest.ErrorAt("port must be at least 1", "port")
	}

	return nil
}

// layerNested holds a [layerServer] one struct down.
type layerNested struct {
	Inner layerServer `yaml:"inner"`
}

// layerLimits needs a timeout, which a null in a document sets to nil.
type layerLimits struct {
	Timeout *int `yaml:"timeout"`
}

func (l layerLimits) Validate() error {
	if l.Timeout == nil {
		return yamltest.ErrorAt("timeout is required", "timeout")
	}

	return nil
}

// layerRate is a rate that needs a limit.
type layerRate struct {
	Limit int `yaml:"limit"`
}

func (r layerRate) Validate() error {
	if r.Limit < 1 {
		return yamltest.ErrorAt("limit must be at least 1", "limit")
	}

	return nil
}

// layerSwitch decodes itself into a new value, so a second decode
// replaces it whole.
type layerSwitch struct {
	Mode string `yaml:"mode"`
	On   bool   `yaml:"on"`
}

func (s *layerSwitch) UnmarshalYAML(unmarshal func(any) error) error {
	type plain layerSwitch

	var decoded plain

	err := unmarshal(&decoded)
	if err != nil {
		return err
	}

	*s = layerSwitch(decoded)

	return nil
}

// LayerCommon holds the field [layerConfig] inlines.
type LayerCommon struct {
	Region string `yaml:"region"`
}

func (c LayerCommon) Validate() error {
	if c.Region == "nowhere" {
		return yamltest.ErrorAt("unknown region", "region")
	}

	return nil
}

// layerConfig holds a field of each kind that a second decode into
// one value merges or replaces. The go-yaml decoder fills the fields in
// the order the struct declares them, and the alias case of
// [TestNewSourceFromLayers_SelfValidate] needs Backup to decode before
// Server.
type layerConfig struct {
	LayerCommon `yaml:",inline"`

	Extra    any                    `yaml:"extra"`
	Backends map[string]layerServer `yaml:"backends"`
	Rates    map[float64]layerRate  `yaml:"rates"`
	Backup   *layerServer           `yaml:"backup"`
	Limits   *layerLimits           `yaml:"limits"`
	Name     string                 `yaml:"name"`
	Servers  []layerServer          `yaml:"servers"`
	Nested   layerNested            `yaml:"nested"`
	Server   layerServer            `yaml:"server"`
	Switch   layerSwitch            `yaml:"switch"`
	Pair     [2]layerServer         `yaml:"pair"`

	// The paths Validate reports, which a test sets after the decodes.
	odd []paths.Path
}

// Validate reports an error at each path in odd under one summary, as a
// parent that checks the values below it does.
func (c layerConfig) Validate() error {
	if len(c.odd) == 0 {
		return nil
	}

	errs := make([]error, 0, len(c.odd))
	for _, path := range c.odd {
		errs = append(errs, niceyaml.NewError("odd value", niceyaml.AtPath(path)))
	}

	return niceyaml.NewSummary("config checks", errs...)
}
