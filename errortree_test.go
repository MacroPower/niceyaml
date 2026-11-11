package niceyaml_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"iter"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/escape"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// countNodes returns the number of nodes with text in t.
func countNodes(t niceyaml.ErrorTree) int {
	n := 0
	if t.Label() != "" {
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
	out := niceyaml.ErrorTree{Row: niceyaml.ErrorRow{Text: t.Label()}}

	for _, child := range t.Children {
		out.Children = append(out.Children, textTree(child))
	}

	return out
}

// assertRows asserts that each node of tree, the tree of err, has a row
// whose parts are well formed and whose string is the row that
// [niceyaml.FormatError] prints for it. A row of several lines prints its
// later lines behind connectors, so the check reads its first line.
func assertRows(t *testing.T, err error, tree niceyaml.ErrorTree) {
	t.Helper()

	printed := niceyaml.FormatError(err)

	for node := range tree.All() {
		row := node.Row

		assert.NotEqual(t, niceyaml.ErrorRow{}, row)
		assert.Equal(t, row.String(), node.Label())
		assert.False(t, strings.HasSuffix(row.Location, ":"), "location %q", row.Location)
		assert.Contains(t, []string{"", "warning"}, row.Severity)

		if row.Path != "" {
			assert.Contains(t, "$@", row.Path[:1], "path %q", row.Path)
		}

		first, _, _ := strings.Cut(escape.Message(row.String()), "\n")
		assert.Contains(t, printed, first)
	}
}

// textNode returns a node with the text label above children, the shape
// [textTree] gives a node of a tree.
func textNode(label string, children ...niceyaml.ErrorTree) niceyaml.ErrorTree {
	return niceyaml.ErrorTree{Row: niceyaml.ErrorRow{Text: label}, Children: children}
}

// textForest returns a root with no text above children, the shape
// [textTree] gives the tree of a join.
func textForest(children ...niceyaml.ErrorTree) niceyaml.ErrorTree {
	return niceyaml.ErrorTree{Children: children}
}

// errorTreeSource returns a new Source named f.yaml that holds the key a
// on its first line and the key b on its second.
func errorTreeSource() *niceyaml.Source {
	return niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("f.yaml"))
}

// badKeyError returns a function that builds an error with the message
// "bad <key>" at the path of key under the value in scope. Each call
// builds an error of its own.
func badKeyError(key string) func() *niceyaml.Error {
	return func() *niceyaml.Error {
		return yamltest.ErrorAt("bad "+key, key)
	}
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

// rewrittenError wraps err with a message of its own that does not hold
// the message of err.
type rewrittenError struct {
	err error
	msg string
}

// Error returns the message of the wrapper alone.
func (e rewrittenError) Error() string {
	return e.msg
}

// Unwrap returns the error the wrapper wraps.
func (e rewrittenError) Unwrap() error {
	return e.err
}

// causeError is an error of its own type that can wrap a cause. Its
// Unwrap method returns nil when it does not have one.
type causeError struct {
	cause error
	msg   string
}

// Error returns the message, and the message of the cause behind it when
// the error has one.
func (e *causeError) Error() string {
	if e.cause == nil {
		return e.msg
	}

	return e.msg + ": " + e.cause.Error()
}

// Unwrap returns the cause, which can be nil.
func (e *causeError) Unwrap() error {
	return e.cause
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

	// The tree reads each message reads times at most, whatever n is. An
	// Error with a location around the join reads the message of the join
	// once more to cut it from its own.
	tcs := map[string]struct {
		build  func(error) error
		prefix string
		reads  int64
	}{
		"join": {
			build: func(err error) error { return err },
			reads: 2,
		},
		"rebased join": {
			build:  func(err error) error { return niceyaml.Rebase(err, paths.Current()) },
			prefix: "@: ",
			reads:  2,
		},
		"bound join": {
			build:  func(err error) error { return source.Bind(err) },
			prefix: "f.yaml: ",
			reads:  2,
		},
		"located join": {
			build:  func(err error) error { return niceyaml.Locate(err, niceyaml.AtPath(paths.Current())) },
			prefix: "@: ",
			reads:  3,
		},
		"bound located join": {
			build: func(err error) error {
				return source.Bind(niceyaml.Locate(err, niceyaml.AtPath(paths.Current())))
			},
			prefix: "f.yaml:1:1: $: ",
			reads:  3,
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
				want.Children = append(want.Children, textNode(tc.prefix+msg))
			}

			got := textTree(niceyaml.NewErrorTree(tc.build(joined)))

			assert.Equal(t, want, got)
			// Finding each join by rebuilding its message would read every
			// message once per join above it, n*n/2 times in all.
			assert.LessOrEqual(t, calls.Load(), tc.reads*n)
		})
	}
}

func TestErrorTree_New_DeepMultiWrap(t *testing.T) {
	t.Parallel()

	const n = 20

	source := errorTreeSource()

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
			want: textNode("e0; e1; e2; e3; e4; e5; e6; e7; e8; e9; " +
				"e10; e11; e12; e13; e14; e15; e16; e17; e18; e19"),
		},
		"sentinel wrappers over a located error": {
			build: func(calls *atomic.Int64) error {
				sentinel := countingError{calls: calls, msg: "invalid"}

				var err error = yamltest.ErrorAt("bad", "a")

				for i := 1; i < n; i++ {
					err = fmt.Errorf("%w: %w", sentinel, err)
				}

				return err
			},
			want: textNode("@.a: " + strings.Repeat("invalid: ", n-1) + "bad"),
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

	source := errorTreeSource()

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
					located := yamltest.ErrorAt(fmt.Sprintf("e%d", i), "a")
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
			located := yamltest.ErrorAt("last", "b")
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
				yamltest.ErrorAt("bad a", "a"),
				errors.New("plain"),
			}})

			assert.LessOrEqual(t, calls.Load(), tc.want)
		})
	}
}

func TestErrorTree_New(t *testing.T) {
	t.Parallel()

	source := errorTreeSource()
	other := niceyaml.NewSourceFromString("c: 3\n", niceyaml.WithName("g.yaml"))
	wide := niceyaml.NewSourceFromString("a: hello world\nb: 2\n", niceyaml.WithName("f.yaml"))
	second := yamltest.Documents(t, niceyaml.NewSourceFromString("a: 1\n---\na: 1\nb: 2\n", niceyaml.WithName("m.yaml")), 2)[1]

	badA, badB, badC := badKeyError("a"), badKeyError("b"), badKeyError("c")

	errA := errors.New("first")
	errB := errors.New("second")

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
			want: textNode("boom"),
		},
		"error without nested errors": {
			err:  badA(),
			want: textNode("@.a: bad a"),
		},
		"nested errors are children": {
			err: niceyaml.NewSummary("2 problems", badA(), niceyaml.NewError("no path")),
			want: textNode("2 problems",
				textNode("@.a: bad a"),
				textNode("no path"),
			),
		},
		"bound children carry their position without the name": {
			err: yamltest.Bind(t, source, niceyaml.NewSummary("2 problems", badA(), badB())),
			want: textNode("f.yaml: 2 problems",
				textNode("1:4: $.a: bad a"),
				textNode("2:4: $.b: bad b"),
			),
		},
		"bound child reports the position it was given": {
			// The position sits inside the token of the value, so the
			// child names it as its own message does, rather than the
			// start of the token the excerpt marks.
			err: yamltest.Bind(t, wide, niceyaml.NewSummary("2 problems",
				badB(),
				niceyaml.NewError("mid", niceyaml.AtPosition(position.New(0, 8))),
			)),
			want: textNode("f.yaml: 2 problems",
				textNode("1:9: mid"),
				textNode("2:4: $.b: bad b"),
			),
		},
		"children sort by position": {
			err: yamltest.Bind(t, source, niceyaml.NewSummary("2 problems", badB(), badA())),
			want: textNode("f.yaml: 2 problems",
				textNode("1:4: $.a: bad a"),
				textNode("2:4: $.b: bad b"),
			),
		},
		"children without a position follow the positioned ones": {
			err: yamltest.Bind(t, source, niceyaml.NewSummary("3 problems",
				niceyaml.NewError("bad x", niceyaml.AtPath(paths.Current().Child("x").IndexAll())),
				niceyaml.NewError("no path"),
				badA(),
			)),
			want: textNode("f.yaml: 3 problems",
				textNode("1:4: $.a: bad a"),
				textNode("$.x[*]: bad x"),
				textNode("no path"),
			),
		},
		"child wrapping a binding of the same source keeps one position": {
			err: yamltest.Bind(t, source, niceyaml.NewError("outer", niceyaml.WithDetails(
				fmt.Errorf("ctx: %w", yamltest.Bind(t, source, badA())),
			))),
			want: textNode("f.yaml: outer",
				textNode("1:4: $.a: ctx: bad a"),
			),
		},
		"child wrapping a binding in Errors alone reads as the binding": {
			err: yamltest.Bind(t, source, niceyaml.NewSummary("summary",
				niceyaml.Invalid(yamltest.Bind(t, source, badA())),
				niceyaml.Invalid(yamltest.Bind(t, source, badB()), niceyaml.WithDetails(niceyaml.NewError("x"))),
				niceyaml.Invalid(yamltest.Bind(t, source, errors.New("plain"))),
				niceyaml.Invalid(yamltest.Bind(t, source,
					niceyaml.Invalid(yamltest.Bind(t, source, badA()), niceyaml.WithDetails(niceyaml.NewError("y"))),
				)),
			)),
			want: textNode("f.yaml: summary",
				textNode("1:4: $.a: bad a"),
				textNode("1:4: $.a: bad a", textNode("y")),
				textNode("2:4: $.b: bad b", textNode("x")),
				textNode("plain"),
			),
		},
		"root with a position keeps it": {
			err: yamltest.Bind(t, source, niceyaml.NewError("outer",
				niceyaml.AtPath(paths.Current().Child("a")),
				niceyaml.WithDetails(badB()),
			)),
			want: textNode("f.yaml:1:4: $.a: outer",
				textNode("2:4: $.b: bad b"),
			),
		},
		"wrapper context reads as a wrapper inside the binding": {
			err: fmt.Errorf("document 0: %w", yamltest.Bind(t, source,
				niceyaml.NewSummary("2 problems", badA(), badB()),
			)),
			want: textNode("f.yaml: document 0: 2 problems",
				textNode("1:4: $.a: bad a"),
				textNode("2:4: $.b: bad b"),
			),
		},
		"wrapper inside the binding stays in front of the root": {
			err: yamltest.Bind(t, source, fmt.Errorf("document 0: %w",
				niceyaml.NewSummary("2 problems", badA(), badB()),
			)),
			want: textNode("f.yaml: document 0: 2 problems",
				textNode("1:4: $.a: bad a"),
				textNode("2:4: $.b: bad b"),
			),
		},
		"nested error with nested errors is a subtree": {
			err: yamltest.Bind(t, source, niceyaml.NewError("outer", niceyaml.WithDetails(
				niceyaml.NewError("mid",
					niceyaml.AtPath(paths.Current().Child("a")),
					niceyaml.WithDetails(badB()),
				),
			))),
			want: textNode("f.yaml: outer",
				textNode("1:4: $.a: mid",
					textNode("2:4: $.b: bad b"),
				),
			),
		},
		"nested error bound to another source keeps its own positions": {
			err: yamltest.Bind(t, source, niceyaml.NewError("outer", niceyaml.WithDetails(
				niceyaml.Invalid(yamltest.Bind(t, other, niceyaml.NewError("inner", niceyaml.WithDetails(badC())))),
			))),
			want: textNode("f.yaml: outer",
				textNode("g.yaml: inner",
					textNode("1:4: $.c: bad c"),
				),
			),
		},
		"binding rebound to another binding keeps the inner positions": {
			err: yamltest.Bind(t, source, fmt.Errorf("outer: %w",
				yamltest.Bind(t, other, niceyaml.NewError("inner", niceyaml.WithDetails(badC()))),
			)),
			want: textNode("g.yaml: outer: inner",
				textNode("1:4: $.c: bad c"),
			),
		},
		"located error above a binding without a location gives it the location": {
			err: yamltest.Bind(t, source, niceyaml.Invalid(
				yamltest.Bind(t, source, errors.New("inner")),
				niceyaml.AtPath(paths.Current().Child("a")),
			)),
			want: textNode("f.yaml:1:4: $.a: inner"),
		},
		"located error above a binding whose location did not resolve gives it the location": {
			err: yamltest.Bind(t, source, niceyaml.Locate(
				yamltest.Bind(t, source, niceyaml.NewError("far", niceyaml.AtPosition(position.New(9, 0)))),
				niceyaml.AtPath(paths.Current().Child("a")),
			)),
			want: textNode("f.yaml:1:4: $.a: far"),
		},
		"located error above a located binding of another source keeps its own location": {
			err: yamltest.Bind(t, source, niceyaml.Invalid(
				yamltest.Bind(t, other, badC()),
				niceyaml.AtPath(paths.Current().Child("a")),
			)),
			want: textNode("f.yaml:1:4: $.a: g.yaml:1:4: $.c: bad c"),
		},
		"located error above a located binding keeps the inner location": {
			err: yamltest.Bind(t, source, niceyaml.Locate(
				yamltest.Bind(t, source, badB()),
				niceyaml.AtPath(paths.Current().Child("a")),
				niceyaml.WithDetails(niceyaml.NewError("see a", niceyaml.AtPath(paths.Current().Child("a")))),
			)),
			want: textNode("f.yaml:2:4: $.b: bad b",
				textNode("1:4: $.a: see a"),
			),
		},
		"located error above an inner binding positions its own nested errors": {
			err: yamltest.Bind(t, source, niceyaml.Invalid(
				yamltest.Bind(t, other, niceyaml.NewError("inner", niceyaml.WithDetails(badC()))),
				niceyaml.AtPath(paths.Current().Child("a")),
				niceyaml.WithDetails(badB()),
			)),
			want: textNode("f.yaml:1:4: $.a: g.yaml: inner",
				textNode("2:4: $.b: bad b"),
				textNode("g.yaml:1:4: $.c: bad c"),
			),
		},
		"joined errors form a forest": {
			err: errors.Join(
				yamltest.Bind(t, source, badA()),
				yamltest.Bind(t, other, badC()),
			),
			want: textForest(
				textNode("f.yaml:1:4: $.a: bad a"),
				textNode("g.yaml:1:4: $.c: bad c"),
			),
		},
		"joined errors with nested errors are subtrees": {
			err: errors.Join(
				yamltest.Bind(t, source, niceyaml.NewSummary("2 problems", badA(), badB())),
				yamltest.Bind(t, other, badC()),
			),
			want: textForest(
				textNode("f.yaml: 2 problems",
					textNode("1:4: $.a: bad a"),
					textNode("2:4: $.b: bad b"),
				),
				textNode("g.yaml:1:4: $.c: bad c"),
			),
		},
		"unbound Error keeps the order of its bindings": {
			err: niceyaml.NewSummary("summary",
				yamltest.Bind(t, source, badB()),
				yamltest.Bind(t, source, badA()),
			),
			want: textNode("summary",
				textNode("f.yaml:2:4: $.b: bad b"),
				textNode("f.yaml:1:4: $.a: bad a"),
			),
		},
		"unbound join keeps the order of its bindings": {
			err: errors.Join(
				yamltest.Bind(t, source, badB()),
				yamltest.Bind(t, source, badA()),
			),
			want: textForest(
				textNode("f.yaml:2:4: $.b: bad b"),
				textNode("f.yaml:1:4: $.a: bad a"),
			),
		},
		"bound join of two sources keeps the order it was given": {
			err: yamltest.Bind(t, source, errors.Join(
				yamltest.Bind(t, source, badB()),
				yamltest.Bind(t, other, badC()),
			)),
			want: textForest(
				textNode("f.yaml:2:4: $.b: bad b"),
				textNode("g.yaml:1:4: $.c: bad c"),
			),
			multiLine: true,
		},
		"bound join is a forest of named bindings": {
			err: yamltest.Bind(t, source, errors.Join(badB(), badA())),
			want: textForest(
				textNode("f.yaml:1:4: $.a: bad a"),
				textNode("f.yaml:2:4: $.b: bad b"),
			),
			multiLine: true,
		},
		"bound nested join flattens into one forest": {
			err: yamltest.Bind(t, source, errors.Join(errors.Join(badB()), badA())),
			want: textForest(
				textNode("f.yaml:1:4: $.a: bad a"),
				textNode("f.yaml:2:4: $.b: bad b"),
			),
		},
		"join nested under a message gives its place to its branches": {
			err: yamltest.Bind(t, source, niceyaml.NewError("outer", niceyaml.WithDetails(
				errors.Join(badB(), badA()),
			))),
			want: textNode("f.yaml: outer",
				textNode("1:4: $.a: bad a"),
				textNode("2:4: $.b: bad b"),
			),
		},
		"nested join bound to another source names it on its branches": {
			err: yamltest.Bind(t, source, niceyaml.NewSummary("outer",
				badA(),
				yamltest.Bind(t, other, errors.Join(
					badC(),
					niceyaml.NewError("start", niceyaml.AtPosition(position.New(0, 0))),
				)),
			)),
			want: textNode("f.yaml: outer",
				textNode("1:4: $.a: bad a"),
				textNode("g.yaml:1:1: start"),
				textNode("g.yaml:1:4: $.c: bad c"),
			),
		},
		"error that only wraps a join is the join": {
			err: niceyaml.Invalid(errors.Join(badA(), badB())),
			want: textForest(
				textNode("@.a: bad a"),
				textNode("@.b: bad b"),
			),
		},
		"error with a location around a join is the join with the location on its branches": {
			err: niceyaml.Invalid(errors.Join(errA, badB()), niceyaml.AtPath(paths.Current().Child("a"))),
			want: textForest(
				textNode("@.a: first"),
				textNode("@.b: bad b"),
			),
		},
		"bound error with a location around a join is a forest of named bindings": {
			err: yamltest.Bind(t, source, niceyaml.Invalid(
				errors.Join(badB(), errA),
				niceyaml.AtPath(paths.Current().Child("a")),
			)),
			want: textForest(
				textNode("f.yaml:1:4: $.a: first"),
				textNode("f.yaml:2:4: $.b: bad b"),
			),
			multiLine: true,
		},
		"nested join with a location gives its place to the branches": {
			// The branches sort among the other problems of the summary, so
			// the key of b sits between them.
			err: yamltest.Bind(t, source, niceyaml.NewSummary("2 problems",
				niceyaml.Locate(errors.Join(badA(), errA), niceyaml.AtPath(paths.Current().Child("b"))),
				niceyaml.NewError("key b", niceyaml.AtPath(paths.Current().Child("b").Key())),
			)),
			want: textNode("f.yaml: 2 problems",
				textNode("1:4: $.a: bad a"),
				textNode("2:1: $.b~: key b"),
				textNode("2:4: $.b: first"),
			),
		},
		"bound error that only wraps a join is a forest of named bindings": {
			err: yamltest.Bind(t, source, niceyaml.Invalid(errors.Join(badB(), badA()))),
			want: textForest(
				textNode("f.yaml:1:4: $.a: bad a"),
				textNode("f.yaml:2:4: $.b: bad b"),
			),
			multiLine: true,
		},
		"nested error that only wraps a join gives its place to the branches": {
			err: yamltest.Bind(t, source, niceyaml.NewError("outer", niceyaml.WithDetails(
				niceyaml.Invalid(errors.Join(badB(), badA())),
			))),
			want: textNode("f.yaml: outer",
				textNode("1:4: $.a: bad a"),
				textNode("2:4: $.b: bad b"),
			),
		},
		"error that only wraps a bound join is the bound join": {
			err: niceyaml.Invalid(yamltest.Bind(t, source, errors.Join(badB(), badA()))),
			want: textForest(
				textNode("f.yaml:1:4: $.a: bad a"),
				textNode("f.yaml:2:4: $.b: bad b"),
			),
		},
		"nested error that only wraps a bound join gives its place to the branches": {
			err: yamltest.Bind(t, source, niceyaml.NewError("outer", niceyaml.WithDetails(
				niceyaml.Invalid(yamltest.Bind(t, source, errors.Join(badB(), badA()))),
			))),
			want: textNode("f.yaml: outer",
				textNode("1:4: $.a: bad a"),
				textNode("2:4: $.b: bad b"),
			),
		},
		"nested error that only wraps a bound join with a nil branch gives its place to the rest": {
			err: yamltest.Bind(t, source, niceyaml.NewError("outer", niceyaml.WithDetails(
				niceyaml.Invalid(yamltest.Bind(t, source, errors.Join((*niceyaml.Error)(nil), badB()))),
			))),
			want: textNode("f.yaml: outer",
				textNode("2:4: $.b: bad b"),
			),
		},
		"bound join below a wrapper keeps the text of the wrapper as the root": {
			err: yamltest.Bind(t, source, fmt.Errorf("ctx: %w", errors.Join(badA(), badB()))),
			want: textNode("f.yaml: ctx:",
				textNode("1:4: $.a: bad a"),
				textNode("2:4: $.b: bad b"),
			),
		},
		"wrapper around a join of bindings at the root names its source": {
			// The rows below the wrapper leave out the name, so the root
			// names it.
			err: yamltest.Bind(t, source, fmt.Errorf("ctx: %w", errors.Join(
				yamltest.Bind(t, source, badA()),
				yamltest.Bind(t, source, badB()),
			))),
			want: textNode("f.yaml: ctx:",
				textNode("1:4: $.a: bad a"),
				textNode("2:4: $.b: bad b"),
			),
		},
		"wrapper around a join of bindings in a later document names the document": {
			err: second.Bind(fmt.Errorf("ctx: %w", errors.Join(second.Bind(badA()), second.Bind(badB())))),
			want: textNode("m.yaml: document 2: ctx:",
				textNode("3:4: $.a: bad a"),
				textNode("4:4: $.b: bad b"),
			),
		},
		"wrapper around a bound join in a bound join names its source": {
			// The rows below the wrapper leave out the name, so the row of
			// the wrapper names it.
			err: yamltest.Bind(t, source, errors.Join(
				errA,
				fmt.Errorf("ctx: %w", yamltest.Bind(t, source, errors.Join(badB(), badA()))),
			)),
			want: textForest(
				textNode("f.yaml: first"),
				textNode("f.yaml: ctx:",
					textNode("1:4: $.a: bad a"),
					textNode("2:4: $.b: bad b"),
				),
			),
		},
		"wrapper around a bound join of another source names that source": {
			err: yamltest.Bind(t, source, errors.Join(
				badA(),
				fmt.Errorf("ctx: %w", yamltest.Bind(t, other, errors.Join(badC(), errB))),
			)),
			want: textForest(
				textNode("f.yaml:1:4: $.a: bad a"),
				textNode("g.yaml: ctx:",
					textNode("1:4: $.c: bad c"),
					textNode("second"),
				),
			),
		},
		"bound join that leads with another source names the root": {
			err: yamltest.Bind(t, source, fmt.Errorf("ctx: %w", errors.Join(
				yamltest.Bind(t, other, badC()),
				badB(),
			))),
			want: textNode("f.yaml: ctx:",
				textNode("g.yaml:1:4: $.c: bad c"),
				textNode("2:4: $.b: bad b"),
			),
		},
		"bound join that leads with a same-source binding of another source names the root": {
			err: yamltest.Bind(t, source, fmt.Errorf("ctx: %w", errors.Join(
				yamltest.Bind(t, source, errors.Join(yamltest.Bind(t, other, badC()))),
				badB(),
			))),
			want: textNode("f.yaml: ctx:",
				textNode("g.yaml:1:4: $.c: bad c"),
				textNode("2:4: $.b: bad b"),
			),
		},
		"Error with details around a same-source binding of another source names the root": {
			err: yamltest.Bind(t, source, fmt.Errorf("ctx: %w", niceyaml.Invalid(
				yamltest.Bind(t, source, errors.Join(yamltest.Bind(t, other, badC()))),
				niceyaml.WithDetails(badB()),
			))),
			want: textNode("f.yaml: ctx:",
				textNode("2:4: $.b: bad b"),
				textNode("g.yaml:1:4: $.c: bad c"),
			),
		},
		"child with details around a bound join gives its place to its named children": {
			err: yamltest.Bind(t, other, niceyaml.NewError("root",
				niceyaml.AtPath(paths.Current().Child("c")),
				niceyaml.WithDetails(yamltest.Bind(t, source, niceyaml.Invalid(
					yamltest.Bind(t, source, errors.Join(yamltest.Bind(t, other, badC()))),
					niceyaml.WithDetails(badB()),
				))),
			)),
			want: textNode("g.yaml:1:4: $.c: root",
				textNode("f.yaml:2:4: $.b: bad b"),
				textNode("g.yaml:1:4: $.c: bad c"),
			),
		},
		"branch with details around a bound join gives its place to its named children": {
			err: yamltest.Bind(t, source, errors.Join(
				yamltest.Bind(t, source, niceyaml.Invalid(
					yamltest.Bind(t, source, errors.Join(yamltest.Bind(t, other, badC()))),
					niceyaml.WithDetails(badB()),
				)),
				badA(),
			)),
			want: textForest(
				textNode("f.yaml:1:4: $.a: bad a"),
				textNode("f.yaml:2:4: $.b: bad b"),
				textNode("g.yaml:1:4: $.c: bad c"),
			),
		},
		"wrapper around a join of bindings keeps its own text": {
			err: fmt.Errorf("load: %w", errors.Join(
				yamltest.Bind(t, source, badB()),
				yamltest.Bind(t, source, badA()),
			)),
			want: textNode("load:",
				textNode("f.yaml:2:4: $.b: bad b"),
				textNode("f.yaml:1:4: $.a: bad a"),
			),
		},
		"wrapper around a bound join names the source on each branch": {
			err: fmt.Errorf("load: %w", yamltest.Bind(t, source, errors.Join(badB(), badA()))),
			want: textNode("load:",
				textNode("f.yaml:1:4: $.a: bad a"),
				textNode("f.yaml:2:4: $.b: bad b"),
			),
		},
		"wrapper with no text of its own around a join is the join": {
			err: fmt.Errorf("%w", errors.Join(badA(), badB())),
			want: textForest(
				textNode("@.a: bad a"),
				textNode("@.b: bad b"),
			),
		},
		"wrapper that puts text behind a binding keeps it on the line of the binding": {
			err: fmt.Errorf("%w (while loading)", yamltest.Bind(t, source,
				niceyaml.NewSummary("2 problems", badA(), badB()))),
			want: textNode("f.yaml: 2 problems (while loading)",
				textNode("1:4: $.a: bad a"),
				textNode("2:4: $.b: bad b"),
			),
		},
		"wrapper that rewrites the message of a binding keeps its text whole": {
			err: yamltest.UpperError{Err: yamltest.Bind(t, source,
				niceyaml.NewSummary("2 problems", badA(), badB()))},
			want: textNode(stringtest.JoinLF("F.YAML: 2 PROBLEMS", "F.YAML:1:4: $.A: BAD A", "F.YAML:2:4: $.B: BAD B"),
				textNode("1:4: $.a: bad a"),
				textNode("2:4: $.b: bad b"),
			),
		},
		"join of one error is that error": {
			err:  errors.Join(errors.Join(errors.New("boom"))),
			want: textNode("boom"),
		},
		"nested joins flatten into one forest": {
			err: errors.Join(
				errors.Join(errors.New("one"), errors.New("two")),
				nil,
				errors.New("three"),
			),
			want: textForest(
				textNode("one"),
				textNode("two"),
				textNode("three"),
			),
		},
		"error without a message and nested errors is a forest": {
			err: niceyaml.NewError("", niceyaml.WithDetails(
				niceyaml.NewError("one"),
				niceyaml.NewError("two"),
			)),
			want: textForest(
				textNode("one"),
				textNode("two"),
			),
		},
		"nil details are skipped": {
			err:  niceyaml.NewError("main", niceyaml.WithDetails(nil, niceyaml.NewError("one"), nil)),
			want: textNode("main", textNode("one")),
		},
		"nil errors a summary heads are skipped": {
			err: niceyaml.NewSummary("main", nil, niceyaml.NewError("one"), nil, niceyaml.NewError("two")),
			want: textNode("main",
				textNode("one"),
				textNode("two"),
			),
		},
		"rewritten message keeps its children": {
			err:  yamltest.RewriteError{Err: niceyaml.NewError("main", niceyaml.WithDetails(badA()))},
			want: textNode("rewritten", textNode("@.a: bad a")),
		},
		"rewritten bound message keeps its children with positions": {
			err: yamltest.Bind(
				t,
				source,
				yamltest.RewriteError{Err: niceyaml.NewError("main", niceyaml.WithDetails(badA()))},
			),
			want: textNode("f.yaml: rewritten",
				textNode("1:4: $.a: bad a"),
			),
		},
		"multi-line nested message stays whole": {
			err: yamltest.Bind(t, source, niceyaml.NewError("outer", niceyaml.WithDetails(
				niceyaml.NewError(
					stringtest.JoinLF("bad a", "  see docs"),
					niceyaml.AtPath(paths.Current().Child("a")),
				),
			))),
			want: textNode("f.yaml: outer",
				textNode(stringtest.JoinLF("1:4: $.a: bad a", "  see docs")),
			),
			multiLine: true,
		},
		"wrapper with two verbs over plain errors is one node": {
			err:  fmt.Errorf("parse path: %w: %w", errA, errB),
			want: textNode("parse path: first: second"),
		},
		"wrapper with two verbs goes on through its one branch with children": {
			err: fmt.Errorf("%w: %w", errA, niceyaml.NewError("parent", niceyaml.WithDetails(errB))),
			want: textNode("first: parent",
				textNode("second"),
			),
		},
		"wrapper with two verbs keeps its own text": {
			err: fmt.Errorf("parse path: %w; %w",
				niceyaml.NewError("x", niceyaml.WithDetails(errA)),
				niceyaml.NewError("y", niceyaml.WithDetails(errB)),
			),
			want: textNode("parse path: x; y",
				textNode("x", textNode("first")),
				textNode("y", textNode("second")),
			),
		},
		"multi-error of its own type keeps each branch": {
			err: violationsError{errA, errB},
			want: textNode("2 violations",
				textNode("first"),
				textNode("second"),
			),
		},
		"bound multi-error of its own type keeps each branch": {
			err: yamltest.Bind(t, source, violationsError{errA, errB}),
			want: textNode("f.yaml: 2 violations",
				textNode("first"),
				textNode("second"),
			),
		},
		"join below a wrapper yields one child per branch": {
			err: fmt.Errorf("while checking: %w", errors.Join(errA, errB)),
			want: textNode("while checking:",
				textNode("first"),
				textNode("second"),
			),
		},
		"children that add nothing leave no children": {
			err:  niceyaml.NewError("outer", niceyaml.WithDetails(niceyaml.NewError(""))),
			want: textNode("outer"),
		},
		"join at the root is textless": {
			err: errors.Join(errA, errB),
			want: textForest(
				textNode("first"),
				textNode("second"),
			),
		},
		"join with a leading nil branch is textless": {
			err: sparseJoinError{errs: []error{nil, errA, errB}},
			want: textForest(
				textNode("first"),
				textNode("second"),
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tree := niceyaml.NewErrorTree(tc.err)
			got := textTree(tree)
			assert.Equal(t, tc.want, got)
			assertRows(t, tc.err, tree)

			// The %+v verb of a bound error lists every nested error on a
			// line of its own before the excerpt, so that part has one line
			// per node of the tree. A wrapper around a binding has no
			// formatter of its own, so the check applies to a bound error
			// at the top.
			bound, ok := tc.err.(*niceyaml.SourceError) //nolint:errorlint // The value itself formats.
			if ok && !tc.multiLine {
				assert.Len(t, strings.Split(report(bound), "\n"), countNodes(got))
			}
		})
	}
}

func TestErrorRow_String(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		row  niceyaml.ErrorRow
		want string
	}{
		"zero": {
			want: "",
		},
		"text alone": {
			row:  niceyaml.ErrorRow{Text: "boom"},
			want: "boom",
		},
		"location alone keeps its colon": {
			row:  niceyaml.ErrorRow{Location: "cfg.yaml:3:6"},
			want: "cfg.yaml:3:6:",
		},
		"path alone keeps its colon": {
			row:  niceyaml.ErrorRow{Path: "$.a"},
			want: "$.a:",
		},
		"every part": {
			row: niceyaml.ErrorRow{
				Location: "cfg.yaml:3:6",
				Severity: "warning",
				Path:     "$.b.c",
				Text:     "old c",
			},
			want: "cfg.yaml:3:6: warning: $.b.c: old c",
		},
		"severity and text": {
			row:  niceyaml.ErrorRow{Severity: "warning", Text: "deprecated thing"},
			want: "warning: deprecated thing",
		},
		"place with a document": {
			row:  niceyaml.ErrorRow{Location: "cfg.yaml: document 2", Path: "$.x[*]", Text: "bad x"},
			want: "cfg.yaml: document 2: $.x[*]: bad x",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.row.String())
			assert.Equal(t, tc.want == "", tc.row.IsZero())
			assert.Equal(t, tc.want, niceyaml.ErrorTree{Row: tc.row}.Label())
		})
	}
}

func TestErrorTree_Row(t *testing.T) {
	t.Parallel()

	source := errorTreeSource()
	nested := niceyaml.NewSourceFromString("a: 1\nb:\n  c: 2\n",
		niceyaml.WithName("deploy/environments/production/cfg.yaml"))
	second := yamltest.Documents(t, niceyaml.NewSourceFromString("a: 1\n---\na: 1\nb: 2\n", niceyaml.WithName("m.yaml")), 2)[1]
	other := niceyaml.NewSourceFromString("c: 3\n", niceyaml.WithName("g.yaml"))

	badA, badB, badC := badKeyError("a"), badKeyError("b"), badKeyError("c")
	warnAt := func(msg, key string) error {
		return niceyaml.NewWarning(msg, niceyaml.AtPath(paths.Current().Child(key)))
	}

	tcs := map[string]struct {
		err error
		// The rows of the nodes, in the order that [niceyaml.ErrorTree.All]
		// yields them.
		want []niceyaml.ErrorRow
	}{
		"bound warning": {
			err: yamltest.Bind(t, nested,
				niceyaml.NewWarning("old c", niceyaml.AtPath(paths.Current().Child("b", "c")))),
			want: []niceyaml.ErrorRow{
				{
					Location: "deploy/environments/production/cfg.yaml:3:6",
					Severity: "warning",
					Path:     "$.b.c",
					Text:     "old c",
				},
			},
		},
		"bound children drop the name": {
			err: yamltest.Bind(t, source, niceyaml.NewSummary("2 problems", badA(), badB())),
			want: []niceyaml.ErrorRow{
				{Location: "f.yaml", Text: "2 problems"},
				{Location: "1:4", Path: "$.a", Text: "bad a"},
				{Location: "2:4", Path: "$.b", Text: "bad b"},
			},
		},
		"binding without a position names its document": {
			err: second.Bind(niceyaml.NewError("bad x", niceyaml.AtPath(paths.Current().Child("x").IndexAll()))),
			want: []niceyaml.ErrorRow{
				{Location: "m.yaml: document 2", Path: "$.x[*]", Text: "bad x"},
			},
		},
		"unbound warning": {
			err: warnAt("old", "a"),
			want: []niceyaml.ErrorRow{
				{Severity: "warning", Path: "@.a", Text: "old"},
			},
		},
		"unbound warning without a path": {
			err: niceyaml.NewWarning("deprecated thing"),
			want: []niceyaml.ErrorRow{
				{Severity: "warning", Text: "deprecated thing"},
			},
		},
		"wrapper around a binding keeps its text in the text": {
			err: fmt.Errorf("load item: %w", yamltest.Bind(t, source, warnAt("old", "a"))),
			want: []niceyaml.ErrorRow{
				{Location: "f.yaml:1:4", Severity: "warning", Path: "$.a", Text: "load item: old"},
			},
		},
		"heading over warnings does not carry the label": {
			err: fmt.Errorf("lint warning: found:\n%w", errors.Join(
				yamltest.Bind(t, source, warnAt("old a", "a")),
				yamltest.Bind(t, source, warnAt("old b", "b")),
			)),
			want: []niceyaml.ErrorRow{
				{Text: "lint warning: found:"},
				{Location: "f.yaml:1:4", Severity: "warning", Path: "$.a", Text: "old a"},
				{Location: "f.yaml:2:4", Severity: "warning", Path: "$.b", Text: "old b"},
			},
		},
		"warning detail carries the label": {
			err: yamltest.Bind(t, source, niceyaml.NewError("outer",
				niceyaml.AtPath(paths.Current().Child("b")),
				niceyaml.WithDetails(warnAt("see a", "a")),
			)),
			want: []niceyaml.ErrorRow{
				{Location: "f.yaml:2:4", Path: "$.b", Text: "outer"},
				{Location: "1:4", Severity: "warning", Path: "$.a", Text: "see a"},
			},
		},
		"wrapper that rewrites the line keeps it whole in the text": {
			err: yamltest.UpperError{Err: yamltest.Bind(t, source, warnAt("old", "a"))},
			want: []niceyaml.ErrorRow{
				{Text: "F.YAML:1:4: WARNING: $.A: OLD"},
			},
		},
		"located error above a binding of another source keeps its line in the text": {
			err: yamltest.Bind(t, source, niceyaml.Invalid(
				yamltest.Bind(t, other, badC()),
				niceyaml.AtPath(paths.Current().Child("a")),
			)),
			want: []niceyaml.ErrorRow{
				{Location: "f.yaml:1:4", Path: "$.a", Text: "g.yaml:1:4: $.c: bad c"},
			},
		},
		"name that holds the label stays in the location": {
			err: yamltest.Bind(t,
				niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName("warning:x.yaml")),
				warnAt("old", "a")),
			want: []niceyaml.ErrorRow{
				{Location: "warning:x.yaml:1:4", Severity: "warning", Path: "$.a", Text: "old"},
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tree := niceyaml.NewErrorTree(tc.err)

			var got []niceyaml.ErrorRow

			for node := range tree.All() {
				got = append(got, node.Row)
			}

			assert.Equal(t, tc.want, got)
			assertRows(t, tc.err, tree)
		})
	}
}

func TestErrorTree_New_ExcerptsOff(t *testing.T) {
	t.Parallel()

	const text = "password: hunter2\nport: 0\n"

	// The tree holds the message of each error and no excerpt, so it
	// reads the same whether or not an error may show the text of its
	// source.
	shown := niceyaml.NewSourceFromString(text, niceyaml.WithName("secrets.yaml"))
	secrets := niceyaml.NewSourceFromString(text, niceyaml.WithName("secrets.yaml"), niceyaml.WithExcerpts(false))

	badPort := niceyaml.NewError("bad port", niceyaml.AtPath(paths.Doc().Child("port")))
	short := niceyaml.NewError("too short", niceyaml.AtPath(paths.Doc().Child("password")))

	tcs := map[string]struct {
		err  error
		want niceyaml.ErrorTree
	}{
		"one error": {
			err:  badPort,
			want: textNode("secrets.yaml:2:7: $.port: bad port"),
		},
		"a summary over two errors": {
			err: niceyaml.NewSummary("2 problems", badPort, short),
			want: textNode("secrets.yaml: 2 problems",
				textNode("1:11: $.password: too short"),
				textNode("2:7: $.port: bad port"),
			),
		},
		"an error with a detail": {
			err: niceyaml.NewError("bad port", niceyaml.AtPath(paths.Doc().Child("port")), niceyaml.WithDetails(short)),
			want: textNode("secrets.yaml:2:7: $.port: bad port",
				textNode("1:11: $.password: too short"),
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tree := niceyaml.NewErrorTree(yamltest.Bind(t, secrets, tc.err))

			assert.Equal(t, tc.want, textTree(tree))
			assert.Equal(t, textTree(niceyaml.NewErrorTree(yamltest.Bind(t, shown, tc.err))), textTree(tree))

			// A renderer that reads the tree asks the binding of each node
			// for its excerpt, and the binding has none to give.
			for node := range tree.All() {
				require.NotNil(t, node.Bound)
				assert.False(t, node.Bound.Source().ShowsExcerpts())

				_, ok := node.Bound.Excerpt(niceyaml.WithContextLines(0))
				assert.False(t, ok)
			}
		})
	}
}

func TestErrorTree_Bound(t *testing.T) {
	t.Parallel()

	source := errorTreeSource()

	badA, badB := badKeyError("a"), badKeyError("b")

	bind := func(t *testing.T, err error) *niceyaml.SourceError {
		t.Helper()

		return yamltest.RequireSourceError(t, yamltest.Bind(t, source, err))
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

				return tree{err: err, want: []node{{text: "f.yaml:1:4: $.a: load: bad a", err: err, bound: bound}}}
			},
		},
		"children of a binding": {
			build: func(t *testing.T) tree {
				t.Helper()

				bound := bind(t, niceyaml.NewSummary("2 problems", badB(), badA()))
				kids := yamltest.ProblemsOf(bound)
				require.Len(t, kids, 2)

				// The tree sorts the children by position, so the second
				// error of the summary comes first.
				return tree{err: bound, want: []node{
					{text: "f.yaml: 2 problems", err: bound, bound: bound},
					{text: "1:4: $.a: bad a", err: kids[0].Bound, bound: kids[0].Bound},
					{text: "2:4: $.b: bad b", err: kids[1].Bound, bound: kids[1].Bound},
				}}
			},
		},
		"branches of a bound join": {
			build: func(t *testing.T) tree {
				t.Helper()

				bound := bind(t, errors.Join(badA(), badB()))
				kids := yamltest.ProblemsOf(bound)
				require.Len(t, kids, 2)

				return tree{err: bound, want: []node{
					{text: "f.yaml:1:4: $.a: bad a", err: kids[0].Bound, bound: kids[0].Bound},
					{text: "f.yaml:2:4: $.b: bad b", err: kids[1].Bound, bound: kids[1].Bound},
				}}
			},
		},
		"Error with details around a binding": {
			build: func(t *testing.T) tree {
				t.Helper()

				bound := bind(t, badA())
				reason := errors.New("see docs")
				err := niceyaml.Invalid(bound, niceyaml.WithDetails(reason))

				return tree{err: err, want: []node{
					{text: "f.yaml:1:4: $.a: bad a", err: err, bound: bound},
					{text: "see docs", err: reason},
				}}
			},
		},
		"located Error above a binding": {
			build: func(t *testing.T) tree {
				t.Helper()

				// The binding resolved its location already, so the node
				// reports the binding, and the location of the Error does
				// not show.
				bound := bind(t, badA())
				err := niceyaml.Invalid(bound, niceyaml.AtPath(paths.Current().Child("b")))

				return tree{err: err, want: []node{{text: "f.yaml:1:4: $.a: bad a", err: err, bound: bound}}}
			},
		},
		"located Error above a binding without a location": {
			build: func(t *testing.T) tree {
				t.Helper()

				// No source resolved the location of the Error, so the node
				// has no binding to report it through.
				err := niceyaml.Invalid(bind(t, errors.New("bad")), niceyaml.AtPath(paths.Current().Child("b")))

				return tree{err: err, want: []node{{text: "@.b: f.yaml: bad", err: err}}}
			},
		},
		"located Error with the path of a binding whose path did not resolve": {
			build: func(t *testing.T) tree {
				t.Helper()

				// The row of the binding shows the path of the Error
				// already, so the node reads as the binding.
				far := paths.Doc().Child("a").ChildAll()
				bound := bind(t, niceyaml.NewError("far", niceyaml.AtPath(far)))
				err := niceyaml.Invalid(bound, niceyaml.AtPath(far))

				return tree{err: err, want: []node{{text: "f.yaml: $.a.*: far", err: err, bound: bound}}}
			},
		},
		"located Errors with the path of a binding whose path did not resolve": {
			build: func(t *testing.T) tree {
				t.Helper()

				// A helper and its caller both locate the binding at its
				// path, so the node reads as the binding.
				far := paths.Doc().Child("a").ChildAll()
				bound := bind(t, niceyaml.NewError("far", niceyaml.AtPath(far)))
				err := niceyaml.Locate(niceyaml.Locate(bound, niceyaml.AtPath(far)), niceyaml.AtPath(far))

				return tree{err: err, want: []node{{text: "f.yaml: $.a.*: far", err: err, bound: bound}}}
			},
		},
		"located Error around a wrapper of a located Error with the path of a binding": {
			build: func(t *testing.T) tree {
				t.Helper()

				// The text of the wrapper follows the path of the binding, as
				// it does after the bind.
				far := paths.Doc().Child("a").ChildAll()
				bound := bind(t, niceyaml.NewError("far", niceyaml.AtPath(far)))
				err := niceyaml.Locate(
					fmt.Errorf("check: %w", niceyaml.Locate(bound, niceyaml.AtPath(far))),
					niceyaml.AtPath(far),
				)

				return tree{err: err, want: []node{{text: "f.yaml: $.a.*: check: far", err: err, bound: bound}}}
			},
		},
		"located Error with the path of a binding above one at its parent": {
			build: func(t *testing.T) tree {
				t.Helper()

				// A bind takes the location of the outer Error, so the
				// location of the inner one does not show before the bind
				// either.
				far := paths.Doc().Child("a").ChildAll()
				bound := bind(t, niceyaml.NewError("far", niceyaml.AtPath(far)))
				err := niceyaml.Locate(
					niceyaml.Locate(bound, niceyaml.AtPath(paths.Doc().Child("a"))),
					niceyaml.AtPath(far),
				)

				return tree{err: err, want: []node{{text: "f.yaml: $.a.*: far", err: err, bound: bound}}}
			},
		},
		"located Error with the path of a binding above one at a position": {
			build: func(t *testing.T) tree {
				t.Helper()

				far := paths.Doc().Child("a").ChildAll()
				bound := bind(t, niceyaml.NewError("far", niceyaml.AtPath(far)))
				err := niceyaml.Locate(
					niceyaml.Locate(bound, niceyaml.AtPosition(position.New(1, 0))),
					niceyaml.AtPath(far),
				)

				return tree{err: err, want: []node{{text: "f.yaml: $.a.*: far", err: err, bound: bound}}}
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
				assert.Equal(t, n.text, got[i].Label())
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

	assert.Empty(t, got.Label())
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
			tree: textNode("boom"),
			want: []string{"boom"},
		},
		"each node comes before the nodes below it": {
			tree: textNode("root",
				textNode("a", textNode("a1"), textNode("a2")),
				textNode("b", textNode("b1")),
			),
			want: []string{"root", "a", "a1", "a2", "b", "b1"},
		},
		"root with no text gives its place to its children": {
			tree: textForest(textNode("a"), textNode("b")),
			want: []string{"a", "b"},
		},
		"node with no text gives its place to its children": {
			tree: textNode("root",
				textForest(textNode("x"), textNode("y")),
				textNode("z"),
			),
			want: []string{"root", "x", "y", "z"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var got []string

			for node := range tc.tree.All() {
				got = append(got, node.Label())
			}

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestErrorTree_All_PrintedOrder(t *testing.T) {
	t.Parallel()

	source := errorTreeSource()

	err := errors.Join(
		yamltest.Bind(t, source, niceyaml.NewSummary("2 problems",
			yamltest.ErrorAt("bad b", "b"),
			yamltest.ErrorAt("bad a", "a"),
		)),
		errors.New("read g.yaml: no such file"),
	)

	var rows []string

	for node := range niceyaml.NewErrorTree(err).All() {
		rows = append(rows, node.Label())
	}

	// The tree [niceyaml.FormatError] prints holds one row per node, in the
	// same order, each behind its connector.
	tree, _, _ := strings.Cut(niceyaml.FormatError(err, niceyaml.WithContextLines(0)), "\n\n")
	printed := strings.Split(tree, "\n")
	require.Len(t, printed, len(rows))

	for i, row := range rows {
		assert.True(t, strings.HasSuffix(printed[i], row), "%q does not end with %q", printed[i], row)
	}
}

func TestErrorTree_Stops(t *testing.T) {
	t.Parallel()

	// The loop of each case ends at the node whose text matches stop.
	tcs := map[string]struct {
		seq  iter.Seq[niceyaml.ErrorTree]
		stop string
		want []string
	}{
		"All": {
			seq: textNode("root",
				textNode("a", textNode("a1")),
				textNode("b"),
			).All(),
			stop: "a1",
			want: []string{"root", "a", "a1"},
		},
		"Problems": {
			seq: niceyaml.NewErrorTree(errors.Join(
				errors.New("first"),
				errors.New("second"),
				errors.New("third"),
			)).Problems(),
			stop: "second",
			want: []string{"first", "second"},
		},
		"Problems of an error": {
			seq: niceyaml.Problems(errors.Join(
				niceyaml.NewError("bad a", niceyaml.AtPath(paths.Doc().Child("a"))),
				niceyaml.NewError("bad b", niceyaml.AtPath(paths.Doc().Child("b"))),
			)),
			stop: "$.a: bad a",
			want: []string{"$.a: bad a"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var got []string

			for node := range tc.seq {
				got = append(got, node.Label())
				if node.Label() == tc.stop {
					break
				}
			}

			assert.Equal(t, tc.want, got)
		})
	}
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

		rows = append(rows, problem.Label())
	}

	return rows
}

func TestErrorTree_Problems(t *testing.T) {
	t.Parallel()

	source := errorTreeSource()

	badA, badB := badKeyError("a"), badKeyError("b")

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
			err: yamltest.Bind(t, source, niceyaml.Invalid(errNoMatch, niceyaml.WithDetails(
				errors.New("no schema directive"),
			))),
			want: []string{"no matching schema"},
		},
		"bound error with two reasons": {
			err: yamltest.Bind(t, source, niceyaml.Invalid(errNoMatch, niceyaml.WithDetails(
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
					yamltest.ErrorAt("first declared here", "a"),
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
				niceyaml.Invalid(errors.Join(badA(), badB()), niceyaml.WithDetails(errors.New("see docs"))),
			))),
			want: []string{"ports conflict"},
		},
		"detail above a join of one and a reason": {
			err: yamltest.Bind(t, source, niceyaml.NewError("ports conflict", niceyaml.WithDetails(
				niceyaml.Invalid(errors.Join(badA()), niceyaml.WithDetails(errors.New("see docs"))),
			))),
			want: []string{"ports conflict"},
		},
		"unbound detail above a join and a reason": {
			err: niceyaml.NewError("ports conflict", niceyaml.WithDetails(
				niceyaml.Invalid(errors.Join(badA(), badB()), niceyaml.WithDetails(errors.New("see docs"))),
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
				yamltest.ErrorAt("missing", "c"),
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
			err: niceyaml.Invalid(
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
			err: niceyaml.Invalid(errNoMatch, niceyaml.WithDetails(
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
				yamltest.Bind(t, source, niceyaml.Invalid(errNoMatch, niceyaml.WithDetails(
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

	source := niceyaml.NewSourceFromString(stringtest.LinesLF(
		"a: 1",
		"b: 2",
		"c: 3",
	), niceyaml.WithName("f.yaml"))

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

	source := errorTreeSource()

	build := func() error {
		return niceyaml.NewSummary("2 problems",
			niceyaml.NewError(
				"bad a",
				niceyaml.AtPath(paths.Current().Child("a")),
				niceyaml.WithDetails(
					errors.New("reason"),
					yamltest.ErrorAt("see b", "b"),
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
			out = append(out, fmt.Sprintf("%s%s %t", indent, child.Label(), child.Detail))
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
		return niceyaml.NewError("conflict", niceyaml.WithDetails(niceyaml.Invalid(
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

			problem := yamltest.OnlyProblem(t, tc.err)
			assert.Equal(t, "conflict", problem.Message())

			var details []string

			for _, child := range problem.Children {
				assert.True(t, child.Detail, child.Label())

				details = append(details, child.Message())
			}

			assert.Equal(t, []string{"reason", "first", "second"}, details)
		})
	}
}

func TestErrorTree_Problems_Reasons(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName("f.yaml"))

	err := yamltest.Bind(t, source, niceyaml.Invalid(
		errors.New("no matching schema"),
		niceyaml.WithDetails(errors.New("no schema directive"), errors.New("no catalog entry matches")),
	))

	problem := yamltest.OnlyProblem(t, err)

	// The reasons stay below the node that yields.
	assert.Equal(t, textNode("f.yaml: no matching schema",
		textNode("no schema directive"),
		textNode("no catalog entry matches"),
	), textTree(problem))
	require.ErrorIs(t, problem.Err, err)
}

func TestErrorTree_Problems_Err(t *testing.T) {
	t.Parallel()

	source := errorTreeSource()
	a := paths.Current().Child("a")
	b := paths.Current().Child("b")

	own := &ruleError{id: "own"}
	// The detail names where the value was first declared and wraps a rule
	// of its own, which is never the rule of the problem it explains.
	first := niceyaml.Invalid(&ruleError{id: "first-use"}, niceyaml.AtPath(a))
	detailed := niceyaml.WithDetails(first)

	// Each case builds one problem, and want is the rule [errors.As] finds
	// on the Err of its row, or empty when the problem wraps none.
	tcs := map[string]struct {
		err  error
		want string
	}{
		"rule of the problem beside a rule of its detail": {
			err:  yamltest.Bind(t, source, niceyaml.Invalid(own, niceyaml.AtPath(b), detailed)),
			want: "own",
		},
		"no rule beside a rule of its detail": {
			err: yamltest.Bind(t, source, niceyaml.NewError("conflict", niceyaml.AtPath(b), detailed)),
		},
		"wrapper below the binding": {
			err: yamltest.Bind(t, source,
				fmt.Errorf("check: %w", niceyaml.NewError("conflict", niceyaml.AtPath(b), detailed))),
		},
		"rule in a wrapper above the binding": {
			err: fmt.Errorf("%w: %w", own,
				yamltest.Bind(t, source, niceyaml.NewError("conflict", niceyaml.AtPath(b), detailed))),
			want: "own",
		},
		"bound to no source": {
			err: niceyaml.NewError("conflict", niceyaml.AtPath(b), detailed),
		},
		"bound to no source below a wrapper": {
			err: fmt.Errorf("check: %w", niceyaml.NewError("conflict", niceyaml.AtPath(b), detailed)),
		},
		"rule of a problem bound to no source": {
			err:  niceyaml.Unchecked(fmt.Errorf("%w: read rules: %w", own, fs.ErrNotExist), detailed),
			want: "own",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			problem := yamltest.OnlyProblem(t, tc.err)

			got := ""
			if rule, ok := errors.AsType[*ruleError](problem.Err); ok {
				got = rule.id
			}

			assert.Equal(t, tc.want, got)

			// The detail keeps its own rule, and the row reads it there.
			require.Len(t, problem.Children, 1)
			assert.True(t, problem.Children[0].Detail)

			rule, ok := errors.AsType[*ruleError](problem.Children[0].Err)
			require.True(t, ok)
			assert.Equal(t, "first-use", rule.id)
		})
	}
}

func TestErrorTree_Problems_WrappedJoin(t *testing.T) {
	t.Parallel()

	doc := errorTreeHours(t)

	hours := yamltest.At(t, doc, paths.Current().Child("hours"))

	early := errors.New("opens too early")
	late := errors.New("closes too late")

	tcs := map[string]struct {
		err  error
		want []string
	}{
		"unbound": {
			err:  niceyaml.Invalid(errors.Join(early, late)),
			want: []string{"opens too early", "closes too late"},
		},
		"bound at the root": {
			err:  doc.Bind(niceyaml.Invalid(errors.Join(early, late))),
			want: []string{"f.yaml: opens too early", "f.yaml: closes too late"},
		},
		"bound through a scoped Node": {
			err:  hours.Bind(niceyaml.Invalid(errors.Join(early, late))),
			want: []string{"f.yaml:1:1: $.hours: opens too early", "f.yaml:1:1: $.hours: closes too late"},
		},
		"rebased": {
			err:  niceyaml.Rebase(niceyaml.Invalid(errors.Join(early, late)), paths.Current().Child("hours")),
			want: []string{"@.hours: opens too early", "@.hours: closes too late"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// Invalid declares each branch of the join a problem the
			// document is at fault for, and each keeps its text.
			var got []string

			for problem := range niceyaml.NewErrorTree(tc.err).Problems() {
				assert.True(t, problem.IsInvalid(), problem.Label())

				got = append(got, problem.Label())
			}

			assert.Equal(t, tc.want, got)
		})
	}
}

// errorTreeHours returns the root Node of a document named f.yaml whose
// hours mapping holds the key open.
func errorTreeHours(t *testing.T) *niceyaml.Node {
	t.Helper()

	return yamltest.FirstDocument(t, stringtest.LinesLF("hours:", "  open: a"), niceyaml.WithName("f.yaml"))
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

	doc := errorTreeHours(t)

	var cfg struct {
		Hours reasonedHours `yaml:"hours"`
	}

	// The decode rebases the error and its reasons under $.hours, which
	// locates the error at the value, a mapping that points at its key.
	// The reasons explain it and take no location.
	problem := yamltest.OnlyProblem(t, doc.DecodeInto(t.Context(), &cfg))
	require.NotNil(t, problem.Bound)

	assert.Equal(t, "hours invalid", problem.Bound.Message())
	require.Len(t, problem.Children, 2)

	for _, reason := range problem.Children {
		assert.True(t, reason.Detail)

		_, ok := reason.Bound.Position()
		assert.False(t, ok)
	}

	assert.Equal(t, position.New(0, 0), yamltest.PositionOf(t, problem.Bound))
}

func TestErrorTree_Problems_Scope(t *testing.T) {
	t.Parallel()

	doc := errorTreeHours(t)

	hours := yamltest.At(t, doc, paths.Current().Child("hours"))

	// The binding joins the scope in front of each path, and the summary
	// is a heading, so it yields no row.
	bound := hours.Bind(niceyaml.NewSummary("2 problems",
		yamltest.ErrorAt("bad open", "open"),
		yamltest.ErrorAt("no close", "close"),
	))

	// Each row reads as the position, the path, and the message.
	var got []string

	for problem := range niceyaml.NewErrorTree(bound).Problems() {
		require.NotNil(t, problem.Bound)

		pos := yamltest.PositionOf(t, problem.Bound)
		path := yamltest.PathOf(t, problem.Bound)
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
				Row:      niceyaml.ErrorRow{Text: "no matching schema"},
				Children: []niceyaml.ErrorTree{{Row: niceyaml.ErrorRow{Text: "no schema directive"}, Detail: true}},
			},
			want: []string{"no matching schema"},
		},
		"root with no text gives its place to its children": {
			tree: niceyaml.ErrorTree{Children: []niceyaml.ErrorTree{
				{
					Row:      niceyaml.ErrorRow{Text: "first"},
					Children: []niceyaml.ErrorTree{{Row: niceyaml.ErrorRow{Text: "reason"}, Detail: true}},
				},
				{Row: niceyaml.ErrorRow{Text: "second"}},
			}},
			want: []string{"first", "second"},
		},
		"node above children that are no details heads them": {
			tree: niceyaml.ErrorTree{
				Row: niceyaml.ErrorRow{Text: "2 problems"},
				Children: []niceyaml.ErrorTree{
					{Row: niceyaml.ErrorRow{Text: "$.a: bad a"}},
					{
						Row:      niceyaml.ErrorRow{Text: "too old"},
						Children: []niceyaml.ErrorTree{{Row: niceyaml.ErrorRow{Text: "see docs"}, Detail: true}},
					},
				},
			},
			want: []string{"$.a: bad a", "too old"},
		},
		"heading passes over its own details": {
			tree: niceyaml.ErrorTree{
				Row: niceyaml.ErrorRow{Text: "2 problems"},
				Children: []niceyaml.ErrorTree{
					{Row: niceyaml.ErrorRow{Text: "see docs"}, Detail: true},
					{Row: niceyaml.ErrorRow{Text: "bad a"}},
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

func TestErrorTree_IsInvalid(t *testing.T) {
	t.Parallel()

	source := errorTreeSource()

	badA, badB := badKeyError("a"), badKeyError("b")

	errRead := fmt.Errorf("read g.yaml: %w", fs.ErrPermission)

	// Want is what IsInvalid reports for the tree. Each row of wantNodes,
	// for a case that lists them, is the message of a node and what
	// IsInvalid reports for it.
	tcs := map[string]struct {
		err       error
		wantNodes []string
		want      bool
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
			wantNodes: []string{
				"2 problems false",
				"bad a true",
				"read g.yaml: permission denied false",
			},
		},
		"summary below a mixed join": {
			err: errors.Join(niceyaml.NewSummary("2 violations", badA(), badB()), errRead),
			wantNodes: []string{
				"2 violations true",
				"bad a true",
				"bad b true",
				"read g.yaml: permission denied false",
			},
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
			wantNodes: []string{
				"load false",
				"bad a true",
				"read g.yaml: permission denied false",
			},
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
		"wrapper with several verbs over an ended context and an invalid problem": {
			// The context error reports that the call stopped, not a fault
			// of the document, so the wrapper declares nothing. Invalid
			// declares nothing for it either.
			err: fmt.Errorf("%w: %w", context.Canceled, badA()),
		},
		"wrapper with several verbs over an invalid problem and a wrapped deadline": {
			err: fmt.Errorf("%w; %w", niceyaml.NewError("too old"), fmt.Errorf("fetch: %w", context.DeadlineExceeded)),
		},
		"read error with an invalid detail": {
			// A detail explains the problem and decides nothing, and it
			// answers for itself.
			err: niceyaml.Unchecked(errRead, niceyaml.AtPath(paths.Current().Child("a")), niceyaml.WithDetails(badB())),
			wantNodes: []string{
				"read g.yaml: permission denied false",
				"bad b true",
			},
		},
		"invalid problem with a detail that is a read error": {
			err: niceyaml.NewError("ports conflict", niceyaml.WithDetails(errRead)),
			wantNodes: []string{
				"ports conflict true",
				"read g.yaml: permission denied false",
			},
			want: true,
		},
		"invalid problem with a detail that heads a read error": {
			// The detail answers for the problems it heads.
			err: niceyaml.NewError("ports conflict", niceyaml.WithDetails(
				niceyaml.NewSummary("2 reasons", badA(), errRead),
			)),
			wantNodes: []string{
				"ports conflict true",
				"2 reasons false",
				"bad a true",
				"read g.yaml: permission denied false",
			},
			want: true,
		},
		"Invalid over a summary of bound errors": {
			// A problem that is bound already takes the mark as any other
			// problem the Error heads does.
			err: niceyaml.Invalid(niceyaml.NewSummary("2 problems",
				yamltest.Bind(t, source, niceyaml.Rebase(errRead, paths.Current().Child("a"))),
				yamltest.Bind(t, source, errors.New("too many keys")),
			)),
			wantNodes: []string{
				"2 problems true",
				"read g.yaml: permission denied true",
				"too many keys true",
			},
			want: true,
		},
		"summary of bound errors": {
			err: niceyaml.NewSummary("2 problems",
				yamltest.Bind(t, source, niceyaml.Rebase(errRead, paths.Current().Child("a"))),
				yamltest.Bind(t, source, errors.New("too many keys")),
			),
			wantNodes: []string{
				"2 problems false",
				"read g.yaml: permission denied false",
				"too many keys false",
			},
		},
	}

	// A heading that a wrapper makes of a join does not have a message, so
	// its row names its context.
	answers := func(tree niceyaml.ErrorTree) []string {
		var out []string

		for node := range tree.All() {
			text := node.Message()
			if text == "" {
				text = node.Context()
			}

			out = append(out, fmt.Sprintf("%s %t", text, node.IsInvalid()))
		}

		return out
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			unbound := niceyaml.NewErrorTree(tc.err)
			bound := niceyaml.NewErrorTree(yamltest.Bind(t, source, tc.err))

			assert.Equal(t, tc.want, unbound.IsInvalid(), "unbound")
			assert.Equal(t, tc.want, bound.IsInvalid(), "bound")

			if tc.wantNodes != nil {
				assert.Equal(t, tc.wantNodes, answers(unbound), "unbound")

				// A binding orders its children by position, so the rows
				// hold in any order.
				assert.ElementsMatch(t, tc.wantNodes, answers(bound), "bound")
			}
		})
	}
}

func TestErrorTree_IsInvalid_HandBuilt(t *testing.T) {
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
			tree: textNode("bad a"),
		},
		"heading over invalid problems": {
			tree: niceyaml.ErrorTree{
				Row: niceyaml.ErrorRow{Text: "2 problems"},
				Children: []niceyaml.ErrorTree{
					{Row: niceyaml.ErrorRow{Text: "bad a"}, Err: niceyaml.NewError("bad a")},
					{Row: niceyaml.ErrorRow{Text: "bad b"}, Err: niceyaml.NewError("bad b")},
				},
			},
			want: true,
		},
		"heading answers through its problems": {
			// The Err of a heading decides nothing.
			tree: niceyaml.ErrorTree{
				Row: niceyaml.ErrorRow{Text: "2 problems"},
				Err: niceyaml.NewError("2 problems"),
				Children: []niceyaml.ErrorTree{
					{Row: niceyaml.ErrorRow{Text: "bad a"}, Err: niceyaml.NewError("bad a")},
					{Row: niceyaml.ErrorRow{Text: "read g.yaml: permission denied"}, Err: errRead},
				},
			},
		},
		"problem above an invalid detail": {
			tree: niceyaml.ErrorTree{
				Row: niceyaml.ErrorRow{Text: "read g.yaml: permission denied"},
				Err: errRead,
				Children: []niceyaml.ErrorTree{
					{Row: niceyaml.ErrorRow{Text: "bad a"}, Err: niceyaml.NewError("bad a"), Detail: true},
				},
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.tree.IsInvalid())
		})
	}
}

func TestErrorTree_MessageContextAndPath(t *testing.T) {
	t.Parallel()

	source := errorTreeSource()

	pathA := paths.Current().Child("a")
	pathB := paths.Current().Child("b")

	// A wildcard path does not resolve to one node.
	farPath := paths.Doc().Child("a").ChildAll()

	badA, badB := badKeyError("a"), badKeyError("b")
	withDetail := func() *niceyaml.Error {
		return badA().With(niceyaml.WithDetails(yamltest.ErrorAt("see b", "b")))
	}

	problems := func(err error) []niceyaml.ErrorTree {
		got := slices.Collect(niceyaml.NewErrorTree(err).Problems())
		require.NotEmpty(t, got)

		return got
	}

	// DetailOf returns the first detail of the first problem of err.
	detailOf := func(err error) niceyaml.ErrorTree {
		detail := problems(err)[0].Children[0]
		require.True(t, detail.Detail)

		return detail
	}

	_, errMissing := niceyaml.NewSourceFromFS(fstest.MapFS{}, "missing.yaml")
	require.Error(t, errMissing)

	// A located Error keeps its location above a binding of another
	// source, and above a binding whose location did not resolve.
	included := niceyaml.NewSourceFromString("key: 1\nport: 2\n", niceyaml.WithName("inc.yaml"))
	badKey, badPort := badKeyError("key"), badKeyError("port")
	atB := niceyaml.AtPath(pathB)

	// The wrapper does not keep the line of the binding that it wraps.
	rewrite := func(err error) error { return rewrittenError{msg: "config is invalid", err: err} }

	// The binding of a join binds the wrapper around a binding again, so
	// the first problem of the join holds a binding that adopted one.
	rewritten := yamltest.ProblemsOf(yamltest.Bind(t, included, errors.Join(
		rewrite(yamltest.Bind(t, included, badKey())),
		badPort(),
	)))[0].Bound
	require.NotNil(t, rewritten)

	// The wrapper around the binding has the text of the wrapper inside
	// it, and the binding does not resolve a location.
	rewrittenTwice := yamltest.ProblemsOf(yamltest.Bind(t, included, errors.Join(
		rewrite(yamltest.Bind(t, included, rewrite(niceyaml.NewError("no place")))),
		badPort(),
	)))[1].Bound
	require.NotNil(t, rewrittenTwice)

	check := func(err error) error { return fmt.Errorf("check: %w", err) }
	oldKey := func() error {
		return niceyaml.NewWarning("old key", niceyaml.AtPath(paths.Current().Child("key")))
	}
	checkedSummary := func(src *niceyaml.Source, errs ...error) error {
		return yamltest.Bind(t, src, check(niceyaml.NewSummary("some problems", errs...)))
	}
	checkedJoin := func(src *niceyaml.Source, errs ...error) error {
		return yamltest.Bind(t, src, check(errors.Join(errs...)))
	}

	oldA := func() error {
		return niceyaml.NewWarning("old a", niceyaml.AtPath(paths.Doc().Child("a")))
	}

	tcs := map[string]struct {
		want        string
		wantContext string
		wantPath    string
		node        niceyaml.ErrorTree
		wantWarning bool
	}{
		"bound root": {
			node:     niceyaml.NewErrorTree(yamltest.Bind(t, source, badA())),
			want:     "bad a",
			wantPath: "$.a",
		},
		"bound child of a summary": {
			node:     problems(yamltest.Bind(t, source, niceyaml.NewSummary("2 problems", badA(), badB())))[1],
			want:     "bad b",
			wantPath: "$.b",
		},
		"unbound located Error": {
			node:     niceyaml.NewErrorTree(badA()),
			want:     "bad a",
			wantPath: "@.a",
		},
		"unbound violation under a summary": {
			// A schema validation of a decoded value returns violations
			// in this shape, with no source to bind them to.
			node: problems(niceyaml.NewSummary("2 schema violations",
				niceyaml.Invalid(errors.New(`expected "integer"`), niceyaml.AtPath(pathA)),
				niceyaml.Invalid(errors.New(`expected "string"`), niceyaml.AtPath(pathB)),
			))[1],
			want:     `expected "string"`,
			wantPath: "@.b",
		},
		"rebased Error with no location of its own": {
			node:     niceyaml.NewErrorTree(niceyaml.Rebase(niceyaml.NewError("never closes"), pathA)),
			want:     "never closes",
			wantPath: "@.a",
		},
		"plain error": {
			node: niceyaml.NewErrorTree(errors.New("too old")),
			want: "too old",
		},
		"unreadable file": {
			node: niceyaml.NewErrorTree(errMissing),
			want: "read file: open missing.yaml: file does not exist",
		},
		"wrapper around a binding": {
			// The message leaves out the location of the binding and the
			// text the wrapper added, which is the context.
			node:        niceyaml.NewErrorTree(fmt.Errorf("load config: %w", yamltest.Bind(t, source, badA()))),
			want:        "bad a",
			wantContext: "load config",
			wantPath:    "$.a",
		},
		"wrapper that adds text after a binding": {
			node:        niceyaml.NewErrorTree(fmt.Errorf("%w: see docs", yamltest.Bind(t, source, badA()))),
			want:        "bad a",
			wantContext: "see docs",
			wantPath:    "$.a",
		},
		"wrapper that adds text around a binding": {
			node:        niceyaml.NewErrorTree(fmt.Errorf("item %w is bad", yamltest.Bind(t, source, badA()))),
			want:        "bad a",
			wantContext: "item is bad",
			wantPath:    "$.a",
		},
		"wrappers that add text on both sides of a binding": {
			node: niceyaml.NewErrorTree(
				fmt.Errorf("%w: see docs", fmt.Errorf("load config: %w", yamltest.Bind(t, source, badA()))),
			),
			want:        "bad a",
			wantContext: "load config: see docs",
			wantPath:    "$.a",
		},
		"wrapper that rewrites the line of a binding": {
			// The text of the wrapper does not hold the line of the
			// binding, so the whole of it is the context.
			node: niceyaml.NewErrorTree(rewrittenError{
				msg: "config is invalid",
				err: yamltest.Bind(t, source, badA()),
			}),
			want:        "bad a",
			wantContext: "config is invalid",
			wantPath:    "$.a",
		},
		"wrapper that rewrites the message of an unbound Error": {
			node:        niceyaml.NewErrorTree(rewrittenError{msg: "config is invalid", err: badA()}),
			want:        "bad a",
			wantContext: "config is invalid",
			wantPath:    "@.a",
		},
		"wrapper around a wrapped warning in a bound join": {
			node: problems(yamltest.Bind(t, source, errors.Join(
				fmt.Errorf("ctx: %w", yamltest.Bind(t, source, niceyaml.NewWarning("old", niceyaml.AtPath(pathA)))),
				badB(),
			)))[0],
			want:        "old",
			wantContext: "ctx",
			wantPath:    "$.a",
			wantWarning: true,
		},
		"wrapper around a binding in a bound join": {
			node: problems(yamltest.Bind(t, source, errors.Join(
				fmt.Errorf("load config: %w", yamltest.Bind(t, source, badA())),
				badB(),
			)))[0],
			want:        "bad a",
			wantContext: "load config",
			wantPath:    "$.a",
		},
		"wrapper around a bound join": {
			// The wrapper heads the problems of the join, so its text is
			// context alone and the node does not have a message.
			node: niceyaml.NewErrorTree(errors.Join(
				fmt.Errorf("spec: %w", yamltest.Bind(t, source, errors.Join(badA(), badB()))),
				errors.New("lint"),
			)).Children[0],
			wantContext: "spec",
		},
		"wrapper around an unbound Error": {
			// The text of the node puts the path in front of the text the
			// wrapper added, and the message leaves both out.
			node:        niceyaml.NewErrorTree(fmt.Errorf("load config: %w", badA())),
			want:        "bad a",
			wantContext: "load config",
			wantPath:    "@.a",
		},
		"wrapper inside a binding": {
			// A validator adds the context before a Node binds the error.
			node:        niceyaml.NewErrorTree(yamltest.Bind(t, source, fmt.Errorf("decode a: %w", badA()))),
			want:        "bad a",
			wantContext: "decode a",
			wantPath:    "$.a",
		},
		"wrappers inside and around a binding that adopted a binding": {
			node: niceyaml.NewErrorTree(fmt.Errorf("load config: %w", yamltest.Bind(t, source,
				fmt.Errorf("check: %w", yamltest.Bind(t, source, fmt.Errorf("decode a: %w", badA()))),
			))),
			want:        "bad a",
			wantContext: "load config: check: decode a",
			wantPath:    "$.a",
		},
		"wrapper around a located Error around a wrapper": {
			node: niceyaml.NewErrorTree(fmt.Errorf("load config: %w",
				niceyaml.Invalid(fmt.Errorf("decode a: %w", niceyaml.NewError("bad a")), niceyaml.AtPath(pathA)),
			)),
			want:        "bad a",
			wantContext: "load config: decode a",
			wantPath:    "@.a",
		},
		"wrapper around an Error that wraps a plain error": {
			// The Error is the problem, so the text below it is the
			// message, and only the wrapper above it is context.
			node: niceyaml.NewErrorTree(fmt.Errorf("load config: %w",
				niceyaml.Invalid(fmt.Errorf("parse a: %w", errors.New("not a number")), niceyaml.AtPath(pathA)),
			)),
			want:        "parse a: not a number",
			wantContext: "load config",
			wantPath:    "@.a",
		},
		"wrapper around a plain error": {
			// A chain that holds neither an Error nor a binding does not
			// name its problem, so its whole text is the message.
			node: niceyaml.NewErrorTree(fmt.Errorf("load config: %w", errors.New("too old"))),
			want: "load config: too old",
		},
		"binding of a wrapper around a plain error": {
			node: niceyaml.NewErrorTree(yamltest.Bind(t, source, fmt.Errorf("load config: %w", errors.New("too old")))),
			want: "load config: too old",
		},
		"wrapper around a binding of a plain error": {
			node: niceyaml.NewErrorTree(
				fmt.Errorf("load config: %w", yamltest.Bind(t, source, fmt.Errorf("parse: %w", errors.New("too old")))),
			),
			want:        "parse: too old",
			wantContext: "load config",
		},
		"unbound warning": {
			// The label of the severity stays out of the message.
			node:        niceyaml.NewErrorTree(niceyaml.NewWarning("old layout")),
			want:        "old layout",
			wantWarning: true,
		},
		"wrapper around an unbound warning": {
			node:        niceyaml.NewErrorTree(fmt.Errorf("load plugin: %w", niceyaml.NewWarning("old layout"))),
			want:        "old layout",
			wantContext: "load plugin",
			wantWarning: true,
		},
		"wrapper around a summary": {
			// The summary keeps its own message, which is not context.
			node: niceyaml.NewErrorTree(
				fmt.Errorf("load config: %w", niceyaml.NewSummary("2 problems", badA(), badB())),
			),
			want:        "2 problems",
			wantContext: "load config",
		},
		"wrapper around a bound summary": {
			node: niceyaml.NewErrorTree(
				fmt.Errorf(
					"load config: %w",
					yamltest.Bind(t, source, niceyaml.NewSummary("2 problems", badA(), badB())),
				),
			),
			want:        "2 problems",
			wantContext: "load config",
		},
		"wrapper around a join": {
			node:        niceyaml.NewErrorTree(fmt.Errorf("check: %w", errors.Join(badA(), badB()))),
			wantContext: "check",
		},
		"binding of a wrapper around a join": {
			node: niceyaml.NewErrorTree(
				yamltest.Bind(t, source, fmt.Errorf("check: %w", errors.Join(badA(), badB()))),
			),
			wantContext: "check",
		},
		"wrapper with several verbs around several problems": {
			// The wrapper is a heading whose text is its own message.
			node: niceyaml.NewErrorTree(
				fmt.Errorf("check: %w, %w", niceyaml.NewError("bad a"), niceyaml.NewError("bad b")),
			),
			want: "check: bad a, bad b",
		},
		"wrapper with several verbs around sentinels": {
			// No branch is a problem, so the whole text is the message.
			node: niceyaml.NewErrorTree(fmt.Errorf("%w: %w", errors.New("bad config"), errors.New("too old"))),
			want: "bad config: too old",
		},
		"wrapper with a sentinel around a binding": {
			node: niceyaml.NewErrorTree(
				fmt.Errorf("%w: %w", errors.New("bad config"), yamltest.Bind(t, source, badA())),
			),
			want:        "bad a",
			wantContext: "bad config",
			wantPath:    "$.a",
		},
		"Error with a path of its own around a binding": {
			// The binding resolved its location already, so the node reads
			// as the binding, and the outer path does not move it.
			node: niceyaml.NewErrorTree(
				niceyaml.Invalid(yamltest.Bind(t, source, badA()), niceyaml.AtPath(pathB)),
			),
			want:     "bad a",
			wantPath: "$.a",
		},
		"Error with a path of its own around a binding whose location did not resolve": {
			// The binding does not name a place in the text, so the node
			// reads as the Error, which no source bound yet.
			node: niceyaml.NewErrorTree(niceyaml.Invalid(
				yamltest.Bind(t, source, niceyaml.NewError("far", niceyaml.AtPosition(position.New(9, 0)))),
				niceyaml.AtPath(pathB),
			)),
			want:     "f.yaml: far",
			wantPath: "@.b",
		},
		"Error with the path of a binding whose path did not resolve": {
			// The row of the binding shows the path of the Error already,
			// so the node reads as the binding.
			node: niceyaml.NewErrorTree(niceyaml.Invalid(
				yamltest.Bind(t, source, niceyaml.NewError("far", niceyaml.AtPath(farPath))),
				niceyaml.AtPath(farPath),
			)),
			want:     "far",
			wantPath: "$.a.*",
		},
		"Error with a path of its own around a value binding": {
			node:     niceyaml.NewErrorTree(niceyaml.Invalid(niceyaml.BindValue(badA()), niceyaml.AtPath(pathB))),
			want:     "$.a: bad a",
			wantPath: "@.b",
		},
		"unbound located Error above a value binding of a warning": {
			// The node reports the severity in Warning, so the message
			// leaves out the label that the line of the binding holds.
			node:        niceyaml.NewErrorTree(niceyaml.Locate(niceyaml.BindValue(oldA()), atB)),
			want:        "$.a: old a",
			wantPath:    "@.b",
			wantWarning: true,
		},
		"wrapper around an unbound located Error above a value binding of a warning": {
			node: niceyaml.NewErrorTree(
				fmt.Errorf("load: %w", niceyaml.Locate(niceyaml.BindValue(oldA()), atB)),
			),
			want:        "$.a: old a",
			wantContext: "load",
			wantPath:    "@.b",
			wantWarning: true,
		},
		"unbound located Error above a wrapper around a value binding of a warning": {
			node:        niceyaml.NewErrorTree(niceyaml.Locate(check(niceyaml.BindValue(oldA())), atB)),
			want:        "$.a: old a",
			wantContext: "check",
			wantPath:    "@.b",
			wantWarning: true,
		},
		"unbound located Error above a value binding of a wrapped warning": {
			node:        niceyaml.NewErrorTree(niceyaml.Locate(niceyaml.BindValue(check(oldA())), atB)),
			want:        "$.a: old a",
			wantContext: "check",
			wantPath:    "@.b",
			wantWarning: true,
		},
		"located Error above a value binding of a warning": {
			// The source that binds the Error gives the binding its place,
			// so the message is that of the warning alone.
			node: niceyaml.NewErrorTree(yamltest.Bind(t, source,
				niceyaml.Locate(niceyaml.BindValue(oldA()), atB),
			)),
			want:        "old a",
			wantPath:    "$.b",
			wantWarning: true,
		},
		"located Error above a binding of a wrapper in another source": {
			// The message holds the line of the binding with the message
			// of its problem, and the text of the wrapper is context.
			node: niceyaml.NewErrorTree(yamltest.Bind(t, source,
				niceyaml.Locate(yamltest.Bind(t, included, check(badKey())), atB),
			)),
			want:        "inc.yaml:1:6: $.key: bad key",
			wantContext: "check",
			wantPath:    "$.b",
		},
		"located Error above a wrapper around a binding in another source": {
			// The wrapper reads the same outside the binding.
			node: niceyaml.NewErrorTree(yamltest.Bind(t, source,
				niceyaml.Locate(check(yamltest.Bind(t, included, badKey())), atB),
			)),
			want:        "inc.yaml:1:6: $.key: bad key",
			wantContext: "check",
			wantPath:    "$.b",
		},
		"located Error above a binding of a wrapped warning in another source": {
			node: niceyaml.NewErrorTree(yamltest.Bind(t, source, niceyaml.Locate(
				yamltest.Bind(t, included, check(oldKey())),
				atB,
			))),
			want:        "inc.yaml:1:6: $.key: old key",
			wantContext: "check",
			wantPath:    "$.b",
			wantWarning: true,
		},
		"wrappers around and below a located Error above a binding in another source": {
			node: niceyaml.NewErrorTree(yamltest.Bind(t, source, fmt.Errorf("include: %w",
				niceyaml.Locate(yamltest.Bind(t, included, check(badKey())), atB),
			))),
			want:        "inc.yaml:1:6: $.key: bad key",
			wantContext: "include: check",
			wantPath:    "$.b",
		},
		"located Error above a binding of a wrapper that rewrites a line in another source": {
			// The whole text of the wrapper is context, and the message
			// holds the line of the binding that the wrapper wraps.
			node:        niceyaml.NewErrorTree(yamltest.Bind(t, source, niceyaml.Locate(rewritten, atB))),
			want:        "inc.yaml:1:6: $.key: bad key",
			wantContext: "config is invalid",
			wantPath:    "$.b",
		},
		"located Error above a binding of wrappers that rewrite a line with one text": {
			// The message names the source of the binding, although the
			// text of each wrapper ends with the text of the other.
			node:        niceyaml.NewErrorTree(yamltest.Bind(t, source, niceyaml.Locate(rewrittenTwice, atB))),
			want:        "inc.yaml: no place",
			wantContext: "config is invalid: config is invalid",
			wantPath:    "$.b",
		},
		"unbound located Error above a binding of wrappers that rewrite a line with one text": {
			node:        niceyaml.NewErrorTree(niceyaml.Locate(rewrittenTwice, atB)),
			want:        "inc.yaml: no place",
			wantContext: "config is invalid: config is invalid",
			wantPath:    "@.b",
		},
		"located Error above a binding that adopted a rewritten warning in another source": {
			node: niceyaml.NewErrorTree(yamltest.Bind(t, source, niceyaml.Locate(
				yamltest.Bind(t, included, rewrite(yamltest.Bind(t, included, oldKey()))),
				atB,
			))),
			want:        "inc.yaml:1:6: $.key: old key",
			wantContext: "config is invalid",
			wantPath:    "$.b",
			wantWarning: true,
		},
		"located Error above a wrapper that rewrites the line of a warning in another source": {
			// The wrapper does not keep the line of the binding, and the
			// message reads as it does below a wrapper that keeps it.
			node: niceyaml.NewErrorTree(yamltest.Bind(t, source,
				niceyaml.Locate(rewrite(yamltest.Bind(t, included, oldKey())), atB),
			)),
			want:        "inc.yaml:1:6: $.key: old key",
			wantContext: "config is invalid",
			wantPath:    "$.b",
			wantWarning: true,
		},
		"wrapper that rewrites the line of a located Error above a warning in another source": {
			node: niceyaml.NewErrorTree(yamltest.Bind(t, source,
				rewrite(niceyaml.Locate(yamltest.Bind(t, included, oldKey()), atB)),
			)),
			want:        "inc.yaml:1:6: $.key: old key",
			wantContext: "config is invalid",
			wantPath:    "$.b",
			wantWarning: true,
		},
		"located Error above a wrapper that rewrites the line of a warning whose location did not resolve": {
			// The source that binds the Error bound the warning too, so
			// the message leaves out its name as well as the label.
			node: niceyaml.NewErrorTree(yamltest.Bind(t, source,
				niceyaml.Locate(rewrite(yamltest.Bind(t, source, niceyaml.NewWarning("old"))), atB),
			)),
			want:        "old",
			wantContext: "config is invalid",
			wantPath:    "$.b",
			wantWarning: true,
		},
		"located Error above a wrapper that rewrites the line of a binding whose location did not resolve": {
			node: niceyaml.NewErrorTree(yamltest.Bind(t, source,
				niceyaml.Locate(rewrite(yamltest.Bind(t, source, niceyaml.NewError("no place"))), atB),
			)),
			want:        "no place",
			wantContext: "config is invalid",
			wantPath:    "$.b",
		},
		"located Error above a binding of a wrapper whose location did not resolve": {
			node: niceyaml.NewErrorTree(yamltest.Bind(t, source,
				niceyaml.Locate(yamltest.Bind(t, source, check(niceyaml.NewError("no place"))), atB),
			)),
			want:        "no place",
			wantContext: "check",
			wantPath:    "$.b",
		},
		"unbound located Error above a binding of a wrapper whose location did not resolve": {
			node: niceyaml.NewErrorTree(
				niceyaml.Locate(yamltest.Bind(t, source, check(niceyaml.NewError("no place"))), atB),
			),
			want:        "f.yaml: no place",
			wantContext: "check",
			wantPath:    "@.b",
		},
		"located Error above a binding of a wrapped summary": {
			node: niceyaml.NewErrorTree(yamltest.Bind(t, source,
				niceyaml.Locate(checkedSummary(source, badA(), badB()), atB),
			)),
			want:        "some problems",
			wantContext: "check",
			wantPath:    "$.b",
		},
		"unbound located Error above a binding of a wrapped summary": {
			node:        niceyaml.NewErrorTree(niceyaml.Locate(checkedSummary(source, badA(), badB()), atB)),
			want:        "f.yaml: some problems",
			wantContext: "check",
			wantPath:    "@.b",
		},
		"located Error above a binding of a wrapped summary in another source": {
			node: niceyaml.NewErrorTree(yamltest.Bind(t, source,
				niceyaml.Locate(checkedSummary(included, badKey(), badPort()), atB),
			)),
			want:        "inc.yaml: some problems",
			wantContext: "check",
			wantPath:    "$.b",
		},
		"located Error above a binding of a wrapped join": {
			// The wrapper heads the problems of the join, so its text is
			// context alone below a located Error too.
			node: niceyaml.NewErrorTree(yamltest.Bind(t, source,
				niceyaml.Locate(checkedJoin(source, badA(), badB()), atB),
			)),
			wantContext: "check",
			wantPath:    "$.b",
		},
		"unbound located Error above a binding of a wrapped join": {
			node:        niceyaml.NewErrorTree(niceyaml.Locate(checkedJoin(source, badA(), badB()), atB)),
			wantContext: "check",
			wantPath:    "@.b",
		},
		"located Error above a binding of a wrapped join in another source": {
			node: niceyaml.NewErrorTree(yamltest.Bind(t, source,
				niceyaml.Locate(checkedJoin(included, badKey(), badPort()), atB),
			)),
			wantContext: "check",
			wantPath:    "$.b",
		},
		"summary": {
			node: niceyaml.NewErrorTree(niceyaml.NewSummary("2 problems", badA(), badB())),
			want: "2 problems",
		},
		"root of a join": {
			node: niceyaml.NewErrorTree(errors.Join(badA(), badB())),
		},
		"unbound detail": {
			node:     detailOf(withDetail()),
			want:     "see b",
			wantPath: "@.b",
		},
		"detail of a wrapped problem": {
			// A detail does not take the context of the problem it
			// explains.
			node:     detailOf(fmt.Errorf("load config: %w", yamltest.Bind(t, source, withDetail()))),
			want:     "see b",
			wantPath: "$.b",
		},
		"wrapped detail": {
			node: detailOf(yamltest.Bind(t, source, badA().With(
				niceyaml.WithDetails(fmt.Errorf("first use: %w", yamltest.ErrorAt("see b", "b"))),
			))),
			want:        "see b",
			wantContext: "first use",
			wantPath:    "$.b",
		},
		"bound detail": {
			node:     detailOf(yamltest.Bind(t, source, withDetail())),
			want:     "see b",
			wantPath: "$.b",
		},
		"hand-built node": {
			node: niceyaml.ErrorTree{
				Err: errors.New("disk full"),
				Row: niceyaml.ErrorRow{Text: "write cache: disk full"},
			},
			want: "write cache: disk full",
		},
		"hand-built node of a wrapper": {
			node: niceyaml.ErrorTree{
				Err: fmt.Errorf("load config: %w", niceyaml.NewError("bad a")),
				Row: niceyaml.ErrorRow{Text: "load config: bad a"},
			},
			want:        "bad a",
			wantContext: "load config",
		},
		"hand-built node whose error does not hold its binding": {
			// The node cannot tell the wrappers of the binding from its
			// message. So the message is that of the binding, and the node
			// does not have a context. The wrapper in Err does not add one
			// either.
			node: niceyaml.ErrorTree{
				Err:   fmt.Errorf("write cache: %w", niceyaml.NewError("disk full")),
				Bound: yamltest.RequireSourceError(t, yamltest.Bind(t, source, fmt.Errorf("decode a: %w", badA()))),
				Row:   niceyaml.ErrorRow{Text: "write cache: disk full"},
			},
			want:     "decode a: bad a",
			wantPath: "$.a",
		},
		"zero node": {},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.node.Message())
			assert.Equal(t, tc.wantContext, tc.node.Context())
			assert.Equal(t, tc.wantWarning, tc.node.Warning)

			path, ok := tc.node.Path()
			if tc.wantPath == "" {
				assert.False(t, ok)

				return
			}

			require.True(t, ok)
			assert.Equal(t, tc.wantPath, path.String())
		})
	}
}

func TestErrorTree_Context(t *testing.T) {
	t.Parallel()

	source := errorTreeSource()
	badA, badB := badKeyError("a"), badKeyError("b")

	// A row holds the context and the message that a report carries for a
	// problem.
	rows := func(err error) []string {
		var got []string

		for problem := range niceyaml.Problems(err) {
			got = append(got, fmt.Sprintf("[%s] %s", problem.Context(), problem.Message()))
		}

		return got
	}

	// Each wrap puts a wrapper around the problems it receives. The row of
	// the problem at a must not change with the number of problems beside
	// it, whether the wrapper shares its row or heads several.
	tcs := map[string]struct {
		wrap func(errs ...error) error
		want string
	}{
		"wrapper around a join": {
			wrap: func(errs ...error) error { return fmt.Errorf("check: %w", errors.Join(errs...)) },
			want: "check",
		},
		"wrapper around a bound join": {
			wrap: func(errs ...error) error {
				return fmt.Errorf("load item: %w", yamltest.Bind(t, source, errors.Join(errs...)))
			},
			want: "load item",
		},
		"binding of a wrapper around a join": {
			wrap: func(errs ...error) error {
				return yamltest.Bind(t, source, fmt.Errorf("check: %w", errors.Join(errs...)))
			},
			want: "check",
		},
		"wrapper around a summary": {
			// The summary returns one problem alone, and its heading over
			// several is not context.
			wrap: func(errs ...error) error {
				return fmt.Errorf("load item: %w", niceyaml.NewSummary("some problems", errs...))
			},
			want: "load item",
		},
		"wrapper around a bound summary": {
			wrap: func(errs ...error) error {
				return fmt.Errorf("load item: %w",
					yamltest.Bind(t, source, niceyaml.NewSummary("some problems", errs...)))
			},
			want: "load item",
		},
		"wrappers around and inside a binding": {
			wrap: func(errs ...error) error {
				return fmt.Errorf("load item: %w",
					yamltest.Bind(t, source, fmt.Errorf("check: %w", errors.Join(errs...))))
			},
			want: "load item: check",
		},
		"wrapper with a sentinel around a join": {
			wrap: func(errs ...error) error {
				return fmt.Errorf("%w: %w", errors.New("bad item"), errors.Join(errs...))
			},
			want: "bad item",
		},
		"wrapper that adds text after a join": {
			wrap: func(errs ...error) error { return fmt.Errorf("%w (see docs)", errors.Join(errs...)) },
			want: "(see docs)",
		},
		"nested wrappers around joins": {
			wrap: func(errs ...error) error {
				inner := fmt.Errorf("check: %w", errors.Join(errs...))

				return fmt.Errorf("load item: %w", errors.Join(inner, errors.New("lint")))
			},
			want: "load item: check",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			wantA := "[" + tc.want + "] bad a"
			wantB := "[" + tc.want + "] bad b"

			assert.Equal(t, wantA, rows(tc.wrap(badA()))[0])

			two := rows(tc.wrap(badA(), badB()))
			require.GreaterOrEqual(t, len(two), 2)
			assert.Equal(t, []string{wantA, wantB}, two[:2])
		})
	}

	t.Run("a problem beside a wrapped heading does not take its context", func(t *testing.T) {
		t.Parallel()

		err := errors.Join(
			fmt.Errorf("check: %w", errors.Join(badA(), badB())),
			niceyaml.NewError("too old"),
		)

		assert.Equal(t, []string{"[check] bad a", "[check] bad b", "[] too old"}, rows(err))
	})

	// The wrapper of a heading reads around the wrapper of each problem
	// below it as it reads around the wrapper of one problem alone. It does
	// so whatever the side that it writes its text on.
	decodeA := func() error { return fmt.Errorf("decode a: %w", badA()) }
	decodeB := func() error { return fmt.Errorf("decode b: %w", badB()) }

	heads := map[string]func(errs ...error) error{
		"summary": func(errs ...error) error { return niceyaml.NewSummary("some problems", errs...) },
		"bound summary": func(errs ...error) error {
			return yamltest.Bind(t, source, niceyaml.NewSummary("some problems", errs...))
		},
		"join": errors.Join,
		"bound join": func(errs ...error) error {
			return yamltest.Bind(t, source, errors.Join(errs...))
		},
	}

	forms := map[string]struct {
		// The format of the wrapper, and the context of the problem at
		// each key, with %s for the key.
		format string
		want   string
	}{
		"text in front with a colon":    {format: "check: %w", want: "check: decode %s"},
		"text in front without a colon": {format: "check %w", want: "check decode %s"},
		"text in front with a comma":    {format: "load config, %w", want: "load config, decode %s"},
		"text behind":                   {format: "%w (see docs)", want: "decode %s: (see docs)"},
		"text on both sides":            {format: "item %w is bad", want: "item decode %s: is bad"},
	}

	for headName, head := range heads {
		for formName, form := range forms {
			t.Run("wrapper with "+formName+" around a "+headName+" of wrapped problems", func(t *testing.T) {
				t.Parallel()

				wantA := "[" + fmt.Sprintf(form.want, "a") + "] bad a"
				wantB := "[" + fmt.Sprintf(form.want, "b") + "] bad b"

				assert.Equal(t, []string{wantA}, rows(fmt.Errorf(form.format, head(decodeA()))))
				assert.Equal(t, []string{wantA, wantB}, rows(fmt.Errorf(form.format, head(decodeA(), decodeB()))))
			})
		}
	}

	// The wrapper of a heading can meet a wrapper of each problem below it
	// that writes its text on the same side, or that rewrites the text of
	// its problem. The context joins the two texts in one order, and with
	// one joiner, around one problem and around several.
	rewrite := func(err error) error { return rewrittenError{msg: "config is invalid", err: err} }
	plain := func(err error) error { return err }
	behind := func(format string) func(err error) error {
		return func(err error) error { return fmt.Errorf(format, err) }
	}

	stacked := map[string]struct {
		// The wrapper around each problem, the format of the wrapper
		// around the head, and the context of each problem.
		inner  func(err error) error
		format string
		want   string
	}{
		"text behind around text behind": {
			inner:  behind("%w (inner)"),
			format: "%w (outer)",
			want:   "(inner) (outer)",
		},
		"text behind a colon around text behind a colon": {
			inner:  behind("%w: see spec"),
			format: "%w: see guide",
			want:   "see spec: see guide",
		},
		"text in front and text behind a colon": {
			inner:  plain,
			format: "check %w: see docs",
			want:   "check: see docs",
		},
		"text in front around a wrapper that rewrites the text": {
			inner:  rewrite,
			format: "load %w",
			want:   "load config is invalid",
		},
	}

	for headName, head := range heads {
		for stackName, stack := range stacked {
			t.Run("wrapper with "+stackName+" of each problem of a "+headName, func(t *testing.T) {
				t.Parallel()

				wantA := "[" + stack.want + "] bad a"
				wantB := "[" + stack.want + "] bad b"

				one := fmt.Errorf(stack.format, head(stack.inner(badA())))
				two := fmt.Errorf(stack.format, head(stack.inner(badA()), stack.inner(badB())))

				assert.Equal(t, []string{wantA}, rows(one))
				assert.Equal(t, []string{wantA, wantB}, rows(two))

				assert.Equal(t, []string{wantA}, rows(yamltest.Bind(t, source, one)))
				assert.Equal(t, []string{wantA, wantB}, rows(yamltest.Bind(t, source, two)))
			})
		}
	}

	t.Run("the detail of a heading does not take the context above the heading", func(t *testing.T) {
		t.Parallel()

		// A row holds the context and the message of a node, and marks a
		// detail. The place of a detail among the rows is not under test.
		nodes := func(err error) []string {
			var got []string

			for node := range niceyaml.NewErrorTree(err).All() {
				row := fmt.Sprintf("[%s] %s", node.Context(), node.Message())
				if node.Detail {
					row = "detail " + row
				}

				got = append(got, row)
			}

			return got
		}

		// The Error with the detail does not have a row above a wrapper
		// around a join, so the detail explains the heading of that wrapper.
		checked := fmt.Errorf("load: %w", niceyaml.Invalid(
			fmt.Errorf("check: %w", errors.Join(badA(), badB())),
			niceyaml.WithDetails(errors.New("see docs")),
		))
		wantChecked := []string{"[load: check] ", "[load: check] bad a", "[load: check] bad b", "detail [] see docs"}

		assert.ElementsMatch(t, wantChecked, nodes(checked))
		assert.ElementsMatch(t, wantChecked, nodes(yamltest.Bind(t, source, checked)))

		// The detail of a summary keeps the text of its own wrapper alone.
		summed := fmt.Errorf("load: %w", niceyaml.Invalid(
			niceyaml.NewSummary("2 violations", badA(), badB()),
			niceyaml.WithDetails(fmt.Errorf("hint: %w", niceyaml.NewError("see docs"))),
		))
		wantSummed := []string{"[load] 2 violations", "[load] bad a", "[load] bad b", "detail [hint] see docs"}

		assert.ElementsMatch(t, wantSummed, nodes(summed))
		assert.ElementsMatch(t, wantSummed, nodes(yamltest.Bind(t, source, summed)))
	})

	// The words of a wrapper can repeat the message of the problem at a.
	// The text of the wrapper then holds that message in several places.
	// The wrapper must still read around one problem as it reads around
	// several.
	sentinel := func() error { return errors.New("bad a") }

	repeats := map[string]struct {
		wrap func(err error) error
		want string
	}{
		"text in front": {
			wrap: func(err error) error { return fmt.Errorf("bad a: %w", err) },
			want: "bad a",
		},
		"text behind a colon": {
			wrap: func(err error) error { return fmt.Errorf("%w: bad a in registry", err) },
			want: "bad a in registry",
		},
		"text behind in parentheses": {
			wrap: func(err error) error { return fmt.Errorf("%w (bad a or bad b)", err) },
			want: "(bad a or bad b)",
		},
		"a sentinel in front": {
			wrap: func(err error) error { return fmt.Errorf("%w: %w", sentinel(), err) },
			want: "bad a",
		},
		"a sentinel behind": {
			wrap: func(err error) error { return fmt.Errorf("%w: %w", err, errors.New("key bad a")) },
			want: "key bad a",
		},
		"a sentinel on each side": {
			wrap: func(err error) error { return fmt.Errorf("%w: %w: %w", sentinel(), err, sentinel()) },
			want: "bad a: bad a",
		},
		"a sentinel in front and text behind": {
			wrap: func(err error) error { return fmt.Errorf("%w: %w: bad a in registry", sentinel(), err) },
			want: "bad a: bad a in registry",
		},
		"text in front and a sentinel behind": {
			wrap: func(err error) error { return fmt.Errorf("bad a item: %w: %w", err, errors.New("lookup")) },
			want: "bad a item: lookup",
		},
		"a sentinel in front that holds the message, and text behind": {
			// The search for the problem starts behind the sentinel, so it
			// does not read the "bad a" of the sentinel as the problem.
			wrap: func(err error) error { return fmt.Errorf("%w: %w (see docs)", errors.New("key bad a"), err) },
			want: "key bad a: (see docs)",
		},
		"text in front and two sentinels behind": {
			// The search for the problem ends in front of the first of the
			// sentinels, although that one holds the message too.
			wrap: func(err error) error {
				return fmt.Errorf("check %w: %w: %w", err, errors.New("very bad a"), errors.New("z"))
			},
			want: "check: very bad a: z",
		},
	}

	// A wrapper must give one context to a lone problem and to each of
	// several problems, whether a source binds the wrapper or not.
	sameAround := func(t *testing.T, wrap func(err error) error, want string) {
		t.Helper()

		wantA := "[" + want + "] bad a"
		wantB := "[" + want + "] bad b"

		assert.Equal(t, []string{wantA}, rows(wrap(badA())))
		assert.Equal(t, []string{wantA, wantB}, rows(wrap(errors.Join(badA(), badB()))))

		assert.Equal(t, []string{wantA}, rows(yamltest.Bind(t, source, wrap(badA()))))
		assert.Equal(t, []string{wantA, wantB},
			rows(yamltest.Bind(t, source, wrap(errors.Join(badA(), badB())))))
	}

	for name, tc := range repeats {
		t.Run("wrapper with "+name+" that repeats the message", func(t *testing.T) {
			t.Parallel()

			sameAround(t, tc.wrap, tc.want)
		})
	}

	// A wrapper with explicit argument indexes can write its errors in
	// another order than the order that it unwraps them in. The text of
	// the wrapper then does not hold the errors beside the problem where
	// the unwrap order puts them. A nil [*niceyaml.Error] among the errors
	// does not write any text. The wrapper must still read around one
	// problem as it reads around several.
	var none *niceyaml.Error

	lookup := func() error { return errors.New("lookup") }
	registry := func() error { return errors.New("registry") }

	reordered := map[string]struct {
		wrap func(err error) error
		want string
	}{
		"a sentinel behind the problem that it writes in front": {
			wrap: func(err error) error { return fmt.Errorf("%[2]w: %[1]w", err, lookup()) },
			want: "lookup",
		},
		"a sentinel in front of the problem that it writes behind": {
			wrap: func(err error) error { return fmt.Errorf("%[2]w: %[1]w", lookup(), err) },
			want: "lookup",
		},
		"two sentinels in front that it writes in the other order": {
			wrap: func(err error) error { return fmt.Errorf("%[2]w %[1]w %[3]w", lookup(), registry(), err) },
			want: "registry lookup",
		},
		"two sentinels behind that it writes in the other order": {
			wrap: func(err error) error { return fmt.Errorf("%[1]w: %[3]w %[2]w", err, lookup(), registry()) },
			want: "registry lookup",
		},
		"three errors that it writes in reverse": {
			wrap: func(err error) error { return fmt.Errorf("%[3]w: %[2]w: %[1]w", lookup(), err, registry()) },
			want: "registry: lookup",
		},
		"a nil Error in front of the problem": {
			wrap: func(err error) error { return fmt.Errorf("%w: %w", none, err) },
			want: "",
		},
		"a nil Error behind the problem": {
			wrap: func(err error) error { return fmt.Errorf("%w: %w", err, none) },
			want: "",
		},
		"a nil Error and a sentinel on each side of the problem": {
			wrap: func(err error) error {
				return fmt.Errorf("%w%w: %w: %w%w", none, lookup(), err, registry(), none)
			},
			want: "lookup: registry",
		},
	}

	for name, tc := range reordered {
		t.Run("wrapper with "+name, func(t *testing.T) {
			t.Parallel()

			sameAround(t, tc.wrap, tc.want)
		})
	}

	t.Run("wrapper with text on both sides that repeats the message", func(t *testing.T) {
		t.Parallel()

		// The text of the wrapper does not tell which "bad a" the wrapper
		// wraps. The split of a lone problem thus takes the one at the end.
		atEnd := func(err error) error { return fmt.Errorf("item %w was bad a", err) }

		assert.Equal(t, []string{"[item bad a was] bad a"}, rows(atEnd(badA())))
		assert.Equal(t, []string{"[item was bad a] bad a", "[item was bad a] bad b"},
			rows(atEnd(errors.Join(badA(), badB()))))

		// Neither "bad a" stands at an end, so the whole text is context.
		inside := func(err error) error { return fmt.Errorf("item %w was bad a here", err) }

		assert.Equal(t, []string{"[item bad a was bad a here] bad a"}, rows(inside(badA())))
		assert.Equal(t, []string{"[item was bad a here] bad a", "[item was bad a here] bad b"},
			rows(inside(errors.Join(badA(), badB()))))
	})

	t.Run("wrapper with a nil pointer behind the problem", func(t *testing.T) {
		t.Parallel()

		// The fmt package writes "<nil>" for a nil pointer whose Error
		// method panics, and the context holds that text.
		var missing *customTestError

		wrap := func(err error) error { return fmt.Errorf("%w: %w", err, missing) }

		assert.Equal(t, []string{"[<nil>] bad a"}, rows(wrap(badA())))
		assert.Equal(t, []string{"[<nil>] bad a", "[<nil>] bad b"}, rows(wrap(errors.Join(badA(), badB()))))
	})

	// A located Error keeps its location above a binding of another source
	// and above a binding that heads several problems. The wrapper that
	// such a binding binds is context for each problem either way.
	included := niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("inc.yaml"))
	atB := niceyaml.AtPath(paths.Current().Child("b"))

	located := map[string]func(errs ...error) error{
		"wrapped summary": func(errs ...error) error {
			return niceyaml.Locate(yamltest.Bind(t, source,
				fmt.Errorf("check: %w", niceyaml.NewSummary("some problems", errs...))), atB)
		},
		"wrapped join": func(errs ...error) error {
			return niceyaml.Locate(yamltest.Bind(t, source, fmt.Errorf("check: %w", errors.Join(errs...))), atB)
		},
		"wrapped summary in another source": func(errs ...error) error {
			return niceyaml.Locate(yamltest.Bind(t, included,
				fmt.Errorf("check: %w", niceyaml.NewSummary("some problems", errs...))), atB)
		},
		"wrapped join in another source": func(errs ...error) error {
			return niceyaml.Locate(yamltest.Bind(t, included, fmt.Errorf("check: %w", errors.Join(errs...))), atB)
		},
	}

	contexts := func(err error) []string {
		var got []string

		for problem := range niceyaml.Problems(err) {
			got = append(got, problem.Context())
		}

		return got
	}

	for name, locate := range located {
		t.Run("located Error above a binding of a "+name, func(t *testing.T) {
			t.Parallel()

			one, two := []string{"check"}, []string{"check", "check"}

			assert.Equal(t, one, contexts(locate(badA())))
			assert.Equal(t, two, contexts(locate(badA(), badB())))

			// The source that binds the located Error leaves the context
			// as it is, and so does a wrapper above that Error.
			assert.Equal(t, one, contexts(yamltest.Bind(t, source, locate(badA()))))
			assert.Equal(t, two, contexts(yamltest.Bind(t, source, locate(badA(), badB()))))

			include := []string{"include: check", "include: check"}

			assert.Equal(t, include[:1],
				contexts(yamltest.Bind(t, source, fmt.Errorf("include: %w", locate(badA())))))
			assert.Equal(t, include,
				contexts(yamltest.Bind(t, source, fmt.Errorf("include: %w", locate(badA(), badB())))))
		})
	}

	t.Run("an error without an Error or a binding takes the context of its headings alone", func(t *testing.T) {
		t.Parallel()

		tooOld, tooNew := errors.New("too old"), errors.New("too new")

		// The summary returns one member as it is. The wrapper then
		// shares a row with a plain error, which keeps the whole text.
		one := fmt.Errorf("load: %w", niceyaml.NewSummary("some problems", tooOld))
		two := fmt.Errorf("load: %w", niceyaml.NewSummary("some problems", tooOld, tooNew))

		assert.Equal(t, []string{"[] load: too old"}, rows(one))
		assert.Equal(t, []string{"[load] too old", "[load] too new"}, rows(two))
	})

	// The cause chain of an Error can end without another Error or a
	// binding. The text of each wrapper below the Error is then part of its
	// message, and only a wrapper above the Error adds context. A chain
	// without an Error keeps its whole text as its message.
	ends := map[string]func() error{
		"an error that does not have a cause": func() error { return &causeError{msg: "no cause"} },
		"an error that has a plain cause": func() error {
			return &causeError{msg: "open", cause: errors.New("no file")}
		},
		"a wrapper with several verbs around sentinels": func() error {
			return fmt.Errorf("%w: %w", errors.New("plugin"), errors.New("no file"))
		},
	}

	for name, end := range ends {
		t.Run("chain that ends at "+name, func(t *testing.T) {
			t.Parallel()

			chain := func() error { return fmt.Errorf("read config: %w", end()) }
			text := chain().Error()

			load := func(err error) error { return fmt.Errorf("load: %w", err) }

			tcs := map[string]struct {
				err  error
				want string
			}{
				"plain chain":                  {err: chain(), want: "[] " + text},
				"Error around the chain":       {err: niceyaml.Invalid(chain()), want: "[] " + text},
				"wrapper around such an Error": {err: load(niceyaml.Invalid(chain())), want: "[load] " + text},
				"wrapper around a plain chain": {err: load(chain()), want: "[] load: " + text},
			}

			for shape, tc := range tcs {
				assert.Equal(t, []string{tc.want}, rows(tc.err), shape)
				assert.Equal(t, []string{tc.want}, rows(yamltest.Bind(t, source, tc.err)), shape+", bound")
			}
		})
	}

	t.Run("every node of the tree carries its context", func(t *testing.T) {
		t.Parallel()

		err := fmt.Errorf("load item: %w", yamltest.Bind(t, source, niceyaml.NewSummary("2 problems", badA(), badB())))

		var got []string

		for node := range niceyaml.NewErrorTree(err).All() {
			got = append(got, fmt.Sprintf("[%s] %s", node.Context(), node.Message()))
		}

		assert.Equal(t, []string{"[load item] 2 problems", "[load item] bad a", "[load item] bad b"}, got)
	})

	t.Run("the rendering keeps the text of the wrapper where it was", func(t *testing.T) {
		t.Parallel()

		one := fmt.Errorf("load item: %w", yamltest.Bind(t, source, badA()))
		two := fmt.Errorf("load item: %w", yamltest.Bind(t, source, niceyaml.NewSummary("2 problems", badA(), badB())))

		assert.Equal(t, "f.yaml:1:4: $.a: load item: bad a", niceyaml.NewErrorTree(one).Label())

		var labels []string

		for node := range niceyaml.NewErrorTree(two).All() {
			labels = append(labels, node.Label())
		}

		assert.Equal(t, []string{
			"f.yaml: load item: 2 problems",
			"1:4: $.a: bad a",
			"2:4: $.b: bad b",
		}, labels)
	})
}

func TestErrorTree_Accessors(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString(stringtest.LinesLF(
		"a: 1",
		"---",
		"b: 2",
	), niceyaml.WithName("f.yaml"))
	docs := yamltest.Documents(t, source, 2)

	badB := niceyaml.NewError("bad b", niceyaml.AtPath(paths.Doc().Child("b")))
	gone := niceyaml.NewError("gone", niceyaml.AtPath(paths.Doc().Child("x").IndexAll()))

	t.Run("a bound node reads its binding", func(t *testing.T) {
		t.Parallel()

		node := niceyaml.NewErrorTree(docs[1].Bind(badB))
		require.NotNil(t, node.Bound)

		assert.Same(t, source, node.Source())
		assert.Equal(t, "f.yaml", node.Name())

		pos := yamltest.PositionOf(t, node)
		assert.Equal(t, position.New(2, 3), pos)
		assert.Equal(t, yamltest.LineRange(2, 3, 4), yamltest.RangeOf(t, node))

		index, ok := node.DocumentIndex()
		require.True(t, ok)
		assert.Equal(t, 1, index)

		assert.Equal(t, yamltest.PositionOf(t, node.Bound), pos)
	})

	t.Run("an unresolved location keeps the source", func(t *testing.T) {
		t.Parallel()

		node := niceyaml.NewErrorTree(docs[0].Bind(gone))
		require.NotNil(t, node.Bound)

		assert.Same(t, source, node.Source())
		assert.Equal(t, "f.yaml", node.Name())

		_, ok := node.Position()
		assert.False(t, ok)

		_, ok = node.Range()
		assert.False(t, ok)

		index, ok := node.DocumentIndex()
		require.True(t, ok)
		assert.Zero(t, index)
	})

	t.Run("a node reads the file path of its source", func(t *testing.T) {
		t.Parallel()

		// The file system of NewSourceFromFS names the file by its path
		// there, and WithName gives it another name for the report.
		fsys := fstest.MapFS{"config/menu.yaml": {Data: []byte("b: 2\n")}}

		source, err := niceyaml.NewSourceFromFS(fsys, "config/menu.yaml", niceyaml.WithName("menu"))
		require.NoError(t, err)

		bound := yamltest.Bind(t, source, badB)

		tcs := map[string]struct {
			err error
		}{
			"live":     {err: bound},
			"detached": {err: niceyaml.Detach(bound)},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				node := niceyaml.NewErrorTree(tc.err)
				assert.Equal(t, "menu", node.Name())
				assert.Equal(t, "config/menu.yaml", node.FilePath())
			})
		}
	})

	t.Run("a detached node keeps the name and no source", func(t *testing.T) {
		t.Parallel()

		node := niceyaml.NewErrorTree(niceyaml.Detach(docs[1].Bind(badB)))
		require.NotNil(t, node.Bound)

		assert.Nil(t, node.Source())
		assert.Equal(t, "f.yaml", node.Name())

		index, ok := node.DocumentIndex()
		require.True(t, ok)
		assert.Equal(t, 1, index)
	})

	t.Run("a node bound to no source returns zero values", func(t *testing.T) {
		t.Parallel()

		tcs := map[string]struct {
			node niceyaml.ErrorTree
		}{
			"unbound Error": {node: niceyaml.NewErrorTree(badB)},
			"plain error":   {node: niceyaml.NewErrorTree(errors.New("plain"))},
			"zero":          {},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				require.Nil(t, tc.node.Bound)
				assert.Nil(t, tc.node.Source())
				assert.Empty(t, tc.node.Name())
				assert.Empty(t, tc.node.FilePath())

				pos, ok := tc.node.Position()
				assert.False(t, ok)
				assert.Zero(t, pos)

				rng, ok := tc.node.Range()
				assert.False(t, ok)
				assert.Zero(t, rng)

				index, ok := tc.node.DocumentIndex()
				assert.False(t, ok)
				assert.Zero(t, index)
			})
		}
	})
}

func TestErrorTree_MarshalJSON(t *testing.T) {
	t.Parallel()

	source := errorTreeSource()
	fsys := fstest.MapFS{"config/f.yaml": {Data: []byte("a: 1\nb: 2\n")}}

	fileSource, err := niceyaml.NewSourceFromFS(fsys, "config/f.yaml", niceyaml.WithName("f.yaml"))
	require.NoError(t, err)

	badA := niceyaml.NewError("bad a", niceyaml.AtPath(paths.Doc().Child("a")))
	badB := niceyaml.NewError("bad b", niceyaml.AtPath(paths.Doc().Child("b")),
		niceyaml.WithDetails(niceyaml.NewError("see a", niceyaml.AtPath(paths.Doc().Child("a")))))
	gone := niceyaml.NewError("gone", niceyaml.AtPath(paths.Doc().Child("x").IndexAll()))

	tcs := map[string]struct {
		node niceyaml.ErrorTree
		want string
	}{
		"a bound problem writes its location and its context": {
			node: niceyaml.NewErrorTree(fmt.Errorf("check: %w", yamltest.Bind(t, fileSource, badA))),
			want: `{
				"message": "bad a",
				"context": "check",
				"name": "f.yaml",
				"filePath": "config/f.yaml",
				"path": "$.a",
				"range": {"start": {"line": 0, "col": 3}, "end": {"line": 0, "col": 4}}
			}`,
		},
		"a detail writes under children": {
			node: niceyaml.NewErrorTree(yamltest.Bind(t, source, badB)),
			want: `{
				"message": "bad b",
				"name": "f.yaml",
				"path": "$.b",
				"range": {"start": {"line": 1, "col": 3}, "end": {"line": 1, "col": 4}},
				"children": [{
					"message": "see a",
					"name": "f.yaml",
					"path": "$.a",
					"range": {"start": {"line": 0, "col": 3}, "end": {"line": 0, "col": 4}},
					"detail": true
				}]
			}`,
		},
		"a warning writes its severity": {
			node: niceyaml.NewErrorTree(yamltest.Bind(t, source,
				niceyaml.NewWarning("old a", niceyaml.AtPath(paths.Doc().Child("a"))))),
			want: `{
				"message": "old a",
				"name": "f.yaml",
				"path": "$.a",
				"range": {"start": {"line": 0, "col": 3}, "end": {"line": 0, "col": 4}},
				"warning": true
			}`,
		},
		"a heading writes the same fields as its problems": {
			node: niceyaml.NewErrorTree(yamltest.Bind(t, source, niceyaml.NewSummary("2 problems", badA, gone))),
			want: `{
				"message": "2 problems",
				"name": "f.yaml",
				"children": [
					{
						"message": "bad a",
						"name": "f.yaml",
						"path": "$.a",
						"range": {"start": {"line": 0, "col": 3}, "end": {"line": 0, "col": 4}}
					},
					{"message": "gone", "name": "f.yaml", "path": "$.x[*]"}
				]
			}`,
		},
		"a position inside a token writes the range of the token": {
			node: niceyaml.NewErrorTree(yamltest.Bind(t,
				niceyaml.NewSourceFromString("key: hello\n", niceyaml.WithName("m.yaml")),
				niceyaml.NewError("bad", niceyaml.AtPosition(position.New(0, 7))))),
			want: `{
				"message": "bad",
				"name": "m.yaml",
				"range": {"start": {"line": 0, "col": 5}, "end": {"line": 0, "col": 10}}
			}`,
		},
		"an unbound Error writes its path alone": {
			node: niceyaml.NewErrorTree(badA),
			want: `{"message": "bad a", "path": "$.a"}`,
		},
		"a plain error writes its message alone": {
			node: niceyaml.NewErrorTree(errors.New("too old")),
			want: `{"message": "too old"}`,
		},
		"the zero tree writes an empty message": {
			want: `{"message": ""}`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := json.Marshal(tc.node)
			require.NoError(t, err)
			assert.JSONEq(t, tc.want, string(got))
		})
	}

	t.Run("a JSON log handler writes each problem as an object", func(t *testing.T) {
		t.Parallel()

		err := yamltest.Bind(t, source, niceyaml.NewSummary("2 problems", badA, badB))

		var buf bytes.Buffer

		slog.New(slog.NewJSONHandler(&buf, nil)).Error("decode", slog.Any("err", err),
			slog.Any("problems", slices.Collect(niceyaml.Problems(err))))

		var record struct {
			Err      string            `json:"err"`
			Problems []json.RawMessage `json:"problems"`
		}

		require.NoError(t, json.Unmarshal(buf.Bytes(), &record))

		// The error stays the string that its LogValue method gives.
		assert.Contains(t, record.Err, "f.yaml: 2 problems")
		require.Len(t, record.Problems, 2)

		assert.JSONEq(t, `{
			"message": "bad a",
			"name": "f.yaml",
			"path": "$.a",
			"range": {"start": {"line": 0, "col": 3}, "end": {"line": 0, "col": 4}}
		}`, string(record.Problems[0]))
		assert.JSONEq(t, `{
			"message": "bad b",
			"name": "f.yaml",
			"path": "$.b",
			"range": {"start": {"line": 1, "col": 3}, "end": {"line": 1, "col": 4}},
			"children": [{
				"message": "see a",
				"name": "f.yaml",
				"path": "$.a",
				"range": {"start": {"line": 0, "col": 3}, "end": {"line": 0, "col": 4}},
				"detail": true
			}]
		}`, string(record.Problems[1]))
	})

	t.Run("the range counts from 0 where the Label counts from 1", func(t *testing.T) {
		t.Parallel()

		node := niceyaml.NewErrorTree(yamltest.Bind(t, source, badA))
		assert.Contains(t, node.Label(), "1:4: $.a: bad a")

		got, err := json.Marshal(node)
		require.NoError(t, err)
		assert.Contains(t, string(got), `"range":{"start":{"line":0,"col":3}`)
	})

	t.Run("the caller sets the escaping of HTML characters", func(t *testing.T) {
		t.Parallel()

		node := niceyaml.NewErrorTree(niceyaml.NewError("want >= 2 & <= 5",
			niceyaml.WithDetails(errors.New("<a> & <b>"))))

		// The json.Marshal function escapes the characters.
		got, err := json.Marshal(node)
		require.NoError(t, err)
		assert.Contains(t, string(got), `"message":"want \u003e= 2 \u0026 \u003c= 5"`)
		assert.Contains(t, string(got), `"message":"\u003ca\u003e \u0026 \u003cb\u003e"`)

		var buf bytes.Buffer

		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		require.NoError(t, enc.Encode(node))
		assert.Contains(t, buf.String(), `"message":"want >= 2 & <= 5"`)
		assert.Contains(t, buf.String(), `"message":"<a> & <b>"`)

		// The JSON handler of log/slog does not escape them.
		buf.Reset()
		slog.New(slog.NewJSONHandler(&buf, nil)).Error("decode", slog.Any("problem", node))
		assert.Contains(t, buf.String(), `"message":"want >= 2 & <= 5"`)
		assert.Contains(t, buf.String(), `"message":"<a> & <b>"`)
	})
}

func TestProblems(t *testing.T) {
	t.Parallel()

	source := errorTreeSource()

	badA := niceyaml.NewError("bad a", niceyaml.AtPath(paths.Doc().Child("a")))
	badB := niceyaml.NewError("bad b", niceyaml.AtPath(paths.Doc().Child("b")),
		niceyaml.WithDetails(niceyaml.NewError("see a", niceyaml.AtPath(paths.Doc().Child("a")))))

	// A row joins what the report example in the docs reads from a node:
	// the file, the line, the path, the context, the message, and the
	// related messages of the details, leaving out the ones the node
	// lacks.
	rows := func(err error) []string {
		var got []string

		for problem := range niceyaml.Problems(err) {
			var parts []string

			if src := problem.Source(); src != nil {
				parts = append(parts, src.Name())
			}

			if pos, ok := problem.Position(); ok {
				parts = append(parts, fmt.Sprintf("line %d", pos.Line+1))
			}

			if path, ok := problem.Path(); ok {
				parts = append(parts, path.String())
			}

			if context := problem.Context(); context != "" {
				parts = append(parts, context)
			}

			parts = append(parts, problem.Message())

			for _, detail := range problem.Children {
				parts = append(parts, detail.Message())
			}

			got = append(got, strings.Join(parts, " | "))
		}

		return got
	}

	tcs := map[string]struct {
		err  error
		want []string
	}{
		"a bound summary yields one row per problem": {
			err: yamltest.Bind(t, source, niceyaml.NewSummary("2 problems", badB, badA)),
			want: []string{
				"f.yaml | line 1 | $.a | bad a",
				"f.yaml | line 2 | $.b | bad b | see a",
			},
		},
		"an unbound Error yields its row with no file": {
			err:  fmt.Errorf("load config: %w", badA),
			want: []string{"$.a | load config | bad a"},
		},
		"a join of a binding and a plain error": {
			err: errors.Join(yamltest.Bind(t, source, badA), errors.New("too old")),
			want: []string{
				"f.yaml | line 1 | $.a | bad a",
				"too old",
			},
		},
		"nil yields nothing": {},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, rows(tc.err))
			assert.Equal(t,
				slices.Collect(niceyaml.NewErrorTree(tc.err).Problems()),
				slices.Collect(niceyaml.Problems(tc.err)),
			)
		})
	}
}

// severityRows returns one row per problem of err: its message, then its
// severity and what [niceyaml.ErrorTree.IsInvalid] reports for it.
func severityRows(err error) []string {
	var rows []string

	for problem := range niceyaml.Problems(err) {
		severity, fault := "error", "no fault"
		if problem.Warning {
			severity = "warning"
		}

		if problem.IsInvalid() {
			fault = "invalid"
		}

		rows = append(rows, fmt.Sprintf("%s [%s, %s]", problem.Message(), severity, fault))
	}

	return rows
}

func TestProblems_WrapperBranches(t *testing.T) {
	t.Parallel()

	source := errorTreeSource()
	atA := niceyaml.AtPath(paths.Current().Child("a"))
	atB := niceyaml.AtPath(paths.Current().Child("b"))

	// Each branch that niceyaml builds is a problem of its own. A wrapper
	// with several %w verbs thus answers as a join of its branches does,
	// whether a branch carries a location or not.
	tcs := map[string]struct {
		first     error
		second    error
		want      []string
		hasErrors bool
		invalid   bool
	}{
		"an error and a warning": {
			first:  niceyaml.NewError("port is required"),
			second: niceyaml.NewWarning("legacy layout"),
			want: []string{
				"port is required [error, invalid]",
				"legacy layout [warning, invalid]",
			},
			hasErrors: true,
			invalid:   true,
		},
		"a located error and a warning": {
			first:  niceyaml.NewError("port is required", atA),
			second: niceyaml.NewWarning("legacy layout"),
			want: []string{
				"port is required [error, invalid]",
				"legacy layout [warning, invalid]",
			},
			hasErrors: true,
			invalid:   true,
		},
		"an error and a located warning": {
			first:  niceyaml.NewError("port is required"),
			second: niceyaml.NewWarning("legacy layout", atB),
			want: []string{
				"port is required [error, invalid]",
				"legacy layout [warning, invalid]",
			},
			hasErrors: true,
			invalid:   true,
		},
		"two errors": {
			first:  niceyaml.NewError("port is required"),
			second: niceyaml.NewError("host is required"),
			want: []string{
				"port is required [error, invalid]",
				"host is required [error, invalid]",
			},
			hasErrors: true,
			invalid:   true,
		},
		"two warnings": {
			first:  niceyaml.NewWarning("legacy layout"),
			second: niceyaml.NewWarning("legacy port"),
			want: []string{
				"legacy layout [warning, invalid]",
				"legacy port [warning, invalid]",
			},
			invalid: true,
		},
		"an unchecked I/O error and an error": {
			first:  niceyaml.Unchecked(fs.ErrPermission),
			second: niceyaml.NewError("port is required"),
			want: []string{
				"permission denied [error, no fault]",
				"port is required [error, invalid]",
			},
			hasErrors: true,
		},
		"a located unchecked I/O error and an error": {
			first:  niceyaml.Unchecked(fs.ErrPermission, atA),
			second: niceyaml.NewError("port is required"),
			want: []string{
				"permission denied [error, no fault]",
				"port is required [error, invalid]",
			},
			hasErrors: true,
		},
		"an unchecked I/O error and a warning": {
			first:  niceyaml.Unchecked(fs.ErrPermission),
			second: niceyaml.NewWarning("legacy layout"),
			want: []string{
				"permission denied [error, no fault]",
				"legacy layout [warning, invalid]",
			},
			hasErrors: true,
		},
		"an invalid I/O error and a warning": {
			first:  niceyaml.Invalid(fs.ErrPermission),
			second: niceyaml.NewWarning("legacy layout"),
			want: []string{
				"permission denied [error, invalid]",
				"legacy layout [warning, invalid]",
			},
			hasErrors: true,
			invalid:   true,
		},
		"a warning made an error and a warning": {
			first:  niceyaml.AsErrors(niceyaml.NewWarning("legacy port")),
			second: niceyaml.NewWarning("legacy layout"),
			want: []string{
				"legacy port [error, invalid]",
				"legacy layout [warning, invalid]",
			},
			hasErrors: true,
			invalid:   true,
		},
		"a summary and a warning": {
			first: niceyaml.NewSummary("2 problems",
				niceyaml.NewError("port is required"), niceyaml.NewError("host is required")),
			second: niceyaml.NewWarning("legacy layout"),
			want: []string{
				"port is required [error, invalid]",
				"host is required [error, invalid]",
				"legacy layout [warning, invalid]",
			},
			hasErrors: true,
			invalid:   true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			wrapped := fmt.Errorf("%w; %w", tc.first, tc.second)
			joined := errors.Join(tc.first, tc.second)

			for _, err := range []error{wrapped, joined} {
				assert.Equal(t, tc.want, severityRows(err), err.Error())
				assert.Equal(t, tc.hasErrors, niceyaml.HasErrors(err), err.Error())
				assert.Equal(t, tc.invalid, niceyaml.IsInvalid(err), err.Error())
			}

			// A binding orders its problems by position, so the rows of
			// the two bindings hold in any order.
			boundWrapped := yamltest.Bind(t, source, wrapped)
			boundJoined := yamltest.Bind(t, source, joined)
			assert.ElementsMatch(t, severityRows(boundJoined), severityRows(boundWrapped))
			assert.Len(t, severityRows(boundWrapped), len(tc.want))

			for _, err := range []error{boundWrapped, boundJoined} {
				assert.Equal(t, tc.hasErrors, niceyaml.HasErrors(err), err.Error())
				assert.Equal(t, tc.invalid, niceyaml.IsInvalid(err), err.Error())
			}
		})
	}
}

// reportExcerpts returns each excerpt of rep: its view as plain text,
// below the name of its source. The name stands in parentheses when the
// excerpt has Named unset, which has a renderer leave the name out.
func reportExcerpts(rep niceyaml.ErrorReport) []string {
	var got []string

	for _, excerpt := range rep.Excerpts {
		name := excerpt.Name
		if !excerpt.Named {
			name = "(" + name + ")"
		}

		got = append(got, name+"\n"+excerpt.View.String())
	}

	return got
}

// reportUnresolved returns the message of each unresolved binding of rep.
func reportUnresolved(rep niceyaml.ErrorReport) []string {
	var got []string

	for _, bound := range rep.Unresolved {
		got = append(got, bound.Error())
	}

	return got
}

func TestNewErrorReport(t *testing.T) {
	t.Parallel()

	first := niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("first.yaml"))
	second := niceyaml.NewSourceFromString("c: 3\n", niceyaml.WithName("second.yaml"))
	unnamed := niceyaml.NewSourceFromString("d: 4\n")
	secret := niceyaml.NewSourceFromString("e: 5\n", niceyaml.WithName("secret.yaml"), niceyaml.WithExcerpts(false))
	empty := niceyaml.NewSourceFromString("", niceyaml.WithName("empty.yaml"))

	outside := niceyaml.AtPosition(position.New(99, 0))

	badA := yamltest.Bind(t, first, yamltest.ErrorAt("bad a", "a"))
	badB := yamltest.Bind(t, first, yamltest.ErrorAt("bad b", "b"))
	badC := yamltest.Bind(t, second, yamltest.ErrorAt("bad c", "c"))
	badD := yamltest.Bind(t, unnamed, yamltest.ErrorAt("bad d", "d"))
	badE := yamltest.Bind(t, secret, yamltest.ErrorAt("bad e", "e"))
	farA := yamltest.Bind(t, first, niceyaml.NewError("far a", outside))
	farC := yamltest.Bind(t, second, niceyaml.NewError("far c", outside))
	gone := yamltest.Bind(t, second, niceyaml.NewError("gone", niceyaml.AtPath(paths.Current().Child("x").IndexAll())))

	// The excerpt of each source among several, below its name.
	namedA := stringtest.JoinLF(
		"first.yaml",
		"   1 | a: 1",
		"     |    ^ bad a",
	)
	namedC := stringtest.JoinLF(
		"second.yaml",
		"   1 | c: 3",
		"     |    ^ bad c",
	)
	// The excerpt of the first source alone, with a mark at a.
	aloneA := stringtest.JoinLF(
		"(first.yaml)",
		"   1 | a: 1",
		"     |    ^ bad a",
	)

	tcs := map[string]struct {
		err            error
		want           []string
		wantUnresolved []string
	}{
		"a lone binding has one excerpt with no name": {
			err: fmt.Errorf("check: %w", badA),
			want: []string{stringtest.JoinLF(
				"(first.yaml)",
				"   1 | a: 1",
				"     |    ^",
			)},
		},
		"bindings of one source share an excerpt with no name": {
			err: errors.Join(badA, badB),
			want: []string{stringtest.JoinLF(
				"(first.yaml)",
				"   1 | a: 1",
				"     |    ^ bad a",
				"   2 | b: 2",
				"     |    ^ bad b",
			)},
		},
		"the excerpts of several sources are named": {
			err:  errors.Join(badC, badA),
			want: []string{namedC, namedA},
		},
		"sources come in the order the bindings reach them": {
			err: errors.Join(badC, badA, badB),
			want: []string{namedC, stringtest.JoinLF(
				"first.yaml",
				"   1 | a: 1",
				"     |    ^ bad a",
				"   2 | b: 2",
				"     |    ^ bad b",
			)},
		},
		"a binding the error reaches twice marks its line once": {
			err:  errors.Join(badA, fmt.Errorf("again: %w", badA)),
			want: []string{aloneA},
		},
		"a nested binding joined beside its parent marks its line once": {
			err: errors.Join(
				yamltest.Bind(t, first, niceyaml.NewError("parent", niceyaml.WithDetails(badA))),
				badA,
			),
			want: []string{aloneA},
		},
		"a source with a path that selects nothing still names the other": {
			err:            errors.Join(badA, gone),
			want:           []string{namedA},
			wantUnresolved: []string{"second.yaml: $.x[*]: gone"},
		},
		"a source with no name is not named": {
			err: errors.Join(badA, badD),
			want: []string{namedA, stringtest.JoinLF(
				"()",
				"   1 | d: 4",
				"     |    ^ bad d",
			)},
		},
		"a source where nothing resolves still names the other": {
			err:            errors.Join(badA, farC),
			want:           []string{namedA},
			wantUnresolved: []string{"second.yaml: far c"},
		},
		"a source with excerpts off still names the other": {
			err:  errors.Join(badA, badE),
			want: []string{namedA},
		},
		"a location outside the source is unresolved": {
			err:            fmt.Errorf("check: %w", farA),
			wantUnresolved: []string{"first.yaml: far a"},
		},
		"each unresolved binding comes in the order of the bindings": {
			err:            errors.Join(farC, farA),
			wantUnresolved: []string{"second.yaml: far c", "first.yaml: far a"},
		},
		"a binding with no location has nothing to explain": {
			err: yamltest.Bind(t, first, errors.New("plain")),
		},
		"a tree in one source has one excerpt with no name": {
			err: yamltest.Bind(t, first, niceyaml.NewError("bad", niceyaml.WithDetails(
				yamltest.ErrorAt("bad a", "a"),
			))),
			want: []string{aloneA},
		},
		"a path that selects nothing is unresolved": {
			err:            gone,
			wantUnresolved: []string{"second.yaml: $.x[*]: gone"},
		},
		"a path into a document with no content has nothing to explain": {
			err: yamltest.Bind(t, empty, yamltest.ErrorAt("bad a", "a")),
		},
		"an unresolved binding whose detail resolves has an excerpt alone": {
			err: yamltest.Bind(t, first, niceyaml.NewError("far", outside,
				niceyaml.WithDetails(yamltest.ErrorAt("see a", "a")),
			)),
			want: []string{stringtest.JoinLF(
				"(first.yaml)",
				"   1 | a: 1",
				"     |    ^ see a",
			)},
		},
		"a join located at an alias reports the alias when its problems resolve nowhere": {
			// The join has no line of its own, so its location marks no
			// line, and the report explains why the alias did not resolve.
			err: yamltest.Bind(
				t,
				niceyaml.NewSourceFromString("a: *x\n", niceyaml.WithName("alias.yaml")),
				niceyaml.Invalid(
					errors.Join(niceyaml.NewError("far a", outside), niceyaml.NewError("far b", outside)),
					niceyaml.AtPath(paths.Current().Child("a", "b")),
				),
			),
			wantUnresolved: []string{"alias.yaml: far a\nalias.yaml: far b"},
		},
		"an error bound to no source has a tree alone": {
			err: errors.New("plain"),
		},
		"nil has nothing": {},
		"a nil binding has nothing": {
			err: (*niceyaml.SourceError)(nil),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rep := niceyaml.NewErrorReport(tc.err, niceyaml.WithContextLines(0))

			assert.Equal(t, tc.want, reportExcerpts(rep))
			assert.Equal(t, tc.wantUnresolved, reportUnresolved(rep))
			assert.Equal(t, textTree(niceyaml.NewErrorTree(tc.err)), textTree(rep.Tree))
		})
	}

	t.Run("an excerpt holds its source", func(t *testing.T) {
		t.Parallel()

		rep := niceyaml.NewErrorReport(errors.Join(badA, badC), niceyaml.WithContextLines(0))

		require.Len(t, rep.Excerpts, 2)
		assert.Same(t, first, rep.Excerpts[0].Source)
		assert.Same(t, second, rep.Excerpts[1].Source)
	})

	t.Run("an excerpt holds the file path of its source", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "menu.yaml")
		require.NoError(t, os.WriteFile(path, []byte("a: 1\n"), 0o600))

		// WithName gives the file another name for the report, so the
		// file path differs from the name.
		source, err := niceyaml.NewSourceFromFile(path, niceyaml.WithName("menu"))
		require.NoError(t, err)

		bound := yamltest.Bind(t, source, yamltest.ErrorAt("bad a", "a"))

		tcs := map[string]struct {
			err error
		}{
			"live":     {err: bound},
			"detached": {err: niceyaml.Detach(bound)},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				rep := niceyaml.NewErrorReport(tc.err)
				require.Len(t, rep.Excerpts, 1)
				assert.Equal(t, "menu", rep.Excerpts[0].Name)
				assert.Equal(t, path, rep.Excerpts[0].FilePath)
			})
		}
	})

	t.Run("each excerpt is a view of its own", func(t *testing.T) {
		t.Parallel()

		rep := niceyaml.NewErrorReport(errors.Join(badA, badB), niceyaml.WithContextLines(0))
		require.Len(t, rep.Excerpts, 1)

		// The source keeps no decoration from the excerpt.
		assert.Equal(t, stringtest.JoinLF(
			"   1 | a: 1",
			"   2 | b: 2",
		), first.View().String())
	})

	t.Run("an unresolved binding gives its reason", func(t *testing.T) {
		t.Parallel()

		rep := niceyaml.NewErrorReport(errors.Join(badA, farC), niceyaml.WithContextLines(0))

		require.Len(t, rep.Unresolved, 1)
		require.ErrorIs(t, rep.Unresolved[0], farC)
		require.ErrorIs(t, rep.Unresolved[0].Unresolved(), niceyaml.ErrOutOfRange)
	})

	t.Run("context keeps lines around each mark", func(t *testing.T) {
		t.Parallel()

		rep := niceyaml.NewErrorReport(badB, niceyaml.WithContextLines(1))

		want := []string{stringtest.JoinLF(
			"(first.yaml)",
			"   1 | a: 1",
			"   2 | b: 2",
			"     |    ^",
		)}

		assert.Equal(t, want, reportExcerpts(rep))
		assert.Equal(
			t,
			reportExcerpts(niceyaml.NewErrorReport(badB, niceyaml.WithContextLines(0))),
			reportExcerpts(niceyaml.NewErrorReport(badB, niceyaml.WithContextLines(-1))),
		)
	})

	t.Run("FormatError draws each part", func(t *testing.T) {
		t.Parallel()

		err := errors.Join(badA, badC, farA)
		rep := niceyaml.NewErrorReport(err, niceyaml.WithContextLines(0))

		want := stringtest.JoinLF(
			"|-- first.yaml:1:4: $.a: bad a",
			"|-- second.yaml:1:4: $.c: bad c",
			"`-- first.yaml: far a",
			"",
			"first.yaml",
			rep.Excerpts[0].View.String(),
			"",
			"second.yaml",
			rep.Excerpts[1].View.String(),
			"",
			"no excerpt: "+rep.Unresolved[0].Unresolved().Error(),
		)

		assert.Equal(t, want, niceyaml.FormatError(err, niceyaml.WithContextLines(0)))
	})
}

func TestNewErrorReport_JoinOfNothing(t *testing.T) {
	t.Parallel()

	var nilErr *niceyaml.Error

	source := niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName("f.yaml"))
	unnamed := niceyaml.NewSourceFromString("a: 1\n")

	nothing := source.Bind(errors.Join(nilErr, nilErr))
	require.Error(t, nothing)

	// The message of this binding is empty, so it has no row in a tree,
	// and its location lies outside the source.
	silent := yamltest.Bind(t, unnamed, niceyaml.NewError("", niceyaml.AtPosition(position.New(99, 0))))

	tcs := map[string]struct {
		err            error
		want           niceyaml.ErrorTree
		wantUnresolved int
	}{
		"a bound join of typed-nil errors holds its message": {
			err:  nothing,
			want: textNode("f.yaml: \n"),
		},
		"a join of typed-nil errors holds its message": {
			err:  errors.Join(nilErr, nilErr),
			want: textNode("\n"),
		},
		"an error with no message holds nothing": {
			err: errors.New(""),
		},
		"a reason below an empty tree leaves the tree empty": {
			err:            errors.Join(nothing, silent),
			wantUnresolved: 1,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rep := niceyaml.NewErrorReport(tc.err)

			// The tree of the error itself stays empty, so a report of its
			// problems lists none.
			assert.Equal(t, niceyaml.ErrorTree{}, niceyaml.NewErrorTree(tc.err))

			assert.Equal(t, tc.want, rep.Tree)
			assert.Empty(t, rep.Excerpts)
			assert.Len(t, rep.Unresolved, tc.wantUnresolved)
		})
	}
}

func TestNewErrorReport_SeveralSources(t *testing.T) {
	t.Parallel()

	manifest := niceyaml.NewSourceFromString("kind: App\nvalues: values.yaml\n", niceyaml.WithFilePath("manifest.yaml"))
	values := niceyaml.NewSourceFromString("port: many\nhost: 7\n", niceyaml.WithFilePath("values.yaml"))

	port := yamltest.Bind(t, values, yamltest.ErrorAt("not a number", "port"))
	host := yamltest.Bind(t, values, yamltest.ErrorAt("not a name", "host"))
	err := yamltest.Bind(t, manifest, niceyaml.NewError(
		"bad values",
		niceyaml.AtPath(paths.Current().Child("values")),
		niceyaml.WithDetails(port, host),
	))

	bound := yamltest.RequireSourceError(t, err)

	t.Run("one excerpt per source, the binding's source first", func(t *testing.T) {
		t.Parallel()

		rep := niceyaml.NewErrorReport(bound, niceyaml.WithContextLines(0))

		// The sibling children bound to the other source share one
		// excerpt of it, each with its message beside its line.
		assert.Equal(t, []string{
			stringtest.JoinLF(
				"manifest.yaml",
				"   2 | values: values.yaml",
				"     |         ^^^^^^^^^^^",
			),
			stringtest.JoinLF(
				"values.yaml",
				"   1 | port: many",
				"     |       ^^^^ not a number",
				"   2 | host: 7",
				"     |       ^ not a name",
			),
		}, reportExcerpts(rep))
	})

	t.Run("Excerpt keeps to the source of the binding", func(t *testing.T) {
		t.Parallel()

		excerpt, ok := bound.Excerpt(niceyaml.WithContextLines(0))
		require.True(t, ok)
		assert.Equal(t, 1, excerpt.Count())
	})

	t.Run("Annotate marks the lines of any source the view holds", func(t *testing.T) {
		t.Parallel()

		view := values.View()
		require.True(t, bound.Annotate(view), "the details are bound to the source of the view")

		assert.Equal(t, stringtest.JoinLF(
			"   1 | port: many",
			"     |       ^^^^ not a number",
			"   2 | host: 7",
			"     |       ^ not a name",
		), view.String())

		// The view of the binding's own source takes the mark of the
		// binding and none of the details.
		own := bound.Source().View()
		require.True(t, bound.Annotate(own))
		assert.Equal(t, []int{2}, yamltest.LineNumbers(own.Hunks(0)))
	})

	t.Run("FormatError renders every excerpt", func(t *testing.T) {
		t.Parallel()

		got := niceyaml.FormatError(bound, niceyaml.WithContextLines(0))
		assert.Equal(t, stringtest.JoinLF(
			"manifest.yaml:2:9: $.values: bad values",
			"|-- values.yaml:1:7: $.port: not a number",
			"`-- values.yaml:2:7: $.host: not a name",
			"",
			"manifest.yaml",
			"   2 | values: values.yaml",
			"     |         ^^^^^^^^^^^",
			"",
			"values.yaml",
			"   1 | port: many",
			"     |       ^^^^ not a number",
			"   2 | host: 7",
			"     |       ^ not a name",
		), got)
	})

	t.Run("children that alternate between sources mark their own", func(t *testing.T) {
		t.Parallel()

		srcs := make([]*niceyaml.Source, 3)
		for i := range srcs {
			srcs[i] = niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName(fmt.Sprintf("s%d.yaml", i)))
		}

		// Each source gets two children on line 1 and one on line 2, in
		// turn, so the tree leaves every source and comes back to it.
		var children []error

		for _, spec := range []struct{ key, msg string }{
			{"a", "first"}, {"a", "second"}, {"b", "third"},
		} {
			for i, src := range srcs {
				msg := fmt.Sprintf("%s %d", spec.msg, i)
				children = append(children, yamltest.Bind(t, src, yamltest.ErrorAt(msg, spec.key)))
			}
		}

		mixed := yamltest.RequireSourceError(t, yamltest.Bind(t, srcs[1], niceyaml.NewError(
			"root",
			niceyaml.AtPath(paths.Current().Child("b")),
			niceyaml.WithDetails(children...),
		)))

		// The source of the binding comes first.
		assert.Equal(t, []string{
			stringtest.JoinLF(
				"s1.yaml",
				"   1 | a: 1",
				"     |    ^ first 1; second 1",
				"   2 | b: 2",
				"     |    ^ third 1",
			),
			stringtest.JoinLF(
				"s0.yaml",
				"   1 | a: 1",
				"     |    ^ first 0; second 0",
				"   2 | b: 2",
				"     |    ^ third 0",
			),
			stringtest.JoinLF(
				"s2.yaml",
				"   1 | a: 1",
				"     |    ^ first 2; second 2",
				"   2 | b: 2",
				"     |    ^ third 2",
			),
		}, reportExcerpts(niceyaml.NewErrorReport(mixed, niceyaml.WithContextLines(0))))
	})
}

func TestNewErrorReport_ExcerptsOff(t *testing.T) {
	t.Parallel()

	secrets := niceyaml.NewSourceFromString("password: hunter2\nport: 0\n",
		niceyaml.WithName("secrets.yaml"), niceyaml.WithExcerpts(false))
	open := niceyaml.NewSourceFromString("port: 0\n", niceyaml.WithName("open.yaml"))

	port := paths.Doc().Child("port")

	badPort := yamltest.Bind(t, secrets, niceyaml.NewError("bad port", niceyaml.AtPath(port)))
	openPort := yamltest.Bind(t, open, niceyaml.NewError("open port", niceyaml.AtPath(port)))

	// The excerpt of the open source, with the message of the error at
	// its port and without it.
	marked := stringtest.JoinLF(
		"open.yaml",
		"   1 | port: 0",
		"     |       ^ open port",
	)
	bare := stringtest.JoinLF(
		"open.yaml",
		"   1 | port: 0",
		"     |       ^",
	)

	tcs := map[string]struct {
		err error
		// The excerpts of the report of err.
		want []string
		// The excerpts of the report of the first binding in err.
		wantOwn []string
	}{
		"a binding in the source yields nothing": {
			err: badPort,
		},
		"several bindings in the source yield nothing": {
			err: errors.Join(badPort, yamltest.Bind(t, secrets,
				niceyaml.NewError("too short", niceyaml.AtPath(paths.Doc().Child("password"))))),
		},
		"a binding in another source yields its excerpt": {
			err:  errors.Join(badPort, openPort),
			want: []string{marked},
		},
		"a detail in another source yields its excerpt": {
			err: yamltest.Bind(t, secrets, niceyaml.NewError(
				"ports differ", niceyaml.AtPath(port), niceyaml.WithDetails(openPort),
			)),
			want:    []string{marked},
			wantOwn: []string{marked},
		},
		"a detail in the source leaves the excerpt of its parent": {
			err: yamltest.Bind(t, open, niceyaml.NewError(
				"ports differ", niceyaml.AtPath(port), niceyaml.WithDetails(badPort),
			)),
			want:    []string{bare},
			wantOwn: []string{bare},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, reportExcerpts(niceyaml.NewErrorReport(tc.err)))

			bound := yamltest.RequireSourceError(t, tc.err)
			assert.Equal(t, tc.wantOwn, reportExcerpts(niceyaml.NewErrorReport(bound)))
		})
	}

	t.Run("Excerpt reports false for a location that resolved", func(t *testing.T) {
		t.Parallel()

		bound := yamltest.RequireSourceError(t, badPort)

		excerpt, ok := bound.Excerpt()
		assert.False(t, ok)
		assert.Nil(t, excerpt)

		// The binding still names where it is, and it has no reason to
		// print in place of the excerpt.
		assert.Equal(t, position.New(1, 6), yamltest.PositionOf(t, bound))
		require.NoError(t, bound.Unresolved())
	})

	t.Run("Annotate marks a view the caller supplies", func(t *testing.T) {
		t.Parallel()

		view := secrets.View()
		require.True(t, niceyaml.Annotate(badPort, view))

		assert.Equal(t, stringtest.JoinLF(
			"   1 | password: hunter2",
			"   2 | port: 0",
			"     |       ^ bad port",
		), view.String())
	})
}

func TestNewErrorReport_RepeatedWarnings(t *testing.T) {
	t.Parallel()

	const hours = "apiVersion: v1alpha1\nkind: Hours\n"

	docs := func(name, text string) []*niceyaml.Node {
		return niceyaml.NewSourceFromString(text, niceyaml.WithName(name)).Documents()
	}

	deprecated := func(doc *niceyaml.Node, key string) error {
		return doc.Bind(niceyaml.NewWarning(`deprecated version "v1alpha1"`,
			niceyaml.AtPath(paths.Doc().Child(key)),
			niceyaml.WithDetails(errors.New("cafe 4.0 removes v1alpha1"))))
	}

	tcs := map[string]struct {
		build func() error
		want  string
	}{
		"two files with one warning each": {
			// The second file shows only its row, so the report stays short
			// for many files that share one deprecation.
			build: func() error {
				return errors.Join(
					deprecated(docs("hours-01.yaml", hours)[0], "apiVersion"),
					deprecated(docs("hours-02.yaml", hours)[0], "apiVersion"),
				)
			},
			want: stringtest.JoinLF(
				`|-- hours-01.yaml:1:13: warning: $.apiVersion: deprecated version "v1alpha1"`,
				"|   `-- cafe 4.0 removes v1alpha1",
				"`-- hours-02.yaml:1:13: warning: $.apiVersion: deprecated version \"v1alpha1\"",
				"",
				"hours-01.yaml",
				"   1 | apiVersion: v1alpha1",
				`     |             ^^^^^^^^ warning: deprecated version "v1alpha1"`,
				"   2 | kind: Hours",
			),
		},
		"one file with the warning at two keys": {
			build: func() error {
				doc := docs("hours.yaml", hours+"baseVersion: v1alpha1\n")[0]

				return errors.Join(deprecated(doc, "apiVersion"), deprecated(doc, "baseVersion"))
			},
			want: stringtest.JoinLF(
				`|-- hours.yaml:1:13: warning: $.apiVersion: deprecated version "v1alpha1"`,
				"|   `-- cafe 4.0 removes v1alpha1",
				"`-- hours.yaml:3:14: warning: $.baseVersion: deprecated version \"v1alpha1\"",
				"",
				"   1 | apiVersion: v1alpha1",
				`     |             ^^^^^^^^ warning: deprecated version "v1alpha1"`,
				"   2 | kind: Hours",
				"   3 | baseVersion: v1alpha1",
				`     |              ^^^^^^^^ warning: deprecated version "v1alpha1"`,
			),
		},
		"two documents of one file": {
			build: func() error {
				both := docs("hours.yaml", hours+"---\n"+hours)

				return errors.Join(deprecated(both[0], "apiVersion"), deprecated(both[1], "apiVersion"))
			},
			want: stringtest.JoinLF(
				`|-- hours.yaml:1:13: warning: $.apiVersion: deprecated version "v1alpha1"`,
				"|   `-- cafe 4.0 removes v1alpha1",
				"`-- hours.yaml:4:13: warning: $.apiVersion: deprecated version \"v1alpha1\"",
				"",
				"   1 | apiVersion: v1alpha1",
				`     |             ^^^^^^^^ warning: deprecated version "v1alpha1"`,
				"   2 | kind: Hours",
				"   3 | ---",
				"   4 | apiVersion: v1alpha1",
				`     |             ^^^^^^^^ warning: deprecated version "v1alpha1"`,
				"   5 | kind: Hours",
			),
		},
		"a shared collector where the second file also holds another warning": {
			// The excerpt of b.yaml shows for its other warning, so it marks
			// the repeat too.
			build: func() error {
				a, b := docs("a.yaml", hours)[0], docs("b.yaml", hours)[0]

				return errors.Join(
					deprecated(a, "apiVersion"),
					deprecated(b, "apiVersion"),
					b.Bind(niceyaml.NewWarning("old kind", niceyaml.AtPath(paths.Doc().Child("kind")))),
				)
			},
			want: stringtest.JoinLF(
				`|-- a.yaml:1:13: warning: $.apiVersion: deprecated version "v1alpha1"`,
				"|   `-- cafe 4.0 removes v1alpha1",
				`|-- b.yaml:1:13: warning: $.apiVersion: deprecated version "v1alpha1"`,
				"`-- b.yaml:2:7: warning: $.kind: old kind",
				"",
				"a.yaml",
				"   1 | apiVersion: v1alpha1",
				`     |             ^^^^^^^^ warning: deprecated version "v1alpha1"`,
				"   2 | kind: Hours",
				"",
				"b.yaml",
				"   1 | apiVersion: v1alpha1",
				`     |             ^^^^^^^^ warning: deprecated version "v1alpha1"`,
				"   2 | kind: Hours",
				"     |       ^^^^^ warning: old kind",
			),
		},
		"a source that only a detail of another source marks": {
			// The detail gives b.yaml an excerpt, so the repeat there keeps
			// its mark too.
			build: func() error {
				a, b := docs("a.yaml", hours+"name: x\n")[0], docs("b.yaml", hours+"z: 1\n")[0]
				taken := b.Bind(niceyaml.NewError("taken here", niceyaml.AtPath(paths.Doc().Child("z"))))

				return errors.Join(
					deprecated(a, "apiVersion"),
					deprecated(b, "apiVersion"),
					a.Bind(niceyaml.NewWarning("name is taken",
						niceyaml.AtPath(paths.Doc().Child("name")), niceyaml.WithDetails(taken))),
				)
			},
			want: stringtest.JoinLF(
				`|-- a.yaml:1:13: warning: $.apiVersion: deprecated version "v1alpha1"`,
				"|   `-- cafe 4.0 removes v1alpha1",
				`|-- b.yaml:1:13: warning: $.apiVersion: deprecated version "v1alpha1"`,
				"`-- a.yaml:3:7: warning: $.name: name is taken",
				"    `-- b.yaml:3:4: $.z: taken here",
				"",
				"a.yaml",
				"   1 | apiVersion: v1alpha1",
				`     |             ^^^^^^^^ warning: deprecated version "v1alpha1"`,
				"   2 | kind: Hours",
				"   3 | name: x",
				"     |       ^ warning: name is taken",
				"",
				"b.yaml",
				"   1 | apiVersion: v1alpha1",
				`     |             ^^^^^^^^ warning: deprecated version "v1alpha1"`,
				"   2 | kind: Hours",
				"   3 | z: 1",
				"     |    ^ taken here",
			),
		},
		"an unbound first copy": {
			// The first copy does not mark an excerpt, so the bound repeat
			// marks its own in place of it.
			build: func() error {
				return errors.Join(
					niceyaml.NewWarning(`deprecated version "v1alpha1"`,
						niceyaml.AtPath(paths.Doc().Child("apiVersion")),
						niceyaml.WithDetails(errors.New("cafe 4.0 removes v1alpha1"))),
					deprecated(docs("hours.yaml", hours)[0], "apiVersion"),
				)
			},
			want: stringtest.JoinLF(
				`|-- warning: $.apiVersion: deprecated version "v1alpha1"`,
				"|   `-- cafe 4.0 removes v1alpha1",
				"`-- hours.yaml:1:13: warning: $.apiVersion: deprecated version \"v1alpha1\"",
				"",
				"   1 | apiVersion: v1alpha1",
				"     |             ^^^^^^^^",
				"   2 | kind: Hours",
			),
		},
		"unbound repeats": {
			build: func() error {
				old := func() error {
					return niceyaml.NewWarning("old", niceyaml.WithDetails(errors.New("remove it")))
				}

				return errors.Join(old(), old())
			},
			want: stringtest.JoinLF(
				"|-- warning: old",
				"|   `-- remove it",
				"`-- warning: old",
			),
		},
		"the same binding twice": {
			// The repeat is the first copy, so its mark is the mark of that
			// copy and stays.
			build: func() error {
				w := deprecated(docs("hours.yaml", hours)[0], "apiVersion")

				return errors.Join(w, w)
			},
			want: stringtest.JoinLF(
				`|-- hours.yaml:1:13: warning: $.apiVersion: deprecated version "v1alpha1"`,
				"|   `-- cafe 4.0 removes v1alpha1",
				"`-- hours.yaml:1:13: warning: $.apiVersion: deprecated version \"v1alpha1\"",
				"",
				"   1 | apiVersion: v1alpha1",
				`     |             ^^^^^^^^ warning: deprecated version "v1alpha1"`,
				"   2 | kind: Hours",
			),
		},
		"the same binding twice with a detail in another source": {
			// The detail of the repeat is the detail of the first copy, so
			// the excerpt of its source stays too.
			build: func() error {
				a, b := docs("a.yaml", hours+"name: x\n")[0], docs("b.yaml", hours+"z: 1\n")[0]
				taken := b.Bind(niceyaml.NewError("taken here", niceyaml.AtPath(paths.Doc().Child("z"))))
				w := a.Bind(niceyaml.NewWarning("name is taken",
					niceyaml.AtPath(paths.Doc().Child("name")), niceyaml.WithDetails(taken)))

				return errors.Join(w, w)
			},
			want: stringtest.JoinLF(
				"|-- a.yaml:3:7: warning: $.name: name is taken",
				"|   `-- b.yaml:3:4: $.z: taken here",
				"`-- a.yaml:3:7: warning: $.name: name is taken",
				"",
				"a.yaml",
				"   1 | apiVersion: v1alpha1",
				"   2 | kind: Hours",
				"   3 | name: x",
				"     |       ^ warning: name is taken",
				"",
				"b.yaml",
				"   1 | apiVersion: v1alpha1",
				"   2 | kind: Hours",
				"   3 | z: 1",
				"     |    ^ taken here",
			),
		},
		"separate Detach calls": {
			// Each call keeps a copy of its own source, and the repeat across
			// the copies still loses its detail and its excerpt.
			build: func() error {
				return errors.Join(
					niceyaml.Detach(deprecated(docs("hours-01.yaml", hours)[0], "apiVersion")),
					niceyaml.Detach(deprecated(docs("hours-02.yaml", hours)[0], "apiVersion")),
				)
			},
			want: stringtest.JoinLF(
				`|-- hours-01.yaml:1:13: warning: $.apiVersion: deprecated version "v1alpha1"`,
				"|   `-- cafe 4.0 removes v1alpha1",
				"`-- hours-02.yaml:1:13: warning: $.apiVersion: deprecated version \"v1alpha1\"",
				"",
				"hours-01.yaml",
				"   1 | apiVersion: v1alpha1",
				`     |             ^^^^^^^^ warning: deprecated version "v1alpha1"`,
				"   2 | kind: Hours",
			),
		},
		"two files each wrapped with their name": {
			// The text of each wrapper stays out of the match, so the second
			// file still shows only its row.
			build: func() error {
				return errors.Join(
					fmt.Errorf("check hours-01.yaml: %w", deprecated(docs("hours-01.yaml", hours)[0], "apiVersion")),
					fmt.Errorf("check hours-02.yaml: %w", deprecated(docs("hours-02.yaml", hours)[0], "apiVersion")),
				)
			},
			want: stringtest.JoinLF(
				`|-- hours-01.yaml:1:13: warning: $.apiVersion: check hours-01.yaml: deprecated version "v1alpha1"`,
				"|   `-- cafe 4.0 removes v1alpha1",
				"`-- hours-02.yaml:1:13: warning: $.apiVersion: check hours-02.yaml: deprecated version \"v1alpha1\"",
				"",
				"hours-01.yaml",
				"   1 | apiVersion: v1alpha1",
				`     |             ^^^^^^^^ warning: check hours-01.yaml: deprecated version "v1alpha1"`,
				"   2 | kind: Hours",
			),
		},
		"two files each wrapped with their name and bound again": {
			// Each source binds the wrapper of its file again inside a join,
			// and the text of the wrapper still stays out of the match.
			build: func() error {
				one, two := docs("hours-01.yaml", hours)[0], docs("hours-02.yaml", hours)[0]

				return errors.Join(
					one.Bind(errors.Join(fmt.Errorf("check hours-01.yaml: %w", deprecated(one, "apiVersion")))),
					two.Bind(errors.Join(fmt.Errorf("check hours-02.yaml: %w", deprecated(two, "apiVersion")))),
				)
			},
			want: stringtest.JoinLF(
				`|-- hours-01.yaml:1:13: warning: $.apiVersion: check hours-01.yaml: deprecated version "v1alpha1"`,
				"|   `-- cafe 4.0 removes v1alpha1",
				"`-- hours-02.yaml:1:13: warning: $.apiVersion: check hours-02.yaml: deprecated version \"v1alpha1\"",
				"",
				"hours-01.yaml",
				"   1 | apiVersion: v1alpha1",
				`     |             ^^^^^^^^ warning: check hours-01.yaml: deprecated version "v1alpha1"`,
				"   2 | kind: Hours",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := tc.build()
			assert.Equal(t, tc.want, niceyaml.FormatError(err))

			// The tree of the error keeps every detail.
			tree := niceyaml.NewErrorTree(err)
			require.GreaterOrEqual(t, len(tree.Children), 2)
			assert.Len(t, tree.Children[1].Children, 1)
			assert.True(t, tree.Warning)
		})
	}
}
