package edit_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/manifest/internal/edit"
	"go.jacobcolvin.com/niceyaml/paths"
)

var (
	spec       = paths.Doc().Child("spec")
	typeFields = []paths.Path{paths.Doc().Child("apiVersion"), paths.Doc().Child("kind")}
)

// newDoc returns a Doc of the first document of text.
func newDoc(t *testing.T, text string) *edit.Doc {
	t.Helper()

	doc := yamltest.FirstDocument(t, text, niceyaml.WithName("test.yaml"))

	d, err := edit.New(doc, edit.Options{App: "cafe", TypeFields: typeFields})
	require.NoError(t, err)

	d.BeginStep("cafe.jacobcolvin.com/v1")

	return d
}

func TestHandle_SetKey(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		before string
		path   paths.Path
		key    string
		want   string
		err    string
	}{
		"block entry keeps its comment": {
			before: "spec:\n  cost: 45 # kr\n  tags: vegan\n",
			path:   spec.Child("cost"),
			key:    "price",
			want:   "spec:\n  price: 45 # kr\n  tags: vegan\n",
		},
		"quoted key keeps its quotes": {
			before: "spec:\n  'cost': 45\n",
			path:   spec.Child("cost"),
			key:    "price",
			want:   "spec:\n  'price': 45\n",
		},
		"key that reads as another value": {
			before: "spec:\n  cost: 45\n",
			path:   spec.Child("cost"),
			key:    "null",
			want:   "spec:\n  \"null\": 45\n",
		},
		"key that holds the text of a null value": {
			before: "spec:\n  cost: 45\n",
			path:   spec.Child("cost"),
			key:    "price: null",
			want:   "spec:\n  \"price: null\": 45\n",
		},
		"flow key with a flow indicator": {
			before: "spec: {a: 5}\n",
			path:   spec.Child("a"),
			key:    "a,b",
			want:   "spec: {\"a,b\": 5}\n",
		},
		"same key": {
			before: "spec:\n  cost: 45\n",
			path:   spec.Child("cost"),
			key:    "cost",
			want:   "spec:\n  cost: 45\n",
		},
		"missing place": {
			before: "spec:\n  cost: 45\n",
			path:   spec.Child("price"),
			key:    "cost",
			want:   "spec:\n  cost: 45\n",
		},
		"key that exists": {
			before: "spec:\n  extras:\n    oat_milk: 4\n    oatMilk: 5\n",
			path:   spec.Child("extras", "oatMilk"),
			key:    "oat_milk",
			err: "" +
				"test.yaml:4:5: $.spec.extras.oatMilk~: cafe.jacobcolvin.com/v1 renames \"oatMilk\" to \"oat_milk\", " +
				"which already exists\n" +
				"|-- 3:5: $.spec.extras.oat_milk~: \"oat_milk\" is defined here\n" +
				"`-- remove one of the two",
		},
		"item": {
			before: "spec:\n  - a\n",
			path:   spec.Index(0),
			key:    "b",
			err:    "cafe.jacobcolvin.com/v1 renames this item, but an item of a sequence has no key",
		},
		"type field": {
			before: "kind: Cafe\n",
			path:   paths.Doc().Child("kind"),
			key:    "type",
			err:    "cafe.jacobcolvin.com/v1 renames \"kind\", which holds a type field",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before)

			err := d.Handle(tc.path).SetKey(tc.key)
			if tc.err != "" {
				require.Error(t, err)
				assert.Contains(t, niceyaml.FormatError(err), tc.err)

				return
			}

			require.NoError(t, err)
			require.NoError(t, d.Check(t.Context()))
			assert.Equal(t, tc.want, d.Text())
		})
	}
}

func TestHandle_Set(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		value  any
		before string
		path   paths.Path
		want   string
		err    string
	}{
		"scalar keeps the comment": {
			before: "spec:\n  cost: 45 # kr\n",
			path:   spec.Child("cost"),
			value:  50,
			want:   "spec:\n  cost: 50 # kr\n",
		},
		"string keeps its quotes": {
			before: "spec:\n  open: \"07:00\"\n",
			path:   spec.Child("open"),
			value:  "08:00",
			want:   "spec:\n  open: \"08:00\"\n",
		},
		"string with a control rune past U+FFFF keeps its quotes": {
			before: "spec:\n  tag: \"q\"\n",
			path:   spec.Child("tag"),
			value:  "a\U000E0001",
			want:   "spec:\n  tag: \"a\\U000e0001\"\n",
		},
		"number over a quoted string": {
			before: "spec:\n  size: \"3\"\n",
			path:   spec.Child("size"),
			value:  2,
			want:   "spec:\n  size: 2\n",
		},
		"list over a scalar": {
			before: "spec:\n  tags: vegan\n  price: 5\n",
			path:   spec.Child("tags"),
			value:  []string{"vegan"},
			want:   "spec:\n  tags: [vegan]\n  price: 5\n",
		},
		"list over a tagged scalar": {
			before: "spec:\n  tags: !!str vegan\n",
			path:   spec.Child("tags"),
			value:  []string{"vegan"},
			want:   "spec:\n  tags: [vegan]\n",
		},
		"block mapping over a block mapping": {
			before: "spec:\n  wifi: # router\n    security: wep\n    # old\n  price: 5\n",
			path:   spec.Child("wifi"),
			value:  map[string]any{"mode": "wpa3", "ssid": "fika"},
			want:   "spec:\n  wifi: # router\n    mode: wpa3\n    ssid: fika\n  price: 5\n",
		},
		"scalar over a block mapping": {
			before: "spec:\n  wifi: # router\n    security: wep\n  price: 5\n",
			path:   spec.Child("wifi"),
			value:  "off",
			want:   "spec:\n  wifi: \"off\" # router\n  price: 5\n",
		},
		"scalar over a block mapping without a comment": {
			before: "spec:\n  wifi:\n    security: wep\n    mode: open\n  price: 5\n",
			path:   spec.Child("wifi"),
			value:  "off",
			want:   "spec:\n  wifi: \"off\"\n  price: 5\n",
		},
		"scalar over a block sequence": {
			before: "spec:\n  tags:\n  - vegan\n  - oat\n",
			path:   spec.Child("tags"),
			value:  "none",
			want:   "spec:\n  tags: none\n",
		},
		"value of an entry with none": {
			before: "spec:\n  wifi:\n  price: 5\n",
			path:   spec.Child("wifi"),
			value:  "open",
			want:   "spec:\n  wifi: open\n  price: 5\n",
		},
		"new key at the end of a mapping": {
			before: "spec:\n  price: 45 # kr\n# trailing\n",
			path:   spec.Child("currency"),
			value:  "SEK",
			want:   "spec:\n  price: 45 # kr\n  currency: SEK\n# trailing\n",
		},
		"new mapping above a key": {
			before: "metadata:\n    name: yamull\n",
			path:   spec.Child("currency"),
			value:  "SEK",
			want:   "metadata:\n    name: yamull\nspec:\n    currency: SEK\n",
		},
		"new key in a flow mapping": {
			before: "spec: {price: 65}\n",
			path:   spec.Child("currency"),
			value:  "SEK",
			want:   "spec: {price: 65, currency: SEK}\n",
		},
		"new key below an empty mapping that the step wrote": {
			before: "spec:\n  extras:\n    oat: ~\n",
			path:   spec.Child("extras", "oat"),
			value:  map[string]any{},
			want:   "spec:\n  extras:\n    oat:\n      price: 0\n",
		},
		"item past the last": {
			before: "spec:\n  tags:\n    - a\n",
			path:   spec.Child("tags").Index(1),
			value:  "b",
			want:   "spec:\n  tags:\n    - a\n    - b\n",
		},
		"item past the last of a flow sequence": {
			before: "spec:\n  tags: [a]\n",
			path:   spec.Child("tags").Index(1),
			value:  "b",
			want:   "spec:\n  tags: [a, b]\n",
		},
		"below a scalar": {
			before: "spec: 5\n",
			path:   spec.Child("currency"),
			value:  "SEK",
			err:    "cafe.jacobcolvin.com/v1 sets $.spec.currency, but spec holds a scalar",
		},
		"below a null": {
			before: "spec:\n",
			path:   spec.Child("currency"),
			value:  "SEK",
			err:    "cafe.jacobcolvin.com/v1 sets $.spec.currency, but spec holds null",
		},
		"item far past the last": {
			before: "tags: [a]\n",
			path:   paths.Doc().Child("tags").Index(3),
			value:  "b",
			err:    "cafe.jacobcolvin.com/v1 sets $.tags[3], but tags holds 1 items",
		},
		"anchored block mapping on one line keeps its alias": {
			before: "spec:\n  oat: &milk\n    price: 5\n  soy: *milk\n",
			path:   spec.Child("oat"),
			value:  6,
			want:   "spec:\n  oat: 6\n  soy: &milk\n    price: 5\n",
		},
		"anchored value keeps its alias": {
			before: "tags: &diet vegan\nlabels:\n  diet: *diet\n",
			path:   paths.Doc().Child("tags"),
			value:  []string{"vegan"},
			want:   "tags: [vegan]\nlabels:\n  diet: &diet vegan\n",
		},
		"line endings of the file": {
			before: "spec:\r\n  price: 45\r\n",
			path:   spec.Child("currency"),
			value:  "SEK",
			want:   "spec:\r\n  price: 45\r\n  currency: SEK\r\n",
		},
		"file without a final newline": {
			before: "spec:\n  price: 45",
			path:   spec.Child("currency"),
			value:  "SEK",
			want:   "spec:\n  price: 45\n  currency: SEK",
		},
		"type field": {
			before: "kind: Cafe\n",
			path:   paths.Doc().Child("kind"),
			value:  "Bistro",
			err:    "cafe.jacobcolvin.com/v1 sets $.kind, which holds a type field",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before)
			ctx := t.Context()

			h := d.Handle(tc.path)

			if name == "new key below an empty mapping that the step wrote" {
				require.NoError(t, h.Set(ctx, tc.value))

				h = h.At(paths.Current().Child("price"))
				tc.value = 0
			}

			err := h.Set(ctx, tc.value)
			if tc.err != "" {
				require.Error(t, err)
				assert.Contains(t, niceyaml.FormatError(err), tc.err)

				return
			}

			require.NoError(t, err)
			require.NoError(t, d.Check(ctx))
			assert.Equal(t, tc.want, d.Text())
		})
	}
}

func TestHandle_Remove(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		before string
		path   paths.Path
		want   string
	}{
		"entry with its comments": {
			before: "spec:\n  # Matches the brand palette.\n  color: red # or a hex code\n  price: 45\n",
			path:   spec.Child("color"),
			want:   "spec:\n  price: 45\n",
		},
		"one of the blank lines around it": {
			before: "a: 1\n\nb: 2\n\nc: 3\n",
			path:   paths.Doc().Child("b"),
			want:   "a: 1\n\nc: 3\n",
		},
		"last entry of a block mapping": {
			before: "wifi: &w # router\n  security: wep\n",
			path:   paths.Doc().Child("wifi", "security"),
			want:   "wifi: &w {} # router\n",
		},
		"last item of a block sequence": {
			before: "tags:\n  - a\nprice: 1\n",
			path:   paths.Doc().Child("tags").Index(0),
			want:   "tags: []\nprice: 1\n",
		},
		"entry of a flow mapping": {
			before: "spec: {a: 1, b: 2, c: 3}\n",
			path:   spec.Child("b"),
			want:   "spec: {a: 1, c: 3}\n",
		},
		"last entry of a flow mapping": {
			before: "spec: {a: 1, b: 2}\n",
			path:   spec.Child("b"),
			want:   "spec: {a: 1}\n",
		},
		"block value": {
			before: "spec:\n  extras:\n    oat: 5\n    soy: 6\n  price: 1\n",
			path:   spec.Child("extras"),
			want:   "spec:\n  price: 1\n",
		},
		"missing place": {
			before: "a: 1\n",
			path:   paths.Doc().Child("b"),
			want:   "a: 1\n",
		},
		"anchored value keeps its alias": {
			before: "spec:\n  tags: &diet vegan\n  labels:\n    diet: *diet\n",
			path:   spec.Child("tags"),
			want:   "spec:\n  labels:\n    diet: &diet vegan\n",
		},
		"anchored block value keeps its alias": {
			before: "spec:\n  oat: &milk\n    price: 5\n  soy: *milk\n",
			path:   spec.Child("oat"),
			want:   "spec:\n  soy: &milk\n    price: 5\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before)

			require.NoError(t, d.Handle(tc.path).Remove("gone"))
			require.NoError(t, d.Check(t.Context()))
			assert.Equal(t, tc.want, d.Text())
		})
	}
}

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
			before: "# Café Yamüll, the main room.\nname: yamull\nseats: 40\n",
			moves: [][2]paths.Path{
				{paths.Doc().Child("name"), meta.Child("name")},
				{paths.Doc().Child("seats"), spec.Child("seats")},
			},
			want: "# Café Yamüll, the main room.\nmetadata:\n  name: yamull\nspec:\n  seats: 40\n",
		},
		"with comments and a block value": {
			before: "listen:\n  host: x\n# The port.\nport: # p\n  number: 80\n",
			moves:  [][2]paths.Path{{paths.Doc().Child("port"), paths.Doc().Child("listen", "port")}},
			want:   "listen:\n  host: x\n  # The port.\n  port: # p\n    number: 80\n",
		},
		"target that exists": {
			before: "a: 1\nb: 2\n",
			moves:  [][2]paths.Path{{paths.Doc().Child("a"), paths.Doc().Child("b")}},
			err:    `cafe.jacobcolvin.com/v1 moves "a" to $.b, which already exists`,
		},
		"target inside the value": {
			before: "a:\n  b: 1\n",
			moves:  [][2]paths.Path{{paths.Doc().Child("a"), paths.Doc().Child("a", "c")}},
			err:    `cafe.jacobcolvin.com/v1 moves "a" to $.a.c, which lies inside it`,
		},
		"anchor past its alias": {
			before: "name: &n yamull\nspec:\n  title: *n\n",
			moves:  [][2]paths.Path{{paths.Doc().Child("name"), meta.Child("name")}},
			want:   "spec:\n  title: &n yamull\nmetadata:\n  name: &n yamull\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before)

			for _, m := range tc.moves {
				err := d.Handle(m[0]).Move(m[1])
				if tc.err != "" {
					require.Error(t, err)
					assert.Contains(t, niceyaml.FormatError(err), tc.err)

					return
				}

				require.NoError(t, err)
			}

			require.NoError(t, d.Check(t.Context()))
			assert.Equal(t, tc.want, d.Text())
		})
	}
}

func TestDoc_SetTypeField(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		before string
		want   string
	}{
		"fields that exist": {
			before: "apiVersion: cafe.jacobcolvin.com/v1beta1\nkind: Dish\nspec: {}\n",
			want:   "apiVersion: cafe.jacobcolvin.com/v1\nkind: MenuItem\nspec: {}\n",
		},
		"fields that the document lacks": {
			before: "# Café Yamüll, the main room.\nmetadata:\n  name: yamull\n",
			want: "# Café Yamüll, the main room.\napiVersion: cafe.jacobcolvin.com/v1\nkind: MenuItem\n" +
				"metadata:\n  name: yamull\n",
		},
		"quoted version": {
			before: "apiVersion: \"cafe.jacobcolvin.com/v1beta1\"\nkind: MenuItem\n",
			want:   "apiVersion: \"cafe.jacobcolvin.com/v1\"\nkind: MenuItem\n",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before)
			ctx := t.Context()

			require.NoError(t, d.SetTypeField(ctx, paths.Doc().Child("kind"), "MenuItem"))
			require.NoError(t, d.SetTypeField(ctx, paths.Doc().Child("apiVersion"), "cafe.jacobcolvin.com/v1"))
			assert.Equal(t, tc.want, d.Text())
		})
	}
}

func TestDoc_Locate(t *testing.T) {
	t.Parallel()

	d := newDoc(t, "spec:\n  cost: 45 # kr\n")
	require.NoError(t, d.Handle(spec.Child("cost")).SetKey("price"))

	err := d.Node().Bind(niceyaml.NewError("too cheap", niceyaml.AtPath(spec.Child("price"))))
	assert.Equal(t, "test.yaml:2:9: $.spec.cost: too cheap", err.Error())

	err = d.Node().Bind(niceyaml.NewError("deprecated", niceyaml.AtPath(spec.Child("price").Key())))
	assert.Equal(t, "test.yaml:2:3: $.spec.cost~: deprecated", err.Error())

	err = d.Node().Bind(niceyaml.NewError("missing", niceyaml.AtPath(spec.Child("close"))))
	assert.Equal(t, "test.yaml:1:1: $.spec.close: missing", err.Error())
}
