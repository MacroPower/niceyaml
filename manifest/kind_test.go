package manifest_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/manifest"
	"go.jacobcolvin.com/niceyaml/paths"
)

func TestNameField(t *testing.T) {
	t.Parallel()

	// A registry whose plain settings kind has no name, beside two kinds
	// that keep their names at different paths, and a kind without a name
	// that allows repeats.
	reg := manifest.MustNewRegistry(manifest.WithKinds(
		manifest.NewKind[CafeSpec]("Settings", manifest.Singleton(), manifest.Unversioned()),
		manifest.NewKind[Hours]("Hours", manifest.NameField(meta.Child("name")), manifest.Version("v1")),
		manifest.NewKind[stageSource]("Source", manifest.NameField(paths.Doc().Child("id")), manifest.Version("v1")),
		manifest.NewKind[MenuItem]("MenuItem", manifest.Version("v1")),
	))

	settings := "seats: 3"
	hours := func(name string) string {
		return stringtest.JoinLF("apiVersion: v1", "kind: Hours", "metadata:", "  name: "+name)
	}
	source := func(id string) string {
		return stringtest.JoinLF("apiVersion: v1", "kind: Source", "id: "+id)
	}
	item := stringtest.JoinLF("apiVersion: v1", "kind: MenuItem", "metadata:", "  name: semla")

	tcs := map[string]struct {
		err  error
		want string
		docs []string
	}{
		"each kind with its own rule": {
			docs: []string{settings, hours("weekday"), source("in"), item, item},
		},
		"duplicate name at the name path of the kind": {
			docs: []string{settings, hours("weekday"), hours("weekday")},
			err:  manifest.ErrDuplicateName,
			want: stringtest.JoinLF(
				`cafe.yaml:11:9: $.metadata.name: duplicate Hours "weekday"`,
				"`-- 6:9: $.metadata.name: first defined here",
			),
		},
		"duplicate name at another path": {
			docs: []string{source("in"), source("in")},
			err:  manifest.ErrDuplicateName,
			want: stringtest.JoinLF(
				`cafe.yaml:7:5: $.id: duplicate Source "in"`,
				"`-- 3:5: $.id: first defined here",
			),
		},
		"missing name of a named kind": {
			docs: []string{settings, stringtest.JoinLF("apiVersion: v1", "kind: Hours", "spec:", "  open: '07:00'")},
			err:  manifest.ErrMissingName,
			want: "cafe.yaml:3:1: $.metadata.name: name must be a non-empty string",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			text := ""
			for i, d := range tc.docs {
				if i > 0 {
					text += "\n---\n"
				}

				text += d
			}

			set, err := reg.Load(t.Context(),
				manifest.Sources(niceyaml.NewSourceFromString(text+"\n", niceyaml.WithName("cafe.yaml"))))
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				// Compare as many lines of the error as the case expects.
				lines := strings.Split(niceyaml.FormatError(err), "\n")
				n := min(len(lines), strings.Count(tc.want, "\n")+1)
				assert.Equal(t, tc.want, strings.Join(lines[:n], "\n"))

				return
			}

			require.NoError(t, err)

			cafe, ok := set.One[CafeSpec]()
			require.True(t, ok)
			assert.Empty(t, cafe.Name)
			assert.Equal(t, 3, cafe.Value.Seats)

			weekday, ok := set.Get[Hours]("weekday")
			require.True(t, ok)
			assert.Equal(t, "weekday", weekday.Name)

			in, ok := set.Get[stageSource]("in")
			require.True(t, ok)
			assert.Equal(t, "in", in.Value.ID)

			for o := range set.All[MenuItem]() {
				assert.Empty(t, o.Name)
			}

			assert.PanicsWithValue(t, "manifest: Get needs a kind with manifest.NameField, and Settings has none",
				func() { set.Get[CafeSpec]("x") })
		})
	}
}
