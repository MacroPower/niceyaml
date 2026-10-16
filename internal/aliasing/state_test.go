package aliasing_test

import (
	"errors"
	"testing"

	"github.com/goccy/go-yaml/ast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/aliasing"
	"go.jacobcolvin.com/niceyaml/internal/aliaslimit"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

func TestStateOf(t *testing.T) {
	t.Parallel()

	docs := yamltest.Documents(t, niceyaml.NewSourceFromString(stringtest.LinesLF("a: [1, 2]", "---", "b: 3")), 2)

	first := aliasing.StateOf(docs[0])
	require.NotNil(t, first)

	scoped := yamltest.At(t, docs[0], paths.Current().Child("a").Index(1))

	assert.Same(t, first, aliasing.StateOf(scoped))
	assert.NotSame(t, first, aliasing.StateOf(docs[1]))
	assert.Nil(t, aliasing.StateOf((*niceyaml.Node)(nil)))
	assert.PanicsWithValue(t, "aliasing.StateOf: string is not a *niceyaml.Node", func() {
		aliasing.StateOf("a")
	})
}

func TestState_AliasLimit(t *testing.T) {
	t.Parallel()

	assert.True(t, aliasing.NewState(true, aliasing.Budget{}, nil).AliasLimit())
	assert.False(t, aliasing.NewState(false, aliasing.Budget{}, nil).AliasLimit())
}

func TestState_Budget(t *testing.T) {
	t.Parallel()

	budget := aliasing.Budget{Bytes: 1024, Depth: 10}

	assert.Equal(t, budget, aliasing.NewState(true, budget, nil).Budget())
}

func TestState_Facts(t *testing.T) {
	t.Parallel()

	calls := 0
	doc := yamltest.FirstDocument(t, stringtest.LinesLF("a: &x 1", "b: *x"))

	s := aliasing.NewState(true, aliasing.Budget{}, func() (ast.Node, *paths.Resolver) {
		calls++

		return doc.AST(), doc.Resolver()
	})

	first := s.Facts()
	require.Len(t, first.Aliases, 1)

	// Every call returns the facts of the first.
	assert.Same(t, first, s.Facts())
	assert.Equal(t, 1, calls)
}

func TestState_KeepsFirstResult(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		err  error
		keep func(s *aliasing.State, check func() error) error
	}{
		"excessive aliasing":    {keep: (*aliasing.State).ExcessiveAliasing, err: aliaslimit.ErrExcessiveAliasing},
		"no excessive aliasing": {keep: (*aliasing.State).ExcessiveAliasing},
		"excessive text aliasing": {
			keep: (*aliasing.State).ExcessiveTextAliasing,
			err:  aliaslimit.ErrExcessiveAliasing,
		},
		"no excessive text aliasing": {keep: (*aliasing.State).ExcessiveTextAliasing},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s := aliasing.NewState(true, aliasing.Budget{}, nil)

			calls := 0
			check := func() error {
				calls++

				return tc.err
			}

			assert.Equal(t, tc.err, tc.keep(s, check))
			assert.Equal(t, tc.err, tc.keep(s, check))
			assert.Equal(t, 1, calls)
		})
	}
}

func TestState_ExcessiveTextAliasing(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		value error
		text  error
	}{
		"text count excessive":  {text: aliaslimit.ErrExcessiveAliasing},
		"value count excessive": {value: aliaslimit.ErrExcessiveAliasing},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s := aliasing.NewState(true, aliasing.Budget{}, nil)

			// The State keeps the text result apart from the value result,
			// so neither count reads the result of the other.
			assert.Equal(t, tc.value, s.ExcessiveAliasing(func() error { return tc.value }))
			assert.Equal(t, tc.text, s.ExcessiveTextAliasing(func() error { return tc.text }))
			assert.Equal(t, tc.value, s.ExcessiveAliasing(func() error { return errors.New("second count") }))
		})
	}
}
