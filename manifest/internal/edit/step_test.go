package edit_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml/paths"
)

func TestDoc_SetTypeField(t *testing.T) {
	t.Parallel()

	tcs := map[string]struct {
		before string
		want   string
	}{
		"fields that exist": {
			before: stringtest.LinesLF(
				"apiVersion: cafe.jacobcolvin.com/v1beta1",
				"kind: Dish",
				"spec: {}",
			),
			want: stringtest.LinesLF(
				"apiVersion: cafe.jacobcolvin.com/v1",
				"kind: MenuItem",
				"spec: {}",
			),
		},
		"fields that the document lacks": {
			before: stringtest.LinesLF(
				"# Café Yamüll, the main room.",
				"metadata:",
				"  name: yamull",
			),
			want: stringtest.LinesLF(
				"# Café Yamüll, the main room.",
				"apiVersion: cafe.jacobcolvin.com/v1",
				"kind: MenuItem",
				"metadata:",
				"  name: yamull",
			),
		},
		"quoted version": {
			before: stringtest.LinesLF(`apiVersion: "cafe.jacobcolvin.com/v1beta1"`, "kind: MenuItem"),
			want:   stringtest.LinesLF(`apiVersion: "cafe.jacobcolvin.com/v1"`, "kind: MenuItem"),
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
