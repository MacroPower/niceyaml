package edit_test

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/edit"
	"go.jacobcolvin.com/niceyaml/paths"
)

func TestNode_Set(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		value  any
		before string
		path   paths.Path
		// A second Set gives the key thenKey below path the value
		// thenValue, when thenKey is set.
		thenKey   string
		thenValue any
		want      string
		err       string
		// The text of a reference document of the document, or "".
		ref string
		// Whether the Source allows duplicate keys.
		dup bool
		// Whether err matches edit.ErrEngine.
		engine bool
		// The value of the panic of the Set, which the Apply passes on, or
		// nil.
		panics any
	}{
		"string of several lines at the end of a document before a comment line deeper than its text": {
			// The comment line between the document and the next "---" belongs
			// to b, as at the end of a file, so the new entry goes after it.
			before: stringtest.LinesLF("a:", "  b: 1", "    # d", "---", "y: 3"),
			path:   paths.MustParse("$.a.c"),
			value:  "x\n",
			want:   stringtest.LinesLF("a:", "  b: 1", "    # d", "  c: |", "    x"),
		},
		"string of several lines in place of the last value of a document before a comment line deeper than its text": {
			// The lines of the block scalar go one space past the comment line,
			// as at the end of a file.
			before: stringtest.LinesLF("a: 1", "    # d", "---", "y: 3"),
			path:   paths.Doc().Child("a"),
			value:  "x\n",
			want:   stringtest.LinesLF("a: |", "     x", "    # d"),
		},
		"string with blank lines at the end of a document before a later document": {
			// The block scalar takes the place of the blank lines.
			before: stringtest.LinesLF("a: 1", "", "", "---", "y: 3"),
			path:   paths.Doc().Child("b"),
			value:  "q\n\n",
			want:   stringtest.LinesLF("a: 1", "b: |+", "  q", ""),
		},
		"string with a line of spaces": {
			// A block scalar reads the spaces of a line of spaces alone past
			// its indent, so the line keeps them.
			before: "a: 1\n",
			path:   paths.Doc().Child("a"),
			value:  "a\n \nb\n",
			want:   stringtest.LinesLF("a: |", "  a", "   ", "  b"),
		},
		"string with a line of spaces in a new entry": {
			before: "a: 1\n",
			path:   paths.Doc().Child("b"),
			value:  "a\n \nb\n",
			want:   stringtest.LinesLF("a: 1", "b: |", "  a", "   ", "  b"),
		},
		"string with a line of spaces in a nested mapping": {
			before: stringtest.LinesLF("m:", "  a: 1"),
			path:   paths.MustParse("$.m.a"),
			value:  "a\n \nb\n",
			want:   stringtest.LinesLF("m:", "  a: |", "    a", "     ", "    b"),
		},
		"string with a line of spaces in an item": {
			before: "- 1\n",
			path:   paths.Doc().Index(0),
			value:  "a\n \nb\n",
			want:   stringtest.LinesLF("- |", "  a", "   ", "  b"),
		},
		"string with a line of spaces before the blank lines that it keeps": {
			before: "a: 1\n",
			path:   paths.Doc().Child("a"),
			value:  "x\n \n\n",
			want:   stringtest.LinesLF("a: |+", "  x", "   ", ""),
		},
		"string with a line of spaces in a text of carriage returns and line feeds": {
			before: "a: 1\r\nb: 2\r\n",
			path:   paths.Doc().Child("a"),
			value:  "a\n \nb\n",
			want:   "a: |\r\n  a\r\n   \r\n  b\r\nb: 2\r\n",
		},
		"string with a line of spaces before a comment line that its lines go past": {
			// The lines go one space past the comment line, the line of spaces
			// too, so the comment line stays out of the scalar.
			before: stringtest.LinesLF("a: 1", "   # d", "b: 2"),
			path:   paths.Doc().Child("a"),
			value:  "a\n \nb\n",
			want:   stringtest.LinesLF("a: |", "    a", "     ", "    b", "   # d", "b: 2"),
		},
		"value of a key without a value in a flow mapping": {
			// Control: the value goes after a ":" that the Set writes right
			// after the key.
			before: "{a, b: 1}\n",
			path:   paths.Doc().Child("a"),
			value:  "v",
			want:   "{a: v, b: 1}\n",
		},
		"entry after a block scalar key without a value": {
			before: stringtest.LinesLF("? |", "  k"),
			path:   paths.Doc().Child("z"),
			value:  1,
			err:    "test.yaml:1:1: $.'k\\n': cafe.jacobcolvin.com/v1 sets $.z, which reaches an entry with an explicit key, and cafe does not edit such an entry other than by removing it",
		},
		"key that a lookup through a merge key finds in an entry with an explicit key": {
			// The lookup of p meets the merge key and the explicit key at one
			// step, and the explicit key comes first in the text.
			before: stringtest.LinesLF("p0: &x0", "  ? p", "  : {k: 1}", "<<: [*x0, *x0]"),
			path:   paths.MustParse("$.p.z"),
			value:  1,
			err:    "test.yaml:2:3: $.p0.p: cafe.jacobcolvin.com/v1 sets $.p.z, which reaches an entry with an explicit key",
		},
		"key below a scalar that an alias of an explicit key reads from the value of a merge key": {
			// The path fails at the scalar 2. The step of r3 meets the explicit
			// key and the value of the merge key through the alias, and the
			// merge key comes first in the text.
			before: stringtest.LinesLF("<<: &x0 {b: 2}", "? r3", ": *x0"),
			path:   paths.MustParse("$.r3.b.z"),
			value:  1,
			err:    "test.yaml:1:1: $: cafe.jacobcolvin.com/v1 sets $.r3.b.z, which goes through a merge key whose value has an anchor",
		},
		"key below an explicit key that a later step looks up through a merge key": {
			// Control: the step of r1 meets the explicit key before the step
			// of c looks c up through the merge key.
			before: stringtest.LinesLF("r0: &x0", "  <<: {c: {a: 2}}", "? r1", ": *x0"),
			path:   paths.MustParse("$.r1.c.z"),
			value:  1,
			err:    "test.yaml:3:1: $.r1: cafe.jacobcolvin.com/v1 sets $.r1.c.z, which reaches an entry with an explicit key",
		},
		"mapping in place of a mapping under a << key with a tab after its colon": {
			// Go-yaml reads the << key before ":" and a tab as a string key,
			// and the tab stays.
			before: stringtest.LinesLF("<<:\t", "  x: q"),
			path:   paths.MustParse("$.<<"),
			value:  map[string]int{"z": 1},
			want:   stringtest.LinesLF("<<:\t", "  z: 1"),
		},
		"scalar in place of a mapping under a << key with a tab after its colon": {
			before: stringtest.LinesLF("<<:\t", "  x: q"),
			path:   paths.MustParse("$.<<"),
			value:  5,
			want:   stringtest.LinesLF("<<:\t5"),
		},
		"scalar in place of an empty value of a << key with a tab after its colon": {
			before: stringtest.LinesLF("<<:\t", "b: 1"),
			path:   paths.MustParse("$.<<"),
			value:  5,
			want:   stringtest.LinesLF("<<:\t5", "b: 1"),
		},
		"scalar in place of an empty value with a tab and a comment after its colon": {
			// The value goes after the tab, and a space parts it from the
			// comment.
			before: stringtest.LinesLF("k:\t# c", "z: 1"),
			path:   paths.Doc().Child("k"),
			value:  5,
			want:   stringtest.LinesLF("k:\t5 # c", "z: 1"),
		},
		"string in place of an empty value with a tab and a comment after its colon": {
			before: stringtest.LinesLF("k:\t# c", "z: 1"),
			path:   paths.Doc().Child("k"),
			value:  "x",
			want:   stringtest.LinesLF("k:\tx # c", "z: 1"),
		},
		"flow sequence in place of an empty value with a tab and a comment after its colon": {
			before: stringtest.LinesLF("k:\t# c", "z: 1"),
			path:   paths.Doc().Child("k"),
			value:  []int{1, 2},
			want:   stringtest.LinesLF("k:\t[1, 2] # c", "z: 1"),
		},
		"scalar in place of an empty item with a tab and a comment after its dash": {
			before: stringtest.LinesLF("-\t# c", "- 2"),
			path:   paths.Doc().Index(0),
			value:  1,
			want:   stringtest.LinesLF("-\t1 # c", "- 2"),
		},
		"scalar in place of an empty value with two tabs and a comment after its colon": {
			// A control: the second tab parts the value from the comment.
			before: stringtest.LinesLF("k:\t\t# c", "z: 1"),
			path:   paths.Doc().Child("k"),
			value:  5,
			want:   stringtest.LinesLF("k:\t5\t# c", "z: 1"),
		},
		"new key after a tag alone at the end of a mapping": {
			// Go-yaml reads the new line as the value of the tag, and the
			// message names that value, not a tag. The Set left the value in
			// its place, so the problem binds at the tag, where an error at
			// the value binds in the text that the Set found (fix24 G24-4).
			before: stringtest.LinesLF("a: !t"),
			path:   paths.Doc().Child("b"),
			value:  9,
			err:    `1:4: $.a: go-yaml reads $.a as {"b": 9}, not null, after cafe.jacobcolvin.com/v1 sets $.b`,
			engine: true,
		},
		"new key after a tag alone at the end of a mapping in a mapping": {
			before: stringtest.LinesLF("m:", "  x:", "    a: !GetAZs"),
			path:   paths.MustParse("$.m.nk"),
			value:  9,
			err: `3:8: $.m.x.a: go-yaml reads $.m.x.a as {"nk": 9}, not null, after cafe.jacobcolvin.com/v1 ` +
				"sets $.m.nk",
			engine: true,
		},
		"new key after a block scalar that ends with a line of spaces, in a text without a final newline": {
			// The new key gives the line of spaces a line break, which changes
			// the string of the scalar.
			before: stringtest.JoinLF("a: |+", "  x", "", "   "),
			path:   paths.Doc().Child("b"),
			value:  1,
			err:    "test.yaml:1:4: $.a: cafe.jacobcolvin.com/v1 adds lines after this block scalar, which ends the text without a line break, so it changes the string of the scalar, when it sets $.b",
		},
		"literal scalar with \"\\r\\n\" lines that a marshaler writes": {
			// The encoder of go-yaml writes the header with a "\n" and keeps
			// the "\r\n" lines, so the Set fails loudly.
			before: "a: 1\r\n",
			path:   paths.Doc().Child("a"),
			value:  yamlMarshaler("|\r\n  p\r\n  q\r\n"),
			err: `1:4: $.a: go-yaml's encoder writes the value with both "\n" and "\r\n" line breaks, ` +
				"though the YAML of each marshaler of the value writes one kind",
			engine: true,
		},
		"folded scalar with \"\\r\\n\" lines that a marshaler writes in a mapping": {
			before: "a: 1\n",
			path:   paths.Doc().Child("a"),
			value:  yamlMarshaler("x: >\r\n  p\r\n  q\r\n"),
			err: `1:4: $.a: go-yaml's encoder writes the value with both "\n" and "\r\n" line breaks, ` +
				"though the YAML of each marshaler of the value writes one kind",
			engine: true,
		},
		"Go string that ends with two line breaks": {
			// A control: the encoder writes the block scalar with keep
			// chomping, and the Set keeps it.
			before: "a: 1\n",
			path:   paths.Doc().Child("z"),
			value:  "p\n\n",
			want:   stringtest.LinesLF("a: 1", "z: |+", "  p", ""),
		},
		"Go string that YAML reads as a float without quotes": {
			// Go-yaml reads 1e3 as a string, and YAML reads it as a float.
			before: "a: 1\n",
			path:   paths.Doc().Child("z"),
			value:  "1e3",
			want:   stringtest.LinesLF("a: 1", `z: "1e3"`),
		},
		"Go string that YAML reads as an infinity without quotes": {
			before: "a: 1\n",
			path:   paths.Doc().Child("z"),
			value:  "+.inf",
			want:   stringtest.LinesLF("a: 1", `z: "+.inf"`),
		},
		"Go string of a hexadecimal number in capitals": {
			// A control: YAML reads 0X1F as a string, as go-yaml does.
			before: "a: 1\n",
			path:   paths.Doc().Child("z"),
			value:  "0X1F",
			want:   stringtest.LinesLF("a: 1", "z: 0X1F"),
		},
		"Go string key that YAML reads as a float without quotes": {
			before: "a: 1\n",
			path:   paths.Doc().Child("z"),
			value:  map[string]int{"-1E+3": 1},
			want:   stringtest.LinesLF("a: 1", "z:", `  "-1E+3": 1`),
		},
		"block scalar of the user whose last line ends with a space under strip chomping": {
			// The spaces at the end of the last line belong to the scalar, so
			// the new value replaces them.
			before: stringtest.LinesLF("a: |-", "  x ", "b: 1"),
			path:   paths.Doc().Child("a"),
			value:  "z",
			want:   stringtest.LinesLF("a: z", "b: 1"),
		},
		"block scalar of the user with a comment on its header whose last line ends with a space": {
			before: stringtest.LinesLF("a: |-   # c", "  x ", "b: 1"),
			path:   paths.Doc().Child("a"),
			value:  "z",
			want:   stringtest.LinesLF("a: z # c", "b: 1"),
		},
		"mapping with \"\\r\\n\" lines that a marshaler writes": {
			// Control: the encoder writes the mapping in flow style.
			before: "a: 1\r\n",
			path:   paths.Doc().Child("a"),
			value:  yamlMarshaler("x: 1\r\ny: 2\r\n"),
			want:   "a: {x: 1, y: 2}\r\n",
		},
		"float that go-yaml's encoder writes in an exponent form": {
			// The encoder of go-yaml writes 1e+06, which its decoder reads as
			// a string, so the Set fails loudly.
			before: "replicas: 3\n",
			path:   paths.Doc().Child("replicas"),
			value:  1000000.0,
			err: "1:11: $.replicas: go-yaml's encoder writes the float 1e+06 " +
				`as text that go-yaml's decoder reads as "1e+06"`,
			engine: true,
		},
		"map with an array key, which go-yaml's encoder writes as a string": {
			// The encoder writes the key as the string that fmt prints for
			// it, which does not read back as the array.
			before: "v: 1\n",
			path:   paths.Doc().Child("v"),
			value:  map[[2]int]string{{1, 2}: "x"},
			err:    `1:4: $.v: go-yaml's encoder writes a key of type [2]int as the string "[1 2]"`,
			engine: true,
		},
		"map with a struct key, which go-yaml's encoder writes as a string": {
			before: "v: 1\n",
			path:   paths.Doc().Child("v"),
			value:  map[struct{ X, Y int }]string{{1, 2}: "x"},
			err:    `1:4: $.v: go-yaml's encoder writes a key of type struct { X int; Y int } as the string "{1 2}"`,
			engine: true,
		},
		"map with an array key inside a slice": {
			before: "v: 1\n",
			path:   paths.Doc().Child("v"),
			value:  []any{1, map[[1]string]int{{"a"}: 1}},
			err:    `1:4: $.v: go-yaml's encoder writes a key of type [1]string as the string "[a]"`,
			engine: true,
		},
		"map of any keys with an array key": {
			before: "v: 1\n",
			path:   paths.Doc().Child("v"),
			value:  map[any]int{"a": 1, [2]int{1, 2}: 2},
			err:    `1:4: $.v: go-yaml's encoder writes a key of type [2]int as the string "[1 2]"`,
			engine: true,
		},
		"map with a key whose MarshalYAML fails": {
			// Go-yaml's encoder writes the string that fmt prints for the key
			// in place of the error, so the Set fails with the error, as for
			// a value whose marshaler fails.
			before: "v: 1\n",
			path:   paths.Doc().Child("v"),
			value:  map[failingMarshaler]int{1: 1},
			err: "1:4: $.v: cafe.jacobcolvin.com/v1 cannot write the value of $.v: encode a key of type " +
				"edit_test.failingMarshaler: the marshaler fails",
		},
		"map with a key whose MarshalText writes a string": {
			// Control: the encoder writes the text of the key.
			before: "v: 1\n",
			path:   paths.Doc().Child("v"),
			value:  map[textMarshaler]int{"k": 1},
			want:   "v: {k: 1}\n",
		},
		"small float that go-yaml's encoder writes in an exponent form": {
			before: "replicas: 3\n",
			path:   paths.Doc().Child("replicas"),
			value:  1e-5,
			err: "1:11: $.replicas: go-yaml's encoder writes the float 1e-05 " +
				`as text that go-yaml's decoder reads as "1e-05"`,
			engine: true,
		},
		"large float in a new entry that go-yaml's encoder writes in an exponent form": {
			// The call creates the place, so the problem binds at the nearest value above it that the user
			// wrote, under its path (fix19 G1-1).
			before: "replicas: 3\n",
			path:   paths.MustParse("$.scale.max"),
			value:  map[string]any{"n": 1e20},
			err: "1:1: $: go-yaml's encoder writes the float 1e+20 " +
				`as text that go-yaml's decoder reads as "1e+20"`,
			engine: true,
		},
		"mapping with a key that holds a colon in place of a scalar": {
			// The value takes flow style, and go-yaml's flow encoder quotes
			// the key, which go-yaml reads as a key and a value otherwise.
			before: "a: 1\n",
			path:   paths.Doc().Child("a"),
			value:  map[string]any{"x:y": 1.0},
			want:   "a: {\"x:y\": 1.0}\n",
		},
		"mapping with a key that holds a colon as a new item of a flow sequence": {
			before: "s: [1]\n",
			path:   paths.MustParse("$.s[1]"),
			value:  map[string]any{"x:y": 1.0},
			want:   "s: [1, {\"x:y\": 1.0}]\n",
		},
		"byte slice, which go-yaml's encoder writes as a sequence": {
			before: "a: 1\n",
			path:   paths.Doc().Child("a"),
			value:  []byte("hi"),
			want:   "a: [104, 105]\n",
		},
		"time in a new entry": {
			// Go-yaml's encoder writes a time.Time as a plain timestamp.
			before: "a: 1\n",
			path:   paths.Doc().Child("x"),
			value:  time.Date(2001, 12, 14, 21, 59, 43, 1e8, time.UTC),
			want:   "a: 1\nx: 2001-12-14T21:59:43.1Z\n",
		},
		"time in a new entry of a flow mapping": {
			// A time.Time is not a Go string, so its flow text is the text of
			// the encoder too.
			before: "a: {b: 1}\n",
			path:   paths.MustParse("$.a.x"),
			value:  time.Date(2001, 12, 14, 21, 59, 43, 1e8, time.UTC),
			want:   "a: {b: 1, x: 2001-12-14T21:59:43.1Z}\n",
		},
		"mapping of a time and a string in a new item of a flow sequence": {
			// The string takes the text of go-yaml's flow encoder, and the time
			// keeps the text of the encoder.
			before: "a: [1]\n",
			path:   paths.MustParse("$.a[1]"),
			value:  map[string]any{"t": time.Date(2001, 12, 14, 21, 59, 43, 1e8, time.UTC), "s": "x:y"},
			want:   "a: [1, {s: \"x:y\", t: 2001-12-14T21:59:43.1Z}]\n",
		},
		"string that reads as a time": {
			// Go-yaml's encoder quotes a Go string that a decode would read as
			// a timestamp.
			before: "a: {b: 1}\n",
			path:   paths.MustParse("$.a.x"),
			value:  "2001-12-14T21:59:43.1Z",
			want:   "a: {b: 1, x: \"2001-12-14T21:59:43.1Z\"}\n",
		},
		"map that holds itself": {
			// Go-yaml's encoder would write the map without end, so the Set
			// fails before the encoder runs.
			before: stringtest.LinesLF("a: 1", "b: x # c"),
			path:   paths.Doc().Child("b"),
			value:  selfMap(),
			err:    "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 cannot write the value of $.b: it refers to itself",
		},
		"slice that holds itself": {
			before: stringtest.LinesLF("a: 1", "b: x # c"),
			path:   paths.Doc().Child("b"),
			value:  selfSlice(),
			err:    "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 cannot write the value of $.b: it refers to itself",
		},
		"pointer that holds itself": {
			before: stringtest.LinesLF("a: 1", "b: x # c"),
			path:   paths.Doc().Child("b"),
			value:  selfPointer(),
			err:    "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 cannot write the value of $.b: it refers to itself",
		},
		"struct that holds itself through a pointer field": {
			before: stringtest.LinesLF("a: 1", "b: x # c"),
			path:   paths.Doc().Child("b"),
			value:  selfStruct(),
			err:    "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 cannot write the value of $.b: it refers to itself",
		},
		"new value that holds itself": {
			before: "a: {k: 1}\n",
			path:   paths.MustParse("$.a.z.y"),
			value:  selfMap(),
			err:    "test.yaml:1:1: $.a: cafe.jacobcolvin.com/v1 cannot write the value of $.a.z.y: it refers to itself",
		},
		"marshaler that returns a map that holds itself": {
			// The check calls the marshaler and reads the map that it returns,
			// which go-yaml's encoder would write without end.
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  valueMarshaler{selfMap()},
			err:    "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 cannot write the value of $.b: it refers to itself",
		},
		"context marshaler that returns a map that holds itself": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  contextMarshaler{selfMap()},
			err:    "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 cannot write the value of $.b: it refers to itself",
		},
		"marshaler that returns its own value": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  selfMarshaler(1),
			err:    "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 cannot write the value of $.b: it refers to itself",
		},
		"marshaler that returns a new pointer to an equal value": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  &copyMarshaler{n: 1},
			err:    "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 cannot write the value of $.b: it refers to itself",
		},
		"marshalers that return values without end": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  countMarshaler(-1),
			err: "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 cannot write the value of $.b: the MarshalYAML methods of " +
				"edit_test.countMarshaler return values with marshalers more than 1,000 levels deep",
		},
		"chain of 1,000 marshalers": {
			// A control: the chain of the limit passes.
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  countMarshaler(999),
			want:   stringtest.LinesLF("a: 1", "b: end"),
		},
		"chain of 1,001 marshalers": {
			// The chain holds no cycle, so the message names the limit.
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  countMarshaler(1000),
			err: "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 cannot write the value of $.b: the MarshalYAML methods of " +
				"edit_test.countMarshaler return values with marshalers more than 1,000 levels deep",
		},
		"marshalers that return values to an end": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  countMarshaler(3),
			want:   stringtest.LinesLF("a: 1", "b: end"),
		},
		"marshaler that returns a value with a marshaler": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  valueMarshaler{[]any{1, valueMarshaler{"x"}}},
			want:   stringtest.LinesLF("a: 1", "b: [1, x]"),
		},
		"nil pointer with a marshaler": {
			// Go-yaml's encoder writes a nil pointer as null without a call of
			// its marshaler.
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  (*copyMarshaler)(nil),
			want:   stringtest.LinesLF("a: 1", "b: null"),
		},
		"YAML marshaler": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("{x: 1}"),
			want:   stringtest.LinesLF("a: 1", "b: {x: 1}"),
		},
		"context YAML marshaler": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  contextYAMLMarshaler("[1, 2]"),
			want:   stringtest.LinesLF("a: 1", "b: [1, 2]"),
		},
		"YAML marshaler that writes an anchor and its alias": {
			// The finder of the YAML of the value finds the anchor first.
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("{x: &x 1, y: *x}"),
			err:    "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 sets $.b to a value whose YAML holds the anchor &x, and cafe does not write anchors or aliases",
		},
		"text marshaler": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  textMarshaler("*zz"),
			want:   stringtest.LinesLF("a: 1", `b: "*zz"`),
		},
		"YAML marshaler that writes an alias of an anchor that its text lacks": {
			// The program wrote the alias, so the problem is not the document's.
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("*zz"),
			err:    "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 sets $.b to a value whose YAML holds the alias *zz, and cafe does not write anchors or aliases",
		},
		"context YAML marshaler that writes an alias of an anchor that its text lacks": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  contextYAMLMarshaler("[1, *zz]"),
			err:    "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 sets $.b to a value whose YAML holds the alias *zz, and cafe does not write anchors or aliases",
		},
		"YAML marshaler that writes text that go-yaml's parser refuses": {
			// The refusal is go-yaml's, whether YAML allows the text or not.
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("a: ["),
			err: "2:4: $.b: go-yaml's parser refuses the YAML that the MarshalYAML method of " +
				"edit_test.yamlMarshaler writes: sequence end token ']' not found at 1:4 of that YAML",
			engine: true,
		},
		"YAML marshaler that writes an explicit collection key": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("? [a]\n: 1\n"),
			err: "2:4: $.b: go-yaml's parser refuses the YAML that the MarshalYAML method of " +
				"edit_test.yamlMarshaler writes: found an invalid key for this map at 2:1 of that YAML",
			engine: true,
		},
		"YAML marshaler that writes a flow collection key": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("[a]: 1\n"),
			err: "2:4: $.b: go-yaml's parser refuses the YAML that the MarshalYAML method of " +
				"edit_test.yamlMarshaler writes: found an invalid key for this map at 1:4 of that YAML",
			engine: true,
		},
		"YAML marshaler that writes an anchored and tagged key with a block scalar in a nested mapping": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("a:\n  &k !!str m: |\n    q\n"),
			err: "2:4: $.b: go-yaml's parser refuses the YAML that the MarshalYAML method of " +
				"edit_test.yamlMarshaler writes: value is not allowed in this context. " +
				"map key-value is pre-defined at 3:5 of that YAML",
			engine: true,
		},
		"YAML marshaler that writes an anchored key with a block scalar in a nested mapping": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("a:\n  &k m: |\n    q\n"),
			err: "2:4: $.b: go-yaml's parser refuses the YAML that the MarshalYAML method of " +
				"edit_test.yamlMarshaler writes: value is not allowed in this context. " +
				"map key-value is pre-defined at 3:5 of that YAML",
			engine: true,
		},
		"YAML marshaler that writes a mapping that repeats a key": {
			// YAML does not allow the text, so the problem is the marshaler's.
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("a: 1\na: 2\n"),
			err: "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 cannot write the value of $.b: the MarshalYAML method of " +
				"edit_test.yamlMarshaler writes a mapping that repeats the key a, which YAML does not allow",
		},
		"YAML marshaler that writes a float key and a string key of one text": {
			// Control: go-yaml's decoder reads the keys apart, but its parser
			// compares them by their text.
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("1.0: a\n\"1.0\": b\n"),
			err: "2:4: $.b: go-yaml's parser refuses the YAML that the MarshalYAML method of " +
				`edit_test.yamlMarshaler writes: mapping key "1.0" already defined`,
			engine: true,
		},
		"YAML marshaler that writes an alias key": {
			// Go-yaml's encoder writes "*a:", which its parser reads as an
			// alias named "a:". The anchor in the YAML of the marshaler explains
			// that problem, so the Set fails with the anchor.
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("n:\n  x: &a k\n  *a : 1\n  z: 2\n"),
			err:    "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 sets $.b to a value whose YAML holds the anchor &a, and cafe does not write anchors or aliases",
		},
		"YAML marshaler that writes a tagged alias inside its own anchor": {
			// Go-yaml's decoder reads a value without end there, which the merge
			// key of the YAML of the marshaler explains.
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("k0:\n  <<: &a !!str *a\n"),
			err:    "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 sets $.b to a value whose YAML holds a merge key, and cafe does not write merge keys",
		},
		"YAML marshaler that writes an alias key in a flow mapping": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("{&k a: 1, *k : 2}"),
			err:    "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 sets $.b to a value whose YAML holds the anchor &k, and cafe does not write anchors or aliases",
		},
		"YAML marshaler that writes a byte order mark before a key": {
			// Go-yaml reads the mark as text of the key.
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("\ufeffk: 1\n"),
			err: "2:4: $.b: go-yaml misreads the YAML that the MarshalYAML method of edit_test.yamlMarshaler " +
				"writes: in that YAML, go-yaml reads the byte order mark at 1:1 as text of the value",
			engine: true,
		},
		"YAML marshaler that writes a byte order mark after a comment line": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("# c\n\ufeffhello\n"),
			err: "2:4: $.b: go-yaml misreads the YAML that the MarshalYAML method of edit_test.yamlMarshaler " +
				"writes: in that YAML, go-yaml reads the byte order mark at 2:1 as text of the value",
			engine: true,
		},
		"YAML marshaler that writes a block scalar with keep chomping at its end": {
			// Go-yaml's encoder drops the blank line that the string holds, and
			// the message names the scalar at its place in the YAML of the
			// marshaler.
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("a: |+\n  x\n\n"),
			err: "2:4: $.b: go-yaml reads the block scalar of \"x\\n\\n\" at 1:4 of the YAML that a marshaler " +
				"of the value returns, but go-yaml's encoder writes that scalar as \"|+\\n  x\", which go-yaml's " +
				"decoder reads as \"x\\n\"",
			engine: true,
		},
		"YAML marshaler that writes a tagged flow sequence": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("!t [1]"),
			want:   stringtest.LinesLF("a: 1", "b: !t [1]"),
		},
		"YAML marshaler that writes a tagged flow mapping": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("!t {a: 1}"),
			want:   stringtest.LinesLF("a: 1", "b: !t {a: 1}"),
		},
		"YAML marshaler that writes a flow mapping under the map tag": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("!!map {a: 1}"),
			want:   stringtest.LinesLF("a: 1", "b: !!map {a: 1}"),
		},
		"YAML marshaler that writes a flow sequence under the seq tag": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("!!seq [1]"),
			want:   stringtest.LinesLF("a: 1", "b: !!seq [1]"),
		},
		"YAML marshaler that writes a tagged flow sequence over a tagged value": {
			before: stringtest.LinesLF("a: 1", "b: !o [2]"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("!t [1]"),
			want:   stringtest.LinesLF("a: 1", "b: !t [1]"),
		},
		"YAML marshaler that writes a tagged flow sequence in a new entry": {
			before: "a: 1\n",
			path:   paths.MustParse("$.x"),
			value:  yamlMarshaler("!t [1]"),
			want:   stringtest.LinesLF("a: 1", "x: !t [1]"),
		},
		"YAML marshaler that writes a tagged flow sequence below a new entry": {
			before: "a: 1\n",
			path:   paths.MustParse("$.x.z"),
			value:  yamlMarshaler("!t [1]"),
			want:   stringtest.LinesLF("a: 1", "x:", "  z: !t [1]"),
		},
		"YAML marshaler that writes a tagged flow sequence in a new item": {
			before: stringtest.LinesLF("- 1", "- 2"),
			path:   paths.MustParse("$[2]"),
			value:  yamlMarshaler("!t [1]"),
			want:   stringtest.LinesLF("- 1", "- 2", "- !t [1]"),
		},
		"YAML marshaler that writes a tagged flow sequence in a flow mapping": {
			before: "{a: 1, b: 2}\n",
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("!t [1]"),
			want:   "{a: 1, b: !t [1]}\n",
		},
		"YAML marshaler that writes a tag inside a flow mapping": {
			// A control: the tag below the root of the value passes.
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("{a: !t [1]}"),
			want:   stringtest.LinesLF("a: 1", "b: {a: !t [1]}"),
		},
		"YAML marshaler that writes a tagged scalar": {
			// A control: a tag on a scalar decodes with the scalar.
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("!!str 'x'"),
			want:   stringtest.LinesLF("a: 1", "b: !!str 'x'"),
		},
		"YAML marshaler that writes a core tag alone before a line break": {
			// The YAML holds an empty string under the tag, which the engine
			// writes in quotes, as go-yaml and YAML read it.
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("!!str\n"),
			want:   stringtest.LinesLF("a: 1", "b: !!str ''"),
		},
		"YAML marshaler that writes a core tag on a plain scalar": {
			// Go-yaml's decoder reads the string 1 under the tag, as YAML does.
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("!!str 1"),
			want:   stringtest.LinesLF("a: 1", "b: !!str 1"),
		},
		"YAML marshaler that writes a local tag before a comment and a number": {
			// Go-yaml reads the integer 1 from the YAML, but the encoder writes
			// "!t 1", which go-yaml reads as the string "1".
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("!t # c\n1\n"),
			err: `2:4: $.b: go-yaml reads the integer 1 at 2:1 of the YAML that a marshaler of the value returns, ` +
				`but go-yaml's encoder writes that scalar as "!t 1", which go-yaml's decoder reads as "1"` + "\n" +
				"|-- write the scalar as \"!t 1\" in the YAML of the marshaler, so that go-yaml reads it as it reads " +
				"the text that the encoder writes\n`-- cafe.jacobcolvin.com/v1 sets $.b from this value",
			engine: true,
		},
		"YAML marshaler that writes an anchor alone on the line before a block mapping": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("&z\na: 1"),
			err:    "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 sets $.b to a value whose YAML holds the anchor &z, and cafe does not write anchors or aliases",
		},
		"YAML marshaler that writes a tag alone on the line before a block mapping": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("!t\na: 1"),
			want:   stringtest.LinesLF("a: 1", "b: !t", "  a: 1"),
		},
		"YAML marshaler that writes a tag alone on the line before a block sequence, as an item of a sequence at the column of its key": {
			// YAML nests the sequence below the "-" only past its column, though
			// the sequence of l starts at the column of its key.
			before: stringtest.LinesLF("l:", "- x"),
			path:   paths.MustParse("$.l[0]"),
			value:  yamlMarshaler("!t\n- 1\n- 2"),
			want:   stringtest.LinesLF("l:", "- !t", "  - 1", "  - 2"),
		},
		"YAML marshaler that writes a tag alone on the line before a block mapping with a tagged alias": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("!tt\na: &a !t [1]\nc: [x, !u *a]"),
			err:    "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 sets $.b to a value whose YAML holds the anchor &a, and cafe does not write anchors or aliases",
		},
		"YAML marshaler in a map that writes a tagged alias below a tag": {
			// The anchor before the tagged alias comes first.
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  map[string]any{"k": yamlMarshaler("!tt\na: &a !t [1]\nc: [x, !u *a]")},
			err:    "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 sets $.b to a value whose YAML holds the anchor &a, and cafe does not write anchors or aliases",
		},
		"YAML marshaler that writes an anchor and a tag alone on the line before a block mapping": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("&z !t\na: 1"),
			err:    "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 sets $.b to a value whose YAML holds the anchor &z, and cafe does not write anchors or aliases",
		},
		"YAML marshaler that writes an anchor alone on the line before a block sequence": {
			// The program wrote the anchor, so the Set does not write the
			// sequence below it.
			before: stringtest.LinesLF("s:", "- 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("&z\n- 1\n- 2"),
			err:    "test.yaml:3:4: $.b: cafe.jacobcolvin.com/v1 sets $.b to a value whose YAML holds the anchor &z, and cafe does not write anchors or aliases",
		},
		"YAML marshaler that writes a tag alone on the line before an indented block sequence": {
			before: stringtest.LinesLF("s:", "  - 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("!t\n- 1"),
			want:   stringtest.LinesLF("s:", "  - 1", "b: !t", "  - 1"),
		},
		"YAML marshaler that writes an anchor alone on the line before a block mapping in an item": {
			before: stringtest.LinesLF("- 1", "- 2"),
			path:   paths.MustParse("$[1]"),
			value:  yamlMarshaler("&z\na: 1\nc: 2"),
			err:    "test.yaml:2:3: $[1]: cafe.jacobcolvin.com/v1 sets $[1] to a value whose YAML holds the anchor &z, and cafe does not write anchors or aliases",
		},
		"YAML marshaler that writes an anchor alone on the line before a block mapping over a block mapping": {
			before: stringtest.LinesLF("a: 1", "b:", "  x: 1"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("&z\na: 1"),
			err:    "test.yaml:2:1: $.b: cafe.jacobcolvin.com/v1 sets $.b to a value whose YAML holds the anchor &z, and cafe does not write anchors or aliases",
		},
		"YAML marshaler that writes an anchor alone on the line before a block mapping with a block scalar": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("&z\na: |\n  x\nc: 1"),
			err:    "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 sets $.b to a value whose YAML holds the anchor &z, and cafe does not write anchors or aliases",
		},
		"YAML marshaler that writes an anchor alone on the line before a block mapping below a new entry": {
			// Go-yaml's encoder writes the mapping at the column of the new key,
			// which the anchor in the YAML of the marshaler explains.
			before: "a: 1\n",
			path:   paths.MustParse("$.x.y"),
			value:  yamlMarshaler("&z\na: 1"),
			err:    "test.yaml:1:1: $: cafe.jacobcolvin.com/v1 sets $.x.y to a value whose YAML holds the anchor &z, and cafe does not write anchors or aliases",
		},
		"YAML marshaler that writes an anchor alone on the line before an indented block mapping": {
			// The program wrote the anchor, so the Set does not write the
			// mapping below it.
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("&z\n  a: 1"),
			err:    "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 sets $.b to a value whose YAML holds the anchor &z, and cafe does not write anchors or aliases",
		},
		"YAML marshaler that panics": {
			// The check of the value calls the marshaler, so the panic is the
			// caller's.
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  panicYAMLMarshaler{},
			panics: "edit_test.panicYAMLMarshaler",
		},
		"context YAML marshaler that panics": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  panicContextYAMLMarshaler{},
			panics: "edit_test.panicContextYAMLMarshaler",
		},
		"marshaler that panics": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  panicMarshaler{},
			panics: "edit_test.panicMarshaler",
		},
		"context marshaler that panics": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  panicContextMarshaler{},
			panics: "edit_test.panicContextMarshaler",
		},
		"text marshaler that panics": {
			// A control: go-yaml's encoder alone calls the method, so the panic
			// is a panic of the encoder.
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  panicTextMarshaler{},
			err:    "2:4: $.b: go-yaml's encoder panicked on the value: edit_test.panicTextMarshaler",
			engine: true,
		},
		"YAML marshaler that writes two documents": {
			// Go-yaml's encoder would write the first document alone.
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("x: 1\n---\ny: 2"),
			err: "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 cannot write the value of $.b: " +
				"the MarshalYAML method of edit_test.yamlMarshaler writes 2 documents, but a value is one node of YAML",
		},
		"YAML marshaler that writes an anchor without a value": {
			// YAML allows the anchor, and go-yaml's parser refuses it.
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("&a"),
			err: "2:4: $.b: go-yaml's parser refuses the YAML that the MarshalYAML method of " +
				"edit_test.yamlMarshaler writes: undefined anchor value at 1:1 of that YAML",
			engine: true,
		},
		"YAML marshaler that writes an anchor without a value in a flow mapping": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("{a: &x, b: *x}"),
			err: "2:4: $.b: go-yaml's parser refuses the YAML that the MarshalYAML method of " +
				"edit_test.yamlMarshaler writes: unexpected scalar value type at 1:7 of that YAML",
			engine: true,
		},
		"YAML marshaler that writes a tag without a value": {
			// Go-yaml's encoder reads no value from the tag alone.
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("!t"),
			err: "2:4: $.b: go-yaml's encoder reads no value from the YAML that the MarshalYAML method of " +
				`edit_test.yamlMarshaler writes: "!t"`,
			engine: true,
		},
		"YAML marshaler that writes a %YAML directive": {
			// Go-yaml's encoder writes the directive alone for the YAML, though
			// go-yaml reads that YAML alone as x, so the message names the
			// directive in that YAML (fix19 P58).
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("%YAML 1.2\n---\nx\n"),
			err: "2:4: $.b: the YAML that the MarshalYAML method of edit_test.yamlMarshaler writes holds the " +
				"directive %YAML at 1:1 of that YAML before its value, and go-yaml's encoder writes only the " +
				"directive for that YAML\n|-- return the YAML of edit_test.yamlMarshaler without directives\n" +
				"`-- cafe.jacobcolvin.com/v1 sets $.b from this value",
			engine: true,
		},
		"YAML marshaler that writes a %TAG directive after a comment line": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("# c\n%TAG !e! tag:e,2000:\n---\n!e!a x\n"),
			err: "2:4: $.b: the YAML that the MarshalYAML method of edit_test.yamlMarshaler writes holds the " +
				"directive %TAG at 2:1 of that YAML before its value, and go-yaml's encoder writes only the " +
				"directive for that YAML\n|-- return the YAML of edit_test.yamlMarshaler without directives\n" +
				"`-- cafe.jacobcolvin.com/v1 sets $.b from this value",
			engine: true,
		},
		"YAML marshaler that writes a document start": {
			// Control.
			before: stringtest.LinesLF("a: 1", "b: 2"),
			path:   paths.Doc().Child("b"),
			value:  yamlMarshaler("--- x\n"),
			want:   stringtest.LinesLF("a: 1", "b: x"),
		},
		"map that holds one map at two places": {
			before: "a: 1\n",
			path:   paths.Doc().Child("a"),
			value:  sharedMap(),
			want:   "a: {p: {k: 1}, q: {k: 1}}\n",
		},
		"struct that the encoder writes without its field back to itself": {
			// The encoder skips the field that the tag names "-".
			before: "a: 1\n",
			path:   paths.Doc().Child("a"),
			value:  childStruct(),
			want:   "a: {name: c}\n",
		},
		"map with a nil key, on which go-yaml's encoder panics": {
			before: "a: 1\n",
			path:   paths.Doc().Child("a"),
			value:  map[any]any{nil: 1},
			// The engine wrote no text, so the error does not describe one.
			err:    "test.yaml:1:4: $.a: go-yaml's encoder panicked on the value: runtime error: ",
			engine: true,
		},
		"new mapping with a nil key, on which go-yaml's encoder panics": {
			// The call creates the place, so the problem binds at the nearest value above it that the user
			// wrote, under its path (fix19 G1-1).
			before: "a: 1\n",
			path:   paths.MustParse("$.b.c"),
			value:  map[any]any{nil: 1},
			err:    "1:1: $: go-yaml's encoder panicked on the value: runtime error: ",
			engine: true,
		},
		"map slice with a key that is not a string, on which go-yaml's encoder panics": {
			before: "a: 1\n",
			path:   paths.Doc().Child("a"),
			value:  yaml.MapSlice{{Key: 1, Value: 2}},
			err: "1:4: $.a: go-yaml's encoder panicked on the value: " +
				"interface conversion: interface {} is int, not string",
			engine: true,
		},
		"multi-line string in place of a value with spaces after it": {
			// The spaces would end the last line of the block scalar.
			before: stringtest.LinesLF("a: [] ", "b: 1"),
			path:   paths.Doc().Child("a"),
			value:  "x\n",
			want:   stringtest.LinesLF("a: |", "  x", "b: 1"),
		},
		"block mapping that ends with a block scalar with keep chomping, by a block scalar": {
			// The blank line after the old block scalar belongs to its string,
			// so the new value takes its place.
			before: stringtest.LinesLF("r0:", "  p: |+", "    q", "", "z: 1"),
			path:   paths.Doc().Child("r0"),
			value:  "w\n\n",
			want:   stringtest.LinesLF("r0: |+", "  w", "", "z: 1"),
		},
		"block mapping that ends with a block scalar with keep chomping, by a plain scalar": {
			before: stringtest.LinesLF("r0:", "  p: |+", "    q", "", "z: 1"),
			path:   paths.Doc().Child("r0"),
			value:  "w",
			want:   stringtest.LinesLF("r0: w", "z: 1"),
		},
		"value that holds two keys of one text": {
			// A decode reads the float key 1.0 as 1, which the finder of the
			// YAML of the value refuses before go-yaml's parser compares the two
			// keys by their text.
			before: "m: {}\n",
			path:   paths.MustParse("$.m.x"),
			value:  map[any]any{1.0: "a", "1.0": "b"},
			err:    "test.yaml:1:1: $.m: cafe.jacobcolvin.com/v1 sets $.m.x to a value whose YAML holds a key that reads as another value than its text, such as 1.0, which reads as 1, and cafe does not write such YAML",
		},
		"value that holds two keys of one text, with duplicate keys": {
			before: "m: {}\n",
			path:   paths.MustParse("$.m.x"),
			value:  map[any]any{1.0: "a", "1.0": "b"},
			dup:    true,
			err:    "test.yaml:1:1: $.m: cafe.jacobcolvin.com/v1 sets $.m.x to a value whose YAML holds a key that reads as another value than its text, such as 1.0, which reads as 1, and cafe does not write such YAML",
		},
		"string of several lines at an entry with a line comment": {
			// The comment goes on the line of the indicator, because the lines
			// after it hold the text of the block scalar.
			before: "a: x # note\n",
			path:   paths.Doc().Child("a"),
			value:  "line1\nline2",
			want:   stringtest.LinesLF("a: |- # note", "  line1", "  line2"),
		},
		"string with a line break at its end, at the end of a text without a final newline": {
			// The string of the block scalar ends with the line break of its
			// last line, so that line takes one.
			before: "a: x",
			path:   paths.Doc().Child("a"),
			value:  "x\n",
			want:   stringtest.LinesLF("a: |", "  x"),
		},
		"new string with a line break at its end, at the end of a text without a final newline": {
			before: "a: 1",
			path:   paths.Doc().Child("z"),
			value:  "y\n z\n",
			want:   stringtest.LinesLF("a: 1", "z: |", "  y", "   z"),
		},
		"string of several lines at an entry with a deeper comment line after it": {
			// A block scalar reads each line after it that indents as deep as
			// its lines, so its lines go past the comment line.
			before: stringtest.LinesLF("a: x", "  # note", "b: 1"),
			path:   paths.Doc().Child("a"),
			value:  "line1\nline2",
			want:   stringtest.LinesLF("a: |-", "   line1", "   line2", "  # note", "b: 1"),
		},
		"string of several lines at an entry with a line comment and a deeper comment line after it": {
			before: stringtest.LinesLF("a: x # note", "  # more", "b: 1"),
			path:   paths.Doc().Child("a"),
			value:  "line1\nline2",
			want:   stringtest.LinesLF("a: |- # note", "   line1", "   line2", "  # more", "b: 1"),
		},
		"string of several lines at a nested entry with a deeper comment line after it": {
			before: stringtest.LinesLF("m:", "  a: x", "    # note", "  b: 1"),
			path:   paths.MustParse("$.m.a"),
			value:  "line1\nline2",
			want:   stringtest.LinesLF("m:", "  a: |-", "     line1", "     line2", "    # note", "  b: 1"),
		},
		"string of several lines at an item with a deeper comment line after it": {
			before: stringtest.LinesLF("- x", "  # note", "- y"),
			path:   paths.MustParse("$[0]"),
			value:  "line1\nline2",
			want:   stringtest.LinesLF("- |-", "   line1", "   line2", "  # note", "- y"),
		},
		"string of several lines at the last entry with a deeper comment line after it": {
			before: stringtest.LinesLF("a: x", "  # note"),
			path:   paths.Doc().Child("a"),
			value:  "line1\nline2",
			want:   stringtest.LinesLF("a: |-", "   line1", "   line2", "  # note"),
		},
		"string with blank lines at its end at an entry with a deeper comment line after it": {
			before: stringtest.LinesLF("a: x", "  # note", "b: 1"),
			path:   paths.Doc().Child("a"),
			value:  "line1\nline2\n\n",
			want:   stringtest.LinesLF("a: |+", "   line1", "   line2", "", "  # note", "b: 1"),
		},
		"string of several lines at an entry with a blank line and a deeper comment line after it": {
			before: stringtest.LinesLF("a: x", "", "  # note", "b: 1"),
			path:   paths.Doc().Child("a"),
			value:  "line1\nline2",
			want:   stringtest.LinesLF("a: |-", "   line1", "   line2", "", "  # note", "b: 1"),
		},
		"new string of several lines after a deeper comment line of the entry before it": {
			// The comment line that the user indented past the key of a belongs
			// to a, so the new entry goes after it.
			before: stringtest.LinesLF("a: 1", "  # note"),
			path:   paths.Doc().Child("z"),
			value:  "line1\nline2",
			want:   stringtest.LinesLF("a: 1", "  # note", "z: |-", "  line1", "  line2"),
		},
		"new item of several lines after a deeper comment line of the item before it": {
			before: stringtest.LinesLF("- 1", "  # note"),
			path:   paths.MustParse("$[1]"),
			value:  "line1\nline2",
			want:   stringtest.LinesLF("- 1", "  # note", "- |-", "  line1", "  line2"),
		},
		"new entry after a deeper comment line of the entry before it": {
			before: stringtest.LinesLF("a: x", "  # about a"),
			path:   paths.Doc().Child("b"),
			value:  1,
			want:   stringtest.LinesLF("a: x", "  # about a", "b: 1"),
		},
		"new entry of a nested mapping after a deeper comment line of the entry before it": {
			before: stringtest.LinesLF("m:", "  a: x", "    # about a", "z: 1"),
			path:   paths.MustParse("$.m.b"),
			value:  1,
			want:   stringtest.LinesLF("m:", "  a: x", "    # about a", "  b: 1", "z: 1"),
		},
		"new item after a deeper comment line of the item before it": {
			before: stringtest.LinesLF("s:", "  - x", "    # about x"),
			path:   paths.MustParse("$.s[1]"),
			value:  "z",
			want:   stringtest.LinesLF("s:", "  - x", "    # about x", "  - z"),
		},
		"string of several lines at an empty entry with a line comment": {
			before: stringtest.LinesLF("a: # note", "b: 1"),
			path:   paths.Doc().Child("a"),
			value:  "line1\nline2\n",
			want:   stringtest.LinesLF("a: | # note", "  line1", "  line2", "b: 1"),
		},
		"string of several lines at an item with a line comment": {
			before: stringtest.LinesLF("- x # note", "- y"),
			path:   paths.MustParse("$[0]"),
			value:  "line1\nline2",
			want:   stringtest.LinesLF("- |- # note", "  line1", "  line2", "- y"),
		},
		"string with blank lines at its end over a block scalar that keeps its blank lines": {
			// The blank lines after the old block scalar belong to it, so the
			// new one takes their place.
			before: stringtest.LinesLF("a: |+", "  x", "", "b: 1"),
			path:   paths.Doc().Child("a"),
			value:  "q\n\n",
			want:   stringtest.LinesLF("a: |+", "  q", "", "b: 1"),
		},
		"scalar over a block scalar with a comment after its indicator": {
			before: stringtest.LinesLF("a: |- # note", "  x", "  y", "b: 1"),
			path:   paths.Doc().Child("a"),
			value:  "w",
			want:   stringtest.LinesLF("a: w # note", "b: 1"),
		},
		"string of several lines over a block scalar with a comment after its indicator": {
			// The blank line belongs to the old block scalar, so it goes with
			// it.
			before: stringtest.LinesLF("a: |+ # note", "  x", "", "b: 1"),
			path:   paths.Doc().Child("a"),
			value:  "w\nz",
			want:   stringtest.LinesLF("a: |- # note", "  w", "  z", "b: 1"),
		},
		"control: value beside a mapping whose merge key holds a tagged mapping": {
			// Control: the check of the merge keys after the Set compares the
			// tagged mapping of the merge key with the one that the Apply
			// found, through its tag.
			before: stringtest.LinesLF("m:", "  <<: !!map {x: 1}", "  y: 2", "z: 1"),
			path:   paths.Doc().Child("z"),
			value:  2,
			want:   stringtest.LinesLF("m:", "  <<: !!map {x: 1}", "  y: 2", "z: 2"),
		},
		"value that holds two keys that a decode reads as one, at a flow root that repeats its first key": {
			// The key has no text in the user's text, so the problem binds where an error at the value that the
			// Set writes binds (fix19 A3).
			before: "{a: 1, a: 2}\n",
			path:   paths.Doc().Child("b"),
			value:  map[any]any{1: "x", "1": "y"},
			dup:    true,
			err:    "test.yaml:1:2: $: cafe.jacobcolvin.com/v1 sets $.b, but",
		},
		"value that holds two keys that a decode reads as one, at a block root mapping": {
			// The key has no text in the user's text, so the problem binds where an error at the value that the
			// Set writes binds (fix19 A3). The root has no key, so the problem binds at its first key under the
			// path $.
			before: stringtest.LinesLF("a:", "b:", "  d: 1"),
			path:   paths.Doc().Child("c"),
			value:  map[any]any{1: "x", "1": "y"},
			err:    "test.yaml:1:1: $: cafe.jacobcolvin.com/v1 sets $.c, but",
		},
		"value that holds two keys that a decode reads as one, at a block root sequence": {
			// The key has no text in the user's text, so the problem binds where an error at the value that the
			// Set writes binds (fix19 A3).
			before: stringtest.LinesLF("- q", "- d: 1"),
			path:   paths.Doc().Index(2),
			value:  map[any]any{1: "x", "1": "y"},
			err:    "test.yaml:1:3: $: cafe.jacobcolvin.com/v1 sets $[2], but",
		},
		"value that holds two keys that a decode reads as one, at a block root sequence with an empty first item": {
			// The key has no text in the user's text, so the problem binds where an error at the value that the
			// Set writes binds (fix19 A3).
			// The first item has no text, so the problem binds at its "-".
			before: stringtest.LinesLF("-", "- d: 1"),
			path:   paths.Doc().Index(2),
			value:  map[any]any{1: "x", "1": "y"},
			err:    "test.yaml:1:1: $: cafe.jacobcolvin.com/v1 sets $[2], but",
		},
		"key at a flow root sequence": {
			// A problem at the root binds at its first item, past the "[".
			before: "[1, 2]\n",
			path:   paths.Doc().Child("x"),
			value:  1,
			err:    "test.yaml:1:2: $: cafe.jacobcolvin.com/v1 sets $.x, but the document holds a sequence",
		},
		"key at a block root sequence": {
			before: "- 1\n",
			path:   paths.Doc().Child("x"),
			value:  1,
			err:    "test.yaml:1:3: $: cafe.jacobcolvin.com/v1 sets $.x, but the document holds a sequence",
		},
		"index at a root mapping": {
			before: "a: 1\n",
			path:   paths.Doc().Index(0),
			value:  1,
			err:    "test.yaml:1:1: $: cafe.jacobcolvin.com/v1 sets $[0], but the document holds a mapping",
		},
		"index past the item after the last of the root": {
			before: "- a\n",
			path:   paths.Doc().Index(2),
			value:  "x",
			err:    "test.yaml:1:3: $: cafe.jacobcolvin.com/v1 sets $[2], but the document holds 1 item",
		},
		"root": {
			before: "a: 1\n",
			path:   paths.Doc(),
			value:  1,
			err:    "test.yaml:1:1: $: cafe.jacobcolvin.com/v1 replaces the root of the document",
		},
		"value that holds two keys that a decode reads as one": {
			// The key has no text in the user's text, so the problem binds where an error at the value that the
			// Set writes binds (fix19 A3).
			// The int 1 and the float 1.0 read as the key "1". The problem of
			// the value comes before the float key, which reads as another
			// value than its text.
			before: "m: {}\n",
			path:   paths.MustParse("$.m.x"),
			value:  map[any]any{1: "a", 1.0: "b"},
			err: `test.yaml:1:1: $.m: cafe.jacobcolvin.com/v1 sets $.m.x, but the value it writes holds two keys ` +
				`that a decode reads as "1"`,
		},
		"string with a control character through a merge key of a mapping written in place": {
			// Go-yaml's encoder writes the character as it is, which fails the
			// Set before the merge key in a refused form does.
			before: "<<: {8: {}, p: 2}\n",
			path:   paths.MustParse("$.8"),
			value:  "\x01",
			engine: true,
			err:    "go-yaml's encoder writes U+0001 as it is, and YAML does not allow that in a text",
		},
		"string with a control character in place of a quoted scalar through a merge key of a mapping written in place": {
			// Control: the string keeps the double quotes, which escape the
			// character, so the merge key in a refused form fails the Set.
			before: "<<: {a: \"x\", p: 2}\n",
			path:   paths.MustParse("$.a"),
			value:  "\x01",
			err:    "which goes through a merge key that merges a mapping written in place",
		},
		"value of a marshaler with an anchor beside a float that go-yaml's decoder reads as a string": {
			// The float fails the round trip of the encode, whatever the YAML
			// of the marshaler holds.
			before: "a: 1\n",
			path:   paths.MustParse("$.b"),
			value:  map[string]any{"x": yamlMarshaler("&q 1\n"), "y": 1e6},
			engine: true,
			err:    `go-yaml's encoder writes the float 1e+06 as text that go-yaml's decoder reads as "1e+06"`,
		},
		"value of a marshaler with an alias that the YAML does not anchor": {
			// Control: the alias makes go-yaml's decoder refuse the text of the
			// encode, and the Set fails with the alias.
			before: "a: 1\n",
			path:   paths.MustParse("$.b"),
			value:  map[string]any{"x": yamlMarshaler("*q\n"), "y": 1e6},
			err:    "sets $.b to a value whose YAML holds the alias *q",
		},
		"string of the text of a block scalar and a line break at the end of a text without a line break": {
			// Go-yaml reads the scalar as "x", so the Set writes the line break
			// that ends the string.
			before: stringtest.JoinLF("a: |", "  x"),
			path:   paths.MustParse("$.a"),
			value:  "x\n",
			want:   stringtest.LinesLF("a: |", "  x"),
		},
		"string with a line break at a block scalar at the end of a text without a line break": {
			// Control.
			before: stringtest.JoinLF("a: |", "  x"),
			path:   paths.MustParse("$.a"),
			value:  "y\n",
			want:   stringtest.LinesLF("a: |", "  y"),
		},
		"value that holds two keys of one text that a decode reads as one": {
			// The key has no text in the user's text, so the problem binds where an error at the value that the
			// Set writes binds (fix19 A3).
			// Control: the int 1 and the string "1" read as the key "1".
			before: "m: {}\n",
			path:   paths.MustParse("$.m.x"),
			value:  map[any]any{1: "a", "1": "b"},
			err: stringtest.JoinLF(
				`test.yaml:1:1: $.m: cafe.jacobcolvin.com/v1 sets $.m.x, `+
					`but the value it writes holds two keys that a decode reads as "1"`,
				"|-- remove one of the two",
				"`-- cafe.jacobcolvin.com/v1 sets $.m.x",
			),
		},
		"value below an alias that holds two keys of one text that a decode reads as one": {
			// The detail names the place where the text holds it once the Set
			// writes it, below the anchored value (fix19 F6).
			before: stringtest.LinesLF("base: &b", "  sub: {k: 1}", "md: *b"),
			path:   paths.MustParse("$.md.sub.z"),
			value:  map[any]any{1: "a", "1": "b"},
			err: stringtest.JoinLF(
				`test.yaml:2:3: $.base.sub: cafe.jacobcolvin.com/v1 sets $.md.sub.z, `+
					`but the value it writes holds two keys that a decode reads as "1"`,
				"|-- remove one of the two",
				"`-- cafe.jacobcolvin.com/v1 sets $.md.sub.z",
			),
		},
		"value at an existing place that holds two keys that a decode reads as one": {
			// The key has no text in the user's text, so the problem binds where an error at the value that the
			// Set writes binds (fix19 A3).
			before: "m: 1\n",
			path:   paths.MustParse("$.m"),
			value:  map[any]any{1.0: "a", 1: "b"},
			err: `test.yaml:1:4: $.m: cafe.jacobcolvin.com/v1 sets $.m, but the value it writes holds two keys ` +
				`that a decode reads as "1"`,
		},
		"value at a new nested place that holds two keys that a decode reads as one": {
			// The key has no text in the user's text, so the problem binds where an error at the value that the
			// Set writes binds (fix19 A3).
			before: "m: {}\n",
			path:   paths.MustParse("$.m.n.o"),
			value:  map[any]any{1.0: "a", 1: "b"},
			err: `test.yaml:1:1: $.m: cafe.jacobcolvin.com/v1 sets $.m.n.o, but the value it writes holds two ` +
				`keys that a decode reads as "1"`,
		},
		"value that holds a mapping of two keys that a decode reads as one": {
			// The key has no text in the user's text, so the problem binds where an error at the value that the
			// Set writes binds (fix19 A3).
			before: stringtest.LinesLF("m:", "  a: 1"),
			path:   paths.MustParse("$.m.y"),
			value:  map[string]any{"x": map[any]any{16: "a", 16.0: "b"}},
			err: `test.yaml:1:1: $.m: cafe.jacobcolvin.com/v1 sets $.m.y, but the value it writes holds two ` +
				`keys that a decode reads as "16"`,
		},
		"key that a decode reads apart from a key of the same digits": {
			// The decode reads 1e3 as the string "1e3", not as 1000.
			before: stringtest.LinesLF("m:", "  1e3: a"),
			path:   paths.MustParse("$.m.1000"),
			value:  "b",
			want:   stringtest.LinesLF("m:", "  1e3: a", `  "1000": b`),
		},
		"new key of a mapping item of a flow sequence without braces": {
			before: stringtest.LinesLF("f: [0, a: 1, 2] # c"),
			path:   paths.MustParse("$.f[1].b"),
			value:  2,
			err:    "test.yaml:1:8: $.f[1].a: cafe.jacobcolvin.com/v1 sets $.f[1].b, which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"new mapping in a mapping item of a flow sequence without braces": {
			before: stringtest.LinesLF("f: [", "  a: 1,", "  2", "]"),
			path:   paths.MustParse("$.f[0].b.c"),
			value:  2,
			err:    "test.yaml:2:3: $.f[0].a: cafe.jacobcolvin.com/v1 sets $.f[0].b.c, which reaches a pair without braces in a flow sequence, and cafe does not edit inside such a pair",
		},
		"new key of an anchored mapping that an alias item of a flow sequence reads": {
			before: stringtest.LinesLF("d: &d", "  a: 1", "  b: 2", "s: [*d]"),
			path:   paths.MustParse("$.s[0].z"),
			value:  3,
			want:   stringtest.LinesLF("d: &d", "  a: 1", "  b: 2", "  z: 3", "s: [*d]"),
		},
		"new key of an anchored mapping with an empty first value that an alias item of a flow sequence reads": {
			before: stringtest.LinesLF("defaults: &defaults", "  image:", "  replicas: 1", "services: [*defaults]"),
			path:   paths.MustParse("$.services[0].port"),
			value:  80,
			want: stringtest.LinesLF(
				"defaults: &defaults", "  image:", "  replicas: 1", "  port: 80", "services: [*defaults]",
			),
		},
		"new key of an anchored mapping with an empty last value that an alias item of a flow sequence reads": {
			before: stringtest.LinesLF("x: &x", "  uses: v4", "  with:", "steps: [*x]"),
			path:   paths.MustParse("$.steps[0].name"),
			value:  "checkout",
			want:   stringtest.LinesLF("x: &x", "  uses: v4", "  with:", "  name: checkout", "steps: [*x]"),
		},
		"new key of an anchored mapping with comments that an alias item of a flow sequence reads": {
			before: stringtest.LinesLF(
				"d: &d # d", "  a: # c", "  b: 1 # b", "s: [", "  0, # zero", "  *d, # alias", "]",
			),
			path:  paths.MustParse("$.s[1].z"),
			value: 2,
			err:   "test.yaml:4:4: $.s: cafe.jacobcolvin.com/v1 sets $.s[1].z, which reaches a flow collection with a comment inside it, and cafe does not edit inside such a collection",
		},
		"new mapping in an anchored mapping that an alias item of a flow sequence reads": {
			before: stringtest.LinesLF("d: &d", "  a: # c", "  b: 1", "s: [*d] # s"),
			path:   paths.MustParse("$.s[0].z.y"),
			value:  2,
			want:   stringtest.LinesLF("d: &d", "  a: # c", "  b: 1", "  z:", "    \"y\": 2", "s: [*d] # s"),
		},
		"scalar keeps the comment": {
			before: stringtest.LinesLF("spec:", "  cost: 45 # kr"),
			path:   spec.Child("cost"),
			value:  50,
			want:   stringtest.LinesLF("spec:", "  cost: 50 # kr"),
		},
		"NaN": {
			before: stringtest.LinesLF("spec:", "  cost: 45 # kr"),
			path:   spec.Child("cost"),
			value:  math.NaN(),
			want:   stringtest.LinesLF("spec:", "  cost: .nan # kr"),
		},
		"string keeps its quotes": {
			before: stringtest.LinesLF("spec:", `  open: "07:00"`),
			path:   spec.Child("open"),
			value:  "08:00",
			want:   stringtest.LinesLF("spec:", `  open: "08:00"`),
		},
		"string keeps its double quotes in a flow mapping": {
			before: `m: {a: "q", b: 1}` + "\n",
			path:   paths.MustParse("$.m.a"),
			value:  "hello",
			want:   `m: {a: "hello", b: 1}` + "\n",
		},
		"string keeps its single quotes in a flow mapping": {
			before: "m: {a: 'q', b: 1}\n",
			path:   paths.MustParse("$.m.a"),
			value:  "it's",
			want:   "m: {a: 'it''s', b: 1}\n",
		},
		"plain string in a flow mapping takes the text of go-yaml's flow encoder": {
			before: "m: {a: q, b: 1}\n",
			path:   paths.MustParse("$.m.a"),
			value:  "x: y",
			want:   `m: {a: "x: y", b: 1}` + "\n",
		},
		"string keeps its double quotes in a flow sequence": {
			before: `m: ["q", 1]` + "\n",
			path:   paths.MustParse("$.m[0]"),
			value:  "hello",
			want:   `m: ["hello", 1]` + "\n",
		},
		"string keeps its single quotes in a flow sequence": {
			before: "m: ['q', 1]\n",
			path:   paths.MustParse("$.m[0]"),
			value:  "hello",
			want:   "m: ['hello', 1]\n",
		},
		"string with a control character keeps its double quotes in a flow mapping": {
			// The double quotes escape U+0001, so the text reads back.
			before: `m: {a: "q"}` + "\n",
			path:   paths.MustParse("$.m.a"),
			value:  "a\x01b",
			want:   `m: {a: "a\u0001b"}` + "\n",
		},
		"write-back of a decoded string with a control character in a flow mapping": {
			before: `m: {a: "a\u0001b"}` + "\n",
			path:   paths.MustParse("$.m.a"),
			value:  "a\x01b",
			want:   `m: {a: "a\u0001b"}` + "\n",
		},
		"string with a control character over a plain scalar in a flow mapping": {
			// Go-yaml's flow encoder writes U+0001 as it is.
			before: "m: {a: q}\n",
			path:   paths.MustParse("$.m.a"),
			value:  "a\x01b",
			err: "1:8: $.m.a: go-yaml's encoder writes U+0001 as it is, " +
				"and YAML does not allow that in a text",
			engine: true,
		},
		"string of several lines keeps its double quotes": {
			before: stringtest.LinesLF(`a: "q" # c`, "b: 1"),
			path:   paths.Doc().Child("a"),
			value:  "x\ny",
			want:   stringtest.LinesLF(`a: "x\ny" # c`, "b: 1"),
		},
		"string of several lines takes double quotes over a single-quoted scalar": {
			// A single-quoted scalar has no escapes.
			before: "a: 'q'\n",
			path:   paths.Doc().Child("a"),
			value:  "x\ny",
			want:   `a: "x\ny"` + "\n",
		},
		"string with a next line character keeps its single quotes": {
			// YAML allows U+0085 in a text.
			before: "a: 'q'\n",
			path:   paths.Doc().Child("a"),
			value:  "x\u0085y",
			want:   "a: 'x\u0085y'\n",
		},
		"string with a control rune past U+FFFF keeps its quotes": {
			before: stringtest.LinesLF("spec:", `  tag: "q"`),
			path:   spec.Child("tag"),
			value:  "a\U000E0001",
			want:   stringtest.LinesLF("spec:", `  tag: "a\U000e0001"`),
		},
		"number over a quoted string": {
			before: stringtest.LinesLF("spec:", `  size: "3"`),
			path:   spec.Child("size"),
			value:  2,
			want:   stringtest.LinesLF("spec:", "  size: 2"),
		},
		"list over a scalar": {
			before: stringtest.LinesLF(
				"spec:",
				"  tags: vegan",
				"  price: 5",
			),
			path:  spec.Child("tags"),
			value: []string{"vegan"},
			want: stringtest.LinesLF(
				"spec:",
				"  tags: [vegan]",
				"  price: 5",
			),
		},
		"list over a tagged scalar": {
			// Go-yaml reads a quoted scalar under !!str as YAML does.
			before: stringtest.LinesLF("spec:", "  tags: !!str 'vegan'"),
			path:   spec.Child("tags"),
			value:  []string{"vegan"},
			want:   stringtest.LinesLF("spec:", "  tags: [vegan]"),
		},
		"block mapping over a block mapping": {
			before: stringtest.LinesLF(
				"spec:",
				"  wifi: # router",
				"    security: wep",
				"    # old",
				"  price: 5",
			),
			path:  spec.Child("wifi"),
			value: map[string]any{"mode": "wpa3", "ssid": "fika"},
			want: stringtest.LinesLF(
				"spec:",
				"  wifi: # router",
				"    mode: wpa3",
				"    ssid: fika",
				"  price: 5",
			),
		},
		"scalar over a block mapping": {
			before: stringtest.LinesLF(
				"spec:",
				"  wifi: # router",
				"    security: wep",
				"  price: 5",
			),
			path:  spec.Child("wifi"),
			value: "off",
			want: stringtest.LinesLF(
				"spec:",
				`  wifi: "off" # router`,
				"  price: 5",
			),
		},
		"scalar over a block mapping without a comment": {
			before: stringtest.LinesLF(
				"spec:",
				"  wifi:",
				"    security: wep",
				"    mode: open",
				"  price: 5",
			),
			path:  spec.Child("wifi"),
			value: "off",
			want: stringtest.LinesLF(
				"spec:",
				`  wifi: "off"`,
				"  price: 5",
			),
		},
		"scalar over an anchored block mapping": {
			before: stringtest.LinesLF("milk: &oat", "  name: x", "  cost: 5 # kr", "other: 1"),
			path:   paths.Doc().Child("milk"),
			value:  "z",
			want:   stringtest.LinesLF("milk: &oat z", "other: 1"),
		},
		"scalar over an anchored block mapping with a key comment": {
			before: stringtest.LinesLF("milk: &oat # top", "  name: x # n", "  cost: 5 # kr"),
			path:   paths.Doc().Child("milk"),
			value:  "z",
			want:   "milk: &oat z # top\n",
		},
		"scalar over a tagged block mapping": {
			before: stringtest.LinesLF("milk: !!map # top", "  name: x # n", "other: 1"),
			path:   paths.Doc().Child("milk"),
			value:  "z",
			want:   stringtest.LinesLF("milk: z # top", "other: 1"),
		},
		"scalar over a block mapping item": {
			before: stringtest.LinesLF("l:", "  - k: v", "    j: w # c"),
			path:   paths.MustParse("$.l[0]"),
			value:  "z",
			want:   stringtest.LinesLF("l:", "  - z"),
		},
		"scalar over an anchored item": {
			before: stringtest.LinesLF("l:", "  - &a", "    k: v # c"),
			path:   paths.MustParse("$.l[0]"),
			value:  "z",
			want:   stringtest.LinesLF("l:", "  - &a z"),
		},
		"scalar over a mapping item with a comment line after it": {
			before: stringtest.LinesLF("l:", "  - k: v", "    # end", "x: 1"),
			path:   paths.MustParse("$.l[0]"),
			value:  "z",
			want:   stringtest.LinesLF("l:", "  - z", "x: 1"),
		},
		"mapping over an anchored block mapping": {
			before: stringtest.LinesLF("milk: &oat", "  name: x", "other: 1"),
			path:   paths.Doc().Child("milk"),
			value:  map[string]int{"a": 1},
			want:   stringtest.LinesLF("milk: &oat", "  a: 1", "other: 1"),
		},
		"mapping over a tagged block mapping": {
			before: stringtest.LinesLF("milk: !!map", "  name: x"),
			path:   paths.Doc().Child("milk"),
			value:  map[string]int{"a": 1},
			want:   stringtest.LinesLF("milk:", "  a: 1"),
		},
		"mapping over an anchored block mapping that an alias reads": {
			before: stringtest.LinesLF("milk: &oat", "  name: x", "d: *oat"),
			path:   paths.Doc().Child("milk"),
			value:  map[string]int{"a": 1},
			want:   stringtest.LinesLF("milk: &oat", "  a: 1", "d: *oat"),
		},
		"sequence over an anchored block sequence": {
			before: stringtest.LinesLF("s: &q", "  - 1"),
			path:   paths.Doc().Child("s"),
			value:  []int{7, 8},
			want:   stringtest.LinesLF("s: &q", "  - 7", "  - 8"),
		},
		"scalar over a block sequence": {
			before: stringtest.LinesLF(
				"spec:",
				"  tags:",
				"  - vegan",
				"  - oat",
			),
			path:  spec.Child("tags"),
			value: "none",
			want:  stringtest.LinesLF("spec:", "  tags: none"),
		},
		"one-entry mapping over a mapping item": {
			before: stringtest.LinesLF("l:", "  - k: v", "    j: w", "x: 1"),
			path:   paths.MustParse("$.l[0]"),
			value:  map[string]int{"a": 1},
			want:   stringtest.LinesLF("l:", "  - a: 1", "x: 1"),
		},
		"one-item sequence over a sequence item": {
			before: stringtest.LinesLF("l:", "  - - a", "    - b", "x: 1"),
			path:   paths.MustParse("$.l[0]"),
			value:  []string{"z"},
			want:   stringtest.LinesLF("l:", "  - - z", "x: 1"),
		},
		"value of an entry with none": {
			before: stringtest.LinesLF(
				"spec:",
				"  wifi:",
				"  price: 5",
			),
			path:  spec.Child("wifi"),
			value: "open",
			want: stringtest.LinesLF(
				"spec:",
				"  wifi: open",
				"  price: 5",
			),
		},
		"empty value of a flow mapping before a comma": {
			before: stringtest.LinesLF("m: {a: , b: 1}"),
			path:   paths.MustParse("$.m.a"),
			value:  3,
			want:   stringtest.LinesLF("m: {a: 3, b: 1}"),
		},
		"empty value at the end of a flow mapping": {
			before: stringtest.LinesLF("m: {b: 1, a:  }"),
			path:   paths.MustParse("$.m.a"),
			value:  3,
			want:   stringtest.LinesLF("m: {b: 1, a: 3}"),
		},
		"empty value of a flow mapping of several lines before a comma and a comment": {
			before: stringtest.LinesLF("m: {", "  a: , # c", "  b: 1", "}"),
			path:   paths.MustParse("$.m.a"),
			value:  3,
			err:    "test.yaml:1:4: $.m: cafe.jacobcolvin.com/v1 sets $.m.a, which reaches a flow collection with a comment inside it, and cafe does not edit inside such a collection",
		},
		"empty value with spaces at the end of its line": {
			before: stringtest.LinesCRLF("m:", "  a: \t ", "  b: 1"),
			path:   paths.MustParse("$.m.a"),
			value:  3,
			want:   stringtest.LinesCRLF("m:", "  a: 3", "  b: 1"),
		},
		"empty item with spaces at the end of its line": {
			before: stringtest.LinesLF("l:", "  -  ", "  - 1"),
			path:   paths.MustParse("$.l[0]"),
			value:  3,
			want:   stringtest.LinesLF("l:", "  - 3", "  - 1"),
		},
		"empty value with spaces before a comment": {
			before: stringtest.LinesLF("m:", "  a:  # c", "  b: 1"),
			path:   paths.MustParse("$.m.a"),
			value:  3,
			want:   stringtest.LinesLF("m:", "  a: 3  # c", "  b: 1"),
		},
		"new key at the end of a mapping": {
			before: stringtest.LinesLF(
				"spec:",
				"  price: 45 # kr",
				"# trailing",
			),
			path:  spec.Child("currency"),
			value: "SEK",
			want: stringtest.LinesLF(
				"spec:",
				"  price: 45 # kr",
				"  currency: SEK",
				"# trailing",
			),
		},
		"new key in a mapping that starts with a merge key": {
			before: stringtest.LinesLF("base: &b", "  k: 1", "soy:", "  <<: *b", "  z: 3", "x: 1"),
			path:   paths.MustParse("$.soy.price"),
			value:  2,
			want:   stringtest.LinesLF("base: &b", "  k: 1", "soy:", "  <<: *b", "  z: 3", "  price: 2", "x: 1"),
		},
		"new key in a mapping that holds only a merge key": {
			before: stringtest.LinesLF("base: &b", "  k: 1", "soy:", "  <<: *b", "x: 1"),
			path:   paths.MustParse("$.soy.price"),
			value:  2,
			want:   stringtest.LinesLF("base: &b", "  k: 1", "soy:", "  <<: *b", "  price: 2", "x: 1"),
		},
		"key that an earlier merge key also brings in": {
			before: stringtest.LinesLF("d: &d", "  port: 80", "svc:", "  <<: *d", "  port: 8080"),
			path:   paths.MustParse("$.svc.port"),
			value:  9090,
			want:   stringtest.LinesLF("d: &d", "  port: 80", "svc:", "  <<: *d", "  port: 9090"),
		},
		"block sequence through an alias in a flow sequence": {
			before: stringtest.LinesLF("a: &x", "  k:", "    - 1", "b: [*x]"),
			path:   paths.MustParse("$.b[0].k"),
			value:  []int{2, 3},
			want:   stringtest.LinesLF("a: &x", "  k:", "    - 2", "    - 3", "b: [*x]"),
		},
		"anchored entry of the mapping that merges it": {
			before: stringtest.LinesLF("soy:", "  base: &b", "    k: 1", "  <<: *b", "  name: s"),
			path:   paths.MustParse("$.soy.base"),
			value:  "z",
			err:    "test.yaml:4:3: $.soy: cafe.jacobcolvin.com/v1 sets $.soy.base, which leaves <<: *b at 4:3 reading a value other than a mapping, and cafe does not merge a value other than a mapping",
		},
		"new mapping above a key": {
			before: stringtest.LinesLF("metadata:", "    name: yamull"),
			path:   spec.Child("currency"),
			value:  "SEK",
			want: stringtest.LinesLF(
				"metadata:",
				"    name: yamull",
				"spec:",
				"    currency: SEK",
			),
		},
		"new key in a flow mapping": {
			before: "spec: {price: 65}\n",
			path:   spec.Child("currency"),
			value:  "SEK",
			want:   "spec: {price: 65, currency: SEK}\n",
		},
		"new key below an empty mapping that the edit wrote": {
			before: stringtest.LinesLF(
				"spec:",
				"  extras:",
				"    oat: ~",
			),
			path:      spec.Child("extras", "oat"),
			value:     map[string]any{},
			thenKey:   "price",
			thenValue: 0,
			want: stringtest.LinesLF(
				"spec:",
				"  extras:",
				"    oat:",
				"      price: 0",
			),
		},
		"new key below an empty mapping that the edit wrote before a comment": {
			before:    stringtest.LinesLF("spec:", "  oat: ~ # c"),
			path:      spec.Child("oat"),
			value:     map[string]any{},
			thenKey:   "price",
			thenValue: 0,
			want:      stringtest.LinesLF("spec:", "  oat: # c", "    price: 0"),
		},
		"new key below an empty mapping that the edit wrote before a comment in an item": {
			before:    stringtest.LinesLF("l:", "  - ~ # c"),
			path:      paths.MustParse("$.l[0]"),
			value:     map[string]any{},
			thenKey:   "price",
			thenValue: 0,
			want:      stringtest.LinesLF("l:", "  - # c", "    price: 0"),
		},
		"item past the last": {
			before: stringtest.LinesLF(
				"spec:",
				"  tags:",
				"    - a",
			),
			path:  spec.Child("tags").Index(1),
			value: "b",
			want: stringtest.LinesLF(
				"spec:",
				"  tags:",
				"    - a",
				"    - b",
			),
		},
		"string of two lines past the last item": {
			before: stringtest.LinesLF("spec:", "  tags:", "    - a"),
			path:   spec.Child("tags").Index(1),
			value:  "b\nc",
			want:   stringtest.LinesLF("spec:", "  tags:", "    - a", "    - |-", "      b", "      c"),
		},
		"item past the last of a flow sequence": {
			before: stringtest.LinesLF("spec:", "  tags: [a]"),
			path:   spec.Child("tags").Index(1),
			value:  "b",
			want:   stringtest.LinesLF("spec:", "  tags: [a, b]"),
		},
		"new key in a flow mapping with a trailing comma": {
			before: "spec: {price: 65, size: M,}\n",
			path:   spec.Child("currency"),
			value:  "SEK",
			want:   "spec: {price: 65, size: M, currency: SEK,}\n",
		},
		"new key in a flow mapping of several lines with a trailing comma": {
			before: stringtest.LinesLF("{", "  price: 65,", "  size: M,", "}"),
			path:   paths.Doc().Child("currency"),
			value:  "SEK",
			want:   stringtest.LinesLF("{", "  price: 65,", "  size: M, currency: SEK,", "}"),
		},
		"new key in a flow mapping with a comment after its last entry": {
			before: stringtest.LinesLF("spec: {price: 65, # kr", "}"),
			path:   spec.Child("currency"),
			value:  "SEK",
			err:    "test.yaml:1:7: $.spec: cafe.jacobcolvin.com/v1 sets $.spec.currency, which reaches a flow collection with a comment inside it, and cafe does not edit inside such a collection",
		},
		"item past the last of a flow sequence with a trailing comma": {
			before: stringtest.LinesLF("spec:", "  tags: [a, b,]"),
			path:   spec.Child("tags").Index(2),
			value:  "c",
			want:   stringtest.LinesLF("spec:", "  tags: [a, b, c,]"),
		},
		"item past the last of a flow sequence with a comment after its last item": {
			before: stringtest.LinesLF("spec:", "  tags: [a # last", "  ]"),
			path:   spec.Child("tags").Index(1),
			value:  "b",
			err:    "test.yaml:2:9: $.spec.tags: cafe.jacobcolvin.com/v1 sets $.spec.tags[1], which reaches a flow collection with a comment inside it, and cafe does not edit inside such a collection",
		},
		"below a scalar": {
			before: "spec: 5\n",
			path:   spec.Child("currency"),
			value:  "SEK",
			err:    "cafe.jacobcolvin.com/v1 sets $.spec.currency, but spec holds a scalar",
		},
		"two levels below a scalar": {
			// The text holds spec, the deepest value on the path, so the
			// problem binds there.
			before: "spec: 5\n",
			path:   spec.Child("currency").Child("code"),
			value:  "SEK",
			err:    "test.yaml:1:7: $.spec: cafe.jacobcolvin.com/v1 sets $.spec.currency.code, but spec holds a scalar",
		},
		"two levels below a scalar that a merge key brings in": {
			// The path looks k up in a mapping that a refused merge key holds,
			// before it meets the scalar, so the refusal comes first.
			before: stringtest.LinesLF("8:", "  <<: &x0 {c: q, k: q}", "<<: *x0"),
			path:   paths.MustParse("$.k.16.y"),
			value:  1,
			err: "test.yaml:2:3: $.8: cafe.jacobcolvin.com/v1 sets $.k.16.y, which goes through a merge key whose " +
				"value has an anchor",
		},
		"two levels below a scalar that a kept merge key brings in": {
			// The problem binds at the value that the merge key brings in, under
			// the path of that value in the user's text (fix19 P56).
			before: stringtest.LinesLF("x0: &x0 {c: q, k: q}", "<<: *x0"),
			path:   paths.MustParse("$.k.16.y"),
			value:  1,
			err:    "test.yaml:1:19: $.x0.k: cafe.jacobcolvin.com/v1 sets $.k.16.y, but k holds a scalar",
		},
		"two levels below an alias of a scalar": {
			before: stringtest.LinesLF("x: &x q", "m: *x"),
			path:   paths.MustParse("$.m.a.b"),
			value:  1,
			err:    "test.yaml:2:4: $.m: cafe.jacobcolvin.com/v1 sets $.m.a.b, but m holds a scalar",
		},
		"below a null": {
			before: "spec:\n",
			path:   spec.Child("currency"),
			value:  "SEK",
			err:    "cafe.jacobcolvin.com/v1 sets $.spec.currency, but spec holds null",
		},
		"item far past the last": {
			before: "tags: [a]\n",
			path:   paths.Doc().Child("tags").Index(3),
			value:  "b",
			err:    "cafe.jacobcolvin.com/v1 sets $.tags[3], but tags holds 1 item\n",
		},
		"item far past the last of two": {
			before: "tags: [a, b]\n",
			path:   paths.Doc().Child("tags").Index(3),
			value:  "c",
			err:    "cafe.jacobcolvin.com/v1 sets $.tags[3], but tags holds 2 items",
		},
		"item past the end of an empty sequence": {
			before: "tags: []\n",
			path:   paths.Doc().Child("tags").Index(1),
			value:  "b",
			err:    "cafe.jacobcolvin.com/v1 sets $.tags[1], but tags holds no items",
		},
		"item of a sequence that the document lacks": {
			before: "tags: [a]\n",
			path:   paths.Doc().Child("sizes").Index(0),
			value:  "s",
			err:    "test.yaml:1:1: $: cafe.jacobcolvin.com/v1 sets $.sizes[0], but the document holds no $.sizes",
		},
		"key of an item of a sequence that a mapping lacks": {
			before: "spec: {tags: [a]}\n",
			path:   paths.MustParse("$.spec.sizes[0].name"),
			value:  "s",
			err: "test.yaml:1:1: $.spec: cafe.jacobcolvin.com/v1 sets $.spec.sizes[0].name, " +
				"but the document holds no $.spec.sizes",
		},
		"item of a sequence below a scalar": {
			before: "spec: 1\n",
			path:   paths.MustParse("$.spec.sizes[0]"),
			value:  "s",
			err:    "test.yaml:1:7: $.spec: cafe.jacobcolvin.com/v1 sets $.spec.sizes[0], but spec holds a scalar",
		},
		"anchored block mapping on one line keeps its alias": {
			before: stringtest.LinesLF(
				"spec:",
				"  oat: &milk",
				"    price: 5",
				"  soy: *milk",
			),
			path:  spec.Child("oat"),
			value: 6,
			want:  stringtest.LinesLF("spec:", "  oat: &milk 6", "  soy: *milk"),
		},
		"anchored block mapping with its alias as the first key of an item": {
			before: stringtest.LinesLF(
				"milk: &oat",
				"  name: x",
				"  cost: 5",
				"drinks:",
				"  - milk: *oat",
				"  - milk: *oat",
			),
			path:  paths.Doc().Child("milk"),
			value: "z",
			want:  stringtest.LinesLF("milk: &oat z", "drinks:", "  - milk: *oat", "  - milk: *oat"),
		},
		"anchored block mapping keeps its inner comments at its alias": {
			before: stringtest.LinesLF(
				"milk: &oat # on key",
				"  # Oat milk.",
				"  name: x # inner",
				"  cost: 5 # kr",
				"d:",
				"  m: *oat # the milk",
			),
			path:  paths.Doc().Child("milk"),
			value: 1,
			want:  stringtest.LinesLF("milk: &oat 1 # on key", "d:", "  m: *oat # the milk"),
		},
		"anchored block mapping with its alias in a flow sequence": {
			before: stringtest.LinesLF("milk: &oat", "  name: x", "d: [*oat]"),
			path:   paths.Doc().Child("milk"),
			value:  1,
			want:   stringtest.LinesLF("milk: &oat 1", "d: [*oat]"),
		},
		"anchored block scalar keeps its alias": {
			before: stringtest.LinesLF("s: &q |", "  text", "d:", "  m: *q"),
			path:   paths.Doc().Child("s"),
			value:  "z",
			want:   stringtest.LinesLF("s: &q z", "d:", "  m: *q"),
		},
		"anchored value keeps its alias": {
			before: stringtest.LinesLF(
				"tags: &diet vegan",
				"labels:",
				"  diet: *diet",
			),
			path:  paths.Doc().Child("tags"),
			value: []string{"vegan"},
			want:  stringtest.LinesLF("tags: &diet", "  - vegan", "labels:", "  diet: *diet"),
		},
		"line endings of the file": {
			before: stringtest.LinesCRLF("spec:", "  price: 45"),
			path:   spec.Child("currency"),
			value:  "SEK",
			want: stringtest.LinesCRLF(
				"spec:",
				"  price: 45",
				"  currency: SEK",
			),
		},
		"file without a final newline": {
			before: stringtest.JoinLF("spec:", "  price: 45"),
			path:   spec.Child("currency"),
			value:  "SEK",
			want: stringtest.JoinLF(
				"spec:",
				"  price: 45",
				"  currency: SEK",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := []niceyaml.SourceOption{niceyaml.WithAllowDuplicateKeys(tc.dup)}
			if tc.ref != "" {
				opts = append(opts, niceyaml.WithReferences(niceyaml.NewSourceFromString(tc.ref)))
			}

			d := newDoc(t, tc.before, opts...)

			set := func(ctx context.Context, n *edit.Node) error {
				err := n.Set(ctx, tc.value)
				if err != nil || tc.thenKey == "" {
					return err
				}

				return n.At(paths.Current().Child(tc.thenKey)).Set(ctx, tc.thenValue)
			}

			var err error

			if tc.panics != nil {
				require.PanicsWithValue(t, tc.panics, func() { err = applyAt(t, d, tc.path, set) })
				require.NoError(t, err)
				require.Equal(t, tc.before, d.Text())

				return
			}

			err = applyAt(t, d, tc.path, set)

			switch {
			case tc.engine:
				require.ErrorIs(t, err, edit.ErrEngine)
			case tc.err != "":
				require.NotErrorIs(t, err, edit.ErrEngine)
			}

			requireEdit(t, d, err, tc.err, tc.want)
		})
	}

	t.Run("YAML of a marshaler that YAML reads otherwise than go-yaml", func(t *testing.T) {
		t.Parallel()

		// The YAML that a marshaler returns is text of the program, which
		// goes into the document as go-yaml reads it. Each row compares the
		// value at the place with niceyaml's decode of that YAML alone and
		// with niceyaml's decode of the whole document after the Set, so no
		// row names the value that go-yaml reads.
		tcs := map[string]struct {
			yaml string
			want string
			// Whether the Set may fail with ErrEngine instead.
			engine bool
		}{
			"block scalar under the int tag": {
				yaml: "!!int |\n  5\n",
				want: "a: !!int |\n  5\n",
			},
			"verbatim int tag on a plain scalar": {
				yaml: "!<tag:yaml.org,2002:int> 5",
				want: "a: !<tag:yaml.org,2002:int> 5\n",
			},
			"int tag on a number with a leading zero": {
				yaml: "!!int 017",
				want: "a: !!int 017\n",
			},
			"str tag on a hexadecimal number": {
				yaml: "!!str 0x10",
				want: "a: !!str 0x10\n",
			},
			"block scalar whose last line ends with a space under strip chomping": {
				yaml: "|-\n  x \n",
				want: "a: |-\n  x \n",
			},
			"mapping after a tag alone on the line of its key": {
				// A control: go-yaml's encoder writes the mapping in flow style,
				// where YAML reads it as go-yaml does.
				yaml: "x: !t\ny: 1\n",
				want: "a: {x: !t {y: 1}}\n",
			},
			"int tag on an integer past the range of an int64": {
				// A control: yaml.Unmarshal reads the integer otherwise than
				// niceyaml's decode does. The Set either writes a text that reads
				// as the YAML of the marshaler or fails with ErrEngine.
				yaml:   "!!int 18446744073709551615",
				engine: true,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				d := newDoc(t, "a: 1\n")

				err := applyAt(t, d, paths.Doc().Child("a"), func(ctx context.Context, n *edit.Node) error {
					return n.Set(ctx, yamlMarshaler(tc.yaml))
				})
				if err != nil && tc.engine {
					require.ErrorIs(t, err, edit.ErrEngine)
					require.Equal(t, "a: 1\n", d.Text())

					return
				}

				require.NoError(t, err)

				if tc.want != "" {
					require.Equal(t, tc.want, d.Text())
				}

				alone, err := niceyaml.NewSourceFromString(tc.yaml).Document()
				require.NoError(t, err)

				want, err := alone.Decode[any](t.Context())
				require.NoError(t, err)

				whole, err := niceyaml.NewSourceFromString(d.Text()).Document()
				require.NoError(t, err)

				got, err := whole.Decode[map[string]any](t.Context())
				require.NoError(t, err)
				require.Equal(t, want, got["a"])
			})
		}
	})
}

func TestNode_Set_Anchor(t *testing.T) {
	t.Parallel()

	// Each Set replaces an anchored value, and the value keeps the anchor,
	// so each alias of the anchor reads the new value.
	tcs := map[string]struct {
		value  any
		before string
		path   string
		want   string
		// The paths of the aliases of the anchor, each of which must decode
		// as the value at path does.
		aliases []string
	}{
		"scalar": {
			before:  stringtest.LinesLF("a: &v 1", "b: *v"),
			path:    "$.a",
			value:   2,
			want:    stringtest.LinesLF("a: &v 2", "b: *v"),
			aliases: []string{"$.b"},
		},
		"mapping": {
			// The anchor stays on the line of the key, and the mapping goes
			// below it.
			before:  stringtest.LinesLF("a: &v 1", "b: *v"),
			path:    "$.a",
			value:   map[string]any{"x": 1},
			want:    stringtest.LinesLF("a: &v", "  x: 1", "b: *v"),
			aliases: []string{"$.b"},
		},
		"sequence": {
			before:  stringtest.LinesLF("a: &v 1", "b: *v"),
			path:    "$.a",
			value:   []any{1, 2},
			want:    stringtest.LinesLF("a: &v", "  - 1", "  - 2", "b: *v"),
			aliases: []string{"$.b"},
		},
		"block scalar": {
			before:  stringtest.LinesLF("a: &v 1", "b: *v"),
			path:    "$.a",
			value:   "x\ny\n",
			want:    stringtest.LinesLF("a: &v |", "  x", "  y", "b: *v"),
			aliases: []string{"$.b"},
		},
		"block scalar over a block scalar": {
			before:  stringtest.LinesLF("a: &v |", "  x", "b: *v"),
			path:    "$.a",
			value:   "y\nz\n",
			want:    stringtest.LinesLF("a: &v |", "  y", "  z", "b: *v"),
			aliases: []string{"$.b"},
		},
		"scalar over a block mapping": {
			before:  stringtest.LinesLF("a: &v", "  x: 1", "b: *v"),
			path:    "$.a",
			value:   5,
			want:    stringtest.LinesLF("a: &v 5", "b: *v"),
			aliases: []string{"$.b"},
		},
		"sequence over a block sequence": {
			before:  stringtest.LinesLF("a: &v", "  - 1", "b: *v"),
			path:    "$.a",
			value:   []any{2, 3},
			want:    stringtest.LinesLF("a: &v", "  - 2", "  - 3", "b: *v"),
			aliases: []string{"$.b"},
		},
		"item": {
			before:  stringtest.LinesLF("l:", "  - &v 1", "b: *v"),
			path:    "$.l[0]",
			value:   2,
			want:    stringtest.LinesLF("l:", "  - &v 2", "b: *v"),
			aliases: []string{"$.b"},
		},
		"mapping as an item": {
			// The mapping goes below the "-", so the anchor does not land on
			// its first key.
			before:  stringtest.LinesLF("l:", "  - &v 1", "b: *v"),
			path:    "$.l[0]",
			value:   map[string]any{"a": 1, "b": 2},
			want:    stringtest.LinesLF("l:", "  - &v", "    a: 1", "    b: 2", "b: *v"),
			aliases: []string{"$.b"},
		},
		"sequence as an item": {
			before:  stringtest.LinesLF("l:", "  - &v 1", "b: *v"),
			path:    "$.l[0]",
			value:   []any{1, 2},
			want:    stringtest.LinesLF("l:", "  - &v", "    - 1", "    - 2", "b: *v"),
			aliases: []string{"$.b"},
		},
		"sequence as an item of a sequence at the column of its key": {
			// YAML nests the sequence below the "-" only past its column, though
			// the sequence of l starts at the column of its key.
			before:  stringtest.LinesLF("l:", "- &v 1", "b: *v"),
			path:    "$.l[0]",
			value:   []any{1, 2},
			want:    stringtest.LinesLF("l:", "- &v", "  - 1", "  - 2", "b: *v"),
			aliases: []string{"$.b"},
		},
		"sequence over a block sequence of an item of a sequence at the column of its key": {
			before:  stringtest.LinesLF("l:", "- &v", "  - 3", "b: *v"),
			path:    "$.l[0]",
			value:   []any{1, 2},
			want:    stringtest.LinesLF("l:", "- &v", "  - 1", "  - 2", "b: *v"),
			aliases: []string{"$.b"},
		},
		"block scalar as an item": {
			before:  stringtest.LinesLF("l:", "  - &v 1", "b: *v"),
			path:    "$.l[0]",
			value:   "x\ny\n",
			want:    stringtest.LinesLF("l:", "  - &v |", "    x", "    y", "b: *v"),
			aliases: []string{"$.b"},
		},
		"nil": {
			before:  stringtest.LinesLF("a: &v 1", "b: *v"),
			path:    "$.a",
			value:   nil,
			want:    stringtest.LinesLF("a: &v null", "b: *v"),
			aliases: []string{"$.b"},
		},
		"item of a flow sequence": {
			before:  "f: [&v 1, *v]\n",
			path:    "$.f[0]",
			value:   2,
			want:    "f: [&v 2, *v]\n",
			aliases: []string{"$.f[1]"},
		},
		"mapping over a flow mapping": {
			before:  stringtest.LinesLF("a: &v {k: 1}", "b: *v"),
			path:    "$.a",
			value:   map[string]any{"k": 2},
			want:    stringtest.LinesLF("a: &v", "  k: 2", "b: *v"),
			aliases: []string{"$.b"},
		},
		"value that two aliases read": {
			before:  stringtest.LinesLF("a: &v 1", "b: [*v, *v]"),
			path:    "$.a",
			value:   map[string]any{"k": 1},
			want:    stringtest.LinesLF("a: &v", "  k: 1", "b: [*v, *v]"),
			aliases: []string{"$.b[0]", "$.b[1]"},
		},
		"mapping that a merge key reads": {
			before:  stringtest.LinesLF("d: &d", "  x: 1", "m:", "  <<: *d"),
			path:    "$.d",
			value:   map[string]any{"y": 2},
			want:    stringtest.LinesLF("d: &d", `  "y": 2`, "m:", "  <<: *d"),
			aliases: []string{"$.m"},
		},
		"scalar with a tag after the anchor": {
			// The tag goes with the old value.
			before:  stringtest.LinesLF("a: &v !t 1", "b: *v"),
			path:    "$.a",
			value:   2,
			want:    stringtest.LinesLF("a: &v 2", "b: *v"),
			aliases: []string{"$.b"},
		},
		"scalar over a mapping with a comment after the anchor": {
			before:  stringtest.LinesLF("a: &v # c", "  x: 1", "b: *v"),
			path:    "$.a",
			value:   3,
			want:    stringtest.LinesLF("a: &v 3 # c", "b: *v"),
			aliases: []string{"$.b"},
		},
		"tagged scalar of a marshaler": {
			before:  stringtest.LinesLF("a: &v 1", "b: *v"),
			path:    "$.a",
			value:   yamlMarshaler("!t x"),
			want:    stringtest.LinesLF("a: &v !t x", "b: *v"),
			aliases: []string{"$.b"},
		},
		"tagged flow sequence of a marshaler": {
			before:  stringtest.LinesLF("a: &v 1", "b: *v"),
			path:    "$.a",
			value:   yamlMarshaler("!t [1]"),
			want:    stringtest.LinesLF("a: &v !t [1]", "b: *v"),
			aliases: []string{"$.b"},
		},
		"tagged block mapping of a marshaler": {
			// The tag stays on the line of the anchor, and the mapping goes
			// below them.
			before:  stringtest.LinesLF("a: &v 1", "b: *v"),
			path:    "$.a",
			value:   yamlMarshaler("!t\nk: 1\n"),
			want:    stringtest.LinesLF("a: &v !t", "  k: 1", "b: *v"),
			aliases: []string{"$.b"},
		},
		"value without an alias": {
			// The anchor stays without a reader too.
			before: stringtest.LinesLF("a: &v 1", "b: 2"),
			path:   "$.a",
			value:  3,
			want:   stringtest.LinesLF("a: &v 3", "b: 2"),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before)
			path := paths.MustParse(tc.path)

			err := applyAt(t, d, path, func(ctx context.Context, n *edit.Node) error {
				return n.Set(ctx, tc.value)
			})
			requireEdit(t, d, err, "", tc.want)

			err = d.Apply(t.Context(), paths.Doc(), func(ctx context.Context, root *edit.Node) error {
				want, err := root.At(path).Decode[any](ctx)
				require.NoError(t, err)

				for _, alias := range tc.aliases {
					got, err := root.At(paths.MustParse(alias)).Decode[any](ctx)
					require.NoError(t, err)
					require.Equal(t, want, got, alias)
				}

				return nil
			})
			require.NoError(t, err)
		})
	}
}

func TestNode_Set_Unsupported(t *testing.T) {
	t.Parallel()

	const before = "a: 1\nb: 2\n"

	// Each Set at $.b passes a value whose YAML holds a construct that the
	// engine does not write.
	tcs := map[string]struct {
		value any
		// The first line of the error, or "" for a Set that passes.
		err  string
		want string
		// The construct of the error.
		construct edit.Construct
	}{
		"anchor in the YAML of a marshaler": {
			value:     yamlMarshaler("{x: &x 1}"),
			err:       "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 sets $.b to a value whose YAML holds the anchor &x, and cafe does not write anchors or aliases",
			construct: edit.ConstructValueAlias,
		},
		"alias in the YAML of a marshaler": {
			// The YAML lacks the anchor of the alias, so go-yaml's parse of the
			// encode fails, and the alias explains the failure.
			value:     yamlMarshaler("[*zz]"),
			err:       "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 sets $.b to a value whose YAML holds the alias *zz, and cafe does not write anchors or aliases",
			construct: edit.ConstructValueAlias,
		},
		"explicit key in the YAML of a marshaler": {
			value:     yamlMarshaler("? a\n: 1\n"),
			err:       "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 sets $.b to a value whose YAML holds an explicit key, and cafe does not write explicit keys",
			construct: edit.ConstructExplicitKey,
		},
		"merge key in the YAML of a marshaler": {
			value:     yamlMarshaler("<<: {a: 1}\nb: 2\n"),
			err:       "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 sets $.b to a value whose YAML holds a merge key, and cafe does not write merge keys",
			construct: edit.ConstructMergeForm,
		},
		"anchor in the YAML of a context marshaler": {
			value:     contextYAMLMarshaler("{x: &x 1}"),
			err:       "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 sets $.b to a value whose YAML holds the anchor &x, and cafe does not write anchors or aliases",
			construct: edit.ConstructValueAlias,
		},
		"alias in the YAML of a context marshaler": {
			value:     contextYAMLMarshaler("[*zz]"),
			err:       "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 sets $.b to a value whose YAML holds the alias *zz, and cafe does not write anchors or aliases",
			construct: edit.ConstructValueAlias,
		},
		"explicit key in the YAML of a context marshaler": {
			value:     contextYAMLMarshaler("? a\n: 1\n"),
			err:       "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 sets $.b to a value whose YAML holds an explicit key, and cafe does not write explicit keys",
			construct: edit.ConstructExplicitKey,
		},
		"merge key in the YAML of a context marshaler": {
			value:     contextYAMLMarshaler("<<: {a: 1}\n"),
			err:       "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 sets $.b to a value whose YAML holds a merge key, and cafe does not write merge keys",
			construct: edit.ConstructMergeForm,
		},
		"anchor in the YAML of a marshaler that a value marshaler returns": {
			value:     valueMarshaler{v: yamlMarshaler("{x: &x 1}")},
			err:       "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 sets $.b to a value whose YAML holds the anchor &x, and cafe does not write anchors or aliases",
			construct: edit.ConstructValueAlias,
		},
		"explicit key in the YAML of a marshaler that a context value marshaler returns": {
			value:     contextMarshaler{v: yamlMarshaler("? a\n: 1\n")},
			err:       "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 sets $.b to a value whose YAML holds an explicit key, and cafe does not write explicit keys",
			construct: edit.ConstructExplicitKey,
		},
		"comment inside a flow collection in the YAML of a marshaler": {
			// The encoder drops the comment, and the finder reads the YAML of
			// the marshaler.
			value:     yamlMarshaler("[1, # c\n 2]\n"),
			err:       "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 sets $.b to a value whose YAML holds a comment inside a flow collection, and cafe does not write comments inside flow collections",
			construct: edit.ConstructFlowLayout,
		},
		"pair without braces in the YAML of a marshaler": {
			value:     yamlMarshaler("[a: 1]\n"),
			err:       "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 sets $.b to a value whose YAML holds a pair without braces in a flow sequence, and cafe does not write pairs without braces in flow sequences",
			construct: edit.ConstructFlowLayout,
		},
		"tag kind in the YAML of a marshaler": {
			value:     yamlMarshaler("!!set {a: null}\n"),
			err:       "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 sets $.b to a value whose YAML holds the tag !!set, and cafe does not write such YAML",
			construct: edit.ConstructTagKind,
		},
		"key that reads as another value in the YAML of a marshaler": {
			value:     yamlMarshaler("0x10: a\n"),
			err:       "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 sets $.b to a value whose YAML holds a key that reads as another value than its text, such as 0x10, which reads as 16, and cafe does not write such YAML",
			construct: edit.ConstructKeyText,
		},
		"anchor before a tagged alias in the YAML of a marshaler": {
			// The anchor comes before the properties of the alias.
			value:     yamlMarshaler("[&x 1, !t *x]\n"),
			err:       "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 sets $.b to a value whose YAML holds the anchor &x, and cafe does not write anchors or aliases",
			construct: edit.ConstructValueAlias,
		},
		"float key of a map": {
			// The encoder writes the key as 1.0, which reads as 1.
			value: map[any]any{1.0: "x"},
			err: "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 sets $.b to a value whose YAML holds a key that reads " +
				"as another value than its text, such as 1.0, which reads as 1, and cafe does not write such YAML\n" +
				"|-- make the key a Go string, as in \"1.0\"\n" +
				"`-- cafe.jacobcolvin.com/v1 sets $.b from this value",
			construct: edit.ConstructKeyText,
		},
		"anchor and alias options of struct tags": {
			value: anchoredFields{A: map[string]int{"k": 1}, B: map[string]int{"k": 1}},
			err: "test.yaml:2:4: $.b: cafe.jacobcolvin.com/v1 sets $.b to a value whose YAML holds the anchor &x, " +
				"and cafe does not write anchors or aliases\n" +
				"|-- remove the anchor and alias options from the yaml tags of the fields of the value\n" +
				"`-- cafe.jacobcolvin.com/v1 sets $.b from this value",
			construct: edit.ConstructValueAlias,
		},
		"string key that YAML reads as a merge key": {
			// Control: the encoder quotes the key, so go-yaml reads the
			// string.
			value: map[string]any{"<<": 1},
			want:  "a: 1\nb: {\"<<\": 1}\n",
		},
		"string key that YAML reads as an explicit key": {
			// Control: the encoder quotes the key.
			value: map[string]any{"? x": 1},
			want:  "a: 1\nb: {\"? x\": 1}\n",
		},
		"string key that YAML reads as infinity": {
			// Control: the encoder quotes the key.
			value: map[string]any{".inf": 1},
			want:  "a: 1\nb: {\".inf\": 1}\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, before)

			err := applyAt(t, d, paths.Doc().Child("b"), func(ctx context.Context, n *edit.Node) error {
				return n.Set(ctx, tc.value)
			})
			if tc.err == "" {
				requireEdit(t, d, err, "", tc.want)

				return
			}

			require.ErrorIs(t, err, edit.ErrUnsupported)
			require.NotErrorIs(t, err, edit.ErrEngine)
			require.Contains(t, niceyaml.FormatError(err), tc.err)
			require.Equal(t, before, d.Text())

			var u *edit.UnsupportedError

			require.ErrorAs(t, err, &u)
			require.Equal(t, tc.construct, u.Construct)
			require.True(t, u.InValue)
			require.Empty(t, u.Related)
			require.False(t, niceyaml.IsInvalid(err))
		})
	}
}

// selfMap returns a map whose entry holds the map.
func selfMap() map[string]any {
	m := map[string]any{"k": 1}
	m["a"] = m

	return m
}

// selfSlice returns a slice whose item holds the slice.
func selfSlice() []any {
	s := []any{nil}
	s[0] = s

	return s
}

// selfPointer returns a pointer to a value that holds the pointer.
func selfPointer() *any {
	var v any

	p := &v
	v = p

	return p
}

// link is a struct that a field can point back to.
type link struct {
	Parent *link  `yaml:"-"`
	Next   *link  `yaml:"next,omitempty"`
	Name   string `yaml:"name"`
}

// selfStruct returns a struct whose field points to the struct.
func selfStruct() *link {
	l := &link{Name: "l"}
	l.Next = l

	return l
}

// childStruct returns a struct whose field that the encoder skips points
// to the struct.
func childStruct() *link {
	l := &link{Name: "c"}
	l.Parent = l

	return l
}

// sharedMap returns a map whose two entries hold one map.
func sharedMap() map[string]any {
	inner := map[string]any{"k": 1}

	return map[string]any{"p": inner, "q": inner}
}

// anchoredFields is a struct whose yaml tags write an anchor and an alias.
type anchoredFields struct {
	A map[string]int `yaml:"a,anchor=x"`
	B map[string]int `yaml:"b,alias=x"`
}

// valueMarshaler marshals as the value that it holds.
type valueMarshaler struct{ v any }

// MarshalYAML returns the value of m.
func (m valueMarshaler) MarshalYAML() (any, error) {
	return m.v, nil
}

// contextMarshaler marshals as the value that it holds, with a context.
type contextMarshaler struct{ v any }

// MarshalYAML returns the value of m.
func (m contextMarshaler) MarshalYAML(context.Context) (any, error) {
	return m.v, nil
}

// failingMarshaler is a value whose MarshalYAML method fails.
type failingMarshaler int

// MarshalYAML returns an error.
func (failingMarshaler) MarshalYAML() (any, error) {
	return nil, errors.New("the marshaler fails")
}

// selfMarshaler marshals as itself.
type selfMarshaler int

// MarshalYAML returns m.
func (m selfMarshaler) MarshalYAML() (any, error) {
	return m, nil
}

// copyMarshaler marshals as a new pointer to a copy of itself.
type copyMarshaler struct{ n int }

// MarshalYAML returns a pointer to a copy of m.
func (m *copyMarshaler) MarshalYAML() (any, error) {
	return &copyMarshaler{n: m.n}, nil
}

// countMarshaler marshals as the count before it, and 0 as "end". A
// negative count never reaches 0.
type countMarshaler int

// MarshalYAML returns the count before c, or "end" for 0.
func (c countMarshaler) MarshalYAML() (any, error) {
	if c == 0 {
		return "end", nil
	}

	return c - 1, nil
}

func TestNode_Set_CreatedPlace(t *testing.T) {
	t.Parallel()

	const (
		plain = "m:\n  a: 1\n"
		alias = "base: &b\n  a: 1\nmd: *b\n"
		item  = "base: &b\n  a: 1\nl:\n  - *b\n"
	)

	// Each problem of a Set at a place that it creates binds at the key or
	// the "-" of the nearest value above it that the user wrote, with a
	// detail that names the Set and the path of the place as the text
	// spells it once the Set writes it (fix19 F6 and F8).
	tcs := map[string]struct {
		value  any
		before string
		path   string
		// The first line of the error, and the lines of its last details.
		err, detail string
		// Whether the error matches edit.ErrEngine.
		engine bool
	}{
		"anchor in the YAML of a value at a place that the set creates": {
			before: plain,
			path:   "$.m.z",
			value:  yamlMarshaler("&q 1"),
			err:    "test.yaml:1:1: $.m: cafe.jacobcolvin.com/v1 sets $.m.z to a value whose YAML holds the anchor &q",
			detail: "`-- cafe.jacobcolvin.com/v1 sets $.m.z\n",
		},
		"anchor in the YAML of a value at a place that the set creates through an alias": {
			before: alias,
			path:   "$.md.z",
			value:  yamlMarshaler("&q 1"),
			// An alias binds at itself, as an error at its value does (fix20 B2).
			err:    "test.yaml:3:5: $.md: cafe.jacobcolvin.com/v1 sets $.md.z to a value whose YAML holds the anchor &q",
			detail: "test.yaml:3:5: $.md: cafe.jacobcolvin.com/v1 sets $.md.z to a value whose YAML holds the anchor &q, and cafe does not write anchors or aliases\n",
		},
		"control: anchor in the YAML of a value at a place that the set replaces": {
			before: plain,
			path:   "$.m.a",
			value:  yamlMarshaler("&q 1"),
			err:    "test.yaml:2:6: $.m.a: cafe.jacobcolvin.com/v1 sets $.m.a to a value whose YAML holds the anchor &q",
			detail: "|-- return the YAML of edit_test.yamlMarshaler without anchors and aliases\n" +
				"`-- cafe.jacobcolvin.com/v1 sets $.m.a from this value\n",
		},
		"character that the encoder writes as it is at a place that the set creates through an alias": {
			before: alias,
			path:   "$.md.z",
			value:  "x\u0001y",
			err:    "test.yaml:3:5: $.md: go-yaml's encoder writes U+0001 as it is",
			detail: "test.yaml:3:5: $.md: go-yaml's encoder writes U+0001 as it is, and YAML does not allow that in a text\n",
			engine: true,
		},
		"character that the encoder writes as it is at an item that the set creates through an alias": {
			before: item,
			path:   "$.l[0].z",
			value:  "x\u0001y",
			err:    "test.yaml:4:5: $.l[0]: go-yaml's encoder writes U+0001 as it is",
			detail: "test.yaml:4:5: $.l[0]: go-yaml's encoder writes U+0001 as it is, and YAML does not allow that in a text\n",
			engine: true,
		},
		"character that the encoder writes as it is at a place that the set creates below an alias": {
			before: "base: &b\n  sub: {k: 1}\nmd: *b\n",
			path:   "$.md.sub.z",
			value:  "x\u0001y",
			err:    "test.yaml:2:3: $.base.sub: go-yaml's encoder writes U+0001 as it is",
			detail: "test.yaml:2:3: $.base.sub: go-yaml's encoder writes U+0001 as it is, and YAML does not allow that in a text\n",
			engine: true,
		},
		"character that the encoder writes as it is through an alias of a value that a merge key brings in": {
			// No path names the value of a << merge entry, and $.m.z reads the
			// own z, so the detail names the place by the path of the Set.
			before: "m:\n  <<: &b {a: 1}\n  z: 0\nl:\n  - *b\n",
			path:   "$.l[0].z",
			value:  "x\u0001y",
			err:    "test.yaml:5:5: $.l[0]: go-yaml's encoder writes U+0001 as it is",
			detail: "`-- cafe.jacobcolvin.com/v1 sets $.l[0].z\n",
			engine: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before)

			err := applyAt(t, d, paths.MustParse(tc.path), func(ctx context.Context, n *edit.Node) error {
				return n.Set(ctx, tc.value)
			})
			require.Error(t, err)
			require.Equal(t, tc.engine, errors.Is(err, edit.ErrEngine))

			got := niceyaml.FormatError(err)
			require.Contains(t, got, tc.err)
			require.Contains(t, got, tc.detail)
			require.Equal(t, tc.before, d.Text())
		})
	}
}

// yamlMarshaler marshals as the YAML text that it holds.
type yamlMarshaler string

// MarshalYAML returns the text of m.
func (m yamlMarshaler) MarshalYAML() ([]byte, error) {
	return []byte(m), nil
}

// contextYAMLMarshaler marshals as the YAML text that it holds, with a
// context.
type contextYAMLMarshaler string

// MarshalYAML returns the text of m.
func (m contextYAMLMarshaler) MarshalYAML(context.Context) ([]byte, error) {
	return []byte(m), nil
}

// textMarshaler marshals as the text that it holds.
type textMarshaler string

// MarshalText returns the text of m.
func (m textMarshaler) MarshalText() ([]byte, error) {
	return []byte(m), nil
}

// panicYAMLMarshaler panics with the name of its type when it marshals.
type panicYAMLMarshaler struct{}

// MarshalYAML panics.
func (panicYAMLMarshaler) MarshalYAML() ([]byte, error) {
	panic("edit_test.panicYAMLMarshaler")
}

// panicContextYAMLMarshaler panics with the name of its type when it
// marshals.
type panicContextYAMLMarshaler struct{}

// MarshalYAML panics.
func (panicContextYAMLMarshaler) MarshalYAML(context.Context) ([]byte, error) {
	panic("edit_test.panicContextYAMLMarshaler")
}

// panicMarshaler panics with the name of its type when it marshals.
type panicMarshaler struct{}

// MarshalYAML panics.
func (panicMarshaler) MarshalYAML() (any, error) {
	panic("edit_test.panicMarshaler")
}

// panicContextMarshaler panics with the name of its type when it marshals.
type panicContextMarshaler struct{}

// MarshalYAML panics.
func (panicContextMarshaler) MarshalYAML(context.Context) (any, error) {
	panic("edit_test.panicContextMarshaler")
}

// panicTextMarshaler panics with the name of its type when it marshals.
type panicTextMarshaler struct{}

// MarshalText panics.
func (panicTextMarshaler) MarshalText() ([]byte, error) {
	panic("edit_test.panicTextMarshaler")
}
