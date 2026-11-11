package encoder_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/encoder"
)

var (
	// The marshaler that writes a [net.IPNet] as its CIDR text.
	cidr = encoder.CustomMarshaler(func(_ context.Context, n net.IPNet) (any, error) {
		return n.String(), nil
	})

	// The network 10.0.0.0/8.
	tenNet = net.IPNet{IP: net.IPv4(10, 0, 0, 0).To4(), Mask: net.CIDRMask(8, 32)}
)

// quantity writes JSON alone, as the string of n with an "m" after it.
type quantity struct {
	n int
}

func (q quantity) MarshalJSON() ([]byte, error) {
	return fmt.Appendf(nil, `"%dm"`, q.n), nil
}

// jsonText writes JSON alone, as the JSON of its value.
type jsonText struct {
	v any
}

func (j jsonText) MarshalJSON() ([]byte, error) {
	//nolint:wrapcheck // The test returns the error of the JSON encoder.
	return json.Marshal(j.v)
}

// textQuantity writes text and JSON. The encoder writes its text.
type textQuantity struct{}

func (q textQuantity) MarshalJSON() ([]byte, error) {
	return []byte(`"json"`), nil
}

func (q textQuantity) MarshalText() ([]byte, error) {
	return []byte("text"), nil
}

// ownYAML writes itself through a MarshalYAML method.
type ownYAML struct{}

func (o ownYAML) MarshalYAML() (any, error) {
	return "method", nil
}

// note is a type whose marshaler returns its text.
type note struct {
	text string
}

// hop is a type whose marshaler returns a collection.
type hop struct {
	name string
	next []net.IPNet
}

func TestCustomMarshaler(t *testing.T) {
	t.Parallel()

	type route struct {
		Net  net.IPNet  `yaml:"net"`
		Via  *net.IPNet `yaml:"via"`
		Gate *net.IPNet `yaml:"gate"`
	}

	errSecret := errors.New("secret")

	hops := encoder.CustomMarshaler(func(_ context.Context, h hop) (any, error) {
		return map[string]any{"name": h.name, "next": h.next}, nil
	})
	notes := encoder.CustomMarshaler(func(_ context.Context, n note) (any, error) {
		return n.text, nil
	})

	tcs := map[string]struct {
		input any
		want  string
		err   error
		// The message of the error, when set.
		msg  string
		opts []encoder.Option
	}{
		"value, pointer, and nil pointer": {
			input: route{Net: tenNet, Via: &tenNet},
			opts:  []encoder.Option{encoder.WithMarshalers(cidr)},
			want:  "net: 10.0.0.0/8\nvia: 10.0.0.0/8\ngate: null\n",
		},
		"without the marshaler": {
			input: tenNet,
			want:  "ip: 10.0.0.0\nmask:\n- 255\n- 0\n- 0\n- 0\n",
		},
		"a collection under the layout of the options": {
			input: map[string]any{"hops": []hop{{name: "a", next: []net.IPNet{tenNet}}}},
			opts:  []encoder.Option{encoder.Pretty(), encoder.WithMarshalers(hops, cidr)},
			want:  "hops:\n  - name: a\n    next:\n      - 10.0.0.0/8\n",
		},
		"a string that reads as another value": {
			input: map[string]hop{"h": {name: "1e3"}},
			opts:  []encoder.Option{encoder.WithMarshalers(hops)},
			want:  "h:\n  name: \"1e3\"\n  next: []\n",
		},
		"a string with a line break": {
			input: map[string]any{"a": map[string]hop{"b": {name: "x\ny\n"}}},
			opts:  []encoder.Option{encoder.WithMarshalers(hops)},
			want:  "a:\n  b:\n    name: \"x\\ny\\n\"\n    next: []\n",
		},
		"a string with an indented line in a nested sequence": {
			input: map[string]map[string][]note{"a": {"b": {{text: "x\n  y"}}}},
			opts:  []encoder.Option{encoder.WithMarshalers(notes)},
			want:  "a:\n  b:\n  - \"x\\n  y\"\n",
		},
		"a string with an indented line in a sequence under Pretty": {
			input: map[string][]note{"a": {{text: "x\n  y\nz"}}},
			opts:  []encoder.Option{encoder.Pretty(), encoder.WithMarshalers(notes)},
			want:  "a:\n  - \"x\\n  y\\nz\"\n",
		},
		"a string with even lines in a nested sequence": {
			input: map[string]map[string][]note{"a": {"b": {{text: "x\ny"}}}},
			opts:  []encoder.Option{encoder.WithMarshalers(notes)},
			want:  "a:\n  b:\n  - |-\n      x\n      y\n",
		},
		"a mapping under a key in a sequence under Pretty": {
			input: []struct {
				Name string `yaml:"name"`
				Via  hop    `yaml:"via"`
			}{{Name: "r", Via: hop{name: "a"}}},
			opts: []encoder.Option{encoder.Pretty(), encoder.WithMarshalers(hops)},
			want: "- name: r\n  via:\n    name: a\n    next: []\n",
		},
		"a mapping under a key in a nested sequence with an indent of four": {
			input: map[string][]map[string]hop{"hops": {{"h": {name: "a", next: []net.IPNet{tenNet}}}}},
			opts: []encoder.Option{
				encoder.WithIndent(4), encoder.WithIndentSequence(true), encoder.WithMarshalers(hops, cidr),
			},
			want: "hops:\n    - h:\n          name: a\n          next:\n              - 10.0.0.0/8\n",
		},
		"a sequence under a key in a sequence under Pretty": {
			input: []map[string]note{{"k": {text: "x"}}},
			opts: []encoder.Option{encoder.Pretty(), encoder.WithMarshalers(encoder.CustomMarshaler(
				func(_ context.Context, n note) (any, error) { return []string{n.text}, nil },
			))},
			want: "- k:\n    - x\n",
		},
		"a root sequence of strings under Pretty": {
			input: []note{{text: "b"}},
			opts:  []encoder.Option{encoder.Pretty(), encoder.WithMarshalers(notes)},
			want:  "- b\n",
		},
		"a root sequence of mappings under Pretty": {
			input: []hop{{name: "a"}},
			opts:  []encoder.Option{encoder.Pretty(), encoder.WithMarshalers(hops)},
			want:  "- name: a\n  next: []\n",
		},
		"a root sequence of literal blocks under Pretty": {
			input: []note{{text: "x\ny"}},
			opts:  []encoder.Option{encoder.Pretty(), encoder.WithMarshalers(notes)},
			want:  "- |-\n  x\n  y\n",
		},
		"a root sequence of structs with a string field under Pretty": {
			input: []struct {
				Name string `yaml:"name"`
				Via  note   `yaml:"via"`
				Desc string `yaml:"desc"`
			}{{Name: "r", Via: note{text: "b"}, Desc: "d\ne"}},
			opts: []encoder.Option{encoder.Pretty(), encoder.WithMarshalers(notes)},
			want: "- name: r\n  via: b\n  desc: |-\n    d\n    e\n",
		},
		"a string that ends in two line breaks": {
			input: note{text: "b\n\n"},
			opts:  []encoder.Option{encoder.WithMarshalers(notes)},
			want:  "\"b\\n\\n\"\n",
		},
		"a string that ends in two line breaks under a key": {
			input: map[string]note{"k": {text: "b\n\n"}},
			opts:  []encoder.Option{encoder.WithMarshalers(notes)},
			want:  "k: \"b\\n\\n\"\n",
		},
		"a string that ends in two line breaks in a sequence": {
			input: []note{{text: "b\n\n"}},
			opts:  []encoder.Option{encoder.WithMarshalers(notes)},
			want:  "- \"b\\n\\n\"\n",
		},
		"a nil function removes the function before it": {
			input: tenNet,
			opts:  []encoder.Option{encoder.WithMarshalers(cidr, encoder.CustomMarshaler[net.IPNet](nil))},
			want:  "ip: 10.0.0.0\nmask:\n- 255\n- 0\n- 0\n- 0\n",
		},
		"a later function replaces the function before it": {
			input: tenNet,
			opts: []encoder.Option{
				encoder.WithMarshalers(cidr),
				encoder.WithMarshalers(encoder.CustomMarshaler(func(context.Context, net.IPNet) (any, error) {
					return "net", nil
				})),
			},
			want: "net\n",
		},
		"the zero Marshaler": {
			input: tenNet,
			opts:  []encoder.Option{encoder.WithMarshalers(cidr, encoder.Marshaler{})},
			want:  "10.0.0.0/8\n",
		},
		"ahead of a MarshalYAML method": {
			input: struct {
				O ownYAML `yaml:"o"`
			}{},
			opts: []encoder.Option{encoder.WithMarshalers(encoder.CustomMarshaler(
				func(context.Context, ownYAML) (any, error) { return "function", nil },
			))},
			want: "o: function\n",
		},
		"a MarshalYAML method of a value inside an interface": {
			input: map[string]any{"o": ownYAML{}},
			opts: []encoder.Option{encoder.WithMarshalers(encoder.CustomMarshaler(
				func(context.Context, ownYAML) (any, error) { return "function", nil },
			))},
			want: "o: method\n",
		},
		"an error of the function": {
			input: tenNet,
			opts: []encoder.Option{encoder.WithMarshalers(encoder.CustomMarshaler(
				func(context.Context, net.IPNet) (any, error) { return nil, errSecret },
			))},
			err: errSecret,
		},
		"a function that returns its own type": {
			input: tenNet,
			opts: []encoder.Option{encoder.WithMarshalers(encoder.CustomMarshaler(
				func(_ context.Context, n net.IPNet) (any, error) { return &n, nil },
			))},
			err: encoder.ErrEncode,
		},
		"a function whose value encodes to no node": {
			// The encode of the value already labels its error, so the error
			// keeps one label.
			input: tenNet,
			opts: []encoder.Option{encoder.WithMarshalers(encoder.CustomMarshaler(
				func(context.Context, net.IPNet) (any, error) { return emptyMarshaler{}, nil },
			))},
			err: encoder.ErrNoNode,
			msg: "value encodes to no YAML node",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := encoder.Marshal(t.Context(), tc.input, tc.opts...)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				require.ErrorIs(t, err, encoder.ErrEncode)

				if tc.msg != "" {
					assert.Equal(t, tc.msg, err.Error())
				}

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, string(got))

			var read any

			require.NoError(t, yaml.Unmarshal(got, &read))
		})
	}
}

func TestCustomMarshaler_context(t *testing.T) {
	t.Parallel()

	type key struct{}

	ctx := context.WithValue(t.Context(), key{}, "from the context")

	got, err := encoder.Marshal(ctx, tenNet, encoder.WithMarshalers(encoder.CustomMarshaler(
		func(ctx context.Context, _ net.IPNet) (any, error) { return ctx.Value(key{}), nil },
	)))
	require.NoError(t, err)
	assert.Equal(t, "from the context\n", string(got))
}

func TestCustomMarshaler_panics(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		marshaler func()
		want      string
	}{
		"pointer type": {
			marshaler: func() {
				encoder.CustomMarshaler(func(context.Context, *net.IPNet) (any, error) { return "net", nil })
			},
			want: "encoder.CustomMarshaler: *net.IPNet is a pointer type, " +
				"which the encoder writes through the type it points to",
		},
		"interface type": {
			marshaler: func() {
				encoder.CustomMarshaler(func(_ context.Context, s fmt.Stringer) (any, error) { return s.String(), nil })
			},
			want: "encoder.CustomMarshaler: fmt.Stringer is an interface type, " +
				"for which the encoder cannot register a function",
		},
		"empty interface type": {
			marshaler: func() {
				encoder.CustomMarshaler[any](nil)
			},
			want: "encoder.CustomMarshaler: interface {} is an interface type, " +
				"for which the encoder cannot register a function",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.PanicsWithValue(t, tc.want, tc.marshaler)
		})
	}
}

func TestJSONMarshalers(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input any
		err   error
		want  string
		opts  []encoder.Option
	}{
		"a string with a YAML indicator that go-yaml converts to a mapping": {
			input: map[string]any{"k": jsonText{v: "? x"}},
			opts:  []encoder.Option{encoder.WithMarshalers(encoder.JSONMarshalers(true))},
			want:  "k:\n  ? x:\n",
		},
		"a string of a tab that go-yaml converts to no value": {
			input: map[string]any{"k": jsonText{v: "\t"}},
			opts:  []encoder.Option{encoder.WithMarshalers(encoder.JSONMarshalers(true))},
			err:   encoder.ErrNoNode,
		},
		"a document marker that go-yaml converts to no value": {
			input: map[string]any{"k": jsonText{v: "---"}},
			opts:  []encoder.Option{encoder.WithMarshalers(encoder.JSONMarshalers(true))},
			err:   encoder.ErrNoNode,
		},
		"on": {
			input: map[string]any{"cpu": quantity{n: 500}},
			opts:  []encoder.Option{encoder.WithMarshalers(encoder.JSONMarshalers(true))},
			want:  "cpu: 500m\n",
		},
		"off": {
			input: map[string]any{"cpu": quantity{n: 500}},
			want:  "cpu: {}\n",
		},
		"the last one applies": {
			input: map[string]any{"cpu": quantity{n: 500}},
			opts: []encoder.Option{
				encoder.WithMarshalers(encoder.JSONMarshalers(true)),
				encoder.WithMarshalers(encoder.JSONMarshalers(false)),
			},
			want: "cpu: {}\n",
		},
		"a small float that go-yaml converts to a string": {
			input: map[string]any{"k": jsonText{v: 1e-7}},
			opts:  []encoder.Option{encoder.WithMarshalers(encoder.JSONMarshalers(true))},
			want:  "k: \"1e-7\"\n",
		},
		"an integer beyond 64 bits that go-yaml converts to a string": {
			input: map[string]any{"k": jsonText{v: json.Number("18446744073709551616")}},
			opts:  []encoder.Option{encoder.WithMarshalers(encoder.JSONMarshalers(true))},
			want:  "k: \"18446744073709551616\"\n",
		},
		"an object key that go-yaml converts to a merge key": {
			input: map[string]any{"k": jsonText{v: map[string]any{"<<": map[string]int{"a": 1}}}},
			opts:  []encoder.Option{encoder.WithMarshalers(encoder.JSONMarshalers(true))},
			want:  "k:\n  <<:\n    a: 1\n",
		},
		"a MarshalText method first": {
			input: map[string]any{"cpu": textQuantity{}},
			opts:  []encoder.Option{encoder.WithMarshalers(encoder.JSONMarshalers(true))},
			want:  "cpu: text\n",
		},
		"a string with a line break at the top": {
			input: jsonText{v: "x\ny\n"},
			opts:  []encoder.Option{encoder.WithMarshalers(encoder.JSONMarshalers(true))},
			want:  "|\n  x\n  y\n",
		},
		"a string with a line break under a key": {
			input: map[string]any{"a": jsonText{v: "x\ny\n"}},
			opts:  []encoder.Option{encoder.WithMarshalers(encoder.JSONMarshalers(true))},
			want:  "a: |\n  x\n  y\n",
		},
		"a string with a line break under a nested key": {
			input: map[string]any{"a": map[string]any{"b": jsonText{v: "x\ny\n"}}},
			opts:  []encoder.Option{encoder.WithMarshalers(encoder.JSONMarshalers(true))},
			want:  "a:\n  b: \"x\\ny\\n\"\n",
		},
		"a string with an indented line in a nested sequence": {
			input: map[string]any{"a": map[string]any{"b": []jsonText{{v: "x\n  y"}}}},
			opts:  []encoder.Option{encoder.WithMarshalers(encoder.JSONMarshalers(true))},
			want:  "a:\n  b:\n  - \"x\\n  y\"\n",
		},
		"a string with an indented line in a sequence under Pretty": {
			input: map[string]any{"a": []jsonText{{v: "x\n  y\nz"}}},
			opts:  []encoder.Option{encoder.Pretty(), encoder.WithMarshalers(encoder.JSONMarshalers(true))},
			want:  "a:\n  - \"x\\n  y\\nz\"\n",
		},
		"an object with a string with a line break": {
			input: map[string]any{"a": map[string]any{"b": jsonText{v: map[string]any{"c": "x\ny", "d": []int{1}}}}},
			opts:  []encoder.Option{encoder.WithMarshalers(encoder.JSONMarshalers(true))},
			want:  "a:\n  b:\n    c: \"x\\ny\"\n    d:\n    - 1\n",
		},
		"an object under a key in a sequence under Pretty": {
			input: map[string][]struct {
				Name string   `yaml:"name"`
				Res  jsonText `yaml:"res"`
			}{"pods": {{Name: "p", Res: jsonText{v: map[string]string{"cpu": "1"}}}}},
			opts: []encoder.Option{encoder.Pretty(), encoder.WithMarshalers(encoder.JSONMarshalers(true))},
			want: "pods:\n  - name: p\n    res:\n      cpu: \"1\"\n",
		},
		"a string that ends in two line breaks": {
			input: jsonText{v: "foo\n\n"},
			opts:  []encoder.Option{encoder.WithMarshalers(encoder.JSONMarshalers(true))},
			want:  "\"foo\\n\\n\"\n",
		},
		"a string that ends in two line breaks under a key": {
			input: map[string]any{"k": jsonText{v: "foo\n\n"}},
			opts:  []encoder.Option{encoder.WithMarshalers(encoder.JSONMarshalers(true))},
			want:  "k: \"foo\\n\\n\"\n",
		},
		"a string that ends in two line breaks in a sequence": {
			input: []jsonText{{v: "foo\n\n"}},
			opts:  []encoder.Option{encoder.WithMarshalers(encoder.JSONMarshalers(true))},
			want:  "- \"foo\\n\\n\"\n",
		},
		"inside the value of a function": {
			input: tenNet,
			opts: []encoder.Option{encoder.WithMarshalers(
				encoder.JSONMarshalers(true),
				encoder.CustomMarshaler(func(context.Context, net.IPNet) (any, error) {
					return []quantity{{n: 1}}, nil
				}),
			)},
			want: "- 1m\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := encoder.Marshal(t.Context(), tc.input, tc.opts...)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				require.ErrorIs(t, err, encoder.ErrEncode)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, string(got))

			var read any

			require.NoError(t, yaml.Unmarshal(got, &read))
		})
	}
}

func TestWithMarshalers_copies(t *testing.T) {
	t.Parallel()

	ms := []encoder.Marshaler{cidr}
	opt := encoder.WithMarshalers(ms...)
	ms[0] = encoder.CustomMarshaler[net.IPNet](nil)

	got, err := encoder.Marshal(t.Context(), tenNet, opt)
	require.NoError(t, err)
	assert.Equal(t, "10.0.0.0/8\n", string(got))
}
