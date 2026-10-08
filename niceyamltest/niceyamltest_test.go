package niceyamltest_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/niceyamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

// menu holds a list whose second item has the name [reservedName] rejects.
const menu = "name: lunch\nitems:\n  - name: soup\n  - name: admin\n"

// reservedName is a [niceyaml.Validator] of a type of its own that rejects
// the name admin. It leaves its error unbound when unbound is set, and
// binds it through the Node of the name otherwise.
type reservedName struct {
	unbound bool
}

func (r reservedName) Validate(ctx context.Context, n *niceyaml.Node) error {
	namePath := paths.Current().Child("name")

	node, err := n.At(namePath)
	if err != nil {
		return err //nolint:wrapcheck // The validator passes the error on.
	}

	name, err := node.Decode[string](ctx)
	if err != nil {
		return err //nolint:wrapcheck // The validator passes the error on.
	}

	switch {
	case name != "admin":
		return nil

	case r.unbound:
		return niceyaml.NewError("reserved name", niceyaml.AtPath(namePath))

	default:
		return node.NewError("reserved name") //nolint:wrapcheck // The Node binds the error already.
	}
}

// eachItem is a [niceyaml.Validator] that runs rule on every item of the
// list at $.items. It calls the Validate method of rule itself when direct
// is set, and runs rule through [niceyaml.Node.Validate] otherwise.
type eachItem struct {
	rule   niceyaml.Validator
	direct bool
}

func (e eachItem) Validate(ctx context.Context, n *niceyaml.Node) error {
	items, err := n.Nodes(paths.Current().Child("items").IndexAll())
	if err != nil {
		return err //nolint:wrapcheck // The validator passes the error on.
	}

	for _, item := range items {
		if e.direct {
			err = e.rule.Validate(ctx, item)
		} else {
			err = item.Validate(ctx, e.rule)
		}

		if err != nil {
			return err //nolint:wrapcheck // The validator passes the error on.
		}
	}

	return nil
}

func ExampleCheckBound() {
	ctx := context.Background()

	doc, err := niceyaml.NewSourceFromString(menu, niceyaml.WithName("c.yaml")).Document()
	if err != nil {
		log.Fatal(err)
	}

	item, err := doc.At(paths.Doc().Child("items").Index(1))
	if err != nil {
		log.Fatal(err)
	}

	rule := reservedName{unbound: true}

	// Node.Validate binds the error the rule left unbound.
	fmt.Println(niceyamltest.CheckBound(item.Validate(ctx, rule)))

	// A direct call returns the error as the rule left it.
	fmt.Println(niceyamltest.CheckBound(rule.Validate(ctx, item)))

	// Output:
	// <nil>
	// bound to no source: "reserved name"
}

func TestCheckBound(t *testing.T) {
	t.Parallel()

	namePath := paths.Current().Child("name")
	reserved := niceyaml.NewError("reserved name", niceyaml.AtPath(namePath))

	var (
		nilError       *niceyaml.Error
		nilSourceError *niceyaml.SourceError
	)

	tcs := map[string]struct {
		// Build returns the error to check, given the Node a validator
		// binds its errors through.
		build func(item *niceyaml.Node) error
		// The message of the result, which is empty when the check passes.
		want string
	}{
		"no error": {
			build: func(*niceyaml.Node) error { return nil },
		},
		"nil Error pointer": {
			build: func(*niceyaml.Node) error { return nilError },
		},
		"nil SourceError pointer": {
			build: func(*niceyaml.Node) error { return nilSourceError },
		},
		"bound through the node": {
			build: func(item *niceyaml.Node) error { return item.Bind(reserved) },
		},
		"bound through the document": {
			build: func(item *niceyaml.Node) error { return item.Document().Bind(reserved) },
		},
		"built through the node": {
			build: func(item *niceyaml.Node) error { return item.NewError("bad item") },
		},
		"context around a bound error": {
			build: func(item *niceyaml.Node) error { return fmt.Errorf("rule: %w", item.Bind(reserved)) },
		},
		"fault declared above a bound error": {
			build: func(item *niceyaml.Node) error { return niceyaml.Invalid(item.Bind(reserved)) },
		},
		"join of bound errors": {
			build: func(item *niceyaml.Node) error {
				return errors.Join(item.Bind(reserved), item.NewError("bad item"))
			},
		},
		// A message lists [niceyaml.ErrorListLimit] errors and counts the
		// rest on a line that names the source of the binding.
		"join of more bound errors than a message lists": {
			build: func(item *niceyaml.Node) error {
				errs := make([]error, 0, niceyaml.ErrorListLimit+2)
				for i := range cap(errs) {
					errs = append(errs, item.NewError(fmt.Sprintf("bad item %d", i)))
				}

				return errors.Join(errs...)
			},
		},
		"bound detail of a bound error": {
			build: func(item *niceyaml.Node) error {
				return item.Invalid(errors.New("bad item"), niceyaml.WithDetails(reserved))
			},
		},
		"@ path": {
			build: func(*niceyaml.Node) error { return reserved },
			want:  `bound to no source: "reserved name"`,
		},
		"$ path": {
			build: func(*niceyaml.Node) error {
				return niceyaml.NewError("reserved name", niceyaml.AtPath(paths.Doc().Child("name")))
			},
			want: `bound to no source: "reserved name"`,
		},
		"no location": {
			build: func(*niceyaml.Node) error { return errors.New("bad item") },
			want:  `bound to no source: "bad item"`,
		},
		"Rebase result": {
			build: func(item *niceyaml.Node) error { return niceyaml.Rebase(reserved, item.Path()) },
			want:  `bound to no source: "reserved name"`,
		},
		"BindValue result": {
			build: func(*niceyaml.Node) error { return niceyaml.BindValue(reserved) },
			want:  `bound to no source: "reserved name"`,
		},
		"join of a bound error and an unbound one": {
			build: func(item *niceyaml.Node) error { return errors.Join(item.Bind(reserved), errors.New("bad item")) },
			want:  `bound to no source: "bad item"`,
		},
		"join of unbound errors": {
			build: func(*niceyaml.Node) error { return errors.Join(reserved, errors.New("bad item")) },
			want:  `bound to no source: "reserved name", "bad item"`,
		},
		"unbound summary of bound errors": {
			build: func(item *niceyaml.Node) error {
				return niceyaml.NewSummary("2 problems", item.Bind(reserved), item.NewError("bad item"))
			},
			want: `bound to no source: "2 problems"`,
		},
		"location above a bound error": {
			build: func(item *niceyaml.Node) error {
				return niceyaml.Invalid(item.Bind(reserved), niceyaml.AtPath(namePath))
			},
			want: `bound to no source: "c.yaml:4:11: $.items[1].name: reserved name"`,
		},
		"unbound detail of a bound error": {
			build: func(item *niceyaml.Node) error {
				return niceyaml.Invalid(item.Bind(errors.New("bad item")), niceyaml.WithDetails(reserved))
			},
			want: `bound to no source: "reserved name"`,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, menu, niceyaml.WithName("c.yaml"))
			item := yamltest.At(t, doc, paths.Doc().Child("items").Index(1))

			err := niceyamltest.CheckBound(tc.build(item))

			if tc.want == "" {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, niceyamltest.ErrUnbound)
			assert.EqualError(t, err, tc.want)
		})
	}
}

// TestCheckBound_Validator calls the Validate method of each validator
// itself, as a test of a validator does.
func TestCheckBound_Validator(t *testing.T) {
	t.Parallel()

	itemPath := paths.Doc().Child("items").Index(1)

	tcs := map[string]struct {
		v niceyaml.Validator
		// The Node the validator runs on.
		path paths.Path
		// The message of the result, which is empty when the check passes.
		want string
	}{
		"rule that binds through its Node": {
			v:    reservedName{},
			path: itemPath,
		},
		"rule that binds nothing": {
			v:    reservedName{unbound: true},
			path: itemPath,
			want: `bound to no source: "reserved name"`,
		},
		"ValidatorFunc around a rule that binds nothing": {
			v:    niceyaml.ValidatorFunc(reservedName{unbound: true}.Validate),
			path: itemPath,
		},
		"composer that calls a rule that binds nothing": {
			v:    eachItem{rule: reservedName{unbound: true}, direct: true},
			path: paths.Doc(),
			want: `bound to no source: "reserved name"`,
		},
		"composer that runs that rule through each Node": {
			v:    eachItem{rule: reservedName{unbound: true}},
			path: paths.Doc(),
		},
		"composer that calls a rule that binds": {
			v:    eachItem{rule: reservedName{}, direct: true},
			path: paths.Doc(),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			doc := yamltest.FirstDocument(t, menu, niceyaml.WithName("c.yaml"))
			node := yamltest.At(t, doc, tc.path)

			// Node.Validate binds what the validator left unbound, so the
			// check passes for every validator that runs through it.
			bound := node.Validate(t.Context(), tc.v)
			require.Error(t, bound)
			require.NoError(t, niceyamltest.CheckBound(bound))

			err := niceyamltest.CheckBound(tc.v.Validate(t.Context(), node))

			if tc.want == "" {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, niceyamltest.ErrUnbound)
			assert.EqualError(t, err, tc.want)
		})
	}
}
