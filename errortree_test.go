package niceyaml_test

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// countNodes returns the number of nodes with text in t.
func countNodes(t niceyaml.ErrorTree) int {
	n := 0
	if t.Text != "" {
		n++
	}

	for _, child := range t.Children {
		n += countNodes(child)
	}

	return n
}

// sparseJoinError is an error joined from several, the way [errors.Join]
// builds one, except that it keeps a nil branch instead of dropping it.
// Its message is the messages of the branches that are not nil, one per
// line.
type sparseJoinError struct {
	errs []error
}

// Error returns the messages of the branches that are not nil, one per
// line.
func (e sparseJoinError) Error() string {
	msgs := make([]string, 0, len(e.errs))

	for _, err := range e.errs {
		if err != nil {
			msgs = append(msgs, err.Error())
		}
	}

	return strings.Join(msgs, "\n")
}

// Unwrap returns the branches, nil ones included.
func (e sparseJoinError) Unwrap() []error {
	return e.errs
}

// violationsError is a multi-error of its own type, as a validator might
// build one. Its message counts its branches rather than listing them.
type violationsError []error

// Error returns the number of branches.
func (e violationsError) Error() string {
	return fmt.Sprintf("%d violations", len(e))
}

// Unwrap returns the branches.
func (e violationsError) Unwrap() []error {
	return e
}

// listError is a multi-error of its own type that lists the messages of
// its branches on one line, joined with "; ", as some multi-error
// libraries write them.
type listError []error

// Error returns the messages of the branches joined with "; ".
func (e listError) Error() string {
	msgs := make([]string, 0, len(e))
	for _, err := range e {
		msgs = append(msgs, err.Error())
	}

	return strings.Join(msgs, "; ")
}

// Unwrap returns the branches.
func (e listError) Unwrap() []error {
	return e
}

// countingError is an error that counts the calls to its Error method.
type countingError struct {
	calls *atomic.Int64
	msg   string
}

// Error returns the message and counts the call.
func (e countingError) Error() string {
	e.calls.Add(1)

	return e.msg
}

func TestErrorTree_New_LeftDeepJoin(t *testing.T) {
	t.Parallel()

	const n = 200

	tcs := map[string]struct {
		build  func(error) error
		prefix string
	}{
		"join": {
			build: func(err error) error { return err },
		},
		"rebased join": {
			build:  func(err error) error { return niceyaml.Rebase(err, paths.Root()) },
			prefix: "$: ",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var (
				calls  atomic.Int64
				joined error
				want   niceyaml.ErrorTree
			)

			for i := range n {
				msg := fmt.Sprintf("error %d", i)
				joined = errors.Join(joined, countingError{calls: &calls, msg: msg})
				want.Children = append(want.Children, niceyaml.ErrorTree{Text: tc.prefix + msg})
			}

			got := niceyaml.NewErrorTree(tc.build(joined))

			assert.Equal(t, want, got)
			// Finding each join by rebuilding its message would read every
			// message once per join above it, n*n/2 times in all.
			assert.LessOrEqual(t, calls.Load(), int64(2*n))
		})
	}
}

func TestErrorTree_New_DeepMultiWrap(t *testing.T) {
	t.Parallel()

	const n = 20

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("f.yaml"))

	tcs := map[string]struct {
		build func(calls *atomic.Int64) error
		want  niceyaml.ErrorTree
	}{
		"left-deep wrappers over plain errors": {
			build: func(calls *atomic.Int64) error {
				var err error = countingError{calls: calls, msg: "e0"}

				for i := 1; i < n; i++ {
					err = fmt.Errorf("%w; %w", err, countingError{calls: calls, msg: fmt.Sprintf("e%d", i)})
				}

				return err
			},
			want: niceyaml.ErrorTree{Text: "e0; e1; e2; e3; e4; e5; e6; e7; e8; e9; " +
				"e10; e11; e12; e13; e14; e15; e16; e17; e18; e19"},
		},
		"sentinel wrappers over a located error": {
			build: func(calls *atomic.Int64) error {
				sentinel := countingError{calls: calls, msg: "invalid"}

				var err error = niceyaml.NewError("bad", niceyaml.AtPath(paths.Root().Child("a")))

				for i := 1; i < n; i++ {
					err = fmt.Errorf("%w: %w", sentinel, err)
				}

				return err
			},
			want: niceyaml.ErrorTree{Text: strings.Repeat("invalid: ", n-1) + "$.a: bad"},
		},
	}

	ops := map[string]func(error){
		"tree":   func(err error) { niceyaml.NewErrorTree(err) },
		"bind":   func(err error) { _ = source.Bind(err).Error() },
		"rebase": func(err error) { _ = niceyaml.Rebase(err, paths.Root()).Error() },
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var calls atomic.Int64

			err := tc.build(&calls)

			assert.Equal(t, tc.want, niceyaml.NewErrorTree(err))

			for op, run := range ops {
				calls.Store(0)
				run(err)

				// Deciding whether each wrapper keeps a branch by walking
				// the chain below it twice per level reads the messages
				// 2^n times.
				assert.LessOrEqual(t, calls.Load(), int64(2*n*n), op)
			}
		})
	}
}

func TestErrorTree_New_MultiWrap(t *testing.T) {
	t.Parallel()

	errA := errors.New("first")
	errB := errors.New("second")

	tcs := map[string]struct {
		err  error
		want niceyaml.ErrorTree
	}{
		"wrapper with two verbs over plain errors is one node": {
			err:  fmt.Errorf("parse path: %w: %w", errA, errB),
			want: niceyaml.ErrorTree{Text: "parse path: first: second"},
		},
		"wrapper with two verbs goes on through its one branch with children": {
			err: fmt.Errorf("%w: %w", errA, niceyaml.NewError("summary", niceyaml.WithErrors(errB))),
			want: niceyaml.ErrorTree{
				Text:     "first: summary",
				Children: []niceyaml.ErrorTree{{Text: "second"}},
			},
		},
		"wrapper with two verbs keeps its own text": {
			err: fmt.Errorf("parse path: %w; %w",
				niceyaml.NewError("x", niceyaml.WithErrors(errA)),
				niceyaml.NewError("y", niceyaml.WithErrors(errB)),
			),
			want: niceyaml.ErrorTree{
				Text: "parse path: x; y",
				Children: []niceyaml.ErrorTree{
					{Text: "x", Children: []niceyaml.ErrorTree{{Text: "first"}}},
					{Text: "y", Children: []niceyaml.ErrorTree{{Text: "second"}}},
				},
			},
		},
		"multi-error of its own type keeps each branch": {
			err: violationsError{errA, errB},
			want: niceyaml.ErrorTree{
				Text: "2 violations",
				Children: []niceyaml.ErrorTree{
					{Text: "first"},
					{Text: "second"},
				},
			},
		},
		"bound multi-error of its own type keeps each branch": {
			err: yamltest.Bind(t, niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName("f.yaml")),
				violationsError{errA, errB}),
			want: niceyaml.ErrorTree{
				Text: "f.yaml: 2 violations",
				Children: []niceyaml.ErrorTree{
					{Text: "first"},
					{Text: "second"},
				},
			},
		},
		"join below a wrapper yields one child per branch": {
			err: fmt.Errorf("while checking: %w", errors.Join(errA, errB)),
			want: niceyaml.ErrorTree{
				Text: "while checking: first\nsecond",
				Children: []niceyaml.ErrorTree{
					{Text: "first"},
					{Text: "second"},
				},
			},
		},
		"children that add nothing leave no children": {
			err:  niceyaml.NewError("outer", niceyaml.WithErrors(niceyaml.NewError(""))),
			want: niceyaml.ErrorTree{Text: "outer"},
		},
		"join at the root is textless": {
			err: errors.Join(errA, errB),
			want: niceyaml.ErrorTree{
				Children: []niceyaml.ErrorTree{
					{Text: "first"},
					{Text: "second"},
				},
			},
		},
		"join with a leading nil branch is textless": {
			err: sparseJoinError{errs: []error{nil, errA, errB}},
			want: niceyaml.ErrorTree{
				Children: []niceyaml.ErrorTree{
					{Text: "first"},
					{Text: "second"},
				},
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, niceyaml.NewErrorTree(tc.err))
		})
	}
}

func TestErrorTree_New(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("f.yaml"))
	other := niceyaml.NewSourceFromString("c: 3\n", niceyaml.WithName("g.yaml"))
	wide := niceyaml.NewSourceFromString("a: hello world\nb: 2\n", niceyaml.WithName("f.yaml"))

	badA := func() *niceyaml.Error {
		return niceyaml.NewError("bad a", niceyaml.AtPath(paths.Root().Child("a")))
	}
	badB := func() *niceyaml.Error {
		return niceyaml.NewError("bad b", niceyaml.AtPath(paths.Root().Child("b")))
	}

	tcs := map[string]struct {
		err  error
		want niceyaml.ErrorTree
		// The multiLine flag marks an error whose message holds a line that
		// is not a node of its own, so the node count check does not apply.
		multiLine bool
	}{
		"nil": {
			err:  nil,
			want: niceyaml.ErrorTree{},
		},
		"nil Error": {
			err:  (*niceyaml.Error)(nil),
			want: niceyaml.ErrorTree{},
		},
		"plain error": {
			err:  errors.New("boom"),
			want: niceyaml.ErrorTree{Text: "boom"},
		},
		"error without nested errors": {
			err:  badA(),
			want: niceyaml.ErrorTree{Text: "$.a: bad a"},
		},
		"nested errors are children": {
			err: niceyaml.NewError("2 problems", niceyaml.WithErrors(badA(), niceyaml.NewError("no path"))),
			want: niceyaml.ErrorTree{
				Text: "2 problems",
				Children: []niceyaml.ErrorTree{
					{Text: "$.a: bad a"},
					{Text: "no path"},
				},
			},
		},
		"bound children carry their position without the name": {
			err: yamltest.Bind(t, source, niceyaml.NewError("2 problems", niceyaml.WithErrors(badA(), badB()))),
			want: niceyaml.ErrorTree{
				Text: "f.yaml: 2 problems",
				Children: []niceyaml.ErrorTree{
					{Text: "1:4: $.a: bad a"},
					{Text: "2:4: $.b: bad b"},
				},
			},
		},
		"bound child reports the position it was given": {
			// The position sits inside the token of the value, so the
			// child names it as its own message does, rather than the
			// start of the token the excerpt marks.
			err: yamltest.Bind(t, wide, niceyaml.NewError("2 problems", niceyaml.WithErrors(
				badB(),
				niceyaml.NewError("mid", niceyaml.AtPosition(position.New(0, 8))),
			))),
			want: niceyaml.ErrorTree{
				Text: "f.yaml: 2 problems",
				Children: []niceyaml.ErrorTree{
					{Text: "1:9: mid"},
					{Text: "2:4: $.b: bad b"},
				},
			},
		},
		"children sort by position": {
			err: yamltest.Bind(t, source, niceyaml.NewError("2 problems", niceyaml.WithErrors(badB(), badA()))),
			want: niceyaml.ErrorTree{
				Text: "f.yaml: 2 problems",
				Children: []niceyaml.ErrorTree{
					{Text: "1:4: $.a: bad a"},
					{Text: "2:4: $.b: bad b"},
				},
			},
		},
		"children without a position follow the positioned ones": {
			err: yamltest.Bind(t, source, niceyaml.NewError("3 problems", niceyaml.WithErrors(
				niceyaml.NewError("bad x", niceyaml.AtPath(paths.Root().Child("x"))),
				niceyaml.NewError("no path"),
				badA(),
			))),
			want: niceyaml.ErrorTree{
				Text: "f.yaml: 3 problems",
				Children: []niceyaml.ErrorTree{
					{Text: "1:4: $.a: bad a"},
					{Text: "$.x: bad x"},
					{Text: "no path"},
				},
			},
		},
		"child wrapping a binding of the same source keeps one position": {
			err: yamltest.Bind(t, source, niceyaml.NewError("outer", niceyaml.WithErrors(
				fmt.Errorf("ctx: %w", yamltest.Bind(t, source, badA())),
			))),
			want: niceyaml.ErrorTree{
				Text: "f.yaml: outer",
				Children: []niceyaml.ErrorTree{
					{Text: "ctx: f.yaml:1:4: $.a: bad a"},
				},
			},
		},
		"child wrapping a binding in Errors alone reads as the binding": {
			err: yamltest.Bind(t, source, niceyaml.NewError("summary", niceyaml.WithErrors(
				niceyaml.WrapError(yamltest.Bind(t, source, badA())),
				niceyaml.WrapError(yamltest.Bind(t, source, badB()), niceyaml.WithErrors(niceyaml.NewError("x"))),
				niceyaml.WrapError(yamltest.Bind(t, source, errors.New("plain"))),
				niceyaml.WrapError(yamltest.Bind(t, source,
					niceyaml.WrapError(yamltest.Bind(t, source, badA()), niceyaml.WithErrors(niceyaml.NewError("y"))),
				)),
			))),
			want: niceyaml.ErrorTree{
				Text: "f.yaml: summary",
				Children: []niceyaml.ErrorTree{
					{Text: "1:4: $.a: bad a"},
					{Text: "1:4: $.a: bad a", Children: []niceyaml.ErrorTree{{Text: "y"}}},
					{Text: "2:4: $.b: bad b", Children: []niceyaml.ErrorTree{{Text: "x"}}},
					{Text: "plain"},
				},
			},
		},
		"root with a position keeps it": {
			err: yamltest.Bind(t, source, niceyaml.NewError("outer",
				niceyaml.AtPath(paths.Root().Child("a")),
				niceyaml.WithErrors(badB()),
			)),
			want: niceyaml.ErrorTree{
				Text: "f.yaml:1:4: $.a: outer",
				Children: []niceyaml.ErrorTree{
					{Text: "2:4: $.b: bad b"},
				},
			},
		},
		"wrapper context stays in front of the root": {
			err: fmt.Errorf("document 0: %w", yamltest.Bind(t, source,
				niceyaml.NewError("2 problems", niceyaml.WithErrors(badA(), badB())),
			)),
			want: niceyaml.ErrorTree{
				Text: "document 0: f.yaml: 2 problems",
				Children: []niceyaml.ErrorTree{
					{Text: "1:4: $.a: bad a"},
					{Text: "2:4: $.b: bad b"},
				},
			},
		},
		"wrapper inside the binding stays in front of the root": {
			err: yamltest.Bind(t, source, fmt.Errorf("document 0: %w",
				niceyaml.NewError("2 problems", niceyaml.WithErrors(badA(), badB())),
			)),
			want: niceyaml.ErrorTree{
				Text: "f.yaml: document 0: 2 problems",
				Children: []niceyaml.ErrorTree{
					{Text: "1:4: $.a: bad a"},
					{Text: "2:4: $.b: bad b"},
				},
			},
		},
		"nested error with nested errors is a subtree": {
			err: yamltest.Bind(t, source, niceyaml.NewError("outer", niceyaml.WithErrors(
				niceyaml.NewError("mid",
					niceyaml.AtPath(paths.Root().Child("a")),
					niceyaml.WithErrors(badB()),
				),
			))),
			want: niceyaml.ErrorTree{
				Text: "f.yaml: outer",
				Children: []niceyaml.ErrorTree{
					{
						Text: "1:4: $.a: mid",
						Children: []niceyaml.ErrorTree{
							{Text: "2:4: $.b: bad b"},
						},
					},
				},
			},
		},
		"nested error bound to another source keeps its own positions": {
			err: yamltest.Bind(t, source, niceyaml.NewError("outer", niceyaml.WithErrors(
				niceyaml.WrapError(yamltest.Bind(t, other,
					niceyaml.NewError("inner", niceyaml.WithErrors(
						niceyaml.NewError("bad c", niceyaml.AtPath(paths.Root().Child("c"))),
					)),
				)),
			))),
			want: niceyaml.ErrorTree{
				Text: "f.yaml: outer",
				Children: []niceyaml.ErrorTree{
					{
						Text: "g.yaml: inner",
						Children: []niceyaml.ErrorTree{
							{Text: "1:4: $.c: bad c"},
						},
					},
				},
			},
		},
		"binding rebound to another binding keeps the inner positions": {
			err: yamltest.Bind(t, source, fmt.Errorf("outer: %w", yamltest.Bind(t, other,
				niceyaml.NewError("inner", niceyaml.WithErrors(
					niceyaml.NewError("bad c", niceyaml.AtPath(paths.Root().Child("c"))),
				)),
			))),
			want: niceyaml.ErrorTree{
				Text: "outer: g.yaml: inner",
				Children: []niceyaml.ErrorTree{
					{Text: "1:4: $.c: bad c"},
				},
			},
		},
		"located error above an inner binding positions its own nested errors": {
			err: yamltest.Bind(t, source, niceyaml.WrapError(
				yamltest.Bind(t, other, niceyaml.NewError("inner", niceyaml.WithErrors(
					niceyaml.NewError("bad c", niceyaml.AtPath(paths.Root().Child("c"))),
				))),
				niceyaml.AtPath(paths.Root().Child("a")),
				niceyaml.WithErrors(badB()),
			)),
			want: niceyaml.ErrorTree{
				Text: "f.yaml:1:4: $.a: g.yaml: inner",
				Children: []niceyaml.ErrorTree{
					{Text: "2:4: $.b: bad b"},
					{Text: "g.yaml:1:4: $.c: bad c"},
				},
			},
		},
		"joined errors form a forest": {
			err: errors.Join(
				yamltest.Bind(t, source, badA()),
				yamltest.Bind(t, other, niceyaml.NewError("bad c", niceyaml.AtPath(paths.Root().Child("c")))),
			),
			want: niceyaml.ErrorTree{
				Children: []niceyaml.ErrorTree{
					{Text: "f.yaml:1:4: $.a: bad a"},
					{Text: "g.yaml:1:4: $.c: bad c"},
				},
			},
		},
		"joined errors with nested errors are subtrees": {
			err: errors.Join(
				yamltest.Bind(t, source, niceyaml.NewError("2 problems", niceyaml.WithErrors(badA(), badB()))),
				yamltest.Bind(t, other, niceyaml.NewError("bad c", niceyaml.AtPath(paths.Root().Child("c")))),
			),
			want: niceyaml.ErrorTree{
				Children: []niceyaml.ErrorTree{
					{
						Text: "f.yaml: 2 problems",
						Children: []niceyaml.ErrorTree{
							{Text: "1:4: $.a: bad a"},
							{Text: "2:4: $.b: bad b"},
						},
					},
					{Text: "g.yaml:1:4: $.c: bad c"},
				},
			},
		},
		"unbound Error keeps the order of its bindings": {
			err: niceyaml.NewError("summary", niceyaml.WithErrors(
				yamltest.Bind(t, source, badB()),
				yamltest.Bind(t, source, badA()),
			)),
			want: niceyaml.ErrorTree{
				Text: "summary",
				Children: []niceyaml.ErrorTree{
					{Text: "f.yaml:2:4: $.b: bad b"},
					{Text: "f.yaml:1:4: $.a: bad a"},
				},
			},
		},
		"unbound join keeps the order of its bindings": {
			err: errors.Join(
				yamltest.Bind(t, source, badB()),
				yamltest.Bind(t, source, badA()),
			),
			want: niceyaml.ErrorTree{
				Children: []niceyaml.ErrorTree{
					{Text: "f.yaml:2:4: $.b: bad b"},
					{Text: "f.yaml:1:4: $.a: bad a"},
				},
			},
		},
		"bound join of two sources keeps the order it was given": {
			err: yamltest.Bind(t, source, errors.Join(
				yamltest.Bind(t, source, badB()),
				yamltest.Bind(t, other, niceyaml.NewError("bad c", niceyaml.AtPath(paths.Root().Child("c")))),
			)),
			want: niceyaml.ErrorTree{
				Children: []niceyaml.ErrorTree{
					{Text: "f.yaml:2:4: $.b: bad b"},
					{Text: "g.yaml:1:4: $.c: bad c"},
				},
			},
			multiLine: true,
		},
		"bound join is a forest of named bindings": {
			err: yamltest.Bind(t, source, errors.Join(badB(), badA())),
			want: niceyaml.ErrorTree{
				Children: []niceyaml.ErrorTree{
					{Text: "f.yaml:1:4: $.a: bad a"},
					{Text: "f.yaml:2:4: $.b: bad b"},
				},
			},
			multiLine: true,
		},
		"bound nested join flattens into one forest": {
			err: yamltest.Bind(t, source, errors.Join(errors.Join(badB()), badA())),
			want: niceyaml.ErrorTree{
				Children: []niceyaml.ErrorTree{
					{Text: "f.yaml:1:4: $.a: bad a"},
					{Text: "f.yaml:2:4: $.b: bad b"},
				},
			},
		},
		"join nested under a message gives its place to its branches": {
			err: yamltest.Bind(t, source, niceyaml.NewError("outer", niceyaml.WithErrors(
				errors.Join(badB(), badA()),
			))),
			want: niceyaml.ErrorTree{
				Text: "f.yaml: outer",
				Children: []niceyaml.ErrorTree{
					{Text: "1:4: $.a: bad a"},
					{Text: "2:4: $.b: bad b"},
				},
			},
		},
		"nested join bound to another source names it on its branches": {
			err: yamltest.Bind(t, source, niceyaml.NewError("outer", niceyaml.WithErrors(
				badA(),
				yamltest.Bind(t, other, errors.Join(
					niceyaml.NewError("bad c", niceyaml.AtPath(paths.Root().Child("c"))),
					niceyaml.NewError("start", niceyaml.AtPosition(position.New(0, 0))),
				)),
			))),
			want: niceyaml.ErrorTree{
				Text: "f.yaml: outer",
				Children: []niceyaml.ErrorTree{
					{Text: "1:4: $.a: bad a"},
					{Text: "g.yaml:1:1: start"},
					{Text: "g.yaml:1:4: $.c: bad c"},
				},
			},
		},
		"error that only wraps a join is the join": {
			err: niceyaml.WrapError(errors.Join(badA(), badB())),
			want: niceyaml.ErrorTree{
				Children: []niceyaml.ErrorTree{
					{Text: "$.a: bad a"},
					{Text: "$.b: bad b"},
				},
			},
		},
		"bound error that only wraps a join is a forest of named bindings": {
			err: yamltest.Bind(t, source, niceyaml.WrapError(errors.Join(badB(), badA()))),
			want: niceyaml.ErrorTree{
				Children: []niceyaml.ErrorTree{
					{Text: "f.yaml:1:4: $.a: bad a"},
					{Text: "f.yaml:2:4: $.b: bad b"},
				},
			},
			multiLine: true,
		},
		"nested error that only wraps a join gives its place to the branches": {
			err: yamltest.Bind(t, source, niceyaml.NewError("outer", niceyaml.WithErrors(
				niceyaml.WrapError(errors.Join(badB(), badA())),
			))),
			want: niceyaml.ErrorTree{
				Text: "f.yaml: outer",
				Children: []niceyaml.ErrorTree{
					{Text: "1:4: $.a: bad a"},
					{Text: "2:4: $.b: bad b"},
				},
			},
		},
		"error that only wraps a bound join is the bound join": {
			err: niceyaml.WrapError(yamltest.Bind(t, source, errors.Join(badB(), badA()))),
			want: niceyaml.ErrorTree{
				Children: []niceyaml.ErrorTree{
					{Text: "f.yaml:1:4: $.a: bad a"},
					{Text: "f.yaml:2:4: $.b: bad b"},
				},
			},
		},
		"nested error that only wraps a bound join gives its place to the branches": {
			err: yamltest.Bind(t, source, niceyaml.NewError("outer", niceyaml.WithErrors(
				niceyaml.WrapError(yamltest.Bind(t, source, errors.Join(badB(), badA()))),
			))),
			want: niceyaml.ErrorTree{
				Text: "f.yaml: outer",
				Children: []niceyaml.ErrorTree{
					{Text: "1:4: $.a: bad a"},
					{Text: "2:4: $.b: bad b"},
				},
			},
		},
		"nested error that only wraps a bound join with a nil branch gives its place to the rest": {
			err: yamltest.Bind(t, source, niceyaml.NewError("outer", niceyaml.WithErrors(
				niceyaml.WrapError(yamltest.Bind(t, source, errors.Join((*niceyaml.Error)(nil), badB()))),
			))),
			want: niceyaml.ErrorTree{
				Text: "f.yaml: outer",
				Children: []niceyaml.ErrorTree{
					{Text: "2:4: $.b: bad b"},
				},
			},
		},
		"bound join below a wrapper keeps the wrapper as the root": {
			err: yamltest.Bind(t, source, fmt.Errorf("ctx: %w", errors.Join(badA(), badB()))),
			want: niceyaml.ErrorTree{
				Text: "f.yaml: ctx: $.a: bad a\n$.b: bad b",
				Children: []niceyaml.ErrorTree{
					{Text: "1:4: $.a: bad a"},
					{Text: "2:4: $.b: bad b"},
				},
			},
			multiLine: true,
		},
		"bound join that leads with another source names the root": {
			err: yamltest.Bind(t, source, fmt.Errorf("ctx: %w", errors.Join(
				yamltest.Bind(t, other, niceyaml.NewError("bad c", niceyaml.AtPath(paths.Root().Child("c")))),
				badB(),
			))),
			want: niceyaml.ErrorTree{
				Text: "f.yaml: ctx: g.yaml:1:4: $.c: bad c\n$.b: bad b",
				Children: []niceyaml.ErrorTree{
					{Text: "g.yaml:1:4: $.c: bad c"},
					{Text: "2:4: $.b: bad b"},
				},
			},
			multiLine: true,
		},
		"join of one error is that error": {
			err:  errors.Join(errors.Join(errors.New("boom"))),
			want: niceyaml.ErrorTree{Text: "boom"},
		},
		"nested joins flatten into one forest": {
			err: errors.Join(
				errors.Join(errors.New("one"), errors.New("two")),
				nil,
				errors.New("three"),
			),
			want: niceyaml.ErrorTree{
				Children: []niceyaml.ErrorTree{
					{Text: "one"},
					{Text: "two"},
					{Text: "three"},
				},
			},
		},
		"error without a message and nested errors is a forest": {
			err: niceyaml.WrapError(nil, niceyaml.WithErrors(
				niceyaml.NewError("one"),
				niceyaml.NewError("two"),
			)),
			want: niceyaml.ErrorTree{
				Children: []niceyaml.ErrorTree{
					{Text: "one"},
					{Text: "two"},
				},
			},
		},
		"nil nested errors are skipped": {
			err:  niceyaml.NewError("main", niceyaml.WithErrors(nil, niceyaml.NewError("one"), nil)),
			want: niceyaml.ErrorTree{Text: "main", Children: []niceyaml.ErrorTree{{Text: "one"}}},
		},
		"rewritten message keeps its children": {
			err:  yamltest.RewriteError{Err: niceyaml.NewError("main", niceyaml.WithErrors(badA()))},
			want: niceyaml.ErrorTree{Text: "rewritten", Children: []niceyaml.ErrorTree{{Text: "$.a: bad a"}}},
		},
		"rewritten bound message keeps its children with positions": {
			err: yamltest.Bind(
				t,
				source,
				yamltest.RewriteError{Err: niceyaml.NewError("main", niceyaml.WithErrors(badA()))},
			),
			want: niceyaml.ErrorTree{
				Text:     "f.yaml: rewritten",
				Children: []niceyaml.ErrorTree{{Text: "1:4: $.a: bad a"}},
			},
		},
		"multi-line nested message stays whole": {
			err: yamltest.Bind(t, source, niceyaml.NewError("outer", niceyaml.WithErrors(
				niceyaml.NewError("bad a\n  see docs", niceyaml.AtPath(paths.Root().Child("a"))),
			))),
			want: niceyaml.ErrorTree{
				Text: "f.yaml: outer",
				Children: []niceyaml.ErrorTree{
					{Text: "1:4: $.a: bad a\n  see docs"},
				},
			},
			multiLine: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := niceyaml.NewErrorTree(tc.err)
			assert.Equal(t, tc.want, got)

			// The %+v verb of a bound error lists every nested error on a
			// line of its own before the excerpt, so that part has one line
			// per node of the tree. A wrapper around a binding has no
			// formatter of its own, so the check applies to a bound error
			// at the top.
			bound, ok := tc.err.(*niceyaml.SourceError) //nolint:errorlint // The value itself formats.
			if ok && !tc.multiLine {
				msg, _, _ := strings.Cut(fmt.Sprintf("%+v", bound), "\n\n")
				assert.Len(t, strings.Split(msg, "\n"), countNodes(got))
			}
		})
	}
}
