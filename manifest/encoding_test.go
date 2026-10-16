package manifest_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
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

func TestRegistry_Load_EncodingOfEachInput(t *testing.T) {
	t.Parallel()

	reg := newCafeRegistry(t)

	latin1 := stringtest.LinesLF(
		"apiVersion: cafe.jacobcolvin.com/v1",
		"kind: Cafe",
		"metadata:",
		"  name: sm\xf6rg\xe5s",
	)

	tcs := map[string]struct {
		in   func(t *testing.T) manifest.Input
		is   error
		want string
	}{
		"file on disk": {
			in: func(t *testing.T) manifest.Input {
				t.Helper()

				return manifest.Path(yamltest.WriteFile(t, t.TempDir(), "cafe.yaml", latin1))
			},
			is:   manifest.ErrEncoding,
			want: stringtest.JoinLF("cafe.yaml:4:11: byte 0xf6 is not UTF-8", "`-- save the file as UTF-8"),
		},
		"file of a file system": {
			in: func(*testing.T) manifest.Input {
				return configFS(map[string]string{"cafe.yaml": latin1})
			},
			is:   manifest.ErrEncoding,
			want: stringtest.JoinLF("config/cafe.yaml:4:11: byte 0xf6 is not UTF-8", "`-- save the file as UTF-8"),
		},
		"reader": {
			in: func(*testing.T) manifest.Input {
				return manifest.Reader("cafe.yaml", strings.NewReader(latin1))
			},
			is:   manifest.ErrEncoding,
			want: stringtest.JoinLF("cafe.yaml:4:11: byte 0xf6 is not UTF-8", "`-- encode the text as UTF-8"),
		},
		"reader with a file path": {
			in: func(*testing.T) manifest.Input {
				return manifest.Reader("cafe.yaml", strings.NewReader(latin1),
					manifest.WithSourceOptions(niceyaml.WithFilePath("cafe.yaml")))
			},
			is:   manifest.ErrEncoding,
			want: stringtest.JoinLF("cafe.yaml:4:11: byte 0xf6 is not UTF-8", "`-- save the file as UTF-8"),
		},
		"source": {
			in: func(*testing.T) manifest.Input {
				return manifest.Sources(niceyaml.NewSourceFromString(latin1, niceyaml.WithName("cafe.yaml")))
			},
			is:   manifest.ErrEncoding,
			want: stringtest.JoinLF("cafe.yaml:4:11: byte 0xf6 is not UTF-8", "`-- encode the text as UTF-8"),
		},
		"source of a file": {
			in: func(t *testing.T) manifest.Input {
				t.Helper()

				src, err := niceyaml.NewSourceFromFile(yamltest.WriteFile(t, t.TempDir(), "cafe.yaml", latin1))
				require.NoError(t, err)

				return manifest.Sources(src)
			},
			is:   manifest.ErrEncoding,
			want: stringtest.JoinLF("cafe.yaml:4:11: byte 0xf6 is not UTF-8", "`-- save the file as UTF-8"),
		},
		"utf-16 source": {
			in: func(*testing.T) manifest.Input {
				return manifest.Sources(niceyaml.NewSourceFromString("\xff\xfea\x00:\x00",
					niceyaml.WithName("cafe.yaml")))
			},
			is:   manifest.ErrEncoding,
			want: stringtest.JoinLF("cafe.yaml: text is UTF-16, not UTF-8", "`-- encode the text as UTF-8"),
		},
		"source past its byte limit": {
			in: func(*testing.T) manifest.Input {
				return manifest.Sources(niceyaml.NewSourceFromString(latin1,
					niceyaml.WithName("big.yaml"), niceyaml.WithByteLimit(10)))
			},
			is:   niceyaml.ErrLimit,
			want: "big.yaml: file is larger than 10 bytes",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := reg.Load(t.Context(), tc.in(t), manifest.WithSetChecks(false))
			require.ErrorIs(t, err, tc.is)
			assert.True(t, niceyaml.IsInvalid(err))
			assert.Contains(t, filepath.ToSlash(niceyaml.FormatError(err)), tc.want)
		})
	}
}
