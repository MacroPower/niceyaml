package manifest_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/manifest"
	"go.jacobcolvin.com/niceyaml/manifest/migrate"
)

// planDir writes files below a new directory config and plans their
// migration with reg. It returns the plan and the path of config.
func planDir(t *testing.T, reg *manifest.Registry, files map[string]string) (*manifest.Plan, string) {
	t.Helper()

	dir := t.TempDir()

	tr := tree{files: make(map[string]string)}
	for name, text := range files {
		tr.files["config/"+name] = text
	}

	tr.write(t, dir)

	config := filepath.Join(dir, "config")

	plan, err := reg.PlanMigration(t.Context(), manifest.Path(config))
	require.NoError(t, err)

	return plan, config
}

// readFile returns the text of the file at path.
func readFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path) //nolint:gosec // The test reads its own files.
	require.NoError(t, err)

	return string(data)
}

func TestPlan_Apply(t *testing.T) {
	t.Parallel()

	reg := newCafeRegistry(t)

	plan, config := planDir(t, reg, map[string]string{
		"cafe.yaml":  cafeDoc,
		"hours.yaml": hoursDoc,
		"menu.yaml":  menuDoc,
	})
	require.NoError(t, plan.Err())

	res, err := plan.Apply(t.Context())
	require.NoError(t, err)
	require.NoError(t, res.Err())
	require.NoError(t, res.Warnings())
	assert.Len(t, res.Files(), 2)

	assert.Equal(t, ""+
		config+"/hours.yaml: migrated Hours \"weekday\" to cafe.jacobcolvin.com/v1\n"+
		config+"/menu.yaml: migrated MenuItem \"kanelbulle\" to cafe.jacobcolvin.com/v1\n"+
		"2 files migrated, 1 up to date", res.Summary())

	assert.Equal(t, string(plan.Files()[0].After()), readFile(t, filepath.Join(config, "hours.yaml")))
	assert.Equal(t, cafeDoc, readFile(t, filepath.Join(config, "cafe.yaml")))

	entries, err := os.ReadDir(config)
	require.NoError(t, err)
	assert.Len(t, entries, 3)

	again, err := reg.PlanMigration(t.Context(), manifest.Path(config))
	require.NoError(t, err)
	assert.Empty(t, again.Files())
	assert.Equal(t, "3 files up to date, nothing written", again.Summary())

	res, err = again.Apply(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "3 files up to date", res.Summary())
}

func TestPlan_Apply_Warnings(t *testing.T) {
	t.Parallel()

	remove := func(reason string) migrate.Func {
		return func(_ context.Context, n *migrate.Node) error {
			return n.Remove(reason)
		}
	}

	reg, err := manifest.NewRegistry(
		manifest.WithAppName("cafe"),
		manifest.WithKinds(manifest.NewKind[map[string]any]("Drink",
			manifest.Version("v1"),
			manifest.Version("v2", manifest.Written(), manifest.Migrate(spec.Child("note"), remove("notes moved"))),
			manifest.Version("v3", manifest.Migrate(spec.Child("color"), remove("colors moved"))),
		)),
	)
	require.NoError(t, err)

	plan, config := planDir(t, reg, map[string]string{
		"drink.yaml": "apiVersion: v1\nkind: Drink\nspec:\n  note: hot\n  color: red\n",
	})

	assert.Equal(t, "  removes $.spec.note: notes moved", lastLine(plan.Files()[0].Header()))
	assert.Equal(t, config+"/drink.yaml:5:3: warning: $.spec.color~: v3 ignores field \"color\"",
		firstLine(plan.Warnings().Error()))

	res, err := plan.Apply(t.Context())
	require.NoError(t, err)
	require.NoError(t, res.Err())
	assert.Equal(t, config+"/drink.yaml:4:3: warning: $.spec.color~: v3 ignores field \"color\"",
		firstLine(res.Warnings().Error()))
	assert.Equal(t, ""+
		config+"/drink.yaml: migrated Drink to v2\n"+
		"  removed $.spec.note\n"+
		"1 file migrated", res.Summary())
	assert.Equal(t, "apiVersion: v2\nkind: Drink\nspec:\n  color: red\n",
		readFile(t, filepath.Join(config, "drink.yaml")))
}

func TestPlan_Apply_ReadOnly(t *testing.T) {
	t.Parallel()

	reg := newCafeRegistry(t)

	fsys := mapFS(map[string]string{"hours.yaml": hoursDoc})

	fsPlan, err := reg.PlanMigration(t.Context(), manifest.FS(fsys, "config"))
	require.NoError(t, err)

	res, err := fsPlan.Apply(t.Context())
	require.NoError(t, err)
	assert.Empty(t, res.Files())
	require.ErrorIs(t, res.Err(), manifest.ErrReadOnly)
	assert.False(t, niceyaml.IsInvalid(res.Err()))
	assert.Equal(t, ""+
		"config/hours.yaml: cannot rewrite a file that cafe provides\n"+
		"`-- only a newer release of cafe updates this file", niceyaml.FormatError(res.Err()))
	assert.Equal(t, ""+
		"config/hours.yaml: not written, cannot rewrite a file that cafe provides\n"+
		"1 file not written", res.Summary())

	src := niceyaml.NewSourceFromString(hoursDoc, niceyaml.WithName("hours.yaml"))

	srcPlan, err := reg.PlanMigration(t.Context(), manifest.Sources(src))
	require.NoError(t, err)

	res, err = srcPlan.Apply(t.Context())
	require.NoError(t, err)
	require.ErrorIs(t, res.Err(), manifest.ErrReadOnly)
	assert.Equal(t, "hours.yaml: cannot rewrite a file that cafe built in memory", res.Err().Error())

	base := niceyaml.NewSourceFromString(hoursDoc, niceyaml.WithName("base.yaml"))
	top := niceyaml.NewSourceFromString("spec:\n  closes: \"20:00\"\n", niceyaml.WithName("top.yaml"))

	merged, err := niceyaml.NewSourceFromLayers(t.Context(), []*niceyaml.Source{base, top})
	require.NoError(t, err)

	layersPlan, err := reg.PlanMigration(t.Context(), manifest.Sources(merged))
	require.NoError(t, err)
	require.Len(t, layersPlan.Files(), 1)

	res, err = layersPlan.Apply(t.Context())
	require.NoError(t, err)
	require.ErrorIs(t, res.Err(), manifest.ErrReadOnly)
	assert.Equal(t, "base.yaml:1:13: $.apiVersion: cannot rewrite a document that merges layers",
		firstLine(res.Err().Error()))
}

func TestPlan_Apply_Changed(t *testing.T) {
	t.Parallel()

	reg := newCafeRegistry(t)

	tcs := map[string]struct {
		change func(t *testing.T, path string)
		err    string
	}{
		"changed": {
			change: func(t *testing.T, path string) {
				t.Helper()

				require.NoError(t, os.WriteFile(path, []byte(hoursDoc+"# edited\n"), 0o600))
			},
			err: "hours.yaml: changed after cafe read it\n`-- run the command again",
		},
		"removed": {
			change: func(t *testing.T, path string) {
				t.Helper()

				require.NoError(t, os.Remove(path))
			},
			err: "hours.yaml: removed after cafe read it\n`-- run the command again",
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			plan, config := planDir(t, reg, map[string]string{"hours.yaml": hoursDoc})
			path := filepath.Join(config, "hours.yaml")

			tc.change(t, path)

			res, err := plan.Apply(t.Context())
			require.NoError(t, err)
			assert.Empty(t, res.Files())
			require.ErrorIs(t, res.Err(), manifest.ErrChanged)
			assert.Equal(t, filepath.Join(config, tc.err), niceyaml.FormatError(res.Err()))
		})
	}
}

func TestPlan_Apply_Context(t *testing.T) {
	t.Parallel()

	plan, _ := planDir(t, newCafeRegistry(t), map[string]string{"hours.yaml": hoursDoc})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := plan.Apply(ctx)
	require.ErrorIs(t, err, context.Canceled)
}

// endingContext is a context whose Err reports [context.Canceled] after
// its first live calls.
type endingContext struct {
	context.Context

	live int
}

// Err returns nil for the first live calls, and [context.Canceled] after.
func (c *endingContext) Err() error {
	if c.live > 0 {
		c.live--

		return nil
	}

	return context.Canceled
}

func TestPlan_Apply_ContextEndsPartway(t *testing.T) {
	t.Parallel()

	plan, config := planDir(t, newCafeRegistry(t), map[string]string{"hours.yaml": hoursDoc, "menu.yaml": menuDoc})

	// The context ends after Apply writes the first file.
	res, err := plan.Apply(&endingContext{Context: t.Context(), live: 2})
	require.NoError(t, err)
	require.Len(t, res.Files(), 1)
	require.ErrorIs(t, res.Err(), context.Canceled)
	assert.Equal(t, config+"/menu.yaml: context canceled", res.Err().Error())
	assert.Equal(t, string(plan.Files()[0].After()), readFile(t, filepath.Join(config, "hours.yaml")))
}

func TestResult_Summary(t *testing.T) {
	t.Parallel()

	reg, err := manifest.NewRegistry(drinkOptions(true)...)
	require.NoError(t, err)

	plan, config := planDir(t, reg, map[string]string{
		"a.yaml": drinkDoc + "  size: XL\n",
		"b.yaml": drinkDoc + "  size: S\n---\napiVersion: cafe.jacobcolvin.com/v9\nkind: Drink\n",
	})

	res, err := plan.Apply(t.Context())
	require.NoError(t, err)
	assert.Equal(t, ""+
		config+"/a.yaml: not migrated, 1 error\n"+
		config+"/b.yaml: migrated document 1: Drink to cafe.jacobcolvin.com/v1\n"+
		config+"/b.yaml: 1 document not migrated\n"+
		"1 file migrated, 1 document not migrated, 1 not migrated", res.Summary())
}

// lastLine returns the last line of s.
func lastLine(s string) string {
	lines := strings.Split(s, "\n")

	return lines[len(lines)-1]
}
