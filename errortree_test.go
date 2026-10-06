package niceyaml_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"

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
			build:  func(err error) error { return niceyaml.Rebase(err, paths.Current()) },
			prefix: "@: ",
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

				var err error = niceyaml.NewError("bad", niceyaml.AtPath(paths.Current().Child("a")))

				for i := 1; i < n; i++ {
					err = fmt.Errorf("%w: %w", sentinel, err)
				}

				return err
			},
			want: niceyaml.ErrorTree{Text: "@.a: " + strings.Repeat("invalid: ", n-1) + "bad"},
		},
	}

	ops := map[string]func(error){
		"tree":   func(err error) { niceyaml.NewErrorTree(err) },
		"bind":   func(err error) { _ = source.Bind(err).Error() },
		"rebase": func(err error) { _ = niceyaml.Rebase(err, paths.Current()).Error() },
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
			// The text of each level holds the message of the located error
			// it wraps and not its position, so the level lists that error.
			// The binding below holds its own lines in its text already.
			build: func(calls *atomic.Int64) error {
				err := source.Bind(countingError{calls: calls, msg: "e0"})

				for i := 1; i < n; i++ {
					located := niceyaml.NewError(fmt.Sprintf("e%d", i), niceyaml.AtPath(paths.Current().Child("a")))
					err = source.Bind(fmt.Errorf("%w; %w", err, located))
				}

				return err
			},
			want: stringtest.JoinLF(
				"f.yaml: e0; e1",
				"f.yaml:1:4: $.a: e1; e2",
				"f.yaml:1:4: $.a: e2; e3",
				"f.yaml:1:4: $.a: e3; e4",
				"f.yaml:1:4: $.a: e4; e5",
				"f.yaml:1:4: $.a: e5; e6",
				"f.yaml:1:4: $.a: e6; e7",
				"f.yaml:1:4: $.a: e7; e8",
				"f.yaml:1:4: $.a: e8; e9",
				"f.yaml:1:4: $.a: e9; e10",
				"f.yaml:1:4: $.a: e10; e11",
				"f.yaml:1:4: $.a: e11; e12",
				"f.yaml:1:4: $.a: e12; e13",
				"f.yaml:1:4: $.a: e13; e14",
				"f.yaml:1:4: $.a: e14; e15",
				"f.yaml:1:4: $.a: e15; e16",
				"f.yaml:1:4: $.a: e16; e17",
				"f.yaml:1:4: $.a: e17; e18",
				"f.yaml:1:4: $.a: e18; e19",
				"f.yaml:1:4: $.a: e19",
			),
		},
	}

	ops := map[string]func(error){
		"message": func(err error) { _ = err.Error() },
		"tree":    func(err error) { niceyaml.NewErrorTree(err) },
		"rebind": func(err error) {
			located := niceyaml.NewError("last", niceyaml.AtPath(paths.Current().Child("b")))
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
	// its own type never call its Error method. Binding reads it once to
	// learn whether it is a join, whose branches each take the scope of
	// the binding. A bound message reads it to find the binding its first
	// line comes from, and to learn whether it holds the messages of its
	// branches already.
	tcs := map[string]struct {
		run  func(err error)
		want int64
	}{
		"bind": {
			// Once to check for a join.
			run:  func(err error) { _ = source.Bind(err) }, //nolint:errcheck // Only the calls count.
			want: 1,
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
				niceyaml.NewError("bad a", niceyaml.AtPath(paths.Current().Child("a"))),
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
			err: fmt.Errorf("%w: %w", errA, niceyaml.NewError("parent", niceyaml.WithDetails(errB))),
			want: niceyaml.ErrorTree{
				Text:     "first: parent",
				Children: []niceyaml.ErrorTree{{Text: "second"}},
			},
		},
		"wrapper with two verbs keeps its own text": {
			err: fmt.Errorf("parse path: %w; %w",
				niceyaml.NewError("x", niceyaml.WithDetails(errA)),
				niceyaml.NewError("y", niceyaml.WithDetails(errB)),
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
			err:  niceyaml.NewError("outer", niceyaml.WithDetails(niceyaml.NewError(""))),
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
		return niceyaml.NewError("bad a", niceyaml.AtPath(paths.Current().Child("a")))
	}
	badB := func() *niceyaml.Error {
		return niceyaml.NewError("bad b", niceyaml.AtPath(paths.Current().Child("b")))
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
			want: niceyaml.ErrorTree{Text: "@.a: bad a"},
		},
		"nested errors are children": {
			err: niceyaml.NewSummary("2 problems", badA(), niceyaml.NewError("no path")),
			want: niceyaml.ErrorTree{
				Text: "2 problems",
				Children: []niceyaml.ErrorTree{
					{Text: "@.a: bad a"},
					{Text: "no path"},
				},
			},
		},
		"bound children carry their position without the name": {
			err: yamltest.Bind(t, source, niceyaml.NewSummary("2 problems", badA(), badB())),
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
			err: yamltest.Bind(t, wide, niceyaml.NewSummary("2 problems",
				badB(),
				niceyaml.NewError("mid", niceyaml.AtPosition(position.New(0, 8))),
			)),
			want: niceyaml.ErrorTree{
				Text: "f.yaml: 2 problems",
				Children: []niceyaml.ErrorTree{
					{Text: "1:9: mid"},
					{Text: "2:4: $.b: bad b"},
				},
			},
		},
		"children sort by position": {
			err: yamltest.Bind(t, source, niceyaml.NewSummary("2 problems", badB(), badA())),
			want: niceyaml.ErrorTree{
				Text: "f.yaml: 2 problems",
				Children: []niceyaml.ErrorTree{
					{Text: "1:4: $.a: bad a"},
					{Text: "2:4: $.b: bad b"},
				},
			},
		},
		"children without a position follow the positioned ones": {
			err: yamltest.Bind(t, source, niceyaml.NewSummary("3 problems",
				niceyaml.NewError("bad x", niceyaml.AtPath(paths.Current().Child("x").Index(0))),
				niceyaml.NewError("no path"),
				badA(),
			)),
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
			err: yamltest.Bind(t, source, niceyaml.NewError("outer", niceyaml.WithDetails(
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
			err: yamltest.Bind(t, source, niceyaml.NewSummary("summary",
				niceyaml.WrapError(yamltest.Bind(t, source, badA())),
				niceyaml.WrapError(yamltest.Bind(t, source, badB()), niceyaml.WithDetails(niceyaml.NewError("x"))),
				niceyaml.WrapError(yamltest.Bind(t, source, errors.New("plain"))),
				niceyaml.WrapError(yamltest.Bind(t, source,
					niceyaml.WrapError(yamltest.Bind(t, source, badA()), niceyaml.WithDetails(niceyaml.NewError("y"))),
				)),
			)),
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
				niceyaml.AtPath(paths.Current().Child("a")),
				niceyaml.WithDetails(badB()),
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
				niceyaml.NewSummary("2 problems", badA(), badB()),
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
				niceyaml.NewSummary("2 problems", badA(), badB()),
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
			err: yamltest.Bind(t, source, niceyaml.NewError("outer", niceyaml.WithDetails(
				niceyaml.NewError("mid",
					niceyaml.AtPath(paths.Current().Child("a")),
					niceyaml.WithDetails(badB()),
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
			err: yamltest.Bind(t, source, niceyaml.NewError("outer", niceyaml.WithDetails(
				niceyaml.WrapError(yamltest.Bind(t, other,
					niceyaml.NewError("inner", niceyaml.WithDetails(
						niceyaml.NewError("bad c", niceyaml.AtPath(paths.Current().Child("c"))),
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
				niceyaml.NewError("inner", niceyaml.WithDetails(
					niceyaml.NewError("bad c", niceyaml.AtPath(paths.Current().Child("c"))),
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
				yamltest.Bind(t, other, niceyaml.NewError("inner", niceyaml.WithDetails(
					niceyaml.NewError("bad c", niceyaml.AtPath(paths.Current().Child("c"))),
				))),
				niceyaml.AtPath(paths.Current().Child("a")),
				niceyaml.WithDetails(badB()),
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
				yamltest.Bind(t, other, niceyaml.NewError("bad c", niceyaml.AtPath(paths.Current().Child("c")))),
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
				yamltest.Bind(t, source, niceyaml.NewSummary("2 problems", badA(), badB())),
				yamltest.Bind(t, other, niceyaml.NewError("bad c", niceyaml.AtPath(paths.Current().Child("c")))),
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
			err: niceyaml.NewSummary("summary",
				yamltest.Bind(t, source, badB()),
				yamltest.Bind(t, source, badA()),
			),
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
				yamltest.Bind(t, other, niceyaml.NewError("bad c", niceyaml.AtPath(paths.Current().Child("c")))),
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
			err: yamltest.Bind(t, source, niceyaml.NewError("outer", niceyaml.WithDetails(
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
			err: yamltest.Bind(t, source, niceyaml.NewSummary("outer",
				badA(),
				yamltest.Bind(t, other, errors.Join(
					niceyaml.NewError("bad c", niceyaml.AtPath(paths.Current().Child("c"))),
					niceyaml.NewError("start", niceyaml.AtPosition(position.New(0, 0))),
				)),
			)),
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
					{Text: "@.a: bad a"},
					{Text: "@.b: bad b"},
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
			err: yamltest.Bind(t, source, niceyaml.NewError("outer", niceyaml.WithDetails(
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
			err: yamltest.Bind(t, source, niceyaml.NewError("outer", niceyaml.WithDetails(
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
			err: yamltest.Bind(t, source, niceyaml.NewError("outer", niceyaml.WithDetails(
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
				yamltest.Bind(t, other, niceyaml.NewError("bad c", niceyaml.AtPath(paths.Current().Child("c")))),
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
					yamltest.Bind(t, other, niceyaml.NewError("bad c", niceyaml.AtPath(paths.Current().Child("c")))),
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
		"Error with details around a same-source binding of another source names the root": {
			err: yamltest.Bind(t, source, fmt.Errorf("ctx: %w", niceyaml.WrapError(
				yamltest.Bind(t, source, errors.Join(
					yamltest.Bind(t, other, niceyaml.NewError("bad c", niceyaml.AtPath(paths.Current().Child("c")))),
				)),
				niceyaml.WithDetails(badB()),
			))),
			want: niceyaml.ErrorTree{
				Text: "f.yaml: ctx:",
				Children: []niceyaml.ErrorTree{
					{Text: "2:4: $.b: bad b"},
					{Text: "g.yaml:1:4: $.c: bad c"},
				},
			},
		},
		"child with details around a bound join gives its place to its named children": {
			err: yamltest.Bind(t, other, niceyaml.NewError("root",
				niceyaml.AtPath(paths.Current().Child("c")),
				niceyaml.WithDetails(yamltest.Bind(t, source, niceyaml.WrapError(
					yamltest.Bind(t, source, errors.Join(
						yamltest.Bind(
							t,
							other,
							niceyaml.NewError("bad c", niceyaml.AtPath(paths.Current().Child("c"))),
						),
					)),
					niceyaml.WithDetails(badB()),
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
		"branch with details around a bound join gives its place to its named children": {
			err: yamltest.Bind(t, source, errors.Join(
				yamltest.Bind(t, source, niceyaml.WrapError(
					yamltest.Bind(t, source, errors.Join(
						yamltest.Bind(
							t,
							other,
							niceyaml.NewError("bad c", niceyaml.AtPath(paths.Current().Child("c"))),
						),
					)),
					niceyaml.WithDetails(badB()),
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
				niceyaml.NewSummary("2 problems", badA(), badB()))),
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
					{Text: "@.a: bad a"},
					{Text: "@.b: bad b"},
				},
			},
		},
		"wrapper that puts text behind a binding keeps it on the line of the binding": {
			err: fmt.Errorf("%w (while loading)", yamltest.Bind(t, source,
				niceyaml.NewSummary("2 problems", badA(), badB()))),
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
				niceyaml.NewSummary("2 problems", badA(), badB()))},
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
			err: niceyaml.WrapError(nil, niceyaml.WithDetails(
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
		"nil details are skipped": {
			err:  niceyaml.NewError("main", niceyaml.WithDetails(nil, niceyaml.NewError("one"), nil)),
			want: niceyaml.ErrorTree{Text: "main", Children: []niceyaml.ErrorTree{{Text: "one"}}},
		},
		"nil errors a summary heads are skipped": {
			err: niceyaml.NewSummary("main", nil, niceyaml.NewError("one"), nil, niceyaml.NewError("two")),
			want: niceyaml.ErrorTree{
				Text:     "main",
				Children: []niceyaml.ErrorTree{{Text: "one"}, {Text: "two"}},
			},
		},
		"rewritten message keeps its children": {
			err:  yamltest.RewriteError{Err: niceyaml.NewError("main", niceyaml.WithDetails(badA()))},
			want: niceyaml.ErrorTree{Text: "rewritten", Children: []niceyaml.ErrorTree{{Text: "@.a: bad a"}}},
		},
		"rewritten bound message keeps its children with positions": {
			err: yamltest.Bind(
				t,
				source,
				yamltest.RewriteError{Err: niceyaml.NewError("main", niceyaml.WithDetails(badA()))},
			),
			want: niceyaml.ErrorTree{
				Text:     "f.yaml: rewritten",
				Children: []niceyaml.ErrorTree{{Text: "1:4: $.a: bad a"}},
			},
		},
		"multi-line nested message stays whole": {
			err: yamltest.Bind(t, source, niceyaml.NewError("outer", niceyaml.WithDetails(
				niceyaml.NewError("bad a\n  see docs", niceyaml.AtPath(paths.Current().Child("a"))),
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
		return niceyaml.NewError("bad a", niceyaml.AtPath(paths.Current().Child("a")))
	}
	badB := func() *niceyaml.Error {
		return niceyaml.NewError("bad b", niceyaml.AtPath(paths.Current().Child("b")))
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

				return tree{err: err, want: []node{{text: "@.a: bad a", err: err}}}
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

				bound := bind(t, niceyaml.NewSummary("2 problems", badB(), badA()))
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
		"Error with details around a binding": {
			build: func(t *testing.T) tree {
				t.Helper()

				bound := bind(t, badA())
				reason := errors.New("see docs")
				err := niceyaml.WrapError(bound, niceyaml.WithDetails(reason))

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
				err := niceyaml.WrapError(bind(t, badA()), niceyaml.AtPath(paths.Current().Child("b")))

				return tree{err: err, want: []node{{text: "@.b: f.yaml:1:4: $.a: bad a", err: err}}}
			},
		},
		"unbound summary that heads a binding": {
			build: func(t *testing.T) tree {
				t.Helper()

				bound := bind(t, badA())
				read := errors.New("read g.yaml: no such file")
				err := niceyaml.NewSummary("2 files", read, bound)

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
		yamltest.Bind(t, source, niceyaml.NewSummary("2 problems",
			niceyaml.NewError("bad b", niceyaml.AtPath(paths.Current().Child("b"))),
			niceyaml.NewError("bad a", niceyaml.AtPath(paths.Current().Child("a"))),
		)),
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
		return niceyaml.NewError("bad a", niceyaml.AtPath(paths.Current().Child("a")))
	}
	badB := func() *niceyaml.Error {
		return niceyaml.NewError("bad b", niceyaml.AtPath(paths.Current().Child("b")))
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
			// A summary is a heading, so the errors it heads are the rows.
			err: yamltest.Bind(
				t,
				source,
				niceyaml.NewSummary("2 schema violations", badA(), badB()),
			),
			want: []string{"bad a", "bad b"},
		},
		"bound join of located errors": {
			err:  yamltest.Bind(t, source, errors.Join(badA(), badB())),
			want: []string{"bad a", "bad b"},
		},
		"summary of summaries": {
			err: yamltest.Bind(t, source, niceyaml.NewSummary("2 checks",
				niceyaml.NewSummary("2 schema violations", badA(), badB()),
				niceyaml.NewError("too old"),
			)),
			want: []string{"bad a", "bad b", "too old"},
		},
		"bound error without a location": {
			err:  yamltest.Bind(t, source, errNoMatch),
			want: []string{"no matching schema"},
		},
		"bound error with one reason": {
			err: yamltest.Bind(t, source, niceyaml.WrapError(errNoMatch, niceyaml.WithDetails(
				errors.New("no schema directive"),
			))),
			want: []string{"no matching schema"},
		},
		"bound error with two reasons": {
			err: yamltest.Bind(t, source, niceyaml.WrapError(errNoMatch, niceyaml.WithDetails(
				errors.New("no schema directive"),
				errors.New("no catalog entry matches"),
			))),
			want: []string{"no matching schema"},
		},
		"located error keeps its reasons": {
			err: yamltest.Bind(t, source, niceyaml.NewError("bad a",
				niceyaml.AtPath(paths.Current().Child("a")),
				niceyaml.WithDetails(errors.New("tag missing"), errors.New("registry unknown")),
			)),
			want: []string{"bad a"},
		},
		"located error above a located detail": {
			// A detail is never a row, whatever location it carries.
			err: yamltest.Bind(t, source, niceyaml.NewError("bad a",
				niceyaml.AtPath(paths.Current().Child("a")),
				niceyaml.WithDetails(badB()),
			)),
			want: []string{"bad a"},
		},
		"located error above forms": {
			// The forms and the located error under the first explain the
			// error, so it is one row.
			err: yamltest.Bind(t, source, niceyaml.NewError("matches no form",
				niceyaml.AtPath(paths.Current().Child("a")),
				niceyaml.WithDetails(
					niceyaml.NewError("form 1", niceyaml.WithDetails(badB(), errors.New("needs a list"))),
					niceyaml.NewError("form 2", niceyaml.WithDetails(errors.New("needs a string"))),
				),
			)),
			want: []string{"matches no form"},
		},
		"error with no location above located details": {
			// The error names the conflict, and the details name the
			// values in conflict, so the report keeps its message.
			err: yamltest.Bind(t, source, niceyaml.NewError("ports conflict",
				niceyaml.WithDetails(badA(), badB()),
			)),
			want: []string{"ports conflict"},
		},
		"located error above a located error it relates to": {
			err: yamltest.Bind(t, source, niceyaml.NewError(
				"port conflicts",
				niceyaml.AtPath(paths.Current().Child("b")),
				niceyaml.WithDetails(
					niceyaml.NewError("first declared here", niceyaml.AtPath(paths.Current().Child("a"))),
				),
			)),
			want: []string{"port conflicts"},
		},
		"located error above located errors that explain it": {
			err: yamltest.Bind(t, source, niceyaml.NewError("invalid",
				niceyaml.AtPath(paths.Current()),
				niceyaml.WithDetails(badA(), badB()),
			)),
			want: []string{"invalid"},
		},
		"detail above a join and a reason": {
			// The branches of the join explain the error as the detail
			// does, so the error stays one row.
			err: yamltest.Bind(t, source, niceyaml.NewError("ports conflict", niceyaml.WithDetails(
				niceyaml.WrapError(errors.Join(badA(), badB()), niceyaml.WithDetails(errors.New("see docs"))),
			))),
			want: []string{"ports conflict"},
		},
		"detail above a join of one and a reason": {
			err: yamltest.Bind(t, source, niceyaml.NewError("ports conflict", niceyaml.WithDetails(
				niceyaml.WrapError(errors.Join(badA()), niceyaml.WithDetails(errors.New("see docs"))),
			))),
			want: []string{"ports conflict"},
		},
		"unbound detail above a join and a reason": {
			err: niceyaml.NewError("ports conflict", niceyaml.WithDetails(
				niceyaml.WrapError(errors.Join(badA(), badB()), niceyaml.WithDetails(errors.New("see docs"))),
			)),
			want: []string{"ports conflict"},
		},
		"summary above problems with no location": {
			// A summary and an error with reasons share a shape, and the
			// constructor tells them apart.
			err: yamltest.Bind(t, source, niceyaml.NewSummary("2 problems",
				errors.New("v is short"),
				errors.New("w is short"),
			)),
			want: []string{"v is short", "w is short"},
		},
		"summary above problems with details": {
			err: yamltest.Bind(t, source, niceyaml.NewSummary("2 conflicts",
				niceyaml.NewError("a conflicts", niceyaml.AtPath(paths.Current().Child("a")),
					niceyaml.WithDetails(badB())),
				niceyaml.NewError("b conflicts", niceyaml.AtPath(paths.Current().Child("b")),
					niceyaml.WithDetails(badA())),
			)),
			want: []string{"a conflicts", "b conflicts"},
		},
		"summary inside a join": {
			err: yamltest.Bind(t, source, errors.Join(
				niceyaml.NewSummary("2 schema violations", badA(), badB()),
				errors.New("too old"),
			)),
			want: []string{"bad a", "bad b", "too old"},
		},
		"join inside a summary": {
			err: yamltest.Bind(t, source, niceyaml.NewSummary("2 checks",
				errors.Join(badA(), badB()),
				errors.New("too old"),
			)),
			want: []string{"bad a", "bad b", "too old"},
		},
		"location the source does not hold": {
			// A location that did not resolve is still the location of a
			// problem.
			err: yamltest.Bind(t, source, niceyaml.NewSummary("3 problems",
				badB(),
				niceyaml.NewError("off the end", niceyaml.AtPosition(position.New(99, 0))),
				errors.New("too old"),
			)),
			want: []string{"bad b", "off the end", "too old"},
		},
		"path the document does not hold": {
			err: yamltest.Bind(t, source, niceyaml.NewSummary("2 problems",
				niceyaml.NewError("missing", niceyaml.AtPath(paths.Current().Child("c"))),
				badA(),
			)),
			want:     []string{"bad a", "missing"},
			anyOrder: true,
		},
		"wrapper around a bound summary": {
			err: fmt.Errorf("load: %w", yamltest.Bind(t, source,
				niceyaml.NewSummary("2 schema violations", badA(), badB()),
			)),
			want: []string{"bad a", "bad b"},
		},
		"wrapper around a bound located error": {
			err:  fmt.Errorf("load: %w", yamltest.Bind(t, source, badA())),
			want: []string{"bad a"},
		},
		"Error with a reason around a binding": {
			err: niceyaml.WrapError(
				yamltest.Bind(t, source, badA()),
				niceyaml.WithDetails(errors.New("see docs")),
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
			err: niceyaml.WrapError(errNoMatch, niceyaml.WithDetails(
				errors.New("no schema directive"),
				errors.New("no catalog entry matches"),
			)),
			want: []string{"no matching schema"},
		},
		"unbound located error": {
			err:  badA(),
			want: []string{"@.a: bad a"},
		},
		"unbound summary above located errors": {
			// A validator that checked a Go value binds its errors to no
			// source, and each still names its path.
			err:  niceyaml.NewSummary("2 schema violations", badA(), badB()),
			want: []string{"@.a: bad a", "@.b: bad b"},
		},
		"unbound located error keeps its reasons": {
			err: niceyaml.NewError("bad a",
				niceyaml.AtPath(paths.Current().Child("a")),
				niceyaml.WithDetails(errors.New("tag missing")),
			),
			want: []string{"@.a: bad a"},
		},
		"unbound wrapper around a located error": {
			err:  fmt.Errorf("check: %w", badA()),
			want: []string{"@.a: check: bad a"},
		},
		"unbound error above an unbound error and a binding": {
			err: niceyaml.NewSummary("2 files",
				errRead,
				yamltest.Bind(t, source, badA()),
			),
			want: []string{"read g.yaml: no such file", "bad a"},
		},
		"rebased error without a location": {
			err:  yamltest.Bind(t, source, niceyaml.Rebase(errors.New("closes early"), paths.Current().Child("a"))),
			want: []string{"closes early"},
		},
		"rebased error keeps its reasons": {
			// The base locates the error, and the reasons explain it.
			err: yamltest.Bind(t, source, niceyaml.Rebase(
				niceyaml.NewError("hours invalid", niceyaml.WithDetails(
					errors.New("opens too early"),
					errors.New("never closes"),
				)),
				paths.Current().Child("a"),
			)),
			want: []string{"hours invalid"},
		},
		"rebased summary above problems with no location": {
			// The base locates each error the summary heads.
			err: yamltest.Bind(t, source, niceyaml.Rebase(
				niceyaml.NewSummary("hours invalid",
					errors.New("opens too early"),
					errors.New("never closes"),
				),
				paths.Current().Child("a"),
			)),
			want: []string{"opens too early", "never closes"},
		},
		"rebased summary above located errors": {
			err: yamltest.Bind(t, source, niceyaml.Rebase(
				niceyaml.NewSummary("2 problems", badA(), badB()),
				paths.Current(),
			)),
			want: []string{"bad a", "bad b"},
		},
		"unbound rebased error keeps its reasons": {
			err: niceyaml.Rebase(
				niceyaml.NewError("hours invalid", niceyaml.WithDetails(errors.New("never closes"))),
				paths.Current().Child("x"),
			),
			want: []string{"@.x: hours invalid"},
		},
		"unbound rebased summary above located errors": {
			err: niceyaml.Rebase(
				niceyaml.NewSummary("2 problems", badA(), badB()),
				paths.Current().Child("x"),
			),
			want: []string{"@.x.a: bad a", "@.x.b: bad b"},
		},
		"join of a run over several files": {
			err: errors.Join(
				yamltest.Bind(t, source, niceyaml.NewSummary("2 schema violations", badA(), badB())),
				yamltest.Bind(t, source, badB()),
				fmt.Errorf("read file: %w", errRead),
				yamltest.Bind(t, source, niceyaml.WrapError(errNoMatch, niceyaml.WithDetails(
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

func TestErrorTree_Problems_Lines(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\nc: 3\n", niceyaml.WithName("f.yaml"))

	at := func(msg, key string, opts ...niceyaml.ErrorOption) *niceyaml.Error {
		return niceyaml.NewError(
			msg,
			append([]niceyaml.ErrorOption{niceyaml.AtPath(paths.Current().Child(key))}, opts...)...,
		)
	}

	// The message of a binding holds a line for each heading, then one line
	// per problem. The rows are those lines of the problems, in the same
	// order, whatever their details carry.
	tcs := map[string]struct {
		err error
		// The number of lines of headings that open the message.
		headings int
	}{
		"one problem with details": {
			err: at("conflict", "c", niceyaml.WithDetails(at("bad a", "a"), at("bad b", "b"))),
		},
		"summary of problems with details": {
			err: niceyaml.NewSummary("2 conflicts",
				at("a conflicts", "a", niceyaml.WithDetails(at("first declared here", "b"))),
				at("c conflicts", "c", niceyaml.WithDetails(at("first declared here", "b"))),
			),
			headings: 1,
		},
		"summary of problems with and without a location": {
			err:      niceyaml.NewSummary("3 problems", at("bad c", "c"), errors.New("too old"), at("bad a", "a")),
			headings: 1,
		},
		"join of problems": {
			err: errors.Join(at("bad a", "a"), niceyaml.NewError("conflict", niceyaml.WithDetails(at("bad b", "b")))),
		},
		"wrapper around a summary": {
			err:      fmt.Errorf("load: %w", niceyaml.NewSummary("2 problems", at("bad a", "a"), at("bad b", "b"))),
			headings: 1,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			bound := yamltest.Bind(t, source, tc.err)

			var rows []string

			for problem := range niceyaml.NewErrorTree(bound).Problems() {
				require.NotNil(t, problem.Bound)

				rows = append(rows, problem.Bound.Error())
			}

			assert.Equal(t, strings.Split(bound.Error(), "\n")[tc.headings:], rows)
		})
	}
}

func TestErrorTree_New_Detail(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("f.yaml"))

	build := func() error {
		return niceyaml.NewSummary("2 problems",
			niceyaml.NewError(
				"bad a",
				niceyaml.AtPath(paths.Current().Child("a")),
				niceyaml.WithDetails(
					errors.New("reason"),
					niceyaml.NewError("see b", niceyaml.AtPath(paths.Current().Child("b"))),
				),
			),
			errors.New("too old"),
		)
	}

	// Marks returns each node of t below the root as its text and its
	// Detail mark, indented by its depth.
	var marks func(t niceyaml.ErrorTree, indent string) []string

	marks = func(t niceyaml.ErrorTree, indent string) []string {
		var out []string

		for _, child := range t.Children {
			out = append(out, fmt.Sprintf("%s%s %t", indent, child.Text, child.Detail))
			out = append(out, marks(child, indent+"  ")...)
		}

		return out
	}

	tcs := map[string]struct {
		err  error
		want []string
	}{
		"unbound": {
			err: build(),
			want: []string{
				"@.a: bad a false",
				"  reason true",
				"  @.b: see b true",
				"too old false",
			},
		},
		"bound": {
			err: yamltest.Bind(t, source, build()),
			want: []string{
				"1:4: $.a: bad a false",
				"  2:4: $.b: see b true",
				"  reason true",
				"too old false",
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// The problems the summary heads carry no mark, and the details
			// of a problem carry it.
			assert.Equal(t, tc.want, marks(niceyaml.NewErrorTree(tc.err), ""))
		})
	}
}

func TestErrorTree_Problems_JoinDetail(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName("f.yaml"))

	// The detail wraps a join and holds a reason, so it has no text of its
	// own, and the reason and the branches take its place below the error.
	build := func() error {
		return niceyaml.NewError("conflict", niceyaml.WithDetails(niceyaml.WrapError(
			errors.Join(errors.New("first"), errors.New("second")),
			niceyaml.WithDetails(errors.New("reason")),
		)))
	}

	tcs := map[string]struct {
		err error
	}{
		"unbound": {err: build()},
		"bound":   {err: yamltest.Bind(t, source, build())},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := slices.Collect(niceyaml.NewErrorTree(tc.err).Problems())
			require.Len(t, got, 1)
			assert.Equal(t, "conflict", got[0].Message())

			var details []string

			for _, child := range got[0].Children {
				assert.True(t, child.Detail, child.Text)

				details = append(details, child.Message())
			}

			assert.Equal(t, []string{"reason", "first", "second"}, details)
		})
	}
}

func TestErrorTree_Problems_Reasons(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName("f.yaml"))

	err := yamltest.Bind(t, source, niceyaml.WrapError(
		errors.New("no matching schema"),
		niceyaml.WithDetails(errors.New("no schema directive"), errors.New("no catalog entry matches")),
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

func TestErrorTree_Problems_WrappedJoin(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("hours:\n  open: a\n", niceyaml.WithName("f.yaml"))

	doc, err := source.Document()
	require.NoError(t, err)

	early := errors.New("opens too early")
	late := errors.New("closes too late")

	tcs := map[string]struct {
		build func(t *testing.T) error
		want  []string
	}{
		"unbound": {
			build: func(*testing.T) error {
				return niceyaml.WrapError(errors.Join(early, late))
			},
			want: []string{"opens too early", "closes too late"},
		},
		"bound at the root": {
			build: func(*testing.T) error {
				return doc.Bind(niceyaml.WrapError(errors.Join(early, late)))
			},
			want: []string{"f.yaml: opens too early", "f.yaml: closes too late"},
		},
		"bound through a scoped Node": {
			build: func(t *testing.T) error {
				t.Helper()

				hours := yamltest.At(t, doc, paths.Current().Child("hours"))

				return hours.Bind(niceyaml.WrapError(errors.Join(early, late)))
			},
			want: []string{"f.yaml:2:3: $.hours: opens too early", "f.yaml:2:3: $.hours: closes too late"},
		},
		"rebased": {
			build: func(*testing.T) error {
				return niceyaml.Rebase(niceyaml.WrapError(errors.Join(early, late)), paths.Current().Child("hours"))
			},
			want: []string{"@.hours: opens too early", "@.hours: closes too late"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// WrapError declares each branch of the join a problem the
			// document is at fault for, and each keeps its text.
			var got []string

			for problem := range niceyaml.NewErrorTree(tc.build(t)).Problems() {
				assert.True(t, problem.Invalid(), problem.Text)

				got = append(got, problem.Text)
			}

			assert.Equal(t, tc.want, got)
		})
	}
}

// reasonedHours is a [niceyaml.SelfValidator] whose error names no
// location and gives two reasons.
type reasonedHours struct {
	Open string `yaml:"open"`
}

// Validate returns the error.
func (reasonedHours) Validate() error {
	return niceyaml.NewError("hours invalid", niceyaml.WithDetails(
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
	// locates the error at the value. The reasons explain it and take no
	// location.
	got := slices.Collect(niceyaml.NewErrorTree(doc.DecodeInto(t.Context(), &cfg)).Problems())
	require.Len(t, got, 1)
	require.NotNil(t, got[0].Bound)

	assert.Equal(t, "hours invalid", got[0].Bound.Message())
	require.Len(t, got[0].Children, 2)

	for _, reason := range got[0].Children {
		assert.True(t, reason.Detail)

		_, ok := reason.Bound.Position()
		assert.False(t, ok)
	}

	pos, ok := got[0].Bound.Position()
	require.True(t, ok)
	assert.Equal(t, position.New(1, 2), pos)
}

func TestErrorTree_Problems_Scope(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("hours:\n  open: a\n", niceyaml.WithName("f.yaml"))

	doc, err := source.Document()
	require.NoError(t, err)

	hours := yamltest.At(t, doc, paths.Current().Child("hours"))

	// The binding joins the scope in front of each path, and the summary
	// is a heading, so it yields no row.
	bound := hours.Bind(niceyaml.NewSummary("2 problems",
		niceyaml.NewError("bad open", niceyaml.AtPath(paths.Current().Child("open"))),
		niceyaml.NewError("no close", niceyaml.AtPath(paths.Current().Child("close"))),
	))

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
				Children: []niceyaml.ErrorTree{{Text: "no schema directive", Detail: true}},
			},
			want: []string{"no matching schema"},
		},
		"root with no text gives its place to its children": {
			tree: niceyaml.ErrorTree{Children: []niceyaml.ErrorTree{
				{Text: "first", Children: []niceyaml.ErrorTree{{Text: "reason", Detail: true}}},
				{Text: "second"},
			}},
			want: []string{"first", "second"},
		},
		"node above children that are no details heads them": {
			tree: niceyaml.ErrorTree{
				Text: "2 problems",
				Children: []niceyaml.ErrorTree{
					{Text: "$.a: bad a"},
					{Text: "too old", Children: []niceyaml.ErrorTree{{Text: "see docs", Detail: true}}},
				},
			},
			want: []string{"$.a: bad a", "too old"},
		},
		"heading passes over its own details": {
			tree: niceyaml.ErrorTree{
				Text: "2 problems",
				Children: []niceyaml.ErrorTree{
					{Text: "see docs", Detail: true},
					{Text: "bad a"},
				},
			},
			want: []string{"bad a"},
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

func TestErrorTree_Invalid(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("f.yaml"))

	badA := func() *niceyaml.Error {
		return niceyaml.NewError("bad a", niceyaml.AtPath(paths.Current().Child("a")))
	}
	badB := func() *niceyaml.Error {
		return niceyaml.NewError("bad b", niceyaml.AtPath(paths.Current().Child("b")))
	}

	errRead := fmt.Errorf("read g.yaml: %w", fs.ErrPermission)

	tcs := map[string]struct {
		err  error
		want bool
	}{
		"empty tree": {
			err: nil,
		},
		"error with an empty message": {
			err: errors.New(""),
		},
		"invalid problem": {
			err:  badA(),
			want: true,
		},
		"read error": {
			err: errRead,
		},
		"context error": {
			err: context.Canceled,
		},
		"join of invalid problems": {
			err:  errors.Join(badA(), badB()),
			want: true,
		},
		"mixed join": {
			err: errors.Join(badA(), errRead),
		},
		"summary of invalid problems": {
			err:  niceyaml.NewSummary("2 problems", badA(), badB()),
			want: true,
		},
		"mixed summary": {
			err: niceyaml.NewSummary("2 problems", badA(), errRead),
		},
		"fmt wrapper over an invalid problem": {
			err:  fmt.Errorf("load: %w", badA()),
			want: true,
		},
		"fmt wrapper over a join of invalid problems": {
			err:  fmt.Errorf("load: %w", errors.Join(badA(), badB())),
			want: true,
		},
		"fmt wrapper over a mixed join": {
			err: fmt.Errorf("load: %w", errors.Join(badA(), errRead)),
		},
		"joins of joins of invalid problems": {
			err: errors.Join(
				errors.Join(badA(), badB()),
				errors.Join(niceyaml.NewError("too old")),
			),
			want: true,
		},
		"joins of joins with one read error": {
			err: errors.Join(
				errors.Join(badA(), badB()),
				errors.Join(niceyaml.NewError("too old"), errors.Join(errRead)),
			),
		},
		"wrapper with several verbs over an invalid problem and a read error": {
			// The wrapper is one problem, and the invalid branch decides.
			err:  fmt.Errorf("%w; close: %w", badA(), errRead),
			want: true,
		},
		"wrapper with several verbs over a sentinel and a read error": {
			err: fmt.Errorf("%w: %w", errors.New("config rejected"), errRead),
		},
		"read error with an invalid detail": {
			// A detail explains the problem and decides nothing.
			err: readWithDetail(t, errRead, badB()),
		},
		"invalid problem with a detail that is a read error": {
			err:  niceyaml.NewError("ports conflict", niceyaml.WithDetails(errRead)),
			want: true,
		},
		"invalid problem with a detail that heads a read error": {
			err: niceyaml.NewError("ports conflict", niceyaml.WithDetails(
				niceyaml.NewSummary("2 reasons", badA(), errRead),
			)),
			want: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, niceyaml.NewErrorTree(tc.err).Invalid(), "unbound")
			assert.Equal(t, tc.want, niceyaml.NewErrorTree(yamltest.Bind(t, source, tc.err)).Invalid(), "bound")
		})
	}
}

// readWithDetail returns err, an error that is no fault of the document,
// placed at a value and explained by detail. [niceyaml.Rebase] builds the
// only [*niceyaml.Error] that declares nothing and takes details.
func readWithDetail(t *testing.T, err, detail error) error {
	t.Helper()

	placed, ok := errors.AsType[*niceyaml.Error](niceyaml.Rebase(err, paths.Current().Child("a")))
	require.True(t, ok)

	return placed.With(niceyaml.WithDetails(detail))
}

func TestErrorTree_Invalid_Nodes(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("f.yaml"))

	badA := func() *niceyaml.Error {
		return niceyaml.NewError("bad a", niceyaml.AtPath(paths.Current().Child("a")))
	}
	badB := func() *niceyaml.Error {
		return niceyaml.NewError("bad b", niceyaml.AtPath(paths.Current().Child("b")))
	}

	errRead := fmt.Errorf("read g.yaml: %w", fs.ErrPermission)

	// Each row of want is the message of a node and what Invalid reports
	// for it.
	tcs := map[string]struct {
		err  error
		want []string
	}{
		"detail of a read error answers for itself": {
			err: readWithDetail(t, errRead, badB()),
			want: []string{
				"read g.yaml: permission denied false",
				"bad b true",
			},
		},
		"detail of an invalid problem answers for itself": {
			err: niceyaml.NewError("ports conflict", niceyaml.WithDetails(errRead)),
			want: []string{
				"ports conflict true",
				"read g.yaml: permission denied false",
			},
		},
		"detail that heads problems answers for them": {
			err: niceyaml.NewError("ports conflict", niceyaml.WithDetails(
				niceyaml.NewSummary("2 reasons", badA(), errRead),
			)),
			want: []string{
				"ports conflict true",
				"2 reasons false",
				"bad a true",
				"read g.yaml: permission denied false",
			},
		},
		"summary below a mixed join": {
			err: errors.Join(niceyaml.NewSummary("2 violations", badA(), badB()), errRead),
			want: []string{
				"2 violations true",
				"bad a true",
				"bad b true",
				"read g.yaml: permission denied false",
			},
		},
		"mixed summary": {
			err: niceyaml.NewSummary("2 problems", badA(), errRead),
			want: []string{
				"2 problems false",
				"bad a true",
				"read g.yaml: permission denied false",
			},
		},
		"fmt wrapper over a mixed join": {
			err: fmt.Errorf("load: %w", errors.Join(badA(), errRead)),
			want: []string{
				"load: false",
				"bad a true",
				"read g.yaml: permission denied false",
			},
		},
	}

	answers := func(tree niceyaml.ErrorTree) []string {
		var out []string

		for node := range tree.All() {
			out = append(out, fmt.Sprintf("%s %t", node.Message(), node.Invalid()))
		}

		return out
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, answers(niceyaml.NewErrorTree(tc.err)), "unbound")

			// A binding orders its children by position, so the rows
			// hold in any order.
			bound := niceyaml.NewErrorTree(yamltest.Bind(t, source, tc.err))
			assert.ElementsMatch(t, tc.want, answers(bound), "bound")
		})
	}
}

func TestErrorTree_Invalid_HandBuilt(t *testing.T) {
	t.Parallel()

	errRead := fmt.Errorf("read g.yaml: %w", fs.ErrPermission)

	tcs := map[string]struct {
		tree niceyaml.ErrorTree
		want bool
	}{
		"zero tree": {
			tree: niceyaml.ErrorTree{},
		},
		"node with no Err": {
			tree: niceyaml.ErrorTree{Text: "bad a"},
		},
		"heading over invalid problems": {
			tree: niceyaml.ErrorTree{
				Text: "2 problems",
				Children: []niceyaml.ErrorTree{
					{Text: "bad a", Err: niceyaml.NewError("bad a")},
					{Text: "bad b", Err: niceyaml.NewError("bad b")},
				},
			},
			want: true,
		},
		"heading answers through its problems": {
			// The Err of a heading decides nothing.
			tree: niceyaml.ErrorTree{
				Text: "2 problems",
				Err:  niceyaml.NewError("2 problems"),
				Children: []niceyaml.ErrorTree{
					{Text: "bad a", Err: niceyaml.NewError("bad a")},
					{Text: "read g.yaml: permission denied", Err: errRead},
				},
			},
		},
		"problem above an invalid detail": {
			tree: niceyaml.ErrorTree{
				Text: "read g.yaml: permission denied",
				Err:  errRead,
				Children: []niceyaml.ErrorTree{
					{Text: "bad a", Err: niceyaml.NewError("bad a"), Detail: true},
				},
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.tree.Invalid())
		})
	}
}

func TestErrorTree_MessageAndPath(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("f.yaml"))

	pathA := paths.Current().Child("a")
	pathB := paths.Current().Child("b")

	badA := func() *niceyaml.Error {
		return niceyaml.NewError("bad a", niceyaml.AtPath(pathA))
	}
	badB := func() *niceyaml.Error {
		return niceyaml.NewError("bad b", niceyaml.AtPath(pathB))
	}
	withDetail := func() *niceyaml.Error {
		return badA().With(niceyaml.WithDetails(niceyaml.NewError("see b", niceyaml.AtPath(pathB))))
	}

	problems := func(t *testing.T, err error) []niceyaml.ErrorTree {
		t.Helper()

		got := slices.Collect(niceyaml.NewErrorTree(err).Problems())
		require.NotEmpty(t, got)

		return got
	}

	tcs := map[string]struct {
		node     func(t *testing.T) niceyaml.ErrorTree
		want     string
		wantPath string
	}{
		"bound root": {
			node: func(t *testing.T) niceyaml.ErrorTree {
				t.Helper()

				return niceyaml.NewErrorTree(yamltest.Bind(t, source, badA()))
			},
			want:     "bad a",
			wantPath: "$.a",
		},
		"bound child of a summary": {
			node: func(t *testing.T) niceyaml.ErrorTree {
				t.Helper()

				return problems(t, yamltest.Bind(t, source, niceyaml.NewSummary("2 problems", badA(), badB())))[1]
			},
			want:     "bad b",
			wantPath: "$.b",
		},
		"unbound located Error": {
			node: func(*testing.T) niceyaml.ErrorTree {
				return niceyaml.NewErrorTree(badA())
			},
			want:     "bad a",
			wantPath: "@.a",
		},
		"unbound violation under a summary": {
			// A schema validation of a decoded value returns violations
			// in this shape, with no source to bind them to.
			node: func(t *testing.T) niceyaml.ErrorTree {
				t.Helper()

				return problems(t, niceyaml.NewSummary("2 schema violations",
					niceyaml.WrapError(errors.New(`expected "integer"`), niceyaml.AtPath(pathA)),
					niceyaml.WrapError(errors.New(`expected "string"`), niceyaml.AtPath(pathB)),
				))[1]
			},
			want:     `expected "string"`,
			wantPath: "@.b",
		},
		"rebased Error with no location of its own": {
			node: func(*testing.T) niceyaml.ErrorTree {
				return niceyaml.NewErrorTree(niceyaml.Rebase(niceyaml.NewError("never closes"), pathA))
			},
			want:     "never closes",
			wantPath: "@.a",
		},
		"plain error": {
			node: func(*testing.T) niceyaml.ErrorTree {
				return niceyaml.NewErrorTree(errors.New("too old"))
			},
			want: "too old",
		},
		"unreadable file": {
			node: func(t *testing.T) niceyaml.ErrorTree {
				t.Helper()

				_, err := niceyaml.NewSourceFromFS(fstest.MapFS{}, "missing.yaml")
				require.Error(t, err)

				return niceyaml.NewErrorTree(err)
			},
			want: "read file: open missing.yaml: file does not exist",
		},
		"wrapper around a binding": {
			// The node reports the binding, without the text the
			// wrapper added.
			node: func(t *testing.T) niceyaml.ErrorTree {
				t.Helper()

				return niceyaml.NewErrorTree(fmt.Errorf("load config: %w", yamltest.Bind(t, source, badA())))
			},
			want:     "bad a",
			wantPath: "$.a",
		},
		"wrapper around an unbound Error": {
			// The text of the node puts the path in front of the text the
			// wrapper added, and the message leaves the path out.
			node: func(*testing.T) niceyaml.ErrorTree {
				return niceyaml.NewErrorTree(fmt.Errorf("load config: %w", badA()))
			},
			want:     "load config: bad a",
			wantPath: "@.a",
		},
		"Error with a path of its own around a binding": {
			// No source resolved the outer path, so the node has no
			// binding, and the message keeps the line of the inner one.
			node: func(t *testing.T) niceyaml.ErrorTree {
				t.Helper()

				return niceyaml.NewErrorTree(
					niceyaml.WrapError(yamltest.Bind(t, source, badA()), niceyaml.AtPath(pathB)),
				)
			},
			want:     "f.yaml:1:4: $.a: bad a",
			wantPath: "@.b",
		},
		"summary": {
			node: func(*testing.T) niceyaml.ErrorTree {
				return niceyaml.NewErrorTree(niceyaml.NewSummary("2 problems", badA(), badB()))
			},
			want: "2 problems",
		},
		"root of a join": {
			node: func(*testing.T) niceyaml.ErrorTree {
				return niceyaml.NewErrorTree(errors.Join(badA(), badB()))
			},
		},
		"unbound detail": {
			node: func(t *testing.T) niceyaml.ErrorTree {
				t.Helper()

				detail := problems(t, withDetail())[0].Children[0]
				require.True(t, detail.Detail)

				return detail
			},
			want:     "see b",
			wantPath: "@.b",
		},
		"bound detail": {
			node: func(t *testing.T) niceyaml.ErrorTree {
				t.Helper()

				detail := problems(t, yamltest.Bind(t, source, withDetail()))[0].Children[0]
				require.True(t, detail.Detail)

				return detail
			},
			want:     "see b",
			wantPath: "$.b",
		},
		"hand-built node": {
			node: func(*testing.T) niceyaml.ErrorTree {
				return niceyaml.ErrorTree{Err: errors.New("disk full"), Text: "write cache: disk full"}
			},
			want: "write cache: disk full",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			node := tc.node(t)

			assert.Equal(t, tc.want, node.Message())

			path, ok := node.Path()
			if tc.wantPath == "" {
				assert.False(t, ok)

				return
			}

			require.True(t, ok)
			assert.Equal(t, tc.wantPath, path.String())
		})
	}
}
