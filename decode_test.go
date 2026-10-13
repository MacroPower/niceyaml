package niceyaml_test

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"
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

var (
	// Sentinel errors for mock validators.
	errSchemaValidationFailed = errors.New("schema validation failed: name cannot be 'invalid'")
	errNameRequired           = errors.New("name is required")
	errDocumentRejected       = errors.New("document rejected")
	errPlainValidation        = errors.New("plain validation failure")

	// The error rejectingUnmarshaler and wrappingUnmarshaler report from
	// their own decode.
	errUnmarshal = errors.New("unmarshaler rejected the value")

	// The error decodeLevel reports for a name that is no level.
	errUnknownLevel = errors.New("unknown level")

	// RegisterStrict has go-yaml decode a [strictRegistered] with a
	// function that accepts every key, in every decode of the program.
	registerStrict = sync.OnceFunc(func() {
		yaml.RegisterCustomUnmarshaler(func(*strictRegistered, []byte) error { return nil })
	})

	// OpenOnlyDoc is a document whose hours hold an open and no close.
	openOnlyDoc = stringtest.LinesLF("spec:", "  hours:", "    open: 9")

	// The errors the unmarshalers of this file report.
	errBadServer    = errors.New("bad server")
	errRepoRequired = errors.New("repo is required")
	errPortRequired = errors.New("port is required")

	// Has go-yaml decode a problemRegistered as decodePulled decodes a
	// problemPulled, in every decode of the program.
	registerPulled = sync.OnceFunc(func() {
		yaml.RegisterCustomUnmarshalerContext(func(ctx context.Context, p *problemRegistered, data []byte) error {
			return decodePulled(ctx, (*problemPulled)(p), func(v any) error {
				err := yaml.Unmarshal(data, v)
				if err != nil {
					return fmt.Errorf("decode image: %w", err)
				}

				return nil
			})
		})
	})

	// The calls of the UnmarshalText method of [problemLevel], by text.
	levelCalls callCounter

	// The calls of the UnmarshalJSON method of [problemEither].
	eitherCalls callCounter

	// The calls of the UnmarshalText method of [problemOnce], by text.
	onceCalls callCounter

	// The error tier reports for text it does not know.
	errUnknownTier = errors.New("unknown tier")

	// The error a span reports for a to below its from.
	errSpan = errors.New("to is below from")
)

// decodedAs decodes n into a T through [niceyaml.Node.Decode]. A table
// that lists decodes into several types holds it as a func of one type.
func decodedAs[T any](t *testing.T, n *niceyaml.Node) (any, error) {
	t.Helper()

	return n.Decode[T](t.Context())
}

// requireDocumentValues decodes every document of input into an any and
// compares the values with want. When kept is not empty, the tree of the
// file must also hold that comment.
func requireDocumentValues(t *testing.T, input, kept string, want []any) {
	t.Helper()

	source := niceyaml.NewSourceFromString(input)

	file, err := source.File()
	require.NoError(t, err)

	if kept != "" {
		assert.Contains(t, file.String(), kept)
	}

	docs := source.Documents()

	got := make([]any, len(docs))
	for i, d := range docs {
		got[i], err = d.Decode[any](t.Context())
		require.NoError(t, err)
	}

	assert.Equal(t, want, got)
}

func TestDecode_GoYAMLErrorReachable(t *testing.T) {
	t.Parallel()

	// The binding renders the position itself, so the chain carries the
	// go-yaml message alone, but the original error stays reachable. The
	// text of the go-yaml error holds an excerpt of the source, which no
	// message of the chain may hold.
	t.Run("decode error", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "b: notanint\n")

		_, err := dd.Decode[struct{ B int }](t.Context())
		require.Error(t, err)

		yamlErr, ok := errors.AsType[yaml.Error](err)
		require.True(t, ok, "errors.As finds no yaml.Error: %v", err)
		require.Contains(t, yamlErr.Error(), "b: notanint")

		for _, msg := range chainMessages(err) {
			assert.NotContains(t, msg, "notanint", "the go-yaml excerpt leaked into the chain")
		}
	})

	t.Run("parse error", func(t *testing.T) {
		t.Parallel()

		_, err := niceyaml.NewSourceFromString("a: [\n").File()
		require.Error(t, err)

		yamlErr, ok := errors.AsType[yaml.Error](err)
		require.True(t, ok, "errors.As finds no yaml.Error: %v", err)
		require.Contains(t, yamlErr.Error(), "a: [")

		for _, msg := range chainMessages(err) {
			assert.NotContains(t, msg, "a: [", "the go-yaml excerpt leaked into the chain")
		}
	})
}

func TestDocument_Decode(t *testing.T) {
	t.Parallel()

	type testStruct struct {
		Name  string `yaml:"name"`
		Value int    `yaml:"value"`
	}

	t.Run("decodes the root into a type", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			decode func(t *testing.T, n *niceyaml.Node) (any, error)
			want   any
			input  string
		}{
			"map": {
				input:  "key: value",
				decode: decodedAs[map[string]string],
				want:   map[string]string{"key": "value"},
			},
			"struct": {
				input: stringtest.JoinLF(
					"name: test",
					"value: 42",
				),
				decode: decodedAs[testStruct],
				want:   testStruct{Name: "test", Value: 42},
			},
			"slice": {
				input: stringtest.JoinLF(
					"- one",
					"- two",
					"- three",
				),
				decode: decodedAs[[]string],
				want:   []string{"one", "two", "three"},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				got, err := tc.decode(t, yamltest.FirstDocument(t, tc.input))
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
			})
		}
	})

	t.Run("decode multiple documents", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			---
			name: first
			value: 1
			---
			name: second
			value: 2
		`)
		source := niceyaml.NewSourceFromString(input)
		d := source.Documents()

		var results []testStruct

		for _, dd := range d {
			result, err := dd.Decode[testStruct](t.Context())
			require.NoError(t, err)

			results = append(results, result)
		}

		require.Len(t, results, 2)
		assert.Equal(t, testStruct{Name: "first", Value: 1}, results[0])
		assert.Equal(t, testStruct{Name: "second", Value: 2}, results[1])
	})

	t.Run("alias with a trailing comment", func(t *testing.T) {
		t.Parallel()

		// The parser attaches a comment on the line of an alias to its
		// name, which the go-yaml decoder looks the anchor up by.
		tcs := map[string]struct {
			input   string
			path    paths.Path
			want    any
			comment string
		}{
			"mapping value": {
				input:   "base: &x 1\nref: *x # same as base\n",
				path:    paths.Current(),
				want:    map[string]any{"base": uint64(1), "ref": uint64(1)},
				comment: "# same as base",
			},
			"sequence item": {
				input:   "- &x 1\n- *x # c\n",
				path:    paths.Current(),
				want:    []any{uint64(1), uint64(1)},
				comment: "# c",
			},
			"scoped node": {
				input: stringtest.LinesLF(
					"base: &x 1",
					"sub:",
					"  ref: *x # same as base",
				),
				path:    paths.Current().Child("sub"),
				want:    map[string]any{"ref": uint64(1)},
				comment: "# same as base",
			},
			"scoped node through an anchor": {
				input: stringtest.LinesLF(
					"a: &a 1",
					"b: &b",
					"  - *a # c",
					"sub: {k: *b}",
				),
				path:    paths.Current().Child("sub"),
				want:    map[string]any{"k": []any{uint64(1)}},
				comment: "# c",
			},
			"spec example 2.10": {
				input: stringtest.Input(`
					---
					hr:
					  - Mark McGwire
					  # Following node labeled SS
					  - &SS Sammy Sosa
					rbi:
					  - *SS # Subsequent occurrence
					  - Ken Griffey
				`),
				path: paths.Current(),
				want: map[string]any{
					"hr":  []any{"Mark McGwire", "Sammy Sosa"},
					"rbi": []any{"Sammy Sosa", "Ken Griffey"},
				},
				comment: "# Subsequent occurrence",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				got, err := yamltest.At(t, dd, tc.path).Decode[any](t.Context())
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)

				// The decode leaves the tree the Source shares as it was.
				assert.Contains(t, dd.DocumentAST().String(), tc.comment)
			})
		}
	})

	t.Run("comment on a line of its own after the content", func(t *testing.T) {
		t.Parallel()

		// When it keeps comments, the go-yaml parser rejects such a comment
		// in three places. One is below a root that is not a block mapping
		// or a block sequence. One is left of the first key or "-" of such
		// a root. One is between a directive and its header. Each case
		// lists the value of every document.
		tcs := map[string]struct {
			input string
			want  []any
		}{
			"plain scalar": {
				input: "x\n# c\n",
				want:  []any{"x"},
			},
			"quoted scalar on two lines": {
				input: stringtest.LinesLF(`"a`, ` b"`, "# c"),
				want:  []any{"a b"},
			},
			"anchored scalar": {
				input: "&a x\n# c\n",
				want:  []any{"x"},
			},
			"tagged scalar": {
				input: "!!str 1\n# c\n",
				want:  []any{"1"},
			},
			"indented comment": {
				input: stringtest.LinesLF("x", "  # c"),
				want:  []any{"x"},
			},
			"comment on the line and below it": {
				input: "x # same\n# c\n",
				want:  []any{"x"},
			},
			// The parser attaches a comment only to a token that starts on
			// its line, so it reads a comment on the last line of a scalar
			// on several lines as one on a line of its own.
			"comment on the last line of a plain scalar": {
				input: stringtest.LinesLF("a", " b # c"),
				want:  []any{"a b"},
			},
			"comment on the last line of a single-quoted scalar": {
				input: stringtest.LinesLF("'x", " y' # c"),
				want:  []any{"x y"},
			},
			"comment on the last line of a double-quoted scalar above a header": {
				input: stringtest.LinesLF(
					`"multi`,
					` line" # c`,
					"---",
					"x",
				),
				want: []any{"multi line", "x"},
			},
			"comment on the last line of a tagged scalar": {
				input: stringtest.LinesLF("!!str a", " b # c"),
				want:  []any{"a b"},
			},
			"comment on the last line of an anchored scalar": {
				input: stringtest.LinesLF("&x a", " b # c"),
				want:  []any{"a b"},
			},
			"comment on the last line of a scalar above an end marker": {
				input: stringtest.LinesLF("--- a", " b # c", "..."),
				want:  []any{"a b"},
			},
			"comments on and below the last line of a scalar": {
				input: stringtest.LinesLF("a", " b # c", "# d"),
				want:  []any{"a b"},
			},
			"flow sequence": {
				input: "[1, 2]\n# c\n",
				want:  []any{[]any{uint64(1), uint64(2)}},
			},
			"flow mapping": {
				input: "{a: 1}\n# c\n",
				want:  []any{map[string]any{"a": uint64(1)}},
			},
			"block scalar": {
				input: stringtest.LinesLF("--- |", "  lit", "# c"),
				want:  []any{"lit\n"},
			},
			"scalar above an end marker": {
				input: stringtest.LinesLF("x", "# c", "...", "--- y"),
				want:  []any{"x", "y"},
			},
			"flow sequence above a header": {
				input: stringtest.LinesLF("--- [1, 2]", "# c", "# d", "--- [3]"),
				want:  []any{[]any{uint64(1), uint64(2)}, []any{uint64(3)}},
			},
			"scalar after a directive": {
				input: stringtest.LinesLF("%YAML 1.2", "---", "x", "# c"),
				want:  []any{"x"},
			},
			"comment between a directive and its header": {
				input: stringtest.LinesLF("%YAML 1.2", "# c", "---", "a: 1"),
				want:  []any{map[string]any{"a": uint64(1)}},
			},
			"comment between a tag directive and its header": {
				input: stringtest.LinesLF(
					"%TAG !e! tag:example.com,2000:",
					"# c",
					"",
					"# d",
					"---",
					"!e!x y",
				),
				want: []any{"y"},
			},
			"comment left of an indented mapping below a header": {
				input: stringtest.LinesLF(
					"---",
					"  apiVersion: v1",
					"  kind: Pod",
					"# end",
				),
				want: []any{map[string]any{"apiVersion": "v1", "kind": "Pod"}},
			},
			"comment left of an indented sequence above an end marker": {
				input: stringtest.LinesLF(
					"  - a",
					"  - b",
					"# c",
					"...",
				),
				want: []any{[]any{"a", "b"}},
			},
			"comment left of a tagged indented mapping": {
				input: stringtest.LinesLF("!!map", "  a: 1", "# c"),
				want:  []any{map[string]any{"a": uint64(1)}},
			},
			"comment left of an indented mapping below a tagged header": {
				input: stringtest.LinesLF("--- !!map", "  a: 1", "# c"),
				want:  []any{map[string]any{"a": uint64(1)}},
			},
			"comment left of an indented mapping above a header": {
				input: stringtest.LinesLF(
					"  a: 1",
					"# c",
					"---",
					"b: 2",
				),
				want: []any{map[string]any{"a": uint64(1)}, map[string]any{"b": uint64(2)}},
			},
			"comment left of an indented explicit key": {
				input: stringtest.LinesLF("  ? a", "  : 1", "# c"),
				want:  []any{map[string]any{"a": uint64(1)}},
			},
			"comment left of an indented anchored key": {
				input: stringtest.LinesLF("  &x a: 1", " # c"),
				want:  []any{map[string]any{"a": uint64(1)}},
			},
			"comment left of an indented nested mapping": {
				input: stringtest.LinesLF(
					"  a:",
					"    b: 1",
					"  # c",
					"# d",
				),
				want: []any{map[string]any{"a": map[string]any{"b": uint64(1)}}},
			},
			"comments in and left of the column of an indented mapping": {
				input: stringtest.LinesLF("  a: 1", "  # c", "# d"),
				want:  []any{map[string]any{"a": uint64(1)}},
			},
			"comment left of an indented mapping above more keys": {
				input: stringtest.LinesLF("  a: 1", "# c", "  b: 2"),
				want:  []any{map[string]any{"a": uint64(1), "b": uint64(2)}},
			},
			"comment in the column of an indented mapping": {
				input: stringtest.LinesLF("  a: 1", "  # c"),
				want:  []any{map[string]any{"a": uint64(1)}},
			},
			"comment in the column of a sequence in a mapping": {
				input: stringtest.LinesLF("a:", "- b", "# c"),
				want:  []any{map[string]any{"a": []any{"b"}}},
			},
			// The parser takes the comment below an anchor with no value
			// that ends its document as the value of the anchor, and
			// rejects the anchor without it.
			"anchor with no value": {
				input: "&a\n# c\n",
				want:  []any{nil},
			},
			"anchor with no value above an end marker": {
				input: stringtest.LinesLF("--- &a", "# c", "..."),
				want:  []any{nil},
			},
			"anchor with no value above a header": {
				input: stringtest.LinesLF("&a", "# c", "---", "b"),
				want:  []any{nil, "b"},
			},
			// In an indented root, the parser takes an anchor that directly
			// follows a "-", or a key and its ":", on its line without the
			// comment when a header or an end marker follows the comment.
			// It rejects a comment left of the first key or "-" of the
			// root.
			"anchor with no value in an indented mapping above an end marker": {
				input: stringtest.LinesLF("  a: &x", "# c", "..."),
				want:  []any{map[string]any{"a": nil}},
			},
			"anchor with no value in an indented mapping above a header": {
				input: stringtest.LinesLF(
					"  a: &x",
					"# c",
					"---",
					"b: 1",
				),
				want: []any{map[string]any{"a": nil}, map[string]any{"b": uint64(1)}},
			},
			"anchor with no value in an indented sequence above a header": {
				input: stringtest.LinesLF(
					"  - &x",
					"# c",
					"---",
					"b: 1",
				),
				want: []any{[]any{nil}, map[string]any{"b": uint64(1)}},
			},
			"anchor with no value on a later key of an indented mapping": {
				input: stringtest.LinesLF(
					"  a: 1",
					"  b: &x",
					" # c",
					"---",
					"c: 1",
				),
				want: []any{
					map[string]any{"a": uint64(1), "b": nil},
					map[string]any{"c": uint64(1)},
				},
			},
			"anchor with no value in an anchored indented mapping": {
				input: stringtest.LinesLF(
					"&r",
					"  a: &x",
					"# c",
					"...",
				),
				want: []any{map[string]any{"a": nil}},
			},
			"anchor with no value above comments left of and in the column": {
				input: stringtest.LinesLF(
					"  a: &x",
					"# c",
					"  # d",
					"...",
				),
				want: []any{map[string]any{"a": nil}},
			},
			// The parser rejects any other anchor without the comment, so
			// the comment stays in the parser input as its value even left
			// of the first key or "-" of the root. The tree holds a null in
			// its place.
			"anchor with no value after the colon of an explicit key": {
				input: stringtest.LinesLF(
					"  ? a",
					"  : &x",
					"# c",
					"...",
				),
				want: []any{map[string]any{"a": nil}},
			},
			"anchor with no value after the colon of an explicit key above a header": {
				input: stringtest.LinesLF(
					"  ? a",
					"  : &x",
					"# c",
					"---",
					"b: 2",
				),
				want: []any{map[string]any{"a": nil}, map[string]any{"b": uint64(2)}},
			},
			"anchor with no value on a line below its key": {
				input: stringtest.LinesLF(
					"  a:",
					"    &x",
					"# c",
					"...",
				),
				want: []any{map[string]any{"a": nil}},
			},
			"anchor with no value on a line below its entry": {
				input: stringtest.LinesLF(
					"  -",
					"    &x",
					"# c",
					"---",
					"b: 2",
				),
				want: []any{[]any{nil}, map[string]any{"b": uint64(2)}},
			},
			"anchor with no value after a tag": {
				input: stringtest.LinesLF("  a: !!str &x", "# c", "..."),
				want:  []any{map[string]any{"a": ""}},
			},
			"anchor with no value after the colon of a nested explicit key": {
				input: stringtest.LinesLF(
					"  a:",
					"    ? b",
					"    : &x",
					"# c",
					"...",
				),
				want: []any{map[string]any{"a": map[string]any{"b": nil}}},
			},
			// With the comment, the parser would take the node below it
			// as the value of the anchor.
			"anchor with no value above a mapping key": {
				input: stringtest.LinesLF("a: &x", "# c", "b: 1"),
				want:  []any{map[string]any{"a": nil, "b": uint64(1)}},
			},
			"anchor with no value above a sequence entry": {
				input: stringtest.LinesLF("- &x", "# c", "- 1"),
				want:  []any{[]any{nil, uint64(1)}},
			},
			"anchor with no value in a nested mapping": {
				input: stringtest.LinesLF(
					"a:",
					"  k: &x",
					"  # c",
					"  l: 1",
					"m: 2",
				),
				want: []any{map[string]any{
					"a": map[string]any{"k": nil, "l": uint64(1)},
					"m": uint64(2),
				}},
			},
			"anchor with no value above a dedent": {
				input: stringtest.LinesLF(
					"a:",
					"  b: &x",
					"  # c",
					"c: 1",
				),
				want: []any{map[string]any{
					"a": map[string]any{"b": nil},
					"c": uint64(1),
				}},
			},
			"anchor with no value above blank lines and comments": {
				input: stringtest.LinesLF(
					"a: &x",
					"",
					"# c",
					"# d",
					"",
					"b: 1",
				),
				want: []any{map[string]any{"a": nil, "b": uint64(1)}},
			},
			"anchor with no value in a mapping in a sequence": {
				input: stringtest.LinesLF("- a: &x", "  # c", "  b: 1"),
				want:  []any{[]any{map[string]any{"a": nil, "b": uint64(1)}}},
			},
			"anchor with no value in a nested sequence": {
				input: stringtest.LinesLF(
					"- - &x",
					"  # c",
					"  - 1",
					"- 2",
				),
				want: []any{[]any{[]any{nil, uint64(1)}, uint64(2)}},
			},
			// Below a comment left of its "-", the parser gives the anchor
			// a null value. Without the comment, it takes the key in the
			// column of that "-" as the value of the anchor.
			"anchor with no value in a sequence in the column of its key": {
				input: stringtest.LinesLF(
					"p:",
					"  q:",
					"  - &x",
					"# c",
					"  n: 1",
				),
				want: []any{map[string]any{
					"p": map[string]any{"q": []any{nil}, "n": uint64(1)},
				}},
			},
			"anchor with no value in a sequence in the column of a key of an entry": {
				input: stringtest.LinesLF(
					"- p:",
					"  - &x",
					"# c",
					"  n: 1",
				),
				want: []any{[]any{map[string]any{"p": []any{nil}, "n": uint64(1)}}},
			},
			// The anchor still takes the indented node below the comment.
			"anchor above a comment and its nested value": {
				input: stringtest.LinesLF(
					"a: &x",
					"# c",
					"  b: 1",
					"c: *x",
				),
				want: []any{map[string]any{
					"a": map[string]any{"b": uint64(1)},
					"c": map[string]any{"b": uint64(1)},
				}},
			},
			// Below a comment left of the key or "-" that the anchor
			// directly follows on its line, the parser gives the anchor a
			// null value and rejects the value of the anchor.
			"comment left of the key above a sequence in its column": {
				input: stringtest.LinesLF(
					"a:",
					"  b: &x",
					"# c",
					"  - 1",
				),
				want: []any{map[string]any{"a": map[string]any{"b": []any{uint64(1)}}}},
			},
			"comment left of the key above an indented value": {
				input: stringtest.LinesLF(
					"a:",
					"  b: &x",
					"# c",
					"    c: 1",
				),
				want: []any{map[string]any{
					"a": map[string]any{"b": map[string]any{"c": uint64(1)}},
				}},
			},
			"comment left of the entry above an indented value": {
				input: stringtest.LinesLF("- - &x", "# c", "    - 1"),
				want:  []any{[]any{[]any{[]any{uint64(1)}}}},
			},
			"comment left of the key below a comment on its line": {
				input: stringtest.LinesLF(
					"a:",
					"  b: &x # c",
					"# d",
					"    c: 1",
				),
				want: []any{map[string]any{
					"a": map[string]any{"b": map[string]any{"c": uint64(1)}},
				}},
			},
			// The first comment on a line of its own decides for the
			// comments below it.
			"comment left of the key below a comment in its column": {
				input: stringtest.LinesLF(
					"a:",
					"  b: &x",
					"  # c",
					"# d",
					"    c: 1",
				),
				want: []any{map[string]any{
					"a": map[string]any{"b": map[string]any{"c": uint64(1)}},
				}},
			},
			"comment in the column of the key below a comment left of it": {
				input: stringtest.LinesLF(
					"a:",
					"  b: &x",
					"# c",
					"    # d",
					"    c: 1",
				),
				want: []any{map[string]any{
					"a": map[string]any{"b": map[string]any{"c": uint64(1)}},
				}},
			},
			// The parser accepts a comment left of the "?" of an explicit
			// key above the value of the anchor.
			"comment left of an explicit key above an indented value": {
				input: stringtest.LinesLF(
					"  ? a",
					"  : &x",
					"# c",
					"    b: 2",
				),
				want: []any{map[string]any{"a": map[string]any{"b": uint64(2)}}},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				requireDocumentValues(t, tc.input, "", tc.want)
			})
		}
	})

	t.Run("anchor with no value above a comment", func(t *testing.T) {
		t.Parallel()

		// The parser takes the comment as the value of the anchor. Each
		// typed decode reads the anchored node as null, as it reads "&x ~".
		type config struct {
			A int `yaml:"a"`
		}

		tcs := map[string]struct {
			input string
			path  paths.Path
		}{
			"anchored null": {
				input: "&x ~\n",
				path:  paths.Current(),
			},
			"root": {
				input: "&x\n# c\n",
				path:  paths.Current(),
			},
			"root above a header": {
				input: stringtest.LinesLF("--- &x", "# c", "---", "b: 1"),
				path:  paths.Current(),
			},
			"mapping value": {
				input: "list: &d\n# end\n",
				path:  paths.Current().Child("list"),
			},
			"mapping value after a reused anchor name": {
				input: stringtest.LinesLF("x: &d 1", "y: *d", "list: &d", "# end"),
				path:  paths.Current().Child("list"),
			},
			"sequence item": {
				input: "- &d\n# end\n",
				path:  paths.Current().Index(0),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				node := yamltest.At(t, yamltest.FirstDocument(t, tc.input), tc.path)

				cfg := &config{A: 7}
				require.NoError(t, node.DecodeInto(t.Context(), &cfg))
				assert.Nil(t, cfg)

				list, err := node.Decode[[]int](t.Context())
				require.NoError(t, err)
				assert.Nil(t, list)

				str := new("keep")
				require.NoError(t, node.DecodeInto(t.Context(), &str))
				assert.Nil(t, str)
			})
		}
	})

	t.Run("anchored field with no value above a comment", func(t *testing.T) {
		t.Parallel()

		type config struct {
			List []int `yaml:"list"`
		}

		tcs := map[string]struct {
			input string
		}{
			"anchored null":                    {input: "list: &d ~\n"},
			"field":                            {input: "list: &d\n# end\n"},
			"field after a reused anchor name": {input: stringtest.LinesLF("x: &d 1", "y: *d", "list: &d", "# end")},
			"field above a header":             {input: stringtest.LinesLF("list: &d", "# end", "---", "b: 1")},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				got, err := yamltest.FirstDocument(t, tc.input).Decode[config](t.Context())
				require.NoError(t, err)
				assert.Equal(t, config{}, got)
			})
		}
	})

	t.Run("int-tagged integer", func(t *testing.T) {
		t.Parallel()

		// The go-yaml decoder reads an integer under a !!int tag as a Go
		// int, which it rejects for every integer type.
		type config struct {
			Version int `yaml:"version"`
		}

		tcs := map[string]struct {
			input   string
			want    int64
			wantAny any
		}{
			"integer": {
				input:   "version: !!int 16\n",
				want:    16,
				wantAny: uint64(16),
			},
			"hex integer": {
				input:   "version: !!int 0x10\n",
				want:    16,
				wantAny: uint64(16),
			},
			"negative integer": {
				input:   "version: !!int -5\n",
				want:    -5,
				wantAny: int64(-5),
			},
			"anchor on the tag": {
				input:   "version: &v !!int 0x10\n",
				want:    16,
				wantAny: uint64(16),
			},
			"tag on an anchor": {
				input:   "version: !!int &v 0x10\n",
				want:    16,
				wantAny: uint64(16),
			},
			"alias to a tagged anchor": {
				input:   "base: &k !!int 16\nversion: *k\n",
				want:    16,
				wantAny: uint64(16),
			},
			"after a reused anchor name": {
				input: stringtest.LinesLF(
					"a: &x 1",
					"b: &x 2",
					"version: !!int 16",
				),
				want:    16,
				wantAny: uint64(16),
			},
			"alias to a tagged anchor of a reused name": {
				input: stringtest.LinesLF(
					"a: &x 1",
					"b: &x !!int 16",
					"version: *x",
				),
				want:    16,
				wantAny: uint64(16),
			},
			"after nested collections": {
				input: stringtest.LinesLF(
					"spec:",
					"  name: x",
					"  ports: [80, {n: !!str 1}]",
					"version: !!int 0x10",
				),
				want:    16,
				wantAny: uint64(16),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				got, err := dd.Decode[config](t.Context())
				require.NoError(t, err)
				assert.Equal(t, config{Version: int(tc.want)}, got)

				node := yamltest.At(t, dd, paths.Current().Child("version"))

				gotInt, err := node.Decode[int64](t.Context())
				require.NoError(t, err)
				assert.Equal(t, tc.want, gotInt)

				gotAny, err := node.Decode[any](t.Context())
				require.NoError(t, err)
				assert.Equal(t, tc.wantAny, gotAny)
			})
		}
	})

	t.Run("int-tagged integer in the text of an UnmarshalYAML method", func(t *testing.T) {
		t.Parallel()

		// The text spells the tag, and the space before it, as the
		// document does, so it parses to the same mapping again.
		type wrapper struct {
			Spec rawText `yaml:"spec"`
		}

		tcs := map[string]struct {
			input string
			want  string
		}{
			"tag": {
				input: stringtest.LinesLF("spec:", "  version: !!int 16"),
				want:  "version: !!int 16\n",
			},
			"tag on an anchor": {
				input: stringtest.LinesLF("spec:", "  version: !!int &x 16"),
				want:  "version: !!int &x 16\n",
			},
			"anchors on both sides of the tag": {
				input: stringtest.LinesLF("spec:", "  version: &x !!int &y 16"),
				want:  "version: &x !!int &y 16\n",
			},
			"after a reused anchor name": {
				input: stringtest.LinesLF(
					"a: &x 1",
					"b: &x 2",
					"spec:",
					"  version: !!int 16",
				),
				want: "version: !!int 16\n",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				got, err := dd.Decode[wrapper](t.Context())
				require.NoError(t, err)
				assert.Equal(t, tc.want, got.Spec.text)

				var spec map[string]any

				require.NoError(t, yaml.Unmarshal([]byte(got.Spec.text), &spec))
				assert.Equal(t, map[string]any{"version": 16}, spec)

				whole, err := dd.Decode[rawText](t.Context())
				require.NoError(t, err)
				assert.Equal(t, tc.input, whole.text)
			})
		}
	})

	t.Run("comment on a line of its own inside a flow collection", func(t *testing.T) {
		t.Parallel()

		// The go-yaml parser rejects such a comment before a key of a flow
		// mapping, and before a ",", ":", "]", or "}", when it keeps
		// comments. It reads one before any other node as the head comment
		// of that node, so the tree keeps it. Each case lists the value of
		// every document and the comment the tree keeps, if any.
		tcs := map[string]struct {
			input string
			kept  string
			want  []any
		}{
			"comment at the start of a flow mapping": {
				input: stringtest.LinesLF(
					"config: {",
					"  # production",
					"  name: prod",
					"}",
				),
				want: []any{map[string]any{"config": map[string]any{"name": "prod"}}},
			},
			"comment between flow mapping entries": {
				input: stringtest.LinesLF(
					"a: {",
					"  b: 1,",
					"  # c",
					"  c: 2",
					"}",
				),
				want: []any{map[string]any{"a": map[string]any{"b": uint64(1), "c": uint64(2)}}},
			},
			"comment before a comma in a flow mapping": {
				input: stringtest.LinesLF(
					"a: {",
					"  b: 1",
					"  # c",
					"  , c: 2",
					"}",
				),
				want: []any{map[string]any{"a": map[string]any{"b": uint64(1), "c": uint64(2)}}},
			},
			"comments before a closing brace": {
				input: stringtest.LinesLF(
					"a: {",
					"  b: 1",
					"  # c",
					"  # d",
					"}",
				),
				want: []any{map[string]any{"a": map[string]any{"b": uint64(1)}}},
			},
			"comment after a trailing comma in a flow mapping": {
				input: stringtest.LinesLF(
					"a: {",
					"  b: 1,",
					"  # c",
					"}",
				),
				want: []any{map[string]any{"a": map[string]any{"b": uint64(1)}}},
			},
			"comment in an empty flow mapping": {
				input: stringtest.LinesLF("a: {", "  # c", "}"),
				want:  []any{map[string]any{"a": map[string]any{}}},
			},
			"comment between an explicit key and its colon": {
				input: stringtest.LinesLF(
					"a: {",
					"  ? b",
					"  # c",
					"  : 1",
					"}",
				),
				want: []any{map[string]any{"a": map[string]any{"b": uint64(1)}}},
			},
			"comment after an explicit key indicator in a flow mapping": {
				input: stringtest.LinesLF("a: {? ", "  # c", "  b : 1, c: d}"),
				want:  []any{map[string]any{"a": map[string]any{"b": uint64(1), "c": "d"}}},
			},
			"comment after an explicit key indicator in a flow sequence": {
				input: stringtest.LinesLF("a: [? ", "  # c", "  b : 1, c]"),
				want:  []any{map[string]any{"a": []any{map[string]any{"b": uint64(1)}, "c"}}},
			},
			"comments after an explicit key indicator in a root flow sequence": {
				input: stringtest.LinesLF(
					"[? ",
					"  # c",
					"  # d",
					"  b : 1, c]",
				),
				want: []any{[]any{map[string]any{"b": uint64(1)}, "c"}},
			},
			"comment before a closing bracket": {
				input: stringtest.LinesLF(
					"a: [",
					"  1",
					"  # c",
					"]",
				),
				want: []any{map[string]any{"a": []any{uint64(1)}}},
			},
			"comment before a comma in a flow sequence": {
				input: stringtest.LinesLF(
					"a: [",
					"  1",
					"  # c",
					"  , 2",
					"]",
				),
				want: []any{map[string]any{"a": []any{uint64(1), uint64(2)}}},
			},
			"comment after a trailing comma in a flow sequence": {
				input: stringtest.LinesLF(
					"a: [",
					"  1,",
					"  # c",
					"]",
				),
				want: []any{map[string]any{"a": []any{uint64(1)}}},
			},
			"comment in an empty flow sequence": {
				input: stringtest.LinesLF("a: [", "  # c", "]"),
				want:  []any{map[string]any{"a": []any{}}},
			},
			"comment below a comment on the line of an entry": {
				input: stringtest.LinesLF(
					"a: [",
					"  1 # c",
					"  # d",
					"]",
				),
				kept: "# c",
				want: []any{map[string]any{"a": []any{uint64(1)}}},
			},
			// The parser reads a comment on the last line of a scalar on
			// several lines as one on a line of its own.
			"comment on the last line of an entry before a closing bracket": {
				input: stringtest.LinesLF("[a", " b # c", "]"),
				want:  []any{[]any{"a b"}},
			},
			"comment on the last line of an entry before a comma": {
				input: stringtest.LinesLF("[a", " b # c", ", d]"),
				want:  []any{[]any{"a b", "d"}},
			},
			"comment on the last line of a value before a closing brace": {
				input: stringtest.LinesLF(`{a: "x`, ` y" # c`, "}"),
				want:  []any{map[string]any{"a": "x y"}},
			},
			"flow mapping in a flow sequence": {
				input: stringtest.LinesLF(
					"a: [1, {",
					"  # c",
					"  b: 2",
					"}]",
				),
				want: []any{map[string]any{"a": []any{uint64(1), map[string]any{"b": uint64(2)}}}},
			},
			"flow mapping at the root": {
				input: stringtest.LinesLF(
					"{",
					"  # c",
					"  a: 1",
					"}",
				),
				want: []any{map[string]any{"a": uint64(1)}},
			},
			"crlf line endings": {
				input: stringtest.LinesCRLF(
					"config: {",
					"  # production",
					"  name: prod",
					"}",
				),
				want: []any{map[string]any{"config": map[string]any{"name": "prod"}}},
			},
			"second document": {
				input: stringtest.LinesLF(
					"a: {",
					"  # c",
					"  b: 1",
					"}",
					"---",
					"x: [",
					"  1",
					"  # d",
					"]",
				),
				want: []any{
					map[string]any{"a": map[string]any{"b": uint64(1)}},
					map[string]any{"x": []any{uint64(1)}},
				},
			},
			"comment after an opening bracket": {
				input: stringtest.LinesLF(
					"a: [",
					"  # c",
					"  1",
					"]",
				),
				kept: "# c",
				want: []any{map[string]any{"a": []any{uint64(1)}}},
			},
			"comment between flow sequence entries": {
				input: stringtest.LinesLF(
					"a: [",
					"  1,",
					"  # c",
					"  2",
					"]",
				),
				kept: "# c",
				want: []any{map[string]any{"a": []any{uint64(1), uint64(2)}}},
			},
			"comment between a colon and its value": {
				input: stringtest.LinesLF(
					"a: {",
					"  b:",
					"  # c",
					"  1",
					"}",
				),
				kept: "# c",
				want: []any{map[string]any{"a": map[string]any{"b": uint64(1)}}},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				requireDocumentValues(t, tc.input, tc.kept, tc.want)
			})
		}
	})

	t.Run("comment on a line of its own around an explicit key", func(t *testing.T) {
		t.Parallel()

		// When it keeps comments, the go-yaml parser takes such a comment
		// after a "?" or before a ":" as a key, and rejects or misreads the
		// mapping. Each case lists the value of every document and the
		// comment the tree keeps, if any.
		tcs := map[string]struct {
			input string
			kept  string
			want  []any
		}{
			"comment between a key and its colon": {
				input: stringtest.LinesLF("? a", "# c", ": 1"),
				want:  []any{map[string]any{"a": uint64(1)}},
			},
			"indented comment between a key and its colon": {
				input: stringtest.LinesLF("? a", "  # c", ": 1"),
				want:  []any{map[string]any{"a": uint64(1)}},
			},
			"comments with a blank line between them": {
				input: stringtest.LinesLF(
					"? a",
					"# c",
					"",
					"# d",
					": 1",
				),
				want: []any{map[string]any{"a": uint64(1)}},
			},
			"comment before the colon of a later key": {
				input: stringtest.LinesLF(
					"? a",
					": 1",
					"? b",
					"# c",
					": 2",
				),
				want: []any{map[string]any{"a": uint64(1), "b": uint64(2)}},
			},
			"comment in a nested mapping": {
				input: stringtest.LinesLF(
					"x:",
					"  ? a",
					"  # c",
					"  : 1",
				),
				want: []any{map[string]any{"x": map[string]any{"a": uint64(1)}}},
			},
			"comment in a mapping in a sequence": {
				input: stringtest.LinesLF("- ? a", "  # c", "  : 1"),
				want:  []any{[]any{map[string]any{"a": uint64(1)}}},
			},
			"comment below a block scalar key": {
				input: stringtest.LinesLF(
					"? |",
					"  lit",
					"# c",
					": 1",
				),
				want: []any{map[string]any{"lit\n": uint64(1)}},
			},
			"comment above a block sequence value": {
				input: stringtest.LinesLF("? a", "# c", ": - 1"),
				want:  []any{map[string]any{"a": []any{uint64(1)}}},
			},
			"comment between an indicator and its key": {
				input: stringtest.LinesLF(
					"? ",
					"  # c",
					"  a",
					": 1",
				),
				want: []any{map[string]any{"a": uint64(1)}},
			},
			"comment on the line of the key": {
				input: stringtest.LinesLF("? a # k", "# c", ": 1"),
				kept:  "# k",
				want:  []any{map[string]any{"a": uint64(1)}},
			},
			// The parser reads a comment on the last line of a key on
			// several lines as one on a line of its own.
			"comment on the last line of a quoted key": {
				input: stringtest.LinesLF(`? "a`, ` b" # c`, ": 1"),
				want:  []any{map[string]any{"a b": uint64(1)}},
			},
			"comment between entries": {
				input: stringtest.LinesLF(
					"? a",
					": 1",
					"# c",
					"? b",
					": 2",
				),
				kept: "# c",
				want: []any{map[string]any{"a": uint64(1), "b": uint64(2)}},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				requireDocumentValues(t, tc.input, tc.kept, tc.want)
			})
		}
	})
}

// checkHours is a value for the tests of a check bound after a decode.
type checkHours struct {
	Open  string `yaml:"open"`
	Close string `yaml:"close"`
}

// accumulatingConfig is a [niceyaml.SelfValidator] whose Validate builds
// its result in a typed pointer, as an accumulator does, so a valid value
// returns a nil [*niceyaml.Error] rather than a nil error.
type accumulatingConfig struct {
	Name  string `yaml:"name"`
	Value int    `yaml:"value"`
}

// Validate implements [niceyaml.SelfValidator].
func (c accumulatingConfig) Validate() error {
	var err *niceyaml.Error

	if c.Value == 0 {
		err = niceyaml.NewError("value is required", niceyaml.AtPath(paths.Current().Child("value")))
	}

	return err
}

func TestDocument_Decode_SelfValidator(t *testing.T) {
	t.Parallel()

	t.Run("runs Validate unless the last WithSelfValidation turns it off", func(t *testing.T) {
		t.Parallel()

		off := niceyaml.WithSelfValidation(false)

		tcs := map[string]struct {
			err   error
			input string
			opts  []niceyaml.DecodeOption
			// Whether Validate ran on the value a decode returns.
			validated bool
		}{
			"no option": {
				input: stringtest.JoinLF(
					"name: test",
					"value: 42",
				),
				validated: true,
			},
			"no option returns the error of Validate": {
				input: stringtest.JoinLF(
					`name: ""`,
					"value: 42",
				),
				err: errNameRequired,
			},
			"false skips Validate": {
				input: `name: ""`,
				opts:  []niceyaml.DecodeOption{off},
			},
			"false keeps schemas": {
				input: "name: invalid",
				opts:  []niceyaml.DecodeOption{niceyaml.WithValidator(nameSchema(nil)), off},
				err:   errSchemaValidationFailed,
			},
			"a later true turns Validate back on": {
				input: `name: ""`,
				opts:  []niceyaml.DecodeOption{off, niceyaml.WithSelfValidation(true)},
				err:   errNameRequired,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocument(t, tc.input)

				got, err := doc.Decode[validatorConfig](t.Context(), tc.opts...)

				// A decode into a value the caller holds runs Validate as
				// a decode that returns the value does.
				var into validatorConfig

				intoErr := doc.DecodeInto(t.Context(), &into, tc.opts...)

				if tc.err != nil {
					require.ErrorIs(t, err, tc.err)
					require.ErrorIs(t, intoErr, tc.err)

					return
				}

				require.NoError(t, err)
				require.NoError(t, intoErr)
				assert.Equal(t, tc.validated, got.validated)
				assert.Equal(t, tc.validated, into.validated)
			})
		}
	})

	t.Run("a typed nil from Validate decodes without error", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, "name: test\nvalue: 42\n")

		result, err := doc.Decode[accumulatingConfig](t.Context())
		require.NoError(t, err)
		assert.Equal(t, accumulatingConfig{Name: "test", Value: 42}, result)
	})

	t.Run("a typed non-nil from Validate binds to the document", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, "name: test\nvalue: 0\n")

		_, err := doc.Decode[accumulatingConfig](t.Context())

		bound := yamltest.RequireSourceError(t, err)
		assert.Equal(t, "2:8: $.value: value is required", bound.Error())
	})

	t.Run("runs schema and Validate in order", func(t *testing.T) {
		t.Parallel()

		input := stringtest.Input(`
			name: test
			value: 42
		`)
		dd := yamltest.FirstDocument(t, input)

		var called bool

		result, err := dd.Decode[validatorConfig](
			t.Context(),
			niceyaml.WithValidator(nameSchema(&called)),
		)
		require.NoError(t, err)
		assert.True(t, called, "the validator should have been called")
		assert.Equal(t, validatorConfig{Name: "test", Value: 42, validated: true}, result)
	})
}

// validatorConfig implements niceyaml.SelfValidator.
type validatorConfig struct {
	Name      string `yaml:"name"`
	Value     int    `yaml:"value"`
	validated bool
}

func (c *validatorConfig) Validate() error {
	c.validated = true

	if c.Name == "" {
		return niceyaml.Invalid(
			errNameRequired,
			niceyaml.AtPath(paths.Current().Child("name").Key()),
		)
	}

	return nil
}

// plainConfig does not implement niceyaml.SelfValidator.
type plainConfig struct {
	Name  string `yaml:"name"`
	Value int    `yaml:"value"`
}

// nameSchema returns a [niceyaml.Validator] that rejects a document
// whose name is "invalid" with a path error, and records each call in called
// when it is not nil.
func nameSchema(called *bool) niceyaml.Validator {
	return niceyaml.ValidatorFunc(func(ctx context.Context, doc *niceyaml.Node) error {
		if called != nil {
			*called = true
		}

		m, err := doc.Decode[map[string]any](ctx)
		if err != nil {
			return err
		}

		if name, ok := m["name"].(string); ok && name == "invalid" {
			return niceyaml.Invalid(
				errSchemaValidationFailed,
				niceyaml.AtPath(paths.Current().Child("name").Key()),
			)
		}

		return nil
	})
}

// failingValidator always fails self-validation with a path error.
type failingValidator struct {
	Name string `yaml:"name"`
}

func (failingValidator) Validate() error {
	return niceyaml.NewError("rejected", niceyaml.AtPath(paths.Current().Child("name")))
}

// fieldValidator is a [niceyaml.Validator] that returns the error its
// field holds, so a nil pointer to one panics when it runs.
type fieldValidator struct {
	err error
}

func (v *fieldValidator) Check(context.Context, *niceyaml.Node) error {
	return v.err
}

// negativePrice rejects a price below zero. It is a validator of a type
// of its own, with a body a [niceyaml.ValidatorFunc] can hold as it is.
type negativePrice struct{}

func (negativePrice) Check(ctx context.Context, n *niceyaml.Node) error {
	pricePath := paths.Current().Child("price")

	node, err := n.At(pricePath)
	if err != nil {
		return err //nolint:wrapcheck // The test inspects the error as it is.
	}

	price, err := node.Decode[int](ctx)
	if err != nil {
		return err
	}

	if price < 0 {
		return niceyaml.NewError("negative price", niceyaml.AtPath(pricePath))
	}

	return nil
}

func TestDocument_ErrorsResolveInDocument(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		name: first
		---
		name: second
	`)

	badName := niceyaml.NewError("bad name", niceyaml.AtPath(paths.Current().Child("name")))
	validator := rejectingValidator(badName)
	// A validator that binds its own error, through the Node it gets.
	binding := niceyaml.ValidatorFunc(func(_ context.Context, doc *niceyaml.Node) error {
		return doc.Bind(badName)
	})

	tcs := map[string]struct {
		// Returns the error of one document.
		call func(ctx context.Context, doc *niceyaml.Node) error
		// The first line of the error of each document.
		want []string
	}{
		"validator errors": {
			call: func(ctx context.Context, doc *niceyaml.Node) error {
				return doc.Validate(ctx, validator)
			},
			want: []string{"1:7: $.name: bad name", "3:7: $.name: bad name"},
		},
		"validator errors of a decode": {
			call: decodeAs[map[string]string](niceyaml.WithValidator(validator)),
			want: []string{"1:7: $.name: bad name", "3:7: $.name: bad name"},
		},
		"errors a validator binds itself": {
			call: decodeAs[map[string]string](niceyaml.WithValidator(binding)),
			want: []string{"1:7: $.name: bad name", "3:7: $.name: bad name"},
		},
		"self validation errors": {
			call: decodeAs[failingValidator](),
			want: []string{"1:7: $.name: rejected", "3:7: $.name: rejected"},
		},
		"decode errors": {
			call: decodeAs[struct {
				Name int `yaml:"name"`
			}](),
			want: []string{"1:7: $.name: expected integer, got string", "3:7: $.name: expected integer, got string"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var got []string

			for _, doc := range niceyaml.NewSourceFromString(input).Documents() {
				err := tc.call(t.Context(), doc)
				require.Error(t, err)

				got = append(got, strings.SplitN(err.Error(), "\n", 2)[0])
			}

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestWithDisallowUnknownFields_ControlCharacters(t *testing.T) {
	t.Parallel()

	type config struct {
		Name string `yaml:"name"`
	}

	// The decoder writes the name of an unknown field into its message as
	// the document spells it. A key with a line feed or an escape sequence
	// thus reaches the message, where it must not start a line that reads
	// as another error or write to a terminal.
	tcs := map[string]struct {
		input string
		want  string
	}{
		"a key that forges a line": {
			input: "name: x\n\"a\\nother.yaml:9:9: $.secret: forged\": 1\n",
			want: `f.yaml:2:1: $.'a\nother.yaml:9:9: $.secret: forged'~: ` +
				"unknown field \"a\u240aother.yaml:9:9: $.secret: forged\"",
		},
		"a key with an escape sequence": {
			input: "name: x\n\"a\\e[31mb\": 1\n",
			want:  `f.yaml:2:1: $.'a\u001b[31mb'~: ` + "unknown field \"a\u241b[31mb\"",
		},
		"two keys": {
			input: stringtest.LinesLF(
				"name: x",
				`"a\nb": 1`,
				`"c\td": 2`,
			),
			want: stringtest.JoinLF(
				"f.yaml: 2 unknown fields",
				`f.yaml:2:1: $.'a\nb'~: `+"unknown field \"a\u240ab\"",
				`f.yaml:3:1: $.'c\td'~: `+"unknown field \"c\u2409d\"",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := niceyaml.NewSourceFromString(tc.input, niceyaml.WithName("f.yaml"))

			_, err := source.Decode[config](t.Context(), niceyaml.WithDisallowUnknownFields(true))
			require.EqualError(t, err, tc.want)
			assert.NotContains(t, err.Error(), "\x1b")

			// The path reads back as the key the document holds.
			require.ErrorAs(t, err, new(*niceyaml.SourceError))

			for _, b := range yamltest.BoundIn(err) {
				path, ok := b.Path()
				if !ok {
					continue
				}

				parsed, err := paths.Parse(path.String())
				require.NoError(t, err)
				assert.Equal(t, path, parsed)
			}
		})
	}
}

func TestWithDisallowUnknownFields(t *testing.T) {
	t.Parallel()

	type strictConfig struct {
		Name string `yaml:"name"`
	}

	t.Run("option applies per call", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input string
			path  paths.Path
		}{
			"root": {
				input: "name: test\nextra: field",
				path:  paths.Current(),
			},
			"scoped node": {
				input: stringtest.JoinLF("inner:", "  name: test", "  extra: field"),
				path:  paths.Current().Child("inner"),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				node := yamltest.At(t, yamltest.FirstDocument(t, tc.input), tc.path)

				// The same node decodes strictly on one call and loosely on
				// the next, so the option belongs to the call rather than
				// the Source.
				_, err := node.Decode[strictConfig](t.Context(), niceyaml.WithDisallowUnknownFields(true))
				require.ErrorAs(t, err, new(*niceyaml.Error))

				result, err := node.Decode[strictConfig](t.Context())
				require.NoError(t, err)
				assert.Equal(t, "test", result.Name)
			})
		}
	})

	t.Run("a later false turns the option off", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: test\nextra: field\n")

		result, err := dd.Decode[strictConfig](t.Context(),
			niceyaml.WithDisallowUnknownFields(true),
			niceyaml.WithDisallowUnknownFields(false),
		)
		require.NoError(t, err)
		assert.Equal(t, "test", result.Name)
	})
}

func TestWithAllowedFieldPrefixes(t *testing.T) {
	t.Parallel()

	type config struct {
		Name string `yaml:"name"`
	}

	strict := niceyaml.WithDisallowUnknownFields(true)

	tcs := map[string]struct {
		input string
		err   string
		opts  []niceyaml.DecodeOption
	}{
		"allows a key under a prefix": {
			input: "name: a\nx-note: b\n",
			opts:  []niceyaml.DecodeOption{strict, niceyaml.WithAllowedFieldPrefixes("x-")},
		},
		"rejects a key under no prefix": {
			input: stringtest.LinesLF(
				"name: a",
				"x-note: b",
				"note: c",
			),
			opts: []niceyaml.DecodeOption{strict, niceyaml.WithAllowedFieldPrefixes("x-")},
			err:  `3:1: $.note~: unknown field "note"`,
		},
		"each option adds to the prefixes before it": {
			input: stringtest.LinesLF(
				"name: a",
				"x-note: b",
				"y-note: c",
				"z-note: d",
			),
			opts: []niceyaml.DecodeOption{
				strict,
				niceyaml.WithAllowedFieldPrefixes("x-"),
				niceyaml.WithAllowedFieldPrefixes("y-"),
			},
			err: `4:1: $.z-note~: unknown field "z-note"`,
		},
		"empty prefix allows every key": {
			input: "name: a\nnote: b\n",
			opts:  []niceyaml.DecodeOption{strict, niceyaml.WithAllowedFieldPrefixes("")},
		},
		"changes nothing in a decode that accepts unknown fields": {
			input: "name: a\nnote: b\n",
			opts:  []niceyaml.DecodeOption{niceyaml.WithAllowedFieldPrefixes("x-")},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := yamltest.FirstDocument(t, tc.input).Decode[config](t.Context(), tc.opts...)
			if tc.err != "" {
				require.EqualError(t, err, tc.err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, config{Name: "a"}, got)
		})
	}
}

// plainLevel has no method of its own. A function from
// niceyaml.WithCustomUnmarshaler decodes it from the name of a level.
type plainLevel int

// The levels a document names.
const (
	levelLow plainLevel = iota + 1
	levelHigh
)

// decodeLevel decodes a [plainLevel] from its name.
func decodeLevel(_ context.Context, l *plainLevel, decode func(any) error) error {
	var name string

	err := decode(&name)
	if err != nil {
		return err
	}

	switch name {
	case "low":
		*l = levelLow
	case "high":
		*l = levelHigh
	default:
		return fmt.Errorf("%w %q", errUnknownLevel, name)
	}

	return nil
}

// decodeCIDR decodes a [net.IPNet] from a string in CIDR notation, as
// the example of niceyaml.WithCustomUnmarshaler does.
func decodeCIDR(_ context.Context, n *net.IPNet, decode func(any) error) error {
	var s string

	err := decode(&s)
	if err != nil {
		return err
	}

	_, parsed, err := net.ParseCIDR(s)
	if err != nil {
		return err //nolint:wrapcheck // The test inspects the error as it is.
	}

	*n = *parsed

	return nil
}

// plainSpan has no method of its own. A function from
// niceyaml.WithCustomUnmarshaler decodes it, as decodeSpan does.
type plainSpan struct {
	From  int        `yaml:"from"`
	To    int        `yaml:"to"`
	Level plainLevel `yaml:"level"`
}

// decodeSpan decodes a [plainSpan] by its fields and reports a to below
// its from at the path of the to, which reads from the span.
func decodeSpan(_ context.Context, s *plainSpan, decode func(any) error) error {
	type fields plainSpan

	err := decode((*fields)(s))
	if err != nil {
		return err
	}

	if s.To < s.From {
		return niceyaml.NewError("to is below from", niceyaml.AtPath(paths.Current().Child("to")))
	}

	return nil
}

// levelHolder decodes itself through a second type with the same fields
// and puts text of its own in front of the error of its level.
type levelHolder struct {
	Level plainLevel `yaml:"level"`
}

func (h *levelHolder) UnmarshalYAML(unmarshal func(any) error) error {
	type plain levelHolder

	err := unmarshal((*plain)(h))
	if err != nil {
		return fmt.Errorf("holder: %w", err)
	}

	return nil
}

// methodLevel decodes itself from text, and reports an error whenever
// the decoder calls its method.
type methodLevel int

func (*methodLevel) UnmarshalText([]byte) error {
	return errors.New("the decoder called the method")
}

func TestWithCustomUnmarshaler(t *testing.T) {
	t.Parallel()

	levels := niceyaml.WithCustomUnmarshaler(decodeLevel)

	t.Run("decodes every value of the type", func(t *testing.T) {
		t.Parallel()

		type config struct {
			Pointer *plainLevel           `yaml:"pointer"`
			ByName  map[string]plainLevel `yaml:"by_name"`
			ByLevel map[plainLevel]string `yaml:"by_level"`
			List    []plainLevel          `yaml:"list"`
			Level   plainLevel            `yaml:"level"`
		}

		doc := yamltest.FirstDocument(t, stringtest.Input(`
			level: high
			pointer: low
			list: [low, high]
			by_name: {a: high}
			by_level: {low: a}
		`))

		got, err := doc.Decode[config](t.Context(), levels)
		require.NoError(t, err)

		low := levelLow

		assert.Equal(t, config{
			Level:   levelHigh,
			Pointer: &low,
			List:    []plainLevel{levelLow, levelHigh},
			ByName:  map[string]plainLevel{"a": levelHigh},
			ByLevel: map[plainLevel]string{levelLow: "a"},
		}, got)
	})

	t.Run("hands the function the context and a decode of the value", func(t *testing.T) {
		t.Parallel()

		type ctxKey struct{}

		var got []string

		record := niceyaml.WithCustomUnmarshaler(
			func(ctx context.Context, _ *plainLevel, decode func(any) error) error {
				var name string

				err := decode(&name)
				if err != nil {
					return err
				}

				got = append(got, fmt.Sprintf("%v|%s", ctx.Value(ctxKey{}), name))

				return nil
			},
		)

		// The decode reads an alias as the content of its anchor, and a
		// scalar without its quotes and the comment on its line.
		doc := yamltest.FirstDocument(t, stringtest.LinesLF(
			"a: &a low",
			"levels:",
			"  - *a",
			`  - "high" # top`,
		))

		_, err := doc.Decode[struct {
			Levels []plainLevel `yaml:"levels"`
		}](context.WithValue(t.Context(), ctxKey{}, "decode"), record)
		require.NoError(t, err)
		assert.Equal(t, []string{"decode|low", "decode|high"}, got)
	})

	t.Run("the decode reads a scalar however the document writes it", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input string
		}{
			"plain":          {input: "office: 10.0.0.0/8\n"},
			"double quoted":  {input: "office: \"10.0.0.0/8\"\n"},
			"single quoted":  {input: "office: '10.0.0.0/8'\n"},
			"with a comment": {input: "office: 10.0.0.0/8 # the office\n"},
			"block":          {input: stringtest.LinesLF("office: |-", "  10.0.0.0/8")},
			"folded":         {input: stringtest.LinesLF("office: >-", "  10.0.0.0/8")},
			"tagged":         {input: "office: !!str 10.0.0.0/8\n"},
			"anchored":       {input: "office: &net 10.0.0.0/8\n"},
			"alias":          {input: "net: &net 10.0.0.0/8\noffice: *net\n"},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				got, err := yamltest.FirstDocument(t, tc.input).Decode[map[string]net.IPNet](
					t.Context(), niceyaml.WithCustomUnmarshaler(decodeCIDR),
				)
				require.NoError(t, err)
				assert.Equal(t, "10.0.0.0/8", new(got["office"]).String())
			})
		}
	})

	t.Run("the decode reads the text of the value into a raw message", func(t *testing.T) {
		t.Parallel()

		var got []string

		record := niceyaml.WithCustomUnmarshaler(
			func(_ context.Context, _ *plainLevel, decode func(any) error) error {
				var text yaml.RawMessage

				err := decode(&text)
				if err != nil {
					return err
				}

				got = append(got, string(text))

				return nil
			},
		)

		// The text spells an alias as the content of its anchor, and keeps
		// the quotes and the comment of a scalar.
		doc := yamltest.FirstDocument(t, stringtest.LinesLF(
			"a: &a low",
			"levels:",
			"  - *a",
			`  - "high" # top`,
			"  - {a: 1}",
		))

		_, err := doc.Decode[struct {
			Levels []plainLevel `yaml:"levels"`
		}](t.Context(), record)
		require.NoError(t, err)
		assert.Equal(t, []string{"low\n", "\"high\" # top\n", "{a: 1}\n"}, got)
	})

	// The decode callback returns the rejection of a decode, and the
	// decode that called the function binds it in the document.
	t.Run("the error of the function binds in the document", func(t *testing.T) {
		t.Parallel()

		type config struct {
			Base    any                   `yaml:"base"`
			ByName  map[string]net.IPNet  `yaml:"by_name"`
			ByLevel map[plainLevel]string `yaml:"by_level"`
			Nets    []net.IPNet           `yaml:"nets"`
			Spans   []plainSpan           `yaml:"spans"`
			Count   int                   `yaml:"count"`
		}

		decoders := niceyaml.DecodeOptions(
			niceyaml.WithCustomUnmarshaler(decodeCIDR),
			niceyaml.WithCustomUnmarshaler(decodeSpan),
			levels,
		)

		tcs := map[string]struct {
			input string
			want  string
			opts  []niceyaml.DecodeOption
		}{
			"sequence where the function decodes a string": {
				input: stringtest.LinesLF(
					"nets:",
					"  - 10.0.0.0/8",
					"  - [1, 2]",
				),
				want: "3:3: $.nets[1]: expected string, got sequence",
			},
			"mapping where the function decodes a string": {
				input: stringtest.LinesLF(
					"nets:",
					"  - 10.0.0.0/8",
					"  - {a: 1}",
				),
				want: "3:3: $.nets[1]: expected string, got mapping",
			},
			"string the function rejects": {
				input: stringtest.LinesLF(
					"nets:",
					"  - 10.0.0.0/8",
					"  - 10.0.0.0/33",
				),
				want: "3:5: $.nets[1]: invalid CIDR address: 10.0.0.0/33",
			},
			"map value": {
				input: stringtest.LinesLF(
					"by_name:",
					"  a: 10.0.0.0/8",
					"  b: [1]",
				),
				want: "3:3: $.by_name.b: expected string, got sequence",
			},
			"map key": {
				input: stringtest.LinesLF(
					"by_level:",
					"  low: a",
					"  mid: b",
				),
				want: `3:3: $.by_level.mid~: unknown level "mid"`,
			},
			"field of the value": {
				input: stringtest.LinesLF(
					"spans:",
					"  - {from: 1, to: 5}",
					"  - from: nine",
					"    to: 3",
				),
				want: "3:11: $.spans[1].from: expected integer, got string",
			},
			// The decode of a value reports every problem of that value.
			"two fields of the value": {
				input: stringtest.LinesLF(
					"spans:",
					"  - from: nine",
					"    to: [3]",
				),
				want: stringtest.JoinLF(
					"2 problems",
					"2:11: $.spans[0].from: expected integer, got string",
					"3:5: $.spans[0].to: expected integer, got sequence",
				),
			},
			// The path the function writes reads from its value.
			"path the function writes": {
				input: stringtest.LinesLF(
					"spans:",
					"  - {from: 1, to: 5}",
					"  - from: 9",
					"    to: 3",
				),
				want: "4:9: $.spans[1].to: to is below from",
			},
			// A path goes through an alias to the line that holds the
			// value.
			"path in a value an alias holds": {
				input: stringtest.LinesLF(
					"base: &base {from: 9, to: 3}",
					"spans:",
					"  - *base",
				),
				want: "1:27: $.spans[0].to: to is below from",
			},
			"field of a value an alias holds": {
				input: stringtest.LinesLF(
					"base: &base {from: nine}",
					"spans:",
					"  - *base",
				),
				want: "1:20: $.spans[0].from: expected integer, got string",
			},
			// The text of the value writes out what a merge key brings in,
			// so the path names the merge key, and resolves through it.
			"field a merge key brings in": {
				input: stringtest.LinesLF(
					"base: &base {from: nine}",
					"spans:",
					"  - <<: *base",
					"    to: 3",
				),
				want: "1:20: $.spans[0].<<.from: expected integer, got string",
			},
			"field that holds an alias": {
				input: stringtest.LinesLF(
					"base: &base [1]",
					"spans:",
					"  - from: *base",
					"    to: 3",
				),
				want: "3:11: $.spans[0].from: expected integer, got sequence",
			},
			// The decode of a value takes the options of the decode that
			// called the function, so another function decodes the level.
			"value another function decodes below the value": {
				input: stringtest.LinesLF("spans:", "  - {from: 1, to: 2, level: mid}"),
				want:  `2:29: $.spans[0].level: unknown level "mid"`,
			},
			"unknown field of the value": {
				input: stringtest.LinesLF(
					"spans:",
					"  - from: 1",
					"    until: 2",
				),
				opts: []niceyaml.DecodeOption{niceyaml.WithDisallowUnknownFields(true)},
				want: `3:5: $.spans[0].until~: unknown field "until"`,
			},
			// The decoder stops at the first value a function rejects, and
			// the search for the other problems of the decode calls no
			// function again. One decode thus reports the first network
			// alone.
			"second value a function rejects": {
				input: stringtest.LinesLF(
					"nets:",
					"  - 10.0.0.0/33",
					"  - [1, 2]",
				),
				want: "2:5: $.nets[0]: invalid CIDR address: 10.0.0.0/33",
			},
			// The search still finds the count, which no function decodes.
			"second value a function rejects beside another problem": {
				input: stringtest.LinesLF(
					"count: x",
					"nets:",
					"  - [1, 2]",
					"  - {a: 1}",
				),
				want: stringtest.JoinLF(
					"2 problems",
					"1:8: $.count: expected integer, got string",
					"3:3: $.nets[0]: expected string, got sequence",
				),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				_, err := yamltest.FirstDocument(t, tc.input).Decode[config](
					t.Context(), append([]niceyaml.DecodeOption{decoders}, tc.opts...)...,
				)
				require.EqualError(t, err, tc.want)
				require.ErrorIs(t, err, niceyaml.ErrDecode)
				requireInvalid(t, err, max(strings.Count(tc.want, "\n"), 1))
			})
		}
	})

	// An unmarshaler below the value writes its paths from its own value,
	// and the decode of the value puts them under the path that value has
	// below the value of the function.
	t.Run("the error of an unmarshaler below the value binds in the document", func(t *testing.T) {
		t.Parallel()

		type box struct {
			Item   reporting            `yaml:"item"`
			Placed positioned           `yaml:"placed"`
			Broken panickingUnmarshaler `yaml:"broken"`
		}

		boxes := niceyaml.WithCustomUnmarshaler(func(_ context.Context, b *box, decode func(any) error) error {
			type fields box

			return decode((*fields)(b))
		})

		// The item of the one box holds a to below its from.
		reversed := stringtest.LinesLF("boxes:", "  - item: {from: 9, to: 3}")

		tcs := map[string]struct {
			report error
			input  string
			want   string
			detail string
		}{
			"path": {
				input:  reversed,
				report: spanError(),
				want:   "2:25: $.boxes[0].item.to: to is below from",
			},
			"path of a detail": {
				input: reversed,
				report: niceyaml.NewError("span is odd", niceyaml.WithDetails(
					niceyaml.NewError("to set here", niceyaml.AtPath(paths.Current().Child("to"))),
				)),
				want:   "2:5: $.boxes[0].item: span is odd",
				detail: "2:25: $.boxes[0].item.to: to set here",
			},
			"join": {
				input:  reversed,
				report: errors.Join(errors.New("from is odd"), errors.New("to is odd")),
				want: stringtest.JoinLF(
					"2:5: $.boxes[0].item: from is odd",
					"2:5: $.boxes[0].item: to is odd",
				),
			},
			// The position counts the lines of the text of the value, so
			// the error binds at the value in its place.
			"position an unmarshaler built from its node": {
				input: stringtest.LinesLF(
					"boxes:",
					"  - name: a",
					"  - placed: [a, b]",
				),
				want: "3:3: $.boxes[1]: unmarshaler rejected the value",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				_, err := yamltest.FirstDocument(t, tc.input).Decode[struct {
					Boxes []box `yaml:"boxes"`
				}](context.WithValue(t.Context(), reportKey{}, tc.report), boxes)
				require.EqualError(t, err, tc.want)
				require.ErrorIs(t, err, niceyaml.ErrDecode)

				if tc.detail == "" {
					return
				}

				srcErr := yamltest.RequireSourceError(t, err)
				require.Len(t, yamltest.DetailsOf(srcErr), 1)
				assert.EqualError(t, yamltest.DetailsOf(srcErr)[0].Bound, tc.detail)
			})
		}

		// The decode of the value recovers the panic at the first token
		// of the text, and the decode of the document binds it at the
		// value in its place.
		t.Run("panic", func(t *testing.T) {
			t.Parallel()

			_, err := yamltest.FirstDocument(t, stringtest.LinesLF(
				"boxes:",
				"  - name: a",
				"  - broken: 1",
			)).Decode[struct {
				Boxes []box `yaml:"boxes"`
			}](t.Context(), boxes)
			require.EqualError(t, err, "3:3: $.boxes[1]: decoder panicked: unmarshaler rejected the value")
			require.NotErrorIs(t, err, niceyaml.ErrDecode)

			value, _ := requirePanic(t, err)
			assert.Equal(t, errUnmarshal, value)
		})
	})

	// The error reaches the unmarshaler above the value inside a wrapper
	// that matches what the error matches, and the error that unmarshaler
	// returns binds at its own value.
	t.Run("an unmarshaler above the value wraps the error of the function", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			is    error
			input string
			want  string
		}{
			"error of the function": {
				input: stringtest.LinesLF("main:", "  level: mid"),
				is:    errUnknownLevel,
				want:  `1:1: $.main: holder: unknown level "mid"`,
			},
			"rejection of the decode": {
				input: stringtest.LinesLF("main:", "  level: [a]"),
				is:    niceyaml.ErrDecode,
				want:  "1:1: $.main: holder: expected string, got sequence",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				_, err := yamltest.FirstDocument(t, tc.input).Decode[map[string]levelHolder](t.Context(), levels)
				require.EqualError(t, err, tc.want)
				require.ErrorIs(t, err, tc.is)
			})
		}
	})

	// No value below the node reports the error, so the rejection of a
	// scalar points at the node, and that of a sequence gains no location,
	// as the error of any unmarshaler does there.
	t.Run("the error of the function for the node the decode reads", func(t *testing.T) {
		t.Parallel()

		cidr := niceyaml.WithCustomUnmarshaler(decodeCIDR)

		_, err := yamltest.FirstDocument(t, "10.0.0.0/33\n").Decode[net.IPNet](t.Context(), cidr)
		require.EqualError(t, err, "1:1: $: invalid CIDR address: 10.0.0.0/33")

		_, err = yamltest.FirstDocument(t, "[1, 2]\n").Decode[net.IPNet](t.Context(), cidr)
		require.EqualError(t, err, "expected string, got sequence")

		_, err = yamltest.FirstDocument(t, "from: nine\nto: 3\n").Decode[plainSpan](
			t.Context(), niceyaml.WithCustomUnmarshaler(decodeSpan),
		)
		require.EqualError(t, err, "1:7: $.from: expected integer, got string")
	})

	t.Run("the decode of a value below a scoped node binds in the document", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.LinesLF(
			"name: api",
			"nets:",
			"  - 10.0.0.0/8",
			"  - {a: 1}",
		))

		_, err := doc.DecodeAt[[]net.IPNet](
			t.Context(), paths.Current().Child("nets"), niceyaml.WithCustomUnmarshaler(decodeCIDR),
		)
		require.EqualError(t, err, "4:3: $.nets[1]: expected string, got mapping")
	})

	t.Run("the decode of a value allows the duplicate keys its source allows", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, "span: {from: 1, from: 2, to: 3}\n", niceyaml.WithAllowDuplicateKeys(true))

		got, err := doc.Decode[map[string]plainSpan](t.Context(), niceyaml.WithCustomUnmarshaler(decodeSpan))
		require.NoError(t, err)
		assert.Equal(t, map[string]plainSpan{"span": {From: 2, To: 3}}, got)
	})

	t.Run("the decode takes a pointer alone", func(t *testing.T) {
		t.Parallel()

		byValue := niceyaml.WithCustomUnmarshaler(func(_ context.Context, _ *plainLevel, decode func(any) error) error {
			var name string

			return decode(name)
		})

		_, err := yamltest.FirstDocument(t, "level: high\n").Decode[map[string]plainLevel](t.Context(), byValue)
		require.EqualError(t, err, "1:8: $.level: decode target is not a non-nil pointer: got string")
		require.ErrorIs(t, err, niceyaml.ErrDecodeTarget)
	})

	// A decode into the type of the function would call the function for
	// the same value again, and so on without end.
	t.Run("the decode refuses the type its function decodes", func(t *testing.T) {
		t.Parallel()

		recursive := niceyaml.WithCustomUnmarshaler(
			func(_ context.Context, l *plainLevel, decode func(any) error) error {
				return decode(l)
			},
		)

		_, err := yamltest.FirstDocument(t, "level: 2\n").Decode[map[string]plainLevel](t.Context(), recursive)
		require.EqualError(
			t, err, "1:8: $.level: decode target is the type the function decodes: got *niceyaml_test.plainLevel",
		)
		require.ErrorIs(t, err, niceyaml.ErrDecode)
	})

	// The go-yaml decoder returns the type error it finds in the error of
	// a struct field in place of that error.
	t.Run("an error that wraps a go-yaml type error keeps its text", func(t *testing.T) {
		t.Parallel()

		counted := niceyaml.WithCustomUnmarshaler(func(_ context.Context, _ *plainLevel, decode func(any) error) error {
			var text yaml.RawMessage

			err := decode(&text)
			if err != nil {
				return err
			}

			var count int

			err = yaml.Unmarshal(text, &count)
			if err != nil {
				return fmt.Errorf("%w: %w", errUnknownLevel, err)
			}

			return nil
		})

		_, err := yamltest.FirstDocument(t, "name: api\nlevel: high\n").Decode[struct {
			Name  string     `yaml:"name"`
			Level plainLevel `yaml:"level"`
		}](t.Context(), counted)
		require.ErrorIs(t, err, errUnknownLevel)

		var typeErr *yaml.TypeError

		require.ErrorAs(t, err, &typeErr)
		require.EqualError(t, err, "2:8: $.level: unknown level: "+typeErr.Error())
	})

	t.Run("a null leaves a pointer nil without a call", func(t *testing.T) {
		t.Parallel()

		calls := 0
		count := niceyaml.WithCustomUnmarshaler(func(context.Context, *plainLevel, func(any) error) error {
			calls++

			return nil
		})

		got, err := yamltest.FirstDocument(t, "level: null\n").Decode[struct {
			Level *plainLevel `yaml:"level"`
		}](t.Context(), count)
		require.NoError(t, err)
		assert.Nil(t, got.Level)
		assert.Zero(t, calls)
	})

	t.Run("the function decodes ahead of a method of the type", func(t *testing.T) {
		t.Parallel()

		byFunction := niceyaml.WithCustomUnmarshaler(func(_ context.Context, l *methodLevel, _ func(any) error) error {
			*l = 7

			return nil
		})

		doc := yamltest.FirstDocument(t, "level: high\n")

		got, err := doc.Decode[map[string]methodLevel](t.Context(), byFunction)
		require.NoError(t, err)
		assert.Equal(t, map[string]methodLevel{"level": 7}, got)

		_, err = doc.Decode[map[string]methodLevel](t.Context())
		require.EqualError(t, err, "1:8: $.level: the decoder called the method")
	})

	t.Run("the error of the function binds at the value", func(t *testing.T) {
		t.Parallel()

		_, err := yamltest.FirstDocument(t, "name: api\nlevel: mid\n").Decode[struct {
			Name  string     `yaml:"name"`
			Level plainLevel `yaml:"level"`
		}](t.Context(), levels)
		require.EqualError(t, err, `2:8: $.level: unknown level "mid"`)
		require.ErrorIs(t, err, errUnknownLevel)
		require.ErrorIs(t, err, niceyaml.ErrDecode)
	})

	t.Run("a nil function removes the function of the type", func(t *testing.T) {
		t.Parallel()

		removed := niceyaml.WithCustomUnmarshaler[plainLevel](nil)

		tcs := map[string]struct {
			opts []niceyaml.DecodeOption
		}{
			"alone": {
				opts: []niceyaml.DecodeOption{removed},
			},
			"after a function": {
				opts: []niceyaml.DecodeOption{levels, removed},
			},
			"in a bundle after a function": {
				opts: []niceyaml.DecodeOption{niceyaml.DecodeOptions(levels, removed)},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				// The function reads a name, so the decode reads the number
				// only once the function is gone.
				got, err := yamltest.FirstDocument(t, "level: 2\n").Decode[map[string]plainLevel](
					t.Context(),
					tc.opts...,
				)
				require.NoError(t, err)
				assert.Equal(t, map[string]plainLevel{"level": levelHigh}, got)
			})
		}
	})

	t.Run("a pointer type names no type", func(t *testing.T) {
		t.Parallel()

		// The decoder decodes the value a pointer points to, so no value
		// of the document reaches a function for the pointer.
		pointers := niceyaml.WithCustomUnmarshaler(func(context.Context, **plainLevel, func(any) error) error {
			return errors.New("the decoder called the function")
		})

		got, err := yamltest.FirstDocument(t, "level: 2\n").Decode[map[string]*plainLevel](t.Context(), pointers)
		require.NoError(t, err)

		high := levelHigh

		assert.Equal(t, map[string]*plainLevel{"level": &high}, got)
	})

	t.Run("the option reaches the decode that gets it alone", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, "level: high\n")

		_, err := doc.Decode[map[string]plainLevel](t.Context(), levels)
		require.NoError(t, err)

		_, err = doc.Decode[map[string]plainLevel](t.Context())
		require.EqualError(t, err, "1:8: $.level: expected integer, got string")
	})

	t.Run("a validator decodes with the options it gives its own decode", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, "level: high\n")

		// The validator decodes the node it gets, and that decode takes
		// the options the validator passes and no option of the decode
		// that runs the validator.
		decodes := func(opts ...niceyaml.DecodeOption) niceyaml.DecodeOption {
			return niceyaml.WithValidator(niceyaml.ValidatorFunc(decodeAs[map[string]plainLevel](opts...)))
		}

		_, err := doc.Decode[map[string]plainLevel](t.Context(), levels, decodes(levels))
		require.NoError(t, err)

		_, err = doc.Decode[map[string]plainLevel](t.Context(), levels, decodes())
		require.EqualError(t, err, "1:8: $.level: expected integer, got string")
	})
}

// jsonSize has an UnmarshalJSON method that reads a size such as "4k",
// which its field does not mirror.
type jsonSize struct {
	Bytes int `yaml:"bytes"`
}

func (s *jsonSize) UnmarshalJSON(data []byte) error {
	var text string

	err := json.Unmarshal(data, &text)
	if err != nil {
		return fmt.Errorf("size: %w", err)
	}

	kilobytes, err := strconv.Atoi(strings.TrimSuffix(text, "k"))
	if err != nil {
		return fmt.Errorf("size: %w", err)
	}

	s.Bytes = kilobytes * 1024

	return nil
}

// yamlSize has an UnmarshalYAML method beside an UnmarshalJSON method
// that reports an error whenever the decoder calls it.
type yamlSize struct {
	Bytes int
}

func (s *yamlSize) UnmarshalYAML([]byte) error {
	s.Bytes = 1

	return nil
}

func (*yamlSize) UnmarshalJSON([]byte) error {
	return errors.New("the decoder called UnmarshalJSON")
}

// checkedMirror has no Validate method, so only a function from
// niceyaml.WithValueCheck checks it.
type checkedMirror struct {
	URL string `yaml:"url"`
}

// checkedOwner is a struct that [checkedService] inlines.
type checkedOwner struct {
	Owner string `yaml:"owner"`
}

// checkedService holds values whose types have no Validate method.
type checkedService struct {
	Backup *checkedMirror `yaml:"backup"`
	Owner  checkedOwner   `yaml:",inline"`
	Mirror checkedMirror  `yaml:"mirror"`
	Addr   netip.Addr     `yaml:"addr"`
}

// checkedServices holds services under keys that a document can spell
// as 0x10, and in a list.
type checkedServices struct {
	Services map[int]checkedService `yaml:"services"`
	List     []checkedService       `yaml:"list"`
}

// hostName is a string type, which a check of string does not match.
type hostName string

// rejectLoopback rejects a loopback address.
func rejectLoopback(_ context.Context, a *netip.Addr) error {
	if a.IsLoopback() {
		return niceyaml.NewError("loopback address " + a.String())
	}

	return nil
}

// requireHTTP rejects a mirror whose URL is not http, at the path of the
// URL.
func requireHTTP(_ context.Context, m *checkedMirror) error {
	if !strings.HasPrefix(m.URL, "http://") {
		return niceyaml.NewError(
			fmt.Sprintf("url %q is not http", m.URL),
			niceyaml.AtPath(paths.Current().Child("url")),
		)
	}

	return nil
}

// recordCheck returns a check of T that appends name to order and returns
// err.
func recordCheck[T any](order *[]string, name string, err error) niceyaml.DecodeOption {
	return niceyaml.WithValueCheck(func(context.Context, *T) error {
		*order = append(*order, name)

		return err
	})
}

func ExampleWithValueCheck() {
	ctx := context.Background()

	type network struct {
		Allow []netip.Prefix `yaml:"allow"`
	}

	type config struct {
		Networks map[string]network `yaml:"networks"`
		Admin    netip.Prefix       `yaml:"admin"`
	}

	// The type netip.Prefix comes from another package, so the program
	// cannot give it a Validate method.
	masked := niceyaml.WithValueCheck(func(_ context.Context, p *netip.Prefix) error {
		if *p != p.Masked() {
			return niceyaml.NewError(fmt.Sprintf("%s sets host bits, want %s", p, p.Masked()))
		}

		return nil
	})

	source := niceyaml.NewSourceFromString(stringtest.LinesLF(
		"networks:",
		"  office:",
		"    allow: [10.0.0.0/8, 192.168.1.7/24]",
		"admin: 172.16.0.0/12",
	), niceyaml.WithName("app.yaml"))

	_, err := source.Decode[config](ctx, masked)
	fmt.Println(err)

	// Output:
	// app.yaml:3:25: $.networks.office.allow[1]: 192.168.1.7/24 sets host bits, want 192.168.1.0/24
}

func TestWithValueCheck(t *testing.T) {
	t.Parallel()

	noLoopback := niceyaml.WithValueCheck(rejectLoopback)

	t.Run("checks every value of the type", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input string
			err   string
		}{
			"a value below a key the document spells as 0x10": {
				input: stringtest.Input(`
					services:
					  0x10:
					    addr: 127.0.0.1
					  2:
					    addr: 10.0.0.1
				`),
				err: "3:11: $.services.0x10.addr: loopback address 127.0.0.1",
			},
			"an element of a list": {
				input: stringtest.Input(`
					list:
					  - addr: 10.0.0.1
					  - addr: ::1
				`),
				err: "3:11: $.list[1].addr: loopback address ::1",
			},
			"several values": {
				input: stringtest.Input(`
					services:
					  0x10:
					    addr: 127.0.0.1
					list:
					  - addr: ::1
				`),
				err: stringtest.JoinLF(
					"3:11: $.services.0x10.addr: loopback address 127.0.0.1",
					"5:11: $.list[0].addr: loopback address ::1",
				),
			},
			"values that pass": {
				input: stringtest.Input(`
					services:
					  0x10:
					    addr: 10.0.0.1
				`),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				_, err := yamltest.FirstDocument(t, tc.input).Decode[checkedServices](t.Context(), noLoopback)
				if tc.err == "" {
					require.NoError(t, err)

					return
				}

				require.EqualError(t, err, tc.err)
				assert.True(t, niceyaml.IsInvalid(err))
			})
		}
	})

	t.Run("a map whose values hold the type further down", func(t *testing.T) {
		t.Parallel()

		type config struct {
			Nets map[string][]netip.Addr `yaml:"nets"`
		}

		_, err := yamltest.FirstDocument(t, stringtest.Input(`
			nets:
			  a: [10.0.0.1]
			  b: [10.0.0.2, 127.0.0.1]
		`)).Decode[config](t.Context(), noLoopback)
		require.EqualError(t, err, "3:17: $.nets.b[1]: loopback address 127.0.0.1")
	})

	t.Run("an inline struct checks at the path of its parent", func(t *testing.T) {
		t.Parallel()

		owned := niceyaml.WithValueCheck(func(_ context.Context, o *checkedOwner) error {
			if o.Owner == "" {
				return niceyaml.NewError("owner is required", niceyaml.AtPath(paths.Current().Child("owner")))
			}

			return nil
		})

		// The second service leaves the owner out, so its error binds at
		// the key of the mapping that lacks it.
		_, err := yamltest.FirstDocument(t, stringtest.Input(`
			services:
			  0x10:
			    owner: ""
			  2:
			    addr: 10.0.0.1
		`)).Decode[checkedServices](t.Context(), owned)
		require.EqualError(t, err, stringtest.JoinLF(
			"3:12: $.services.0x10.owner: owner is required",
			"4:3: $.services.2.owner: owner is required",
		))
	})

	t.Run("reaches a pointer field through its value and the zero value of an absent field", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input string
			want  []string
		}{
			"a pointer field": {
				input: "backup: {url: http://b}\nmirror: {url: http://a}\n",
				want:  []string{"http://b", "http://a"},
			},
			"a nil pointer field": {
				input: "mirror: {url: http://a}\n",
				want:  []string{"http://a"},
			},
			"an absent struct field": {
				input: "backup: {url: http://b}\n",
				want:  []string{"http://b", ""},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				var got []string

				record := niceyaml.WithValueCheck(func(_ context.Context, m *checkedMirror) error {
					got = append(got, m.URL)

					return nil
				})

				_, err := yamltest.FirstDocument(t, tc.input).Decode[checkedService](t.Context(), record)
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
			})
		}
	})

	t.Run("DecodeAt and DecodeIfPresent check the value they decode", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, stringtest.Input(`
			services:
			  0x10:
			    addr: 127.0.0.1
		`))

		at := paths.Doc().Child("services", "0x10")
		want := "3:11: $.services.0x10.addr: loopback address 127.0.0.1"

		_, err := doc.DecodeAt[checkedService](t.Context(), at, noLoopback)
		require.EqualError(t, err, want)

		var svc checkedService

		found, err := doc.DecodeIfPresent(t.Context(), at, &svc, noLoopback)
		require.EqualError(t, err, want)
		assert.False(t, found)

		found, err = doc.DecodeIfPresent(t.Context(), paths.Doc().Child("services", "3"), &svc, noLoopback)
		require.NoError(t, err)
		assert.False(t, found)
	})

	t.Run("the checks of a value run in order and stop at the first that fails", func(t *testing.T) {
		t.Parallel()

		var order []string

		stop := niceyaml.NewError("second check failed")

		_, err := yamltest.FirstDocument(t, "mirror: {url: http://a}\n").Decode[checkedService](
			t.Context(),
			recordCheck[checkedMirror](&order, "first", nil),
			recordCheck[checkedMirror](&order, "second", stop),
			recordCheck[checkedMirror](&order, "third", nil),
		)
		require.EqualError(t, err, "1:1: $.mirror: second check failed")
		require.ErrorIs(t, err, stop)
		assert.Equal(t, []string{"first", "second"}, order)
	})

	t.Run("a value runs its checks once the values below it pass", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			mirror error
			err    string
			want   []string
		}{
			"the value below passes": {
				want: []string{"mirror", "service"},
			},
			"the value below fails": {
				mirror: niceyaml.NewError("mirror failed"),
				err:    "1:1: $.mirror: mirror failed",
				want:   []string{"mirror"},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				var order []string

				_, err := yamltest.FirstDocument(t, "mirror: {url: http://a}\n").Decode[checkedService](
					t.Context(),
					recordCheck[checkedService](&order, "service", nil),
					recordCheck[checkedMirror](&order, "mirror", tc.mirror),
				)
				if tc.err == "" {
					require.NoError(t, err)
				} else {
					require.EqualError(t, err, tc.err)
				}

				assert.Equal(t, tc.want, order)
			})
		}
	})

	t.Run("a value runs its checks once its Validate passes", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input string
			err   string
			want  []bool
		}{
			"Validate passes": {
				input: "name: test\n",
				want:  []bool{true},
			},
			"Validate fails": {
				input: "value: 1\n",
				err:   "$.name~: name is required",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				// Validate marks the value, so the check sees whether
				// Validate ran before it.
				var got []bool

				record := niceyaml.WithValueCheck(func(_ context.Context, c *validatorConfig) error {
					got = append(got, c.validated)

					return nil
				})

				_, err := yamltest.FirstDocument(t, tc.input).Decode[validatorConfig](t.Context(), record)
				if tc.err == "" {
					require.NoError(t, err)
				} else {
					require.EqualError(t, err, tc.err)
				}

				assert.Equal(t, tc.want, got)
			})
		}
	})

	t.Run("the error declares the fault as the error of a validator does", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			err     error
			invalid bool
		}{
			"NewError": {
				err:     niceyaml.NewError(errPlainValidation.Error()),
				invalid: true,
			},
			"Invalid": {
				err:     niceyaml.Invalid(errPlainValidation),
				invalid: true,
			},
			"Place": {
				err: niceyaml.Place(errPlainValidation),
			},
			"a plain error": {
				err: errPlainValidation,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				check := niceyaml.WithValueCheck(func(context.Context, *netip.Addr) error {
					return tc.err
				})

				_, err := yamltest.FirstDocument(t, "addr: 10.0.0.1\n").Decode[checkedService](t.Context(), check)
				require.EqualError(t, err, "1:7: $.addr: plain validation failure")
				assert.Equal(t, tc.invalid, niceyaml.IsInvalid(err))
			})
		}
	})

	t.Run("a check gets the context of the call", func(t *testing.T) {
		t.Parallel()

		type ctxKey struct{}

		var got []any

		record := niceyaml.WithValueCheck(func(ctx context.Context, _ *netip.Addr) error {
			got = append(got, ctx.Value(ctxKey{}))

			return nil
		})

		ctx := context.WithValue(t.Context(), ctxKey{}, "decode")

		_, err := yamltest.FirstDocument(t, "addr: 10.0.0.1\n").Decode[checkedService](ctx, record)
		require.NoError(t, err)
		assert.Equal(t, []any{"decode"}, got)
	})

	t.Run("the error of a context that ended stops the walk", func(t *testing.T) {
		t.Parallel()

		var calls int

		canceled := niceyaml.WithValueCheck(func(context.Context, *netip.Addr) error {
			calls++

			return context.Canceled
		})

		_, err := yamltest.FirstDocument(t, stringtest.Input(`
			list:
			  - addr: 10.0.0.1
			  - addr: 10.0.0.2
		`)).Decode[checkedServices](t.Context(), canceled)
		require.ErrorIs(t, err, context.Canceled)
		require.EqualError(t, err, "context canceled")
		assert.False(t, niceyaml.IsInvalid(err))
		assert.Equal(t, 1, calls)
	})

	t.Run("WithSelfValidation off runs no check", func(t *testing.T) {
		t.Parallel()

		var calls int

		counted := niceyaml.WithValueCheck(func(ctx context.Context, a *netip.Addr) error {
			calls++

			return rejectLoopback(ctx, a)
		})

		doc := yamltest.FirstDocument(t, "addr: 127.0.0.1\n")

		got, err := doc.Decode[checkedService](t.Context(), counted, niceyaml.WithSelfValidation(false))
		require.NoError(t, err)
		assert.Equal(t, netip.MustParseAddr("127.0.0.1"), got.Addr)
		assert.Zero(t, calls)

		_, err = doc.Decode[checkedService](
			t.Context(), niceyaml.WithSelfValidation(false), counted, niceyaml.WithSelfValidation(true),
		)
		require.EqualError(t, err, "1:7: $.addr: loopback address 127.0.0.1")
		assert.Equal(t, 1, calls)
	})

	t.Run("a DecodeOptions value given twice runs its checks twice", func(t *testing.T) {
		t.Parallel()

		var order []string

		opts := niceyaml.DecodeOptions(recordCheck[netip.Addr](&order, "addr", nil))

		_, err := yamltest.FirstDocument(t, "addr: 10.0.0.1\n").Decode[checkedService](
			t.Context(), opts, opts, //nolint:gocritic // The call gets the value twice.
		)
		require.NoError(t, err)
		assert.Equal(t, []string{"addr", "addr"}, order)
	})

	t.Run("each decode checks the types of its own checks", func(t *testing.T) {
		t.Parallel()

		// No value below checkedServices has a Validate method, so a decode
		// finds the values only through the types of its checks.
		doc := yamltest.FirstDocument(t, stringtest.Input(`
			services:
			  0x10:
			    addr: 127.0.0.1
			    mirror: {url: ftp://m}
		`))

		loopback := "3:11: $.services.0x10.addr: loopback address 127.0.0.1"
		notHTTP := `4:19: $.services.0x10.mirror.url: url "ftp://m" is not http`
		mirrors := niceyaml.WithValueCheck(requireHTTP)

		_, err := doc.Decode[checkedServices](t.Context(), noLoopback)
		require.EqualError(t, err, loopback)

		_, err = doc.Decode[checkedServices](t.Context(), mirrors)
		require.EqualError(t, err, notHTTP)

		_, err = doc.Decode[checkedServices](t.Context())
		require.NoError(t, err)

		_, err = doc.Decode[checkedServices](t.Context(), mirrors, noLoopback)
		require.EqualError(t, err, stringtest.JoinLF(loopback, notHTTP))

		_, err = doc.Decode[checkedServices](t.Context(), noLoopback, mirrors)
		require.EqualError(t, err, stringtest.JoinLF(loopback, notHTTP))
	})

	t.Run("a check of a type matches that type alone", func(t *testing.T) {
		t.Parallel()

		type config struct {
			Hosts map[string]hostName `yaml:"hosts"`
			Name  hostName            `yaml:"name"`
			Plain string              `yaml:"plain"`
		}

		doc := yamltest.FirstDocument(t, stringtest.Input(`
			name: web
			plain: text
			hosts:
			  a: db
		`))

		var strs, hosts []string

		_, err := doc.Decode[config](t.Context(),
			niceyaml.WithValueCheck(func(_ context.Context, s *string) error {
				strs = append(strs, *s)

				return nil
			}),
			niceyaml.WithValueCheck(func(_ context.Context, h *hostName) error {
				hosts = append(hosts, string(*h))

				return nil
			}),
		)
		require.NoError(t, err)
		assert.Equal(t, []string{"a", "text"}, strs, "a string check reads map keys and plain strings")
		assert.Equal(t, []string{"db", "web"}, hosts)
	})

	t.Run("a check of a map key binds at the key", func(t *testing.T) {
		t.Parallel()

		type config struct {
			Hosts map[hostName]string `yaml:"hosts"`
		}

		lower := niceyaml.WithValueCheck(func(_ context.Context, h *hostName) error {
			if strings.ToLower(string(*h)) != string(*h) {
				return niceyaml.NewError("host name is not lowercase")
			}

			return nil
		})

		_, err := yamltest.FirstDocument(t, "hosts:\n  web: a\n  DB: b\n").Decode[config](t.Context(), lower)
		require.EqualError(t, err, "3:3: $.hosts.DB~: host name is not lowercase")
	})

	t.Run("a nil function adds nothing", func(t *testing.T) {
		t.Parallel()

		_, err := yamltest.FirstDocument(t, "addr: 127.0.0.1\n").Decode[checkedService](
			t.Context(), niceyaml.WithValueCheck[netip.Addr](nil),
		)
		require.NoError(t, err)
	})

	t.Run("panics for a pointer or interface type", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			call func()
			want string
		}{
			"a pointer": {
				call: func() {
					niceyaml.WithValueCheck(func(context.Context, **checkedMirror) error { return nil })
				},
				want: "*niceyaml_test.checkedMirror",
			},
			"an interface": {
				call: func() {
					niceyaml.WithValueCheck(func(context.Context, *fmt.Stringer) error { return nil })
				},
				want: "fmt.Stringer",
			},
			"any": {
				call: func() { niceyaml.WithValueCheck[any](nil) },
				want: "interface {}",
			},
			"a pointer with a nil function": {
				call: func() { niceyaml.WithValueCheck[*netip.Addr](nil) },
				want: "*netip.Addr",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				assert.PanicsWithValue(t,
					"niceyaml.WithValueCheck: "+tc.want+" is a pointer or interface type, which the walk never checks",
					tc.call,
				)
			})
		}
	})
}

func TestWithJSONUnmarshalers(t *testing.T) {
	t.Parallel()

	type config struct {
		Size jsonSize `yaml:"size"`
	}

	tcs := map[string]struct {
		input string
		err   string
		opts  []niceyaml.DecodeOption
		want  int
	}{
		"decodes through the method": {
			input: "size: 4k\n",
			opts:  []niceyaml.DecodeOption{niceyaml.WithJSONUnmarshalers(true)},
			want:  4096,
		},
		"reads the fields by default": {
			input: "size: {bytes: 3}\n",
			want:  3,
		},
		"a later false turns the option off": {
			input: "size: {bytes: 3}\n",
			opts: []niceyaml.DecodeOption{
				niceyaml.WithJSONUnmarshalers(true),
				niceyaml.WithJSONUnmarshalers(false),
			},
			want: 3,
		},
		"the error of the method binds at the value": {
			input: "size: big\n",
			opts:  []niceyaml.DecodeOption{niceyaml.WithJSONUnmarshalers(true)},
			err:   `1:7: $.size: size: strconv.Atoi: parsing "big": invalid syntax`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := yamltest.FirstDocument(t, tc.input).Decode[config](t.Context(), tc.opts...)
			if tc.err != "" {
				require.EqualError(t, err, tc.err)
				require.ErrorIs(t, err, niceyaml.ErrDecode)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, got.Size.Bytes)
		})
	}

	t.Run("a type with an UnmarshalYAML method keeps it", func(t *testing.T) {
		t.Parallel()

		got, err := yamltest.FirstDocument(t, "size: 4k\n").Decode[map[string]yamlSize](
			t.Context(), niceyaml.WithJSONUnmarshalers(true),
		)
		require.NoError(t, err)
		assert.Equal(t, map[string]yamlSize{"size": {Bytes: 1}}, got)
	})
}

func TestWithYAMLOrderedMaps(t *testing.T) {
	t.Parallel()

	input := "b: 1\na: {d: 2, c: 3}\n"

	tcs := map[string]struct {
		want any
		opts []niceyaml.DecodeOption
	}{
		"a mapping decodes into a map by default": {
			want: map[string]any{
				"b": uint64(1),
				"a": map[string]any{"d": uint64(2), "c": uint64(3)},
			},
		},
		"a mapping keeps the order of the document": {
			opts: []niceyaml.DecodeOption{niceyaml.WithYAMLOrderedMaps(true)},
			want: yaml.MapSlice{
				{Key: "b", Value: uint64(1)},
				{Key: "a", Value: yaml.MapSlice{
					{Key: "d", Value: uint64(2)},
					{Key: "c", Value: uint64(3)},
				}},
			},
		},
		"a later false turns the option off": {
			opts: []niceyaml.DecodeOption{
				niceyaml.WithYAMLOrderedMaps(true),
				niceyaml.WithYAMLOrderedMaps(false),
			},
			want: map[string]any{
				"b": uint64(1),
				"a": map[string]any{"d": uint64(2), "c": uint64(3)},
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := yamltest.FirstDocument(t, input).Decode[any](t.Context(), tc.opts...)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("a typed map decodes as it does without the option", func(t *testing.T) {
		t.Parallel()

		got, err := yamltest.FirstDocument(t, "b: 1\na: 2\n").Decode[map[string]int](
			t.Context(), niceyaml.WithYAMLOrderedMaps(true),
		)
		require.NoError(t, err)
		assert.Equal(t, map[string]int{"b": 1, "a": 2}, got)
	})
}

func TestNode_YAMLComments(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		# The name of the service.
		name: api # short
		items:
		  # The first item.
		  a: {name: x}
	`)

	want := yaml.CommentMap{
		"$.name": {
			yaml.HeadComment(" The name of the service."),
			yaml.LineComment(" short"),
		},
		"$.items.a": {yaml.HeadComment(" The first item.")},
	}

	anchors := stringtest.Input(`
		shared: &shared
		  size: 1 # small
		items:
		  a: *shared
		  b: 2 # two
	`)

	tcs := map[string]struct {
		want  yaml.CommentMap
		input string
		path  paths.Path
	}{
		"document": {
			input: input,
			want:  want,
		},
		"node below the root keeps the paths of the document": {
			input: input,
			path:  paths.Doc().Child("items"),
			want:  yaml.CommentMap{"$.items.a": want["$.items.a"]},
		},
		"node below the root leaves out an anchor outside it": {
			input: anchors,
			path:  paths.Doc().Child("items"),
			want:  yaml.CommentMap{"$.items.b": {yaml.LineComment(" two")}},
		},
		"document without comments": {
			input: "name: api\n",
			want:  yaml.CommentMap{},
		},
		"empty document": {
			input: "",
			want:  yaml.CommentMap{},
		},
		"document of comments": {
			input: "# note\n",
			want:  yaml.CommentMap{},
		},
		"null": {
			input: "~ # none\n",
			want:  yaml.CommentMap{"$": {yaml.LineComment(" none")}},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			n := yamltest.AtOrSelf(t, yamltest.FirstDocument(t, tc.input), tc.path)

			got, err := n.YAMLComments(t.Context())
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	doc := yamltest.FirstDocument(t, input)

	t.Run("the encoder writes the comments back", func(t *testing.T) {
		t.Parallel()

		type service struct {
			Items map[string]map[string]string `yaml:"items"`
			Name  string                       `yaml:"name"`
		}

		got, err := doc.Decode[service](t.Context())
		require.NoError(t, err)

		comments, err := doc.YAMLComments(t.Context())
		require.NoError(t, err)

		got.Name = "web"

		out, err := encoder.Marshal(t.Context(), got, encoder.WithYAMLComments(comments))
		require.NoError(t, err)
		assert.Equal(t, stringtest.LinesLF(
			"items:",
			"  # The first item.",
			"  a:",
			"    name: x",
			"# The name of the service.",
			"name: web # short",
		), string(out))
	})

	t.Run("each call returns a map of its own", func(t *testing.T) {
		t.Parallel()

		first, err := doc.YAMLComments(t.Context())
		require.NoError(t, err)

		clear(first)

		second, err := doc.YAMLComments(t.Context())
		require.NoError(t, err)
		assert.Equal(t, want, second)
	})

	t.Run("one Node serves several calls at once", func(t *testing.T) {
		t.Parallel()

		var wg sync.WaitGroup

		for range 8 {
			wg.Go(func() {
				got, err := doc.YAMLComments(t.Context())
				if assert.NoError(t, err) {
					assert.Equal(t, want, got)
				}
			})
		}

		wg.Wait()
	})
}

func TestNode_YAMLComments_Errors(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		err   error
		input string
	}{
		"document that did not parse": {
			input: "a: 1 # one\nb: [\n",
			err:   niceyaml.ErrSyntax,
		},
		// The decoder collects the comment of a before it rejects b.
		"alias with no anchor": {
			input: "a: 1 # one\nb: *missing\n",
			err:   niceyaml.ErrDecode,
		},
		"document past the alias limit": {
			input: yamltest.AliasLevels(7),
			err:   niceyaml.ErrExcessiveAliasing,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			docs := niceyaml.NewSourceFromString(tc.input).AllDocuments()
			require.Len(t, docs, 1)

			got, err := docs[0].YAMLComments(t.Context())
			require.ErrorIs(t, err, tc.err)
			assert.Nil(t, got)
		})
	}

	t.Run("context that has ended", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		got, err := yamltest.FirstDocument(t, "a: 1 # one\n").YAMLComments(ctx)
		require.ErrorIs(t, err, context.Canceled)
		assert.Nil(t, got)
	})
}

// rawText is a value that decodes itself from the text go-yaml hands it.
type rawText struct {
	text string
}

func (r *rawText) UnmarshalYAML(data []byte) error {
	r.text = string(data)

	return nil
}

// echoingUnmarshaler reports errUnmarshal with the text go-yaml hands it,
// so the message of the decode quotes that text.
type echoingUnmarshaler struct{}

func (*echoingUnmarshaler) UnmarshalYAML(data []byte) error {
	return fmt.Errorf("%w: %q", errUnmarshal, data)
}

func TestDocument_Decode_ReusedAnchorNames(t *testing.T) {
	t.Parallel()

	t.Run("alias reads the anchor it refers to", func(t *testing.T) {
		t.Parallel()

		sibling := stringtest.LinesLF(
			"outer: &o",
			"  one: &y 1",
			"  two: &x {d: *y}",
			"use:",
			"  v: *x",
		)

		// Each alias reads the anchor of its name defined last before it,
		// as a path through the alias resolves, even when the node defines
		// the name again after the alias.
		tcs := map[string]struct {
			input string
			path  paths.Path
			want  any
		}{
			"alias beside text spelled like a renamed anchor": {
				input: stringtest.LinesLF(
					"a: &x 1",
					"b: &x 2",
					"c: x [1]",
					"d: *x",
				),
				path: paths.Current(),
				want: map[string]any{"a": uint64(1), "b": uint64(2), "c": "x [1]", "d": uint64(2)},
			},
			"failure in an anchor a later one hides": {
				input: stringtest.LinesLF(
					"a: &x !!bool nope",
					"b: &x 1",
					"c:",
					"  d: *x",
				),
				path: paths.Current().Child("c"),
				want: map[string]any{"d": uint64(1)},
			},
			"sequence redefines the name after the alias": {
				input: stringtest.LinesLF(
					"x0: &x 1",
					"l:",
					"  - *x",
					"  - &x 2",
				),
				path: paths.Current().Child("l"),
				want: []any{uint64(1), uint64(2)},
			},
			"mapping redefines the name after the alias": {
				input: stringtest.LinesLF(
					"b: &x 2",
					"d:",
					"  e: *x",
					"  f: &x 3",
				),
				path: paths.Current().Child("d"),
				want: map[string]any{"e": uint64(2), "f": uint64(3)},
			},
			"aliases before and after the redefinition": {
				input: stringtest.LinesLF(
					"a: &x 1",
					"b:",
					"  c: *x",
					"  d: &x 2",
					"  e: *x",
				),
				path: paths.Current().Child("b"),
				want: map[string]any{"c": uint64(1), "d": uint64(2), "e": uint64(2)},
			},
			"sequence aliases before and after the redefinition": {
				input: stringtest.LinesLF(
					"a: &x 1",
					"b:",
					"  - *x",
					"  - &x 2",
					"  - *x",
				),
				path: paths.Current().Child("b"),
				want: []any{uint64(1), uint64(2), uint64(2)},
			},
			"each element reads the anchor before it": {
				input: stringtest.LinesLF(
					"items:",
					"  - &x {a: 1}",
					"  - {b: *x}",
					"  - &x {a: 2}",
					"  - {b: *x}",
				),
				path: paths.Current().Child("items").Index(1),
				want: map[string]any{"b": map[string]any{"a": uint64(1)}},
			},
			"merge key that records an anchor again": {
				input: stringtest.LinesLF(
					"m: &m {k: &b 1}",
					"b: &b 2",
					"q: {<<: *m}",
					"node:",
					"  p: *b",
					"  r: *b",
				),
				path: paths.Current().Child("node"),
				want: map[string]any{"p": uint64(1), "r": uint64(1)},
			},
			"whole document with an alias to a sibling in an anchor": {
				input: sibling,
				path:  paths.Current(),
				want: map[string]any{
					"outer": map[string]any{
						"one": uint64(1),
						"two": map[string]any{"d": uint64(1)},
					},
					"use": map[string]any{"v": map[string]any{"d": uint64(1)}},
				},
			},
			"node with an alias to a sibling in an anchor": {
				input: sibling,
				path:  paths.Current().Child("use"),
				want:  map[string]any{"v": map[string]any{"d": uint64(1)}},
			},
			"node with an alias to a redefined sibling in an anchor": {
				input: stringtest.LinesLF(
					"d: &x",
					"  &y b: &x {q: *x}",
					"  a: &x",
					"    d: *y",
					"c:",
					"  <<: *x",
					"  b: *x",
					"a:",
					"  a: [*y, &y 5, *x]",
				),
				path: paths.Current().Child("a"),
				want: map[string]any{
					"a": []any{"b", uint64(5), map[string]any{"d": "b"}},
				},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				got, err := yamltest.At(t, dd, tc.path).Decode[any](t.Context())
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)

				var scoped any

				err = yamltest.At(t, dd, tc.path).DecodeInto(t.Context(), &scoped)
				require.NoError(t, err)
				assert.Equal(t, tc.want, scoped)
			})
		}
	})

	t.Run("typed decode reads the anchor the alias refers to", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, stringtest.LinesLF(
			"x0: &x 1",
			"l:",
			"  - *x",
			"  - &x 2",
		))

		list, err := yamltest.At(t, dd, paths.Current().Child("l")).Decode[[]int](t.Context())
		require.NoError(t, err)
		assert.Equal(t, []int{1, 2}, list)

		type inner struct {
			E int `yaml:"e"`
			F int `yaml:"f"`
		}

		type outer struct {
			D inner `yaml:"d"`
		}

		// The struct has no field for b, so the decode reads the anchor of
		// b only for the alias under d.
		dd = yamltest.FirstDocument(t, stringtest.LinesLF(
			"b: &x 2",
			"d:",
			"  e: *x",
			"  f: &x 3",
		))

		got, err := dd.Decode[outer](t.Context())
		require.NoError(t, err)
		assert.Equal(t, outer{D: inner{E: 2, F: 3}}, got)
	})

	t.Run("typed decode of an anchor reads the anchors of its definition", func(t *testing.T) {
		t.Parallel()

		type base struct {
			Image string `yaml:"image"`
		}

		type svc struct {
			Web     base   `yaml:"web"`
			Sidecar string `yaml:"sidecar"`
		}

		// The alias inside base reads the first img, while the sidecar
		// reads the second.
		dd := yamltest.FirstDocument(t, stringtest.Input(`
			image: &img nginx:1.0
			base: &base
			  image: *img
			image2: &img nginx:2.0
			svc:
			  web: *base
			  sidecar: *img
		`))

		got, err := yamltest.At(t, dd, paths.Current().Child("svc")).Decode[svc](t.Context())
		require.NoError(t, err)
		assert.Equal(t, svc{Web: base{Image: "nginx:1.0"}, Sidecar: "nginx:2.0"}, got)
	})

	t.Run("failure in an anchor the node reads fails the decode", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input string
			err   string
		}{
			"anchor the node reads": {
				input: stringtest.LinesLF(
					"a: &x 1",
					"b: &x !!bool nope",
					"c:",
					"  d: *x",
				),
				err: `2:14: $.b: cannot convert "nope" to boolean`,
			},
			"anchor another anchor reads": {
				input: stringtest.LinesLF(
					"a: &x 1",
					"m: &m {k: *x}",
					"b: &x !!bool nope",
					"c:",
					"  d: *x",
					"  e: *m",
				),
				err: `3:14: $.b: cannot convert "nope" to boolean`,
			},
			"anchor read before a later redefinition": {
				input: stringtest.LinesLF(
					"a: &x 1",
					"b: &x !!bool nope",
					"m: &m {k: *x}",
					"z: &x 2",
					"c:",
					"  e: *m",
					"  f: *x",
				),
				err: `2:14: $.b: cannot convert "nope" to boolean`,
			},
			"anchor inside a failing anchor": {
				input: stringtest.LinesLF(
					"q: &i 0",
					"o: &o {bad: !!bool nope, i: &i 1}",
					"c:",
					"  d: *i",
				),
				err: `2:20: $.o.bad: cannot convert "nope" to boolean`,
			},
			"anchor with an alias to nothing": {
				input: stringtest.LinesLF(
					"a: &x 1",
					"b: &x [*nope]",
					"c:",
					"  d: *x",
				),
				err: `2:8: $.b[0]: could not find alias "nope"`,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				_, err := dd.Decode[any](t.Context())
				require.ErrorIs(t, err, niceyaml.ErrDecode)

				_, err = yamltest.At(t, dd, paths.Current().Child("c")).Decode[any](t.Context())
				require.EqualError(t, err, tc.err)
				require.ErrorIs(t, err, niceyaml.ErrDecode)
			})
		}
	})

	t.Run("quoted name spelled like a renamed anchor", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input string
			want  any
			err   string
		}{
			"alias reads the anchor it refers to": {
				input: stringtest.LinesLF(
					"b: &x {q: 2}",
					`a: &"x [1]" {p: 1}`,
					"e: *x",
					"c: &x {q: 3}",
				),
				want: map[string]any{
					"a": map[string]any{"p": uint64(1)},
					"b": map[string]any{"q": uint64(2)},
					"c": map[string]any{"q": uint64(3)},
					"e": map[string]any{"q": uint64(2)},
				},
			},
			"error keeps the name the alias spells": {
				input: stringtest.LinesLF(
					`a: &"x [1]" 1`,
					"b: &x 2",
					"c: &x 3",
					`d: *"x [1]"`,
				),
				err: `4:4: $.d: could not find alias "\"x [1]\""`,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				got, err := dd.Decode[any](t.Context())
				if tc.err != "" {
					require.EqualError(t, err, tc.err)

					return
				}

				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
			})
		}
	})

	t.Run("text spelled like a renamed anchor", func(t *testing.T) {
		t.Parallel()

		type config struct {
			C echoingUnmarshaler `yaml:"c"`
			A int                `yaml:"a"`
			B int                `yaml:"b"`
			D time.Duration      `yaml:"d"`
		}

		// The two anchors named x get new names, and each message quotes
		// the text as the document spells it. The error of a value that
		// decodes itself keeps that text at the path of the value.
		tcs := map[string]struct {
			input string
			err   string
		}{
			"unknown key": {
				input: stringtest.LinesLF(
					"a: &x 1",
					"b: &x 2",
					`"x [1]": 3`,
				),
				err: `3:1: $.'x [1]'~: unknown field "x [1]"`,
			},
			"invalid duration": {
				input: stringtest.LinesLF(
					"a: &x 1",
					"b: &x 2",
					"d: x [2]",
				),
				err: `3:4: $.d: time: invalid duration "x [2]"`,
			},
			"escaped value": {
				input: stringtest.LinesLF(
					"a: &x 1",
					"b: &x 2",
					`d: "x\x20[1]"`,
				),
				err: `3:4: $.d: time: invalid duration "x [1]"`,
			},
			"folded value": {
				input: stringtest.LinesLF(
					"a: &x 1",
					"b: &x 2",
					"d: x",
					"  [1]",
				),
				err: `3:4: $.d: time: invalid duration "x [1]"`,
			},
			"unmarshaler text": {
				input: stringtest.LinesLF(
					"a: &x 1",
					"b: &x 2",
					"c: x [1]",
				),
				err: `3:4: $.c: unmarshaler rejected the value: "x [1]\n"`,
			},
			"unmarshaler text across tokens": {
				input: stringtest.LinesLF(
					"a: &x 1",
					"b: &x 2",
					"c: !x [1]",
				),
				err: `3:1: $.c: unmarshaler rejected the value: "!x [1]\n"`,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				_, err := dd.Decode[config](t.Context(), niceyaml.WithDisallowUnknownFields(true))
				require.EqualError(t, err, tc.err)
			})
		}
	})

	t.Run("later document after a folded node", func(t *testing.T) {
		t.Parallel()

		// The comment after the "..." marker parses to a node of its own,
		// so the second document is the third node of the file.
		input := stringtest.LinesLF(
			"a: 1",
			"...",
			"# note",
			"---",
			"a: &x 1",
			"b:",
			"  - *x",
			"  - &x 2",
			"  - *x",
		)

		docs := niceyaml.NewSourceFromString(input).Documents()
		require.Len(t, docs, 2)

		got, err := yamltest.At(t, docs[1], paths.Current().Child("b")).Decode[[]int](t.Context())
		require.NoError(t, err)
		assert.Equal(t, []int{1, 2, 2}, got)
	})

	t.Run("unmarshaler text spells the anchors of the document", func(t *testing.T) {
		t.Parallel()

		type wrapper struct {
			T rawText `yaml:"t"`
		}

		input := stringtest.LinesLF(
			"a: &x 1",
			"t:",
			"  p: &x 2",
			"  q: *x",
		)

		var want wrapper

		require.NoError(t, yaml.Unmarshal([]byte(input), &want))

		got, err := yamltest.FirstDocument(t, input).Decode[wrapper](t.Context())
		require.NoError(t, err)
		assert.Equal(t, want.T.text, got.T.text)
		assert.Contains(t, got.T.text, "&x 2")
	})

	t.Run("node spells an alias to a renamed anchor with its new name", func(t *testing.T) {
		t.Parallel()

		type wrapper struct {
			C ast.Node `yaml:"c"`
			D ast.Node `yaml:"d"`
		}

		dd := yamltest.FirstDocument(t, stringtest.LinesLF(
			"a: &x 1",
			"b: &x 2",
			"c: [*x, 3]",
			"d: {p: &x 4, q: *x}",
		))

		got, err := dd.Decode[wrapper](t.Context())
		require.NoError(t, err)
		require.NotNil(t, got.C)
		require.NotNil(t, got.D)
		assert.Equal(t, "[*x [2], 3]", got.C.String())
		assert.Equal(t, "{p: &x 4, q: *x [3]}", got.D.String())
	})

	t.Run("new names skip the counts the document spells", func(t *testing.T) {
		t.Parallel()

		type wrapper struct {
			C ast.Node `yaml:"c"`
		}

		tcs := map[string]struct {
			input string
			want  string
		}{
			"nothing spelled": {
				input: stringtest.LinesLF(
					"a: &x 1",
					"b: &x 2",
					"c: [*x]",
				),
				want: "[*x [2]]",
			},
			"several counts spelled": {
				input: stringtest.LinesLF(
					`a: &"x [1]" 1`,
					"b: &x 2 # x [2]",
					"d: a x [3] b",
					"e: &x 4",
					"c: [*x]",
				),
				want: "[*x [5]]",
			},
			"name and count in separate values": {
				input: stringtest.LinesLF(
					"a: &x 1",
					"b: &x 2",
					`d: "z [x"`,
					`e: " [2] z"`,
					"c: [*x]",
				),
				want: "[*x [2]]",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				got, err := yamltest.FirstDocument(t, tc.input).Decode[wrapper](t.Context())
				require.NoError(t, err)
				require.NotNil(t, got.C)
				assert.Equal(t, tc.want, got.C.String())
			})
		}
	})

	t.Run("new names skip the counts a reference document spells", func(t *testing.T) {
		t.Parallel()

		type wrapper struct {
			C ast.Node `yaml:"c"`
		}

		// The one anchor named x gets a new name, since the source has a
		// reference document.
		tcs := map[string]struct {
			reference string
			want      string
		}{
			"nothing spelled": {
				reference: "r: 1\n",
				want:      "[*x [1]]",
			},
			"count of another name": {
				reference: "r: y [1]\n",
				want:      "[*x [1]]",
			},
			"key": {
				reference: "\"x [1]\": 1\n",
				want:      "[*x [2]]",
			},
			"escaped value": {
				reference: "r: \"x\\x20[1]\"\n",
				want:      "[*x [2]]",
			},
			"tag before a flow sequence": {
				reference: "r: !x [1]\n",
				want:      "[*x [2]]",
			},
			"second reference": {
				reference: stringtest.LinesLF(
					"r: x [1]",
					"---",
					"r: x [2] # x [3]",
				),
				want: "[*x [4]]",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, "a: &x 1\nc: [*x]\n",
					niceyaml.WithReferences(niceyaml.NewSourceFromString(tc.reference)))

				got, err := dd.Decode[wrapper](t.Context())
				require.NoError(t, err)
				require.NotNil(t, got.C)
				assert.Equal(t, tc.want, got.C.String())
			})
		}
	})

	t.Run("text of a reference document spelled like a renamed anchor", func(t *testing.T) {
		t.Parallel()

		type config struct {
			V struct {
				A int `yaml:"a"`
			} `yaml:"v"`
			X int `yaml:"x"`
		}

		// The anchor named x gets a new name, and the message quotes the
		// key as the reference document spells it.
		dd := yamltest.FirstDocument(t, "x: &x 9\nv: *r\n",
			niceyaml.WithReferences(niceyaml.NewSourceFromString("r: &r {\"x [1]\": 2}\n")))

		_, err := dd.Decode[config](t.Context(), niceyaml.WithDisallowUnknownFields(true))
		require.EqualError(t, err, `2:4: unknown field "x [1]"`)
	})

	t.Run("documents of one source decode at once", func(t *testing.T) {
		t.Parallel()

		type wrapper struct {
			T rawText        `yaml:"t"`
			R int            `yaml:"r"`
			S map[string]any `yaml:"s"`
		}

		// The alias inside its own anchor makes each document write a null
		// into the tokens of the second parse, which the go-yaml formatter
		// reads for rawText in every other document.
		var sb strings.Builder

		for i := range 8 {
			fmt.Fprintf(&sb, stringtest.LinesLF(
				"---",
				"a: &x %d",
				"t:",
				"  p: &x 2",
				"  q: *x",
				"r: *x",
				"s: &s {s: *s}",
			), i)
		}

		docs := niceyaml.NewSourceFromString(sb.String()).Documents()

		var wg sync.WaitGroup

		for range 4 {
			for _, doc := range docs {
				scope := yamltest.At(t, doc, paths.Current().Child("t"))

				wg.Go(func() {
					got, err := doc.Decode[wrapper](t.Context())
					if assert.NoError(t, err) {
						assert.Equal(t, 2, got.R)
						assert.Contains(t, got.T.text, "&x 2")
						assert.Equal(t, map[string]any{"s": nil}, got.S)
					}

					scoped, err := scope.Decode[any](t.Context())
					if assert.NoError(t, err) {
						assert.Equal(t, map[string]any{"p": uint64(2), "q": uint64(2)}, scoped)
					}
				})
			}
		}

		wg.Wait()
	})

	t.Run("error names an anchor as the document does", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "a: &x 1\nb: &x {<<: *x}\n")

		_, err := dd.Decode[any](t.Context())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "alias name x")
		assert.NotContains(t, err.Error(), "[")
	})

	t.Run("error binds at the source", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, stringtest.LinesLF(
			"a: &x 1",
			"b: &x 2",
			"c: {k: !!bool nope}",
		))

		_, err := yamltest.At(t, dd, paths.Current().Child("c")).Decode[any](t.Context())
		require.EqualError(t, err, `3:15: $.c.k: cannot convert "nope" to boolean`)
		require.ErrorIs(t, err, niceyaml.ErrDecode)

		bound := yamltest.RequireSourceError(t, err)

		rng, ok := bound.Range()
		require.True(t, ok)
		assert.Equal(t, 2, rng.Start.Line)
	})
}

func TestDocument_Decode_ForwardAlias(t *testing.T) {
	t.Parallel()

	// An alias before every anchor of its name in the document reads the
	// anchor of a reference document, however many anchors of the name
	// follow the alias.
	refs := niceyaml.WithReferences(niceyaml.NewSourceFromString("defaults: &defaults {port: 80}\nx: &x 1\n"))

	tcs := map[string]struct {
		input  string
		path   paths.Path
		want   any
		scoped any
		err    string
	}{
		"merge key before the anchor": {
			input: stringtest.LinesLF(
				"service:",
				"  <<: *defaults",
				"defaults: &defaults",
				"  port: 8080",
			),
			path: paths.Current().Child("service"),
			want: map[string]any{
				"service":  map[string]any{"port": uint64(80)},
				"defaults": map[string]any{"port": uint64(8080)},
			},
			scoped: map[string]any{"port": uint64(80)},
			err:    "2:7: decoder rejected the value: cannot find anchor by alias name defaults",
		},
		"merge key before two anchors": {
			input: stringtest.LinesLF(
				"service:",
				"  <<: *defaults",
				"defaults: &defaults",
				"  port: 8080",
				"other: &defaults",
				"  port: 9090",
			),
			path: paths.Current().Child("service"),
			want: map[string]any{
				"service":  map[string]any{"port": uint64(80)},
				"defaults": map[string]any{"port": uint64(8080)},
				"other":    map[string]any{"port": uint64(9090)},
			},
			scoped: map[string]any{"port": uint64(80)},
			err:    "2:7: decoder rejected the value: cannot find anchor by alias name defaults",
		},
		"sequence alias before the anchor": {
			input:  "p: [*x, &x 2]\n",
			path:   paths.Current().Child("p"),
			want:   map[string]any{"p": []any{uint64(1), uint64(2)}},
			scoped: []any{uint64(1), uint64(2)},
			err:    `1:5: $.p[0]: could not find alias "x"`,
		},
		"mapping alias before the anchor": {
			input: stringtest.LinesLF(
				"p:",
				"  a: *x",
				"  b: &x 2",
			),
			path:   paths.Current().Child("p"),
			want:   map[string]any{"p": map[string]any{"a": uint64(1), "b": uint64(2)}},
			scoped: map[string]any{"a": uint64(1), "b": uint64(2)},
			err:    `2:6: $.p.a: could not find alias "x"`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dd := yamltest.FirstDocument(t, tc.input, refs)

			got, err := dd.Decode[any](t.Context())
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)

			scoped, err := yamltest.At(t, dd, tc.path).Decode[any](t.Context())
			require.NoError(t, err)
			assert.Equal(t, tc.scoped, scoped)

			// Without the reference document, the alias finds no anchor.
			bare := yamltest.FirstDocument(t, tc.input)

			_, err = bare.Decode[any](t.Context())
			require.EqualError(t, err, tc.err)

			_, err = yamltest.At(t, bare, tc.path).Decode[any](t.Context())
			require.EqualError(t, err, tc.err)
		})
	}
}

func TestDocument_DecodeInto(t *testing.T) {
	t.Parallel()

	t.Run("keeps fields absent from the document", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: test")

		result := plainConfig{Name: "default", Value: 7}

		err := dd.DecodeInto(t.Context(), &result)
		require.NoError(t, err)
		assert.Equal(t, plainConfig{Name: "test", Value: 7}, result)
	})

	t.Run("leaves the value as it is for a document that holds no value", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input string
		}{
			// The body of each of these documents holds nothing, only
			// comments, or only a directive above an empty document.
			"empty":        {input: ""},
			"comment only": {input: "# just a comment\n"},
			"directive":    {input: "%YAML 1.2\n---\n"},
			// Each of these holds a null.
			"null":           {input: "null\n"},
			"tilde":          {input: "~\n"},
			"anchored null":  {input: "&a null\n"},
			"anchored tilde": {input: "&a ~\n"},
			// Each of these tags a value it leaves out, which the go-yaml
			// decoder reads as null.
			"map tag after a header": {input: "--- !!map\n"},
			"seq tag":                {input: "!!seq\n"},
			"null tag":               {input: "!!null\n"},
			"local tag":              {input: "!custom\n"},
			"anchored seq tag":       {input: "&a !!seq\n"},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				slice := []string{"default"}

				err := dd.DecodeInto(t.Context(), &slice)
				require.NoError(t, err)
				assert.Equal(t, []string{"default"}, slice)

				array := [2]string{"a", "b"}

				err = dd.DecodeInto(t.Context(), &array)
				require.NoError(t, err)
				assert.Equal(t, [2]string{"a", "b"}, array)

				config := plainConfig{Name: "default", Value: 7}

				err = dd.DecodeInto(t.Context(), &config)
				require.NoError(t, err)
				assert.Equal(t, plainConfig{Name: "default", Value: 7}, config)

				value := 1

				err = dd.DecodeInto(t.Context(), &value)
				require.NoError(t, err)
				assert.Equal(t, 1, value)

				got, err := dd.Decode[map[string]any](t.Context())
				require.NoError(t, err)
				assert.Nil(t, got)
			})
		}
	})

	t.Run("leaves the value as it is for a scoped tagged null", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "items: !!seq\n")
		scoped := yamltest.At(t, dd, paths.Current().Child("items"))

		got, err := scoped.Decode[[]string](t.Context())
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("leaves the value as it is for a scoped null, as a field does", func(t *testing.T) {
		t.Parallel()

		type nullFields struct {
			Map    map[string]int `yaml:"map"`
			Config plainConfig    `yaml:"config"`
			Items  []plainConfig  `yaml:"items"`
			Int    int            `yaml:"int"`
			Uint   uint           `yaml:"uint"`
		}

		defaults := func() nullFields {
			return nullFields{
				Map:    map[string]int{"k": 1},
				Config: plainConfig{Name: "default", Value: 7},
				Int:    1,
				Uint:   2,
			}
		}

		// The go-yaml decoder rejects each of these nulls at the top of a
		// decode into an int, a uint, a map, or a struct. Each case names
		// how the document spells the null after a ":" or a "-".
		tcs := map[string]struct {
			null string
		}{
			"no value": {null: ""},
			"null":     {null: " null"},
			"tilde":    {null: " ~"},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, stringtest.LinesLF(
					"int:"+tc.null,
					"uint:"+tc.null,
					"map:"+tc.null,
					"config:"+tc.null,
					"items:",
					"  -"+tc.null,
				))

				want := defaults()
				want.Items = []plainConfig{{}}

				whole := defaults()

				err := dd.DecodeInto(t.Context(), &whole)
				require.NoError(t, err)
				assert.Equal(t, want, whole)

				scoped := defaults()

				for key, target := range map[string]any{
					"int":    &scoped.Int,
					"uint":   &scoped.Uint,
					"map":    &scoped.Map,
					"config": &scoped.Config,
				} {
					err = yamltest.At(t, dd, paths.Current().Child(key)).DecodeInto(t.Context(), target)
					require.NoError(t, err, key)
				}

				items, err := dd.Nodes(paths.Current().Child("items").IndexAll())
				require.NoError(t, err)

				for _, item := range items {
					got, err := item.Decode[plainConfig](t.Context())
					require.NoError(t, err)

					scoped.Items = append(scoped.Items, got)
				}

				assert.Equal(t, want, scoped)
			})
		}
	})

	t.Run("leaves the value as it is for a scoped anchored null", func(t *testing.T) {
		t.Parallel()

		type intField struct {
			Int int `yaml:"int"`
		}

		// A path through an anchor reaches the null the anchor names, so a
		// scoped decode reads it as a decode of an anchored null document
		// reads it.
		tcs := map[string]struct {
			input string
		}{
			"null":  {input: "int: &i null\nconfig: &c null\n"},
			"tilde": {input: "int: &i ~\nconfig: &c ~\n"},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				value := 1

				err := yamltest.At(t, dd, paths.Current().Child("int")).DecodeInto(t.Context(), &value)
				require.NoError(t, err)
				assert.Equal(t, 1, value)

				config := plainConfig{Name: "default", Value: 7}

				err = yamltest.At(t, dd, paths.Current().Child("config")).DecodeInto(t.Context(), &config)
				require.NoError(t, err)
				assert.Equal(t, plainConfig{Name: "default", Value: 7}, config)

				// The go-yaml decoder rejects an anchored null in an int field.
				whole := intField{Int: 1}

				err = dd.DecodeInto(t.Context(), &whole)
				require.ErrorIs(t, err, niceyaml.ErrDecode)
			})
		}
	})

	t.Run("rejects a tagged value the decoder cannot read", func(t *testing.T) {
		t.Parallel()

		type listConfig struct {
			Items []string `yaml:"items"`
		}

		// The go-yaml decoder reads a sequence out of each tagged string
		// and reports the tagged value.
		tcs := map[string]struct {
			input  string
			decode func(ctx context.Context, dd *niceyaml.Node) error
			err    string
		}{
			"str tag in a field": {
				input:  "items: !!str foo\n",
				decode: decodeAs[listConfig](),
				err:    "1:14: $.items: expected sequence, got string",
			},
			"str tag into a slice": {
				input:  "!!str foo\n",
				decode: decodeAs[[]string](),
				err:    "1:7: $: expected sequence, got string",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				var err error

				require.NotPanics(t, func() {
					err = tc.decode(t.Context(), dd)
				})
				require.EqualError(t, err, tc.err)
				require.ErrorIs(t, err, niceyaml.ErrDecode)

				require.ErrorAs(t, err, new(*niceyaml.SourceError), "the rejection is not bound to the source")
			})
		}
	})

	t.Run("decodes a seq tag without a value in a field as no value", func(t *testing.T) {
		t.Parallel()

		type listConfig struct {
			Name  string   `yaml:"name"`
			Items []string `yaml:"items"`
		}

		dd := yamltest.FirstDocument(t, "name: x\nitems: !!seq\n")

		got, err := dd.Decode[listConfig](t.Context())
		require.NoError(t, err)
		assert.Equal(t, listConfig{Name: "x"}, got)
	})

	t.Run("locates a panic at the value, not a head comment", func(t *testing.T) {
		t.Parallel()

		type listConfig struct {
			Items panickingUnmarshaler `yaml:"items"`
		}

		tcs := map[string]struct {
			input string
			path  paths.Path
			want  position.Position
		}{
			"whole document": {
				input: stringtest.LinesLF(
					"# about the file",
					"",
					"name: x",
					"items: [a]",
				),
				path: paths.Current(),
				want: position.New(2, 0),
			},
			"scoped node": {
				input: stringtest.LinesLF(
					"a: 1",
					"wrap:",
					"  # the list",
					"  items: [a]",
				),
				path: paths.Current().Child("wrap"),
				want: position.New(3, 2),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				var err error

				require.NotPanics(t, func() {
					_, err = yamltest.At(t, dd, tc.path).Decode[listConfig](t.Context())
				})
				require.NotErrorIs(t, err, niceyaml.ErrDecode)
				require.ErrorContains(t, err, "decoder panicked: "+errUnmarshal.Error())
				requirePanic(t, err)

				_, ok := errors.AsType[yaml.Error](err)
				assert.False(t, ok, "a go-yaml error is in the chain")

				srcErr := yamltest.RequireSourceError(t, err)

				rng, ok := srcErr.Range()
				require.True(t, ok, "the rejection has no location")
				assert.Equal(t, tc.want, rng.Start)
			})
		}
	})

	t.Run("rejects a target that is not a non-nil pointer", func(t *testing.T) {
		t.Parallel()

		var nilConfig *plainConfig

		tcs := map[string]struct {
			target any
			want   string
		}{
			"nil": {
				target: nil,
				want:   "decode target is not a non-nil pointer: got nil",
			},
			"value": {
				target: 5,
				want:   "decode target is not a non-nil pointer: got int",
			},
			"nil pointer": {
				target: nilConfig,
				want:   "decode target is not a non-nil pointer: got *niceyaml_test.plainConfig",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				var called bool

				dd := yamltest.FirstDocument(t, "name: test")

				err := dd.DecodeInto(t.Context(), tc.target, niceyaml.WithValidator(nameSchema(&called)))
				require.ErrorIs(t, err, niceyaml.ErrDecodeTarget)

				require.ErrorAs(t, err, new(*niceyaml.SourceError))
				assert.Equal(t, tc.want, err.Error())
				assert.False(t, called, "the validator should not run for a bad target")
			})
		}
	})

	t.Run("binds a rejected target to the source", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocumentWithPath(t, "name: test", "f.yaml")

		err := dd.DecodeInto(t.Context(), nil)
		require.ErrorIs(t, err, niceyaml.ErrDecodeTarget)

		require.ErrorAs(t, err, new(*niceyaml.SourceError))
		assert.Equal(t, "f.yaml: decode target is not a non-nil pointer: got nil", err.Error())
	})

	t.Run("decodes through a pointer target", func(t *testing.T) {
		t.Parallel()

		// A selfPointer never reaches a value that is not a pointer.
		type selfPointer *selfPointer

		// Decodes into a pointer to a config and returns the pointer the
		// decode leaves.
		intoConfig := func(t *testing.T, dd *niceyaml.Node) (any, error) {
			t.Helper()

			got := &plainConfig{Value: 7}

			err := dd.DecodeInto(t.Context(), &got)

			return got, err //nolint:wrapcheck // The test inspects the error of the decode.
		}

		// Decodes as intoConfig does, and checks that the decode left the
		// pointer in place.
		intoSameConfig := func(t *testing.T, dd *niceyaml.Node) (any, error) {
			t.Helper()

			got := &plainConfig{Value: 7}
			before := got

			err := dd.DecodeInto(t.Context(), &got)
			assert.Same(t, before, got, "the decode replaced the pointer")

			return got, err //nolint:wrapcheck // The test inspects the error of the decode.
		}

		tcs := map[string]struct {
			decode     func(t *testing.T, dd *niceyaml.Node) (any, error)
			want       any
			err        error
			input      string
			references string
		}{
			"Decode validates the value": {
				input:  "name: a\nvalue: 1\n",
				decode: decodedAs[*validatorConfig],
				want:   &validatorConfig{Name: "a", Value: 1, validated: true},
			},
			"Decode returns the error of Validate": {
				input:  "value: 1\n",
				decode: decodedAs[*validatorConfig],
				want:   (*validatorConfig)(nil),
				err:    errNameRequired,
			},
			"DecodeInto allocates a nil pointer": {
				input: "name: a\nvalue: 1\n",
				decode: func(t *testing.T, dd *niceyaml.Node) (any, error) {
					t.Helper()

					var got *validatorConfig

					err := dd.DecodeInto(t.Context(), &got)

					return got, err //nolint:wrapcheck // The test inspects the error of the decode.
				},
				want: &validatorConfig{Name: "a", Value: 1, validated: true},
			},
			"DecodeInto keeps the fields of a non-nil pointer": {
				input:  "name: a\n",
				decode: intoSameConfig,
				want:   &plainConfig{Name: "a", Value: 7},
			},
			"null": {
				input:  "null\n",
				decode: decodedAs[*validatorConfig],
				want:   (*validatorConfig)(nil),
			},
			"DecodeInto sets a non-nil pointer to nil for null": {
				input:  "null\n",
				decode: intoConfig,
				want:   (*plainConfig)(nil),
			},
			"DecodeInto keeps a non-nil pointer for a tagged null": {
				input:  "!!null\n",
				decode: intoSameConfig,
				want:   &plainConfig{Value: 7},
			},
			"anchored null": {
				input:  "&a null\n",
				decode: decodedAs[*validatorConfig],
				want:   (*validatorConfig)(nil),
			},
			"alias to a null of a reference document": {
				input:      "*x\n",
				references: "a: &x ~\n",
				decode:     decodedAs[*validatorConfig],
				want:       (*validatorConfig)(nil),
			},
			"DecodeInto sets a non-nil pointer to nil for an alias to a null": {
				input:      "*x\n",
				references: "a: &x ~\n",
				decode:     intoConfig,
				want:       (*plainConfig)(nil),
			},
			"anchored alias to a null of a reference document": {
				input:      "&y\n*x\n",
				references: "a: &x ~\n",
				decode:     decodedAs[*validatorConfig],
				want:       (*validatorConfig)(nil),
			},
			"alias to a tagged null of a reference document": {
				input:      "*x\n",
				references: "a: &x !!null\n",
				decode:     decodedAs[*string],
				want:       (*string)(nil),
			},
			"alias to a scalar of a reference document": {
				input:      "*x\n",
				references: "a: &x 7\n",
				decode:     decodedAs[*int],
				want:       new(7),
			},
			"comment only": {
				input:  "# comment\n",
				decode: decodedAs[*validatorConfig],
				want:   (*validatorConfig)(nil),
			},
			"scalar": {
				input:  "7\n",
				decode: decodedAs[*int],
				want:   new(7),
			},
			"string tag over no value": {
				input:  "!!str\n",
				decode: decodedAs[*string],
				want:   new(""),
			},
			"pointer to a pointer": {
				input:  "7\n",
				decode: decodedAs[**int],
				want:   new(new(7)),
			},
			"pointer type that refers to itself": {
				input:  "a: 1\n",
				decode: decodedAs[selfPointer],
				want:   selfPointer(nil),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				var opts []niceyaml.SourceOption

				if tc.references != "" {
					opts = append(opts, niceyaml.WithReferences(niceyaml.NewSourceFromString(tc.references)))
				}

				dd := yamltest.FirstDocument(t, tc.input, opts...)

				got, err := tc.decode(t, dd)
				if tc.err != nil {
					require.ErrorIs(t, err, tc.err)
				} else {
					require.NoError(t, err)
				}

				assert.Equal(t, tc.want, got)
			})
		}
	})

	t.Run("runs schema and Validate around the decode", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: test")

		result := validatorConfig{Value: 7}

		var called bool

		err := dd.DecodeInto(t.Context(), &result, niceyaml.WithValidator(nameSchema(&called)))
		require.NoError(t, err)
		assert.Equal(t, "test", result.Name)
		assert.Equal(t, 7, result.Value)
		assert.True(t, called, "the validator should have been called")
		assert.True(t, result.validated, "Validate() should have been called")
	})
}

// mergeLeaf is a struct with two fields, so a second decode can set one
// and leave the other.
type mergeLeaf struct {
	A int `yaml:"a"`
	B int `yaml:"b"`
}

// mergeNested holds a [mergeLeaf] one struct down.
type mergeNested struct {
	Name string    `yaml:"name"`
	Leaf mergeLeaf `yaml:"leaf"`
}

// MergeInline holds the fields [mergeTarget] inlines.
type MergeInline struct {
	InA int `yaml:"in_a"`
	InB int `yaml:"in_b"`
}

// MergeShared holds the fields [mergeTarget] inlines through a pointer.
type MergeShared struct {
	SharedA int `yaml:"shared_a"`
	SharedB int `yaml:"shared_b"`
}

// mergeTarget holds a field of each kind that a second decode into one
// value merges or replaces.
type mergeTarget struct {
	*MergeShared `yaml:",inline"`
	MergeInline  `yaml:",inline"`

	Any     any                  `yaml:"any"`
	Map     map[string]mergeLeaf `yaml:"map"`
	Pointer *mergeLeaf           `yaml:"pointer"`
	Number  *int                 `yaml:"number"`
	Slice   []mergeLeaf          `yaml:"slice"`
	Nested  mergeNested          `yaml:"nested"`
	Struct  mergeLeaf            `yaml:"struct"`
	Array   [2]mergeLeaf         `yaml:"array"`
}

func TestNode_DecodeInto_Merge(t *testing.T) {
	t.Parallel()

	base := stringtest.Input(`
		in_a: 1
		in_b: 2
		shared_a: 1
		shared_b: 2
		struct: {a: 1, b: 2}
		nested: {name: n, leaf: {a: 1, b: 2}}
		pointer: {a: 1, b: 2}
		number: 1
		slice: [{a: 1, b: 2}, {a: 3, b: 4}]
		array: [{a: 1, b: 2}, {a: 3, b: 4}]
		map: {k: {a: 1, b: 2}, j: {a: 3, b: 4}}
		any: {k: v, j: w}
	`)

	// The value a decode of base fills, built anew for each case.
	decoded := func() mergeTarget {
		number := 1

		return mergeTarget{
			InA:         1,
			InB:         2,
			MergeShared: &MergeShared{SharedA: 1, SharedB: 2},
			Struct:      mergeLeaf{A: 1, B: 2},
			Nested:      mergeNested{Name: "n", Leaf: mergeLeaf{A: 1, B: 2}},
			Pointer:     &mergeLeaf{A: 1, B: 2},
			Number:      &number,
			Slice:       []mergeLeaf{{A: 1, B: 2}, {A: 3, B: 4}},
			Array:       [2]mergeLeaf{{A: 1, B: 2}, {A: 3, B: 4}},
			Map:         map[string]mergeLeaf{"k": {A: 1, B: 2}, "j": {A: 3, B: 4}},
			Any:         map[string]any{"k": "v", "j": "w"},
		}
	}

	// Each case decodes base into a value and then over into the same
	// value. The want func changes the value base filled into the value
	// both leave, and a nil one says over changed nothing. A program that
	// decodes one file over another relies on these results, and so does
	// a decode of [niceyaml.NewSourceFromLayers] into a value that holds defaults, so
	// a go-yaml release that changes one fails here first.
	tcs := map[string]struct {
		want func(v *mergeTarget)
		over string
	}{
		"a struct merges field by field": {
			over: "struct: {a: 9}\n",
			want: func(v *mergeTarget) { v.Struct.A = 9 },
		},
		"a nested struct merges at every depth": {
			over: "nested: {leaf: {a: 9}}\n",
			want: func(v *mergeTarget) { v.Nested.Leaf.A = 9 },
		},
		"an inline struct merges": {
			over: "in_a: 9\n",
			want: func(v *mergeTarget) { v.InA = 9 },
		},
		"an inline pointer to a struct merges": {
			over: "shared_a: 9\n",
			want: func(v *mergeTarget) { v.SharedA = 9 },
		},
		"a pointer to a struct merges": {
			over: "pointer: {a: 9}\n",
			want: func(v *mergeTarget) { v.Pointer.A = 9 },
		},
		"an empty mapping keeps a struct": {
			over: stringtest.LinesLF(
				"struct: {}",
				"nested: {leaf: {}}",
				"pointer: {}",
			),
		},
		"a slice replaces": {
			over: "slice: [{a: 9}]\n",
			want: func(v *mergeTarget) { v.Slice = []mergeLeaf{{A: 9}} },
		},
		"an empty sequence replaces a slice": {
			over: "slice: []\n",
			want: func(v *mergeTarget) { v.Slice = []mergeLeaf{} },
		},
		"an array replaces": {
			over: "array: [{a: 9}]\n",
			want: func(v *mergeTarget) { v.Array = [2]mergeLeaf{{A: 9}} },
		},
		"a map replaces": {
			over: "map: {k: {a: 9}}\n",
			want: func(v *mergeTarget) { v.Map = map[string]mergeLeaf{"k": {A: 9}} },
		},
		"an empty mapping replaces a map": {
			over: "map: {}\n",
			want: func(v *mergeTarget) { v.Map = map[string]mergeLeaf{} },
		},
		"a value of an interface type replaces": {
			over: "any: {z: y}\n",
			want: func(v *mergeTarget) { v.Any = map[string]any{"z": "y"} },
		},
		"a null keeps every kind but a pointer": {
			over: stringtest.LinesLF(
				"struct: {a: null}",
				"nested: null",
				"slice: null",
				"array: null",
				"map: null",
				"any: null",
			),
		},
		"a null sets a pointer to nil": {
			over: "pointer: null\nnumber: null\n",
			want: func(v *mergeTarget) { v.Pointer, v.Number = nil, nil },
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var got mergeTarget

			err := yamltest.FirstDocument(t, base).DecodeInto(t.Context(), &got)
			require.NoError(t, err)
			require.Equal(t, decoded(), got)

			err = yamltest.FirstDocument(t, tc.over).DecodeInto(t.Context(), &got)
			require.NoError(t, err)

			want := decoded()
			if tc.want != nil {
				tc.want(&want)
			}

			assert.Equal(t, want, got)
		})
	}
}

func TestDocument_Decode_ValueReceivers(t *testing.T) {
	t.Parallel()

	t.Run("calls value receiver hooks", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: test")

		result, err := dd.Decode[valueValidatorConfig](t.Context())
		require.NoError(t, err)
		assert.Equal(t, "test", result.Name)
	})

	t.Run("returns value receiver Validate error", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, `name: ""`)

		result, err := dd.Decode[valueValidatorConfig](t.Context())
		require.ErrorIs(t, err, errNameRequired)
		assert.Zero(t, result)
	})
}

// valueValidatorConfig implements niceyaml.SelfValidator with a value receiver.
type valueValidatorConfig struct {
	Name string `yaml:"name"`
}

func (c valueValidatorConfig) Validate() error {
	if c.Name == "" {
		return errNameRequired
	}

	return nil
}

func TestWithAllowDuplicateKeys(t *testing.T) {
	t.Parallel()

	type config struct {
		Name string `yaml:"name"`
	}

	input := stringtest.Input(`
		name: first
		name: second
	`)

	t.Run("the parser rejects duplicate keys", func(t *testing.T) {
		t.Parallel()

		tcs := map[string][]niceyaml.SourceOption{
			"without option": nil,
			"a later false turns the option off": {
				niceyaml.WithAllowDuplicateKeys(true),
				niceyaml.WithAllowDuplicateKeys(false),
			},
		}

		for name, opts := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				_, err := niceyaml.NewSourceFromString(input, opts...).File()
				require.Error(t, err)
				assert.Contains(t, err.Error(), `mapping key "name" already defined`)
			})
		}
	})

	t.Run("with option the last value wins", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, input, niceyaml.WithAllowDuplicateKeys(true))

		result, err := doc.Decode[config](t.Context())
		require.NoError(t, err)
		assert.Equal(t, "second", result.Name)
	})

	t.Run("At resolves the value the decode keeps", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, input, niceyaml.WithAllowDuplicateKeys(true))
		name := yamltest.At(t, doc, paths.Current().Child("name"))

		got, err := name.Decode[string](t.Context())
		require.NoError(t, err)
		assert.Equal(t, "second", got)
	})

	t.Run("without option a decode keeps the later of two keys it reads as one", func(t *testing.T) {
		t.Parallel()

		aliased := yamltest.FirstDocument(t, "&k name: first\n*k : second\n")

		result, err := aliased.Decode[config](t.Context())
		require.NoError(t, err)
		assert.Equal(t, "second", result.Name)

		respelled := yamltest.FirstDocument(t, "1: first\n0x1: second\n")

		got, err := respelled.Decode[map[string]string](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"1": "second"}, got)
	})

	t.Run("without option a decode accepts a duplicate key of a reference document", func(t *testing.T) {
		t.Parallel()

		ref := niceyaml.NewSourceFromString("defaults: &defaults {name: first, name: second}\n")

		_, err := ref.File()
		require.ErrorIs(t, err, niceyaml.ErrSyntax)

		doc := yamltest.FirstDocument(t, "server: *defaults\n", niceyaml.WithReferences(ref))

		got, err := doc.Decode[map[string]config](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]config{"server": {Name: "second"}}, got)
	})
}

func TestDocument_Decode_MergeOverride(t *testing.T) {
	t.Parallel()

	type server struct {
		Host string `yaml:"host"`
		Port int    `yaml:"port"`
	}

	// Each case decodes the server of input into a struct and into a map
	// of a type, which are the targets the go-yaml decoder checks for a
	// duplicate key.
	tcs := map[string]struct {
		input string
		want  server
	}{
		"a key after the merge overrides it": {
			input: stringtest.LinesLF(
				"defaults: &defaults {host: h, port: 1}",
				"server:",
				"  <<: *defaults",
				"  port: 2",
			),
			want: server{Host: "h", Port: 2},
		},
		"a merge after the key overrides it": {
			input: stringtest.LinesLF(
				"defaults: &defaults {host: h, port: 1}",
				"server:",
				"  port: 2",
				"  <<: *defaults",
			),
			want: server{Host: "h", Port: 1},
		},
		"a key overrides a mapping the merge key holds": {
			input: stringtest.LinesLF(
				"server:",
				"  <<: {host: h, port: 1}",
				"  port: 2",
			),
			want: server{Host: "h", Port: 2},
		},
		"a key overrides a merge in a flow mapping": {
			input: "defaults: &defaults {host: h, port: 1}\nserver: {<<: *defaults, port: 2}\n",
			want:  server{Host: "h", Port: 2},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			at := paths.Current().Child("server")
			doc := yamltest.FirstDocument(t, tc.input)

			got, err := doc.DecodeAt[server](t.Context(), at)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)

			whole, err := doc.Decode[map[string]server](t.Context())
			require.NoError(t, err)
			assert.Equal(t, tc.want, whole["server"])

			typed, err := doc.DecodeAt[map[string]any](t.Context(), at)
			require.NoError(t, err)
			assert.Equal(t, map[string]any{"host": tc.want.Host, "port": uint64(tc.want.Port)}, typed)

			// A path reads the entry the decode keeps.
			port, err := doc.DecodeAt[int](t.Context(), at.Child("port"))
			require.NoError(t, err)
			assert.Equal(t, tc.want.Port, port)
		})
	}
}

// plainValidated is a [niceyaml.SelfValidator] that returns an error with no
// location.
type plainValidated struct {
	Name string `yaml:"name"`
}

func (plainValidated) Validate() error {
	return errPlainValidation
}

func TestDocument_ErrorsBindToSource(t *testing.T) {
	t.Parallel()

	namePath := paths.Current().Child("name")

	// The helper asserts err is a [*niceyaml.SourceError] bound to the
	// source of doc.
	requireBound := func(t *testing.T, doc *niceyaml.Node, err error) {
		t.Helper()

		bound, ok := err.(*niceyaml.SourceError) //nolint:errorlint // The top-level value is the bound error.
		require.True(t, ok, "want *niceyaml.SourceError, got %T", err)
		assert.Same(t, doc.Source(), bound.Source())
	}

	type config struct {
		Name string `yaml:"name"`
	}

	failing := rejectingValidator(niceyaml.NewError("bad name", niceyaml.AtPath(namePath)))

	tcs := map[string]struct {
		call func(ctx context.Context, doc *niceyaml.Node) error
		// An error the result matches, for a case that names one.
		is    error
		input string
	}{
		"Decode binds a decoding error": {
			input: "name: [1, 2]\n",
			call:  decodeAs[config](),
			is:    niceyaml.ErrDecode,
		},
		"Decode binds a schema error": {
			input: "name: a\n",
			call:  decodeAs[config](niceyaml.WithValidator(failing)),
		},
		"DecodeInto binds a schema error": {
			input: "name: a\n",
			call: func(ctx context.Context, doc *niceyaml.Node) error {
				return doc.DecodeInto(ctx, new(config), niceyaml.WithValidator(failing))
			},
		},
		"DecodeAt binds a decoding error": {
			input: "name: [1, 2]\n",
			call: func(ctx context.Context, doc *niceyaml.Node) error {
				_, err := doc.DecodeAt[string](ctx, namePath)

				return err
			},
			is: niceyaml.ErrDecode,
		},
		"Validate binds a validator error": {
			input: "name: a\n",
			call: func(ctx context.Context, doc *niceyaml.Node) error {
				return doc.Validate(ctx, failing)
			},
		},
		"Decode binds a plain self validation error": {
			input: "name: a\n",
			call:  decodeAs[plainValidated](),
			is:    errPlainValidation,
		},
		"At binds a path resolution error": {
			input: "name: a\n",
			call: func(_ context.Context, doc *niceyaml.Node) error {
				_, err := doc.At(paths.Current().Child("missing"))

				return err //nolint:wrapcheck // The test inspects the error of the call.
			},
			is: paths.ErrNotFound,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input)

			err := tc.call(t.Context(), doc)
			requireBound(t, doc, err)

			if tc.is != nil {
				require.ErrorIs(t, err, tc.is)
			}
		})
	}

	t.Run("a validator error bound to the source comes back as it is", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocument(t, "name: a\n")

		var pre error

		validator := niceyaml.ValidatorFunc(func(_ context.Context, doc *niceyaml.Node) error {
			pre = yamltest.Bind(t, doc.Source(), niceyaml.NewError("bad name", niceyaml.AtPath(namePath)))

			return pre
		})

		_, err := doc.Decode[config](t.Context(), niceyaml.WithValidator(validator))
		assert.Same(t, pre, err)
		requireBound(t, doc, err)
	})
}

func TestDocument_Decode_Validator(t *testing.T) {
	t.Parallel()

	t.Run("receives the document and decodes on success", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: test\nvalue: 42\n")

		var got *niceyaml.Node

		capture := niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
			got = n

			return nil
		})

		result, err := dd.Decode[plainConfig](t.Context(), niceyaml.WithValidator(capture))
		require.NoError(t, err)
		assert.Same(t, dd, got)
		assert.Same(t, dd, got.Document())
		assert.Equal(t, "test", result.Name)
		assert.Equal(t, 42, result.Value)
	})

	t.Run("decodes the node with the settings of the source", func(t *testing.T) {
		t.Parallel()

		// The alias names an anchor of the reference document the source
		// holds. The ordered map option reaches the decode that gets it and
		// no decode the validator runs.
		refs := niceyaml.NewSourceFromString("base: &x 1\n")
		dd := yamltest.FirstDocument(t, stringtest.LinesLF("b:", "  c: *x"), niceyaml.WithReferences(refs))
		ordered := niceyaml.WithYAMLOrderedMaps(true)

		var (
			seen  []any
			nodes []*niceyaml.Node
		)

		record := niceyaml.ValidatorFunc(func(ctx context.Context, n *niceyaml.Node) error {
			nodes = append(nodes, n, n.Document())

			for _, node := range []*niceyaml.Node{n, n.Document()} {
				data, err := node.Decode[any](ctx)
				if err != nil {
					return err
				}

				seen = append(seen, data)
			}

			b, err := n.At(paths.Current().Child("b"))
			if err != nil {
				return fmt.Errorf("scope b: %w", err)
			}

			data, err := b.Decode[any](ctx)
			if err != nil {
				return err
			}

			seen = append(seen, data)

			return nil
		})

		inner := map[string]any{"c": uint64(1)}
		plain := map[string]any{"b": inner}
		want := yaml.MapSlice{{Key: "b", Value: yaml.MapSlice{{Key: "c", Value: uint64(1)}}}}

		got, err := dd.Decode[any](t.Context(), ordered, niceyaml.WithValidator(record))
		require.NoError(t, err)
		assert.Equal(t, want, got)
		assert.Equal(t, []any{plain, plain, inner}, seen)

		seen = nil

		err = dd.Validate(t.Context(), record)
		require.NoError(t, err)
		assert.Equal(t, []any{plain, plain, inner}, seen)

		require.Len(t, nodes, 4)

		for _, n := range nodes {
			assert.Same(t, dd, n)
		}
	})

	t.Run("runs in order and stops at the first failure", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: test\n")

		var order []string

		record := func(name string, err error) niceyaml.Validator {
			return niceyaml.ValidatorFunc(func(_ context.Context, _ *niceyaml.Node) error {
				order = append(order, name)

				return err
			})
		}

		_, err := dd.Decode[plainConfig](t.Context(),
			niceyaml.WithValidator(record("first", nil)),
			niceyaml.WithValidator(record("second", errDocumentRejected)),
			niceyaml.WithValidator(record("third", nil)),
		)
		require.ErrorIs(t, err, errDocumentRejected)
		assert.Equal(t, []string{"first", "second"}, order)
	})

	t.Run("a typed nil pointer is no failure and the next validator runs", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: test\n")

		result, err := dd.Decode[plainConfig](t.Context(),
			niceyaml.WithValidator(typedNilValidator()),
			niceyaml.WithValidator(rejectingValidator(errDocumentRejected)),
		)
		require.ErrorIs(t, err, errDocumentRejected)
		assert.Equal(t, plainConfig{}, result)
	})

	t.Run("skips a nil validator", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			validator niceyaml.Validator
		}{
			"nil interface": {validator: nil},
			"nil pointer":   {validator: (*fieldValidator)(nil)},
			"nil func":      {validator: niceyaml.ValidatorFunc(nil)},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, "name: test\nvalue: 42\n")

				result, err := dd.Decode[plainConfig](t.Context(), niceyaml.WithValidator(tc.validator))
				require.NoError(t, err)
				assert.Equal(t, plainConfig{Name: "test", Value: 42}, result)

				require.NoError(t, dd.Validate(t.Context(), tc.validator))
			})
		}
	})

	t.Run("a scoped decode passes the scope", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: test\nvalue: 42\n")
		valuePath := paths.Doc().Child("value")
		scoped := yamltest.At(t, dd, valuePath)

		var got *niceyaml.Node

		capture := niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
			got = n

			return nil
		})

		value, err := scoped.Decode[int](t.Context(), niceyaml.WithValidator(capture))
		require.NoError(t, err)
		assert.Same(t, scoped, got)
		assert.Equal(t, valuePath, got.Path())
		assert.Equal(t, 42, value)
	})
}

// passingValidator returns a [niceyaml.Validator] that accepts every
// document.
func passingValidator() niceyaml.Validator {
	return niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
		return nil
	})
}

// rejectingValidator returns a [niceyaml.Validator] that rejects every
// document with err.
func rejectingValidator(err error) niceyaml.Validator {
	return niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
		return err
	})
}

// recordingValidator returns a [niceyaml.Validator] that accepts every
// document and appends name to order on each run.
func recordingValidator(order *[]string, name string) niceyaml.Validator {
	return niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
		*order = append(*order, name)

		return nil
	})
}

// typedNilValidator returns a [niceyaml.Validator] that accepts every
// document by returning a nil [*niceyaml.Error] pointer.
func typedNilValidator() niceyaml.Validator {
	return niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
		var e *niceyaml.Error

		return e
	})
}

// hoursConfig is a value decoded from one node of a document, and its
// Validate names a path from that node.
type hoursConfig struct {
	Open  string `yaml:"open"`
	Close string `yaml:"close"`
}

func (h hoursConfig) Validate() error {
	if h.Open == "" {
		return niceyaml.NewError("open is required", niceyaml.AtPath(paths.Current().Child("open").Key()))
	}

	if h.Open >= h.Close {
		return niceyaml.NewError("open must be before close", niceyaml.AtPath(paths.Current().Child("open")))
	}

	return nil
}

// valuesOfTokens returns the value of each token of tks, or nil for no
// tokens.
func valuesOfTokens(tks token.Tokens) []string {
	var values []string

	for _, tk := range tks {
		values = append(values, tk.Value)
	}

	return values
}

// requireNodeExtent checks the span and the token values of the Node that
// path selects in doc.
func requireNodeExtent(t *testing.T, doc *niceyaml.Node, path paths.Path, span position.Span, values []string) {
	t.Helper()

	scoped := yamltest.At(t, doc, path)

	assert.Equal(t, span, scoped.Span())
	assert.Equal(t, values, valuesOfTokens(scoped.Tokens()))
}

func TestDocument_Decode_ValidatorDecodesWithoutHooks(t *testing.T) {
	t.Parallel()

	t.Run("a validator that decodes the document runs once", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: test\nvalue: 42\n")

		var runs int

		// A validator shaped like schema.Schema.Check decodes the
		// document to inspect it. Its own decode must carry no hooks, or
		// the validator would run itself again on every decode it performs.
		inspect := niceyaml.ValidatorFunc(func(ctx context.Context, doc *niceyaml.Node) error {
			runs++

			_, err := doc.Decode[any](ctx)

			return err
		})

		result, err := dd.Decode[plainConfig](t.Context(), niceyaml.WithValidator(inspect))
		require.NoError(t, err)
		assert.Equal(t, 1, runs)
		assert.Equal(t, "test", result.Name)
		assert.Equal(t, 42, result.Value)
	})

	t.Run("a validator that reads one field resolves the path once", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: test\nvalue: 42\n")

		readName := niceyaml.ValidatorFunc(func(ctx context.Context, doc *niceyaml.Node) error {
			scoped := yamltest.At(t, doc, paths.Current().Child("name"))

			name, err := scoped.Decode[string](ctx)
			if err != nil {
				return err
			}

			if name != "test" {
				return errNameRequired
			}

			return nil
		})

		_, err := dd.Decode[plainConfig](t.Context(), niceyaml.WithValidator(readName))
		require.NoError(t, err)
	})
}

// decodeSettings holds the source options and the decode options of one
// case of [validatorSettings].
type decodeSettings struct {
	source []niceyaml.SourceOption
	opts   []niceyaml.DecodeOption
}

// validatorSettings returns the settings under which a test repeats a
// decode that runs a validator, to show that the binding of the
// validator's error does not depend on them.
func validatorSettings() map[string]decodeSettings {
	return map[string]decodeSettings{
		"no options": {},
		"references": {
			source: []niceyaml.SourceOption{
				niceyaml.WithReferences(niceyaml.NewSourceFromString("base: &x 1\n")),
			},
		},
		"ordered maps": {opts: []niceyaml.DecodeOption{niceyaml.WithYAMLOrderedMaps(true)}},
	}
}

func TestDocument_Decode_ValidatorErrorBoundToReceiver(t *testing.T) {
	t.Parallel()

	fail := rejectingValidator(errDocumentRejected)

	// A validator gets the receiver itself, whatever options the source
	// and the decode hold. A [niceyaml.ValidatorFunc] binds the error of
	// its function through it, and the decode binds the error a
	// validator of another type leaves unbound.
	validators := map[string]niceyaml.Validator{
		"returns an unbound error":             &fieldValidator{err: errDocumentRejected},
		"returns an unbound error from a func": fail,
		"binds its own error": niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
			return n.Bind(errDocumentRejected)
		}),
		"binds at the root it reaches": niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
			return n.Document().Bind(errDocumentRejected)
		}),
		"fails a decode of its own": niceyaml.ValidatorFunc(decodeAs[any](
			niceyaml.WithYAMLOrderedMaps(true),
			niceyaml.WithValidator(fail),
		)),
	}

	for name, tc := range validatorSettings() {
		for validatorName, validator := range validators {
			t.Run(name+"/"+validatorName, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, "a: 1\n", tc.source...)

				opts := append(slices.Clone(tc.opts), niceyaml.WithValidator(validator))

				_, decodeErr := dd.Decode[any](t.Context(), opts...)
				validateErr := dd.Validate(t.Context(), validator)
				decodeIntoErr := dd.DecodeInto(t.Context(), new(any), niceyaml.DecodeOptions(opts...))

				for _, err := range []error{decodeErr, validateErr, decodeIntoErr} {
					bound := yamltest.RequireSourceError(t, err)
					require.ErrorIs(t, err, errDocumentRejected)
					assert.Same(t, dd, yamltest.BoundNode(t, bound))
					assert.Same(t, dd, yamltest.BoundDocument(t, bound))
				}
			})
		}
	}
}

func TestDocument_Decode_ScopedValidatorErrorDocument(t *testing.T) {
	t.Parallel()

	// The validator scopes the Node it gets to the second item and binds
	// its error there, so the scoped Node holds the error, and the root
	// of its document is the receiver.
	itemPath := paths.Doc().Child("items").Index(1)

	scopes := map[string]func(n *niceyaml.Node) (*niceyaml.Node, error){
		"at": func(n *niceyaml.Node) (*niceyaml.Node, error) {
			return n.At(itemPath)
		},
		"nodes": func(n *niceyaml.Node) (*niceyaml.Node, error) {
			items, err := n.Nodes(paths.Current().Child("items").IndexAll())
			if err != nil {
				return nil, fmt.Errorf("items: %w", err)
			}

			if len(items) != 2 {
				return nil, fmt.Errorf("got %d items", len(items))
			}

			return items[1], nil
		},
	}

	for name, tc := range validatorSettings() {
		for scopeName, scope := range scopes {
			t.Run(name+"/"+scopeName, func(t *testing.T) {
				t.Parallel()

				scoping := niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
					scoped, err := scope(n)
					if err != nil {
						return fmt.Errorf("scope: %w", err)
					}

					return scoped.Bind(niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("x"))))
				})

				dd := yamltest.FirstDocument(t, stringtest.LinesLF(
					"x: 0",
					"items:",
					"  - x: 1",
					"  - x: 2",
				), tc.source...)

				opts := append(slices.Clone(tc.opts), niceyaml.WithValidator(scoping))

				_, err := dd.Decode[any](t.Context(), opts...)
				require.EqualError(t, err, "4:8: $.items[1].x: bad")

				bound := yamltest.RequireSourceError(t, err)
				assert.Equal(t, itemPath, yamltest.BoundNode(t, bound).Path())
				assert.Same(t, dd, yamltest.BoundDocument(t, bound))
			})
		}
	}
}

func TestDocument_Decode_ValidatorAmbiguousErrorBoundToReceiver(t *testing.T) {
	t.Parallel()

	// The validator decodes the Node it gets into a map whose NaN keys
	// give its errors no position. The errors bind to the Node the
	// decode runs on, whatever options the source and the decode hold.
	decoding := niceyaml.ValidatorFunc(decodeAs[map[float64]signed]())

	for name, tc := range validatorSettings() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dd := yamltest.FirstDocument(t, "NaN: {n: -1}\n.nan: {n: -2}\n", tc.source...)

			opts := append(slices.Clone(tc.opts), niceyaml.WithValidator(decoding))

			_, err := dd.Decode[any](t.Context(), opts...)
			require.Error(t, err)

			ambiguous := 0

			for _, bound := range yamltest.BoundIn(err) {
				assert.Same(t, dd, yamltest.BoundNode(t, bound))

				if errors.Is(bound.Unresolved(), niceyaml.ErrAmbiguousPath) {
					ambiguous++
				}
			}

			assert.Equal(t, 2, ambiguous)
		})
	}
}

func ExampleDecodeOptions() {
	ctx := context.Background()

	type manifest struct {
		Kind string `yaml:"kind"`
		Name string `yaml:"name"`
	}

	manifests := schema.MustCompile([]byte(`{"type": "object", "required": ["kind", "name"]}`))

	settings := niceyaml.DecodeOptions(niceyaml.WithDisallowUnknownFields(true))
	strict := niceyaml.DecodeOptions(niceyaml.WithValidator(manifests), settings)

	docs := niceyaml.NewSourceFromString(
		stringtest.LinesLF(
			"kind: Service",
			"name: web",
			"---",
			"kind: Deployment",
			"name: web",
			"replicas: 3",
		),
		niceyaml.WithName("app.yaml"),
	).Documents()

	// Each whole document validates against the schema and decodes with
	// the settings.
	for _, doc := range docs {
		m, err := doc.Decode[manifest](ctx, strict)
		if err != nil {
			fmt.Println(err)

			continue
		}

		fmt.Println(m.Kind, m.Name)
	}

	kindPath := paths.Current().Child("kind")

	// The validator checks the node the call decodes, and the schema of
	// the document does not describe the value at $.kind.
	_, err := docs[0].DecodeAt[string](ctx, kindPath, strict)
	fmt.Println(err)

	// A decode of one value takes the settings without that schema.
	kind, err := docs[0].DecodeAt[string](ctx, kindPath, settings)
	fmt.Println(kind, err)

	// Output:
	// Service web
	// app.yaml:6:1: $.replicas~: unknown field "replicas"
	// app.yaml:1:7: $.kind: expected "object", got "string"
	// Service <nil>
}

func TestDecodeOptions(t *testing.T) {
	t.Parallel()

	type strictConfig struct {
		Name string `yaml:"name"`
	}

	input := stringtest.Input(`
		name: test
		extra: field
		---
		name: other
		extra: field
	`)

	record := func(order *[]string, name string) niceyaml.DecodeOption {
		return niceyaml.WithValidator(recordingValidator(order, name))
	}

	strict := niceyaml.WithDisallowUnknownFields(true)
	lax := niceyaml.WithDisallowUnknownFields(false)

	dd := yamltest.FirstDocument(t, input)

	t.Run("decodes every document with the options stated once", func(t *testing.T) {
		t.Parallel()

		var order []string

		opts := niceyaml.DecodeOptions(record(&order, "schema"), strict)

		docs := niceyaml.NewSourceFromString(input).Documents()
		require.Len(t, docs, 2)

		for _, dd := range docs {
			_, err := dd.Decode[strictConfig](t.Context(), opts)
			require.ErrorContains(t, err, `unknown field "extra"`)

			bound := yamltest.RequireSourceError(t, err)
			assert.Same(t, dd, yamltest.BoundNode(t, bound))
		}

		assert.Equal(t, []string{"schema", "schema"}, order)
	})

	t.Run("without options decodes as a call without options does", func(t *testing.T) {
		t.Parallel()

		got, err := dd.Decode[validatorConfig](t.Context(), niceyaml.DecodeOptions())
		require.NoError(t, err)
		assert.Equal(t, "test", got.Name)
		assert.True(t, got.validated, "the value did not validate itself")
	})

	t.Run("applies its options in order among the options of the call", func(t *testing.T) {
		t.Parallel()

		var order []string

		opts := niceyaml.DecodeOptions(
			record(&order, "first"),
			niceyaml.DecodeOptions(record(&order, "nested")),
			record(&order, "last"),
		)

		_, err := dd.Decode[strictConfig](t.Context(), record(&order, "before"), opts, record(&order, "after"))
		require.NoError(t, err)
		assert.Equal(t, []string{"before", "first", "nested", "last", "after"}, order)
	})

	t.Run("stops at the first validator that fails", func(t *testing.T) {
		t.Parallel()

		var order []string

		opts := niceyaml.DecodeOptions(
			record(&order, "first"),
			niceyaml.WithValidator(rejectingValidator(errNameRequired)),
			record(&order, "unreached"),
		)

		got, err := dd.Decode[strictConfig](t.Context(), opts, record(&order, "after"))
		require.ErrorIs(t, err, errNameRequired)
		assert.Equal(t, strictConfig{}, got)
		assert.Equal(t, []string{"first"}, order)
	})

	t.Run("the last WithDisallowUnknownFields of the call sets the value", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			opts []niceyaml.DecodeOption
			err  string
		}{
			"the option alone": {
				opts: []niceyaml.DecodeOption{niceyaml.DecodeOptions(strict)},
				err:  `2:1: $.extra~: unknown field "extra"`,
			},
			"an option after it": {opts: []niceyaml.DecodeOption{niceyaml.DecodeOptions(strict), lax}},
			"an option before it": {
				opts: []niceyaml.DecodeOption{lax, niceyaml.DecodeOptions(strict)},
				err:  `2:1: $.extra~: unknown field "extra"`,
			},
			"two options inside it": {opts: []niceyaml.DecodeOption{niceyaml.DecodeOptions(strict, lax)}},
			"a nested value after an option": {
				opts: []niceyaml.DecodeOption{niceyaml.DecodeOptions(lax, niceyaml.DecodeOptions(strict))},
				err:  `2:1: $.extra~: unknown field "extra"`,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				got, err := dd.Decode[strictConfig](t.Context(), tc.opts...)
				if tc.err != "" {
					require.EqualError(t, err, tc.err)

					return
				}

				require.NoError(t, err)
				assert.Equal(t, strictConfig{Name: "test"}, got)
			})
		}
	})

	t.Run("the last WithSelfValidation of the call sets the value", func(t *testing.T) {
		t.Parallel()

		off := niceyaml.DecodeOptions(niceyaml.WithSelfValidation(false))

		got, err := dd.Decode[failingValidator](t.Context(), off)
		require.NoError(t, err)
		assert.Equal(t, "test", got.Name)

		_, err = dd.Decode[failingValidator](t.Context(), off, niceyaml.WithSelfValidation(true))
		require.Error(t, err, "the value validates itself")

		_, err = dd.Decode[failingValidator](t.Context(), niceyaml.WithSelfValidation(true), off)
		require.NoError(t, err)
	})

	t.Run("the same value twice runs its validators twice", func(t *testing.T) {
		t.Parallel()

		var order []string

		opts := niceyaml.DecodeOptions(record(&order, "schema"))

		_, err := dd.Decode[strictConfig](t.Context(), opts, opts) //nolint:gocritic // The call gets the value twice.
		require.NoError(t, err)
		assert.Equal(t, []string{"schema", "schema"}, order)
	})

	t.Run("a value built from another leaves it as it was", func(t *testing.T) {
		t.Parallel()

		var order []string

		base := niceyaml.DecodeOptions(record(&order, "base"), strict)
		derived := niceyaml.DecodeOptions(base, record(&order, "derived"), lax)

		got, err := dd.Decode[strictConfig](t.Context(), derived)
		require.NoError(t, err)
		assert.Equal(t, "test", got.Name)
		assert.Equal(t, []string{"base", "derived"}, order)

		order = nil

		_, err = dd.Decode[strictConfig](t.Context(), base)
		require.Error(t, err, "the first value keeps its strictness")
		assert.Equal(t, []string{"base"}, order, "the first value keeps its validators")
	})

	t.Run("a later custom unmarshaler replaces the one before it", func(t *testing.T) {
		t.Parallel()

		type marker string

		type holder struct {
			M   any    `yaml:"m"`
			Tag marker `yaml:"tag"`
		}

		// The last WithCustomUnmarshaler given for a type decodes it, so
		// the marker a decode yields names the option that came last.
		setMarker := func(value marker) niceyaml.DecodeOption {
			return niceyaml.WithCustomUnmarshaler(func(_ context.Context, m *marker, _ func(any) error) error {
				*m = value

				return nil
			})
		}

		base := niceyaml.DecodeOptions(niceyaml.WithYAMLOrderedMaps(true), setMarker("base"))
		derived := niceyaml.DecodeOptions(base, setMarker("derived"))

		dd := yamltest.FirstDocument(t, "m: {b: 1, a: 2}\ntag: x\n")

		got, err := dd.Decode[holder](t.Context(), derived)
		require.NoError(t, err)
		assert.Equal(t, yaml.MapSlice{
			{Key: "b", Value: uint64(1)},
			{Key: "a", Value: uint64(2)},
		}, got.M, "the options of the first value still apply")
		assert.Equal(t, marker("derived"), got.Tag, "the later options apply after the ones of the first value")

		got, err = dd.Decode[holder](t.Context(), base)
		require.NoError(t, err)
		assert.Equal(t, marker("base"), got.Tag, "the first value keeps its options")
	})

	t.Run("keeps its options when the caller edits the slice", func(t *testing.T) {
		t.Parallel()

		var order []string

		given := []niceyaml.DecodeOption{record(&order, "given")}
		opts := niceyaml.DecodeOptions(given...)
		given[0] = record(&order, "edited")

		_, err := dd.Decode[strictConfig](t.Context(), opts)
		require.NoError(t, err)
		assert.Equal(t, []string{"given"}, order)
	})

	t.Run("a nil option panics in the call that applies it", func(t *testing.T) {
		t.Parallel()

		var opts niceyaml.DecodeOption

		require.NotPanics(t, func() {
			opts = niceyaml.DecodeOptions(nil)
		})

		assert.Panics(t, func() {
			_, _ = dd.Decode[strictConfig](t.Context(), opts) //nolint:errcheck // The call panics before it returns.
		})
		assert.Panics(t, func() {
			_, _ = dd.Decode[strictConfig](t.Context(), nil) //nolint:errcheck // The call panics before it returns.
		}, "a nil option given to the call panics too")
	})
}

// TestDecodeOptions_EntryPoints gives one [niceyaml.DecodeOptions] value
// to every method that takes a [niceyaml.DecodeOption], and requires what
// the same options written out in the call give.
func TestDecodeOptions_EntryPoints(t *testing.T) {
	t.Parallel()

	input := stringtest.Input(`
		by_grade:
		  high: {url: http://h}
		extra: 1
	`)

	byGrade := paths.Current().Child("by_grade")
	unknown := `app.yaml:3:1: $.extra~: unknown field "extra"`
	notHTTP := `app.yaml:2:15: $.by_grade.high.url: url "ftp://h" is not http`

	// The value a layer changed after the decode, which the walk rejects.
	changed := func() *gradedConfig {
		return &gradedConfig{ByGrade: map[grade]upstream{gradeHigh: {URL: "ftp://h"}}}
	}

	// Each option shows that it reached the call. A grade decodes only
	// under gradeNames, and the walk binds at the key of the document
	// only under it. The unknown key fails only a strict decode. The
	// validator counts its runs, and the walk runs with the option that
	// turns it off for a decode.
	tcs := map[string]struct {
		// Calls the method on src, or on its document, with opts.
		call func(t *testing.T, src *niceyaml.Source, opts ...niceyaml.DecodeOption) error
		err  string
		// How many times the validator runs.
		runs int
	}{
		"Node.Decode": {
			call: func(t *testing.T, src *niceyaml.Source, opts ...niceyaml.DecodeOption) error {
				t.Helper()

				_, err := yamltest.Document(t, src).Decode[gradedConfig](t.Context(), opts...)

				return err
			},
			err:  unknown,
			runs: 1,
		},
		"Node.DecodeInto": {
			call: func(t *testing.T, src *niceyaml.Source, opts ...niceyaml.DecodeOption) error {
				t.Helper()

				return yamltest.Document(t, src).DecodeInto(t.Context(), new(gradedConfig), opts...)
			},
			err:  unknown,
			runs: 1,
		},
		"Node.DecodeAt": {
			call: func(t *testing.T, src *niceyaml.Source, opts ...niceyaml.DecodeOption) error {
				t.Helper()

				got, err := yamltest.Document(t, src).DecodeAt[map[grade]upstream](t.Context(), byGrade, opts...)
				assert.Equal(t, map[grade]upstream{gradeHigh: {URL: "http://h"}}, got)

				return err
			},
			runs: 1,
		},
		"Node.DecodeIfPresent": {
			call: func(t *testing.T, src *niceyaml.Source, opts ...niceyaml.DecodeOption) error {
				t.Helper()

				var got map[grade]upstream

				present, err := yamltest.Document(t, src).DecodeIfPresent(t.Context(), byGrade, &got, opts...)
				assert.True(t, present)
				assert.Equal(t, map[grade]upstream{gradeHigh: {URL: "http://h"}}, got)

				return err //nolint:wrapcheck // The test inspects the error of the call.
			},
			runs: 1,
		},
		"Node.SelfValidate": {
			call: func(t *testing.T, src *niceyaml.Source, opts ...niceyaml.DecodeOption) error {
				t.Helper()

				return yamltest.Document(t, src).SelfValidate(t.Context(), changed(), opts...)
			},
			err: notHTTP,
		},
		"Source.Decode": {
			call: func(t *testing.T, src *niceyaml.Source, opts ...niceyaml.DecodeOption) error {
				t.Helper()

				_, err := src.Decode[gradedConfig](t.Context(), opts...)

				return err
			},
			err:  unknown,
			runs: 1,
		},
		"Source.DecodeInto": {
			call: func(t *testing.T, src *niceyaml.Source, opts ...niceyaml.DecodeOption) error {
				t.Helper()

				return src.DecodeInto(t.Context(), new(gradedConfig), opts...)
			},
			err:  unknown,
			runs: 1,
		},
		"Source.SelfValidate": {
			call: func(t *testing.T, src *niceyaml.Source, opts ...niceyaml.DecodeOption) error {
				t.Helper()

				return src.SelfValidate(t.Context(), changed(), opts...)
			},
			err: notHTTP,
		},
		"merged Source.Decode": {
			call: func(t *testing.T, src *niceyaml.Source, opts ...niceyaml.DecodeOption) error {
				t.Helper()

				_, err := yamltest.Layers(t, yamltest.Document(t, src)).Decode[gradedConfig](t.Context(), opts...)

				return err
			},
			err:  unknown,
			runs: 1,
		},
		"merged Source.DecodeInto": {
			call: func(t *testing.T, src *niceyaml.Source, opts ...niceyaml.DecodeOption) error {
				t.Helper()

				return yamltest.Layers(t, yamltest.Document(t, src)).
					DecodeInto(t.Context(), new(gradedConfig), opts...)
			},
			err:  unknown,
			runs: 1,
		},
		"merged Source.SelfValidate": {
			call: func(t *testing.T, src *niceyaml.Source, opts ...niceyaml.DecodeOption) error {
				t.Helper()

				return yamltest.Layers(t, yamltest.Document(t, src)).SelfValidate(t.Context(), changed(), opts...)
			},
			err: notHTTP,
		},
	}

	// How the call gets the options.
	forms := map[string]func(opts []niceyaml.DecodeOption) []niceyaml.DecodeOption{
		"written out": func(opts []niceyaml.DecodeOption) []niceyaml.DecodeOption {
			return opts
		},
		"as one value": func(opts []niceyaml.DecodeOption) []niceyaml.DecodeOption {
			return []niceyaml.DecodeOption{niceyaml.DecodeOptions(opts...)}
		},
	}

	for name, tc := range tcs {
		for formName, form := range forms {
			t.Run(name+"/"+formName, func(t *testing.T) {
				t.Parallel()

				runs := 0

				opts := form([]niceyaml.DecodeOption{
					gradeNames,
					niceyaml.WithValidator(niceyaml.ValidatorFunc(func(context.Context, *niceyaml.Node) error {
						runs++

						return nil
					})),
					niceyaml.WithDisallowUnknownFields(true),
					niceyaml.WithSelfValidation(false),
				})

				err := tc.call(t, niceyaml.NewSourceFromString(input, niceyaml.WithName("app.yaml")), opts...)
				if tc.err == "" {
					require.NoError(t, err)
				} else {
					require.EqualError(t, err, tc.err)
				}

				assert.Equal(t, tc.runs, runs)
			})
		}
	}
}

// nodePaths returns the path of each Node of nodes as a string, or nil for
// no nodes.
func nodePaths(nodes []*niceyaml.Node) []string {
	var got []string

	for _, n := range nodes {
		got = append(got, n.Path().String())
	}

	return got
}

// rejectingUnmarshaler decodes itself and reports errUnmarshal, so the
// error the decoder returns is the caller's own rather than go-yaml's.
type rejectingUnmarshaler struct{}

func (*rejectingUnmarshaler) UnmarshalYAML([]byte) error {
	return errUnmarshal
}

// panickingUnmarshaler panics with errUnmarshal when it decodes itself,
// so the decode meets a panic below the node it reads.
type panickingUnmarshaler struct{}

func (*panickingUnmarshaler) UnmarshalYAML([]byte) error {
	panic(errUnmarshal)
}

// wrappingUnmarshaler decodes itself as an int through the decoder and
// wraps the error the decoder returns in errUnmarshal, so the error
// carries go-yaml's error inside the caller's own.
type wrappingUnmarshaler int

func (u *wrappingUnmarshaler) UnmarshalYAML(unmarshal func(any) error) error {
	var n int

	err := unmarshal(&n)
	if err != nil {
		return fmt.Errorf("%w: %w", errUnmarshal, err)
	}

	*u = wrappingUnmarshaler(n)

	return nil
}

// reparsingUnmarshaler decodes itself by parsing the bytes it gets again,
// so the go-yaml error it returns carries a token of that parse rather
// than one of the source.
type reparsingUnmarshaler struct {
	N int
}

func (u *reparsingUnmarshaler) UnmarshalYAML(data []byte) error {
	var raw struct{ N int }

	err := yaml.Unmarshal(data, &raw)
	if err != nil {
		return fmt.Errorf("custom context: %w", err)
	}

	u.N = raw.N

	return nil
}

// bareReparsingUnmarshaler decodes itself by parsing the bytes it gets
// again and returns the go-yaml error of that parse unwrapped. The parse
// starts at line 1, so the token of its error can share its type, value,
// and position with a token near the top of the source.
type bareReparsingUnmarshaler struct {
	Name []int
}

func (u *bareReparsingUnmarshaler) UnmarshalYAML(data []byte) error {
	var raw struct{ Name []int }

	err := yaml.Unmarshal(data, &raw)
	if err != nil {
		return err //nolint:wrapcheck // The test needs the go-yaml error as it is.
	}

	u.Name = raw.Name

	return nil
}

// cancelAwareUnmarshaler decodes itself by wrapping the error of the
// context it gets, so a canceled context fails the decode. The go-yaml
// decoder never checks the context itself and only passes it to
// unmarshalers like this one.
type cancelAwareUnmarshaler struct{}

func (*cancelAwareUnmarshaler) UnmarshalYAML(ctx context.Context, _ []byte) error {
	err := ctx.Err()
	if err != nil {
		return fmt.Errorf("decode stopped: %w", err)
	}

	return nil
}

// deadlineUnmarshaler decodes itself by wrapping the error of a deadline
// of its own, which the context of the decode has not reached.
type deadlineUnmarshaler struct{}

func (*deadlineUnmarshaler) UnmarshalYAML([]byte) error {
	return fmt.Errorf("lookup stopped: %w", context.DeadlineExceeded)
}

// selfRejecting decodes as a struct and reports errUnmarshal when it
// validates itself.
type selfRejecting struct {
	Value int `yaml:"value"`
}

func (selfRejecting) Validate() error {
	return errUnmarshal
}

func TestErrDecode(t *testing.T) {
	t.Parallel()

	rejected := map[string]struct {
		input  string
		decode func(ctx context.Context, dd *niceyaml.Node) error
	}{
		"type mismatch": {
			input:  "value: abc",
			decode: decodeAs[struct{ Value int }](),
		},
		// The validator passes, and the decode after it fails.
		"type mismatch after a validator": {
			input:  "value: abc",
			decode: decodeAs[struct{ Value int }](niceyaml.WithValidator(passingValidator())),
		},
		"overflow": {
			input:  "value: 300",
			decode: decodeAs[struct{ Value int8 }](),
		},
		"unknown field": {
			input:  "other: 1",
			decode: decodeAs[struct{ Value int }](niceyaml.WithDisallowUnknownFields(true)),
		},
		"mapping into scalar": {
			input: stringtest.JoinLF(
				"value:",
				"  a: 1",
			),
			decode: decodeAs[struct{ Value string }](),
		},
		"DecodeInto": {
			input: "value: abc",
			decode: func(ctx context.Context, dd *niceyaml.Node) error {
				var v struct{ Value int }

				return dd.DecodeInto(ctx, &v)
			},
		},
		// The parser makes a null token for each value the document leaves
		// out, which the lexer never saw.
		"mapping value with only a tag": {
			input: "a: null\nc: !!int\n",
			decode: func(ctx context.Context, dd *niceyaml.Node) error {
				_, err := dd.DecodeAt[struct{ X int }](ctx, paths.Current().Child("c"))

				return err
			},
		},
		"sequence item with only a tag": {
			input: stringtest.LinesLF("items:", "  - !!int"),
			decode: func(ctx context.Context, dd *niceyaml.Node) error {
				items, err := dd.Nodes(paths.Current().Child("items").IndexAll())
				if err != nil {
					return err //nolint:wrapcheck // The test inspects the error as it is.
				}

				_, err = items[0].Decode[struct{ X int }](ctx)

				return err
			},
		},
		"tag over no value": {
			input:  "a: !!bool\n",
			decode: decodeAs[map[string]any](),
		},
	}

	for name, tc := range rejected {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dd := yamltest.FirstDocument(t, tc.input)

			err := tc.decode(t.Context(), dd)
			require.ErrorIs(t, err, niceyaml.ErrDecode)

			require.ErrorAs(t, err, new(*niceyaml.Error))
			require.ErrorAs(t, err, new(*niceyaml.SourceError), "the rejection is not bound to the source")

			_, ok := errors.AsType[yaml.Error](err)
			assert.True(t, ok, "errors.As no longer finds the go-yaml error")
			assert.NotContains(t, err.Error(), "\n", "the go-yaml excerpt leaked into the message")
		})
	}

	t.Run("rejection the decoder reports without a token", func(t *testing.T) {
		t.Parallel()

		// The decoder reports these with no token of the source, so the
		// decode binds them where the document causes them.
		refs := niceyaml.WithReferences(niceyaml.NewSourceFromString("base: &base {x: 1}\n"))

		noAnchor := stringtest.LinesLF(
			"a:",
			"  <<: *nope",
			"  b: 1",
		)

		tcs := map[string]struct {
			input  string
			path   paths.Path
			source []niceyaml.SourceOption
			line   int
			msg    string
		}{
			"merge alias the references leave unbound": {
				input:  noAnchor,
				path:   paths.Current(),
				source: []niceyaml.SourceOption{refs},
				line:   1,
				msg:    "cannot find anchor by alias name nope",
			},
			"merge alias after one the references bind": {
				input: stringtest.LinesLF(
					"a:",
					"  <<: *base",
					"  b:",
					"    <<: *nope",
				),
				path:   paths.Current(),
				source: []niceyaml.SourceOption{refs},
				line:   3,
				msg:    "cannot find anchor by alias name nope",
			},
			"merge of the mapping that holds it": {
				input: stringtest.LinesLF(
					"a: &x",
					"  <<: *x",
					"  b: 1",
				),
				path: paths.Current(),
				line: 1,
				msg:  "cannot find anchor by alias name x",
			},
			"merge alias with no anchor": {
				input: noAnchor,
				path:  paths.Current(),
				line:  1,
				msg:   "cannot find anchor by alias name nope",
			},
			"merge alias before its anchor": {
				input: stringtest.LinesLF(
					"a:",
					"  <<: *y",
					"  b: 1",
					"c: &y",
					"  d: 1",
				),
				path: paths.Current(),
				line: 1,
				msg:  "cannot find anchor by alias name y",
			},
			"merge alias in a list of sources": {
				input: "a: {<<: [{b: 1}, *nope]}\n",
				path:  paths.Current(),
				line:  0,
				msg:   "cannot find anchor by alias name nope",
			},
			"merge alias with no anchor in a scoped decode": {
				input: noAnchor,
				path:  paths.Current().Child("a"),
				line:  1,
				msg:   "cannot find anchor by alias name nope",
			},
			"merge alias in an anchor the node merges": {
				input: "b: &b {<<: *nope}\nc: {<<: *b}\n",
				path:  paths.Current().Child("c"),
				line:  0,
				msg:   "cannot find anchor by alias name nope",
			},
			"merge of an anchor that holds the node": {
				input: stringtest.LinesLF(
					"a: &x",
					"  m:",
					"    <<: *x",
				),
				path: paths.Current().Child("a", "m"),
				line: 2,
				msg:  "cannot find anchor by alias name x",
			},
			"merge of an anchor that holds the node after one of the same name": {
				input: stringtest.LinesLF(
					"a: &x {k: 1}",
					"b: &x",
					"  j: 2",
					"  <<: *x",
				),
				path: paths.Current().Child("b"),
				line: 3,
				msg:  "cannot find anchor by alias name x",
			},
			"depth limit": {
				input: "a: " + strings.Repeat("[", 10005) + strings.Repeat("]", 10005) + "\n",
				path:  paths.Current(),
				line:  0,
				msg:   "exceeded max depth",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input, tc.source...)

				_, err := yamltest.At(t, dd, tc.path).Decode[any](t.Context())
				require.ErrorIs(t, err, niceyaml.ErrDecode)
				assert.Contains(t, err.Error(), tc.msg)
				assert.NotContains(t, err.Error(), "\n", "the go-yaml excerpt leaked into the message")

				srcErr := yamltest.RequireSourceError(t, err)

				rng, ok := srcErr.Range()
				require.True(t, ok, "the rejection carries no location")
				assert.Equal(t, tc.line, rng.Start.Line)
				assert.Empty(t, yamltest.ProblemsOf(srcErr), "the rejection binds its own causes as children")
			})
		}
	})

	t.Run("rejection in a reference document", func(t *testing.T) {
		t.Parallel()

		// The decoder reports these at a token of the reference document,
		// so the decode binds them at the alias that reads it.
		type cfg struct {
			Item struct{ X int } `yaml:"item"`
		}

		ref := niceyaml.WithReferences(niceyaml.NewSourceFromString("base: &base {x: notint}\nother: &other 1\n"))

		// Decodes the node under the key outer.
		scoped := func(ctx context.Context, dd *niceyaml.Node) error {
			_, err := dd.DecodeAt[cfg](ctx, paths.Current().Child("outer"))

			return err
		}

		// A line of -1 means the rejection carries no location.
		tcs := map[string]struct {
			input  string
			decode func(ctx context.Context, dd *niceyaml.Node) error
			line   int
		}{
			"merge from a reference": {
				input: stringtest.LinesLF(
					"item:",
					"  <<: *base",
					"y: 1",
				),
				decode: decodeAs[cfg](),
				line:   1,
			},
			"alias to a reference": {
				input:  "a: 1\nitem: *base\n",
				decode: decodeAs[cfg](),
				line:   1,
			},
			"alias to a reference in a scoped decode": {
				input: stringtest.LinesLF(
					"a: 1",
					"outer:",
					"  item: *base",
				),
				decode: scoped,
				line:   2,
			},
			// The decode reads the reference through an anchor outside
			// the node, so it binds at the alias in that anchor.
			"alias to a reference through an anchor in a scoped decode": {
				input: stringtest.LinesLF(
					"m: &m {<<: *base}",
					"outer:",
					"  item: *m",
				),
				decode: scoped,
				line:   0,
			},
			// The token of the rejection does not tell which of the two
			// aliases led to it, so the rejection carries no location even
			// though cfg reads only *base.
			"aliases to two references": {
				input:  "a: *other\nitem: *base\n",
				decode: decodeAs[cfg](),
				line:   -1,
			},
			// The decoder reports a key of an inline map with no token,
			// which the decode cannot tell from a token of a reference
			// document, so it binds the rejection at the alias too.
			"key of an inline map beside an alias to a reference": {
				input: "a: *other\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					var v struct {
						M map[int]int `yaml:",inline"`
					}

					return dd.DecodeInto(ctx, &v)
				},
				line: 0,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input, ref)

				err := tc.decode(t.Context(), dd)
				require.ErrorIs(t, err, niceyaml.ErrDecode)
				assert.Contains(t, err.Error(), "expected integer, got string")
				assert.NotContains(t, err.Error(), "[1:", "go-yaml's position leaked into the message")
				assert.NotContains(t, err.Error(), "base: &base", "go-yaml's excerpt leaked into the message")

				_, ok := errors.AsType[yaml.Error](err)
				assert.True(t, ok, "errors.As no longer finds the go-yaml error")

				srcErr := yamltest.RequireSourceError(t, err)

				rng, ok := srcErr.Range()
				if tc.line < 0 {
					assert.False(t, ok, "the rejection binds at an alias that may not have read it")

					return
				}

				require.True(t, ok, "the rejection carries no location")
				assert.Equal(t, tc.line, rng.Start.Line)
			})
		}
	})

	t.Run("unmarshaler error matches and keeps its chain", func(t *testing.T) {
		t.Parallel()

		type item struct {
			When rejectingUnmarshaler `yaml:"when"`
			X    int                  `yaml:"x"`
		}

		tcs := map[string]struct {
			input  string
			source []niceyaml.SourceOption
			decode func(ctx context.Context, dd *niceyaml.Node) error
			// Whether errors.As finds a go-yaml error in the chain, which
			// it does when the unmarshaler wraps one.
			goYAML bool
		}{
			"top-level value": {
				input:  "value: 1",
				decode: decodeAs[rejectingUnmarshaler](),
			},
			// The document's resolver binds no anchor for the alias, which
			// the reference document defines for the decoder.
			"merge the references resolve": {
				input: stringtest.LinesLF(
					"item:",
					"  <<: *base",
					"  when: x",
				),
				source: []niceyaml.SourceOption{
					niceyaml.WithReferences(niceyaml.NewSourceFromString("base: &base {x: 1}\n")),
				},
				decode: decodeAs[map[string]item](),
			},
			"error that wraps the go-yaml error of a top-level value": {
				input:  "abc",
				decode: decodeAs[wrappingUnmarshaler](),
				goYAML: true,
			},
			"error that wraps the go-yaml error of a sequence element": {
				input:  "- 1\n- abc\n",
				decode: decodeAs[[]wrappingUnmarshaler](),
				goYAML: true,
			},
			"error that wraps the go-yaml error of a mapping value": {
				input:  "a: 1\nb: abc\n",
				decode: decodeAs[map[string]wrappingUnmarshaler](),
				goYAML: true,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input, tc.source...)

				err := tc.decode(t.Context(), dd)
				require.ErrorIs(t, err, errUnmarshal)
				require.ErrorIs(t, err, niceyaml.ErrDecode)

				_, ok := errors.AsType[yaml.Error](err)
				assert.Equal(t, tc.goYAML, ok)
			})
		}
	})

	t.Run("error without a token of the source matches with no location", func(t *testing.T) {
		t.Parallel()

		type inner struct {
			A int `yaml:"a"`
		}

		tcs := map[string]struct {
			input  string
			decode func(ctx context.Context, dd *niceyaml.Node) error
			msg    string
		}{
			// The error of the value's own parse carries a token of the
			// bytes it parsed, not of the source, so it comes back as the
			// value's own error rather than as a rejection at the wrong
			// line.
			"parse error of an unmarshaler": {
				input: stringtest.LinesLF(
					"a: 1",
					"b: x",
					"c: y",
					"z:",
					"  n: notanumber",
				),
				decode: decodeAs[struct{ Z reparsingUnmarshaler }](),
				msg:    "cannot unmarshal string into Go struct field .Z of type int",
			},
			// The item's own parse fails at "abc" on its line 1, where the
			// source holds the same token at the same position.
			"parse error of an unmarshaler at a token the source holds": {
				input: stringtest.LinesLF(
					"name: [abc]",
					"items:",
					"  - name: [abc]",
				),
				decode: decodeAs[struct {
					Name  []string
					Items []bareReparsingUnmarshaler
				}](),
				msg: "cannot unmarshal string into Go struct field .Items of type int",
			},
			// The decoder reports each of the other cases with no token to
			// bind it to, and none comes from a value that decodes itself,
			// so the decode finds no value to bind it at. A go-yaml release
			// that gives one a token gives its case a location.
			"duplicated struct field name": {
				input: "a: 1\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					var v struct {
						A int `yaml:"a"`
						B int `yaml:"a"`
					}

					return dd.DecodeInto(ctx, &v)
				},
				msg: "duplicated struct field name a",
			},
			"unexported inline embedded struct": {
				input: "a: 1\nm: 2\n",
				decode: decodeAs[struct {
					*inner `yaml:",inline"`

					M int `yaml:"m"`
				}](),
				msg: "cannot set embedded type as unexported field",
			},
			// The decoder builds the mapping of an inline field itself,
			// so the mapping and its keys carry no token.
			"key of an inline map": {
				input: "a: 1\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					var v struct {
						M map[int]int `yaml:",inline"`
					}

					return dd.DecodeInto(ctx, &v)
				},
				msg: "cannot unmarshal string into Go struct field .M of type int",
			},
			"inline field that cannot hold a mapping": {
				input: "a: 1\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					var v struct {
						N int `yaml:",inline"`
					}

					return dd.DecodeInto(ctx, &v)
				},
				msg: "cannot unmarshal map[string]interface {} into Go struct field .N of type int",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				err := tc.decode(t.Context(), dd)
				require.ErrorContains(t, err, tc.msg)
				require.ErrorIs(t, err, niceyaml.ErrDecode)

				srcErr := yamltest.RequireSourceError(t, err)

				_, ok := srcErr.Range()
				assert.False(t, ok, "the error took a location")
			})
		}
	})

	t.Run("value of an inline map matches at its token", func(t *testing.T) {
		t.Parallel()

		// The mapping the decoder builds for an inline field holds the
		// values of the source, so a value keeps its token though its
		// key has none.
		dd := yamltest.FirstDocument(t, "a: 1\nb: x\n")

		var v struct {
			M map[string]int `yaml:",inline"`
		}

		err := dd.DecodeInto(t.Context(), &v)
		require.EqualError(t, err, "2:4: $.b: expected integer, got string")
		require.ErrorIs(t, err, niceyaml.ErrDecode)

		srcErr := yamltest.RequireSourceError(t, err)

		rng, ok := srcErr.Range()
		require.True(t, ok, "the rejection carries no location")
		assert.Equal(t, position.New(1, 3), rng.Start)
	})

	t.Run("panic does not match", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "# note\nvalue: 1\n")

		_, err := dd.Decode[panickingUnmarshaler](t.Context())
		require.NotErrorIs(t, err, niceyaml.ErrDecode)
		require.EqualError(t, err, "2:1: decoder panicked: unmarshaler rejected the value")
		requirePanic(t, err)
	})

	t.Run("decode inside a validator matches", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "value: abc\n")

		decodes := niceyaml.ValidatorFunc(decodeAs[struct{ Value int }]())

		err := dd.Validate(t.Context(), decodes)
		require.ErrorIs(t, err, niceyaml.ErrDecode)

		_, err = dd.Decode[any](t.Context(), niceyaml.WithValidator(decodes))
		require.ErrorIs(t, err, niceyaml.ErrDecode)
	})

	t.Run("error of another kind does not match", func(t *testing.T) {
		t.Parallel()

		tooLow := niceyaml.NewError("too low", niceyaml.AtPath(paths.Current().Child("value")))

		tcs := map[string]struct {
			decode func(ctx context.Context, dd *niceyaml.Node) error
			is     error
			input  string
			// The message of the error, for a case that checks it.
			want string
		}{
			"validator error": {
				input:  "value: 1\n",
				decode: decodeAs[struct{ Value int }](niceyaml.WithValidator(rejectingValidator(tooLow))),
				is:     tooLow,
				want:   "1:8: $.value: too low",
			},
			"self validator error": {
				input:  "value: 1\n",
				decode: decodeAs[selfRejecting](),
				is:     errUnmarshal,
			},
			"excessive aliasing": {
				input:  yamltest.AliasLevels(10),
				decode: decodeAs[any](),
				is:     niceyaml.ErrExcessiveAliasing,
			},
			"decode target": {
				input: "value: 1",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					return dd.DecodeInto(ctx, nil)
				},
				is: niceyaml.ErrDecodeTarget,
			},
			"canceled context": {
				input: "value: 1",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					ctx, cancel := context.WithCancel(ctx)
					cancel()

					return decodeAs[cancelAwareUnmarshaler]()(ctx, dd)
				},
				is: context.Canceled,
			},
			// The context of the decode has not ended, and the value reports
			// the error of a deadline of its own.
			"context error of a value the decode locates": {
				input:  "value: 1\n",
				decode: decodeAs[struct{ Value deadlineUnmarshaler }](),
				is:     context.DeadlineExceeded,
				want:   "lookup stopped: context deadline exceeded",
			},
			"context error of the node the decode reads": {
				input:  "value: 1\n",
				decode: decodeAs[deadlineUnmarshaler](),
				is:     context.DeadlineExceeded,
				want:   "lookup stopped: context deadline exceeded",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				err := tc.decode(t.Context(), yamltest.FirstDocument(t, tc.input))
				require.ErrorIs(t, err, tc.is)
				require.NotErrorIs(t, err, niceyaml.ErrDecode)

				if tc.want != "" {
					require.EqualError(t, err, tc.want)
				}
			})
		}
	})

	t.Run("parse error does not match", func(t *testing.T) {
		t.Parallel()

		source := niceyaml.NewSourceFromString("a: [\n")

		_, err := source.File()
		require.ErrorIs(t, err, niceyaml.ErrSyntax)
		require.NotErrorIs(t, err, niceyaml.ErrDecode)

		docs := source.AllDocuments()
		require.Len(t, docs, 1)

		_, err = docs[0].Decode[any](t.Context())
		require.ErrorIs(t, err, niceyaml.ErrSyntax)
		require.NotErrorIs(t, err, niceyaml.ErrDecode)
	})

	t.Run("canceled context stops a decode that reads an anchor", func(t *testing.T) {
		t.Parallel()

		// The alias under b needs the anchor under a, which the decode
		// would otherwise skip and report as missing.
		dd := yamltest.FirstDocument(t, stringtest.LinesLF(
			"a: &x 1",
			"b:",
			"  c: *x",
		))

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		tcs := map[string]struct {
			node *niceyaml.Node
		}{
			"scoped": {node: yamltest.At(t, dd, paths.Current().Child("b"))},
			"root":   {node: dd},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				_, err := tc.node.Decode[map[string]any](ctx)
				require.ErrorIs(t, err, context.Canceled)
				require.NotErrorIs(t, err, niceyaml.ErrDecode)
			})
		}
	})
}

func TestDocument_Decode_ExcessiveAliasing(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		err   error
		input string
		path  paths.Path
		opts  []niceyaml.SourceOption
	}{
		"nested merge keys": {
			input: yamltest.MergeLevels(7),
			err:   niceyaml.ErrExcessiveAliasing,
		},
		"node with an alias in a document past the limit": {
			input: yamltest.MergeLevels(7),
			path:  paths.Current().Child("m7"),
			err:   niceyaml.ErrExcessiveAliasing,
		},
		"node without an alias in a document past the limit": {
			input: yamltest.MergeLevels(7),
			path:  paths.Current().Child("m0"),
		},
		"nested lists": {
			input: yamltest.AliasLevels(7),
			err:   niceyaml.ErrExcessiveAliasing,
		},
		"nested lists with the limit off": {
			// The decoder shares each list between its aliases, so it
			// decodes the document quickly.
			input: yamltest.AliasLevels(7),
			opts:  []niceyaml.SourceOption{niceyaml.WithAliasLimit(false)},
		},
		"node of nested lists with the limit off": {
			input: yamltest.AliasLevels(7),
			path:  paths.Current().Child("a").Index(7),
			opts:  []niceyaml.SourceOption{niceyaml.WithAliasLimit(false)},
		},
		"nested lists with the limit on again": {
			// The last option wins, as it does for every source option.
			input: yamltest.AliasLevels(7),
			opts: []niceyaml.SourceOption{
				niceyaml.WithAliasLimit(false),
				niceyaml.WithAliasLimit(true),
			},
			err: niceyaml.ErrExcessiveAliasing,
		},
		"a few aliases": {
			input: stringtest.LinesLF(
				"base: &b {a: 1, b: 2}",
				"x:",
				"  <<: *b",
				"y: [*b, *b]",
			),
		},
		"a small mapping aliased in each item of a long list": {
			// Each alias counts as a node of the document, so the 150
			// aliases stay a small enough share of what a decode reads.
			input: stringtest.LinesLF(
				"base: &b {os: linux, arch: amd64, go: stable, cgo: false}",
				"matrix:",
			) + strings.Repeat("  - *b\n", 150),
		},
		"a mapping of 200 keys aliased under 150 keys": {input: yamltest.AliasedMapping(200, 150)},
		"a mapping of 1000 keys aliased under 300 keys": {
			input: yamltest.AliasedMapping(1000, 300),
			err:   niceyaml.ErrExcessiveAliasing,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.AtOrSelf(t, yamltest.FirstDocument(t, tc.input, tc.opts...), tc.path)

			var got any

			err := doc.DecodeInto(t.Context(), &got)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				require.NotErrorIs(t, err, niceyaml.ErrDecode)
				assert.True(t, niceyaml.IsInvalid(err))
				assert.Nil(t, got)

				return
			}

			require.NoError(t, err)
			assert.NotEmpty(t, got)
		})
	}
}

// aliasText is a value that decodes itself from the text of its node.
type aliasText string

func (a *aliasText) UnmarshalText(text []byte) error {
	*a = aliasText(text)

	return nil
}

// forwardText decodes its node into a list of [aliasText] through the
// function go-yaml hands its UnmarshalYAML, and holds the length of the
// list. Its type reaches no text type, so only its method leads the
// decoder to one.
type forwardText int

func (f *forwardText) UnmarshalYAML(unmarshal func(any) error) error {
	var items []aliasText

	err := unmarshal(&items)
	*f = forwardText(len(items))

	return err
}

// forwardTextContext is [forwardText] with an UnmarshalYAML that takes a
// context.
type forwardTextContext int

func (f *forwardTextContext) UnmarshalYAML(_ context.Context, unmarshal func(any) error) error {
	var items []aliasText

	err := unmarshal(&items)
	*f = forwardTextContext(len(items))

	return err
}

// nodeDecoded decodes itself from the node go-yaml hands its
// UnmarshalYAML. Go-yaml calls that method ahead of its UnmarshalText
// and decodes nothing into its text field.
type nodeDecoded struct {
	Addr netip.Addr
}

func (*nodeDecoded) UnmarshalYAML(ast.Node) error { return nil }

func (*nodeDecoded) UnmarshalText([]byte) error {
	return errors.New("unexpected UnmarshalText call")
}

// aliasPlain has no method of its own, so it reads text only when a
// function from niceyaml.WithCustomUnmarshaler decodes it.
type aliasPlain string

// aliasJSON has an UnmarshalJSON method, so it reads text only under
// niceyaml.WithJSONUnmarshalers.
type aliasJSON string

func (a *aliasJSON) UnmarshalJSON(data []byte) error {
	*a = aliasJSON(data)

	return nil
}

func TestDocument_Decode_ExcessiveTextAliasing(t *testing.T) {
	t.Parallel()

	// To decode a type that reads text, the decoder writes the node out
	// with a copy of the long scalar at each alias. A plain string shares
	// the scalar between the aliases, so it decodes.
	manyAliases := stringtest.LinesLF(
		"a: &a "+strings.Repeat("x", 2000),
		"kind: "+yamltest.FlowSequence("*a", 500),
	)
	kind := paths.Current().Child("kind")

	plainText := niceyaml.WithCustomUnmarshaler(func(_ context.Context, a *aliasPlain, decode func(any) error) error {
		return decode((*string)(a))
	})

	pointerText := niceyaml.WithCustomUnmarshaler(
		func(_ context.Context, a **aliasPlain, decode func(any) error) error {
			*a = new(aliasPlain)

			return decode((*string)(*a))
		},
	)

	tcs := map[string]struct {
		err    error
		target func() any
		// The document, or none for manyAliases.
		input  string
		path   paths.Path
		opts   []niceyaml.SourceOption
		decode []niceyaml.DecodeOption
	}{
		"custom unmarshaler elements": {
			path:   kind,
			target: func() any { return new([]aliasPlain) },
			decode: []niceyaml.DecodeOption{plainText},
			err:    niceyaml.ErrExcessiveAliasing,
		},
		"elements no custom unmarshaler decodes": {
			path:   kind,
			target: func() any { return new([]aliasPlain) },
		},
		"custom unmarshaler of a type the target does not reach": {
			path:   kind,
			target: func() any { return new([]string) },
			decode: []niceyaml.DecodeOption{plainText},
		},
		"custom unmarshaler of a pointer type, which names no type": {
			path:   kind,
			target: func() any { return new([]*aliasPlain) },
			decode: []niceyaml.DecodeOption{pointerText},
		},
		"json unmarshaler elements under their option": {
			path:   kind,
			target: func() any { return new([]aliasJSON) },
			decode: []niceyaml.DecodeOption{niceyaml.WithJSONUnmarshalers(true)},
			err:    niceyaml.ErrExcessiveAliasing,
		},
		"json unmarshaler elements without their option": {
			path:   kind,
			target: func() any { return new([]aliasJSON) },
		},
		"text unmarshaler elements": {
			path:   kind,
			target: func() any { return new([]aliasText) },
			err:    niceyaml.ErrExcessiveAliasing,
		},
		"bytes unmarshaler": {
			path:   kind,
			target: func() any { return new(rawText) },
			err:    niceyaml.ErrExcessiveAliasing,
		},
		"pointer to a bytes unmarshaler": {
			path:   kind,
			target: func() any { return new(*rawText) },
			err:    niceyaml.ErrExcessiveAliasing,
		},
		"struct field text unmarshaler": {
			target: func() any {
				return new(struct {
					Kind aliasText `yaml:"kind"`
				})
			},
			err: niceyaml.ErrExcessiveAliasing,
		},
		"struct field of a standard library text type": {
			target: func() any {
				return new(struct {
					Kind netip.Prefix `yaml:"kind"`
				})
			},
			err: niceyaml.ErrExcessiveAliasing,
		},
		"map value bytes unmarshaler": {
			target: func() any { return new(map[string]rawText) },
			err:    niceyaml.ErrExcessiveAliasing,
		},
		"unmarshaler that decodes into text unmarshalers": {
			path:   kind,
			target: func() any { return new(forwardText) },
			err:    niceyaml.ErrExcessiveAliasing,
		},
		"unmarshaler with a context that decodes into text unmarshalers": {
			path:   kind,
			target: func() any { return new(forwardTextContext) },
			err:    niceyaml.ErrExcessiveAliasing,
		},
		"struct field unmarshaler that decodes into text unmarshalers": {
			target: func() any {
				return new(struct {
					Kind forwardText `yaml:"kind"`
				})
			},
			err: niceyaml.ErrExcessiveAliasing,
		},
		"node unmarshaler": {
			path:   kind,
			target: func() any { return new(nodeDecoded) },
		},
		"struct field node unmarshaler": {
			target: func() any {
				return new(struct {
					Kind nodeDecoded `yaml:"kind"`
				})
			},
		},
		"string elements": {
			path:   kind,
			target: func() any { return new([]string) },
		},
		"struct with a time field": {
			target: func() any {
				return new(struct {
					At   time.Time `yaml:"at"`
					Kind []string  `yaml:"kind"`
				})
			},
		},
		"struct with text fields the decoder skips": {
			target: func() any {
				return new(struct {
					hidden netip.Prefix
					Addr   netip.Prefix `yaml:"-"`
					Prefix netip.Prefix `json:"-"`
					Kind   []string     `yaml:"kind"`
				})
			},
		},
		"a few aliases": {
			input:  "a: &a hello\nkind: [*a, *a, *a]\n",
			path:   kind,
			target: func() any { return new([]aliasText) },
		},
		"text unmarshaler elements with the limit off": {
			path:   kind,
			target: func() any { return new([]aliasText) },
			opts:   []niceyaml.SourceOption{niceyaml.WithAliasLimit(false)},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, cmp.Or(tc.input, manyAliases), tc.opts...)
			doc = yamltest.AtOrSelf(t, doc, tc.path)

			err := doc.DecodeInto(t.Context(), tc.target(), tc.decode...)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				require.NotErrorIs(t, err, niceyaml.ErrDecode)
				assert.True(t, niceyaml.IsInvalid(err))

				return
			}

			require.NoError(t, err)
		})
	}
}

// strictInner is a struct with two fields, for the decodes that reject
// unknown fields.
type strictInner struct {
	A int `yaml:"a"`
	B int `yaml:"b"`
}

// StrictDeep is an inline field of [StrictNested]. The decoder sets an
// embedded field only when its type is exported.
type StrictDeep struct {
	D int `yaml:"d"`
}

// StrictNested is an inline struct that holds another inline struct.
type StrictNested struct {
	C          int `yaml:"c"`
	StrictDeep `yaml:",inline"`
}

// strictSelf decodes itself through a second type with the same fields,
// so the decoder checks those fields, at the struct and below it.
type strictSelf struct {
	N []strictInner `yaml:"n"`
	X int           `yaml:"x"`
}

func (s *strictSelf) UnmarshalYAML(unmarshal func(any) error) error {
	type plain strictSelf

	return unmarshal((*plain)(s))
}

// strictText decodes itself from the text of its node with a decoder of
// its own, which skips unknown fields, so the decoder of the decode
// checks none of its fields.
type strictText struct {
	N strictInner `yaml:"n"`
	X int         `yaml:"x"`
}

func (s *strictText) UnmarshalYAML(data []byte) error {
	type plain strictText

	return yaml.Unmarshal(data, (*plain)(s)) //nolint:wrapcheck // The decode reports the error as it is.
}

// strictOption has the fields of [strictText] and no method. A function
// from niceyaml.WithCustomUnmarshaler decodes it and accepts every key.
type strictOption struct {
	N strictInner `yaml:"n"`
	X int         `yaml:"x"`
}

// strictRegistered has the fields of [strictOption]. A function from
// yaml.RegisterCustomUnmarshaler decodes it in every decode of the
// program, as [registerStrict] registers one, and accepts every key.
type strictRegistered strictOption

// strictJSON has the fields of [strictOption] and an UnmarshalJSON
// method that accepts every key, which the decoder calls only under
// niceyaml.WithJSONUnmarshalers.
type strictJSON strictOption

func (*strictJSON) UnmarshalJSON([]byte) error { return nil }

// StrictBase is the type of an anchor that [strictAliased] reads through
// its inline field.
type StrictBase struct {
	A int `yaml:"a"`
}

// strictAliased holds an inline field that reads the anchor a `<<` key
// names. With the omitempty option beside it, the decoder leaves out the
// entries the `<<` keys bring in.
type strictAliased struct {
	*StrictBase `yaml:",omitempty,inline,alias"`

	In strictInner `yaml:"in"`
	B  int         `yaml:"b"`
}

// decodeAs returns a decode of a node as a T with opts that returns the
// error alone, for a table of decodes as different types.
func decodeAs[T any](opts ...niceyaml.DecodeOption) func(context.Context, *niceyaml.Node) error {
	return func(ctx context.Context, n *niceyaml.Node) error {
		_, err := n.Decode[T](ctx, opts...)

		return err
	}
}

// rejectionRows returns the text of each problem [niceyaml.ErrorTree]
// lists for err, one per line, with the position and the path in front
// of each message.
func rejectionRows(err error) string {
	var rows []string

	for node := range niceyaml.NewErrorTree(err).Problems() {
		rows = append(rows, node.Label)
	}

	return strings.Join(rows, "\n")
}

func TestDocument_Decode_UnknownFields(t *testing.T) {
	t.Parallel()

	strict := niceyaml.WithDisallowUnknownFields(true)

	registerStrict()

	// The mapping of s holds a key no field reads, and so do the mapping
	// below it and the root.
	nestedUnknown := stringtest.LinesLF(
		"s:",
		"  x: 1",
		"  zzz: 2",
		"  n:",
		"    a: 1",
		"    www: 2",
		"qqq: 1",
	)

	tcs := map[string]struct {
		decode     func(context.Context, *niceyaml.Node) error
		input      string
		scope      paths.Path
		want       string
		sourceOpts []niceyaml.SourceOption
	}{
		// The decoder reports one of the three, and not always the same
		// one. The rows come in the order of the source.
		"several in one mapping": {
			decode: decodeAs[struct {
				Name string `yaml:"name"`
			}](strict),
			input: stringtest.LinesLF(
				"baz: 3",
				"name: x",
				"foo: 1",
				"bar: 2",
			),
			want: stringtest.JoinLF(
				`1:1: $.baz~: unknown field "baz"`,
				`3:1: $.foo~: unknown field "foo"`,
				`4:1: $.bar~: unknown field "bar"`,
			),
		},
		"several levels": {
			decode: decodeAs[struct {
				Name    string        `yaml:"name"`
				One     strictInner   `yaml:"one"`
				Servers []strictInner `yaml:"servers"`
			}](strict),
			input: stringtest.LinesLF(
				"top: 1",
				"one:",
				"  a: 1",
				"  x: 2",
				"servers:",
				"  - b: 1",
				"    y: 2",
				"  - z: 3",
				"name: n",
			),
			want: stringtest.JoinLF(
				`1:1: $.top~: unknown field "top"`,
				`4:3: $.one.x~: unknown field "x"`,
				`7:5: $.servers[0].y~: unknown field "y"`,
				`8:5: $.servers[1].z~: unknown field "z"`,
			),
		},
		// The fields of an inline struct are fields of the struct that
		// holds it, however deep the inline structs nest.
		"inline fields": {
			decode: decodeAs[struct {
				In           *strictInner `yaml:",inline"`
				Name         string       `yaml:"name"`
				StrictNested `yaml:",inline"`
			}](strict),
			input: stringtest.LinesLF(
				"name: x",
				"c: 1",
				"d: 2",
				"a: 3",
				"zzz: 4",
				"yyy: 5",
			),
			want: stringtest.JoinLF(
				`5:1: $.zzz~: unknown field "zzz"`,
				`6:1: $.yyy~: unknown field "yyy"`,
			),
		},
		// A field reads the key of its yaml tag, its json tag, or its
		// lowercased name, and no other spelling. A field the decoder
		// skips reads no key.
		"field names": {
			decode: decodeAs[struct {
				Tagged   string `json:"nm"`
				Upper    string
				Dashed   string `yaml:"-"`
				hidden   string
				Embedded strictInner
			}](strict),
			input: stringtest.LinesLF(
				"nm: a",
				"upper: b",
				"Upper: c",
				"tagged: d",
				"dashed: e",
				"hidden: f",
				"embedded: {a: 1}",
				"Embedded: {a: 1}",
			),
			want: stringtest.JoinLF(
				`3:1: $.Upper~: unknown field "Upper"`,
				`4:1: $.tagged~: unknown field "tagged"`,
				`5:1: $.dashed~: unknown field "dashed"`,
				`6:1: $.hidden~: unknown field "hidden"`,
				`8:1: $.Embedded~: unknown field "Embedded"`,
			),
		},
		"map, pointer, and array": {
			decode: decodeAs[struct {
				M map[string]strictInner `yaml:"m"`
				P *strictInner           `yaml:"p"`
				L [2]strictInner         `yaml:"l"`
			}](strict),
			input: stringtest.LinesLF(
				"m:",
				"  k:",
				"    x: 1",
				"  j:",
				"    y: 1",
				"p:",
				"  z: 1",
				"l:",
				"  - w: 1",
				"  - a: 1",
			),
			want: stringtest.JoinLF(
				`3:5: $.m.k.x~: unknown field "x"`,
				`5:5: $.m.j.y~: unknown field "y"`,
				`7:3: $.p.z~: unknown field "z"`,
				`9:5: $.l[0].w~: unknown field "w"`,
			),
		},
		// Two elements merge one mapping, and its key reports once, at
		// the place the anchor defines it.
		"merge source read twice": {
			decode: decodeAs[struct {
				Defs    any           `yaml:"defs"`
				Servers []strictInner `yaml:"servers"`
			}](strict),
			input: stringtest.LinesLF(
				"defs: &d",
				"  zzz: 1",
				"  a: 1",
				"servers:",
				"  - <<: *d",
				"  - <<: *d",
				"    yyy: 2",
			),
			want: stringtest.JoinLF(
				`2:3: $.defs.zzz~: unknown field "zzz"`,
				`7:5: $.servers[1].yyy~: unknown field "yyy"`,
			),
		},
		"value an alias reads twice": {
			decode: decodeAs[struct {
				A strictInner `yaml:"a"`
				B strictInner `yaml:"b"`
			}](strict),
			input: stringtest.LinesLF(
				"a: &v",
				"  zzz: 1",
				"  yyy: 2",
				"b: *v",
			),
			want: stringtest.JoinLF(
				`2:3: $.a.zzz~: unknown field "zzz"`,
				`3:3: $.a.yyy~: unknown field "yyy"`,
			),
		},
		// The decoder allows the prefix, so the keys under it stay out
		// of the report though no field has their names.
		"prefix an option allows": {
			decode: decodeAs[struct {
				Name string `yaml:"name"`
			}](
				strict,
				niceyaml.WithAllowedFieldPrefixes("x-"),
			),
			input: stringtest.LinesLF(
				"name: x",
				"x-foo: 1",
				"zzz: 2",
				"x-bar: 3",
				"yyy: 4",
			),
			want: stringtest.JoinLF(
				`3:1: $.zzz~: unknown field "zzz"`,
				`5:1: $.yyy~: unknown field "yyy"`,
			),
		},
		// The unmarshaler of the type hands its fields to the decoder,
		// which checks them, at the struct and below it.
		"type that decodes itself": {
			decode: decodeAs[struct {
				S strictSelf `yaml:"s"`
			}](strict),
			input: stringtest.LinesLF(
				"s:",
				"  x: 1",
				"  zzz: 2",
				"  yyy: 3",
				"  n:",
				"    - a: 1",
				"      www: 2",
				"    - vvv: 3",
				"qqq: 1",
			),
			want: stringtest.JoinLF(
				`3:3: $.s.zzz~: unknown field "zzz"`,
				`4:3: $.s.yyy~: unknown field "yyy"`,
				`7:7: $.s.n[0].www~: unknown field "www"`,
				`8:7: $.s.n[1].vvv~: unknown field "vvv"`,
				`9:1: $.qqq~: unknown field "qqq"`,
			),
		},
		// The option names the type its function decodes, so the function
		// decides, at the struct and below it.
		"type a custom unmarshaler decodes": {
			decode: decodeAs[struct {
				S strictOption `yaml:"s"`
			}](
				strict,
				niceyaml.WithCustomUnmarshaler(
					func(context.Context, *strictOption, func(any) error) error { return nil },
				),
			),
			input: nestedUnknown,
			want:  `7:1: $.qqq~: unknown field "qqq"`,
		},
		// Nothing shows which type a registered function decodes. The
		// function still decides the keys of its own struct, and the
		// search reports a key below it that the function accepts.
		"type a registered function decodes": {
			decode: decodeAs[struct {
				S strictRegistered `yaml:"s"`
			}](strict),
			input: nestedUnknown,
			want: stringtest.JoinLF(
				`6:5: $.s.n.www~: unknown field "www"`,
				`7:1: $.qqq~: unknown field "qqq"`,
			),
		},
		"type with an UnmarshalJSON method under its option": {
			decode: decodeAs[struct {
				S strictJSON `yaml:"s"`
			}](strict, niceyaml.WithJSONUnmarshalers(true)),
			input: nestedUnknown,
			want:  `7:1: $.qqq~: unknown field "qqq"`,
		},
		// The decoder reads the fields of the type, and checks them.
		"type with an UnmarshalJSON method without its option": {
			decode: decodeAs[struct {
				S strictJSON `yaml:"s"`
			}](strict),
			input: nestedUnknown,
			want: stringtest.JoinLF(
				`3:3: $.s.zzz~: unknown field "zzz"`,
				`6:5: $.s.n.www~: unknown field "www"`,
				`7:1: $.qqq~: unknown field "qqq"`,
			),
		},
		"decode into a type that decodes itself": {
			decode: decodeAs[strictSelf](strict),
			input: stringtest.LinesLF(
				"zzz: 2",
				"x: 1",
				"n:",
				"  - www: 2",
			),
			want: stringtest.JoinLF(
				`1:1: $.zzz~: unknown field "zzz"`,
				`4:5: $.n[0].www~: unknown field "www"`,
			),
		},
		// The unmarshaler of the type parses the text with a decoder of
		// its own, which skips unknown fields, so none inside it reports.
		"type that parses its own text": {
			decode: decodeAs[struct {
				S strictText `yaml:"s"`
			}](strict),
			input: stringtest.LinesLF(
				"s:",
				"  x: 1",
				"  zzz: 2",
				"  n:",
				"    www: 2",
				"qqq: 1",
				"ppp: 2",
			),
			want: stringtest.JoinLF(
				`6:1: $.qqq~: unknown field "qqq"`,
				`7:1: $.ppp~: unknown field "ppp"`,
			),
		},
		// The decoder leaves out the entries the merge key brings in, so
		// neither www nor the field under in reports.
		"struct that ignores merge keys": {
			decode: decodeAs[struct {
				Defs *StrictBase   `yaml:"defs"`
				V    strictAliased `yaml:"v"`
			}](strict),
			input: stringtest.LinesLF(
				"v:",
				"  <<: {in: {qqq: 1}, www: 1}",
				"  b: 1",
				"  yyy: 2",
				"  zzz: 3",
			),
			want: stringtest.JoinLF(
				`4:3: $.v.yyy~: unknown field "yyy"`,
				`5:3: $.v.zzz~: unknown field "zzz"`,
			),
		},
		"struct that reads merge keys": {
			decode: decodeAs[struct {
				V struct {
					In strictInner `yaml:"in"`
					B  int         `yaml:"b"`
				} `yaml:"v"`
			}](strict),
			input: stringtest.LinesLF(
				"v:",
				"  <<: {in: {qqq: 1}, www: 1}",
				"  b: 1",
				"  yyy: 2",
			),
			want: stringtest.JoinLF(
				`2:13: $.v.<<.in.qqq~: unknown field "qqq"`,
				`2:22: $.v.<<.www~: unknown field "www"`,
				`4:3: $.v.yyy~: unknown field "yyy"`,
			),
		},
		"interface field": {
			decode: decodeAs[struct {
				S any `yaml:"s"`
			}](strict),
			input: stringtest.LinesLF(
				"s:",
				"  zzz: 2",
				"qqq: 1",
				"ppp: 2",
			),
			want: stringtest.JoinLF(
				`3:1: $.qqq~: unknown field "qqq"`,
				`4:1: $.ppp~: unknown field "ppp"`,
			),
		},
		// The decoder decodes no field from a mapping with a key it does
		// not read as a string, and rejects none of its keys.
		"mapping with a key that is no string": {
			decode: decodeAs[struct {
				A strictInner `yaml:"a"`
				B strictInner `yaml:"b"`
			}](strict),
			input: stringtest.LinesLF(
				"a:",
				"  zzz: 1",
				"  yyy: 1",
				"b:",
				"  1: x",
				"  www: 2",
			),
			want: stringtest.JoinLF(
				`2:3: $.a.zzz~: unknown field "zzz"`,
				`3:3: $.a.yyy~: unknown field "yyy"`,
			),
		},
		// The decoder takes no key from such a mapping that a merge key
		// brings in either, so it rejects the keys of the struct itself
		// and neither www nor the key under in.
		"merged mapping with a key that is no string": {
			decode: decodeAs[struct {
				V struct {
					In strictInner `yaml:"in"`
					B  int         `yaml:"b"`
				} `yaml:"v"`
			}](strict),
			input: stringtest.LinesLF(
				"v:",
				"  <<: {1: x, in: {qqq: 1}, www: 1}",
				"  b: 1",
				"  yyy: 2",
				"  zzz: 3",
			),
			want: stringtest.JoinLF(
				`4:3: $.v.yyy~: unknown field "yyy"`,
				`5:3: $.v.zzz~: unknown field "zzz"`,
			),
		},
		// The paths read from the root of the document.
		"scoped decode": {
			decode: decodeAs[strictInner](strict),
			input: stringtest.LinesLF(
				"servers:",
				"  - a: 1",
				"    zzz: 2",
				"    yyy: 3",
			),
			scope: paths.MustParse("$.servers[0]"),
			want: stringtest.JoinLF(
				`3:5: $.servers[0].zzz~: unknown field "zzz"`,
				`4:5: $.servers[0].yyy~: unknown field "yyy"`,
			),
		},
		// The later of two entries with one key is the one the decoder
		// reads.
		"duplicate keys": {
			decode: decodeAs[struct {
				Name string `yaml:"name"`
			}](strict),
			input: stringtest.LinesLF(
				"zzz: 1",
				"zzz: 2",
				"yyy: 3",
			),
			sourceOpts: []niceyaml.SourceOption{niceyaml.WithAllowDuplicateKeys(true)},
			want: stringtest.JoinLF(
				`2:1: $.zzz~: unknown field "zzz"`,
				`3:1: $.yyy~: unknown field "yyy"`,
			),
		},
		"quoted, tagged, and explicit keys": {
			decode: decodeAs[struct {
				Name string `yaml:"name"`
			}](strict),
			input: stringtest.LinesLF(
				`"q q": 1`,
				"!!str zzz: 2",
				"? yyy",
				": 3",
			),
			want: stringtest.JoinLF(
				`1:1: $.'q q'~: unknown field "q q"`,
				`2:7: $.zzz~: unknown field "zzz"`,
				`3:3: $.yyy~: unknown field "yyy"`,
			),
		},
		// The document reads a second parse, whose tokens the decoder
		// reports.
		"document that reuses an anchor name": {
			decode: decodeAs[struct {
				X int         `yaml:"x"`
				Y int         `yaml:"y"`
				A strictInner `yaml:"a"`
			}](strict),
			input: stringtest.LinesLF(
				"x: &n 1",
				"y: &n 2",
				"a:",
				"  zzz: *n",
				"  yyy: 1",
			),
			want: stringtest.JoinLF(
				`4:3: $.a.zzz~: unknown field "zzz"`,
				`5:3: $.a.yyy~: unknown field "yyy"`,
			),
		},
		"unknown fields allowed": {
			decode: decodeAs[struct {
				Name string `yaml:"name"`
			}](),
			input: "foo: 1\nbar: 2\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rows := strings.Count(tc.want, "\n") + 1

			// The decoder picks the field it reports in no fixed order,
			// so several decodes show that the report does not depend on
			// its pick.
			for range 10 {
				node := yamltest.AtOrSelf(t, yamltest.FirstDocument(t, tc.input, tc.sourceOpts...), tc.scope)

				err := tc.decode(t.Context(), node)
				if tc.want == "" {
					require.NoError(t, err)

					continue
				}

				require.Error(t, err)
				assert.Equal(t, tc.want, rejectionRows(err))

				if rows == 1 {
					require.EqualError(t, err, tc.want)

					continue
				}

				// The message lists each field under the summary, as the
				// tree does.
				require.EqualError(t, err, fmt.Sprintf("%d unknown fields\n%s", rows, tc.want))
				require.ErrorIs(t, err, niceyaml.ErrDecode)

				srcErr := yamltest.RequireSourceError(t, err)
				assert.Same(t, node, yamltest.BoundNode(t, srcErr))

				// The summary counts the fields and points at none.
				_, ok := srcErr.Path()
				assert.False(t, ok)

				_, ok = srcErr.Range()
				assert.False(t, ok)

				fields := yamltest.ProblemsOf(srcErr)
				require.Len(t, fields, rows)

				for _, field := range fields {
					require.ErrorIs(t, field.Err, niceyaml.ErrDecode)
					require.NoError(t, field.Bound.Unresolved())

					path, ok := field.Path()
					require.True(t, ok)
					assert.True(t, strings.HasSuffix(path.String(), "~"), "path %s", path)

					// Each field holds a rejection the decoder returned.
					rejected, ok := errors.AsType[*yaml.UnknownFieldError](field.Err)
					require.True(t, ok)
					assert.Equal(t, rejected.GetMessage(), field.Message())
				}
			}
		})
	}
}

func TestDocument_Decode_UnknownFields_BesideValues(t *testing.T) {
	t.Parallel()

	// The decoder reports the value of the wrong kind and no unknown
	// field. The decode lists the unknown fields beside that rejection,
	// and counts problems, since not each of them is an unknown field.
	doc := yamltest.FirstDocument(t, stringtest.LinesLF(
		"t: abc",
		"qqq: 1",
		"ppp: 2",
	))

	_, err := doc.Decode[struct {
		T int `yaml:"t"`
	}](t.Context(), niceyaml.WithDisallowUnknownFields(true))
	require.EqualError(t, err, stringtest.JoinLF(
		"3 problems",
		"1:4: $.t: expected integer, got string",
		`2:1: $.qqq~: unknown field "qqq"`,
		`3:1: $.ppp~: unknown field "ppp"`,
	))
	require.ErrorIs(t, err, niceyaml.ErrDecode)
	requireInvalid(t, err, 3)

	srcErr := yamltest.RequireSourceError(t, err)

	problems := yamltest.ProblemsOf(srcErr)
	require.Len(t, problems, 3)

	var mismatch *yaml.TypeError

	require.ErrorAs(t, problems[0].Err, &mismatch)

	for _, field := range problems[1:] {
		var unknown *yaml.UnknownFieldError

		require.ErrorAs(t, field.Err, &unknown)
	}

	// Without the option the decoder skips both keys, and the decode
	// reports the value alone.
	_, err = doc.Decode[struct {
		T int `yaml:"t"`
	}](t.Context())
	require.EqualError(t, err, "1:4: $.t: expected integer, got string")
}

func TestDocument_Decode_UnknownFields_KeyKinds(t *testing.T) {
	t.Parallel()

	// KeyKinds holds two structs. The mapping of A always holds an
	// unknown field, so the decode fails and the Node looks for others.
	// The mapping of B holds the key under test beside the unknown field
	// www.
	type keyKinds struct {
		S any         `yaml:"s"`
		I any         `yaml:"i"`
		A strictInner `yaml:"a"`
		B strictInner `yaml:"b"`
	}

	strict := niceyaml.WithDisallowUnknownFields(true)

	// The decoder reads the keys of a struct as strings. It decodes
	// nothing from a mapping that holds a key of another kind, and
	// rejects none of its keys, so www reports only beside a key that
	// reads as a string.
	tcs := map[string]struct {
		key  string
		want bool
	}{
		"plain string":          {key: "k: x", want: true},
		"double quoted":         {key: `"k": x`, want: true},
		"single quoted":         {key: "'k': x", want: true},
		"anchored string":       {key: "&anc k: x", want: true},
		"explicit string":       {key: "? k\n  : x", want: true},
		"string tag on an int":  {key: "!!str 1: x", want: true},
		"local tag on a string": {key: "!custom k: x", want: true},
		"local tag on a number": {key: "!custom 1: x", want: true},
		"date without a tag":    {key: "2020-01-01: x", want: true},
		"alias to a string":     {key: "*s : x", want: true},
		"merge key":             {key: "<<: {a: 1}", want: true},
		"integer":               {key: "1: x"},
		"hexadecimal integer":   {key: "0x10: x"},
		"float":                 {key: "1.5: x"},
		"infinity":              {key: ".inf: x"},
		"boolean":               {key: "true: x"},
		"null":                  {key: "~: x"},
		"explicit integer":      {key: "? 1\n  : x"},
		"integer tag":           {key: "!!int 1: x"},
		"float tag":             {key: "!!float 1: x"},
		"boolean tag":           {key: "!!bool true: x"},
		"null tag":              {key: "!!null k: x"},
		"binary tag":            {key: "!!binary YQ==: x"},
		"timestamp tag":         {key: "!!timestamp 2020-01-01: x"},
		"alias to an integer":   {key: "*i : x"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			anchors := "s: &s str\ni: &i 5\n"
			b := stringtest.LinesLF("b:", "  www: 2", "  "+tc.key)

			// The decoder alone says whether it rejects www beside the
			// key.
			var v keyKinds

			err := yamltest.FirstDocument(t, anchors+b).DecodeInto(t.Context(), &v, strict)
			if tc.want {
				require.ErrorContains(t, err, `unknown field`)
			} else {
				require.NoError(t, err)
			}

			// The report agrees with it.
			var w keyKinds

			err = yamltest.FirstDocument(t, anchors+"a:\n  zzz: 1\n"+b).DecodeInto(t.Context(), &w, strict)
			require.ErrorIs(t, err, niceyaml.ErrDecode)

			rows := rejectionRows(err)
			assert.Contains(t, rows, `$.a.zzz~: unknown field "zzz"`)
			assert.Equal(t, tc.want, strings.Contains(rows, `$.b.www~: unknown field "www"`), rows)
		})
	}
}

func TestDocument_Decode_UnknownFields_IgnoredMerges(t *testing.T) {
	t.Parallel()

	// Aliased holds a struct with an inline field tagged alias and
	// omitempty, for which the decoder leaves out the entries a `<<`
	// merge key brings in. Merged holds the same fields without that
	// field, so the decoder reads those entries.
	type aliased struct {
		Defs *StrictBase   `yaml:"defs"`
		V    strictAliased `yaml:"v"`
	}

	type merged struct {
		V struct {
			In strictInner `yaml:"in"`
			B  int         `yaml:"b"`
		} `yaml:"v"`
		Defs *StrictBase `yaml:"defs"`
	}

	strict := niceyaml.WithDisallowUnknownFields(true)

	tcs := map[string]struct {
		merge string
	}{
		"inline mapping": {
			merge: stringtest.LinesLF(
				"v:",
				"  <<: {in: {qqq: 1}, www: 1}",
				"  b: 1",
			),
		},
		"sequence of sources": {
			merge: stringtest.LinesLF(
				"v:",
				"  <<: [{in: {qqq: 1}}, {www: 1}]",
				"  b: 1",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			decode := func(input string, v any) error {
				return yamltest.FirstDocument(t, input).DecodeInto(t.Context(), v, strict)
			}

			// The decoder alone rejects no key the merge brings in for
			// the struct that ignores merges.
			err := decode(tc.merge, &aliased{})
			require.NoError(t, err)

			// With a key of its own that no field reads, the report
			// lists that key and still none the merge brings in.
			err = decode(tc.merge+"  yyy: 2\n", &aliased{})
			require.ErrorIs(t, err, niceyaml.ErrDecode)

			rows := rejectionRows(err)
			assert.Contains(t, rows, `$.v.yyy~: unknown field "yyy"`)
			assert.NotContains(t, rows, "qqq")
			assert.NotContains(t, rows, "www")

			// The decoder reads the same merge for the struct without
			// the field. It rejects a sequence of sources another way,
			// and the report then lists no unknown field.
			err = decode(tc.merge+"  yyy: 2\n", &merged{})
			require.ErrorIs(t, err, niceyaml.ErrDecode)

			rows = rejectionRows(err)
			if strings.Contains(tc.merge, "<<: [") {
				assert.NotContains(t, rows, "unknown field")

				return
			}

			assert.Contains(t, rows, `$.v.yyy~: unknown field "yyy"`)
			assert.Contains(t, rows, `unknown field "qqq"`)
			assert.Contains(t, rows, `unknown field "www"`)
		})
	}
}

func TestDocument_Decode_UnknownFields_Source(t *testing.T) {
	t.Parallel()

	// A named source puts its name in front of the summary and of each
	// field in the message, and each field keeps its own position.
	source := niceyaml.NewSourceFromString(stringtest.LinesLF(
		"foo: 1",
		"name: x",
		"bar: 2",
	), niceyaml.WithName("cfg.yaml"))

	_, err := source.Decode[struct {
		Name string `yaml:"name"`
	}](t.Context(), niceyaml.WithDisallowUnknownFields(true))
	require.EqualError(t, err, stringtest.JoinLF(
		"cfg.yaml: 2 unknown fields",
		`cfg.yaml:1:1: $.foo~: unknown field "foo"`,
		`cfg.yaml:3:1: $.bar~: unknown field "bar"`,
	))
	require.ErrorIs(t, err, niceyaml.ErrDecode)

	assert.Equal(t, stringtest.JoinLF(
		"cfg.yaml: 2 unknown fields",
		`|-- 1:1: $.foo~: unknown field "foo"`,
		"`-- "+`3:1: $.bar~: unknown field "bar"`,
		"",
		"   1 | foo: 1",
		`     | ^^^ unknown field "foo"`,
		"     | ...",
		"   3 | bar: 2",
		`     | ^^^ unknown field "bar"`,
	), niceyaml.FormatError(err, niceyaml.WithContextLines(0)))
}

func TestDocument_DecodeInto_UnknownFields_EachDocument(t *testing.T) {
	t.Parallel()

	// One value carries the option to the decode of every document.
	strict := niceyaml.DecodeOptions(niceyaml.WithDisallowUnknownFields(true))

	docs := niceyaml.NewSourceFromString(stringtest.LinesLF(
		"foo: 1",
		"bar: 2",
		"---",
		"name: x",
		"---",
		"baz: 3",
	)).Documents()
	require.Len(t, docs, 3)

	want := []string{
		stringtest.JoinLF(
			`1:1: $.foo~: unknown field "foo"`,
			`2:1: $.bar~: unknown field "bar"`,
		),
		"",
		`6:1: $.baz~: unknown field "baz"`,
	}

	for i, doc := range docs {
		var v struct {
			Name string `yaml:"name"`
		}

		err := doc.DecodeInto(t.Context(), &v, strict)
		assert.Equal(t, want[i], rejectionRows(err), "document %d", i)
	}
}

// openHours is the value the tests of this file read at a path. It has
// no Validate method, so each test names the checks that run.
type openHours struct {
	Open  int `yaml:"open"`
	Close int `yaml:"close"`
}

// requiresClose returns a [niceyaml.Validator] that reads the key close
// of the node it gets, as a check for a required key does, and returns
// the error of that read.
func requiresClose() niceyaml.Validator {
	return niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
		_, err := n.At(paths.Current().Child("close"))

		return err //nolint:wrapcheck // The validator returns the error of the read as it is.
	})
}

// pathRead reads the value at a path as one Go type, in the two ways
// the tests compare.
type pathRead struct {
	// Reads through [niceyaml.Node.DecodeAt].
	decodeAt func(ctx context.Context, n *niceyaml.Node, path paths.Path) (any, error)
	// Reads through [niceyaml.Node.At] and then [niceyaml.Node.Decode],
	// the two calls DecodeAt stands for.
	scoped func(ctx context.Context, n *niceyaml.Node, path paths.Path) (any, error)
}

// readAs returns a [pathRead] that decodes into a T with opts.
func readAs[T any](opts ...niceyaml.DecodeOption) pathRead {
	return pathRead{
		decodeAt: func(ctx context.Context, n *niceyaml.Node, path paths.Path) (any, error) {
			return n.DecodeAt[T](ctx, path, opts...)
		},
		scoped: func(ctx context.Context, n *niceyaml.Node, path paths.Path) (any, error) {
			node, err := n.At(path)
			if err != nil {
				var zero T

				return zero, err //nolint:wrapcheck // The test compares the error of the call.
			}

			return node.Decode[T](ctx, opts...)
		},
	}
}

func TestNode_DecodeAt(t *testing.T) {
	t.Parallel()

	const input = `
		kind: Deployment
		replicas: 3
		note: ~
		spec:
		  hours:
		    open: 9
		    close: 17
		    days: 5
		items:
		  - a
		  - b
		ref: *nope
	`

	document := func(t *testing.T) *niceyaml.Node {
		t.Helper()

		return yamltest.FirstDocumentWithPath(t, stringtest.Input(input), "cfg.yaml")
	}

	specPath := paths.Doc().Child("spec")
	hoursPath := specPath.Child("hours")

	tcs := map[string]struct {
		want any
		// The sentinel the error matches, or nil for a read that succeeds.
		err error
		// The two reads, which decode into one type.
		read pathRead
		// The message of the error.
		wantErr string
		// The path of the Node that reads, or none for the root.
		scope paths.Path
		path  paths.Path
	}{
		"a string": {
			path: paths.Current().Child("kind"),
			read: readAs[string](),
			want: "Deployment",
		},
		"an integer": {
			path: paths.Current().Child("replicas"),
			read: readAs[int](),
			want: 3,
		},
		"a sequence": {
			path: paths.Current().Child("items"),
			read: readAs[[]string](),
			want: []string{"a", "b"},
		},
		"a struct": {
			path: hoursPath,
			read: readAs[openHours](),
			want: openHours{Open: 9, Close: 17},
		},
		"a null reads as the zero value": {
			path: paths.Current().Child("note"),
			read: readAs[string](),
			want: "",
		},
		"an @ path reads from the receiver": {
			scope: specPath,
			path:  paths.Current().Child("hours", "open"),
			read:  readAs[int](),
			want:  9,
		},
		"a $ path reads from the root of the document": {
			scope: specPath,
			path:  paths.Doc().Child("kind"),
			read:  readAs[string](),
			want:  "Deployment",
		},
		"a key the root leaves out": {
			path:    paths.Current().Child("name"),
			read:    readAs[string](),
			want:    "",
			err:     paths.ErrNotFound,
			wantErr: "cfg.yaml:1:1: $.name: not found",
		},
		"a key the receiver leaves out": {
			scope:   hoursPath,
			path:    paths.Current().Child("lunch"),
			read:    readAs[int](),
			want:    0,
			err:     paths.ErrNotFound,
			wantErr: "cfg.yaml:5:3: $.spec.hours.lunch: not found",
		},
		"a key looked up in a scalar": {
			path:    paths.Current().Child("kind", "group"),
			read:    readAs[string](),
			want:    "",
			err:     paths.ErrNotFound,
			wantErr: "cfg.yaml: resolve $.kind.group: not found",
		},
		"a value of the wrong type": {
			path:    paths.Current().Child("kind"),
			read:    readAs[int](),
			want:    0,
			err:     niceyaml.ErrDecode,
			wantErr: "cfg.yaml:1:7: $.kind: expected integer, got string",
		},
		"a field the type lacks under WithDisallowUnknownFields": {
			path:    hoursPath,
			read:    readAs[openHours](niceyaml.WithDisallowUnknownFields(true)),
			want:    openHours{},
			err:     niceyaml.ErrDecode,
			wantErr: `cfg.yaml:8:5: $.spec.hours.days~: unknown field "days"`,
		},
		"a wildcard path": {
			path:    paths.Current().Child("items").IndexAll(),
			read:    readAs[string](),
			want:    "",
			err:     paths.ErrWildcard,
			wantErr: "cfg.yaml: resolve $.items[*]: wildcard path matches any number of nodes",
		},
		"an alias that does not resolve": {
			path:    paths.Current().Child("ref"),
			read:    readAs[string](),
			want:    "",
			err:     paths.ErrAlias,
			wantErr: "cfg.yaml: resolve $.ref: alias does not resolve: *nope has no anchor before it",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			node := yamltest.AtOrSelf(t, document(t), tc.scope)

			got, err := tc.read.decodeAt(t.Context(), node, tc.path)
			assert.Equal(t, tc.want, got)

			// DecodeAt returns what At and then Decode return.
			scoped, scopedErr := tc.read.scoped(t.Context(), node, tc.path)
			assert.Equal(t, scoped, got)

			if tc.err == nil {
				require.NoError(t, err)
				require.NoError(t, scopedErr)

				return
			}

			require.ErrorIs(t, err, tc.err)
			require.EqualError(t, err, tc.wantErr)
			require.EqualError(t, scopedErr, tc.wantErr)
		})
	}

	t.Run("a validator checks the node the path selects", func(t *testing.T) {
		t.Parallel()

		var seen string

		late := niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
			seen = n.Path().String()

			return niceyaml.NewError("opens too late", niceyaml.AtPath(paths.Current().Child("open")))
		})

		got, err := document(t).DecodeAt[openHours](t.Context(), hoursPath, niceyaml.WithValidator(late))
		require.EqualError(t, err, "cfg.yaml:6:11: $.spec.hours.open: opens too late")
		assert.Equal(t, "$.spec.hours", seen)
		assert.Equal(t, openHours{}, got)

		// The error is bound through the Node the validator got.
		bound := yamltest.RequireSourceError(t, err)
		assert.Equal(t, "$.spec.hours", yamltest.BoundNode(t, bound).Path().String())
	})

	t.Run("a validator that reads a required key reports not found for a present node", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocumentWithPath(t, openOnlyDoc, "cfg.yaml")

		_, err := doc.At(hoursPath)
		require.NoError(t, err)

		// The error matches paths.ErrNotFound, and the node at the path
		// is present, so the sentinel does not show an absent value.
		_, err = doc.DecodeAt[openHours](t.Context(), hoursPath, niceyaml.WithValidator(requiresClose()))
		require.ErrorIs(t, err, paths.ErrNotFound)
		require.EqualError(t, err, "cfg.yaml:2:3: $.spec.hours.close: not found")
	})

	t.Run("Rebase binds a check of the value where its Node binds it", func(t *testing.T) {
		t.Parallel()

		doc := document(t)

		// A check written for the type, which reads `@` paths from the
		// value.
		check := func(h openHours) error {
			if h.Open < 12 {
				return niceyaml.NewError("opens before noon", niceyaml.AtPath(paths.Current().Child("open")))
			}

			return nil
		}

		h, err := doc.DecodeAt[openHours](t.Context(), hoursPath)
		require.NoError(t, err)

		const want = "cfg.yaml:6:11: $.spec.hours.open: opens before noon"

		require.EqualError(t, doc.Bind(niceyaml.Rebase(check(h), hoursPath)), want)
		require.EqualError(t, yamltest.At(t, doc, hoursPath).Bind(check(h)), want)
	})

	t.Run("a document with no content returns the error of At", func(t *testing.T) {
		t.Parallel()

		versionPath := paths.Current().Child("version")

		tcs := map[string]struct {
			input string
			// The message of the error, or none for a path that selects
			// a node.
			wantErr string
			path    paths.Path
		}{
			"a key of an empty file": {
				input:   "",
				path:    versionPath,
				wantErr: "cfg.yaml: resolve $.version: not found: document has no content",
			},
			"a key of a file of comments": {
				input:   "# nothing\n",
				path:    versionPath,
				wantErr: "cfg.yaml: resolve $.version: not found: document has no content",
			},
			"a key below a header": {
				input:   "---\n",
				path:    versionPath,
				wantErr: "cfg.yaml: resolve $.version: not found: document has no content",
			},
			"the root of an empty file": {
				input:   "",
				path:    paths.Current(),
				wantErr: "cfg.yaml: resolve $: not found: document has no content",
			},
			"the root below a header is a null": {
				input: "---\n",
				path:  paths.Current(),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocumentWithPath(t, tc.input, "cfg.yaml")
				require.True(t, doc.IsEmpty())

				// A decode of the root returns the zero value.
				whole, err := doc.Decode[int](t.Context())
				require.NoError(t, err)
				assert.Zero(t, whole)

				got, err := doc.DecodeAt[int](t.Context(), tc.path)
				assert.Zero(t, got)

				_, atErr := doc.At(tc.path)

				if tc.wantErr == "" {
					require.NoError(t, err)
					require.NoError(t, atErr)

					return
				}

				require.ErrorIs(t, err, paths.ErrNoDocument)
				require.ErrorIs(t, err, paths.ErrNotFound)
				require.EqualError(t, err, tc.wantErr)
				require.EqualError(t, atErr, tc.wantErr)
			})
		}
	})

	t.Run("a document that did not parse returns its syntax error", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.UnparsedDocument(t, "kind: [\n", niceyaml.WithFilePath("cfg.yaml"))

		got, err := doc.DecodeAt[string](t.Context(), paths.Current().Child("kind"))
		require.ErrorIs(t, err, niceyaml.ErrSyntax)
		require.ErrorIs(t, err, doc.Err())
		assert.Empty(t, got)
	})
}

func TestNode_DecodeIfPresent(t *testing.T) {
	t.Parallel()

	versionPath := paths.Current().Child("version")
	portPath := paths.Current().Child("server", "port")
	hoursPath := paths.Doc().Child("spec", "hours")

	tcs := map[string]struct {
		// The sentinel the error matches, or nil for a call that succeeds.
		err   error
		input string
		// The message of the error.
		wantErr string
		path    paths.Path
		// The value of a target that held 1 before the call.
		want  int
		found bool
	}{
		"a value": {
			input: "version: 3\n",
			path:  versionPath,
			want:  3,
			found: true,
		},
		"a value behind an alias": {
			input: "base: &b 3\nversion: *b\n",
			path:  versionPath,
			want:  3,
			found: true,
		},
		"a null keeps the default": {
			input: "version: ~\n",
			path:  versionPath,
			want:  1,
			found: true,
		},
		"a key with no value keeps the default": {
			input: "version:\n",
			path:  versionPath,
			want:  1,
			found: true,
		},
		"an anchored null keeps the default": {
			input: "version: &v ~\n",
			path:  versionPath,
			want:  1,
			found: true,
		},
		"a key the mapping leaves out": {
			input: "name: app\n",
			path:  versionPath,
			want:  1,
		},
		"a key below a key the mapping leaves out": {
			input: "name: app\n",
			path:  portPath,
			want:  1,
		},
		"a key looked up in a scalar": {
			input: "server: hello\n",
			path:  portPath,
			want:  1,
		},
		"an index past the end of a sequence": {
			input: "ports: [80, 443]\n",
			path:  paths.Current().Child("ports").Index(7),
			want:  1,
		},
		"a key of an empty file": {
			input: "",
			path:  versionPath,
			want:  1,
		},
		"a key of a file of comments": {
			input: "# nothing\n",
			path:  versionPath,
			want:  1,
		},
		"a key below a header": {
			input: "---\n",
			path:  versionPath,
			want:  1,
		},
		"the root of an empty file": {
			input: "",
			path:  paths.Current(),
			want:  1,
		},
		"a value of the wrong type": {
			input:   "version: three\n",
			path:    versionPath,
			want:    1,
			err:     niceyaml.ErrDecode,
			wantErr: "cfg.yaml:1:10: $.version: expected integer, got string",
		},
		"an alias that does not resolve": {
			input:   "version: *nope\n",
			path:    versionPath,
			want:    1,
			err:     paths.ErrAlias,
			wantErr: "cfg.yaml: resolve $.version: alias does not resolve: *nope has no anchor before it",
		},
		"a path through an alias that does not resolve": {
			input:   "server: *nope\n",
			path:    portPath,
			want:    1,
			err:     paths.ErrAlias,
			wantErr: "cfg.yaml: resolve $.server.port: alias does not resolve: *nope has no anchor before it",
		},
		"a wildcard path": {
			input:   "ports: [80, 443]\n",
			path:    paths.Current().Child("ports").IndexAll(),
			want:    1,
			err:     paths.ErrWildcard,
			wantErr: "cfg.yaml: resolve $.ports[*]: wildcard path matches any number of nodes",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocumentWithPath(t, tc.input, "cfg.yaml")

			version := 1

			found, err := doc.DecodeIfPresent(t.Context(), tc.path, &version)
			assert.Equal(t, tc.found, found)
			assert.Equal(t, tc.want, version)

			if tc.err == nil {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, tc.err)
			require.EqualError(t, err, tc.wantErr)

			// The error is the one At and then DecodeInto return.
			scoped, scopedErr := doc.At(tc.path)
			if scopedErr == nil {
				other := 1
				scopedErr = scoped.DecodeInto(t.Context(), &other)
			}

			require.EqualError(t, scopedErr, tc.wantErr)
		})
	}

	t.Run("a validator that reads a required key fails a present node", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocumentWithPath(t, openOnlyDoc, "cfg.yaml")
		h := openHours{Open: 8, Close: 17}

		// The error matches paths.ErrNotFound, as the error of an absent
		// path does, and it comes back as an error.
		found, err := doc.DecodeIfPresent(t.Context(), hoursPath, &h, niceyaml.WithValidator(requiresClose()))
		require.ErrorIs(t, err, paths.ErrNotFound)
		require.EqualError(t, err, "cfg.yaml:2:3: $.spec.hours.close: not found")
		assert.False(t, found)
		assert.Equal(t, openHours{Open: 8, Close: 17}, h)
	})

	t.Run("a validator checks the node the path selects", func(t *testing.T) {
		t.Parallel()

		var seen []string

		records := niceyaml.ValidatorFunc(func(_ context.Context, n *niceyaml.Node) error {
			seen = append(seen, n.Path().String())

			return nil
		})

		doc := yamltest.FirstDocumentWithPath(t, openOnlyDoc, "cfg.yaml")

		var h openHours

		found, err := doc.DecodeIfPresent(t.Context(), hoursPath, &h, niceyaml.WithValidator(records))
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, []string{"$.spec.hours"}, seen)

		// No validator runs for a path that selects nothing.
		found, err = doc.DecodeIfPresent(t.Context(), hoursPath.Child("lunch"), &h, niceyaml.WithValidator(records))
		require.NoError(t, err)
		assert.False(t, found)
		assert.Equal(t, []string{"$.spec.hours"}, seen)
	})

	t.Run("a struct target keeps the fields the node leaves out", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			input string
			// The value of a target that held 8 and 17 before the call.
			want  openHours
			found bool
		}{
			"a mapping that sets one field": {
				input: openOnlyDoc,
				want:  openHours{Open: 9, Close: 17},
				found: true,
			},
			"a null": {
				input: stringtest.LinesLF(
					"spec:",
					"  hours: ~",
				),
				want:  openHours{Open: 8, Close: 17},
				found: true,
			},
			"a key the mapping leaves out": {
				input: "spec: {}\n",
				want:  openHours{Open: 8, Close: 17},
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocumentWithPath(t, tc.input, "cfg.yaml")
				h := openHours{Open: 8, Close: 17}

				found, err := doc.DecodeIfPresent(t.Context(), hoursPath, &h)
				require.NoError(t, err)
				assert.Equal(t, tc.found, found)
				assert.Equal(t, tc.want, h)
			})
		}
	})

	t.Run("a pointer target", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			want  *int
			input string
			// Whether the target points to a 7 before the call. It is
			// nil otherwise.
			preset bool
			found  bool
		}{
			"a value fills a nil pointer": {
				input: "version: 3\n",
				want:  new(3),
				found: true,
			},
			"a value fills the value the pointer points to": {
				input:  "version: 3\n",
				preset: true,
				want:   new(3),
				found:  true,
			},
			"a null sets the pointer to nil": {
				input:  "version: ~\n",
				preset: true,
				found:  true,
			},
			"a key the mapping leaves out leaves the pointer as it is": {
				input:  "name: app\n",
				preset: true,
				want:   new(7),
			},
			"a key the mapping leaves out leaves a nil pointer nil": {
				input: "name: app\n",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocumentWithPath(t, tc.input, "cfg.yaml")

				var target *int

				if tc.preset {
					target = new(7)
				}

				before := target

				found, err := doc.DecodeIfPresent(t.Context(), versionPath, &target)
				require.NoError(t, err)
				assert.Equal(t, tc.found, found)
				assert.Equal(t, tc.want, target)

				// The decode fills the value the pointer points to.
				if tc.preset && tc.want != nil {
					assert.Same(t, before, target)
				}
			})
		}
	})

	t.Run("a null sets an interface target to nil", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.FirstDocumentWithPath(t, "version: ~\n", "cfg.yaml")

		var target any = "unset"

		found, err := doc.DecodeIfPresent(t.Context(), paths.Current().Child("name"), &target)
		require.NoError(t, err)
		assert.False(t, found)
		assert.Equal(t, "unset", target)

		found, err = doc.DecodeIfPresent(t.Context(), versionPath, &target)
		require.NoError(t, err)
		assert.True(t, found)
		assert.Nil(t, target)
	})

	t.Run("a target that is no pointer returns ErrDecodeTarget", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			target  any
			wantErr string
		}{
			"a value": {
				target:  1,
				wantErr: "cfg.yaml: decode target is not a non-nil pointer: got int",
			},
			"a nil pointer": {
				target:  (*int)(nil),
				wantErr: "cfg.yaml: decode target is not a non-nil pointer: got *int",
			},
			"nil": {
				wantErr: "cfg.yaml: decode target is not a non-nil pointer: got nil",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				doc := yamltest.FirstDocumentWithPath(t, "version: 3\n", "cfg.yaml")

				// The target fails the call whether or not the document
				// holds the value.
				for holds, path := range map[string]paths.Path{
					"present": versionPath,
					"absent":  paths.Current().Child("name"),
				} {
					found, err := doc.DecodeIfPresent(t.Context(), path, tc.target)
					require.ErrorIs(t, err, niceyaml.ErrDecodeTarget, holds)
					require.EqualError(t, err, tc.wantErr, holds)
					assert.False(t, found, holds)
				}
			})
		}
	})

	t.Run("a document that did not parse returns its syntax error", func(t *testing.T) {
		t.Parallel()

		doc := yamltest.UnparsedDocument(t, "version: [\n", niceyaml.WithFilePath("cfg.yaml"))

		version := 1

		found, err := doc.DecodeIfPresent(t.Context(), versionPath, &version)
		require.ErrorIs(t, err, niceyaml.ErrSyntax)
		require.ErrorIs(t, err, doc.Err())
		assert.False(t, found)
		assert.Equal(t, 1, version)
	})
}

// problemServer is an element of [problemConfig].
type problemServer struct {
	Name    string        `yaml:"name"`
	Port    int           `yaml:"port"`
	Timeout time.Duration `yaml:"timeout"`
}

// problemConfig is the target of a document that holds several problems.
// The decoder reads its fields in the order below, so it rejects a label
// before a server and a server before the replicas.
type problemConfig struct {
	Labels   map[string]int  `yaml:"labels"`
	Servers  []problemServer `yaml:"servers"`
	Replicas int             `yaml:"replicas"`
	Timeout  int             `yaml:"timeout"`
}

// problemImaged is the target of a document that holds an image of the
// type I beside its replicas.
type problemImaged[I any] struct {
	Image    I   `yaml:"image"`
	Replicas int `yaml:"replicas"`
}

// problemTiered is a server whose tier decodes itself from text.
type problemTiered struct {
	Name string `yaml:"name"`
	Tier tier   `yaml:"tier"`
	Port int    `yaml:"port"`
}

// problemImage decodes itself through a type whose fields do not mirror
// its own, and requires a repo. Its document writes pull as text, where
// the field holds a boolean.
type problemImage struct {
	Repo string
	Pull bool
}

func (i *problemImage) UnmarshalYAML(unmarshal func(any) error) error {
	var raw struct {
		Repo string `yaml:"repo"`
		Pull string `yaml:"pull"`
	}

	err := unmarshal(&raw)
	if err != nil {
		return err
	}

	if raw.Repo == "" {
		return errRepoRequired
	}

	i.Repo, i.Pull = raw.Repo, raw.Pull == "always"

	return nil
}

// problemPulled has the fields of [problemImage] and no method. A
// function from niceyaml.WithCustomUnmarshaler decodes it.
type problemPulled struct {
	Repo string `yaml:"repo"`
	Pull bool   `yaml:"pull"`
}

// problemRegistered has the fields of [problemPulled]. A function from
// yaml.RegisterCustomUnmarshaler decodes it in every decode of the
// program, as registerPulled registers one.
type problemRegistered problemPulled

// problemJSON has the fields of [problemPulled] and an UnmarshalJSON
// method that accepts every value, which the decoder calls only under
// niceyaml.WithJSONUnmarshalers.
type problemJSON problemPulled

func (*problemJSON) UnmarshalJSON([]byte) error { return nil }

// decodePulled decodes a [problemPulled] as [problemImage] decodes
// itself, through decode.
func decodePulled(_ context.Context, p *problemPulled, decode func(any) error) error {
	var raw struct {
		Repo string `yaml:"repo"`
		Pull string `yaml:"pull"`
	}

	err := decode(&raw)
	if err != nil {
		return err
	}

	if raw.Repo == "" {
		return errRepoRequired
	}

	p.Repo, p.Pull = raw.Repo, raw.Pull == "always"

	return nil
}

// problemWrapped decodes itself through a second type with the same
// fields and wraps the error of that decode in errBadServer.
type problemWrapped struct {
	Name    string        `yaml:"name"`
	Timeout time.Duration `yaml:"timeout"`
}

func (w *problemWrapped) UnmarshalYAML(unmarshal func(any) error) error {
	type plain problemWrapped

	err := unmarshal((*plain)(w))
	if err != nil {
		return fmt.Errorf("%w: %w", errBadServer, err)
	}

	return nil
}

// problemPorted decodes itself over the value it holds and requires a
// port, which a caller may set before the decode.
type problemPorted struct {
	Name string `yaml:"name"`
	Port int    `yaml:"port"`
}

func (p *problemPorted) UnmarshalYAML(unmarshal func(any) error) error {
	type plain problemPorted

	err := unmarshal((*plain)(p))
	if err != nil {
		return err
	}

	if p.Port == 0 {
		return errPortRequired
	}

	return nil
}

// callsKey is the context key of the [*problemCalls] of a decode.
type callsKey struct{}

// problemCalls is the state the unmarshalers of one decode share: the
// names [problemUnique] has seen, the calls [problemCounted] took, and a
// function that ends the context of the decode.
type problemCalls struct {
	seen   map[string]bool
	cancel context.CancelFunc
	count  int
}

// problemUnique decodes itself from text and rejects a name another
// value of the decode holds.
type problemUnique string

func (u *problemUnique) UnmarshalYAML(ctx context.Context, unmarshal func(any) error) error {
	var name string

	err := unmarshal(&name)
	if err != nil {
		return err
	}

	calls, ok := ctx.Value(callsKey{}).(*problemCalls)
	if !ok {
		return nil
	}

	if calls.seen[name] {
		return fmt.Errorf("duplicate name %q", name)
	}

	calls.seen[name] = true
	*u = problemUnique(name)

	return nil
}

// problemUniqueText rejects a name another value of the decode holds, as
// [problemUnique] does, and has an UnmarshalText method too. The decoder
// finds its UnmarshalYAML method first and never calls the other.
type problemUniqueText string

func (u *problemUniqueText) UnmarshalYAML(ctx context.Context, unmarshal func(any) error) error {
	return (*problemUnique)(u).UnmarshalYAML(ctx, unmarshal)
}

func (*problemUniqueText) UnmarshalText([]byte) error {
	return errBadServer
}

// uniqueTier decodes a [tier] in place of the UnmarshalText method of the
// type, as a function from niceyaml.WithCustomUnmarshaler does, and
// rejects a name another value of the decode holds.
func uniqueTier(ctx context.Context, _ *tier, decode func(any) error) error {
	calls, ok := ctx.Value(callsKey{}).(*problemCalls)
	if !ok {
		return nil
	}

	var name string

	err := decode(&name)
	if err != nil {
		return err
	}

	if calls.seen[name] {
		return fmt.Errorf("duplicate name %q", name)
	}

	calls.seen[name] = true

	return nil
}

// callCounter counts the calls of an unmarshaler that takes no context,
// under a name the unmarshaler gives each call. Every decode of the type
// shares the count, so one test alone decodes a type that counts in one.
type callCounter struct {
	calls map[string]int
	mu    sync.Mutex
}

// add counts one call under name and returns the calls counted under
// that name since the last take.
func (c *callCounter) add(name string) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.calls == nil {
		c.calls = map[string]int{}
	}

	c.calls[name]++

	return c.calls[name]
}

// take returns the calls counted since the last take.
func (c *callCounter) take() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()

	calls := c.calls
	c.calls = nil

	return calls
}

// problemLevel decodes itself from text, takes "info" and "warn" alone,
// and counts its calls in levelCalls.
type problemLevel string

func (l *problemLevel) UnmarshalText(text []byte) error {
	level := string(text)

	levelCalls.add(level)

	if level != "info" && level != "warn" {
		return fmt.Errorf("unknown level %q", level)
	}

	*l = problemLevel(level)

	return nil
}

// problemOnce decodes itself from text, takes each text once, and panics
// when it gets a text again. It counts its calls in onceCalls.
type problemOnce string

func (o *problemOnce) UnmarshalText(text []byte) error {
	if onceCalls.add(string(text)) > 1 {
		panic("text decoded twice")
	}

	*o = problemOnce(text)

	return nil
}

// problemEither decodes itself from the text of a scalar. Under
// [niceyaml.WithJSONUnmarshalers], the decoder hands its UnmarshalJSON
// method a mapping or a sequence, and the method counts its calls in
// eitherCalls.
type problemEither struct{}

func (*problemEither) UnmarshalText([]byte) error {
	return nil
}

func (*problemEither) UnmarshalJSON([]byte) error {
	eitherCalls.add("json")

	return nil
}

// problemTextual has the methods of [problemEither] and counts no call,
// so several tests decode it at once.
type problemTextual struct{}

func (*problemTextual) UnmarshalText([]byte) error {
	return nil
}

func (*problemTextual) UnmarshalJSON([]byte) error {
	return nil
}

// problemPlaced decodes itself from text and reports an error that names
// a position already.
type problemPlaced string

func (*problemPlaced) UnmarshalText([]byte) error {
	return niceyaml.Invalid(errBadServer, niceyaml.AtPosition(position.New(0, 0)))
}

// problemCounted decodes itself from any mapping and counts its calls.
type problemCounted struct {
	Name string
}

func (c *problemCounted) UnmarshalYAML(ctx context.Context, unmarshal func(any) error) error {
	if calls, ok := ctx.Value(callsKey{}).(*problemCalls); ok {
		calls.count++
	}

	var raw struct {
		Name string `yaml:"name"`
	}

	err := unmarshal(&raw)
	if err != nil {
		return err
	}

	c.Name = raw.Name

	return nil
}

// problemEnding decodes itself from any value and ends the context of
// the decode.
type problemEnding struct{}

func (*problemEnding) UnmarshalYAML(ctx context.Context, _ func(any) error) error {
	if calls, ok := ctx.Value(callsKey{}).(*problemCalls); ok {
		calls.cancel()
	}

	return nil
}

// problemNested decodes itself with a decode of its text as a source of
// its own, which rejects unknown fields, and returns the error of that
// decode as that source bound it.
type problemNested struct{}

func (*problemNested) UnmarshalYAML(ctx context.Context, data []byte) error {
	source := niceyaml.NewSourceFromString(string(data), niceyaml.WithName("inner.yaml"))

	_, err := source.Decode[struct {
		Port int `yaml:"port"`
	}](ctx, niceyaml.WithDisallowUnknownFields(true))

	return err
}

// problemLocated decodes itself from a word and returns the error the
// word names, located as an unmarshaler locates its own error. The
// decoder hands it the node of its value, and the token of that node
// gives the position of the word. It panics for the word "panic" and
// accepts a word it does not know.
type problemLocated struct{}

func (*problemLocated) UnmarshalYAML(ctx context.Context, node ast.Node) error {
	tk := node.GetToken()
	at := position.NewFromToken(tk)
	port := paths.Doc().Child("port")

	switch tk.Value {
	case "plain":
		return errBadServer

	case "position":
		return niceyaml.Invalid(errBadServer, niceyaml.AtPosition(at))

	case "range":
		end := position.New(at.Line, at.Col+len(tk.Value))

		return niceyaml.Invalid(errBadServer, niceyaml.AtRange(position.NewRange(at, end)))

	case "far":
		far := position.New(at.Line+100, at.Col)

		return niceyaml.Invalid(errBadServer, niceyaml.AtRange(position.NewRange(far, far)))

	case "path":
		return niceyaml.Invalid(errBadServer, niceyaml.AtPath(port))

	case "absent":
		return niceyaml.Invalid(errBadServer, niceyaml.AtPath(paths.Doc().Child("absent")))

	case "below":
		return niceyaml.Invalid(errBadServer, niceyaml.AtPath(port.Child("unit")))

	case "unplaced":
		//nolint:wrapcheck // The test checks where a decode places the result as it is.
		return niceyaml.BindValue(niceyaml.Invalid(errBadServer, niceyaml.AtPath(port)))

	case "ambiguous":
		// The keys 1 and "1" share a path, and the rate under the first
		// has no limit.
		//nolint:wrapcheck // The test checks where a decode places the result as it is.
		return niceyaml.SelfValidateValue(ctx, map[any]layerRate{1: {}, "1": {Limit: 1}})

	case "elsewhere":
		inner := niceyaml.NewSourceFromString("port: 1\n", niceyaml.WithName("inner.yaml"))

		return inner.Bind(niceyaml.Invalid(errBadServer, niceyaml.AtPath(port)))

	case "panic":
		panic(errBadServer)

	default:
		return nil
	}
}

// problemTimed is the target of a document whose first rejection names no
// token. The decoder reads the fields in the order below. It thus rejects
// a value that reports its own error, a timeout, or a tier ahead of the
// port, and it rejects the port at a token.
type problemTimed struct {
	Own     problemLocated `yaml:"own"`
	Timeout time.Duration  `yaml:"timeout"`
	Tier    tier           `yaml:"tier"`
	Port    uint16         `yaml:"port"`
}

// decodeRows returns what a report reads from err, the error of a decode
// of doc, as one row for each node of its [niceyaml.ErrorTree]. A row
// holds the name of the source, the text and the message of the node,
// its path, its position, and its range. It also says whether the node
// has a path and a position, is bound to doc, matches
// [niceyaml.ErrDecode], and blames the document.
func decodeRows(doc *niceyaml.Node, err error) []string {
	var rows []string

	for node := range niceyaml.NewErrorTree(err).All() {
		path, pathed := node.Path()
		pos, located := node.Bound.Position()
		rng, _ := node.Bound.Range()
		bound, _ := node.Bound.Node()

		rows = append(rows, fmt.Sprintf(
			"%s | %s | %s | %s %t | %s %s %t | bound=%t decode=%t invalid=%t",
			node.Bound.Source().Name(), node.Label, node.Message(), path, pathed, pos, rng, located,
			bound == doc, errors.Is(node.Err, niceyaml.ErrDecode), node.IsInvalid(),
		))
	}

	return rows
}

// problemDeep holds itself, for a document nested many levels deep.
type problemDeep struct {
	C *problemDeep `yaml:"c"`
	V int          `yaml:"v"`
}

// deepDocument returns a flow mapping nested depth levels deep, whose
// innermost mapping holds a value no integer field takes.
func deepDocument(depth int) string {
	return strings.Repeat("{c: ", depth) + "{v: bad}" + strings.Repeat("}", depth) + "\n"
}

// requireInvalid checks that the document is at fault for err, the error
// of a decode, and for each of its problems, of which it holds count.
func requireInvalid(t *testing.T, err error, count int) {
	t.Helper()

	require.True(t, niceyaml.IsInvalid(err))

	found := 0

	for problem := range niceyaml.NewErrorTree(err).Problems() {
		require.True(t, problem.IsInvalid(), problem.Label)

		found++
	}

	require.Equal(t, count, found)
}

func TestSource_Decode_Problems(t *testing.T) {
	t.Parallel()

	// A user fixed one report for each run of this document: the timeout,
	// then the port, then the unknown field.
	source := niceyaml.NewSourceFromString(
		stringtest.LinesLF(
			"replcas: 3",
			"timeout: soon",
			"servers:",
			"  - port: http",
			"  - port: 80",
		),
		niceyaml.WithName("app.yaml"),
	)

	_, err := source.Decode[problemConfig](t.Context(), niceyaml.WithDisallowUnknownFields(true))
	require.EqualError(t, err, stringtest.JoinLF(
		"app.yaml: 3 problems",
		`app.yaml:1:1: $.replcas~: unknown field "replcas"`,
		"app.yaml:2:10: $.timeout: expected integer, got string",
		"app.yaml:4:11: $.servers[0].port: expected integer, got string",
	))
	require.ErrorIs(t, err, niceyaml.ErrDecode)
	requireInvalid(t, err, 3)

	assert.Equal(t, stringtest.JoinLF(
		"app.yaml: 3 problems",
		`|-- 1:1: $.replcas~: unknown field "replcas"`,
		"|-- 2:10: $.timeout: expected integer, got string",
		"`-- 4:11: $.servers[0].port: expected integer, got string",
		"",
		"   1 | replcas: 3",
		`     | ^^^^^^^ unknown field "replcas"`,
		"   2 | timeout: soon",
		"     |          ^^^^ expected integer, got string",
		"     | ...",
		"   4 |   - port: http",
		"     |           ^^^^ expected integer, got string",
	), niceyaml.FormatError(err, niceyaml.WithContextLines(0)))

	srcErr := yamltest.RequireSourceError(t, err)

	// The summary counts the problems and points at none.
	_, ok := srcErr.Position()
	assert.False(t, ok)

	_, ok = srcErr.Path()
	assert.False(t, ok)

	problems := yamltest.ProblemsOf(srcErr)
	require.Len(t, problems, 3)

	want := []struct {
		path string
		pos  position.Position
	}{
		{path: "$.replcas~", pos: position.New(0, 0)},
		{path: "$.timeout", pos: position.New(1, 9)},
		{path: "$.servers[0].port", pos: position.New(3, 10)},
	}

	for i, problem := range problems {
		require.ErrorIs(t, problem.Err, niceyaml.ErrDecode)
		require.NoError(t, problem.Bound.Unresolved())

		path, ok := problem.Path()
		require.True(t, ok)
		assert.Equal(t, want[i].path, path.String())

		pos, ok := problem.Position()
		require.True(t, ok)
		assert.Equal(t, want[i].pos, pos)
	}

	// Each problem holds the rejection the decoder returned for it.
	var unknown *yaml.UnknownFieldError

	require.ErrorAs(t, problems[0].Err, &unknown)
	assert.Equal(t, "replcas", unknown.Token.Value)

	for i, value := range []string{"soon", "http"} {
		var mismatch *yaml.TypeError

		require.ErrorAs(t, problems[i+1].Err, &mismatch)
		assert.Equal(t, value, mismatch.Token.Value)
	}
}

func TestDocument_Decode_Problems(t *testing.T) {
	t.Parallel()

	strict := niceyaml.WithDisallowUnknownFields(true)

	tcs := map[string]struct {
		decode func(context.Context, *niceyaml.Node) error
		input  string
		want   string
		scope  paths.Path
	}{
		// One problem comes back as the error itself, with no summary.
		"one problem": {
			decode: decodeAs[problemConfig](),
			input:  "timeout: soon\nreplicas: 3\n",
			want:   "1:10: $.timeout: expected integer, got string",
		},
		"one unknown field": {
			decode: decodeAs[problemConfig](strict),
			input:  "tmeout: 1\n",
			want:   `1:1: $.tmeout~: unknown field "tmeout"`,
		},
		// The rows come in the order of the source, whatever order the
		// target declares its fields in.
		"scalars of the wrong kind": {
			decode: decodeAs[problemConfig](),
			input: stringtest.LinesLF(
				"timeout: soon",
				"replicas: 1.5x",
				"servers:",
				"  - port: http",
				"  - name: [a]",
				"labels: {a: x, b: 2, c: y}",
			),
			want: stringtest.JoinLF(
				"6 problems",
				"1:10: $.timeout: expected integer, got string",
				"2:11: $.replicas: expected integer, got string",
				"4:11: $.servers[0].port: expected integer, got string",
				"5:5: $.servers[1].name: expected string, got sequence",
				"6:13: $.labels.a: expected integer, got string",
				"6:25: $.labels.c: expected integer, got string",
			),
		},
		// A mapping or a sequence where the target takes another kind is
		// one problem, and the walk reads nothing below it.
		"mappings and sequences of the wrong kind": {
			decode: decodeAs[problemConfig](),
			input: stringtest.LinesLF(
				"timeout: {a: soon}",
				"replicas: [x, y]",
				"servers:",
				"  - a",
				"  - [b]",
				"labels: [c]",
			),
			want: stringtest.JoinLF(
				"5 problems",
				"1:1: $.timeout: expected integer, got mapping",
				"2:1: $.replicas: expected integer, got sequence",
				"4:5: $.servers[0]: expected mapping, got string",
				"5:3: $.servers[1]: expected mapping, got sequence",
				"6:1: $.labels: expected mapping, got sequence",
			),
		},
		"numbers out of range": {
			decode: decodeAs[struct {
				Small int8   `yaml:"small"`
				Wide  uint16 `yaml:"wide"`
				Count uint   `yaml:"count"`
			}](),
			input: stringtest.LinesLF(
				"small: 300",
				"wide: 70000",
				"count: -1",
			),
			want: stringtest.JoinLF(
				"3 problems",
				"1:8: $.small: expected integer from -128 to 127, got 300",
				"2:7: $.wide: expected integer from 0 to 65535, got 70000",
				"3:8: $.count: expected integer from 0 to 18446744073709551615, got -1",
			),
		},
		// The decoder parses a duration itself and reports the error of
		// the parse with no token.
		"durations": {
			decode: decodeAs[problemConfig](),
			input: stringtest.LinesLF(
				"servers:",
				"  - {name: a, timeout: soon}",
				`  - {name: b, timeout: "5"}`,
				"  - {name: c, timeout: 5}",
				"  - {name: d, timeout: [1]}",
				"replicas: x",
			),
			want: stringtest.JoinLF(
				"5 problems",
				`2:24: $.servers[0].timeout: time: invalid duration "soon"`,
				`3:24: $.servers[1].timeout: time: missing unit in duration "5"`,
				"4:24: $.servers[2].timeout: expected duration, got integer",
				"5:15: $.servers[3].timeout: expected duration, got sequence",
				"6:11: $.replicas: expected integer, got string",
			),
		},
		// The decoder reads text that is no timestamp as the zero time,
		// and rejects a value of another kind.
		"timestamps": {
			decode: decodeAs[struct {
				When  time.Time `yaml:"when"`
				Until time.Time `yaml:"until"`
				N     int       `yaml:"n"`
			}](),
			input: stringtest.LinesLF(
				"when: later",
				"until: 5",
				"n: x",
			),
			want: stringtest.JoinLF(
				"2 problems",
				"2:8: $.until: expected timestamp, got integer",
				"3:4: $.n: expected integer, got string",
			),
		},
		"unknown fields beside values": {
			decode: decodeAs[problemConfig](strict),
			input: stringtest.LinesLF(
				"timeout: soon",
				"servers:",
				"  - {name: a, timeout: soon, prt: 1}",
				"labels: {a: x}",
				"replcas: 2",
			),
			want: stringtest.JoinLF(
				"5 problems",
				"1:10: $.timeout: expected integer, got string",
				`3:24: $.servers[0].timeout: time: invalid duration "soon"`,
				`3:30: $.servers[0].prt~: unknown field "prt"`,
				"4:13: $.labels.a: expected integer, got string",
				`5:1: $.replcas~: unknown field "replcas"`,
			),
		},
		// The decoder reads nothing from a null, so a null is no problem
		// for any type.
		"nulls": {
			decode: decodeAs[problemConfig](),
			input: stringtest.LinesLF(
				"timeout: soon",
				"replicas: ~",
				"servers:",
				"  - ~",
				"  - {port: ~, timeout: null}",
				"labels: {a: ~}",
			),
			want: "1:10: $.timeout: expected integer, got string",
		},
		"pointers and arrays": {
			decode: decodeAs[struct {
				P   *int           `yaml:"p"`
				PS  *problemServer `yaml:"ps"`
				PP  **int          `yaml:"pp"`
				Arr [2]int         `yaml:"arr"`
			}](),
			input: stringtest.LinesLF(
				"p: x",
				"ps: {port: y}",
				"pp: z",
				"arr: [1, w]",
			),
			want: stringtest.JoinLF(
				"4 problems",
				"1:4: $.p: expected integer, got string",
				"2:12: $.ps.port: expected integer, got string",
				"3:5: $.pp: expected integer, got string",
				"4:10: $.arr[1]: expected integer, got string",
			),
		},
		// The decoder fills an interface with any value, so no value
		// below one is a problem.
		"values below an interface": {
			decode: decodeAs[struct {
				A any            `yaml:"a"`
				M map[string]any `yaml:"m"`
				L []any          `yaml:"l"`
				N int            `yaml:"n"`
			}](),
			input: stringtest.LinesLF(
				"a: {port: [http]}",
				"m: {k: {port: http}}",
				"l: [x, {y: z}]",
				"n: w",
			),
			want: "4:4: $.n: expected integer, got string",
		},
		// The fields of an inline struct read the mapping of the struct
		// that holds it.
		"inline struct": {
			decode: decodeAs[struct {
				Name         string `yaml:"name"`
				StrictNested `yaml:",inline"`
			}](),
			input: stringtest.LinesLF(
				"c: x",
				"d: y",
				"name: [n]",
			),
			want: stringtest.JoinLF(
				"3 problems",
				"1:4: $.c: expected integer, got string",
				"2:4: $.d: expected integer, got string",
				"3:1: $.name: expected string, got sequence",
			),
		},
		// The decoder hands an inline map every entry of the mapping, in
		// no fixed order, so it rejects x in one decode and y in another.
		// The report holds both in every decode.
		"inline map": {
			decode: decodeAs[struct {
				Rest map[string]int `yaml:",inline"`
				A    bool           `yaml:"a"`
				B    bool           `yaml:"b"`
			}](),
			input: stringtest.LinesLF(
				"x: bad",
				"a: 1",
				"y: worse",
				"b: 2",
			),
			want: stringtest.JoinLF(
				"4 problems",
				"1:4: $.x: expected integer, got string",
				"2:4: $.a: expected boolean, got integer",
				"3:4: $.y: expected integer, got string",
				"4:4: $.b: expected boolean, got integer",
			),
		},
		// The decoder names a value by the place its anchor defines it,
		// so a value several aliases read is one problem.
		"value several aliases read": {
			decode: decodeAs[struct {
				A int `yaml:"a"`
				B int `yaml:"b"`
				C int `yaml:"c"`
				D int `yaml:"d"`
			}](),
			input: stringtest.LinesLF(
				"a: &x soon",
				"b: *x",
				"c: *x",
				"d: bad",
			),
			want: stringtest.JoinLF(
				"2 problems",
				"1:7: $.a: expected integer, got string",
				"4:4: $.d: expected integer, got string",
			),
		},
		// The error of a parse has no token, so it takes the path of the
		// value that reads it, and each alias reads the duration anew.
		"duration several aliases read": {
			decode: decodeAs[struct {
				A any           `yaml:"a"`
				B time.Duration `yaml:"b"`
				C time.Duration `yaml:"c"`
				D int           `yaml:"d"`
			}](),
			input: stringtest.LinesLF(
				"a: &x soon",
				"b: *x",
				"c: *x",
				"d: bad",
			),
			want: stringtest.JoinLF(
				"3 problems",
				`2:4: $.b: time: invalid duration "soon"`,
				`3:4: $.c: time: invalid duration "soon"`,
				"4:4: $.d: expected integer, got string",
			),
		},
		// Two servers read the timeout of one anchor through a merge
		// key, which is one value and one problem.
		"values a merge key brings in": {
			decode: decodeAs[problemConfig](),
			input: stringtest.LinesLF(
				"base: &b {port: http, timeout: soon}",
				"servers:",
				"  - *b",
				"  - {<<: *b, name: [a]}",
				"labels: {<<: {k: x}, j: y}",
				"replicas: z",
			),
			want: stringtest.JoinLF(
				"6 problems",
				"1:17: $.base.port: expected integer, got string",
				`1:32: $.servers[0].timeout: time: invalid duration "soon"`,
				"4:14: $.servers[1].name: expected string, got sequence",
				"5:18: $.labels.<<.k: expected integer, got string",
				"5:25: $.labels.j: expected integer, got string",
				"6:11: $.replicas: expected integer, got string",
			),
		},
		// The decoder leaves out what the merge key brings in for a
		// struct that ignores merge keys, so b is no problem.
		"struct that ignores merge keys": {
			decode: decodeAs[struct {
				Defs *StrictBase   `yaml:"defs"`
				V    strictAliased `yaml:"v"`
			}](),
			input: stringtest.LinesLF(
				"v:",
				"  <<: {b: bad, in: {a: worse}}",
				"  in: {b: x}",
			),
			want: "3:11: $.v.in.b: expected integer, got string",
		},
		// The decoder refuses a sequence of sources for a struct and
		// decodes none of its fields, so b is no problem beside the
		// refusal.
		"struct whose merge key the decoder refuses": {
			decode: decodeAs[struct {
				V strictInner `yaml:"v"`
				N int         `yaml:"n"`
			}](),
			input: stringtest.LinesLF(
				"v:",
				"  <<: [{a: x}]",
				"  b: bad",
				"n: z",
			),
			want: stringtest.JoinLF(
				"2 problems",
				"2:3: $.v.<<: expected mapping, got sequence",
				"4:4: $.n: expected integer, got string",
			),
		},
		// The decoder decodes no field from a mapping that holds a key it
		// does not read as a string, and takes no entry from one that a
		// merge key brings in.
		"mappings with a key that is no string": {
			decode: decodeAs[struct {
				V strictInner `yaml:"v"`
				W strictInner `yaml:"w"`
				N int         `yaml:"n"`
			}](),
			input: stringtest.LinesLF(
				"v:",
				"  1: q",
				"  a: bad",
				"w:",
				"  <<: {true: q, a: bad}",
				"  b: worse",
				"n: z",
			),
			want: stringtest.JoinLF(
				"2 problems",
				"6:6: $.w.b: expected integer, got string",
				"7:4: $.n: expected integer, got string",
			),
		},
		// The decoder holds the value of an anchor and assigns it to the
		// target of an alias that takes it, so the alias passes where
		// the value alone does not decode.
		"alias to a value the decoder assigns": {
			decode: decodeAs[struct {
				Defs any   `yaml:"defs"`
				L    []int `yaml:"l"`
				X    int   `yaml:"x"`
				N    int   `yaml:"n"`
			}](),
			input: stringtest.LinesLF(
				"defs: {a: &a !!int 1.5}",
				"l: [*a, 2]",
				"x: *a",
				"n: z",
			),
			want: "4:4: $.n: expected integer, got string",
		},
		// The decoder rejects a tag that does not convert its value
		// before it decodes any value, and the others still report.
		"tag that does not convert its value": {
			decode: decodeAs[problemConfig](),
			input:  "timeout: !!bool maybe\nreplicas: x\n",
			want: stringtest.JoinLF(
				"2 problems",
				`1:17: $.timeout: cannot convert "maybe" to boolean`,
				"2:11: $.replicas: expected integer, got string",
			),
		},
		// The paths read from the root of the document.
		"scoped decode": {
			decode: decodeAs[problemServer](),
			input: stringtest.LinesLF(
				"base: &b {port: http}",
				"servers:",
				"  - {name: a, port: 1}",
				"  - {<<: *b, name: [b], timeout: soon}",
			),
			scope: paths.MustParse("$.servers[1]"),
			want: stringtest.JoinLF(
				"3 problems",
				"1:17: $.base.port: expected integer, got string",
				"4:14: $.servers[1].name: expected string, got sequence",
				`4:34: $.servers[1].timeout: time: invalid duration "soon"`,
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// The decoder picks the unknown field and the inline map value
			// it reports in no fixed order, so several decodes show that
			// the report does not depend on its pick. Every second decode
			// reads a source with a reference document. The decoder then
			// reads each anchor of the document under a new name, and the
			// report stays the same.
			for i := range 20 {
				var srcOpts []niceyaml.SourceOption

				if i%2 == 1 {
					srcOpts = append(srcOpts, niceyaml.WithReferences(
						niceyaml.NewSourceFromString(stringtest.LinesLF(
							"a: &a 2",
							"b: &b {port: 1}",
							"x: &x 1",
						)),
					))
				}

				node := yamltest.AtOrSelf(t, yamltest.FirstDocument(t, tc.input, srcOpts...), tc.scope)

				err := tc.decode(t.Context(), node)
				require.EqualError(t, err, tc.want)
				require.ErrorIs(t, err, niceyaml.ErrDecode)

				srcErr := yamltest.RequireSourceError(t, err)
				assert.Same(t, node, yamltest.BoundNode(t, srcErr))

				rows := strings.Count(tc.want, "\n")

				requireInvalid(t, err, max(rows, 1))

				// One problem points at its value, and a summary at none.
				_, ok := srcErr.Position()
				assert.Equal(t, rows == 0, ok)

				_, ok = srcErr.Path()
				assert.Equal(t, rows == 0, ok)

				problems := yamltest.ProblemsOf(srcErr)
				require.Len(t, problems, rows)

				for _, problem := range problems {
					require.ErrorIs(t, problem.Err, niceyaml.ErrDecode)
					require.NoError(t, problem.Bound.Unresolved())

					_, ok := problem.Position()
					assert.True(t, ok)

					_, ok = problem.Path()
					assert.True(t, ok)
				}
			}
		})
	}
}

func TestDocument_Decode_Problems_SecondDocument(t *testing.T) {
	t.Parallel()

	// A problem of a later document keeps its line in the file.
	docs := niceyaml.NewSourceFromString(
		stringtest.LinesLF(
			"replicas: 1",
			"---",
			"replicas: x",
			"timeout: soon",
		),
		niceyaml.WithName("app.yaml"),
	).Documents()
	require.Len(t, docs, 2)

	var cfg problemConfig

	err := docs[1].DecodeInto(t.Context(), &cfg)
	require.EqualError(t, err, stringtest.JoinLF(
		"app.yaml: document 2: 2 problems",
		"app.yaml:3:11: $.replicas: expected integer, got string",
		"app.yaml:4:10: $.timeout: expected integer, got string",
	))
}

func TestDocument_Decode_Problems_Unmarshalers(t *testing.T) {
	t.Parallel()

	custom := niceyaml.WithCustomUnmarshaler(decodePulled)

	registerPulled()

	// The documents several cases share. Each holds replicas no integer
	// field takes. The image of the first lacks the repo its unmarshalers
	// require, the image of the second passes them, and the image of the
	// third holds a repo no string field takes.
	const (
		noRepo   = "image: {pull: always}\nreplicas: x\n"
		withRepo = "replicas: x\nimage: {repo: r, pull: always}\n"
		listRepo = "replicas: x\nimage: {repo: [r], pull: always}\n"
	)

	tcs := map[string]struct {
		decode func(context.Context, *niceyaml.Node) error
		is     error
		input  string
		want   string
	}{
		// The unmarshaler reads pull as text, so the boolean field of
		// the type is no problem, and its own error stays the report.
		"unmarshaler whose fields do not mirror the document": {
			decode: decodeAs[problemImaged[problemImage]](),
			input:  "image: {pull: always}\n",
			is:     errRepoRequired,
			want:   "1:1: $.image: repo is required",
		},
		"unmarshaler error beside a value": {
			decode: decodeAs[problemImaged[problemImage]](),
			input:  noRepo,
			is:     errRepoRequired,
			want: stringtest.JoinLF(
				"2 problems",
				"1:1: $.image: repo is required",
				"2:11: $.replicas: expected integer, got string",
			),
		},
		// The unmarshaler names a field of its value, and the report
		// holds the error at that field beside the other problem.
		"unmarshaler error at a path of its own beside a value": {
			decode: decodeAs[struct {
				Spans    []span `yaml:"spans"`
				Replicas int    `yaml:"replicas"`
			}](),
			input: stringtest.LinesLF(
				"spans:",
				"  - {from: 9, to: 3}",
				"replicas: x",
			),
			is: errSpan,
			want: stringtest.JoinLF(
				"2 problems",
				"2:19: $.spans[0].to: to is below from",
				"3:11: $.replicas: expected integer, got string",
			),
		},
		"value beside an unmarshaler that passes": {
			decode: decodeAs[problemImaged[problemImage]](),
			input:  withRepo,
			want:   "1:11: $.replicas: expected integer, got string",
		},
		// The option names the type its function decodes, so the error
		// of the function binds at the value, beside the other problem.
		"custom unmarshaler function": {
			decode: decodeAs[problemImaged[problemPulled]](custom),
			input:  noRepo,
			is:     errRepoRequired,
			want: stringtest.JoinLF(
				"2 problems",
				"1:1: $.image: repo is required",
				"2:11: $.replicas: expected integer, got string",
			),
		},
		// The search leaves the image to the function, which takes pull
		// as text, so the boolean field of the type is no problem.
		"value beside a custom unmarshaler function that passes": {
			decode: decodeAs[problemImaged[problemPulled]](custom),
			input:  withRepo,
			want:   "1:11: $.replicas: expected integer, got string",
		},
		// Nothing shows which type a registered function decodes, so the
		// error of the function has no location and comes back alone.
		"registered unmarshaler function": {
			decode: decodeAs[problemImaged[problemRegistered]](),
			input:  noRepo,
			is:     errRepoRequired,
			want:   "repo is required",
		},
		// The struct the function decodes confirms each of its fields,
		// so the function, which takes pull as text, has the last word.
		"value beside a registered unmarshaler function that passes": {
			decode: decodeAs[problemImaged[problemRegistered]](),
			input:  withRepo,
			want:   "1:11: $.replicas: expected integer, got string",
		},
		// The option turns the method on, so the search leaves the image
		// to the method, which accepts it.
		"json unmarshaler under its option": {
			decode: decodeAs[problemImaged[problemJSON]](niceyaml.WithJSONUnmarshalers(true)),
			input:  listRepo,
			want:   "1:11: $.replicas: expected integer, got string",
		},
		// The decoder reads the fields of the type, so the search reads
		// them too.
		"json unmarshaler without its option": {
			decode: decodeAs[problemImaged[problemJSON]](),
			input:  listRepo,
			want: stringtest.JoinLF(
				"3 problems",
				"1:11: $.replicas: expected integer, got string",
				"2:9: $.image.repo: expected string, got sequence",
				"2:26: $.image.pull: expected boolean, got string",
			),
		},
		// The error of the unmarshaler keeps its wrapper and its
		// sentinel beside the other problems.
		"unmarshaler that wraps a sentinel": {
			decode: decodeAs[struct {
				Server   problemWrapped `yaml:"server"`
				Replicas int            `yaml:"replicas"`
				Limit    int            `yaml:"limit"`
			}](),
			input: stringtest.LinesLF(
				"server: {timeout: soon}",
				"replicas: x",
				"limit: y",
			),
			is: errBadServer,
			want: stringtest.JoinLF(
				"3 problems",
				`1:1: $.server: bad server: time: invalid duration "soon"`,
				"2:11: $.replicas: expected integer, got string",
				"3:8: $.limit: expected integer, got string",
			),
		},
		// An enum reports every value its UnmarshalText method rejects,
		// since the search hands the method each scalar again.
		"values that decode themselves": {
			decode: decodeAs[struct {
				Tiers []tier `yaml:"tiers"`
				N     int    `yaml:"n"`
			}](),
			input: "tiers: [low, mid, top]\nn: x\n",
			is:    errUnknownTier,
			want: stringtest.JoinLF(
				"3 problems",
				`1:14: $.tiers[1]: unknown tier "mid"`,
				`1:19: $.tiers[2]: unknown tier "top"`,
				"2:4: $.n: expected integer, got string",
			),
		},
		// The decoder rejects the port first, and the search finds the
		// tier.
		"text value beside a value of the wrong kind": {
			decode: decodeAs[struct {
				Port int  `yaml:"port"`
				Tier tier `yaml:"tier"`
			}](),
			input: "tier: mid\nport: x\n",
			is:    errUnknownTier,
			want: stringtest.JoinLF(
				"2 problems",
				`1:7: $.tier: unknown tier "mid"`,
				"2:7: $.port: expected integer, got string",
			),
		},
		// The decoder rejects the timeout first, the field its struct
		// declares before the tier.
		"text value beside a duration": {
			decode: decodeAs[tierServer](),
			input:  "tier: mid\ntimeout: soon\n",
			is:     errUnknownTier,
			want: stringtest.JoinLF(
				"2 problems",
				`1:7: $.tier: unknown tier "mid"`,
				`2:10: $.timeout: time: invalid duration "soon"`,
			),
		},
		// The map holds pointers, and the search reads the value each
		// points to.
		"text values of a map": {
			decode: decodeAs[struct {
				Tiers map[string]*tier `yaml:"tiers"`
			}](),
			input: "tiers: {a: mid, b: low, c: top}\n",
			is:    errUnknownTier,
			want: stringtest.JoinLF(
				"2 problems",
				`1:12: $.tiers.a: unknown tier "mid"`,
				`1:28: $.tiers.c: unknown tier "top"`,
			),
		},
		// An alias reads the scalar into an element of its own, so the
		// element reports at the alias, as a duration behind one does.
		"alias to a text value": {
			decode: decodeAs[struct {
				Tiers []tier `yaml:"tiers"`
			}](),
			input: "tiers: [&t mid, *t, top]\n",
			is:    errUnknownTier,
			want: stringtest.JoinLF(
				"3 problems",
				`1:12: $.tiers[0]: unknown tier "mid"`,
				`1:17: $.tiers[1]: unknown tier "mid"`,
				`1:21: $.tiers[2]: unknown tier "top"`,
			),
		},
		// The document writes the tier once, so both servers that merge
		// it report one problem.
		"text value a merge key brings in": {
			decode: decodeAs[struct {
				Servers []problemTiered `yaml:"servers"`
			}](),
			input: stringtest.LinesLF(
				"base: &b {tier: mid}",
				"servers:",
				"  - {<<: *b, name: a}",
				"  - {<<: *b, name: b, port: x}",
			),
			is: errUnknownTier,
			want: stringtest.JoinLF(
				"2 problems",
				`1:17: $.servers[0].tier: unknown tier "mid"`,
				"4:29: $.servers[1].port: expected integer, got string",
			),
		},
		// The search reads no key of a map, so the key reports in a
		// later decode.
		"text keys of a map": {
			decode: decodeAs[struct {
				N     int          `yaml:"n"`
				Tiers map[tier]int `yaml:"tiers"`
			}](),
			input: "n: x\ntiers: {mid: 1, low: 2}\n",
			want:  "1:4: $.n: expected integer, got string",
		},
		// The error of the method points at a place of its own, so the
		// search does not put it at the value.
		"text error that names a place": {
			decode: decodeAs[struct {
				N      int           `yaml:"n"`
				Placed problemPlaced `yaml:"placed"`
			}](),
			input: "n: x\nplaced: a\n",
			want:  "1:4: $.n: expected integer, got string",
		},
		// The decoder refuses a mapping or a sequence for a text type
		// with no call of its method, so the search reports each one.
		"mappings and sequences in a text type": {
			decode: decodeAs[struct {
				Tiers []tier `yaml:"tiers"`
				N     int    `yaml:"n"`
			}](),
			input: "tiers: [low, [a], mid, {b: 1}]\nn: x\n",
			is:    errUnknownTier,
			want: stringtest.JoinLF(
				"4 problems",
				"1:14: $.tiers[1]: expected string, got sequence",
				`1:19: $.tiers[2]: unknown tier "mid"`,
				"1:24: $.tiers[3]: expected string, got mapping",
				"2:4: $.n: expected integer, got string",
			),
		},
		// The decoder rejects the port first, and the search finds the
		// sequence.
		"sequence in a text type beside a value of the wrong kind": {
			decode: decodeAs[struct {
				Port int  `yaml:"port"`
				Tier tier `yaml:"tier"`
			}](),
			input: "tier: [a]\nport: x\n",
			want: stringtest.JoinLF(
				"2 problems",
				"1:1: $.tier: expected string, got sequence",
				"2:7: $.port: expected integer, got string",
			),
		},
		// The decoder rejects the sequence first, and the search finds
		// it again, so the report holds it once.
		"sequence in a text type the decoder rejects first": {
			decode: decodeAs[struct {
				Tier tier `yaml:"tier"`
				Port int  `yaml:"port"`
			}](),
			input: "tier: [a]\nport: x\n",
			want: stringtest.JoinLF(
				"2 problems",
				"1:1: $.tier: expected string, got sequence",
				"2:7: $.port: expected integer, got string",
			),
		},
		// The option hands the mapping to the UnmarshalJSON method of the
		// type, so the search leaves it to that method.
		"mapping in a text type with an UnmarshalJSON method under its option": {
			decode: decodeAs[struct {
				Either map[string]problemTextual `yaml:"either"`
				Port   int                       `yaml:"port"`
			}](niceyaml.WithJSONUnmarshalers(true)),
			input: "either: {a: {b: 1}}\nport: x\n",
			want:  "2:7: $.port: expected integer, got string",
		},
		// The unmarshaler returns an error bound to a source of its own. Its
		// position lies in that source, so nothing joins it.
		"unmarshaler error bound to another source": {
			decode: decodeAs[struct {
				Nested   problemNested `yaml:"nested"`
				Replicas int           `yaml:"replicas"`
			}](),
			input: "nested: {prt: 80}\nreplicas: x\n",
			want:  `inner.yaml:1:2: $.prt~: unknown field "prt"`,
		},
		"mapping in a text type with an UnmarshalJSON method": {
			decode: decodeAs[struct {
				Either map[string]problemTextual `yaml:"either"`
				Port   int                       `yaml:"port"`
			}](),
			input: "either: {a: {b: 1}}\nport: x\n",
			want: stringtest.JoinLF(
				"2 problems",
				"1:10: $.either.a: expected string, got mapping",
				"2:7: $.port: expected integer, got string",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := tc.decode(t.Context(), yamltest.FirstDocument(t, tc.input))
			require.EqualError(t, err, tc.want)
			require.ErrorIs(t, err, niceyaml.ErrDecode)
			requireInvalid(t, err, max(strings.Count(tc.want, "\n"), 1))

			if tc.is != nil {
				require.ErrorIs(t, err, tc.is)
			}
		})
	}
}

func TestDocument_Decode_Problems_References(t *testing.T) {
	t.Parallel()

	strict := niceyaml.WithDisallowUnknownFields(true)

	// The reference document defines b, as some of the documents do.
	references := stringtest.LinesLF(
		"defaults: &defaults {port: http}",
		"limit: &limit soon",
		"b: &b {port: 1}",
	)

	// The decoder reads the timeout before any other field.
	type timeoutFirst struct {
		Timeout  int             `yaml:"timeout"`
		Replicas int             `yaml:"replicas"`
		Servers  []problemServer `yaml:"servers"`
	}

	tcs := map[string]struct {
		decode func(context.Context, *niceyaml.Node) error
		input  string
		want   string
		scope  paths.Path
	}{
		// An alias reads the anchor of its own document, so the port of
		// the reference document is no part of the report.
		"anchor name the reference document defines too": {
			decode: decodeAs[problemConfig](),
			input: stringtest.LinesLF(
				"base: &b {port: bad}",
				"servers:",
				"  - *b",
				"  - {<<: *b, name: [a]}",
				"replicas: x",
			),
			want: stringtest.JoinLF(
				"3 problems",
				"1:17: $.base.port: expected integer, got string",
				"4:14: $.servers[1].name: expected string, got sequence",
				"5:11: $.replicas: expected integer, got string",
			),
		},
		// The document holds no line for a value of a reference document,
		// so the search reads nothing behind an alias to one. The port the
		// merge key brings in reports in a later decode.
		"mapping that merges a reference document": {
			decode: decodeAs[problemConfig](strict),
			input: stringtest.LinesLF(
				"servers:",
				"  - {<<: *defaults, name: [a], prt: 1}",
				"  - {port: y, nme: 2}",
				"replicas: x",
				"rplicas: 2",
			),
			want: stringtest.JoinLF(
				"6 problems",
				"2:21: $.servers[0].name: expected string, got sequence",
				`2:32: $.servers[0].prt~: unknown field "prt"`,
				"3:12: $.servers[1].port: expected integer, got string",
				`3:15: $.servers[1].nme~: unknown field "nme"`,
				"4:11: $.replicas: expected integer, got string",
				`5:1: $.rplicas~: unknown field "rplicas"`,
			),
		},
		// The decoder rejects the timeout of the reference document first.
		// That rejection binds at the alias, and the others report beside
		// it.
		"rejection inside a reference document": {
			decode: decodeAs[timeoutFirst](),
			input: stringtest.LinesLF(
				"replicas: x",
				"timeout: *limit",
				"servers:",
				"  - {port: y}",
			),
			want: stringtest.JoinLF(
				"3 problems",
				"1:11: $.replicas: expected integer, got string",
				"2:10: expected integer, got string",
				"4:12: $.servers[0].port: expected integer, got string",
			),
		},
		// The decoder rejects the port first, and the search reads nothing
		// behind the alias, so the timeout reports in a later decode.
		"value of a reference document beside a rejection": {
			decode: decodeAs[problemConfig](),
			input: stringtest.LinesLF(
				"replicas: x",
				"timeout: *limit",
				"servers:",
				"  - {port: y}",
			),
			want: stringtest.JoinLF(
				"2 problems",
				"1:11: $.replicas: expected integer, got string",
				"4:12: $.servers[0].port: expected integer, got string",
			),
		},
		// Two aliases read the reference document, and nothing says which
		// of them led to the rejection. It binds at no position, so it
		// comes back alone.
		"rejection with no position": {
			decode: decodeAs[problemConfig](),
			input: stringtest.LinesLF(
				"replicas: x",
				"timeout: *limit",
				"servers:",
				"  - *defaults",
				"  - {port: y}",
			),
			want: "expected integer, got string",
		},
		"scoped decode": {
			decode: decodeAs[problemServer](),
			input: stringtest.LinesLF(
				"base: &b {port: bad}",
				"servers:",
				"  - {name: a}",
				"  - {<<: *b, name: [b], timeout: *limit}",
			),
			scope: paths.MustParse("$.servers[1]"),
			want: stringtest.JoinLF(
				"2 problems",
				"1:17: $.base.port: expected integer, got string",
				"4:14: $.servers[1].name: expected string, got sequence",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			for range 10 {
				doc := yamltest.FirstDocument(t, tc.input,
					niceyaml.WithReferences(niceyaml.NewSourceFromString(references)))
				node := yamltest.AtOrSelf(t, doc, tc.scope)

				err := tc.decode(t.Context(), node)
				require.EqualError(t, err, tc.want)
				require.ErrorIs(t, err, niceyaml.ErrDecode)
				requireInvalid(t, err, max(strings.Count(tc.want, "\n"), 1))
			}
		})
	}
}

func TestDocument_DecodeInto_Problems_Defaults(t *testing.T) {
	t.Parallel()

	// The decode fills the server over the port the caller set, so the
	// check for a port passes. No second decode of the server into a new
	// value runs that check without the port.
	cfg := struct {
		Server  problemPorted `yaml:"server"`
		Timeout int           `yaml:"timeout"`
	}{Server: problemPorted{Port: 8080}}

	err := yamltest.FirstDocument(t, "timeout: soon\nserver: {name: a}\n").DecodeInto(t.Context(), &cfg)
	require.EqualError(t, err, "1:10: $.timeout: expected integer, got string")
	require.NotErrorIs(t, err, errPortRequired)
}

func TestDocument_Decode_Problems_UnmarshalerCalls(t *testing.T) {
	t.Parallel()

	// The unmarshaler takes each name once. A second call for a name it
	// has seen would report a duplicate.
	stateful := map[string]struct {
		decode func(context.Context, *niceyaml.Node) error
	}{
		"unmarshaler with state": {
			decode: decodeAs[struct {
				Names    []problemUnique `yaml:"names"`
				Timeout  int             `yaml:"timeout"`
				Replicas int             `yaml:"replicas"`
			}](),
		},
		// The decoder calls the UnmarshalYAML method of the type and
		// never its UnmarshalText method, so the search hands the type no
		// scalar again.
		"unmarshaler with state and an UnmarshalText method": {
			decode: decodeAs[struct {
				Names    []problemUniqueText `yaml:"names"`
				Timeout  int                 `yaml:"timeout"`
				Replicas int                 `yaml:"replicas"`
			}](),
		},
		// The decoder calls the function in place of the UnmarshalText
		// method of the type, so the search hands the type no scalar
		// again.
		"custom unmarshaler function with state": {
			decode: decodeAs[struct {
				Names    []tier `yaml:"names"`
				Timeout  int    `yaml:"timeout"`
				Replicas int    `yaml:"replicas"`
			}](niceyaml.WithCustomUnmarshaler(uniqueTier)),
		},
	}

	for name, tc := range stateful {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			calls := &problemCalls{seen: map[string]bool{}}
			ctx := context.WithValue(t.Context(), callsKey{}, calls)

			err := tc.decode(ctx, yamltest.FirstDocument(t, stringtest.LinesLF(
				"names: [a, b, c]",
				"timeout: soon",
				"replicas: x",
			)))
			require.EqualError(t, err, stringtest.JoinLF(
				"2 problems",
				"2:10: $.timeout: expected integer, got string",
				"3:11: $.replicas: expected integer, got string",
			))
			assert.Len(t, calls.seen, 3)
		})
	}

	// The decode calls the method once for each level. The second decode
	// that finds the value behind the rejection calls it again for the
	// levels up to "loud". The search calls it once more for each level,
	// and again to confirm each level the method rejects.
	t.Run("UnmarshalText method", func(t *testing.T) {
		t.Parallel()

		_, err := yamltest.FirstDocument(t, stringtest.LinesLF(
			"services:",
			"  - {name: a, level: info, port: 1}",
			"  - {name: b, level: loud, port: x}",
			"  - {name: c, level: quiet, port: y}",
		)).Decode[struct {
			Services []struct {
				Name  string       `yaml:"name"`
				Level problemLevel `yaml:"level"`
				Port  int          `yaml:"port"`
			} `yaml:"services"`
		}](t.Context())
		require.EqualError(t, err, stringtest.JoinLF(
			"4 problems",
			`3:22: $.services[1].level: unknown level "loud"`,
			"3:34: $.services[1].port: expected integer, got string",
			`4:22: $.services[2].level: unknown level "quiet"`,
			"4:35: $.services[2].port: expected integer, got string",
		))
		requireInvalid(t, err, 4)
		assert.Equal(t, map[string]int{"info": 3, "loud": 4, "quiet": 3}, levelCalls.take())
	})

	// The decode calls the method once and rejects the port. The search
	// calls the method again, and the panic of that call adds no problem.
	t.Run("UnmarshalText method that panics in the search", func(t *testing.T) {
		t.Parallel()

		_, err := yamltest.FirstDocument(t, "port: x\nmode: fast\n").Decode[struct {
			Port int         `yaml:"port"`
			Mode problemOnce `yaml:"mode"`
		}](t.Context())
		require.EqualError(t, err, "1:7: $.port: expected integer, got string")
		requireInvalid(t, err, 1)
		assert.Equal(t, map[string]int{"fast": 2}, onceCalls.take())

		var p *niceyaml.PanicError

		assert.NotErrorAs(t, err, &p, "the error holds the panic of the search")
	})

	// The decoder hands the mapping to the UnmarshalJSON method of the
	// type. The search hands a type with an UnmarshalText method a scalar
	// alone, so it does not call the other method again.
	t.Run("UnmarshalJSON method beside an UnmarshalText method", func(t *testing.T) {
		t.Parallel()

		_, err := yamltest.FirstDocument(t, "either: {a: 1}\nport: x\n").Decode[struct {
			Either problemEither `yaml:"either"`
			Port   int           `yaml:"port"`
		}](t.Context(), niceyaml.WithJSONUnmarshalers(true))
		require.EqualError(t, err, "2:7: $.port: expected integer, got string")
		assert.Equal(t, map[string]int{"json": 1}, eitherCalls.take())
	})

	// A decode of the struct would call the unmarshaler of its inline
	// field, so nothing confirms the fields beside it, and the decoder
	// reports them one for each decode.
	t.Run("inline field that decodes itself", func(t *testing.T) {
		t.Parallel()

		calls := &problemCalls{}
		ctx := context.WithValue(t.Context(), callsKey{}, calls)

		_, err := yamltest.FirstDocument(t, stringtest.LinesLF(
			"name: a",
			"port: http",
			"limit: x",
			"rest: {port: y}",
		)).Decode[struct {
			Meta  problemCounted `yaml:",inline"`
			Port  int            `yaml:"port"`
			Rest  problemServer  `yaml:"rest"`
			Limit int            `yaml:"limit"`
		}](ctx)
		require.EqualError(t, err, stringtest.JoinLF(
			"2 problems",
			"2:7: $.port: expected integer, got string",
			"4:14: $.rest.port: expected integer, got string",
		))
		assert.Equal(t, 1, calls.count)
	})
}

func TestDocument_Decode_Problems_Context(t *testing.T) {
	t.Parallel()

	type config struct {
		Ending   problemEnding `yaml:"ending"`
		Replicas int           `yaml:"replicas"`
		Timeout  int           `yaml:"timeout"`
	}

	input := stringtest.LinesLF(
		"ending: now",
		"replicas: x",
		"timeout: soon",
	)
	imageInput := stringtest.LinesLF(
		"replicas: x",
		"timeout: soon",
		"image: {pull: always}",
	)

	t.Run("context that goes on", func(t *testing.T) {
		t.Parallel()

		_, err := yamltest.FirstDocument(t, input).Decode[config](t.Context())
		require.EqualError(t, err, stringtest.JoinLF(
			"2 problems",
			"2:11: $.replicas: expected integer, got string",
			"3:10: $.timeout: expected integer, got string",
		))
	})

	// The decoder never checks the context, so the decode still returns
	// its rejection, and the search for the others does not run.
	t.Run("context that ends in the decode", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		ctx = context.WithValue(ctx, callsKey{}, &problemCalls{cancel: cancel})

		_, err := yamltest.FirstDocument(t, input).Decode[config](ctx)
		require.EqualError(t, err, "2:11: $.replicas: expected integer, got string")
	})

	// The registered function decodes the image in the decode, and ends
	// the context when the search asks it about a field of the image. The
	// search has found the timeout by then, and reports none of what it
	// found.
	t.Run("context that ends in the search", func(t *testing.T) {
		t.Parallel()

		type ended struct {
			Pull bool `yaml:"pull"`
		}

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		calls := 0

		yaml.RegisterCustomUnmarshaler(func(*ended, []byte) error {
			calls++
			if calls > 1 {
				cancel()
			}

			return nil
		})

		_, err := yamltest.FirstDocument(t, imageInput).Decode[struct {
			Replicas int   `yaml:"replicas"`
			Timeout  int   `yaml:"timeout"`
			Image    ended `yaml:"image"`
		}](ctx)
		require.EqualError(t, err, "1:11: $.replicas: expected integer, got string")
		assert.Equal(t, 2, calls)
	})

	// The option names the type its function decodes, so the search asks
	// the function about no field of the image, and reports the timeout.
	t.Run("custom unmarshaler the search leaves alone", func(t *testing.T) {
		t.Parallel()

		calls := 0
		counting := niceyaml.WithCustomUnmarshaler(func(context.Context, *problemPulled, func(any) error) error {
			calls++

			return nil
		})

		_, err := yamltest.FirstDocument(t, imageInput).Decode[struct {
			Replicas int           `yaml:"replicas"`
			Timeout  int           `yaml:"timeout"`
			Image    problemPulled `yaml:"image"`
		}](t.Context(), counting)
		require.EqualError(t, err, stringtest.JoinLF(
			"2 problems",
			"1:11: $.replicas: expected integer, got string",
			"2:10: $.timeout: expected integer, got string",
		))
		assert.Equal(t, 1, calls)
	})
}

func TestNewSourceFromLayers_Decode_Problems(t *testing.T) {
	t.Parallel()

	strict := niceyaml.WithDisallowUnknownFields(true)

	// Own returns a file whose own key holds word, below a comment and a
	// blank line and between a port and a timeout the decoder rejects.
	own := func(word string) string {
		return stringtest.LinesLF("# prod", "", "port: 70000", "own: "+word, "timeout: soon")
	}

	// The decoder names no token for the value it rejects first in each
	// input. The layers hold one file, so their decode reports what a
	// decode of that file reports.
	tcs := map[string]struct {
		err   error
		input string
		want  string
		opts  []niceyaml.DecodeOption
	}{
		"one problem": {
			input: "timeout: soon\n",
			want:  `base.yaml:1:10: $.timeout: time: invalid duration "soon"`,
		},
		"duration beside an unknown field": {
			input: "timeout: soon\nextra: 1\n",
			opts:  []niceyaml.DecodeOption{strict},
			want: stringtest.JoinLF(
				"base.yaml: 2 problems",
				`base.yaml:1:10: $.timeout: time: invalid duration "soon"`,
				`base.yaml:2:1: $.extra~: unknown field "extra"`,
			),
		},
		"text value beside a number out of range": {
			input: "tier: mid\nport: 70000\n",
			err:   errUnknownTier,
			want: stringtest.JoinLF(
				"base.yaml: 2 problems",
				`base.yaml:1:7: $.tier: unknown tier "mid"`,
				"base.yaml:2:7: $.port: expected integer from 0 to 65535, got 70000",
			),
		},
		// The rows come in the order of the file, with the rejection of
		// the decoder among them.
		"rejection after the other problems": {
			input: stringtest.LinesLF(
				"extra: 1",
				"port: 70000",
				"tier: mid",
				"timeout: soon",
			),
			opts: []niceyaml.DecodeOption{strict},
			err:  errUnknownTier,
			want: stringtest.JoinLF(
				"base.yaml: 4 problems",
				`base.yaml:1:1: $.extra~: unknown field "extra"`,
				"base.yaml:2:7: $.port: expected integer from 0 to 65535, got 70000",
				`base.yaml:3:7: $.tier: unknown tier "mid"`,
				`base.yaml:4:10: $.timeout: time: invalid duration "soon"`,
			),
		},
		// The merged text holds each value on a line of its own, at
		// another position than the file has for it. Each value still
		// reports once, at its place in the file.
		"flow mapping": {
			input: "{port: 70000, timeout: soon, extra: 1}\n",
			opts:  []niceyaml.DecodeOption{strict},
			want: stringtest.JoinLF(
				"base.yaml: 3 problems",
				"base.yaml:1:8: $.port: expected integer from 0 to 65535, got 70000",
				`base.yaml:1:24: $.timeout: time: invalid duration "soon"`,
				`base.yaml:1:30: $.extra~: unknown field "extra"`,
			),
		},
		"flow mapping below a comment": {
			input: stringtest.LinesLF(
				"# prod",
				"",
				"{tier: mid, port: 70000, timeout: soon}",
			),
			err: errUnknownTier,
			want: stringtest.JoinLF(
				"base.yaml: 3 problems",
				`base.yaml:3:8: $.tier: unknown tier "mid"`,
				"base.yaml:3:19: $.port: expected integer from 0 to 65535, got 70000",
				`base.yaml:3:35: $.timeout: time: invalid duration "soon"`,
			),
		},
		"alias to a duration": {
			input: stringtest.LinesLF(
				"wait: &wait soon",
				"timeout: *wait",
				"port: 70000",
			),
			want: stringtest.JoinLF(
				"base.yaml: 2 problems",
				`base.yaml:2:10: $.timeout: time: invalid duration "soon"`,
				"base.yaml:3:7: $.port: expected integer from 0 to 65535, got 70000",
			),
		},
		// The merged text leaves out the blank line of each input below,
		// so the unmarshaler reads another position than the file has for
		// its value.
		"unmarshaler error with no location": {
			input: own("plain"),
			err:   errBadServer,
			want: stringtest.JoinLF(
				"base.yaml: 3 problems",
				"base.yaml:3:7: $.port: expected integer from 0 to 65535, got 70000",
				"base.yaml:4:6: $.own: bad server",
				`base.yaml:5:10: $.timeout: time: invalid duration "soon"`,
			),
		},
		"unmarshaler error at a position": {
			input: own("position"),
			err:   errBadServer,
			want: stringtest.JoinLF(
				"base.yaml: 3 problems",
				"base.yaml:3:7: $.port: expected integer from 0 to 65535, got 70000",
				"base.yaml:4:6: bad server",
				`base.yaml:5:10: $.timeout: time: invalid duration "soon"`,
			),
		},
		"unmarshaler error at a range": {
			input: own("range"),
			err:   errBadServer,
			want: stringtest.JoinLF(
				"base.yaml: 3 problems",
				"base.yaml:3:7: $.port: expected integer from 0 to 65535, got 70000",
				"base.yaml:4:6: bad server",
				`base.yaml:5:10: $.timeout: time: invalid duration "soon"`,
			),
		},
		// The error names the port, so it stands for the problem the
		// search finds there.
		"unmarshaler error at the path of another problem": {
			input: own("path"),
			err:   errBadServer,
			want: stringtest.JoinLF(
				"base.yaml: 2 problems",
				"base.yaml:3:7: $.port: bad server",
				`base.yaml:5:10: $.timeout: time: invalid duration "soon"`,
			),
		},
		"unmarshaler error from BindValue": {
			input: own("unplaced"),
			err:   errBadServer,
			want: stringtest.JoinLF(
				"base.yaml: 2 problems",
				"base.yaml:3:7: $.port: bad server",
				`base.yaml:5:10: $.timeout: time: invalid duration "soon"`,
			),
		},
		// The error binds at the key of the mapping that lacks the key.
		"unmarshaler error at a path the document leaves out": {
			input: own("absent"),
			err:   errBadServer,
			want: stringtest.JoinLF(
				"base.yaml: 3 problems",
				"base.yaml:3:1: $.absent: bad server",
				"base.yaml:3:7: $.port: expected integer from 0 to 65535, got 70000",
				`base.yaml:5:10: $.timeout: time: invalid duration "soon"`,
			),
		},
		// The panic binds at the first key of the mapping, so it stands
		// for the unknown field the search finds there. The search still
		// reports the duration, which matches ErrDecode beside the panic.
		"unmarshaler that panics": {
			input: stringtest.LinesLF(
				"# prod",
				"",
				"extra: 1",
				"own: panic",
				"timeout: soon",
			),
			opts: []niceyaml.DecodeOption{strict},
			err:  errBadServer,
			want: stringtest.JoinLF(
				"base.yaml: 2 problems",
				"base.yaml:3:1: decoder panicked: bad server",
				`base.yaml:5:10: $.timeout: time: invalid duration "soon"`,
			),
		},
		// An error with no position comes back alone, since nothing tells
		// it apart from a problem the search finds.
		"unmarshaler error at a range no line holds": {
			input: own("far"),
			err:   errBadServer,
			want:  "base.yaml: bad server",
		},
		"unmarshaler error at a path that resolves nowhere": {
			input: own("below"),
			err:   errBadServer,
			want:  "base.yaml: $.port.unit: bad server",
		},
		"unmarshaler error at a path several keys share": {
			input: own("ambiguous"),
			want:  "base.yaml: $.1.limit: limit must be at least 1",
		},
		// The position of the error lies in the other source, so nothing
		// joins it either.
		"unmarshaler error bound to another source": {
			input: own("elsewhere"),
			err:   errBadServer,
			want:  "inner.yaml:1:7: $.port: bad server",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, tc.input, niceyaml.WithName("base.yaml"))

			_, fromLayers := yamltest.Layers(t, doc).Decode[problemTimed](t.Context(), tc.opts...)
			_, fromSource := doc.Source().Decode[problemTimed](t.Context(), tc.opts...)

			for route, err := range map[string]error{"layers": fromLayers, "source": fromSource} {
				require.EqualError(t, err, tc.want, route)
				require.ErrorIs(t, err, niceyaml.ErrDecode, route)

				if tc.err != nil {
					require.ErrorIs(t, err, tc.err, route)
				}
			}

			want := decodeRows(doc, fromSource)
			assert.Equal(t, want, decodeRows(doc, fromLayers))
		})
	}
}

func TestDocument_Decode_Problems_Deep(t *testing.T) {
	t.Parallel()

	// The search reads each level once, so a document nested this deep
	// takes no longer for it.
	const depth = 4000

	_, err := yamltest.FirstDocument(t, deepDocument(depth)).Decode[problemDeep](t.Context())
	require.ErrorIs(t, err, niceyaml.ErrDecode)

	srcErr := yamltest.RequireSourceError(t, err)
	assert.Empty(t, yamltest.ProblemsOf(srcErr))
	assert.Equal(t, "expected integer, got string", srcErr.Message())

	path, ok := srcErr.Path()
	require.True(t, ok)
	assert.Equal(t, "$"+strings.Repeat(".c", depth)+".v", path.String())
}

func TestDocument_Decode_Problems_Many(t *testing.T) {
	t.Parallel()

	// The report holds every problem, and the message lists the first of
	// them and counts the rest.
	const servers = 500

	_, err := yamltest.FirstDocument(t, "servers:\n"+strings.Repeat("  - port: http\n", servers)).
		Decode[problemConfig](t.Context())
	require.ErrorIs(t, err, niceyaml.ErrDecode)

	srcErr := yamltest.RequireSourceError(t, err)
	require.Len(t, yamltest.ProblemsOf(srcErr), servers)

	lines := strings.Split(err.Error(), "\n")
	require.Len(t, lines, niceyaml.ErrorListLimit+2)
	assert.Equal(t, "500 problems", lines[0])
	assert.Equal(t, "2:11: $.servers[0].port: expected integer, got string", lines[1])
	assert.Equal(t, "and 490 more", lines[len(lines)-1])
}

// tier decodes itself from text and reports errUnknownTier for any text
// but "low" and "high".
type tier int

func (t *tier) UnmarshalText(text []byte) error {
	switch string(text) {
	case "low":
		*t = 1
	case "high":
		*t = 2
	default:
		return fmt.Errorf("%w %q", errUnknownTier, text)
	}

	return nil
}

// tierServer holds a value of each kind the decoder returns the error of
// with no token.
type tierServer struct {
	Name    string        `yaml:"name"`
	Timeout time.Duration `yaml:"timeout"`
	Tier    tier          `yaml:"tier"`
}

// aliasedServer is a [tierServer] whose inline field reads the anchor a
// `<<` key names. With the omitempty option beside it, the decoder leaves
// out the entries the `<<` keys bring in.
type aliasedServer struct {
	*StrictBase `yaml:",omitempty,inline,alias"`

	Name string `yaml:"name"`
	Tier tier   `yaml:"tier"`
}

// mirrored decodes itself through a second type with the same fields, so
// the error of a field comes back as the field reported it.
type mirrored struct {
	Name    string       `yaml:"name"`
	Servers []tierServer `yaml:"servers"`
}

func (m *mirrored) UnmarshalYAML(unmarshal func(any) error) error {
	type plain mirrored

	return unmarshal((*plain)(m))
}

// prefixing decodes itself through a second type and puts text of its
// own in front of the error, so no field below it reports that message.
type prefixing struct {
	Server tierServer
}

func (p *prefixing) UnmarshalYAML(unmarshal func(any) error) error {
	err := unmarshal(&p.Server)
	if err != nil {
		return fmt.Errorf("server: %w", err)
	}

	return nil
}

// opaqueValue decodes itself from YAML bytes and rejects any that hold
// the word bad, so its fields mirror nothing in the document.
type opaqueValue struct{}

func (*opaqueValue) UnmarshalYAML(data []byte) error {
	if strings.Contains(string(data), "bad") {
		return errUnmarshal
	}

	return nil
}

// jsonDecoded decodes itself from JSON, which the decoder hands it only
// under niceyaml.WithJSONUnmarshalers.
type jsonDecoded struct {
	V string `yaml:"v"`
}

func (*jsonDecoded) UnmarshalJSON([]byte) error {
	return errUnmarshal
}

// jsonFielded has an UnmarshalJSON method the decoder calls only under
// niceyaml.WithJSONUnmarshalers. Without that option the decoder reads
// its field.
type jsonFielded struct {
	Tier tier `yaml:"tier"`
}

func (*jsonFielded) UnmarshalJSON([]byte) error {
	return nil
}

// tierHolder decodes itself from text and holds a field that decodes
// from text too, which the decoder never reads.
type tierHolder struct {
	Tier tier `yaml:"tier"`
}

func (*tierHolder) UnmarshalText([]byte) error {
	return nil
}

// lockable decodes itself from text unless the value it decodes into is
// locked already, which only a value the caller filled can be.
type lockable struct {
	Text   string
	Locked bool
}

func (l *lockable) UnmarshalText(text []byte) error {
	if l.Locked {
		return errUnmarshal
	}

	l.Text = string(text)

	return nil
}

// rejectsOnce decodes itself by reporting errUnmarshal on the first call
// that the counter in the context of the decode counts, and by panicking
// on every call after it.
type rejectsOnce struct{}

func (*rejectsOnce) UnmarshalYAML(ctx context.Context, _ []byte) error {
	if calls, ok := ctx.Value(panicCallsKey{}).(*atomic.Int32); ok && calls.Add(1) == 1 {
		return errUnmarshal
	}

	panic("decoded twice")
}

// positioned decodes itself from its node and reports an error at the
// position of that node.
type positioned struct{}

func (*positioned) UnmarshalYAML(node ast.Node) error {
	return niceyaml.Invalid(errUnmarshal, niceyaml.AtPosition(position.NewFromToken(node.GetToken())))
}

// spanError returns errSpan at the path of the to, which reads from the
// value that reports it.
func spanError() error {
	return niceyaml.Invalid(errSpan, niceyaml.AtPath(paths.Current().Child("to")))
}

// span decodes itself through a second type with the same fields and
// reports a to below its from, as spanError returns it.
type span struct {
	From int `yaml:"from"`
	To   int `yaml:"to"`
}

func (s *span) UnmarshalYAML(unmarshal func(any) error) error {
	type plain span

	err := unmarshal((*plain)(s))
	if err != nil {
		return err
	}

	if s.To < s.From {
		return spanError()
	}

	return nil
}

// spanNode decodes itself from its node and reports spanError for every
// node.
type spanNode struct{}

func (*spanNode) UnmarshalYAML(ast.Node) error {
	return spanError()
}

// spanBytes decodes itself from YAML bytes and reports spanError for
// every node.
type spanBytes struct{}

func (*spanBytes) UnmarshalYAML(context.Context, []byte) error {
	return spanError()
}

// spanMirror decodes itself through a second type with the same fields,
// so the error of its span comes back as the span reported it.
type spanMirror struct {
	Name string `yaml:"name"`
	Span span   `yaml:"span"`
}

func (m *spanMirror) UnmarshalYAML(unmarshal func(any) error) error {
	type plain spanMirror

	return unmarshal((*plain)(m))
}

// spanWrapper decodes itself as [spanMirror] does and puts text of its
// own in front of the error of its span.
type spanWrapper struct {
	Span span `yaml:"span"`
}

func (w *spanWrapper) UnmarshalYAML(unmarshal func(any) error) error {
	type plain spanWrapper

	err := unmarshal((*plain)(w))
	if err != nil {
		return fmt.Errorf("wrapper: %w", err)
	}

	return nil
}

// spanRebaser decodes itself as [spanMirror] does and puts the error of
// its span under the path of that field itself.
type spanRebaser struct {
	Span span `yaml:"span"`
}

func (r *spanRebaser) UnmarshalYAML(unmarshal func(any) error) error {
	type plain spanRebaser

	//nolint:wrapcheck // The test inspects the error as it is.
	return niceyaml.Rebase(unmarshal((*plain)(r)), paths.Current().Child("span"))
}

// tierNamer decodes itself through a second type with the same fields
// and names its tier in the error of that tier, at a path of its own.
type tierNamer struct {
	Tier tier `yaml:"tier"`
}

func (n *tierNamer) UnmarshalYAML(unmarshal func(any) error) error {
	type plain tierNamer

	return niceyaml.Invalid(unmarshal((*plain)(n)), niceyaml.AtPath(paths.Current().Child("tier")))
}

// reportKey is the context key of the error a [reporting] returns.
type reportKey struct{}

// reporting decodes itself from any value and reports the error the
// context of the decode holds under reportKey.
type reporting struct{}

func (*reporting) UnmarshalYAML(ctx context.Context, _ func(any) error) error {
	err, ok := ctx.Value(reportKey{}).(error)
	if !ok {
		return nil
	}

	return err
}

// rebasing decodes itself through a second type with the same fields
// and puts the error of its item under the path of that field itself.
type rebasing struct {
	Item reporting `yaml:"item"`
}

func (r *rebasing) UnmarshalYAML(unmarshal func(any) error) error {
	type plain rebasing

	//nolint:wrapcheck // The test inspects the error as it is.
	return niceyaml.Rebase(unmarshal((*plain)(r)), paths.Current().Child("item"))
}

// optionDecoded has no method of its own. A function from
// niceyaml.WithCustomUnmarshaler decodes it.
type optionDecoded struct{}

// registeredDecoded has no method of its own. A function from
// yaml.RegisterCustomUnmarshaler decodes it in every decode of the
// program.
type registeredDecoded struct{}

// cancelKey is the context key of the function canceling calls.
type cancelKey struct{}

// canceling decodes itself with the context of the decode, which it ends
// before it reports errUnmarshal.
type canceling struct{}

func (*canceling) UnmarshalYAML(ctx context.Context, _ []byte) error {
	cancel, ok := ctx.Value(cancelKey{}).(context.CancelFunc)
	if ok {
		cancel()
	}

	return errUnmarshal
}

// rejectionServer is an element of [rejectionConfig].
type rejectionServer struct {
	Host  string `yaml:"host"`
	Port  int    `yaml:"port"`
	Small int8   `yaml:"small"`
	Wide  uint16 `yaml:"wide"`
}

// rejectionConfig holds a field of each kind the go-yaml decoder rejects
// a value for with a token of the source.
type rejectionConfig struct {
	When    time.Time         `yaml:"when"`
	Labels  map[string]string `yaml:"labels"`
	Counts  map[int]int       `yaml:"counts"`
	Name    string            `yaml:"name"`
	Servers []rejectionServer `yaml:"servers"`
	One     rejectionServer   `yaml:"one"`
	Top     int               `yaml:"top"`
	Ratio   float64           `yaml:"ratio"`
	Wait    time.Duration     `yaml:"wait"`
	On      bool              `yaml:"on"`
}

// requireNamedPath returns the path srcErr carries, after checking that
// want, the message of the error, names that path behind its position.
func requireNamedPath(t *testing.T, srcErr *niceyaml.SourceError, want string) paths.Path {
	t.Helper()

	path, ok := srcErr.Path()
	require.True(t, ok, "the error carries no path")
	assert.Contains(t, want, ": "+path.String()+": ")

	return path
}

func TestDocument_Decode_Rejection(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		want  string
		opts  []niceyaml.DecodeOption
	}{
		"scalar in an element": {
			input: stringtest.LinesLF(
				"servers:",
				"  - host: h",
				"    port: abc",
			),
			want: "3:11: $.servers[0].port: expected integer, got string",
		},
		"block mapping for an integer": {
			input: stringtest.LinesLF(
				"top:",
				"  a: 1",
				"  b: 2",
			),
			want: "1:1: $.top: expected integer, got mapping",
		},
		"block mapping for a string in a map": {
			input: stringtest.LinesLF(
				"labels:",
				"  a:",
				"    b: 1",
			),
			want: "2:3: $.labels.a: expected string, got mapping",
		},
		"block sequence for an integer": {
			input: stringtest.LinesLF(
				"top:",
				"  - 1",
				"  - 2",
			),
			want: "1:1: $.top: expected integer, got sequence",
		},
		"flow sequence for a string": {
			input: "name: [a, b]\n",
			want:  "1:1: $.name: expected string, got sequence",
		},
		"flow mapping for an integer": {
			input: "top: {a: 1}\n",
			want:  "1:1: $.top: expected integer, got mapping",
		},
		"empty flow sequence for an integer": {
			input: "top: []\n",
			want:  "1:1: $.top: expected integer, got sequence",
		},
		"string for a sequence": {
			input: "servers: abc\n",
			want:  "1:10: $.servers: expected sequence, got string",
		},
		"string for a mapping": {
			input: "one: abc\n",
			want:  "1:6: $.one: expected mapping, got string",
		},
		"block sequence for a mapping": {
			input: stringtest.LinesLF("one:", "  - a"),
			want:  "1:1: $.one: expected mapping, got sequence",
		},
		"sequence element for a mapping": {
			input: stringtest.LinesLF("servers:", "  - - a"),
			want:  "2:3: $.servers[0]: expected mapping, got sequence",
		},
		"integer for a sequence": {
			input: "servers: 5\n",
			want:  "1:10: $.servers: expected sequence, got integer",
		},
		"boolean for a mapping": {
			input: "one: true\n",
			want:  "1:6: $.one: expected mapping, got boolean",
		},
		"block scalar for a mapping": {
			input: stringtest.LinesLF("one: |", "  abc"),
			want:  "1:6: $.one: expected mapping, got string",
		},
		"anchored scalar": {
			input: "top: &t abc\n",
			want:  "1:9: $.top: expected integer, got string",
		},
		"tagged scalar": {
			input: "top: !!str abc\n",
			want:  "1:12: $.top: expected integer, got string",
		},
		"value an alias reads": {
			input: stringtest.LinesLF(
				"defs:",
				"  p: &p abc",
				"servers:",
				"  - port: *p",
			),
			want: "2:9: $.defs.p: expected integer, got string",
		},
		"value a merge key brings in": {
			input: stringtest.LinesLF(
				"defs: &d",
				"  port: abc",
				"servers:",
				"  - <<: *d",
				"    host: h",
			),
			want: "2:9: $.defs.port: expected integer, got string",
		},
		"map key": {
			input: stringtest.LinesLF("counts:", "  x: 1"),
			want:  "2:3: $.counts.x~: expected integer, got string",
		},
		"explicit map key": {
			input: stringtest.LinesLF(
				"counts:",
				"  ? x",
				"  : 1",
			),
			want: "2:5: $.counts.x~: expected integer, got string",
		},
		"map value": {
			input: stringtest.LinesLF("counts:", "  1: x"),
			want:  "2:6: $.counts.1: expected integer, got string",
		},
		"key a path quotes": {
			input: stringtest.LinesLF("labels:", `  "a.b": [1]`),
			want:  "2:3: $.labels.'a.b': expected string, got sequence",
		},
		"overflow of a signed integer": {
			input: stringtest.LinesLF("servers:", "  - small: 300"),
			want:  "2:12: $.servers[0].small: expected integer from -128 to 127, got 300",
		},
		"negative for an unsigned integer": {
			input: stringtest.LinesLF("servers:", "  - wide: -1"),
			want:  "2:11: $.servers[0].wide: expected integer from 0 to 65535, got -1",
		},
		"overflow of an unsigned integer": {
			input: "one: {wide: 70000}\n",
			want:  "1:13: $.one.wide: expected integer from 0 to 65535, got 70000",
		},
		"float past an integer": {
			input: "one: {small: 1e10}\n",
			want:  "1:14: $.one.small: expected integer from -128 to 127, got 1e10",
		},
		"boolean for an integer": {
			input: "top: true\n",
			want:  "1:6: $.top: expected integer, got boolean",
		},
		"integer for a boolean": {
			input: "on: 5\n",
			want:  "1:5: $.on: expected boolean, got integer",
		},
		"mapping for a float": {
			input: "ratio: {a: 1}\n",
			want:  "1:1: $.ratio: expected float, got mapping",
		},
		"sequence for a timestamp": {
			input: "when: [1]\n",
			want:  "1:1: $.when: expected timestamp, got sequence",
		},
		"sequence for a duration": {
			input: "wait: [1]\n",
			want:  "1:1: $.wait: expected duration, got sequence",
		},
		"tagged null for an integer": {
			input: "top: !!null x\n",
			want:  "1:13: $.top: expected integer, got null",
		},
		"binary for an integer": {
			input: "top: !!binary YWJj\n",
			want:  "1:15: $.top: expected integer, got binary",
		},
		"timestamp for an integer": {
			input: "top: !!timestamp 2020-01-01\n",
			want:  "1:18: $.top: expected integer, got timestamp",
		},
		"ordered mapping for an integer": {
			input: "top: {a: 1}\n",
			opts:  []niceyaml.DecodeOption{niceyaml.WithYAMLOrderedMaps(true)},
			want:  "1:1: $.top: expected integer, got mapping",
		},
		"unknown field": {
			input: "name: x\nfoo: 1\n",
			opts:  []niceyaml.DecodeOption{niceyaml.WithDisallowUnknownFields(true)},
			want:  `2:1: $.foo~: unknown field "foo"`,
		},
		"unknown field in an element": {
			input: stringtest.LinesLF("servers:", "  - zzz: 1"),
			opts:  []niceyaml.DecodeOption{niceyaml.WithDisallowUnknownFields(true)},
			want:  `2:5: $.servers[0].zzz~: unknown field "zzz"`,
		},
		"unknown field a merge key brings in": {
			input: stringtest.LinesLF(
				"labels: &d",
				"  zzz: 1",
				"one:",
				"  <<: *d",
			),
			opts: []niceyaml.DecodeOption{niceyaml.WithDisallowUnknownFields(true)},
			want: `2:3: $.labels.zzz~: unknown field "zzz"`,
		},
		"alias with no anchor": {
			input: "top: *nope\n",
			want:  `1:6: $.top: could not find alias "nope"`,
		},
		"value under renamed anchors": {
			input: stringtest.LinesLF(
				"a: &x 1",
				"b: &x 2",
				"top: abc",
			),
			want: "3:6: $.top: expected integer, got string",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dd := yamltest.FirstDocument(t, tc.input)

			var v rejectionConfig

			err := dd.DecodeInto(t.Context(), &v, tc.opts...)
			require.EqualError(t, err, tc.want)
			require.ErrorIs(t, err, niceyaml.ErrDecode)

			srcErr := yamltest.RequireSourceError(t, err)
			require.NoError(t, srcErr.Unresolved())

			path := requireNamedPath(t, srcErr, tc.want)

			// The message names no Go type or field.
			for _, word := range []string{"unmarshal", "Go ", "struct", "interface"} {
				assert.NotContains(t, srcErr.Message(), word)
			}

			// The rejection binds where an error at its path binds, as one
			// from a validator does.
			atPath := yamltest.RequireSourceError(t, dd.Bind(niceyaml.NewError("at the path", niceyaml.AtPath(path))))
			require.NoError(t, atPath.Unresolved())

			_, near := atPath.Nearest()
			require.False(t, near, "the path selects nothing")

			want, ok := atPath.Range()
			require.True(t, ok)

			got, ok := srcErr.Range()
			require.True(t, ok)
			assert.Equal(t, want, got)

			// The position of the error lies at the path: on the key for
			// the path of a key, and at or inside the value for any other.
			pos, ok := srcErr.Position()
			require.True(t, ok)

			under, ok := dd.PathAt(pos)
			require.True(t, ok)

			if strings.HasSuffix(path.String(), "~") {
				assert.Equal(t, path, under)

				return
			}

			_, ok = under.CutPrefix(path)
			assert.True(t, ok, "position %s lies at %s", pos, under)
		})
	}
}

func TestDocument_Decode_Rejection_GoTypes(t *testing.T) {
	t.Parallel()

	// An inline field that takes the value of the anchor a `<<` key names.
	// The decoder assigns the value as it decoded the anchor, and rejects
	// one of any Go type but the type of the field.
	type aliased struct {
		*StrictBase `yaml:",inline,alias"`

		B int `yaml:"b"`
	}

	type other struct {
		A int `yaml:"a"`
	}

	merge := stringtest.LinesLF(
		"defs: &d",
		"  a: 1",
		"v:",
		"  <<: *d",
		"  b: 1",
	)

	tcs := map[string]struct {
		decode func(context.Context, *niceyaml.Node) error
		input  string
		want   string
	}{
		"channel": {
			decode: decodeAs[struct {
				V chan int `yaml:"v"`
			}](),
			input: "v: 5\n",
			want:  "1:4: $.v: expected no value, got integer",
		},
		"pointer to a channel": {
			decode: decodeAs[struct {
				V *chan int `yaml:"v"`
			}](),
			input: "v: {a: 1}\n",
			want:  "1:1: $.v: expected no value, got mapping",
		},
		"function": {
			decode: decodeAs[struct {
				V func() `yaml:"v"`
			}](),
			input: "v: abc\n",
			want:  "1:4: $.v: expected no value, got string",
		},
		"complex number": {
			decode: decodeAs[struct {
				V complex128 `yaml:"v"`
			}](),
			input: "v: 5\n",
			want:  "1:4: $.v: expected no value, got integer",
		},
		"element of an array of complex numbers": {
			decode: decodeAs[struct {
				V [2]complex64 `yaml:"v"`
			}](),
			input: "v: [1.5]\n",
			want:  "1:5: $.v[0]: expected no value, got float",
		},
		"value of a map of functions": {
			decode: decodeAs[struct {
				V map[string]func() `yaml:"v"`
			}](),
			input: "v: {a: true}\n",
			want:  "1:8: $.v.a: expected no value, got boolean",
		},
		"unsafe pointer": {
			decode: decodeAs[struct {
				V unsafe.Pointer `yaml:"v"`
			}](),
			input: "v: 5\n",
			want:  "1:4: $.v: expected no value, got integer",
		},
		// The decoder fills one pointer around a value and no more.
		"pointer to a pointer": {
			decode: decodeAs[struct {
				V **int `yaml:"v"`
			}](),
			input: "v: 5\n",
			want:  "1:4: $.v: expected integer, got integer of another type",
		},
		"anchor read into an interface": {
			decode: decodeAs[struct {
				Defs any     `yaml:"defs"`
				V    aliased `yaml:"v"`
			}](),
			input: merge,
			want:  "1:1: $.defs: expected mapping, got value of another type",
		},
		"anchor read into another struct": {
			decode: decodeAs[struct {
				Defs *other  `yaml:"defs"`
				V    aliased `yaml:"v"`
			}](),
			input: merge,
			want:  "1:1: $.defs: expected mapping, got mapping of another type",
		},
		"anchor read into a map": {
			decode: decodeAs[struct {
				Defs map[string]int `yaml:"defs"`
				V    aliased        `yaml:"v"`
			}](),
			input: merge,
			want:  "1:1: $.defs: expected mapping, got mapping of another type",
		},
		"anchor read into no field": {
			decode: decodeAs[struct {
				V aliased `yaml:"v"`
			}](),
			input: merge,
			want:  "1:1: $.defs: expected mapping, got mapping of another type",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dd := yamltest.FirstDocument(t, tc.input)

			err := tc.decode(t.Context(), dd)
			require.EqualError(t, err, tc.want)
			require.ErrorIs(t, err, niceyaml.ErrDecode)

			// The decoder reports the two Go types it compared, and the
			// message spells neither of them.
			var typeErr *yaml.TypeError

			require.ErrorAs(t, err, &typeErr)

			srcErr := yamltest.RequireSourceError(t, err)

			for _, typ := range []reflect.Type{typeErr.DstType, typeErr.SrcType} {
				// The kinds integer, string, and boolean hold the names of
				// the Go types int, string, and bool.
				if spelled := typ.String(); !slices.Contains([]string{"int", "string", "bool"}, spelled) {
					assert.NotContains(t, srcErr.Message(), spelled)
				}
			}

			for _, word := range []string{"chan", "func", "complex", "unsafe", "interface", "struct", "map[", "*", "niceyaml"} {
				assert.NotContains(t, srcErr.Message(), word)
			}
		})
	}
}

func TestDocument_Decode_Rejection_GoYAMLError(t *testing.T) {
	t.Parallel()

	// The message reads in YAML terms, and [errors.As] still finds the
	// error the decoder returned, with its Go types.
	t.Run("type mismatch", func(t *testing.T) {
		t.Parallel()

		var v rejectionConfig

		err := yamltest.FirstDocument(t, "top: abc\n").DecodeInto(t.Context(), &v)
		require.EqualError(t, err, "1:6: $.top: expected integer, got string")

		got, ok := errors.AsType[*yaml.TypeError](err)
		require.True(t, ok)
		assert.Equal(t, reflect.TypeFor[int](), got.DstType)
		assert.Equal(t, reflect.TypeFor[string](), got.SrcType)
	})

	t.Run("overflow", func(t *testing.T) {
		t.Parallel()

		var v rejectionConfig

		err := yamltest.FirstDocument(t, "one: {small: 300}\n").DecodeInto(t.Context(), &v)
		require.EqualError(t, err, "1:14: $.one.small: expected integer from -128 to 127, got 300")

		got, ok := errors.AsType[*yaml.OverflowError](err)
		require.True(t, ok)
		assert.Equal(t, reflect.TypeFor[int8](), got.DstType)
		assert.Equal(t, "300", got.SrcNum)
	})

	t.Run("unexpected node", func(t *testing.T) {
		t.Parallel()

		var v rejectionConfig

		err := yamltest.FirstDocument(t, "servers: abc\n").DecodeInto(t.Context(), &v)
		require.EqualError(t, err, "1:10: $.servers: expected sequence, got string")

		got, ok := errors.AsType[*yaml.UnexpectedNodeTypeError](err)
		require.True(t, ok)
		assert.Equal(t, ast.SequenceType, got.Expected)
		assert.Equal(t, ast.StringType, got.Actual)
	})

	t.Run("unknown field", func(t *testing.T) {
		t.Parallel()

		var v rejectionConfig

		err := yamltest.FirstDocument(t, "foo: 1\n").
			DecodeInto(t.Context(), &v, niceyaml.WithDisallowUnknownFields(true))
		require.EqualError(t, err, `1:1: $.foo~: unknown field "foo"`)

		got, ok := errors.AsType[*yaml.UnknownFieldError](err)
		require.True(t, ok)
		assert.Equal(t, `unknown field "foo"`, got.GetMessage())
	})
}

// requireSentinels checks that err matches each sentinel in want, and
// none of the other sentinels a decode or the parse reports a problem
// with.
func requireSentinels(t *testing.T, err error, want ...error) {
	t.Helper()

	sentinels := []error{
		niceyaml.ErrDecode,
		niceyaml.ErrSyntax,
		niceyaml.ErrUnknownField,
		niceyaml.ErrWrongKind,
		niceyaml.ErrOverflow,
		niceyaml.ErrOutOfRange,
	}

	for _, sentinel := range sentinels {
		if slices.Contains(want, sentinel) {
			require.ErrorIs(t, err, sentinel)
		} else {
			assert.NotErrorIs(t, err, sentinel)
		}
	}
}

func TestDocument_Decode_Rejection_Label(t *testing.T) {
	t.Parallel()

	// The target holds a field of each kind whose rejection a decode
	// labels, and of each kind whose error it gives no label.
	type target struct {
		Addr   netip.Addr               `yaml:"addr"`
		Bare   bareReparsingUnmarshaler `yaml:"bare"`
		Deep   **int                    `yaml:"deep"`
		Self   opaqueValue              `yaml:"self"`
		Option optionDecoded            `yaml:"option"`
		One    rejectionServer          `yaml:"one"`
		Wait   time.Duration            `yaml:"wait"`
		Ratio  float64                  `yaml:"ratio"`
		Top    int                      `yaml:"top"`
		Tier   tier                     `yaml:"tier"`
	}

	strict := niceyaml.WithDisallowUnknownFields(true)

	// The function decodes the value as an int8 and puts text of its own
	// in front of the rejection.
	wrapping := niceyaml.WithCustomUnmarshaler(
		func(_ context.Context, _ *optionDecoded, decode func(any) error) error {
			var small int8

			err := decode(&small)
			if err != nil {
				return fmt.Errorf("option: %w", err)
			}

			return nil
		},
	)

	refs := niceyaml.WithReferences(niceyaml.NewSourceFromString("base: &base {small: 300}\n"))

	tcs := map[string]struct {
		input string
		// The message of the one problem the decode reports.
		want string
		// The sentinels that problem matches.
		err    []error
		opts   []niceyaml.DecodeOption
		source []niceyaml.SourceOption
	}{
		"unknown field": {
			input: "other: 1\n",
			opts:  []niceyaml.DecodeOption{strict},
			want:  `unknown field "other"`,
			err:   []error{niceyaml.ErrDecode, niceyaml.ErrUnknownField},
		},
		"scalar for a mapping": {
			input: "one: abc\n",
			want:  "expected mapping, got string",
			err:   []error{niceyaml.ErrDecode, niceyaml.ErrWrongKind},
		},
		"mapping for a scalar": {
			input: "top: {a: 1}\n",
			want:  "expected integer, got mapping",
			err:   []error{niceyaml.ErrDecode, niceyaml.ErrWrongKind},
		},
		// The decode words this rejection itself, so its chain holds no
		// go-yaml error to name its kind.
		"sequence for a value that decodes from text": {
			input: "addr: [1]\n",
			want:  "expected string, got sequence",
			err:   []error{niceyaml.ErrDecode, niceyaml.ErrWrongKind},
		},
		// The decoder reports a type error for a number no float64 holds.
		"number too large for a float": {
			input: "ratio: 1e400\n",
			want:  "expected float, got string",
			err:   []error{niceyaml.ErrDecode, niceyaml.ErrWrongKind},
		},
		"value of another type": {
			input: "deep: 1\n",
			want:  "expected integer, got integer of another type",
			err:   []error{niceyaml.ErrDecode, niceyaml.ErrWrongKind},
		},
		"number past the range of an int8": {
			input: "one: {small: 300}\n",
			want:  "expected integer from -128 to 127, got 300",
			err:   []error{niceyaml.ErrDecode, niceyaml.ErrOverflow},
		},
		// The decoder reports an overflow for an infinity in an integer.
		"infinity for an integer": {
			input: "one: {small: .inf}\n",
			want:  "expected integer from -128 to 127, got +Inf",
			err:   []error{niceyaml.ErrDecode, niceyaml.ErrOverflow},
		},
		"number an alias reads from a reference document": {
			input:  "one: *base\n",
			source: []niceyaml.SourceOption{refs},
			want:   "expected integer from -128 to 127, got 300",
			err:    []error{niceyaml.ErrDecode, niceyaml.ErrOverflow},
		},
		"rejection a custom unmarshaler wraps": {
			input: "option: 300\n",
			opts:  []niceyaml.DecodeOption{wrapping},
			want:  "option: expected integer from -128 to 127, got 300",
			err:   []error{niceyaml.ErrDecode, niceyaml.ErrOverflow},
		},
		"error of an UnmarshalYAML method": {
			input: "self: bad\n",
			want:  "unmarshaler rejected the value",
			err:   []error{niceyaml.ErrDecode},
		},
		"error of an UnmarshalText method": {
			input: "tier: mid\n",
			want:  `unknown tier "mid"`,
			err:   []error{niceyaml.ErrDecode},
		},
		"duration that does not parse": {
			input: "wait: soon\n",
			want:  `time: invalid duration "soon"`,
			err:   []error{niceyaml.ErrDecode},
		},
		"alias with no anchor": {
			input: "top: *nope\n",
			want:  `could not find alias "nope"`,
			err:   []error{niceyaml.ErrDecode},
		},
		"syntax error": {
			input: "top: [1\n",
			want:  "sequence end token ']' not found",
			err:   []error{niceyaml.ErrSyntax},
		},
		"duplicate key": {
			input: "top: 1\ntop: 2\n",
			want:  `mapping key "top" already defined at [1:1]`,
			err:   []error{niceyaml.ErrSyntax},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := niceyaml.NewSourceFromString(tc.input, tc.source...).Decode[target](t.Context(), tc.opts...)
			require.Error(t, err)

			problems := slices.Collect(niceyaml.Problems(err))
			require.Len(t, problems, 1)
			assert.Equal(t, tc.want, problems[0].Message())
			requireSentinels(t, problems[0].Err, tc.err...)
		})
	}

	// The method returns the error of a go-yaml decode of its bytes, with
	// go-yaml's text. The message decides the label, so the go-yaml error
	// in the chain gives the problem none.
	t.Run("go-yaml error of an UnmarshalYAML method", func(t *testing.T) {
		t.Parallel()

		_, err := niceyaml.NewSourceFromString("bare: {name: abc}\n").Decode[target](t.Context())
		require.Error(t, err)

		problems := slices.Collect(niceyaml.Problems(err))
		require.Len(t, problems, 1)
		requireSentinels(t, problems[0].Err, niceyaml.ErrDecode)
		require.ErrorAs(t, problems[0].Err, new(*yaml.UnexpectedNodeTypeError))
	})

	// A summary matches each label that one of its problems matches, and
	// a problem matches no label but its own.
	t.Run("several problems", func(t *testing.T) {
		t.Parallel()

		input := stringtest.LinesLF(
			"other: 1",
			"top: abc",
			"one: {small: 300}",
			"addr: [1]",
			"wait: soon",
		)

		_, err := niceyaml.NewSourceFromString(input).Decode[target](t.Context(), strict)
		require.Error(t, err)
		requireSentinels(t, err,
			niceyaml.ErrDecode, niceyaml.ErrUnknownField, niceyaml.ErrWrongKind, niceyaml.ErrOverflow)

		want := [][]error{
			{niceyaml.ErrDecode, niceyaml.ErrUnknownField},
			{niceyaml.ErrDecode, niceyaml.ErrWrongKind},
			{niceyaml.ErrDecode, niceyaml.ErrOverflow},
			{niceyaml.ErrDecode, niceyaml.ErrWrongKind},
			{niceyaml.ErrDecode},
		}

		problems := slices.Collect(niceyaml.Problems(err))
		require.Len(t, problems, len(want))

		for i, problem := range problems {
			requireSentinels(t, problem.Err, want[i]...)
		}
	})
}

func TestDocument_Decode_Rejection_Scoped(t *testing.T) {
	t.Parallel()

	doc := yamltest.FirstDocumentWithPath(t, stringtest.Input(`
		defs: &d
		  port: abc
		servers:
		  - host: h
		    port: xyz
		  - <<: *d
	`), "s.yaml")

	tcs := map[string]struct {
		scope string
		want  string
	}{
		// The path starts at `$`, so the scope of the Node does not go
		// in front of it a second time.
		"value inside the scope": {
			scope: "$.servers[0]",
			want:  "s.yaml:5:11: $.servers[0].port: expected integer, got string",
		},
		// The merge key brings the value in from outside the scope,
		// and the path names the place where the anchor defines it.
		"value a merge key brings in from outside the scope": {
			scope: "$.servers[1]",
			want:  "s.yaml:2:9: $.defs.port: expected integer, got string",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			scoped := yamltest.At(t, doc, paths.MustParse(tc.scope))

			_, err := scoped.Decode[rejectionServer](t.Context())
			require.EqualError(t, err, tc.want)
			require.ErrorIs(t, err, niceyaml.ErrDecode)

			srcErr := yamltest.RequireSourceError(t, err)

			path := requireNamedPath(t, srcErr, tc.want)

			// The error stays bound to the Node that decoded, and its
			// path resolves from the root of the document.
			assert.Same(t, scoped, yamltest.BoundNode(t, srcErr))
			assert.Same(t, doc, yamltest.BoundDocument(t, srcErr))

			ranges, err := yamltest.BoundDocument(t, srcErr).Ranges(path)
			require.NoError(t, err)
			require.Len(t, ranges, 1)

			pos, ok := srcErr.Position()
			require.True(t, ok)
			assert.Equal(t, ranges[0].Start, pos)
		})
	}
}

func TestDocument_Decode_Rejection_Unselected(t *testing.T) {
	t.Parallel()

	t.Run("path that selects another entry", func(t *testing.T) {
		t.Parallel()

		// The document may hold the key twice, and a decode into a map
		// reads every entry, so the decoder rejects the value of the
		// second. A path through the key selects the third, so the error
		// binds at the token the decoder reported, and the path names
		// the value in the message.
		doc := yamltest.FirstDocument(t, stringtest.LinesLF(
			"name: 1",
			"name: y",
			"name: 3",
		), niceyaml.WithAllowDuplicateKeys(true))

		_, err := doc.Decode[map[string]int](t.Context())
		require.EqualError(t, err, "2:7: $.name: expected integer, got string")
		require.ErrorIs(t, err, niceyaml.ErrDecode)

		srcErr := yamltest.RequireSourceError(t, err)
		require.NoError(t, srcErr.Unresolved())

		path, ok := srcErr.Path()
		require.True(t, ok)
		assert.Equal(t, "$.name", path.String())

		pos, ok := srcErr.Position()
		require.True(t, ok)
		assert.Equal(t, position.New(1, 6), pos)

		ranges, err := doc.Ranges(path)
		require.NoError(t, err)
		require.Len(t, ranges, 1)
		assert.Equal(t, position.New(2, 6), ranges[0].Start)

		_, ok = doc.PathAt(pos)
		assert.False(t, ok, "a path selects the second entry")
	})

	t.Run("key with no name", func(t *testing.T) {
		t.Parallel()

		// An alias key that names no anchor has no name, so no path
		// spells its entry and the rejection carries the position alone.
		doc := yamltest.FirstDocument(t, "b: 2\n*nope : 1\n")

		_, err := doc.Decode[map[string]int](t.Context())
		require.EqualError(t, err, `2:2: could not find alias "nope"`)
		require.ErrorIs(t, err, niceyaml.ErrDecode)

		srcErr := yamltest.RequireSourceError(t, err)

		_, ok := srcErr.Path()
		assert.False(t, ok)

		pos, ok := srcErr.Position()
		require.True(t, ok)
		assert.Equal(t, position.New(1, 1), pos)
	})
}

func TestDocument_Decode_UnmarshalerError(t *testing.T) {
	t.Parallel()

	// A server whose tier names no tier.
	const midTier = "name: api\ntier: mid\n"

	// A list of one span whose to lies below its from.
	oneSpan := stringtest.LinesLF(
		"spans:",
		"  - from: 9",
		"    to: 3",
	)

	t.Run("binds at the value that reported it", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			decode func(ctx context.Context, dd *niceyaml.Node) error
			input  string
			want   string
			// The path of the node that decodes, for a case that decodes
			// below the root.
			scope paths.Path
		}{
			"duration field": {
				input:  "name: api\ntimeout: soon\n",
				decode: decodeAs[tierServer](),
				want:   `2:10: $.timeout: time: invalid duration "soon"`,
			},
			"text unmarshaler in a sequence of structs": {
				input: stringtest.LinesLF(
					"- name: a",
					"  tier: low",
					"- name: b",
					"  tier: mid",
				),
				decode: decodeAs[[]tierServer](),
				want:   `4:9: $[1].tier: unknown tier "mid"`,
			},
			"pointer element": {
				input: "tiers: [low, mid]\n",
				decode: decodeAs[struct {
					Tiers []*tier `yaml:"tiers"`
				}](),
				want: `1:14: $.tiers[1]: unknown tier "mid"`,
			},
			"map key": {
				input: stringtest.LinesLF(
					"hosts:",
					"  10.0.0.1: a",
					"  nope: b",
				),
				decode: decodeAs[struct {
					Hosts map[netip.Addr]string `yaml:"hosts"`
				}](),
				want: `3:3: $.hosts.nope~: ParseAddr("nope"): unable to parse IP`,
			},
			"map value": {
				input:  "a: low\nb: mid\n",
				decode: decodeAs[map[string]tier](),
				want:   `2:4: $.b: unknown tier "mid"`,
			},
			// The path through a merge key resolves to the entry of the
			// mapping the key brings in.
			"map value a merge key brings in": {
				input: stringtest.LinesLF(
					"base: &base",
					"  timeout: soon",
					"servers:",
					"  api:",
					"    <<: *base",
				),
				decode: decodeAs[struct {
					Servers map[string]tierServer `yaml:"servers"`
				}](),
				want: `2:12: $.servers.api.timeout: time: invalid duration "soon"`,
			},
			"map entry a merge key brings in": {
				input: stringtest.LinesLF(
					"base: &base",
					"  b: mid",
					"tiers:",
					"  a: low",
					"  <<: *base",
				),
				decode: decodeAs[struct {
					Tiers map[string]tier `yaml:"tiers"`
				}](),
				want: `2:6: $.tiers.b: unknown tier "mid"`,
			},
			// The path of a value an alias holds points at the alias.
			"alias": {
				input: stringtest.LinesLF("label: &label mid", "tiers: [low, *label]"),
				decode: decodeAs[struct {
					Label string `yaml:"label"`
					Tiers []tier `yaml:"tiers"`
				}](),
				want: `2:14: $.tiers[1]: unknown tier "mid"`,
			},
			"inline struct": {
				input: stringtest.LinesLF(
					"id: 1",
					"name: api",
					"tier: mid",
				),
				decode: decodeAs[struct {
					Server tierServer `yaml:",inline"`
					ID     int        `yaml:"id"`
				}](),
				want: `3:7: $.tier: unknown tier "mid"`,
			},
			// The decoder reads no field of a struct from a mapping that
			// holds a key it does not read as a string, so the tier under s
			// reported nothing.
			"field of a mapping with a key of another kind": {
				input: "s: {1: a, tier: mid}\nt: mid\n",
				decode: decodeAs[struct {
					S tierServer `yaml:"s"`
					T tier       `yaml:"t"`
				}](),
				want: `2:4: $.t: unknown tier "mid"`,
			},
			// The decoder gathers nothing from a merge source that holds a
			// key it does not read as a string.
			"field a dropped merge source holds": {
				input: stringtest.LinesLF(
					"base: &base {1: a, tier: mid}",
					"s: {<<: *base, name: api}",
					"t: mid",
				),
				decode: decodeAs[struct {
					Base any        `yaml:"base"`
					S    tierServer `yaml:"s"`
					T    tier       `yaml:"t"`
				}](),
				want: `3:4: $.t: unknown tier "mid"`,
			},
			// A struct whose inline field reads the anchor of a merge key
			// reads none of the entries the merge key brings in. That field
			// takes the value the decoder holds for the anchor, so base
			// decodes into its type.
			"field a merge brings in to a struct that ignores merges": {
				input: stringtest.LinesLF(
					"base: &base {tier: mid}",
					"s: {<<: *base, name: api}",
					"t: mid",
				),
				decode: decodeAs[struct {
					Base *StrictBase   `yaml:"base"`
					S    aliasedServer `yaml:"s"`
					T    tier          `yaml:"t"`
				}](),
				want: `3:4: $.t: unknown tier "mid"`,
			},
			"inline map": {
				input: "id: low\nextra: mid\n",
				decode: decodeAs[struct {
					Tiers map[string]tier `yaml:",inline"`
					ID    string          `yaml:"id"`
				}](),
				want: `2:8: $.extra: unknown tier "mid"`,
			},
			// The decoder reads the fields of a struct in the order the
			// struct declares them, so the first field that fails reports
			// the error, wherever the document puts it. The search for the
			// other problems of the decode reads no opaqueValue, so the
			// error comes back alone.
			"first field in struct order": {
				input: "value: bad\ntimeout: soon\n",
				decode: decodeAs[struct {
					Timeout time.Duration `yaml:"timeout"`
					Value   opaqueValue   `yaml:"value"`
				}](),
				want: `2:10: $.timeout: time: invalid duration "soon"`,
			},
			"value that decodes through a second type": {
				input: stringtest.LinesLF(
					"name: prod",
					"servers:",
					"  - name: a",
					"    tier: low",
					"  - name: b",
					"    tier: mid",
				),
				decode: decodeAs[mirrored](),
				want:   `6:11: $.servers[1].tier: unknown tier "mid"`,
			},
			"field that decodes through a second type": {
				input: stringtest.LinesLF(
					"prod:",
					"  servers:",
					"    - timeout: soon",
				),
				decode: decodeAs[struct {
					Prod mirrored `yaml:"prod"`
				}](),
				want: `3:16: $.prod.servers[0].timeout: time: invalid duration "soon"`,
			},
			// No field below the value reports the message with the text
			// the value put in front, so the error points at the value.
			"value that adds text to the error of a field": {
				input: stringtest.LinesLF(
					"main:",
					"  name: api",
					"  tier: mid",
				),
				decode: decodeAs[struct {
					Main prefixing `yaml:"main"`
				}](),
				want: `1:1: $.main: server: unknown tier "mid"`,
			},
			"value whose fields mirror nothing": {
				input: stringtest.LinesLF(
					"name: api",
					"value:",
					"  a: 1",
					"  b: bad",
				),
				decode: decodeAs[struct {
					Name  string      `yaml:"name"`
					Value opaqueValue `yaml:"value"`
				}](),
				want: `2:1: $.value: unmarshaler rejected the value`,
			},
			"json unmarshaler under its option": {
				input: stringtest.LinesLF(
					"name: api",
					"value:",
					"  v: 1",
				),
				decode: decodeAs[struct {
					Name  string      `yaml:"name"`
					Value jsonDecoded `yaml:"value"`
				}](niceyaml.WithJSONUnmarshalers(true)),
				want: `2:1: $.value: unmarshaler rejected the value`,
			},
			"type an option decodes": {
				input: "name: api\nvalue: bad\n",
				decode: decodeAs[struct {
					Name  string        `yaml:"name"`
					Value optionDecoded `yaml:"value"`
				}](niceyaml.WithCustomUnmarshaler(
					func(context.Context, *optionDecoded, func(any) error) error { return errUnmarshal },
				)),
				want: `2:8: $.value: unmarshaler rejected the value`,
			},
			// The decoder reads the value field by field, so the field
			// reported the error.
			"json unmarshaler without its option": {
				input: stringtest.LinesLF(
					"name: api",
					"value:",
					"  tier: mid",
				),
				decode: decodeAs[struct {
					Name  string      `yaml:"name"`
					Value jsonFielded `yaml:"value"`
				}](),
				want: `3:9: $.value.tier: unknown tier "mid"`,
			},
			// The scalar the decode reads is the value, so the error
			// points at the node itself, which the path names from the
			// root of the document.
			"scalar the decode reads": {
				input:  midTier,
				scope:  paths.Current().Child("tier"),
				decode: decodeAs[tier](),
				want:   `2:7: $.tier: unknown tier "mid"`,
			},
			"value below a scoped node": {
				input: stringtest.LinesLF(
					"servers:",
					"  - name: a",
					"    tier: mid",
				),
				scope:  paths.Current().Child("servers").Index(0),
				decode: decodeAs[tierServer](),
				want:   `3:11: $.servers[0].tier: unknown tier "mid"`,
			},
			"DecodeInto": {
				input: midTier,
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					var v tierServer

					return dd.DecodeInto(ctx, &v)
				},
				want: `2:7: $.tier: unknown tier "mid"`,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.AtOrSelf(t, yamltest.FirstDocument(t, tc.input), tc.scope)

				err := tc.decode(t.Context(), dd)
				require.EqualError(t, err, tc.want)

				srcErr := yamltest.RequireSourceError(t, err)

				requireNamedPath(t, srcErr, tc.want)

				_, ok := srcErr.Range()
				assert.True(t, ok, "the error carries no location")
			})
		}
	})

	// The decoder hands an UnmarshalText method a scalar alone, and words
	// its refusal of any other node for neither the value nor its kind.
	t.Run("reads a mapping or a sequence in a text type as a string field does", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			decode func(ctx context.Context, dd *niceyaml.Node) error
			input  string
			want   string
		}{
			"sequence element": {
				input: stringtest.LinesLF(
					"addrs:",
					"  - 10.0.0.1",
					"  - [a, b]",
				),
				decode: decodeAs[struct {
					Addrs []netip.Addr `yaml:"addrs"`
				}](),
				want: "3:3: $.addrs[1]: expected string, got sequence",
			},
			"mapping field": {
				input: "name: api\naddr: {a: b}\n",
				decode: decodeAs[struct {
					Name string     `yaml:"name"`
					Addr netip.Addr `yaml:"addr"`
				}](),
				want: "2:1: $.addr: expected string, got mapping",
			},
			"pointer field": {
				input: stringtest.LinesLF("tier:", "  a: b"),
				decode: decodeAs[struct {
					Tier *tier `yaml:"tier"`
				}](),
				want: "1:1: $.tier: expected string, got mapping",
			},
			"map value": {
				input:  "a: low\nb: [mid]\n",
				decode: decodeAs[map[string]tier](),
				want:   "2:1: $.b: expected string, got sequence",
			},
			// The path of a value an alias holds points at the alias.
			"alias to a sequence": {
				input: "all: &all [low, high]\ntier: *all\n",
				decode: decodeAs[struct {
					Tier tier `yaml:"tier"`
				}](),
				want: "2:7: $.tier: expected string, got sequence",
			},
			// The decoder reads nothing below the mapping, so the tier in
			// it reports nothing.
			"mapping that holds a sequence": {
				input: stringtest.LinesLF("holder:", "  tier: [a]"),
				decode: decodeAs[struct {
					Holder tierHolder `yaml:"holder"`
				}](),
				want: "1:1: $.holder: expected string, got mapping",
			},
			// The decoder calls the UnmarshalJSON method of the type only
			// under its option.
			"type with an UnmarshalJSON method too": {
				input: "either: {a: 1}\n",
				decode: decodeAs[struct {
					Either problemTextual `yaml:"either"`
				}](),
				want: "1:1: $.either: expected string, got mapping",
			},
			"sequence the decode reads": {
				input:  "[a, b]\n",
				decode: decodeAs[tier](),
				want:   "1:1: $: expected string, got sequence",
			},
			"empty mapping the decode reads": {
				input:  "{}\n",
				decode: decodeAs[*tier](),
				want:   "1:1: $: expected string, got mapping",
			},
			"mapping a scoped decode reads": {
				input: "name: api\ntier: {a: b}\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.DecodeAt[tier](ctx, paths.Current().Child("tier"))

					return err
				},
				want: "2:1: $.tier: expected string, got mapping",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				err := tc.decode(t.Context(), dd)
				require.EqualError(t, err, tc.want)
				require.ErrorIs(t, err, niceyaml.ErrDecode)
				assert.True(t, niceyaml.IsInvalid(err))

				requireNamedPath(t, yamltest.RequireSourceError(t, err), tc.want)
			})
		}
	})

	t.Run("comes back with no location", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			decode func(ctx context.Context, dd *niceyaml.Node) error
			input  string
			want   string
			source []niceyaml.SourceOption
		}{
			// The fields of the value mirror nothing, and the mapping the
			// decode reads is the whole of what the caller asked for.
			"mapping the decode reads": {
				input:  "a: 1\nb: bad\n",
				decode: decodeAs[opaqueValue](),
				want:   "unmarshaler rejected the value",
			},
			"value that adds text at the node the decode reads": {
				input:  midTier,
				decode: decodeAs[prefixing](),
				want:   `server: unknown tier "mid"`,
			},
			// The second decode reads a new value, which is not locked.
			"unmarshaler that reads the value the caller filled": {
				input: "a: x\nb: y\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					v := struct {
						A lockable `yaml:"a"`
						B lockable `yaml:"b"`
					}{B: lockable{Locked: true}}

					return dd.DecodeInto(ctx, &v)
				},
				want: "unmarshaler rejected the value",
			},
			// The second decode calls the unmarshaler again, and the panic
			// of that call reproduces nothing and adds no problem.
			"unmarshaler that panics in the second decode": {
				input: "a: 1\nv: x\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.Decode[struct {
						A int         `yaml:"a"`
						V rejectsOnce `yaml:"v"`
					}](context.WithValue(ctx, panicCallsKey{}, new(atomic.Int32)))

					return err
				},
				want: "unmarshaler rejected the value",
			},
			// The decoder shows no type a function registered for the
			// whole program decodes.
			"type a registered function decodes": {
				input: "name: api\nvalue: bad\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					yaml.RegisterCustomUnmarshaler(func(*registeredDecoded, []byte) error { return errUnmarshal })

					_, err := dd.Decode[struct {
						Name  string            `yaml:"name"`
						Value registeredDecoded `yaml:"value"`
					}](ctx)

					return err
				},
				want: "unmarshaler rejected the value",
			},
			// No path resolves through an alias to a reference document.
			"value an alias reads from a reference document": {
				input: "timeout: *limit\n",
				source: []niceyaml.SourceOption{
					niceyaml.WithReferences(niceyaml.NewSourceFromString("limit: &limit soon\n")),
				},
				decode: decodeAs[tierServer](),
				want:   `time: invalid duration "soon"`,
			},
			// Every decode of the node fails the same way, whatever the
			// target, so no value of the target reported the error.
			"error of the decoder itself": {
				input: "timeout: soon\n",
				source: []niceyaml.SourceOption{
					niceyaml.WithReferences(niceyaml.NewSourceFromString("limit: [\n")),
				},
				decode: decodeAs[tierServer](),
				want: stringtest.LinesLF(
					"[1:8] sequence end token ']' not found",
					">  1 | limit: [",
					"              ^",
				),
			},
			// The walk decodes nothing once the context has ended.
			"context an unmarshaler ends": {
				input: "- low\n- mid\n",
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					ctx, cancel := context.WithCancel(ctx)
					defer cancel()

					_, err := dd.Decode[[]canceling](context.WithValue(ctx, cancelKey{}, cancel))

					return err
				},
				want: "unmarshaler rejected the value",
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input, tc.source...)

				err := tc.decode(t.Context(), dd)
				require.EqualError(t, err, tc.want)
				require.ErrorIs(t, err, niceyaml.ErrDecode)

				srcErr := yamltest.RequireSourceError(t, err)

				_, ok := srcErr.Path()
				assert.False(t, ok, "the error took a path")

				_, ok = srcErr.Range()
				assert.False(t, ok, "the error took a location")
			})
		}
	})

	t.Run("matches the decode sentinel", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			decode func(ctx context.Context, dd *niceyaml.Node) error
			input  string
			want   string
		}{
			"field": {
				input:  "name: api\ntimeout: 5 minutes\n",
				decode: decodeAs[tierServer](),
				want:   `2:10: $.timeout: time: unknown unit " minutes" in duration "5 minutes"`,
			},
			"scalar the decode reads": {
				input:  "5 minutes\n",
				decode: decodeAs[time.Duration](),
				want:   `1:1: $: time: unknown unit " minutes" in duration "5 minutes"`,
			},
			"map key": {
				input:  "5 minutes: a\n",
				decode: decodeAs[map[time.Duration]string](),
				want:   `1:1: $.'5 minutes'~: time: unknown unit " minutes" in duration "5 minutes"`,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				err := tc.decode(t.Context(), dd)
				require.EqualError(t, err, tc.want)
				require.ErrorIs(t, err, niceyaml.ErrDecode)
			})
		}

		// The error of an unmarshaler is the value's own, so it keeps what
		// it matched beside the sentinel.
		dd := yamltest.FirstDocument(t, midTier)

		_, err := dd.Decode[tierServer](t.Context())
		require.EqualError(t, err, `2:7: $.tier: unknown tier "mid"`)
		require.ErrorIs(t, err, errUnknownTier)
		require.ErrorIs(t, err, niceyaml.ErrDecode)
	})

	// An unmarshaler holds no Node, so the path it writes reads from its
	// own value, as the path of a niceyaml.SelfValidator does.
	t.Run("puts the path an unmarshaler writes under its value", func(t *testing.T) {
		t.Parallel()

		// Returns a decode of a document whose items report err.
		reported := func(err error) func(context.Context, *niceyaml.Node) error {
			return func(ctx context.Context, dd *niceyaml.Node) error {
				_, derr := dd.Decode[struct {
					Name  string      `yaml:"name"`
					Items []reporting `yaml:"items"`
				}](context.WithValue(ctx, reportKey{}, err))

				return derr
			}
		}

		items := stringtest.LinesLF(
			"name: api",
			"items:",
			"  - from: 9",
			"    to: 3",
		)
		twoSpans := stringtest.LinesLF(
			"spans:",
			"  - {from: 1, to: 5}",
			"  - from: 9",
			"    to: 3",
		)
		mainSpan := stringtest.LinesLF("main:", "  span: {from: 9, to: 3}")

		tcs := map[string]struct {
			decode func(ctx context.Context, dd *niceyaml.Node) error
			is     error
			input  string
			want   string
		}{
			"method that takes a decode function": {
				input: twoSpans,
				decode: decodeAs[struct {
					Spans []span `yaml:"spans"`
				}](),
				is:   errSpan,
				want: "4:9: $.spans[1].to: to is below from",
			},
			"method that takes the node": {
				input: oneSpan,
				decode: decodeAs[struct {
					Spans []spanNode `yaml:"spans"`
				}](),
				is:   errSpan,
				want: "3:9: $.spans[0].to: to is below from",
			},
			"method that takes bytes": {
				input: oneSpan,
				decode: decodeAs[struct {
					Spans []spanBytes `yaml:"spans"`
				}](),
				is:   errSpan,
				want: "3:9: $.spans[0].to: to is below from",
			},
			"function of an option": {
				input: "name: api\nvalue: {from: 9, to: 3}\n",
				decode: decodeAs[struct {
					Name  string        `yaml:"name"`
					Value optionDecoded `yaml:"value"`
				}](niceyaml.WithCustomUnmarshaler(
					func(context.Context, *optionDecoded, func(any) error) error { return spanError() },
				)),
				is:   errSpan,
				want: "2:22: $.value.to: to is below from",
			},
			"map value": {
				input:  stringtest.LinesLF("a: {from: 1, to: 2}", "b: {from: 9, to: 3}"),
				decode: decodeAs[map[string]span](),
				is:     errSpan,
				want:   "2:18: $.b.to: to is below from",
			},
			"pointer field": {
				input: stringtest.LinesLF(
					"span:",
					"  from: 9",
					"  to: 3",
				),
				decode: decodeAs[struct {
					Span *span `yaml:"span"`
				}](),
				is:   errSpan,
				want: "3:7: $.span.to: to is below from",
			},
			// The path goes through the alias to the line that holds the
			// value.
			"value an alias holds": {
				input: stringtest.LinesLF("base: &base {from: 9, to: 3}", "spans: [*base]"),
				decode: decodeAs[struct {
					Spans []span `yaml:"spans"`
				}](),
				is:   errSpan,
				want: "1:27: $.spans[0].to: to is below from",
			},
			"field a merge key brings in": {
				input: stringtest.LinesLF(
					"base: &base {to: 3}",
					"spans:",
					"  - <<: *base",
					"    from: 9",
				),
				decode: decodeAs[struct {
					Spans []span `yaml:"spans"`
				}](),
				is:   errSpan,
				want: "1:18: $.spans[0].to: to is below from",
			},
			"value below a scoped node": {
				input: twoSpans,
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.DecodeAt[[]span](ctx, paths.Current().Child("spans"))

					return err
				},
				is:   errSpan,
				want: "4:9: $.spans[1].to: to is below from",
			},
			// No value below the node reports the error, so the path reads
			// from the node, which is the value.
			"mapping the decode reads": {
				input:  "from: 9\nto: 3\n",
				decode: decodeAs[span](),
				is:     errSpan,
				want:   "2:5: $.to: to is below from",
			},
			"mapping a scoped decode reads": {
				input: twoSpans,
				decode: func(ctx context.Context, dd *niceyaml.Node) error {
					_, err := dd.DecodeAt[span](ctx, paths.Current().Child("spans").Index(1))

					return err
				},
				is:   errSpan,
				want: "4:9: $.spans[1].to: to is below from",
			},
			// The span wrote the path, so the path goes under the span and
			// not under the value that handed its error on.
			"value that returns the error of a field": {
				input: stringtest.LinesLF(
					"mirrors:",
					"  - name: a",
					"    span: {from: 9, to: 3}",
				),
				decode: decodeAs[struct {
					Mirrors []spanMirror `yaml:"mirrors"`
				}](),
				is:   errSpan,
				want: "3:25: $.mirrors[0].span.to: to is below from",
			},
			"value that adds text to the error of a field": {
				input: mainSpan,
				decode: decodeAs[struct {
					Main spanWrapper `yaml:"main"`
				}](),
				is:   errSpan,
				want: "2:23: $.main.span.to: wrapper: to is below from",
			},
			// The value wrote the path of its field, so the path goes
			// under the value once, and not under the field too.
			"value that rebases the error of a field": {
				input: mainSpan,
				decode: decodeAs[struct {
					Main spanRebaser `yaml:"main"`
				}](),
				is:   errSpan,
				want: "2:23: $.main.span.to: to is below from",
			},
			"value that names a field in the error of that field": {
				input: stringtest.LinesLF("main:", "  tier: mid"),
				decode: decodeAs[struct {
					Main tierNamer `yaml:"main"`
				}](),
				is:   errUnknownTier,
				want: `2:9: $.main.tier: unknown tier "mid"`,
			},
			"path of the value itself": {
				input:  items,
				decode: reported(niceyaml.Invalid(errUnmarshal, niceyaml.AtPath(paths.Current()))),
				is:     errUnmarshal,
				want:   "3:3: $.items[0]: unmarshaler rejected the value",
			},
			"error with no location": {
				input:  items,
				decode: reported(niceyaml.Invalid(errUnmarshal)),
				is:     errUnmarshal,
				want:   "3:3: $.items[0]: unmarshaler rejected the value",
			},
			"rebased error with no location": {
				input:  items,
				decode: reported(niceyaml.Rebase(errUnmarshal, paths.Current().Child("to"))),
				is:     errUnmarshal,
				want:   "4:9: $.items[0].to: unmarshaler rejected the value",
			},
			// A path from the root reads from no value, so it stays.
			"path from the root": {
				input:  items,
				decode: reported(niceyaml.Invalid(errUnmarshal, niceyaml.AtPath(paths.Doc().Child("name")))),
				is:     errUnmarshal,
				want:   "1:7: $.name: unmarshaler rejected the value",
			},
			// Each problem of a summary goes under the value on its own.
			"summary": {
				input: items,
				decode: reported(niceyaml.NewSummary(
					"2 problems in the span",
					niceyaml.NewError("from is odd", niceyaml.AtPath(paths.Current().Child("from"))),
					niceyaml.NewError("to is odd", niceyaml.AtPath(paths.Current().Child("to"))),
				)),
				want: stringtest.JoinLF(
					"2 problems in the span",
					"3:11: $.items[0].from: from is odd",
					"4:9: $.items[0].to: to is odd",
				),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, tc.input)

				err := tc.decode(t.Context(), dd)
				require.EqualError(t, err, tc.want)
				require.ErrorIs(t, err, niceyaml.ErrDecode)

				if tc.is != nil {
					require.ErrorIs(t, err, tc.is)
				}

				srcErr := yamltest.RequireSourceError(t, err)

				// A summary points at no value, so it carries no path.
				if len(yamltest.ProblemsOf(srcErr)) > 0 {
					_, ok := srcErr.Path()
					assert.False(t, ok)

					return
				}

				requireNamedPath(t, srcErr, tc.want)
			})
		}
	})

	// An error that heads several problems names no place itself. The
	// places of its problems tell the error a value rebased from the
	// error of its field, so each path goes under the value once.
	t.Run("puts the problems a value rebased under that value once", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			report error
			want   string
		}{
			"summary": {
				report: niceyaml.NewSummary(
					"2 problems in the span",
					niceyaml.NewError("from is odd", niceyaml.AtPath(paths.Current().Child("from"))),
					niceyaml.NewError("to is odd", niceyaml.AtPath(paths.Current().Child("to"))),
				),
				want: stringtest.JoinLF(
					"2 problems in the span",
					"3:11: $.main.item.from: from is odd",
					"4:9: $.main.item.to: to is odd",
				),
			},
			"join": {
				report: errors.Join(errors.New("from is odd"), errors.New("to is odd")),
				want: stringtest.JoinLF(
					"2:3: $.main.item: from is odd",
					"2:3: $.main.item: to is odd",
				),
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				dd := yamltest.FirstDocument(t, stringtest.LinesLF(
					"main:",
					"  item:",
					"    from: 9",
					"    to: 3",
				))

				_, err := dd.Decode[struct {
					Main rebasing `yaml:"main"`
				}](context.WithValue(t.Context(), reportKey{}, tc.report))
				require.EqualError(t, err, tc.want)
				require.ErrorIs(t, err, niceyaml.ErrDecode)
			})
		}
	})

	// The detail explains the error, so it points at nothing itself, and
	// its path goes under the value all the same.
	t.Run("puts the path of a detail under the value", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, oneSpan)
		report := niceyaml.NewError("span is odd", niceyaml.WithDetails(
			niceyaml.NewError("to set here", niceyaml.AtPath(paths.Current().Child("to"))),
		))

		_, err := dd.Decode[struct {
			Spans []reporting `yaml:"spans"`
		}](context.WithValue(t.Context(), reportKey{}, error(report)))
		require.EqualError(t, err, "2:3: $.spans[0]: span is odd")

		srcErr := yamltest.RequireSourceError(t, err)
		require.Len(t, yamltest.DetailsOf(srcErr), 1)
		assert.EqualError(t, yamltest.DetailsOf(srcErr)[0].Bound, "3:9: $.spans[0].to: to set here")
	})

	t.Run("keeps the location the error carries", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, "name: api\nvalue: [a, b]\n")

		_, err := dd.Decode[struct {
			Name  string     `yaml:"name"`
			Value positioned `yaml:"value"`
		}](t.Context())
		require.EqualError(t, err, "2:8: unmarshaler rejected the value")
		require.ErrorIs(t, err, errUnmarshal)
		require.ErrorIs(t, err, niceyaml.ErrDecode)

		srcErr := yamltest.RequireSourceError(t, err)

		_, ok := srcErr.Path()
		assert.False(t, ok, "the error took a path")
	})

	t.Run("leaves the decoded value alone", func(t *testing.T) {
		t.Parallel()

		dd := yamltest.FirstDocument(t, stringtest.LinesLF(
			"a: x",
			"b: y",
			"c: z",
		))

		// The second decode fills values of its own, so the value the
		// caller holds keeps what the first decode set.
		var v struct {
			A lockable `yaml:"a"`
			B tier     `yaml:"b"`
			C lockable `yaml:"c"`
		}

		err := dd.DecodeInto(t.Context(), &v)
		require.EqualError(t, err, `2:4: $.b: unknown tier "y"`)
		assert.Equal(t, lockable{Text: "x"}, v.A)
		assert.Equal(t, lockable{Text: "z"}, v.C)
	})
}
