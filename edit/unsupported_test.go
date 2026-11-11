package edit_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/edit"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

func TestUnsupportedError(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		run       func(t *testing.T) error
		construct edit.Construct
		fix       string
		// The messages of the details of the problem that have a place,
		// which come before the fix.
		sites []string
		// The messages of the details after the fix, which name calls.
		calls   []string
		inValue bool
	}{
		"anchor that the document defines twice": {
			run: func(t *testing.T) error {
				t.Helper()

				doc := yamltest.FirstDocument(t, stringtest.LinesLF("a: &x 1", "b: &x 2"),
					niceyaml.WithName("test.yaml"))
				_, err := edit.New(doc)

				return err //nolint:wrapcheck // The test reads the error of New.
			},
			construct: edit.ConstructReusedAnchor,
			fix:       "rename one of the two anchors and the aliases that read it",
			sites:     []string{"&x is defined here"},
		},
		"comment inside a flow sequence that a Set reaches": {
			run: func(t *testing.T) error {
				t.Helper()

				d := newDoc(t, stringtest.LinesLF("a: [1, # one", "  2]"))

				return applyAt(t, d, paths.Doc().Child("a").Index(0), func(ctx context.Context, n *edit.Node) error {
					return n.Set(ctx, 3)
				})
			},
			construct: edit.ConstructFlowLayout,
			fix:       "move the comment out of the flow collection",
			sites:     []string{"the comment is here"},
			calls:     []string{"cafe.jacobcolvin.com/v1 sets $.a[0]"},
		},
		"anchor in the YAML of a Set value": {
			run: func(t *testing.T) error {
				t.Helper()

				d := newDoc(t, "a: 1\nb: 2\n")

				return applyAt(t, d, paths.Doc().Child("b"), func(ctx context.Context, n *edit.Node) error {
					return n.Set(ctx, anchoredFields{A: map[string]int{"k": 1}, B: map[string]int{"k": 1}})
				})
			},
			construct: edit.ConstructValueAlias,
			fix:       "remove the anchor and alias options from the yaml tags of the fields of the value",
			calls:     []string{"cafe.jacobcolvin.com/v1 sets $.b from this value"},
			inValue:   true,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := tc.run(t)
			require.ErrorIs(t, err, edit.ErrUnsupported)

			var unsupported *edit.UnsupportedError

			require.ErrorAs(t, err, &unsupported)
			assert.Equal(t, tc.construct, unsupported.Construct)
			assert.Equal(t, tc.fix, unsupported.Fix)
			assert.Equal(t, tc.inValue, unsupported.InValue)

			var sites, fixes, calls []string

			for _, c := range unsupportedProblem(t, err, unsupported).Children {
				require.True(t, c.Detail)

				_, located := c.Position()

				switch {
				case located:
					require.Empty(t, fixes, "a related site comes before the fix")

					sites = append(sites, c.Message())

				case fixes == nil:
					fixes = append(fixes, c.Message())
				default:
					calls = append(calls, c.Message())
				}
			}

			assert.Equal(t, tc.sites, sites)
			assert.Equal(t, []string{tc.fix}, fixes)
			assert.Equal(t, tc.calls, calls)
		})
	}
}

// unsupportedProblem returns the node of the [niceyaml.ErrorTree] of err
// that stands for the problem of unsupported, an error that err holds.
func unsupportedProblem(t *testing.T, err error, unsupported *edit.UnsupportedError) niceyaml.ErrorTree {
	t.Helper()

	for p := range niceyaml.NewErrorTree(err).Problems() {
		var u *edit.UnsupportedError

		if errors.As(p.Err, &u) && u == unsupported {
			return p
		}
	}

	require.FailNow(t, "the error tree does not hold the problem of the UnsupportedError")

	return niceyaml.ErrorTree{}
}

// assertFix checks that the first detail of the problem of unsupported,
// an error that err holds, without a place, holds the words of its Fix.
func assertFix(t *testing.T, err error, unsupported *edit.UnsupportedError) {
	t.Helper()

	require.NotEmpty(t, unsupported.Fix)

	for _, c := range unsupportedProblem(t, err, unsupported).Children {
		if _, located := c.Position(); c.Detail && !located {
			assert.Equal(t, unsupported.Fix, c.Message())

			return
		}
	}

	assert.Fail(t, "the problem does not have a detail without a place")
}
