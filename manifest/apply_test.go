package manifest_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/edit"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/manifest"
)

// planDir writes files below a new directory config and plans their
// migration with reg. It returns the plan and the path of config.
func planDir(t *testing.T, reg *manifest.Registry, files map[string]string) (*manifest.Plan, string) {
	t.Helper()

	config := filepath.Join(t.TempDir(), "config")
	yamltest.WriteFiles(t, config, files)

	plan, err := reg.PlanMigration(t.Context(), manifest.Path(config))
	require.NoError(t, err)
	requireReloads(t, reg, plan, nil)

	return plan, config
}

func TestPlan_Apply(t *testing.T) {
	t.Parallel()

	reg := newCafeRegistry(t)

	plan, config := planDir(t, reg, exampleFiles)
	require.NoError(t, plan.Err())

	res, err := plan.Apply(t.Context())
	require.NoError(t, err)
	require.NoError(t, res.Err())
	require.NoError(t, res.Warnings())
	assert.Len(t, res.Files(), 2)

	assert.Equal(t, stringtest.JoinLF(
		config+`/hours.yaml: migrated Hours "weekday" to cafe.jacobcolvin.com/v1`,
		config+`/menu.yaml: migrated MenuItem "kanelbulle" to cafe.jacobcolvin.com/v1`,
		"2 files migrated, 1 up to date",
	), res.Summary())

	assert.Equal(t, string(plan.Files()[0].After()), yamltest.ReadFile(t, filepath.Join(config, "hours.yaml")))
	assert.Equal(t, cafeDoc, yamltest.ReadFile(t, filepath.Join(config, "cafe.yaml")))

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

	remove := func(reason string) edit.Func {
		return func(_ context.Context, n *edit.Node) error {
			return n.Remove(edit.WithReason(reason))
		}
	}

	reg := manifest.MustNewRegistry(
		manifest.WithAppName("cafe"),
		manifest.WithKinds(manifest.NewKind[map[string]any]("Drink",
			manifest.Version("v1"),
			manifest.Version("v2", manifest.Written(), manifest.Migrate(spec.Child("note"), remove("notes moved"))),
			manifest.Version("v3", manifest.Migrate(spec.Child("color"), remove("colors moved"))),
		)),
	)

	plan, config := planDir(t, reg, map[string]string{
		"drink.yaml": stringtest.LinesLF(
			"apiVersion: v1",
			"kind: Drink",
			"spec:",
			"  note: hot",
			"  color: red",
		),
	})

	assert.Equal(t, "  removes $.spec.note: notes moved", lastLine(plan.Files()[0].Header()))
	assert.Equal(t, config+`/drink.yaml:5:3: warning: $.spec.color~: v3 ignores field "color"`,
		firstLine(plan.Warnings().Error()))

	res, err := plan.Apply(t.Context())
	require.NoError(t, err)
	require.NoError(t, res.Err())
	assert.Equal(t, config+`/drink.yaml:4:3: warning: $.spec.color~: v3 ignores field "color"`,
		firstLine(res.Warnings().Error()))
	assert.Equal(t, stringtest.JoinLF(
		config+"/drink.yaml: migrated Drink to v2",
		"  removed $.spec.note",
		"1 file migrated",
	), res.Summary())
	assert.Equal(t, stringtest.LinesLF(
		"apiVersion: v2",
		"kind: Drink",
		"spec:",
		"  color: red",
	),
		yamltest.ReadFile(t, filepath.Join(config, "drink.yaml")))
}

func TestPlan_Apply_ReadOnly(t *testing.T) {
	t.Parallel()

	reg := newCafeRegistry(t)

	base := niceyaml.NewSourceFromString(hoursDoc, niceyaml.WithName("base.yaml"))
	top := niceyaml.NewSourceFromString(stringtest.LinesLF("spec:", `  closes: "20:00"`), niceyaml.WithName("top.yaml"))

	tcs := map[string]struct {
		in      manifest.Input
		err     string
		summary string
	}{
		"file of a file system": {
			in:  configFS(map[string]string{"hours.yaml": hoursDoc}),
			err: "config/hours.yaml: cannot rewrite a file of a file system that cafe only reads",
			summary: stringtest.JoinLF(
				"config/hours.yaml: not written, cannot rewrite a file of a file system that cafe only reads",
				"1 file not written",
			),
		},
		"reader": {
			in:  manifest.Reader("hours.yaml", strings.NewReader(hoursDoc)),
			err: "hours.yaml: cannot rewrite a file that cafe read from a stream",
		},
		"source": {
			in:  manifest.Sources(niceyaml.NewSourceFromString(hoursDoc, niceyaml.WithName("hours.yaml"))),
			err: "hours.yaml: cannot rewrite a file that cafe built in memory",
		},
		"merged layers": {
			in: manifest.Sources(yamltest.Layers(t, base, top)),
			err: stringtest.JoinLF(
				"base.yaml:1:13: $.apiVersion: cannot rewrite a document that merges layers",
				"",
				"   1 | apiVersion: cafe.jacobcolvin.com/v1alpha1",
				"     |             ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^",
				"   2 | kind: Hours",
				"   3 | metadata:",
			),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			plan, err := reg.PlanMigration(t.Context(), tc.in)
			require.NoError(t, err)
			require.Len(t, plan.Files(), 1)

			res, err := plan.Apply(t.Context())
			require.ErrorIs(t, err, manifest.ErrReadOnly)
			assert.Equal(t, res.Err(), err)
			assert.Empty(t, res.Files())
			assert.False(t, niceyaml.IsInvalid(err))
			assert.Equal(t, tc.err, niceyaml.FormatError(err))

			if tc.summary != "" {
				assert.Equal(t, tc.summary, res.Summary())
			}
		})
	}
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
			err: stringtest.JoinLF("hours.yaml: changed after cafe read it", "`-- run the command again"),
		},
		"removed": {
			change: func(t *testing.T, path string) {
				t.Helper()

				require.NoError(t, os.Remove(path))
			},
			err: stringtest.JoinLF("hours.yaml: removed after cafe read it", "`-- run the command again"),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			plan, config := planDir(t, reg, map[string]string{"hours.yaml": hoursDoc})
			path := filepath.Join(config, "hours.yaml")

			tc.change(t, path)

			res, err := plan.Apply(t.Context())
			require.ErrorIs(t, err, manifest.ErrChanged)
			assert.Equal(t, res.Err(), err)
			assert.Empty(t, res.Files())
			assert.Equal(t, filepath.Join(config, tc.err), niceyaml.FormatError(err))
		})
	}
}

func TestPlan_Apply_Context(t *testing.T) {
	t.Parallel()

	plan, config := planDir(t, newCafeRegistry(t), map[string]string{"hours.yaml": hoursDoc})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	res, err := plan.Apply(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.NotNil(t, res)
	assert.Empty(t, res.Files())
	assert.Equal(t, stringtest.JoinLF(
		config+"/hours.yaml: not written, context canceled",
		"1 file not written",
	), res.Summary())
	assert.Equal(t, hoursDoc, yamltest.ReadFile(t, filepath.Join(config, "hours.yaml")))
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
	res, err := plan.Apply(&endingContext{Context: t.Context(), live: 1})
	require.ErrorIs(t, err, context.Canceled)
	require.Len(t, res.Files(), 1)
	assert.Equal(t, config+"/menu.yaml: context canceled", err.Error())
	assert.Equal(t, string(plan.Files()[0].After()), yamltest.ReadFile(t, filepath.Join(config, "hours.yaml")))
}

func TestResult_Summary(t *testing.T) {
	t.Parallel()

	reg := manifest.MustNewRegistry(drinkOptions(false)...)

	tcs := map[string]struct {
		files map[string]string
		// Want holds the lines of the summary, where {config} stands for
		// the directory of the files.
		want []string
		err  bool
	}{
		"blocked file and document that fails to route": {
			files: map[string]string{
				"a.yaml": drinkDoc + "  size: XL\n",
				"b.yaml": drinkDoc + stringtest.LinesLF(
					"  size: S",
					"---",
					"apiVersion: cafe.jacobcolvin.com/v9",
					"kind: Drink",
				),
			},
			want: []string{
				"{config}/a.yaml: not migrated, 1 error",
				"{config}/b.yaml: migrated document 1: Drink to cafe.jacobcolvin.com/v1",
				"{config}/b.yaml: 1 document not migrated",
				"1 file migrated, 1 document not migrated, 1 file not migrated",
			},
			err: true,
		},
		"clean plan": {
			files: map[string]string{"a.yaml": drinkDoc + "  size: S\n"},
			want: []string{
				"{config}/a.yaml: migrated Drink to cafe.jacobcolvin.com/v1",
				"1 file migrated",
			},
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			plan, config := planDir(t, reg, tc.files)

			res, err := plan.Apply(t.Context())
			if tc.err {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			want := strings.ReplaceAll(stringtest.JoinLF(tc.want...), "{config}", config)
			assert.Equal(t, want, res.Summary())
		})
	}
}

// lastLine returns the last line of s.
func lastLine(s string) string {
	lines := strings.Split(s, "\n")

	return lines[len(lines)-1]
}
