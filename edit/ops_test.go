package edit_test

import (
	"context"
	"testing"

	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/edit"
	"go.jacobcolvin.com/niceyaml/paths"
)

func TestNode_SetKey(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		before string
		path   paths.Path
		key    string
		want   string
		err    string
	}{
		"block entry keeps its comment": {
			before: stringtest.LinesLF(
				"spec:",
				"  cost: 45 # kr",
				"  tags: vegan",
			),
			path: spec.Child("cost"),
			key:  "price",
			want: stringtest.LinesLF(
				"spec:",
				"  price: 45 # kr",
				"  tags: vegan",
			),
		},
		"quoted key keeps its quotes": {
			before: stringtest.LinesLF("spec:", "  'cost': 45"),
			path:   spec.Child("cost"),
			key:    "price",
			want:   stringtest.LinesLF("spec:", "  'price': 45"),
		},
		"key that reads as another value": {
			before: stringtest.LinesLF("spec:", "  cost: 45"),
			path:   spec.Child("cost"),
			key:    "null",
			want:   stringtest.LinesLF("spec:", `  "null": 45`),
		},
		"key that holds the text of a null value": {
			before: stringtest.LinesLF("spec:", "  cost: 45"),
			path:   spec.Child("cost"),
			key:    "price: null",
			want:   stringtest.LinesLF("spec:", `  "price: null": 45`),
		},
		"flow key with a flow indicator": {
			before: "spec: {a: 5}\n",
			path:   spec.Child("a"),
			key:    "a,b",
			want:   "spec: {\"a,b\": 5}\n",
		},
		"same key": {
			before: stringtest.LinesLF("spec:", "  cost: 45"),
			path:   spec.Child("cost"),
			key:    "cost",
			want:   stringtest.LinesLF("spec:", "  cost: 45"),
		},
		"missing place": {
			before: stringtest.LinesLF("spec:", "  cost: 45"),
			path:   spec.Child("price"),
			key:    "cost",
			want:   stringtest.LinesLF("spec:", "  cost: 45"),
		},
		"key that exists": {
			before: stringtest.LinesLF(
				"spec:",
				"  extras:",
				"    oat_milk: 4",
				"    oatMilk: 5",
			),
			path: spec.Child("extras", "oatMilk"),
			key:  "oat_milk",
			err: stringtest.JoinLF(
				`test.yaml:4:5: $.spec.extras.oatMilk~: cafe.jacobcolvin.com/v1 renames "oatMilk" to "oat_milk", `+
					"which already exists",
				`|-- 3:5: $.spec.extras.oat_milk~: "oat_milk" is defined here`,
				"`-- remove one of the two",
			),
		},
		"item": {
			before: stringtest.LinesLF("spec:", "  - a"),
			path:   spec.Index(0),
			key:    "b",
			err:    "cafe.jacobcolvin.com/v1 renames this item, but an item of a sequence has no key",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before)

			err := applyAt(t, d, tc.path, func(_ context.Context, n *edit.Node) error {
				return n.SetKey(tc.key)
			})

			requireEdit(t, d, err, tc.err, tc.want)
		})
	}
}

func TestNode_Remove(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		before string
		path   paths.Path
		want   string
	}{
		"entry with its comments": {
			before: stringtest.LinesLF(
				"spec:",
				"  # Matches the brand palette.",
				"  color: red # or a hex code",
				"  price: 45",
			),
			path: spec.Child("color"),
			want: stringtest.LinesLF("spec:", "  price: 45"),
		},
		"one of the blank lines around it": {
			before: stringtest.LinesLF(
				"a: 1",
				"",
				"b: 2",
				"",
				"c: 3",
			),
			path: paths.Doc().Child("b"),
			want: stringtest.LinesLF(
				"a: 1",
				"",
				"c: 3",
			),
		},
		"last entry of a block mapping": {
			before: stringtest.LinesLF("wifi: &w # router", "  security: wep"),
			path:   paths.Doc().Child("wifi", "security"),
			want:   "wifi: &w {} # router\n",
		},
		"last item of a block sequence": {
			before: stringtest.LinesLF(
				"tags:",
				"  - a",
				"price: 1",
			),
			path: paths.Doc().Child("tags").Index(0),
			want: stringtest.LinesLF("tags: []", "price: 1"),
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
			before: stringtest.LinesLF(
				"spec:",
				"  extras:",
				"    oat: 5",
				"    soy: 6",
				"  price: 1",
			),
			path: spec.Child("extras"),
			want: stringtest.LinesLF("spec:", "  price: 1"),
		},
		"missing place": {
			before: "a: 1\n",
			path:   paths.Doc().Child("b"),
			want:   "a: 1\n",
		},
		"anchored value keeps its alias": {
			before: stringtest.LinesLF(
				"spec:",
				"  tags: &diet vegan",
				"  labels:",
				"    diet: *diet",
			),
			path: spec.Child("tags"),
			want: stringtest.LinesLF(
				"spec:",
				"  labels:",
				"    diet: &diet vegan",
			),
		},
		"anchored block value keeps its alias": {
			before: stringtest.LinesLF(
				"spec:",
				"  oat: &milk",
				"    price: 5",
				"  soy: *milk",
			),
			path: spec.Child("oat"),
			want: stringtest.LinesLF(
				"spec:",
				"  soy: &milk",
				"    price: 5",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDoc(t, tc.before)

			err := applyAt(t, d, tc.path, func(_ context.Context, n *edit.Node) error {
				return n.Remove(edit.WithReason("gone"))
			})

			requireEdit(t, d, err, "", tc.want)
		})
	}
}
