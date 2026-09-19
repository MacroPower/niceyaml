package errortree_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/errortree"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

// countNodes returns the number of nodes with text in t.
func countNodes(t errortree.Tree) int {
	n := 0
	if t.Text != "" {
		n++
	}

	for _, child := range t.Children {
		n += countNodes(child)
	}

	return n
}

func TestNew_MultiWrap(t *testing.T) {
	t.Parallel()

	errA := errors.New("first")
	errB := errors.New("second")

	tcs := map[string]struct {
		err  error
		want errortree.Tree
	}{
		"wrapper with two verbs keeps its own text": {
			err: fmt.Errorf("parse path: %w: %w", errA, errB),
			want: errortree.Tree{
				Text: "parse path: first: second",
				Children: []errortree.Tree{
					{Text: "first"},
					{Text: "second"},
				},
			},
		},
		"join below a wrapper yields one child per branch": {
			err: fmt.Errorf("while checking: %w", errors.Join(errA, errB)),
			want: errortree.Tree{
				Text: "while checking: first\nsecond",
				Children: []errortree.Tree{
					{Text: "first"},
					{Text: "second"},
				},
			},
		},
		"join at the root is textless": {
			err: errors.Join(errA, errB),
			want: errortree.Tree{
				Children: []errortree.Tree{
					{Text: "first"},
					{Text: "second"},
				},
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, errortree.New(tc.err))
		})
	}
}

func TestNew(t *testing.T) {
	t.Parallel()

	source := niceyaml.NewSourceFromString("a: 1\nb: 2\n", niceyaml.WithName("f.yaml"))
	other := niceyaml.NewSourceFromString("c: 3\n", niceyaml.WithName("g.yaml"))

	badA := func() *niceyaml.Error {
		return niceyaml.NewError("bad a", niceyaml.WithPath(paths.Root().Child("a")))
	}
	badB := func() *niceyaml.Error {
		return niceyaml.NewError("bad b", niceyaml.WithPath(paths.Root().Child("b")))
	}

	tcs := map[string]struct {
		err  error
		want errortree.Tree
		// The multiLine flag marks an error whose message holds a line that
		// is not a node of its own, so the node count check does not apply.
		multiLine bool
	}{
		"nil": {
			err:  nil,
			want: errortree.Tree{},
		},
		"nil Error": {
			err:  (*niceyaml.Error)(nil),
			want: errortree.Tree{},
		},
		"plain error": {
			err:  errors.New("boom"),
			want: errortree.Tree{Text: "boom"},
		},
		"error without nested errors": {
			err:  badA(),
			want: errortree.Tree{Text: "$.a: bad a"},
		},
		"nested errors are children": {
			err: niceyaml.NewError("2 problems", niceyaml.WithErrors(badA(), niceyaml.NewError("no path"))),
			want: errortree.Tree{
				Text: "2 problems",
				Children: []errortree.Tree{
					{Text: "$.a: bad a"},
					{Text: "no path"},
				},
			},
		},
		"bound children carry their position without the name": {
			err: yamltest.Bind(t, source, niceyaml.NewError("2 problems", niceyaml.WithErrors(badA(), badB()))),
			want: errortree.Tree{
				Text: "f.yaml: 2 problems",
				Children: []errortree.Tree{
					{Text: "1:4: $.a: bad a"},
					{Text: "2:4: $.b: bad b"},
				},
			},
		},
		"children sort by position": {
			err: yamltest.Bind(t, source, niceyaml.NewError("2 problems", niceyaml.WithErrors(badB(), badA()))),
			want: errortree.Tree{
				Text: "f.yaml: 2 problems",
				Children: []errortree.Tree{
					{Text: "1:4: $.a: bad a"},
					{Text: "2:4: $.b: bad b"},
				},
			},
		},
		"children without a position follow the positioned ones": {
			err: yamltest.Bind(t, source, niceyaml.NewError("3 problems", niceyaml.WithErrors(
				niceyaml.NewError("bad x", niceyaml.WithPath(paths.Root().Child("x"))),
				niceyaml.NewError("no path"),
				badA(),
			))),
			want: errortree.Tree{
				Text: "f.yaml: 3 problems",
				Children: []errortree.Tree{
					{Text: "1:4: $.a: bad a"},
					{Text: "$.x: bad x"},
					{Text: "no path"},
				},
			},
		},
		"root with a position keeps it": {
			err: yamltest.Bind(t, source, niceyaml.NewError("outer",
				niceyaml.WithPath(paths.Root().Child("a")),
				niceyaml.WithErrors(badB()),
			)),
			want: errortree.Tree{
				Text: "f.yaml:1:4: $.a: outer",
				Children: []errortree.Tree{
					{Text: "2:4: $.b: bad b"},
				},
			},
		},
		"wrapper context stays in front of the root": {
			err: fmt.Errorf("document 0: %w", yamltest.Bind(t, source,
				niceyaml.NewError("2 problems", niceyaml.WithErrors(badA(), badB())),
			)),
			want: errortree.Tree{
				Text: "document 0: f.yaml: 2 problems",
				Children: []errortree.Tree{
					{Text: "1:4: $.a: bad a"},
					{Text: "2:4: $.b: bad b"},
				},
			},
		},
		"wrapper inside the binding stays in front of the root": {
			err: yamltest.Bind(t, source, fmt.Errorf("document 0: %w",
				niceyaml.NewError("2 problems", niceyaml.WithErrors(badA(), badB())),
			)),
			want: errortree.Tree{
				Text: "f.yaml: document 0: 2 problems",
				Children: []errortree.Tree{
					{Text: "1:4: $.a: bad a"},
					{Text: "2:4: $.b: bad b"},
				},
			},
		},
		"nested error with nested errors is a subtree": {
			err: yamltest.Bind(t, source, niceyaml.NewError("outer", niceyaml.WithErrors(
				niceyaml.NewError("mid",
					niceyaml.WithPath(paths.Root().Child("a")),
					niceyaml.WithErrors(badB()),
				),
			))),
			want: errortree.Tree{
				Text: "f.yaml: outer",
				Children: []errortree.Tree{
					{
						Text: "1:4: $.a: mid",
						Children: []errortree.Tree{
							{Text: "2:4: $.b: bad b"},
						},
					},
				},
			},
		},
		"nested error bound to another source keeps its own positions": {
			err: yamltest.Bind(t, source, niceyaml.NewError("outer", niceyaml.WithErrors(
				niceyaml.NewErrorFrom(yamltest.Bind(t, other,
					niceyaml.NewError("inner", niceyaml.WithErrors(
						niceyaml.NewError("bad c", niceyaml.WithPath(paths.Root().Child("c"))),
					)),
				)),
			))),
			want: errortree.Tree{
				Text: "f.yaml: outer",
				Children: []errortree.Tree{
					{
						Text: "g.yaml: inner",
						Children: []errortree.Tree{
							{Text: "1:4: $.c: bad c"},
						},
					},
				},
			},
		},
		"binding rebound to another binding keeps the inner positions": {
			err: yamltest.Bind(t, source, fmt.Errorf("outer: %w", yamltest.Bind(t, other,
				niceyaml.NewError("inner", niceyaml.WithErrors(
					niceyaml.NewError("bad c", niceyaml.WithPath(paths.Root().Child("c"))),
				)),
			))),
			want: errortree.Tree{
				Text: "outer: g.yaml: inner",
				Children: []errortree.Tree{
					{Text: "1:4: $.c: bad c"},
				},
			},
		},
		"located error above an inner binding positions its own nested errors": {
			err: yamltest.Bind(t, source, niceyaml.NewErrorFrom(
				yamltest.Bind(t, other, niceyaml.NewError("inner", niceyaml.WithErrors(
					niceyaml.NewError("bad c", niceyaml.WithPath(paths.Root().Child("c"))),
				))),
				niceyaml.WithPath(paths.Root().Child("a")),
				niceyaml.WithErrors(badB()),
			)),
			want: errortree.Tree{
				Text: "f.yaml:1:4: $.a: g.yaml: inner",
				Children: []errortree.Tree{
					{Text: "g.yaml:1:4: $.c: bad c"},
					{Text: "2:4: $.b: bad b"},
				},
			},
		},
		"joined errors form a forest": {
			err: errors.Join(
				yamltest.Bind(t, source, badA()),
				yamltest.Bind(t, other, niceyaml.NewError("bad c", niceyaml.WithPath(paths.Root().Child("c")))),
			),
			want: errortree.Tree{
				Children: []errortree.Tree{
					{Text: "f.yaml:1:4: $.a: bad a"},
					{Text: "g.yaml:1:4: $.c: bad c"},
				},
			},
		},
		"joined errors with nested errors are subtrees": {
			err: errors.Join(
				yamltest.Bind(t, source, niceyaml.NewError("2 problems", niceyaml.WithErrors(badA(), badB()))),
				yamltest.Bind(t, other, niceyaml.NewError("bad c", niceyaml.WithPath(paths.Root().Child("c")))),
			),
			want: errortree.Tree{
				Children: []errortree.Tree{
					{
						Text: "f.yaml: 2 problems",
						Children: []errortree.Tree{
							{Text: "1:4: $.a: bad a"},
							{Text: "2:4: $.b: bad b"},
						},
					},
					{Text: "g.yaml:1:4: $.c: bad c"},
				},
			},
		},
		"bound join is a forest of named bindings": {
			err: yamltest.Bind(t, source, errors.Join(badB(), badA())),
			want: errortree.Tree{
				Children: []errortree.Tree{
					{Text: "f.yaml:1:4: $.a: bad a"},
					{Text: "f.yaml:2:4: $.b: bad b"},
				},
			},
			multiLine: true,
		},
		"bound join below a wrapper keeps the wrapper as the root": {
			err: yamltest.Bind(t, source, fmt.Errorf("ctx: %w", errors.Join(badA(), badB()))),
			want: errortree.Tree{
				Text: "f.yaml: ctx: $.a: bad a\n$.b: bad b",
				Children: []errortree.Tree{
					{Text: "1:4: $.a: bad a"},
					{Text: "2:4: $.b: bad b"},
				},
			},
			multiLine: true,
		},
		"join of one error is that error": {
			err:  errors.Join(errors.Join(errors.New("boom"))),
			want: errortree.Tree{Text: "boom"},
		},
		"nested joins flatten into one forest": {
			err: errors.Join(
				errors.Join(errors.New("one"), errors.New("two")),
				nil,
				errors.New("three"),
			),
			want: errortree.Tree{
				Children: []errortree.Tree{
					{Text: "one"},
					{Text: "two"},
					{Text: "three"},
				},
			},
		},
		"error without a message and nested errors is a forest": {
			err: niceyaml.NewErrorFrom(nil, niceyaml.WithErrors(
				niceyaml.NewError("one"),
				niceyaml.NewError("two"),
			)),
			want: errortree.Tree{
				Children: []errortree.Tree{
					{Text: "one"},
					{Text: "two"},
				},
			},
		},
		"nil nested errors are skipped": {
			err:  niceyaml.NewError("main", niceyaml.WithErrors(nil, niceyaml.NewError("one"), nil)),
			want: errortree.Tree{Text: "main", Children: []errortree.Tree{{Text: "one"}}},
		},
		"rewritten message keeps its children": {
			err:  yamltest.RewriteError{Err: niceyaml.NewError("main", niceyaml.WithErrors(badA()))},
			want: errortree.Tree{Text: "rewritten", Children: []errortree.Tree{{Text: "$.a: bad a"}}},
		},
		"rewritten bound message keeps its children with positions": {
			err: yamltest.Bind(
				t,
				source,
				yamltest.RewriteError{Err: niceyaml.NewError("main", niceyaml.WithErrors(badA()))},
			),
			want: errortree.Tree{
				Text:     "f.yaml: rewritten",
				Children: []errortree.Tree{{Text: "1:4: $.a: bad a"}},
			},
		},
		"multi-line nested message stays whole": {
			err: yamltest.Bind(t, source, niceyaml.NewError("outer", niceyaml.WithErrors(
				niceyaml.NewError("bad a\n  see docs", niceyaml.WithPath(paths.Root().Child("a"))),
			))),
			want: errortree.Tree{
				Text: "f.yaml: outer",
				Children: []errortree.Tree{
					{Text: "1:4: $.a: bad a\n  see docs"},
				},
			},
			multiLine: true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := errortree.New(tc.err)
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
