package manifest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/manifest"
)

func TestRegistry_Load_Encoding(t *testing.T) {
	t.Parallel()

	reg := newCafeRegistry(t)

	// NotUTF8 returns the error of a file in the encoding enc.
	notUTF8 := func(enc string) string {
		return stringtest.JoinLF("config/cafe.yaml: file is "+enc+", not UTF-8", "`-- save the file as UTF-8")
	}

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
			text: stringtest.LinesLF(
				"apiVersion: cafe.jacobcolvin.com/v1",
				"kind: Cafe",
				"metadata:",
				"  name: sm\xf6rg\xe5s",
			),
			err: stringtest.JoinLF(
				"config/cafe.yaml:4:11: byte 0xf6 is not UTF-8",
				"`-- save the file as UTF-8",
				"",
				"   2 | kind: Cafe",
				"   3 | metadata:",
				"   4 |   name: sm\ufffdrg\ufffds",
				"     |           ^",
			),
		},
		"utf-16 byte order mark": {
			text: "\xff\xfea\x00:\x00",
			err:  notUTF8("UTF-16"),
		},
		"utf-16 without a byte order mark": {
			text: "\x00a\x00:",
			err:  notUTF8("UTF-16"),
		},
		"utf-32 byte order mark": {
			text: "\xff\xfe\x00\x00a\x00\x00\x00",
			err:  notUTF8("UTF-32"),
		},
		"utf-32 without a byte order mark": {
			text: "a\x00\x00\x00",
			err:  notUTF8("UTF-32"),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := reg.Load(t.Context(), configFS(map[string]string{"cafe.yaml": tc.text}),
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
