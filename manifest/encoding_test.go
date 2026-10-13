package manifest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/manifest"
)

func TestRegistry_Load_Encoding(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		text string
		err  string
	}{
		"utf-8": {
			text: cafeDoc,
		},
		"utf-8 with a byte order mark": {
			text: "\ufeff" + cafeDoc,
		},
		"latin-1 byte": {
			text: "apiVersion: cafe.jacobcolvin.com/v1\nkind: Cafe\nmetadata:\n  name: sm\xf6rg\xe5s\n",
			err: "" +
				"config/cafe.yaml:4:11: byte 0xf6 is not UTF-8\n" +
				"`-- save the file as UTF-8\n" +
				"\n" +
				"   2 | kind: Cafe\n" +
				"   3 | metadata:\n" +
				"   4 |   name: sm\ufffdrg\ufffds\n" +
				"     |           ^",
		},
		"utf-16 byte order mark": {
			text: "\xff\xfea\x00:\x00",
			err:  "config/cafe.yaml: file is UTF-16, not UTF-8\n`-- save the file as UTF-8",
		},
		"utf-16 without a byte order mark": {
			text: "\x00a\x00:",
			err:  "config/cafe.yaml: file is UTF-16, not UTF-8\n`-- save the file as UTF-8",
		},
		"utf-32 byte order mark": {
			text: "\xff\xfe\x00\x00a\x00\x00\x00",
			err:  "config/cafe.yaml: file is UTF-32, not UTF-8\n`-- save the file as UTF-8",
		},
		"utf-32 without a byte order mark": {
			text: "a\x00\x00\x00",
			err:  "config/cafe.yaml: file is UTF-32, not UTF-8\n`-- save the file as UTF-8",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reg := newCafeRegistry(t)

			_, err := reg.Load(t.Context(), manifest.FS(mapFS(map[string]string{"cafe.yaml": tc.text}), "config"),
				manifest.WithSetChecks(false))
			if tc.err == "" {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, manifest.ErrEncoding)
			assert.True(t, niceyaml.IsInvalid(err))
			assert.Equal(t, tc.err, niceyaml.FormatError(err))
		})
	}
}
