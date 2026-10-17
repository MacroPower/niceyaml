package astnode_test

import (
	"bytes"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/internal/astnode"
)

func TestNodeToValue(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		node string
		want any
		err  bool
	}{
		"scalar":           {node: "!!int '7'", want: 7},
		"mapping":          {node: "{a: 1}", want: map[string]any{"a": uint64(1)}},
		"alias of nothing": {node: "*a", err: true},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			file, err := parser.ParseBytes([]byte(tc.node), 0)
			require.NoError(t, err)

			var got any

			err = astnode.NodeToValue(file.Docs[0].Body, &got)
			if tc.err {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestDecodeNode(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		// The nodes that one decoder decodes in order, the last of which
		// gives the value.
		nodes []string
		want  any
		err   bool
	}{
		"scalar":                 {nodes: []string{"1"}, want: uint64(1)},
		"mapping after a scalar": {nodes: []string{"1", "{a: 2}"}, want: map[string]any{"a": uint64(2)}},
		"alias of nothing":       {nodes: []string{"*a"}, err: true},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dec := yaml.NewDecoder(&bytes.Buffer{})

			var (
				got any
				err error
			)

			for _, text := range tc.nodes {
				file, perr := parser.ParseBytes([]byte(text), 0)
				require.NoError(t, perr)

				got = nil
				err = astnode.DecodeNode(dec, file.Docs[0].Body, &got)
			}

			if tc.err {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
