package aliasing_test

import (
	"testing"

	"github.com/goccy/go-yaml/ast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/aliasing"
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

	assert.True(t, aliasing.NewState(true, nil).AliasLimit())
	assert.False(t, aliasing.NewState(false, nil).AliasLimit())
}

func TestState_Facts(t *testing.T) {
	t.Parallel()

	calls := 0
	doc := yamltest.FirstDocument(t, stringtest.LinesLF("a: &x 1", "b: *x"))

	s := aliasing.NewState(true, func() (ast.Node, *paths.Resolver) {
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
		keep func(s *aliasing.State, check func() bool) bool
		want bool
	}{
		"excessive aliasing":         {keep: (*aliasing.State).ExcessiveAliasing, want: true},
		"no excessive aliasing":      {keep: (*aliasing.State).ExcessiveAliasing, want: false},
		"excessive text aliasing":    {keep: (*aliasing.State).ExcessiveTextAliasing, want: true},
		"no excessive text aliasing": {keep: (*aliasing.State).ExcessiveTextAliasing, want: false},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s := aliasing.NewState(true, nil)

			calls := 0
			check := func() bool {
				calls++

				return tc.want
			}

			assert.Equal(t, tc.want, tc.keep(s, check))
			assert.Equal(t, tc.want, tc.keep(s, check))
			assert.Equal(t, 1, calls)
		})
	}
}

func TestState_ExcessiveTextAliasing(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		value bool
		text  bool
	}{
		"text count excessive":  {text: true},
		"value count excessive": {value: true},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s := aliasing.NewState(true, nil)

			// The State keeps the text verdict apart from the value
			// verdict, so neither count reads the verdict of the other.
			assert.Equal(t, tc.value, s.ExcessiveAliasing(func() bool { return tc.value }))
			assert.Equal(t, tc.text, s.ExcessiveTextAliasing(func() bool { return tc.text }))
			assert.Equal(t, tc.value, s.ExcessiveAliasing(func() bool { return !tc.value }))
		})
	}
}
