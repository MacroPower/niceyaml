package fangs_test

import (
	"image/color"
	"testing"

	"charm.land/fang/v2"
	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/fangs"
	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/kind"
	"go.jacobcolvin.com/niceyaml/style/theme"
)

func TestColorScheme(t *testing.T) {
	t.Parallel()

	styles := theme.Charm.Styles()
	text := styles.Style(kind.Text)
	comment := styles.Style(kind.Comment)
	genericError := styles.Style(kind.GenericError)

	cs := fangs.ColorScheme(styles)

	tcs := map[string]struct {
		got  color.Color
		want color.Color
	}{
		"base": {
			got:  cs.Base,
			want: text.GetForeground(),
		},
		"title": {
			got:  cs.Title,
			want: styles.Style(kind.NameTag).GetForeground(),
		},
		"description": {
			got:  cs.Description,
			want: text.GetForeground(),
		},
		"codeblock": {
			got:  cs.Codeblock,
			want: text.GetBackground(),
		},
		"program": {
			got:  cs.Program,
			want: styles.Style(kind.NameTag).GetForeground(),
		},
		"command": {
			got:  cs.Command,
			want: styles.Style(kind.NameAnchor).GetForeground(),
		},
		"dimmed argument": {
			got:  cs.DimmedArgument,
			want: comment.GetForeground(),
		},
		"comment": {
			got:  cs.Comment,
			want: comment.GetForeground(),
		},
		"flag": {
			got:  cs.Flag,
			want: styles.Style(kind.LiteralNumber).GetForeground(),
		},
		"flag default": {
			got:  cs.FlagDefault,
			want: comment.GetForeground(),
		},
		"quoted string": {
			got:  cs.QuotedString,
			want: styles.Style(kind.LiteralString).GetForeground(),
		},
		"argument": {
			got:  cs.Argument,
			want: text.GetForeground(),
		},
		"dash": {
			got:  cs.Dash,
			want: styles.Style(kind.Punctuation).GetForeground(),
		},
		"error header foreground": {
			got:  cs.ErrorHeader[0],
			want: genericError.GetForeground(),
		},
		"error header background": {
			got:  cs.ErrorHeader[1],
			want: genericError.GetBackground(),
		},
		// ColorScheme maps no kind to Help or ErrorDetails.
		"help": {
			got:  cs.Help,
			want: nil,
		},
		"error details": {
			got:  cs.ErrorDetails,
			want: nil,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// A kind without a color resolves to NoColor on both sides, which
			// would let a wrong mapping pass, so the test requires every
			// expected color to have a value.
			assert.NotEqual(t, lipgloss.NoColor{}, tc.want)
			assert.Equal(t, tc.want, tc.got)
		})
	}
}

func TestColorSchemeFunc(t *testing.T) {
	t.Parallel()

	styles := theme.Charm.Styles()

	csFunc := fangs.ColorSchemeFunc(styles)

	// The returned function ignores the LightDarkFunc parameter.
	assert.Equal(t, fangs.ColorScheme(styles), csFunc(nil))
}

// stylerFunc adapts a func to [style.Styler].
type stylerFunc func(kind.Kind) lipgloss.Style

func (f stylerFunc) Style(k kind.Kind) lipgloss.Style {
	return f(k)
}

func TestColorScheme_NilStyler(t *testing.T) {
	t.Parallel()

	want := fangs.ColorScheme(style.Default())

	// A nil pointer to a type with a value Style method still satisfies
	// style.Styler, and calling Style through it panics. So does a nil
	// func with a Style method.
	nils := map[string]style.Styler{
		"nil":               nil,
		"nil *style.Styles": (*style.Styles)(nil),
		"nil *theme.Theme":  (*theme.Theme)(nil),
		"nil func":          stylerFunc(nil),
	}

	tcs := map[string]struct {
		scheme func(styles style.Styler) fang.ColorScheme
	}{
		"ColorScheme": {
			scheme: fangs.ColorScheme,
		},
		"ColorSchemeFunc": {
			scheme: func(styles style.Styler) fang.ColorScheme {
				return fangs.ColorSchemeFunc(styles)(nil)
			},
		},
		"LightDarkColorSchemeFunc on a light terminal": {
			scheme: func(styles style.Styler) fang.ColorScheme {
				return fangs.LightDarkColorSchemeFunc(styles, styles)(lipgloss.LightDark(false))
			},
		},
		"LightDarkColorSchemeFunc on a dark terminal": {
			scheme: func(styles style.Styler) fang.ColorScheme {
				return fangs.LightDarkColorSchemeFunc(styles, styles)(lipgloss.LightDark(true))
			},
		},
	}

	for name, tc := range tcs {
		for nilName, styles := range nils {
			t.Run(name+" with "+nilName, func(t *testing.T) {
				t.Parallel()

				var got fang.ColorScheme

				require.NotPanics(t, func() { got = tc.scheme(styles) })
				assert.Equal(t, want, got)
			})
		}
	}
}

func TestLightDarkColorSchemeFunc(t *testing.T) {
	t.Parallel()

	light, ok := theme.Builtin().Get("github")
	require.True(t, ok)

	dark := theme.Charm

	// Equal schemes would let a function that ignores the background pass.
	require.NotEqual(t, fangs.ColorScheme(light), fangs.ColorScheme(dark))

	csFunc := fangs.LightDarkColorSchemeFunc(light, dark)

	tcs := map[string]struct {
		isDark bool
		want   fang.ColorScheme
	}{
		"light terminal": {
			isDark: false,
			want:   fangs.ColorScheme(light),
		},
		"dark terminal": {
			isDark: true,
			want:   fangs.ColorScheme(dark),
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, csFunc(lipgloss.LightDark(tc.isDark)))
		})
	}
}
