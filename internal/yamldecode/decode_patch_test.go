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

						var missing *yaml.MissingAnchorError

						require.ErrorAs(t, err, &missing)
						assert.Same(t, inner, missing.Alias)
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
