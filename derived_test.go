package niceyaml_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.jacobcolvin.com/x/stringtest"

	"go.jacobcolvin.com/niceyaml"
	"go.jacobcolvin.com/niceyaml/internal/yamltest"
	"go.jacobcolvin.com/niceyaml/paths"
	"go.jacobcolvin.com/niceyaml/position"
)

// expandVars stands for a preprocessor. It replaces "${PORT}" in text
// with value. Its locate function moves a range on the line of the
// variable back over the variable.
func expandVars(text, value string) (string, func(niceyaml.Located) niceyaml.Place) {
	const variable = "${PORT}"

	before, _, _ := strings.Cut(text, variable)
	line := strings.Count(before, "\n")
	col := len(before) - (strings.LastIndex(before, "\n") + 1)

	locate := func(at niceyaml.Located) niceyaml.Place {
		place := niceyaml.Place{Path: at.Path, HasPath: at.HasPath, Range: at.Range, HasRange: at.HasRange}
		if at.HasRange && at.Range.Start.Line == line && at.Range.Start.Col >= col {
			place.Range = position.NewRange(position.New(line, col), position.New(line, col+len(variable)))
		}

		return place
	}

	return strings.ReplaceAll(text, variable, value), locate
}

func TestNewSourceFromDerived(t *testing.T) {
	t.Parallel()

	type config struct {
		Name string `yaml:"name"`
		Port int    `yaml:"port"`
	}

	t.Run("binds in home at the mapped place", func(t *testing.T) {
		t.Parallel()

		text := stringtest.LinesLF("name: app", "port: ${PORT}")
		home := niceyaml.NewSourceFromString(text, niceyaml.WithName("app.yaml"))
		doc := yamltest.Document(t, home)

		expanded, locate := expandVars(text, "eighty")
		work := niceyaml.NewSourceFromDerived(expanded, doc, locate)

		_, err := work.Decode[config](t.Context())
		require.Error(t, err)

		yamltest.AssertBoundTo(t, home, err)

		bound := yamltest.RequireSourceError(t, err)
		assert.Equal(t, position.NewRange(position.New(1, 6), position.New(1, 13)), yamltest.RangeOf(t, bound))
		assert.Equal(t, "$.port", yamltest.PathOf(t, bound).String())
		assert.Equal(t, "app.yaml:2:7: ", err.Error()[:len("app.yaml:2:7: ")])

		got := niceyaml.FormatError(err, niceyaml.WithContextLines(0))
		assert.Contains(t, got, "port: ${PORT}")
		assert.NotContains(t, got, "eighty")
	})

	t.Run("binds through a home that is derived too", func(t *testing.T) {
		t.Parallel()

		text := stringtest.LinesLF("name: app", "port: ${PORT}")
		home := niceyaml.NewSourceFromString(text, niceyaml.WithName("app.yaml"))
		doc := yamltest.Document(t, home)

		// The middle text adds two lines on top, so its locate moves each
		// range two lines up and over the variable.
		expanded, locate := expandVars(text, "eighty")
		shifted := "# one\n# two\n" + expanded

		middle := niceyaml.NewSourceFromDerived(shifted, doc, func(at niceyaml.Located) niceyaml.Place {
			if at.HasRange {
				at.Range = position.NewRange(
					position.New(at.Range.Start.Line-2, at.Range.Start.Col),
					position.New(at.Range.End.Line-2, at.Range.End.Col),
				)
			}

			return locate(at)
		})

		work := niceyaml.NewSourceFromDerived(shifted, yamltest.Document(t, middle), nil)

		_, err := work.Decode[config](t.Context())
		require.Error(t, err)

		yamltest.AssertBoundTo(t, home, err)

		bound := yamltest.RequireSourceError(t, err)
		assert.Equal(t, position.NewRange(position.New(1, 6), position.New(1, 13)), yamltest.RangeOf(t, bound))
		assert.Equal(t, "$.port", yamltest.PathOf(t, bound).String())
		assert.Equal(t, "app.yaml:2:7: ", err.Error()[:len("app.yaml:2:7: ")])
	})

	t.Run("nil locate keeps the location", func(t *testing.T) {
		t.Parallel()

		text := stringtest.LinesLF("name: app", "port: ${PORT}")
		home := niceyaml.NewSourceFromString(text, niceyaml.WithName("app.yaml"))
		doc := yamltest.Document(t, home)

		work := niceyaml.NewSourceFromDerived(strings.ReplaceAll(text, "${PORT}", "eighty!"), doc, nil)

		_, err := work.Decode[config](t.Context())
		require.Error(t, err)

		yamltest.AssertBoundTo(t, home, err)

		bound := yamltest.RequireSourceError(t, err)
		assert.Equal(t, position.NewRange(position.New(1, 6), position.New(1, 13)), yamltest.RangeOf(t, bound))
	})

	t.Run("locate sees the path and the excerpt range", func(t *testing.T) {
		t.Parallel()

		text := stringtest.LinesLF("name: app", "port: ${PORT}")
		home := niceyaml.NewSourceFromString(text)
		doc := yamltest.Document(t, home)

		var got []niceyaml.Located

		work := niceyaml.NewSourceFromDerived(
			strings.ReplaceAll(text, "${PORT}", "x"),
			doc,
			func(at niceyaml.Located) niceyaml.Place {
				got = append(got, at)

				return niceyaml.Place{}
			},
		)

		_, err := work.Decode[config](t.Context())
		require.Error(t, err)
		require.Len(t, got, 1)

		assert.True(t, got[0].HasPath)
		assert.Equal(t, "$.port", got[0].Path.String())
		assert.True(t, got[0].HasRange)
		assert.Equal(t, position.NewRange(position.New(1, 6), position.New(1, 7)), got[0].Range)

		// An empty place binds the error in home without a position.
		yamltest.AssertBoundTo(t, home, err)

		bound := yamltest.RequireSourceError(t, err)
		_, ok := bound.Position()
		assert.False(t, ok)
	})

	t.Run("an error without a location has no range", func(t *testing.T) {
		t.Parallel()

		home := niceyaml.NewSourceFromString("a: 1\n")
		doc := yamltest.Document(t, home)

		var got []niceyaml.Located

		work := niceyaml.NewSourceFromDerived("a: 1\n", doc, func(at niceyaml.Located) niceyaml.Place {
			got = append(got, at)

			return niceyaml.Place{Path: at.Path, HasPath: at.HasPath}
		})

		// A plain error does not resolve a location, so it binds in home
		// without one, and locate does not see it.
		err := work.Bind(errors.New("whole document"))

		assert.Empty(t, got)
		yamltest.AssertBoundTo(t, home, err)
		assert.Equal(t, "whole document", err.Error())

		// An error at a range outside the derived text resolves without a
		// range.
		err = work.Bind(
			niceyaml.NewError("far", niceyaml.AtRange(position.NewRange(position.New(9, 0), position.New(9, 1)))),
		)

		require.Len(t, got, 1)
		assert.False(t, got[0].HasRange)
		assert.False(t, got[0].HasPath)

		yamltest.AssertBoundTo(t, home, err)
		assert.Equal(t, "far", err.Error())
	})

	t.Run("the place replaces the path and adds details", func(t *testing.T) {
		t.Parallel()

		home := niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName("h.yaml"))
		doc := yamltest.Document(t, home)

		work := niceyaml.NewSourceFromDerived("a: x\n", doc, func(at niceyaml.Located) niceyaml.Place {
			return niceyaml.Place{
				Path: paths.Doc().Child("b"), HasPath: true,
				Range: at.Range, HasRange: at.HasRange,
				Details: []error{niceyaml.NewError("see the value", niceyaml.AtPath(paths.Doc().Child("a")))},
			}
		})

		_, err := work.Decode[struct {
			A int `yaml:"a"`
		}](t.Context())
		require.Error(t, err)

		yamltest.AssertBoundTo(t, home, err)
		assert.Equal(t, "$.b", yamltest.PathOf(t, yamltest.RequireSourceError(t, err)).String())

		details := yamltest.DetailsOf(err)
		require.Len(t, details, 1)
		assert.Equal(t, "$.a", yamltest.PathOf(t, details[0]).String())
		assert.Equal(t, "see the value", details[0].Message())
		assert.Equal(t, position.New(0, 3), yamltest.PositionOf(t, details[0]))
		assert.Same(t, home, details[0].Source())
	})

	t.Run("a chain binds each error and each detail in the first home", func(t *testing.T) {
		t.Parallel()

		home := niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName("h.yaml"))

		// The middle text adds a line on top, so its locate moves each
		// range up a line. The work text keeps every line of the middle
		// text. Each locate adds a detail at a range of its own home. A
		// detail thus binds at the right line only through the homes in
		// order.
		text := "# top\na: x\n"

		up := func(at niceyaml.Located) niceyaml.Place {
			place := niceyaml.Place{
				Path: at.Path, HasPath: at.HasPath,
				Details: []error{niceyaml.NewError("from middle", niceyaml.AtRange(
					position.NewRange(position.New(0, 3), position.New(0, 4)),
				))},
			}

			if at.HasRange {
				place.Range, place.HasRange = position.NewRange(
					position.New(at.Range.Start.Line-1, at.Range.Start.Col),
					position.New(at.Range.End.Line-1, at.Range.End.Col),
				), true
			}

			return place
		}

		keep := func(at niceyaml.Located) niceyaml.Place {
			return niceyaml.Place{
				Path: at.Path, HasPath: at.HasPath,
				Range: at.Range, HasRange: at.HasRange,
				Details: []error{niceyaml.NewError("from work", niceyaml.AtRange(
					position.NewRange(position.New(1, 3), position.New(1, 4)),
				))},
			}
		}

		middle := niceyaml.NewSourceFromDerived(text, yamltest.Document(t, home), up)
		work := niceyaml.NewSourceFromDerived(text, yamltest.Document(t, middle), keep)

		// A plain error does not resolve a location, and it binds in the
		// first home all the same.
		yamltest.AssertBoundTo(t, home, work.Bind(errors.New("whole document")))

		_, err := work.Decode[struct {
			A int `yaml:"a"`
		}](t.Context())
		require.Error(t, err)

		yamltest.AssertBoundTo(t, home, err)
		assert.Equal(t, position.New(0, 3), yamltest.PositionOf(t, yamltest.RequireSourceError(t, err)))

		details := yamltest.DetailsOf(err)

		var msgs []string

		for _, d := range details {
			msgs = append(msgs, d.Message())

			assert.Same(t, home, d.Source(), "detail %q", d.Message())
			assert.Equal(t, position.New(0, 3), yamltest.PositionOf(t, d), "detail %q", d.Message())
		}

		assert.Equal(t, []string{"from work", "from middle"}, msgs)
	})

	t.Run("keeps the settings of home", func(t *testing.T) {
		t.Parallel()

		home := niceyaml.NewSourceFromString("a: *x\n",
			niceyaml.WithName("h.yaml"), niceyaml.WithFilePath("/h.yaml"),
			yamltest.References("x: &x 1\n"), niceyaml.WithAllowDuplicateKeys(true),
			niceyaml.WithByteLimit(100), niceyaml.WithDocumentByteLimit(50), niceyaml.WithDepthLimit(3))
		doc := yamltest.Document(t, home)

		work := niceyaml.NewSourceFromDerived("b: *x\n", doc, nil)

		assert.Equal(t, "h.yaml", work.Name())
		assert.Equal(t, "/h.yaml", work.FilePath())
		assert.Equal(t, home.References(), work.References())
		assert.True(t, work.AllowsDuplicateKeys())
		assert.Equal(t, 100, work.ByteLimit())
		assert.Equal(t, 50, work.DocumentByteLimit())
		assert.Equal(t, 3, work.DepthLimit())

		got, err := work.Decode[map[string]int](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]int{"b": 1}, got)
	})

	t.Run("applies opts over the settings of home", func(t *testing.T) {
		t.Parallel()

		home := niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName("h.yaml"))
		doc := yamltest.Document(t, home)

		work := niceyaml.NewSourceFromDerived(
			"a: 1\n",
			doc,
			nil,
			niceyaml.WithName("derived"),
			niceyaml.WithByteLimit(7),
		)

		assert.Equal(t, "derived", work.Name())
		assert.Equal(t, 7, work.ByteLimit())
	})

	t.Run("keeps the limits of home", func(t *testing.T) {
		t.Parallel()

		home := niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithName("h.yaml"), niceyaml.WithByteLimit(8))
		doc := yamltest.Document(t, home)

		work := niceyaml.NewSourceFromDerived("a: 123456\nb: 2\n", doc, nil)

		_, err := work.Document()
		require.ErrorIs(t, err, niceyaml.ErrLimit)
		yamltest.AssertBoundTo(t, home, err)
		assert.Equal(t, "h.yaml: file is larger than 8 bytes", err.Error())

		// The text past the limit keeps its tokens and its lines.
		assert.Equal(t, 2, work.Lines().Len())
		assert.Equal(t, "a: 123456\nb: 2", work.Lines().Content())
	})

	t.Run("WithoutLimits turns the limits of home off", func(t *testing.T) {
		t.Parallel()

		home := niceyaml.NewSourceFromString("a: 1\n", niceyaml.WithByteLimit(8), niceyaml.WithDepthLimit(1))
		doc := yamltest.Document(t, home)

		work := niceyaml.NewSourceFromDerived("a: {b: {c: 123456}}\n", doc, nil, niceyaml.WithoutLimits())

		assert.Equal(t, niceyaml.NoLimit, work.ByteLimit())
		assert.Equal(t, niceyaml.NoLimit, work.DepthLimit())

		got, err := work.Decode[map[string]any](t.Context())
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"a": map[string]any{"b": map[string]any{"c": uint64(123456)}}}, got)
	})

	t.Run("a home that is derived too keeps the alias bounds", func(t *testing.T) {
		t.Parallel()

		// The two aliases repeat 1200 bytes of text, past the 1 KiB that the
		// document byte limit of the first home allows.
		text := "a: &a " + strings.Repeat("x", 600) + "\nb: *a\nc: *a\n"
		home := niceyaml.NewSourceFromString(text, niceyaml.WithDocumentByteLimit(1024))
		middle := niceyaml.NewSourceFromDerived(text, yamltest.Document(t, home), nil, niceyaml.WithoutLimits())

		work := niceyaml.NewSourceFromDerived(text, yamltest.Document(t, middle), nil)

		_, err := work.Decode[map[string]rawText](t.Context())
		require.ErrorIs(t, err, niceyaml.ErrLimit)
		yamltest.AssertBoundTo(t, home, err)
	})

	t.Run("the unmarshalers of opts apply over those of home", func(t *testing.T) {
		t.Parallel()

		type port int

		ofHome := niceyaml.CustomUnmarshaler(func(_ context.Context, v *port, _ func(any) error) error {
			*v = 1

			return nil
		})
		ofOpts := niceyaml.CustomUnmarshaler(func(_ context.Context, v *port, _ func(any) error) error {
			*v = 2

			return nil
		})

		home := niceyaml.NewSourceFromString("p: 0\n", niceyaml.WithTypeRules(ofHome))
		doc := yamltest.Document(t, home)

		tcs := map[string]struct {
			opts []niceyaml.SourceOption
			want port
		}{
			"no option keeps the function of home": {
				want: 1,
			},
			"a function of opts replaces the one of home": {
				opts: []niceyaml.SourceOption{niceyaml.WithTypeRules(ofOpts)},
				want: 2,
			},
			"a nil function of opts removes the one of home": {
				opts: []niceyaml.SourceOption{niceyaml.WithTypeRules(niceyaml.CustomUnmarshaler[port](nil))},
				want: 0,
			},
			"defaults under the unmarshalers of home keep the function of home": {
				opts: []niceyaml.SourceOption{
					niceyaml.WithTypeRules(slices.Concat([]niceyaml.TypeRule{ofOpts}, home.TypeRules())...),
				},
				want: 1,
			},
		}

		for name, tc := range tcs {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				work := niceyaml.NewSourceFromDerived("p: 0\n", doc, nil, tc.opts...)

				got, err := work.Decode[map[string]port](t.Context())
				require.NoError(t, err)
				assert.Equal(t, map[string]port{"p": tc.want}, got)

				// WithText applies the same options the same way.
				got, err = home.WithText("p: 0\n", tc.opts...).Decode[map[string]port](t.Context())
				require.NoError(t, err)
				assert.Equal(t, map[string]port{"p": tc.want}, got)
			})
		}
	})

	t.Run("a nil home binds in the Source itself", func(t *testing.T) {
		t.Parallel()

		work := niceyaml.NewSourceFromDerived("a: x\n", nil, nil, niceyaml.WithName("own.yaml"))

		_, err := work.Decode[struct {
			A int `yaml:"a"`
		}](t.Context())
		require.Error(t, err)

		yamltest.AssertBoundTo(t, work, err)
		assert.Equal(t, "own.yaml:1:4: ", err.Error()[:len("own.yaml:1:4: ")])
	})
}
