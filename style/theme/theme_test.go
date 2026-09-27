package theme_test

import (
	"image/color"
	"slices"
	"sync/atomic"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.jacobcolvin.com/niceyaml/printer"
	"go.jacobcolvin.com/niceyaml/style"
	"go.jacobcolvin.com/niceyaml/style/kind"
	"go.jacobcolvin.com/niceyaml/style/theme"
)

func TestNew_NilBuild(t *testing.T) {
	t.Parallel()

	th := theme.New("test-nil-build", theme.Dark, nil)

	assert.Equal(t, "test-nil-build", th.Name)
	assert.Equal(t, theme.Dark, th.Mode)
	assert.Equal(t, style.Styles{}, th.Styles())

	got, ok := theme.Catalog{}.With(th).Get("test-nil-build")
	require.True(t, ok)
	assert.Equal(t, style.Styles{}, got.Styles())
}

func TestCharm(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "charm", theme.Charm.Name)
	assert.Equal(t, theme.Dark, theme.Charm.Mode)
	assert.Equal(t, style.Default(), theme.Charm.Styles())

	got, ok := theme.Builtin().Get("charm")
	require.True(t, ok)
	assert.Equal(t, theme.Charm.Name, got.Name)
}

func TestCatalog_With(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		check func(t *testing.T)
	}{
		"added theme is returned by Get": {
			check: func(t *testing.T) {
				t.Helper()

				c := theme.Builtin().With(theme.New("test-custom", theme.Dark, marked(kind.Comment)))

				got, ok := c.Get("test-custom")
				require.True(t, ok)
				assert.Equal(t, "test-custom", got.Name)
				assert.Equal(t, theme.Dark, got.Mode)
				assert.True(t, isMarked(got.Styles(), kind.Comment))
			},
		},
		"added theme goes on the end": {
			check: func(t *testing.T) {
				t.Helper()

				c := theme.Builtin().With(theme.New("test-listed", theme.Light, empty))

				all := c.All()
				assert.Len(t, all, theme.Builtin().Len()+1)
				assert.Equal(t, "test-listed", all[len(all)-1].Name)
				assert.Equal(t, theme.Light, all[len(all)-1].Mode)
			},
		},
		"later theme of one name wins": {
			check: func(t *testing.T) {
				t.Helper()

				c := theme.Catalog{}.With(
					theme.New("test-replace", theme.Dark, marked(kind.Comment)),
					theme.New("test-replace", theme.Light, marked(kind.NameTag)),
				)

				got, ok := c.Get("test-replace")
				require.True(t, ok)
				assert.Equal(t, theme.Light, got.Mode)
				assert.True(t, isMarked(got.Styles(), kind.NameTag))
				assert.Equal(t, 1, c.Len())
			},
		},
		"built-in theme is replaced in place": {
			check: func(t *testing.T) {
				t.Helper()

				c := theme.Builtin().With(theme.New("vulcan", theme.Dark, marked(kind.NameTag)))

				got, ok := c.Get("vulcan")
				require.True(t, ok)
				assert.True(t, isMarked(got.Styles(), kind.NameTag))
				assert.Equal(t, theme.Builtin().Len(), c.Len())

				names := make([]string, 0, c.Len())
				for _, th := range c.All() {
					names = append(names, th.Name)
				}

				assert.Equal(t, 1, countOf(names, "vulcan"))
				assert.True(t, slices.IsSorted(names), "replacing keeps the position")
			},
		},
		"receiver is unchanged": {
			check: func(t *testing.T) {
				t.Helper()

				base := theme.Catalog{}.With(theme.New("test-base", theme.Dark, empty))
				_ = base.With(
					theme.New("test-extra", theme.Dark, empty),
					theme.New("test-base", theme.Light, empty),
				)

				assert.Equal(t, 1, base.Len())

				got, ok := base.Get("test-base")
				require.True(t, ok)
				assert.Equal(t, theme.Dark, got.Mode)

				_, ok = base.Get("test-extra")
				assert.False(t, ok)
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tt.check(t)
		})
	}
}

func TestCatalog_Get(t *testing.T) {
	t.Parallel()

	t.Run("built-in theme", func(t *testing.T) {
		t.Parallel()

		got, ok := theme.Builtin().Get("dracula")
		require.True(t, ok)
		assert.Equal(t, "dracula", got.Name)
		assert.Equal(t, theme.Dark, got.Mode)

		text := got.Styles().Style(kind.Text)
		assert.Equal(t, lipgloss.Color("#f8f8f2"), text.GetForeground())
		assert.Equal(t, lipgloss.Color("#282a36"), text.GetBackground())
	})

	t.Run("unknown theme", func(t *testing.T) {
		t.Parallel()

		_, ok := theme.Builtin().Get("no-such-theme")
		assert.False(t, ok)
	})

	t.Run("zero catalog", func(t *testing.T) {
		t.Parallel()

		var c theme.Catalog

		_, ok := c.Get("dracula")
		assert.False(t, ok)
		assert.Empty(t, c.All())
		assert.Equal(t, 0, c.Len())
	})
}

func TestCatalog_Mode(t *testing.T) {
	t.Parallel()

	c := theme.Catalog{}.With(
		theme.New("a-dark", theme.Dark, empty),
		theme.New("b-light", theme.Light, empty),
		theme.New("c-dark", theme.Dark, empty),
	)

	dark := c.Mode(theme.Dark)
	assert.Equal(t, 2, dark.Len())
	assert.Equal(t, "a-dark", dark.All()[0].Name)
	assert.Equal(t, "c-dark", dark.All()[1].Name)

	_, ok := dark.Get("b-light")
	assert.False(t, ok)

	light := c.Mode(theme.Light)
	assert.Equal(t, 1, light.Len())
	assert.Equal(t, "b-light", light.All()[0].Name)

	none := light.Mode(theme.Dark)
	assert.Equal(t, 0, none.Len())
	assert.Empty(t, none.All())

	builtinDark := theme.Builtin().Mode(theme.Dark)
	builtinLight := theme.Builtin().Mode(theme.Light)
	assert.Equal(t, theme.Builtin().Len(), builtinDark.Len()+builtinLight.Len())

	darkNames := make([]string, 0, builtinDark.Len())
	for _, th := range builtinDark.All() {
		assert.Equal(t, theme.Dark, th.Mode, th.Name)

		darkNames = append(darkNames, th.Name)
	}

	assert.True(t, slices.IsSorted(darkNames), "filtering keeps the name order")
}

func TestBuiltin(t *testing.T) {
	t.Parallel()

	all := theme.Builtin().All()

	names := make([]string, 0, len(all))
	for _, th := range all {
		names = append(names, th.Name)
	}

	assert.Equal(t, "abap", names[0])
	assert.Contains(t, names, "charm")
	assert.True(t, slices.IsSorted(names))
	assert.Equal(t, len(names), theme.Builtin().Len())

	// Every entry is complete and builds.
	for _, th := range all {
		assert.NotEmpty(t, th.Name)
		assert.NotEqual(t, style.Styles{}, th.Styles(), th.Name)
		assert.NotEqual(t, lipgloss.NoColor{}, th.Style(kind.Comment).GetForeground(), th.Name)
	}

	// The slice is a copy.
	all[0] = theme.Theme{}

	assert.Equal(t, "abap", theme.Builtin().All()[0].Name)
}

func TestThemeStyles(t *testing.T) {
	t.Parallel()

	t.Run("builds once and shares across copies", func(t *testing.T) {
		t.Parallel()

		var calls atomic.Int32

		th := theme.New("test-memo", theme.Dark, func() style.Styles {
			calls.Add(1)

			return marked(kind.Comment)()
		})

		first := th.Styles()

		duplicate := th
		second := duplicate.Styles()

		assert.Equal(t, int32(1), calls.Load())
		assert.True(t, isMarked(first, kind.Comment))
		assert.True(t, isMarked(second, kind.Comment))
	})

	t.Run("zero value", func(t *testing.T) {
		t.Parallel()

		var th theme.Theme

		assert.Equal(t, style.Styles{}, th.Styles())
		assert.Equal(t, lipgloss.NewStyle(), th.Style(kind.Text))
	})
}

func TestPalette_SubtleTextDiffersFromText(t *testing.T) {
	t.Parallel()

	// Subtle text is de-emphasized text, so every built-in theme must give
	// it a foreground of its own, and the dim variant another.
	for _, th := range theme.Builtin().All() {
		t.Run(th.Name, func(t *testing.T) {
			t.Parallel()

			styles := th.Styles()
			text := styles.Style(kind.Text).GetForeground()
			subtle := styles.Style(kind.TextSubtle).GetForeground()
			dim := styles.Style(kind.TextSubtleDim).GetForeground()

			assert.NotEqual(t, text, subtle, "TextSubtle matches Text")
			assert.NotEqual(t, subtle, dim, "TextSubtleDim matches TextSubtle")
		})
	}
}

func TestPalette_DiffAndErrorKindsStandOut(t *testing.T) {
	t.Parallel()

	// The printer paints whole diff lines and error marks in these kinds,
	// so every built-in theme must draw each one apart from plain text and
	// draw deleted lines apart from inserted ones.
	for _, th := range theme.Builtin().All() {
		t.Run(th.Name, func(t *testing.T) {
			t.Parallel()

			styles := th.Styles()
			text := style.Encode(styles.Style(kind.Text))

			for _, k := range []kind.Kind{kind.GenericError, kind.GenericDeleted, kind.GenericInserted} {
				st := styles.Style(k)

				assert.NotEqual(t, text, style.Encode(st), "%s matches Text", k)
				assert.NotEqual(t, st.GetForeground(), st.GetBackground(), "%s draws its foreground on itself", k)
			}

			assert.NotEqual(t,
				style.Encode(styles.Style(kind.GenericDeleted)),
				style.Encode(styles.Style(kind.GenericInserted)),
				"GenericDeleted matches GenericInserted",
			)
		})
	}
}

func TestPalette_TokensLayerOverAncestors(t *testing.T) {
	t.Parallel()

	// A token spec sets what it names and takes the rest from the kind
	// above it, so "bold" alone keeps the parent's foreground.
	tests := map[string]struct {
		theme string
		kind  kind.Kind
		want  string
	}{
		"attributes alone keep the parent color": {
			theme: "solarized-light",
			kind:  kind.NameTag,
			want:  "bold #268bd2 bg:#eee8d5",
		},
		"a color of its own replaces the parent's": {
			theme: "solarized-dark",
			kind:  kind.NameTag,
			want:  "#268bd2 bg:#002b36",
		},
		"a child keeps the parent's bold": {
			theme: "tokyonight-night",
			kind:  kind.PunctuationHeading,
			want:  "bold #e0af68 bg:#1a1b26",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			th, ok := theme.Builtin().Get(tc.theme)
			require.True(t, ok)
			assert.Equal(t, tc.want, style.Encode(th.Style(tc.kind)))
		})
	}
}

func TestPalette_NameKindsKeepSourceAttributes(t *testing.T) {
	t.Parallel()

	// Algol and algol-nu set their builtin names in bold italic, and the
	// keys and tags below Name must not pick up that italic, since their
	// source entries draw them upright.
	tests := map[string]struct {
		theme string
		kind  kind.Kind
		want  string
	}{
		"algol keys": {
			theme: "algol",
			kind:  kind.NameTag,
			want:  "bold underline #000000 bg:#ffffff",
		},
		"algol tags": {
			theme: "algol",
			kind:  kind.NameDecorator,
			want:  "bold #888888 bg:#ffffff",
		},
		"algol-nu keys": {
			theme: "algol-nu",
			kind:  kind.NameTag,
			want:  "bold #000000 bg:#ffffff",
		},
		"algol-nu tags": {
			theme: "algol-nu",
			kind:  kind.NameDecorator,
			want:  "bold #888888 bg:#ffffff",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			th, ok := theme.Builtin().Get(tc.theme)
			require.True(t, ok)
			assert.Equal(t, tc.want, style.Encode(th.Style(tc.kind)))
		})
	}
}

func TestPalette_LiteralsTakeSourceColors(t *testing.T) {
	t.Parallel()

	// These source themes color numbers through their Literal entry, and
	// solarized-light colors booleans through Keyword, so each literal
	// must keep that color rather than fall back to the body text.
	tests := map[string]struct {
		theme string
		kind  kind.Kind
		want  string
	}{
		"solarized-light numbers": {
			theme: "solarized-light",
			kind:  kind.LiteralNumberInteger,
			want:  "bold #2aa198 bg:#eee8d5",
		},
		"solarized-light booleans": {
			theme: "solarized-light",
			kind:  kind.LiteralBoolean,
			want:  "bold #859900 bg:#eee8d5",
		},
		"github-dark numbers": {
			theme: "github-dark",
			kind:  kind.LiteralNumberInteger,
			want:  "#a5d6ff bg:#0d1117",
		},
		"hrdark numbers": {
			theme: "hrdark",
			kind:  kind.LiteralNumberInteger,
			want:  "#a6be9d bg:#1d2432",
		},
		"modus-operandi numbers": {
			theme: "modus-operandi",
			kind:  kind.LiteralNumberInteger,
			want:  "#0000c0 bg:#ffffff",
		},
		"modus-vivendi numbers": {
			theme: "modus-vivendi",
			kind:  kind.LiteralNumberFloat,
			want:  "#00bcff bg:#000000",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			th, ok := theme.Builtin().Get(tc.theme)
			require.True(t, ok)
			assert.Equal(t, tc.want, style.Encode(th.Style(tc.kind)))
		})
	}
}

func TestPalette_DiffAndErrorKindsUpright(t *testing.T) {
	t.Parallel()

	// No source theme sets its diff or error tokens in italic, so none of
	// them may pick up italic from the Generic kind above them.
	upright := []kind.Kind{
		kind.GenericDeleted,
		kind.GenericInserted,
		kind.GenericError,
		kind.GenericErrorInvalid,
		kind.GenericErrorUnknown,
	}

	for _, th := range theme.Builtin().All() {
		t.Run(th.Name, func(t *testing.T) {
			t.Parallel()

			styles := th.Styles()
			for _, k := range upright {
				assert.False(t, styles.Style(k).GetItalic(), "%s renders italic", k)
			}
		})
	}

	// Fruity sets its headings in bold and swapoff its strong text, and
	// neither may pass that bold on to its diff lines.
	for _, name := range []string{"fruity", "swapoff"} {
		t.Run(name+" diff lines", func(t *testing.T) {
			t.Parallel()

			th, ok := theme.Builtin().Get(name)
			require.True(t, ok)

			for _, k := range []kind.Kind{kind.GenericDeleted, kind.GenericInserted} {
				assert.False(t, th.Style(k).GetBold(), "%s renders bold", k)
			}
		})
	}
}

func TestPalette_TokyonightStormSurface(t *testing.T) {
	t.Parallel()

	// Storm and Night share every syntax color and part ways on their
	// surfaces, so Storm renders on #24283b with #1f2335 behind the diff
	// marks.
	night, ok := theme.Builtin().Get("tokyonight-night")
	require.True(t, ok)

	storm, ok := theme.Builtin().Get("tokyonight-storm")
	require.True(t, ok)

	assert.NotEqual(t,
		night.Style(kind.Text).GetBackground(),
		storm.Style(kind.Text).GetBackground(),
		"Storm renders on Night's background",
	)

	tests := map[string]struct {
		got  color.Color
		want color.Color
	}{
		"text background": {
			got:  storm.Style(kind.Text).GetBackground(),
			want: lipgloss.Color("#24283b"),
		},
		"deleted background": {
			got:  storm.Style(kind.GenericDeleted).GetBackground(),
			want: lipgloss.Color("#1f2335"),
		},
		"inserted background": {
			got:  storm.Style(kind.GenericInserted).GetBackground(),
			want: lipgloss.Color("#1f2335"),
		},
		"OK heading foreground": {
			got:  storm.Style(kind.GenericHeadingOK).GetForeground(),
			want: lipgloss.Color("#24283b"),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.got)
		})
	}
}

func TestPalette_HeadingsCarryForeground(t *testing.T) {
	t.Parallel()

	// A heading paints a background, so every built-in theme must pair it
	// with a foreground of its own rather than leaving the terminal default
	// to land on the painted surface.
	headings := []kind.Kind{
		kind.GenericHeading,
		kind.GenericHeadingAccent,
		kind.GenericHeadingSubtle,
		kind.GenericHeadingOK,
		kind.GenericHeadingWarn,
		kind.GenericHeadingError,
	}

	for _, th := range theme.Builtin().All() {
		t.Run(th.Name, func(t *testing.T) {
			t.Parallel()

			styles := th.Styles()
			for _, k := range headings {
				st := styles.Style(k)
				assert.NotEqual(t, lipgloss.NoColor{}, st.GetBackground(), "%s has no background", k)
				assert.NotEqual(t, lipgloss.NoColor{}, st.GetForeground(), "%s has no foreground", k)
			}
		})
	}
}

func TestBuiltin_ChromeIgnoresCommentOverride(t *testing.T) {
	t.Parallel()

	// Every built-in theme sets the chrome itself, so a program that
	// restyles comments keeps the line numbers each theme draws.
	comment := lipgloss.NewStyle().Foreground(lipgloss.Color("#123456")).Italic(true)

	for _, th := range theme.Builtin().All() {
		t.Run(th.Name, func(t *testing.T) {
			t.Parallel()

			got := th.Styles().With(style.Set(kind.Comment, comment))

			assert.Equal(t, th.Styles().Style(kind.UILineNumber), got.Style(kind.UILineNumber))
			assert.False(t, got.Style(kind.UILineNumber).GetItalic())
		})
	}
}

// countOf returns how many times name appears in names.
func countOf(names []string, name string) int {
	n := 0

	for _, s := range names {
		if s == name {
			n++
		}
	}

	return n
}

// marker is the foreground that marked gives one kind, so a test can tell
// which dummy theme a lookup returned.
var marker = lipgloss.Color("#123456")

// empty builds a theme with no kinds set.
func empty() style.Styles {
	return style.New(lipgloss.NewStyle())
}

// marked returns a builder for a theme whose only set kind is s.
func marked(s kind.Kind) func() style.Styles {
	return func() style.Styles {
		return style.New(lipgloss.NewStyle(), style.Set(s, lipgloss.NewStyle().Foreground(marker)))
	}
}

// isMarked reports whether s carries the marker in styles.
func isMarked(styles style.Styles, s kind.Kind) bool {
	return styles.Style(s).GetForeground() == marker
}

func TestTheme_Style(t *testing.T) {
	t.Parallel()

	var _ style.Styler = theme.Theme{}

	assert.Equal(t, theme.Charm.Styles().Style(kind.Comment), theme.Charm.Style(kind.Comment))
	assert.Equal(t, lipgloss.NewStyle(), theme.Theme{}.Style(kind.Comment))

	p := printer.New(printer.WithStyles(theme.Charm))
	assert.Equal(t, theme.Charm.Styles().Style(kind.NameTag), p.Style(kind.NameTag))
}
