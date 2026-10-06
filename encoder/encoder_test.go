package encoder_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/encoder"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
)

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("creates encoder with no options", func(t *testing.T) {
		t.Parallel()

		var buf bytes.Buffer

		enc := encoder.New(&buf)
		require.NotNil(t, enc)
	})

	t.Run("creates encoder with Pretty", func(t *testing.T) {
		t.Parallel()

		var buf bytes.Buffer

		enc := encoder.New(&buf, encoder.Pretty())
		require.NotNil(t, enc)
	})
}

func TestEncoder_Encode(t *testing.T) {
	t.Parallel()

	type nested struct {
		Name  string `yaml:"name"`
		Value int    `yaml:"value"`
	}

	tcs := map[string]struct {
		input any
		want  string
	}{
		"simple string": {
			input: "hello",
			want:  "hello\n",
		},
		"simple map": {
			input: map[string]string{"key": "value"},
			want:  "key: value\n",
		},
		"nested struct": {
			input: nested{Name: "test", Value: 42},
			want:  "name: test\nvalue: 42\n",
		},
		"slice": {
			input: []string{"a", "b", "c"},
			want:  "- a\n- b\n- c\n",
		},
		"nil value": {
			input: nil,
			want:  "null\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			enc := encoder.New(&buf)

			err := enc.Encode(t.Context(), tc.input)
			require.NoError(t, err)

			got := buf.String()
			assert.Equal(t, tc.want, got)
		})
	}
}

// literalBlock is a string whose marshaler returns it as a literal block.
type literalBlock string

func (b literalBlock) MarshalYAML() ([]byte, error) {
	return []byte(literal(string(b))), nil
}

// taggedLiteralBlock is a string whose marshaler returns it as a literal
// block with a "!!str" tag.
type taggedLiteralBlock string

func (b taggedLiteralBlock) MarshalYAML() ([]byte, error) {
	return []byte("!!str " + literal(string(b))), nil
}

// anchoredLiteralBlock is a string whose marshaler returns it as a literal
// block with a "&k" anchor.
type anchoredLiteralBlock string

func (b anchoredLiteralBlock) MarshalYAML() ([]byte, error) {
	return []byte("&k " + literal(string(b))), nil
}

// taggedString is a string whose marshaler returns it as a plain scalar
// with a "!!str" tag.
type taggedString string

func (s taggedString) MarshalYAML() ([]byte, error) {
	return []byte("!!str " + string(s)), nil
}

// rawYAML is a string whose marshaler returns it as YAML as is.
type rawYAML string

func (r rawYAML) MarshalYAML() ([]byte, error) {
	return []byte(r), nil
}

// literal returns s as a literal block that strips the final line break.
func literal(s string) string {
	return "|-\n  " + strings.ReplaceAll(s, "\n", "\n  ") + "\n"
}

func TestEncoder_Encode_quoting(t *testing.T) {
	t.Parallel()

	flow := encoder.WithYAMLOptions(yaml.Flow(true))
	literalStyle := encoder.WithYAMLOptions(yaml.UseLiteralStyleIfMultiline(true))

	tcs := map[string]struct {
		input any
		// Holds the value want decodes to, when it differs from input.
		decoded any
		want    string
		opts    []encoder.Option
	}{
		"leading question mark": {
			input: map[string]string{"k": "? x"},
			want:  "k: \"? x\"\n",
		},
		"leading question mark key": {
			input: map[string]int{"? x": 1},
			want:  "\"? x\": 1\n",
		},
		"leading dash before NUL": {
			input: "-\x00x",
			want:  "\"-\\x00x\"\n",
		},
		"leading tab": {
			input: map[string]string{"k": "\tx"},
			want:  "k: \"\\tx\"\n",
		},
		"trailing tab": {
			input: map[string]string{"k": "x\t"},
			want:  "k: \"x\\t\"\n",
		},
		"inner tab": {
			input: "x\tx",
			want:  "\"x\\tx\"\n",
		},
		"colon before tab": {
			input: map[string]string{"k": "a:\tb"},
			want:  "k: \"a:\\tb\"\n",
		},
		"infinity": {
			input: map[string]string{"k": ".inf"},
			want:  "k: \".inf\"\n",
		},
		"negative infinity": {
			input: "-.inf",
			want:  "\"-.inf\"\n",
		},
		"not a number": {
			input: map[string]string{"k": ".nan"},
			want:  "k: \".nan\"\n",
		},
		"not a number key": {
			input: map[string]int{".NaN": 1},
			want:  "\".NaN\": 1\n",
		},
		"document start marker": {
			input: "---",
			want:  "\"---\"\n",
		},
		"document end marker": {
			input: "...",
			want:  "\"...\"\n",
		},
		"document end marker with text": {
			input: "...x",
			want:  "\"...x\"\n",
		},
		"merge key": {
			input: map[string]int{"<<": 1},
			want:  "\"<<\": 1\n",
		},
		"key ending in merge key": {
			input: map[string]int{"x<<": 1},
			want:  "\"x<<\": 1\n",
		},
		"CRLF line break": {
			input: map[string]string{"k": "x\r\ny"},
			want:  "k: \"x\\r\\ny\"\n",
		},
		"CR line break": {
			input: "x\ry",
			want:  "\"x\\ry\"\n",
		},
		"line break key": {
			input: map[string]int{"x\ny": 1},
			want:  "\"x\\ny\": 1\n",
		},
		"line break alone": {
			input: "\n",
			want:  "\"\\n\"\n",
		},
		"indented line after line break": {
			input: map[string]string{"k": "\n  y"},
			want:  "k: \"\\n  y\"\n",
		},
		"space at end of last line": {
			input: map[string]string{"k": "x\ny "},
			opts:  []encoder.Option{literalStyle},
			want:  "k: \"x\\ny \"\n",
		},
		"space before final line break at indent 1": {
			input: map[string]string{"k": "x \n"},
			opts:  []encoder.Option{encoder.WithIndent(1)},
			want:  "k: \"x \\n\"\n",
		},
		"blank last line": {
			input: map[string]string{"k": "x\n \n"},
			want:  "k: \"x\\n \\n\"\n",
		},
		"line breaks alone before sequence entry": {
			input: []string{"\n\n", "z"},
			want:  "- \"\\n\\n\"\n- z\n",
		},
		"line break in flow mapping": {
			input: map[string]string{"k": "x\ny"},
			opts:  []encoder.Option{flow},
			want:  "{k: \"x\\ny\"}\n",
		},
		"flow indicator in flow mapping": {
			input: map[string]string{"k": "a[b"},
			opts:  []encoder.Option{flow},
			want:  "{k: \"a[b\"}\n",
		},
		"byte order mark": {
			input: "\ufeffx",
			want:  "\"\\ufeffx\"\n",
		},
		"byte order mark key": {
			input: map[string]int{"\ufeffx": 1},
			want:  "\"\\ufeffx\": 1\n",
		},
		"indented line in sequence at indent 1": {
			input: []string{"x\n y"},
			opts:  []encoder.Option{encoder.WithIndent(1)},
			want:  "- \"x\\n y\"\n",
		},
		"indented line in indented sequence at indent 1": {
			input: map[string][]string{"k": {"x\n  y"}},
			opts:  []encoder.Option{encoder.WithIndent(1), encoder.WithIndentSequence(true)},
			want:  "k:\n - \"x\\n  y\"\n",
		},
		"marshaled literal block in flow mapping": {
			input: map[string]literalBlock{"k": "x\ny"},
			opts:  []encoder.Option{flow},
			want:  "{k: \"x\\ny\"}\n",
		},
		"marshaled literal block in flow sequence": {
			input: []literalBlock{"x\ny"},
			opts:  []encoder.Option{flow},
			want:  "[\"x\\ny\"]\n",
		},
		"marshaled literal block with anchor in flow mapping": {
			input: struct {
				A literalBlock `yaml:"a,anchor"`
			}{A: "x\ny"},
			opts: []encoder.Option{flow},
			want: "{a: &a \"x\\ny\"}\n",
		},
		"marshaled literal block with tag in flow sequence": {
			input: []taggedLiteralBlock{"x\ny"},
			opts:  []encoder.Option{flow},
			want:  "[!!str \"x\\ny\"]\n",
		},
		"marshaled literal block key": {
			input: map[literalBlock]int{"x\ny": 1},
			want:  "\"x\\ny\": 1\n",
		},
		"marshaled literal block key with tag": {
			input: map[taggedLiteralBlock]int{"x\ny": 1},
			want:  "!!str \"x\\ny\": 1\n",
		},
		"marshaled literal block key with anchor": {
			input: map[anchoredLiteralBlock]int{"x\ny": 1},
			want:  "&k \"x\\ny\": 1\n",
		},
		"marshaled merge key with tag": {
			input: map[taggedString]int{"<<": 1},
			want:  "!!str \"<<\": 1\n",
		},
		"marshaled plain scalar over a blank line": {
			input:   map[string]rawYAML{"k": "a\n\n  b"},
			decoded: map[string]rawYAML{"k": "a\nb"},
			want:    "k: \"a\\nb\"\n",
		},
		"marshaled plain scalar over a blank line at top level": {
			input:   rawYAML("a\n\n  b"),
			decoded: rawYAML("a\nb"),
			want:    "\"a\\nb\"\n",
		},
		"marshaled plain scalar over a blank line in nested mapping": {
			input:   map[string]map[string]rawYAML{"a": {"k": "a\n\n  b"}},
			decoded: map[string]map[string]rawYAML{"a": {"k": "a\nb"}},
			want:    "a:\n  k: \"a\\nb\"\n",
		},
		"marshaled single-quoted line break": {
			input:   map[string]rawYAML{"k": "'a\n\n  b'"},
			decoded: map[string]rawYAML{"k": "a\nb"},
			want:    "k: \"a\\nb\"\n",
		},
		"marshaled single-quoted line break key": {
			input:   map[rawYAML]string{"'a\n\n  b'": "v"},
			decoded: map[rawYAML]string{"a\nb": "v"},
			want:    "\"a\\nb\": v\n",
		},
		"marshaled keyword under a custom tag": {
			input:   map[string]rawYAML{"k": "!foo .inf"},
			decoded: map[string]rawYAML{"k": ".inf"},
			want:    "k: !foo .inf\n",
		},
		"marshaled number under a custom tag": {
			input:   []rawYAML{"!foo 1"},
			decoded: []rawYAML{"1"},
			want:    "- !foo 1\n",
		},
		"marshaled keyword key under a custom tag": {
			input:   map[rawYAML]int{"!foo true": 1},
			decoded: map[rawYAML]int{"true": 1},
			want:    "!foo true: 1\n",
		},
		"marshaled block mapping in flow mapping": {
			input:   map[string]rawYAML{"k": "? a\n: |"},
			decoded: map[string]map[string]string{"k": {"a": ""}},
			opts:    []encoder.Option{flow},
			want:    "{k:\n  ? a: |\n}\n",
		},
		"marshaled single-quoted string": {
			input:   map[string]rawYAML{"k": "'a b'"},
			decoded: map[string]rawYAML{"k": "a b"},
			want:    "k: 'a b'\n",
		},
		"literal block": {
			input: map[string]string{"k": "x\ny"},
			want:  "k: |-\n  x\n  y\n",
		},
		"literal block with space before line break": {
			input: map[string]string{"k": "x  \ny"},
			want:  "k: |-\n  x  \n  y\n",
		},
		"literal block with tab before line break": {
			input: map[string]string{"k": "x\t\ny"},
			want:  "k: |-\n  x\t\n  y\n",
		},
		"literal block with tab at end": {
			input: map[string]string{"k": "x\ny\t"},
			want:  "k: |-\n  x\n  y\t\n",
		},
		"literal block with space before final line breaks": {
			input: map[string]string{"k": "x \n\n"},
			want:  "k: |+\n  x \n\n",
		},
		"literal block in sequence at indent 1": {
			input: []string{"x\ny"},
			opts:  []encoder.Option{encoder.WithIndent(1)},
			want:  "- |-\n   x\n   y\n",
		},
		"marshaled literal block": {
			input: map[string]literalBlock{"k": "x\ny"},
			want:  "k: |-\n  x\n  y\n",
		},
		"plain string": {
			input: map[string]string{"k": "a-b ?c"},
			want:  "k: a-b ?c\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			enc := encoder.New(&buf, tc.opts...)

			require.NoError(t, enc.Encode(t.Context(), tc.input))
			assert.Equal(t, tc.want, buf.String())

			decoded := tc.decoded
			if decoded == nil {
				decoded = tc.input
			}

			got := reflect.New(reflect.TypeOf(decoded))
			doc := yamltest.FirstDocument(t, buf.String())

			require.NoError(t, doc.DecodeInto(t.Context(), got.Interface()))
			assert.Equal(t, decoded, got.Elem().Interface())
		})
	}
}

// shared is a value that [anchoredDoc] and [unencodableDoc] anchor.
type shared struct {
	Name string `yaml:"name"`
}

// anchoredDoc anchors the [shared] value it points to.
type anchoredDoc struct {
	A *shared `yaml:"a,anchor"`
}

// unencodableDoc anchors a [shared] value before a field go-yaml cannot
// encode.
type unencodableDoc struct {
	A *shared  `yaml:"a,anchor"`
	C chan int `yaml:"c"`
}

func TestEncoder_Encode_documents(t *testing.T) {
	t.Parallel()

	s := &shared{Name: "x"}

	type step struct {
		value any
		fails bool
	}

	tcs := map[string]struct {
		steps []step
		want  string
	}{
		"separator between documents": {
			steps: []step{{value: "a"}, {value: "b"}},
			want:  "a\n---\nb\n",
		},
		"document marker strings": {
			steps: []step{{value: "---"}, {value: "..."}, {value: "next"}},
			want:  "\"---\"\n---\n\"...\"\n---\nnext\n",
		},
		"anchor in every document": {
			steps: []step{
				{value: anchoredDoc{A: s}},
				{value: anchoredDoc{A: s}},
			},
			want: "a: &a\n  name: x\n---\na: &a\n  name: x\n",
		},
		"no anchor from a failed encode": {
			steps: []step{
				{value: unencodableDoc{A: s, C: make(chan int)}, fails: true},
				{value: anchoredDoc{A: s}},
			},
			want: "a: &a\n  name: x\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			enc := encoder.New(&buf)

			for i, st := range tc.steps {
				err := enc.Encode(t.Context(), st.value)
				if st.fails {
					require.Error(t, err, "step %d", i)
				} else {
					require.NoError(t, err, "step %d", i)
				}
			}

			assert.Equal(t, tc.want, buf.String())

			docs, err := niceyaml.NewSourceFromString(buf.String()).Documents()
			require.NoError(t, err)

			for i, doc := range docs {
				var got any

				require.NoError(t, doc.DecodeInto(t.Context(), &got), "document %d", i)
			}
		})
	}
}

// policyKey is the context key that [policyMarshaler] reads.
type policyKey struct{}

// policyMarshaler encodes as the policy its context holds, or as "none".
type policyMarshaler struct{}

func (policyMarshaler) MarshalYAML(ctx context.Context) (any, error) {
	policy, ok := ctx.Value(policyKey{}).(string)
	if !ok {
		policy = "none"
	}

	return policy, nil
}

func TestEncoder_Encode_context(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	enc := encoder.New(&buf)
	ctx := context.WithValue(t.Context(), policyKey{}, "redact")

	require.NoError(t, enc.Encode(ctx, map[string]policyMarshaler{"policy": {}}))
	assert.Equal(t, "policy: redact\n", buf.String())
}

// emptyMarshaler marshals to no YAML.
type emptyMarshaler struct{}

func (emptyMarshaler) MarshalYAML() ([]byte, error) {
	return nil, nil
}

// commentMarshaler marshals to YAML that holds only a comment.
type commentMarshaler struct{}

func (commentMarshaler) MarshalYAML() ([]byte, error) {
	return []byte("# comment\n"), nil
}

func TestEncoder_Encode_noNode(t *testing.T) {
	t.Parallel()

	comments := yamltest.FirstDocument(t, "# comment\n").AST()

	tcs := map[string]struct {
		input any
	}{
		"empty marshaler": {
			input: emptyMarshaler{},
		},
		"empty marshaler as a map value": {
			input: map[string]any{"k": emptyMarshaler{}},
		},
		"empty marshaler as a sequence entry": {
			input: []any{emptyMarshaler{}, 1},
		},
		"comment marshaler as a map value": {
			input: map[string]any{"k": commentMarshaler{}},
		},
		"comment-only AST": {
			input: comments,
		},
		"comment-only AST as a map value": {
			input: map[string]any{"k": comments},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			enc := encoder.New(&buf)

			err := enc.Encode(t.Context(), tc.input)
			require.ErrorIs(t, err, encoder.ErrNoNode)
			assert.Empty(t, buf.String())

			require.NoError(t, enc.Encode(t.Context(), "next"))
			assert.Equal(t, "next\n", buf.String(), "a later document has no separator")
		})
	}
}

func TestEncoder_Close(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	enc := encoder.New(&buf)

	err := enc.Close()
	assert.NoError(t, err)
}

// failWriter refuses every write with errWrite.
type failWriter struct{}

var errWrite = errors.New("disk full")

func (failWriter) Write([]byte) (int, error) {
	return 0, errWrite
}

// countingMarshaler counts the calls to its MarshalYAML method.
type countingMarshaler struct {
	calls *int
}

func (m countingMarshaler) MarshalYAML() (any, error) {
	*m.calls++

	return "x", nil
}

func TestEncoder_Encode_writeError(t *testing.T) {
	t.Parallel()

	enc := encoder.New(failWriter{})

	err := enc.Encode(t.Context(), map[string]int{"a": 1})
	require.ErrorIs(t, err, errWrite)

	err = enc.Encode(t.Context(), map[string]int{"b": 2})
	require.ErrorIs(t, err, errWrite, "a later Encode reports the same error")

	err = enc.Encode(t.Context(), make(chan int))
	require.ErrorIs(t, err, errWrite, "a value go-yaml cannot encode reports the write error")

	calls := 0
	err = enc.Encode(t.Context(), countingMarshaler{calls: &calls})
	require.ErrorIs(t, err, errWrite)
	assert.Zero(t, calls, "Encode runs no marshaler after a refused write")

	err = enc.Close()
	require.ErrorIs(t, err, errWrite, "Close reports the write error")
}

func TestWithIndent_panicsBelowOne(t *testing.T) {
	t.Parallel()

	for _, spaces := range []int{0, -1} {
		assert.Panics(t, func() { encoder.WithIndent(spaces) }, "spaces=%d", spaces)
	}
}

func TestWithYAMLOptions_negativeIndent(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	enc := encoder.New(&buf, encoder.WithYAMLOptions(yaml.Indent(-2)))

	err := enc.Encode(t.Context(), map[string]map[string]int{"a": {"b": 1}})
	require.Error(t, err)
	assert.Empty(t, buf.String())
}

func TestPretty(t *testing.T) {
	t.Parallel()

	type config struct {
		Items []string `yaml:"items"`
	}

	type server struct {
		Name  string `yaml:"name"`
		Ports []int  `yaml:"ports"`
	}

	// Each input encodes as a document of its own. Every want is the text
	// prettier writes for it, except where a marshaler returned YAML.
	tcs := map[string]struct {
		inputs []any
		want   string
	}{
		"sequence under a key": {
			inputs: []any{config{Items: []string{"one", "two"}}},
			want:   "items:\n  - one\n  - two\n",
		},
		"root sequence": {
			inputs: []any{[]string{"one", "two"}},
			want:   "- one\n- two\n",
		},
		"root sequence of mappings": {
			inputs: []any{[]server{{Name: "a", Ports: []int{1, 2}}, {Name: "b"}}},
			want:   "- name: a\n  ports:\n    - 1\n    - 2\n- name: b\n  ports: []\n",
		},
		"root sequence of sequences": {
			inputs: []any{[][]string{{"a", "b"}, {"c"}}},
			want:   "- - a\n  - b\n- - c\n",
		},
		"literal block in a root sequence": {
			inputs: []any{[]string{"one\ntwo\n"}},
			want:   "- |\n  one\n  two\n",
		},
		"literal block in a mapping in a root sequence": {
			inputs: []any{[]map[string]string{{"k": "one\ntwo\n"}}},
			want:   "- k: |\n    one\n    two\n",
		},
		"root sequence in each document": {
			inputs: []any{[]string{"a"}, config{Items: []string{"b"}}, []string{"c"}},
			want:   "- a\n---\nitems:\n  - b\n---\n- c\n",
		},
		"root sequence that a marshaler returned": {
			inputs: []any{rawYAML("- a\n- b\n")},
			want:   "- a\n- b\n",
		},
		"root sequence that holds YAML a marshaler returned": {
			inputs: []any{[]rawYAML{"- a\n- b\n"}},
			want:   "  - - a\n    - b\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			enc := encoder.New(&buf, encoder.Pretty())

			for _, input := range tc.inputs {
				require.NoError(t, enc.Encode(t.Context(), input))
			}

			assert.Equal(t, tc.want, buf.String())
		})
	}
}

func TestPretty_order(t *testing.T) {
	t.Parallel()

	const (
		pretty     = "k:\n  items:\n    - one\n"
		fourSpaces = "k:\n    items:\n        - one\n"
		atParent   = "k:\n  items:\n  - one\n"
	)

	tcs := map[string]struct {
		want string
		opts []encoder.Option
	}{
		"alone": {
			opts: []encoder.Option{encoder.Pretty()},
			want: pretty,
		},
		"twice": {
			opts: []encoder.Option{encoder.Pretty(), encoder.Pretty()},
			want: pretty,
		},
		"before WithIndent": {
			opts: []encoder.Option{encoder.Pretty(), encoder.WithIndent(4)},
			want: fourSpaces,
		},
		"after WithIndent": {
			opts: []encoder.Option{encoder.WithIndent(4), encoder.Pretty()},
			want: pretty,
		},
		"before WithIndentSequence": {
			opts: []encoder.Option{encoder.Pretty(), encoder.WithIndentSequence(false)},
			want: atParent,
		},
		"after WithIndentSequence": {
			opts: []encoder.Option{encoder.WithIndentSequence(false), encoder.Pretty()},
			want: pretty,
		},
		"before a go-yaml indent": {
			opts: []encoder.Option{encoder.Pretty(), encoder.WithYAMLOptions(yaml.Indent(4))},
			want: fourSpaces,
		},
		"after a go-yaml indent": {
			opts: []encoder.Option{encoder.WithYAMLOptions(yaml.Indent(4)), encoder.Pretty()},
			want: pretty,
		},
		"before a go-yaml sequence indent": {
			opts: []encoder.Option{encoder.Pretty(), encoder.WithYAMLOptions(yaml.IndentSequence(false))},
			want: atParent,
		},
		"after a go-yaml sequence indent": {
			opts: []encoder.Option{encoder.WithYAMLOptions(yaml.IndentSequence(false)), encoder.Pretty()},
			want: pretty,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			enc := encoder.New(&buf, tc.opts...)

			require.NoError(t, enc.Encode(t.Context(), map[string]map[string][]string{"k": {"items": {"one"}}}))
			assert.Equal(t, tc.want, buf.String())
		})
	}
}

func TestPretty_reuse(t *testing.T) {
	t.Parallel()

	pretty := encoder.Pretty()
	input := map[string][]string{"items": {"one"}}

	var wide, plain bytes.Buffer

	require.NoError(t, encoder.New(&wide, pretty, encoder.WithIndent(4)).Encode(t.Context(), input))
	require.NoError(t, encoder.New(&plain, pretty).Encode(t.Context(), input))

	assert.Equal(t, "items:\n    - one\n", wide.String())
	assert.Equal(t, "items:\n  - one\n", plain.String(), "an option after Pretty in one call reaches no other call")
}

// TestPretty_indentOptions checks that Pretty writes what WithIndent(2) and
// WithIndentSequence(true) write in its place, before and after each set of
// other options.
func TestPretty_indentOptions(t *testing.T) {
	t.Parallel()

	type server struct {
		Name  string   `yaml:"name"`
		Note  string   `yaml:"note,omitempty"`
		Ports []int    `yaml:"ports"`
		Tags  []string `yaml:"tags,omitempty"`
	}

	inputs := map[string]any{
		"null":                       nil,
		"plain string":               "x",
		"quoted keyword":             ".inf",
		"quoted tab":                 "a\tb",
		"root sequence":              []string{"a", "b"},
		"root sequence of sequences": [][]string{{"a", "b"}, {"c"}},
		"nested mapping": map[string]any{
			"k": map[string]any{"items": []string{"one", "two"}, "n": 1},
		},
		"root sequence of mappings": []server{
			{Name: "a", Ports: []int{1, 2}, Note: "l1\nl2\n"},
			{Name: "b", Tags: []string{"x"}},
		},
		"literal block in a root sequence":      []map[string]string{{"k": "one\ntwo\n"}},
		"root sequence that a marshaler wrote":  rawYAML("- a\n- b\n"),
		"root sequence of YAML from marshalers": []rawYAML{"- a\n- b\n"},
		"YAML from a marshaler under a key":     map[string]rawYAML{"k": "x:\n  - 1\n"},
	}

	// The head comment has no place on a root scalar, so the comments case
	// also compares the errors of four inputs.
	tcs := map[string]struct {
		opts []encoder.Option
	}{
		"no other option": {},
		"indent 4": {
			opts: []encoder.Option{encoder.WithIndent(4)},
		},
		"indent 1": {
			opts: []encoder.Option{encoder.WithIndent(1)},
		},
		"sequences at the parent indent": {
			opts: []encoder.Option{encoder.WithIndentSequence(false)},
		},
		"go-yaml indent 3": {
			opts: []encoder.Option{encoder.WithYAMLOptions(yaml.Indent(3))},
		},
		"flow style": {
			opts: []encoder.Option{encoder.WithYAMLOptions(yaml.Flow(true))},
		},
		"literal style": {
			opts: []encoder.Option{encoder.WithYAMLOptions(yaml.UseLiteralStyleIfMultiline(true))},
		},
		"comments": {
			opts: []encoder.Option{encoder.WithYAMLComments(yaml.CommentMap{"$": {yaml.HeadComment(" h")}})},
		},
		"indent, sequence, and block style": {
			opts: []encoder.Option{
				encoder.WithIndent(4),
				encoder.WithIndentSequence(false),
				encoder.WithYAMLOptions(yaml.Flow(false)),
			},
		},
	}

	encode := func(t *testing.T, input any, opts ...encoder.Option) (string, string) {
		t.Helper()

		var (
			buf bytes.Buffer
			msg string
		)

		err := encoder.New(&buf, opts...).Encode(t.Context(), input)
		if err != nil {
			msg = err.Error()
		}

		return buf.String(), msg
	}

	pretty := []encoder.Option{encoder.Pretty()}
	indentOptions := []encoder.Option{encoder.WithIndent(2), encoder.WithIndentSequence(true)}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			for inputName, input := range inputs {
				want, wantErr := encode(t, input, slices.Concat(indentOptions, tc.opts)...)
				got, gotErr := encode(t, input, slices.Concat(pretty, tc.opts)...)

				assert.Equal(t, want, got, "%s, with Pretty first", inputName)
				assert.Equal(t, wantErr, gotErr, "%s, with Pretty first", inputName)

				want, wantErr = encode(t, input, slices.Concat(tc.opts, indentOptions)...)
				got, gotErr = encode(t, input, slices.Concat(tc.opts, pretty)...)

				assert.Equal(t, want, got, "%s, with Pretty last", inputName)
				assert.Equal(t, wantErr, gotErr, "%s, with Pretty last", inputName)
			}
		})
	}
}

func TestWithIndentSequence(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input  any
		want   string
		indent int
		seq    bool
	}{
		"root sequence": {
			input:  []string{"a"},
			indent: 2,
			want:   "- a\n",
		},
		"indented root sequence": {
			input:  []string{"a"},
			indent: 2,
			seq:    true,
			want:   "- a\n",
		},
		"sequence under a key": {
			input:  []map[string][]string{{"k": {"a"}}},
			indent: 2,
			want:   "- k:\n  - a\n",
		},
		"indented sequence under a key": {
			input:  []map[string][]string{{"k": {"a"}}},
			indent: 2,
			seq:    true,
			want:   "- k:\n    - a\n",
		},
		"indented sequence under a key at indent 4": {
			input:  []map[string][]string{{"k": {"a"}}},
			indent: 4,
			seq:    true,
			want:   "- k:\n      - a\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			enc := encoder.New(&buf, encoder.WithIndent(tc.indent), encoder.WithIndentSequence(tc.seq))

			require.NoError(t, enc.Encode(t.Context(), tc.input))
			assert.Equal(t, tc.want, buf.String())
		})
	}
}

func TestWithYAMLComments(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input    any
		comments yaml.CommentMap
		want     string
		opts     []encoder.Option
	}{
		"head, line, and foot comments": {
			input: map[string]any{"a": 1, "b": map[string]any{"c": "x"}},
			comments: yaml.CommentMap{
				"$.a":   {yaml.HeadComment(" head a"), yaml.LineComment(" line a")},
				"$.b.c": {yaml.LineComment(" line c")},
				"$.b":   {yaml.FootComment(" foot b")},
			},
			want: "# head a\na: 1 # line a\nb:\n  c: x # line c\n# foot b\n",
		},
		"line comment on a quoted keyword": {
			input:    map[string]string{"k": ".inf"},
			comments: yaml.CommentMap{"$.k": {yaml.LineComment(" not a float")}},
			want:     "k: \".inf\" # not a float\n",
		},
		"line comment on a quoted CRLF string": {
			input:    map[string]string{"k": "x\r\ny"},
			comments: yaml.CommentMap{"$.k": {yaml.LineComment(" two lines")}},
			want:     "k: \"x\\r\\ny\" # two lines\n",
		},
		"line comment on a mapping": {
			input:    map[string]any{"b": map[string]any{"c": "x"}},
			comments: yaml.CommentMap{"$.b": {yaml.LineComment(" nested")}},
			want:     "b: # nested\n  c: x\n",
		},
		"line comment on a root scalar": {
			input:    "x",
			comments: yaml.CommentMap{"$": {yaml.LineComment(" root")}},
			want:     "x # root\n",
		},
		"head comment of two lines": {
			input:    map[string]int{"a": 1},
			comments: yaml.CommentMap{"$.a": {yaml.HeadComment(" one", " two")}},
			want:     "# one\n# two\na: 1\n",
		},
		"head comment on a sequence entry": {
			input:    map[string][]string{"items": {"one", "two"}},
			comments: yaml.CommentMap{"$.items[1]": {yaml.HeadComment(" second")}},
			opts:     []encoder.Option{encoder.Pretty()},
			want:     "items:\n  - one\n  # second\n  - two\n",
		},
		"head comment on a root sequence": {
			input:    []string{"a"},
			comments: yaml.CommentMap{"$": {yaml.HeadComment(" list")}},
			want:     "# list\n- a\n",
		},
		"line comment on a sequence entry": {
			input:    map[string][]string{"items": {"one", "two"}},
			comments: yaml.CommentMap{"$.items[0]": {yaml.LineComment(" first")}},
			want:     "items:\n- one # first\n- two\n",
		},
		"foot comment on a sequence": {
			input:    map[string][]string{"items": {"one", "two"}},
			comments: yaml.CommentMap{"$.items[0]": {yaml.FootComment(" end")}},
			want:     "items:\n- one\n- two\n# end\n",
		},
		"path that selects no node": {
			input:    map[string]int{"a": 1},
			comments: yaml.CommentMap{"$.missing": {yaml.HeadComment(" none")}},
			want:     "a: 1\n",
		},
		"nil map": {
			input: map[string]int{"a": 1},
			want:  "a: 1\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			enc := encoder.New(&buf, append(tc.opts, encoder.WithYAMLComments(tc.comments))...)

			require.NoError(t, enc.Encode(t.Context(), tc.input))
			assert.Equal(t, tc.want, buf.String())
		})
	}
}

func TestWithYAMLComments_errors(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input    any
		err      error
		comments yaml.CommentMap
		// Holds the message of an error that has no sentinel.
		msg string
	}{
		"key that is not a path": {
			input:    map[string]int{"a": 1},
			comments: yaml.CommentMap{"a": {yaml.LineComment(" x")}},
			err:      yaml.ErrInvalidPathString,
		},
		"empty key": {
			input:    map[string]int{"a": 1},
			comments: yaml.CommentMap{"": {yaml.LineComment(" x")}},
			err:      yaml.ErrInvalidPathString,
		},
		"key that ends in an index number": {
			input:    []int{1},
			comments: yaml.CommentMap{"$[0": {yaml.LineComment(" x")}},
			err:      yaml.ErrInvalidPathString,
		},
		"key that ends in an index number under a key": {
			input:    map[string][]int{"a": {1, 2}},
			comments: yaml.CommentMap{"$.a[1": {yaml.LineComment(" x")}},
			err:      yaml.ErrInvalidPathString,
		},
		"key that ends in an index wildcard": {
			input:    []int{1},
			comments: yaml.CommentMap{"$[*": {yaml.LineComment(" x")}},
			err:      yaml.ErrInvalidPathString,
		},
		"path that does not fit the document": {
			input:    map[string]int{"a": 1},
			comments: yaml.CommentMap{"$.a[0]": {yaml.LineComment(" x")}},
			err:      yaml.ErrInvalidQuery,
		},
		"unknown position": {
			input:    map[string]int{"a": 1},
			comments: yaml.CommentMap{"$.a": {{Texts: []string{" x"}, Position: 99}}},
			err:      yaml.ErrUnknownCommentPositionType,
		},
		"head comment on a root scalar": {
			input:    "x",
			comments: yaml.CommentMap{"$": {yaml.HeadComment(" x")}},
			msg:      "unsupported comment head position for String",
		},
		"head comment on an empty root sequence": {
			input:    []string{},
			comments: yaml.CommentMap{"$": {yaml.HeadComment(" x")}},
			msg:      "unsupported comment head position for Sequence",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			enc := encoder.New(&buf, encoder.WithYAMLComments(tc.comments))

			err := enc.Encode(t.Context(), tc.input)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
			} else {
				require.EqualError(t, err, tc.msg)
			}

			assert.Empty(t, buf.String())
		})
	}
}

func TestWithYAMLComments_documents(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	enc := encoder.New(
		&buf,
		encoder.WithYAMLComments(yaml.CommentMap{"$.a": {yaml.LineComment(" replaced")}}),
		encoder.WithYAMLComments(yaml.CommentMap{"$.a": {yaml.LineComment(" count")}}),
	)

	require.NoError(t, enc.Encode(t.Context(), map[string]int{"a": 1}))
	require.NoError(t, enc.Encode(t.Context(), map[string]int{"a": 2}))
	assert.Equal(t, "a: 1 # count\n---\na: 2 # count\n", buf.String())
}

func TestWithYAMLOptions(t *testing.T) {
	t.Parallel()

	type config struct {
		Items []string `yaml:"items"`
	}

	var buf bytes.Buffer

	enc := encoder.New(&buf, encoder.WithYAMLOptions(yaml.Flow(true)))

	require.NoError(t, enc.Encode(t.Context(), config{Items: []string{"one", "two"}}))
	assert.Equal(t, "{items: [one, two]}\n", buf.String())
}
