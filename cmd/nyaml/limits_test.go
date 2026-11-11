package main

import (
	"bytes"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
)

func TestLimitFlags(t *testing.T) {
	t.Parallel()

	// The shallow file nests 6 levels in 14 bytes. The deep file nests
	// past [niceyaml.DefaultDepthLimit], and the large file holds more
	// bytes than [niceyaml.DefaultByteLimit] in a comment.
	nested := func(n int) string {
		return "a: " + strings.Repeat("[", n) + strings.Repeat("]", n) + "\n"
	}
	shallow := nested(5)
	deep := nested(niceyaml.DefaultDepthLimit)
	large := "a: 1\n# " + strings.Repeat("x", niceyaml.DefaultByteLimit) + "\n"

	// A case with missing set passes a path that does not exist. Its
	// flag error shows that the command checks the flags before it reads
	// any file.
	tcs := map[string]struct {
		err     error
		text    string
		msg     string
		args    []string
		missing bool
	}{
		"the default limits pass a shallow file": {text: shallow},
		"a depth limit fails a deeper document": {
			text: shallow,
			args: []string{"--depth-limit", "3"},
			err:  niceyaml.ErrLimit,
			msg:  "document nests more than 3 levels",
		},
		"a byte limit fails a larger file": {
			text: shallow,
			args: []string{"--byte-limit", "4"},
			err:  niceyaml.ErrLimit,
			msg:  "test.yaml: file is larger than 4 bytes",
		},
		"the default depth limit fails a deep document": {
			text: deep,
			err:  niceyaml.ErrLimit,
			msg:  "document nests more than 100 levels",
		},
		"a depth limit of zero passes a deep document": {
			text: deep,
			args: []string{"--depth-limit", "0"},
		},
		"the default byte limit fails a large file": {
			text: large,
			err:  niceyaml.ErrLimit,
			msg:  "test.yaml: file is larger than 256 KiB",
		},
		"a byte limit of zero passes a large file": {
			text: large,
			args: []string{"--byte-limit", "0"},
		},
		"a negative byte limit fails the command before it reads a file": {
			args:    []string{"--byte-limit", "-1"},
			missing: true,
			err:     errNegativeLimit,
			msg:     "--byte-limit -1: limit is negative",
		},
		"a negative depth limit fails the command before it reads a file": {
			args:    []string{"--depth-limit", "-1"},
			missing: true,
			err:     errNegativeLimit,
			msg:     "--depth-limit -1: limit is negative",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "missing.yaml")
			if !tc.missing {
				path = yamltest.WriteFile(t, t.TempDir(), "test.yaml", tc.text)
			}

			out := &bytes.Buffer{}

			err := quietValidateCmd(out, io.Discard, append(tc.args, path)...).Execute()
			if tc.err == nil {
				require.NoError(t, err)
				assert.Contains(t, out.String(), "test.yaml: valid")

				return
			}

			require.ErrorIs(t, err, tc.err)

			if tc.missing {
				require.NotErrorIs(t, err, fs.ErrNotExist)
				assert.EqualError(t, err, tc.msg)

				return
			}

			assert.Contains(t, err.Error(), tc.msg)
		})
	}
}
