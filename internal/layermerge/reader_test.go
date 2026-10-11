package layermerge_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/internal/layermerge"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
)

func TestReader_Read(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		input string
		refs  []string
		want  string
		err   error
		// Whether the error carries a token.
		located bool
	}{
		"an alias to a reference document": {
			input: "a: *r\n",
			refs:  []string{"r: &r {x: 1}\n"},
			want:  stringtest.LinesLF("a:", "  x: 1"),
		},
		"an alias inside its anchor reads as null": {
			input: "a: &a [1, *a]\n",
			want:  stringtest.LinesLF("a:", "  - 1", "  - null"),
		},
		"a key with no name": {
			input:   stringtest.LinesLF("x: &x {a: 1}", "? *x", ": v"),
			err:     errUnnamed,
			located: true,
		},
		"an alias with no anchor": {
			input:   "a: *x\n",
			err:     paths.ErrAlias,
			located: true,
		},
		"an alias in a reference document with no anchor": {
			input: "a: *r\n",
			refs:  []string{"r: &r [*missing]\n"},
			err:   paths.ErrAlias,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			v, err := read(t, tc.input, 0, tc.refs...)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				nodeErr, ok := errors.AsType[*layermerge.NodeError](err)
				require.True(t, ok)
				assert.Equal(t, tc.located, nodeErr.Token != nil)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, layermerge.Text("", v))
		})
	}

	t.Run("an ended context stops the read", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		doc := yamltest.FirstDocument(t, "a: 1\n")
		r := layermerge.Reader{Ctx: ctx, Resolver: doc.Resolver()}

		_, err := r.Read(doc.AST())
		require.ErrorIs(t, err, context.Canceled)
	})
}
