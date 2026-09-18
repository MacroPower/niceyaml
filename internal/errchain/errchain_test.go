package errchain_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml/internal/errchain"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/position"
)

// node is a test error that holds a cause and nested errors, and writes
// its nested lines after its own message as a niceyaml Error does.
type node struct {
	msg     string
	cause   error
	nested  []error
	located bool
}

func (n *node) Error() string {
	msg := n.msg
	if n.cause != nil {
		msg = errchain.Prefix(msg, n.cause.Error())
	}

	for _, nested := range n.nested {
		if nested == nil {
			continue
		}

		if text := nested.Error(); text != "" {
			msg += "\n" + text
		}
	}

	return msg
}

// binding is a test error bound to a named source, with the positions its
// nested errors resolved to. Its message is the message of the error it
// binds with the nested lines positioned, as a niceyaml SourceError writes
// it, unless it is rebound.
type binding struct {
	name      string
	inner     error
	positions map[error]position.Position
}

func (b *binding) Error() string {
	if funcs.Rebound(b) {
		return b.inner.Error()
	}

	return b.name + ": " + funcs.Positioned(b, b.inner, b.inner.Error())
}

func (b *binding) Unwrap() error {
	return b.inner
}

var funcs = errchain.Funcs{
	Nothing: func(err error) bool {
		switch x := err.(type) { //nolint:errorlint // The node itself, not a chain search.
		case nil:
			return true
		case *node:
			return x == nil
		case *binding:
			return x == nil
		default:
			return false
		}
	},
	Node: func(err error) (errchain.Node, bool) {
		x, ok := err.(*node) //nolint:errorlint // The node itself, not a chain search.
		if !ok || x == nil {
			return errchain.Node{}, false
		}

		return errchain.Node{Cause: x.cause, Nested: x.nested, Located: x.located}, true
	},
	Binding: func(err error) (errchain.Binding, bool) {
		x, ok := err.(*binding) //nolint:errorlint // The node itself, not a chain search.
		if !ok || x == nil {
			return errchain.Binding{}, false
		}

		return errchain.Binding{Inner: x.inner, Name: x.name}, true
	},
	Position: func(bound, nested error) (position.Position, bool) {
		x, ok := bound.(*binding) //nolint:errorlint // The node itself, not a chain search.
		if !ok || x == nil {
			return position.Position{}, false
		}

		pos, ok := x.positions[nested]

		return pos, ok
	},
}

func TestFuncs_Rebound(t *testing.T) {
	t.Parallel()

	inner := &binding{name: "g", inner: &node{msg: "inner"}}

	tcs := map[string]struct {
		err  error
		want bool
	}{
		"not a binding": {
			err:  errors.New("boom"),
			want: false,
		},
		"binding of a node": {
			err:  &binding{name: "f", inner: &node{msg: "main"}},
			want: false,
		},
		"binding of a binding": {
			err:  &binding{name: "f", inner: inner},
			want: true,
		},
		"binding through a wrapper": {
			err:  &binding{name: "f", inner: fmt.Errorf("ctx: %w", inner)},
			want: true,
		},
		"binding through a node without a location": {
			err:  &binding{name: "f", inner: &node{msg: "main", cause: inner}},
			want: true,
		},
		"located node ends the chain first": {
			err:  &binding{name: "f", inner: &node{msg: "main", cause: inner, located: true}},
			want: false,
		},
		"join follows its first branch": {
			err:  &binding{name: "f", inner: errors.Join(inner, errors.New("other"))},
			want: true,
		},
		"join skips a branch that is nothing": {
			err:  &binding{name: "f", inner: errors.Join((*node)(nil), inner)},
			want: true,
		},
		"join whose first branch is foreign": {
			err:  &binding{name: "f", inner: errors.Join(errors.New("other"), inner)},
			want: false,
		},
		"nil binding": {
			err:  (*binding)(nil),
			want: false,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, funcs.Rebound(tc.err))
		})
	}
}

func TestFuncs_Positioned(t *testing.T) {
	t.Parallel()

	a := &node{msg: "bad a"}
	b := &node{msg: "bad b"}
	positions := map[error]position.Position{
		a: position.New(0, 3),
		b: position.New(1, 3),
	}

	tcs := map[string]struct {
		bind func(err error) *binding
		err  error
		want string
	}{
		"no nested errors": {
			err:  &node{msg: "main"},
			want: "f: main",
		},
		"nested lines get positions": {
			err:  &node{msg: "main", nested: []error{a, b}},
			want: "f: main\nf:1:4: bad a\nf:2:4: bad b",
		},
		"unresolved nested line stays plain": {
			err:  &node{msg: "main", nested: []error{a, &node{msg: "no position"}}},
			want: "f: main\nf:1:4: bad a\nno position",
		},
		"nested lines behind a wrapper": {
			err:  fmt.Errorf("ctx: %w", &node{msg: "main", nested: []error{a}}),
			want: "f: ctx: main\nf:1:4: bad a",
		},
		"rewritten message stays as it is": {
			err:  yamltest.RewriteError{Err: &node{msg: "main", nested: []error{a}}},
			want: "f: rewritten",
		},
		"empty message leaves the lines alone": {
			err:  &node{nested: []error{a, b}},
			want: "f: f:1:4: bad a\nf:2:4: bad b",
		},
		"nested errors of a nested error": {
			err:  &node{msg: "main", nested: []error{&node{msg: "mid", nested: []error{a}}}},
			want: "f: main\nmid\nf:1:4: bad a",
		},
		"rebound binding leaves the message as the inner one wrote it": {
			err: &node{msg: "main", nested: []error{b}, cause: &binding{
				name:      "g",
				inner:     &node{msg: "inner", nested: []error{a}},
				positions: positions,
			}},
			want: "main g: inner\ng:1:4: bad a\nbad b",
		},
		"located node above an inner binding keeps both positions": {
			err: &node{msg: "main", located: true, nested: []error{b}, cause: &binding{
				name:      "g",
				inner:     &node{msg: "inner", nested: []error{a}},
				positions: positions,
			}},
			want: "f: main g: inner\ng:1:4: bad a\nf:2:4: bad b",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			bound := &binding{name: "f", inner: tc.err, positions: positions}

			assert.Equal(t, tc.want, bound.Error())
		})
	}
}

func TestFuncs_Split(t *testing.T) {
	t.Parallel()

	a := &node{msg: "bad a"}
	b := &node{msg: "bad b"}
	positions := map[error]position.Position{a: position.New(0, 3)}

	t.Run("splits the head from the nested lines", func(t *testing.T) {
		t.Parallel()

		err := &node{msg: "main", nested: []error{a, b}}

		head, nested, ok := funcs.Split(err, err.Error(), nil)
		assert.True(t, ok)
		assert.Equal(t, "main", head)
		assert.Equal(t, []errchain.Nested{{Err: a, Text: "bad a"}, {Err: b, Text: "bad b"}}, nested)
	})

	t.Run("lines a binding wrote carry its positions", func(t *testing.T) {
		t.Parallel()

		bound := &binding{name: "f", inner: &node{msg: "main", nested: []error{a, b}}, positions: positions}

		head, nested, ok := funcs.Split(bound, bound.Error(), nil)
		assert.True(t, ok)
		assert.Equal(t, "f: main", head)
		assert.Equal(t, []errchain.Nested{
			{Err: a, Text: "bad a", Binding: bound, Positioned: true},
			{Err: b, Text: "bad b", Binding: bound, Positioned: true},
		}, nested)
	})

	t.Run("message without the lines reports false", func(t *testing.T) {
		t.Parallel()

		err := yamltest.RewriteError{Err: &node{msg: "main", nested: []error{a}}}

		head, nested, ok := funcs.Split(err, err.Error(), nil)
		assert.False(t, ok)
		assert.Equal(t, "rewritten", head)
		assert.Empty(t, nested)
	})

	t.Run("message that is the lines has an empty head", func(t *testing.T) {
		t.Parallel()

		err := &node{nested: []error{a, b}}

		head, nested, ok := funcs.Split(err, err.Error(), nil)
		assert.True(t, ok)
		assert.Empty(t, head)
		assert.Len(t, nested, 2)
	})
}

func TestFuncs_Line(t *testing.T) {
	t.Parallel()

	a := &node{msg: "bad a"}
	bound := &binding{name: "f", positions: map[error]position.Position{a: position.New(4, 1)}}

	assert.Equal(t, "f:5:2: bad a", funcs.Line(bound, a))
	assert.Equal(t, "no position", funcs.Line(bound, &node{msg: "no position"}))
}

func TestFormatPosition(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "f.yaml:3:5:", errchain.FormatPosition("f.yaml", position.New(2, 4)))
	assert.Equal(t, "3:5:", errchain.FormatPosition("", position.New(2, 4)))
}

func TestPrefix(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "1:1: msg", errchain.Prefix("1:1:", "msg"))
	assert.Equal(t, "1:1:", errchain.Prefix("1:1:", ""))
}
