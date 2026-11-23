package yamldecode_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
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

func TestUnmarshalMergeInsideItsAnchor(t *testing.T) {
	t.Parallel()

	// Without a resolver too, a merge of an alias inside the anchor that it
	// names fails with a MergeLoopError at the alias. Go-yaml does not find
	// the anchor there.
	docs := map[string]struct {
		src string
		col int
	}{
		"alias":    {src: "a: &a {<<: *a, x: 1}\n", col: 12},
		"sequence": {src: "a: &a {<<: [*a], x: 1}\n", col: 13},
	}

	targets := map[string]func() any{
		"any": func() any { return new(any) },
		"map": func() any { return new(map[string]map[string]int) },
	}

	for docName, doc := range docs {
		for targetName, target := range targets {
			t.Run(docName+"/"+targetName, func(t *testing.T) {
				t.Parallel()

				err := yaml.NewDecoder(strings.NewReader(doc.src)).Decode(target())

				var loop *yaml.MergeLoopError

				require.ErrorAs(t, err, &loop)
				assert.Equal(t, doc.col, loop.Alias.GetToken().Position.Column)
				assert.EqualError(t, loop, "merge key loops back to anchor a")
			})
		}
	}
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

	// The callback returns the error of an inner unmarshaler at the node of
	// the inner value, and an outer unmarshaler that returns that error
	// unchanged leaves it at that node.
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

				assert.ErrorIs(t, err, errSentinel)
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

				assert.ErrorIs(t, err, errSentinel)
			},
		},
		"bare TypeError": {
			decode: func() (error, error) {
				var c callbackHolder[bareReparsed]

				err := yaml.Unmarshal([]byte("x: {v: x}\n"), &c)

				return c.err, err
			},
			check: func(t *testing.T, err error) {
				t.Helper()

				var te *up.TypeError

				require.ErrorAs(t, err, &te)
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

			var own *yaml.UnmarshalerError

			require.ErrorAs(t, callbackErr, &own)
			assert.Equal(t, 4, own.Node.GetToken().Position.Column)

			// The decode returns the error that the outer unmarshaler
			// returned.
			assert.Same(t, callbackErr, err)
		})
	}
}

// failingText is a text type whose UnmarshalText returns errSentinel.
type failingText struct{}

func (*failingText) UnmarshalText([]byte) error { return errSentinel }

// failingKey is a text type that returns errSentinel for the key "bad".
type failingKey string

func (k *failingKey) UnmarshalText(b []byte) error {
	if string(b) == "bad" {
		return errSentinel
	}

	*k = failingKey(b)

	return nil
}

// failingHolder decodes a struct whose field fails through the callback
// of an InterfaceUnmarshaler.
type failingHolder struct{ In failing }

func (h *failingHolder) UnmarshalYAML(unmarshal func(any) error) error {
	type plain failingHolder

	return unmarshal((*plain)(h))
}

// wrappingHolder decodes a struct whose field fails through the callback
// of an InterfaceUnmarshaler, and wraps the error of the callback.
type wrappingHolder struct{ In failing }

func (h *wrappingHolder) UnmarshalYAML(unmarshal func(any) error) error {
	type plain wrappingHolder

	err := unmarshal((*plain)(h))
	if err != nil {
		return fmt.Errorf("holder: %w", err)
	}

	return nil
}

func TestUnmarshalUnmarshalerErrorNode(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		decode func() error
		cause  error
		msg    string
		line   int
		col    int
	}{
		"bytes unmarshaler": {
			decode: func() error {
				var v struct {
					X int
					B failing
				}

				return yaml.Unmarshal([]byte("x: 1\nb: 2\n"), &v)
			},
			cause: errSentinel,
			line:  2,
			col:   4,
		},
		"element": {
			decode: func() error {
				var v struct{ L []failing }

				return yaml.Unmarshal([]byte("l:\n  - 1\n"), &v)
			},
			cause: errSentinel,
			line:  2,
			col:   5,
		},
		"text unmarshaler in a map": {
			decode: func() error {
				var v struct{ M map[string]failingText }

				return yaml.Unmarshal([]byte("m:\n  k: v\n"), &v)
			},
			cause: errSentinel,
			line:  2,
			col:   6,
		},
		"duration": {
			decode: func() error {
				var v struct{ T time.Duration }

				return yaml.Unmarshal([]byte("t: soon\n"), &v)
			},
			msg:  `time: invalid duration "soon"`,
			line: 1,
			col:  4,
		},
		"map key": {
			decode: func() error {
				var v map[failingKey]int

				return yaml.Unmarshal([]byte("a: 1\nbad: 2\n"), &v)
			},
			cause: errSentinel,
			line:  2,
			col:   1,
		},
		"deepest value": {
			decode: func() error {
				var v struct{ O failingHolder }

				return yaml.Unmarshal([]byte("o:\n  in: 5\n"), &v)
			},
			cause: errSentinel,
			line:  2,
			col:   7,
		},
		"wrapped by the outer unmarshaler": {
			decode: func() error {
				var v struct{ O wrappingHolder }

				return yaml.Unmarshal([]byte("o:\n  in: 5\n"), &v)
			},
			cause: errSentinel,
			msg:   "holder: sentinel",
			line:  2,
			col:   5,
		},
		"mapping for a text type": {
			decode: func() error {
				var v struct{ T failingText }

				return yaml.Unmarshal([]byte("t: {a: 1}\n"), &v)
			},
			cause: yaml.ErrTextOnly,
			line:  1,
			col:   4,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := tc.decode()

			var own *yaml.UnmarshalerError

			require.ErrorAs(t, err, &own)

			if tc.cause != nil {
				require.ErrorIs(t, err, tc.cause)
			}

			if tc.msg != "" {
				require.EqualError(t, err, tc.msg)
			}

			pos := own.Node.GetToken().Position
			assert.Equal(t, []int{tc.line, tc.col}, []int{pos.Line, pos.Column})
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

// binder binds each alias to the last anchor of its name that starts
// before the alias, as the resolver of the paths package binds it. It binds
// each alias on a line of nulls to a null that holds the token of the alias.
type binder struct {
	last  map[string]ast.Node
	bound map[*ast.AliasNode]ast.Node
	nulls map[int]bool
}

func (b *binder) Visit(n ast.Node) ast.Visitor {
	switch n := n.(type) {
	case *ast.AnchorNode:
		b.last[n.Name.GetToken().Value] = n.Value

	case *ast.AliasNode:
		if b.nulls[n.Start.Position.Line] {
			b.bound[n] = ast.Null(n.Start)
		} else if node, ok := b.last[n.Value.GetToken().Value]; ok {
			b.bound[n] = node
		}
	}

	return b
}

// documentOrder returns a [yaml.ResolveAlias] option that binds the aliases
// of root as [binder] binds them, with each alias on a line of nulls bound
// to a null.
func documentOrder(root ast.Node, nulls ...int) yaml.DecodeOption {
	b := &binder{last: map[string]ast.Node{}, bound: map[*ast.AliasNode]ast.Node{}, nulls: map[int]bool{}}
	for _, line := range nulls {
		b.nulls[line] = true
	}

	ast.Walk(b, root)

	return yaml.ResolveAlias(func(alias *ast.AliasNode) (ast.Node, bool) {
		node, ok := b.bound[alias]

		return node, ok
	})
}

func TestResolveAliasReusedAnchorName(t *testing.T) {
	t.Parallel()

	const src = "a: &x 1\nb: *x\nc: &x 2\nd: *x\n"

	type target struct{ D, C, B, A int }

	// Go-yaml reads the fields in the order of the struct, so B reads the
	// second anchor.
	var stock target

	require.NoError(t, up.Unmarshal([]byte(src), &stock))
	assert.Equal(t, 2, stock.B)

	body := parseBody(t, src)

	var v target

	require.NoError(t, yaml.NewDecoder(bytes.NewReader(nil), documentOrder(body)).DecodeFromNode(body, &v))
	assert.Equal(t, target{D: 2, C: 2, B: 1, A: 1}, v)
}

func TestResolveAliasDocumentOrder(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		src  string
		want string
	}{
		"anchor on a merge value": {
			src:  "m: {<<: &x {p: 1}}\nb: *x\nc: &x 2\n",
			want: "map[b:map[p:1] c:2 m:map[p:1]]",
		},
		"alias inside a merged mapping": {
			src:  "k1: &k 1\nb: &b {v: *k}\nk2: &k 2\nm: {<<: *b}\n",
			want: "map[b:map[v:1] k1:1 k2:2 m:map[v:1]]",
		},
		"inner anchor of the outer name": {
			src:  "r1: &x0 {z1: [&x0 d, *x0]}\n",
			want: "map[r1:map[z1:[d d]]]",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			body := parseBody(t, tc.src)

			var v any

			require.NoError(t, yaml.NewDecoder(bytes.NewReader(nil), documentOrder(body)).DecodeFromNode(body, &v))
			assert.Equal(t, tc.want, fmt.Sprint(v))
		})
	}
}

func TestResolveAliasBelowDocument(t *testing.T) {
	t.Parallel()

	body := parseBody(t, "base: &b {port: 1, tags: [x]}\nsvc: {<<: *b, name: n}\nlist: [*b]\n")
	mapping, ok := body.(*ast.MappingNode)
	require.True(t, ok)

	type svc struct {
		Name string
		Tags []string
		Port int
	}

	var s svc

	dec := yaml.NewDecoder(bytes.NewReader(nil), documentOrder(body))
	require.NoError(t, dec.DecodeFromNode(mapping.Values[1].Value, &s))
	assert.Equal(t, svc{Port: 1, Name: "n", Tags: []string{"x"}}, s)

	var l []svc

	dec = yaml.NewDecoder(bytes.NewReader(nil), documentOrder(body))
	require.NoError(t, dec.DecodeFromNode(mapping.Values[2].Value, &l))
	assert.Equal(t, []svc{{Port: 1, Tags: []string{"x"}}}, l)

	// Without the resolver, the decoder has not read the anchor.
	err := yaml.NewDecoder(bytes.NewReader(nil)).DecodeFromNode(mapping.Values[2].Value, &l)
	require.ErrorContains(t, err, `could not find alias "b"`)
}

func TestResolveAliasUnmarshalerBytes(t *testing.T) {
	t.Parallel()

	const src = "a: &x 1\nb: [*x]\nc: &x 2\n"

	// Go-yaml writes out the last anchor of the name.
	var stock struct{ B bytesValue }

	require.NoError(t, up.Unmarshal([]byte(src), &stock))
	assert.Contains(t, stock.B.s, "2")

	body := parseBody(t, src)

	var v struct{ B bytesValue }

	require.NoError(t, yaml.NewDecoder(bytes.NewReader(nil), documentOrder(body)).DecodeFromNode(body, &v))
	assert.Contains(t, v.B.s, "1")
	assert.NotContains(t, v.B.s, "2")
}

func TestResolveAliasNull(t *testing.T) {
	t.Parallel()

	// A merge key copies the entry of the alias inside the anchor, and the
	// decoder reads the copy after it has read the anchor. The resolver
	// binds that alias to a null.
	const src = "a: &x {k: *x}\nb: {<<: *x}\nc: [*x]\n"

	body := parseBody(t, src)

	var v struct {
		B struct{ K any }
		C bytesValue
	}

	require.NoError(t, yaml.NewDecoder(bytes.NewReader(nil), documentOrder(body, 1)).DecodeFromNode(body, &v))
	assert.Nil(t, v.B.K)
	assert.Equal(t, "[ {k: null}]", v.C.s)
}

func TestResolveAliasUnbound(t *testing.T) {
	t.Parallel()

	body := parseBody(t, "a: &x {p: 1}\nm: {<<: *y}\n")

	var v any

	err := yaml.NewDecoder(bytes.NewReader(nil), documentOrder(body)).DecodeFromNode(body, &v)

	var missing *yaml.MissingAnchorError

	require.ErrorAs(t, err, &missing)
	assert.Equal(t, "y", missing.Alias.Value.GetToken().Value)
}

func TestResolveAliasUnreadAnchor(t *testing.T) {
	t.Parallel()

	// The resolver binds the alias to an anchor of another parse, which
	// the decoder has not read. The alias inside that anchor reads as null.
	ref := parseBody(t, "r: &r {a: *r}\n")
	refMapping, ok := ref.(*ast.MappingNode)
	require.True(t, ok)

	anchor, ok := refMapping.Values[0].Value.(*ast.AnchorNode)
	require.True(t, ok)

	resolve := yaml.ResolveAlias(func(*ast.AliasNode) (ast.Node, bool) { return anchor.Value, true })
	body := parseBody(t, "m: *r\n")
	want := map[string]any{"m": map[string]any{"a": nil}}

	var typed map[string]any

	require.NoError(t, yaml.NewDecoder(bytes.NewReader(nil), resolve).DecodeFromNode(body, &typed))
	assert.Equal(t, want, typed)

	var untyped any

	require.NoError(t, yaml.NewDecoder(bytes.NewReader(nil), resolve).DecodeFromNode(body, &untyped))
	assert.Equal(t, want, untyped)
}

func TestResolveAliasUnmarshalerBytesOtherParse(t *testing.T) {
	t.Parallel()

	// The resolver binds the alias to an anchor of another parse. The text
	// that an unmarshaler gets holds the value of that anchor, without the
	// space and the line breaks that end the other parse.
	tcs := map[string]struct {
		ref  string
		want string
	}{
		"flow mapping": {
			ref:  "r: &r {k: 1, j: 2}\n",
			want: "{a: {k: 1, j: 2}}",
		},
		"plain scalar": {
			ref:  "r: &r x  \n\n",
			want: "{a: x}",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ref := parseBody(t, tc.ref)
			refMapping, ok := ref.(*ast.MappingNode)
			require.True(t, ok)

			anchor, ok := refMapping.Values[0].Value.(*ast.AnchorNode)
			require.True(t, ok)

			resolve := yaml.ResolveAlias(func(*ast.AliasNode) (ast.Node, bool) { return anchor.Value, true })
			body := parseBody(t, "m: {a: *r}\n")

			var v struct{ M bytesValue }

			require.NoError(t, yaml.NewDecoder(bytes.NewReader(nil), resolve).DecodeFromNode(body, &v))
			assert.Equal(t, tc.want, strings.TrimSpace(v.M.s))
		})
	}
}

func TestDecodeFromNodePointerToNodeType(t *testing.T) {
	t.Parallel()

	// Go-yaml sets a pointer to a node type to a copy of the node. The
	// field A reads x first, so the decoder keeps a value of x before the
	// alias of b reads it. The fields T, U and V read a mapping under a tag.
	body := parseBody(t, "m: {k: 1}\na: &x [1]\nb: *x\nc: &y {k: 2}\nl: [&z {k: 3}, *z]\n"+
		"t: !!map {k: 4}\nu: &w !!map {k: 5}\nv: *w\n")
	mapping, ok := body.(*ast.MappingNode)
	require.True(t, ok)

	x, ok := mapping.Values[1].Value.(*ast.AnchorNode)
	require.True(t, ok)

	y, ok := mapping.Values[3].Value.(*ast.AnchorNode)
	require.True(t, ok)

	l, ok := mapping.Values[4].Value.(*ast.SequenceNode)
	require.True(t, ok)

	z, ok := l.Values[0].(*ast.AnchorNode)
	require.True(t, ok)

	tagged, ok := mapping.Values[5].Value.(*ast.TagNode)
	require.True(t, ok)

	wAnchor, ok := mapping.Values[6].Value.(*ast.AnchorNode)
	require.True(t, ok)

	wTagged, ok := wAnchor.Value.(*ast.TagNode)
	require.True(t, ok)

	var v struct {
		M *ast.MappingNode
		A *ast.SequenceNode
		B *ast.SequenceNode
		C *ast.MappingNode
		T *ast.MappingNode
		U *ast.MappingNode
		V *ast.MappingNode
		L []*ast.MappingNode
	}

	require.NoError(t, yaml.NewDecoder(bytes.NewReader(nil)).DecodeFromNode(body, &v))
	assert.Same(t, mapping.Values[0].Value, v.M)
	assert.Same(t, x.Value, v.A)
	assert.Same(t, x.Value, v.B)
	assert.Same(t, y.Value, v.C)
	require.Len(t, v.L, 2)
	assert.Same(t, z.Value, v.L[0])
	assert.Same(t, z.Value, v.L[1])
	assert.Same(t, tagged.Value, v.T)
	assert.Same(t, wTagged.Value, v.U)
	assert.Same(t, wTagged.Value, v.V)

	var w map[string]*ast.MappingNode

	require.NoError(t, yaml.NewDecoder(bytes.NewReader(nil)).DecodeFromNode(parseBody(t, "a: &x {k: 1}\nb: *x\n"), &w))
	assert.Same(t, w["a"], w["b"])

	var m *ast.MappingNode

	require.NoError(t, yaml.NewDecoder(bytes.NewReader(nil)).DecodeFromNode(mapping.Values[0].Value, &m))
	assert.Same(t, mapping.Values[0].Value, m)
}

func TestResolveAliasMergeLoop(t *testing.T) {
	t.Parallel()

	// The resolver binds each alias of b to the anchor b of another parse,
	// which merges itself. The document merges b alone or in a sequence.
	sources := map[string]string{
		"alias":    "b: &b {<<: *b, y: 2}\n",
		"sequence": "b: &b {<<: [*b], y: 2}\n",
	}

	docs := map[string]string{
		"alias":    "m: {<<: *b}\n",
		"sequence": "m: {<<: [*b]}\n",
	}

	targets := map[string]func() any{
		"any":       func() any { return new(any) },
		"map":       func() any { return new(map[string]map[string]int) },
		"struct":    func() any { return new(struct{ M struct{ Y int } }) },
		"map slice": func() any { return new(yaml.MapSlice) },
	}

	for srcName, src := range sources {
		ref := parseBody(t, src)
		refMapping, ok := ref.(*ast.MappingNode)
		require.True(t, ok)

		anchor, ok := refMapping.Values[0].Value.(*ast.AnchorNode)
		require.True(t, ok)

		anchored, ok := anchor.Value.(*ast.MappingNode)
		require.True(t, ok)

		merged := anchored.Values[0].Value
		if seq, ok := merged.(*ast.SequenceNode); ok {
			merged = seq.Values[0]
		}

		inner, ok := merged.(*ast.AliasNode)
		require.True(t, ok)

		resolve := yaml.ResolveAlias(func(*ast.AliasNode) (ast.Node, bool) { return anchor.Value, true })

		for docName, doc := range docs {
			body := parseBody(t, doc)

			for targetName, target := range targets {
				for _, ordered := range []bool{false, true} {
					name := fmt.Sprintf("%s/%s/%s/ordered=%t", srcName, docName, targetName, ordered)
					t.Run(name, func(t *testing.T) {
						t.Parallel()

						opts := []yaml.DecodeOption{resolve}
						if ordered {
							opts = append(opts, yaml.UseOrderedMap())
						}

						err := yaml.NewDecoder(bytes.NewReader(nil), opts...).DecodeFromNode(body, target())

						var loop *yaml.MergeLoopError

						require.ErrorAs(t, err, &loop)
						assert.Same(t, inner, loop.Alias)
						assert.EqualError(t, loop, "merge key loops back to anchor b")
					})
				}
			}
		}
	}
}

func TestResolveAliasMergeSequence(t *testing.T) {
	t.Parallel()

	// The entries of b merge a, which the sequence also merges. The merge
	// of a inside b does not close a loop. Each target allows the key that
	// both b and a bring in twice.
	body := parseBody(t, "a: &a {x: 1}\nb: &b {<<: *a, y: 2}\nm: {<<: [*b, *a]}\n")
	mapping, ok := body.(*ast.MappingNode)
	require.True(t, ok)

	node := mapping.Values[2].Value
	slice := yaml.MapSlice{{Key: "x", Value: uint64(1)}, {Key: "y", Value: uint64(2)}, {Key: "x", Value: uint64(1)}}

	tcs := map[string]struct {
		target  func() any
		want    any
		ordered bool
	}{
		"any": {
			target: func() any { return new(any) },
			want:   map[string]any{"x": uint64(1), "y": uint64(2)},
		},
		"any ordered": {
			target:  func() any { return new(any) },
			want:    slice,
			ordered: true,
		},
		"map": {
			target: func() any { return new(map[string]int) },
			want:   map[string]int{"x": 1, "y": 2},
		},
		"map slice": {
			target: func() any { return new(yaml.MapSlice) },
			want:   slice,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := []yaml.DecodeOption{documentOrder(body), yaml.AllowDuplicateMapKey()}
			if tc.ordered {
				opts = append(opts, yaml.UseOrderedMap())
			}

			got := tc.target()

			require.NoError(t, yaml.NewDecoder(bytes.NewReader(nil), opts...).DecodeFromNode(node, got))
			assert.Equal(t, tc.want, reflect.ValueOf(got).Elem().Interface())
		})
	}
}

func TestResolveAliasInsideMergedAnchor(t *testing.T) {
	t.Parallel()

	// The resolver binds each alias to the anchor of its name in another
	// parse, where b holds an alias to itself and s is a sequence of an
	// alias to b. The document merges b alone, in a sequence, or through
	// s, and the alias inside b reads as null in each.
	ref := parseBody(t, "b: &b {x: *b, y: 1}\ns: &s [*b]\n")
	refMapping, ok := ref.(*ast.MappingNode)
	require.True(t, ok)

	anchors := map[string]ast.Node{}

	for _, value := range refMapping.Values {
		anchor, ok := value.Value.(*ast.AnchorNode)
		require.True(t, ok)

		anchors[anchor.Name.GetToken().Value] = anchor.Value
	}

	resolve := yaml.ResolveAlias(func(alias *ast.AliasNode) (ast.Node, bool) {
		node, ok := anchors[alias.Value.GetToken().Value]
		return node, ok
	})

	docs := map[string]string{
		"alias":             "m: {<<: *b}\n",
		"sequence":          "m: {<<: [*b]}\n",
		"alias to sequence": "m: {<<: *s}\n",
	}

	type Entry struct {
		X any
		Y int
	}

	type inlineEntry struct {
		Entry `yaml:",inline"`
	}

	type inlineMap struct {
		R map[string]any `yaml:",inline"`
	}

	entries := yaml.MapSlice{{Key: "x", Value: nil}, {Key: "y", Value: uint64(1)}}

	tcs := map[string]struct {
		target  func() any
		want    any
		ordered bool
		// Whether the target is a struct, where go-yaml refuses a sequence
		// under a merge key.
		structTarget bool
	}{
		"any": {
			target: func() any { return new(any) },
			want:   map[string]any{"m": map[string]any{"x": nil, "y": uint64(1)}},
		},
		"any ordered": {
			target:  func() any { return new(any) },
			want:    yaml.MapSlice{{Key: "m", Value: entries}},
			ordered: true,
		},
		"map": {
			target: func() any { return new(map[string]map[string]any) },
			want:   map[string]map[string]any{"m": {"x": nil, "y": uint64(1)}},
		},
		"map slice": {
			target: func() any { return new(map[string]yaml.MapSlice) },
			want:   map[string]yaml.MapSlice{"m": entries},
		},
		"struct": {
			target:       func() any { return new(map[string]Entry) },
			want:         map[string]Entry{"m": {Y: 1}},
			structTarget: true,
		},
		"inline struct": {
			target:       func() any { return new(map[string]inlineEntry) },
			want:         map[string]inlineEntry{"m": {Entry{Y: 1}}},
			structTarget: true,
		},
		"inline map": {
			target:       func() any { return new(map[string]inlineMap) },
			want:         map[string]inlineMap{"m": {R: map[string]any{"x": nil, "y": uint64(1)}}},
			structTarget: true,
		},
	}

	for docName, doc := range docs {
		body := parseBody(t, doc)

		for name, tc := range tcs {
			t.Run(docName+"/"+name, func(t *testing.T) {
				t.Parallel()

				opts := []yaml.DecodeOption{resolve}
				if tc.ordered {
					opts = append(opts, yaml.UseOrderedMap())
				}

				got := tc.target()

				err := yaml.NewDecoder(bytes.NewReader(nil), opts...).DecodeFromNode(body, got)
				if docName != "alias" && tc.structTarget {
					require.ErrorContains(t, err, "sequence was used where mapping is expected")

					return
				}

				require.NoError(t, err)
				assert.Equal(t, tc.want, reflect.ValueOf(got).Elem().Interface())
			})
		}
	}
}

func TestResolveAliasLoopAcrossAnchors(t *testing.T) {
	t.Parallel()

	// The anchors b and c of another parse alias each other. An alias reads
	// its anchor with the alias inside that closes the loop as null. Each
	// alias of the document reads the same value whichever alias the decode
	// reads first.
	ref := parseBody(t, "b: &b {k: *c, y: 2}\nc: &c {k: *b, y: 3}\n")
	refMapping, ok := ref.(*ast.MappingNode)
	require.True(t, ok)

	anchors := map[string]ast.Node{}

	for _, value := range refMapping.Values {
		anchor, ok := value.Value.(*ast.AnchorNode)
		require.True(t, ok)

		anchors[anchor.Name.GetToken().Value] = anchor.Value
	}

	resolve := yaml.ResolveAlias(func(alias *ast.AliasNode) (ast.Node, bool) {
		node, ok := anchors[alias.Value.GetToken().Value]
		return node, ok
	})

	type entry struct {
		K *entry
		Y int
	}

	wantAny := map[string]any{
		"b": map[string]any{"k": map[string]any{"k": nil, "y": uint64(3)}, "y": uint64(2)},
		"c": map[string]any{"k": map[string]any{"k": nil, "y": uint64(2)}, "y": uint64(3)},
	}
	wantTyped := struct{ B, C entry }{
		B: entry{K: &entry{Y: 3}, Y: 2},
		C: entry{K: &entry{Y: 2}, Y: 3},
	}

	sources := map[string]string{
		"b first": "b: *b\nc: *c\n",
		"c first": "c: *c\nb: *b\n",
	}

	for name, src := range sources {
		body := parseBody(t, src)

		t.Run(name+"/any", func(t *testing.T) {
			t.Parallel()

			var got any

			require.NoError(t, yaml.NewDecoder(bytes.NewReader(nil), resolve).DecodeFromNode(body, &got))
			assert.Equal(t, wantAny, got)
		})

		t.Run(name+"/typed", func(t *testing.T) {
			t.Parallel()

			var got struct{ B, C entry }

			require.NoError(t, yaml.NewDecoder(bytes.NewReader(nil), resolve).DecodeFromNode(body, &got))
			assert.Equal(t, wantTyped, got)
		})
	}
}

func TestResolveAliasBelowDocumentReadsAnchorOnce(t *testing.T) {
	t.Parallel()

	// Each anchor holds two aliases of the anchor before it, so the value
	// of the last anchor doubles at each step. The decode of the node at t
	// reads every anchor through an alias.
	const depth = 24

	var src strings.Builder

	src.WriteString("a0: &a0 x\n")

	for i := 1; i <= depth; i++ {
		fmt.Fprintf(&src, "a%d: &a%d [*a%d, *a%d]\n", i, i, i-1, i-1)
	}

	fmt.Fprintf(&src, "t: {v: *a%d}\n", depth)

	body := parseBody(t, src.String())
	mapping, ok := body.(*ast.MappingNode)
	require.True(t, ok)

	node := mapping.Values[depth+1].Value

	targets := map[string]func() any{
		"any":    func() any { return new(any) },
		"struct": func() any { return new(struct{ V []any }) },
	}

	for name, target := range targets {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// The decoder keeps the value of each anchor that an alias
			// reads, so it binds each alias of the document once. A decode
			// that read the anchor at every alias would bind 2^depth
			// aliases.
			b := &binder{last: map[string]ast.Node{}, bound: map[*ast.AliasNode]ast.Node{}, nulls: map[int]bool{}}
			ast.Walk(b, body)

			binds := 0
			resolve := yaml.ResolveAlias(func(alias *ast.AliasNode) (ast.Node, bool) {
				binds++
				n, ok := b.bound[alias]

				return n, ok
			})

			v := target()

			require.NoError(t, yaml.NewDecoder(bytes.NewReader(nil), resolve).DecodeFromNode(node, v))
			assert.LessOrEqual(t, binds, 4*(2*depth+1))

			got := reflect.ValueOf(v).Elem()

			var leaf reflect.Value

			if got.Kind() == reflect.Struct {
				leaf = got.Field(0)
			} else {
				leaf = got.Elem().MapIndex(reflect.ValueOf("v"))
			}

			for range depth {
				if leaf.Kind() == reflect.Interface {
					leaf = leaf.Elem()
				}

				require.Equal(t, 2, leaf.Len())

				leaf = leaf.Index(1)
			}

			assert.Equal(t, "x", fmt.Sprint(leaf.Interface()))
		})
	}
}

func TestResolveAliasInlineAliasBelowDocument(t *testing.T) {
	t.Parallel()

	// The resolver binds the merge alias to an anchor outside the node,
	// which the decoder has not read. The inline field reads that anchor
	// as the decode of the whole document reads it.
	body := parseBody(t, "defs: &d {a: 1}\nv: {<<: *d, b: 2}\n")
	mapping, ok := body.(*ast.MappingNode)
	require.True(t, ok)

	type aliased struct {
		Base map[string]any `yaml:",inline,alias"`
		B    int
	}

	var scoped aliased

	require.NoError(
		t,
		yaml.NewDecoder(bytes.NewReader(nil), documentOrder(body)).DecodeFromNode(mapping.Values[1].Value, &scoped),
	)

	var whole struct{ V aliased }

	require.NoError(t, yaml.NewDecoder(bytes.NewReader(nil), documentOrder(body)).DecodeFromNode(body, &whole))
	assert.Equal(t, aliased{Base: map[string]any{"a": uint64(1)}, B: 2}, scoped)
	assert.Equal(t, whole.V, scoped)
}

// errorAt returns the line and column where err lies: at the node of an
// UnmarshalerError, or at the token of an error of the decoder.
func errorAt(t *testing.T, err error) string {
	t.Helper()

	own, ok := errors.AsType[*yaml.UnmarshalerError](err)
	if ok {
		pos := own.Node.GetToken().Position

		return fmt.Sprintf("%d:%d", pos.Line, pos.Column)
	}

	located, ok := errors.AsType[up.Error](err)
	require.True(t, ok, "no place in %v", err)

	pos := located.GetToken().Position

	return fmt.Sprintf("%d:%d", pos.Line, pos.Column)
}

// errorMessage returns the message of err without the excerpt that an
// error of the decoder prints.
func errorMessage(err error) string {
	located, ok := errors.AsType[up.Error](err)
	if ok {
		return located.GetMessage()
	}

	return err.Error()
}

// collectServer and collectApp declare their fields in another order than
// the document of TestCollectErrors writes them.
type (
	collectServer struct {
		Port    int           `yaml:"port"`
		Timeout time.Duration `yaml:"timeout"`
	}

	collectApp struct {
		Servers  []collectServer `yaml:"servers"`
		Limits   map[string]int  `yaml:"limits"`
		Pair     [2]int          `yaml:"pair"`
		Timeout  int             `yaml:"timeout"`
		Replicas int             `yaml:"replicas"`
	}
)

func TestCollectErrors(t *testing.T) {
	t.Parallel()

	const src = `replcas: 3
timeout: soon
servers:
  - port: x
    timeout: later
    prot: 1
  - port: 70000000000000000000
limits: {cpu: lots, mem: 2}
pair: [a, 1]
zzz: 1
`

	var v collectApp

	err := yaml.UnmarshalWithOptions([]byte(src), &v, yaml.DisallowUnknownField(), yaml.CollectErrors())

	var all yaml.Errors

	require.ErrorAs(t, err, &all)

	got := make([]string, 0, len(all))
	for _, e := range all {
		got = append(got, errorAt(t, e)+" "+errorMessage(e))
	}

	assert.Equal(t, []string{
		`1:1 unknown field "replcas"`,
		`2:10 cannot unmarshal string into Go struct field collectApp.Timeout of type int`,
		`4:11 cannot unmarshal string into Go struct field collectApp.Servers of type int`,
		`5:14 time: invalid duration "later"`,
		`6:5 unknown field "prot"`,
		`7:11 cannot unmarshal 70000000000000000000 into Go value of type int ( overflow )`,
		`8:15 cannot unmarshal string into Go struct field collectApp.Limits of type int`,
		`9:8 cannot unmarshal string into Go struct field collectApp.Pair of type int`,
		`10:1 unknown field "zzz"`,
	}, got)

	// The message holds the message of each error, one per line.
	msgs := make([]string, 0, len(all))
	for _, e := range all {
		msgs = append(msgs, e.Error())
	}

	assert.Equal(t, strings.Join(msgs, "\n"), all.Error())

	// Without the option, the decode returns the first error it finds.
	err = yaml.UnmarshalWithOptions([]byte(src), &v, yaml.DisallowUnknownField())
	require.Error(t, err)
	require.NotErrorAs(t, err, &all)
}

func TestCollectErrorsOne(t *testing.T) {
	t.Parallel()

	var v struct{ A, B int }

	err := yaml.UnmarshalWithOptions([]byte("a: x\nb: 2\n"), &v, yaml.CollectErrors())

	//nolint:errorlint // The test checks the type of the error itself.
	located, ok := err.(*yaml.PathError)
	require.True(t, ok, "%T", err)
	assert.Equal(t, "$.a", pathOf(located))

	//nolint:errorlint // The test checks the type of the error itself.
	_, typed := located.Err.(*up.TypeError)
	assert.True(t, typed, "%T", located.Err)
	assert.Equal(t, 2, v.B)
	assert.Equal(t, "1:4", errorAt(t, err))
}

func TestCollectErrorsMapStops(t *testing.T) {
	t.Parallel()

	// A map stops at the error of a key, after the errors of the entries
	// before it.
	var v map[int]int

	err := yaml.UnmarshalWithOptions([]byte("1: x\nk: 2\n3: y\n"), &v, yaml.CollectErrors())

	var all yaml.Errors

	require.ErrorAs(t, err, &all)
	require.Len(t, all, 2)
	assert.Equal(t, "1:4", errorAt(t, all[0]))
	assert.Equal(t, "2:1", errorAt(t, all[1]))
}

func TestDisallowUnknownFieldOrder(t *testing.T) {
	t.Parallel()

	// The decode names the first unknown field of the document at every
	// decode. Go-yaml names any of them, as the order of a map gives it.
	for range 300 {
		var v struct{ A int }

		err := yaml.UnmarshalWithOptions([]byte("a: 1\nb: 2\nc: 3\nd: 4\n"), &v, yaml.DisallowUnknownField())

		var unknown *up.UnknownFieldError

		require.ErrorAs(t, err, &unknown)
		require.Equal(t, `unknown field "b"`, unknown.Message)
	}
}

func TestReportUnknownField(t *testing.T) {
	t.Parallel()

	var v struct {
		H    callbackHolder[struct{ A int }] `yaml:"h"`
		Port int                             `yaml:"port"`
	}

	var fields []error

	report := yaml.ReportUnknownField(func(err error) { fields = append(fields, err) })

	// The callback of the holder reads a mapping with an unknown field and
	// does not fail. The decode returns the error of the port alone.
	err := yaml.UnmarshalWithOptions([]byte("h: {x: {a: 1, b: 2}}\nzzz: 1\nport: x\nyyy: 2\n"), &v,
		report, yaml.CollectErrors())
	require.Error(t, err)
	assert.Equal(t, "$.port", pathOf(err))
	require.NoError(t, v.H.err)

	got := make([]string, 0, len(fields))
	for _, e := range fields {
		require.ErrorAs(t, e, new(*up.UnknownFieldError))

		got = append(got, errorAt(t, e)+" "+pathOf(e))
	}

	// Each struct hands its fields in the order of the document, after the
	// fields of the values inside it.
	assert.Equal(t, []string{"1:15 $.h.x.b~", "2:1 $.zzz~", "4:1 $.yyy~"}, got)

	// Without CollectErrors, the errors carry no path.
	fields = nil

	require.NoError(t, yaml.UnmarshalWithOptions([]byte("port: 1\nzzz: 1\n"), &v, report))
	require.Len(t, fields, 1)
	assert.Equal(t, "(no path)", pathOf(fields[0]))
}

func TestReportUnknownFieldAliases(t *testing.T) {
	t.Parallel()

	type named struct{ Name string }

	type holder struct{ In named }

	tcs := map[string]struct {
		src  string
		v    any
		want []string
	}{
		"plain alias and merge key": {
			src: "a: &x {name: n, extra: 1}\nb: *x\nc:\n  <<: *x\n",
			v:   &struct{ A, B, C named }{},
			want: []string{
				"1:17 $.a.extra~",
				"1:17 $.b.extra~",
				"1:17 $.c.extra~",
			},
		},
		"anchor that reads another anchor": {
			src: "b: &b {name: n, extra: 1}\no: &o {in: *b, more: 2}\nu: *o\n",
			v: &struct {
				B    named
				O, U holder
			}{},
			want: []string{
				"1:17 $.b.extra~",
				"1:17 $.o.in.extra~",
				"2:16 $.o.more~",
				"1:17 $.u.in.extra~",
				"2:16 $.u.more~",
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var reported []string

			report := yaml.ReportUnknownField(func(err error) {
				reported = append(reported, errorAt(t, err)+" "+pathOf(err))
			})

			// Each alias that takes the value of a read anchor reports the
			// unknown fields of that anchor at its own path. A decode that
			// fails for them returns them for each alias too.
			require.NoError(t, yaml.UnmarshalWithOptions([]byte(tc.src), tc.v, report, yaml.CollectErrors()))
			assert.Equal(t, tc.want, reported)

			err := yaml.UnmarshalWithOptions([]byte(tc.src), tc.v, yaml.DisallowUnknownField(), yaml.CollectErrors())

			var all yaml.Errors

			require.ErrorAs(t, err, &all)

			rejected := make([]string, 0, len(all))
			for _, e := range all {
				rejected = append(rejected, errorAt(t, e)+" "+pathOf(e))
			}

			assert.ElementsMatch(t, tc.want, rejected)
		})
	}
}

// fallbackHolder decodes a mapping into a struct with an int field a
// through the callback. When that call fails, it decodes the mapping into
// a map instead, and keeps its error as passing tells it to.
type fallbackHolder struct {
	M       map[string]string
	passing string
}

func (h *fallbackHolder) UnmarshalYAML(unmarshal func(any) error) error {
	var strict struct {
		A int `yaml:"a"`
	}

	err := unmarshal(&strict)
	if err == nil {
		return nil
	}

	switch h.passing {
	case "unchanged":
		return err
	case "wrapped":
		return fmt.Errorf("strict: %w", err)
	}

	return unmarshal(&h.M)
}

func TestReportUnknownFieldCallbackFails(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		passing string
		err     bool
		want    []string
	}{
		"unmarshaler that reads the value another way": {
			want: []string{},
		},
		"unmarshaler that returns the error of the call": {
			passing: "unchanged",
			err:     true,
			want:    []string{"1:11 $.v.b~"},
		},
		"unmarshaler that wraps the error of the call": {
			passing: "wrapped",
			err:     true,
			want:    []string{},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reported := []string{}

			report := yaml.ReportUnknownField(func(err error) {
				reported = append(reported, errorAt(t, err)+" "+pathOf(err))
			})

			v := struct{ V fallbackHolder }{V: fallbackHolder{passing: tc.passing}}

			// The call of the callback fails for its value of a. The decode
			// reports the unknown field b of that call only when the
			// unmarshaler returns the error of the call as it is.
			err := yaml.UnmarshalWithOptions([]byte("v: {a: q, b: r}\n"), &v, report, yaml.CollectErrors())
			if tc.err {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, map[string]string{"a": "q", "b": "r"}, v.V.M)
			}

			assert.Equal(t, tc.want, reported)
		})
	}
}

func TestCollectErrorsInlineMapOrder(t *testing.T) {
	t.Parallel()

	// The mapping of an inline map holds its entries in the order of the
	// document, so the errors at one position come in that order at every
	// decode.
	const src = "x: &x abc\nb: *x\nc: *x\na: *x\n"

	for range 50 {
		var v struct {
			Rest map[string]int `yaml:",inline"`
			X    string         `yaml:"x"`
		}

		err := yaml.UnmarshalWithOptions([]byte(src), &v, yaml.CollectErrors())

		var all yaml.Errors

		require.ErrorAs(t, err, &all)

		got := make([]string, 0, len(all))
		for _, e := range all {
			got = append(got, errorAt(t, e)+" "+pathOf(e))
		}

		require.Equal(t, []string{"1:7 $.x", "1:7 $.b", "1:7 $.c", "1:7 $.a"}, got)
	}
}

// pathOf returns the path of a PathError in err, with "~" after the name
// of a key, or "(no path)".
func pathOf(err error) string {
	located, ok := errors.AsType[*yaml.PathError](err)
	if !ok {
		return "(no path)"
	}

	var b strings.Builder

	b.WriteString("$")

	for _, step := range located.Path {
		switch {
		case step.IsIndex:
			fmt.Fprintf(&b, "[%d]", step.Index)
		case step.IsKey:
			fmt.Fprintf(&b, ".%s~", step.Key)
		default:
			fmt.Fprintf(&b, ".%s", step.Key)
		}
	}

	return b.String()
}

func TestCollectErrorsPathOfEachUse(t *testing.T) {
	t.Parallel()

	type srv struct {
		Env  map[string]int `yaml:"env"`
		Tags []string       `yaml:"tags"`
		Port int            `yaml:"port"`
	}

	var v struct {
		ByKey map[string]srv `yaml:"byKey"`
		Base  srv            `yaml:"base"`
		A     srv            `yaml:"a"`
		C     srv            `yaml:"c"`
		List  []srv          `yaml:"list"`
	}

	const src = `base: &b {port: x, env: {LOG: info}}
a: *b
c: {<<: *b, tags: {}}
list: [*b, {port: 1}, {port: y}]
byKey: {k: *b}
`

	err := yaml.UnmarshalWithOptions([]byte(src), &v, yaml.CollectErrors())

	var all yaml.Errors

	require.ErrorAs(t, err, &all)

	got := make([]string, 0, len(all))
	for _, e := range all {
		got = append(got, errorAt(t, e)+" "+pathOf(e))
	}

	// Each use of the anchor gives an error at the position of the anchor,
	// in the order of the fields at one position.
	assert.Equal(t, []string{
		"1:17 $.byKey.k.port",
		"1:17 $.base.port",
		"1:17 $.a.port",
		"1:17 $.c.port",
		"1:17 $.list[0].port",
		"1:31 $.byKey.k.env.LOG",
		"1:31 $.base.env.LOG",
		"1:31 $.a.env.LOG",
		"1:31 $.c.env.LOG",
		"1:31 $.list[0].env.LOG",
		"3:19 $.c.tags",
		"4:30 $.list[2].port",
	}, got)
}

func TestCollectErrorsPathOfKeys(t *testing.T) {
	t.Parallel()

	var v struct {
		Items []struct {
			Name string  `yaml:"name"`
			When failing `yaml:"when"`
		} `yaml:"items"`
		Keys map[int]int `yaml:"keys"`
	}

	err := yaml.UnmarshalWithOptions([]byte("items:\n  - {name: a, nme: b, when: 1}\nkeys: {x: 1}\n"), &v,
		yaml.DisallowUnknownField(), yaml.CollectErrors())

	var all yaml.Errors

	require.ErrorAs(t, err, &all)

	got := make([]string, 0, len(all))
	for _, e := range all {
		got = append(got, pathOf(e))
	}

	assert.Equal(t, []string{"$.items[0].nme~", "$.items[0].when", "$.keys.x~"}, got)

	// Without CollectErrors, the error carries no path.
	err = yaml.Unmarshal([]byte("items:\n  - {name: [1]}\n"), &v)

	//nolint:errorlint // The test checks the type of the error itself.
	_, bare := err.(*up.TypeError)
	assert.True(t, bare, "%T", err)
}

func TestCollectErrorsPathInsideCallback(t *testing.T) {
	t.Parallel()

	// A value that decodes itself through the callback reports the errors
	// below it, each with its own path from the node of the decode.
	var v struct {
		L []callbackHolder[struct{ A, B int }] `yaml:"l"`
	}

	err := yaml.UnmarshalWithOptions([]byte("l:\n  - x: {a: p, b: q}\n"), &v, yaml.CollectErrors())

	var all yaml.Errors

	require.ErrorAs(t, err, &all)

	got := make([]string, 0, len(all))
	for _, e := range all {
		got = append(got, errorAt(t, e)+" "+pathOf(e))
	}

	assert.Equal(t, []string{"2:12 $.l[0].x.a", "2:18 $.l[0].x.b"}, got)
}

func TestCollectErrorsPathOfArray(t *testing.T) {
	t.Parallel()

	// Each element of an array has the index of its place in the sequence,
	// after an element that does not decode too.
	var v struct {
		X [3]time.Duration `yaml:"x"`
	}

	err := yaml.UnmarshalWithOptions([]byte("x: [a, soon, later]\n"), &v, yaml.CollectErrors())

	var all yaml.Errors

	require.ErrorAs(t, err, &all)

	got := make([]string, 0, len(all))
	for _, e := range all {
		got = append(got, errorAt(t, e)+" "+pathOf(e))
	}

	assert.Equal(t, []string{"1:5 $.x[0]", "1:8 $.x[1]", "1:14 $.x[2]"}, got)
}

// panicText panics when it decodes.
type panicText struct{}

func (*panicText) UnmarshalText([]byte) error { panic("panicText") }

// guard decodes a panicText through the callback, and it returns an error
// in place of the panic.
type guard struct{}

func (*guard) UnmarshalYAML(unmarshal func(any) error) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("guarded")
		}
	}()

	var v struct {
		Deep struct {
			P panicText `yaml:"p"`
		} `yaml:"deep"`
	}

	return unmarshal(&v)
}

func TestCollectErrorsPathAfterRecoveredPanic(t *testing.T) {
	t.Parallel()

	// A panic below the callback leaves no step of its values on the path
	// when the unmarshaler recovers it.
	var v struct {
		G guard         `yaml:"g"`
		N int           `yaml:"n"`
		T time.Duration `yaml:"t"`
	}

	err := yaml.UnmarshalWithOptions([]byte("g: {deep: {p: x}}\nn: y\nt: soon\n"), &v, yaml.CollectErrors())

	var all yaml.Errors

	require.ErrorAs(t, err, &all)

	got := make([]string, 0, len(all))
	for _, e := range all {
		got = append(got, errorAt(t, e)+" "+pathOf(e)+" "+errorMessage(e))
	}

	assert.Equal(t, []string{
		"1:4 $.g guarded",
		"2:4 $.n cannot unmarshal string into Go struct field .N of type int",
		`3:4 $.t time: invalid duration "soon"`,
	}, got)
}

// startPrinter is a printer whose start under [startValues] sets its port.
type startPrinter struct {
	Name string `yaml:"name"`
	Port int    `yaml:"port"`
}

// startCafe holds each kind of place that the decoder creates a value for.
type startCafe struct {
	ByName   map[string]startPrinter `yaml:"byName"`
	Spare    *startPrinter           `yaml:"spare"`
	Printers []startPrinter          `yaml:"printers"`
	Cafes    []startCafe             `yaml:"cafes"`
	Kitchen  startPrinter            `yaml:"kitchen"`
	Pair     [2]startPrinter         `yaml:"pair"`
	Seats    int                     `yaml:"seats"`
}

// startKey is a map key that decodes itself from text.
type startKey struct{ Label, Unit string }

func (k *startKey) UnmarshalText(b []byte) error {
	k.Label = string(b)

	return nil
}

// startSelf decodes itself, and it records in Start the value of Got that
// it started from.
type startSelf struct{ Start, Got int }

func (s *startSelf) UnmarshalYAML(unmarshal func(any) error) error {
	s.Start = s.Got

	return unmarshal(&s.Got)
}

// startValues gives the start of each type of the start tests. A printer,
// a café, a key, and a value that decodes itself each have one, and the
// kitchen printer of the café has a port of its own. The function counts
// each call for a printer in calls when calls is not nil.
func startValues(calls *int) func(reflect.Type, ast.Node) reflect.Value {
	return func(t reflect.Type, _ ast.Node) reflect.Value {
		switch t {
		case reflect.TypeFor[startPrinter]():
			if calls != nil {
				*calls++
			}

			return reflect.ValueOf(startPrinter{Port: 9100})

		case reflect.TypeFor[startCafe]():
			return reflect.ValueOf(startCafe{Seats: 20, Kitchen: startPrinter{Port: 9101}})
		case reflect.TypeFor[startKey]():
			return reflect.ValueOf(startKey{Unit: "ml"})
		case reflect.TypeFor[startSelf]():
			return reflect.ValueOf(startSelf{Got: 7})
		}

		return reflect.Value{}
	}
}

func TestStartValue(t *testing.T) {
	t.Parallel()

	const src = `
kitchen: {name: k}
printers: [{name: a}, {name: b, port: 1}, ~]
byName: {x: {name: x}}
spare: {name: s}
pair: [{name: p}]
cafes: [{kitchen: {name: inner}}]
`

	got := startCafe{Seats: 20, Kitchen: startPrinter{Port: 9101}}
	require.NoError(t, yaml.UnmarshalWithOptions([]byte(src), &got, yaml.StartValue(startValues(nil))))

	// Each struct field starts from the value it holds, and each value
	// that the decoder creates starts from the start of its type. A null
	// element holds that start. The array keeps zero in the slot that the
	// sequence does not write.
	assert.Equal(t, startCafe{
		Seats:    20,
		Kitchen:  startPrinter{Name: "k", Port: 9101},
		Printers: []startPrinter{{Name: "a", Port: 9100}, {Name: "b", Port: 1}, {Port: 9100}},
		ByName:   map[string]startPrinter{"x": {Name: "x", Port: 9100}},
		Spare:    &startPrinter{Name: "s", Port: 9100},
		Pair:     [2]startPrinter{{Name: "p", Port: 9100}, {}},
		Cafes:    []startCafe{{Seats: 20, Kitchen: startPrinter{Name: "inner", Port: 9101}}},
	}, got)

	// Without the option, each value that the decoder creates starts from
	// zero.
	var plain startCafe

	require.NoError(t, yaml.Unmarshal([]byte(src), &plain))
	assert.Equal(t, startPrinter{Name: "a"}, plain.Printers[0])
	assert.Equal(t, &startPrinter{Name: "s"}, plain.Spare)
}

func TestStartValueKeysUnmarshalersPointers(t *testing.T) {
	t.Parallel()

	var got struct {
		Sizes map[startKey]int `yaml:"sizes"`
		Self  []startSelf      `yaml:"self"`
		Ptrs  []*startPrinter  `yaml:"ptrs"`
	}

	src := "sizes: {small: 1}\nself: [3]\nptrs: [~, {name: p}]\n"
	require.NoError(t, yaml.UnmarshalWithOptions([]byte(src), &got, yaml.StartValue(startValues(nil))))

	assert.Equal(t, map[startKey]int{{Label: "small", Unit: "ml"}: 1}, got.Sizes)
	assert.Equal(t, []startSelf{{Start: 7, Got: 3}}, got.Self)

	// A null sets a pointer to nil.
	assert.Equal(t, []*startPrinter{nil, {Name: "p", Port: 9100}}, got.Ptrs)
}

// startPointerHolder decodes itself into the nil pointer P through the
// callback of an InterfaceUnmarshaler.
type startPointerHolder struct{ P *startPrinter }

func (h *startPointerHolder) UnmarshalYAML(unmarshal func(any) error) error {
	return unmarshal(&h.P)
}

func TestStartValueNilPointer(t *testing.T) {
	t.Parallel()

	start := yaml.StartValue(startValues(nil))

	// The target of a nil pointer that the decode reaches directly, such
	// as the root or the value of a callback, starts from the start of
	// the type that the pointer reaches.
	var p *startPrinter

	require.NoError(t, yaml.UnmarshalWithOptions([]byte("name: s\n"), &p, start))
	assert.Equal(t, &startPrinter{Name: "s", Port: 9100}, p)

	var pp **startPrinter

	require.NoError(t, yaml.UnmarshalWithOptions([]byte("name: s\n"), &pp, start))
	require.NotNil(t, pp)
	assert.Equal(t, &startPrinter{Name: "s", Port: 9100}, *pp)

	var h startPointerHolder

	require.NoError(t, yaml.UnmarshalWithOptions([]byte("name: s\n"), &h, start))
	assert.Equal(t, &startPrinter{Name: "s", Port: 9100}, h.P)

	// A pointer that reaches a value starts from that value.
	held := &startPrinter{Port: 1}

	require.NoError(t, yaml.UnmarshalWithOptions([]byte("name: s\n"), &held, start))
	assert.Equal(t, &startPrinter{Name: "s", Port: 1}, held)
}

func TestStartValueNode(t *testing.T) {
	t.Parallel()

	var got struct {
		Sizes map[startKey]int `yaml:"sizes"`
		List  []startPrinter   `yaml:"list"`
		Ptr   *startPrinter    `yaml:"ptr"`
	}

	// Each start gets the node that the decoder reads into the value.
	starts := map[reflect.Type][]string{}
	start := func(t reflect.Type, node ast.Node) reflect.Value {
		pos := node.GetToken().Position
		starts[t] = append(starts[t], fmt.Sprintf("%d:%d", pos.Line, pos.Column))

		return reflect.Value{}
	}

	src := "sizes: {small: 1}\nlist: [{name: a}, {name: b}]\nptr: {name: p}\n"
	require.NoError(t, yaml.UnmarshalWithOptions([]byte(src), &got, yaml.StartValue(start)))

	assert.Equal(t, []string{"1:9"}, starts[reflect.TypeFor[startKey]()])
	assert.Equal(t, []string{"2:8", "2:19", "3:6"}, starts[reflect.TypeFor[startPrinter]()])
}

func TestStartValueAliases(t *testing.T) {
	t.Parallel()

	// The decoder reads the fields in this order, and an alias takes the
	// value of an anchor that the decoder read before it.
	type places struct {
		Head     startPrinter    `yaml:"head"`
		Printers []startPrinter  `yaml:"printers"`
		Ptrs     []*startPrinter `yaml:"ptrs"`
		Any      any             `yaml:"any"`
		Spare    *startPrinter   `yaml:"spare"`
		Kitchen  startPrinter    `yaml:"kitchen"`
	}

	tcs := map[string]struct {
		want  func(t *testing.T, got places)
		input string
		calls int
	}{
		// Each element starts from the start of its type, so the alias
		// takes the value of the anchor, and the decoder does not ask for a
		// start to compare.
		"element at an alias to an element": {
			input: "ptrs: [&b {name: b}, *b]\n",
			calls: 1,
			want: func(t *testing.T, got places) {
				t.Helper()

				require.Len(t, got.Ptrs, 2)
				assert.Equal(t, &startPrinter{Name: "b", Port: 9100}, got.Ptrs[0])
				assert.Same(t, got.Ptrs[0], got.Ptrs[1])
			},
		},
		// The kitchen starts from another value than an element, so the
		// decoder decodes the anchor again for it. The target of the nil
		// pointer starts from the start of its type, as the element does.
		"field at an alias to an element": {
			input: "printers: [&b {name: b}]\nkitchen: *b\nspare: *b\n",
			calls: 1,
			want: func(t *testing.T, got places) {
				t.Helper()

				assert.Equal(t, []startPrinter{{Name: "b", Port: 9100}}, got.Printers)
				assert.Equal(t, startPrinter{Name: "b", Port: 9101}, got.Kitchen)
				assert.Equal(t, &startPrinter{Name: "b", Port: 9100}, got.Spare)
			},
		},
		// The element starts from another value than the head, so the
		// decoder decodes the anchor again for it, from the start that it
		// asked for to compare.
		"element at an alias to a field": {
			input: "head: &k {name: k}\nprinters: [*k]\n",
			calls: 1,
			want: func(t *testing.T, got places) {
				t.Helper()

				assert.Equal(t, startPrinter{Name: "k", Port: 9101}, got.Head)
				assert.Equal(t, []startPrinter{{Name: "k", Port: 9100}}, got.Printers)
			},
		},
		// An interface value takes the value of the anchor as go-yaml
		// gives it.
		"interface value at an alias to a field": {
			input: "head: &k {name: k}\nany: *k\n",
			want: func(t *testing.T, got places) {
				t.Helper()

				assert.Equal(t, startPrinter{Name: "k", Port: 9101}, got.Any)
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			calls := 0
			got := places{Head: startPrinter{Port: 9101}, Kitchen: startPrinter{Port: 9101}}

			require.NoError(t, yaml.UnmarshalWithOptions([]byte(tc.input), &got, yaml.StartValue(startValues(&calls))))
			tc.want(t, got)
			assert.Equal(t, tc.calls, calls)
		})
	}
}

func TestStartValueInlineFieldNamedTwice(t *testing.T) {
	t.Parallel()

	type inner struct {
		Name string `yaml:"name"`
		Port int    `yaml:"port"`
	}

	type outer struct {
		In   inner  `yaml:",inline"`
		Name string `yaml:"name"`
	}

	none := func(reflect.Type, ast.Node) reflect.Value { return reflect.Value{} }

	tcs := map[string]struct {
		opts []yaml.DecodeOption
		want outer
	}{
		// Go-yaml sets the field of the inline struct that the outer
		// struct names too to zero.
		"without the option": {
			want: outer{In: inner{Port: 2}, Name: "n"},
		},
		"with the option": {
			opts: []yaml.DecodeOption{yaml.StartValue(none)},
			want: outer{In: inner{Name: "keep", Port: 2}, Name: "n"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := outer{In: inner{Name: "keep"}}

			require.NoError(t, yaml.UnmarshalWithOptions([]byte("name: n\nport: 2\n"), &got, tc.opts...))
			assert.Equal(t, tc.want, got)
		})
	}
}

// startBag is a map of the start tests that decodes itself. It copies
// the entries of the mapping into the map that it starts from.
type startBag map[string]int

func (b *startBag) UnmarshalYAML(unmarshal func(any) error) error {
	var entries map[string]int

	err := unmarshal(&entries)
	if err != nil {
		return err
	}

	if *b == nil {
		*b = startBag{}
	}

	maps.Copy(*b, entries)

	return nil
}

func TestStartValueInlineFieldWritesIntoStart(t *testing.T) {
	t.Parallel()

	type inner struct {
		Tags startBag `yaml:"tags"`
		Name string   `yaml:"name"`
	}

	type outer struct {
		In   inner    `yaml:",inline"`
		Tags startBag `yaml:"tags"`
	}

	// The unmarshaler of the tags of the inline struct writes the entries
	// of the document into the map of its start. The field keeps the start
	// all the same.
	tcs := map[string]struct {
		start func(reflect.Type, ast.Node) reflect.Value
		got   *outer
		want  outer
	}{
		"start of the type": {
			start: func(t reflect.Type, _ ast.Node) reflect.Value {
				if t != reflect.TypeFor[outer]() {
					return reflect.Value{}
				}

				return reflect.ValueOf(outer{In: inner{Tags: startBag{"d": 1}}})
			},
			want: outer{In: inner{Tags: startBag{"d": 1}, Name: "a"}, Tags: startBag{"k": 2}},
		},
		"value that the target holds": {
			start: func(reflect.Type, ast.Node) reflect.Value { return reflect.Value{} },
			got:   &outer{In: inner{Tags: startBag{"pre": 1}}},
			want:  outer{In: inner{Tags: startBag{"pre": 1}, Name: "a"}, Tags: startBag{"k": 2}},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := tc.got

			require.NoError(t, yaml.UnmarshalWithOptions(
				[]byte("name: a\ntags: {k: 2}\n"), &got, yaml.StartValue(tc.start),
			))
			require.NotNil(t, got)
			assert.Equal(t, tc.want, *got)
		})
	}
}

// startKinds holds fields of several kinds that the decoder compares when
// it compares the start of the place of an alias with the start of the
// place of its anchor.
type startKinds struct {
	M     map[string]int `yaml:"-"`
	Any   any            `yaml:"-"`
	S     []int          `yaml:"-"`
	Codes []int          `yaml:"codes"`
	C     complex128     `yaml:"-"`
	Arr   [2]int         `yaml:"-"`
	U     uint           `yaml:"-"`
	B     bool           `yaml:"-"`
}

// startKindsValue returns a [startKinds] whose fields hold values that
// differ from zero.
func startKindsValue() startKinds {
	return startKinds{
		M:   map[string]int{"k": 1},
		Any: 1,
		S:   []int{1},
		C:   complex(1, 2),
		Arr: [2]int{1, 2},
		U:   1,
		B:   true,
	}
}

func TestStartValueAliasStarts(t *testing.T) {
	t.Parallel()

	// The place of the alias starts from the value that edit makes of the
	// start of the place of the anchor. It shares the value of the anchor
	// only when the two starts are equal.
	tcs := map[string]struct {
		edit   func(k *startKinds)
		shared bool
	}{
		"equal starts": {
			edit:   func(*startKinds) {},
			shared: true,
		},
		"map value": {
			edit: func(k *startKinds) { k.M = map[string]int{"k": 2} },
		},
		"map key": {
			edit: func(k *startKinds) { k.M = map[string]int{"j": 1} },
		},
		"slice length": {
			edit: func(k *startKinds) { k.S = []int{1, 1} },
		},
		"array element": {
			edit: func(k *startKinds) { k.Arr[1] = 3 },
		},
		"interface value": {
			edit: func(k *startKinds) { k.Any = 2 },
		},
		"interface type": {
			edit: func(k *startKinds) { k.Any = "1" },
		},
		"nil interface": {
			edit: func(k *startKinds) { k.Any = nil },
		},
		"uint": {
			edit: func(k *startKinds) { k.U = 2 },
		},
		"complex": {
			edit: func(k *startKinds) { k.C = complex(1, 3) },
		},
		"bool": {
			edit: func(k *startKinds) { k.B = false },
		},
	}

	none := func(reflect.Type, ast.Node) reflect.Value { return reflect.Value{} }

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := struct {
				A startKinds `yaml:"a"`
				B startKinds `yaml:"b"`
			}{A: startKindsValue(), B: startKindsValue()}

			tc.edit(&got.B)

			require.NoError(t, yaml.UnmarshalWithOptions(
				[]byte("a: &a {codes: [1]}\nb: *a\n"), &got, yaml.StartValue(none),
			))
			require.Equal(t, []int{1}, got.A.Codes)
			require.Equal(t, []int{1}, got.B.Codes)
			assert.Equal(t, tc.shared, &got.A.Codes[0] == &got.B.Codes[0])
		})
	}
}

// startRing is a ring of the start tests. Its start points to itself.
type startRing struct {
	Next *startRing `yaml:"-"`
	Name string     `yaml:"name"`
}

// startRingValue returns a ring whose Next points to itself.
func startRingValue() startRing {
	r := &startRing{}
	r.Next = r

	return *r
}

func TestStartValueCyclicStart(t *testing.T) {
	t.Parallel()

	start := func(t reflect.Type, _ ast.Node) reflect.Value {
		if t != reflect.TypeFor[startRing]() {
			return reflect.Value{}
		}

		return reflect.ValueOf(startRingValue())
	}

	got := struct {
		A     startRing   `yaml:"a"`
		B     startRing   `yaml:"b"`
		Rings []startRing `yaml:"rings"`
	}{A: startRingValue(), B: startRingValue()}

	src := "a: &a {name: a}\nb: *a\nrings: [&r {name: r}, *r]\n"
	require.NoError(t, yaml.UnmarshalWithOptions([]byte(src), &got, yaml.StartValue(start)))

	// The decoder copies the start of each anchor, and the two rings at
	// a and b start from equal values, so b takes the value of a.
	assert.Equal(t, "a", got.B.Name)
	assert.Same(t, got.A.Next, got.B.Next)

	require.Len(t, got.Rings, 2)
	assert.Equal(t, "r", got.Rings[1].Name)
	assert.Same(t, got.Rings[0].Next, got.Rings[1].Next)
}
