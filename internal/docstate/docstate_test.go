package docstate_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/docstate"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

func TestOf(t *testing.T) {
	t.Parallel()

	docs, err := niceyaml.NewSourceFromString("a: [1, 2]\n---\nb: 3\n").Documents()
	require.NoError(t, err)
	require.Len(t, docs, 2)

	first := docstate.Of(docs[0])
	require.NotNil(t, first)

	scoped := yamltest.At(t, docs[0], paths.Root().Child("a").Index(1))

	assert.Same(t, first, docstate.Of(scoped))
	assert.NotSame(t, first, docstate.Of(docs[1]))
	assert.Same(t, first.Resolver(), docstate.Of(scoped).Resolver())
	assert.Nil(t, docstate.Of((*niceyaml.Node)(nil)))
	assert.PanicsWithValue(t, "docstate.Of: string is not a *niceyaml.Node", func() {
		docstate.Of("a")
	})
}

func TestState_ExcessiveAliasing(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		want bool
	}{
		"excessive":     {want: true},
		"not excessive": {want: false},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s := docstate.New(func() *paths.Resolver { return paths.NewResolver(nil) })

			calls := 0
			count := func() bool {
				calls++

				return tc.want
			}

			assert.Equal(t, tc.want, s.ExcessiveAliasing(count))
			assert.Equal(t, tc.want, s.ExcessiveAliasing(count))
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

			s := docstate.New(func() *paths.Resolver { return paths.NewResolver(nil) })

			calls := 0
			count := func() bool {
				calls++

				return tc.text
			}

			// The State keeps the text verdict apart from the value
			// verdict, so each count runs once.
			assert.Equal(t, tc.value, s.ExcessiveAliasing(func() bool { return tc.value }))
			assert.Equal(t, tc.text, s.ExcessiveTextAliasing(count))
			assert.Equal(t, tc.text, s.ExcessiveTextAliasing(count))
			assert.Equal(t, 1, calls)
			assert.Equal(t, tc.value, s.ExcessiveAliasing(func() bool { return !tc.value }))
		})
	}
}
