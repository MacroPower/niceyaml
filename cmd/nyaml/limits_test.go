package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
)

func TestLimitFlags(t *testing.T) {
	t.Parallel()

	deep := "a: " + strings.Repeat("[", 5) + strings.Repeat("]", 5) + "\n"

	tcs := map[string]struct {
		err  error
		msg  string
		args []string
	}{
		"the default limits pass a shallow file": {},
		"a depth limit fails a deeper document": {
			args: []string{"--depth-limit", "3"},
			err:  niceyaml.ErrLimit,
		},
		"a byte limit fails a larger file": {
			args: []string{"--byte-limit", "4"},
			err:  niceyaml.ErrLimit,
			msg:  "deep.yaml: file is larger than 4 bytes",
		},
		"a limit of zero turns the check off": {
			args: []string{"--depth-limit", "0", "--byte-limit", "0"},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := writeFile(t, t.TempDir(), "deep.yaml", deep)
			out := &bytes.Buffer{}

			err := quietValidateCmd(out, append(tc.args, path)...).Execute()
			if tc.err == nil {
				require.NoError(t, err)
				assert.Contains(t, out.String(), "deep.yaml: valid")

				return
			}

			require.ErrorIs(t, err, tc.err)
			assert.Contains(t, err.Error(), tc.msg)
		})
	}
}
