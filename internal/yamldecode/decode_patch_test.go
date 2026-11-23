package yamldecode_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	up "github.com/goccy/go-yaml"

	yaml "go.jacobcolvin.com/niceyaml/internal/yamldecode"
)

// textValue decodes itself from the text that UnmarshalText gets.
type textValue struct{ s string }

func (t *textValue) UnmarshalText(b []byte) error {
	t.s = string(b)

	return nil
}

// bytesValue decodes itself from the YAML that UnmarshalYAML gets.
type bytesValue struct{ s string }

func (b *bytesValue) UnmarshalYAML(p []byte) error {
	b.s = string(p)

	return nil
}

// reparsed decodes the YAML it gets again with the copy, and wraps the
// error of that decode.
type reparsed struct{ V int }

func (r *reparsed) UnmarshalYAML(b []byte) error {
	var v struct{ V int }

	err := yaml.Unmarshal(b, &v)
	if err != nil {
		return fmt.Errorf("my wrapper: %w", err)
	}

	r.V = v.V

	return nil
}

// bareReparsed decodes the YAML it gets again with the copy, and returns
// the error of that decode as it is.
type bareReparsed struct{ V int }

func (r *bareReparsed) UnmarshalYAML(b []byte) error {
	var v struct{ V int }

	err := yaml.Unmarshal(b, &v)
	if err != nil {
		return err
	}

	r.V = v.V

	return nil
}

// customReparsed is a type that a function from CustomUnmarshaler decodes.
type customReparsed struct{ V int }

// errSentinel is the error that failing returns.
var errSentinel = errors.New("sentinel")

// failing is a type whose UnmarshalYAML returns errSentinel.
type failing struct{}

func (*failing) UnmarshalYAML([]byte) error { return errSentinel }

// callbackHolder decodes a struct whose field holds T through the callback
// of an InterfaceUnmarshaler, and keeps the error of the callback.
type callbackHolder[T any] struct{ err error }

func (c *callbackHolder[T]) UnmarshalYAML(unmarshal func(any) error) error {
	var v struct{ X T }

	c.err = unmarshal(&v)

	return c.err
}

// callbackContextHolder is a callbackHolder that implements
// InterfaceUnmarshalerContext.
type callbackContextHolder[T any] struct{ err error }

func (c *callbackContextHolder[T]) UnmarshalYAML(_ context.Context, unmarshal func(any) error) error {
	var v struct{ X T }

	c.err = unmarshal(&v)

	return c.err
}

// selfPointer and the pair of pointerA and pointerB are pointer types that
// never end in a type that is not a pointer.
type (
	selfPointer *selfPointer
	pointerA    *pointerB
	pointerB    *pointerA
)

// aliasBomb returns a document of depth anchors. Each anchor after the
// first is a sequence of nine aliases of the one before it, so 9^depth
// strings stand behind the last anchor.
func aliasBomb(depth int) string {
	var b strings.Builder

	b.WriteString("a: &a [x,x,x,x,x,x,x,x,x]\n")

	for i := 1; i < depth; i++ {
		cur, prev := string(rune('a'+i)), string(rune('a'+i-1))
		alias := "*" + prev
		fmt.Fprintf(&b, "%s: &%s [%s]\n", cur, cur, strings.Join([]string{
			alias, alias, alias, alias, alias, alias, alias, alias, alias,
		}, ","))
	}

	return b.String()
}

// parseBody parses the first document of src with its comments.
func parseBody(t *testing.T, src string) ast.Node {
	t.Helper()

	f, err := parser.ParseBytes([]byte(src), parser.ParseComments)
	require.NoError(t, err)

	return f.Docs[0].Body
}

func TestUnmarshalInlineMapUnknownFields(t *testing.T) {
	t.Parallel()

	type inner struct {
		Hosts      map[string]string `yaml:",inline"`
		RoundRobin bool              `yaml:"roundRobin"`
	}

	var v struct {
		Extra   map[string]any `yaml:",inline"`
		Name    string         `yaml:"name"`
		Details inner          `yaml:"details"`
	}

	src := "name: x\nfoo: 1\ndetails: {roundRobin: true, h1: a}\n"
	require.NoError(t, yaml.UnmarshalWithOptions([]byte(src), &v, yaml.DisallowUnknownField()))
	assert.Equal(t, "x", v.Name)
	assert.True(t, v.Details.RoundRobin)
	assert.Equal(t, "a", v.Details.Hosts["h1"])
	assert.Equal(t, uint64(1), v.Extra["foo"])

	// A struct without an inline map still rejects a key it does not know,
	// below a struct that holds one.
	var w struct {
		Extra  map[string]any  `yaml:",inline"`
		Strict struct{ A int } `yaml:"strict"`
	}

	err := yaml.UnmarshalWithOptions([]byte("strict: {a: 1, b: 2}\n"), &w, yaml.DisallowUnknownField())

	var unknown *up.UnknownFieldError

	require.ErrorAs(t, err, &unknown)
	assert.Equal(t, `unknown field "b"`, unknown.Message)

	// A field with the alias option that is not inline reads its own key.
	// The decoder knows that key when another struct holds the field's
	// struct inline.
	type ref struct{ A int }

	type held struct {
		Ref *ref `yaml:"ref,alias"`
	}

	var x struct {
		In held `yaml:",inline"`
		B  int  `yaml:"b"`
	}

	require.NoError(t, yaml.UnmarshalWithOptions([]byte("ref: {a: 1}\nb: 2\n"), &x, yaml.DisallowUnknownField()))
	require.NotNil(t, x.In.Ref)
	assert.Equal(t, 1, x.In.Ref.A)
	assert.Equal(t, 2, x.B)
}

func TestUnmarshalIntegerTag(t *testing.T) {
	t.Parallel()

	var v struct {
		D any
		A int
		C int64
		E float64
		B uint8
	}

	src := "a: !!int 5\nb: !!int 6\nc: !!int -7\nd: !!int 8\ne: !!int 9\n"
	require.NoError(t, yaml.Unmarshal([]byte(src), &v))
	assert.Equal(t, 5, v.A)
	assert.Equal(t, uint8(6), v.B)
	assert.Equal(t, int64(-7), v.C)
	assert.Equal(t, 8, v.D)
	assert.InDelta(t, 9.0, v.E, 0)

	// An integer past the range of int keeps its value.
	var wide struct {
		D any
		A uint64
	}

	require.NoError(t, yaml.Unmarshal([]byte("a: !!int 18446744073709551615\nd: !!int 18446744073709551615\n"), &wide))
	assert.Equal(t, uint64(18446744073709551615), wide.A)
	assert.Equal(t, uint64(18446744073709551615), wide.D)

	// A quoted string that spells an integer reads as that integer.
	var quoted struct {
		D any
		A int
	}

	require.NoError(t, yaml.Unmarshal([]byte("a: !!int \"0x10\"\nd: !!int '-5'\n"), &quoted))
	assert.Equal(t, 16, quoted.A)
	assert.Equal(t, -5, quoted.D)

	// The range check still applies.
	tcs := map[string]struct {
		input string
	}{
		"too large": {input: "b: !!int 300\n"},
		"negative":  {input: "b: !!int -1\n"},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var o struct{ B uint8 }

			var overflow *up.OverflowError

			require.ErrorAs(t, yaml.Unmarshal([]byte(tc.input), &o), &overflow)
		})
	}

	// A value under !!int that is not an integer fails, into an integer
	// field and into an interface.
	rejected := map[string]struct {
		input string
	}{
		"string":                   {input: "a: !!int abc\n"},
		"quoted fraction":          {input: "a: !!int \"1.5\"\n"},
		"quoted string past int64": {input: "a: !!int \"-99999999999999999999\"\n"},
		"fraction":                 {input: "a: !!int 1.5\n"},
		"past uint64":              {input: "a: !!int 99999999999999999999\n"},
		"null":                     {input: "a: !!int null\n"},
		"empty value":              {input: "a: !!int\n"},
		"boolean":                  {input: "a: !!int true\n"},
		"string into an interface": {input: "d: !!int abc\n"},
	}

	for name, tc := range rejected {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var o struct {
				D any
				A int64
			}

			var typeErr *up.TypeError

			require.ErrorAs(t, yaml.Unmarshal([]byte(tc.input), &o), &typeErr)
			assert.Equal(t, reflect.TypeFor[int](), typeErr.DstType)
		})
	}
}

func TestUnmarshalAliasInsideItsAnchor(t *testing.T) {
	t.Parallel()

	var text struct{ A map[string]textValue }

	require.NoError(t, yaml.Unmarshal([]byte("a: &x {k: *x}\n"), &text))
	assert.Equal(t, map[string]textValue{"k": {}}, text.A)

	type node struct{ K *node }

	var tree struct{ A *node }

	require.NoError(t, yaml.Unmarshal([]byte("a: &x {k: *x}\n"), &tree))
	require.NotNil(t, tree.A)
	assert.Nil(t, tree.A.K)

	// The formatter writes the alias as null in the text it hands an
	// unmarshaler. Go-yaml writes the anchor again for each such alias, and
	// that text never ends.
	var raw struct{ A bytesValue }

	require.NoError(t, yaml.Unmarshal([]byte("a: &x {k: *x, j: 1}\n"), &raw))
	assert.Equal(t, "{k: null, j: 1}", raw.A.s)
}

func TestUnmarshalContextEnds(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	var v struct{ D [][][][]string }

	err := yaml.UnmarshalContext(ctx, []byte(aliasBomb(4)), &v)
	require.ErrorIs(t, err, context.Canceled)

	// A decoder that a context stopped decodes again under a live one.
	dec := yaml.NewDecoder(bytes.NewReader(nil))
	body := parseBody(t, strings.Repeat("- x\n", 1000))

	var stopped, live any

	require.ErrorIs(t, dec.DecodeFromNodeContext(ctx, body, &stopped), context.Canceled)
	require.NoError(t, dec.DecodeFromNodeContext(t.Context(), body, &live))
}

func TestAliasBudget(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		decode func() error
		err    error
	}{
		"nodes past the budget": {
			decode: func() error {
				var v struct{ D [][][][]string }

				return yaml.UnmarshalWithOptions([]byte(aliasBomb(4)), &v, yaml.AliasBudget(10_000, 1<<20))
			},
			err: yaml.ErrAliasBudget,
		},
		"nodes inside the budget": {
			decode: func() error {
				var v struct{ C [][][]string }

				err := yaml.UnmarshalWithOptions([]byte(aliasBomb(3)), &v, yaml.AliasBudget(10_000, 1<<20))
				if err == nil && len(v.C) != 9 {
					return fmt.Errorf("got %d elements", len(v.C))
				}

				return err
			},
		},
		"text past the budget": {
			decode: func() error {
				var v struct{ D bytesValue }

				return yaml.UnmarshalWithOptions([]byte(aliasBomb(4)), &v, yaml.AliasBudget(1<<30, 1000))
			},
			err: yaml.ErrAliasBudget,
		},
		"text of an UnmarshalText past the budget": {
			decode: func() error {
				var v struct{ D textValue }

				return yaml.UnmarshalWithOptions([]byte(aliasBomb(4)), &v, yaml.AliasBudget(1<<30, 1000))
			},
			err: yaml.ErrAliasBudget,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := tc.decode()
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				return
			}

			require.NoError(t, err)
		})
	}
}

func TestUnmarshalWrappedTypeError(t *testing.T) {
	t.Parallel()

	reparse := yaml.CustomUnmarshaler(func(_ *customReparsed, _ []byte) error {
		var v struct{ V int }

		return yaml.Unmarshal([]byte("v: x\n"), &v)
	})

	tcs := map[string]struct {
		decode func() error
		prefix string
	}{
		"unmarshaler that wraps the error": {
			decode: func() error {
				var v struct{ W reparsed }

				return yaml.Unmarshal([]byte("w:\n  v: x\n"), &v)
			},
			prefix: "my wrapper: ",
		},
		"unmarshaler that returns the error as it is": {
			decode: func() error {
				var v struct{ W bareReparsed }

				return yaml.Unmarshal([]byte("w:\n  v: x\n"), &v)
			},
		},
		"function that returns the error as it is": {
			decode: func() error {
				var v struct{ W customReparsed }

				return yaml.UnmarshalWithOptions([]byte("w:\n  v: x\n"), &v, reparse)
			},
		},
		"field below an inline struct": {
			decode: func() error {
				var v struct {
					In struct{ W bareReparsed } `yaml:",inline"`
				}

				return yaml.Unmarshal([]byte("w:\n  v: x\n"), &v)
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := tc.decode()
			require.Error(t, err)
			assert.True(t, strings.HasPrefix(err.Error(), tc.prefix), err.Error())

			// The decoder does not write the name of the outer field into
			// the TypeError of the inner decode.
			var te *up.TypeError

			require.ErrorAs(t, err, &te)
			require.NotNil(t, te.StructFieldName)
			assert.Equal(t, ".V", *te.StructFieldName)
		})
	}
}

func TestUnmarshalInterfaceUnmarshalerCallback(t *testing.T) {
	t.Parallel()

	// The callback returns the error of an inner unmarshaler as the inner
	// unmarshaler returned it.
	tcs := map[string]struct {
		decode func() (callbackErr, err error)
		check  func(t *testing.T, err error)
	}{
		"sentinel": {
			decode: func() (error, error) {
				var c callbackHolder[failing]

				err := yaml.Unmarshal([]byte("x: 1\n"), &c)

				return c.err, err
			},
			check: func(t *testing.T, err error) {
				t.Helper()

				assert.Same(t, errSentinel, err)
			},
		},
		"sentinel with a context": {
			decode: func() (error, error) {
				var c callbackContextHolder[failing]

				err := yaml.Unmarshal([]byte("x: 1\n"), &c)

				return c.err, err
			},
			check: func(t *testing.T, err error) {
				t.Helper()

				assert.Same(t, errSentinel, err)
			},
		},
		"bare TypeError": {
			decode: func() (error, error) {
				var c callbackHolder[bareReparsed]

				err := yaml.Unmarshal([]byte("x:\n  v: x\n"), &c)

				return c.err, err
			},
			check: func(t *testing.T, err error) {
				t.Helper()

				//nolint:errorlint // The test checks the type of the error itself.
				te, ok := err.(*up.TypeError)
				require.True(t, ok, "%T", err)
				require.NotNil(t, te.StructFieldName)
				assert.Equal(t, ".V", *te.StructFieldName)
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			callbackErr, err := tc.decode()
			require.Error(t, callbackErr)
			tc.check(t, callbackErr)

			// The decode returns the error that the outer unmarshaler
			// returned.
			tc.check(t, err)
		})
	}
}

func TestUnmarshalPointerBelowTarget(t *testing.T) {
	t.Parallel()

	type one struct{ A int }

	type two struct{ A, B int }

	var nilPtr *one

	require.NoError(t, yaml.Unmarshal([]byte("a: 1\n"), &nilPtr))
	assert.Equal(t, &one{A: 1}, nilPtr)

	var nilPtrPtr **one

	require.NoError(t, yaml.Unmarshal([]byte("a: 2\n"), &nilPtrPtr))
	require.NotNil(t, nilPtrPtr)
	assert.Equal(t, &one{A: 2}, *nilPtrPtr)

	// The decode keeps the fields of the held value that the document
	// omits.
	held := &two{B: 7}

	require.NoError(t, yaml.Unmarshal([]byte("a: 1\n"), &held))
	assert.Equal(t, &two{A: 1, B: 7}, held)

	// A null leaves a nil pointer nil, and clears one that holds a value.
	var null *one

	require.NoError(t, yaml.Unmarshal([]byte("null\n"), &null))
	assert.Nil(t, null)

	cleared := &one{A: 3}

	require.NoError(t, yaml.Unmarshal([]byte("~\n"), &cleared))
	assert.Nil(t, cleared)
}

func TestUnmarshalEndlessPointerType(t *testing.T) {
	t.Parallel()

	// The TypeError reports the Go type of the value the document holds.
	tcs := map[string]struct {
		target any
		want   reflect.Type
		input  string
	}{
		"self": {
			target: &struct{ P selfPointer }{},
			input:  "p: 1\n",
			want:   reflect.TypeFor[uint64](),
		},
		"cycle": {
			target: &struct{ P pointerA }{},
			input:  "p: 1\n",
			want:   reflect.TypeFor[uint64](),
		},
		"string": {
			target: &struct{ P selfPointer }{},
			input:  "p: x\n",
			want:   reflect.TypeFor[string](),
		},
		"mapping": {
			target: &struct{ P selfPointer }{},
			input:  "p: {a: 1}\n",
			want:   reflect.TypeFor[map[string]any](),
		},
		"sequence": {
			target: new(selfPointer),
			input:  "[1]\n",
			want:   reflect.TypeFor[[]any](),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			done := make(chan error, 1)

			go func() {
				done <- yaml.Unmarshal([]byte(tc.input), tc.target)
			}()

			select {
			case err := <-done:
				var te *up.TypeError

				require.ErrorAs(t, err, &te)
				assert.Equal(t, tc.want, te.SrcType)

			case <-time.After(10 * time.Second):
				require.FailNow(t, "the decode did not return")
			}
		})
	}
}

func TestDecodeFromNodeAliasToNull(t *testing.T) {
	t.Parallel()

	body := parseBody(t, "a: &x null\nb: *x\n")
	mapping, ok := body.(*ast.MappingNode)
	require.True(t, ok)

	// The decoder reads the anchor in the first decode, as a decode of a
	// node below the document needs.
	dec := yaml.NewDecoder(bytes.NewReader(nil))

	var sink any

	require.NoError(t, dec.DecodeFromNode(body, &sink))

	n := 5
	p := &n

	require.NoError(t, dec.DecodeFromNode(mapping.Values[1].Value, &p))
	assert.Nil(t, p)
}

func TestUnmarshalTextReadsTokensOnce(t *testing.T) {
	t.Parallel()

	// Go-yaml reads every token of the parse for each value that an
	// unmarshaler reads, so its time grows with the square of those values.
	const n = 2000

	src := []byte("l:\n" + strings.Repeat("  - v\n", n))

	var stock struct{ L []textValue }

	start := time.Now()

	require.NoError(t, up.Unmarshal(src, &stock))

	upstream := time.Since(start)

	var v struct{ L []textValue }

	start = time.Now()

	require.NoError(t, yaml.Unmarshal(src, &v))

	copied := time.Since(start)

	require.Len(t, v.L, n)
	assert.Equal(t, "v", v.L[n-1].s)
	assert.Less(t, copied*5, upstream, "the copy took %v, go-yaml %v", copied, upstream)
}

func TestUnmarshalMissingMergeAnchor(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		target any
	}{
		"map":    {target: &map[string]map[string]int{}},
		"struct": {target: &struct{ M struct{ A int } }{}},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := yaml.Unmarshal([]byte("m: {<<: *nope}\n"), tc.target)

			var missing *yaml.MissingAnchorError

			require.ErrorAs(t, err, &missing)
			assert.Equal(t, 9, missing.Alias.GetToken().Position.Column)
			assert.EqualError(t, err, "cannot find anchor by alias name nope")
		})
	}
}

func TestDecodeFromNodeAliasWithComment(t *testing.T) {
	t.Parallel()

	body := parseBody(t, "a: &x 1\nb: *x # note\n")

	var v any

	require.NoError(t, yaml.NewDecoder(bytes.NewReader(nil)).DecodeFromNode(body, &v))
	assert.Equal(t, map[string]any{"a": uint64(1), "b": uint64(1)}, v)
}
