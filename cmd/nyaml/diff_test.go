package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"
)

func TestDiffCmdPrint(t *testing.T) {
	t.Parallel()

	// Stdout is not a terminal, so diff prints the hunks of each pair of
	// adjacent files and exits rather than open the interactive viewer.
	tcs := map[string]struct {
		err  error
		want string
		args []string
		// Runs the command with a context that has ended.
		canceled bool
	}{
		"two files": {
			args: []string{"a.yaml", "b.yaml"},
			want: stringtest.LinesLF(
				"--- DIR/a.yaml",
				"+++ DIR/b.yaml",
				"      @@ -1,2 +1,3 @@",
				"   1  a: 1",
				"   2 -b: [1, 2]",
				"   2 +b: [1, 3]",
				"   3 +c: 3",
			),
		},
		"each adjacent pair": {
			args: []string{"a.yaml", "b.yaml", "empty.yaml"},
			want: stringtest.LinesLF(
				"--- DIR/a.yaml",
				"+++ DIR/b.yaml",
				"      @@ -1,2 +1,3 @@",
				"   1  a: 1",
				"   2 -b: [1, 2]",
				"   2 +b: [1, 3]",
				"   3 +c: 3",
				"",
				"--- DIR/b.yaml",
				"+++ DIR/empty.yaml",
				"      @@ -1,3 +0,0 @@",
				"   1 -a: 1",
				"   2 -b: [1, 3]",
				"   3 -c: 3",
			),
		},
		"files with the same lines": {
			args: []string{"a.yaml", "copy.yaml"},
			want: "",
		},
		"without line numbers": {
			args: []string{"--line-numbers=false", "a.yaml", "b.yaml"},
			want: stringtest.LinesLF(
				"--- DIR/a.yaml",
				"+++ DIR/b.yaml",
				" @@ -1,2 +1,3 @@",
				" a: 1",
				"-b: [1, 2]",
				"+b: [1, 3]",
				"+c: 3",
			),
		},
		"a glob that names two files": {
			args: []string{"[ab].yaml"},
			want: stringtest.LinesLF(
				"--- DIR/a.yaml",
				"+++ DIR/b.yaml",
				"      @@ -1,2 +1,3 @@",
				"   1  a: 1",
				"   2 -b: [1, 2]",
				"   2 +b: [1, 3]",
				"   3 +c: 3",
			),
		},
		"one file": {
			args: []string{"a.yaml"},
			err:  errTooFewFiles,
		},
		"canceled": {
			args:     []string{"a.yaml", "b.yaml"},
			canceled: true,
			err:      context.Canceled,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			if tc.canceled {
				var cancel context.CancelFunc

				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}

			got, want, err := runPrint(ctx, t, diffCmd(), tc.args, tc.want)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				assert.Empty(t, got)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, want, got)
		})
	}
}
