package edit_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/paths"
)

func TestHandle_Set(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		value  any
		before string
		path   paths.Path
		// A second Set gives the key thenKey below path the value
		// thenValue, when thenKey is set.
		thenKey   string
		thenValue any
		want      string
		err       string
	}{
		"scalar keeps the comment": {
			before: stringtest.LinesLF("spec:", "  cost: 45 # kr"),
			path:   spec.Child("cost"),
			value:  50,
			want:   stringtest.LinesLF("spec:", "  cost: 50 # kr"),
		},
		"string keeps its quotes": {
			before: stringtest.LinesLF("spec:", `  open: "07:00"`),
			path:   spec.Child("open"),
			value:  "08:00",
			want:   stringtest.LinesLF("spec:", `  open: "08:00"`),
		},
		"string with a control rune past U+FFFF keeps its quotes": {
			before: stringtest.LinesLF("spec:", `  tag: "q"`),
			path:   spec.Child("tag"),
			value:  "a\U000E0001",
			want:   stringtest.LinesLF("spec:", `  tag: "a\U000e0001"`),
		},
		"number over a quoted string": {
			before: stringtest.LinesLF("spec:", `  size: "3"`),
			path:   spec.Child("size"),
			value:  2,
			want:   stringtest.LinesLF("spec:", "  size: 2"),
		},
		"list over a scalar": {
			before: stringtest.LinesLF(
				"spec:",
				"  tags: vegan",
				"  price: 5",
			),
			path:  spec.Child("tags"),
			value: []string{"vegan"},
			want: stringtest.LinesLF(
				"spec:",
				"  tags: [vegan]",
				"  price: 5",
			),
		},
		"list over a tagged scalar": {
			before: stringtest.LinesLF("spec:", "  tags: !!str vegan"),
			path:   spec.Child("tags"),
			value:  []string{"vegan"},
			want:   stringtest.LinesLF("spec:", "  tags: [vegan]"),
		},
		"block mapping over a block mapping": {
			before: stringtest.LinesLF(
				"spec:",
				"  wifi: # router",
				"    security: wep",
				"    # old",
				"  price: 5",
			),
			path:  spec.Child("wifi"),
			value: map[string]any{"mode": "wpa3", "ssid": "fika"},
			want: stringtest.LinesLF(
				"spec:",
				"  wifi: # router",
				"    mode: wpa3",
				"    ssid: fika",
				"  price: 5",
			),
		},
		"scalar over a block mapping": {
			before: stringtest.LinesLF(
				"spec:",
				"  wifi: # router",
				"    security: wep",
				"  price: 5",
			),
			path:  spec.Child("wifi"),
			value: "off",
			want: stringtest.LinesLF(
				"spec:",
				`  wifi: "off" # router`,
				"  price: 5",
			),
		},
		"scalar over a block mapping without a comment": {
			before: stringtest.LinesLF(
				"spec:",
				"  wifi:",
				"    security: wep",
				"    mode: open",
				"  price: 5",
			),
			path:  spec.Child("wifi"),
			value: "off",
			want: stringtest.LinesLF(
				"spec:",
				`  wifi: "off"`,
				"  price: 5",
			),
		},
		"scalar over a block sequence": {
			before: stringtest.LinesLF(
				"spec:",
				"  tags:",
				"  - vegan",
				"  - oat",
			),
			path:  spec.Child("tags"),
			value: "none",
			want:  stringtest.LinesLF("spec:", "  tags: none"),
		},
		"value of an entry with none": {
			before: stringtest.LinesLF(
				"spec:",
				"  wifi:",
				"  price: 5",
			),
			path:  spec.Child("wifi"),
			value: "open",
			want: stringtest.LinesLF(
				"spec:",
				"  wifi: open",
				"  price: 5",
			),
		},
		"new key at the end of a mapping": {
			before: stringtest.LinesLF(
				"spec:",
				"  price: 45 # kr",
				"# trailing",
			),
			path:  spec.Child("currency"),
			value: "SEK",
			want: stringtest.LinesLF(
				"spec:",
				"  price: 45 # kr",
				"  currency: SEK",
				"# trailing",
			),
		},
		"new mapping above a key": {
			before: stringtest.LinesLF("metadata:", "    name: yamull"),
			path:   spec.Child("currency"),
			value:  "SEK",
			want: stringtest.LinesLF(
				"metadata:",
				"    name: yamull",
				"spec:",
				"    currency: SEK",
			),
		},
		"new key in a flow mapping": {
			before: "spec: {price: 65}\n",
			path:   spec.Child("currency"),
			value:  "SEK",
			want:   "spec: {price: 65, currency: SEK}\n",
		},
		"new key below an empty mapping that the step wrote": {
			before: stringtest.LinesLF(
				"spec:",
				"  extras:",
				"    oat: ~",
			),
			path:      spec.Child("extras", "oat"),
			value:     map[string]any{},
			thenKey:   "price",
			thenValue: 0,
			want: stringtest.LinesLF(
				"spec:",
				"  extras:",
				"    oat:",
				"      price: 0",
			),
		},
		"item past the last": {
			before: stringtest.LinesLF(
				"spec:",
				"  tags:",
				"    - a",
			),
			path:  spec.Child("tags").Index(1),
			value: "b",
			want: stringtest.LinesLF(
				"spec:",
				"  tags:",
				"    - a",
				"    - b",
			),
		},
		"item past the last of a flow sequence": {
			before: stringtest.LinesLF("spec:", "  tags: [a]"),
			path:   spec.Child("tags").Index(1),
			value:  "b",
			want:   stringtest.LinesLF("spec:", "  tags: [a, b]"),
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
			before: stringtest.LinesLF(
				"spec:",
				"  oat: &milk",
				"    price: 5",
				"  soy: *milk",
			),
			path:  spec.Child("oat"),
			value: 6,
			want: stringtest.LinesLF(
				"spec:",
				"  oat: 6",
				"  soy: &milk",
				"    price: 5",
			),
		},
		"anchored value keeps its alias": {
			before: stringtest.LinesLF(
				"tags: &diet vegan",
				"labels:",
				"  diet: *diet",
			),
			path:  paths.Doc().Child("tags"),
			value: []string{"vegan"},
			want: stringtest.LinesLF(
				"tags: [vegan]",
				"labels:",
				"  diet: &diet vegan",
			),
		},
		"line endings of the file": {
			before: stringtest.LinesCRLF("spec:", "  price: 45"),
			path:   spec.Child("currency"),
			value:  "SEK",
			want: stringtest.LinesCRLF(
				"spec:",
				"  price: 45",
				"  currency: SEK",
			),
		},
		"file without a final newline": {
			before: stringtest.JoinLF("spec:", "  price: 45"),
			path:   spec.Child("currency"),
			value:  "SEK",
			want: stringtest.JoinLF(
				"spec:",
				"  price: 45",
				"  currency: SEK",
			),
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
			h := d.Handle(tc.path)

			err := h.Set(t.Context(), tc.value)
			if tc.thenKey != "" {
				require.NoError(t, err)

				err = h.At(paths.Current().Child(tc.thenKey)).Set(t.Context(), tc.thenValue)
			}

			requireEdit(t, d, err, tc.err, tc.want)
		})
	}
}
