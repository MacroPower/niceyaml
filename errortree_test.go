package niceyaml_test

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

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

// textTree returns t with the text and children of each node alone, so a
// test of the shape of a tree compares it without the errors behind it.
func textTree(t niceyaml.ErrorTree) niceyaml.ErrorTree {
	out := niceyaml.ErrorTree{Text: t.Text}

	for _, child := range t.Children {
		out.Children = append(out.Children, textTree(child))
	}

	return out
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

// upperError models a wrapper that rewrites the message of the error it
// wraps. Its message is that message in upper case.
type upperError struct {
	err error
}

// Error returns the message of the wrapped error in upper case.
func (e upperError) Error() string {
	return strings.ToUpper(e.err.Error())
}

// Unwrap returns the wrapped error.
func (e upperError) Unwrap() error {
	return e.err
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

// countingViolationsError is a multi-error of its own type, as
// [violationsError] is, that counts the calls to its Error method.
type countingViolationsError struct {
	calls *atomic.Int64
	errs  []error
}

// Error returns the number of branches and counts the call.
func (e countingViolationsError) Error() string {
	e.calls.Add(1)

	return fmt.Sprintf("%d violations", len(e.errs))
}

// Unwrap returns the branches.
func (e countingViolationsError) Unwrap() []error {
	return e.errs
}

func TestErrorTree_New_LeftDeepJoin(t *testing.T) {
	t.Parallel()

	const n = 200

	source := niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName("f.yaml"))

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
		"bound join": {
			build:  func(err error) error { return source.Bind(err) },
			prefix: "f.yaml: ",
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

			got := textTree(niceyaml.NewErrorTree(tc.build(joined)))

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

			assert.Equal(t, tc.want, textTree(niceyaml.NewErrorTree(err)))

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

func TestErrorTree_New_DeepRebind(t *testing.T) {
	t.Parallel()

	const n = 20

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("f.yaml"))

	tcs := map[string]struct {
		build func(calls *atomic.Int64) error
		want  string
	}{
		"list multi-error bound at each level": {
			build: func(calls *atomic.Int64) error {
				var err error = countingError{calls: calls, msg: "e0"}

				for i := 1; i < n; i++ {
					next := countingError{calls: calls, msg: fmt.Sprintf("e%d", i)}
					err = source.Bind(listError{err, next})
				}

				return err
			},
			want: "f.yaml: e0; e1; e2; e3; e4; e5; e6; e7; e8; e9; " +
				"e10; e11; e12; e13; e14; e15; e16; e17; e18; e19",
		},
		"join bound at each level": {
			build: func(calls *atomic.Int64) error {
				var err error = countingError{calls: calls, msg: "e0"}

				for i := 1; i < n; i++ {
					next := countingError{calls: calls, msg: fmt.Sprintf("e%d", i)}
					err = source.Bind(errors.Join(err, next))
				}

				return err
			},
			want: stringtest.JoinLF(
				"f.yaml: e0", "f.yaml: e1", "f.yaml: e2", "f.yaml: e3", "f.yaml: e4",
				"f.yaml: e5", "f.yaml: e6", "f.yaml: e7", "f.yaml: e8", "f.yaml: e9",
				"f.yaml: and 10 more",
			),
		},
		"multi-wrap over a located error bound at each level": {
			build: func(calls *atomic.Int64) error {
				err := source.Bind(countingError{calls: calls, msg: "e0"})

				for i := 1; i < n; i++ {
					located := niceyaml.NewError(fmt.Sprintf("e%d", i), niceyaml.AtPath(paths.Root().Child("a")))
					err = source.Bind(fmt.Errorf("%w; %w", err, located))
				}

				return err
			},
			want: "f.yaml: e0; $.a: e1; $.a: e2; $.a: e3; $.a: e4; $.a: e5; " +
				"$.a: e6; $.a: e7; $.a: e8; $.a: e9; $.a: e10; $.a: e11; " +
				"$.a: e12; $.a: e13; $.a: e14; $.a: e15; $.a: e16; $.a: e17; " +
				"$.a: e18; $.a: e19",
		},
	}

	ops := map[string]func(error){
		"message": func(err error) { _ = err.Error() },
		"tree":    func(err error) { niceyaml.NewErrorTree(err) },
		"rebind": func(err error) {
			located := niceyaml.NewError("last", niceyaml.AtPath(paths.Root().Child("b")))
			_ = source.Bind(fmt.Errorf("%w; %w", err, located)).Error()
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var calls atomic.Int64

			for op, run := range ops {
				err := tc.build(&calls)

				calls.Store(0)
				run(err)

				// Building the message of each binding anew for every read
				// rebuilds the bindings below it once per binding above
				// them, n*n times in all.
				assert.LessOrEqual(t, calls.Load(), int64(8*n), op)
				assert.Equal(t, tc.want, err.Error(), op)
			}
		})
	}
}

func TestErrorTree_New_MultiErrorCalls(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName("f.yaml"))
	sentinel := errors.New("invalid")

	// Only a wrapper with several %w verbs reads its message to pick its
	// branches, so the walks that pick the branches of a multi-error of
	// its own type never call its Error method. A bound message reads it
	// to find the binding its first line comes from, and to learn whether
	// it holds the messages of its branches already.
	tcs := map[string]struct {
		run  func(err error)
		want int64
	}{
		"bind": {
			run:  func(err error) { _ = source.Bind(err) }, //nolint:errcheck // Only the calls count.
			want: 0,
		},
		"tree": {
			// Once to check for a join, and once for the text of the node.
			run:  func(err error) { niceyaml.NewErrorTree(err) },
			want: 2,
		},
		"message of a bound sentinel wrapper": {
			// Once as fmt.Errorf builds the wrapper, once to check
			// whether the message starts with that of the first branch,
			// and once to check whether it holds the branches the message
			// would otherwise list.
			run:  func(err error) { _ = source.Bind(fmt.Errorf("%w: %w", sentinel, err)).Error() },
			want: 3,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var calls atomic.Int64

			tc.run(countingViolationsError{calls: &calls, errs: []error{
				niceyaml.NewError("bad a", niceyaml.AtPath(paths.Root().Child("a"))),
				errors.New("plain"),
			}})

			assert.LessOrEqual(t, calls.Load(), tc.want)
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
				Text: "while checking:",
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

			assert.Equal(t, tc.want, textTree(niceyaml.NewErrorTree(tc.err)))
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
				niceyaml.NewError("bad x", niceyaml.AtPath(paths.Root().Child("x").Index(0))),
				niceyaml.NewError("no path"),
				badA(),
			))),
			want: niceyaml.ErrorTree{
				Text: "f.yaml: 3 problems",
				Children: []niceyaml.ErrorTree{
					{Text: "1:4: $.a: bad a"},
					{Text: "$.x[0]: bad x"},
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
		"bound join below a wrapper keeps the text of the wrapper as the root": {
			err: yamltest.Bind(t, source, fmt.Errorf("ctx: %w", errors.Join(badA(), badB()))),
			want: niceyaml.ErrorTree{
				Text: "f.yaml: ctx:",
				Children: []niceyaml.ErrorTree{
					{Text: "1:4: $.a: bad a"},
					{Text: "2:4: $.b: bad b"},
				},
			},
		},
		"bound join that leads with another source names the root": {
			err: yamltest.Bind(t, source, fmt.Errorf("ctx: %w", errors.Join(
				yamltest.Bind(t, other, niceyaml.NewError("bad c", niceyaml.AtPath(paths.Root().Child("c")))),
				badB(),
			))),
			want: niceyaml.ErrorTree{
				Text: "f.yaml: ctx:",
				Children: []niceyaml.ErrorTree{
					{Text: "g.yaml:1:4: $.c: bad c"},
					{Text: "2:4: $.b: bad b"},
				},
			},
		},
		"bound join that leads with a same-source binding of another source names the root": {
			err: yamltest.Bind(t, source, fmt.Errorf("ctx: %w", errors.Join(
				yamltest.Bind(t, source, errors.Join(
					yamltest.Bind(t, other, niceyaml.NewError("bad c", niceyaml.AtPath(paths.Root().Child("c")))),
				)),
				badB(),
			))),
			want: niceyaml.ErrorTree{
				Text: "f.yaml: ctx:",
				Children: []niceyaml.ErrorTree{
					{Text: "g.yaml:1:4: $.c: bad c"},
					{Text: "2:4: $.b: bad b"},
				},
			},
		},
		"Error that nests errors around a same-source binding of another source names the root": {
			err: yamltest.Bind(t, source, fmt.Errorf("ctx: %w", niceyaml.WrapError(
				yamltest.Bind(t, source, errors.Join(
					yamltest.Bind(t, other, niceyaml.NewError("bad c", niceyaml.AtPath(paths.Root().Child("c")))),
				)),
				niceyaml.WithErrors(badB()),
			))),
			want: niceyaml.ErrorTree{
				Text: "f.yaml: ctx:",
				Children: []niceyaml.ErrorTree{
					{Text: "2:4: $.b: bad b"},
					{Text: "g.yaml:1:4: $.c: bad c"},
				},
			},
		},
		"child that nests errors around a bound join gives its place to its named children": {
			err: yamltest.Bind(t, other, niceyaml.NewError("root",
				niceyaml.AtPath(paths.Root().Child("c")),
				niceyaml.WithErrors(yamltest.Bind(t, source, niceyaml.WrapError(
					yamltest.Bind(t, source, errors.Join(
						yamltest.Bind(t, other, niceyaml.NewError("bad c", niceyaml.AtPath(paths.Root().Child("c")))),
					)),
					niceyaml.WithErrors(badB()),
				))),
			)),
			want: niceyaml.ErrorTree{
				Text: "g.yaml:1:4: $.c: root",
				Children: []niceyaml.ErrorTree{
					{Text: "f.yaml:2:4: $.b: bad b"},
					{Text: "g.yaml:1:4: $.c: bad c"},
				},
			},
		},
		"branch that nests errors around a bound join gives its place to its named children": {
			err: yamltest.Bind(t, source, errors.Join(
				yamltest.Bind(t, source, niceyaml.WrapError(
					yamltest.Bind(t, source, errors.Join(
						yamltest.Bind(t, other, niceyaml.NewError("bad c", niceyaml.AtPath(paths.Root().Child("c")))),
					)),
					niceyaml.WithErrors(badB()),
				)),
				badA(),
			)),
			want: niceyaml.ErrorTree{
				Children: []niceyaml.ErrorTree{
					{Text: "f.yaml:1:4: $.a: bad a"},
					{Text: "f.yaml:2:4: $.b: bad b"},
					{Text: "g.yaml:1:4: $.c: bad c"},
				},
			},
		},
		"wrapper around a binding that lists errors keeps the line of the binding": {
			err: fmt.Errorf("load: %w", yamltest.Bind(t, source,
				niceyaml.NewError("2 problems", niceyaml.WithErrors(badA(), badB())))),
			want: niceyaml.ErrorTree{
				Text: "load: f.yaml: 2 problems",
				Children: []niceyaml.ErrorTree{
					{Text: "1:4: $.a: bad a"},
					{Text: "2:4: $.b: bad b"},
				},
			},
		},
		"wrapper around a join of bindings keeps its own text": {
			err: fmt.Errorf("load: %w", errors.Join(
				yamltest.Bind(t, source, badB()),
				yamltest.Bind(t, source, badA()),
			)),
			want: niceyaml.ErrorTree{
				Text: "load:",
				Children: []niceyaml.ErrorTree{
					{Text: "f.yaml:2:4: $.b: bad b"},
					{Text: "f.yaml:1:4: $.a: bad a"},
				},
			},
		},
		"wrapper around a bound join names the source on each branch": {
			err: fmt.Errorf("load: %w", yamltest.Bind(t, source, errors.Join(badB(), badA()))),
			want: niceyaml.ErrorTree{
				Text: "load:",
				Children: []niceyaml.ErrorTree{
					{Text: "f.yaml:1:4: $.a: bad a"},
					{Text: "f.yaml:2:4: $.b: bad b"},
				},
			},
		},
		"wrapper with no text of its own around a join is the join": {
			err: fmt.Errorf("%w", errors.Join(badA(), badB())),
			want: niceyaml.ErrorTree{
				Children: []niceyaml.ErrorTree{
					{Text: "$.a: bad a"},
					{Text: "$.b: bad b"},
				},
			},
		},
		"wrapper that puts text behind a binding keeps it on the line of the binding": {
			err: fmt.Errorf("%w (while loading)", yamltest.Bind(t, source,
				niceyaml.NewError("2 problems", niceyaml.WithErrors(badA(), badB())))),
			want: niceyaml.ErrorTree{
				Text: "f.yaml: 2 problems (while loading)",
				Children: []niceyaml.ErrorTree{
					{Text: "1:4: $.a: bad a"},
					{Text: "2:4: $.b: bad b"},
				},
			},
		},
		"wrapper that rewrites the message of a binding keeps its text whole": {
			err: upperError{err: yamltest.Bind(t, source,
				niceyaml.NewError("2 problems", niceyaml.WithErrors(badA(), badB())))},
			want: niceyaml.ErrorTree{
				Text: "F.YAML: 2 PROBLEMS\nF.YAML:1:4: $.A: BAD A\nF.YAML:2:4: $.B: BAD B",
				Children: []niceyaml.ErrorTree{
					{Text: "1:4: $.a: bad a"},
					{Text: "2:4: $.b: bad b"},
				},
			},
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

			got := textTree(niceyaml.NewErrorTree(tc.err))
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

func TestErrorTree_Bound(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("f.yaml"))

	badA := func() *niceyaml.Error {
		return niceyaml.NewError("bad a", niceyaml.AtPath(paths.Root().Child("a")))
	}
	badB := func() *niceyaml.Error {
		return niceyaml.NewError("bad b", niceyaml.AtPath(paths.Root().Child("b")))
	}

	bind := func(t *testing.T, err error) *niceyaml.SourceError {
		t.Helper()

		var bound *niceyaml.SourceError

		require.ErrorAs(t, yamltest.Bind(t, source, err), &bound)

		return bound
	}

	// Node is one node of a tree, as [niceyaml.ErrorTree.All] yields it.
	type node struct {
		err   error
		bound *niceyaml.SourceError
		text  string
	}

	// Tree is the error a case builds and the nodes its tree holds.
	type tree struct {
		err  error
		want []node
	}

	tcs := map[string]struct {
		build func(t *testing.T) tree
	}{
		"plain error": {
			build: func(*testing.T) tree {
				err := errors.New("boom")

				return tree{err: err, want: []node{{text: "boom", err: err}}}
			},
		},
		"unbound located Error": {
			build: func(*testing.T) tree {
				err := badA()

				return tree{err: err, want: []node{{text: "$.a: bad a", err: err}}}
			},
		},
		"binding": {
			build: func(t *testing.T) tree {
				t.Helper()

				bound := bind(t, badA())

				return tree{err: bound, want: []node{{text: "f.yaml:1:4: $.a: bad a", err: bound, bound: bound}}}
			},
		},
		"wrapper around a binding": {
			build: func(t *testing.T) tree {
				t.Helper()

				bound := bind(t, badA())
				err := fmt.Errorf("load: %w", bound)

				return tree{err: err, want: []node{{text: "load: f.yaml:1:4: $.a: bad a", err: err, bound: bound}}}
			},
		},
		"children of a binding": {
			build: func(t *testing.T) tree {
				t.Helper()

				bound := bind(t, niceyaml.NewError("2 problems", niceyaml.WithErrors(badB(), badA())))
				kids := bound.Errors()
				require.Len(t, kids, 2)

				// The tree sorts the children by position, so the second
				// child of the binding comes first.
				return tree{err: bound, want: []node{
					{text: "f.yaml: 2 problems", err: bound, bound: bound},
					{text: "1:4: $.a: bad a", err: kids[1], bound: kids[1]},
					{text: "2:4: $.b: bad b", err: kids[0], bound: kids[0]},
				}}
			},
		},
		"branches of a bound join": {
			build: func(t *testing.T) tree {
				t.Helper()

				bound := bind(t, errors.Join(badA(), badB()))
				kids := bound.Errors()
				require.Len(t, kids, 2)

				return tree{err: bound, want: []node{
					{text: "f.yaml:1:4: $.a: bad a", err: kids[0], bound: kids[0]},
					{text: "f.yaml:2:4: $.b: bad b", err: kids[1], bound: kids[1]},
				}}
			},
		},
		"Error that nests errors around a binding": {
			build: func(t *testing.T) tree {
				t.Helper()

				bound := bind(t, badA())
				reason := errors.New("see docs")
				err := niceyaml.WrapError(bound, niceyaml.WithErrors(reason))

				return tree{err: err, want: []node{
					{text: "f.yaml:1:4: $.a: bad a", err: err, bound: bound},
					{text: "see docs", err: reason},
				}}
			},
		},
		"located Error above a binding": {
			build: func(t *testing.T) tree {
				t.Helper()

				// No source resolved the location of the Error, so the node
				// has no binding to report it through.
				err := niceyaml.WrapError(bind(t, badA()), niceyaml.AtPath(paths.Root().Child("b")))

				return tree{err: err, want: []node{{text: "$.b: f.yaml:1:4: $.a: bad a", err: err}}}
			},
		},
		"unbound Error that nests a binding": {
			build: func(t *testing.T) tree {
				t.Helper()

				bound := bind(t, badA())
				read := errors.New("read g.yaml: no such file")
				err := niceyaml.NewError("2 files", niceyaml.WithErrors(read, bound))

				return tree{err: err, want: []node{
					{text: "2 files", err: err},
					{text: "read g.yaml: no such file", err: read},
					{text: "f.yaml:1:4: $.a: bad a", err: bound, bound: bound},
				}}
			},
		},
		"join of a plain error and a binding": {
			build: func(t *testing.T) tree {
				t.Helper()

				bound := bind(t, badA())
				read := errors.New("read g.yaml: no such file")

				return tree{err: errors.Join(read, bound), want: []node{
					{text: "read g.yaml: no such file", err: read},
					{text: "f.yaml:1:4: $.a: bad a", err: bound, bound: bound},
				}}
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tree := tc.build(t)

			got := slices.Collect(niceyaml.NewErrorTree(tree.err).All())
			require.Len(t, got, len(tree.want))

			for i, n := range tree.want {
				assert.Equal(t, n.text, got[i].Text)
				assert.Same(t, n.err, got[i].Err, n.text)
				assert.Same(t, n.bound, got[i].Bound, n.text)
			}
		})
	}
}

func TestErrorTree_Bound_JoinRoot(t *testing.T) {
	t.Parallel()

	// The root of a join has no text, so it stands for no error of its own.
	got := niceyaml.NewErrorTree(errors.Join(errors.New("first"), errors.New("second")))

	assert.Empty(t, got.Text)
	require.NoError(t, got.Err)
	assert.Nil(t, got.Bound)
	assert.Len(t, got.Children, 2)
}

func TestErrorTree_All(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		tree niceyaml.ErrorTree
		want []string
	}{
		"zero tree": {
			tree: niceyaml.ErrorTree{},
			want: nil,
		},
		"single node": {
			tree: niceyaml.ErrorTree{Text: "boom"},
			want: []string{"boom"},
		},
		"each node comes before the nodes below it": {
			tree: niceyaml.ErrorTree{
				Text: "root",
				Children: []niceyaml.ErrorTree{
					{Text: "a", Children: []niceyaml.ErrorTree{{Text: "a1"}, {Text: "a2"}}},
					{Text: "b", Children: []niceyaml.ErrorTree{{Text: "b1"}}},
				},
			},
			want: []string{"root", "a", "a1", "a2", "b", "b1"},
		},
		"root with no text gives its place to its children": {
			tree: niceyaml.ErrorTree{Children: []niceyaml.ErrorTree{{Text: "a"}, {Text: "b"}}},
			want: []string{"a", "b"},
		},
		"node with no text gives its place to its children": {
			tree: niceyaml.ErrorTree{
				Text: "root",
				Children: []niceyaml.ErrorTree{
					{Children: []niceyaml.ErrorTree{{Text: "x"}, {Text: "y"}}},
					{Text: "z"},
				},
			},
			want: []string{"root", "x", "y", "z"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var got []string

			for node := range tc.tree.All() {
				got = append(got, node.Text)
			}

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestErrorTree_All_PrintedOrder(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("f.yaml"))

	err := errors.Join(
		yamltest.Bind(t, source, niceyaml.NewError("2 problems", niceyaml.WithErrors(
			niceyaml.NewError("bad b", niceyaml.AtPath(paths.Root().Child("b"))),
			niceyaml.NewError("bad a", niceyaml.AtPath(paths.Root().Child("a"))),
		))),
		errors.New("read g.yaml: no such file"),
	)

	var rows []string

	for node := range niceyaml.NewErrorTree(err).All() {
		rows = append(rows, node.Text)
	}

	// The tree [niceyaml.FormatError] prints holds one row per node, in the
	// same order, each behind its connector.
	tree, _, _ := strings.Cut(niceyaml.FormatError(err, 0), "\n\n")
	printed := strings.Split(tree, "\n")
	require.Len(t, printed, len(rows))

	for i, row := range rows {
		assert.True(t, strings.HasSuffix(printed[i], row), "%q does not end with %q", printed[i], row)
	}
}

func TestErrorTree_All_Stops(t *testing.T) {
	t.Parallel()

	tree := niceyaml.ErrorTree{
		Text: "root",
		Children: []niceyaml.ErrorTree{
			{Text: "a", Children: []niceyaml.ErrorTree{{Text: "a1"}}},
			{Text: "b"},
		},
	}

	var got []string

	for node := range tree.All() {
		got = append(got, node.Text)
		if node.Text == "a1" {
			break
		}
	}

	assert.Equal(t, []string{"root", "a", "a1"}, got)
}

// problemRows returns one row per node [niceyaml.ErrorTree.Problems]
// yields: the message of its binding, or its text for a node bound to no
// source. The message of a binding holds no position, so a row reads the
// same wherever the location of the error resolves.
func problemRows(t niceyaml.ErrorTree) []string {
	var rows []string

	for problem := range t.Problems() {
		if problem.Bound != nil {
			rows = append(rows, problem.Bound.Message())

			continue
		}

		rows = append(rows, problem.Text)
	}

	return rows
}

func TestErrorTree_Problems(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("f.yaml"))

	badA := func() *niceyaml.Error {
		return niceyaml.NewError("bad a", niceyaml.AtPath(paths.Root().Child("a")))
	}
	badB := func() *niceyaml.Error {
		return niceyaml.NewError("bad b", niceyaml.AtPath(paths.Root().Child("b")))
	}

	errNoMatch := errors.New("no matching schema")
	errRead := errors.New("read g.yaml: no such file")

	tcs := map[string]struct {
		err  error
		want []string
		// The anyOrder flag marks a case whose rows hold in any order, since
		// the tree sorts the children of a binding by where each resolves.
		anyOrder bool
	}{
		"nil": {
			err:  nil,
			want: nil,
		},
		"bound located error": {
			err:  yamltest.Bind(t, source, badA()),
			want: []string{"bad a"},
		},
		"bound summary above located errors": {
			// An error with no location above located ones reads as a
			// summary whatever its message says.
			err: yamltest.Bind(
				t,
				source,
				niceyaml.NewError("2 schema violations", niceyaml.WithErrors(badA(), badB())),
			),
			want: []string{"bad a", "bad b"},
		},
		"bound join of located errors": {
			err:  yamltest.Bind(t, source, errors.Join(badA(), badB())),
			want: []string{"bad a", "bad b"},
		},
		"summary of summaries": {
			err: yamltest.Bind(t, source, niceyaml.NewError("2 checks", niceyaml.WithErrors(
				niceyaml.NewError("2 schema violations", niceyaml.WithErrors(badA(), badB())),
				niceyaml.NewError("too old"),
			))),
			want: []string{"bad a", "bad b", "too old"},
		},
		"bound error without a location": {
			err:  yamltest.Bind(t, source, errNoMatch),
			want: []string{"no matching schema"},
		},
		"bound error with one reason": {
			err: yamltest.Bind(t, source, niceyaml.WrapError(errNoMatch, niceyaml.WithErrors(
				errors.New("no schema directive"),
			))),
			want: []string{"no matching schema"},
		},
		"bound error with two reasons": {
			err: yamltest.Bind(t, source, niceyaml.WrapError(errNoMatch, niceyaml.WithErrors(
				errors.New("no schema directive"),
				errors.New("no catalog entry matches"),
			))),
			want: []string{"no matching schema"},
		},
		"located error keeps its reasons": {
			err: yamltest.Bind(t, source, niceyaml.NewError("bad a",
				niceyaml.AtPath(paths.Root().Child("a")),
				niceyaml.WithErrors(errors.New("tag missing"), errors.New("registry unknown")),
			)),
			want: []string{"bad a"},
		},
		"located error above a located error": {
			err: yamltest.Bind(t, source, niceyaml.NewError("bad a",
				niceyaml.AtPath(paths.Root().Child("a")),
				niceyaml.WithErrors(badB()),
			)),
			want: []string{"bad a", "bad b"},
		},
		"located error above forms": {
			// The forms carry no location, so neither yields. The located
			// error under the first form does, and every node without a
			// location stays a reason of the error above.
			err: yamltest.Bind(t, source, niceyaml.NewError("matches no form",
				niceyaml.AtPath(paths.Root().Child("a")),
				niceyaml.WithErrors(
					niceyaml.NewError("form 1", niceyaml.WithErrors(badB(), errors.New("needs a list"))),
					niceyaml.NewError("form 2", niceyaml.WithErrors(errors.New("needs a string"))),
				),
			)),
			want: []string{"matches no form", "bad b"},
		},
		"location the source does not hold": {
			// A location that did not resolve is still the location of a
			// problem.
			err: yamltest.Bind(t, source, niceyaml.NewError("3 problems", niceyaml.WithErrors(
				badB(),
				niceyaml.NewError("off the end", niceyaml.AtPosition(position.New(99, 0))),
				errors.New("too old"),
			))),
			want: []string{"bad b", "off the end", "too old"},
		},
		"path the document does not hold": {
			err: yamltest.Bind(t, source, niceyaml.NewError("2 problems", niceyaml.WithErrors(
				niceyaml.NewError("missing", niceyaml.AtPath(paths.Root().Child("c"))),
				badA(),
			))),
			want:     []string{"bad a", "missing"},
			anyOrder: true,
		},
		"wrapper around a bound summary": {
			err: fmt.Errorf("load: %w", yamltest.Bind(t, source,
				niceyaml.NewError("2 schema violations", niceyaml.WithErrors(badA(), badB())),
			)),
			want: []string{"bad a", "bad b"},
		},
		"wrapper around a bound located error": {
			err:  fmt.Errorf("load: %w", yamltest.Bind(t, source, badA())),
			want: []string{"bad a"},
		},
		"Error that nests a reason around a binding": {
			err: niceyaml.WrapError(
				yamltest.Bind(t, source, badA()),
				niceyaml.WithErrors(errors.New("see docs")),
			),
			want: []string{"bad a"},
		},
		"unbound error": {
			err:  errRead,
			want: []string{"read g.yaml: no such file"},
		},
		"unbound join": {
			err:  errors.Join(errRead, errors.New("read h.yaml: no such file")),
			want: []string{"read g.yaml: no such file", "read h.yaml: no such file"},
		},
		"unbound error with reasons": {
			err: niceyaml.WrapError(errNoMatch, niceyaml.WithErrors(
				errors.New("no schema directive"),
				errors.New("no catalog entry matches"),
			)),
			want: []string{"no matching schema"},
		},
		"unbound located error": {
			err:  badA(),
			want: []string{"$.a: bad a"},
		},
		"unbound summary above located errors": {
			// A validator that checked a Go value binds its errors to no
			// source, and each still names its path.
			err:  niceyaml.NewError("2 schema violations", niceyaml.WithErrors(badA(), badB())),
			want: []string{"$.a: bad a", "$.b: bad b"},
		},
		"unbound located error keeps its reasons": {
			err: niceyaml.NewError("bad a",
				niceyaml.AtPath(paths.Root().Child("a")),
				niceyaml.WithErrors(errors.New("tag missing")),
			),
			want: []string{"$.a: bad a"},
		},
		"unbound wrapper around a located error": {
			err:  fmt.Errorf("check: %w", badA()),
			want: []string{"check: $.a: bad a"},
		},
		"unbound error above an unbound error and a binding": {
			err: niceyaml.NewError("2 files", niceyaml.WithErrors(
				errRead,
				yamltest.Bind(t, source, badA()),
			)),
			want: []string{"read g.yaml: no such file", "bad a"},
		},
		"rebased error without a location": {
			err:  yamltest.Bind(t, source, niceyaml.Rebase(errors.New("closes early"), paths.Root().Child("a"))),
			want: []string{"closes early"},
		},
		"rebased error keeps its reasons": {
			// The base locates the error and each reason at the same value,
			// and none of them names a location of its own.
			err: yamltest.Bind(t, source, niceyaml.Rebase(
				niceyaml.NewError("hours invalid", niceyaml.WithErrors(
					errors.New("opens too early"),
					errors.New("never closes"),
				)),
				paths.Root().Child("a"),
			)),
			want: []string{"hours invalid"},
		},
		"rebased summary above located errors": {
			err: yamltest.Bind(t, source, niceyaml.Rebase(
				niceyaml.NewError("2 problems", niceyaml.WithErrors(badA(), badB())),
				paths.Root(),
			)),
			want: []string{"bad a", "bad b"},
		},
		"unbound rebased error keeps its reasons": {
			err: niceyaml.Rebase(
				niceyaml.NewError("hours invalid", niceyaml.WithErrors(errors.New("never closes"))),
				paths.Root().Child("x"),
			),
			want: []string{"$.x: hours invalid"},
		},
		"unbound rebased summary above located errors": {
			err: niceyaml.Rebase(
				niceyaml.NewError("2 problems", niceyaml.WithErrors(badA(), badB())),
				paths.Root().Child("x"),
			),
			want: []string{"$.x.a: bad a", "$.x.b: bad b"},
		},
		"join of a run over several files": {
			err: errors.Join(
				yamltest.Bind(t, source, niceyaml.NewError("2 schema violations", niceyaml.WithErrors(badA(), badB()))),
				yamltest.Bind(t, source, badB()),
				fmt.Errorf("read file: %w", errRead),
				yamltest.Bind(t, source, niceyaml.WrapError(errNoMatch, niceyaml.WithErrors(
					errors.New("no schema directive"),
				))),
				yamltest.Bind(t, source, errNoMatch),
			),
			want: []string{
				"bad a",
				"bad b",
				"bad b",
				"read file: read g.yaml: no such file",
				"no matching schema",
				"no matching schema",
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := problemRows(niceyaml.NewErrorTree(tc.err))

			if tc.anyOrder {
				assert.ElementsMatch(t, tc.want, got)

				return
			}

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestErrorTree_Problems_Reasons(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName("f.yaml"))

	err := yamltest.Bind(t, source, niceyaml.WrapError(
		errors.New("no matching schema"),
		niceyaml.WithErrors(errors.New("no schema directive"), errors.New("no catalog entry matches")),
	))

	got := slices.Collect(niceyaml.NewErrorTree(err).Problems())
	require.Len(t, got, 1)

	// The reasons stay below the node that yields.
	assert.Equal(t, niceyaml.ErrorTree{
		Text: "f.yaml: no matching schema",
		Children: []niceyaml.ErrorTree{
			{Text: "no schema directive"},
			{Text: "no catalog entry matches"},
		},
	}, textTree(got[0]))
	require.ErrorIs(t, got[0].Err, err)
}

// reasonedHours is a [niceyaml.SelfValidator] whose error names no
// location and gives two reasons.
type reasonedHours struct {
	Open string `yaml:"open"`
}

// Validate returns the error.
func (reasonedHours) Validate() error {
	return niceyaml.NewError("hours invalid", niceyaml.WithErrors(
		errors.New("opens too early"),
		errors.New("never closes"),
	))
}

func TestErrorTree_Problems_SelfValidator(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("hours:\n  open: a\n", niceyaml.WithName("f.yaml"))

	doc, err := source.Document()
	require.NoError(t, err)

	var cfg struct {
		Hours reasonedHours `yaml:"hours"`
	}

	// The decode rebases the error and its reasons under $.hours, which
	// locates each of them at the value.
	got := slices.Collect(niceyaml.NewErrorTree(doc.DecodeInto(t.Context(), &cfg)).Problems())
	require.Len(t, got, 1)
	require.NotNil(t, got[0].Bound)

	assert.Equal(t, "hours invalid", got[0].Bound.Message())
	assert.Len(t, got[0].Children, 2)

	pos, ok := got[0].Bound.Position()
	require.True(t, ok)
	assert.Equal(t, position.New(1, 2), pos)
}

func TestErrorTree_Problems_Scope(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("hours:\n  open: a\n", niceyaml.WithName("f.yaml"))

	doc, err := source.Document()
	require.NoError(t, err)

	hours := yamltest.At(t, doc, paths.Root().Child("hours"))

	// The binding joins the scope in front of each path, and the summary
	// names no path, so it stays a summary.
	bound := hours.Bind(niceyaml.NewError("2 problems", niceyaml.WithErrors(
		niceyaml.NewError("bad open", niceyaml.AtPath(paths.Root().Child("open"))),
		niceyaml.NewError("no close", niceyaml.AtPath(paths.Root().Child("close"))),
	)))

	// Each row reads as the position, the path, and the message.
	var got []string

	for problem := range niceyaml.NewErrorTree(bound).Problems() {
		require.NotNil(t, problem.Bound)

		path, ok := problem.Bound.Path()
		require.True(t, ok)

		pos, ok := problem.Bound.Position()
		require.True(t, ok)

		got = append(got, fmt.Sprintf("%d:%d %s %s", pos.Line, pos.Col, path, problem.Bound.Message()))
	}

	// The key the mapping leaves out binds at the key of the mapping.
	assert.ElementsMatch(t, []string{
		"1:8 $.hours.open bad open",
		"0:0 $.hours.close no close",
	}, got)
}

func TestErrorTree_Problems_HandBuilt(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		tree niceyaml.ErrorTree
		want []string
	}{
		"zero tree": {
			tree: niceyaml.ErrorTree{},
			want: nil,
		},
		"node above its reasons": {
			tree: niceyaml.ErrorTree{
				Text:     "no matching schema",
				Children: []niceyaml.ErrorTree{{Text: "no schema directive"}},
			},
			want: []string{"no matching schema"},
		},
		"root with no text gives its place to its children": {
			tree: niceyaml.ErrorTree{Children: []niceyaml.ErrorTree{
				{Text: "first", Children: []niceyaml.ErrorTree{{Text: "reason"}}},
				{Text: "second"},
			}},
			want: []string{"first", "second"},
		},
		"located error under a node with no location": {
			tree: niceyaml.ErrorTree{
				Text: "2 problems",
				Children: []niceyaml.ErrorTree{
					{
						Text: "$.a: bad a",
						Err:  niceyaml.NewError("bad a", niceyaml.AtPath(paths.Root().Child("a"))),
					},
					{Text: "too old"},
				},
			},
			want: []string{"$.a: bad a", "too old"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, problemRows(tc.tree))
		})
	}
}

func TestErrorTree_Problems_Stops(t *testing.T) {
	t.Parallel()

	tree := niceyaml.NewErrorTree(errors.Join(
		errors.New("first"),
		errors.New("second"),
		errors.New("third"),
	))

	var got []string

	for problem := range tree.Problems() {
		got = append(got, problem.Text)
		if problem.Text == "second" {
			break
		}
	}

	assert.Equal(t, []string{"first", "second"}, got)
}
