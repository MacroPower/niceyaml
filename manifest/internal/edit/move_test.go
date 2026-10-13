package edit_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/paths"
)

func TestHandle_Move(t *testing.T) {
	t.Parallel()

	meta := paths.Doc().Child("metadata")

	tcs := map[string]struct {
		before string
		moves  [][2]paths.Path
		want   string
		err    string
	}{
		"into new mappings": {
			before: stringtest.LinesLF(
				"# Café Yamüll, the main room.",
				"name: yamull",
				"seats: 40",
			),
			moves: [][2]paths.Path{
				{paths.Doc().Child("name"), meta.Child("name")},
				{paths.Doc().Child("seats"), spec.Child("seats")},
			},
			want: stringtest.LinesLF(
				"# Café Yamüll, the main room.",
				"metadata:",
				"  name: yamull",
				"spec:",
				"  seats: 40",
			),
		},
		"with comments and a block value": {
			before: stringtest.LinesLF(
				"listen:",
				"  host: x",
				"# The port.",
				"port: # p",
				"  number: 80",
			),
			moves: [][2]paths.Path{{paths.Doc().Child("port"), paths.Doc().Child("listen", "port")}},
			want: stringtest.LinesLF(
				"listen:",
				"  host: x",
				"  # The port.",
				"  port: # p",
				"    number: 80",
			),
		},
		"target that exists": {
			before: stringtest.LinesLF("a: 1", "b: 2"),
			moves:  [][2]paths.Path{{paths.Doc().Child("a"), paths.Doc().Child("b")}},
			err:    `cafe.jacobcolvin.com/v1 moves "a" to $.b, which already exists`,
		},
		"target inside the value": {
			before: stringtest.LinesLF("a:", "  b: 1"),
			moves:  [][2]paths.Path{{paths.Doc().Child("a"), paths.Doc().Child("a", "c")}},
			err:    `cafe.jacobcolvin.com/v1 moves "a" to $.a.c, which lies inside it`,
		},
		"past the last item of a block sequence": {
			before: stringtest.LinesLF(
				"spec:",
				"  # The house blend.",
				"  blend: kaffe # dark",
				"  items:",
				"    - te",
			),
			moves: [][2]paths.Path{{spec.Child("blend"), spec.Child("items").Index(1)}},
			want: stringtest.LinesLF(
				"spec:",
				"  items:",
				"    - te",
				"    # The house blend.",
				"    - kaffe # dark",
			),
		},
		"block mapping past the last item": {
			before: stringtest.LinesLF(
				"wifi:",
				"  ssid: fika",
				"  mode: wpa3",
				"networks:",
				"- ssid: guest",
			),
			moves: [][2]paths.Path{{paths.Doc().Child("wifi"), paths.Doc().Child("networks").Index(1)}},
			want: stringtest.LinesLF(
				"networks:",
				"- ssid: guest",
				"- ssid: fika",
				"  mode: wpa3",
			),
		},
		"past the last item of a flow sequence": {
			before: stringtest.LinesLF("blend: kaffe", "items: [te]"),
			moves:  [][2]paths.Path{{paths.Doc().Child("blend"), paths.Doc().Child("items").Index(1)}},
			want:   "items: [te, kaffe]\n",
		},
		"far past the last item": {
			before: stringtest.LinesLF("blend: kaffe", "items: [te]"),
			moves:  [][2]paths.Path{{paths.Doc().Child("blend"), paths.Doc().Child("items").Index(3)}},
			err:    `cafe.jacobcolvin.com/v1 moves "blend" to $.items[3], but items holds no such item`,
		},
		"item into a later item": {
			before: stringtest.LinesLF(
				"items:",
				"  - kaffe",
				"  - name: te",
				"  - name: saft",
			),
			moves: [][2]paths.Path{{
				paths.Doc().Child("items").Index(0),
				paths.Doc().Child("items").Index(1).Child("blend"),
			}},
			want: stringtest.LinesLF(
				"items:",
				"  - name: te",
				"    blend: kaffe",
				"  - name: saft",
			),
		},
		"anchor past its alias": {
			before: stringtest.LinesLF(
				"name: &n yamull",
				"spec:",
				"  title: *n",
			),
			moves: [][2]paths.Path{{paths.Doc().Child("name"), meta.Child("name")}},
			want: stringtest.LinesLF(
				"spec:",
				"  title: &n yamull",
				"metadata:",
				"  name: &n yamull",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before)

			var err error

			for _, m := range tc.moves {
				err = d.Handle(m[0]).Move(m[1])
				if err != nil {
					break
				}
			}

			requireEdit(t, d, err, tc.err, tc.want)
		})
	}
}

func TestHandle_Move_Items(t *testing.T) {
	t.Parallel()

	d := newDoc(t, stringtest.LinesLF("items:", "  - kaffe", "  - te", "  - saft"))

	handles, err := d.Matches(paths.Doc().Child("items").IndexAll())
	require.NoError(t, err)
	require.Len(t, handles, 3)

	// Each item moves after the item before it left the sequence.
	require.NoError(t, handles[0].Move(paths.Doc().Child("first")))
	require.NoError(t, handles[1].Move(paths.Doc().Child("second")))

	assert.Equal(t, paths.Doc().Child("first"), handles[0].Path())
	assert.Equal(t, paths.Doc().Child("second"), handles[1].Path())
	assert.Equal(t, paths.Doc().Child("items").Index(0), handles[2].Path())

	requireEdit(t, d, nil, "", stringtest.LinesLF(
		"items:",
		"  - saft",
		"first: kaffe",
		"second: te",
	))
}
